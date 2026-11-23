package resource

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/impulseframework/impulse/accesstypes"
)

// A committed transaction's writes are what the live pages publish: the rows a
// request created, updated, or deleted, per resource, each with the tenant domain the
// patch was decoded in. The transaction wrapper records every patch it buffers (the
// change-event rows a tracked resource writes beside its patches are the library's
// bookkeeping, never a row a page shows, and are not recorded), and the executor hands
// the record to the collector the request put in its context once the commit lands. A
// transaction that does not commit touched nothing.

// RowChange is one row a committed transaction wrote: its key as the read route spells
// it (RowKey), and whether the write deleted it.
type RowChange struct {
	Key     string
	Deleted bool
}

// RowKey spells a row's primary key the way the read route does: the key values in
// route order, each in its text form, joined with "/". It is the key a live row
// subscription names and a change document carries, so a client that addresses the row
// by its route and the publisher that saw the patch spell it identically.
func RowKey(values ...any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, keyPartString(value))
	}

	return strings.Join(parts, "/")
}

// keyPartString renders one key value: its String form when it has one (a UUID, a
// typed identifier), fmt's otherwise.
func keyPartString(value any) string {
	if s, ok := value.(fmt.Stringer); ok {
		return s.String()
	}

	return fmt.Sprint(value)
}

// touchedRow is one recorded write: the row, and the domain the patch was decoded in
// (the zero Domain for a global resource, and for a patch built by hand, which is then
// the request's).
type touchedRow struct {
	resource accesstypes.Resource
	domain   accesstypes.Domain
	change   RowChange
}

// touchedRows is the record a write transaction keeps of the rows its patches wrote, in
// first-buffered order, one entry per row: a row written twice keeps its position and
// takes the later write's deleted flag. Like the released-keys record it belongs to one
// attempt: a retried transaction runs its function again over a fresh record.
type touchedRows struct {
	rows []touchedRow
	// matches holds each recorded row's key as two patches of one row both spell it
	// (rowMatchKey), beside rows: what a row is found again by.
	matches []string
}

// rowMatchKey spells a row's primary key the one way every patch of the row does,
// whichever order the patch was keyed in: the key columns sorted by name, each column's
// name and value preceded by its length. A patch names its key columns in the order its
// SetKey calls came, so the key a page is told (RowKey) is no way to find a row again.
func rowMatchKey(key KeySet) string {
	parts := slices.Clone(key.Parts())
	slices.SortFunc(parts, func(a, b KeyPart) int {
		return strings.Compare(string(a.Key), string(b.Key))
	})
	var match strings.Builder
	for _, part := range parts {
		value := keyPartString(part.Value)
		fmt.Fprintf(&match, "%d:%s=%d:%s;", len(part.Key), part.Key, len(value), value)
	}

	return match.String()
}

// newTouchedRows returns an empty record.
func newTouchedRows() *touchedRows {
	return &touchedRows{}
}

// record notes one buffered patch's row. A patch with no key (a resource with no
// primary key cannot be patched, so none arrives) and a change-event row are skipped.
func (t *touchedRows) record(patch PatchSetMetadata) {
	if _, isEvent := patch.(*DataChangeEvent); isEvent {
		return
	}
	key := patch.PrimaryKey()
	if key.Len() == 0 {
		return
	}
	values := make([]any, 0, key.Len())
	for _, part := range key.Parts() {
		values = append(values, part.Value)
	}
	row := touchedRow{
		resource: patch.Resource(),
		change:   RowChange{Key: RowKey(values...), Deleted: patch.PatchType() == DeletePatchType},
	}
	if scoped, ok := patch.(interface{ patchDomain() accesstypes.Domain }); ok {
		row.domain = scoped.patchDomain()
	}
	match := rowMatchKey(key)
	for i, r := range t.rows {
		if r.resource == row.resource && t.matches[i] == match && r.domain == row.domain {
			t.rows[i].change.Deleted = row.change.Deleted

			return
		}
	}
	t.rows = append(t.rows, row)
	t.matches = append(t.matches, match)
}

// find reports whether a patch of the transaction wrote the row the given patch names,
// and whether the last such write deleted it. The row is matched by its resource and key
// alone: a row is one row whichever domain the patch that wrote it was decoded in.
func (t *touchedRows) find(patch PatchSetMetadata) (written, deleted bool) {
	key := patch.PrimaryKey()
	if key.Len() == 0 {
		return false, false
	}
	match := rowMatchKey(key)
	for i, row := range t.rows {
		if row.resource == patch.Resource() && t.matches[i] == match {
			written, deleted = true, row.change.Deleted
		}
	}

	return written, deleted
}

