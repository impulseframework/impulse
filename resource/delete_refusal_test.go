package resource

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
)

// under is a kind with rows directly under a record, by one column that holds the
// record's Id.
func under(table accesstypes.Resource, one, many, column, record string) DeleteKind {
	return DeleteKind{
		Resource: table,
		Words:    DeleteWords{One: one, Many: many},
		Key:      []string{"Id"},
		Paths:    []DeletePath{{{Columns: []string{column}, Table: record, References: []string{"Id"}}}},
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
		Under:  []DeleteKind{under("RefusalMissions", "mission", "missions", "AssignedSquadronId", "RefusalSquadrons")},
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
		Under:  []DeleteKind{under("RefusalFolders", "folder", "folders", "ShortcutToId", "RefusalFolders")},
	}
}

// tableCounts answers each count statement with the rows held for the table it reads,
// and notes the tables asked, in order.
type tableCounts struct {
	rows  map[string]int64
	asked []string
}

func (c *tableCounts) count(_ context.Context, statement rowsUnderStatement) (int64, error) {
	table, _, _ := strings.Cut(strings.TrimPrefix(statement.SQL, "SELECT COUNT(*) FROM (SELECT `"), "`")
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
// counted for them; and one count runs per kind. Where every kind
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
			name:      "a folder under the folder is counted",
			deletes:   []recordedDelete{{tree: folderTree(), key: map[string]any{"Id": "f1"}}},
			rows:      map[string]int64{"RefusalFolders": 2},
			want:      "This folder has 2 folders. Delete them first.",
			wantAsked: []string{"RefusalFolders"},
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
			got, err := asked.question(t.Context(), counted(asked.tree), nil)
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
		_, err := asked.question(t.Context(), counted(asked.tree), nil)
		if want := "type int is neither string-kinded nor an encoding.TextMarshaler"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("question() error = %v, want one saying %q", err, want)
		}
	})
}

