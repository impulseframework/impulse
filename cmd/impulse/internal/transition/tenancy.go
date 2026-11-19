package transition

import (
	"context"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
)

// Tenancy makes a flat, untenanted application tenanted: the tenant record becomes a
// table and a global resource annotated @tenant, from which the generator derives the
// tenant segment, serves the tenant-scoped resources under it with tenant existence
// concealed, and emits the constructor of the tenant roster; the data level builds the
// roster and starts it, and the app exposes it to the generated code. Nobody creates a
// tenant directly: staff of the auth whose outlets serve the record invite one person,
// and that person's first sign-in makes the tenant, so that auth is handed the generated
// maker over the roster, which the data level builds ahead of it. Which resources
// become tenant-scoped, how existing rows are assigned, the bootstrap order, the tests,
// the tenant segment in the browser's routes, and the tenant menus in its menu file are
// the agent's.
type Tenancy struct {
	// Table is the tenant-record table, PascalCase and plural: Tenants. The domain route
	// segment is its kebab-case form (tenants), and the resource struct its singular.
	Table string
	// OneTenantPerPerson says a person belongs to one tenant, as in an application
	// whose tenants own their people: every auth whose roles the application assigns
	// hands its identity client identity.WithOneTenantPerPerson(), so a role in a second
	// tenant is refused however it would be given. Without it a person can be in
	// several tenants.
	OneTenantPerPerson bool
	// NoDirectories says no tenant has a directory of its own: the transition writes no
	// directories record and no table for one, the tenant record carries no
	// directory-only switch, no auth sets a directory admission, and the browser gets no
	// tenant Sign-in page. A tenant's people then come by invitation alone, and a
	// sign-in method through an organization's directory is a way for an invited person
	// to sign in and nothing more, whichever methods the application has or gets. Without
	// it a tenant can have a directory, which its administrator sets from a sign-in
	// through it.
	NoDirectories bool
}

// The tenant roster as the application wires it: the accessor the generated contract
// (domainScopedApp) asks the App for, the data level's method that builds the roster and
// starts it, and the packages the wiring draws on.
const (
	rosterAccessor      = "TenantRoster"
	rosterStart         = "startTenants"
	resourceImportPath  = "github.com/impulseframework/impulse/resource"
	liveFirestoreImport = "github.com/impulseframework/impulse/resource/live/firestore"
	spannerImportPath   = "cloud.google.com/go/spanner"
	errorsImportPath    = "github.com/go-playground/errors/v5"
)

// TenancyReferenceCandidate is the embedded skeleton with tenancy wired.
const TenancyReferenceCandidate = "tenanted"

// tenancyAdminCandidate is the embedded skeleton the tenant record's pages are copied
// from: the All tenants page's configuration, the menus that place it, and the dialog a
// person who signed up creates a tenant of their own through (the create-your-tenant
// route). The page staff bring a tenant in from, by inviting one person, is the
// library's, drawn from the generated description among the auth's own pages.
const tenancyAdminCandidate = "outlets"

// tenantWebFile is one file a candidate's console carries, by its path under the
// project's source root.
type tenantWebFile struct {
	candidate, rel string
}

// tenantWebFiles are the tenant pages, laid into the default browser project beside the
// generated client: the tenant's sign-in page (who may enter the tenant: its declared
// directories and whether they are the only way in) with its route and its page for the
// menu file, the All tenants page's configuration (the library's list page over the
// tenant record), and the new-tenant dialog, through which a person who signed up and
// holds no tenant yet creates their own. The library's frame carries the rest: the
// tenant the URL names, the guard on the tenant segment, the refusal page and the place
// switcher. The tenant segment in the routes and the menus in the menu file are the
// agent's.
var tenantWebFiles = []tenantWebFile{
	{TenancyReferenceCandidate, tenantSignInRoutesPath},
	{TenancyReferenceCandidate, "app/components/ui/sign-in/tenant-sign-in.component.ts"},
	{TenancyReferenceCandidate, "app/components/ui/sign-in/tenant-sign-in.component.html"},
	{TenancyReferenceCandidate, "app/components/ui/sign-in/tenant-sign-in.component.scss"},
	{TenancyReferenceCandidate, "app/components/ui/sign-in/tenant-sign-in.component.spec.ts"},
	{tenancyAdminCandidate, "app/configs/tenants.config.ts"},
	{tenancyAdminCandidate, "app/components/ui/new-tenant/new-tenant-dialog.component.ts"},
	{tenancyAdminCandidate, "app/components/ui/new-tenant/new-tenant-dialog.component.html"},
	{tenancyAdminCandidate, "app/components/ui/new-tenant/new-tenant-dialog.component.scss"},
	{tenancyAdminCandidate, "app/components/ui/new-tenant/new-tenant-dialog.component.spec.ts"},
}

// tenantSignInRoutesPath is the tenant's sign-in page's route inside the reference
// candidate's console, relative to the project's source root: an application that has
// it has the tenant pages already.
const tenantSignInRoutesPath = "app/components/ui/sign-in/sign-in.routes.ts"

var tableNameRE = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

// devTenantNorth and devTenantSouth are the ids the development seed gives its two
// tenants, the reference's own: a tenant's key is an id, and the seed fixes the two so
// an application's suites can name them.
const (
	devTenantNorth = "11111111-1111-4111-8111-111111111111"
	devTenantSouth = "22222222-2222-4222-8222-222222222222"
)

// Command is the impulse command line for the transition.
func (tn Tenancy) Command() string {
	cmd := fmt.Sprintf("impulse add tenancy --tenant-table %s", tn.Table)
	if tn.OneTenantPerPerson {
		cmd += " --one-tenant-per-person"
	}
	if tn.NoDirectories {
		cmd += " --tenant-directories=false"
	}

	return cmd
}

// Segment is the domain route segment: the table's kebab-case form.
func (tn Tenancy) Segment() string { return strcase.ToKebab(tn.Table) }

// Record is the tenant-record struct: the table's singular.
func (tn Tenancy) Record() string { return singular(tn.Table) }

// Validate checks the transition against the application before anything is changed.
func (tn Tenancy) Validate(a *app.App) error {
	if !tableNameRE.MatchString(tn.Table) || singular(tn.Table) == tn.Table {
		return errors.Newf("table %q: name the tenant-record table in PascalCase and plural, such as Tenants", tn.Table)
	}
	p := a.Profile()
	switch {
	case len(p.Sites) == 0:
		return errors.New("no generator program emits handlers; the application has no site to make tenanted")
	case len(p.Sites) > 1:
		return errors.New("making an application in the sites layout tenanted is not supported yet")
	}
	site := &p.Sites[0]
	if site.Tenanted() {
		return errors.Newf("%s:%d: the application is already tenanted; %s is its tenant record", site.TenantRecord.File, site.TenantRecord.Line, site.TenantRecord.Name)
	}
	if len(site.Generator.MigrationSources) == 0 || !strings.HasPrefix(site.Generator.MigrationSources[0], "file://") {
		return errors.Newf("%s: the tenant table migration needs a file:// migration source", site.Generator.File)
	}

	return nil
}

// Apply makes the deterministic half of the transition.
func (tn Tenancy) Apply(ctx context.Context, a *app.App, exec check.Execer) (*Change, error) {
	if err := tn.Validate(a); err != nil {
		return nil, err
	}
	ch := &Change{Command: tn.Command()}
	site := &a.Profile().Sites[0]
	g := site.Generator

	if err := tn.editProgram(a, g, ch); err != nil {
		return nil, err
	}
	migrations := strings.TrimPrefix(g.MigrationSources[0], "file://")
	if err := tn.writeMigration(a, migrations, ch); err != nil {
		return nil, err
	}
	if err := tn.writeSeed(a, path.Join(path.Dir(migrations), "devseed"), ch); err != nil {
		return nil, err
	}
	if err := tn.writeRecord(a, g.ResourcePackageDir, ch); err != nil {
		return nil, err
	}
	if !tn.NoDirectories {
		if err := tn.writeDirectoryRecord(a, g.ResourcePackageDir, ch); err != nil {
			return nil, err
		}
	}
	early, err := tn.editConfig(a, site, g, ch)
	if err != nil {
		return nil, err
	}
	if err := tn.editAuths(a, site, g, early, ch); err != nil {
		return nil, err
	}
	if tn.OneTenantPerPerson {
		if err := tn.declareOneTenant(a, ch); err != nil {
			return nil, err
		}
	}
	if err := tn.editApp(a, ch); err != nil {
		return nil, err
	}
	if err := tn.writeBrowser(a, site, ch); err != nil {
		return nil, err
	}
	tn.noteBaseSuites(a, ch)
	if err := tn.retellReadme(a, ch); err != nil {
		return nil, err
	}

	emitted := fmt.Sprintf("the %s and %s resources", tn.Record(), tn.DirectoryRecord())
	if tn.NoDirectories {
		emitted = fmt.Sprintf("the %s resource", tn.Record())
	}
	served := tn.managementHandlers(a, g)
	if err := generate(ctx, a, exec, ch, "go generate ./... failed, so the tenant routes are not generated yet; fix the cause and run it:", fmt.Sprintf("ran go generate ./..., which emitted %s, the tenant segment pair under /%s/{%sID}, the roster constructor New%sRoster and the declaration reader %s", emitted, tn.Segment(), strcase.ToCamel(tn.Record()), tn.Record(), tn.DeclarationReader())); err != nil {
		return nil, err
	}
	if err := tn.pruneRoles(a, g, served, ch); err != nil {
		return nil, err
	}

	return ch, nil
}

// managementHandlers lists the generated handler files of each auth's management, by
// the auth's name: what the application serves of its people before the regeneration.
func (tn Tenancy) managementHandlers(a *app.App, g *app.Generator) map[string]map[string]bool {
	served := map[string]map[string]bool{}
	for i := range a.AuthPackages {
		name := a.AuthPackages[i].Name
		served[name] = generatedHandlers(a, path.Join(g.IdentityDir(), name+"handlers"))
	}

	return served
}

// pruneRoles takes out of each auth's role file every grant on a resource the auth's
// generated management served before the regeneration and serves no more. An
// application without tenants lists the roles a person who signs up may be given
// (<Auth>SignUpRoles), and the base's role file grants the list; the generator writes
// no such list once the application has tenants, and the permission engine refuses a
// file that grants a resource the application does not serve, so the application would
// not start with the grant. Every other grant is left as it is, and so is the file of
// an auth that lost no handler.
func (tn Tenancy) pruneRoles(a *app.App, g *app.Generator, served map[string]map[string]bool, ch *Change) error {
	for i := range a.AuthPackages {
		pkg := &a.AuthPackages[i]
		gone := removedHandlers(served[pkg.Name], generatedHandlers(a, path.Join(g.IdentityDir(), pkg.Name+"handlers")))
		rel := path.Join(pkg.Dir, app.RolesFileName)
		if _, err := os.Stat(a.Abs(rel)); err != nil || len(gone) == 0 {
			continue
		}
		pruned, err := pruneGrants(a, rel, gone, nil)
		if err != nil {
			return err
		}
		if len(pruned) > 0 {
			ch.didf("%s: %d grant(s) taken out, on what the %s auth's generated management no longer serves now the application has tenants (%s): the permission engine refuses a role file that grants a resource the application does not serve", rel, len(pruned), pkg.Name, strings.Join(pruned, ", "))
		}
	}

	return nil
}

