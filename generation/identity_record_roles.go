package generation

import (
	"go/ast"
	"go/build"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// The roles held on one record in an auth's generated management. Where the application
// marks a record as one a role can be held on (@holdsRoles) and the auth's role file holds
// a role on it (heldOn), the generated management declares the permission store's two
// tables of the roles held on records, a person's and a group's, and its store reads and
// writes their rows through the two declarations:
//
//   - the declarations are a tenant's rows, by the record's tenant (DomainId), with no
//     route of their own: the auth's management writes them through the identity
//     package's rules, and each write leaves its change event;
//   - the groups' declaration is written where the instance keeps groups, and a group's
//     delete takes the roles the group holds on records with its other roles;
//   - an instance whose role file holds no role on a marked record declares neither,
//     though the schema holds the tables, which then hold no row: its store reads nothing
//     and refuses a write, saying what is missing.

// The kinds of the tables of the roles held on one record.
const (
	identityRecordUserRoles  identityTable = "recordUserRoles"
	identityRecordGroupRoles identityTable = "recordGroupRoles"
)

// The suffixes of the declarations over the tables of the roles held on one record: the
// auth's name, then the suffix, is the struct, whose plural is the table.
const (
	recordUserRoleSuffix  = "RecordUserRole"
	recordGroupRoleSuffix = "RecordGroupRole"
)

// identityHeldRecord is a record of the application's that a role of the instance's role
// file can be held on: its struct, and its resource name, which the role file's heldOn
// names it by.
//
// keyType is the record's key field's type as another package writes it, with the imports
// that spelling needs (keyImports): the methods that share one of the records name its row
// by a field of that type (identity_record_roles.go). It is "" where the schema holds no
// table for the record, its key is more than one column, or the type cannot be named from
// another package; such a record's sharing is not written, and the load refuses the mark
// of a record whose key is not one text column (refuseRecordMark).
type identityHeldRecord struct {
	name       string
	resource   accesstypes.Resource
	keyType    string
	keyImports []string
}

// findRecordTables marks the tables of the roles held on a record that the schema holds:
// the people's, and the groups' where the instance keeps groups, so it runs once the
// group tables are settled (settleGroupTables). An instance that keeps groups has both or
// neither: the permission store migrates the two together, and a schema that holds one
// alone is refused once the packages are loaded, where a record is marked
// (collectRecordRoleRegistrations).
func (i *identityInstance) findRecordTables(tables map[string]*tableMetadata) {
	_, people := tables[i.tableName(identityRecordUserRoles)]
	_, groups := tables[i.tableName(identityRecordGroupRoles)]
	if i.keepsGroups() && people != groups {
		return
	}
	if people {
		i.tables[identityRecordUserRoles] = true
	}
	if groups && i.keepsGroups() {
		i.tables[identityRecordGroupRoles] = true
	}
}

// holdsRecordRoles reports whether the instance keeps the roles held on a record: its role
// file holds a role on a record the application marks, and the schema holds the people's
// table.
func (i *identityInstance) holdsRecordRoles() bool {
	return len(i.heldRecords) > 0 && i.has(identityRecordUserRoles)
}

// keepsRecordGroupRoles reports whether the instance keeps the roles groups hold on a
// record as well: it keeps the roles held on a record, and it keeps groups.
func (i *identityInstance) keepsRecordGroupRoles() bool {
	return i.holdsRecordRoles() && i.has(identityRecordGroupRoles)
}

// recordRoleDeclarations are the declarations over the tables of the roles held on a
// record, for an instance whose role file holds a role on a marked record: none for
// another. writeIdentitySources writes each where the instance has its table, as it
// writes the other declarations.
func (i *identityInstance) recordRoleDeclarations() []identityDeclaration {
	if len(i.heldRecords) == 0 {
		return nil
	}

	return []identityDeclaration{
		{kind: identityRecordUserRoles, suffix: recordUserRoleSuffix, template: identityRecordUserRolesTemplate},
		{kind: identityRecordGroupRoles, suffix: recordGroupRoleSuffix, template: identityRecordGroupRolesTemplate},
	}
}

// readHeldRecords reads, before the packages are loaded, the records of the resources
// package a role of each instance's role file can be held on: the structs whose comment
// carries @holdsRoles, read off the sources as the tenant record is, each kept for an
// instance whose role file names it in a role's heldOn. Once the packages are loaded the
// extraction refuses a mark on a struct that cannot carry it, and a heldOn that names no
// marked record.
func (r *resourceGenerator) readHeldRecords(instances []*identityInstance) error {
	marked, err := r.heldRecordsIn(r.resource.Dir())
	if err != nil {
		return errors.Wrapf(err, "GenerateIdentity(%q): reading the records that hold roles (@%s) from the sources of %s", r.identityTree.Dir(), holdsRolesKeyword, r.resource.Dir())
	}
	if len(marked) == 0 {
		return nil
	}
	for _, inst := range instances {
		named, err := r.heldOnNames(inst)
		if err != nil {
			return err
		}
		for _, record := range marked {
			if named[record.resource] {
				inst.heldRecords = append(inst.heldRecords, record)
			}
		}
	}

	return nil
}

// heldOnNames are the records the instance's role file names in a role's heldOn. The file
// is read where the auth package lives in the main module, as refuseReservedRole reads
// it: an auth package outside it, and one that holds no role file yet, names none, and a
// file that does not parse is left to the engine and to impulse check, which say why.
func (r *resourceGenerator) heldOnNames(inst *identityInstance) (map[accesstypes.Resource]bool, error) {
	dir, ok := r.moduleDir(inst.importPath)
	if !ok {
		return nil, nil
	}
	file := filepath.Join(dir, identityRolesFile)
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, errors.Wrapf(err, "Auth(%q): os.ReadFile(%q)", inst.importPath, file)
	}
	named := make(map[accesstypes.Resource]bool)
	for _, role := range definedRoles(data) {
		if role == nil {
			continue
		}
		for _, record := range role.HeldOn {
			named[record] = true
		}
	}

	return named, nil
}

