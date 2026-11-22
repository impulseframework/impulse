package resource

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/cloudspannerecosystem/memefish"
	"github.com/cloudspannerecosystem/memefish/token"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// TenantsToEnter is what a resource's struct answers about the tenants a caller's session
// must enter before a method is offered on one of its rows. A method declared with
// @callerEnters, one that names the row (@target, @transition) or one that names no row and
// lists the struct (@callerEnters(Row)), is offered by the row, under capabilities=Execute,
// only to a caller whose session enters every tenant the row's query names
// (UserPermissions.Reaches); the method's own code refuses the call, and the declaration
// refuses nothing. The struct declares it as a method of its own:
//
//	func (User) TenantsToEnter() resource.TenantsToEnter
type TenantsToEnter struct {
	// Query is one query, with no WITH (a SELECT, or a compound of them such as a UNION),
	// that selects a column named Tenant and the key columns Match names: one row for each
	// tenant a caller's session must enter to be offered the held methods on the row its key
	// columns name. It may select other columns, which the statement does not read. It is
	// written in the dialect of the database it runs on; on PostgreSQL the names of the
	// columns the statement reads are written quoted (AS "RowKey", AS "Tenant"), since the
	// statement names them quoted.
	Query string

	// Params are the named parameters the query binds. None starts with an underscore,
	// which is the statement's own namespace, and none is one the statement binds
	// already.
	Params map[string]any

	// Match pairs each key column of the query with the column of the resource's row it is
	// matched to: a row of the query speaks of a row of the resource where every pair is
	// equal. A row named by one column takes one pair (RowKey to Id); a row named by
	// several takes one for each.
	Match map[string]string
}

// tenantsToEnterer is what a resource's struct answers when a method that names its rows
// is offered only to a caller whose session enters the row's tenants (@callerEnters).
type tenantsToEnterer interface {
	TenantsToEnter() TenantsToEnter
}

// callerEntersParamKey is the key the tenants the session does not enter are bound under
// (paramRegistry.bindShared): one array for the statement, whatever number of booleans
// read it.
const callerEntersParamKey = "callerEnters"

// tenantsToEnter answers the TenantsToEnter the resource's struct declares, and whether
// it declares one.
func (q *QuerySet[Resource]) tenantsToEnter() (TenantsToEnter, bool) {
	var r Resource
	declarer, ok := any(r).(tenantsToEnterer)
	if !ok {
		return TenantsToEnter{}, false
	}

	return declarer.TenantsToEnter(), true
}

// askUnentered keeps for the statement the tenants the session does not enter, asked
// once, where held, a method that names the resource's rows, is offered only to a caller
// whose session enters the row's tenants. The resource's struct must say which tenants
// those are, in a form a statement can render. It is held to that form here, whether or
// not this session's statement renders the term, so a declaration that cannot work fails
// the first read that asks for capabilities=Execute, and not the first read of a caller
// some tenant keeps out.
func (q *QuerySet[Resource]) askUnentered(ctx context.Context, held accesstypes.Resource) error {
	if q.unentered != nil {
		return nil
	}
	if _, ok := q.tenantsToEnter(); !ok {
		return errors.Newf("method %s is offered on a row of %s only to a caller who enters the row's tenants, and %s declares no TenantsToEnter; regenerate", held, q.Resource(), q.Resource())
	}
	if _, err := q.enteringDeclaration(); err != nil {
		return err
	}
	unentered, err := unenteredDomains(ctx, q.userPermissions)
	if err != nil {
		return err
	}
	q.unentered = unentered

	return nil
}

