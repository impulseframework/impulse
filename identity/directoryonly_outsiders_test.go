package identity

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// entering is a request's caller as the rule on a directory-only tenant reads one: who
// they are, and the tenants the session they hold enters, which is the reach the
// resource library answers for a session (resource.UserPermissions.Reaches). asked
// records the tenants the rule asked about.
type entering struct {
	resource.UserPermissions
	user   accesstypes.User
	enters []accesstypes.Domain
	asked  *[]accesstypes.Domain
}

func (e entering) User() accesstypes.User {
	return e.user
}

func (e entering) Reaches(_ context.Context, domain accesstypes.Domain) (bool, error) {
	if e.asked != nil {
		*e.asked = append(*e.asked, domain)
	}

	return slices.Contains(e.enters, domain), nil
}

// asEntering is the context of a request made by the user in a session that enters the
// tenants named and no other.
func asEntering(ctx context.Context, user accesstypes.User, enters ...accesstypes.Domain) context.Context {
	return resource.WithCaller(resource.WithAuth(ctx, managedAuth), &resource.Caller{Permissions: entering{user: user, enters: enters}})
}

// tenantWardenRoles are the roles of the tests' engine: Warden administers a tenant, and
// exists in alpha and in every tenant beside Reader.
var tenantWardenRoles = map[accesstypes.PolicyScope][]accesstypes.Role{
	globalScope: {"Steward"},
	alphaScope:  {"Reader", "Warden"},
	everyScope:  {"Reader", "Warden"},
}

