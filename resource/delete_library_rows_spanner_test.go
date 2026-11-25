package resource

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// tablePermissions is a caller's grants by permission and resource, as decided; a
// resource the table does not name is Denied.
type tablePermissions struct {
	renderStubPermissions
	decided map[accesstypes.Permission]accesstypes.Decisions
}

func (p tablePermissions) Check(_ context.Context, _ accesstypes.Environment, _ accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	out := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		if decision, ok := p.decided[perm][res]; ok {
			out[res] = decision
		} else {
			out[res] = accesstypes.Denied()
		}
	}

	return out, nil
}

// callerCollection binds the tasks' title, which a test's grant may hold a condition on.
func callerCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		{
			Name: "CascadeTasks", Scope: accesstypes.GlobalPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Delete, accesstypes.List},
			Attributes:  []AttributeData{{Name: "title", Column: "Title", Type: AttributeTypeString}},
		},
		{
			Name: "CascadeComments", Scope: accesstypes.GlobalPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.Delete, accesstypes.List},
		},
	}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// callerOf is a person in the global scope whose grants are the table's.
func callerOf(t *testing.T, decided map[accesstypes.Permission]accesstypes.Decisions) *Caller {
	t.Helper()

	return &Caller{Permissions: tablePermissions{decided: decided}, Scope: accesstypes.GlobalScope(), Env: RequestEnvironment(), collection: callerCollection(t)}
}

// callerTask is a CascadeTasks row whose delete asks a rule, which refuses a task whose
// id starts with "ruled", and, armed, the caller's grant on tasks and a question, which
// refuses a task whose id starts with "asked", as a record that decides what a person
// may do is asked as a removal.
type callerTask struct {
	ID           string             `spanner:"Id"`
	ProjectID    spanner.NullString `spanner:"ProjectId"`
	ParentTaskID spanner.NullString `spanner:"ParentTaskId"`
	Title        spanner.NullString `spanner:"Title"`
}

func (callerTask) Resource() accesstypes.Resource {
	return "CascadeTasks"
}

// armCallerTask arms a task's delete as the generator arms a kind a record takes.
func armCallerTask(patchSet *PatchSet[callerTask], caller *Caller) {
	patchSet.RegisterValidateDeleteFunc(func(context.Context, ReadWriteTransaction) error {
		if strings.HasPrefix(patchSet.PrimaryKey().RowID(), "ruled") {
			return httpio.NewBadRequestMessage("This task is ruled out of a delete.")
		}

		return nil
	})
	if caller.IsZero() {
		return
	}
	patchSet.Enforce(caller, newDeleteSet[callerTask](), accesstypes.Delete)
	patchSet.RegisterDeleteCheckFunc(func(context.Context, ReadWriteTransaction) error {
		if strings.HasPrefix(patchSet.PrimaryKey().RowID(), "asked") {
			return httpio.NewConflictMessage("This task's person may not lose it.")
		}

		return nil
	})
}

// armCallerComment arms a comment's delete against the caller where there is one.
func armCallerComment(patchSet *PatchSet[roundComment], caller *Caller) {
	if caller.IsZero() {
		return
	}
	patchSet.Enforce(caller, newDeleteSet[roundComment](), accesstypes.Delete)
}

// callerProjectTree is the description of a CascadeProjects record of the library's
// whose delete takes the comments and the tasks that name it, the tasks marked AsCaller
// where marked says so.
func callerProjectTree(marked bool) *DeleteTree {
	byProject := DeletePath{{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}}}

	return &DeleteTree{
		Record: "CascadeProjects", Words: DeleteWords{One: "project", Many: "projects"}, Key: []string{"Id"},
		Taken: []DeleteKind{
			{
				Resource: "CascadeTasks", Words: DeleteWords{One: "task", Many: "tasks"}, Key: []string{"Id"},
				Paths: []DeletePath{byProject}, Rows: NewTakenRows(armCallerTask), AsCaller: marked,
			},
			{
				Resource: "CascadeComments", Words: DeleteWords{One: "comment", Many: "comments"}, Key: []string{"Id"},
				Paths: []DeletePath{byProject}, Rows: NewTakenRows(armCallerComment),
			},
		},
	}
}

