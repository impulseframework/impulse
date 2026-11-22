package access

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// TestGroupMayHold pins the rule a list of the roles to offer a group asks, the one
// every write of a group's role holds its row to: no group holds the Super
// Administrator role, a group kept in one tenant holds roles in that tenant alone, and
// a group kept for the whole application holds a role wherever a person can. What the
// list offers is what the write takes: the two never disagree.
func TestGroupMayHold(t *testing.T) {
	t.Parallel()

	global, every := accesstypes.GlobalPolicyScope(), accesstypes.EveryDomainPolicyScope()
	anvil, bastion := accesstypes.DomainPolicyScope("anvil"), accesstypes.DomainPolicyScope("bastion")
	tests := []struct {
		name  string
		kept  accesstypes.PolicyScope
		scope accesstypes.PolicyScope
		role  accesstypes.Role
		want  bool
	}{
		{name: "a tenant's group, a role in its tenant", kept: anvil, scope: anvil, role: "Reader", want: true},
		{name: "a tenant's group, a role for the whole application", kept: anvil, scope: global, role: "Reader"},
		{name: "a tenant's group, a role in every tenant", kept: anvil, scope: every, role: "Reader"},
		{name: "a tenant's group, a role in another tenant", kept: anvil, scope: bastion, role: "Reader"},
		{name: "a tenant's group, the Super Administrator role", kept: anvil, scope: anvil, role: accesstypes.SuperAdministrator},
		{name: "the whole application's group, a role for the whole application", kept: global, scope: global, role: "Steward", want: true},
		{name: "the whole application's group, a role in every tenant", kept: global, scope: every, role: "Reader", want: true},
		{name: "the whole application's group, a role in one tenant", kept: global, scope: anvil, role: "Reader", want: true},
		{name: "the whole application's group, the Super Administrator role", kept: global, scope: global, role: accesstypes.SuperAdministrator},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := GroupMayHold(tt.kept, tt.scope, tt.role); got != tt.want {
				t.Errorf("GroupMayHold(%v, %v, %s) = %t, want %t", tt.kept, tt.scope, tt.role, got, tt.want)
			}
			if taken := refuseGroupRole(tt.kept, tt.scope, tt.role) == nil; taken != tt.want {
				t.Errorf("a write of %s in %v for a group kept in %v is taken = %t, want %t as the list offers it", tt.role, tt.scope, tt.kept, taken, tt.want)
			}
		})
	}
}

// groupClient is superClient with the custom role Editor made in tenant1: the
// role file gives Steward (global) and Reader (every tenant).
func groupClient(t *testing.T) (*Client, UserManager) {
	t.Helper()
	client, _ := superClient(t)
	manager := client.UserManager()
	if err := manager.AddRole(t.Context(), tenant1Policy, "Editor", aDescription); err != nil {
		t.Fatalf("AddRole() error = %v", err)
	}
	if err := manager.AddRolePermissionResources(t.Context(), tenant1Policy, "Editor", "Read", "Widgets"); err != nil {
		t.Fatalf("AddRolePermissionResources() error = %v", err)
	}

	return client, manager
}

// mustAddGroup creates a group and fails the test when the manager refuses.
func mustAddGroup(t *testing.T, manager UserManager, scope accesstypes.PolicyScope, name string) accesstypes.Group {
	t.Helper()
	group, err := manager.AddGroup(t.Context(), scope, name, "The people the tests put in "+name+".")
	if err != nil {
		t.Fatalf("AddGroup(%s) error = %v", name, err)
	}

	return group
}

