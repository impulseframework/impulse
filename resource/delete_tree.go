package resource

import (
	"reflect"
	"slices"
	"strconv"

	"github.com/impulseframework/impulse/accesstypes"
)

const (
	// DeleteMutationLimit is the most mutations Spanner takes in one commit. A delete
	// that would need more is refused before anything is deleted (DeleteTree.Mutations).
	DeleteMutationLimit = 80_000

	// rowDeleteMutations is what the delete of one row costs by itself: Spanner counts a
	// delete once, whatever the row's columns.
	rowDeleteMutations = 1

	// fileReleaseMutations is what the release of one stored file costs in the commit.
	// The file is taken from its store once the commit is back, so the commit carries
	// nothing for it; a release that comes to write a row is counted here.
	fileReleaseMutations = 0
)

// changeRecordMutations is what the change record of one removed row costs: an insert
// counts once for each column it writes, and the change record's table has no index
// besides its key.
var changeRecordMutations = reflect.TypeFor[DataChangeEvent]().NumField()

// DeleteLink is one step from a row up toward the record it is under: the row's Columns
// hold the key of a row of Table, in that table's References columns. A foreign key
// gives one, and so does an interleave, whose rows open their key with their parent's.
type DeleteLink struct {
	Columns    []string
	Table      string
	References []string
	// MayBeEmpty marks a step the row can be taken off without being deleted: a foreign
	// key with a column that may be left empty and is no part of the row's own key. The
	// generator writes it from the schema. A row under a record by such steps alone can
	// be moved from under it, and the sentence that refuses the record's delete says so
	// (DeleteKind.movable).
	MayBeEmpty bool
}

// DeletePath is the steps from a kind's row up to the record, nearest first: the last
// step's Table is the record's. A kind with two ways up to the record has two paths, and
// a row is under the record when either leads to it.
type DeletePath []DeleteLink

// DeleteWords is what a sentence calls a kind of record: one of them and many, in lower
// case ("ship class", "ship classes"). The generator writes them (deleteWords), so every
// sentence about a delete takes a kind's words from the one place.
type DeleteWords struct {
	One  string
	Many string
}

// Count is the words for a number of the kind's rows: "1 project", "14 projects".
func (w DeleteWords) Count(rows int) string {
	if rows == 1 {
		return "1 " + w.One
	}

	return strconv.Itoa(rows) + " " + w.Many
}

// DeleteCost is what deleting one row of a kind costs in mutations beyond the delete
// itself, as far as the schema tells: the entries of the table's indexes, and the files
// the row stores. The change record of the removal is added where changes are tracked
// (Mutations).
type DeleteCost struct {
	// IndexEntries is the number of indexes on the kind's table besides its key: the
	// delete of a row takes one entry out of each.
	IndexEntries int
	// Files is the number of stored files a row of the kind holds a key to.
	Files int
}

// Mutations is what the delete of one row costs in a commit: the delete, an entry in
// each of the table's indexes, the change record written for the removal where changes
// are tracked, and the release of each file the row stores.
func (c DeleteCost) Mutations(tracked bool) int {
	mutations := rowDeleteMutations + c.IndexEntries + c.Files*fileReleaseMutations
	if tracked {
		mutations += changeRecordMutations
	}

	return mutations
}

// DeleteKind is one kind of row under a record.
type DeleteKind struct {
	// Resource is the kind's resource, which is its table.
	Resource accesstypes.Resource
	// Words are what a sentence calls the kind's rows.
	Words DeleteWords
	// Key is the kind's primary key columns, in key order.
	Key []string
	// Paths are the ways from a row of the kind up to the record.
	Paths []DeletePath
	// Cost is what deleting one row of the kind costs.
	Cost DeleteCost
	// RemovedWith are the tables whose row's delete makes the database remove rows of
	// the kind with it: every table up from the kind's by links declared ON DELETE
	// CASCADE. The generator writes it for the kinds under Under. A transaction that
	// deletes a row of one of those tables may take the kind's rows from under the
	// record, so the kind is then not counted, and the commit answers.
	RemovedWith []string
	// Within are the links from a row of the kind to another row of its own kind (a
	// task under a task), which no path holds, since a path never passes a table twice.
	// A row under a taken row by one of them is under the record too, and a delete that
	// takes the kind's rows follows them level by level (delete_cascade.go). The
	// generator writes them for the kinds under Taken.
	Within []DeleteLink
	// Rows is how a delete of the record deletes a row of the kind with it. The
	// generator writes one for each kind under Taken of a record that declares its
	// cascade (NewTakenRows). A kind without one is not taken in code, and a delete of
	// the record is refused while it has a row of the kind (delete_refusal.go).
	Rows TakenRows
}

// sameAs reports whether the two links are the one step: the same columns to the same
// columns of the same table.
func (l DeleteLink) sameAs(other DeleteLink) bool {
	return l.Table == other.Table && slices.Equal(l.Columns, other.Columns) && slices.Equal(l.References, other.References)
}

