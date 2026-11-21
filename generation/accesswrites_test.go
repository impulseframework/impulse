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

// Test_writesThatDecideAccess_theTenantRecord pins the third shape of a write that
// decides what a person may do: the delete of the tenant record. A tenant that goes
// takes every role held in it, every group and every invitation into it, so its delete
// carries the class access, whatever the record carries otherwise. Its create and its
// change carry none, and a record of the tenant carries none. A run with no tenant
// record classes no delete for one (Test_writesThatDecideAccess, the record that
// decides nothing).
func Test_writesThatDecideAccess_theTenantRecord(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "tenantfixture"))
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
			name:    "the tenant record beside a record of the tenant",
			structs: []string{"Sector", "Berth"},
			want:    map[accesstypes.Resource][]accesstypes.Permission{"Sectors": {accesstypes.Delete}},
			scope:   map[accesstypes.Resource]accesstypes.PermissionScope{"Sectors": accesstypes.GlobalPermissionScope, "Berths": accesstypes.DomainPermissionScope},
		},
		{
			name:    "the tenant record alone",
			structs: []string{"Sector"},
			want:    map[accesstypes.Resource][]accesstypes.Permission{"Sectors": {accesstypes.Delete}},
			scope:   map[accesstypes.Resource]accesstypes.PermissionScope{"Sectors": accesstypes.GlobalPermissionScope},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{tableMap: tenantFixtureTables()}
			resources, err := c.structsToResources(tenantFixtureStructs(t, structs, tt.structs...))
			if err != nil {
				t.Fatalf("structsToResources() error = %v", err)
			}
			// The run's resources are the client's, so each case has a client of its own.
			r := &resourceGenerator{client: c}
			r.resources = resources

			got := make(map[accesstypes.Resource][]accesstypes.Permission)
			for _, write := range r.writesThatDecideAccess() {
				record := accesstypes.Resource(r.pluralize(write.Record.Name()))
				got[record] = append(got[record], write.Permission)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("writesThatDecideAccess() mismatch (-want +got):\n%s", diff)
			}

			// The collection the run yields carries the class on the tenant record's
			// delete, in the record's own scope, and on no other permission of any record.
			b := resource.NewCollectionBuilder()
			r.collectWriteClassRegistrations(b)
			collection, err := resource.NewGeneratedCollection(b.Data())
			if err != nil {
				t.Fatalf("NewGeneratedCollection() error = %v", err)
			}
			for record, scope := range tt.scope {
				for _, permission := range []accesstypes.Permission{accesstypes.List, accesstypes.Read, accesstypes.Create, accesstypes.Update, accesstypes.Delete} {
					for _, in := range []accesstypes.PermissionScope{accesstypes.GlobalPermissionScope, accesstypes.DomainPermissionScope} {
						var want []accesstypes.ActionClass
						if in == scope && slices.Contains(tt.want[record], permission) {
							want = access
						}
						if diff := cmp.Diff(want, collection.WriteClasses(in, permission, record), cmpopts.EquateEmpty()); diff != "" {
							t.Errorf("WriteClasses(%s, %s, %s) mismatch (-want +got):\n%s", in, permission, record, diff)
						}
					}
				}
			}
		})
	}
}

// TestSubjectPathWarnings pins the warning for a subject set whose dotted value is read
// through a table no resource of the run declares. The generator classes the change of
// a record such a value is read through, and a table with no record has no write to
// class, so the run says so, naming the table: a route written by hand for it moves the
// standing of everyone the set reaches. With the table declared the run is silent.
//
// A path is resolved through the struct of each table it lands on, and a table with no
// struct at all is refused there, so the table a path lands on with no record is the
// table of a struct that carries no @resource. Every struct of the fixture carries the
// annotation, so a case names the ones to hold as if they did not: each stays in the
// package for the path to be resolved through and is taken out of the run's resources,
// which is the list a struct with no @resource leaves.
func TestSubjectPathWarnings(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))

	tests := []struct {
		name string
		// structs are the structs of the package. Stake reads its set's value through
		// Projects, the table of Project.
		structs []string
		// plain are the structs held as carrying no @resource: no resource of the run.
		plain []string
		want  []Warning
	}{
		{
			name:    "the table the value is read through is no resource's",
			structs: []string{"TeamMember", "Project", "Stake"},
			plain:   []string{"Project"},
			want:    []Warning{SubjectPathWarning{Resource: "Stake", Name: "portfolios", Table: "Projects"}},
		},
		{
			name:    "the table is declared",
			structs: []string{"TeamMember", "Project", "Stake"},
		},
		{
			name:    "a set that yields a column of its own row",
			structs: []string{"TeamMember"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := subjectSetFixtureClient(t, structs)
			resources, err := c.structsToResources(subjectSetStructs(t, structs, tt.structs...))
			if err != nil {
				t.Fatalf("structsToResources() error = %v", err)
			}
			resources = slices.DeleteFunc(resources, func(res *resourceInfo) bool {
				return slices.Contains(tt.plain, res.Name())
			})

			var got []Warning
			for _, warning := range c.schemaWarnings(resources) {
				if _, ok := warning.(SubjectPathWarning); ok {
					got = append(got, warning)
				}
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("schemaWarnings() mismatch in its SubjectPathWarning values (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSubjectPathWarning_String pins the one-line text a runner prints, for a set and
// for a value: the table by name, what a route written by hand for it does, and the two
// things its author can do.
func TestSubjectPathWarning_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		warning SubjectPathWarning
		want    string
	}{
		{
			name:    "a subject set",
			warning: SubjectPathWarning{Resource: "Stake", Name: "portfolios", Table: "Projects"},
			want: "Stake declares subject.portfolios, a @subjectSet read through Projects, a table no @resource struct declares, so no generated route changes Projects and its change carries no class; " +
				"a route written by hand that changes a row of Projects moves the standing of everyone the set reaches, and a mask that refuses the class access does not refuse it: " +
				"declare Projects as a @resource struct, or register that route as an action of the class (@manualAddResource(Execute) with @class(access))",
		},
		{
			name:    "a subject value",
			warning: SubjectPathWarning{Resource: "Seat", Name: "tier", Scalar: true, Table: "Plans"},
			want: "Seat declares subject.tier, a @subjectValue read through Plans, a table no @resource struct declares, so no generated route changes Plans and its change carries no class; " +
				"a route written by hand that changes a row of Plans moves the standing of everyone the value reaches, and a mask that refuses the class access does not refuse it: " +
				"declare Plans as a @resource struct, or register that route as an action of the class (@manualAddResource(Execute) with @class(access))",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.warning.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
