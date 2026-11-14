package generation

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// The computed resources of an auth's generated management. A computed resource is
// declared in the application's one computed package, and an auth's generated
// management declares its own beside its methods, whose client their list functions
// take. These tests pin where the generator homes each: the package it names the
// resource in, the handlers it writes for it, and the client those hand its functions.

func crewInstance() *identityInstance {
	return &identityInstance{auth: "Crew", resources: "pkg/identity/internal/crewresources", methods: "pkg/identity/crewidentity", handlers: "pkg/identity/crewhandlers"}
}

// Test_homeOf_computed pins the package a home's handlers name a computed resource in:
// the application's computed package for its own, and an auth's methods package.
func Test_homeOf_computed(t *testing.T) {
	t.Parallel()

	r := &resourceGenerator{client: &client{}}
	r.handler, r.computed = "app", "pkg/computedresources"

	if got := r.homeOf(nil).computed; got != "pkg/computedresources" {
		t.Errorf("the application's computed resources = %q, want its computed package", got)
	}
	if got := r.homeOf(crewInstance()).computed; got != "pkg/identity/crewidentity" {
		t.Errorf("an auth's computed resources = %q, want its methods package", got)
	}
}

// Test_computedOf pins which computed resources a home's handlers serve: the
// application's are the ones no auth declared, and an auth's are its own alone.
func Test_computedOf(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadCollectionFixture(t))
	crew, members := crewInstance(), &identityInstance{auth: "Members", methods: "pkg/identity/membersidentity", handlers: "pkg/identity/membershandlers"}
	own := fixtureComputedResource(t, structs, "Summary")
	generated := fixtureComputedResource(t, structs, "Summary")
	generated.identity = crew

	r := &resourceGenerator{client: &client{computedResources: []*computedResource{own, generated}}}
	r.handler, r.computed = "app", "pkg/computedresources"

	tests := []struct {
		name string
		home *handlerHome
		want []*computedResource
	}{
		{name: "the application's", home: r.homeOf(nil), want: []*computedResource{own}},
		{name: "the auth that declared one", home: r.homeOf(crew), want: []*computedResource{generated}},
		{name: "an auth that declared none", home: r.homeOf(members)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := r.computedOf(tt.home)
			if !slices.Equal(got, tt.want) {
				t.Errorf("computedOf() = %v, want %v", got, tt.want)
			}
			if features := r.handlerFeatures(tt.home); features.hasComputed != (len(tt.want) > 0) {
				t.Errorf("handlerFeatures().hasComputed = %v with %d computed resources of the home", features.hasComputed, len(tt.want))
			}
		})
	}

	if got := r.computedDirOf(own); got != "pkg/computedresources" {
		t.Errorf("computedDirOf(the application's) = %q, want its computed package", got)
	}
	if got := r.computedDirOf(generated); got != "pkg/identity/crewidentity" {
		t.Errorf("computedDirOf(the auth's) = %q, want the auth's methods package", got)
	}
}

// Test_formatResourceInterfaceTypes_computed pins the union a home's decoders are
// constrained to: each computed resource is named in the package it is declared in.
func Test_formatResourceInterfaceTypes_computed(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadCollectionFixture(t))
	own := fixtureComputedResource(t, structs, "Summary")
	generated := fixtureComputedResource(t, structs, "Summary")
	generated.identity = crewInstance()

	c := &client{}
	c.computed = "pkg/computedresources"

	if diff := cmp.Diff("\tcomputedresources.Summary", c.formatResourceInterfaceTypes(nil, []*computedResource{own})); diff != "" {
		t.Errorf("the application's union (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("\tcrewidentity.Summary", c.formatResourceInterfaceTypes(nil, []*computedResource{generated})); diff != "" {
		t.Errorf("the auth's union (-want +got):\n%s", diff)
	}
}

// Test_tenantComputedSources pins where the roles of a tenant are declared: beside the
// methods of an auth whose roles a tenant gives by hand, in a file named for the
// resource, and nowhere else.
func Test_tenantComputedSources(t *testing.T) {
	t.Parallel()

	ship := &identityTenant{name: "Ship", table: "Ships", key: "Id"}
	tests := []struct {
		name   string
		inst   identityInstance
		tables []identityTable
		want   []string
	}{
		{name: "no tenant record", inst: identityInstance{}, tables: []identityTable{identityUsers, identityMemberships}},
		{name: "a tenant gives roles by hand", inst: identityInstance{tenant: ship}, tables: []identityTable{identityUsers, identityMemberships}, want: []string{"crew_ship_roles"}},
		{name: "a directory writes the roles: nobody picks one", inst: identityInstance{tenant: ship, rolesSynced: true}, tables: []identityTable{identityUsers, identityMemberships}},
		{name: "no memberships: no role is held, so none is picked", inst: identityInstance{tenant: ship}, tables: []identityTable{identityUsers}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inst := tt.inst
			inst.auth, inst.methods, inst.tables = "Crew", "pkg/identity/crewidentity", make(map[identityTable]bool)
			for _, kind := range tt.tables {
				inst.tables[kind] = true
			}
			r := identityFixtureGenerator(t)
			var got []string
			for _, file := range r.tenantComputedSources(&inst) {
				if !file.tenant || file.dir != inst.methods {
					t.Errorf("%s: tenant = %v in %q, want a tenant's source beside the methods", file.name, file.tenant, file.dir)
				}
				got = append(got, file.name)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("tenantComputedSources() (-want +got):\n%s", diff)
			}
			if inst.picksRoles() != (len(tt.want) > 0) {
				t.Errorf("picksRoles() = %v with sources %v", inst.picksRoles(), got)
			}
		})
	}
}
