package generation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/generation/parser/genlang"
)

// Declared transitions and located-row targets (ABAC design plan §09, §12):
// an RPC method that moves a workflow root along one state edge declares the
// edge instead of coding it — `@transition(Root, from: a, b, to: c)` on the
// @rpc struct, `@target` on the field carrying the target row's key. The
// generated handler owns the whole mechanical frame inside the transaction it
// already runs: it locates the target row within the tenancy predicate
// (absent or cross-tenant is NotFound), verifies the pre-image state is in
// the from set, evaluates any row-referencing Execute condition the caller's
// grant carries against the same row, runs the body, and stamps the to state
// as the last mutation. The body never reads or writes the state field — it
// carries only the business effect of the edge.
//
// A method that moves no state can still declare a target row —
// `@target(Root)` names the resource directly — and gets the plain form of
// the frame: locate, tenancy, condition, body, no state check and no stamp.
// Either way, who may run the method stays its Execute grant; a target is
// what lets that grant carry a row-referencing condition (§12).
//
// A target keyed by several columns is named by one @target field for each,
// every one declaring the key field it carries (@target(Root, key: Field)):
// the method still addresses one row, by its whole key. A @transition's root is
// keyed by one column.
//
// The plain form's root may be a view (a @virtual struct) keyed by its
// @primarykey field or fields. A view's row is one row of the view's query, and in a
// tenant-scoped view its key names a row in each tenant that holds one, so
// the frame reads no row by its key: it hands the gate the view's own value,
// named in the package the view is declared in, and the gate locates the row
// by the key within the request's tenant and judges the condition against it
// in one SELECT over the view's query (resource.ExecuteGate.EnforceView). A
// view has no @state to move, so it is never a @transition's root.

// The @transition annotation's named arguments.
const (
	transitionFromArgKey = "from"
	transitionToArgKey   = "to"
)

// targetKeyArgKey is the @target annotation's named argument: the key field of
// the target a field carries, where the target's key spans several columns.
const targetKeyArgKey = "key"

// rpcTarget is a validated @target declaration: the row the generated
// handler locates inside its transaction before the body runs. A
// transition's target root is named by its @transition; a plain method names
// it directly with @target(Root).
type rpcTarget struct {
	// RootStruct is the target's struct name.
	RootStruct string

	// RootResource is the target's registered resource name (pluralized).
	RootResource string

	// Keys pairs the RPC fields carrying the target row's key with the target's
	// key fields, in the key's order: one pair for a single-column key, one for
	// each column of a compound one.
	Keys []rpcTargetKey

	// StateField is the root's @state field, read for a transition's
	// pre-image check and stamped through Set<StateField> after the body;
	// empty for the plain form, which never touches state.
	StateField string

	// TenantField is the target's bare tenant-key field, read to locate the
	// row within the tenancy predicate; empty for a global-scoped target and
	// for a join-path binding (TenantByPath).
	TenantField string

	// TenantByPath marks a target whose @domain binding is a join path: the
	// located row carries no tenant key of its own, so the generated frame
	// verifies tenancy through the gate's join-path check instead.
	TenantByPath bool

	// ViewPackage marks a target that is a row of a view, and names the
	// package the view is declared in: the frame hands the gate the view's
	// value from there, and the gate locates the row. Empty for a table. A
	// view target carries neither TenantField nor TenantByPath: the gate
	// holds the row to the request's tenant by the view's @domain column,
	// which the collection carries.
	ViewPackage string
}

// rpcTargetKey is one column of a target row's key and the RPC field that
// carries its value.
type rpcTargetKey struct {
	// TargetField is the RPC field carrying this column of the target row's key.
	TargetField string

	// RootPKField is the target's key field: the query builder's Set<PKField>
	// setter locates the row, the update patch constructor takes its value.
	RootPKField string

	// RootPKColumn is the key field's database column — the Execute-condition
	// check-SELECT locates the row by it.
	RootPKColumn string
}