// outsiderManager builds a manager whose engine names Warden as a tenant's
// administrator's role, with role grants that refuse nobody anything.
func outsiderManager(t *testing.T, store Store) *Manager {
	t.Helper()
	engine := &fakeEngine{roles: tenantWardenRoles, tenantAdministrators: []accesstypes.Role{"Warden"}}
	m, err := New(managedAuth, store, engine, fakeSessions{mail: &fakeMail{}}, WithRoleGrants(&fakeGrants{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m
}

const (
	roleKeptFromOutsiders    = "Warden is a role that administers a tenant, and this tenant admits only people who sign in through its own directory, which your sign-in does not enter. Who holds the role there is changed by one of its administrators, or by a super administrator"
	accountKeptFromOutsiders = "This person administers a tenant that admits only people who sign in through its own directory, which your sign-in does not enter. Their account is changed by one of that tenant's administrators, or by a super administrator"
)

// TestManager_directoryOnly_administratorsRole pins that who holds a tenant's
// administrator role in the tenant is changed only by a caller the tenant admits: every
// act that gives the role or takes it away, to a person, through a new account, by an
// invitation, to a group, or by a place in a group that holds it, is refused a caller
// whose session does not enter the tenant, in a sentence that says who does it. A
// caller the tenant admits makes the change, the application acts outside any request,
// and neither another role of the tenant nor the role held in every tenant is judged.
func TestManager_directoryOnly_administratorsRole(t *testing.T) {
	t.Parallel()

	alpha := "alpha"
	wardenInAlpha := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Warden")
	acts := []struct {
		name string
		// setup commits what the act changes.
		setup func(store *fakeStore)
		act   func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, scope accesstypes.PolicyScope, role accesstypes.Role) error
		// tenantOnly marks an act that names one tenant and no other scope.
		tenantOnly bool
		// buffersFirst marks an act that buffers a row before its roles are judged: the
		// refusal fails the transaction, so nothing of it is committed.
		buffersFirst bool
	}{
		{
			name: "giving a person the role",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, scope accesstypes.PolicyScope, role accesstypes.Role) error {
				return m.AssignRole(ctx, txn, scope, userID, role)
			},
		},
		{
			name: "taking the role from a person",
			setup: func(store *fakeStore) {
				store.memberships[access.NewMembershipKey(alphaScope, dana, "Warden")] = true
				store.memberships[access.NewMembershipKey(alphaScope, dana, "Reader")] = true
				store.memberships[access.NewMembershipKey(everyScope, dana, "Warden")] = true
				store.memberships[access.NewMembershipKey(globalScope, dana, "Steward")] = true
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, scope accesstypes.PolicyScope, role accesstypes.Role) error {
				return m.RemoveRole(ctx, txn, scope, userID, role)
			},
		},
		{
			name: "a new account that starts with the role", buffersFirst: true,
			setup: func(store *fakeStore) { store.nextID = createdID },
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, scope accesstypes.PolicyScope, role accesstypes.Role) error {
				_, err := m.CreateUser(ctx, txn, &NewAccount{Username: "hale", Scope: scope, Roles: []accesstypes.Role{role}})

				return err
			},
		},
		{
			name: "an invitation that carries the role", tenantOnly: true,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, _ accesstypes.PolicyScope, role accesstypes.Role) error {
				_, err := m.Invite(ctx, txn, &Invitation{Email: "hale@example.com", Domain: &alpha, Roles: []accesstypes.Role{role}}, nil)

				return err
			},
		},
		{
			name: "giving a group the role, though nobody is in it yet", tenantOnly: true,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, scope accesstypes.PolicyScope, role accesstypes.Role) error {
				return m.GiveGroupRoleFor(ctx, txn, nightID, scope, role, accesstypes.Period{})
			},
		},
		{
			name: "taking the role from a group", tenantOnly: true,
			setup: func(store *fakeStore) {
				store.groupRoles[wardenInAlpha] = true
				store.groupRoles[access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")] = true
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager, scope accesstypes.PolicyScope, role accesstypes.Role) error {
				return m.TakeGroupRole(ctx, txn, nightID, scope, role)
			},
		},
	}
	callers := []struct {
		name    string
		ctx     func(context.Context) context.Context
		scope   accesstypes.PolicyScope
		role    accesstypes.Role
		refused bool
	}{
		{
			name: "a caller the tenant does not admit is refused",
			ctx:  func(ctx context.Context) context.Context { return asEntering(ctx, erin) }, scope: alphaScope, role: "Warden", refused: true,
		},
		{
			name: "a caller who views as one of the tenant's administrators is refused: the session they hold enters nothing",
			ctx:  func(ctx context.Context) context.Context { return viewedBy(asEntering(ctx, erin), frankID) }, scope: alphaScope, role: "Warden", refused: true,
		},
		{
			name: "a caller the tenant admits makes the change",
			ctx:  func(ctx context.Context) context.Context { return asEntering(ctx, erin, "alpha") }, scope: alphaScope, role: "Warden",
		},
		{
			name: "the application acts outside any request",
			ctx:  func(ctx context.Context) context.Context { return ctx }, scope: alphaScope, role: "Warden",
		},
		{
			name: "another role of the tenant is the role file's to hand out",
			ctx:  func(ctx context.Context) context.Context { return asEntering(ctx, erin) }, scope: alphaScope, role: "Reader",
		},
		{
			name: "the role held in every tenant is no way into one, and is not judged",
			ctx:  func(ctx context.Context) context.Context { return asEntering(ctx, erin) }, scope: everyScope, role: "Warden",
		},
	}
	for _, act := range acts {
		for _, caller := range callers {
			if act.tenantOnly && caller.scope != alphaScope {
				continue
			}
			t.Run(act.name+": "+caller.name, func(t *testing.T) {
				t.Parallel()

				store := groupStore()
				if act.setup != nil {
					act.setup(store)
				}
				m := outsiderManager(t, store)

				err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					return act.act(caller.ctx(ctx), txn, m, caller.scope, caller.role)
				})
				if !caller.refused {
					if err != nil {
						t.Fatalf("error = %v, want the act to go through", err)
					}

					return
				}
				if !httpio.HasForbidden(err) || httpio.Message(err) != roleKeptFromOutsiders {
					t.Fatalf("error = %v, want the refusal reading %q", err, roleKeptFromOutsiders)
				}
				if len(store.writes) != 0 && !act.buffersFirst {
					t.Errorf("buffered writes = %v, want none", store.writes)
				}
			})
		}
	}
}

