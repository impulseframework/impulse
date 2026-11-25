package resource

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"google.golang.org/api/iterator"
)

// This file evaluates row-referencing Execute conditions (ABAC design plan
// §12): an RPC method that declares a @target row locates it inside its own
// transaction, and that located row is where a condition on the method's
// Execute grant evaluates — the same pre-image the transition frame's
// from-set check reads. The decoder carries the caller's Conditional decision
// to the generated frame as an ExecuteGate; the frame enforces it after its
// own NotFound/tenancy/state checks and before the body runs. When the
// target's @domain binding is a join path, the frame's tenancy check itself
// rides the gate (VerifyTenancy): the located row carries no tenant key of
// its own, so the gate walks the binding hops in the same transaction.
// Methods without a target row keep the decode-time posture: a grant of one
// under a row-referencing condition is refused in a role file
// (access.ValidateRoles), refused as a write to a custom role and left out of
// the policy when the store holds one, and a Conditional decision reaching
// their decode is a 500-class invariant breach.
//
// A target may be a row of a view (a @virtual struct). A view's row is one row
// of the view's query, and in a tenant-scoped view its key names a row in each
// tenant that holds one, so the frame reads no row by its key alone: the gate
// locates the row and judges the condition in one SELECT over the view's
// query, by the key within the request's tenant (EnforceView).

// TargetedRPCDecoder decodes an HTTP request for a @target-bearing RPC
// method. Unlike RPCDecoder, a Conditional Execute decision here is not an
// invariant breach: the generated handler has a located row to evaluate it
// against, so Decode carries the condition forward as an ExecuteGate.
type TargetedRPCDecoder[Request any] struct {
	d               *structDecoder[Request]
	res             accesstypes.Resource
	perm            accesstypes.Permission
	userPermissions func(*http.Request) UserPermissions
	collection      *GeneratedCollection
}

// MustNewTargetedRPCDecoder builds a decoder for a @target-bearing RPC method
// request, wired to the generated collection so a carried condition can lower
// against the target resource's bindings. It panics on construction errors:
// they are programming errors (a malformed request struct), surfaced at
// application startup where generated handlers construct their decoders.
func MustNewTargetedRPCDecoder[Request any](a DecoderAccessor, collection *GeneratedCollection, methodName accesstypes.Resource, perm accesstypes.Permission) *TargetedRPCDecoder[Request] {
	decoder, err := newStructDecoder[Request]()
	if err != nil {
		panic(errors.Wrap(err, "newStructDecoder()"))
	}

	return &TargetedRPCDecoder[Request]{
		d:               decoder.WithValidator(a.Validator()),
		res:             methodName,
		perm:            perm,
		userPermissions: a.UserPermissions,
		collection:      collection,
	}
}

// WithValidator sets a validator function on the decoder.
func (s *TargetedRPCDecoder[Request]) WithValidator(v ValidatorFunc) *TargetedRPCDecoder[Request] {
	decoder := *s
	decoder.d = s.d.WithValidator(v)

	return &decoder
}

// Decode decodes the HTTP request body into the Request struct and checks the
// caller's Execute permission in the given scope. Denied is Forbidden here;
// Granted and Conditional both admit — a Conditional decision's payload rides
// the returned ExecuteGate, which the generated frame enforces against the
// located target row inside its transaction.
func (s *TargetedRPCDecoder[Request]) Decode(request *http.Request, scope accesstypes.Scope) (*Request, *ExecuteGate, error) {
	req, err := s.d.Decode(request)
	if err != nil {
		return nil, nil, errors.Wrap(err, "structDecoder.Decode()")
	}

	caller := &Caller{Permissions: s.userPermissions(request), Scope: scope, Env: RequestEnvironment(), collection: s.collection}
	gate, err := caller.gateFor(request.Context(), s.res, s.perm, req)
	if err != nil {
		return nil, nil, err
	}
	gate.entryPermission = s.perm

	return req, gate, nil
}