// baseSuites are the integration tests an application without tenants starts with that
// tenancy changes the ground under, each with what becomes of it. They are written
// against the base's harness and its sign-up, so they stop compiling or stop holding
// once the harness is wired for tenants, and a compile error names one line of one
// file at a time: the change says what to do with each before the first is met.
var baseSuites = []struct{ file, fate string }{
	{
		"sign_up_test.go",
		"proves sign-up in an application without tenants, where opening a sign-in method names the role each new person gets; a tenanted application opens a method with no role, and a person who signs up creates a tenant of their own, so remove the file and prove sign-up as the reference's self_service_test.go does",
	},
	{
		"sign_up_role_test.go",
		"proves the list the role of a person who signs up is picked from, which the generator no longer writes once the application has tenants (its generated file is removed by the regeneration); remove the file",
	},
	{
		"first_administrator_test.go",
		"proves what tenancy leaves as it was, since the first administrator signs in as before; it is written against the base's harness, so where the harness takes the reference's sign-in helpers this file's calls of them follow",
	},
}

// integrationSuite is where the skeleton keeps the suites that run the served stack.
const integrationSuite = "test/integration"

// noteBaseSuites records, for each of the base's integration tests the application
// still carries, what becomes of it in a tenanted application (baseSuites).
func (tn Tenancy) noteBaseSuites(a *app.App, ch *Change) {
	for _, suite := range baseSuites {
		rel := path.Join(integrationSuite, suite.file)
		if _, err := os.Stat(a.Abs(rel)); err != nil {
			continue
		}
		ch.skipf("%s %s", rel, suite.fate)
	}
}

// untenantedOpeningRE matches what the base's README says the application is, in its
// opening paragraph: no tenancy and no resources. It stops being true with this change.
var untenantedOpeningRE = regexp.MustCompile(`no tenancy, no resources yet — the generated API\s+carries only the session's permission digest, and the console shows who is signed in and\s+what they hold\.`)

