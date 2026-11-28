package resource

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// A delete of a record with rows under it is refused before the commit, in a sentence
// that says what is in the way: "This ship has 3 sorties. Delete them first.", or "This
// squadron has 3 missions. Move or delete them first." where the rows can be taken from
// under the record without being deleted (DeleteLink.MayBeEmpty). The record's
// description (DeleteTree) names the kinds to count: the kinds its @deleteCascade lists
// under refusedBy, and the kinds with a row directly under the record that the database
// does not remove with it (Under). A record that declares its delete is judged earlier,
// as the delete is buffered (deleteTaken), where a row of a refusedBy kind must be gone
// before the record in the transaction; the count here is for a record that declares
// none, and for a declared one that deleteTaken leaves to it. The count runs once the
// transaction's function has returned, over every delete it buffered, and reads what the
// database held before the transaction: a read inside a transaction does not see its own
// buffered writes. Rows that go with a record are declared (@deleteCascade); rows that
// are not declared are deleted in an earlier transaction. What the count does not reach
// (a row under a foreign key the generator does not follow) is left to the commit, which
// answers in its own sentence (referentialMessage).

// deleteRecorder is how the patch machinery reaches a transaction's record of the
// deletes to count over: the Spanner wrapper satisfies it. A transaction that does not
// (the Mock's, which has no rows to count; Postgres) records nothing, and no delete on it
// is counted over.
type deleteRecorder interface {
	recordDelete(d recordedDelete)
}

// recordedDelete is one buffered delete of a record whose description names kinds to
// count: the description, the record's key by column, and who the delete is for.
type recordedDelete struct {
	tree *DeleteTree
	key  map[string]any
	// caller is the person the sentence is written for, a zero Caller for a trusted
	// call, which is told every count.
	caller *Caller
}

// transactionDeletes is the record a write transaction keeps of the deletes to count
// over, in buffered order. Like the released-keys record it belongs to one attempt: a
// retried transaction runs its function again over a fresh record.
type transactionDeletes struct {
	deletes []recordedDelete
}

// newTransactionDeletes returns an empty record.
func newTransactionDeletes() *transactionDeletes {
	return &transactionDeletes{}
}

// recordDelete notes a delete to count over.
func (t *transactionDeletes) recordDelete(d recordedDelete) {
	t.deletes = append(t.deletes, d)
}

// keyByColumn is the patch's primary key by column name. ok is false when a key field
// has no column on the database type, and the row is then not recorded.
func (p *PatchSet[Resource]) keyByColumn(dbType DBType) (key map[string]any, ok bool) {
	fields := p.querySet.rMeta.dbFieldMap(dbType)
	parts := p.PrimaryKey().Parts()
	key = make(map[string]any, len(parts))
	for _, part := range parts {
		field, found := fields[part.Key]
		if !found {
			return nil, false
		}
		key[field.ColumnName] = part.Value
	}

	return key, len(key) > 0
}

// recordDelete notes the patch's delete on the transaction, where the record's
// description names kinds to count, with who it is for. The person is the one the patch is armed against,
// else the caller the request's frame stamped (CallerFrom), else nobody: a trusted
// call.
func (p *PatchSet[Resource]) recordDelete(ctx context.Context, txn ReadWriteTransaction) {
	recorder, ok := txn.(deleteRecorder)
	if !ok {
		return
	}
	// A delete that answered for the rows under its record when it was buffered has
	// nothing left to count (PatchSet.underAnswered).
	tree := p.deleteDescription()
	if tree == nil || p.underAnswered || len(tree.RefusedBy)+len(tree.Under) == 0 {
		return
	}
	key, ok := p.keyByColumn(txn.DBType())
	if !ok {
		return
	}
	recorder.recordDelete(recordedDelete{tree: tree, key: key, caller: p.deleteCaller(ctx)})
}

// deleteCaller is the person a delete is for: the one the patch is armed against, else
// the caller the request's frame stamped (CallerFrom), else nobody, a trusted call.
func (p *PatchSet[Resource]) deleteCaller(ctx context.Context) *Caller {
	caller := p.querySet.Caller()
	if caller.IsZero() {
		caller = CallerFrom(ctx)
	}

	return caller
}

// rowsUnderStatement is a statement about one kind's rows under a record: its text, and
// its values in the order the text names them (@d1, @d2 on Spanner; $1, $2 on
// PostgreSQL). Named holds the values of a check's conditions the text names by name
// (a page of a kind's rows read with their answers; Spanner alone).
type rowsUnderStatement struct {
	SQL   string
	Args  []any
	Named map[string]any
}

