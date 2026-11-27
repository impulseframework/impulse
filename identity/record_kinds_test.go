package identity

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// The records of these tests' roles: projects p1 and p2 of alpha, and q1 of beta.
var (
	onP1 = accesstypes.NewRecordScope("alpha", projects, "p1")
	onP2 = accesstypes.NewRecordScope("alpha", projects, "p2")
	onQ1 = accesstypes.NewRecordScope("beta", projects, "q1")
)

// p1Store is the store of the groups (groupStore) holding the roles on p1 that these tests
// take, dana's, erin's whose period has ended and the night group's, beside dana's on p2,
// the night group's on p2 and dana's on q1, which are no roles on p1.
func p1Store() *fakeStore {
	store := groupStore()
	store.recordRoles = map[access.RecordMembershipKey]accesstypes.Period{
		danaOnP1: {},
		access.NewRecordMembershipKey(onP1, erin, "ProjectViewer"): {End: time.Now().Add(-time.Hour)},
		danaOnP2: {},
		danaOnQ1: {},
	}
	store.recordGroupRoles = map[access.RecordGroupRoleKey]accesstypes.Period{
		access.NewRecordGroupRoleKey(GroupID(nightID), onP1, "ProjectViewer"): {},
		access.NewRecordGroupRoleKey(GroupID(nightID), onP2, "ProjectViewer"): {},
	}
	store.costs = TenantDeleteCosts{RecordMembership: 1, RecordGroupRole: 2}

	return store
}

// TestManager_RecordKinds_counts pins what a record's delete counts of the roles held on
// it: one kind, in its words, counting the people's and the groups' roles on that record,
// an ended one among them, and none on another record of its tenant or of another.
func TestManager_RecordKinds_counts(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, p1Store(), engineRoles)
	kinds := m.RecordKinds(onP1)
	var words []resource.DeleteWords
	for _, kind := range kinds {
		words = append(words, kind.Words())
	}
	if diff := cmp.Diff([]resource.DeleteWords{{One: "role held on it", Many: "roles held on it"}}, words); diff != "" {
		t.Errorf("the kinds' words (-want +got):\n%s", diff)
	}
	var got map[string]int64
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		got, err = kindCounts(ctx, txn, kinds)

		return err
	}); err != nil {
		t.Fatalf("inTransaction() error = %v", err)
	}
	if diff := cmp.Diff(map[string]int64{"roles held on it": 3}, got); diff != "" {
		t.Errorf("counts (-want +got):\n%s", diff)
	}
}

