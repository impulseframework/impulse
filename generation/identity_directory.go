package generation

import (
	"go/ast"
	"go/build"
	goparser "go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"

	"github.com/go-playground/errors/v5"
)

// What a tenant's list of its people says of its directory-only switch. A tenant that
// is directory only admits the people who sign in through its own directory and nobody
// else, so the list of its people carries, for each person, whether they have a sign-in
// through that directory, and while the switch is on the row of a person who has none
// says so. The same fact, asked before the switch is on, is who turning it on would
// keep out: the generated identity client answers it (DirectoryOutsiders) for the
// warning of the method that turns the switch on, which a tenant's Sign-in page shows
// before anything is saved (resource.TenantDirectoryRules.DirectoryOnlyWarning).
//
// The sources of the generated management are written before the packages are loaded
// (identity_lists.go), so the switch and the directories record are read off the
// resources package's syntax here, and held to what the load finds afterwards
// (holdIdentityTenantDirectory).

// identityTenantDirectory names where the tenant record keeps its directory-only
// switch and its directory, as the people's list reads them: the record's
// @directoryOnly column, and the directories record's table with its tenant, method
// and connection columns.
type identityTenantDirectory struct {
	OnlyColumn       string
	Table            string
	TenantColumn     string
	MethodColumn     string
	ConnectionColumn string
}

// identityDirectoriesRecord is the tenant's directories record (@tenantDirectory) as
// the reading before the load finds it in a package's sources (tenantDirectoryIn). Each
// part is read whether the tenant record declares the directory-only switch or not.
type identityDirectoriesRecord struct {
	// record is the record's struct name, "" where the package declares none.
	record string
	// seat is the record's default seat column (@directoryDefaultSeat), "" where the
	// package declares no directories record or the record no such column: the role a
	// tenant is opened to its directory's people with is judged by the role file's
	// grants, which the sources written before the load must carry (seatsDirectory).
	seat string
	// seated is where the record keeps its default seat with its table, tenant, method
	// and connection columns, nil where the package declares no directories record, the
	// record no default seat column, or one of those columns cannot be read off the
	// syntax (identityDirectorySeat).
	seated *identityDirectorySeat
	// outlets are the outlets the record is served on, as its @outlet names them, nil
	// where the package declares no directories record: the two methods that say how a
	// tenant admits its directory's people are served there, and the list their role is
	// picked from is written for the auth those outlets sign people in through
	// (identityOpensToDirectory).
	outlets []string
}

// identityDirectorySeat names where the tenant's directories record keeps its default
// seat, as the generated identity client reads whether a person removed from a tenant is
// given its starting role again at their next sign-in (SeatedAgainBySignIn): the
// directories record's table with its tenant, method and connection columns, and its
// @directoryDefaultSeat column. It is read whether or not the tenant record declares the
// directory-only switch: a tenant is open to its directory's people by its directories
// row alone.
type identityDirectorySeat struct {
	Table            string
	TenantColumn     string
	MethodColumn     string
	ConnectionColumn string
	SeatColumn       string
}

// DirectorySeat is where the tenant's directories record keeps its default seat, nil
// where the application has no tenant record, its directories record declares no default
// seat, or a column of it cannot be read off the sources.
func (d *identitySourceData) DirectorySeat() *identityDirectorySeat {
	if d.tenant == nil {
		return nil
	}

	return d.tenant.seated
}

// ReadsSeatedAgain reports whether the instance's client answers whether a person removed
// from a tenant is given its starting role again at their next sign-in
// (SeatedAgainBySignIn), which the warning of the method that removes a person from a
// tenant asks: the tenant's directories record declares the default seat (DirectorySeat),
// the auth keeps the identities its people sign in through a directory with
// (HasIdentities), and its roles are given by hand. An auth whose directory writes its
// roles has no such method to ask.
func (d *identitySourceData) ReadsSeatedAgain() bool {
	return d.DirectorySeat() != nil && d.HasIdentities() && !d.rolesSynced
}

// TenantDirectory is the tenant record's directory-only switch and directory as the
// people's list reads them, nil where the application has no tenant record or the
// record declares no switch: the list then says nothing of a directory.
func (d *identitySourceData) TenantDirectory() *identityTenantDirectory {
	if d.tenant == nil {
		return nil
	}

	return d.tenant.directory
}

