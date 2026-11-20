package resource

import (
	"context"
	"fmt"
	"math/big"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/jackc/pgx/v5"
)

// This file answers two questions about one named person and the application's
// subject sets, for a rule that must hold whoever the session's permissions are
// read from (a person acting as someone else is still themselves to the rule that
// stops them giving themselves something):
//
//   - is a value in one of the person's sets (the squadron a hangar pass is given
//     to, asked of the squadrons the person is in), and
//   - is one of the person's sets, or one of their subject values, read through
//     this row's column (the squadron whose wing decides which wings the person is
//     in).
//
// Both statements are built from the set's own declaration by the pieces a
// condition's membership test is rendered with (subjectSetExists, subjectSetRows,
// pathTarget), so who is in a set is defined once: a set that changes its who: or
// its where: changes these answers with it. The person is named, never read from
// the request, and the rows are read in no one tenant: a membership held in any
// tenant counts, which is the strict reading of a rule about a person.
//
// The questions are asked on both databases the library supports: on Spanner
// through the transaction's own reads, on PostgreSQL through the connection the
// PostgreSQL transaction holds, each in its own dialect of the one statement.

// SubjectSetHolds reports whether value is in the subject set named set for the
// person subject, by the set's own definition, read inside the write's
// transaction. It is for a rule about a person other than the one the request's
// permissions belong to; a condition that tests the caller writes
// `x IN subject.<set>` and needs none of this.
//
// The patch must come from a decoder that knows the application's collection. A
// patch with none, and a transaction with no connection to ask on, are errors and
// never a pass: a rule that could not ask does not let the write through.
func (p *PatchSet[Resource]) SubjectSetHolds(ctx context.Context, txn ReadWriteTransaction, set string, subject accesstypes.User, value any) (bool, error) {
	collection, err := p.reachCollection()
	if err != nil {
		return false, err
	}
	sql, registry, err := subjectSetHoldsStatement(collection, txn.DBType(), set, value)
	if err != nil {
		return false, err
	}

	holds, err := p.askReach(ctx, txn, sql, registry, subject)
	if err != nil {
		return false, errors.Wrapf(err, "asking whether a value is in subject.%s", set)
	}

	return holds, nil
}

// SubjectReadThrough reports whether the fact about a person named name, a
// subject set or a subject value, is read for the person subject through the
// column field of the row key names: the fact's value is declared as
// a path (`value: F.G`), one hop of that path lands on this record and reads this
// column, and a row of the person's reaches this very row. A change that sets the
// column then changes what the person's set holds, without touching a row that
// names them.
//
// A name that is no fact of the application, and a fact that is not read through
// this record's column, are errors: the generated code names the pair from the
// same declarations, so a mismatch is a stale build and not a pass. So are a
// patch with no collection and a transaction with no connection to ask on.
func (p *PatchSet[Resource]) SubjectReadThrough(ctx context.Context, txn ReadWriteTransaction, name string, field accesstypes.Field, subject accesstypes.User, key KeySet) (bool, error) {
	collection, err := p.reachCollection()
	if err != nil {
		return false, err
	}

	fields := p.querySet.rMeta.dbFieldMap(txn.DBType())
	column, ok := fields[field]
	if !ok {
		return false, errors.Newf("%s has no stored column for the field %s, so subject.%s cannot be read through it", p.Resource(), field, name)
	}
	parts := key.Parts()
	if len(parts) == 0 {
		return false, errors.Newf("the change to %s names no row, so whether subject.%s is read through it cannot be asked", p.Resource(), name)
	}
	located := make([]reachKeyColumn, 0, len(parts))
	for _, part := range parts {
		keyColumn, ok := fields[part.Key]
		if !ok {
			return false, errors.Newf("%s has no stored column for its key field %s", p.Resource(), part.Key)
		}
		located = append(located, reachKeyColumn{column: keyColumn.ColumnName, value: part.Value})
	}

	sql, registry, err := subjectReadThroughStatement(collection, txn.DBType(), name, string(p.Resource()), column.ColumnName, located)
	if err != nil {
		return false, err
	}

	reads, err := p.askReach(ctx, txn, sql, registry, subject)
	if err != nil {
		return false, errors.Wrapf(err, "asking whether subject.%s is read through %s.%s", name, p.Resource(), field)
	}

	return reads, nil
}

