package resource

// These tests pin how a computed resource's rows are judged against a grant's
// condition (computed_conditions.go): the decoder keeps the Conditional decisions,
// and the rows the application's function yields are judged in Go under a table's
// rules. A row the condition on the resource refuses is not listed and not counted,
// a projected field granted under a condition the row does not meet is left out of
// that row, and a row that would show none of the fields a grant covers is left out,
// the key being outside the rule.

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
)

const judgedResource = accesstypes.Resource("judgedRows")

// judgedRow is a computed row: no database tags, the fields a condition may read
// declared in judgedCollection.
type judgedRow struct {
	Name    string
	Owner   string
	Level   int64
	Tags    []string
	Note    *string
	Expires time.Time
	Secret  string
	Summary string
}

func (judgedRow) Resource() accesstypes.Resource { return judgedResource }

func (judgedRow) DefaultConfig() Config { return Config{} }

type judgedRequest struct {
	Name    string `json:"name"    perm:"-"`
	Owner   string `json:"owner"   allow_filter:"true"`
	Level   int64  `json:"level"   allow_filter:"true"`
	Secret  string `json:"secret"`
	Summary string `json:"summary"`
}

// judgedFileRequest is the frame's projection of a @file route over the row.
type judgedFileRequest struct {
	Name string `json:"name" perm:"-"`
}

// judgedCollection registers the computed resource with the attributes its fields
// declare: each Column is the row struct's Go field, as the generator records it.
func judgedCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
		Name:        judgedResource,
		Scope:       accesstypes.DomainPermissionScope,
		Computed:    true,
		Permissions: []accesstypes.Permission{accesstypes.List, accesstypes.Read},
		Attributes: []AttributeData{
			{Name: "name", Column: "Name", Type: AttributeTypeString},
			{Name: "owner", Column: "Owner", Type: AttributeTypeString},
			{Name: "level", Column: "Level", Type: AttributeTypeNumber},
			{Name: "tag", Column: "Tags", Type: AttributeTypeString, List: true},
			{Name: "note", Column: "Note", Type: AttributeTypeString},
			{Name: "expires", Column: "Expires", Type: AttributeTypeTimestamp},
			{Name: "gone", Column: "NoSuchField", Type: AttributeTypeString},
		},
	}}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// judgedRows is the body's answer, in the order it yields. The decision instant of
// a request is the moment it is decoded, so one row expired long ago and the others
// expire long from now.
func judgedRows() []*judgedRow {
	later := time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)

	return []*judgedRow{
		{Name: "Admin", Owner: "root", Level: 9, Tags: []string{"staff", "restricted"}, Expires: later, Secret: "a", Summary: "runs the tenant"},
		{Name: "Editor", Owner: "testUser", Level: 5, Tags: []string{"staff"}, Note: strPtr("checked"), Expires: later, Secret: "e", Summary: "edits"},
		{Name: "Guest", Owner: "someoneElse", Level: 1, Expires: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), Secret: "g", Summary: "looks"},
		{Name: "Reader", Owner: "testUser", Level: 2, Tags: []string{"public"}, Expires: later, Secret: "r", Summary: "reads"},
	}
}

func judgedDecoder(t *testing.T, perm accesstypes.Permission) *ComputedQueryDecoder[judgedRow, judgedRequest] {
	t.Helper()

	return MustNewComputedQueryDecoder[judgedRow, judgedRequest](SpannerDBType, perm).
		WithCollection(judgedCollection(t)).
		WithCursorKey(mustCursorKey(t, testCookieKey)).
		WithPaging(Paging{Order: []SortField{{Field: "Name", Direction: SortAscending}}})
}

func yieldJudged(rows []*judgedRow) func(yield func(*judgedRow, error) bool) {
	return func(yield func(*judgedRow, error) bool) {
		for _, row := range rows {
			if !yield(row, nil) {
				return
			}
		}
	}
}