// Test_userManager_AddGroup pins the rules of a group's creation: where it may
// be kept, what its name and description must be, and that a name is held to
// one group where it is kept and free everywhere else.
func Test_userManager_AddGroup(t *testing.T) {
	t.Parallel()

	_, manager := groupClient(t)
	ctx := t.Context()
	night := mustAddGroup(t, manager, tenant1Policy, "Night shift")

	refused := []struct {
		name        string
		scope       accesstypes.PolicyScope
		group       string
		description string
		want        string
		conflict    bool
	}{
		{name: "kept in every tenant", scope: everyDomainPolicy, group: "Everyone", description: "Everybody.", want: "never in every tenant"},
		{name: "no name", scope: globalPolicy, group: "  ", description: "Nobody.", want: "has no name"},
		{name: "a name too long", scope: globalPolicy, group: strings.Repeat("n", 129), description: "Long.", want: "longer than 128 characters"},
		{name: "a control character in the name", scope: globalPolicy, group: "Night\tshift", description: "Tabbed.", want: "cannot be shown"},
		{name: "no description", scope: globalPolicy, group: "Staff", description: " ", want: "carries no description"},
		{name: "a name taken in the same tenant", scope: tenant1Policy, group: " Night shift ", description: "Again.", want: "already exists there", conflict: true},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := manager.AddGroup(ctx, tt.scope, tt.group, tt.description)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("AddGroup() error = %v, want one containing %q", err, tt.want)
			}
			if tt.conflict != httpio.HasConflict(err) {
				t.Errorf("AddGroup() conflict = %v, want %v", httpio.HasConflict(err), tt.conflict)
			}
		})
	}

	elsewhere := mustAddGroup(t, manager, tenant2Policy, "Night shift")
	forAll := mustAddGroup(t, manager, globalPolicy, "Night shift")
	if night == elsewhere || night == forAll || elsewhere == forAll {
		t.Errorf("groups of one name kept in three places share an id: %s, %s, %s", night, elsewhere, forAll)
	}
	listed, err := manager.Groups(ctx, tenant1Policy)
	if err != nil {
		t.Fatalf("Groups() error = %v", err)
	}
	want := []GroupInfo{{ID: night, Scope: tenant1Policy, Name: "Night shift", Description: "The people the tests put in Night shift."}}
	if diff := cmp.Diff(want, listed, cmp.AllowUnexported(accesstypes.PolicyScope{})); diff != "" {
		t.Errorf("Groups(tenant1) (-want +got):\n%s", diff)
	}
}

// Test_userManager_groupGivesItsRoles pins the whole of what a group does
// through the Client: a person put in a group holds the group's role at their
// next check and is listed as holding it, a role given later reaches them, and
// leaving the group or the group losing the role takes it away.
func Test_userManager_groupGivesItsRoles(t *testing.T) {
	t.Parallel()

	client, manager := groupClient(t)
	ctx := t.Context()
	env := accesstypes.Environment{}
	night := mustAddGroup(t, manager, tenant1Policy, "Night shift")

	reads := func(user accesstypes.User) bool {
		t.Helper()
		decisions, err := client.CheckUserResources(ctx, env, user, tenant1Scope, "Read", "Widgets")
		if err != nil {
			t.Fatalf("CheckUserResources() error = %v", err)
		}

		return decisions["Widgets"].IsGranted()
	}
	held := func(user accesstypes.User) accesstypes.RoleCollection {
		t.Helper()
		roles, err := manager.UserRoles(ctx, user)
		if err != nil {
			t.Fatalf("UserRoles() error = %v", err)
		}

		return roles
	}

	if err := manager.AddGroupUsers(ctx, night, "ann", "bob"); err != nil {
		t.Fatalf("AddGroupUsers() error = %v", err)
	}
	if reads("ann") || len(held("ann")) != 0 {
		t.Fatal("a member of a group that holds no role holds something")
	}

	if err := manager.AddGroupRoles(ctx, night, tenant1Policy, "Editor"); err != nil {
		t.Fatalf("AddGroupRoles() error = %v", err)
	}
	for _, user := range []accesstypes.User{"ann", "bob"} {
		if !reads(user) {
			t.Errorf("%s does not hold the role the group was given", user)
		}
		if diff := cmp.Diff(accesstypes.RoleCollection{tenant1Policy: {"Editor"}}, held(user)); diff != "" {
			t.Errorf("UserRoles(%s) (-want +got):\n%s", user, diff)
		}
	}
	if reads("cal") {
		t.Error("a person outside the group holds its role")
	}
	if has, err := client.UserHasGrants(ctx, "ann", tenant1Scope); err != nil || !has {
		t.Errorf("UserHasGrants(ann) = (%v, %v), want a foothold in the tenant through the group", has, err)
	}

	// The person's own membership of the same role: held once, and it stays
	// when the group's goes.
	if err := manager.AddUserRoles(ctx, tenant1Policy, "bob", "Editor"); err != nil {
		t.Fatalf("AddUserRoles() error = %v", err)
	}
	if diff := cmp.Diff(accesstypes.RoleCollection{tenant1Policy: {"Editor"}}, held("bob")); diff != "" {
		t.Errorf("UserRoles(bob), who holds the role both ways (-want +got):\n%s", diff)
	}
	scoped, err := manager.UserRoles(ctx, "ann", tenant1Policy, tenant2Policy)
	if err != nil {
		t.Fatalf("UserRoles(scopes) error = %v", err)
	}
	if diff := cmp.Diff(accesstypes.RoleCollection{tenant1Policy: {"Editor"}, tenant2Policy: {}}, scoped); diff != "" {
		t.Errorf("UserRoles(ann, tenant1, tenant2) (-want +got):\n%s", diff)
	}

	if err := manager.DeleteGroupUsers(ctx, night, "ann"); err != nil {
		t.Fatalf("DeleteGroupUsers() error = %v", err)
	}
	if reads("ann") || len(held("ann")) != 0 {
		t.Error("a person who left the group still holds its role")
	}
	if err := manager.DeleteGroupRoles(ctx, night, tenant1Policy, "Editor"); err != nil {
		t.Fatalf("DeleteGroupRoles() error = %v", err)
	}
	if !reads("bob") {
		t.Error("bob lost the role he holds by his own membership when the group lost it")
	}
	if err := manager.DeleteUserRoles(ctx, tenant1Policy, "bob", "Editor"); err != nil {
		t.Fatalf("DeleteUserRoles() error = %v", err)
	}
	if reads("bob") {
		t.Error("bob holds the role after his own membership and the group's are both gone")
	}
}

