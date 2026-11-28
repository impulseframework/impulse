package resource

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
)

const (
	// DeleteMutationLimit is the most mutations Spanner takes in one commit. A delete
	// that would need more takes what fits and answers the rows it deleted
	// (PatchSet.DeleteLeft); the same delete sent again takes the next part.
	DeleteMutationLimit = 80_000

	// DefaultDeleteSaveTime is how long one save of a delete that takes rows runs where
	// the client was given no WithDeleteSaveTime. Thirty seconds was chosen from large
	// deletes measured on the real service with the application beside its database: a
	// full save there ended at the mutation limit well before thirty seconds, so the time
	// limit ends only a save whose rows are slow to take.
	DefaultDeleteSaveTime = 30 * time.Second

	// rowDeleteMutations is what the delete of one row costs by itself: Spanner counts a
	// delete once, whatever the row's columns.
	rowDeleteMutations = 1

	// fileReleaseMutations is what the release of one stored file costs in the commit.
	// The file is taken from its store once the commit is back, so the commit carries
	// nothing for it; a release that comes to write a row is counted here.
	fileReleaseMutations = 0
)

// ChangeRecordMutations is what the change record of one removed row costs in a commit.
// An insert counts once for each column it sets, and the change history table has no index
// to enter the record in. A record sets the six columns every record sets, the tenant's
// where the row has one, and each column of who made the change that holds something
// (DataChangeEvent.Columns): eleven for a person in a session. The tenant's column is
// counted for every row. With no source it is sixteen, the table's columns, which is the
// most a record can set.
func ChangeRecordMutations(source ...eventsource.EventSource) int {
	if len(source) == 0 || source[0].IsZero() {
		return changeRecordColumns + len(eventsource.ColumnNames())
	}

	return changeRecordColumns + len(source[0].Columns())
}

// changeRecordColumns are the columns of a change record besides who made the change:
// TableName, RowId, EventTime, Sequence, Operation, ChangeSet and TenantId.
const changeRecordColumns = 7

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

// Count is the words for a number of the kind's rows, its digits grouped in threes the
// English way, as every sentence of the server is written: "1 project", "14 projects",
// "26,700 sorties".
func (w DeleteWords) Count(rows int) string {
	if rows == 1 {
		return "1 " + w.One
	}

	return groupedDigits(rows) + " " + w.Many
}

// groupedDigits writes a count with a comma between each three digits from the right:
// 26,700.
func groupedDigits(n int) string {
	digits := strconv.Itoa(n)
	var b strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}

	return b.String()
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
// each of the table's indexes, the release of each file the row stores, and changeRecord,
// what the change record written for the removal costs (ChangeRecordMutations), which is 0
// for a kind that keeps no change history.
func (c DeleteCost) Mutations(changeRecord int) int {
	return rowDeleteMutations + c.IndexEntries + c.Files*fileReleaseMutations + changeRecord
}

