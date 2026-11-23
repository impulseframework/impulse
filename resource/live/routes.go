package live

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// The limits the live routes hold a request to.
const (
	// maxBodyBytes bounds a live route's JSON body.
	maxBodyBytes = 1 << 20
	// MaxRenewSubscriptions is the most subscriptions one renewal carries; a tab holding
	// more has more live pages open than a browser shows.
	MaxRenewSubscriptions = 1000
)

// SubscriptionRequest is one subscription as the renew route carries it: a row by
// resource and key, or a list by resource and domain (empty or absent for a global
// resource). A row subscription of a domain-scoped resource names the domain the row
// was read in, so the server re-checks Read in that domain; without it the re-check
// runs in the global scope, where a domain-scoped resource holds no grants.
//
// A list across tenants is its resource with AcrossTenants set, and Domains when the
// page narrowed the list to tenants it named, spelled as the list's own domains
// parameter spells them, the tenants' keys separated by commas: the page's filter on
// the tenant travels with the renewal, and the server works the set out again from it.
// It names no key and no domain. Without Domains the list is of every tenant.
//
// A list a page subscribes to because it shows names read from it (NamesHeader) carries
// NameField, the resource's name field as the grants name it, when the name is not the
// resource's key: the server re-checks List on that field, the grant the page's read
// asked, since a person may hold the name's grant and not the list's. It names no key
// and is never a list across tenants. The record keeps it as the resource's list in the
// domain, as it keeps any list.
type SubscriptionRequest struct {
	Resource      string `json:"resource"`
	Key           string `json:"key,omitempty"`
	Domain        string `json:"domain,omitempty"`
	AcrossTenants bool   `json:"acrossTenants,omitempty"`
	Domains       string `json:"domains,omitempty"`
	NameField     string `json:"nameField,omitempty"`
}

// checked is the resource the renewal re-checks the subscription's grant on: the
// resource's name field for a list held for its names (NameField), the resource itself
// otherwise.
func (s *SubscriptionRequest) checked() accesstypes.Resource {
	res := accesstypes.Resource(s.Resource)
	if s.NameField == "" {
		return res
	}

	return res.ResourceWithTag(accesstypes.Tag(s.NameField))
}

// refuseDots refuses a subscription whose resource or name field holds a dot. A
// subscription names a resource by its own name and a name field is one field's name,
// and neither holds a dot, so the grants are never asked about such a name.
func (s *SubscriptionRequest) refuseDots() error {
	if strings.Contains(s.Resource, ".") {
		return httpio.NewBadRequestMessagef("the %s subscription's resource holds a dot: a subscription names a resource by its own name, which holds none, and a list held for its names names the field in nameField", s.Resource)
	}
	if strings.Contains(s.NameField, ".") {
		return httpio.NewBadRequestMessagef("the %s subscription's name field holds a dot: nameField is the name of one field of the resource, which holds none", s.Resource)
	}

	return nil
}

// narrowed is the page's filter on the tenant a renewal of a list across tenants
// carries: nil for every tenant.
func (s *SubscriptionRequest) narrowed() []accesstypes.Domain {
	if s.Domains == "" {
		return nil
	}
	names := strings.Split(s.Domains, ",")
	narrowed := make([]accesstypes.Domain, 0, len(names))
	for _, domain := range names {
		narrowed = append(narrowed, accesstypes.Domain(domain))
	}

	return narrowed
}

// RenewRequest is the renew route's body: the tab and its live subscriptions.
type RenewRequest struct {
	Tab           string                `json:"tab"`
	Subscriptions []SubscriptionRequest `json:"subscriptions"`
}

// RenewResponse is the renew route's answer: the subscriptions kept, the ones dropped,
// each echoed as sent, and when the kept ones expire. A subscription is dropped when the
// user's grants no longer cover it (an unknown resource among them), when it is a list
// across tenants of a resource not declared for it or of more tenants than one
// subscription holds (MaxSubscriptionTenants), and when the record did not take it.
type RenewResponse struct {
	Kept      []SubscriptionRequest `json:"kept"`
	Dropped   []SubscriptionRequest `json:"dropped"`
	ExpiresAt time.Time             `json:"expiresAt"`
}

