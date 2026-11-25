package generation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/generation/parser/genlang"
	"github.com/impulseframework/impulse/resource"
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
		"func (p *ClientDeletePatch) Left() *resource.DeleteLeft {",
		"func (p *ClientDeletePatch) DeleteAlso(kinds ...resource.TakenKind) *ClientDeletePatch {",
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

	// Code hands kinds to the delete of a record that takes rows; a record that takes
	// nothing is handed none.
	for name, want := range map[string]bool{"Client": true, "Supplier": false} {
		output, err := r.generateTemplateOutput("resourceFileTemplate", resourceFileTemplate, &resourceFileData{Source: "fixture", Package: "resources", Resource: byName[name]})
		if err != nil {
			t.Fatalf("generateTemplateOutput(%s) error = %v", name, err)
		}
		if got := strings.Contains(string(output), "func (p *"+name+"DeletePatch) DeleteAlso(kinds ...resource.TakenKind) *"+name+"DeletePatch {"); got != want {
			t.Errorf("rendered %s carries DeleteAlso = %t, want %t", name, got, want)
		}
	}
}

// bareTenant is the delete fixture with Supplier as the tenant record: no resource holds
// a supplier's key or leads to one, so no kind of row is under a tenant.
func bareTenant(t *testing.T) (c *client, byName map[string]*resourceInfo) {
	t.Helper()

	c, byName = deleteFixtureResources(t)
	byName["Supplier"].IsTenant = true
	for _, res := range byName {
		res.DeleteTree = nil
	}
	if err := c.resolveDeleteTrees(resourceList(byName)); err != nil {
		t.Fatalf("resolveDeleteTrees() error = %v", err)
	}

	return c, byName
}

// Test_deleteCascade_bareTenant pins the tenant record of an application with no kind of
// row under a tenant: its description lists no taken kind, and its delete takes rows all
// the same, the kinds handed to it, so its delete patch carries WithEverythingUnder, Left
// and DeleteAlso.
func Test_deleteCascade_bareTenant(t *testing.T) {
	t.Parallel()

	c, byName := bareTenant(t)
	supplier := byName["Supplier"]
	if supplier.DeleteTree == nil || len(supplier.DeleteTree.Taken) != 0 {
		t.Fatalf("the tenant record's description = %+v, want one that lists no taken kind", supplier.DeleteTree)
	}
	if !supplier.TakesRowsUnder() {
		t.Error("TakesRowsUnder() = false for the tenant record with nothing under a tenant, want true")
	}

	r := &resourceGenerator{client: c}
	output, err := r.generateTemplateOutput("resourceFileTemplate", resourceFileTemplate, &resourceFileData{Source: "fixture", Package: "resources", Resource: supplier})
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	for _, want := range []string{
		"func (p *SupplierDeletePatch) WithEverythingUnder() *SupplierDeletePatch {",
		"// (DeleteAlso). The grant to delete the Supplier answers for them all.",
		"func (p *SupplierDeletePatch) Left() *resource.DeleteLeft {",
		"func (p *SupplierDeletePatch) DeleteAlso(kinds ...resource.TakenKind) *SupplierDeletePatch {",
	} {
		if !strings.Contains(string(output), want) {
			t.Errorf("rendered Supplier, the tenant record, lacks %q", want)
		}
	}
}