// list returns the recorded rows, in recording order.
func (t *touchedRows) list() []touchedRow {
	return slices.Clone(t.rows)
}

// patchDomain is the domain a PatchSet was decoded in: the tenant domain of the scope
// its permission enforcement was bound to, or the zero Domain for the global scope and
// for a patch built without enforcement (a body's own patch inside a method).
func (p *PatchSet[Resource]) patchDomain() accesstypes.Domain {
	domain, _ := p.querySet.scope.Domain()

	return domain
}

// TouchedRows collects what the transactions a request commits wrote, so the request
// publishes them once the commit lands and before it answers. CollectTouchedRows puts
// one in the context the generated handler runs its transaction under; the executor
// adds each committed transaction's rows, and the handler reads them back with Rows.
// Code that writes some other way tells it too (Touch, TouchTable). It is safe for
// concurrent use.
type TouchedRows struct {
	mu   sync.Mutex
	rows []touchedRow
	// tables are the tables touched whole (TouchTable), each once, in the order told.
	tables []accesstypes.Resource
}

// touchedRowsKey is the context key the collector rides under.
type touchedRowsKey struct{}

// CollectTouchedRows returns ctx carrying a fresh collector, and the collector. Every
// transaction committed under the returned context (ExecuteFunc, a patch's Apply)
// adds the rows it wrote; a transaction that does not commit adds nothing.
func CollectTouchedRows(ctx context.Context) (context.Context, *TouchedRows) {
	collector := &TouchedRows{}

	return context.WithValue(ctx, touchedRowsKey{}, collector), collector
}

// touchedRowsFrom returns the collector ctx carries, or nil.
func touchedRowsFrom(ctx context.Context) *TouchedRows {
	collector, _ := ctx.Value(touchedRowsKey{}).(*TouchedRows)

	return collector
}

// add appends a committed transaction's rows.
func (t *TouchedRows) add(rows []touchedRow) {
	if t == nil || len(rows) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rows = append(t.rows, rows...)
}

// Touch records a row written some other way than through the resource client (another
// library's store, a statement of the caller's own), so the publish tells its pages as it
// tells a patch's. The row is recorded in the zero domain, so Rows lists it under the
// fallback, as a patch built by hand is. A nil collector records nothing.
func (t *TouchedRows) Touch(res accesstypes.Resource, change RowChange) {
	t.add([]touchedRow{{resource: res, change: change}})
}

// TouchTable records that a table changed in rows the writer cannot name: every
// subscriber of the table and of every view that reads it is told, in every tenant, and
// refetches what it holds. A table is recorded once however often it is told. A nil
// collector records nothing.
func (t *TouchedRows) TouchTable(res accesstypes.Resource) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if slices.Contains(t.tables, res) {
		return
	}
	t.tables = append(t.tables, res)
}

// Tables returns the tables touched whole (TouchTable), each once, sorted by name: nil
// when none was, and for a nil collector.
func (t *TouchedRows) Tables() []accesstypes.Resource {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	tables := slices.Clone(t.tables)
	slices.Sort(tables)

	return tables
}

// Empty reports whether nothing was committed: no row and no table touched whole.
func (t *TouchedRows) Empty() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.rows) == 0 && len(t.tables) == 0
}

// Rows returns the committed rows grouped by the domain each was written in and then by
// resource, in first-written order within each resource. A row whose patch carried no
// domain (a global resource, or a patch built by hand) is listed under fallback: the
// mutating request's route domain, which is the zero Domain on a global route.
func (t *TouchedRows) Rows(fallback accesstypes.Domain) map[accesstypes.Domain]map[accesstypes.Resource][]RowChange {
	grouped := make(map[accesstypes.Domain]map[accesstypes.Resource][]RowChange)
	if t == nil {
		return grouped
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, row := range t.rows {
		domain := row.domain
		if domain == "" {
			domain = fallback
		}
		byResource, ok := grouped[domain]
		if !ok {
			byResource = make(map[accesstypes.Resource][]RowChange)
			grouped[domain] = byResource
		}
		byResource[row.resource] = append(byResource[row.resource], row.change)
	}

	return grouped
}

// collectTouched hands a committed transaction's rows to the collector ctx carries, if
// any. It runs after the commit, so nothing is published for a transaction that rolled
// back.
func collectTouched(ctx context.Context, rows []touchedRow) {
	if collector := touchedRowsFrom(ctx); collector != nil {
		collector.add(rows)
	}
}
