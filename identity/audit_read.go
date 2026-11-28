package identity

import (
	"context"
	"iter"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// The two lists of the audit log, the whole log and a tenant's, are computed resources of
// the generated management: what a person reads of a row (who acted, what was done, to
// whom) is written from the row's parts as the row is read (auditlog.Stored.Sentences),
// so no query selects it. The lists filter by the parts the table holds and sort by the
// time and the role, and the statement here reads the table for them: it takes from a
// list's query what it renders, and the generated handler applies the rest.

// AuditQuery is what a list of the audit log asks of the table.
type AuditQuery struct {
	Tenant     accesstypes.Domain   // "" for the whole log; otherwise the rows done in this tenant alone
	Conditions []resource.Condition // the filter's conditions the list took
	Order      []resource.SortField // the order the list took, over At, Role and ID; nil where it took none
	Page       *resource.PageBounds // the page the list took; nil where the handler pages
}

// AuditLine is one row of the audit log as a list serves it.
type AuditLine struct {
	Row   auditlog.Stored
	Reads auditlog.Sentences
}

// The fields of a list of the audit log the statement renders, by their Go names: the
// filterable ones, and ID, which ends the order.
const (
	auditFieldAt            = "At"
	auditFieldAct           = "Act"
	auditFieldActorUsername = "ActorUsername"
	auditFieldSubjectName   = "SubjectName"
	auditFieldRole          = "Role"
	auditFieldID            = "ID"
)

// The comparisons of the filter's grammar the statement renders, as the grammar spells
// them: the ten every filterable field answers.
const (
	auditOpEqual        = "eq"
	auditOpNotEqual     = "ne"
	auditOpGreater      = "gt"
	auditOpGreaterEqual = "gte"
	auditOpLess         = "lt"
	auditOpLessEqual    = "lte"
	auditOpIn           = "in"
	auditOpNotIn        = "notin"
	auditOpIsNull       = "isnull"
	auditOpIsNotNull    = "isnotnull"
)

// auditColumn is the table's column a field of a list of the audit log reads, and false
// for a field the statement does not render.
func auditColumn(field string) (string, bool) {
	switch field {
	case auditFieldAt:
		return "CreatedAt", true
	case auditFieldAct, auditFieldActorUsername, auditFieldSubjectName:
		return field, true
	case auditFieldRole:
		return "RoleName", true
	case auditFieldID:
		return "Id", true
	default:
		return "", false
	}
}

// auditFiltered reports whether the statement renders a condition on the field: the
// list's filterable fields, which are all but ID.
func auditFiltered(field string) bool {
	_, ok := auditColumn(field)

	return ok && field != auditFieldID
}

// auditRenders reports whether the statement renders the operator: the ten comparisons.
func auditRenders(operator string) bool {
	switch operator {
	case auditOpEqual, auditOpNotEqual, auditOpGreater, auditOpGreaterEqual, auditOpLess, auditOpLessEqual,
		auditOpIn, auditOpNotIn, auditOpIsNull, auditOpIsNotNull:
		return true
	default:
		return false
	}
}

// TakeAuditQuery takes from a list's query what AuditStatement renders, and nothing
// else, so whatever it leaves the generated handler still applies and the answer is
// right either way. Of the fields At, Act, ActorUsername, SubjectName and Role it takes
// the conditions of each field whose every condition is one of the ten comparisons; a
// field with a text match (starts with, ends with, contains) or the search box is left
// whole to the handler, which applies it to the rows in memory and then pages. Where the
// order names only At, Role and ID it takes the order and then asks for the page: with a
// page, Order is the page's order and Page its bounds; without, Order is the taken
// order. Where the order names any other field it takes neither. tenant is the one
// tenant whose rows the list reads, "" for the whole log.
func TakeAuditQuery[Row resource.Resourcer](qSet *resource.QuerySet[Row], tenant accesstypes.Domain) *AuditQuery {
	query := &AuditQuery{Tenant: tenant, Conditions: nil, Order: nil, Page: nil}
	query.Conditions = qSet.Filter().Take(auditTakable(qSet.Filter().Conditions())...)
	if slices.ContainsFunc(qSet.Order(), func(sf resource.SortField) bool {
		return sf.Field != auditFieldAt && sf.Field != auditFieldRole && sf.Field != auditFieldID
	}) {
		return query
	}
	query.Order = qSet.TakeSort()
	if bounds, ok := qSet.TakePage(); ok {
		query.Order, query.Page = bounds.Order(), &bounds
	}

	return query
}

// auditTakable names the filterable fields whose every condition the statement renders:
// one of the ten comparisons, and never the search box.
func auditTakable(conditions []resource.Condition) []string {
	var fields []string
	for _, field := range []string{auditFieldAt, auditFieldAct, auditFieldActorUsername, auditFieldSubjectName, auditFieldRole} {
		rendered := true
		for i := range conditions {
			if conditions[i].Field == field && (conditions[i].IsSearch() || !auditRenders(conditions[i].Operator)) {
				rendered = false
			}
		}
		if rendered {
			fields = append(fields, field)
		}
	}

	return fields
}

// AuditStatement writes the one statement that reads the rows of the audit records
// table the query asks for: every column of the table (auditlog.Columns); the tenant's
// rows alone where the query names one; each taken condition and the page's boundary;
// the taken order; and the page's size and one more. Values are bound as parameters and
// never written into the text, the table's name being the one name written in. The
// table is read through the index by tenant where the query names a tenant, through
// the index by time where the order begins with At, and otherwise with no hint; a
// statement that reads one tenant's rows carries the hint the Spanner emulator asks for
// (auditHint). A condition on a field or with an operator the statement does not
// render, and an order by such a field, are errors for a developer: TakeAuditQuery
// takes none.
func AuditStatement(table string, query *AuditQuery) (sql string, params map[string]any, err error) {
	var page auditPage
	if query.Page != nil {
		page = auditPage{paged: true, boundary: query.Page.Boundary(), fetch: query.Page.Fetch()}
	}

	return auditStatement(table, query, page)
}

// auditPage is what the statement renders of the page a list took: whether it took one,
// the row the page starts after (nil on a first page), and how many rows to read.
type auditPage struct {
	paged    bool
	boundary []any
	fetch    uint64
}

// auditStatement is AuditStatement over the parts of the page it renders.
func auditStatement(table string, query *AuditQuery, page auditPage) (sql string, params map[string]any, err error) {
	params = map[string]any{}
	var where []string
	if query.Tenant != "" {
		params["tenant"] = string(query.Tenant)
		where = append(where, "TenantId = @tenant")
	}
	for i := range query.Conditions {
		clause, err := auditCondition(&query.Conditions[i], "f"+strconv.Itoa(i), params)
		if err != nil {
			return "", nil, err
		}
		where = append(where, clause)
	}
	orderBy, err := auditOrderBy(query.Order)
	if err != nil {
		return "", nil, err
	}
	var limit string
	if page.paged {
		if page.boundary != nil {
			clause, err := auditBoundary(query.Order, page.boundary, params)
			if err != nil {
				return "", nil, err
			}
			where = append(where, clause)
		}
		params["fetch"] = auditFetch(page.fetch)
		limit = " LIMIT @fetch"
	}

	sql = auditHint(query) + "SELECT " + strings.Join(auditlog.Columns(), ", ") + " FROM " + auditSource(table, query)
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	if orderBy != "" {
		sql += " ORDER BY " + orderBy
	}

	return sql + limit, params, nil
}

// auditHint is the statement's hint, where it reads one tenant's rows through the index
// by tenant, which is null filtered: the Spanner emulator cannot tell that TenantId =
// @tenant asks for no row the index leaves out, and refuses the statement unless it is
// told to skip that check. The hint is qualified for the emulator, and Cloud Spanner
// takes the index without it. Every other statement has none.
func auditHint(query *AuditQuery) string {
	if query.Tenant == "" {
		return ""
	}

	return "@{spanner_emulator.disable_query_null_filtered_index_check=true} "
}

// auditSource is the table as the statement reads it: through the index by tenant for
// one tenant's rows, through the index by time for an order that begins with At, and
// otherwise as Spanner chooses.
func auditSource(table string, query *AuditQuery) string {
	switch {
	case query.Tenant != "":
		return table + "@{FORCE_INDEX=" + table + "ByTenant}"
	case len(query.Order) > 0 && query.Order[0].Field == auditFieldAt:
		return table + "@{FORCE_INDEX=" + table + "ByCreatedAt}"
	default:
		return table
	}
}

// auditCondition renders one taken condition, binding its value under name. A condition
// on Role finds a row by any of its roles where it asks for one role or for any of a
// list (eq, in); every other comparison reads the row's first role, RoleName.
func auditCondition(c *resource.Condition, name string, params map[string]any) (string, error) {
	column, ok := auditColumn(c.Field)
	if !ok || !auditFiltered(c.Field) {
		return "", errors.Newf("identity.AuditStatement: the audit log is not filtered by %s", c.Field)
	}
	role := c.Field == auditFieldRole
	switch c.Operator {
	case auditOpEqual:
		params[name] = c.Value
		if role {
			return "(RoleName = @" + name + " OR @" + name + " IN UNNEST(OtherRoleNames))", nil
		}

		return column + " = @" + name, nil
	case auditOpNotEqual, auditOpGreater, auditOpGreaterEqual, auditOpLess, auditOpLessEqual:
		params[name] = c.Value

		return column + " " + auditComparison(c.Operator) + " @" + name, nil
	case auditOpIn:
		params[name] = c.TypedValues()
		if role {
			return "(RoleName IN UNNEST(@" + name + ") OR EXISTS (SELECT 1 FROM UNNEST(OtherRoleNames) AS n WHERE n IN UNNEST(@" + name + ")))", nil
		}

		return column + " IN UNNEST(@" + name + ")", nil
	case auditOpNotIn:
		params[name] = c.TypedValues()

		return column + " NOT IN UNNEST(@" + name + ")", nil
	case auditOpIsNull:
		return column + " IS NULL", nil
	case auditOpIsNotNull:
		return column + " IS NOT NULL", nil
	default:
		return "", errors.Newf("identity.AuditStatement: the operator %s is not rendered on %s", c.Operator, c.Field)
	}
}

// auditComparison is the SQL of one of the five comparisons that are not equality.
func auditComparison(operator string) string {
	switch operator {
	case auditOpNotEqual:
		return "!="
	case auditOpGreater:
		return ">"
	case auditOpGreaterEqual:
		return ">="
	case auditOpLess:
		return "<"
	default:
		return "<="
	}
}

// auditOrderBy renders the taken order as each column in its direction: RoleName, the one
// nullable column of the order, sorts in Spanner's own placement, NULL first ascending and
// last descending, the placement TakeSort promises.
func auditOrderBy(order []resource.SortField) (string, error) {
	parts := make([]string, 0, len(order))
	for _, sf := range order {
		column, ok := auditColumn(sf.Field)
		if !ok || (sf.Field != auditFieldAt && sf.Field != auditFieldRole && sf.Field != auditFieldID) {
			return "", errors.Newf("identity.AuditStatement: the audit log is not sorted by %s", sf.Field)
		}
		direction := " ASC"
		if sf.Direction == resource.SortDescending {
			direction = " DESC"
		}
		parts = append(parts, column+direction)
	}

	return strings.Join(parts, ", "), nil
}

// auditBoundary renders "strictly after the boundary row in the read order" as a keyset
// cursor's comparison: (a > b0) OR (a = b0 AND b > b1) ..., each field's comparator
// following its direction. CreatedAt and Id are never NULL. RoleName admits its NULL
// region on Spanner's side of its direction: ascending, NULL comes first, so every row
// with a role follows a NULL boundary and no NULL row follows a role; descending, NULL
// comes last, so the NULL rows follow a role and nothing follows a NULL boundary.
// Equality on a NULL boundary is IS NULL.
func auditBoundary(order []resource.SortField, boundary []any, params map[string]any) (string, error) {
	if len(order) != len(boundary) {
		return "", errors.New("identity.AuditStatement: the page's boundary does not match its order")
	}
	var disjuncts, equalities []string
	for i, sf := range order {
		column, _ := auditColumn(sf.Field)
		param := "c" + strconv.Itoa(i)
		nullable := sf.Field == auditFieldRole
		var after, equal string
		switch {
		case boundary[i] == nil && !nullable:
			return "", errors.Newf("identity.AuditStatement: the page's boundary holds no %s, which every row holds", sf.Field)
		case boundary[i] == nil:
			equal = column + " IS NULL"
			if sf.Direction == resource.SortAscending {
				after = column + " IS NOT NULL"
			}
		case sf.Direction == resource.SortAscending:
			params[param] = boundary[i]
			equal, after = column+" = @"+param, column+" > @"+param
		default:
			params[param] = boundary[i]
			equal, after = column+" = @"+param, column+" < @"+param
			if nullable {
				after = "(" + after + " OR " + column + " IS NULL)"
			}
		}
		if after != "" {
			disjuncts = append(disjuncts, "("+strings.Join(append(slices.Clone(equalities), after), " AND ")+")")
		}
		equalities = append(equalities, equal)
	}
	if len(disjuncts) == 0 {
		// A NULL boundary at the end of the NULL region: nothing follows it.
		return "FALSE", nil
	}

	return "(" + strings.Join(disjuncts, " OR ") + ")", nil
}

// auditFetch is the page's fetch as the int64 Spanner binds, bounded.
func auditFetch(fetch uint64) int64 {
	if fetch > math.MaxInt64 {
		return math.MaxInt64
	}

	return int64(fetch)
}

// AuditRecords yields the rows of the auth's audit log the query asks for, in its order,
// each with what a person reads of it, written in the words the Manager holds for a
// tenant (WithTenantWord). reader is a read-only transaction of the application's
// database.
func (m *Manager) AuditRecords(ctx context.Context, reader resource.ReadOnlyTransaction, query *AuditQuery) iter.Seq2[*AuditLine, error] {
	return func(yield func(*AuditLine, error) bool) {
		ctx, span := tracer.Start(ctx)
		defer span.End()

		for row, err := range m.store.AuditRecords(ctx, reader, query) {
			if err != nil {
				yield(nil, errors.Wrap(err, "identity.Store.AuditRecords()"))

				return
			}
			if !yield(&AuditLine{Row: *row, Reads: row.Sentences(m.tenantWord)}, nil) {
				return
			}
		}
	}
}
