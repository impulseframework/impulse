package resource

// These tests pin a list filtered by a name beside an id: the filter names the
// name as it names a field, and the statement tests the id against the keys of
// the pointed-to rows whose name answers the condition, held to the request's
// tenant and to the condition the name's grant is held under, so the condition
// is true of a row exactly where it is true of the name the row shows. The
// filter takes the name's two grants, as showing the name does.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// loosePassRequest is a list request type for lookupPass whose names a filter
// reaches in other ways than lookupPassRequest's: the squadron's id and name by
// allow_filter alone, the crew's name sensitive, and the level's name beside an
// id the request type does not carry.
type loosePassRequest struct {
	ID           string  `json:"id"           index:"true"        perm:"-"`
	SectorID     string  `json:"sectorId"     index:"true"`
	SquadronID   *string `json:"squadronId"   allow_filter:"true"`
	SquadronName *string `json:"squadronName" allow_filter:"true" nameof:"SquadronID"`
	LevelName    *string `json:"levelName"    index:"true"        nameof:"LevelID"`
	CrewID       *string `json:"crewId"       index:"true"`
	CrewName     *string `json:"crewName"     index:"true"        nameof:"CrewID"     pii:"true"`
}

// decodeNameFilter decodes a list request for lookupPass through the request
// type, with no grants asked, and runs its read checks in sector-1.
func decodeNameFilter[Request any](t *testing.T, req *http.Request) (*QuerySet[lookupPass], error) {
	t.Helper()

	resSet, err := NewSet[lookupPass, Request](accesstypes.List)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewQueryDecoder[lookupPass, Request](resSet)
	if err != nil {
		t.Fatalf("NewQueryDecoder() error = %v", err)
	}
	qSet, err := decoder.DecodeWithoutPermissions(req)
	if err != nil {
		return nil, err
	}
	qSet.scope = inSector1
	if err := qSet.checkPermissions(t.Context(), SpannerDBType); err != nil {
		t.Fatalf("checkPermissions() error = %v", err)
	}

	return qSet, nil
}

