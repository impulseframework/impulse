// Package policy defines the normalized policy records that flow between a
// policy store and the snapshot compiler: a store reader turns rows into
// records; everything after (interning, bitsets, caches) is one shared code
// path in the access package. The package is internal by design — the public
// access.Store interface references these types in its method signatures, so
// only packages inside this module can implement a store.
package policy

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// SubjectKind discriminates who a grant or membership row refers to.
type SubjectKind uint8

// The three subject kinds: a role, a user, or a group of people.
const (
	SubjectRole SubjectKind = iota
	SubjectUser
	SubjectGroup
)

// Subject is a grant target or membership holder. The typed stores only ever
// produce role grant subjects, and user and group members; the compiler
// additionally supports user-direct grants and role-to-role membership
// (inheritance), so the record model does too. A group is named by the id the
// store keeps it under.
type Subject struct {
	Kind SubjectKind
	Name string // bare (unprefixed) role or user name, or a group's id
}

// Grant is one normalized permission grant, held where its Scope says: the
// global partition, one tenant domain, or every tenant domain.
//   - Resource == "" (with Field == "") is a scope-wide grant: the permission
//     is held with no resource attachment. Real resource names are validated
//     non-empty at every write boundary, so "" is structurally unreachable
//     from data — it is the store-level encoding of absence, the same idiom
//     as Field == "".
//   - Field == "" is an endpoint grant on the resource itself.
//   - Field == "*" grants all fields by implication (compiles to an all-flag,
//     never materialized bits, so newly generated fields are covered).
//   - otherwise it grants the single named field.
//   - Condition == "" is an unconditional grant; otherwise it is the grant's
//     condition as opaque expression text. The condition is part of the
//     stored row's identity, so one (subject, permission, resource, field)
//     may carry several grants, one per condition. The records carry the
//     text verbatim — nothing below the snapshot compiler interprets it, and
//     the compiler only interns it.
type Grant struct {
	Scope     accesstypes.PolicyScope
	Subject   Subject
	Perm      accesstypes.Permission
	Resource  string // bare base resource name; "" = scope-wide
	Field     string
	Condition string // opaque expression text; "" = unconditional
}

// Membership is one normalized role membership, held where its Scope says.
// Member is usually a user. A group member is a role a group of people holds,
// which the compiler folds at load time into a membership of each person in
// the group; a role member expresses role inheritance, folded at load time
// too. The typed stores produce user and group members.
//
// Period is when the membership counts. The zero Period is no start and no
// end, which is every membership a store wrote without one and every
// inheritance edge.
type Membership struct {
	Scope  accesstypes.PolicyScope
	Member Subject
	Role   accesstypes.Role
	Period accesstypes.Period
}

// Assignment is one membership of a person's own as a listing of what the
// person holds returns it: the membership and the reason it was given for, ""
// where none was given. The reason is not policy: the snapshot compiler never
// reads it, so ReadPolicy leaves it out and Membership, which the compiler
// reads, does not carry it.
type Assignment struct {
	Membership

	Reason string
}

// Role is one custom role row: a role that exists where its Scope says
// because the store holds it, with or without grants. The release's default
// roles have no row.
type Role struct {
	Scope accesstypes.PolicyScope
	Name  accesstypes.Role
}

// RoleRow is one custom role row less its scope columns: the name the role
// is held under and the description its creator gave it — the shape a store
// returns for a scope's role listing, as RoleGrant is for a role's grants.
// The description is not policy: the snapshot compiler never reads it, so
// ReadPolicy leaves it out and Role, the row's identity, does not carry it.
type RoleRow struct {
	Name        accesstypes.Role
	Description string
}

// Group is one group of people: the id the store keeps it under and where it
// is kept, the global partition or one tenant domain. A group is never kept in
// every domain. Its name and description are not policy: the snapshot compiler
// never reads them, so the record does not carry them and GroupRow does.
type Group struct {
	ID    accesstypes.Group
	Scope accesstypes.PolicyScope
}

// GroupRow is one group as a store lists it: the record and the words people
// read it by.
type GroupRow struct {
	Group

	Name        string
	Description string
}

// GroupMember is one person's place in a group, with the period it counts in:
// the person holds the group's roles while their place and the group's role
// both count.
type GroupMember struct {
	Group  accesstypes.Group
	User   accesstypes.User
	Period accesstypes.Period
}

// GroupRole is one role a group holds, with the group it belongs to as a
// store's join returns it: the shape of the listing of what a person holds
// through the groups they are in.
type GroupRole struct {
	Group Group
	Scope accesstypes.PolicyScope
	Role  accesstypes.Role
}

