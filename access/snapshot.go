package access

import (
	"crypto/sha256"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// snapshot is an immutable, fully-compiled view of the policy store. It is
// built once per load and shared by reference; evaluation is lock-free and
// allocation-free. Subtraction never happens at evaluation time (additive-only
// invariant): a snapshot only encodes what is granted.
type snapshot struct {
	perms     map[accesstypes.Permission]uint16
	resources map[string]uint16   // base resource name -> dense ID ("" = scope-wide)
	fields    []map[string]uint16 // by resource ID: field name -> bit position
	loadedAt  time.Time

	// Policy is held in three places and read in two. global is the global
	// partition. every is what reaches every tenant domain: the release's
	// domain default roles, the custom roles held in every domain, and the
	// users holding every-domain memberships. domains holds, for each domain
	// the store has rows in, what those rows add on top of every: the roles
	// and users they touch, resolved with every's contribution folded in. A
	// lookup in a domain reads the domain's entry when it has one for the
	// subject and every's otherwise, so a membership held in every domain
	// reaches a tenant the store holds no row in, and a thousand tenants
	// sharing the release's defaults share one compiled copy of them.
	global  *scopePolicy
	every   *scopePolicy
	domains map[accesstypes.Domain]*scopePolicy

	// permNames and resourceNames are the reverse of perms and resources,
	// aligned by dense ID: the digest enumeration walks grant maps and needs
	// names back out of keys.
	permNames     []accesstypes.Permission
	resourceNames []string

	// conditions interns the distinct condition texts, sorted so a term set
	// resolves to canonically ordered texts; compiled carries each text's
	// vocabulary AST, aligned by index. Compilation happens once, at load —
	// malformed condition text fails the load, never a check.
	conditions []string
	compiled   []condition.Expr
	condIDs    map[string]uint16

	// recordsHash identifies the snapshot's source content; the heartbeat
	// skips recompiling when a fresh read matches it.
	recordsHash [sha256.Size]byte

	// writeGen is the engine's local write generation observed BEFORE the
	// store read this snapshot was compiled from: any local write up to and
	// including that generation is guaranteed to be reflected here.
	writeGen int64
}

// scopePolicy holds one partition's fully-resolved grants. Role inheritance
// and per-user role combination are folded at compile time, so a check is a
// single subject lookup.
type scopePolicy struct {
	roleGrants map[accesstypes.Role]grantMap
	userGrants map[accesstypes.User]grantMap

	// userRoles and userDirect are the memberships and direct grants the
	// partition compiled its users from, kept so a domain overlay can resolve
	// a user again over every's rows with the domain's own added.
	userRoles  map[accesstypes.User][]accesstypes.Role
	userDirect map[accesstypes.User]grantMap
}

// grantKey packs (permission ID, resource ID) into one map key.
type grantKey uint32

func packGrantKey(permID, resID uint16) grantKey {
	return grantKey(permID)<<16 | grantKey(resID)
}

func unpackGrantKey(key grantKey) (permID, resID uint16) {
	return uint16(key >> 16), uint16(key & 0xFFFF)
}

// grantMap is a subject's complete effective grants: everything it holds
// directly, through role membership, and through role inheritance.
type grantMap map[grantKey]*fieldSet

// condTerms is a sorted, deduplicated set of interned condition IDs with OR
// semantics: the target is covered when any one condition holds. Methods
// never mutate their receiver in place after building — merges replace
// slices — so term sets alias safely across merged grant maps.
type condTerms []uint16

// with returns the terms with id included, keeping the set sorted.
func (t condTerms) with(id uint16) condTerms {
	i, ok := slices.BinarySearch(t, id)
	if ok {
		return t
	}

	return slices.Insert(t, i, id)
}

// union returns the sorted union of t and other, aliasing when either side
// is empty.
func (t condTerms) union(other condTerms) condTerms {
	switch {
	case len(other) == 0:
		return t
	case len(t) == 0:
		return other
	}

	merged := slices.Clone(t)
	for _, id := range other {
		merged = merged.with(id)
	}

	return merged
}

// condSet is a fieldSet's conditional coverage: for each target an endpoint,
// all-fields, or single-field grant can occupy, the conditions of the
// conditional grants covering it. It records coverage only — settling (an
// unconditional cover making conditions moot) happens at evaluation, which
// consults conditional coverage only after the unconditional check missed.
// Stores without conditions never allocate one.
type condSet struct {
	endpoint condTerms
	all      condTerms
	fields   map[uint16]condTerms // by field bit position
}

func (c *condSet) clone() *condSet {
	if c == nil {
		return nil
	}
	out := &condSet{endpoint: c.endpoint, all: c.all}
	if c.fields != nil {
		out.fields = maps.Clone(c.fields)
	}

	return out
}

// orIn merges src's coverage into c.
func (c *condSet) orIn(src *condSet) {
	c.endpoint = c.endpoint.union(src.endpoint)
	c.all = c.all.union(src.all)
	for i, terms := range src.fields {
		if c.fields == nil {
			c.fields = make(map[uint16]condTerms, len(src.fields))
		}
		c.fields[i] = c.fields[i].union(terms)
	}
}

// fieldSet is what one subject holds on one (permission, resource) pair.
// endpoint and field grants are distinct: an endpoint grant alone gives no
// field visibility, and field grants alone do not grant the endpoint.
// all is an implication flag, never materialized bits: it covers fields that
// did not exist when the grant was written.
// endpoint/all/bits encode unconditional grants only; conditional grants
// land in cond, nil unless one exists.
type fieldSet struct {
	endpoint bool
	all      bool
	bits     []uint64
	cond     *condSet
}

func (f *fieldSet) setBit(i uint16) {
	f.bits[i/64] |= 1 << (i % 64)
}

func (f *fieldSet) bit(i uint16) bool {
	return f.bits[i/64]&(1<<(i%64)) != 0
}

// ensureCond returns f's condSet, allocating it on first use.
func (f *fieldSet) ensureCond() *condSet {
	if f.cond == nil {
		f.cond = &condSet{}
	}

	return f.cond
}

// orIn merges src into f. Both bitsets are sized by the resource's field
// count, so lengths always match.
func (f *fieldSet) orIn(src *fieldSet) {
	f.endpoint = f.endpoint || src.endpoint
	f.all = f.all || src.all
	for i, w := range src.bits {
		f.bits[i] |= w
	}
	if src.cond != nil {
		if f.cond == nil {
			f.cond = src.cond.clone()
		} else {
			f.cond.orIn(src.cond)
		}
	}
}

func (f *fieldSet) clone() *fieldSet {
	return &fieldSet{
		endpoint: f.endpoint,
		all:      f.all,
		bits:     slices.Clone(f.bits),
		cond:     f.cond.clone(),
	}
}

// resourceDecision is the engine-internal decision for one checked resource.
// The zero value is denied (fail closed); granted marks an unconditional
// cover; conditions carries the covering conditional grants' condition texts
// (OR semantics, canonically ordered) when only conditional grants cover the
// resource, with exprs the matching compiled trees, aligned by index.
type resourceDecision struct {
	granted    bool
	conditions []string
	exprs      []condition.Expr
}

// newDecisions assembles the public Decisions for one checked batch, aligned
// by input order, folding facts first. Grouping is the engine's job — only
// the engine owns grant-set identity: a Conditional decision carries one
// ConditionGroup whose Resources lists every checked resource in the batch
// sharing that covering condition set (first-appearance input order,
// deduplicated), and every member's Decision carries the same group value, so
// callers deduplicate by sorted-Resources equality.
//
// Each distinct covering set's any-of combination folds once against the
// facts: TRUE settles its members Granted (some covering condition already
// holds), FALSE settles them Denied (no covering condition can hold), and
// anything left is a Conditional decision whose group payload carries the
// folded expression — only what the database must still evaluate.
//
// The group key is the canonically ordered condition-text set of the covering
// grants — grant identity, never the folded text, so members stay grouped
// even when folding rewrites the payload. Covering sets whose texts are
// identical produce identical any-of expressions, so collapsing them is pure
// deduplication: their group booleans could never differ.
func newDecisions(resources []accesstypes.Resource, decisions []resourceDecision, facts condition.Facts) (accesstypes.Decisions, error) {
	// Pass 1: fold each distinct covering set once and collect its members in
	// input order, so every member's group names the complete set before
	// decisions build.
	type foldedGroup struct {
		settled bool // folding reached a definite answer
		holds   bool // that answer, when settled
		group   accesstypes.ConditionGroup
	}
	folded := make(map[string]*foldedGroup)
	for i, d := range decisions {
		if d.granted || len(d.conditions) == 0 {
			continue
		}
		key := joinConditions(d.conditions)
		fg, ok := folded[key]
		if !ok {
			result, err := condition.Fold(anyOf(d.exprs), facts)
			if err != nil {
				return nil, errors.Wrapf(err, "folding the conditions covering %s", resources[i])
			}
			fg = &foldedGroup{}
			if t, isTruth := result.(condition.Truth); isTruth {
				fg.settled = true
				fg.holds = t.Value
			} else {
				fg.group = accesstypes.ConditionGroup{Condition: accesstypes.ConditionFromExpr(result)}
			}
			folded[key] = fg
		}
		if !fg.settled && !slices.Contains(fg.group.Resources, resources[i]) {
			fg.group.Resources = append(fg.group.Resources, resources[i])
		}
	}

	out := make(accesstypes.Decisions, len(resources))
	for i, d := range decisions {
		switch {
		case d.granted:
			out[resources[i]] = accesstypes.Granted()
		case len(d.conditions) > 0:
			fg := folded[joinConditions(d.conditions)]
			switch {
			case fg.settled && fg.holds:
				out[resources[i]] = accesstypes.Granted()
			case fg.settled:
				out[resources[i]] = accesstypes.Denied()
			default:
				out[resources[i]] = accesstypes.Conditional(fg.group)
			}
		default:
			out[resources[i]] = accesstypes.Denied()
		}
	}

	return out, nil
}

// anyOf combines a covering set's compiled conditions into the set's single
// any-of expression: the target is covered when any one condition holds.
func anyOf(exprs []condition.Expr) condition.Expr {
	if len(exprs) == 1 {
		return exprs[0]
	}

	return condition.Or{Operands: exprs}
}

// joinConditions builds the grouping key for one covering condition set: the
// canonically ordered texts, NUL-joined (the joinRoles idiom). Equal keys mean
// the covering sets' any-of combinations render as the same expression.
func joinConditions(conditions []string) string {
	var b strings.Builder
	for _, c := range conditions {
		b.WriteString(c)
		b.WriteByte(0)
	}

	return b.String()
}

// checkUser returns user's scope-wide decision within scope — the decision
// for the empty resource, which real resources can never occupy (validated
// non-empty at every write boundary). Grants reach a user through role
// membership or records written directly against the user, both folded into
// one lookup at compile time. Conditional coverage here is always row-free
// (the load rejects anything else), so the caller folds it to a definite
// answer.
func (s *snapshot) checkUser(user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission) resourceDecision {
	return s.decideScopeWide(s.userGrants(scope, user), perm)
}

// checkRole returns role's scope-wide decision within scope: what checkUser
// answers a member holding only that role, minus the membership lookup. A
// role's effective grants are its own plus its transitive parents', folded at
// compile time exactly as they are for its members.
func (s *snapshot) checkRole(role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission) resourceDecision {
	return s.decideScopeWide(s.roleGrants(scope, role), perm)
}

// decideUserResources returns user's decision for each resource within
// scope, aligned with the input order.
func (s *snapshot) decideUserResources(user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) []resourceDecision {
	return s.decideResources(s.userGrants(scope, user), perm, resources)
}

// decideRoleResources returns role's decision for each resource within
// scope, aligned with the input order — the role twin of
// decideUserResources, over the role's effective grants.
func (s *snapshot) decideRoleResources(role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) []resourceDecision {
	return s.decideResources(s.roleGrants(scope, role), perm, resources)
}

// decideScopeWide answers the scope-wide check against one subject's grants:
// the decision for the empty resource. Every check primitive is a function
// of a grant map, so users and roles share it.
func (s *snapshot) decideScopeWide(grants grantMap, perm accesstypes.Permission) resourceDecision {
	permID, ok := s.perms[perm]
	if !ok {
		return resourceDecision{}
	}

	return s.decide(grants, permID, "")
}

// decideResources answers each resource against one subject's grants,
// aligned with the input order. An unknown permission fails every resource
// closed.
func (s *snapshot) decideResources(grants grantMap, perm accesstypes.Permission, resources []accesstypes.Resource) []resourceDecision {
	decisions := make([]resourceDecision, len(resources))
	permID, ok := s.perms[perm]
	if !ok {
		return decisions
	}
	for i, resource := range resources {
		decisions[i] = s.decide(grants, permID, resource)
	}

	return decisions
}

// userHasGrants reports whether user holds at least one grant in scope —
// any permission, resource-attached or scope-wide, unconditional or
// conditional. Role membership alone is not a grant: a user whose roles
// resolve to nothing can observe nothing in the scope.
func (s *snapshot) userHasGrants(scope accesstypes.Scope, user accesstypes.User) bool {
	return len(s.userGrants(scope, user)) > 0
}

// roleHasGrants reports whether role holds at least one grant in scope, own
// or inherited — the foothold a session operating as the role has there. A
// role that exists but resolves to no grants has none.
func (s *snapshot) roleHasGrants(scope accesstypes.Scope, role accesstypes.Role) bool {
	return len(s.roleGrants(scope, role)) > 0
}

// userDigest returns user's structural grant enumeration within scope: every
// resource and field the user's grants reach, each mapping permission to
// granted (an unconditional cover exists) or conditional (only conditional
// grants cover it). Denied targets are absent — the digest is fail-closed by
// construction — and nothing folds: no facts are consulted, so the answer is
// a pure function of the snapshot.
//
// Two grant shapes have no enumerable name and are deliberately outside the
// digest: scope-wide grants (they attach to no resource) and the coverage an
// all-fields grant extends to fields the snapshot has never seen named (the
// enumeration lists the known field vocabulary; a check still answers for
// the unnamed field itself).
func (s *snapshot) userDigest(scope accesstypes.Scope, user accesstypes.User) accesstypes.PermissionDigest {
	return s.digest(s.userGrants(scope, user))
}

// roleDigest returns role's structural grant enumeration within scope — the
// digest a session operating as the role shows its frontend. See userDigest
// for what the enumeration covers and leaves out.
func (s *snapshot) roleDigest(scope accesstypes.Scope, role accesstypes.Role) accesstypes.PermissionDigest {
	return s.digest(s.roleGrants(scope, role))
}

// digest enumerates one subject's grants into the digest shape.
func (s *snapshot) digest(grants grantMap) accesstypes.PermissionDigest {
	digest := make(accesstypes.PermissionDigest, len(grants))
	set := func(res accesstypes.Resource, perm accesstypes.Permission, state accesstypes.DigestState) {
		entry := digest[res]
		if entry == nil {
			entry = make(map[accesstypes.Permission]accesstypes.DigestState)
			digest[res] = entry
		}
		entry[perm] = state
	}
	for key, fs := range grants {
		permID, resID := unpackGrantKey(key)
		base := s.resourceNames[resID]
		if base == "" {
			continue
		}
		perm := s.permNames[permID]
		switch {
		case fs.endpoint:
			set(accesstypes.Resource(base), perm, accesstypes.DigestGranted)
		case fs.cond != nil && len(fs.cond.endpoint) > 0:
			set(accesstypes.Resource(base), perm, accesstypes.DigestConditional)
		}
		for field, i := range s.fields[resID] {
			var state accesstypes.DigestState
			switch {
			case fs.all || fs.bit(i):
				state = accesstypes.DigestGranted
			case fs.cond != nil && (len(fs.cond.all) > 0 || len(fs.cond.fields[i]) > 0):
				state = accesstypes.DigestConditional
			default:
				continue
			}
			set(accesstypes.Resource(base).ResourceWithTag(accesstypes.Tag(field)), perm, state)
		}
	}

	return digest
}

// userPermissions enumerates user's effective permissions within scope by
// name: the permissions held scope-wide and the permissions held on each
// resource, all-fields grants as Resource.* and field grants as
// Resource.field, a conditional grant counted as held. The management
// listing answers from the same compiled policy the checks do.
func (s *snapshot) userPermissions(scope accesstypes.Scope, user accesstypes.User) accesstypes.UserScopePermissions {
	out := accesstypes.UserScopePermissions{Resources: make(map[accesstypes.Resource][]accesstypes.Permission)}
	s.forEachHeld(s.userGrants(scope, user), func(perm accesstypes.Permission, res accesstypes.Resource) {
		if res == "" {
			out.ScopeWide = append(out.ScopeWide, perm)

			return
		}
		out.Resources[res] = append(out.Resources[res], perm)
	})
	slices.Sort(out.ScopeWide)
	for _, perms := range out.Resources {
		slices.Sort(perms)
	}

	return out
}

// rolePermissions enumerates role's effective grants within scope by name,
// inheritance folded, in the shape the management listing speaks: each
// permission held scope-wide, on resources, or both — the role twin of
// userPermissions.
func (s *snapshot) rolePermissions(scope accesstypes.Scope, role accesstypes.Role) accesstypes.RolePermissionCollection {
	out := make(accesstypes.RolePermissionCollection)
	s.forEachHeld(s.roleGrants(scope, role), func(perm accesstypes.Permission, res accesstypes.Resource) {
		pg := out[perm]
		if res == "" {
			pg.ScopeWide = true
		} else {
			pg.Resources = append(pg.Resources, res)
		}
		out[perm] = pg
	})
	for perm, pg := range out {
		slices.Sort(pg.Resources)
		out[perm] = pg
	}

	return out
}

// forEachHeld visits each (permission, target) a grant map holds, by name:
// the empty resource for a scope-wide grant, the base resource for an
// endpoint grant, Resource.* for an all-fields grant and Resource.field for a
// field grant, unconditional and conditional alike. Visit order is the map's.
func (s *snapshot) forEachHeld(grants grantMap, visit func(perm accesstypes.Permission, res accesstypes.Resource)) {
	for key, fs := range grants {
		permID, resID := unpackGrantKey(key)
		perm := s.permNames[permID]
		base := accesstypes.Resource(s.resourceNames[resID])
		if fs.endpoint || (fs.cond != nil && len(fs.cond.endpoint) > 0) {
			visit(perm, base)
		}
		if base == "" {
			continue
		}
		if fs.all || (fs.cond != nil && len(fs.cond.all) > 0) {
			visit(perm, base.ResourceWithTag("*"))
		}
		for field, i := range s.fields[resID] {
			if fs.bit(i) || (fs.cond != nil && len(fs.cond.fields[i]) > 0) {
				visit(perm, base.ResourceWithTag(accesstypes.Tag(field)))
			}
		}
	}
}

// userGrants resolves user's effective grants within scope: the global
// partition's entry for the global scope; in a domain, the domain's own entry
// when its rows touched the user and every's otherwise.
func (s *snapshot) userGrants(scope accesstypes.Scope, user accesstypes.User) grantMap {
	if scope.IsGlobal() {
		return s.global.userGrants[user]
	}
	domain, _ := scope.Domain()
	if dp := s.domains[domain]; dp != nil {
		if gm, ok := dp.userGrants[user]; ok {
			return gm
		}
	}

	return s.every.userGrants[user]
}

// roleGrants resolves role's effective grants within scope the way userGrants
// resolves a user's.
func (s *snapshot) roleGrants(scope accesstypes.Scope, role accesstypes.Role) grantMap {
	if scope.IsGlobal() {
		return s.global.roleGrants[role]
	}
	domain, _ := scope.Domain()
	if dp := s.domains[domain]; dp != nil {
		if gm, ok := dp.roleGrants[role]; ok {
			return gm
		}
	}

	return s.every.roleGrants[role]
}

// decide answers one resource against a subject's grants. Any unconditional
// cover settles the decision as granted — conditions on other covering
// grants are moot — so conditional coverage is consulted only after the
// unconditional check (the pre-ABAC rules, kept verbatim as the RBAC oracle
// in the snapshot tests) missed. A conditional
// field cover is the union of the single-field terms and the all-fields
// terms; a field unknown to the snapshot is covered only by all-fields
// grants, mirroring the unconditional implication rule.
func (s *snapshot) decide(grants grantMap, permID uint16, resource accesstypes.Resource) resourceDecision {
	if grants == nil {
		return resourceDecision{}
	}

	base, field := splitResourceField(string(resource))
	resID, ok := s.resources[base]
	if !ok {
		return resourceDecision{}
	}

	fs := grants[packGrantKey(permID, resID)]
	if fs == nil {
		return resourceDecision{}
	}

	var terms condTerms
	switch field {
	case "":
		if fs.endpoint {
			return resourceDecision{granted: true}
		}
		if fs.cond != nil {
			terms = fs.cond.endpoint
		}
	case "*":
		if fs.all {
			return resourceDecision{granted: true}
		}
		if fs.cond != nil {
			terms = fs.cond.all
		}
	default:
		if fs.all {
			return resourceDecision{granted: true}
		}
		i, known := s.fields[resID][field]
		if known && fs.bit(i) {
			return resourceDecision{granted: true}
		}
		if fs.cond != nil {
			terms = fs.cond.all
			if known {
				terms = terms.union(fs.cond.fields[i])
			}
		}
	}
	if len(terms) == 0 {
		return resourceDecision{}
	}

	conditions := make([]string, len(terms))
	exprs := make([]condition.Expr, len(terms))
	for i, id := range terms {
		conditions[i] = s.conditions[id]
		exprs[i] = s.compiled[id]
	}

	return resourceDecision{conditions: conditions, exprs: exprs}
}

// newSnapshot compiles the release's default roles and the store's records
// into an immutable snapshot, and reports what the store holds that this
// release cannot use as written: a grant it skipped (SkippedGrant), a custom
// role a default of the same name shadows, whose grants it skipped
// (ShadowedRole), and memberships naming a role that is neither a default nor
// a custom role in their scope, which grant nothing (OrphanedMembership). The
// findings are sorted for a stable report. defaults and collection may be
// nil: no default roles, and grant names unchecked.
func newSnapshot(records *policy.Records, defaults *defaultRoles, collection PermissionCollection, loadedAt time.Time) (*snapshot, []policyFinding, error) {
	s := &snapshot{
		perms:       make(map[accesstypes.Permission]uint16),
		resources:   make(map[string]uint16),
		condIDs:     make(map[string]uint16),
		domains:     make(map[accesstypes.Domain]*scopePolicy),
		loadedAt:    loadedAt,
		recordsHash: records.Hash(),
	}

	customs, storeGrants, findings := definedRoles(records, defaults)
	all := make([]policy.Grant, 0, len(defaults.allGrants())+len(storeGrants))
	all = append(all, defaults.allGrants()...)
	all = append(all, storeGrants...)
	grants, skipped, err := s.intern(all, collection)
	if err != nil {
		return nil, nil, err
	}
	for _, g := range skipped {
		findings = append(findings, g)
	}
	memberships, orphaned := resolvedMemberships(records.Memberships, defaults, customs)
	findings = append(findings, orphaned...)
	sortFindings(findings)

	// Group the records by where they are held and compile the global
	// partition and every-domain policy outright; each domain with rows of
	// its own is then an overlay on every.
	global, every, byDomain := groupByScope(grants, memberships)
	s.global = s.compileScope(global.grants, global.memberships)
	s.every = s.compileScope(every.grants, every.memberships)
	for domain, sr := range byDomain {
		s.domains[domain] = s.overlayScope(s.every, sr.grants, sr.memberships)
	}

	return s, findings, nil
}

// definedRoles sorts the store's roles against the release's defaults: a
// custom role whose name a default of its kind has is shadowed — the default
// wins, so the custom role's grants are left out and its members hold the
// default's grants — and is reported; the rest are the custom roles the
// snapshot defines, keyed by where they are held. A role with grants is
// defined where its grants are held whether or not its row came back: the
// stores keep a grant under its role row, so this only widens the set for
// records built by hand. It returns the store's grants less the shadowed
// roles'.
func definedRoles(records *policy.Records, defaults *defaultRoles) (customs map[policy.Role]bool, grants []policy.Grant, findings []policyFinding) {
	customs = make(map[policy.Role]bool, len(records.Roles))
	shadowed := make(map[policy.Role]bool)
	for _, r := range records.Roles {
		if defaults.exists(r.Scope, r.Name) {
			shadowed[r] = true
			findings = append(findings, &ShadowedRole{Scope: r.Scope, Role: r.Name})

			continue
		}
		customs[r] = true
	}
	grants = make([]policy.Grant, 0, len(records.Grants))
	for _, g := range records.Grants {
		if g.Subject.Kind == policy.SubjectRole {
			role := policy.Role{Scope: g.Scope, Name: accesstypes.Role(g.Subject.Name)}
			if shadowed[role] {
				continue
			}
			if !defaults.exists(role.Scope, role.Name) {
				customs[role] = true
			}
		}
		grants = append(grants, g)
	}

	return customs, grants, findings
}

// resolvedMemberships keeps the memberships whose role is defined as seen
// from where the membership is held — a default of the scope's kind, a custom
// role held in the scope, or, for one domain, a custom role held in every
// domain — and reports the rest once per (scope, role) with their members
// sorted: an orphaned membership grants nothing anywhere, not even in a
// domain whose own rows happen to define a role of that name. Role-to-role
// edges pass through; a parent nothing defines contributes nothing.
func resolvedMemberships(memberships []policy.Membership, defaults *defaultRoles, customs map[policy.Role]bool) ([]policy.Membership, []policyFinding) {
	resolves := func(scope accesstypes.PolicyScope, role accesstypes.Role) bool {
		if defaults.exists(scope, role) || customs[policy.Role{Scope: scope, Name: role}] {
			return true
		}
		if _, oneDomain := scope.Domain(); oneDomain {
			return customs[policy.Role{Scope: accesstypes.EveryDomainPolicyScope(), Name: role}]
		}

		return false
	}

	kept := make([]policy.Membership, 0, len(memberships))
	orphans := make(map[policy.Role][]accesstypes.User)
	for _, m := range memberships {
		if m.Member.Kind == policy.SubjectUser && !resolves(m.Scope, m.Role) {
			key := policy.Role{Scope: m.Scope, Name: m.Role}
			orphans[key] = append(orphans[key], accesstypes.User(m.Member.Name))

			continue
		}
		kept = append(kept, m)
	}
	findings := make([]policyFinding, 0, len(orphans))
	for key, users := range orphans {
		slices.Sort(users)
		findings = append(findings, &OrphanedMembership{Scope: key.Scope, Role: key.Name, Users: slices.Compact(users)})
	}

	return kept, findings
}

// scopeRecords is one partition's grants and memberships.
type scopeRecords struct {
	grants      []policy.Grant
	memberships []policy.Membership
}

// groupByScope sorts records by where they are held: the global partition,
// every domain, and each domain with rows of its own.
func groupByScope(grants []policy.Grant, memberships []policy.Membership) (global, every scopeRecords, byDomain map[accesstypes.Domain]*scopeRecords) {
	byDomain = make(map[accesstypes.Domain]*scopeRecords)
	recordsFor := func(scope accesstypes.PolicyScope) *scopeRecords {
		switch {
		case scope.IsGlobal():
			return &global
		case scope.IsEveryDomain():
			return &every
		}
		domain, _ := scope.Domain()
		sr := byDomain[domain]
		if sr == nil {
			sr = &scopeRecords{}
			byDomain[domain] = sr
		}

		return sr
	}
	for _, g := range grants {
		sr := recordsFor(g.Scope)
		sr.grants = append(sr.grants, g)
	}
	for _, m := range memberships {
		sr := recordsFor(m.Scope)
		sr.memberships = append(sr.memberships, m)
	}

	return global, every, byDomain
}

// intern assigns dense IDs to permissions and resources and bit positions to
// each resource's named fields, and compiles the distinct condition texts. It
// returns the grants the snapshot holds and the ones it skipped. IDs are
// uint16 by design; overflowing one would silently truncate and grant the
// wrong permissions, so it fails the load instead.
//
// A grant the running release cannot use is skipped, not fatal: one bad row
// in the store must not stop authorization for everyone, freeze a running
// instance on a stale snapshot, or keep a new instance from becoming ready. A
// skipped grant is denied, since a permission the snapshot does not hold is
// not held. Three things make a grant unusable: when the collection is known,
// a permission, resource or field the release does not declare; a condition
// text that does not parse, so a check never meets an unparseable condition;
// and a row-referencing condition (a binding-name attribute or new. reference)
// on a scope-wide grant, which has no row for it to see; only row-free
// conditions (environment and subject attributes) are valid there, folding at
// check time (design plan §05, revised 2026-08-31).
func (s *snapshot) intern(grants []policy.Grant, collection PermissionCollection) ([]policy.Grant, []*SkippedGrant, error) {
	exprs := make(map[string]condition.Expr)
	kept := make([]policy.Grant, 0, len(grants))
	var skipped []*SkippedGrant
	for _, g := range grants {
		if collection != nil {
			if err := unusableName(collection, &g); err != nil {
				skipped = append(skipped, newSkippedGrant(&g, err))

				continue
			}
		}
		if g.Condition != "" {
			expr, ok := exprs[g.Condition]
			if !ok {
				if len(exprs) >= math.MaxUint16 {
					return nil, nil, errors.Newf("too many distinct conditions to compile: limit %d", math.MaxUint16)
				}
				var err error
				expr, err = condition.Parse(g.Condition)
				if err != nil {
					skipped = append(skipped, newSkippedGrant(&g, errors.Wrap(err, "the condition does not parse")))

					continue
				}
				exprs[g.Condition] = expr
			}
			if g.Resource == "" && !condition.RowFree(expr) {
				skipped = append(skipped, newSkippedGrant(&g, errors.New("a row-referencing condition on a scope-wide grant: a grant attached to no resource has no row for it to see")))

				continue
			}
		}
		kept = append(kept, g)
		if _, ok := s.perms[g.Perm]; !ok {
			if len(s.perms) >= math.MaxUint16 {
				return nil, nil, errors.Newf("too many permissions to compile: limit %d", math.MaxUint16)
			}
			s.perms[g.Perm] = uint16(len(s.perms)) //nolint:gosec // bounded by the guard above
			s.permNames = append(s.permNames, g.Perm)
		}
		resID, ok := s.resources[g.Resource]
		if !ok {
			if len(s.resources) >= math.MaxUint16 {
				return nil, nil, errors.Newf("too many resources to compile: limit %d", math.MaxUint16)
			}
			resID = uint16(len(s.resources)) //nolint:gosec // bounded by the guard above
			s.resources[g.Resource] = resID
			s.resourceNames = append(s.resourceNames, g.Resource)
			s.fields = append(s.fields, make(map[string]uint16))
		}
		if g.Field != "" && g.Field != "*" {
			if _, ok := s.fields[resID][g.Field]; !ok {
				if len(s.fields[resID]) >= math.MaxUint16 {
					return nil, nil, errors.Newf("too many fields on resource %q to compile: limit %d", g.Resource, math.MaxUint16)
				}
				s.fields[resID][g.Field] = uint16(len(s.fields[resID])) //nolint:gosec // bounded by the guard above
			}
		}
	}

	// Condition IDs sort by text so term sets — and the texts a decision
	// carries — are canonically ordered regardless of record order.
	s.conditions = slices.Sorted(maps.Keys(exprs))
	s.compiled = make([]condition.Expr, len(s.conditions))
	for i, text := range s.conditions {
		s.condIDs[text] = uint16(i)
		s.compiled[i] = exprs[text]
	}

	return kept, skipped, nil
}

func (s *snapshot) compileScope(grants []policy.Grant, memberships []policy.Membership) *scopePolicy {
	roleOwn, userDirect := s.compileSubjectGrants(grants)
	inherits, userRoles := splitMemberships(memberships)

	// Fold inheritance: every role referenced anywhere gets its effective
	// grants (its own plus its transitive parents').
	roleSet := referencedRoles(roleOwn, inherits, userRoles)
	dp := &scopePolicy{
		roleGrants: make(map[accesstypes.Role]grantMap, len(roleSet)),
		userGrants: make(map[accesstypes.User]grantMap, len(userRoles)+len(userDirect)),
		userRoles:  userRoles,
		userDirect: userDirect,
	}
	for role := range roleSet {
		var sources []grantMap
		for _, r := range inheritanceChain(role, inherits) {
			if own := roleOwn[r]; own != nil {
				sources = append(sources, own)
			}
		}
		dp.roleGrants[role] = mergeGrants(sources)
	}

	users := make(map[accesstypes.User]bool, len(userRoles)+len(userDirect))
	for user := range userRoles {
		users[user] = true
	}
	for user := range userDirect {
		users[user] = true
	}
	dp.resolveUsers(users, func(role accesstypes.Role) grantMap { return dp.roleGrants[role] }, nil)

	return dp
}

// overlayScope compiles what one domain's own rows add on top of base, the
// every-domain policy: a role the rows touch — with grants here, inheriting
// here, or named by a membership here — resolves to base's effective grants
// plus the rows' own, and a user the rows touch, or who holds a role the rows
// changed, resolves again over the union of base's memberships and direct
// grants and the domain's. Subjects the rows leave alone are absent, so a
// lookup falls back to base.
func (s *snapshot) overlayScope(base *scopePolicy, grants []policy.Grant, memberships []policy.Membership) *scopePolicy {
	roleOwn, userDirect := s.compileSubjectGrants(grants)
	inherits, userRoles := splitMemberships(memberships)

	dp := &scopePolicy{
		roleGrants: make(map[accesstypes.Role]grantMap),
		userGrants: make(map[accesstypes.User]grantMap),
	}
	for role := range referencedRoles(roleOwn, inherits, userRoles) {
		chain := inheritanceChain(role, inherits)
		if len(chain) == 1 && roleOwn[role] == nil {
			// Nothing here changes the role: base's answer stands.
			continue
		}
		var sources []grantMap
		for _, r := range chain {
			if inherited := base.roleGrants[r]; len(inherited) > 0 {
				sources = append(sources, inherited)
			}
			if own := roleOwn[r]; own != nil {
				sources = append(sources, own)
			}
		}
		dp.roleGrants[role] = mergeGrants(sources)
	}

	users := make(map[accesstypes.User]bool, len(userRoles)+len(userDirect))
	for user := range userRoles {
		users[user] = true
	}
	for user := range userDirect {
		users[user] = true
	}
	for user, roles := range base.userRoles {
		for _, r := range roles {
			if _, changed := dp.roleGrants[r]; changed {
				users[user] = true

				break
			}
		}
	}
	dp.userRoles, dp.userDirect = userRoles, userDirect
	dp.resolveUsers(users, func(role accesstypes.Role) grantMap {
		if gm, ok := dp.roleGrants[role]; ok {
			return gm
		}

		return base.roleGrants[role]
	}, base)

	return dp
}

// resolveUsers folds each user's effective grants from the roles they hold
// and their direct grants — in the partition's own rows and, when the
// partition overlays a base, base's too — deduplicating by role set so users
// sharing a role combination share one merged map.
func (dp *scopePolicy) resolveUsers(users map[accesstypes.User]bool, roleGrants func(accesstypes.Role) grantMap, base *scopePolicy) {
	combos := make(map[string]grantMap)
	for user := range users {
		roles := slices.Clone(dp.userRoles[user])
		direct := []grantMap{dp.userDirect[user]}
		if base != nil {
			roles = append(roles, base.userRoles[user]...)
			direct = append(direct, base.userDirect[user])
		}
		slices.Sort(roles)
		roles = slices.Compact(roles)

		if directGrants := mergeGrants(direct); len(directGrants) > 0 {
			sources := make([]grantMap, 0, len(roles)+1)
			for _, r := range roles {
				sources = append(sources, roleGrants(r))
			}
			sources = append(sources, directGrants)
			dp.userGrants[user] = mergeGrants(sources)

			continue
		}

		key := joinRoles(roles)
		combined, ok := combos[key]
		if !ok {
			sources := make([]grantMap, 0, len(roles))
			for _, r := range roles {
				sources = append(sources, roleGrants(r))
			}
			combined = mergeGrants(sources)
			combos[key] = combined
		}
		dp.userGrants[user] = combined
	}
}

// referencedRoles is every role a partition's rows name: with grants of its
// own, on either side of an inheritance edge, or held by a user.
func referencedRoles(roleOwn map[accesstypes.Role]grantMap, inherits map[accesstypes.Role][]accesstypes.Role, userRoles map[accesstypes.User][]accesstypes.Role) map[accesstypes.Role]bool {
	roleSet := make(map[accesstypes.Role]bool)
	for role := range roleOwn {
		roleSet[role] = true
	}
	for member, parents := range inherits {
		roleSet[member] = true
		for _, p := range parents {
			roleSet[p] = true
		}
	}
	for _, roles := range userRoles {
		for _, r := range roles {
			roleSet[r] = true
		}
	}

	return roleSet
}

// compileSubjectGrants builds each subject's raw grant map from one scope's
// grant records.
func (s *snapshot) compileSubjectGrants(grants []policy.Grant) (roleOwn map[accesstypes.Role]grantMap, userDirect map[accesstypes.User]grantMap) {
	roleOwn = make(map[accesstypes.Role]grantMap)
	userDirect = make(map[accesstypes.User]grantMap)
	for _, g := range grants {
		var gm grantMap
		switch g.Subject.Kind {
		case policy.SubjectRole:
			role := accesstypes.Role(g.Subject.Name)
			if roleOwn[role] == nil {
				roleOwn[role] = make(grantMap)
			}
			gm = roleOwn[role]
		case policy.SubjectUser:
			user := accesstypes.User(g.Subject.Name)
			if userDirect[user] == nil {
				userDirect[user] = make(grantMap)
			}
			gm = userDirect[user]
		}

		resID := s.resources[g.Resource]
		key := packGrantKey(s.perms[g.Perm], resID)
		fs := gm[key]
		if fs == nil {
			fs = &fieldSet{bits: make([]uint64, (len(s.fields[resID])+63)/64)}
			gm[key] = fs
		}
		if g.Condition != "" {
			cond := fs.ensureCond()
			id := s.condIDs[g.Condition]
			switch g.Field {
			case "":
				cond.endpoint = cond.endpoint.with(id)
			case "*":
				cond.all = cond.all.with(id)
			default:
				if cond.fields == nil {
					cond.fields = make(map[uint16]condTerms)
				}
				i := s.fields[resID][g.Field]
				cond.fields[i] = cond.fields[i].with(id)
			}

			continue
		}
		switch g.Field {
		case "":
			fs.endpoint = true
		case "*":
			fs.all = true
		default:
			fs.setBit(s.fields[resID][g.Field])
		}
	}

	return roleOwn, userDirect
}

// splitMemberships separates one scope's membership records into user role
// assignments and role-to-role inheritance edges. Casbin resolves inheritance
// transitively at evaluation time; the compiler folds it at load time.
func splitMemberships(memberships []policy.Membership) (inherits map[accesstypes.Role][]accesstypes.Role, userRoles map[accesstypes.User][]accesstypes.Role) {
	inherits = make(map[accesstypes.Role][]accesstypes.Role)
	userRoles = make(map[accesstypes.User][]accesstypes.Role)
	for _, m := range memberships {
		switch m.Member.Kind {
		case policy.SubjectRole:
			member := accesstypes.Role(m.Member.Name)
			if !slices.Contains(inherits[member], m.Role) {
				inherits[member] = append(inherits[member], m.Role)
			}
		case policy.SubjectUser:
			user := accesstypes.User(m.Member.Name)
			if !slices.Contains(userRoles[user], m.Role) {
				userRoles[user] = append(userRoles[user], m.Role)
			}
		}
	}

	return inherits, userRoles
}

// inheritanceChain lists role and, transitively, every role it inherits
// (cycle-safe), role first: the roles whose own grants fold into its
// effective grants.
func inheritanceChain(role accesstypes.Role, inherits map[accesstypes.Role][]accesstypes.Role) []accesstypes.Role {
	var chain []accesstypes.Role
	visited := make(map[accesstypes.Role]bool)
	stack := []accesstypes.Role{role}
	for len(stack) > 0 {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[r] {
			continue
		}
		visited[r] = true
		chain = append(chain, r)
		stack = append(stack, inherits[r]...)
	}

	return chain
}

// mergeGrants ORs the sources into one grantMap. With zero or one source it
// aliases rather than copies; snapshots are immutable so sharing is safe.
func mergeGrants(sources []grantMap) grantMap {
	sources = slices.DeleteFunc(sources, func(g grantMap) bool { return len(g) == 0 })
	switch len(sources) {
	case 0:
		return grantMap{}
	case 1:
		return sources[0]
	}

	merged := make(grantMap)
	for _, src := range sources {
		for key, fs := range src {
			if existing := merged[key]; existing != nil {
				existing.orIn(fs)
			} else {
				merged[key] = fs.clone()
			}
		}
	}

	return merged
}

func joinRoles(roles []accesstypes.Role) string {
	var b strings.Builder
	for _, r := range roles {
		b.WriteString(string(r))
		b.WriteByte(0)
	}

	return b.String()
}

// splitResourceField splits a resource name on its last '.' into the base
// resource and field. Checked resources and stored grants split with the same
// rule, so field matching is exact.
func splitResourceField(obj string) (resource, field string) {
	i := strings.LastIndexByte(obj, '.')
	if i < 0 {
		return obj, ""
	}

	return obj[:i], obj[i+1:]
}
