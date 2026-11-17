package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// registeredMethods are the sign-in methods of the auth the sign-up tests manage, one
// of each width: a method limited to one organization, one that admits any account of
// its provider, the password method, and one whose directory gives the roles.
func registeredMethods() []session.RegisteredMethod {
	return []session.RegisteredMethod{
		{Name: "password", Method: sessioninfo.MethodPassword, Label: "Password", Audience: signin.Audience{Accounts: "an email address"}},
		{Name: "workspace", Method: sessioninfo.MethodGoogle, Label: "Acme", Audience: signin.Audience{Organizations: []string{"acme.example"}}},
		{Name: "google", Method: sessioninfo.MethodGoogle, Label: "Google", Audience: signin.Audience{Accounts: "a Google account"}},
		{Name: "entra", Method: sessioninfo.MethodAzure, Label: "Staff directory", Audience: signin.Audience{Organizations: []string{"Lakeside"}}, SyncsRoles: true},
	}
}

// newSignUpManager builds a manager of an auth that registers registeredMethods, over
// the store and the engine's roles.
func newSignUpManager(t *testing.T, store Store, opts ...Option) *Manager {
	t.Helper()

	m, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, fakeSessions{methods: registeredMethods()}, opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m
}

func TestManager_OpenSignUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		open SignUp
		// wantWrites are the store's buffered writes; none for a refusal.
		wantWrites []string
		// refused says how a refusal answers, and wantHas what its words say.
		refused func(err error) bool
		wantHas string
	}{
		{
			name:       "without tenants, a method is opened with its default role",
			open:       SignUp{Method: "google", DefaultRole: "Steward"},
			wantWrites: []string{"set sign-in method google open=true role=Steward"},
		},
		{
			name:    "without tenants, open without a default role is refused at the save",
			open:    SignUp{Method: "google"},
			refused: httpio.HasBadRequest, wantHas: "choose the default role",
		},
		{
			name:    "a default role that is not held for the whole application is refused",
			open:    SignUp{Method: "google", DefaultRole: "Reader"},
			refused: httpio.HasBadRequest, wantHas: "is not a role held for the whole application: choose one of the roles the picker lists",
		},
		{
			name:    "the Super Administrator role is never a default role",
			open:    SignUp{Method: "google", DefaultRole: accesstypes.SuperAdministrator},
			refused: httpio.HasBadRequest, wantHas: "is never a default role",
		},
		{
			name:       "with tenants, a method is opened with no role",
			open:       SignUp{Method: "google", CreatesTenant: true},
			wantWrites: []string{"set sign-in method google open=true role=<none>"},
		},
		{
			name:    "with tenants, a default role is refused",
			open:    SignUp{Method: "google", DefaultRole: "Steward", CreatesTenant: true},
			refused: httpio.HasBadRequest, wantHas: "no default role is chosen here",
		},
		{
			name:    "a method the auth does not register is refused, and its methods named",
			open:    SignUp{Method: "github", DefaultRole: "Steward"},
			refused: httpio.HasNotFound, wantHas: "its methods are password, workspace, google, entra",
		},
		{
			name:    "a method whose directory gives the roles is refused",
			open:    SignUp{Method: "entra", DefaultRole: "Steward"},
			refused: httpio.HasConflict, wantHas: "takes its roles from its directory",
		},
		{
			name:       "the password method is opened like any other",
			open:       SignUp{Method: "password", DefaultRole: "Steward"},
			wantWrites: []string{"set sign-in method password open=true role=Steward"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{roles: map[access.RoleKey]bool{}}
			m := newSignUpManager(t, store)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.OpenSignUp(ctx, txn, &tt.open)
			})

			if tt.refused == nil {
				if err != nil {
					t.Fatalf("OpenSignUp() error = %v", err)
				}
			} else if !tt.refused(err) || !containsText(err, tt.wantHas) {
				t.Fatalf("OpenSignUp() error = %v, want the refusal saying %q", err, tt.wantHas)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

func TestManager_OpenSignUpWarning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		open    SignUp
		want    *resource.Warning
		refused func(err error) bool
	}{
		{
			name: "a method limited to its organization, with the role picked",
			open: SignUp{Method: "workspace", DefaultRole: "Steward"},
			want: &resource.Warning{
				Consequence: "Anyone with an account at acme.example will be able to sign in without an invitation. Each of them gets the role Steward.",
				Confirm:     "Open to anyone with an account at acme.example",
			},
		},
		{
			name: "a method that admits any account of its provider, with the role picked",
			open: SignUp{Method: "google", DefaultRole: "Steward"},
			want: &resource.Warning{
				Consequence: "Anyone with a Google account will be able to sign in without an invitation. Each of them gets the role Steward.",
				Confirm:     "Open to anyone with a Google account",
			},
		},
		{
			name: "the password method signs people up",
			open: SignUp{Method: "password", DefaultRole: "Steward"},
			want: &resource.Warning{
				Consequence: "Anyone with an email address will be able to sign up without an invitation. Each of them gets the role Steward.",
				Confirm:     "Open to anyone with an email address",
			},
		},
		{
			name: "before the role is picked, the warning says it is to be chosen",
			open: SignUp{Method: "google"},
			want: &resource.Warning{
				Consequence: "Anyone with a Google account will be able to sign in without an invitation. Choose the role each of them gets.",
				Confirm:     "Open to anyone with a Google account",
			},
		},
		{
			name: "with tenants, a person who signs up creates a tenant of their own",
			open: SignUp{Method: "google", CreatesTenant: true},
			want: &resource.Warning{
				Consequence: "Anyone with a Google account will be able to sign up and create a tenant of their own, without an invitation.",
				Confirm:     "Open to anyone with a Google account",
			},
		},
		{name: "a role that is not held for the whole application is refused", open: SignUp{Method: "google", DefaultRole: "Reader"}, refused: httpio.HasBadRequest},
		{name: "a method the auth does not register is refused", open: SignUp{Method: "github"}, refused: httpio.HasNotFound},
		{name: "a method whose directory gives the roles is refused", open: SignUp{Method: "entra"}, refused: httpio.HasConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{roles: map[access.RoleKey]bool{}}
			m := newSignUpManager(t, store)

			var got *resource.Warning
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				got, err = m.OpenSignUpWarning(ctx, txn, &tt.open)

				return err
			})

			if tt.refused != nil {
				if !tt.refused(err) {
					t.Fatalf("OpenSignUpWarning() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("OpenSignUpWarning() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("OpenSignUpWarning() (-want +got):\n%s", diff)
			}
			if tt.want != nil {
				if err := resource.CheckWarning("OpenSignUp", got); err != nil {
					t.Errorf("the warning does not hold to what a dialog needs: %v", err)
				}
			}
			// A warning is read, never written.
			if len(store.writes) != 0 {
				t.Errorf("the warning buffered %v, want nothing written", store.writes)
			}
		})
	}

	t.Run("an application's own word for a tenant is the warning's", func(t *testing.T) {
		t.Parallel()

		m := newSignUpManager(t, &fakeStore{}, WithTenantWord("customer"))
		var got *resource.Warning
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			var err error
			got, err = m.OpenSignUpWarning(ctx, txn, &SignUp{Method: "google", CreatesTenant: true})

			return err
		})
		want := "Anyone with a Google account will be able to sign up and create a customer of their own, without an invitation."
		if err != nil || got.Consequence != want {
			t.Errorf("OpenSignUpWarning() = %+v, %v; want the consequence %q", got, err, want)
		}
	})
}

