package resource

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// lookupPass is a tenant's resource whose rows point to a squadron, a tenant's
// resource that says what its rows are known by, to a level, an enumeration
// table, and to a crew, which reaches its tenant through its wing.
type lookupPass struct {
	ID         string  `spanner:"Id"         postgres:"Id"`
	SectorID   string  `spanner:"SectorId"   postgres:"SectorId"`
	SquadronID *string `spanner:"SquadronId" postgres:"SquadronId"`
	LevelID    string  `spanner:"LevelId"    postgres:"LevelId"`
	CrewID     *string `spanner:"CrewId"     postgres:"CrewId"`
}

// Resource names the resource in grants and routes.
func (lookupPass) Resource() accesstypes.Resource {
	return "HangarPasses"
}

// DefaultConfig is the resource's configuration.
func (lookupPass) DefaultConfig() Config {
	return Config{}
}

// NameFields describes the resource's name fields as the generator writes them.
func (lookupPass) NameFields() []NameField {
	return []NameField{
		{Field: "SquadronName", Key: "SquadronID", Resource: "Squadrons", Tag: "name", Table: "Squadrons", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "SectorId"},
		{Field: "LevelName", Key: "LevelID", Table: "PassLevels", KeyColumn: "Id", NameColumn: "Description"},
		{
			Field: "CrewName", Key: "CrewID", Resource: "Crews", Tag: "name", Table: "Crews", KeyColumn: "Id", NameColumn: "Name",
			TenantColumn: "WingId", TenantPath: []BindingHop{{Table: "Wings", JoinColumn: "Id", Column: "SectorId"}},
		},
	}
}

// lookupPassRequest is lookupPass's list request type as the generator writes it.
type lookupPassRequest struct {
	ID           string  `json:"id"           index:"true" perm:"-"`
	SectorID     string  `json:"sectorId"     index:"true"`
	SquadronID   *string `json:"squadronId"   index:"true"`
	LevelID      string  `json:"levelId"      index:"true"`
	SquadronName *string `json:"squadronName" index:"true" nameof:"SquadronID" search:"token=Name_Search;index=SquadronsSearch;role=text;contains"`
	LevelName    *string `json:"levelName"    index:"true" nameof:"LevelID"`
	CrewID       *string `json:"crewId"       index:"true"`
	CrewName     *string `json:"crewName"     index:"true" nameof:"CrewID"`
}

const (
	squadronLookup = "(SELECT `ca1`.`Name` FROM `Squadrons` `ca1` WHERE `ca1`.`Id` = `HangarPasses`.`SquadronId` AND `ca1`.`SectorId` = @domain)"
	levelLookup    = "(SELECT `ca1`.`Description` FROM `PassLevels` `ca1` WHERE `ca1`.`Id` = `HangarPasses`.`LevelId`)"
	crewLookup     = "(SELECT `ca1`.`Name` FROM `Crews` `ca1` WHERE `ca1`.`Id` = `HangarPasses`.`CrewId` AND (SELECT `ca2`.`SectorId` FROM `Wings` `ca2` WHERE `ca2`.`Id` = `ca1`.`WingId`) = @domain)"
)

// lookupQuerySet decodes a list request for lookupPass inside one tenant, under the
// declared paging, with no grants in the way.
func lookupQuerySet(t *testing.T, paging Paging, target string) *QuerySet[lookupPass] {
	t.Helper()

	resSet, err := NewSet[lookupPass, lookupPassRequest](accesstypes.List)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewQueryDecoder[lookupPass, lookupPassRequest](resSet)
	if err != nil {
		t.Fatalf("NewQueryDecoder() error = %v", err)
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("WithPaging() panicked: %v", r)
			}
		}()
		decoder.WithPaging(paging)
	}()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	qSet, err := decoder.DecodeWithoutPermissions(req)
	if err != nil {
		t.Fatalf("DecodeWithoutPermissions(%s) error = %v", target, err)
	}
	qSet.scope = accesstypes.DomainScope("sector-1")
	if err := qSet.checkPermissions(t.Context(), SpannerDBType); err != nil {
		t.Fatalf("checkPermissions() error = %v", err)
	}

	return qSet
}

// oneLine folds a statement's white space, so a fragment is found whatever the
// statement's layout.
func oneLine(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}

