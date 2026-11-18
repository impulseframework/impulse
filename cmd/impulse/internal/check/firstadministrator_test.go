package check

import (
	"context"
	"fmt"
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
	return a.handlers.API().InviteFirstAdministrator(ctx, a.access.UserManager(), &session.FirstAdministratorRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})
}

func (a *Auth) ReinviteAdministrator(ctx context.Context, email string) (*session.FirstAdministrator, error) {
	return a.handlers.API().ReinviteFirstAdministrator(ctx, a.access.UserManager(), &session.FirstAdministratorRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})
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

// What the cases take out of the auth package's source (admittingAuth): the import of
// its generated identity client and the client it builds from it, and the acceptance
// hook it sets.
const (
	acceptanceHook = "\ts.SetInvitationAccepted(identityClient.InvitationAccepted())\n"
	identityImport = "\t\"example.com/harbor/pkg/identity/staffidentity\"\n"
	identityBuilt  = "\tidentityClient, err := staffidentity.NewClient(engine, s.API())\n\tif err != nil {\n\t\treturn nil, err\n\t}\n"
)

// withoutIdentity is the auth package's source with no generated identity client: the
// auth an application that generates no management of its people has.
func withoutIdentity(t *testing.T, source string) string {
	t.Helper()

	for _, part := range []string{identityImport, identityBuilt} {
		if !strings.Contains(source, part) {
			t.Fatalf("the auth package's source lacks %q, which the case takes out", part)
		}
		source = strings.Replace(source, part, "", 1)
	}

	return source
}

// migrateCommand is a migrate command's function that runs a release's steps, whose
// statements a case gives: they open on line 6. invitesStep invites for every auth and
// ownInvitation for the staff auth alone, each three lines whose first holds the call;
// seedsStep seeds the staff auth's cast on its second line of five.
const (
	migrateFile    = "cmd/deployment/migrate/main.go"
	migrateCommand = "package main\n\nimport \"context\"\n\nfunc migrate(ctx context.Context, data *config.DataConfiguration, seed bool) error {\n%s\n\treturn nil\n}\n"
	invitesStep    = "\tif err := deploy.InviteAdministrators(ctx, data, os.Stdout); err != nil {\n\t\treturn err\n\t}\n"
	ownInvitation  = "\tif _, err := data.Staff().InviteAdministrator(ctx, data.StaffAdministrator()); err != nil {\n\t\treturn err\n\t}\n"
	seedsStep      = "\tif seed {\n\t\tif _, err := data.Staff().SeedCast(ctx); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n"
	seededLate     = ": the cast is seeded (SeedCast) after the first administrator is invited (line 6): a release invites while nobody of the auth holds the Super Administrator role, and a persona of the cast may hold it, so an environment whose cast holds the role is asked for an address and a mailbox it has no use for, or mails an invitation nobody wants. Seed the cast first: move the SeedCast call above the invitation, so the invitation is decided with the personas in place"
)

// The parts of invitingFoothold the cases replace: each function's one statement.
const (
	inviteCall   = `return a.handlers.API().InviteFirstAdministrator(ctx, a.access.UserManager(), &session.FirstAdministratorRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})`
	reinviteCall = `return a.handlers.API().ReinviteFirstAdministrator(ctx, a.access.UserManager(), &session.FirstAdministratorRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})`
)

