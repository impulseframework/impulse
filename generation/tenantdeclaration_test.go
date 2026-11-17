package generation

import (
	"go/format"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// declarationResources extracts the named tenantfixture structs and resolves the
// package's declaration the way Generate does: the structs first, then the tenant
// record and its declaration across them.
func declarationResources(t *testing.T, names ...string) (*resourceGenerator, error) {
	t.Helper()

	structs := fixtureStructs(loadFixture(t, "tenantfixture"))
	c := &client{tableMap: tenantFixtureTables()}
	resources, err := c.structsToResources(tenantFixtureStructs(t, structs, names...))
	if err != nil {
		return nil, err
	}
	c.resources = resources
	r := &resourceGenerator{client: c, routePrefix: "api", applicationName: "App", receiverName: "a", domainRouteSegment: defaultDomainRouteSegment, domainRouteParam: defaultDomainRouteParam}
	err = r.resolveTenantRecord()

	return r, err
}

// Test_resolveTenantDeclaration pins the declaration's capture on each struct: the
// record's flag and the directories record's columns are marked, and each refused
// shape names its rule, at capture (one struct) or once every struct is known (the
// record the directories belong to).
func Test_resolveTenantDeclaration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		structs []string
		wantErr string
	}{
		{name: "the record's flag and the directories record are captured", structs: []string{"Harbor", "HarborDirectory"}},
		{name: "a directories record beside a record with no flag is a declaration too", structs: []string{"Sector", "HarborDirectory"}},
		{
			name:    "@directoryOnly on a resource that is not the tenant record is refused",
			structs: []string{"Harbor", "HarborDirectory", "Pier"},
			wantErr: "struct Pier field Closed: @directoryOnly on a resource that is not the tenant record; the directory-only flag is the tenant record's column, so annotate the @tenant record's BOOL column",
		},
		{
			name:    "@directoryOnly on a column that is not a BOOL is refused",
			structs: []string{"Quay", "HarborDirectory"},
			wantErr: "struct Quay field Mode: @directoryOnly on a string column; the directory-only flag is a BOOL, true when the tenant's people enter through its directories only",
		},
		{
			name:    "@tenantDirectory on a global resource is refused",
			structs: []string{"Harbor", "Beacon"},
			wantErr: "struct Beacon: @tenantDirectory on a global resource; the directories record is tenant-scoped (@permissionScope(domain)), since a tenant's directories are its own and its administrator declares them",
		},
		{
			name:    "@directoryMethod off the directories record is refused",
			structs: []string{"Harbor", "HarborDirectory", "Buoy"},
			wantErr: "struct Buoy field Method: @directoryMethod on a resource that is not the directories record; the sign-in method a directory is reached through is the @tenantDirectory record's column, so annotate the record's struct @tenantDirectory first",
		},
		{
			name:    "@directoryMethod on a second column is refused naming the first",
			structs: []string{"Harbor", "Jetty"},
			wantErr: "struct Jetty field Kind: @directoryMethod on a second column; field Method carries it already",
		},
		{
			name:    "a second directories record is refused naming the first",
			structs: []string{"Harbor", "HarborDirectory", "Mooring"},
			wantErr: "struct Mooring: @tenantDirectory on a second struct; struct HarborDirectory is the directories record already, and a tenant record has one",
		},
		{
			name:    "a directories record missing its connection column is refused",
			structs: []string{"Harbor", "Mooring"},
			wantErr: "struct Mooring: @tenantDirectory with no @directoryConnection column; annotate the string column holding the connection a directory's identities carry (a Google Workspace by its domain, a Microsoft Entra tenant by its tenant id)",
		},
		{
			name:    "a directories record whose tenant is a join path is refused naming the bare column",
			structs: []string{"Sector", "Berth", "Anchorage"},
			wantErr: "struct Anchorage: @tenantDirectory needs the bare @domain column, the tenant's key on the row itself, since a directory is the tenant's own declaration; a join path (@domain(via: ...)) is refused",
		},
		{
			name:    "a directories record whose tenant column is neither the id nor its text is refused",
			structs: []string{"Harbor", "Lighthouse"},
			wantErr: "struct Lighthouse field HarborID: the @domain column of the @tenantDirectory record is *string; it holds the tenant's key, an id, as impulse.UUID or as its text (string), since the create-your-tenant route writes the creator's directory under the id it generated",
		},
		{
			name:    "a directories record whose table keeps no harbor to one directory is refused naming the index",
			structs: []string{"Harbor", "Causeway"},
			wantErr: "struct Causeway: table Causeways has no unique index on HarborId alone; a tenant has one directory, which the schema holds: CREATE UNIQUE INDEX CausewaysByHarborId ON Causeways(HarborId)",
		},
		{
			name:    "a directories record whose table keeps no directory to one harbor is refused naming the index",
			structs: []string{"Harbor", "Seawall"},
			wantErr: "struct Seawall: table Seawalls has no unique index on Method and Connection together; a directory belongs to one tenant, which the schema holds: CREATE UNIQUE INDEX SeawallsByMethodConnection ON Seawalls(Method, Connection)",
		},
		{
			name:    "a directories record keyed by a name the request would type is refused",
			structs: []string{"Harbor", "Pontoon"},
			wantErr: "struct Pontoon: the @tenantDirectory record's key is not an id its create generates; a directory is set from a sign-in, which names no key, so key the record by one impulse.UUID column (Id)",
		},
		{
			name:    "the flag without a directories record is refused",
			structs: []string{"Harbor"},
			wantErr: "struct Harbor field DirectoryOnly: @directoryOnly with no directories record in the package; a tenant whose people enter through its directories only needs directories to enter through, so declare the directories record (@tenantDirectory) or drop the flag",
		},
		{
			name:    "@directoryDefaultSeat off the directories record is refused",
			structs: []string{"Harbor", "HarborDirectory", "Slipway"},
			wantErr: "struct Slipway field Seat: @directoryDefaultSeat on a resource that is not the directories record; the default seat is the @tenantDirectory record's column, so annotate the record's struct @tenantDirectory first",
		},
		{
			name:    "@directoryDefaultSeat on a column that is not a string is refused",
			structs: []string{"Harbor", "Wharf"},
			wantErr: "struct Wharf field Seated: @directoryDefaultSeat on a bool column; the default seat is a role name, a string or a nullable string (*string), NULL where the tenant admits the directory's strangers by invitation only",
		},
		{
			name:    "@directoryDefaultSeat on a second column is refused naming the first",
			structs: []string{"Harbor", "Bollard"},
			wantErr: "struct Bollard field Rank: @directoryDefaultSeat on a second column; field Seat carries it already",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := declarationResources(t, tt.structs...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("declaration error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("declaration error = %v", err)
			}
			record, directories := r.tenantRecord(), r.tenantDirectoryRecord()
			if record == nil || directories == nil {
				t.Fatalf("record = %v, directories = %v; want both", record, directories)
			}
			if !record.HasDeclaration() || record.declaration.Directories != directories {
				t.Errorf("%s.declaration = %v, want the %s record", record.Name(), record.declaration, directories.Name())
			}
			if directories.DirectoryMethodField == nil || directories.DirectoryMethodField.Name() != "Method" {
				t.Errorf("%s.DirectoryMethodField = %v, want Method", directories.Name(), directories.DirectoryMethodField)
			}
			if directories.DirectoryConnectionField == nil || directories.DirectoryConnectionField.Name() != "Connection" {
				t.Errorf("%s.DirectoryConnectionField = %v, want Connection", directories.Name(), directories.DirectoryConnectionField)
			}
			// A directory is never typed in: the wire writes neither of its two columns.
			for _, field := range []*resourceField{directories.DirectoryMethodField, directories.DirectoryConnectionField} {
				if field != nil && !field.IsOutputOnly() {
					t.Errorf("%s.%s is writable by the wire, want it output-only: a directory is set from a sign-in", directories.Name(), field.Name())
				}
			}
			if directories.DirectoryDefaultSeatField != nil && directories.DirectoryDefaultSeatField.IsOutputOnly() {
				t.Errorf("%s.%s is output-only, want it writable: the administrator picks the default seat", directories.Name(), directories.DirectoryDefaultSeatField.Name())
			}
			if directories.DirectoryDefaultSeatField == nil || directories.DirectoryDefaultSeatField.Name() != "DefaultSeat" {
				t.Errorf("%s.DirectoryDefaultSeatField = %v, want DefaultSeat", directories.Name(), directories.DirectoryDefaultSeatField)
			}
			if !directories.SignalsTenants() || !record.SignalsTenants() {
				t.Error("both records' write paths keep the roster current")
			}
			if (record.DirectoryOnlyField != nil) != (record.Name() == "Harbor") {
				t.Errorf("%s.DirectoryOnlyField = %v", record.Name(), record.DirectoryOnlyField)
			}
		})
	}
}

