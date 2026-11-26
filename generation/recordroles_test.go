package generation

import (
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// recordRolesTables is the schema of the recordrolesfixture package: each table's key
// and the foreign keys and interleaves that put a row under a project, or name one.
func recordRolesTables() map[string]*tableMetadata {
	text := columnMeta{IsPrimaryKey: true, IsIndex: true, IsUniqueIndex: true, SpannerType: "STRING(36)"}
	key := func(ordinal int64, spannerType string) columnMeta {
		return columnMeta{IsPrimaryKey: true, IsIndex: true, OrdinalPosition: ordinal, KeyOrdinalPosition: ordinal, SpannerType: spannerType}
	}
	plain := columnMeta{SpannerType: "STRING(36)"}
	foreignKey := func(table string) columnMeta {
		return columnMeta{IsForeignKey: true, ReferencedTable: table, ReferencedColumn: "Id", SpannerType: "STRING(36)"}
	}
	to := func(column, table string) foreignKeyMeta {
		return foreignKeyMeta{Name: "FK_" + column, Columns: []string{column}, ReferencedTable: table, ReferencedColumns: []string{"Id"}, DeleteRule: "NO ACTION"}
	}

	return map[string]*tableMetadata{
		"Realms": {
			PkCount: 1, ForeignKeys: []foreignKeyMeta{to("FeaturedProjectId", "Projects")},
			Columns: map[string]columnMeta{"Id": text, "Name": plain, "FeaturedProjectId": foreignKey("Projects"), "Disabled": {SpannerType: "BOOL"}},
		},
		"Currencies": {PkCount: 1, Columns: map[string]columnMeta{"Id": text, "Name": plain}},
		"Clients":    {PkCount: 1, Columns: map[string]columnMeta{"Id": text, "RealmId": plain, "Name": plain}},
		"Projects": {
			PkCount: 1, ForeignKeys: []foreignKeyMeta{to("ClientId", "Clients"), to("CurrencyId", "Currencies")},
			Columns: map[string]columnMeta{"Id": text, "RealmId": plain, "ClientId": foreignKey("Clients"), "CurrencyId": foreignKey("Currencies")},
		},
		"Tasks": {
			PkCount: 1, ForeignKeys: []foreignKeyMeta{to("ProjectId", "Projects"), to("FormerProjectId", "Projects")},
			Columns: map[string]columnMeta{"Id": text, "RealmId": plain, "ProjectId": foreignKey("Projects"), "FormerProjectId": foreignKey("Projects")},
		},
		"Comments": {
			PkCount: 1, ForeignKeys: []foreignKeyMeta{to("TaskId", "Tasks")},
			Columns: map[string]columnMeta{"Id": text, "RealmId": plain, "TaskId": foreignKey("Tasks")},
		},
		"ProjectNotes": {PkCount: 2, IsInterleaved: true, ParentTable: "Projects", Columns: map[string]columnMeta{
			"Id": key(0, "STRING(36)"), "Sequence": key(1, "INT64"), "RealmId": plain,
		}},
		"NoteReplies": {PkCount: 3, IsInterleaved: true, ParentTable: "ProjectNotes", Columns: map[string]columnMeta{
			"Id": key(0, "STRING(36)"), "Sequence": key(1, "INT64"), "Reply": key(2, "INT64"), "RealmId": plain,
		}},
		"Invoices": {
			PkCount: 1, ForeignKeys: []foreignKeyMeta{to("ClientId", "Clients")},
			Columns: map[string]columnMeta{"Id": text, "RealmId": plain, "ClientId": foreignKey("Clients")},
		},
		"Settings": {
			PkCount: 1, ForeignKeys: []foreignKeyMeta{to("RealmId", "Realms")},
			Columns: map[string]columnMeta{"Id": text, "RealmId": foreignKey("Realms")},
		},
		"StaffRecordUserRoles":  {PkCount: 6, Columns: map[string]columnMeta{}},
		"StaffRecordGroupRoles": {PkCount: 6, Columns: map[string]columnMeta{}},
	}
}

// recordRolesResources extracts the fixture's table-backed resources over tables, every
// cross-resource pass run, and answers them by name. The marks are the fixture's unless
// marked names others, in which case those are marked alone and read again.
func recordRolesResources(t *testing.T, tables map[string]*tableMetadata, marked ...string) (*client, map[string]*resourceInfo, error) {
	t.Helper()

	c := &client{tableMap: tables}
	resources, err := c.structsToResources(loadFixture(t, "recordrolesfixture").Structs)
	if err != nil {
		return nil, nil, err
	}
	byName := make(map[string]*resourceInfo, len(resources))
	for _, res := range resources {
		byName[res.Name()] = res
	}
	if len(marked) > 0 {
		for _, res := range resources {
			res.holdsRoles = false
			res.heldRecord, res.recordWays, res.recordAttributes = nil, nil, nil
		}
		for _, name := range marked {
			byName[name].holdsRoles = true
		}
		err = c.resolveRecordRoles(resources)
	}

	return c, byName, err
}

// Test_resolveRecordRoles pins what the generator reads for a record that holds roles:
// the ways down to each tenant's table under it, each an attribute that follows the key
// of the record down the way; the record's own way; the rows it names; and what it does
// not reach: a table apart, the tenant record, a table under the record only through the
// tenant record, and a table that is no tenant's.
func Test_resolveRecordRoles(t *testing.T) {
	t.Parallel()

	_, byName, err := recordRolesResources(t, recordRolesTables())
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}
	way := func(n int, column string, path ...resource.BindingHop) resource.AttributeData {
		return resource.AttributeData{Name: resource.RecordWayAttribute("Projects", n), Column: column, Type: resource.AttributeTypeString, Path: path}
	}
	ways := func(n int) []resource.RecordWayData {
		var under []resource.RecordWayData
		for i := range n {
			under = append(under, resource.RecordWayData{Record: "Projects", Attribute: resource.RecordWayAttribute("Projects", i)})
		}

		return under
	}
	tests := []struct {
		name       string
		resource   string
		attributes []resource.AttributeData
		ways       []resource.RecordWayData
	}{
		{
			name: "the record's own way, on its key", resource: "Project",
			attributes: []resource.AttributeData{way(0, "Id")},
			ways:       []resource.RecordWayData{{Record: "Projects", Attribute: resource.RecordWayAttribute("Projects", 0), Self: true}},
		},
		{
			name: "a row one step under, by two ways", resource: "Task",
			attributes: []resource.AttributeData{way(0, "FormerProjectId"), way(1, "ProjectId")},
			ways:       ways(2),
		},
		{
			name: "a row two steps under, each way one hop through the row above", resource: "Comment",
			attributes: []resource.AttributeData{
				way(0, "TaskId", resource.BindingHop{Table: "Tasks", JoinColumn: "Id", Column: "FormerProjectId"}),
				way(1, "TaskId", resource.BindingHop{Table: "Tasks", JoinColumn: "Id", Column: "ProjectId"}),
			},
			ways: ways(2),
		},
		{name: "an interleaved table reads its own column", resource: "ProjectNote", attributes: []resource.AttributeData{way(0, "Id")}, ways: ways(1)},
		{name: "an interleaved table two levels down reads its own column with no hop", resource: "NoteReply", attributes: []resource.AttributeData{way(0, "Id")}, ways: ways(1)},
		{
			name: "a row the record names, by the column its key references", resource: "Client",
			attributes: []resource.AttributeData{{Name: resource.RecordNameAttribute("Projects", 0), Column: "Id", Type: resource.AttributeTypeString}},
		},
		{name: "a table apart", resource: "Invoice"},
		{name: "a table under the record only through the tenant record", resource: "Setting"},
		{name: "the tenant record", resource: "Realm"},
		{name: "a table that is no tenant's", resource: "Currency"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := byName[tt.resource]
			if diff := cmp.Diff(tt.attributes, res.recordAttributes, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s's attributes mismatch (-want +got):\n%s", tt.resource, diff)
			}
			if diff := cmp.Diff(tt.ways, res.recordWays, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s's ways mismatch (-want +got):\n%s", tt.resource, diff)
			}
			if res.Name() != "Project" && res.heldRecord != nil {
				t.Errorf("%s is marked as a record that holds roles", res.Name())
			}
		})
	}

	t.Run("the record's key, words and names", func(t *testing.T) {
		t.Parallel()

		want := &resource.RecordData{
			Key: "Id", One: "project", Many: "projects",
			Names: []resource.RecordNameData{{Resource: "Clients", Column: "ClientId", Attribute: resource.RecordNameAttribute("Projects", 0), One: "client", Many: "clients"}},
		}
		if diff := cmp.Diff(want, byName["Project"].heldRecord); diff != "" {
			t.Errorf("Project's record data mismatch (-want +got):\n%s", diff)
		}
	})
}

