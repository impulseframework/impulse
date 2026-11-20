package generation

import (
	"go/format"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
)

// TestNameGroupColumns pins which columns a run declares as naming a group of people of
// an auth, driven through structsToResources and the run's resolution as a generation
// run drives them: on a tenant's resource, a column a who: tests against the set of a
// person's groups an auth of the run declares, once however many sets test it, with the
// resource's own way to its tenant; and none on a table that is no tenant's, for a
// column tested against another set, or in a run that generates no management.
func TestNameGroupColumns(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	staff := func(tenant *identityTenant) []*identityInstance {
		return []*identityInstance{{auth: "Staff", policy: access.Tables{Groups: "StaffGroups"}, groupSet: "staffGroups", tenant: tenant}}
	}

	tests := []struct {
		name     string
		structs  []string
		identity []*identityInstance
		want     map[string][]string
	}{
		{
			name:     "a tenant's row that carries its tenant",
			structs:  []string{"StaffGroup", "StaffGroupMember", "Depot", "DepotPass"},
			identity: staff(&identityTenant{word: "realm"}),
			want: map[string][]string{
				"DepotPass": {`{Field: "GroupID", Name: "groupId", Tenant: "TenantID", Groups: "StaffGroups", Key: "Id", Kept: "DomainId", TenantWord: "realm"}`},
			},
		},
		{
			name:     "a tenant's row that finds its tenant through a parent",
			structs:  []string{"StaffGroup", "StaffGroupMember", "Depot", "DepotVisit"},
			identity: staff(&identityTenant{word: "realm"}),
			want: map[string][]string{
				"DepotVisit": {`{Field: "GroupID", Name: "groupId", Tenant: "DepotID", Path: []resource.BindingHop{{Table: "Depots", JoinColumn: "Id", Column: "TenantId"}}, Groups: "StaffGroups", Key: "Id", Kept: "DomainId", TenantWord: "realm"}`},
			},
		},
		{
			name:     "an application with no tenant record gives no word for one",
			structs:  []string{"StaffGroup", "StaffGroupMember", "Depot", "DepotPass"},
			identity: staff(nil),
			want: map[string][]string{
				"DepotPass": {`{Field: "GroupID", Name: "groupId", Tenant: "TenantID", Groups: "StaffGroups", Key: "Id", Kept: "DomainId"}`},
			},
		},
		{
			name:     "a table that is no tenant's declares none",
			structs:  []string{"StaffGroup", "StaffGroupMember", "Depot", "OpenPass"},
			identity: staff(&identityTenant{word: "realm"}),
			want:     map[string][]string{},
		},
		{
			name:     "a column tested against a set that is not an auth's groups declares none",
			structs:  []string{"StaffGroup", "StaffGroupMember", "TeamMember", "Assignment"},
			identity: staff(&identityTenant{word: "realm"}),
			want:     map[string][]string{},
		},
		{
			name:    "a run that generates no management declares none",
			structs: []string{"StaffGroup", "StaffGroupMember", "Depot", "DepotPass"},
			want:    map[string][]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := subjectSetFixtureClient(t, structs)
			c.identity = tt.identity
			resources, err := c.structsToResources(subjectSetStructs(t, structs, tt.structs...))
			if err != nil {
				t.Fatalf("structsToResources() error = %v", err)
			}
			if err := c.resolveRunSubjectSets(resources); err != nil {
				t.Fatalf("resolveRunSubjectSets() error = %v", err)
			}
			got := map[string][]string{}
			for _, res := range resources {
				for _, column := range res.GroupColumns() {
					got[res.Name()] = append(got[res.Name()], column.Literal())
				}
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the group columns declared mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_resourceFileTemplate_groupColumns pins the generated GroupColumns method: a
// resource whose row names a group declares the column for the library, and one that
// names none declares no such method.
func Test_resourceFileTemplate_groupColumns(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)
	c.identity = []*identityInstance{{auth: "Staff", policy: access.Tables{Groups: "StaffGroups"}, groupSet: "staffGroups", tenant: &identityTenant{word: "realm"}}}
	resources, err := c.structsToResources(subjectSetStructs(t, structs, "StaffGroup", "StaffGroupMember", "Depot", "DepotPass"))
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}
	if err := c.resolveRunSubjectSets(resources); err != nil {
		t.Fatalf("resolveRunSubjectSets() error = %v", err)
	}

	rendered := make(map[string]string)
	for _, res := range resources {
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

	want := "// GroupColumns names the columns that name a group of people (GroupID): a row is\n" +
		"// written only with a group its own tenant keeps, so the library refuses a create or an\n" +
		"// update that names another tenant's group, or a group kept for the whole application,\n" +
		"// whoever writes it.\n" +
		"func (DepotPass) GroupColumns() []resource.GroupColumn {\n" +
		"\treturn []resource.GroupColumn{\n" +
		"\t\t{Field: \"GroupID\", Name: \"groupId\", Tenant: \"TenantID\", Groups: \"StaffGroups\", Key: \"Id\", Kept: \"DomainId\", TenantWord: \"realm\"},\n" +
		"\t}\n" +
		"}"
	if !strings.Contains(rendered["DepotPass"], want) {
		t.Errorf("resource file mismatch: want\n%s\nin\n%s", want, rendered["DepotPass"])
	}
	if strings.Contains(rendered["Depot"], "GroupColumns") {
		t.Errorf("Depot, which names no group, declares GroupColumns:\n%s", rendered["Depot"])
	}
}
