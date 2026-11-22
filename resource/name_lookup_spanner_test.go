package resource

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// lookupWings, lookupCrews, lookupSquadrons and lookupPassRows are the name
// lookups' fixture rows. A wing, a squadron and a pass is in a sector; a crew is
// in the sector of its wing. An empty id is no id.
var (
	lookupWings     = [][2]string{{"w1", "sector-1"}, {"w2", "sector-2"}}
	lookupCrews     = [][3]string{{"c1", "w1", "Bravo"}, {"c2", "w1", "Alpha"}, {"c3", "w1", "Bravo"}, {"c4", "w2", "Zulu"}}
	lookupSquadrons = []struct {
		id, sector, name string
		rank             int64
	}{{"s1", "sector-1", "Red", 1}, {"s3", "sector-1", "Green", 2}, {"s2", "sector-2", "Blue", 1}}
	lookupPassRows = [][4]string{
		{"p1", "sector-1", "c1", "s1"},
		{"p2", "sector-1", "c2", "s2"},
		{"p3", "sector-1", "c3", "s3"},
		{"p4", "sector-1", "c4", ""},
		{"p5", "sector-1", "", ""},
		{"p6", "sector-2", "c4", "s2"},
	}
)

// lookupSpannerClient makes a database with the name lookups' fixture on the
// shared emulator and writes the fixture's rows.
func lookupSpannerClient(t *testing.T, name string) *SpannerClient {
	t.Helper()

	container := spannerEmulator(t)
	db, err := container.CreateDatabase(context.Background(), name)
	if err != nil {
		t.Fatalf("initiator.SpannerContainer.CreateDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropDatabase(context.Background()); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.MigrateUp("file://testdata/names/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}

	mutations := []*spanner.Mutation{
		spanner.InsertMap("PassLevels", map[string]any{"Id": "visit", "Description": "Visit"}),
	}
	for _, row := range lookupWings {
		mutations = append(mutations, spanner.InsertMap("Wings", map[string]any{"Id": row[0], "SectorId": row[1], "Name": row[0]}))
	}
	for _, row := range lookupCrews {
		mutations = append(mutations, spanner.InsertMap("Crews", map[string]any{"Id": row[0], "WingId": row[1], "Name": row[2]}))
	}
	for _, row := range lookupSquadrons {
		mutations = append(mutations, spanner.InsertMap("Squadrons", map[string]any{"Id": row.id, "SectorId": row.sector, "Name": row.name, "Rank": row.rank}))
	}
	for _, row := range lookupPassRows {
		pass := map[string]any{"Id": row[0], "SectorId": row[1], "LevelId": "visit"}
		if row[2] != "" {
			pass["CrewId"] = row[2]
		}
		if row[3] != "" {
			pass["SquadronId"] = row[3]
		}
		mutations = append(mutations, spanner.InsertMap("HangarPasses", pass))
	}
	if _, err := db.Apply(context.Background(), mutations); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	return NewSpannerClient(db.Client)
}

// lookupCollection binds the hangar passes to their sector by its column.
func lookupCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
		Name:        "HangarPasses",
		Scope:       accesstypes.DomainPermissionScope,
		Permissions: []accesstypes.Permission{accesstypes.List},
		Domain:      &DomainBindingData{Column: "SectorId"},
	}}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// lookupDecoder is lookupPass's list decoder over the emulator's fixture, under
// the declared paging.
func lookupDecoder(t *testing.T, paging Paging) *QueryDecoder[lookupPass, lookupPassRequest] {
	t.Helper()

	resSet, err := NewSet[lookupPass, lookupPassRequest](accesstypes.List)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewQueryDecoder[lookupPass, lookupPassRequest](resSet)
	if err != nil {
		t.Fatalf("NewQueryDecoder() error = %v", err)
	}
	decoder.collection = lookupCollection(t)
	key, err := NewCursorKey("dGVzdC1jdXJzb3Ita2V5LW1hdGVyaWFsLTMyLWJ5dGVzIQ==")
	if err != nil {
		t.Fatalf("NewCursorKey() error = %v", err)
	}

	return decoder.WithCursorKey(key).WithPaging(paging)
}

