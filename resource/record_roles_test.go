package resource

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// recordRolesData is the data of an application that marks one record a role can be held
// on: Projects names a client, Tasks are under a project by two ways (the project a task
// is in and the one it came from), Comments by one (through their task), and Invoices
// stand apart. The auth staff keeps the roles held on records with groups.
func recordRolesData() CollectionData {
	domain := accesstypes.DomainPermissionScope
	read := []accesstypes.Permission{accesstypes.Create, accesstypes.Read}

	return CollectionData{
		Resources: []CollectionResource{
			{Name: "Clients", Scope: domain, Permissions: read},
			{
				Name: "Comments", Scope: domain, Permissions: read,
				Under: []RecordWayData{{Record: "Projects", Attribute: "commentProject"}},
			},
			{Name: "Invoices", Scope: domain, Permissions: read},
			{
				Name: "Projects", Scope: domain, Permissions: read,
				HoldsRoles: &RecordData{
					Key: "Id", One: "project", Many: "projects",
					Names: []RecordNameData{{Resource: "Clients", Column: "ClientId", Attribute: "clientOfProject", One: "client", Many: "clients"}},
				},
				Under: []RecordWayData{{Record: "Projects", Attribute: "projectItself", Self: true}},
			},
			{
				Name: "Tasks", Scope: domain, Permissions: read,
				Under: []RecordWayData{
					{Record: "Projects", Attribute: "taskProject"},
					{Record: "Projects", Attribute: "taskFormerProject"},
				},
			},
		},
		RecordRoleStores: []RecordRoleStoreData{
			{Auth: "staff", UserRoles: "StaffRecordUserRoles", GroupRoles: "StaffRecordGroupRoles", GroupSet: "groupsOfStaff"},
		},
	}
}

