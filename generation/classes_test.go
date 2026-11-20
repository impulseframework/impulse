package generation

import (
	"go/format"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/resource"
)

// Test_resolveClasses pins @class on a method: the classes it names, sorted, whether in
// one list or over repeated annotations, and the refusals of a class named twice and of
// a name no class may carry.
func Test_resolveClasses(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "classfixture"))

	tests := []struct {
		name       string
		structName string
		want       []accesstypes.ActionClass
		wantErr    string
	}{
		{name: "one class", structName: "RotateKey", want: []accesstypes.ActionClass{accesstypes.ClassCredential}},
		{name: "two classes in one list come out sorted", structName: "WireFunds", want: []accesstypes.ActionClass{"money_movement", "sensitive"}},
		{name: "a method of no class", structName: "Launch"},
		{name: "a class named twice is refused", structName: "Twice", wantErr: "struct Twice: @class names the class credential twice"},
		{name: "a name no class may carry is refused", structName: "Shouted", wantErr: `struct Shouted: @class(Credential) names the class "Credential"; a class is named by a lowercase letter, then lowercase letters, digits and underscores, like @class(credential)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pStruct, annotations := scanFixtureStruct(t, structs, tt.structName)
			got, err := resolveClasses(annotations.Struct, "struct "+pStruct.Name())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveClasses() error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveClasses() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("resolveClasses() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_rejectClassAnnotation pins that a class is carried by an action alone: on a
// resource it is refused, with the way a mask holds a session off a record.
func Test_rejectClassAnnotation(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "classfixture"))

	pStruct, annotations := scanFixtureStruct(t, structs, "Vault")
	err := rejectClassAnnotation(pStruct, annotations, "table-backed resource")
	want := "struct Vault: @class marks an action, an @rpc struct or a constant carrying @manualAddResource; a table-backed resource is none. To hold an impersonated session off a record, the mask names the record (accesstypes.RefuseWrites)"
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("rejectClassAnnotation() error = %v, want %q", err, want)
	}

	pStruct, annotations = scanFixtureStruct(t, structs, "Launch")
	if err := rejectClassAnnotation(pStruct, annotations, "table-backed resource"); err != nil {
		t.Errorf("rejectClassAnnotation() on a struct without @class error = %v", err)
	}
}

// Test_manualRegistrationsFromConstants_classes pins @class beside @manualAddResource:
// the registration carries the classes, and a class on a constant that registers
// nothing, or one named as no class is, is refused.
func Test_manualRegistrationsFromConstants_classes(t *testing.T) {
	t.Parallel()

	pkg := loadFixture(t, "classfixture")
	named := func(names ...string) []*parser.Constant {
		var constants []*parser.Constant
		for _, c := range pkg.Constants {
			for _, name := range names {
				if c.Name() == name {
					constants = append(constants, c)
				}
			}
		}
		if len(constants) != len(names) {
			t.Fatalf("the fixture holds %d of the constants %v", len(constants), names)
		}

		return constants
	}

	got, err := manualRegistrationsFromConstants(named("ResetLogin", "ExportLedger", "PlainThing"))
	if err != nil {
		t.Fatalf("manualRegistrationsFromConstants() error = %v", err)
	}
	want := []ManualRegistration{
		{Permission: accesstypes.Execute, Resource: "ResetLogin", Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
		{Scope: accesstypes.DomainPermissionScope, Permission: accesstypes.List, Resource: "ExportLedger", Classes: []accesstypes.ActionClass{"money_movement", "sensitive"}},
		{Permission: accesstypes.Execute, Resource: "PlainThing"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("manualRegistrationsFromConstants() mismatch (-want +got):\n%s", diff)
	}

	for name, wantErr := range map[string]string{
		"Orphan":   `constant "Orphan": @class marks a registration made by hand, and the constant registers nothing; add @manualAddResource(<permission>) beside it or remove it`,
		"Misnamed": `constant Misnamed: @class(Money Movement) names the class "Money Movement"`,
	} {
		if _, err := manualRegistrationsFromConstants(named(name)); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("manualRegistrationsFromConstants(%s) error = %v, want it to contain %q", name, err, wantErr)
		}
	}
}

// Test_computeCollectionData_classes pins that the collection carries each action's
// classes, a generated method's and a manual registration's, in the action's own scope,
// and leaves an action of no class with none.
func Test_computeCollectionData_classes(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "classfixture"))
	r := &resourceGenerator{client: &client{genRPCMethods: true}}
	r.genRoutes = true
	r.rpcMethods = []*rpcMethodInfo{
		{Struct: structs["RotateKey"], Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
		{Struct: structs["WireFunds"], PermissionScope: accesstypes.DomainPermissionScope, Classes: []accesstypes.ActionClass{"money_movement", "sensitive"}},
		{Struct: structs["Launch"]},
	}
	r.manualRegistrations = []ManualRegistration{
		{Permission: accesstypes.Execute, Resource: "ResetLogin", Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
		{Permission: accesstypes.Execute, Resource: "PlainThing"},
	}

	data, err := r.computeCollectionData()
	if err != nil {
		t.Fatalf("computeCollectionData() error = %v", err)
	}
	want := resource.CollectionData{Resources: []resource.CollectionResource{
		{Name: "WireFunds", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{"money_movement", "sensitive"}},
		{Name: "Launch", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}},
		{Name: "PlainThing", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}},
		{Name: "ResetLogin", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
		{Name: "RotateKey", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
	}}
	// The feature flags' own registrations are every application's and carry no class.
	got := resource.CollectionData{}
	for _, res := range data.Resources {
		if res.Name == "FeatureFlags" || res.Name == "SetFeature" {
			if len(res.Classes) != 0 {
				t.Errorf("%s carries the classes %v, want none", res.Name, res.Classes)
			}

			continue
		}
		got.Resources = append(got.Resources, res)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("computeCollectionData() mismatch (-want +got):\n%s", diff)
	}

	// A registration made by hand through the option carries a class under the same
	// rule for its name as the annotation's.
	r.manualRegistrations = append(r.manualRegistrations, ManualRegistration{Permission: accesstypes.Execute, Resource: "Loud", Classes: []accesstypes.ActionClass{"LOUD"}})
	if _, err := r.computeCollectionData(); err == nil || !strings.Contains(err.Error(), `manual registration for resource "Loud": the class "LOUD" is not a class's name`) {
		t.Errorf("computeCollectionData() with a class named as none is, error = %v", err)
	}
}