// gateFor checks the caller's permission on a method in the caller's scope and
// environment and answers the gate that carries the decision. Denied is Forbidden;
// Granted and Conditional both admit. The atoms of a Conditional decision that read
// the method's own request fields settle here, against request. What is left reads
// the target row and rides the gate; a constant FALSE rides it too, so the row is
// still looked for and a missing row is still NotFound.
func (c *Caller) gateFor(ctx context.Context, method accesstypes.Resource, perm accesstypes.Permission, request any) (*ExecuteGate, error) {
	decisions, err := judgedDecisions(ctx, c.Permissions, c.collection, c.Env, c.Scope, perm, method)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	if denied := decisions.DeniedResources(); len(denied) > 0 {
		return nil, methodRefused(errors.Newf("user %s, scope %s, does not have %s on %s", c.Permissions.User(), c.Scope, perm, denied))
	}

	gate := &ExecuteGate{
		method:          method,
		user:            c.Permissions.User(),
		userPermissions: c.Permissions,
		scope:           c.Scope,
		env:             c.Env,
		collection:      c.collection,
	}
	if decision, ok := decisions[method]; ok && decision.IsConditional() {
		expr, err := conditionalExpr(method, decision)
		if err != nil {
			return nil, err
		}
		expr, err = settleRequestFields(c.collection, method, expr, request)
		if err != nil {
			return nil, err
		}
		if truth, constant := expr.(condition.Truth); !constant || !truth.Value {
			gate.cond = expr
		}
	}

	return gate, nil
}

// ExecuteGate answers the caller's Execute decision on another method, for a request
// the body builds in place of one a decoder read: what that method's own frame would
// decide for the request, in the scope and the environment the caller was checked in.
// A body that does through one method what another method does asks it, so the grant
// of the method that does the thing directly judges the same thing done another way: a
// person put in a group is given the group's roles, and is judged by the grant of the
// method that gives a role.
//
// Denied is the Forbidden the method's own decode answers. The atoms of a conditional
// grant that read the method's request fields (new.<name>) are settled against
// request, a value of the method's own request struct. What is left reads the method's
// target row and rides the gate: Settled says whether anything is left, and Enforce,
// EnforceView or EnforceProposed judges it against the row.
func (c *Caller) ExecuteGate(ctx context.Context, method accesstypes.Resource, request any) (*ExecuteGate, error) {
	if c.IsZero() {
		return nil, errNoCaller
	}

	return c.gateFor(ctx, method, accesstypes.Execute, request)
}

// ExecuteTarget describes the located row an ExecuteGate evaluates against.
// The generated frame supplies it from generation-time facts.
type ExecuteTarget struct {
	// Resource is the target row resource: its table, or the view's resource.
	Resource accesstypes.Resource

	// Label is the target's struct name. A refusal says the row by it in words
	// ("You may not do this to this mission.", TargetRefused), the same sentence
	// the frame's own state check refuses with, so the wire never distinguishes a
	// wrong-state refusal from a condition refusal. The NotFound answer and the
	// server's log say the name as it is.
	Label string

	// PKColumns are the primary-key columns the check-SELECT locates the row by,
	// in the key's order: one for a single-column key, each column of a compound
	// one. The key values the gate is handed follow the same order.
	PKColumns []string

	// View is the target's query when the target is a row of a view (a @virtual
	// struct): the struct's own value, whose Subquery the check-SELECT selects
	// from under the resource's name, as the view's lists and reads do. Nil for
	// a table.
	View interface {
		Subquery() (query string, params map[string]any)
	}
}

// targetKeyParamName locates the target row in the gate's check-SELECT; the
// zz prefix keeps it outside the lowering registry's @_c namespace and the
// reserved subject/now/domain vocabulary. A compound key's later columns take
// the name with their position (targetKeyParam).
const targetKeyParamName = "zzTargetKey"