func TestQuerySet_Collect_judgesRows(t *testing.T) {
	t.Parallel()

	const (
		secret  = judgedResource + ".secret"
		summary = judgedResource + ".summary"
		owner   = judgedResource + ".owner"
		level   = judgedResource + ".level"
	)
	every := []accesstypes.Resource{judgedResource, owner, level, secret, summary}

	tests := []struct {
		name       string
		target     string
		grants     []accesstypes.Resource
		conditions map[accesstypes.Resource]string
		// wantRows is each listed row with the wire names of the fields it shows.
		wantRows  []string
		wantTotal *int64
		wantMore  bool
	}{
		{
			name:     "with nothing conditional every row is shown whole",
			target:   "/?columns=name,summary&limit=all",
			grants:   every,
			wantRows: []string{"Admin[name summary]", "Editor[name summary]", "Guest[name summary]", "Reader[name summary]"},
		},
		{
			name:       "a row the condition on the resource refuses is not listed",
			target:     "/?columns=name,summary&limit=all",
			grants:     []accesstypes.Resource{summary},
			conditions: map[accesstypes.Resource]string{judgedResource: "name != 'Admin'"},
			wantRows:   []string{"Editor[name summary]", "Guest[name summary]", "Reader[name summary]"},
		},
		{
			name:       "nor counted, and the page fills from the rows that are left",
			target:     "/?columns=name&limit=2&count=true",
			grants:     []accesstypes.Resource{summary},
			conditions: map[accesstypes.Resource]string{judgedResource: "name != 'Admin'"},
			wantRows:   []string{"Editor[name]", "Guest[name]"},
			wantTotal:  new(int64(3)),
			wantMore:   true,
		},
		{
			name:       "a request for the key alone is trimmed by the condition on the resource",
			target:     "/?columns=name&limit=all",
			conditions: map[accesstypes.Resource]string{judgedResource: "name != 'Admin'"},
			wantRows:   []string{"Editor[name]", "Guest[name]", "Reader[name]"},
		},
		{
			name:       "a field granted under a condition is left out of the rows that do not meet it",
			target:     "/?columns=name,summary,secret&limit=all",
			grants:     []accesstypes.Resource{judgedResource, summary},
			conditions: map[accesstypes.Resource]string{secret: "owner = subject"},
			wantRows:   []string{"Admin[name summary]", "Editor[name summary secret]", "Guest[name summary]", "Reader[name summary secret]"},
		},
		{
			name:       "the default projection carries the conditional field, row by row",
			target:     "/?limit=all",
			grants:     []accesstypes.Resource{judgedResource, summary},
			conditions: map[accesstypes.Resource]string{secret: "level >= 5"},
			wantRows:   []string{"Admin[name secret summary]", "Editor[name secret summary]", "Guest[name summary]", "Reader[name summary]"},
		},
		{
			name:       "a row that would show none of the fields a grant covers is left out",
			target:     "/?columns=name,secret&limit=all",
			grants:     []accesstypes.Resource{judgedResource, summary},
			conditions: map[accesstypes.Resource]string{secret: "owner = subject"},
			wantRows:   []string{"Editor[name secret]", "Reader[name secret]"},
		},
		{
			name:       "two conditional fields: a row shows with either, and without both it is left out",
			target:     "/?columns=name,secret,summary&limit=all",
			grants:     []accesstypes.Resource{judgedResource},
			conditions: map[accesstypes.Resource]string{secret: "owner = subject", summary: "level >= 5"},
			wantRows:   []string{"Admin[name summary]", "Editor[name secret summary]", "Reader[name secret]"},
		},
		{
			name:       "a list field is judged item by item, and every item has to pass",
			target:     "/?columns=name&limit=all",
			conditions: map[accesstypes.Resource]string{judgedResource: "tag != 'restricted'"},
			wantRows:   []string{"Editor[name]", "Reader[name]"},
		},
		{
			name:       "a field with no value never permits, and IS NULL is true of it",
			target:     "/?columns=name&limit=all",
			conditions: map[accesstypes.Resource]string{judgedResource: "note IS NULL OR note = 'checked'"},
			wantRows:   []string{"Admin[name]", "Editor[name]", "Guest[name]", "Reader[name]"},
		},
		{
			name:       "a comparison against no value is unknown, which refuses",
			target:     "/?columns=name&limit=all",
			conditions: map[accesstypes.Resource]string{judgedResource: "note != 'x'"},
			wantRows:   []string{"Editor[name]"},
		},
		{
			name:       "now is the request's decision instant",
			target:     "/?columns=name&limit=all",
			conditions: map[accesstypes.Resource]string{judgedResource: "expires > now"},
			wantRows:   []string{"Admin[name]", "Editor[name]", "Reader[name]"},
		},
		{
			name:       "the request's filter and sort run over the rows the grants admit",
			target:     "/?columns=name&filter=level:gt:1&sort=level:desc&limit=all&count=true",
			grants:     []accesstypes.Resource{level},
			conditions: map[accesstypes.Resource]string{judgedResource: "name != 'Admin'"},
			wantRows:   []string{"Editor[name]", "Reader[name]"},
			wantTotal:  new(int64(2)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			userPermissions := &fakeUserPermissions{
				granted:    map[accesstypes.Permission][]accesstypes.Resource{accesstypes.List: tt.grants},
				conditions: tt.conditions,
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody)
			qSet, err := judgedDecoder(t, accesstypes.List).Decode(req, userPermissions, testScope)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if got, want := qSet.JudgesRows(), len(tt.conditions) > 0; got != want {
				t.Errorf("JudgesRows() = %v, want %v", got, want)
			}

			page, err := qSet.Collect(yieldJudged(judgedRows()))
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			got := make([]string, 0, len(page.Rows()))
			for i, row := range page.Rows() {
				names := make([]string, 0, len(page.RowFields(i)))
				for _, field := range page.RowFields(i) {
					names = append(names, qSet.jsonName(field))
				}
				got = append(got, row.Name+"["+strings.Join(names, " ")+"]")
			}
			if !slices.Equal(got, tt.wantRows) {
				t.Errorf("Collect() rows = %v, want %v", got, tt.wantRows)
			}
			if (page.total == nil) != (tt.wantTotal == nil) || (page.total != nil && *page.total != *tt.wantTotal) {
				t.Errorf("total = %v, want %v", page.total, tt.wantTotal)
			}
			if page.more != tt.wantMore {
				t.Errorf("more = %v, want %v", page.more, tt.wantMore)
			}
		})
	}
}

