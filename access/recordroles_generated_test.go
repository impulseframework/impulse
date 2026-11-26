package access

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// TestValidateRoles_heldOnGeneratedCollection pins that the generated collection is one
// that marks records (recordRoles): a role file whose role is held on the record the
// collection marks validates, and one whose role is held on a resource it does not mark
// is refused.
func TestValidateRoles_heldOnGeneratedCollection(t *testing.T) {
	t.Parallel()

	domain := accesstypes.DomainPermissionScope
	read := []accesstypes.Permission{accesstypes.Read}
	collection := generated(t, resource.CollectionData{
		Resources: []resource.CollectionResource{
			{
				Name: "Projects", Scope: domain, Permissions: read,
				HoldsRoles: &resource.RecordData{Key: "Id", One: "project", Many: "projects"},
				Under:      []resource.RecordWayData{{Record: "Projects", Attribute: resource.RecordWayAttribute("Projects", 0), Self: true}},
			},
			{
				Name: "Tasks", Scope: domain, Permissions: read,
				Under: []resource.RecordWayData{{Record: "Projects", Attribute: resource.RecordWayAttribute("Projects", 0)}},
			},
		},
	})
	if _, ok := collection.(recordRoles); !ok {
		t.Fatal("the generated collection does not answer for the records a role can be held on")
	}
	roleHeldOn := func(record accesstypes.Resource) *RoleConfig {
		return &RoleConfig{Roles: ScopedRoles{Domain: []*Role{{
			Name: "ProjectEditor", Description: "Edits a project.", HeldOn: []accesstypes.Resource{record},
			Permissions: map[accesstypes.Permission][]Grant{accesstypes.Read: {{Resource: "Projects"}, {Resource: "Tasks"}}},
		}}}}
	}

	warnings, err := ValidateRoles(collection, roleHeldOn("Projects"), "")
	if err != nil {
		t.Fatalf("ValidateRoles() of a role held on the marked record: error = %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("ValidateRoles() of a role held on the marked record: warnings = %v, want none", warnings)
	}
	_, err = ValidateRoles(collection, roleHeldOn("Tasks"), "")
	if want := "Tasks is not marked as a record that can hold a role"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("ValidateRoles() of a role held on a resource not marked: error = %v, want one containing %q", err, want)
	}
}
