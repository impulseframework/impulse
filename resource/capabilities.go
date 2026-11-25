package resource

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
	"github.com/impulseframework/impulse/httpio"
)

// This file renders the capability envelope of the ABAC design plan (§13):
// per-row write affordances riding the read response, opt-in per request via
// the capabilities query parameter. The answers are advisory hints for the
// UI — which fields to render editable, whether the Delete button is live —
// computed from the same row image and the same decision instant (@now) the
// read already stamped, in the same statement. Enforcement is untouched: the
// write stages (§05) judge the mutation that actually arrives.
//
// Evaluation reuses the write path's decision shape. Per requested
// permission, the engine's decisions settle what they can without data —
// Granted marks a field editable (or the delete live), Denied and absence
// drop it — and only Conditional decisions render SQL: each distinct
// condition payload becomes one boolean in SELECT position against the
// pre-image, deduplicated across fields and permissions exactly like the
// write check's grouping. Pure RBAC therefore folds to zero extra SQL, and a
// capability-free request renders a byte-identical statement.
//
// A new.-referencing term is structurally unevaluable before the user types,
// so it counts as potentially-true — per TERM (condition.WithoutPostImage):
// the fail-open bound assumes every post-image atom passes while the
// evaluable residue still renders, so a state guard beside an old-vs-new
// conjunct keeps narrowing per row. Deliberately fail-open — for the hint
// only; apply-time enforcement judges what they actually typed.

const (
	// CapabilitiesProperty is the reserved per-row JSON property the generated
	// handlers attach capability answers under. Generation rejects resource
	// columns that would collide with it.
	CapabilitiesProperty = "zzCapabilities"

	// capabilityChecksColumnName is the read statement's reserved output
	// column carrying the condition-group booleans, aligned with the plan's
	// group order. Generation rejects resource columns that would collide
	// with it.
	capabilityChecksColumnName = "zzCapabilityChecks"
)

// capabilityPermissions returns the permissions capability evaluation
// supports: Update carries a positive list of editable field names, Delete a
// boolean — its footprint is the row — Execute a positive list of the RPC
// methods whose declared transitions apply to the row (§09), and Create a
// positive list of the workflow member resources the user may create beneath
// the row (§11 — the affordance rides the immediate parent hop; the
// permission digest answers every other create). Read-shaped permissions are
// answered by the response itself.
func capabilityPermission(name string) (accesstypes.Permission, error) {
	perm := accesstypes.Permission(name)
	switch perm {
	case accesstypes.Create, accesstypes.Update, accesstypes.Delete, accesstypes.Execute:
		return perm, nil
	default:
		return "", httpio.NewBadRequestMessagef("capability evaluation supports %s, %s, %s and %s, not %q", accesstypes.Create, accesstypes.Update, accesstypes.Delete, accesstypes.Execute, name)
	}
}

// capabilityPlan is one statement's capability evaluation: how each row's
// reserved property assembles from the scanned group booleans. It is built
// beside the statement and carried on it for the reader.
type capabilityPlan struct {
	// checksColumn names the reserved boolean-array output column, "" when no
	// group needs SQL — the statement is then byte-identical to the
	// capability-free statement and every answer assembles from grants alone.
	checksColumn string

	// groups counts the rendered booleans, the condition groups and the marks'
	// (capabilityGroups); the scanned array must carry exactly this many members.
	groups int

	perms []plannedCapability

	// enteringParams are the parameters the TenantsToEnter query binds where the
	// statement has the entering boolean, which holds a row to the tenants its
	// caller enters; the statement merges them beside its own
	// (mergeEnteringParams). Nil otherwise.
	enteringParams map[string]any
}

// plannedCapability is one requested permission's assembly recipe.
type plannedCapability struct {
	perm accesstypes.Permission

	// isDelete selects the boolean shape; otherwise the positive field list.
	isDelete bool

	// fields lists the Update affordance's candidates in name order: every
	// field of the resource the write permission is registered on, projected
	// or not.
	fields []capabilityField

	// allowed is Delete's data-free answer; group overrides it when >= 0.
	allowed bool

	// group indexes the checks array for a conditional Delete, -1 when
	// allowed is settled without data.
	group int
}

