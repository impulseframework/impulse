package resource

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"cloud.google.com/go/civil"
	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/spxscan/spxapi"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/structpb"
)

// A condition on a computed resource may compare its rows against a fact about
// the caller that is held in a table: a subject value (subject.<name>, one value
// off the caller's own anchor row) or a subject set (attr IN subject.<name>, the
// values the caller's anchor rows yield).
//
// A table's statement reads such a fact as a subquery beside the rows it selects
// (lowering.go). A computed resource's rows come from the application's function
// and through no statement, so the facts are read once for the request, when it
// is decoded, by statements built from the same pieces the lowering uses: the
// anchor table, the caller's rows on it, the anchor's own tenancy filter where
// both the request and the anchor are partitioned, and a dotted value hop by
// hop. The evaluator then judges every row the function yields against them
// (computed_conditions.go), so a request costs one read for each fact its
// conditions name, whatever the number of rows.

// SubjectFactSource is the database a computed decoder reads the caller's facts
// from: the application's resource client.
type SubjectFactSource interface {
	DBType() DBType
	SpannerReadOnlyTransaction() spxapi.Querier
}

// subjectFactColumn is the output column of a fact's statement.
const subjectFactColumn = "fact"

// subjectFactStatement renders the statement that reads one fact about the
// caller. A subject value's statement yields one row: the value, or NULL where
// the caller has no anchor row, a hop reaches no row, or the column is NULL. A
// subject set's yields one row for each of the caller's anchor rows, restricted
// to the request's partition where the anchor is partitioned, the value NULL
// where the row's column, or a hop of its dotted path, has none.
func subjectFactStatement(collection *GeneratedCollection, dbType DBType, partitioned bool, name string, set bool) (string, *paramRegistry, error) {
	gen, err := loweredSQLGenerator(dbType)
	if err != nil {
		return "", nil, err
	}
	registry := newParamRegistry()
	gen.registry = registry
	lctx := &loweringContext{collection: collection, partitioned: partitioned}
	column := gen.quoteIdentifier(subjectFactColumn)

	if !set {
		subquery, err := lctx.subjectValueSubquery(name, registry)
		if err != nil {
			return "", nil, err
		}
		value, err := gen.renderScalarSubquery(subquery)
		if err != nil {
			return "", nil, err
		}

		return fmt.Sprintf("SELECT %s AS %s", value, column), registry, nil
	}

	anchor, ok := collection.SubjectSet(name)
	if !ok {
		return "", nil, errors.Newf("subject.%s is not a declared subject set", name)
	}
	alias := registry.alias()
	var where ExpressionNode = &loweredComparisonNode{
		left:  columnComparand(alias, anchor.Binding.UserColumn),
		op:    "=",
		right: namedComparand(subjectParamName),
	}
	where = lctx.withAnchorTenancy(where, &anchor, alias, registry)

	value := columnComparand(alias, anchor.Binding.Column)
	if len(anchor.Binding.Path) > 0 {
		value = pathScalar(&value, anchor.Binding.Path, registry)
	}
	valueSQL, err := gen.renderComparand(&value)
	if err != nil {
		return "", nil, err
	}
	whereSQL, _, err := gen.generateSQLRecursive(where)
	if err != nil {
		return "", nil, err
	}

	return fmt.Sprintf("SELECT %s AS %s FROM %s %s WHERE %s", valueSQL, column, gen.quoteIdentifier(string(anchor.Resource)), gen.quoteIdentifier(alias), whereSQL), registry, nil
}

// subjectFactParams supplies the reserved parameters a fact's statement names: the
// caller, and the request's partition where the anchor is filtered to it.
func subjectFactParams(registry *paramRegistry, subject accesstypes.User, scope accesstypes.Scope) (map[string]any, error) {
	params := make(map[string]any)
	for _, param := range registry.boundParams() {
		params[param.Name] = param.Value
	}
	for _, name := range registry.referencedNames() {
		switch name {
		case subjectParamName:
			params[name] = string(subject)
		case domainParamName:
			domain, ok := scope.Domain()
			if !ok {
				return nil, errors.New("a fact about the caller is filtered to the request's partition in a global request")
			}
			params[name] = string(domain)
		default:
			return nil, errors.Newf("a fact about the caller references unknown named parameter %q", name)
		}
	}

	return params, nil
}