// TestGeneratedCollection_recordRoles pins the data of the records a role can be held on:
// the builder's setters write it, it survives the round trip through
// NewGeneratedCollection and Data() unchanged, and the collection answers which records
// hold roles, their words, and how each resource stands to the record.
func TestGeneratedCollection_recordRoles(t *testing.T) {
	t.Parallel()

	data := recordRolesData()
	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	t.Run("the data survives the round trip", func(t *testing.T) {
		t.Parallel()

		if diff := cmp.Diff(data, g.Data()); diff != "" {
			t.Errorf("the data does not survive the round trip (-want +got):\n%s", diff)
		}
	})

	t.Run("the builder's setters write the data", func(t *testing.T) {
		t.Parallel()

		b := NewCollectionBuilder()
		for _, res := range data.Resources {
			for _, perm := range res.Permissions {
				if err := b.AddResource(res.Scope, perm, res.Name); err != nil {
					t.Fatalf("AddResource(%s, %s) error = %v", perm, res.Name, err)
				}
			}
			b.SetResourceUnder(res.Scope, res.Name, res.Under)
		}
		domain := accesstypes.DomainPermissionScope
		b.SetResourceHoldsRoles(domain, "Invoices", &RecordData{Key: "Id", One: "invoice", Many: "invoices"})
		b.SetResourceHoldsRoles(domain, "Invoices", nil)
		b.SetResourceHoldsRoles(domain, "Projects", &RecordData{Key: "Id"})
		b.SetResourceHoldsRoles(domain, "Projects", data.Resources[3].HoldsRoles)
		b.AddRecordRoleStore(&RecordRoleStoreData{Auth: "staff", UserRoles: "Former"})
		b.AddRecordRoleStore(&data.RecordRoleStores[0])
		if diff := cmp.Diff(data, b.Data()); diff != "" {
			t.Errorf("CollectionBuilder.Data() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("the records that hold roles, and their words", func(t *testing.T) {
		t.Parallel()

		if diff := cmp.Diff([]accesstypes.Resource{"Projects"}, g.RecordsHoldingRoles()); diff != "" {
			t.Errorf("RecordsHoldingRoles() mismatch (-want +got):\n%s", diff)
		}
		words := map[accesstypes.Resource][2]string{
			"Projects": {"project", "projects"},
			"Clients":  {"client", "clients"},
			"Tasks":    {"", ""},
			"Invoices": {"", ""},
		}
		for res, want := range words {
			if one, many := g.RecordWords(res); one != want[0] || many != want[1] {
				t.Errorf("RecordWords(%s) = (%q, %q), want (%q, %q)", res, one, many, want[0], want[1])
			}
		}
	})

	t.Run("how each resource stands to the record", func(t *testing.T) {
		t.Parallel()

		reach := map[accesstypes.Resource][3]bool{
			"Projects": {true, false, false},
			"Tasks":    {false, true, false},
			"Comments": {false, true, false},
			"Clients":  {false, false, true},
			"Invoices": {false, false, false},
		}
		for res, want := range reach {
			if self, under, named := g.RecordReach("Projects", res); [3]bool{self, under, named} != want {
				t.Errorf("RecordReach(Projects, %s) = %v, want %v", res, [3]bool{self, under, named}, want)
			}
			if self, under, named := g.RecordReach("Clients", res); self || under || named {
				t.Errorf("RecordReach(Clients, %s) = (%v, %v, %v), want nothing: Clients is no record a role is held on", res, self, under, named)
			}
		}
	})

	t.Run("a nil collection marks no record", func(t *testing.T) {
		t.Parallel()

		var none *GeneratedCollection
		if got := none.RecordsHoldingRoles(); got != nil {
			t.Errorf("RecordsHoldingRoles() = %v, want nil", got)
		}
		if one, many := none.RecordWords("Projects"); one != "" || many != "" {
			t.Errorf("RecordWords(Projects) = (%q, %q), want nothing", one, many)
		}
		if self, under, named := none.RecordReach("Projects", "Tasks"); self || under || named {
			t.Errorf("RecordReach(Projects, Tasks) = (%v, %v, %v), want nothing", self, under, named)
		}
	})
}

// The fixture of the conditions a role held on a record counts under, on both databases
// (record_roles_database_test.go, record_roles_postgres_test.go): the auth staff keeps
// its people's and its groups' roles on records, Projects hold roles, Tasks are under a
// project by the project they are in and by the one they came from, Comments by their
// task's two, a project names its client, and Invoices stand apart. The requests are
// made in the tenant north at onRecordNow.
const (
	onRecordAuth  = "staff"
	onRecordNorth = "north"
	onRecordSouth = "south"
)

var (
	onRecordNow   = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	onRecordEnded = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

// onRecordConditionData is the collection data of the fixture, with each attribute a way or a
// name of a record reads, and the set of a person's groups as the generator declares it
// for an auth.
func onRecordConditionData() CollectionData {
	domain := accesstypes.DomainPermissionScope
	read := []accesstypes.Permission{accesstypes.Create, accesstypes.Read}
	tenancy := &DomainBindingData{Column: "DomainId"}
	way := func(n int, column string, path ...BindingHop) AttributeData {
		return AttributeData{Name: RecordWayAttribute("Projects", n), Column: column, Type: AttributeTypeString, Path: path}
	}
	ways := func(n int) []RecordWayData {
		var under []RecordWayData
		for i := range n {
			under = append(under, RecordWayData{Record: "Projects", Attribute: RecordWayAttribute("Projects", i)})
		}

		return under
	}

	return CollectionData{
		Resources: []CollectionResource{
			{
				Name: "Clients", Scope: domain, Permissions: read, Domain: tenancy,
				Attributes: []AttributeData{{Name: RecordNameAttribute("Projects", 0), Column: "Id", Type: AttributeTypeString}},
			},
			{
				Name: "Comments", Scope: domain, Permissions: read, Domain: tenancy,
				Attributes: []AttributeData{
					way(0, "TaskId", BindingHop{Table: "Tasks", JoinColumn: "Id", Column: "ProjectId"}),
					way(1, "TaskId", BindingHop{Table: "Tasks", JoinColumn: "Id", Column: "FormerProjectId"}),
				},
				Under: ways(2),
			},
			{Name: "Invoices", Scope: domain, Permissions: read, Domain: tenancy},
			{
				Name: "Projects", Scope: domain, Permissions: read, Domain: tenancy,
				Attributes: []AttributeData{way(0, "Id")},
				HoldsRoles: &RecordData{
					Key: "Id", One: "project", Many: "projects",
					Names: []RecordNameData{{Resource: "Clients", Column: "ClientId", Attribute: RecordNameAttribute("Projects", 0), One: "client", Many: "clients"}},
				},
				Under: []RecordWayData{{Record: "Projects", Attribute: RecordWayAttribute("Projects", 0), Self: true}},
			},
			{
				Name: "StaffGroupMembers", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.List},
				SubjectSets: []SubjectBindingData{{
					Name: "staffGroups", UserColumn: "UserId", Column: "GroupId", Type: AttributeTypeString,
					Where: &SubjectWhereData{Condition: recordPeriodWhere, Columns: []SubjectColumnData{
						{Name: "EndsAt", Column: "EndsAt", Type: AttributeTypeTimestamp},
						{Name: "StartsAt", Column: "StartsAt", Type: AttributeTypeTimestamp},
					}},
					Domain:  &DomainBindingData{Column: "GroupId", Path: []BindingHop{{Table: "StaffGroups", JoinColumn: "Id", Column: "DomainId"}}},
					Offered: true, Auth: onRecordAuth,
				}},
			},
			{
				Name: "Tasks", Scope: domain, Permissions: read, Domain: tenancy,
				Attributes: []AttributeData{way(0, "ProjectId"), way(1, "FormerProjectId")},
				Under:      ways(2),
			},
		},
		RecordRoleStores: []RecordRoleStoreData{
			{Auth: onRecordAuth, UserRoles: "StaffRecordUserRoles", GroupRoles: "StaffRecordGroupRoles", GroupSet: "staffGroups", GroupMembers: "StaffGroupMembers", Groups: "StaffGroups"},
		},
	}
}

// TestGeneratedCollection_RecordPlace pins how the collection spells a resource's place
// under a record, column by column, for two sites to be compared by: a line for each way,
// its attribute, the column the row leaves through and each hop of its path; a line for
// each name, the record's column and the named row's; the record's own way by its key; and
// nothing for a resource apart.
func TestGeneratedCollection_RecordPlace(t *testing.T) {
	t.Parallel()

	data := onRecordConditionData()
	// Replies are under a project by one way of two hops: their comment's task's project.
	data.Resources = append(data.Resources, CollectionResource{
		Name: "Replies", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read},
		Domain: &DomainBindingData{Column: "DomainId"},
		Attributes: []AttributeData{{
			Name: RecordWayAttribute("Projects", 0), Column: "CommentId", Type: AttributeTypeString,
			Path: []BindingHop{{Table: "Comments", JoinColumn: "Id", Column: "TaskId"}, {Table: "Tasks", JoinColumn: "Id", Column: "ProjectId"}},
		}},
		Under: []RecordWayData{{Record: "Projects", Attribute: RecordWayAttribute("Projects", 0)}},
	})
	g, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	tests := []struct {
		name string
		res  accesstypes.Resource
		want []string
	}{
		{name: "one way of two hops", res: "Replies", want: []string{"held_way0_Projects: CommentId, Comments.Id -> TaskId, Tasks.Id -> ProjectId"}},
		{name: "two ways", res: "Comments", want: []string{"held_way0_Projects: TaskId, Tasks.Id -> ProjectId", "held_way1_Projects: TaskId, Tasks.Id -> FormerProjectId"}},
		{name: "a resource the record names", res: "Clients", want: []string{"held_named0_Projects: ClientId -> Id"}},
		{name: "the record itself", res: "Projects", want: []string{"held_way0_Projects: Id"}},
		{name: "a resource apart", res: "Invoices"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, g.RecordPlace("Projects", tt.res)); diff != "" {
				t.Errorf("RecordPlace(Projects, %s) (-want +got):\n%s", tt.res, diff)
			}
		})
	}
	if got := g.RecordPlace("Invoices", "Tasks"); got != nil {
		t.Errorf("RecordPlace(Invoices, Tasks) = %q, want nil: Invoices is no record a role can be held on", got)
	}
}

// onRecordSecondSiteData is the data of a second site of the fixture's application: its
// program does not generate the auth's management, so no struct declares the set of a
// person's groups (StaffGroupMembers is left out), and it serves Milestones, under a
// project by the project a milestone is in, which the first site does not declare. It
// carries the auth's store as the first does.
func onRecordSecondSiteData() CollectionData {
	data := onRecordConditionData()
	data.Resources = slices.DeleteFunc(data.Resources, func(res CollectionResource) bool {
		return res.Name == "StaffGroupMembers"
	})
	data.Resources = append(data.Resources, CollectionResource{
		Name: "Milestones", Scope: accesstypes.DomainPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Read},
		Domain:     &DomainBindingData{Column: "DomainId"},
		Attributes: []AttributeData{{Name: RecordWayAttribute("Projects", 0), Column: "ProjectId", Type: AttributeTypeString}},
		Under:      []RecordWayData{{Record: "Projects", Attribute: RecordWayAttribute("Projects", 0)}},
	})

	return data
}

