package resource

import (
	"context"
	"fmt"
	"hash/fnv"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
)

// A delete of a record that declares what goes with it (@deleteCascade) takes those rows
// in code, inside the delete's own transaction, when the request says so
// (PatchSet.DeleteEverythingUnder). The rows are found a kind at a time, by one statement
// that follows the kind's declared ways up to the record (DeleteKind.Paths). A kind that
// sits under itself (DeleteKind.Within) is found level by level, by the same statement
// with the kind's link to itself repeated, until a level finds nothing. A row belongs
// to the deepest level that finds it, so a row is deleted before the row it is under.
//
// A kind may be nested under itself to any depth. A level's statement is one link longer
// than the level before it, and the database joins only so many tables in one statement.
// Where it refuses one (joinsRefused), the delete reads no deeper from where it started:
// it takes the rows of the nesting kinds at the last level it read, a page at a time (the
// frontier), and reads what is under each page from the page's keys (takenUnder) as it
// read what is under the record from the record's, level by level, and from pages again
// wherever those statements are refused in turn. A delete first counts every start
// (takenStart), so the refusals, the grants and the question are answered for everything
// under the record before anything goes. Then it walks the starts again, a start's
// branches before the start's own levels, and asks or takes the rows deepest first. A save
// shares one budget over the whole walk and ends where it is full.
//
// Before anything is deleted, the kinds are checked as their own deletes would check
// them: the caller's grants on each kind once, then, for a kind whose rows have anything
// to be asked (what the kind's generated code asks of a delete, a condition on the
// grant, a tenant the row must be found in, the kind's delete rule), each of its rows,
// read a page at a time with the answers of the grant's conditions: every row by a delete
// that asks, the rows it takes by a delete that takes them. A refusal says why. A delete
// larger than one save runs in rounds: each request deletes what fits one save, deepest
// rows first, and answers 202 with what is left; the record goes last (DeleteLeft). Each
// row goes through the delete of one row (bufferTaken), so each has its change record, is
// announced to open pages, and has its stored files released. Each round checks the rows
// it takes, and nothing is kept between rounds: a refusal that comes after rounds have run
// stops the delete there, and what is left is a whole, smaller record.
//
// A kind with a row straight under the record that the description neither takes nor
// lists under refusedBy refuses the delete as it refuses a plain one, in the same
// sentence and before any row is asked about: "This group has 5 tasks. Move or delete
// them first."
//
// A kind marked WithinReach goes on the permission the delete's request was let in by,
// in the request's tenant and in each other tenant in which the person holds the same
// permission: no grant is asked on its rows, and a row in a tenant that permission does
// not reach refuses the delete before any row is asked about: "This account still has 2 task
// assignments in Borealis. They have to be removed there before the account can be
// deleted."
//
// A delete that does not say it takes the rows, of a record that has some, deletes
// nothing and is answered with the question a person is asked first: "Delete this
// client and everything under it: 2 client contacts. This can't be undone." It is a 428,
// answered once the whole check has passed, so the question is never asked of a delete
// that would be refused.
//
// A request's question is asked with no read-write transaction open
// (SpannerClient.ExecuteDeleteFunc): the record's own check, the counts, the refusals,
// the grants' conditions and each row's check read the database as it stood at one
// moment, and lock nothing. What is deleted in real use is not being written: a record
// is closed long before it is deleted, or was made by mistake and never used, so the
// answer does not change while it is asked, and nothing is built for a writer that races
// the question. Where nothing is under the record there is nothing to ask, and the
// request opens its read-write transaction and deletes the record there, counting once
// more inside it. A delete that takes the rows runs wholly inside its read-write
// transaction.

// takenPageRows is the most rows one read of a page of a kind's rows brings.
const takenPageRows = 1000

// joinsRefused reports whether the database refused a statement because
// it joins more tables than one statement may. It is the one thing the delete knows about
// how deep a statement can go: the library counts no joins and holds no depth of its own.
func joinsRefused(err error) bool {
	return spanner.ErrCode(err) == codes.InvalidArgument && strings.Contains(err.Error(), "Number of joins exceeds")
}

// levelRefused is a page or a count by tenant the database refused for its joins
// (joinsRefused) at a level above 0 of a start, after the start's counts were made: the
// run reads no deeper than the level before it from every start and starts again
// (takingDelete.run), or, in a save that has buffered a row, ends the save there.
type levelRefused struct {
	level int
	err   error
}

func (e *levelRefused) Error() string {
	return e.err.Error()
}

func (e *levelRefused) Unwrap() error {
	return e.err
}

// refusedAt is the error of a page or a count by tenant at the level: a levelRefused
// where the database refused it for its joins at a level above 0, the error as it is
// otherwise.
func refusedAt(level int, err error) error {
	if level > 0 && joinsRefused(err) {
		return &levelRefused{level: level, err: err}
	}

	return err
}

// TakenRows is how a delete that takes a kind's rows reaches the delete of one of them.
// The generator writes one for each kind under DeleteTree.Taken of a record that
// declares its cascade (NewTakenRows), so a taken row goes through the kind's own
// delete: its grants, what its generated write asks of a removal, its rule
// (@validateDeleteType), its change record, its files.
type TakenRows interface {
	// columns are the kind's columns on the database type, each with the field that
	// holds it and the field's Go type.
	columns(dbType DBType) map[string]takenColumn
	// row is the delete of the kind's row with the given key, field by field in key
	// order, armed against the caller, or trusted for a zero Caller.
	row(fields []accesstypes.Field, values []any, caller *Caller) takenRow
	// kind is the delete of the kind's rows as one, armed as each row's is, with no key:
	// what the kind asks before any of its rows is read.
	kind(caller *Caller) takenKind
	// tracked reports whether a change record is written for the removal of a row.
	tracked() bool
}

// TakenKind is a kind of row a record's delete takes with it that the record's
// description does not list: its rows are found, checked, costed and deleted by code.
// The question counts it, the check before the delete asks it, and each round takes what
// fits of it, after the kinds the description lists and before the record. The library's
// rows for a tenant are such kinds.
//
// Count and Check are handed the transaction the delete reads on. For a request's
// question that is a question transaction, which reads the database as it stood at one
// moment, locks nothing and drops what is buffered through it. Take is always handed the
// read-write transaction of a save.
type TakenKind interface {
	// Words are what the question calls the kind's rows.
	Words() DeleteWords
	// Count is the number of the kind's rows under the record as the transaction it is
	// handed reads them.
	Count(ctx context.Context, txn ReadWriteTransaction) (int64, error)
	// Check refuses the delete where a row of the kind may not go. It writes nothing.
	Check(ctx context.Context, txn ReadWriteTransaction) error
	// Take checks and buffers the delete of rows of the kind, as many as cost no more than
	// budget mutations. It answers the rows it took, what they cost, and whether rows of the
	// kind are left.
	Take(ctx context.Context, txn ReadWriteTransaction, budget int, eventSource ...eventsource.EventSource) (rows int64, mutations int, more bool, err error)
}

// takenColumn is a column of a kind's table as its resource holds it.
type takenColumn struct {
	field accesstypes.Field
	typ   reflect.Type
}

// takenRow is the delete of one row that a record's delete takes with it: the patch set
// of the row's own delete.
type takenRow interface {
	checkTaken(ctx context.Context, txn ReadWriteTransaction, asks *takenAsks, answers []spanner.NullBool) (byRule bool, err error)
	bufferTaken(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) error
}

// takenKind is the delete of a kind's rows as one (TakenRows.kind): the caller's grants
// on the kind's resource, and what the kind's rows are asked.
type takenKind interface {
	checkPermissions(ctx context.Context, dbType DBType) error
	takenAsks(dbType DBType) (*takenAsks, error)
}

// takenRows is TakenRows for one resource.
type takenRows[R Resourcer] struct {
	arm func(patchSet *PatchSet[R], caller *Caller)
}

// NewTakenRows returns how a delete of a record deletes a row of the resource R with
// it. arm makes the patch set of one row's delete into the resource's own delete: it
// registers the resource's delete rule, and arms the delete against the caller unless
// the caller is zero, which is a trusted call. Where the resource's rows decide what a
// person may do, it also registers what the resource's generated write asks of a
// removal (RegisterDeleteCheckFunc). The generator writes the call, with the resource's
// generated delete patch.
func NewTakenRows[R Resourcer](arm func(patchSet *PatchSet[R], caller *Caller)) TakenRows {
	return &takenRows[R]{arm: arm}
}

