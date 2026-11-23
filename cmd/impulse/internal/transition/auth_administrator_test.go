package transition

import (
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
)

// directoryRoles is a new application whose first auth, staff, signs in through a Google
// directory that gives every role: one that holds nothing that invites a first
// administrator.
func directoryRoles(t *testing.T) *app.App {
	t.Helper()

	start := directoryRolesStart(app.MethodGoogle, "fleet.example")
	a, _ := started(t, &start, "send_staff_user_password_reset_link", "assign_staff_role", "remove_staff_role", "invite_staff_user", "withdraw_staff_invitation")

	return a
}

// added applies impulse add auth to an application and returns the change.
func added(t *testing.T, a *app.App, au *Auth) *Change {
	t.Helper()

	ch, err := au.Apply(t.Context(), a, &fakeExec{})
	if err != nil {
		t.Fatalf("Auth.Apply() error = %v", err)
	}

	return ch
}

// wellFormed holds a Go file of the application to what the compiler and gofmt take.
func wellFormed(t *testing.T, a *app.App, rel string) {
	t.Helper()

	text := read(t, a, rel)
	if _, err := parser.ParseFile(token.NewFileSet(), rel, text, parser.SkipObjectResolution); err != nil {
		t.Errorf("%s does not parse: %v", rel, err)
	}
	if formatted, err := format.Source([]byte(text)); err != nil || string(formatted) != text {
		t.Errorf("%s is not as gofmt writes it (error %v)", rel, err)
	}
}

// The lines impulse add auth says of the partners auth's first administrator.
const (
	partnersSettingDid = "pkg/config/data.go: dataConfig reads the address of the partners auth's first administrator from APP_PARTNERS_ADMINISTRATOR, a setting with no secret tag; pkg/config/partners.go: its accessor PartnersAdministrator()"
	// partnersSettingSaid is the setting's line where the level's comment on its mail
	// sender had lost what it says of an invitation, and says it again.
	partnersSettingSaid = "pkg/config/data.go: dataConfig reads the address of the partners auth's first administrator from APP_PARTNERS_ADMINISTRATOR, a setting with no secret tag; the comment on openMailSender says again that the log is no mailbox to invite a first administrator through, so the mailbox comes before the first release; pkg/config/partners.go: its accessor PartnersAdministrator()"
	mailSenderSaid      = "// start: the links and codes it mails are secrets, and a log is no place for them. The\n// log is no mailbox (mail.Delivers): while nobody of an auth holds the Super\n// Administrator role the migrate command refuses to invite its first administrator\n// through it, since an invitation's link is never written to a log, so the mailbox and\n// its app password come before the first release.\nfunc openMailSender("
	partnersEnvDid      = ".envrc.template: the partners auth's first administrator's variable (APP_PARTNERS_ADMINISTRATOR), unset: a deployment names the address per environment"
	partnersCommandDid  = "cmd/deployment/migrate/main.go: the invitation of the partners auth's first administrator, which a release runs after its role check (deploy.InviteAdministrators), and the -reinvite-administrator flag, which mails a fresh invitation where the first was lost (deploy.ReinviteAdministrator)"
	partnersTestHeld    = "cmd/deployment/migrate/main_test.go: the cases that give the staff auth a holder of the Super Administrator role give the partners auth one too (withPartnersHolder), and expect the line a release prints for each auth, since a release now invites the partners auth's first administrator and stops while nobody of the auth holds the role and APP_PARTNERS_ADMINISTRATOR names no address"
	partnersTestSaid    = "cmd/deployment/migrate/main_test.go: the case that proved a release invites nobody now proves it stops for the partners auth's first administrator while APP_PARTNERS_ADMINISTRATOR names no address, and the case that proved the command has no -reinvite-administrator flag now proves the flag refuses the staff auth's name, since the partners auth invites a first administrator and the staff auth's directory gives every role"
	// partnersIdentityAccessor is the accessor of the auth's identity client the data level
	// gains beside the accessor of the auth.
	partnersIdentityAccessor = "\n// PartnersIdentity returns the partners auth's identity client, which the auth built when it\n// opened: what the generated methods that manage the auth's people run over.\nfunc (c *DataConfiguration) PartnersIdentity() *partnersidentity.Client {\n\treturn c.partners.Identity()\n}\n"
	partnersTestLeft         = "cmd/deployment/migrate/main_test.go: a release now invites the partners auth's first administrator, and refuses while nobody of the auth holds the Super Administrator role and APP_PARTNERS_ADMINISTRATOR names no address. Give the cases that run a release the address or a holder of the role, take out a case that says nobody is invited, that the command has no -reinvite-administrator flag or that the flag refuses the partners auth's name, and prove the auth's invitation as the base's test of the command proves its own"
	partnersRolesLeft        = "pkg/auth/partners/foothold.go: the first administrator the migrate command invites while nobody of the auth holds the Super Administrator role, by an invitation mailed to the address the placement names (APP_PARTNERS_ADMINISTRATOR) that gives the Super Administrator role and the first administrator's roles; no account and no password are made ahead of the person, who signs in from the link by a method of the auth. Author AdministratorRoles (the roles whose holder can give a role back) and FirstAdministratorRoles (what the invitation gives) with the role file"
	partnersAccessor         = "\n// PartnersAdministrator returns the email address of the partners auth's first administrator,\n// which the placement names and the migrate command has the invitation mailed to; empty\n// where the deployment names none.\nfunc (c *DataConfiguration) PartnersAdministrator() string {\n\treturn c.env.PartnersAdministrator\n}\n"
	partnersEnvBlock         = "# APP_PARTNERS_ADMINISTRATOR is the email address of the partners auth's first administrator\n# (pkg/auth/partners, InviteAdministrator): while nobody of the auth holds the Super Administrator\n# role the migrate command has an invitation mailed there, and the person signs in from\n# its link. A deployment names it per environment, with the mailbox (APP_MAIL_MAILBOX),\n# since the link is never written to a log; no release runs in development, so it stays\n# unset here.\n# export APP_PARTNERS_ADMINISTRATOR=\n"
)