// HasIdentities reports whether the instance keeps the identities its people sign in
// through a directory with. An auth that keeps none signs nobody in through a
// directory.
func (d *identitySourceData) HasIdentities() bool {
	return d.has(identityIdentities)
}

// TenantWordLiteral is what the application calls one tenant in the middle of a
// sentence ("sector", "service district"), as the text of a SQL string literal the
// list's query writes it into.
func (d *identitySourceData) TenantWordLiteral() string {
	literal := sqlStringLiteral(d.TenantWord())

	return literal[1 : len(literal)-1]
}

// tenantAdministratorsParam is the query parameter a tenant's list of its people reads
// the roles that administer a tenant from: the names the auth's permission engine
// answers who administers a tenant by, handed to the list by the generated client.
const tenantAdministratorsParam = "tenantAdministratorRoles"

// AdministratorsEnter reports whether the tenant's list of its people knows the
// administrators' way into a directory-only tenant: the instance carries the list and
// the tenant record declares the switch (listsDirectoryOutsiders), and the auth
// declares the password method, the one sign-in a tenant's own administrator enters
// with beside the directory. The list then leaves such a person out of the people the
// switch keeps out, and the instance's client hands the list the roles that administer
// a tenant; an instance that writes no such list has nothing to hand them to.
func (d *identitySourceData) AdministratorsEnter() bool {
	return d.listsDirectoryOutsiders() && d.password
}

// TenantAdministratorsParam is tenantAdministratorsParam for the list's template.
func (d *identitySourceData) TenantAdministratorsParam() string {
	return tenantAdministratorsParam
}

// listsDirectoryOutsiders reports whether the instance's client answers who a tenant's
// directory-only switch keeps out: the tenant record declares the switch and the
// instance carries the tenant's list of its people.
func (i *identityInstance) listsDirectoryOutsiders() bool {
	return i.tenant != nil && i.tenant.directory != nil && i.hasList(listPeopleSuffix)
}

// offersToEntering reports whether a row of the instance's people offers the methods
// that change how a person signs in, or whether their account stands, only to a caller
// whose session enters every tenant the person administers (@callerEnters on the
// methods, TenantsToEnter on the rows): where the tenant record declares the
// directory-only switch. In any other application no tenant keeps a session out, so a
// row would offer them to everyone, and the sources say nothing of it.
func (i *identityInstance) offersToEntering() bool {
	return i.tenant != nil && i.tenant.directory != nil
}

// OffersToEntering is offersToEntering for the templates.
func (d *identitySourceData) OffersToEntering() bool {
	return d.offersToEntering()
}

// RoleRemovalRows names the lists a page runs the whole application's method that takes a
// role from a person from, for its @callerEnters: the roles people hold and, where a person
// takes a role for a set time, the roles held so ("CrewHeldRole, CrewTakenRole"). Empty
// where no row offers the method only to a caller who enters a tenant: no switch is
// declared (offersToEntering), the instance declares no list of the roles people hold, or a
// directory writes the auth's roles and the method is not written (GivesRoles).
func (d *identitySourceData) RoleRemovalRows() string {
	if !d.offersToEntering() || !d.listsHeldRoles() || !d.GivesRoles() {
		return ""
	}
	rows := d.HeldRole()
	if d.takesRoles() {
		rows += ", " + d.TakenRole()
	}

	return rows
}

// enteringAdministratorsParam is the query parameter the rows of the instance's people
// read the roles that administer a tenant from, where they offer a method only to a
// caller who enters the tenants the person administers (offersToEntering): the names
// the auth's permission engine answers who administers a tenant by, handed to them by
// the generated client.
const enteringAdministratorsParam = "enteringAdministratorRoles"

// EnteringAdministratorsParam is enteringAdministratorsParam for the templates.
func (d *identitySourceData) EnteringAdministratorsParam() string {
	return enteringAdministratorsParam
}

// ListsDirectoryOutsiders is listsDirectoryOutsiders for the client's template.
func (d *identitySourceData) ListsDirectoryOutsiders() bool {
	return d.listsDirectoryOutsiders()
}

