package resource

// These tests pin a name read from a view: the pointed-to resource is a query of
// the application's, so the statement reads the view's query, with its
// parameters, where it reads a table's name for a name read from a table.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// squadronLabelsQuery is the query of a view of the names fixture's squadrons, each
// known by its label, the squadron's name, less the label its parameter names.
const squadronLabelsQuery = "SELECT Id, SectorId, Name AS Label FROM Squadrons WHERE Name != @leftOutLabel"

// squadronLabelsView is the view of squadron labels, its parameter leaving the
// green squadron out.
type squadronLabelsView struct{}

// Subquery is the view's query.
func (squadronLabelsView) Subquery() (query string, params map[string]any) {
	return squadronLabelsQuery, map[string]any{"leftOutLabel": "Green"}
}

// withLabelsView is a view of squadron labels whose query opens with a WITH clause.
type withLabelsView struct{}

// Subquery is the view's query.
func (withLabelsView) Subquery() (query string, params map[string]any) {
	return "WITH Named AS (SELECT Id, SectorId, Name FROM Squadrons) SELECT Id, SectorId, Name AS Label FROM Named", nil
}

// underscoreLabelsView is a view of squadron labels whose parameter's name starts
// with an underscore.
type underscoreLabelsView struct{}

// Subquery is the view's query.
func (underscoreLabelsView) Subquery() (query string, params map[string]any) {
	return "SELECT Id, SectorId, Name AS Label FROM Squadrons WHERE Name != @_leftOut", map[string]any{"_leftOut": "Green"}
}

// unreadableLabelsView is a view of squadron labels that binds a parameter and whose
// query the lexer cannot read to its end: it never closes a string literal.
type unreadableLabelsView struct{}

// Subquery is the view's query.
func (unreadableLabelsView) Subquery() (query string, params map[string]any) {
	return "SELECT Id, SectorId, Name AS Label FROM Squadrons WHERE Name != @leftOutLabel AND Name != 'Gray", map[string]any{"leftOutLabel": "Green"}
}

// squadronTagsView is a second view of the squadrons, each known by its tag, which
// binds a parameter of the labels view's name to another value: it leaves the red
// squadron out.
type squadronTagsView struct{}

// Subquery is the view's query.
func (squadronTagsView) Subquery() (query string, params map[string]any) {
	return "SELECT Id, SectorId, Name AS Tag FROM Squadrons WHERE Name != @leftOutLabel", map[string]any{"leftOutLabel": "Red"}
}

// labelViews hands the squadron labels' view to a decoder.
var labelViews = NameViews{"SquadronLabels": squadronLabelsView{}}

// squadronLabelGrant is the grant on the view's name field.
const squadronLabelGrant = accesstypes.Resource("SquadronLabels.label")

// labelNameFields are the name fields of a list of the fixture's passes whose
// squadron is known by its label in the view.
func labelNameFields() []NameField {
	return []NameField{
		{Field: "SquadronName", Key: "SquadronID", Resource: "SquadronLabels", Tag: "label", Table: "SquadronLabels", KeyColumn: "Id", NameColumn: "Label", View: true, TenantColumn: "SectorId"},
	}
}

// labelPass is the fixture's hangar pass, whose squadron's name is read from the
// view of squadron labels.
type labelPass struct {
	ID         string  `spanner:"Id"         postgres:"Id"`
	SectorID   string  `spanner:"SectorId"   postgres:"SectorId"`
	SquadronID *string `spanner:"SquadronId" postgres:"SquadronId"`
}

// Resource names the resource in grants and routes.
func (labelPass) Resource() accesstypes.Resource {
	return "HangarPasses"
}

// DefaultConfig is the resource's configuration.
func (labelPass) DefaultConfig() Config {
	return Config{}
}

// NameFields describes the resource's name fields as the generator writes them.
func (labelPass) NameFields() []NameField {
	return labelNameFields()
}

// labelPassRequest is labelPass's list request type.
type labelPassRequest struct {
	ID           string  `json:"id"           index:"true" perm:"-"`
	SectorID     string  `json:"sectorId"     index:"true"`
	SquadronID   *string `json:"squadronId"   index:"true"`
	SquadronName *string `json:"squadronName" index:"true" nameof:"SquadronID"`
}

// labelPassView is a view of the fixture's passes with labelPass's names, whose own
// query binds a parameter the squadron labels' view binds too.
type labelPassView struct {
	ID         string  `spanner:"Id"         postgres:"Id"`
	SectorID   string  `spanner:"SectorId"   postgres:"SectorId"`
	SquadronID *string `spanner:"SquadronId" postgres:"SquadronId"`
}

// Resource names the resource in grants and routes.
func (labelPassView) Resource() accesstypes.Resource {
	return "PassViews"
}

