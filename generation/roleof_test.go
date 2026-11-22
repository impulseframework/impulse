package generation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
)

// roleOfFixtureTables are the tables of the roleOf fixture's table-backed structs.
func roleOfFixtureTables() map[string]*tableMetadata {
	primaryKey := []indexMeta{{Name: "PRIMARY_KEY", PrimaryKey: true, Unique: true, Key: []indexColumn{{Column: "Id"}}}}
	tables := map[string]*tableMetadata{
		"Seats": {PkCount: 1, Columns: map[string]columnMeta{
			"Id":     {IsPrimaryKey: true, SpannerType: "STRING(36)"},
			"Role":   {SpannerType: "STRING(MAX)"},
			"Backup": {SpannerType: "STRING(MAX)", IsNullable: true},
			"Roles":  {SpannerType: "ARRAY<STRING(MAX)>"},
			"Note":   {SpannerType: "STRING(MAX)"},
		}, Indexes: primaryKey},
		"Benches": {PkCount: 1, Columns: map[string]columnMeta{
			"Id":    {IsPrimaryKey: true, SpannerType: "STRING(36)"},
			"Count": {SpannerType: "INT64"},
		}, Indexes: primaryKey},
		"Stools": {PkCount: 1, Columns: map[string]columnMeta{
			"Id":   {IsPrimaryKey: true, SpannerType: "STRING(36)"},
			"Role": {SpannerType: "STRING(MAX)"},
		}, Indexes: primaryKey},
	}
	for _, table := range tables {
		table.deriveIndexFlags()
	}

	return tables
}

// Test_declareRoleOf pins the field annotation @roleOf at extraction, on every path that
// reads it. A table, a view and a computed resource take it on a string, a nullable
// string and a list of strings, and record the auth as written on the field, and a field
// that declares nothing records none. A field of another type, two auths named, and a
// request field are refused, each naming the struct and the field with its sentence.
func Test_declareRoleOf(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "roleoffixture"))
	const typeRefusal = "a field that holds a role's name is a string, a nullable string (*string) or a list of strings ([]string)"
	const argsRefusal = "@roleOf takes one argument, the name of the auth whose roles the field holds; got 2"
	tests := []struct {
		name       string
		structName string
		kind       string
		// wantDeclared are the fields that record the auth, by name, when the struct is
		// taken; wantErr is the refusal when it is not.
		wantDeclared []string
		wantErr      string
	}{
		{name: "a table takes a string, a nullable string and a list of strings", structName: "Seat", kind: resourceKeyword, wantDeclared: []string{"Role", "Backup", "Roles"}},
		{name: "a table refuses a number", structName: "Bench", kind: resourceKeyword, wantErr: "struct Bench field Count: @roleOf on an int64 field; " + typeRefusal},
		{name: "a table refuses two auths", structName: "Stool", kind: resourceKeyword, wantErr: "struct Stool field Role: " + argsRefusal},
		{name: "a view takes a string, a nullable string and a list of strings", structName: "SeatView", kind: virtualKeyword, wantDeclared: []string{"Role", "Backup", "Roles"}},
		{name: "a view refuses a boolean", structName: "BenchView", kind: virtualKeyword, wantErr: "struct BenchView field Open: @roleOf on a bool field; " + typeRefusal},
		{name: "a view refuses two auths", structName: "StoolView", kind: virtualKeyword, wantErr: "struct StoolView field Role: " + argsRefusal},
		{name: "a computed resource takes a string, a nullable string and a list of strings", structName: "SeatLedger", kind: computedKeyword, wantDeclared: []string{"Role", "Backup", "Roles"}},
		{name: "a computed resource refuses a list of numbers", structName: "BenchLedger", kind: computedKeyword, wantErr: "struct BenchLedger field Counts: @roleOf on a []int64 field; " + typeRefusal},
		{name: "a computed resource refuses two auths", structName: "StoolLedger", kind: computedKeyword, wantErr: "struct StoolLedger field Role: " + argsRefusal},
		{
			name: "a request field is refused", structName: "TakeSeat", kind: rpcKeyword,
			wantErr: "struct TakeSeat field Role: @roleOf on a request field; a form's role field picks from the list its @enumerate names, and that list's field carries @roleOf, so remove the annotation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{tableMap: roleOfFixtureTables()}
			pStruct := []*parser.Struct{structs[tt.structName]}
			declared := map[string]string{}
			var err error
			switch tt.kind {
			case resourceKeyword, virtualKeyword:
				var resources []*resourceInfo
				if tt.kind == resourceKeyword {
					resources, err = c.structsToResources(pStruct)
				} else {
					resources, err = c.structsToVirtualResources(pStruct)
				}
				if err == nil {
					for _, field := range resources[0].Fields {
						declared[field.Name()] = field.RoleOf()
					}
				}
			case computedKeyword:
				var computed []*computedResource
				computed, err = c.structsToCompResources(pStruct)
				if err == nil {
					for _, field := range computed[0].Fields {
						declared[field.Name()] = field.RoleOf()
					}
				}
			case rpcKeyword:
				_, err = c.structsToRPCMethods(pStruct)
			default:
				t.Fatalf("unknown struct kind %q", tt.kind)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("extraction error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("extraction error = %v, want none", err)
			}
			for name, auth := range declared {
				want := ""
				for _, field := range tt.wantDeclared {
					if field == name {
						want = "Staff"
					}
				}
				if auth != want {
					t.Errorf("field %s RoleOf() = %q, want %q", name, auth, want)
				}
			}
		})
	}
}