// readSubjectFacts reads the facts about the caller that a computed resource's
// conditions name: each subject set as the non-null values the caller's anchor
// rows yield, each subject value as its one value, absent where there is none.
func readSubjectFacts(ctx context.Context, source SubjectFactSource, collection *GeneratedCollection, scope accesstypes.Scope, subject accesstypes.User, sets, values []string) (setValues map[string][]any, scalarValues map[string]any, err error) {
	if source.DBType() != SpannerDBType {
		return nil, nil, errors.Newf("a condition on a computed resource reads a fact about the caller held in a table, and reading one is implemented for %s alone (the database is %s)", SpannerDBType, source.DBType())
	}
	_, partitioned := scope.Domain()

	read := func(name string, set bool) ([]any, error) {
		sql, registry, err := subjectFactStatement(collection, SpannerDBType, partitioned, name, set)
		if err != nil {
			return nil, err
		}
		params, err := subjectFactParams(registry, subject, scope)
		if err != nil {
			return nil, err
		}
		rows, err := querySpannerFact(ctx, source.SpannerReadOnlyTransaction(), spanner.Statement{SQL: sql, Params: params})
		if err != nil {
			return nil, errors.Wrapf(err, "reading subject.%s", name)
		}

		return rows, nil
	}

	setValues = make(map[string][]any, len(sets))
	for _, name := range sets {
		rows, err := read(name, true)
		if err != nil {
			return nil, nil, err
		}
		members := make([]any, 0, len(rows))
		for _, value := range rows {
			if value != nil {
				members = append(members, value)
			}
		}
		setValues[name] = members
	}

	scalarValues = make(map[string]any, len(values))
	for _, name := range values {
		rows, err := read(name, false)
		if err != nil {
			return nil, nil, err
		}
		if len(rows) == 1 && rows[0] != nil {
			scalarValues[name] = rows[0]
		}
	}

	return setValues, scalarValues, nil
}

// querySpannerFact runs a fact's statement and returns its one column, row by row.
func querySpannerFact(ctx context.Context, querier spxapi.Querier, stmt spanner.Statement) ([]any, error) {
	it := querier.Query(ctx, stmt)
	defer it.Stop()

	var values []any
	for {
		row, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return values, nil
		}
		if err != nil {
			return nil, errors.Wrap(err, "spanner.RowIterator.Next()")
		}
		var column spanner.GenericColumnValue
		if err := row.Column(0, &column); err != nil {
			return nil, errors.Wrap(err, "spanner.Row.Column()")
		}
		value, err := spannerFactValue(column)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
}

// spannerFactValue converts a column value to the value the condition evaluator
// compares, by the column's own type: a string, an int64, a float64, an exact
// decimal, a bool, an instant or a date; nil for NULL.
func spannerFactValue(column spanner.GenericColumnValue) (any, error) {
	if _, null := column.Value.GetKind().(*structpb.Value_NullValue); null {
		return nil, nil
	}

	var err error
	switch code := column.Type.GetCode(); code {
	case sppb.TypeCode_STRING:
		var v string
		if err = column.Decode(&v); err == nil {
			return v, nil
		}
	case sppb.TypeCode_INT64:
		var v int64
		if err = column.Decode(&v); err == nil {
			return v, nil
		}
	case sppb.TypeCode_FLOAT64:
		var v float64
		if err = column.Decode(&v); err == nil {
			return v, nil
		}
	case sppb.TypeCode_FLOAT32:
		var v float32
		if err = column.Decode(&v); err == nil {
			return float64(v), nil
		}
	case sppb.TypeCode_NUMERIC:
		var v big.Rat
		if err = column.Decode(&v); err == nil {
			return &v, nil
		}
	case sppb.TypeCode_BOOL:
		var v bool
		if err = column.Decode(&v); err == nil {
			return v, nil
		}
	case sppb.TypeCode_TIMESTAMP:
		var v time.Time
		if err = column.Decode(&v); err == nil {
			return v.UTC(), nil
		}
	case sppb.TypeCode_DATE:
		var v civil.Date
		if err = column.Decode(&v); err == nil {
			return condition.Date{Year: v.Year, Month: v.Month, Day: v.Day}, nil
		}
	default:
		return nil, errors.Newf("a fact about the caller is held in a %s column, which a condition does not compare", code)
	}

	return nil, errors.Wrap(err, "spanner.GenericColumnValue.Decode()")
}
