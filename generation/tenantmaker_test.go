package generation

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// Test_resourceInfo_createRefusal pins which resources refuse a create op, what each
// answers one with, and what stays registered. Nobody creates a tenant directly, so the
// tenant record refuses the op in every application, with the way a tenant is made
// where the generator writes the invitation and with the application's own code where
// it writes none. Its Create permission stays registered: it is the grant an invitation
// is sent under. A plain resource refuses nothing, and a resource whose @file key takes
// no NULL refuses with its own sentence and registers no Create.
func Test_resourceInfo_createRefusal(t *testing.T) {
	t.Parallel()

	tenantStructs := fixtureStructs(loadFixture(t, "tenantfixture"))
	fileStructs := fixtureStructs(loadFixture(t, "filefixture"))
	document, err := fileFixtureResource(t, fileStructs, "Document", nil)
	if err != nil {
		t.Fatal(err)
	}
	every := []accesstypes.Permission{accesstypes.Create, accesstypes.Update, accesstypes.Delete}

	tests := []struct {
		name            string
		res             *resourceInfo
		wantRefuses     bool
		wantRefusal     string
		wantPermissions []accesstypes.Permission
	}{
		{
			name:            "a plain resource takes a create",
			res:             fixtureResource(t, tenantStructs, "Berth", nil),
			wantPermissions: every,
		},
		{
			name: "the tenant record of an application whose staff invite",
			res: fixtureResource(t, tenantStructs, "Sector", func(res *resourceInfo) {
				res.IsTenant, res.TenantCreation = true, tenantCreationBoth
			}),
			wantRefuses:     true,
			wantRefusal:     "a sector is not created directly: invite one person, and the sector is made when that person first signs in",
			wantPermissions: every,
		},
		{
			name: "a tenant record the generator writes no invitation for",
			res: fixtureResource(t, tenantStructs, "Outland", func(res *resourceInfo) {
				res.IsTenant, res.TenantCreation = true, tenantCreationNone
			}),
			wantRefuses:     true,
			wantRefusal:     "an outland is not created directly: the application makes one in its own code",
			wantPermissions: every,
		},
		{
			name: "a tenant record whose name is two words",
			res: fixtureResource(t, tenantStructs, "HarborDirectory", func(res *resourceInfo) {
				res.IsTenant, res.TenantCreation = true, tenantCreationBoth
			}),
			wantRefuses:     true,
			wantRefusal:     "a harbor directory is not created directly: invite one person, and the harbor directory is made when that person first signs in",
			wantPermissions: every,
		},
		{
			name:            "a resource whose @file key takes no NULL",
			res:             document,
			wantRefuses:     true,
			wantRefusal:     "a Document row is added by the @upload method that stores its file; a create cannot supply the file's key",
			wantPermissions: []accesstypes.Permission{accesstypes.Update, accesstypes.Delete},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.res.RefusesCreate(); got != tt.wantRefuses {
				t.Errorf("RefusesCreate() = %v, want %v", got, tt.wantRefuses)
			}
			if got := tt.res.ClientCreateDisabled(); got != tt.wantRefuses {
				t.Errorf("ClientCreateDisabled() = %v, want %v", got, tt.wantRefuses)
			}
			if tt.wantRefuses {
				if got := tt.res.CreateRefusal(); got != tt.wantRefusal {
					t.Errorf("CreateRefusal() = %q, want %q", got, tt.wantRefusal)
				}
			}
			if diff := cmp.Diff(tt.wantPermissions, tt.res.PatchPermissions()); diff != "" {
				t.Errorf("PatchPermissions() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("a tenant record whose patch handler is suppressed offers the browser no create either", func(t *testing.T) {
		t.Parallel()

		res := fixtureResource(t, tenantStructs, "Sector", func(res *resourceInfo) {
			res.SuppressedHandlers = []HandlerType{PatchHandler}
		})
		if !res.ClientCreateDisabled() || res.RefusesCreate() {
			t.Errorf("ClientCreateDisabled() = %v, RefusesCreate() = %v, want true and false: a suppressed handler serves no op to refuse", res.ClientCreateDisabled(), res.RefusesCreate())
		}
	})
}

// Test_patchOpCases_tenantRecord pins the authorization matrix's cases for the tenant
// record's patch route: the create op is refused before any grant is read, so the
// matrix has no denied create to pin for it, and keeps the update's and the delete's. A
// plain resource keeps all three.
func Test_patchOpCases_tenantRecord(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "tenantfixture"))
	names := func(res *resourceInfo) []string {
		cases, err := patchOpCases("Sectors", "/api/sectors", "", res, resourcePKTypes(res))
		if err != nil {
			t.Fatalf("patchOpCases() error = %v", err)
		}
		out := make([]string, 0, len(cases))
		for _, c := range cases {
			if !c.DeniedOnly {
				t.Errorf("case %q is not denied-only", c.Name)
			}
			out = append(out, c.Name)
		}

		return out
	}

	plain := fixtureResource(t, structs, "Sector", nil)
	if got, want := names(plain), []string{"Sectors create", "Sectors update", "Sectors delete"}; !slices.Equal(got, want) {
		t.Errorf("a plain resource's cases = %v, want %v", got, want)
	}
	tenant := fixtureResource(t, structs, "Sector", func(res *resourceInfo) { res.IsTenant = true })
	if got, want := names(tenant), []string{"Sectors update", "Sectors delete"}; !slices.Equal(got, want) {
		t.Errorf("the tenant record's cases = %v, want %v: its create op is refused before any grant is read", got, want)
	}
}