// callerProject is a CascadeProjects record whose tasks are checked as whoever its
// delete is for (callerProjectTree).
type callerProject struct {
	ID   string             `spanner:"Id"`
	Code spanner.NullString `spanner:"Code"`
}

func (callerProject) Resource() accesstypes.Resource {
	return "CascadeProjects"
}

func (callerProject) DeleteTree() *DeleteTree {
	return callerProjectTree(true)
}

// unmarkedProject is callerProject with its tasks not marked.
type unmarkedProject struct {
	ID   string             `spanner:"Id"`
	Code spanner.NullString `spanner:"Code"`
}

func (unmarkedProject) Resource() accesstypes.Resource {
	return "CascadeProjects"
}

func (unmarkedProject) DeleteTree() *DeleteTree {
	return callerProjectTree(false)
}

// deleteUnarmed deletes the record R with the id by a patch that is not armed, in a
// context that carries the caller, or none where it is nil, told to take what is under
// it where everythingUnder says so.
func deleteUnarmed[R Resourcer](t *testing.T, db *spanner.Client, caller *Caller, id string, everythingUnder bool) error {
	t.Helper()

	return NewSpannerClient(db).ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
		if caller != nil {
			ctx = WithCaller(ctx, caller)
		}
		record := NewPatchSet(NewMetadata[R]()).SetPatchType(DeletePatchType)
		if everythingUnder {
			record.DeleteEverythingUnder()
		}
		record.SetKey("ID", id)

		return record.Buffer(ctx, txn, "test")
	})
}

// rowsStanding counts the rows of the table with the ids.
func rowsStanding(t *testing.T, db *spanner.Client, table string, ids ...string) int64 {
	t.Helper()

	var n int64
	stmt := spanner.Statement{SQL: "SELECT COUNT(*) FROM " + table + " WHERE Id IN UNNEST(@ids)", Params: map[string]any{"ids": ids}}
	if err := db.Single().Query(t.Context(), stmt).Do(func(row *spanner.Row) error {
		return row.Column(0, &n)
	}); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}

	return n
}

