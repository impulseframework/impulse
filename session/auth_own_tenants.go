package session

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// noOwnTenantsMessage answers both own-tenants handlers on an Auth that holds no hook to
// answer them (SetOwnTenants).
const noOwnTenantsMessage = "This application does not serve a person's tenants."

// OwnTenant is one tenant a person holds something in, as the application's own-tenants
// hook lists it: the tenant's key, the roles the person holds there in words, and why they
// cannot leave it ("" where they can).
type OwnTenant struct {
	Domain  accesstypes.Domain
	Roles   []string
	Refusal string
}

// OwnTenantsHook answers the own-tenants handlers: List the tenants userID holds
// something in, and Leave one of them, everything userID holds in domain going. The
// account stays. A refusal is an httpio message, and the handler answers it as it is: who
// belongs where is the application's to say. The generated identity client's OwnTenants
// is one.
type OwnTenantsHook struct {
	List  func(ctx context.Context, userID impulse.UUID) ([]OwnTenant, error)
	Leave func(ctx context.Context, userID impulse.UUID, domain accesstypes.Domain) error
}

// ownTenant is one of a person's own tenants as their account page lists it.
type ownTenant struct {
	Domain accesstypes.Domain `json:"domain"`
	// Name is the tenant's name (SetTenantNames), its key where no name is known.
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
	// Refusal is why the person cannot leave the tenant, absent where they can.
	Refusal string `json:"refusal,omitempty"`
}

// leaveTenantRequest names the tenant a person leaves.
type leaveTenantRequest struct {
	Domain accesstypes.Domain `json:"domain"`
}

var leaveTenantDecoder = sessioncore.NewDecoder[leaveTenantRequest]()

// OwnTenants is the handler a signed-in person's account page lists their tenants with:
// {"tenants": [{"domain", "name", "roles", "refusal"}]}, the tenants the hook
// (SetOwnTenants) says the person holds something in, each by its name (SetTenantNames;
// the tenant's key where no name is known) with the roles they hold there in words and
// why they cannot leave it ("refusal", absent where they can). The tenants are sorted by
// name without regard to case, then by key; "tenants" and "roles" are never null. An
// Auth with no hook answers 404.
//
// The handler validates the session itself, as OwnSessions does, and refuses an
// impersonated session and a request a token authenticated.
func (a *Auth[S, U, Tx]) OwnTenants() http.HandlerFunc {
	type response struct {
		Tenants []ownTenant `json:"tenants"`
	}

	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.ownSessionRequest(ctx, "OwnTenants")
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if a.ownTenants == nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewNotFoundMessage(noOwnTenantsMessage))
		}
		held, err := a.ownTenants.List(ctx, sessioninfo.UserFromCtx(ctx).ID)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "session.OwnTenantsHook.List()"))
		}

		return httpio.NewEncoder(w).Ok(response{Tenants: a.ownTenantsOf(held)})
	}))
}

// LeaveTenant is the handler a signed-in person's account page leaves one of their
// tenants with: it accepts {"domain"}, the tenant's key, and answers 200 with no body.
// The hook (SetOwnTenants) holds the rules and does the leaving, and its refusal is
// answered as it is: the handler checks nothing of its own about the tenant. Leaving
// takes every role the person holds in the tenant. The account stays, and no session
// ends. An Auth with no hook answers 404. No mail is sent: it is the person's own act, at
// their own browser.
//
// The handler validates the session itself, as OwnSessions does, and refuses an
// impersonated session and a request a token authenticated.
func (a *Auth[S, U, Tx]) LeaveTenant() http.HandlerFunc {
	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.ownSessionRequest(ctx, "LeaveTenant")
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if a.ownTenants == nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewNotFoundMessage(noOwnTenantsMessage))
		}
		req, err := leaveTenantDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := a.ownTenants.Leave(ctx, sessioninfo.UserFromCtx(ctx).ID, req.Domain); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "session.OwnTenantsHook.Leave()"))
		}

		return httpio.NewEncoder(w).Ok(nil)
	}))
}

// ownTenantsOf names each tenant the hook listed and sorts them as the account page
// shows them: by name without regard to case, then by key. Never nil, and no tenant's
// roles are nil.
func (a *Auth[S, U, Tx]) ownTenantsOf(held []OwnTenant) []ownTenant {
	out := make([]ownTenant, 0, len(held))
	for _, t := range held {
		name := a.tenantName(string(t.Domain))
		if name == "" {
			name = string(t.Domain)
		}
		roles := t.Roles
		if roles == nil {
			roles = []string{}
		}
		out = append(out, ownTenant{Domain: t.Domain, Name: name, Roles: roles, Refusal: t.Refusal})
	}
	slices.SortFunc(out, func(x, y ownTenant) int {
		return cmp.Or(strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)), strings.Compare(string(x.Domain), string(y.Domain)))
	})

	return out
}