// reachCollection returns the collection the patch was decoded with, and refuses
// a patch that carries none.
func (p *PatchSet[Resource]) reachCollection() (*GeneratedCollection, error) {
	if p.querySet.collection == nil {
		return nil, errors.Newf("the change to %s carries no collection, so a subject set cannot be asked about: decode the patch with a decoder built from the application's collection", p.Resource())
	}

	return p.querySet.collection, nil
}

// askReach runs one of this file's statements for the person subject and reads its
// one boolean, on the database the transaction runs on. The instant a set's where:
// reads as now is the patch's own, the one its permission check folded with, or the
// present for a patch that carries none.
func (p *PatchSet[Resource]) askReach(ctx context.Context, txn ReadWriteTransaction, sql string, registry *paramRegistry, subject accesstypes.User) (bool, error) {
	env := p.querySet.env
	if _, ok := env.Now(); !ok {
		env = RequestEnvironment()
	}
	params, err := subjectFactParams(registry, subject, accesstypes.GlobalScope(), env)
	if err != nil {
		return false, err
	}

	var rows []any
	switch dbType := txn.DBType(); dbType {
	case SpannerDBType:
		rows, err = querySpannerFact(ctx, txn.SpannerReadOnlyTransaction(), spanner.Statement{SQL: sql, Params: params})
	case PostgresDBType:
		rows, err = queryPostgresReach(ctx, txn.PostgresReadOnlyTransaction(), sql, params)
	default:
		return false, errors.Newf("asking a subject set about a named person is not built for the database %s", dbType)
	}
	if err != nil {
		return false, err
	}
	if len(rows) != 1 {
		return false, errors.Newf("the statement answered %d rows, want one", len(rows))
	}
	answer, ok := rows[0].(bool)
	if !ok {
		return false, errors.Newf("the statement answered %T, want a boolean", rows[0])
	}

	return answer, nil
}

// queryPostgresReach runs one of this file's statements on what a PostgreSQL
// transaction reads through and returns its one boolean column, row by row. A
// transaction that hands over nothing to run a statement on is an error, never an
// answer.
func queryPostgresReach(ctx context.Context, source any, sql string, params map[string]any) ([]any, error) {
	querier, ok := source.(postgresQuerier)
	if !ok {
		return nil, errors.Newf("the PostgreSQL transaction holds no connection a statement can be run on (it handed over %T), so a subject set cannot be asked about", source)
	}

	rows, err := querier.Query(ctx, sql, postgresReachArgs(params))
	if err != nil {
		return nil, errors.Wrap(err, "postgresQuerier.Query()")
	}
	defer rows.Close()

	var values []any
	for rows.Next() {
		var value bool
		if err := rows.Scan(&value); err != nil {
			return nil, errors.Wrap(err, "pgx.Rows.Scan()")
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "pgx.Rows.Err()")
	}

	return values, nil
}

// postgresReachArgs names a statement's parameters for PostgreSQL. A value is bound
// as it is, which the driver sends as the type of the column it is compared with
// (an id that knows its own text form goes as that text); an exact decimal, which
// the library holds as the Spanner client's type for it, goes as its text.
func postgresReachArgs(params map[string]any) pgx.NamedArgs {
	args := make(pgx.NamedArgs, len(params))
	for name, value := range params {
		if rat, ok := value.(*big.Rat); ok {
			args[name] = rat.FloatString(numericScale)
		} else {
			args[name] = value
		}
	}

	return args
}

// reachKeyColumn is one column of the primary key that names the row a
// read-through question is about, and its value.
type reachKeyColumn struct {
	column string
	value  any
}