// Test_readRoleOfRoles pins the roles a field that holds a role's name may hold, read from
// its auth's role file: a tenant-scoped resource's field holds the file's tenant roles
// alone, and any other resource's every role of the file and the Super Administrator
// role, sorted. A file that is not there and one that does not parse define no role.
func Test_readRoleOfRoles(t *testing.T) {
	t.Parallel()

	const auth = "example.com/beacon/pkg/auth/staff"
	tests := []struct {
		name string
		// roles is the role file's content; empty writes no file.
		roles      string
		wantDomain []accesstypes.Role
		wantOther  []accesstypes.Role
	}{
		{
			name:       "a role file with global and tenant roles",
			roles:      `{"roles": {"global": [{"name": "Steward", "description": "Keeps.", "permissions": {}}], "domain": [{"name": "Warden", "description": "Guards.", "permissions": {}}, {"name": "Auditor", "description": "Reads.", "permissions": {}}]}}`,
			wantDomain: []accesstypes.Role{"Auditor", "Warden"},
			wantOther:  []accesstypes.Role{"Auditor", "Steward", "SuperAdministrator", "Warden"},
		},
		{
			name:      "no role file",
			wantOther: []accesstypes.Role{"SuperAdministrator"},
		},
		{
			name:      "a role file that does not parse",
			roles:     `{"roles": {"global": [`,
			wantOther: []accesstypes.Role{"SuperAdministrator"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file := filepath.Join(t.TempDir(), identityRolesFile)
			if tt.roles != "" {
				if err := os.WriteFile(file, []byte(tt.roles), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			roles, err := readRoleFileRoles(auth, file)
			if err != nil {
				t.Fatalf("readRoleFileRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantDomain, roles.fieldRoles(true), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("fieldRoles(tenant-scoped) (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantOther, roles.fieldRoles(false)); diff != "" {
				t.Errorf("fieldRoles(not tenant-scoped) (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_resolveRoleOf pins the resolution of a field's @roleOf against the run's auths: an
// auth whose management the run generates resolves, and reads its roles; one none of the
// run's auths has is refused naming them, sorted; and a run that generates no auth's
// management refuses every declaration.
func Test_resolveRoleOf(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "roleoffixture"))
	// The auth packages are outside the main module, so they have no role file to read.
	instances := []*identityInstance{{auth: "Staff", importPath: "example.com/staff"}, {auth: "Crew", importPath: "example.com/crew"}}
	tests := []struct {
		name      string
		identity  []*identityInstance
		auth      string
		domain    bool
		wantRoles []accesstypes.Role
		wantErr   string
	}{
		{name: "an auth of the run, on a global resource", identity: instances, auth: "Staff", wantRoles: []accesstypes.Role{"SuperAdministrator"}},
		{name: "an auth of the run, on a tenant-scoped resource", identity: instances, auth: "Crew", domain: true},
		{
			name: "an auth that is none of the run's", identity: instances, auth: "Pilots",
			wantErr: "struct Seat field Role: @roleOf(Pilots): the application has no auth named Pilots whose management is generated; its auths are Crew, Staff",
		},
		{
			name: "a run that generates no auth's management", auth: "Staff",
			wantErr: "struct Seat field Role: @roleOf(Staff): the application generates the management of no auth (GenerateIdentity), so there is no role file to read the roles from; remove the annotation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := fixtureResource(t, structs, "Seat", func(res *resourceInfo) {
				if tt.domain {
					res.PermissionScope = accesstypes.DomainPermissionScope
				}
				res.Fields[1].roleOfAuth = tt.auth
			})
			computed := fixtureComputedResource(t, structs, "SeatLedger")
			computed.Fields[1].roleOfAuth = tt.auth
			c := &client{identity: tt.identity, modulePathRoot: "example.com/beacon"}

			err := c.resolveRoleFields([]*resourceInfo{res}, []*computedResource{computed})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveRoleFields() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveRoleFields() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantRoles, res.Fields[1].roles, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("the table's field's roles (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]accesstypes.Role{"SuperAdministrator"}, computed.Fields[1].roles); diff != "" {
				t.Errorf("the computed field's roles (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_typescriptResourcesTemplate_roleOf pins the metadata of a field that holds a role's
// name: the roles it may hold as the literal's last property, quoted and joined; an empty
// list where the field declares @roleOf with no role read; and no roles on a field that
// declares nothing. A table's field and a computed resource's alike.
func Test_typescriptResourcesTemplate_roleOf(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadCollectionFixture(t))
	table := func(roles ...accesstypes.Role) *resourceInfo {
		return fixtureResource(t, structs, "Widget", func(res *resourceInfo) {
			for _, f := range res.Fields {
				f.typescriptType = "string"
			}
			res.Fields[1].roleOfAuth = "Staff"
			res.Fields[1].roles = roles
		})
	}
	computed := func(roles ...accesstypes.Role) *computedResource {
		res := fixtureComputedResource(t, structs, "Gadget")
		for _, f := range res.Fields {
			f.typescriptType = "string"
		}
		res.Fields[1].roleOfAuth = "Staff"
		res.Fields[1].roles = roles

		return res
	}

	tests := []struct {
		name            string
		data            tsResourcesData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:         "a table's field lists its roles",
			data:         tsResourcesData{Resources: []*resourceInfo{table("Auditor", "Warden")}, GenPrefix: "zz_gen"},
			wantContains: []string{"{ fieldName: 'name', displayType: 'string', required: true, isIndex: false, roles: [\"Auditor\", \"Warden\"] },"},
		},
		{
			name:         "a table's field with no role read lists none",
			data:         tsResourcesData{Resources: []*resourceInfo{table()}, GenPrefix: "zz_gen"},
			wantContains: []string{"{ fieldName: 'name', displayType: 'string', required: true, isIndex: false, roles: [] },"},
		},
		{
			name:            "a field that declares nothing carries no roles",
			data:            tsResourcesData{Resources: []*resourceInfo{table("Auditor")}, GenPrefix: "zz_gen"},
			wantContains:    []string{"{ fieldName: 'listedName', displayType: 'string', required: true, isIndex: false },"},
			wantNotContains: []string{"fieldName: 'listedName', displayType: 'string', required: true, isIndex: false, roles"},
		},
		{
			name:         "a computed resource's field lists its roles",
			data:         tsResourcesData{ComputedResources: []*computedResource{computed("SuperAdministrator", "Warden")}, GenPrefix: "zz_gen"},
			wantContains: []string{"{ fieldName: 'name', displayType: 'string', required: false, isIndex: false, roles: [\"SuperAdministrator\", \"Warden\"] },"},
		},
		{
			// The generator does not judge a role file's names: one with a quote in it is written as JavaScript reads it.
			name:         "a role whose name holds a quote is written quoted",
			data:         tsResourcesData{Resources: []*resourceInfo{table("O'Hare")}, GenPrefix: "zz_gen"},
			wantContains: []string{`{ fieldName: 'name', displayType: 'string', required: true, isIndex: false, roles: ["O'Hare"] },`},
		},
		{
			name:            "a computed resource's field with no role read lists none, and its key none at all",
			data:            tsResourcesData{ComputedResources: []*computedResource{computed()}, GenPrefix: "zz_gen"},
			wantContains:    []string{"{ fieldName: 'name', displayType: 'string', required: false, isIndex: false, roles: [] },"},
			wantNotContains: []string{"fieldName: 'id', displayType: 'string', required: true, isIndex: false, roles"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := &typescriptGenerator{client: &client{}}
			tt.data.File = g
			out, err := g.generateTemplateOutput("typescriptResourcesTemplate", typescriptResourcesTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(string(out), want) {
					t.Errorf("output lacks %q\n%s", want, out)
				}
			}
			for _, unwanted := range tt.wantNotContains {
				if strings.Contains(string(out), unwanted) {
					t.Errorf("output must not contain %q", unwanted)
				}
			}
		})
	}
}
