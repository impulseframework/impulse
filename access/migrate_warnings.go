package access

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// This file flags the shapes a role file can carry that are legal and
// probably not what the author wanted. The release holds them as written;
// Client.CheckPolicy and ValidateRoles return them, one line each, for the
// deploy's migrate step to print.
//
// The existence probe (GrantWarning). A Delete, an Update, and a targeted
// Execute locate their row first and answer NotFound when it is absent, then
// evaluate the grant's condition and answer Forbidden when it fails. A read
// hides a row its condition does not select behind the same NotFound. So a
// role holding a conditional write on a resource it can neither Read nor List
// learns, from the response code alone, that a row exists — without changing
// it. The combination can be legitimate, and the check is a heuristic (a Read
// whose condition is narrower than the write's leaks the same way). An
// unconditional write reveals existence only by succeeding, which is what the
// grant permits, so it is not flagged.
//
// The concealing key (ConcealingKeyWarning). A sort or filter on a
// conditionally visible field runs over the visible projection, CASE WHEN
// <condition> THEN column END, so a masked cell is NULL wherever the query
// looks at it and nothing about a hidden value leaks through order or match.
// No index serves that expression: every page sorts the tenant's whole
// partition. The renderer drops the CASE when the row filter has already
// proven the field's condition on every surviving row — when every other
// field the role lists on the resource is granted under a condition that
// implies the field's own (spells it, or is an equality inside its IN list:
// condition.Implies) — and keeps it otherwise. A field may
// instead declare masking:"positional" at generation time: the cell stays
// hidden, the query runs on the real column, the index serves the page, and
// the field's rank is disclosed. The warning fires exactly where the renderer
// keeps the CASE on a field the resource orders by or admits as a sort or
// filter key; it is informational, never a refusal.

// Warning is one thing a deploy should hear about before the release takes
// traffic: a shape the role file carries as written but flags (GrantWarning,
// ConcealingKeyWarning), or something the policy store holds that this
// release cannot use as written (SkippedGrant, ShadowedRole,
// OrphanedMembership). A consumer that ranges and prints sees one line each,
// and one that switches on the kind reads its fields.
type Warning interface {
	fmt.Stringer

	// warning seals the interface to this package's kinds.
	warning()
}

// GrantWarning is one grant the role file carries as written but flags: a
// conditional Delete, Update, or targeted Execute in a role that holds neither
// Read nor List on the row resource the grant checks.
type GrantWarning struct {
	// Role holds the grant; Scope is the scope the role is declared at.
	Role  accesstypes.Role
	Scope accesstypes.PermissionScope

	// Permission and Resource name the grant: the row resource for Delete and
	// Update, the method for Execute.
	Permission accesstypes.Permission
	Resource   accesstypes.Resource

	// Row is the row resource whose existence the response code reveals: the
	// grant's own resource, or the method's @target.
	Row accesstypes.Resource

	// Condition is the grant's condition text.
	Condition string
}

func (GrantWarning) warning() {}

// String renders the warning as one line: what is granted, what the answer
// reveals, and what closes it.
func (w GrantWarning) String() string {
	row := string(w.Row)
	if w.Row != w.Resource {
		row = fmt.Sprintf("its @target %s", w.Row)
	}

	return fmt.Sprintf("role %s: %s on %s is granted under %q without Read or List on %s: a Forbidden answer tells the caller a %s row exists where a read would answer NotFound. Grant Read or List on %s in this role or in a role assigned with it, or accept the disclosure; a Read whose condition is narrower than this one leaks the same way.",
		w.Role, w.Permission, w.Resource, w.Condition, row, w.Row, w.Row)
}

// grantWarnings flags the role's conditional Delete, Update, and targeted
// Execute grants whose row resource the role can neither Read nor List, in
// permission, resource, condition order. It reads the expanded grant set, so a
// grant carrying fields is one warning for its base row, not one per field.
func grantWarnings(store PermissionCollection, role *Role, scope accesstypes.PermissionScope, set grantSet) []Warning {
	var warnings []Warning
	for _, perm := range []accesstypes.Permission{accesstypes.Delete, accesstypes.Execute, accesstypes.Update} {
		for _, res := range sortedResources(set[perm]) {
			if strings.ContainsRune(string(res), '.') {
				continue
			}

			row := res
			if perm == accesstypes.Execute {
				// A method without a @target locates no row: its conditions
				// are row-free and settle at decode time.
				target, targeted := store.MethodTarget(scope, res)
				if !targeted {
					continue
				}
				row = target
			}
			if set.reads(row) {
				continue
			}

			for _, condition := range sortedConditions(set[perm][res]) {
				if condition == "" {
					continue
				}
				warnings = append(warnings, GrantWarning{
					Role:       role.Name,
					Scope:      scope,
					Permission: perm,
					Resource:   res,
					Row:        row,
					Condition:  condition,
				})
			}
		}
	}

	return warnings
}

