package transition

import (
	"bytes"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
)

// administratorPlan is what invites one auth's first administrator outside the auth's
// own package, ready to write: each file as it will stand, what the change did in it and
// what it leaves the author.
type administratorPlan struct {
	// level is the file that declares the data level, which the address is read on.
	level string
	files []rewritten
	did   []string
	left  []string
}

// file takes one file into the plan as it will stand.
func (p *administratorPlan) file(rel string, data []byte, mode os.FileMode) {
	p.files = append(p.files, rewritten{rel: rel, data: data, mode: mode})
}

// password reports an auth one of whose methods is the password method.
func (au Auth) password() bool {
	for _, m := range au.methods() {
		if m.Kind == app.MethodPassword {
			return true
		}
	}

	return false
}

// inviteAdministrator writes what invites the new auth's first administrator outside the
// auth's own package, as impulse new writes it for the auth an application starts with:
// the address on the data level with its accessor, the auth among the auths the
// deployment package invites, the invitation a release runs after its role check with
// the migrate command's -reinvite-administrator flag, and the variable in the
// environment template. It is all written or none of it: where the data level, the
// deployment package or the migrate command is not as the tool knows it, the change says
// which and what to write by hand. The roles the invitation gives stay the author's,
// with the role file.
func (au Auth) inviteAdministrator(a *app.App, src *authSource, ch *Change) error {
	plan := &administratorPlan{}
	steps := []func(*app.App, *authSource, *administratorPlan) (string, error){au.administratorSetting, au.administratorDeployment, au.administratorCommand}
	for _, step := range steps {
		why, err := step(a, src, plan)
		if err != nil {
			return err
		}
		if why != "" {
			ch.skipf("%s, so nothing that invites the %s auth's first administrator was written outside its package, and no release invites one. Read the address on the data level (%s, a setting with no secret tag, behind a %sAdministrator() accessor), name the auth in the deployment package's AdministratorAuths and administratorOf, and have the migrate command call deploy.InviteAdministrators after its role check and deploy.ReinviteAdministrator from a -reinvite-administrator flag, as impulse new writes them for the auth an application starts with", why, au.Name, authpkg.AdministratorVariable(au.Name), au.Pascal())

			return nil
		}
	}
	for i := range plan.files {
		f := &plan.files[i]
		if err := os.WriteFile(a.Abs(f.rel), f.data, f.mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
	}
	ch.Did = append(ch.Did, plan.did...)
	if err := au.administratorEnv(a, src, ch); err != nil {
		return err
	}
	ch.Skipped = append(ch.Skipped, plan.left...)
	if exists(a, migrateTest) {
		held, err := au.holdMigrateTest(a, src)
		if err != nil {
			return err
		}
		if held != "" {
			ch.didf("%s", held)

			return nil
		}
		ch.skipf("%[1]s: a release now invites the %[2]s auth's first administrator, and refuses while nobody of the auth holds the Super Administrator role and %[3]s names no address. Give the cases that run a release the address or a holder of the role, take out a case that says nobody is invited, that the command has no -reinvite-administrator flag or that the flag refuses the %[2]s auth's name, and prove the auth's invitation as the base's test of the command proves its own", migrateTest, au.Name, authpkg.AdministratorVariable(au.Name))
	}

	return nil
}

// administratorSetting plans the address of the auth's first administrator on the data
// level: a field of the struct the level reads its environment into, under the auth's
// variable, and an accessor on DataConfiguration in the file the auth's accessor went
// to. Where a start whose directory gives every role took what the level's comment on
// its mail sender says of an invitation, the sentence goes back.
func (au Auth) administratorSetting(a *app.App, _ *authSource, plan *administratorPlan) (why string, err error) {
	rel, data, mode, err := findDeclaringFile(a, "DataConfiguration")
	if err != nil {
		return "", err
	}
	if rel == "" {
		return "no file declares a DataConfiguration struct", nil
	}
	env := environmentStruct(rel, data)
	if env == "" {
		return rel + ": DataConfiguration keeps no environment struct (a field env *T) to read the address into", nil
	}
	edited, err := app.AddStructField(rel, data, env, au.administratorField())
	if errors.Is(err, app.ErrNoAnchor) {
		return fmt.Sprintf("%s declares no struct %s, which its DataConfiguration reads the environment into", rel, env), nil
	}
	if err != nil {
		return "", err
	}
	said := ""
	if rel == baseDataLevel {
		// A start whose directory gives every role took the sentence out with the rest;
		// a level that does not stand as that start left it keeps its comment as it is.
		rendering, err := baseRendering(baseDataLevel, modulePath(a), au.Name)
		if err != nil {
			return "", err
		}
		if restored, err := withPieces(edited, rendering, []piece{dataLevelDocPiece}); err == nil {
			edited = restored
			said = "; the comment on openMailSender says again that the log is no mailbox to invite a first administrator through, so the mailbox comes before the first release"
		}
	}
	// The file the auth's own accessor went to, which the command wrote beside the level
	// when it constructed the auth there.
	accessors := path.Join(path.Dir(rel), au.Name+".go")
	held, accessorsMode, err := readFile(a, accessors)
	if err != nil {
		return "", err
	}
	accessor := fmt.Sprintf(`
// %[1]sAdministrator returns the email address of the %[2]s auth's first administrator,
// which the placement names and the migrate command has the invitation mailed to; empty
// where the deployment names none.
func (c *DataConfiguration) %[1]sAdministrator() string {
	return c.env.%[1]sAdministrator
}
`, au.Pascal(), au.Name)
	plan.level = rel
	plan.file(rel, edited, mode)
	plan.file(accessors, append(held, accessor...), accessorsMode)
	plan.did = append(plan.did, fmt.Sprintf("%s: %s reads the address of the %s auth's first administrator from %s, a setting with no secret tag%s; %s: its accessor %sAdministrator()", rel, env, au.Name, authpkg.AdministratorVariable(au.Name), said, accessors, au.Pascal()))

	return "", nil
}

// administratorField is the field the data level's environment struct reads the address
// of the auth's first administrator into, set apart from the field before it, with its
// comment: what app.AddStructField takes.
func (au Auth) administratorField() string {
	signsIn := "and the person\n// signs in from its link. It is an address, not a secret, and no password is\n// generated for the person. Unset in development, where no release runs and nobody\n// is invited."
	if !au.password() {
		signsIn = "and the person's\n// sign-in through the directory from its link accepts it and is given the roles; the\n// address the directory gives must be the invited one. Unset in development, where no\n// release runs and nobody is invited."
	}

	return fmt.Sprintf("\n// %[1]sAdministrator is the email address of the %[2]s auth's first administrator\n// (%[2]s.AdministratorVariable), which the placement names for each environment:\n// while nobody of the auth holds the Super Administrator role the migrate command\n// has an invitation mailed there (%[2]s.Auth.InviteAdministrator), %[3]s\n%[1]sAdministrator string `env:%[4]q`", au.Pascal(), au.Name, signsIn, authpkg.AdministratorVariable(au.Name))
}

// administratorDeployment plans the auth among the auths the deployment package invites.
// Where the package holds what invites a first administrator, as an application does
// whose first auth invites one, the auth joins AdministratorAuths and administratorOf.
// Where it holds none of it, as an application does whose first auth's directory gives
// every role, the declarations are written for the auth as the base writes them
// (administratorsFile).
func (au Auth) administratorDeployment(a *app.App, src *authSource, plan *administratorPlan) (why string, err error) {
	declared, err := deploymentDeclarations(a)
	if err != nil {
		return "", err
	}
	if len(declared) == 0 {
		return au.administratorsFile(a, src, plan)
	}
	holder := declared["AdministratorAuths"]
	if holder == "" || declared["administratorOf"] != holder {
		held := make([]string, 0, len(declared))
		for name := range declared {
			held = append(held, name)
		}
		sort.Strings(held)

		return fmt.Sprintf("%s declares %s and not AdministratorAuths with administratorOf in one file", deployDir, listed(held)), nil
	}
	data, mode, err := readFile(a, holder)
	if err != nil {
		return "", err
	}
	edited, err := joinAdministrators(holder, data, au.Name, path.Join(modulePath(a), src.AuthDir, au.Name))
	if err != nil {
		return fmt.Sprintf("%s is not as the base writes it (%v)", holder, errors.Cause(err)), nil
	}
	plan.file(holder, edited, mode)
	plan.did = append(plan.did, fmt.Sprintf("%s: the %s auth joins AdministratorAuths and administratorOf, so a release invites its first administrator after the others' and the migrate command's -reinvite-administrator takes its name", holder, au.Name))
	plan.left = append(plan.left, fmt.Sprintf("%s, %s: the comments on the first administrator speak of the auths the application had (AdministratorAuths, InviteAdministrators, the migrate command's package comment and its step after the role check); name the %s auth and its setting (%s) in them", holder, migrateCommand, au.Name, authpkg.AdministratorVariable(au.Name)))

	return "", nil
}

// deploymentDeclarations finds what the deployment package declares of what invites a
// first administrator: each of administratorDeclarations it declares, with the file that
// declares it. It is empty for a package that declares none, and for an application with
// no deployment package.
func deploymentDeclarations(a *app.App) (map[string]string, error) {
	entries, err := os.ReadDir(a.Abs(deployDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "os.ReadDir()")
	}
	declared := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		rel := path.Join(deployDir, name)
		src, _, err := readFile(a, rel)
		if err != nil {
			return nil, err
		}
		// A file the tool cannot parse declares nothing it can join.
		file, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			found, _ := declaredNames(decl)
			for _, one := range found {
				for _, wanted := range administratorDeclarations {
					if one == wanted {
						declared[one] = rel
					}
				}
			}
		}
	}

	return declared, nil
}

