package access

import (
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// TestUnionCollection_secondSite pins the union of two generated sites of one application:
// the first declares the set of a person's groups and Tasks under the project, the second
// declares no set and serves Milestones under the project, which the first does not
// declare. The union gives a condition on Milestones, the second's, and says Milestones
// are under the project; the second site defines the set of a person's groups from its
// store as the first declares it, so the union takes the two; and the two sites agree on
// Tasks.
func TestUnionCollection_secondSite(t *testing.T) {
	t.Parallel()

	domain := accesstypes.DomainPermissionScope
	read := []accesstypes.Permission{accesstypes.Read}
	tenancy := &resource.DomainBindingData{Column: "DomainId"}
	way := resource.RecordWayAttribute("Projects", 0)
	store := resource.RecordRoleStoreData{Auth: "staff", UserRoles: "StaffRecordUserRoles", GroupRoles: "StaffRecordGroupRoles", GroupSet: "staffGroups", GroupMembers: "StaffGroupMembers", Groups: "StaffGroups"}
	under := func(name accesstypes.Resource, column string) resource.CollectionResource {
		return resource.CollectionResource{
			Name: name, Scope: domain, Permissions: read, Domain: tenancy,
			Attributes: []resource.AttributeData{{Name: way, Column: column, Type: resource.AttributeTypeString}},
			Under:      []resource.RecordWayData{{Record: "Projects", Attribute: way, Self: name == "Projects"}},
		}
	}
	projects := under("Projects", "Id")
	projects.HoldsRoles = &resource.RecordData{Key: "Id", One: "project", Many: "projects"}
	members := resource.CollectionResource{
		Name: "StaffGroupMembers", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.List},
		SubjectSets: []resource.SubjectBindingData{{
			Name: "staffGroups", UserColumn: "UserId", Column: "GroupId", Type: resource.AttributeTypeString,
			Where: &resource.SubjectWhereData{Condition: "(StartsAt IS NULL OR StartsAt <= now) AND (EndsAt IS NULL OR EndsAt > now)", Columns: []resource.SubjectColumnData{
				{Name: "EndsAt", Column: "EndsAt", Type: resource.AttributeTypeTimestamp},
				{Name: "StartsAt", Column: "StartsAt", Type: resource.AttributeTypeTimestamp},
			}},
			Domain:  &resource.DomainBindingData{Column: "GroupId", Path: []resource.BindingHop{{Table: "StaffGroups", JoinColumn: "Id", Column: "DomainId"}}},
			Offered: true, Auth: "staff",
		}},
	}
	first := generated(t, &resource.CollectionData{
		Resources:        []resource.CollectionResource{projects, under("Tasks", "ProjectId"), members},
		RecordRoleStores: []resource.RecordRoleStoreData{store},
	})
	second := generated(t, &resource.CollectionData{
		Resources:        []resource.CollectionResource{projects, under("Tasks", "ProjectId"), under("Milestones", "ProjectId")},
		RecordRoleStores: []resource.RecordRoleStoreData{store},
	})
	union, err := UnionCollection(first, second)
	if err != nil {
		t.Fatalf("UnionCollection() error = %v", err)
	}

	condition, ok := recordCondition(union, "staff", "ProjectEditor", "Projects", "Milestones", accesstypes.Read)
	want, _ := recordCondition(second, "staff", "ProjectEditor", "Projects", "Milestones", accesstypes.Read)
	if !ok || condition != want {
		t.Errorf("the union's condition on Milestones = %q, %v, want the second site's %q", condition, ok, want)
	}
	if _, under, _ := recordReach(union, "Projects", "Milestones"); !under {
		t.Error("the union does not say Milestones are under the project, which the second site declares")
	}
	firstSet, secondSet := first.SubjectSetDefinition("staffGroups"), second.SubjectSetDefinition("staffGroups")
	if secondSet == nil || !slices.Equal(firstSet, secondSet) {
		t.Errorf("the second site's set staffGroups = %q, want the first's declaration %q", secondSet, firstSet)
	}
	if got := union.SubjectSetAuth("staffGroups"); got != "staff" {
		t.Errorf("the union's SubjectSetAuth(staffGroups) = %q, want staff", got)
	}
}

// TestUnionCollection_placedThroughOtherColumns pins the union's refusal of two generated
// sites that both mark the project and reach Comments by one way each, through other
// columns: the first through the comment's task, the second through its milestone. The
// condition each gives a holder is the same text, since a condition names a way by its
// number, so the columns each way passes are what tells the two apart. The same sites
// reaching Comments through the same columns are taken.
func TestUnionCollection_placedThroughOtherColumns(t *testing.T) {
	t.Parallel()

	domain := accesstypes.DomainPermissionScope
	read := []accesstypes.Permission{accesstypes.Read}
	tenancy := &resource.DomainBindingData{Column: "DomainId"}
	way := resource.RecordWayAttribute("Projects", 0)
	store := resource.RecordRoleStoreData{Auth: "staff", UserRoles: "StaffRecordUserRoles"}
	under := func(name accesstypes.Resource, column string, path ...resource.BindingHop) resource.CollectionResource {
		return resource.CollectionResource{
			Name: name, Scope: domain, Permissions: read, Domain: tenancy,
			Attributes: []resource.AttributeData{{Name: way, Column: column, Type: resource.AttributeTypeString, Path: path}},
			Under:      []resource.RecordWayData{{Record: "Projects", Attribute: way, Self: name == "Projects"}},
		}
	}
	projects := under("Projects", "Id")
	projects.HoldsRoles = &resource.RecordData{Key: "Id", One: "project", Many: "projects"}
	site := func(parent accesstypes.Resource, column string) PermissionCollection {
		return generated(t, &resource.CollectionData{
			Resources: []resource.CollectionResource{
				projects, under(parent, "ProjectId"),
				under("Comments", column, resource.BindingHop{Table: string(parent), JoinColumn: "Id", Column: "ProjectId"}),
			},
			RecordRoleStores: []resource.RecordRoleStoreData{store},
		})
	}
	first, second := site("Tasks", "TaskId"), site("Milestones", "MilestoneId")

	firstCondition, _ := recordCondition(first, "staff", "ProjectEditor", "Projects", "Comments", accesstypes.Read)
	secondCondition, _ := recordCondition(second, "staff", "ProjectEditor", "Projects", "Comments", accesstypes.Read)
	if firstCondition == "" || firstCondition != secondCondition {
		t.Fatalf("the two sites' conditions on Comments are %q and %q, want one text: the fixture no longer shows what the columns add", firstCondition, secondCondition)
	}
	_, err := UnionCollection(first, second)
	want := `UnionCollection: collections 1 and 2 (in argument order) both register resource "Comments" and disagree on its place under Projects: ` +
		`"held_way0_Projects: TaskId, Tasks.Id -> ProjectId" vs "held_way0_Projects: MilestoneId, Milestones.Id -> ProjectId"`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("UnionCollection() error = %v, want one containing %q", err, want)
	}
	if _, err := UnionCollection(first, site("Tasks", "TaskId")); err != nil {
		t.Errorf("UnionCollection() of two sites reaching Comments through the same columns: error = %v", err)
	}
}

// TestValidateRoles_heldOnGeneratedCollection pins that the generated collection is one
// that marks records (recordRoles): a role file whose role is held on the record the
// collection marks validates, and one whose role is held on a resource it does not mark
// is refused.
func TestValidateRoles_heldOnGeneratedCollection(t *testing.T) {
	t.Parallel()

	domain := accesstypes.DomainPermissionScope
	read := []accesstypes.Permission{accesstypes.Read}
	collection := generated(t, &resource.CollectionData{
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