// columns are the resource's columns on the database type.
func (r *takenRows[R]) columns(dbType DBType) map[string]takenColumn {
	return columnsOf(NewMetadata[R](), dbType)
}

// row is the delete of the resource's row with the given key.
func (r *takenRows[R]) row(fields []accesstypes.Field, values []any, caller *Caller) takenRow {
	patchSet := NewPatchSet(NewMetadata[R]()).SetPatchType(DeletePatchType)
	for i, field := range fields {
		patchSet.SetKey(field, values[i])
	}
	r.arm(patchSet, caller)

	return patchSet
}

// kind is the delete of the resource's rows as one, with no key.
func (r *takenRows[R]) kind(caller *Caller) takenKind {
	patchSet := NewPatchSet(NewMetadata[R]()).SetPatchType(DeletePatchType)
	r.arm(patchSet, caller)

	return patchSet
}

// tracked reports whether the resource's changes are recorded.
func (r *takenRows[R]) tracked() bool {
	return NewMetadata[R]().trackChanges
}

// columnsOf are a resource's columns on the database type, each with its field and the
// field's Go type.
func columnsOf[R Resourcer](rMeta *Metadata[R], dbType DBType) map[string]takenColumn {
	fields := rMeta.dbFieldMap(dbType)
	columns := make(map[string]takenColumn, len(fields))
	for field, meta := range fields {
		columns[meta.ColumnName] = takenColumn{field: field, typ: meta.fieldType}
	}

	return columns
}

// takenAsks is what a kind's rows are asked, beyond the caller's grants on the kind: the
// question the kind's generated code registered (question) and its delete rule (rule),
// asked of each row, and the answers a page of the rows is read with (terms), one
// boolean each, for the row as its own delete's check reads it: first whether it is in
// the request's tenant (tenancy), then whether each condition the grant carries holds
// (groups, by the resources each names). params are the values the terms name.
type takenAsks struct {
	question bool
	rule     bool
	tenancy  bool
	groups   [][]accesstypes.Resource
	terms    []string
	params   map[string]any
}

// asksRows reports whether the kind's rows have anything to be asked. A kind that asks
// nothing of its rows is not read before it is taken: it is counted.
func (a *takenAsks) asksRows() bool {
	return a.question || a.rule || len(a.terms) > 0
}

// takenAsks is what the delete the patch set carries asks of a row of its resource,
// once its grants have been checked (checkPermissions): its terms are what
// writeCheckStatement reads of the one row a delete names, the conditions lowered over
// the row read under its table's name, and the tenant predicate as a term of its own in
// place of the condition that locates the row.
func (p *PatchSet[Resource]) takenAsks(dbType DBType) (*takenAsks, error) {
	asks := &takenAsks{question: p.deleteCheckFunc != nil, rule: p.validateDeleteFunc != nil}
	groups, err := p.writeConditionGroups()
	if err != nil {
		return nil, err
	}
	tenancy, err := p.mutationTenancy()
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 && !tenancy.locatesRow() {
		return asks, nil
	}
	if dbType != SpannerDBType {
		return nil, errors.Newf("conditional grant and tenancy enforcement is not implemented for %s", dbType)
	}
	q := p.querySet
	if q.collection == nil {
		return nil, errors.Newf("resource %s carries conditional decisions but no generated collection is wired to render them", q.Resource())
	}

	gen := newSQLGenerator(Spanner)
	registry := newParamRegistry()
	bindings, _ := q.collection.Bindings(q.collection.Scope(q.Resource()), q.Resource())
	_, partitioned := q.scope.Domain()
	lctx := &loweringContext{
		outer:       string(q.Resource()),
		bindings:    bindings,
		collection:  q.collection,
		partitioned: partitioned,
	}
	if tenancy.locatesRow() {
		sql, err := q.tenancyPredicate(dbType, registry)
		if err != nil {
			return nil, err
		}
		if sql != "" {
			asks.tenancy = true
			asks.terms = append(asks.terms, sql)
		}
	}
	for _, group := range groups {
		sql, err := lowerToSQL(group.expr, lctx, gen, registry)
		if err != nil {
			return nil, err
		}
		asks.groups = append(asks.groups, group.resources)
		asks.terms = append(asks.terms, "("+sql+")")
	}
	asks.params = make(map[string]any)
	if err := p.mergeCheckParams(registry, asks.params); err != nil {
		return nil, err
	}

	return asks, nil
}

// checkTaken asks of the patch set's row what its own delete asks of it (checkDelete),
// beyond the grants, which were asked of its kind once: the question the generated code
// registered, then the answers a page read of the row (answers, in the order of
// asks.terms), then the delete rule. A row not in the request's tenant is answered as
// no row, and a condition that does not hold for it, or holds NULL, as the grant's
// refusal, both as enforceWriteConditions answers them. byRule says a refusal is the
// row's own reason, the question's or the rule's, whose sentence is passed on.
func (p *PatchSet[Resource]) checkTaken(ctx context.Context, txn ReadWriteTransaction, asks *takenAsks, answers []spanner.NullBool) (byRule bool, err error) {
	if p.deleteCheckFunc != nil {
		if err := p.deleteCheckFunc(ctx, txn); err != nil {
			return true, errors.Wrap(err, "deleteCheckFunc()")
		}
	}

	// The tenant's term, where there is one, comes before the groups'.
	first := 0
	if asks.tenancy {
		first = 1
	}
	for i, answer := range answers {
		if answer.Valid && answer.Bool {
			continue
		}
		if i < first {
			return false, recordNotFound[Resource](p.PrimaryKey().RowID())
		}

		return false, p.querySet.conditionRefused(asks.groups[i-first])
	}

	if p.validateDeleteFunc != nil {
		if err := p.validateDeleteFunc(ctx, txn); err != nil {
			return true, errors.Wrap(err, "validateDeleteFunc()")
		}
	}

	return false, nil
}

// takenUnder is a page of rows of a table that nests, where a delete's
// statements start from in place of the record: the rows a level of the delete could
// still read, by their keys, when the database refused the next level's statement for its
// joins. What is under them is read from their keys as what is under the record is read
// from the record's.
type takenUnder struct {
	// table is the table that nests, and key its key columns, in key order.
	table string
	key   []string
	// ids are the page's keys as a statement binds them (pageKeys).
	ids any
	// crossed says the page's rows' own level was read by a start of another table, or by
	// the record (DeleteTree.waysUnder).
	crossed bool
	// rows are the page's keys as read, one text for each row, for the check that no page
	// is read inside itself (takingDelete.countFrom).
	rows map[string]bool
}

// withoutKeys is the page with its keys let go: what a branch keeps between the walks.
func (u *takenUnder) withoutKeys() *takenUnder {
	return &takenUnder{table: u.table, key: u.key, ids: nil, crossed: u.crossed, rows: nil}
}

// pageKeys are the keys of a page's rows as a statement binds them: for a key of one
// column, an array of the column's values; for a key of several, an array of rows, each
// holding the row's key columns in key order. Each value is encoded as the Spanner client
// encodes it in any statement (spanner.Encoder), and a value of a type of the
// application's own over a plain one is the plain value.
func pageKeys(rows []takenPageRow) (any, error) {
	if len(rows) == 0 {
		return nil, errors.New("a page of no rows has no keys")
	}
	encoded := make([][]reflect.Value, 0, len(rows))
	for _, row := range rows {
		values := make([]reflect.Value, 0, len(row.key))
		for _, value := range row.key {
			plain, err := pageValue(value)
			if err != nil {
				return nil, err
			}
			values = append(values, reflect.ValueOf(plain))
		}
		encoded = append(encoded, values)
	}
	if len(encoded[0]) == 1 {
		ids := reflect.MakeSlice(reflect.SliceOf(encoded[0][0].Type()), 0, len(encoded))
		for _, values := range encoded {
			ids = reflect.Append(ids, values[0])
		}

		return ids.Interface(), nil
	}
	fields := make([]reflect.StructField, 0, len(encoded[0]))
	for i, value := range encoded[0] {
		name := pageKeyField(i)
		fields = append(fields, reflect.StructField{Name: name, Type: value.Type(), Tag: reflect.StructTag(`spanner:"` + name + `"`)})
	}
	keyType := reflect.StructOf(fields)
	ids := reflect.MakeSlice(reflect.SliceOf(keyType), 0, len(encoded))
	for _, values := range encoded {
		key := reflect.New(keyType).Elem()
		for i, value := range values {
			key.Field(i).Set(value)
		}
		ids = reflect.Append(ids, key)
	}

	return ids.Interface(), nil
}