// spannerStatement is the statement as the Spanner client takes it, each value under
// the name the text gives it.
func (s rowsUnderStatement) spannerStatement() spanner.Statement {
	params := make(map[string]any, len(s.Args)+len(s.Named))
	for name, value := range s.Named {
		params[name] = value
	}
	for i, arg := range s.Args {
		params["d"+strconv.Itoa(i+1)] = arg
	}

	return spanner.Statement{SQL: s.SQL, Params: params}
}

// rowsUnderWriter renders one statement about a kind's rows for a database type.
type rowsUnderWriter struct {
	dbType DBType
	args   []any
	// steps counts the steps written of rows' ways to their tenants, so each reads its
	// table under a name of its own.
	steps int
	// ways counts the ways up to the record written, so the rows between on each are
	// read under names of their own.
	ways int
	// page is the page whose keys a way started from (wayFromPage), nil where none did:
	// its keys are bound under pageName (named).
	page *takenUnder
	// given are the values the statement names by name besides a page's keys (the values
	// a page's terms name, takenAsks.params).
	given map[string]any
}

// pageIDs is the name a statement binds a page's keys under (wayFromPage), where the
// statement's other named values do not hold it (pageName).
const pageIDs = "ids"

// quote spells an identifier as the database type quotes it.
func (w *rowsUnderWriter) quote(identifier string) string {
	if w.dbType == PostgresDBType {
		return postgresIdentifier(identifier)
	}

	return newSQLGenerator(Spanner).quoteIdentifier(identifier)
}

// value adds a value to the statement and returns the placeholder that names it.
func (w *rowsUnderWriter) value(v any) string {
	w.args = append(w.args, v)
	if w.dbType == PostgresDBType {
		return "$" + strconv.Itoa(len(w.args))
	}

	return "@d" + strconv.Itoa(len(w.args))
}

// rowTenant is the tenant of a row as a statement reads it: the column that holds the
// tenant's key, spelled after the prefix, or, for a row that reaches its tenant through
// other rows, one scalar subquery for each step of the way. It is NULL where a step's
// column is empty or names no row, and NULL equals no tenant.
func (w *rowsUnderWriter) rowTenant(prefix string, binding *DomainBindingData) string {
	tenant := prefix + w.quote(binding.Column)
	for _, hop := range binding.Path {
		w.steps++
		alias := "n" + strconv.Itoa(w.steps)
		tenant = fmt.Sprintf("(SELECT %[1]s.%[2]s FROM %[3]s AS %[1]s WHERE %[1]s.%[4]s = %[5]s)",
			alias, w.quote(hop.Column), w.quote(hop.Table), w.quote(hop.JoinColumn), tenant)
	}

	return tenant
}

// ofRecordsTenant is the condition that a row of the kind, its columns spelled after
// the prefix, is of the record's tenant, as " AND ..." after the conditions that put the
// row under the record. The record's tenant is the value its key holds where the key
// holds the tenant's column (the tenant record itself, or a record keyed by its
// tenant), and is read from the record's row by its key otherwise. It is empty where
// the kind's rows or the record are no tenant's: every row is then under the record
// that the links put there.
func (w *rowsUnderWriter) ofRecordsTenant(prefix string, kind *DeleteKind, tree *DeleteTree, recordKey map[string]any) string {
	if tree == nil || tree.Tenant == nil || kind.Tenant == nil {
		return ""
	}
	row := w.rowTenant(prefix, kind.Tenant)
	if value, held := heldTenant(tree, recordKey); held {
		return fmt.Sprintf(" AND %s = %s", row, w.value(value))
	}
	equal := make([]string, 0, len(recordKey))
	for _, column := range slices.Sorted(maps.Keys(recordKey)) {
		equal = append(equal, fmt.Sprintf("tr.%s = %s", w.quote(column), w.value(recordKey[column])))
	}

	return fmt.Sprintf(" AND %s = (SELECT %s FROM %s AS tr WHERE %s)",
		row, w.rowTenant("tr.", tree.Tenant), w.quote(string(tree.Record)), strings.Join(equal, " AND "))
}