// capabilityField is one field of an Update affordance, or one member or
// method of a Create or Execute affordance: included in the positive list
// when its group boolean holds, or unconditionally when group is -1 (an
// unconditional grant, or a potentially-true new. condition), and, for a
// method, when each of its mark booleans holds too.
type capabilityField struct {
	jsonName string
	group    int

	// marks index the mark booleans a method needs beside its condition's: the
	// statement's entering boolean (@callerEnters) and the own-account boolean
	// of its column (@notOnOwnAccount). Nil where no mark is in force.
	marks []int
}

// assemble builds one row's capability property from the scanned group
// booleans. A NULL boolean arrives as false — a condition permits only on
// TRUE (§05), and the hint follows the enforcement's reading.
func (p *capabilityPlan) assemble(checks []bool) map[accesstypes.Permission]any {
	holds := func(group int) bool {
		return group < 0 || (group < len(checks) && checks[group])
	}
	listed := func(field capabilityField) bool {
		if !holds(field.group) {
			return false
		}
		for _, mark := range field.marks {
			if !holds(mark) {
				return false
			}
		}

		return true
	}

	out := make(map[accesstypes.Permission]any, len(p.perms))
	for _, planned := range p.perms {
		if planned.isDelete {
			allowed := planned.allowed
			if planned.group >= 0 {
				allowed = holds(planned.group)
			}
			out[planned.perm] = allowed

			continue
		}

		names := make([]string, 0, len(planned.fields))
		for _, field := range planned.fields {
			if listed(field) {
				names = append(names, field.jsonName)
			}
		}
		out[planned.perm] = names
	}

	return out
}

// checkCapabilityPermissions runs the requested capability checks — one
// engine call per permission, the same environment and scope as the read —
// and carries the full Decisions for the plan. Capabilities are advisory:
// a Denied answer shapes the hint and never fails the request.
func (q *QuerySet[Resource]) checkCapabilityPermissions(ctx context.Context) error {
	if len(q.capabilities) == 0 || q.resourceSet == nil || q.userPermissions == nil {
		return nil
	}

	for _, perm := range q.capabilities {
		resources := []accesstypes.Resource{q.resourceSet.BaseResource()}
		switch perm {
		case accesstypes.Delete:
			// Delete's footprint is the row: the base resource carries it.
		case accesstypes.Execute:
			// The affordance question is per targeted method: the RPC method
			// resources whose @target row is this resource — a declared
			// transition or the plain located-row form (§09, §12) — and the
			// methods that name no row and are offered on its rows.
			if q.collection == nil {
				continue
			}
			var err error
			if resources, err = q.executeResources(ctx); err != nil {
				return err
			}
			if len(resources) == 0 {
				continue
			}
		case accesstypes.Create:
			// The affordance question is per member resource: the workflow
			// members whose immediate parent hop is this resource (§11).
			if q.collection == nil {
				continue
			}
			resources = q.collection.MembersOf(q.resourceSet.BaseResource())
			if len(resources) == 0 {
				continue
			}
		default:
			// The affordance question is per writable field: every field of the
			// resource the write permission is registered on, whether or not the
			// read projected it. The read's Set registers the read contract alone
			// (a write-only field carries json:"-" there), so the generated
			// collection, which registers every field with its permissions, is
			// the carrier; permission-exempt primary keys carry none and are
			// never editable.
			if q.collection == nil {
				return errors.Newf("resource %s asked for the %s capability, which names the fields the caller may write, but no generated collection is wired to name them", q.Resource(), perm)
			}
			resources = resources[:0]
			for _, tag := range q.collection.TagsRequiring(q.resourceSet.BaseResource(), perm) {
				resources = append(resources, q.resourceSet.BaseResource().ResourceWithTag(tag))
			}
			if len(resources) == 0 {
				continue
			}
		}

		decisions, err := judgedDecisions(ctx, q.userPermissions, q.collection, q.env, q.scope, perm, resources...)
		if err != nil {
			return errors.Wrap(err, "resource.UserPermissions.Check()")
		}
		if q.capabilityDecisions == nil {
			q.capabilityDecisions = make(map[accesstypes.Permission]accesstypes.Decisions, len(q.capabilities))
		}
		q.capabilityDecisions[perm] = decisions
	}

	return nil
}

