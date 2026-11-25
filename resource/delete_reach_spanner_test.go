package resource

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
)

// The companies of the reach fixture, which are its tenants: Acme and Borealis have
// names, cobalt's name is NULL, and dune has no row.
var reachCompanies = &DeleteTenants{Table: "CascadeCompanies", Key: "Id", Name: "Name", Word: "company"}

// acmeCaller is a person whose request was checked in Acme, whose grants deny
// everything.
func acmeCaller(t *testing.T) *Caller {
	t.Helper()

	return &Caller{Permissions: tablePermissions{}, Scope: accesstypes.DomainScope("acme"), Env: RequestEnvironment(), collection: callerCollection(t)}
}

// byAccount is the one step from a row up to the account it names.
var byAccount = DeletePath{{Columns: []string{"AccountId"}, Table: "CascadeAccounts", References: []string{"Id"}}}

// assignmentKind, noteKind and filterKind are the kinds an account's delete takes: the
// assignments, a tenant's by their own column; the notes, a tenant's through their
// board; the filters, which are no tenant's. Each is marked as mark says.
func assignmentKind(mark func(*DeleteKind)) DeleteKind {
	kind := DeleteKind{
		Resource: "CascadeAssignments", Words: DeleteWords{One: "assignment", Many: "assignments"}, Key: []string{"Id"},
		Paths: []DeletePath{byAccount}, Tenant: &DomainBindingData{Column: "TenantId"},
	}
	mark(&kind)

	return kind
}

func noteKind(mark func(*DeleteKind)) DeleteKind {
	kind := DeleteKind{
		Resource: "CascadeNotes", Words: DeleteWords{One: "note", Many: "notes"}, Key: []string{"Id"},
		Paths:  []DeletePath{byAccount},
		Tenant: &DomainBindingData{Column: "BoardId", Path: []BindingHop{{Table: "CascadeBoards", JoinColumn: "Id", Column: "TenantId"}}},
	}
	mark(&kind)

	return kind
}

func filterKind(mark func(*DeleteKind)) DeleteKind {
	kind := DeleteKind{Resource: "CascadeFilters", Words: DeleteWords{One: "filter", Many: "filters"}, Key: []string{"Id"}, Paths: []DeletePath{byAccount}}
	mark(&kind)

	return kind
}

// withinReach and unmarked are the marks a test kind is given.
func withinReach(kind *DeleteKind) { kind.WithinReach = true }

func unmarked(*DeleteKind) {}

// accountTree is the description of a CascadeAccounts record of the library's whose
// delete takes the kinds, in delete order, naming the tenants as given.
func accountTree(tenants *DeleteTenants, taken ...DeleteKind) *DeleteTree {
	return &DeleteTree{Record: "CascadeAccounts", Words: DeleteWords{One: "account", Many: "accounts"}, Key: []string{"Id"}, Taken: taken, Tenants: tenants}
}

// reachDatabase is the cascade fixture with the companies, the boards and the account
// rows the tests of the reach read: each row is an assignment, a note or a filter of an
// account, by its id.
func reachDatabase(t *testing.T, name string, rows ...*spanner.Mutation) *spanner.Client {
	t.Helper()

	db := cascadeDatabase(t, name)
	fixed := []*spanner.Mutation{
		spanner.InsertMap("CascadeCompanies", map[string]any{"Id": "acme", "Name": "Acme"}),
		spanner.InsertMap("CascadeCompanies", map[string]any{"Id": "borealis", "Name": "Borealis"}),
		spanner.InsertMap("CascadeCompanies", map[string]any{"Id": "cobalt"}),
		spanner.InsertMap("CascadeBoards", map[string]any{"Id": "acme-board", "TenantId": "acme"}),
		spanner.InsertMap("CascadeBoards", map[string]any{"Id": "borealis-board", "TenantId": "borealis"}),
	}
	if _, err := db.Apply(t.Context(), append(fixed, rows...)); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	return db
}