// TestPatchSet_deleteTaken_asCaller holds, over the emulator, a record deleted by a
// patch that is not armed, in a context that carries a caller, whose description marks
// its tasks AsCaller: a caller with no grant to delete tasks is refused in the sentence
// that names them, asking and taking, and nothing goes; so is one whose grant's
// condition a task fails; a task the kind's rule refuses refuses in the rule's sentence,
// and one the kind's question refuses in the question's; a caller who holds the grant is
// asked the question, with the tasks counted where they may list them and "records you
// can't see" where they may not, and confirmed, the rows and the record go. With no
// caller in the context the rows go and only the rule is asked. The comments, not
// marked, are taken with no grant asked; and so are the tasks of a description that does
// not mark them.
func TestPatchSet_deleteTaken_asCaller(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "delete-taken-as-caller")
	var mutations []*spanner.Mutation
	for project, tasks := range map[string][]map[string]any{
		"denied":   {{"Id": "denied-t1", "Title": "open"}},
		"locked":   {{"Id": "locked-t1", "Title": "open"}, {"Id": "locked-t2", "Title": "locked"}},
		"ruled":    {{"Id": "ruled-t1", "Title": "open"}},
		"asked":    {{"Id": "asked-t1", "Title": "open"}},
		"held":     {{"Id": "held-t1", "Title": "open"}, {"Id": "held-t2", "Title": "open"}},
		"nobody":   {{"Id": "asked-t2", "Title": "open"}},
		"ruledout": {{"Id": "ruled-t2", "Title": "open"}},
		"unmarked": {{"Id": "unmarked-t1", "Title": "open"}},
	} {
		mutations = append(mutations,
			spanner.InsertMap("CascadeProjects", map[string]any{"Id": project}),
			spanner.InsertMap("CascadeComments", map[string]any{"Id": project + "-c1", "ProjectId": project}))
		for _, task := range tasks {
			task["ProjectId"] = project
			mutations = append(mutations, spanner.InsertMap("CascadeTasks", task))
		}
	}
	if _, err := db.Apply(t.Context(), mutations); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	granted := accesstypes.Granted()
	lists := accesstypes.Decisions{"CascadeTasks": granted, "CascadeComments": granted}
	holder := callerOf(t, map[accesstypes.Permission]accesstypes.Decisions{
		accesstypes.Delete: {"CascadeTasks": granted},
		accesstypes.List:   lists,
	})
	standing := func(project string) bool {
		return rowsStanding(t, db, "CascadeProjects", project) == 1
	}

	refused := []struct {
		name    string
		project string
		caller  *Caller
		status  int
		want    string
	}{
		{
			name: "no grant to delete tasks", project: "denied",
			caller: callerOf(t, map[accesstypes.Permission]accesstypes.Decisions{accesstypes.List: lists}),
			status: http.StatusForbidden, want: "This project has tasks that you can't delete, so it can't be deleted.",
		},
		{
			name: "a grant whose condition a task fails", project: "locked",
			caller: callerOf(t, map[accesstypes.Permission]accesstypes.Decisions{
				accesstypes.Delete: {"CascadeTasks": accesstypes.Conditional(accesstypes.ConditionGroup{
					Resources: []accesstypes.Resource{"CascadeTasks"}, Condition: mustCondition("title != 'locked'"),
				})},
				accesstypes.List: lists,
			}),
			status: http.StatusForbidden, want: "This project has tasks that you can't delete, so it can't be deleted.",
		},
		{name: "a task the rule refuses", project: "ruled", caller: holder, status: http.StatusBadRequest, want: "This task is ruled out of a delete."},
		{name: "a task the question refuses", project: "asked", caller: holder, status: http.StatusConflict, want: "This task's person may not lose it."},
		{name: "no caller, a task the rule refuses", project: "ruledout", status: http.StatusBadRequest, want: "This task is ruled out of a delete."},
	}
	for _, tt := range refused {
		for _, everythingUnder := range []bool{false, true} {
			err := deleteUnarmed[callerProject](t, db, tt.caller, tt.project, everythingUnder)
			if statusOf(err) != tt.status || httpio.Message(err) != tt.want {
				t.Errorf("%s (everythingUnder %t): the delete = %v (%q), want a %d saying %q", tt.name, everythingUnder, err, httpio.Message(err), tt.status, tt.want)
			}
			if !standing(tt.project) {
				t.Fatalf("%s (everythingUnder %t): the project is gone, want it kept", tt.name, everythingUnder)
			}
		}
	}

	for _, tt := range []struct {
		name   string
		caller *Caller
		want   string
	}{
		{name: "who may list the tasks", caller: holder, want: "Delete this project and everything under it: 1 comment, 2 tasks. This can't be undone."},
		{
			name: "who may not list the tasks",
			caller: callerOf(t, map[accesstypes.Permission]accesstypes.Decisions{
				accesstypes.Delete: {"CascadeTasks": granted},
				accesstypes.List:   {"CascadeComments": granted},
			}),
			want: "Delete this project and everything under it: 1 comment, and records you can't see. This can't be undone.",
		},
	} {
		err := deleteUnarmed[callerProject](t, db, tt.caller, "held", false)
		if statusOf(err) != http.StatusPreconditionRequired || httpio.Message(err) != tt.want {
			t.Errorf("the question for a person %s = %v (%q), want a 428 saying %q", tt.name, err, httpio.Message(err), tt.want)
		}
	}
	if err := deleteUnarmed[callerProject](t, db, holder, "held", true); err != nil {
		t.Fatalf("the confirmed delete of a person who holds the grant = %v", err)
	}
	if standing("held") || rowsStanding(t, db, "CascadeTasks", "held-t1", "held-t2") != 0 || rowsStanding(t, db, "CascadeComments", "held-c1") != 0 {
		t.Error("the confirmed delete of a person who holds the grant left rows, want the project and everything under it gone")
	}

	// No caller: the question is not asked of the task, and it goes.
	if err := deleteUnarmed[callerProject](t, db, nil, "nobody", true); err != nil {
		t.Fatalf("the delete with no caller = %v", err)
	}
	if standing("nobody") || rowsStanding(t, db, "CascadeTasks", "asked-t2") != 0 {
		t.Error("the delete with no caller left rows, want the project and its task gone")
	}

	// Not marked: the tasks go with no grant asked, as the comments do.
	noGrant := callerOf(t, map[accesstypes.Permission]accesstypes.Decisions{accesstypes.List: lists})
	if err := deleteUnarmed[unmarkedProject](t, db, noGrant, "unmarked", true); err != nil {
		t.Fatalf("the delete of a description that does not mark its tasks = %v", err)
	}
	if standing("unmarked") || rowsStanding(t, db, "CascadeTasks", "unmarked-t1") != 0 || rowsStanding(t, db, "CascadeComments", "unmarked-c1") != 0 {
		t.Error("the delete of a description that does not mark its tasks left rows, want the project and everything under it gone")
	}
}

