package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// The people of the tenant alpha in the tests of its kinds. anna and ben hold roles in
// alpha alone, ben through a place in its group too; cara holds a role in alpha and one
// in beta; dora signs in through alpha's directory and holds nothing; evan holds a role in
// alpha and signs in through beta's directory.
var (
	annaID    = mustUUID("0190c000-0000-7000-8000-000000000001")
	benID     = mustUUID("0190c000-0000-7000-8000-000000000002")
	caraID    = mustUUID("0190c000-0000-7000-8000-000000000003")
	doraID    = mustUUID("0190c000-0000-7000-8000-000000000004")
	evanID    = mustUUID("0190c000-0000-7000-8000-000000000005")
	annaLogin = mustUUID("0190c000-0000-7000-8000-000000000011")
	doraLogin = mustUUID("0190c000-0000-7000-8000-000000000014")
	doraLink  = mustUUID("0190c000-0000-7000-8000-000000000024")
	evanLink  = mustUUID("0190c000-0000-7000-8000-000000000025")
	invited   = mustUUID("0190c000-0000-7000-8000-000000000031")
	betaInvit = mustUUID("0190c000-0000-7000-8000-000000000032")

	annaAlpha = access.NewMembershipKey(alphaScope, User(annaID), "Reader")
	benAlpha  = access.NewMembershipKey(alphaScope, User(benID), "Reader")
	caraAlpha = access.NewMembershipKey(alphaScope, User(caraID), "Reader")
	caraBeta  = access.NewMembershipKey(accesstypes.DomainPolicyScope("beta"), User(caraID), "Reader")
	evanAlpha = access.NewMembershipKey(alphaScope, User(evanID), "Reader")

	nightInAlpha = access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")
	staffInAlpha = access.NewGroupRoleKey(GroupID(staffID), alphaScope, "Reader")
	staffInBeta  = access.NewGroupRoleKey(GroupID(staffID), accesstypes.DomainPolicyScope("beta"), "Reader")
)

// alphaStore is the auth's tables holding the tenant alpha's people, its group night
// with ben and cara in it and a role, the staff group of the whole application holding
// a role in alpha and one in beta, a custom role made in alpha and one in beta, and an
// invitation into each. The accounts in gone were deleted by an earlier save, with
// everything they held.
func alphaStore(gone ...impulse.UUID) *fakeStore {
	store := &fakeStore{
		memberships: map[access.MembershipKey]bool{annaAlpha: true, benAlpha: true, caraAlpha: true, caraBeta: true, evanAlpha: true},
		roles: map[access.RoleKey]bool{
			{Kind: "domain", Domain: "alpha", Role: "Auditor"}: true,
			{Kind: "domain", Domain: "beta", Role: "Auditor"}:  true,
		},
		usernames:   map[impulse.UUID]string{annaID: "anna", benID: "ben", caraID: "cara", doraID: "dora", evanID: "evan"},
		sessions:    map[impulse.UUID][]impulse.UUID{annaID: {annaLogin}, doraID: {doraLogin}},
		identities:  map[impulse.UUID]impulse.UUID{doraLink: doraID, evanLink: evanID},
		managing:    map[impulse.UUID][]accesstypes.Domain{doraID: {"alpha"}, evanID: {"beta"}},
		invitations: map[accesstypes.Domain][]impulse.UUID{"alpha": {invited}, "beta": {betaInvit}},
		fakeGroupTables: fakeGroupTables{
			groups: map[impulse.UUID]*Group{
				nightID: {Kind: "domain", Domain: "alpha", Name: "Night shift"},
				staffID: {Kind: "global", Name: "Staff"},
			},
			groupMembers: map[access.GroupMemberKey]bool{memberKey(nightID, benID): true, memberKey(nightID, caraID): true},
			groupRoles:   map[access.GroupRoleKey]bool{nightInAlpha: true, staffInAlpha: true, staffInBeta: true},
		},
	}
	for _, id := range gone {
		delete(store.usernames, id)
		delete(store.sessions, id)
		delete(store.managing, id)
		for key := range store.memberships {
			if key.User == User(id) {
				delete(store.memberships, key)
			}
		}
		for key := range store.groupMembers {
			if key.User == User(id) {
				delete(store.groupMembers, key)
			}
		}
		for link, user := range store.identities {
			if user == id {
				delete(store.identities, link)
			}
		}
	}

	return store
}

