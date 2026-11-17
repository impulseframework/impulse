package generation

import (
	"go/types"
	"slices"
	"strings"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// The tenant record's declaration of who may enter a tenant (signin.TenantDeclaration),
// kept as data of the application rather than deploy configuration: the record's
// directory-only column (@directoryOnly, a BOOL saying the tenant's people enter
// through its directories only, so a password does not reach it) and the directories
// record (@tenantDirectory), the tenant-scoped table whose row is the directory a
// tenant admits its people through, naming the sign-in
// method (@directoryMethod: google, azure, workos, as sessioninfo.AuthMethod spells
// them) and the connection the method's identities carry (@directoryConnection: a
// Google Workspace by its domain, a Microsoft Entra tenant by its tenant id), and
// naming, when the record carries the column, the default seat (@directoryDefaultSeat:
// the role a person is seated with in the tenant at their first sign-in through the
// directory, NULL where the tenant admits a stranger from it by invitation only). A
// tenant has one directory and a directory is one tenant's: the schema holds both with
// a unique index each, which the generator requires, and the generated write paths
// refuse each first (resource.TenantDirectoryRules). A directory is never typed in:
// the method and the connection are off the wire's writes, and the record's create
// stamps them from the directory the caller is signed in through. The
// generator holds the shape at capture, so the generated constructors
// (New<Record>Roster with the declaration's columns, New<Record>DeclarationReader for
// the sign-in policy, New<Record>DirectoryReader for the directory admission,
// New<Record>DirectoryRules and Set<Record>Directory for the writes) and the
// write paths that keep every instance's copy current render against a declaration
// that can serve as one.

// resolveTenantDeclaration reads a table-backed struct's declaration annotations:
// @tenantDirectory on the struct, @directoryOnly, @directoryMethod, @directoryConnection
// and @directoryDefaultSeat on its fields, each refused where it cannot mean anything,
// and the record's @tenantName, the name the tenant list carries, beside them.
// The rules that read across structs (one directories record, the record it belongs
// to) run once every struct is extracted (rejectSecondTenantDirectory,
// validateTenantDeclaration). The scope and the tenant record resolve first.
func resolveTenantDeclaration(res *resourceInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	if annotations.Struct.Has(tenantDirectoryKeyword) {
		switch {
		case res.IsTenant:
			return errors.Newf("struct %s: @%s on the tenant record; the directories record is a tenant-scoped resource of its own, one row per directory a tenant admits its people through", res.Name(), tenantDirectoryKeyword)
		case !res.IsDomainScoped():
			return errors.Newf("struct %s: @%s on a global resource; the directories record is tenant-scoped (@%s(%s)), since a tenant's directories are its own and its administrator declares them", res.Name(), tenantDirectoryKeyword, permissionScopeKeyword, "domain")
		}
		res.IsTenantDirectory = true
	}
	for i, field := range pStruct.Fields() {
		fieldAnnotations := annotations.Fields[i]
		switch {
		case fieldAnnotations.Has(directoryOnlyKeyword):
			if !res.IsTenant {
				return errors.Newf("struct %s field %s: @%s on a resource that is not the tenant record; the directory-only flag is the tenant record's column, so annotate the @%s record's BOOL column", res.Name(), field.Name(), directoryOnlyKeyword, tenantKeyword)
			}
			if !isBoolKinded(field.GoType()) {
				return errors.Newf("struct %s field %s: @%s on a %s column; the directory-only flag is a BOOL, true when the tenant's people enter through its directories only", res.Name(), field.Name(), directoryOnlyKeyword, field.Type())
			}
			if res.DirectoryOnlyField != nil {
				return errors.Newf("struct %s field %s: @%s on a second column; field %s carries it already, and a tenant has one directory-only flag", res.Name(), field.Name(), directoryOnlyKeyword, res.DirectoryOnlyField.Name())
			}
			flag := res.fieldNamed(field.Name())
			if flag == nil {
				return errors.Newf("struct %s field %s: @%s on a field the resource does not carry as a column; the directory-only flag is the tenant record's BOOL column", res.Name(), field.Name(), directoryOnlyKeyword)
			}
			flag.IsDirectoryOnly = true
			res.DirectoryOnlyField = flag
		case fieldAnnotations.Has(tenantNameKeyword):
			if err := res.setTenantNameColumn(field); err != nil {
				return err
			}
		case fieldAnnotations.Has(directoryMethodKeyword):
			if err := res.setDirectoryColumn(&res.DirectoryMethodField, field, directoryMethodKeyword, "the sign-in method a directory is reached through"); err != nil {
				return err
			}
		case fieldAnnotations.Has(directoryConnectionKeyword):
			if err := res.setDirectoryColumn(&res.DirectoryConnectionField, field, directoryConnectionKeyword, "the connection a directory's identities carry"); err != nil {
				return err
			}
		case fieldAnnotations.Has(directoryDefaultSeatKeyword):
			if err := res.setDirectorySeatColumn(field); err != nil {
				return err
			}
		}
	}

	return nil
}

// setTenantNameColumn records the tenant record's name column (@tenantName), refusing it
// off the record, on a column that is neither a string nor a nullable string, and
// declared twice.
func (r *resourceInfo) setTenantNameColumn(field *parser.Field) error {
	switch {
	case !r.IsTenant:
		return errors.Newf("struct %s field %s: @%s on a resource that is not the tenant record; a tenant's name is the @%s record's column", r.Name(), field.Name(), tenantNameKeyword, tenantKeyword)
	case !isStringOrNullableString(field.GoType()):
		return errors.Newf("struct %s field %s: @%s on a %s column; a tenant's name is a string or a nullable string (*string)", r.Name(), field.Name(), tenantNameKeyword, field.Type())
	case r.TenantNameField != nil:
		return errors.Newf("struct %s field %s: @%s on a second column; field %s carries it already, and a tenant has one name", r.Name(), field.Name(), tenantNameKeyword, r.TenantNameField.Name())
	}
	r.TenantNameField = r.fieldNamed(field.Name())
	if r.TenantNameField == nil {
		return errors.Newf("struct %s field %s: @%s on a column the schema does not hold", r.Name(), field.Name(), tenantNameKeyword)
	}

	return nil
}

// setDirectorySeatColumn records the directories record's default seat column
// (@directoryDefaultSeat), refusing it off the record, on a column that is neither a
// string nor a nullable string, and declared twice.
func (r *resourceInfo) setDirectorySeatColumn(field *parser.Field) error {
	switch {
	case !r.IsTenantDirectory:
		return errors.Newf("struct %s field %s: @%s on a resource that is not the directories record; the default seat is the @%s record's column, so annotate the record's struct @%s first", r.Name(), field.Name(), directoryDefaultSeatKeyword, tenantDirectoryKeyword, tenantDirectoryKeyword)
	case !isStringOrNullableString(field.GoType()):
		return errors.Newf("struct %s field %s: @%s on a %s column; the default seat is a role name, a string or a nullable string (*string), NULL where the tenant admits the directory's strangers by invitation only", r.Name(), field.Name(), directoryDefaultSeatKeyword, field.Type())
	case r.DirectoryDefaultSeatField != nil:
		return errors.Newf("struct %s field %s: @%s on a second column; field %s carries it already", r.Name(), field.Name(), directoryDefaultSeatKeyword, r.DirectoryDefaultSeatField.Name())
	}
	r.DirectoryDefaultSeatField = r.fieldNamed(field.Name())
	if r.DirectoryDefaultSeatField == nil {
		return errors.Newf("struct %s field %s: @%s on a column the schema does not hold", r.Name(), field.Name(), directoryDefaultSeatKeyword)
	}

	return nil
}

// isStringOrNullableString reports whether the type is a string or a pointer to one.
func isStringOrNullableString(t types.Type) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if p, isPointer := t.(*types.Pointer); isPointer {
		t = p.Elem()
	}

	return isStringKinded(t)
}