// administratorAuthsRE matches AdministratorAuths as the base writes it: one list of
// auth names, returned.
var administratorAuthsRE = regexp.MustCompile(`(?m)^func AdministratorAuths\(\) \[\]string \{\n\treturn \[\]string\{([^{}\n]*)\}\n\}\n`)

// joinAdministrators names an auth among the auths a deployment package invites: last in
// the list AdministratorAuths returns, so a release invites its first administrator
// after the others', and as a case of administratorOf's switch, with the auth's package
// imported. It refuses a file whose two functions are not as the base writes them.
func joinAdministrators(rel string, src []byte, auth, pkgPath string) ([]byte, error) {
	list := administratorAuthsRE.FindSubmatchIndex(src)
	if list == nil {
		return nil, errors.New("AdministratorAuths does not return its auths as one list, return []string{...}")
	}
	named := auth + ".Name"
	if held := strings.TrimSpace(string(src[list[2]:list[3]])); held != "" {
		named = held + ", " + named
	}
	text := string(src[:list[2]]) + named + string(src[list[3]:])
	of, err := declarationText(rel, []byte(text), "administratorOf")
	if err != nil {
		return nil, err
	}
	if !strings.Contains(of, "func administratorOf(data *") || strings.Count(of, "\tdefault:\n") != 1 {
		return nil, errors.New("administratorOf is not a switch over the auth's name with a default case, taking the data level as data")
	}
	pascal := names.Pascal(auth)
	with := strings.Replace(of, "\tdefault:\n", fmt.Sprintf("\tcase %[1]s.Name:\n\t\treturn &firstAdministrator{email: data.%[2]sAdministrator(), invite: data.%[2]s().InviteAdministrator, reinvite: data.%[2]s().ReinviteAdministrator}, nil\n\tdefault:\n", auth, pascal), 1)

	return app.AddImport(rel, []byte(strings.Replace(text, of, with, 1)), pkgPath)
}