// TestNameLookup_statement pins what a list's statement writes in a name field's
// place: one scalar subquery over the pointed-to table by the id, held to the
// request's tenant where that table belongs to one, wherever the statement names
// the field (the select list, the order).
func TestNameLookup_statement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		paging       Paging
		target       string
		wantContains []string
		wantWithout  []string
	}{
		{
			name:   "sorted by the name of a tenant's row",
			target: "/?sort=squadronName:asc&limit=2",
			wantContains: []string{
				"ORDER BY " + squadronLookup + " ASC, `Id` ASC",
				squadronLookup + " AS zzNameSquadronName",
			},
		},
		{
			name:   "sorted by an enumeration table's name, the other way",
			target: "/?sort=levelName:desc&columns=id&limit=2",
			wantContains: []string{
				"ORDER BY " + levelLookup + " DESC, `Id` ASC",
				levelLookup + " AS zzNameLevelName",
			},
			wantWithout: []string{"@domain", "zzNameSquadronName"},
		},
		{
			name:   "a declared order by a name",
			paging: Paging{Order: []SortField{{Field: "LevelName", Direction: SortAscending}}},
			target: "/?columns=id&limit=2",
			wantContains: []string{
				"ORDER BY " + levelLookup + " ASC, `Id` ASC",
			},
		},
		{
			name:   "a name asked for as a column",
			target: "/?columns=id,squadronName&sort=id&limit=2",
			wantContains: []string{
				"SELECT Id, " + squadronLookup + " AS zzNameSquadronName FROM HangarPasses",
				"ORDER BY `Id` ASC",
			},
			wantWithout: []string{"zzNameLevelName"},
		},
		{
			name:   "a name asked for alone",
			target: "/?columns=levelName&sort=id&limit=all",
			wantContains: []string{
				"SELECT Id AS zzCursorId, " + levelLookup + " AS zzNameLevelName FROM HangarPasses",
			},
		},
		{
			name:   "the name of a row that reaches its tenant through another table",
			target: "/?columns=id,crewName&sort=crewName&limit=2",
			wantContains: []string{
				"SELECT Id, " + crewLookup + " AS zzNameCrewName FROM HangarPasses",
				"ORDER BY " + crewLookup + " ASC, `Id` ASC",
			},
		},
		{
			name:   "no columns named: the name beside every id returned",
			target: "/?sort=id&limit=2",
			wantContains: []string{
				" AS zzNameSquadronName",
				" AS zzNameLevelName",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			qSet := lookupQuerySet(t, tt.paging, tt.target)
			stmt, err := qSet.stmt(SpannerDBType)
			if err != nil {
				t.Fatalf("stmt() error = %v", err)
			}
			sql := oneLine(stmt.SQL)
			for _, want := range tt.wantContains {
				if !strings.Contains(sql, want) {
					t.Errorf("stmt() SQL =\n%s\nwant it to contain:\n%s", sql, want)
				}
			}
			for _, without := range tt.wantWithout {
				if strings.Contains(sql, without) {
					t.Errorf("stmt() SQL =\n%s\nwant it without:\n%s", sql, without)
				}
			}
			if strings.Contains(sql, "@domain") && stmt.Params["domain"] != "sector-1" {
				t.Errorf("stmt() domain parameter = %v, want the request's tenant", stmt.Params["domain"])
			}
		})
	}
}

// TestNameLookup_cursor pins that a list sorted by a name pages by it: the cursor
// reads the boundary row's name off the row's envelope, and the next page's
// predicate compares the lookup with it, the key breaking ties among equal names.
func TestNameLookup_cursor(t *testing.T) {
	t.Parallel()

	qSet := lookupQuerySet(t, Paging{}, "/?sort=squadronName:asc&columns=id&limit=2")

	red := "Red"
	row := &Row[lookupPass]{
		Data:         lookupPass{ID: "p2"},
		cursorValues: map[accesstypes.Field]reflect.Value{"SquadronName": reflect.ValueOf(&red)},
	}
	keys, err := qSet.boundaryKeys(row)
	if err != nil {
		t.Fatalf("boundaryKeys() error = %v", err)
	}
	p2 := "p2"
	if diff := cmp.Diff([]*string{&red, &p2}, keys); diff != "" {
		t.Errorf("boundaryKeys() mismatch (-want +got):\n%s", diff)
	}

	qSet.cursor = &cursor{Direction: pageNext, Keys: keys}
	stmt, err := qSet.stmt(SpannerDBType)
	if err != nil {
		t.Fatalf("stmt() error = %v", err)
	}
	sql := oneLine(stmt.SQL)
	where, _, _ := strings.Cut(sql, "ORDER BY")
	_, where, _ = strings.Cut(where, "WHERE")
	if !strings.Contains(where, squadronLookup+" > @") || !strings.Contains(where, squadronLookup+" = @") || !strings.Contains(where, "`Id` > @") {
		t.Errorf("stmt() WHERE =\n%s\nwant the rows after the boundary: a later name, or the same name and a later key", where)
	}
	var bound []any
	for _, value := range stmt.Params {
		bound = append(bound, value)
	}
	if !slicesContainsAny(bound, "Red") || !slicesContainsAny(bound, "p2") {
		t.Errorf("stmt() params = %v, want the boundary row's name and key bound", stmt.Params)
	}

	// A boundary row with no name (its id points to no row) is the NULL key.
	row.cursorValues["SquadronName"] = reflect.ValueOf((*string)(nil))
	keys, err = qSet.boundaryKeys(row)
	if err != nil {
		t.Fatalf("boundaryKeys() error = %v", err)
	}
	if diff := cmp.Diff([]*string{nil, &p2}, keys); diff != "" {
		t.Errorf("boundaryKeys() of a row with no name mismatch (-want +got):\n%s", diff)
	}
}