// ExecuteGate carries one request's Execute decision to the generated frame.
// A nil condition (the grant was unconditional) enforces nothing.
type ExecuteGate struct {
	method          accesstypes.Resource
	user            accesstypes.User
	userPermissions UserPermissions
	scope           accesstypes.Scope
	env             accesstypes.Environment
	collection      *GeneratedCollection
	cond            condition.Expr
	// entryPermission is the permission a method's decode asked on the method, for the
	// gate its decode answers (TargetedRPCDecoder.Decode); empty for a gate a body asks
	// through Caller.ExecuteGate.
	entryPermission accesstypes.Permission
}

// Caller returns the caller the decode-time check ran as — checker, scope, sampled
// environment, and the collection — for the handler to stamp into the context the
// method's body runs under. The caller of the gate a method's decode answers carries the
// method and the permission the decode asked; the caller of a gate a body asks through
// Caller.ExecuteGate carries neither.
func (g *ExecuteGate) Caller() *Caller {
	caller := &Caller{Permissions: g.userPermissions, Scope: g.scope, Env: g.env, collection: g.collection}
	if g.entryPermission != "" {
		caller.method, caller.permission = g.method, g.entryPermission
	}

	return caller
}

// Settled reports what the gate decides before any row is read. settled is true where
// nothing of the grant's condition is left to judge against the target row: the grant
// carries no condition, or its condition read the request's fields alone, and granted
// says which way it went. Where settled is false the decision is the row's to make
// (Enforce, EnforceView, EnforceProposed).
func (g *ExecuteGate) Settled() (granted, settled bool) {
	if g == nil || g.cond == nil {
		return true, true
	}
	if truth, constant := g.cond.(condition.Truth); constant {
		return truth.Value, true
	}

	return false, false
}

// targetKeyParam names the parameter of the key column at position i: the bare
// name for the first, so a single-column key's statement reads as it always
// has, and the name with the position after it.
func targetKeyParam(i int) string {
	if i == 0 {
		return targetKeyParamName
	}

	return fmt.Sprintf("%s%d", targetKeyParamName, i)
}

// targetKey is the key one request names its target row by: the columns the
// frame declares and the values the request carries, one value for each column.
type targetKey struct {
	columns []string
	values  []any
}

// newTargetKey pairs the target's key columns with the request's values. The
// generated frame hands one value for each column; another count is generated
// code out of step with the library, and fails as that.
func (g *ExecuteGate) newTargetKey(target ExecuteTarget, pkValues []any) (*targetKey, error) {
	if len(target.PKColumns) == 0 {
		return nil, errors.Newf("method %s: target %s names no key column to locate its row by; regenerate", g.method, target.Resource)
	}
	if len(pkValues) != len(target.PKColumns) {
		return nil, errors.Newf("method %s: target %s is keyed by %d column(s) and the frame handed %d value(s); regenerate", g.method, target.Resource, len(target.PKColumns), len(pkValues))
	}

	return &targetKey{columns: target.PKColumns, values: pkValues}, nil
}

// where renders the predicate that names the row: each key column equal to its
// parameter.
func (k *targetKey) where() string {
	terms := make([]string, 0, len(k.columns))
	for i, column := range k.columns {
		terms = append(terms, fmt.Sprintf("%s = @%s", column, targetKeyParam(i)))
	}

	return strings.Join(terms, " AND ")
}

// bind sets the key's parameters.
func (k *targetKey) bind(params map[string]any) {
	for i, value := range k.values {
		params[targetKeyParam(i)] = value
	}
}

// String is the key as a message names the row: the value of a single-column
// key, the values of a compound one in order.
func (k *targetKey) String() string {
	if len(k.values) == 1 {
		return fmt.Sprint(k.values[0])
	}
	parts := make([]string, 0, len(k.values))
	for _, value := range k.values {
		parts = append(parts, fmt.Sprint(value))
	}

	return strings.Join(parts, ", ")
}

