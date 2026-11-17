package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// superRoles are the roles the engine of these tests holds: the fixture's, and the Super
// Administrator role in the global scope, where the framework defines it.
var superRoles = map[accesstypes.PolicyScope][]accesstypes.Role{
	globalScope: {"Steward", accesstypes.SuperAdministrator},
	alphaScope:  {"Reader"},
	everyScope:  {"Reader"},
}

// The memberships of the tests: dana's and erin's Super Administrator role, and what
// else they hold.
var (
	danaSuper   = access.NewMembershipKey(globalScope, dana, accesstypes.SuperAdministrator)
	erinSuper   = access.NewMembershipKey(globalScope, erin, accesstypes.SuperAdministrator)
	danaSteward = access.NewMembershipKey(globalScope, dana, "Steward")
	erinSteward = access.NewMembershipKey(globalScope, erin, "Steward")
)

// viewedBy makes the request's session an impersonated one whose actor, the real person
// behind it, is the account: the caller the context already carries is the person the
// session views as, whose grants the entry check ran with.
func viewedBy(ctx context.Context, actor impulse.UUID) context.Context {
	session := &sessioninfo.SessionData{Impersonation: &sessioninfo.Impersonation{ActorID: impulse.NullUUIDFromUUID(actor)}}

	return context.WithValue(ctx, sessioninfo.CtxSessionInfo, session)
}

// superStore is a store holding dana and erin as accounts that can sign in, with the
// memberships held.
func superStore(held ...access.MembershipKey) *fakeStore {
	store := &fakeStore{
		usernames:   map[impulse.UUID]string{userID: "dana", otherUser: "erin"},
		disabled:    map[impulse.UUID]bool{},
		memberships: map[access.MembershipKey]bool{},
	}
	for _, key := range held {
		store.memberships[key] = true
	}

	return store
}