// pageKeyField is the name of the field of a page's key that holds its key column at the
// index, in key order, where the key has several columns: C1, C2 and on.
func pageKeyField(index int) string {
	return "C" + strconv.Itoa(index+1)
}

// pageValue is one key value of a page's row as a statement binds it: what the value
// encodes to where it encodes itself (spanner.Encoder), and a value of a named type over a
// string, an integer, a float or a bool as the plain value.
func pageValue(value any) (any, error) {
	if encoder, ok := value.(spanner.Encoder); ok {
		encoded, err := encoder.EncodeSpanner()
		if err != nil {
			return nil, errors.Wrap(err, "spanner.Encoder.EncodeSpanner()")
		}
		value = encoded
	}
	v := reflect.ValueOf(value)
	if !v.IsValid() || v.Type().PkgPath() == "" {
		return value, nil
	}
	switch v.Kind() {
	case reflect.String:
		return v.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), nil
	case reflect.Float32, reflect.Float64:
		return v.Float(), nil
	case reflect.Bool:
		return v.Bool(), nil
	default:
		return value, nil
	}
}

// takenQuery writes a kind's path statement at a level for the delete of one record: the
// record's description and key, and the page the statements start from in place of the
// record (under), nil where they start from the record.
type takenQuery struct {
	tree  *DeleteTree
	key   map[string]any
	under *takenUnder
}

// ways are the kind's ways at the level from where the query starts: up to the record
// (DeleteTree.waysAt), or up to a row of its page (DeleteTree.waysUnder).
func (q *takenQuery) ways(kind *DeleteKind, level int) []DeletePath {
	if q.under == nil {
		return q.tree.waysAt(kind, level)
	}

	return q.tree.waysUnder(kind, q.under.table, q.under.crossed, level)
}

// anyWay is the condition that a row of the kind, read under the name outer, is where the
// query starts by one of the ways: under the record (rowsUnderWriter.anyWay), or under a
// row of its page (rowsUnderWriter.wayUnder).
func (q *takenQuery) anyWay(w *rowsUnderWriter, ways []DeletePath, outer string) string {
	if q.under == nil {
		return w.anyWay(ways, q.key, outer)
	}
	conditions := make([]string, 0, len(ways))
	for _, path := range ways {
		conditions = append(conditions, w.wayUnder(path, q.under, outer))
	}

	return "(" + strings.Join(conditions, " OR ") + ")"
}

// condition is the condition that a row of the kind, read under its table's name, is
// at the level under the record, below the deepest level that finds a row (countLevels):
// the level finds it, by one of the kind's ways at the level (DeleteTree.waysAt), in the
// record's tenant. A row that more than one level finds belongs to the deepest: below
// the deepest level, a level's condition leaves out the rows the next level finds. A row
// that two levels find, with a level between them that does not, is at both: it is
// counted twice, and deleted at the deeper level first. ok is false where the kind has
// no way at the level.
func (q *takenQuery) condition(w *rowsUnderWriter, kind *DeleteKind, level, deepest int) (string, bool) {
	ways := q.ways(kind, level)
	if len(ways) == 0 {
		return "", false
	}
	outer := w.quote(string(kind.Resource))
	condition := q.anyWay(w, ways, outer)
	if next := q.ways(kind, level+1); level < deepest && len(next) > 0 {
		// A way that compares an empty column is NULL, not false: COALESCE makes it false,
		// so a row the next level does not find is kept.
		condition += " AND NOT COALESCE(" + q.anyWay(w, next, outer) + ", FALSE)"
	}

	return condition + w.ofRecordsTenant(outer+".", kind, q.tree, q.key), true
}

