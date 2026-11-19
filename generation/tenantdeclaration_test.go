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

// Test_namedDirectoryRules pins the expressions a generated frame names a directory by:
// the DirectoryName of the generated identity client of every auth whose management the
// program generates, in declaration order, through the application's accessor. The
// rules of a tenant record's directory are built named by them wherever a frame builds
// them, the directories record's write paths among them, and the tenant list is handed
// them where the package declares a tenant's directories; a program that generates no
// auth's management names by nobody, and its rules read what a directory alone says.
func Test_namedDirectoryRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// auths are the auths whose management the program generates, and structs the
		// fixture's structs the package declares.
		auths      []string
		structs    []string
		wantNamers []string
		wantRules  string
		// wantSwitch is the expression of the rules the directory-only switch's frame
		// asks: the named rules, told what each auth says of the caller's password.
		wantSwitch string
		wantClosed string
	}{
		{
			name: "two auths name the rules and the tenant list, in declaration order", auths: []string{"Staff", "Members"}, structs: []string{"Harbor", "HarborDirectory"},
			wantNamers: []string{"a.StaffIdentity().DirectoryName", "a.MembersIdentity().DirectoryName"},
			wantRules:  "NewHarborDirectoryRules().NamedBy(a.StaffIdentity().DirectoryName, a.MembersIdentity().DirectoryName)",
			wantSwitch: "NewHarborDirectoryRules().NamedBy(a.StaffIdentity().DirectoryName, a.MembersIdentity().DirectoryName).PasswordsBy(a.StaffIdentity().CallerPassword, a.MembersIdentity().CallerPassword)",
			wantClosed: "a.StaffIdentity().DirectoryName, a.MembersIdentity().DirectoryName",
		},
		{
			name: "one auth", auths: []string{"Staff"}, structs: []string{"Harbor", "HarborDirectory"},
			wantNamers: []string{"a.StaffIdentity().DirectoryName"},
			wantRules:  "NewHarborDirectoryRules().NamedBy(a.StaffIdentity().DirectoryName)",
			wantSwitch: "NewHarborDirectoryRules().NamedBy(a.StaffIdentity().DirectoryName).PasswordsBy(a.StaffIdentity().CallerPassword)",
			wantClosed: "a.StaffIdentity().DirectoryName",
		},
		{
			name: "a program that generates no auth's management names by nobody and asks nobody about a password", structs: []string{"Harbor", "HarborDirectory"},
			wantNamers: []string{}, wantRules: "NewHarborDirectoryRules()", wantSwitch: "NewHarborDirectoryRules()",
		},
		{
			name: "a package that declares no directories has no tenant closed to a session, so its tenant list is handed no namer", auths: []string{"Staff"}, structs: []string{"Sector"},
			wantNamers: []string{"a.StaffIdentity().DirectoryName"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			structs := fixtureStructs(loadFixture(t, "tenantfixture"))
			c := &client{tableMap: tenantFixtureTables()}
			resources, err := c.structsToResources(tenantFixtureStructs(t, structs, tt.structs...))
			if err != nil {
				t.Fatalf("structsToResources() error = %v", err)
			}
			c.resources = resources
			for _, auth := range tt.auths {
				c.identity = append(c.identity, &identityInstance{auth: auth})
			}
			r := &resourceGenerator{client: c, routePrefix: "api", applicationName: "App", receiverName: "a", domainRouteSegment: defaultDomainRouteSegment, domainRouteParam: defaultDomainRouteParam}
			if err := r.resolveTenantRecord(); err != nil {
				t.Fatalf("resolveTenantRecord() error = %v", err)
			}

			if diff := cmp.Diff(tt.wantNamers, r.directoryNamers()); diff != "" {
				t.Errorf("directoryNamers() mismatch (-want +got):\n%s", diff)
			}
			if got := r.closedTenantNamers(); got != tt.wantClosed {
				t.Errorf("closedTenantNamers() = %q, want %q", got, tt.wantClosed)
			}
			directories := r.tenantDirectoryRecord()
			if tt.wantRules == "" {
				if directories != nil {
					t.Fatalf("the fixture resolved a directories record, %s, want none", directories.Name())
				}

				return
			}
			if got := r.namedDirectoryRules(r.tenantRecord()); got != tt.wantRules {
				t.Errorf("namedDirectoryRules() = %q, want %q", got, tt.wantRules)
			}
			if got := r.switchDirectoryRules(r.tenantRecord()); got != tt.wantSwitch {
				t.Errorf("switchDirectoryRules() = %q, want %q", got, tt.wantSwitch)
			}
			// The directories record's write paths build the rules by the same expression.
			if got := directories.DirectoryRules(); got != tt.wantRules {
				t.Errorf("the directories record's DirectoryRules() = %q, want %q", got, tt.wantRules)
			}
			for _, res := range r.resources {
				if res != directories && res.DirectoryRules() != "" {
					t.Errorf("%s.DirectoryRules() = %q, want none on a resource that is not the directories record", res.Name(), res.DirectoryRules())
				}
			}
		})
	}
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
			// Nor the default seat: the tenant's own methods set and clear it, the one
			// that sets it behind its warning and the rule of who may give the role.
			if directories.DirectoryDefaultSeatField != nil && !directories.DirectoryDefaultSeatField.IsOutputOnly() {
				t.Errorf("%s.%s is writable by the wire, want it output-only: the tenant's own methods set the default seat", directories.Name(), directories.DirectoryDefaultSeatField.Name())
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
			want:    &tenantDeclarationColumns{DirectoryOnly: "DirectoryOnly", DirectoriesTable: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection", DirectoriesKeyColumn: "Id", DefaultSeatColumn: "DefaultSeat"},
		},
		{
			name:    "a record without the flag names no directory-only column",
			structs: []string{"Sector", "HarborDirectory"},
			want:    &tenantDeclarationColumns{DirectoriesTable: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection", DirectoriesKeyColumn: "Id", DefaultSeatColumn: "DefaultSeat"},
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
// sign-in policy's reader constructor beside it; a record declaring none carries no
// columns, and its reader answers nothing.
func Test_tenantsTemplate_declaration(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "tenantfixture"))

	tests := []struct {
		name            string
		data            *tenantsData
		wantContains    []string
		wantNotContains []string
		// wantOrder names fragments the rendered file carries in this order, and
		// wantImports the import lines the file carries once its imports are resolved.
		wantOrder   []string
		wantImports []string
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
			// An application whose tenants have no directories hands its auths the same
			// constructor: the reader answers nothing, with no columns to read.
			name: "a record that declares no directories gets a reader that answers nothing, and nothing of a directory",
			data: &tenantsData{Source: "resources", Package: "app", Name: "Harbor", Table: "Harbors", KeyColumn: "Id", RecordLower: "harbor", NoDirectories: true},
			wantContains: []string{
				`"cloud.google.com/go/spanner"`,
				`"github.com/impulseframework/impulse/session/signin"`,
				"The\n// Harbor record declares no directories (no record is annotated @tenantDirectory), so no\n// harbor declares anything",
				"func NewHarborDeclarationReader() signin.DeclarationReader[*spanner.ReadWriteTransaction] {",
				`return resource.NewTenantDeclarationReader("Harbors", "Id", nil)`,
				`return resource.NewTenantRoster(client, "Harbors", "Id", opts...)`,
			},
			wantNotContains: []string{"harborDeclaration", "WithTenantDeclarations", "DirectoryReader", "DirectoryRules", "SetHarborDirectory", "ClearHarborDirectory"},
		},
		{
			// A site that declares the record another site serves reads one package and
			// cannot know whether the tenants have directories: the other site's reader
			// is the one the auths are handed, and this package gets none.
			name: "a record another site serves, in a package with no directories record, gets no reader and imports nothing for one",
			data: &tenantsData{Source: "resources", Package: "app", Name: "Harbor", Table: "Harbors", KeyColumn: "Id", RecordLower: "harbor"},
			wantContains: []string{
				"import (\n\t\"github.com/impulseframework/impulse/resource\"\n)",
				`return resource.NewTenantRoster(client, "Harbors", "Id", opts...)`,
			},
			wantNotContains: []string{"DeclarationReader", "signin", "cloud.google.com/go/spanner", "declares no directories"},
		},
		{
			name: "a directories record whose tenant column is a string takes the tenant's key as text",
			data: &tenantsData{
				Source: "resources", Package: "app", Name: "Harbor", Table: "Harbors", KeyColumn: "Id", RecordLower: "harbor", Directories: "Marina",
				Declaration:         &tenantDeclarationColumns{DirectoriesTable: "Marinas", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
				Directory:           &tenantDirectoryWrite{TenantField: "HarborID", TenantType: "string", TenantParam: "tenant", TenantDomain: "accesstypes.Domain(tenant)", MethodField: "Method", ConnectionField: "Connection", KeyField: "ID"},
				Word:                "harbor",
				ResourcePackage:     "resources",
				LocalPackageImports: `"example.com/app/pkg/resources"`,
			},
			wantContains: []string{
				"func SetHarborDirectory(ctx context.Context, txn resource.ReadWriteTransaction, tenant string, directory signin.Directory, eventSource ...string) error {",
				"if err := NewHarborDirectoryRules().Set(ctx, txn, accesstypes.Domain(tenant), directory); err != nil {",
				"patch, err := resources.NewMarinaCreatePatch()",
				// The removal that goes with the harbor takes the key as the column holds it.
				"func ClearHarborDirectory(ctx context.Context, txn resource.ReadWriteTransaction, tenant string, eventSource ...string) error {",
				"Where(resources.NewMarinaQueryClause().HarborID().Equal(tenant))",
			},
		},
		{
			name: "the removal of a harbor's directory with the harbor: the row read by its harbor, then removed through the record's delete patch, and no rule asked",
			data: &tenantsData{
				Source: "resources", Package: "app", Name: "Harbor", Table: "Harbors", KeyColumn: "Id", RecordLower: "harbor", Directories: "HarborDirectory",
				Declaration:         &tenantDeclarationColumns{DirectoryOnly: "DirectoryOnly", DirectoriesTable: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection"},
				Directory:           harborDirectoryWrite(),
				Word:                "harbor",
				ResourcePackage:     "resources",
				LocalPackageImports: `"example.com/app/pkg/resources"`,
			},
			wantContains: []string{
				"func ClearHarborDirectory(ctx context.Context, txn resource.ReadWriteTransaction, tenant impulse.UUID, eventSource ...string) error {",
				"AddColumns(resources.NewHarborDirectoryColumns().ID()).",
				"var removals []*resources.HarborDirectoryDeletePatch",
				`return errors.Wrap(err, "resources.HarborDirectoryQuery.List()")`,
				`return errors.Wrap(err, "resources.HarborDirectoryDeletePatch.Buffer()")`,
			},
			wantOrder: []string{
				"func ClearHarborDirectory(",
				// The harbor's rows alone, read before any removal is buffered.
				"Where(resources.NewHarborDirectoryQueryClause().HarborID().Equal(tenant))",
				"for row, err := range query.List(ctx, txn) {",
				"removals = append(removals, resources.NewHarborDirectoryDeletePatch(row.Data.ID))",
				"if err := removal.Buffer(ctx, txn, eventSource...); err != nil {",
			},
		},
		{
			name: "the seat column, and the maker of a tenant from an accepted invitation",
			data: &tenantsData{
				Source: "resources", Package: "app", Name: "Harbor", Table: "Harbors", KeyColumn: "Id", RecordLower: "harbor", Directories: "HarborDirectory", DefaultSeat: "DefaultSeat",
				Declaration:         &tenantDeclarationColumns{DirectoryOnly: "DirectoryOnly", DirectoriesTable: "HarborDirectories", TenantColumn: "HarborId", MethodColumn: "Method", ConnectionColumn: "Connection", DirectoriesKeyColumn: "Id", DefaultSeatColumn: "DefaultSeat"},
				Directory:           harborDirectoryWrite(),
				Word:                "harbor",
				ResourcePackage:     "resources",
				LocalPackageImports: `"example.com/app/pkg/resources"`,
				Maker: &tenantMakerData{
					Fields:      []*tenantMakerField{{Name: "Name", Type: "string", Wire: "name", Unique: true}, {Name: "Berths", Type: "*int64", Wire: "berths"}},
					Key:         "ID",
					Flag:        "DirectoryOnly",
					TenantValue: "id",
				},
				record:          fixtureResource(t, structs, "Harbor", nil),
				RouterPackage:   "router",
				ApplicationName: "App",
			},
			wantContains: []string{
				`DefaultSeatColumn: "DefaultSeat",`,
				"with the default seat the row carries (HarborDirectory.DefaultSeat:",
				"func NewHarborMaker(roster *resource.TenantRoster, signals live.Service) identity.TenantMaker {",
				"func (m *harborMaker) Make(ctx context.Context, txn resource.ReadWriteTransaction, tenant *identity.NewTenant) (accesstypes.Domain, error) {",
				// The record's own create patch, its key generated, each field the
				// invitation carries set through its setter, and a field the record does
				// not carry on an invitation refused.
				"patch, err := resources.NewHarborCreatePatch()",
				"id := patch.ID()",
				`if value, ok := carried["Name"]; ok {`,
				"var given string",
				// A name another harbor holds is refused where the answer says so, and
				// not by the database at the commit.
				"if patch.NameIsSet() {",
				"switch _, err := resources.NewHarborQuery().SetName(patch.Name()).AddColumns(resources.NewHarborColumns().ID()).Read(ctx, txn); {",
				`return "", httpio.NewConflictMessagef("a harbor with the name %v already exists: give the new one another", patch.Name())`,
				"case !httpio.HasNotFound(err):",
				"patch.SetName(given)",
				`if value, ok := carried["Berths"]; ok {`,
				"var given *int64",
				"patch.SetBerths(given)",
				`return "", httpio.NewBadRequestMessagef("an invitation carries no field %s of the Harbor record: a field the wire cannot write, or a secret, is given to the harbor once it exists", name)`,
				// Directory only is set where the invitation says so, with the directory, and
				// never without one.
				"hasDirectory := tenant.Directory.Organization()",
				"\tif tenant.DirectoryOnly {\n\t\tif !hasDirectory {\n\t\t\treturn \"\", errors.New(\"a harbor that is directory only is made with its directory: the invitation was accepted through none\")\n\t\t}\n\t\tpatch.SetDirectoryOnly(true)\n\t}",
				// The check a send makes arms the create with the caller.
				"if tenant.Caller != nil {\n\t\tpatch.Enforce(tenant.Caller)\n\t}",
				"if err := patch.Buffer(ctx, txn, tenant.Source); err != nil {",
				// The directory, where the person came through one, held to the rules: one
				// another tenant has refuses the sign-in with its own code.
				"\tif hasDirectory {\n\t\t// The directory the person came through becomes the harbor's own, held to",
				"if err := NewHarborDirectoryRules().Set(ctx, txn, domain, tenant.Directory); err != nil {",
				"if httpio.HasConflict(err) {",
				`return "", sessioninfo.NewLoginRefusal(sessioninfo.RefusedOrganizationHasTenant, err)`,
				"directory, err := resources.NewHarborDirectoryCreatePatch()",
				"directory.SetHarborID(id).SetMethod(string(tenant.Directory.Method)).SetConnection(tenant.Directory.Connection)",
				// The role the directory's people start with, where the invitation names one.
				"\t\tif tenant.DefaultRole != \"\" {\n\t\t\trole := string(tenant.DefaultRole)\n\t\t\tdirectory.SetDefaultSeat(&role)\n\t\t}",
				// The roster and the signal follow the commit, and a check owes neither.
				"// The check a send makes: nothing commits, and nothing is owed after it.\n\t\treturn domain, nil",
				"if err := resource.AfterCommit(txn, func(ctx context.Context) {",
				"if err := m.roster.Reload(ctx); err != nil {",
				"if err := m.signals.Signal(ctx, resource.KindTenants); err != nil {",
				"func (m *harborMaker) SeatsDefaultRole() bool {\n\treturn true\n}",
				"func (m *harborMaker) DeclaresDirectoryOnly() bool {\n\treturn true\n}",
			},
			// The creator's seat went with the direct create: nothing is asked of the
			// application for it here.
			wantNotContains: []string{"UserSurface", "AdministratorRoles"},
			wantOrder: []string{
				"patch.SetDirectoryOnly(true)",
				"patch.Enforce(tenant.Caller)",
				"patch.Buffer(ctx, txn, tenant.Source)",
				// The grants answer (the buffer) before a taken value is read for.
				"resources.NewHarborQuery().SetName(patch.Name())",
				"NewHarborDirectoryRules().Set(ctx, txn, domain, tenant.Directory)",
				"directory.Buffer(ctx, txn, tenant.Source)",
				"// The check a send makes: nothing commits, and nothing is owed after it.",
				"resource.AfterCommit(txn,",
			},
		},
		{
			name: "a maker for a record that declares no directory makes the row alone, and refuses a directory-only tenant",
			data: &tenantsData{
				Source: "resources", Package: "app", Name: "Sector", Table: "Sectors", KeyColumn: "Id", RecordLower: "sector",
				NoDirectories:       true,
				ResourcePackage:     "resources",
				LocalPackageImports: `"example.com/app/pkg/resources"`,
				Maker:               &tenantMakerData{Fields: []*tenantMakerField{{Name: "Name", Type: "string", Wire: "name"}}, Key: "ID"},
				record:              fixtureResource(t, structs, "Sector", nil),
			},
			wantContains: []string{
				"func NewSectorMaker(roster *resource.TenantRoster, signals live.Service) identity.TenantMaker {",
				`return "", errors.New("the Sector record declares no directory-only switch, so no sector is made directory only")`,
				"func (m *sectorMaker) SeatsDefaultRole() bool {\n\treturn false\n}",
				"func (m *sectorMaker) DeclaresDirectoryOnly() bool {\n\treturn false\n}",
				// The reader that answers nothing stands beside the maker, its imports
				// after the standard library's.
				"\t\"encoding/json\"\n\n\t\"cloud.google.com/go/spanner\"\n\t\"github.com/go-playground/errors/v5\"\n",
				`return resource.NewTenantDeclarationReader("Sectors", "Id", nil)`,
			},
			// A field under no unique index is not read for.
			wantNotContains: []string{"hasDirectory", "DirectoryRules", "sessioninfo", "signin.Directory", "SetDirectoryOnly", "NewSectorQuery()", "already exists"},
		},
		{
			name: "a maker reads a field of an imported type, whose package the file imports by the record's own fields",
			data: &tenantsData{
				Source: "resources", Package: "app", Name: "Citadel", Table: "Citadels", KeyColumn: "Id", RecordLower: "citadel",
				ResourcePackage:     "resources",
				LocalPackageImports: `"example.com/app/pkg/resources"`,
				Maker:               &tenantMakerData{Fields: []*tenantMakerField{{Name: "Name", Type: "string", Wire: "name"}, {Name: "Founded", Type: "civil.Date", Wire: "founded"}}, Key: "ID"},
				record:              fixtureResource(t, structs, "Citadel", nil),
			},
			wantContains: []string{
				`if value, ok := carried["Founded"]; ok {`,
				"var given civil.Date",
				`return "", httpio.NewBadRequestMessageWithError(err, "the citadel's founded does not read")`,
				"patch.SetFounded(given)",
			},
			wantImports: []string{"\t\"cloud.google.com/go/civil\"\n", "\t\"github.com/impulseframework/impulse/identity\"\n", "\t\"github.com/impulseframework/impulse/resource/live\"\n"},
		},
		{
			name:            "a record declaring none, and made by no invitation, carries the roster and the reader that answers nothing, and no more",
			data:            &tenantsData{Source: "resources", Package: "app", Name: "Sector", Table: "Sectors", KeyColumn: "Id", RecordLower: "sector", NoDirectories: true},
			wantContains:    []string{"import (\n\t\"cloud.google.com/go/spanner\"\n", `return resource.NewTenantRoster(client, "Sectors", "Id", opts...)`, `return resource.NewTenantDeclarationReader("Sectors", "Id", nil)`},
			wantNotContains: []string{"WithTenantDeclarations", "sectorDeclaration", "DirectoryReader", "signin.Directory", "AdministratorRoles", "Maker", "identity"},
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
			rendered, at := string(out), -1
			for _, fragment := range tt.wantOrder {
				next := strings.Index(rendered, fragment)
				if next < 0 || next <= at {
					t.Errorf("tenantsTemplate: %q at %d, want it after the fragment before it (%d):\n%s", fragment, next, at, out)
				}
				at = next
			}
			// Every qualifier the file references resolves to an import the template
			// declares, as generation resolves it: the tenant's key in the signature
			// of the step that sets a directory is the framework's id type.
			fixed, err := c.formatGoBytes("app/zz_gen_tenant_roster.go", "tenantsTemplate", out, tt.data)
			if err != nil {
				t.Fatalf("formatGoBytes() error = %v\n%s", err, out)
			}
			for _, line := range tt.wantImports {
				if !strings.Contains(string(fixed), line) {
					t.Errorf("the file lacks the import %q:\n%s", line, fixed)
				}
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
		MethodField: "Method", ConnectionField: "Connection", KeyField: "ID",
	}
}

// Test_tenantRecord_deleteTakesItsDirectory pins the delete of a tenant record that
// declares its directories, in its own handler and in the consolidated one: a tenant
// that goes takes its directory with it, in the delete's transaction, through the
// generated Clear<Record>Directory, handed the tenant's key as the directories record's
// tenant column holds it. The directory goes after the tenant's own row is buffered,
// which is where the caller's Delete grant on the tenant record answers, and before
// what the auths hold for the tenant; the rule that keeps a directory-only tenant's
// directory is not asked, since it is for a tenant that stays. A tenant record that
// declares no directories, and every other resource, take nothing.
func Test_tenantRecord_deleteTakesItsDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// structs are the fixture's structs the package declares, and resource the one
		// whose handlers are rendered.
		structs   []string
		resource  string
		wantClear *tenantDirectoryClear
		wantCall  string
	}{
		{
			name: "a directories record keyed to its tenant by an id is cleared by the id", structs: []string{"Harbor", "HarborDirectory"}, resource: "Harbor",
			wantClear: &tenantDirectoryClear{Func: "ClearHarborDirectory", Tenant: "id", Word: "harbor"},
			wantCall:  "if err := ClearHarborDirectory(ctx, txn, id, eventSource); err != nil {",
		},
		{
			name: "a directories record keyed to its tenant by the id's text is cleared by the text", structs: []string{"Harbor", "Marina"}, resource: "Harbor",
			wantClear: &tenantDirectoryClear{Func: "ClearHarborDirectory", Tenant: "id.String()", Word: "harbor"},
			wantCall:  "if err := ClearHarborDirectory(ctx, txn, id.String(), eventSource); err != nil {",
		},
		{name: "a tenant record that declares no directories takes nothing", structs: []string{"Sector"}, resource: "Sector"},
		{name: "the directories record itself takes nothing with it", structs: []string{"Harbor", "HarborDirectory"}, resource: "HarborDirectory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := declarationResources(t, tt.structs...)
			if err != nil {
				t.Fatalf("declarationResources() error = %v", err)
			}
			r.resource = "testdata/tenantfixture"
			r.defaultOutlet.auth = &outletAuth{importPath: "example.com/app/pkg/auth/staff"}
			r.identity = generatedIdentityOf("example.com/app/pkg/auth/staff")
			var res *resourceInfo
			for _, candidate := range r.resources {
				if candidate.Name() == tt.resource {
					res = candidate
				}
			}
			if res == nil {
				t.Fatalf("the fixture resolved no resource %s", tt.resource)
			}
			removal := r.tenantDirectoryClear(res)
			if diff := cmp.Diff(tt.wantClear, removal); diff != "" {
				t.Errorf("tenantDirectoryClear() mismatch (-want +got):\n%s", diff)
			}

			standalone, err := r.handlerContent(PatchHandler, res)
			if err != nil {
				t.Fatalf("handlerContent() error = %v", err)
			}
			if _, err := format.Source(append([]byte("package app\n\n"), standalone...)); err != nil {
				t.Fatalf("the rendered handler does not parse: %v\n%s", err, standalone)
			}
			segment := consolidatedCaseData{resourceInfo: res, ResourcePackage: "resources", ReceiverName: "a", Clears: r.tenantClears(res), DirectoryClear: removal}
			data := consolidatedPatchData{
				Resources:           []*resourceInfo{res},
				HasTenant:           true,
				DomainRouteSegment:  "harbors",
				DomainPatternPrefix: "/harbors/{harborID}",
				LocalPackageImports: `"example.com/app/pkg/resources"` + "\n\t" + `"example.com/app/pkg/router"`,
				Package:             "app",
				ResourcePackage:     "resources",
				ApplicationName:     "App",
				ReceiverName:        "a",
				HandlerName:         "PatchResources",
			}
			if res.IsDomainScoped() {
				segment.DomainPatternPrefix = data.DomainPatternPrefix
				data.DomainCases = []consolidatedCaseData{segment}
			} else {
				data.GlobalCases = []consolidatedCaseData{segment}
			}
			c := &client{}
			consolidated, err := c.generateTemplateOutput("consolidatedPatchTemplate", consolidatedPatchTemplate, data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(consolidated); err != nil {
				t.Fatalf("the rendered consolidated handler does not parse: %v\n%s", err, consolidated)
			}

			for name, out := range map[string]string{"the record's own handler": string(standalone), "the consolidated handler": string(consolidated)} {
				if tt.wantCall == "" {
					assertContains(t, name, out, nil, []string{"Directory(ctx, txn, id"})

					continue
				}
				assertInOrder(t, out,
					".New"+tt.resource+"DeletePatchFromPatchSet(id, patchSet).Buffer(ctx, txn, eventSource)",
					tt.wantCall,
					`return errors.Wrap(err, "ClearHarborDirectory()")`,
					"if err := a.StaffIdentity().ClearTenant(ctx, txn, accesstypes.Domain(id.String())); err != nil {",
				)
				// The rule on a directory's own removal is for a tenant that stays: the
				// tenant's delete asks no rule to remove anything, however the rules are
				// spelled (a named set chains before the call).
				assertContains(t, name, out, nil, []string{".Remove(ctx, txn,"})
			}
		})
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
				OwnCreation:         tenantCreationOwnCreation("Harbor"),
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
				"if err := identityClient.AssignRole(ctx, txn, accesstypes.DomainPolicyScope(domain), userPermissions.User(), role); err != nil {",
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
			given := strings.Index(s, "identityClient.AssignRole(ctx, txn,")
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

// Test_declaresNoDirectories pins which tenant record's package gets the reader that
// answers nothing: one that declares no directories and makes the record's rows. A
// record that declares directories has its own reader, and a package that declares the
// record another site serves (the patch handler suppressed) gets none, since whether the
// application's tenants have directories is that site's to say.
func Test_declaresNoDirectories(t *testing.T) {
	t.Parallel()

	declared := &tenantDeclarationColumns{DirectoriesTable: "HarborDirectories"}
	tests := []struct {
		name        string
		suppressed  []HandlerType
		declaration *tenantDeclarationColumns
		want        bool
	}{
		{name: "no directories, the rows made here", want: true},
		{name: "no directories, a handler other than the patch suppressed", suppressed: []HandlerType{ListHandler}, want: true},
		{name: "directories declared", declaration: declared},
		{name: "another site serves the record", suppressed: []HandlerType{ListHandler, ReadHandler, PatchHandler}},
		{name: "another site serves the record, and this package declares directories", suppressed: []HandlerType{PatchHandler}, declaration: declared},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tenant := &resourceInfo{SuppressedHandlers: tt.suppressed}
			if got := declaresNoDirectories(tenant, tt.declaration); got != tt.want {
				t.Errorf("declaresNoDirectories() = %v, want %v", got, tt.want)
			}
		})
	}
}