// filterRequest is a GET of the target, or, with a body, a POST of the body.
func filterRequest(t *testing.T, target, body string) *http.Request {
	t.Helper()

	if body == "" {
		return httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	return req
}

// TestNameFilter_decoder pins what a filter naming a name decodes to: a name
// whose request field carries a filter tag is a field to filter by, counted as
// its id among the fields the filter touches and reported among the names it
// names; a name with no filter tag, or beside an id the request type does not
// carry, is not; a sensitive name travels in the body; and no name joins the
// search box or answers a match its tag does not name.
func TestNameFilter_decoder(t *testing.T) {
	t.Parallel()

	type decoded struct {
		fields []accesstypes.Field
		names  []accesstypes.Field
	}
	tests := []struct {
		name    string
		decode  func(*testing.T, *http.Request) (*QuerySet[lookupPass], error)
		target  string
		body    string
		want    decoded
		wantErr string
	}{
		{
			name:   "a tagged name: its id among the filter's fields, the name among its names",
			decode: decodeNameFilter[lookupPassRequest],
			target: "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2",
			want:   decoded{fields: []accesstypes.Field{"SquadronID"}, names: []accesstypes.Field{"SquadronName"}},
		},
		{
			name:   "names and ids, each once, in first appearance",
			decode: decodeNameFilter[lookupPassRequest],
			target: "/?columns=id&filter=crewName:eq:Bravo,squadronName:eq:Red,crewId:eq:c1,crewName:ne:Alpha&sort=id&limit=2",
			want:   decoded{fields: []accesstypes.Field{"CrewID", "SquadronID"}, names: []accesstypes.Field{"CrewName", "SquadronName"}},
		},
		{
			name:    "the search box lists no name",
			decode:  decodeNameFilter[lookupPassRequest],
			target:  "/?columns=id&filter=*:search:red&sort=id&limit=2",
			wantErr: "no field of this resource is searchable",
		},
		{
			name:    "a match on a name whose tag names none",
			decode:  decodeNameFilter[lookupPassRequest],
			target:  "/?columns=id&filter=levelName:contains:Vis&sort=id&limit=2",
			wantErr: "unknown operator 'contains' for 'levelName' in condition 'levelName:contains:Vis': the field answers no text match: eq, ne, in, notin, isnull and isnotnull",
		},
		{
			name:    "a name beside an id the request type does not carry",
			decode:  decodeNameFilter[loosePassRequest],
			target:  "/?columns=id&filter=levelName:eq:Visit&sort=id&limit=2",
			wantErr: "'levelName' is not filterable",
		},
		{
			name:    "a sensitive name in the URL",
			decode:  decodeNameFilter[loosePassRequest],
			target:  "/?columns=id&filter=crewName:eq:Bravo&sort=id&limit=2",
			wantErr: "cannot filter on sensitive field in URL: crewName",
		},
		{
			name:    "a sensitive name in the URL, its field name padded",
			decode:  decodeNameFilter[loosePassRequest],
			target:  "/?columns=id&filter=+crewName:eq:Bravo&sort=id&limit=2",
			wantErr: "cannot filter on sensitive field in URL: crewName",
		},
		{
			name:   "a sensitive name in the body",
			decode: decodeNameFilter[loosePassRequest],
			target: "/?columns=id&sort=id&limit=2",
			body:   `{"filter":"crewName:eq:Bravo"}`,
			want:   decoded{fields: []accesstypes.Field{"CrewID"}, names: []accesstypes.Field{"CrewName"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			qSet, err := tt.decode(t, filterRequest(t, tt.target, tt.body))
			if tt.wantErr != "" {
				if err == nil || !httpio.HasBadRequest(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("DecodeWithoutPermissions(%s) error = %v, want a bad request saying %q", tt.target, err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("DecodeWithoutPermissions(%s) error = %v", tt.target, err)
			}
			if diff := cmp.Diff(tt.want.fields, qSet.filterFields); diff != "" {
				t.Errorf("filterFields mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.want.names, qSet.filterNames); diff != "" {
				t.Errorf("filterNames mismatch (-want +got):\n%s", diff)
			}
			if names := qSet.Names(); len(names) != 0 {
				t.Errorf("Names() = %v, want none: a filter shows no name", names)
			}
		})
	}
}

// TestNameFilter_indexRule pins the index rule over a name: a name tagged
// allow_filter alone is no indexed field, so a filter naming it alone is
// refused, and passes beside an indexed field.
func TestNameFilter_indexRule(t *testing.T) {
	t.Parallel()

	for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
		alone, err := decodeNameFilter[loosePassRequest](t, filterRequest(t, "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2", ""))
		if err != nil {
			t.Fatalf("DecodeWithoutPermissions() error = %v", err)
		}
		if _, err := alone.stmt(dbType); err == nil || !strings.Contains(err.Error(), "Filter must contain at least one column that is indexed") {
			t.Errorf("stmt(%s) error = %v, want the index rule's refusal", dbType, err)
		}

		beside, err := decodeNameFilter[loosePassRequest](t, filterRequest(t, "/?columns=id&filter=sectorId:eq:sector-1,squadronName:eq:Red&sort=id&limit=2", ""))
		if err != nil {
			t.Fatalf("DecodeWithoutPermissions() error = %v", err)
		}
		stmt, err := beside.stmt(dbType)
		if err != nil {
			t.Fatalf("stmt(%s) error = %v", dbType, err)
		}
		if sql := oneLine(stmt.SQL); !strings.Contains(sql, "IN (SELECT") {
			t.Errorf("stmt(%s) SQL =\n%s\nwant the name's subquery", dbType, sql)
		}
	}
}

// TestNameFilter_statement pins what the statement writes for a condition on a
// name: the id among the keys of the pointed-to rows, held to the request's
// tenant, whose name answers the condition as the name column's own condition
// is written; isnull, the rows whose name is not shown.
func TestNameFilter_statement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		filter     string
		want       map[DBType]string
		wantParams map[DBType]map[string]any
	}{
		{
			name:   "eq on a tenant's row's name",
			filter: "squadronName:eq:Red",
			want: bothDatabases(
				"WHERE `SquadronId` IN (SELECT `ca1`.`Id` FROM `Squadrons` `ca1` WHERE `ca1`.`SectorId` = @domain AND `Name` = @_p1)",
				`WHERE "SquadronId" IN (SELECT "ca1"."Id" FROM "Squadrons" "ca1" WHERE "ca1"."SectorId" = @domain AND "Name" = @_p1)`,
			),
			wantParams: map[DBType]map[string]any{SpannerDBType: {"_p1": "Red"}, PostgresDBType: {"_p1": "Red"}},
		},
		{
			name:   "in",
			filter: "squadronName:in:(Red,Green)",
			want: bothDatabases(
				"WHERE `SquadronId` IN (SELECT `ca1`.`Id` FROM `Squadrons` `ca1` WHERE `ca1`.`SectorId` = @domain AND `Name` IN (@_p1, @_p2))",
				`WHERE "SquadronId" IN (SELECT "ca1"."Id" FROM "Squadrons" "ca1" WHERE "ca1"."SectorId" = @domain AND "Name" IN (@_p1, @_p2))`,
			),
			wantParams: map[DBType]map[string]any{SpannerDBType: {"_p1": "Red", "_p2": "Green"}, PostgresDBType: {"_p1": "Red", "_p2": "Green"}},
		},
		{
			name:   "contains, through the pointed-to table's token column",
			filter: "squadronName:contains:ed",
			want: bothDatabases(
				"WHERE `SquadronId` IN (SELECT `ca1`.`Id` FROM `Squadrons` `ca1` WHERE `ca1`.`SectorId` = @domain AND SEARCH_SUBSTRING(`Name_Search`, @_p1))",
				`WHERE "SquadronId" IN (SELECT "ca1"."Id" FROM "Squadrons" "ca1" WHERE "ca1"."SectorId" = @domain AND "Name" ILIKE @_p1)`,
			),
			wantParams: map[DBType]map[string]any{SpannerDBType: {"_p1": "ed"}, PostgresDBType: {"_p1": "%ed%"}},
		},
		{
			name:   "isnotnull",
			filter: "squadronName:isnotnull",
			want: bothDatabases(
				"WHERE `SquadronId` IN (SELECT `ca1`.`Id` FROM `Squadrons` `ca1` WHERE `ca1`.`SectorId` = @domain AND `Name` IS NOT NULL)",
				`WHERE "SquadronId" IN (SELECT "ca1"."Id" FROM "Squadrons" "ca1" WHERE "ca1"."SectorId" = @domain AND "Name" IS NOT NULL)`,
			),
		},
		{
			name:   "isnull: no id, or no name shown for it",
			filter: "squadronName:isnull",
			want: bothDatabases(
				"WHERE (`SquadronId` IS NULL OR `SquadronId` NOT IN (SELECT `ca1`.`Id` FROM `Squadrons` `ca1` WHERE `ca1`.`SectorId` = @domain AND `Name` IS NOT NULL))",
				`WHERE ("SquadronId" IS NULL OR "SquadronId" NOT IN (SELECT "ca1"."Id" FROM "Squadrons" "ca1" WHERE "ca1"."SectorId" = @domain AND "Name" IS NOT NULL))`,
			),
		},
		{
			name:   "an enumeration table's name: no tenant test",
			filter: "levelName:eq:Visit",
			want: bothDatabases(
				"WHERE `LevelId` IN (SELECT `ca1`.`Id` FROM `PassLevels` `ca1` WHERE `Description` = @_p1)",
				`WHERE "LevelId" IN (SELECT "ca1"."Id" FROM "PassLevels" "ca1" WHERE "Description" = @_p1)`,
			),
		},
		{
			name:   "a row that reaches its tenant through another table",
			filter: "crewName:eq:Bravo",
			want: bothDatabases(
				"WHERE `CrewId` IN (SELECT `ca1`.`Id` FROM `Crews` `ca1` WHERE (SELECT `ca2`.`SectorId` FROM `Wings` `ca2` WHERE `ca2`.`Id` = `ca1`.`WingId`) = @domain AND `Name` = @_p1)",
				`WHERE "CrewId" IN (SELECT "ca1"."Id" FROM "Crews" "ca1" WHERE (SELECT "ca2"."SectorId" FROM "Wings" "ca2" WHERE "ca2"."Id" = "ca1"."WingId") = @domain AND "Name" = @_p1)`,
			),
		},
		{
			name:   "in an OR with another condition",
			filter: "squadronName:eq:Red%7CsectorId:eq:sector-1",
			want: bothDatabases(
				"WHERE (`SquadronId` IN (SELECT `ca1`.`Id` FROM `Squadrons` `ca1` WHERE `ca1`.`SectorId` = @domain AND `Name` = @_p1) OR `SectorId` = @_p2)",
				`WHERE ("SquadronId" IN (SELECT "ca1"."Id" FROM "Squadrons" "ca1" WHERE "ca1"."SectorId" = @domain AND "Name" = @_p1) OR "SectorId" = @_p2)`,
			),
		},
		{
			name:   "the same name twice, in a group",
			filter: "levelName:eq:Visit,(squadronName:eq:Red%7CsquadronName:eq:Green)",
			want: bothDatabases(
				"WHERE `LevelId` IN (SELECT `ca1`.`Id` FROM `PassLevels` `ca1` WHERE `Description` = @_p1) AND (`SquadronId` IN (SELECT `ca2`.`Id` FROM `Squadrons` `ca2` WHERE `ca2`.`SectorId` = @domain AND `Name` = @_p2) OR `SquadronId` IN (SELECT `ca3`.`Id` FROM `Squadrons` `ca3` WHERE `ca3`.`SectorId` = @domain AND `Name` = @_p3))",
				`WHERE "LevelId" IN (SELECT "ca1"."Id" FROM "PassLevels" "ca1" WHERE "Description" = @_p1) AND ("SquadronId" IN (SELECT "ca2"."Id" FROM "Squadrons" "ca2" WHERE "ca2"."SectorId" = @domain AND "Name" = @_p2) OR "SquadronId" IN (SELECT "ca3"."Id" FROM "Squadrons" "ca3" WHERE "ca3"."SectorId" = @domain AND "Name" = @_p3))`,
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for dbType, want := range tt.want {
				qSet, sql := passesStatement(t, Paging{}, "/?columns=id&filter="+tt.filter+"&sort=id&limit=2", &nameGrantPermissions{}, dbType)
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
				}
				stmt, err := qSet.stmt(dbType)
				if err != nil {
					t.Fatalf("stmt(%s) error = %v", dbType, err)
				}
				for param, value := range tt.wantParams[dbType] {
					if got := stmt.Params[param]; got != value {
						t.Errorf("%s params[%s] = %v, want %v", dbType, param, got, value)
					}
				}
			}
		})
	}

	t.Run("a match on a name names the search optimizer version on Spanner", func(t *testing.T) {
		t.Parallel()

		qSet, _ := passesStatement(t, Paging{}, "/?columns=id&filter=squadronName:contains:ed&sort=id&limit=2", &nameGrantPermissions{}, SpannerDBType)
		stmt, err := qSet.stmt(SpannerDBType)
		if err != nil {
			t.Fatalf("stmt() error = %v", err)
		}
		if stmt.optimizerVersion != searchOptimizerVersion {
			t.Errorf("optimizerVersion = %q, want %q", stmt.optimizerVersion, searchOptimizerVersion)
		}
	})

	t.Run("decoded with no grants asked: the tenant test alone", func(t *testing.T) {
		t.Parallel()

		qSet := lookupQuerySet(t, Paging{}, "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2")
		stmt, err := qSet.stmt(SpannerDBType)
		if err != nil {
			t.Fatalf("stmt() error = %v", err)
		}
		want := "WHERE `SquadronId` IN (SELECT `ca1`.`Id` FROM `Squadrons` `ca1` WHERE `ca1`.`SectorId` = @domain AND `Name` = @_p1)"
		if sql := oneLine(stmt.SQL); !strings.Contains(sql, want) {
			t.Errorf("statement =\n%s\nwant it to contain:\n%s", sql, want)
		}
	})
}

// TestNameFilter_grants pins the two grants a filter by a name takes: List on
// the pointed-to resource's name field, asked once where that resource's grants
// are held, and the grant on the id, asked as a filter on the id; each refuses
// in the words a sort by the name gets, and a condition either is held under
// holds the filter to it.
func TestNameFilter_grants(t *testing.T) {
	t.Parallel()

	t.Run("a filter alone brings the name into play: its grant asked once, in the tenant", func(t *testing.T) {
		t.Parallel()

		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			perms := &nameGrantPermissions{}
			passesStatement(t, Paging{}, "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2", perms, dbType)
			calls := perms.askedFor(squadronNameGrant)
			if len(calls) != 1 || calls[0].scope != inSector1 || calls[0].perm != accesstypes.List {
				t.Errorf("%s calls naming %s = %+v, want one, with List in sector-1", dbType, squadronNameGrant, calls)
			}
		}
	})

	t.Run("an enumeration table's name, everything but the passes denied: no grant asked of the table", func(t *testing.T) {
		t.Parallel()

		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			perms := &nameGrantPermissions{ownOnly: true}
			_, sql := passesStatement(t, Paging{}, "/?columns=id&filter=levelName:eq:Visit&sort=id&limit=2", perms, dbType)
			if !strings.Contains(sql, "PassLevels") {
				t.Errorf("%s statement =\n%s\nwant the level's subquery", dbType, sql)
			}
			perms.mu.Lock()
			for _, call := range perms.asked {
				if slices.ContainsFunc(call.resources, func(res accesstypes.Resource) bool { return strings.HasPrefix(string(res), "PassLevels") }) {
					t.Errorf("%s asked %+v, want no call naming PassLevels", dbType, call)
				}
			}
			perms.mu.Unlock()
		}
	})

	refusals := []struct {
		name     string
		target   string
		perms    *nameGrantPermissions
		sentence string
		cause    string
	}{
		{
			name:     "the name's grant denied",
			target:   "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2",
			perms:    deniedInSector1(squadronNameGrant),
			sentence: "You may not sort or filter hangar passes by the squadron name here.",
			cause:    "scope (sector-1), user (u1) cannot sort or filter on squadronName: (List) on Squadrons.name is denied",
		},
		{
			name:     "a sorted name and a filtered name denied: the sort's first",
			target:   "/?columns=id&filter=squadronName:eq:Red&sort=crewName&limit=2",
			perms:    deniedInSector1(squadronNameGrant, crewNameGrant),
			sentence: "You may not sort or filter hangar passes by the crew name here.",
			cause:    "scope (sector-1), user (u1) cannot sort or filter on crewName: (List) on Crews.name is denied",
		},
		{
			name:     "the id denied: refused as a filter on the id",
			target:   "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2",
			perms:    deniedInSector1("HangarPasses.squadronId"),
			sentence: "You may not sort or filter hangar passes by the squadron id here.",
			cause:    "(List) on HangarPasses.squadronId is denied",
		},
		{
			name:     "the id and the name denied: the id's refusal first",
			target:   "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2",
			perms:    deniedInSector1("HangarPasses.squadronId", squadronNameGrant),
			sentence: "You may not sort or filter hangar passes by the squadron id here.",
			cause:    "(List) on HangarPasses.squadronId is denied",
		},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
				_, err := decodePasses(t, accesstypes.List, Paging{}, tt.target, tt.perms, dbType)
				plainAnswer(t, err, httpio.HasForbidden, tt.sentence, tt.cause)
			}
		})
	}

	held := []struct {
		name       string
		perms      *nameGrantPermissions
		want       map[DBType]string
		wantParams map[string]any
	}{
		{
			name: "the name's grant held under a condition: the condition after the tenant test",
			perms: &nameGrantPermissions{decisions: map[grantByScope]accesstypes.Decision{
				{scope: inSector1, perm: accesstypes.List, res: squadronNameGrant}: conditionalOn(squadronNameGrant, "rank = 1"),
			}},
			want: bothDatabases(
				"WHERE `SquadronId` IN (SELECT `ca1`.`Id` FROM `Squadrons` `ca1` WHERE `ca1`.`SectorId` = @domain AND (`ca1`.`Rank` = @_c1) AND `Name` = @_p1)",
				`WHERE "SquadronId" IN (SELECT "ca1"."Id" FROM "Squadrons" "ca1" WHERE "ca1"."SectorId" = @domain AND ("ca1"."Rank" = @_c1) AND "Name" = @_p1)`,
			),
			wantParams: map[string]any{"_c1": int64(1), "_p1": "Red"},
		},
		{
			name: "the id held under a condition on the pass: the id read inside it",
			perms: &nameGrantPermissions{decisions: map[grantByScope]accesstypes.Decision{
				{scope: inSector1, perm: accesstypes.List, res: "HangarPasses.squadronId"}: conditionalOn("HangarPasses.squadronId", "level = 'visit'"),
			}},
			want: bothDatabases(
				"WHERE CASE WHEN `HangarPasses`.`LevelId` = @_c1 THEN `SquadronId` END IN (SELECT `ca1`.`Id` FROM `Squadrons` `ca1` WHERE `ca1`.`SectorId` = @domain AND `Name` = @_p1)",
				`WHERE CASE WHEN "HangarPasses"."LevelId" = @_c1 THEN "SquadronId" END IN (SELECT "ca1"."Id" FROM "Squadrons" "ca1" WHERE "ca1"."SectorId" = @domain AND "Name" = @_p1)`,
			),
			wantParams: map[string]any{"_c1": "visit", "_p1": "Red"},
		},
	}
	for _, tt := range held {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for dbType, want := range tt.want {
				qSet, sql := passesStatement(t, Paging{}, "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2", tt.perms, dbType)
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
				}
				stmt, err := qSet.stmt(dbType)
				if err != nil {
					t.Fatalf("stmt(%s) error = %v", dbType, err)
				}
				for param, value := range tt.wantParams {
					if got := stmt.Params[param]; got != value {
						t.Errorf("%s params[%s] = %v, want %v", dbType, param, got, value)
					}
				}
			}
		})
	}
}

