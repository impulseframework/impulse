package generation

import (
	"go/ast"
	"go/build"
	goparser "go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/internal/words"
)

// The invitation that makes a tenant, in an auth's generated management. Staff bring an
// organization into a tenanted application by inviting one person: the invitation carries
// the tenant record's fields as staff entered them, and the person's first sign-in makes
// the tenant from them (identity.Manager.InviteTenant). The generator writes, for the
// auth whose people the tenant record's outlets sign in:
//
//   - Invite<Auth><Tenant>, the method staff send one with. Its request is the invited
//     address, the person's name, and the tenant record's fields, each under the record's
//     name (SectorName, SectorRegion), so the form a page draws from the method is the
//     tenant record's own, with the directory-only switch and the role the people of the
//     tenant's directory start with where the records keep them;
//   - <Auth>Invited<Tenant>, the list of the invitations that still stand, each with
//     the details it carries, a field shown only to a caller who may read it on the
//     tenant record;
//   - Withdraw<Auth>Invited<Tenant>, which withdraws one: staff who want other details
//     withdraw the invitation and send another.
//
// The record's fields are read off the resources package's syntax and the schema, since
// the sources are written before the packages are loaded (identity_lists.go), and held
// to what the load finds afterwards (holdIdentityTenantInvitation). A field the wire
// cannot write is not carried, and neither is a secret: a tenant made by invitation is
// given its secret afterward.

// newTenantColumn is the mail links table's column an invitation that makes a tenant
// keeps the tenant in (the session library's migration adds it).
const newTenantColumn = "NewTenant"

// identityTenantField is one field of the tenant record as an invitation carries it.
type identityTenantField struct {
	// Name is the record's Go field name, and Type its type as a source file of another
	// package writes it.
	Name string
	Type string
	// Wire is the field's name on the wire, which is the tag a grant on the tenant
	// record names it by.
	Wire string
	// Column is the field's column of the record's table, which the list of tenants
	// reads it by (identity_listedtenant.go).
	Column string
	// Required marks a field the record's create requires: its column takes no NULL and
	// has no default. Text marks a field a form cannot leave zero when it is required: every
	// required field but a boolean, whose zero is an answer.
	Required bool
	Text     bool
}

// PointerType is the field's type as the list of invited tenants carries it: a field
// the caller may not read on the tenant record is absent, so every field can be.
func (f identityTenantField) PointerType() string {
	if strings.HasPrefix(f.Type, "*") || strings.HasPrefix(f.Type, "[]") {
		return f.Type
	}

	return "*" + f.Type
}

// identityTenantInvitation is the tenant record as an invitation carries it.
type identityTenantInvitation struct {
	// fields are the record's fields the invitation carries, in declaration order.
	fields []identityTenantField
	// nameField is the field people call the tenant by (@tenantName), "" where the
	// record marks none: the invitation page then says the application's word for one.
	// nameColumn is its column of the record's table, which a list that names a tenant
	// in a line of text reads (identitySourceData.GroupRoleLines).
	nameField  string
	nameColumn string
	// directoryOnly is the record's directory-only switch, nil where it declares none
	// (Type is unused: the switch is a yes or a no), and seats says the directories
	// record keeps a default role.
	directoryOnly *identityTenantField
	seats         bool
	// since is the field the record marks as when a tenant was made (@tenantSince), nil
	// where it marks none (Type and the two marks unused): the list of tenants says it as
	// Since. An invitation carries it only where the wire can write it, as any field.
	since *identityTenantField
	// imports are the imports the fields' types need, each as an import block writes
	// it (importLine), the resources package's among them.
	imports []string
	// key is the record's one key field, which no invitation carries (the tenant's id
	// is generated when it is made) and the list of tenants keys its rows by (Wire and
	// the two marks unused), and keyImports the imports its type needs, kept apart
	// from the fields' so the invitation's sources import nothing for it.
	key        identityTenantField
	keyImports []string
}

