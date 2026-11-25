package resource

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"google.golang.org/api/iterator"
)

// tenantRows is one tenant's rows of a kind, as a statement counts a kind's rows by their
// tenant (takenQuery.byTenant): the tenant's key read as text, NULL for rows of no
// tenant, and the rows.
type tenantRows struct {
	tenant spanner.NullString
	rows   int64
}

// byTenant is the count of the kind's rows at the level, below the deepest level, by
// their tenant: the rows under the condition the level's count is written with
// (condition), each tenant read as text as the roster reads a tenant's key, one row of
// the answer for each tenant and one, NULL, for the rows of no tenant. ok is false where
// the kind is not counted there, and for a kind whose rows are no tenant's.
func (q *takenQuery) byTenant(kind *DeleteKind, level, deepest int) (rowsUnderStatement, bool) {
	if kind.Tenant == nil {
		return rowsUnderStatement{}, false
	}
	w := &rowsUnderWriter{dbType: SpannerDBType}
	condition, ok := q.condition(w, kind, level, deepest)
	if !ok {
		return rowsUnderStatement{}, false
	}
	outer := w.quote(string(kind.Resource))
	tenant := w.rowTenant(outer+".", kind.Tenant)
	sql := fmt.Sprintf("SELECT tenant, COUNT(*) FROM (SELECT CAST(%s AS %s) AS tenant FROM %s WHERE %s) GROUP BY tenant",
		tenant, columnTypeString, outer, condition)

	return rowsUnderStatement{SQL: sql, Args: w.args}, true
}

// countByTenant runs one statement that counts a kind's rows by their tenant inside the
// transaction (takenQuery.byTenant).
func (c *SpannerReadWriteTransaction) countByTenant(ctx context.Context, statement rowsUnderStatement) ([]tenantRows, error) {
	it := c.txn.Query(ctx, statement.spannerStatement())
	defer it.Stop()

	var counted []tenantRows
	for {
		row, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return counted, nil
		}
		if err != nil {
			return nil, errors.Wrap(err, "spanner.RowIterator.Next()")
		}
		var tenant tenantRows
		if err := row.Columns(&tenant.tenant, &tenant.rows); err != nil {
			return nil, errors.Wrap(err, "spanner.Row.Columns()")
		}
		counted = append(counted, tenant)
	}
}

// tenantName reads the name of the tenant whose key, read as text, is the one given,
// inside the transaction: the Name column of the row of the tenants table the
// description names (DeleteTree.Tenants). It answers "" where there is no name to show:
// a description that names no tenants or no name column, no such row, and a name that is
// NULL or empty. The key goes into the statement as a value.
func (c *SpannerReadWriteTransaction) tenantName(ctx context.Context, tenants *DeleteTenants, key string) (string, error) {
	if tenants == nil || tenants.Name == "" {
		return "", nil
	}
	w := &rowsUnderWriter{dbType: SpannerDBType}
	statement := rowsUnderStatement{
		SQL: fmt.Sprintf("SELECT CAST(%s AS %s) FROM %s WHERE CAST(%s AS %s) = %s",
			w.quote(tenants.Name), columnTypeString, w.quote(tenants.Table), w.quote(tenants.Key), columnTypeString, w.value(key)),
	}
	statement.Args = w.args
	it := c.txn.Query(ctx, statement.spannerStatement())
	defer it.Stop()

	row, err := it.Next()
	if errors.Is(err, iterator.Done) {
		return "", nil
	}
	if err != nil {
		return "", errors.Wrap(err, "spanner.RowIterator.Next()")
	}
	var name spanner.NullString
	if err := row.Columns(&name); err != nil {
		return "", errors.Wrap(err, "spanner.Row.Columns()")
	}

	return name.StringVal, nil
}