// onRecordSecondSiteCollection is the collection of onRecordSecondSiteData.
func onRecordSecondSiteCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(onRecordSecondSiteData())
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// onRecordConditionCollection is the collection of onRecordConditionData.
func onRecordConditionCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(onRecordConditionData())
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// onRecordRow is one row of the fixture: its table and its columns, a column with no value
// left out.
type onRecordRow struct {
	table  string
	values map[string]any
}

// onRecordRows are the fixture's rows, each written after the rows it refers to. In north:
// erin holds ProjectEditor on p1, and on p2 until a time already past; omar holds it on
// p2; the group crew holds ProjectViewer on p2 and erin has a place in it; the group
// guests holds it on p3 and erin's place there has ended. Task t2 is in p2 and came from
// p1; task t7 is in p1 and came from it. Milestones m1, m2 and m3 are in p1, p2 and p3.
// In south erin holds ProjectEditor on p9, which milestone m9 is in.
func onRecordRows() []onRecordRow {
	row := func(table string, values map[string]any) onRecordRow {
		return onRecordRow{table: table, values: values}
	}
	held := func(table, domain, record, role, column, member string, ends *time.Time) onRecordRow {
		values := map[string]any{"Axis": "", "DomainId": domain, "Resource": "Projects", "RecordId": record, "Role": role, column: member}
		if ends != nil {
			values["EndsAt"] = *ends
		}

		return row(table, values)
	}
	ended := onRecordEnded
	editor := func(domain, record, user string, ends *time.Time) onRecordRow {
		return held("StaffRecordUserRoles", domain, record, "ProjectEditor", "UserId", user, ends)
	}
	byGroup := func(record, role, group string) onRecordRow {
		return held("StaffRecordGroupRoles", onRecordNorth, record, role, "GroupId", group, nil)
	}

	return []onRecordRow{
		row("Clients", map[string]any{"Id": "c1", "DomainId": onRecordNorth}),
		row("Clients", map[string]any{"Id": "c2", "DomainId": onRecordNorth}),
		row("Clients", map[string]any{"Id": "c3", "DomainId": onRecordNorth}),
		row("Clients", map[string]any{"Id": "c9", "DomainId": onRecordSouth}),
		row("Projects", map[string]any{"Id": "p1", "DomainId": onRecordNorth, "ClientId": "c1"}),
		row("Projects", map[string]any{"Id": "p2", "DomainId": onRecordNorth, "ClientId": "c2"}),
		row("Projects", map[string]any{"Id": "p3", "DomainId": onRecordNorth, "ClientId": "c3"}),
		row("Projects", map[string]any{"Id": "p9", "DomainId": onRecordSouth, "ClientId": "c9"}),
		row("Tasks", map[string]any{"Id": "t1", "DomainId": onRecordNorth, "ProjectId": "p1"}),
		row("Tasks", map[string]any{"Id": "t2", "DomainId": onRecordNorth, "ProjectId": "p2", "FormerProjectId": "p1"}),
		row("Tasks", map[string]any{"Id": "t3", "DomainId": onRecordNorth, "ProjectId": "p2"}),
		row("Tasks", map[string]any{"Id": "t4", "DomainId": onRecordNorth, "ProjectId": "p3"}),
		row("Tasks", map[string]any{"Id": "t7", "DomainId": onRecordNorth, "ProjectId": "p1", "FormerProjectId": "p1"}),
		row("Tasks", map[string]any{"Id": "t9", "DomainId": onRecordSouth, "ProjectId": "p9"}),
		row("Comments", map[string]any{"Id": "k1", "DomainId": onRecordNorth, "TaskId": "t1"}),
		row("Comments", map[string]any{"Id": "k2", "DomainId": onRecordNorth, "TaskId": "t2"}),
		row("Comments", map[string]any{"Id": "k3", "DomainId": onRecordNorth, "TaskId": "t3"}),
		row("Comments", map[string]any{"Id": "k4", "DomainId": onRecordNorth, "TaskId": "t4"}),
		row("Comments", map[string]any{"Id": "k9", "DomainId": onRecordSouth, "TaskId": "t9"}),
		row("Milestones", map[string]any{"Id": "m1", "DomainId": onRecordNorth, "ProjectId": "p1"}),
		row("Milestones", map[string]any{"Id": "m2", "DomainId": onRecordNorth, "ProjectId": "p2"}),
		row("Milestones", map[string]any{"Id": "m3", "DomainId": onRecordNorth, "ProjectId": "p3"}),
		row("Milestones", map[string]any{"Id": "m9", "DomainId": onRecordSouth, "ProjectId": "p9"}),
		row("Invoices", map[string]any{"Id": "i1", "DomainId": onRecordNorth}),
		row("StaffGroups", map[string]any{"Id": "crew", "DomainId": onRecordNorth}),
		row("StaffGroups", map[string]any{"Id": "guests", "DomainId": onRecordNorth}),
		row("StaffGroupMembers", map[string]any{"GroupId": "crew", "UserId": "erin"}),
		row("StaffGroupMembers", map[string]any{"GroupId": "guests", "UserId": "erin", "EndsAt": onRecordEnded}),
		editor(onRecordNorth, "p1", "erin", nil),
		editor(onRecordNorth, "p2", "erin", &ended),
		editor(onRecordNorth, "p2", "omar", nil),
		editor(onRecordSouth, "p9", "erin", nil),
		byGroup("p2", "ProjectViewer", "crew"),
		byGroup("p3", "ProjectViewer", "guests"),
	}
}

// onRecordRead is one read the fixture's rows answer: the keys of the rows of Resource that a
// holder of Role, User, reaches in north through the role held on projects.
type onRecordRead struct {
	name     string
	user     string
	role     accesstypes.Role
	resource accesstypes.Resource
	want     []string
}