// aloneInvoice is a CascadeInvoices row, whose changes are recorded.
type aloneInvoice struct {
	ID            string             `spanner:"Id"`
	TaskID        spanner.NullString `spanner:"TaskId"`
	CheckedTaskID spanner.NullString `spanner:"CheckedTaskId"`
}

func (aloneInvoice) Resource() accesstypes.Resource {
	return "CascadeInvoices"
}

func (aloneInvoice) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (aloneInvoice) DefaultConfig() Config {
	return Config{}
}

// aloneTimesheet is a CascadeTimesheets row.
type aloneTimesheet struct {
	ID     string             `spanner:"Id"`
	TaskID spanner.NullString `spanner:"TaskId"`
}

func (aloneTimesheet) Resource() accesstypes.Resource {
	return "CascadeTimesheets"
}

// aloneTask is a CascadeTasks record of the library's: its delete takes the invoices
// that name it, marked AsCaller, its assignments, marked WithinReach, and its comments,
// trusted; its timesheets, marked, refuse it; and the tasks under it, marked, are under
// it and in no other list.
type aloneTask struct {
	ID           string             `spanner:"Id"`
	ProjectID    spanner.NullString `spanner:"ProjectId"`
	ParentTaskID spanner.NullString `spanner:"ParentTaskId"`
	Title        spanner.NullString `spanner:"Title"`
}

func (aloneTask) Resource() accesstypes.Resource {
	return "CascadeTasks"
}