// TestManager_RecordKinds_take pins the kind's Take within a save's budget: the people's
// roles on the record in key order, then the groups', while the next one's cost fits,
// answering the rows taken, their cost and whether rows are left; nothing on another
// record is taken. The permission engine is told once, after the commit, where any row
// went, and not at all where none did. The kind asks nothing: the record's delete asks its
// own permission.
func TestManager_RecordKinds_take(t *testing.T) {
	t.Parallel()

	erinOnP1 := access.NewRecordMembershipKey(onP1, erin, "ProjectViewer")
	nightOnP1 := access.NewRecordGroupRoleKey(GroupID(nightID), onP1, "ProjectViewer")
	tests := []struct {
		name        string
		budget      int
		want        takenRows
		wantWrites  []string
		wantChanged int
	}{
		{
			name: "room for every role", budget: resource.DeleteMutationLimit,
			want: takenRows{Rows: 3, Mutations: 4},
			wantWrites: []string{
				"delete record membership " + recordMembershipText(&danaOnP1),
				"delete record membership " + recordMembershipText(&erinOnP1),
				"delete record group role " + recordGroupRoleText(&nightOnP1),
			},
			wantChanged: 1,
		},
		{
			name: "room for the people's roles and not the group's", budget: 3,
			want: takenRows{Rows: 2, Mutations: 2, More: true},
			wantWrites: []string{
				"delete record membership " + recordMembershipText(&danaOnP1),
				"delete record membership " + recordMembershipText(&erinOnP1),
			},
			wantChanged: 1,
		},
		{
			name: "no room for one", budget: 0,
			want: takenRows{More: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := p1Store()
			m, engine := newManager(t, store, engineRoles)
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				kind := m.RecordKinds(onP1)[0]
				if err := kind.Check(ctx, txn); err != nil {
					return errors.Wrap(err, "resource.TakenKind.Check()")
				}
				rows, mutations, more, err := kind.Take(ctx, txn, tt.budget)
				if err != nil {
					return errors.Wrap(err, "resource.TakenKind.Take()")
				}
				if got := (takenRows{Rows: rows, Mutations: mutations, More: more}); got != tt.want {
					t.Errorf("Take() = %+v, want %+v", got, tt.want)
				}
				if engine.changed != 0 {
					t.Errorf("the engine was told %d time(s) before the commit", engine.changed)
				}

				return nil
			}); err != nil {
				t.Fatalf("inTransaction() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told the policy changed %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// The roles held on records in the tests of a tenant's kinds (alphaStore): anna's on
// alpha's p1, cara's on beta's q1, the night group's on p1 (night is alpha's own group),
// and the staff group's on alpha's p2 (staff is kept for the whole application).
var (
	annaOnP1  = access.NewRecordMembershipKey(onP1, User(annaID), "ProjectEditor")
	caraOnQ1  = access.NewRecordMembershipKey(onQ1, User(caraID), "ProjectViewer")
	nightOnP1 = access.NewRecordGroupRoleKey(GroupID(nightID), onP1, "ProjectViewer")
	staffOnP2 = access.NewRecordGroupRoleKey(GroupID(staffID), onP2, "ProjectViewer")
)

// alphaRecordStore is alphaStore with the roles above held on records.
func alphaRecordStore() *fakeStore {
	store := alphaStore()
	store.recordRoles = map[access.RecordMembershipKey]accesstypes.Period{annaOnP1: {}, caraOnQ1: {}}
	store.recordGroupRoles = map[access.RecordGroupRoleKey]accesstypes.Period{nightOnP1: {}, staffOnP2: {}}

	return store
}

// recordWrites are the writes of the log that remove a role held on a record, in order.
func recordWrites(writes []string) []string {
	return slices.DeleteFunc(slices.Clone(writes), func(write string) bool { return !strings.HasPrefix(write, "delete record ") })
}

// TestManager_TenantKinds_recordRoles pins what a tenant's delete counts and takes of the
// roles held on its records: a kind of their own, counting anna's role on p1 and the staff
// group's on p2 and taking them after the accounts, anna's though her account went first;
// cara's role on beta's record is not counted or taken. The night group's role on p1 goes
// with the group, through the store's delete of a group that goes with its tenant, whose
// cost it adds to, and is not counted or taken by the kind, so no row is deleted twice.
func TestManager_TenantKinds_recordRoles(t *testing.T) {
	t.Parallel()

	store := alphaRecordStore()
	store.costs = TenantDeleteCosts{
		Membership: 1, Group: 1, GroupMember: 1, GroupRole: 1, Role: 1, Identity: 1, User: 1, Invitation: 3, RecordMembership: 1, RecordGroupRole: 2,
	}
	m, engine := newManager(t, store, engineRoles)
	var counts map[string]int64
	var got []takenRows
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		if counts, err = kindCounts(ctx, txn, m.TenantKinds("alpha")); err != nil {
			return err
		}
		for _, kind := range m.TenantKinds("alpha") {
			rows, mutations, more, err := kind.Take(ctx, txn, resource.DeleteMutationLimit)
			if err != nil {
				return errors.Wrap(err, "resource.TakenKind.Take()")
			}
			got = append(got, takenRows{Rows: rows, Mutations: mutations, More: more})
		}

		return nil
	}); err != nil {
		t.Fatalf("inTransaction() error = %v", err)
	}
	wantCounts := map[string]int64{"accounts": 3, "roles held": 5, "roles held on records": 2, "groups": 1, "custom roles": 1, "invitations": 1}
	if diff := cmp.Diff(wantCounts, counts); diff != "" {
		t.Errorf("counts (-want +got):\n%s", diff)
	}
	want := []takenRows{
		// The three accounts, each with the row of the audit log that records its delete.
		{Rows: 3, Mutations: 7 + 3*deletedRowMutations(t, m)},
		{Rows: 5, Mutations: 5},
		// anna's role on p1 (1) and the staff group's on p2 (2).
		{Rows: 2, Mutations: 3},
		// The group, its two members, its role and its role on p1 (2).
		{Rows: 1, Mutations: 6},
		{Rows: 1, Mutations: 1},
		{Rows: 1, Mutations: 3},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Take() by kind (-want +got):\n%s", diff)
	}
	wantRecords := []string{
		"delete record membership " + recordMembershipText(&annaOnP1),
		"delete record group role " + recordGroupRoleText(&staffOnP2),
	}
	if diff := cmp.Diff(wantRecords, recordWrites(store.writes)); diff != "" {
		t.Errorf("the roles on records the kinds took (-want +got):\n%s", diff)
	}
	if !slices.Contains(store.writes, "delete group "+nightID.String()+" with its tenant") {
		t.Errorf("buffered writes = %v, want the night group deleted with its tenant", store.writes)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
	}
}

// TestManager_TenantKinds_aGroupsRecordRolesInPart pins a group whose cost the round has
// no room for, its role on a record among it: what fits is taken one row at a time, its
// members, then its role, then its role on p1, and the group's own row stays for a later
// round.
func TestManager_TenantKinds_aGroupsRecordRolesInPart(t *testing.T) {
	t.Parallel()

	store := alphaRecordStore()
	store.costs = TenantDeleteCosts{Group: 1, GroupMember: 1, GroupRole: 1, RecordGroupRole: 2}
	m, _ := newManager(t, store, engineRoles)
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		rows, mutations, more, err := m.TenantKinds("alpha")[3].Take(ctx, txn, 5)
		if err != nil {
			return errors.Wrap(err, "resource.TakenKind.Take()")
		}
		if got, want := (takenRows{Rows: rows, Mutations: mutations, More: more}), (takenRows{Mutations: 5, More: true}); got != want {
			t.Errorf("Take() = %+v, want %+v", got, want)
		}

		return nil
	}); err != nil {
		t.Fatalf("inTransaction() error = %v", err)
	}
	want := []string{
		"delete group member " + nightID.String() + " " + benID.String(),
		"delete group member " + nightID.String() + " " + caraID.String(),
		"delete group role " + groupRoleText(&nightInAlpha),
		"delete record group role " + recordGroupRoleText(&nightOnP1),
	}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
}

