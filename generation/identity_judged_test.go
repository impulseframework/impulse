package generation

import (
	"go/format"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// judgedFixtureInstances are the instances of the fixture with a tenant, as
// prepareIdentity settles them: the crew auth, whose schema holds every table, with Ship
// as its tenant record, and the members auth, whose directory writes its roles. The
// resources package and the instances' sources are written under the test's working
// directory, so the caller has changed to a directory of its own.
func judgedFixtureInstances(t *testing.T) (*resourceGenerator, []*identityInstance) {
	t.Helper()

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	return r, r.identity
}

// Test_identityInstance_judgedMethods pins the methods of the generated management no
// role is given, each with the method whose grant judges it: sending an invitation again
// on each side the instance writes it, judged by the invitation of the same side, and
// nothing where the instance invites nobody.
func Test_identityInstance_judgedMethods(t *testing.T) {
	// The resources package is written relative to the working directory, which belongs
	// to the whole process: no t.Parallel here.
	t.Chdir(t.TempDir())

	judgedOf := func(instances []*identityInstance) map[string][]judgedMethod {
		got := make(map[string][]judgedMethod, len(instances))
		for _, inst := range instances {
			got[inst.auth] = inst.judgedMethods()
		}

		return got
	}

	t.Run("an auth with a tenant record", func(t *testing.T) {
		_, instances := judgedFixtureInstances(t)
		want := map[string][]judgedMethod{
			"Crew": {
				{Method: "ResendCrewInvitation", By: "InviteCrewUser", Scope: accesstypes.GlobalPermissionScope},
				{Method: "ResendCrewShipInvitation", By: "InviteCrewShipUser", Scope: accesstypes.DomainPermissionScope},
			},
			"Members": nil,
		}
		if diff := cmp.Diff(want, judgedOf(instances)); diff != "" {
			t.Errorf("judgedMethods() mismatch (-want +got):\n%s", diff)
		}
	})

	for _, tt := range []struct {
		name      string
		generator func(t *testing.T) *resourceGenerator
		want      []judgedMethod
	}{
		{
			name: "an auth without a tenant record",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()

				return identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
			},
			want: []judgedMethod{{Method: "ResendCrewInvitation", By: "InviteCrewUser", Scope: accesstypes.GlobalPermissionScope}},
		},
		{
			name: "an auth whose roles a directory writes",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()
				r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
				r.defaultOutlet.auth = &outletAuth{importPath: crewAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}

				return r
			},
		},
		{
			name: "an auth whose schema holds no mail links",
			generator: func(t *testing.T) *resourceGenerator {
				t.Helper()

				return identityFixtureGenerator(t, slices.Concat(crewIdentityTables, membersIdentityTables)...)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			instances, err := tt.generator(t).identityInstances()
			if err != nil {
				t.Fatalf("identityInstances() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, judgedOf(instances)["Crew"]); diff != "" {
				t.Errorf("the crew's judgedMethods() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_collectIdentityJudgedRegistrations pins the marks the generated management's
// methods are registered with: each Send again an action no role is given, judged by
// the invitation of its side, and no other method marked. The generator's collection
// carries them where the methods are registered, and none where nothing is.
func Test_collectIdentityJudgedRegistrations(t *testing.T) {
	// As Test_identityInstance_judgedMethods: no t.Parallel.
	t.Chdir(t.TempDir())

	r, _ := judgedFixtureInstances(t)
	registered := []ManualRegistration{
		{Permission: accesstypes.Execute, Resource: "InviteCrewUser"},
		{Permission: accesstypes.Execute, Resource: "ResendCrewInvitation"},
		{Permission: accesstypes.Execute, Resource: "WithdrawCrewInvitation"},
		{Scope: accesstypes.DomainPermissionScope, Permission: accesstypes.Execute, Resource: "InviteCrewShipUser"},
		{Scope: accesstypes.DomainPermissionScope, Permission: accesstypes.Execute, Resource: "ResendCrewShipInvitation"},
		{Scope: accesstypes.DomainPermissionScope, Permission: accesstypes.Execute, Resource: "WithdrawCrewShipInvitation"},
	}
	type marks struct {
		NoRole   bool
		JudgedBy accesstypes.Resource
	}
	want := map[accesstypes.Resource]marks{
		"InviteCrewUser":             {},
		"ResendCrewInvitation":       {NoRole: true, JudgedBy: "InviteCrewUser"},
		"WithdrawCrewInvitation":     {},
		"InviteCrewShipUser":         {},
		"ResendCrewShipInvitation":   {NoRole: true, JudgedBy: "InviteCrewShipUser"},
		"WithdrawCrewShipInvitation": {},
	}
	marked := func(data resource.CollectionData) map[accesstypes.Resource]marks {
		got := make(map[accesstypes.Resource]marks)
		for _, res := range data.Resources {
			if _, named := want[res.Name]; named || res.NoRole || res.JudgedBy != "" {
				got[res.Name] = marks{NoRole: res.NoRole, JudgedBy: res.JudgedBy}
			}
		}

		return got
	}

	t.Run("over a builder that registered the methods", func(t *testing.T) {
		b := resource.NewCollectionBuilder()
		for _, reg := range registered {
			if err := b.AddMethodResource(scopeOrGlobal(reg.Scope), reg.Permission, reg.Resource); err != nil {
				t.Fatalf("AddMethodResource(%s) error = %v", reg.Resource, err)
			}
		}
		r.collectIdentityJudgedRegistrations(b)
		if diff := cmp.Diff(want, marked(b.Data())); diff != "" {
			t.Errorf("the marks of the registrations (-want +got):\n%s", diff)
		}
	})

	t.Run("over a builder that registered nothing", func(t *testing.T) {
		b := resource.NewCollectionBuilder()
		r.collectIdentityJudgedRegistrations(b)
		if got := b.Data().Resources; len(got) != 0 {
			t.Errorf("the marks of a run that registers no method are in the data: %v", got)
		}
	})

	t.Run("in the generator's collection", func(t *testing.T) {
		r.manualRegistrations = registered
		data, err := r.computeCollectionData()
		if err != nil {
			t.Fatalf("computeCollectionData() error = %v", err)
		}
		if diff := cmp.Diff(want, marked(data)); diff != "" {
			t.Errorf("the marks in the generator's collection (-want +got):\n%s", diff)
		}
	})
}

// Test_collectionTemplate_identityJudged pins that the collection file writes the method
// whose grant judges a method, so the application's collection carries it, and writes it
// for that method alone.
func Test_collectionTemplate_identityJudged(t *testing.T) {
	t.Parallel()

	r := identityFixtureGenerator(t)
	b := resource.NewCollectionBuilder()
	for _, method := range []accesstypes.Resource{"InviteCrewUser", "ResendCrewInvitation"} {
		if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, method); err != nil {
			t.Fatalf("AddMethodResource(%s) error = %v", method, err)
		}
	}
	b.SetMethodTarget(accesstypes.GlobalPermissionScope, "ResendCrewInvitation", "CrewListedMailLinks")
	b.SetActionNoRole(accesstypes.GlobalPermissionScope, "ResendCrewInvitation")
	b.SetMethodJudgedBy(accesstypes.GlobalPermissionScope, "ResendCrewInvitation", "InviteCrewUser")

	output, err := r.generateTemplateOutput("collectionTemplate", collectionTemplate, &collectionFileData{Source: "pkg/resources", Package: "router", Data: b.Data()})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	formatted, err := format.Source(output)
	if err != nil {
		t.Fatalf("rendered collection file is not valid Go: %v\n%s", err, output)
	}
	// gofmt aligns the struct-literal fields, so collapse space runs.
	collapsed := strings.Join(strings.Fields(string(formatted)), " ")
	assertContains(t, "collectionTemplate", collapsed,
		[]string{
			`Name: "ResendCrewInvitation", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, NoRole: true, Target: "CrewListedMailLinks", JudgedBy: "InviteCrewUser", },`,
			`Name: "InviteCrewUser", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, },`,
		}, nil)
	if got := strings.Count(collapsed, "JudgedBy:"); got != 1 {
		t.Errorf("the collection file marks %d methods as judged by another's grant, want ResendCrewInvitation alone:\n%s", got, collapsed)
	}
}
