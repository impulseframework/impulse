package integration

// This suite pins the list across tenants over the shipped demo world. Missions declares
// @listAcrossTenants, so its list is also served without the sector's segment, at
// /console/api/missions: one request reads the missions of every sector the session
// reaches in which the person may list them, each row checked under the person's roles in
// its own sector and naming it (zzTenant), with one filter, one sort, one page and one
// count across them.
//
// The liaison is the persona it is built around: she holds the Cadet's board in Anvil
// (the low-hazard missions, no fee), the Archivist's ledger in Cinder (the closed
// missions, the fee once one completed) and no role in Bastion. What her one list holds
// is checked two ways: against pinned facts of the seed, and against what the same person
// lists under each sector's own route, which the list across sectors must equal row for
// row. The matrix beside it asks the same route as the rest of the cast: a person of one
// sector, one who holds every sector, one who may list missions nowhere, and the client
// on the portal's outlet.
//
// Demonstrates: list.across-tenants.

import (
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// missionsAcross is the list of missions across sectors on the console's outlet.
const missionsAcross = consoleAPI + "/missions"

// acrossList asks the list across sectors as the user and returns its rows in the
// answer's order with the count header.
func acrossList(t *testing.T, h http.Handler, user accesstypes.User, target string) (rows []map[string]any, total string) {
	t.Helper()

	rr := doRequestRecordedAs(t, h, user, http.MethodGet, target, "")
	assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())

	return decodeRows(t, rr.Body.Bytes()), rr.Header().Get(resource.TotalCountHeader)
}

// inSectors is the oracle: what the user lists under each of the sectors' own routes
// with the query, every row carrying the sector it was listed in as zzTenant, keyed by
// the mission's id. A sector's route that refuses the person contributes nothing.
func inSectors(t *testing.T, h http.Handler, user accesstypes.User, list func(sector string) string, query string, sectors ...string) map[string]map[string]any {
	t.Helper()

	want := make(map[string]map[string]any)
	for _, sector := range sectors {
		status, body := doRequestAs(t, h, user, http.MethodGet, list(sector)+"?limit=200"+query, "")
		if status == http.StatusNotFound || status == http.StatusForbidden {
			continue
		}
		assertStatus(t, status, http.StatusOK, body)
		for _, row := range decodeRows(t, body) {
			row[resource.TenantProperty] = sector
			want[cell[string](t, row, "id")] = row
		}
	}

	return want
}

// consoleMissions and portalMissions are the sector's own list on each outlet.
func consoleMissions(sector string) string {
	return sectorPath(sector, "missions")
}

func portalMissions(sector string) string {
	return portalPath(sector, "missions")
}

// assertAcrossEqualsSectors fails unless the rows are exactly the oracle's, cell for
// cell: a row of the list across sectors shows what the person's roles in the row's own
// sector show, no more and no less.
func assertAcrossEqualsSectors(t *testing.T, rows []map[string]any, want map[string]map[string]any) {
	t.Helper()

	if len(rows) != len(want) {
		t.Errorf("rows = %d, want %d, the sectors' own lists together", len(rows), len(want))
	}
	for _, row := range rows {
		id := cell[string](t, row, "id")
		sectorRow, ok := want[id]
		if !ok {
			t.Errorf("mission %s (%v) is in the list across sectors and in no sector's own list", id, row["title"])

			continue
		}
		if !reflect.DeepEqual(row, sectorRow) {
			t.Errorf("mission %s differs from its sector's own list:\n across  %v\n sector  %v", id, row, sectorRow)
		}
	}
}

