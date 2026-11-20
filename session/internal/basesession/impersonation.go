package basesession

import (
	"context"
	"net/http"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/internal/sealed"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// DefaultImpersonationTimeout is the hard cap on an impersonated session's lifetime
// when BaseSession.ImpersonationTimeout is zero.
var DefaultImpersonationTimeout = time.Hour

// ErrImpersonationNotConfigured is returned by every impersonation API when the storage
// has no impersonation record table.
var ErrImpersonationNotConfigured = errors.New("impersonation is not configured on the storage: attach sessionstorage.WithImpersonation")

// ImpersonationTTL returns the hard cap for a new impersonated session: the configured
// impersonation timeout (or the default), shortened by a positive maxDuration.
func (s *BaseSession) ImpersonationTTL(maxDuration time.Duration) time.Duration {
	ttl := s.ImpersonationTimeout
	if ttl <= 0 {
		ttl = DefaultImpersonationTimeout
	}
	if maxDuration > 0 && maxDuration < ttl {
		ttl = maxDuration
	}

	return ttl
}

// StartImpersonatedSession is the establishing step shared by every session type once
// the effective identity is resolved: it binds the actor to the validated session the
// call arrived on, if any, verifies a local actor's source session, writes the session
// row and its record atomically, delivers the Started event (a failing audit hook
// destroys the session and fails the call, so no unevidenced impersonated session ever
// lives), writes the auth and XSRF cookies, and stamps the evidence on the establishing
// request's log entry. req.Username is the session's effective identity; imp.SessionID
// is set on return.
//
// Only a base session the library built (New) starts one. Any other, which is one a
// program made by reflection, is answered sessionstorage.ErrImpersonationSealed before
// anything is read or written. The same error comes back from the write itself over a
// store that holds no write of the library's making: a mock of the store, or a store
// that embeds sessionstorage.NoImpersonatedSessions.
func (s *BaseSession) StartImpersonatedSession(ctx context.Context, w http.ResponseWriter, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	// Every session type reaches here from a request the application's declaration made,
	// on the base session the library built (New). A BaseSession nobody built is one a
	// program made by reflection, to hand this method a request and a record of its own:
	// it is refused before anything is read or written.
	if !s.built {
		return impulse.NilUUID, sessionstorage.ErrImpersonationSealed
	}

	if err := bindActorToSession(ctx, imp); err != nil {
		return impulse.NilUUID, err
	}
	if err := s.verifyLocalActor(ctx, imp); err != nil {
		return impulse.NilUUID, err
	}

	// The storage's write is sealed to the session library: it is a method no package
	// outside the library's directory can name, reached through the library's internal
	// package and nowhere else. The store the application handed the session type has
	// the write only from a store the library made (its own, or one it embeds); any
	// other store refuses here with nothing written.
	id, err := sealed.CreateImpersonatedSession(ctx, s.Storage, req, imp)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "sealed.CreateImpersonatedSession()")
	}
	imp.SessionID = id
	// The establishing call's own span carries the evidence on the source side.
	span.SetAttributes(impersonationSpanAttributes(imp)...)

	if err := s.EmitImpersonationEvent(ctx, sessioninfo.ImpersonationStarted, imp, ""); err != nil {
		if derr := s.Storage.DestroySession(ctx, id); derr != nil {
			logger.FromCtx(ctx).Error(errors.Wrap(derr, "sessionstorage.BaseStore.DestroySession()"))
		}

		return impulse.NilUUID, errors.Wrap(err, "basesession.BaseSession.EmitImpersonationEvent()")
	}

	// The establishing call is a same-site handoff, never an OAuth redirect, so the
	// cookie is SameSite=Strict for every session type.
	s.CookieHandler.NewAuthCookie(w, true, id)
	// write new XSRF Token Cookie to match the new SessionID
	s.CookieHandler.CreateXSRFTokenCookie(w, id)

	// Evidence on the establishing request's own log entry (the source side).
	l := logger.FromCtx(ctx).AddRequestAttribute("Username", req.Username).AddRequestAttribute(string(internalcookie.SessionID), id)
	for _, a := range imp.Attributes() {
		l = l.AddRequestAttribute(a.Key, a.Value)
	}

	return id, nil
}