// retellReadme brings the README's opening up to the change where it is still the
// base's: the sentence that says the application has no tenancy and no resources says
// what it has now, in the record's own names. The rest of the README is the
// application's to write as its tenant-scoped resources arrive, which the change says;
// a README whose opening is the application's own words is left as it is.
func (tn Tenancy) retellReadme(a *app.App, ch *Change) error {
	if _, err := os.Stat(a.Abs(readmeFile)); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	src, mode, err := readFile(a, readmeFile)
	if err != nil {
		return err
	}
	if !untenantedOpeningRE.Match(src) {
		return nil
	}
	opening := fmt.Sprintf("tenancy as data: the `%[1]s` record (`@tenant`) over the\n`%[2]s` table is the domain universe, a tenant's key is an id the server generates, and\ntenant-scoped routes live under `/api/%[3]s/{%[4]sID}/`.", tn.Record(), tn.Table, tn.Segment(), lowerFirst(tn.Record()))
	edited := untenantedOpeningRE.ReplaceAllLiteral(src, []byte(opening))
	if err := os.WriteFile(a.Abs(readmeFile), edited, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: the opening says the application has tenancy (the %s record over %s, its routes under /api/%s) where it said it had none", readmeFile, tn.Record(), tn.Table, tn.Segment())
	ch.skipf("%s describes the application as it was rendered beyond that sentence: say what its %s hold and who its people are as the tenant-scoped resources arrive (the reference's README is the pattern)", readmeFile, tn.manyLower())

	return nil
}

// editProgram adds WithConcealedDomains after GenerateRoutes; the tenant segment itself
// derives from the record, so the program names none.
func (tn Tenancy) editProgram(a *app.App, g *app.Generator, ch *Change) error {
	src, mode, err := readFile(a, g.File)
	if err != nil {
		return err
	}
	edited, err := app.InsertOptions(g.File, src, "GenerateRoutes", []string{"generation.WithConcealedDomains()"})
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.Abs(g.File), edited, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: added WithConcealedDomains(); the tenant segment /%s derives from the %s record", g.File, tn.Segment(), tn.Record())

	return nil
}

// writeMigration adds the tenant table as the next migration, and the directories table
// with it where a tenant can have a directory.
func (tn Tenancy) writeMigration(a *app.App, dir string, ch *Change) error {
	n, err := nextMigration(a.Abs(dir))
	if err != nil {
		return err
	}
	base := fmt.Sprintf("%06d_%s", n, tn.Table)
	up, down := tn.tenantTable(), fmt.Sprintf("DROP INDEX %[1]sByName;\nDROP TABLE %[1]s;\n", tn.Table)
	did := fmt.Sprintf("the %s table (a generated id as its primary key and a unique Name; no tenant has a directory, so the table carries no directory-only switch and no directories table is written)", tn.Table)
	if !tn.NoDirectories {
		up += "\n" + tn.directoryTable()
		down = fmt.Sprintf("DROP INDEX %[2]sByMethodConnection;\nDROP INDEX %[2]sBy%[3]sId;\nDROP TABLE %[2]s;\n", tn.Table, tn.DirectoryTable(), tn.Record()) + down
		did = fmt.Sprintf("the %s table (a generated id as its primary key, a unique Name, and DirectoryOnly) and the %s table (a tenant's directory with its DefaultSeat, held to one for a tenant and to one tenant by its two unique indexes)", tn.Table, tn.DirectoryTable())
	}
	if err := writeNew(a, path.Join(dir, base+".up.sql"), up); err != nil {
		return err
	}
	if err := writeNew(a, path.Join(dir, base+".down.sql"), down); err != nil {
		return err
	}
	ch.didf("%s/%s.up.sql and .down.sql: %s", dir, base, did)

	return nil
}

// tenantTable is the tenant table's statements: the table keyed by a generated id, with
// its unique name, and the directory-only switch where a tenant can have a directory.
func (tn Tenancy) tenantTable() string {
	directoryOnly := ""
	if !tn.NoDirectories {
		directoryOnly = fmt.Sprintf(`  -- The tenant's people enter through its directory only: no other sign-in reaches a
  -- tenant whose flag is true, a password included, but for the password of one of the
  -- tenant's own administrators with a second factor, which is their way back in. Half
  -- of what the tenant declares about who may enter it; the other half is its row of
  -- %s.
  DirectoryOnly BOOL NOT NULL DEFAULT (FALSE),
`, tn.DirectoryTable())
	}

	return fmt.Sprintf(`-- A tenant's key is an id generated when the tenant is made, never a word a person
-- types: it is written into every row the tenant owns and is the domain of every role held in it,
-- so it never changes and is never used twice. The name is what people call the
-- tenant, changed like any field; the unique index below keeps two tenants from
-- carrying one name, and an application that allows that drops it.
CREATE TABLE %[1]s (
  Id STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
%[2]s
  CONSTRAINT CK_%[1]s_Id CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')),
) PRIMARY KEY (Id);

CREATE UNIQUE INDEX %[1]sByName ON %[1]s(Name);
`, tn.Table, directoryOnly)
}

// directoryTable is the directories table's statements, written after the tenant table
// where a tenant can have a directory.
func (tn Tenancy) directoryTable() string {
	return fmt.Sprintf(`-- A tenant's directory, the organization its people sign in through: the sign-in method
-- (google, azure, workos, oidc) and the connection the method's identities carry (a
-- Google Workspace by its domain, a Microsoft Entra tenant by its tenant id, an
-- organization's own OpenID Connect provider by its registration's). Nobody types a
-- row in: it is written from a sign-in through the directory, when the tenant is made
-- by a person who came through it or when its administrator, signed in through it,
-- makes it the tenant's.
-- A tenant has one directory and a directory belongs to one tenant, which the two
-- unique indexes below hold.
-- DefaultSeat names the role a person is given in the tenant at their first sign-in
-- through the directory; NULL says the tenant admits them by invitation only.
CREATE TABLE %[2]s (
  Id STRING(36) NOT NULL,
  %[3]sId STRING(36) NOT NULL,
  Method STRING(16) NOT NULL,
  Connection STRING(MAX) NOT NULL,
  DefaultSeat STRING(64),

  CONSTRAINT CK_%[2]s_Id CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')),
  CONSTRAINT CK_%[2]s_Method CHECK (Method IN ('google', 'azure', 'workos', 'oidc')),
  CONSTRAINT FK_%[2]s_%[3]sId FOREIGN KEY (%[3]sId) REFERENCES %[1]s(Id),
) PRIMARY KEY (Id);

CREATE UNIQUE INDEX %[2]sBy%[3]sId ON %[2]s(%[3]sId);
CREATE UNIQUE INDEX %[2]sByMethodConnection ON %[2]s(Method, Connection);
`, tn.Table, tn.DirectoryTable(), tn.Record())
}

// writeSeed adds the development tenants as a data migration beside the schema.
func (tn Tenancy) writeSeed(a *app.App, dir string, ch *Change) error {
	if _, err := os.Stat(a.Abs(dir)); err == nil {
		ch.skipf("%s already exists, so no development tenants were seeded; add two to it", dir)

		return nil
	}
	up := fmt.Sprintf(`-- The development tenants, under fixed ids: a tenant's key is an id the application
-- generates, and the development data fixes the two so the suites can name them. The
-- development logins name a tenant by its name (the bootstrap identities file).
INSERT INTO %[1]s (Id, Name) VALUES ('%[2]s', 'North');
INSERT INTO %[1]s (Id, Name) VALUES ('%[3]s', 'South');
`, tn.Table, devTenantNorth, devTenantSouth)
	down := fmt.Sprintf("DELETE FROM %s WHERE Id IN ('%s', '%s');\n", tn.Table, devTenantNorth, devTenantSouth)
	base := "000001_dev_" + tn.Segment()
	if err := writeNew(a, path.Join(dir, base+".up.sql"), up); err != nil {
		return err
	}
	if err := writeNew(a, path.Join(dir, base+".down.sql"), down); err != nil {
		return err
	}
	ch.didf("%s/%s.up.sql and .down.sql: the development tenants North and South under fixed ids, a data migration for the bootstrap to apply before the logins", dir, base)

	return nil
}

// writeRecord adds the tenant-record resource struct.
func (tn Tenancy) writeRecord(a *app.App, resourceDir string, ch *Change) error {
	// The generator expects a resource in the file named for it in snake case
	// (ServiceAreas in service_areas.go).
	rel := path.Join(resourceDir, strcase.ToSnake(tn.Table)+".go")
	if _, err := os.Stat(a.Abs(rel)); err == nil {
		ch.skipf("%s already exists, so the %s resource struct was not written; make sure it is a global @resource annotated @tenant and @order, keyed by a generated id (ID impulse.UUID), with a Name marked @tenantName", rel, tn.Record())

		return nil
	}
	pkg, words := packageNameOf(a, resourceDir), tn.recordWords()
	src := fmt.Sprintf(`package %s

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// %[2]s is the tenant record (@tenant): the global resource whose rows are the
	// tenants. Its route name is the segment the tenant-scoped routes are served under,
	// so /%[3]s lists the tenants while /%[3]s/{%[4]sID}/... serves them, and its key is
	// the domain in every tenant-scoped URL. The generator derives the segment from it
	// and emits New%[2]sRoster, the constructor of the tenant roster the data level builds
	// and starts: the tenants as the generated guard knows them, kept current on every
	// instance by what writes this record, so a new tenant is usable at once, without a
	// restart.
	//
	// Nobody creates a tenant directly: the record's create is refused, saying so. An
	// administrator brings an organization in by inviting one person (the generated
	// Invite<Auth>%[2]s of the auth whose outlets serve the record): the invitation
	// carries this record's fields as the administrator entered them, held to the
	// record's own create under the sender's Create grant when it is sent, and the
	// invited person's first sign-in makes the tenant%[6]s. A person who signed up creates their own through the generated
	// create-your-tenant route, while an administrator has a sign-in method open to
	// sign-up.
	//
	// The primary key is an id generated when the tenant is made, as every record's is,
	// and the generator refuses any other key on the tenant record: the id is written into
	// every row the tenant owns and is the domain of every role held in it, so it never
	// changes and is never used twice. What people call the tenant is its name, an
	// ordinary field its administrators change; the unique index on the name (the
	// migration) is what keeps two tenants from carrying one name, and an application
	// that allows that drops the index.
	//
	// %[2]s itself is a GLOBAL resource: administering the tenant list is a global
	// concern. Its list comes in name order (@order) when the request names no sort,
	// since a paged request with neither is refused.
	//
%[7]s	//
	// @resource
	// @tenant
	// @order(Name asc)
	%[2]s struct {
		ID impulse.UUID `+"`spanner:\"Id\"`"+`
		// Name is the tenant's name as its people know it: the session's tenant list
		// carries it, so the place switcher shows it to them.
		// @tenantName
		Name string `+"`spanner:\"Name\"`"+`
%[8]s	}
)

// Config enables change tracking: %[2]s mutations write DataChangeEvents rows in the
// same transaction, the audit of every change to %[9]s.
func (%[2]s) Config() resource.Config {
	return %[5]s
}
`, pkg, tn.Record(), tn.Segment(), strcase.ToCamel(tn.Record()), trackingConfig(a, resourceDir), words.made, words.declares, words.flag, words.audited)
	if err := writeNew(a, rel, src); err != nil {
		return err
	}
	ch.didf("%s: the %s resource struct, global, keyed by a generated id, listed in name order (@order(Name asc)), with %s", rel, tn.Record(), words.did)

	return nil
}

// recordWords are the places the tenant record's source speaks of a tenant's directory,
// in the words of an application whose tenants can have one or of one whose tenants
// have none: how the first sign-in of an invited person makes the tenant, what a tenant
// declares about who may enter it, the directory-only field, what the change events
// audit, and what the change says it wrote.
type recordWords struct {
	made, declares, flag, audited, did string
}

// recordWords words the tenant record for the transition's choice.
func (tn Tenancy) recordWords() recordWords {
	if tn.NoDirectories {
		return recordWords{
			made: " and seats them as its\n\t// administrator",
			declares: fmt.Sprintf(`	// No tenant has a directory in this application: the package declares no
	// directories record and this record carries no directory-only switch, so a
	// tenant's people come by invitation alone, and a tenant admits everyone it seats,
	// however they signed in. A sign-in method through an organization's directory
	// (Google, Microsoft, another provider) is a way for an invited person to sign in
	// and nothing more. The sign-in rule still asks what a tenant declares, through the
	// generated reader (%s, handed to each auth),
	// which answers nothing for every tenant. The record's change events
	// (DataChangeEvents, Config) are the audit of every edit.
`, tn.DeclarationReader()),
			flag:    "",
			audited: "a tenant",
			did:     "change tracking and no directory-only switch, since no tenant has a directory",
		}
	}

	return recordWords{
		made: ", seats them as its administrator\n\t// and, where they came through their organization's directory, makes that directory\n\t// the tenant's",
		declares: fmt.Sprintf(`	// Who may enter a tenant is the record's declaration, data of the application
	// rather than deploy configuration: the directory its people sign in through
	// (%[1]s, one row at most, set from a sign-in through the directory and never
	// typed in) and whether its people enter through it only
	// (DirectoryOnly). The sign-in rule reads it in the sign-in's own transaction
	// (%[2]s, handed to each auth) and every instance's roster keeps a copy beside the
	// keys (New%[3]sRoster, Declarations), which the record's and the directories'
	// generated write paths reload after their commit; a tenant is offered to a session
	// only when the session may enter it. The record's change events (DataChangeEvents,
	// Config) are the audit of every edit.
`, tn.DirectoryRecord(), tn.DeclarationReader(), tn.Record()),
		flag: `		// DirectoryOnly says the tenant's people enter through its directory only: no
		// other sign-in reaches the tenant, and the flag closes this tenant alone. Its
		// own administrators keep a way in, with a password and a code sent to their
		// email, so a tenant whose directory has a problem is mended by its own people.
		// The switch is the tenant's own: the tenant's administrator turns it with the
		// generated methods in the tenant's scope (Make<Record>DirectoryOnly, which
		// refuses turning it on until the tenant has its directory, unless the caller is
		// signed in through it, and until the caller has a password where their auth has
		// passwords, and End<Record>DirectoryOnly). The record shows the flag and its
		// own write paths never write it. A tenant is directory only from the start
		// where the invitation that makes it says so: that invitation is accepted
		// through the directory the tenant will be only of and no other way.
		// @directoryOnly
		DirectoryOnly bool ` + "`spanner:\"DirectoryOnly\"`" + `
`,
		audited: "what a tenant declares",
		did:     "its declaration's flag (DirectoryOnly) and change tracking",
	}
}

// editConfig gives the data level the tenant roster: a field on DataConfiguration, a new
// file whose startTenants builds the roster with the record's generated constructor and
// starts it and whose TenantRoster accessor hands it to the app, and the start inserted
// where the configuration is built. Where an auth makes tenants the roster is built
// ahead of it, in NewDataConfiguration, and startTenants only starts it: the answer names
// the roster there (nil where the level builds it in startTenants).
func (tn Tenancy) editConfig(a *app.App, site *app.Site, g *app.Generator, ch *Change) (*earlyRoster, error) {
	handlersPath := a.GoMod.Module.Mod.Path + "/" + g.HandlersDir()
	handlersPkg := packageNameOf(a, g.HandlersDir())
	constructor := handlersPkg + ".New" + tn.Record() + "Roster"
	rel, src, mode, err := findDeclaringFile(a, "DataConfiguration")
	if err != nil {
		return nil, err
	}
	if rel == "" {
		ch.skipf("no file declares a DataConfiguration struct, so the tenant roster was not added to the data level; build it where the database is opened with %s(client, resource.WithTenantSignals(<the live service>)), start it (Start) and fail the start on its error, and expose it as %s()", constructor, rosterAccessor)

		return nil, nil
	}
	client, err := clientExpression(rel, src)
	if err != nil {
		return nil, err
	}
	if client == "" {
		ch.skipf("%s: DataConfiguration holds no *resource.SpannerClient and no *spanner.Client field the roster could read through, so the tenant roster was not added; build it with %s over the database client, start it, and expose it as %s()", rel, constructor, rosterAccessor)

		return nil, nil
	}
	liveField, err := app.StructFieldOfType(rel, src, "DataConfiguration", liveFirestoreImport, "Service")
	if err != nil {
		return nil, err
	}
	pkg, err := app.PackageName(rel, src)
	if err != nil {
		return nil, err
	}
	gained, err := addRosterField(a, rel, src, mode, ch)
	if err != nil {
		return nil, err
	}
	authFields, err := authFieldsOf(a, rel, src)
	if err != nil {
		return nil, err
	}
	early, err := tn.buildRosterEarly(a, site, rel, handlersPath, constructor, client, liveField)
	if err != nil {
		return nil, err
	}
	tenancyFile := path.Join(path.Dir(rel), "tenancy.go")
	source, err := tn.configSource(pkg, handlersPath, handlersPkg, client, liveField, authFields, early != nil)
	if err != nil {
		return nil, err
	}
	if err := writeNew(a, tenancyFile, source); err != nil {
		return nil, err
	}
	if liveField == "" {
		ch.skipf("%s: DataConfiguration holds no *firestore.Service field (resource/live/firestore), so the roster is built without resource.WithTenantSignals and reloads at its backstop alone; pass the live service the level opens, so a tenant made on another instance reaches this one at once", tenancyFile)
	}
	if early != nil {
		ch.didf("%s: the tenant roster (NewDataConfiguration builds it with %s ahead of the %s auth, through which a tenant is made, and %s starts it), %s() and %s() on DataConfiguration; %s gained %s and the roster's construction", tenancyFile, constructor, early.auth, rosterStart, rosterAccessor, declarationsAccessor, rel, gained)

		return early, nil
	}
	ch.didf("%s: the tenant roster (%s builds it with %s and starts it), %s() and %s() on DataConfiguration; %s gained %s", tenancyFile, rosterStart, constructor, rosterAccessor, declarationsAccessor, rel, gained)

	return nil, nil
}

// earlyRoster is the roster the data level builds ahead of the auth a tenant is made
// through, as NewDataConfiguration names it: the auth, the variable the roster is held
// in, and the variable the live service is held in, which the generated maker is
// constructed over beside the roster.
type earlyRoster struct {
	auth, roster, live string
}

// earlyRosterVar is the variable NewDataConfiguration holds the roster in when it builds
// it ahead of an auth.
const earlyRosterVar = "tenants"

// The names the roster's early construction reads: a variable, or a field of one, as a
// configuration literal names it; the level's field a client expression reads on a
// method's receiver; and a statement that declares or assigns the roster's variable.
var (
	localNameRE     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)
	receiverFieldRE = regexp.MustCompile(`\bc\.(\w+)`)
	rosterVarRE     = regexp.MustCompile(`(?m)^\s*` + earlyRosterVar + `\b[^\n]*:?= `)
)

// literalValue is the value the first "field: value," line of a keyed literal gives the
// field, as the file writes it; empty when the file has no such line.
func literalValue(src []byte, field string) string {
	m := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(field) + `:\s+(.+),$`).FindSubmatch(src)
	if m == nil {
		return ""
	}

	return string(m[1])
}

