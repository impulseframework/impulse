package transition

import (
	"bytes"
	"embed"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// suiteFiles are the tests the command writes in the place of the base's, in trees: each
// file is the base's test of the same name, proving what the base's proves in the
// application the command made of it. A start with no password sign-in signs in through
// the simulated directory where the base's posts a password, and an application with
// tenants builds what its tenants need. They are written as the base candidate's files
// are, against its module path and its placeholder auth, and rendered the same way. What
// a file takes from the application is written between double square brackets, since a
// Go file has braces of its own in pairs.
//
//go:embed all:suites
var suiteFiles embed.FS

// The sets of tests the command writes: for the start whose one directory gives the
// roles, for the start whose people sign in through their tenant's own directory, and
// for an application with a password sign-in that gains tenants.
const (
	directoryRolesSuites  = "directoryroles"
	byDirectorySuites     = "bydirectory"
	passwordTenantsSuites = "tenants"
)

// tenantedSuites is the tree of what every application with tenants takes, whatever its
// sign-in: the authorization suite's harness and the test that holds each role file's
// grants to the engine, neither of which signs anybody in.
const tenantedSuites = "tenanted"

// tenantPeopleSuites is the set, and the tree, of the one test that proves the conditions
// on the grants of the role a tenant's administrator holds. It is written with the role,
// after the regeneration, and never with a start's other tests.
const tenantPeopleSuites = "tenantpeople"

// suiteTrees are the trees of suiteFiles a set is rendered from.
var suiteTrees = map[string][]string{
	directoryRolesSuites:  {directoryRolesSuites},
	byDirectorySuites:     {tenantedSuites, byDirectorySuites},
	passwordTenantsSuites: {tenantedSuites, passwordTenantsSuites},
	tenantPeopleSuites:    {tenantPeopleSuites},
}

// The base's tests a start reads, writes again or takes out by name.
const (
	integrationHarness      = "test/integration/harness_test.go"
	simulatedSignIn         = "test/integration/sign_in_skipauth_test.go"
	firstAdministratorSuite = "test/integration/first_administrator_test.go"
	passwordResetSuite      = "test/integration/password_reset_test.go"
	signUpSuite             = "test/integration/sign_up_test.go"
	signUpRoleSuite         = "test/integration/sign_up_role_test.go"
	deployTest              = "pkg/deploy/deploy_test.go"
	migrateTest             = "cmd/deployment/migrate/main_test.go"
	loginPageSpec           = "web/console/src/app/components/login/login.component.spec.ts"
)

// suiteData is what a start's tests are written from.
type suiteData struct {
	// Method is the registration of the one directory method of a start whose directory
	// gives the roles, Field its name as the auth's Settings carry it, and Upper its name
	// as the simulated directory's variables carry it (APP_<Upper>_USERNAME).
	Method, Field, Upper string
	// Google reports that method a Google one; it is a Microsoft one otherwise.
	Google bool
	// Record is the tenant record of an application with tenants, OneTenant whether a
	// person belongs to one tenant, Directories whether a tenant can have a directory of
	// its own, and North the id the development seed gives its first tenant.
	Record      string
	OneTenant   bool
	Directories bool
	North       string
	// What the test of a tenant administrator's conditions takes beside them: Kebab is
	// the record as a method's route spells it and Segment the tenant segment of the
	// routes, South is the id the development seed gives its second tenant, and Password
	// reports that the auth whose people make tenants has the password method.
	Kebab, Segment, South string
	Password              bool
	// Proven reports that the test of a tenant administrator's conditions is written beside
	// the harness. Every grant it proves is one role's, so the harness's provesGrant takes
	// that role in each call, and says so to the application's lint (unparam), as the
	// tenanted reference application's harness does. A harness with no such test beside it
	// says nothing, since a directive that silences nothing is itself refused.
	Proven bool
}

// removedSuite is a test of the base that a start takes out, with the reason.
type removedSuite struct{ file, why string }

// The base's tests each start takes out: what they prove is not there to prove.
var (
	directoryRolesRemoved = []removedSuite{
		{firstAdministratorSuite, "nobody is invited to an auth whose directory gives every role"},
		{"test/integration/groups_test.go", "nothing in the application gives a role, so the auth's generated management keeps no groups"},
		{passwordResetSuite, "no account has a password to reset"},
		{signUpSuite, "nobody signs up: everyone the directory signs in with a role becomes a user"},
		{signUpRoleSuite, "nobody signs up, so no role is given at a sign-up"},
	}
	byDirectoryRemoved = []removedSuite{
		{firstAdministratorSuite, "the first administrator chooses no password; test/integration/invited_administrator_test.go proves the invitation accepted through Google in its place"},
		{passwordResetSuite, "no account has a password to reset"},
		{signUpSuite, "nobody signs up with a password: a person comes by invitation or as a person of a tenant's directory"},
		{signUpRoleSuite, "nobody signs up with a password, so no role is given at such a sign-up"},
	}
)

// suiteSet names the tests the start writes: the tree, what it is rendered from, and the
// base's tests that go. It names none where the start is neither of the two the tool
// knows whole: a start with tenants that was not told its tenancy, or one whose tenants
// have no directories.
func (d *DirectoryStart) suiteSet() (set string, data *suiteData, removed []removedSuite) {
	switch {
	case d.syncsRoles():
		m := d.methods()[0]

		return directoryRolesSuites, &suiteData{Method: m.Name, Field: m.Pascal(), Upper: m.Upper(), Google: m.Kind == app.MethodGoogle}, directoryRolesRemoved
	case d.persona() && d.Tenants != nil && !d.Tenants.NoDirectories:
		return byDirectorySuites, &suiteData{Record: d.Tenants.Record(), OneTenant: d.Tenants.OneTenantPerPerson, Directories: true, North: devTenantNorth}, byDirectoryRemoved
	}

	return "", nil, nil
}

// writeSuites writes the application's tests for the sign-in the auth has, in the place of
// the base's, which sign in with a password: an application impulse new just made passes
// its own tests. It returns the files it settled, written, taken out or left as they
// compile and hold, so the change lists what is left of the base's tests alone. It writes
// nothing where the start is not one it knows whole or the application's tests are not
// the base's.
func (d *DirectoryStart) writeSuites(a *app.App, ch *Change) (map[string]bool, error) {
	set, data, removed := d.suiteSet()
	if set == "" {
		return nil, nil
	}
	if !exists(a, integrationHarness) {
		return nil, nil
	}
	files, err := renderSuites(set, data, modulePath(a), d.Auth)
	if err != nil {
		return nil, err
	}
	settled := map[string]bool{}
	if err := d.writeLoginPageSpec(a, ch, files[loginPageSpec], settled); err != nil {
		return nil, err
	}
	written := make([]string, 0, len(files))
	for rel := range files {
		if rel != loginPageSpec {
			written = append(written, rel)
		}
	}
	sort.Strings(written)
	for _, rel := range written {
		if err := os.MkdirAll(filepath.Dir(a.Abs(rel)), 0o755); err != nil {
			return nil, errors.Wrap(err, "os.MkdirAll()")
		}
		if err := os.WriteFile(a.Abs(rel), files[rel], 0o644); err != nil {
			return nil, errors.Wrap(err, "os.WriteFile()")
		}
		settled[rel] = true
	}
	ch.didf("%s: the tests written for the sign-in the %s auth has, each proving what the base's test of its name proves and signing in through the simulated directory (the session library's skipAuth build tag) where the base's posts a password; under the default build the cases that sign in are skipped, and say so", strings.Join(written, ", "), d.Auth)
	var gone []string
	for _, suite := range removed {
		if err := os.Remove(a.Abs(suite.file)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			return nil, errors.Wrap(err, "os.Remove()")
		}
		settled[suite.file] = true
		gone = append(gone, fmt.Sprintf("%s (%s)", suite.file, suite.why))
	}
	if len(gone) > 0 {
		ch.didf("the base's tests of what the %s auth does not have were taken out: %s", d.Auth, strings.Join(gone, "; "))
	}
	if set == directoryRolesSuites {
		if err := d.deriveDeploymentTests(a, ch, settled); err != nil {
			return nil, err
		}
		// The generated authorization matrix signs nobody in, and its harness builds the
		// auth's identity client as the base's does, so it holds as it is.
		settled["test/authz/harness_test.go"] = true
	}
	if set == byDirectorySuites {
		// The migrate command's test opens the auth through the data level, which gives
		// each registration its settings, and invites the first administrator as the
		// base's does, so it holds as it is.
		settled[migrateTest] = true
	}
	// The cases that hold a grant to the role file read the engine alone, and sign nobody in.
	settled["test/integration/grants_test.go"] = true

	return settled, nil
}

// writeLoginPageSpec writes the spec of the browser application's login page for the
// sign-in the auth has, in the place of the base's, which answers the methods route with
// the password method and expects its form. The Go tests prove the sign-in on the served
// stack; the spec proves what the page draws of it, and it runs in the application's
// browser tests, so the base's would fail there as the application was made. It is
// written where the application's spec is the base's still: one somebody changed is left
// as it is, and the change lists it among what is left.
func (d *DirectoryStart) writeLoginPageSpec(a *app.App, ch *Change, spec []byte, settled map[string]bool) error {
	if spec == nil || !asTheBaseRenders(a, loginPageSpec, d.Auth) {
		return nil
	}
	if err := os.WriteFile(a.Abs(loginPageSpec), spec, 0o644); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	settled[loginPageSpec] = true
	methods := d.methods()
	labels := make([]string, 0, len(methods))
	for i := range methods {
		labels = append(labels, methodLabel(&methods[i]))
	}
	ch.didf("%s: the spec of the browser application's login page written for the sign-in the %s auth has: it answers the methods route with what the auth registers (%s), and expects a button for each and no password form, where the base's answers it with the password method and expects its form", loginPageSpec, d.Auth, strings.Join(labels, ", "))

	return nil
}

// methodLabel is the words the login page shows for a method of a start: the label the
// generator gives a method of its kind.
func methodLabel(m *authpkg.Method) string {
	switch m.Kind {
	case app.MethodGoogle:
		return "Google"
	case app.MethodAzure:
		return "Microsoft"
	case app.MethodPersona:
		return "Persona"
	}

	return m.Name
}

// renderSuites renders a set of suiteFiles for an application, each of the set's trees:
// each file by its path in the application. A file is rendered as the base candidate's
// are: the candidate's module path gives way to the application's and the placeholder
// auth to the first auth's name, and a Go file is formatted.
func renderSuites(set string, data *suiteData, module, auth string) (map[string][]byte, error) {
	candidate, err := baseModulePath()
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, tree := range suiteTrees[set] {
		root := path.Join("suites", tree)
		err = fs.WalkDir(suiteFiles, root, func(p string, entry fs.DirEntry, err error) error {
			if err != nil {
				return errors.Wrapf(err, "fs.WalkDir(): %s", p)
			}
			if entry.IsDir() {
				return nil
			}
			rel := strings.TrimSuffix(strings.TrimPrefix(p, root+"/"), ".tmpl")
			tmpl, err := template.New(rel).Delims("[[", "]]").ParseFS(suiteFiles, p)
			if err != nil {
				return errors.Wrapf(err, "template.ParseFS(): %s", p)
			}
			var b bytes.Buffer
			if err := tmpl.ExecuteTemplate(&b, path.Base(p), data); err != nil {
				return errors.Wrapf(err, "template.Execute(): %s", p)
			}
			out, err := renderedAs(rel, b.String(), candidate, module, auth)
			if err != nil {
				return errors.Wrapf(err, "format.Source(): %s", rel)
			}
			files[rel] = out

			return nil
		})
		if err != nil {
			return nil, errors.Wrapf(err, "fs.WalkDir(): %s", root)
		}
	}

	return files, nil
}

// renderedAs is a test written against the base candidate's module path and its
// placeholder auth, as impulse new renders it for an application: the module path the
// application's, the auth its first auth's name, and a Go file formatted. A spec of the
// browser application is left as it is written, as impulse new leaves the base's.
func renderedAs(rel, text, candidate, module, auth string) ([]byte, error) {
	text = strings.ReplaceAll(text, candidate, module)
	if auth != skeleton.PlaceholderAuth {
		text = names.Rename(text, skeleton.PlaceholderAuth, auth)
	}
	if path.Ext(rel) != ".go" {
		return []byte(text), nil
	}
	out, err := format.Source([]byte(text))
	if err != nil {
		return nil, errors.Wrap(err, "format.Source()")
	}

	return out, nil
}

// baseRendering is a test of the base candidate as impulse new renders it for an
// application of that module path whose first auth has that name: what the file holds
// until somebody changes it. A test names the application nowhere else, so the name the
// application was given does not change it.
func baseRendering(rel, module, auth string) ([]byte, error) {
	candidate, err := baseModulePath()
	if err != nil {
		return nil, err
	}
	base, err := skeleton.FS(skeleton.Base)
	if err != nil {
		return nil, err
	}
	src, err := fs.ReadFile(base, rel)
	if err != nil {
		return nil, errors.Wrapf(err, "fs.ReadFile(): %s", rel)
	}

	return renderedAs(rel, string(src), candidate, module, auth)
}

// asTheBaseRenders reports whether a test of the application is the base's still, byte
// for byte: one the command may write again or take out without losing anybody's work.
func asTheBaseRenders(a *app.App, rel, auth string) bool {
	held, err := os.ReadFile(a.Abs(rel))
	if err != nil {
		return false
	}
	rendered, err := baseRendering(rel, modulePath(a), auth)

	return err == nil && bytes.Equal(held, rendered)
}

// baseModulePath is the module path the base candidate's files import their own packages
// by, which rendering replaces with the application's.
func baseModulePath() (string, error) {
	candidates, err := skeleton.Candidates()
	if err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		if candidate.Name == skeleton.Base {
			return candidate.ModulePath, nil
		}
	}

	return "", errors.Newf("no candidate named %s is embedded", skeleton.Base)
}