// bindActorToSession ties an establishing call that arrives on a validated session of
// this application to that session. The request's own session is proof of who is at
// the keyboard, so the actor must be its user, and a local actor's SourceSessionID
// must be it (and defaults to it when omitted). A claim to a foreign realm is refused:
// an actor logged in here is local. Without a validated session in ctx (a server-side
// caller, which names the actor through session.Impersonations.RequestFor) the
// request's claims stand as given, for verifyLocalActor and the caller's own check of
// the kind's grant to hold.
//
// Without this, an actor who learned another user's session ID — it appears in logs,
// spans and ActiveImpersonations — could mint a session as that user by naming them.
func bindActorToSession(ctx context.Context, imp *sessioninfo.Impersonation) error {
	sessData, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	if !ok {
		return nil
	}

	if imp.ActorRealm != "" {
		return httpio.NewForbiddenMessage(
			"the request carries a session of this application, so the actor is that session's user: " +
				"an actor another system signed in is named only by a call that carries no session here",
		)
	}
	if imp.Actor != sessData.Username {
		return httpio.NewForbiddenMessagef("actor %q is not the user of the session making the request", imp.Actor)
	}
	if !imp.SourceSessionID.Valid {
		imp.SourceSessionID = impulse.NullUUID{UUID: sessData.ID, Valid: true}

		return nil
	}
	if imp.SourceSessionID.UUID != sessData.ID {
		return httpio.NewForbiddenMessage("the session named as the actor's own is not the session making the request")
	}

	return nil
}

// verifyLocalActor proves a local actor's claim. An actor with no ActorRealm is an
// user of this application, so the request must name the actor's own session in
// this store as SourceSessionID, and that session must be live, not itself impersonated,
// and carry the actor's username. The record then takes the actor's account from that
// session (Impersonation.ActorID), which is what the permission store names the actor
// by; a session type whose sessions belong to no account leaves it null. A foreign
// actor (ActorRealm set) was authenticated elsewhere and has nothing here to check.
//
// The actor's session is read through the library's own store (sealed.Session), never
// through the exported Session of a store that wraps it: who is recorded as the actor is
// what the library's own row says. And when the call arrived on a validated session,
// which bindActorToSession has held to be that same session, the row must name the
// account that session names as well as its username: a session that reached the request
// as another account's than the store holds it under starts nothing.
func (s *BaseSession) verifyLocalActor(ctx context.Context, imp *sessioninfo.Impersonation) error {
	if !imp.IsLocalActor() {
		return nil
	}
	if !imp.SourceSessionID.Valid {
		return httpio.NewBadRequestMessage("a person of this application acts from their own session, and the call names none: name it, or, for an actor another system signed in, name that system")
	}

	src, err := sealed.Session(ctx, s.Storage, imp.SourceSessionID.UUID)
	if err != nil {
		if errors.Is(err, sealed.ErrSealed) {
			return errors.Wrap(err, "sealed.Session()")
		}

		return httpio.NewForbiddenMessageWithError(err, "the actor's source session is not a session of this application")
	}
	if !s.live(src) || src.Impersonation != nil || src.Username != imp.Actor {
		return httpio.NewForbiddenMessagef("session %s is not a live session of %q in this application", src.ID, imp.Actor)
	}
	if current, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData); ok && current != nil && current.UserID != src.UserID {
		return httpio.NewForbiddenMessagef(
			"session %s reached this request as the session of another account than the one the session store holds it under, so it starts no session that acts as someone else",
			src.ID,
		)
	}
	imp.ActorID = src.UserID

	return nil
}

// live reports whether a session row is neither destroyed nor past the idle timeout.
func (s *BaseSession) live(sess *sessioninfo.SessionData) bool {
	return !sess.Expired && time.Since(sess.UpdatedAt) <= s.SessionTimeout
}

// EndImpersonationResponse is the body of the EndImpersonation handler.
type EndImpersonationResponse struct {
	// Restored reports that the browser's cookies name the actor's own session again;
	// false means the actor must log in.
	Restored bool `json:"restored"`
}

// EndImpersonation ends the impersonated session and, when it can, returns the actor to
// their own session; see EndImpersonationAPI. The body says which happened.
func (s *BaseSession) EndImpersonation() http.HandlerFunc {
	return s.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		restored, err := s.EndImpersonationAPI(ctx, w)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(EndImpersonationResponse{Restored: restored})
	})
}