// heldRecordsIn reads the structs a package's sources mark as records a role can be held
// on (@holdsRoles), in name order, none where the directory holds no package, each with
// its key field's type where it can be read (identityHeldRecord.keyType). Only the syntax
// is read, as tenantRecordIn reads it: the files the default build takes, each struct's
// comment scanned as the extraction scans it.
func (r *resourceGenerator) heldRecordsIn(dir string) ([]identityHeldRecord, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}

		return nil, errors.Wrap(err, "os.Stat()")
	}
	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		var noGo *build.NoGoError
		if errors.As(err, &noGo) {
			return nil, nil
		}

		return nil, errors.Wrap(err, "build.ImportDir()")
	}
	resourcesPath, named := r.modulePath(r.resource)

	var records []identityHeldRecord
	fset := token.NewFileSet()
	for _, name := range pkg.GoFiles {
		file, err := goparser.ParseFile(fset, filepath.Join(dir, name), nil, goparser.ParseComments|goparser.SkipObjectResolution)
		if err != nil {
			return nil, errors.Wrap(err, "parser.ParseFile()")
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
				if !ok || !structCommentHas(gen, typeSpec, holdsRolesKeyword) {
					continue
				}
				record := identityHeldRecord{name: typeSpec.Name.Name, resource: accesstypes.Resource(r.pluralize(typeSpec.Name.Name))}
				if named {
					naming := &typeNaming{file: file, pkgName: pkg.Name, pkgPath: resourcesPath, imports: map[string]bool{}}
					record.keyType, err = r.heldRecordKeyType(structType, string(record.resource), naming)
					if err != nil {
						return nil, errors.Wrapf(err, "struct %s", record.name)
					}
					record.keyImports = sortedImports(naming.imports)
				}
				records = append(records, record)
			}
		}
	}
	slices.SortFunc(records, func(a, b identityHeldRecord) int { return strings.Compare(a.name, b.name) })

	return records, nil
}

// heldRecordKeyType reads the type of a marked record's key field off its struct, as
// another package names it: the field whose column is the table's one key column. It is
// "" where the schema holds no such table or its key is more than one column, or the type
// cannot be named from another package.
func (r *resourceGenerator) heldRecordKeyType(structType *ast.StructType, table string, naming *typeNaming) (string, error) {
	meta, ok := r.tableMap[table]
	if !ok {
		return "", nil
	}
	key := meta.keyColumns()
	if len(key) != 1 {
		return "", nil
	}
	for _, field := range structType.Fields.List {
		if len(field.Names) != 1 || field.Tag == nil {
			continue
		}
		unquoted, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			return "", errors.Wrapf(err, "field %s: its struct tag", field.Names[0].Name)
		}
		if reflect.StructTag(unquoted).Get(spannerTagKey) != key[0] {
			continue
		}
		typeText, ok := naming.name(field.Type)
		if !ok {
			return "", nil
		}

		return typeText, nil
	}

	return "", nil
}

// RecordUserRole is the struct of the declaration over the table of the roles people hold
// on a record.
func (d *identitySourceData) RecordUserRole() string {
	return d.auth + recordUserRoleSuffix
}

// RecordGroupRole is the struct of the declaration over the table of the roles groups hold
// on a record.
func (d *identitySourceData) RecordGroupRole() string {
	return d.auth + recordGroupRoleSuffix
}

// HoldsRecordRoles reports whether the instance keeps the roles held on a record.
func (d *identitySourceData) HoldsRecordRoles() bool {
	return d.holdsRecordRoles()
}