// subjectSetHoldsStatement renders the statement that answers whether value is in
// subject.<set> for the person bound as @subject: the EXISTS a condition's
// `x IN subject.<set>` renders, with the value where the row's column stands, in
// no one tenant.
func subjectSetHoldsStatement(collection *GeneratedCollection, dbType DBType, set string, value any) (string, *paramRegistry, error) {
	gen, err := loweredSQLGenerator(dbType)
	if err != nil {
		return "", nil, err
	}
	registry := newParamRegistry()
	gen.registry = registry
	lctx := &loweringContext{collection: collection}

	attr := boundComparand(registry.bind(value))
	holds, err := lctx.subjectSetExists(set, &attr, registry)
	if err != nil {
		return "", nil, err
	}
	sql, _, err := gen.generateSQLRecursive(holds)
	if err != nil {
		return "", nil, err
	}

	return fmt.Sprintf("SELECT %s AS %s", sql, gen.quoteIdentifier(subjectFactColumn)), registry, nil
}

// subjectReadThroughStatement renders the statement that answers whether the fact
// subject.<name> is read, for the person bound as @subject, through the column of
// the row of table that key names. The row is found by its key, and for each hop
// of the fact's path that lands on table and reads column, a row of the person's
// (the fact's own who: and where:) must arrive at it: straight from the fact's
// column for the first hop, through the hops before it otherwise.
func subjectReadThroughStatement(collection *GeneratedCollection, dbType DBType, name, table, column string, key []reachKeyColumn) (string, *paramRegistry, error) {
	anchor, ok := collection.SubjectSet(name)
	if !ok {
		if anchor, ok = collection.SubjectValue(name); !ok {
			return "", nil, errors.Newf("subject.%s is not a declared subject set or subject value", name)
		}
	}

	gen, err := loweredSQLGenerator(dbType)
	if err != nil {
		return "", nil, err
	}
	registry := newParamRegistry()
	gen.registry = registry
	lctx := &loweringContext{collection: collection}

	row := registry.alias()
	located := make([]ExpressionNode, 0, len(key)+1)
	for _, part := range key {
		located = append(located, &loweredComparisonNode{left: columnComparand(row, part.column), op: "=", right: boundComparand(registry.bind(part.value))})
	}

	var through ExpressionNode
	for k, hop := range anchor.Binding.Path {
		if hop.Table != table || hop.Column != column {
			continue
		}
		alias := registry.alias()
		rows, err := lctx.subjectSetRows(&anchor, alias, registry)
		if err != nil {
			return "", nil, err
		}
		anchorColumn := columnComparand(alias, anchor.Binding.Column)
		arrives := ExpressionNode(&loweredComparisonNode{left: anchorColumn, op: "=", right: columnComparand(row, hop.JoinColumn)})
		if k > 0 {
			target, wrap := lctx.pathTarget(&anchorColumn, anchor.Binding.Path[:k], registry)
			arrives = wrap(&loweredComparisonNode{left: target, op: "=", right: columnComparand(row, hop.JoinColumn)})
		}
		reached := ExpressionNode(&existsNode{table: string(anchor.Resource), alias: alias, where: andChain(rows, arrives)})
		if through == nil {
			through = reached

			continue
		}
		through = &LogicalOpNode{Left: through, Operator: OperatorOr, Right: reached}
	}
	if through == nil {
		return "", nil, errors.Newf("subject.%s is not read through %s.%s: no hop of its value's path lands on that column", name, table, column)
	}
	if _, several := through.(*LogicalOpNode); several {
		through = &GroupNode{Expression: through}
	}
	located = append(located, through)

	sql, _, err := gen.generateSQLRecursive(&existsNode{table: table, alias: row, where: andChain(located...)})
	if err != nil {
		return "", nil, err
	}

	return fmt.Sprintf("SELECT %s AS %s", sql, gen.quoteIdentifier(subjectFactColumn)), registry, nil
}