// assignment, note and filter are rows of the account's: an assignment in the tenant
// ("" for none), a note on the board ("" for none), a filter.
func assignment(id, account, tenant string) *spanner.Mutation {
	row := map[string]any{"Id": id, "AccountId": account}
	if tenant != "" {
		row["TenantId"] = tenant
	}

	return spanner.InsertMap("CascadeAssignments", row)
}

func note(id, account, board string) *spanner.Mutation {
	row := map[string]any{"Id": id, "AccountId": account}
	if board != "" {
		row["BoardId"] = board
	}

	return spanner.InsertMap("CascadeNotes", row)
}

func filter(id, account string) *spanner.Mutation {
	return spanner.InsertMap("CascadeFilters", map[string]any{"Id": id, "AccountId": account})
}

// reachAssignment is a CascadeAssignments row whose delete asks a rule, which refuses an
// assignment whose id starts with "ruled", and, armed, the caller's grant and a question,
// which refuses one whose id starts with "asked", as armCallerTask arms a task.
type reachAssignment struct {
	ID        string             `spanner:"Id"`
	AccountID spanner.NullString `spanner:"AccountId"`
	TaskID    spanner.NullString `spanner:"TaskId"`
	TenantID  spanner.NullString `spanner:"TenantId"`
}

func (reachAssignment) Resource() accesstypes.Resource {
	return "CascadeAssignments"
}

// armReachAssignment arms an assignment's delete as the generator arms a kind a record
// takes.
func armReachAssignment(patchSet *PatchSet[reachAssignment], caller *Caller) {
	patchSet.RegisterValidateDeleteFunc(func(context.Context, ReadWriteTransaction) error {
		if strings.HasPrefix(patchSet.PrimaryKey().RowID(), "ruled") {
			return httpio.NewBadRequestMessage("This assignment is ruled out of a delete.")
		}

		return nil
	})
	if caller.IsZero() {
		return
	}
	patchSet.Enforce(caller, newDeleteSet[reachAssignment](), accesstypes.Delete)
	patchSet.RegisterDeleteCheckFunc(func(context.Context, ReadWriteTransaction) error {
		if strings.HasPrefix(patchSet.PrimaryKey().RowID(), "asked") {
			return httpio.NewConflictMessage("This assignment's person may not lose it.")
		}

		return nil
	})
}

// reachAccount is a CascadeAccounts record of the library's whose delete takes its
// assignments, marked WithinReach, naming the companies.
type reachAccount struct {
	ID string `spanner:"Id"`
}

func (reachAccount) Resource() accesstypes.Resource {
	return "CascadeAccounts"
}

func (reachAccount) DeleteTree() *DeleteTree {
	kind := assignmentKind(withinReach)
	kind.Rows = NewTakenRows(armReachAssignment)

	return accountTree(reachCompanies, kind)
}

// callerAccount is reachAccount with its assignments marked AsCaller in place of
// WithinReach.
type callerAccount struct {
	ID string `spanner:"Id"`
}

func (callerAccount) Resource() accesstypes.Resource {
	return "CascadeAccounts"
}

func (callerAccount) DeleteTree() *DeleteTree {
	kind := assignmentKind(func(kind *DeleteKind) { kind.AsCaller = true })
	kind.Rows = NewTakenRows(armReachAssignment)

	return accountTree(reachCompanies, kind)
}