// LocateColumnCalls renders the located-row read's column accessors: the
// state and tenant fields when the frame reads them, else the key itself —
// the read exists to prove the row, so it always selects something.
func (t *rpcTarget) LocateColumnCalls() string {
	var b strings.Builder
	if t.StateField != "" {
		b.WriteString("." + t.StateField + "()")
	}
	if t.TenantField != "" {
		b.WriteString("." + t.TenantField + "()")
	}
	if b.Len() == 0 {
		b.WriteString("." + t.Keys[0].RootPKField + "()")
	}

	return b.String()
}

// KeyArgs renders the request's key values in the key's order, each read off
// the named receiver: `p.TaskID`, or `p.UserID, p.Role` for a compound key.
// The gate, the update patch's constructor, and a message's arguments take them.
func (t *rpcTarget) KeyArgs(receiver string) string {
	args := make([]string, 0, len(t.Keys))
	for _, key := range t.Keys {
		args = append(args, receiver+"."+key.TargetField)
	}

	return strings.Join(args, ", ")
}

// KeyVerbs renders the format verbs a message names the row with: one %s for
// each key value, as the gate's own messages join them.
func (t *rpcTarget) KeyVerbs() string {
	verbs := make([]string, 0, len(t.Keys))
	for range t.Keys {
		verbs = append(verbs, "%s")
	}

	return strings.Join(verbs, ", ")
}

// PKColumns renders the target's key columns as the Go literal the gate's
// ExecuteTarget takes: `[]string{"Id"}`, or `[]string{"UserId", "Role"}`.
func (t *rpcTarget) PKColumns() string {
	columns := make([]string, 0, len(t.Keys))
	for _, key := range t.Keys {
		columns = append(columns, fmt.Sprintf("%q", key.RootPKColumn))
	}

	return "[]string{" + strings.Join(columns, ", ") + "}"
}

// rpcTransition is a validated @transition declaration, resolved against the
// workflow root it names.
type rpcTransition struct {
	rpcTarget

	// From lists the pre-image states the transition may run from, in
	// declaration order.
	From []string

	// To is the state the generated handler stamps after the body returns.
	To string
}

// FromCases renders the from set as switch-case values: `"draft", "scheduled"`.
func (t *rpcTransition) FromCases() string {
	quoted := make([]string, 0, len(t.From))
	for _, v := range t.From {
		quoted = append(quoted, fmt.Sprintf("%q", v))
	}

	return strings.Join(quoted, ", ")
}