// Test_userManager_AddGroupRoles_refusals pins what a group may hold: never the
// Super Administrator role, a tenant's group nothing outside its tenant, and
// only a role that exists where it is given. A group kept for the whole
// application holds a role anywhere a person can.
func Test_userManager_AddGroupRoles_refusals(t *testing.T) {
	t.Parallel()

	_, manager := groupClient(t)
	ctx := t.Context()
	night := mustAddGroup(t, manager, tenant1Policy, "Night shift")
	staff := mustAddGroup(t, manager, globalPolicy, "Staff")

	tests := []struct {
		name    string
		group   accesstypes.Group
		scope   accesstypes.PolicyScope
		role    accesstypes.Role
		wantErr string
	}{
		{name: "a global group, the global scope", group: staff, scope: globalPolicy, role: "Steward"},
		{name: "a global group, every tenant", group: staff, scope: everyDomainPolicy, role: "Reader"},
		{name: "a global group, one tenant", group: staff, scope: tenant1Policy, role: "Editor"},
		{name: "a tenant's group, its tenant", group: night, scope: tenant1Policy, role: "Editor"},
		{name: "a tenant's group, a role of every tenant given there", group: night, scope: tenant1Policy, role: "Reader"},
		{name: "a tenant's group, another tenant", group: night, scope: tenant2Policy, role: "Reader", wantErr: "holds roles in that tenant alone"},
		{name: "a tenant's group, every tenant", group: night, scope: everyDomainPolicy, role: "Reader", wantErr: "holds roles in that tenant alone"},
		{name: "a tenant's group, the global scope", group: night, scope: globalPolicy, role: "Steward", wantErr: "holds roles in that tenant alone"},
		{name: "the Super Administrator role, a global group", group: staff, scope: globalPolicy, role: accesstypes.SuperAdministrator, wantErr: "the Super administrator role is given to a person, never to a group"},
		{name: "the Super Administrator role, a tenant's group", group: night, scope: tenant1Policy, role: accesstypes.SuperAdministrator, wantErr: "the Super administrator role is given to a person, never to a group"},
		{name: "a role that does not exist there", group: staff, scope: tenant2Policy, role: "Editor", wantErr: "The role Editor does not exist."},
		{name: "a group that does not exist", group: "no-such-group", scope: globalPolicy, role: "Steward", wantErr: "This group does not exist."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := manager.AddGroupRoles(ctx, tt.group, tt.scope, tt.role)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("AddGroupRoles() error = %v", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("AddGroupRoles() error = %v, want one containing %q", err, tt.wantErr)
			}
			held, err := manager.GroupRoles(ctx, tt.group)
			if err != nil {
				t.Fatalf("GroupRoles() error = %v", err)
			}
			if _, written := held[tt.scope]; written && tt.group == night && tt.scope != tenant1Policy {
				t.Errorf("GroupRoles() = %v, want nothing written by the refused call", held)
			}
		})
	}

	// The same rules for a caller that writes the row in a transaction of its own.
	noCustomRoles := func(context.Context, RoleKey) (bool, error) { return false, nil }
	if _, err := manager.GroupRole(ctx, staff, globalPolicy, globalPolicy, accesstypes.SuperAdministrator, noCustomRoles); err == nil || !strings.Contains(err.Error(), "the Super administrator role is given to a person, never to a group") {
		t.Errorf("GroupRole(Super Administrator) error = %v, want the role refused", err)
	}
	if _, err := manager.GroupRole(ctx, night, tenant1Policy, tenant2Policy, "Reader", noCustomRoles); err == nil || !strings.Contains(err.Error(), "holds roles in that tenant alone") {
		t.Errorf("GroupRole(another tenant) error = %v, want the place refused", err)
	}
	key, err := manager.GroupRole(ctx, staff, globalPolicy, everyDomainPolicy, "Reader", noCustomRoles)
	if err != nil {
		t.Fatalf("GroupRole() error = %v", err)
	}
	if diff := cmp.Diff(NewGroupRoleKey(staff, everyDomainPolicy, "Reader"), key); diff != "" {
		t.Errorf("GroupRole() key (-want +got):\n%s", diff)
	}
}

