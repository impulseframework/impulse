package resource

import (
	"context"
	"slices"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// A column that names a row of another table (a foreign key, or a column picked from a
// table's list), or a row of a view (a column picked from a view's list), is set only to a
// row its writer can list. The picker of such a column lists its source under the
// person's own List grant, so it offers only those rows; the patch set holds a create and
// an update to the same rows, and answers any other as it answers a row that is not there.
// "Can list" is what the person's own list of that kind would show, asked in the scope
// that list is asked in: the grant on the kind, its condition, and the request's tenant
// for a kind that belongs to one. A view's row is always read, from the view's query,
// which the write is handed (NamedRowSources): which rows a view has is its query's to
// say, so the pass with no read is a table's alone. The rule is held
// here once, for every write an armed patch carries; a patch that is not armed (a job, a
// bootstrap) runs trusted and is not asked. A create is asked right after the grant's own
// check. An update and an update-or-create are asked once their row is found in the
// request's tenant and the write's conditions hold for it, so a caller the row's own checks
// refuse learns nothing from this one of a row they may not change. A row an earlier patch
// of the same request created is not asked where it is the request's tenant's: one request
// can carry the writes of several tenants, and a write of one names no row another's made.

// NamedRow is one column of a resource that names a row of another resource the
// application lists. The generator writes it from the column's picker (the generated
// NamedRows); it is a fact of the schema, so nothing an application writes changes it.
type NamedRow struct {
	// Field is the struct field that holds the named row's key.
	Field accesstypes.Field
	// Resource is the kind the column names a row of, Table its table and KeyColumn the
	// table's one key column.
	Resource  accesstypes.Resource
	Table     string
	KeyColumn string
	// TenantColumn and TenantPath say where the named row's tenant is read, as a
	// NameField's do; both are empty for a kind that belongs to no tenant.
	TenantColumn string
	TenantPath   []BindingHop
	// View says the column is picked from a view: Resource is the view's resource, Table
	// its resource's name, KeyColumn the view's one key column and TenantColumn the
	// view's own tenant column, empty for a view of the whole application. A view has no
	// TenantPath. The row is read from the view's query, which the write is handed
	// (NamedRowSources).
	View bool
	// Stored is the table the column's foreign key references, where the column is picked
	// from a view or a computed list: a row an earlier patch of the same request created
	// there passes as a table's does. Its Table, KeyColumn, TenantColumn and TenantPath
	// are read and nothing else of it. Nil where the column has no foreign key.
	Stored *NamedRow
}

// namedRower is the generated declaration of which columns of a resource name a row of
// another resource the application lists (NamedRows).
type namedRower interface {
	NamedRows() []NamedRow
}

// namedRowsNotImplemented is the refusal of the named-row check's read on a database its
// reads are not written for.
const namedRowsNotImplemented = "holding a row a write names to the rows its writer can list"

// grantScope is the scope the caller's List on the named kind is asked in, the scope a
// name of that kind is asked in (QuerySet.nameGrantScope): the request's own scope for a
// kind that belongs to a tenant, and the global scope, where its grants are held, for a
// kind of the whole application.
func (n *NamedRow) grantScope(request accesstypes.Scope) accesstypes.Scope {
	if n.TenantColumn == "" {
		return accesstypes.GlobalScope()
	}

	return request
}

// tenantTest holds the named row to the request's tenant: its tenant column, or the value
// at the end of the way it reaches its tenant by, equals the request's tenant, as a
// name's lookup holds its row (nameTenantTest). It is nil for a kind that belongs to no
// tenant. alias is the alias the named table is read under.
func (n *NamedRow) tenantTest(alias string, registry *paramRegistry) ExpressionNode {
	if n.TenantColumn == "" {
		return nil
	}
	tenant := columnComparand(alias, n.TenantColumn)
	if len(n.TenantPath) > 0 {
		tenant = pathScalar(&tenant, n.TenantPath, registry)
	}

	return &loweredComparisonNode{left: tenant, op: "=", right: namedComparand(domainParamName)}
}

// namedValue is one declared column a write sets, with the value it sets it to as held and
// as spelled (columnText).
type namedValue struct {
	row     *NamedRow
	value   any
	spelled string
}

// checkNamedRows holds every row the write names to the rows its caller can list: named,
// the values namedValues read before the server's own defaults were set, so it judges what
// the caller sent and never a value the server stamps. A create asks it right after the
// grant's own check; an update and an update-or-create once the write's conditions have
// held for the row (enforceWriteConditions), so the stored row is read only once it is
// found within the request's tenant and the caller may change it. A patch that is not
// armed sends nothing to judge, as it runs trusted. For each declared column the patch sets
// to a value that is not empty, a create's key parts among them, the caller's List on the
// named kind is asked in its scope (grantScope):
//
//   - granted, for a table that belongs to no tenant, or for the column the row's own way
//     to its tenant leaves through, whose row the tenancy term already holds to the
//     request's tenant: it passes, with no read. A view's row is always read;
//   - otherwise an update, or an update-or-create whose row is there, that leaves the
//     column naming the row it already names is not judged, and a row an earlier patch of
//     the same transaction created in the request's tenant passes, its create held to its
//     own checks (createdInTransaction): for a view, a row of the table the column's
//     foreign key references (Stored), and none where the declaration names no such table;
//   - denied: refused;
//   - granted: the row is read by key within the request's tenant (tenantTest), a view's
//     from the view's query;
//   - conditional: the row is read by key, within the tenant, under the grant's condition
//     lowered against the named table, or the view, as a name's lookup lowers it.
//
// A refusal, and a read that finds no row, is answered as a row that is not there:
// 404, "A record this one refers to does not exist." (referencedNotFound), so the answer
// says nothing of a row the caller cannot see. A mocked transaction holds no tables to
// ask, and a column whose judgment needs a read passes there.
func (p *PatchSet[Resource]) checkNamedRows(ctx context.Context, txn ReadWriteTransaction, named []namedValue) error {
	if len(named) == 0 {
		return nil
	}
	q := p.querySet
	decisions, err := p.namedRowDecisions(ctx, named)
	if err != nil {
		return err
	}

	for _, n := range named {
		decision := decisions[n.row.grantScope(q.scope)][n.row.Resource]
		if decision.IsGranted() && !n.row.View && (n.row.TenantColumn == "" || p.leavesForTenant(n.row.Field)) {
			continue
		}
		mocked := txn.DBType() == SpannerDBType && txn.SpannerReadOnlyTransaction() == nil
		if p.patchType != CreatePatchType {
			if mocked {
				continue
			}
			unchanged, err := p.namesStoredRow(ctx, txn, n)
			if err != nil {
				return err
			}
			if unchanged {
				continue
			}
		}
		created, err := p.createdInTransaction(ctx, txn, n)
		if err != nil {
			return err
		}
		if created {
			continue
		}
		if decision.IsDenied() {
			return referencedNotFound(p.Resource(), n.row.Table)
		}
		if txn.DBType() != SpannerDBType {
			return errors.Newf("%s is not implemented for %s", namedRowsNotImplemented, txn.DBType())
		}
		if mocked {
			continue
		}
		listed, err := p.listsNamedRow(ctx, txn, n, decision)
		if err != nil {
			return err
		}
		if !listed {
			return referencedNotFound(p.Resource(), n.row.Table)
		}
	}

	return nil
}

// namedValues are the declared columns the patch sets to a value that is not empty: the
// fields it sets, and a create's key parts. A patch that is not armed has none, as it runs
// trusted. The buffer functions read them right after the grant's own check, before the
// server's own defaults are set, and hand them to checkNamedRows where they ask it.
func (p *PatchSet[Resource]) namedValues() ([]namedValue, error) {
	q := p.querySet
	if q.resourceSet == nil || len(q.rMeta.namedRows) == 0 {
		return nil, nil
	}
	rows := q.rMeta.namedRows
	var named []namedValue
	for i := range rows {
		row := &rows[i]
		var value any
		switch {
		case p.IsSet(row.Field):
			value = p.Get(row.Field)
		case p.patchType == CreatePatchType && p.Key(row.Field) != nil:
			value = p.Key(row.Field)
		default:
			continue
		}
		spelled, err := columnText(value)
		if err != nil {
			return nil, errors.Wrapf(err, "field %s", row.Field)
		}
		if spelled != "" {
			named = append(named, namedValue{row: row, value: value, spelled: spelled})
		}
	}

	return named, nil
}

// namedRowDecisions asks the caller's List on each named kind, once per scope it is
// asked in, and answers the decisions by scope.
func (p *PatchSet[Resource]) namedRowDecisions(ctx context.Context, named []namedValue) (map[accesstypes.Scope]accesstypes.Decisions, error) {
	q := p.querySet
	var scopes []accesstypes.Scope
	kinds := make(map[accesstypes.Scope][]accesstypes.Resource)
	for _, n := range named {
		scope := n.row.grantScope(q.scope)
		if _, seen := kinds[scope]; !seen {
			scopes = append(scopes, scope)
		}
		if !slices.Contains(kinds[scope], n.row.Resource) {
			kinds[scope] = append(kinds[scope], n.row.Resource)
		}
	}
	decisions := make(map[accesstypes.Scope]accesstypes.Decisions, len(scopes))
	for _, scope := range scopes {
		answer, err := q.userPermissions.Check(ctx, q.env, scope, accesstypes.List, kinds[scope]...)
		if err != nil {
			return nil, errors.Wrap(err, "resource.UserPermissions.Check()")
		}
		decisions[scope] = answer
	}

	return decisions, nil
}

// storedRow is the row of a table a row the column names is created as: the named
// table's own, or, for a view, the table the column's foreign key references (Stored),
// nil where the declaration names none.
func (n *NamedRow) storedRow() *NamedRow {
	if !n.View {
		return n
	}

	return n.Stored
}

// leavesForTenant reports whether field is the one the row's own way to its tenant
// leaves through, a way of at least one hop: the tenancy term of the write's check holds
// the row it names to the request's tenant (mutationTenancy).
func (p *PatchSet[Resource]) leavesForTenant(field accesstypes.Field) bool {
	binding := p.querySet.rMeta.tenantBinding

	return binding != nil && len(binding.Path) > 0 && binding.Field == field
}

// namesStoredRow reports whether the row an update changes names the same row there now:
// what the transaction's earlier patches buffered for the row, else the stored row's. A
// row that is not there (an update-or-create that creates it) names none.
func (p *PatchSet[Resource]) namesStoredRow(ctx context.Context, txn ReadWriteTransaction, n namedValue) (bool, error) {
	stored, known := p.bufferedOwnColumn(txn, n.row.Field)
	if !known {
		var found bool
		var err error
		stored, found, err = p.storedRowValue(ctx, txn, n.row.Field)
		if err != nil {
			return false, err
		}
		if !found {
			return false, nil
		}
	}
	spelled, err := columnText(stored)
	if err != nil {
		return false, errors.Wrapf(err, "field %s", n.row.Field)
	}

	return spelled == n.spelled, nil
}

// createdInTransaction reports whether an earlier patch of the transaction created the
// named row (transactionRows.namedWrite) in the request's tenant: a transaction that keeps
// no record of its rows (the Mock's, PostgreSQL) created none that a read would not see.
// A column picked from a view names a row of the table its foreign key references
// (storedRow), and one that declares none names no row a patch created.
// For a kind that belongs to a tenant, the created row's tenant column, as its image holds
// it, is followed along the kind's way to its tenant by the transaction's point reads
// (followTenantPath), which see what the request buffered, and must spell the request's
// tenant: one request can carry the writes of several tenants. A kind that belongs to no
// tenant, and a request of the whole application, which writes for no one tenant, pass a
// created row as it is.
func (p *PatchSet[Resource]) createdInTransaction(ctx context.Context, txn ReadWriteTransaction, n namedValue) (bool, error) {
	stored := n.row.storedRow()
	if stored == nil {
		return false, nil
	}
	recorder, ok := txn.(rowsRecorder)
	if !ok {
		return false, nil
	}
	rows := recorder.transactionRows()
	write, named := rows.namedWrite(pointRead{table: stored.Table, keyColumn: stored.KeyColumn, value: n.spelled})
	if !named {
		return false, nil
	}
	image, ok := rows.image(stored.Table, write.row)
	if !ok || image.state != rowCreated {
		return false, nil
	}
	domain, partitioned := p.querySet.scope.Domain()
	if stored.TenantColumn == "" || !partitioned {
		return true, nil
	}
	tenant, _, err := followTenantPath(ctx, transactionPointReader(txn, namedRowsNotImplemented), n.row.Field, image.columns[stored.TenantColumn], stored.TenantPath)
	if err != nil {
		return false, errors.Wrapf(err, "the tenant of the %s row a write of %s names", stored.Table, p.Resource())
	}

	return tenant == string(domain), nil
}

// listsNamedRow reads the named row by key as the caller's list of its kind would read
// it: within the request's tenant for a kind that belongs to one, and under the grant's
// condition where the decision carries one. A view's row is read from the view's query,
// under the view's resource's name (namedViewItem). It answers whether there is such a
// row.
func (p *PatchSet[Resource]) listsNamedRow(ctx context.Context, txn ReadWriteTransaction, n namedValue, decision accesstypes.Decision) (bool, error) {
	registry := newParamRegistry()
	alias := registry.alias()
	if n.row.View {
		alias = string(n.row.Resource)
	}
	terms := []ExpressionNode{&loweredComparisonNode{left: columnComparand(alias, n.row.KeyColumn), op: "=", right: valueComparand(n.value)}}
	if test := n.row.tenantTest(alias, registry); test != nil {
		terms = append(terms, test)
	}
	if decision.IsConditional() {
		held, err := p.namedRowCondition(n.row, decision, alias, registry)
		if err != nil {
			return false, err
		}
		if held != nil {
			terms = append(terms, held)
		}
	}
	gen := newSQLGenerator(Spanner)
	where, err := gen.generateLowered(andChain(terms...), registry)
	if err != nil {
		return false, errors.Wrapf(err, "the read of %s a write of %s names", n.row.Table, p.Resource())
	}
	params := make(map[string]any)
	if err := p.mergeCheckParams(registry, params); err != nil {
		return false, err
	}
	withClause, from := "", gen.quoteIdentifier(n.row.Table)+" "+gen.quoteIdentifier(alias)
	if n.row.View {
		withClause, from, err = p.namedViewItem(n.row, params)
		if err != nil {
			return false, err
		}
	}
	stmt := spanner.Statement{
		SQL:    withClause + "SELECT 1 FROM " + from + " WHERE " + where,
		Params: params,
	}
	_, found, err := spannerFirstRow(ctx, txn.SpannerReadOnlyTransaction(), stmt)
	if err != nil {
		return false, errors.Wrapf(err, "reading the %s row a write of %s names", n.row.Table, p.Resource())
	}

	return found, nil
}

// namedRowCondition lowers the condition the caller's List on the named kind is held
// under against the named table read under alias, in that kind's own vocabulary, as a
// name's lookup lowers it (QuerySet.nameGrantCondition). A condition that comes to true
// adds nothing, and answers nil.
func (p *PatchSet[Resource]) namedRowCondition(row *NamedRow, decision accesstypes.Decision, alias string, registry *paramRegistry) (ExpressionNode, error) {
	q := p.querySet
	expr, err := conditionalExpr(row.Resource, decision)
	if err != nil {
		return nil, err
	}
	expr = condition.WithoutPostImage(expr)
	if t, ok := expr.(condition.Truth); ok && t.Value {
		return nil, nil
	}
	if q.collection == nil {
		return nil, errors.Newf("resource %s names a row of %s, on which the caller's List is conditional, but no generated collection is wired to render the condition", q.Resource(), row.Resource)
	}
	bindings, _ := q.collection.Bindings(q.collection.Scope(row.Resource), row.Resource)
	_, partitioned := row.grantScope(q.scope).Domain()
	lctx := &loweringContext{outer: alias, bindings: bindings, collection: q.collection, partitioned: partitioned}
	held, err := lowerRowCondition(expr, lctx, registry)
	if err != nil {
		return nil, errors.Wrapf(err, "lowering the condition on %s for the row %s names", row.Resource, q.Resource())
	}

	return &GroupNode{Expression: held}, nil
}