// EndImpersonationAPI ends the impersonated session in ctx: the session row is expired
// and the record ends with reason Released, in one storage transaction, and the end is
// announced as an Ended event. For a local actor whose source session is still live,
// not itself impersonated, and still theirs, the response is given that session's
// cookies, so the actor is back in their own session without logging in; restored
// reports whether that happened. A context whose session is not impersonated is
// refused.
func (s *BaseSession) EndImpersonationAPI(ctx context.Context, w http.ResponseWriter) (restored bool, err error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	sessData, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	if !ok || sessData.Impersonation == nil {
		return false, httpio.NewBadRequestMessage("the session is not impersonated")
	}
	imp := sessData.Impersonation

	// One transaction: a session must never be left with an ended record and a live
	// row, which would validate while ActiveImpersonations no longer lists it.
	if err := s.Storage.DestroyImpersonatedSession(ctx, sessData.ID, sessioninfo.ImpersonationEndedByRelease); err != nil {
		return false, errors.Wrap(err, "sessionstorage.BaseStore.DestroyImpersonatedSession()")
	}

	now := time.Now()
	imp.EndedAt = &now
	imp.EndReason = sessioninfo.ImpersonationEndedByRelease
	if err := s.EmitImpersonationEvent(ctx, sessioninfo.ImpersonationEnded, imp, ""); err != nil {
		logger.FromCtx(ctx).Error(err)
	}

	src, ok := s.restorableSource(ctx, imp)
	if !ok {
		return false, nil
	}

	s.CookieHandler.NewAuthCookie(w, true, src.ID)
	s.CookieHandler.CreateXSRFTokenCookie(w, src.ID)
	logger.FromCtx(ctx).AddRequestAttribute("RestoredSessionId", src.ID)

	return true, nil
}

// restorableSource loads a local actor's own session and reports whether they can be
// returned to it: it is live, not itself impersonated, and still the actor's, by the
// account the record names or, for a session type whose sessions belong to no account,
// by the username. A foreign actor's session lives in another application.
func (s *BaseSession) restorableSource(ctx context.Context, imp *sessioninfo.Impersonation) (*sessioninfo.SessionData, bool) {
	if !imp.IsLocalActor() || !imp.SourceSessionID.Valid {
		return nil, false
	}

	// The library's own read (sealed.Session): the session the actor is returned to is
	// the one the library's own store holds, whatever a store that wraps it answers.
	src, err := sealed.Session(ctx, s.Storage, imp.SourceSessionID.UUID)
	if err != nil {
		if !httpio.HasNotFound(err) {
			logger.FromCtx(ctx).Error(errors.Wrap(err, "sealed.Session()"))
		}

		return nil, false
	}
	if !s.live(src) || src.Impersonation != nil {
		return nil, false
	}
	if imp.ActorID.Valid || src.UserID.Valid {
		if imp.ActorID != src.UserID {
			return nil, false
		}
	} else if src.Username != imp.Actor {
		return nil, false
	}

	return src, true
}

// DestroyImpersonatedSessions expires every live impersonated session established by
// actor and ends their records with reason Revoked. It errors when the storage has no
// impersonation table.
func (s *BaseSession) DestroyImpersonatedSessions(ctx context.Context, actor string) error {
	if !s.Storage.ImpersonationEnabled() {
		return ErrImpersonationNotConfigured
	}

	if err := s.Storage.DestroyImpersonatedSessions(ctx, actor); err != nil {
		return errors.Wrap(err, "sessionstorage.BaseStore.DestroyImpersonatedSessions()")
	}

	return nil
}