// lookupPage serves one page of the passes of sector-1 as the generated handler
// does: decode, list into the page, the headers. It returns the page's rows in
// the list's order, each its id followed by the names the query returns ("none"
// where the row carries none), and the Link relations the page named.
func lookupPage(t *testing.T, client *SpannerClient, decoder *QueryDecoder[lookupPass, lookupPassRequest], target string, perms UserPermissions) (rows []string, links map[string]string) {
	t.Helper()

	ctx := t.Context()
	req := acrossRequest(t, target)
	qSet, err := decoder.Decode(req, perms, accesstypes.DomainScope("sector-1"))
	if err != nil {
		t.Fatalf("QueryDecoder.Decode(%s) error = %v", target, err)
	}
	page := qSet.Page()
	for row, err := range qSet.List(ctx, client) {
		if err != nil {
			t.Fatalf("QuerySet.List(%s) error = %v", target, err)
		}
		if !page.Add(row) {
			break
		}
		cells := []string{row.Data.ID}
		for _, name := range qSet.Names() {
			if value := row.Name(name.Field); value != nil {
				cells = append(cells, *value)
			} else {
				cells = append(cells, "none")
			}
		}
		rows = append(rows, strings.Join(cells, " "))
	}
	if page.Reversed() {
		slices.Reverse(rows)
	}
	rr := httptest.NewRecorder()
	if err := page.WriteHeaders(rr, req); err != nil {
		t.Fatalf("Page.WriteHeaders(%s) error = %v", target, err)
	}
	links = make(map[string]string)
	for _, match := range linkPattern.FindAllStringSubmatch(rr.Header().Get(LinkHeader), -1) {
		links[match[2]] = match[1]
	}

	return rows, links
}

// lookupWalk serves every page of a list from target forward by its next
// relation, and returns the pages and each page's prev relation.
func lookupWalk(t *testing.T, client *SpannerClient, decoder *QueryDecoder[lookupPass, lookupPassRequest], target string, perms UserPermissions) (pages [][]string, prevs []string) {
	t.Helper()

	for {
		rows, links := lookupPage(t, client, decoder, target, perms)
		pages = append(pages, rows)
		prevs = append(prevs, links["prev"])
		next, more := links["next"]
		if !more {
			return pages, prevs
		}
		target = next
	}
}

// TestNameLookup_onSpanner runs the name lookups of a tenant's list on the
// Spanner emulator through the calls the generated handler makes: a list sorted
// by the name of a row that reaches its tenant through another table, walked
// forward and back, and the names of a tenant's row and of an enumeration
// table's row. An id that points to another tenant's row gives no name, and
// the other tenant's pass is in no answer.
func TestNameLookup_onSpanner(t *testing.T) {
	t.Parallel()

	client := lookupSpannerClient(t, "name-lookups")
	grantAll := renderStubPermissions{}

	t.Run("sorted by a crew's name, walked forward and back", func(t *testing.T) {
		t.Parallel()

		decoder := lookupDecoder(t, Paging{})
		want := [][]string{{"p4 none", "p5 none"}, {"p2 Alpha", "p1 Bravo"}, {"p3 Bravo"}}
		pages, prevs := lookupWalk(t, client, decoder, "/?columns=id,crewName&sort=crewName:asc&limit=2", grantAll)
		if diff := cmp.Diff(want, pages); diff != "" {
			t.Errorf("the walk forward mismatch (-want +got):\n%s", diff)
		}
		if prevs[0] != "" {
			t.Errorf("the first page names a previous page: %s", prevs[0])
		}
		for i := len(pages) - 1; i > 0; i-- {
			if prevs[i] == "" {
				t.Fatalf("page %d names no previous page", i+1)
			}
			rows, links := lookupPage(t, client, decoder, prevs[i], grantAll)
			if diff := cmp.Diff(want[i-1], rows); diff != "" {
				t.Errorf("walking back from page %d mismatch (-want +got):\n%s", i+1, diff)
			}
			prevs[i-1] = links["prev"]
		}
	})

	t.Run("sorted by a crew's name the other way", func(t *testing.T) {
		t.Parallel()

		want := [][]string{{"p1 Bravo", "p3 Bravo"}, {"p2 Alpha", "p4 none"}, {"p5 none"}}
		pages, _ := lookupWalk(t, client, lookupDecoder(t, Paging{}), "/?columns=id,crewName&sort=crewName:desc&limit=2", grantAll)
		if diff := cmp.Diff(want, pages); diff != "" {
			t.Errorf("the walk forward mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a squadron's name and a level's name", func(t *testing.T) {
		t.Parallel()

		want := []string{"p1 Red Visit", "p2 none Visit", "p3 Green Visit", "p4 none Visit", "p5 none Visit"}
		rows, links := lookupPage(t, client, lookupDecoder(t, Paging{}), "/?columns=id,squadronName,levelName&sort=id&limit=all", grantAll)
		if diff := cmp.Diff(want, rows); diff != "" {
			t.Errorf("the rows mismatch (-want +got):\n%s", diff)
		}
		if len(links) != 0 {
			t.Errorf("the whole list names neighboring pages: %v", links)
		}
	})
}