// onRecordReads are the reads both databases are held to, written as a person reads the rows.
func onRecordReads() []onRecordRead {
	return []onRecordRead{
		{name: "erin's project", user: "erin", role: "ProjectEditor", resource: "Projects", want: []string{"p1"}},
		{name: "erin's project's tasks, one by the project it came from alone", user: "erin", role: "ProjectEditor", resource: "Tasks", want: []string{"t1", "t2", "t7"}},
		{name: "the comments on those tasks", user: "erin", role: "ProjectEditor", resource: "Comments", want: []string{"k1", "k2"}},
		{name: "the client erin's project names, and not the ended one's", user: "erin", role: "ProjectEditor", resource: "Clients", want: []string{"c1"}},
		{name: "through a group: the group's project", user: "erin", role: "ProjectViewer", resource: "Projects", want: []string{"p2"}},
		{name: "through a group: the tasks under it", user: "erin", role: "ProjectViewer", resource: "Tasks", want: []string{"t2", "t3"}},
		{name: "through a group: the comments under it", user: "erin", role: "ProjectViewer", resource: "Comments", want: []string{"k2", "k3"}},
		{name: "through a group: its client", user: "erin", role: "ProjectViewer", resource: "Clients", want: []string{"c2"}},
		{name: "omar's project", user: "omar", role: "ProjectEditor", resource: "Projects", want: []string{"p2"}},
		{name: "omar's project's tasks", user: "omar", role: "ProjectEditor", resource: "Tasks", want: []string{"t2", "t3"}},
		{name: "omar's project's comments", user: "omar", role: "ProjectEditor", resource: "Comments", want: []string{"k2", "k3"}},
		{name: "omar's project's client", user: "omar", role: "ProjectEditor", resource: "Clients", want: []string{"c2"}},
	}
}

// onRecordCreate is one create of a task the fixture's rows answer: whether erin, holding
// ProjectEditor, may write the row with these columns.
type onRecordCreate struct {
	name     string
	proposed map[string]any
	want     bool
}

// onRecordCreates are the creates both databases are held to.
func onRecordCreates() []onRecordCreate {
	return []onRecordCreate{
		{name: "a task in erin's project", proposed: map[string]any{"Id": "t5", "DomainId": onRecordNorth, "ProjectId": "p1", "FormerProjectId": "p3"}, want: true},
		{name: "a task in the project whose role has ended", proposed: map[string]any{"Id": "t6", "DomainId": onRecordNorth, "ProjectId": "p2", "FormerProjectId": "p3"}, want: false},
	}
}

// onRecordUpdate is one update the fixture's rows answer: whether erin, holding
// ProjectEditor on p1 alone, may change the row of the resource keyed id by the columns
// proposed.
type onRecordUpdate struct {
	name     string
	resource accesstypes.Resource
	id       string
	proposed map[string]any
	want     bool
}

// onRecordUpdates are the updates Spanner is held to: a row the update leaves under p1 is
// written, and one it moves out from under p1 is not.
func onRecordUpdates() []onRecordUpdate {
	return []onRecordUpdate{
		{name: "a comment moved to another task of erin's project", resource: "Comments", id: "k2", proposed: map[string]any{"TaskId": "t1"}, want: true},
		{name: "a comment moved to a task of another project", resource: "Comments", id: "k1", proposed: map[string]any{"TaskId": "t3"}, want: false},
		{name: "a comment's body alone", resource: "Comments", id: "k1", proposed: map[string]any{"Body": "Seen to."}, want: true},
		{name: "a task moved to another project", resource: "Tasks", id: "t1", proposed: map[string]any{"ProjectId": "p2"}, want: false},
		{name: "a task under erin's project both ways, the project it came from moved", resource: "Tasks", id: "t7", proposed: map[string]any{"FormerProjectId": "p2"}, want: true},
		{name: "a task under erin's project by the project it came from alone, that moved", resource: "Tasks", id: "t2", proposed: map[string]any{"FormerProjectId": "p2"}, want: false},
	}
}

// onRecordStatement lowers the condition a holder of role reaches resource's rows under for
// perm: a read's and an update's over the row read as "r" in a request of a tenant, an
// update's with proposed as the columns it touches, and a create's over the row being
// created, whose columns proposed holds.
func onRecordStatement(t *testing.T, collection *GeneratedCollection, dialect SQLDialect, role accesstypes.Role, res accesstypes.Resource, perm accesstypes.Permission, proposed map[string]any) (string, *paramRegistry) {
	t.Helper()

	source, ok := collection.RecordCondition(onRecordAuth, role, "Projects", res, perm)
	if !ok {
		t.Fatalf("RecordCondition(%s, %s, Projects, %s, %s) is not ok", onRecordAuth, role, res, perm)
	}
	expr, err := condition.Parse(source)
	if err != nil {
		t.Fatalf("condition.Parse(%q) error = %v", source, err)
	}
	bindings, _ := collection.Bindings(accesstypes.DomainPermissionScope, res)
	lctx := &loweringContext{outer: "r", bindings: bindings, collection: collection, partitioned: true}
	switch perm {
	case accesstypes.Create:
		lctx.outer, lctx.proposed, lctx.insertImage = "", newProposedOverlay(proposed), true
	case accesstypes.Update:
		lctx.proposed = newProposedOverlay(proposed)
	default:
	}
	registry := newParamRegistry()
	sql, err := lowerToSQL(expr, lctx, newSQLGenerator(dialect), registry)
	if err != nil {
		t.Fatalf("lowerToSQL(%q) error = %v", source, err)
	}

	return sql, registry
}

// onRecordParams are a statement's parameters: what it bound, and the reserved names it reads
// (the person, the tenant north and the instant onRecordNow).
func onRecordParams(registry *paramRegistry, user string) map[string]any {
	params := make(map[string]any)
	for _, param := range registry.boundParams() {
		params[param.Name] = param.Value
	}
	for _, name := range registry.referencedNames() {
		switch name {
		case subjectParamName:
			params[name] = user
		case domainParamName:
			params[name] = onRecordNorth
		case nowParamName:
			params[name] = onRecordNow
		}
	}

	return params
}

// onRecordDatabase is a database the fixture's rows are written to, as the questions of
// the fixture ask it: keys runs a statement that selects one text column and answers its
// values, and holds runs one that selects one boolean.
type onRecordDatabase struct {
	dialect SQLDialect
	keys    func(t *testing.T, sql string, params map[string]any) []string
	holds   func(t *testing.T, sql string, params map[string]any) bool
}