// Test_newRowsUnderStatement pins the count of a kind's rows under a record as each
// database takes it: the keys of the kind's rows, read under their table's name, one
// SELECT for each of its ways joined by a union that holds each row once, each way
// written from the record's key down: the kind's own columns against the record's key; a
// way of more than one step from the table its last step leaves, held to the key, joined
// down through the rows between, nothing left out; and a way that ends on a column the
// record's key does not hold from the record's own row. Where the kind's rows and the
// record are a tenant's, the count is of the rows of the record's own tenant: the tenant
// its key holds, or the one its row resolves to, against the tenant each row resolves
// to, by its column or through other rows. Under a record whose key holds its tenant,
// where one of the kind's ways is its way to its tenant, that way alone is written, with
// no tenant's condition: by the kind's own column, or through the table it reaches its
// tenant by. A way whose last step references more than the tenant's column is not that
// way. A record or a kind that is no tenant's counts every row its ways put there.
func Test_newRowsUnderStatement(t *testing.T) {
	t.Parallel()

	bySector := &DomainBindingData{Column: "SectorId"}
	byWing := &DomainBindingData{Column: "WingId", Path: []BindingHop{{Table: "Wings", JoinColumn: "Id", Column: "SectorId"}}}
	byCaseNumber := DeleteLink{Columns: []string{"CaseNumber"}, Table: "Calls", References: []string{"CaseNumber"}}
	sectors := &DeleteTree{Record: "Sectors", Key: []string{"Id"}, Tenant: &DomainBindingData{Column: "Id"}}
	calls := &DeleteTree{Record: "Calls", Key: []string{"Id"}, Tenant: bySector}
	wings := &DeleteTree{Record: "Wings", Key: []string{"Id"}, Tenant: bySector}
	bySectorID := DeleteLink{Columns: []string{"SectorId"}, Table: "Sectors", References: []string{"Id"}}

	tests := []struct {
		name      string
		dbType    DBType
		tree      *DeleteTree
		kind      DeleteKind
		recordKey map[string]any
		wantSQL   string
		wantArgs  []any
	}{
		{
			name:      "one step",
			dbType:    SpannerDBType,
			kind:      under("Missions", "mission", "missions", "AssignedSquadronId", "Squadrons"),
			recordKey: map[string]any{"Id": "s1"},
			wantSQL:   "SELECT COUNT(*) FROM (SELECT `Missions`.`Id` AS k1 FROM `Missions` WHERE `Missions`.`AssignedSquadronId` = @d1) AS u",
			wantArgs:  []any{"s1"},
		},
		{
			name:   "two ways",
			dbType: SpannerDBType,
			kind: DeleteKind{Resource: "Transfers", Key: []string{"Id"}, Paths: []DeletePath{
				{{Columns: []string{"FromHangarId"}, Table: "Hangars", References: []string{"Id"}}},
				{{Columns: []string{"ToHangarId"}, Table: "Hangars", References: []string{"Id"}}},
			}},
			recordKey: map[string]any{"Id": "h1"},
			wantSQL: "SELECT COUNT(*) FROM (SELECT `Transfers`.`Id` AS k1 FROM `Transfers` WHERE `Transfers`.`FromHangarId` = @d1" +
				" UNION DISTINCT SELECT `Transfers`.`Id` AS k1 FROM `Transfers` WHERE `Transfers`.`ToHangarId` = @d2) AS u",
			wantArgs: []any{"h1", "h1"},
		},
		{
			name:   "a key of two columns, on PostgreSQL",
			dbType: PostgresDBType,
			kind: DeleteKind{Resource: "Ships", Key: []string{"Id"}, Paths: []DeletePath{
				{{Columns: []string{"TenantId", "HomeHangarId"}, Table: "Hangars", References: []string{"TenantId", "Id"}}},
			}},
			recordKey: map[string]any{"TenantId": "anvil", "Id": "h1"},
			wantSQL:   `SELECT COUNT(*) FROM (SELECT "Ships"."Id" AS k1 FROM "Ships" WHERE "Ships"."TenantId" = $1 AND "Ships"."HomeHangarId" = $2) AS u`,
			wantArgs:  []any{"anvil", "h1"},
		},
		{
			name:   "a way of two steps",
			dbType: SpannerDBType,
			kind: DeleteKind{Resource: "Sorties", Key: []string{"Id"}, Paths: []DeletePath{
				{
					{Columns: []string{"MissionId"}, Table: "Missions", References: []string{"Id"}},
					{Columns: []string{"ClientId"}, Table: "Clients", References: []string{"Id"}},
				},
			}},
			recordKey: map[string]any{"Id": "c1"},
			wantSQL:   "SELECT COUNT(*) FROM (SELECT `Sorties`.`Id` AS k1 FROM `Missions` AS w0_1 JOIN `Sorties` ON `Sorties`.`MissionId` = w0_1.`Id` WHERE w0_1.`ClientId` = @d1) AS u",
			wantArgs:  []any{"c1"},
		},
		{
			// A foreign key to columns that are not the other table's key: the record's
			// row is read for the column, found by its key.
			name:      "a way that ends on a column the record's key does not hold",
			dbType:    SpannerDBType,
			kind:      DeleteKind{Resource: "Missions", Key: []string{"Id"}, Paths: []DeletePath{{{Columns: []string{"SquadronCode"}, Table: "Squadrons", References: []string{"Code"}}}}},
			recordKey: map[string]any{"Id": "s1"},
			wantSQL:   "SELECT COUNT(*) FROM (SELECT `Missions`.`Id` AS k1 FROM `Squadrons` AS w0_r JOIN `Missions` ON `Missions`.`SquadronCode` = w0_r.`Code` WHERE w0_r.`Id` = @d1) AS u",
			wantArgs:  []any{"s1"},
		},
		{
			name:   "a way of two steps that ends on such a column",
			dbType: SpannerDBType,
			kind: DeleteKind{Resource: "Sorties", Key: []string{"Id"}, Paths: []DeletePath{
				{
					{Columns: []string{"MissionId"}, Table: "Missions", References: []string{"Id"}},
					{Columns: []string{"TenantId", "SquadronCode"}, Table: "Squadrons", References: []string{"TenantId", "Code"}},
				},
			}},
			recordKey: map[string]any{"TenantId": "anvil", "Id": "s1"},
			wantSQL: "SELECT COUNT(*) FROM (SELECT `Sorties`.`Id` AS k1 FROM `Squadrons` AS w0_r" +
				" JOIN `Missions` AS w0_1 ON w0_1.`TenantId` = w0_r.`TenantId` AND w0_1.`SquadronCode` = w0_r.`Code`" +
				" JOIN `Sorties` ON `Sorties`.`MissionId` = w0_1.`Id` WHERE w0_r.`Id` = @d1 AND w0_r.`TenantId` = @d2) AS u",
			wantArgs: []any{"s1", "anvil"},
		},
		{
			name:      "the tenant record: a kind of a tenant's rows, by a way of two steps",
			dbType:    SpannerDBType,
			tree:      sectors,
			kind:      DeleteKind{Resource: "Relays", Key: []string{"Id"}, Tenant: bySector, Paths: []DeletePath{{byCaseNumber, {Columns: []string{"SectorId"}, Table: "Sectors", References: []string{"Id"}}}}},
			recordKey: map[string]any{"Id": "anvil"},
			wantSQL: "SELECT COUNT(*) FROM (SELECT `Relays`.`Id` AS k1 FROM `Calls` AS w0_1 JOIN `Relays` ON `Relays`.`CaseNumber` = w0_1.`CaseNumber`" +
				" WHERE w0_1.`SectorId` = @d1 AND `Relays`.`SectorId` = @d2) AS u",
			wantArgs: []any{"anvil", "anvil"},
		},
		{
			name:      "a record under a tenant: its tenant read from its row by its key",
			dbType:    SpannerDBType,
			tree:      calls,
			kind:      DeleteKind{Resource: "Relays", Key: []string{"Id"}, Tenant: bySector, Paths: []DeletePath{{byCaseNumber}}},
			recordKey: map[string]any{"Id": "c1"},
			wantSQL: "SELECT COUNT(*) FROM (SELECT `Relays`.`Id` AS k1 FROM `Calls` AS w0_r JOIN `Relays` ON `Relays`.`CaseNumber` = w0_r.`CaseNumber`" +
				" WHERE w0_r.`Id` = @d1 AND `Relays`.`SectorId` = (SELECT tr.`SectorId` FROM `Calls` AS tr WHERE tr.`Id` = @d2)) AS u",
			wantArgs: []any{"c1", "c1"},
		},
		{
			name:      "a kind whose rows reach their tenant through another table",
			dbType:    SpannerDBType,
			tree:      wings,
			kind:      DeleteKind{Resource: "Squadrons", Key: []string{"Id"}, Tenant: byWing, Paths: []DeletePath{{{Columns: []string{"PatrolWingId"}, Table: "Wings", References: []string{"Id"}}}}},
			recordKey: map[string]any{"Id": "w1"},
			wantSQL: "SELECT COUNT(*) FROM (SELECT `Squadrons`.`Id` AS k1 FROM `Squadrons` WHERE `Squadrons`.`PatrolWingId` = @d1" +
				" AND (SELECT n1.`SectorId` FROM `Wings` AS n1 WHERE n1.`Id` = `Squadrons`.`WingId`) = (SELECT tr.`SectorId` FROM `Wings` AS tr WHERE tr.`Id` = @d2)) AS u",
			wantArgs: []any{"w1", "w1"},
		},
		{
			// The key holds the column the record's way to its tenant leaves through,
			// which is not its tenant: the row is read for it.
			name:      "a record whose own tenant is through another table, keyed by two columns",
			dbType:    SpannerDBType,
			tree:      &DeleteTree{Record: "Squadrons", Key: []string{"WingId", "Id"}, Tenant: byWing},
			kind:      DeleteKind{Resource: "Missions", Key: []string{"Id"}, Tenant: bySector, Paths: []DeletePath{{{Columns: []string{"WingId", "SquadronId"}, Table: "Squadrons", References: []string{"WingId", "Id"}}}}},
			recordKey: map[string]any{"WingId": "w1", "Id": "q1"},
			wantSQL: "SELECT COUNT(*) FROM (SELECT `Missions`.`Id` AS k1 FROM `Missions` WHERE `Missions`.`WingId` = @d1 AND `Missions`.`SquadronId` = @d2" +
				" AND `Missions`.`SectorId` = (SELECT (SELECT n1.`SectorId` FROM `Wings` AS n1 WHERE n1.`Id` = tr.`WingId`)" +
				" FROM `Squadrons` AS tr WHERE tr.`Id` = @d3 AND tr.`WingId` = @d4)) AS u",
			wantArgs: []any{"w1", "q1", "q1", "w1"},
		},
		{
			name:      "the values in the order the text names them",
			dbType:    SpannerDBType,
			tree:      calls,
			kind:      DeleteKind{Resource: "Relays", Key: []string{"Id"}, Tenant: bySector, Paths: []DeletePath{{byCaseNumber}}},
			recordKey: map[string]any{"Id": "c1"},
			wantSQL: "SELECT COUNT(*) FROM (SELECT `Relays`.`Id` AS k1 FROM `Calls` AS w0_r JOIN `Relays` ON `Relays`.`CaseNumber` = w0_r.`CaseNumber`" +
				" WHERE w0_r.`Id` = @d1 AND `Relays`.`SectorId` = (SELECT tr.`SectorId` FROM `Calls` AS tr WHERE tr.`Id` = @d2)) AS u",
			wantArgs: []any{"c1", "c1"},
		},
		{
			name:      "the tenant record, on PostgreSQL",
			dbType:    PostgresDBType,
			tree:      sectors,
			kind:      DeleteKind{Resource: "Missions", Key: []string{"Id"}, Tenant: bySector, Paths: []DeletePath{{{Columns: []string{"HostSectorId"}, Table: "Sectors", References: []string{"Id"}}}}},
			recordKey: map[string]any{"Id": "anvil"},
			wantSQL:   `SELECT COUNT(*) FROM (SELECT "Missions"."Id" AS k1 FROM "Missions" WHERE "Missions"."HostSectorId" = $1 AND "Missions"."SectorId" = $2) AS u`,
			wantArgs:  []any{"anvil", "anvil"},
		},
		{
			name:      "the tenant record: a kind that holds its tenant in a column of its own is read by that column alone",
			dbType:    SpannerDBType,
			tree:      sectors,
			kind:      DeleteKind{Resource: "Missions", Key: []string{"Id"}, Tenant: bySector, Paths: []DeletePath{{bySectorID}}},
			recordKey: map[string]any{"Id": "anvil"},
			wantSQL:   "SELECT COUNT(*) FROM (SELECT `Missions`.`Id` AS k1 FROM `Missions` WHERE `Missions`.`SectorId` = @d1) AS u",
			wantArgs:  []any{"anvil"},
		},
		{
			name:   "the tenant record: of two ways, the kind's way to its tenant alone is written",
			dbType: SpannerDBType,
			tree:   sectors,
			kind: DeleteKind{Resource: "Missions", Key: []string{"Id"}, Tenant: bySector, Paths: []DeletePath{
				{{Columns: []string{"HostSectorId"}, Table: "Sectors", References: []string{"Id"}}},
				{bySectorID},
			}},
			recordKey: map[string]any{"Id": "anvil"},
			wantSQL:   "SELECT COUNT(*) FROM (SELECT `Missions`.`Id` AS k1 FROM `Missions` WHERE `Missions`.`SectorId` = @d1) AS u",
			wantArgs:  []any{"anvil"},
		},
		{
			name:   "the tenant record: a kind that reaches its tenant through another table is read by that way alone",
			dbType: SpannerDBType,
			tree:   sectors,
			kind: DeleteKind{Resource: "Squadrons", Key: []string{"Id"}, Tenant: byWing, Paths: []DeletePath{
				{{Columns: []string{"PatrolWingId"}, Table: "Wings", References: []string{"Id"}}, bySectorID},
				{{Columns: []string{"WingId"}, Table: "Wings", References: []string{"Id"}}, bySectorID},
			}},
			recordKey: map[string]any{"Id": "anvil"},
			wantSQL:   "SELECT COUNT(*) FROM (SELECT `Squadrons`.`Id` AS k1 FROM `Wings` AS w0_1 JOIN `Squadrons` ON `Squadrons`.`WingId` = w0_1.`Id` WHERE w0_1.`SectorId` = @d1) AS u",
			wantArgs:  []any{"anvil"},
		},
		{
			name:      "the tenant record: a kind read by its tenant's column alone, on PostgreSQL",
			dbType:    PostgresDBType,
			tree:      sectors,
			kind:      DeleteKind{Resource: "Missions", Key: []string{"Id"}, Tenant: bySector, Paths: []DeletePath{{bySectorID}}},
			recordKey: map[string]any{"Id": "anvil"},
			wantSQL:   `SELECT COUNT(*) FROM (SELECT "Missions"."Id" AS k1 FROM "Missions" WHERE "Missions"."SectorId" = $1) AS u`,
			wantArgs:  []any{"anvil"},
		},
		{
			// The last step references the record's tenant's column and another: it is not
			// the kind's way to its tenant, and the tenant's condition stays.
			name:   "a record keyed by its tenant: a way whose last step references more than the tenant's column keeps the tenant's condition",
			dbType: SpannerDBType,
			tree:   &DeleteTree{Record: "Wings", Key: []string{"SectorId", "Id"}, Tenant: bySector},
			kind: DeleteKind{Resource: "Squadrons", Key: []string{"Id"}, Tenant: bySector, Paths: []DeletePath{
				{{Columns: []string{"SectorId", "WingId"}, Table: "Wings", References: []string{"SectorId", "Id"}}},
			}},
			recordKey: map[string]any{"SectorId": "anvil", "Id": "w1"},
			wantSQL:   "SELECT COUNT(*) FROM (SELECT `Squadrons`.`Id` AS k1 FROM `Squadrons` WHERE `Squadrons`.`SectorId` = @d1 AND `Squadrons`.`WingId` = @d2 AND `Squadrons`.`SectorId` = @d3) AS u",
			wantArgs:  []any{"anvil", "w1", "anvil"},
		},
		{
			name:      "a record that is no tenant's",
			dbType:    SpannerDBType,
			tree:      &DeleteTree{Record: "Calls", Key: []string{"Id"}},
			kind:      DeleteKind{Resource: "Relays", Key: []string{"Id"}, Tenant: bySector, Paths: []DeletePath{{byCaseNumber}}},
			recordKey: map[string]any{"Id": "c1"},
			wantSQL:   "SELECT COUNT(*) FROM (SELECT `Relays`.`Id` AS k1 FROM `Calls` AS w0_r JOIN `Relays` ON `Relays`.`CaseNumber` = w0_r.`CaseNumber` WHERE w0_r.`Id` = @d1) AS u",
			wantArgs:  []any{"c1"},
		},
		{
			name:      "a kind whose rows are no tenant's",
			dbType:    SpannerDBType,
			tree:      calls,
			kind:      DeleteKind{Resource: "Relays", Key: []string{"Id"}, Paths: []DeletePath{{byCaseNumber}}},
			recordKey: map[string]any{"Id": "c1"},
			wantSQL:   "SELECT COUNT(*) FROM (SELECT `Relays`.`Id` AS k1 FROM `Calls` AS w0_r JOIN `Relays` ON `Relays`.`CaseNumber` = w0_r.`CaseNumber` WHERE w0_r.`Id` = @d1) AS u",
			wantArgs:  []any{"c1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := newRowsUnderStatement(tt.dbType, tt.tree, &tt.kind, tt.recordKey)
			if !ok {
				t.Fatal("newRowsUnderStatement() wrote no count")
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

// Test_tenantsWay holds which of a kind's ways is its way to its tenant under a record
// whose key holds its tenant: the way that follows, column for column, the links the
// kind's tenant is found by and ends on the record's tenant column, the first of two that
// do. A kind or a record that is no tenant's, a record that reaches its tenant through
// another row, a key without the tenant's column, a way one link longer or shorter, a link
// of two columns, a link to another table than the tenant's way names and a last link
// that references two columns have none.
func Test_tenantsWay(t *testing.T) {
	t.Parallel()

	bySector := &DomainBindingData{Column: "SectorId"}
	byWing := &DomainBindingData{Column: "WingId", Path: []BindingHop{{Table: "Wings", JoinColumn: "Id", Column: "SectorId"}}}
	bySquadron := &DomainBindingData{Column: "SquadronId", Path: []BindingHop{
		{Table: "Squadrons", JoinColumn: "Id", Column: "WingId"},
		{Table: "Wings", JoinColumn: "Id", Column: "SectorId"},
	}}
	toSector := DeleteLink{Columns: []string{"SectorId"}, Table: "Sectors", References: []string{"Id"}}
	toWing := DeleteLink{Columns: []string{"WingId"}, Table: "Wings", References: []string{"Id"}}
	sectors := &DeleteTree{Record: "Sectors", Key: []string{"Id"}, Tenant: &DomainBindingData{Column: "Id"}}
	inSector := map[string]any{"Id": "anvil"}

	tests := []struct {
		name      string
		tree      *DeleteTree
		tenant    *DomainBindingData
		recordKey map[string]any
		ways      []DeletePath
		want      DeletePath
	}{
		{name: "a kind with no tenant", tree: sectors, recordKey: inSector, ways: []DeletePath{{toSector}}},
		{name: "a record with no tenant", tree: &DeleteTree{Record: "Sectors", Key: []string{"Id"}}, tenant: bySector, recordKey: inSector, ways: []DeletePath{{toSector}}},
		{
			name:      "a record that reaches its tenant through another row",
			tree:      &DeleteTree{Record: "Wings", Key: []string{"Id"}, Tenant: &DomainBindingData{Column: "HangarId", Path: []BindingHop{{Table: "Hangars", JoinColumn: "Id", Column: "SectorId"}}}},
			tenant:    bySector,
			recordKey: map[string]any{"Id": "w1", "HangarId": "h1"},
			ways:      []DeletePath{{{Columns: []string{"SectorId"}, Table: "Wings", References: []string{"HangarId"}}}},
		},
		{
			name:      "a key that does not hold the tenant's column",
			tree:      &DeleteTree{Record: "Wings", Key: []string{"Id"}, Tenant: bySector},
			tenant:    bySector,
			recordKey: map[string]any{"Id": "w1"},
			ways:      []DeletePath{{{Columns: []string{"SectorId"}, Table: "Wings", References: []string{"SectorId"}}}},
		},
		{
			name: "a way one link longer than the tenant's way", tree: sectors, tenant: bySector, recordKey: inSector,
			ways: []DeletePath{{{Columns: []string{"SectorId"}, Table: "Calls", References: []string{"Id"}}, toSector}},
		},
		{name: "a way one link shorter than the tenant's way", tree: sectors, tenant: byWing, recordKey: inSector, ways: []DeletePath{{{Columns: []string{"WingId"}, Table: "Sectors", References: []string{"Id"}}}}},
		{
			name: "a link of two columns", tree: sectors, tenant: byWing, recordKey: inSector,
			ways: []DeletePath{{{Columns: []string{"WingId", "SectorId"}, Table: "Wings", References: []string{"Id", "SectorId"}}, toSector}},
		},
		{
			name: "a link to the right column of another table than the tenant's way names", tree: sectors, tenant: byWing, recordKey: inSector,
			ways: []DeletePath{{{Columns: []string{"WingId"}, Table: "Hangars", References: []string{"Id"}}, toSector}},
		},
		{
			name:      "a last link that references two columns",
			tree:      &DeleteTree{Record: "Wings", Key: []string{"SectorId", "Id"}, Tenant: bySector},
			tenant:    bySector,
			recordKey: map[string]any{"SectorId": "anvil", "Id": "w1"},
			ways:      []DeletePath{{{Columns: []string{"SectorId", "WingId"}, Table: "Wings", References: []string{"SectorId", "Id"}}}},
		},
		{name: "the match, with no step between the row and its tenant", tree: sectors, tenant: bySector, recordKey: inSector, ways: []DeletePath{{toSector}}, want: DeletePath{toSector}},
		{
			name: "the match, with two steps between the row and its tenant", tree: sectors, tenant: bySquadron, recordKey: inSector,
			ways: []DeletePath{{{Columns: []string{"SquadronId"}, Table: "Squadrons", References: []string{"Id"}}, toWing, toSector}},
			want: DeletePath{{Columns: []string{"SquadronId"}, Table: "Squadrons", References: []string{"Id"}}, toWing, toSector},
		},
		{
			name: "two matching ways: the first is answered", tree: sectors, tenant: byWing, recordKey: inSector,
			ways: []DeletePath{
				{{Columns: []string{"PatrolWingId"}, Table: "Wings", References: []string{"Id"}}, toSector},
				{{Columns: []string{"WingId"}, Table: "Wings", References: []string{"Id"}, MayBeEmpty: true}, toSector},
				{toWing, toSector},
			},
			want: DeletePath{{Columns: []string{"WingId"}, Table: "Wings", References: []string{"Id"}, MayBeEmpty: true}, toSector},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tenantsWay(tt.tree, &DeleteKind{Resource: "Rows", Key: []string{"Id"}, Tenant: tt.tenant}, tt.recordKey, tt.ways)
			if ok != (tt.want != nil) {
				t.Fatalf("tenantsWay() answered a way = %t, want %t", ok, tt.want != nil)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("tenantsWay() (-want +got):\n%s", diff)
			}
		})
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
	Buffer(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) error
}

// countingTxn is a transaction that keeps the record of the deletes to count over, as
// the Spanner wrapper does.
type countingTxn struct {
	bufferingTxn
	*transactionDeletes
}

// TestPatchSet_recordsDeletes pins what a patch notes on its transaction for the check
// before the commit: a delete of a record whose description names kinds under it is
// noted with the record's key by column and who it is for; an update, a create and the
// delete of a record with no description nothing. The person is the one the patch is
// armed against, else the caller the request stamped, else nobody.
func TestPatchSet_recordsDeletes(t *testing.T) {
	t.Parallel()

	armed := whoLists(nil)
	stamped := whoLists(accesstypes.Decisions{"RefusalMissions": accesstypes.Denied()})

	tests := []struct {
		name        string
		patch       func() bufferer
		ctx         func(ctx context.Context) context.Context
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
		},
		{
			name: "an update notes nothing",
			patch: func() bufferer {
				p := NewPatchSet(NewMetadata[refusalSquadron]()).SetPatchType(UpdatePatchType)
				p.SetKey("ID", "s1")
				p.Set("Name", "Hammer")

				return p
			},
		},
		{
			name: "a create notes nothing",
			patch: func() bufferer {
				p := NewPatchSet(NewMetadata[refusalSquadron]()).SetPatchType(CreatePatchType)
				p.SetKey("ID", "s1")
				p.Set("Name", "Hammer")

				return p
			},
		},
		{
			name: "the delete of a record with no description notes nothing",
			patch: func() bufferer {
				p := NewPatchSet(NewMetadata[forkRow]()).SetPatchType(DeletePatchType)
				p.SetKey("ID", "row-1")

				return p
			},
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