// TestManager_AssignRole_superAdministrator pins who gives the Super Administrator role
// and where: a caller who holds it, and the application outside any request, in the
// global scope alone. A caller who does not hold it is refused, whatever else they hold,
// and so is one who came through another auth, who holds no role of this one. A super
// administrator gives any other role in a tenant, which the rules never refuse them.
func TestManager_AssignRole_superAdministrator(t *testing.T) {
	t.Parallel()

	given := "insert membership " + membershipText(&danaSuper)
	tests := []struct {
		name string
		ctx  func(context.Context) context.Context
		// held are the memberships committed; scope and role what is given to dana.
		held    []access.MembershipKey
		scope   accesstypes.PolicyScope
		role    accesstypes.Role
		wantErr func(error) bool
		// wantSays is a part of the sentence a refusal reads.
		wantSays   string
		wantWrites []string
	}{
		{
			name:  "a super administrator makes another",
			ctx:   func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, erin) },
			held:  []access.MembershipKey{erinSuper},
			scope: globalScope, role: accesstypes.SuperAdministrator,
			wantWrites: []string{given},
		},
		{
			name:  "a caller who does not hold the role is refused it, an administrator of everything else included",
			ctx:   func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, erin) },
			held:  []access.MembershipKey{erinSteward},
			scope: globalScope, role: accesstypes.SuperAdministrator,
			wantErr: httpio.HasForbidden, wantSays: "only a super administrator gives the Super Administrator role, and you do not hold the Super Administrator role",
		},
		{
			name:  "a caller cannot give themselves the role they do not hold",
			ctx:   func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) },
			held:  []access.MembershipKey{danaSteward},
			scope: globalScope, role: accesstypes.SuperAdministrator,
			wantErr: httpio.HasForbidden, wantSays: "only a super administrator gives",
		},
		{
			name:  "a caller who came through another auth holds no role of this one, whatever their id holds here",
			ctx:   func(ctx context.Context) context.Context { return asCallerThrough(ctx, otherAuth, erin) },
			held:  []access.MembershipKey{erinSuper},
			scope: globalScope, role: accesstypes.SuperAdministrator,
			wantErr: httpio.HasForbidden, wantSays: "only a super administrator gives",
		},
		{
			name: "a person who views as a super administrator is not one: the session's actor is judged, not the person viewed as",
			ctx: func(ctx context.Context) context.Context {
				return viewedBy(asCallerThrough(ctx, managedAuth, erin), userID)
			},
			held:  []access.MembershipKey{erinSuper, danaSteward},
			scope: globalScope, role: accesstypes.SuperAdministrator,
			wantErr: httpio.HasForbidden, wantSays: "only a super administrator gives the Super Administrator role, and you do not hold the Super Administrator role",
		},
		{
			name: "a super administrator who views as someone else is a super administrator still",
			ctx: func(ctx context.Context) context.Context {
				return viewedBy(asCallerThrough(ctx, managedAuth, dana), otherUser)
			},
			held:  []access.MembershipKey{erinSuper, danaSteward},
			scope: globalScope, role: accesstypes.SuperAdministrator,
			wantWrites: []string{given},
		},
		{
			name: "an actor from another application holds no role here, whoever the session views as",
			ctx: func(ctx context.Context) context.Context {
				foreign := &sessioninfo.SessionData{Impersonation: &sessioninfo.Impersonation{Actor: string(erin), ActorRealm: "another-application"}}

				return context.WithValue(asCallerThrough(ctx, managedAuth, erin), sessioninfo.CtxSessionInfo, foreign)
			},
			held:  []access.MembershipKey{erinSuper},
			scope: globalScope, role: accesstypes.SuperAdministrator,
			wantErr: httpio.HasForbidden, wantSays: "only a super administrator gives",
		},
		{
			name:  "a call outside any request is the application's act: the deployment's first administrator",
			ctx:   func(ctx context.Context) context.Context { return ctx },
			scope: globalScope, role: accesstypes.SuperAdministrator,
			wantWrites: []string{given},
		},
		{
			name:  "the role is refused in a tenant, a super administrator's hand included",
			ctx:   func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, erin) },
			held:  []access.MembershipKey{erinSuper},
			scope: alphaScope, role: accesstypes.SuperAdministrator,
			wantErr: httpio.HasBadRequest, wantSays: "the SuperAdministrator role is held for the whole application alone, never in a tenant or in every tenant",
		},
		{
			name:  "the role is refused in every tenant",
			ctx:   func(ctx context.Context) context.Context { return ctx },
			scope: everyScope, role: accesstypes.SuperAdministrator,
			wantErr: httpio.HasBadRequest, wantSays: "held for the whole application alone",
		},
		{
			name:  "a super administrator gives a role in a tenant",
			ctx:   func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, erin) },
			held:  []access.MembershipKey{erinSuper},
			scope: alphaScope, role: "Reader",
			wantWrites: []string{"insert membership domain/alpha " + userID.String() + " Reader"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := superStore(tt.held...)
			m, _ := newManager(t, store, superRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.AssignRole(tt.ctx(ctx), txn, tt.scope, userID, tt.role)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) || !strings.Contains(httpio.Message(err), tt.wantSays) {
					t.Fatalf("AssignRole() error = %v, want the refusal saying %q", err, tt.wantSays)
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

// TestTenant_AssignRole_superAdministrator pins that a tenant's own method gives no
// Super Administrator role: a tenant has none.
func TestTenant_AssignRole_superAdministrator(t *testing.T) {
	t.Parallel()

	store := superStore(erinSuper)
	m, _ := newManager(t, store, superRoles, WithTenantWord("customer"))
	tenant := &Tenant{manager: m, domain: "alpha"}

	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return tenant.AssignRole(asCallerThrough(ctx, managedAuth, erin), txn, userID, accesstypes.SuperAdministrator)
	})
	want := "the SuperAdministrator role is held for the whole application alone, never in a customer or in every customer: give it with no customer named"
	if !httpio.HasBadRequest(err) || httpio.Message(err) != want {
		t.Fatalf("Tenant.AssignRole() error = %v, want the refusal reading %q", err, want)
	}
	if len(store.writes) != 0 {
		t.Errorf("buffered writes = %v, want none", store.writes)
	}
}