// slicesContainsAny reports whether values holds want.
func slicesContainsAny(values []any, want any) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

// undescribedPass is a resource that describes no name field.
type undescribedPass struct {
	ID         string `spanner:"Id"`
	SquadronID string `spanner:"SquadronId"`
}

// Resource names the resource in grants and routes.
func (undescribedPass) Resource() accesstypes.Resource {
	return "UndescribedPasses"
}

// DefaultConfig is the resource's configuration.
func (undescribedPass) DefaultConfig() Config {
	return Config{}
}

// TestNewQueryDecoder_undescribedName pins that a request type carrying a name field
// its resource describes no lookup for is refused where the decoder is built, with
// what to do.
func TestNewQueryDecoder_undescribedName(t *testing.T) {
	t.Parallel()

	resSet, err := NewSet[undescribedPass, nameFieldRequest](accesstypes.List)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	_, err = NewQueryDecoder[undescribedPass, nameFieldRequest](resSet)
	want := `request field SquadronName is marked the name of SquadronID (nameof:"SquadronID"), but the resource's NameFields() describes no such name, so there is nothing to look it up in; regenerate the resource and its handlers together`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("NewQueryDecoder() error = %v, want it to contain %q", err, want)
	}
}

// passViewQuery is the query a test view of passes opens with.
type passViewQuery interface {
	query() string
}

// squadronsWith opens the view's query with a WITH table spelled as the table the
// squadron's name is read from.
type squadronsWith struct{}

func (squadronsWith) query() string {
	return "WITH Squadrons AS (SELECT Id, SectorId, SquadronId, LevelId, CrewId FROM HangarPasses) SELECT * FROM Squadrons"
}

// lowerSquadronsWith spells that WITH table in another case.
type lowerSquadronsWith struct{}

func (lowerSquadronsWith) query() string {
	return "WITH squadrons AS (SELECT Id, SectorId, SquadronId, LevelId, CrewId FROM HangarPasses) SELECT * FROM squadrons"
}

// levelsThenSquadronsWith opens with a WITH table spelled as each of two names'
// tables, the later name's first.
type levelsThenSquadronsWith struct{}

func (levelsThenSquadronsWith) query() string {
	return "WITH PassLevels AS (SELECT Id FROM Levels), Squadrons AS (SELECT Id, SectorId, SquadronId, LevelId, CrewId FROM HangarPasses) SELECT * FROM Squadrons"
}

// renamedWith opens with a WITH table no name is read from.
type renamedWith struct{}

func (renamedWith) query() string {
	return "WITH PassRows AS (SELECT Id, SectorId, SquadronId, LevelId, CrewId FROM HangarPasses) SELECT * FROM PassRows"
}

// passView is a view (a @virtual resource) of passes, with lookupPass's names, whose
// query Q gives.
type passView[Q passViewQuery] struct {
	ID         string  `spanner:"Id"         postgres:"Id"`
	SectorID   string  `spanner:"SectorId"   postgres:"SectorId"`
	SquadronID *string `spanner:"SquadronId" postgres:"SquadronId"`
	LevelID    string  `spanner:"LevelId"    postgres:"LevelId"`
	CrewID     *string `spanner:"CrewId"     postgres:"CrewId"`
}

// Resource names the resource in grants and routes.
func (passView[Q]) Resource() accesstypes.Resource {
	return "PassViews"
}

// DefaultConfig is the resource's configuration.
func (passView[Q]) DefaultConfig() Config {
	return Config{}
}

// NameFields describes the view's name fields: lookupPass's.
func (passView[Q]) NameFields() []NameField {
	return lookupPass{}.NameFields()
}

// Subquery is the view's query.
func (passView[Q]) Subquery() (query string, params map[string]any) {
	var q Q

	return q.query(), nil
}

// passViewPlainRequest is a list request type of the view that carries no name.
type passViewPlainRequest struct {
	ID         string  `json:"id"         index:"true" perm:"-"`
	SectorID   string  `json:"sectorId"   index:"true"`
	SquadronID *string `json:"squadronId" index:"true"`
}

// newPassViewDecoder builds the decoder of a list of the view over the request type.
func newPassViewDecoder[Q passViewQuery, Request any](t *testing.T) error {
	t.Helper()

	resSet, err := NewSet[passView[Q], Request](accesstypes.List)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	_, err = NewQueryDecoder[passView[Q], Request](resSet)

	return err
}