// The sentence of the base's InviteAdministrators that says how a first administrator
// signs in from the invitation's link, which holds for an auth with a password method,
// and what stands in its place for an auth whose every method is a directory's.
const (
	passwordChosen  = "the person signs in from its link and chooses a\n// password there."
	directoryProven = "the person signs in from its link through\n// the directory, whose address for them must be the invited one."
)

// administratorsFile plans the deployment package's declarations of what invites a first
// administrator, for a package that holds none: a file of their own, each declaration as
// the base writes it for an application whose first auth is this one, with the base's
// test of what they report where the package has none, and the package comment's
// sentence on the invitation. The base's declarations import the data level and the
// auths from where the base keeps them, so an application that keeps them elsewhere is
// refused.
func (au Auth) administratorsFile(a *app.App, src *authSource, plan *administratorPlan) (why string, err error) {
	module := modulePath(a)
	switch {
	case !exists(a, deployFile):
		return deployFile + " is not there", nil
	case exists(a, deployAdministrators):
		return deployAdministrators + " is there and declares nothing that invites a first administrator", nil
	case path.Dir(plan.level) != path.Dir(baseDataLevel) || src.AuthDir != baseAuthDir:
		return fmt.Sprintf("the data level is in %s and the auths are under %s, and the base's deployment package imports them from %s and %s", path.Dir(plan.level), src.AuthDir, path.Dir(baseDataLevel), baseAuthDir), nil
	}
	rendering, err := baseRendering(deployFile, module, au.Name)
	if err != nil {
		return "", err
	}
	imports, err := importBlock(deployFile, rendering)
	if err != nil {
		return "", err
	}
	built := "package deploy\n\n" + imports
	for _, name := range administratorDeclarations {
		text, err := declarationText(deployFile, rendering, name)
		if err != nil {
			return "", err
		}
		built += "\n" + text
	}
	if !au.password() {
		if strings.Count(built, passwordChosen) != 1 {
			return "", errors.Newf("the base's %s does not say once how a first administrator signs in (%q)", deployFile, passwordChosen)
		}
		built = strings.Replace(built, passwordChosen, directoryProven, 1)
	}
	file, err := finishSource(deployAdministrators, rendering, []byte(built))
	if err != nil {
		return "", err
	}
	plan.file(deployAdministrators, file, 0o644)
	written := deployAdministrators
	if !exists(a, deployAdministratorsTest) {
		test, err := baseRendering(deployAdministratorsTest, module, au.Name)
		if err != nil {
			return "", err
		}
		plan.file(deployAdministratorsTest, test, 0o644)
		written += ", " + deployAdministratorsTest
	}
	doc, mode, err := readFile(a, deployFile)
	if err != nil {
		return "", err
	}
	if with, err := withPieces(doc, rendering, []piece{deployDocPiece}); err == nil {
		plan.file(deployFile, with, mode)
		written += ", " + deployFile
	} else {
		plan.left = append(plan.left, fmt.Sprintf("%s: its package comment was left as it is; say there that the deployment invites the %s auth's first administrator, where it does not", deployFile, au.Name))
	}
	plan.did = append(plan.did, fmt.Sprintf("%s: what invites a first administrator, which the application had none of, written for the %s auth as impulse new writes it: AdministratorAuths, InviteAdministrators and ReinviteAdministrator with what they stand on, the test of what they report where the package had none, and the package comment's sentence on the invitation", written, au.Name))

	return "", nil
}

