package resource

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// under is a kind with rows directly under a record, by one column that holds the
// record's Id.
func under(table accesstypes.Resource, one, many, column, record string, removedWith ...string) DeleteKind {
	return DeleteKind{
		Resource:    table,
		Words:       DeleteWords{One: one, Many: many},
		Key:         []string{"Id"},
		Paths:       []DeletePath{{{Columns: []string{column}, Table: record, References: []string{"Id"}}}},
		RemovedWith: removedWith,
	}
}

// movableUnderWing is a kind with rows directly under a wing (under), by a column the
// row may leave empty.
func movableUnderWing(table accesstypes.Resource, one, many, column string) DeleteKind {
	kind := under(table, one, many, column, "RefusalWings")
	for i := range kind.Paths {
		kind.Paths[i][0].MayBeEmpty = true
	}

	return kind
}

// wingTree describes a record whose squadrons name it in a column that may be empty,
// and whose patrols name it twice: as the wing that flies them, which may be empty, and
// as the wing that pays for them, which may not. Its pilots name it in a column that may
// not be empty.
func wingTree() *DeleteTree {
	patrols := movableUnderWing("RefusalPatrols", "patrol", "patrols", "FlownByWingId")
	patrols.Paths = append(patrols.Paths, DeletePath{{Columns: []string{"PaidByWingId"}, Table: "RefusalWings", References: []string{"Id"}}})

	return &DeleteTree{
		Record: "RefusalWings",
		Words:  DeleteWords{One: "wing", Many: "wings"},
		Key:    []string{"Id"},
		Under: []DeleteKind{
			movableUnderWing("RefusalLiaisons", "liaison", "liaisons", "WingId"),
			patrols,
			under("RefusalPilots", "pilot", "pilots", "WingId", "RefusalWings"),
			movableUnderWing("RefusalSquadrons", "squadron", "squadrons", "WingId"),
		},
	}
}

// squadronTree describes a record that declares nothing: its missions refuse its
// plain delete, and the database removes a mission with its sector.
func squadronTree() *DeleteTree {
	return &DeleteTree{
		Record: "RefusalSquadrons",
		Words:  DeleteWords{One: "squadron", Many: "squadrons"},
		Key:    []string{"Id"},
		Under:  []DeleteKind{under("RefusalMissions", "mission", "missions", "AssignedSquadronId", "RefusalSquadrons", "RefusalSectors")},
	}
}

// shipTree describes a record with three kinds under it.
func shipTree() *DeleteTree {
	return &DeleteTree{
		Record: "RefusalShips",
		Words:  DeleteWords{One: "ship", Many: "ships"},
		Key:    []string{"Id"},
		Under: []DeleteKind{
			under("RefusalDroidReports", "droid report", "droid reports", "ShipId", "RefusalShips"),
			under("RefusalInspections", "inspection", "inspections", "ShipId", "RefusalShips"),
			under("RefusalSorties", "sortie", "sorties", "ShipId", "RefusalShips"),
		},
	}
}

// clientTree describes a record that declares its delete: its contacts go with it, and
// a mission or a consignment refuses it. All three kinds are directly under it.
func clientTree() *DeleteTree {
	contacts := under("RefusalClientContacts", "client contact", "client contacts", "ClientId", "RefusalClients")
	missions := under("RefusalMissions", "mission", "missions", "ClientId", "RefusalClients")
	consignments := under("RefusalConsignments", "consignment", "consignments", "ClientId", "RefusalClients")

	return &DeleteTree{
		Record:    "RefusalClients",
		Words:     DeleteWords{One: "client", Many: "clients"},
		Key:       []string{"Id"},
		Taken:     []DeleteKind{contacts},
		RefusedBy: []DeleteKind{missions, consignments},
		Under:     []DeleteKind{contacts, consignments, missions},
	}
}

// folderTree describes a record with rows of its own kind under it, in a table whose
// rows the database also removes with a row of the same table.
func folderTree() *DeleteTree {
	return &DeleteTree{
		Record: "RefusalFolders",
		Words:  DeleteWords{One: "folder", Many: "folders"},
		Key:    []string{"Id"},
		Under:  []DeleteKind{under("RefusalFolders", "folder", "folders", "ShortcutToId", "RefusalFolders", "RefusalFolders")},
	}
}

// tableCounts answers each count statement with the rows held for the table it reads,
// and notes the tables asked, in order.
type tableCounts struct {
	rows  map[string]int64
	asked []string
}

func (c *tableCounts) count(_ context.Context, statement rowsUnderStatement) (int64, error) {
	table, _, _ := strings.Cut(strings.TrimPrefix(statement.SQL, "SELECT COUNT(*) FROM `"), "`")
	c.asked = append(c.asked, table)

	return c.rows[table], nil
}

// whoLists is a caller whose List grant on each resource is as decided, granted where
// nothing is said.
func whoLists(decisions accesstypes.Decisions) *Caller {
	return &Caller{Permissions: renderStubPermissions{decisions: decisions}}
}