// executeResources answers the RPC methods the Execute capability asks about on the
// resource's rows (executeCandidates), and reads once what their marks need: the
// accounts that are the caller's own, for a method a row keeps off the caller's own
// account (@notOnOwnAccount), read from the session; and the tenants this session
// does not enter, for a method offered only to a caller whose session enters the
// row's tenants (@callerEnters), one that names the row or one offered on it, asked
// once for the statement.
func (q *QuerySet[Resource]) executeResources(ctx context.Context) ([]accesstypes.Resource, error) {
	candidates := executeCandidates(q.collection, q.resourceSet.BaseResource())
	resources := make([]accesstypes.Resource, 0, len(candidates))
	var held accesstypes.Resource
	keptOffOwn := false
	for _, tm := range candidates {
		resources = append(resources, tm.Method)
		if tm.CallerEnters && held == "" {
			held = tm.Method
		}
		if tm.OwnAccountColumn != "" {
			keptOffOwn = true
		}
	}
	if len(resources) == 0 {
		return nil, nil
	}
	if keptOffOwn && q.ownAccounts == nil {
		q.ownAccounts = sessionOwnAccounts(ctx)
	}
	if held != "" {
		if err := q.askUnentered(ctx, held); err != nil {
			return nil, err
		}
	}

	return resources, nil
}

// capabilityGroups interns the statement's booleans across every requested
// permission. They are of three kinds, each rendered alone: a condition
// boolean for each distinct condition payload, exactly the write check's
// grouping, shared by every method under that condition, marked or not; the
// entering boolean, at most one a statement, which holds a row to the tenants
// its caller's session enters (@callerEnters); and an own-account boolean for
// each distinct column, which holds the row's column to none of the caller's
// own accounts (@notOnOwnAccount). A method is listed where its condition
// boolean and each of its mark booleans hold (capabilityField.marks), so the
// entering subquery is in a statement once, however many conditions the
// methods held to it carry and whichever of them are marked.
type capabilityGroups struct {
	bySource map[string]int

	// exprs are the condition booleans' expressions, by boolean; nil for a mark
	// boolean.
	exprs []condition.Expr

	// held says, by boolean, which one is the entering boolean. ownColumns
	// name, by boolean, the row's column an own-account boolean holds to none
	// of the caller's own accounts, "" for every other boolean.
	held       []bool
	ownColumns []string
}

// intern resolves one Conditional decision's payload to its group index,
// returning -1 for a payload whose fail-open bound is a constant. Post-image
// terms count potentially-true per TERM (WithoutPostImage): a term depending
// on a value the user has not yet typed assumes TRUE, while the evaluable
// residue still narrows the hint — so a state guard beside an old-vs-new
// conjunct keeps gating per row (§13's fail-open posture, applied per term).
func (g *capabilityGroups) intern(res accesstypes.Resource, decision accesstypes.Decision) (int, error) {
	expr, err := conditionalExpr(res, decision)
	if err != nil {
		return 0, err
	}
	expr = condition.WithoutPostImage(expr)
	if _, ok := expr.(condition.Truth); ok {
		// A fully potentially-true payload (an unknown atom is unknown in
		// both polarities, so the bound never simplifies to FALSE).
		return -1, nil
	}

	return g.internExpr(expr), nil
}

// internExpr resolves an already-built expression to its group index, deduped
// by canonical source like intern.
func (g *capabilityGroups) internExpr(expr condition.Expr) int {
	source := expr.String()
	idx, ok := g.bySource[source]
	if !ok {
		idx = len(g.exprs)
		g.bySource[source] = idx
		g.exprs = append(g.exprs, expr)
		g.held = append(g.held, false)
		g.ownColumns = append(g.ownColumns, "")
	}

	return idx
}

// internEntering resolves the statement's entering boolean to its index: the
// one boolean that holds a row to the tenants its caller's session enters
// (@callerEnters), made the first time a method needs it.
func (g *capabilityGroups) internEntering() int {
	if idx := slices.Index(g.held, true); idx >= 0 {
		return idx
	}

	return g.internMark(true, "")
}

// internOwnAccount resolves the own-account boolean of the row's column to its
// index: one boolean a column, holding the column to none of the caller's own
// accounts (@notOnOwnAccount), made the first time a method needs it.
func (g *capabilityGroups) internOwnAccount(column string) int {
	if idx := slices.Index(g.ownColumns, column); idx >= 0 {
		return idx
	}

	return g.internMark(false, column)
}