// TestAuth_Apply_invitesAdministrator holds what impulse add auth writes outside the
// auth's package for an auth that invites a first administrator: the address on the data
// level, the auth among what the deployment invites, the migrate command's invitation
// and the environment template's variable, and what it leaves the author.
func TestAuth_Apply_invitesAdministrator(t *testing.T) {
	t.Parallel()

	t.Run("beside an auth that invites one, the auth joins what the deployment invites", func(t *testing.T) {
		t.Parallel()

		a := newBase(t, "staff")
		command := read(t, a, migrateCommand)
		ch := added(t, a, &Auth{Name: "partners", Outlet: app.DefaultOutletName})

		deploy := read(t, a, deployFile)
		for _, want := range []string{
			"func AdministratorAuths() []string {\n\treturn []string{staff.Name, partners.Name}\n}\n",
			"\tcase staff.Name:\n\t\treturn &firstAdministrator{email: data.StaffAdministrator(), invite: data.Staff().InviteAdministrator, reinvite: data.Staff().ReinviteAdministrator}, nil\n\tcase partners.Name:\n\t\treturn &firstAdministrator{email: data.PartnersAdministrator(), invite: data.Partners().InviteAdministrator, reinvite: data.Partners().ReinviteAdministrator}, nil\n\tdefault:\n",
			"\t\"example.com/acme/depot/pkg/auth/partners\"\n",
		} {
			if !strings.Contains(deploy, want) {
				t.Errorf("%s does not contain %q:\n%s", deployFile, want, deploy)
			}
		}
		level := read(t, a, baseDataLevel)
		if want := "\tStaffAdministrator string `env:\"APP_STAFF_ADMINISTRATOR\"`\n\n\t// PartnersAdministrator is the email address of the partners auth's first administrator\n\t// (partners.AdministratorVariable), which the placement names for each environment:\n\t// while nobody of the auth holds the Super Administrator role the migrate command\n\t// has an invitation mailed there (partners.Auth.InviteAdministrator), and the person\n\t// signs in from its link. It is an address, not a secret, and no password is\n\t// generated for the person. Unset in development, where no release runs and nobody\n\t// is invited.\n\tPartnersAdministrator string `env:\"APP_PARTNERS_ADMINISTRATOR\"`\n}\n"; !strings.Contains(level, want) {
			t.Errorf("%s does not read the address into its environment struct as %q:\n%s", baseDataLevel, want, level)
		}
		if strings.Count(level, mailSenderSaid) != 1 {
			t.Errorf("%s does not say once on its mail sender what the base says of an invitation:\n%s", baseDataLevel, level)
		}
		if accessors := read(t, a, "pkg/config/partners.go"); !strings.HasSuffix(accessors, "func (c *DataConfiguration) Partners() *partners.Auth {\n\treturn c.partners\n}\n"+partnersIdentityAccessor+partnersAccessor) {
			t.Errorf("pkg/config/partners.go does not end in the accessor of the address:\n%s", accessors)
		}
		for _, rel := range []string{deployFile, baseDataLevel, "pkg/config/partners.go"} {
			wellFormed(t, a, rel)
		}
		if got := read(t, a, migrateCommand); got != command {
			t.Errorf("%s changed, and it runs the invitation of every auth the deployment names already:\n%s", migrateCommand, cmp.Diff(command, got))
		}
		if template := read(t, a, baseEnvTemplate); !strings.HasSuffix(template, "\n"+partnersEnvBlock) || len(administratorEnvRE("APP_PARTNERS_ADMINISTRATOR").FindAllString(template, -1)) != 1 {
			t.Errorf("%s does not end in the variable's block, as the base writes one:\n%s", baseEnvTemplate, template)
		}
		for _, did := range []string{
			partnersSettingDid,
			deployFile + ": the partners auth joins AdministratorAuths and administratorOf, so a release invites its first administrator after the others' and the migrate command's -reinvite-administrator takes its name",
			partnersEnvDid,
			partnersTestHeld,
		} {
			once(t, ch.Did, did)
		}
		for _, left := range []string{
			partnersRolesLeft,
			deployFile + ", " + migrateCommand + ": the comments on the first administrator speak of the auths the application had (AdministratorAuths, InviteAdministrators, the migrate command's package comment and its step after the role check); name the partners auth and its setting (APP_PARTNERS_ADMINISTRATOR) in them",
		} {
			once(t, ch.Skipped, left)
		}
		if got := linesWith(ch.Skipped, "", []string{"nothing that invites"}); len(got) != 0 {
			t.Errorf("the change hands the invitation on to the author, and it wrote it: %q", got)
		}
		if got := linesWith(ch.Did, migrateCommand, nil); len(got) != 0 {
			t.Errorf("the change says it wrote the migrate command, which it left as it was: %q", got)
		}
	})

	t.Run("beside an auth whose directory gives every role, what invites one is written for the auth", func(t *testing.T) {
		t.Parallel()

		a := directoryRoles(t)
		ch := added(t, a, &Auth{Name: "partners", Outlet: app.DefaultOutletName})

		administrators := read(t, a, deployAdministrators)
		if want := "package deploy\n\nimport (\n\t\"context\"\n\t\"fmt\"\n\t\"io\"\n\t\"strings\"\n\t\"time\"\n\n\t\"example.com/acme/depot/pkg/auth/partners\"\n\t\"example.com/acme/depot/pkg/config\"\n\t\"github.com/go-playground/errors/v5\"\n\t\"github.com/impulseframework/impulse/session\"\n)\n\n// AdministratorAuths names the auths whose first administrator the deployment invites, in\n// the order a release invites them: the partners auth."; !strings.HasPrefix(administrators, want) {
			t.Errorf("%s does not open as %q:\n%s", deployAdministrators, want, administrators)
		}
		for _, want := range []string{
			"func AdministratorAuths() []string {\n\treturn []string{partners.Name}\n}\n",
			"type firstAdministrator struct {\n",
			"\tcase partners.Name:\n\t\treturn &firstAdministrator{email: data.PartnersAdministrator(), invite: data.Partners().InviteAdministrator, reinvite: data.Partners().ReinviteAdministrator}, nil\n\tdefault:\n",
			"environment (APP_PARTNERS_ADMINISTRATOR) and gives the Super Administrator role and the\n// auth's first administrator's roles; " + passwordChosen + " No account is created ahead of them and no password is generated.\n",
			"func InviteAdministrators(ctx context.Context, data *config.DataConfiguration, out io.Writer) error {\n",
			"func ReinviteAdministrator(ctx context.Context, data *config.DataConfiguration, auth string, out io.Writer) error {\n",
			"func invitationRefused(err error, auth, method string) error {\n",
			"func reportAdministrator(out io.Writer, auth string, invited *session.FirstAdministrator) {\n",
		} {
			if !strings.Contains(administrators, want) {
				t.Errorf("%s does not contain %q:\n%s", deployAdministrators, want, administrators)
			}
		}
		if strings.Contains(administrators, "staff") {
			t.Errorf("%s names the staff auth, which invites nobody:\n%s", deployAdministrators, administrators)
		}
		rendered, err := baseRendering(deployAdministratorsTest, "example.com/acme/depot", "partners")
		if err != nil {
			t.Fatalf("baseRendering() error = %v", err)
		}
		if got := read(t, a, deployAdministratorsTest); got != string(rendered) {
			t.Errorf("%s is not the base's test of what the deployment reports, for the partners auth:\n%s", deployAdministratorsTest, cmp.Diff(string(rendered), got))
		}
		if deploy := read(t, a, deployFile); !strings.HasPrefix(deploy, "// Package deploy holds the database steps a deployment runs and the development\n// bootstrap reuses: applying the schema migrations, checking the release's role policy\n// against what the permission engine's store holds, inviting each auth's first\n// administrator while nobody of the auth holds the Super Administrator role, and bringing\n// the feature flags to the release's declarations.\npackage deploy\n") {
			t.Errorf("%s does not say in its package comment that the deployment invites a first administrator:\n%s", deployFile, deploy)
		}
		// The migrate command is the base's again, byte for byte, but for the three
		// places its comments name the auth that invites, which is the new one.
		base, err := baseRendering(migrateCommand, "example.com/acme/depot", "staff")
		if err != nil {
			t.Fatalf("baseRendering() error = %v", err)
		}
		want := string(base)
		for _, named := range []string{
			"// invites the %s auth's first administrator while nobody of the auth holds the Super\n",
			"// mail that was lost: it withdraws the standing invitation of the named auth (%s),\n",
			"\t// administer it, so the %s auth's first administrator is invited now, after the\n",
		} {
			held := strings.Replace(named, "%s", "staff", 1)
			if strings.Count(want, held) != 1 {
				t.Fatalf("the base's %s holds %q %d times, want once", migrateCommand, held, strings.Count(want, held))
			}
			want = strings.Replace(want, held, strings.Replace(named, "%s", "partners", 1), 1)
		}
		if got := read(t, a, migrateCommand); got != want {
			t.Errorf("%s is not the base's command with the invitation named for the partners auth (-want +got):\n%s", migrateCommand, cmp.Diff(want, got))
		}
		if level := read(t, a, baseDataLevel); !strings.Contains(level, "\t// generated for the person. Unset in development, where no release runs and nobody\n\t// is invited.\n\tPartnersAdministrator string `env:\"APP_PARTNERS_ADMINISTRATOR\"`\n}\n") {
			t.Errorf("%s does not read the address into its environment struct:\n%s", baseDataLevel, level)
		}
		if accessors := read(t, a, "pkg/config/partners.go"); !strings.HasSuffix(accessors, partnersAccessor) {
			t.Errorf("pkg/config/partners.go does not end in the accessor of the address:\n%s", accessors)
		}
		for _, rel := range []string{deployAdministrators, deployAdministratorsTest, deployFile, migrateCommand, baseDataLevel, "pkg/config/partners.go"} {
			wellFormed(t, a, rel)
		}
		if template := read(t, a, baseEnvTemplate); !strings.HasSuffix(template, "\n"+partnersEnvBlock) {
			t.Errorf("%s does not end in the variable's block:\n%s", baseEnvTemplate, template)
		}
		if level := read(t, a, baseDataLevel); strings.Count(level, mailSenderSaid) != 1 {
			t.Errorf("%s does not say once on its mail sender that the log is no mailbox to invite through, as the base says it:\n%s", baseDataLevel, level)
		}
		for _, did := range []string{
			partnersSettingSaid,
			deployAdministrators + ", " + deployAdministratorsTest + ", " + deployFile + ": what invites a first administrator, which the application had none of, written for the partners auth as impulse new writes it: AdministratorAuths, InviteAdministrators and ReinviteAdministrator with what they stand on, the test of what they report where the package had none, and the package comment's sentence on the invitation",
			partnersCommandDid,
			partnersEnvDid,
			partnersTestSaid,
		} {
			once(t, ch.Did, did)
		}
		for _, left := range []string{partnersRolesLeft} {
			once(t, ch.Skipped, left)
		}
		if got := linesWith(ch.Skipped, "", []string{"the comments on the first administrator"}); len(got) != 0 {
			t.Errorf("the change says the comments speak of other auths, and it wrote them for this one: %q", got)
		}
	})

	t.Run("an auth whose people sign in through a directory alone is not said to choose a password", func(t *testing.T) {
		t.Parallel()

		a := directoryRoles(t)
		added(t, a, &Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityApplication}}, Outlet: app.DefaultOutletName})

		administrators := read(t, a, deployAdministrators)
		if !strings.Contains(administrators, "auth's first administrator's roles; "+directoryProven+" No account is created ahead of them and no password is generated.\n") || strings.Contains(administrators, "chooses a") {
			t.Errorf("%s does not say that the first administrator signs in through the directory:\n%s", deployAdministrators, administrators)
		}
		wellFormed(t, a, deployAdministrators)
		if level := read(t, a, baseDataLevel); !strings.Contains(level, "\t// has an invitation mailed there (partners.Auth.InviteAdministrator), and the person's\n\t// first sign-in through the directory, from its link, is tied to the roles by the\n\t// invitation; the address the directory gives must be the invited one. Unset in\n\t// development, where no release runs and nobody is invited.\n\tPartnersAdministrator string `env:\"APP_PARTNERS_ADMINISTRATOR\"`\n") {
			t.Errorf("%s does not say that the first administrator signs in through the directory:\n%s", baseDataLevel, level)
		}
	})

	t.Run("a deployment package whose comment somebody changed keeps it, and the change says what to say there", func(t *testing.T) {
		t.Parallel()

		a := directoryRoles(t)
		rewriteOnce(t, a, deployFile, "and bringing the feature\n// flags to the release's declarations.\npackage deploy\n", "and bringing the\n// feature flags to what the release declares.\npackage deploy\n")
		deploy := read(t, a, deployFile)
		ch := added(t, a, &Auth{Name: "partners", Outlet: app.DefaultOutletName})

		if got := read(t, a, deployFile); got != deploy {
			t.Errorf("%s changed, and its package comment is somebody's own:\n%s", deployFile, cmp.Diff(deploy, got))
		}
		if !strings.Contains(read(t, a, deployAdministrators), "func InviteAdministrators(") {
			t.Errorf("%s does not hold the invitation, which a comment was not to stop", deployAdministrators)
		}
		once(t, ch.Skipped, deployFile+": its package comment was left as it is; say there that the deployment invites the partners auth's first administrator, where it does not")
		if got := linesWith(ch.Did, deployAdministrators+", "+deployAdministratorsTest+": what invites a first administrator, which the application had none of, written for the partners auth", nil); len(got) != 1 {
			t.Errorf("the change does not say once what it wrote, the package's own file not among them: %q", ch.Did)
		}
	})

	t.Run("a test of the deployment's report that is there already is kept", func(t *testing.T) {
		t.Parallel()

		const own = "package deploy\n\nimport \"testing\"\n\nfunc TestOwn(t *testing.T) {\n\tt.Parallel()\n}\n"
		a := directoryRoles(t)
		if err := os.WriteFile(a.Abs(deployAdministratorsTest), []byte(own), 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
		ch := added(t, a, &Auth{Name: "partners", Outlet: app.DefaultOutletName})

		if got := read(t, a, deployAdministratorsTest); got != own {
			t.Errorf("%s was written over, and it is somebody's own:\n%s", deployAdministratorsTest, got)
		}
		if got := linesWith(ch.Did, deployAdministrators+", "+deployFile+": what invites a first administrator, which the application had none of, written for the partners auth", nil); len(got) != 1 {
			t.Errorf("the change does not say once what it wrote, the test not among them: %q", ch.Did)
		}
	})

	t.Run("an environment template that names the variable already is left as it is", func(t *testing.T) {
		t.Parallel()

		a := newBase(t, "staff")
		rewriteOnce(t, a, baseEnvTemplate, "# export APP_STAFF_ADMINISTRATOR=\n", "# export APP_STAFF_ADMINISTRATOR=\nexport APP_PARTNERS_ADMINISTRATOR=first@partners.example\n")
		template := read(t, a, baseEnvTemplate)
		ch := added(t, a, &Auth{Name: "partners", Outlet: app.DefaultOutletName})

		if got := read(t, a, baseEnvTemplate); got != template {
			t.Errorf("%s changed, and it named the variable already:\n%s", baseEnvTemplate, cmp.Diff(template, got))
		}
		if got := linesWith(ch.Did, baseEnvTemplate, []string{"first administrator"}); len(got) != 0 {
			t.Errorf("the change says it named the variable in the template, which named it already: %q", got)
		}
		once(t, ch.Did, partnersSettingDid)
	})

	t.Run("an application with no environment template is told the variable to name", func(t *testing.T) {
		t.Parallel()

		a := newBase(t, "staff")
		if err := os.Remove(a.Abs(baseEnvTemplate)); err != nil {
			t.Fatalf("os.Remove() error = %v", err)
		}
		a, err := app.Discover(a.Root)
		if err != nil {
			t.Fatalf("app.Discover() error = %v", err)
		}
		ch := added(t, a, &Auth{Name: "partners", Outlet: app.DefaultOutletName})

		once(t, ch.Skipped, "no environment template (.envrc.template, .env.template, .env.example) to name the partners auth's first administrator in (APP_PARTNERS_ADMINISTRATOR)")
		once(t, ch.Did, partnersSettingDid)
		if exists(a, baseEnvTemplate) {
			t.Errorf("%s was written, and the application had none", baseEnvTemplate)
		}
	})

	t.Run("an auth whose directory gives every role invites nobody, and nothing is written for one", func(t *testing.T) {
		t.Parallel()

		a := newBase(t, "staff")
		before := map[string]string{}
		for _, rel := range []string{deployFile, migrateCommand, baseEnvTemplate} {
			before[rel] = read(t, a, rel)
		}
		ch := added(t, a, &Auth{Name: "partners", Methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "workspace", Authority: AuthorityDirectory}}, Outlet: app.DefaultOutletName})

		for _, rel := range []string{deployFile, migrateCommand} {
			if got := read(t, a, rel); got != before[rel] {
				t.Errorf("%s changed for an auth that invites nobody:\n%s", rel, cmp.Diff(before[rel], got))
			}
		}
		for rel, never := range map[string]string{baseDataLevel: "PartnersAdministrator", "pkg/config/partners.go": "PartnersAdministrator", baseEnvTemplate: "APP_PARTNERS_ADMINISTRATOR"} {
			if strings.Contains(read(t, a, rel), never) {
				t.Errorf("%s names %s, and the partners auth invites nobody", rel, never)
			}
		}
		if got := append(linesWith(ch.Did, "", []string{"first administrator"}), linesWith(ch.Skipped, "", []string{"first administrator"})...); len(got) != 0 {
			t.Errorf("the change speaks of a first administrator, and the partners auth invites nobody: %q", got)
		}
	})
}

