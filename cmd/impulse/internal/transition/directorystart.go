package transition

import (
	"context"
	"fmt"
	"go/format"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cast"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
)

// DirectoryStart makes a new application's first auth one with no password method: the
// auth impulse new rendered is written again with the methods of the shape the
// application starts from in the password's place. Two shapes have no password sign-in.
// In Directory roles one directory gives every role, so the auth registers that
// directory's method and nothing beside it. In Tenant-owned users, by directory, every
// person of a tenant signs in through the tenant's own directory, so the auth registers
// a Google method open to any Google Workspace and a Microsoft method open to any
// Microsoft Entra tenant, neither with personal accounts, and the persona method beside
// them, the sign-in of the test environments, which is how a development run signs its
// administrator in.
//
// It is a step of impulse new alone, applied before any other composed option: no
// command removes a sign-in method from an application that has one.
type DirectoryStart struct {
	// Auth is the application's first auth, as impulse new named it.
	Auth string
	// Methods are the methods the auth registers in the password's place, in order.
	Methods []authpkg.Method
	// Organization is the one organization of a method that admits one: a Google
	// Workspace by its domain, a Microsoft Entra tenant by its tenant id. It is written
	// as the development value of the method's organization setting; a deployment sets
	// its own per environment. Empty keeps the placeholder.
	Organization string
	// ShapeKey and ShapeName are the shape the application starts from, as --shape takes
	// it and by number and name.
	ShapeKey  string
	ShapeName string
	// Tenants is the tenancy impulse new composes after this step, where the application
	// starts with tenants: the application's tests are written for it, since the step
	// that gives the application its tenants writes no test. Nil where the application
	// starts with none, or where the step is applied alone.
	Tenants *Tenancy
}

// DevelopmentPersona is the login of the persona a development run signs its
// administrator in as, where the first auth registers the persona method.
const DevelopmentPersona = "admin"

// Command is the impulse command line the step is part of.
func (d *DirectoryStart) Command() string {
	cmd := "impulse new --shape " + d.ShapeKey
	if d.syncsRoles() {
		m := d.methods()[0]
		cmd += " --method " + m.Kind + ":" + m.Name
		if d.Organization != "" {
			cmd += " --organization " + d.Organization
		}
	}

	return cmd
}

// methods are the step's methods with each registration named: a Google or a Microsoft
// method is registered under its kind where no name was given, as --method google
// names it, and the persona method under its fixed name.
func (d *DirectoryStart) methods() []authpkg.Method {
	methods := append([]authpkg.Method(nil), d.Methods...)
	for i := range methods {
		m := &methods[i]
		m.SettleName()
		if m.Name == "" && (m.Kind == app.MethodGoogle || m.Kind == app.MethodAzure) {
			m.Name = m.Kind
		}
	}

	return methods
}

// syncsRoles reports the start whose one directory gives the roles.
func (d *DirectoryStart) syncsRoles() bool {
	return len(d.Methods) == 1 && d.Methods[0].SyncsRoles()
}

// persona reports whether the auth registers the persona method.
func (d *DirectoryStart) persona() bool {
	for i := range d.Methods {
		if d.Methods[i].Persona() {
			return true
		}
	}

	return false
}

// Validate checks the step against the application before anything is changed: what the
// author chose is well formed (ValidateChoices), and the auth is the base's, registering
// the password method and nothing else.
func (d *DirectoryStart) Validate(a *app.App) error {
	if err := d.ValidateChoices(); err != nil {
		return err
	}
	_, auth, err := (&Method{Auth: d.Auth}).target(a)
	if err != nil {
		return err
	}
	if len(auth.Methods) != 1 || auth.Methods[0].Kind != app.MethodPassword {
		registered := make([]string, 0, len(auth.Methods))
		for i := range auth.Methods {
			registered = append(registered, auth.Methods[i].Name)
		}

		return errors.Newf("%s: the %s auth registers %s, and an application starts from %s only when it is new: its first auth is then the base's, with the password method alone, which this step writes again with the shape's methods. No command removes a sign-in method from an application that has one", auth.File, d.Auth, strings.Join(registered, ", "), d.ShapeName)
	}

	return nil
}

// ValidateChoices checks what the author chose, which needs no application to judge, so
// impulse new refuses it before it renders anything: the methods that take the
// password's place are well formed and none of them a password, and the one organization
// of a start whose directory gives the roles is what its provider knows one by.
func (d *DirectoryStart) ValidateChoices() error {
	if len(d.Methods) == 0 {
		return errors.Newf("%s names no sign-in method to write in the password's place", d.ShapeName)
	}
	for _, m := range d.methods() {
		if m.Kind == app.MethodPassword {
			return errors.Newf("%s has no password sign-in, and the %s auth was given the password method", d.ShapeName, d.Auth)
		}
		if err := m.Validate(); err != nil {
			return errors.Wrapf(err, "the %s auth's %s method", d.Auth, m.Name)
		}
	}
	if d.syncsRoles() {
		return validOrganization(&d.Methods[0], d.Organization)
	}

	return nil
}

