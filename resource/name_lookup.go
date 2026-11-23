package resource

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/cloudspannerecosystem/memefish"
	"github.com/cloudspannerecosystem/memefish/token"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/httpio"
)

// nameColumnPrefix opens the alias a statement selects a looked-up name under,
// followed by the name field. The generator refuses a resource column spelled
// with it, so the alias never meets a column of the row.
const nameColumnPrefix = "zzName"

// nameFielder is implemented by a generated resource that carries name fields
// (NameFields): the lookups its statements read the names of the rows it points
// to through.
type nameFielder interface {
	NameFields() []NameField
}

// requestName is one name field of the request type whose id field the request
// type carries: the resource's description of the lookup, and the name the wire
// calls the field by. A request may name it only while no flag hides its id.
type requestName struct {
	NameField

	jsonName string
	// flagged says the name's source stands behind a flag that is off for the
	// request (NameField.Features): no request may name it and none is given it,
	// while a declared order by it orders by it (queryNames).
	flagged bool
}

// RequestedName is one name field a query returns: the field, and the name a
// row carries it under on the wire.
type RequestedName struct {
	Field    accesstypes.Field
	JSONName string
}

// requestNames reads a request type's name fields (the fields marked nameof)
// against the lookups the resource describes. A name field the resource does not
// describe has nothing to be looked up in: the request type and the resource are
// out of step, which is refused where the decoder is built.
func requestNames[Resource Resourcer](reqType reflect.Type) ([]requestName, error) {
	var res Resource
	var lookups []NameField
	if described, ok := any(res).(nameFielder); ok {
		lookups = described.NameFields()
	}
	if reqType.Kind() == reflect.Pointer {
		reqType = reqType.Elem()
	}
	if reqType.Kind() != reflect.Struct {
		return nil, nil
	}

	var names []requestName
	for i := range reqType.NumField() {
		field := reqType.Field(i)
		key, ok := field.Tag.Lookup(nameOfTagKey)
		if !ok {
			continue
		}
		described := slices.IndexFunc(lookups, func(n NameField) bool {
			return n.Field == accesstypes.Field(field.Name) && n.Key == accesstypes.Field(key)
		})
		if described < 0 {
			return nil, errors.Newf("request field %s is marked the name of %s (%s:%q), but the resource's NameFields() describes no such name, so there is nothing to look it up in; regenerate the resource and its handlers together", field.Name, key, nameOfTagKey, key)
		}
		jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if jsonName == "" || jsonName == "-" {
			return nil, errors.Newf("request field %s is marked the name of %s (%s:%q) and carries no json name to be asked for by; regenerate the handlers", field.Name, key, nameOfTagKey, key)
		}
		names = append(names, requestName{NameField: lookups[described], jsonName: jsonName})
	}

	return names, nil
}

// requestName finds one of the query set's name fields by its Go field name: a
// name a request may name, or one beside an id a flag hides, which only the
// declared order can bring in.
func (q *QuerySet[Resource]) requestName(field accesstypes.Field) (*requestName, bool) {
	for i := range q.names {
		if q.names[i].Field == field {
			return &q.names[i], true
		}
	}

	return nil, false
}

// grantField is the field of the resource whose grant covers a field the
// request names: a name field is read under the grant on the id field it stands
// beside (and under its own grant besides, nameGrant), and every other field
// under its own.
func (q *QuerySet[Resource]) grantField(field accesstypes.Field) accesstypes.Field {
	if name, ok := q.requestName(field); ok {
		return name.Key
	}

	return field
}

// grantResource is the grant reading a name takes beside the grant on its id:
// List on the pointed-to resource's name field, or on the resource itself where
// its name is its key (Tag is empty). It is "" for an enumeration table's name,
// which takes none.
func (n *NameField) grantResource() accesstypes.Resource {
	switch {
	case n.Resource == "":
		return ""
	case n.Tag == "":
		return n.Resource
	default:
		return n.Resource.ResourceWithTag(n.Tag)
	}
}

// nameGrantScope is the scope a name's grant is asked in, where the pointed-to
// resource's grants are held: the request's scope where that resource belongs
// to a tenant, the global scope where it does not. A list across tenants holds
// no name of a tenant's row (DecodeAcrossTenants), so the request's scope here
// is always one tenant's.
func (q *QuerySet[Resource]) nameGrantScope(name *NameField) accesstypes.Scope {
	if name.TenantColumn == "" {
		return accesstypes.GlobalScope()
	}

	return q.scope
}

