package access

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// Test_UnionCollection_carriesADeclaredPermission pins that a permission one site's
// collection declares with no resource (CollectionData.Permissions) is one the release
// has: the union of the sites' collections lists it, with no resource when no site
// registers it and with its resources when another site does. Without it a release of
// several sites would skip every stored grant of a permission held for the whole of a
// scope, which its one-site twin carries.
func Test_UnionCollection_carriesADeclaredPermission(t *testing.T) {
	t.Parallel()

	read := []accesstypes.Permission{accesstypes.Read}
	console := generated(t, resource.CollectionData{
		Resources:   []resource.CollectionResource{{Name: "Ledgers", Scope: accesstypes.DomainPermissionScope, Permissions: read}},
		Permissions: []accesstypes.Permission{"ExportReports", "Launch"},
	})
	portal := generated(t, resource.CollectionData{
		Resources: []resource.CollectionResource{
			{Name: "Orders", Scope: accesstypes.DomainPermissionScope, Permissions: read},
			{Name: "Rockets", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{"Launch"}},
		},
		Permissions: []accesstypes.Permission{"ApproveBudgets", "ExportReports"},
	})
	// A collection written by hand that lists a permission with no resource is
	// carried the same way.
	byHand := &stubCollection{list: map[accesstypes.Permission][]accesstypes.Resource{"Audit": nil}}

	want := map[accesstypes.Permission][]accesstypes.Resource{
		"ApproveBudgets": nil,
		"Audit":          nil,
		"ExportReports":  nil,
		"Launch":         {"Rockets"},
		accesstypes.Read: {"Ledgers", "Orders"},
	}
	for _, order := range [][]PermissionCollection{{console, portal, byHand}, {byHand, portal, console}} {
		union, err := UnionCollection(order...)
		if err != nil {
			t.Fatalf("UnionCollection() error = %v", err)
		}
		if diff := cmp.Diff(want, union.List()); diff != "" {
			t.Errorf("UnionCollection().List() mismatch (-want +got):\n%s", diff)
		}
	}
}