// TestManager_TenantKinds_recordRolesAlone pins which accounts are the tenant's alone where
// people hold roles on records: kim, who holds a role on alpha's p1 and nothing else, is
// alpha's alone; lee, who holds a role in alpha and one on beta's q1, stays; mo, who holds
// a role on beta's q1 alone, is none of alpha's people.
func TestManager_TenantKinds_recordRolesAlone(t *testing.T) {
	t.Parallel()

	kimID := mustUUID("0190c000-0000-7000-8000-00000000000c")
	leeID := mustUUID("0190c000-0000-7000-8000-00000000000d")
	moID := mustUUID("0190c000-0000-7000-8000-00000000000e")
	store := alphaStore()
	store.memberships[access.NewMembershipKey(alphaScope, User(leeID), "Reader")] = true
	store.recordRoles = map[access.RecordMembershipKey]accesstypes.Period{
		access.NewRecordMembershipKey(onP1, User(kimID), "ProjectEditor"): {},
		access.NewRecordMembershipKey(onQ1, User(leeID), "ProjectViewer"): {},
		access.NewRecordMembershipKey(onQ1, User(moID), "ProjectViewer"):  {},
	}
	store.usernames[kimID], store.usernames[leeID], store.usernames[moID] = "kim", "lee", "mo"

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
	if diff := cmp.Diff([]string{"anna", "ben", "dora", "kim"}, accounts); diff != "" {
		t.Errorf("the accounts the delete takes (-want +got):\n%s", diff)
	}
}