// Test_computeCollectionData_classWithoutRegistration pins that a class is never lost
// without a word: a method that carries one and registers no action, because its
// handler is suppressed or because the generation writes no routes, is refused with
// where the class belongs, and the same method without a class generates as before.
func Test_computeCollectionData_classWithoutRegistration(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "classfixture"))
	credential := []accesstypes.ActionClass{accesstypes.ClassCredential}

	tests := []struct {
		name      string
		genRoutes bool
		methods   []*rpcMethodInfo
		wantErr   string
	}{
		{
			name:      "a suppressed method that carries a class",
			genRoutes: true,
			methods:   []*rpcMethodInfo{{Struct: structs["RotateKey"], SuppressHandler: true, Classes: credential}},
			wantErr:   "struct RotateKey: @class(credential) is carried by the action the generated routes register, and @suppress leaves the method without the generated handler that registers its action; put @class on the constant that registers the action by hand, beside its @manualAddResource, so a mask that refuses the class refuses the action",
		},
		{
			name:    "a method that carries a class where no routes are written",
			methods: []*rpcMethodInfo{{Struct: structs["WireFunds"], Classes: []accesstypes.ActionClass{"money_movement", "sensitive"}}},
			wantErr: "struct WireFunds: @class(money_movement, sensitive) is carried by the action the generated routes register, and this generation writes no routes (GenerateRoutes), so the method registers no action; put @class on the constant that registers the action by hand, beside its @manualAddResource, so a mask that refuses the class refuses the action",
		},
		{
			name:      "a suppressed method of no class",
			genRoutes: true,
			methods:   []*rpcMethodInfo{{Struct: structs["Launch"], SuppressHandler: true}},
		},
		{
			name:    "a method of no class where no routes are written",
			methods: []*rpcMethodInfo{{Struct: structs["Launch"]}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &resourceGenerator{client: &client{genRPCMethods: true}}
			r.genRoutes = tt.genRoutes
			r.rpcMethods = tt.methods

			_, err := r.computeCollectionData()
			if tt.wantErr != "" {
				if err == nil || !strings.HasSuffix(err.Error(), tt.wantErr) {
					t.Fatalf("computeCollectionData() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("computeCollectionData() error = %v", err)
			}
		})
	}
}

// Test_collectionTemplate_classes pins the collection file's part in a class: a constant
// for each class in use, which the collection's own data is written with, so a mask is
// written against a name the compiler knows; an application whose actions carry no
// class declares none.
func Test_collectionTemplate_classes(t *testing.T) {
	t.Parallel()

	// render answers the collection file with its space runs collapsed, for the lines
	// gofmt aligns, and as formatted, for the file's layout.
	render := func(t *testing.T, data resource.CollectionData) (collapsed, laidOut string) {
		t.Helper()

		output, err := (&resourceGenerator{client: &client{}}).generateTemplateOutput("collectionTemplate", collectionTemplate, &collectionFileData{Source: "pkg/resources", Package: "router", Data: data})
		if err != nil {
			t.Fatalf("generateTemplateOutput() error = %v", err)
		}
		formatted, err := format.Source(output)
		if err != nil {
			t.Fatalf("rendered collection file is not valid Go: %v\n%s", err, output)
		}

		// gofmt aligns the constants and the struct-literal fields, so collapse space runs.
		return strings.Join(strings.Fields(string(formatted)), " "), string(formatted)
	}

	classed, classedLayout := render(t, resource.CollectionData{Resources: []resource.CollectionResource{
		{Name: "RotateKey", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}},
		{Name: "WireFunds", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{"money_movement", accesstypes.ClassCredential}},
		{Name: "Launch", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}},
	}})
	for _, want := range []string{
		`ClassCredential accesstypes.ActionClass = "credential"`,
		`ClassMoneyMovement accesstypes.ActionClass = "money_movement"`,
		"accesstypes.RefuseClass(ClassCredential)",
		`Name: "RotateKey", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: []accesstypes.ActionClass{ClassCredential}, },`,
		`Classes: []accesstypes.ActionClass{ClassMoneyMovement, ClassCredential},`,
		`Name: "Launch", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, },`,
	} {
		if !strings.Contains(classed, want) {
			t.Errorf("rendered collection file missing %q:\n%s", want, classed)
		}
	}
	if got := strings.Count(classed, "ClassCredential accesstypes.ActionClass"); got != 1 {
		t.Errorf("the class credential is declared %d times, want once", got)
	}

	// The constants stand as a declaration of their own between the imports and the
	// function, each under its comment.
	for _, want := range []string{
		")\n\n// The classes the application's actions carry (@class)",
		"\n)\n\n// Collection returns the application's permission collection",
		"const (\n\t// ClassCredential is the class credential.\n\tClassCredential ",
	} {
		if !strings.Contains(classedLayout, want) {
			t.Errorf("rendered collection file is not laid out with %q:\n%s", want, classedLayout)
		}
	}

	plain, plainLayout := render(t, resource.CollectionData{Resources: []resource.CollectionResource{
		{Name: "Launch", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}},
	}})
	if strings.Contains(plain, "accesstypes.ActionClass") || strings.Contains(plain, "const (") {
		t.Errorf("a collection whose actions carry no class declares a class:\n%s", plain)
	}
	// A collection with no class is written as it was before classes: the function's
	// comment a blank line under the imports, never on the line that closes them.
	if want := "\t\"github.com/impulseframework/impulse/resource\"\n)\n\n// Collection returns the application's permission collection"; !strings.Contains(plainLayout, want) {
		t.Errorf("a collection with no class is not laid out with %q:\n%s", want, plainLayout)
	}
}