// buildRosterEarly builds the roster where the data level opens, ahead of the auth a
// tenant is made through: that auth's identity client is constructed over the generated
// maker and the maker over the roster, so the roster cannot wait for startTenants, which
// runs once every auth is built. The variables NewDataConfiguration holds the client and
// the live service in are read from the configuration's literal, the construction is
// inserted before the auth's, and the literal is handed the roster. The answer is nil,
// and the file is left as it was, where no auth makes tenants or the level has another
// shape (no live service, a client or a live service the literal builds in place, an
// auth built elsewhere): the settings edit then records the wiring as the agent's.
func (tn Tenancy) buildRosterEarly(a *app.App, site *app.Site, rel, handlersPath, constructor, client, liveField string) (*earlyRoster, error) {
	pkg := tn.makerAuth(a, site)
	if pkg == nil || liveField == "" {
		return nil, nil
	}
	src, mode, err := readFile(a, rel)
	if err != nil {
		return nil, err
	}
	// client names the level's field on a method's receiver (c.database.ResourceClient);
	// in NewDataConfiguration the same client is the variable the literal gives the field.
	field := receiverFieldRE.FindStringSubmatch(client)
	if field == nil {
		return nil, nil
	}
	clientLocal, liveLocal := literalValue(src, field[1]), literalValue(src, liveField)
	if !localNameRE.MatchString(clientLocal) || !localNameRE.MatchString(liveLocal) || rosterVarRE.Match(src) {
		return nil, nil
	}
	construction := fmt.Sprintf("\t// The tenant roster is built ahead of the %[1]s auth: a tenant is made through that\n\t// auth, by the first sign-in of a person its staff invited, so the auth's identity\n\t// client is constructed over the generated maker, and the maker over the roster.\n\t// %[2]s starts it once the configuration is built.\n\t%[3]s := %[4]s(%[5]s, resource.WithTenantSignals(%[6]s))",
		pkg.Name, rosterStart, earlyRosterVar, constructor, strings.Replace(client, "c."+field[1], clientLocal, 1), liveLocal)
	edited, err := app.AddStatementsBeforeCall(rel, src, "", "NewDataConfiguration", pkg.Name+".New", construction)
	if errors.Is(err, app.ErrNoAnchor) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	edited, err = app.AddLiteralElement(rel, edited, "NewDataConfiguration", "DataConfiguration", "tenants: "+earlyRosterVar)
	if errors.Is(err, app.ErrNoAnchor) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if edited, err = app.AddImport(rel, edited, handlersPath); err != nil {
		return nil, err
	}
	if err := os.WriteFile(a.Abs(rel), edited, mode); err != nil {
		return nil, errors.Wrap(err, "os.WriteFile()")
	}

	return &earlyRoster{auth: pkg.Name, roster: earlyRosterVar, live: liveLocal}, nil
}

// addRosterField gives DataConfiguration the roster field (and the resource import its
// type needs) and inserts the roster's start where the configuration is built, writing
// the file; gained says which of the two the file took. An anchor miss for the start
// leaves the file as edited so far: the working copy is only replaced by a result the
// editor produced.
func addRosterField(a *app.App, rel string, src []byte, mode os.FileMode, ch *Change) (gained string, err error) {
	edited, err := app.AddStructField(rel, src, "DataConfiguration", "tenants *resource.TenantRoster")
	if err != nil {
		return "", err
	}
	edited, err = app.AddImport(rel, edited, resourceImportPath)
	if err != nil {
		return "", err
	}
	wrapErr := plainErr
	if ok, _ := app.HasImport(rel, src, errorsImportPath); ok {
		wrapErr = `errors.Wrap(err, "` + rosterStart + `()")`
	}
	gained = "the field and the start"
	wrapped, err := app.WrapReturn(rel, edited, "NewDataConfiguration", "DataConfiguration", "conf", "conf."+rosterStart+"(ctx)", wrapErr)
	switch {
	case errors.Is(err, app.ErrNoAnchor):
		gained = "the field"
		ch.skipf("%s: NewDataConfiguration does not end in \"return &DataConfiguration{...}, nil\", so the roster's start was not inserted; call conf.%s(ctx) once the configuration is built, and fail the start on its error", rel, rosterStart)
	case err != nil:
		return "", err
	default:
		edited = wrapped
	}
	if err := os.WriteFile(a.Abs(rel), edited, mode); err != nil {
		return "", errors.Wrap(err, "os.WriteFile()")
	}

	return gained, nil
}

// clientExpression is the expression, on a DataConfiguration method's receiver c, of the
// client the roster reads through: the database driver's resource client, a
// *resource.SpannerClient field as it is, or a *spanner.Client field wrapped as a
// resource client; empty when the level holds none of them.
func clientExpression(rel string, src []byte) (string, error) {
	field, err := app.StructFieldOfType(rel, src, "DataConfiguration", databaseImportPath, "Driver")
	if err != nil {
		return "", err
	}
	if field != "" {
		return "c." + field + ".ResourceClient", nil
	}
	field, err = app.StructFieldOfType(rel, src, "DataConfiguration", resourceImportPath, "SpannerClient")
	if err != nil {
		return "", err
	}
	if field != "" {
		return "c." + field, nil
	}
	field, err = app.StructFieldOfType(rel, src, "DataConfiguration", spannerImportPath, "Client")
	if err != nil {
		return "", err
	}
	if field != "" {
		return "resource.NewSpannerClient(c." + field + ")", nil
	}

	return "", nil
}

// authFieldsOf names the DataConfiguration fields that hold an auth of the application
// (a *Auth of one of its auth packages), in the packages' order: the auths startTenants
// hands the roster to, so the invitation screen names a tenant.
func authFieldsOf(a *app.App, rel string, src []byte) ([]string, error) {
	var fields []string
	for i := range a.AuthPackages {
		field, err := app.StructFieldOfType(rel, src, "DataConfiguration", a.AuthPackages[i].Path, "Auth")
		if err != nil {
			return nil, err
		}
		if field != "" {
			fields = append(fields, field)
		}
	}

	return fields, nil
}

// configSource is the data level's tenancy file: startTenants building the roster with
// the generated constructor over the client and the live service's tenants signal (a
// comment stands where the level has no live service field), starting it and handing it
// to each auth the level holds (authFields) to name the tenant an invitation seats a
// person in, and the accessor the app's Configurer asks for. With early, the roster is
// built where the level opens, ahead of the auth a tenant is made through, and
// startTenants only starts it. The source is formatted, so the imports sort whatever the
// handlers package's path is.
func (tn Tenancy) configSource(pkg, handlersPath, handlersPkg, client, liveField string, authFields []string, early bool) (string, error) {
	alias := ""
	if handlersPkg != path.Base(handlersPath) {
		alias = handlersPkg + " "
	}
	handlersImport := fmt.Sprintf("\t%s%q\n", alias, handlersPath)
	does := fmt.Sprintf("builds the tenant roster over the %s record with the generated\n// constructor and starts it", tn.Record())
	// construction is the statement that builds the roster, as a line of its own that
	// opens the function, or nothing: the function then opens with the start, and no
	// blank line stands between its brace and its first statement, which the
	// application's own lint refuses (gofumpt, whitespace).
	construction := fmt.Sprintf("\tc.tenants = %s.New%sRoster(%s, resource.WithTenantSignals(c.%s))\n", handlersPkg, tn.Record(), client, liveField)
	if liveField == "" {
		construction = fmt.Sprintf("\t// No live service field was found on DataConfiguration, so the roster reloads at\n"+
			"\t// its backstop alone; pass resource.WithTenantSignals(<the live service>) so a tenant\n"+
			"\t// made on another instance reaches this one at once.\n"+
			"\tc.tenants = %s.New%sRoster(%s)\n", handlersPkg, tn.Record(), client)
	}
	if early {
		// NewDataConfiguration built the roster, over the generated constructor it imports.
		handlersImport, construction = "", ""
		does = fmt.Sprintf("starts the tenant roster, built over the %s record with the generated\n// constructor where the level is opened, ahead of the auth a tenant is made through", tn.Record())
	}
	naming := ""
	if len(authFields) > 0 {
		naming = "\t// The invitation screen is read by a person not yet signed in, who can read no\n" +
			"\t// tenant record: the roster names the tenant an invitation seats them in, from its\n" +
			"\t// copy, so the screen says where they are invited and not the tenant's id.\n"
		for _, field := range authFields {
			naming += fmt.Sprintf("\tc.%s.Session().SetTenantNames(c.tenants)\n", field)
		}
	}
	src := fmt.Sprintf(`package %[1]s

import (
	"context"

%[2]s	"github.com/impulseframework/impulse/resource"
	"github.com/go-playground/errors/v5"
)

// startTenants %[3]s: the roster reads the %[5]s table once, fails the start when
// it cannot, and keeps the set current until ctx ends, rereading on the tenants signal
// (a tenant made or deleted on any instance, published by what writes the record) and
// at its backstop. The generated DomainGuard asks it before a tenant-scoped request
// runs, so a new tenant is usable at once, without a restart.
func (c *DataConfiguration) startTenants(ctx context.Context) error {
%[6]s	if err := c.tenants.Start(ctx); err != nil {
		return errors.Wrap(err, "resource.TenantRoster.Start()")
	}
%[8]s
	return nil
}

// TenantRoster returns the application's tenant roster: the tenants as the generated
// guard knows them, and the roster a session's tenant list is filtered from.
func (c *DataConfiguration) TenantRoster() *resource.TenantRoster {
	return c.tenants
}

%[7]s
func (c *DataConfiguration) TenantDeclarations() resource.TenantDeclarations {
	return c.tenants.Declarations
}
`, pkg, handlersImport, does, tn.Record(), tn.Table, construction, tn.declarationsComment(), naming)
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return "", errors.Wrap(err, "format.Source()")
	}

	return string(formatted), nil
}

// declarationsComment is the comment over the data level's TenantDeclarations accessor:
// the request-time half of what a tenant declares, read from the roster's copy, or, in
// an application whose tenants have no directories, what the accessor answers there and
// why it stands.
func (tn Tenancy) declarationsComment() string {
	if tn.NoDirectories {
		return fmt.Sprintf(`// TenantDeclarations reads what a tenant declares about who may enter it from the
// roster's copy. The %[1]s record declares no directories, so every tenant declares
// nothing and a session is offered every tenant that seats its person, however they
// signed in. The accessor stands where an application whose tenants have directories
// has it, as the generated reader each auth's settings carry does
// (%[2]s): the sign-in rule and the session's permissions are
// wired the same way in both.`, tn.Record(), tn.DeclarationReader())
	}

	return fmt.Sprintf(`// TenantDeclarations reads what a tenant declares about who may enter it from the
// roster's copy, reloaded on every write of the %[1]s record or its directories: the
// request-time half of the declaration, which filters a session's tenant list. The
// sign-in half reads the same declaration in the sign-in's transaction, through the
// generated reader each auth's settings carry (%[2]s).`, tn.Record(), tn.DeclarationReader())
}