// heldTenant is the tenant the record's key holds: the value of the column that holds
// the tenant's key, where the record's rows are a tenant's by a column of their own and
// the key holds that column (the tenant record itself, or a record keyed by its tenant).
// held is false where the record is no tenant's, reaches its tenant through other rows,
// or is keyed without the column.
func heldTenant(tree *DeleteTree, recordKey map[string]any) (value any, held bool) {
	if tree == nil || tree.Tenant == nil || len(tree.Tenant.Path) != 0 {
		return nil, false
	}
	value, held = recordKey[tree.Tenant.Column]

	return value, held
}

// tenantsWay is the first of the ways, in their order, that is the kind's own way to its
// tenant, under a record whose key holds its tenant (heldTenant): the way that leaves
// the kind's row by the column its tenant is found by, follows each step of the way to
// the tenant (DomainBindingData.Path) column for column, and ends on the record's column
// that holds the tenant's key. Under such a record that way's rows are every row of the
// kind in the record's tenant, each of them under the record, so the kind's other ways
// find none it does not, and the way needs no tenant's condition beside it. ok is false
// where the record's key does not hold its tenant, where the kind's rows are no tenant's,
// and where no way is the kind's way to its tenant.
func tenantsWay(tree *DeleteTree, kind *DeleteKind, recordKey map[string]any, ways []DeletePath) (way DeletePath, ok bool) {
	if kind.Tenant == nil {
		return nil, false
	}
	if _, held := heldTenant(tree, recordKey); !held {
		return nil, false
	}
	for _, path := range ways {
		if leadsToTenant(path, kind.Tenant, tree.Tenant.Column) {
			return path, true
		}
	}

	return nil, false
}

// leadsToTenant reports whether the path is the way to the tenant the binding names,
// ending on the record's column that holds the tenant's key: one link more than the
// binding's steps, the first leaving by the binding's column alone, each to the step's
// table by its join column alone, the next leaving by the step's column alone, and the
// last referencing the record's column alone.
func leadsToTenant(path DeletePath, binding *DomainBindingData, column string) bool {
	if len(path) != len(binding.Path)+1 || !slices.Equal(path[0].Columns, []string{binding.Column}) {
		return false
	}
	for j, hop := range binding.Path {
		if path[j].Table != hop.Table || !slices.Equal(path[j].References, []string{hop.JoinColumn}) || !slices.Equal(path[j+1].Columns, []string{hop.Column}) {
			return false
		}
	}

	return slices.Equal(path[len(path)-1].References, []string{column})
}

// tenantValues is the most values ofRecordsTenant adds to a statement about the kind's
// rows: the record's key, and none where it writes no condition.
func tenantValues(kind *DeleteKind, tree *DeleteTree, recordKey map[string]any) int {
	if tree == nil || tree.Tenant == nil || kind.Tenant == nil {
		return 0
	}

	return max(1, len(recordKey))
}

// wayRows is one way's SELECT of the kind's rows, each read as columns says: the way
// written from where it starts down to the kind (wayFrom, or wayFromPage where under is a
// page), its conditions, and, where tenant is set, the condition that the row is of the
// record's tenant (ofRecordsTenant). The kind's table is read under its own name.
func (w *rowsUnderWriter) wayRows(columns string, kind *DeleteKind, path DeletePath, tree *DeleteTree, recordKey map[string]any, under *takenUnder, tenant bool) string {
	table := w.quote(string(kind.Resource))
	var (
		from       string
		conditions []string
	)
	if under == nil {
		from, conditions = w.wayFrom(path, recordKey, table)
	} else {
		from = w.wayFromPage(path, under, table)
	}
	if tenant {
		if condition := w.ofRecordsTenant(table+".", kind, tree, recordKey); condition != "" {
			conditions = append(conditions, strings.TrimPrefix(condition, " AND "))
		}
	}
	sql := "SELECT " + columns + " " + from
	if len(conditions) > 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}

	return sql
}

// keyColumns is the list of a SELECT of the kind's rows that reads each row's key: its
// key columns, in key order, as k1, k2 and on.
func (w *rowsUnderWriter) keyColumns(kind *DeleteKind) []string {
	table := w.quote(string(kind.Resource))
	columns := make([]string, 0, len(kind.Key))
	for i, column := range kind.Key {
		columns = append(columns, fmt.Sprintf("%s.%s AS k%d", table, w.quote(column), i+1))
	}

	return columns
}

