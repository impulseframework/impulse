package generation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// deleteFixtureResources extracts the deletefixture package over its schema: every
// cross-resource pass has run, the one that holds a @deleteCascade to the schema among
// them.
func deleteFixtureResources(t *testing.T) (c *client, byName map[string]*resourceInfo) {
	t.Helper()

	c = &client{tableMap: deleteFixtureTables()}
	resources, err := c.structsToResources(loadFixture(t, "deletefixture").Structs)
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}

	byName = make(map[string]*resourceInfo, len(resources))
	for _, res := range resources {
		byName[res.Name()] = res
	}

	return c, byName
}

// resourceList is the resources in a fixed order, as the pass over them takes them.
func resourceList(byName map[string]*resourceInfo) []*resourceInfo {
	list := make([]*resourceInfo, 0, len(byName))
	for _, name := range []string{"Client", "Project", "Task", "TaskComment", "ProjectNote", "Ledger", "Supplier", "Invoice"} {
		list = append(list, byName[name])
	}

	return list
}

func link(column, table string) deleteLink {
	return deleteLink{Columns: []string{column}, Table: table, References: []string{"Id"}}
}

// Test_deleteCascade_description pins the description the generator writes for a record
// that declares what its delete takes: the kinds taken in delete order, deepest first,
// each with its ways up to the record and what a row's delete costs, the kinds that
// refuse the delete, and the kinds with a row directly under the record that the
// database does not remove with it. A record that declares nothing has a description
// of those last kinds alone, and a record with no such kind has none.
func Test_deleteCascade_description(t *testing.T) {
	t.Parallel()

	c, byName := deleteFixtureResources(t)
	client := byName["Client"]

	want := &deleteTree{
		Record: &deleteKind{Name: "Client", Table: "Clients", One: "client", Many: "clients", Key: []string{"Id"}},
		Taken: []*deleteKind{
			{
				Name: "TaskComment", Table: "TaskComments", One: "task comment", Many: "task comments", Key: []string{"Id"},
				Paths: [][]deleteLink{{link("TaskId", "Tasks"), link("ProjectId", "Projects"), link("ClientId", "Clients")}}, Deletes: true,
			},
			{
				Name: "ProjectNote", Table: "ProjectNotes", One: "project note", Many: "project notes", Key: []string{"Id", "Sequence"},
				Paths: [][]deleteLink{{link("Id", "Projects"), link("ClientId", "Clients")}}, Deletes: true,
			},
			{
				Name: "Task", Table: "Tasks", One: "task", Many: "tasks", Key: []string{"Id"}, IndexEntries: 1,
				Paths: [][]deleteLink{{link("ProjectId", "Projects"), link("ClientId", "Clients")}}, Deletes: true,
			},
			{
				Name: "Project", Table: "Projects", One: "project", Many: "projects", Key: []string{"Id"},
				Paths: [][]deleteLink{{link("ClientId", "Clients")}}, Deletes: true,
			},
		},
		RefusedBy: []*deleteKind{
			{
				Name: "Invoice", Table: "Invoices", One: "invoice", Many: "invoices", Key: []string{"Id"},
				Paths: [][]deleteLink{{link("ClientId", "Clients")}},
			},
		},
		Under: []*deleteKind{
			{
				Name: "Invoice", Table: "Invoices", One: "invoice", Many: "invoices", Key: []string{"Id"},
				Paths: [][]deleteLink{{link("ClientId", "Clients")}},
			},
			{
				Name: "Project", Table: "Projects", One: "project", Many: "projects", Key: []string{"Id"},
				Paths: [][]deleteLink{{link("ClientId", "Clients")}},
			},
		},
	}
	if diff := cmp.Diff(want, client.DeleteTree); diff != "" {
		t.Fatalf("Client's description mismatch (-want +got):\n%s", diff)
	}

	// A project declares nothing: its description is the kinds that refuse its plain
	// delete, its tasks and the notes interleaved in it with no delete declared.
	wantProject := &deleteTree{
		Record: &deleteKind{Name: "Project", Table: "Projects", One: "project", Many: "projects", Key: []string{"Id"}},
		Under: []*deleteKind{
			{
				Name: "ProjectNote", Table: "ProjectNotes", One: "project note", Many: "project notes", Key: []string{"Id", "Sequence"},
				Paths: [][]deleteLink{{link("Id", "Projects")}},
			},
			{
				Name: "Task", Table: "Tasks", One: "task", Many: "tasks", Key: []string{"Id"}, IndexEntries: 1,
				Paths: [][]deleteLink{{link("ProjectId", "Projects")}},
			},
		},
	}
	if diff := cmp.Diff(wantProject, byName["Project"].DeleteTree); diff != "" {
		t.Errorf("Project's description mismatch (-want +got):\n%s", diff)
	}

	for _, name := range []string{"Invoice", "Supplier", "Ledger", "TaskComment"} {
		if byName[name].DeleteTree != nil {
			t.Errorf("%s has a description, though it declares no @%s, is no tenant record and has no kind of row under it", name, deleteCascadeKeyword)
		}
	}

	r := &resourceGenerator{client: c}
	output, err := r.generateTemplateOutput("resourceFileTemplate", resourceFileTemplate, &resourceFileData{
		Source:   "fixture",
		Package:  "resources",
		Resource: client,
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	rendered := string(output)
	for _, wantLine := range []string{
		"func (Client) DeleteTree() *resource.DeleteTree {",
		`Record: "Clients",`,
		`Words: resource.DeleteWords{One: "task comment", Many: "task comments"},`,
		`Key: []string{"Id", "Sequence"},`,
		`Cost: resource.DeleteCost{IndexEntries: 1, Files: 0},`,
		`{Columns: []string{"TaskId"}, Table: "Tasks", References: []string{"Id"}},`,
		`{Columns: []string{"Id"}, Table: "Projects", References: []string{"Id"}},`,
		"RefusedBy: []resource.DeleteKind{",
		"Under: []resource.DeleteKind{",
		"Rows: resource.NewTakenRows(func(patchSet *resource.PatchSet[Task], caller *resource.Caller) {",
		"patch := &TaskDeletePatch{patchSet: patchSet}",
		"func (p *ClientDeletePatch) WithEverythingUnder() *ClientDeletePatch {",
	} {
		if !strings.Contains(rendered, wantLine) {
			t.Errorf("rendered Client is missing %q", wantLine)
		}
	}
	// Deepest first: a comment is written before the task it is on, and the task before
	// its project.
	comment := strings.Index(rendered, `Resource: "TaskComments",`)
	task := strings.Index(rendered, `Resource: "Tasks",`)
	project := strings.Index(rendered, `Resource: "Projects",`)
	if comment < 0 || task < comment || project < task {
		t.Errorf("rendered Client lists its taken kinds out of delete order: TaskComments at %d, Tasks at %d, Projects at %d", comment, task, project)
	}
}

// Test_kindsUnder pins which kinds refuse a record's plain delete, over a schema where
// the database removes some rows itself: a kind the database removes with the record is
// not one, whether by a foreign key that says so or by an interleave that does while a
// foreign key on the same columns says nothing; a kind under the record by a link the
// database does not follow is one, and carries the tables its rows go with at the
// database; a row under a row of its own kind is one, unless the database follows a
// link of the table to itself; a table no resource declares is left out; and a
// read-only table (@enumerate) gets no description, whatever is under it.
func Test_kindsUnder(t *testing.T) {
	t.Parallel()

	c, byName := deleteFixtureResources(t)
	for _, res := range byName {
		res.deleteCascade, res.DeleteTree = nil, nil
	}
	byName["Supplier"].EnumerationType = "SupplierKind"

	to := func(column, table, rule string) foreignKeyMeta {
		return foreignKeyMeta{Name: "FK_" + column, Columns: []string{column}, ReferencedTable: table, ReferencedColumns: []string{"Id"}, DeleteRule: rule}
	}
	const refuses = "NO ACTION"
	tables := deleteFixtureTables()
	// A project names its client and its supplier, and the database follows neither.
	tables["Projects"].ForeignKeys = []foreignKeyMeta{to("ClientId", "Clients", refuses), to("SupplierId", "Suppliers", refuses)}
	// A task goes with its project and with the task it is a part of, and names a task
	// that blocks it.
	tables["Tasks"].ForeignKeys = []foreignKeyMeta{
		to("ProjectId", "Projects", cascadeDeleteAction), to("ParentTaskId", "Tasks", cascadeDeleteAction), to("BlockedById", "Tasks", refuses),
	}
	// A note is interleaved in its project with the delete declared, and holds a
	// foreign key on the same column that says nothing of one.
	tables["ProjectNotes"].OnDeleteCascade = true
	tables["ProjectNotes"].ForeignKeys = []foreignKeyMeta{to("Id", "Projects", refuses)}
	// An invoice names its client, and goes with its project.
	tables["Invoices"].ForeignKeys = []foreignKeyMeta{to("ClientId", "Clients", refuses), to("ProjectId", "Projects", cascadeDeleteAction)}
	// A ledger can be under another ledger.
	tables["Ledgers"].ForeignKeys = []foreignKeyMeta{to("ParentId", "Ledgers", refuses)}
	// Receipts name a client, and no resource declares the table.
	tables["Receipts"] = &tableMetadata{PkCount: 1, ForeignKeys: []foreignKeyMeta{to("ClientId", "Clients", refuses)}, Columns: map[string]columnMeta{"Id": {IsPrimaryKey: true}}}
	c.tableMap = tables

	if err := c.resolveDeleteTrees(resourceList(byName)); err != nil {
		t.Fatalf("resolveDeleteTrees() error = %v", err)
	}

	type kindUnder struct {
		Table       string
		Paths       [][]deleteLink
		RemovedWith []string
	}
	want := map[string][]kindUnder{
		"Client": {
			{Table: "Invoices", Paths: [][]deleteLink{{link("ClientId", "Clients")}}, RemovedWith: []string{"Projects"}},
			{Table: "Projects", Paths: [][]deleteLink{{link("ClientId", "Clients")}}},
		},
		"Task":   {{Table: "TaskComments", Paths: [][]deleteLink{{link("TaskId", "Tasks")}}}},
		"Ledger": {{Table: "Ledgers", Paths: [][]deleteLink{{link("ParentId", "Ledgers")}}}},
	}
	got := make(map[string][]kindUnder)
	for name, res := range byName {
		if res.DeleteTree == nil {
			continue
		}
		if len(res.DeleteTree.Taken)+len(res.DeleteTree.RefusedBy) != 0 {
			t.Errorf("%s declares nothing, and its description lists kinds taken or refusing", name)
		}
		for _, kind := range res.DeleteTree.Under {
			got[name] = append(got[name], kindUnder{Table: kind.Table, Paths: kind.Paths, RemovedWith: kind.RemovedWith})
		}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the kinds under each record mismatch (-want +got):\n%s", diff)
	}

	r := &resourceGenerator{client: c}
	output, err := r.generateTemplateOutput("resourceFileTemplate", resourceFileTemplate, &resourceFileData{
		Source:   "fixture",
		Package:  "resources",
		Resource: byName["Client"],
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	rendered := string(output)
	for _, wantLine := range []string{
		"// DeleteTree describes what keeps a Client from being deleted:",
		"func (Client) DeleteTree() *resource.DeleteTree {",
		"Under: []resource.DeleteKind{",
		`RemovedWith: []string{"Projects"},`,
	} {
		if !strings.Contains(rendered, wantLine) {
			t.Errorf("rendered Client is missing %q", wantLine)
		}
	}
	for _, unwanted := range []string{"Taken: []resource.DeleteKind{", "RefusedBy: []resource.DeleteKind{"} {
		if strings.Contains(rendered, unwanted) {
			t.Errorf("rendered Client holds %q, though it declares nothing", unwanted)
		}
	}
}

// Test_deleteLink_mayBeEmpty pins which steps a row can be taken off without being
// deleted: a foreign key with a column that takes NULL and is no part of the row's key,
// alone or beside a column that takes none. A foreign key whose columns all take a
// value is not one, nor is one whose only column that takes NULL is part of the row's
// key, nor an interleave, with or without a foreign key on its columns. A table's
// foreign key to itself is one by the same rule. The description carries the fact on
// each step, for a kind taken with the record as for a kind under it, and the record's
// file writes it for those steps alone.
func Test_deleteLink_mayBeEmpty(t *testing.T) {
	t.Parallel()

	key := func(ordinal int64) columnMeta {
		return columnMeta{IsPrimaryKey: true, KeyOrdinalPosition: ordinal}
	}
	empty := columnMeta{IsNullable: true}
	to := func(table string, columns, references []string) foreignKeyMeta {
		return foreignKeyMeta{Name: "FK_" + strings.Join(columns, ""), Columns: columns, ReferencedTable: table, ReferencedColumns: references, DeleteRule: "NO ACTION"}
	}
	id := []string{"Id"}
	both := []string{"TenantId", "Id"}

	c := &client{tableMap: map[string]*tableMetadata{
		"Hangars": {PkCount: 2, Columns: map[string]columnMeta{"TenantId": key(0), "Id": key(1)}},
		"Crews":   {PkCount: 1, Columns: map[string]columnMeta{"Id": key(0)}},
		"Ships": {
			PkCount: 2,
			Columns: map[string]columnMeta{
				"TenantId": key(0), "Id": key(1),
				"HomeHangarId": {}, "AwayHangarId": empty, "CrewId": empty, "BuiltByCrewId": {},
				"LeadShipId": empty, "TwinShipId": {},
			},
			ForeignKeys: []foreignKeyMeta{
				to("Hangars", []string{"TenantId", "HomeHangarId"}, both),
				to("Hangars", []string{"TenantId", "AwayHangarId"}, both),
				to("Crews", []string{"CrewId"}, id),
				to("Crews", []string{"BuiltByCrewId"}, id),
				to("Ships", []string{"TenantId", "LeadShipId"}, both),
				to("Ships", []string{"TenantId", "TwinShipId"}, both),
			},
		},
		// A berth is keyed by the crew that holds it, in a key column that takes NULL.
		"Berths": {
			PkCount:     2,
			Columns:     map[string]columnMeta{"CrewId": {IsPrimaryKey: true, IsNullable: true}, "Slot": key(1)},
			ForeignKeys: []foreignKeyMeta{to("Crews", []string{"CrewId"}, id)},
		},
		// A log is interleaved in its ship, and a second table holds a foreign key on the
		// interleave's own columns as well.
		"ShipLogs": {
			PkCount: 3, IsInterleaved: true, ParentTable: "Ships",
			Columns: map[string]columnMeta{"TenantId": key(0), "Id": key(1), "Sequence": key(2)},
		},
		"ShipMarks": {
			PkCount: 3, IsInterleaved: true, ParentTable: "Ships",
			Columns:     map[string]columnMeta{"TenantId": key(0), "Id": key(1), "Mark": key(2)},
			ForeignKeys: []foreignKeyMeta{to("Ships", both, both)},
		},
	}}

	step := func(table string, columns, references []string, mayBeEmpty bool) deleteLink {
		return deleteLink{Columns: columns, Table: table, References: references, MayBeEmpty: mayBeEmpty}
	}
	wantLinks := map[string][]deleteLink{
		"Ships": {
			step("Hangars", []string{"TenantId", "HomeHangarId"}, both, false),
			step("Hangars", []string{"TenantId", "AwayHangarId"}, both, true),
			step("Crews", []string{"CrewId"}, id, true),
			step("Crews", []string{"BuiltByCrewId"}, id, false),
		},
		"Berths":    {step("Crews", []string{"CrewId"}, id, false)},
		"ShipLogs":  {step("Ships", both, both, false)},
		"ShipMarks": {step("Ships", both, both, false)},
	}
	if diff := cmp.Diff(wantLinks, c.schemaLinks()); diff != "" {
		t.Errorf("schemaLinks() mismatch (-want +got):\n%s", diff)
	}
	wantSelf := map[string][]deleteLink{
		"Ships": {
			step("Ships", []string{"TenantId", "LeadShipId"}, both, true),
			step("Ships", []string{"TenantId", "TwinShipId"}, both, false),
		},
	}
	if diff := cmp.Diff(wantSelf, c.selfLinks()); diff != "" {
		t.Errorf("selfLinks() mismatch (-want +got):\n%s", diff)
	}

	// The description of a record carries the fact on every step of every way: a project
	// that may be left with no client makes the way from a task, two steps down, one
	// that may be empty at its second step.
	fixture, byName := deleteFixtureResources(t)
	tables := deleteFixtureTables()
	clientID := tables["Projects"].Columns["ClientId"]
	clientID.IsNullable = true
	tables["Projects"].Columns["ClientId"] = clientID
	fixture.tableMap = tables
	if err := fixture.resolveDeleteTrees(resourceList(byName)); err != nil {
		t.Fatalf("resolveDeleteTrees() error = %v", err)
	}

	projectToClient := step("Clients", []string{"ClientId"}, id, true)
	invoiceToClient := step("Clients", []string{"ClientId"}, id, false)
	tree := byName["Client"].DeleteTree
	paths := func(kinds []*deleteKind) map[string][][]deleteLink {
		byTable := make(map[string][][]deleteLink, len(kinds))
		for _, kind := range kinds {
			byTable[kind.Table] = kind.Paths
		}

		return byTable
	}
	wantUnder := map[string][][]deleteLink{"Invoices": {{invoiceToClient}}, "Projects": {{projectToClient}}}
	if diff := cmp.Diff(wantUnder, paths(tree.Under)); diff != "" {
		t.Errorf("the ways of the kinds under a client mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([][]deleteLink{{link("ProjectId", "Projects"), projectToClient}}, paths(tree.Taken)["Tasks"]); diff != "" {
		t.Errorf("the way of a task up to its client mismatch (-want +got):\n%s", diff)
	}

	var b strings.Builder
	writeDeleteKinds(&b, "Under", tree.Under)
	rendered := b.String()
	for _, wantLine := range []string{
		`{Columns: []string{"ClientId"}, Table: "Clients", References: []string{"Id"}, MayBeEmpty: true},`,
		`{Columns: []string{"ClientId"}, Table: "Clients", References: []string{"Id"}},`,
	} {
		if !strings.Contains(rendered, wantLine) {
			t.Errorf("the rendered kinds are missing %q in:\n%s", wantLine, rendered)
		}
	}
	if got := strings.Count(rendered, "MayBeEmpty"); got != 1 {
		t.Errorf("the rendered kinds name MayBeEmpty %d times, want once, on the project's step alone:\n%s", got, rendered)
	}
}

// Test_removedWith pins the tables whose row's delete makes the database remove a
// table's rows: every table up from it by links the database follows, however many
// steps, the table itself where a row goes with the row of its own kind it is under,
// and no table reached by a link the database does not follow.
func Test_removedWith(t *testing.T) {
	t.Parallel()

	followed := func(column, table string) deleteLink {
		l := link(column, table)
		l.Cascades = true

		return l
	}
	cascading := map[string][]deleteLink{
		"Expenses": {followed("SortieId", "Sorties")},
		"Sorties":  {followed("MissionId", "Missions")},
		"Missions": {followed("SectorId", "Sectors")},
	}
	self := map[string][]deleteLink{
		"Expenses": {followed("SplitFromId", "Expenses")},
		"Sorties":  {link("RepeatsId", "Sorties")},
	}

	if diff := cmp.Diff([]string{"Expenses", "Missions", "Sectors", "Sorties"}, removedWith(cascading, self, "Expenses")); diff != "" {
		t.Errorf("removedWith(Expenses) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"Missions", "Sectors"}, removedWith(cascading, self, "Sorties")); diff != "" {
		t.Errorf("removedWith(Sorties) mismatch (-want +got):\n%s", diff)
	}
	if got := removedWith(cascading, self, "Sectors"); len(got) != 0 {
		t.Errorf("removedWith(Sectors) = %v, want none", got)
	}
}

// Test_deleteCascade_refusals pins what a @deleteCascade is held to: a kind under the
// record that neither list names is refused naming the kind and the record, and so is a
// name that is no resource, a kind that is not under the record, a kind that can only
// be under a refusing kind, and a declaration on the tenant record. A kind under a
// refusing kind alone needs no place in a list.
func Test_deleteCascade_refusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		decl   *deleteCascadeDecl
		tenant bool
		// schema changes the fixture's tables before the declaration is held to them.
		schema        func(c *client)
		wantErr       []string
		wantTaken     []string
		wantRefusedBy []string
	}{
		{
			name:    "the record's own kind is refused where no row of it is under another",
			decl:    &deleteCascadeDecl{Taken: []string{"Project", "Task", "TaskComment", "ProjectNote", "Client"}, RefusedBy: []string{"Invoice"}},
			wantErr: []string{"@deleteCascade on Client names Client, but no foreign key puts a Client under a Client. Remove Client from the list."},
		},
		{
			name: "a record with rows under rows of its own kind has to name its kind",
			decl: &deleteCascadeDecl{Taken: []string{"Project", "Task", "TaskComment", "ProjectNote"}, RefusedBy: []string{"Invoice"}},
			schema: func(c *client) {
				underItsOwnKind(c, "Clients", "ParentClientId")
			},
			wantErr: []string{"@deleteCascade on Client: a Client is under a Client (Clients.ParentClientId to Clients.Id)", "neither as taken nor under refusedBy", "List Client to delete it with the Client"},
		},
		{
			name: "the record's own kind is taken last, after everything under its rows",
			decl: &deleteCascadeDecl{Taken: []string{"Client", "Project", "Task", "TaskComment", "ProjectNote"}, RefusedBy: []string{"Invoice"}},
			schema: func(c *client) {
				underItsOwnKind(c, "Clients", "ParentClientId")
			},
			wantTaken:     []string{"TaskComments", "ProjectNotes", "Tasks", "Projects", "Clients"},
			wantRefusedBy: []string{"Invoices"},
		},
		{
			name: "the record's own kind may refuse the delete",
			decl: &deleteCascadeDecl{Taken: []string{"Project", "Task", "TaskComment", "ProjectNote"}, RefusedBy: []string{"Invoice", "Client"}},
			schema: func(c *client) {
				underItsOwnKind(c, "Clients", "ParentClientId")
			},
			wantTaken:     []string{"TaskComments", "ProjectNotes", "Tasks", "Projects"},
			wantRefusedBy: []string{"Invoices", "Clients"},
		},
		{
			name:    "a kind under the record in neither list is refused, naming the kind and the record",
			decl:    &deleteCascadeDecl{Taken: []string{"Project", "Task", "TaskComment"}, RefusedBy: []string{"Invoice"}},
			wantErr: []string{"@deleteCascade on Client: a ProjectNote is under a Client (ProjectNotes.Id to Projects.Id, then Projects.ClientId to Clients.Id)", "neither as taken nor under refusedBy", "List ProjectNote to delete it with the Client"},
		},
		{
			name:    "a kind two steps under the record is refused the same way",
			decl:    &deleteCascadeDecl{Taken: []string{"Project", "Task", "ProjectNote"}, RefusedBy: []string{"Invoice"}},
			wantErr: []string{"a TaskComment is under a Client (TaskComments.TaskId to Tasks.Id, then Tasks.ProjectId to Projects.Id, then Projects.ClientId to Clients.Id)"},
		},
		{
			name:    "a name that is no resource is refused",
			decl:    &deleteCascadeDecl{Taken: []string{"Projekt"}},
			wantErr: []string{"@deleteCascade on Client names Projekt, which is not a resource of this package"},
		},
		{
			name:    "a kind that is not under the record is refused",
			decl:    &deleteCascadeDecl{Taken: []string{"Project", "Task", "TaskComment", "ProjectNote", "Supplier"}, RefusedBy: []string{"Invoice"}},
			wantErr: []string{"@deleteCascade on Client names Supplier, but no foreign key or interleave puts a Supplier under a Client"},
		},
		{
			name:          "what is under a refusing kind alone needs no place in a list",
			decl:          &deleteCascadeDecl{Taken: []string{"Invoice"}, RefusedBy: []string{"Project"}},
			wantTaken:     []string{"Invoices"},
			wantRefusedBy: []string{"Projects"},
		},
		{
			name:    "a taken kind that is only under a refusing kind is refused",
			decl:    &deleteCascadeDecl{Taken: []string{"Invoice", "Task"}, RefusedBy: []string{"Project"}},
			wantErr: []string{"@deleteCascade on Client takes Task, but a Task is under a Client only through a kind that refuses the delete"},
		},
		{
			name:    "the tenant record declares none: the library declares its delete",
			decl:    &deleteCascadeDecl{Taken: []string{"Project"}},
			tenant:  true,
			wantErr: []string{"Client is the tenant record, and the library declares what a tenant's delete takes", "Remove @deleteCascade from Client."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, byName := deleteFixtureResources(t)
			client := byName["Client"]
			client.deleteCascade, client.DeleteTree, client.IsTenant = tt.decl, nil, tt.tenant
			if tt.schema != nil {
				tt.schema(c)
			}

			err := c.resolveDeleteTrees(resourceList(byName))
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("resolveDeleteTrees() error = %v", err)
				}
				if client.DeleteTree == nil {
					t.Fatal("Client has no description after resolveDeleteTrees()")
				}
				var taken, refusedBy []string
				for _, kind := range client.DeleteTree.Taken {
					taken = append(taken, kind.Table)
				}
				for _, kind := range client.DeleteTree.RefusedBy {
					refusedBy = append(refusedBy, kind.Table)
				}
				if diff := cmp.Diff(tt.wantTaken, taken); diff != "" {
					t.Errorf("taken kinds mismatch (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(tt.wantRefusedBy, refusedBy); diff != "" {
					t.Errorf("refusing kinds mismatch (-want +got):\n%s", diff)
				}

				return
			}
			if err == nil {
				t.Fatalf("resolveDeleteTrees() error = nil, want one containing %q", tt.wantErr)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("resolveDeleteTrees() error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

// underItsOwnKind puts a row of the table under another row of the same table in the
// fixture's schema: a foreign key on the column, which may be left empty, to the
// table's own Id.
func underItsOwnKind(c *client, table, column string) {
	meta := c.tableMap[table]
	meta.Columns[column] = columnMeta{IsForeignKey: true, IsNullable: true, ReferencedTable: table, ReferencedColumn: "Id", DeleteRule: "NO ACTION", OrdinalPosition: 9}
	meta.ForeignKeys = append(meta.ForeignKeys, foreignKeyMeta{Name: "FK_" + column, Columns: []string{column}, ReferencedTable: table, ReferencedColumns: []string{"Id"}, DeleteRule: "NO ACTION"})
}

// Test_deleteCascade_rowsUnderRowsOfTheirKind pins what the description says of a row
// under a row of its own kind, and of how a taken row is deleted. The record's own kind
// is a taken kind like any other, last in delete order, with one way up for each link
// of the record to itself. A taken kind with a link to itself carries it (Within), so
// the delete finds a row under a row of the kind. Every taken kind is written with the
// delete of one of its rows (Rows): the kind's generated delete patch, with its rule
// where it declares one, armed against the caller unless the call is trusted.
func Test_deleteCascade_rowsUnderRowsOfTheirKind(t *testing.T) {
	t.Parallel()

	c, byName := deleteFixtureResources(t)
	client := byName["Client"]
	client.deleteCascade = &deleteCascadeDecl{Taken: []string{"Client", "Project", "Task", "TaskComment", "ProjectNote", "Invoice"}}
	client.DeleteTree = nil
	underItsOwnKind(c, "Clients", "ParentClientId")
	underItsOwnKind(c, "Tasks", "ParentTaskId")

	if err := c.resolveDeleteTrees(resourceList(byName)); err != nil {
		t.Fatalf("resolveDeleteTrees() error = %v", err)
	}

	within := func(column, table string) deleteLink {
		return deleteLink{Columns: []string{column}, Table: table, References: []string{"Id"}, MayBeEmpty: true}
	}
	want := []*deleteKind{
		{
			Name: "TaskComment", Table: "TaskComments", One: "task comment", Many: "task comments", Key: []string{"Id"},
			Paths: [][]deleteLink{{link("TaskId", "Tasks"), link("ProjectId", "Projects"), link("ClientId", "Clients")}}, Deletes: true,
		},
		{
			Name: "ProjectNote", Table: "ProjectNotes", One: "project note", Many: "project notes", Key: []string{"Id", "Sequence"},
			Paths: [][]deleteLink{{link("Id", "Projects"), link("ClientId", "Clients")}}, Deletes: true,
		},
		{
			Name: "Task", Table: "Tasks", One: "task", Many: "tasks", Key: []string{"Id"}, IndexEntries: 1,
			Paths:  [][]deleteLink{{link("ProjectId", "Projects"), link("ClientId", "Clients")}},
			Within: []deleteLink{within("ParentTaskId", "Tasks")}, Deletes: true,
		},
		{
			Name: "Invoice", Table: "Invoices", One: "invoice", Many: "invoices", Key: []string{"Id"},
			Paths: [][]deleteLink{{link("ClientId", "Clients")}}, Deletes: true, Rule: true,
		},
		{
			Name: "Project", Table: "Projects", One: "project", Many: "projects", Key: []string{"Id"},
			Paths: [][]deleteLink{{link("ClientId", "Clients")}}, Deletes: true,
		},
		{
			Name: "Client", Table: "Clients", One: "client", Many: "clients", Key: []string{"Id"},
			Paths: [][]deleteLink{{within("ParentClientId", "Clients")}}, Deletes: true,
		},
	}
	if diff := cmp.Diff(want, client.DeleteTree.Taken); diff != "" {
		t.Fatalf("Client's taken kinds mismatch (-want +got):\n%s", diff)
	}

	r := &resourceGenerator{client: c}
	output, err := r.generateTemplateOutput("resourceFileTemplate", resourceFileTemplate, &resourceFileData{
		Source:   "fixture",
		Package:  "resources",
		Resource: client,
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	rendered := strings.Join(strings.Fields(string(output)), " ")
	for _, wantText := range []string{
		`Within: []resource.DeleteLink{ {Columns: []string{"ParentTaskId"}, Table: "Tasks", References: []string{"Id"}, MayBeEmpty: true}, },`,
		`Paths: []resource.DeletePath{ { {Columns: []string{"ParentClientId"}, Table: "Clients", References: []string{"Id"}, MayBeEmpty: true}, }, },`,
		// A kind with no rule: its delete patch, armed against the caller unless the
		// call is trusted.
		`Rows: resource.NewTakenRows(func(patchSet *resource.PatchSet[Task], caller *resource.Caller) { patch := &TaskDeletePatch{patchSet: patchSet} if !caller.IsZero() { patch.Enforce(caller) } }),`,
		// A kind with a rule registers it first.
		`Rows: resource.NewTakenRows(func(patchSet *resource.PatchSet[Invoice], caller *resource.Caller) { patch := &InvoiceDeletePatch{patchSet: patchSet} patch.registerValidateFunc() if !caller.IsZero() { patch.Enforce(caller) } }),`,
		`Rows: resource.NewTakenRows(func(patchSet *resource.PatchSet[Client], caller *resource.Caller) {`,
	} {
		if !strings.Contains(rendered, wantText) {
			t.Errorf("rendered Client is missing %q", wantText)
		}
	}

	// A record that declares nothing asks no one to take its rows.
	project, err := r.generateTemplateOutput("resourceFileTemplate", resourceFileTemplate, &resourceFileData{
		Source:   "fixture",
		Package:  "resources",
		Resource: byName["Project"],
	})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	for _, unwanted := range []string{"WithEverythingUnder", "NewTakenRows"} {
		if strings.Contains(string(project), unwanted) {
			t.Errorf("rendered Project carries %s, though it declares no @%s", unwanted, deleteCascadeKeyword)
		}
	}
}

// Test_parseDeleteCascade pins the reading of the annotation: the kinds taken, then the
// kinds after refusedBy, and a kind named once.
func Test_parseDeleteCascade(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		arg     string
		want    *deleteCascadeDecl
		wantErr string
	}{
		{
			name: "kinds taken and kinds that refuse",
			arg:  "Project, Task, TaskComment, refusedBy: Invoice, Payment",
			want: &deleteCascadeDecl{Taken: []string{"Project", "Task", "TaskComment"}, RefusedBy: []string{"Invoice", "Payment"}},
		},
		{
			name: "kinds taken alone",
			arg:  "Project",
			want: &deleteCascadeDecl{Taken: []string{"Project"}},
		},
		{
			name:    "a list of refusing kinds alone takes nothing and is refused",
			arg:     "refusedBy: Invoice",
			wantErr: "expected at least 1 positional argument(s)",
		},
		{
			name:    "a kind in both lists is refused",
			arg:     "Project, refusedBy: Project",
			wantErr: "@deleteCascade on Client names Project twice",
		},
		{
			name: "the record's own kind is read like any other: the schema says whether a row of it is under another",
			arg:  "Project, Client",
			want: &deleteCascadeDecl{Taken: []string{"Project", "Client"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseDeleteCascade("Client", genlang.Arg(tt.arg))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseDeleteCascade(%q) error = %v, want one containing %q", tt.arg, err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("parseDeleteCascade(%q) error = %v", tt.arg, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("parseDeleteCascade(%q) mismatch (-want +got):\n%s", tt.arg, diff)
			}
		})
	}
}

// Test_tenantDeleteTree pins the tenant record's description, which the library
// declares: every kind of the application's rows that carry the tenant's key, by a
// foreign key or as the column a tenant-scoped resource is read by, or lead to a row
// that does, deepest first, and no kind that refuses.
//
// The tenant's delete takes those rows in code: each kind is written with the delete of
// one of its rows, which asks the kind's delete rule where it declares one and is never
// armed against the caller, since the grant to delete a tenant answers for every row
// under it. A kind with a row under a row of its own kind carries that link, and the
// tenant's delete patch can be told to take everything under.
func Test_tenantDeleteTree(t *testing.T) {
	t.Parallel()

	c, byName := deleteFixtureResources(t)
	client, ledger := byName["Client"], byName["Ledger"]
	client.deleteCascade, client.DeleteTree, client.IsTenant = nil, nil, true
	ledger.DomainBinding = &domainBinding{Anchor: ledger.fieldNamed("ClientID")}
	underItsOwnKind(c, "Tasks", "ParentTaskId")

	if err := c.resolveDeleteTrees(resourceList(byName)); err != nil {
		t.Fatalf("resolveDeleteTrees() error = %v", err)
	}
	if client.DeleteTree == nil {
		t.Fatal("the tenant record has no description after resolveDeleteTrees()")
	}

	type kindPaths struct {
		Table string
		Paths [][]deleteLink
	}
	var got []kindPaths
	for _, kind := range client.DeleteTree.Taken {
		got = append(got, kindPaths{Table: kind.Table, Paths: kind.Paths})
	}
	want := []kindPaths{
		{Table: "TaskComments", Paths: [][]deleteLink{{link("TaskId", "Tasks"), link("ProjectId", "Projects"), link("ClientId", "Clients")}}},
		{Table: "ProjectNotes", Paths: [][]deleteLink{{link("Id", "Projects"), link("ClientId", "Clients")}}},
		{Table: "Tasks", Paths: [][]deleteLink{{link("ProjectId", "Projects"), link("ClientId", "Clients")}}},
		{Table: "Invoices", Paths: [][]deleteLink{{link("ClientId", "Clients")}}},
		{Table: "Ledgers", Paths: [][]deleteLink{{link("ClientId", "Clients")}}},
		{Table: "Projects", Paths: [][]deleteLink{{link("ClientId", "Clients")}}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the tenant's taken kinds mismatch (-want +got):\n%s", diff)
	}
	if len(client.DeleteTree.RefusedBy) != 0 {
		t.Errorf("the tenant's description lists %d kinds that refuse its delete, want none", len(client.DeleteTree.RefusedBy))
	}

	type kindDelete struct {
		Table                  string
		Deletes, Rule, Trusted bool
		Within                 []deleteLink
	}
	var gotDeletes []kindDelete
	for _, kind := range client.DeleteTree.Taken {
		gotDeletes = append(gotDeletes, kindDelete{Table: kind.Table, Deletes: kind.Deletes, Rule: kind.Rule, Trusted: kind.Trusted, Within: kind.Within})
	}
	wantDeletes := []kindDelete{
		{Table: "TaskComments", Deletes: true, Trusted: true},
		{Table: "ProjectNotes", Deletes: true, Trusted: true},
		{
			Table: "Tasks", Deletes: true, Trusted: true,
			Within: []deleteLink{{Columns: []string{"ParentTaskId"}, Table: "Tasks", References: []string{"Id"}, MayBeEmpty: true}},
		},
		{Table: "Invoices", Deletes: true, Rule: true, Trusted: true},
		{Table: "Ledgers", Deletes: true, Trusted: true},
		{Table: "Projects", Deletes: true, Trusted: true},
	}
	if diff := cmp.Diff(wantDeletes, gotDeletes); diff != "" {
		t.Errorf("how the tenant's delete takes each kind mismatch (-want +got):\n%s", diff)
	}
	if !client.TakesRowsUnder() {
		t.Error("TakesRowsUnder() = false for the tenant record, want true: its delete patch is told to take what is under the tenant")
	}

	literal := client.DeleteTreeLiteral()
	for _, want := range []string{
		// A kind with a delete rule registers it, and takes no caller.
		"Rows: resource.NewTakenRows(func(patchSet *resource.PatchSet[Invoice], _ *resource.Caller) {\npatch := &InvoiceDeletePatch{patchSet: patchSet}\npatch.registerValidateFunc()\n}),",
		// A kind without one has nothing to arm.
		"Rows: resource.NewTakenRows(func(*resource.PatchSet[Project], *resource.Caller) {}),",
		"Within: []resource.DeleteLink{\n{Columns: []string{\"ParentTaskId\"}, Table: \"Tasks\", References: []string{\"Id\"}, MayBeEmpty: true},\n},",
	} {
		if !strings.Contains(literal, want) {
			t.Errorf("the tenant's description as written lacks %q:\n%s", want, literal)
		}
	}
	if strings.Contains(literal, "Enforce(") {
		t.Errorf("the tenant's description arms a taken row's delete against the caller, want none armed:\n%s", literal)
	}
}

// Test_tableMetadata_foreignKeys pins how the schema read keeps a table's foreign keys
// whole: a key of two columns is one key with its columns in order, two keys to one
// table stay two, and a key whose referenced columns the read could not match has no
// referenced table, so nothing follows it. A key declared ON DELETE CASCADE is a step
// the database follows.
func Test_tableMetadata_foreignKeys(t *testing.T) {
	t.Parallel()

	text := func(value string) *string {
		return &value
	}

	table := &tableMetadata{Columns: map[string]columnMeta{
		"TenantId": {IsPrimaryKey: true, KeyOrdinalPosition: 0},
		"Id":       {IsPrimaryKey: true, KeyOrdinalPosition: 1},
	}}
	for _, row := range []foreignKeySchemaResult{
		{TableName: "Ships", ConstraintName: "FK_Away", ColumnName: "AwayHangarId", ReferencedTable: text("Hangars"), ReferencedColumn: text("Id"), DeleteRule: text("NO ACTION")},
		{TableName: "Ships", ConstraintName: "FK_Home", ColumnName: "TenantId", ReferencedTable: text("Hangars"), ReferencedColumn: text("TenantId"), DeleteRule: text("CASCADE")},
		{TableName: "Ships", ConstraintName: "FK_Home", ColumnName: "HomeHangarId", ReferencedTable: text("Hangars"), ReferencedColumn: text("Id"), DeleteRule: text("CASCADE")},
		{TableName: "Ships", ConstraintName: "FK_Tag", ColumnName: "TagName", DeleteRule: text("NO ACTION")},
	} {
		table.addForeignKeyResult(&row)
	}

	want := []foreignKeyMeta{
		{Name: "FK_Away", Columns: []string{"AwayHangarId"}, ReferencedTable: "Hangars", ReferencedColumns: []string{"Id"}, DeleteRule: "NO ACTION"},
		{Name: "FK_Home", Columns: []string{"TenantId", "HomeHangarId"}, ReferencedTable: "Hangars", ReferencedColumns: []string{"TenantId", "Id"}, DeleteRule: "CASCADE"},
		{Name: "FK_Tag", Columns: []string{"TagName"}, DeleteRule: "NO ACTION"},
	}
	if diff := cmp.Diff(want, table.ForeignKeys); diff != "" {
		t.Errorf("foreign keys mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"TenantId", "Id"}, table.keyColumns()); diff != "" {
		t.Errorf("keyColumns() mismatch (-want +got):\n%s", diff)
	}

	c := &client{tableMap: map[string]*tableMetadata{"Ships": table, "Hangars": {Columns: map[string]columnMeta{}}}}
	wantLinks := []deleteLink{
		{Columns: []string{"AwayHangarId"}, Table: "Hangars", References: []string{"Id"}},
		{Columns: []string{"TenantId", "HomeHangarId"}, Table: "Hangars", References: []string{"TenantId", "Id"}, Cascades: true},
	}
	if diff := cmp.Diff(wantLinks, c.schemaLinks()["Ships"]); diff != "" {
		t.Errorf("schemaLinks() mismatch (-want +got):\n%s", diff)
	}
}