// Test_resolveRecordRoles_refusals pins each mark and each schema the generator refuses,
// word for word.
func Test_resolveRecordRoles_refusals(t *testing.T) {
	t.Parallel()

	twoColumns := func(table, column, to string) func(map[string]*tableMetadata) {
		return func(tables map[string]*tableMetadata) {
			meta := tables[table]
			for i, foreignKey := range meta.ForeignKeys {
				if foreignKey.Columns[0] == column {
					meta.ForeignKeys[i] = foreignKeyMeta{Name: "FK_" + column, Columns: []string{column, "RealmId"}, ReferencedTable: to, ReferencedColumns: []string{"Id", "RealmId"}, DeleteRule: "NO ACTION"}
				}
			}
		}
	}
	tests := []struct {
		name   string
		marked string
		schema func(map[string]*tableMetadata)
		want   string
	}{
		{
			name: "a resource that is not a tenant's", marked: "Currency",
			want: "struct Currency: a role held on one record is a tenant's role, and Currency is not a tenant's resource (@permissionScope(domain) and @domain). Take @holdsRoles off, or make the resource a tenant's.",
		},
		{
			name: "the tenant record", marked: "Realm",
			want: "struct Realm: Realm is the tenant record, and a role held on it is the role held in the tenant. Take @holdsRoles off.",
		},
		{
			name: "a table keyed by more than one column", marked: "ProjectNote",
			want: "struct ProjectNote: a record that holds roles is keyed by one text column, and ProjectNotes is keyed by (Id STRING(36), Sequence INT64). Take @holdsRoles off, or key the table by one id column.",
		},
		{
			name: "a way down through a foreign key of two columns", marked: "Project", schema: twoColumns("Comments", "TaskId", "Tasks"),
			want: "struct Project: Comments is under Projects by Comments.TaskId+RealmId to Tasks.Id+RealmId, then Tasks.FormerProjectId to Projects.Id, and that foreign key matches 2 columns at once, " +
				"where a role held on one project is followed one column at a time. Name the row above by its one key column, or take @holdsRoles off Project.",
		},
		{
			name: "a name by a foreign key of two columns", marked: "Project", schema: twoColumns("Projects", "ClientId", "Clients"),
			want: "struct Project: Projects names Clients by Projects.ClientId+RealmId to Clients.Id+RealmId, and that foreign key matches 2 columns at once, " +
				"where a role held on one project is followed one column at a time. Name the row above by its one key column, or take @holdsRoles off Project.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tables := recordRolesTables()
			if tt.schema != nil {
				tt.schema(tables)
			}
			_, _, err := recordRolesResources(t, tables, tt.marked)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("resolveRecordRoles() error = %v, want one containing %q", err, tt.want)
			}
		})
	}

	t.Run("a struct with no table", func(t *testing.T) {
		t.Parallel()

		c := &client{tableMap: recordRolesTables()}
		structs := fixtureStructs(loadFixture(t, "recordrolesfixture"))
		_, err := c.structsToVirtualResources(tenantFixtureStructs(t, structs, "ProjectSummary"))
		want := "struct ProjectSummary: @holdsRoles marks a record a role can be held on, and ProjectSummary has no table of its own. Write it on a resource with a table."
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("structsToVirtualResources() error = %v, want one containing %q", err, want)
		}
	})

	t.Run("a binding name the library keeps", func(t *testing.T) {
		t.Parallel()

		want := "@attribute(held_projects): a name that starts with held_ is the library's own, for the roles held on one record. Write another name."
		if err := validateBindingName("held_projects", attributeKeyword); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("validateBindingName() error = %v, want one containing %q", err, want)
		}
	})
}