// KeepsRecordGroupRoles reports whether the instance keeps the roles groups hold on a
// record.
func (d *identitySourceData) KeepsRecordGroupRoles() bool {
	return d.keepsRecordGroupRoles()
}

// A record's sharing. Where the instance keeps the roles held on a record and gives a
// tenant's roles by hand, it writes, for each record its role file holds a role on, the
// tenant's methods that give a person or a group a role on one of the records and take it
// away, the list those methods pick a role from, and the list of who holds what on a
// record (recordSharingSources):
//
//   - Give<Auth>RoleOn<Record> and Take<Auth>RoleOn<Record>, a person's, and
//     Give<Auth>GroupRoleOn<Record> and Take<Auth>GroupRoleOn<Record>, a group's where the
//     instance keeps groups: each names the record's row (@target), so the generated frame
//     locates the row in the request's tenant and judges the caller's grant against it, and
//     a caller who holds the method through a role on one record shares that record alone;
//   - <Auth><Record>Role, the roles the give methods' dialogs pick from: the roles the role
//     file holds on the record's kind that the caller could give with the dialog's method
//     (identity.Manager.GivableRecordRoles);
//   - <Auth><Record>Holder, a view of the tenant over the two tables: who holds what on a
//     record and has not ended, a person by their name as the tenant's people list shows
//     it and a group by its own. It is under its record (resolveRecordRoles), so a grant on
//     it held on one record lists that record's holders alone.

// The suffixes of a record's two lists, after the auth's name and the record's.
const (
	recordRoleChoiceSuffix = "Role"
	recordHolderSuffix     = "Holder"
)

// sharesRecords reports whether the instance writes the sharing of its records: it keeps
// the roles held on a record, the application has a tenant record, the tenant's roles are
// given by hand, and the tenant lists its people, whom a share picks a person from.
func (i *identityInstance) sharesRecords() bool {
	return i.holdsRecordRoles() && i.tenant != nil && !i.rolesSynced && i.hasList(listPeopleSuffix)
}

// sharedRecords are the records whose sharing the instance writes: every record of
// heldRecords whose key could be read, none where it shares no record.
func (i *identityInstance) sharedRecords() []identityHeldRecord {
	if !i.sharesRecords() {
		return nil
	}
	var records []identityHeldRecord
	for _, record := range i.heldRecords {
		if record.keyType != "" {
			records = append(records, record)
		}
	}

	return records
}

// recordSharing is the names of one record's sharing (recordSharingSources), each "" where
// the instance writes none: the group pair where it keeps no groups.
type recordSharing struct {
	Holders, Roles       string
	Give, Take           string
	GiveGroup, TakeGroup string
	record               identityHeldRecord
}

// sharing names one record's sharing.
func (i *identityInstance) sharing(record identityHeldRecord) recordSharing {
	names := recordSharing{
		Holders: i.auth + record.name + recordHolderSuffix,
		Roles:   i.auth + record.name + recordRoleChoiceSuffix,
		Give:    "Give" + i.auth + "RoleOn" + record.name,
		Take:    "Take" + i.auth + "RoleOn" + record.name,
		record:  record,
	}
	if i.keepsRecordGroupRoles() {
		names.GiveGroup = "Give" + i.auth + "GroupRoleOn" + record.name
		names.TakeGroup = "Take" + i.auth + "GroupRoleOn" + record.name
	}

	return names
}

// recordSharingSources are the source files of each shared record's sharing: the holders'
// view beside the tables it reads, and the roles' list and the methods beside the other
// methods.
func (r *resourceGenerator) recordSharingSources(inst *identityInstance) []identitySourceFile {
	var files []identitySourceFile
	for _, record := range inst.sharedRecords() {
		names := inst.sharing(record)
		file := func(dir packageDir, name, template string, needs ...identityTable) identitySourceFile {
			return identitySourceFile{dir: dir, name: name, template: template, needs: needs, tenant: true, record: &record}
		}
		files = append(files,
			file(inst.resources, fileStem(r.pluralize(names.Holders)), identityRecordHoldersTemplate, identityUsers, identityRecordUserRoles),
			file(inst.methods, fileStem(r.pluralize(names.Roles)), identityRecordRoleChoicesTemplate, identityRecordUserRoles),
			file(inst.methods, fileStem(names.Give), identityGiveRoleOnRecordTemplate, identityRecordUserRoles),
			file(inst.methods, fileStem(names.Take), identityTakeRoleOnRecordTemplate, identityRecordUserRoles),
		)
		if names.GiveGroup != "" {
			files = append(files,
				file(inst.methods, fileStem(names.GiveGroup), identityGiveGroupRoleOnRecordTemplate, identityRecordGroupRoles),
				file(inst.methods, fileStem(names.TakeGroup), identityTakeGroupRoleOnRecordTemplate, identityRecordGroupRoles),
			)
		}
	}

	return files
}

