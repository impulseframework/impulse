package identity

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// namingEngine is the tests' engine told which roles administer a tenant.
type namingEngine struct {
	*fakeEngine
	administrators []accesstypes.Role
}

func (e namingEngine) TenantAdministratorRoles() []accesstypes.Role {
	return e.administrators
}

// directoryOnlyManager builds a manager over an engine that names administrators and a
// session API that registers methods.
func directoryOnlyManager(t *testing.T, store Store, administrators []accesstypes.Role, methods ...sessioninfo.AuthMethod) *Manager {
	t.Helper()
	registered := make([]session.RegisteredMethod, 0, len(methods))
	for _, method := range methods {
		registered = append(registered, session.RegisteredMethod{Name: string(method), Method: method})
	}
	m, err := New(managedAuth, store, namingEngine{fakeEngine: &fakeEngine{}, administrators: administrators}, fakeSessions{methods: registered})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m
}

// TestManager_AdministratorsEnterWithPassword pins when an auth's tenant administrators
// keep a way into a directory-only tenant: the auth registers the password method and
// its engine names the roles that administer a tenant; and that the roles the manager
// answers are the engine's own.
func TestManager_AdministratorsEnterWithPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		administrators []accesstypes.Role
		methods        []sessioninfo.AuthMethod
		want           bool
	}{
		{name: "a password method and administrator roles", administrators: []accesstypes.Role{"Warden"}, methods: []sessioninfo.AuthMethod{sessioninfo.MethodGoogle, sessioninfo.MethodPassword}, want: true},
		{name: "no password method: nobody has a password to come back with", administrators: []accesstypes.Role{"Warden"}, methods: []sessioninfo.AuthMethod{sessioninfo.MethodGoogle}},
		{name: "no administrator role named: nobody administers a tenant", methods: []sessioninfo.AuthMethod{sessioninfo.MethodPassword}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := directoryOnlyManager(t, &fakeStore{}, tt.administrators, tt.methods...)
			if got := m.AdministratorsEnterWithPassword(); got != tt.want {
				t.Errorf("AdministratorsEnterWithPassword() = %v, want %v", got, tt.want)
			}
			if diff := cmp.Diff(tt.administrators, m.TenantAdministratorRoles()); diff != "" {
				t.Errorf("TenantAdministratorRoles() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_CallerPassword pins what the rule on the directory-only switch is told of
// the person behind the request: whether they hold a password in this auth, and that
// the rule asks nothing where the auth has no password method, where the request came
// through another auth, and outside a request. The person is the real one behind the
// session, never the one it views as.
func TestManager_CallerPassword(t *testing.T) {
	t.Parallel()

	withPassword := []sessioninfo.AuthMethod{sessioninfo.MethodPassword, sessioninfo.MethodGoogle}
	tests := []struct {
		name        string
		ctx         func(context.Context) context.Context
		methods     []sessioninfo.AuthMethod
		passwords   map[impulse.UUID]bool
		failRead    bool
		wantHas     bool
		wantApplies bool
		wantErr     bool
	}{
		{
			name:        "a caller who holds a password",
			ctx:         func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) },
			methods:     withPassword,
			passwords:   map[impulse.UUID]bool{userID: true},
			wantHas:     true,
			wantApplies: true,
		},
		{
			name:        "a caller who has only ever signed in through a directory",
			ctx:         func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) },
			methods:     withPassword,
			passwords:   map[impulse.UUID]bool{userID: false},
			wantApplies: true,
		},
		{
			name:      "an auth with no password method asks nothing of anyone",
			ctx:       func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) },
			methods:   []sessioninfo.AuthMethod{sessioninfo.MethodGoogle},
			passwords: map[impulse.UUID]bool{userID: false},
		},
		{
			name:      "a caller who came through another auth is nobody of this one",
			ctx:       func(ctx context.Context) context.Context { return asCallerThrough(ctx, otherAuth, dana) },
			methods:   withPassword,
			passwords: map[impulse.UUID]bool{userID: false},
		},
		{
			name:      "a call outside any request is the application's own",
			ctx:       func(ctx context.Context) context.Context { return ctx },
			methods:   withPassword,
			passwords: map[impulse.UUID]bool{userID: false},
		},
		{
			name: "a session that views as a person with a password is judged by the person behind it, who has none",
			ctx: func(ctx context.Context) context.Context {
				return viewedBy(asCallerThrough(ctx, managedAuth, dana), otherUser)
			},
			methods:     withPassword,
			passwords:   map[impulse.UUID]bool{userID: true, otherUser: false},
			wantApplies: true,
		},
		{
			name: "a person of another application has no password here",
			ctx: func(ctx context.Context) context.Context {
				foreign := &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{}, Impersonation: &sessioninfo.Impersonation{Actor: "dana", ActorRealm: "admin-portal"}}

				return context.WithValue(asCallerThrough(ctx, managedAuth, dana), sessioninfo.CtxSessionInfo, foreign)
			},
			methods:     withPassword,
			passwords:   map[impulse.UUID]bool{userID: true},
			wantApplies: true,
		},
		{
			name:        "a caller with no account in the auth has no password in it",
			ctx:         func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, User(frankID)) },
			methods:     withPassword,
			passwords:   map[impulse.UUID]bool{userID: true},
			wantApplies: true,
		},
		{
			name:     "a failed read is the answer's error",
			ctx:      func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) },
			methods:  withPassword,
			failRead: true,
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{passwords: tt.passwords}
			if tt.failRead {
				store.failOn = "UserHasPassword"
			}
			m := directoryOnlyManager(t, store, []accesstypes.Role{"Warden"}, tt.methods...)

			var has, applies bool
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				has, applies, err = m.CallerPassword(tt.ctx(ctx), txn)

				return err
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("CallerPassword() error = %v, wantErr %v", err, tt.wantErr)
			}
			if has != tt.wantHas || applies != tt.wantApplies {
				t.Errorf("CallerPassword() = (has %v, applies %v), want (has %v, applies %v)", has, applies, tt.wantHas, tt.wantApplies)
			}
		})
	}
}

