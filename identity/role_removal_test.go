package identity

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// The sentences of a role's removal the tests read, word for word, in an application
// that calls a tenant a sector.
const (
	roleGoesConfirmSays        = "Take the role away"
	roleNoLongerHeldSays       = "This person no longer holds that role. Refresh the page to see what they hold."
	roleHeldEverywhereSays     = "This person holds the role Reader in every sector, which is not one sector's to take away."
	roleNamedNoneSays          = "No role is named. Name the role to take away."
	lastSuperAdministratorSays = "This person is the last super administrator who can sign in. Give another person the Super administrator role before you take the role from them."
)

// roleRemovalRoles are the roles the engine of these tests holds.
var roleRemovalRoles = map[accesstypes.PolicyScope][]accesstypes.Role{
	globalScope: {"Steward", accesstypes.SuperAdministrator},
	alphaScope:  {"Reader", "NightShiftLead"},
	betaScope:   {"Reader"},
	everyScope:  {"Reader"},
}

// roleRemovalManager builds a manager over the store that calls a tenant a sector, mails
// through a mailbox, names Warden as the role that administers a tenant, and is given
// the names of the tenants by the session library.
func roleRemovalManager(t *testing.T, store Store, tenantNames map[accesstypes.Domain]string) (*Manager, *fakeEngine, *fakeMail) {
	t.Helper()

	mail := &fakeMail{}
	engine := &fakeEngine{roles: roleRemovalRoles}
	m, err := New(managedAuth, store, engine, fakeSessions{mail: mail, tenantNames: tenantNames}, WithTenantWord("sector"), administrators)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m, engine, mail
}

