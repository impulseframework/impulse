package resource

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// cascadeRows is how the tests of a delete that takes rows reach a kind's rows: the
// fields its key is held in, and the deletes it made, in order.
type cascadeRows struct {
	// refused answers a row's check by the row's key: the error, and whether it is the
	// row's rule that refuses.
	refused map[string]cascadeRefusal
	made    *[]string
}

// cascadeRefusal is what a row's check answers.
type cascadeRefusal struct {
	err    error
	byRule bool
}

func (cascadeRows) columns(DBType) map[string]takenColumn {
	return map[string]takenColumn{"Id": {field: "ID", typ: reflect.TypeFor[string]()}}
}

func (r cascadeRows) row(fields []accesstypes.Field, values []any, caller *Caller) takenRow {
	name := make([]string, 0, len(fields))
	for i, field := range fields {
		name = append(name, fmt.Sprintf("%s=%v", field, values[i]))
	}
	who := "trusted"
	if !caller.IsZero() {
		who = "armed"
	}
	row := &cascadeRow{name: strings.Join(name, ",") + " " + who, made: r.made}
	if refusal, ok := r.refused[fmt.Sprint(values...)]; ok {
		row.refusal = refusal
	}

	return row
}

func (cascadeRows) tracked() bool {
	return false
}

// cascadeRow is the delete of one row a test's delete takes.
type cascadeRow struct {
	name    string
	refusal cascadeRefusal
	made    *[]string
}

func (r *cascadeRow) checkDelete(context.Context, ReadWriteTransaction) (byRule bool, err error) {
	return r.refusal.byRule, r.refusal.err
}

func (r *cascadeRow) bufferTaken(context.Context, ReadWriteTransaction, ...string) error {
	*r.made = append(*r.made, r.name)

	return nil
}

// projectTree describes a record whose delete takes three kinds: its tasks, which name
// it and may be under another task, the comments on its tasks, and its labels, which
// name it by its code and not by its key.
func projectTree(rows TakenRows) *DeleteTree {
	return &DeleteTree{
		Record: "CascadeProjects",
		Words:  DeleteWords{One: "project", Many: "projects"},
		Key:    []string{"Id"},
		Taken: []DeleteKind{
			{
				Resource: "CascadeComments",
				Words:    DeleteWords{One: "comment", Many: "comments"},
				Key:      []string{"Id"},
				Paths: []DeletePath{{
					{Columns: []string{"TaskId"}, Table: "CascadeTasks", References: []string{"Id"}},
					{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}},
				}},
				Rows: rows,
			},
			{
				Resource: "CascadeLabels",
				Words:    DeleteWords{One: "label", Many: "labels"},
				Key:      []string{"Id"},
				Paths:    []DeletePath{{{Columns: []string{"ProjectCode"}, Table: "CascadeProjects", References: []string{"Code"}}}},
				Rows:     rows,
			},
			{
				Resource: "CascadeTasks",
				Words:    DeleteWords{One: "task", Many: "tasks"},
				Key:      []string{"Id"},
				Paths:    []DeletePath{{{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}}}},
				Within:   []DeleteLink{{Columns: []string{"ParentTaskId"}, Table: "CascadeTasks", References: []string{"Id"}}},
				Rows:     rows,
			},
		},
	}
}

// cascadeTables holds each table's rows for a walk, and answers the walk's reads from
// them as the database would: a kind's rows whose columns hold what the link references
// of one of the rows above.
type cascadeTables map[accesstypes.Resource][]map[string]any

func (c cascadeTables) under(_ context.Context, kind *DeleteKind, link DeleteLink, above []map[string]any, most int) ([]map[string]any, error) {
	var rows []map[string]any
	for _, row := range c[kind.Resource] {
		if len(rows) == most {
			break
		}
		if slices.ContainsFunc(above, func(over map[string]any) bool {
			for i, column := range link.Columns {
				if row[column] == nil || row[column] != over[link.References[i]] {
					return false
				}
			}

			return true
		}) {
			rows = append(rows, row)
		}
	}

	return rows, nil
}

// ids are the Id of each row, in order.
func ids(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, fmt.Sprint(row["Id"]))
	}

	return out
}