// resolveTransition compiles and validates an RPC method's @transition and
// @target annotations. An RPC method without either generates exactly what it
// generates today; @target alone is the plain located-row form.
func (c *client) resolveTransition(rpcMethod *rpcMethodInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations) error {
	targetIdxs := targetFieldIndexes(pStruct, annotations)

	if !annotations.Struct.Has(transitionKeyword) {
		if len(targetIdxs) == 0 {
			return nil
		}

		return c.resolvePlainTarget(rpcMethod, pStruct, annotations, targetIdxs)
	}

	invocations, err := annotations.Struct.Get(transitionKeyword).ParseInvocations(&genlang.ArgSpec{
		Positional: 1,
		Keys:       []string{transitionFromArgKey, transitionToArgKey},
		Required:   []string{transitionFromArgKey, transitionToArgKey},
		Multi:      []string{transitionFromArgKey},
	})
	if err != nil {
		return errors.Wrapf(err, "@%s on %s", transitionKeyword, pStruct.Name())
	}
	rootName := invocations[0].Positional[0]
	from := invocations[0].List(transitionFromArgKey)
	to, _ := invocations[0].Named(transitionToArgKey)

	switch len(targetIdxs) {
	case 0:
		return errors.Newf("struct %s: @%s requires exactly one @%s field carrying the target row's key", pStruct.Name(), transitionKeyword, targetKeyword)
	case 1:
	default:
		return errors.Newf("struct %s: @%s appears on fields %s and %s — a @%s moves one row of a workflow root, which is keyed by one column, so one field carries its key", pStruct.Name(), targetKeyword, pStruct.Fields()[targetIdxs[0]].Name(), pStruct.Fields()[targetIdxs[1]].Name(), transitionKeyword)
	}
	targetIdx := targetIdxs[0]
	if arg := annotations.Fields[targetIdx].Get(targetKeyword); len(arg) > 0 {
		return errors.Newf("struct %s field %s: with @%s, @%s takes no argument — the declared root is the target", pStruct.Name(), pStruct.Fields()[targetIdx].Name(), transitionKeyword, targetKeyword)
	}

	target, root, err := c.resolveTarget(rpcMethod, pStruct, []targetDeclaration{{index: targetIdx}}, rootName, transitionKeyword)
	if err != nil {
		return err
	}
	if root.IsVirtual {
		return errors.Newf("struct %s: @%s(%s): %s is a view, which has no @%s column to move — @%s names a table-backed workflow root; a method that takes a view's row as its target declares @%s(%s) on the field carrying the row's key, with no @%s", pStruct.Name(), transitionKeyword, rootName, rootName, stateKeyword, transitionKeyword, targetKeyword, rootName, transitionKeyword)
	}

	state := stateField(root)
	if state == nil {
		return errors.Newf("struct %s: @%s(%s): the root carries no @%s field", pStruct.Name(), transitionKeyword, rootName, stateKeyword)
	}
	target.StateField = state.Name()

	if err := c.validateTransitionStates(pStruct, rootName, state, from, to); err != nil {
		return err
	}

	rpcMethod.Transition = &rpcTransition{rpcTarget: *target, From: from, To: to}
	rpcMethod.Target = &rpcMethod.Transition.rpcTarget

	return nil
}

// targetDeclaration is one @target field of a method: the field, and the key
// field of the target it says it carries (empty where it says none).
type targetDeclaration struct {
	index int
	key   string
}

// resolvePlainTarget compiles @target(Root) without a @transition: the plain
// located-row form (design plan §12). The generated frame locates the row
// (absent or cross-tenant is NotFound) and evaluates any row-referencing
// Execute condition against it; there is no pre-image state check and no
// stamp — the method moves no state.
//
// A target keyed by one column is named by one field. A target keyed by
// several is named by one field for each column, every one of them declaring
// @target(Root, key: Field) with the key field it carries: the method still
// addresses one row, by its whole key.
func (c *client) resolvePlainTarget(rpcMethod *rpcMethodInfo, pStruct *parser.Struct, annotations genlang.StructAnnotations, targetIdxs []int) error {
	var (
		rootName  string
		rootField string
	)
	declarations := make([]targetDeclaration, 0, len(targetIdxs))
	for _, targetIdx := range targetIdxs {
		field := pStruct.Fields()[targetIdx]
		invocations, err := annotations.Fields[targetIdx].Get(targetKeyword).ParseInvocations(&genlang.ArgSpec{Positional: 1, Keys: []string{targetKeyArgKey}})
		if err != nil || len(invocations) == 0 {
			if err == nil {
				err = errors.New("missing argument")
			}

			return errors.Wrapf(err, "struct %s field %s: without @%s, @%s(Root) names the row resource the handler locates", pStruct.Name(), field.Name(), transitionKeyword, targetKeyword)
		}
		named := invocations[0].Positional[0]
		if rootName != "" && named != rootName {
			return errors.Newf("struct %s: @%s names %s on field %s and %s on field %s — a method addresses exactly one target row, so every @%s field names the same resource", pStruct.Name(), targetKeyword, rootName, rootField, named, field.Name(), targetKeyword)
		}
		rootName, rootField = named, field.Name()
		key, _ := invocations[0].Named(targetKeyArgKey)
		declarations = append(declarations, targetDeclaration{index: targetIdx, key: key})
	}

	target, _, err := c.resolveTarget(rpcMethod, pStruct, declarations, rootName, targetKeyword)
	if err != nil {
		return err
	}

	rpcMethod.Target = target

	return nil
}

