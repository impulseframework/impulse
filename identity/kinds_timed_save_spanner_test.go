package identity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
)

// timedSaveEmulatorVersion pins the emulator image the timed save test runs against: the
// version the resource package's tests pin.
const timedSaveEmulatorVersion = "1.5.56"

// savedRecord is a record of the timed save test's own table whose description lists
// nothing under it: what its delete takes is the kind the test hands it.
type savedRecord struct {
	ID string `spanner:"Id"`
}

func (savedRecord) Resource() accesstypes.Resource {
	return "SavedRecords"
}

func (savedRecord) DeleteTree() *resource.DeleteTree {
	return &resource.DeleteTree{Record: "SavedRecords", Words: resource.DeleteWords{One: "record", Many: "records"}, Key: []string{"Id"}}
}

// timedSaveDatabase is a database of the fixture's table on an emulator of the test's own,
// both gone when the test ends.
func timedSaveDatabase(t *testing.T) *spanner.Client {
	t.Helper()

	if testing.Short() {
		t.Skip("requires the Spanner emulator")
	}
	container, err := initiator.NewSpannerContainer(context.Background(), timedSaveEmulatorVersion)
	if err != nil {
		t.Fatalf("initiator.NewSpannerContainer() error = %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Error(err)
		}
		if err := container.Close(); err != nil {
			t.Error(err)
		}
	})
	db, err := container.CreateDatabase(context.Background(), "kinds-timed-save")
	if err != nil {
		t.Fatalf("initiator.SpannerContainer.CreateDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropDatabase(context.Background()); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.MigrateUp("file://testdata/timedsave/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}

	return db.Client
}

// TestManager_kindsInATimedSave holds, over the emulator, each kind of the auth's rows
// whose Take takes its rows one after another, handed alone to the delete of a record in
// a save whose time limit has passed (resource.WithDeleteSaveTime of a nanosecond): it
// takes its first row, or the first row of a group too large for the save, asks
// resource.DeleteSaveOver before the next and stops, saying rows are left. The record
// stands, and the save answers the rows the kind counts as taken (resource.DeleteLeft).
// Each case starts where the loop it holds is the one that runs.
func TestManager_kindsInATimedSave(t *testing.T) { //nolint:tparallel // the cases run one after another, as the comment at their loop says
	t.Parallel()

	db := timedSaveDatabase(t)
	client := resource.NewSpannerClient(db, resource.WithDeleteSaveTime(time.Nanosecond))

	staffAuditsAlpha := access.NewGroupRoleKey(GroupID(staffID), alphaScope, "Auditor")
	nightAuditsAlpha := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Auditor")
	nightOnP1 := access.NewRecordGroupRoleKey(GroupID(nightID), onP1, "ProjectViewer")
	nightOnP2 := access.NewRecordGroupRoleKey(GroupID(nightID), onP2, "ProjectViewer")
	staffOnP1 := access.NewRecordGroupRoleKey(GroupID(staffID), onP1, "ProjectViewer")
	staffOnP2 := access.NewRecordGroupRoleKey(GroupID(staffID), onP2, "ProjectViewer")
	alsoInvited := mustUUID("0190c000-0000-7000-8000-000000000033")
	tenantKind := func(i int) func(m *Manager) resource.TakenKind {
		return func(m *Manager) resource.TakenKind {
			return m.TenantKinds("alpha")[i]
		}
	}
	tests := []struct {
		name       string
		store      func() *fakeStore
		kind       func(m *Manager) resource.TakenKind
		wantTaken  int64
		wantWrites []string
	}{
		{
			name: "the tenant's accounts: anna, of anna, ben and dora",
			store: func() *fakeStore {
				store := alphaStore()
				store.costs = TenantDeleteCosts{Membership: 1, GroupMember: 1, Identity: 1, User: 1}

				return store
			},
			kind:      tenantKind(0),
			wantTaken: 1,
			wantWrites: []string{
				"delete membership " + membershipText(&annaAlpha),
				"end session " + annaLogin.String(),
				"delete user " + annaID.String() + " with its tenant",
			},
		},
		{
			name: "the roles held in the tenant, the people's: anna's, of four",
			store: func() *fakeStore {
				store := alphaStore()
				store.costs = TenantDeleteCosts{Membership: 1, GroupRole: 1}

				return store
			},
			kind:       tenantKind(1),
			wantTaken:  1,
			wantWrites: []string{"delete membership " + membershipText(&annaAlpha)},
		},
		{
			name: "the roles held in the tenant, the groups': the staff group's Auditor, of its two",
			store: func() *fakeStore {
				store := alphaStore()
				store.memberships = map[access.MembershipKey]bool{caraBeta: true}
				store.groupRoles[staffAuditsAlpha] = true
				store.costs = TenantDeleteCosts{Membership: 1, GroupRole: 1}

				return store
			},
			kind:       tenantKind(1),
			wantTaken:  1,
			wantWrites: []string{"delete group role " + groupRoleText(&staffAuditsAlpha)},
		},
		{
			name: "the roles on the tenant's records, the groups': the staff group's on p1, of its two",
			store: func() *fakeStore {
				store := alphaStore()
				store.recordGroupRoles = map[access.RecordGroupRoleKey]accesstypes.Period{staffOnP1: {}, staffOnP2: {}}
				store.costs = TenantDeleteCosts{RecordMembership: 1, RecordGroupRole: 1}

				return store
			},
			kind:       tenantKind(2),
			wantTaken:  1,
			wantWrites: []string{"delete record group role " + recordGroupRoleText(&staffOnP1)},
		},
		{
			name: "the tenant's groups: night, of night and day",
			store: func() *fakeStore {
				store := alphaStore()
				store.groups[dayID] = &Group{Kind: "domain", Domain: "alpha", Name: "Day shift"}
				store.costs = TenantDeleteCosts{Group: 1, GroupMember: 1, GroupRole: 1}

				return store
			},
			kind:       tenantKind(3),
			wantTaken:  1,
			wantWrites: []string{"delete group " + nightID.String() + " with its tenant"},
		},
		{
			name: "a group too large for the save, taken in part, its members: ben, of ben and cara",
			store: func() *fakeStore {
				store := alphaStore()
				store.costs = TenantDeleteCosts{Group: 30000, GroupMember: 30000, GroupRole: 1}

				return store
			},
			kind:       tenantKind(3),
			wantWrites: []string{"delete group member " + nightID.String() + " " + benID.String()},
		},
		{
			name: "a group too large for the save, taken in part, its roles: its Auditor, of its two",
			store: func() *fakeStore {
				store := alphaStore()
				store.groupMembers = map[access.GroupMemberKey]bool{}
				store.groupRoles[nightAuditsAlpha] = true
				store.costs = TenantDeleteCosts{Group: 30000, GroupRole: 30000}

				return store
			},
			kind:       tenantKind(3),
			wantWrites: []string{"delete group role " + groupRoleText(&nightAuditsAlpha)},
		},
		{
			name: "a group too large for the save, taken in part, its roles on records: on p1, of its two",
			store: func() *fakeStore {
				store := alphaStore()
				store.groupMembers = map[access.GroupMemberKey]bool{}
				delete(store.groupRoles, nightInAlpha)
				store.recordGroupRoles = map[access.RecordGroupRoleKey]accesstypes.Period{nightOnP1: {}, nightOnP2: {}}
				store.costs = TenantDeleteCosts{Group: 30000, RecordGroupRole: 30000}

				return store
			},
			kind:       tenantKind(3),
			wantWrites: []string{"delete record group role " + recordGroupRoleText(&nightOnP1)},
		},
		{
			name: "the tenant's invitations: the first, of two",
			store: func() *fakeStore {
				store := alphaStore()
				store.invitations["alpha"] = []impulse.UUID{alsoInvited, invited}
				store.costs = TenantDeleteCosts{Invitation: 1}

				return store
			},
			kind:       tenantKind(5),
			wantTaken:  1,
			wantWrites: []string{"consume mail link " + invited.String()},
		},
		{
			name:  "the roles held on a record, the people's: dana's on p1, of dana's and erin's",
			store: p1Store,
			kind: func(m *Manager) resource.TakenKind {
				return m.RecordKinds(onP1)[0]
			},
			wantTaken:  1,
			wantWrites: []string{"delete record membership " + recordMembershipText(&danaOnP1)},
		},
	}
	// The emulator runs one read-write transaction at a time, so the cases run one after
	// another, each on a record of its own.
	for i, tt := range tests { //nolint:paralleltest // one after another, as the comment above says
		t.Run(tt.name, func(t *testing.T) {
			id := fmt.Sprintf("record-%d", i+1)
			if _, err := db.Apply(t.Context(), []*spanner.Mutation{spanner.InsertMap("SavedRecords", map[string]any{"Id": id})}); err != nil {
				t.Fatalf("spanner.Client.Apply() error = %v", err)
			}
			store := tt.store()
			m, _ := newManager(t, store, engineRoles)
			var left *resource.DeleteLeft
			if err := client.ExecuteFunc(t.Context(), func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				// A transaction the database runs again starts from nothing.
				store.writes = nil
				record := resource.NewPatchSet(resource.NewMetadata[savedRecord]()).SetPatchType(resource.DeletePatchType).
					DeleteAlso(tt.kind(m)).DeleteEverythingUnder()
				record.SetKey("ID", id)
				if err := record.Buffer(ctx, txn, eventsource.ProcessEvent("test")); err != nil {
					return errors.Wrap(err, "resource.PatchSet.Buffer()")
				}
				left = record.DeleteLeft()

				return nil
			}); err != nil {
				t.Fatalf("the save: error = %v", err)
			}
			if diff := cmp.Diff(&resource.DeleteLeft{Deleted: tt.wantTaken}, left); diff != "" {
				t.Errorf("DeleteLeft() (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("the save's writes (-want +got):\n%s", diff)
			}
			row, err := db.Single().ReadRow(t.Context(), "SavedRecords", spanner.Key{id}, []string{"Id"})
			if err != nil || row == nil {
				t.Errorf("the record is gone: ReadRow() error = %v", err)
			}
		})
	}
}
