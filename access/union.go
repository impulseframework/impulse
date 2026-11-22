package access

import (
	"fmt"
	"maps"
	"slices"
	"strings"

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
// resource per field), its scope, its immutability, its computed marking, its method
// target, its concealing keys, and the fields a create of it must carry. The attribute vocabulary a shared resource declares is not enumerable through
// the interface, so keeping it identical stays the declaring sites' business.
//
// An action one collection marks as no role's (the action of a site's generated
// create-your-tenant route) is no role's in the union: it is in no List, a role's grant
// that names it is refused, and a collection that registers the same name for roles is
// refused here.
//
// The subject namespace is one for the whole application: collections that say one set
// describes two auths' people are refused (refuseSetOfTwoAuths), collections that
// declare one set in two different ways are refused (refuseSetDefinedTwoWays), and so,
// where the collections together know several auths, is a set a collection lists
// without saying whose people it describes (refuseUnsaidSet).
//
// A collection handed over as nil (the nil interface, or a nil pointer of a collection's
// type, which a variable nothing was assigned to hands over) is refused with a sentence
// that says which one, before anything is asked of it.
func UnionCollection(collections ...PermissionCollection) (PermissionCollection, error) {
	if len(collections) == 0 {
		return nil, errors.New("UnionCollection: at least one collection is required")
	}
	for i, c := range collections {
		if IsNilCollection(c) {
			return nil, errors.Newf("UnionCollection: collection %d (in argument order) is nil: pass each site's permission collection, its generated router's Collection()", i+1)
		}
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
	// A permission a collection lists with no resource, one held for the whole of a
	// scope alone, is one the release has in every site.
	for _, c := range collections {
		for perm := range c.List() {
			if _, listed := u.merged[perm]; !listed {
				u.merged[perm] = nil
			}
		}
	}
	for perm := range u.merged {
		slices.Sort(u.merged[perm])
	}
	// An action one collection marks as no role's is one no collection registers for
	// roles: the mark takes the name out of what a role file may grant in every site.
	for i, c := range collections {
		marker, ok := c.(noRoleActions)
		if !ok {
			continue
		}
		for _, action := range marker.NoRoleActions() {
			if reg, registered := first[action]; registered {
				return nil, errors.Newf("UnionCollection: collection %d (in argument order) marks %q as an action no role is given and collection %d registers it for roles: a name is one or the other in every site, so rename one of the two", i+1, action, reg.collection+1)
			}
		}
	}
	if err := refuseSetOfTwoAuths(collections); err != nil {
		return nil, err
	}
	if err := refuseSetDefinedTwoWays(collections); err != nil {
		return nil, err
	}
	if err := refuseUnsaidSet(collections, u.Auths()); err != nil {
		return nil, err
	}

	return u, nil
}

// refuseUnsaidSet refuses, where the collections together know several auths, a
// subject set a collection lists without saying whose people it describes. A set that
// names no auth is any auth's to read, so a role of every auth could read it, and
// nothing holds the role to a set its holders' ids find nothing in. The set is refused
// in each collection that leaves it unsaid, though another labels it, and the first
// such collection in argument order is named, its sets in name order. Where the
// collections know one auth or none an unsaid set is taken: every role is that auth's,
// or is held to no auth's sets.
func refuseUnsaidSet(collections []PermissionCollection, auths []string) error {
	if len(auths) < 2 {
		return nil
	}
	for i, c := range collections {
		for _, name := range slices.Sorted(maps.Keys(c.SubjectSets(""))) {
			if c.SubjectSetAuth(name) != "" {
				continue
			}

			return errors.Newf("UnionCollection: collection %d (in argument order) declares subject set %q and does not say whose people it describes, and the collections together know several auths (%s). A role of any of them could read the set, so say whose people it holds: add auth: and that auth's name to the set's @subjectSet",
				i+1, name, strings.Join(auths, ", "))
		}
	}

	return nil
}

// refuseSetOfTwoAuths refuses collections that say one subject set describes two
// different auths' people. subject.<name> is one name to a role file, and a grant is
// held to the sets of its own auth by that name (SubjectSetAuth), so a name is one
// auth's in every site. A set one collection labels and another leaves unsaid is taken
// where the collections know one auth, the label standing for both (where they know
// several, refuseUnsaidSet refuses it). The sets asked about are the ones some collection lists
// (SubjectSets). The set of a person's groups that generation declares is not listed and
// is not compared here: it is named after its auth, so two auths' sets never share a
// name.
func refuseSetOfTwoAuths(collections []PermissionCollection) error {
	names := make(map[string]struct{})
	for _, c := range collections {
		for name := range c.SubjectSets("") {
			names[name] = struct{}{}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		first, firstAuth := 0, ""
		for i, c := range collections {
			auth := c.SubjectSetAuth(name)
			if auth == "" || auth == firstAuth {
				continue
			}
			if firstAuth != "" {
				return errors.Newf("UnionCollection: collections %d and %d (in argument order) both declare subject set %q and disagree on whose people it describes: auth %q vs auth %q. A role file reads the set by its name alone, so give one of the two sets another name, or name the same auth on both (auth: in the @subjectSet)", first+1, i+1, name, firstAuth, auth)
			}
			first, firstAuth = i, auth
		}
	}

	return nil
}

// refuseSetDefinedTwoWays refuses collections that declare one subject set in two
// different ways (SubjectSetDefinition). subject.<name> is one name to a role file, so
// a name holds the same people in every site. For each set some collection lists
// (SubjectSets), in name order, each collection that lists it is compared with the
// first that does, in argument order; the first two that differ are named, with the
// part at the first place their definitions differ. A set a collection names an auth
// for without listing it is not compared.
func refuseSetDefinedTwoWays(collections []PermissionCollection) error {
	listers := make(map[string][]int)
	for i, c := range collections {
		for name := range c.SubjectSets("") {
			listers[name] = append(listers[name], i)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(listers)) {
		first := listers[name][0]
		definition := collections[first].SubjectSetDefinition(name)
		for _, i := range listers[name][1:] {
			other := collections[i].SubjectSetDefinition(name)
			if slices.Equal(definition, other) {
				continue
			}
			at := 0
			for at < len(definition) && at < len(other) && definition[at] == other[at] {
				at++
			}

			return errors.Newf("UnionCollection: collections %d and %d (in argument order) both declare subject set %q and define it differently: %s vs %s. "+
				"A role file reads the set by its name alone, so it must hold the same people in every site: declare the set the same way in both, or give one of the two sets another name",
				first+1, i+1, name, definitionPart(definition, at), definitionPart(other, at))
		}
	}

	return nil
}

// definitionPart says the part of a subject set's definition at a place, the way a
// refusal of two definitions names it: the part itself, or, where the definition has
// none there, no definition when it is empty and nothing more when it stops short.
func definitionPart(definition []string, at int) string {
	switch {
	case at < len(definition):
		return definition[at]
	case len(definition) == 0:
		return "no definition"
	default:
		return "nothing more"
	}
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
	required    []accesstypes.Tag
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
			required:    c.RequiredOnCreate(scope, res),
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
	case !slices.Equal(r.required, other.required):
		return "fields a create must carry", fmt.Sprintf("%v vs %v", r.required, other.required), true
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

// SubjectSets merges the subject sets the collections declare for one auth's people:
// the subject namespace is application-wide, and a set two collections declare is
// declared the same way in both (refuseSetDefinedTwoWays), so it builds on what either
// says.
func (u *unionCollection) SubjectSets(auth string) map[string][]string {
	sets := make(map[string][]string)
	for _, c := range u.collections {
		for name, builds := range c.SubjectSets(auth) {
			merged := sets[name]
			for _, built := range builds {
				if !slices.Contains(merged, built) {
					merged = append(merged, built)
				}
			}
			sets[name] = merged
		}
	}

	return sets
}

// Auths lists the auths any collection's generation knew: an application's sites may
// each declare an auth of their own, and a role file of any of them is checked against
// the union.
func (u *unionCollection) Auths() []string {
	var auths []string
	for _, c := range u.collections {
		for _, auth := range c.Auths() {
			if !slices.Contains(auths, auth) {
				auths = append(auths, auth)
			}
		}
	}
	slices.Sort(auths)

	return auths
}

// SubjectSetAuth names the auth whose people a subject set describes, from the first
// collection (in argument order) that names one for it: the subject namespace is
// application-wide, and a collection may leave unsaid what another says. Collections
// that name two different auths for a set any of them lists are refused where the union
// is built (UnionCollection), so the first answer is every collection's.
func (u *unionCollection) SubjectSetAuth(name string) string {
	for _, c := range u.collections {
		if auth := c.SubjectSetAuth(name); auth != "" {
			return auth
		}
	}

	return ""
}

// SubjectSetDefinition says how a subject set is declared, from the first collection
// (in argument order) that answers for it: the subject namespace is application-wide,
// and collections that list one set and declare it in two different ways are refused
// where the union is built (UnionCollection), so the first answer is every listing
// collection's.
func (u *unionCollection) SubjectSetDefinition(name string) []string {
	for _, c := range u.collections {
		if definition := c.SubjectSetDefinition(name); definition != nil {
			return definition
		}
	}

	return nil
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

func (u *unionCollection) RequiredOnCreate(scope accesstypes.PermissionScope, res accesstypes.Resource) []accesstypes.Tag {
	return u.owner(res).RequiredOnCreate(scope, res)
}

// IsNoRoleAction reports whether any of the collections marks action as one no role
// is given: the mark of one site holds in the whole application, since a role is one
// set of powers across every site.
func (u *unionCollection) IsNoRoleAction(action accesstypes.Resource) bool {
	for _, c := range u.collections {
		if isNoRoleAction(c, action) {
			return true
		}
	}

	return false
}

// NoRoleActions lists the actions any of the collections marks as no role's, sorted.
func (u *unionCollection) NoRoleActions() []accesstypes.Resource {
	var actions []accesstypes.Resource
	for _, c := range u.collections {
		if marker, ok := c.(noRoleActions); ok {
			actions = append(actions, marker.NoRoleActions()...)
		}
	}
	slices.Sort(actions)

	return slices.Compact(actions)
}