// DefaultConfig is the resource's configuration.
func (labelPassView) DefaultConfig() Config {
	return Config{}
}

// NameFields describes the view's name fields: labelPass's.
func (labelPassView) NameFields() []NameField {
	return labelNameFields()
}

// Subquery is the view's query.
func (labelPassView) Subquery() (query string, params map[string]any) {
	return "SELECT Id, SectorId, SquadronId FROM HangarPasses WHERE SectorId != @leftOutLabel", map[string]any{"leftOutLabel": "sector-9"}
}

// labelCollection binds the passes and the squadron labels to their sector by its
// column, and declares a label, which a condition on the grant of the name reads, and
// a pass's squadron, which a condition on the grant of the squadron's id reads.
func labelCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		{
			Name:        "HangarPasses",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.List},
			Attributes:  []AttributeData{{Name: "squadron", Column: "SquadronId", Type: AttributeTypeString}},
			Domain:      &DomainBindingData{Column: "SectorId"},
		},
		{
			Name:        "PassViews",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.List},
			Domain:      &DomainBindingData{Column: "SectorId"},
		},
		{
			Name:        "SquadronLabels",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.List},
			Attributes:  []AttributeData{{Name: "label", Column: "Label", Type: AttributeTypeString}},
			Domain:      &DomainBindingData{Column: "SectorId"},
		},
	}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// newLabelDecoder is the list decoder of Resource over the request type, handed the
// views where views is not nil.
func newLabelDecoder[Resource Resourcer, Request any](t *testing.T, views NameViews) *QueryDecoder[Resource, Request] {
	t.Helper()

	resSet, err := NewSet[Resource, Request](accesstypes.List)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewQueryDecoder[Resource, Request](resSet)
	if err != nil {
		t.Fatalf("NewQueryDecoder() error = %v", err)
	}
	decoder.collection = labelCollection(t)
	key, err := NewCursorKey("dGVzdC1jdXJzb3Ita2V5LW1hdGVyaWFsLTMyLWJ5dGVzIQ==")
	if err != nil {
		t.Fatalf("NewCursorKey() error = %v", err)
	}
	decoder = decoder.WithCursorKey(key).WithPaging(Paging{})
	if views != nil {
		decoder = decoder.WithNameViews(views)
	}

	return decoder
}

// decodeLabels decodes a list request of sector-1's passes through the decoder under
// the stub, and runs its read checks for the database.
func decodeLabels[Resource Resourcer, Request any](t *testing.T, decoder *QueryDecoder[Resource, Request], target string, perms UserPermissions, dbType DBType) *QuerySet[Resource] {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	qSet, err := decoder.Decode(req, perms, inSector1)
	if err != nil {
		t.Fatalf("QueryDecoder.Decode(%s) error = %v", target, err)
	}
	if err := qSet.checkPermissions(t.Context(), dbType); err != nil {
		t.Fatalf("checkPermissions(%s, %s) error = %v", dbType, target, err)
	}

	return qSet
}

// labelStatement renders the statement of a list request of sector-1's passes whose
// squadron's name is read from the view.
func labelStatement(t *testing.T, target string, perms UserPermissions, dbType DBType) *Statement {
	t.Helper()

	qSet := decodeLabels(t, newLabelDecoder[labelPass, labelPassRequest](t, labelViews), target, perms, dbType)
	stmt, err := qSet.stmt(dbType)
	if err != nil {
		t.Fatalf("stmt(%s, %s) error = %v", dbType, target, err)
	}

	return stmt
}

// labelsFrom is the view's query as a statement reads it under the alias, folded to
// one line, for the database: its parameter written as the statement's first
// placeholder of its own, @_c1.
func labelsFrom(dbType DBType, alias string) string {
	query := strings.Replace(squadronLabelsQuery, "@leftOutLabel", "@_c1", 1)
	if dbType == PostgresDBType {
		return fmt.Sprintf("(%s ) AS %q", query, alias)
	}

	return fmt.Sprintf("(%s ) AS %#q", query, alias)
}

// nameReference is what a statement writes for the squadron's name where the name is an
// item of the statement's row of names.
func nameReference(dbType DBType) string {
	if dbType == PostgresDBType {
		return `"zzNames"."zzNameSquadronName"`
	}

	return "zzNames.zzNameSquadronName"
}

// labelRowItem is the squadron's name lookup as an item of the row of names.
func labelRowItem(dbType DBType, lookup string) string {
	if dbType == PostgresDBType {
		return lookup + ` AS "zzNameSquadronName"`
	}

	return lookup + " AS zzNameSquadronName"
}