// internMark appends a mark boolean, one with no expression of its own: the
// entering boolean where held says so, else the own-account boolean of
// ownColumn.
func (g *capabilityGroups) internMark(held bool, ownColumn string) int {
	idx := len(g.exprs)
	g.exprs = append(g.exprs, nil)
	g.held = append(g.held, held)
	g.ownColumns = append(g.ownColumns, ownColumn)

	return idx
}

// stateMembershipExpr builds a transition's from set as a `state IN (…)`
// expression over the pre-image's uniform state binding, sorted for a
// canonical key so every transition with the same set shares one boolean.
func stateMembershipExpr(from []string) condition.Expr {
	values := slices.Clone(from)
	slices.Sort(values)
	literals := make([]condition.Literal, 0, len(values))
	for _, v := range values {
		literals = append(literals, condition.StringLiteral{Value: v})
	}

	return condition.In{Left: condition.Ref{Name: StateAttribute}, Literals: literals}
}

// plannedDelete builds Delete's recipe from the base decision — the delete's
// footprint is the row, so the base decision is the condition's sole carrier
// (§05).
func (q *QuerySet[Resource]) plannedDelete(decisions accesstypes.Decisions, groups *capabilityGroups) (plannedCapability, error) {
	planned := plannedCapability{perm: accesstypes.Delete, isDelete: true, group: -1}
	decision := decisions[q.resourceSet.BaseResource()]
	switch {
	case decision.IsGranted():
		planned.allowed = true
	case decision.IsConditional():
		group, err := groups.intern(q.resourceSet.BaseResource(), decision)
		if err != nil {
			return plannedCapability{}, err
		}
		if group < 0 {
			planned.allowed = true
		} else {
			planned.group = group
		}
	}

	return planned, nil
}

// plannedUpdate builds one write permission's per-field recipe over every field
// of the resource the permission is registered on, in name order, projected or
// not: the envelope speaks for every field the caller may write, so a write-only
// field, which no read returns, is one more field in the loop. The candidates
// are the collection's; checkCapabilityPermissions asked the same set.
func (q *QuerySet[Resource]) plannedUpdate(perm accesstypes.Permission, decisions accesstypes.Decisions, groups *capabilityGroups) (plannedCapability, error) {
	planned := plannedCapability{perm: perm, group: -1}
	if q.collection == nil {
		return planned, nil
	}
	for _, tag := range q.collection.TagsRequiring(q.resourceSet.BaseResource(), perm) {
		res := q.resourceSet.BaseResource().ResourceWithTag(tag)
		decision := decisions[res]
		group := -1
		switch {
		case decision.IsGranted():
		case decision.IsConditional():
			var err error
			group, err = groups.intern(res, decision)
			if err != nil {
				return plannedCapability{}, err
			}
		default:
			continue
		}
		planned.fields = append(planned.fields, capabilityField{jsonName: string(tag), group: group})
	}

	return planned, nil
}