// recordRolesGenerator is a run over the fixture's resources and an auth staff that keeps
// groups, its policy tables named by the library's rule.
func recordRolesGenerator(t *testing.T, tables map[string]*tableMetadata) *resourceGenerator {
	t.Helper()

	c, byName, err := recordRolesResources(t, tables)
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}
	r := &resourceGenerator{client: c}
	for _, res := range byName {
		r.resources = append(r.resources, res)
	}
	sortResources(r.resources)
	policy, err := access.TablesWithPrefix("Staff", "")
	if err != nil {
		t.Fatalf("access.TablesWithPrefix() error = %v", err)
	}
	r.identity = []*identityInstance{{importPath: "example.com/app/pkg/auth/staff", auth: "Staff", policy: policy, groupSet: "staffGroups"}}

	return r
}

// Test_collectRecordRoleRegistrations pins what the collection says of the fixture: the
// marked record, the ways of each resource, the attributes beside the written ones, and
// where the auth keeps the roles held on records; the file the collection is written to
// carries them; and a schema without the auth's tables refuses the run.
func Test_collectRecordRoleRegistrations(t *testing.T) {
	t.Parallel()

	r := recordRolesGenerator(t, recordRolesTables())
	b := resource.NewCollectionBuilder()
	for _, res := range r.resources {
		if err := b.AddResource(scopeOrGlobal(res.PermissionScope), accesstypes.Read, accesstypes.Resource(r.pluralize(res.Name()))); err != nil {
			t.Fatalf("AddResource(%s) error = %v", res.Name(), err)
		}
	}
	r.collectBindingRegistrations(b)
	if err := r.collectRecordRoleRegistrations(b); err != nil {
		t.Fatalf("collectRecordRoleRegistrations() error = %v", err)
	}
	data := b.Data()

	held := map[accesstypes.Resource]*resource.RecordData{}
	under := map[accesstypes.Resource][]resource.RecordWayData{}
	attributes := map[accesstypes.Resource][]string{}
	for _, res := range data.Resources {
		if res.HoldsRoles != nil {
			held[res.Name] = res.HoldsRoles
		}
		if len(res.Under) > 0 {
			under[res.Name] = res.Under
		}
		for _, attr := range res.Attributes {
			if strings.HasPrefix(attr.Name, resource.ReservedBindingPrefix) {
				attributes[res.Name] = append(attributes[res.Name], attr.Name)
			}
		}
	}
	if diff := cmp.Diff([]accesstypes.Resource{"Projects"}, keysOf(held)); diff != "" {
		t.Errorf("the records that hold roles mismatch (-want +got):\n%s", diff)
	}
	wantUnder := map[accesstypes.Resource]int{"Comments": 2, "NoteReplies": 1, "ProjectNotes": 1, "Projects": 1, "Tasks": 2}
	for res, n := range wantUnder {
		if len(under[res]) != n {
			t.Errorf("%s has %d ways under a project, want %d: %v", res, len(under[res]), n, under[res])
		}
	}
	wantAttributes := map[accesstypes.Resource][]string{
		"Clients":      {"held_named0_Projects"},
		"Comments":     {"held_way0_Projects", "held_way1_Projects"},
		"NoteReplies":  {"held_way0_Projects"},
		"ProjectNotes": {"held_way0_Projects"},
		"Projects":     {"held_way0_Projects"},
		"Tasks":        {"held_way0_Projects", "held_way1_Projects"},
	}
	if diff := cmp.Diff(wantAttributes, attributes); diff != "" {
		t.Errorf("the generated attributes mismatch (-want +got):\n%s", diff)
	}
	wantStores := []resource.RecordRoleStoreData{{Auth: "staff", UserRoles: "StaffRecordUserRoles", GroupRoles: "StaffRecordGroupRoles", GroupSet: "staffGroups"}}
	if diff := cmp.Diff(wantStores, data.RecordRoleStores); diff != "" {
		t.Errorf("the stores mismatch (-want +got):\n%s", diff)
	}

	t.Run("the collection file carries them", func(t *testing.T) {
		t.Parallel()

		output, err := r.generateTemplateOutput("collectionTemplate", collectionTemplate, &collectionFileData{Source: "pkg/resources", Package: "router", Data: data})
		if err != nil {
			t.Fatalf("generateTemplateOutput() error = %v", err)
		}
		formatted, err := format.Source(output)
		if err != nil {
			t.Fatalf("the rendered collection file is not valid Go: %v\n%s", err, output)
		}
		normalized := strings.Join(strings.Fields(string(formatted)), " ")
		for _, want := range []string{
			`HoldsRoles: &resource.RecordData{Key: "Id", One: "project", Many: "projects", Names: []resource.RecordNameData{ {Resource: "Clients", Column: "ClientId", Attribute: "held_named0_Projects", One: "client", Many: "clients"}, }},`,
			`Under: []resource.RecordWayData{ {Record: "Projects", Attribute: "held_way0_Projects", Self: true}, },`,
			`Under: []resource.RecordWayData{ {Record: "Projects", Attribute: "held_way0_Projects"}, {Record: "Projects", Attribute: "held_way1_Projects"}, },`,
			`{Name: "held_way0_Projects", Column: "TaskId", Type: "string", Path: []resource.BindingHop{{Table: "Tasks", JoinColumn: "Id", Column: "FormerProjectId"}}},`,
			`RecordRoleStores: []resource.RecordRoleStoreData{ {Auth: "staff", UserRoles: "StaffRecordUserRoles", GroupRoles: "StaffRecordGroupRoles", GroupSet: "staffGroups"}, },`,
		} {
			if !strings.Contains(normalized, want) {
				t.Errorf("the rendered collection file is missing %q:\n%s", want, formatted)
			}
		}
	})

	t.Run("a schema without the auth's tables", func(t *testing.T) {
		t.Parallel()

		tables := recordRolesTables()
		delete(tables, "StaffRecordGroupRoles")
		r := recordRolesGenerator(t, tables)
		want := `struct Project: the roles held on one project are kept for Auth("example.com/app/pkg/auth/staff") in the table StaffRecordGroupRoles, and the schema holds none. Add the policy store's tables for the auth to the schema.`
		if err := r.collectRecordRoleRegistrations(resource.NewCollectionBuilder()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("collectRecordRoleRegistrations() error = %v, want one containing %q", err, want)
		}
	})
}