// Enforce evaluates the carried condition against the target row inside the
// frame's transaction: one check-SELECT locating the row by key, its single
// boolean the condition lowered against the target resource's bindings, bound
// to the same @subject, @now, and @domain the permission check folded with. A
// false or NULL answer is Forbidden with the frame's uniform refusal — the
// wire never says whether the state or the condition refused. The frame has
// located a table's row before it calls Enforce, so a grant with no condition
// enforces nothing; a view's row is located by EnforceView instead.
func (g *ExecuteGate) Enforce(ctx context.Context, txn ReadWriteTransaction, target ExecuteTarget, pkValues ...any) error {
	if g == nil || g.cond == nil {
		return nil
	}

	return g.enforce(ctx, txn, target, pkValues)
}

// EnforceView is the whole located-row check for a target that is a row of a
// view. One SELECT in the frame's transaction does both halves: it selects
// from the view's query by the key and, for a tenant-scoped view, the
// request's tenant, and its single boolean is the carried condition lowered
// against the view's bindings — TRUE where the grant carries none, since the
// row still has to be found. No row is the NotFound a missing row and another
// tenant's row share; a false or NULL answer is the frame's uniform refusal. A
// key that names more than one row there is a fault of the view's query, never
// a row to pick between, and fails as one.
func (g *ExecuteGate) EnforceView(ctx context.Context, txn ReadWriteTransaction, target ExecuteTarget, pkValues ...any) error {
	if g == nil {
		return errors.New("locating a view's row requires the decoder's ExecuteGate")
	}
	if target.View == nil {
		return errors.Newf("method %s: EnforceView locates a row of a view, and target %s carries no view query; a table's row is located by the frame and judged by Enforce", g.method, target.Resource)
	}

	return g.enforce(ctx, txn, target, pkValues)
}

// EnforceProposed judges the carried condition against a row the body proposes, where
// the database holds no row to locate: a role a person holds through a group is no row
// of the list of the roles held, and the method that takes a role away is granted on
// the rows of that list. row holds the target's attributes by the names a condition
// reads them, the target's @attribute declarations, each as the value the condition
// language compares (a string, an int64, a float64, a bool, a time.Time or a
// condition.Date, nil for no value).
//
// The condition is judged by the language's own evaluator (condition.Evaluate), the
// statement of its meaning that the SQL rendering is held to, with the caller and the
// decision instant the permission check folded with. A fact about the caller that is
// held in a table (subject.<name>) is read through facts, the body's transaction. A
// false or unknown answer is the frame's uniform refusal, and a grant with no
// condition enforces nothing. A condition that reads an attribute row does not carry
// is generated code out of step with the library, and fails as that.
func (g *ExecuteGate) EnforceProposed(ctx context.Context, facts SubjectFactSource, target ExecuteTarget, row map[string]any) error {
	if g == nil || g.cond == nil {
		return nil
	}
	for _, name := range condition.Bindings(g.cond) {
		if _, carried := row[name]; !carried {
			return errors.Newf("method %s: its condition reads %q and the proposed row of %s carries no such attribute; regenerate", g.method, name, target.Resource)
		}
	}
	image := &condition.Image{Pre: row, Post: nil, Subject: string(g.user), Now: time.Time{}, Zone: nil, SubjectSets: nil, SubjectValues: nil}
	now, ok := g.env.Now()
	if !ok && condition.UsesNow(g.cond) {
		return errors.Newf("method %s: its condition references now but the request environment carries no decision instant", g.method)
	}
	image.Now = now
	image.Zone, _ = g.env.Zone()
	if sets, values := condition.SubjectSets(g.cond), condition.SubjectValues(g.cond); len(sets)+len(values) > 0 {
		if g.collection == nil {
			return errors.Newf("method %s carries a conditional Execute decision but no generated collection is wired to read the caller's facts with", g.method)
		}
		var err error
		image.SubjectSets, image.SubjectValues, err = readSubjectFacts(ctx, facts, g.collection, g.scope, g.env, g.user, sets, values)
		if err != nil {
			return errors.Wrapf(err, "method %s", g.method)
		}
	}
	truth, err := condition.Evaluate(g.cond, image)
	if err != nil {
		return errors.Wrapf(err, "method %s: condition.Evaluate()", g.method)
	}
	if !truth.Permits() {
		return TargetRefused(g.method, target.Label)
	}

	return nil
}