// enteringDeclaration answers the struct's TenantsToEnter for a statement that renders
// its term, refusing one the statement cannot render.
func (q *QuerySet[Resource]) enteringDeclaration() (TenantsToEnter, error) {
	declared, ok := q.tenantsToEnter()
	if !ok {
		return TenantsToEnter{}, errors.Newf("%s declares no TenantsToEnter; regenerate", q.Resource())
	}
	if strings.TrimSpace(declared.Query) == "" {
		return TenantsToEnter{}, errors.Newf("the TenantsToEnter query of %s is empty", q.Resource())
	}
	if len(declared.Match) == 0 {
		return TenantsToEnter{}, errors.Newf("the TenantsToEnter of %s matches no column of its row: Match pairs each key column of the query with the row's column it is matched to", q.Resource())
	}
	for _, key := range slices.Sorted(maps.Keys(declared.Match)) {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(declared.Match[key]) == "" {
			return TenantsToEnter{}, errors.Newf("the TenantsToEnter of %s has a pair in Match with an empty name: each pair names a key column of the query and a column of the row", q.Resource())
		}
	}
	if startsWithWith(declared.Query) {
		return TenantsToEnter{}, errors.Newf("the TenantsToEnter query of %s starts with WITH; write it with no WITH, as a SELECT or a UNION of SELECTs", q.Resource())
	}
	for _, name := range slices.Sorted(maps.Keys(declared.Params)) {
		if strings.HasPrefix(name, "_") {
			return TenantsToEnter{}, errors.Newf("TenantsToEnter params for %s can not start with an _", q.Resource())
		}
	}

	return declared, nil
}

// enteringTerm renders the boolean a held method's offer asks beside its own: no row of
// the struct's query names this row and a tenant the session does not enter. The query's
// row names this one where each pair of Match is equal, the pairs taken in the order of
// the query columns' names. outer is the statement's name for the row
// (loweringContext.outer). The query sits in
// parentheses under an alias of the statement's, with a newline before the closing
// parenthesis for a query that ends in a comment, as viewFromItem sets a view's; the
// tenants are bound once for the statement.
func (q *QuerySet[Resource]) enteringTerm(gen *sqlGenerator, outer string, declared TenantsToEnter, registry *paramRegistry) string {
	alias := registry.alias()
	tenants := registry.bindShared(callerEntersParamKey, domainStrings(q.unentered))
	terms := make([]string, 0, len(declared.Match)+1)
	for _, key := range slices.Sorted(maps.Keys(declared.Match)) {
		terms = append(terms, fmt.Sprintf("%s = %s",
			gen.renderColumnRef(columnRef{qualifier: alias, column: key}),
			gen.renderColumnRef(columnRef{qualifier: outer, column: declared.Match[key]}),
		))
	}
	tenant := gen.renderColumnRef(columnRef{qualifier: alias, column: "Tenant"})
	member := fmt.Sprintf("%s IN UNNEST(%s)", tenant, tenants)
	if gen.dialect == PostgreSQL {
		member = fmt.Sprintf("%s = ANY(%s)", tenant, tenants)
	}
	terms = append(terms, member)

	return fmt.Sprintf("NOT EXISTS (SELECT 1 FROM (%s\n) AS %s WHERE %s)", declared.Query, gen.quoteIdentifier(alias), strings.Join(terms, " AND "))
}

// mergeEnteringParams folds the parameters a TenantsToEnter query binds into the
// statement's, refusing a name the statement binds already.
func (q *QuerySet[Resource]) mergeEnteringParams(params map[string]any, plan *capabilityPlan) error {
	if plan == nil {
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(plan.enteringParams)) {
		if _, ok := params[name]; ok {
			return errors.Newf("named parameter collision: the TenantsToEnter query of %s and the statement both contain named parameter %q", q.Resource(), name)
		}
		params[name] = plan.enteringParams[name]
	}

	return nil
}

// startsWithWith reports whether the query's first token is WITH.
func startsWithWith(query string) bool {
	lex := &memefish.Lexer{
		File: &token.File{Buffer: query},
	}
	if err := lex.NextToken(); err != nil {
		return false
	}

	return strings.EqualFold(lex.Token.Raw, "WITH")
}