// DeleteKind is one kind of row under a record.
type DeleteKind struct {
	// Resource is the kind's resource, which is its table.
	Resource accesstypes.Resource
	// Words are what a sentence calls the kind's rows.
	Words DeleteWords
	// Key is the kind's primary key columns, in key order. Every statement a delete writes
	// about the kind's rows names them.
	Key []string
	// Paths are the ways from a row of the kind up to the record.
	Paths []DeletePath
	// Cost is what deleting one row of the kind costs.
	Cost DeleteCost
	// Within are the links from a row of the kind to another row of its own kind (a
	// task under a task), which no path holds, since a path never passes a table twice.
	// The generator writes them for the kinds under Taken. A row under a taken row by
	// one of them is under the record too, and a delete that takes the kind's rows finds
	// them level by level, the deepest first (delete_cascade.go).
	Within []DeleteLink
	// Rows is how a delete of the record deletes a row of the kind with it. The
	// generator writes one for each kind under Taken of a record that declares its
	// cascade (NewTakenRows). A kind without one is not taken in code, and a delete of
	// the record is refused while it has a row of the kind (delete_refusal.go).
	Rows TakenRows
	// Tenant is how a row of the kind resolves to its tenant, for a kind whose rows are
	// a tenant's: the column that holds the tenant's key, or the foreign key the way to
	// it leaves through and that way. The generator writes it from the kind's @domain.
	// A row of such a kind is under a tenant's record only where its tenant is the
	// record's (DeleteTree.Tenant): a row of another tenant that names the record, or a
	// row under it, is not under it, and a delete of the record neither counts it, names
	// it nor takes it, whoever asks.
	Tenant *DomainBindingData
	// Trusted marks a kind whose rows a delete of the record takes without asking the
	// caller's grant on them, and counts for the caller whatever they may list: each
	// kind under the tenant record, whose delete asks one permission, to delete a
	// tenant. The kind's delete rule is asked all the same. The generator writes it.
	Trusted bool
	// AsCaller marks a kind of the application's under a record of the library's (a
	// group, an account): its rows are checked against whoever the delete is for, the
	// patch's caller or else the request's, though the record's own delete is not armed,
	// since the library's method asked that person's grant on the record and none on
	// these rows. Outside a request nobody is asked, and the rows go trusted. A delete
	// told to take the library's rows alone leaves the kind out
	// (PatchSet.DeleteLibraryRowsAlone). The kinds an account's delete takes are marked
	// WithinReach instead. The generator writes it.
	AsCaller bool
	// WithinReach marks a kind of the application's that an account of the library's
	// takes with it: its rows go on the permission the delete's request was let in by,
	// with no grant asked on them and nothing asked for the caller, as far as that
	// permission reaches. A request checked for the whole application reaches every row.
	// One checked in a tenant reaches the rows of that tenant, and of each other tenant in
	// which the person holds the same permission, with or without a condition; a row of
	// any other tenant, or of none, refuses the delete before anything goes, in a sentence
	// that says where it is. Outside a request every row goes. The kind's delete rule is asked
	// all the same, every caller is told its count, and a delete told to take the
	// library's rows alone leaves the kind out (PatchSet.DeleteLibraryRowsAlone). The
	// generator writes it.
	WithinReach bool
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
	// are deleted: deepest first, so no row is deleted before the rows under it, and kinds
	// of one depth in reverse order of their names, so the question, which reads the
	// order backward, lists them by name. The record's own kind is last among them where
	// a row of the record is under another (a folder in a folder).
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
	// Tenant is how the record's row resolves to its tenant, as DeleteKind.Tenant is for
	// a kind's row. For the tenant record its column is the record's own key: the row is
	// its tenant. It is nil for a record that is no tenant's, and the rows under such a
	// record are under it whatever tenant they are of.
	Tenant *DomainBindingData
	// Tenants is how a sentence about the record's delete names a tenant. The generator
	// writes it for a record with a kind marked WithinReach, in an application that has a
	// tenant record; it is nil anywhere else.
	Tenants *DeleteTenants
}