// setDirectoryColumn records one of the directories record's two columns, refusing it
// off the record, on a column that is not a string, and declared twice.
func (r *resourceInfo) setDirectoryColumn(dest **resourceField, field *parser.Field, keyword, meaning string) error {
	switch {
	case !r.IsTenantDirectory:
		return errors.Newf("struct %s field %s: @%s on a resource that is not the directories record; %s is the @%s record's column, so annotate the record's struct @%s first", r.Name(), field.Name(), keyword, meaning, tenantDirectoryKeyword, tenantDirectoryKeyword)
	case !isStringKinded(field.GoType()):
		return errors.Newf("struct %s field %s: @%s on a %s column; %s is a string", r.Name(), field.Name(), keyword, field.Type(), meaning)
	case *dest != nil:
		return errors.Newf("struct %s field %s: @%s on a second column; field %s carries it already", r.Name(), field.Name(), keyword, (*dest).Name())
	}
	*dest = r.fieldNamed(field.Name())
	if *dest == nil {
		return errors.Newf("struct %s field %s: @%s on a column the schema does not hold", r.Name(), field.Name(), keyword)
	}
	// A directory is never typed in: the wire writes neither column.
	(*dest).IsDirectoryKey = true

	return nil
}

// fieldNamed returns the resource's field by its Go name, nil when the struct's field
// was refused at capture (a spanner tag naming no column).
func (r *resourceInfo) fieldNamed(name string) *resourceField {
	for _, f := range r.Fields {
		if f.Name() == name {
			return f
		}
	}

	return nil
}