// positionalPassRequest is lookupPassRequest with the squadron's id declared
// masking:"positional": a filter on the id compares the raw column even where
// its cells are shown under a condition.
type positionalPassRequest struct {
	ID           string  `json:"id"           index:"true" perm:"-"`
	SectorID     string  `json:"sectorId"     index:"true"`
	SquadronID   *string `json:"squadronId"   index:"true" masking:"positional"`
	LevelID      string  `json:"levelId"      index:"true"`
	SquadronName *string `json:"squadronName" index:"true" nameof:"SquadronID"`
	LevelName    *string `json:"levelName"    index:"true" nameof:"LevelID"`
	CrewID       *string `json:"crewId"       index:"true"`
	CrewName     *string `json:"crewName"     index:"true" nameof:"CrewID"`
}

// TestNameFilter_positionalID pins that a filter by a name reads the id as the
// name's lookup reads it, inside the condition the id's cells are shown under,
// and not as a filter on the id reads it: a positional id's own filter compares
// the raw column, while its name is shown only where the id is, so a filter by
// the name tests the id inside its condition.
func TestNameFilter_positionalID(t *testing.T) {
	t.Parallel()

	const target = "/?columns=id&filter=squadronName:eq:Red,squadronId:eq:s1&sort=id&limit=2"
	for dbType, want := range bothDatabases(
		"WHERE CASE WHEN `HangarPasses`.`LevelId` = @_c1 THEN `SquadronId` END IN (SELECT `ca1`.`Id` FROM `Squadrons` `ca1` WHERE `ca1`.`SectorId` = @domain AND `Name` = @_p1) AND `SquadronId` = @_p2",
		`WHERE CASE WHEN "HangarPasses"."LevelId" = @_c1 THEN "SquadronId" END IN (SELECT "ca1"."Id" FROM "Squadrons" "ca1" WHERE "ca1"."SectorId" = @domain AND "Name" = @_p1) AND "SquadronId" = @_p2`,
	) {
		resSet, err := NewSet[lookupPass, positionalPassRequest](accesstypes.List)
		if err != nil {
			t.Fatalf("NewSet() error = %v", err)
		}
		decoder, err := NewQueryDecoder[lookupPass, positionalPassRequest](resSet)
		if err != nil {
			t.Fatalf("NewQueryDecoder() error = %v", err)
		}
		key, err := NewCursorKey("dGVzdC1jdXJzb3Ita2V5LW1hdGVyaWFsLTMyLWJ5dGVzIQ==")
		if err != nil {
			t.Fatalf("NewCursorKey() error = %v", err)
		}
		decoder = decoder.WithCursorKey(key)
		decoder.collection = lookupCollection(t)
		perms := &nameGrantPermissions{decisions: map[grantByScope]accesstypes.Decision{
			{scope: inSector1, perm: accesstypes.List, res: "HangarPasses.squadronId"}: conditionalOn("HangarPasses.squadronId", "level = 'visit'"),
		}}
		qSet, err := decoder.Decode(httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody), perms, inSector1)
		if err != nil {
			t.Fatalf("QueryDecoder.Decode(%s) error = %v", target, err)
		}
		if err := qSet.checkPermissions(t.Context(), dbType); err != nil {
			t.Fatalf("checkPermissions(%s) error = %v", dbType, err)
		}
		stmt, err := qSet.stmt(dbType)
		if err != nil {
			t.Fatalf("stmt(%s) error = %v", dbType, err)
		}
		if sql := oneLine(stmt.SQL); !strings.Contains(sql, want) {
			t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
		}
	}
}

