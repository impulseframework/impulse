package generation

import (
	"go/format"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// callerEntersFixtureClient is a client whose resources are the fixture's rows, as the RPC
// extraction sees them once every resource is extracted.
func callerEntersFixtureClient(structs map[string]*parser.Struct) *client {
	c := &client{}
	for _, name := range []string{"Keeper", "Bollard", "Dock", "Crane", "Hoist", "Winch"} {
		c.resources = append(c.resources, &resourceInfo{TypeInfo: structs[name].TypeInfo})
	}

	return c
}

// TestResolveCallerEnters pins the @callerEnters contract: a method that names a row whose
// struct declares TenantsToEnter in its one form is marked, a method that names no row is
// offered on the rows of the resource structs it lists, in the order written, a method
// that declares nothing is neither, and each other shape is refused naming the method's
// struct and what to write. The row is the one resolveTransition resolved before (root,
// "" for none).
func TestResolveCallerEnters(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "callerentersfixture"))
	c := callerEntersFixtureClient(structs)

	tests := []struct {
		name        string
		structName  string
		root        string
		scope       accesstypes.PermissionScope
		want        bool
		wantOffered []string
		wantErr     string
	}{
		{name: "a method whose row declares TenantsToEnter is marked", structName: "RetireKeeper", root: "Keeper", want: true},
		{name: "a method that declares nothing is not", structName: "GreetKeeper", root: "Keeper"},
		{name: "a method that declares nothing and names no row is neither", structName: "GreetKeeper"},
		{name: "a method that names no row is offered on the rows it lists, in the order written", structName: "SweepPosts", wantOffered: []string{"Keeper", "Bollard"}},
		{name: "a method that names its row and lists rows is refused", structName: "CallerEntersWithArgument", root: "Keeper", wantErr: "struct CallerEntersWithArgument: @callerEnters(Keeper) names the rows that offer a method that names no row, and this method names its row, Keeper; write @callerEnters with no argument"},
		{name: "a method that names no row and lists none is refused", structName: "CallerEntersNoRow", wantErr: "struct CallerEntersNoRow: @callerEnters says a row offers the method only to a caller who enters the row's tenants, and the method names no row; declare @target on the field that carries the row's key, or name the resource structs whose rows a page runs the method from, as @callerEnters(Row)"},
		{name: "an empty name among the rows is refused", structName: "CallerEntersEmptyName", wantErr: "struct CallerEntersEmptyName: @callerEnters(Keeper, , Bollard) holds an empty name; name each resource struct whose rows a page runs the method from"},
		{name: "a row named twice is refused", structName: "CallerEntersTwice", wantErr: "struct CallerEntersTwice: @callerEnters names Keeper twice"},
		{name: "a name that is no resource struct is refused", structName: "CallerEntersNotResource", wantErr: "struct CallerEntersNotResource: @callerEnters(Keeper, Mooring): Mooring is not a resource struct; name the resource structs whose rows a page runs the method from"},
		{name: "a row of another permission scope is refused", structName: "CallerEntersOtherScope", scope: accesstypes.DomainPermissionScope, wantErr: "struct CallerEntersOtherScope: @callerEnters(Keeper): the method and Keeper are of different permission scopes; a row offers a method of its own scope"},
		{name: "a listed row whose struct declares no TenantsToEnter is refused", structName: "CallerEntersOfferedUndeclared", wantErr: "struct CallerEntersOfferedUndeclared: @callerEnters: Dock, a row that offers the method, does not say which tenants a caller must enter; write beside it: func (Dock) TenantsToEnter() resource.TenantsToEnter"},
		{name: "a row whose struct declares no TenantsToEnter is refused", structName: "CallerEntersUndeclared", root: "Dock", wantErr: "struct CallerEntersUndeclared: @callerEnters: Dock, the row the method names, does not say which tenants a caller must enter; write beside it: func (Dock) TenantsToEnter() resource.TenantsToEnter"},
		{name: "TenantsToEnter on a pointer receiver is refused", structName: "CallerEntersPointer", root: "Crane", wantErr: "struct CallerEntersPointer: @callerEnters: TenantsToEnter of Crane is declared func (*Crane) TenantsToEnter() resource.TenantsToEnter; write it as: func (Crane) TenantsToEnter() resource.TenantsToEnter"},
		{name: "TenantsToEnter with a parameter is refused", structName: "CallerEntersParameter", root: "Hoist", wantErr: "struct CallerEntersParameter: @callerEnters: TenantsToEnter of Hoist is declared func (Hoist) TenantsToEnter(column string) resource.TenantsToEnter; write it as: func (Hoist) TenantsToEnter() resource.TenantsToEnter"},
		{name: "TenantsToEnter answering another type is refused", structName: "CallerEntersString", root: "Winch", wantErr: "struct CallerEntersString: @callerEnters: TenantsToEnter of Winch is declared func (Winch) TenantsToEnter() string; write it as: func (Winch) TenantsToEnter() resource.TenantsToEnter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := structs[tt.structName]
			if s == nil {
				t.Fatalf("struct %q not found in fixture package", tt.structName)
			}
			annotations, err := genlang.NewScanner(resourceKeywords()).ScanStruct(s)
			if err != nil {
				t.Fatalf("ScanStruct(%s) error = %v", tt.structName, err)
			}
			rpcMethod := &rpcMethodInfo{Struct: s, PermissionScope: tt.scope}
			if tt.root != "" {
				rpcMethod.Target = &rpcTarget{RootStruct: tt.root, RootResource: c.pluralize(tt.root)}
			}

			err = c.resolveCallerEnters(rpcMethod, s, annotations)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveCallerEnters(%s) error = %v, want containing %q", tt.structName, err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveCallerEnters(%s) error = %v", tt.structName, err)
			}
			if rpcMethod.CallerEnters != tt.want {
				t.Errorf("resolveCallerEnters(%s) CallerEnters = %v, want %v", tt.structName, rpcMethod.CallerEnters, tt.want)
			}
			var want []rpcOfferingRow
			for _, name := range tt.wantOffered {
				want = append(want, rpcOfferingRow{RootStruct: name, RootResource: c.pluralize(name)})
			}
			if diff := cmp.Diff(want, rpcMethod.OfferedOn); diff != "" {
				t.Errorf("resolveCallerEnters(%s) OfferedOn mismatch (-want +got):\n%s", tt.structName, diff)
			}
		})
	}
}