// deriveDeploymentTests brings the base's two tests of the deployment to an auth whose
// directory gives every role, which invites no first administrator and has no foothold:
// the deployment package's test loses the two tests that hold the first administrator's
// roles to the role file, and the migrate command's test loses the cases that invite or
// reinvite one, gains the cases that prove nobody is invited, and gives the run the
// method's settings, which the registration validates as the data level opens. Each test
// is derived from the application's own file, so everything else it proves stands as the
// base wrote it; a file that is not as the base writes it is left alone, and listed.
func (d *DirectoryStart) deriveDeploymentTests(a *app.App, ch *Change, settled map[string]bool) error {
	derivations := []struct {
		rel    string
		derive func(src []byte) ([]byte, error)
		did    string
	}{
		{deployTest, d.derivedDeployTest, "the two tests that hold the first administrator's roles to the role file were taken out, since the auth has no foothold; the test that validates the role file stands"},
		{migrateTest, d.derivedMigrateTest, "the cases that invite or reinvite a first administrator were taken out, two cases prove that a release invites nobody and that a fresh invitation is refused, and the run is given the method's settings, which the registration validates as the data level opens"},
	}
	for _, one := range derivations {
		src, mode, err := readFile(a, one.rel)
		if err != nil {
			continue
		}
		out, err := one.derive(src)
		if err != nil {
			ch.skipf("%s: not as the base writes it (%v), so it was left as it is; it is listed among what is left", one.rel, errors.Cause(err))

			continue
		}
		if err := os.WriteFile(a.Abs(one.rel), out, mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		settled[one.rel] = true
		ch.didf("%s: %s", one.rel, one.did)
	}

	return nil
}

// derivedDeployTest is the deployment package's test without the tests of a foothold.
func (d *DirectoryStart) derivedDeployTest(src []byte) ([]byte, error) {
	out, err := cutSource(deployTest, src, []string{"TestFirstAdministratorRoles_areInTheRoleFile", "TestAdministratorRoles_areHeldByTheFirstAdministrator"}, "", nil, "")
	if err != nil {
		return nil, err
	}

	return finishSource(deployTest, src, out)
}

// The declarations of the base's migrate test that serve its first administrator's cases
// alone, and what a case of its table names when it is one of them.
var (
	migrateFoothold      = []string{"administratorAddress", "roleHeld", "someone", "withUser", "withHolder"}
	migrateFootholdCases = regexp.MustCompile(`\b(administratorAddress|roleHeld|noMailbox|whoseInvitation|mailboxHandStep|withUser|withHolder|AdministratorVariable)\b`)
)

// derivedMigrateTest is the migrate command's test for an auth that invites nobody.
func (d *DirectoryStart) derivedMigrateTest(src []byte) ([]byte, error) {
	m := d.methods()[0]
	cases := fmt.Sprintf(`
		{
			name:      "the migrations with the seed apply every schema and data migration and invite nobody: the %[1]s auth's directory gives every role",
			args:      []string{"-seed"},
			wantAfter: fmt.Sprintf("schema: version %%d\ndata: %%s\n", last, seededData(t)),
			after:     nobodyInvited,
		},
		{
			name:     "a fresh invitation for the %[1]s auth is refused before the database is touched: nobody is invited to it",
			missing:  true,
			args:     []string{"-reinvite-administrator", %[1]s.Name},
			wantCode: exitRefused,
			wantErr:  "-reinvite-administrator %[1]s: no auth of that name invites a first administrator",
		},`, d.Auth)
	out, err := cutSource(migrateTest, src, migrateFoothold, "TestExecute", migrateFootholdCases, cases)
	if err != nil {
		return nil, err
	}
	text := string(out)
	edits := []struct{ old, new string }{
		{
			fmt.Sprintf("\t// The foothold's setting, which a case may override, and no mailbox, whatever the\n\t// shell the suite runs in names.\n\tt.Setenv(%s.AdministratorVariable, administratorAddress)\n", d.Auth),
			"\t// No mailbox, whatever the shell the suite runs in names.\n",
		},
		{
			fmt.Sprintf("no auth of that name invites a first administrator; name one of %s\"", d.Auth),
			"no auth of that name invites a first administrator\"",
		},
		{
			"the first administrator's invitation where the\n// command can decide it without mailing, the report on a fresh, a migrated and a dirty\n",
			"the refusal of a first administrator's\n// invitation, the report on a fresh, a migrated and a dirty\n",
		},
		{
			"\tt.Setenv(\"APP_PUBLIC_URL\", \"http://solo.test\")\n",
			"\tt.Setenv(\"APP_PUBLIC_URL\", \"http://solo.test\")\n" + d.methodEnvironment(&m),
		},
	}
	for _, edit := range edits {
		if strings.Count(text, edit.old) != 1 {
			return nil, errors.Newf("%q is not there once", edit.old)
		}
		text = strings.Replace(text, edit.old, edit.new, 1)
	}
	from, to := strings.Index(text, "// No case names a mailbox, so the data level writes its mail to the log"), strings.Index(text, "func TestExecute(")
	if from < 0 || to < from {
		return nil, errors.New("TestExecute's account of the first administrator is not there")
	}
	text = text[:from] + fmt.Sprintf("// The %[1]s auth's directory gives every role, the Super Administrator's included, so a\n// release invites no first administrator and asks for no mailbox on that account, and\n// -reinvite-administrator has no auth to name. Whatever a case does, no invitation's\n// link is in what the command wrote: its output, its errors and its log are read for one\n// after every run.\n", d.Auth) + text[to:]

	return finishSource(migrateTest, src, []byte(text))
}

// methodEnvironment is the lines that give a test's process the settings of the start's
// one method that its registration validates, as a deployment's environment does: the
// one organization, the callback and, where the directory's groups carry the roles,
// their prefix.
func (d *DirectoryStart) methodEnvironment(m *authpkg.Method) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\t// The %s auth's %s registration validates its settings as the data level\n\t// opens, so the run names them, as a deployment's environment does.\n", d.Auth, m.Name)
	fields := m.Fields()
	for i := range fields {
		f := &fields[i]
		var value string
		switch f.Name {
		case authpkg.FieldHostedDomain:
			value = "fleet.example"
		case authpkg.FieldTenantID:
			value = "5b6c1f0e-2d7a-4c3b-9e8f-1a2b3c4d5e6f"
		case authpkg.FieldRedirectURL:
			value = "http://solo.test/api/user/" + m.Name + "/callback"
		case authpkg.FieldGroupPrefix:
			value = d.Auth + "-"
		default:
			continue
		}
		fmt.Fprintf(&b, "\tt.Setenv(%s, %s)\n", strconv.Quote(m.Variable(d.Auth, f)), strconv.Quote(value))
	}

	return b.String()
}

