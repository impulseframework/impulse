package access

import (
	"context"
	"slices"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// goneStore is the fake store with one custom role's rows deleted as a write of a
// holding of it arrives: what a DeleteRole made at that moment by somebody else leaves,
// once the user manager's own checks have found the role.
type goneStore struct {
	*fakeStore
	// role is the custom role whose rows go.
	role accesstypes.Role
}

// dropRole deletes every row of role, wherever it is held, when it is the role that
// goes.
func (s *goneStore) dropRole(role accesstypes.Role) {
	if role != s.role {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.roles {
		if key.role == role {
			delete(s.roles, key)
		}
	}
}

func (s *goneStore) InsertUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) error {
	s.dropRole(role)

	return s.fakeStore.InsertUserRole(ctx, scope, user, role, period, reason)
}

func (s *goneStore) InsertCustomUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) error {
	s.dropRole(role)

	return s.fakeStore.InsertCustomUserRole(ctx, scope, user, role, period, reason)
}

func (s *goneStore) InsertGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	s.dropRole(role)

	return s.fakeStore.InsertGroupRole(ctx, group, scope, role, period)
}

func (s *goneStore) InsertCustomGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	s.dropRole(role)

	return s.fakeStore.InsertCustomGroupRole(ctx, group, scope, role, period)
}

// The roles of the tests of a role deleted as it is given: the one that goes, and one
// that stays.
const (
	nightWarden = accesstypes.Role("NightWarden")
	dayWarden   = accesstypes.Role("DayWarden")

	nightWardenGone = "The role Night warden does not exist."
)

// goneClient is a client over a store where nightWarden goes as it is given, with
// nightWarden and dayWarden made in tenant1 and the client's announcements on signal.
func goneClient(t *testing.T) (UserManager, *goneStore, *announcedSignal) {
	t.Helper()
	store := &goneStore{fakeStore: newFakeStore(), role: nightWarden}
	signal := newAnnouncedSignal()
	client, _ := announcingClientOver(t, store, signal)
	manager := client.UserManager()
	for _, role := range []accesstypes.Role{nightWarden, dayWarden} {
		if err := manager.AddRole(t.Context(), tenant1Policy, role, aDescription); err != nil {
			t.Fatalf("AddRole(%s) error = %v", role, err)
		}
	}

	return manager, store, signal
}

// Test_userManager_roleDeletedAsItIsGiven pins that a custom role deleted at the moment
// it is given, after the call's own checks found it, is not given: each way to give it
// answers that the role does not exist, writes no holding of it, and announces no
// change. A holding left behind would give nothing today, and everything a role made
// later under the name grants.
func Test_userManager_roleDeletedAsItIsGiven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		give func(ctx context.Context, manager UserManager, group accesstypes.Group) error
	}{
		{
			name: "AddRoleUsers",
			give: func(ctx context.Context, manager UserManager, _ accesstypes.Group) error {
				return manager.AddRoleUsers(ctx, tenant1Policy, nightWarden, "bob", "carol")
			},
		},
		{
			name: "AddUserRoles",
			give: func(ctx context.Context, manager UserManager, _ accesstypes.Group) error {
				return manager.AddUserRoles(ctx, tenant1Policy, "bob", nightWarden)
			},
		},
		{
			name: "AssignUserRole",
			give: func(ctx context.Context, manager UserManager, _ accesstypes.Group) error {
				return manager.AssignUserRole(ctx, tenant1Policy, "bob", nightWarden, accesstypes.Period{}, "Covers the night.")
			},
		},
		{
			name: "AddGroupRoles",
			give: func(ctx context.Context, manager UserManager, group accesstypes.Group) error {
				return manager.AddGroupRoles(ctx, group, tenant1Policy, nightWarden)
			},
		},
		{
			name: "AssignGroupRole",
			give: func(ctx context.Context, manager UserManager, group accesstypes.Group) error {
				return manager.AssignGroupRole(ctx, group, tenant1Policy, nightWarden, accesstypes.Period{})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			manager, store, signal := goneClient(t)
			group := mustAddGroup(t, manager, tenant1Policy, "Night shift")
			announced := signal.begun.Load()

			notFoundPlainly(t, tt.give(t.Context(), manager, group), nightWardenGone, string(nightWarden))
			store.mu.Lock()
			memberships, held := len(store.memberships), len(store.groups.held)
			store.mu.Unlock()
			if memberships != 0 || held != 0 {
				t.Errorf("the store holds %d memberships and %d group roles, want none: the role was gone when it was given", memberships, held)
			}
			if got := signal.begun.Load(); got != announced {
				t.Errorf("announcements made by the refused call = %d, want none", got-announced)
			}
		})
	}
}