// labelRowFrom is the row of names the FROM carries after the list's own FROM item,
// holding the squadron's name lookup alone.
func labelRowFrom(dbType DBType, lookup string) string {
	if dbType == PostgresDBType {
		return ` CROSS JOIN LATERAL (SELECT ` + labelRowItem(dbType, lookup) + `) AS "zzNames"`
	}

	return ", UNNEST(ARRAY(SELECT AS STRUCT " + labelRowItem(dbType, lookup) + ")) AS zzNames"
}

// labelIDColumn is the passes' id as the select list writes it.
func labelIDColumn(dbType DBType) string {
	if dbType == PostgresDBType {
		return `"Id"`
	}

	return "Id"
}

// TestNameView_statement pins what a statement writes for a name read from a view:
// the lookup and the filter read the view's query under their own alias where a
// table's name would be, held to the request's tenant by the view's own column, and
// the view's parameter is written in that query as a placeholder of the statement's
// own, never under its own name, one placeholder where the name is both shown and
// filtered by.
func TestNameView_statement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		want   map[DBType]string
		reads  int
	}{
		{
			name:   "shown",
			target: "/?columns=id,squadronName&sort=id&limit=2",
			reads:  1,
			want: bothDatabases(
				"(SELECT `ca1`.`Label` FROM "+labelsFrom(SpannerDBType, "ca1")+" WHERE `ca1`.`Id` = `HangarPasses`.`SquadronId` AND `ca1`.`SectorId` = @domain) AS zzNameSquadronName",
				`(SELECT "ca1"."Label" FROM `+labelsFrom(PostgresDBType, "ca1")+` WHERE "ca1"."Id" = "HangarPasses"."SquadronId" AND "ca1"."SectorId" = @domain) AS "zzNameSquadronName"`,
			),
		},
		{
			name:   "filtered by eq",
			reads:  1,
			target: "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2",
			want: bothDatabases(
				"WHERE `SquadronId` IN (SELECT `ca1`.`Id` FROM "+labelsFrom(SpannerDBType, "ca1")+" WHERE `ca1`.`SectorId` = @domain AND `Label` = @_p1)",
				`WHERE "SquadronId" IN (SELECT "ca1"."Id" FROM `+labelsFrom(PostgresDBType, "ca1")+` WHERE "ca1"."SectorId" = @domain AND "Label" = @_p1)`,
			),
		},
		{
			name:   "filtered by isnotnull",
			reads:  1,
			target: "/?columns=id&filter=squadronName:isnotnull&sort=id&limit=2",
			want: bothDatabases(
				"WHERE `SquadronId` IN (SELECT `ca1`.`Id` FROM "+labelsFrom(SpannerDBType, "ca1")+" WHERE `ca1`.`SectorId` = @domain AND `Label` IS NOT NULL)",
				`WHERE "SquadronId" IN (SELECT "ca1"."Id" FROM `+labelsFrom(PostgresDBType, "ca1")+` WHERE "ca1"."SectorId" = @domain AND "Label" IS NOT NULL)`,
			),
		},
		{
			name:   "filtered by isnull",
			reads:  1,
			target: "/?columns=id&filter=squadronName:isnull&sort=id&limit=2",
			want: bothDatabases(
				"WHERE (`SquadronId` IS NULL OR `SquadronId` NOT IN (SELECT `ca1`.`Id` FROM "+labelsFrom(SpannerDBType, "ca1")+" WHERE `ca1`.`SectorId` = @domain AND `Label` IS NOT NULL))",
				`WHERE ("SquadronId" IS NULL OR "SquadronId" NOT IN (SELECT "ca1"."Id" FROM `+labelsFrom(PostgresDBType, "ca1")+` WHERE "ca1"."SectorId" = @domain AND "Label" IS NOT NULL))`,
			),
		},
		{
			name:   "shown and filtered by: the lookup and the filter's key set name one placeholder",
			reads:  2,
			target: "/?columns=id,squadronName&filter=squadronName:eq:Red&sort=id&limit=2",
			want: bothDatabases(
				"WHERE `SquadronId` IN (SELECT `ca2`.`Id` FROM "+labelsFrom(SpannerDBType, "ca2")+" WHERE `ca2`.`SectorId` = @domain AND `Label` = @_p1)",
				`WHERE "SquadronId" IN (SELECT "ca2"."Id" FROM `+labelsFrom(PostgresDBType, "ca2")+` WHERE "ca2"."SectorId" = @domain AND "Label" = @_p1)`,
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for dbType, want := range tt.want {
				stmt := labelStatement(t, tt.target, &nameGrantPermissions{}, dbType)
				sql := oneLine(stmt.SQL)
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
				}
				if reads := strings.Count(sql, "Name != @_c1"); reads != tt.reads {
					t.Errorf("%s statement reads the view's query with its parameter as @_c1 %d times, want %d:\n%s", dbType, reads, tt.reads, sql)
				}
				if _, own := stmt.Params["leftOutLabel"]; own || stmt.Params["_c1"] != "Green" {
					t.Errorf("%s params = %v, want the view's leftOutLabel bound to Green as _c1, and nothing under its own name", dbType, stmt.Params)
				}
			}
		})
	}

	t.Run("a conditional name grant's condition follows the tenant test", func(t *testing.T) {
		t.Parallel()

		perms := &nameGrantPermissions{decisions: map[grantByScope]accesstypes.Decision{
			{scope: inSector1, perm: accesstypes.List, res: squadronLabelGrant}: conditionalOn(squadronLabelGrant, "label = 'Red'"),
		}}
		lookups := bothDatabases(
			"(SELECT `ca1`.`Label` FROM "+labelsFrom(SpannerDBType, "ca1")+" WHERE `ca1`.`Id` = `HangarPasses`.`SquadronId` AND `ca1`.`SectorId` = @domain AND (`ca1`.`Label` = @_c2))",
			`(SELECT "ca1"."Label" FROM `+labelsFrom(PostgresDBType, "ca1")+` WHERE "ca1"."Id" = "HangarPasses"."SquadronId" AND "ca1"."SectorId" = @domain AND ("ca1"."Label" = @_c2))`,
		)
		for dbType, lookup := range lookups {
			stmt := labelStatement(t, "/?columns=id,squadronName&sort=squadronName&limit=2", perms, dbType)
			sql := oneLine(stmt.SQL)
			for _, want := range []string{labelRowItem(dbType, lookup), "ORDER BY " + nameReference(dbType) + " ASC"} {
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
				}
			}
			if _, own := stmt.Params["leftOutLabel"]; own || stmt.Params["_c1"] != "Green" || stmt.Params["_c2"] != "Red" {
				t.Errorf("%s params = %v, want the view's leftOutLabel bound to Green as _c1 and the label Red as _c2", dbType, stmt.Params)
			}
		}
	})

	t.Run("sorted by the name: the view's query is read once, in the row of names the FROM carries", func(t *testing.T) {
		t.Parallel()

		lookups := bothDatabases(
			"(SELECT `ca1`.`Label` FROM "+labelsFrom(SpannerDBType, "ca1")+" WHERE `ca1`.`Id` = `HangarPasses`.`SquadronId` AND `ca1`.`SectorId` = @domain)",
			`(SELECT "ca1"."Label" FROM `+labelsFrom(PostgresDBType, "ca1")+` WHERE "ca1"."Id" = "HangarPasses"."SquadronId" AND "ca1"."SectorId" = @domain)`,
		)
		for dbType, lookup := range lookups {
			qSet := decodeLabels(t, newLabelDecoder[labelPass, labelPassRequest](t, labelViews), "/?columns=id,squadronName&sort=squadronName&limit=2", &nameGrantPermissions{}, dbType)
			ref := nameReference(dbType)
			item := ref + " AS zzNameSquadronName"
			if dbType == PostgresDBType {
				item = ref + ` AS "zzNameSquadronName"`
			}

			stmt, err := qSet.stmt(dbType)
			if err != nil {
				t.Fatalf("stmt(%s) error = %v", dbType, err)
			}
			sql := oneLine(stmt.SQL)
			for _, want := range []string{"FROM HangarPasses" + labelRowFrom(dbType, lookup) + " WHERE", "SELECT " + labelIDColumn(dbType) + ", " + item + " FROM", "ORDER BY " + ref + " ASC"} {
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
				}
			}
			if reads := strings.Count(sql, "Name != @_c1"); reads != 1 {
				t.Errorf("%s statement reads the view's query %d times, want once:\n%s", dbType, reads, sql)
			}

			// The next page's predicate compares the same item with the boundary row's name.
			red := "Red"
			row := &Row[labelPass]{Data: labelPass{ID: "p2"}, cursorValues: map[accesstypes.Field]reflect.Value{"SquadronName": reflect.ValueOf(&red)}}
			keys, err := qSet.boundaryKeys(row)
			if err != nil {
				t.Fatalf("boundaryKeys() error = %v", err)
			}
			qSet.cursor = &cursor{Direction: pageNext, Keys: keys}
			stmt, err = qSet.stmt(dbType)
			if err != nil {
				t.Fatalf("stmt(%s) of the next page error = %v", dbType, err)
			}
			sql = oneLine(stmt.SQL)
			where, _, _ := strings.Cut(sql, "ORDER BY")
			_, where, _ = strings.Cut(where, " WHERE ")
			if !strings.Contains(where, ref+" > @") || !strings.Contains(where, ref+" = @") {
				t.Errorf("%s next page's WHERE =\n%s\nwant the boundary compared with %s", dbType, where, ref)
			}
			if reads := strings.Count(sql, "Name != @_c1"); reads != 1 {
				t.Errorf("%s next page's statement reads the view's query %d times, want once:\n%s", dbType, reads, sql)
			}
		}
	})

	t.Run("sorted by the name, the squadron's id held under a condition: the condition wraps the item's reference", func(t *testing.T) {
		t.Parallel()

		perms := &nameGrantPermissions{decisions: map[grantByScope]accesstypes.Decision{
			{scope: inSector1, perm: accesstypes.List, res: "HangarPasses.squadronId"}: conditionalOn("HangarPasses.squadronId", "squadron = 'q-red'"),
		}}
		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			stmt := labelStatement(t, "/?columns=id,squadronName&sort=squadronName&limit=2", perms, dbType)
			sql := oneLine(stmt.SQL)
			held := regexp.MustCompile(`CASE WHEN \S+ = @_c\d THEN ` + regexp.QuoteMeta(nameReference(dbType)) + ` END`)
			if got := len(held.FindAllString(sql, -1)); got != 2 {
				t.Errorf("%s statement =\n%s\nholds %d CASEs over the reference, want two: the select item and the order", dbType, sql, got)
			}
			if !strings.Contains(sql, "ORDER BY CASE WHEN") {
				t.Errorf("%s statement =\n%s\nwant the order on the CASE over the reference", dbType, sql)
			}
			// The condition's value takes the first placeholder here, the view's parameter the second.
			if reads := strings.Count(sql, "Name != @_c"); reads != 1 {
				t.Errorf("%s statement reads the view's query %d times, want once:\n%s", dbType, reads, sql)
			}
		}
	})

	t.Run("the count statement carries the view's parameter", func(t *testing.T) {
		t.Parallel()

		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			qSet := decodeLabels(t, newLabelDecoder[labelPass, labelPassRequest](t, labelViews), "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2&count=true", &nameGrantPermissions{}, dbType)
			stmt, err := qSet.countStmt(dbType)
			if err != nil {
				t.Fatalf("countStmt(%s) error = %v", dbType, err)
			}
			want := fmt.Sprintf("FROM %s WHERE", labelsFrom(dbType, "ca1"))
			if sql := oneLine(stmt.SQL); !strings.Contains(sql, want) {
				t.Errorf("%s count statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
			}
			if _, own := stmt.Params["leftOutLabel"]; own || stmt.Params["_c1"] != "Green" {
				t.Errorf("%s count params = %v, want the view's leftOutLabel bound to Green as _c1, and nothing under its own name", dbType, stmt.Params)
			}
		}
	})
}

