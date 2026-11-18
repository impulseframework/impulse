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
// administrators' way into a directory-only tenant: the tenant record declares the
// switch and the auth declares the password method, the one sign-in a tenant's own
// administrator enters with beside the directory. The list then leaves such a person
// out of the people the switch keeps out.
func (d *identitySourceData) AdministratorsEnter() bool {
	return d.tenant != nil && d.tenant.directory != nil && d.password
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

// ListsDirectoryOutsiders is listsDirectoryOutsiders for the client's template.
func (d *identitySourceData) ListsDirectoryOutsiders() bool {
	return d.listsDirectoryOutsiders()
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
// seat is the directories record's default seat column (@directoryDefaultSeat), ""
// where the package declares no directories record or the record no such column. It is
// read whether the tenant record declares the switch or not: the role a tenant is
// opened to its directory's people with is judged by the role file's grants, which the
// sources written before the load must carry (seatsDirectory). outlets are the outlets
// the directories record is served on, as its @outlet names them, nil where the package
// declares no directories record: the two methods that say how a tenant admits its
// directory's people are served there, and the list their role is picked from is
// written for the auth those outlets sign people in through (identityOpensToDirectory).
func tenantDirectoryIn(dir, record string, pluralize func(string) string) (directory *identityTenantDirectory, seat string, outlets []string, err error) {
	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		return nil, "", nil, errors.Wrap(err, "build.ImportDir()")
	}

	out := &identityTenantDirectory{}
	fset := token.NewFileSet()
	for _, name := range pkg.GoFiles {
		file, err := goparser.ParseFile(fset, filepath.Join(dir, name), nil, goparser.ParseComments|goparser.SkipObjectResolution)
		if err != nil {
			return nil, "", nil, errors.Wrap(err, "parser.ParseFile()")
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
				case typeSpec.Name.Name == record:
					out.OnlyColumn = annotatedColumn(structType, fieldDirectoryOnly)
				case structCommentHas(gen, typeSpec, tenantDirectoryKeyword):
					out.Table = pluralize(typeSpec.Name.Name)
					out.TenantColumn = annotatedColumn(structType, fieldBareDomain)
					out.MethodColumn = annotatedColumn(structType, fieldDirectoryMethod)
					out.ConnectionColumn = annotatedColumn(structType, fieldDirectoryConnection)
					seat = annotatedColumn(structType, fieldDirectorySeat)
					outlets = structCommentOutlets(gen, typeSpec)
				}
			}
		}
	}
	if out.OnlyColumn == "" || out.Table == "" || out.TenantColumn == "" || out.MethodColumn == "" || out.ConnectionColumn == "" {
		return nil, seat, outlets, nil
	}

	return out, seat, outlets, nil
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

// holdIdentityTenantDirectory holds the directory-only switch, the directories record
// and its default seat column the sources were written for to the ones the load found,
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