// namesInPlay returns the name fields a request brings into play, each once:
// those its columns named, those it orders by (its sort, or the declared order),
// those its filter names, and, where it named no columns, every name beside an
// id the request may be given. A name beside an id a flag hides, or whose source
// a flag hides, can never be shown, so it is in play only where the order names it.
func (q *QuerySet[Resource]) namesInPlay() []accesstypes.Field {
	fields := slices.Clone(q.shownNames)
	for _, field := range slices.Concat(q.orderedNames(), q.filterNames) {
		if !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}
	if len(q.fields) == 0 && len(q.shownNames) == 0 && q.returnAccessibleFields {
		for i := range q.names {
			if q.requestable(q.names[i].Key) && !q.names[i].flagged && !slices.Contains(fields, q.names[i].Field) {
				fields = append(fields, q.names[i].Field)
			}
		}
	}

	return fields
}

// checkNameGrants asks the grants of the names in play that take one
// (grantResource), once a request and on a set that enforces grants: List, in
// one Check for each scope among them (nameGrantScope), whatever the request's
// own permission, so a name a person sees on a list they see on the row's page.
// A request with no such name in play asks nothing.
func (q *QuerySet[Resource]) checkNameGrants(ctx context.Context) error {
	if q.resourceSet == nil || q.nameGrants != nil {
		return nil
	}

	type pending struct {
		field accesstypes.Field
		res   accesstypes.Resource
		scope accesstypes.Scope
	}
	var asks []pending
	var scopes []accesstypes.Scope
	resources := make(map[accesstypes.Scope][]accesstypes.Resource)
	for _, field := range q.namesInPlay() {
		name, ok := q.requestName(field)
		if !ok {
			return errors.Newf("name field %s is not a name field of %s", field, q.Resource())
		}
		res := name.grantResource()
		if res == "" {
			continue
		}
		scope := q.nameGrantScope(&name.NameField)
		if _, seen := resources[scope]; !seen {
			scopes = append(scopes, scope)
		}
		if !slices.Contains(resources[scope], res) {
			resources[scope] = append(resources[scope], res)
		}
		asks = append(asks, pending{field: field, res: res, scope: scope})
	}

	answers := make(map[accesstypes.Scope]accesstypes.Decisions, len(scopes))
	for _, scope := range scopes {
		decisions, err := q.userPermissions.Check(ctx, q.env, scope, accesstypes.List, resources[scope]...)
		if err != nil {
			return errors.Wrap(err, "resource.UserPermissions.Check()")
		}
		answers[scope] = decisions
	}
	grants := make(map[accesstypes.Field]accesstypes.Decision, len(asks))
	for _, ask := range asks {
		grants[ask.field] = answers[ask.scope][ask.res]
	}
	q.nameGrants = grants

	return nil
}

// closeDeclaredOrder decides, where the declared order is in force, the grants
// on the fields it orders by, as the request's own sort fields are decided
// (queryFieldDecisions): each field of the order, and the id field of each name
// it orders by. A field the person may not read is closed (closedOrder), so the
// statement reads it as NULL on every row (withClosedOrder), and so is every name
// of the order that stands beside it (nameGrant), so the statement reads no name
// for it: the list comes in the order of the order's other terms. A field held
// under a condition is carried, so the order reads its visible projection, a
// masked cell being NULL, and a name's lookup sits inside the condition, whether
// or not the request asks for the field. Nothing is refused: the person asked
// for no sort.
func (q *QuerySet[Resource]) closeDeclaredOrder(ctx context.Context) error {
	q.closedNames = nil
	q.closedOrder = nil
	fields := q.declaredOrderFields()
	if len(fields) == 0 {
		return nil
	}
	decisions, decided, err := q.queryFieldDecisions(ctx, q.resourceSet, q.userPermissions, fields)
	if err != nil {
		return err
	}
	for _, qf := range decided {
		if !decisions[qf.res].IsDenied() {
			continue
		}
		for _, field := range q.orderedNames() {
			if name, ok := q.requestName(field); ok && name.Key == qf.field {
				if q.closedNames == nil {
					q.closedNames = make(map[accesstypes.Field]struct{})
				}
				q.closedNames[field] = struct{}{}
			}
		}
		if slices.ContainsFunc(q.defaultOrder, func(sf SortField) bool { return accesstypes.Field(sf.Field) == qf.field }) {
			if q.closedOrder == nil {
				q.closedOrder = make(map[accesstypes.Field]struct{})
			}
			q.closedOrder[qf.field] = struct{}{}
		}
	}
	q.carryConditionalDecisions(decisions)

	return nil
}