// TestNewQueryDecoder_withTableNamedAsAName pins that a view whose query opens with a
// WITH table spelled, in any case, as the table one of the request's names is read
// from is refused where its list's decoder is built, naming the first such name in the
// request type's order and what to rename; and that the view builds with the WITH
// table renamed, or over a request type that carries no name.
func TestNewQueryDecoder_withTableNamedAsAName(t *testing.T) {
	t.Parallel()

	const refusal = "the query of PassViews opens with a WITH table named %s, which is also the table the name %s is read from: a statement over the view would read the name from the WITH table's rows. Rename the WITH table in the view's query."
	tests := []struct {
		name    string
		build   func(t *testing.T) error
		wantErr string
	}{
		{
			name:    "a WITH table spelled as a name's table",
			build:   newPassViewDecoder[squadronsWith, lookupPassRequest],
			wantErr: fmt.Sprintf(refusal, "Squadrons", "SquadronName"),
		},
		{
			name:    "spelled in another case",
			build:   newPassViewDecoder[lowerSquadronsWith, lookupPassRequest],
			wantErr: fmt.Sprintf(refusal, "squadrons", "SquadronName"),
		},
		{
			name:    "two names' tables: the first name in the request type's order",
			build:   newPassViewDecoder[levelsThenSquadronsWith, lookupPassRequest],
			wantErr: fmt.Sprintf(refusal, "Squadrons", "SquadronName"),
		},
		{
			name:  "the WITH table renamed",
			build: newPassViewDecoder[renamedWith, lookupPassRequest],
		},
		{
			name:  "a request type with no name",
			build: newPassViewDecoder[squadronsWith, passViewPlainRequest],
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.build(t)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NewQueryDecoder() error = %v, want none", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewQueryDecoder() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// flaggedPass is a resource whose rows point to a squadron by an id a feature flag
// hides, with the squadron's name beside it.
type flaggedPass struct {
	ID         string  `spanner:"Id"         postgres:"Id"`
	SquadronID *string `spanner:"SquadronId" postgres:"SquadronId"`
}

// Resource names the resource in grants and routes.
func (flaggedPass) Resource() accesstypes.Resource {
	return "FlaggedPasses"
}

// NameFields describes the name beside the flagged id as the generator writes it.
func (flaggedPass) NameFields() []NameField {
	return []NameField{
		{Field: "SquadronName", Key: "SquadronID", Resource: "Squadrons", Tag: "name", Table: "Squadrons", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "SectorId"},
	}
}

// flaggedPassRequest is flaggedPass's list request type: the id behind the flag
// squadrons, the name beside it.
type flaggedPassRequest struct {
	ID           string  `json:"id"           index:"true"        perm:"-"`
	SquadronID   *string `json:"squadronId"   feature:"squadrons" index:"true"`
	SquadronName *string `json:"squadronName" index:"true"        nameof:"SquadronID"`
}

// decodeFlagged decodes a list request for sector-1's flagged passes, which the
// declared order sorts by the squadron's name, under the grants perms answers. The
// decoder has no flags wired, so the id is hidden.
func decodeFlagged(t *testing.T, target string, perms *nameGrantPermissions) (*QuerySet[flaggedPass], error) {
	t.Helper()

	resSet, err := NewSet[flaggedPass, flaggedPassRequest](accesstypes.List)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewQueryDecoder[flaggedPass, flaggedPassRequest](resSet)
	if err != nil {
		t.Fatalf("NewQueryDecoder() error = %v", err)
	}
	key, err := NewCursorKey("dGVzdC1jdXJzb3Ita2V5LW1hdGVyaWFsLTMyLWJ5dGVzIQ==")
	if err != nil {
		t.Fatalf("NewCursorKey() error = %v", err)
	}
	decoder = decoder.WithCursorKey(key).WithPaging(Paging{Order: []SortField{{Field: "SquadronName", Direction: SortAscending}}})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)

	return decoder.Decode(req, perms, inSector1)
}

// TestNameLookup_hiddenID pins a declared order by a name whose id a feature flag
// hides: the list still orders by the name's lookup, as a declared order by an
// ordinary field behind a flag orders by its column, while no request may name the
// name and no row carries it or its id.
func TestNameLookup_hiddenID(t *testing.T) {
	t.Parallel()

	lookups := bothDatabases(
		"(SELECT `ca1`.`Name` FROM `Squadrons` `ca1` WHERE `ca1`.`Id` = `FlaggedPasses`.`SquadronId` AND `ca1`.`SectorId` = @domain)",
		`(SELECT "ca1"."Name" FROM "Squadrons" "ca1" WHERE "ca1"."Id" = "FlaggedPasses"."SquadronId" AND "ca1"."SectorId" = @domain)`,
	)
	tests := []struct {
		name    string
		target  string
		wantErr string
	}{
		{name: "F1 columns named: ordered by the name", target: "/?columns=id&limit=2"},
		{name: "F2 no columns named: ordered by the name, which no row carries", target: "/?limit=2"},
		{name: "F3 the name as a column is an unknown column", target: "/?columns=id,squadronName&limit=2", wantErr: "unknown column: squadronName"},
		{name: "F3 the name as a sort is an unknown sort field", target: "/?columns=id&sort=squadronName&limit=2", wantErr: "unknown sort field: squadronName"},
		{name: "F3 the name in a filter is not filterable", target: "/?columns=id&filter=squadronName:eq:Red&limit=2", wantErr: "'squadronName' is not filterable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.wantErr != "" {
				_, err := decodeFlagged(t, tt.target, &nameGrantPermissions{})
				if err == nil || !httpio.HasBadRequest(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Decode(%s) error = %v, want a bad request saying %q", tt.target, err, tt.wantErr)
				}

				return
			}
			for dbType, lookup := range lookups {
				qSet, err := decodeFlagged(t, tt.target, &nameGrantPermissions{})
				if err != nil {
					t.Fatalf("Decode(%s) error = %v", tt.target, err)
				}
				if err := qSet.checkPermissions(t.Context(), dbType); err != nil {
					t.Fatalf("checkPermissions(%s) error = %v", dbType, err)
				}
				stmt, err := qSet.stmt(dbType)
				if err != nil {
					t.Fatalf("stmt(%s) error = %v", dbType, err)
				}
				if sql := oneLine(stmt.SQL); !strings.Contains(sql, "ORDER BY "+lookup+" ASC") {
					t.Errorf("stmt(%s) SQL =\n%s\nwant its order to open with the name's lookup:\n%s", dbType, sql, lookup)
				}
				if diff := cmp.Diff([]accesstypes.Field{"ID"}, qSet.Fields()); diff != "" {
					t.Errorf("Fields() on %s mismatch (-want +got):\n%s", dbType, diff)
				}
				if names := qSet.Names(); len(names) != 0 {
					t.Errorf("Names() on %s = %v, want none: the name's id is hidden", dbType, names)
				}
			}
		})
	}

	t.Run("F4 no columns named and a sort of the request's own: the name's grant is not asked", func(t *testing.T) {
		t.Parallel()

		for dbType := range lookups {
			perms := &nameGrantPermissions{}
			qSet, err := decodeFlagged(t, "/?sort=id&limit=2", perms)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if err := qSet.checkPermissions(t.Context(), dbType); err != nil {
				t.Fatalf("checkPermissions(%s) error = %v", dbType, err)
			}
			if calls := perms.askedFor("Squadrons.name"); len(calls) != 0 {
				t.Errorf("checkPermissions(%s) asked %v, want no Check naming Squadrons.name: the name's id is hidden and the order does not name it", dbType, calls)
			}
		}
	})
}