func (aloneTask) DeleteTree() *DeleteTree {
	byTask := func(column string) []DeletePath {
		return []DeletePath{{{Columns: []string{column}, Table: "CascadeTasks", References: []string{"Id"}}}}
	}

	return &DeleteTree{
		Record: "CascadeTasks", Words: DeleteWords{One: "task", Many: "tasks"}, Key: []string{"Id"},
		Taken: []DeleteKind{
			{
				Resource: "CascadeInvoices", Words: DeleteWords{One: "invoice", Many: "invoices"}, Key: []string{"Id"},
				Paths: byTask("TaskId"), Rows: NewTakenRows(func(*PatchSet[aloneInvoice], *Caller) {}), AsCaller: true,
			},
			{
				Resource: "CascadeComments", Words: DeleteWords{One: "comment", Many: "comments"}, Key: []string{"Id"},
				Paths: byTask("TaskId"), Rows: NewTakenRows(func(*PatchSet[roundComment], *Caller) {}), Trusted: true,
			},
			{
				Resource: "CascadeAssignments", Words: DeleteWords{One: "assignment", Many: "assignments"}, Key: []string{"Id"},
				Paths: byTask("TaskId"), Rows: NewTakenRows(func(*PatchSet[reachAssignment], *Caller) {}), Tenant: &DomainBindingData{Column: "TenantId"}, WithinReach: true,
			},
		},
		RefusedBy: []DeleteKind{{
			Resource: "CascadeTimesheets", Words: DeleteWords{One: "timesheet", Many: "timesheets"}, Key: []string{"Id"},
			Paths: byTask("TaskId"), Rows: NewTakenRows(func(*PatchSet[aloneTimesheet], *Caller) {}), AsCaller: true,
		}},
		Under: []DeleteKind{{
			Resource: "CascadeTasks", Words: DeleteWords{One: "task", Many: "tasks"}, Key: []string{"Id"},
			Paths: byTask("ParentTaskId"), AsCaller: true,
		}},
	}
}

// aloneAccount is a CascadeProjects record of the library's with the tasks that name it
// under it alone, marked, as an account's description has the application's rows that
// name it.
type aloneAccount struct {
	ID   string             `spanner:"Id"`
	Code spanner.NullString `spanner:"Code"`
}

func (aloneAccount) Resource() accesstypes.Resource {
	return "CascadeProjects"
}

func (aloneAccount) DeleteTree() *DeleteTree {
	return &DeleteTree{
		Record: "CascadeProjects", Words: DeleteWords{One: "project", Many: "projects"}, Key: []string{"Id"},
		Under: []DeleteKind{{
			Resource: "CascadeTasks", Words: DeleteWords{One: "task", Many: "tasks"}, Key: []string{"Id"},
			Paths: []DeletePath{{{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}}}}, AsCaller: true,
		}},
	}
}