// ErrStillAssigned is the refusal of a deletion that assignments stand in the
// way of: a role that people hold now or will, a group that has a member or a
// role now or will. A store wraps it, so the access package tells the refusal
// from a failure and says it to the caller in words. The listings above the
// store leave out an assignment whose period has not begun, so the store's own
// check is the one that sees it.
var ErrStillAssigned = errors.New("assignments that have not ended stand in the way")

// ErrHeldByGroup is the refusal of a role's deletion while a group holds the
// role, now or for a period that has not begun: a role deleted from under a
// group would leave the group's row behind, and a role of the name made again
// would at once be held by everyone in the group. A store wraps it, as it
// wraps ErrStillAssigned for a person's own membership, so the access package
// says which of the two stands in the way.
var ErrHeldByGroup = errors.New("a group holds the role, now or for a period that has not begun")

// ErrNoRole is the refusal of a write that needs a custom role's row where none
// is held: a store wraps it, so the access package answers that the role does
// not exist.
var ErrNoRole = errors.New("the roles table holds no row of the role")

// SameRoleName reports whether a and b are the same role name but for the case
// of their ASCII letters: an ASCII capital letter equals its lower-case letter,
// and every other byte equals only itself. It is the rule the library compares
// two addresses by, never Unicode's case folding, which takes some letters from
// outside ASCII for ASCII ones (the Kelvin sign for k, the long s for s), so a
// name written with one would find the role its plain name names.
func SameRoleName(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if lowerRoleNameByte(a[i]) != lowerRoleNameByte(b[i]) {
			return false
		}
	}

	return true
}

// FoldRoleName is name with its ASCII capital letters in lower case and every
// other byte as it was written: two names fold alike exactly when SameRoleName
// reports them the same.
func FoldRoleName(name string) string {
	b := []byte(name)
	for i, c := range b {
		b[i] = lowerRoleNameByte(c)
	}

	return string(b)
}

// lowerRoleNameByte is c in lower case when it is an ASCII capital letter, and
// c otherwise.
func lowerRoleNameByte(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + ('a' - 'A')
	}

	return c
}

// RoleNamesMeet reports whether a new role's name in scope is held to a role
// held in held: whether the two are read side by side, so that names which
// differ only in case could not be told apart. Two places on one axis meet when
// they are the global partition both, the same domain, or every domain and one
// domain or every domain. The global partition and a domain never meet, and
// neither do two different domains.
func RoleNamesMeet(scope, held accesstypes.PolicyScope) bool {
	if scope.Axis() != held.Axis() {
		return false
	}
	switch {
	case scope.IsGlobal():
		return held.IsGlobal()
	case scope.IsEveryDomain():
		return !held.IsGlobal()
	case held.IsEveryDomain():
		return true
	default:
		domain, _ := scope.Domain()
		heldDomain, oneDomain := held.Domain()

		return oneDomain && heldDomain == domain
	}
}

// CaseTwin returns the role among roles that a new role's name in scope
// differs from only in case: one held where the name meets it (RoleNamesMeet)
// whose name is not name but is the same by SameRoleName. The same exact name
// held in another place is no twin. With several it answers the first of one
// held in scope itself, then one held in every domain, then by domain in byte
// order, then by name in byte order; with none it answers false. Rows that do
// not meet scope are ignored, so a caller may hand it more rows than the places.
func CaseTwin(roles []Role, scope accesstypes.PolicyScope, name accesstypes.Role) (Role, bool) {
	var twin Role
	found := false
	for _, r := range roles {
		if r.Name == name || !RoleNamesMeet(scope, r.Scope) || !SameRoleName(string(r.Name), string(name)) {
			continue
		}
		if !found || compareTwins(scope, r, twin) < 0 {
			twin = r
			found = true
		}
	}

	return twin, found
}

// compareTwins orders two twins of a name in scope the way CaseTwin answers
// the first: held in scope itself, then held in every domain, then by domain,
// then by name.
func compareTwins(scope accesstypes.PolicyScope, a, b Role) int {
	place := func(r Role) int {
		switch {
		case r.Scope == scope:
			return 0
		case r.Scope.IsEveryDomain():
			return 1
		default:
			return 2
		}
	}
	aDomain, _ := a.Scope.Domain()
	bDomain, _ := b.Scope.Domain()

	return cmpChain(place(a)-place(b), strings.Compare(string(aDomain), string(bDomain)), strings.Compare(string(a.Name), string(b.Name)))
}

// CaseTwinError is a store's refusal of a role whose name differs only in case
// from a custom role held where the two are read side by side (CaseTwin). Held
// is that role. A store may wrap it, so the access package finds it with As and
// says the refusal to the caller in words.
type CaseTwinError struct {
	Held Role
}