// gatedNamePass is a resource whose rows point to a squadron by an id no flag hides,
// with the squadron's name beside it, read from a resource behind the flag
// squadrons and a name field behind the flag squadron_names.
type gatedNamePass struct {
	ID         string  `spanner:"Id"         postgres:"Id"`
	SquadronID *string `spanner:"SquadronId" postgres:"SquadronId"`
}

// Resource names the resource in grants and routes.
func (gatedNamePass) Resource() accesstypes.Resource {
	return "GatedNamePasses"
}

// NameFields describes the gated name as the generator writes it.
func (gatedNamePass) NameFields() []NameField {
	return []NameField{
		{
			Field: "SquadronName", Key: "SquadronID", Resource: "Squadrons", Tag: "name", Table: "Squadrons", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "SectorId",
			Features: []Feature{"squadrons", "squadron_names"},
		},
	}
}

// gatedNamePassRequest is gatedNamePass's list request type.
type gatedNamePassRequest struct {
	ID           string  `json:"id"           index:"true" perm:"-"`
	SquadronID   *string `json:"squadronId"   index:"true"`
	SquadronName *string `json:"squadronName" index:"true" nameof:"SquadronID"`
}

// decodeGatedName decodes a list request for sector-1's gated-name passes, which the
// declared order sorts by the squadron's name, under the flags features holds and
// the grants perms answers.
func decodeGatedName(t *testing.T, target string, features *FeatureSet, perms *nameGrantPermissions) (*QuerySet[gatedNamePass], error) {
	t.Helper()

	resSet, err := NewSet[gatedNamePass, gatedNamePassRequest](accesstypes.List)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewQueryDecoder[gatedNamePass, gatedNamePassRequest](resSet)
	if err != nil {
		t.Fatalf("NewQueryDecoder() error = %v", err)
	}
	key, err := NewCursorKey("dGVzdC1jdXJzb3Ita2V5LW1hdGVyaWFsLTMyLWJ5dGVzIQ==")
	if err != nil {
		t.Fatalf("NewCursorKey() error = %v", err)
	}
	decoder = decoder.WithCursorKey(key).WithPaging(Paging{Order: []SortField{{Field: "SquadronName", Direction: SortAscending}}}).WithFeatures(features)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)

	return decoder.Decode(req, perms, inSector1)
}