// ConcealingKeyWarning is one conditional List grant the role file carries
// as written but flags: it covers a field the resource orders by or admits as
// a sort or filter key, the field's masked cells conceal, and the role's other
// List grants on the resource leave the field's condition standing in the
// query, so a page this role orders or filters by the field sorts the tenant's
// whole partition.
type ConcealingKeyWarning struct {
	// Role holds the grant; Scope is the scope the role is declared at.
	Role  accesstypes.Role
	Scope accesstypes.PermissionScope

	// Resource is the listed resource and Field the concealing key on it, as
	// its wire tag.
	Resource accesstypes.Resource
	Field    accesstypes.Tag

	// Conditions are the conditions the role's List grants cover the field
	// under, sorted; the field's visible projection is their OR.
	Conditions []string

	// DefaultOrder marks a field of the resource's declared order: every page
	// this role lists pays, not only a page a request sorts or filters by the
	// field.
	DefaultOrder bool

	// Why the CASE stands. Unconditional: the role lists other fields of the
	// resource with no condition, so the row filter proves nothing. Uncovered:
	// the conditions, one disjunct each in canonical text, the role lists other
	// fields under and the field's own conditions do not imply
	// (condition.Implies), so the row filter admits rows the field's condition
	// does not select.
	Unconditional bool
	Uncovered     []string
}

func (ConcealingKeyWarning) warning() {}

// String renders the warning as one line: the grant, why its condition stays
// in the query, what that costs, and the three ways out.
func (w ConcealingKeyWarning) String() string {
	role, page := "a sort or filter key", fmt.Sprintf("a page this role sorts or filters by %s", w.Field)
	if w.DefaultOrder {
		role, page = "the default order", "every page this role lists"
	}
	reason := fmt.Sprintf("this role lists other %s fields unconditionally", w.Resource)
	if !w.Unconditional {
		reason = fmt.Sprintf("this role also lists %s fields under %s, which the field's condition does not cover", w.Resource, quoteEach(w.Uncovered))
	}

	return fmt.Sprintf("role %s: List on %s.%s is granted under %s, and %s is %s of %s whose masked cells conceal; %s, so the row filter does not prove the field's condition and %s orders on CASE WHEN <condition> THEN column END, which no index serves: it sorts the tenant's whole partition. Grant %s unconditionally in this role, tag the field masking:\"positional\" and disclose where its hidden values fall, or accept the cost for a table that never pages at volume.",
		w.Role, w.Resource, w.Field, quoteEach(w.Conditions), w.Field, role, w.Resource, reason, page, w.Field)
}

// quoteEach renders conditions as "a" or "b".
func quoteEach(conditions []string) string {
	quoted := make([]string, 0, len(conditions))
	for _, c := range conditions {
		quoted = append(quoted, fmt.Sprintf("%q", c))
	}

	return strings.Join(quoted, " or ")
}

// listFieldGrants reads the role's List field rows on res out of the expanded
// grant set: each field's conditions where every grant on it is conditional,
// and whether any field is granted with no condition at all.
func listFieldGrants(set grantSet, res accesstypes.Resource) (conditional map[accesstypes.Tag][]string, unconditional bool) {
	conditional = make(map[accesstypes.Tag][]string)
	prefix := string(res) + "."
	for fieldRes, conditions := range set[accesstypes.List] {
		if !strings.HasPrefix(string(fieldRes), prefix) {
			continue
		}
		if _, ok := conditions[""]; ok {
			unconditional = true

			continue
		}
		conditional[accesstypes.Tag(strings.TrimPrefix(string(fieldRes), prefix))] = sortedConditions(conditions)
	}

	return conditional, unconditional
}