// The names of the three pieces, each the one place it is named.
func (i *identityInstance) inviteTenantMethod() string {
	return "Invite" + i.auth + i.tenant.name
}

func (i *identityInstance) invitedTenantName() string {
	return i.auth + "Invited" + i.tenant.name
}

func (i *identityInstance) withdrawInvitedTenantMethod() string {
	return "Withdraw" + i.auth + "Invited" + i.tenant.name
}

// invitedTenantRoleName names one row of the list the invitation's starting role is
// picked from.
func (i *identityInstance) invitedTenantRoleName() string {
	return i.auth + "Invited" + i.tenant.name + "Role"
}

// makesTenants reports whether the instance's auth is the one a tenant is made through:
// its people are the ones the tenant record's outlets sign in, the application gives its
// roles (the person who accepts is seated as the tenant's administrator), and the schema
// keeps its invitations and its memberships.
func (i *identityInstance) makesTenants() bool {
	return i.createsTenant && !i.rolesSynced && i.tenant != nil && i.has(identityMailLinks) && i.has(identityMemberships)
}

// invitesTenants reports whether the instance carries the invitation that makes a
// tenant: makesTenants, and the record's fields were read.
func (i *identityInstance) invitesTenants() bool {
	return i.makesTenants() && i.tenant.invitation != nil
}

// picksStartingRole reports whether the instance's invitation that makes a tenant
// carries the role the people of the tenant's directory start with, picked from a list
// of its own: invitesTenants, and the directories record keeps a default role. It holds
// on an instance with no tenant and on one whose invitation was not read, as false.
func (i *identityInstance) picksStartingRole() bool {
	return i.invitesTenants() && i.tenant.invitation.seats
}

// InvitesTenants is invitesTenants for the templates.
func (d *identitySourceData) InvitesTenants() bool {
	return d.invitesTenants()
}

// PicksStartingRole is picksStartingRole for the templates.
func (d *identitySourceData) PicksStartingRole() bool {
	return d.picksStartingRole()
}

// InviteTenant, InvitedTenant and WithdrawInvitedTenant name the three pieces for the
// templates.
func (d *identitySourceData) InviteTenant() string {
	return d.inviteTenantMethod()
}

func (d *identitySourceData) InvitedTenant() string {
	return d.invitedTenantName()
}

func (d *identitySourceData) WithdrawInvitedTenant() string {
	return d.withdrawInvitedTenantMethod()
}

// InvitedTenantRole names one row of the list the invitation's starting role is picked
// from, for the templates.
func (d *identitySourceData) InvitedTenantRole() string {
	return d.invitedTenantRoleName()
}

// TenantFields are the tenant record's fields an invitation carries.
func (d *identitySourceData) TenantFields() []identityTenantField {
	return d.tenant.invitation.fields
}

// TenantFieldImports are the import lines the fields' types need.
func (d *identitySourceData) TenantFieldImports() string {
	return strings.Join(d.tenant.invitation.imports, "\n\t")
}

// TenantNameField is the field people call the tenant by, "" where the record marks
// none.
func (d *identitySourceData) TenantNameField() string {
	return d.tenant.invitation.nameField
}

// TenantDirectoryOnly reports whether an invitation may say directory only,
// TenantDirectoryOnlyField names the record's switch, and TenantSeats reports whether an
// invitation may name the role the people of the tenant's directory start with.
func (d *identitySourceData) TenantDirectoryOnly() bool {
	return d.tenant.invitation.directoryOnly != nil
}

func (d *identitySourceData) TenantDirectoryOnlyField() *identityTenantField {
	return d.tenant.invitation.directoryOnly
}

func (d *identitySourceData) TenantSeats() bool {
	return d.tenant.invitation.seats
}

// TenantDirectories reports whether the application declares a directories record, so
// that a tenant made by an invitation can take the directory of the person who accepts
// it. Where it declares none, no tenant has a directory, and what the invitation says
// of itself leaves the directory out.
func (d *identitySourceData) TenantDirectories() bool {
	return len(d.tenant.directoryOutlets) > 0
}