// askOnRecord holds the database to the fixture's reads and creates: each condition's
// statement is run against the rows, and the keys it keeps, or its answer for a row being
// created, compared with what the fixture says a person reads in the rows.
func askOnRecord(t *testing.T, db onRecordDatabase) {
	t.Helper()

	collection := onRecordConditionCollection(t)
	quote := newSQLGenerator(db.dialect).quoteIdentifier
	for _, read := range onRecordReads() {
		t.Run(read.name, func(t *testing.T) {
			t.Parallel()

			sql, registry := onRecordStatement(t, collection, db.dialect, read.role, read.resource, accesstypes.Read, nil)
			statement := fmt.Sprintf("SELECT %[1]s.%[2]s FROM %[3]s %[1]s WHERE %[4]s ORDER BY %[1]s.%[2]s", quote("r"), quote("Id"), quote(string(read.resource)), sql)
			if got := db.keys(t, statement, onRecordParams(registry, read.user)); !slices.Equal(got, read.want) {
				t.Errorf("%s holding %s reaches %s %v, want %v\n%s", read.user, read.role, read.resource, got, read.want, statement)
			}
		})
	}
	for _, create := range onRecordCreates() {
		t.Run(create.name, func(t *testing.T) {
			t.Parallel()

			sql, registry := onRecordStatement(t, collection, db.dialect, "ProjectEditor", "Tasks", accesstypes.Create, create.proposed)
			statement := "SELECT (" + sql + ")"
			if got := db.holds(t, statement, onRecordParams(registry, "erin")); got != create.want {
				t.Errorf("erin holding ProjectEditor may create %v: %v, want %v\n%s", create.proposed, got, create.want, statement)
			}
		})
	}
}

// askOnRecordSecondSite holds the database to a second site's reads of Milestones, which
// only the second site declares: the condition is the one the sites' union gives, the
// first member's that gives one, which is the second's, since the first declares none on
// Milestones; lowered with the second site's collection, which declares no set of a
// person's groups and reads it from the store, it reaches the milestones under the
// projects a person holds the role on, and those a group they are in holds it on, and no
// others.
func askOnRecordSecondSite(t *testing.T, db onRecordDatabase) {
	t.Helper()

	first, second := onRecordConditionCollection(t), onRecordSecondSiteCollection(t)
	quote := newSQLGenerator(db.dialect).quoteIdentifier
	for _, read := range []onRecordRead{
		{name: "a second site: erin's project's milestones", user: "erin", role: "ProjectEditor", resource: "Milestones", want: []string{"m1"}},
		{name: "a second site: through a group, the group's project's milestones", user: "erin", role: "ProjectViewer", resource: "Milestones", want: []string{"m2"}},
		{name: "a second site: omar's project's milestones", user: "omar", role: "ProjectEditor", resource: "Milestones", want: []string{"m2"}},
	} {
		t.Run(read.name, func(t *testing.T) {
			t.Parallel()

			if _, ok := first.RecordCondition(onRecordAuth, read.role, "Projects", read.resource, accesstypes.Read); ok {
				t.Fatalf("the first site gives a condition on %s, which it does not declare", read.resource)
			}
			sql, registry := onRecordStatement(t, second, db.dialect, read.role, read.resource, accesstypes.Read, nil)
			statement := fmt.Sprintf("SELECT %[1]s.%[2]s FROM %[3]s %[1]s WHERE %[4]s ORDER BY %[1]s.%[2]s", quote("r"), quote("Id"), quote(string(read.resource)), sql)
			if got := db.keys(t, statement, onRecordParams(registry, read.user)); !slices.Equal(got, read.want) {
				t.Errorf("%s holding %s reaches %s %v in the second site, want %v\n%s", read.user, read.role, read.resource, got, read.want, statement)
			}
		})
	}
}

// askOnRecordUpdates holds the database to the fixture's updates: each update's condition
// is run against the row it changes, read as "r", with the columns it touches as their
// proposed values.
func askOnRecordUpdates(t *testing.T, db onRecordDatabase) {
	t.Helper()

	collection := onRecordConditionCollection(t)
	quote := newSQLGenerator(db.dialect).quoteIdentifier
	for _, update := range onRecordUpdates() {
		t.Run(update.name, func(t *testing.T) {
			t.Parallel()

			sql, registry := onRecordStatement(t, collection, db.dialect, "ProjectEditor", update.resource, accesstypes.Update, update.proposed)
			statement := fmt.Sprintf("SELECT (%[4]s) FROM %[3]s %[1]s WHERE %[1]s.%[2]s = @onRecordKey", quote("r"), quote("Id"), quote(string(update.resource)), sql)
			params := onRecordParams(registry, "erin")
			params["onRecordKey"] = update.id
			if got := db.holds(t, statement, params); got != update.want {
				t.Errorf("erin holding ProjectEditor may change %s %s by %v: %v, want %v\n%s", update.resource, update.id, update.proposed, got, update.want, statement)
			}
		})
	}
}

