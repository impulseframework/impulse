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
// under refusedBy, and the kinds with a row directly under the record that the
// database does not remove with it (Under). The count runs once the
// transaction's function has returned, over every delete it buffered, so a request that
// deletes the rows and the record together, in either order, is not refused: a read
// inside the transaction does not see its own buffered writes, and each count leaves
// out the rows the transaction itself deleted. A row the transaction changed is counted
// as the read shows it, as it was before the transaction: a request that moves a row
// from under a record and deletes the record is refused, and the two are sent one after
// the other. The count never refuses a delete the database would take: a kind whose
// rows the database may remove with another row the transaction deleted is not counted
// (DeleteKind.RemovedWith). What the count does not reach (a row two steps down that
// the database comes to by a cascade, a row under a foreign key the generator does not
// follow, a row written in the same transaction and still under the record) is left to
// the commit, which answers in its own sentence (referentialMessage).

// deleteExclusionLimit is the most rows of one table a count leaves out by key. A
// transaction that deleted more rows of a table than this is not counted over that
// table, and the commit answers for it: the statement would outgrow what a query takes
// in parameters.
const deleteExclusionLimit = 200

// deleteRecorder is how the patch machinery reaches a transaction's record of the rows
// its patches delete and update: the Spanner wrapper satisfies it. A transaction that
// does not (the Mock's, which has no rows to count; Postgres) records nothing, and no
// delete on it is counted over.
type deleteRecorder interface {
	recordDelete(d recordedDelete)
	recordWritten(res accesstypes.Resource, key map[string]any, deleted bool)
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
// over, in buffered order, of the key of every row its patches deleted, by table, of
// how many rows of each table they deleted, and of every row they deleted or changed.
// Like the released-keys record it belongs to one attempt: a retried transaction runs
// its function again over a fresh record.
type transactionDeletes struct {
	deletes []recordedDelete
	// gone are the keys of the rows the transaction's patches deleted, by table, in the
	// order they were deleted: the rows a count leaves out.
	gone    map[accesstypes.Resource][]map[string]any
	deleted map[accesstypes.Resource]int
	// rows holds each row a patch deleted or changed, by table and key (rowOfTable),
	// for the walk that asks about one row at a time: true where a patch deleted it
	// (deletedRow), false where patches only updated it, or inserted or updated it
	// (changedRow).
	rows map[string]bool
}

// newTransactionDeletes returns an empty record.
func newTransactionDeletes() *transactionDeletes {
	return &transactionDeletes{
		gone:    make(map[accesstypes.Resource][]map[string]any),
		deleted: make(map[accesstypes.Resource]int),
		rows:    make(map[string]bool),
	}
}

// rowOfTable names a row by its table and its key by column, the columns in order, so
// two readings of one row give one name.
func rowOfTable(res accesstypes.Resource, key map[string]any) string {
	columns := make([]string, 0, len(key))
	for column := range key {
		columns = append(columns, column)
	}
	slices.Sort(columns)

	var b strings.Builder
	b.WriteString(string(res))
	for _, column := range columns {
		fmt.Fprintf(&b, "\x00%s=%v", column, key[column])
	}

	return b.String()
}

// deletedRow reports whether a patch of the transaction deleted the row.
func (t *transactionDeletes) deletedRow(res accesstypes.Resource, key map[string]any) bool {
	return t.rows[rowOfTable(res, key)]
}

// changedRow reports whether a patch of the transaction updated the row, or inserted or
// updated it, and none deleted it.
func (t *transactionDeletes) changedRow(res accesstypes.Resource, key map[string]any) bool {
	deleted, ok := t.rows[rowOfTable(res, key)]

	return ok && !deleted
}

// recordDelete notes a delete to count over.
func (t *transactionDeletes) recordDelete(d recordedDelete) {
	t.deletes = append(t.deletes, d)
}

// recordWritten notes the key of a row a patch deleted, or changed without deleting it.
// A row a patch deleted stays deleted, whatever else the transaction's patches did to
// it.
func (t *transactionDeletes) recordWritten(res accesstypes.Resource, key map[string]any, deleted bool) {
	row := rowOfTable(res, key)
	t.rows[row] = t.rows[row] || deleted
	if deleted {
		t.gone[res] = append(t.gone[res], key)
		t.deleted[res]++
	}
}

// databaseRemoves reports whether the database may remove rows of the kind with a row
// the transaction deleted, other than the record itself: the transaction deleted a row
// of a table the kind's rows go with (DeleteKind.RemovedWith). A read does not show
// which of the kind's rows those are, so the kind is not counted under the record.
func (t *transactionDeletes) databaseRemoves(kind *DeleteKind, record accesstypes.Resource) bool {
	for _, table := range kind.RemovedWith {
		deleted := t.deleted[accesstypes.Resource(table)]
		if accesstypes.Resource(table) == record {
			// The record's own delete is one of them.
			deleted--
		}
		if deleted > 0 {
			return true
		}
	}

	return false
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

// recordWrittenRow notes on the transaction that the patch changed its row, so a delete
// in the same transaction that would take the row is refused: the delete's reads show
// the row as it was before the transaction, and the row may be moved under the record
// or from under it by the commit.
func (p *PatchSet[Resource]) recordWrittenRow(txn ReadWriteTransaction) {
	recorder, ok := txn.(deleteRecorder)
	if !ok {
		return
	}
	if key, ok := p.keyByColumn(txn.DBType()); ok {
		recorder.recordWritten(p.Resource(), key, false)
	}
}

// recordDelete notes the patch's delete on the transaction: the row as one the
// transaction wrote, and, where the record's description names kinds to count, the
// delete itself, with who it is for. The person is the one the patch is armed against,
// else the caller the request's frame stamped (CallerFrom), else nobody: a trusted
// call.
func (p *PatchSet[Resource]) recordDelete(ctx context.Context, txn ReadWriteTransaction) {
	recorder, ok := txn.(deleteRecorder)
	if !ok {
		return
	}
	key, ok := p.keyByColumn(txn.DBType())
	if !ok {
		return
	}
	recorder.recordWritten(p.Resource(), key, true)

	// A delete that answered for the rows under its record when it was buffered has
	// nothing left to count (PatchSet.underAnswered).
	tree := p.querySet.rMeta.deleteTree
	if tree == nil || p.underAnswered || len(tree.RefusedBy)+len(tree.Under) == 0 {
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

// rowsUnderStatement is the count of one kind's rows under a record: its text, and its
// values in the order the text names them (@d1, @d2 on Spanner; $1, $2 on PostgreSQL).
type rowsUnderStatement struct {
	SQL  string
	Args []any
}

// spannerStatement is the statement as the Spanner client takes it, each value under
// the name the text gives it.
func (s rowsUnderStatement) spannerStatement() spanner.Statement {
	params := make(map[string]any, len(s.Args))
	for i, arg := range s.Args {
		params["d"+strconv.Itoa(i+1)] = arg
	}

	return spanner.Statement{SQL: s.SQL, Params: params}
}

// rowsUnderWriter renders one count statement for a database type.
type rowsUnderWriter struct {
	dbType  DBType
	written map[accesstypes.Resource][]map[string]any
	args    []any
	// steps counts the steps written of rows' ways to their tenants, so each reads its
	// table under a name of its own.
	steps int
}

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

// notWritten is the conditions that leave out the rows of a table the transaction
// itself deleted, for the table read under alias: " AND NOT (...)" for each such row,
// in the order the transaction deleted them.
func (w *rowsUnderWriter) notWritten(alias string, table accesstypes.Resource) string {
	var b strings.Builder
	for _, key := range w.written[table] {
		columns := make([]string, 0, len(key))
		for column := range key {
			columns = append(columns, column)
		}
		slices.Sort(columns)
		equal := make([]string, 0, len(columns))
		for _, column := range columns {
			equal = append(equal, fmt.Sprintf("%s.%s = %s", alias, w.quote(column), w.value(key[column])))
		}
		fmt.Fprintf(&b, " AND NOT (%s)", strings.Join(equal, " AND "))
	}

	return b.String()
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
	if value, held := recordKey[tree.Tenant.Column]; held && len(tree.Tenant.Path) == 0 {
		return fmt.Sprintf(" AND %s = %s", row, w.value(value))
	}
	equal := make([]string, 0, len(recordKey))
	for _, column := range slices.Sorted(maps.Keys(recordKey)) {
		equal = append(equal, fmt.Sprintf("tr.%s = %s", w.quote(column), w.value(recordKey[column])))
	}

	return fmt.Sprintf(" AND %s = (SELECT %s FROM %s AS tr WHERE %s)",
		row, w.rowTenant("tr.", tree.Tenant), w.quote(string(tree.Record)), strings.Join(equal, " AND "))
}

// tenantValues is the most values ofRecordsTenant adds to a statement about the kind's
// rows: the record's key, and none where it writes no condition.
func tenantValues(kind *DeleteKind, tree *DeleteTree, recordKey map[string]any) int {
	if tree == nil || tree.Tenant == nil || kind.Tenant == nil {
		return 0
	}

	return max(1, len(recordKey))
}

// way is the condition that a row of the kind, read as t0, is under the record by one
// path. A path of one step compares the row's own columns with the record's key. A
// longer one asks for the rows between, each joined to the one before it, the last
// holding the record's key, and none of them a row the transaction deleted. A last step
// that references a column outside the record's key (a foreign key to other columns of
// the record's table) asks for the record's own row too, found by its key, and compares
// with the column there.
func (w *rowsUnderWriter) way(index int, path DeletePath, recordKey map[string]any) string {
	last := path[len(path)-1]
	tables := make([]string, 0, len(path))
	var (
		conditions []string
		excluded   string
	)
	alias := "t0"
	for step, link := range path[:len(path)-1] {
		next := fmt.Sprintf("w%d_%d", index, step+1)
		tables = append(tables, fmt.Sprintf("%s AS %s", w.quote(link.Table), next))
		for i, column := range link.Columns {
			conditions = append(conditions, fmt.Sprintf("%s.%s = %s.%s", next, w.quote(link.References[i]), alias, w.quote(column)))
		}
		alias = next
	}
	if slices.ContainsFunc(last.References, func(column string) bool {
		_, held := recordKey[column]

		return !held
	}) {
		record := fmt.Sprintf("w%d_r", index)
		tables = append(tables, fmt.Sprintf("%s AS %s", w.quote(last.Table), record))
		for i, column := range last.Columns {
			conditions = append(conditions, fmt.Sprintf("%s.%s = %s.%s", alias, w.quote(column), record, w.quote(last.References[i])))
		}
		for _, column := range slices.Sorted(maps.Keys(recordKey)) {
			conditions = append(conditions, fmt.Sprintf("%s.%s = %s", record, w.quote(column), w.value(recordKey[column])))
		}
	} else {
		for i, column := range last.Columns {
			conditions = append(conditions, fmt.Sprintf("%s.%s = %s", alias, w.quote(column), w.value(recordKey[last.References[i]])))
		}
	}
	if len(tables) == 0 {
		return "(" + strings.Join(conditions, " AND ") + ")"
	}
	for step, link := range path[:len(path)-1] {
		excluded += w.notWritten(fmt.Sprintf("w%d_%d", index, step+1), accesstypes.Resource(link.Table))
	}

	return fmt.Sprintf("EXISTS (SELECT 1 FROM %s WHERE %s%s)", strings.Join(tables, ", "), strings.Join(conditions, " AND "), excluded)
}

// newRowsUnderStatement renders the count of a kind's rows under the record with the
// given key, by any of the kind's ways up to it, leaving out the rows the transaction
// itself deleted (written) and, where the kind's rows and the record are a tenant's,
// the rows of another tenant (ofRecordsTenant). ok is false when the transaction
// deleted more rows of one of the tables read than a statement leaves out by key
// (deleteExclusionLimit): the kind is then not counted.
func newRowsUnderStatement(dbType DBType, tree *DeleteTree, kind *DeleteKind, recordKey map[string]any, written map[accesstypes.Resource][]map[string]any) (statement rowsUnderStatement, ok bool) {
	if len(written[kind.Resource]) > deleteExclusionLimit {
		return rowsUnderStatement{}, false
	}
	for _, path := range kind.Paths {
		for _, link := range path[:len(path)-1] {
			if len(written[accesstypes.Resource(link.Table)]) > deleteExclusionLimit {
				return rowsUnderStatement{}, false
			}
		}
	}

	w := &rowsUnderWriter{dbType: dbType, written: written}
	ways := make([]string, 0, len(kind.Paths))
	for i, path := range kind.Paths {
		if len(path) == 0 {
			continue
		}
		ways = append(ways, w.way(i, path, recordKey))
	}
	if len(ways) == 0 {
		return rowsUnderStatement{}, false
	}
	sql := fmt.Sprintf("SELECT COUNT(*) FROM %s AS t0 WHERE (%s)%s%s",
		w.quote(string(kind.Resource)), strings.Join(ways, " OR "), w.ofRecordsTenant("t0.", kind, tree, recordKey), w.notWritten("t0", kind.Resource))

	return rowsUnderStatement{SQL: sql, Args: w.args}, true
}

// rowCounter runs one count statement inside the delete's transaction.
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
		sentence, err := t.deletes[i].refusal(ctx, dbType, t, count)
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
// client contacts. Delete them first." A kind the database may remove with another row
// the transaction deleted is not among them (databaseRemoves): the count never refuses
// a delete the database would take.
func (d *recordedDelete) refusal(ctx context.Context, dbType DBType, t *transactionDeletes, count rowCounter) (string, error) {
	refusing, err := d.rowsOf(ctx, dbType, d.tree.RefusedBy, t.gone, count)
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
		if t.databaseRemoves(under, d.tree.Record) {
			continue
		}
		plain = append(plain, *under)
	}
	under, err := d.rowsOf(ctx, dbType, plain, t.gone, count)
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
func (d *recordedDelete) rowsOf(ctx context.Context, dbType DBType, kinds []DeleteKind, written map[accesstypes.Resource][]map[string]any, count rowCounter) ([]kindRows, error) {
	var found []kindRows
	for i := range kinds {
		statement, ok := newRowsUnderStatement(dbType, d.tree, &kinds[i], d.key, written)
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
// outright in the delete's own scope. A grant that carries a condition shows some of a
// kind's rows and not others, and the count is of every row, so it is not enough. A
// trusted call is told every kind.
func (d *recordedDelete) seen(ctx context.Context, kinds []kindRows) ([]kindRows, error) {
	if d.caller.IsZero() {
		return kinds, nil
	}
	resources := make([]accesstypes.Resource, 0, len(kinds))
	for _, k := range kinds {
		resources = append(resources, k.kind.Resource)
	}
	decisions, err := d.caller.Check(ctx, accesstypes.List, resources...)
	if err != nil {
		return nil, err
	}
	var seen []kindRows
	for _, k := range kinds {
		if decisions[k.kind.Resource].IsGranted() {
			seen = append(seen, k)
		}
	}

	return seen, nil
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

// countRows runs one count statement inside the transaction.
func (c *SpannerReadWriteTransaction) countRows(ctx context.Context, statement rowsUnderStatement) (int64, error) {
	it := c.txn.Query(ctx, statement.spannerStatement())
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

// recordWritten notes the key of a row a patch of the transaction deleted, or changed
// without deleting it.
func (c *SpannerReadWriteTransaction) recordWritten(res accesstypes.Resource, key map[string]any, deleted bool) {
	c.deleted.recordWritten(res, key, deleted)
}