// keysOf is a map's keys, sorted.
func keysOf[V any](m map[accesstypes.Resource]V) []accesstypes.Resource {
	keys := make([]accesstypes.Resource, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	return keys
}

// Test_unmarkedHeldOnIn pins the role file's heldOn held to the records the run marks: a
// role held on the marked record passes, and one held on a resource the run does not mark
// is refused in the engine's words, with what to write.
func Test_unmarkedHeldOnIn(t *testing.T) {
	t.Parallel()

	r := recordRolesGenerator(t, recordRolesTables())
	write := func(t *testing.T, heldOn string) string {
		t.Helper()

		file := filepath.Join(t.TempDir(), identityRolesFile)
		content := `{"roles": {"domain": [{"name": "ProjectEditor", "description": "Edits a project.", "heldOn": ["` + heldOn + `"], "permissions": {"Read": [{"resource": "Projects"}]}}]}}`
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}

		return file
	}

	if err := r.unmarkedHeldOnIn("example.com/app/pkg/auth/staff", write(t, "Projects")); err != nil {
		t.Errorf("unmarkedHeldOnIn() of a role held on the marked record: error = %v", err)
	}
	file := write(t, "Invoices")
	want := filepath.ToSlash(file) + ": ProjectEditor is held on Invoices, and Invoices is not marked as a record that can hold a role. Write @holdsRoles on the Invoice struct, or take Invoices out of the role's heldOn."
	if err := r.unmarkedHeldOnIn("example.com/app/pkg/auth/staff", file); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("unmarkedHeldOnIn() error = %v, want one containing %q", err, want)
	}
}