// googleDomainRE and tenantIDRE are the shapes of the one organization a method admits:
// a Google Workspace's domain and a Microsoft Entra tenant's id.
var (
	googleDomainRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	tenantIDRE     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// validOrganization refuses an organization that is not what the method's provider
// knows one by. Empty is no organization named, which keeps the placeholder.
func validOrganization(m *authpkg.Method, organization string) error {
	switch {
	case organization == "":
		return nil
	case m.Kind == app.MethodGoogle && !googleDomainRE.MatchString(organization):
		return errors.Newf("--organization %s: a Google method's one organization is a Google Workspace, named by its domain in lowercase (fleet.example)", organization)
	case m.Kind == app.MethodAzure && !tenantIDRE.MatchString(organization):
		return errors.Newf("--organization %s: a Microsoft method's one organization is a Microsoft Entra tenant, named by its tenant id (a GUID, as the Entra admin center shows it)", organization)
	}

	return nil
}

// Apply makes the deterministic half: the auth package written again with the methods,
// the generator program's declarations, the data level's settings, the development
// environment and its logins, the regeneration, and the role file brought down to what
// the auth's generated management still serves.
func (d *DirectoryStart) Apply(ctx context.Context, a *app.App, exec check.Execer) (*Change, error) {
	if err := d.Validate(a); err != nil {
		return nil, err
	}
	step := &Method{Auth: d.Auth}
	pkg, auth, err := step.target(a)
	if err != nil {
		return nil, err
	}
	ch := &Change{Command: d.Command()}
	spec := d.spec(a, pkg)
	if err := d.writePackage(a, pkg, auth, spec, ch); err != nil {
		return nil, err
	}
	if err := d.declare(a, pkg, spec, ch); err != nil {
		return nil, err
	}
	for i := range spec.Methods {
		m := &spec.Methods[i]
		if err := step.editConfig(a, m, ch); err != nil {
			return nil, err
		}
	}
	if err := tagProcfile(a, d.Auth, ch); err != nil {
		return nil, err
	}
	for i := range spec.Methods {
		m := &spec.Methods[i]
		if err := writeMethodEnv(a, d.Auth, m, step.callback(a, pkg, m), ch); err != nil {
			return nil, err
		}
	}
	if err := d.developmentLogins(a, pkg, spec, ch); err != nil {
		return nil, err
	}
	if !authpkg.Foothold(spec) {
		if err := d.inviteNobody(a, ch); err != nil {
			return nil, err
		}
	}
	handlers := path.Join(spec.IdentityDir, d.Auth+"handlers")
	before := generatedHandlers(a, handlers)
	if err := generate(ctx, a, exec, ch, "go generate ./... failed, so the router and the auth's management still serve the password method; fix the cause and run it:", "ran go generate ./..., so the router mounts the methods' routes in the password's place and the auth's generated management serves nothing of a password"); err != nil {
		return nil, err
	}
	if err := d.pruneRoles(a, pkg, before, generatedHandlers(a, handlers), ch); err != nil {
		return nil, err
	}
	settled, err := d.writeSuites(a, ch)
	if err != nil {
		return nil, err
	}
	if err := d.editMenuSpec(a, ch); err != nil {
		return nil, err
	}
	if !settled[integrationHarness] || !settled[migrateTest] {
		// The tests were not written for the methods, so what each registration
		// validates is still the author's to give them.
		for i := range spec.Methods {
			slotHandoff(ch, d.Auth, &spec.Methods[i])
		}
	}
	d.noteBaseSuites(a, ch, settled)
	// Each method's settings are passed by the same step, which says the same of the
	// login page every time: once is enough.
	ch.Skipped = distinct(ch.Skipped)

	return ch, nil
}

// spec is the first auth as impulse new renders it, with the step's methods and what
// the package comment says of its people in the password's place.
func (d *DirectoryStart) spec(a *app.App, pkg *app.AuthPackage) *authpkg.Spec {
	spec := authpkg.FirstAuth(modulePath(a), d.Auth)
	spec.AuthDir = path.Dir(pkg.Dir)
	spec.Application = applicationName(a, &authSource{Name: d.Auth, Dir: pkg.Dir})
	spec.Methods = d.methods()
	var ways []string
	for i := range spec.Methods {
		m := &spec.Methods[i]
		if m.Persona() {
			// The persona method is the test environments', and no way the auth's
			// people sign in.
			continue
		}
		ways = append(ways, signInHow(m))
	}
	spec.Doc = "the people who sign in to the console " + strings.Join(ways, " or ")

	return &spec
}

// writePackage writes the auth package again from its description: the Auth file, the
// foothold where the application gives the roles, one file for each method, and the
// cast of the persona method. The password method's file goes, and the foothold with it
// where a directory gives the roles.
func (d *DirectoryStart) writePackage(a *app.App, pkg *app.AuthPackage, auth *app.Auth, spec *authpkg.Spec, ch *Change) error {
	files, err := authpkg.Files(spec)
	if err != nil {
		return err
	}
	_, mode, err := readFile(a, auth.File)
	if err != nil {
		return err
	}
	for _, name := range authpkg.FileNames(spec) {
		if err := os.WriteFile(a.Abs(path.Join(pkg.Dir, name)), files[name], mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
	}
	removed := []string{app.PasswordRegistration + ".go"}
	if !authpkg.Foothold(spec) {
		removed = append(removed, "foothold.go")
	}
	for _, name := range removed {
		if err := os.Remove(a.Abs(path.Join(pkg.Dir, name))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Wrap(err, "os.Remove()")
		}
	}
	var described []string
	for i := range spec.Methods {
		m := &spec.Methods[i]
		described = append(described, fmt.Sprintf("%s (%s, %s, role membership the %s's)", m.Name, m.Kind, slotSentence(m), m.Authority))
	}
	ch.didf("%s: the %s auth written again with no password method, as %s starts: %s, each in its own file, registered in New (%s); %s removed", pkg.Dir, d.Auth, d.ShapeName, strings.Join(described, ", "), strings.Join(authpkg.FileNames(spec), ", "), strings.Join(removed, " and "))
	if !d.persona() {
		return nil
	}
	rel := path.Join(pkg.Dir, cast.CastFile)
	if err := writeNew(a, rel, developmentCast(spec)); err != nil {
		return err
	}
	ch.didf("%s: the cast of the persona sign-in, with one persona, %s, who holds the Super Administrator role and the first administrator's roles: the persona a development run signs its administrator in as, and the one a test environment seeds, so such an environment invites no first administrator and needs no mailbox", rel, DevelopmentPersona)

	return nil
}

// developmentCast is the cast a new application's persona method starts with: one
// administrator, holding the framework's Super Administrator role and the roles the
// auth's first administrator is given.
func developmentCast(spec *authpkg.Spec) string {
	roles := append([]string{"SuperAdministrator"}, spec.FirstAdministratorRoles.Global...)
	quoted := make([]string, 0, len(roles))
	for _, role := range roles {
		quoted = append(quoted, fmt.Sprintf("%q", role))
	}

	return fmt.Sprintf("{\n  \"personas\": [\n    {\"login\": %q, \"name\": \"Development Administrator\", \"email\": \"%s@example.com\", \"roles\": [%s]}\n  ]\n}\n", DevelopmentPersona, DevelopmentPersona, strings.Join(quoted, ", "))
}

// passwordDeclarationRE matches the password method's declaration on an Auth option of
// a generator program, with whatever it chains, and the comma that follows it.
var passwordDeclarationRE = regexp.MustCompile(`generation\.PasswordMethod\(\)(?:\.\w+\([^()]*\))*,\s*`)

// declare puts the methods' declarations in the password's place on every outlet bound
// to the auth in the generator programs, so the generated router mounts their routes
// and the auth's generated management serves nothing of a password.
func (d *DirectoryStart) declare(a *app.App, pkg *app.AuthPackage, spec *authpkg.Spec, ch *Change) error {
	var files []string
	declared := make([]string, 0, len(spec.Methods))
	for i := range spec.Methods {
		declared = append(declared, spec.Methods[i].Declaration())
	}
	for _, g := range a.SiteGenerators() {
		src, mode, err := readFile(a, g.File)
		if err != nil {
			return err
		}
		out, edited := src, 0
		for i := range spec.Methods {
			m := &spec.Methods[i]
			var n int
			if out, n, err = app.AddAuthMethod(g.File, out, pkg.Path, m.Name, m.Declaration()); err != nil {
				return err
			}
			edited += n
		}
		if edited == 0 {
			continue
		}
		out = passwordDeclarationRE.ReplaceAll(out, nil)
		if out, err = format.Source(out); err != nil {
			return errors.Wrapf(err, "format.Source(): %s without the password method's declaration", g.File)
		}
		if err := os.WriteFile(a.Abs(g.File), out, mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		files = append(files, g.File)
	}
	if len(files) == 0 {
		ch.skipf("no outlet declares Auth(%q, ...), so the methods' routes are mounted nowhere; declare %s on the Auth option of the outlet that serves the %s auth, in the password method's place", pkg.Path, strings.Join(declared, ", "), d.Auth)

		return nil
	}
	ch.didf("%s: the %s auth's outlets declare %s in the place of generation.PasswordMethod(), so the generated router mounts each method's login and callback and the methods route offers them", strings.Join(files, ", "), d.Auth, strings.Join(declared, ", "))

	return nil
}

// bootstrapPasswordRE matches what the base's bootstrap carries of a development
// login's password: the field of its identities file, and the request's.
var (
	bootstrapFieldRE   = regexp.MustCompile("(?m)^\\s*Password\\s+string\\s+`json:\"password\"`\\n")
	bootstrapRequestRE = regexp.MustCompile(`(?m)^\s*Password:\s*&user\.Password,\n`)
	bootstrapSeedRE    = regexp.MustCompile(`(?m)^(\tif err := seedIdentities\(ctx, data\); err != nil \{\n\t\treturn errors\.Wrap\(err, "seedIdentities\(\)"\)\n\t\}\n)`)
	usersPasswordRE    = regexp.MustCompile(`(?m)^\s*"password":\s*"[^"]*",?\n`)
)

// bootstrapUsers is the development identities file of the base, by its path, and
// bootstrapMain the program that reads it.
const (
	bootstrapUsers = "cmd/bootstrap/users.json"
	bootstrapMain  = "cmd/bootstrap/main.go"
)

// developmentLogins brings the development logins to an auth with no password: the
// bootstrap's identities file seeds no account (an account made ahead with a password
// is what it held), the bootstrap gives no account a password, and the development
// people sign in through what the auth registers. Where the auth registers the persona
// method the bootstrap seeds the cast, and the development administrator is its
// persona; where a directory gives the roles nobody is seeded, and the simulated
// directory asserts the roles the environment template names.
func (d *DirectoryStart) developmentLogins(a *app.App, pkg *app.AuthPackage, spec *authpkg.Spec, ch *Change) error {
	if err := d.emptyIdentities(a, ch); err != nil {
		return err
	}
	if err := d.editBootstrap(a, ch); err != nil {
		return err
	}
	if a.EnvTemplate == "" {
		return nil
	}
	data, mode, err := readFile(a, a.EnvTemplate)
	if err != nil {
		return err
	}
	text := string(data)
	switch {
	case d.persona():
		variable := "APP_" + strings.ToUpper(authpkg.PersonaRegistration) + "_USERNAME"
		text = strings.TrimRight(text, "\n") + fmt.Sprintf("\n\n# The development administrator is the persona %[1]s (%[2]s/%[3]s), whose account the\n# bootstrap seeds with the cast: press the persona button on the login page. The persona\n# method reads its own variable, so the directory methods keep APP_USERNAME for the\n# person a tenant's directory signs in.\nexport %[4]s=%[1]s\n", DevelopmentPersona, pkg.Dir, cast.CastFile, variable)
		ch.didf("%s: %s=%s, so a development run signs its administrator in as the cast's persona", a.EnvTemplate, variable, DevelopmentPersona)
	case d.syncsRoles():
		roles := strings.Join(append([]string{"SuperAdministrator"}, spec.FirstAdministratorRoles.Global...), ",")
		if !strings.Contains(text, "export APP_ROLES=\n") {
			ch.skipf("%s: no empty APP_ROLES to fill in; set APP_ROLES to the roles the simulated directory asserts for the development person (%s), since nothing in the application gives a role", a.EnvTemplate, roles)

			break
		}
		text = strings.Replace(text, "export APP_ROLES=\n", "export APP_ROLES="+roles+"\n", 1)
		ch.didf("%s: APP_ROLES=%s, the roles the simulated directory asserts for the development person (APP_USERNAME): the directory gives every role, so no account is seeded and nothing in the application gives one", a.EnvTemplate, roles)
	}
	if d.syncsRoles() && d.Organization != "" {
		text = d.nameOrganization(a, text, &spec.Methods[0], ch)
	}
	if err := os.WriteFile(a.Abs(a.EnvTemplate), []byte(text), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}

	return nil
}

// nameOrganization writes the one organization the author named as the development
// value of the method's organization setting, in the placeholder's place.
func (d *DirectoryStart) nameOrganization(a *app.App, text string, m *authpkg.Method, ch *Change) string {
	fields := m.Fields()
	for i := range fields {
		f := &fields[i]
		if f.Name != authpkg.FieldHostedDomain && f.Name != authpkg.FieldTenantID {
			continue
		}
		variable := m.Variable(d.Auth, f)
		placeholder := "export " + variable + "=" + devValue(m, f, d.Auth) + "\n"
		if !strings.Contains(text, placeholder) {
			break
		}
		ch.didf("%s: %s=%s, the one organization whose directory gives the roles; a deployment names its own per environment", a.EnvTemplate, variable, d.Organization)

		return strings.Replace(text, placeholder, "export "+variable+"="+d.Organization+"\n", 1)
	}
	ch.skipf("%s: the %s method's organization setting was not found as the tool writes it, so %s is not named there; set it by hand", a.EnvTemplate, m.Name, d.Organization)

	return text
}

// emptyIdentities leaves the bootstrap's identities file with no account: the base's
// one development login is an account made ahead with a password.
func (d *DirectoryStart) emptyIdentities(a *app.App, ch *Change) error {
	data, mode, err := readFile(a, bootstrapUsers)
	if err != nil {
		ch.skipf("%s: not read (%v); seed no account with a password, since the %s auth has no password method", bootstrapUsers, errors.Cause(err), d.Auth)

		return nil
	}
	if !usersPasswordRE.Match(data) {
		ch.skipf("%s: no development login with a password as the base writes it; seed no account with a password, since the %s auth has no password method", bootstrapUsers, d.Auth)

		return nil
	}
	if err := os.WriteFile(a.Abs(bootstrapUsers), []byte("{\n  \"users\": []\n}\n"), mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: no account is seeded: the base's development login was an account made ahead with a password, and the %s auth has no password method", bootstrapUsers, d.Auth)

	return nil
}

// editBootstrap takes the password out of the bootstrap program, and has it seed the
// cast where the auth registers the persona method.
func (d *DirectoryStart) editBootstrap(a *app.App, ch *Change) error {
	data, mode, err := readFile(a, bootstrapMain)
	if err != nil {
		ch.skipf("%s: not read (%v); give no development account a password%s", bootstrapMain, errors.Cause(err), d.seedCastByHand())

		return nil
	}
	if !bootstrapFieldRE.Match(data) || !bootstrapRequestRE.Match(data) {
		ch.skipf("%s: does not create its development logins as the base's does (a password field read from %s and handed to CreateSessionUser); give no development account a password%s", bootstrapMain, bootstrapUsers, d.seedCastByHand())

		return nil
	}
	out := bootstrapRequestRE.ReplaceAll(bootstrapFieldRE.ReplaceAll(data, nil), nil)
	did := fmt.Sprintf("%s: a development login carries no password (the identities file's field and the request's are gone), so an account the file names is made with its username alone and signs in through a method of the auth", bootstrapMain)
	if d.persona() {
		if !bootstrapSeedRE.Match(out) {
			ch.skipf("%s: the call of seedIdentities is not as the base writes it, so the cast is not seeded; call data.%s().SeedCast(ctx, os.Stdout) after it", bootstrapMain, names.Pascal(d.Auth))
		} else {
			seed := fmt.Sprintf("${1}\n\t// The cast of the persona sign-in: a development run signs its administrator in as\n\t// the cast's persona, whose account this seeds.\n\tif _, err := data.%[1]s().SeedCast(ctx, os.Stdout); err != nil {\n\t\treturn errors.Wrap(err, \"%[2]s.Auth.SeedCast()\")\n\t}\n", names.Pascal(d.Auth), d.Auth)
			out = bootstrapSeedRE.ReplaceAll(out, []byte(seed))
			did += fmt.Sprintf(", and the bootstrap seeds the cast (data.%s().SeedCast), so the development administrator's account is there for the persona sign-in to link", names.Pascal(d.Auth))
		}
	}
	if out, err = format.Source(out); err != nil {
		return errors.Wrapf(err, "format.Source(): %s", bootstrapMain)
	}
	if err := os.WriteFile(a.Abs(bootstrapMain), out, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s", did)

	return nil
}

// seedCastByHand names the cast's seeding for a bootstrap the step could not edit.
func (d *DirectoryStart) seedCastByHand() string {
	if !d.persona() {
		return ""
	}

	return fmt.Sprintf(", and seed the cast after the development logins (data.%s().SeedCast(ctx, os.Stdout)), so the development administrator's account is there for the persona sign-in to link", names.Pascal(d.Auth))
}

// generatedHandlers lists the generated handler files of an auth's management, by name,
// or nothing where the directory is not there.
func generatedHandlers(a *app.App, dir string) map[string]bool {
	entries, err := os.ReadDir(a.Abs(dir))
	if err != nil {
		return nil
	}
	files := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "zz_gen_") {
			files[entry.Name()] = true
		}
	}

	return files
}

// grantRE matches one grant of a role file as the tool writes them: an object naming a
// resource, with its fields and condition where it has them, and the comma after it.
var grantRE = regexp.MustCompile(`(?s)\s*\{\s*"resource":\s*"(\w+)"[^{}]*\},?`)

// trailingCommaRE matches the comma a removed last grant leaves before its list's end.
var trailingCommaRE = regexp.MustCompile(`,(\s*\])`)

// pruneRoles takes out of the auth's role file every grant on a resource or a method
// the auth's generated management served before the regeneration and serves no more:
// what set, removed or reset a password, and, where a directory gives the roles, what
// gave a role or invited a person. The permission engine refuses a file that grants a
// resource the application does not serve, so the application would not start with
// them. Every other grant is left as it is.
func (d *DirectoryStart) pruneRoles(a *app.App, pkg *app.AuthPackage, before, after map[string]bool, ch *Change) error {
	gone := removedHandlers(before, after)
	rel := path.Join(pkg.Dir, app.RolesFileName)
	if len(gone) == 0 {
		ch.skipf("%s: the regeneration removed no handler of the %s auth's management, so no grant was taken out of the role file; a grant on a resource the application no longer serves stops it at start (impulse check, auths-wired)", rel, d.Auth)

		return nil
	}
	pruned, err := pruneGrants(a, rel, gone, func(file []byte) []byte {
		return []byte(strings.Replace(string(file), ", keeps groups of people, and sets temporary passwords.", ", and keeps groups of people.", 1))
	})
	if err != nil || len(pruned) == 0 {
		return err
	}
	ch.didf("%s: %d grant(s) taken out, on what the %s auth's generated management no longer serves (%s): the permission engine refuses a role file that grants a resource the application does not serve", rel, len(pruned), d.Auth, strings.Join(pruned, ", "))

	return nil
}

// removedHandlers are the generated handler files a regeneration removed: the ones
// listed before it and not after.
func removedHandlers(before, after map[string]bool) map[string]bool {
	gone := map[string]bool{}
	for name := range before {
		if !after[name] {
			gone[name] = true
		}
	}

	return gone
}

// pruneGrants takes out of a role file every grant on a resource or a method whose
// generated handler is gone, named by the handler's file, and returns the resources
// whose grants were taken out, each once and in order. retell, where one is given,
// brings the file's own words up to what is left. A file that carries no such grant is
// left as it is.
func pruneGrants(a *app.App, rel string, gone map[string]bool, retell func(file []byte) []byte) ([]string, error) {
	data, mode, err := readFile(a, rel)
	if err != nil {
		return nil, err
	}
	var pruned []string
	out := grantRE.ReplaceAllFunc(data, func(grant []byte) []byte {
		resource := string(grantRE.FindSubmatch(grant)[1])
		if !gone["zz_gen_"+strcase.ToSnake(resource)+".go"] {
			return grant
		}
		pruned = append(pruned, resource)

		return nil
	})
	if len(pruned) == 0 {
		return nil, nil
	}
	out = trailingCommaRE.ReplaceAll(out, []byte("$1"))
	if retell != nil {
		out = retell(out)
	}
	if err := os.WriteFile(a.Abs(rel), out, mode); err != nil {
		return nil, errors.Wrap(err, "os.WriteFile()")
	}
	sort.Strings(pruned)

	return distinct(pruned), nil
}

// distinct drops the repeats of a list, keeping each line where it first stood.
func distinct(lines []string) []string {
	seen := make(map[string]bool, len(lines))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}

	return out
}

// passwordSuites are the tests a new application starts with that prove its password
// sign-in, sign in with a password to prove something else, or name what an auth whose
// directory gives the roles does not have, each with what becomes of it in an
// application whose first auth has no password method. They are written against the
// base's harness and its first auth, so they stop compiling or stop holding with this
// change. The start writes them for the sign-in the auth has (writeSuites), the spec of
// the browser application's login page among them; the change lists the ones it did not
// settle, and says what to do with each before the first compile error is met.
// fate holds for every such start; directory is added where a directory gives the roles
// and application where the application does, and a test with nothing to say of a start
// is left out of it. {auth} stands for the first auth's name.
var passwordSuites = []struct{ file, fate, directory, application string }{
	{file: integrationHarness, fate: "signs a person in by posting a username and a password, which no route takes now; sign in through a method of the auth under -tags skipAuth, as the reference's test/integration/portal_login_skipauth_test.go does (APP_<REGISTRATION>_USERNAME, then user/<registration>/login to the callback), and move the suites that sign in to files built under that tag"},
	{file: "test/authz/harness_test.go", fate: "signs each generated authorization case in with a password; sign in the same way the integration harness now does"},
	{file: "test/integration/login_test.go", fate: "proves the password login and its refusals; remove it, and prove the sign-in through each method the auth registers"},
	{file: passwordResetSuite, fate: "proves the reset of a forgotten password; remove it, since no account has a password to reset"},
	{file: "test/integration/second_factor_test.go", fate: "proves the sign-in code after a password; a sign-in through a directory the deployment does not administer is asked for the same code, so prove it there or remove the file"},
	{file: signUpSuite, fate: "proves sign-up with a password; remove it"},
	{file: signUpRoleSuite, fate: "proves the role a person who signs up with a password is given; remove it"},
	{
		file: firstAdministratorSuite, fate: "has the first administrator choose a password from the invitation's link",
		directory:   "nobody is invited to this auth, whose directory gives every role, so remove it",
		application: "the invitation is accepted through a method of the auth instead (the link, then user/<registration>/login), so prove that",
	},
	{file: "test/integration/groups_test.go", fate: "signs in with a password through the harness; it follows the harness"},
	{
		file: migrateTest, fate: "constructs the auth with the base's settings; give the construction each registration's settings, as the change's lines above say",
		directory: "it also names the first administrator's address by {auth}.AdministratorVariable, which the auth no longer has, so it does not compile: take out the cases that invite or reinvite a first administrator, the refusals of -reinvite-administrator among them, since the command has no such flag now",
	},
	{
		file:      deployTest,
		directory: "holds the first administrator's roles to the role file ({auth}.FirstAdministratorRoles, {auth}.AdministratorRoles), which an auth whose directory gives the roles does not have, so the package's tests do not compile: take TestFirstAdministratorRoles_areInTheRoleFile and TestAdministratorRoles_areHeldByTheFirstAdministrator out, and keep the test that validates the role file",
	},
	{file: loginPageSpec, fate: "answers the methods route with the password method and expects its form; answer it with the methods the auth registers and expect their buttons and no password form"},
}

// noteBaseSuites records, for each of the base's tests the application still carries
// that this start breaks and did not settle, what becomes of it. They are obligations
// the check cannot see whole: a test that signs in with a password still compiles where
// it posts one, and fails when it runs, which the check does not do. So the change lists
// them as left to the author, and is handed on even when the check is clean.
func (d *DirectoryStart) noteBaseSuites(a *app.App, ch *Change, settled map[string]bool) {
	for _, suite := range passwordSuites {
		if settled[suite.file] {
			continue
		}
		if _, err := os.Stat(a.Abs(suite.file)); err != nil {
			continue
		}
		fates := []string{suite.fate, suite.application}
		if d.syncsRoles() {
			fates = []string{suite.fate, suite.directory}
		}
		fates = slices.DeleteFunc(fates, func(fate string) bool {
			return fate == ""
		})
		if len(fates) == 0 {
			continue
		}
		ch.Left = append(ch.Left, suite.file+" "+strings.ReplaceAll(strings.Join(fates, "; "), "{auth}", d.Auth))
	}
	switch {
	case len(ch.Left) == 0:
	case len(settled) > 0:
		ch.LeftNote = fmt.Sprintf("The application's tests were written for the sign-in the %s auth has, which has no password. What the start did not bring to it is yours:", d.Auth)
	default:
		ch.LeftNote = fmt.Sprintf("The tests the application started with were written for a first auth that signs in with a password, and the %s auth has none now. Each of these is yours to bring to the sign-in the auth has, the harness first, since the others sign in through it:", d.Auth)
	}
}

// Meaning explains the start and names the wiring left to do, as it is for a new
// application, whose tests the start writes for the sign-in the auth has.
func (d *DirectoryStart) Meaning() string {
	set, _, _ := d.suiteSet()

	return d.meaning(set)
}

// MeaningIn is Meaning for the application the start was applied to: it says the tests
// were written where they were, which the sign-in through the simulated directory among
// the application's tests shows, and hands them on where they were not.
func (d *DirectoryStart) MeaningIn(a *app.App) string {
	set, _, _ := d.suiteSet()
	if _, err := os.Stat(a.Abs(simulatedSignIn)); err != nil {
		set = ""
	}

	return d.meaning(set)
}

// mailboxItem is what a Directory roles start leaves the author to do about the mailbox.
// A release of such an application asks for none, since it invites nobody, and the
// application mails all the same: the sign-in code is the second factor of every sign-in
// the directory does not answer for, and it goes to the address on the person's account.
// A directory that vouches for no address (Microsoft Entra ID) leaves the person to give
// theirs at the first sign-in and to prove it with that code, whatever the directory
// says of a second factor. Without a mailbox the code is written to the server's log, so
// the item names the settings that give an environment one.
func (d *DirectoryStart) mailboxItem() string {
	m := d.methods()[0]
	organization, unless := "Workspace", "or"
	first := ""
	if m.Kind == app.MethodAzure {
		organization, unless = "tenant", "the directory's token says a second factor was passed, or"
		first = " Each person's first sign-in waits for one whatever the directory says of a second factor: Microsoft Entra ID vouches for no address, so the login page asks the person for theirs, and the code proves it."
	}
	administered := ""
	fields := m.Fields()
	for i := range fields {
		if f := &fields[i]; f.Name == authpkg.FieldAdministeredTenants || f.Name == authpkg.FieldAdministeredWorkspaces {
			administered = fmt.Sprintf(" (`%s`)", m.Variable(d.Auth, f))
		}
	}

	return fmt.Sprintf("Give each deployed environment its mailbox (`APP_MAIL_MAILBOX`, with the secret `APP_MAIL_APP_PASSWORD`) before anybody signs in. No release asks for one, since nobody is invited, and the application mails all the same: a sign-in waits for a code mailed to the person's address unless the %s is one the deployment administers%s, %s the browser is one the person asked to be remembered.%s Without the mailbox the code is written to the server's log and reaches nobody, and the person is left at the login page's code step.", organization, administered, unless, first)
}

// meaning is the start's account, given the tests it wrote (a tree of suiteFiles, or
// none).
func (d *DirectoryStart) meaning(set string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The application starts from %s, a shape with no password sign-in, so its first auth, %s, was written again with no password method: no password form on the login page, no forgotten-password link, no password section on a person's account page, and nothing in the generated management that sets, resets or removes a password. ", d.ShapeName, d.Auth)
	if d.syncsRoles() {
		m := d.methods()[0]
		fmt.Fprintf(&b, "It registers one method, %s, through %s, and that directory gives every role, the Super Administrator's included: every sign-in reconciles the person's roles to what the directory asserts, everyone the directory signs in with a role the role file defines becomes a user, nobody is invited, and nothing in the application gives or takes a role. The method stands alone in its auth: a second method would be a second authority over the same people, and impulse check refuses it. ", m.Name, slotSentence(&m))
		b.WriteString("The deployment invites no first administrator for it: nothing in the deployment package, the migrate command or the data level speaks of one, and `impulse add auth` writes the invitation for an auth added later that invites one. A development run signs in the person APP_USERNAME names with the roles APP_ROLES lists, which stand in for the directory's under the session library's skipAuth tag.")
	} else {
		var named []string
		for _, m := range d.methods() {
			if m.Persona() {
				continue
			}
			named = append(named, fmt.Sprintf("%s (%s)", m.Name, slotSentence(&m)))
		}
		fmt.Fprintf(&b, "It registers %s, each open to any organization and to no personal account, with the application giving the roles: a tenant is the directory of whichever organization its founder signs in from, at Google or at Microsoft, and the application's own people come by invitation and accept it through either. Before the application deploys it is registered with both providers, since each method needs its own client id and secret. ", strings.Join(named, " and "))
		if d.persona() {
			fmt.Fprintf(&b, "Beside them it registers the persona method, the sign-in of the test environments, which is part of no shape and is registered only where the persona provider is configured: its cast (`pkg/auth/%[1]s/cast.json`) holds one persona, `%[2]s`, with the Super Administrator role and the first administrator's roles. A development run seeds the cast in the bootstrap and signs its administrator in as that persona (`APP_PERSONA_USERNAME`), since an account made ahead with no password is linked to a directory's identity only through the directory of the one tenant it belongs to or by its own invitation; a deployed environment whose placement names the persona provider seeds the same cast and invites no first administrator, and any other invites one, who accepts through Google or Microsoft.", d.Auth, DevelopmentPersona)
		}
	}
	b.WriteString("\n\nLeft to wire:\n\n")
	tests := "Bring the tests to the sign-in the auth has. The change names each of the base's tests that signs in with a password or names what the auth no longer has, with what becomes of it; the harness comes first, since the others sign in through it."
	if set != "" {
		tests = "Read the tests the start wrote. The application's Go tests sign in through the simulated directory, so run them as the Procfile and CI do, with `go test -tags skipAuth ./...`: under the default build the cases that sign in are skipped. The spec of the browser application's login page (`" + loginPageSpec + "`) is the start's too: it answers the methods route with the methods the auth registers, and expects a button for each and no password form. The change names the tests of the base it took out, each with the reason, and lists any it left as it found it."
	}
	readme := "the development login, the first administrator's password"
	if d.syncsRoles() {
		readme = "the development login, the first administrator's password, and the first administrator a release invites, with the migrate command's `-reinvite-administrator`, which the start took out of the application"
	}
	items := []string{
		tests,
		fmt.Sprintf("Read the role file (`pkg/auth/%s/roles.json`). The grants on what the generated management no longer serves were taken out; read what the roles' descriptions say of passwords, and what the remaining grants give.", d.Auth),
		"Bring the README's account of signing in (" + readme + ") to what the application does now, and the comments that still speak of a password: the generator program's (`cmd/generate/resourcegenerator/generator.go`), the bootstrap's (`cmd/bootstrap/main.go`) and `app/users.go`'s.",
	}
	if d.syncsRoles() {
		items = append(items,
			"Define the roles in the role file and assign them in the directory: nothing is seeded for people who sign in this way, and the development person holds what `APP_ROLES` lists.",
			d.mailboxItem(),
		)
	} else if d.persona() {
		items = append(items,
			fmt.Sprintf("Seed the cast where a deployment takes the seed: in `cmd/deployment/migrate`, under `-seed`, once the data level is open and before `InviteAdministrators`, call `data.%s().SeedCast(ctx, out)`. `impulse check` refuses a cast seeded after the invitation (`first-administrator`).", names.Pascal(d.Auth)),
			"Name the persona provider in `placement.json` (a `persona` block with `issuer` and `clientId`) for the environments that sign in as a persona, or leave it out: the method is then registered nowhere but a development run.",
		)
	}
	for i, item := range items {
		fmt.Fprintf(&b, "%d. %s\n", i+1, item)
	}
	b.WriteString("\nData consequence: none; the application is new.\n")

	return b.String()
}