// TestNameLookup_sourceBehindFlag pins a name whose pointed-to resource, or whose
// name field there, stands behind a flag that is off: it is unknown to a request,
// as a name beside a hidden id is, while a declared order by it still orders by
// its lookup. With every flag on it is a name as any other.
func TestNameLookup_sourceBehindFlag(t *testing.T) {
	t.Parallel()

	lookups := bothDatabases(
		"(SELECT `ca1`.`Name` FROM `Squadrons` `ca1` WHERE `ca1`.`Id` = `GatedNamePasses`.`SquadronId` AND `ca1`.`SectorId` = @domain)",
		`(SELECT "ca1"."Name" FROM "Squadrons" "ca1" WHERE "ca1"."Id" = "GatedNamePasses"."SquadronId" AND "ca1"."SectorId" = @domain)`,
	)
	squadrons := NameSource{Resource: "Squadrons", Field: "name", Domain: "sector-1"}
	offs := map[string]*FeatureSet{
		"the resource's flag off":   featureSetOf(map[Feature]bool{"squadrons": false, "squadron_names": true}),
		"the name field's flag off": featureSetOf(map[Feature]bool{"squadrons": true, "squadron_names": false}),
		"no flags wired":            nil,
	}
	on := featureSetOf(map[Feature]bool{"squadrons": true, "squadron_names": true})

	for name, features := range offs {
		t.Run(name+": the name is unknown to columns, sort and filter", func(t *testing.T) {
			t.Parallel()

			for target, want := range map[string]string{
				"/?columns=id,squadronName&limit=2":               "unknown column: squadronName",
				"/?columns=id&sort=squadronName&limit=2":          "unknown sort field: squadronName",
				"/?columns=id&filter=squadronName:eq:Red&limit=2": "'squadronName' is not filterable",
			} {
				_, err := decodeGatedName(t, target, features, &nameGrantPermissions{})
				if err == nil || !httpio.HasBadRequest(err) || !strings.Contains(err.Error(), want) {
					t.Errorf("Decode(%s) error = %v, want a bad request saying %q", target, err, want)
				}
			}
		})

		t.Run(name+": no columns named: the id and not the name, ordered by the name's lookup", func(t *testing.T) {
			t.Parallel()

			for dbType, lookup := range lookups {
				qSet, err := decodeGatedName(t, "/?limit=2", features, &nameGrantPermissions{})
				if err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				if err := qSet.checkPermissions(t.Context(), dbType); err != nil {
					t.Fatalf("checkPermissions(%s) error = %v", dbType, err)
				}
				stmt, err := qSet.stmt(dbType)
				if err != nil {
					t.Fatalf("stmt(%s) error = %v", dbType, err)
				}
				if sql := oneLine(stmt.SQL); !strings.Contains(sql, "ORDER BY "+lookup+" ASC") {
					t.Errorf("stmt(%s) SQL =\n%s\nwant its order to open with the name's lookup:\n%s", dbType, sql, lookup)
				}
				if diff := cmp.Diff([]accesstypes.Field{"ID", "SquadronID"}, qSet.Fields()); diff != "" {
					t.Errorf("Fields() on %s mismatch (-want +got):\n%s", dbType, diff)
				}
				if names := qSet.Names(); len(names) != 0 {
					t.Errorf("Names() on %s = %v, want none: a flag hides the name's source", dbType, names)
				}
			}
		})

		t.Run(name+": the name's list is not answered", func(t *testing.T) {
			t.Parallel()

			qSet, err := decodeGatedName(t, "/?sort=id&limit=2", features, &nameGrantPermissions{})
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			got, err := qSet.NameSources(t.Context())
			if err != nil {
				t.Fatalf("NameSources() error = %v", err)
			}
			if len(got) != 0 {
				t.Errorf("NameSources() = %v, want none: a flag hides the name's source", got)
			}
		})
	}

	t.Run("every flag on: the name is asked for, sorted by, filtered by and given", func(t *testing.T) {
		t.Parallel()

		for _, target := range []string{
			"/?columns=id,squadronName&limit=2",
			"/?columns=id&sort=squadronName&limit=2",
			"/?columns=id&filter=squadronName:eq:Red&limit=2",
		} {
			if _, err := decodeGatedName(t, target, on, &nameGrantPermissions{}); err != nil {
				t.Errorf("Decode(%s) error = %v, want the name taken", target, err)
			}
		}
		qSet, err := decodeGatedName(t, "/?sort=id&limit=2", on, &nameGrantPermissions{})
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if err := qSet.checkPermissions(t.Context(), SpannerDBType); err != nil {
			t.Fatalf("checkPermissions() error = %v", err)
		}
		if diff := cmp.Diff([]RequestedName{{Field: "SquadronName", JSONName: "squadronName"}}, qSet.Names()); diff != "" {
			t.Errorf("Names() mismatch (-want +got):\n%s", diff)
		}
		got, err := qSet.NameSources(t.Context())
		if err != nil {
			t.Fatalf("NameSources() error = %v", err)
		}
		if diff := cmp.Diff([]NameSource{squadrons}, got); diff != "" {
			t.Errorf("NameSources() mismatch (-want +got):\n%s", diff)
		}
	})
}