// plannedExecute builds the Execute affordance's recipe: the RPC methods
// whose @target row is this resource (§09, §12), gated by the user's Execute
// grants. A transition method's boolean is the row's pre-image state
// membership in its from set; a conditional grant ANDs its condition into the
// same boolean (a plain method's is the condition alone); a plain method with
// an unconditional grant is structural — no boolean, no SQL. A method offered
// only to a caller whose session enters the row's tenants (@callerEnters) also
// needs the statement's entering boolean while the session does not enter some
// tenant; while it enters every one, the method is planned as any other. A
// method that names no row and is offered on the resource's rows
// (@callerEnters naming the resource) is a candidate beside them, planned as a
// plain method held to the tenants the caller enters. A method a row keeps off
// the caller's own account (@notOnOwnAccount) also needs the own-account
// boolean of its column while the request has an account of its own; while it
// has none, the method is planned as any other. A planned method carries its
// condition boolean, -1 for none, and the mark booleans it also needs, and is
// listed where each of them holds: distinct conditions share one boolean per
// statement, marked or not, the entering boolean is one a statement and the
// own-account boolean one a column (capabilityGroups).
func (q *QuerySet[Resource]) plannedExecute(decisions accesstypes.Decisions, groups *capabilityGroups) (plannedCapability, error) {
	planned := plannedCapability{perm: accesstypes.Execute, group: -1}
	if q.collection == nil {
		return planned, nil
	}
	for _, tm := range executeCandidates(q.collection, q.resourceSet.BaseResource()) {
		decision := decisions[tm.Method]
		var exprs []condition.Expr
		if tm.Transition != nil {
			exprs = append(exprs, stateMembershipExpr(tm.Transition.From))
		}
		switch {
		case decision.IsGranted():
		case decision.IsConditional():
			expr, err := conditionalExpr(tm.Method, decision)
			if err != nil {
				return plannedCapability{}, err
			}
			// A term that reads the request (new.<field>) has nothing to read here: a
			// row is answered before a request is written. It is dropped as potentially
			// true, the posture intern takes, so the method is offered and the call
			// itself is judged.
			expr = condition.WithoutPostImage(expr)
			if _, ok := expr.(condition.Truth); !ok {
				exprs = append(exprs, expr)
			}
		default:
			continue
		}

		var expr condition.Expr
		switch len(exprs) {
		case 1:
			expr = exprs[0]
		case 2:
			expr = condition.And{Operands: exprs}
		}
		group := -1
		if expr != nil {
			group = groups.internExpr(expr)
		}
		var marks []int
		if tm.CallerEnters && len(q.unentered) > 0 {
			marks = append(marks, groups.internEntering())
		}
		if tm.OwnAccountColumn != "" && len(q.ownAccounts) > 0 {
			marks = append(marks, groups.internOwnAccount(tm.OwnAccountColumn))
		}
		planned.fields = append(planned.fields, capabilityField{jsonName: string(tm.Method), group: group, marks: marks})
	}

	return planned, nil
}

// executeCandidates lists the Execute affordance's candidates on a row of res, in one
// list in method-name order: every method that targets res (MethodsTargeting), and every
// method that names no row and is offered on the rows of res (MethodsOfferedOn), the
// latter as a plain method that targets res and is held to the tenants the caller's
// session enters.
func executeCandidates(collection *GeneratedCollection, res accesstypes.Resource) []TargetedMethod {
	candidates := collection.MethodsTargeting(res)
	offered := collection.MethodsOfferedOn(res)
	if len(offered) == 0 {
		return candidates
	}
	for _, method := range offered {
		candidates = append(candidates, TargetedMethod{Method: method, Target: res, CallerEnters: true})
	}
	slices.SortFunc(candidates, func(a, b TargetedMethod) int {
		return strings.Compare(string(a.Method), string(b.Method))
	})

	return candidates
}

// plannedCreate builds the create-under-parent affordance's recipe (§11): the
// workflow member resources whose immediate parent hop is this resource,
// gated by the user's Create grants on each member. An unconditional grant is
// structural — the member lists with no SQL; a conditional grant contributes
// its state-evaluable residue, lowered against this row's own uniform state
// binding (the member's synthesized state IS its parent's state, so the
// immediate-parent row answers it), while every other term counts
// potentially-true (§13's fail-open posture, per term).
func (q *QuerySet[Resource]) plannedCreate(decisions accesstypes.Decisions, groups *capabilityGroups) (plannedCapability, error) {
	planned := plannedCapability{perm: accesstypes.Create, group: -1}
	if q.collection == nil {
		return planned, nil
	}
	for _, member := range q.collection.MembersOf(q.resourceSet.BaseResource()) {
		decision := decisions[member]
		group := -1
		switch {
		case decision.IsGranted():
		case decision.IsConditional():
			expr, err := conditionalExpr(member, decision)
			if err != nil {
				return plannedCapability{}, err
			}
			expr = condition.FailOpen(expr, stateEvaluable)
			if _, ok := expr.(condition.Truth); !ok {
				group = groups.internExpr(expr)
			}
		default:
			continue
		}
		planned.fields = append(planned.fields, capabilityField{jsonName: string(member), group: group})
	}

	return planned, nil
}