// TestManager_Invite_superAdministrator pins that an invitation gives the role only by a
// super administrator's hand, and never into a tenant.
func TestManager_Invite_superAdministrator(t *testing.T) {
	t.Parallel()

	alpha := "alpha"
	tests := []struct {
		name     string
		held     []access.MembershipKey
		domain   *string
		wantErr  func(error) bool
		wantSays string
		wantRows int
	}{
		{name: "a super administrator invites a person to the role", held: []access.MembershipKey{erinSuper}, wantRows: 1},
		{name: "a caller who does not hold the role is refused", held: []access.MembershipKey{erinSteward}, wantErr: httpio.HasForbidden, wantSays: "only a super administrator invites a person to the Super Administrator role"},
		{name: "an invitation into a tenant never gives it", held: []access.MembershipKey{erinSuper}, domain: &alpha, wantErr: httpio.HasBadRequest, wantSays: `role "SuperAdministrator" does not exist where the invitation gives it`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := superStore(tt.held...)
			m, err := New(managedAuth, store, &fakeEngine{roles: superRoles}, fakeSessions{mail: &fakeMail{}})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				_, err := m.Invite(asCallerThrough(ctx, managedAuth, erin), txn, &Invitation{Email: "frank@example.com", Domain: tt.domain, Roles: []accesstypes.Role{accesstypes.SuperAdministrator}}, nil)

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) || !strings.Contains(httpio.Message(err), tt.wantSays) {
					t.Fatalf("Invite() error = %v, want the refusal saying %q", err, tt.wantSays)
				}
			} else if err != nil {
				t.Fatalf("Invite() error = %v", err)
			}
			if len(store.writes) != tt.wantRows {
				t.Errorf("buffered writes = %v, want %d", store.writes, tt.wantRows)
			}
		})
	}
}