// Test_userManager_AddUserRoles_secondRoleDeleted pins what a call that gives several
// roles leaves when one of them is deleted as it is given: the call stops there with the
// answer that the role does not exist, and the role it gave before stays given and is
// announced.
func Test_userManager_AddUserRoles_secondRoleDeleted(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	manager, store, signal := goneClient(t)
	announced := signal.begun.Load()

	notFoundPlainly(t, manager.AddUserRoles(ctx, tenant1Policy, "bob", dayWarden, nightWarden), nightWardenGone, string(nightWarden))
	roles, err := store.ListUserRoles(ctx, tenant1Policy, "bob")
	if err != nil {
		t.Fatalf("ListUserRoles() error = %v", err)
	}
	if want := []accesstypes.Role{dayWarden}; !slices.Equal(roles, want) {
		t.Errorf("bob holds %v, want %v: the role given before the one that went stays given", roles, want)
	}
	if got := signal.begun.Load(); got != announced+1 {
		t.Errorf("announcements made by the call = %d, want 1 for the role it gave", got-announced)
	}
}

// heldInTenant is what a person is told of a role held in every tenant that a person or
// a group holds in one tenant.
const heldInTenant = "A person or a group holds the role in a tenant, now or for a period that has not begun yet. You cannot delete a role that is held in a tenant: take it away there first"

// Test_userManager_DeleteRole_heldInDomain pins that a role held in every tenant is not
// deleted while a person holds it in one tenant through it: the deletion is refused in
// words, with the store's refusal kept as its cause, and the role stays. A tenant with a
// role of the name of its own does not stand in the way: its people hold that one.
func Test_userManager_DeleteRole_heldInDomain(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, manager, store := periodClient(t)
	every := accesstypes.EveryDomainPolicyScope()

	if err := manager.AddRole(ctx, every, nightWarden, aDescription); err != nil {
		t.Fatalf("AddRole(every domain) error = %v", err)
	}
	if err := manager.AddRoleUsers(ctx, tenant1Policy, nightWarden, "bob"); err != nil {
		t.Fatalf("AddRoleUsers(%s) error = %v", tenant1Policy, err)
	}

	deleted, err := manager.DeleteRole(ctx, every, nightWarden)
	if deleted || err == nil || !httpio.HasBadRequest(err) || !errors.Is(err, policy.ErrHeldInDomain) {
		t.Fatalf("DeleteRole(every domain) of a role bob holds in a tenant = (%v, %v), want a bad request whose cause is policy.ErrHeldInDomain", deleted, err)
	}
	if messages := httpio.Messages(err); len(messages) != 1 || messages[0] != heldInTenant {
		t.Errorf("the person is told %q, want only %q", messages, heldInTenant)
	}
	if exists, err := store.RoleExists(ctx, every, nightWarden); err != nil || !exists {
		t.Fatalf("Store.RoleExists() after the refusal = (%v, %v), want the role there", exists, err)
	}

	// The tenant's own role of the name is made first: a role held in every domain
	// is the one a tenant's new role of the name would be refused beside.
	for _, scope := range []accesstypes.PolicyScope{tenant1Policy, every} {
		if err := manager.AddRole(ctx, scope, dayWarden, aDescription); err != nil {
			t.Fatalf("AddRole(%s) error = %v", scope, err)
		}
	}
	if err := manager.AddRoleUsers(ctx, tenant1Policy, dayWarden, "bob"); err != nil {
		t.Fatalf("AddRoleUsers(%s) error = %v", tenant1Policy, err)
	}
	if deleted, err := manager.DeleteRole(ctx, every, dayWarden); err != nil || !deleted {
		t.Errorf("DeleteRole(every domain) of a role bob holds through the tenant's own role of the name = (%v, %v), want it deleted", deleted, err)
	}
}