// TestGeneratedCollection_RecordCondition pins the condition a role held on a record counts
// under, as text: the ways of a resource to the record, then the record's names of it, a
// person's set before a group's; a create's, which leaves the record's own way and its
// names out; an update's, the read's asked of the row as stored and again of the row as the
// update leaves it; and where there is none.
func TestGeneratedCollection_RecordCondition(t *testing.T) {
	t.Parallel()

	collection := onRecordConditionCollection(t)
	set := func(kind, role string) string {
		return "subject.held_" + kind + "_staff_" + role + "_Projects"
	}
	member := func(image, attribute, kind string) string {
		return image + attribute + " IN " + set(kind, "ProjectEditor") + " OR " + image + attribute + " IN " + set(kind[:len(kind)-2]+"bygroup", "ProjectEditor")
	}
	tests := []struct {
		name string
		auth string
		res  accesstypes.Resource
		perm accesstypes.Permission
		want string
	}{
		{name: "the record itself", auth: onRecordAuth, res: "Projects", want: member("", "held_way0_Projects", "by")},
		{name: "a resource under the record by two ways", auth: onRecordAuth, res: "Tasks", want: member("", "held_way0_Projects", "by") + " OR " + member("", "held_way1_Projects", "by")},
		{name: "a resource the record names", auth: onRecordAuth, res: "Clients", want: member("", "held_named0_Projects", "named0by")},
		{name: "a create under the record", auth: onRecordAuth, res: "Tasks", perm: accesstypes.Create, want: member("", "held_way0_Projects", "by") + " OR " + member("", "held_way1_Projects", "by")},
		{name: "a create of the record's own kind", auth: onRecordAuth, res: "Projects", perm: accesstypes.Create},
		{name: "a create of a resource the record names", auth: onRecordAuth, res: "Clients", perm: accesstypes.Create},
		{
			name: "an update of the record itself, by its one way", auth: onRecordAuth, res: "Projects", perm: accesstypes.Update,
			want: "(" + member("", "held_way0_Projects", "by") + ") AND (" + member("new.", "held_way0_Projects", "by") + ")",
		},
		{
			name: "an update of a resource under the record by two ways", auth: onRecordAuth, res: "Tasks", perm: accesstypes.Update,
			want: "(" + member("", "held_way0_Projects", "by") + " OR " + member("", "held_way1_Projects", "by") + ") AND (" +
				member("new.", "held_way0_Projects", "by") + " OR " + member("new.", "held_way1_Projects", "by") + ")",
		},
		{
			name: "an update of a resource the record names", auth: onRecordAuth, res: "Clients", perm: accesstypes.Update,
			want: "(" + member("", "held_named0_Projects", "named0by") + ") AND (" + member("new.", "held_named0_Projects", "named0by") + ")",
		},
		{name: "a method's target reads as a read", auth: onRecordAuth, res: "Tasks", perm: accesstypes.Execute, want: member("", "held_way0_Projects", "by") + " OR " + member("", "held_way1_Projects", "by")},
		{name: "an update of a resource apart", auth: onRecordAuth, res: "Invoices", perm: accesstypes.Update},
		{name: "a resource apart", auth: onRecordAuth, res: "Invoices"},
		{name: "an auth that keeps no roles on records", auth: "crew", res: "Tasks"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			perm := tt.perm
			if perm == "" {
				perm = accesstypes.Read
			}
			got, ok := collection.RecordCondition(tt.auth, "ProjectEditor", "Projects", tt.res, perm)
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("RecordCondition(%s, ProjectEditor, Projects, %s, %s) = (%q, %v), want (%q, %v)", tt.auth, tt.res, perm, got, ok, tt.want, tt.want != "")
			}
		})
	}

	t.Run("a record that is not marked", func(t *testing.T) {
		t.Parallel()

		if got, ok := collection.RecordCondition(onRecordAuth, "ProjectEditor", "Tasks", "Comments", accesstypes.Read); ok {
			t.Errorf("RecordCondition(staff, ProjectEditor, Tasks, Comments) = (%q, true), want nothing", got)
		}
	})

	t.Run("a store that keeps no groups has no group's test", func(t *testing.T) {
		t.Parallel()

		data := onRecordConditionData()
		data.RecordRoleStores[0].GroupRoles, data.RecordRoleStores[0].GroupSet = "", ""
		g, err := NewGeneratedCollection(data)
		if err != nil {
			t.Fatalf("NewGeneratedCollection() error = %v", err)
		}
		want := "held_named0_Projects IN " + set("named0by", "ProjectEditor")
		if got, _ := g.RecordCondition(onRecordAuth, "ProjectEditor", "Projects", "Clients", accesstypes.Read); got != want {
			t.Errorf("RecordCondition(staff, ProjectEditor, Projects, Clients) = %q, want %q", got, want)
		}
		if _, ok := g.SubjectSet("held_bygroup_staff_ProjectEditor_Projects"); ok {
			t.Error("SubjectSet(held_bygroup_staff_ProjectEditor_Projects) is a set in a store that keeps no groups")
		}
	})
}

// TestGeneratedCollection_storeGroupSet pins the set of a person's groups a collection
// reads from its store where no struct declares it: a second site's answers the anchor
// the generated group member declares in the first, compared whole, and its readers
// answer for it as for a declared set; a declared set wins over the store's; a store that
// names no group tables gives none, and neither does a name no store gives its sets.
func TestGeneratedCollection_storeGroupSet(t *testing.T) {
	t.Parallel()

	first, second := onRecordConditionCollection(t), onRecordSecondSiteCollection(t)
	declared, ok := first.SubjectSet("staffGroups")
	if !ok {
		t.Fatal("the first site does not answer its declared set staffGroups")
	}
	derived, ok := second.SubjectSet("staffGroups")
	if !ok {
		t.Fatal("the second site, which declares no set staffGroups, does not answer it from the store")
	}
	if diff := cmp.Diff(declared, derived); diff != "" {
		t.Errorf("the store's set against the declared one (-declared +store):\n%s", diff)
	}
	if diff := cmp.Diff(first.SubjectSetDefinition("staffGroups"), second.SubjectSetDefinition("staffGroups")); diff != "" {
		t.Errorf("SubjectSetDefinition(staffGroups) of the two sites (-first +second):\n%s", diff)
	}
	if got := second.SubjectSetAuth("staffGroups"); got != onRecordAuth {
		t.Errorf("SubjectSetAuth(staffGroups) = %q, want %q", got, onRecordAuth)
	}
	if got, ok := second.SubjectSetComparisonType("staffGroups"); !ok || got != AttributeTypeString {
		t.Errorf("SubjectSetComparisonType(staffGroups) = %q, %v, want string, true", got, ok)
	}

	data := onRecordConditionData()
	for i := range data.Resources {
		if data.Resources[i].Name == "StaffGroupMembers" {
			data.Resources[i].SubjectSets[0].Column = "UserId"
		}
	}
	marked, err := NewGeneratedCollection(data)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}
	if anchor, _ := marked.SubjectSet("staffGroups"); anchor.Binding.Column != "UserId" {
		t.Errorf("a declared set staffGroups yields %q, want its own column UserId: a declared set wins over the store's", anchor.Binding.Column)
	}

	bare := onRecordSecondSiteData()
	bare.RecordRoleStores[0].GroupMembers, bare.RecordRoleStores[0].Groups = "", ""
	none, err := NewGeneratedCollection(bare)
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}
	if _, ok := none.SubjectSet("staffGroups"); ok {
		t.Error("a store that names no group tables gives the set staffGroups")
	}
	if _, ok := second.SubjectSet("crewGroups"); ok {
		t.Error("the set crewGroups, which no store gives, is answered")
	}
}

