package resource

import (
	"testing"

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
		name    string
		rows    map[accesstypes.Resource]int
		tracked bool
		want    int
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tree.Mutations(tt.rows, tt.tracked); got != tt.want {
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