// TestManager_directoryOnly_groupOfAdministrators pins that a place in a group that
// holds a tenant's administrator role in the tenant is the role: putting a person in the
// group and taking one out are refused a caller the tenant does not admit, and made by
// one it admits and by the application; a group that holds another role is not judged.
func TestManager_directoryOnly_groupOfAdministrators(t *testing.T) {
	t.Parallel()

	acts := []struct {
		name string
		in   bool
		act  func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
	}{
		{
			name: "putting a person in the group",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AddGroupMemberFor(ctx, txn, nightID, userID, accesstypes.Period{})
			},
		},
		{
			name: "taking a person out of the group", in: true,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemoveGroupMember(ctx, txn, nightID, userID)
			},
		},
	}
	callers := []struct {
		name    string
		ctx     func(context.Context) context.Context
		holds   accesstypes.Role
		refused bool
	}{
		{name: "a caller the tenant does not admit is refused", ctx: func(ctx context.Context) context.Context { return asEntering(ctx, erin) }, holds: "Warden", refused: true},
		{
			name: "a caller who views as one of the tenant's administrators is refused",
			ctx:  func(ctx context.Context) context.Context { return viewedBy(asEntering(ctx, erin), frankID) }, holds: "Warden", refused: true,
		},
		{name: "a caller the tenant admits makes the change", ctx: func(ctx context.Context) context.Context { return asEntering(ctx, erin, "alpha") }, holds: "Warden"},
		{name: "the application acts outside any request", ctx: func(ctx context.Context) context.Context { return ctx }, holds: "Warden"},
		{name: "a group that holds another role is not judged", ctx: func(ctx context.Context) context.Context { return asEntering(ctx, erin) }, holds: "Reader"},
	}
	for _, act := range acts {
		for _, caller := range callers {
			t.Run(act.name+": "+caller.name, func(t *testing.T) {
				t.Parallel()

				store := groupStore()
				store.groupRoles[access.NewGroupRoleKey(GroupID(nightID), alphaScope, caller.holds)] = true
				if act.in {
					store.groupMembers[memberKey(nightID, userID)] = true
					// The person holds something else too, so leaving the group does not end the account.
					store.memberships[access.NewMembershipKey(globalScope, dana, "Steward")] = true
				}
				m := outsiderManager(t, store)

				err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					return act.act(caller.ctx(ctx), txn, m)
				})
				if !caller.refused {
					if err != nil {
						t.Fatalf("error = %v, want the act to go through", err)
					}

					return
				}
				if !httpio.HasForbidden(err) || httpio.Message(err) != roleKeptFromOutsiders {
					t.Fatalf("error = %v, want the refusal reading %q", err, roleKeptFromOutsiders)
				}
				if len(store.writes) != 0 {
					t.Errorf("buffered writes = %v, want none", store.writes)
				}
			})
		}
	}
}

// TestManager_directoryOnly_administratorsAccount pins that the account of a person who
// administers a tenant is changed only by a caller the tenant admits. Each act is a way
// to become the person, or to shut them out: a password set for them or taken from
// them, a change of password required of them, a new username, a new address, the
// unlinking of an identity, the account disabled, enabled or deleted. A caller whose
// session does not enter the tenant is refused each one, whether the person holds the
// role of their own, through a group, or in an assignment that has not begun, and so is
// a caller who views as one of the tenant's administrators. A caller the tenant admits
// acts, the application acts outside any request, and a person who administers no
// tenant stays whoever's the role file says: one who holds another role there, the role
// in every tenant, or an assignment that has ended.
func TestManager_directoryOnly_administratorsAccount(t *testing.T) {
	t.Parallel()

	address := "dana@example.com"
	ended, upcoming, _ := periodFixtures()
	wardenOfAlpha := access.NewMembershipKey(alphaScope, dana, "Warden")
	acts := []struct {
		name string
		act  func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
	}{
		{name: "a temporary password", act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.SetTemporaryPassword(ctx, txn, userID, "first-day")
		}},
		{name: "the removal of the password", act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.RemovePassword(ctx, txn, userID)
		}},
		{name: "a required change of password", act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.RequirePasswordChange(ctx, txn, userID)
		}},
		{name: "a new username", act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.RenameUser(ctx, txn, userID, "dana.r")
		}},
		{name: "a new address", act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.SetEmail(ctx, txn, userID, &address)
		}},
		{name: "the unlinking of an identity", act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.UnlinkIdentity(ctx, txn, identityA)
		}},
		{name: "the account disabled", act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.DisableUser(ctx, txn, userID)
		}},
		{name: "the account enabled", act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.EnableUser(ctx, txn, userID)
		}},
		{name: "the account deleted", act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.DeleteUser(ctx, txn, userID)
		}},
	}
	callers := []struct {
		name string
		ctx  func(context.Context) context.Context
		// holds commits what the person holds.
		holds   func(store *fakeStore)
		refused bool
	}{
		{
			name:    "a caller the tenant does not admit is refused",
			ctx:     func(ctx context.Context) context.Context { return asEntering(ctx, erin) },
			holds:   func(store *fakeStore) { store.memberships[wardenOfAlpha] = true },
			refused: true,
		},
		{
			name:    "a caller who views as one of the tenant's administrators is refused: the session they hold enters nothing",
			ctx:     func(ctx context.Context) context.Context { return viewedBy(asEntering(ctx, erin), frankID) },
			holds:   func(store *fakeStore) { store.memberships[wardenOfAlpha] = true },
			refused: true,
		},
		{
			name: "the role held through a group counts",
			ctx:  func(ctx context.Context) context.Context { return asEntering(ctx, erin) },
			holds: func(store *fakeStore) {
				store.groupRoles[access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Warden")] = true
				store.groupMembers[memberKey(nightID, userID)] = true
			},
			refused: true,
		},
		{
			name: "an assignment that has not begun counts: the person will hold it",
			ctx:  func(ctx context.Context) context.Context { return asEntering(ctx, erin) },
			holds: func(store *fakeStore) {
				store.memberships[wardenOfAlpha] = true
				store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{wardenOfAlpha: upcoming}
			},
			refused: true,
		},
		{
			name:  "a caller the tenant admits acts",
			ctx:   func(ctx context.Context) context.Context { return asEntering(ctx, erin, "alpha") },
			holds: func(store *fakeStore) { store.memberships[wardenOfAlpha] = true },
		},
		{
			name:  "the application acts outside any request",
			ctx:   func(ctx context.Context) context.Context { return ctx },
			holds: func(store *fakeStore) { store.memberships[wardenOfAlpha] = true },
		},
		{
			name:  "a person who holds another role of the tenant is the role file's to manage",
			ctx:   func(ctx context.Context) context.Context { return asEntering(ctx, erin) },
			holds: func(store *fakeStore) { store.memberships[access.NewMembershipKey(alphaScope, dana, "Reader")] = true },
		},
		{
			name:  "the role held in every tenant is no way into one",
			ctx:   func(ctx context.Context) context.Context { return asEntering(ctx, erin) },
			holds: func(store *fakeStore) { store.memberships[access.NewMembershipKey(everyScope, dana, "Warden")] = true },
		},
		{
			name: "an assignment that has ended gives nothing",
			ctx:  func(ctx context.Context) context.Context { return asEntering(ctx, erin) },
			holds: func(store *fakeStore) {
				store.memberships[wardenOfAlpha] = true
				store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{wardenOfAlpha: ended}
			},
		},
	}
	for _, act := range acts {
		for _, caller := range callers {
			t.Run(act.name+": "+caller.name, func(t *testing.T) {
				t.Parallel()

				store := groupStore()
				store.usernames[otherUser] = "erin"
				store.disabled = map[impulse.UUID]bool{}
				store.passwords = map[impulse.UUID]bool{userID: true}
				store.identities = map[impulse.UUID]impulse.UUID{identityA: userID, identityB: userID}
				caller.holds(store)
				m := outsiderManager(t, store)

				err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					return act.act(caller.ctx(ctx), txn, m)
				})
				if !caller.refused {
					if err != nil {
						t.Fatalf("error = %v, want the act to go through", err)
					}

					return
				}
				if !httpio.HasForbidden(err) || httpio.Message(err) != accountKeptFromOutsiders {
					t.Fatalf("error = %v, want the refusal reading %q", err, accountKeptFromOutsiders)
				}
				if len(store.writes) != 0 {
					t.Errorf("buffered writes = %v, want none", store.writes)
				}
			})
		}
	}
}

