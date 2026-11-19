package session

import (
	"context"
	"net/http"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/internal/basesession"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// ImpersonationRequest is the request an impersonated session starts from: who acts,
// as whom or as what, and under which mask. Only the declaration of the impersonation
// an application offers makes one (Impersonations.Request for a person signed in here,
// Impersonations.RequestFor for a server-side caller), once it has checked the kind's
// grant and computed the session's mask, and StartImpersonatedSession takes nothing
// else. The request has no field an application can set, so one built by hand does not
// compile; the zero value, which no declaration made, is refused when a session is
// started from it. What a request carries is read through its methods, none of which
// hands out anything that can be written to. A request is for the call that follows
// it: the grant was checked when it was made, so a program does not keep one for later.
type ImpersonationRequest struct {
	// kind names the declared kind the request was made for. It is empty on a value
	// no declaration made.
	kind string
	// core is the request as the session library establishes it.
	core sessioncore.ImpersonationRequest
}

// Kind returns the name of the declared kind of impersonation the request was made for.
func (r *ImpersonationRequest) Kind() string {
	return r.kind
}

// Actor returns the real person the session acts on behalf of, by name.
func (r *ImpersonationRequest) Actor() string {
	return r.core.Actor
}

// ActorRealm returns the application or identity provider that signed the actor in,
// empty for a person of this application.
func (r *ImpersonationRequest) ActorRealm() string {
	return r.core.ActorRealm
}

// ActorSessionID returns the actor's own session: for a person of this application,
// the session they act from, which the library verifies; for an actor another system
// signed in, their session there, when the call named one.
func (r *ImpersonationRequest) ActorSessionID() impulse.NullUUID {
	return r.core.SourceSessionID
}

// Principal returns whom or what the session acts as: a person or a role.
func (r *ImpersonationRequest) Principal() accesstypes.Principal {
	return r.core.Principal
}

// Mask returns the mask the session gets: the kind's widest, narrowed by what the
// person asked for.
func (r *ImpersonationRequest) Mask() accesstypes.PermissionMask {
	return r.core.Mask
}

// Reason returns the justification recorded on the impersonation record.
func (r *ImpersonationRequest) Reason() string {
	return r.core.Reason
}

// MaxDuration returns the cap the request puts on the session's lifetime, zero for the
// configured impersonation timeout.
func (r *ImpersonationRequest) MaxDuration() time.Duration {
	return r.core.MaxDuration
}

// established returns the request as the session library establishes it, or the
// refusal of a value no declaration made: the nil request and the zero one. That
// refusal is a mistake in the program, never a person's, so it is no client message.
func (r *ImpersonationRequest) established() (*sessioncore.ImpersonationRequest, error) {
	if r == nil || r.kind == "" {
		return nil, errors.New(
			"the impersonation request was made by no declaration, so nothing checked a grant or set a mask for it: " +
				"declare the kinds of impersonation the application offers (session.NewImpersonations) and take the request from its Request or RequestFor",
		)
	}
	core := r.core

	return &core, nil
}

// ImpersonationQuery narrows an ActiveImpersonations listing; see
// sessioninfo.ImpersonationQuery. The zero query lists every active impersonation.
type ImpersonationQuery = sessioninfo.ImpersonationQuery

// ImpersonationAuditHook receives every lifecycle event of an impersonated session.
// For the Started event a returned error fails the establishment — the session is
// destroyed and the caller sees the error — so an application that must persist
// evidence before the session lives can guarantee it. Errors from other events are
// logged.
type ImpersonationAuditHook = func(ctx context.Context, event sessioninfo.ImpersonationEvent) error

// WithImpersonationTimeout sets the hard cap on an impersonated session's lifetime.
// The cap is absolute: it is fixed when the session is established and idle-activity
// renewal never extends past it. The idle session timeout applies independently. The
// default is one hour.
func WithImpersonationTimeout(d time.Duration) BaseSessionOption {
	return func(b *basesession.BaseSession) {
		b.ImpersonationTimeout = d
	}
}

// WithImpersonationAudit registers the hook that receives impersonation lifecycle
// events (see ImpersonationAuditHook). Events are logged with the canonical evidence
// attributes whether or not a hook is registered.
func WithImpersonationAudit(hook ImpersonationAuditHook) BaseSessionOption {
	return func(b *basesession.BaseSession) {
		b.ImpersonationAudit = hook
	}
}

// StartImpersonatedSession establishes a session that operates as the request's
// principal on behalf of its actor; see AuthAPI.StartImpersonatedSession for the model.
// The request is one the application's declaration made (Impersonations.Request or
// RequestFor): a request built by hand does not compile, and the zero value is refused.
//
// Preauth has no user record, so a user principal is taken as given: the session's
// username is the principal's name and its user ID is zero, exactly as Login trusts
// the caller's username. Whether that user exists is the application's guard.
//
// Establishment is refused when the storage has no impersonation table and when the
// calling context is itself an impersonated session (no chaining). Optional customData
// (at most one *T) follows Login's semantics.
func (p *PreauthAPI[T]) StartImpersonatedSession(ctx context.Context, w http.ResponseWriter, req *ImpersonationRequest, customData ...*T) (impulse.UUID, error) {
	established, err := req.established()
	if err != nil {
		return impulse.NilUUID, err
	}

	return p.shared().StartImpersonatedSession(ctx, w, established, customData, sessioncore.IdentityAsGiven)
}

// DestroyImpersonatedSessions expires every live impersonated session established by
// actor and ends their records with reason Revoked — the offboarding and incident
// tool. It errors when the storage has no impersonation table.
func (p *PreauthAPI[T]) DestroyImpersonatedSessions(ctx context.Context, actor string) error {
	return p.shared().DestroyImpersonatedSessions(ctx, actor)
}

// ActiveImpersonations lists the impersonated sessions that are live right now, newest
// first — the admin surface's view of who is acting as whom. A session is active while
// its record has not ended, its hard cap has not passed, its session row is not
// expired, and it has seen activity within the idle session timeout. q narrows the
// listing by actor and/or principal; nil lists every active impersonation. It errors
// when the storage has no impersonation table.
func (p *PreauthAPI[T]) ActiveImpersonations(ctx context.Context, q *ImpersonationQuery) ([]*sessioninfo.Impersonation, error) {
	return p.shared().ActiveImpersonations(ctx, q)
}

// DestroyImpersonatedSession ends one live impersonated session — the operator's action
// on an ActiveImpersonations row: the session row is expired and the record ended with
// reason Revoked, in one transaction, so the next request on that session is refused as
// expired. A session that is not impersonated, or whose record has already ended, is
// left untouched. Who may revoke is the application's guard. It errors when the storage
// has no impersonation table.
func (p *PreauthAPI[T]) DestroyImpersonatedSession(ctx context.Context, sessionID impulse.UUID) error {
	return p.shared().DestroyImpersonatedSession(ctx, sessionID)
}

// EndImpersonation ends the impersonated session in ctx with reason Released and, for a
// local actor whose own session is still live, gives the response that session's
// cookies so the actor is back in it without logging in; restored reports whether that
// happened. See EndImpersonation on the handler type and the "Impersonated sessions"
// section of the README.
func (p *PreauthAPI[T]) EndImpersonation(ctx context.Context, w http.ResponseWriter) (restored bool, err error) {
	return p.shared().EndImpersonation(ctx, w)
}
