package generation

import (
	"go/format"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
)

// notOnOwnAccountFixtureClient is a client whose resources are the fixture's rows, with
// their fields, as the RPC extraction sees them once every resource is extracted: Keeper
// a table's row and Lookout a view's.
func notOnOwnAccountFixtureClient(structs map[string]*parser.Struct) *client {
	c := &client{}
	for _, name := range []string{"Keeper", "Lookout"} {
		res := &resourceInfo{TypeInfo: structs[name].TypeInfo, IsVirtual: name == "Lookout"}
		for _, f := range structs[name].Fields() {
			res.Fields = append(res.Fields, &resourceField{Field: f, Parent: res})
		}
		c.resources = append(c.resources, res)
	}

	return c
}

// TestResolveNotOnOwnAccount pins the @notOnOwnAccount contract: a method that names a row
// and one field of the row's struct that is an impulse.UUID records the field's column, a
// view's row as a table's, a method that declares nothing records none, @callerEnters on
// the same struct is recorded beside it, and each other shape is refused naming the
// method's struct and what to write. The row is the one resolveTransition resolved before
// (root, "" for none).
func TestResolveNotOnOwnAccount(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "notonownaccountfixture"))
	c := notOnOwnAccountFixtureClient(structs)

	tests := []struct {
		name             string
		structName       string
		root             string
		want             string
		wantCallerEnters bool
		wantErr          string
	}{
		{name: "a method that names its row and the row's id field records the field's column", structName: "DisableKeeper", root: "Keeper", want: "Id"},
		{name: "a view's row is accepted as a table's is", structName: "DisableLookout", root: "Lookout", want: "Id"},
		{name: "a method that declares nothing records none", structName: "GreetKeeper", root: "Keeper"},
		{name: "a method with @callerEnters as well records both", structName: "RetireKeeper", root: "Keeper", want: "Id", wantCallerEnters: true},
		{name: "a method that names no row is refused", structName: "NotOnOwnAccountNoRow", wantErr: "struct NotOnOwnAccountNoRow: @notOnOwnAccount says the row the method names does not offer it to the person whose account the row is, and the method names no row; declare @target on the field that carries the row's key"},
		{name: "two names are refused", structName: "NotOnOwnAccountTwoNames", root: "Keeper", wantErr: "struct NotOnOwnAccountTwoNames: @notOnOwnAccount(ID, Name) names one field: the field of Keeper that holds the account's id, as @notOnOwnAccount(ID)"},
		{name: "an empty name is refused", structName: "NotOnOwnAccountEmptyName", root: "Keeper", wantErr: "struct NotOnOwnAccountEmptyName: @notOnOwnAccount(ID, ) names one field: the field of Keeper that holds the account's id, as @notOnOwnAccount(ID)"},
		{name: "a field the row's struct does not have is refused", structName: "NotOnOwnAccountNoField", root: "Keeper", wantErr: "struct NotOnOwnAccountNoField: @notOnOwnAccount(AccountID): Keeper has no field AccountID; name the field of the row that holds the account's id"},
		{name: "a field that is not an impulse.UUID is refused", structName: "NotOnOwnAccountString", root: "Keeper", wantErr: "struct NotOnOwnAccountString: @notOnOwnAccount(Name): Keeper.Name is string; the field that holds an account's id is an impulse.UUID"},
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
			rpcMethod := &rpcMethodInfo{Struct: s}
			if tt.root != "" {
				rpcMethod.Target = &rpcTarget{RootStruct: tt.root, RootResource: c.pluralize(tt.root)}
			}

			if err := c.resolveCallerEnters(rpcMethod, s, annotations); err != nil {
				t.Fatalf("resolveCallerEnters(%s) error = %v", tt.structName, err)
			}
			err = c.resolveNotOnOwnAccount(rpcMethod, s, annotations)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveNotOnOwnAccount(%s) error = %v, want containing %q", tt.structName, err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveNotOnOwnAccount(%s) error = %v", tt.structName, err)
			}
			if rpcMethod.NotOnOwnAccount != tt.want {
				t.Errorf("resolveNotOnOwnAccount(%s) NotOnOwnAccount = %q, want %q", tt.structName, rpcMethod.NotOnOwnAccount, tt.want)
			}
			if rpcMethod.CallerEnters != tt.wantCallerEnters {
				t.Errorf("resolveCallerEnters(%s) CallerEnters = %v, want %v", tt.structName, rpcMethod.CallerEnters, tt.wantCallerEnters)
			}
		})
	}
}

// Test_rejectRPCOnlyAnnotations_notOnOwnAccount pins that a struct of another kind may not
// carry @notOnOwnAccount: it declares no method to offer.
func Test_rejectRPCOnlyAnnotations_notOnOwnAccount(t *testing.T) {
	t.Parallel()

	s := fixtureStructs(loadFixture(t, "notonownaccountfixture"))["Tower"]
	annotations, err := genlang.NewScanner(resourceKeywords()).ScanStruct(s)
	if err != nil {
		t.Fatalf("ScanStruct(Tower) error = %v", err)
	}
	want := "struct Tower: @notOnOwnAccount is only valid on @rpc structs; a resource declares no method to offer"
	if err := rejectRPCOnlyAnnotations(s, annotations, "resource"); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("rejectRPCOnlyAnnotations(Tower) error = %v, want containing %q", err, want)
	}
}

// Test_collectionTemplate_notOnOwnAccount pins the mark from the method to the collection
// file: a marked method's registration carries its column, after CallerEnters where the
// method carries both, and a method that names the same row unmarked is written without
// a word of it.
func Test_collectionTemplate_notOnOwnAccount(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "notonownaccountfixture"))
	keeper := &rpcTarget{RootStruct: "Keeper", RootResource: "Keepers"}
	r := &resourceGenerator{client: &client{
		genRPCMethods: true,
		rpcMethods: []*rpcMethodInfo{
			{Struct: structs["DisableKeeper"], PermissionScope: accesstypes.DomainPermissionScope, Target: keeper, NotOnOwnAccount: "Id"},
			{Struct: structs["GreetKeeper"], PermissionScope: accesstypes.DomainPermissionScope, Target: keeper},
			{Struct: structs["RetireKeeper"], PermissionScope: accesstypes.DomainPermissionScope, Target: keeper, CallerEnters: true, NotOnOwnAccount: "Id"},
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
			`Name: "DisableKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Target: "Keepers", NotOnOwnAccount: "Id", },`,
			`Name: "GreetKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Target: "Keepers", },`,
			`Name: "RetireKeeper", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute}, Target: "Keepers", CallerEnters: true, NotOnOwnAccount: "Id", },`,
		}, nil)
	if got := strings.Count(collapsed, "NotOnOwnAccount"); got != 2 {
		t.Errorf("the collection file mentions NotOnOwnAccount %d times, want for DisableKeeper and RetireKeeper alone:\n%s", got, collapsed)
	}
}