// stepsUp are the links a row of the kind is found by from the row above it: the first
// step of each of its ways up to the record, and its links to a row of its own kind,
// each once.
func (k *DeleteKind) stepsUp() []DeleteLink {
	var steps []DeleteLink
	add := func(link DeleteLink) {
		if !slices.ContainsFunc(steps, link.sameAs) {
			steps = append(steps, link)
		}
	}
	for _, path := range k.Paths {
		if len(path) > 0 {
			add(path[0])
		}
	}
	for _, link := range k.Within {
		add(link)
	}

	return steps
}

// movable reports whether a row of the kind can be taken from under the record without
// being deleted: each of its ways up to the record starts on columns the row may leave
// empty (DeleteLink.MayBeEmpty). A kind with one way that cannot be cleared is not
// movable, since a row under the record by that way stays under it.
func (k *DeleteKind) movable() bool {
	if len(k.Paths) == 0 {
		return false
	}
	for _, path := range k.Paths {
		if len(path) == 0 || !path[0].MayBeEmpty {
			return false
		}
	}

	return true
}

// DeleteTree describes what a delete of a record takes with it and what refuses it. The
// generator writes one for a resource that declares it (@deleteCascade), for the
// tenant record, whose description the library declares (every kind of the
// application's rows that are under the tenant goes with it), and for every other
// record with a kind of row directly under it that the database does not remove with
// it (Under): a plain delete of such a record is refused while it has one of those
// rows, in a sentence that says what is in the way (delete_refusal.go).
type DeleteTree struct {
	// Record is the resource the description is of.
	Record accesstypes.Resource
	// Words are what a sentence calls a row of the record.
	Words DeleteWords
	// Key is the record's primary key columns, in key order.
	Key []string
	// Cost is what deleting the record's own row costs.
	Cost DeleteCost
	// Taken are the kinds a delete of the record takes with it, in the order their rows
	// are deleted: deepest first, so no row is deleted before the rows under it. The
	// record's own kind is last among them where a row of the record is under another
	// (a folder in a folder).
	Taken []DeleteKind
	// RefusedBy are the kinds whose rows refuse the delete while one of them is under
	// the record.
	RefusedBy []DeleteKind
	// Under are the kinds with a row directly under the record that the database does
	// not remove with it: a foreign key to the record with no delete rule, or an
	// interleave that does not cascade, from a kind no cascading way leads up to the
	// record from. The record's own kind is among them where a row names another row
	// of its kind (a folder in a folder). Each kind's Paths are those links alone, one
	// step each. A delete of the record is refused while one of these rows is there.
	Under []DeleteKind
}

// RowName is what a sentence calls the row a delete is asked of, opening the sentence:
// "This client". With rowNameInside it is the one place a row gets its name in a
// sentence about a delete: a record that comes to name its rows changes these two
// functions alone.
func (t *DeleteTree) RowName() string {
	return "This " + t.Words.One
}

// rowNameInside is RowName where the sentence does not open with it: "this client".
func (t *DeleteTree) rowNameInside() string {
	return "this " + t.Words.One
}

// referenced are the columns of a table that the taken kinds' rows are found by: the
// columns their steps up to a row of the table reference, each once. They are the
// table's key, or part of it, except where a foreign key references other columns.
func (t *DeleteTree) referenced(table string) []string {
	var columns []string
	for i := range t.Taken {
		for _, link := range t.Taken[i].stepsUp() {
			if link.Table != table {
				continue
			}
			for _, column := range link.References {
				if !slices.Contains(columns, column) {
					columns = append(columns, column)
				}
			}
		}
	}

	return columns
}

// reads are the columns a delete that takes rows reads of a row of the table: its key,
// and any other column the taken kinds' rows under it are found by (referenced).
func (t *DeleteTree) reads(table accesstypes.Resource, key []string) []string {
	columns := slices.Clone(key)
	for _, column := range t.referenced(string(table)) {
		if !slices.Contains(columns, column) {
			columns = append(columns, column)
		}
	}

	return columns
}

// Mutations is the one count of what a delete of the record and everything it takes
// costs in a commit: the record's own row, and for each taken kind the number of its
// rows under the record (rows, by the kind's resource) times what one of them costs. A
// kind's rows cost a change record each where the kind's changes are tracked (tracked,
// by resource, the record's own among them). The delete is refused when the count
// passes DeleteMutationLimit.
func (t *DeleteTree) Mutations(rows map[accesstypes.Resource]int, tracked func(accesstypes.Resource) bool) int {
	mutations := t.Cost.Mutations(tracked(t.Record))
	for i := range t.Taken {
		mutations += rows[t.Taken[i].Resource] * t.Taken[i].Cost.Mutations(tracked(t.Taken[i].Resource))
	}

	return mutations
}
