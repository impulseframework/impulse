package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// askedToGive is how the role file's grants are asked whether the caller may give a
// role for the whole application to a person who has no account yet: the question names
// nobody in particular.
func askedToGive(role accesstypes.Role) []string {
	key := access.NewMembershipKey(globalScope, User(impulse.NilUUID), role)

	return []string{"give " + membershipText(&key)}
}

// registeredMethods are the sign-in methods of the auth the sign-up tests manage, one
// of each width: a method limited to one organization, one that admits any account of
// its provider, the password method, one whose directory gives the roles, and one whose
// registration carries an account rule of its own.
func registeredMethods() []session.RegisteredMethod {
	return []session.RegisteredMethod{
		{Name: "password", Method: sessioninfo.MethodPassword, Label: "Password", Audience: signin.Audience{Accounts: "an email address", Identities: []string{"any-email-address"}}},
		{Name: "workspace", Method: sessioninfo.MethodGoogle, Label: "Acme", Audience: signin.Audience{Organizations: []string{"acme.example"}, Identities: []string{"domain:acme.example"}}},
		{Name: "google", Method: sessioninfo.MethodGoogle, Label: "Google", Audience: signin.Audience{Accounts: "a Google account", Identities: []string{"any-google-account"}}},
		{Name: "entra", Method: sessioninfo.MethodAzure, Label: "Staff directory", Audience: signin.Audience{Organizations: []string{"Lakeside"}, Identities: []string{"tenant:1111"}}, SyncsRoles: true},
		// A method that gives no identities of whom it admits: its audience is kept by
		// the names it gives.
		{Name: "partners", Method: sessioninfo.MethodOIDC, Label: "Partners", Audience: signin.Audience{Organizations: []string{"Harbor"}}, OwnRule: true},
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
		// caller makes the request, "" for a call outside any request; grants are the
		// role file's answers on giving a role, nil for a manager built without them.
		caller accesstypes.User
		grants *fakeGrants
		// wantWrites are the store's buffered writes; none for a refusal. wantFor is the
		// audience the row written was opened for, as the row keeps it: what identifies
		// whom the method's registration admits, and never a name of theirs.
		wantWrites []string
		wantFor    string
		// refused says how a refusal answers, and wantHas what its words say.
		refused func(err error) bool
		wantHas string
		// wantAsked is what the role file's grants were asked.
		wantAsked []string
	}{
		{
			name:       "without tenants, a method is opened with its default role, for the audience its registration has",
			open:       SignUp{Method: "google", DefaultRole: "Steward"},
			wantWrites: []string{"set sign-in method google open=true role=Steward"},
			wantFor:    `{"admits":["any-google-account"]}`,
		},
		{
			name:   "a caller who may give the role opens a method with it: the grant of the method that gives a role is asked, for the whole application and nobody in particular",
			open:   SignUp{Method: "workspace", DefaultRole: "Steward"},
			caller: erin, grants: &fakeGrants{},
			wantWrites: []string{"set sign-in method workspace open=true role=Steward"},
			wantFor:    `{"admits":["domain:acme.example"]}`,
			wantAsked:  askedToGive("Steward"),
		},
		{
			name:   "a caller who may not give the role does not make it a default role: opening the method would give it to every person who signs up",
			open:   SignUp{Method: "google", DefaultRole: "Steward"},
			caller: erin, grants: &fakeGrants{noGive: []accesstypes.Role{"Steward"}},
			refused: httpio.HasForbidden, wantHas: "the role Steward, which you may not give",
			wantAsked: askedToGive("Steward"),
		},
		{
			name:    "a request reaches a manager built without the role file's grants: an error, never a pass",
			open:    SignUp{Method: "google", DefaultRole: "Steward"},
			caller:  erin,
			refused: func(err error) bool { return err != nil && !httpio.HasForbidden(err) }, wantHas: "identity.WithRoleGrants",
		},
		{
			name:   "with tenants no role is given, so the role file's grants are not asked",
			open:   SignUp{Method: "google", CreatesTenant: true},
			caller: erin, grants: &fakeGrants{noGive: []accesstypes.Role{"Steward"}},
			wantWrites: []string{"set sign-in method google open=true role=<none>"},
			wantFor:    `{"admits":["any-google-account"]}`,
		},
		{
			name:    "a method whose registration carries an account rule of its own is refused by name",
			open:    SignUp{Method: "partners", DefaultRole: "Steward"},
			refused: httpio.HasConflict, wantHas: `partners\" is decided by the application's own account rule`,
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
			name:   "a caller who may give the role is told that it is not held for the whole application",
			open:   SignUp{Method: "google", DefaultRole: "Reader"},
			caller: erin, grants: &fakeGrants{},
			refused: httpio.HasBadRequest, wantHas: "is not a role held for the whole application: choose one of the roles the picker lists",
			wantAsked: askedToGive("Reader"),
		},
		{
			name:   "a caller who may not give a role learns nothing of whether it exists: the grant answers first, in the words it answers a role that exists",
			open:   SignUp{Method: "google", DefaultRole: "Ghost"},
			caller: erin, grants: &fakeGrants{noGive: []accesstypes.Role{"Ghost"}},
			refused: httpio.HasForbidden, wantHas: "opening the method gives every person who signs up the role Ghost, which you may not give: choose a role you may give, or have someone who may give Ghost open the method",
			wantAsked: askedToGive("Ghost"),
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
			wantFor:    `{"admits":["any-google-account"]}`,
		},
		{
			name:    "with tenants, a default role is refused",
			open:    SignUp{Method: "google", DefaultRole: "Steward", CreatesTenant: true},
			refused: httpio.HasBadRequest, wantHas: "no default role is chosen here",
		},
		{
			name:    "a method the auth does not register is refused, and its methods named",
			open:    SignUp{Method: "github", DefaultRole: "Steward"},
			refused: httpio.HasNotFound, wantHas: "its methods are password, workspace, google, entra, partners",
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
			wantFor:    `{"admits":["any-email-address"]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{roles: map[access.RoleKey]bool{}}
			var opts []Option
			if tt.grants != nil {
				opts = append(opts, WithRoleGrants(tt.grants))
			}
			m := newSignUpManager(t, store, opts...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.OpenSignUp(asCaller(ctx, tt.caller), txn, &tt.open)
			})
			if tt.grants != nil {
				if diff := cmp.Diff(tt.wantAsked, tt.grants.asked); diff != "" {
					t.Errorf("the role file's grants were asked (-want +got):\n%s", diff)
				}
			}
			var openedFor string
			if store.openedFor != nil {
				if _, ok := signin.IdentitiesOfKey(*store.openedFor); !ok {
					t.Fatalf("the row was opened for %q, which is no audience's key", *store.openedFor)
				}
				openedFor = *store.openedFor
			}
			if openedFor != tt.wantFor {
				t.Errorf("the row was opened for %q, want %q", openedFor, tt.wantFor)
			}

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

// TestManager_OpenSignUpWarning_roleTheCallerMayNotGive pins that the warning refuses
// what the call would refuse of the role: a caller reads no warning that promises a role
// they may not give, and the role file's grants are not asked while no role is picked.
func TestManager_OpenSignUpWarning_roleTheCallerMayNotGive(t *testing.T) {
	t.Parallel()
	grants := &fakeGrants{noGive: []accesstypes.Role{"Steward"}}
	m := newSignUpManager(t, &fakeStore{roles: map[access.RoleKey]bool{}}, WithRoleGrants(grants))

	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if _, err := m.OpenSignUpWarning(asCaller(ctx, erin), txn, &SignUp{Method: "google"}); err != nil {
			t.Errorf("OpenSignUpWarning() with no role picked yet error = %v, want the warning", err)
		}
		if len(grants.asked) != 0 {
			t.Errorf("the role file's grants were asked %v while no role is picked, want nothing", grants.asked)
		}
		_, err := m.OpenSignUpWarning(asCaller(ctx, erin), txn, &SignUp{Method: "google", DefaultRole: "Steward"})

		return err
	})

	if !httpio.HasForbidden(err) || !containsText(err, "the role Steward, which you may not give") {
		t.Errorf("OpenSignUpWarning() error = %v, want it forbidden, naming the role", err)
	}
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
		{name: "a method whose registration carries an account rule of its own is refused", method: "partners", refused: httpio.HasConflict},
		{
			name:       "a method whose row was opened for another audience opens nothing, and is made invite only all the same: the row is written closed",
			method:     "workspace",
			rows:       map[string]SignInMethodRow{"workspace": {Name: "workspace", OpenSignUp: true, DefaultRole: &member}},
			wantWrites: []string{"set sign-in method workspace open=false role=<none>"},
		},
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
	audienceOf := func(name string) *string {
		for _, method := range registeredMethods() {
			if method.Name == name {
				key := method.Audience.Key()

				return &key
			}
		}
		t.Fatalf("no method is registered under %q", name)

		return nil
	}
	// Another organization under the name the registration gives its own now: the two
	// read alike and are not the same people.
	other := signin.Audience{Organizations: []string{"acme.example"}, Identities: []string{"domain:lakeside.edu"}}.Key()
	// The audiences the workspace and the password registrations have now, as a row kept
	// them in the earlier form, by the names of their organizations and the words for
	// their accounts.
	earlierWorkspace := `{"organizations":["acme.example"],"accounts":""}`
	earlierPassword := `{"organizations":[],"accounts":"an email address"}`
	store := &fakeStore{methodRows: map[string]SignInMethodRow{
		"google":   {Name: "google", OpenSignUp: true, OpenedFor: audienceOf("google"), DefaultRole: &member},
		"password": {Name: "password"},
		// A row opened when the registration admitted one organization opens nothing now
		// that it admits another, whatever the two are called: the method is invite only
		// until it is opened again.
		"workspace": {Name: "workspace", OpenSignUp: true, OpenedFor: &other, DefaultRole: &member},
		// A row the directory's method was given by hand opens nothing: the directory decides.
		"entra": {Name: "entra", OpenSignUp: true, OpenedFor: audienceOf("entra"), DefaultRole: &member},
		// Neither does a row of a registration whose own rule decides.
		"partners": {Name: "partners", OpenSignUp: true, OpenedFor: audienceOf("partners"), DefaultRole: &member},
		// A row for a method the auth no longer registers lists nothing.
		"retired": {Name: "retired", OpenSignUp: true, OpenedFor: audienceOf("google")},
	}}
	m := newSignUpManager(t, store)

	got, err := m.SignInMethods(t.Context(), nil)
	if err != nil {
		t.Fatalf("SignInMethods() error = %v", err)
	}

	want := []SignInMethod{
		{Name: "password", Label: "Password", Admits: "Anyone with an email address", Says: "Invite only: a person signs in once they are invited."},
		{
			Name: "workspace", Label: "Acme", Admits: "Anyone with an account at acme.example",
			Says: "Invite only: the people this method admits have changed since it was opened, and it admits anyone with an account at acme.example now. Open it again to admit them.",
		},
		{
			Name: "google", Label: "Google", Admits: "Anyone with a Google account", OpenSignUp: true, DefaultRole: "Steward",
			Says: "Open: Anyone with a Google account will be able to sign in without an invitation. Each of them gets the role Steward.",
		},
		{
			Name: "entra", Label: "Staff directory", Admits: "Anyone with an account at Lakeside", DirectoryGivesRoles: true,
			Says: "The directory gives the roles, so it decides who may sign in: a person it gives a role signs in, and nobody else.",
		},
		{
			Name: "partners", Label: "Partners", Admits: "Anyone with an account at Harbor", OwnRule: true,
			Says: "The application's own rule decides who may sign in through this method: it is neither opened to sign-up nor made invite only here.",
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
		"password":  {Name: "password", OpenSignUp: true, OpenedFor: &earlierPassword, DefaultRole: &member},
		"workspace": {Name: "workspace", OpenSignUp: true, OpenedFor: &earlierWorkspace, DefaultRole: &member},
		"google":    {Name: "google", OpenSignUp: true},
		"entra":     {Name: "entra", OpenSignUp: true, OpenedFor: audienceOf("entra")},
		"partners":  {Name: "partners", OpenSignUp: true, OpenedFor: audienceOf("partners")},
		"retired":   {Name: "retired", OpenSignUp: true, OpenedFor: audienceOf("google")},
	}})
	if open, err := closed.AnyMethodOpen(t.Context(), nil); err != nil || open {
		t.Errorf("AnyMethodOpen() = %v, %v; want false: a row that kept its audience in the earlier form, by names, a row that names none, a row of the directory's method, one of a registration whose own rule decides, and one of a method the auth no longer registers open nothing", open, err)
	}
	// A row that names no audience says so in the list too, and so does a row that kept
	// its audience in the earlier form, by names: neither can be compared with whom the
	// method admits, so neither is open, even where the names are the registration's own.
	listed, err := closed.SignInMethods(t.Context(), nil)
	if err != nil {
		t.Fatalf("SignInMethods() error = %v", err)
	}
	for at, admits := range map[int]string{0: "anyone with an email address", 1: "anyone with an account at acme.example", 2: "anyone with a Google account"} {
		want := "Invite only: this method was opened before its audience was kept, and it admits " + admits + " now. Open it again to admit them."
		if listed[at].OpenSignUp || listed[at].DefaultRole != "" || listed[at].Says != want {
			t.Errorf("the row of %s lists as open = %v with the role %q saying %q, want invite only with no role saying %q", listed[at].Name, listed[at].OpenSignUp, listed[at].DefaultRole, listed[at].Says, want)
		}
	}
}

func TestManager_SignUpRoles(t *testing.T) {
	t.Parallel()

	roles := map[accesstypes.PolicyScope][]accesstypes.Role{
		globalScope: {accesstypes.SuperAdministrator, "Steward", "Member"},
		alphaScope:  {"Reader"},
	}
	steward := access.RoleInfo{Name: "Steward", Description: "the Steward role"}
	member := access.RoleInfo{Name: "Member", Description: "the Member role"}

	tests := []struct {
		name string
		// caller makes the request, "" for a call outside any request; grants are the
		// role file's answers on giving a role, nil for a manager built without them.
		caller  accesstypes.User
		grants  *fakeGrants
		want    []access.RoleInfo
		wantErr string
	}{
		{
			// The roles held for the whole application, each with its description, and
			// never the Super Administrator role or a tenant's role.
			name: "outside a request: every role held for the whole application but the Super Administrator's",
			want: []access.RoleInfo{steward, member},
		},
		{
			name:   "a caller who may give every role is offered every one",
			caller: erin, grants: &fakeGrants{},
			want: []access.RoleInfo{steward, member},
		},
		{
			name:   "a caller is offered the roles they may give and no other: the dialog offers no role its save would refuse",
			caller: erin, grants: &fakeGrants{noGive: []accesstypes.Role{"Steward"}},
			want: []access.RoleInfo{member},
		},
		{
			name:   "a caller who may give no role is offered none",
			caller: erin, grants: &fakeGrants{noGive: []accesstypes.Role{"Steward", "Member"}},
		},
		{
			name:   "a request reaches a manager built without the role file's grants: an error, never the whole list",
			caller: erin, wantErr: "identity.WithRoleGrants",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var opts []Option
			if tt.grants != nil {
				opts = append(opts, WithRoleGrants(tt.grants))
			}
			m, _ := newManager(t, &fakeStore{}, roles, opts...)

			got, err := m.SignUpRoles(asCaller(t.Context(), tt.caller))

			if tt.wantErr != "" {
				if err == nil || !containsText(err, tt.wantErr) {
					t.Fatalf("SignUpRoles() = %v, %v; want an error saying %q", got, err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("SignUpRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("SignUpRoles() (-want +got):\n%s", diff)
			}
		})
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

// TestManager_SetSelfServiceReset pins an administrator's switch for a person's own
// password reset: the one field of the password method's row is written, on and off;
// the list of sign-in methods says it of the password method alone; and an auth nobody
// signs in to with a password has nothing to switch.
func TestManager_SetSelfServiceReset(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	m := newSignUpManager(t, store)
	for _, on := range []bool{true, false} {
		if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.SetSelfServiceReset(ctx, txn, on)
		}); err != nil {
			t.Fatalf("SetSelfServiceReset(%v) error = %v", on, err)
		}
	}
	want := []string{"set self-service reset of password on=true", "set self-service reset of password on=false"}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}

	// The row says it, and the list says it of the password method and of no other,
	// whatever a row of another method holds.
	store = &fakeStore{methodRows: map[string]SignInMethodRow{
		"password": {Name: "password", SelfServiceReset: true},
		"google":   {Name: "google", SelfServiceReset: true},
	}}
	m = newSignUpManager(t, store)
	var methods []SignInMethod
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		methods, err = m.SignInMethods(ctx, txn)

		return err
	}); err != nil {
		t.Fatalf("SignInMethods() error = %v", err)
	}
	for i := range methods {
		if got, want := methods[i].SelfServiceReset, methods[i].Name == "password"; got != want {
			t.Errorf("SignInMethods(): %s says SelfServiceReset %v, want %v", methods[i].Name, got, want)
		}
	}

	directory, err := New(managedAuth, &fakeStore{}, &fakeEngine{roles: engineRoles}, fakeSessions{methods: registeredMethods()[1:]})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return directory.SetSelfServiceReset(ctx, txn, true)
	})
	if !httpio.HasConflict(err) {
		t.Errorf("SetSelfServiceReset() on an auth with no password method error = %v, want Conflict", err)
	}
}