// UnsubscribeRequest is the unsubscribe route's body: the tab leaving, or all for the
// logout path.
type UnsubscribeRequest struct {
	Tab string `json:"tab"`
	All bool   `json:"all"`
}

// RenewOption configures RenewHandler.
type RenewOption func(*renewOptions)

// renewOptions is what the options set.
type renewOptions struct {
	across AcrossTenantsDeclarations
}

// AcrossTenantsDeclarations answers whether a resource is declared for listing across
// tenants (@listAcrossTenants), as the generated collection does.
type AcrossTenantsDeclarations interface {
	ListsAcrossTenants(res accesstypes.Resource) bool
}

// WithListsAcrossTenants names the resources a subscription over a set of tenants may
// be renewed for: the ones declared for listing across tenants, which the generated
// collection answers (Collection()). Without it the handler knows of none, and keeps
// no such subscription.
func WithListsAcrossTenants(declared AcrossTenantsDeclarations) RenewOption {
	return func(o *renewOptions) {
		o.across = declared
	}
}

// declares reports whether the resource is declared for listing across tenants.
func (o *renewOptions) declares(res accesstypes.Resource) bool {
	return o.across != nil && o.across.ListsAcrossTenants(res)
}

// RenewHandler serves POST <prefix>/live/renew: the tab's subscriptions are each
// re-checked against the user's grants (Read for a row, List for a list, in the
// subscription's domain or the global scope; List on the name field for a list held for
// its names, SubscriptionRequest.NameField), the kept ones are written with a fresh
// expiry in one batch, and the answer says which were kept and which dropped. A list
// across tenants is re-checked by working its set out again (resource.TenantsListed):
// the tenants the session reaches now, narrowed by the filter the renewal carries, in
// which the person may list the resource now. It is kept with that set, so a tenant
// gained or lost since is in or out of it from this renewal on, and dropped when the
// set is empty or has grown past MaxSubscriptionTenants, the most tenants one
// subscription holds: the list is then a plain one, and the tab's other subscriptions
// are kept and extended as ever. It is kept only for a resource declared for listing
// across tenants
// (WithListsAcrossTenants): for any other resource it is dropped before its set is
// worked out, as the list's own request is refused before it is made
// (resource.QueryDecoder.DecodeAcrossTenants), so no record holds a subscription over
// a set of tenants for one. A
// subscription the grants no longer cover, or one naming a resource the grants do not
// know, is dropped, never refused. The application's permission accessor is the one
// every generated handler checks through. A subscription the record does not take is
// dropped too, with the record's reason logged, and stops none of the others (Refusal);
// a failure of the record as a whole fails the renewal, which the tab makes again. The
// subscriptions are written for the request's principal id (PrincipalID); an id over
// Firebase's uid limit is refused with 403, the reason logged, and nothing is written.
func RenewHandler(svc Service, userPermissions func(r *http.Request) resource.UserPermissions, opts ...RenewOption) http.HandlerFunc {
	var o renewOptions
	for _, opt := range opts {
		opt(&o)
	}

	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		var req RenewRequest
		if err := decodeBody(w, r, &req); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if !ValidTab(req.Tab) {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessage(malformedTabMessageFmt))
		}
		if len(req.Subscriptions) > MaxRenewSubscriptions {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessagef("a renewal carries at most %d subscriptions", MaxRenewSubscriptions))
		}
		for _, sub := range req.Subscriptions {
			if sub.Resource == "" {
				return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessage("a subscription names its resource"))
			}
			if err := sub.refuseDots(); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
			if sub.AcrossTenants && (sub.Key != "" || sub.Domain != "") {
				return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessagef("the %s subscription across tenants names a key or a domain: a list across tenants names neither, and narrows itself with domains", sub.Resource))
			}
			if !sub.AcrossTenants && sub.Domains != "" {
				return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessagef("the %s subscription carries domains and is not a list across tenants: one tenant's list names its tenant in domain", sub.Resource))
			}
			if sub.NameField != "" && sub.Key != "" {
				return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessagef("the %s subscription names a key and a name field: a page's names are read from the resource's list, which names no key", sub.Resource))
			}
			if sub.NameField != "" && sub.AcrossTenants {
				return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessagef("the %s subscription across tenants names a name field: a page's names are read from the list of one tenant, or of none, never from a list across tenants", sub.Resource))
			}
		}

		principal, err := PrincipalID(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		kept, dropped, sets, err := recheck(ctx, userPermissions(r), &o, req.Subscriptions)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		expiry := time.Now().Add(SubscriptionTTL)
		subs := make([]Subscription, 0, len(kept))
		for i, sub := range kept {
			renewed := Subscription{
				Principal: principal,
				Tab:       req.Tab,
				Resource:  accesstypes.Resource(sub.Resource),
				Key:       sub.Key,
				Domain:    accesstypes.Domain(sub.Domain),
				Expiry:    expiry,
			}
			if sub.AcrossTenants {
				renewed.Across = &TenantSet{Narrowed: sub.narrowed(), Domains: sets[i]}
			}
			subs = append(subs, renewed.Normalized())
		}
		if len(subs) > 0 {
			ctx, cancel := context.WithTimeout(ctx, RecordTimeout)
			defer cancel()
			refusals, err := svc.Renew(ctx, subs)
			if err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
			kept, dropped = withoutRefused(ctx, req.Tab, kept, dropped, subs, refusals)
		}

		return httpio.NewEncoder(w).Ok(RenewResponse{Kept: kept, Dropped: dropped, ExpiresAt: expiry})
	})
}