// declaredOrderFields returns the fields whose grants the declared order is read
// under, each once and in the order's order, where that order is in force: the
// request states no sort of its own. A term's field is read under its own grant,
// and a name's under the grant on the id field it stands beside (grantField). It
// is nil where the request states a sort.
func (q *QuerySet[Resource]) declaredOrderFields() []accesstypes.Field {
	if len(q.sortFields) != 0 {
		return nil
	}
	var fields []accesstypes.Field
	for _, sf := range q.defaultOrder {
		if field := q.grantField(accesstypes.Field(sf.Field)); !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}

	return fields
}

// nameGrant is the decision on a name's grant: Granted on a set that does not
// enforce grants, Denied for a name closed by its id (closeDeclaredOrder), Granted
// for a name that takes no grant of its own, otherwise the decision Check
// returned. A name that takes a grant and was never asked about is Denied.
func (q *QuerySet[Resource]) nameGrant(field accesstypes.Field) accesstypes.Decision {
	if q.resourceSet == nil {
		return accesstypes.Granted()
	}
	if _, closed := q.closedNames[field]; closed {
		return accesstypes.Denied()
	}
	name, ok := q.requestName(field)
	if !ok {
		return accesstypes.Denied()
	}
	if name.grantResource() == "" {
		return accesstypes.Granted()
	}

	return q.nameGrants[field]
}

// nameResource is a name field as a refusal names it: the list's own resource
// with the name's wire name as its tag (HangarPasses.squadronName), so the
// sentence speaks of the field the person asked for.
func (q *QuerySet[Resource]) nameResource(name *requestName) accesstypes.Resource {
	return q.Resource().ResourceWithTag(accesstypes.Tag(name.jsonName))
}

// refuseQueriedName refuses a request that sorts or filters by a name the person
// may not list, naming the first such name, the sort's before the filter's, in
// the place where. A declared order by such a name is not refused
// (withNameLookups).
func (q *QuerySet[Resource]) refuseQueriedName(where string) error {
	queried := make([]accesstypes.Field, 0, len(q.sortFields)+len(q.filterNames))
	for _, sf := range q.sortFields {
		queried = append(queried, accesstypes.Field(sf.Field))
	}
	for _, field := range slices.Concat(queried, q.filterNames) {
		name, ok := q.requestName(field)
		if !ok || !q.nameGrant(name.Field).IsDenied() {
			continue
		}

		return httpio.NewForbiddenMessageWithError(
			errors.Newf("scope (%s), user (%s) cannot sort or filter on %s: (%s) on %s is denied", q.nameGrantScope(&name.NameField), q.userPermissions.User(), name.jsonName, accesstypes.List, name.grantResource()),
			mayNotAskBy(bySortOrFilter, q.nameResource(name), where),
		)
	}

	return nil
}

// refuseNamedColumns refuses a request whose columns name a name the person may
// not list: every such name in one refusal, in the place where, the cause naming
// the first.
func (q *QuerySet[Resource]) refuseNamedColumns(where string) error {
	var denied []accesstypes.Resource
	var cause error
	for _, field := range q.shownNames {
		name, ok := q.requestName(field)
		if !ok || !q.nameGrant(field).IsDenied() {
			continue
		}
		if cause == nil {
			cause = errors.Newf("scope (%s), user (%s) cannot read the name %s: (%s) on %s is denied", q.nameGrantScope(&name.NameField), q.userPermissions.User(), name.jsonName, accesstypes.List, name.grantResource())
		}
		denied = append(denied, q.nameResource(name))
	}
	if len(denied) == 0 {
		return nil
	}

	return httpio.NewForbiddenMessageWithError(cause, mayNot(q.requiredPermission, denied, where))
}