// editApp exposes the roster to the generated code, whose contract (domainScopedApp) asks
// the App for TenantRoster(): the Configurer requires it, the App carries it and hands its
// Domains to the session permissions, and a new file declares the interface and the
// accessor.
func (tn Tenancy) editApp(a *app.App, ch *Change) error {
	rel, src, mode, err := findDeclaringFile(a, "Configurer")
	if err != nil {
		return err
	}
	if rel == "" {
		ch.skipf("no file declares a Configurer interface, so the tenant roster was not exposed on the app; the generated code needs %s() *resource.TenantRoster on the handlers, answering the roster the data level built", rosterAccessor)

		return nil
	}
	pkg, err := app.PackageName(rel, src)
	if err != nil {
		return err
	}
	edited, err := app.AddInterfaceLine(rel, src, "Configurer", "TenancyConfigurer")
	if err != nil {
		return err
	}
	edited, err = app.AddStructField(rel, edited, "App", "tenants *resource.TenantRoster")
	if errors.Is(err, app.ErrNoAnchor) {
		ch.skipf("%s: no App struct to carry the roster; give the handlers a %s method answering the configurer's, and hand the configurer's %s() to resource.SessionPermissions", rel, rosterAccessor, declarationsAccessor)
		edited = nil
	} else if err != nil {
		return err
	}
	if edited != nil {
		edited, err = app.AddStructField(rel, edited, "App", "declarations resource.TenantDeclarations")
		if err != nil {
			return err
		}
		edited, err = app.AddLiteralElement(rel, edited, "New", "App", "tenants: cfg."+rosterAccessor+"()")
		if errors.Is(err, app.ErrNoAnchor) {
			ch.skipf("%s: New builds no App literal; set tenants and declarations from the configurer's %s() and %s() where the App is constructed", rel, rosterAccessor, declarationsAccessor)
		} else if err != nil {
			return err
		} else if edited, err = app.AddLiteralElement(rel, edited, "New", "App", "declarations: cfg."+declarationsAccessor+"()"); err != nil {
			return err
		}
	}
	if edited != nil {
		edited, err = app.AddImport(rel, edited, resourceImportPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(a.Abs(rel), edited, mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
	}
	tenancyFile := path.Join(path.Dir(rel), "tenancy.go")
	seatPkg := tn.seatAuth(a)
	seatPkgName := ""
	if seatPkg != nil {
		seatPkgName = packageNameOf(a, seatPkg.Dir)
	}
	if err := writeNew(a, tenancyFile, tn.appSource(pkg, seatPkg, seatPkgName)); err != nil {
		return err
	}
	if seatPkg == nil {
		ch.skipf("no auth whose roles the application assigns constructs a session.Auth the scan reads, so the roles a person who creates a tenant of their own holds in it were not exposed on the app; the generated create-your-own route of the %s record asks the handlers for %s() []accesstypes.Role (the domain roles that person holds in the new tenant, the auth's AdministratorRoles held in every tenant) beside the auth's identity client (<Auth>Identity()); add them where the App is declared", tn.Record(), tn.rolesAccessor())
		ch.didf("%s: TenancyConfigurer (embedded in Configurer, asking %s() and %s()) and App.%s(); %s gained the fields and their assignments", tenancyFile, rosterAccessor, declarationsAccessor, rosterAccessor, rel)
	} else {
		ch.didf("%s: TenancyConfigurer (embedded in Configurer, asking %s() and %s()), App.%s() and App.%s() (the %s auth's AdministratorRoles held in every tenant: the domain roles the %s record's generated create-your-own route gives the person who creates a tenant of their own, through the auth's identity client, which the generated contract asks the handlers for); %s gained the fields and their assignments", tenancyFile, rosterAccessor, declarationsAccessor, rosterAccessor, tn.rolesAccessor(), seatPkg.Name, tn.Record(), rel)
	}
	if edited != nil {
		if err := tn.editPermissions(a, rel, ch); err != nil {
			return err
		}
	}

	return nil
}

// editPermissions hands the roster's Domains and the declarations to the session
// permissions: the resource.SessionPermissions call of the App's UserPermissions passed
// nil for both where an untenanted application lists no domain, and a login's tenant
// list is the roster filtered by the principal's footholds and by what each tenant
// declares about who may enter it.
func (tn Tenancy) editPermissions(a *app.App, rel string, ch *Change) error {
	src, mode, err := readFile(a, rel)
	if err != nil {
		return err
	}
	edited, err := app.ReplaceNilArgument(rel, src, "UserPermissions", "SessionPermissions", "a.tenants.Domains")
	switch {
	case errors.Is(err, app.ErrNoAnchor):
		ch.skipf("%s: UserPermissions passes no nil roster to resource.SessionPermissions, so the roster's Domains and the declarations were not handed to it; pass a.tenants.Domains and a.declarations where the session permissions are composed, so a login's tenant list is the roster filtered by its footholds and by what each tenant declares", rel)

		return nil
	case err != nil:
		return err
	}
	handed := "the roster's Domains"
	withDeclarations, err := app.ReplaceNilArgument(rel, edited, "UserPermissions", "SessionPermissions", "a.declarations")
	switch {
	case errors.Is(err, app.ErrNoAnchor):
		ch.skipf("%s: UserPermissions passes no nil declarations to resource.SessionPermissions after the roster, so the declarations were not handed to it; pass a.declarations as its fifth argument, so a tenant is offered only to a session it admits", rel)
	case err != nil:
		return err
	default:
		// The function's comment says what it does now, which is true once the
		// declarations are handed to it too.
		edited = tn.retellPermissions(withDeclarations)
		handed = "the roster's Domains and the declarations"
	}
	if err := os.WriteFile(a.Abs(rel), edited, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: UserPermissions passes %s to resource.SessionPermissions", rel, handed)

	return nil
}

// The sentence the base skeleton's UserPermissions comment ends with, which says the
// application has no tenants, and what a tenanted application's says in its place: the
// reference's words. The comment is rewritten only where it reads as the skeleton
// rendered it; an author's own words are left.
var (
	untenantedPermissionsRE = regexp.MustCompile(`(?m)^// and attenuated by the session's permission mask\. The application has no tenants, so\n// it passes no roster and no tenant declarations, and a session lists no domain\.\n`)
	tenantedPermissions     = "// listing the tenants of the application's roster where the principal holds a grant,\n" +
		"// and attenuated by the session's permission mask. A tenant is offered only when the\n" +
		"// session may enter it: the tenant's declaration, read from the roster's copy, admits\n" +
		"// the session's first sign-in (any sign-in where the tenant is not directory only, a\n" +
		"// sign-in through the tenant's own directory where it is), and a super administrator\n" +
		"// reaches every tenant. A tenant that seats the person and is closed to the session\n" +
		"// stays in its tenant list marked closed, with the reason.\n"
	tenantedOpenPermissions = "// listing the tenants of the application's roster where the principal holds a grant,\n" +
		"// and attenuated by the session's permission mask. No tenant has a directory in this\n" +
		"// application, so no tenant declares who may enter it and none is closed to a\n" +
		"// session: every tenant that seats the person is offered, however they signed in.\n"
)

// retellPermissions rewrites the end of the UserPermissions comment the base skeleton
// carries, which says the application has no tenants, to what the function does once
// it is handed the roster and the declarations: the reference's words where a tenant
// can have a directory, and the words of an application whose tenants have none.
func (tn Tenancy) retellPermissions(src []byte) []byte {
	if tn.NoDirectories {
		return untenantedPermissionsRE.ReplaceAll(src, []byte(tenantedOpenPermissions))
	}

	return untenantedPermissionsRE.ReplaceAll(src, []byte(tenantedPermissions))
}

// rolesAccessor is the generated contract's accessor of the roles the tenant record's
// create-your-own route gives the person who creates a tenant of their own:
// <Record>AdministratorRoles.
func (tn Tenancy) rolesAccessor() string {
	return tn.Record() + "AdministratorRoles"
}

// seatAuth picks the auth whose generated management the tenant record's create-your-own
// route gives a tenant's creator the roles through: the first auth package whose Auth
// the scan reads and whose roles the application assigns (an auth whose directory names
// the roles seats nobody). Nil when none qualifies.
func (tn Tenancy) seatAuth(a *app.App) *app.AuthPackage {
	for i := range a.AuthPackages {
		pkg := &a.AuthPackages[i]
		for j := range a.Auths {
			if path.Dir(a.Auths[j].File) == pkg.Dir && !directoryAuthority(&a.Auths[j]) {
				return pkg
			}
		}
	}

	return nil
}

// appSource is the App's tenancy file: the configurer's tenancy half, the roster's
// accessor and, over the seat auth when one qualifies, the creator's roles.
func (tn Tenancy) appSource(pkg string, seatPkg *app.AuthPackage, seatPkgName string) string {
	imports := `import "github.com/impulseframework/impulse/resource"`
	roles := ""
	if seatPkg != nil {
		imports = fmt.Sprintf("import (\n\t\"github.com/impulseframework/impulse/accesstypes\"\n\t\"github.com/impulseframework/impulse/resource\"\n\t%q\n)", seatPkg.Path)
		roles = fmt.Sprintf(`
// %[1]s names the domain roles the person who creates a %[2]s through the
// generated create-your-own route of the %[3]s record holds in it from then on: the %[4]s
// auth's administrator role in a tenant, the one its first administrator holds in every
// tenant (%[5]s.AdministratorRoles), so the first person in a tenant can do there what an
// administrator can, invite its people included.
func (a *App) %[1]s() []accesstypes.Role {
	_, everyDomain := %[5]s.AdministratorRoles()

	return everyDomain
}
`, tn.rolesAccessor(), strings.ToLower(tn.Record()), tn.Record(), seatPkg.Name, seatPkgName)
	}

	return fmt.Sprintf(`package %s

%s

// TenancyConfigurer is what the configuration provides for tenancy: the tenant roster the
// data level built with the generated constructor and started, and the roster's copy of
// what each tenant declares about who may enter it. The generated DomainGuard and the
// consolidated dispatcher ask the roster whether a domain is a tenant, the session's
// permissions ask the declarations whether the session may enter it, and since tenant
// existence is concealed (generation.WithConcealedDomains) they ask the caller's foothold
// next, so a prober cannot confirm a tenant exists from the rejection shape.
type TenancyConfigurer interface {
	%[3]s() *resource.TenantRoster
%[6]s
	%[4]s() resource.TenantDeclarations
}

// %[3]s returns the application's tenant roster, the generated contract's accessor
// (domainScopedApp); UserPermissions hands its Domains to resource.SessionPermissions, so
// a session's tenant list is the roster filtered by the principal's footholds.
func (a *App) %[3]s() *resource.TenantRoster {
	return a.tenants
}
%[5]s`, pkg, imports, rosterAccessor, declarationsAccessor, roles, tn.configurerComment())
}

// configurerComment is the comment over the configurer's TenantDeclarations: what the
// session's permissions decide with it, in an application whose tenants can have a
// directory or in one whose tenants have none.
func (tn Tenancy) configurerComment() string {
	if tn.NoDirectories {
		return fmt.Sprintf(`	// %[1]s reads what a tenant declares about who may enter it, as the running
	// application knows it: the roster's copy. The tenant record declares no
	// directories, so every tenant declares nothing, and UserPermissions, which hands it
	// to resource.SessionPermissions, offers a session every tenant that seats its
	// person, however they signed in.`, declarationsAccessor)
	}

	return fmt.Sprintf(`	// %[1]s reads what a tenant declares about who may enter it, as the running
	// application knows it: the roster's copy, reloaded on every write of the tenant
	// record or its directories. UserPermissions hands it to resource.SessionPermissions,
	// so a tenant that is not directory only is offered to every session of a person it
	// seats, and a directory-only tenant to a session that came through its own
	// directory and to no other, but for its own administrator's password session that
	// carries a second factor.`, declarationsAccessor)
}

// packageNameOf is the package clause of the non-test Go files under a root-relative
// directory, or the directory's name when none can be read.
func packageNameOf(a *app.App, dir string) string {
	entries, err := os.ReadDir(a.Abs(dir))
	if err != nil {
		return path.Base(dir)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(a.Abs(path.Join(dir, e.Name())))
		if err != nil {
			continue
		}
		if name, err := app.PackageName(e.Name(), src); err == nil {
			return name
		}
	}

	return path.Base(dir)
}

// findDeclaringFile finds the non-test Go file under the module that declares the named
// type, skipping generated files and browser workspaces.
func findDeclaringFile(a *app.App, typeName string) (rel string, src []byte, mode os.FileMode, err error) {
	root, err := os.OpenRoot(a.Root)
	if err != nil {
		return "", nil, 0, errors.Wrap(err, "os.OpenRoot()")
	}
	defer root.Close()
	webDirs := map[string]bool{}
	for _, w := range a.WebApps {
		webDirs[w.Dir] = true
	}
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.Wrap(walkErr, "fs.WalkDir()")
		}
		if d.IsDir() {
			if p != "." && (webDirs[p] || skippedNames[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}

			return nil
		}
		name := d.Name()
		if rel != "" || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, "zz_gen_") {
			return nil
		}
		data, readErr := root.ReadFile(filepath.FromSlash(p))
		if readErr != nil {
			return errors.Wrap(readErr, "os.Root.ReadFile()")
		}
		// A file the tool cannot parse is not the one.
		if declares, parseErr := app.DeclaresType(p, data, typeName); parseErr == nil && declares {
			info, statErr := d.Info()
			if statErr != nil {
				return errors.Wrap(statErr, "fs.DirEntry.Info()")
			}
			rel, src, mode = p, data, info.Mode().Perm()
		}

		return nil
	})
	if err != nil {
		return "", nil, 0, errors.Wrap(err, "fs.WalkDir()")
	}

	return rel, src, mode, nil
}