// Error says which role the refused name differs from only in case, and where
// it is held.
func (e *CaseTwinError) Error() string {
	return fmt.Sprintf("the role %q held in scope %s differs from the name only in case", e.Held.Name, e.Held.Scope)
}

// Records is a reader's complete, store-agnostic output.
type Records struct {
	Grants       []Grant
	Memberships  []Membership
	Roles        []Role
	Groups       []Group
	GroupMembers []GroupMember
}

// RoleGrant is one stored grant row scoped to an already-known (scope, role):
// the shape a store returns for role-grant listings.
type RoleGrant struct {
	Perm      accesstypes.Permission
	Resource  string // bare base resource name
	Field     string // '' endpoint · '*' all fields · field name
	Condition string // opaque expression text; "" = unconditional
}

// Hash returns a canonical content hash of the records: independent of row
// order (stores return rows in arbitrary order) but sensitive to any change
// in effective policy, an assignment's period among them. The heartbeat
// compares it to decide whether a fresh read needs a recompile and snapshot
// swap.
func (r *Records) Hash() [sha256.Size]byte {
	grants := slices.Clone(r.Grants)
	slices.SortFunc(grants, compareGrants)
	memberships := slices.Clone(r.Memberships)
	slices.SortFunc(memberships, compareMemberships)
	roles := slices.Clone(r.Roles)
	slices.SortFunc(roles, compareRoles)
	groups := slices.Clone(r.Groups)
	slices.SortFunc(groups, compareGroups)
	members := slices.Clone(r.GroupMembers)
	slices.SortFunc(members, compareGroupMembers)

	h := sha256.New()
	for _, g := range grants {
		hashString(h, "g")
		hashScope(h, g.Scope)
		hashByte(h, byte(g.Subject.Kind))
		hashString(h, g.Subject.Name)
		hashString(h, string(g.Perm))
		hashString(h, g.Resource)
		hashString(h, g.Field)
		hashString(h, g.Condition)
	}
	for _, m := range memberships {
		hashString(h, "m")
		hashScope(h, m.Scope)
		hashByte(h, byte(m.Member.Kind))
		hashString(h, m.Member.Name)
		hashString(h, string(m.Role))
		hashPeriod(h, m.Period)
	}
	for _, role := range roles {
		hashString(h, "r")
		hashScope(h, role.Scope)
		hashString(h, string(role.Name))
	}
	for _, group := range groups {
		hashString(h, "G")
		hashString(h, string(group.ID))
		hashScope(h, group.Scope)
	}
	for _, member := range members {
		hashString(h, "M")
		hashString(h, string(member.Group))
		hashString(h, string(member.User))
		hashPeriod(h, member.Period)
	}

	return [sha256.Size]byte(h.Sum(nil))
}

func compareGrants(a, b Grant) int {
	return cmpChain(
		CompareScopes(a.Scope, b.Scope),
		int(a.Subject.Kind)-int(b.Subject.Kind),
		strings.Compare(a.Subject.Name, b.Subject.Name),
		strings.Compare(string(a.Perm), string(b.Perm)),
		strings.Compare(a.Resource, b.Resource),
		strings.Compare(a.Field, b.Field),
		strings.Compare(a.Condition, b.Condition),
	)
}

func compareMemberships(a, b Membership) int {
	return cmpChain(
		CompareScopes(a.Scope, b.Scope),
		int(a.Member.Kind)-int(b.Member.Kind),
		strings.Compare(a.Member.Name, b.Member.Name),
		strings.Compare(string(a.Role), string(b.Role)),
		comparePeriods(a.Period, b.Period),
	)
}

func compareRoles(a, b Role) int {
	return cmpChain(CompareScopes(a.Scope, b.Scope), strings.Compare(string(a.Name), string(b.Name)))
}

func compareGroups(a, b Group) int {
	return cmpChain(strings.Compare(string(a.ID), string(b.ID)), CompareScopes(a.Scope, b.Scope))
}

func compareGroupMembers(a, b GroupMember) int {
	return cmpChain(strings.Compare(string(a.Group), string(b.Group)), strings.Compare(string(a.User), string(b.User)), comparePeriods(a.Period, b.Period))
}

// comparePeriods orders two periods by start then end. A store holds one row
// for a membership's key, so the order only settles records built by hand.
func comparePeriods(a, b accesstypes.Period) int {
	return cmpChain(a.Start.Compare(b.Start), a.End.Compare(b.End))
}

