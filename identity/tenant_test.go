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
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// inAlpha is the context of a request the user made under the address of the tenant
// alpha, as a generated handler hands it to a tenant's method.
func inAlpha(ctx context.Context, user accesstypes.User) context.Context {
	return resource.WithCaller(ctx, &resource.Caller{Permissions: callerPermissions{user: user}, Scope: accesstypes.DomainScope("alpha")})
}

// TestManager_Tenant pins where a tenant's method takes its tenant from: the scope its
// request was checked in. A call outside any request has none and is an error, and so is
// a request checked for the whole application.
func TestManager_Tenant(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, &fakeStore{}, engineRoles)

	tenant, err := m.Tenant(inAlpha(t.Context(), "erin"))
	if err != nil {
		t.Fatalf("Tenant() error = %v", err)
	}
	if tenant.Domain() != "alpha" {
		t.Errorf("Tenant().Domain() = %q, want the request's tenant alpha", tenant.Domain())
	}

	if _, err := m.Tenant(t.Context()); err == nil || !strings.Contains(err.Error(), "outside a request") {
		t.Errorf("Tenant() outside a request: error = %v, want one that says so", err)
	}
	global := resource.WithCaller(t.Context(), &resource.Caller{Permissions: callerPermissions{user: "erin"}, Scope: accesstypes.GlobalScope()})
	if _, err := m.Tenant(global); err == nil || !strings.Contains(err.Error(), "names no tenant") {
		t.Errorf("Tenant() for a request of the whole application: error = %v, want one that says so", err)
	}
}