// Test_declarationColumns pins the columns the generated constructors carry: the
// spanner columns behind the annotated fields, the flag empty where the record
// declares none, nil where the package declares no directories.
func Test_declarationColumns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		structs []string
		want    *tenantDeclarationColumns
	}{
		{
			name:    "the record's flag and the directories record's columns",
			structs: []string{"Harbor", "HarborDirectory"},
			want:    &tenantDeclarationColumns{DirectoryOnly: "DirectoryOnly", DirectoriesTable: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection", DefaultSeatColumn: "DefaultSeat"},
		},
		{
			name:    "a record without the flag names no directory-only column",
			structs: []string{"Sector", "HarborDirectory"},
			want:    &tenantDeclarationColumns{DirectoriesTable: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection", DefaultSeatColumn: "DefaultSeat"},
		},
		{
			name:    "a directories record without the seat column names none",
			structs: []string{"Harbor", "Breakwater"},
			want:    &tenantDeclarationColumns{DirectoryOnly: "DirectoryOnly", DirectoriesTable: "Breakwaters", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
		},
		{
			name:    "a directories record whose tenant column is a string, the id's text, names it the same way",
			structs: []string{"Harbor", "Marina"},
			want:    &tenantDeclarationColumns{DirectoryOnly: "DirectoryOnly", DirectoriesTable: "Marinas", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
		},
		{name: "no directories record is no declaration", structs: []string{"Sector", "Berth"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := declarationResources(t, tt.structs...)
			if err != nil {
				t.Fatalf("declaration error = %v", err)
			}
			if diff := cmp.Diff(tt.want, r.declarationColumns(r.tenantRecord()), cmp.AllowUnexported(tenantDeclarationColumns{})); diff != "" {
				t.Errorf("declarationColumns() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_tenantsTemplate_declaration pins the roster constructor of a record that
// declares who may enter a tenant: the declaration's columns as a package variable,
// handed to resource.WithTenantDeclarations ahead of the caller's options, and the
// sign-in policy's reader constructor beside it; a record declaring none carries
// neither.
func Test_tenantsTemplate_declaration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		data            *tenantsData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "the declaration's columns, the option and the reader constructor",
			data: &tenantsData{
				Source: "resources", Package: "app", Name: "Harbor", Table: "Harbors", KeyColumn: "Id", RecordLower: "harbor", Directories: "HarborDirectory",
				Declaration:         &tenantDeclarationColumns{DirectoryOnly: "DirectoryOnly", DirectoriesTable: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
				Directory:           harborDirectoryWrite(),
				Word:                "harbor",
				ResourcePackage:     "resources",
				LocalPackageImports: `"example.com/app/pkg/resources"`,
			},
			wantContains: []string{
				`"cloud.google.com/go/spanner"`,
				`"github.com/impulseframework/impulse/session/signin"`,
				"var harborDeclaration = resource.TenantDeclarationColumns{",
				`DirectoryOnly:    "DirectoryOnly",`,
				`DirectoriesTable: "HarborDirectories",`,
				`TenantColumn:     "HarborId",`,
				`MethodColumn:     "Method",`,
				`ConnectionColumn: "Connection",`,
				"opts = append([]resource.TenantRosterOption{resource.WithTenantDeclarations(&harborDeclaration)}, opts...)",
				`return resource.NewTenantRoster(client, "Harbors", "Id", opts...)`,
				"func NewHarborDeclarationReader() signin.DeclarationReader[*spanner.ReadWriteTransaction] {",
				`return resource.NewTenantDeclarationReader("Harbors", "Id", &harborDeclaration)`,
				"func NewHarborDirectoryReader() signin.DirectoryReader[*spanner.ReadWriteTransaction] {",
				"return resource.NewTenantDirectoryReader(&harborDeclaration)",
				"with no default seat (the record carries no @directoryDefaultSeat column",
				// The rules of a harbor's directory, and the one step that sets a
				// directory from a sign-in: the rules are asked, then the row is buffered
				// through the record's create patch.
				"func NewHarborDirectoryRules() *resource.TenantDirectoryRules {",
				`return resource.NewTenantDirectoryRules("Harbors", "Id", &harborDeclaration, "harbor")`,
				"func SetHarborDirectory(ctx context.Context, txn resource.ReadWriteTransaction, tenant impulse.UUID, directory signin.Directory, eventSource ...string) error {",
				"if err := NewHarborDirectoryRules().Set(ctx, txn, accesstypes.Domain(tenant.String()), directory); err != nil {",
				"patch, err := resources.NewHarborDirectoryCreatePatch()",
				"patch.SetHarborID(tenant).SetMethod(string(directory.Method)).SetConnection(directory.Connection)",
				"if err := patch.Buffer(ctx, txn, eventSource...); err != nil {",
			},
			wantNotContains: []string{"DefaultSeatColumn", "AdministratorRoles", "harborDirectoryRules"},
		},
		{
			name: "a directories record whose tenant column is a string takes the tenant's key as text",
			data: &tenantsData{
				Source: "resources", Package: "app", Name: "Harbor", Table: "Harbors", KeyColumn: "Id", RecordLower: "harbor", Directories: "Marina",
				Declaration:         &tenantDeclarationColumns{DirectoriesTable: "Marinas", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
				Directory:           &tenantDirectoryWrite{TenantField: "HarborID", TenantType: "string", TenantParam: "tenant", TenantDomain: "accesstypes.Domain(tenant)", MethodField: "Method", ConnectionField: "Connection"},
				Word:                "harbor",
				ResourcePackage:     "resources",
				LocalPackageImports: `"example.com/app/pkg/resources"`,
			},
			wantContains: []string{
				"func SetHarborDirectory(ctx context.Context, txn resource.ReadWriteTransaction, tenant string, directory signin.Directory, eventSource ...string) error {",
				"if err := NewHarborDirectoryRules().Set(ctx, txn, accesstypes.Domain(tenant), directory); err != nil {",
				"patch, err := resources.NewMarinaCreatePatch()",
			},
		},
		{
			name: "the seat column and the creator's seat contract",
			data: &tenantsData{
				Source: "resources", Package: "app", Name: "Harbor", Table: "Harbors", KeyColumn: "Id", RecordLower: "harbor", Directories: "HarborDirectory", DefaultSeat: "DefaultSeat",
				Declaration:         &tenantDeclarationColumns{DirectoriesTable: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection", DefaultSeatColumn: "DefaultSeat"},
				Directory:           harborDirectoryWrite(),
				Word:                "harbor",
				ResourcePackage:     "resources",
				LocalPackageImports: `"example.com/app/pkg/resources"`,
				Seat:                &tenantSeatData{Record: "Harbor", RecordLower: "harbor", KeyType: "impulse.UUID", Auth: "Staff", IdentityAccessor: "StaffIdentity", RolesAccessor: "HarborAdministratorRoles"},
				RouterPackage:       "router",
				ApplicationName:     "App",
			},
			wantContains: []string{
				`DefaultSeatColumn: "DefaultSeat",`,
				"with the default seat the row carries (HarborDirectory.DefaultSeat:",
				`"github.com/impulseframework/impulse/accesstypes"`,
				"HarborAdministratorRoles() []accesstypes.Role",
				"through the Staff auth's generated identity\n// client (StaffIdentity,",
				"} = (*App)(nil)",
			},
			// The roles are given through the identity client, and nothing else is asked
			// of the application for them.
			wantNotContains: []string{"UserSurface"},
		},
		{
			name:            "a record declaring none carries neither",
			data:            &tenantsData{Source: "resources", Package: "app", Name: "Sector", Table: "Sectors", KeyColumn: "Id", RecordLower: "sector"},
			wantContains:    []string{`return resource.NewTenantRoster(client, "Sectors", "Id", opts...)`},
			wantNotContains: []string{"WithTenantDeclarations", "DeclarationReader", "DirectoryReader", "signin", "spanner", "AdministratorRoles"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("tenantsTemplate", tenantsTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(out); err != nil {
				t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
			}
			assertContains(t, "tenantsTemplate", string(out), tt.wantContains, tt.wantNotContains)
			// Every qualifier the file references resolves to an import the template
			// declares, as generation resolves it: the tenant's key in the signature
			// of the step that sets a directory is the framework's id type.
			fixed, err := c.formatGoBytes("app/zz_gen_tenant_roster.go", "tenantsTemplate", out, tt.data)
			if err != nil {
				t.Fatalf("formatGoBytes() error = %v\n%s", err, out)
			}
			if tt.data.Directory != nil {
				wantsID := tt.data.Directory.TenantType == impulseUUIDGoType
				if got := strings.Contains(string(fixed), "\t\"github.com/impulseframework/impulse\"\n"); got != wantsID {
					t.Errorf("the file imports the framework's root package = %t, want %t for a tenant column of type %s:\n%s", got, wantsID, tt.data.Directory.TenantType, fixed)
				}
			}
		})
	}
}

// Test_directoriesRecord_writes pins the write paths of the directories record, in its
// own handler and in the consolidated one: a directory is never typed in, so the wire
// writes neither the method nor the connection, the create stamps both from the
// directory the caller is signed in through, held to the rules of a tenant's
// directory, before the row is buffered, and the delete asks the rules before the
// row's removal is buffered.
func Test_directoriesRecord_writes(t *testing.T) {
	t.Parallel()

	r, err := declarationResources(t, "Harbor", "HarborDirectory")
	if err != nil {
		t.Fatalf("declarationResources() error = %v", err)
	}
	r.resource = "testdata/tenantfixture"
	var directories *resourceInfo
	for _, res := range r.resources {
		if res.IsTenantDirectory {
			directories = res
		}
	}
	if directories == nil {
		t.Fatal("the fixture resolved no directories record")
	}
	for _, field := range directories.Fields {
		if (field.Name() == "Method" || field.Name() == "Connection") && !field.IsOutputOnly() {
			t.Errorf("field %s is writable over the wire; a directory is never typed in", field.Name())
		}
	}

	standalone, err := r.handlerContent(PatchHandler, directories)
	if err != nil {
		t.Fatalf("handlerContent() error = %v", err)
	}
	if _, err := format.Source(append([]byte("package app\n\n"), standalone...)); err != nil {
		t.Fatalf("the rendered handler does not parse: %v\n%s", err, standalone)
	}
	c := &client{}
	consolidated, err := c.generateTemplateOutput("consolidatedPatchTemplate", consolidatedPatchTemplate, consolidatedPatchData{
		Resources: []*resourceInfo{directories},
		DomainCases: []consolidatedCaseData{{
			resourceInfo: directories, DomainPatternPrefix: "/harbors/{harborID}", ResourcePackage: "resources", ReceiverName: "a",
		}},
		HasTenant:           true,
		DomainRouteSegment:  "harbors",
		DomainPatternPrefix: "/harbors/{harborID}",
		LocalPackageImports: `"example.com/app/pkg/resources"` + "\n\t" + `"example.com/app/pkg/router"`,
		Package:             "app",
		ResourcePackage:     "resources",
		ApplicationName:     "App",
		ReceiverName:        "a",
		HandlerName:         "PatchResources",
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	if _, err := format.Source(consolidated); err != nil {
		t.Fatalf("the rendered consolidated handler does not parse: %v\n%s", err, consolidated)
	}
	if _, err := c.formatGoBytes("app/zz_gen_consolidated_handler.go", "consolidatedPatchTemplate", consolidated, nil); err != nil {
		t.Fatalf("formatGoBytes() error = %v\n%s", err, consolidated)
	}

	for name, out := range map[string]string{"the record's own handler": string(standalone), "the consolidated handler": string(consolidated)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The caller's grants answer before the rules do, on the create and on the
			// delete: a caller the grants refuse learns nothing from a rule's sentence.
			assertInOrder(t, out,
				".NewHarborDirectoryCreatePatchFromPatchSet(patchSet)",
				"if err := patchSet.Permitted(ctx, txn); err != nil {",
				"directory, err := NewHarborDirectoryRules().FromSession(ctx, txn, domain)",
				`return errors.Wrap(err, "resource.TenantDirectoryRules.FromSession()")`,
				"patch.SetMethod(string(directory.Method)).SetConnection(directory.Connection)",
				"if err := patch.Buffer(ctx, txn, eventSource); err != nil {",
				"if err := patchSet.Permitted(ctx, txn); err != nil {",
				"if err := NewHarborDirectoryRules().Remove(ctx, txn, domain); err != nil {",
				`return errors.Wrap(err, "resource.TenantDirectoryRules.Remove()")`,
				".NewHarborDirectoryDeletePatchFromPatchSet(id, patchSet).Buffer(ctx, txn, eventSource)",
			)
			// A write of a directory changes who may enter a tenant, so the roster is
			// reloaded and the tenants kind signaled, as for the record itself.
			assertContains(t, name, out, []string{"tenantsWritten = true", "a.TenantRoster().Reload(ctx)", "resource.KindTenants"}, nil)
		})
	}
}

// assertInOrder fails unless every want appears in out, each after the one before it.
func assertInOrder(t *testing.T, out string, wants ...string) {
	t.Helper()

	at := 0
	for _, want := range wants {
		i := strings.Index(out[at:], want)
		if i < 0 {
			t.Fatalf("output is missing %q after offset %d:\n%s", want, at, out)
		}
		at += i + len(want)
	}
}

// harborDirectoryWrite is the fixture's directories record as the roster file's
// template writes it: keyed to its harbor by an id.
func harborDirectoryWrite() *tenantDirectoryWrite {
	return &tenantDirectoryWrite{
		TenantField: "HarborID", TenantType: "impulse.UUID", TenantParam: "tenant", TenantDomain: "accesstypes.Domain(tenant.String())",
		MethodField: "Method", ConnectionField: "Connection",
	}
}

// Test_tenantCreationTemplate_declaration pins the create-your-tenant route of a record
// that declares who may enter a tenant: the request leaves the key and the
// directory-only flag out, and the new tenant's directory is the one its creator came
// through, set in the row's transaction through the generated Set<Record>Directory,
// which holds it to the rules of a tenant's directory, when the session was established
// through an organization's directory, under the id the create generated: the id itself
// where the directories record's tenant column is an id, its text where the column is a
// string. A creator who came by password or with a personal account sets none.
func Test_tenantCreationTemplate_declaration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		directories   string
		wantDirectory *tenantCreationDirectory
		wantWritten   string
	}{
		{
			name:          "a directories record whose tenant column is an id takes the generated id",
			directories:   "HarborDirectory",
			wantDirectory: &tenantCreationDirectory{Record: "HarborDirectory", TenantValue: "id"},
			wantWritten:   "if err := SetHarborDirectory(ctx, txn, id, directory, eventSource); err != nil {",
		},
		{
			name:          "a directories record whose tenant column is a string takes the generated id's text",
			directories:   "Marina",
			wantDirectory: &tenantCreationDirectory{Record: "Marina", TenantValue: "id.String()"},
			wantWritten:   "if err := SetHarborDirectory(ctx, txn, id.String(), directory, eventSource); err != nil {",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := declarationResources(t, "Harbor", tt.directories)
			if err != nil {
				t.Fatalf("declaration error = %v", err)
			}
			record := r.tenantRecord()
			key, fields, setters := tenantCreationFields(record)
			if diff := cmp.Diff([]string{"Name"}, setters); diff != "" {
				t.Errorf("setters mismatch (-want +got):\n%s", diff)
			}
			if len(fields) != 1 || fields[0].Name != "Name" || key == nil || key.Name != "ID" || key.Type != "impulse.UUID" {
				t.Errorf("fields = %v, key = %v; want the name alone in the request, and the generated id as the key", fields, key)
			}
			directory := tenantCreationDirectoryData(record)
			if diff := cmp.Diff(tt.wantDirectory, directory); diff != "" {
				t.Errorf("Directory mismatch (-want +got):\n%s", diff)
			}
			data := &tenantCreationData{
				Source:              "resources",
				Package:             "app",
				LocalPackageImports: `"example.com/app/pkg/resources"` + "\n\t" + `"example.com/app/pkg/router"`,
				ApplicationName:     "App",
				ReceiverName:        "a",
				ResourcePackage:     "resources",
				RouterPackage:       "router",
				Record:              "Harbor",
				RecordLower:         "harbor",
				HandlerFunc:         "CreateHarbor",
				OfferedHandlerFunc:  "CreateHarborOffered",
				RoutePath:           "/api/create-harbor",
				Auth:                "Staff",
				IdentityAccessor:    "StaffIdentity",
				RolesAccessor:       "HarborAdministratorRoles",
				Key:                 key,
				Fields:              fields,
				Setters:             setters,
				Refusal:             tenantCreationRefusal("Harbor"),
				Directory:           directory,
				record:              record,
			}
			out, err := r.generateTemplateOutput("tenantCreationTemplate", tenantCreationTemplate, data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(out); err != nil {
				t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
			}
			// Every qualifier the route references resolves to an import the template
			// declares, as generation resolves it.
			if _, err := r.formatGoBytes("app/zz_gen_tenant_creation.go", "tenantCreationTemplate", out, data); err != nil {
				t.Fatalf("formatGoBytes() error = %v\n%s", err, out)
			}
			assertContains(t, "tenantCreationTemplate", string(out), []string{
				`"github.com/impulseframework/impulse/session/sessioninfo"`,
				"patch, err := resources.NewHarborCreatePatch()",
				"id = patch.ID()",
				`"github.com/impulseframework/impulse/session/signin"`,
				"if established, ok := sessioninfo.DataFromCtx(ctx).EstablishedBy(); ok {",
				// A password and a personal account are no organization's directory:
				// neither sets one.
				"if directory := (signin.Directory{Method: established.Method, Connection: established.Connection}); directory.Organization() {",
				tt.wantWritten,
				`return errors.Wrap(err, "SetHarborDirectory()")`,
				"if err := identity.AssignRole(ctx, txn, accesstypes.DomainPolicyScope(domain), userPermissions.User(), role); err != nil {",
				"if err := a.TenantRoster().Reload(ctx); err != nil {",
				"if err := resource.AwaitFirstAdministrator(ctx, userPermissions, domain); err != nil {",
				"return httpio.NewEncoder(w).Ok(response{ID: id})",
			}, []string{
				"DirectoryOnly", "SetDirectoryOnly",
				// Nothing is removed after the commit: the row, the directory and the
				// administrator's membership are one transaction.
				"DeletePatch", "SeatFirstAdministrator", "UserSurface",
				// The person names no key: the directory is set on the tenant the
				// create generated, not on one the request names.
				"req.ID", "NewHarborCreatePatch(req",
				// The route writes no directory row of its own: the one step that holds a
				// directory to its rules writes it.
				"New" + tt.directories + "CreatePatch()", "ThroughDirectory()",
			})

			// The id is generated before the row is buffered; the directory and the
			// administrator's membership are written in the row's transaction: after the
			// row, before the commit; the roster reloads before the answer waits on the
			// membership.
			s := string(out)
			generated := strings.Index(s, "id = patch.ID()")
			row := strings.Index(s, "patch.Buffer(ctx, txn, eventSource)")
			written := strings.Index(s, "SetHarborDirectory(ctx, txn,")
			given := strings.Index(s, "identity.AssignRole(ctx, txn,")
			commit := strings.Index(s, "}); err != nil {")
			roster := strings.Index(s, "a.TenantRoster().Reload(ctx)")
			wait := strings.Index(s, "resource.AwaitFirstAdministrator(")
			if generated < 0 || generated >= row || row >= written || written >= given || given >= commit || commit >= roster || roster >= wait {
				t.Errorf("order: generated id %d, row %d, directory %d, membership %d, commit %d, roster %d, wait %d; want ascending:\n%s", generated, row, written, given, commit, roster, wait, out)
			}
		})
	}
}

// Test_apiClientData_tenantCreation_declaration pins the generated client's create
// body: neither the key, which the route generates and its answer carries, nor the
// directory-only flag is among its fields.
func Test_apiClientData_tenantCreation_declaration(t *testing.T) {
	t.Parallel()

	r, err := declarationResources(t, "Harbor", "HarborDirectory")
	if err != nil {
		t.Fatalf("declaration error = %v", err)
	}
	// The route is generated where it can serve; here the target is told so as the run
	// tells it.
	tsg := &typescriptGenerator{client: r.client, tenantCreation: tenantCreationBoth}
	got := tsg.tenantCreationClientData()
	if got == nil {
		t.Fatal("tenantCreationClientData() = nil, want the route")
	}
	names := make([]string, 0, len(got.Fields))
	for _, f := range got.Fields {
		names = append(names, f.Name)
	}
	if diff := cmp.Diff([]string{"name"}, names); diff != "" {
		t.Errorf("fields mismatch (-want +got):\n%s", diff)
	}
	if got.KeyWire != "id" {
		t.Errorf("KeyWire = %q, want the generated id's name in the answer, id", got.KeyWire)
	}
}