// kindCounts are the counts of a tenant's kinds by their words.
func kindCounts(ctx context.Context, txn resource.ReadWriteTransaction, kinds []resource.TakenKind) (map[string]int64, error) {
	counts := make(map[string]int64, len(kinds))
	for _, kind := range kinds {
		rows, err := kind.Count(ctx, txn)
		if err != nil {
			return nil, errors.Wrapf(err, "counting the %s", kind.Words().Many)
		}
		counts[kind.Words().Many] = rows
	}

	return counts, nil
}

// takenRows is what one kind's Take answered.
type takenRows struct {
	Rows      int64
	Mutations int
	More      bool
}

// TestManager_TenantKinds_counts pins what the tenant's delete counts of the auth's rows:
// the accounts that are the tenant's alone (anna and ben, who hold roles in it alone, and
// dora, whom its directory manages and who holds nothing), every membership held in it and
// the role a group of the whole application holds there, its group, its custom role and
// its invitation that stands. cara, who holds a role in beta, and evan, whom beta's
// directory manages, keep their accounts; another tenant's rows are not counted.
//
// Six more hold a role in alpha, which is counted with the roles held, and keep their
// accounts, each for one thing more: fay holds a role for the whole application, gus one
// in every tenant, hal a place in the staff group of the whole application, ida a place in
// beta's group, and jo a role in beta whose period has not begun. The sixth membership
// names an id that is no account's, and no account is counted for it.
func TestManager_TenantKinds_counts(t *testing.T) {
	t.Parallel()

	_, upcoming, _ := periodFixtures()
	fayID := mustUUID("0190c000-0000-7000-8000-000000000006")
	gusID := mustUUID("0190c000-0000-7000-8000-000000000007")
	halID := mustUUID("0190c000-0000-7000-8000-000000000008")
	idaID := mustUUID("0190c000-0000-7000-8000-000000000009")
	joID := mustUUID("0190c000-0000-7000-8000-00000000000a")
	nobodyID := mustUUID("0190c000-0000-7000-8000-00000000000b")
	joBeta := access.NewMembershipKey(accesstypes.DomainPolicyScope("beta"), User(joID), "Reader")

	store := alphaStore()
	for _, id := range []impulse.UUID{fayID, gusID, halID, idaID, joID, nobodyID} {
		store.memberships[access.NewMembershipKey(alphaScope, User(id), "Reader")] = true
	}
	store.memberships[access.NewMembershipKey(globalScope, User(fayID), "Steward")] = true
	store.memberships[access.NewMembershipKey(everyScope, User(gusID), "Reader")] = true
	store.memberships[joBeta] = true
	store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{joBeta: upcoming}
	store.groups[dayID] = &Group{Kind: "domain", Domain: "beta", Name: "Day shift"}
	store.groupMembers[memberKey(staffID, halID)], store.groupMembers[memberKey(dayID, idaID)] = true, true
	store.usernames[fayID], store.usernames[gusID], store.usernames[halID] = "fay", "gus", "hal"
	store.usernames[idaID], store.usernames[joID] = "ida", "jo"

	m, _ := newManager(t, store, engineRoles)
	var got map[string]int64
	var accounts []string
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		if got, err = kindCounts(ctx, txn, m.TenantKinds("alpha")); err != nil {
			return err
		}
		alone, err := (&tenantHeld{manager: m, domain: "alpha"}).accounts(ctx, txn)
		for i := range alone {
			accounts = append(accounts, alone[i].username)
		}

		return err
	}); err != nil {
		t.Fatalf("inTransaction() error = %v", err)
	}
	want := map[string]int64{"accounts": 3, "roles held": 11, "roles held on records": 0, "groups": 1, "custom roles": 1, "invitations": 1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("counts (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"anna", "ben", "dora"}, accounts); diff != "" {
		t.Errorf("the accounts the delete takes (-want +got):\n%s", diff)
	}

	var words []resource.DeleteWords
	for _, kind := range m.TenantKinds("alpha") {
		words = append(words, kind.Words())
	}
	wantWords := []resource.DeleteWords{
		{One: "account", Many: "accounts"},
		{One: "role held", Many: "roles held"},
		{One: "role held on a record", Many: "roles held on records"},
		{One: "group", Many: "groups"},
		{One: "custom role", Many: "custom roles"},
		{One: "invitation", Many: "invitations"},
	}
	if diff := cmp.Diff(wantWords, words); diff != "" {
		t.Errorf("the kinds' words, in the order returned (-want +got):\n%s", diff)
	}
}

