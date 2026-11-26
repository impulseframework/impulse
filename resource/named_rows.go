package resource

import (
	"context"
	"slices"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/spxscan/spxapi"
)

// A column that names a row of another table (a foreign key, or a column picked from a
// table's list), of a view or of a computed list (a column picked from the view's or the
// list's own list), is set only to a row its writer can list. The picker of such a column
// lists its source under the person's own List grant, so it offers only those rows; the
// patch set holds a create and an update to the same rows, and answers any other as it
// answers a row that is not there. "Can list" is what the person's own list of that kind
// would show, asked in the scope that list is asked in: the grant on the kind, its
// condition, and the request's tenant for a kind that belongs to one. A view's row is
// always read, from the view's query, and a computed list always asked, through its own
// function, each of which the write is handed (NamedRowSources): which rows a view or a
// list has is its query's or its function's to say, so the pass with no read is a
// table's alone. The rule is held
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
	// Computed says the column is picked from a computed list: Resource is the list's
	// resource, and Table, KeyColumn, TenantColumn and TenantPath are empty, since its
	// rows are made in code. The list is asked through its own function, which the
	// write is handed (NamedRowSources).
	Computed bool
	// GrantInTenant says the List grant on Resource is asked in the request's tenant
	// although no TenantColumn says so: a computed list of a tenant.
	GrantInTenant bool
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
// kind that belongs to a tenant, a computed list of a tenant among them (GrantInTenant),
// and the global scope, where its grants are held, for a kind of the whole application.
func (n *NamedRow) grantScope(request accesstypes.Scope) accesstypes.Scope {
	if n.TenantColumn == "" && !n.GrantInTenant {
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
//     request's tenant: it passes, with no read. A view's row is always read, and a
//     computed list always asked;
//   - otherwise an update, or an update-or-create whose row is there, that leaves the
//     column naming the row it already names is not judged, and a row an earlier patch of
//     the same transaction created in the request's tenant passes, its create held to its
//     own checks (createdInTransaction): for a view or a computed list, a row of the table
//     the column's foreign key references (Stored), and none where the declaration names
//     no such table;
//   - denied: refused;
//   - granted: the row is read by key within the request's tenant (tenantTest), a view's
//     from the view's query;
//   - conditional: the row is read by key, within the tenant, under the grant's condition
//     lowered against the named table, or the view, as a name's lookup lowers it;
//   - a computed list, granted or conditional: the list's function is run as the
//     caller's list of it runs, and its rows the caller is shown are read for the key
//     (listsComputedRow). The library reads nothing itself there, so no database is
//     refused.
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
	caller := q.Caller()
	decisions, err := listDecisions(ctx, caller, named)
	if err != nil {
		return err
	}

	ask := &listedAsk{caller: caller, reader: txn, owner: p.Resource()}
	for _, n := range named {
		decision := decisions[n.row.grantScope(q.scope)][n.row.Resource]
		if decision.IsGranted() && n.row.fromTable() && (n.row.TenantColumn == "" || p.leavesForTenant(n.row.Field)) {
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
			return referencedNotFound(p.Resource(), n.row.lookedIn())
		}
		if txn.DBType() != SpannerDBType && !n.row.Computed {
			return errors.Newf("%s is not implemented for %s", namedRowsNotImplemented, txn.DBType())
		}
		if mocked {
			continue
		}
		listed, err := ask.listsRow(ctx, n, decision)
		if err != nil {
			return err
		}
		if !listed {
			return referencedNotFound(p.Resource(), n.row.lookedIn())
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

// listDecisions asks the caller's List on each kind the values name, once for each scope
// it is asked in (grantScope over the caller's scope), in the caller's environment, and
// answers the decisions by scope.
func listDecisions(ctx context.Context, caller *Caller, named []namedValue) (map[accesstypes.Scope]accesstypes.Decisions, error) {
	var scopes []accesstypes.Scope
	kinds := make(map[accesstypes.Scope][]accesstypes.Resource)
	for _, n := range named {
		scope := n.row.grantScope(caller.Scope)
		if _, seen := kinds[scope]; !seen {
			scopes = append(scopes, scope)
		}
		if !slices.Contains(kinds[scope], n.row.Resource) {
			kinds[scope] = append(kinds[scope], n.row.Resource)
		}
	}
	decisions := make(map[accesstypes.Scope]accesstypes.Decisions, len(scopes))
	for _, scope := range scopes {
		answer, err := caller.Permissions.Check(ctx, caller.Env, scope, accesstypes.List, kinds[scope]...)
		if err != nil {
			return nil, errors.Wrap(err, "resource.UserPermissions.Check()")
		}
		decisions[scope] = answer
	}

	return decisions, nil
}

// fromTable reports whether the column names a row of a table: not one of a view, and
// not one of a computed list.
func (n *NamedRow) fromTable() bool {
	return !n.View && !n.Computed
}

// storedRow is the row of a table a row the column names is created as: the named
// table's own, or, for a view or a computed list, the table the column's foreign key
// references (Stored), nil where the declaration names none.
func (n *NamedRow) storedRow() *NamedRow {
	if n.fromTable() {
		return n
	}

	return n.Stored
}

// lookedIn is what a refusal's cause says the row was looked for in: the table or the
// view the row is read from, or the computed list asked for it.
func (n *NamedRow) lookedIn() string {
	if n.Computed {
		return string(n.Resource)
	}

	return n.Table
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
// A column picked from a view or a computed list names a row of the table its foreign
// key references (storedRow), and one that declares none names no row a patch created.
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

// namedRowReader is what a named row is read through: the transaction of the write that
// names it, or the client a method's request is held with (HoldPicked). A reader whose
// database is Spanner and that holds no Spanner reader is a mock's: it holds no tables to
// ask, and a row whose judgment needs a read passes there.
type namedRowReader interface {
	DBType() DBType
	SpannerReadOnlyTransaction() spxapi.Querier
}

// listedAsk is what the question whether the caller's list of a kind shows a row is asked
// over, once the caller's List on the kind is decided: the caller (its checker, scope,
// environment, collection and named-row sources), the reader the row is read through, and
// the resource whose write names the row, or the method whose request does (method).
// A write's check (checkNamedRows) asks it with the query set's caller and the write's
// transaction, a method's (HoldPicked) with the method's caller and the client; the
// statements it writes are the same.
type listedAsk struct {
	caller *Caller
	reader namedRowReader
	owner  accesstypes.Resource
	method bool
}

// naming is what the errors say names the row: a write of the resource, or a request of
// the method.
func (a *listedAsk) naming() string {
	if a.method {
		return "a request of method " + string(a.owner)
	}

	return "a write of " + string(a.owner)
}

// listsRow answers whether the caller's list of the kind shows the row the value names,
// the caller's List on it having answered decision (granted or conditional): a computed
// list is asked through its own function (listsComputedRow), every other row is read
// (listsNamedRow).
func (a *listedAsk) listsRow(ctx context.Context, n namedValue, decision accesstypes.Decision) (bool, error) {
	if n.row.Computed {
		return a.listsComputedRow(ctx, n, decision)
	}

	return a.listsNamedRow(ctx, n, decision)
}

// listsNamedRow reads the named row by key as the caller's list of its kind would read
// it: within the request's tenant for a kind that belongs to one, and under the grant's
// condition where the decision carries one. A view's row is read from the view's query,
// under the view's resource's name (viewItem). It answers whether there is such a row.
func (a *listedAsk) listsNamedRow(ctx context.Context, n namedValue, decision accesstypes.Decision) (bool, error) {
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
		held, err := a.namedRowCondition(n.row, decision, alias, registry)
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
		return false, errors.Wrapf(err, "the read of %s %s names", n.row.Table, a.naming())
	}
	params := make(map[string]any)
	if err := a.bindParams(registry, params); err != nil {
		return false, err
	}
	withClause, from := "", gen.quoteIdentifier(n.row.Table)+" "+gen.quoteIdentifier(alias)
	if n.row.View {
		withClause, from, err = a.viewItem(n.row, params)
		if err != nil {
			return false, err
		}
	}
	stmt := spanner.Statement{
		SQL:    withClause + "SELECT 1 FROM " + from + " WHERE " + where,
		Params: params,
	}
	_, found, err := spannerFirstRow(ctx, a.reader.SpannerReadOnlyTransaction(), stmt)
	if err != nil {
		return false, errors.Wrapf(err, "reading the %s row %s names", n.row.Table, a.naming())
	}

	return found, nil
}

// bindParams folds the read's bound values and the reserved parameters it references
// into params: the caller's user, the decision instant of its environment and its
// tenant, the values the caller's own check folded with.
func (a *listedAsk) bindParams(registry *paramRegistry, params map[string]any) error {
	for _, param := range registry.boundParams() {
		if _, ok := params[param.Name]; ok {
			return errors.Newf("named parameter collision: the read of the row %s names already contains named parameter %q", a.naming(), param.Name)
		}
		params[param.Name] = param.Value
	}
	for _, name := range registry.referencedNames() {
		value, err := a.caller.paramValue(name)
		if err != nil {
			return err
		}
		params[name] = value
	}

	return nil
}

// paramValue supplies a reserved parameter a statement asked as the caller references:
// the same values the caller's entry check folded with, never sampled again.
func (c *Caller) paramValue(name string) (any, error) {
	switch name {
	case subjectParamName:
		return string(c.Permissions.User()), nil
	case nowParamName:
		now, ok := c.Env.Now()
		if !ok {
			return nil, errors.New("condition references now but the request environment carries no decision instant")
		}

		return now, nil
	case domainParamName:
		domain, ok := c.Scope.Domain()
		if !ok {
			return nil, errors.New("condition rendering referenced the request partition in a global request")
		}

		return string(domain), nil
	default:
		return nil, errors.Newf("condition rendering referenced unknown named parameter %q", name)
	}
}

// namedRowCondition lowers the condition the caller's List on the named kind is held
// under against the named table read under alias, in that kind's own vocabulary, as a
// name's lookup lowers it (QuerySet.nameGrantCondition). A condition that comes to true
// adds nothing, and answers nil.
func (a *listedAsk) namedRowCondition(row *NamedRow, decision accesstypes.Decision, alias string, registry *paramRegistry) (ExpressionNode, error) {
	expr, err := conditionalExpr(row.Resource, decision)
	if err != nil {
		return nil, err
	}
	expr = condition.WithoutPostImage(expr)
	if t, ok := expr.(condition.Truth); ok && t.Value {
		return nil, nil
	}
	collection := a.caller.collection
	if collection == nil {
		return nil, errors.Newf("%s names a row of %s, on which the caller's List is conditional, but no generated collection is wired to render the condition", a.naming(), row.Resource)
	}
	bindings, _ := collection.Bindings(collection.Scope(row.Resource), row.Resource)
	_, partitioned := row.grantScope(a.caller.Scope).Domain()
	lctx := &loweringContext{outer: alias, bindings: bindings, collection: collection, partitioned: partitioned}
	held, err := lowerRowCondition(expr, lctx, registry)
	if err != nil {
		return nil, errors.Wrapf(err, "lowering the condition on %s for the row %s names", row.Resource, a.naming())
	}

	return &GroupNode{Expression: held}, nil
}