// TestPatchSet_deleteTaken_withinReach holds, over the emulator, an account of the
// library's deleted by a patch that is not armed, in a context that carries a caller
// checked in Acme whose grants deny everything, with its assignments marked
// WithinReach: every assignment of Acme's, asked, is counted in the question though the
// caller may list none; confirmed, the rows and the account go, with no grant asked. A
// row the kind's rule refuses refuses in the rule's sentence, with nothing gone; a row
// the kind's question would refuse goes, the question not asked. The same description
// with the kind marked AsCaller is refused, 403: the mark is what changes it. A caller
// checked for the whole application, and no caller, take the rows of every tenant and of
// none.
func TestPatchSet_deleteTaken_withinReach(t *testing.T) {
	t.Parallel()

	db := reachDatabase(t, "delete-within-reach",
		spanner.InsertMap("CascadeAccounts", map[string]any{"Id": "dana"}),
		assignment("dana-1", "dana", "acme"), assignment("dana-2", "dana", "acme"),
		spanner.InsertMap("CascadeAccounts", map[string]any{"Id": "ruled"}),
		assignment("ruled-1", "ruled", "acme"),
		spanner.InsertMap("CascadeAccounts", map[string]any{"Id": "asked"}),
		assignment("asked-1", "asked", "acme"),
		spanner.InsertMap("CascadeAccounts", map[string]any{"Id": "marked"}),
		assignment("marked-1", "marked", "acme"),
		spanner.InsertMap("CascadeAccounts", map[string]any{"Id": "staff"}),
		assignment("staff-1", "staff", "acme"), assignment("staff-2", "staff", "borealis"), assignment("staff-3", "staff", ""),
		spanner.InsertMap("CascadeAccounts", map[string]any{"Id": "app"}),
		assignment("app-1", "app", "borealis"), assignment("app-2", "app", ""),
	)
	acme := acmeCaller(t)
	standing := func(id string) bool {
		return rowsStanding(t, db, "CascadeAccounts", id) == 1
	}

	err := deleteUnarmed[reachAccount](t, db, acme, "dana", false)
	if want := "Delete this account and everything under it: 2 assignments. This can't be undone."; !httpio.HasPreconditionRequired(err) || !strings.Contains(err.Error(), want) {
		t.Errorf("the asking delete = %v, want the question %q", err, want)
	}
	if err := deleteUnarmed[reachAccount](t, db, acme, "dana", true); err != nil {
		t.Fatalf("the confirmed delete = %v", err)
	}
	if standing("dana") || rowsStanding(t, db, "CascadeAssignments", "dana-1", "dana-2") != 0 {
		t.Error("the confirmed delete left rows, want the account and its assignments gone")
	}

	err = deleteUnarmed[reachAccount](t, db, acme, "ruled", true)
	if want := "This assignment is ruled out of a delete."; !httpio.HasBadRequest(err) || !strings.Contains(err.Error(), want) {
		t.Errorf("the delete over a row the rule refuses = %v, want the rule's %q", err, want)
	}
	if !standing("ruled") || rowsStanding(t, db, "CascadeAssignments", "ruled-1") != 1 {
		t.Error("the delete the rule refused deleted rows, want none")
	}

	if err := deleteUnarmed[reachAccount](t, db, acme, "asked", true); err != nil {
		t.Fatalf("the delete over a row the kind's question would refuse = %v, want it to go", err)
	}
	if standing("asked") || rowsStanding(t, db, "CascadeAssignments", "asked-1") != 0 {
		t.Error("the delete over a row the kind's question would refuse left rows, want them gone")
	}

	if err := deleteUnarmed[callerAccount](t, db, acme, "marked", false); !httpio.HasForbidden(err) {
		t.Errorf("the asking delete of a description that marks the kind AsCaller = %v, want a 403", err)
	}
	if !standing("marked") || rowsStanding(t, db, "CascadeAssignments", "marked-1") != 1 {
		t.Error("the refused delete deleted rows, want none")
	}

	staff := &Caller{Permissions: tablePermissions{}, Scope: accesstypes.GlobalScope(), Env: RequestEnvironment(), collection: callerCollection(t)}
	if err := deleteUnarmed[reachAccount](t, db, staff, "staff", true); err != nil {
		t.Fatalf("the delete of a caller checked for the whole application = %v", err)
	}
	if standing("staff") || rowsStanding(t, db, "CascadeAssignments", "staff-1", "staff-2", "staff-3") != 0 {
		t.Error("the delete of a caller checked for the whole application left rows, want every tenant's and none's gone")
	}
	if err := deleteUnarmed[reachAccount](t, db, nil, "app", true); err != nil {
		t.Fatalf("the delete with no caller = %v", err)
	}
	if standing("app") || rowsStanding(t, db, "CascadeAssignments", "app-1", "app-2") != 0 {
		t.Error("the delete with no caller left rows, want every tenant's and none's gone")
	}
}