// disjunctsOf compiles conditions and flattens each over OR.
func disjunctsOf(conditions []string) ([]condition.Expr, error) {
	var out []condition.Expr
	for _, source := range conditions {
		compiled, err := accesstypes.NewCondition(source)
		if err != nil {
			return nil, errors.Wrapf(err, "condition %q", source)
		}
		out = append(out, condition.Disjuncts(compiled.Expr())...)
	}

	return out, nil
}

// concealingKeyWarnings flags, per listed resource the role holds conditional
// field grants on, each concealing key the role lists under a condition the
// renderer would keep in the query: the role also lists a field of the
// resource unconditionally, or under a disjunct that does not imply the key's
// condition set. The covering test is the renderer's own (condition.Uncovered
// empty is condition.Covers), so the warning fires exactly where the CASE
// stays. Default-order fields come first, in declared order, then the sort and
// filter keys.
func concealingKeyWarnings(store PermissionCollection, role *Role, scope accesstypes.PermissionScope, set grantSet) ([]Warning, error) {
	var warnings []Warning
	for _, res := range sortedResources(set[accesstypes.List]) {
		if strings.ContainsRune(string(res), '.') {
			continue
		}
		order, keys := store.ConcealingKeys(scope, res)
		if len(order) == 0 && len(keys) == 0 {
			continue
		}

		conditional, unconditional := listFieldGrants(set, res)
		if len(conditional) == 0 {
			continue
		}

		// The row predicate the renderer builds from the widest projection this
		// role can ask for: every conditional field's disjuncts, first appearance
		// kept, in field order so the message is stable.
		var union []condition.Expr
		seen := make(map[string]struct{})
		for _, tag := range sortedTags(conditional) {
			disjuncts, err := disjunctsOf(conditional[tag])
			if err != nil {
				return nil, errors.Wrapf(err, "role %s: List on %s.%s", role.Name, res, tag)
			}
			for _, d := range disjuncts {
				if _, dup := seen[d.String()]; dup {
					continue
				}
				seen[d.String()] = struct{}{}
				union = append(union, d)
			}
		}

		visited := make(map[accesstypes.Tag]struct{})
		for _, key := range concealingKeyOrder(order, keys) {
			if _, done := visited[key.tag]; done {
				continue
			}
			visited[key.tag] = struct{}{}

			conditions, ok := conditional[key.tag]
			if !ok {
				// Not granted to this role (a sort on it is refused), or granted
				// with no condition: nothing masks.
				continue
			}
			own, err := disjunctsOf(conditions)
			if err != nil {
				return nil, errors.Wrapf(err, "role %s: List on %s.%s", role.Name, res, key.tag)
			}
			uncovered := condition.Uncovered(own, union)
			if !unconditional && len(uncovered) == 0 {
				continue
			}

			warnings = append(warnings, ConcealingKeyWarning{
				Role:          role.Name,
				Scope:         scope,
				Resource:      res,
				Field:         key.tag,
				Conditions:    conditions,
				DefaultOrder:  key.defaultOrder,
				Unconditional: unconditional,
				Uncovered:     sortedText(uncovered),
			})
		}
	}

	return warnings, nil
}

// concealingKey is one field the warning examines and how the resource uses it.
type concealingKey struct {
	tag          accesstypes.Tag
	defaultOrder bool
}

// concealingKeyOrder lists the default-order fields first, then the sort and
// filter keys sorted, so the warnings a resource raises come out stable.
func concealingKeyOrder(order, keys []accesstypes.Tag) []concealingKey {
	out := make([]concealingKey, 0, len(order)+len(keys))
	for _, tag := range order {
		out = append(out, concealingKey{tag: tag, defaultOrder: true})
	}
	sorted := slices.Clone(keys)
	slices.Sort(sorted)
	for _, tag := range sorted {
		out = append(out, concealingKey{tag: tag})
	}

	return out
}

// sortedText renders expressions as canonical text, sorted, for a stable
// message; nil for none.
func sortedText(exprs []condition.Expr) []string {
	if len(exprs) == 0 {
		return nil
	}
	out := make([]string, 0, len(exprs))
	for _, e := range exprs {
		out = append(out, e.String())
	}
	slices.Sort(out)

	return out
}

func sortedTags(m map[accesstypes.Tag][]string) []accesstypes.Tag {
	tags := make([]accesstypes.Tag, 0, len(m))
	for tag := range m {
		tags = append(tags, tag)
	}
	slices.Sort(tags)

	return tags
}