// showName has the query return a name field, once.
func (q *QuerySet[Resource]) showName(field accesstypes.Field) {
	if !slices.Contains(q.shownNames, field) {
		q.shownNames = append(q.shownNames, field)
	}
}

// showNamesOf has a request that named no columns return the name beside every
// id field it returns, less the names the person may not list and those a flag
// hides.
func (q *QuerySet[Resource]) showNamesOf(fields []accesstypes.Field) {
	for i := range q.names {
		if slices.Contains(fields, q.names[i].Key) && !q.names[i].flagged && !q.nameGrant(q.names[i].Field).IsDenied() {
			q.showName(q.names[i].Field)
		}
	}
}

// Names returns the name fields the query returns, in the request type's order,
// each with the name a row carries it under on the wire. A generated handler
// writes Row.Name of each onto the row it encodes.
func (q *QuerySet[Resource]) Names() []RequestedName {
	names := make([]RequestedName, 0, len(q.shownNames))
	for i := range q.names {
		if slices.Contains(q.shownNames, q.names[i].Field) {
			names = append(names, RequestedName{Field: q.names[i].Field, JSONName: q.names[i].jsonName})
		}
	}

	return names
}

// shownNameKeys returns the id fields of the names the query returns, each once.
func (q *QuerySet[Resource]) shownNameKeys() []accesstypes.Field {
	keys := make([]accesstypes.Field, 0, len(q.shownNames))
	for _, field := range q.shownNames {
		if name, ok := q.requestName(field); ok && !slices.Contains(keys, name.Key) {
			keys = append(keys, name.Key)
		}
	}

	return keys
}

// orderedNames returns the name fields the query orders by, each once.
func (q *QuerySet[Resource]) orderedNames() []accesstypes.Field {
	var ordered []accesstypes.Field
	for _, sf := range q.Order() {
		field := accesstypes.Field(sf.Field)
		if _, ok := q.requestName(field); ok && !slices.Contains(ordered, field) {
			ordered = append(ordered, field)
		}
	}

	return ordered
}

// nameKeys returns the id fields of the names the statement names, those it
// returns, those it orders by and those its filter names, each once: the fields
// whose conditions the names' lookups and filters sit inside.
func (q *QuerySet[Resource]) nameKeys() []accesstypes.Field {
	fields := slices.Clone(q.shownNames)
	for _, field := range slices.Concat(q.orderedNames(), q.filterNames) {
		if !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}
	keys := make([]accesstypes.Field, 0, len(fields))
	for _, field := range fields {
		if name, ok := q.requestName(field); ok && !slices.Contains(keys, name.Key) {
			keys = append(keys, name.Key)
		}
	}

	return keys
}

// nameTenantTest holds a pointed-to row to the request's tenant: the row's
// tenant column, or the value at the end of the way the row reaches its tenant
// by, equals the request's tenant. It is nil where the pointed-to resource
// belongs to no tenant. qualifier is the alias the pointed-to table is read
// under.
func nameTenantTest(name *NameField, qualifier string, registry *paramRegistry) ExpressionNode {
	if name.TenantColumn == "" {
		return nil
	}
	tenant := columnComparand(qualifier, name.TenantColumn)
	if len(name.TenantPath) > 0 {
		tenant = pathScalar(&tenant, name.TenantPath, registry)
	}

	return &loweredComparisonNode{left: tenant, op: "=", right: namedComparand(domainParamName)}
}

// readNames returns the name fields the statement selects: those the query
// returns and, on a paged list, those it orders by, whose values the cursor
// carries.
func (q *QuerySet[Resource]) readNames() []accesstypes.Field {
	read := slices.Clone(q.shownNames)
	if q.page == nil {
		return read
	}
	for _, field := range q.orderedNames() {
		if !slices.Contains(read, field) {
			read = append(read, field)
		}
	}

	return read
}