// Test_patchTemplates_deleteLeft pins the answer of a delete larger than one save: the
// patch handler of a record that takes rows and the dispatcher that serves one keep what
// each delete left (the delete patch's Left), summed and reset with the transaction, and
// once the commit has landed and open pages are told answer 202 with it. A record that
// takes no rows carries none of it.
func Test_patchTemplates_deleteLeft(t *testing.T) {
	t.Parallel()

	c, byName := deleteFixtureResources(t)
	_, bare := bareTenant(t)
	render := func(t *testing.T, res *resourceInfo) map[string]string {
		t.Helper()

		consolidated, err := c.generateTemplateOutput("consolidatedPatchTemplate", consolidatedPatchTemplate, consolidatedPatchData{
			Resources:       []*resourceInfo{res},
			GlobalCases:     []consolidatedCaseData{{resourceInfo: res, ResourcePackage: "resources", ReceiverName: "a"}},
			TakesRowsUnder:  res.TakesRowsUnder(),
			Package:         "app",
			ResourcePackage: "resources",
			ApplicationName: "App",
			ReceiverName:    "a",
			HandlerName:     "PatchResources",
		})
		if err != nil {
			t.Fatalf("generateTemplateOutput(consolidatedPatchTemplate) error = %v", err)
		}
		own, err := c.generateTemplateOutput("handler", patchTemplate, handlerContentData{
			ResourcePackage: "resources",
			Resource:        res,
			ApplicationName: "App",
			ReceiverName:    "a",
		})
		if err != nil {
			t.Fatalf("generateTemplateOutput(patchTemplate) error = %v", err)
		}

		return map[string]string{"the dispatcher": string(consolidated), "the resource's own handler": string(own)}
	}

	// A record that declares its cascade, and the tenant record of an application with
	// no kind of row under a tenant, whose delete takes the kinds handed to it.
	for name, res := range map[string]*resourceInfo{"Client": byName["Client"], "Supplier": bare["Supplier"]} {
		for handler, rendered := range render(t, res) {
			flat := strings.Join(strings.Fields(rendered), " ")
			for _, want := range []string{
				"var left *resource.DeleteLeft",
				"left = nil",
				"patch := resources.New" + name + "DeletePatchFromPatchSet(id, patchSet) if err := patch.Buffer(ctx, txn, eventSource); err != nil {",
				"if taken := patch.Left(); taken != nil { if left == nil { left = &resource.DeleteLeft{} } left.Deleted += taken.Deleted left.Left += taken.Left }",
				"if left != nil {",
				"return httpio.NewEncoder(w).StatusCodeWithBody(http.StatusAccepted, left)",
			} {
				if !strings.Contains(flat, want) {
					t.Errorf("%s of %s lacks %q:\n%s", handler, name, want, rendered)
				}
			}
			published := strings.Index(rendered, "live.Publish(")
			accepted := strings.Index(rendered, "StatusCodeWithBody(http.StatusAccepted, left)")
			answered := strings.LastIndex(rendered, "return httpio.NewEncoder(w).Ok(")
			if published < 0 || accepted < published || answered < accepted {
				t.Errorf("%s of %s answers out of order: open pages told at %d, the 202 at %d, the plain answer at %d:\n%s", handler, name, published, accepted, answered, rendered)
			}
		}
	}
	for handler, rendered := range render(t, byName["Invoice"]) {
		for _, unwanted := range []string{"DeleteLeft", "Left()", "StatusAccepted"} {
			if strings.Contains(rendered, unwanted) {
				t.Errorf("%s of a record that takes no rows holds %q:\n%s", handler, unwanted, rendered)
			}
		}
	}
}

// Test_deleteCascade_featureKind pins a kind behind a feature flag (@feature) that a
// record's @deleteCascade names: it is a taken kind like any other, in the same place,
// and nothing of its flag is in the record's description. A delete reads no flag: a flag
// turns a feature's routes and pages off and does not change what belongs to a record.
func Test_deleteCascade_featureKind(t *testing.T) {
	t.Parallel()

	c, byName := deleteFixtureResources(t)
	ungated := byName["Client"].DeleteTreeLiteral()
	byName["TaskComment"].Feature = &featureGate{Constant: "CommentThreads", Name: "comment_threads"}
	for _, res := range byName {
		res.DeleteTree = nil
	}
	if err := c.resolveDeleteTrees(resourceList(byName)); err != nil {
		t.Fatalf("resolveDeleteTrees() error = %v", err)
	}

	tree := byName["Client"].DeleteTree
	if len(tree.Taken) == 0 || tree.Taken[0].Table != "TaskComments" || !tree.Taken[0].Deletes {
		t.Errorf("the gated kind is not taken first, with its delete: %+v", tree.Taken)
	}
	gated := byName["Client"].DeleteTreeLiteral()
	if gated != ungated {
		t.Errorf("the description with the kind gated differs from the one without:\n%s\nwant\n%s", gated, ungated)
	}
	for _, flag := range []string{"CommentThreads", "comment_threads", "Feature"} {
		if strings.Contains(gated, flag) {
			t.Errorf("the description holds %q of the kind's flag:\n%s", flag, gated)
		}
	}
}

// Test_deleteWords pins the words a sentence about a delete calls a kind of record by:
// its name in words, one and many, in lower case, where a name that ends in an acronym
// keeps the acronym's letters where there are several.
func Test_deleteWords(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "rowwordfixture"))

	tests := []struct {
		name     string
		wantOne  string
		wantMany string
	}{
		{name: "ShipClass", wantOne: "ship class", wantMany: "ship classes"},
		{name: "PilotID", wantOne: "pilot id", wantMany: "pilot IDs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := structs[tt.name]
			if s == nil {
				t.Fatalf("struct %q not found in fixture package", tt.name)
			}
			one, many := (&client{}).deleteWords(&resourceInfo{TypeInfo: s.TypeInfo})
			if one != tt.wantOne || many != tt.wantMany {
				t.Errorf("deleteWords(%s) = %q and %q, want %q and %q", tt.name, one, many, tt.wantOne, tt.wantMany)
			}
		})
	}
}

