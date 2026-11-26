package access

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// recordManager is the record tests' Client, its manager and its store, with the
// group crew kept in tenant1, the group staff kept for the whole application and the
// group others kept in tenant2.
func recordManager(t *testing.T) (*Client, UserManager, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	for _, group := range []*policy.GroupRow{
		{Group: policy.Group{ID: "crew", Scope: tenant1Policy}, Name: "Crew", Description: "Works the projects."},
		{Group: policy.Group{ID: "staff", Scope: globalPolicy}, Name: "Staff", Description: "The application's own people."},
		{Group: policy.Group{ID: "others", Scope: tenant2Policy}, Name: "Others", Description: "Another tenant's people."},
	} {
		if taken, err := store.InsertGroup(t.Context(), group); err != nil || taken {
			t.Fatalf("InsertGroup(%s) = (%v, %v)", group.ID, taken, err)
		}
	}
	client := recordClient(t, store)

	return client, client.UserManager(), store
}

// TestUserManager_recordRoles_refusals pins the one rule a role on a record is given
// under, by both gives and both key methods, each refusal a 400 in its words.
func TestUserManager_recordRoles_refusals(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, manager, _ := recordManager(t)

	backwards := accesstypes.Period{Start: hoursOn(2), End: hoursOn(1)}
	tests := []struct {
		name   string
		at     accesstypes.RecordScope
		user   accesstypes.User
		group  accesstypes.Group
		role   accesstypes.Role
		period accesstypes.Period
		want   string
	}{
		{name: "no tenant", at: accesstypes.NewRecordScope("", "Projects", "p-1"), user: "ann", role: "ProjectEditor", want: "No tenant is named. Name the tenant the record is in."},
		{name: "a kind no role is held on", at: accesstypes.NewRecordScope("tenant1", "Invoices", "i-1"), user: "ann", role: "ProjectEditor", want: "No role can be held on one Invoices. Give the role in the tenant instead."},
		{name: "no record", at: accesstypes.NewRecordScope("tenant1", "Projects", ""), user: "ann", role: "ProjectEditor", want: "No project is named. Name the project the role is held on."},
		{name: "a role not held on the kind", at: projectP1, user: "ann", role: "Reader", want: "Reader is not a role held on a project."},
		{name: "a role that is no default role", at: projectP1, user: "ann", role: "Steward", want: "Steward is not a role held on a project."},
		{name: "no person", at: projectP1, role: "ProjectEditor", want: "No person is named. Name the person the role is for."},
		{name: "no group", at: projectP1, group: "-", role: "ProjectEditor", want: "No group is named. Name the group the role is for."},
		{name: "a period that never counts", at: projectP1, user: "ann", role: "ProjectEditor", period: backwards, want: PeriodNeverCountsMessage},
		{name: "a group kept for the whole application", at: projectP1, group: "staff", role: "ProjectEditor", want: "A role on one project is given to a person or to a group of the project's tenant. This group is kept for the whole application."},
		{name: "a group kept in another tenant", at: projectP1, group: "others", role: "ProjectEditor", want: "A role on one project is given to a person or to a group of the project's tenant. This group is kept in another tenant."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			group := tt.group
			if group == "-" {
				group = ""
			}
			var err error
			if tt.group != "" {
				err = manager.AssignRecordGroupRole(ctx, group, tt.at, tt.role, tt.period)
			} else {
				err = manager.AssignRecordUserRole(ctx, tt.at, tt.user, tt.role, tt.period)
			}
			if err == nil || !httpio.HasBadRequest(err) || httpio.Message(err) != tt.want {
				t.Errorf("the give = %v (message %q), want a 400 saying %q", err, httpio.Message(err), tt.want)
			}
			if tt.period != (accesstypes.Period{}) {
				return
			}
			if tt.group != "" {
				kept := map[accesstypes.Group]accesstypes.PolicyScope{"staff": globalPolicy, "others": tenant2Policy}[group]
				_, err = manager.RecordGroupRole(ctx, group, kept, tt.at, tt.role)
			} else {
				_, err = manager.RecordMembership(ctx, tt.at, tt.user, tt.role)
			}
			if err == nil || !httpio.HasBadRequest(err) || httpio.Message(err) != tt.want {
				t.Errorf("the key = %v (message %q), want a 400 saying %q", err, httpio.Message(err), tt.want)
			}
		})
	}

	if err := manager.AssignRecordGroupRole(ctx, "gone", projectP1, "ProjectEditor", accesstypes.Period{}); err == nil || httpio.Message(err) != groupNotFoundMessage {
		t.Errorf("AssignRecordGroupRole() for a group that is not there = %v, want %q", err, groupNotFoundMessage)
	}
}