func TestManager_MakeInviteOnly(t *testing.T) {
	t.Parallel()

	member := "Steward"
	tests := []struct {
		name       string
		method     string
		rows       map[string]SignInMethodRow
		wantWrites []string
		refused    func(err error) bool
	}{
		{
			name:       "an open method is made invite only, and its role goes with the choice",
			method:     "google",
			rows:       map[string]SignInMethodRow{"google": {Name: "google", OpenSignUp: true, DefaultRole: &member}},
			wantWrites: []string{"set sign-in method google open=false role=<none>"},
		},
		{name: "a method with no row is invite only already: nothing is written", method: "google"},
		{
			name:   "a method made invite only before is left as it is",
			method: "google",
			rows:   map[string]SignInMethodRow{"google": {Name: "google"}},
		},
		{
			name:   "another method's row is not this method's",
			method: "google",
			rows:   map[string]SignInMethodRow{"password": {Name: "password", OpenSignUp: true, DefaultRole: &member}},
		},
		{name: "a method the auth does not register is refused", method: "github", refused: httpio.HasNotFound},
		{name: "a method whose directory gives the roles is refused", method: "entra", refused: httpio.HasConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{methodRows: tt.rows}
			m := newSignUpManager(t, store)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.MakeInviteOnly(ctx, txn, tt.method)
			})

			if tt.refused != nil {
				if !tt.refused(err) {
					t.Fatalf("MakeInviteOnly() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("MakeInviteOnly() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

func TestManager_SignInMethods(t *testing.T) {
	t.Parallel()

	member := "Steward"
	store := &fakeStore{methodRows: map[string]SignInMethodRow{
		"google":   {Name: "google", OpenSignUp: true, DefaultRole: &member},
		"password": {Name: "password"},
		// A row the directory's method was given by hand opens nothing: the directory decides.
		"entra": {Name: "entra", OpenSignUp: true, DefaultRole: &member},
		// A row for a method the auth no longer registers lists nothing.
		"retired": {Name: "retired", OpenSignUp: true},
	}}
	m := newSignUpManager(t, store)

	got, err := m.SignInMethods(t.Context(), nil)
	if err != nil {
		t.Fatalf("SignInMethods() error = %v", err)
	}

	want := []SignInMethod{
		{Name: "password", Label: "Password", Admits: "Anyone with an email address", Says: "Invite only: a person signs in once they are invited."},
		{Name: "workspace", Label: "Acme", Admits: "Anyone with an account at acme.example", Says: "Invite only: a person signs in once they are invited."},
		{
			Name: "google", Label: "Google", Admits: "Anyone with a Google account", OpenSignUp: true, DefaultRole: "Steward",
			Says: "Open: Anyone with a Google account will be able to sign in without an invitation. Each of them gets the role Steward.",
		},
		{
			Name: "entra", Label: "Staff directory", Admits: "Anyone with an account at Lakeside", DirectoryGivesRoles: true,
			Says: "The directory gives the roles, so it decides who may sign in: a person it gives a role signs in, and nobody else.",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("SignInMethods() (-want +got):\n%s", diff)
	}

	open, err := m.AnyMethodOpen(t.Context(), nil)
	if err != nil || !open {
		t.Errorf("AnyMethodOpen() = %v, %v; want true while google is open", open, err)
	}
	closed := newSignUpManager(t, &fakeStore{methodRows: map[string]SignInMethodRow{
		"password": {Name: "password"},
		"entra":    {Name: "entra", OpenSignUp: true},
		"retired":  {Name: "retired", OpenSignUp: true},
	}})
	if open, err := closed.AnyMethodOpen(t.Context(), nil); err != nil || open {
		t.Errorf("AnyMethodOpen() = %v, %v; want false: a row of the directory's method, and one of a method the auth no longer registers, open nothing", open, err)
	}
}

func TestManager_SignUpRoles(t *testing.T) {
	t.Parallel()

	roles := map[accesstypes.PolicyScope][]accesstypes.Role{
		globalScope: {accesstypes.SuperAdministrator, "Steward", "Member"},
		alphaScope:  {"Reader"},
	}
	m, _ := newManager(t, &fakeStore{}, roles)

	got, err := m.SignUpRoles(t.Context())
	if err != nil {
		t.Fatalf("SignUpRoles() error = %v", err)
	}

	// The roles held for the whole application, each with its description, and never
	// the Super Administrator role or a tenant's role.
	want := []access.RoleInfo{{Name: "Steward", Description: "the Steward role"}, {Name: "Member", Description: "the Member role"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("SignUpRoles() (-want +got):\n%s", diff)
	}
}

func TestManager_SignedUp(t *testing.T) {
	t.Parallel()

	t.Run("the default role is given for the whole application, in the transaction it is handed, and the engine told once it has committed", func(t *testing.T) {
		t.Parallel()

		store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
		m, engine := newManager(t, store, engineRoles)
		hook := m.SignedUp("Staff")

		// The first run's transaction aborts: its committed function is never called.
		if _, err := hook(t.Context(), nil, userID, "dana", "google", "Steward"); err != nil {
			t.Fatalf("the hook's first run error = %v", err)
		}
		committed, err := hook(t.Context(), nil, userID, "dana", "google", "Steward")
		if err != nil {
			t.Fatalf("the hook's second run error = %v", err)
		}
		if engine.changed != 0 {
			t.Fatalf("the engine was told %d time(s) before any commit", engine.changed)
		}
		committed(t.Context())
		if engine.changed != 1 {
			t.Errorf("the engine was told the policy changed %d time(s) after the commit, want 1", engine.changed)
		}
		given := "insert membership global/ " + userID.String() + " Steward"
		if diff := cmp.Diff([]string{given, given}, store.writes); diff != "" {
			t.Errorf("buffered writes of the two runs (-want +got):\n%s", diff)
		}
	})

	t.Run("a role that no longer exists fails the hook, so no account is made holding nothing", func(t *testing.T) {
		t.Parallel()

		store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
		m, engine := newManager(t, store, map[accesstypes.PolicyScope][]accesstypes.Role{})

		committed, err := m.SignedUp("Staff")(t.Context(), nil, userID, "dana", "google", "Steward")

		if !httpio.HasNotFound(err) || committed != nil {
			t.Fatalf("the hook = committed %v, error %v; want NotFound and nothing to run", committed != nil, err)
		}
		if len(store.writes) != 0 || engine.changed != 0 {
			t.Errorf("the hook buffered %v and told the engine %d time(s), want nothing", store.writes, engine.changed)
		}
	})

	t.Run("the Super Administrator role is given to nobody who signs up, whatever the row says", func(t *testing.T) {
		t.Parallel()

		store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
		roles := map[accesstypes.PolicyScope][]accesstypes.Role{globalScope: {accesstypes.SuperAdministrator}}
		m, _ := newManager(t, store, roles)

		committed, err := m.SignedUp("Staff")(t.Context(), nil, userID, "dana", "google", string(accesstypes.SuperAdministrator))

		if err == nil || committed != nil {
			t.Fatalf("the hook = committed %v, error %v; want a failure", committed != nil, err)
		}
		if len(store.writes) != 0 {
			t.Errorf("the hook buffered %v, want nothing", store.writes)
		}
	})
}

// containsText reports whether the error's words say want.
func containsText(err error, want string) bool {
	return err != nil && strings.Contains(err.Error(), want)
}

func Test_withArticle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		word string
		want string
	}{
		{word: "tenant", want: "a tenant"},
		{word: "sector", want: "a sector"},
		{word: "outlet", want: "an outlet"},
		{word: "Organization", want: "an Organization"},
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			t.Parallel()

			if got := withArticle(tt.word); got != tt.want {
				t.Errorf("withArticle(%q) = %q, want %q", tt.word, got, tt.want)
			}
		})
	}
}