// TestQuerySet_TakePage_judgedRows pins that a body does not page for itself while
// a condition is left to judge its rows by: a row the handler leaves out would
// leave the body's page short.
func TestQuerySet_TakePage_judgedRows(t *testing.T) {
	t.Parallel()

	for name, conditions := range map[string]map[accesstypes.Resource]string{
		"nothing conditional":         nil,
		"a condition on the resource": {judgedResource: "name != 'Admin'"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			userPermissions := &fakeUserPermissions{
				granted:    map[accesstypes.Permission][]accesstypes.Resource{accesstypes.List: {judgedResource}},
				conditions: conditions,
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?columns=name&limit=2", http.NoBody)
			qSet, err := judgedDecoder(t, accesstypes.List).Decode(req, userPermissions, testScope)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			qSet.TakeSort()
			if _, taken := qSet.TakePage(); taken != (conditions == nil) {
				t.Errorf("TakePage() taken = %v, want %v", taken, conditions == nil)
			}
		})
	}
}

// TestQuerySet_Admit pins the keyed read: the row the application's Read function
// answered is admitted under the rules a list's rows are, and a row the grants do not
// admit is answered as one that is not there.
func TestQuerySet_Admit(t *testing.T) {
	t.Parallel()

	const (
		secret  = judgedResource + ".secret"
		summary = judgedResource + ".summary"
	)
	rows := judgedRows()
	admin, editor := rows[0], rows[1]

	tests := []struct {
		name       string
		target     string
		grants     []accesstypes.Resource
		conditions map[accesstypes.Resource]string
		row        *judgedRow
		wantShown  bool
		wantFields []accesstypes.Field
	}{
		{
			name:   "a row that is not there is not shown",
			target: "/",
			grants: []accesstypes.Resource{judgedResource, summary},
		},
		{
			name:       "with nothing conditional the row is shown whole",
			target:     "/",
			grants:     []accesstypes.Resource{judgedResource, summary},
			row:        admin,
			wantShown:  true,
			wantFields: []accesstypes.Field{"Name", "Summary"},
		},
		{
			name:       "a row the condition on the resource refuses is not shown",
			target:     "/",
			grants:     []accesstypes.Resource{summary},
			conditions: map[accesstypes.Resource]string{judgedResource: "name != 'Admin'"},
			row:        admin,
		},
		{
			name:       "a row it admits is",
			target:     "/",
			grants:     []accesstypes.Resource{summary},
			conditions: map[accesstypes.Resource]string{judgedResource: "name != 'Admin'"},
			row:        editor,
			wantShown:  true,
			wantFields: []accesstypes.Field{"Name", "Summary"},
		},
		{
			name:       "a field granted under a condition the row does not meet is left out",
			target:     "/",
			grants:     []accesstypes.Resource{judgedResource, summary},
			conditions: map[accesstypes.Resource]string{secret: "owner = subject"},
			row:        admin,
			wantShown:  true,
			wantFields: []accesstypes.Field{"Name", "Summary"},
		},
		{
			name:       "and shown on a row that meets it",
			target:     "/",
			grants:     []accesstypes.Resource{judgedResource, summary},
			conditions: map[accesstypes.Resource]string{secret: "owner = subject"},
			row:        editor,
			wantShown:  true,
			wantFields: []accesstypes.Field{"Name", "Secret", "Summary"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			userPermissions := &fakeUserPermissions{
				granted:    map[accesstypes.Permission][]accesstypes.Resource{accesstypes.Read: tt.grants},
				conditions: tt.conditions,
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody)
			qSet, err := judgedDecoder(t, accesstypes.Read).Decode(req, userPermissions, testScope)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			fields, shown, err := qSet.Admit(tt.row)
			if err != nil {
				t.Fatalf("Admit() error = %v", err)
			}
			if shown != tt.wantShown {
				t.Fatalf("Admit() shown = %v, want %v", shown, tt.wantShown)
			}
			if got := slices.Sorted(slices.Values(fields)); !slices.Equal(got, tt.wantFields) {
				t.Errorf("Admit() fields = %v, want %v", got, tt.wantFields)
			}
		})
	}
}

