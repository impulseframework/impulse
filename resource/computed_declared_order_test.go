package resource

// These tests pin a computed list's declared order, where the request states no
// sort. A field of the order the person may not read is left out of their order,
// and their list comes in the order of the order's other terms. A field they hold
// under a condition stays in the order: a row orders by its value where the
// condition holds for it, and as a row with no value where it does not. Nothing
// is refused, and the decoder's declared order is the same for the next request.

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// byLevelDecoder is the judged rows' list decoder in a declared order by level,
// highest first: by it the rows come Admin, Editor, Reader, Guest, and by their
// names Admin, Editor, Guest, Reader.
func byLevelDecoder(t *testing.T) *ComputedQueryDecoder[judgedRow, judgedRequest] {
	t.Helper()

	return byLevelDecoderOn(t, SpannerDBType)
}

// byLevelDecoderOn is byLevelDecoder stamped with the application database's
// type, which places NULL for the handler's sort.
func byLevelDecoderOn(t *testing.T, dbType DBType) *ComputedQueryDecoder[judgedRow, judgedRequest] {
	t.Helper()

	return MustNewComputedQueryDecoder[judgedRow, judgedRequest](dbType, accesstypes.List).
		WithCollection(judgedCollection(t)).
		WithCursorKey(mustCursorKey(t, testCookieKey)).
		WithPaging(Paging{Order: []SortField{{Field: "Level", Direction: SortDescending}}})
}

// judgedPage decodes the request on the decoder, collects the judged rows and
// returns the query set, the rows' names and the page's next link, "" where it
// names none.
func judgedPage(t *testing.T, decoder *ComputedQueryDecoder[judgedRow, judgedRequest], target string, perms UserPermissions) (qSet *QuerySet[judgedRow], names []string, next string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	qSet, err := decoder.Decode(req, perms, testScope)
	if err != nil {
		t.Fatalf("Decode(%s) error = %v", target, err)
	}
	page, err := qSet.Collect(yieldJudged(judgedRows()))
	if err != nil {
		t.Fatalf("Collect(%s) error = %v", target, err)
	}
	for _, row := range page.Rows() {
		names = append(names, row.Name)
	}
	rr := httptest.NewRecorder()
	if err := page.WriteHeaders(rr, req); err != nil {
		t.Fatalf("Page.WriteHeaders(%s) error = %v", target, err)
	}
	for _, match := range linkPattern.FindAllStringSubmatch(rr.Header().Get(LinkHeader), -1) {
		if match[2] == "next" {
			next = match[1]
		}
	}

	return qSet, names, next
}