// DeleteTenants is where a delete reads a tenant's name for a sentence, and what the
// sentence calls a tenant. Table is the tenant record's table and Key its key column,
// read as text. Name is the column that holds a tenant's name, empty where the record
// marks none. Word is one tenant in the middle of a sentence ("company").
type DeleteTenants struct {
	Table string
	Key   string
	Name  string
	Word  string
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

// Mutations is the one count of what a delete of the record and everything it takes
// costs in a commit: the record's own row, and for each taken kind the number of its
// rows under the record (rows, by the kind's resource) times what one of them costs. A
// kind's rows cost a change record each where the kind's changes are tracked, as
// changeRecord answers it by resource, the record's own among them, and 0 where they are
// not. A round of a delete is sized by it.
func (t *DeleteTree) Mutations(rows map[accesstypes.Resource]int, changeRecord func(accesstypes.Resource) int) int {
	mutations := t.Cost.Mutations(changeRecord(t.Record))
	for i := range t.Taken {
		mutations += rows[t.Taken[i].Resource] * t.Taken[i].Cost.Mutations(changeRecord(t.Taken[i].Resource))
	}

	return mutations
}

// sameAs reports whether the two links are the one step: the same columns to the same
// columns of the same table.
func (l DeleteLink) sameAs(other DeleteLink) bool {
	return l.Table == other.Table && slices.Equal(l.Columns, other.Columns) && slices.Equal(l.References, other.References)
}

// holds reports whether the way is a way of a kind of the resource that the description
// takes or lists under refusedBy, compared step by step (DeleteLink.sameAs): a row under
// the record by it is taken, or refuses the delete, with that kind.
func (t *DeleteTree) holds(resource accesstypes.Resource, way DeletePath) bool {
	for _, kinds := range [][]DeleteKind{t.Taken, t.RefusedBy} {
		for i := range kinds {
			if kinds[i].Resource != resource {
				continue
			}
			if slices.ContainsFunc(kinds[i].Paths, func(path DeletePath) bool {
				return slices.EqualFunc(path, way, DeleteLink.sameAs)
			}) {
				return true
			}
		}
	}

	return false
}

// nestingLinks are the links a row of the table is under another row of the same table
// by, among what the record's delete takes: the links of a taken kind of the table to
// its own kind (Within), and for the record's own table, where the record's own kind is
// taken (a folder in a folder), the first step of each of that kind's ways up to the
// record. Each is there once. A table with any nests: a row of it can be under another
// row of it among what the delete takes.
func (t *DeleteTree) nestingLinks(table string) []DeleteLink {
	var links []DeleteLink
	add := func(link DeleteLink) {
		if !slices.ContainsFunc(links, link.sameAs) {
			links = append(links, link)
		}
	}
	for i := range t.Taken {
		kind := &t.Taken[i]
		if string(kind.Resource) != table {
			continue
		}
		for _, link := range kind.Within {
			add(link)
		}
		if kind.Resource == t.Record {
			for _, path := range kind.Paths {
				if len(path) > 0 {
					add(path[0])
				}
			}
		}
	}

	return links
}

// nestingPlace is a place on one of a kind's ways where links to a row of the same table
// may stand: before the way's step at index at (after the last step where at is the
// way's length), the links of the table the way stands on there (nestingLinks).
type nestingPlace struct {
	at    int
	links []DeleteLink
}

// placesOn are the places on the path where the way stands on a table that nests: before
// the first step, where the kind's own table nests and the kind is not the record's own,
// and after each step whose table nests.
func (t *DeleteTree) placesOn(kind *DeleteKind, path DeletePath) []nestingPlace {
	var places []nestingPlace
	if kind.Resource != t.Record {
		if links := t.nestingLinks(string(kind.Resource)); len(links) > 0 {
			places = append(places, nestingPlace{at: 0, links: links})
		}
	}
	for i, step := range path {
		if links := t.nestingLinks(step.Table); len(links) > 0 {
			places = append(places, nestingPlace{at: i + 1, links: links})
		}
	}

	return places
}

// waysAt are the kind's ways up to the record at the level: each of its paths with that
// many links to a row of the same table added at the places on it (placesOn), every way
// the links can be spread over the places and chosen at each. Level 0 is the paths
// themselves. A kind with no place on any path has no way above level 0.
func (t *DeleteTree) waysAt(kind *DeleteKind, level int) []DeletePath {
	var ways []DeletePath
	for _, path := range kind.Paths {
		if len(path) == 0 {
			continue
		}
		places := t.placesOn(kind, path)
		for _, counts := range spread(level, len(places)) {
			ways = append(ways, nestedWays(path, places, counts)...)
		}
	}

	return ways
}

// spread is every way of spreading n over the given number of places, each place taking
// none or more: the counts, place by place. With no place, n is spread only where it is
// none.
func spread(n, places int) [][]int {
	if places == 0 {
		if n == 0 {
			return [][]int{{}}
		}

		return nil
	}
	var spreads [][]int
	for here := 0; here <= n; here++ {
		for _, rest := range spread(n-here, places-1) {
			spreads = append(spreads, append([]int{here}, rest...))
		}
	}

	return spreads
}

// nestedWays are the path with counts[i] links added at places[i], each link any of the
// place's, in every combination.
func nestedWays(path DeletePath, places []nestingPlace, counts []int) []DeletePath {
	ways := []DeletePath{{}}
	next := 0
	for at := 0; at <= len(path); at++ {
		if next < len(places) && places[next].at == at {
			for range counts[next] {
				grown := make([]DeletePath, 0, len(ways)*len(places[next].links))
				for _, way := range ways {
					for _, link := range places[next].links {
						grown = append(grown, append(slices.Clone(way), link))
					}
				}
				ways = grown
			}
			next++
		}
		if at < len(path) {
			for i := range ways {
				ways[i] = append(ways[i], path[at])
			}
		}
	}

	return ways
}

// waysUnder are the kind's ways up to a row of a page of the table at the
// level. A kind of the table itself is under a page row by the table's links to itself,
// one more than the level. Any other kind is under one by the part of each of its paths
// that ends on the table, with that many links added at the places on it (placesOn). A
// crossed page is one whose rows' own level was read by a start of another table, or by
// the record: the rows that hang on a page row with no link of the table between were
// read there too, so under a crossed page a way counts only with a link at its last
// place.
func (t *DeleteTree) waysUnder(kind *DeleteKind, table string, crossed bool, level int) []DeletePath {
	links := t.nestingLinks(table)
	if string(kind.Resource) == table {
		ways := []DeletePath{{}}
		for range level + 1 {
			grown := make([]DeletePath, 0, len(ways)*len(links))
			for _, way := range ways {
				for _, link := range links {
					grown = append(grown, append(slices.Clone(way), link))
				}
			}
			ways = grown
		}

		return ways
	}

	var ways []DeletePath
	for _, path := range kind.Paths {
		places := t.placesOn(kind, path)
		for i := 1; i <= len(path); i++ {
			if path[i-1].Table != table {
				continue
			}
			upTo := slices.DeleteFunc(slices.Clone(places), func(place nestingPlace) bool { return place.at > i })
			for _, counts := range spread(level, len(upTo)) {
				if crossed && (len(upTo) == 0 || upTo[len(upTo)-1].at != i || counts[len(counts)-1] == 0) {
					continue
				}
				ways = append(ways, nestedWays(path[:i], upTo, counts)...)
			}
		}
	}

	return ways
}
