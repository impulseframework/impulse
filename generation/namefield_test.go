package generation

import (
	"go/format"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
)

// nameFixtureTables is the synthetic schema behind the namefixture structs: every table
// keyed by one Id, each pointing column a foreign key except the ones a struct declares
// with @enumerate, Grades shaped as an enumeration table, and Seals with a search index
// over its motto.
func nameFixtureTables() map[string]*tableMetadata {
	pk := columnMeta{IsPrimaryKey: true, IsIndex: true, IsUniqueIndex: true}
	plain := columnMeta{}
	nullable := columnMeta{IsNullable: true}
	to := func(table string) columnMeta {
		return columnMeta{IsForeignKey: true, ReferencedTable: table, ReferencedColumn: "Id"}
	}
	indexedTo := func(table string) columnMeta {
		column := to(table)
		column.IsIndex = true

		return column
	}
	indexed := columnMeta{IsIndex: true}

	return map[string]*tableMetadata{
		"Bases":    {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Title": plain, "Disabled": plain}},
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
		"Badges":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Title": indexed}},
		"Waybills": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "KitId": to("Kits")}},
		"Passes":   {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "HolderId": plain, "GroupId": to("CrewGroups"), "LooseGroupId": plain, "IssuerId": plain}},
		"Rolls":    {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HolderId": plain, "GroupId": plain}},
		"Routes": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "BaseId": to("Bases"), "KitId": to("Kits"), "PickedKitId": to("Kits"), "PatrolKitId": to("Kits"),
			"WingId": to("Wings"), "PickedWingId": to("Wings"), "CrateId": to("Crates"), "PickedCrateId": to("Crates"),
		}},
		"CrewGroups":       {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "DomainId": plain, "Name": plain}},
		"CrewSessionUsers": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Username": plain}},
		"Chores": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "BaseId": to("Bases"), "HolderId": to("CrewSessionUsers"), "GroupId": to("CrewGroups"),
			"AssigneeId": to("CrewSessionUsers"), "TeamId": to("CrewGroups"), "PickedAssigneeId": plain, "PickedTeamId": plain,
		}},
		"Errands": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "HolderId": to("CrewSessionUsers"), "GroupId": to("CrewGroups"), "AssigneeId": to("CrewSessionUsers"), "TeamId": to("CrewGroups"),
		}},
		"Manifests": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "BaseId": to("Bases"), "HangarId": indexed, "BeaconId": indexed, "PatrolId": indexed,
		}},
		"Tours": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "BaseId": to("Bases"), "HangarId": plain, "MemberId": plain}},
		"Seals": {
			PkCount: 1,
			Columns: map[string]columnMeta{
				"Id": pk, "Motto": {SpannerType: "STRING(MAX)"},
				"Motto_Search": {SpannerType: tokenListType, GenerationExpression: "TOKENIZE_SUBSTRING(Motto)"},
			},
			Indexes: []indexMeta{{Name: "SealsSearch", Search: true, Key: []indexColumn{{Column: "Motto_Search"}}}},
		},
		"Wards": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "Alias": indexed}},
		"Rotas": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "BaseId": to("Bases"), "HangarId": plain, "MemberId": plain, "WingId": to("Wings"), "DrillId": plain, "ShiftId": plain, "RationId": plain,
		}},
		"Depots": {PkCount: 1, Columns: map[string]columnMeta{"Id": pk, "HangarId": plain, "ShiftId": plain, "DrillId": plain}},
		"Dockets": {PkCount: 1, Columns: map[string]columnMeta{
			"Id": pk, "BadgeId": indexedTo("Badges"), "Grade": indexedTo("Grades"), "SealId": indexedTo("Seals"),
			"SpareSealId": to("Seals"), "LooseBadgeId": to("Badges"), "KitId": indexedTo("Kits"), "WardId": indexedTo("Wards"),
		}},
	}
}