// TestManager_lastSuperAdministrator pins the rule that there is always a super
// administrator who can sign in, and that only a super administrator removes one. The
// role is taken away, and a holder's account deleted, by a caller who holds the role.
// From the last holder who can sign in the role is taken by nobody, and their account
// is neither disabled nor deleted: not by themselves, not by an administrator, not by
// another holder whose own account cannot sign in, and not by the application outside
// any request. A holder who cannot sign in is nobody's way back in, so they do not
// count as another.
func TestManager_lastSuperAdministrator(t *testing.T) {
	t.Parallel()

	remove := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.RemoveRole(ctx, txn, globalScope, userID, accesstypes.SuperAdministrator)
	}
	disable := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.DisableUser(ctx, txn, userID)
	}
	deleteUser := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.DeleteUser(ctx, txn, userID)
	}
	removed := "delete membership " + membershipText(&danaSuper)
	const last = "this person is the last super administrator who can sign in: give another person the Super Administrator role before you "

	tests := []struct {
		name string
		act  func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		// caller is who asks, "" for a call outside any request; held the memberships
		// committed beside dana's Steward role; erinDisabled an erin who cannot sign in.
		caller       accesstypes.User
		held         []access.MembershipKey
		erinDisabled bool
		danaDisabled bool
		wantErr      func(error) bool
		wantSays     string
		wantWrites   []string
	}{
		// Taking the role away.
		{
			name: "a super administrator takes the role from another while they hold it themselves",
			act:  remove, caller: erin, held: []access.MembershipKey{danaSuper, erinSuper},
			wantWrites: []string{removed},
		},
		{
			name: "a super administrator gives the role up while another holds it",
			act:  remove, caller: dana, held: []access.MembershipKey{danaSuper, erinSuper},
			wantWrites: []string{removed},
		},
		{
			name: "a caller who does not hold the role takes it from nobody",
			act:  remove, caller: erin, held: []access.MembershipKey{danaSuper, erinSteward},
			wantErr: httpio.HasForbidden, wantSays: "only a super administrator takes the Super Administrator role away, and you do not hold the Super Administrator role",
		},
		{
			name: "the last holder does not give the role up",
			act:  remove, caller: dana, held: []access.MembershipKey{danaSuper, erinSteward},
			wantErr: httpio.HasForbidden, wantSays: "you are the last super administrator who can sign in: give another person the Super Administrator role before you remove your own",
		},
		{
			name: "another holder whose account cannot sign in does not take it from the last who can",
			act:  remove, caller: erin, held: []access.MembershipKey{danaSuper, erinSuper}, erinDisabled: true,
			wantErr: httpio.HasForbidden, wantSays: last + "take the role from them",
		},
		{
			name: "the last holder does not give the role up while the other holder cannot sign in",
			act:  remove, caller: dana, held: []access.MembershipKey{danaSuper, erinSuper}, erinDisabled: true,
			wantErr: httpio.HasForbidden, wantSays: "you are the last super administrator who can sign in",
		},
		{
			name: "the application outside any request does not take it from the last holder either",
			act:  remove, held: []access.MembershipKey{danaSuper},
			wantErr: httpio.HasForbidden, wantSays: last + "take the role from them",
		},
		{
			name: "a holder who cannot sign in is nobody's way back in: the role is taken from them",
			act:  remove, caller: erin, held: []access.MembershipKey{danaSuper, erinSuper}, danaDisabled: true,
			wantWrites: []string{removed},
		},
		// Disabling the account.
		{
			name: "the last holder's account is disabled by nobody: an administrator who holds the grant",
			act:  disable, caller: erin, held: []access.MembershipKey{danaSuper, erinSteward},
			wantErr: httpio.HasForbidden, wantSays: last + "disable their account",
		},
		{
			name: "the last holder's account is disabled by nobody: another holder who cannot sign in",
			act:  disable, caller: erin, held: []access.MembershipKey{danaSuper, erinSuper}, erinDisabled: true,
			wantErr: httpio.HasForbidden, wantSays: last + "disable their account",
		},
		{
			name: "the last holder's account is disabled by nobody: the application outside any request",
			act:  disable, held: []access.MembershipKey{danaSuper},
			wantErr: httpio.HasForbidden, wantSays: last + "disable their account",
		},
		{
			name: "a holder's account is disabled while another holder can sign in",
			act:  disable, caller: erin, held: []access.MembershipKey{danaSuper, erinSuper},
			wantWrites: []string{"update user " + userID.String() + " disabled=true"},
		},
		// Deleting the account.
		{
			name: "a holder's account is deleted by a super administrator alone: an administrator is refused",
			act:  deleteUser, caller: erin, held: []access.MembershipKey{danaSuper, erinSteward},
			wantErr: httpio.HasForbidden, wantSays: "only a super administrator deletes a super administrator's account, since the role goes with it",
		},
		{
			name: "the last holder's account is deleted by nobody: another holder who cannot sign in",
			act:  deleteUser, caller: erin, held: []access.MembershipKey{danaSuper, erinSuper}, erinDisabled: true,
			wantErr: httpio.HasForbidden, wantSays: last + "delete their account",
		},
		{
			name: "the last holder's account is deleted by nobody: the application outside any request",
			act:  deleteUser, held: []access.MembershipKey{danaSuper},
			wantErr: httpio.HasForbidden, wantSays: last + "delete their account",
		},
		{
			name: "a holder's account is deleted by another super administrator who can sign in",
			act:  deleteUser, caller: erin, held: []access.MembershipKey{danaSuper, erinSuper},
			wantWrites: []string{"delete user " + userID.String(), "delete membership " + membershipText(&danaSteward), removed},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := superStore(append([]access.MembershipKey{danaSteward}, tt.held...)...)
			store.disabled[otherUser], store.disabled[userID] = tt.erinDisabled, tt.danaDisabled
			m, _ := newManager(t, store, superRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				if tt.caller != "" {
					ctx = asCallerThrough(ctx, managedAuth, tt.caller)
				}

				return tt.act(ctx, txn, m)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) || !strings.Contains(httpio.Message(err), tt.wantSays) {
					t.Fatalf("error = %v, want the refusal saying %q", err, tt.wantSays)
				}
			} else if err != nil {
				t.Fatalf("error = %v, want the act to go through", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// builtEngine is an engine that says which methods it was built to let a super
// administrator execute, as the access library's own does.
type builtEngine struct {
	fakeEngine
	methods []accesstypes.Resource
}

func (e *builtEngine) SuperAdministratorMethods() []accesstypes.Resource {
	return e.methods
}

// TestNew_superAdministratorMethods pins that a manager told the methods that give and
// take the auth's roles refuses an engine built without one of them, naming what the
// application left out, and holds an engine that does not say what it was built with, a
// test's scripted one, to nothing.
func TestNew_superAdministratorMethods(t *testing.T) {
	t.Parallel()

	option := WithSuperAdministratorMethods("AssignMembersRole", "RemoveMembersRole")
	if _, err := New(managedAuth, &fakeStore{}, &builtEngine{methods: []accesstypes.Resource{"AssignMembersRole", "RemoveMembersRole"}}, fakeSessions{}, option); err != nil {
		t.Errorf("New() over an engine built with the methods: error = %v", err)
	}
	for missing, built := range map[accesstypes.Resource][]accesstypes.Resource{"RemoveMembersRole": {"AssignMembersRole"}, "AssignMembersRole": nil} {
		_, err := New(managedAuth, &fakeStore{}, &builtEngine{methods: built}, fakeSessions{}, option)
		want := fmt.Sprintf("the permission engine of the members auth does not let a super administrator execute %s, a method that gives or takes the auth's roles; build the engine with access.WithSuperAdministratorMethods", missing)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("New() over an engine built with %v: error = %v, want one containing %q", built, err, want)
		}
	}
	if _, err := New(managedAuth, &fakeStore{}, &fakeEngine{}, fakeSessions{}, option); err != nil {
		t.Errorf("New() over a scripted engine that does not say what it was built with: error = %v, want none", err)
	}
}

// TestManager_aSuperAdministratorsSignIn pins that how a super administrator signs in is
// changed by a super administrator alone. Each act is a way to become the person or to
// shut them out: a password set for them or taken from them, a change of password
// required of them, a new username, a new address, where the code of their second
// factor is mailed, and the unlinking of an identity. An administrator the role file
// lets manage people is refused each one for a holder, and so is a person who views as
// a super administrator, since the real person behind the session is judged. A super
// administrator acts on another and on themselves, the application acts outside any
// request, and a person who does not hold the role stays whoever's the role file says.
func TestManager_aSuperAdministratorsSignIn(t *testing.T) {
	t.Parallel()

	frank := mustUUID("55555555-5555-4555-8555-555555555555")
	address := "dana@example.com"
	acts := []struct {
		name string
		// says is the act as its refusal words it.
		says string
		act  func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
	}{
		{
			name: "a temporary password", says: "sets a super administrator's password",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.SetTemporaryPassword(ctx, txn, userID, "first-day")
			},
		},
		{
			name: "the removal of the password", says: "removes a super administrator's password",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemovePassword(ctx, txn, userID)
			},
		},
		{
			name: "a required change of password", says: "requires a super administrator to change their password",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RequirePasswordChange(ctx, txn, userID)
			},
		},
		{
			name: "a new username", says: "renames a super administrator",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RenameUser(ctx, txn, userID, "dana.r")
			},
		},
		{
			name: "a new address", says: "changes a super administrator's email address",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.SetEmail(ctx, txn, userID, &address)
			},
		},
		{
			name: "the unlinking of an identity", says: "unlinks a super administrator's sign-in identity",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.UnlinkIdentity(ctx, txn, identityA)
			},
		},
	}
	callers := []struct {
		name string
		ctx  func(context.Context) context.Context
		// held are the memberships committed; the act is on dana.
		held    []access.MembershipKey
		refused bool
	}{
		{
			name:    "an administrator who does not hold the role is refused",
			ctx:     func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, erin) },
			held:    []access.MembershipKey{danaSuper, erinSteward},
			refused: true,
		},
		{
			name: "a person who views as a super administrator is refused: the session's actor is judged",
			ctx: func(ctx context.Context) context.Context {
				return viewedBy(asCallerThrough(ctx, managedAuth, erin), frank)
			},
			held:    []access.MembershipKey{danaSuper, erinSuper},
			refused: true,
		},
		{
			name:    "a caller who came through another auth is refused, whatever their id holds here",
			ctx:     func(ctx context.Context) context.Context { return asCallerThrough(ctx, otherAuth, erin) },
			held:    []access.MembershipKey{danaSuper, erinSuper},
			refused: true,
		},
		{
			name: "a super administrator acts on another",
			ctx:  func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, erin) },
			held: []access.MembershipKey{danaSuper, erinSuper},
		},
		{
			name: "a super administrator acts on themselves",
			ctx:  func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) },
			held: []access.MembershipKey{danaSuper},
		},
		{
			name: "a super administrator who views as someone else acts as one",
			ctx: func(ctx context.Context) context.Context {
				return viewedBy(asCallerThrough(ctx, managedAuth, User(frank)), otherUser)
			},
			held: []access.MembershipKey{danaSuper, erinSuper},
		},
		{
			name: "the application acts outside any request",
			ctx:  func(ctx context.Context) context.Context { return ctx },
			held: []access.MembershipKey{danaSuper},
		},
		{
			name: "a person who does not hold the role is the role file's to manage",
			ctx:  func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, erin) },
			held: []access.MembershipKey{danaSteward, erinSteward},
		},
	}
	for _, act := range acts {
		for _, caller := range callers {
			t.Run(act.name+": "+caller.name, func(t *testing.T) {
				t.Parallel()

				store := superStore(caller.held...)
				store.passwords = map[impulse.UUID]bool{userID: true}
				store.identities = map[impulse.UUID]impulse.UUID{identityA: userID, identityB: userID}
				m, _ := newManager(t, store, superRoles)

				err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					return act.act(caller.ctx(ctx), txn, m)
				})
				if !caller.refused {
					if err != nil {
						t.Fatalf("error = %v, want the act to go through", err)
					}
					if len(store.writes) == 0 {
						t.Error("buffered writes: none, want the act's")
					}

					return
				}
				want := "only a super administrator " + act.says + ", and you do not hold the Super Administrator role"
				if !httpio.HasForbidden(err) || httpio.Message(err) != want {
					t.Fatalf("error = %v, want the refusal reading %q", err, want)
				}
				if len(store.writes) != 0 {
					t.Errorf("buffered writes = %v, want none", store.writes)
				}
			})
		}
	}
}