// wayFrom writes one way from the record down to the kind's table (read under its own
// name, table): the FROM, and the conditions that hold its top to the record's key. A way
// whose last step references only columns the key holds starts from the table that step
// leaves, whose step columns are held to the key's values. A last step that references a
// column outside the key (a foreign key to other columns of the record's table) starts
// from the record's own row (w<index>_r), held to the key, and joins the step from there.
// Each row between is read under a name of its own (w<index>_<step>) and joined to the
// one above it by the step that leads up from it.
func (w *rowsUnderWriter) wayFrom(path DeletePath, recordKey map[string]any, table string) (from string, conditions []string) {
	index := w.ways
	w.ways++
	top := len(path) - 1
	last := path[top]
	if slices.ContainsFunc(last.References, func(column string) bool {
		_, held := recordKey[column]

		return !held
	}) {
		record := fmt.Sprintf("w%d_r", index)
		from = fmt.Sprintf("FROM %s AS %s JOIN %s ON %s", w.quote(last.Table), record, w.wayTable(path, index, table, top), w.linkOn(last, w.wayRow(index, table, top), record))
		for _, column := range slices.Sorted(maps.Keys(recordKey)) {
			conditions = append(conditions, fmt.Sprintf("%s.%s = %s", record, w.quote(column), w.value(recordKey[column])))
		}
	} else {
		from = "FROM " + w.wayTable(path, index, table, top)
		for i, column := range last.Columns {
			conditions = append(conditions, fmt.Sprintf("%s.%s = %s", w.wayRow(index, table, top), w.quote(column), w.value(recordKey[last.References[i]])))
		}
	}

	return from + w.joinsDown(path, index, table, top), conditions
}

// wayFromPage writes one way from a page's rows (takenUnder) down to the kind's table
// (read under its own name, table), whose last step lands on the page's table: the page's
// rows (pg, bound under pageName) joined to the rows under them. Where every column the
// last step references is held by a field of the page's rows, the key's or a carried one
// (takenUnder.field), the step's columns are joined straight to those fields (onPage);
// where it references a column the page does not carry, the page's own row of the table
// is read (w<index>_r) by the page's keys, and the step is joined from there. The rows
// between are joined as wayFrom joins them.
func (w *rowsUnderWriter) wayFromPage(path DeletePath, under *takenUnder, table string) string {
	index := w.ways
	w.ways++
	w.page = under
	top := len(path) - 1
	last := path[top]
	from := "FROM UNNEST(@" + w.pageName() + ") AS pg"
	if on, ok := w.onPage(last, w.wayRow(index, table, top), under); ok {
		from += " JOIN " + w.wayTable(path, index, table, top) + " ON " + on
	} else {
		row := fmt.Sprintf("w%d_r", index)
		from += " JOIN " + w.quote(under.table) + " AS " + row + " ON " + w.inPage(row, under.key) +
			" JOIN " + w.wayTable(path, index, table, top) + " ON " + w.linkOn(last, w.wayRow(index, table, top), row)
	}

	return from + w.joinsDown(path, index, table, top)
}

// wayRow is the name a way reads the row at the step under: the kind's table's own name
// at step 0, and w<index>_<step> above it.
func (w *rowsUnderWriter) wayRow(index int, table string, step int) string {
	if step == 0 {
		return table
	}

	return fmt.Sprintf("w%d_%d", index, step)
}

// wayTable is the table a way reads at the step, as a FROM or a JOIN names it: the kind's
// table at step 0, and the table the step before leads to, under its name (wayRow),
// above it.
func (w *rowsUnderWriter) wayTable(path DeletePath, index int, table string, step int) string {
	if step == 0 {
		return table
	}

	return w.quote(path[step-1].Table) + " AS " + w.wayRow(index, table, step)
}

// joinsDown joins the rows of a way below the step, from the one under it down to the
// kind's table, each by the step that leads up from it.
func (w *rowsUnderWriter) joinsDown(path DeletePath, index int, table string, step int) string {
	var joins strings.Builder
	for below := step - 1; below >= 0; below-- {
		joins.WriteString(" JOIN " + w.wayTable(path, index, table, below) + " ON " + w.linkOn(path[below], w.wayRow(index, table, below), w.wayRow(index, table, below+1)))
	}

	return joins.String()
}

