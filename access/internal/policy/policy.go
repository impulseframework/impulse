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
	"hash"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// SubjectKind discriminates who a grant or membership row refers to.
type SubjectKind uint8

// The two subject kinds: a role or a user.
const (
	SubjectRole SubjectKind = iota
	SubjectUser
)

// Subject is a grant target or membership holder. The typed stores only ever
// produce role grant subjects and user members; the compiler additionally
// supports user-direct grants and role-to-role membership (inheritance), so
// the record model does too.
type Subject struct {
	Kind SubjectKind
	Name string // bare (unprefixed) role or user name
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
// Member is usually a user; a role member expresses role inheritance, which
// the compiler folds at load time. The typed stores only ever produce user
// members.
type Membership struct {
	Scope  accesstypes.PolicyScope
	Member Subject
	Role   accesstypes.Role
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

// Records is a reader's complete, store-agnostic output.
type Records struct {
	Grants      []Grant
	Memberships []Membership
	Roles       []Role
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
// in effective policy. The heartbeat compares it to decide whether a fresh
// read needs a recompile and snapshot swap.
func (r *Records) Hash() [sha256.Size]byte {
	grants := slices.Clone(r.Grants)
	slices.SortFunc(grants, compareGrants)
	memberships := slices.Clone(r.Memberships)
	slices.SortFunc(memberships, compareMemberships)
	roles := slices.Clone(r.Roles)
	slices.SortFunc(roles, compareRoles)

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
	}
	for _, role := range roles {
		hashString(h, "r")
		hashScope(h, role.Scope)
		hashString(h, string(role.Name))
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
	)
}

func compareRoles(a, b Role) int {
	return cmpChain(CompareScopes(a.Scope, b.Scope), strings.Compare(string(a.Name), string(b.Name)))
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

// hashScope writes a scope's structural triple (kind, axis, domain).
func hashScope(h hash.Hash, s accesstypes.PolicyScope) {
	kind, axis, domain := ScopeColumns(s)
	hashString(h, kind)
	hashString(h, axis)
	hashString(h, domain)
}