// TestGeneratedCollection_recordSets pins the subject sets of the records a person holds a
// role on, which the collection reads from their names alone: what each reads, and the
// names that are none of them.
func TestGeneratedCollection_recordSets(t *testing.T) {
	t.Parallel()

	collection := onRecordConditionCollection(t)
	where := recordSetWhere("Projects", "ProjectEditor")
	tenancy := &DomainBindingData{Column: "DomainId"}
	toClient := []BindingHop{{Table: "Projects", JoinColumn: "Id", Column: "ClientId"}}
	groups := []SubjectWhoData{{Column: "GroupId", Set: "staffGroups"}}
	anchor := func(name, table, user string, who []SubjectWhoData, path []BindingHop) SubjectAnchor {
		return SubjectAnchor{
			Resource: accesstypes.Resource(table), Scope: accesstypes.DomainPermissionScope, Domain: tenancy,
			Binding: SubjectBindingData{Name: name, UserColumn: user, Column: "RecordId", Type: AttributeTypeString, Path: path, Who: who, Where: where, Offered: true, Auth: onRecordAuth},
		}
	}
	for name, want := range map[string]SubjectAnchor{
		"held_by_staff_ProjectEditor_Projects":            anchor("held_by_staff_ProjectEditor_Projects", "StaffRecordUserRoles", "UserId", nil, nil),
		"held_bygroup_staff_ProjectEditor_Projects":       anchor("held_bygroup_staff_ProjectEditor_Projects", "StaffRecordGroupRoles", "", groups, nil),
		"held_named0by_staff_ProjectEditor_Projects":      anchor("held_named0by_staff_ProjectEditor_Projects", "StaffRecordUserRoles", "UserId", nil, toClient),
		"held_named0bygroup_staff_ProjectEditor_Projects": anchor("held_named0bygroup_staff_ProjectEditor_Projects", "StaffRecordGroupRoles", "", groups, toClient),
	} {
		got, ok := collection.SubjectSet(name)
		if !ok {
			t.Errorf("SubjectSet(%s) is no set", name)

			continue
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("SubjectSet(%s) mismatch (-want +got):\n%s", name, diff)
		}
		if typ, ok := collection.SubjectSetComparisonType(name); !ok || typ != AttributeTypeString {
			t.Errorf("SubjectSetComparisonType(%s) = (%q, %v), want (string, true)", name, typ, ok)
		}
		if got := collection.SubjectSetAuth(name); got != onRecordAuth {
			t.Errorf("SubjectSetAuth(%s) = %q, want %q", name, got, onRecordAuth)
		}
	}
	if want := "Resource = 'Projects' AND Role = 'ProjectEditor' AND " + recordPeriodWhere; where.Condition != want {
		t.Errorf("the record set's where: = %q, want %q", where.Condition, want)
	}
	for name := range collection.SubjectSets("") {
		if strings.HasPrefix(name, ReservedBindingPrefix) {
			t.Errorf("SubjectSets() lists %s: it lists the declared sets alone", name)
		}
	}

	for _, name := range []string{
		"held_by_crew_ProjectEditor_Projects",
		"held_by_staff_ProjectEditor_Tasks",
		"held_by_staff_projectEditor_Projects",
		"held_by_staff__Projects",
		"held_named1by_staff_ProjectEditor_Projects",
		"held_named00by_staff_ProjectEditor_Projects",
		"held_namedby_staff_ProjectEditor_Projects",
		"held_of_staff_ProjectEditor_Projects",
		"by_staff_ProjectEditor_Projects",
	} {
		if got, ok := collection.SubjectSet(name); ok {
			t.Errorf("SubjectSet(%s) = %+v, want no set", name, got)
		}
	}
}

// TestGeneratedCollection_recordConditionRendering pins the statement both databases are
// sent for the comments a holder of a role on a project reaches, two hops from it, so a
// change of its shape is seen.
func TestGeneratedCollection_recordConditionRendering(t *testing.T) {
	t.Parallel()

	collection := onRecordConditionCollection(t)
	for dialect, want := range map[SQLDialect]string{
		Spanner:    onRecordCommentsSpanner,
		PostgreSQL: onRecordCommentsPostgres,
	} {
		sql, _ := onRecordStatement(t, collection, dialect, "ProjectEditor", "Comments", accesstypes.Read, nil)
		if sql != want {
			t.Errorf("the comments' statement on %v =\n%s\nwant\n%s", dialect, sql, want)
		}
	}
}

