package generation

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/resource"
)

// nameFixtureTables is the synthetic schema behind the namefixture structs: every table
// keyed by one Id, each pointing column a foreign key except the two a struct declares
// with @enumerate, and Grades shaped as an enumeration table.
func nameFixtureTables() map[string]*tableMetadata {
	pk := columnMeta{IsPrimaryKey: true, IsIndex: true, IsUniqueIndex: true}
	plain := columnMeta{}
	nullable := columnMeta{IsNullable: true}
	to := func(table string) columnMeta {
		return columnMeta{IsForeignKey: true, ReferencedTable: table, ReferencedColumn: "Id"}
	}

	return map[string]*tableMetadata{
		"Bases":    {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Title": plain}},
		"Wings":    {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "CallSign": plain}},
		"Kits":     {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Label": nullable}},
		"Bays":     {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "WingId": to("Wings"), "Code": plain}},
		"Berths":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "BayId": to("Bays")}},
		"Crates":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "Weight": plain}},
		"Permits":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "WingId": to("Wings"), "LeadWing": nullable, "KitId": to("Kits"), "CrateId": to("Crates"), "Grade": to("Grades"), "MemberId": plain}},
		"Ledgers":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "WingId": to("Wings"), "KitId": to("Kits"), "Grade": to("Grades")}},
		"Stocks":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "WingId": to("Wings"), "KitId": to("Kits")}},
		"Clashes":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "WingId": to("Wings"), "WingName": plain}},
		"Pairs":    {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "WingId": to("Wings"), "Wing": plain}},
		"Counteds": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Count": plain}},
		"Twices":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "First": plain, "Second": plain}},
		"Forts":    {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Title": plain}},
		"Grades":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Description": plain}},
		"Vaults":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Phrase": plain}},
		"Drops":    {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Slip": nullable}},
		"Folders":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Scan": plain}},
		"Sorties":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "WingId": to("Wings"), "KitId": to("Kits")}},
		"Detours":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "CrateId": to("Crates")}},
		"Astrays":  {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "WingId": to("Wings")}},
	}
}

// nameFixtureResources extracts the named namefixture structs, tables and views, and
// resolves across them the way Generate does up to the name fields: the enumerations,
// the tenant record, then the name fields.
func nameFixtureResources(t *testing.T, names ...string) (*client, error) {
	t.Helper()

	structs := fixtureStructs(loadFixture(t, "namefixture"))
	picked := tenantFixtureStructs(t, structs, names...)
	c := &client{tableMap: nameFixtureTables(), enumerateTables: map[string]string{"Grades": "Grade"}}
	resources, err := c.structsToResources(picked)
	if err != nil {
		return nil, err
	}
	views, err := c.structsToVirtualResources(picked)
	if err != nil {
		return nil, err
	}
	c.resources = slices.Concat(resources, views)
	r := &resourceGenerator{client: c, routePrefix: "api", applicationName: "App", receiverName: "a", domainRouteSegment: defaultDomainRouteSegment, domainRouteParam: defaultDomainRouteParam}
	if err := r.resolveFieldEnumerations(c.resources, nil); err != nil {
		return nil, err
	}
	if err := r.resolveTenantRecord(); err != nil {
		return nil, err
	}

	err = c.resolveNameFields(c.resources)

	return c, err
}