// TestManager_TenantKinds_readsNoPersonAlone pins that the accounts kind reads the
// tenant's people and what each holds once (Store.TenantHoldings) and nothing of one
// person: with each of the store's reads of one person failing in turn, Count and Check
// answer as they do with none failing, and Take takes every account and buffers the
// writes TestManager_TenantKinds_takeEverything wants of them, in its order.
func TestManager_TenantKinds_readsNoPersonAlone(t *testing.T) {
	t.Parallel()

	for _, call := range []string{
		"Memberships", "UserGroups", "RecordMemberships", "Username",
		"Identities", "LiveSessions", "ImpersonationsHeldBy", "Impersonation", "SessionExpired", "RowsWithUser",
	} {
		t.Run(call, func(t *testing.T) {
			t.Parallel()

			store := alphaStore()
			store.withUser = map[impulse.UUID]int{doraID: 2}
			store.failOn = call
			m, _ := newManager(t, store, engineRoles)
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				accounts := m.TenantKinds("alpha")[0]
				count, err := accounts.Count(ctx, txn)
				if err != nil {
					return errors.Wrap(err, "resource.TakenKind.Count()")
				}
				if count != 3 {
					t.Errorf("Count() = %d, want the 3 accounts that are alpha's alone", count)
				}
				if err := accounts.Check(ctx, txn); err != nil {
					return errors.Wrap(err, "resource.TakenKind.Check()")
				}
				rows, _, more, err := accounts.Take(ctx, txn, resource.DeleteMutationLimit)
				if err != nil {
					return errors.Wrap(err, "resource.TakenKind.Take()")
				}
				if rows != 3 || more {
					t.Errorf("Take() = %d rows, more %t, want the 3 accounts and none left", rows, more)
				}

				return nil
			}); err != nil {
				t.Fatalf("with %s failing: %v", call, err)
			}
			if diff := cmp.Diff(alphaAccountWrites(), store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// alphaAccountWrites are the writes of taking alpha's three accounts, in order: anna's
// membership, her session ended and her row; ben's membership, his place in the night
// group and his row; dora's session ended, her identity link and her row.
func alphaAccountWrites() []string {
	return []string{
		"delete membership " + membershipText(&annaAlpha),
		"end session " + annaLogin.String(),
		"delete user " + annaID.String() + " with its tenant",
		"delete membership " + membershipText(&benAlpha),
		"delete group member " + nightID.String() + " " + benID.String(),
		"delete user " + benID.String() + " with its tenant",
		"end session " + doraLogin.String(),
		"delete identity " + doraLink.String(),
		"delete user " + doraID.String() + " with its tenant",
	}
}

// TestManager_TenantKinds_directoriesAlone pins which accounts are the tenant's alone where
// people sign in through tenants' directories and hold nothing: lu, who signs in through
// alpha's directory and beta's, stays, since beta's directory signs her in too; max, who
// signs in through beta's directory alone, is none of alpha's people.
func TestManager_TenantKinds_directoriesAlone(t *testing.T) {
	t.Parallel()

	luID := mustUUID("0190c000-0000-7000-8000-00000000000f")
	maxID := mustUUID("0190c000-0000-7000-8000-000000000010")
	luLink := mustUUID("0190c000-0000-7000-8000-000000000026")
	maxLink := mustUUID("0190c000-0000-7000-8000-000000000027")
	store := alphaStore()
	store.usernames[luID], store.usernames[maxID] = "lu", "max"
	store.identities[luLink], store.identities[maxLink] = luID, maxID
	store.managing[luID], store.managing[maxID] = []accesstypes.Domain{"alpha", "beta"}, []accesstypes.Domain{"beta"}

	m, _ := newManager(t, store, engineRoles)
	var accounts []string
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		alone, err := (&tenantHeld{manager: m, domain: "alpha"}).accounts(ctx, txn)
		for i := range alone {
			accounts = append(accounts, alone[i].username)
		}

		return err
	}); err != nil {
		t.Fatalf("inTransaction() error = %v", err)
	}
	if diff := cmp.Diff([]string{"anna", "ben", "dora"}, accounts); diff != "" {
		t.Errorf("the accounts the delete takes (-want +got):\n%s", diff)
	}
}

// TestManager_TenantKinds_takeEverything pins a round with room for everything: the
// three accounts go whole, each with what it held in the tenant, its identity links and
// its sessions ended; cara's and evan's accounts stay, their memberships in alpha going
// with the roles held. A membership an account took is found and deleted again with the
// roles held, as every delete in a save finds the rows the database held before it. The
// group goes with everything under it, through the store's delete of a group that goes
// with its tenant and never its plain delete, then the custom roles of alpha and its
// invitation. The permission engine is told once, after the commit.
func TestManager_TenantKinds_takeEverything(t *testing.T) {
	t.Parallel()

	store := alphaStore()
	store.costs = TenantDeleteCosts{Membership: 1, Group: 1, GroupMember: 1, GroupRole: 1, Role: 1, Identity: 1, User: 1, Invitation: 3}
	store.withUser = map[impulse.UUID]int{doraID: 2}
	m, engine := newManager(t, store, engineRoles)
	var got []takenRows
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		for _, kind := range m.TenantKinds("alpha") {
			rows, mutations, more, err := kind.Take(ctx, txn, resource.DeleteMutationLimit)
			if err != nil {
				return errors.Wrap(err, "resource.TakenKind.Take()")
			}
			got = append(got, takenRows{Rows: rows, Mutations: mutations, More: more})
		}
		if engine.changed != 0 {
			t.Errorf("the engine was told %d time(s) before the commit", engine.changed)
		}

		return nil
	}); err != nil {
		t.Fatalf("Take() error = %v", err)
	}
	want := []takenRows{
		// anna: a membership and the user (2); ben: a membership, a place and the user
		// (3); dora: a link, the user and what the database removes with it (4); and each
		// the row of the audit log that records its delete.
		{Rows: 3, Mutations: 9 + 3*deletedRowMutations(t, m, "alpha")},
		{Rows: 5, Mutations: 5},
		// No role is held on alpha's records.
		{},
		// The group, its two members and its role.
		{Rows: 1, Mutations: 4},
		{Rows: 1, Mutations: 1},
		{Rows: 1, Mutations: 3},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Take() by kind (-want +got):\n%s", diff)
	}
	wantWrites := append(alphaAccountWrites(),
		"delete membership "+membershipText(&annaAlpha),
		"delete membership "+membershipText(&benAlpha),
		"delete membership "+membershipText(&caraAlpha),
		"delete membership "+membershipText(&evanAlpha),
		"delete group role "+groupRoleText(&staffInAlpha),
		"delete group "+nightID.String()+" with its tenant",
		"delete the custom roles of alpha",
		"consume mail link "+invited.String(),
	)
	if diff := cmp.Diff(wantWrites, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
	}
	// Each account taken is one row, deleted with its tenant, in the tenant; the roles,
	// the group and the invitation that go with the tenant have none.
	deleted := func(id impulse.UUID, username string) auditlog.Record {
		return auditlog.Record{
			Act: auditlog.ActAccountDeleted, Form: auditlog.FormWithTenant, Source: eventsource.ProcessEvent(auditlog.ProcessApplication),
			SubjectKind: auditlog.SubjectAccount, SubjectID: id.String(), SubjectName: username, TenantID: "alpha", TenantName: "alpha",
		}
	}
	wantRows := []auditlog.Record{deleted(annaID, "anna"), deleted(benID, "ben"), deleted(doraID, "dora")}
	if diff := cmp.Diff(wantRows, store.audit, cmp.AllowUnexported(eventsource.EventSource{})); diff != "" {
		t.Errorf("audit rows (-want +got):\n%s", diff)
	}
}

