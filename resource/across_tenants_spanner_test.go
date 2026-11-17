package resource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// acrossSpannerClient makes a database with the list across tenants' fixture on
// the shared emulator and writes the fixture's rows.
func acrossSpannerClient(t *testing.T, name string) *SpannerClient {
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
	if err := db.MigrateUp("file://testdata/acrosstenants/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}

	var mutations []*spanner.Mutation
	for _, row := range acrossCrateRows {
		mutations = append(mutations, spanner.InsertMap("Crates", map[string]any{
			"Id": row.ID, "Yard": row.Yard, "Label": row.Label, "Owner": row.Owner, "Team": row.Team, "Weight": row.Weight, "Value": row.Value,
		}))
	}
	for _, row := range acrossSealRows {
		mutations = append(mutations, spanner.InsertMap("Seals", map[string]any{"Id": row.ID, "CrateId": row.CrateID, "Mark": row.Mark}))
	}
	for _, row := range acrossCrewRows {
		mutations = append(mutations, spanner.InsertMap("YardCrews", map[string]any{"Id": row[0], "Yard": row[1], "UserId": row[2], "Team": row[3]}))
	}
	for _, row := range acrossTagRows {
		tag := map[string]any{"Id": row[0], "CrateId": row[1], "Yard": row[2], "Name": row[3]}
		if row[4] != "" {
			tag["Team"] = row[4]
		}
		mutations = append(mutations, spanner.InsertMap("CrateTags", tag))
	}
	if _, err := db.Apply(context.Background(), mutations); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}

	return NewSpannerClient(db.Client)
}

var linkPattern = regexp.MustCompile(`<([^>]*)>; rel="(next|prev)"`)