// linkOn is the condition that the row read as lower is under the row read as upper by
// the step: each of the step's columns of lower equal to the column it references of
// upper, in the step's order.
func (w *rowsUnderWriter) linkOn(step DeleteLink, lower, upper string) string {
	conditions := make([]string, 0, len(step.Columns))
	for i, column := range step.Columns {
		conditions = append(conditions, fmt.Sprintf("%s.%s = %s.%s", lower, w.quote(column), upper, w.quote(step.References[i])))
	}

	return strings.Join(conditions, " AND ")
}

// onPage is the condition that the row read as alias is under a row of the page by the
// step: each of the step's columns of alias equal to the field of the page's rows that
// holds the column it references (takenUnder.field), written in the order of the fields.
// ok is false where a column the step references has no field.
func (w *rowsUnderWriter) onPage(step DeleteLink, alias string, under *takenUnder) (string, bool) {
	type joined struct {
		column string
		field  int
	}
	pairs := make([]joined, 0, len(step.Columns))
	for i, column := range step.Columns {
		field, ok := under.field(step.References[i])
		if !ok {
			return "", false
		}
		pairs = append(pairs, joined{column: column, field: field})
	}
	slices.SortStableFunc(pairs, func(a, b joined) int { return a.field - b.field })
	conditions := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		conditions = append(conditions, fmt.Sprintf("%s.%s = pg.%s", alias, w.quote(pair.column), pageKeyField(pair.field)))
	}

	return strings.Join(conditions, " AND "), true
}

// inPage is the condition that the columns of the row read as alias, in the key's
// order, hold the key of the page's row pg: each against the field of the page's keys
// that holds the key column at its place (pageKeyField).
func (w *rowsUnderWriter) inPage(alias string, columns []string) string {
	conditions := make([]string, 0, len(columns))
	for i, column := range columns {
		conditions = append(conditions, fmt.Sprintf("%s.%s = pg.%s", alias, w.quote(column), pageKeyField(i)))
	}

	return strings.Join(conditions, " AND ")
}

// pageName is the name the statement binds a page's keys under: pageIDs, or, where the
// statement's other named values hold that name (given), pageIDs with the first number
// after it they do not hold.
func (w *rowsUnderWriter) pageName() string {
	name := pageIDs
	for n := 2; ; n++ {
		if _, held := w.given[name]; !held {
			return name
		}
		name = pageIDs + strconv.Itoa(n)
	}
}

// named is the values the statement names by name: the given ones, and the page's keys
// under pageName where a way started from them (wayFromPage).
func (w *rowsUnderWriter) named() map[string]any {
	if w.page == nil {
		return w.given
	}
	named := make(map[string]any, len(w.given)+1)
	maps.Copy(named, w.given)
	named[w.pageName()] = w.page.ids

	return named
}

// newRowsUnderStatement renders the count of a kind's rows under the record with the
// given key, by any of the kind's ways up to it (the path statement at level 0,
// delete_cascade.go), each way written from the record's key down to the kind (wayRows),
// the ways joined by a union that holds each row once, and, where the kind's rows and the
// record are a tenant's, of the record's tenant alone (ofRecordsTenant). Under a record
// whose key holds its tenant, where one of the kind's ways is its way to its tenant
// (tenantsWay), that way alone is written, with no tenant's condition: it finds every row
// of the kind in the tenant, and the other ways nothing more. ok is false for a kind with
// no way up to the record.
func newRowsUnderStatement(dbType DBType, tree *DeleteTree, kind *DeleteKind, recordKey map[string]any) (statement rowsUnderStatement, ok bool) {
	ways := slices.DeleteFunc(slices.Clone(kind.Paths), func(path DeletePath) bool { return len(path) == 0 })
	if len(ways) == 0 {
		return rowsUnderStatement{}, false
	}
	tenant := true
	if way, own := tenantsWay(tree, kind, recordKey, ways); own {
		ways, tenant = []DeletePath{way}, false
	}
	w := &rowsUnderWriter{dbType: dbType}
	selects := make([]string, 0, len(ways))
	for _, path := range ways {
		selects = append(selects, w.wayRows(strings.Join(w.keyColumns(kind), ", "), kind, path, tree, recordKey, nil, tenant))
	}
	sql := "SELECT COUNT(*) FROM (" + strings.Join(selects, " UNION DISTINCT ") + ") AS u"

	return rowsUnderStatement{SQL: sql, Args: w.args}, true
}

// rowCounter runs one count statement where the delete reads: its transaction, or the
// read-only transaction of its question.
type rowCounter func(ctx context.Context, statement rowsUnderStatement) (int64, error)