// invitedTenantSources are the source files of the invitation that makes a tenant, and
// the list its starting role is picked from where it carries one.
func (r *resourceGenerator) invitedTenantSources(inst *identityInstance) []identitySourceFile {
	if !inst.invitesTenants() {
		return nil
	}
	needs := []identityTable{identityMailLinks, identityMemberships}

	files := []identitySourceFile{
		{dir: inst.methods, name: fileStem(inst.inviteTenantMethod()), template: identityInviteTenantTemplate, needs: needs},
		{dir: inst.methods, name: fileStem(r.pluralize(inst.invitedTenantName())), template: identityInvitedTenantsTemplate, needs: needs},
		{dir: inst.methods, name: fileStem(inst.withdrawInvitedTenantMethod()), template: identityWithdrawInvitedTenantTemplate, needs: needs},
	}
	if inst.picksStartingRole() {
		files = append(files, identitySourceFile{dir: inst.methods, name: fileStem(r.pluralize(inst.invitedTenantRoleName())), template: identityInvitedTenantRolesTemplate, needs: needs})
	}

	return files
}

// refuseInvitationNamesTaken refuses a tenant record whose name gives the invitation's
// method the name of another method of the auth: a record named User would name it as
// the method that invites a person into the application is named (Invite<Auth>User).
func (i *identityInstance) refuseInvitationNamesTaken() error {
	if !i.invitesTenants() {
		return nil
	}
	if taken := "Invite" + i.auth + "User"; i.inviteTenantMethod() == taken {
		return errors.Newf("Auth(%q): the tenant record %s gives the method that invites one the name %s, which is the method that invites a person into the application; the tenant record takes another name", i.importPath, i.tenant.name, taken)
	}

	return nil
}

// requireNewTenantColumn refuses a schema whose mail links table lacks the column an
// invitation that makes a tenant is kept in, for the auth that sends them: nothing else
// creates a tenant, so the application would have no way to bring one in.
func (r *resourceGenerator) requireNewTenantColumn(inst *identityInstance) error {
	if !inst.invitesTenants() {
		return nil
	}
	table := inst.tableName(identityMailLinks)
	if _, ok := r.tableMap[table].Columns[newTenantColumn]; ok {
		return nil
	}

	return errors.Newf("Auth(%q): the table %s has no column %s, which an invitation that makes %s is kept in, and staff bring %s in by such an invitation alone; add it as the session library's schema does (ALTER TABLE %s ADD COLUMN %s STRING(MAX))",
		inst.importPath, table, newTenantColumn, withArticle(inst.tenant.word), withArticle(inst.tenant.word), table, newTenantColumn)
}

// The annotations and tags the reading before the load looks for on a field.
var (
	fieldTenantName        = fieldAnnotation(tenantNameKeyword)
	fieldTenantSince       = fieldAnnotation(tenantSinceKeyword)
	fieldDirectoryDefault  = fieldAnnotation(directoryDefaultSeatKeyword)
	fieldAnyAnnotation     = regexp.MustCompile(`(?m)^\s*@([a-zA-Z]+)`)
	carriedFieldAnnotation = []string{tenantNameKeyword, tenantSinceKeyword}
)

