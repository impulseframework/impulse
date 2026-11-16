package generation

import (
	"go/format"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
)

// tenantFixtureTables is the synthetic schema behind the tenantfixture's table-backed
// structs: every table keyed once on Id, a key of the table's own that no foreign key
// names and that sits in no interleaved table, so the create path of a struct declaring
// it an impulse.UUID generates it (resourceInfo.PrimaryKeyIsGeneratedUUID). The
// exceptions are Docks, keyed on two columns, and Depots, whose key is a foreign key.
func tenantFixtureTables() map[string]*tableMetadata {
	pk := columnMeta{IsPrimaryKey: true, IsIndex: true, IsUniqueIndex: true}
	foreignPK := columnMeta{IsPrimaryKey: true, IsIndex: true, IsUniqueIndex: true, IsForeignKey: true, ReferencedTable: "Sectors", ReferencedColumn: "Id"}
	compositeKey := columnMeta{IsPrimaryKey: true, IsIndex: true}
	plain := columnMeta{}
	nullable := columnMeta{IsNullable: true}
	// A directories record's table holds the two rules of a tenant's directory with a
	// unique index each: the tenant column alone, and the method with the connection.
	oneTenant := columnMeta{IsIndex: true, IsUniqueIndex: true}
	byTenant := indexMeta{Name: "ByHarborId", Unique: true, Key: []indexColumn{{Column: "HarborId"}}}
	byDirectory := indexMeta{Name: "ByMethodConnection", Unique: true, Key: []indexColumn{{Column: "Method"}, {Column: "Connection"}}}
	byTenantAndDirectory := indexMeta{Name: "ByHarborIdMethodConnection", Unique: true, Key: []indexColumn{{Column: "HarborId"}, {Column: "Method"}, {Column: "Connection"}}}
	directoryIndexes := []indexMeta{byTenant, byDirectory}

	return map[string]*tableMetadata{
		"Sectors":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": plain}},
		"Berths":    {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "SectorId": plain, "Name": plain}},
		"Outposts":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "SectorId": plain}},
		"Docks":     {PkCount: 2, Columns: map[string]columnMeta{"SectorId": compositeKey, "Number": compositeKey}},
		"Hangars":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": plain}},
		"Depots":    {PkCount: 1, Columns: map[string]columnMeta{"Id": foreignPK, "Name": plain}},
		"Regions":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk}},
		"Stations":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": plain, "Motto": nullable, "Opened": plain, "Derived": plain}},
		"Colonies":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": plain}},
		"Enclaves":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk}},
		"Outlands":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk}},
		"Provinces": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk}},
		"Bastions":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk}},
		// The name and the label: the record marking both, and each refused shape.
		"Lagoons": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": plain}},
		"Reefs":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": plain}},
		"Atolls":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Rank": plain}},
		"Islets":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": plain, "Title": nullable}},
		"Shoals":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk}},
		// The declaration's shapes: the record with its flag, the directories record,
		// and each shape the annotations refuse.
		"Harbors":           {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Name": plain, "DirectoryOnly": plain}},
		"HarborDirectories": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": oneTenant, "Method": plain, "Connection": plain, "DefaultSeat": nullable}, Indexes: directoryIndexes},
		"Groynes":           {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": plain, "Method": plain, "Connection": plain}, Indexes: []indexMeta{byDirectory}},
		"Seawalls":          {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": oneTenant, "Method": plain, "Connection": plain}, Indexes: []indexMeta{byTenant, byTenantAndDirectory}},
		"Pontoons":          {PkCount: 1, Columns: map[string]columnMeta{"Name": pk, "HarborId": oneTenant, "Method": plain, "Connection": plain}, Indexes: directoryIndexes},
		"Slipways":          {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": plain, "Seat": plain}},
		"Wharfs":            {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": plain, "Method": plain, "Connection": plain, "Seated": plain}},
		"Breakwaters":       {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": oneTenant, "Method": plain, "Connection": plain}, Indexes: directoryIndexes},
		"Marinas":           {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": oneTenant, "Method": plain, "Connection": plain}, Indexes: directoryIndexes},
		"Lighthouses":       {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": nullable, "Method": plain, "Connection": plain}},
		"Bollards":          {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": plain, "Method": plain, "Connection": plain, "Seat": plain, "Rank": plain}},
		"Piers":             {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Closed": plain}},
		"Quays":             {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Mode": plain}},
		"Beacons":           {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Method": plain, "Connection": plain}},
		"Buoys":             {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": plain, "Method": plain}},
		"Moorings":          {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": plain, "Method": plain, "Connection": plain}},
		"Jetties":           {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HarborId": plain, "Method": plain, "Kind": plain, "Connection": plain}},
		"Anchorages":        {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BerthId": {IsForeignKey: true, ReferencedTable: "Berths", ReferencedColumn: "Id"}, "Method": plain, "Connection": plain}},
	}
}

// tenantFixtureStructs returns the named tenantfixture structs, in order.
func tenantFixtureStructs(t *testing.T, structs map[string]*parser.Struct, names ...string) []*parser.Struct {
	t.Helper()

	pStructs := make([]*parser.Struct, 0, len(names))
	for _, name := range names {
		s := structs[name]
		if s == nil {
			t.Fatalf("struct %q not found in fixture package", name)
		}
		pStructs = append(pStructs, s)
	}

	return pStructs
}

// Test_resolveTenant pins the tenant record's capture on a table-backed struct: the
// well-formed record is marked, and each refused shape names its rule.
func Test_resolveTenant(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "tenantfixture"))

	tests := []struct {
		name       string
		structs    []string
		wantTenant string
		wantShape  tenantCreation
		wantLabel  string
		wantErr    string
	}{
		{
			name:       "the global record keyed by one generated id is the tenant record",
			structs:    []string{"Sector", "Berth"},
			wantTenant: "Sector",
			wantShape:  tenantCreationAdministrator,
		},
		{
			name:       "creation: self-service is the self-service shape",
			structs:    []string{"Station"},
			wantTenant: "Station",
			wantShape:  tenantCreationSelfService,
		},
		{
			name:       "creation: both is both shapes",
			structs:    []string{"Colony"},
			wantTenant: "Colony",
			wantShape:  tenantCreationBoth,
		},
		{
			name:       "creation: administrator spells the default out",
			structs:    []string{"Enclave"},
			wantTenant: "Enclave",
			wantShape:  tenantCreationAdministrator,
		},
		{
			name:       "label: names the word for one tenant",
			structs:    []string{"Lagoon"},
			wantTenant: "Lagoon",
			wantShape:  tenantCreationAdministrator,
			wantLabel:  "Service district",
		},
		{
			name:    "a label the screen cannot show as written is refused",
			structs: []string{"Shoal"},
			wantErr: "struct Shoal: @tenant(label: Shoal's) is not a word the screen shows as written; a label is letters, digits, single spaces and hyphens, starting with a letter",
		},
		{
			name:    "a creation shape the generator does not know is refused naming the three",
			structs: []string{"Outland"},
			wantErr: "struct Outland: @tenant(creation: nowhere) names no creation shape; the shapes are administrator (an administrator alone, the default), self-service (the person who becomes its administrator) and both",
		},
		{
			name:    "the shape without its key is refused",
			structs: []string{"Province"},
			wantErr: `expected 0 positional argument(s), found 1 in "self-service"`,
		},
		{
			name:    "an argument @tenant does not take is refused",
			structs: []string{"Bastion"},
			wantErr: `unknown argument "shape" in "shape: both" (accepted: creation, label)`,
		},
		{
			name:    "@tenant on a tenant-scoped resource is refused",
			structs: []string{"Outpost"},
			wantErr: "struct Outpost: @tenant on a tenant-scoped resource; the tenant record is global, since its rows are the tenants themselves",
		},
		{
			name:    "@tenant on a resource with a compound key is refused",
			structs: []string{"Dock"},
			wantErr: "struct Dock: @tenant on a resource with a compound primary key; the tenant record has one key, the domain in every tenant-scoped URL",
		},
		{
			name:    "@tenant on a resource whose key is a string a person types is refused",
			structs: []string{"Hangar"},
			wantErr: "struct Hangar: @tenant on a resource whose key ID is string; the tenant record's key is an id the create path generates (impulse.UUID), so a tenant's name can change and no key is used twice",
		},
		{
			name:    "@tenant on a resource whose id is a foreign key, which the create path does not generate, is refused",
			structs: []string{"Depot"},
			wantErr: "struct Depot: @tenant on a resource whose key ID is another record's (a foreign key, or the table is interleaved in a parent); the tenant record's key is an id its own create path generates, so a tenant's name can change and no key is used twice",
		},
		{
			name:    "@tenant on a second struct is refused naming the first",
			structs: []string{"Sector", "Region"},
			wantErr: "struct Region: @tenant on a second struct; struct Sector is the tenant record already, and an application has one",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{tableMap: tenantFixtureTables()}
			resources, err := c.structsToResources(tenantFixtureStructs(t, structs, tt.structs...))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("structsToResources() error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("structsToResources() error = %v", err)
			}
			for _, res := range resources {
				if got, want := res.IsTenant, res.Name() == tt.wantTenant; got != want {
					t.Errorf("%s.IsTenant = %v, want %v", res.Name(), got, want)
				}
				if res.IsTenant && res.TenantCreation != tt.wantShape {
					t.Errorf("%s.TenantCreation = %q, want %q", res.Name(), res.TenantCreation, tt.wantShape)
				}
				if res.IsTenant && res.TenantLabel != tt.wantLabel {
					t.Errorf("%s.TenantLabel = %q, want %q", res.Name(), res.TenantLabel, tt.wantLabel)
				}
			}
		})
	}
}

// Test_rejectTenantAnnotation pins the refusal of @tenant on the kinds that have no
// table: a view, a computed resource and a method, each through its own extractor.
func Test_rejectTenantAnnotation(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "tenantfixture"))

	tests := []struct {
		name       string
		structName string
		extract    func(c *client, pStruct *parser.Struct) error
		wantErr    string
	}{
		{
			name:       "a view",
			structName: "Ledger",
			extract: func(c *client, pStruct *parser.Struct) error {
				_, err := c.structsToVirtualResources([]*parser.Struct{pStruct})

				return err
			},
			wantErr: "struct Ledger: @tenant on a @virtual struct; the tenant record is a table-backed @resource, since its rows are the tenants",
		},
		{
			name:       "a computed resource",
			structName: "Tally",
			extract: func(c *client, pStruct *parser.Struct) error {
				_, err := c.structsToCompResources([]*parser.Struct{pStruct})

				return err
			},
			wantErr: "struct Tally: @tenant on a @computed struct; the tenant record is a table-backed @resource, since its rows are the tenants",
		},
		{
			name:       "a method",
			structName: "Reindex",
			extract: func(c *client, pStruct *parser.Struct) error {
				_, err := c.structsToRPCMethods([]*parser.Struct{pStruct})

				return err
			},
			wantErr: "struct Reindex: @tenant on a @rpc struct; the tenant record is a table-backed @resource, since its rows are the tenants",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pStruct := structs[tt.structName]
			if pStruct == nil {
				t.Fatalf("struct %q not found in fixture package", tt.structName)
			}
			err := tt.extract(&client{tableMap: tenantFixtureTables()}, pStruct)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("extraction error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// Test_resolveTenantRecord pins what the package-level resolution does once every kind
// is extracted: the record names the segment pair, nothing tenant-scoped keeps the
// defaults, and a tenant-scoped resource, computed resource or method without a record
// is refused with the one sentence.
func Test_resolveTenantRecord(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadCollectionFixture(t))
	tenantStructs := fixtureStructs(loadFixture(t, "tenantfixture"))

	domainScoped := func(res *resourceInfo) { res.PermissionScope = accesstypes.DomainPermissionScope }

	tests := []struct {
		name        string
		resources   []*resourceInfo
		computed    []*computedResource
		methods     []*rpcMethodInfo
		wantSegment string
		wantParam   string
		wantWord    tenantWordData
		wantErr     string
	}{
		{
			name: "the record names the segment pair and the word",
			resources: []*resourceInfo{
				fixtureResource(t, tenantStructs, "Sector", func(res *resourceInfo) { res.IsTenant = true }),
				fixtureResource(t, structs, "Vault", domainScoped),
			},
			wantSegment: "sectors",
			wantParam:   "sectorID",
			wantWord:    tenantWordData{One: "Sector", Many: "Sectors"},
		},
		{
			name: "a label is the word, the segment pair stays the record's",
			resources: []*resourceInfo{
				fixtureResource(t, tenantStructs, "Sector", func(res *resourceInfo) { res.IsTenant, res.TenantLabel = true, "Service district" }),
				fixtureResource(t, structs, "Vault", domainScoped),
			},
			wantSegment: "sectors",
			wantParam:   "sectorID",
			wantWord:    tenantWordData{One: "Service district", Many: "Service districts"},
		},
		{
			name:        "nothing tenant-scoped and no record keeps the defaults",
			resources:   []*resourceInfo{fixtureResource(t, structs, "Fossil", nil)},
			wantSegment: "domains",
			wantParam:   "domain",
		},
		{
			name:      "a tenant-scoped resource without a record is refused",
			resources: []*resourceInfo{fixtureResource(t, structs, "Vault", domainScoped)},
			wantErr:   "struct Vault: a tenant-scoped resource needs a tenant record; annotate it @tenant",
		},
		{
			name: "a tenant-scoped computed resource without a record is refused",
			computed: []*computedResource{{
				Struct:          structs["Summary"],
				PermissionScope: accesstypes.DomainPermissionScope,
			}},
			wantErr: "struct Summary: a tenant-scoped resource needs a tenant record; annotate it @tenant",
		},
		{
			name: "a tenant-scoped method without a record is refused",
			methods: []*rpcMethodInfo{{
				Struct:          structs["DoSomething"],
				PermissionScope: accesstypes.DomainPermissionScope,
			}},
			wantErr: "struct DoSomething: a tenant-scoped resource needs a tenant record; annotate it @tenant",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &resourceGenerator{
				client:             &client{},
				domainRouteSegment: defaultDomainRouteSegment,
				domainRouteParam:   defaultDomainRouteParam,
			}
			r.resources = tt.resources
			r.computedResources = tt.computed
			r.rpcMethods = tt.methods

			// Generate's order: the record resolves before the methods are extracted,
			// and the methods are checked against it afterwards.
			err := r.resolveTenantRecord()
			if err == nil {
				err = r.requireTenantRecordForMethods()
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveTenantRecord() error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveTenantRecord() error = %v", err)
			}
			if r.domainRouteSegment != tt.wantSegment || r.domainRouteParam != tt.wantParam {
				t.Errorf("segment pair = %q/%q, want %q/%q", r.domainRouteSegment, r.domainRouteParam, tt.wantSegment, tt.wantParam)
			}
			if r.domainWord != tt.wantWord {
				t.Errorf("domainWord = %+v, want %+v", r.domainWord, tt.wantWord)
			}
		})
	}
}

// Test_tenantsTemplate pins the generated roster constructor: named after the record,
// carrying its table and key column into resource.NewTenantRoster.
func Test_tenantsTemplate(t *testing.T) {
	t.Parallel()

	c := &client{}
	out, err := c.generateTemplateOutput("tenantsTemplate", tenantsTemplate, &tenantsData{
		Source:    "resources",
		Package:   "app",
		Name:      "Sector",
		Table:     "Sectors",
		KeyColumn: "Id",
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	if _, err := format.Source(out); err != nil {
		t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
	}

	for _, want := range []string{
		"package app",
		"func NewSectorRoster(client resource.Client, opts ...resource.TenantRosterOption) *resource.TenantRoster {",
		`return resource.NewTenantRoster(client, "Sectors", "Id", opts...)`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("tenantsTemplate output missing %q:\n%s", want, out)
		}
	}
}

// Test_tenantsTemplate_names pins the roster constructor of a record that marks its
// name: the roster reads the name column beside the key.
func Test_tenantsTemplate_names(t *testing.T) {
	t.Parallel()

	c := &client{}
	out, err := c.generateTemplateOutput("tenantsTemplate", tenantsTemplate, &tenantsData{
		Source:      "resources",
		Package:     "app",
		Name:        "Lagoon",
		Table:       "Lagoons",
		KeyColumn:   "Id",
		RecordLower: "lagoon",
		NameColumn:  "Name",
		NameField:   "Name",
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	if _, err := format.Source(out); err != nil {
		t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
	}
	for _, want := range []string{
		"// It reads each lagoon's name (Lagoon.Name) beside its key",
		`opts = append([]resource.TenantRosterOption{resource.WithTenantNames("Name")}, opts...)`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("tenantsTemplate output missing %q:\n%s", want, out)
		}
	}
}

// Test_tenantNameColumn pins the record's name mark: captured on the record, and refused
// off it, on a column that is not a string, and twice.
func Test_tenantNameColumn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		structs []string
		wantErr string
	}{
		{name: "the record's name column is captured", structs: []string{"Lagoon"}},
		{
			name:    "@tenantName on a resource that is not the tenant record is refused",
			structs: []string{"Lagoon", "Reef"},
			wantErr: "struct Reef field Name: @tenantName on a resource that is not the tenant record; a tenant's name is the @tenant record's column",
		},
		{
			name:    "@tenantName on a column that is not a string is refused",
			structs: []string{"Atoll"},
			wantErr: "struct Atoll field Rank: @tenantName on a int64 column; a tenant's name is a string or a nullable string (*string)",
		},
		{
			name:    "@tenantName on a second column is refused naming the first",
			structs: []string{"Islet"},
			wantErr: "struct Islet field Title: @tenantName on a second column; field Name carries it already, and a tenant has one name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := declarationResources(t, tt.structs...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if field := r.tenantRecord().TenantNameField; field == nil || field.Name() != "Name" {
				t.Errorf("TenantNameField = %v, want Name", field)
			}
		})
	}
}

// Test_screenWords pins a Go name spelled as the screen shows it.
func Test_screenWords(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{"Sector": "Sector", "ShipClass": "Ship class", "ShipClasses": "Ship classes", "": ""} {
		if got := screenWords(name); got != want {
			t.Errorf("screenWords(%q) = %q, want %q", name, got, want)
		}
	}
}

// Test_patchTemplate_tenant pins the standalone patch handler of the tenant record and
// of the directories record: a write of either marks the transaction, the roster is
// reloaded after the commit, and the tenants kind is signaled after the roster and
// before the answer; a resource that is neither carries none of it.
func Test_patchTemplate_tenant(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "tenantfixture"))

	tests := []struct {
		name     string
		tenant   bool
		creation tenantCreation
		// auth binds the default outlet to the staff auth, which the record's create
		// seats its creator through.
		auth            bool
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:   "the record reloads the roster after the commit and signals",
			tenant: true,
			wantContains: []string{
				"tenantsWritten := false",
				"tenantsWritten = false",
				"tenantsWritten = true",
				"if tenantsWritten {",
				"if err := a.TenantRoster().Reload(ctx); err != nil {",
				"if err := a.LiveService().Signal(ctx, resource.KindTenants); err != nil {",
				"logger.FromCtx(ctx).Errorf(",
			},
		},
		{
			name:     "a self-service record refuses the create path naming the route",
			tenant:   true,
			creation: tenantCreationSelfService,
			wantContains: []string{
				`return httpio.NewBadRequestMessage("a Sector is created by the person who becomes its administrator, through the create-sector route (@tenant(creation: self-service)); no administrator creates one")`,
				"if err := a.TenantRoster().Reload(ctx); err != nil {",
			},
		},
		{
			name:   "the record's create gives its creator the administrator roles in its transaction, and waits on them between the roster's reload and the signal",
			tenant: true,
			auth:   true,
			wantContains: []string{
				"roles := a.SectorAdministratorRoles()",
				`panic("SectorAdministratorRoles() names no role: the person who creates a Sector holds its administrator role, which the application names there")`,
				"identity := a.StaffIdentity()",
				"var created []impulse.UUID",
				"userPermissions := a.UserPermissions(r)",
				"created = nil",
				// The create generates the id: the caller names none, and the creator's
				// roles are given under the id the buffered row carries, whose text is
				// the tenant's domain.
				"patch, err := tenantfixture.NewSectorCreatePatchFromPatchSet(patchSet)",
				"id := patch.ID()",
				"if err := identity.AssignRole(ctx, txn, accesstypes.DomainPolicyScope(accesstypes.Domain(id.String())), userPermissions.User(), role); err != nil {",
				"created = append(created, id)",
				"for _, id := range created {",
				"if err := resource.AwaitFirstAdministrator(ctx, userPermissions, accesstypes.Domain(id.String())); err != nil {",
				`logger.FromCtx(ctx).Errorf("sector %s: %v", id, err)`,
				"if err := a.TenantRoster().Reload(ctx); err != nil {",
				"if err := a.LiveService().Signal(ctx, resource.KindTenants); err != nil {",
			},
			wantNotContains: []string{
				// Nothing is removed after the commit: the row and the membership are one
				// transaction, written through the auth's identity client.
				"SeatFirstAdministrator", "UserSurface", ".NewSectorDeletePatch(row)",
				// No create takes its key from the caller, so no row a create meets is
				// there already: nothing is skipped, and no key is converted as a string.
				"NewSectorCreatePatchFromPatchSet(id, patchSet)", "RowSkipped", "OrSkip", "RequireCreatePath", "accesstypes.Domain(id))", "accesstypes.Domain(domain))",
			},
		},
		{
			name:   "the record's delete clears what the auth holds for the tenant, in the delete's transaction",
			tenant: true,
			auth:   true,
			wantContains: []string{
				"if err := tenantfixture.NewSectorDeletePatchFromPatchSet(id, patchSet).Buffer(ctx, txn, eventSource); err != nil {",
				"if err := a.StaffIdentity().ClearTenant(ctx, txn, accesstypes.Domain(id.String())); err != nil {",
				`return errors.Wrap(err, "StaffIdentity().ClearTenant()")`,
			},
		},
		{
			name:            "a program that generates no auth's management clears nothing: the memberships are the application's own",
			tenant:          true,
			wantNotContains: []string{"ClearTenant"},
		},
		{
			name:            "a self-service record's handler seats nobody, since it creates nothing",
			tenant:          true,
			auth:            true,
			creation:        tenantCreationSelfService,
			wantNotContains: []string{"AwaitFirstAdministrator", "AssignRole", "AdministratorRoles"},
		},
		{
			name:            "another resource carries none of it",
			auth:            true,
			wantNotContains: []string{"tenantsWritten", "TenantRoster()", "KindTenants", "ClearTenant"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := fixtureResource(t, structs, "Sector", func(res *resourceInfo) {
				res.IsTenant, res.TenantCreation = tt.tenant, tt.creation
			})
			r := &resourceGenerator{client: &client{resource: "testdata/tenantfixture"}, applicationName: "App", receiverName: "a"}
			if tt.auth {
				r.defaultOutlet.auth = &outletAuth{importPath: "example.com/app/pkg/auth/staff"}
				r.identity = generatedIdentityOf("example.com/app/pkg/auth/staff")
			}
			out, err := r.handlerContent(PatchHandler, res)
			if err != nil {
				t.Fatalf("handlerContent() error = %v", err)
			}
			if _, err := format.Source(append([]byte("package app\n\n"), out...)); err != nil {
				t.Fatalf("the rendered handler does not parse: %v\n%s", err, out)
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(string(out), want) {
					t.Errorf("patch handler missing %q:\n%s", want, out)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(string(out), notWant) {
					t.Errorf("patch handler must not contain %q:\n%s", notWant, out)
				}
			}
			if !tt.tenant || tt.creation.SelfService() {
				return
			}

			// After the commit means after ExecuteFunc returns; the roster before the
			// signal, the signal before the answer, and the creator's seat, where the
			// record's create writes one, between the roster and the signal.
			s := string(out)
			commit := strings.Index(s, "}); err != nil {")
			reload := strings.Index(s, "a.TenantRoster().Reload(ctx)")
			signal := strings.LastIndex(s, "Signal(ctx, resource.KindTenants)")
			answer := strings.Index(s, "return httpio.NewEncoder(w).Ok(")
			if commit >= reload || reload >= signal || signal >= answer {
				t.Errorf("order: commit %d, roster %d, signal %d, answer %d; want ascending:\n%s", commit, reload, signal, answer, out)
			}
			if wait := strings.Index(s, "resource.AwaitFirstAdministrator("); tt.auth && (reload >= wait || wait >= signal) {
				t.Errorf("order: roster %d, wait %d, signal %d; want the wait between them:\n%s", reload, wait, signal, out)
			}
			if given := strings.Index(s, "identity.AssignRole(ctx, txn,"); tt.auth && (given < 0 || given >= commit) {
				t.Errorf("order: membership %d, commit %d; want the membership written inside the transaction:\n%s", given, commit, out)
			}
			// The membership is the create's own: after the row is buffered and its
			// generated id read, before the update's case begins.
			row := strings.Index(s, "patch.Buffer(ctx, txn, eventSource)")
			generated := strings.Index(s, "id := patch.ID()")
			given := strings.Index(s, "identity.AssignRole(ctx, txn,")
			update := strings.Index(s, "case resource.OperationUpdate:")
			if tt.auth && (row < 0 || row >= generated || generated >= given || given >= update) {
				t.Errorf("order: row %d, generated id %d, membership %d, update %d; want the membership in the create, under the id the row was buffered with:\n%s", row, generated, given, update, out)
			}
		})
	}
}

// Test_authzTestTemplate_tenantContract pins the generated matrix's harness contract:
// newTestHandler adds the suite's domain value to the test application's roster, and
// the retired seam is not asked for.
func Test_authzTestTemplate_tenantContract(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		`newTestHandler adds the suite's domain (the generated router's TestDomain) to the test`,
		"tenant roster with Add (resource.TenantRoster.Add)",
	} {
		if !strings.Contains(authzTestTemplate, want) {
			t.Errorf("authzTestTemplate missing %q", want)
		}
	}
	for _, notWant := range []string{"DomainExists", "DomainVisible"} {
		if strings.Contains(authzTestTemplate, notWant) {
			t.Errorf("authzTestTemplate still asks the harness for %s", notWant)
		}
	}
}