// kindRows is a kind with the number of its rows under a record.
type kindRows struct {
	kind *DeleteKind
	rows int64
}

// refuse is the executor's check before the commit: each delete the transaction
// buffered of a record with rows under it is refused as a 409 whose sentence says what
// is in the way, the first such delete answering. One count runs per kind, inside the
// transaction.
func (t *transactionDeletes) refuse(ctx context.Context, dbType DBType, count rowCounter) error {
	for i := range t.deletes {
		sentence, err := t.deletes[i].refusal(ctx, dbType, count)
		if err != nil {
			return err
		}
		if sentence != "" {
			return httpio.NewConflictMessage(sentence)
		}
	}

	return nil
}

// refusal is the sentence that refuses the delete, "" when nothing is in its way. The
// kinds the record lists under refusedBy answer first, whatever the database would do
// with their rows: "This client has 3 consignments, so it can't be deleted." With none
// of those, the kinds with rows directly under the record answer: "This client has 2
// client contacts. Delete them first."
func (d *recordedDelete) refusal(ctx context.Context, dbType DBType, count rowCounter) (string, error) {
	refusing, err := d.rowsOf(ctx, dbType, d.tree.RefusedBy, count)
	if err != nil {
		return "", err
	}
	if len(refusing) > 0 {
		seen, err := d.seen(ctx, refusing)
		if err != nil {
			return "", err
		}

		return deleteRefusedSentence(d.tree, seen, true), nil
	}

	// A refusing kind was counted by every way it has, the one step among them.
	var plain []DeleteKind
	for i := range d.tree.Under {
		under := &d.tree.Under[i]
		if slices.ContainsFunc(d.tree.RefusedBy, func(kind DeleteKind) bool { return kind.Resource == under.Resource }) {
			continue
		}
		plain = append(plain, *under)
	}
	under, err := d.rowsOf(ctx, dbType, plain, count)
	if err != nil {
		return "", err
	}
	if len(under) == 0 {
		return "", nil
	}
	seen, err := d.seen(ctx, under)
	if err != nil {
		return "", err
	}

	return deleteRefusedSentence(d.tree, seen, false), nil
}

// rowsOf counts each kind's rows under the record and returns the kinds that have one,
// in the description's order.
func (d *recordedDelete) rowsOf(ctx context.Context, dbType DBType, kinds []DeleteKind, count rowCounter) ([]kindRows, error) {
	var found []kindRows
	for i := range kinds {
		statement, ok := newRowsUnderStatement(dbType, d.tree, &kinds[i], d.key)
		if !ok {
			continue
		}
		rows, err := count(ctx, statement)
		if err != nil {
			return nil, errors.Wrapf(err, "counting the %s under a %s", kinds[i].Words.Many, d.tree.Words.One)
		}
		if rows > 0 {
			found = append(found, kindRows{kind: &kinds[i], rows: rows})
		}
	}

	return found, nil
}

// seen is the kinds the person may be told of: the ones they may list, granted
// outright. A kind of a tenant's rows under a record whose key holds its tenant (the
// tenant record above all, or a record keyed by its tenant) is asked about in that
// tenant, whatever scope the delete is asked in, since its rows under the record are
// that tenant's; every other kind is asked about in the delete's own scope. One check
// is asked for the kinds of each scope. A grant that carries a condition shows some of
// a kind's rows and not others, and the count is of every row, so it is not enough. A
// trusted call is told every kind, and every caller a trusted kind (DeleteKind.Trusted)
// and a kind that goes on the permission to delete the record (DeleteKind.WithinReach).
func (d *recordedDelete) seen(ctx context.Context, kinds []kindRows) ([]kindRows, error) {
	if d.caller.IsZero() {
		return kinds, nil
	}
	tenant, inTenant, err := d.tenantScope(kinds)
	if err != nil {
		return nil, err
	}
	scopeOf := func(kind *DeleteKind) accesstypes.Scope {
		if inTenant && kind.Tenant != nil {
			return tenant
		}

		return d.caller.Scope
	}
	var scopes []accesstypes.Scope
	resources := make(map[accesstypes.Scope][]accesstypes.Resource, 2)
	for _, k := range kinds {
		if k.kind.Trusted || k.kind.WithinReach {
			continue
		}
		scope := scopeOf(k.kind)
		if _, asked := resources[scope]; !asked {
			scopes = append(scopes, scope)
		}
		resources[scope] = append(resources[scope], k.kind.Resource)
	}
	decisions := make(map[accesstypes.Scope]accesstypes.Decisions, len(scopes))
	for _, scope := range scopes {
		decided, err := d.caller.Permissions.Check(ctx, d.caller.Env, scope, accesstypes.List, resources[scope]...)
		if err != nil {
			return nil, errors.Wrap(err, "resource.UserPermissions.Check()")
		}
		decisions[scope] = decided
	}
	var seen []kindRows
	for _, k := range kinds {
		if k.kind.Trusted || k.kind.WithinReach || decisions[scopeOf(k.kind)][k.kind.Resource].IsGranted() {
			seen = append(seen, k)
		}
	}

	return seen, nil
}