// stateEvaluable reports whether an atom of a member's Create condition can
// be answered by the parent row: every reference it carries is the uniform
// state binding, read from the pre-image. Anything else — a column the
// created row would carry, a post-image reference, an attribute compared to
// another attribute — is unknown before the user creates, and counts
// potentially-true.
func stateEvaluable(atom condition.Expr) bool {
	switch n := atom.(type) {
	case condition.Comparison:
		if n.Left.PostImage || n.Left.Name != StateAttribute {
			return false
		}
		if _, ok := n.Right.(condition.Ref); ok {
			return false
		}

		return true
	case condition.In:
		return !n.Left.PostImage && n.Left.Name == StateAttribute
	case condition.NullTest:
		return !n.Left.PostImage && n.Left.Name == StateAttribute
	default:
		return true
	}
}

// renderCapabilities builds the statement's capability plan and, when any
// condition group needs data, the reserved boolean-array select item. Both
// are empty when the request asked for no capabilities.
func (q *QuerySet[Resource]) renderCapabilities(dbType DBType, registry *paramRegistry) (*capabilityPlan, string, error) {
	if len(q.capabilities) == 0 || q.resourceSet == nil || q.userPermissions == nil {
		return nil, "", nil
	}

	plan := &capabilityPlan{}
	groups := &capabilityGroups{bySource: make(map[string]int)}

	for _, perm := range q.capabilities {
		var planned plannedCapability
		var err error
		switch perm {
		case accesstypes.Delete:
			planned, err = q.plannedDelete(q.capabilityDecisions[perm], groups)
		case accesstypes.Execute:
			planned, err = q.plannedExecute(q.capabilityDecisions[perm], groups)
		case accesstypes.Create:
			planned, err = q.plannedCreate(q.capabilityDecisions[perm], groups)
		default:
			planned, err = q.plannedUpdate(perm, q.capabilityDecisions[perm], groups)
		}
		if err != nil {
			return nil, "", err
		}
		plan.perms = append(plan.perms, planned)
	}

	groupExprs := groups.exprs
	if len(groupExprs) == 0 {
		return plan, "", nil
	}

	if q.collection == nil {
		return nil, "", errors.Newf("resource %s carries conditional capability decisions but no generated collection is wired to render them", q.Resource())
	}

	gen, err := loweredSQLGenerator(dbType)
	if err != nil {
		return nil, "", err
	}
	lctx := q.loweringCtx()

	var entering TenantsToEnter
	if slices.Contains(groups.held, true) {
		entering, err = q.enteringDeclaration()
		if err != nil {
			return nil, "", err
		}
		plan.enteringParams = entering.Params
	}

	terms := make([]string, len(groupExprs))
	for i := range groupExprs {
		terms[i], err = q.capabilityTerm(groups, i, gen, lctx, entering, registry)
		if err != nil {
			return nil, "", err
		}
	}

	plan.checksColumn = capabilityChecksColumnName
	plan.groups = len(groupExprs)

	return plan, capabilityChecksItem(dbType, terms), nil
}

// capabilityTerm renders the boolean at index i alone: a condition boolean's
// expression lowered; the entering boolean's term, holding the row to the
// tenants the caller's session enters (@callerEnters); or an own-account
// boolean's term, holding the row's column to none of the caller's own
// accounts (@notOnOwnAccount). A method that needs more than one boolean is
// listed where each holds (capabilityPlan.assemble), so no term is ANDed into
// another.
func (q *QuerySet[Resource]) capabilityTerm(groups *capabilityGroups, i int, gen *sqlGenerator, lctx *loweringContext, entering TenantsToEnter, registry *paramRegistry) (string, error) {
	switch {
	case groups.held[i]:
		return "(" + q.enteringTerm(gen, lctx.outer, entering, registry) + ")", nil
	case groups.ownColumns[i] != "":
		return "(" + ownAccountTerm(gen, lctx.outer, groups.ownColumns[i], q.ownAccounts, registry) + ")", nil
	}
	sql, err := lowerToSQL(groups.exprs[i], lctx, gen, registry)
	if err != nil {
		return "", err
	}

	return "(" + sql + ")", nil
}

// capabilityChecksItem renders the reserved boolean-array select item.
func capabilityChecksItem(dbType DBType, terms []string) string {
	if dbType == PostgresDBType {
		return fmt.Sprintf(`ARRAY[%s] AS %q`, strings.Join(terms, ", "), capabilityChecksColumnName)
	}

	return fmt.Sprintf("ARRAY<BOOL>[%s] AS %s", strings.Join(terms, ", "), capabilityChecksColumnName)
}
