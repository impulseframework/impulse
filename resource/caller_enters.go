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
// must enter before a method is offered on one of its rows. A method that names the row
// (@target, @transition) and is declared with @callerEnters is offered by the row, under
// capabilities=Execute, only to a caller whose session enters every tenant the row's
// query names (UserPermissions.Reaches); the method's own code refuses the call, and the
// declaration refuses nothing. The struct declares it as a method of its own:
//
//	func (User) TenantsToEnter() resource.TenantsToEnter
type TenantsToEnter struct {
	// Query is one query, with no WITH, of two columns, RowKey and Tenant (a SELECT, or a
	// compound of them such as a UNION): one row for each tenant a caller's session must
	// enter to be offered the held methods on the row RowKey names. It is written in the
	// dialect of the database it runs on; on PostgreSQL its two column names are written
	// quoted (AS "RowKey", AS "Tenant"), since the statement names them quoted.
	Query string

	// Params are the named parameters the query binds. None starts with an underscore,
	// which is the statement's own namespace, and none is one the statement binds
	// already.
	Params map[string]any

	// Column is the column of the resource's row that the query's RowKey is matched to.
	Column string
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
// those are.
func (q *QuerySet[Resource]) askUnentered(ctx context.Context, held accesstypes.Resource) error {
	if q.unentered != nil {
		return nil
	}
	if _, ok := q.tenantsToEnter(); !ok {
		return errors.Newf("method %s is offered on a row of %s only to a caller who enters the row's tenants, and %s declares no TenantsToEnter; regenerate", held, q.Resource(), q.Resource())
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
	if strings.TrimSpace(declared.Column) == "" {
		return TenantsToEnter{}, errors.Newf("the TenantsToEnter of %s names no column of its row for RowKey to match", q.Resource())
	}
	if startsWithWith(declared.Query) {
		return TenantsToEnter{}, errors.Newf("the TenantsToEnter query of %s starts with WITH; write it as one SELECT", q.Resource())
	}
	for _, name := range slices.Sorted(maps.Keys(declared.Params)) {
		if strings.HasPrefix(name, "_") {
			return TenantsToEnter{}, errors.Newf("TenantsToEnter params for %s can not start with an _", q.Resource())
		}
	}

	return declared, nil
}

// enteringTerm renders the boolean a held method's offer asks beside its own: no row of
// the struct's query names this row and a tenant the session does not enter. outer is
// the statement's name for the row (loweringContext.outer). The query sits in
// parentheses under an alias of the statement's, with a newline before the closing
// parenthesis for a query that ends in a comment, as viewFromItem sets a view's; the
// tenants are bound once for the statement.
func (q *QuerySet[Resource]) enteringTerm(gen *sqlGenerator, outer string, declared TenantsToEnter, registry *paramRegistry) string {
	alias := registry.alias()
	tenants := registry.bindShared(callerEntersParamKey, domainStrings(q.unentered))
	rowKey := gen.renderColumnRef(columnRef{qualifier: alias, column: "RowKey"})
	tenant := gen.renderColumnRef(columnRef{qualifier: alias, column: "Tenant"})
	column := gen.renderColumnRef(columnRef{qualifier: outer, column: declared.Column})
	member := fmt.Sprintf("%s IN UNNEST(%s)", tenant, tenants)
	if gen.dialect == PostgreSQL {
		member = fmt.Sprintf("%s = ANY(%s)", tenant, tenants)
	}

	return fmt.Sprintf("NOT EXISTS (SELECT 1 FROM (%s\n) AS %s WHERE %s = %s AND %s)", declared.Query, gen.quoteIdentifier(alias), rowKey, column, member)
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