// TestNameView_decoder pins what a decoder refuses of a name read from a view: a
// view it is not handed, where it is handed views and where it never is; a view
// whose query opens with WITH; a view that binds a parameter and whose query cannot
// be read to its end; and a parameter of the view's that starts with an underscore.
// A parameter of the list's own view's that the name's view binds too is no refusal:
// each view keeps its own value.
func TestNameView_decoder(t *testing.T) {
	t.Parallel()

	const noView = "name field SquadronName of HangarPasses is read from the view SquadronLabels, and the decoder holds no view of that name: hand the application's views to the decoder (WithNameViews), which the generated handlers do; regenerate them"
	panics := []struct {
		name  string
		views NameViews
		want  string
	}{
		{
			name:  "handed no view of the name's",
			views: NameViews{"OtherLabels": squadronLabelsView{}},
			want:  noView,
		},
		{
			name:  "handed a view whose query opens with WITH",
			views: NameViews{"SquadronLabels": withLabelsView{}},
			want:  "name field SquadronName of HangarPasses is read from the view SquadronLabels, whose query opens with a WITH clause: the name is read inside the statement of the list that shows it, where a WITH clause cannot go. Write the view's query without WITH, each WITH table as a subquery in its FROM.",
		},
		{
			name:  "handed a view that binds a parameter and whose query cannot be read to its end",
			views: NameViews{"SquadronLabels": unreadableLabelsView{}},
			want:  "name field SquadronName of HangarPasses is read from the view SquadronLabels, and the view's query cannot be read to its end (syntax error: :1:91: unclosed string literal): the statement that reads the name writes each parameter of the view under a name of its own, and so reads the whole query. Correct the query at that place.",
		},
	}
	for _, tt := range panics {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decoder := newLabelDecoder[labelPass, labelPassRequest](t, nil)
			defer func() {
				if got := fmt.Sprint(recover()); got != tt.want {
					t.Errorf("WithNameViews() panicked with %q, want %q", got, tt.want)
				}
			}()
			decoder.WithNameViews(tt.views)
		})
	}

	t.Run("a decoder whose names are read from tables accepts no views", func(t *testing.T) {
		t.Parallel()

		resSet, err := NewSet[lookupPass, lookupPassRequest](accesstypes.List)
		if err != nil {
			t.Fatalf("NewSet() error = %v", err)
		}
		decoder, err := NewQueryDecoder[lookupPass, lookupPassRequest](resSet)
		if err != nil {
			t.Fatalf("NewQueryDecoder() error = %v", err)
		}
		decoder.WithNameViews(nil)
	})

	refusals := []struct {
		name  string
		build func(t *testing.T) (*Statement, error)
		want  string
	}{
		{
			name: "a decoder never handed views answers at the read",
			build: func(t *testing.T) (*Statement, error) {
				return decodeLabels(t, newLabelDecoder[labelPass, labelPassRequest](t, nil), "/?columns=id,squadronName&sort=id&limit=2", &nameGrantPermissions{}, SpannerDBType).stmt(SpannerDBType)
			},
			want: noView,
		},
		{
			name: "a view's parameter that starts with an underscore",
			build: func(t *testing.T) (*Statement, error) {
				decoder := newLabelDecoder[labelPass, labelPassRequest](t, NameViews{"SquadronLabels": underscoreLabelsView{}})

				return decodeLabels(t, decoder, "/?columns=id,squadronName&sort=id&limit=2", &nameGrantPermissions{}, SpannerDBType).stmt(SpannerDBType)
			},
			want: "Subquery params for SquadronLabels can not start with an _",
		},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := tt.build(t)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("stmt() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// TestNameView_ownViewSharesAParameterName pins that a list of a view whose own query
// binds a parameter of the name the name's view binds too reads each: the list's
// under its own name, with the list's value, and the name's view's under a
// placeholder of the statement's own, with the view's.
func TestNameView_ownViewSharesAParameterName(t *testing.T) {
	t.Parallel()

	for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
		decoder := newLabelDecoder[labelPassView, labelPassRequest](t, labelViews)
		stmt, err := decodeLabels(t, decoder, "/?columns=id,squadronName&sort=id&limit=2", &nameGrantPermissions{}, dbType).stmt(dbType)
		if err != nil {
			t.Fatalf("stmt(%s) error = %v", dbType, err)
		}
		if stmt.Params["leftOutLabel"] != "sector-9" || stmt.Params["_c1"] != "Green" {
			t.Errorf("%s params = %v, want the list's leftOutLabel bound to sector-9 and the name's view's to Green as _c1", dbType, stmt.Params)
		}
		if sql := oneLine(stmt.SQL); !strings.Contains(sql, labelsFrom(dbType, "ca1")) {
			t.Errorf("%s statement =\n%s\nwant the name's view read with its parameter as @_c1:\n%s", dbType, sql, labelsFrom(dbType, "ca1"))
		}
	}
}

// escortPass is the fixture's pass with a second squadron, its escort, whose name is
// read from the view of squadron tags.
type escortPass struct {
	ID         string  `spanner:"Id"         postgres:"Id"`
	SectorID   string  `spanner:"SectorId"   postgres:"SectorId"`
	SquadronID *string `spanner:"SquadronId" postgres:"SquadronId"`
	EscortID   *string `spanner:"EscortId"   postgres:"EscortId"`
}

// Resource names the resource in grants and routes.
func (escortPass) Resource() accesstypes.Resource {
	return "HangarPasses"
}

// DefaultConfig is the resource's configuration.
func (escortPass) DefaultConfig() Config {
	return Config{}
}

// NameFields describes the resource's name fields as the generator writes them: the
// squadron's from the labels view, the escort's from the tags view.
func (escortPass) NameFields() []NameField {
	return append(labelNameFields(), NameField{Field: "EscortName", Key: "EscortID", Resource: "SquadronTags", Tag: "tag", Table: "SquadronTags", KeyColumn: "Id", NameColumn: "Tag", View: true, TenantColumn: "SectorId"})
}

// escortPassRequest is escortPass's list request type.
type escortPassRequest struct {
	ID           string  `json:"id"           index:"true" perm:"-"`
	SectorID     string  `json:"sectorId"     index:"true"`
	SquadronID   *string `json:"squadronId"   index:"true"`
	SquadronName *string `json:"squadronName" index:"true" nameof:"SquadronID"`
	EscortID     *string `json:"escortId"     index:"true"`
	EscortName   *string `json:"escortName"   index:"true" nameof:"EscortID"`
}

// TestNameView_twoViewsBindOneParameterName pins that a list whose names are read
// from two views that bind a parameter of one name to two values writes each view's
// query with a placeholder of its own, bound to that view's value.
func TestNameView_twoViewsBindOneParameterName(t *testing.T) {
	t.Parallel()

	views := NameViews{"SquadronLabels": squadronLabelsView{}, "SquadronTags": squadronTagsView{}}
	for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
		decoder := newLabelDecoder[escortPass, escortPassRequest](t, views)
		stmt, err := decodeLabels(t, decoder, "/?columns=id,squadronName,escortName&sort=id&limit=2", &nameGrantPermissions{}, dbType).stmt(dbType)
		if err != nil {
			t.Fatalf("stmt(%s) error = %v", dbType, err)
		}
		sql := oneLine(stmt.SQL)
		for _, view := range []struct {
			query string
			value string
		}{
			{query: "SELECT Id, SectorId, Name AS Label FROM Squadrons WHERE Name != @", value: "Green"},
			{query: "SELECT Id, SectorId, Name AS Tag FROM Squadrons WHERE Name != @", value: "Red"},
		} {
			at := strings.Index(sql, view.query)
			if at < 0 {
				t.Errorf("%s statement =\n%s\nwant it to read the view %q", dbType, sql, view.query)

				continue
			}
			placeholder, _, _ := strings.Cut(sql[at+len(view.query):], " ")
			if got := stmt.Params[placeholder]; got != view.value || !strings.HasPrefix(placeholder, "_c") {
				t.Errorf("%s statement reads %q with @%s, bound to %v, want a placeholder of the statement's own bound to %s; params = %v", dbType, view.query, placeholder, got, view.value, stmt.Params)
			}
		}
		if _, own := stmt.Params["leftOutLabel"]; own {
			t.Errorf("%s params = %v, want nothing under the views' own parameter name", dbType, stmt.Params)
		}
	}
}

