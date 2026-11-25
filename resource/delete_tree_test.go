package resource

import (
	"slices"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestDeleteTree_Mutations pins the one count of what a delete costs in a commit: each
// row's delete, an entry in each index of its table, the change record of its removal
// where changes are tracked, counted at the most its columns can cost (the change history
// table has no index), and nothing for a stored file's release, which the commit does not carry.
func TestDeleteTree_Mutations(t *testing.T) {
	t.Parallel()

	tree := &DeleteTree{
		Record: "Clients",
		Cost:   DeleteCost{IndexEntries: 2},
		Taken: []DeleteKind{
			{Resource: "Tasks", Cost: DeleteCost{IndexEntries: 1}},
			{Resource: "Documents", Cost: DeleteCost{Files: 1}},
			{Resource: "Projects"},
		},
		RefusedBy: []DeleteKind{{Resource: "Invoices", Cost: DeleteCost{IndexEntries: 5}}},
	}
	// A change record is counted at the most it can write: the table's sixteen columns.
	changeRecord := 16

	tests := []struct {
		name string
		rows map[accesstypes.Resource]int
		// tracked says every kind's changes are recorded, and trackedKinds names the kinds
		// whose changes are, where only some are.
		tracked      bool
		trackedKinds []accesstypes.Resource
		want         int
	}{
		{
			name: "the record alone: its delete and its index entries",
			want: 1 + 2,
		},
		{
			name: "each taken row by what its kind costs, and a refusing kind's rows never",
			rows: map[accesstypes.Resource]int{"Tasks": 230, "Documents": 41, "Projects": 14, "Invoices": 3},
			want: (1 + 2) + 230*(1+1) + 41*1 + 14*1,
		},
		{
			name:    "a tracked delete adds a change record for every row, counted by its columns",
			rows:    map[accesstypes.Resource]int{"Tasks": 230, "Documents": 41, "Projects": 14},
			tracked: true,
			want:    (1 + 2 + changeRecord) + 230*(1+1+changeRecord) + 41*(1+changeRecord) + 14*(1+changeRecord),
		},
		{
			name:         "a change record is counted for the rows of the kinds that are tracked, and no other",
			rows:         map[accesstypes.Resource]int{"Tasks": 230, "Documents": 41, "Projects": 14},
			trackedKinds: []accesstypes.Resource{"Documents"},
			want:         (1 + 2) + 230*(1+1) + 41*(1+changeRecord) + 14*1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tracked := func(kind accesstypes.Resource) bool {
				return tt.tracked || slices.Contains(tt.trackedKinds, kind)
			}
			if got := tree.Mutations(tt.rows, tracked); got != tt.want {
				t.Errorf("DeleteTree.Mutations() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestChangeRecordMutations pins what a delete counts a change record to cost: the number
// of columns the change history table declares, which is the most a record can set, and
// never less than the fullest record sets (a session acting as a person, through a
// process, in an RPC method, on a tenant's row), since a save is filled to
// DeleteMutationLimit with no margin.
func TestChangeRecordMutations(t *testing.T) {
	t.Parallel()

	stmts := DataChangeEventsDDL(SpannerDBType, KeepChangeHistoryForever())
	if len(stmts) == 0 {
		t.Fatal("DataChangeEventsDDL() = 0 statements, want the table's")
	}
	if declared := len(tableColumns(t, stmts[0])); changeRecordMutations != declared {
		t.Errorf("changeRecordMutations = %d, want the %d columns the table declares", changeRecordMutations, declared)
	}

	asPerson := accountCtx("bob", bobID, &sessioninfo.Impersonation{
		Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.UserPrincipal(bobUser),
	})
	fullest := &DataChangeEvent{
		TableName: "Orders", RowID: "order-1", EventTime: spanner.CommitTimestamp, Operation: ChangeDelete,
		ChangeSet: spanner.NullJSON{Valid: true, Value: map[string]any{}}, TenantID: "tenant-1",
		Source: eventsource.UserProcessEvent(WithRPC(WithAuth(asPerson, "crew"), "CloseOrder"), "Import"),
	}
	if got := len(fullest.Columns()); got != 15 || got > changeRecordMutations {
		t.Errorf("the fullest record sets %d columns, want 15, no more than the count %d", got, changeRecordMutations)
	}
}

// TestDeleteWords_Count pins the words for a number of rows: one of a kind takes the
// kind's word for one, and any other number its word for many, its digits grouped in
// threes the English way.
func TestDeleteWords_Count(t *testing.T) {
	t.Parallel()

	words := DeleteWords{One: "ship class", Many: "ship classes"}

	tests := []struct {
		rows int
		want string
	}{
		{rows: 1, want: "1 ship class"},
		{rows: 0, want: "0 ship classes"},
		{rows: 14, want: "14 ship classes"},
		{rows: 999, want: "999 ship classes"},
		{rows: 1000, want: "1,000 ship classes"},
		{rows: 26700, want: "26,700 ship classes"},
		{rows: 1234567, want: "1,234,567 ship classes"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := words.Count(tt.rows); got != tt.want {
				t.Errorf("DeleteWords.Count(%d) = %q, want %q", tt.rows, got, tt.want)
			}
		})
	}
}

// TestDeleteTree_waysAt pins the ways a kind's rows are found under the record at each
// level: level 0 is the declared paths; above it, a link to a row of the same table is
// added at each place the way stands on a table that nests, before the first step where
// the kind's own table does and after each step whose table does, every way the links
// can be spread over the places; a kind with no such place has no way above level 0;
// and the record's own kind nests by the first step of its ways (a folder in a folder).
func TestDeleteTree_waysAt(t *testing.T) {
	t.Parallel()

	byProject := DeleteLink{Columns: []string{"ProjectId"}, Table: "Projects", References: []string{"Id"}}
	bySection := DeleteLink{Columns: []string{"SectionId"}, Table: "Sections", References: []string{"Id"}}
	byParentSection := DeleteLink{Columns: []string{"ParentSectionId"}, Table: "Sections", References: []string{"Id"}}
	byParent := DeleteLink{Columns: []string{"ParentTaskId"}, Table: "Tasks", References: []string{"Id"}}
	byTask := DeleteLink{Columns: []string{"TaskId"}, Table: "Tasks", References: []string{"Id"}}
	byProjectCode := DeleteLink{Columns: []string{"ProjectCode"}, Table: "Projects", References: []string{"Code"}}
	tree := &DeleteTree{
		Record: "Projects",
		Key:    []string{"Id"},
		Taken: []DeleteKind{
			{Resource: "Notes", Key: []string{"Id"}, Paths: []DeletePath{{byTask, bySection, byProject}}},
			{Resource: "Labels", Key: []string{"Id"}, Paths: []DeletePath{{byProjectCode}}},
			{Resource: "Tasks", Key: []string{"Id"}, Paths: []DeletePath{{bySection, byProject}}, Within: []DeleteLink{byParent}},
			{Resource: "Sections", Key: []string{"Id"}, Paths: []DeletePath{{byProject}}, Within: []DeleteLink{byParentSection}},
		},
	}
	byFolder := DeleteLink{Columns: []string{"ParentId"}, Table: "Folders", References: []string{"Id"}}
	folders := &DeleteTree{
		Record: "Folders",
		Key:    []string{"Id"},
		Taken:  []DeleteKind{{Resource: "Folders", Key: []string{"Id"}, Paths: []DeletePath{{byFolder}}}},
	}

	tests := []struct {
		name  string
		tree  *DeleteTree
		kind  int
		level int
		want  []DeletePath
	}{
		{name: "level 0 is the declared paths", tree: tree, kind: 0, level: 0, want: []DeletePath{{byTask, bySection, byProject}}},
		{name: "a kind under itself, one level up", tree: tree, kind: 3, level: 1, want: []DeletePath{{byParentSection, byProject}}},
		{name: "a kind under itself, two levels up", tree: tree, kind: 3, level: 2, want: []DeletePath{{byParentSection, byParentSection, byProject}}},
		{
			name: "a kind under itself on a way through another that sits under itself", tree: tree, kind: 2, level: 1,
			want: []DeletePath{{bySection, byParentSection, byProject}, {byParent, bySection, byProject}},
		},
		{
			name: "a kind under two that sit under themselves, the links spread every way", tree: tree, kind: 0, level: 2,
			want: []DeletePath{
				{byTask, bySection, byParentSection, byParentSection, byProject},
				{byTask, byParent, bySection, byParentSection, byProject},
				{byTask, byParent, byParent, bySection, byProject},
			},
		},
		{name: "a kind with no place has no way above level 0", tree: tree, kind: 1, level: 1, want: nil},
		{name: "the record's own kind, a folder in a folder", tree: folders, kind: 0, level: 1, want: []DeletePath{{byFolder, byFolder}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, tt.tree.waysAt(&tt.tree.Taken[tt.kind], tt.level)); diff != "" {
				t.Errorf("waysAt() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
