package resource

// These tests pin a name read from a view: the pointed-to resource is a query of
// the application's, so the statement reads the view's query, with its
// parameters, where it reads a table's name for a name read from a table.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
// column, and declares a label, which a condition on the grant of the name reads.
func labelCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{
		{
			Name:        "HangarPasses",
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.List},
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
// one line, for the database.
func labelsFrom(dbType DBType, alias string) string {
	if dbType == PostgresDBType {
		return fmt.Sprintf("(%s ) AS %q", squadronLabelsQuery, alias)
	}

	return fmt.Sprintf("(%s ) AS %#q", squadronLabelsQuery, alias)
}

// TestNameView_statement pins what a statement writes for a name read from a view:
// the lookup and the filter read the view's query under their own alias where a
// table's name would be, held to the request's tenant by the view's own column, and
// the view's parameter joins the statement's, once where the name is both shown and
// filtered by.
func TestNameView_statement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		want   map[DBType]string
	}{
		{
			name:   "shown",
			target: "/?columns=id,squadronName&sort=id&limit=2",
			want: bothDatabases(
				"(SELECT `ca1`.`Label` FROM "+labelsFrom(SpannerDBType, "ca1")+" WHERE `ca1`.`Id` = `HangarPasses`.`SquadronId` AND `ca1`.`SectorId` = @domain) AS zzNameSquadronName",
				`(SELECT "ca1"."Label" FROM `+labelsFrom(PostgresDBType, "ca1")+` WHERE "ca1"."Id" = "HangarPasses"."SquadronId" AND "ca1"."SectorId" = @domain) AS "zzNameSquadronName"`,
			),
		},
		{
			name:   "filtered by eq",
			target: "/?columns=id&filter=squadronName:eq:Red&sort=id&limit=2",
			want: bothDatabases(
				"WHERE `SquadronId` IN (SELECT `ca1`.`Id` FROM "+labelsFrom(SpannerDBType, "ca1")+" WHERE `ca1`.`SectorId` = @domain AND `Label` = @_p1)",
				`WHERE "SquadronId" IN (SELECT "ca1"."Id" FROM `+labelsFrom(PostgresDBType, "ca1")+` WHERE "ca1"."SectorId" = @domain AND "Label" = @_p1)`,
			),
		},
		{
			name:   "filtered by isnotnull",
			target: "/?columns=id&filter=squadronName:isnotnull&sort=id&limit=2",
			want: bothDatabases(
				"WHERE `SquadronId` IN (SELECT `ca1`.`Id` FROM "+labelsFrom(SpannerDBType, "ca1")+" WHERE `ca1`.`SectorId` = @domain AND `Label` IS NOT NULL)",
				`WHERE "SquadronId" IN (SELECT "ca1"."Id" FROM `+labelsFrom(PostgresDBType, "ca1")+` WHERE "ca1"."SectorId" = @domain AND "Label" IS NOT NULL)`,
			),
		},
		{
			name:   "filtered by isnull",
			target: "/?columns=id&filter=squadronName:isnull&sort=id&limit=2",
			want: bothDatabases(
				"WHERE (`SquadronId` IS NULL OR `SquadronId` NOT IN (SELECT `ca1`.`Id` FROM "+labelsFrom(SpannerDBType, "ca1")+" WHERE `ca1`.`SectorId` = @domain AND `Label` IS NOT NULL))",
				`WHERE ("SquadronId" IS NULL OR "SquadronId" NOT IN (SELECT "ca1"."Id" FROM `+labelsFrom(PostgresDBType, "ca1")+` WHERE "ca1"."SectorId" = @domain AND "Label" IS NOT NULL))`,
			),
		},
		{
			name:   "shown and filtered by: one binding of the view's parameter",
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
				if sql := oneLine(stmt.SQL); !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
				}
				if got := stmt.Params["leftOutLabel"]; got != "Green" {
					t.Errorf("%s params = %v, want the view's leftOutLabel bound to Green", dbType, stmt.Params)
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
			"(SELECT `ca1`.`Label` FROM "+labelsFrom(SpannerDBType, "ca1")+" WHERE `ca1`.`Id` = `HangarPasses`.`SquadronId` AND `ca1`.`SectorId` = @domain AND (`ca1`.`Label` = @_c1))",
			`(SELECT "ca1"."Label" FROM `+labelsFrom(PostgresDBType, "ca1")+` WHERE "ca1"."Id" = "HangarPasses"."SquadronId" AND "ca1"."SectorId" = @domain AND ("ca1"."Label" = @_c1))`,
		)
		for dbType, lookup := range lookups {
			stmt := labelStatement(t, "/?columns=id,squadronName&sort=squadronName&limit=2", perms, dbType)
			sql := oneLine(stmt.SQL)
			for _, want := range []string{lookup, "ORDER BY " + lookup + " ASC"} {
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
				}
			}
			if stmt.Params["_c1"] != "Red" || stmt.Params["leftOutLabel"] != "Green" {
				t.Errorf("%s params = %v, want the label Red bound as _c1 and the view's leftOutLabel", dbType, stmt.Params)
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
			if got := stmt.Params["leftOutLabel"]; got != "Green" {
				t.Errorf("%s count params = %v, want the view's leftOutLabel bound to Green", dbType, stmt.Params)
			}
		}
	})
}

// TestNameView_decoder pins what a decoder refuses of a name read from a view: a
// view it is not handed, where it is handed views and where it never is; a view
// whose query opens with WITH; a parameter of the view's that starts with an
// underscore; and a parameter of the list's own view's that the name's view binds
// too.
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
		{
			name: "the list's own view binds the name's view's parameter",
			build: func(t *testing.T) (*Statement, error) {
				decoder := newLabelDecoder[labelPassView, labelPassRequest](t, labelViews)

				return decodeLabels(t, decoder, "/?columns=id,squadronName&sort=id&limit=2", &nameGrantPermissions{}, SpannerDBType).stmt(SpannerDBType)
			},
			want: `named parameter collision: PassViews statement already contains named parameter "leftOutLabel"`,
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