// TestTransactionDeletes_refuse pins the check before the commit: a delete of a record
// with rows under it is refused as a 409 whose sentence names the record's kind and
// each kind in the way with its count; a kind the record lists under refusedBy answers
// before any other and keeps the record from being deleted at all; a kind the person
// may not list, or may list only by a grant with a condition, is never named or
// counted for them; a kind whose rows the database may remove with another row the
// transaction deleted is not counted; and one count runs per kind. Where every kind
// named reaches the record only through columns that may be empty, the sentence says
// the rows may be moved or deleted; beside one kind that does not, it says only to
// delete them.
func TestTransactionDeletes_refuse(t *testing.T) {
	t.Parallel()

	conditional := accesstypes.Conditional(accesstypes.ConditionGroup{Resources: []accesstypes.Resource{"RefusalMissions"}})

	tests := []struct {
		name string
		// deletes are the deletes the transaction buffered, in order.
		deletes []recordedDelete
		// deleted is how many rows of each table the transaction deleted.
		deleted map[accesstypes.Resource]int
		rows    map[string]int64
		want    string
		// wantAsked is the tables counted, in order.
		wantAsked []string
	}{
		{
			name:      "a record with nothing under it goes",
			deletes:   []recordedDelete{{tree: squadronTree(), key: map[string]any{"Id": "s1"}}},
			wantAsked: []string{"RefusalMissions"},
		},
		{
			name:      "rows of one kind",
			deletes:   []recordedDelete{{tree: squadronTree(), key: map[string]any{"Id": "s1"}}},
			rows:      map[string]int64{"RefusalMissions": 3},
			want:      "This squadron has 3 missions. Delete them first.",
			wantAsked: []string{"RefusalMissions"},
		},
		{
			name:      "one row",
			deletes:   []recordedDelete{{tree: squadronTree(), key: map[string]any{"Id": "s1"}}},
			rows:      map[string]int64{"RefusalMissions": 1},
			want:      "This squadron has 1 mission. Delete it first.",
			wantAsked: []string{"RefusalMissions"},
		},
		{
			name:      "rows of two kinds",
			deletes:   []recordedDelete{{tree: shipTree(), key: map[string]any{"Id": "x1"}}},
			rows:      map[string]int64{"RefusalDroidReports": 1, "RefusalSorties": 2},
			want:      "This ship has 1 droid report and 2 sorties. Delete them first.",
			wantAsked: []string{"RefusalDroidReports", "RefusalInspections", "RefusalSorties"},
		},
		{
			name:      "rows of three kinds",
			deletes:   []recordedDelete{{tree: shipTree(), key: map[string]any{"Id": "x1"}}},
			rows:      map[string]int64{"RefusalDroidReports": 1, "RefusalInspections": 4, "RefusalSorties": 2},
			want:      "This ship has 1 droid report, 4 inspections, and 2 sorties. Delete them first.",
			wantAsked: []string{"RefusalDroidReports", "RefusalInspections", "RefusalSorties"},
		},
		{
			name:      "rows that can be taken from under the record may be moved or deleted",
			deletes:   []recordedDelete{{tree: wingTree(), key: map[string]any{"Id": "w1"}}},
			rows:      map[string]int64{"RefusalSquadrons": 3},
			want:      "This wing has 3 squadrons. Move or delete them first.",
			wantAsked: []string{"RefusalLiaisons", "RefusalPatrols", "RefusalPilots", "RefusalSquadrons"},
		},
		{
			name:      "one row that can be taken from under the record",
			deletes:   []recordedDelete{{tree: wingTree(), key: map[string]any{"Id": "w1"}}},
			rows:      map[string]int64{"RefusalSquadrons": 1},
			want:      "This wing has 1 squadron. Move or delete it first.",
			wantAsked: []string{"RefusalLiaisons", "RefusalPatrols", "RefusalPilots", "RefusalSquadrons"},
		},
		{
			name:      "two kinds that can both be taken from under the record",
			deletes:   []recordedDelete{{tree: wingTree(), key: map[string]any{"Id": "w1"}}},
			rows:      map[string]int64{"RefusalLiaisons": 1, "RefusalSquadrons": 3},
			want:      "This wing has 1 liaison and 3 squadrons. Move or delete them first.",
			wantAsked: []string{"RefusalLiaisons", "RefusalPatrols", "RefusalPilots", "RefusalSquadrons"},
		},
		{
			name:      "beside a kind that cannot be moved, the sentence says only to delete",
			deletes:   []recordedDelete{{tree: wingTree(), key: map[string]any{"Id": "w1"}}},
			rows:      map[string]int64{"RefusalPilots": 2, "RefusalSquadrons": 3},
			want:      "This wing has 2 pilots and 3 squadrons. Delete them first.",
			wantAsked: []string{"RefusalLiaisons", "RefusalPatrols", "RefusalPilots", "RefusalSquadrons"},
		},
		{
			name:      "a kind with one way that may not be empty cannot be moved",
			deletes:   []recordedDelete{{tree: wingTree(), key: map[string]any{"Id": "w1"}}},
			rows:      map[string]int64{"RefusalPatrols": 4},
			want:      "This wing has 4 patrols. Delete them first.",
			wantAsked: []string{"RefusalLiaisons", "RefusalPatrols", "RefusalPilots", "RefusalSquadrons"},
		},
		{
			name: "a kind that cannot be moved and that the person may not list does not change the sentence",
			deletes: []recordedDelete{{
				tree: wingTree(), key: map[string]any{"Id": "w1"},
				caller: whoLists(accesstypes.Decisions{"RefusalPilots": accesstypes.Denied()}),
			}},
			rows:      map[string]int64{"RefusalPilots": 2, "RefusalSquadrons": 3},
			want:      "This wing has 3 squadrons. Move or delete them first.",
			wantAsked: []string{"RefusalLiaisons", "RefusalPatrols", "RefusalPilots", "RefusalSquadrons"},
		},
		{
			name: "a refusing kind keeps the record from being deleted, whether its rows can be moved or not",
			deletes: []recordedDelete{{
				tree: &DeleteTree{
					Record:    "RefusalWings",
					Words:     DeleteWords{One: "wing", Many: "wings"},
					Key:       []string{"Id"},
					RefusedBy: []DeleteKind{movableUnderWing("RefusalSquadrons", "squadron", "squadrons", "WingId")},
				},
				key: map[string]any{"Id": "w1"},
			}},
			rows:      map[string]int64{"RefusalSquadrons": 3},
			want:      "This wing has 3 squadrons, so it can't be deleted.",
			wantAsked: []string{"RefusalSquadrons"},
		},
		{
			name:      "a kind listed under refusedBy answers before any other, and no other is counted",
			deletes:   []recordedDelete{{tree: clientTree(), key: map[string]any{"Id": "c1"}}},
			rows:      map[string]int64{"RefusalClientContacts": 2, "RefusalConsignments": 3},
			want:      "This client has 3 consignments, so it can't be deleted.",
			wantAsked: []string{"RefusalMissions", "RefusalConsignments"},
		},
		{
			name:      "both refusing kinds, in the order the record lists them",
			deletes:   []recordedDelete{{tree: clientTree(), key: map[string]any{"Id": "c1"}}},
			rows:      map[string]int64{"RefusalMissions": 1, "RefusalConsignments": 3},
			want:      "This client has 1 mission and 3 consignments, so it can't be deleted.",
			wantAsked: []string{"RefusalMissions", "RefusalConsignments"},
		},
		{
			name:      "with no refusing row, the rows under a declared record answer, each kind counted once",
			deletes:   []recordedDelete{{tree: clientTree(), key: map[string]any{"Id": "c1"}}},
			rows:      map[string]int64{"RefusalClientContacts": 2},
			want:      "This client has 2 client contacts. Delete them first.",
			wantAsked: []string{"RefusalMissions", "RefusalConsignments", "RefusalClientContacts"},
		},
		{
			name: "a kind the person may not list is not named",
			deletes: []recordedDelete{{
				tree: squadronTree(), key: map[string]any{"Id": "s1"},
				caller: whoLists(accesstypes.Decisions{"RefusalMissions": accesstypes.Denied()}),
			}},
			rows:      map[string]int64{"RefusalMissions": 3},
			want:      "This squadron has records under it that you can't see, so it can't be deleted.",
			wantAsked: []string{"RefusalMissions"},
		},
		{
			name: "a grant with a condition shows some rows and not others, and is not enough",
			deletes: []recordedDelete{{
				tree: squadronTree(), key: map[string]any{"Id": "s1"},
				caller: whoLists(accesstypes.Decisions{"RefusalMissions": conditional}),
			}},
			rows:      map[string]int64{"RefusalMissions": 3},
			want:      "This squadron has records under it that you can't see, so it can't be deleted.",
			wantAsked: []string{"RefusalMissions"},
		},
		{
			name: "of two kinds the person is told the one they may list",
			deletes: []recordedDelete{{
				tree: shipTree(), key: map[string]any{"Id": "x1"},
				caller: whoLists(accesstypes.Decisions{"RefusalDroidReports": accesstypes.Denied()}),
			}},
			rows:      map[string]int64{"RefusalDroidReports": 5, "RefusalSorties": 2},
			want:      "This ship has 2 sorties. Delete them first.",
			wantAsked: []string{"RefusalDroidReports", "RefusalInspections", "RefusalSorties"},
		},
		{
			name: "a refusing kind the person may not list is not named either",
			deletes: []recordedDelete{{
				tree: clientTree(), key: map[string]any{"Id": "c1"},
				caller: whoLists(accesstypes.Decisions{"RefusalConsignments": accesstypes.Denied()}),
			}},
			rows:      map[string]int64{"RefusalConsignments": 3},
			want:      "This client has records under it that you can't see, so it can't be deleted.",
			wantAsked: []string{"RefusalMissions", "RefusalConsignments"},
		},
		{
			name:    "a kind the database may remove with another row the transaction deleted is not counted",
			deletes: []recordedDelete{{tree: squadronTree(), key: map[string]any{"Id": "s1"}}},
			deleted: map[accesstypes.Resource]int{"RefusalSquadrons": 1, "RefusalSectors": 1},
			rows:    map[string]int64{"RefusalMissions": 3},
		},
		{
			name:      "the record's own delete is not such a row",
			deletes:   []recordedDelete{{tree: folderTree(), key: map[string]any{"Id": "f1"}}},
			deleted:   map[accesstypes.Resource]int{"RefusalFolders": 1},
			rows:      map[string]int64{"RefusalFolders": 2},
			want:      "This folder has 2 folders. Delete them first.",
			wantAsked: []string{"RefusalFolders"},
		},
		{
			name:    "another row of the record's table is",
			deletes: []recordedDelete{{tree: folderTree(), key: map[string]any{"Id": "f1"}}},
			deleted: map[accesstypes.Resource]int{"RefusalFolders": 2},
			rows:    map[string]int64{"RefusalFolders": 2},
		},
		{
			name: "the first delete with rows under it answers",
			deletes: []recordedDelete{
				{tree: shipTree(), key: map[string]any{"Id": "x1"}},
				{tree: squadronTree(), key: map[string]any{"Id": "s1"}},
			},
			rows:      map[string]int64{"RefusalMissions": 2},
			want:      "This squadron has 2 missions. Delete them first.",
			wantAsked: []string{"RefusalDroidReports", "RefusalInspections", "RefusalSorties", "RefusalMissions"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			deletes := newTransactionDeletes()
			for _, d := range tt.deletes {
				deletes.recordDelete(d)
			}
			for table, rows := range tt.deleted {
				deletes.deleted[table] = rows
			}
			counts := &tableCounts{rows: tt.rows}

			err := deletes.refuse(t.Context(), SpannerDBType, counts.count)
			if (err != nil) != (tt.want != "") {
				t.Fatalf("refuse() error = %v, want the sentence %q", err, tt.want)
			}
			if tt.want != "" {
				if !httpio.HasConflict(err) {
					t.Errorf("refuse() error = %v, want a conflict", err)
				}
				if got := httpio.Message(err); got != tt.want {
					t.Errorf("httpio.Message() = %q, want %q", got, tt.want)
				}
			}
			if diff := cmp.Diff(tt.wantAsked, counts.asked); diff != "" {
				t.Errorf("the tables counted mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// scopedLists is a caller's checker whose List grant on a resource holds in the scopes
// it is listed under and in no other, and which notes each scope it is asked in.
type scopedLists struct {
	renderStubPermissions
	lists map[accesstypes.Scope][]accesstypes.Resource
	asked map[accesstypes.Scope]int
}

func (s *scopedLists) Check(_ context.Context, _ accesstypes.Environment, scope accesstypes.Scope, _ accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	s.asked[scope]++
	out := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		if slices.Contains(s.lists[scope], res) {
			out[res] = accesstypes.Granted()
		} else {
			out[res] = accesstypes.Denied()
		}
	}

	return out, nil
}

// scopeTree is the description of a record with two kinds of rows under it, taken by
// its delete (takes) or in its way: squadrons, a tenant's rows by a column of their own,
// and notices, rows of no tenant.
func scopeTree(record accesstypes.Resource, one string, tenant *DomainBindingData, takes bool) *DeleteTree {
	kind := func(table accesstypes.Resource, one, many string, tenant *DomainBindingData) DeleteKind {
		return DeleteKind{
			Resource: table,
			Words:    DeleteWords{One: one, Many: many},
			Key:      []string{"Id"},
			Paths:    []DeletePath{{{Columns: []string{"RecordId"}, Table: string(record), References: []string{"Id"}}}},
			Tenant:   tenant,
		}
	}
	kinds := []DeleteKind{
		kind("RefusalNotices", "notice", "notices", nil),
		kind("RefusalSquadrons", "squadron", "squadrons", &DomainBindingData{Column: "SectorId"}),
	}
	tree := &DeleteTree{Record: record, Words: DeleteWords{One: one, Many: one + "s"}, Key: []string{"Id"}, Tenant: tenant}
	if takes {
		tree.Taken = kinds
	} else {
		tree.Under = kinds
	}

	return tree
}

// textTenant is a tenant's key of a type that marshals to text, as a UUID does.
type textTenant struct{ text string }

func (k textTenant) MarshalText() ([]byte, error) {
	return []byte(k.text), nil
}

// namedTenant is a tenant's key of a string-kinded type.
type namedTenant string

// TestRecordedDelete_seen pins the scope each kind is asked about in, on the question
// before a delete and on a refusal that counts: a kind of a tenant's rows under a record
// whose key holds its tenant (the tenant record, or a record keyed by its tenant) in
// that tenant, whatever scope the delete is asked in, and every other kind in the
// delete's own scope, with one check for each scope asked. A person who lists a kind in
// the tenant alone is told its count for the tenant record's delete asked in the global
// scope; one who lists it in the global scope alone, or in another tenant alone, is not.
// A key that marshals to text names its tenant by that text, and a trusted call is told
// every kind.
func TestRecordedDelete_seen(t *testing.T) {
	t.Parallel()

	global, anvil, bastion := accesstypes.GlobalScope(), accesstypes.DomainScope("anvil"), accesstypes.DomainScope("bastion")
	byKey := &DomainBindingData{Column: "Id"}
	bySector := &DomainBindingData{Column: "SectorId"}
	byWing := &DomainBindingData{Column: "WingId", Path: []BindingHop{{Table: "RefusalWings", JoinColumn: "Id", Column: "SectorId"}}}
	counts := map[string]int64{"RefusalSquadrons": 2, "RefusalNotices": 1}
	// counted is what a delete that takes the kinds of the tree counts of each.
	counted := func(tree *DeleteTree) *takenLevels {
		levels := &takenLevels{rows: make(map[*DeleteKind][]int64)}
		for i := range tree.Taken {
			levels.rows[&tree.Taken[i]] = []int64{counts[string(tree.Taken[i].Resource)]}
		}

		return levels
	}

	tests := []struct {
		name   string
		record accesstypes.Resource
		one    string
		tenant *DomainBindingData
		key    map[string]any
		// trusted is a call with no person; scope is the one the delete is asked in.
		trusted      bool
		scope        accesstypes.Scope
		lists        map[accesstypes.Scope][]accesstypes.Resource
		wantQuestion string
		wantRefusal  string
		// wantAsked is the scopes a check was asked in, once each.
		wantAsked []accesstypes.Scope
	}{
		{
			name: "a trusted call is told every kind", record: "RefusalSectors", one: "sector", tenant: byKey, key: map[string]any{"Id": "anvil"},
			trusted:      true,
			wantQuestion: "Delete this sector and everything under it: 2 squadrons, 1 notice. This can't be undone.",
			wantRefusal:  "This sector has 1 notice and 2 squadrons. Delete them first.",
		},
		{
			name: "the tenant record: a tenant's kind in the tenant, and a kind of no tenant in the delete's own scope", record: "RefusalSectors", one: "sector", tenant: byKey, key: map[string]any{"Id": "anvil"},
			scope:        global,
			lists:        map[accesstypes.Scope][]accesstypes.Resource{anvil: {"RefusalSquadrons"}, global: {"RefusalNotices"}},
			wantQuestion: "Delete this sector and everything under it: 2 squadrons, 1 notice. This can't be undone.",
			wantRefusal:  "This sector has 1 notice and 2 squadrons. Delete them first.",
			wantAsked:    []accesstypes.Scope{anvil, global},
		},
		{
			name: "the tenant record: a tenant's kind listed in the global scope alone is not told", record: "RefusalSectors", one: "sector", tenant: byKey, key: map[string]any{"Id": "anvil"},
			scope:        global,
			lists:        map[accesstypes.Scope][]accesstypes.Resource{global: {"RefusalSquadrons", "RefusalNotices"}},
			wantQuestion: "Delete this sector and everything under it: 1 notice, and records you can't see. This can't be undone.",
			wantRefusal:  "This sector has 1 notice. Delete it first.",
			wantAsked:    []accesstypes.Scope{anvil, global},
		},
		{
			name: "the tenant record: a tenant's kind listed in another tenant alone is not told", record: "RefusalSectors", one: "sector", tenant: byKey, key: map[string]any{"Id": "anvil"},
			scope:        global,
			lists:        map[accesstypes.Scope][]accesstypes.Resource{bastion: {"RefusalSquadrons"}, global: {"RefusalNotices"}},
			wantQuestion: "Delete this sector and everything under it: 1 notice, and records you can't see. This can't be undone.",
			wantRefusal:  "This sector has 1 notice. Delete it first.",
			wantAsked:    []accesstypes.Scope{anvil, global},
		},
		{
			name: "the tenant record: a kind of no tenant listed in the tenant alone is not told", record: "RefusalSectors", one: "sector", tenant: byKey, key: map[string]any{"Id": "anvil"},
			scope:        global,
			lists:        map[accesstypes.Scope][]accesstypes.Resource{anvil: {"RefusalSquadrons", "RefusalNotices"}},
			wantQuestion: "Delete this sector and everything under it: 2 squadrons, and records you can't see. This can't be undone.",
			wantRefusal:  "This sector has 2 squadrons. Delete them first.",
			wantAsked:    []accesstypes.Scope{anvil, global},
		},
		{
			name: "a record keyed by its tenant: a tenant's kind in the tenant its key holds", record: "RefusalWings", one: "wing", tenant: bySector, key: map[string]any{"SectorId": "anvil", "Id": "w1"},
			scope:        global,
			lists:        map[accesstypes.Scope][]accesstypes.Resource{anvil: {"RefusalSquadrons", "RefusalNotices"}},
			wantQuestion: "Delete this wing and everything under it: 2 squadrons, and records you can't see. This can't be undone.",
			wantRefusal:  "This wing has 2 squadrons. Delete them first.",
			wantAsked:    []accesstypes.Scope{anvil, global},
		},
		{
			name: "the delete asked in the tenant the key holds is one check", record: "RefusalSectors", one: "sector", tenant: byKey, key: map[string]any{"Id": "anvil"},
			scope:        anvil,
			lists:        map[accesstypes.Scope][]accesstypes.Resource{anvil: {"RefusalSquadrons", "RefusalNotices"}},
			wantQuestion: "Delete this sector and everything under it: 2 squadrons, 1 notice. This can't be undone.",
			wantRefusal:  "This sector has 1 notice and 2 squadrons. Delete them first.",
			wantAsked:    []accesstypes.Scope{anvil},
		},
		{
			name: "a record whose key does not hold its tenant: every kind in the delete's own scope", record: "RefusalWings", one: "wing", tenant: bySector, key: map[string]any{"Id": "anvil"},
			scope:        global,
			lists:        map[accesstypes.Scope][]accesstypes.Resource{anvil: {"RefusalSquadrons", "RefusalNotices"}},
			wantQuestion: "Delete this wing and everything under it? This can't be undone.",
			wantRefusal:  "This wing has records under it that you can't see, so it can't be deleted.",
			wantAsked:    []accesstypes.Scope{global},
		},
		{
			name: "a record that reaches its tenant through another row: every kind in the delete's own scope", record: "RefusalSquadrons", one: "squadron", tenant: byWing, key: map[string]any{"WingId": "anvil", "Id": "q9"},
			scope:        global,
			lists:        map[accesstypes.Scope][]accesstypes.Resource{anvil: {"RefusalSquadrons", "RefusalNotices"}},
			wantQuestion: "Delete this squadron and everything under it? This can't be undone.",
			wantRefusal:  "This squadron has records under it that you can't see, so it can't be deleted.",
			wantAsked:    []accesstypes.Scope{global},
		},
		{
			name: "a record that is no tenant's: every kind in the delete's own scope", record: "RefusalSectors", one: "sector", key: map[string]any{"Id": "anvil"},
			scope:        global,
			lists:        map[accesstypes.Scope][]accesstypes.Resource{anvil: {"RefusalSquadrons", "RefusalNotices"}, global: {"RefusalNotices"}},
			wantQuestion: "Delete this sector and everything under it: 1 notice, and records you can't see. This can't be undone.",
			wantRefusal:  "This sector has 1 notice. Delete it first.",
			wantAsked:    []accesstypes.Scope{global},
		},
		{
			name: "a key that marshals to text names its tenant by the text", record: "RefusalSectors", one: "sector", tenant: byKey, key: map[string]any{"Id": textTenant{text: "anvil"}},
			scope:        global,
			lists:        map[accesstypes.Scope][]accesstypes.Resource{anvil: {"RefusalSquadrons"}, global: {"RefusalNotices"}},
			wantQuestion: "Delete this sector and everything under it: 2 squadrons, 1 notice. This can't be undone.",
			wantRefusal:  "This sector has 1 notice and 2 squadrons. Delete them first.",
			wantAsked:    []accesstypes.Scope{anvil, global},
		},
		{
			name: "a key of a string-kinded type names its tenant by its string", record: "RefusalSectors", one: "sector", tenant: byKey, key: map[string]any{"Id": namedTenant("anvil")},
			scope:        global,
			lists:        map[accesstypes.Scope][]accesstypes.Resource{anvil: {"RefusalSquadrons"}, global: {"RefusalNotices"}},
			wantQuestion: "Delete this sector and everything under it: 2 squadrons, 1 notice. This can't be undone.",
			wantRefusal:  "This sector has 1 notice and 2 squadrons. Delete them first.",
			wantAsked:    []accesstypes.Scope{anvil, global},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			caller := func() (*Caller, *scopedLists) {
				if tt.trusted {
					return nil, &scopedLists{asked: map[accesstypes.Scope]int{}}
				}
				checker := &scopedLists{lists: tt.lists, asked: map[accesstypes.Scope]int{}}

				return &Caller{Permissions: checker, Scope: tt.scope}, checker
			}
			wantAsked := make(map[accesstypes.Scope]int, len(tt.wantAsked))
			for _, scope := range tt.wantAsked {
				wantAsked[scope] = 1
			}

			asker, checker := caller()
			asked := &recordedDelete{tree: scopeTree(tt.record, tt.one, tt.tenant, true), key: tt.key, caller: asker}
			got, err := asked.question(t.Context(), counted(asked.tree))
			if err != nil {
				t.Fatalf("question() error = %v", err)
			}
			if got != tt.wantQuestion {
				t.Errorf("question() = %q, want %q", got, tt.wantQuestion)
			}
			if diff := cmp.Diff(wantAsked, checker.asked, cmp.AllowUnexported(accesstypes.Scope{})); diff != "" {
				t.Errorf("the question's checks by scope mismatch (-want +got):\n%s", diff)
			}

			refuser, checker := caller()
			deletes := newTransactionDeletes()
			deletes.recordDelete(recordedDelete{tree: scopeTree(tt.record, tt.one, tt.tenant, false), key: tt.key, caller: refuser})
			err = deletes.refuse(t.Context(), SpannerDBType, (&tableCounts{rows: counts}).count)
			if got := httpio.Message(err); !httpio.HasConflict(err) || got != tt.wantRefusal {
				t.Errorf("refuse() = %v (%q), want a conflict saying %q", err, got, tt.wantRefusal)
			}
			if diff := cmp.Diff(wantAsked, checker.asked, cmp.AllowUnexported(accesstypes.Scope{})); diff != "" {
				t.Errorf("the refusal's checks by scope mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("a key that names its tenant neither as a string nor as text is an error", func(t *testing.T) {
		t.Parallel()

		asked := &recordedDelete{
			tree:   scopeTree("RefusalSectors", "sector", byKey, true),
			key:    map[string]any{"Id": 7},
			caller: &Caller{Permissions: &scopedLists{asked: map[accesstypes.Scope]int{}}, Scope: global},
		}
		_, err := asked.question(t.Context(), counted(asked.tree))
		if want := "type int is neither string-kinded nor an encoding.TextMarshaler"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("question() error = %v, want one saying %q", err, want)
		}
	})
}

// Test_newRowsUnderStatement pins the count of a kind's rows under a record as each
// database takes it: the kind's own columns against the record's key, by any of its
// ways; a way of more than one step through the rows between; and the rows the
// transaction itself deleted left out, of the kind and of every table on the way. A
// transaction that deleted more rows of a table than a statement leaves out is not
// counted over that table, and a way that ends on a column the record's key does not
// hold is an error. Where the kind's rows and the record are a tenant's, the count is of
// the rows of the record's own tenant: the tenant its key holds, or the one its row
// resolves to, against the tenant each row resolves to, by its column or through other
// rows. A record or a kind that is no tenant's counts every row its ways put there.
func Test_newRowsUnderStatement(t *testing.T) {
	t.Parallel()

	manyWritten := make([]map[string]any, deleteExclusionLimit+1)
	for i := range manyWritten {
		manyWritten[i] = map[string]any{"Id": i}
	}
	bySector := &DomainBindingData{Column: "SectorId"}
	byWing := &DomainBindingData{Column: "WingId", Path: []BindingHop{{Table: "Wings", JoinColumn: "Id", Column: "SectorId"}}}
	byCaseNumber := DeleteLink{Columns: []string{"CaseNumber"}, Table: "Calls", References: []string{"CaseNumber"}}
	sectors := &DeleteTree{Record: "Sectors", Key: []string{"Id"}, Tenant: &DomainBindingData{Column: "Id"}}
	calls := &DeleteTree{Record: "Calls", Key: []string{"Id"}, Tenant: bySector}
	wings := &DeleteTree{Record: "Wings", Key: []string{"Id"}, Tenant: bySector}

	tests := []struct {
		name      string
		dbType    DBType
		tree      *DeleteTree
		kind      DeleteKind
		recordKey map[string]any
		written   map[accesstypes.Resource][]map[string]any
		wantSQL   string
		wantArgs  []any
		wantSkip  bool
	}{
		{
			name:      "one step",
			dbType:    SpannerDBType,
			kind:      under("Missions", "mission", "missions", "AssignedSquadronId", "Squadrons"),
			recordKey: map[string]any{"Id": "s1"},
			wantSQL:   "SELECT COUNT(*) FROM `Missions` WHERE ((`Missions`.`AssignedSquadronId` = @d1))",
			wantArgs:  []any{"s1"},
		},
		{
			name:   "two ways, and the rows the transaction wrote left out",
			dbType: SpannerDBType,
			kind: DeleteKind{Resource: "Transfers", Paths: []DeletePath{
				{{Columns: []string{"FromHangarId"}, Table: "Hangars", References: []string{"Id"}}},
				{{Columns: []string{"ToHangarId"}, Table: "Hangars", References: []string{"Id"}}},
			}},
			recordKey: map[string]any{"Id": "h1"},
			written:   map[accesstypes.Resource][]map[string]any{"Transfers": {{"Id": "t1"}, {"Id": "t2"}}, "Hangars": {{"Id": "h1"}}},
			wantSQL: "SELECT COUNT(*) FROM `Transfers` WHERE ((`Transfers`.`FromHangarId` = @d1) OR (`Transfers`.`ToHangarId` = @d2))" +
				" AND NOT (`Transfers`.`Id` = @d3) AND NOT (`Transfers`.`Id` = @d4)",
			wantArgs: []any{"h1", "h1", "t1", "t2"},
		},
		{
			name:   "a key of two columns, on PostgreSQL",
			dbType: PostgresDBType,
			kind: DeleteKind{Resource: "Ships", Paths: []DeletePath{
				{{Columns: []string{"TenantId", "HomeHangarId"}, Table: "Hangars", References: []string{"TenantId", "Id"}}},
			}},
			recordKey: map[string]any{"TenantId": "anvil", "Id": "h1"},
			written:   map[accesstypes.Resource][]map[string]any{"Ships": {{"TenantId": "anvil", "Id": "x1"}}},
			wantSQL: `SELECT COUNT(*) FROM "Ships" WHERE (("Ships"."TenantId" = $1 AND "Ships"."HomeHangarId" = $2))` +
				` AND NOT ("Ships"."Id" = $3 AND "Ships"."TenantId" = $4)`,
			wantArgs: []any{"anvil", "h1", "x1", "anvil"},
		},
		{
			name:   "a way of two steps, with the rows written on the way left out",
			dbType: SpannerDBType,
			kind: DeleteKind{Resource: "Sorties", Paths: []DeletePath{
				{
					{Columns: []string{"MissionId"}, Table: "Missions", References: []string{"Id"}},
					{Columns: []string{"ClientId"}, Table: "Clients", References: []string{"Id"}},
				},
			}},
			recordKey: map[string]any{"Id": "c1"},
			written:   map[accesstypes.Resource][]map[string]any{"Missions": {{"Id": "m9"}}, "Sorties": {{"Id": "x1"}}},
			wantSQL: "SELECT COUNT(*) FROM `Sorties` WHERE (EXISTS (SELECT 1 FROM `Missions` AS w0_1" +
				" WHERE w0_1.`Id` = `Sorties`.`MissionId` AND w0_1.`ClientId` = @d1 AND NOT (w0_1.`Id` = @d2)))" +
				" AND NOT (`Sorties`.`Id` = @d3)",
			wantArgs: []any{"c1", "m9", "x1"},
		},
		{
			name:      "more rows written than a statement leaves out",
			dbType:    SpannerDBType,
			kind:      under("Missions", "mission", "missions", "AssignedSquadronId", "Squadrons"),
			recordKey: map[string]any{"Id": "s1"},
			written:   map[accesstypes.Resource][]map[string]any{"Missions": manyWritten},
			wantSkip:  true,
		},
		{
			// A foreign key to columns that are not the other table's key: the record's
			// row is read for the column, found by its key.
			name:      "a way that ends on a column the record's key does not hold",
			dbType:    SpannerDBType,
			kind:      DeleteKind{Resource: "Missions", Paths: []DeletePath{{{Columns: []string{"SquadronCode"}, Table: "Squadrons", References: []string{"Code"}}}}},
			recordKey: map[string]any{"Id": "s1"},
			wantSQL: "SELECT COUNT(*) FROM `Missions` WHERE (EXISTS (SELECT 1 FROM `Squadrons` AS w0_r" +
				" WHERE `Missions`.`SquadronCode` = w0_r.`Code` AND w0_r.`Id` = @d1))",
			wantArgs: []any{"s1"},
		},
		{
			name:   "a way of two steps that ends on such a column",
			dbType: SpannerDBType,
			kind: DeleteKind{Resource: "Sorties", Paths: []DeletePath{
				{
					{Columns: []string{"MissionId"}, Table: "Missions", References: []string{"Id"}},
					{Columns: []string{"TenantId", "SquadronCode"}, Table: "Squadrons", References: []string{"TenantId", "Code"}},
				},
			}},
			recordKey: map[string]any{"TenantId": "anvil", "Id": "s1"},
			wantSQL: "SELECT COUNT(*) FROM `Sorties` WHERE (EXISTS (SELECT 1 FROM `Missions` AS w0_1, `Squadrons` AS w0_r" +
				" WHERE w0_1.`Id` = `Sorties`.`MissionId` AND w0_1.`TenantId` = w0_r.`TenantId` AND w0_1.`SquadronCode` = w0_r.`Code`" +
				" AND w0_r.`Id` = @d1 AND w0_r.`TenantId` = @d2))",
			wantArgs: []any{"s1", "anvil"},
		},
		{
			name:      "the tenant record: a kind of a tenant's rows, by a way of two steps",
			dbType:    SpannerDBType,
			tree:      sectors,
			kind:      DeleteKind{Resource: "Relays", Tenant: bySector, Paths: []DeletePath{{byCaseNumber, {Columns: []string{"SectorId"}, Table: "Sectors", References: []string{"Id"}}}}},
			recordKey: map[string]any{"Id": "anvil"},
			wantSQL: "SELECT COUNT(*) FROM `Relays` WHERE (EXISTS (SELECT 1 FROM `Calls` AS w0_1" +
				" WHERE w0_1.`CaseNumber` = `Relays`.`CaseNumber` AND w0_1.`SectorId` = @d1)) AND `Relays`.`SectorId` = @d2",
			wantArgs: []any{"anvil", "anvil"},
		},
		{
			name:      "a record under a tenant: its tenant read from its row by its key",
			dbType:    SpannerDBType,
			tree:      calls,
			kind:      DeleteKind{Resource: "Relays", Tenant: bySector, Paths: []DeletePath{{byCaseNumber}}},
			recordKey: map[string]any{"Id": "c1"},
			wantSQL: "SELECT COUNT(*) FROM `Relays` WHERE (EXISTS (SELECT 1 FROM `Calls` AS w0_r" +
				" WHERE `Relays`.`CaseNumber` = w0_r.`CaseNumber` AND w0_r.`Id` = @d1))" +
				" AND `Relays`.`SectorId` = (SELECT tr.`SectorId` FROM `Calls` AS tr WHERE tr.`Id` = @d2)",
			wantArgs: []any{"c1", "c1"},
		},
		{
			name:      "a kind whose rows reach their tenant through another table",
			dbType:    SpannerDBType,
			tree:      wings,
			kind:      DeleteKind{Resource: "Squadrons", Tenant: byWing, Paths: []DeletePath{{{Columns: []string{"PatrolWingId"}, Table: "Wings", References: []string{"Id"}}}}},
			recordKey: map[string]any{"Id": "w1"},
			wantSQL: "SELECT COUNT(*) FROM `Squadrons` WHERE ((`Squadrons`.`PatrolWingId` = @d1))" +
				" AND (SELECT n1.`SectorId` FROM `Wings` AS n1 WHERE n1.`Id` = `Squadrons`.`WingId`) = (SELECT tr.`SectorId` FROM `Wings` AS tr WHERE tr.`Id` = @d2)",
			wantArgs: []any{"w1", "w1"},
		},
		{
			// The key holds the column the record's way to its tenant leaves through,
			// which is not its tenant: the row is read for it.
			name:      "a record whose own tenant is through another table, keyed by two columns",
			dbType:    SpannerDBType,
			tree:      &DeleteTree{Record: "Squadrons", Key: []string{"WingId", "Id"}, Tenant: byWing},
			kind:      DeleteKind{Resource: "Missions", Tenant: bySector, Paths: []DeletePath{{{Columns: []string{"WingId", "SquadronId"}, Table: "Squadrons", References: []string{"WingId", "Id"}}}}},
			recordKey: map[string]any{"WingId": "w1", "Id": "q1"},
			wantSQL: "SELECT COUNT(*) FROM `Missions` WHERE ((`Missions`.`WingId` = @d1 AND `Missions`.`SquadronId` = @d2))" +
				" AND `Missions`.`SectorId` = (SELECT (SELECT n1.`SectorId` FROM `Wings` AS n1 WHERE n1.`Id` = tr.`WingId`)" +
				" FROM `Squadrons` AS tr WHERE tr.`Id` = @d3 AND tr.`WingId` = @d4)",
			wantArgs: []any{"w1", "q1", "q1", "w1"},
		},
		{
			name:      "the values in the order the text names them, with the rows written left out",
			dbType:    SpannerDBType,
			tree:      calls,
			kind:      DeleteKind{Resource: "Relays", Tenant: bySector, Paths: []DeletePath{{byCaseNumber}}},
			recordKey: map[string]any{"Id": "c1"},
			written:   map[accesstypes.Resource][]map[string]any{"Relays": {{"Id": "r9"}}},
			wantSQL: "SELECT COUNT(*) FROM `Relays` WHERE (EXISTS (SELECT 1 FROM `Calls` AS w0_r" +
				" WHERE `Relays`.`CaseNumber` = w0_r.`CaseNumber` AND w0_r.`Id` = @d1))" +
				" AND `Relays`.`SectorId` = (SELECT tr.`SectorId` FROM `Calls` AS tr WHERE tr.`Id` = @d2) AND NOT (`Relays`.`Id` = @d3)",
			wantArgs: []any{"c1", "c1", "r9"},
		},
		{
			name:      "the tenant record, on PostgreSQL",
			dbType:    PostgresDBType,
			tree:      sectors,
			kind:      DeleteKind{Resource: "Missions", Tenant: bySector, Paths: []DeletePath{{{Columns: []string{"HostSectorId"}, Table: "Sectors", References: []string{"Id"}}}}},
			recordKey: map[string]any{"Id": "anvil"},
			wantSQL:   `SELECT COUNT(*) FROM "Missions" WHERE (("Missions"."HostSectorId" = $1)) AND "Missions"."SectorId" = $2`,
			wantArgs:  []any{"anvil", "anvil"},
		},
		{
			name:      "a record that is no tenant's",
			dbType:    SpannerDBType,
			tree:      &DeleteTree{Record: "Calls", Key: []string{"Id"}},
			kind:      DeleteKind{Resource: "Relays", Tenant: bySector, Paths: []DeletePath{{byCaseNumber}}},
			recordKey: map[string]any{"Id": "c1"},
			wantSQL: "SELECT COUNT(*) FROM `Relays` WHERE (EXISTS (SELECT 1 FROM `Calls` AS w0_r" +
				" WHERE `Relays`.`CaseNumber` = w0_r.`CaseNumber` AND w0_r.`Id` = @d1))",
			wantArgs: []any{"c1"},
		},
		{
			name:      "a kind whose rows are no tenant's",
			dbType:    SpannerDBType,
			tree:      calls,
			kind:      DeleteKind{Resource: "Relays", Paths: []DeletePath{{byCaseNumber}}},
			recordKey: map[string]any{"Id": "c1"},
			wantSQL: "SELECT COUNT(*) FROM `Relays` WHERE (EXISTS (SELECT 1 FROM `Calls` AS w0_r" +
				" WHERE `Relays`.`CaseNumber` = w0_r.`CaseNumber` AND w0_r.`Id` = @d1))",
			wantArgs: []any{"c1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := newRowsUnderStatement(tt.dbType, tt.tree, &tt.kind, tt.recordKey, tt.written)
			if ok == tt.wantSkip {
				t.Fatalf("newRowsUnderStatement() ok = %v, want %v", ok, !tt.wantSkip)
			}
			if got.SQL != tt.wantSQL {
				t.Errorf("newRowsUnderStatement() SQL mismatch\n got: %s\nwant: %s", got.SQL, tt.wantSQL)
			}
			if diff := cmp.Diff(tt.wantArgs, got.Args); diff != "" {
				t.Errorf("newRowsUnderStatement() values mismatch (-want +got):\n%s", diff)
			}
		})
	}

	statement := rowsUnderStatement{SQL: "SELECT 1", Args: []any{"h1", int64(2)}}.spannerStatement()
	if diff := cmp.Diff(map[string]any{"d1": "h1", "d2": int64(2)}, statement.Params); diff != "" {
		t.Errorf("spannerStatement() names its values mismatch (-want +got):\n%s", diff)
	}
}

// refusalSquadron is a resource whose generated description names a kind under it.
type refusalSquadron struct {
	ID   string `spanner:"Id"`
	Name string `spanner:"Name"`
}

func (refusalSquadron) Resource() accesstypes.Resource {
	return "RefusalSquadrons"
}

func (refusalSquadron) DeleteTree() *DeleteTree {
	return squadronTree()
}

// bufferer is a patch as a test buffers it.
type bufferer interface {
	Buffer(ctx context.Context, txn ReadWriteTransaction, eventSource ...string) error
}

// countingTxn is a transaction that keeps the record of the deletes to count over, as
// the Spanner wrapper does.
type countingTxn struct {
	bufferingTxn
	*transactionDeletes
}

// TestPatchSet_recordsDeletes pins what a patch notes on its transaction for the check
// before the commit: a delete of a record whose description names kinds under it is
// noted with the record's key by column and who it is for, and its row as one the
// transaction deleted; an update's row as one the transaction changed, which no count
// leaves out; a create nothing; and the delete of a record with no description its row
// alone. The person is the one the patch is armed against, else the caller the request
// stamped, else nobody.
func TestPatchSet_recordsDeletes(t *testing.T) {
	t.Parallel()

	armed := whoLists(nil)
	stamped := whoLists(accesstypes.Decisions{"RefusalMissions": accesstypes.Denied()})

	tests := []struct {
		name        string
		patch       func() bufferer
		ctx         func(ctx context.Context) context.Context
		wantWritten map[accesstypes.Resource][]map[string]any
		wantDeleted map[accesstypes.Resource]int
		// wantChanged are the rows noted as changed and not deleted.
		wantChanged map[accesstypes.Resource][]map[string]any
		wantCounted bool
		// wantLists is whether the person the delete is noted for may list missions: the
		// armed person may, the stamped caller may not.
		wantLists    bool
		wantNoPerson bool
	}{
		{
			name: "a trusted delete of a record with a kind under it",
			patch: func() bufferer {
				p := NewPatchSet(NewMetadata[refusalSquadron]()).SetPatchType(DeletePatchType)
				p.SetKey("ID", "s1")

				return p
			},
			wantWritten:  map[accesstypes.Resource][]map[string]any{"RefusalSquadrons": {{"Id": "s1"}}},
			wantDeleted:  map[accesstypes.Resource]int{"RefusalSquadrons": 1},
			wantCounted:  true,
			wantNoPerson: true,
		},
		{
			name: "an armed delete is for the person the patch is armed against",
			patch: func() bufferer {
				p := NewPatchSet(NewMetadata[refusalSquadron]()).SetPatchType(DeletePatchType)
				p.SetKey("ID", "s1")
				p.Enforce(armed, nil, accesstypes.Delete)

				return p
			},
			ctx: func(ctx context.Context) context.Context {
				return WithCaller(ctx, stamped)
			},
			wantWritten: map[accesstypes.Resource][]map[string]any{"RefusalSquadrons": {{"Id": "s1"}}},
			wantDeleted: map[accesstypes.Resource]int{"RefusalSquadrons": 1},
			wantCounted: true,
			wantLists:   true,
		},
		{
			name: "a delete that is not armed is for the caller the request stamped",
			patch: func() bufferer {
				p := NewPatchSet(NewMetadata[refusalSquadron]()).SetPatchType(DeletePatchType)
				p.SetKey("ID", "s1")

				return p
			},
			ctx: func(ctx context.Context) context.Context {
				return WithCaller(ctx, stamped)
			},
			wantWritten: map[accesstypes.Resource][]map[string]any{"RefusalSquadrons": {{"Id": "s1"}}},
			wantDeleted: map[accesstypes.Resource]int{"RefusalSquadrons": 1},
			wantCounted: true,
			wantLists:   false,
		},
		{
			name: "a delete that answered for the rows under its record when it was buffered is not counted over",
			patch: func() bufferer {
				p := NewPatchSet(NewMetadata[refusalSquadron]()).SetPatchType(DeletePatchType)
				p.SetKey("ID", "s1")
				p.underAnswered = true

				return p
			},
			wantWritten: map[accesstypes.Resource][]map[string]any{"RefusalSquadrons": {{"Id": "s1"}}},
			wantDeleted: map[accesstypes.Resource]int{"RefusalSquadrons": 1},
		},
		{
			name: "an update's row is one the transaction changed",
			patch: func() bufferer {
				p := NewPatchSet(NewMetadata[refusalSquadron]()).SetPatchType(UpdatePatchType)
				p.SetKey("ID", "s1")
				p.Set("Name", "Hammer")

				return p
			},
			wantWritten: map[accesstypes.Resource][]map[string]any{},
			wantDeleted: map[accesstypes.Resource]int{},
			wantChanged: map[accesstypes.Resource][]map[string]any{"RefusalSquadrons": {{"Id": "s1"}}},
		},
		{
			name: "a create notes nothing",
			patch: func() bufferer {
				p := NewPatchSet(NewMetadata[refusalSquadron]()).SetPatchType(CreatePatchType)
				p.SetKey("ID", "s1")
				p.Set("Name", "Hammer")

				return p
			},
			wantWritten: map[accesstypes.Resource][]map[string]any{},
			wantDeleted: map[accesstypes.Resource]int{},
		},
		{
			name: "the delete of a record with no description notes its row alone",
			patch: func() bufferer {
				p := NewPatchSet(NewMetadata[forkRow]()).SetPatchType(DeletePatchType)
				p.SetKey("ID", "row-1")

				return p
			},
			wantWritten: map[accesstypes.Resource][]map[string]any{"ForkRows": {{"Id": "row-1"}}},
			wantDeleted: map[accesstypes.Resource]int{"ForkRows": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(ctx)
			}
			txn := &countingTxn{transactionDeletes: newTransactionDeletes()}
			if err := tt.patch().Buffer(ctx, txn); err != nil {
				t.Fatalf("PatchSet.Buffer() error = %v", err)
			}

			if diff := cmp.Diff(tt.wantWritten, txn.gone); diff != "" {
				t.Errorf("the rows noted as deleted, which a count leaves out, mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantDeleted, txn.deleted); diff != "" {
				t.Errorf("the deletes noted by table mismatch (-want +got):\n%s", diff)
			}
			for table, keys := range tt.wantWritten {
				for _, key := range keys {
					if !txn.deletedRow(table, key) || txn.changedRow(table, key) {
						t.Errorf("the transaction does not answer that it deleted the row %v of %s", key, table)
					}
				}
			}
			for table, keys := range tt.wantChanged {
				for _, key := range keys {
					if !txn.changedRow(table, key) || txn.deletedRow(table, key) {
						t.Errorf("the transaction does not answer that it changed the row %v of %s without deleting it", key, table)
					}
				}
			}
			another := map[string]any{"Id": "another"}
			if txn.deletedRow("RefusalSquadrons", another) || txn.changedRow("RefusalSquadrons", another) {
				t.Error("the transaction answers that it wrote a row it did not")
			}
			if !tt.wantCounted {
				if len(txn.deletes) != 0 {
					t.Fatalf("%d deletes are noted to count over, want none", len(txn.deletes))
				}

				return
			}
			if len(txn.deletes) != 1 {
				t.Fatalf("%d deletes are noted to count over, want 1", len(txn.deletes))
			}
			noted := txn.deletes[0]
			if noted.tree.Record != "RefusalSquadrons" {
				t.Errorf("the delete is noted with the description of %s, want RefusalSquadrons", noted.tree.Record)
			}
			if diff := cmp.Diff(map[string]any{"Id": "s1"}, noted.key); diff != "" {
				t.Errorf("the noted record's key mismatch (-want +got):\n%s", diff)
			}
			if tt.wantNoPerson != noted.caller.IsZero() {
				t.Errorf("the delete is noted for nobody = %v, want %v", noted.caller.IsZero(), tt.wantNoPerson)
			}
			if !tt.wantNoPerson {
				decisions, err := noted.caller.Check(ctx, accesstypes.List, "RefusalMissions")
				if err != nil {
					t.Fatalf("Caller.Check() error = %v", err)
				}
				if lists := decisions["RefusalMissions"].IsGranted(); lists != tt.wantLists {
					t.Errorf("the delete is noted for a person who may list missions = %v, want %v", lists, tt.wantLists)
				}
			}
		})
	}
}