// TestTakenWalk_find pins how the rows a delete takes are found: level by level from
// the record's row, a kind's rows under the rows the level before found by each link
// that puts them there, a row under a row of its own kind among them and a row that
// names the record by a column outside its key. A row is found once, whatever ways lead
// to it, and never before a row it was found under; rows under each other end the walk;
// a row the transaction itself deleted is left out with what is under it, and a row it
// changed ends the walk, an update of a task before its project's delete in one request
// among them; and the walk stops once the rows found cost more than the delete may.
func TestTakenWalk_find(t *testing.T) {
	t.Parallel()

	tables := cascadeTables{
		"CascadeTasks": {
			{"Id": "t1", "ProjectId": "p1"},
			{"Id": "t2", "ProjectId": "p1"},
			{"Id": "t3", "ParentTaskId": "t1"},
			{"Id": "t4", "ParentTaskId": "t3"},
			// Under the project and under one of its tasks at once.
			{"Id": "t5", "ProjectId": "p1", "ParentTaskId": "t2"},
			// Under each other, the one under the project.
			{"Id": "t6", "ProjectId": "p1", "ParentTaskId": "t7"},
			{"Id": "t7", "ParentTaskId": "t6"},
			{"Id": "other", "ProjectId": "p2"},
		},
		"CascadeComments": {
			{"Id": "c1", "TaskId": "t1"},
			{"Id": "c2", "TaskId": "t4"},
			{"Id": "c3", "TaskId": "other"},
		},
		"CascadeLabels": {
			{"Id": "l1", "ProjectCode": "ALPHA"},
			{"Id": "l2", "ProjectCode": "BETA"},
		},
	}
	record := map[string]any{"Id": "p1", "Code": "ALPHA"}

	tests := []struct {
		name  string
		limit int
		// deleted and changed are the tasks the transaction deleted, and the ones it
		// updated, before the walk.
		deleted     []string
		changed     []string
		want        map[accesstypes.Resource][]string
		wantTooMany bool
		wantChanged bool
	}{
		{
			name:  "every row under the record, level by level",
			limit: DeleteMutationLimit,
			want: map[accesstypes.Resource][]string{
				"CascadeTasks":    {"t1", "t2", "t5", "t6", "t3", "t7", "t4"},
				"CascadeComments": {"c1", "c2"},
				"CascadeLabels":   {"l1"},
			},
		},
		{
			name:    "a row the transaction deleted is left out with what is under it",
			limit:   DeleteMutationLimit,
			deleted: []string{"t3"},
			want: map[accesstypes.Resource][]string{
				"CascadeTasks":    {"t1", "t2", "t5", "t6", "t7"},
				"CascadeComments": {"c1"},
				"CascadeLabels":   {"l1"},
			},
		},
		{
			name:    "a row the transaction updated and then deleted is one it deleted",
			limit:   DeleteMutationLimit,
			changed: []string{"t3"},
			deleted: []string{"t3"},
			want: map[accesstypes.Resource][]string{
				"CascadeTasks":    {"t1", "t2", "t5", "t6", "t7"},
				"CascadeComments": {"c1"},
				"CascadeLabels":   {"l1"},
			},
		},
		{
			// The request updates a task and then deletes the task's project.
			name:        "a row the transaction changed ends the walk",
			limit:       DeleteMutationLimit,
			changed:     []string{"t3"},
			wantChanged: true,
		},
		{
			name:    "a row the transaction changed under a row it deleted is left out with it",
			limit:   DeleteMutationLimit,
			deleted: []string{"t3"},
			changed: []string{"t4"},
			want: map[accesstypes.Resource][]string{
				"CascadeTasks":    {"t1", "t2", "t5", "t6", "t7"},
				"CascadeComments": {"c1"},
				"CascadeLabels":   {"l1"},
			},
		},
		{
			// The record's row, its label and four of its tasks cost six mutations.
			name:        "the walk stops once the rows found cost more than the delete may",
			limit:       4,
			want:        map[accesstypes.Resource][]string{"CascadeLabels": {"l1"}, "CascadeTasks": {"t1", "t2", "t5", "t6"}},
			wantTooMany: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			txn := newTransactionDeletes()
			for _, id := range tt.changed {
				txn.recordWritten("CascadeTasks", map[string]any{"Id": id}, false)
			}
			for _, id := range tt.deleted {
				txn.recordWritten("CascadeTasks", map[string]any{"Id": id}, true)
			}
			walk := &takenWalk{
				tree:  projectTree(nil),
				limit: tt.limit,
				tracked: func(accesstypes.Resource) bool {
					return false
				},
				under: func(ctx context.Context, kind *DeleteKind, link DeleteLink, above []map[string]any, most int) ([]map[string]any, error) {
					if most != tt.limit+1 {
						t.Errorf("the walk reads up to %d rows, want one more than the limit of %d", most, tt.limit)
					}

					return tables.under(ctx, kind, link, above, most)
				},
				deleted: txn.deletedRow,
				changed: txn.changedRow,
			}
			found, err := walk.find(t.Context(), record)
			if err != nil {
				t.Fatalf("takenWalk.find() error = %v", err)
			}
			if found.changed != tt.wantChanged {
				t.Fatalf("takenWalk.find() changed = %v, want %v", found.changed, tt.wantChanged)
			}
			if tt.wantChanged {
				// The delete is refused, and nothing the walk found is used.
				return
			}
			got := make(map[accesstypes.Resource][]string, len(found.rows))
			for kind, rows := range found.rows {
				got[kind] = ids(rows)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("takenWalk.find() rows mismatch (-want +got):\n%s", diff)
			}
			if found.tooMany != tt.wantTooMany {
				t.Errorf("takenWalk.find() tooMany = %v, want %v", found.tooMany, tt.wantTooMany)
			}
		})
	}
}