// count is the count of the kind's rows at the level, below the deepest level; ok is
// false where the kind is not counted there (condition). With the deepest level the
// level itself, it is the count of the rows the level finds.
func (q *takenQuery) count(kind *DeleteKind, level, deepest int) (rowsUnderStatement, bool) {
	w := &rowsUnderWriter{dbType: SpannerDBType}
	condition, ok := q.condition(w, kind, level, deepest)
	if !ok {
		return rowsUnderStatement{}, false
	}

	return rowsUnderStatement{SQL: fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", w.quote(string(kind.Resource)), condition), Args: w.args, Named: w.named()}, true
}

// page is the read of one page of at most limit of the kind's rows at the level, below
// the deepest level, in key order, after the first offset rows: each row's key, then each
// of the terms asks reads of it (none where asks is nil).
func (q *takenQuery) page(kind *DeleteKind, level, deepest int, asks *takenAsks, limit, offset int) (rowsUnderStatement, bool) {
	w := &rowsUnderWriter{dbType: SpannerDBType}
	if asks != nil {
		w.given = asks.params
	}
	condition, ok := q.condition(w, kind, level, deepest)
	if !ok {
		return rowsUnderStatement{}, false
	}
	outer := w.quote(string(kind.Resource))
	key := make([]string, 0, len(kind.Key))
	for _, column := range kind.Key {
		key = append(key, outer+"."+w.quote(column))
	}
	columns := slices.Clone(key)
	if asks != nil {
		for i, term := range asks.terms {
			columns = append(columns, fmt.Sprintf("%s AS a%d", term, i+1))
		}
	}
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY %s LIMIT %d", strings.Join(columns, ", "), outer, condition, strings.Join(key, ", "), limit)
	if offset > 0 {
		sql += fmt.Sprintf(" OFFSET %d", offset)
	}

	return rowsUnderStatement{SQL: sql, Args: w.args, Named: w.named()}, true
}

// countedKind is a kind a delete counts, with the query its statements are written by.
type countedKind struct {
	kind  *DeleteKind
	query *takenQuery
}

// takenLevels are the rows under where a start begins of each kind a delete counts,
// level by level (rows, by kind, at each level from 0), and the deepest level that finds
// a row of one of them. A cut start (cut) reads no deeper than its deepest level: the
// rows under the rows at that level are read from their keys (takenStart).
type takenLevels struct {
	deepest int
	rows    map[*DeleteKind][]int64
	cut     bool
}

// countLevels finds the deepest level, counting what each level from level 1 finds of
// each kind with a place on a way for a link to a row of the same table
// (takenQuery.ways), until a level finds no row of any of them: the deepest is the
// level before it. Then it counts each kind's rows at each level from 0 to the deepest
// (takenQuery.condition): the deepest level's count is the one that found it, and a
// level that found no row of the kind holds none without a second count. A kind with no
// place has rows at level 0 alone. A kind's count is the sum of its levels (total).
//
// Where the database refuses a count above level 0 for its joins (joinsRefused), the
// start is cut: a finding count refused at a level ends the finding there and cuts the
// start at the level before it at the latest, and a kind's count refused at a level ends
// that kind's counts above level 0 and cuts the start at the level before it at the
// latest; the kinds after it count no level above the lowest cut. A start with a reach
// (reach, -1 for none) finds no deeper than the level after it, and a row found there
// cuts the start at the reach. A cut start's deepest level is its cut, and each kind's
// rows are its counts at levels 0 to the cut, every one leaving out what the next level
// finds. A count at level 0 that is refused is returned as the error it is.
func countLevels(ctx context.Context, count rowCounter, kinds []countedKind, reach int) (*takenLevels, error) {
	counts := &levelCounts{count: count, cut: -1}
	found, deepest, err := counts.find(ctx, kinds, reach)
	if err != nil {
		return nil, err
	}
	levels := &takenLevels{deepest: deepest, rows: make(map[*DeleteKind][]int64, len(kinds))}
	for _, k := range kinds {
		finds, nested := found[k.kind]
		at, err := counts.kindRows(ctx, k, finds, nested, deepest)
		if err != nil {
			return nil, err
		}
		levels.rows[k.kind] = at
	}
	if counts.cut >= 0 {
		levels.cut, levels.deepest = true, counts.cut
		for kind, at := range levels.rows {
			levels.rows[kind] = at[:min(len(at), counts.cut+1)]
		}
	}

	return levels, nil
}

// below is the level each statement of the start is written below (takenQuery.condition):
// one past the deepest for a cut start, whose deepest level leaves out what the next level
// finds, and the deepest itself for a whole one.
func (l *takenLevels) below() int {
	if l.cut {
		return l.deepest + 1
	}

	return l.deepest
}

// total is the kind's rows under the record, every level summed.
func (l *takenLevels) total(kind *DeleteKind) int64 {
	var rows int64
	for _, at := range l.rows[kind] {
		rows += at
	}

	return rows
}

// at is the kind's rows at the level.
func (l *takenLevels) at(kind *DeleteKind, level int) int64 {
	if level >= len(l.rows[kind]) {
		return 0
	}

	return l.rows[kind][level]
}

// levelCounts is one countLevels: how its counts are sent, and the lowest level the start
// is cut at so far, -1 where it is not cut.
type levelCounts struct {
	count rowCounter
	cut   int
}

// counted is the kind's count at the level, below the deepest level, none where the kind
// is not counted there (takenQuery.count).
func (c *levelCounts) counted(ctx context.Context, k countedKind, level, deepest int) (int64, error) {
	statement, ok := k.query.count(k.kind, level, deepest)
	if !ok {
		return 0, nil
	}
	rows, err := c.count(ctx, statement)
	if err != nil {
		return 0, errors.Wrapf(err, "counting the %s under a %s", k.kind.Words.Many, k.query.tree.Words.One)
	}

	return rows, nil
}

// cutAt cuts the start at the level at the latest.
func (c *levelCounts) cutAt(level int) {
	if c.cut < 0 || level < c.cut {
		c.cut = level
	}
}

// find counts what each level from level 1 finds of each kind with a place on a way for a
// link to a row of the same table, until a level finds no row of any of them, a finding
// count is refused (the refused level is taken to hold rows, and the start is cut at the
// level before it), or the level after the reach (a row found there cuts the start at the
// reach). It answers what each level found of each such kind (found[kind][i] is what
// level i+1 found) and the deepest level.
func (c *levelCounts) find(ctx context.Context, kinds []countedKind, reach int) (found map[*DeleteKind][]int64, deepest int, err error) {
	nested := slices.DeleteFunc(slices.Clone(kinds), func(k countedKind) bool {
		return len(k.query.ways(k.kind, 1)) == 0
	})
	found = make(map[*DeleteKind][]int64, len(nested))
	for level := 1; len(nested) > 0 && (reach < 0 || level <= reach+1); level++ {
		foundAny := false
		for _, k := range nested {
			rows, err := c.counted(ctx, k, level, level)
			if joinsRefused(err) {
				c.cutAt(level - 1)

				return found, level, nil
			}
			if err != nil {
				return nil, 0, err
			}
			found[k.kind] = append(found[k.kind], rows)
			foundAny = foundAny || rows > 0
		}
		if !foundAny {
			break
		}
		deepest = level
		if level == reach+1 {
			c.cutAt(reach)
		}
	}

	return found, deepest, nil
}

// kindRows are the kind's rows at each level from 0 to the deepest. A nested kind (one
// that find counted; finds is what each level found of it) is counted at each level from
// 1 that found a row of it, up to the cut, and holds at the deepest level what found it
// there where the start is not cut; a count refused at a level ends its counts above
// level 0 and cuts the start at the level before it. Every kind's level-0 count comes
// last.
func (c *levelCounts) kindRows(ctx context.Context, k countedKind, finds []int64, nested bool, deepest int) ([]int64, error) {
	at := []int64{0}
	if nested {
		at = make([]int64, deepest+1)
		for level := 1; level < deepest && (c.cut < 0 || level <= c.cut); level++ {
			if finds[level-1] == 0 {
				continue
			}
			rows, err := c.counted(ctx, k, level, deepest)
			if joinsRefused(err) {
				c.cutAt(level - 1)

				break
			}
			if err != nil {
				return nil, err
			}
			at[level] = rows
		}
		if c.cut < 0 && deepest > 0 {
			at[deepest] = finds[deepest-1]
		}
	}
	rows, err := c.counted(ctx, k, 0, deepest)
	if err != nil {
		return nil, err
	}
	at[0] = rows

	return at, nil
}

// takenPageRow is one row of a page: its key, column by column in key order, in the Go
// types the kind's resource holds them in, and what the page's terms answered of it.
type takenPageRow struct {
	key     []any
	answers []spanner.NullBool
}

// readPage reads the rows a page statement finds, through the transaction's reads
// (reads): each row's key, in the given types, and as many answers after it.
func (c *SpannerReadWriteTransaction) readPage(ctx context.Context, statement rowsUnderStatement, types []reflect.Type, answers int) ([]takenPageRow, error) {
	it := c.reads().Query(ctx, statement.spannerStatement())
	defer it.Stop()

	var rows []takenPageRow
	for {
		row, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, errors.Wrap(err, "spanner.RowIterator.Next()")
		}
		read := takenPageRow{key: make([]any, 0, len(types)), answers: make([]spanner.NullBool, answers)}
		for i, typ := range types {
			value := reflect.New(typ)
			if err := row.Column(i, value.Interface()); err != nil {
				return nil, errors.Wrap(err, "spanner.Row.Column()")
			}
			read.key = append(read.key, value.Elem().Interface())
		}
		for i := range answers {
			if err := row.Column(len(types)+i, &read.answers[i]); err != nil {
				return nil, errors.Wrap(err, "spanner.Row.Column()")
			}
		}
		rows = append(rows, read)
	}

	return rows, nil
}

// DeleteLeft is what a delete that takes rows says when one save did not hold them all:
// the rows this save deleted, and the rows still under the record. The record's own row
// stands until a save takes the last of them. The same delete, sent again, goes on.
type DeleteLeft struct {
	// Deleted is the rows this save deleted.
	Deleted int64 `json:"deleted"`
	// Left is the rows still under the record, as they were counted when the save began,
	// less those it deleted.
	Left int64 `json:"left"`
}

// takenDelete is one row a delete takes, with its kind.
type takenDelete struct {
	kind *DeleteKind
	row  takenRow
}

// takenRound is what one save of a delete that takes rows did: the rows counted under
// the record as the save began, the rows the save buffered the delete of, and whether
// it took the last of them, so the record goes in the same save.
type takenRound struct {
	counted  int64
	deleted  int64
	finished bool
}

// left is what the round says of the delete (PatchSet.DeleteLeft): nil where it
// finished.
func (r *takenRound) left() *DeleteLeft {
	if r.finished {
		return nil
	}

	return &DeleteLeft{Deleted: r.deleted, Left: max(0, r.counted-r.deleted)}
}

// takenTotals are the rows of each kind a delete counted: across every start under the
// record (takenStart), or within one (takenLevels).
type takenTotals interface {
	total(kind *DeleteKind) int64
}

// takenStart is one place a delete's statements start from, with what they counted
// there: the record, or a page of a frontier (takenUnder); its levels (takenLevels); and,
// for a cut start, the branches opened from its frontier, in the order they were opened.
// A branch keeps its counts and not its page's keys: the second walk reads the page
// again (takingDelete.walk) and matches it by its kind, its rows and what its keys come
// to.
type takenStart struct {
	query    *takenQuery
	levels   *takenLevels
	branches []*takenStart
	kind     *DeleteKind
	rows     int
	digest   uint64
}

// total is the kind's rows in the start and in every start under it.
func (s *takenStart) total(kind *DeleteKind) int64 {
	rows := s.levels.total(kind)
	for _, branch := range s.branches {
		rows += branch.total(kind)
	}

	return rows
}

// matches reports whether a page read again is the page the branch was counted from.
func (s *takenStart) matches(kind *DeleteKind, page []takenPageRow) bool {
	return s.kind == kind && s.rows == len(page) && s.digest == pageDigest(page)
}

// pageDigest is what a page's keys come to, by which a page read again is matched to
// the page a branch was counted from.
func pageDigest(page []takenPageRow) uint64 {
	digest := fnv.New64a()
	for _, row := range page {
		_, _ = fmt.Fprintf(digest, "%#v;", row.key)
	}

	return digest.Sum64()
}