// TestManager_RoleRemovalWarning pins what an administrator is told before a role is
// taken from a person, from the judgment the removal runs: whose role goes and where, the
// tenant named by what the application calls it and otherwise as "this sector", with the
// one button and no note, the last role the person holds among them. A role the person
// no longer holds,
// an account that is not there, and a removal the rules refuse are answered as refusals.
// Each warning is one a dialog can draw (resource.CheckWarning), and reading it writes
// nothing.
func TestManager_RoleRemovalWarning(t *testing.T) {
	t.Parallel()

	ended, _, _ := periodFixtures()
	named := map[accesstypes.Domain]string{"alpha": "North Yard"}
	tests := []struct {
		name string
		// held are the memberships committed; store adds what else the case holds.
		held   []access.MembershipKey
		store  func(store *fakeStore)
		names  map[accesstypes.Domain]string
		caller accesstypes.User
		person impulse.UUID // the person the role is taken from; userID when unset
		scope  accesstypes.PolicyScope
		role   accesstypes.Role
		want   *resource.Warning
		// wantErr is the refusal, and wantSays the sentence it reads.
		wantErr  func(error) bool
		wantSays string
	}{
		{
			name: "a role held for the whole application names no place",
			held: []access.MembershipKey{danaSteward, danaAlphaReader}, caller: erin, scope: globalScope, role: "Steward",
			want: &resource.Warning{Consequence: "dana loses the role Steward.", Names: nil, Confirm: roleGoesConfirmSays, Also: nil},
		},
		{
			name: "a role held in every tenant says so",
			held: []access.MembershipKey{danaEvery, danaSteward}, caller: erin, scope: everyScope, role: "Reader",
			want: &resource.Warning{Consequence: "dana loses the role Reader in every sector.", Names: nil, Confirm: roleGoesConfirmSays, Also: nil},
		},
		{
			name: "a role held in a tenant the application names says its name",
			held: []access.MembershipKey{danaAlphaReader, danaSteward}, names: named, caller: erin, scope: alphaScope, role: "Reader",
			want: &resource.Warning{Consequence: "dana loses the role Reader in the sector North Yard.", Names: nil, Confirm: roleGoesConfirmSays, Also: nil},
		},
		{
			name: "a role held in a tenant the application does not name says this sector",
			held: []access.MembershipKey{danaAlphaReader, danaSteward}, names: map[accesstypes.Domain]string{"beta": "South Yard"}, caller: erin, scope: alphaScope, role: "Reader",
			want: &resource.Warning{Consequence: "dana loses the role Reader in this sector.", Names: nil, Confirm: roleGoesConfirmSays, Also: nil},
		},
		{
			name: "the last role anywhere: the one button and no note",
			held: []access.MembershipKey{danaAlphaLead}, names: named, caller: erin, scope: alphaScope, role: "NightShiftLead",
			want: &resource.Warning{Consequence: "dana loses the role Night shift lead in the sector North Yard.", Names: nil, Confirm: roleGoesConfirmSays, Also: nil},
		},
		{
			name: "the last role of a person who has a place in a group",
			held: []access.MembershipKey{danaSteward}, caller: erin, scope: globalScope, role: "Steward",
			store: func(store *fakeStore) { store.groupMembers[memberKey(staffID, userID)] = true },
			want:  &resource.Warning{Consequence: "dana loses the role Steward.", Names: nil, Confirm: roleGoesConfirmSays, Also: nil},
		},
		{
			name: "a role whose period is over, and the last thing held: the one button and no note",
			held: []access.MembershipKey{danaAlphaReader}, caller: erin, scope: alphaScope, role: "Reader",
			store: func(store *fakeStore) { store.membershipPeriods[danaAlphaReader] = ended },
			want:  &resource.Warning{Consequence: "dana loses the role Reader in this sector.", Names: nil, Confirm: roleGoesConfirmSays, Also: nil},
		},
		{
			name: "a role the person no longer holds is refused, and says to refresh",
			held: []access.MembershipKey{danaSteward}, caller: erin, scope: globalScope, role: "Reader",
			wantErr: httpio.HasNotFound, wantSays: roleNoLongerHeldSays,
		},
		{
			name: "an account that is not there is not found",
			held: []access.MembershipKey{danaSteward}, caller: erin, person: frankID, scope: globalScope, role: "Steward",
			wantErr: httpio.HasNotFound, wantSays: accountNotFoundMessage,
		},
		{
			name: "the last super administrator who can sign in: the removal's own refusal",
			held: []access.MembershipKey{danaSuper, danaSteward}, scope: globalScope, role: accesstypes.SuperAdministrator,
			wantErr: httpio.HasForbidden, wantSays: lastSuperAdministratorSays,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := leavingStore()
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			if tt.store != nil {
				tt.store(store)
			}
			m, engine, mail := roleRemovalManager(t, store, tt.names)
			person := tt.person
			if person == impulse.NilUUID {
				person = userID
			}

			var warning *resource.Warning
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				if tt.caller != "" {
					ctx = asCallerThrough(ctx, managedAuth, tt.caller)
				}
				var err error
				warning, err = m.RoleRemovalWarning(ctx, txn, tt.scope, person, tt.role)

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) || httpio.Message(err) != tt.wantSays {
					t.Fatalf("RoleRemovalWarning() = %v, error = %v, want the refusal saying %q", warning, err, tt.wantSays)
				}
			} else {
				if err != nil {
					t.Fatalf("RoleRemovalWarning() error = %v", err)
				}
				if diff := cmp.Diff(tt.want, warning); diff != "" {
					t.Errorf("RoleRemovalWarning() (-want +got):\n%s", diff)
				}
				if err := resource.CheckWarning("RemoveMembersRole", warning); err != nil {
					t.Errorf("resource.CheckWarning() error = %v", err)
				}
			}
			if len(store.writes) != 0 || engine.changed != 0 || len(mail.asked) != 0 {
				t.Errorf("buffered writes = %v, the engine told %d time(s) and notices %v, want nothing from a warning", store.writes, engine.changed, mail.asked)
			}
		})
	}
}

