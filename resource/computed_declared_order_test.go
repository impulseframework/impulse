package resource

// These tests pin a computed list's declared order, where the request states no
// sort: the handler, or the body, sorts the rows by their real values, so a
// field of the order the person does not hold with no condition is left out of
// their order, and their list comes in the order of the order's other terms.
// Nothing is refused, and the decoder's declared order is the same for the next
// request.

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

	return MustNewComputedQueryDecoder[judgedRow, judgedRequest](SpannerDBType, accesstypes.List).
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
	conditional := func() *fakeUserPermissions {
		return &fakeUserPermissions{
			granted:    map[accesstypes.Permission][]accesstypes.Resource{accesstypes.List: {judgedResource}},
			conditions: map[accesstypes.Resource]string{level: "owner = subject"},
		}
	}
	byName := []SortField{{Field: "Name", Direction: SortAscending}}

	t.Run("the level granted: by level, highest first", func(t *testing.T) {
		t.Parallel()

		_, names, _ := judgedPage(t, byLevelDecoder(t), "/?columns=name", granted())
		if want := []string{"Admin", "Editor", "Reader", "Guest"}; !slices.Equal(names, want) {
			t.Errorf("rows = %v, want %v", names, want)
		}
	})

	for name, perms := range map[string]func() *fakeUserPermissions{"denied": denied, "held under a condition": conditional} {
		t.Run("the level "+name+": by name, the level left out of the order", func(t *testing.T) {
			t.Parallel()

			qSet, names, _ := judgedPage(t, byLevelDecoder(t), "/?columns=name", perms())
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
	}

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