// resolveTarget validates what both target forms share: the handler frame's
// preconditions and the named root's shape, resolved into an rpcTarget.
func (c *client) resolveTarget(rpcMethod *rpcMethodInfo, pStruct *parser.Struct, declarations []targetDeclaration, rootName, declKeyword string) (*rpcTarget, *resourceInfo, error) {
	switch {
	case rpcMethod.SuppressHandler:
		return nil, nil, errors.Newf("struct %s: @%s runs its checks in the generated handler, which @%s suppresses — remove one of them", pStruct.Name(), declKeyword, suppressKeyword)
	case !rpcMethod.IsTxnForm():
		return nil, nil, errors.Newf("struct %s: @%s requires the transaction form of Execute (second parameter resource.ReadWriteTransaction) — the located-row checks run inside the handler's transaction", pStruct.Name(), declKeyword)
	}

	var root *resourceInfo
	for _, res := range c.resources {
		if res.Name() == rootName {
			root = res

			break
		}
	}
	if root == nil {
		return nil, nil, errors.Newf("struct %s: @%s(%s) names an unknown resource struct", pStruct.Name(), declKeyword, rootName)
	}
	if scopeOrGlobal(rpcMethod.PermissionScope) != scopeOrGlobal(root.PermissionScope) {
		return nil, nil, errors.Newf("struct %s: @%s(%s): method and target permission scopes differ — a located-row check never crosses the tenancy structure", pStruct.Name(), declKeyword, rootName)
	}

	rootKeys, err := targetRootKeys(pStruct, rootName, root, declKeyword)
	if err != nil {
		return nil, nil, err
	}
	keys, err := targetKeys(pStruct, rootName, rootKeys, declarations)
	if err != nil {
		return nil, nil, err
	}

	target := &rpcTarget{
		RootStruct:   rootName,
		RootResource: c.pluralize(rootName),
		Keys:         keys,
	}

	if scopeOrGlobal(root.PermissionScope) == accesstypes.DomainPermissionScope {
		switch {
		case root.DomainBinding == nil:
			// Unreachable through the normal pipeline — validateDomainBinding
			// already rejects a domain-scoped resource with no binding.
			return nil, nil, errors.Newf("struct %s: @%s(%s): the located-row tenancy check needs the root's @%s binding, which the root does not declare", pStruct.Name(), declKeyword, rootName, domainKeyword)
		case root.IsVirtual:
			// The gate holds a view's row to the request's tenant by the
			// view's @domain column: the frame reads nothing off the row.
		case len(root.DomainBinding.Path) == 0:
			target.TenantField = root.DomainBinding.Anchor.Name()
		default:
			target.TenantByPath = true
		}
	}
	if root.IsVirtual {
		target.ViewPackage = c.resourcesDirOf(root).Package()
	}

	return target, root, nil
}

// targetFieldIndexes finds the @target fields, in declaration order; none when
// the method declares no target.
func targetFieldIndexes(pStruct *parser.Struct, annotations genlang.StructAnnotations) []int {
	var targetIdxs []int
	for i := range pStruct.Fields() {
		if annotations.Fields[i].Has(targetKeyword) {
			targetIdxs = append(targetIdxs, i)
		}
	}

	return targetIdxs
}