// tenantInvitationIn reads the tenant record as an invitation carries it off a package's
// sources and the schema: the record's fields the wire may write at create, less the
// directory-only switch and every secret, each with its type as another package writes
// it and its column, and the record's key, which the list of tenants keys its rows by.
// It answers nil where the record cannot be read this way: a field the reading does not
// know (an annotation it does not carry, a type it cannot name from another package)
// stops it, and so does a key of more than one column or of a type another package
// cannot name; the invitation is then not generated, which holdIdentityTenantInvitation
// refuses with what stopped the reading once the packages are loaded. Only the syntax
// is read, as tenantRecordIn reads it.
func (r *resourceGenerator) tenantInvitationIn(dir, record string) (*identityTenantInvitation, string, error) {
	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		return nil, "", errors.Wrap(err, "build.ImportDir()")
	}
	meta, ok := r.tableMap[r.pluralize(record)]
	if !ok {
		return nil, "", nil
	}
	resourcesPath, ok := r.modulePath(r.resource)
	if !ok {
		return nil, "the resources package is outside the module the generator runs in", nil
	}

	out := &identityTenantInvitation{}
	fset := token.NewFileSet()
	for _, name := range pkg.GoFiles {
		file, err := goparser.ParseFile(fset, filepath.Join(dir, name), nil, goparser.ParseComments|goparser.SkipObjectResolution)
		if err != nil {
			return nil, "", errors.Wrap(err, "parser.ParseFile()")
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
					naming := &typeNaming{file: file, pkgName: pkg.Name, pkgPath: resourcesPath, imports: map[string]bool{}}
					keyNaming := &typeNaming{file: file, pkgName: pkg.Name, pkgPath: resourcesPath, imports: map[string]bool{}}
					why, err := out.readFields(structType, meta, naming, keyNaming)
					if err != nil || why != "" {
						return nil, why, err
					}
					out.imports = sortedImports(naming.imports)
					out.keyImports = sortedImports(keyNaming.imports)
				case structCommentHas(gen, typeSpec, tenantDirectoryKeyword):
					out.seats = annotatedColumn(structType, fieldDirectoryDefault) != ""
				}
			}
		}
	}

	return out, "", nil
}

// sortedImports are the import lines a reading noted, in order.
func sortedImports(imports map[string]bool) []string {
	return slices.Sorted(func(yield func(string) bool) {
		for imp := range imports {
			if !yield(imp) {
				return
			}
		}
	})
}

// readFields reads the record's fields off its struct, and its key, whose type is named
// through keyNaming. why says, in words, what stopped the reading, "" when the record
// was read.
func (t *identityTenantInvitation) readFields(structType *ast.StructType, meta *tableMetadata, naming, keyNaming *typeNaming) (why string, err error) {
	wire := strcase.NewCaser(false, nil, nil)
	for _, field := range structType.Fields.List {
		if len(field.Names) != 1 {
			return "the record embeds a struct or declares several fields on one line", nil
		}
		name := field.Names[0].Name
		tag := reflect.StructTag("")
		if field.Tag != nil {
			unquoted, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				return "", errors.Wrapf(err, "field %s: its struct tag", name)
			}
			tag = reflect.StructTag(unquoted)
		}
		column, ok := meta.Columns[tag.Get(spannerTagKey)]
		if !ok {
			return "field " + name + " names no column of the table", nil
		}
		comment := field.Doc.Text() + field.Comment.Text()
		if fieldTenantSince.MatchString(comment) {
			// When a tenant was made is kept whether or not the wire writes it: the list
			// of tenants says it as Since, and the switch below decides, as for any
			// field, whether an invitation carries it.
			t.since = &identityTenantField{Name: name, Wire: wire.ToCamel(name), Column: tag.Get(spannerTagKey)}
		}
		conditions := strings.Split(tag.Get(conditionsTagKey), ",")
		switch {
		case column.IsPrimaryKey:
			if t.key.Name != "" {
				return "the record's key is more than one column", nil
			}
			typeText, ok := keyNaming.name(field.Type)
			if !ok {
				return "the record's key has a type another package cannot name", nil
			}
			t.key = identityTenantField{Name: name, Type: typeText, Column: tag.Get(spannerTagKey)}

			continue
		case fieldDirectoryOnly.MatchString(comment):
			t.directoryOnly = &identityTenantField{Name: name, Type: boolGoType, Wire: wire.ToCamel(name), Column: tag.Get(spannerTagKey)}

			continue
		case slices.Contains(conditions, outputOnlyCondition), column.GenerationExpression != "":
			// The wire cannot write it, so no invitation carries it.
			continue
		case slices.Contains(conditions, secretCondition), slices.Contains(conditions, inputOnlyCondition):
			// A secret is never carried on an invitation: the tenant is given it afterward.
			continue
		}
		for _, annotation := range fieldAnyAnnotation.FindAllStringSubmatch(comment, -1) {
			if !slices.Contains(carriedFieldAnnotation, annotation[1]) {
				return "field " + name + " carries @" + annotation[1] + ", which an invitation's form does not draw", nil
			}
		}
		if tag.Get(defaultCreateFnTagKey) != "" || tag.Get(outputOnlyUpdateFnTagKey) != "" {
			return "field " + name + " is written by a function of the record, which an invitation's form does not draw", nil
		}
		typeText, ok := naming.name(field.Type)
		if !ok {
			return "field " + name + " has a type another package cannot name", nil
		}
		if fieldTenantName.MatchString(comment) {
			t.nameField, t.nameColumn = name, tag.Get(spannerTagKey)
		}
		required := !column.IsNullable && !column.HasDefault
		t.fields = append(t.fields, identityTenantField{
			Name: name, Type: typeText, Wire: wire.ToCamel(name), Column: tag.Get(spannerTagKey), Required: required, Text: required && typeText != boolGoType,
		})
	}

	return "", nil
}