// TestTakingDelete_beyondReach holds, over the emulator, the refusal of an account's
// delete over its rows that the permission its request was checked by does not reach,
// for a caller checked in Acme: a row of another tenant refuses the asking request and
// the taking one alike, 409, in the sentence that names the tenant by its row's name,
// with nothing taken; one row in all is "It has"; a tenant with no name to show (no
// tenants named, no name column, a NULL name, no row of the tenants table) is "another"
// and the tenant's word. Rows of no tenant (a kind with none, and a kind whose way to its
// tenant is empty) refuse in the sentence for those. Rows in two other tenants and of
// none name the tenant whose key sorts first, counting its rows alone, every marked kind
// in the question's order. A caller checked for the whole application, and no caller,
// are refused nothing and send no statement that counts by tenant.
func TestTakingDelete_beyondReach(t *testing.T) {
	t.Parallel()

	db := reachDatabase(t, "delete-beyond-reach",
		assignment("two-1", "two", "acme"), assignment("two-2", "two", "borealis"), assignment("two-3", "two", "borealis"),
		assignment("one-1", "one", "acme"), assignment("one-2", "one", "borealis"),
		assignment("unnamed-1", "unnamed", "cobalt"),
		assignment("rowless-1", "rowless", "dune"),
		filter("filters-1", "filters"), filter("filters-2", "filters"), filter("filters-3", "filters"),
		filter("filter-1", "filter"),
		note("boardless-1", "boardless", ""),
		assignment("many-1", "many", "cobalt"), assignment("many-2", "many", "borealis"), assignment("many-3", "many", "borealis"),
		note("many-4", "many", "borealis-board"), filter("many-5", "many"), assignment("many-6", "many", "acme"),
		assignment("own-1", "own", "acme"), filter("own-2", "own"),
	)
	all := func(mark func(*DeleteKind)) []DeleteKind {
		return []DeleteKind{noteKind(mark), filterKind(mark), assignmentKind(mark)}
	}
	unnamedCompanies := &DeleteTenants{Table: "CascadeCompanies", Key: "Id", Word: "company"}

	tests := []struct {
		name    string
		account string
		tree    *DeleteTree
		want    string
	}{
		{name: "rows in another tenant", account: "two", want: "This account still has 2 assignments in Borealis. They have to be removed there before the account can be deleted."},
		{name: "one row in another tenant", account: "one", want: "This account still has 1 assignment in Borealis. It has to be removed there before the account can be deleted."},
		{name: "no tenants named", account: "one", tree: accountTree(nil, all(withinReach)...), want: "This account still has 1 assignment in another tenant. It has to be removed there before the account can be deleted."},
		{name: "no name column", account: "one", tree: accountTree(unnamedCompanies, all(withinReach)...), want: "This account still has 1 assignment in another company. It has to be removed there before the account can be deleted."},
		{name: "a NULL name", account: "unnamed", want: "This account still has 1 assignment in another company. It has to be removed there before the account can be deleted."},
		{name: "no row of the tenants table", account: "rowless", want: "This account still has 1 assignment in another company. It has to be removed there before the account can be deleted."},
		{name: "rows of a kind that is no tenant's", account: "filters", want: "This account still has 3 filters that are not part of any company. Only someone who manages accounts for the whole application can delete it."},
		{name: "one row that is no tenant's", account: "filter", want: "This account still has 1 filter that is not part of any company. Only someone who manages accounts for the whole application can delete it."},
		{name: "a row whose way to its tenant is empty", account: "boardless", want: "This account still has 1 note that is not part of any company. Only someone who manages accounts for the whole application can delete it."},
		{name: "two other tenants and none: the first by its key, its rows alone", account: "many", want: "This account still has 2 assignments and 1 note in Borealis. They have to be removed there before the account can be deleted."},
		{name: "no other tenant, and rows of none", account: "own", want: "This account still has 1 filter that is not part of any company. Only someone who manages accounts for the whole application can delete it."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tree := tt.tree
			if tree == nil {
				tree = accountTree(reachCompanies, all(withinReach)...)
			}
			for _, everythingUnder := range []bool{false, true} {
				run := runTakingFor(t, db, tree, map[string]any{"Id": tt.account}, cascadeRows{}, everythingUnder, 0, acmeCaller(t))
				if !httpio.HasConflict(run.err) || !strings.Contains(run.err.Error(), tt.want) {
					t.Errorf("everythingUnder %t: the delete = %v, want 409 %q", everythingUnder, run.err, tt.want)
				}
				if len(run.made) != 0 || len(run.asked) != 0 {
					t.Errorf("everythingUnder %t: the refused delete asked about %v and took %v, want nothing", everythingUnder, run.asked, run.made)
				}
			}
		})
	}

	// Whoever the request was checked for the whole application, and nobody, is refused
	// nothing, and nothing counts rows by tenant.
	staff := &Caller{Permissions: tablePermissions{}, Scope: accesstypes.GlobalScope(), Env: RequestEnvironment(), collection: callerCollection(t)}
	for who, caller := range map[string]*Caller{"the whole application's caller": staff, "no caller": nil} {
		run := runTakingFor(t, db, accountTree(reachCompanies, all(withinReach)...), map[string]any{"Id": "many"}, cascadeRows{}, true, 0, caller)
		if run.err != nil || run.round == nil || !run.round.finished || len(run.made) != 6 {
			t.Errorf("%s: the delete = %v, round %+v, took %v, want all 6 rows taken", who, run.err, run.round, run.made)
		}
		if run.byTenant != 0 {
			t.Errorf("%s: %d statements counted rows by tenant, want none", who, run.byTenant)
		}
	}
}