// isBoolKinded reports whether the type's underlying type is bool.
func isBoolKinded(t types.Type) bool {
	if t == nil {
		return false
	}
	basic, ok := t.Underlying().(*types.Basic)

	return ok && basic.Kind() == types.Bool
}

// SignalsTenants reports whether the resource's generated write paths keep every
// instance's tenant roster current after their commit: the tenant record, whose rows
// are the tenants and whose directory-only flag is part of what each declares, and the
// directories record, whose rows are the rest of the declaration.
func (r *resourceInfo) SignalsTenants() bool {
	return r.IsTenant || r.IsTenantDirectory
}

// HasDeclaration reports whether the tenant record declares who may enter a tenant:
// a directories record is declared in its package (set by validateTenantDeclaration).
func (r *resourceInfo) HasDeclaration() bool {
	return r.declaration != nil
}

// tenantDeclaration is what the tenant record's package declares about who may enter a
// tenant, resolved once every struct is extracted: the directories record with its
// two columns, and the record's directory-only column when it carries one.
type tenantDeclaration struct {
	Directories *resourceInfo
}

// rejectSecondTenantDirectory refuses a second @tenantDirectory in the package: a
// tenant record has one directories record, since its declaration is one thing.
func rejectSecondTenantDirectory(resources []*resourceInfo) error {
	var first *resourceInfo
	for _, res := range resources {
		if !res.IsTenantDirectory {
			continue
		}
		if first != nil {
			return errors.Newf("struct %s: @%s on a second struct; struct %s is the directories record already, and a tenant record has one", res.Name(), tenantDirectoryKeyword, first.Name())
		}
		first = res
	}

	return nil
}

// tenantDirectoryRecord returns the package's directories record, nil when none is
// declared.
func (r *resourceGenerator) tenantDirectoryRecord() *resourceInfo {
	for _, res := range r.resources {
		if res.IsTenantDirectory {
			return res
		}
	}

	return nil
}