// TestAcrossTenants_liaison reads the missions of every sector as the liaison, who holds
// different roles in two sectors and none in the third.
func TestAcrossTenants_liaison(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	t.Run("one list holds each sector's rows as her roles there show them", func(t *testing.T) {
		t.Parallel()

		rows, total := acrossList(t, h, "liaison", missionsAcross+"?limit=200&count=true")
		if total != "15" {
			t.Errorf("Total-Count = %q, want 15: Anvil's fourteen low-hazard missions and Cinder's one closed", total)
		}
		assertAcrossEqualsSectors(t, rows, inSectors(t, h, "liaison", consoleMissions, "", anvil, bastion, cinder))

		perSector := make(map[string]int)
		for i, row := range rows {
			sector := cell[string](t, row, resource.TenantProperty)
			perSector[sector]++
			if i > 0 && cell[string](t, rows[i-1], "deadline") > cell[string](t, row, "deadline") {
				t.Errorf("row %d is out of the declared deadline order: %v after %v", i, row["deadline"], rows[i-1]["deadline"])
			}
			_, hasFee := row["fee"]
			_, hasClient := row["clientId"]
			switch sector {
			case anvil:
				// The Cadet's board: hazard 1 and 2, and neither the fee nor the client.
				if hazard := cell[float64](t, row, "hazard"); hazard != 1 && hazard != 2 {
					t.Errorf("Anvil mission %v has hazard %v, want 1 or 2", row["title"], hazard)
				}
				if hasFee || hasClient {
					t.Errorf("Anvil mission %v shows fee=%v client=%v, want neither: the Cadet's grant names neither", row["title"], hasFee, hasClient)
				}
			case cinder:
				// The Archivist's ledger: the closed sweep, its fee shown since it completed.
				if id := cell[string](t, row, "id"); id != missionSweepID {
					t.Errorf("Cinder mission %s, want the sweep %s alone", id, missionSweepID)
				}
				if fee, _ := row["fee"].(string); !hasFee || fee != "60000" {
					t.Errorf("the sweep's fee = %v, want 60000: the Archivist reads the fee of a completed mission", row["fee"])
				}
				if !hasClient {
					t.Errorf("the sweep shows no client, want it: the Archivist's grant names it")
				}
			default:
				t.Errorf("mission %v names sector %s, which she does not reach", row["title"], sector)
			}
		}
		if want := map[string]int{anvil: 14, cinder: 1}; !reflect.DeepEqual(perSector, want) {
			t.Errorf("rows per sector = %v, want %v", perSector, want)
		}
	})

	t.Run("a sector she holds no role in contributes nothing and is never named", func(t *testing.T) {
		t.Parallel()

		// Bastion's beacon repair is open at hazard 2: a Cadet of Bastion would list it.
		rows, _ := acrossList(t, h, "liaison", missionsAcross+"?limit=200")
		for _, row := range rows {
			if row["id"] == missionBeaconID || row[resource.TenantProperty] == bastion {
				t.Errorf("Bastion is in her list: %v", row)
			}
		}
		// Asked for by name, it answers as a sector that does not exist does: no row, no
		// refusal that would say which of the two it is.
		for _, sector := range []string{bastion, "d4444444-4444-4444-8444-444444444444"} {
			rows, total := acrossList(t, h, "liaison", missionsAcross+"?count=true&domains="+sector)
			if len(rows) != 0 || total != "0" {
				t.Errorf("domains=%s: rows = %d, Total-Count = %q, want none and 0", sector, len(rows), total)
			}
		}
	})

	t.Run("domains narrows the set, down to one sector", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			domains string
			sectors []string
		}{
			{name: "one sector", domains: cinder, sectors: []string{cinder}},
			{name: "the other", domains: anvil, sectors: []string{anvil}},
			{name: "both", domains: anvil + "," + cinder, sectors: []string{anvil, cinder}},
			{name: "one she reaches beside one she does not", domains: bastion + "," + cinder, sectors: []string{cinder}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				rows, total := acrossList(t, h, "liaison", missionsAcross+"?limit=200&count=true&domains="+tt.domains)
				want := inSectors(t, h, "liaison", consoleMissions, "", tt.sectors...)
				assertAcrossEqualsSectors(t, rows, want)
				if total != strconv.Itoa(len(want)) {
					t.Errorf("Total-Count = %q, want %d", total, len(want))
				}
			})
		}
	})

	t.Run("one page, one count and one order across the sectors", func(t *testing.T) {
		t.Parallel()

		whole, _ := acrossList(t, h, "liaison", missionsAcross+"?limit=200")
		wantOrder := make([]string, 0, len(whole))
		for _, row := range whole {
			wantOrder = append(wantOrder, cell[string](t, row, "id"))
		}

		var got []string
		target, pages := missionsAcross+"?limit=4&count=true", 0
		for target != "" {
			rr := doRequestRecordedAs(t, h, "liaison", http.MethodGet, target, "")
			assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())
			rows := decodeRows(t, rr.Body.Bytes())
			if pages == 0 {
				if total := rr.Header().Get(resource.TotalCountHeader); total != "15" {
					t.Errorf("first page Total-Count = %q, want 15", total)
				}
			}
			if len(rows) > 4 {
				t.Fatalf("page %d holds %d rows, want at most 4", pages, len(rows))
			}
			for _, row := range rows {
				got = append(got, cell[string](t, row, "id"))
			}
			rels := linkRelations(t, rr.Header().Get(resource.LinkHeader))
			if _, hasPrev := rels["prev"]; hasPrev != (pages > 0) {
				t.Errorf("page %d prev relation present = %v", pages, hasPrev)
			}
			target = rels["next"]
			pages++
			if pages > 10 {
				t.Fatal("the pages do not end")
			}
		}
		if pages != 4 {
			t.Errorf("pages = %d, want 4 for fifteen rows four at a time", pages)
		}
		if !slices.Equal(got, wantOrder) {
			t.Errorf("the pages together = %v\nwant the whole list's order %v", got, wantOrder)
		}

		// A sort the page asks for orders the two sectors' rows together: the sweep, at
		// hazard 5, leads Anvil's low-hazard rows.
		byHazard, _ := acrossList(t, h, "liaison", missionsAcross+"?limit=200&sort=hazard:desc")
		if len(byHazard) != 15 || byHazard[0]["id"] != missionSweepID {
			t.Errorf("sorted by hazard descending: %d rows led by %v, want 15 led by the sweep", len(byHazard), byHazard[0]["title"])
		}
	})

	t.Run("a filter and the search box apply in every sector", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			filter string
		}{
			{name: "a filter on an indexed column", filter: `statusId:eq:open`},
			{name: "a filter matching rows of one sector alone", filter: `statusId:eq:completed`},
			{name: "the search box", filter: `*:search:"salvage"`},
			{name: "the search box beside a filter", filter: `(statusId:in:(open,completed)),*:search:"s"`},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				query := "&filter=" + url.QueryEscape(tt.filter)
				rows, total := acrossList(t, h, "liaison", missionsAcross+"?limit=200&count=true"+query)
				want := inSectors(t, h, "liaison", consoleMissions, query, anvil, cinder)
				assertAcrossEqualsSectors(t, rows, want)
				if total != strconv.Itoa(len(want)) {
					t.Errorf("Total-Count = %q, want %d", total, len(want))
				}
			})
		}
	})

	t.Run("the list reads and nothing else", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			method     string
			target     string
			body       string
			wantStatus int
		}{
			{name: "a row's capabilities are asked on its page inside its sector", method: http.MethodGet, target: missionsAcross + "?capabilities=Execute", wantStatus: http.StatusBadRequest},
			{name: "a column her roles show in neither sector is refused", method: http.MethodGet, target: missionsAcross + "?columns=title,briefingTemplateId", wantStatus: http.StatusForbidden},
			{name: "no patch is served without the sector", method: http.MethodPatch, target: missionsAcross, body: `{"title":"x"}`, wantStatus: http.StatusMethodNotAllowed},
			{name: "no delete is served without the sector", method: http.MethodDelete, target: missionsAcross, wantStatus: http.StatusMethodNotAllowed},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				status, body := doRequestAs(t, h, "liaison", tt.method, tt.target, tt.body)
				assertStatus(t, status, tt.wantStatus, body)
			})
		}
	})
}