// The statements TestGeneratedCollection_recordConditionRendering pins.
const (
	onRecordCommentsSpanner  = "(CASE WHEN (SELECT `ca1`.`ProjectId` FROM `Tasks` `ca1` WHERE `ca1`.`Id` = `r`.`TaskId`) IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `StaffRecordUserRoles` `ca2` WHERE `ca2`.`UserId` = @subject AND (`ca2`.`Resource` = @_c1 AND `ca2`.`Role` = @_c2 AND (`ca2`.`StartsAt` IS NULL OR `ca2`.`StartsAt` <= @now) AND (`ca2`.`EndsAt` IS NULL OR `ca2`.`EndsAt` > @now)) AND `ca2`.`RecordId` = (SELECT `ca1`.`ProjectId` FROM `Tasks` `ca1` WHERE `ca1`.`Id` = `r`.`TaskId`) AND `ca2`.`DomainId` = @domain) END OR CASE WHEN (SELECT `ca3`.`ProjectId` FROM `Tasks` `ca3` WHERE `ca3`.`Id` = `r`.`TaskId`) IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `StaffRecordGroupRoles` `ca4` WHERE EXISTS (SELECT 1 FROM `StaffGroupMembers` `ca5` WHERE `ca5`.`UserId` = @subject AND ((`ca5`.`StartsAt` IS NULL OR `ca5`.`StartsAt` <= @now) AND (`ca5`.`EndsAt` IS NULL OR `ca5`.`EndsAt` > @now)) AND `ca5`.`GroupId` = `ca4`.`GroupId` AND EXISTS (SELECT 1 FROM `StaffGroups` `ca6` WHERE `ca6`.`Id` = `ca5`.`GroupId` AND `ca6`.`DomainId` = @domain)) AND (`ca4`.`Resource` = @_c3 AND `ca4`.`Role` = @_c4 AND (`ca4`.`StartsAt` IS NULL OR `ca4`.`StartsAt` <= @now) AND (`ca4`.`EndsAt` IS NULL OR `ca4`.`EndsAt` > @now)) AND `ca4`.`RecordId` = (SELECT `ca3`.`ProjectId` FROM `Tasks` `ca3` WHERE `ca3`.`Id` = `r`.`TaskId`) AND `ca4`.`DomainId` = @domain) END OR CASE WHEN (SELECT `ca7`.`FormerProjectId` FROM `Tasks` `ca7` WHERE `ca7`.`Id` = `r`.`TaskId`) IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `StaffRecordUserRoles` `ca8` WHERE `ca8`.`UserId` = @subject AND (`ca8`.`Resource` = @_c5 AND `ca8`.`Role` = @_c6 AND (`ca8`.`StartsAt` IS NULL OR `ca8`.`StartsAt` <= @now) AND (`ca8`.`EndsAt` IS NULL OR `ca8`.`EndsAt` > @now)) AND `ca8`.`RecordId` = (SELECT `ca7`.`FormerProjectId` FROM `Tasks` `ca7` WHERE `ca7`.`Id` = `r`.`TaskId`) AND `ca8`.`DomainId` = @domain) END OR CASE WHEN (SELECT `ca9`.`FormerProjectId` FROM `Tasks` `ca9` WHERE `ca9`.`Id` = `r`.`TaskId`) IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `StaffRecordGroupRoles` `ca10` WHERE EXISTS (SELECT 1 FROM `StaffGroupMembers` `ca11` WHERE `ca11`.`UserId` = @subject AND ((`ca11`.`StartsAt` IS NULL OR `ca11`.`StartsAt` <= @now) AND (`ca11`.`EndsAt` IS NULL OR `ca11`.`EndsAt` > @now)) AND `ca11`.`GroupId` = `ca10`.`GroupId` AND EXISTS (SELECT 1 FROM `StaffGroups` `ca12` WHERE `ca12`.`Id` = `ca11`.`GroupId` AND `ca12`.`DomainId` = @domain)) AND (`ca10`.`Resource` = @_c7 AND `ca10`.`Role` = @_c8 AND (`ca10`.`StartsAt` IS NULL OR `ca10`.`StartsAt` <= @now) AND (`ca10`.`EndsAt` IS NULL OR `ca10`.`EndsAt` > @now)) AND `ca10`.`RecordId` = (SELECT `ca9`.`FormerProjectId` FROM `Tasks` `ca9` WHERE `ca9`.`Id` = `r`.`TaskId`) AND `ca10`.`DomainId` = @domain) END)"
	onRecordCommentsPostgres = `(CASE WHEN (SELECT "ca1"."ProjectId" FROM "Tasks" "ca1" WHERE "ca1"."Id" = "r"."TaskId") IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "StaffRecordUserRoles" "ca2" WHERE "ca2"."UserId" = @subject AND ("ca2"."Resource" = @_c1 AND "ca2"."Role" = @_c2 AND ("ca2"."StartsAt" IS NULL OR "ca2"."StartsAt" <= @now) AND ("ca2"."EndsAt" IS NULL OR "ca2"."EndsAt" > @now)) AND "ca2"."RecordId" = (SELECT "ca1"."ProjectId" FROM "Tasks" "ca1" WHERE "ca1"."Id" = "r"."TaskId") AND "ca2"."DomainId" = @domain) END OR CASE WHEN (SELECT "ca3"."ProjectId" FROM "Tasks" "ca3" WHERE "ca3"."Id" = "r"."TaskId") IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "StaffRecordGroupRoles" "ca4" WHERE EXISTS (SELECT 1 FROM "StaffGroupMembers" "ca5" WHERE "ca5"."UserId" = @subject AND (("ca5"."StartsAt" IS NULL OR "ca5"."StartsAt" <= @now) AND ("ca5"."EndsAt" IS NULL OR "ca5"."EndsAt" > @now)) AND "ca5"."GroupId" = "ca4"."GroupId" AND EXISTS (SELECT 1 FROM "StaffGroups" "ca6" WHERE "ca6"."Id" = "ca5"."GroupId" AND "ca6"."DomainId" = @domain)) AND ("ca4"."Resource" = @_c3 AND "ca4"."Role" = @_c4 AND ("ca4"."StartsAt" IS NULL OR "ca4"."StartsAt" <= @now) AND ("ca4"."EndsAt" IS NULL OR "ca4"."EndsAt" > @now)) AND "ca4"."RecordId" = (SELECT "ca3"."ProjectId" FROM "Tasks" "ca3" WHERE "ca3"."Id" = "r"."TaskId") AND "ca4"."DomainId" = @domain) END OR CASE WHEN (SELECT "ca7"."FormerProjectId" FROM "Tasks" "ca7" WHERE "ca7"."Id" = "r"."TaskId") IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "StaffRecordUserRoles" "ca8" WHERE "ca8"."UserId" = @subject AND ("ca8"."Resource" = @_c5 AND "ca8"."Role" = @_c6 AND ("ca8"."StartsAt" IS NULL OR "ca8"."StartsAt" <= @now) AND ("ca8"."EndsAt" IS NULL OR "ca8"."EndsAt" > @now)) AND "ca8"."RecordId" = (SELECT "ca7"."FormerProjectId" FROM "Tasks" "ca7" WHERE "ca7"."Id" = "r"."TaskId") AND "ca8"."DomainId" = @domain) END OR CASE WHEN (SELECT "ca9"."FormerProjectId" FROM "Tasks" "ca9" WHERE "ca9"."Id" = "r"."TaskId") IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "StaffRecordGroupRoles" "ca10" WHERE EXISTS (SELECT 1 FROM "StaffGroupMembers" "ca11" WHERE "ca11"."UserId" = @subject AND (("ca11"."StartsAt" IS NULL OR "ca11"."StartsAt" <= @now) AND ("ca11"."EndsAt" IS NULL OR "ca11"."EndsAt" > @now)) AND "ca11"."GroupId" = "ca10"."GroupId" AND EXISTS (SELECT 1 FROM "StaffGroups" "ca12" WHERE "ca12"."Id" = "ca11"."GroupId" AND "ca12"."DomainId" = @domain)) AND ("ca10"."Resource" = @_c7 AND "ca10"."Role" = @_c8 AND ("ca10"."StartsAt" IS NULL OR "ca10"."StartsAt" <= @now) AND ("ca10"."EndsAt" IS NULL OR "ca10"."EndsAt" > @now)) AND "ca10"."RecordId" = (SELECT "ca9"."FormerProjectId" FROM "Tasks" "ca9" WHERE "ca9"."Id" = "r"."TaskId") AND "ca10"."DomainId" = @domain) END)`
)