// typeNaming names a type of the resources package's sources as a source file of
// another package writes it: a type the package declares is qualified with the package,
// and an imported one keeps its qualifier, its import noted as an import block writes
// it (importLine).
type typeNaming struct {
	file    *ast.File
	pkgName string
	pkgPath string
	imports map[string]bool
}

// name renders expr; ok is false for a type that cannot be named from outside (one that
// reads an import the file does not declare under the qualifier, or a shape the reading
// does not walk).
func (n *typeNaming) name(expr ast.Expr) (text string, ok bool) {
	rewritten, ok := n.rewrite(expr)
	if !ok {
		return "", false
	}

	return types.ExprString(rewritten), true
}

func (n *typeNaming) rewrite(expr ast.Expr) (ast.Expr, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		if types.Universe.Lookup(e.Name) != nil {
			return e, true
		}
		n.imports[importLine(n.pkgName, n.pkgPath)] = true

		return &ast.SelectorExpr{X: ast.NewIdent(n.pkgName), Sel: ast.NewIdent(e.Name)}, true
	case *ast.SelectorExpr:
		qualifier, ok := e.X.(*ast.Ident)
		if !ok {
			return nil, false
		}
		imported, ok := n.importOf(qualifier.Name)
		if !ok {
			return nil, false
		}
		n.imports[importLine(qualifier.Name, imported)] = true

		return e, true
	case *ast.StarExpr:
		inner, ok := n.rewrite(e.X)

		return &ast.StarExpr{X: inner}, ok
	case *ast.ArrayType:
		if e.Len != nil {
			return nil, false
		}
		inner, ok := n.rewrite(e.Elt)

		return &ast.ArrayType{Elt: inner}, ok
	case *ast.IndexExpr:
		generic, ok := n.rewrite(e.X)
		if !ok {
			return nil, false
		}
		argument, ok := n.rewrite(e.Index)

		return &ast.IndexExpr{X: generic, Index: argument}, ok
	default:
		return nil, false
	}
}

// importLine is an import as an import block writes it: the path alone where the
// package's name is read off it, and the name before the path otherwise, so a type the
// record's file reaches through an alias is reached through the same alias in the
// sources written from it.
func importLine(name, importPath string) string {
	if name == assumedPackageName(importPath) {
		return strconv.Quote(importPath)
	}

	return name + " " + strconv.Quote(importPath)
}

// importOf answers the path the file imports under a qualifier: the import's own name
// where it declares one, the path's last element otherwise, a major version's element
// (v5) passed over.
func (n *typeNaming) importOf(qualifier string) (string, bool) {
	for _, spec := range n.file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := assumedPackageName(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == qualifier {
			return importPath, true
		}
	}

	return "", false
}