// ImpersonationResponse is the impersonation portion of the Authenticated response,
// present only for an impersonated session so a frontend can banner it and render
// read-only affordances.
type ImpersonationResponse struct {
	Actor         string `json:"actor"`
	ActorRealm    string `json:"actorRealm,omitempty"`
	PrincipalKind string `json:"principalKind"`
	Principal     string `json:"principal"`
	// Mask lists the permission kinds the session keeps. It is omitted for a session
	// that keeps every kind, and also for one that keeps none: MaskWords tells the two
	// apart ("nothing" for the second), and a banner shows that.
	Mask []string `json:"mask,omitempty"`
	// RefusedClasses are the classes of action the mask refuses inside the kinds it
	// keeps, and Refused the permissions it refuses on a named action or record;
	// each omitted when the mask refuses none.
	RefusedClasses []string             `json:"refusedClasses,omitempty"`
	Refused        []ImpersonationNamed `json:"refused,omitempty"`
	// MaskWords says the whole mask as a person would (accesstypes.PermissionMask.Words):
	// "everything", "reading only", "everything except credential actions". A banner
	// shows it as it is.
	MaskWords string    `json:"maskWords"`
	Reason    string    `json:"reason,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// ImpersonationNamed is one permission a mask refuses on one named action or record.
type ImpersonationNamed struct {
	Permission string `json:"permission"`
	Resource   string `json:"resource"`
}

// NewImpersonationResponse renders the record for the Authenticated response; nil in,
// nil out. Mask is omitted for a session that keeps every kind and for one that keeps
// none, which MaskWords tells apart, and the refusals for a mask that refuses nothing
// inside the kinds it keeps.
func NewImpersonationResponse(imp *sessioninfo.Impersonation) *ImpersonationResponse {
	if imp == nil {
		return nil
	}

	res := &ImpersonationResponse{
		Actor:         imp.Actor,
		ActorRealm:    imp.ActorRealm,
		PrincipalKind: imp.PrincipalKind(),
		Principal:     imp.PrincipalName(),
		MaskWords:     imp.Mask.Words(),
		Reason:        imp.Reason,
		ExpiresAt:     imp.ExpiresAt,
	}
	if perms := imp.Mask.Permissions(); perms != nil {
		res.Mask = make([]string, len(perms))
		for i, perm := range perms {
			res.Mask[i] = string(perm)
		}
	}
	for _, refusal := range imp.Mask.Refusals() {
		if class, ok := refusal.Class(); ok {
			res.RefusedClasses = append(res.RefusedClasses, string(class))

			continue
		}
		perm, resource, _ := refusal.Named()
		res.Refused = append(res.Refused, ImpersonationNamed{Permission: string(perm), Resource: string(resource)})
	}

	return res
}

// EmitImpersonationEvent logs a lifecycle event of an impersonated session with the
// record's evidence attributes, records it as an event on the current trace span, and
// delivers it to the audit hook when one is configured. It returns the hook's error, if
// any; the log line and span event are written regardless.
func (s *BaseSession) EmitImpersonationEvent(ctx context.Context, kind sessioninfo.ImpersonationEventKind, imp *sessioninfo.Impersonation, operation string) error {
	addImpersonationSpanEvent(ctx, kind, imp, operation)

	attrs := logger.FromCtx(ctx).WithAttributes()
	for _, a := range imp.Attributes() {
		attrs = attrs.AddAttribute(a.Key, a.Value)
	}
	attrs = attrs.AddAttribute("impersonation.event", string(kind))
	if operation != "" {
		attrs = attrs.AddAttribute("impersonation.operation", operation)
	}
	if imp.EndReason != "" {
		attrs = attrs.AddAttribute("impersonation.end_reason", string(imp.EndReason))
	}

	switch kind {
	case sessioninfo.ImpersonationIdentityOperationBlocked, sessioninfo.ImpersonationWriteBlocked:
		attrs.Logger().Warnf("impersonation: %s refused for %s", operation, imp.Principal)
	case sessioninfo.ImpersonationStarted, sessioninfo.ImpersonationEnded:
		attrs.Logger().Infof("impersonation %s: %s as %s", kind, imp.Actor, imp.Principal)
	default:
		attrs.Logger().Infof("impersonation %s", kind)
	}

	if s.ImpersonationAudit == nil {
		return nil
	}

	event := sessioninfo.ImpersonationEvent{Kind: kind, Impersonation: imp, Operation: operation, At: time.Now()}
	if err := s.ImpersonationAudit(ctx, event); err != nil {
		return errors.Wrapf(err, "impersonation audit hook (%s)", kind)
	}

	return nil
}

// DestroyImpersonatedSession ends one live impersonated session: the session row is
// expired and the record ended with reason Revoked, in one transaction — the
// single-session form of DestroyImpersonatedSessions. The next request on that session
// is refused as expired. A session that is not impersonated, or whose record has already
// ended, is left untouched. It errors when the storage has no impersonation table.
func (s *BaseSession) DestroyImpersonatedSession(ctx context.Context, sessionID impulse.UUID) error {
	if !s.Storage.ImpersonationEnabled() {
		return ErrImpersonationNotConfigured
	}

	if err := s.Storage.DestroyImpersonatedSession(ctx, sessionID, sessioninfo.ImpersonationEndedByRevocation); err != nil {
		return errors.Wrap(err, "sessionstorage.BaseStore.DestroyImpersonatedSession()")
	}

	return nil
}

// ActiveImpersonations lists the impersonated sessions that are live right now,
// newest first: their record has not ended, their hard cap has not passed, their
// session row is not expired, and the session has seen activity within the idle
// session timeout. q narrows the listing; nil lists every active impersonation. It
// errors when the storage has no impersonation table.
func (s *BaseSession) ActiveImpersonations(ctx context.Context, q *sessioninfo.ImpersonationQuery) ([]*sessioninfo.Impersonation, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if !s.Storage.ImpersonationEnabled() {
		return nil, ErrImpersonationNotConfigured
	}
	if q == nil {
		q = &sessioninfo.ImpersonationQuery{}
	}

	imps, err := s.Storage.ActiveImpersonations(ctx, time.Now().Add(-s.SessionTimeout), q)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.BaseStore.ActiveImpersonations()")
	}

	return imps, nil
}

// EnforceReadOnlyMask refuses non-safe requests (anything but GET, HEAD, OPTIONS and
// TRACE) from an impersonated session whose mask is read-only — a mask that names the
// kinds it keeps and keeps nothing beyond List and Read
// (accesstypes.PermissionMask.ReadOnly) — with 403 Forbidden, evidenced as a
// WriteBlocked event. Every other request, including every request of a session that
// is not impersonated or not masked, passes through: a mask that keeps every kind and
// refuses a class or a named action inside them is not read-only, and its refusals are
// the permission check's. It runs after ValidateSession; without a validated session in
// the context it passes the request through.
//
// The middleware is a coarse, opt-in backstop: it stops a read-only session from
// reaching any mutating handler regardless of whether that handler consults the mask.
// It is not a substitute for honoring the mask in permission checks (Execute reaches
// handlers by POST, and a session masked to Execute is not read-only).
func (s *BaseSession) EnforceReadOnlyMask(next http.Handler) http.Handler {
	return s.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		if !internalcookie.SafeMethods.Contain(r.Method) {
			if sessData, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData); ok && sessData.Impersonation != nil && sessData.Impersonation.Mask.ReadOnly() {
				operation := r.Method + " " + r.URL.Path
				if err := s.EmitImpersonationEvent(ctx, sessioninfo.ImpersonationWriteBlocked, sessData.Impersonation, operation); err != nil {
					logger.FromCtx(ctx).Error(err)
				}

				return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewForbiddenMessage("this session is read-only"))
			}
		}

		next.ServeHTTP(w, r)

		return nil
	})
}

// endImpersonation records an observed end of an impersonated session — an expiry
// noticed at validation — on its record and announces it. Failures are logged, never
// returned: the session is already being refused, and the record is evidence rather
// than a gate.
func (s *BaseSession) endImpersonation(ctx context.Context, sessData *sessioninfo.SessionData, reason sessioninfo.ImpersonationEndReason) {
	imp := sessData.Impersonation
	if imp == nil || imp.EndedAt != nil {
		return
	}

	if err := s.Storage.EndImpersonation(ctx, imp.SessionID, reason); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.BaseStore.EndImpersonation()"))

		return
	}

	now := time.Now()
	imp.EndedAt = &now
	imp.EndReason = reason
	if err := s.EmitImpersonationEvent(ctx, sessioninfo.ImpersonationEnded, imp, ""); err != nil {
		logger.FromCtx(ctx).Error(err)
	}
}

// LogoutAPI destroys the session in ctx. For an impersonated session that this
// request validated, the record's end (written by DestroySession as Logout) is
// announced as an Ended event.
func (s *BaseSession) LogoutAPI(ctx context.Context) error {
	if err := s.Storage.DestroySession(ctx, sessioninfo.IDFromCtx(ctx)); err != nil {
		return errors.Wrap(err, "sessionstorage.BaseStore.DestroySession()")
	}

	sessData, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	if !ok || sessData.Impersonation == nil || sessData.Impersonation.EndedAt != nil {
		return nil
	}

	now := time.Now()
	sessData.Impersonation.EndedAt = &now
	sessData.Impersonation.EndReason = sessioninfo.ImpersonationEndedByLogout
	if err := s.EmitImpersonationEvent(ctx, sessioninfo.ImpersonationEnded, sessData.Impersonation, ""); err != nil {
		logger.FromCtx(ctx).Error(err)
	}

	return nil
}

// impersonationExpired reports whether an impersonated session's hard cap has passed.
func impersonationExpired(sessData *sessioninfo.SessionData) bool {
	return sessData.Impersonation != nil && !time.Now().Before(sessData.Impersonation.ExpiresAt)
}

// impersonationEnded reports whether an impersonated session's record has ended. The
// record is the authority: a session whose record has ended is over even if its row
// was not expired with it, so no ended-but-live session can keep validating while
// ActiveImpersonations no longer lists it.
func impersonationEnded(sessData *sessioninfo.SessionData) bool {
	return sessData.Impersonation != nil && sessData.Impersonation.EndedAt != nil
}
