package generation

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// Test_writesThatDecideAccess pins the writes the generator gives the class access, one
// case for each shape of a record that decides what a person may do, and the collection
// each run yields. A record that carries a subject value has its create, its change and
// its delete classed, and so has a record that carries a subject set. A record a dotted
// value is read through has its change classed and nothing else. A record that carries
// neither and is read through by nothing carries no class, and a record of the first
// shape is classed by its annotation alone: the fixture's structs differ in nothing a
// mask or a class is written in.
func Test_writesThatDecideAccess(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	writes := []accesstypes.Permission{accesstypes.Create, accesstypes.Update, accesstypes.Delete}
	access := []accesstypes.ActionClass{accesstypes.ClassAccess}

	tests := []struct {
		name string
		// structs are the resources of the run, in the order they are declared.
		structs []string
		// want are the classed writes, each record's by the name the collection
		// holds the record under.
		want map[accesstypes.Resource][]accesstypes.Permission
		// scope is the scope each record of the run is registered in.
		scope map[accesstypes.Resource]accesstypes.PermissionScope
	}{
		{
			name:    "a record that carries a subject value",
			structs: []string{"Seat", "Project"},
			want:    map[accesstypes.Resource][]accesstypes.Permission{"Seats": writes},
			scope:   map[accesstypes.Resource]accesstypes.PermissionScope{"Seats": accesstypes.GlobalPermissionScope, "Projects": accesstypes.GlobalPermissionScope},
		},
		{
			name:    "a record that carries a subject set",
			structs: []string{"TeamMember", "Project"},
			want:    map[accesstypes.Resource][]accesstypes.Permission{"TeamMembers": writes},
			scope:   map[accesstypes.Resource]accesstypes.PermissionScope{"TeamMembers": accesstypes.GlobalPermissionScope, "Projects": accesstypes.GlobalPermissionScope},
		},
		{
			name:    "a record a set's value is read through",
			structs: []string{"TeamMember", "Project", "Stake"},
			want: map[accesstypes.Resource][]accesstypes.Permission{
				"TeamMembers": writes,
				"Stakes":      writes,
				"Projects":    {accesstypes.Update},
			},
			scope: map[accesstypes.Resource]accesstypes.PermissionScope{
				"TeamMembers": accesstypes.GlobalPermissionScope, "Stakes": accesstypes.GlobalPermissionScope, "Projects": accesstypes.GlobalPermissionScope,
			},
		},
		{
			name:    "two sets of one record in a tenant's scope, classed once",
			structs: []string{"TeamMember", "Assignment"},
			want:    map[accesstypes.Resource][]accesstypes.Permission{"TeamMembers": writes, "Assignments": writes},
			scope:   map[accesstypes.Resource]accesstypes.PermissionScope{"TeamMembers": accesstypes.GlobalPermissionScope, "Assignments": accesstypes.DomainPermissionScope},
		},
		{
			name:    "a record that decides nothing",
			structs: []string{"Project"},
			scope:   map[accesstypes.Resource]accesstypes.PermissionScope{"Projects": accesstypes.GlobalPermissionScope},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The run's resources are the client's, so each case has a client of its own.
			c := subjectSetFixtureClient(t, structs)
			r := &resourceGenerator{client: c}
			for _, name := range tt.structs {
				res, err := resolveFixtureBindings(t, c, structs, name)
				if err != nil {
					t.Fatalf("resolveBindingAnnotations(%s) error = %v", name, err)
				}
				r.resources = append(r.resources, res)
			}

			got := make(map[accesstypes.Resource][]accesstypes.Permission)
			for _, write := range r.writesThatDecideAccess() {
				record := accesstypes.Resource(r.pluralize(write.Record.Name()))
				got[record] = append(got[record], write.Permission)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("writesThatDecideAccess() mismatch (-want +got):\n%s", diff)
			}

			// The collection the run yields carries the class on each of those writes,
			// in the record's own scope, and on no other permission of any record.
			b := resource.NewCollectionBuilder()
			r.collectWriteClassRegistrations(b)
			collection, err := resource.NewGeneratedCollection(b.Data())
			if err != nil {
				t.Fatalf("NewGeneratedCollection() error = %v", err)
			}
			for record, scope := range tt.scope {
				for _, permission := range []accesstypes.Permission{accesstypes.List, accesstypes.Read, accesstypes.Create, accesstypes.Update, accesstypes.Delete} {
					var want []accesstypes.ActionClass
					if slices.Contains(tt.want[record], permission) {
						want = access
					}
					for _, in := range []accesstypes.PermissionScope{accesstypes.GlobalPermissionScope, accesstypes.DomainPermissionScope} {
						wantIn := want
						if in != scope {
							wantIn = nil
						}
						if diff := cmp.Diff(wantIn, collection.WriteClasses(in, permission, record), cmpopts.EquateEmpty()); diff != "" {
							t.Errorf("WriteClasses(%s, %s, %s) mismatch (-want +got):\n%s", in, permission, record, diff)
						}
					}
				}
			}
			wantInUse := access
			if len(tt.want) == 0 {
				wantInUse = nil
			}
			if diff := cmp.Diff(wantInUse, collection.ClassesInUse(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ClassesInUse() mismatch (-want +got):\n%s", diff)
			}
			constants, err := actionClassConstants(b.Data())
			if err != nil {
				t.Fatalf("actionClassConstants() error = %v", err)
			}
			if got, want := len(constants), len(wantInUse); got != want {
				t.Errorf("the collection file declares %d class constants (%v), want %d: a mask names the class by its constant", got, constants, want)
			}
		})
	}
}