// targetKeys pairs a method's @target fields with the target's key fields, in
// the key's order. A single-column key is carried by the one @target field,
// which need not name it. Each column of a compound key is carried by the
// field that names it with key:, every column by exactly one field. A field's
// type is its key field's.
func targetKeys(pStruct *parser.Struct, rootName string, rootKeys []*resourceField, declarations []targetDeclaration) ([]rpcTargetKey, error) {
	fields := pStruct.Fields()
	keyNames := make([]string, 0, len(rootKeys))
	for _, pk := range rootKeys {
		keyNames = append(keyNames, pk.Name())
	}

	carriedBy := make(map[string]targetDeclaration, len(rootKeys))
	if len(rootKeys) == 1 {
		if len(declarations) > 1 {
			return nil, errors.Newf("struct %s: @%s appears on fields %s and %s — %s is keyed by one column (%s), and a method addresses exactly one target row, so one field carries its key", pStruct.Name(), targetKeyword, fields[declarations[0].index].Name(), fields[declarations[1].index].Name(), rootName, keyNames[0])
		}
		if key := declarations[0].key; key != "" && key != keyNames[0] {
			return nil, errors.Newf("struct %s field %s: @%s(%s, %s: %s): %s is keyed by %s", pStruct.Name(), fields[declarations[0].index].Name(), targetKeyword, rootName, targetKeyArgKey, key, rootName, keyNames[0])
		}
		carriedBy[keyNames[0]] = declarations[0]
	} else {
		for _, declaration := range declarations {
			field := fields[declaration.index]
			switch {
			case declaration.key == "":
				return nil, errors.Newf("struct %s field %s: @%s(%s): the target's key spans %d columns (%s), so each @%s field says which one it carries — @%s(%s, %s: %s)", pStruct.Name(), field.Name(), targetKeyword, rootName, len(rootKeys), strings.Join(keyNames, ", "), targetKeyword, targetKeyword, rootName, targetKeyArgKey, keyNames[0])
			case !slices.Contains(keyNames, declaration.key):
				return nil, errors.Newf("struct %s field %s: @%s(%s, %s: %s): %s is no key field of %s, which is keyed by %s", pStruct.Name(), field.Name(), targetKeyword, rootName, targetKeyArgKey, declaration.key, declaration.key, rootName, strings.Join(keyNames, ", "))
			}
			if other, taken := carriedBy[declaration.key]; taken {
				return nil, errors.Newf("struct %s: fields %s and %s both carry %s.%s — each column of the target's key is carried by one field", pStruct.Name(), fields[other.index].Name(), field.Name(), rootName, declaration.key)
			}
			carriedBy[declaration.key] = declaration
		}
	}

	keys := make([]rpcTargetKey, 0, len(rootKeys))
	var missing []string
	for _, pk := range rootKeys {
		declaration, ok := carriedBy[pk.Name()]
		if !ok {
			missing = append(missing, pk.Name())

			continue
		}
		targetField := fields[declaration.index]
		if targetField.Type() != pk.Type() {
			return nil, errors.Newf("struct %s field %s: @%s type %s does not match the root key %s.%s (%s)", pStruct.Name(), targetField.Name(), targetKeyword, targetField.Type(), rootName, pk.Name(), pk.Type())
		}
		keys = append(keys, rpcTargetKey{TargetField: targetField.Name(), RootPKField: pk.Name(), RootPKColumn: fieldColumn(pk)})
	}
	if len(missing) != 0 {
		return nil, errors.Newf("struct %s: @%s(%s): no field carries %s — a method names its target row by the whole key (%s), one @%s(%s, %s: Field) field for each column", pStruct.Name(), targetKeyword, rootName, strings.Join(missing, ", "), strings.Join(keyNames, ", "), targetKeyword, rootName, targetKeyArgKey)
	}

	return keys, nil
}

// validateTransitionStates checks every from/to value against the root's
// state enum table and rejects duplicate from values.
func (c *client) validateTransitionStates(pStruct *parser.Struct, rootName string, state *resourceField, from []string, to string) error {
	values := c.enumValues[state.ReferencedResource]
	isValue := func(v string) bool {
		return slices.ContainsFunc(values, func(e *enumData) bool { return e.ID == v })
	}
	for i, v := range from {
		if !isValue(v) {
			return errors.Newf("struct %s: @%s(%s): %q is not a value of the state enum table %q", pStruct.Name(), transitionKeyword, rootName, v, state.ReferencedResource)
		}
		if slices.Contains(from[:i], v) {
			return errors.Newf("struct %s: @%s(%s): %q appears twice in the from set", pStruct.Name(), transitionKeyword, rootName, v)
		}
	}
	if !isValue(to) {
		return errors.Newf("struct %s: @%s(%s): %q is not a value of the state enum table %q", pStruct.Name(), transitionKeyword, rootName, to, state.ReferencedResource)
	}

	return nil
}