// TestPatchSet_DeleteLibraryRowsAlone holds, over the emulator, a record of the
// library's deleted with what its tenant's delete took before it: a save that deletes the
// rows of its marked kinds by patches that are not armed and then the record, told to
// take the library's rows alone, commits, in a context whose caller, checked in Acme,
// holds no grant, with the trusted kind's rows and the record gone, the taken marked
// kind's row deleted once, and the row of the kind marked WithinReach, of another
// tenant, standing and refusing nothing; the delete it reads names none of the marked
// kinds. The same save without it is refused. A plain delete of a record with marked kinds under it alone, told to take the
// library's rows alone, records nothing to count before the commit.
func TestPatchSet_DeleteLibraryRowsAlone(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "delete-library-rows-alone")
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.InsertMap("CascadeTasks", map[string]any{"Id": "r"}),
		spanner.InsertMap("CascadeTasks", map[string]any{"Id": "z1", "ParentTaskId": "r"}),
		spanner.InsertMap("CascadeInvoices", map[string]any{"Id": "x1", "TaskId": "r"}),
		spanner.InsertMap("CascadeComments", map[string]any{"Id": "m1", "TaskId": "r"}),
		spanner.InsertMap("CascadeTimesheets", map[string]any{"Id": "y1", "TaskId": "r"}),
		spanner.InsertMap("CascadeAssignments", map[string]any{"Id": "w1", "TaskId": "r", "TenantId": "borealis"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	nobodysGrants := acmeCaller(t)
	save := func(alone bool) error {
		return NewSpannerClient(db).ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
			ctx = WithCaller(ctx, nobodysGrants)
			invoice := NewPatchSet(NewMetadata[aloneInvoice]()).SetPatchType(DeletePatchType)
			invoice.SetKey("ID", "x1")
			timesheet := NewPatchSet(NewMetadata[aloneTimesheet]()).SetPatchType(DeletePatchType)
			timesheet.SetKey("ID", "y1")
			child := NewPatchSet(NewMetadata[aloneTask]()).SetPatchType(DeletePatchType).DeleteEverythingUnder()
			child.SetKey("ID", "z1")
			for _, row := range []bufferer{invoice, timesheet, child} {
				if err := row.Buffer(ctx, txn, "test"); err != nil {
					return errors.Wrap(err, "a row's own delete")
				}
			}
			record := NewPatchSet(NewMetadata[aloneTask]()).SetPatchType(DeletePatchType).DeleteEverythingUnder()
			if alone {
				record.DeleteLibraryRowsAlone()
			}
			record.SetKey("ID", "r")

			return record.Buffer(ctx, txn, "test")
		})
	}

	if err := save(false); !httpio.HasConflict(err) {
		t.Errorf("the save whose record's delete takes the application's rows = %v, want a 409", err)
	}
	if rowsStanding(t, db, "CascadeTasks", "r") != 1 || rowsStanding(t, db, "CascadeInvoices", "x1") != 1 {
		t.Fatal("the refused save deleted rows, want none")
	}
	if err := save(true); err != nil {
		t.Fatalf("the save whose record's delete takes the library's rows alone = %v, want it committed", err)
	}
	for table, id := range map[string]string{"CascadeTasks": "r", "CascadeComments": "m1", "CascadeInvoices": "x1", "CascadeTimesheets": "y1"} {
		if rowsStanding(t, db, table, id) != 0 {
			t.Errorf("the row %s of %s stands, want it gone", id, table)
		}
	}
	if rowsStanding(t, db, "CascadeAssignments", "w1") != 1 {
		t.Error("the assignment of another tenant is gone, want it left out of the delete")
	}
	var events int64
	if err := db.Single().Query(t.Context(), spanner.Statement{SQL: "SELECT COUNT(*) FROM DataChangeEvents WHERE TableName = 'CascadeInvoices' AND RowId = 'x1'"}).Do(func(row *spanner.Row) error {
		return row.Column(0, &events)
	}); err != nil {
		t.Fatalf("counting the invoice's change records: %v", err)
	}
	if events != 1 {
		t.Errorf("the invoice has %d change records, want 1: deleted once", events)
	}

	// The delete the record reads, run as deleteTaken runs it.
	alone := NewPatchSet(NewMetadata[aloneTask]()).SetPatchType(DeletePatchType).DeleteLibraryRowsAlone()
	run := runTakingFor(t, db, alone.deleteDescription(), map[string]any{"Id": "r"}, cascadeRows{}, true, 0, nobodysGrants)
	if run.err != nil {
		t.Fatalf("the delete the record reads = %v", run.err)
	}
	if named := slices.IndexFunc(run.statements, func(sql string) bool {
		return strings.Contains(sql, "CascadeInvoices") || strings.Contains(sql, "CascadeTimesheets") || strings.Contains(sql, "ParentTaskId") || strings.Contains(sql, "CascadeAssignments")
	}); named >= 0 {
		t.Errorf("the delete told to take the library's rows alone sent %q, want no statement naming a marked kind", run.statements[named])
	}

	// An account's shape: marked kinds under it alone.
	for _, tt := range []struct {
		alone bool
		want  int
	}{{alone: false, want: 1}, {alone: true, want: 0}} {
		txn := &countingTxn{transactionDeletes: newTransactionDeletes()}
		record := NewPatchSet(NewMetadata[aloneAccount]()).SetPatchType(DeletePatchType)
		if tt.alone {
			record.DeleteLibraryRowsAlone()
		}
		record.SetKey("ID", "p1")
		if err := record.Buffer(t.Context(), txn); err != nil {
			t.Fatalf("PatchSet.Buffer() error = %v", err)
		}
		if len(txn.deletes) != tt.want {
			t.Errorf("told to take the library's rows alone %t: %d deletes are noted to count over, want %d", tt.alone, len(txn.deletes), tt.want)
		}
	}
}
