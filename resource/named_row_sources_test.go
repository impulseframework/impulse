package resource

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

// countedView is a view of the tests whose query is the one given, with its parameters,
// counting the times its query is read.
type countedView struct {
	query  string
	params map[string]any

	mu    sync.Mutex
	reads int
}

func (v *countedView) Subquery() (query string, params map[string]any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++

	return v.query, maps.Clone(v.params)
}

// readsMade answers how many times the view's query was read.
func (v *countedView) readsMade() int {
	v.mu.Lock()
	defer v.mu.Unlock()

	return v.reads
}

// TestMergeNamedRowSources pins the merge of the sources a write is handed: every entry of
// every argument, a later argument's entry replacing an earlier one's, and an empty map,
// never nil, for none.
func TestMergeNamedRowSources(t *testing.T) {
	t.Parallel()

	first, second := ViewRows(&countedView{query: "SELECT 1"}), ViewRows(&countedView{query: "SELECT 2"})
	merged := MergeNamedRowSources(NamedRowSources{"A": first, "B": first}, NamedRowSources{"B": second})
	if len(merged) != 2 || merged["A"].view != first.view || merged["B"].view != second.view {
		t.Errorf("MergeNamedRowSources() = %v, want A from the first argument and B from the second", merged)
	}
	if none := MergeNamedRowSources(); none == nil {
		t.Error("MergeNamedRowSources() of nothing = nil, want an empty map")
	}
}