// TestManager_TenantRoles pins the roles a tenant picks from: the ones the engine lists
// for the tenant's own scope, with their descriptions. No tenant named is an error and
// never the roles of the whole application, and the engine's failure is the caller's.
func TestManager_TenantRoles(t *testing.T) {
	t.Parallel()

	m, engine := newManager(t, &fakeStore{}, engineRoles)

	got, err := m.TenantRoles(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("TenantRoles(alpha) error = %v", err)
	}
	if diff := cmp.Diff([]access.RoleInfo{{Name: "Reader", Description: "the Reader role"}}, got); diff != "" {
		t.Errorf("TenantRoles(alpha) mismatch (-want +got):\n%s", diff)
	}

	got, err = m.TenantRoles(t.Context(), "beta")
	if err != nil {
		t.Fatalf("TenantRoles(beta) error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("TenantRoles(beta) = %v, want none: the engine lists none for a tenant it holds no role of", got)
	}

	if _, err := m.TenantRoles(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "no tenant was named") {
		t.Errorf("TenantRoles(no tenant) error = %v, want one that says no tenant was named", err)
	}

	engine.rolesErr = errors.New("the roles table is away")
	if _, err := m.TenantRoles(t.Context(), "alpha"); err == nil || !strings.Contains(err.Error(), "the roles table is away") {
		t.Errorf("TenantRoles() with a failing engine: error = %v, want the engine's", err)
	}
}

// TestTenant_AssignRole pins a role given in a tenant: held in the request's tenant and
// nowhere else, to a person named by id, under the engine's rule on the role.
func TestTenant_AssignRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		user       impulse.UUID
		role       accesstypes.Role
		wantErr    func(error) bool
		wantWrites []string
	}{
		{name: "the role is held in the request's tenant", user: userID, role: "Reader", wantWrites: []string{"insert membership domain/alpha dana Reader"}},
		{name: "a role of the whole application is no role of a tenant", user: userID, role: "Steward", wantErr: httpio.HasNotFound},
		{name: "a person that does not exist is not found", user: createdID, role: "Reader", wantErr: httpio.HasNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, memberships: map[access.MembershipKey]bool{}}
			m, _ := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = inAlpha(ctx, "erin")
				tenant, err := m.Tenant(ctx)
				if err != nil {
					return err
				}

				return tenant.AssignRole(ctx, txn, tt.user, tt.role)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("AssignRole() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("AssignRole() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTenant_RemoveRole pins a role taken in a tenant: the tenant's own membership goes,
// under the rules of the whole application's removal; a role held in every tenant alone
// is not the tenant's to take; a role held in another tenant is untouched; and an empty
// role is refused.
func TestTenant_RemoveRole(t *testing.T) {
	t.Parallel()

	membership := access.NewMembershipKey
	betaScope := accesstypes.DomainPolicyScope("beta")

	tests := []struct {
		name   string
		caller accesstypes.User
		person impulse.UUID // the person the role is taken from; userID when unset
		role   accesstypes.Role
		// memberships are the committed rows before the call.
		memberships []access.MembershipKey
		opts        []Option
		wantErr     func(error) bool
		wantWrites  []string
	}{
		{
			name:        "the tenant's own membership goes",
			caller:      "erin",
			role:        "Reader",
			memberships: []access.MembershipKey{membership(alphaScope, "dana", "Reader"), membership(globalScope, "dana", "Steward")},
			wantWrites:  []string{"delete membership domain/alpha dana Reader"},
		},
		{
			name:        "a role held both ways: the tenant's own goes, the one held in every tenant stands",
			caller:      "erin",
			role:        "Reader",
			memberships: []access.MembershipKey{membership(alphaScope, "dana", "Reader"), membership(everyScope, "dana", "Reader")},
			wantWrites:  []string{"delete membership domain/alpha dana Reader"},
		},
		{
			name:        "a role held in every tenant alone is not the tenant's to take",
			caller:      "erin",
			role:        "Reader",
			memberships: []access.MembershipKey{membership(everyScope, "dana", "Reader")},
			wantErr:     httpio.HasConflict,
		},
		{
			name:        "a role held in another tenant is not this tenant's: nothing is written",
			caller:      "erin",
			role:        "Reader",
			memberships: []access.MembershipKey{membership(betaScope, "dana", "Reader")},
		},
		{
			name:        "the last role anywhere takes the account with it",
			caller:      "erin",
			role:        "Reader",
			memberships: []access.MembershipKey{membership(alphaScope, "dana", "Reader")},
			wantWrites:  []string{"delete user " + userID.String(), "delete membership domain/alpha dana Reader"},
		},
		{
			name:        "the tenant's last administrator keeps their own role",
			caller:      "dana",
			role:        "Warden",
			memberships: []access.MembershipKey{membership(alphaScope, "dana", "Warden"), membership(alphaScope, "erin", "Reader")},
			opts:        []Option{administrators},
			wantErr:     httpio.HasForbidden,
		},
		{name: "a person that does not exist is not found", caller: "erin", person: createdID, role: "Reader", wantErr: httpio.HasNotFound},
		{name: "an empty role is refused", caller: "erin", wantErr: httpio.HasBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, memberships: map[access.MembershipKey]bool{}}
			for _, key := range tt.memberships {
				store.memberships[key] = true
			}
			m, _ := newManager(t, store, engineRoles, tt.opts...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = inAlpha(ctx, tt.caller)
				tenant, err := m.Tenant(ctx)
				if err != nil {
					return err
				}

				person := tt.person
				if person == impulse.NilUUID {
					person = userID
				}

				return tenant.RemoveRole(ctx, txn, person, tt.role)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("RemoveRole() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("RemoveRole() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTenant_CreateUser pins a person created by a tenant: the roles are held in the
// request's tenant, whatever scope the account names.
func TestTenant_CreateUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		account    NewAccount
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name:       "the roles are held in the request's tenant",
			account:    NewAccount{Username: "pat", Roles: []accesstypes.Role{"Reader"}},
			wantWrites: []string{"insert user pat <none>", "insert membership domain/alpha pat Reader"},
		},
		{
			name:       "a scope the account names is not read",
			account:    NewAccount{Username: "pat", Scope: everyScope, Roles: []accesstypes.Role{"Reader"}},
			wantWrites: []string{"insert user pat <none>", "insert membership domain/alpha pat Reader"},
		},
		{
			name:       "a role of the whole application is no role of a tenant",
			account:    NewAccount{Username: "pat", Scope: globalScope, Roles: []accesstypes.Role{"Steward"}},
			wantErr:    httpio.HasNotFound,
			wantWrites: []string{"insert user pat <none>"},
		},
		{name: "no role is refused", account: NewAccount{Username: "pat"}, wantErr: httpio.HasBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, nextID: createdID}
			m, _ := newManager(t, store, engineRoles)

			var got impulse.UUID
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = inAlpha(ctx, "erin")
				tenant, err := m.Tenant(ctx)
				if err != nil {
					return err
				}
				account := tt.account
				got, err = tenant.CreateUser(ctx, txn, &account)

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("CreateUser() error = %v, want the refusal", err)
				}
			} else {
				if err != nil {
					t.Fatalf("CreateUser() error = %v", err)
				}
				if got != createdID {
					t.Errorf("CreateUser() = %s, want the new user's id %s", got, createdID)
				}
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTenant_Invite pins an invitation a tenant sends: it seats whoever accepts it in
// the request's tenant, whatever tenant the invitation names.
func TestTenant_Invite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		inv        Invitation
		wantErr    func(error) bool
		wantWrites []string
		wantMailed []string
	}{
		{
			name:       "the invitation names the request's tenant",
			inv:        Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Reader"}},
			wantWrites: []string{"insert invitation " + invitationID.String() + " pat@example.com tenant=alpha roles=Reader by=<nobody>"},
			wantMailed: []string{"pat@example.com"},
		},
		{
			name:       "a tenant the invitation names is not read",
			inv:        Invitation{Email: "pat@example.com", Domain: ptr("beta"), Roles: []accesstypes.Role{"Reader"}},
			wantWrites: []string{"insert invitation " + invitationID.String() + " pat@example.com tenant=alpha roles=Reader by=<nobody>"},
			wantMailed: []string{"pat@example.com"},
		},
		{
			name:    "a role of the whole application is no role of a tenant",
			inv:     Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}},
			wantErr: httpio.HasBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{}
			mail := &fakeMail{id: invitationID}
			m := newInviter(t, store, mail)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = inAlpha(ctx, "erin")
				tenant, err := m.Tenant(ctx)
				if err != nil {
					return err
				}
				inv := tt.inv
				_, err = tenant.Invite(ctx, txn, &inv, nil)

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("Invite() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("Invite() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantMailed, mail.sent); diff != "" {
				t.Errorf("addresses mailed (-want +got):\n%s", diff)
			}
		})
	}
}