// withoutRefused moves the subscriptions the record did not take from the kept to the
// dropped, each logged with the record's reason: the record wrote the others, so the
// renewal answers for them as it would have without the refused one. written is what
// the record was handed, one for each kept subscription in the same order.
func withoutRefused(ctx context.Context, tab string, kept, dropped []SubscriptionRequest, written []Subscription, refusals []Refusal) (stillKept, nowDropped []SubscriptionRequest) {
	if len(refusals) == 0 {
		return kept, dropped
	}
	reasons := make(map[string]error, len(refusals))
	for i := range refusals {
		reasons[refusals[i].Subscription.ID()] = refusals[i].Err
	}
	stillKept = make([]SubscriptionRequest, 0, len(kept))
	for i, sub := range kept {
		reason, refused := reasons[written[i].ID()]
		if !refused {
			stillKept = append(stillKept, sub)

			continue
		}
		logger.FromCtx(ctx).Errorf("live: the record did not take the %s subscription of tab %s at its renewal; it is dropped and the tab's others are kept: %v", sub.Resource, tab, reason)
		dropped = append(dropped, sub)
	}

	return stillKept, dropped
}

// recheckKey groups the subscriptions one Check call answers: one permission in one
// scope.
type recheckKey struct {
	scope accesstypes.Scope
	perm  accesstypes.Permission
}