// TestManager_RolesToGive_directoryOnly pins that the list a role is picked from offers
// what the methods take: a tenant's administrator role is not offered to a caller the
// tenant does not admit, and is offered to one it admits; the tenant's other roles are
// offered either way.
func TestManager_RolesToGive_directoryOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		enters []accesstypes.Domain
		want   []string
	}{
		{name: "a caller the tenant does not admit is offered its other roles alone", want: []string{"Reader"}},
		{name: "a caller the tenant admits is offered the administrator's role too", enters: []accesstypes.Domain{"alpha"}, want: []string{"Reader", "Warden"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := outsiderManager(t, groupStore())
			roles, err := m.RolesToGive(asEntering(t.Context(), erin, tt.enters...), "alpha")
			if err != nil {
				t.Fatalf("RolesToGive() error = %v", err)
			}
			got := make([]string, 0, len(roles))
			for _, role := range roles {
				got = append(got, string(role.Name))
			}
			slices.Sort(got)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("RolesToGive() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_directoryOnly_asksOnlyOfATenantsAdministrators pins what the rule reads:
// the engine's reach is asked about the tenant an administrator's role is held in, and
// about nothing for a role that is not one.
func TestManager_directoryOnly_asksOnlyOfATenantsAdministrators(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		role accesstypes.Role
		want []accesstypes.Domain
	}{
		{role: "Warden", want: []accesstypes.Domain{"alpha"}},
		{role: "Reader"},
	} {
		t.Run(string(tt.role), func(t *testing.T) {
			t.Parallel()

			var asked []accesstypes.Domain
			m := outsiderManager(t, groupStore())
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = resource.WithCaller(resource.WithAuth(ctx, managedAuth), &resource.Caller{Permissions: entering{user: erin, enters: []accesstypes.Domain{"alpha"}, asked: &asked}})

				return m.AssignRole(ctx, txn, alphaScope, userID, tt.role)
			})
			if err != nil {
				t.Fatalf("AssignRole() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, asked); diff != "" {
				t.Errorf("the tenants the reach was asked about (-want +got):\n%s", diff)
			}
			if !strings.Contains(roleKeptFromOutsiders, "super administrator") || !strings.Contains(accountKeptFromOutsiders, "super administrator") {
				t.Error("a refusal names nobody who can make the change")
			}
		})
	}
}
