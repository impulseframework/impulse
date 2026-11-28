package generation

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// deleteFixtureTables is the schema of the deletefixture package: a client's projects
// and invoices by foreign key, a project's tasks by foreign key and its notes by
// interleave, and a task's comments by foreign key. A ledger holds a client's key with
// no foreign key on it, and a supplier is under nothing. No delete rule is on any
// foreign key: a row is deleted in code. Tasks carry an index besides their key.
func deleteFixtureTables() map[string]*tableMetadata {
	pk := columnMeta{IsPrimaryKey: true}
	key := func(ordinal int64) columnMeta {
		return columnMeta{IsPrimaryKey: true, OrdinalPosition: ordinal, KeyOrdinalPosition: ordinal}
	}
	foreignKey := func(table string) columnMeta {
		return columnMeta{IsForeignKey: true, ReferencedTable: table, ReferencedColumn: "Id", OrdinalPosition: 1}
	}
	to := func(column, table string) []foreignKeyMeta {
		return []foreignKeyMeta{{Name: "FK_" + column, Columns: []string{column}, ReferencedTable: table, ReferencedColumns: []string{"Id"}, DeleteRule: "NO ACTION"}}
	}

	return map[string]*tableMetadata{
		"Clients": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": {OrdinalPosition: 1}}},
		"Projects": {
			PkCount: 1, ForeignKeys: to("ClientId", "Clients"),
			Columns: map[string]columnMeta{"Id": pk, "ClientId": foreignKey("Clients"), "Name": {OrdinalPosition: 2}},
		},
		"Tasks": {
			PkCount: 1, ForeignKeys: to("ProjectId", "Projects"),
			Columns: map[string]columnMeta{"Id": pk, "ProjectId": foreignKey("Projects"), "Title": {OrdinalPosition: 2}},
			Indexes: []indexMeta{
				{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}}},
				{Name: "TasksByProject", Key: []indexColumn{{Column: "ProjectId"}}},
			},
		},
		"TaskComments": {
			PkCount: 1, ForeignKeys: to("TaskId", "Tasks"),
			Columns: map[string]columnMeta{"Id": pk, "TaskId": foreignKey("Tasks"), "Body": {OrdinalPosition: 2}},
		},
		"ProjectNotes": {PkCount: 2, IsInterleaved: true, ParentTable: "Projects", Columns: map[string]columnMeta{
			"Id": key(0), "Sequence": key(1), "Body": {OrdinalPosition: 2},
		}},
		"Invoices": {
			PkCount: 1, ForeignKeys: to("ClientId", "Clients"),
			Columns: map[string]columnMeta{"Id": pk, "ClientId": foreignKey("Clients"), "Number": {OrdinalPosition: 2}},
		},
		"Ledgers":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "ClientId": {OrdinalPosition: 1}, "Balance": {OrdinalPosition: 2}}},
		"Suppliers": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": {OrdinalPosition: 1}}},
	}
}

// Test_validateDeleteType_isReadFromTheAnnotation pins the reading of
// @validateDeleteType: the resource that declares it carries the named type, and a
// resource that does not carries none.
func Test_validateDeleteType_isReadFromTheAnnotation(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "deletefixture"))

	tests := []struct {
		name       string
		structName string
		want       string
	}{
		{name: "the declared rule is read", structName: "Invoice", want: "InvoiceDeleteRule"},
		{name: "a resource that declares none has none", structName: "Client"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := structs[tt.structName]
			if s == nil {
				t.Fatalf("struct %q not found in fixture package", tt.structName)
			}
			annotations, err := genlang.NewScanner(resourceKeywords()).ScanStruct(s)
			if err != nil {
				t.Fatalf("genlang.Scanner.ScanStruct(%s) error = %v", tt.structName, err)
			}
			res := &resourceInfo{TypeInfo: s.TypeInfo}
			if err := resolveResourceAnnotations(res, annotations); err != nil {
				t.Fatalf("resolveResourceAnnotations(%s) error = %v", tt.structName, err)
			}
			if res.ValidateDeleteType != tt.want {
				t.Errorf("ValidateDeleteType = %q, want %q", res.ValidateDeleteType, tt.want)
			}
			if got := res.HasValidateDeleteType(); got != (tt.want != "") {
				t.Errorf("HasValidateDeleteType() = %v, want %v", got, tt.want != "")
			}
		})
	}
}

// Test_resourceFileTemplate_validateDeleteType pins what the delete patch of a resource
// with a delete rule is written with: both of its constructors hand the patch set the
// rule, and the rule is asked through the declared type's Validate. A resource with no
// rule is written with neither, so its delete asks nothing.
func Test_resourceFileTemplate_validateDeleteType(t *testing.T) {
	t.Parallel()

	c := &client{tableMap: deleteFixtureTables()}
	structs := fixtureStructs(loadFixture(t, "deletefixture"))

	tests := []struct {
		name       string
		structName string
		rule       string
	}{
		{name: "a resource with a delete rule registers it", structName: "Invoice", rule: "InvoiceDeleteRule"},
		{name: "a resource with none registers nothing", structName: "Client"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := structs[tt.structName]
			if s == nil {
				t.Fatalf("struct %q not found in fixture package", tt.structName)
			}
			table, err := c.tableMetadataFor(tt.structName)
			if err != nil {
				t.Fatalf("tableMetadataFor(%s) error = %v", tt.structName, err)
			}
			annotations, err := genlang.NewScanner(resourceKeywords()).ScanStruct(s)
			if err != nil {
				t.Fatalf("genlang.Scanner.ScanStruct(%s) error = %v", tt.structName, err)
			}
			res := &resourceInfo{TypeInfo: s.TypeInfo, PkCount: table.PkCount}
			if err := resolveResourceAnnotations(res, annotations); err != nil {
				t.Fatalf("resolveResourceAnnotations(%s) error = %v", tt.structName, err)
			}
			res.Fields, err = newResourceFields(res, s, table)
			if err != nil {
				t.Fatalf("newResourceFields(%s) error = %v", tt.structName, err)
			}

			r := &resourceGenerator{client: c}
			output, err := r.generateTemplateOutput("resourceFileTemplate", resourceFileTemplate, &resourceFileData{
				Source:   "fixture",
				Package:  "resources",
				Resource: res,
			})
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			rendered := string(output)
			name := tt.structName

			register := "patch.registerValidateFunc()"
			method := "func (p *" + name + "DeletePatch) registerValidateFunc() {"
			if tt.rule == "" {
				for _, unwanted := range []string{register, method, "RegisterValidateDeleteFunc"} {
					if strings.Contains(rendered, unwanted) {
						t.Errorf("rendered %s contains %q, though it declares no delete rule", name, unwanted)
					}
				}

				return
			}

			// Both constructors: the one a handler calls with the request's patch set and
			// the one code calls with the row's key.
			if got := strings.Count(rendered, register); got != 2 {
				t.Errorf("rendered %s registers the delete rule %d times, want 2 (once in each constructor of the delete patch)", name, got)
			}
			for _, want := range []string{
				method,
				"p.patchSet.RegisterValidateDeleteFunc(func(ctx context.Context, txn resource.ReadWriteTransaction) error {",
				"return new(" + tt.rule + ").Validate(ctx, txn, p)",
			} {
				if !strings.Contains(rendered, want) {
					t.Errorf("rendered %s is missing %q", name, want)
				}
			}
		})
	}
}