// Test_userManager_groupNeverGivesSuperAdministrator pins that a row written
// past the manager, straight into the store, makes nobody a super
// administrator: not at a check, and not in what a sign-in reads.
func Test_userManager_groupNeverGivesSuperAdministrator(t *testing.T) {
	t.Parallel()

	client, store := superClient(t)
	manager := client.UserManager()
	ctx := t.Context()
	staff := mustAddGroup(t, manager, globalPolicy, "Staff")
	if err := manager.AddGroupUsers(ctx, staff, "mallory"); err != nil {
		t.Fatalf("AddGroupUsers() error = %v", err)
	}
	if err := store.InsertGroupRole(ctx, staff, globalPolicy, accesstypes.SuperAdministrator, accesstypes.Period{}); err != nil {
		t.Fatalf("InsertGroupRole() error = %v", err)
	}
	client.PolicyChanged()

	if super, err := client.UserIsSuperAdministrator(ctx, "mallory"); err != nil || super {
		t.Errorf("UserIsSuperAdministrator() = (%v, %v), want a member of the group to be no super administrator", super, err)
	}
	held, err := manager.UserRoles(ctx, "mallory")
	if err != nil {
		t.Fatalf("UserRoles() error = %v", err)
	}
	if held.HoldsSuperAdministrator() || len(held) != 0 {
		t.Errorf("UserRoles() = %v, want nothing held through the group's Super Administrator row", held)
	}
}