// TestAcrossTenants_matrix asks the list of missions across sectors as the rest of the
// cast: each persona reads exactly what her own sectors' lists hold, and a person who may
// list missions in no sector reads an empty list, never a refusal.
func TestAcrossTenants_matrix(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	tests := []struct {
		name     string
		user     accesstypes.User
		across   string
		list     func(sector string) string
		wantRows int
		// wantSectors are the sectors the rows name, in no order.
		wantSectors []string
	}{
		{name: "the liaison: the Cadet's board in Anvil and the Archivist's ledger in Cinder", user: "liaison", across: missionsAcross, list: consoleMissions, wantRows: 15, wantSectors: []string{anvil, cinder}},
		{name: "the cadet holds one sector: her list is that sector's own", user: "cadet", across: missionsAcross, list: consoleMissions, wantRows: 14, wantSectors: []string{anvil}},
		{name: "the marshal holds Anvil alone", user: "marshal", across: missionsAcross, list: consoleMissions, wantRows: 30, wantSectors: []string{anvil}},
		{name: "the governor holds every sector", user: "governor", across: missionsAcross, list: consoleMissions, wantRows: 33, wantSectors: []string{anvil, bastion, cinder}},
		{name: "the archivist keeps the record of all three: the closed missions of each", user: "archivist", across: missionsAcross, list: consoleMissions, wantRows: 8, wantSectors: []string{anvil, cinder}},
		{name: "the yeoman may list missions in no sector: an empty list, not a refusal", user: "yeoman", across: missionsAcross, list: consoleMissions, wantRows: 0},
		{name: "the armorer reaches Anvil and may not list its missions: empty", user: "armorer", across: missionsAcross, list: consoleMissions, wantRows: 0},
		{name: "the client on the portal's outlet: her company's missions, which are all in Anvil", user: "client", across: portalAPI + "/missions", list: portalMissions, wantRows: 10, wantSectors: []string{anvil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows, total := acrossList(t, h, tt.user, tt.across+"?limit=200&count=true")
			if len(rows) != tt.wantRows || total != strconv.Itoa(tt.wantRows) {
				t.Errorf("rows = %d, Total-Count = %q, want %d", len(rows), total, tt.wantRows)
			}
			assertAcrossEqualsSectors(t, rows, inSectors(t, h, tt.user, tt.list, "", anvil, bastion, cinder))

			var sectors []string
			for _, row := range rows {
				if sector := cell[string](t, row, resource.TenantProperty); !slices.Contains(sectors, sector) {
					sectors = append(sectors, sector)
				}
			}
			slices.Sort(sectors)
			if !slices.Equal(sectors, tt.wantSectors) {
				t.Errorf("sectors named = %v, want %v", sectors, tt.wantSectors)
			}
		})
	}
}