// nextMigration returns the number after the highest migration in the directory.
func nextMigration(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, errors.Wrapf(err, "os.ReadDir(): migrations at %s", dir)
	}
	highest := 0
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(num); err == nil {
			highest = max(highest, n)
		}
	}

	return highest + 1, nil
}

// writeNew writes a root-relative file that must not exist yet, creating its directory.
// The write goes through a root scoped to the application, so no path lands outside it.
func writeNew(a *app.App, rel, content string) error {
	root, err := os.OpenRoot(a.Root)
	if err != nil {
		return errors.Wrap(err, "os.OpenRoot()")
	}
	defer root.Close()
	local := filepath.FromSlash(rel)
	if _, err := root.Stat(local); err == nil {
		return errors.Newf("%s already exists", rel)
	}
	if dir := filepath.Dir(local); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return errors.Wrap(err, "os.Root.MkdirAll()")
		}
	}
	if err := root.WriteFile(local, []byte(content), 0o644); err != nil {
		return errors.Wrap(err, "os.Root.WriteFile()")
	}

	return nil
}

// singular is the tenant-record struct name for a plural table name.
func singular(table string) string {
	switch {
	case strings.HasSuffix(table, "ies"):
		return strings.TrimSuffix(table, "ies") + "y"
	case strings.HasSuffix(table, "sses"):
		return strings.TrimSuffix(table, "es")
	case strings.HasSuffix(table, "ches"), strings.HasSuffix(table, "shes"), strings.HasSuffix(table, "xes"), strings.HasSuffix(table, "zes"):
		return strings.TrimSuffix(table, "es")
	case strings.HasSuffix(table, "s"):
		return strings.TrimSuffix(table, "s")
	default:
		return table
	}
}

// testsItem is the brief's item for the integration tests: what a tenanted application
// proves on its served stack, with what a tenant's directory adds where a tenant can
// have one, and what its absence means where no tenant has.
func (tn Tenancy) testsItem() string {
	if tn.NoDirectories {
		return "Integration tests: the administrator lists both tenants and reads a tenant's digest; the member lists one and gets not-found in the other and in an unknown tenant; the tenant list is global; the record's own create (an `add` on its collection) answers 400 with the sentence saying how a tenant is made, whoever asks; a tenant made by an invitation (the generated `Invite<Auth><Record>` sent by an administrator, the mailed link followed, the first sign-in) exists under an id the acceptance generated, with the invitation's details, is readable at once, without a restart, and names that person its administrator; the invitation is refused a sender without the Create grant on the record and a name a tenant holds, is in the list of the invitations that stand until it is accepted or withdrawn, and its link is dead once withdrawn; a deleted tenant takes the roles held in it, its standing invitations, and its rows with it, in one request; where the auth has a sign-in method through an organization's directory, an invited person accepts through it and is seated, a person of the same organization with no invitation is refused as a stranger while the method is invite only, with no account made, and no route of the API sets a tenant's directory. The tenanted skeleton's `invited_tenant_test.go` files are the pattern. The suites the application had before were written for an application without tenants: the lines above, under what the change could not make, say what becomes of `sign_up_test.go`, `sign_up_role_test.go` and `first_administrator_test.go` where the application carries them, and every other suite follows the harness as it changes."
	}

	return "Integration tests: the administrator lists both tenants and reads a tenant's digest; the member lists one and gets not-found in the other and in an unknown tenant; the tenant list is global; the record's own create (an `add` on its collection) answers 400 with the sentence saying how a tenant is made, whoever asks; a tenant made by an invitation (the generated `Invite<Auth><Record>` sent by an administrator, the mailed link followed, the first sign-in) exists under an id the acceptance generated, with the invitation's details and no directory when the person signed up with a password, is readable at once, without a restart, and names that person its administrator; the invitation is refused a sender without the Create grant on the record and a name a tenant holds, is in the list of the invitations that stand until it is accepted or withdrawn, and its link is dead once withdrawn; a deleted tenant takes the roles held in it, its standing invitations, its directory and its rows with it, in one request, whether it is directory only or not, and its directory is then free for another tenant. The tenanted skeleton's `invited_tenant_test.go` files are the pattern. The suites the application had before were written for an application without tenants: the lines above, under what the change could not make, say what becomes of `sign_up_test.go`, `sign_up_role_test.go` and `first_administrator_test.go` where the application carries them, and every other suite follows the harness as it changes."
}

// declarationItem is the brief's item for what a tenant declares about who may enter
// it: the grants, the seed and the page of an application whose tenants can have a
// directory, or, where no tenant has one, what is left of it: the auth whose people
// create a tenant of their own.
func (tn Tenancy) declarationItem() string {
	if tn.NoDirectories {
		return "Sign-up. No " + tn.oneLower() + " declares who may enter it, so there are no grants to give on a directories record and no Sign-in page to place. The auth whose outlets serve the record, where the application assigns its roles, admits a person who holds no tenant yet (`signin.AdmitWithoutTenant()` in its policy, which this command wrote), since a person who signs up through a sign-in method an administrator opened creates a tenant of their own: the generated create-your-tenant route (`POST <prefix>/create-<record>`) refuses while every method is invite only, which each is until an administrator opens it on the auth's Sign-in page. The login page's sentence for the rule's refusal code (`no_tenant`) is the library's, in `@impulseframework/resource-angular`'s `LOGIN_MESSAGES`."
	}

	return fmt.Sprintf("The declaration. Grants: the tenant's administrator role (a domain role) gets Create (which takes nothing from the request: the server sets the method and the connection from the session), Delete, List and Read on `%[1]s`, and Execute on the four generated methods of the tenant's own: `Make<Record>DirectoryOnly` and `End<Record>DirectoryOnly`, which turn directory only on and off in the tenant (turning it on is held to its rules, the tenant has its directory, the caller is signed in through it and has a password where their auth has passwords, and warns first: the dialog shows who will lose access and has the person type `close`; an administrator of the tenant who has a password does not lose access, and enters with it and a code sent to their email), and `Open<Record>ToDirectory` and `Make<Record>InviteOnly`, which say how the tenant admits its directory's people (opening names the role each of them gets, a role of the tenant the caller could give directly, and warns first: the person types `open`), with List on the list that role is picked from (`<Auth><Record>DirectoryRoles`, the roles of the tenant the caller may give, so the dialog offers what the method takes); the record shows `defaultSeat` and its own update never writes it; and the global administrator role's List and Read on `%[2]s` gain `directoryOnly`, which the record shows and never writes, so the people who may change the declaration are the tenant's own. The seed: a tenant's directory is what seats a stranger at their first sign-in through it and what the tenant can be made only of, so make a directory method the auth already has (a Google Workspace, a Microsoft Entra tenant) one development tenant's in `schema/devseed` (one `%[1]s` row: the method's name and the connection its identities carry; the indexes refuse a second row for the tenant and the same directory on another) where the development world should show either. The page: the copied sign-in page (`components/ui/sign-in`, `tenantSignInRoute()`) goes under the tenant segment beside the tenant's people, and its page (`tenantSignInPage`) in the menu file; it offers a person signed in through an organization's directory to make it the tenant's, and types nothing; the login page's sentences for the rule's refusal codes (`no_tenant`, `connection_not_in_tenant`, `directory_only`) are the library's, in `@impulseframework/resource-angular`'s `LOGIN_MESSAGES`. The auth whose outlets serve the record, where the application assigns its roles, admits a person who holds no tenant yet (`signin.AdmitWithoutTenant()` in its policy, which this command wrote), since a person who signs up through a sign-in method an administrator opened creates a tenant of their own: the generated create-your-tenant route (`POST <prefix>/create-<record>`) refuses while every method is invite only, which each is until an administrator opens it on the auth's Sign-in page, and sets the directory its creator came through on the new tenant, refusing a directory another tenant has.", tn.DirectoryTable(), tn.Table)
}