// TestAuth_Apply_administratorNotWritten holds the all or nothing of what impulse add
// auth writes for a first administrator: where the data level, the deployment package or
// the migrate command is not as the tool knows it, none of it is written, and the change
// says which file stopped it and what to write by hand.
func TestAuth_Apply_administratorNotWritten(t *testing.T) {
	t.Parallel()

	const handStep = ", so nothing that invites the partners auth's first administrator was written outside its package, and no release invites one. Read the address on the data level (APP_PARTNERS_ADMINISTRATOR, a setting with no secret tag, behind a PartnersAdministrator() accessor), name the auth in the deployment package's AdministratorAuths and administratorOf, and have the migrate command call deploy.InviteAdministrators after its role check and deploy.ReinviteAdministrator from a -reinvite-administrator flag, as impulse new writes them for the auth an application starts with"
	tests := []struct {
		name string
		// directoryRoles starts the application from an auth whose directory gives every
		// role, which holds nothing that invites a first administrator.
		directoryRoles bool
		// level is where the application keeps its data level, where that is not where
		// the base keeps it.
		level   string
		edit    func(t *testing.T, a *app.App)
		wantWhy string
	}{
		{
			name: "the data level reads its environment into no struct of its own",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, baseDataLevel, "\tenv *dataConfig\n", "\tenv any\n")
			},
			wantWhy: "pkg/config/data.go: DataConfiguration keeps no environment struct (a field env *T) to read the address into",
		},
		{
			name: "the deployment's list of auths is not one list",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, deployFile, "\treturn []string{staff.Name}\n", "\tauths := []string{staff.Name}\n\n\treturn auths\n")
			},
			wantWhy: "pkg/deploy/deploy.go is not as the base writes it (AdministratorAuths does not return its auths as one list, return []string{...})",
		},
		{
			name: "the deployment answers an auth's first administrator some other way",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, deployFile, "func administratorOf(data *config.DataConfiguration, auth string)", "func administratorOf(level *config.DataConfiguration, auth string)")
			},
			wantWhy: "pkg/deploy/deploy.go is not as the base writes it (administratorOf is not a switch over the auth's name with a default case, taking the data level as data)",
		},
		{
			name: "the deployment package holds part of what invites one",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, deployFile, "func AdministratorAuths() []string {", "func InvitedAuths() []string {")
			},
			wantWhy: "pkg/deploy declares InviteAdministrators, ReinviteAdministrator, administratorOf, firstAdministrator, invitationRefused, reportAdministrator and not AdministratorAuths with administratorOf in one file",
		},
		{
			name: "no file declares the data level",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, baseDataLevel, "type DataConfiguration struct {", "type DataLevel struct {")
			},
			wantWhy: "no file declares a DataConfiguration struct",
		},
		{
			name: "the data level reads its environment into a struct of another file",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, baseDataLevel, "\tenv *dataConfig\n", "\tenv *environment\n")
			},
			wantWhy: "pkg/config/data.go declares no struct environment, which its DataConfiguration reads the environment into",
		},
		{
			name: "no migrate command",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				if err := os.Remove(a.Abs(migrateCommand)); err != nil {
					t.Fatalf("os.Remove() error = %v", err)
				}
			},
			wantWhy: "cmd/deployment/migrate/main.go is not there",
		},
		{
			name:           "no deployment package",
			directoryRoles: true,
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				if err := os.RemoveAll(a.Abs(deployDir)); err != nil {
					t.Fatalf("os.RemoveAll() error = %v", err)
				}
			},
			wantWhy: "pkg/deploy/deploy.go is not there",
		},
		{
			name:           "the data level is kept where the base's deployment package does not import it from",
			directoryRoles: true,
			level:          "pkg/settings/data.go",
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				if err := os.Rename(a.Abs("pkg/config"), a.Abs("pkg/settings")); err != nil {
					t.Fatalf("os.Rename() error = %v", err)
				}
			},
			wantWhy: "the data level is in pkg/settings and the auths are under pkg/auth, and the base's deployment package imports them from pkg/config and pkg/auth",
		},
		{
			name:           "the migrate command is not as the start left it",
			directoryRoles: true,
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				rewriteOnce(t, a, migrateCommand, "\treturn migrate(ctx, settings, o.seed)\n", "\treturn migrate(ctx, settings, o.seed || o.force.set)\n")
			},
			wantWhy: "cmd/deployment/migrate/main.go runs no invitation (deploy.InviteAdministrators(...)) and is not as a start whose directory gives every role leaves it (\"\\t\\treturn retireSecret(ctx, settings, o.retireSecret, out)\\n\\t}\\n\\n\\treturn migrate(ctx, settings, o.seed)\\n}\\n\\n// report prints one line per migrations table\" is not there once)",
		},
		{
			name:           "the deployment package has a file of the name already",
			directoryRoles: true,
			edit: func(t *testing.T, a *app.App) {
				t.Helper()
				if err := os.WriteFile(a.Abs(deployAdministrators), []byte("package deploy\n"), 0o644); err != nil {
					t.Fatalf("os.WriteFile() error = %v", err)
				}
			},
			wantWhy: "pkg/deploy/administrators.go is there and declares nothing that invites a first administrator",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var a *app.App
			if tt.directoryRoles {
				a = directoryRoles(t)
			} else {
				a = newBase(t, "staff")
			}
			tt.edit(t, a)
			had := exists(a, deployAdministrators)
			before := map[string]string{}
			for _, rel := range []string{deployFile, deployAdministrators, migrateCommand, baseEnvTemplate} {
				if exists(a, rel) {
					before[rel] = read(t, a, rel)
				}
			}
			ch := added(t, a, &Auth{Name: "partners", Outlet: app.DefaultOutletName})

			once(t, ch.Skipped, tt.wantWhy+handStep)
			once(t, ch.Skipped, partnersRolesLeft)
			for rel, held := range before {
				if got := read(t, a, rel); got != held {
					t.Errorf("%s changed, and nothing that invites the auth's first administrator was to be written:\n%s", rel, cmp.Diff(held, got))
				}
			}
			level := baseDataLevel
			if tt.level != "" {
				level = tt.level
			}
			for _, rel := range []string{level, path.Join(path.Dir(level), "partners.go")} {
				if exists(a, rel) && strings.Contains(read(t, a, rel), "PartnersAdministrator") {
					t.Errorf("%s reads the address, and nothing that invites the auth's first administrator was to be written", rel)
				}
			}
			if !had && exists(a, deployAdministrators) {
				t.Errorf("%s was written, and nothing that invites the auth's first administrator was to be", deployAdministrators)
			}
			if got := linesWith(ch.Did, "", []string{"first administrator"}); len(got) != 0 {
				t.Errorf("the change says it wrote for a first administrator, and it was to write nothing: %q", got)
			}
			if got := linesWith(ch.Skipped, migrateTest, nil); len(got) != 0 {
				t.Errorf("the change says the migrate command's test no longer holds, and no release invites the auth's first administrator: %q", got)
			}
		})
	}
}
