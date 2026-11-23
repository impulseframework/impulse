package resource

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/tracer"
)

// DigestOption configures PermissionDigestHandler.
type DigestOption func(*digestOptions)

// digestOptions is what the options set.
type digestOptions struct {
	gates    FeatureGates
	features *FeatureSet
	former   FormerNames
	across   AcrossTenantsLists
}

// ListAcrossTenants is the permission the global permission digest answers for a
// resource declared for listing across tenants (@listAcrossTenants): granted while the
// person may list the resource in at least one tenant the session reaches. It is the
// digest's own entry and no grant's, so no role names it. A page that lists the resource
// across tenants is shown, and its route opened, to whoever the digest answers it for.
const ListAcrossTenants accesstypes.Permission = "ListAcrossTenants"

// AcrossTenantsLists answers the resources declared for listing across tenants
// (@listAcrossTenants), as the generated collection does.
type AcrossTenantsLists interface {
	ListedAcrossTenants() []accesstypes.Resource
}

// WithListsAcrossTenants adds, to the digest of the global scope, the answer a page
// that lists a resource across tenants is shown by: each resource declared for it
// carries ListAcrossTenants, granted, while the person may list it in at least one
// tenant the session reaches (ListedInSomeTenant), and is left as it was otherwise. The
// resources are the generated collection's (Collection()). A tenant's digest is
// untouched: it speaks for that tenant alone.
func WithListsAcrossTenants(lists AcrossTenantsLists) DigestOption {
	return func(o *digestOptions) {
		o.across = lists
	}
}

// FormerNames answers the former wire names of renamed fields and methods (their
// @formerly annotations) within a permission scope, as the generated collection does.
type FormerNames interface {
	FormerTagName(scope accesstypes.PermissionScope, res accesstypes.Resource, tag accesstypes.Tag) (accesstypes.Tag, bool)
	FormerName(scope accesstypes.PermissionScope, res accesstypes.Resource) (accesstypes.Resource, bool)
}

// WithFormerNames mirrors the entry of every renamed field or method under its former
// name too, with the same states: an application built before the rename asks the
// digest for the name it knows, and keeps its column or its action while the server
// still answers it. The names are the generated collection's (Collection()). A former
// name already present in the digest is left as the engine answered it.
func WithFormerNames(names FormerNames) DigestOption {
	return func(o *digestOptions) {
		o.former = names
	}
}

// WithFeatureGates filters the digest by the feature flags: an entry for a gated
// resource, one of its fields, or a gated RPC method is left out while the flag is off,
// so the frontend never renders a surface the routes would answer 404 for. The gates
// are the generated declaration (FeatureGates()), the features the application's
// FeatureSet.
func WithFeatureGates(gates FeatureGates, features *FeatureSet) DigestOption {
	return func(o *digestOptions) {
		o.gates = gates
		o.features = features
	}
}

// PermissionDigestHandler serves the per-scope permission digest: the
// session user's structural grant enumeration, resource → permission →
// granted|conditional|conditionalOnTime, with denied targets absent so
// consumers fail closed.
// The generated router registers it on the default outlet; applications wire
// nothing.
//
// The scope is the request's input, never payload structure: ?domain= names
// one tenant partition, its absence means the global scope. There is no
// domain validation — an unknown tenant simply holds no grants, so its
// digest is empty, which also keeps concealed tenancy unprobeable from this
// endpoint. The payload is advisory UI material (which surfaces to render);
// enforcement stays with the endpoint gate, the read rules, and the write
// stages. With WithFeatureGates the entries behind a flag that is off are left
// out too.
func PermissionDigestHandler(userPermissions func(r *http.Request) UserPermissions, opts ...DigestOption) http.HandlerFunc {
	var o digestOptions
	for _, opt := range opts {
		opt(&o)
	}

	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		scope := accesstypes.GlobalScope()
		if domain := r.URL.Query().Get("domain"); domain != "" {
			scope = accesstypes.DomainScope(accesstypes.Domain(domain))
		}

		perms := userPermissions(r)
		digest, err := perms.PermissionDigest(ctx, scope)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if scope.IsGlobal() {
			digest, err = o.withListsAcrossTenants(ctx, perms, digest)
			if err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
		}
		o.gates.filter(o.features, digest)
		o.mirrorFormerNames(scope, digest)

		return httpio.NewEncoder(w).Ok(digest)
	})
}

// withListsAcrossTenants returns the global digest with ListAcrossTenants on each
// resource declared for listing across tenants that the person may list in some tenant
// the session reaches. It runs before the feature gates, so a resource behind a flag
// that is off is left out with the rest of its entries.
func (o *digestOptions) withListsAcrossTenants(ctx context.Context, perms UserPermissions, digest accesstypes.PermissionDigest) (accesstypes.PermissionDigest, error) {
	if o.across == nil {
		return digest, nil
	}
	listed, err := ListedInSomeTenant(ctx, perms, o.across.ListedAcrossTenants())
	if err != nil {
		return nil, err
	}
	if len(listed) == 0 {
		return digest, nil
	}
	if digest == nil {
		digest = make(accesstypes.PermissionDigest, len(listed))
	}
	for _, res := range listed {
		// The entry is written on a copy: what the engine answered is its own.
		entry := maps.Clone(digest[res])
		if entry == nil {
			entry = make(map[accesstypes.Permission]accesstypes.DigestState, 1)
		}
		entry[ListAcrossTenants] = accesstypes.DigestGranted
		digest[res] = entry
	}

	return digest, nil
}

// mirrorFormerNames adds the former-name entries of the digest's renamed fields and
// methods, in key order so the result is the same for the same digest.
func (o *digestOptions) mirrorFormerNames(scope accesstypes.Scope, digest accesstypes.PermissionDigest) {
	if o.former == nil {
		return
	}
	permScope := accesstypes.DomainPermissionScope
	if scope.IsGlobal() {
		permScope = accesstypes.GlobalPermissionScope
	}
	for _, key := range slices.Sorted(maps.Keys(digest)) {
		former, ok := o.formerKey(permScope, key)
		if !ok {
			continue
		}
		if _, present := digest[former]; present {
			continue
		}
		digest[former] = maps.Clone(digest[key])
	}
}

// formerKey answers the former digest key of a field ("resource.tag") or a method
// ("resource") entry, and whether it has one.
func (o *digestOptions) formerKey(scope accesstypes.PermissionScope, key accesstypes.Resource) (accesstypes.Resource, bool) {
	res, tag, isField := strings.Cut(string(key), ".")
	if !isField {
		return o.former.FormerName(scope, key)
	}
	formerTag, ok := o.former.FormerTagName(scope, accesstypes.Resource(res), accesstypes.Tag(tag))
	if !ok {
		return "", false
	}

	return accesstypes.Resource(res).ResourceWithTag(formerTag), true
}