// makingItem is the brief's item for how a tenant comes to exist: by an invitation, with
// what the invitation says of the tenant's directory where a tenant can have one. rec is
// the tenant record's struct name.
func (tn Tenancy) makingItem(rec string) string {
	if tn.NoDirectories {
		return fmt.Sprintf("How a %[1]s comes to exist. Nobody creates one directly: the record's create answers 400 with a sentence saying so. Staff of the auth whose outlets serve the record, where the application assigns its roles and generates its management, bring an organization in by inviting one person: the generated `Invite<Auth>%[2]s` takes the person's address and the record's fields, each named for the record (`%[3]sName`); that person's first sign-in makes the %[1]s and gives them the roles `%[4]s()` names, in one transaction, however they signed in. `<Auth>Invited%[5]s` lists the invitations that still stand, with the details the caller may list on the record, and `Withdraw<Auth>Invited%[2]s` withdraws one. That auth's identity client is constructed over the generated maker (`<handlers>.%[6]s(<the roster>, <the live service>)`), handed in through the auth's Settings (`TenantMaker`, a nil one refused by `New`), so the data level builds the roster ahead of that auth and `startTenants` only starts it; the did and skipped lines say whether this command wired it or left it. Grants: the global administrator role holds Create on `%[5]s` with the fields an invitation may carry, since sending one is held to the record's own create, and gets Execute on the two methods and List on the list with its fields. The auth's mail links table carries the `NewTenant` column, as the session migration `impulse` renders has it; a table rendered before it gains the column in a migration. A record whose create needs a secret field is refused by the generator while an auth invites, since no invitation carries a secret.", tn.oneLower(), rec, lowerFirst(rec), tn.rolesAccessor(), tn.Table, tn.Maker())
	}

	return fmt.Sprintf("How a %[1]s comes to exist. Nobody creates one directly: the record's create answers 400 with a sentence saying so. Staff of the auth whose outlets serve the record, where the application assigns its roles and generates its management, bring an organization in by inviting one person: the generated `Invite<Auth>%[2]s` takes the person's address and the record's fields, each named for the record (`%[3]sName`), whether the %[1]s is entered through its directory only (`%[3]sDirectoryOnly`) and the role its directory's people start with (`defaultRole`); that person's first sign-in makes the %[1]s, gives them the roles `%[4]s()` names and, where they came through an organization's directory, makes it the %[1]s's own, in one transaction, and an invitation that says directory only is accepted through a directory alone. `<Auth>Invited%[5]s` lists the invitations that still stand, with the details the caller may list on the record, and `Withdraw<Auth>Invited%[2]s` withdraws one. That auth's identity client is constructed over the generated maker (`<handlers>.%[6]s(<the roster>, <the live service>)`), handed in through the auth's Settings (`TenantMaker`, a nil one refused by `New`), so the data level builds the roster ahead of that auth and `startTenants` only starts it; the did and skipped lines say whether this command wired it or left it. Grants: the global administrator role holds Create on `%[5]s` with the fields an invitation may carry, since sending one is held to the record's own create, and gets Execute on the two methods and List on the list with its fields. The auth's mail links table carries the `NewTenant` column, as the session migration `impulse` renders has it; a table rendered before it gains the column in a migration. A record whose create needs a secret field is refused by the generator while an auth invites, since no invitation carries a secret.", tn.oneLower(), rec, lowerFirst(rec), tn.rolesAccessor(), tn.Table, tn.Maker())
}

// Meaning explains tenancy in this framework and names the wiring left to do, in the
// words that hold for an application whose people sign in with a password.
func (tn Tenancy) Meaning() string {
	return tn.meaning(nil)
}

// MeaningIn is Meaning for the application the change was made in: it reads the methods
// the application's auths register and what its test harnesses already build, and says
// what is true of them. Where no auth has the password method, nothing in it speaks of
// a password or of a login made ahead (withoutPasswords).
func (tn Tenancy) MeaningIn(a *app.App) string {
	return tn.meaning(a)
}

// passwordClauses are the clauses of tenancy's account that speak of a password or of a
// login made ahead, each with what is true in an application none of whose auths has
// the password method: nobody signs in, signs up or comes back in with a password, and
// a person the application is developed as is not a login in the bootstrap's file.
var passwordClauses = []struct{ with, without string }{
	{
		"with the invitation's details and no directory when the person signed up with a password,",
		"with the invitation's details and, where the person came through an organization's directory, that directory as its own,",
	},
	{
		"and a session that came by password or with a personal account is refused.",
		"and a session that came through no organization's directory is refused.",
	},
	{
		"an administrator of a directory-only tenant (a holder of one of the auth's administrator roles in that tenant itself, never in every tenant) who signs in with a password is held for the code sent to their email and then enters their own tenant, which is the tenant's way back in when its directory has a problem (who holds such a role in a directory-only tenant, and the account of a person who does, is changed only by a caller whose own session the tenant admits or by a super administrator, so the application's own staff do not give themselves the way in),",
		"a directory-only tenant has no way in beside its directory, since no auth of the application has the password method, so when its directory has a problem a super administrator ends directory only for it (who holds an administrator's role in a directory-only tenant, and the account of a person who does, is changed only by a caller whose own session the tenant admits or by a super administrator),",
	},
	{
		"Turning directory only on is held to three rules by the record's generated method: the tenant has its directory, the caller is signed in through it, and the caller's own account has a password where their auth has the password method (a person who has none chooses one on their account page, with a code sent to their confirmed address);",
		"Turning directory only on is held to two rules by the record's generated method: the tenant has its directory, and the caller is signed in through it (its third rule, on the caller's own account, holds only where their auth has the password method, and no auth of this application has it);",
	},
	{
		"the caller is signed in through it and has a password where their auth has passwords, and warns first: the dialog shows who will lose access and has the person type `close`; an administrator of the tenant who has a password does not lose access, and enters with it and a code sent to their email)",
		"and the caller is signed in through it, and warns first: the dialog shows who will lose access and has the person type `close`; everyone who is not of the directory loses access, the tenant's administrators among them)",
	},
	{
		"BEFORE the logins and before the data level opens",
		"BEFORE anyone is seeded and before the data level opens",
	},
	{
		"so a login's tenant list is read from it",
		"so a person's tenant list is read from it",
	},
	{
		"Give the development logins roles in the tenants (the bootstrap identities file): the administrator under `everyDomain`, and add a member login that holds a role in one tenant only under `domains`, so concealment is observable; name the tenant there by its name, and have the bootstrap find its id through the roster (`TenantRoster().Named(name)`, which refuses a name no tenant or more than one tenant carries) before it writes the membership.",
		"Give the people a development run signs in roles in the tenants. No auth has the password method, so the bootstrap's identities file holds no login: where an auth has a cast (`cast.json` in its package), its personas are those people, and a domain role named among a persona's `roles` is held in every tenant, which is the administrator's. A cast gives no role in one tenant alone, so give a member theirs once the cast is seeded, so concealment is observable: have the bootstrap find the tenant's id through the roster by its name (`TenantRoster().Named(name)`, which refuses a name no tenant or more than one tenant carries) and write the membership there (`DomainPolicyScope`).",
	},
	{
		"so a login with no foothold gets the same not-found an unknown tenant gets. A login's tenant list",
		"so a person with no foothold gets the same not-found an unknown tenant gets. A person's tenant list",
	},
}

// withoutPasswords is tenancy's account for an application none of whose auths has the
// password method: each clause that speaks of a password says what is true there.
func withoutPasswords(text string) string {
	for _, clause := range passwordClauses {
		text = strings.ReplaceAll(text, clause.with, clause.without)
	}

	return text
}

// hasPassword reports whether an auth of the application registers the password method.
// An application whose auths the scan does not read is told the general account.
func hasPassword(a *app.App) bool {
	if a == nil || len(a.Auths) == 0 {
		return true
	}
	for i := range a.Auths {
		for j := range a.Auths[i].Methods {
			if a.Auths[i].Methods[j].Kind == app.MethodPassword {
				return true
			}
		}
	}

	return false
}

// The application's two test harnesses, which tenancy hands on to the author unless
// they build the roster already.
var tenancyHarnesses = []string{"test/authz/harness_test.go", integrationSuite + "/harness_test.go"}

// harnessesBuilt reports whether both of the application's test harnesses build the
// tenant roster already, as the ones a start of impulse new writes for its tenants do.
func (tn Tenancy) harnessesBuilt(a *app.App) bool {
	if a == nil {
		return false
	}
	for _, rel := range tenancyHarnesses {
		src, err := os.ReadFile(a.Abs(rel))
		if err != nil || !strings.Contains(string(src), "New"+tn.Record()+"Roster(") {
			return false
		}
	}

	return true
}

// harnessItem is the brief's item for the test harnesses: what each builds for the
// tenants, as work left where the application's are the base's and as an account where
// they build the roster already.
func (tn Tenancy) harnessItem(a *app.App) string {
	rec := tn.Record()
	if tn.harnessesBuilt(a) {
		return fmt.Sprintf("The test harnesses. Both build the roster already (`%[4]s`, `%[5]s`), written with the application's tests for the sign-in it has, so nothing is left to wire in them: the authorization suite's configurer builds the roster with `New%[1]sRoster` over the test client, adds the generated matrix's tenant to it (`Add(router.TestDomain)`) and answers it as `%[2]s()`, and builds the identity client of the auth that makes tenants over the generated maker (`%[3]s`); the integration harness builds the roster over the test database, with `live.NewFake()` as its signals, starts it once the development seed is applied beside the schema, and hands the auth the maker over it in its Settings (`TenantMaker`). What they wait for is the role a tenant's administrator holds, in the declaration's item below: until a role is named the application's router is not built, so neither suite runs. A member of one tenant, whom concealment is proven with, is yours to add to the integration harness once a role in a tenant exists (`DomainPolicyScope`).", rec, rosterAccessor, tn.Maker(), tenancyHarnesses[0], tenancyHarnesses[1])
	}

	return fmt.Sprintf("The test harnesses. The authorization suite's configurer builds the roster with `New%[1]sRoster` over the test client and adds the generated matrix's tenant to it (`Add(router.TestDomain)`, the constant the generated router exports; the empty test schema holds no tenant row, and `Start` is not needed), and answers it as `%[2]s()`, and where an auth makes tenants it builds that auth's identity client over the generated maker and the roles a tenant's administrator holds (`<auth>identity.NewClient(c.Access(), <the session API>, <handlers>.%[3]s(c.%[2]s(), c.Live()), identity.WithAdministratorRoles(...))`), and the suite's scripted engine answers `TenantAdministratorRoles()` with those roles, which the client of an auth that has passwords asks where it is built, to tell the list of a tenant's people who its administrators are; the integration harness builds it over the test database with `live.NewFake()` as its signals and starts it once the dev seed is applied beside the schema, hands the auth the maker over it in its Settings (`TenantMaker`), names the seeded tenants by the ids the seed gives them, gives the member its assignments in its tenant (`DomainPolicyScope`), and waits for the engine's snapshot to show them.", rec, rosterAccessor, tn.Maker())
}

