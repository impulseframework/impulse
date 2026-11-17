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
// roster and starts it, and the app exposes it to the generated code. Which resources
// become tenant-scoped, how existing rows are assigned, the bootstrap order, the tests,
// the tenant segment in the browser's routes, and the tenant menus in its menu file are
// the agent's.
type Tenancy struct {
	// Table is the tenant-record table, PascalCase and plural: Tenants. The domain route
	// segment is its kebab-case form (tenants), and the resource struct its singular.
	Table string
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
// from: its New tenant dialog creates a tenant both ways, an administrator through the
// record and a person who signed up through the create-your-tenant route.
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
// tenant record), and the New tenant dialog a tenant is created through: by an
// administrator holding Create on the record, and by a person who signed up and holds
// no tenant yet. The library's frame carries the rest: the tenant the URL names, the
// guard on the tenant segment, the refusal page and the place switcher. The tenant segment
// in the routes and the menus in the menu file are the agent's.
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
	return fmt.Sprintf("impulse add tenancy --tenant-table %s", tn.Table)
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
	if err := tn.writeDirectoryRecord(a, g.ResourcePackageDir, ch); err != nil {
		return nil, err
	}
	if err := tn.editConfig(a, g, ch); err != nil {
		return nil, err
	}
	if err := tn.editAuths(a, site, g, ch); err != nil {
		return nil, err
	}
	if err := tn.editApp(a, ch); err != nil {
		return nil, err
	}
	if err := tn.writeBrowser(a, site, ch); err != nil {
		return nil, err
	}

	if err := generate(ctx, a, exec, ch, "go generate ./... failed, so the tenant routes are not generated yet; fix the cause and run it:", fmt.Sprintf("ran go generate ./..., which emitted the %s and %s resources, the tenant segment pair under /%s/{%sID}, the roster constructor New%sRoster and the declaration reader %s", tn.Record(), tn.DirectoryRecord(), tn.Segment(), strcase.ToCamel(tn.Record()), tn.Record(), tn.DeclarationReader())); err != nil {
		return nil, err
	}

	return ch, nil
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

// writeMigration adds the tenant table as the next migration.
func (tn Tenancy) writeMigration(a *app.App, dir string, ch *Change) error {
	n, err := nextMigration(a.Abs(dir))
	if err != nil {
		return err
	}
	base := fmt.Sprintf("%06d_%s", n, tn.Table)
	up := fmt.Sprintf(`-- A tenant's key is an id the create path generates, never a word a person types: it is
-- written into every row the tenant owns and is the domain of every role held in it,
-- so it never changes and is never used twice. The name is what people call the
-- tenant, changed like any field; the unique index below keeps two tenants from
-- carrying one name, and an application that allows that drops it.
CREATE TABLE %[1]s (
  Id STRING(36) NOT NULL,
  Name STRING(MAX) NOT NULL,
  -- The tenant's people enter through its directory only: no other sign-in reaches a
  -- tenant whose flag is true, a password included. Half of what the tenant declares
  -- about who may enter it; the other half is its row of %[2]s.
  DirectoryOnly BOOL NOT NULL DEFAULT (FALSE),

  CONSTRAINT CK_%[1]s_Id CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')),
) PRIMARY KEY (Id);

CREATE UNIQUE INDEX %[1]sByName ON %[1]s(Name);

-- A tenant's directory, the organization its people sign in through: the sign-in method
-- (google, azure, workos, oidc) and the connection the method's identities carry (a
-- Google Workspace by its domain, a Microsoft Entra tenant by its tenant id, an
-- organization's own OpenID Connect provider by its registration's). Nobody types a
-- row in: it is written from a sign-in through the directory, when the tenant is
-- created or when its administrator, signed in through it, makes it the tenant's.
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
	down := fmt.Sprintf("DROP INDEX %[2]sByMethodConnection;\nDROP INDEX %[2]sBy%[3]sId;\nDROP TABLE %[2]s;\nDROP INDEX %[1]sByName;\nDROP TABLE %[1]s;\n", tn.Table, tn.DirectoryTable(), tn.Record())
	if err := writeNew(a, path.Join(dir, base+".up.sql"), up); err != nil {
		return err
	}
	if err := writeNew(a, path.Join(dir, base+".down.sql"), down); err != nil {
		return err
	}
	ch.didf("%s/%s.up.sql and .down.sql: the %s table (a generated id as its primary key, a unique Name, and DirectoryOnly) and the %s table (a tenant's directory with its DefaultSeat, held to one for a tenant and to one tenant by its two unique indexes)", dir, base, tn.Table, tn.DirectoryTable())

	return nil
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
		ch.skipf("%s already exists, so the %s resource struct was not written; make sure it is a global @resource annotated @tenant and @order, keyed by an id its create path generates (ID impulse.UUID), with a Name marked @tenantName", rel, tn.Record())

		return nil
	}
	pkg := packageNameOf(a, resourceDir)
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
	// instance by this record's generated write paths, so a tenant created here is
	// usable at once, without a restart.
	//
	// The primary key is an id the create path generates, as every record's is, and the
	// generator refuses any other key on the tenant record: the id is written into
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
	// Who may enter a tenant is the record's declaration, data of the application
	// rather than deploy configuration: the directory its people sign in through
	// (%[5]s, one row at most, set from a sign-in through the directory and never
	// typed in) and whether its people enter through it only
	// (DirectoryOnly). The sign-in rule reads it in the sign-in's own transaction
	// (%[6]s, handed to each auth) and every instance's roster keeps a copy beside the
	// keys (New%[2]sRoster, Declarations), which the record's and the directories'
	// generated write paths reload after their commit; a tenant is offered to a session
	// only when the session may enter it. The record's change events (DataChangeEvents,
	// Config) are the audit of every edit.
	//
	// @resource
	// @tenant
	// @order(Name asc)
	%[2]s struct {
		ID impulse.UUID `+"`spanner:\"Id\"`"+`
		// Name is the tenant's name as its people know it: the session's tenant list
		// carries it, so the place switcher shows it to them.
		// @tenantName
		Name string `+"`spanner:\"Name\"`"+`
		// DirectoryOnly says the tenant's people enter through its directory only: no
		// other sign-in reaches the tenant, and the flag closes this tenant alone. The
		// switch is the tenant's own: the tenant's administrator turns it with the
		// generated method in the tenant's scope (Set<Record>DirectoryOnly), which
		// refuses turning it on until the tenant has its directory, and unless the caller
		// is signed in through it. The record shows the flag and its own write paths
		// never write it, at creation or after: a new tenant declares nothing.
		// @directoryOnly
		DirectoryOnly bool `+"`spanner:\"DirectoryOnly\"`"+`
	}
)

// Config enables change tracking: %[2]s mutations write DataChangeEvents rows in the
// same transaction, the audit of every change to what a tenant declares.
func (%[2]s) Config() resource.Config {
	return %[7]s
}
`, pkg, tn.Record(), tn.Segment(), strcase.ToCamel(tn.Record()), tn.DirectoryRecord(), tn.DeclarationReader(), trackingConfig(a, resourceDir))
	if err := writeNew(a, rel, src); err != nil {
		return err
	}
	ch.didf("%s: the %s resource struct, global, keyed by a generated id, listed in name order (@order(Name asc)), with its declaration's flag (DirectoryOnly) and change tracking", rel, tn.Record())

	return nil
}

// editConfig gives the data level the tenant roster: a field on DataConfiguration, a new
// file whose startTenants builds the roster with the record's generated constructor and
// starts it and whose TenantRoster accessor hands it to the app, and the start inserted
// where the configuration is built.
func (tn Tenancy) editConfig(a *app.App, g *app.Generator, ch *Change) error {
	handlersPath := a.GoMod.Module.Mod.Path + "/" + g.HandlersDir()
	handlersPkg := packageNameOf(a, g.HandlersDir())
	constructor := handlersPkg + ".New" + tn.Record() + "Roster"
	rel, src, mode, err := findDeclaringFile(a, "DataConfiguration")
	if err != nil {
		return err
	}
	if rel == "" {
		ch.skipf("no file declares a DataConfiguration struct, so the tenant roster was not added to the data level; build it where the database is opened with %s(client, resource.WithTenantSignals(<the live service>)), start it (Start) and fail the start on its error, and expose it as %s()", constructor, rosterAccessor)

		return nil
	}
	client, err := clientExpression(rel, src)
	if err != nil {
		return err
	}
	if client == "" {
		ch.skipf("%s: DataConfiguration holds no *resource.SpannerClient and no *spanner.Client field the roster could read through, so the tenant roster was not added; build it with %s over the database client, start it, and expose it as %s()", rel, constructor, rosterAccessor)

		return nil
	}
	liveField, err := app.StructFieldOfType(rel, src, "DataConfiguration", liveFirestoreImport, "Service")
	if err != nil {
		return err
	}
	pkg, err := app.PackageName(rel, src)
	if err != nil {
		return err
	}
	gained, err := addRosterField(a, rel, src, mode, ch)
	if err != nil {
		return err
	}
	authFields, err := authFieldsOf(a, rel, src)
	if err != nil {
		return err
	}
	tenancyFile := path.Join(path.Dir(rel), "tenancy.go")
	source, err := tn.configSource(pkg, handlersPath, handlersPkg, client, liveField, authFields)
	if err != nil {
		return err
	}
	if err := writeNew(a, tenancyFile, source); err != nil {
		return err
	}
	if liveField == "" {
		ch.skipf("%s: DataConfiguration holds no *firestore.Service field (resource/live/firestore), so the roster is built without resource.WithTenantSignals and reloads at its backstop alone; pass the live service the level opens, so a tenant created on another instance reaches this one at once", tenancyFile)
	}
	ch.didf("%s: the tenant roster (%s builds it with %s and starts it), %s() and %s() on DataConfiguration; %s gained %s", tenancyFile, rosterStart, constructor, rosterAccessor, declarationsAccessor, rel, gained)

	return nil
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
// person in, and the accessor the app's Configurer asks for. The source is formatted, so
// the imports sort whatever the handlers package's path is.
func (tn Tenancy) configSource(pkg, handlersPath, handlersPkg, client, liveField string, authFields []string) (string, error) {
	alias := ""
	if handlersPkg != path.Base(handlersPath) {
		alias = handlersPkg + " "
	}
	construction := fmt.Sprintf("c.tenants = %s.New%sRoster(%s, resource.WithTenantSignals(c.%s))", handlersPkg, tn.Record(), client, liveField)
	if liveField == "" {
		construction = fmt.Sprintf("// No live service field was found on DataConfiguration, so the roster reloads at\n"+
			"\t// its backstop alone; pass resource.WithTenantSignals(<the live service>) so a tenant\n"+
			"\t// created on another instance reaches this one at once.\n"+
			"\tc.tenants = %s.New%sRoster(%s)", handlersPkg, tn.Record(), client)
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

	%[2]s"%[3]s"
	"github.com/impulseframework/impulse/resource"
	"github.com/go-playground/errors/v5"
)

// startTenants builds the tenant roster over the %[4]s record with the generated
// constructor and starts it: the roster reads the %[5]s table once, fails the start when
// it cannot, and keeps the set current until ctx ends, rereading on the tenants signal
// (a tenant created or deleted on any instance, published by the record's generated
// write paths) and at its backstop. The generated DomainGuard asks it before a
// tenant-scoped request runs, so a new tenant is usable at once, without a restart.
func (c *DataConfiguration) startTenants(ctx context.Context) error {
	%[6]s
	if err := c.tenants.Start(ctx); err != nil {
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

// TenantDeclarations reads what a tenant declares about who may enter it from the
// roster's copy, reloaded on every write of the %[4]s record or its directories: the
// request-time half of the declaration, which filters a session's tenant list. The
// sign-in half reads the same declaration in the sign-in's transaction, through the
// generated reader each auth's settings carry (%[7]s).
func (c *DataConfiguration) TenantDeclarations() resource.TenantDeclarations {
	return c.tenants.Declarations
}
`, pkg, alias, handlersPath, tn.Record(), tn.Table, construction, tn.DeclarationReader(), naming)
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return "", errors.Wrap(err, "format.Source()")
	}

	return string(formatted), nil
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
		ch.skipf("no auth whose roles the application assigns constructs a session.Auth the scan reads, so the roles the %s record's create path seats a tenant's creator with were not exposed on the app; the generated contract asks the handlers for %s() []accesstypes.Role (the domain roles the creator holds in the new tenant, the auth's AdministratorRoles held in every tenant) beside the auth's identity client (<Auth>Identity()); add them where the App is declared", tn.Record(), tn.rolesAccessor())
		ch.didf("%s: TenancyConfigurer (embedded in Configurer, asking %s() and %s()) and App.%s(); %s gained the fields and their assignments", tenancyFile, rosterAccessor, declarationsAccessor, rosterAccessor, rel)
	} else {
		ch.didf("%s: TenancyConfigurer (embedded in Configurer, asking %s() and %s()), App.%s() and App.%s() (the %s auth's AdministratorRoles held in every tenant: the domain roles the %s record's create path seats a tenant's creator with, through the auth's identity client, which the generated contract asks the handlers for); %s gained the fields and their assignments", tenancyFile, rosterAccessor, declarationsAccessor, rosterAccessor, tn.rolesAccessor(), seatPkg.Name, tn.Record(), rel)
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
func (Tenancy) editPermissions(a *app.App, rel string, ch *Change) error {
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
		edited = withDeclarations
		handed = "the roster's Domains and the declarations"
	}
	if err := os.WriteFile(a.Abs(rel), edited, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: UserPermissions passes %s to resource.SessionPermissions", rel, handed)

	return nil
}

// rolesAccessor is the generated contract's accessor of the roles the tenant record's
// create path seats a tenant's creator with: <Record>AdministratorRoles.
func (tn Tenancy) rolesAccessor() string {
	return tn.Record() + "AdministratorRoles"
}

// seatAuth picks the auth whose generated management the tenant record's create path
// gives a tenant's creator the roles through: the first auth package whose Auth the scan
// reads and whose roles the application assigns (an auth whose directory names the
// roles seats nobody). Nil when none qualifies.
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
// %[1]s names the domain roles the person who creates a %[2]s holds in it from
// then on, seated by the %[3]s record's create path once the row commits: the %[4]s
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
	// %[4]s reads what a tenant declares about who may enter it, as the running
	// application knows it: the roster's copy, reloaded on every write of the tenant
	// record or its directories. UserPermissions hands it to resource.SessionPermissions,
	// so a tenant that is not directory only is offered to every session of a person it
	// seats, and a directory-only tenant to a session that came through its own
	// directory and no other.
	%[4]s() resource.TenantDeclarations
}

// %[3]s returns the application's tenant roster, the generated contract's accessor
// (domainScopedApp); UserPermissions hands its Domains to resource.SessionPermissions, so
// a session's tenant list is the roster filtered by the principal's footholds.
func (a *App) %[3]s() *resource.TenantRoster {
	return a.tenants
}
%[5]s`, pkg, imports, rosterAccessor, declarationsAccessor, roles)
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

// Meaning explains tenancy in this framework and names the wiring left to do.
func (tn Tenancy) Meaning() string {
	var b strings.Builder
	seg, rec := tn.Segment(), tn.Record()
	fmt.Fprintf(&b, "Tenancy is data. The %[1]s table is the domain universe, a permission domain per row, and `%[2]s`, the global resource over it, is the tenant record (`@tenant`): the generator derives the tenant segment from it, so every struct annotated `@permissionScope(domain)` is served under `/%[3]s/{%[4]sID}/...` with the tenant as its permission domain, and emits `New%[2]sRoster`, the constructor of the tenant roster. The data level builds the roster with it over the database client and the live service's tenants signal and starts it; the roster reads the table once, rereads when the record's generated write paths publish the tenants signal (a tenant created or deleted on any instance) and at its backstop, so a new tenant is usable at once, without a restart. The generated DomainGuard asks the roster whether a domain is a tenant, then whether the session may enter it and, since tenant existence is concealed (`WithConcealedDomains`), the caller's foothold next, so a login with no foothold gets the same not-found an unknown tenant gets. A login's tenant list (`user-domains`) is the roster filtered by the tenants where it holds a grant and that admit it, and a grant needs a tenant-scoped resource to land on.\n\n", tn.Table, rec, seg, strcase.ToCamel(rec))
	fmt.Fprintf(&b, "Who may enter a tenant is the record's declaration, data of the application rather than deploy configuration: the directory its people sign in through (`%[1]s`, one row at most under `/%[2]s/{%[3]sID}/%[4]s`: a sign-in method and the connection its identities carry, a Google Workspace by its domain or a Microsoft Entra tenant by its tenant id) and whether its people enter through it only (`%[5]s.DirectoryOnly`, which closes the tenant to every sign-in but one through its directory). A tenant has one directory and a directory belongs to one tenant: the table's two unique indexes hold both, and the generated write paths refuse first, each with a sentence the person can act on. Nobody types a directory in: the record's create takes no method and no connection and sets the directory the caller is signed in through, so the tenant's administrator sets it while signed in through their organization, and a session that came by password or with a personal account is refused. The generator emits `%[6]s`, the reader of a tenant's declaration in the sign-in's transaction, and each auth's `New` sets the library's sign-in rule over it (`signin.TenantPolicy`, with the memberships read through `resource.SignInRoles`): a tenant's declaration restricts its own door alone, so a tenant that is not directory only admits everyone it seats, however they signed in, a directory-only tenant admits a sign-in through its own directory and nothing else, and a person is refused only when every tenant that seats them is closed to the way they signed in; a super administrator is admitted whatever a tenant declares, a person holding a role outside any one tenant (a global role, or one held in every tenant) is admitted and then enters a tenant only where its declaration admits their session, and a person holding no tenant is refused (`no_tenant`) on every auth but the one whose people create a tenant of their own (below). Which tenants the session then reaches is decided again per tenant where its tenant list is computed, and a tenant that seats the person and is closed to their session stays in the list marked closed, with the reason: the roster keeps every tenant's declaration beside its key (`Declarations`, the data level's `%[7]s()`, handed to `resource.SessionPermissions`), the records' write paths reload it after their commit and signal the other instances, and the records' change events are the audit of every edit. Turning directory only on is held to two rules by the record's generated update: the tenant has its directory, and the caller is signed in through it; the read `GET /directory-only?domain=<tenant>` says what it would do, the sentence and the people it names, before a page saves it.\n\n", tn.DirectoryRecord(), seg, strcase.ToCamel(rec), tn.DirectorySegment(), rec, tn.DeclarationReader(), declarationsAccessor)
	b.WriteString("Left to wire, in this order:\n\n")
	items := []string{
		"The bootstrap and the deployment. Seed the development tenants (`schema/devseed`, a data migration applied with the migrator's data step) BEFORE the logins and before the data level opens, since the roster's start reads the table; the App's `UserPermissions` passes the roster's `Domains` (`a.tenants.Domains`) to `resource.SessionPermissions` so a login's tenant list is read from it. The roles need no provisioning per tenant, since a membership held in every domain (`EveryDomainPolicyScope`) reaches every tenant the roster names. A tenant's key is an id the record's create path generates, so the seed inserts each development tenant under a fixed id of its own (any id's text, written once) with its name, and nothing else types one. Give the development logins roles in the tenants (the bootstrap identities file): the administrator under `everyDomain`, and add a member login that holds a role in one tenant only under `domains`, so concealment is observable; name the tenant there by its name, and have the bootstrap find its id through the roster (`TenantRoster().Named(name)`, which refuses a name no tenant or more than one tenant carries) before it writes the membership.",
		"Tenant-scoped resources. Decide which existing resources belong to a tenant: give each a `TenantId` column referencing the tenant table (`STRING(36)`, the tenant's id; a migration, with a data step assigning existing rows to a tenant), type the field `impulse.UUID`, annotate the struct `@permissionScope(domain)` and the column `// @domain`, then run `go generate ./...`. If none of the application's resources is tenant-scoped yet, add a first one so the option is observable from the first sign-in; the reference has one. `tenancy-wired` requires at least one.",
		fmt.Sprintf("The test harnesses. The authorization suite's configurer builds the roster with `New%[1]sRoster` over the test client and adds the generated matrix's tenant to it (`Add(router.TestDomain)`, the constant the generated router exports; the empty test schema holds no tenant row, and `Start` is not needed), and answers it as `%[2]s()`; the integration harness builds it over the test database with `live.NewFake()` as its signals and starts it once the dev seed is applied beside the schema, names the seeded tenants by the ids the seed gives them, gives the member its assignments in its tenant (`DomainPolicyScope`), and waits for the engine's snapshot to show them.", rec, rosterAccessor),
		"Integration tests: the administrator lists both tenants and reads a tenant's digest; the member lists one and gets not-found in the other and in an unknown tenant; the tenant list is global; a tenant created through the API by its name alone (an `add` on the record's collection, answered with the id the create path generated) is readable at once, without a restart, and names its creator its administrator; a deleted tenant takes the roles held in it and its standing invitations with it.",
		fmt.Sprintf("The browser app mirrors the API's shape, in the library's frame (`provideFrame`, `@impulseframework/resource-angular/auth-frame`, which provides the %[1]s the URL names as every library page's tenant), and this command wrote it where the files were as the skeleton rendered them; the did and skipped lines say which. The routes: the %[2]s segment (`%[3]s/:%[4]sID`, the generated `DomainRoutePath`) with the library's `tenantGuard` on it, the refusal page ahead of it (`refusedRoute(DomainRoutePath)`), the place outside every %[1]s (`outsideGuard`) routed before the segment so the record's own page (`%[3]s/<id>`) is reached, and `homeGuard` at the root, with the dashboard, every %[1]s-scoped page and `frameRoutes()` under the segment and the pages of the place outside every %[1]s beside it, All %[5]s among them (`resourceRoutes(tenantsConfig, resourceMeta)`); routes edited since the skeleton rendered them were left, and take this shape by hand, as the tenanted skeleton's routes have it. The menu file: the %[6]s menu (All %[5]s, `resourcePage(tenantsConfig, resourceMeta)`, and New %[1]s…, an action opening the copied dialog in the place outside every %[1]s under Create on the record), People (the %[1]s's side of the auth's people, `identityMenuPages` with `scope: 'domain'`) and %[7]s (Sign-in settings, `tenantSignInPage`), each showing in its place only; a %[1]s-scoped page the routes gain goes in a menu of its own or one of these, and the menu file's test names a page the routes carry and no menu places. The configuration: the place outside every %[1]s is Configuration with a gear (`outside` in `provideFrame`), which the application may rename. The frame's place switcher shows itself once the session has %[5]s and opens the same page under the chosen one, and the tenant word on screen is the generator's, from the record. The dashboard is the application's: it shows the global digest in every place until it reads the %[1]s the page is open in (`TenantService`), as the tenanted skeleton's does, and it offers a person who holds no %[1]s the copied dialog on its create-your-own path (`openNewTenantDialog(dialog, { selfService: true })`), as the tenanted skeleton's card does, since a person who signs up creates their own. The files this command wrote carry the record's and the auth's own names and were not wrapped again, since the formatter is the prettier the web root pins and exists only once its packages are installed: after the install, run `impulse check --only prettier --fix` once, which formats them; until then the `prettier` check names them.", tn.oneLower(), tn.oneLower(), tn.Segment(), lowerFirst(tn.Record()), tn.manyLower(), tn.many(), tn.one()),
		fmt.Sprintf("The declaration. Grants: the tenant's administrator role (a domain role) gets Create (which takes no method and no connection: the server sets both from the session), Update (`defaultSeat`), Delete, List and Read on `%[1]s`, and Execute on the generated method that turns directory only on and off in the tenant (`Set<Record>DirectoryOnly`, held to its rules: the tenant has its directory, and the caller is signed in through it), and the global administrator role's List and Read on `%[2]s` gain `directoryOnly`, which the record shows and never writes, so the people who may change the declaration are the tenant's own. The seed: a tenant's directory is what seats a stranger at their first sign-in through it and what the tenant can be made only of, so make a directory method the auth already has (a Google Workspace, a Microsoft Entra tenant) one development tenant's in `schema/devseed` (one `%[1]s` row: the method's name and the connection its identities carry; the indexes refuse a second row for the tenant and the same directory on another) where the development world should show either. The page: the copied sign-in page (`components/ui/sign-in`, `tenantSignInRoute()`) goes under the tenant segment beside the tenant's people, and its page (`tenantSignInPage`) in the menu file; it offers a person signed in through an organization's directory to make it the tenant's, and types nothing; the login page's sentences for the rule's refusal codes (`no_tenant`, `connection_not_in_tenant`, `directory_only`) are the library's, in `@impulseframework/resource-angular`'s `LOGIN_MESSAGES`. The auth whose outlets serve the record, where the application assigns its roles, admits a person who holds no tenant yet (`signin.AdmitWithoutTenant()` in its policy, which this command wrote), since a person who signs up through a sign-in method an administrator opened creates a tenant of their own: the generated create-your-tenant route (`POST <prefix>/create-<record>`) refuses while every method is invite only, which each is until an administrator opens it on the auth's Sign-in page, and sets the directory its creator came through on the new tenant, refusing a directory another tenant has.", tn.DirectoryTable(), tn.Table),
	}
	for i, item := range items {
		fmt.Fprintf(&b, "%d. %s\n", i+1, item)
	}
	b.WriteString("\nData consequence: every row of a resource that becomes tenant-scoped needs a tenant. Write the assignment as a migration a person can review, never a default nobody chose.\n")

	return b.String()
}