// tenantInvitationFields are the tenant record's fields an invitation carries, as the
// load finds them: every field the wire may write at create, less every secret. The
// directory-only switch is none of them: the wire never writes it (IsOutputOnly), and
// the invitation carries it apart. The maker reads them (tenantMakerData), and the
// reading before the load is held to them.
func tenantInvitationFields(record *resourceInfo) []*resourceField {
	var fields []*resourceField
	for _, field := range record.Fields {
		switch {
		case field.IsPrimaryKey, field.IsOutputOnly(), field.IsInputOnly():
		default:
			fields = append(fields, field)
		}
	}

	return fields
}

// holdIdentityTenantInvitation holds the fields the invitation's sources were written
// for to the ones the load found, as holdIdentityTenant holds the record itself, and
// refuses a record the invitation cannot serve. Staff bring a tenant in by an invitation
// alone, so a record whose fields the reading before the load could not carry is refused
// here, with what stopped it, and so is one whose create requires a secret: no invitation
// carries a secret, so the tenant could never be made.
func (r *resourceGenerator) holdIdentityTenantInvitation() error {
	record := r.tenantRecord()
	if record == nil {
		return nil
	}
	if !slices.ContainsFunc(r.identity, (*identityInstance).makesTenants) {
		// No auth of the program invites: the application makes its tenants in its own
		// code, which gives a tenant whatever its create requires.
		return nil
	}
	word := withArticle(words.InWords(record.Name()))
	for _, field := range record.Fields {
		switch {
		case field.IsOutputOnly() || !field.IsInputOnly() || !field.IsRequired():
		case field.IsSecret():
			return errors.Newf("struct %s: field %s is a secret the record's create requires, and no invitation carries a secret: %s made by invitation is given its secret afterward, so let the column take NULL or give it a default",
				record.Name(), field.Name(), word)
		default:
			return errors.Newf("struct %s: field %s is write-only (conditions:%q) and the record's create requires it, and no invitation carries a value the server never returns: %s made by invitation is given it afterward, so let the column take NULL or give it a default",
				record.Name(), field.Name(), inputOnlyCondition, word)
		}
	}
	for _, inst := range r.identity {
		if !inst.makesTenants() {
			continue
		}
		if inst.tenant.invitation == nil {
			return errors.Newf("struct %s: the invitation that makes %s cannot carry the record: %s. Staff bring %s in by an invitation alone, so the record keeps to fields its form can draw",
				record.Name(), withArticle(inst.tenant.word), inst.tenant.unreadable, withArticle(inst.tenant.word))
		}
		written := make([]string, 0, len(inst.tenant.invitation.fields))
		for _, field := range inst.tenant.invitation.fields {
			written = append(written, field.Name+" "+field.Wire+" required="+strconv.FormatBool(field.Required))
		}
		if flag := inst.tenant.invitation.directoryOnly; flag != nil {
			written = append(written, "the directory-only switch "+flag.Name+" "+flag.Wire)
		}
		if since := inst.tenant.invitation.since; since != nil {
			written = append(written, "when it was made "+since.Name)
		}
		var loaded []string
		for _, field := range tenantInvitationFields(record) {
			loaded = append(loaded, field.Name()+" "+field.WireName()+" required="+strconv.FormatBool(field.IsRequired()))
		}
		if flag := record.DirectoryOnlyField; flag != nil {
			loaded = append(loaded, "the directory-only switch "+flag.Name()+" "+flag.WireName())
		}
		if since := record.TenantSinceField; since != nil {
			loaded = append(loaded, "when it was made "+since.Name())
		}
		if !slices.Equal(written, loaded) {
			return errors.Newf("struct %s: the invitation's sources were written for the fields [%s] and the loaded record carries [%s]; the two are read off the same files, so this is a fault of the generator's reading before the load: report it",
				record.Name(), strings.Join(written, "; "), strings.Join(loaded, "; "))
		}
	}

	return nil
}