// CompareScopes orders two places policy is held by their stored column
// triple, the order a store lists rows in.
func CompareScopes(a, b accesstypes.PolicyScope) int {
	ak, ax, ad := ScopeColumns(a)
	bk, bx, bd := ScopeColumns(b)

	return cmpChain(strings.Compare(ak, bk), strings.Compare(ax, bx), strings.Compare(ad, bd))
}

// The kinds a store persists in its Kind column: where a row is held. They
// are the column's only values, and the stores' DDL says so.
const (
	KindGlobal = "global"
	KindDomain = "domain"
	KindEvery  = "every"
)

// ScopeColumns decomposes a PolicyScope into the structural column triple the
// stores persist: (kind, axis, domain). The kind is one of KindGlobal,
// KindDomain and KindEvery; the axis is the name of the axis the domain
// belongs to, "" for the default axis; the domain is "" unless the row is
// held in one domain. ScopeFromColumns is its inverse.
func ScopeColumns(s accesstypes.PolicyScope) (kind, axis, domain string) {
	switch {
	case s.IsGlobal():
		return KindGlobal, s.Axis(), ""
	case s.IsEveryDomain():
		return KindEvery, s.Axis(), ""
	default:
		d, _ := s.Domain()

		return KindDomain, s.Axis(), string(d)
	}
}

// ScopeFromColumns reassembles a PolicyScope from its stored column triple.
// Only the default axis ("") can be reassembled: no constructor for a named
// axis exists yet, and a row under another axis must never fold into the
// default one — that would apply its grants to tenants they were not written
// for — so such a row is refused and the read fails closed. A kind the stores
// never write is refused the same way.
func ScopeFromColumns(kind, axis, domain string) (accesstypes.PolicyScope, error) {
	if axis != "" {
		return accesstypes.PolicyScope{}, errors.Newf("row belongs to axis %q, and no axis other than the default is declared", axis)
	}
	switch kind {
	case KindGlobal:
		return accesstypes.GlobalPolicyScope(), nil
	case KindEvery:
		return accesstypes.EveryDomainPolicyScope(), nil
	case KindDomain:
		return accesstypes.DomainPolicyScope(accesstypes.Domain(domain)), nil
	default:
		return accesstypes.PolicyScope{}, errors.Newf("row is held in kind %q, which is not global, domain or every", kind)
	}
}

// GroupColumns decomposes where a group is kept into the column triple the
// stores persist, as ScopeColumns does for a membership. A group is kept in the
// global partition or in one domain: every domain is refused, since a group
// kept in every tenant would be every tenant's to manage and nobody's.
func GroupColumns(s accesstypes.PolicyScope) (kind, axis, domain string, err error) {
	if s.IsEveryDomain() {
		return "", "", "", errors.New("a group is kept for the whole application or in one tenant, never in every tenant")
	}
	kind, axis, domain = ScopeColumns(s)

	return kind, axis, domain, nil
}

// GroupFromColumns reassembles where a group is kept from its stored column
// triple, refusing what ScopeFromColumns refuses and the every-domain kind,
// which no store writes for a group.
func GroupFromColumns(kind, axis, domain string) (accesstypes.PolicyScope, error) {
	if kind == KindEvery {
		return accesstypes.PolicyScope{}, errors.Newf("a group row is kept in kind %q, and a group is kept globally or in one domain", kind)
	}

	return ScopeFromColumns(kind, axis, domain)
}

// cmpChain returns the first non-zero comparison result.
func cmpChain(results ...int) int {
	for _, r := range results {
		if r != 0 {
			return r
		}
	}

	return 0
}

// hashString writes a length-prefixed string so no delimiter collision can
// make two different record sequences hash identically.
func hashString(h hash.Hash, s string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(s)))
	h.Write(length[:])
	h.Write([]byte(s))
}

func hashByte(h hash.Hash, b byte) {
	h.Write([]byte{b})
}

// hashPeriod writes a period's two instants, each as the nanoseconds since the
// epoch behind a byte that says whether it is there: no start and a start at
// the epoch hash apart.
func hashPeriod(h hash.Hash, p accesstypes.Period) {
	for _, instant := range []time.Time{p.Start, p.End} {
		var nanos [8]byte
		if instant.IsZero() {
			hashByte(h, 0)
		} else {
			hashByte(h, 1)
			binary.BigEndian.PutUint64(nanos[:], uint64(instant.UnixNano()))
		}
		h.Write(nanos[:])
	}
}

// hashScope writes a scope's structural triple (kind, axis, domain).
func hashScope(h hash.Hash, s accesstypes.PolicyScope) {
	kind, axis, domain := ScopeColumns(s)
	hashString(h, kind)
	hashString(h, axis)
	hashString(h, domain)
}
