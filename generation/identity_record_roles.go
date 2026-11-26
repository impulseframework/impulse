package generation

import (
	"go/ast"
	"go/build"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
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
type identityHeldRecord struct {
	name     string
	resource accesstypes.Resource
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
	marked, err := heldRecordsIn(r.resource.Dir())
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
		for _, name := range marked {
			record := accesstypes.Resource(r.pluralize(name))
			if named[record] {
				inst.heldRecords = append(inst.heldRecords, identityHeldRecord{name: name, resource: record})
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

// heldRecordsIn reads the names of the structs a package's sources mark as records a role
// can be held on (@holdsRoles), in name order, none where the directory holds no package.
// Only the syntax is read, as tenantRecordIn reads it: the files the default build takes,
// each struct's comment scanned as the extraction scans it.
func heldRecordsIn(dir string) ([]string, error) {
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

	var records []string
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
				if _, ok := typeSpec.Type.(*ast.StructType); ok && structCommentHas(gen, typeSpec, holdsRolesKeyword) {
					records = append(records, typeSpec.Name.Name)
				}
			}
		}
	}
	slices.Sort(records)

	return records, nil
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