// meaning is Meaning in the words that hold for the application, or the general account
// where no application is given.
func (tn Tenancy) meaning(a *app.App) string {
	var b strings.Builder
	seg, rec := tn.Segment(), tn.Record()
	fmt.Fprintf(&b, "Tenancy is data. The %[1]s table is the domain universe, a permission domain per row, and `%[2]s`, the global resource over it, is the tenant record (`@tenant`): the generator derives the tenant segment from it, so every struct annotated `@permissionScope(domain)` is served under `/%[3]s/{%[4]sID}/...` with the tenant as its permission domain, and emits `New%[2]sRoster`, the constructor of the tenant roster. The data level builds the roster with it over the database client and the live service's tenants signal and starts it; the roster reads the table once, rereads on the tenants signal (a tenant made or deleted on any instance) and at its backstop, so a new tenant is usable at once, without a restart. The generated DomainGuard asks the roster whether a domain is a tenant, then whether the session may enter it and, since tenant existence is concealed (`WithConcealedDomains`), the caller's foothold next, so a login with no foothold gets the same not-found an unknown tenant gets. A login's tenant list (`user-domains`) is the roster filtered by the tenants where it holds a grant and that admit it, and a grant needs a tenant-scoped resource to land on.\n\n", tn.Table, rec, seg, strcase.ToCamel(rec))
	if tn.NoDirectories {
		fmt.Fprintf(&b, "No %[1]s has a directory in this application (`--tenant-directories=false`): the resource package declares no directories record and `%[2]s` carries no directory-only switch, so nothing is taken when a %[1]s is made, no route sets a %[1]s's directory, no %[1]s is opened to a directory or made directory only, and the browser has no %[1]s Sign-in page. A %[1]s's people come by invitation alone. A Google, Microsoft or other provider's sign-in method whose roles the application gives (`impulse add method`) is a way for an invited person to sign in and nothing more: a person from an organization's directory who has no account and no invitation joins no %[1]s, whichever methods the application has or gets (refused as a stranger while the method is invite only, as every method starts; where an administrator opened it to sign-up, given an account that holds no %[1]s), and `impulse audit` reads the application as an invite-only shape. Each auth's `New` still sets the library's sign-in rule (`signin.TenantPolicy`, with the memberships read through `resource.SignInRoles`) over the generated reader `%[3]s`, which answers nothing for every %[1]s: a %[1]s admits everyone it seats, however they signed in, a super administrator and a person holding a role outside any one %[1]s (a global role, or one held in every %[1]s) are admitted on the library's checks, and a person holding no %[1]s is refused (`no_tenant`) on every auth but the one whose people create a %[1]s of their own (below). The data level's `%[4]s()` is handed to `resource.SessionPermissions` as in an application whose tenants have directories, and answers nothing for every %[1]s. Giving the application tenant directories later is the author's act and no command's: the directories record (`@tenantDirectory`) and its table with its two unique indexes, the directory-only column on `%[2]s` (`@directoryOnly`), each auth's directory reader and admission (`SetDirectoryAdmission`), the grants on the generated methods and the %[1]s's Sign-in page, as an application made with tenant directories has them (the tenanted skeleton); no `impulse add method` does it.\n\n", tn.oneLower(), rec, tn.DeclarationReader(), declarationsAccessor)
	} else {
		fmt.Fprintf(&b, "Who may enter a tenant is the record's declaration, data of the application rather than deploy configuration: the directory its people sign in through (`%[1]s`, one row at most under `/%[2]s/{%[3]sID}/%[4]s`: a sign-in method and the connection its identities carry, a Google Workspace by its domain or a Microsoft Entra tenant by its tenant id) and whether its people enter through it only (`%[5]s.DirectoryOnly`, which closes the tenant to every sign-in but one through its directory). A tenant has one directory and a directory belongs to one tenant: the table's two unique indexes hold both, and the generated write paths refuse first, each with a sentence the person can act on. Nobody types a directory in: the record's create takes no method and no connection and sets the directory the caller is signed in through, so the tenant's administrator sets it while signed in through their organization, and a session that came by password or with a personal account is refused. The generator emits `%[6]s`, the reader of a tenant's declaration in the sign-in's transaction, and each auth's `New` sets the library's sign-in rule over it (`signin.TenantPolicy`, with the memberships read through `resource.SignInRoles`): a tenant's declaration restricts its own door alone, so a tenant that is not directory only admits everyone it seats, however they signed in, a directory-only tenant admits a sign-in through its own directory and nothing else, and a person is refused only when every tenant that seats them is closed to the way they signed in; a super administrator is admitted whatever a tenant declares, an administrator of a directory-only tenant (a holder of one of the auth's administrator roles in that tenant itself, never in every tenant) who signs in with a password is held for the code sent to their email and then enters their own tenant, which is the tenant's way back in when its directory has a problem (who holds such a role in a directory-only tenant, and the account of a person who does, is changed only by a caller whose own session the tenant admits or by a super administrator, so the application's own staff do not give themselves the way in), a person holding a role outside any one tenant (a global role, or one held in every tenant) is admitted and then enters a tenant only where its declaration admits their session, and a person holding no tenant is refused (`no_tenant`) on every auth but the one whose people create a tenant of their own (below). Which tenants the session then reaches is decided again per tenant where its tenant list is computed, and a tenant that seats the person and is closed to their session stays in the list marked closed, with the reason: the roster keeps every tenant's declaration beside its key (`Declarations`, the data level's `%[7]s()`, handed to `resource.SessionPermissions`), the records' write paths reload it after their commit and signal the other instances, and the records' change events are the audit of every edit. Turning directory only on is held to three rules by the record's generated method: the tenant has its directory, the caller is signed in through it, and the caller's own account has a password where their auth has the password method (a person who has none chooses one on their account page, with a code sent to their confirmed address); the read `GET /directory-only?domain=<tenant>` says what it would do, the sentence and the people it names, before a page saves it.\n\n", tn.DirectoryRecord(), seg, strcase.ToCamel(rec), tn.DirectorySegment(), rec, tn.DeclarationReader(), declarationsAccessor)
	}
	b.WriteString("Left to wire, in this order:\n\n")
	signInMenu := fmt.Sprintf(" and %s (Sign-in settings, `tenantSignInPage`)", tn.one())
	if tn.NoDirectories {
		signInMenu = ", and no menu for Sign-in settings, since no " + tn.oneLower() + " has a directory to set there"
	}
	items := []string{
		"The bootstrap and the deployment. Seed the development tenants (`schema/devseed`, a data migration applied with the migrator's data step) BEFORE the logins and before the data level opens, since the roster's start reads the table; the App's `UserPermissions` passes the roster's `Domains` (`a.tenants.Domains`) to `resource.SessionPermissions` so a login's tenant list is read from it. The roles need no provisioning per tenant, since a membership held in every domain (`EveryDomainPolicyScope`) reaches every tenant the roster names. A tenant's key is an id generated when the tenant is made, so the seed inserts each development tenant under a fixed id of its own (any id's text, written once) with its name, and nothing else types one. Give the development logins roles in the tenants (the bootstrap identities file): the administrator under `everyDomain`, and add a member login that holds a role in one tenant only under `domains`, so concealment is observable; name the tenant there by its name, and have the bootstrap find its id through the roster (`TenantRoster().Named(name)`, which refuses a name no tenant or more than one tenant carries) before it writes the membership.",
		"Tenant-scoped resources. Decide which existing resources belong to a tenant: give each a `TenantId` column referencing the tenant table with `ON DELETE CASCADE` (`STRING(36)`, the tenant's id; a migration, with a data step assigning existing rows to a tenant; the delete rule is what takes a tenant's rows with the tenant, so a tenant is deleted in one request, and a tenant-scoped table without it refuses the delete of a tenant that still holds one of its rows, which is why `tenant-delete-rule` fails on a table that carries the tenant's key without it), type the field `impulse.UUID`, annotate the struct `@permissionScope(domain)` and the column `// @domain`, then run `go generate ./...`. A table that reaches its tenant through another row (`// @domain(via: ...)` on the column that names that row) carries `ON DELETE CASCADE` on that column's foreign key too, as every link of its way to the tenant table does, and the same check fails on the first link that lacks it. If none of the application's resources is tenant-scoped yet, add a first one so the option is observable from the first sign-in; the reference has one. `tenancy-wired` requires at least one.",
		tn.harnessItem(a),
		tn.testsItem(),
		fmt.Sprintf("The browser app mirrors the API's shape, in the library's frame (`provideFrame`, `@impulseframework/resource-angular/auth-frame`, which provides the %[1]s the URL names as every library page's tenant), and this command wrote it where the files were as the skeleton rendered them; the did and skipped lines say which. The routes: the %[2]s segment (`%[3]s/:%[4]sID`, the generated `DomainRoutePath`) with the library's `tenantGuard` on it, the refusal page ahead of it (`refusedRoute(DomainRoutePath)`), the place outside every %[1]s (`outsideGuard`) routed before the segment so the record's own page (`%[3]s/<id>`) is reached, and `homeGuard` at the root, with the dashboard, every %[1]s-scoped page and `frameRoutes()` under the segment and the pages of the place outside every %[1]s beside it, All %[5]s among them (`resourceRoutes(tenantsConfig, resourceMeta)`); routes edited since the skeleton rendered them were left, and take this shape by hand, as the tenanted skeleton's routes have it. The menu file: the %[6]s menu (All %[5]s, `resourcePage(tenantsConfig, resourceMeta)`; the list draws no Create button, since nobody creates a %[1]s directly, and the page staff bring a %[1]s in from by inviting one person, Invited %[5]s, is the library's, among the auth's pages for the whole application that `identityMenuPages` with `scope: 'global'` places), People (the %[1]s's side of the auth's people, `identityMenuPages` with `scope: 'domain'`)%[7]s, each showing in its place only; a %[1]s-scoped page the routes gain goes in a menu of its own or one of these, and the menu file's test names a page the routes carry and no menu places. Where staff bring a %[1]s in through the console's auth, that test, where it lists the pages the System menu places as the skeleton's does, gains Invited %[5]s after Mailed links. The configuration: the place outside every %[1]s is Configuration with a gear (`outside` in `provideFrame`), which the application may rename. The frame's place switcher shows itself once the session has %[5]s and opens the same page under the chosen one, and the tenant word on screen is the generator's, from the record. The dashboard is the application's: it shows the global digest in every place until it reads the %[1]s the page is open in (`TenantService`), as the tenanted skeleton's does, and it offers a person who holds no %[1]s the copied dialog (`openNewTenantDialog(dialog)`), as the tenanted skeleton's card does, since a person who signs up creates their own. The files this command wrote carry the record's and the auth's own names and were not wrapped again, since the formatter is the prettier the web root pins and exists only once its packages are installed: after the install, run `impulse check --only prettier --fix` once, which wraps again any line those names made longer or shorter and changes nothing where they left every line as it was; a file that needs it fails the `prettier` check until then.", tn.oneLower(), tn.oneLower(), tn.Segment(), lowerFirst(tn.Record()), tn.manyLower(), tn.many(), signInMenu),
		tn.declarationItem(),
		tn.makingItem(rec),
	}
	if tn.OneTenantPerPerson {
		given := "by hand, by an invitation sent or accepted, or by a directory's default role"
		if tn.NoDirectories {
			given = "by hand or by an invitation sent or accepted"
		}
		items = append(items, fmt.Sprintf("One tenant per person. This command declared it where it could, and the did and skipped lines say where: `%[1]s` among the options of the identity client of every auth whose roles the application assigns. A role in a second %[2]s is then refused however it would be given, %[3]s, with a refusal that never names the %[2]s that holds the person; the application's own people, who hold a role for the whole application or in every %[2]s, are outside the rule. The authorization suite's configurer builds the same client, so it passes the option too, and the integration suite proves the rule: a member of one %[2]s is refused a role in the other, and an invitation into it.", oneTenantOption, tn.oneLower(), given))
	}
	for i, item := range items {
		fmt.Fprintf(&b, "%d. %s\n", i+1, item)
	}
	b.WriteString("\nData consequence: every row of a resource that becomes tenant-scoped needs a tenant. Write the assignment as a migration a person can review, never a default nobody chose.\n")
	if !hasPassword(a) {
		return withoutPasswords(b.String())
	}

	return b.String()
}