// walkEnded ends a walk where each says it is done (walk).
type walkEnded struct{}

func (walkEnded) Error() string {
	return "the walk ended"
}

// takingDelete is the delete of one record that takes the rows under it, as deleteTaken
// runs it on a transaction: a question transaction, for a request's question, or the
// delete's own read-write transaction.
type takingDelete struct {
	asked *recordedDelete
	query *takenQuery
	txn   *SpannerReadWriteTransaction
	// caller is the person each row's delete is armed against, a zero Caller where the
	// record's delete runs trusted. A kind marked AsCaller is checked as whoever the
	// delete is for instead (callerOf).
	caller *Caller
	// page reads one page of a kind's rows (SpannerReadWriteTransaction.readPage).
	page func(ctx context.Context, statement rowsUnderStatement, types []reflect.Type, answers int) ([]takenPageRow, error)
	// byTenant counts a kind's rows by their tenant
	// (SpannerReadWriteTransaction.countByTenant).
	byTenant func(ctx context.Context, statement rowsUnderStatement) ([]tenantRows, error)
	// size is the most mutations the save holds, DeleteMutationLimit where it is zero.
	size int
	// also are the kinds code handed the delete (PatchSet.DeleteAlso), in the order
	// handed.
	also []TakenKind
	// pageRows is the most rows one page of a frontier holds, takenPageRows where it is
	// zero: a test makes it small.
	pageRows int
}

// callerOf is the person a row of the kind is checked as: none for a trusted kind
// (DeleteKind.Trusted), whose rows go with the record whoever deletes it; none for a kind
// marked WithinReach (DeleteKind.WithinReach), whose rows go on the permission the
// request was let in by; whoever the delete is for, the patch's caller or else the
// request's, for a kind marked AsCaller (DeleteKind.AsCaller), nobody outside a request;
// and for every other kind the person the record's own delete is armed against, nobody
// where it runs trusted.
func (d *takingDelete) callerOf(kind *DeleteKind) *Caller {
	switch {
	case kind.Trusted, kind.WithinReach:
		return nil
	case kind.AsCaller:
		return d.asked.caller
	default:
		return d.caller
	}
}

// keyFields are the fields that hold the kind's key columns, in key order, and their Go
// types.
func (d *takingDelete) keyFields(kind *DeleteKind) ([]accesstypes.Field, []reflect.Type, error) {
	columns := kind.Rows.columns(d.txn.DBType())
	fields := make([]accesstypes.Field, 0, len(kind.Key))
	types := make([]reflect.Type, 0, len(kind.Key))
	for _, column := range kind.Key {
		held, ok := columns[column]
		if !ok {
			return nil, nil, errors.Newf("no field of %s holds its key column %s", kind.Resource, column)
		}
		fields = append(fields, held.field)
		types = append(types, held.typ)
	}

	return fields, types, nil
}

// rowsAt calls each with the delete of each of the kind's rows at the level of the
// start the query writes, below the deepest level, page by page in key order, with what
// the page's terms answered of the row (asks; none where it is nil). It stops at the
// first error each answers. A page the database refuses for its joins at a level above 0
// is a levelRefused.
func (d *takingDelete) rowsAt(ctx context.Context, query *takenQuery, kind *DeleteKind, level, deepest int, asks *takenAsks, each func(taken takenDelete, answers []spanner.NullBool) error) error {
	fields, types, err := d.keyFields(kind)
	if err != nil {
		return err
	}
	answers := 0
	if asks != nil {
		answers = len(asks.terms)
	}
	for offset := 0; ; offset += takenPageRows {
		statement, ok := query.page(kind, level, deepest, asks, takenPageRows, offset)
		if !ok {
			return nil
		}
		rows, err := d.page(ctx, statement, types, answers)
		if err != nil {
			return refusedAt(level, errors.Wrapf(err, "reading the %s under a %s", kind.Words.Many, d.query.tree.Words.One))
		}
		for _, row := range rows {
			if err := each(takenDelete{kind: kind, row: kind.Rows.row(fields, row.key, d.callerOf(kind))}, row.answers); err != nil {
				return err
			}
		}
		if len(rows) < takenPageRows {
			return nil
		}
	}
}

// run is the delete, each count statement run by count: the counts, the refusing kinds'
// refusal, the refusal over a row straight under the record that the delete does not
// take, the refusal over a row beyond the reach of whoever the delete is for, then the
// taken kinds' checks and the rows taken (take). Each request asks them all again, so
// every round of a delete larger than one save does. Where the database refuses a page
// or a count by tenant for its joins at a level above 0 of a start after the start's
// counts were made, and nothing is buffered (levelRefused), the run reads no deeper than
// the level before it from any start (its reach) and starts again from its first count.
// The reach is the run's alone: nothing of it is kept for the next request.
func (d *takingDelete) run(ctx context.Context, count rowCounter, tracked map[accesstypes.Resource]bool, everythingUnder bool, eventSource ...eventsource.EventSource) (*takenRound, error) {
	reach := -1
	for {
		round, err := d.runWithin(ctx, count, reach, tracked, everythingUnder, eventSource...)
		var refused *levelRefused
		if !errors.As(err, &refused) {
			return round, err
		}
		if reach >= 0 && refused.level-1 >= reach {
			return nil, err
		}
		reach = refused.level - 1
	}
}

// runWithin is one run of the delete (run) that reads no deeper than the reach from any
// start, -1 for none.
func (d *takingDelete) runWithin(ctx context.Context, count rowCounter, reach int, tracked map[accesstypes.Resource]bool, everythingUnder bool, eventSource ...eventsource.EventSource) (*takenRound, error) {
	start, err := d.count(ctx, count, reach)
	if err != nil {
		return nil, err
	}
	if err := d.asked.refusedNow(ctx, start); err != nil {
		return nil, err
	}
	if err := d.asked.refusedUnder(ctx, d.txn.DBType(), count); err != nil {
		return nil, err
	}
	if err := d.refusedBeyondReach(ctx, start); err != nil {
		return nil, err
	}

	return d.take(ctx, start, tracked, everythingUnder, eventSource...)
}

// count is the first walk: it counts the refusing kinds' rows and the taken kinds' under
// the record, level by level, and, where the record's start is cut, under each page of
// its frontier the same way, depth first, to the bottom of every descent. It reads no
// deeper than the reach from any start (-1 for none).
func (d *takingDelete) count(ctx context.Context, count rowCounter, reach int) (*takenStart, error) {
	return d.countFrom(ctx, count, d.query, reach, nil)
}

// countFrom counts the start the query writes, and, where it is cut, each branch of its
// frontier (frontier). inside are the pages of the branches the walk is inside: a page
// with the same keys as one of them, of the same table, is a page of rows nested under
// themselves, whose descent would never end, and the delete fails, as it did when the
// levels of such rows never ended. A row a page shares with a page it is inside is no
// ring: a row two levels find is read at both. A ring every level finds whole reaches
// no page, as it reaches no level (takenQuery.condition): it is left out.
func (d *takingDelete) countFrom(ctx context.Context, count rowCounter, query *takenQuery, reach int, inside []*takenUnder) (*takenStart, error) {
	tree := query.tree
	kinds := make([]countedKind, 0, len(tree.RefusedBy)+len(tree.Taken))
	for i := range tree.RefusedBy {
		kinds = append(kinds, countedKind{kind: &tree.RefusedBy[i], query: query})
	}
	for i := range tree.Taken {
		kinds = append(kinds, countedKind{kind: &tree.Taken[i], query: query})
	}
	levels, err := countLevels(ctx, count, kinds, reach)
	if err != nil {
		return nil, err
	}
	start := &takenStart{query: query, levels: levels, branches: nil, kind: nil, rows: 0, digest: 0}
	if !levels.cut {
		return start, nil
	}
	if err := d.frontier(ctx, start, func(kind *DeleteKind, page []takenPageRow, under *takenUnder) error {
		for _, outer := range inside {
			if outer.table == under.table && maps.Equal(outer.rows, under.rows) {
				return errors.Newf("a %s under the %s is nested under itself", kind.Words.One, tree.Words.One)
			}
		}
		branch, err := d.countFrom(ctx, count, &takenQuery{tree: tree, key: query.key, under: under}, reach, append(slices.Clip(inside), under))
		if err != nil {
			return err
		}
		branch.kind, branch.rows, branch.digest = kind, len(page), pageDigest(page)
		branch.query.under = under.withoutKeys()
		start.branches = append(start.branches, branch)

		return nil
	}); err != nil {
		return nil, err
	}

	return start, nil
}