// TestAuditRecordMutations pins what the row of the audit log that records an account
// deleted with its tenant costs: one for each column the row sets (auditlog.Record.Columns),
// one for the time of the commit, and one for each of the table's indexes, as the table's
// statements declare them. Written by a person in a session it sets twelve columns, four
// of them of who acted; written where no session is, as the application's own act, nine,
// one of who acted. Neither costs more than the table's columns and indexes.
func TestAuditRecordMutations(t *testing.T) {
	t.Parallel()

	indexes := 0
	for _, stmt := range auditlog.SpannerDDL("SessionAuditRecords", auditlog.KeepForever())[1:] {
		if strings.HasPrefix(stmt, "CREATE INDEX ") || strings.HasPrefix(stmt, "CREATE NULL_FILTERED INDEX ") {
			indexes++
		}
	}
	if indexes != auditRecordIndexes {
		t.Errorf("auditRecordIndexes = %d, want the %d indexes the table declares", auditRecordIndexes, indexes)
	}

	m, _ := newManager(t, alphaStore(), engineRoles)
	for _, tt := range []struct {
		name string
		ctx  context.Context
		want int
	}{
		{name: "by a person in a session", ctx: recordCaller(accesstypes.DomainScope("alpha")), want: 15},
		{name: "where no session is", ctx: t.Context(), want: 12},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			kind := &tenantAccounts{&tenantHeld{manager: m, domain: "alpha"}}
			got, err := auditRecordMutations(kind.deleted(tt.ctx, &tenantAccount{id: annaID, username: "anna"}))
			if err != nil {
				t.Fatalf("auditRecordMutations() error = %v", err)
			}
			if got != tt.want || got > len(auditlog.Columns())+indexes {
				t.Errorf("auditRecordMutations() = %d, want %d, no more than the %d columns and %d indexes the table declares", got, tt.want, len(auditlog.Columns()), indexes)
			}
		})
	}
}