// TestComputedDeclaredOrder_fieldTheReaderMayNotRead pins the judged rows'
// declared order by level.
func TestComputedDeclaredOrder_fieldTheReaderMayNotRead(t *testing.T) {
	t.Parallel()

	const level = judgedResource + ".level"
	granted := func() *fakeUserPermissions {
		return &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{accesstypes.List: {judgedResource, level}}}
	}
	denied := func() *fakeUserPermissions {
		return &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{accesstypes.List: {judgedResource}}}
	}
	byName := []SortField{{Field: "Name", Direction: SortAscending}}

	t.Run("the level granted: by level, highest first", func(t *testing.T) {
		t.Parallel()

		_, names, _ := judgedPage(t, byLevelDecoder(t), "/?columns=name", granted())
		if want := []string{"Admin", "Editor", "Reader", "Guest"}; !slices.Equal(names, want) {
			t.Errorf("rows = %v, want %v", names, want)
		}
	})

	t.Run("the level denied: by name, the level left out of the order", func(t *testing.T) {
		t.Parallel()

		qSet, names, _ := judgedPage(t, byLevelDecoder(t), "/?columns=name", denied())
		if want := []string{"Admin", "Editor", "Guest", "Reader"}; !slices.Equal(names, want) {
			t.Errorf("rows = %v, want %v", names, want)
		}
		if diff := cmp.Diff(byName, qSet.Order()); diff != "" {
			t.Errorf("Order() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(byName, qSet.TakeSort()); diff != "" {
			t.Errorf("TakeSort() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("the level denied, paged: the next page follows by name", func(t *testing.T) {
		t.Parallel()

		decoder := byLevelDecoder(t)
		_, first, next := judgedPage(t, decoder, "/?columns=name&limit=2", denied())
		if want := []string{"Admin", "Editor"}; !slices.Equal(first, want) {
			t.Errorf("the first page = %v, want %v", first, want)
		}
		if next == "" {
			t.Fatal("the first page names no next page")
		}
		_, second, _ := judgedPage(t, decoder, next, denied())
		if want := []string{"Guest", "Reader"}; !slices.Equal(second, want) {
			t.Errorf("the second page = %v, want %v", second, want)
		}
	})

	t.Run("the decoder's declared order unchanged by a request that left the level out", func(t *testing.T) {
		t.Parallel()

		decoder := byLevelDecoder(t)
		judgedPage(t, decoder, "/?columns=name", denied())
		_, names, _ := judgedPage(t, decoder, "/?columns=name", granted())
		if want := []string{"Admin", "Editor", "Reader", "Guest"}; !slices.Equal(names, want) {
			t.Errorf("the granted request's rows = %v, want %v", names, want)
		}
	})

	t.Run("the level denied, a sort by it asked: refused", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?columns=name&sort=level", http.NoBody)
		_, err := byLevelDecoder(t).Decode(req, denied(), testScope)
		if !httpio.HasForbidden(err) {
			t.Errorf("Decode() error = %v, want Forbidden", err)
		}
	})
}

// TestComputedDeclaredOrder_fieldHeldUnderACondition pins the judged rows'
// declared order by level, the level held under "owner = subject": testUser's
// rows (Editor, level 5, and Reader, level 2) show it and the others (Admin and
// Guest) do not.
func TestComputedDeclaredOrder_fieldHeldUnderACondition(t *testing.T) {
	t.Parallel()

	const (
		level   = judgedResource + ".level"
		summary = judgedResource + ".summary"
	)
	// conditional holds the level under the condition. Where the projection has no
	// field named, the summary is granted outright, so a row that does not meet the
	// condition is still shown, without its level.
	conditional := func() *fakeUserPermissions {
		return &fakeUserPermissions{
			granted:    map[accesstypes.Permission][]accesstypes.Resource{accesstypes.List: {judgedResource, summary}},
			conditions: map[accesstypes.Resource]string{level: "owner = subject"},
		}
	}
	byLevel := []SortField{{Field: "Level", Direction: SortDescending}, {Field: "Name", Direction: SortAscending}}
	// On Spanner NULL sorts last descending: the rows that do not show the level
	// come after those that do, in name order.
	spannerOrder := []string{"Editor", "Reader", "Admin", "Guest"}

	t.Run("columns=name: the caller's rows by level, then the others by name", func(t *testing.T) {
		t.Parallel()

		qSet, names, _ := judgedPage(t, byLevelDecoder(t), "/?columns=name", conditional())
		if !slices.Equal(names, spannerOrder) {
			t.Errorf("rows = %v, want %v", names, spannerOrder)
		}
		if diff := cmp.Diff(byLevel, qSet.Order()); diff != "" {
			t.Errorf("Order() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(byLevel, qSet.TakeSort()); diff != "" {
			t.Errorf("TakeSort() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("no columns: the same order, and the others' rows without the level", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		qSet, err := byLevelDecoder(t).Decode(req, conditional(), testScope)
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		page, err := qSet.Collect(yieldJudged(judgedRows()))
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		var names []string
		for i, row := range page.Rows() {
			names = append(names, row.Name)
			shows := slices.Contains(page.RowFields(i), "Level")
			if want := row.Owner == "testUser"; shows != want {
				t.Errorf("%s shows its level = %v, want %v", row.Name, shows, want)
			}
		}
		if !slices.Equal(names, spannerOrder) {
			t.Errorf("rows = %v, want %v", names, spannerOrder)
		}
	})

	t.Run("walked a row a page: every page follows, across a boundary that shows the level and one that does not", func(t *testing.T) {
		t.Parallel()

		decoder := byLevelDecoder(t)
		perms := conditional()
		var walked []string
		target := "/?columns=name&limit=1"
		for range len(spannerOrder) + 1 {
			_, names, next := judgedPage(t, decoder, target, perms)
			walked = append(walked, names...)
			if next == "" {
				break
			}
			target = next
		}
		if !slices.Equal(walked, spannerOrder) {
			t.Errorf("the walk = %v, want %v", walked, spannerOrder)
		}
	})

	t.Run("the body took the sort and yields by the real level: the handler sorts again", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?columns=name", http.NoBody)
		qSet, err := byLevelDecoder(t).Decode(req, conditional(), testScope)
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if diff := cmp.Diff(byLevel, qSet.TakeSort()); diff != "" {
			t.Errorf("TakeSort() mismatch (-want +got):\n%s", diff)
		}
		if _, took := qSet.TakePage(); took {
			t.Error("TakePage() = true, want false: the handler sorts the page again")
		}
		rows := judgedRows()
		byRealLevel := []*judgedRow{rows[0], rows[1], rows[3], rows[2]}
		page, err := qSet.Collect(yieldJudged(byRealLevel))
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		var names []string
		for _, row := range page.Rows() {
			names = append(names, row.Name)
		}
		if !slices.Equal(names, spannerOrder) {
			t.Errorf("rows = %v, want %v", names, spannerOrder)
		}
	})

	t.Run("on PostgreSQL: the others first", func(t *testing.T) {
		t.Parallel()

		_, names, _ := judgedPage(t, byLevelDecoderOn(t, PostgresDBType), "/?columns=name", conditional())
		if want := []string{"Admin", "Guest", "Editor", "Reader"}; !slices.Equal(names, want) {
			t.Errorf("rows = %v, want %v", names, want)
		}
	})

	t.Run("every field under the one condition, no columns: the caller's rows alone, by level", func(t *testing.T) {
		t.Parallel()

		perms := &fakeUserPermissions{
			granted: map[accesstypes.Permission][]accesstypes.Resource{accesstypes.List: {judgedResource}},
			conditions: map[accesstypes.Resource]string{
				level:                      "owner = subject",
				summary:                    "owner = subject",
				judgedResource + ".owner":  "owner = subject",
				judgedResource + ".secret": "owner = subject",
			},
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		qSet, err := byLevelDecoder(t).Decode(req, perms, testScope)
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		page, err := qSet.Collect(yieldJudged(judgedRows()))
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		var names []string
		for i, row := range page.Rows() {
			names = append(names, row.Name)
			if !slices.Contains(page.RowFields(i), "Level") {
				t.Errorf("%s is shown without its level", row.Name)
			}
		}
		if want := []string{"Editor", "Reader"}; !slices.Equal(names, want) {
			t.Errorf("rows = %v, want %v", names, want)
		}
	})
}