// TestUserManager_recordRoles pins giving, listing and taking a role on a record: a role
// given is listed with its period, a group's with the group's name; given again it is one
// row with the new period; taking what is not held is no error; removing a record's
// roles leaves another record's; the next check answers with the role given; and the
// key methods write nothing.
func TestUserManager_recordRoles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, manager, store := recordManager(t)
	projectP2 := accesstypes.NewRecordScope("tenant1", "Projects", "p-2")

	// ann's period began after the tests' noon and ends ten years on: it counts now.
	if err := manager.AssignRecordUserRole(ctx, projectP1, "ann", "ProjectEditor", between(1, 24*3650)); err != nil {
		t.Fatalf("AssignRecordUserRole() error = %v", err)
	}
	if err := manager.AssignRecordGroupRole(ctx, "crew", projectP1, "ProjectEditor", accesstypes.Period{}); err != nil {
		t.Fatalf("AssignRecordGroupRole() error = %v", err)
	}
	if err := manager.AssignRecordUserRole(ctx, projectP2, "bob", "ProjectEditor", accesstypes.Period{}); err != nil {
		t.Fatalf("AssignRecordUserRole() error = %v", err)
	}
	want := []RecordHolder{
		{User: "ann", Role: "ProjectEditor", Period: between(1, 24*3650)},
		{Group: "crew", GroupName: "Crew", Role: "ProjectEditor"},
	}
	holders, err := manager.RecordHolders(ctx, projectP1)
	if err != nil {
		t.Fatalf("RecordHolders() error = %v", err)
	}
	if diff := cmp.Diff(want, holders, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("RecordHolders(p-1) (-want +got):\n%s", diff)
	}

	// Given again: one row, with the new period. And the check answers with it at once.
	if err := manager.AssignRecordUserRole(ctx, projectP1, "ann", "ProjectEditor", accesstypes.Period{}); err != nil {
		t.Fatalf("AssignRecordUserRole() given again error = %v", err)
	}
	want[0].Period = accesstypes.Period{}
	if holders, err := manager.RecordHolders(ctx, projectP1); err != nil || !cmp.Equal(want, holders, cmpopts.EquateEmpty()) {
		t.Errorf("RecordHolders(p-1) after the role was given again = (%v, %v), want %v", holders, err, want)
	}
	if got := decisionAt(t, client, accesstypes.NewEnvironment(), "ann", tenant1Scope, "List", "Tasks"); !got.IsConditional() {
		t.Errorf("ann List on Tasks right after the give = %s, want conditional: the engine was not told", got)
	}

	// Taking what is not held is no error; taking what is held takes it.
	if err := manager.DeleteRecordUserRoles(ctx, projectP1, "cal", "ProjectEditor"); err != nil {
		t.Errorf("DeleteRecordUserRoles() of what cal does not hold: error = %v, want none", err)
	}
	if err := manager.DeleteRecordGroupRoles(ctx, "crew", projectP2, "ProjectEditor"); err != nil {
		t.Errorf("DeleteRecordGroupRoles() of what the crew does not hold there: error = %v, want none", err)
	}
	if err := manager.DeleteRecordUserRoles(ctx, projectP1, "ann", "ProjectEditor"); err != nil {
		t.Fatalf("DeleteRecordUserRoles() error = %v", err)
	}
	if got := decisionAt(t, client, accesstypes.NewEnvironment(), "ann", tenant1Scope, "List", "Tasks"); !got.IsDenied() {
		t.Errorf("ann List on Tasks right after the take = %s, want denied", got)
	}
	if err := manager.DeleteRecordGroupRoles(ctx, "crew", projectP1, "ProjectEditor"); err != nil {
		t.Fatalf("DeleteRecordGroupRoles() error = %v", err)
	}
	if holders, err := manager.RecordHolders(ctx, projectP1); err != nil || len(holders) != 0 {
		t.Errorf("RecordHolders(p-1) after both were taken = (%v, %v), want none", holders, err)
	}
	if err := manager.DeleteRecordUserRoles(ctx, projectP1, "", "ProjectEditor"); err == nil || httpio.Message(err) != "No person is named. Name the person the role is for." {
		t.Errorf("DeleteRecordUserRoles() naming no person = %v, want it refused", err)
	}

	// Removing a record's roles leaves another record's.
	if err := manager.AssignRecordUserRole(ctx, projectP1, "ann", "ProjectEditor", accesstypes.Period{}); err != nil {
		t.Fatalf("AssignRecordUserRole() error = %v", err)
	}
	if err := manager.DeleteRecordRoles(ctx, projectP1); err != nil {
		t.Fatalf("DeleteRecordRoles() error = %v", err)
	}
	if holders, err := manager.RecordHolders(ctx, projectP1); err != nil || len(holders) != 0 {
		t.Errorf("RecordHolders(p-1) after DeleteRecordRoles = (%v, %v), want none", holders, err)
	}
	if holders, err := manager.RecordHolders(ctx, projectP2); err != nil || len(holders) != 1 || holders[0].User != "bob" {
		t.Errorf("RecordHolders(p-2) after p-1's roles were removed = (%v, %v), want bob's", holders, err)
	}

	// The key methods hold the row to the rule and write nothing.
	written := store.writes
	key, err := manager.RecordMembership(ctx, projectP1, "dan", "ProjectEditor")
	if err != nil {
		t.Fatalf("RecordMembership() error = %v", err)
	}
	if want := NewRecordMembershipKey(projectP1, "dan", "ProjectEditor"); key != want {
		t.Errorf("RecordMembership() = %+v, want %+v", key, want)
	}
	if want := (RecordMembershipKey{Domain: "tenant1", Resource: "Projects", Record: "p-1", Role: "ProjectEditor", User: "dan"}); key != want {
		t.Errorf("RecordMembership() = %+v, want the columns %+v", key, want)
	}
	groupKey, err := manager.RecordGroupRole(ctx, "crew", tenant1Policy, projectP1, "ProjectEditor")
	if err != nil {
		t.Fatalf("RecordGroupRole() error = %v", err)
	}
	if want := (RecordGroupRoleKey{Domain: "tenant1", Resource: "Projects", Record: "p-1", Role: "ProjectEditor", Group: "crew"}); groupKey != want || NewRecordGroupRoleKey("crew", projectP1, "ProjectEditor") != want {
		t.Errorf("RecordGroupRole() = %+v, want %+v", groupKey, want)
	}
	if store.writes != written {
		t.Errorf("the key methods wrote %d rows, want none", store.writes-written)
	}
}

// TestUserManager_RolesHeldOn pins the roles offered on a record: the ones the role file
// marks for its kind, and no other.
func TestUserManager_RolesHeldOn(t *testing.T) {
	t.Parallel()
	_, manager, _ := recordManager(t)

	want := []RoleInfo{{Name: "ProjectEditor", Description: "Edits a project's tasks."}}
	if diff := cmp.Diff(want, manager.RolesHeldOn("Projects")); diff != "" {
		t.Errorf("RolesHeldOn(Projects) (-want +got):\n%s", diff)
	}
	if got := manager.RolesHeldOn("Invoices"); len(got) != 0 {
		t.Errorf("RolesHeldOn(Invoices) = %v, want none", got)
	}
}