// withNameLookups renders the lookup of every name field the statement names,
// those it returns and those it orders by, and carries them on the statement's
// rendering, which it makes where the read rendered no conditions. A lookup
// is one scalar subquery: the name column of the pointed-to table's row whose
// key is this row's id, held to the request's tenant where the pointed-to
// table belongs to one, so a row of another tenant never gives its name. No row
// is NULL. A name is read under two grants. Where the id's cells are shown under
// a condition, the lookup sits inside it, so a masked id gives no name either.
// Where the name's own grant (nameGrant) is held under a condition, the
// condition on the pointed-to row joins the lookup's WHERE last. Where it is
// denied, or the name is closed by its id (closeDeclaredOrder), which only a name
// the declared order names reaches, there is no lookup: the name is NULL on
// every row (noName), so the list comes in the order of the order's other terms.
func (q *QuerySet[Resource]) withNameLookups(dbType DBType, rendered *renderedReadConditions, registry *paramRegistry) (*renderedReadConditions, error) {
	fields := slices.Clone(q.shownNames)
	for _, field := range q.orderedNames() {
		if !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return rendered, nil
	}

	gen, err := loweredSQLGenerator(dbType)
	if err != nil {
		return nil, err
	}
	if rendered == nil {
		rendered = &renderedReadConditions{}
	}
	rendered.nameLookups = make(map[accesstypes.Field]string, len(fields))
	dbFields := q.rMeta.dbFieldMap(dbType)
	for _, field := range fields {
		name, ok := q.requestName(field)
		if !ok {
			return nil, errors.Newf("name field %s is not a name field of %s", field, q.Resource())
		}
		grant := q.nameGrant(field)
		if grant.IsDenied() {
			none, err := noName(dbType)
			if err != nil {
				return nil, err
			}
			rendered.nameLookups[field] = none

			continue
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
		var where ExpressionNode = &loweredComparisonNode{
			left:  columnComparand(alias, name.KeyColumn),
			op:    "=",
			right: columnComparand(string(q.Resource()), key.ColumnName),
		}
		if tenant := nameTenantTest(&name.NameField, alias, registry); tenant != nil {
			where = &LogicalOpNode{Left: where, Operator: OperatorAnd, Right: tenant}
		}
		if grant.IsConditional() {
			if where, err = q.nameGrantCondition(name, grant, alias, where, registry); err != nil {
				return nil, err
			}
		}
		lookup := subqueryComparand(&scalarSubqueryNode{table: name.Table, query: query, alias: alias, column: name.NameColumn, where: where})
		sql, err := gen.generateLoweredComparand(&lookup, registry)
		if err != nil {
			return nil, errors.Wrapf(err, "name field %s", field)
		}
		if shown := rendered.nameConditions[name.Key]; shown != "" {
			sql = fmt.Sprintf("CASE WHEN %s THEN %s END", shown, sql)
		}
		rendered.nameLookups[field] = sql
	}

	return rendered, nil
}

// nameViewQuery returns the query of the view a name is read from, "" where the
// name is read from a table. In the query it returns, each parameter the view binds
// is written as a placeholder of the statement's own, bound under the view and the
// parameter's name (bindShared): a view read twice in one statement binds each of its
// parameters once, and two views that bind a parameter of one name never meet. A
// parameter the view binds and its query never names is not bound. A name read from a
// view the decoder was not handed is not read; a parameter of the view's whose name
// starts with an underscore is refused as a list's own view's is (viewFromItem); and
// a query that binds a parameter and cannot be read to its end is refused, since its
// parameters cannot all be found.
func (q *QuerySet[Resource]) nameViewQuery(name *NameField, registry *paramRegistry) (string, error) {
	if !name.View {
		return "", nil
	}
	view, ok := q.nameViews[name.Resource]
	if !ok {
		return "", errors.New(noNameView(name, q.Resource()))
	}
	query, params := view.Subquery()
	for _, param := range slices.Sorted(maps.Keys(params)) {
		if strings.HasPrefix(param, "_") {
			return "", errors.Newf("Subquery params for %s can not start with an _", name.Resource)
		}
	}
	if len(params) == 0 {
		return query, nil
	}
	toks, err := viewParamTokens(query, params)
	if err != nil {
		return "", errors.New(unreadableViewQuery(name, q.Resource(), err))
	}
	var b strings.Builder
	last := 0
	for _, tok := range toks {
		b.WriteString(query[last:tok.Pos])
		b.WriteString(registry.bindShared(fmt.Sprintf("views/%s/%s", name.Resource, tok.AsString), params[tok.AsString]))
		last = int(tok.End)
	}
	b.WriteString(query[last:])

	return b.String(), nil
}

// viewParamTokens returns the parameter tokens of a view's query that name a
// parameter the view binds, compared exactly, in the query's order. Its error is the
// lexer's, where the query cannot be read to its end.
func viewParamTokens(query string, params map[string]any) ([]token.Token, error) {
	lex := &memefish.Lexer{
		File: &token.File{Buffer: query},
	}
	var toks []token.Token
	for {
		if err := lex.NextToken(); err != nil {
			return nil, err //nolint:wrapcheck // the lexer's own words are what the refusal quotes (unreadableViewQuery); a wrap would add its source line to them
		}
		if lex.Token.Kind == token.TokenEOF {
			return toks, nil
		}
		if _, bound := params[lex.Token.AsString]; bound && lex.Token.Kind == token.TokenParam {
			toks = append(toks, lex.Token)
		}
	}
}

// unreadableViewQuery says the query of the view a name is read from binds a
// parameter and cannot be read to its end.
func unreadableViewQuery(name *NameField, res accesstypes.Resource, err error) string {
	return fmt.Sprintf("name field %s of %s is read from the view %s, and the view's query cannot be read to its end (%v): the statement that reads the name writes each parameter of the view under a name of its own, and so reads the whole query. Correct the query at that place.", name.Field, res, name.Resource, err)
}

// noNameView says a name is read from a view the decoder holds no entry for.
func noNameView(name *NameField, res accesstypes.Resource) string {
	return fmt.Sprintf("name field %s of %s is read from the view %s, and the decoder holds no view of that name: hand the application's views to the decoder (WithNameViews), which the generated handlers do; regenerate them", name.Field, res, name.Resource)
}

// nameGrantCondition joins the condition a name's grant is held under to the
// lookup's WHERE, last and in parentheses: lowered against the pointed-to table
// read under alias, in that resource's own vocabulary, as a search branch's
// child field is (searchBranchGrants). A condition that comes to true adds
// nothing; where is nil, the condition in parentheses stands alone.
func (q *QuerySet[Resource]) nameGrantCondition(name *requestName, decision accesstypes.Decision, alias string, where ExpressionNode, registry *paramRegistry) (ExpressionNode, error) {
	res := name.grantResource()
	expr, err := conditionalExpr(res, decision)
	if err != nil {
		return nil, err
	}
	expr = condition.WithoutPostImage(expr)
	if t, ok := expr.(condition.Truth); ok && t.Value {
		return where, nil
	}
	if q.collection == nil {
		return nil, errors.Newf("resource %s carries a conditional decision on the name %s but no generated collection is wired to render it", q.Resource(), name.Field)
	}
	bindings, _ := q.collection.Bindings(q.collection.Scope(name.Resource), name.Resource)
	_, partitioned := q.nameGrantScope(&name.NameField).Domain()
	lctx := &loweringContext{outer: alias, bindings: bindings, collection: q.collection, partitioned: partitioned}
	held, err := lowerRowCondition(expr, lctx, registry)
	if err != nil {
		return nil, errors.Wrapf(err, "lowering the condition on %s for the name %s", res, name.Field)
	}
	if where == nil {
		return &GroupNode{Expression: held}, nil
	}

	return &LogicalOpNode{Left: where, Operator: OperatorAnd, Right: &GroupNode{Expression: held}}, nil
}

// noName is what a statement reads in place of a name the person may not list:
// NULL, typed as the name is, so the select item and the cursor read it as a
// name no row carries.
func noName(dbType DBType) (string, error) {
	switch dbType {
	case SpannerDBType:
		return "CAST(NULL AS STRING)", nil
	case PostgresDBType:
		return "CAST(NULL AS TEXT)", nil
	default:
		return "", errors.Newf("unsupported dbType: %s", dbType)
	}
}

// nameLookup returns the lookup the statement reads a name field through, ""
// where it rendered none.
func (r *renderedReadConditions) nameLookup(field accesstypes.Field) string {
	if r == nil {
		return ""
	}

	return r.nameLookups[field]
}

// nameItems renders the select items of the names the statement reads, each its
// lookup under the names' reserved prefix followed by the name field, and
// describes them to the readers as the cursor's copies are: a name is no cell
// of the row's data, so it is scanned into the Row envelope, where the cursor
// reads the name a list was ordered by and Row.Name the name a row shows.
func (q *QuerySet[Resource]) nameItems(dbType DBType, rendered *renderedReadConditions) ([]string, []cursorColumn, error) {
	read := q.readNames()
	items := make([]string, 0, len(read))
	columns := make([]cursorColumn, 0, len(read))
	for _, field := range read {
		lookup := rendered.nameLookup(field)
		if lookup == "" {
			return nil, nil, errors.Newf("name field %s was not looked up", field)
		}
		alias := nameColumnPrefix + string(field)
		switch dbType {
		case SpannerDBType:
			items = append(items, fmt.Sprintf("%s AS %s", lookup, alias))
		case PostgresDBType:
			items = append(items, lookup+` AS "`+alias+`"`)
		default:
			return nil, nil, errors.Newf("unsupported dbType: %s", dbType)
		}
		columns = append(columns, cursorColumn{field: field, alias: alias, fieldType: reflect.TypeFor[string](), nullable: true})
	}

	return items, columns, nil
}

// selectListWithNames renders the statement's select list: the row's own columns
// (selectList), then the names the statement reads.
func (q *QuerySet[Resource]) selectListWithNames(dbType DBType, rendered *renderedReadConditions, capChecksItem string) (Columns, []cursorColumn, error) {
	columns, cursorColumns, err := q.selectList(dbType, rendered, capChecksItem)
	if err != nil {
		return "", nil, err
	}
	nameItems, nameColumns, err := q.nameItems(dbType, rendered)
	if err != nil {
		return "", nil, errors.Wrap(err, "QuerySet.nameItems()")
	}
	if len(nameItems) == 0 {
		return columns, cursorColumns, nil
	}
	if columns != "" {
		nameItems = append([]string{string(columns)}, nameItems...)
	}

	return Columns(strings.Join(nameItems, ", ")), append(cursorColumns, nameColumns...), nil
}

// Name returns the name the row carries for a name field the query returns
// (QuerySet.Names): the name of the row the id beside it points to. It is nil
// where the id points to no row, where that row is not the request's tenant's,
// where the id's cell is masked on this row, and where the person's grant on
// the name holds under a condition that row does not meet.
func (r Row[Resource]) Name(field accesstypes.Field) *string {
	copied, ok := r.cursorValues[field]
	if !ok {
		return nil
	}
	name, _ := copied.Interface().(*string)

	return name
}

// NameSource is one list a query's names are read from: the pointed-to resource,
// its name field as the grants name it (empty where the name is the resource's
// key), and the tenant the names are read in (empty where the resource belongs to
// no tenant). A live page that shows the names subscribes to that list, so a write
// to the pointed-to resource reaches the page as it reaches the list's own.
type NameSource struct {
	Resource accesstypes.Resource
	Field    accesstypes.Tag
	Domain   accesstypes.Domain
}

// NameSources returns the lists the query's names are read from: for each name in
// play (namesInPlay) that points to a resource and whose own grant the person holds,
// outright or under a condition, the resource's list in the tenant the names are
// read in, the request's where the resource belongs to a tenant. Each list comes
// once, in the order the request type declares the names: two names read from one
// list are one. An enumeration table's name answers nothing: it has no resource to
// subscribe to. The names' grants are settled as the read settles them
// (checkNameGrants), once a request, so the read that follows asks nothing again. A
// name the read closes with the declared order still answers: the person holds its
// grant, and the list tells them nothing the grant does not. On a set that enforces
// no grants every such name answers.
func (q *QuerySet[Resource]) NameSources(ctx context.Context) ([]NameSource, error) {
	if err := q.checkNameGrants(ctx); err != nil {
		return nil, err
	}
	inPlay := q.namesInPlay()
	var sources []NameSource
	for i := range q.names {
		name := &q.names[i]
		if name.Resource == "" || !slices.Contains(inPlay, name.Field) {
			continue
		}
		if q.resourceSet != nil && q.nameGrants[name.Field].IsDenied() {
			continue
		}
		source := NameSource{Resource: name.Resource, Field: name.Tag}
		if name.TenantColumn != "" {
			source.Domain, _ = q.scope.Domain()
		}
		if !slices.Contains(sources, source) {
			sources = append(sources, source)
		}
	}

	return sources, nil
}