// deletedRowMutations is what the row of the audit log that records an account's delete
// with the tenant costs, built as the accounts kind builds it where no session is
// (tenantAccounts.deleted): each account's row costs the same.
func deletedRowMutations(t *testing.T, m *Manager, domain accesstypes.Domain) int {
	t.Helper()

	kind := &tenantAccounts{&tenantHeld{manager: m, domain: domain}}
	mutations, err := auditRecordMutations(kind.deleted(t.Context(), &tenantAccount{id: annaID, username: "anna"}))
	if err != nil {
		t.Fatalf("auditRecordMutations() error = %v", err)
	}

	return mutations
}

// TestManager_TenantKinds_takeInRounds pins the accounts kind in a save with room for one
// account: it takes the first whole and says accounts are left. The next save, once the
// first has committed, counts the two left and takes the next.
func TestManager_TenantKinds_takeInRounds(t *testing.T) {
	t.Parallel()

	costs := TenantDeleteCosts{Membership: 1, GroupMember: 1, Identity: 1, User: 1}
	first := alphaStore()
	first.costs = costs
	first.withUser = map[impulse.UUID]int{annaID: 2}
	m, _ := newManager(t, first, engineRoles)
	// anna costs her membership, her user, the two rows the database removes with it and
	// the row of the audit log that records her delete.
	budget := 4 + deletedRowMutations(t, m, "alpha")
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		accounts := m.TenantKinds("alpha")[0]
		rows, mutations, more, err := accounts.Take(ctx, txn, budget)
		if err != nil {
			return errors.Wrap(err, "resource.TakenKind.Take()")
		}
		if got, want := (takenRows{Rows: rows, Mutations: mutations, More: more}), (takenRows{Rows: 1, Mutations: budget, More: true}); got != want {
			t.Errorf("the first save's Take() = %+v, want %+v", got, want)
		}

		return nil
	}); err != nil {
		t.Fatalf("the first save: %v", err)
	}
	wantFirst := []string{
		"delete membership " + membershipText(&annaAlpha),
		"end session " + annaLogin.String(),
		"delete user " + annaID.String() + " with its tenant",
	}
	if diff := cmp.Diff(wantFirst, first.writes); diff != "" {
		t.Errorf("the first save's writes (-want +got):\n%s", diff)
	}

	// ben costs his membership, his place, his user and his row of the audit log, and
	// dora more than is left.
	next := alphaStore(annaID)
	next.costs = costs
	m, _ = newManager(t, next, engineRoles)
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		accounts := m.TenantKinds("alpha")[0]
		count, err := accounts.Count(ctx, txn)
		if err != nil {
			return errors.Wrap(err, "resource.TakenKind.Count()")
		}
		if count != 2 {
			t.Errorf("the next save's Count() = %d, want the 2 accounts left", count)
		}
		rows, mutations, more, err := accounts.Take(ctx, txn, budget)
		if err != nil {
			return errors.Wrap(err, "resource.TakenKind.Take()")
		}
		if got, want := (takenRows{Rows: rows, Mutations: mutations, More: more}), (takenRows{Rows: 1, Mutations: 3 + deletedRowMutations(t, m, "alpha"), More: true}); got != want {
			t.Errorf("the next save's Take() = %+v, want %+v", got, want)
		}

		return nil
	}); err != nil {
		t.Fatalf("the next save: %v", err)
	}
	wantNext := []string{
		"delete membership " + membershipText(&benAlpha),
		"delete group member " + nightID.String() + " " + benID.String(),
		"delete user " + benID.String() + " with its tenant",
	}
	if diff := cmp.Diff(wantNext, next.writes); diff != "" {
		t.Errorf("the next save's writes (-want +got):\n%s", diff)
	}
}