// baseAuthDir is where the base keeps its auths' packages.
const baseAuthDir = "pkg/auth"

// administratorCommand plans the invitation in the migrate command. A command that
// already runs the deployment package's invitation runs it for every auth the package
// names, so nothing is written. One that runs none is the command of an application
// whose first auth's directory gives every role, and takes back what such a start took
// out of the base's command (migratePieces), rendered for the auth: the invitation after
// the role check and the -reinvite-administrator flag.
func (au Auth) administratorCommand(a *app.App, _ *authSource, plan *administratorPlan) (why string, err error) {
	if !exists(a, migrateCommand) {
		return migrateCommand + " is not there", nil
	}
	data, mode, err := readFile(a, migrateCommand)
	if err != nil {
		return "", err
	}
	if bytes.Contains(data, []byte(inviteCall)) {
		return "", nil
	}
	rendering, err := baseRendering(migrateCommand, modulePath(a), au.Name)
	if err != nil {
		return "", err
	}
	with, err := withPieces(data, rendering, migratePieces)
	if err == nil {
		with, err = format.Source(with)
	}
	if err != nil {
		return fmt.Sprintf("%s runs no invitation (%s...)) and is not as a start whose directory gives every role leaves it (%v)", migrateCommand, inviteCall, errors.Cause(err)), nil
	}
	plan.file(migrateCommand, with, mode)
	plan.did = append(plan.did, fmt.Sprintf("%s: the invitation of the %s auth's first administrator, which a release runs after its role check (deploy.InviteAdministrators), and the -reinvite-administrator flag, which mails a fresh invitation where the first was lost (deploy.ReinviteAdministrator)", migrateCommand, au.Name))

	return "", nil
}

// administratorEnv names the variable of the auth's first administrator in the
// environment template, unset, with what it is for. A template that names the variable
// already is left as it is.
func (au Auth) administratorEnv(a *app.App, src *authSource, ch *Change) error {
	variable := authpkg.AdministratorVariable(au.Name)
	if a.EnvTemplate == "" {
		ch.skipf("no environment template (.envrc.template, .env.template, .env.example) to name the %s auth's first administrator in (%s)", au.Name, variable)

		return nil
	}
	data, mode, err := readFile(a, a.EnvTemplate)
	if err != nil {
		return err
	}
	if bytes.Contains(data, []byte(variable+"=")) {
		return nil
	}
	block := fmt.Sprintf("# %[1]s is the email address of the %[2]s auth's first administrator\n# (%[3]s, InviteAdministrator): while nobody of the auth holds the Super Administrator\n# role the migrate command has an invitation mailed there, and the person signs in from\n# its link. A deployment names it per environment, with the mailbox (APP_MAIL_MAILBOX),\n# since the link is never written to a log; no release runs in development, so it stays\n# unset here.\n# export %[1]s=\n", variable, au.Name, path.Join(src.AuthDir, au.Name))
	text := strings.TrimRight(string(data), "\n") + "\n" + block
	if err := os.WriteFile(a.Abs(a.EnvTemplate), []byte(text), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: the %s auth's first administrator's variable (%s), unset: a deployment names the address per environment", a.EnvTemplate, au.Name, variable)

	return nil
}
