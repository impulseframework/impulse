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
type SubscriptionRequest struct {
	Resource      string `json:"resource"`
	Key           string `json:"key,omitempty"`
	Domain        string `json:"domain,omitempty"`
	AcrossTenants bool   `json:"acrossTenants,omitempty"`
	Domains       string `json:"domains,omitempty"`
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

// RenewResponse is the renew route's answer: the subscriptions kept, the ones the user's
// grants no longer cover (an unknown resource among them), each echoed as sent, and when
// the kept ones expire.
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

// RenewHandler serves POST <prefix>/live/renew: the tab's subscriptions are each
// re-checked against the user's grants (Read for a row, List for a list, in the
// subscription's domain or the global scope), the kept ones are written with a fresh
// expiry in one batch, and the answer says which were kept and which dropped. A list
// across tenants is re-checked by working its set out again (resource.TenantsListed):
// the tenants the session reaches now, narrowed by the filter the renewal carries, in
// which the person may list the resource now. It is kept with that set, so a tenant
// gained or lost since is in or out of it from this renewal on, and dropped when the
// set is empty. A
// subscription the grants no longer cover, or one naming a resource the grants do not
// know, is dropped, never refused. The application's permission accessor is the one
// every generated handler checks through. The subscriptions are written for the
// request's principal id (PrincipalID); an id over Firebase's uid limit is refused with
// 403, the reason logged, and nothing is written.
func RenewHandler(svc Service, userPermissions func(r *http.Request) resource.UserPermissions) http.HandlerFunc {
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
			if sub.AcrossTenants && (sub.Key != "" || sub.Domain != "") {
				return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessagef("the %s subscription across tenants names a key or a domain: a list across tenants names neither, and narrows itself with domains", sub.Resource))
			}
			if !sub.AcrossTenants && sub.Domains != "" {
				return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessagef("the %s subscription carries domains and is not a list across tenants: one tenant's list names its tenant in domain", sub.Resource))
			}
		}

		principal, err := PrincipalID(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		kept, dropped, sets, err := recheck(ctx, userPermissions(r), req.Subscriptions)
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
			if err := svc.Renew(ctx, subs); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
		}

		return httpio.NewEncoder(w).Ok(RenewResponse{Kept: kept, Dropped: dropped, ExpiresAt: expiry})
	})
}

// recheckKey groups the subscriptions one Check call answers: one permission in one
// scope.
type recheckKey struct {
	scope accesstypes.Scope
	perm  accesstypes.Permission
}

// recheck splits the subscriptions, in the order sent, into the ones the grants still
// cover and the ones they do not: one Check per permission and scope, each against the
// same environment. A list across tenants is covered while its set, worked out again
// here, holds a tenant; sets carries each kept subscription's set, nil for a row and
// for one tenant's list.
func recheck(ctx context.Context, perms resource.UserPermissions, subs []SubscriptionRequest) (kept, dropped []SubscriptionRequest, sets [][]accesstypes.Domain, err error) {
	kept = make([]SubscriptionRequest, 0, len(subs))
	dropped = make([]SubscriptionRequest, 0)
	sets = make([][]accesstypes.Domain, 0, len(subs))
	env := resource.RequestEnvironment()

	keys := make([]recheckKey, len(subs))
	groups := make(map[recheckKey][]accesstypes.Resource)
	across := make(map[int][]accesstypes.Domain)
	for i, sub := range subs {
		if sub.AcrossTenants {
			set, err := resource.TenantsListed(ctx, perms, accesstypes.Resource(sub.Resource), sub.narrowed())
			if err != nil {
				return nil, nil, nil, errors.Wrap(err, "resource.TenantsListed()")
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
		groups[key] = append(groups[key], accesstypes.Resource(sub.Resource))
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
		if decided[keys[i]][accesstypes.Resource(sub.Resource)].IsDenied() {
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
		ctx, cancel := context.WithTimeout(ctx, RecordTimeout)
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