// Test_kindsUnder pins which kinds refuse a record's plain delete, over a schema where
// the database removes some rows itself: a kind the database removes with the record is
// not one, whether by a foreign key that says so or by an interleave that does while a
// foreign key on the same columns says nothing; a kind under the record by a link the
// database does not follow is one; a row under a row of its own kind is one, unless the
// database follows a link of the table to itself; a table no resource declares is left
// out; and a read-only table (@enumerate) gets no description, whatever is under it.
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
		Table string
		Paths [][]deleteLink
	}
	want := map[string][]kindUnder{
		"Client": {
			{Table: "Invoices", Paths: [][]deleteLink{{link("ClientId", "Clients")}}},
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
			got[name] = append(got[name], kindUnder{Table: kind.Table, Paths: kind.Paths})
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
	meta.Columns[column] = columnMeta{IsForeignKey: true, IsNullable: true, ReferencedTable: table, ReferencedColumn: "Id", OrdinalPosition: 9}
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

// Test_deleteTree_tenant pins how a description says the tenant each row resolves to,
// so a delete reads no row of another tenant under a record: the tenant record's own
// row is its tenant, by its key column; a record or a kind with a @domain carries it,
// with the way through other rows where it has one; and a record or a kind with none
// carries nothing and writes no line. A kind's line follows its cost, as the record's
// does.
func Test_deleteTree_tenant(t *testing.T) {
	t.Parallel()

	toClient := []bindingHop{{Table: "Projects", JoinColumn: "Id", Column: "ClientId"}}
	throughProjects := &resource.DomainBindingData{Column: "ProjectId", Path: []resource.BindingHop{{Table: "Projects", JoinColumn: "Id", Column: "ClientId"}}}
	byClient := &resource.DomainBindingData{Column: "ClientId"}

	t.Run("the tenant record", func(t *testing.T) {
		t.Parallel()

		c, byName := deleteFixtureResources(t)
		client := byName["Client"]
		client.deleteCascade, client.DeleteTree, client.IsTenant = nil, nil, true
		for _, name := range []string{"Ledger", "Project", "Invoice"} {
			byName[name].DomainBinding = &domainBinding{Anchor: byName[name].fieldNamed("ClientID")}
		}
		task := byName["Task"]
		task.DomainBinding = &domainBinding{Anchor: task.fieldNamed("ProjectID"), Path: toClient}

		if err := c.resolveDeleteTrees(resourceList(byName)); err != nil {
			t.Fatalf("resolveDeleteTrees() error = %v", err)
		}
		if diff := cmp.Diff(&resource.DomainBindingData{Column: "Id"}, client.DeleteTree.Record.Tenant); diff != "" {
			t.Errorf("the tenant record's own tenant mismatch (-want +got):\n%s", diff)
		}
		got := make(map[string]*resource.DomainBindingData)
		for _, kind := range client.DeleteTree.Taken {
			got[kind.Table] = kind.Tenant
		}
		want := map[string]*resource.DomainBindingData{
			"TaskComments": nil, "ProjectNotes": nil, "Tasks": throughProjects,
			"Invoices": byClient, "Ledgers": byClient, "Projects": byClient,
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("the tenant of each kind the tenant's delete takes mismatch (-want +got):\n%s", diff)
		}

		literal := client.DeleteTreeLiteral()
		for _, want := range []string{
			"Cost: resource.DeleteCost{IndexEntries: 0, Files: 0},\nTenant: &resource.DomainBindingData{Column: \"Id\"},\nTaken:",
			"Resource: \"Tasks\",\nWords: resource.DeleteWords{One: \"task\", Many: \"tasks\"},\nKey: []string{\"Id\"},\nCost: resource.DeleteCost{IndexEntries: 1, Files: 0},\n" +
				"Tenant: &resource.DomainBindingData{Column: \"ProjectId\", Path: []resource.BindingHop{{Table: \"Projects\", JoinColumn: \"Id\", Column: \"ClientId\"}}},\nPaths:",
			"Resource: \"Ledgers\",\nWords: resource.DeleteWords{One: \"ledger\", Many: \"ledgers\"},\nKey: []string{\"Id\"},\nCost: resource.DeleteCost{IndexEntries: 0, Files: 0},\n" +
				"Tenant: &resource.DomainBindingData{Column: \"ClientId\"},\nPaths:",
			// A kind whose rows are no tenant's writes no line.
			"Resource: \"TaskComments\",\nWords: resource.DeleteWords{One: \"task comment\", Many: \"task comments\"},\nKey: []string{\"Id\"},\nCost: resource.DeleteCost{IndexEntries: 0, Files: 0},\nPaths:",
		} {
			if !strings.Contains(literal, want) {
				t.Errorf("the tenant's description as written lacks %q:\n%s", want, literal)
			}
		}
		// Every kind the tenant's delete takes is trusted.
		if got, want := strings.Count(literal, "Trusted: true,\n"), len(client.DeleteTree.Taken); got != want {
			t.Errorf("the tenant's description marks %d kinds trusted, want each of its %d:\n%s", got, want, literal)
		}
	})

	t.Run("a record under a tenant, and one that is no tenant's", func(t *testing.T) {
		t.Parallel()

		c, byName := deleteFixtureResources(t)
		for _, name := range []string{"Project", "Invoice"} {
			byName[name].DomainBinding = &domainBinding{Anchor: byName[name].fieldNamed("ClientID")}
		}
		task := byName["Task"]
		task.DomainBinding = &domainBinding{Anchor: task.fieldNamed("ProjectID"), Path: toClient}
		// The fixture's descriptions were written before the bindings were given.
		for _, res := range byName {
			res.DeleteTree = nil
		}

		if err := c.resolveDeleteTrees(resourceList(byName)); err != nil {
			t.Fatalf("resolveDeleteTrees() error = %v", err)
		}

		client := byName["Client"].DeleteTree
		if client.Record.Tenant != nil {
			t.Errorf("a record with no @domain carries the tenant %+v, want none", client.Record.Tenant)
		}
		tenants := func(kinds []*deleteKind) map[string]*resource.DomainBindingData {
			out := make(map[string]*resource.DomainBindingData, len(kinds))
			for _, kind := range kinds {
				out[kind.Table] = kind.Tenant
			}

			return out
		}
		if diff := cmp.Diff(map[string]*resource.DomainBindingData{"TaskComments": nil, "ProjectNotes": nil, "Tasks": throughProjects, "Projects": byClient}, tenants(client.Taken)); diff != "" {
			t.Errorf("the tenant of each kind the client's delete takes mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(map[string]*resource.DomainBindingData{"Invoices": byClient}, tenants(client.RefusedBy)); diff != "" {
			t.Errorf("the tenant of each kind that refuses the client's delete mismatch (-want +got):\n%s", diff)
		}
		if literal := byName["Client"].DeleteTreeLiteral(); !strings.Contains(literal, "Cost: resource.DeleteCost{IndexEntries: 0, Files: 0},\nTaken:") {
			t.Errorf("the description of a record that is no tenant's writes a tenant:\n%s", literal)
		}
		// No record but the tenant record's marks a kind trusted.
		for name, res := range byName {
			if res.DeleteTree != nil && strings.Contains(res.DeleteTreeLiteral(), "Trusted") {
				t.Errorf("the description of %s, which is not the tenant record, marks a kind trusted:\n%s", name, res.DeleteTreeLiteral())
			}
		}

		project := byName["Project"].DeleteTree
		if project == nil {
			t.Fatal("the project has no description after resolveDeleteTrees()")
		}
		if diff := cmp.Diff(byClient, project.Record.Tenant); diff != "" {
			t.Errorf("the project's own tenant mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(map[string]*resource.DomainBindingData{"ProjectNotes": nil, "Tasks": throughProjects}, tenants(project.Under)); diff != "" {
			t.Errorf("the tenant of each kind under the project mismatch (-want +got):\n%s", diff)
		}
		if literal := byName["Project"].DeleteTreeLiteral(); !strings.Contains(literal, "Cost: resource.DeleteCost{IndexEntries: 0, Files: 0},\nTenant: &resource.DomainBindingData{Column: \"ClientId\"},\nUnder:") {
			t.Errorf("the project's description as written lacks its tenant after its cost:\n%s", literal)
		}
	})
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

// Test_deleteTree_subjectKinds pins which taken kinds ask a record's questions as a
// removal (deleteKind.Subject, set by attachSubjectKinds once the run's last pass has
// run) and what the description writes for them. An armed kind whose record decides
// what a person may do registers the record's function after patch.Enforce(caller),
// inside the armed branch, and so does a kind whose only subject column is one a fact
// is read through, which the last pass finds. An armed kind that decides nothing about
// a person registers nothing, and neither does any kind of the tenant record's
// description, subject guarded or not.
func Test_deleteTree_subjectKinds(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)
	r := &resourceGenerator{client: c}
	run := make(map[string]*resourceInfo)
	for _, name := range []string{"Seat", "Project", "Stake", "Depot", "Pool", "Realm"} {
		res, err := resolveFixtureBindings(t, c, structs, name)
		if err != nil {
			t.Fatalf("resolveBindingAnnotations(%s) error = %v", name, err)
		}
		run[name] = res
		r.resources = append(r.resources, res)
	}

	kind := func(name, one, many string, trusted bool) *deleteKind {
		return &deleteKind{
			Name: name, Table: c.pluralize(name), One: one, Many: many, Key: []string{"Id"},
			Paths: [][]deleteLink{{link("PoolId", "Pools")}}, Deletes: true, Trusted: trusted,
		}
	}
	taken := func(trusted bool) []*deleteKind {
		return []*deleteKind{
			kind("Seat", "seat", "seats", trusted),
			kind("Project", "project", "projects", trusted),
			kind("Depot", "depot", "depots", trusted),
		}
	}
	run["Pool"].DeleteTree = &deleteTree{Record: &deleteKind{Name: "Pool", Table: "Pools", One: "pool", Many: "pools", Key: []string{"Id"}}, Taken: taken(false)}
	run["Realm"].DeleteTree = &deleteTree{Record: &deleteKind{Name: "Realm", Table: "Realms", One: "realm", Many: "realms", Key: []string{"Id"}}, Taken: taken(true)}

	r.attachSubjectReadThroughs()
	r.attachSubjectKinds()

	project := run["Project"]
	if !project.SubjectGuarded() || len(project.SubjectUserFields()) > 0 || len(project.SubjectSetTests()) > 0 {
		t.Fatal("Project is not a record whose only subject column is one a fact is read through, as the test needs")
	}
	if run["Depot"].SubjectGuarded() {
		t.Fatal("Depot decides something about a person, and the test needs a record that decides nothing")
	}

	for _, tt := range []struct {
		record string
		want   map[string]bool
	}{
		{record: "Pool", want: map[string]bool{"Seat": true, "Project": true, "Depot": false}},
		{record: "Realm", want: map[string]bool{"Seat": false, "Project": false, "Depot": false}},
	} {
		tree := run[tt.record].DeleteTree
		for _, kind := range tree.Taken {
			if kind.Subject != tt.want[kind.Name] {
				t.Errorf("%s's description: %s.Subject = %v, want %v", tt.record, kind.Name, kind.Subject, tt.want[kind.Name])
			}
		}

		literal := run[tt.record].DeleteTreeLiteral()
		for _, kind := range tree.Taken {
			at := strings.Index(literal, "Resource: \""+kind.Table+"\",")
			start := at + strings.Index(literal[max(at, 0):], "Rows: resource.NewTakenRows(func(")
			if at < 0 || start < at {
				t.Fatalf("%s's description writes no delete of a %s row:\n%s", tt.record, kind.Name, literal)
			}
			closure := literal[start : start+strings.Index(literal[start:], "}),\n")+len("}),\n")]
			if !strings.Contains(closure, "PatchSet["+kind.Name+"]") {
				t.Fatalf("%s's description writes the delete of a %s row as another kind's:\n%s", tt.record, kind.Name, closure)
			}
			registration := "patch.Enforce(caller)\n" +
				"patchSet.RegisterDeleteCheckFunc(func(ctx context.Context, txn resource.ReadWriteTransaction) error {\n" +
				"return RefuseImpersonatorAs" + kind.Name + "Subject(ctx, txn, resource.OperationDelete, patchSet.PrimaryKey(), patchSet)\n" +
				"})\n" +
				"}\n" +
				"}),\n"
			switch {
			case tt.want[kind.Name] && !strings.Contains(closure, "if !caller.IsZero() {\n"+registration):
				t.Errorf("%s's delete of a %s row does not register the record's questions after patch.Enforce(caller), inside the armed branch:\n%s", tt.record, kind.Name, closure)
			case !tt.want[kind.Name] && strings.Contains(closure, "RegisterDeleteCheckFunc"):
				t.Errorf("%s's delete of a %s row registers a check, and none is asked of it:\n%s", tt.record, kind.Name, closure)
			}
		}
	}
}