// tenantScope is the scope of the tenant the record's key holds (heldTenant), where one
// of the kinds is a tenant's rows: ok is false where none is, or the key holds no tenant.
func (d *recordedDelete) tenantScope(kinds []kindRows) (scope accesstypes.Scope, ok bool, err error) {
	if !slices.ContainsFunc(kinds, func(k kindRows) bool { return k.kind.Tenant != nil }) {
		return accesstypes.Scope{}, false, nil
	}
	value, held := heldTenant(d.tree, d.key)
	if !held {
		return accesstypes.Scope{}, false, nil
	}
	domain, err := tenantDomainOf(value)
	if err != nil {
		return accesstypes.Scope{}, false, errors.Wrapf(err, "the tenant of a %s", d.tree.Words.One)
	}

	return accesstypes.DomainScope(domain), true, nil
}

// deleteRefusedSentence is the sentence a person reads when a delete is refused over
// the rows under the record: the kinds they may see, each with its count, and what to
// do. A kind listed under refusedBy keeps the record from being deleted at all; any
// other is in the way until its rows are deleted, or moved from under the record where
// every kind named can be (DeleteKind.movable): a mission names its squadron in a
// column that may be empty, so a person would sooner give the missions to another
// squadron than delete them. With no kind the person may see, the sentence says only
// that something is there.
func deleteRefusedSentence(tree *DeleteTree, seen []kindRows, refusedBy bool) string {
	if len(seen) == 0 {
		return tree.RowName() + " has records under it that you can't see, so it can't be deleted."
	}

	var total int64
	movable := true
	counts := make([]string, 0, len(seen))
	for _, k := range seen {
		total += k.rows
		movable = movable && k.kind.movable()
		counts = append(counts, k.kind.Words.Count(int(k.rows)))
	}
	has := tree.RowName() + " has " + joinCounts(counts)

	todo := "Delete"
	if movable {
		todo = "Move or delete"
	}

	switch {
	case refusedBy:
		return has + ", so it can't be deleted."
	case total == 1:
		return has + ". " + todo + " it first."
	default:
		return has + ". " + todo + " them first."
	}
}

// joinCounts joins counts as a person lists them: "2 sorties", "2 sorties and 1 droid
// report", "2 sorties, 1 droid report, and 3 refits".
func joinCounts(counts []string) string {
	switch len(counts) {
	case 1:
		return counts[0]
	case 2:
		return counts[0] + " and " + counts[1]
	default:
		return strings.Join(counts[:len(counts)-1], ", ") + ", and " + counts[len(counts)-1]
	}
}

// refuseDeletesOver runs the check before the commit for the transaction (refuse),
// counting through the transaction itself.
func (c *SpannerReadWriteTransaction) refuseDeletesOver(ctx context.Context) error {
	return c.deleted.refuse(ctx, SpannerDBType, c.countRows)
}

// countRows runs one count statement through the transaction's reads (reads).
func (c *SpannerReadWriteTransaction) countRows(ctx context.Context, statement rowsUnderStatement) (int64, error) {
	it := c.reads().Query(ctx, statement.spannerStatement())
	defer it.Stop()

	row, err := it.Next()
	if err != nil {
		return 0, errors.Wrap(err, "spanner.RowIterator.Next()")
	}
	var rows int64
	if err := row.Columns(&rows); err != nil {
		return 0, errors.Wrap(err, "spanner.Row.Columns()")
	}

	return rows, nil
}

// recordDelete notes a delete to count over before the commit.
func (c *SpannerReadWriteTransaction) recordDelete(d recordedDelete) {
	c.deleted.recordDelete(d)
}