// validateTenantDeclaration holds the package's declaration to its shape once the
// tenant record and the bindings are known: the directories record belongs to the
// tenant record through the bare @domain column (the tenant's key on the row itself,
// never a join path), carries both of its columns, is keyed by an id its create
// generates (a directory is set from a sign-in, which names no key), and its table
// holds the two rules of a tenant's directory with a unique index each
// (requireDirectoryIndexes); and the record's directory-only
// flag needs the directories record, since a tenant whose people enter through its
// directories only needs directories to enter through. With the declaration whole the
// record carries it, so the generated constructors and write paths read it.
func (r *resourceGenerator) validateTenantDeclaration() error {
	record := r.tenantRecord()
	directories := r.tenantDirectoryRecord()
	if directories == nil {
		if record != nil && record.DirectoryOnlyField != nil {
			return errors.Newf("struct %s field %s: @%s with no directories record in the package; a tenant whose people enter through its directories only needs directories to enter through, so declare the directories record (@%s) or drop the flag", record.Name(), record.DirectoryOnlyField.Name(), directoryOnlyKeyword, tenantDirectoryKeyword)
		}

		return nil
	}
	if record == nil {
		// Unreachable past resolveTenantRecord, which refuses a tenant-scoped resource
		// with no record; kept so the rule reads whole.
		return errors.Newf("struct %s: %s", directories.Name(), tenantNeeded)
	}
	if directories.DomainBinding == nil || len(directories.DomainBinding.Path) > 0 {
		return errors.Newf("struct %s: @%s needs the bare @%s column, the tenant's key on the row itself, since a directory is the tenant's own declaration; a join path (@%s(via: ...)) is refused", directories.Name(), tenantDirectoryKeyword, domainKeyword, domainKeyword)
	}
	if column := directories.DomainBinding.Anchor; column.Type() != impulseUUIDGoType && column.Type() != "string" {
		return errors.Newf("struct %s field %s: the @%s column of the @%s record is %s; it holds the tenant's key, an id, as %s or as its text (string), since the create-your-tenant route writes the creator's directory under the id it generated", directories.Name(), column.Name(), domainKeyword, tenantDirectoryKeyword, column.Type(), impulseUUIDGoType)
	}
	if directories.DirectoryMethodField == nil {
		return errors.Newf("struct %s: @%s with no @%s column; annotate the string column holding the sign-in method a directory is reached through (google, azure, workos)", directories.Name(), tenantDirectoryKeyword, directoryMethodKeyword)
	}
	if directories.DirectoryConnectionField == nil {
		return errors.Newf("struct %s: @%s with no @%s column; annotate the string column holding the connection a directory's identities carry (a Google Workspace by its domain, a Microsoft Entra tenant by its tenant id)", directories.Name(), tenantDirectoryKeyword, directoryConnectionKeyword)
	}
	if !directories.PrimaryKeyIsGeneratedUUID() {
		return errors.Newf("struct %s: the @%s record's key is not an id its create generates; a directory is set from a sign-in, which names no key, so key the record by one %s column (Id)", directories.Name(), tenantDirectoryKeyword, impulseUUIDGoType)
	}
	if err := r.requireDirectoryIndexes(directories); err != nil {
		return err
	}
	record.declaration = &tenantDeclaration{Directories: directories}
	directories.directoryOf = record

	return nil
}

// requireDirectoryIndexes holds the directories record's table to the two rules of a
// tenant's directory, which the schema keeps whatever writes the row: a unique index on
// the tenant column alone (a tenant has one directory) and a unique index on the method
// and the connection together (a directory is one tenant's). The generated write paths
// refuse each first with a sentence a person can act on; the indexes are what holds
// when two writes race, or when a row is written by hand.
func (r *resourceGenerator) requireDirectoryIndexes(directories *resourceInfo) error {
	table := r.pluralize(directories.Name())
	tenant, method, connection := spannerColumn(directories.DomainBinding.Anchor), spannerColumn(directories.DirectoryMethodField), spannerColumn(directories.DirectoryConnectionField)
	if !r.hasUniqueIndexOn(table, tenant) {
		return errors.Newf("struct %s: table %s has no unique index on %s alone; a tenant has one directory, which the schema holds: CREATE UNIQUE INDEX %sBy%s ON %s(%s)", directories.Name(), table, tenant, table, tenant, table, tenant)
	}
	if !r.hasUniqueIndexOn(table, method, connection) {
		return errors.Newf("struct %s: table %s has no unique index on %s and %s together; a directory belongs to one tenant, which the schema holds: CREATE UNIQUE INDEX %sBy%s%s ON %s(%s, %s)", directories.Name(), table, method, connection, table, method, connection, table, method, connection)
	}

	return nil
}

// hasUniqueIndexOn reports whether the table carries a unique index whose key is
// exactly the columns, in any order: what makes the database refuse a second row with
// the same values in them. A table the schema does not hold has none.
func (r *resourceGenerator) hasUniqueIndexOn(table string, columns ...string) bool {
	meta, ok := r.tableMap[table]
	if !ok {
		return false
	}
	for _, index := range meta.Indexes {
		if !index.Unique || index.Search || len(index.Key) != len(columns) {
			continue
		}
		covered := 0
		for _, key := range index.Key {
			if slices.Contains(columns, key.Column) {
				covered++
			}
		}
		if covered == len(columns) {
			return true
		}
	}

	return false
}