// enforce runs the gate's check-SELECT and answers what it found: NotFound for
// no row, the uniform refusal for a false or NULL answer.
func (g *ExecuteGate) enforce(ctx context.Context, txn ReadWriteTransaction, target ExecuteTarget, pkValues []any) error {
	if txn.DBType() != SpannerDBType {
		return errors.Newf("execute-condition enforcement is not implemented for %s", txn.DBType())
	}
	if g.collection == nil {
		return errors.Newf("method %s carries a conditional Execute decision but no generated collection is wired to render it", g.method)
	}
	key, err := g.newTargetKey(target, pkValues)
	if err != nil {
		return err
	}

	stmt, err := g.checkStatement(target, key)
	if err != nil {
		return err
	}

	passed, found, err := g.runCheck(ctx, txn, stmt, target, key)
	if err != nil {
		return err
	}
	if !found {
		return TargetNotFound(target.Label, pkValues...)
	}
	if !passed.Valid || !passed.Bool {
		return TargetRefused(g.method, target.Label, pkValues...)
	}

	return nil
}

// runCheck runs a check-SELECT in the frame's transaction and reads its answer.
func (g *ExecuteGate) runCheck(ctx context.Context, txn ReadWriteTransaction, stmt *Statement, target ExecuteTarget, key *targetKey) (answer spanner.NullBool, found bool, err error) {
	it := txn.SpannerReadOnlyTransaction().Query(ctx, stmt.SpannerStatement())
	defer it.Stop()

	answer, found, err = readCheck(it.Next)
	if errors.Is(err, errCheckMatchedRows) {
		return spanner.NullBool{}, false, errors.Newf("method %s: key %s names more than one row of %s; a method's target is one row, so a view's query answers one row for a key, in a tenant where the view has one", g.method, key, target.Resource)
	}

	return answer, found, err
}

// errCheckMatchedRows says a check-SELECT matched more than one row.
var errCheckMatchedRows = errors.New("the check matched more than one row")

// readCheck reads a check-SELECT's answer off its rows: the one boolean of the
// one row it matched. found is false when it matched none, and a second row is
// errCheckMatchedRows: a table's key cannot name two rows, and a view's must
// not.
func readCheck(next func() (*spanner.Row, error)) (answer spanner.NullBool, found bool, err error) {
	row, err := next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return spanner.NullBool{}, false, nil
		}

		return spanner.NullBool{}, false, errors.Wrap(err, "spanner.RowIterator.Next()")
	}
	if err := row.Column(0, &answer); err != nil {
		return spanner.NullBool{}, false, errors.Wrap(err, "spanner.Row.Column()")
	}

	if _, err := next(); !errors.Is(err, iterator.Done) {
		if err != nil {
			return spanner.NullBool{}, false, errors.Wrap(err, "spanner.RowIterator.Next()")
		}

		return spanner.NullBool{}, false, errCheckMatchedRows
	}

	return answer, true, nil
}