// refusedBeyondReach refuses the delete of a record with rows of a kind marked
// WithinReach (DeleteKind.WithinReach) that the permission the request was let in by
// does not reach, before any row is asked about and on a request that asks as on one
// that takes. Whoever the delete is for is the asked caller: with none, or one whose
// scope is the whole application's, every row is within reach and no statement is sent.
// Otherwise the rows of the caller's tenant are within reach, and those of each other
// tenant in which the same permission holds (Caller.reachesTenant), each such tenant
// asked once, in the order of its key. Each marked kind with rows is counted by tenant,
// level by level, in the question's order; a kind whose rows are no tenant's has every
// row of no tenant and sends no statement. A row of a tenant out of reach refuses in the
// sentence for the first such tenant, its key read as text, counting each marked kind's
// rows in it; with none, a row of no tenant refuses in the sentence for those.
func (d *takingDelete) refusedBeyondReach(ctx context.Context, levels *takenLevels) error {
	caller := d.asked.caller
	if caller.IsZero() || caller.Scope.IsGlobal() {
		return nil
	}
	own, _ := caller.Scope.Domain()

	tree := d.query.tree
	beyond := make(map[string][]kindRows)
	var none []kindRows
	for i := range slices.Backward(tree.Taken) {
		kind := &tree.Taken[i]
		if !kind.WithinReach || kind.Trusted {
			continue
		}
		total := levels.total(kind)
		if total == 0 {
			continue
		}
		if kind.Tenant == nil {
			none = append(none, kindRows{kind: kind, rows: total})

			continue
		}
		inTenant, ofNone, err := d.kindByTenant(ctx, kind, levels, own)
		if err != nil {
			return err
		}
		for tenant, rows := range inTenant {
			beyond[tenant] = append(beyond[tenant], kindRows{kind: kind, rows: rows})
		}
		if ofNone > 0 {
			none = append(none, kindRows{kind: kind, rows: ofNone})
		}
	}

	for _, tenant := range slices.Sorted(maps.Keys(beyond)) {
		reached, err := caller.reachesTenant(ctx, accesstypes.Domain(tenant))
		if err != nil {
			return errors.Wrapf(err, "asking whether the delete of a %s reaches tenant %s", tree.Words.One, tenant)
		}
		if reached {
			delete(beyond, tenant)
		}
	}
	if len(beyond) > 0 {
		first := slices.Min(slices.Collect(maps.Keys(beyond)))
		name, err := d.txn.tenantName(ctx, tree.Tenants, first)
		if err != nil {
			return errors.Wrapf(err, "reading the name of the tenant of the %s under a %s", beyond[first][0].kind.Words.Many, tree.Words.One)
		}

		return httpio.NewConflictMessage(beyondReachSentence(tree, beyond[first], name))
	}
	if len(none) > 0 {
		return httpio.NewConflictMessage(noTenantSentence(tree, none))
	}

	return nil
}

// kindByTenant counts a marked kind's rows by their tenant, level by level: the rows of
// each tenant but the caller's own (own), and the rows of no tenant.
func (d *takingDelete) kindByTenant(ctx context.Context, kind *DeleteKind, levels *takenLevels, own accesstypes.Domain) (inTenant map[string]int64, ofNone int64, err error) {
	inTenant = make(map[string]int64)
	for level := 0; level <= levels.deepest; level++ {
		if levels.at(kind, level) == 0 {
			continue
		}
		statement, ok := d.query.byTenant(kind, level, levels.deepest)
		if !ok {
			continue
		}
		counted, err := d.countByTenant(ctx, statement)
		if err != nil {
			return nil, 0, errors.Wrapf(err, "counting the %s under a %s by their tenant", kind.Words.Many, d.query.tree.Words.One)
		}
		for _, tenant := range counted {
			switch {
			case !tenant.tenant.Valid:
				ofNone += tenant.rows
			case tenant.tenant.StringVal != string(own):
				inTenant[tenant.tenant.StringVal] += tenant.rows
			}
		}
	}

	return inTenant, ofNone, nil
}

// countByTenant runs a statement that counts a kind's rows by their tenant: through the
// delete's own reader where it has one, and inside the transaction otherwise.
func (d *takingDelete) countByTenant(ctx context.Context, statement rowsUnderStatement) ([]tenantRows, error) {
	if d.byTenant != nil {
		return d.byTenant(ctx, statement)
	}

	return d.txn.countByTenant(ctx, statement)
}

// beyondReachSentence is the sentence a person reads when a delete is refused over rows
// in a tenant the permission it was let in by does not reach: "This account still has 2
// task assignments in Borealis. They have to be removed there before the account can be
// deleted." name is the tenant's name, "" where there is none to show ("in another
// company"). Every count is told, whatever the person may list.
func beyondReachSentence(tree *DeleteTree, rows []kindRows, name string) string {
	where := name
	if where == "" {
		where = "another " + tree.tenantWord()
	}
	counts, total := reachCounts(rows)
	removed := "They have"
	if total == 1 {
		removed = "It has"
	}

	return fmt.Sprintf("%s still has %s in %s. %s to be removed there before the %s can be deleted.",
		tree.RowName(), joinCounts(counts), where, removed, tree.Words.One)
}

// noTenantSentence is the sentence a person reads when a delete is refused over rows of
// no tenant, which only a permission of the whole application's reaches: "This account
// still has 3 saved filters that are not part of any company. Only someone who manages
// accounts for the whole application can delete it."
func noTenantSentence(tree *DeleteTree, rows []kindRows) string {
	counts, total := reachCounts(rows)
	are := "are"
	if total == 1 {
		are = "is"
	}

	return fmt.Sprintf("%s still has %s that %s not part of any %s. Only someone who manages %s for the whole application can delete it.",
		tree.RowName(), joinCounts(counts), are, tree.tenantWord(), tree.Words.Many)
}

// reachCounts are the kinds' counts as a sentence says them, and their rows in all.
func reachCounts(rows []kindRows) (counts []string, total int64) {
	counts = make([]string, 0, len(rows))
	for _, k := range rows {
		counts = append(counts, k.kind.Words.Count(int(k.rows)))
		total += k.rows
	}

	return counts, total
}

// tenantWord is what a sentence about the record's delete calls one tenant: the word
// the description names (DeleteTree.Tenants), and "tenant" where it names none.
func (t *DeleteTree) tenantWord() string {
	if t.Tenants == nil || t.Tenants.Word == "" {
		return "tenant"
	}

	return t.Tenants.Word
}