// TestManager_TenantKinds_otherKindsInRounds pins the other kinds where the save has less
// room than they need: each takes what fits in key order and says rows are left, the
// custom roles go together or not at all, and a group its own delete could not take in
// one save leaves the kind with rows.
func TestManager_TenantKinds_otherKindsInRounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		kind       int
		budget     int
		groupLeft  *resource.DeleteLeft
		want       takenRows
		wantWrites []string
	}{
		{
			name: "roles held: two of five", kind: 1, budget: 2,
			want: takenRows{Rows: 2, Mutations: 2, More: true},
			wantWrites: []string{
				"delete membership " + membershipText(&annaAlpha),
				"delete membership " + membershipText(&benAlpha),
			},
		},
		{
			name: "groups: no room for the group with its members and its role, which it takes in part", kind: 3, budget: 3,
			want: takenRows{Mutations: 3, More: true},
			wantWrites: []string{
				"delete group member " + nightID.String() + " " + benID.String(),
				"delete group member " + nightID.String() + " " + caraID.String(),
				"delete group role " + groupRoleText(&nightInAlpha),
			},
		},
		{
			name: "groups: the group's own delete left rows under it", kind: 3, budget: 4,
			groupLeft:  &resource.DeleteLeft{Deleted: 2, Left: 1},
			want:       takenRows{Mutations: 4, More: true},
			wantWrites: []string{"delete group " + nightID.String() + " with its tenant"},
		},
		{
			name: "custom roles: no room", kind: 4, budget: 0,
			want: takenRows{More: true},
		},
		{
			name: "invitations: no room for one", kind: 5, budget: 2,
			want: takenRows{More: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := alphaStore()
			store.costs = TenantDeleteCosts{Membership: 1, Group: 1, GroupMember: 1, GroupRole: 1, Role: 1, Invitation: 3}
			store.groupLeft = tt.groupLeft
			m, _ := newManager(t, store, engineRoles)
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				rows, mutations, more, err := m.TenantKinds("alpha")[tt.kind].Take(ctx, txn, tt.budget)
				if err != nil {
					return errors.Wrap(err, "resource.TakenKind.Take()")
				}
				if got := (takenRows{Rows: rows, Mutations: mutations, More: more}); got != tt.want {
					t.Errorf("Take() = %+v, want %+v", got, tt.want)
				}

				return nil
			}); err != nil {
				t.Fatalf("Take() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_TenantKinds_sessionsCost pins what an account costs with the ending of its
// sessions: anna, the tenant's alone, has two live sessions, in one of which she acts as
// someone else, and one session that ended before. Her cost is her membership and her
// row, two endings of a session and one of an impersonation record; the ended session
// costs nothing. With a budget one mutation short of it she is not taken and the kind
// says accounts are left; with the budget exact she is.
func TestManager_TenantKinds_sessionsCost(t *testing.T) {
	t.Parallel()

	acting := mustUUID("0190c000-0000-7000-8000-000000000041")
	ended := mustUUID("0190c000-0000-7000-8000-000000000042")
	// The membership and the row (2), two session endings (10), a record's (7) and the
	// row of the audit log that records the delete.
	counting, _ := newManager(t, alphaStore(), engineRoles)
	cost := 2 + 2*5 + 7 + deletedRowMutations(t, counting, "alpha")
	for _, tt := range []struct {
		name   string
		budget int
		want   takenRows
	}{
		{name: "one mutation short", budget: cost - 1, want: takenRows{More: true}},
		{name: "exact", budget: cost, want: takenRows{Rows: 1, Mutations: cost, More: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := alphaStore()
			store.costs = TenantDeleteCosts{Membership: 1, User: 1, SessionEnd: 5, ImpersonationEnd: 7}
			store.sessions[annaID] = []impulse.UUID{annaLogin, acting}
			store.records = map[impulse.UUID]bool{acting: true}
			store.ended = map[impulse.UUID]bool{ended: true}
			m, _ := newManager(t, store, engineRoles)
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				rows, mutations, more, err := m.TenantKinds("alpha")[0].Take(ctx, txn, tt.budget)
				if err != nil {
					return errors.Wrap(err, "resource.TakenKind.Take()")
				}
				if got := (takenRows{Rows: rows, Mutations: mutations, More: more}); got != tt.want {
					t.Errorf("Take() = %+v, want %+v", got, tt.want)
				}

				return nil
			}); err != nil {
				t.Fatalf("Take() error = %v", err)
			}
		})
	}
}

// TestManager_TenantKinds_aGroupInParts pins the groups kind with a group that costs more
// than a save has room for: each save takes what fits of it, its members and then its
// role, one at a time, answering no row, the mutations spent and that rows are left, and
// the engine is told at each commit; the save in which what is left of the group fits
// deletes it through the store's delete of a group that goes with its tenant
// (DeleteGroupWithTenant), once, and says nothing is left. No member or role is deleted
// twice.
func TestManager_TenantKinds_aGroupInParts(t *testing.T) {
	t.Parallel()

	store := alphaStore()
	store.costs = TenantDeleteCosts{Group: 1, GroupMember: 1, GroupRole: 1}
	m, engine := newManager(t, store, engineRoles)
	var got []takenRows
	for save := 1; save <= 5; save++ {
		before := len(store.writes)
		var more bool
		if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			rows, mutations, left, err := m.TenantKinds("alpha")[3].Take(ctx, txn, 1)
			if err != nil {
				return errors.Wrap(err, "resource.TakenKind.Take()")
			}
			got = append(got, takenRows{Rows: rows, Mutations: mutations, More: left})
			more = left

			return nil
		}); err != nil {
			t.Fatalf("save %d: Take() error = %v", save, err)
		}
		if engine.changed != save {
			t.Errorf("save %d: the engine was told the policy changed %d time(s), want %d", save, engine.changed, save)
		}
		// What the save wrote is gone once it commits.
		for _, write := range store.writes[before:] {
			for key := range store.groupMembers {
				if write == "delete group member "+string(key.Group)+" "+string(key.User) {
					delete(store.groupMembers, key)
				}
			}
			for key := range store.groupRoles {
				if write == "delete group role "+groupRoleText(&key) {
					delete(store.groupRoles, key)
				}
			}
		}
		if !more {
			break
		}
	}
	want := []takenRows{
		{Mutations: 1, More: true},
		{Mutations: 1, More: true},
		{Mutations: 1, More: true},
		// The group's own row, with nothing left under it.
		{Rows: 1, Mutations: 1},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Take() by save (-want +got):\n%s", diff)
	}
	wantWrites := []string{
		"delete group member " + nightID.String() + " " + benID.String(),
		"delete group member " + nightID.String() + " " + caraID.String(),
		"delete group role " + groupRoleText(&nightInAlpha),
		"delete group " + nightID.String() + " with its tenant",
	}
	if diff := cmp.Diff(wantWrites, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
}