// recordWaysOf answers what puts a shared record's holders' view under its record, for
// the view of the name: the attribute over the view's RecordID, the way from one of its
// rows up to the record with no hop, so a grant held on one record reaches that record's
// rows of the view alone. Any other view is under nothing.
func (i *identityInstance) recordWaysOf(view string) ([]resource.AttributeData, []resource.RecordWayData) {
	for _, record := range i.sharedRecords() {
		if view != i.sharing(record).Holders {
			continue
		}
		attribute := resource.RecordWayAttribute(record.resource, 0)

		return []resource.AttributeData{{Name: attribute, Column: "RecordId", Type: resource.AttributeTypeString}},
			[]resource.RecordWayData{{Record: record.resource, Attribute: attribute}}
	}

	return nil, nil
}

// recordGivings are the methods of one tenant that give a role on one of its records by
// hand, whose role field picks from the record's roles: each shared record's person's
// method and group's method, asked with the role alone (the generated CouldGive).
func (i *identityInstance) recordGivings() []tenantGiving {
	var givings []tenantGiving
	for _, record := range i.sharedRecords() {
		names := i.sharing(record)
		givings = append(givings, tenantGiving{Method: names.Give, Fields: roleAloneFields})
		if names.GiveGroup != "" {
			givings = append(givings, tenantGiving{Method: names.GiveGroup, Fields: roleAloneFields})
		}
	}

	return givings
}

// RecordGivings are the methods of one tenant that give a role on one of its records by
// hand (recordGivings).
func (d *identitySourceData) RecordGivings() []tenantGiving {
	return d.recordGivings()
}

// TenantHandGivingMethods names, as a comment reads them, every method of one tenant whose
// role field picks a role the caller could give: the methods of TenantGivings, then those
// of RecordGivings.
func (d *identitySourceData) TenantHandGivingMethods() string {
	givings := slices.Concat(d.tenantGivings(), d.recordGivings())
	names := make([]string, 0, len(givings))
	for _, giving := range givings {
		names = append(names, giving.Method)
	}

	return strings.Join(names, ", ")
}

// Sharing names the sharing of the record the file shares.
func (d *identitySourceData) Sharing() recordSharing {
	return d.sharing(*d.HeldRecord)
}

// SharedRecord is the struct of the record the file shares (Mission).
func (d *identitySourceData) SharedRecord() string {
	return d.HeldRecord.name
}

// SharedResource is the resource name of the record the file shares (Missions), which the
// identity package's methods name its kind by.
func (d *identitySourceData) SharedResource() string {
	return string(d.HeldRecord.resource)
}

// SharedResourceLiteral is the resource name of the record the file shares as a SQL string
// literal.
func (d *identitySourceData) SharedResourceLiteral() string {
	return sqlStringLiteral(string(d.HeldRecord.resource))
}

// SharedKeyType is the type of the shared record's key field, as a method names the row.
func (d *identitySourceData) SharedKeyType() string {
	return d.HeldRecord.keyType
}

// SharedKeyImports are the imports SharedKeyType's spelling needs beyond the ones every
// method of a record's sharing imports already (the impulse package, for its ids).
func (d *identitySourceData) SharedKeyImports() []string {
	impulse := importLine("impulse", "github.com/impulseframework/impulse")

	return slices.DeleteFunc(slices.Clone(d.HeldRecord.keyImports), func(line string) bool { return line == impulse })
}

// SharedKeyText spells the key a request names a shared record by as text, the way the
// identity package's methods take it, from the request's field (m.RecordID).
func (d *identitySourceData) SharedKeyText() string {
	return recordKeyText(d.HeldRecord.keyType, "m.RecordID")
}

// recordKeyText spells a record's key, the Go value named by value of the key field's
// type, as the text a role held on the record names it by: the string itself, an id's
// text, or any other value's fmt spelling.
func recordKeyText(keyType, value string) string {
	switch keyType {
	case stringGoType:
		return value
	case impulseUUIDGoType:
		return value + ".String()"
	default:
		return "fmt.Sprint(" + value + ")"
	}
}

// The two kinds of holder of a role on a record, as the holders' view's MemberKind column
// spells them: a person and a group.
const (
	recordHolderPerson = "person"
	recordHolderGroup  = "group"
)

// HolderPerson is the holders' view's kind of a person's row, as a SQL string literal.
func (d *identitySourceData) HolderPerson() string { return sqlStringLiteral(recordHolderPerson) }

// HolderGroup is the holders' view's kind of a group's row, as a SQL string literal.
func (d *identitySourceData) HolderGroup() string { return sqlStringLiteral(recordHolderGroup) }

// TenantDisplayName is the name a person is shown by in the tenant's list of its people,
// read off the users table under u.
func (d *identitySourceData) TenantDisplayName() string { return identityTenantDisplayName }
