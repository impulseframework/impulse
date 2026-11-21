package resource

import (
	"reflect"
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
	// are deleted: deepest first, so no row is deleted before the rows under it.
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
// "This client". It is the one place a row gets its name in a sentence about a
// delete: a record that comes to name its rows changes this function alone.
func (t *DeleteTree) RowName() string {
	return "This " + t.Words.One
}

// Mutations is the one count of what a delete of the record and everything it takes
// costs in a commit: the record's own row, and for each taken kind the number of its
// rows under the record (rows, by the kind's resource) times what one of them costs. The
// delete is refused when the count passes DeleteMutationLimit.
func (t *DeleteTree) Mutations(rows map[accesstypes.Resource]int, tracked bool) int {
	mutations := t.Cost.Mutations(tracked)
	for i := range t.Taken {
		mutations += rows[t.Taken[i].Resource] * t.Taken[i].Cost.Mutations(tracked)
	}

	return mutations
}