// TestManager_TenantKinds_theImpersonatorsOwnAccount holds the tenant's delete to the rule
// that nobody is the subject of what they do in a session that acts as someone else. anna
// holds her roles in alpha alone, so the delete would delete her account: while she acts
// as someone else, the accounts kind refuses before anything is buffered, in the rule's
// sentence, both where the delete asks first and where it takes. A session started by evan,
// whose account stays, is refused nothing.
func TestManager_TenantKinds_theImpersonatorsOwnAccount(t *testing.T) {
	t.Parallel()

	const refusal = "You are acting as someone else, so you cannot delete the only tenant your own account holds a role in."
	for _, tt := range []struct {
		name        string
		actor       impulse.UUID
		wantRefused bool
	}{
		{name: "the person who started the session holds roles in the tenant alone: refused", actor: annaID, wantRefused: true},
		{name: "the person who started the session keeps their account: not refused", actor: evanID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := alphaStore()
			m, _ := newManager(t, store, engineRoles)
			var checked, taken error
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = viewedBy(ctx, tt.actor)
				accounts := m.TenantKinds("alpha")[0]
				checked = accounts.Check(ctx, txn)
				if tt.wantRefused {
					_, _, _, taken = accounts.Take(ctx, txn, resource.DeleteMutationLimit)
				}

				return resource.ErrDryRun
			})
			if !errors.Is(err, resource.ErrDryRun) {
				t.Fatalf("inTransaction() error = %v, want the dry run's", err)
			}
			if !tt.wantRefused {
				if checked != nil {
					t.Fatalf("Check() error = %v, want none", checked)
				}

				return
			}
			for call, err := range map[string]error{"Check()": checked, "Take()": taken} {
				if !httpio.HasForbidden(err) || httpio.Message(err) != refusal {
					t.Errorf("%s error = %v, want the refusal %q", call, err, refusal)
				}
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none", store.writes)
			}
		})
	}
}