// VerifyTenancy confirms the located target row resolves to the request's
// domain, for a target whose @domain binding leaves through a join path — a
// bare-column binding is read off the located row in the generated frame
// instead (resource.TenantKeyEquals), so the row itself carries no tenant key
// here. One SELECT in the frame's transaction: the tenancy predicate rendered
// from the Collection's domain binding as a single boolean, the row located
// by its primary key. A missing row and a cross-tenant row are the same
// NotFound the locate step answers — a row outside the caller's tenant never
// reveals it exists.
func (g *ExecuteGate) VerifyTenancy(ctx context.Context, txn ReadWriteTransaction, target ExecuteTarget, pkValues ...any) error {
	if g == nil {
		return errors.New("tenancy verification requires the decoder's ExecuteGate")
	}
	if txn.DBType() != SpannerDBType {
		return errors.Newf("tenancy verification is not implemented for %s", txn.DBType())
	}
	if g.collection == nil {
		return errors.Newf("method %s verifies tenancy against the generated collection, but none is wired", g.method)
	}

	key, err := g.newTargetKey(target, pkValues)
	if err != nil {
		return err
	}

	stmt, err := g.tenancyStatement(target, key)
	if err != nil {
		return err
	}

	inTenant, found, err := g.runCheck(ctx, txn, stmt, target, key)
	if err != nil {
		return err
	}
	if !found || !inTenant.Valid || !inTenant.Bool {
		return TargetNotFound(target.Label, pkValues...)
	}

	return nil
}

// tenancyStatement renders the tenancy check-SELECT from the target's domain
// binding: the bare form compares the tenant column, the path form walks the
// binding hops as the same EXISTS chain conditions lower through.
func (g *ExecuteGate) tenancyStatement(target ExecuteTarget, key *targetKey) (*Statement, error) {
	bindings, _ := g.collection.Bindings(g.collection.Scope(target.Resource), target.Resource)
	if bindings.Domain == nil {
		return nil, errors.Newf("method %s: target %s declares no domain binding to verify tenancy against", g.method, target.Resource)
	}
	domain, ok := g.scope.Domain()
	if !ok {
		return nil, errors.Newf("method %s: tenancy verification requires a partitioned request", g.method)
	}

	registry := newParamRegistry()
	outer := string(target.Resource)
	tenantColumn := columnComparand(outer, bindings.Domain.Column)

	var predicate ExpressionNode
	if len(bindings.Domain.Path) == 0 {
		predicate = &loweredComparisonNode{left: tenantColumn, op: "=", right: namedComparand(domainParamName)}
	} else {
		lctx := &loweringContext{outer: outer, bindings: bindings, collection: g.collection, partitioned: true}
		terminal, wrap := lctx.pathTarget(&tenantColumn, bindings.Domain.Path, registry)
		predicate = wrap(&loweredComparisonNode{left: terminal, op: "=", right: namedComparand(domainParamName)})
	}

	gen, err := loweredSQLGenerator(SpannerDBType)
	if err != nil {
		return nil, err
	}
	sql, err := gen.generateLowered(predicate, registry)
	if err != nil {
		return nil, errors.Wrapf(err, "method %s: rendering the tenancy predicate for %s", g.method, target.Resource)
	}

	params := map[string]any{domainParamName: string(domain)}
	key.bind(params)
	for _, param := range registry.boundParams() {
		params[param.Name] = param.Value
	}

	return &Statement{
		SQL:    fmt.Sprintf("SELECT (%s) AS g0 FROM %s WHERE %s", sql, target.Resource, key.where()),
		Params: params,
	}, nil
}

