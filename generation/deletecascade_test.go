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
func deleteFixtureResources(t *testing.T) (*client, map[string]*resourceInfo) {
	t.Helper()

	c := &client{tableMap: deleteFixtureTables()}
	resources, err := c.structsToResources(loadFixture(t, "deletefixture").Structs)
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}

	byName := make(map[string]*resourceInfo, len(resources))
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
// each with its ways up to the record and what a row's delete costs, and the kinds that
// refuse the delete.
func Test_deleteCascade_description(t *testing.T) {
	t.Parallel()

	c, byName := deleteFixtureResources(t)
	client := byName["Client"]

	want := &deleteTree{
		Record: &deleteKind{Name: "Client", Table: "Clients", One: "client", Many: "clients", Key: []string{"Id"}},
		Taken: []*deleteKind{
			{
				Name: "TaskComment", Table: "TaskComments", One: "task comment", Many: "task comments", Key: []string{"Id"},
				Paths: [][]deleteLink{{link("TaskId", "Tasks"), link("ProjectId", "Projects"), link("ClientId", "Clients")}},
			},
			{
				Name: "ProjectNote", Table: "ProjectNotes", One: "project note", Many: "project notes", Key: []string{"Id", "Sequence"},
				Paths: [][]deleteLink{{link("Id", "Projects"), link("ClientId", "Clients")}},
			},
			{
				Name: "Task", Table: "Tasks", One: "task", Many: "tasks", Key: []string{"Id"}, IndexEntries: 1,
				Paths: [][]deleteLink{{link("ProjectId", "Projects"), link("ClientId", "Clients")}},
			},
			{
				Name: "Project", Table: "Projects", One: "project", Many: "projects", Key: []string{"Id"},
				Paths: [][]deleteLink{{link("ClientId", "Clients")}},
			},
		},
		RefusedBy: []*deleteKind{
			{
				Name: "Invoice", Table: "Invoices", One: "invoice", Many: "invoices", Key: []string{"Id"},
				Paths: [][]deleteLink{{link("ClientId", "Clients")}},
			},
		},
	}
	if diff := cmp.Diff(want, client.DeleteTree); diff != "" {
		t.Fatalf("Client's description mismatch (-want +got):\n%s", diff)
	}

	for _, name := range []string{"Project", "Invoice", "Supplier"} {
		if byName[name].DeleteTree != nil {
			t.Errorf("%s has a description, though it declares no @%s and is no tenant record", name, deleteCascadeKeyword)
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

// Test_deleteCascade_refusals pins what a @deleteCascade is held to: a kind under the
// record that neither list names is refused naming the kind and the record, and so is a
// name that is no resource, a kind that is not under the record, a kind that can only
// be under a refusing kind, and a declaration on the tenant record. A kind under a
// refusing kind alone needs no place in a list.
func Test_deleteCascade_refusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		decl      *deleteCascadeDecl
		tenant    bool
		wantErr   []string
		wantTaken []string
	}{
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
			name:      "what is under a refusing kind alone needs no place in a list",
			decl:      &deleteCascadeDecl{Taken: []string{"Invoice"}, RefusedBy: []string{"Project"}},
			wantTaken: []string{"Invoices"},
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

			err := c.resolveDeleteTrees(resourceList(byName))
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("resolveDeleteTrees() error = %v", err)
				}
				if client.DeleteTree == nil {
					t.Fatal("Client has no description after resolveDeleteTrees()")
				}
				var taken []string
				for _, kind := range client.DeleteTree.Taken {
					taken = append(taken, kind.Table)
				}
				if diff := cmp.Diff(tt.wantTaken, taken); diff != "" {
					t.Errorf("taken kinds mismatch (-want +got):\n%s", diff)
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
			name:    "the record itself is refused",
			arg:     "Project, Client",
			wantErr: "@deleteCascade on Client names Client itself",
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
func Test_tenantDeleteTree(t *testing.T) {
	t.Parallel()

	c, byName := deleteFixtureResources(t)
	client, ledger := byName["Client"], byName["Ledger"]
	client.deleteCascade, client.DeleteTree, client.IsTenant = nil, nil, true
	ledger.DomainBinding = &domainBinding{Anchor: ledger.fieldNamed("ClientID")}

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
}

// Test_tableMetadata_foreignKeys pins how the schema read keeps a table's foreign keys
// whole: a key of two columns is one key with its columns in order, two keys to one
// table stay two, and a key whose referenced columns the read could not match has no
// referenced table, so nothing follows it.
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
		{Columns: []string{"TenantId", "HomeHangarId"}, Table: "Hangars", References: []string{"TenantId", "Id"}},
	}
	if diff := cmp.Diff(wantLinks, c.schemaLinks()["Ships"]); diff != "" {
		t.Errorf("schemaLinks() mismatch (-want +got):\n%s", diff)
	}
}
