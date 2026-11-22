package generation

import (
	"go/format"
	"maps"
	"slices"
	"strings"
	"testing"
)

// Test_resourceFileTemplate_TenantBinding pins the generated TenantBinding method: a
// row that carries its tenant declares the field, a row that finds it through a parent
// the field and the path, and the tenant record its own key; a resource that belongs to
// no tenant and a view declare no such method.
func Test_resourceFileTemplate_TenantBinding(t *testing.T) {
	t.Parallel()

	c := &client{tableMap: bindingFixtureTables()}
	structs := fixtureStructs(loadFixture(t, "bindingfixture"))
	var resources []*resourceInfo
	for _, name := range []string{"MaintenanceTask", "PathTenantLog", "Ship"} {
		res, err := resolveFixtureBindings(t, c, structs, name)
		if err != nil {
			t.Fatalf("resolveBindingAnnotations(%s) error = %v", name, err)
		}
		resources = append(resources, res)
	}
	views, err := c.structsToVirtualResources(subjectSetStructs(t, structs, "TaskDigest"))
	if err != nil {
		t.Fatalf("structsToVirtualResources() error = %v", err)
	}
	sector := fixtureResource(t, fixtureStructs(loadFixture(t, "tenantfixture")), "Sector", func(res *resourceInfo) {
		res.IsTenant = true
	})

	rendered := make(map[string]string)
	for _, res := range append(append(resources, views...), sector) {
		out, err := (&client{}).generateTemplateOutput("resourceFile", resourceFileTemplate, &resourceFileData{Source: "fixture", Package: "resources", Resource: res})
		if err != nil {
			t.Fatalf("generateTemplateOutput(%s) error = %v", res.Name(), err)
		}
		formatted, err := format.Source(out)
		if err != nil {
			t.Fatalf("format.Source(%s) error = %v on:\n%s", res.Name(), err, out)
		}
		rendered[res.Name()] = string(formatted)
	}

	method := func(name, literal string) string {
		return "// TenantBinding is the row's way to its tenant: the library writes the tenant on every\n" +
			"// change record of the row, whoever writes the row.\n" +
			"func (" + name + ") TenantBinding() resource.TenantBinding {\n" +
			"\treturn " + literal + "\n" +
			"}"
	}
	tests := []struct {
		name     string
		resource string
		want     string
	}{
		{
			name:     "a row that carries its tenant",
			resource: "MaintenanceTask",
			want:     method("MaintenanceTask", `resource.TenantBinding{Field: "StationID"}`),
		},
		{
			name:     "a row that finds its tenant through a parent",
			resource: "PathTenantLog",
			want: method("PathTenantLog", `resource.TenantBinding{Field: "TaskID", Path: []resource.BindingHop{`+
				`{Table: "PathTenantTasks", JoinColumn: "Id", Column: "BerthId"}, {Table: "Berths", JoinColumn: "Id", Column: "StationId"}}}`),
		},
		{
			name:     "the tenant record",
			resource: "Sector",
			want:     method("Sector", `resource.TenantBinding{Field: "ID"}`),
		},
		{name: "a resource that belongs to no tenant", resource: "Ship"},
		{name: "a view", resource: "TaskDigest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := rendered[tt.resource]
			if !ok {
				t.Fatalf("%s was not rendered: %v", tt.resource, slices.Sorted(maps.Keys(rendered)))
			}
			if tt.want == "" {
				if strings.Contains(got, "TenantBinding") {
					t.Errorf("%s declares TenantBinding:\n%s", tt.resource, got)
				}

				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("resource file mismatch: want\n%s\nin\n%s", tt.want, got)
			}
		})
	}
}