// TestManager_AuthSecondFactor pins what the rule that a tenant vouches for its
// directory's own second factor is told of the auth: whether its sign-in settings ask
// everyone for a second factor now, an auth with no settings row asking everyone; and
// that the rule asks nothing of this auth outside a request or for a request that came
// through another auth.
func TestManager_AuthSecondFactor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		ctx          func(context.Context) context.Context
		row          *SignInSettingsRow
		failRead     bool
		wantRequired bool
		wantApplies  bool
		wantErr      bool
	}{
		{
			name:         "an auth with no settings row asks everyone",
			ctx:          func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) },
			wantRequired: true,
			wantApplies:  true,
		},
		{
			name:         "a row that asks everyone",
			ctx:          func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) },
			row:          &SignInSettingsRow{RequireSecondFactor: true, RememberBrowserDays: 30},
			wantRequired: true,
			wantApplies:  true,
		},
		{
			name:        "a row that asks nobody",
			ctx:         func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) },
			row:         &SignInSettingsRow{RememberBrowserDays: 30},
			wantApplies: true,
		},
		{
			name: "a request that came through another auth is not this auth's to answer",
			ctx:  func(ctx context.Context) context.Context { return asCallerThrough(ctx, otherAuth, dana) },
			row:  &SignInSettingsRow{RequireSecondFactor: true, RememberBrowserDays: 30},
		},
		{
			name: "a call outside any request is the application's own",
			ctx:  func(ctx context.Context) context.Context { return ctx },
			row:  &SignInSettingsRow{RequireSecondFactor: true, RememberBrowserDays: 30},
		},
		{
			name:     "a failed read is the answer's error",
			ctx:      func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) },
			failRead: true,
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{settingsRow: tt.row}
			if tt.failRead {
				store.failOn = "SignInSettingsRow"
			}
			m := directoryOnlyManager(t, store, []accesstypes.Role{"Warden"}, sessioninfo.MethodPassword, sessioninfo.MethodGoogle)

			var required, applies bool
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				required, applies, err = m.AuthSecondFactor(tt.ctx(ctx), txn)

				return err
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("AuthSecondFactor() error = %v, wantErr %v", err, tt.wantErr)
			}
			if required != tt.wantRequired || applies != tt.wantApplies {
				t.Errorf("AuthSecondFactor() = (required %v, applies %v), want (required %v, applies %v)", required, applies, tt.wantRequired, tt.wantApplies)
			}
		})
	}
}