// TestTakingDelete_reachStatements holds, over the emulator, the statements the reach
// sends for a caller checked in Acme: one counts by tenant for a marked kind with rows
// and a tenant, and none is sent for a marked kind with no rows, for a marked kind that
// is no tenant's, or for a kind that is not marked.
func TestTakingDelete_reachStatements(t *testing.T) {
	t.Parallel()

	db := reachDatabase(t, "delete-reach-statements",
		assignment("counted-1", "counted", "acme"), assignment("counted-2", "counted", "acme"),
		note("counted-3", "counted", "acme-board"),
		filter("nobodys-1", "nobodys"),
	)
	acme := acmeCaller(t)

	// The assignments are marked and have rows; the notes have rows and are not marked;
	// the filters are marked and have none.
	tree := accountTree(reachCompanies, noteKind(unmarked), filterKind(withinReach), assignmentKind(withinReach))
	run := runTakingFor(t, db, tree, map[string]any{"Id": "counted"}, cascadeRows{}, true, 0, acme)
	if run.err != nil {
		t.Fatalf("the delete = %v", run.err)
	}
	var byTenant []string
	for _, sql := range run.statements {
		if strings.Contains(sql, "GROUP BY tenant") {
			byTenant = append(byTenant, sql)
		}
	}
	if run.byTenant != 1 || len(byTenant) != 1 || !strings.Contains(byTenant[0], "FROM `CascadeAssignments`") {
		t.Errorf("the statements that count by tenant = %q, want one, of the assignments", byTenant)
	}

	// A marked kind that is no tenant's, with rows, is refused without a statement.
	run = runTakingFor(t, db, accountTree(reachCompanies, filterKind(withinReach)), map[string]any{"Id": "nobodys"}, cascadeRows{}, true, 0, acme)
	if !httpio.HasConflict(run.err) || run.byTenant != 0 {
		t.Errorf("the delete over a kind that is no tenant's = %v with %d statements by tenant, want 409 and none", run.err, run.byTenant)
	}
}

