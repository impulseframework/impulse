package resource

import (
	"fmt"
	"strings"

	"github.com/go-playground/errors/v5"
)

// nameFilter is the rendering of one filter condition on a name field: the
// condition tests the id beside the name against the pointed-to table's rows
// whose name meets it, so the pointed-to table's own index or search index
// answers it. A row whose id points to no row, to a row of another tenant, or
// to a row whose name the person may not list meets no condition but isnull.
type nameFilter struct {
	// id is the id column the condition tests, quoted; where the id's cells are
	// shown under a condition, CASE WHEN <that condition> THEN <the column> END,
	// so a masked id meets no condition but isnull.
	id string
	// keys selects the pointed-to table's keys under the condition's own alias:
	// SELECT a.K FROM T a, or, from a view, SELECT a.K FROM (<its query>) AS a.
	keys string
	// held is what the pointed-to rows are held to before the condition: the
	// tenant test and, in parentheses, the condition the name's grant is held
	// under, joined by AND. "" where there is neither.
	held string
}

// nameFilters renders, for each condition of the filter on a name field, the
// pointed-to table's rows it tests the id against (nameFilter), keyed by the
// condition. Each condition reads the pointed-to table under its own alias, so
// the same name may be named twice. The name's grant was decided with the
// filter's other fields, and a denied one refused (refuseQueriedName). Nil
// where the filter names no name.
func (q *QuerySet[Resource]) nameFilters(dbType DBType, filterAst ExpressionNode, rendered *renderedReadConditions, registry *paramRegistry) (map[*ConditionNode]*nameFilter, error) {
	if len(q.filterNames) == 0 {
		return nil, nil
	}
	if registry == nil {
		return nil, errors.Newf("resource %s filters by a name, which needs a statement registry to render", q.Resource())
	}
	gen, err := loweredSQLGenerator(dbType)
	if err != nil {
		return nil, err
	}

	dbFields := q.rMeta.dbFieldMap(dbType)
	filters := make(map[*ConditionNode]*nameFilter)
	for _, leaf := range leaves(filterAst) {
		field := leaf.Condition.name
		if field == "" {
			continue
		}
		name, ok := q.requestName(field)
		if !ok {
			return nil, errors.Newf("name field %s is not a name field of %s", field, q.Resource())
		}
		grant := q.nameGrant(field)
		if grant.IsDenied() {
			return nil, errors.Newf("resource %s filters by the name %s, whose grant is denied", q.Resource(), field)
		}
		key, ok := dbFields[name.Key]
		if !ok {
			return nil, errors.Newf("name field %s: its id field %s not found in db struct", field, name.Key)
		}

		query, err := q.nameViewQuery(&name.NameField, registry)
		if err != nil {
			return nil, err
		}
		alias := registry.alias()
		held := nameTenantTest(&name.NameField, alias, registry)
		if grant.IsConditional() {
			if held, err = q.nameGrantCondition(name, grant, alias, held, registry); err != nil {
				return nil, err
			}
		}
		filter := &nameFilter{
			id: gen.quoteIdentifier(key.ColumnName),
			keys: fmt.Sprintf("SELECT %s FROM %s",
				gen.renderColumnRef(columnRef{qualifier: alias, column: name.KeyColumn}),
				gen.fromItem(name.Table, query, alias)),
		}
		if held != nil {
			if filter.held, err = gen.generateLowered(held, registry); err != nil {
				return nil, errors.Wrapf(err, "name field %s", field)
			}
		}
		if rendered != nil {
			if shown := rendered.nameConditions[name.Key]; shown != "" {
				filter.id = fmt.Sprintf("CASE WHEN %s THEN %s END", shown, filter.id)
			}
		}
		filters[leaf] = filter
	}

	return filters, nil
}

// setNameFilters installs the renderings of the filter's conditions on name
// fields (nameFilters).
func (s *sqlGenerator) setNameFilters(filters map[*ConditionNode]*nameFilter) {
	s.nameFilters = filters
}

// generateNameConditionSQL renders a condition on a name field: the id is in
// the keys of the pointed-to rows, held as the name is held, whose name column
// meets the condition; isnull is the id that is NULL or in none of the rows
// whose name is not NULL. The condition on the name column renders as any
// condition does, its values bound from the filter's own counter, with the
// list's column overrides and search grants set aside: they belong to the
// list's table, not to the pointed-to one.
func (s *sqlGenerator) generateNameConditionSQL(cn *ConditionNode) (string, []QueryParam, error) {
	filter, ok := s.nameFilters[cn]
	if !ok {
		return "", nil, errors.Newf("the filter's condition on the name %s has no rendering of the pointed-to table", cn.Condition.name)
	}

	inner := *cn
	inner.Condition.name = ""
	isNull := strings.EqualFold(cn.Condition.Operator, isnullStr)
	if isNull {
		inner.Condition.Operator = isnotnullStr
	}
	expressions, grants := s.columnExpressions, s.searchGrants
	s.columnExpressions, s.searchGrants = nil, nil
	sql, params, err := s.generateConditionSQL(&inner)
	s.columnExpressions, s.searchGrants = expressions, grants
	if err != nil {
		return "", nil, err
	}

	where := sql
	if filter.held != "" {
		where = filter.held + " AND " + sql
	}
	if isNull {
		return fmt.Sprintf("(%s IS NULL OR %s NOT IN (%s WHERE %s))", filter.id, filter.id, filter.keys, where), params, nil
	}

	return fmt.Sprintf("%s IN (%s WHERE %s)", filter.id, filter.keys, where), params, nil
}