// checkStatement renders the gate's check-SELECT: the carried condition
// lowered against the target resource's bindings as one boolean — TRUE where
// the grant carries none — over the target row (targetRow).
func (g *ExecuteGate) checkStatement(target ExecuteTarget, key *targetKey) (*Statement, error) {
	bindings, _ := g.collection.Bindings(g.collection.Scope(target.Resource), target.Resource)
	registry := newParamRegistry()

	sql := sqlTrue
	if g.cond != nil {
		_, partitioned := g.scope.Domain()
		lctx := &loweringContext{
			outer:       string(target.Resource),
			bindings:    bindings,
			collection:  g.collection,
			partitioned: partitioned,
		}

		gen, err := loweredSQLGenerator(SpannerDBType)
		if err != nil {
			return nil, err
		}
		sql, err = lowerToSQL(g.cond, lctx, gen, registry)
		if err != nil {
			return nil, errors.Wrapf(err, "method %s: lowering the Execute condition against %s", g.method, target.Resource)
		}
	}

	params := map[string]any{}
	key.bind(params)
	for _, param := range registry.boundParams() {
		if _, ok := params[param.Name]; ok {
			return nil, errors.Newf("named parameter collision: %s check statement already contains named parameter %q", g.method, param.Name)
		}
		params[param.Name] = param.Value
	}
	for _, name := range registry.referencedNames() {
		value, err := g.reservedParamValue(name)
		if err != nil {
			return nil, err
		}
		params[name] = value
	}

	withClause, from, where, err := g.targetRow(target, key, bindings.Domain, params)
	if err != nil {
		return nil, err
	}

	return &Statement{
		SQL:    fmt.Sprintf("%sSELECT (%s) AS g0 FROM %s WHERE %s", withClause, sql, from, where),
		Params: params,
	}, nil
}

// targetRow renders where the check-SELECT finds the target row: the FROM item
// and the WHERE that names one row, with the WITH clause that leads the
// statement when the item needs one. A table's row is its primary key's. A
// view's row is its key's within the request's tenant, selected from the
// view's query exactly as the view's lists and reads select from it
// (viewFromItem): the tenant column is the view's bare @domain binding
// (tenancy), bound to the same @domain the permission check folded with, and
// the query's own parameters join params.
func (g *ExecuteGate) targetRow(target ExecuteTarget, key *targetKey, tenancy *DomainBindingData, params map[string]any) (withClause, from, where string, err error) {
	where = key.where()
	if target.View == nil {
		return "", string(target.Resource), where, nil
	}

	switch {
	case tenancy == nil:
		if g.collection.Scope(target.Resource) == accesstypes.DomainPermissionScope {
			return "", "", "", errors.Newf("method %s: target %s is a tenant-scoped view with no domain binding — @domain is mandatory on domain-scoped resources; regenerate", g.method, target.Resource)
		}
	case len(tenancy.Path) != 0:
		return "", "", "", errors.Newf("method %s: target %s is a view whose domain binding is a join path; a view's tenant is a column of its own projection", g.method, target.Resource)
	default:
		domain, ok := g.scope.Domain()
		if !ok {
			return "", "", "", errors.Newf("method %s: locating a row of the tenant-scoped view %s requires a partitioned request", g.method, target.Resource)
		}
		where += fmt.Sprintf(" AND %s = @%s", tenancy.Column, domainParamName)
		params[domainParamName] = string(domain)
	}

	withClause, from, viewParams, err := viewFromItem(target.Resource, target.View)
	if err != nil {
		return "", "", "", errors.Wrapf(err, "method %s", g.method)
	}
	for name, value := range viewParams {
		if _, ok := params[name]; ok {
			return "", "", "", errors.Newf("named parameter collision: %s query and the %s check statement both contain named parameter %q", target.Resource, g.method, name)
		}
		params[name] = value
	}
	if withClause != "" {
		withClause += "\n"
	}

	return withClause, from, where, nil
}

// reservedParamValue supplies a referenced reserved parameter's value: the
// same values the permission check folded with, never re-sampled.
func (g *ExecuteGate) reservedParamValue(name string) (any, error) {
	switch name {
	case subjectParamName:
		return string(g.user), nil
	case nowParamName:
		now, ok := g.env.Now()
		if !ok {
			return nil, errors.New("condition references now but the request environment carries no decision instant")
		}

		return now, nil
	case domainParamName:
		domain, ok := g.scope.Domain()
		if !ok {
			return nil, errors.New("condition rendering referenced the request partition in a global request")
		}

		return string(domain), nil
	default:
		return nil, errors.Newf("condition rendering referenced unknown named parameter %q", name)
	}
}