// TestPatchSet_deleteTaken_withinReachRounds holds, over the emulator, an account's
// delete in saves too small for its rows, for a caller checked in Acme: with one row of
// another tenant the first request is refused, 409, and nothing goes; without it the
// rounds go as any delete's do, each answering what it left, and the account goes last.
func TestPatchSet_deleteTaken_withinReachRounds(t *testing.T) {
	t.Parallel()

	db := reachDatabase(t, "delete-within-reach-rounds",
		spanner.InsertMap("CascadeAccounts", map[string]any{"Id": "kept"}),
		assignment("kept-1", "kept", "acme"), assignment("kept-2", "kept", "acme"), assignment("kept-3", "kept", "borealis"),
		spanner.InsertMap("CascadeAccounts", map[string]any{"Id": "rounds"}),
		assignment("rounds-1", "rounds", "acme"), assignment("rounds-2", "rounds", "acme"), assignment("rounds-3", "rounds", "acme"),
	)
	acme := acmeCaller(t)
	round := func(id string) (*DeleteLeft, error) {
		var left *DeleteLeft
		err := NewSpannerClient(db).ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
			record := NewPatchSet(NewMetadata[reachAccount]()).SetPatchType(DeletePatchType).DeleteEverythingUnder()
			record.SetKey("ID", id)
			record.deleteSaveSize = 2
			if err := record.Buffer(WithCaller(ctx, acme), txn, eventsource.ProcessEvent("test")); err != nil {
				return err
			}
			left = record.DeleteLeft()

			return nil
		})

		return left, err
	}

	if _, err := round("kept"); !httpio.HasConflict(err) {
		t.Errorf("the first round over a row of another tenant = %v, want a 409", err)
	}
	if rowsStanding(t, db, "CascadeAccounts", "kept") != 1 || rowsStanding(t, db, "CascadeAssignments", "kept-1", "kept-2", "kept-3") != 3 {
		t.Error("the refused round deleted rows, want none")
	}

	rounds := 0
	for ; rounds < 10; rounds++ {
		left, err := round("rounds")
		if err != nil {
			t.Fatalf("round %d = %v", rounds+1, err)
		}
		if left == nil {
			break
		}
		if rowsStanding(t, db, "CascadeAccounts", "rounds") != 1 {
			t.Fatalf("round %d left %+v and deleted the account, want it standing until the last", rounds+1, left)
		}
	}
	if rounds == 0 || rowsStanding(t, db, "CascadeAccounts", "rounds") != 0 || rowsStanding(t, db, "CascadeAssignments", "rounds-1", "rounds-2", "rounds-3") != 0 {
		t.Errorf("the delete in rounds ran %d round(s) before the last and left rows, want more than one round and everything gone", rounds)
	}
}

// reachPermissions is a person's checker that answers one permission on one method by
// the tenant it is asked in: the decision the map gives the tenant, Denied in any other
// tenant and for anything else. It records every scope it is asked in.
type reachPermissions struct {
	renderStubPermissions
	method     accesstypes.Resource
	permission accesstypes.Permission
	decided    map[accesstypes.Domain]accesstypes.Decision

	mu    sync.Mutex
	asked []string
}

func (p *reachPermissions) Check(_ context.Context, _ accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	p.mu.Lock()
	p.asked = append(p.asked, fmt.Sprintf("%s %s %v", scope, perm, resources))
	p.mu.Unlock()

	out := make(accesstypes.Decisions, len(resources))
	domain, _ := scope.Domain()
	for _, res := range resources {
		decision, ok := p.decided[domain]
		if !ok || res != p.method || perm != p.permission {
			decision = accesstypes.Denied()
		}
		out[res] = decision
	}

	return out, nil
}

// askedScopes is what the checker was asked, in order.
func (p *reachPermissions) askedScopes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.asked)
}

// askedOnceEachTry reports whether what a checker was asked is the asks wanted, once in
// each try of the transaction: the emulator may abort a transaction, and the delete is
// run again from the start.
func askedOnceEachTry(got, want []string) bool {
	if len(want) == 0 || len(got) == 0 {
		return len(got) == len(want)
	}
	if len(got)%len(want) != 0 {
		return false
	}
	for try := range slices.Chunk(got, len(want)) {
		if !slices.Equal(try, want) {
			return false
		}
	}

	return true
}