// TestNameFilter_spaced pins a condition whose field name is padded with white
// space (` levelId:eq:visit`, `levelId :eq:visit`): the parser reads it as the
// field, and what the decoder decides about the filter is read from the parser's
// tree, so the padded condition is held to everything the unpadded one is held
// to: its field's grant, the condition its field is shown under and the
// sensitive-field rule, on a field of the resource's own as on a name.
func TestNameFilter_spaced(t *testing.T) {
	t.Parallel()

	t.Run("a padded filter decodes to the fields, names and box of the unpadded one", func(t *testing.T) {
		t.Parallel()

		type decoded struct {
			Fields []accesstypes.Field
			Names  []accesstypes.Field
			Box    bool
		}
		decode := func(t *testing.T, filter string) decoded {
			t.Helper()

			qSet, err := decodeNameFilter[lookupPassRequest](t, filterRequest(t, "/?columns=id&filter="+filter+"&sort=id&limit=2", ""))
			if err != nil {
				t.Fatalf("DecodeWithoutPermissions(%s) error = %v", filter, err)
			}

			return decoded{Fields: qSet.filterFields, Names: qSet.filterNames, Box: qSet.searchBox}
		}
		for _, tt := range []struct{ padded, unpadded string }{
			{padded: "%20squadronId:eq:s1", unpadded: "squadronId:eq:s1"},
			{padded: "%20squadronName:eq:Red", unpadded: "squadronName:eq:Red"},
			{padded: "%20squadronName:eq:Red,%20levelId:eq:visit", unpadded: "squadronName:eq:Red,levelId:eq:visit"},
			{padded: "levelId%20:eq:visit", unpadded: "levelId:eq:visit"},
		} {
			want := decode(t, tt.unpadded)
			if len(want.Fields) == 0 {
				t.Fatalf("%s decodes to no field", tt.unpadded)
			}
			if diff := cmp.Diff(want, decode(t, tt.padded)); diff != "" {
				t.Errorf("%s decodes apart from %s (-want +got):\n%s", tt.padded, tt.unpadded, diff)
			}
		}
	})

	refusals := []struct {
		name     string
		target   string
		perms    *nameGrantPermissions
		sentence string
		cause    string
	}{
		{
			name:     "a field of the resource's own, its grant denied",
			target:   "/?columns=id&filter=%20levelId:eq:visit&sort=id&limit=2",
			perms:    deniedInSector1("HangarPasses.levelId"),
			sentence: "You may not sort or filter hangar passes by the level id here.",
			cause:    "(List) on HangarPasses.levelId is denied",
		},
		{
			name:     "a field of the resource's own padded after its name, its grant denied",
			target:   "/?columns=id&filter=levelId%20:eq:visit&sort=id&limit=2",
			perms:    deniedInSector1("HangarPasses.levelId"),
			sentence: "You may not sort or filter hangar passes by the level id here.",
			cause:    "(List) on HangarPasses.levelId is denied",
		},
		{
			name:     "an enumeration table's name, its id's grant denied: refused as the id",
			target:   "/?columns=id&filter=%20levelName:eq:Visit&sort=id&limit=2",
			perms:    deniedInSector1("HangarPasses.levelId"),
			sentence: "You may not sort or filter hangar passes by the level id here.",
			cause:    "(List) on HangarPasses.levelId is denied",
		},
		{
			name:     "a tenant's row's name, the name's grant denied",
			target:   "/?columns=id&filter=%20squadronName:eq:Red&sort=id&limit=2",
			perms:    deniedInSector1(squadronNameGrant),
			sentence: "You may not sort or filter hangar passes by the squadron name here.",
			cause:    "(List) on Squadrons.name is denied",
		},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
				_, err := decodePasses(t, accesstypes.List, Paging{}, tt.target, tt.perms, dbType)
				plainAnswer(t, err, httpio.HasForbidden, tt.sentence, tt.cause)
			}
		})
	}

	statements := []struct {
		name     string
		padded   string
		unpadded string
		perms    *nameGrantPermissions
		want     map[DBType]string
	}{
		{
			name:     "a field of the resource's own held under a condition: filtered inside it",
			padded:   "%20squadronId:eq:s1",
			unpadded: "squadronId:eq:s1",
			perms: &nameGrantPermissions{decisions: map[grantByScope]accesstypes.Decision{
				{scope: inSector1, perm: accesstypes.List, res: "HangarPasses.squadronId"}: conditionalOn("HangarPasses.squadronId", "level = 'visit'"),
			}},
			want: bothDatabases(
				"WHERE CASE WHEN `HangarPasses`.`LevelId` = @_c1 THEN `SquadronId` END = @_p1",
				`WHERE CASE WHEN "HangarPasses"."LevelId" = @_c1 THEN "SquadronId" END = @_p1`,
			),
		},
		{
			name:     "an enumeration table's name, its id held under a condition: the id read inside it",
			padded:   "%20levelName:eq:Visit",
			unpadded: "levelName:eq:Visit",
			perms: &nameGrantPermissions{decisions: map[grantByScope]accesstypes.Decision{
				{scope: inSector1, perm: accesstypes.List, res: "HangarPasses.levelId"}: conditionalOn("HangarPasses.levelId", "level = 'visit'"),
			}},
			want: bothDatabases(
				"WHERE CASE WHEN `HangarPasses`.`LevelId` = @_c1 THEN `LevelId` END IN (SELECT `ca1`.`Id` FROM `PassLevels` `ca1` WHERE `Description` = @_p1)",
				`WHERE CASE WHEN "HangarPasses"."LevelId" = @_c1 THEN "LevelId" END IN (SELECT "ca1"."Id" FROM "PassLevels" "ca1" WHERE "Description" = @_p1)`,
			),
		},
		{
			name:     "a name alone, every grant held",
			padded:   "%20squadronName:eq:Red",
			unpadded: "squadronName:eq:Red",
			perms:    &nameGrantPermissions{},
			want: bothDatabases(
				"WHERE `SquadronId` IN (SELECT `ca1`.`Id` FROM `Squadrons` `ca1` WHERE `ca1`.`SectorId` = @domain AND `Name` = @_p1)",
				`WHERE "SquadronId" IN (SELECT "ca1"."Id" FROM "Squadrons" "ca1" WHERE "ca1"."SectorId" = @domain AND "Name" = @_p1)`,
			),
		},
	}
	for _, tt := range statements {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for dbType, want := range tt.want {
				_, unpadded := passesStatement(t, Paging{}, "/?columns=id&filter="+tt.unpadded+"&sort=id&limit=2", tt.perms, dbType)
				_, padded := passesStatement(t, Paging{}, "/?columns=id&filter="+tt.padded+"&sort=id&limit=2", tt.perms, dbType)
				if !strings.Contains(unpadded, want) {
					t.Errorf("%s statement of %s =\n%s\nwant it to contain:\n%s", dbType, tt.unpadded, unpadded, want)
				}
				if padded != unpadded {
					t.Errorf("%s statement of %s =\n%s\nwant the statement of %s:\n%s", dbType, tt.padded, padded, tt.unpadded, unpadded)
				}
			}
		})
	}
}