// TestNamedRowSources_carried pins who carries the sources a decoder is handed to the
// write that reads them: a patch a decoder answers holds them, the caller an RPC method's
// decode answers carries them, so does the caller of the gate a targeted method's decode
// answers, of a gate a body asks through that caller and of the caller As answers, and a
// computed list's query and its caller carry them; a write armed with Enforce takes them
// from the caller.
func TestNamedRowSources_carried(t *testing.T) {
	t.Parallel()

	sources := NamedRowSources{"ActiveProjects": ViewRows(&countedView{query: "SELECT 1"})}
	carries := func(t *testing.T, of string, got NamedRowSources) {
		t.Helper()

		if len(got) != 1 || got["ActiveProjects"].view != sources["ActiveProjects"].view {
			t.Errorf("%s carries %v, want the sources handed to the decoder", of, got)
		}
	}
	perms := &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{
		accesstypes.Execute: {rpcEnforcedMethod, "TargetedMethod", "RateThing"},
		accesstypes.List:    {judgedResource},
	}}
	body := func() *http.Request {
		return httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{"name":"n"}`))
	}

	rSet, err := NewSet[gatedResource, gatedRequest](accesstypes.Update)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewDecoder[gatedResource, gatedRequest](rSet)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}
	decoder = decoder.WithNamedRowSources(sources)
	trusted, err := decoder.DecodeWithoutPermissions(body())
	if err != nil {
		t.Fatalf("Decoder.DecodeWithoutPermissions() error = %v", err)
	}
	carries(t, "a patch decoded without permissions", trusted.querySet.namedRowSources)
	armed, err := decoder.Decode(body(), perms, testScope, accesstypes.Update)
	if err != nil {
		t.Fatalf("Decoder.Decode() error = %v", err)
	}
	carries(t, "a patch decoded for a caller", armed.querySet.namedRowSources)

	rpc, err := NewRPCDecoder[rpcEnforcementRequest](func(*http.Request) UserPermissions { return perms }, rpcEnforcedMethod, accesstypes.Execute)
	if err != nil {
		t.Fatalf("NewRPCDecoder() error = %v", err)
	}
	_, caller, err := rpc.WithNamedRowSources(sources).DecodeCaller(body(), testScope)
	if err != nil {
		t.Fatalf("RPCDecoder.DecodeCaller() error = %v", err)
	}
	carries(t, "the caller of a method that names no row", caller.namedRowSources)
	carries(t, "the caller As answers", caller.As(perms).namedRowSources)
	enforced := NewPatchSet(NewMetadata[gatedResource]()).SetPatchType(UpdatePatchType).Enforce(caller, rSet, accesstypes.Update)
	carries(t, "a write armed with Enforce", enforced.querySet.namedRowSources)

	targeted := MustNewTargetedRPCDecoder[rpcEnforcementRequest](entryAccessor{perms: perms}, nil, "TargetedMethod", accesstypes.Execute).WithNamedRowSources(sources)
	_, gate, err := targeted.Decode(body(), testScope)
	if err != nil {
		t.Fatalf("TargetedRPCDecoder.Decode() error = %v", err)
	}
	carries(t, "the caller of a method that names a row", gate.Caller().namedRowSources)
	asked, err := gate.Caller().ExecuteGate(context.Background(), "RateThing", &rateRequest{})
	if err != nil {
		t.Fatalf("Caller.ExecuteGate() error = %v", err)
	}
	carries(t, "the caller of a gate a body asks", asked.Caller().namedRowSources)

	list := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?columns=name&limit=all", http.NoBody)
	qSet, err := judgedDecoder(t, accesstypes.List).WithNamedRowSources(sources).Decode(list, perms, testScope)
	if err != nil {
		t.Fatalf("ComputedQueryDecoder.Decode() error = %v", err)
	}
	carries(t, "a computed list's query", qSet.namedRowSources)
	carries(t, "the caller of a computed list's query", qSet.Caller().namedRowSources)
}

// TestPatchSet_namedRows_view_mock pins that a mocked transaction, which holds no view to
// read, passes a column picked from a view, though the write was handed no source.
func TestPatchSet_namedRows_view_mock(t *testing.T) {
	t.Parallel()

	former := "p3"
	p := NewPatchSet(NewMetadata[viewTask]()).SetPatchType(CreatePatchType)
	p.SetKey("ID", "v-mock")
	p.Set("DomainID", onRecordNorth)
	p.Set("FormerProjectID", &former)
	arm(t, p.querySet, namedViewCollection(t), &namedWrite{patchType: CreatePatchType, caller: listsActiveProjects()})
	named, err := p.namedValues()
	if err != nil {
		t.Fatalf("namedValues() error = %v", err)
	}
	if len(named) != 1 {
		t.Fatalf("namedValues() = %v, want the former project", named)
	}
	if err := p.checkNamedRows(context.Background(), NewMockReadWriteTransaction(&bufferingTxn{}), named); err != nil {
		t.Errorf("checkNamedRows() error = %v, want none on a mocked transaction", err)
	}
}

// TestPatchSet_namedRows_computed_mock pins that a mocked transaction passes a column
// picked from a computed list without running the list's function.
func TestPatchSet_namedRows_computed_mock(t *testing.T) {
	t.Parallel()

	former := "p3"
	list := newProjectList()
	p := NewPatchSet(NewMetadata[listTask]()).SetPatchType(CreatePatchType)
	p.SetKey("ID", "l-mock")
	p.Set("DomainID", onRecordNorth)
	p.Set("FormerProjectID", &former)
	arm(t, p.querySet, namedListCollection(t), &namedWrite{patchType: CreatePatchType, caller: listsListedProjects()})
	p.querySet.namedRowSources = list.sources()
	named, err := p.namedValues()
	if err != nil {
		t.Fatalf("namedValues() error = %v", err)
	}
	if err := p.checkNamedRows(context.Background(), NewMockReadWriteTransaction(&bufferingTxn{}), named); err != nil {
		t.Errorf("checkNamedRows() error = %v, want none on a mocked transaction", err)
	}
	if runs := list.runsMade(); runs != 0 {
		t.Errorf("the list's function ran %d times, want none", runs)
	}
}

// TestComputedRows_aKeyTheStructDoesNotHave pins that a source whose key is not a field
// of the row struct is refused where it is made.
func TestComputedRows_aKeyTheStructDoesNotHave(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("ComputedRows() with a key the struct does not have did not panic")
		}
	}()
	ComputedRows[listedProject]("Key", newProjectList().list)
}