// Test_resolveNameFields pins the name field the generator adds beside an id: one for a
// foreign key or a declared enumeration into a resource that says what its rows are
// known by (@name, or the tenant record's @tenantName), into a view that does, and into
// an enumeration table, which needs no declaration; none, and no refusal, where the
// pointed-to resource declares no name; and none for the name of a tenant's row on a
// list that is asked outside one tenant.
func Test_resolveNameFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		structs  []string
		resource string
		want     []string
	}{
		{
			name:     "each kind of row a tenant's resource points to",
			structs:  []string{"Base", "Wing", "Kit", "Crate", "Member", "Permit"},
			resource: "Permit",
			want: []string{
				`{Field: "BaseName", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
				`{Field: "WingName", Key: "WingID", Resource: "Wings", Tag: "callSign", Table: "Wings", KeyColumn: "Id", NameColumn: "CallSign", TenantColumn: "BaseId"}`,
				`{Field: "LeadWingName", Key: "LeadWing", Resource: "Wings", Tag: "callSign", Table: "Wings", KeyColumn: "Id", NameColumn: "CallSign", TenantColumn: "BaseId"}`,
				`{Field: "KitName", Key: "KitID", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""}`,
				`{Field: "GradeName", Key: "Grade", Resource: "", Tag: "", Table: "Grades", KeyColumn: "Id", NameColumn: "Description", TenantColumn: ""}`,
			},
		},
		{
			name:     "a row that reaches its tenant through another table is held to the tenant by that way",
			structs:  []string{"Base", "Wing", "Bay", "Berth"},
			resource: "Berth",
			want: []string{
				`{Field: "BaseName", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
				`{Field: "BayName", Key: "BayID", Resource: "Bays", Tag: "code", Table: "Bays", KeyColumn: "Id", NameColumn: "Code", TenantColumn: "WingId", TenantPath: []resource.BindingHop{{Table: "Wings", JoinColumn: "Id", Column: "BaseId"}}}`,
			},
		},
		{
			name:     "a resource with no name gives no name field and no refusal",
			structs:  []string{"Base", "Crate"},
			resource: "Crate",
			want:     []string{`{Field: "BaseName", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`},
		},
		{
			name:     "a list also asked across tenants reads no name of a tenant's row",
			structs:  []string{"Base", "Wing", "Kit", "Ledger"},
			resource: "Ledger",
			want: []string{
				`{Field: "BaseName", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
				`{Field: "KitName", Key: "KitID", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""}`,
				`{Field: "GradeName", Key: "Grade", Resource: "", Tag: "", Table: "Grades", KeyColumn: "Id", NameColumn: "Description", TenantColumn: ""}`,
			},
		},
		{
			name:     "a global resource reads no name of a tenant's row",
			structs:  []string{"Base", "Wing", "Kit", "Stock"},
			resource: "Stock",
			want:     []string{`{Field: "KitName", Key: "KitID", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""}`},
		},
		{
			name:     "a view pointing to a named resource",
			structs:  []string{"Kit", "Roster"},
			resource: "Roster",
			want:     []string{`{Field: "KitName", Key: "KitID", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""}`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := nameFixtureResources(t, tt.structs...)
			if err != nil {
				t.Fatalf("nameFixtureResources() error = %v", err)
			}
			var got []string
			for _, res := range c.resources {
				if res.Name() != tt.resource {
					continue
				}
				for _, name := range res.NameFields {
					got = append(got, name.GoLiteral())
				}
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("name fields of %s mismatch (-want +got):\n%s", tt.resource, diff)
			}
		})
	}
}

// Test_nameFieldRefusals pins each refusal: a generated name that a declared field or
// another generated name already has, and @name where it cannot say what a row is known by.
func Test_nameFieldRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		structs []string
		wantErr string
	}{
		{name: "a declared field has the generated name", structs: []string{"Base", "Wing", "Clash"}, wantErr: "struct Clash field WingID: the generator adds the field WingName beside it, carrying the name of the Wings row it points to, and the struct declares a field WingName already; remove the struct's WingName where it carried that name, since the generated field now does, or rename it"},
		{name: "two ids are given the same name", structs: []string{"Base", "Wing", "Pair"}, wantErr: "struct Pair field Wing: the generator adds the field WingName beside it, carrying the name of the Wings row it points to, and adds a field of that name beside WingID too; rename one of the two fields"},
		{name: "a number", structs: []string{"Counted"}, wantErr: "struct Counted field Count: @name on an int64 field; the name a row is known by is a string or a nullable string (*string)"},
		{name: "a secret", structs: []string{"Vault"}, wantErr: "struct Vault field Phrase: @name on a secret (conditions:\"secret\"); a row's name is shown to everyone who may read the resource's names, and a secret is never returned, so mark a field the resource returns or remove the annotation"},
		{name: "a write-only field", structs: []string{"Drop"}, wantErr: "struct Drop field Slip: @name on a write-only field (conditions:\"input_only\"); a row's name is shown to everyone who may read the resource's names, and a write-only field is never returned, so mark a field the resource returns or remove the annotation"},
		{name: "a file's store key", structs: []string{"Folder"}, wantErr: "struct Folder field Scan: @name on a file's store key (@file); a row's name is shown to everyone who may read the resource's names, and a file's store key is never returned, so mark a field the resource returns or remove the annotation"},
		{name: "an order by the name of a row that has none", structs: []string{"Base", "Crate", "Detour"}, wantErr: "struct Detour: @order names CrateName, which is not a field of Detour: the generator adds a name field beside CrateID only where the row CrateID points to has a name the list can read (@name), and it adds none here; name a field the struct declares in @order, or declare the pointed-to resource's @name"},
		{name: "an order by a name no field could be given", structs: []string{"Base", "Wing", "Astray"}, wantErr: "SquadName is not a field of Astray"},
		{name: "two fields", structs: []string{"Twice"}, wantErr: "struct Twice field Second: @name on a second field; field First carries it already, and a row is known by one name"},
		{name: "the tenant record", structs: []string{"Fort"}, wantErr: "struct Fort field Title: @name on the tenant record; a tenant's name is declared with @tenantName, which names the record's rows wherever @name would, so write @tenantName here in its place"},
		{name: "an enumeration table", structs: []string{"Grade"}, wantErr: "struct Grade field Description: @name on an enumeration table; its rows are known by their Description column already, so remove the annotation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := nameFixtureResources(t, tt.structs...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("nameFixtureResources() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// Test_declareRowName_unservedField pins the refusal of @name on a field the resource
// does not carry: extraction refuses such a struct before its annotations are read, so
// the declaration is given a resource without the field.
func Test_declareRowName_unservedField(t *testing.T) {
	t.Parallel()

	kit := fixtureStructs(loadFixture(t, "namefixture"))["Kit"]
	annotations, err := scanStruct(kit)
	if err != nil {
		t.Fatalf("scanStruct() error = %v", err)
	}
	c := &client{tableMap: nameFixtureTables()}
	res := &resourceInfo{TypeInfo: kit.TypeInfo}

	err = c.declareRowName(res, kit, annotations)
	wantErr := "struct Kit field Label: @name on a field the resource does not serve, so no list could read a row's name from it; mark a field the resource serves or remove the annotation"
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("declareRowName() error = %v, want error containing %q", err, wantErr)
	}
	if res.RowNameField != nil {
		t.Errorf("declareRowName() recorded the name field %s on a refusal", res.RowNameField.Name())
	}
}

// Test_orderByNameField pins that a default order (@order) may name a name field the
// generator adds, on a table and on a view, among the fields the struct declares: the
// declaration carries it as it carries a declared field.
func Test_orderByNameField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		structs  []string
		resource string
		want     []resource.SortField
		wantOpt  string
	}{
		{
			name:     "a table ordered by two name fields",
			structs:  []string{"Base", "Wing", "Kit", "Sortie"},
			resource: "Sortie",
			want:     []resource.SortField{{Field: "WingName", Direction: resource.SortAscending}, {Field: "KitName", Direction: resource.SortDescending}},
			wantOpt:  `WithPaging(resource.Paging{Order: []resource.SortField{{Field: "WingName", Direction: resource.SortAscending}, {Field: "KitName", Direction: resource.SortDescending}}})`,
		},
		{
			name:     "a view ordered by a name field",
			structs:  []string{"Kit", "Muster"},
			resource: "Muster",
			want:     []resource.SortField{{Field: "KitName", Direction: resource.SortAscending}},
			wantOpt:  `WithPaging(resource.Paging{Order: []resource.SortField{{Field: "KitName", Direction: resource.SortAscending}}})`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := nameFixtureResources(t, tt.structs...)
			if err != nil {
				t.Fatalf("nameFixtureResources() error = %v", err)
			}
			i := slices.IndexFunc(c.resources, func(res *resourceInfo) bool { return res.Name() == tt.resource })
			if i < 0 {
				t.Fatalf("resource %s was not extracted", tt.resource)
			}
			res := c.resources[i]
			if diff := cmp.Diff(tt.want, res.DeclaredOrder); diff != "" {
				t.Errorf("DeclaredOrder of %s mismatch (-want +got):\n%s", tt.resource, diff)
			}
			if got := res.PagingOption(); !strings.HasSuffix(got, tt.wantOpt) {
				t.Errorf("PagingOption() = %s, want it to end %s", got, tt.wantOpt)
			}
		})
	}
}

// Test_rejectRowName pins that a computed resource, whose rows no statement reads,
// refuses @name.
func Test_rejectRowName(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "namefixture"))
	c := &client{}
	_, err := c.structsToCompResources([]*parser.Struct{structs["Tally"]})
	wantErr := "struct Tally field Shown: @name on a computed resource; a name is looked up in a table or a view by the statement that lists the rows pointing to it, and a computed resource has neither, so remove the annotation"
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("structsToCompResources() error = %v, want error containing %q", err, wantErr)
	}
}

// Test_nameFieldName pins the spelling: the id field's own name with a trailing Id
// dropped, then Name, and none for a field that is called Id alone.
func Test_nameFieldName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field  string
		want   string
		wantOK bool
	}{
		{field: "SquadronID", want: "SquadronName", wantOK: true},
		{field: "SquadronId", want: "SquadronName", wantOK: true},
		{field: "MissionStatus", want: "MissionStatusName", wantOK: true},
		{field: "Paid", want: "PaidName", wantOK: true},
		{field: "ID"},
		{field: "Id"},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()

			got, ok := nameFieldName(tt.field)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("nameFieldName(%q) = %q, %v, want %q, %v", tt.field, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// Test_tsNameFields_FieldNameTags pins what a resource's field-name constants are
// written for: its registered tags, then its name fields. A resource with no name field,
// as every computed resource is, has its tags alone, so a page that names a name field
// of one does not compile.
func Test_tsNameFields_FieldNameTags(t *testing.T) {
	t.Parallel()

	names := tsNameFields{"Permits": {"wingName", "gradeName"}}
	tags := []accesstypes.Tag{"id", "wingId", "grade"}

	if diff := cmp.Diff([]accesstypes.Tag{"id", "wingId", "grade", "wingName", "gradeName"}, names.FieldNameTags("Permits", tags)); diff != "" {
		t.Errorf("FieldNameTags(Permits) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(tags, names.FieldNameTags("Tallies", tags)); diff != "" {
		t.Errorf("FieldNameTags(Tallies) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]accesstypes.Tag{"id", "wingId", "grade"}, tags); diff != "" {
		t.Errorf("FieldNameTags changed the registered tags (-want +got):\n%s", diff)
	}
}