// targetRootKeys resolves the key a method names its target row by — a table's
// primary key from the schema, a view's the fields it declares @primarykey on —
// in the key's order. A @transition's root is keyed by one column. A
// tenant-scoped view's key leaves its tenant column out: @domain holds a row
// to the request's tenant already, so a method names a row within one.
func targetRootKeys(pStruct *parser.Struct, rootName string, root *resourceInfo, declKeyword string) ([]*resourceField, error) {
	var keys []*resourceField
	for _, f := range root.Fields {
		if f.IsPrimaryKey {
			keys = append(keys, f)
		}
	}

	switch {
	case len(keys) == 0 && root.IsVirtual:
		return nil, errors.Newf("struct %s: @%s(%s): the view declares no @%s, so no row of it can be addressed — declare @%s on the field, or the fields, that name a row", pStruct.Name(), declKeyword, rootName, primarykeyKeyword, primarykeyKeyword)
	case len(keys) == 0:
		return nil, errors.Newf("struct %s: @%s(%s): the root has no primary-key field", pStruct.Name(), declKeyword, rootName)
	case len(keys) > 1 && declKeyword == transitionKeyword:
		return nil, errors.Newf("struct %s: @%s(%s): the root's primary key spans %d columns — a workflow root is keyed by one column, which the @%s field carries", pStruct.Name(), declKeyword, rootName, len(keys), targetKeyword)
	}

	if root.IsVirtual && root.IsDomainScoped() && root.DomainBinding != nil && len(root.DomainBinding.Path) == 0 {
		for _, key := range keys {
			if key.Name() == root.DomainBinding.Anchor.Name() {
				return nil, errors.Newf("struct %s: @%s(%s): the view's key includes its tenant column %s — in a tenant-scoped view the tenant column is not part of the key, since @%s already holds a row to its tenant, so declare @%s on the fields that name a row within a tenant", pStruct.Name(), declKeyword, rootName, key.Name(), domainKeyword, primarykeyKeyword)
			}
		}
	}

	return keys, nil
}

// rejectRPCOnlyAnnotations fails a struct of another kind that carries an annotation
// only an @rpc struct may: @transition and @schedule on the struct, and @target on a
// field. A resource declares no handler to frame and no method to call.
func rejectRPCOnlyAnnotations(pStruct *parser.Struct, annotations genlang.StructAnnotations, kind string) error {
	var errs []error
	if annotations.Struct.Has(transitionKeyword) {
		errs = append(errs, errors.Newf("struct %s: @%s is only valid on @%s structs; a %s declares no handler to frame", pStruct.Name(), transitionKeyword, rpcKeyword, kind))
	}
	if annotations.Struct.Has(scheduleKeyword) {
		errs = append(errs, errors.Newf("struct %s: @%s is only valid on @%s structs; a %s declares no method for Cloud Scheduler to call", pStruct.Name(), scheduleKeyword, rpcKeyword, kind))
	}
	for i, field := range pStruct.Fields() {
		if annotations.Fields[i].Has(targetKeyword) {
			errs = append(errs, errors.Newf("struct %s field %s: @%s is only valid on @%s structs; a %s declares no handler to frame", pStruct.Name(), field.Name(), targetKeyword, rpcKeyword, kind))
		}
	}
	if len(errs) > 0 {
		return errors.Wrap(errors.Join(errs...), "RPC-only annotation error")
	}

	return nil
}