// nameFixtureResources extracts the named namefixture structs, tables, views and computed
// lists, and resolves across them the way Generate does up to the name fields: the
// enumerations, the tenant record, then the name fields.
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
	c.computedResources, err = c.structsToCompResources(picked)
	if err != nil {
		return nil, err
	}
	r := &resourceGenerator{client: c, routePrefix: "api", applicationName: "App", receiverName: "a", domainRouteSegment: defaultDomainRouteSegment, domainRouteParam: defaultDomainRouteParam}
	if err := r.resolveFieldEnumerations(c.resources, c.computedResources); err != nil {
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
// known by (@name, or the tenant record's @tenantName), into a view that does, read from
// the view (View), and into an enumeration table, which needs no declaration; none, and
// no refusal, where the pointed-to resource declares no name; and none for the name of a
// tenant's row, a table's or a view's, on a list that is asked outside one tenant.
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
				`{Field: "MemberName", Key: "MemberID", Resource: "Members", Tag: "shown", Table: "Members", KeyColumn: "Id", NameColumn: "Shown", View: true, TenantColumn: ""}`,
			},
		},
		{
			name:     "a tenant's view and a global one that say what their rows are known by, and one that does not",
			structs:  []string{"Base", "Hangar", "Beacon", "Patrol", "Manifest"},
			resource: "Manifest",
			want: []string{
				`{Field: "BaseName", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
				`{Field: "HangarName", Key: "HangarID", Resource: "Hangars", Tag: "door", Table: "Hangars", KeyColumn: "Id", NameColumn: "Door", View: true, TenantColumn: "BaseId"}`,
				`{Field: "BeaconName", Key: "BeaconID", Resource: "Beacons", Tag: "signal", Table: "Beacons", KeyColumn: "Id", NameColumn: "Signal", View: true, TenantColumn: ""}`,
			},
		},
		{
			name:     "a list also asked across tenants reads no name of a tenant's view",
			structs:  []string{"Base", "Hangar", "Member", "Tour"},
			resource: "Tour",
			want: []string{
				`{Field: "BaseName", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
				`{Field: "MemberName", Key: "MemberID", Resource: "Members", Tag: "shown", Table: "Members", KeyColumn: "Id", NameColumn: "Shown", View: true, TenantColumn: ""}`,
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
		{
			name:     "a tenant's record that points at the whole application's accounts and groups",
			structs:  []string{"Base", "CrewBaseUser", "CrewBaseGroup", "CrewSessionUser", "CrewGroup", "Chore"},
			resource: "Chore",
			want: []string{
				`{Field: "BaseName", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
				`{Field: "HolderName", Key: "HolderID", Resource: "CrewBaseUsers", Tag: "displayName", Table: "CrewBaseUsers", KeyColumn: "Id", NameColumn: "DisplayName", View: true, TenantColumn: "DomainId"}`,
				`{Field: "GroupName", Key: "GroupID", Resource: "CrewBaseGroups", Tag: "name", Table: "CrewBaseGroups", KeyColumn: "Id", NameColumn: "Name", View: true, TenantColumn: "DomainId"}`,
				`{Field: "AssigneeName", Key: "AssigneeID", Resource: "CrewSessionUsers", Tag: "username", Table: "CrewSessionUsers", KeyColumn: "Id", NameColumn: "Username", TenantColumn: ""}`,
				`{Field: "TeamName", Key: "TeamID", Resource: "CrewGroups", Tag: "name", Table: "CrewGroups", KeyColumn: "Id", NameColumn: "Name", TenantColumn: ""}`,
				`{Field: "PickedAssigneeName", Key: "PickedAssigneeID", Resource: "CrewSessionUsers", Tag: "username", Table: "CrewSessionUsers", KeyColumn: "Id", NameColumn: "Username", TenantColumn: ""}`,
				`{Field: "PickedTeamName", Key: "PickedTeamID", Resource: "CrewGroups", Tag: "name", Table: "CrewGroups", KeyColumn: "Id", NameColumn: "Name", TenantColumn: ""}`,
			},
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

// Test_recordedNames pins the names a resource's change records keep: one beside every
// field that points at a row with a name, found as a name field is, but for a resource
// listed across tenants, which is described where it has no name field, a field no name
// field could stand beside, which is described, and a field that picks from a view or a
// computed list, which is read from the table the row is really in: one of the library's
// lists of a tenant's people by the account's username, one of its groups by the group's
// name, any other by the column's foreign key, and none with no foreign key; and the
// RecordedNames method the generated set renders from them.
func Test_recordedNames(t *testing.T) {
	t.Parallel()

	// crew is an auth over the tenant record Base whose tables hold accounts and groups;
	// lone is one that keeps no groups.
	crew := &identityInstance{
		auth: "Crew", tenant: &identityTenant{name: "Base"},
		session: session.Tables{Users: "CrewSessionUsers"}, policy: access.Tables{Groups: "CrewGroups"},
		tables: map[identityTable]bool{identityUsers: true, identityGroups: true},
	}
	lone := &identityInstance{
		auth: "Crew", tenant: &identityTenant{name: "Base"},
		session: session.Tables{Users: "CrewSessionUsers"},
		tables:  map[identityTable]bool{identityUsers: true},
	}
	holder := `{Field: "", Key: "HolderID", Resource: "CrewBaseUsers", Tag: "username", Table: "CrewSessionUsers", KeyColumn: "Id", NameColumn: "Username", TenantColumn: "", GrantInTenant: true}`
	base := `{Field: "", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`
	// account and group are the names kept beside key of one of the whole application's
	// accounts and groups, read from the users table and the groups table by the id alone.
	account := func(key string) string {
		return `{Field: "", Key: "` + key + `", Resource: "CrewSessionUsers", Tag: "username", Table: "CrewSessionUsers", KeyColumn: "Id", NameColumn: "Username", TenantColumn: ""}`
	}
	group := func(key string) string {
		return `{Field: "", Key: "` + key + `", Resource: "CrewGroups", Tag: "name", Table: "CrewGroups", KeyColumn: "Id", NameColumn: "Name", TenantColumn: ""}`
	}

	tests := []struct {
		name     string
		structs  []string
		resource string
		// lists is the instance the views CrewBaseUser and CrewBaseGroup are marked with,
		// as the generator marks the library's own lists; nil leaves them an
		// application's views.
		lists *identityInstance
		want  []string
	}{
		{
			name:     "one of a tenant's people by the account's username, a group by its name whether or not its column has a foreign key, and none for an input-only field",
			structs:  []string{"Base", "CrewBaseUser", "CrewBaseGroup", "Pass"},
			resource: "Pass",
			lists:    crew,
			want: []string{
				base,
				holder,
				`{Field: "", Key: "GroupID", Resource: "CrewBaseGroups", Tag: "name", Table: "CrewGroups", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "DomainId"}`,
				`{Field: "", Key: "LooseGroupID", Resource: "CrewBaseGroups", Tag: "name", Table: "CrewGroups", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "DomainId"}`,
			},
		},
		{
			name:     "the same lists as an application's views, on columns with no foreign key or one into a table no resource declares: none",
			structs:  []string{"Base", "CrewBaseUser", "CrewBaseGroup", "Pass"},
			resource: "Pass",
			want:     []string{base},
		},
		{
			name:     "an instance that keeps no groups: none for a group",
			structs:  []string{"Base", "CrewBaseUser", "CrewBaseGroup", "Pass"},
			resource: "Pass",
			lists:    lone,
			want:     []string{base, holder},
		},
		{
			name:     "a resource of no tenant: none for either list",
			structs:  []string{"Base", "CrewBaseUser", "CrewBaseGroup", "Roll"},
			resource: "Roll",
			lists:    crew,
		},
		{
			name:     "each kind of row a tenant's resource points at, but a view, and none for a resource with no name",
			structs:  []string{"Base", "Wing", "Kit", "Crate", "Member", "Permit"},
			resource: "Permit",
			want: []string{
				`{Field: "", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
				`{Field: "", Key: "WingID", Resource: "Wings", Tag: "callSign", Table: "Wings", KeyColumn: "Id", NameColumn: "CallSign", TenantColumn: "BaseId"}`,
				`{Field: "", Key: "LeadWing", Resource: "Wings", Tag: "callSign", Table: "Wings", KeyColumn: "Id", NameColumn: "CallSign", TenantColumn: "BaseId"}`,
				`{Field: "", Key: "KitID", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""}`,
				`{Field: "", Key: "Grade", Resource: "", Tag: "", Table: "Grades", KeyColumn: "Id", NameColumn: "Description", TenantColumn: ""}`,
			},
		},
		{
			name:     "a resource listed across tenants is described where it has no name field",
			structs:  []string{"Base", "Wing", "Kit", "Ledger"},
			resource: "Ledger",
			want: []string{
				`{Field: "", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
				`{Field: "", Key: "WingID", Resource: "Wings", Tag: "callSign", Table: "Wings", KeyColumn: "Id", NameColumn: "CallSign", TenantColumn: "BaseId"}`,
				`{Field: "", Key: "KitID", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""}`,
				`{Field: "", Key: "Grade", Resource: "", Tag: "", Table: "Grades", KeyColumn: "Id", NameColumn: "Description", TenantColumn: ""}`,
			},
		},
		{
			name:     "two fields no name field could stand beside are each described",
			structs:  []string{"Base", "Wing", "Pair"},
			resource: "Pair",
			want: []string{
				`{Field: "", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
				`{Field: "", Key: "WingID", Resource: "Wings", Tag: "callSign", Table: "Wings", KeyColumn: "Id", NameColumn: "CallSign", TenantColumn: "BaseId"}`,
				`{Field: "", Key: "Wing", Resource: "Wings", Tag: "callSign", Table: "Wings", KeyColumn: "Id", NameColumn: "CallSign", TenantColumn: "BaseId"}`,
			},
		},
		{
			name:     "a row reached through another table is held to the tenant by that way",
			structs:  []string{"Base", "Wing", "Bay", "Berth"},
			resource: "Berth",
			want: []string{
				`{Field: "", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
				`{Field: "", Key: "BayID", Resource: "Bays", Tag: "code", Table: "Bays", KeyColumn: "Id", NameColumn: "Code", TenantColumn: "WingId", TenantPath: []resource.BindingHop{{Table: "Wings", JoinColumn: "Id", Column: "BaseId"}}}`,
			},
		},
		{
			name:     "an application's view on a column with no foreign key: none",
			structs:  []string{"Base", "Hangar", "Beacon", "Patrol", "Manifest"},
			resource: "Manifest",
			want: []string{
				`{Field: "", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
			},
		},
		{
			name:     "an input-only field: none",
			structs:  []string{"Base", "Kit", "Waybill"},
			resource: "Waybill",
			want: []string{
				`{Field: "", Key: "BaseID", Resource: "Bases", Tag: "title", Table: "Bases", KeyColumn: "Id", NameColumn: "Title", TenantColumn: ""}`,
			},
		},
		{
			name:     "a tenant's row pointed at from a resource of no tenant: none",
			structs:  []string{"Base", "Wing", "Kit", "Stock"},
			resource: "Stock",
			want:     []string{`{Field: "", Key: "KitID", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""}`},
		},
		{
			name:     "a view keeps no change records",
			structs:  []string{"Kit", "Roster"},
			resource: "Roster",
		},
		{
			name:     "a tenant's record that points at the whole application's accounts and groups, by a foreign key or by its picker, and at a tenant's lists over the same foreign keys",
			structs:  []string{"Base", "CrewBaseUser", "CrewBaseGroup", "CrewSessionUser", "CrewGroup", "Chore"},
			resource: "Chore",
			lists:    crew,
			want: []string{
				base,
				`{Field: "", Key: "HolderID", Resource: "CrewBaseUsers", Tag: "username", Table: "CrewSessionUsers", KeyColumn: "Id", NameColumn: "Username", TenantColumn: "", GrantInTenant: true}`,
				`{Field: "", Key: "GroupID", Resource: "CrewBaseGroups", Tag: "name", Table: "CrewGroups", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "DomainId"}`,
				account("AssigneeID"),
				group("TeamID"),
				account("PickedAssigneeID"),
				group("PickedTeamID"),
			},
		},
		{
			name:     "the same record where the two lists are an application's own views: each by its foreign key's table",
			structs:  []string{"Base", "CrewBaseUser", "CrewBaseGroup", "CrewSessionUser", "CrewGroup", "Chore"},
			resource: "Chore",
			want: []string{
				base,
				account("HolderID"),
				group("GroupID"),
				account("AssigneeID"),
				group("TeamID"),
				account("PickedAssigneeID"),
				group("PickedTeamID"),
			},
		},
		{
			name:     "a record of no tenant",
			structs:  []string{"Base", "CrewBaseUser", "CrewBaseGroup", "CrewSessionUser", "CrewGroup", "Errand"},
			resource: "Errand",
			lists:    crew,
			want:     []string{account("HolderID"), group("GroupID"), account("AssigneeID"), group("TeamID")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// A name field the struct cannot take is refused (Pair), which leaves the
			// names its change records keep to be described all the same.
			c, _ := nameFixtureResources(t, tt.structs...)
			markLists(c, tt.lists)
			c.resolveRecordedNames(c.resources)
			var got []string
			for _, res := range c.resources {
				if res.Name() != tt.resource {
					continue
				}
				for _, name := range res.RecordedNames {
					got = append(got, name.GoLiteral())
				}
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("recorded names of %s mismatch (-want +got):\n%s", tt.resource, diff)
			}
		})
	}

	t.Run("a field that picks from a view or a computed list on a column with a foreign key is described as the same field with no picker", func(t *testing.T) {
		t.Parallel()

		c, err := nameFixtureResources(t, "Base", "Wing", "Kit", "Crate", "Hangar", "Beacon", "Patrol", "Route")
		if err != nil {
			t.Fatalf("nameFixtureResources() error = %v", err)
		}
		byName := make(map[string]*resourceInfo, len(c.resources))
		for _, res := range c.resources {
			byName[c.pluralize(res.Name())] = res
		}
		route := byName["Routes"]
		described := func(field *resourceField) string {
			t.Helper()

			name := c.recordedNameOf(route, field, byName)
			if name == nil {
				return "none"
			}
			name.Key = "<the field>"

			return name.GoLiteral()
		}
		// A computed list is no resource of the run: the field's declared picker is
		// named so, as the generator holds a computed resource's name.
		computed := func(field string) *resourceField {
			picked := *route.fieldNamed(field)
			picked.declaredResource = "Tallies"

			return &picked
		}
		for _, tt := range []struct {
			picked *resourceField
			plain  string
			want   string
		}{
			{picked: route.fieldNamed("PickedKitID"), plain: "KitID", want: `{Field: "", Key: "<the field>", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""}`},
			{picked: route.fieldNamed("PatrolKitID"), plain: "KitID", want: `{Field: "", Key: "<the field>", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""}`},
			{picked: route.fieldNamed("PickedWingID"), plain: "WingID", want: `{Field: "", Key: "<the field>", Resource: "Wings", Tag: "callSign", Table: "Wings", KeyColumn: "Id", NameColumn: "CallSign", TenantColumn: "BaseId"}`},
			{picked: route.fieldNamed("PickedCrateID"), plain: "CrateID", want: "none"},
			{picked: computed("PickedKitID"), plain: "KitID", want: `{Field: "", Key: "<the field>", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""}`},
		} {
			if got, plain := described(tt.picked), described(route.fieldNamed(tt.plain)); got != tt.want || plain != tt.want {
				t.Errorf("%s (picking from %s) is described %s, and %s with no picker %s; want both %s", tt.picked.Name(), tt.picked.EnumeratedResource(), got, tt.plain, plain, tt.want)
			}
		}

		c, err = nameFixtureResources(t, "Base", "CrewBaseUser", "CrewBaseGroup", "Pass")
		if err != nil {
			t.Fatalf("nameFixtureResources() error = %v", err)
		}
		for _, res := range c.resources {
			byName[c.pluralize(res.Name())] = res
		}
		loose := *byName["Passes"].fieldNamed("LooseGroupID")
		loose.declaredResource = "Tallies"
		if name := c.recordedNameOf(byName["Passes"], &loose, byName); name != nil {
			t.Errorf("a computed list on a column with no foreign key is described %s, want none", name.GoLiteral())
		}
	})

	t.Run("the generated set says the grant on a person's username is asked in the tenant, and on no other entry", func(t *testing.T) {
		t.Parallel()

		c, err := nameFixtureResources(t, "Base", "CrewBaseUser", "CrewBaseGroup", "Pass")
		if err != nil {
			t.Fatalf("nameFixtureResources() error = %v", err)
		}
		markLists(c, crew)
		c.resolveRecordedNames(c.resources)
		for _, res := range c.resources {
			if res.Name() != "Pass" {
				continue
			}
			out, err := c.generateTemplateOutput("resourceFile", resourceFileTemplate, &resourceFileData{Source: "fixture", Package: "resources", Resource: res})
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			formatted, err := format.Source(out)
			if err != nil {
				t.Fatalf("format.Source() error = %v on:\n%s", err, out)
			}
			_, method, found := strings.Cut(string(formatted), "func (Pass) RecordedNames()")
			method, _, closed := strings.Cut(method, "\n}\n")
			if !found || !closed {
				t.Fatalf("Pass's resource file declares no whole RecordedNames():\n%s", formatted)
			}
			var inTenant []string
			for line := range strings.SplitSeq(method, "\n") {
				if strings.Contains(line, "GrantInTenant") {
					inTenant = append(inTenant, strings.TrimSpace(line))
				}
			}
			if diff := cmp.Diff([]string{holder + ","}, inTenant); diff != "" {
				t.Errorf("the entries of RecordedNames() that say GrantInTenant mismatch (-want +got):\n%s\nin\n%s", diff, method)
			}
		}
	})

	t.Run("the generated set names them, and a resource with none declares no method", func(t *testing.T) {
		t.Parallel()

		c, err := nameFixtureResources(t, "Base", "Kit", "Waybill", "Stock")
		if err != nil {
			t.Fatalf("nameFixtureResources() error = %v", err)
		}
		c.resolveRecordedNames(c.resources)
		for _, res := range c.resources {
			out, err := c.generateTemplateOutput("resourceFile", resourceFileTemplate, &resourceFileData{Source: "fixture", Package: "resources", Resource: res})
			if err != nil {
				t.Fatalf("generateTemplateOutput(%s) error = %v", res.Name(), err)
			}
			formatted, err := format.Source(out)
			if err != nil {
				t.Fatalf("format.Source() error = %v on:\n%s", err, out)
			}
			switch res.Name() {
			case "Stock":
				want := "func (Stock) RecordedNames() []resource.NameField {\n\treturn []resource.NameField{\n\t\t" +
					`{Field: "", Key: "KitID", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""}` + ",\n\t}\n}"
				if !strings.Contains(string(formatted), want) {
					t.Errorf("Stock's resource file: want\n%s\nin\n%s", want, formatted)
				}
			case "Kit":
				if strings.Contains(string(formatted), "RecordedNames") {
					t.Errorf("Kit points at no row and declares RecordedNames:\n%s", formatted)
				}
			}
		}
	})
}

// markLists marks the fixture's views shaped as the library's lists of a tenant's people
// and groups with inst, as the generator marks the lists of an auth's management; nil
// marks nothing.
func markLists(c *client, inst *identityInstance) {
	if inst == nil {
		return
	}
	for _, res := range c.resources {
		if res.Name() == "CrewBaseUser" || res.Name() == "CrewBaseGroup" {
			res.identity = inst
		}
	}
}

// Test_nameFieldFeatures pins the flags a name stands behind: the pointed-to
// resource's, then its name field's, each only where it is declared, and the
// literal the generated NameFields method writes, which names them only where there
// are any.
func Test_nameFieldFeatures(t *testing.T) {
	t.Parallel()

	resourceGate := &featureGate{Constant: "Kits", Name: "kits"}
	fieldGate := &featureGate{Constant: "KitLabels", Name: "kit_labels"}
	const literal = `{Field: "KitName", Key: "KitID", Resource: "Kits", Tag: "label", Table: "Kits", KeyColumn: "Id", NameColumn: "Label", TenantColumn: ""`
	tests := []struct {
		name        string
		resource    *featureGate
		field       *featureGate
		want        []resource.Feature
		wantLiteral string
	}{
		{name: "neither gated: no flags, today's literal", wantLiteral: literal + "}"},
		{name: "the name field gated", field: fieldGate, want: []resource.Feature{"kit_labels"}, wantLiteral: literal + `, Features: []resource.Feature{"kit_labels"}}`},
		{name: "the pointed-to resource gated", resource: resourceGate, want: []resource.Feature{"kits"}, wantLiteral: literal + `, Features: []resource.Feature{"kits"}}`},
		{name: "both gated: the resource's first", resource: resourceGate, field: fieldGate, want: []resource.Feature{"kits", "kit_labels"}, wantLiteral: literal + `, Features: []resource.Feature{"kits", "kit_labels"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := nameFixtureResources(t, "Base", "Wing", "Kit", "Stock")
			if err != nil {
				t.Fatalf("nameFixtureResources() error = %v", err)
			}
			var stock *resourceInfo
			for _, res := range c.resources {
				switch res.Name() {
				case "Kit":
					res.Feature = tt.resource
					for _, field := range res.Fields {
						if field.Name() == "Label" {
							field.Feature = tt.field
						}
					}
				case "Stock":
					stock = res
				}
			}
			if err := c.resolveNameFields(c.resources); err != nil {
				t.Fatalf("resolveNameFields() error = %v", err)
			}
			if len(stock.NameFields) != 1 {
				t.Fatalf("Stock's name fields = %d, want the kit's alone", len(stock.NameFields))
			}
			if diff := cmp.Diff(tt.want, stock.NameFields[0].Features); diff != "" {
				t.Errorf("Features mismatch (-want +got):\n%s", diff)
			}
			if got := stock.NameFields[0].GoLiteral(); got != tt.wantLiteral {
				t.Errorf("GoLiteral() =\n%s\nwant\n%s", got, tt.wantLiteral)
			}
		})
	}
}

// docketNameFields resolves the namefixture's Docket and returns its name fields by Go
// name, with the resource.
func docketNameFields(t *testing.T) (docket *resourceInfo, names map[string]*nameField) {
	t.Helper()

	c, err := nameFixtureResources(t, "Badge", "Seal", "Ward", "Kit", "Docket")
	if err != nil {
		t.Fatalf("nameFixtureResources() error = %v", err)
	}
	for _, res := range c.resources {
		if res.Name() != "Docket" {
			continue
		}
		names = make(map[string]*nameField, len(res.NameFields))
		for _, name := range res.NameFields {
			names[name.Name()] = name
		}

		return res, names
	}
	t.Fatal("the fixture resolved no Docket")

	return nil, nil
}

// Test_nameFieldFilters pins what a filter may do with each name field, in the list
// request types' tag and in the TypeScript metadata: a name counts as indexed where the id
// and the pointed-to name are both indexed (an enumeration table's always is), is
// filterable beside an indexed field where both are filterable otherwise, answers the
// pointed-to name's matches where the id is indexed, carries pii where the pointed-to
// name is PII, and says nothing where the id or the pointed-to name is not filterable.
func Test_nameFieldFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		field          string
		wantTag        string
		wantFilterable string
		wantSearch     string
	}{
		{
			name:           "an indexed id to an indexed name",
			field:          "BadgeName",
			wantTag:        `json:"badgeName" nameof:"BadgeID" index:"true"`,
			wantFilterable: "always",
		},
		{
			name:           "an indexed id to an enumeration table",
			field:          "GradeName",
			wantTag:        `json:"gradeName" nameof:"Grade" index:"true"`,
			wantFilterable: "always",
		},
		{
			name:           "an indexed id to a name with a search fact and no index",
			field:          "SealName",
			wantTag:        `json:"sealName" nameof:"SealID" allow_filter:"true" search:"token=Motto_Search;index=SealsSearch;role=text;contains"`,
			wantFilterable: "withIndexed",
			wantSearch:     "'contains'",
		},
		{
			name:           "an allow_filter id to a name with a search fact answers no match",
			field:          "SpareSealName",
			wantTag:        `json:"spareSealName" nameof:"SpareSealID" allow_filter:"true"`,
			wantFilterable: "withIndexed",
		},
		{
			name:    "an id that is not filterable",
			field:   "LooseBadgeName",
			wantTag: `json:"looseBadgeName" nameof:"LooseBadgeID"`,
		},
		{
			name:    "a name that is not filterable where it lives",
			field:   "KitName",
			wantTag: `json:"kitName" nameof:"KitID"`,
		},
		{
			name:           "a PII name",
			field:          "WardName",
			wantTag:        `json:"wardName" nameof:"WardID" index:"true" pii:"true"`,
			wantFilterable: "always",
		},
	}

	_, names := docketNameFields(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			name := names[tt.field]
			if name == nil {
				t.Fatalf("Docket has no name field %s", tt.field)
			}
			if got := name.ListStructTag(); got != tt.wantTag {
				t.Errorf("ListStructTag() = %s, want %s", got, tt.wantTag)
			}
			if got := name.TypescriptFilterable(); got != tt.wantFilterable {
				t.Errorf("TypescriptFilterable() = %q, want %q", got, tt.wantFilterable)
			}
			if got := name.TypescriptSearch(); got != tt.wantSearch {
				t.Errorf("TypescriptSearch() = %q, want %q", got, tt.wantSearch)
			}
		})
	}
}

// Test_nameFieldFilterTemplates pins where the name's filter facts are written: the list's
// and the list across tenants' request types carry ListStructTag, the read's request type
// StructTag alone, and the metadata's nameFields entry its filterable, search and pii, in
// that order.
func Test_nameFieldFilterTemplates(t *testing.T) {
	t.Parallel()

	docket, _ := docketNameFields(t)
	// The fixture's resolution types no field for TypeScript, and the metadata draws
	// each field with its type.
	for _, f := range docket.Fields {
		f.typescriptType = "string"
	}
	listTag := "SealName *string `json:\"sealName\" nameof:\"SealID\" allow_filter:\"true\" search:\"token=Motto_Search;index=SealsSearch;role=text;contains\"`"
	readTag := "SealName *string `json:\"sealName\" nameof:\"SealID\"`"
	handlerData := handlerContentData{ResourcePackage: "resources", Resource: docket, VirtualResourcesPackage: "virtualresources", ApplicationName: "App", ReceiverName: "a"}

	tests := []struct {
		name            string
		template        string
		data            any
		wantContains    []string
		wantNotContains []string
	}{
		{name: "list", template: listTemplate, data: handlerData, wantContains: []string{listTag}},
		{name: "list across tenants", template: listAcrossTenantsTemplate, data: handlerData, wantContains: []string{listTag}},
		{name: "read", template: readTemplate, data: handlerData, wantContains: []string{readTag}, wantNotContains: []string{listTag}},
		{
			name:     "metadata",
			template: typescriptResourcesTemplate,
			data:     tsResourcesData{Resources: []*resourceInfo{docket}, GenPrefix: "zz_gen"},
			wantContains: []string{
				"{ fieldName: 'badgeName', of: 'badgeId', filterable: 'always' }",
				"{ fieldName: 'gradeName', of: 'grade', filterable: 'always' }",
				"{ fieldName: 'sealName', of: 'sealId', filterable: 'withIndexed', search: ['contains'] }",
				"{ fieldName: 'spareSealName', of: 'spareSealId', filterable: 'withIndexed' }",
				"{ fieldName: 'looseBadgeName', of: 'looseBadgeId' }",
				"{ fieldName: 'kitName', of: 'kitId' }",
				"{ fieldName: 'wardName', of: 'wardId', filterable: 'always', pii: true }",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			data := tt.data
			if ts, ok := data.(tsResourcesData); ok {
				ts.File = &typescriptGenerator{client: c}
				data = ts
			}
			out, err := c.generateTemplateOutput(tt.name, tt.template, data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			// A handler fragment carries the empty tags' spaces, so space runs collapse
			// before matching.
			normalized := strings.Join(strings.Fields(string(out)), " ")
			for _, want := range tt.wantContains {
				if !strings.Contains(normalized, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			for _, unwanted := range tt.wantNotContains {
				if strings.Contains(normalized, unwanted) {
					t.Errorf("output must not contain %q:\n%s", unwanted, out)
				}
			}
		})
	}
}

// Test_nameFieldFromView pins what a filter may do with a name read from a view: the
// list request types' tag follows the view's name field, index:"true" where it is
// indexed and allow_filter:"true" where it allows a filter, and carries the view's
// search fact where its name field says which table column it projects, as a name read
// from a table does; the metadata says the same.
func Test_nameFieldFromView(t *testing.T) {
	t.Parallel()

	c, err := nameFixtureResources(t, "Base", "Hangar", "Beacon", "Patrol", "Manifest")
	if err != nil {
		t.Fatalf("nameFixtureResources() error = %v", err)
	}
	names := make(map[string]*nameField)
	for _, res := range c.resources {
		if res.Name() != "Manifest" {
			continue
		}
		for _, name := range res.NameFields {
			names[name.Name()] = name
		}
	}
	if beacon := names["BeaconName"]; beacon != nil && beacon.source.SearchTag() == "" {
		t.Fatal("the fixture's Beacon must carry a search fact on its name for the case to say anything")
	}

	tests := []struct {
		name           string
		field          string
		wantTag        string
		wantFilterable string
		wantSearch     string
	}{
		{
			name:           "an indexed id to a view's name that allows a filter",
			field:          "HangarName",
			wantTag:        `json:"hangarName" nameof:"HangarID" allow_filter:"true"`,
			wantFilterable: "withIndexed",
		},
		{
			name:           "an indexed id to a view's indexed name that carries a table's search fact",
			field:          "BeaconName",
			wantTag:        `json:"beaconName" nameof:"BeaconID" index:"true" search:"token=Motto_Search;index=SealsSearch;role=text;contains"`,
			wantFilterable: "always",
			wantSearch:     "'contains'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			name := names[tt.field]
			if name == nil {
				t.Fatalf("Manifest has no name field %s", tt.field)
			}
			if got := name.ListStructTag(); got != tt.wantTag {
				t.Errorf("ListStructTag() = %s, want %s", got, tt.wantTag)
			}
			if got := name.TypescriptFilterable(); got != tt.wantFilterable {
				t.Errorf("TypescriptFilterable() = %q, want %q", got, tt.wantFilterable)
			}
			if got := name.TypescriptSearch(); got != tt.wantSearch {
				t.Errorf("TypescriptSearch() = %q, want %q", got, tt.wantSearch)
			}
		})
	}
}

// Test_nameFieldOf_viewKeyedByTwoColumns pins that an id pointing into a view keyed by
// two columns is given no name field: no one id names one of its rows. A field-scope
// @enumerate into such a view is refused before the name fields resolve, so the id is
// pointed at it here directly.
func Test_nameFieldOf_viewKeyedByTwoColumns(t *testing.T) {
	t.Parallel()

	c, err := nameFixtureResources(t, "Base", "Hangar", "Beacon", "Patrol", "Convoy", "Manifest")
	if err != nil {
		t.Fatalf("nameFixtureResources() error = %v", err)
	}
	byName := make(map[string]*resourceInfo, len(c.resources))
	for _, res := range c.resources {
		byName[c.pluralize(res.Name())] = res
	}
	manifest := byName["Manifests"]
	pointing := *manifest.fieldNamed("PatrolID")
	pointing.declaredResource = "Convoys"
	if name := c.nameFieldOf(manifest, &pointing, byName); name != nil {
		t.Errorf("nameFieldOf() = %s, want none for a view keyed by two columns", name.GoLiteral())
	}
}

// Test_packageNameViews pins the name views: the views that declare what their rows are
// known by, by resource, in the application's virtual package first and then in each
// auth's handlers package, by the auth's name, an auth's view named through the auth's
// declarations package; a view with no name is in none. Each home's decoders are handed
// theirs: the application's the merged value, an auth's its package's, or none where the
// package declares none. The generated file declares a package's.
func Test_packageNameViews(t *testing.T) {
	t.Parallel()

	c, err := nameFixtureResources(t, "Base", "Hangar", "Beacon", "Patrol", "Member")
	if err != nil {
		t.Fatalf("nameFixtureResources() error = %v", err)
	}
	crew := &identityInstance{auth: "Crew", importPath: crewAuthPath, resources: "pkg/identity/internal/crewresources", handlers: "pkg/identity/crewhandlers"}
	members := &identityInstance{auth: "Members", importPath: membersAuthPath, resources: "pkg/identity/internal/membersresources", handlers: "pkg/identity/membershandlers"}
	pilots := &identityInstance{auth: "Pilots", resources: "pkg/identity/internal/pilotsresources", handlers: "pkg/identity/pilotshandlers"}
	for _, res := range c.resources {
		switch res.Name() {
		case "Hangar":
			res.identity = crew
		case "Member":
			res.identity = members
		}
	}
	c.virtual = "pkg/virtualresources"
	r := &resourceGenerator{client: c, handler: "app"}

	got := r.nameViewsByPackage()
	want := []nameViewsPackage{
		{dir: "pkg/virtualresources", Views: []nameViewData{{Resource: "Beacons", Type: "Beacon"}}},
		{dir: "pkg/identity/crewhandlers", instance: crew, Views: []nameViewData{{Resource: "Hangars", Type: "crewresources.Hangar"}}},
		{dir: "pkg/identity/membershandlers", instance: members, Views: []nameViewData{{Resource: "Members", Type: "membersresources.Member"}}},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(nameViewsPackage{}, identityInstance{})); diff != "" {
		t.Errorf("nameViewsByPackage() mismatch (-want +got):\n%s", diff)
	}

	for _, tt := range []struct {
		inst *identityInstance
		want string
	}{{nil, "nameViews"}, {crew, "NameViews"}, {pilots, "nil"}} {
		if got := r.nameViewsOf(r.homeOf(tt.inst)); got != tt.want {
			t.Errorf("nameViewsOf(%v) = %s, want %s", tt.inst, got, tt.want)
		}
	}

	out := renderFormatted(t, "nameViewsPackageTemplate", nameViewsPackageTemplate, &nameViewsPackageData{Source: `Auth("` + crewAuthPath + `")`, Package: "crewhandlers", Views: want[1].Views})
	for _, wantText := range []string{
		"package crewhandlers",
		"// NameViews are the views that declare what their rows are known by (@name), by\n// resource: this package's own, or, in an auth's handlers package, those of the auth's\n// generated management. A list that points into one reads the name from the view's\n// query, which its decoder is handed.",
		"var NameViews = resource.NameViews{\n\t\"Hangars\": crewresources.Hangar{},\n}",
	} {
		if !strings.Contains(out, wantText) {
			t.Errorf("output lacks %q:\n%s", wantText, out)
		}
	}
}

// Test_mergedNameViews pins the handler package's name views: every package's NameViews
// merged into the one value the generated query decoders are handed, the application's
// first; with none, the merge of nothing, still handed over.
func Test_mergedNameViews(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data *nameViewsAssemblyData
		want string
	}{
		{
			name: "no package declares a name view",
			data: &nameViewsAssemblyData{Source: "pkg/resources", Package: "app"},
			want: "var nameViews = resource.MergeNameViews()",
		},
		{
			name: "the application's and an auth's",
			data: &nameViewsAssemblyData{Source: "pkg/resources", Package: "app", Values: []string{"virtualresources.NameViews", "crewhandlers.NameViews"}},
			want: "var nameViews = resource.MergeNameViews(\n\tvirtualresources.NameViews,\n\tcrewhandlers.NameViews,\n)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out := renderFormatted(t, "nameViewsTemplate", nameViewsTemplate, tt.data)
			comment := "// nameViews are the application's name views: the NameViews of the virtual resources\n// package and of every auth's generated management, merged into the one value the\n// generated query decoders are handed."
			for _, want := range []string{"package app", comment, tt.want} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
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