// cutSource takes out of a Go source file every top-level declaration that declares one of
// the names, with its comment, and, where a table is named, every case of the table of
// the test function fn whose text matches mentions (the composite literal assigned to
// tests), putting the cases given first in the table. It refuses a file that declares
// none of a name, or whose function has no such table: the file is then not what the
// caller knows.
func cutSource(rel string, src []byte, gone []string, fn string, mentions *regexp.Regexp, cases string) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
	if err != nil {
		return nil, errors.Wrapf(err, "parser.ParseFile(): %s", rel)
	}
	offset := func(pos token.Pos) int {
		return fset.Position(pos).Offset
	}
	want := map[string]bool{}
	for _, name := range gone {
		want[name] = true
	}
	var cuts []span
	insert := -1
	for _, decl := range file.Decls {
		if test, ok := decl.(*ast.FuncDecl); ok && fn != "" && test.Name.Name == fn {
			table := testTable(test)
			if table == nil {
				return nil, errors.Newf("%s assigns no table to tests", fn)
			}
			insert = offset(table.Lbrace) + 1
			for _, elt := range table.Elts {
				if mentions.Match(src[offset(elt.Pos()):offset(elt.End())]) {
					cuts = append(cuts, span{start: lineStart(src, offset(elt.Pos())), end: lineEnd(src, offset(elt.End()))})
				}
			}
		}
		declared, from := declaredNames(decl)
		for _, name := range declared {
			if want[name] {
				delete(want, name)
				cuts = append(cuts, span{start: offset(from), end: lineEnd(src, offset(decl.End()))})

				break
			}
		}
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for name := range want {
			missing = append(missing, name)
		}
		sort.Strings(missing)

		return nil, errors.Newf("it declares no %s", strings.Join(missing, ", "))
	}
	if fn != "" && insert < 0 {
		return nil, errors.Newf("it declares no %s", fn)
	}
	out, ok := cutSpans(src, cuts, insert, cases)
	if !ok {
		return nil, errors.Newf("%s's table is declared inside what is taken out", fn)
	}

	return out, nil
}

