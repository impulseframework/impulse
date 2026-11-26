package resource

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// recordRolesData is the data of an application that marks one record a role can be held
// on: Projects names a client, Tasks are under a project by two ways (the project a task
// is in and the one it came from), Comments by one (through their task), and Invoices
// stand apart. The auth staff keeps the roles held on records with groups.
func recordRolesData() CollectionData {
	domain := accesstypes.DomainPermissionScope
	read := []accesstypes.Permission{accesstypes.Create, accesstypes.Read}

	return CollectionData{
		Resources: []CollectionResource{
			{Name: "Clients", Scope: domain, Permissions: read},
			{
				Name: "Comments", Scope: domain, Permissions: read,
				Under: []RecordWayData{{Record: "Projects", Attribute: "commentProject"}},
			},
			{Name: "Invoices", Scope: domain, Permissions: read},
			{
				Name: "Projects", Scope: domain, Permissions: read,
				HoldsRoles: &RecordData{
					Key: "Id", One: "project", Many: "projects",
					Names: []RecordNameData{{Resource: "Clients", Column: "ClientId", Attribute: "clientOfProject", One: "client", Many: "clients"}},
				},
				Under: []RecordWayData{{Record: "Projects", Attribute: "projectItself", Self: true}},
			},
			{
				Name: "Tasks", Scope: domain, Permissions: read,
				Under: []RecordWayData{
					{Record: "Projects", Attribute: "taskProject"},
					{Record: "Projects", Attribute: "taskFormerProject"},
				},
			},
		},
		RecordRoleStores: []RecordRoleStoreData{
			{Auth: "staff", UserRoles: "StaffRecordUserRoles", GroupRoles: "StaffRecordGroupRoles", GroupSet: "groupsOfStaff"},
		},
	}
}

// TestGeneratedCollection_recordRoles pins the data of the records a role can be held on:
// the builder's setters write it, it survives the round trip through
// NewGeneratedCollection and Data() unchanged, and the collection answers which records
// hold roles, their words, and how each resource stands to the record.
func TestGeneratedCollection_recordRoles(t *testing.T) {
	t.Parallel()

	data := recordRolesData()
	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	t.Run("the data survives the round trip", func(t *testing.T) {
		t.Parallel()

		if diff := cmp.Diff(data, g.Data()); diff != "" {
			t.Errorf("the data does not survive the round trip (-want +got):\n%s", diff)
		}
	})

	t.Run("the builder's setters write the data", func(t *testing.T) {
		t.Parallel()

		b := NewCollectionBuilder()
		for _, res := range data.Resources {
			for _, perm := range res.Permissions {
				if err := b.AddResource(res.Scope, perm, res.Name); err != nil {
					t.Fatalf("AddResource(%s, %s) error = %v", perm, res.Name, err)
				}
			}
			b.SetResourceUnder(res.Scope, res.Name, res.Under)
		}
		domain := accesstypes.DomainPermissionScope
		b.SetResourceHoldsRoles(domain, "Invoices", &RecordData{Key: "Id", One: "invoice", Many: "invoices"})
		b.SetResourceHoldsRoles(domain, "Invoices", nil)
		b.SetResourceHoldsRoles(domain, "Projects", &RecordData{Key: "Id"})
		b.SetResourceHoldsRoles(domain, "Projects", data.Resources[3].HoldsRoles)
		b.AddRecordRoleStore(RecordRoleStoreData{Auth: "staff", UserRoles: "Former"})
		b.AddRecordRoleStore(data.RecordRoleStores[0])
		if diff := cmp.Diff(data, b.Data()); diff != "" {
			t.Errorf("CollectionBuilder.Data() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("the records that hold roles, and their words", func(t *testing.T) {
		t.Parallel()

		if diff := cmp.Diff([]accesstypes.Resource{"Projects"}, g.RecordsHoldingRoles()); diff != "" {
			t.Errorf("RecordsHoldingRoles() mismatch (-want +got):\n%s", diff)
		}
		words := map[accesstypes.Resource][2]string{
			"Projects": {"project", "projects"},
			"Clients":  {"client", "clients"},
			"Tasks":    {"", ""},
			"Invoices": {"", ""},
		}
		for res, want := range words {
			if one, many := g.RecordWords(res); one != want[0] || many != want[1] {
				t.Errorf("RecordWords(%s) = (%q, %q), want (%q, %q)", res, one, many, want[0], want[1])
			}
		}
	})

	t.Run("how each resource stands to the record", func(t *testing.T) {
		t.Parallel()

		reach := map[accesstypes.Resource][3]bool{
			"Projects": {true, false, false},
			"Tasks":    {false, true, false},
			"Comments": {false, true, false},
			"Clients":  {false, false, true},
			"Invoices": {false, false, false},
		}
		for res, want := range reach {
			if self, under, named := g.RecordReach("Projects", res); [3]bool{self, under, named} != want {
				t.Errorf("RecordReach(Projects, %s) = %v, want %v", res, [3]bool{self, under, named}, want)
			}
			if self, under, named := g.RecordReach("Clients", res); self || under || named {
				t.Errorf("RecordReach(Clients, %s) = (%v, %v, %v), want nothing: Clients is no record a role is held on", res, self, under, named)
			}
		}
	})

	t.Run("a nil collection marks no record", func(t *testing.T) {
		t.Parallel()

		var none *GeneratedCollection
		if got := none.RecordsHoldingRoles(); got != nil {
			t.Errorf("RecordsHoldingRoles() = %v, want nil", got)
		}
		if one, many := none.RecordWords("Projects"); one != "" || many != "" {
			t.Errorf("RecordWords(Projects) = (%q, %q), want nothing", one, many)
		}
		if self, under, named := none.RecordReach("Projects", "Tasks"); self || under || named {
			t.Errorf("RecordReach(Projects, Tasks) = (%v, %v, %v), want nothing", self, under, named)
		}
	})
}
