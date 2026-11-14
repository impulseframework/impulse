package access

import (
	"fmt"
	"maps"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// UnionCollection presents several permission collections as one registry: the
// application-wide collection of a deployment whose sites each generate their own
// over one schema and share one policy store, so a role is one set of powers across
// every site. List merges the registries, the subject namespace is the union of the
// collections' declarations, and every other question about a resource is answered by
// the first collection (in argument order) that registers it; a resource no collection
// registers is answered by the first collection.
//
// The first collection's answer is exact only when every collection registering a
// resource declares it identically, the shape the resource generator produces when the
// sites' structs describe one table the same way. UnionCollection refuses collections
// that disagree on a shared resource in anything it can read through the interface: the
// permissions registered on it (the field set rides along, since List registers a field
// resource per field), its scope, its immutability, its computed marking, and its method
// target. The attribute vocabulary a shared resource declares is not enumerable through
// the interface, so keeping it identical stays the declaring sites' business.
func UnionCollection(collections ...PermissionCollection) (PermissionCollection, error) {
	if len(collections) == 0 {
		return nil, errors.New("UnionCollection: at least one collection is required")
	}

	u := &unionCollection{
		collections: collections,
		owners:      make(map[accesstypes.Resource]PermissionCollection),
		merged:      make(map[accesstypes.Permission][]accesstypes.Resource),
	}
	first := make(map[accesstypes.Resource]registration)
	for i, c := range collections {
		registrations := registrationsOf(i, c)
		for _, res := range slices.Sorted(maps.Keys(registrations)) {
			reg := registrations[res]
			prev, shared := first[res]
			if !shared {
				first[res] = reg
				u.owners[res] = c
			} else if property, answers, ok := prev.disagreement(&reg); ok {
				return nil, errors.Newf("UnionCollection: collections %d and %d (in argument order) both register resource %q and disagree on its %s: %s", prev.collection+1, i+1, res, property, answers)
			}
			for _, perm := range reg.permissions {
				if !slices.Contains(u.merged[perm], res) {
					u.merged[perm] = append(u.merged[perm], res)
				}
			}
		}
	}
	for perm := range u.merged {
		slices.Sort(u.merged[perm])
	}

	return u, nil
}

// registration is what one collection says about a resource it registers: the
// properties the union reads through the interface, and so can check for agreement.
type registration struct {
	collection  int
	permissions []accesstypes.Permission
	scope       accesstypes.PermissionScope
	immutable   bool
	computed    bool
	target      accesstypes.Resource
	targeted    bool
	order       []accesstypes.Tag
	keys        []accesstypes.Tag
}

// registrationsOf reads every resource c, the collection at index in argument order,
// registers: base and field resources alike.
func registrationsOf(index int, c PermissionCollection) map[accesstypes.Resource]registration {
	permissions := make(map[accesstypes.Resource][]accesstypes.Permission)
	for perm, resources := range c.List() {
		for _, res := range resources {
			permissions[res] = append(permissions[res], perm)
		}
	}

	registrations := make(map[accesstypes.Resource]registration, len(permissions))
	for res, perms := range permissions {
		slices.Sort(perms)
		scope := c.Scope(res)
		target, targeted := c.MethodTarget(scope, res)
		order, keys := c.ConcealingKeys(scope, res)
		registrations[res] = registration{
			collection:  index,
			permissions: slices.Compact(perms),
			scope:       scope,
			immutable:   c.IsResourceImmutable(scope, res),
			computed:    c.IsComputedResource(scope, res),
			target:      target,
			targeted:    targeted,
			order:       order,
			keys:        keys,
		}
	}

	return registrations
}

// disagreement names the first property on which r and other differ, with both
// answers in r-then-other order; ok is false when they agree on everything.
func (r *registration) disagreement(other *registration) (property, answers string, ok bool) {
	switch {
	case !slices.Equal(r.permissions, other.permissions):
		return "permissions", fmt.Sprintf("%v vs %v", r.permissions, other.permissions), true
	case r.scope != other.scope:
		return "scope", fmt.Sprintf("%q vs %q", r.scope, other.scope), true
	case r.immutable != other.immutable:
		return "immutability", fmt.Sprintf("%t vs %t", r.immutable, other.immutable), true
	case r.computed != other.computed:
		return "computed marking", fmt.Sprintf("%t vs %t", r.computed, other.computed), true
	case r.targeted != other.targeted || r.target != other.target:
		return "method target", fmt.Sprintf("%s vs %s", describeTarget(r), describeTarget(other)), true
	case !slices.Equal(r.order, other.order) || !slices.Equal(r.keys, other.keys):
		return "concealing keys", fmt.Sprintf("%v/%v vs %v/%v", r.order, r.keys, other.order, other.keys), true
	default:
		return "", "", false
	}
}

// describeTarget spells a registration's method target for a disagreement message.
func describeTarget(r *registration) string {
	if !r.targeted {
		return "none"
	}

	return fmt.Sprintf("%q", r.target)
}

// unionCollection is the PermissionCollection UnionCollection builds: the merged
// registry and, per registered resource, the collection that answers for it.
type unionCollection struct {
	collections []PermissionCollection
	owners      map[accesstypes.Resource]PermissionCollection
	merged      map[accesstypes.Permission][]accesstypes.Resource
}

// owner is the collection answering for res: the first registering it, else the first.
func (u *unionCollection) owner(res accesstypes.Resource) PermissionCollection {
	if c, ok := u.owners[res]; ok {
		return c
	}

	return u.collections[0]
}

// List returns the merged registry, every permission's resources sorted, as a copy the
// caller may keep.
func (u *unionCollection) List() map[accesstypes.Permission][]accesstypes.Resource {
	list := make(map[accesstypes.Permission][]accesstypes.Resource, len(u.merged))
	for perm, resources := range u.merged {
		list[perm] = slices.Clone(resources)
	}

	return list
}

func (u *unionCollection) Scope(res accesstypes.Resource) accesstypes.PermissionScope {
	return u.owner(res).Scope(res)
}

func (u *unionCollection) IsResourceImmutable(scope accesstypes.PermissionScope, res accesstypes.Resource) bool {
	return u.owner(res).IsResourceImmutable(scope, res)
}

func (u *unionCollection) AttributeComparisonType(scope accesstypes.PermissionScope, res accesstypes.Resource, name string) (accesstypes.AttributeType, bool) {
	return u.owner(res).AttributeComparisonType(scope, res, name)
}

func (u *unionCollection) AttributeIsColumn(scope accesstypes.PermissionScope, res accesstypes.Resource, name string) bool {
	return u.owner(res).AttributeIsColumn(scope, res, name)
}

func (u *unionCollection) AttributeIsList(scope accesstypes.PermissionScope, res accesstypes.Resource, name string) bool {
	return u.owner(res).AttributeIsList(scope, res, name)
}

// SubjectSetComparisonType resolves a subject set's comparison type from
// whichever collection declares it: the subject namespace is application-wide.
func (u *unionCollection) SubjectSetComparisonType(name string) (accesstypes.AttributeType, bool) {
	for _, c := range u.collections {
		if typ, ok := c.SubjectSetComparisonType(name); ok {
			return typ, true
		}
	}

	return "", false
}

// SubjectValueComparisonType resolves a subject value's comparison type from
// whichever collection declares it.
func (u *unionCollection) SubjectValueComparisonType(name string) (accesstypes.AttributeType, bool) {
	for _, c := range u.collections {
		if typ, ok := c.SubjectValueComparisonType(name); ok {
			return typ, true
		}
	}

	return "", false
}

func (u *unionCollection) IsComputedResource(scope accesstypes.PermissionScope, res accesstypes.Resource) bool {
	return u.owner(res).IsComputedResource(scope, res)
}

func (u *unionCollection) MethodTarget(scope accesstypes.PermissionScope, method accesstypes.Resource) (accesstypes.Resource, bool) {
	return u.owner(method).MethodTarget(scope, method)
}

func (u *unionCollection) ConcealingKeys(scope accesstypes.PermissionScope, res accesstypes.Resource) (order, keys []accesstypes.Tag) {
	return u.owner(res).ConcealingKeys(scope, res)
}