// TestComputedQueryDecoder_Decode_conditionsItCannotJudge pins the runtime backstop
// behind deploy validation and the wiring: a condition that reads what the resource
// does not declare, or a fact about the caller with no client wired to read it
// through, is refused, never judged as false and never let through. (The facts read
// from a database are the semantic differential's, subject_facts_test.go.)
func TestComputedQueryDecoder_Decode_conditionsItCannotJudge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		condition string
		wantErr   string
	}{
		{name: "an attribute the resource does not declare", condition: "color = 'red'", wantErr: `reads "color", which the resource does not declare`},
		{name: "an attribute declared on a field the row does not have", condition: "gone = 'x'", wantErr: `declares the attribute "gone" on the field NoSuchField`},
		{name: "a subject set with no client to read it through", condition: "name IN subject.roles", wantErr: "reads subject.roles, a fact about the caller held in a table, and the decoder has no client to read it through"},
		{name: "a subject value with no client to read it through", condition: "owner = subject.organization", wantErr: "reads subject.organization, a fact about the caller held in a table, and the decoder has no client to read it through"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			userPermissions := &fakeUserPermissions{conditions: map[accesstypes.Resource]string{judgedResource: tt.condition}}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?columns=name&limit=all", http.NoBody)
			_, err := judgedDecoder(t, accesstypes.List).Decode(req, userPermissions, testScope)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Decode() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestFileDecoder_Decode_computedJudgesTheRow pins a computed resource's @file route
// under a conditional Read grant: the gate passes, and the row the resource's read
// function answers is judged by the condition on the resource and by the segment's,
// the route's one grant-bearing projection.
func TestFileDecoder_Decode_computedJudgesTheRow(t *testing.T) {
	t.Parallel()

	const gate = judgedResource + ".content"
	rows := judgedRows()
	admin, editor := rows[0], rows[1]

	tests := []struct {
		name       string
		grants     []accesstypes.Resource
		conditions map[accesstypes.Resource]string
		wantJudged bool
		wantAdmin  bool
		wantEditor bool
	}{
		{
			name:       "unconditional grants show every row",
			grants:     []accesstypes.Resource{judgedResource, gate},
			wantAdmin:  true,
			wantEditor: true,
		},
		{
			name:       "a condition on the segment is the row's condition",
			grants:     []accesstypes.Resource{judgedResource},
			conditions: map[accesstypes.Resource]string{gate: "owner = subject"},
			wantJudged: true,
			wantEditor: true,
		},
		{
			name:       "a condition on the resource is judged too",
			grants:     []accesstypes.Resource{gate},
			conditions: map[accesstypes.Resource]string{judgedResource: "name = 'Admin'"},
			wantJudged: true,
			wantAdmin:  true,
		},
		{
			name:       "both have to admit the row",
			conditions: map[accesstypes.Resource]string{judgedResource: "level >= 5", gate: "owner = subject"},
			wantJudged: true,
			wantEditor: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			userPermissions := &fakeUserPermissions{
				granted:    map[accesstypes.Permission][]accesstypes.Resource{accesstypes.Read: tt.grants},
				conditions: tt.conditions,
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			qSet, err := MustNewComputedFileDecoder[judgedRow, judgedFileRequest](judgedCollection(t), "content").Decode(req, userPermissions, testScope)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if qSet.JudgesRows() != tt.wantJudged {
				t.Errorf("JudgesRows() = %v, want %v", qSet.JudgesRows(), tt.wantJudged)
			}
			for row, want := range map[*judgedRow]bool{admin: tt.wantAdmin, editor: tt.wantEditor} {
				_, shown, err := qSet.Admit(row)
				if err != nil {
					t.Fatalf("Admit(%s) error = %v", row.Name, err)
				}
				if shown != want {
					t.Errorf("Admit(%s) shown = %v, want %v", row.Name, shown, want)
				}
			}
		})
	}
}