// ReadsManagingTenants reports whether the instance's store reads which tenants manage
// an account (identity.Store.ManagingTenants) from the tenant's list of its people: the
// instance carries the list, the tenant record declares a directory
// (listsDirectoryOutsiders), and the auth keeps the identities its people sign in through
// a directory with (HasIdentities). Every other store answers none and reads nothing.
func (d *identitySourceData) ReadsManagingTenants() bool {
	return d.listsDirectoryOutsiders() && d.HasIdentities()
}

// listsDirectoryInvitations reports whether the instance's client answers which
// invitations not yet accepted would make another of a tenant's administrators who
// enters a directory-only tenant with a password: the client answers who the switch
// keeps out (listsDirectoryOutsiders), the auth declares the password method, and the
// instance carries the tenant's list of its invitations.
func (i *identityInstance) listsDirectoryInvitations() bool {
	return i.listsDirectoryOutsiders() && i.password && i.hasList(listInvitationsSuffix)
}

// ListsDirectoryInvitations is listsDirectoryInvitations for the client's template.
func (d *identitySourceData) ListsDirectoryInvitations() bool {
	return d.listsDirectoryInvitations()
}

// The annotations the reading before the load looks for on a field, each at the start
// of a line of the field's comment, as the extraction reads a field's annotations.
var (
	fieldDirectoryOnly       = fieldAnnotation(directoryOnlyKeyword)
	fieldDirectoryMethod     = fieldAnnotation(directoryMethodKeyword)
	fieldDirectoryConnection = fieldAnnotation(directoryConnectionKeyword)
	fieldDirectorySeat       = fieldAnnotation(directoryDefaultSeatKeyword)
	fieldBareDomain          = regexp.MustCompile(`(?m)^\s*@` + domainKeyword + `\s*$`)
)