// Test_actionClassConstants pins the names the collection file declares classes under,
// and that two classes one name would hold are refused before a file is written.
func Test_actionClassConstants(t *testing.T) {
	t.Parallel()

	action := func(classes ...accesstypes.ActionClass) resource.CollectionResource {
		return resource.CollectionResource{Name: "Act", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Classes: classes}
	}

	got, err := actionClassConstants(resource.CollectionData{Resources: []resource.CollectionResource{action("sensitive", accesstypes.ClassCredential), action("money_movement", "sensitive"), action()}})
	if err != nil {
		t.Fatalf("actionClassConstants() error = %v", err)
	}
	want := []actionClassConstant{
		{Name: "ClassCredential", Class: accesstypes.ClassCredential},
		{Name: "ClassMoneyMovement", Class: "money_movement"},
		{Name: "ClassSensitive", Class: "sensitive"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("actionClassConstants() mismatch (-want +got):\n%s", diff)
	}

	_, err = actionClassConstants(resource.CollectionData{Resources: []resource.CollectionResource{action("tier_2", "tier2")}})
	if wantErr := "the classes tier2 and tier_2 would both be declared as the constant ClassTier2; rename one of them"; err == nil || !strings.HasSuffix(err.Error(), wantErr) {
		t.Errorf("actionClassConstants() with two classes of one constant error = %v, want %q", err, wantErr)
	}
}

// generatorActions lists every action the generator declares itself, with no struct of
// the application's or of the generated management's to carry an @class annotation, and
// says of each which class it carries. The generator names these where it registers
// them (tenantOwnMethod.Classes, tenantCreationAction), so the list is where a person
// decides: an action that gives or takes away what someone may do carries the class
// access, and a permission mask that refuses the class refuses it.
//
// The list is the whole input of Test_generatorActions_classed: an action the generator
// comes to declare fails the test until it is listed here with a decision, and one
// listed with a class fails it until its registration carries the class.
var generatorActions = map[accesstypes.Resource]accesstypes.ActionClass{
	// The switch that makes a tenant sign in with its directory alone: on, it keeps
	// out everyone who does not come through the directory, and off, it lets them back
	// in.
	"MakeHarborDirectoryOnly": changesAccess,
	"EndHarborDirectoryOnly":  changesAccess,

	// How a tenant admits its directory's people: everyone the directory sends holds
	// the role the tenant is opened with, and nobody does once it is invite only.
	"OpenHarborToDirectory": changesAccess,
	"MakeHarborInviteOnly":  changesAccess,

	// The making of a tenant of one's own, which makes its maker the tenant's
	// administrator.
	"CreateHarbor": changesAccess,

	// A feature turned on or off: what the application offers everyone, which changes
	// nothing one person may do.
	"SetFeature": unclassed,
}

// Test_generatorActions_classed lists the actions the generator declares itself and
// holds each to generatorActions. The fixture declares what makes the generator write
// every one of them: a tenant record with the directory-only switch, a directories
// record with a default seat, the create-your-tenant route, and the feature flags every
// application carries. It declares no method and no registration of its own, so every
// action in the collection is the generator's.
func Test_generatorActions_classed(t *testing.T) {
	t.Parallel()

	const staffAuth = "example.com/app/pkg/auth/staff"
	r, err := declarationResources(t, "Harbor", "HarborDirectory")
	if err != nil {
		t.Fatalf("declarationResources() error = %v", err)
	}
	r.genRoutes = true
	r.defaultOutlet = routerOutlet{name: defaultOutletName, prefix: "api", servesSessions: true, auth: openAuth(staffAuth)}
	for _, res := range r.resources {
		res.OutletNames = []string{defaultOutletName}
	}
	r.identity = []*identityInstance{{
		importPath: staffAuth, auth: "Staff",
		tenant: &identityTenant{name: "Harbor", table: "Harbors", key: "Id", word: "harbor", directorySeat: "DefaultSeat"},
		tables: map[identityTable]bool{identityUsers: true, identityMemberships: true},
	}}
	record := r.tenantRecord()
	if !r.tenantCreationServes(record) {
		t.Fatal("the fixture's create-your-tenant route does not serve: the test would list no action for it")
	}
	record.TenantCreation = tenantCreationBoth

	data, err := r.computeCollectionData()
	if err != nil {
		t.Fatalf("computeCollectionData() error = %v", err)
	}

	seen := make(map[accesstypes.Resource]bool)
	for _, res := range data.Resources {
		if !slices.Contains(res.Permissions, accesstypes.Execute) {
			continue
		}
		seen[res.Name] = true

		want, listed := generatorActions[res.Name]
		if !listed {
			t.Errorf("%s is not listed in generatorActions: say whether it gives or takes away what someone may do (access), changes how a person signs in (credential), or neither, and give its registration the class", res.Name)

			continue
		}
		var wantClasses []accesstypes.ActionClass
		if want != unclassed {
			wantClasses = []accesstypes.ActionClass{want}
		}
		if diff := cmp.Diff(wantClasses, res.Classes); diff != "" {
			t.Errorf("%s is listed with the class %q and the collection carries other classes for it (-want +got):\n%s", res.Name, want, diff)
		}
	}

	// An action listed and never declared is a line nobody decides any more.
	for action := range generatorActions {
		if !seen[action] {
			t.Errorf("generatorActions lists %s, which the generator does not declare for the fixture: remove the line, or widen the fixture", action)
		}
	}
}