// frontier calls each with every page of a cut start's frontier, in order: for each
// taken kind whose table nests (DeleteTree.nestingLinks), in the description's order,
// with rows at the start's deepest level, those rows read by their keys alone, in key
// order, a page of takenPageRows at a time, each as the page a branch starts from
// (takenUnder). A branch is crossed where the start it is opened from is not one of the
// same table: the record, where the record's table is not the page's, or a page of
// another table. A page the database refuses for its joins at a level above 0 is a
// levelRefused.
func (d *takingDelete) frontier(ctx context.Context, start *takenStart, each func(kind *DeleteKind, page []takenPageRow, under *takenUnder) error) error {
	tree := d.query.tree
	level := start.levels.deepest
	for i := range tree.Taken {
		kind := &tree.Taken[i]
		table := string(kind.Resource)
		if len(tree.nestingLinks(table)) == 0 || start.levels.at(kind, level) == 0 {
			continue
		}
		_, types, err := d.keyFields(kind)
		if err != nil {
			return err
		}
		crossed := table != string(tree.Record)
		if start.query.under != nil {
			crossed = start.query.under.table != table
		}
		pageRows := d.pageRows
		if pageRows == 0 {
			pageRows = takenPageRows
		}
		for offset := 0; ; offset += pageRows {
			statement, ok := start.query.page(kind, level, start.levels.below(), nil, pageRows, offset)
			if !ok {
				break
			}
			page, err := d.page(ctx, statement, types, 0)
			if err != nil {
				return refusedAt(level, errors.Wrapf(err, "reading the %s under a %s", kind.Words.Many, tree.Words.One))
			}
			if len(page) == 0 {
				break
			}
			ids, err := pageKeys(page)
			if err != nil {
				return err
			}
			rows := make(map[string]bool, len(page))
			for _, row := range page {
				rows[fmt.Sprintf("%#v", row.key)] = true
			}
			if err := each(kind, page, &takenUnder{table: table, key: kind.Key, ids: ids, crossed: crossed, rows: rows}); err != nil {
				return err
			}
			if len(page) < pageRows {
				break
			}
		}
	}

	return nil
}

// walk is the second walk: it goes through the starts the first walk counted, a start's
// branches before the start's own levels, and calls each with every start, the record's
// last. For a cut start it reads each frontier page again with the statements the first
// walk read it with, in the same order, and walks the branch counted for it with the
// page's keys; a page that does not match the branch counted for it is an error. It holds
// one page of keys for each start it is inside, and no more. It ends where each says it
// is done, and reports whether it did.
func (d *takingDelete) walk(ctx context.Context, start *takenStart, each func(start *takenStart) (done bool, err error)) (bool, error) {
	if start.levels.cut {
		tree := d.query.tree
		next := 0
		err := d.frontier(ctx, start, func(kind *DeleteKind, page []takenPageRow, under *takenUnder) error {
			if next >= len(start.branches) || !start.branches[next].matches(kind, page) {
				return errors.Newf("a page of the %s under a %s is not the page that was counted", kind.Words.Many, tree.Words.One)
			}
			branch := start.branches[next]
			next++
			branch.query.under = under
			done, err := d.walk(ctx, branch, each)
			branch.query.under = under.withoutKeys()
			if err != nil {
				return err
			}
			if done {
				return walkEnded{}
			}

			return nil
		})
		if errors.As(err, new(walkEnded)) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if next != len(start.branches) {
			return false, errors.Newf("fewer pages of what is under a %s were read than were counted", tree.Words.One)
		}
	}

	return each(start)
}

// take checks the caller's grants on each taken kind with rows under the record, once no
// refusing row is in the way. A delete that takes the rows (everythingUnder) then counts
// the kinds code handed it and takes one round of them all (round). Without it every row
// of a kind that asks anything of its rows is asked, deepest level first and in the
// description's order within a level, each handed kind is counted and then asked
// (TakenKind.Check), and the person is asked the question. With no row under the record
// the delete goes on as a plain one.
func (d *takingDelete) take(ctx context.Context, start *takenStart, tracked map[accesstypes.Resource]bool, everythingUnder bool, eventSource ...eventsource.EventSource) (*takenRound, error) {
	tree := d.query.tree
	asks := make(map[*DeleteKind]*takenAsks, len(tree.Taken))
	var counted int64
	for i := range tree.Taken {
		kind := &tree.Taken[i]
		total := start.total(kind)
		if total == 0 {
			continue
		}
		counted += total
		armed := kind.Rows.kind(d.callerOf(kind))
		if err := armed.checkPermissions(ctx, d.txn.DBType()); err != nil {
			return nil, d.asked.refuseRow(ctx, kind, false, err)
		}
		kindAsks, err := armed.takenAsks(d.txn.DBType())
		if err != nil {
			return nil, err
		}
		asks[kind] = kindAsks
	}
	if everythingUnder {
		handed, err := d.countAlso(ctx)
		if err != nil {
			return nil, err
		}
		for _, rows := range handed {
			counted += rows
		}
		if counted == 0 {
			return &takenRound{finished: true}, nil
		}

		return d.round(ctx, start, asks, handed, counted, tracked, eventSource...)
	}

	if _, err := d.walk(ctx, start, func(at *takenStart) (bool, error) {
		return false, d.eachLevel(at.levels, func(kind *DeleteKind, level int) error {
			if !asks[kind].asksRows() {
				return nil
			}

			return d.rowsAt(ctx, at.query, kind, level, at.levels.below(), asks[kind], func(taken takenDelete, answers []spanner.NullBool) error {
				byRule, err := taken.row.checkTaken(ctx, d.txn, asks[kind], answers)
				if err != nil {
					return d.asked.refuseRow(ctx, kind, byRule, err)
				}

				return nil
			})
		})
	}); err != nil {
		return nil, err
	}
	handed, err := d.countAlso(ctx)
	if err != nil {
		return nil, err
	}
	for _, rows := range handed {
		counted += rows
	}
	if counted == 0 {
		return &takenRound{finished: true}, nil
	}
	for _, kind := range d.also {
		if err := kind.Check(ctx, d.txn); err != nil {
			return nil, errors.Wrapf(err, "checking the %s under a %s", kind.Words().Many, tree.Words.One)
		}
	}
	question, err := d.asked.question(ctx, start, d.handedRows(handed))
	if err != nil {
		return nil, err
	}

	return nil, httpio.NewPreconditionRequiredMessage(question)
}

// countAlso counts the rows of each kind code handed the delete, in the order handed.
func (d *takingDelete) countAlso(ctx context.Context) ([]int64, error) {
	counts := make([]int64, 0, len(d.also))
	for _, kind := range d.also {
		rows, err := kind.Count(ctx, d.txn)
		if err != nil {
			return nil, errors.Wrapf(err, "counting the %s under a %s", kind.Words().Many, d.query.tree.Words.One)
		}
		counts = append(counts, rows)
	}

	return counts, nil
}

// handedRows are the kinds code handed the delete as the question counts them, in the
// order handed, with the kinds that share their words counted together and the kinds
// with no row left out (handed, by kind).
func (d *takingDelete) handedRows(handed []int64) []kindRows {
	var counted []kindRows
	for i, kind := range d.also {
		if handed[i] == 0 {
			continue
		}
		words := kind.Words()
		at := slices.IndexFunc(counted, func(k kindRows) bool { return k.kind.Words == words })
		if at < 0 {
			counted = append(counted, kindRows{kind: &DeleteKind{Words: words, Trusted: true}})
			at = len(counted) - 1
		}
		counted[at].rows += handed[i]
	}

	return counted
}