// The findings the check writes, by what the package does in place of inviting; each
// opens with where it is.
const (
	seedsAnAccount  = ": the staff auth seeds its first administrator as an account (SeedAdministrator), under a password the deployment generates or a sign-in name the placement names. Nobody can read a generated password, and an account made ahead of its person is tied to whoever signs in first by its name alone, so the first administrator is invited instead: replace SeedAdministrator and seedFoothold with InviteAdministrator and ReinviteAdministrator as impulse add auth writes foothold.go (session.AuthAPI.InviteFirstAdministrator and ReinviteFirstAdministrator, mailed to the address the placement names in APP_STAFF_ADMINISTRATOR), read that address in the data level in place of the password (the APP_STAFF_ADMINISTRATOR_PASSWORD declaration and its secret:\"generated\" tag go), and call the two from pkg/deploy and the migrate command"
	createsAUser    = ": the staff auth creates a user where it invites its first administrator (session.AuthAPI.CreateSessionUser or a session.CreateUserRequest): the person's account is made by their own first sign-in from the mailed link, and none is created ahead of them, with a password or without; remove the user it creates"
	invitesItself   = ": the staff auth's InviteAdministrator does not ask the session library for the invitation (session.AuthAPI.InviteFirstAdministrator), which is what stops once somebody holds the Super Administrator role, withdraws an invitation the placement no longer names, and refuses to write the link to a log; invite through it, as impulse add auth writes foothold.go"
	noHook          = ": the staff auth invites its first administrator and no file of its package sets the acceptance hook (SetInvitationAccepted), so an accepted invitation would give its person no role and nobody would come to hold the Super Administrator role; the session library refuses to invite for such an Auth. Set the hook where the Auth is built, in New, with the generated identity client's: SetInvitationAccepted(identityClient.InvitationAccepted())"
	noIdentity      = "pkg/auth/staff: the staff auth gives its roles itself (it holds a role file, and no method is registered with RoleSync) and no file of its package imports a generated identity client (staffidentity), so nothing gives an accepted invitation its roles: nobody can be invited as the auth's first administrator, and an account seeded by hand is refused too, which leaves a deployed auth with no way in. Generate its identity client: add generation.GenerateIdentity with a tree of the program's own to the generator program of the outlet that declares the auth and regenerate, build the client from that tree in the auth's New (staffidentity.NewClient) and set its acceptance hook (SetInvitationAccepted(identityClient.InvitationAccepted())), then invite the first administrator from foothold.go (InviteAdministrator and ReinviteAdministrator, as impulse add auth writes them for an auth whose management the application generates)"
	reinvitesItself = ": the staff auth's ReinviteAdministrator does not ask the session library for the fresh invitation (session.AuthAPI.ReinviteFirstAdministrator), which is what refuses once somebody holds the Super Administrator role, withdraws the standing invitation so its link no longer opens, and refuses to write the new link to a log; invite again through it, as impulse add auth writes foothold.go"
)