// TestTenant_RoleRemovalWarning pins the warning before a tenant's method takes a role
// away, read as the removal reads the role: the tenant's own membership of it is said to
// go in "this sector", whatever the application calls the tenant; a role held in every
// tenant alone is refused as the removal refuses it; a role held neither way is refused
// and says to refresh; and an empty role is refused. Reading it writes nothing.
func TestTenant_RoleRemovalWarning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		held     []access.MembershipKey
		role     accesstypes.Role
		want     *resource.Warning
		wantErr  func(error) bool
		wantSays string
	}{
		{
			name: "the tenant's own role goes in this sector",
			held: []access.MembershipKey{danaAlphaReader, danaSteward}, role: "Reader",
			want: &resource.Warning{Consequence: "dana loses the role Reader in this sector.", Names: nil, Confirm: roleGoesConfirmSays, Also: nil},
		},
		{
			name: "a role held in every tenant alone is not the tenant's to take",
			held: []access.MembershipKey{danaEvery, danaSteward}, role: "Reader",
			wantErr: httpio.HasConflict, wantSays: roleHeldEverywhereSays,
		},
		{
			name: "a role held neither way is refused, and says to refresh",
			held: []access.MembershipKey{danaBetaReader}, role: "Reader",
			wantErr: httpio.HasNotFound, wantSays: roleNoLongerHeldSays,
		},
		{
			name:    "an empty role is refused",
			held:    []access.MembershipKey{danaAlphaReader},
			wantErr: httpio.HasBadRequest, wantSays: roleNamedNoneSays,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := leavingStore()
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			m, engine, mail := roleRemovalManager(t, store, map[accesstypes.Domain]string{"alpha": "North Yard"})

			var warning *resource.Warning
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = inAlpha(ctx, erin)
				tenant, err := m.Tenant(ctx)
				if err != nil {
					return err
				}
				warning, err = tenant.RoleRemovalWarning(ctx, txn, userID, tt.role)

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) || httpio.Message(err) != tt.wantSays {
					t.Fatalf("RoleRemovalWarning() = %v, error = %v, want the refusal saying %q", warning, err, tt.wantSays)
				}
			} else {
				if err != nil {
					t.Fatalf("RoleRemovalWarning() error = %v", err)
				}
				if diff := cmp.Diff(tt.want, warning); diff != "" {
					t.Errorf("RoleRemovalWarning() (-want +got):\n%s", diff)
				}
			}
			if len(store.writes) != 0 || engine.changed != 0 || len(mail.asked) != 0 {
				t.Errorf("buffered writes = %v, the engine told %d time(s) and notices %v, want nothing from a warning", store.writes, engine.changed, mail.asked)
			}
		})
	}
}

// TestManager_RoleRemovalWarning_agreesWithRemoveRole pins that the warning and the act
// read one judgment: the warning says nothing of the account and the removal that follows
// deletes none, where the role is the last thing the person holds and where it is not,
// and an id no user holds is not found before anything is said.
func TestManager_RoleRemovalWarning_agreesWithRemoveRole(t *testing.T) {
	t.Parallel()

	steward := access.NewMembershipKey(globalScope, dana, "Steward")
	reader := access.NewMembershipKey(alphaScope, dana, "Reader")
	tests := []struct {
		name    string
		held    []access.MembershipKey
		account bool
		caller  accesstypes.User
	}{
		{name: "the last role anywhere", held: []access.MembershipKey{steward}, account: true, caller: erin},
		{name: "the last role anywhere, by a call outside any request", held: []access.MembershipKey{steward}, account: true},
		{name: "a person who holds a role in a tenant", held: []access.MembershipKey{steward, reader}, account: true, caller: erin},
		{name: "an id no user holds is not found", held: []access.MembershipKey{steward}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			if tt.account {
				store.usernames = map[impulse.UUID]string{userID: "dana"}
				store.sessions = map[impulse.UUID][]impulse.UUID{userID: {sessionA}}
				store.identities = map[impulse.UUID]impulse.UUID{identityA: userID}
			}
			m, _ := newManager(t, store, engineRoles, administrators)

			var warning *resource.Warning
			warned := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				warning, err = m.RoleRemovalWarning(asCaller(ctx, tt.caller), txn, globalScope, userID, "Steward")

				return err
			})
			if !tt.account {
				if warned == nil || !httpio.HasNotFound(warned) {
					t.Fatalf("RoleRemovalWarning() = %v, error = %v, want the account not found", warning, warned)
				}
			} else if warned != nil {
				t.Fatalf("RoleRemovalWarning() error = %v", warned)
			}
			if len(store.writes) != 0 {
				t.Fatalf("buffered writes = %v, want nothing from a warning", store.writes)
			}
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveRole(asCaller(ctx, tt.caller), txn, globalScope, userID, "Steward")
			}); err != nil {
				t.Fatalf("RemoveRole() error = %v", err)
			}
			if warning != nil && (len(warning.Also) != 0 || warning.Confirm != roleGoesConfirmSays) {
				t.Errorf("RoleRemovalWarning() = %+v, want the one button and no note", warning)
			}
			if diff := cmp.Diff([]string{"delete membership " + membershipText(&steward)}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}