// round takes what fits one save, from the deepest level to level 0 and in the
// description's order within a level. The save's size is d.size less what the record's
// own row costs; for each kind with rows at a level, the most rows it may take is what
// is left of the size over what one of them costs, read in one page, each asked where
// the kind asks anything (asks) and buffered. None left, or a page that came back full,
// ends the round with the record left standing; a round that reaches the end takes the
// record too, once each kind code handed the delete has had what is left of the size
// (TakenKind.Take), in the order handed: one that says rows of it are left ends the round
// there. handed is each handed kind's count, and counted the rows counted under the
// record, the handed kinds' among them. A round that can buffer nothing at all, neither a
// row of the description's kinds nor a mutation of a handed kind, is an error: one row,
// or a handed kind taken the only way it is taken, costs more than a save holds.
func (d *takingDelete) round(ctx context.Context, start *takenStart, asks map[*DeleteKind]*takenAsks, handed []int64, counted int64, tracked map[accesstypes.Resource]bool, eventSource ...eventsource.EventSource) (*takenRound, error) {
	tree := d.query.tree
	size := d.size
	if size == 0 {
		size = DeleteMutationLimit
	}
	record := ChangeRecordMutations(eventSource...)
	changeRecord := func(res accesstypes.Resource) int {
		if tracked[res] {
			return record
		}

		return 0
	}
	taken := make(map[accesstypes.Resource]int, len(tree.Taken))
	round := &takenRound{counted: counted, deleted: 0, finished: false}
	full, err := d.walk(ctx, start, func(at *takenStart) (bool, error) {
		for level := at.levels.deepest; level >= 0; level-- {
			for i := range tree.Taken {
				kind := &tree.Taken[i]
				if at.levels.at(kind, level) == 0 {
					continue
				}
				most := (size - tree.Mutations(taken, changeRecord)) / kind.Cost.Mutations(changeRecord(kind.Resource))
				if most <= 0 {
					if round.deleted == 0 {
						return false, errors.Newf("the delete of a %s can take nothing in one save: one of its %s costs more than a save holds", tree.Words.One, kind.Words.Many)
					}

					return true, nil
				}
				rows, err := d.takeAt(ctx, at.query, kind, level, at.levels.below(), asks[kind], most, eventSource...)
				if err != nil {
					return false, err
				}
				taken[kind.Resource] += rows
				round.deleted += int64(rows)
				if rows == most {
					return true, nil
				}
			}
		}

		return false, nil
	})
	if refused := new(levelRefused); errors.As(err, &refused) && round.deleted > 0 {
		// A save that has buffered rows cannot start again: it ends as one that filled.
		return round, nil
	}
	if err != nil {
		return nil, err
	}
	if full {
		return round, nil
	}
	spent := tree.Mutations(taken, changeRecord)
	before := spent
	for i, kind := range d.also {
		if handed[i] == 0 {
			continue
		}
		budget := size - spent
		if budget <= 0 {
			return round, nil
		}
		rows, mutations, more, err := kind.Take(ctx, d.txn, budget, eventSource...)
		if err != nil {
			return nil, errors.Wrapf(err, "deleting the %s under a %s", kind.Words().Many, tree.Words.One)
		}
		if more && rows == 0 && mutations == 0 && round.deleted == 0 && spent == before {
			return nil, errors.Newf("the delete of a %s can take nothing in one save: its %s cost more than a save holds, taken the only way they are taken", tree.Words.One, kind.Words().Many)
		}
		round.deleted += rows
		spent += mutations
		if more {
			return round, nil
		}
	}
	round.finished = true

	return round, nil
}

// takeAt takes at most most of the kind's rows at the level, below the deepest level, in
// key order, read in one page: each row is asked what its own delete asks where the kind
// asks anything of its rows (asks), and once every row of the page has passed, each is
// buffered. It reports how many it took.
func (d *takingDelete) takeAt(ctx context.Context, query *takenQuery, kind *DeleteKind, level, deepest int, asks *takenAsks, most int, eventSource ...eventsource.EventSource) (int, error) {
	fields, types, err := d.keyFields(kind)
	if err != nil {
		return 0, err
	}
	if !asks.asksRows() {
		asks = nil
	}
	answers := 0
	if asks != nil {
		answers = len(asks.terms)
	}
	statement, ok := query.page(kind, level, deepest, asks, most, 0)
	if !ok {
		return 0, nil
	}
	rows, err := d.page(ctx, statement, types, answers)
	if err != nil {
		return 0, refusedAt(level, errors.Wrapf(err, "reading the %s under a %s", kind.Words.Many, d.query.tree.Words.One))
	}
	taken := make([]takenRow, 0, len(rows))
	for _, read := range rows {
		row := kind.Rows.row(fields, read.key, d.callerOf(kind))
		if asks != nil {
			if byRule, err := row.checkTaken(ctx, d.txn, asks, read.answers); err != nil {
				return 0, d.asked.refuseRow(ctx, kind, byRule, err)
			}
		}
		taken = append(taken, row)
	}
	for _, row := range taken {
		if err := row.bufferTaken(ctx, d.txn, eventSource...); err != nil {
			return 0, errors.Wrapf(err, "deleting a %s under a %s", kind.Words.One, d.query.tree.Words.One)
		}
	}

	return len(taken), nil
}

// eachLevel calls each with every taken kind that has rows at a level, from the deepest
// level to level 0, and in the description's order within a level.
func (d *takingDelete) eachLevel(levels *takenLevels, each func(kind *DeleteKind, level int) error) error {
	for level := levels.deepest; level >= 0; level-- {
		for i := range d.query.tree.Taken {
			kind := &d.query.tree.Taken[i]
			if levels.at(kind, level) == 0 {
				continue
			}
			if err := each(kind, level); err != nil {
				return err
			}
		}
	}

	return nil
}

// takenRecord is what the delete of a record that takes the rows under it reads of the
// record before it runs (PatchSet.takes): the description the delete reads, the
// transaction as Spanner's, the record's key by column, and whether each taken kind's
// changes are recorded (takenTracked).
type takenRecord struct {
	tree    *DeleteTree
	txn     *SpannerReadWriteTransaction
	key     map[string]any
	tracked map[accesstypes.Resource]bool
}

// deleteTaken takes the rows the record's description lists as going with it, before
// the record's own row is deleted: the whole procedure this file opens with. The round
// says what the save took and whether it took the last row under the record, so the
// record goes too; nil says the rows under the record were not answered for here, and
// are counted before the commit. It does nothing for a record that lists none, for a
// description written before its kinds had their deletes (DeleteKind.Rows), and on a
// transaction that is not Spanner's (the Mock's has no rows to read; PostgreSQL has no
// write path): the count before the commit answers for those.
//
// The rows are read as the database held them before the transaction: a row the same
// transaction changed is found where it stood, and a row it deleted is found and deleted
// again. The answers come in this order: a record with rows that refuse its delete
// (refusedBy), counted level by level as the taken kinds are, is refused in the
// sentence that names them; then a record with a row straight under it that the
// description neither takes nor lists under refusedBy, in the counted sentence of a
// plain delete (refusedUnder); then a row of a kind marked WithinReach that the
// permission the request was let in by does not reach, in the sentence that says where
// it is (refusedBeyondReach); then a kind with rows under the record that the caller's
// grants refuse; then the first row of a kind that asks anything of its rows that is
// refused, each row asked as its own delete would ask it: every row by a delete that
// asks, the rows the save takes by one that takes them; then a kind code handed the
// delete (DeleteAlso) that refuses. A row is checked as the person the record's own
// delete is armed against, in that delete's scope, and runs trusted where the record's
// delete does, or where its kind is trusted; a row of a kind marked AsCaller is checked
// as whoever the delete is for (callerOf). The sentences are written for whoever asked
// (deleteCaller).
func (p *PatchSet[Resource]) deleteTaken(ctx context.Context, txn ReadWriteTransaction, eventSource ...eventsource.EventSource) (*takenRound, error) {
	record := p.takes(txn)
	if record == nil {
		return nil, nil
	}
	tree, spannerTxn, key, tracked := record.tree, record.txn, record.key, record.tracked

	count, page, byTenant := rowCounter(spannerTxn.countRows), spannerTxn.readPage, spannerTxn.countByTenant
	if refuses := p.deleteRefuses; refuses != nil {
		count = func(ctx context.Context, statement rowsUnderStatement) (int64, error) {
			if err := refuses(statement.SQL); err != nil {
				return 0, err
			}

			return spannerTxn.countRows(ctx, statement)
		}
		page = func(ctx context.Context, statement rowsUnderStatement, types []reflect.Type, answers int) ([]takenPageRow, error) {
			if err := refuses(statement.SQL); err != nil {
				return nil, err
			}

			return spannerTxn.readPage(ctx, statement, types, answers)
		}
		byTenant = func(ctx context.Context, statement rowsUnderStatement) ([]tenantRows, error) {
			if err := refuses(statement.SQL); err != nil {
				return nil, err
			}

			return spannerTxn.countByTenant(ctx, statement)
		}
	}
	taking := &takingDelete{
		asked:    &recordedDelete{tree: tree, key: key, caller: p.deleteCaller(ctx)},
		query:    &takenQuery{tree: tree, key: key, under: nil},
		txn:      spannerTxn,
		caller:   p.querySet.Caller(),
		page:     page,
		byTenant: byTenant,
		size:     p.deleteSaveSize,
		also:     p.alsoTaken,
	}

	return taking.run(ctx, count, tracked, p.everythingUnder, eventSource...)
}

