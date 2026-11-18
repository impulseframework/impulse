package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

// invitingFoothold is the foothold impulse add auth writes for an auth that brings in a
// first administrator: the invitation and the fresh one, both asked of the session
// library. InviteAdministrator is declared on line 9, its call into the library is on
// line 10; ReinviteAdministrator is declared on line 13.
const invitingFoothold = `package staff

import (
	"context"

	"github.com/impulseframework/impulse/session"
)

func (a *Auth) InviteAdministrator(ctx context.Context, email string) (*session.FirstAdministrator, error) {
	return a.handlers.API().InviteFirstAdministrator(ctx, &session.FirstAdministratorRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})
}

func (a *Auth) ReinviteAdministrator(ctx context.Context, email string) (*session.FirstAdministrator, error) {
	return a.handlers.API().ReinviteFirstAdministrator(ctx, &session.FirstAdministratorRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})
}
`

// seedingFoothold is the foothold an earlier impulse add auth wrote: the first
// administrator seeded as an account, here with the password the deployment generated.
// SeedAdministrator is declared on line 11.
const seedingFoothold = `package staff

import (
	"context"

	"github.com/impulseframework/impulse/session"
)

const Administrator = "administrator"

func (a *Auth) SeedAdministrator(ctx context.Context, password string) (bool, error) {
	return true, a.seedFoothold(ctx, &session.CreateUserRequest{Username: Administrator, Password: &password, PasswordChangeRequired: true})
}

func (a *Auth) seedFoothold(ctx context.Context, req *session.CreateUserRequest) error {
	id := session.SeedUserID(Name, req.Username)
	req.ID = &id
	_, err := a.handlers.API().CreateSessionUser(ctx, req)

	return err
}
`

// The parts of invitingFoothold the cases replace: each function's one statement.
const (
	inviteCall   = `return a.handlers.API().InviteFirstAdministrator(ctx, &session.FirstAdministratorRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})`
	reinviteCall = `return a.handlers.API().ReinviteFirstAdministrator(ctx, &session.FirstAdministratorRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})`
)

// The findings the check writes, by what the package does in place of inviting; each
// opens with where it is.
const (
	seedsAnAccount  = ": the staff auth seeds its first administrator as an account (SeedAdministrator), under a password the deployment generates or a sign-in name the placement names. Nobody can read a generated password, and an account made ahead of its person is tied to whoever signs in first by its name alone, so the first administrator is invited instead: replace SeedAdministrator and seedFoothold with InviteAdministrator and ReinviteAdministrator as impulse add auth writes foothold.go (session.AuthAPI.InviteFirstAdministrator and ReinviteFirstAdministrator, mailed to the address the placement names in APP_STAFF_ADMINISTRATOR), read that address in the data level in place of the password (the APP_STAFF_ADMINISTRATOR_PASSWORD declaration and its secret:\"generated\" tag go), and call the two from pkg/deploy and the migrate command"
	createsAUser    = ": the staff auth creates a user where it invites its first administrator (session.AuthAPI.CreateSessionUser or a session.CreateUserRequest): the person's account is made by their own first sign-in from the mailed link, and none is created ahead of them, with a password or without; remove the user it creates"
	invitesItself   = ": the staff auth's InviteAdministrator does not ask the session library for the invitation (session.AuthAPI.InviteFirstAdministrator), which is what stops once the auth has an account, withdraws an invitation the placement no longer names, and refuses to write the link to a log; invite through it, as impulse add auth writes foothold.go"
	reinvitesItself = ": the staff auth's ReinviteAdministrator does not ask the session library for the fresh invitation (session.AuthAPI.ReinviteFirstAdministrator), which is what refuses once the auth has an account, withdraws the standing invitation so its link no longer opens, and refuses to write the new link to a log; invite again through it, as impulse add auth writes foothold.go"
)