// Test_typescript_tenantRecordCreate pins the browser client's side of the refusal: the
// tenant record's descriptor lists no create operation and declares no Create interface,
// and its metadata says createDisabled, so no page offers to add a tenant. The same
// struct as a plain resource keeps its create.
func Test_typescript_tenantRecordCreate(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "tenantfixture"))
	render := func(tenant bool) (api, resources string) {
		sector := fixtureResource(t, structs, "Sector", func(res *resourceInfo) {
			res.IsTenant = tenant
			for _, f := range res.Fields {
				f.typescriptType = "string"
			}
		})
		c := &client{resources: []*resourceInfo{sector}}
		generator := &typescriptGenerator{client: c}
		data := generator.apiClientData()
		for _, res := range data.Resources {
			if res.Name == "Sectors" && res.HasCreate == tenant {
				t.Errorf("the descriptor's HasCreate = %v for tenant = %v", res.HasCreate, tenant)
			}
		}
		apiOut, err := c.generateTemplateOutput("typescriptAPITemplate", typescriptAPITemplate, data)
		if err != nil {
			t.Fatalf("api template error = %v", err)
		}
		resourcesOut, err := c.generateTemplateOutput("typescriptResourcesTemplate", typescriptResourcesTemplate, tsResourcesData{File: generator, Resources: []*resourceInfo{sector}, GenPrefix: "zz_gen"})
		if err != nil {
			t.Fatalf("resources template error = %v", err)
		}

		return string(apiOut), string(resourcesOut)
	}

	api, resources := render(true)
	assertContains(t, "the tenant record's descriptor", api, []string{"operations: ['list', 'read', 'patch', 'remove'],"}, []string{"SectorsCreate", "'create'"})
	assertContains(t, "the tenant record's metadata", resources, []string{"createDisabled: true,"}, nil)

	api, resources = render(false)
	assertContains(t, "a plain resource's descriptor", api, []string{"operations: ['list', 'read', 'create', 'patch', 'remove'],", "SectorsCreate"}, nil)
	assertContains(t, "a plain resource's metadata", resources, nil, []string{"createDisabled"})
}