// Test_takenStatements pins the reads of one level: the kind's rows whose columns hold
// what the link references of one of the rows above, a row above that holds nothing in
// a referenced column left out, and the rows above read for in parts so that no read
// names more values than a query takes. Where the kind's rows and the record are a
// tenant's, each read finds the rows of the record's own tenant alone, by the kind's
// ways up and by its links to its own kind, and the value that names the tenant takes
// its room among the values of a read.
func Test_takenStatements(t *testing.T) {
	t.Parallel()

	tasks := &DeleteKind{Resource: "CascadeTasks", Key: []string{"Id"}}
	byProject := DeleteLink{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}}
	byCode := DeleteLink{Columns: []string{"TenantId", "ProjectCode"}, Table: "CascadeProjects", References: []string{"TenantId", "Code"}}
	var noCode *string
	code := "ALPHA"

	tests := []struct {
		name     string
		link     DeleteLink
		above    []map[string]any
		columns  []string
		wantSQL  []string
		wantArgs [][]any
		wantErr  bool
	}{
		{
			name:     "the rows that name one of the rows above",
			link:     byProject,
			above:    []map[string]any{{"Id": "p1"}, {"Id": "p2"}},
			columns:  []string{"Id", "Code"},
			wantSQL:  []string{"SELECT `Id`, `Code` FROM `CascadeTasks` WHERE (`ProjectId` = @d1) OR (`ProjectId` = @d2)"},
			wantArgs: [][]any{{"p1", "p2"}},
		},
		{
			name: "a link of two columns, to columns that are not the other table's key",
			link: byCode,
			above: []map[string]any{
				{"Id": "p1", "TenantId": "anvil", "Code": &code},
			},
			columns:  []string{"Id"},
			wantSQL:  []string{"SELECT `Id` FROM `CascadeTasks` WHERE (`TenantId` = @d1 AND `ProjectCode` = @d2)"},
			wantArgs: [][]any{{"anvil", &code}},
		},
		{
			name: "a row above that holds nothing in a referenced column has no row under it",
			link: byCode,
			above: []map[string]any{
				{"Id": "p1", "TenantId": "anvil", "Code": noCode},
				{"Id": "p2", "TenantId": "anvil", "Code": spanner.NullString{}},
				{"Id": "p3", "TenantId": "anvil", "Code": spanner.NullString{StringVal: "BETA", Valid: true}},
			},
			columns:  []string{"Id"},
			wantSQL:  []string{"SELECT `Id` FROM `CascadeTasks` WHERE (`TenantId` = @d1 AND `ProjectCode` = @d2)"},
			wantArgs: [][]any{{"anvil", spanner.NullString{StringVal: "BETA", Valid: true}}},
		},
		{
			name:    "no row above holds anything in the referenced columns",
			link:    byCode,
			above:   []map[string]any{{"Id": "p1", "TenantId": "anvil", "Code": noCode}},
			columns: []string{"Id"},
		},
		{
			name:    "a row above read without a referenced column",
			link:    byCode,
			above:   []map[string]any{{"Id": "p1", "TenantId": "anvil"}},
			columns: []string{"Id"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := takenStatements(SpannerDBType, nil, nil, tasks, tt.link, tt.above, tt.columns)
			if (err != nil) != tt.wantErr {
				t.Fatalf("takenStatements() error = %v, wantErr %v", err, tt.wantErr)
			}
			var (
				sql  []string
				args [][]any
			)
			for _, statement := range got {
				sql = append(sql, statement.SQL)
				args = append(args, statement.Args)
			}
			if diff := cmp.Diff(tt.wantSQL, sql); diff != "" {
				t.Errorf("takenStatements() SQL mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantArgs, args); diff != "" {
				t.Errorf("takenStatements() values mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("more rows above than one read names are read for in parts", func(t *testing.T) {
		t.Parallel()

		above := make([]map[string]any, takenParameterLimit+1)
		for i := range above {
			above[i] = map[string]any{"Id": i, "TenantId": "anvil", "Code": "C"}
		}
		for name, tt := range map[string]struct {
			link DeleteLink
			want []int
		}{
			"one column":  {link: byProject, want: []int{takenParameterLimit, 1}},
			"two columns": {link: byCode, want: []int{takenParameterLimit, takenParameterLimit, 2}},
		} {
			statements, err := takenStatements(SpannerDBType, nil, nil, tasks, tt.link, above, []string{"Id"})
			if err != nil {
				t.Fatalf("takenStatements() error = %v", err)
			}
			got := make([]int, 0, len(statements))
			for _, statement := range statements {
				got = append(got, len(statement.Args))
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("%s: values named by each read mismatch (-want +got):\n%s", name, diff)
			}
		}
	})

	t.Run("a kind of a tenant's rows under a record of a tenant", func(t *testing.T) {
		t.Parallel()

		byTenant := &DomainBindingData{Column: "TenantId"}
		tenantTasks := &DeleteKind{Resource: "CascadeTasks", Key: []string{"Id"}, Tenant: byTenant}
		byParent := DeleteLink{Columns: []string{"ParentTaskId"}, Table: "CascadeTasks", References: []string{"Id"}}
		tenants := &DeleteTree{Record: "CascadeTenants", Key: []string{"Id"}, Tenant: &DomainBindingData{Column: "Id"}}
		clients := &DeleteTree{Record: "CascadeClients", Key: []string{"Id"}, Tenant: byTenant}
		above := []map[string]any{{"Id": "p1"}, {"Id": "p2"}}

		for _, tt := range []struct {
			name      string
			dbType    DBType
			tree      *DeleteTree
			recordKey map[string]any
			kind      *DeleteKind
			link      DeleteLink
			above     []map[string]any
			wantSQL   string
			wantArgs  []any
		}{
			{
				name:      "the tenant record: the tenant its key holds",
				dbType:    SpannerDBType,
				tree:      tenants,
				recordKey: map[string]any{"Id": "anvil"},
				kind:      tenantTasks,
				link:      byProject,
				above:     above,
				wantSQL:   "SELECT `Id` FROM `CascadeTasks` WHERE ((`ProjectId` = @d1) OR (`ProjectId` = @d2)) AND `CascadeTasks`.`TenantId` = @d3",
				wantArgs:  []any{"p1", "p2", "anvil"},
			},
			{
				name:      "a record under a tenant: the tenant its row resolves to",
				dbType:    SpannerDBType,
				tree:      clients,
				recordKey: map[string]any{"Id": "c1"},
				kind:      tenantTasks,
				link:      byProject,
				above:     above,
				wantSQL: "SELECT `Id` FROM `CascadeTasks` WHERE ((`ProjectId` = @d1) OR (`ProjectId` = @d2))" +
					" AND `CascadeTasks`.`TenantId` = (SELECT tr.`TenantId` FROM `CascadeClients` AS tr WHERE tr.`Id` = @d3)",
				wantArgs: []any{"p1", "p2", "c1"},
			},
			{
				name:      "a kind whose rows reach their tenant through another table",
				dbType:    SpannerDBType,
				tree:      tenants,
				recordKey: map[string]any{"Id": "anvil"},
				kind: &DeleteKind{Resource: "CascadeTasks", Key: []string{"Id"}, Tenant: &DomainBindingData{
					Column: "ProjectId", Path: []BindingHop{{Table: "CascadeProjects", JoinColumn: "Id", Column: "TenantId"}},
				}},
				link:  byProject,
				above: above,
				wantSQL: "SELECT `Id` FROM `CascadeTasks` WHERE ((`ProjectId` = @d1) OR (`ProjectId` = @d2))" +
					" AND (SELECT n1.`TenantId` FROM `CascadeProjects` AS n1 WHERE n1.`Id` = `CascadeTasks`.`ProjectId`) = @d3",
				wantArgs: []any{"p1", "p2", "anvil"},
			},
			{
				name:      "a link to a row of its own kind",
				dbType:    SpannerDBType,
				tree:      tenants,
				recordKey: map[string]any{"Id": "anvil"},
				kind:      tenantTasks,
				link:      byParent,
				above:     []map[string]any{{"Id": "t1"}},
				wantSQL:   "SELECT `Id` FROM `CascadeTasks` WHERE ((`ParentTaskId` = @d1)) AND `CascadeTasks`.`TenantId` = @d2",
				wantArgs:  []any{"t1", "anvil"},
			},
			{
				name:      "the tenant record, on PostgreSQL",
				dbType:    PostgresDBType,
				tree:      tenants,
				recordKey: map[string]any{"Id": "anvil"},
				kind:      tenantTasks,
				link:      byProject,
				above:     above,
				wantSQL:   `SELECT "Id" FROM "CascadeTasks" WHERE (("ProjectId" = $1) OR ("ProjectId" = $2)) AND "CascadeTasks"."TenantId" = $3`,
				wantArgs:  []any{"p1", "p2", "anvil"},
			},
			{
				name:      "a record that is no tenant's",
				dbType:    SpannerDBType,
				tree:      &DeleteTree{Record: "CascadeClients", Key: []string{"Id"}},
				recordKey: map[string]any{"Id": "c1"},
				kind:      tenantTasks,
				link:      byProject,
				above:     above,
				wantSQL:   "SELECT `Id` FROM `CascadeTasks` WHERE (`ProjectId` = @d1) OR (`ProjectId` = @d2)",
				wantArgs:  []any{"p1", "p2"},
			},
			{
				name:      "a kind whose rows are no tenant's",
				dbType:    SpannerDBType,
				tree:      tenants,
				recordKey: map[string]any{"Id": "anvil"},
				kind:      tasks,
				link:      byProject,
				above:     above,
				wantSQL:   "SELECT `Id` FROM `CascadeTasks` WHERE (`ProjectId` = @d1) OR (`ProjectId` = @d2)",
				wantArgs:  []any{"p1", "p2"},
			},
		} {
			got, err := takenStatements(tt.dbType, tt.tree, tt.recordKey, tt.kind, tt.link, tt.above, []string{"Id"})
			if err != nil {
				t.Fatalf("%s: takenStatements() error = %v", tt.name, err)
			}
			want := []rowsUnderStatement{{SQL: tt.wantSQL, Args: tt.wantArgs}}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("%s: takenStatements() mismatch (-want +got):\n%s", tt.name, diff)
			}
		}

		many := make([]map[string]any, takenParameterLimit+1)
		for i := range many {
			many[i] = map[string]any{"Id": i}
		}
		statements, err := takenStatements(SpannerDBType, tenants, map[string]any{"Id": "anvil"}, tenantTasks, byProject, many, []string{"Id"})
		if err != nil {
			t.Fatalf("takenStatements() error = %v", err)
		}
		var rows []int
		for i, statement := range statements {
			rows = append(rows, len(statement.Args)-1)
			if len(statement.Args) > takenParameterLimit {
				t.Errorf("read %d names %d values, want no more than %d", i, len(statement.Args), takenParameterLimit)
			}
		}
		if diff := cmp.Diff([]int{takenParameterLimit - 1, 2}, rows); diff != "" {
			t.Errorf("rows above read by each read, the tenant's value beside them, mismatch (-want +got):\n%s", diff)
		}
	})
}

// Test_takenDeletes pins the order the rows found are deleted in: the kinds as the
// description lists them, deepest first, and a kind's rows the last found first, so a
// row under a row of its own kind goes before it. Each is the delete of the row by its
// key, for the caller it is given.
func Test_takenDeletes(t *testing.T) {
	t.Parallel()

	found := &takenFound{rows: map[accesstypes.Resource][]map[string]any{
		"CascadeTasks":    {{"Id": "t1"}, {"Id": "t3"}, {"Id": "t4"}},
		"CascadeComments": {{"Id": "c1"}, {"Id": "c2"}},
	}}

	for name, tt := range map[string]struct {
		caller *Caller
		who    string
	}{
		"armed against the caller":   {caller: whoLists(nil), who: "armed"},
		"trusted for a zero caller":  {caller: &Caller{}, who: "trusted"},
		"trusted for no caller here": {caller: nil, who: "trusted"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var made []string
			deletes, err := takenDeletes(projectTree(cascadeRows{made: &made}), SpannerDBType, found, tt.caller)
			if err != nil {
				t.Fatalf("takenDeletes() error = %v", err)
			}
			for _, taken := range deletes {
				if err := taken.row.bufferTaken(t.Context(), nil); err != nil {
					t.Fatalf("bufferTaken() error = %v", err)
				}
			}
			want := []string{"ID=c2 " + tt.who, "ID=c1 " + tt.who, "ID=t4 " + tt.who, "ID=t3 " + tt.who, "ID=t1 " + tt.who}
			if diff := cmp.Diff(want, made); diff != "" {
				t.Errorf("takenDeletes() order mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// invoicedTree is projectTree with two kinds that refuse the project's delete: an
// invoice, which names a task and may name a second one it was checked against, and a
// timesheet, which names a task.
func invoicedTree() *DeleteTree {
	underTask := func(column string) DeletePath {
		return DeletePath{
			{Columns: []string{column}, Table: "CascadeTasks", References: []string{"Id"}},
			{Columns: []string{"ProjectId"}, Table: "CascadeProjects", References: []string{"Id"}},
		}
	}
	tree := projectTree(nil)
	tree.RefusedBy = []DeleteKind{
		{
			Resource: "CascadeInvoices",
			Words:    DeleteWords{One: "invoice", Many: "invoices"},
			Key:      []string{"Id"},
			Paths:    []DeletePath{underTask("TaskId"), underTask("CheckedTaskId")},
		},
		{
			Resource: "CascadeTimesheets",
			Words:    DeleteWords{One: "timesheet", Many: "timesheets"},
			Key:      []string{"Id"},
			Paths:    []DeletePath{underTask("TaskId")},
		},
	}

	return tree
}

// refusing answers a count's reads from the tables as the database would: the name of
// each of the kind's rows whose columns hold what the link references of one of the
// rows above.
func (c cascadeTables) refusing(ctx context.Context, kind *DeleteKind, link DeleteLink, above []map[string]any) ([]string, error) {
	rows, err := c.under(ctx, kind, link, above, len(c[kind.Resource]))
	if err != nil {
		return nil, err
	}

	return ids(rows), nil
}

// invoicedTables are a project's tasks, two of them under the project only through
// another task, with the invoices and timesheets that name them.
func invoicedTables() cascadeTables {
	return cascadeTables{
		"CascadeTasks": {
			{"Id": "t1", "ProjectId": "p1"},
			{"Id": "t2", "ParentTaskId": "t1"},
			{"Id": "t3", "ParentTaskId": "t2"},
			{"Id": "other", "ProjectId": "p2"},
			{"Id": "other2", "ParentTaskId": "other"},
		},
		"CascadeInvoices": {
			// Under a task that is under the project only through another task.
			{"Id": "i1", "TaskId": "t2"},
			// Under two of the project's tasks, by two columns.
			{"Id": "i2", "TaskId": "t3", "CheckedTaskId": "t2"},
			// Under another project's task.
			{"Id": "i3", "TaskId": "other2"},
		},
		"CascadeTimesheets": {
			{"Id": "s1", "TaskId": "t3"},
		},
	}
}

// walked is what a walk from project p1 finds in the tables.
func walked(t *testing.T, tree *DeleteTree, tables cascadeTables) *takenFound {
	t.Helper()

	walk := &takenWalk{
		tree:    tree,
		limit:   1000,
		tracked: func(accesstypes.Resource) bool { return false },
		under:   tables.under,
		deleted: func(accesstypes.Resource, map[string]any) bool { return false },
		changed: func(accesstypes.Resource, map[string]any) bool { return false },
	}
	found, err := walk.find(t.Context(), map[string]any{"Id": "p1", "Code": "ALPHA"})
	if err != nil {
		t.Fatalf("find() error = %v", err)
	}

	return found
}

// TestRefusingCount_rows pins the count of the rows that refuse a record's delete from
// under the rows a walk found: a row under a task that is under the record only through
// another task is counted, a row under two of the rows found is counted once, a row
// under another record's task is not, the kinds come in the description's order, and a
// record with no such row has none.
func TestRefusingCount_rows(t *testing.T) {
	t.Parallel()

	tree := invoicedTree()
	paid := invoicedTables()
	delete(paid, "CascadeInvoices")
	delete(paid, "CascadeTimesheets")

	tests := []struct {
		name   string
		tables cascadeTables
		want   map[accesstypes.Resource]int64
		order  []accesstypes.Resource
	}{
		{
			name:   "rows under tasks the walk alone reaches",
			tables: invoicedTables(),
			want:   map[accesstypes.Resource]int64{"CascadeInvoices": 2, "CascadeTimesheets": 1},
			order:  []accesstypes.Resource{"CascadeInvoices", "CascadeTimesheets"},
		},
		{
			name:   "no refusing row under the rows found",
			tables: paid,
			want:   map[accesstypes.Resource]int64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			count := &refusingCount{tree: tree, under: tt.tables.refusing}
			refusing, err := count.rows(t.Context(), walked(t, tree, tt.tables))
			if err != nil {
				t.Fatalf("rows() error = %v", err)
			}
			got := make(map[accesstypes.Resource]int64, len(refusing))
			var order []accesstypes.Resource
			for _, k := range refusing {
				got[k.kind.Resource] = k.rows
				order = append(order, k.kind.Resource)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("rows() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.order, order); diff != "" {
				t.Errorf("rows() order mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRecordedDelete_refusedUnder pins the refusal of a delete over a refusing row under
// a row the walk alone reaches: a 409 in the sentence that counts each kind the person
// may list, and no refusal where no such row is there.
func TestRecordedDelete_refusedUnder(t *testing.T) {
	t.Parallel()

	tree := invoicedTree()
	paid := invoicedTables()
	delete(paid, "CascadeInvoices")
	delete(paid, "CascadeTimesheets")

	tests := []struct {
		name        string
		tables      cascadeTables
		caller      *Caller
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "a trusted call is told every kind",
			tables:      invoicedTables(),
			wantStatus:  http.StatusConflict,
			wantMessage: "This project has 2 invoices and 1 timesheet, so it can't be deleted.",
		},
		{
			name:        "a kind the person may not list is not counted for them",
			tables:      invoicedTables(),
			caller:      whoLists(accesstypes.Decisions{"CascadeInvoices": accesstypes.Denied()}),
			wantStatus:  http.StatusConflict,
			wantMessage: "This project has 1 timesheet, so it can't be deleted.",
		},
		{
			name:   "no refusing row under the rows found",
			tables: paid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			asked := &recordedDelete{tree: tree, key: map[string]any{"Id": "p1"}, caller: tt.caller}
			err := asked.refusedUnder(t.Context(), &refusingCount{tree: tree, under: tt.tables.refusing}, walked(t, tree, tt.tables))
			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("refusedUnder() error = %v, want none", err)
				}

				return
			}
			if got := statusOf(err); got != tt.wantStatus {
				t.Errorf("refusedUnder() status = %d, want %d (%v)", got, tt.wantStatus, err)
			}
			if got := httpio.Message(err); got != tt.wantMessage {
				t.Errorf("refusedUnder() message = %q, want %q", got, tt.wantMessage)
			}
		})
	}
}

// Test_refusingStatements pins the reads of the refusing rows under one part of the rows
// found: the key of the kind's rows whose columns hold what the link references of one
// of the rows above, the rows of the kind the transaction itself wrote left out, the
// rows above read for in parts that leave room for those, and no read at all where the
// transaction wrote more rows of the kind than a statement leaves out. Where the kind's
// rows and the record are a tenant's, a read finds the rows of the record's own tenant
// alone, and its parts leave room for the value that names the tenant too.
func Test_refusingStatements(t *testing.T) {
	t.Parallel()

	invoices := &DeleteKind{Resource: "CascadeInvoices", Key: []string{"Id"}}
	byTask := DeleteLink{Columns: []string{"TaskId"}, Table: "CascadeTasks", References: []string{"Id"}}
	rows := func(n int) []map[string]any {
		out := make([]map[string]any, 0, n)
		for i := range n {
			out = append(out, map[string]any{"Id": fmt.Sprintf("r%d", i)})
		}

		return out
	}

	got, err := refusingStatements(nil, nil, invoices, byTask, []map[string]any{{"Id": "t1"}, {"Id": "t2"}}, []map[string]any{{"Id": "i9"}})
	if err != nil {
		t.Fatalf("refusingStatements() error = %v", err)
	}
	want := []rowsUnderStatement{{
		SQL:  "SELECT t0.`Id` FROM `CascadeInvoices` AS t0 WHERE ((t0.`TaskId` = @d1) OR (t0.`TaskId` = @d2)) AND NOT (t0.`Id` = @d3)",
		Args: []any{"t1", "t2", "i9"},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("refusingStatements() mismatch (-want +got):\n%s", diff)
	}

	parts, err := refusingStatements(nil, nil, invoices, byTask, rows(takenParameterLimit), rows(100))
	if err != nil {
		t.Fatalf("refusingStatements() error = %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("refusingStatements() made %d reads of %d rows above with 100 rows left out, want 2", len(parts), takenParameterLimit)
	}
	for i, part := range parts {
		if len(part.Args) > takenParameterLimit {
			t.Errorf("read %d names %d values, want no more than %d", i, len(part.Args), takenParameterLimit)
		}
	}

	none, err := refusingStatements(nil, nil, invoices, byTask, rows(2), rows(deleteExclusionLimit+1))
	if err != nil {
		t.Fatalf("refusingStatements() error = %v", err)
	}
	if len(none) != 0 {
		t.Errorf("refusingStatements() made %d reads with more rows written than a statement leaves out, want none", len(none))
	}

	byTenant := &DomainBindingData{Column: "TenantId"}
	tenantInvoices := &DeleteKind{Resource: "CascadeInvoices", Key: []string{"Id"}, Tenant: byTenant}
	tenants := &DeleteTree{Record: "CascadeTenants", Key: []string{"Id"}, Tenant: &DomainBindingData{Column: "Id"}}
	for _, tt := range []struct {
		name      string
		tree      *DeleteTree
		recordKey map[string]any
		kind      *DeleteKind
		want      rowsUnderStatement
	}{
		{
			name:      "the tenant record: the tenant its key holds",
			tree:      tenants,
			recordKey: map[string]any{"Id": "anvil"},
			kind:      tenantInvoices,
			want: rowsUnderStatement{
				SQL:  "SELECT t0.`Id` FROM `CascadeInvoices` AS t0 WHERE ((t0.`TaskId` = @d1) OR (t0.`TaskId` = @d2)) AND t0.`TenantId` = @d3 AND NOT (t0.`Id` = @d4)",
				Args: []any{"t1", "t2", "anvil", "i9"},
			},
		},
		{
			name:      "a record under a tenant: the tenant its row resolves to",
			tree:      &DeleteTree{Record: "CascadeClients", Key: []string{"Id"}, Tenant: byTenant},
			recordKey: map[string]any{"Id": "c1"},
			kind:      tenantInvoices,
			want: rowsUnderStatement{
				SQL: "SELECT t0.`Id` FROM `CascadeInvoices` AS t0 WHERE ((t0.`TaskId` = @d1) OR (t0.`TaskId` = @d2))" +
					" AND t0.`TenantId` = (SELECT tr.`TenantId` FROM `CascadeClients` AS tr WHERE tr.`Id` = @d3) AND NOT (t0.`Id` = @d4)",
				Args: []any{"t1", "t2", "c1", "i9"},
			},
		},
		{
			name:      "a kind whose rows are no tenant's",
			tree:      tenants,
			recordKey: map[string]any{"Id": "anvil"},
			kind:      invoices,
			want: rowsUnderStatement{
				SQL:  "SELECT t0.`Id` FROM `CascadeInvoices` AS t0 WHERE ((t0.`TaskId` = @d1) OR (t0.`TaskId` = @d2)) AND NOT (t0.`Id` = @d3)",
				Args: []any{"t1", "t2", "i9"},
			},
		},
	} {
		got, err := refusingStatements(tt.tree, tt.recordKey, tt.kind, byTask, []map[string]any{{"Id": "t1"}, {"Id": "t2"}}, []map[string]any{{"Id": "i9"}})
		if err != nil {
			t.Fatalf("%s: refusingStatements() error = %v", tt.name, err)
		}
		if diff := cmp.Diff([]rowsUnderStatement{tt.want}, got); diff != "" {
			t.Errorf("%s: refusingStatements() mismatch (-want +got):\n%s", tt.name, diff)
		}
	}

	roomy, err := refusingStatements(tenants, map[string]any{"Id": "anvil"}, tenantInvoices, byTask, rows(takenParameterLimit), rows(100))
	if err != nil {
		t.Fatalf("refusingStatements() error = %v", err)
	}
	for i, part := range roomy {
		if len(part.Args) > takenParameterLimit {
			t.Errorf("read %d of a tenant's rows names %d values, want no more than %d", i, len(part.Args), takenParameterLimit)
		}
	}
}

// TestRecordedDelete_mayTake pins what a row that may not go answers the whole delete
// with: its rule's own sentence where its rule refuses; where the person may not delete
// it, or may not reach it, a 403 that names the row's kind if they may list it and says
// only that there are records if they may not; and anything else as the failure it is.
func TestRecordedDelete_mayTake(t *testing.T) {
	t.Parallel()

	tree := projectTree(nil)
	tasks := &tree.Taken[2]

	tests := []struct {
		name        string
		refusal     cascadeRefusal
		caller      *Caller
		wantStatus  int
		wantMessage string
		wantFailure bool
	}{
		{
			name: "a row that may go",
		},
		{
			name:        "a row its rule refuses answers in the rule's sentence",
			refusal:     cascadeRefusal{byRule: true, err: httpio.NewBadRequestMessage("A task that is being worked on can't be deleted.")},
			wantStatus:  http.StatusBadRequest,
			wantMessage: "A task that is being worked on can't be deleted.",
		},
		{
			name:        "a row the person may not delete",
			refusal:     cascadeRefusal{err: httpio.NewForbiddenMessage("user (u1) does not have (Delete) on CascadeTasks")},
			caller:      whoLists(nil),
			wantStatus:  http.StatusForbidden,
			wantMessage: "This project has tasks that you can't delete, so it can't be deleted.",
		},
		{
			name:        "a row the person may not reach",
			refusal:     cascadeRefusal{err: httpio.NewNotFoundMessage("CascadeTasks t9 not found")},
			caller:      whoLists(nil),
			wantStatus:  http.StatusForbidden,
			wantMessage: "This project has tasks that you can't delete, so it can't be deleted.",
		},
		{
			name:        "a row of a kind the person may not list is not named",
			refusal:     cascadeRefusal{err: httpio.NewForbiddenMessage("user (u1) does not have (Delete) on CascadeTasks")},
			caller:      whoLists(accesstypes.Decisions{"CascadeTasks": accesstypes.Denied()}),
			wantStatus:  http.StatusForbidden,
			wantMessage: "This project has records under it that you can't delete, so it can't be deleted.",
		},
		{
			name:        "a check that fails is a failure, not a refusal",
			refusal:     cascadeRefusal{err: errors.New("the database is away")},
			wantFailure: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			asked := &recordedDelete{tree: tree, key: map[string]any{"Id": "p1"}, caller: tt.caller}
			err := asked.mayTake(t.Context(), nil, takenDelete{kind: tasks, row: &cascadeRow{refusal: tt.refusal}})
			switch {
			case tt.wantFailure:
				if err == nil || httpio.Message(err) != "" {
					t.Fatalf("mayTake() error = %v, want a failure with no sentence for the person", err)
				}
			case tt.wantStatus == 0:
				if err != nil {
					t.Fatalf("mayTake() error = %v, want none", err)
				}
			default:
				if got := statusOf(err); got != tt.wantStatus {
					t.Errorf("mayTake() status = %d, want %d (%v)", got, tt.wantStatus, err)
				}
				if got := httpio.Message(err); got != tt.wantMessage {
					t.Errorf("mayTake() message = %q, want %q", got, tt.wantMessage)
				}
			}
		})
	}
}

// statusOf is the status a refusal answers with, 0 for an error that is no refusal.
func statusOf(err error) int {
	for status, has := range map[int]func(error) bool{
		http.StatusBadRequest:           httpio.HasBadRequest,
		http.StatusForbidden:            httpio.HasForbidden,
		http.StatusConflict:             httpio.HasConflict,
		http.StatusPreconditionRequired: httpio.HasPreconditionRequired,
	} {
		if has(err) {
			return status
		}
	}

	return 0
}

// TestRecordedDelete_question pins what a person is asked before a delete takes the
// rows under a record: the record's kind, then each kind found with its count, the kind
// nearest the record first; a kind the person may not list is not counted for them, and
// the sentence then says there are records they can't see; with no kind they may list it
// only asks.
func TestRecordedDelete_question(t *testing.T) {
	t.Parallel()

	found := &takenFound{rows: map[accesstypes.Resource][]map[string]any{
		"CascadeTasks":    {{"Id": "t1"}, {"Id": "t2"}, {"Id": "t3"}},
		"CascadeComments": {{"Id": "c1"}},
	}}

	tests := []struct {
		name   string
		caller *Caller
		want   string
	}{
		{
			name: "a trusted call is told every kind",
			want: "Delete this project and everything under it: 3 tasks, 1 comment. This can't be undone.",
		},
		{
			name:   "a person who may list every kind",
			caller: whoLists(nil),
			want:   "Delete this project and everything under it: 3 tasks, 1 comment. This can't be undone.",
		},
		{
			name:   "a kind the person may not list is not counted for them",
			caller: whoLists(accesstypes.Decisions{"CascadeComments": accesstypes.Denied()}),
			want:   "Delete this project and everything under it: 3 tasks, and records you can't see. This can't be undone.",
		},
		{
			name:   "a person who may list none of the kinds is only asked",
			caller: whoLists(accesstypes.Decisions{"CascadeComments": accesstypes.Denied(), "CascadeTasks": accesstypes.Denied()}),
			want:   "Delete this project and everything under it? This can't be undone.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			asked := &recordedDelete{tree: projectTree(nil), key: map[string]any{"Id": "p1"}, caller: tt.caller}
			got, err := asked.question(t.Context(), found)
			if err != nil {
				t.Fatalf("question() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("question() = %q, want %q", got, tt.want)
			}
		})
	}

	if got, want := deleteTooManySentence(projectTree(nil)), "This project has too many records under it to delete at once. Delete some of them first."; got != want {
		t.Errorf("deleteTooManySentence() = %q, want %q", got, want)
	}
	if got, want := deleteWithChangeSentence(projectTree(nil)), "This request also changes a record under this project. Send the change and the delete separately."; got != want {
		t.Errorf("deleteWithChangeSentence() = %q, want %q", got, want)
	}
}

// TestDecoder_readsWhatARemoveSays pins a remove operation's value: "everythingUnder":
// true makes the delete one that takes the rows under its record, a remove that says
// nothing of it takes the record's row alone, and a value that is not an object, or
// says it with anything but true or false, is a 400.
func TestDecoder_readsWhatARemoveSays(t *testing.T) {
	t.Parallel()

	rSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Create, accesstypes.Update, accesstypes.Delete)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewDecoder[tenancyResource, tenancyCreateRequest](rSet)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	tests := []struct {
		name    string
		value   string
		want    bool
		wantBad bool
	}{
		{name: "a remove that says so", value: `{"everythingUnder":true}`, want: true},
		{name: "a remove that says no", value: `{"everythingUnder":false}`},
		{name: "a remove with the value an operation without one is given", value: `{}`},
		{name: "a remove with no value at all"},
		{name: "a remove whose value says something else", value: `{"name":"dock four"}`},
		{name: "a remove that says it with a word", value: `{"everythingUnder":"yes"}`, wantBad: true},
		{name: "a remove whose value is a list", value: `[true]`, wantBad: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for name, decode := range map[string]func(oper *Operation) (*PatchSet[tenancyResource], error){
				"DecodeOperation": func(oper *Operation) (*PatchSet[tenancyResource], error) {
					return decoder.DecodeOperation(oper, renderStubPermissions{}, accesstypes.GlobalScope())
				},
				"DecodeOperationWithoutPermissions": decoder.DecodeOperationWithoutPermissions,
			} {
				oper := &Operation{Type: OperationDelete, Req: &http.Request{Body: io.NopCloser(strings.NewReader(tt.value))}}
				patchSet, err := decode(oper)
				if tt.wantBad {
					if err == nil || !httpio.HasBadRequest(err) {
						t.Fatalf("%s() error = %v, want a 400", name, err)
					}

					continue
				}
				if err != nil {
					t.Fatalf("%s() error = %v", name, err)
				}
				if patchSet.everythingUnder != tt.want {
					t.Errorf("%s(): the delete takes everything under its record = %v, want %v", name, patchSet.everythingUnder, tt.want)
				}
			}
		})
	}
}