// reachMethod is the method an account's delete is asked through in the tests of the
// reach into other tenants.
const reachMethod = accesstypes.Resource("DeleteCrewUser")

// methodCaller is a person whose request to delete an account was let in by Execute on
// the method, checked in Acme, whose checker answers by tenant as decided says. Without
// the method, it is a caller whose entry check is not known.
func methodCaller(t *testing.T, withMethod bool, decided map[accesstypes.Domain]accesstypes.Decision) (*Caller, *reachPermissions) {
	t.Helper()

	perms := &reachPermissions{method: reachMethod, permission: accesstypes.Execute, decided: decided}
	caller := &Caller{Permissions: perms, Scope: accesstypes.DomainScope("acme"), Env: RequestEnvironment(), collection: callerCollection(t)}
	if withMethod {
		caller.method, caller.permission = reachMethod, accesstypes.Execute
	}

	return caller, perms
}

// TestTakingDelete_reachOtherTenants holds, over the emulator, an account's delete
// under Acme's address by a person who may delete accounts in another tenant too: the
// permission the request was let in by, held in Borealis, granted or under a condition,
// reaches the rows of Borealis, which the question counts and the delete takes; denied
// there, the delete is refused in the sentence that names Borealis. Of two other
// tenants, the one reached is passed over and the sentence names the other. A caller
// whose entry check is not known reaches no other tenant and asks nothing. Rows of no
// tenant still refuse. Each other tenant is asked once, for the method and the
// permission the request was let in by, in each try of the transaction.
func TestTakingDelete_reachOtherTenants(t *testing.T) {
	t.Parallel()

	db := reachDatabase(t, "delete-reach-other-tenants",
		spanner.InsertMap("CascadeCompanies", map[string]any{"Id": "eastwind", "Name": "Eastwind"}),
		assignment("dana-1", "dana", "acme"), assignment("dana-2", "dana", "borealis"), assignment("dana-3", "dana", "borealis"),
		assignment("spread-1", "spread", "acme"), assignment("spread-2", "spread", "borealis"), assignment("spread-3", "spread", "eastwind"),
		assignment("kept-1", "kept", "acme"), assignment("kept-2", "kept", "borealis"), filter("kept-3", "kept"),
	)
	granted := map[accesstypes.Domain]accesstypes.Decision{"borealis": accesstypes.Granted()}
	conditional := map[accesstypes.Domain]accesstypes.Decision{
		"borealis": accesstypes.Conditional(accesstypes.ConditionGroup{Resources: []accesstypes.Resource{reachMethod}, Condition: testCondition}),
	}
	borealisAsked := "borealis Execute [DeleteCrewUser]"

	tests := []struct {
		name       string
		account    string
		withMethod bool
		decided    map[accesstypes.Domain]accesstypes.Decision
		wantAsked  string
		wantRefuse string
		wantScopes []string
	}{
		{
			name: "granted in the other tenant", account: "dana", withMethod: true, decided: granted,
			wantAsked: "Delete this account and everything under it: 3 assignments.", wantScopes: []string{borealisAsked},
		},
		{
			name: "held there under a condition", account: "dana", withMethod: true, decided: conditional,
			wantAsked: "Delete this account and everything under it: 3 assignments.", wantScopes: []string{borealisAsked},
		},
		{
			name: "denied there", account: "dana", withMethod: true,
			wantRefuse: "This account still has 2 assignments in Borealis. They have to be removed there before the account can be deleted.", wantScopes: []string{borealisAsked},
		},
		{
			name: "two other tenants, one reached", account: "spread", withMethod: true, decided: granted,
			wantRefuse: "This account still has 1 assignment in Eastwind. It has to be removed there before the account can be deleted.",
			wantScopes: []string{borealisAsked, "eastwind Execute [DeleteCrewUser]"},
		},
		{
			name: "a caller whose entry check is not known", account: "dana", decided: granted,
			wantRefuse: "This account still has 2 assignments in Borealis. They have to be removed there before the account can be deleted.",
		},
		{
			name: "rows of no tenant", account: "kept", withMethod: true, decided: granted,
			wantRefuse: "This account still has 1 filter that is not part of any company. Only someone who manages accounts for the whole application can delete it.",
			wantScopes: []string{borealisAsked},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tree := accountTree(reachCompanies, filterKind(withinReach), assignmentKind(withinReach))
			for _, everythingUnder := range []bool{false, true} {
				caller, perms := methodCaller(t, tt.withMethod, tt.decided)
				run := runTakingFor(t, db, tree, map[string]any{"Id": tt.account}, cascadeRows{}, everythingUnder, 0, caller)
				switch {
				case tt.wantRefuse != "":
					if !httpio.HasConflict(run.err) || !strings.Contains(run.err.Error(), tt.wantRefuse) {
						t.Errorf("everythingUnder %t: the delete = %v, want 409 %q", everythingUnder, run.err, tt.wantRefuse)
					}
					if len(run.made) != 0 {
						t.Errorf("everythingUnder %t: the refused delete took %v, want nothing", everythingUnder, run.made)
					}
				case !everythingUnder:
					if !httpio.HasPreconditionRequired(run.err) || !strings.Contains(run.err.Error(), tt.wantAsked) {
						t.Errorf("the asking delete = %v, want the question %q", run.err, tt.wantAsked)
					}
				default:
					if run.err != nil || run.round == nil || !run.round.finished || len(run.made) != 3 {
						t.Errorf("the taking delete = %v, round %+v, took %v, want the 3 assignments taken", run.err, run.round, run.made)
					}
				}
				if got := perms.askedScopes(); !askedOnceEachTry(got, tt.wantScopes) {
					t.Errorf("everythingUnder %t: the checker was asked %q, want %q in each try of the transaction", everythingUnder, got, tt.wantScopes)
				}
			}
		})
	}
}