// countedApart is makerApart that counts, by resource, the checks asked in the
// global scope.
type countedApart struct {
	*makerApart

	mu     sync.Mutex
	global map[accesstypes.Resource]int
}

func (p *countedApart) Check(ctx context.Context, env accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	if scope.IsGlobal() {
		p.mu.Lock()
		for _, res := range resources {
			p.global[res]++
		}
		p.mu.Unlock()
	}

	return p.makerApart.Check(ctx, env, scope, perm, resources...)
}

// TestAcrossTenants_nameFilters pins a filter by a name on a list over north and
// south: a name of a resource of no tenant asks its grant once for the list, and
// an enumeration table's asks none; a denied name refuses in the words of a list
// across tenants; an id a tenant hides is read where its tenant shows it, and an
// id every tenant hides refuses as a filter on the id.
func TestAcrossTenants_nameFilters(t *testing.T) {
	t.Parallel()

	paging := Paging{Order: []SortField{{Field: "Label", Direction: SortAscending}}, DefaultLimit: 3}
	tests := []struct {
		name         string
		target       string
		perms        func() *countedApart
		wantSpanner  string
		wantPostgres string
		wantAsks     map[accesstypes.Resource]int
	}{
		{
			name:         "a global name: its grant asked once",
			target:       "/crates?columns=id&filter=makerName:eq:Acme",
			wantSpanner:  "`MakerId` IN (SELECT `ca1`.`Id` FROM `Makers` `ca1` WHERE `Name` = @_p1)",
			wantPostgres: `"MakerId" IN (SELECT "ca1"."Id" FROM "Makers" "ca1" WHERE "Name" = @_p1)`,
			wantAsks:     map[accesstypes.Resource]int{makerNameGrant: 1},
		},
		{
			name:         "an enumeration table's name: no grant asked",
			target:       "/crates?columns=id&filter=gradeName:eq:Heavy",
			wantSpanner:  "`GradeId` IN (SELECT `ca1`.`Id` FROM `CrateGrades` `ca1` WHERE `Description` = @_p1)",
			wantPostgres: `"GradeId" IN (SELECT "ca1"."Id" FROM "CrateGrades" "ca1" WHERE "Description" = @_p1)`,
			wantAsks:     map[accesstypes.Resource]int{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for dbType, want := range map[DBType]string{SpannerDBType: tt.wantSpanner, PostgresDBType: tt.wantPostgres} {
				perms := &countedApart{
					makerApart: &makerApart{acrossPermissions: &acrossPermissions{
						reach:  []accesstypes.Domain{"north", "south"},
						grants: map[accesstypes.Domain]accesstypes.Decisions{"north": everyGrant(), "south": everyGrant()},
					}},
					global: make(map[accesstypes.Resource]int),
				}
				_, sql := renderGradedIn(t, dbType, paging, tt.target, perms)
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
				}
				perms.mu.Lock()
				if diff := cmp.Diff(tt.wantAsks, perms.global); diff != "" {
					t.Errorf("%s global checks mismatch (-want +got):\n%s", dbType, diff)
				}
				perms.mu.Unlock()
			}
		})
	}

	t.Run("a denied global name: refused anywhere", func(t *testing.T) {
		t.Parallel()

		perms := &makerApart{
			acrossPermissions: &acrossPermissions{
				reach:  []accesstypes.Domain{"north", "south"},
				grants: map[accesstypes.Domain]accesstypes.Decisions{"north": everyGrant(), "south": everyGrant()},
			},
			global: accesstypes.Decisions{makerNameGrant: accesstypes.Denied()},
		}
		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			decoder := acrossDecoder[gradedCrate, gradedCrateRequest](t, paging)
			qSet, err := decoder.DecodeAcrossTenants(acrossRequest(t, "/crates?columns=id&filter=makerName:eq:Acme"), perms)
			if err != nil {
				t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
			}
			plainAnswer(t, qSet.checkPermissions(t.Context(), dbType), httpio.HasForbidden,
				"You may not sort or filter crates by the maker name anywhere.",
				"scope (global), user (erin) cannot sort or filter on makerName: (List) on Makers.name is denied")
		}
	})

	t.Run("the id hidden in south: the id read where its tenant shows it", func(t *testing.T) {
		t.Parallel()

		wants := map[DBType]string{
			SpannerDBType:  "CASE WHEN " + spannerInNorth + " THEN `GradeId` END IN (SELECT `ca1`.`Id` FROM `CrateGrades` `ca1` WHERE `Description` = @_p1)",
			PostgresDBType: "CASE WHEN " + postgresInNorth + ` THEN "GradeId" END IN (SELECT "ca1"."Id" FROM "CrateGrades" "ca1" WHERE "Description" = @_p1)`,
		}
		for dbType, want := range wants {
			_, sql := renderGradedIn(t, dbType, paging, "/crates?columns=id&filter=gradeName:eq:Heavy", gradedRoles(accesstypes.Denied()))
			if !strings.Contains(sql, want) {
				t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
			}
		}
	})

	t.Run("the id hidden in every tenant: refused as a filter on the id", func(t *testing.T) {
		t.Parallel()

		perms := gradedRoles(accesstypes.Denied())
		perms.grants["north"] = accesstypes.Decisions{acrossCrates + ".gradeId": accesstypes.Denied()}
		decoder := acrossDecoder[gradedCrate, gradedCrateRequest](t, paging)
		qSet, err := decoder.DecodeAcrossTenants(acrossRequest(t, "/crates?columns=id&filter=gradeName:eq:Heavy"), perms)
		if err != nil {
			t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
		}
		plainAnswer(t, qSet.checkPermissions(t.Context(), SpannerDBType), httpio.HasForbidden,
			"You may not sort or filter crates by the grade id anywhere.",
			"user (erin) cannot sort or filter on gradeId: (List) on Crates.gradeId is denied in every tenant the list reads")
	})
}