// TestFirstAdministrator pins the rule on the first administrator an auth brings in: it
// is invited, through the session library, and no account is made ahead of the person.
// The refusals are the cases the rule exists for. A package that still seeds an account
// deploys one nobody can sign in to (nobody reads a generated password) or one whoever
// signs in first takes; an invitation written by hand beside the library's skips what
// the library holds, the stop once the auth has an account and the refusal to write the
// link to a log. What the package does outside the functions a first administrator is
// brought in by, for some other account, is not taken for theirs, and neither is what
// its tests and generated files do.
func TestFirstAdministrator(t *testing.T) {
	t.Parallel()

	const foothold = "pkg/auth/staff/foothold.go"
	tests := []struct {
		name        string
		edit        func(files map[string]string)
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name: "an auth that invites its first administrator through the session library",
			edit: func(files map[string]string) {
				files[foothold] = invitingFoothold
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "an auth that still seeds its first administrator as an account",
			edit: func(files map[string]string) {
				files[foothold] = seedingFoothold
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s): a first administrator is brought in another way than by an invitation",
			wantDetails: []string{foothold + ":11" + seedsAnAccount},
		},
		{
			name: "a seed whose public function was renamed still creates the account in seedFoothold",
			edit: func(files map[string]string) {
				files[foothold] = strings.Replace(seedingFoothold, "func (a *Auth) SeedAdministrator(", "func (a *Auth) Bootstrap(", 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s): a first administrator is brought in another way than by an invitation",
			wantDetails: []string{foothold + ":15" + seedsAnAccount},
		},
		{
			name: "an auth that invites and still seeds: the seed goes",
			edit: func(files map[string]string) {
				files[foothold] = invitingFoothold
				files["pkg/auth/staff/seed.go"] = seedingFoothold
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s): a first administrator is brought in another way than by an invitation",
			wantDetails: []string{"pkg/auth/staff/seed.go:11" + seedsAnAccount},
		},
		{
			name: "an invitation that also creates the account ahead of the person",
			edit: func(files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, inviteCall,
					"if _, err := a.handlers.API().CreateSessionUser(ctx, nil); err != nil {\n\t\treturn nil, err\n\t}\n\n\t"+inviteCall, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s): a first administrator is brought in another way than by an invitation",
			wantDetails: []string{foothold + ":10" + createsAUser},
		},
		{
			name: "an invitation that builds the request for an account",
			edit: func(files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, inviteCall,
					"req := &session.CreateUserRequest{Username: email}\n\t_ = req\n\n\t"+inviteCall, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s): a first administrator is brought in another way than by an invitation",
			wantDetails: []string{foothold + ":10" + createsAUser},
		},
		{
			name: "a fresh invitation that creates the account ahead of the person",
			edit: func(files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, reinviteCall,
					"if _, err := a.handlers.API().CreateSessionUser(ctx, nil); err != nil {\n\t\treturn nil, err\n\t}\n\n\t"+reinviteCall, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s): a first administrator is brought in another way than by an invitation",
			wantDetails: []string{foothold + ":14" + createsAUser},
		},
		{
			name: "an invitation written by hand, beside the session library's",
			edit: func(files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, inviteCall,
					`return nil, a.handlers.API().Invite(ctx, &session.InviteRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})`, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s): a first administrator is brought in another way than by an invitation",
			wantDetails: []string{foothold + ":9" + invitesItself},
		},
		{
			name: "an invitation that asks for the fresh one, which a release would mail again each time",
			edit: func(files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, inviteCall, reinviteCall, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s): a first administrator is brought in another way than by an invitation",
			wantDetails: []string{foothold + ":9" + invitesItself},
		},
		{
			name: "a fresh invitation written by hand, beside the session library's",
			edit: func(files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, reinviteCall,
					`return nil, a.handlers.API().Invite(ctx, &session.InviteRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})`, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s): a first administrator is brought in another way than by an invitation",
			wantDetails: []string{foothold + ":13" + reinvitesItself},
		},
		{
			name: "a fresh invitation alone, with no invitation a release sends",
			edit: func(files map[string]string) {
				files[foothold] = "package staff\n\nfunc (a *Auth) ReinviteAdministrator(ctx context.Context, email string) error {\n\treturn a.mailAgain(ctx, email)\n}\n"
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s): a first administrator is brought in another way than by an invitation",
			wantDetails: []string{foothold + ":3" + reinvitesItself},
		},
		{
			name: "every finding of a package is reported",
			edit: func(files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, inviteCall,
					"_, err := a.handlers.API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: email})\n\n\treturn nil, err", 1)
				files["pkg/auth/staff/seed.go"] = seedingFoothold
			},
			wantStatus:  Fail,
			wantSummary: "3 finding(s): a first administrator is brought in another way than by an invitation",
			wantDetails: []string{
				"pkg/auth/staff/seed.go:11" + seedsAnAccount,
				foothold + ":10" + createsAUser,
				foothold + ":9" + invitesItself,
			},
		},
		{
			name: "a user created outside the invitation's functions, for some other account, is not the first administrator's",
			edit: func(files map[string]string) {
				files[foothold] = invitingFoothold
				files["pkg/auth/staff/operator.go"] = "package staff\n\nfunc (a *Auth) operator(password string) *session.CreateUserRequest {\n\treturn &session.CreateUserRequest{Username: \"operator\", Password: &password}\n}\n"
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "what the package's tests and generated files do is not read",
			edit: func(files map[string]string) {
				files[foothold] = invitingFoothold
				files["pkg/auth/staff/foothold_test.go"] = strings.Replace(seedingFoothold, "package staff", "package staff_test", 1)
				files["pkg/auth/staff/zz_gen_seed.go"] = seedingFoothold
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "an auth that brings in no first administrator is not held",
			edit: func(files map[string]string) {
				files[foothold] = "package staff\n\n// AdministratorRoles are the roles that count as an administrator's.\nfunc AdministratorRoles() (global, everyDomain []string) {\n\treturn []string{\"Administrator\"}, nil\n}\n"
			},
			wantStatus:  Skip,
			wantSummary: "no auth brings in a first administrator",
		},
		{
			name:        "an auth with no foothold is not held",
			wantStatus:  Skip,
			wantSummary: "no auth brings in a first administrator",
		},
		{
			name: "an application with no auth package",
			edit: func(files map[string]string) {
				for rel := range files {
					if strings.HasPrefix(rel, "pkg/auth/") {
						delete(files, rel)
					}
				}
			},
			wantStatus:  Skip,
			wantSummary: "no auth package (pkg/auth/<name>) constructs a session Auth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := admitted()
			if tt.edit != nil {
				tt.edit(files)
			}
			root := t.TempDir()
			write := func(rel, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", "module example.com/harbor\n\ngo 1.26.6\n")
			for rel, content := range files {
				write(rel, content)
			}

			a, err := app.Discover(root)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}
			got := firstAdministrator{}.Run(context.Background(), &Env{App: a})
			want := Result{Name: firstAdministrator{}.Name(), Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