// declaredNames lists the names a top-level declaration declares, a function's own or a
// const or var declaration's, and where the declaration begins, its comment included.
func declaredNames(decl ast.Decl) (declared []string, from token.Pos) {
	from = decl.Pos()
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		declared = []string{decl.Name.Name}
		if decl.Doc != nil {
			from = decl.Doc.Pos()
		}
	case *ast.GenDecl:
		if decl.Doc != nil {
			from = decl.Doc.Pos()
		}
		for _, spec := range decl.Specs {
			if value, ok := spec.(*ast.ValueSpec); ok {
				for _, name := range value.Names {
					declared = append(declared, name.Name)
				}
			}
		}
	}

	return declared, from
}

// cutSpans is a source file without the spans, and with text put in where the offset
// insert of the file as it was now falls; a negative insert puts nothing in. It reports
// false where the place to put the text in is itself inside a span.
func cutSpans(src []byte, cuts []span, insert int, text string) ([]byte, bool) {
	sort.Slice(cuts, func(i, j int) bool {
		return cuts[i].start > cuts[j].start
	})
	out := append([]byte(nil), src...)
	for _, cut := range cuts {
		if insert > cut.start && insert < cut.end {
			return nil, false
		}
		out = append(out[:cut.start], out[cut.end:]...)
		if insert > cut.start {
			insert -= cut.end - cut.start
		}
	}
	if insert >= 0 {
		out = append(out[:insert], append([]byte(text), out[insert:]...)...)
	}

	return out, true
}