// sourcedPass is a tenant's resource whose rows point to two squadrons, a home one
// and a visiting one, whose names are read from one list; to a level, an
// enumeration table; to a maker, a resource of no tenant; and to a bay, a tenant's
// resource known by its key.
type sourcedPass struct {
	ID              string  `spanner:"Id"              postgres:"Id"`
	SectorID        string  `spanner:"SectorId"        postgres:"SectorId"`
	HomeSquadronID  *string `spanner:"HomeSquadronId"  postgres:"HomeSquadronId"`
	VisitSquadronID *string `spanner:"VisitSquadronId" postgres:"VisitSquadronId"`
	LevelID         string  `spanner:"LevelId"         postgres:"LevelId"`
	MakerID         *string `spanner:"MakerId"         postgres:"MakerId"`
	BayID           *string `spanner:"BayId"           postgres:"BayId"`
}

// Resource names the resource in grants and routes.
func (sourcedPass) Resource() accesstypes.Resource {
	return "DockPasses"
}

// DefaultConfig is the resource's configuration.
func (sourcedPass) DefaultConfig() Config {
	return Config{}
}

// NameFields describes the resource's name fields as the generator writes them.
func (sourcedPass) NameFields() []NameField {
	return []NameField{
		{Field: "HomeSquadronName", Key: "HomeSquadronID", Resource: "Squadrons", Tag: "name", Table: "Squadrons", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "SectorId"},
		{Field: "VisitSquadronName", Key: "VisitSquadronID", Resource: "Squadrons", Tag: "name", Table: "Squadrons", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "SectorId"},
		{Field: "LevelName", Key: "LevelID", Table: "PassLevels", KeyColumn: "Id", NameColumn: "Description"},
		{Field: "MakerName", Key: "MakerID", Resource: "Makers", Tag: "name", Table: "Makers", KeyColumn: "Id", NameColumn: "Name"},
		{Field: "BayName", Key: "BayID", Resource: "Bays", Table: "Bays", KeyColumn: "Code", NameColumn: "Code", TenantColumn: "SectorId"},
	}
}

// sourcedPassRequest is sourcedPass's list request type as the generator writes it.
type sourcedPassRequest struct {
	ID                string  `json:"id"                index:"true" perm:"-"`
	SectorID          string  `json:"sectorId"          index:"true"`
	HomeSquadronID    *string `json:"homeSquadronId"    index:"true"`
	VisitSquadronID   *string `json:"visitSquadronId"   index:"true"`
	LevelID           string  `json:"levelId"           index:"true"`
	MakerID           *string `json:"makerId"           index:"true"`
	BayID             *string `json:"bayId"             index:"true"`
	HomeSquadronName  *string `json:"homeSquadronName"  index:"true" nameof:"HomeSquadronID"`
	VisitSquadronName *string `json:"visitSquadronName" index:"true" nameof:"VisitSquadronID"`
	LevelName         *string `json:"levelName"         index:"true" nameof:"LevelID"`
	MakerName         *string `json:"makerName"         index:"true" nameof:"MakerID"`
	BayName           *string `json:"bayName"           index:"true" nameof:"BayID"`
}

// decodeSources decodes a list request in sector-1 under the stub and runs none of
// the read's checks, as a live handler has it when it subscribes.
func decodeSources[Resource Resourcer, Request any](t *testing.T, target string, perms UserPermissions) *QuerySet[Resource] {
	t.Helper()

	resSet, err := NewSet[Resource, Request](accesstypes.List)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewQueryDecoder[Resource, Request](resSet)
	if err != nil {
		t.Fatalf("NewQueryDecoder() error = %v", err)
	}
	decoder.collection = lookupCollection(t)
	key, err := NewCursorKey("dGVzdC1jdXJzb3Ita2V5LW1hdGVyaWFsLTMyLWJ5dGVzIQ==")
	if err != nil {
		t.Fatalf("NewCursorKey() error = %v", err)
	}
	decoder = decoder.WithCursorKey(key)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	qSet, err := decoder.Decode(req, perms, inSector1)
	if err != nil {
		t.Fatalf("QueryDecoder.Decode(%s) error = %v", target, err)
	}

	return qSet
}