// TestManager_AssignRole_superAdministratorSignsInAgain pins that giving the Super
// Administrator role ends every session the person holds, in the transaction that gives
// it: their own, and the ones they established as someone else, whose reach is the
// actor's. A super administrator signs in with a second factor and their session enters
// every tenant, so no session that began before the role carries it. The sessions of
// whoever gave the role stand, a person who holds the role already keeps theirs, and
// another role ends nothing.
func TestManager_AssignRole_superAdministratorSignsInAgain(t *testing.T) {
	t.Parallel()

	viewing, givers := mustUUID("66666666-6666-4666-8666-666666666666"), mustUUID("77777777-7777-4777-8777-777777777777")
	given := "insert membership " + membershipText(&danaSuper)
	tests := []struct {
		name       string
		held       []access.MembershipKey
		scope      accesstypes.PolicyScope
		role       accesstypes.Role
		wantWrites []string
	}{
		{
			name:  "the person's own sessions and the ones they hold as actor end, and the giver's stands",
			held:  []access.MembershipKey{erinSuper, danaSteward},
			scope: globalScope, role: accesstypes.SuperAdministrator,
			wantWrites: []string{
				given,
				"end session " + sessionA.String(),
				"end session " + viewing.String(),
				"end impersonation " + viewing.String() + " " + revoked,
			},
		},
		{
			name:  "a person who holds the role already keeps their sessions",
			held:  []access.MembershipKey{erinSuper, danaSuper},
			scope: globalScope, role: accesstypes.SuperAdministrator,
		},
		{
			name:  "another role ends nothing",
			held:  []access.MembershipKey{erinSuper},
			scope: globalScope, role: "Steward",
			wantWrites: []string{"insert membership " + membershipText(&danaSteward)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := superStore(tt.held...)
			store.sessions = map[impulse.UUID][]impulse.UUID{userID: {sessionA}, otherUser: {givers}}
			store.heldBy = map[string][]impulse.UUID{"dana": {viewing}}
			m, _ := newManager(t, store, superRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.AssignRole(asCallerThrough(ctx, managedAuth, erin), txn, tt.scope, userID, tt.role)
			})
			if err != nil {
				t.Fatalf("AssignRole() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}