// fieldAnnotation matches a no-argument annotation on a line of its own.
func fieldAnnotation(keyword string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*@` + keyword + `\s*$`)
}

// tenantDirectoryIn reads where the tenant record keeps its directory-only switch and
// its directory off a package's sources: the column of the record's field annotated
// @directoryOnly, and the struct annotated @tenantDirectory with its bare @domain,
// @directoryMethod and @directoryConnection columns. It answers nil when the record
// declares no switch or the package no directories record, and when a column cannot be
// read off the syntax: the extraction refuses a declaration that is wrong once the
// packages are loaded, with the struct's place, and holdIdentityTenantDirectory holds
// this reading to the loaded one. Only the syntax is read, as tenantRecordIn reads it.
//
// directories is the directories record as the sources declare it, read whether the
// tenant record declares the switch or not (identityDirectoriesRecord).
func tenantDirectoryIn(dir, tenant string, pluralize func(string) string) (directory *identityTenantDirectory, directories identityDirectoriesRecord, err error) {
	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		return nil, identityDirectoriesRecord{}, errors.Wrap(err, "build.ImportDir()")
	}

	out := &identityTenantDirectory{}
	fset := token.NewFileSet()
	for _, name := range pkg.GoFiles {
		file, err := goparser.ParseFile(fset, filepath.Join(dir, name), nil, goparser.ParseComments|goparser.SkipObjectResolution)
		if err != nil {
			return nil, identityDirectoriesRecord{}, errors.Wrap(err, "parser.ParseFile()")
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				switch {
				case typeSpec.Name.Name == tenant:
					out.OnlyColumn = annotatedColumn(structType, fieldDirectoryOnly)
				case structCommentHas(gen, typeSpec, tenantDirectoryKeyword):
					directories.record = typeSpec.Name.Name
					out.Table = pluralize(typeSpec.Name.Name)
					out.TenantColumn = annotatedColumn(structType, fieldBareDomain)
					out.MethodColumn = annotatedColumn(structType, fieldDirectoryMethod)
					out.ConnectionColumn = annotatedColumn(structType, fieldDirectoryConnection)
					directories.seat = annotatedColumn(structType, fieldDirectorySeat)
					directories.outlets = structCommentOutlets(gen, typeSpec)
				}
			}
		}
	}
	if directories.seat != "" && out.Table != "" && out.TenantColumn != "" && out.MethodColumn != "" && out.ConnectionColumn != "" {
		directories.seated = &identityDirectorySeat{Table: out.Table, TenantColumn: out.TenantColumn, MethodColumn: out.MethodColumn, ConnectionColumn: out.ConnectionColumn, SeatColumn: directories.seat}
	}
	if out.OnlyColumn == "" || out.Table == "" || out.TenantColumn == "" || out.MethodColumn == "" || out.ConnectionColumn == "" {
		return nil, directories, nil
	}

	return out, directories, nil
}

// seatsDirectory reports whether the instance's client judges the role a tenant is
// opened to its directory's people with (identity.Manager.DirectoryRole): the roles of
// a tenant are given by hand, and the directories record carries the default seat. The
// role is one the caller could give a person directly, so the instance's package
// answers the role file's grants (roleGrantsSource).
func (i *identityInstance) seatsDirectory() bool {
	return i.picksRoles() && i.tenant.directorySeat != ""
}

// identityOpensToDirectory reports whether the instance's auth is the one whose
// generated management judges the role a tenant is opened to its directory's people
// with, so that the instance declares the list the role is picked from
// (<Auth><Record>DirectoryRole): the instance judges such a role (seatsDirectory), it
// carries the tenant's list of its people, its client is handed the role file's answers
// (judgesRoleChanges), a session outlet serves the directories record, and every
// session outlet that does is bound to this auth. It reads the record's outlets off the
// sources, before the load, and agrees with where the two methods are served after it
// (directoryRole), which holdIdentityDirectoryRole holds.
func (r *resourceGenerator) identityOpensToDirectory(inst *identityInstance) bool {
	if !inst.seatsDirectory() || !inst.hasList(listPeopleSuffix) || !inst.judgesRoleChanges() {
		return false
	}

	return r.identityRecordsTenantSignIn(inst)
}

// identityRecordsTenantSignIn reports whether the instance's auth is the one a tenant's
// sign-in settings are recorded through (tenantSignInRecorder): a session outlet serves
// the directories record, and every session outlet that does is bound to this auth. It
// asks nothing of what the auth's management gives: an auth whose directory writes its
// roles, or whose tenants' directories carry no default seat, records them all the same.
func (r *resourceGenerator) identityRecordsTenantSignIn(inst *identityInstance) bool {
	if inst.tenant == nil {
		return false
	}
	served := false
	for _, outlet := range r.allOutlets() {
		if !outlet.servesSessions || !slices.Contains(inst.tenant.directoryOutlets, outlet.name) {
			continue
		}
		if outlet.auth == nil || outlet.auth.importPath != inst.importPath {
			return false
		}
		served = true
	}

	return served
}

// holdIdentityDirectoryRole holds the list the role is picked from to the methods it
// feeds: the list was written, before the load, for the instance whose auth serves the
// two methods that say how a tenant admits its directory's people, and for no other.
// The two are decided off the same declarations, so a difference is a fault of the
// reading before the load, and the run stops before it generates a method whose role
// has no list to be picked from, or a list no method reads.
func (r *resourceGenerator) holdIdentityDirectoryRole() error {
	serving := r.directoryRole()
	for _, inst := range r.identity {
		serves := serving != nil && serving.identity == inst
		if inst.opensToDirectory == serves {
			continue
		}
		spell := func(yes bool) string {
			if yes {
				return "is"
			}

			return "is not"
		}

		return errors.Newf("GenerateIdentity(%q): the auth %s %s the one that declares the list the role a tenant is opened to its directory's people with is picked from, as read off the sources of %s before the packages loaded, "+
			"and it %s the one that serves the two methods once they are loaded; the two readings must agree, so this is a fault of the generator",
			r.identityTree.Dir(), inst.auth, spell(inst.opensToDirectory), r.resource.Dir(), spell(serves))
	}

	return nil
}

// annotatedColumn is the column of the struct's field whose comment carries the
// annotation, as the field's spanner tag names it; "" when no field carries it or the
// field has no such tag.
func annotatedColumn(structType *ast.StructType, annotation *regexp.Regexp) string {
	for _, field := range structType.Fields.List {
		if !annotation.MatchString(field.Doc.Text() + field.Comment.Text()) {
			continue
		}
		if field.Tag == nil {
			return ""
		}
		tag, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			return ""
		}

		return reflect.StructTag(tag).Get(spannerTagKey)
	}

	return ""
}

// holdIdentityTenantDirectory holds the directory-only switch, the directories record,
// its default seat column and where the default seat is read (identityDirectorySeat) the
// sources were written for to the ones the load found,
// as holdIdentityTenant holds the record itself. It runs once the record's declaration is whole
// (validateTenantDeclaration), which is after every kind is extracted and before
// anything renders. The two are read off the same files, so a difference is a fault of
// the reading before the load, and the run stops before it generates over lists
// written for another declaration.
func (r *resourceGenerator) holdIdentityTenantDirectory() error {
	if len(r.identity) == 0 || r.identity[0].tenant == nil {
		return nil
	}
	written := r.identity[0].tenant.directory
	var loaded *identityTenantDirectory
	if record := r.tenantRecord(); record != nil {
		if columns := r.declarationColumns(record); columns != nil && columns.DirectoryOnly != "" {
			loaded = &identityTenantDirectory{
				OnlyColumn:       columns.DirectoryOnly,
				Table:            columns.DirectoriesTable,
				TenantColumn:     columns.TenantColumn,
				MethodColumn:     columns.MethodColumn,
				ConnectionColumn: columns.ConnectionColumn,
			}
		}
	}
	writtenSeat, loadedSeat := r.identity[0].tenant.directorySeat, ""
	if record := r.tenantRecord(); record != nil {
		if columns := r.declarationColumns(record); columns != nil {
			loadedSeat = columns.DefaultSeatColumn
		}
	}
	if writtenSeat != loadedSeat {
		return errors.Newf("GenerateIdentity(%q): the role grants were written for the directories record's default seat column %q, read off the sources of %s before the packages loaded, and the loaded package declares %q; the two readings must agree, so this is a fault of the generator", r.identityTree.Dir(), writtenSeat, r.resource.Dir(), loadedSeat)
	}
	writtenSeated, loadedSeated := r.identity[0].tenant.seated, (*identityDirectorySeat)(nil)
	if record := r.tenantRecord(); record != nil {
		if columns := r.declarationColumns(record); columns != nil && columns.DefaultSeatColumn != "" {
			loadedSeated = &identityDirectorySeat{
				Table:            columns.DirectoriesTable,
				TenantColumn:     columns.TenantColumn,
				MethodColumn:     columns.MethodColumn,
				ConnectionColumn: columns.ConnectionColumn,
				SeatColumn:       columns.DefaultSeatColumn,
			}
		}
	}
	if !reflect.DeepEqual(writtenSeated, loadedSeated) {
		spellSeat := func(seated *identityDirectorySeat) string {
			if seated == nil {
				return "no default seat"
			}

			return "the default seat " + seated.SeatColumn + " over the directories table " + seated.Table + " (" + seated.TenantColumn + ", " + seated.MethodColumn + ", " + seated.ConnectionColumn + ")"
		}

		return errors.Newf("GenerateIdentity(%q): the read of whether a person removed from a tenant is seated again was written for %s, read off the sources of %s before the packages loaded, and the loaded package declares %s; the two readings must agree, so this is a fault of the generator", r.identityTree.Dir(), spellSeat(writtenSeated), r.resource.Dir(), spellSeat(loadedSeated))
	}
	if reflect.DeepEqual(written, loaded) {
		return nil
	}
	spell := func(directory *identityTenantDirectory) string {
		if directory == nil {
			return "no directory-only switch"
		}

		return "the switch " + directory.OnlyColumn + " over the directories table " + directory.Table + " (" + directory.TenantColumn + ", " + directory.MethodColumn + ", " + directory.ConnectionColumn + ")"
	}

	return errors.Newf("GenerateIdentity(%q): the tenant's list of its people was written for %s, read off the sources of %s before the packages loaded, and the loaded package declares %s; the two readings must agree, so this is a fault of the generator", r.identityTree.Dir(), spell(written), r.resource.Dir(), spell(loaded))
}