// TestManager_TenantKinds_nothingHeld pins a tenant the auth holds nothing for: every count
// is zero, every kind takes nothing and says nothing is left, and the engine is not told.
func TestManager_TenantKinds_nothingHeld(t *testing.T) {
	t.Parallel()

	store := alphaStore()
	store.costs = TenantDeleteCosts{Membership: 1, Group: 1, GroupMember: 1, GroupRole: 1, Role: 1, Identity: 1, User: 1, Invitation: 1}
	m, engine := newManager(t, store, engineRoles)
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		kinds := m.TenantKinds("gamma")
		want := map[string]int64{"accounts": 0, "roles held": 0, "roles held on records": 0, "groups": 0, "custom roles": 0, "invitations": 0}
		got, err := kindCounts(ctx, txn, kinds)
		if err != nil {
			return err
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("counts (-want +got):\n%s", diff)
		}
		for _, kind := range kinds {
			rows, mutations, more, err := kind.Take(ctx, txn, resource.DeleteMutationLimit)
			if err != nil {
				return errors.Wrap(err, "resource.TakenKind.Take()")
			}
			if got := (takenRows{Rows: rows, Mutations: mutations, More: more}); got != (takenRows{}) {
				t.Errorf("%s: Take() = %+v, want nothing", kind.Words().Many, got)
			}
		}

		return nil
	}); err != nil {
		t.Fatalf("inTransaction() error = %v", err)
	}
	if len(store.writes) != 0 {
		t.Errorf("buffered writes = %v, want none", store.writes)
	}
	if engine.changed != 0 {
		t.Errorf("the engine was told the policy changed %d time(s), want never", engine.changed)
	}
}