// TestPatchSet_deleteTaken_reachOtherTenants holds, over the emulator, a real save of an
// account's delete under Acme's address by a person who may delete accounts in Borealis
// too: told to take them, the assignments of both tenants go with the account; with the
// permission denied in Borealis, nothing goes.
func TestPatchSet_deleteTaken_reachOtherTenants(t *testing.T) {
	t.Parallel()

	db := reachDatabase(t, "delete-reach-other-tenants-saved",
		spanner.InsertMap("CascadeAccounts", map[string]any{"Id": "dana"}),
		assignment("dana-1", "dana", "acme"), assignment("dana-2", "dana", "borealis"),
		spanner.InsertMap("CascadeAccounts", map[string]any{"Id": "kept"}),
		assignment("kept-1", "kept", "acme"), assignment("kept-2", "kept", "borealis"),
	)

	reaching, _ := methodCaller(t, true, map[accesstypes.Domain]accesstypes.Decision{"borealis": accesstypes.Granted()})
	if err := deleteUnarmed[reachAccount](t, db, reaching, "dana", true); err != nil {
		t.Fatalf("the delete by a person who may delete accounts in Borealis = %v", err)
	}
	if rowsStanding(t, db, "CascadeAccounts", "dana") != 0 || rowsStanding(t, db, "CascadeAssignments", "dana-1", "dana-2") != 0 {
		t.Error("the delete left rows, want the account and the assignments of both tenants gone")
	}

	denied, _ := methodCaller(t, true, nil)
	err := deleteUnarmed[reachAccount](t, db, denied, "kept", true)
	if want := "This account still has 1 assignment in Borealis."; !httpio.HasConflict(err) || !strings.Contains(err.Error(), want) {
		t.Errorf("the delete by a person who may not delete accounts in Borealis = %v, want 409 %q", err, want)
	}
	if rowsStanding(t, db, "CascadeAccounts", "kept") != 1 || rowsStanding(t, db, "CascadeAssignments", "kept-1", "kept-2") != 2 {
		t.Error("the refused delete deleted rows, want none")
	}
}
