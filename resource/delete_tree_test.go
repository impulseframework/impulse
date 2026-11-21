package resource

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestDeleteTree_Mutations pins the one count of what a delete costs in a commit: each
// row's delete, an entry in each index of its table, the change record of its removal
// where changes are tracked, counted by its columns, and nothing for a stored file's
// release, which the commit does not carry.
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
	changeRecord := 6

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

// TestDeleteWords_Count pins the words for a number of rows: one of a kind takes the
// kind's word for one, and any other number its word for many.
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

// TestDeleteTree_walk pins what a delete that takes rows reads to find them: a kind's
// rows are found from the row above by the first step of each of its ways up and by its
// links to a row of its own kind, each step once; and a row is read with its key and
// any other column the rows under it are found by, which is a column outside its key
// where a foreign key references one.
func TestDeleteTree_walk(t *testing.T) {
	t.Parallel()

	byProject := DeleteLink{Columns: []string{"ProjectId"}, Table: "Projects", References: []string{"Id"}}
	byParent := DeleteLink{Columns: []string{"ParentTaskId"}, Table: "Tasks", References: []string{"Id"}}
	byTask := DeleteLink{Columns: []string{"TaskId"}, Table: "Tasks", References: []string{"Id"}}
	byTaskCode := DeleteLink{Columns: []string{"TaskCode"}, Table: "Tasks", References: []string{"Code"}}
	byProjectCode := DeleteLink{Columns: []string{"ProjectCode"}, Table: "Projects", References: []string{"Code"}}
	tree := &DeleteTree{
		Record: "Projects",
		Key:    []string{"Id"},
		Taken: []DeleteKind{
			// A comment is under a task by its id, and a second way by the task's code.
			{Resource: "Comments", Key: []string{"Id"}, Paths: []DeletePath{{byTask, byProject}, {byTaskCode, byProject}, {byTask, byProject}}},
			{Resource: "Labels", Key: []string{"Id"}, Paths: []DeletePath{{byProjectCode}}},
			// A task's link to its parent is written with its ways up too: one step.
			{Resource: "Tasks", Key: []string{"Id"}, Paths: []DeletePath{{byProject}}, Within: []DeleteLink{byParent, byProject}},
		},
	}

	if diff := cmp.Diff([]DeleteLink{byTask, byTaskCode}, tree.Taken[0].stepsUp()); diff != "" {
		t.Errorf("a comment's steps up mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]DeleteLink{byProject, byParent}, tree.Taken[2].stepsUp()); diff != "" {
		t.Errorf("a task's steps up mismatch (-want +got):\n%s", diff)
	}
	for table, want := range map[accesstypes.Resource][]string{
		"Projects": {"Id", "Code"},
		"Tasks":    {"Id", "Code"},
		"Comments": {"Id"},
	} {
		if diff := cmp.Diff(want, tree.reads(table, []string{"Id"})); diff != "" {
			t.Errorf("the columns read of %s mismatch (-want +got):\n%s", table, diff)
		}
	}
}