// invitedFixtureGenerator extracts the named tenantfixture structs and resolves the
// tenant record and its declaration across them, as the run does once every struct is
// extracted, in a program that generates the staff auth's management.
func invitedFixtureGenerator(t *testing.T, mutate func(r *resourceGenerator), names ...string) *resourceGenerator {
	t.Helper()

	r, err := declarationResources(t, names...)
	if err != nil {
		t.Fatalf("declarationResources() error = %v", err)
	}
	r.identity = generatedIdentityOf("example.com/app/pkg/auth/staff")
	if mutate != nil {
		mutate(r)
	}

	return r
}

// Test_tenantMakerData pins what the maker of a tenant from an accepted invitation is
// rendered from: nothing where the invitation cannot serve (no session outlet of one
// auth, or an auth whose roles a directory writes), and otherwise the record's fields an
// invitation carries, which leave out the key, every field the wire cannot write, the
// directory-only switch and every secret or write-only field, with the switch's field
// and the new tenant's id as the directories record's tenant column takes it.
func Test_tenantMakerData(t *testing.T) {
	t.Parallel()

	staff := func(r *resourceGenerator) { r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff") }

	tests := []struct {
		name    string
		structs []string
		mutate  func(r *resourceGenerator)
		want    *tenantMakerData
	}{
		{
			name:    "a record no session auth serves is made by no invitation",
			structs: []string{"Citadel"},
		},
		{
			name:    "a record whose auth's roles a directory writes is made by no invitation",
			structs: []string{"Citadel"},
			mutate:  func(r *resourceGenerator) { r.defaultOutlet.auth = syncedAuth() },
		},
		{
			name:    "the fields an invitation carries: no key, no secret, no write-only field, none the server owns",
			structs: []string{"Citadel"},
			mutate:  staff,
			want: &tenantMakerData{
				Key: "ID",
				Fields: []*tenantMakerField{
					{Name: "Name", Type: "string", Wire: "name"},
					{Name: "Motto", Type: "*string", Wire: "motto"},
					{Name: "Founded", Type: "civil.Date", Wire: "founded"},
					{Name: "Walled", Type: "bool", Wire: "walled"},
				},
			},
		},
		{
			name:    "a field that alone identifies a row and takes no NULL is marked, so the maker reads whether its value is taken",
			structs: []string{"Citadel"},
			mutate: func(r *resourceGenerator) {
				staff(r)
				for _, field := range r.tenantRecord().Fields {
					// The name and the motto each under a unique index of its own; the
					// motto takes NULL, which a unique index holds any number of.
					field.IsUniqueIndex = field.Name() == "Name" || field.Name() == "Motto"
				}
			},
			want: &tenantMakerData{
				Key: "ID",
				Fields: []*tenantMakerField{
					{Name: "Name", Type: "string", Wire: "name", Unique: true},
					{Name: "Motto", Type: "*string", Wire: "motto"},
					{Name: "Founded", Type: "civil.Date", Wire: "founded"},
					{Name: "Walled", Type: "bool", Wire: "walled"},
				},
			},
		},
		{
			name:    "a record that declares the switch and its directories: the switch is no field, and the tenant's id is the directory's tenant column",
			structs: []string{"Harbor", "HarborDirectory"},
			mutate:  staff,
			want: &tenantMakerData{
				Key:         "ID",
				Fields:      []*tenantMakerField{{Name: "Name", Type: "string", Wire: "name"}},
				Flag:        "DirectoryOnly",
				TenantValue: "id",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := invitedFixtureGenerator(t, tt.mutate, tt.structs...)
			got := r.tenantMakerData(r.tenantRecord())
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("tenantMakerData() mismatch (-want +got):\n%s", diff)
			}
			for _, field := range tenantInvitationFields(r.tenantRecord()) {
				if slices.Contains([]string{"ID", "Passphrase", "Cipher", "Sealed", "DirectoryOnly"}, field.Name()) {
					t.Errorf("tenantInvitationFields() carries %s", field.Name())
				}
			}
		})
	}
}