// recheck splits the subscriptions, in the order sent, into the ones the grants still
// cover and the ones they do not: one Check per permission and scope, each against the
// same environment. A list across tenants is covered while its resource is declared
// for it and its set, worked out again here, holds a tenant and no more than one
// subscription holds (MaxSubscriptionTenants); sets carries each kept subscription's
// set, nil for a row and for one tenant's list.
func recheck(ctx context.Context, perms resource.UserPermissions, o *renewOptions, subs []SubscriptionRequest) (kept, dropped []SubscriptionRequest, sets [][]accesstypes.Domain, err error) {
	kept = make([]SubscriptionRequest, 0, len(subs))
	dropped = make([]SubscriptionRequest, 0)
	sets = make([][]accesstypes.Domain, 0, len(subs))
	env := resource.RequestEnvironment()

	keys := make([]recheckKey, len(subs))
	groups := make(map[recheckKey][]accesstypes.Resource)
	across := make(map[int][]accesstypes.Domain)
	for i, sub := range subs {
		if sub.AcrossTenants {
			// A resource that is not declared for it holds no set: it is dropped below.
			if !o.declares(accesstypes.Resource(sub.Resource)) {
				continue
			}
			set, err := resource.TenantsListed(ctx, perms, accesstypes.Resource(sub.Resource), sub.narrowed())
			if err != nil {
				return nil, nil, nil, errors.Wrap(err, "resource.TenantsListed()")
			}
			// A set past the ceiling is held by no subscription: it is dropped below,
			// as a set of no tenant is.
			if len(set) > MaxSubscriptionTenants {
				continue
			}
			across[i] = set

			continue
		}
		key := recheckKey{scope: accesstypes.GlobalScope(), perm: accesstypes.List}
		if sub.Domain != "" {
			key.scope = accesstypes.DomainScope(accesstypes.Domain(sub.Domain))
		}
		if sub.Key != "" {
			key.perm = accesstypes.Read
		}
		keys[i] = key
		groups[key] = append(groups[key], sub.checked())
	}

	decided := make(map[recheckKey]accesstypes.Decisions, len(groups))
	for key, resources := range groups {
		decisions, err := perms.Check(ctx, env, key.scope, key.perm, resources...)
		if err != nil {
			return nil, nil, nil, errors.Wrap(err, "resource.UserPermissions.Check()")
		}
		decided[key] = decisions
	}

	for i, sub := range subs {
		if sub.AcrossTenants {
			if len(across[i]) == 0 {
				dropped = append(dropped, sub)

				continue
			}
			kept = append(kept, sub)
			sets = append(sets, across[i])

			continue
		}
		if decided[keys[i]][sub.checked()].IsDenied() {
			dropped = append(dropped, sub)

			continue
		}
		kept = append(kept, sub)
		sets = append(sets, nil)
	}

	return kept, dropped, sets, nil
}

// UnsubscribeHandler serves POST <prefix>/live/unsubscribe: the tab's subscriptions are
// deleted, or with all every subscription of the principal and the browser's identity is
// revoked, the logout path. Both key by the principal id (PrincipalID), so a logout
// through one auth leaves a person of the same name in another auth subscribed and
// signed in. The answer is 204 whether or not anything was there: the client sends it
// best effort as the page leaves. An id over Firebase's uid limit is refused with 403,
// as the token route refused it, so nothing was ever registered or minted for it.
//
// Once the request has arrived, what it asks for is finished whatever becomes of the
// connection: the deletes and the revocation run under the handler's own limit
// (RecordTimeout) and not under the request's context. A page sends this as it closes,
// and the browser, or a proxy in front of the application, may end the connection before
// the record has answered; a delete that ended with the connection would leave the
// subscriptions to their expiry and log a failure for a page that did nothing but
// close. The work is done inside the handler's call, and nothing is left running when
// it returns.
func UnsubscribeHandler(svc Service) http.HandlerFunc {
	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		var req UnsubscribeRequest
		if err := decodeBody(w, r, &req); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		principal, err := PrincipalID(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		// The request's values stay (its log, its trace, its session); its end does not
		// reach the record.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), RecordTimeout)
		defer cancel()
		switch {
		case req.All:
			if err := svc.UnsubscribeAll(ctx, principal); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
			if err := svc.Revoke(ctx, principal); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
		case ValidTab(req.Tab):
			if err := svc.Unsubscribe(ctx, principal, req.Tab); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
		default:
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessage(malformedTabMessageFmt))
		}
		w.WriteHeader(http.StatusNoContent)

		return nil
	})
}

// TokenHandler serves GET <prefix>/live/token: how the browser connects to the session
// principal's change set, as the principal id (PrincipalID) its uid carries. An id over
// Firebase's uid limit is refused with 403 and the reason logged, before anything is
// minted: the person uses the application while its live pages refuse.
func TokenHandler(svc Service) http.HandlerFunc {
	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		principal, err := PrincipalID(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		ctx, cancel := context.WithTimeout(ctx, RecordTimeout)
		defer cancel()
		payload, err := svc.Token(ctx, principal)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(payload)
	})
}

// decodeBody reads a live route's JSON body into v, bounded and strict: unknown fields
// and trailing content are refused.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return httpio.NewBadRequestMessagef("invalid request body: %v", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return httpio.NewBadRequestMessage("invalid request body: trailing content")
	}

	return nil
}