// TestNameSources pins the lists a query's names are read from, which a live page
// that shows them subscribes to: each name in play that points to a resource and
// whose grant the person holds, outright or under a condition, answers that
// resource's list in the tenant its names are read in, with the name field as the
// grants name it; each list once, in the order the request type declares the names.
func TestNameSources(t *testing.T) {
	t.Parallel()

	squadrons := NameSource{Resource: "Squadrons", Field: "name", Domain: "sector-1"}
	crews := NameSource{Resource: "Crews", Field: "name", Domain: "sector-1"}
	tests := []struct {
		name   string
		decode func(*testing.T) *QuerySet[lookupPass]
		want   []NameSource
	}{
		{
			name: "a column asking for a tenant's name: its list in the request's tenant, with the field",
			decode: func(t *testing.T) *QuerySet[lookupPass] {
				return decodeSources[lookupPass, lookupPassRequest](t, "/?columns=id,squadronName&sort=id&limit=2", &nameGrantPermissions{})
			},
			want: []NameSource{squadrons},
		},
		{
			name: "a sort by a name no column shows",
			decode: func(t *testing.T) *QuerySet[lookupPass] {
				return decodeSources[lookupPass, lookupPassRequest](t, "/?columns=id&sort=squadronName&limit=2", &nameGrantPermissions{})
			},
			want: []NameSource{squadrons},
		},
		{
			name: "a filter by a name no column shows",
			decode: func(t *testing.T) *QuerySet[lookupPass] {
				return decodeSources[lookupPass, lookupPassRequest](t, "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2", &nameGrantPermissions{})
			},
			want: []NameSource{squadrons},
		},
		{
			name: "an enumeration table's name: nothing",
			decode: func(t *testing.T) *QuerySet[lookupPass] {
				return decodeSources[lookupPass, lookupPassRequest](t, "/?columns=id,levelName&sort=id&limit=2", &nameGrantPermissions{})
			},
		},
		{
			name: "a denied name: nothing; the name beside it still answers",
			decode: func(t *testing.T) *QuerySet[lookupPass] {
				return decodeSources[lookupPass, lookupPassRequest](t, "/?columns=id,squadronName,crewName&sort=id&limit=2", deniedInSector1(squadronNameGrant))
			},
			want: []NameSource{crews},
		},
		{
			name: "a name held under a condition: its list",
			decode: func(t *testing.T) *QuerySet[lookupPass] {
				perms := &nameGrantPermissions{decisions: map[grantByScope]accesstypes.Decision{
					{scope: inSector1, perm: accesstypes.List, res: squadronNameGrant}: conditionalOn(squadronNameGrant, "rank = 1"),
				}}

				return decodeSources[lookupPass, lookupPassRequest](t, "/?columns=id,squadronName&sort=id&limit=2", perms)
			},
			want: []NameSource{squadrons},
		},
		{
			name: "a set that enforces no grants: every name's list",
			decode: func(t *testing.T) *QuerySet[lookupPass] {
				return lookupQuerySet(t, Paging{}, "/?columns=id,crewName,squadronName,levelName&sort=id&limit=2")
			},
			want: []NameSource{squadrons, crews},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.decode(t).NameSources(t.Context())
			if err != nil {
				t.Fatalf("NameSources() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("NameSources() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	others := []struct {
		name   string
		target string
		want   []NameSource
	}{
		{
			name:   "no columns: every granted name's list, two names of one list once, in the order the request type declares them",
			target: "/?sort=id&limit=2",
			want: []NameSource{
				{Resource: "Squadrons", Field: "name", Domain: "sector-1"},
				{Resource: "Makers", Field: "name"},
				{Resource: "Bays", Domain: "sector-1"},
			},
		},
		{
			name:   "a name of a resource of no tenant: its list under no tenant",
			target: "/?columns=id,makerName&sort=id&limit=2",
			want:   []NameSource{{Resource: "Makers", Field: "name"}},
		},
		{
			name:   "a name that is the pointed-to resource's key: its list with no field",
			target: "/?columns=id,bayName&sort=id&limit=2",
			want:   []NameSource{{Resource: "Bays", Domain: "sector-1"}},
		},
	}
	for _, tt := range others {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeSources[sourcedPass, sourcedPassRequest](t, tt.target, &nameGrantPermissions{}).NameSources(t.Context())
			if err != nil {
				t.Fatalf("NameSources() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("NameSources() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("asked before the read and after it: the same answer, the name's grant asked once", func(t *testing.T) {
		t.Parallel()

		perms := &nameGrantPermissions{}
		qSet := decodeSources[lookupPass, lookupPassRequest](t, "/?columns=id,squadronName&sort=id&limit=2", perms)
		before, err := qSet.NameSources(t.Context())
		if err != nil {
			t.Fatalf("NameSources() before the read error = %v", err)
		}
		if err := qSet.checkPermissions(t.Context(), SpannerDBType); err != nil {
			t.Fatalf("checkPermissions() error = %v", err)
		}
		after, err := qSet.NameSources(t.Context())
		if err != nil {
			t.Fatalf("NameSources() after the read error = %v", err)
		}
		if diff := cmp.Diff([]NameSource{squadrons}, before); diff != "" {
			t.Errorf("NameSources() before the read mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(before, after); diff != "" {
			t.Errorf("NameSources() after the read mismatch (-before +after):\n%s", diff)
		}
		if calls := perms.askedFor(squadronNameGrant); len(calls) != 1 {
			t.Errorf("calls naming %s = %+v, want one: the read asks nothing again", squadronNameGrant, calls)
		}
	})
}