// pageOnSpanner serves one page of a list across tenants as the generated
// handler does: decode, count, list into the page, the headers. It returns the
// page's rows in the list's order, the total when the page answered one, and
// the Link relations it named.
func pageOnSpanner[Resource Resourcer, Request any](
	t *testing.T, client *SpannerClient, decoder *QueryDecoder[Resource, Request], target string, perms UserPermissions, answer func(*QuerySet[Resource], *Row[Resource]) acrossAnswer,
) (rows []acrossAnswer, total string, links map[string]string) {
	t.Helper()

	ctx := t.Context()
	req := acrossRequest(t, target)
	qSet, err := decoder.DecodeAcrossTenants(req, perms)
	if err != nil {
		t.Fatalf("QueryDecoder.DecodeAcrossTenants(%s) error = %v", target, err)
	}
	page := qSet.Page()
	if err := page.Count(ctx, client); err != nil {
		t.Fatalf("Page.Count(%s) error = %v", target, err)
	}
	for row, err := range qSet.List(ctx, client) {
		if err != nil {
			t.Fatalf("QuerySet.List(%s) error = %v", target, err)
		}
		if !page.Add(row) {
			break
		}
		rows = append(rows, answer(qSet, row))
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

	return rows, rr.Header().Get(TotalCountHeader), links
}

// crateAnswer reads a crate's row as a page shows it.
func crateAnswer(qSet *QuerySet[acrossCrate], row *Row[acrossCrate]) acrossAnswer {
	answer := acrossAnswer{ID: row.Data.ID, Tenant: row.Tenant()}
	if !slices.Contains(qSet.Fields(), accesstypes.Field("Value")) {
		return answer
	}
	switch {
	case row.Masked("value"):
		answer.Value = "masked"
	case row.Data.Value == nil:
		answer.Value = "null"
	default:
		answer.Value = strconv.FormatInt(*row.Data.Value, 10)
	}

	return answer
}

// TestAcrossTenants_onSpanner runs the list across tenants on the Spanner
// emulator through the calls the generated handler makes: every page of each
// walk forward by its next relation, then back by its prev relation, with the
// count the first page answers. Each row is held to the tenant it says it is
// in and to what the person's grants in that tenant show of it.
func TestAcrossTenants_onSpanner(t *testing.T) {
	t.Parallel()

	client := acrossSpannerClient(t, "across-tenants")

	for _, walk := range acrossWalks() {
		t.Run(walk.name, func(t *testing.T) {
			t.Parallel()

			decoder := crateDecoder(t)
			var forward [][]acrossAnswer
			var prevs []string
			target := walk.target
			for {
				rows, total, links := pageOnSpanner(t, client, decoder, target, walk.perms(), crateAnswer)
				if len(forward) == 0 {
					if total != strconv.FormatInt(walk.total, 10) {
						t.Errorf("Total-Count = %q, want %d", total, walk.total)
					}
				}
				forward = append(forward, rows)
				prevs = append(prevs, links["prev"])
				next, more := links["next"]
				if !more {
					break
				}
				target = next
			}
			if diff := cmp.Diff(walk.pages, forward); diff != "" {
				t.Errorf("the walk forward mismatch (-want +got):\n%s", diff)
			}
			if prevs[0] != "" {
				t.Errorf("the first page names a previous page: %s", prevs[0])
			}

			// Back from the last page: each prev relation answers the page before.
			for i := len(forward) - 1; i > 0; i-- {
				if prevs[i] == "" {
					t.Fatalf("page %d names no previous page", i+1)
				}
				rows, _, links := pageOnSpanner(t, client, decoder, prevs[i], walk.perms(), crateAnswer)
				if diff := cmp.Diff(walk.pages[i-1], rows); diff != "" {
					t.Errorf("walking back from page %d mismatch (-want +got):\n%s", i+1, diff)
				}
				prevs[i-1] = links["prev"]
			}
		})
	}

	t.Run("a record bound through a join path names the tenant the path resolves to", func(t *testing.T) {
		t.Parallel()

		decoder := acrossDecoder[acrossSeal, acrossSealRequest](t, Paging{Order: []SortField{{Field: "Mark", Direction: SortAscending}}, DefaultLimit: 3})
		answer := func(_ *QuerySet[acrossSeal], row *Row[acrossSeal]) acrossAnswer {
			a := acrossAnswer{ID: row.Data.ID, Tenant: row.Tenant(), Value: row.Data.Mark}
			if row.Masked("mark") {
				a.Value = "masked"
			}

			return a
		}
		// k4's mark is tape, which east does not show: NULL to the sort, first
		// ascending on Spanner.
		want := [][]acrossAnswer{
			{{"k4", "east", "masked"}, {"k2", "south", "tape"}, {"k1", "north", "wax"}},
			{{"k3", "east", "wax"}},
		}
		var got [][]acrossAnswer
		target := "/seals?count=true"
		for {
			rows, total, links := pageOnSpanner(t, client, decoder, target, sealRoles(), answer)
			if len(got) == 0 && total != "4" {
				t.Errorf("Total-Count = %q, want 4", total)
			}
			got = append(got, rows)
			next, more := links["next"]
			if !more {
				break
			}
			target = next
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("the seals mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a tenant's own list still reads its one tenant", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/crates", http.NoBody)
		qSet, err := crateDecoder(t).Decode(req, differingRoles(), accesstypes.DomainScope("south"))
		if err != nil {
			t.Fatalf("QueryDecoder.Decode() error = %v", err)
		}
		var ids []string
		for row, err := range qSet.List(t.Context(), client) {
			if err != nil {
				t.Fatalf("QuerySet.List() error = %v", err)
			}
			if row.Tenant() != "" {
				t.Errorf("Row.Tenant() = %q on a tenant's own list, want none", row.Tenant())
			}
			ids = append(ids, row.Data.ID)
		}
		if diff := cmp.Diff([]string{"s1", "s3"}, ids); diff != "" {
			t.Errorf("south's own list mismatch (-want +got):\n%s", diff)
		}
	})
}