// testTable is the composite literal a test function assigns to tests, or nil.
func testTable(fn *ast.FuncDecl) *ast.CompositeLit {
	var table *ast.CompositeLit
	ast.Inspect(fn, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return table == nil
		}
		if name, ok := assign.Lhs[0].(*ast.Ident); ok && name.Name == "tests" {
			table, _ = assign.Rhs[0].(*ast.CompositeLit)
		}

		return table == nil
	})

	return table
}

// lineStart is the offset where the line holding offset begins, and lineEnd the offset
// after the line break that ends it.
func lineStart(src []byte, offset int) int {
	return bytes.LastIndexByte(src[:offset], '\n') + 1
}

func lineEnd(src []byte, offset int) int {
	if i := bytes.IndexByte(src[offset:], '\n'); i >= 0 {
		return offset + i + 1
	}

	return len(src)
}

// finishSource drops the imports a cut left unused and formats the file: an import the
// file read before the cut and reads no more.
func finishSource(rel string, before, after []byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, after, parser.ParseComments)
	if err != nil {
		return nil, errors.Wrapf(err, "parser.ParseFile(): %s after the cut", rel)
	}
	original, err := parser.ParseFile(token.NewFileSet(), rel, before, parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.Wrapf(err, "parser.ParseFile(): %s", rel)
	}
	was, used := selected(original), selected(file)
	out := append([]byte(nil), after...)
	for i := len(file.Imports) - 1; i >= 0; i-- {
		spec := file.Imports[i]
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		} else if p, err := strconv.Unquote(spec.Path.Value); err == nil {
			name = path.Base(p)
		}
		if !was[name] || used[name] {
			continue
		}
		from, to := fset.Position(spec.Pos()).Offset, fset.Position(spec.End()).Offset
		out = append(out[:lineStart(out, from)], out[lineEnd(out, to):]...)
	}
	formatted, err := format.Source(out)
	if err != nil {
		return nil, errors.Wrapf(err, "format.Source(): %s after the cut", rel)
	}

	return formatted, nil
}

// selected lists the names a file selects from: the packages it reads, among them.
func selected(file *ast.File) map[string]bool {
	read := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok {
				read[pkg.Name] = true
			}
		}

		return true
	})

	return read
}