// Test_rejectRPCOnlyAnnotations_callerEnters pins that a struct of another kind may not
// carry @callerEnters: it declares no method to offer.
func Test_rejectRPCOnlyAnnotations_callerEnters(t *testing.T) {
	t.Parallel()

	s := fixtureStructs(loadFixture(t, "callerentersfixture"))["Tower"]
	annotations, err := genlang.NewScanner(resourceKeywords()).ScanStruct(s)
	if err != nil {
		t.Fatalf("ScanStruct(Tower) error = %v", err)
	}
	want := "struct Tower: @callerEnters is only valid on @rpc structs; a resource declares no method to offer"
	if err := rejectRPCOnlyAnnotations(s, annotations, "resource"); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("rejectRPCOnlyAnnotations(Tower) error = %v, want containing %q", err, want)
	}
}

// Test_collectionTemplate_callerEnters pins the mark from the method to the collection
// file: a marked method's registration carries it and is written with CallerEnters, a
// method that names the same row unmarked is written without a word of it, and a method
// that names no row is written with the resources whose rows offer it, sorted, and with
// neither a Target nor CallerEnters.
func Test_collectionTemplate_callerEnters(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "callerentersfixture"))
	keeper := &rpcTarget{RootStruct: "Keeper", RootResource: "Keepers"}
	r := &resourceGenerator{client: &client{
		genRPCMethods: true,
		rpcMethods: []*rpcMethodInfo{
			{Struct: structs["RetireKeeper"], PermissionScope: accesstypes.DomainPermissionScope, Target: keeper, CallerEnters: true},
			{Struct: structs["GreetKeeper"], PermissionScope: accesstypes.DomainPermissionScope, Target: keeper},
			{Struct: structs["SweepPosts"], PermissionScope: accesstypes.DomainPermissionScope, OfferedOn: []rpcOfferingRow{
				{RootStruct: "Keeper", RootResource: "Keepers"},
				{RootStruct: "Bollard", RootResource: "Bollards"},
			}},
		},
	}}
	r.genRoutes = true
	b := resource.NewCollectionBuilder()
	if err := r.collectRPCRegistrations(b); err != nil {
		t.Fatalf("collectRPCRegistrations() error = %v", err)
	}

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
			`Name: "RetireKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Target: "Keepers", CallerEnters: true, },`,
			`Name: "GreetKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Target: "Keepers", },`,
			`Name: "SweepPosts", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, OfferedOn: []accesstypes.Resource{"Bollards", "Keepers"}, },`,
		}, nil)
	if got := strings.Count(collapsed, "OfferedOn"); got != 1 {
		t.Errorf("the collection file mentions OfferedOn %d times, want for SweepPosts alone:\n%s", got, collapsed)
	}
	if got := strings.Count(collapsed, "CallerEnters"); got != 1 {
		t.Errorf("the collection file mentions CallerEnters %d times, want for RetireKeeper alone:\n%s", got, collapsed)
	}
}