// labelPassWithView is a view of the fixture's passes with labelPass's names, whose
// query Q gives.
type labelPassWithView[Q passViewQuery] struct {
	ID         string  `spanner:"Id"         postgres:"Id"`
	SectorID   string  `spanner:"SectorId"   postgres:"SectorId"`
	SquadronID *string `spanner:"SquadronId" postgres:"SquadronId"`
}

// Resource names the resource in grants and routes.
func (labelPassWithView[Q]) Resource() accesstypes.Resource {
	return "PassViews"
}

// DefaultConfig is the resource's configuration.
func (labelPassWithView[Q]) DefaultConfig() Config {
	return Config{}
}

// NameFields describes the view's name fields: labelPass's.
func (labelPassWithView[Q]) NameFields() []NameField {
	return labelNameFields()
}

// Subquery is the view's query.
func (labelPassWithView[Q]) Subquery() (query string, params map[string]any) {
	var q Q

	return q.query(), nil
}

// TestNameView_withTableInTheNamesView pins that a list of a view whose query opens
// with a WITH table spelled as a name the query of a name's view uses is built, and
// refused where it is handed the views, naming the WITH table, the name's view and the
// name; and that with the WITH table renamed it is handed them.
func TestNameView_withTableInTheNamesView(t *testing.T) {
	t.Parallel()

	t.Run("a WITH table spelled as a table the name's view reads", func(t *testing.T) {
		t.Parallel()

		decoder := newLabelDecoder[labelPassWithView[squadronsWith], labelPassRequest](t, nil)
		const want = "the query of PassViews opens with a WITH table named Squadrons, and the query of the view SquadronLabels, which the name SquadronName is read from, uses that name too: a statement over PassViews would read the WITH table's rows there. Rename the WITH table in the query of PassViews."
		defer func() {
			if got := fmt.Sprint(recover()); got != want {
				t.Errorf("WithNameViews() panicked with %q, want %q", got, want)
			}
		}()
		decoder.WithNameViews(labelViews)
	})

	t.Run("the WITH table renamed", func(t *testing.T) {
		t.Parallel()

		newLabelDecoder[labelPassWithView[renamedWith], labelPassRequest](t, labelViews)
	})
}