// tenantDirectoryWrite is the directories record as the generated Set<Record>Directory
// writes a tenant's directory: the setters of its tenant, method and connection
// columns, the tenant column's Go type with the parameter that carries it, and the
// expression that reads the parameter as the tenant's domain.
type tenantDirectoryWrite struct {
	TenantField     string
	TenantType      string
	TenantParam     string
	TenantDomain    string
	MethodField     string
	ConnectionField string
}

// tenantDirectoryWriteData reads the directories record's write shape off the tenant
// record, nil where the record declares none. The tenant column holds the tenant's key
// as an id or as its text (validateTenantDeclaration), so the domain is the id's text
// or the string itself.
func tenantDirectoryWriteData(record *resourceInfo) *tenantDirectoryWrite {
	if !record.HasDeclaration() {
		return nil
	}
	directories := record.declaration.Directories
	column := directories.DomainBinding.Anchor
	const param = "tenant"
	domain := "accesstypes.Domain(" + param + ")"
	if column.Type() == impulseUUIDGoType {
		domain = "accesstypes.Domain(" + param + ".String())"
	}

	return &tenantDirectoryWrite{
		TenantField:     column.Name(),
		TenantType:      column.Type(),
		TenantParam:     param,
		TenantDomain:    domain,
		MethodField:     directories.DirectoryMethodField.Name(),
		ConnectionField: directories.DirectoryConnectionField.Name(),
	}
}

// directoryTenantWord is what a refusal of the directory's rules calls one tenant: the
// record's label where it declares one, else its name in words, in lower case, as a
// sentence carries it ("sector", "ship class").
func (r *resourceGenerator) directoryTenantWord(record *resourceInfo) string {
	return strings.ToLower(r.tenantWord(record).One)
}

// DirectoryRules is the expression of the rules of a tenant's directory the
// directories record's write paths ask: a call of New<Record>DirectoryRules, which the
// tenant record's roster file declares in the handler package. It is a call and never
// a package variable: a handler file that named a variable another file declares would
// read to the import resolution as naming a package. Empty on every other resource.
func (r *resourceInfo) DirectoryRules() string {
	if r.directoryOf == nil {
		return ""
	}

	return "New" + r.directoryOf.Name() + "DirectoryRules()"
}

// DirectoryTenantWord is the word for one tenant in the comments the directories
// record's write paths carry: the tenant record's name in lower-case words. Empty on
// every other resource.
func (r *resourceInfo) DirectoryTenantWord() string {
	if r.directoryOf == nil {
		return ""
	}

	return strings.ToLower(strcase.ToCase(r.directoryOf.Name(), strcase.LowerCase, ' '))
}

// tenantDeclarationColumns is the declaration as the generated constructors hand it to
// resource.WithTenantDeclarations, resource.NewTenantDeclarationReader and
// resource.NewTenantDirectoryReader: the column names behind the record's annotated
// fields, the default seat's empty where the record carries none.
type tenantDeclarationColumns struct {
	DirectoryOnly     string
	DirectoriesTable  string
	TenantColumn      string
	MethodColumn      string
	ConnectionColumn  string
	DefaultSeatColumn string
}

// declarationColumns reads the declaration's columns off the record, nil when the
// record declares none.
func (r *resourceGenerator) declarationColumns(record *resourceInfo) *tenantDeclarationColumns {
	if !record.HasDeclaration() {
		return nil
	}
	directories := record.declaration.Directories
	columns := &tenantDeclarationColumns{
		DirectoriesTable: r.pluralize(directories.Name()),
		TenantColumn:     spannerColumn(directories.DomainBinding.Anchor),
		MethodColumn:     spannerColumn(directories.DirectoryMethodField),
		ConnectionColumn: spannerColumn(directories.DirectoryConnectionField),
	}
	if record.DirectoryOnlyField != nil {
		columns.DirectoryOnly = spannerColumn(record.DirectoryOnlyField)
	}
	if directories.DirectoryDefaultSeatField != nil {
		columns.DefaultSeatColumn = spannerColumn(directories.DirectoryDefaultSeatField)
	}

	return columns
}

// spannerColumn is the column a field maps to, as its spanner tag names it.
func spannerColumn(field *resourceField) string {
	column, _ := field.LookupTag(spannerTagKey)

	return column
}