// takes is what deleteTaken reads of the patch's delete before it runs (takenRecord):
// nil where deleteTaken does not answer for the rows under the record, for a record that
// lists no rows to take and was handed no kind, on a transaction that is not Spanner's,
// and for a key or kinds it cannot read.
func (p *PatchSet[Resource]) takes(txn ReadWriteTransaction) *takenRecord {
	tree := p.deleteDescription()
	if tree == nil || len(tree.Taken) == 0 && len(p.alsoTaken) == 0 {
		return nil
	}
	spannerTxn, ok := txn.(*SpannerReadWriteTransaction)
	if !ok {
		return nil
	}
	key, ok := p.keyByColumn(txn.DBType())
	if !ok {
		return nil
	}
	tracked, ok := takenTracked(tree, p.querySet.rMeta.trackChanges)
	if !ok {
		return nil
	}

	return &takenRecord{tree: tree, txn: spannerTxn, key: key, tracked: tracked}
}

// deleteDescription is the description the patch's delete reads: its record's, with
// every kind marked AsCaller or WithinReach left out of each of its lists where the
// delete takes the library's rows alone (DeleteLibraryRowsAlone). The record's own
// description is not changed.
func (p *PatchSet[Resource]) deleteDescription() *DeleteTree {
	tree := p.querySet.rMeta.deleteTree
	if tree == nil || !p.libraryRowsAlone {
		return tree
	}
	library := func(kinds []DeleteKind) []DeleteKind {
		return slices.DeleteFunc(slices.Clone(kinds), func(kind DeleteKind) bool { return kind.AsCaller || kind.WithinReach })
	}
	narrowed := *tree
	narrowed.Taken, narrowed.RefusedBy, narrowed.Under = library(tree.Taken), library(tree.RefusedBy), library(tree.Under)

	return &narrowed
}

// takenTracked reports whether each taken kind's changes are recorded, and the record's
// own (record), for the size of the delete. ok is false for a description whose taken
// kinds do not all have their deletes (DeleteKind.Rows).
func takenTracked(tree *DeleteTree, record bool) (tracked map[accesstypes.Resource]bool, ok bool) {
	tracked = map[accesstypes.Resource]bool{tree.Record: record}
	for i := range tree.Taken {
		kind := &tree.Taken[i]
		if kind.Rows == nil {
			return nil, false
		}
		if kind.Resource == tree.Record {
			continue
		}
		tracked[kind.Resource] = kind.Rows.tracked()
	}

	return tracked, true
}

// refusedNow refuses the delete of a record that has rows of a kind its description
// lists under refusedBy, in the sentence that names them. The rows are the ones under
// the record as the database held them before the transaction, at every level.
func (d *recordedDelete) refusedNow(ctx context.Context, levels takenTotals) error {
	var refusing []kindRows
	for i := range d.tree.RefusedBy {
		kind := &d.tree.RefusedBy[i]
		if rows := levels.total(kind); rows > 0 {
			refusing = append(refusing, kindRows{kind: kind, rows: rows})
		}
	}
	if len(refusing) == 0 {
		return nil
	}
	seen, err := d.seen(ctx, refusing)
	if err != nil {
		return err
	}

	return httpio.NewConflictMessage(deleteRefusedSentence(d.tree, seen, true))
}

// refusedUnder refuses the delete of a record that has a row straight under it by a way
// of a kind under it (DeleteTree.Under) that is a way of no kind of its resource the
// description takes or lists under refusedBy, as the count before the commit refuses a
// plain delete: in the counted sentence ("This task has 1 timesheet. Delete it first."),
// before any row is asked about. Each such kind is counted by those ways alone, in one
// statement; a kind with none sends no statement.
func (d *recordedDelete) refusedUnder(ctx context.Context, dbType DBType, count rowCounter) error {
	var unheld []DeleteKind
	for i := range d.tree.Under {
		kind := d.tree.Under[i]
		kind.Paths = slices.DeleteFunc(slices.Clone(kind.Paths), func(way DeletePath) bool {
			return d.tree.holds(kind.Resource, way)
		})
		if len(kind.Paths) > 0 {
			unheld = append(unheld, kind)
		}
	}
	if len(unheld) == 0 {
		return nil
	}
	under, err := d.rowsOf(ctx, dbType, unheld, count)
	if err != nil {
		return err
	}
	if len(under) == 0 {
		return nil
	}
	seen, err := d.seen(ctx, under)
	if err != nil {
		return err
	}

	return httpio.NewConflictMessage(deleteRefusedSentence(d.tree, seen, false))
}

// refuseRow is the answer to a delete over a kind under the record that refused: a
// refusal that is the row's own (byRule: its question's or its rule's) in its own
// sentence; one of the person's grants, a row they may not delete or may not reach, in
// a sentence that names the kind where the person may see it.
func (d *recordedDelete) refuseRow(ctx context.Context, kind *DeleteKind, byRule bool, err error) error {
	switch {
	case byRule:
		return err
	case !httpio.HasForbidden(err) && !httpio.HasNotFound(err):
		return errors.Wrapf(err, "checking a %s under a %s", kind.Words.One, d.tree.Words.One)
	}

	seen, serr := d.seen(ctx, []kindRows{{kind: kind, rows: 1}})
	if serr != nil {
		return serr
	}

	return httpio.NewForbiddenMessageWithError(err, deleteNotAllowedSentence(d.tree, kind, len(seen) > 0))
}

// question is what the person is asked before the delete takes the rows counted: the
// kinds they may see, each with its count, in the delete order read backward (the kind
// nearest the record first, and kinds equally near by name), then the kinds code handed
// the delete (handed), which every caller is told.
func (d *recordedDelete) question(ctx context.Context, levels takenTotals, handed []kindRows) (string, error) {
	var kinds []kindRows
	for i := range slices.Backward(d.tree.Taken) {
		kind := &d.tree.Taken[i]
		if rows := levels.total(kind); rows > 0 {
			kinds = append(kinds, kindRows{kind: kind, rows: rows})
		}
	}
	seen, err := d.seen(ctx, kinds)
	if err != nil {
		return "", err
	}

	return deleteQuestion(d.tree, append(seen, handed...), len(seen) < len(kinds)), nil
}

// deleteQuestion is the sentence a person is asked before a delete takes the rows under
// a record: "Delete this client and everything under it: 14 projects, 230 tasks, 41
// documents. This can't be undone." Kinds the person may not see are not counted for
// them (unseen).
func deleteQuestion(tree *DeleteTree, seen []kindRows, unseen bool) string {
	ask := "Delete " + tree.rowNameInside() + " and everything under it"
	counts := make([]string, 0, len(seen))
	for _, k := range seen {
		counts = append(counts, k.kind.Words.Count(int(k.rows)))
	}

	switch {
	case len(counts) == 0:
		return ask + "? This can't be undone."
	case unseen:
		return ask + ": " + strings.Join(counts, ", ") + ", and records you can't see. This can't be undone."
	default:
		return ask + ": " + strings.Join(counts, ", ") + ". This can't be undone."
	}
}

// deleteNotAllowedSentence is the sentence that refuses a delete over a row under the
// record that the person may not delete. It names the kind where they may see it.
func deleteNotAllowedSentence(tree *DeleteTree, kind *DeleteKind, seen bool) string {
	if !seen {
		return tree.RowName() + " has records under it that you can't delete, so it can't be deleted."
	}

	return tree.RowName() + " has " + kind.Words.Many + " that you can't delete, so it can't be deleted."
}