// TestFirstAdministrator pins the rule on the first administrator an auth brings in: it
// is invited, through the session library, and no account is made ahead of the person.
// The refusals are the cases the rule exists for. A package that still seeds an account
// deploys one nobody can sign in to (nobody reads a generated password) or one whoever
// signs in first takes; an invitation written by hand beside the library's skips what
// the library holds, the stop once somebody holds the Super Administrator role and the
// refusal to write the link to a log. What the package does outside the functions a first administrator is
// brought in by, for some other account, is not taken for theirs, and neither is what
// its tests and generated files do.
//
// An auth whose roles the application gives is held to building its generated identity
// client, whatever its foothold holds: the client's acceptance hook gives an accepted
// invitation its roles, so without it nobody can be invited as the auth's first
// administrator and the auth has no way in. An auth whose directory writes the roles,
// and one with no role file, give nobody a role and are not held.
//
// An auth that invites is held to setting the acceptance hook too, in the package that
// builds the client.
//
// And a function that seeds a cast of personas after it invites that cast's auth is
// refused: who holds the Super Administrator role decides the invitation, and a persona
// may hold it.
func TestFirstAdministrator(t *testing.T) {
	t.Parallel()

	const (
		foothold = "pkg/auth/staff/foothold.go"
		authFile = "pkg/auth/staff/staff.go"
	)
	tests := []struct {
		name        string
		edit        func(t *testing.T, files map[string]string)
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name: "an auth that invites its first administrator through the session library",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = invitingFoothold
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "an auth that still seeds its first administrator as an account",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = seedingFoothold
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{foothold + ":11" + seedsAnAccount},
		},
		{
			name: "a seed whose public function was renamed still creates the account in seedFoothold",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = strings.Replace(seedingFoothold, "func (a *Auth) SeedAdministrator(", "func (a *Auth) Bootstrap(", 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{foothold + ":15" + seedsAnAccount},
		},
		{
			name: "an auth that invites and still seeds: the seed goes",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = invitingFoothold
				files["pkg/auth/staff/seed.go"] = seedingFoothold
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{"pkg/auth/staff/seed.go:11" + seedsAnAccount},
		},
		{
			name: "an invitation that also creates the account ahead of the person",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, inviteCall,
					"if _, err := a.handlers.API().CreateSessionUser(ctx, nil); err != nil {\n\t\treturn nil, err\n\t}\n\n\t"+inviteCall, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{foothold + ":10" + createsAUser},
		},
		{
			name: "an invitation that builds the request for an account",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, inviteCall,
					"req := &session.CreateUserRequest{Username: email}\n\t_ = req\n\n\t"+inviteCall, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{foothold + ":10" + createsAUser},
		},
		{
			name: "a fresh invitation that creates the account ahead of the person",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, reinviteCall,
					"if _, err := a.handlers.API().CreateSessionUser(ctx, nil); err != nil {\n\t\treturn nil, err\n\t}\n\n\t"+reinviteCall, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{foothold + ":14" + createsAUser},
		},
		{
			name: "an invitation written by hand, beside the session library's",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, inviteCall,
					`return nil, a.handlers.API().Invite(ctx, &session.InviteRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})`, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{foothold + ":9" + invitesItself},
		},
		{
			name: "an invitation that asks for the fresh one, which a release would mail again each time",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, inviteCall, reinviteCall, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{foothold + ":9" + invitesItself},
		},
		{
			name: "a fresh invitation written by hand, beside the session library's",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, reinviteCall,
					`return nil, a.handlers.API().Invite(ctx, &session.InviteRequest{Email: email, RoleNames: []string{"SuperAdministrator"}})`, 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{foothold + ":13" + reinvitesItself},
		},
		{
			name: "a fresh invitation alone, with no invitation a release sends",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = "package staff\n\nfunc (a *Auth) ReinviteAdministrator(ctx context.Context, email string) error {\n\treturn a.mailAgain(ctx, email)\n}\n"
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{foothold + ":3" + reinvitesItself},
		},
		{
			name: "every finding of a package is reported",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = strings.Replace(invitingFoothold, inviteCall,
					"_, err := a.handlers.API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: email})\n\n\treturn nil, err", 1)
				files["pkg/auth/staff/seed.go"] = seedingFoothold
			},
			wantStatus:  Fail,
			wantSummary: "3 finding(s) on how a first administrator is brought in",
			wantDetails: []string{
				"pkg/auth/staff/seed.go:11" + seedsAnAccount,
				foothold + ":10" + createsAUser,
				foothold + ":9" + invitesItself,
			},
		},
		{
			name: "a user created outside the invitation's functions, for some other account, is not the first administrator's",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = invitingFoothold
				files["pkg/auth/staff/operator.go"] = "package staff\n\nfunc (a *Auth) operator(password string) *session.CreateUserRequest {\n\treturn &session.CreateUserRequest{Username: \"operator\", Password: &password}\n}\n"
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "what the package's tests and generated files do is not read",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = invitingFoothold
				files["pkg/auth/staff/foothold_test.go"] = strings.Replace(seedingFoothold, "package staff", "package staff_test", 1)
				files["pkg/auth/staff/zz_gen_seed.go"] = seedingFoothold
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "an auth that brings in no first administrator is not held",
			edit: func(_ *testing.T, files map[string]string) {
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
			name: "an auth whose roles the application gives and that builds no generated identity client is refused: nobody can be invited as its first administrator",
			edit: func(t *testing.T, files map[string]string) {
				files[authFile] = withoutIdentity(t, files[authFile])
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{noIdentity},
		},
		{
			name: "such an auth is refused with the foothold impulse add auth writes for it, which names the administrator roles and invites nobody",
			edit: func(t *testing.T, files map[string]string) {
				files[authFile] = withoutIdentity(t, files[authFile])
				files[foothold] = "package staff\n\n// AdministratorRoles are the roles that count as an administrator's.\nfunc AdministratorRoles() (global, everyDomain []string) {\n\treturn []string{\"Administrator\"}, nil\n}\n"
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{noIdentity},
		},
		{
			name: "such an auth that seeds an account by hand is refused for both: the seed is no way in either",
			edit: func(t *testing.T, files map[string]string) {
				files[authFile] = withoutIdentity(t, files[authFile])
				files[foothold] = seedingFoothold
			},
			wantStatus:  Fail,
			wantSummary: "2 finding(s) on how a first administrator is brought in",
			wantDetails: []string{foothold + ":11" + seedsAnAccount, noIdentity},
		},
		{
			name: "such an auth that invites by the session library is refused still: nothing would give the accepted invitation its roles",
			edit: func(t *testing.T, files map[string]string) {
				files[authFile] = withoutIdentity(t, files[authFile])
				files[foothold] = invitingFoothold
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{noIdentity},
		},
		{
			name: "an identity client imported by the package's tests or its generated files alone is not one the package builds",
			edit: func(t *testing.T, files map[string]string) {
				files[authFile] = withoutIdentity(t, files[authFile])
				files["pkg/auth/staff/staff_test.go"] = "package staff\n\nimport _ \"example.com/harbor/pkg/identity/staffidentity\"\n"
				files["pkg/auth/staff/zz_gen_identity.go"] = "package staff\n\nimport _ \"example.com/harbor/pkg/identity/staffidentity\"\n"
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{noIdentity},
		},
		{
			name: "another auth's identity client is not this auth's",
			edit: func(t *testing.T, files map[string]string) {
				files[authFile] = strings.Replace(strings.Replace(files[authFile], "pkg/identity/staffidentity", "pkg/identity/membersidentity", 1), "staffidentity.NewClient", "membersidentity.NewClient", 1)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{noIdentity},
		},
		{
			name: "an identity client imported under another name is the auth's all the same",
			edit: func(_ *testing.T, files map[string]string) {
				files[authFile] = strings.Replace(strings.Replace(files[authFile], identityImport, "\tpeople \"example.com/harbor/pkg/identity/staffidentity\"\n", 1), "staffidentity.NewClient", "people.NewClient", 1)
				files[foothold] = invitingFoothold
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "an auth whose directory writes the roles needs no identity client for a first administrator: the directory decides who its administrators are",
			edit: func(t *testing.T, files map[string]string) {
				files[authFile] = withoutIdentity(t, fmt.Sprintf(admittingAuth, "google.RoleSync()"))
			},
			wantStatus:  Skip,
			wantSummary: "no auth brings in a first administrator",
		},
		{
			name: "an auth that invites from a package that never sets the acceptance hook is refused: the accepted invitation would give no role",
			edit: func(t *testing.T, files map[string]string) {
				if !strings.Contains(files[authFile], acceptanceHook) {
					t.Fatalf("the auth package's source lacks %q, which the case takes out", acceptanceHook)
				}
				files[authFile] = strings.Replace(files[authFile], acceptanceHook, "", 1)
				files[foothold] = invitingFoothold
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{foothold + ":9" + noHook},
		},
		{
			name: "an auth that brings in nobody is not held to the acceptance hook",
			edit: func(_ *testing.T, files map[string]string) {
				files[authFile] = strings.Replace(files[authFile], acceptanceHook, "", 1)
			},
			wantStatus:  Skip,
			wantSummary: "no auth brings in a first administrator",
		},
		{
			name: "an application that seeds each auth's cast before that auth's own invitation, one auth after the other, is in order",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = invitingFoothold
				files[migrateFile] = fmt.Sprintf(migrateCommand, seedsStep+ownInvitation+strings.Replace(seedsStep, "data.Staff()", "data.Members()", 1)+strings.Replace(ownInvitation, "data.Staff()", "data.Members()", 1))
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "an auth's own invitation says nothing of another auth's cast seeded after it",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = invitingFoothold
				files[migrateFile] = fmt.Sprintf(migrateCommand, ownInvitation+strings.Replace(seedsStep, "data.Staff()", "data.Members()", 1))
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "a migrate command that seeds the cast after it invites is refused: a persona may hold the Super Administrator role, so the cast goes first",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = invitingFoothold
				files[migrateFile] = fmt.Sprintf(migrateCommand, invitesStep+seedsStep)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{migrateFile + ":10" + seededLate},
		},
		{
			name: "a migrate command that invites through the auth's own function and seeds that auth's cast after is refused the same",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = invitingFoothold
				files[migrateFile] = fmt.Sprintf(migrateCommand, ownInvitation+seedsStep)
			},
			wantStatus:  Fail,
			wantSummary: "1 finding(s) on how a first administrator is brought in",
			wantDetails: []string{migrateFile + ":10" + seededLate},
		},
		{
			name: "a migrate command that seeds the cast before it invites",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = invitingFoothold
				files[migrateFile] = fmt.Sprintf(migrateCommand, seedsStep+invitesStep)
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "a cast seeded in one function and an invitation asked in another are not put in an order",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = invitingFoothold
				files[migrateFile] = fmt.Sprintf(migrateCommand, invitesStep) + "\nfunc seedCasts(ctx context.Context, data *config.DataConfiguration) error {\n\t_, err := data.Staff().SeedCast(ctx)\n\n\treturn err\n}\n"
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "what a test or a generated file seeds after an invitation is not read",
			edit: func(_ *testing.T, files map[string]string) {
				files[foothold] = invitingFoothold
				files["cmd/deployment/migrate/main_test.go"] = fmt.Sprintf(migrateCommand, invitesStep+seedsStep)
				files["cmd/deployment/migrate/zz_gen_steps.go"] = fmt.Sprintf(migrateCommand, invitesStep+seedsStep)
			},
			wantStatus:  Pass,
			wantSummary: "the first administrator is invited: staff",
		},
		{
			name: "an auth with no role file gives no role, so nobody is to be invited to one",
			edit: func(t *testing.T, files map[string]string) {
				files[authFile] = withoutIdentity(t, files[authFile])
				delete(files, "pkg/auth/staff/roles.json")
			},
			wantStatus:  Skip,
			wantSummary: "no auth brings in a first administrator",
		},
		{
			name: "an application with no auth package",
			edit: func(_ *testing.T, files map[string]string) {
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
				tt.edit(t, files)
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
