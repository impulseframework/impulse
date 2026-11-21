package resource

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestGeneratedCollection_declaredPermissions pins a permission declared with no
// resource (CollectionData.Permissions): the collection lists it, with no resource when
// nothing registers it and with its resources when something does, and the declaration
// survives the round trip through the data, sorted and once each.
func TestGeneratedCollection_declaredPermissions(t *testing.T) {
	t.Parallel()

	data := CollectionData{
		Resources: []CollectionResource{
			{Name: "Ledgers", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read}},
		},
		// Out of order, one twice, and one a resource registers too.
		Permissions: []accesstypes.Permission{"ExportReports", "ApproveBudgets", accesstypes.Read, "ExportReports"},
	}
	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	wantList := map[accesstypes.Permission][]accesstypes.Resource{
		"ApproveBudgets": nil,
		"ExportReports":  nil,
		accesstypes.Read: {"Ledgers"},
	}
	if diff := cmp.Diff(wantList, g.List()); diff != "" {
		t.Errorf("List() mismatch (-want +got):\n%s", diff)
	}

	wantDeclared := []accesstypes.Permission{"ApproveBudgets", "ExportReports", accesstypes.Read}
	if diff := cmp.Diff(wantDeclared, g.Data().Permissions); diff != "" {
		t.Errorf("Data().Permissions mismatch (-want +got):\n%s", diff)
	}

	again, err := NewGeneratedCollection(g.Data())
	if err != nil {
		t.Fatalf("NewGeneratedCollection(Data()) error = %v", err)
	}
	if diff := cmp.Diff(g.Data(), again.Data()); diff != "" {
		t.Errorf("the data does not survive the round trip (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantList, again.List()); diff != "" {
		t.Errorf("List() after the round trip mismatch (-want +got):\n%s", diff)
	}

	// The data a collection hands back is the caller's own: writing to it changes
	// nothing the collection lists.
	handed := g.Data()
	handed.Permissions[0] = "Renamed"
	if diff := cmp.Diff(wantDeclared, g.Data().Permissions); diff != "" {
		t.Errorf("Data().Permissions after the caller wrote to its copy mismatch (-want +got):\n%s", diff)
	}
}

// TestGeneratedCollection_noDeclaredPermission pins the collection that declares none:
// it lists only what its resources register, and its data holds no permission list at
// all, so a collection file generated for it is written as it was.
func TestGeneratedCollection_noDeclaredPermission(t *testing.T) {
	t.Parallel()

	g, err := NewGeneratedCollection(CollectionData{
		Resources: []CollectionResource{
			{Name: "Ledgers", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read}},
		},
	})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	if diff := cmp.Diff(map[accesstypes.Permission][]accesstypes.Resource{accesstypes.Read: {"Ledgers"}}, g.List()); diff != "" {
		t.Errorf("List() mismatch (-want +got):\n%s", diff)
	}
	if got := g.Data().Permissions; got != nil {
		t.Errorf("Data().Permissions = %v, want nil", got)
	}
}

// TestCollectionBuilder_AddPermission pins the builder's declaration: the data holds the
// declared permissions sorted, one declared twice is held once, and a permission with no
// name is refused.
func TestCollectionBuilder_AddPermission(t *testing.T) {
	t.Parallel()

	b := NewCollectionBuilder()
	for _, permission := range []accesstypes.Permission{"ExportReports", "ApproveBudgets", "ExportReports"} {
		if err := b.AddPermission(permission); err != nil {
			t.Fatalf("AddPermission(%s) error = %v", permission, err)
		}
	}
	if diff := cmp.Diff([]accesstypes.Permission{"ApproveBudgets", "ExportReports"}, b.Data().Permissions); diff != "" {
		t.Errorf("Data().Permissions mismatch (-want +got):\n%s", diff)
	}

	const wantErr = "cannot declare a permission with no name"
	if err := b.AddPermission(accesstypes.NullPermission); err == nil || !strings.HasSuffix(err.Error(), wantErr) {
		t.Errorf("AddPermission(no name) error = %v, want %q", err, wantErr)
	}
	if _, err := NewGeneratedCollection(CollectionData{Permissions: []accesstypes.Permission{accesstypes.NullPermission}}); err == nil || !strings.HasSuffix(err.Error(), wantErr) {
		t.Errorf("NewGeneratedCollection(a permission with no name) error = %v, want %q", err, wantErr)
	}
}