// TestNameView_sources pins the live source of a name read from a view: the view's
// list in the request's tenant.
func TestNameView_sources(t *testing.T) {
	t.Parallel()

	qSet := decodeLabels(t, newLabelDecoder[labelPass, labelPassRequest](t, labelViews), "/?columns=id,squadronName&sort=id&limit=2", &nameGrantPermissions{}, SpannerDBType)
	sources, err := qSet.NameSources(t.Context())
	if err != nil {
		t.Fatalf("NameSources() error = %v", err)
	}
	want := []NameSource{{Resource: "SquadronLabels", Field: "label", Domain: "sector-1"}}
	if diff := cmp.Diff(want, sources); diff != "" {
		t.Errorf("NameSources() mismatch (-want +got):\n%s", diff)
	}
}

// labelPage serves one page of the passes of sector-1 whose squadron's name is read
// from the view, as the generated handler does. It returns the page's rows in the
// list's order, each its id and the name ("none" where the row carries none), and
// the Link relations the page named.
func labelPage(t *testing.T, client *SpannerClient, target string) (rows []string, links map[string]string) {
	t.Helper()

	decoder := newLabelDecoder[labelPass, labelPassRequest](t, labelViews)
	req := acrossRequest(t, target)
	qSet, err := decoder.Decode(req, renderStubPermissions{}, inSector1)
	if err != nil {
		t.Fatalf("QueryDecoder.Decode(%s) error = %v", target, err)
	}
	page := qSet.Page()
	for row, err := range qSet.List(t.Context(), client) {
		if err != nil {
			t.Fatalf("QuerySet.List(%s) error = %v", target, err)
		}
		if !page.Add(row) {
			break
		}
		name := "none"
		if value := row.Name("SquadronName"); value != nil {
			name = *value
		}
		rows = append(rows, row.Data.ID+" "+name)
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

// TestNameView_onSpanner runs a list whose squadron's name is read from the view on
// the Spanner emulator, over the name lookups' fixture: the name shows for a row of
// the request's tenant and not for another tenant's, nor for the row the view's
// parameter leaves out; the list sorts by it both ways, walked forward and back
// across rows with no name; and a filter by it gives the rows the view gives.
func TestNameView_onSpanner(t *testing.T) {
	t.Parallel()

	client := lookupSpannerClient(t, "name-views")

	t.Run("shown", func(t *testing.T) {
		t.Parallel()

		rows, _ := labelPage(t, client, "/?columns=id,squadronName&sort=id&limit=all")
		if diff := cmp.Diff([]string{"p1 Red", "p2 none", "p3 none", "p4 none", "p5 none"}, rows); diff != "" {
			t.Errorf("the rows mismatch (-want +got):\n%s", diff)
		}
	})

	walks := []struct {
		name   string
		target string
		want   [][]string
	}{
		{
			name:   "sorted by the name",
			target: "/?columns=id,squadronName&sort=squadronName:asc&limit=2",
			want:   [][]string{{"p2 none", "p3 none"}, {"p4 none", "p5 none"}, {"p1 Red"}},
		},
		{
			name:   "sorted by the name the other way",
			target: "/?columns=id,squadronName&sort=squadronName:desc&limit=2",
			want:   [][]string{{"p1 Red", "p2 none"}, {"p3 none", "p4 none"}, {"p5 none"}},
		},
	}
	for _, tt := range walks {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var pages [][]string
			var prevs []string
			for target := tt.target; ; {
				rows, links := labelPage(t, client, target)
				pages = append(pages, rows)
				prevs = append(prevs, links["prev"])
				next, more := links["next"]
				if !more {
					break
				}
				target = next
			}
			if diff := cmp.Diff(tt.want, pages); diff != "" {
				t.Errorf("the walk forward mismatch (-want +got):\n%s", diff)
			}
			for i := len(pages) - 1; i > 0; i-- {
				if prevs[i] == "" {
					t.Fatalf("page %d names no previous page", i+1)
				}
				rows, _ := labelPage(t, client, prevs[i])
				if diff := cmp.Diff(tt.want[i-1], rows); diff != "" {
					t.Errorf("walking back from page %d mismatch (-want +got):\n%s", i+1, diff)
				}
			}
		})
	}

	filters := []struct {
		name   string
		target string
		want   []string
	}{
		{
			name:   "eq",
			target: "/?columns=id,squadronName&filter=squadronName:eq:Red&sort=id&limit=all",
			want:   []string{"p1 Red"},
		},
		{
			name:   "eq the label the view leaves out",
			target: "/?columns=id,squadronName&filter=squadronName:eq:Green&sort=id&limit=all",
		},
		{
			name:   "isnull",
			target: "/?columns=id,squadronName&filter=squadronName:isnull&sort=id&limit=all",
			want:   []string{"p2 none", "p3 none", "p4 none", "p5 none"},
		},
		{
			name:   "sorted by the name and filtered by it",
			target: "/?columns=id,squadronName&filter=squadronName:isnull&sort=squadronName:asc&limit=all",
			want:   []string{"p2 none", "p3 none", "p4 none", "p5 none"},
		},
		{
			name:   "sorted by the name the other way and filtered by it",
			target: "/?columns=id,squadronName&filter=squadronName:eq:Red&sort=squadronName:desc&limit=all",
			want:   []string{"p1 Red"},
		},
	}
	for _, tt := range filters {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows, _ := labelPage(t, client, tt.target)
			if diff := cmp.Diff(tt.want, rows); diff != "" {
				t.Errorf("the rows mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