// Test_userManager_DeleteGroup pins that a group goes only once it is emptied:
// with a member or a role it is refused, saying what to do, and nobody loses a
// role by the refusal.
func Test_userManager_DeleteGroup(t *testing.T) {
	t.Parallel()

	_, manager := groupClient(t)
	ctx := t.Context()
	night := mustAddGroup(t, manager, tenant1Policy, "Night shift")
	if err := manager.AddGroupUsers(ctx, night, "ann"); err != nil {
		t.Fatalf("AddGroupUsers() error = %v", err)
	}
	if err := manager.AddGroupRoles(ctx, night, tenant1Policy, "Editor"); err != nil {
		t.Fatalf("AddGroupRoles() error = %v", err)
	}

	refuse := func(when string) {
		t.Helper()
		err := manager.DeleteGroup(ctx, night)
		if err == nil || !httpio.HasConflict(err) || !strings.Contains(err.Error(), "remove its members and take its roles away") {
			t.Fatalf("DeleteGroup() with %s error = %v, want a conflict that says what to do", when, err)
		}
	}
	refuse("a member and a role")
	if err := manager.DeleteGroupUsers(ctx, night, "ann"); err != nil {
		t.Fatalf("DeleteGroupUsers() error = %v", err)
	}
	refuse("a role")
	if err := manager.AddGroupUsers(ctx, night, "ann"); err != nil {
		t.Fatalf("AddGroupUsers() error = %v", err)
	}
	if err := manager.DeleteGroupRoles(ctx, night, tenant1Policy, "Editor"); err != nil {
		t.Fatalf("DeleteGroupRoles() error = %v", err)
	}
	refuse("a member")
	if err := manager.DeleteGroupUsers(ctx, night, "ann"); err != nil {
		t.Fatalf("DeleteGroupUsers() error = %v", err)
	}

	if err := manager.DeleteGroup(ctx, night); err != nil {
		t.Fatalf("DeleteGroup() of an emptied group error = %v", err)
	}
	if err := manager.DeleteGroup(ctx, night); err == nil || !httpio.HasNotFound(err) {
		t.Errorf("DeleteGroup() of a group that is gone error = %v, want not found", err)
	}
	if err := manager.AddGroupUsers(ctx, night, "ann"); err == nil || !httpio.HasNotFound(err) {
		t.Errorf("AddGroupUsers() to a group that is gone error = %v, want not found", err)
	}
}

// Test_userManager_DescribeGroup_UserGroups pins the two readings of a group
// beside its members: its description is replaced, and a person's groups are
// listed wherever each is kept.
func Test_userManager_DescribeGroup_UserGroups(t *testing.T) {
	t.Parallel()

	_, manager := groupClient(t)
	ctx := t.Context()
	night := mustAddGroup(t, manager, tenant1Policy, "Night shift")
	staff := mustAddGroup(t, manager, globalPolicy, "Staff")
	for _, group := range []accesstypes.Group{night, staff} {
		if err := manager.AddGroupUsers(ctx, group, "ann"); err != nil {
			t.Fatalf("AddGroupUsers() error = %v", err)
		}
	}
	if err := manager.DescribeGroup(ctx, night, " Who works after dark. "); err != nil {
		t.Fatalf("DescribeGroup() error = %v", err)
	}
	if err := manager.DescribeGroup(ctx, night, ""); err == nil {
		t.Error("DescribeGroup() with no description: want it refused")
	}
	if err := manager.DescribeGroup(ctx, "no-such-group", "Nobody."); err == nil || !httpio.HasNotFound(err) {
		t.Errorf("DescribeGroup() of no group error = %v, want not found", err)
	}

	groups, err := manager.UserGroups(ctx, "ann")
	if err != nil {
		t.Fatalf("UserGroups() error = %v", err)
	}
	got := make(map[accesstypes.Group]GroupInfo, len(groups))
	for _, g := range groups {
		got[g.ID] = g
	}
	want := map[accesstypes.Group]GroupInfo{
		night: {ID: night, Scope: tenant1Policy, Name: "Night shift", Description: "Who works after dark."},
		staff: {ID: staff, Scope: globalPolicy, Name: "Staff", Description: "The people the tests put in Staff."},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(accesstypes.PolicyScope{})); diff != "" {
		t.Errorf("UserGroups() (-want +got):\n%s", diff)
	}
	users, err := manager.GroupUsers(ctx, night)
	if err != nil || len(users) != 1 || users[0] != "ann" {
		t.Errorf("GroupUsers() = (%v, %v), want ann", users, err)
	}
}
