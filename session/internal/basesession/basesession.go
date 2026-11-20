// Package basesession implements the session management for the application.
package basesession

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/internal/sealed"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// attrEndUserID is the OpenTelemetry semantic-convention attribute for the
// authenticated end user of a request.
const attrEndUserID = "enduser.id"

// LogHandler defines the handler signature required for handling logs.
type LogHandler func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc

// BaseSession implements the shared features for all session implementations
type BaseSession struct {
	SessionTimeout time.Duration
	// ImpersonationTimeout is the hard cap on an impersonated session's lifetime,
	// fixed when the session is established; zero selects the library default.
	ImpersonationTimeout time.Duration
	// ImpersonationAudit, when set, receives every lifecycle event of an impersonated
	// session (see sessioninfo.ImpersonationEvent).
	ImpersonationAudit func(ctx context.Context, event sessioninfo.ImpersonationEvent) error
	// PrincipalResolver, when set, chooses the request's authorization subject at
	// validation (see session.WithPrincipalResolver). It runs with the validated
	// session in ctx for ordinary sessions alone: a session that acts as someone else
	// acts as whom its record names, and the resolver is not asked.
	PrincipalResolver func(ctx context.Context) (accesstypes.Principal, error)
	Handle            LogHandler
	Storage           sessionstorage.BaseStore
	CookieHandler     internalcookie.Handler
	// TableNames are the table names the session options set, in order. The
	// constructor hands each to its store setter, which validates it, and reports a
	// refusal under the option's name.
	TableNames []TableName

	// built marks the base session the session library built (New), and nothing else
	// sets it. A BaseSession that New did not build starts no impersonated session:
	// reflection can reach this type through a session type's field and make a value
	// of it over a store of the program's own choosing, and StartImpersonatedSession,
	// which takes whatever request and record it is handed, refuses there before
	// anything is written.
	built bool
}

// New returns the base session a session type runs on, over storage, marked as the
// library's own. The session library builds every base session here
// (sessioncore.NewBaseSession), so the mark is made in one place.
func New(storage sessionstorage.BaseStore) *BaseSession {
	return &BaseSession{Storage: storage, built: true}
}

// TableName is a table name a session option set: the option's name for the error, the
// name, and the store setter that takes and validates it.
type TableName struct {
	Option string
	Name   string
	Set    func(name string) error
}

// StartSession initializes a session by restoring it from a cookie, or if
// that fails, initializing a new session. The session cookie is then updated and
// the sessionID is inserted into the context.
func (s *BaseSession) StartSession(next http.Handler) http.Handler {
	return s.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := s.StartSessionAPI(ctx, w, r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		next.ServeHTTP(w, r.WithContext(ctx))

		return nil
	})
}

// StartSessionAPI exposes the internals of the StartSession Handler for use with the API interface
func (s *BaseSession) StartSessionAPI(ctx context.Context, w http.ResponseWriter, r *http.Request) (context.Context, error) {
	// Read Auth Cookie
	cval, foundAuthCookie, err := s.CookieHandler.ReadAuthCookie(r)
	if err != nil {
		return ctx, errors.Wrap(err, "cookie.CookieHandler.ReadAuthCookie()")
	}

	var sessionID impulse.UUID
	var validSessionID bool
	if foundAuthCookie {
		cSessionID, err := cval.GetString(internalcookie.SessionID)
		if err == nil {
			sessionID, validSessionID = internalcookie.ValidSessionID(cSessionID)
		}
	}

	if !foundAuthCookie || !validSessionID {
		var err error
		sessionID, err = impulse.NewUUID()
		if err != nil {
			return ctx, errors.Wrap(err, "impulse.NewUUID()")
		}
		cval = s.CookieHandler.NewAuthCookie(w, true, sessionID)
	}

	if sameSiteStrict, _ := cval.GetString(internalcookie.SameSiteStrict); sameSiteStrict != strconv.FormatBool(true) {
		// Upgrade cookie to SameSite=Strict
		// An external method's callback writes it None, so it survives the provider's redirect
		s.CookieHandler.WriteAuthCookie(w, true, cval)
	}

	// Store sessionID in context
	ctx = context.WithValue(ctx, sessioninfo.CTXSessionID, sessionID)

	// Add session ID to logging context
	l := logger.FromCtx(ctx).AddRequestAttribute("session ID", sessionID).
		WithAttributes().AddAttribute("session ID", sessionID).Logger()

	ctx = logger.NewCtx(ctx, l)

	return ctx, nil
}

// ValidateSession checks the sessionID in the database to validate that it has not expired
// and updates the last activity timestamp if it is still valid.
// StartSession handler must be called before calling ValidateSession
//
// The request's evidence — enduser.id, and the impersonation.* and principal.*
// attributes when they apply — is stamped on the span current in r.Context(): the
// server span, which is what trace-list filters match on.
func (s *BaseSession) ValidateSession(next http.Handler) http.Handler {
	return s.Handle(func(w http.ResponseWriter, r *http.Request) error {
		evidence := trace.SpanFromContext(r.Context())

		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := s.validateSession(ctx, evidence)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		next.ServeHTTP(w, r.WithContext(ctx))

		return nil
	})
}

// ValidateSessionAPI checks the session cookie and if it is valid, stores the session
// data into the context. The request's evidence (see ValidateSession) is stamped on the
// span current in ctx — the caller's span.
func (s *BaseSession) ValidateSessionAPI(ctx context.Context) (context.Context, error) {
	return s.validateSession(ctx, trace.SpanFromContext(ctx))
}

// validateSession validates the session in ctx and stamps the request's evidence on
// the evidence span.
func (s *BaseSession) validateSession(ctx context.Context, evidence trace.Span) (context.Context, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	// Validate that the sessionID is in database
	sessInfo, err := s.session(ctx, sessioninfo.IDFromCtx(ctx))
	if err != nil {
		return ctx, httpio.NewUnauthorizedMessageWithError(err, "invalid session")
	}

	// An impersonated session's evidence goes on the span before any refusal, so a
	// trace of a refused request still names the actor and principal.
	if imp := sessInfo.Impersonation; imp != nil {
		evidence.SetAttributes(impersonationSpanAttributes(imp)...)
	}

	// Check for expiration: the idle timeout, an impersonated session's hard cap, and an
	// impersonation record that has already ended.
	if sessInfo.Expired || time.Since(sessInfo.UpdatedAt) > s.SessionTimeout || impersonationExpired(sessInfo) || impersonationEnded(sessInfo) {
		s.endImpersonation(ctx, sessInfo, sessioninfo.ImpersonationEndedByExpiry)

		return ctx, httpio.NewUnauthorizedMessage("session expired")
	}

	// Update last activity (rate limit updates)
	if time.Since(sessInfo.UpdatedAt) > time.Second*5 {
		if err := s.Storage.UpdateSessionActivity(ctx, sessInfo.ID); err != nil {
			return ctx, errors.Wrap(err, "sessionstorage.BaseStore.UpdateSessionActivity()")
		}

		// A local actor's own session stays alive while they are impersonating: the
		// actor is at the keyboard, and EndImpersonation returns them to that session.
		// The impersonated session's hard cap bounds how long this can go on.
		if imp := sessInfo.Impersonation; imp != nil && imp.IsLocalActor() && imp.SourceSessionID.Valid {
			if err := s.Storage.UpdateSessionActivity(ctx, imp.SourceSessionID.UUID); err != nil {
				logger.FromCtx(ctx).Warnf("impersonation: source session %s not refreshed: %v", imp.SourceSessionID.UUID, err)
			}
		}
	}

	return s.Establish(ctx, evidence, sessInfo)
}

// session reads the session of a request. What decides an impersonated session is the
// library's own store's to say, so the library's own read is asked first
// (sealed.Session), which a store that wraps the library's cannot replace:
//   - A store the library made, handed over as it is, answers there, once.
//   - A session the library's own read finds impersonated is that session: its record,
//     its mask, its principal and whether it has ended are the stored ones, and the
//     wrapper's Session is never asked. A read cache in front of the store therefore
//     cannot keep an ended one alive.
//   - Every other session is an ordinary one, and the store's exported Session answers
//     for it: one the library's own read finds ordinary, one it does not hold, and
//     every session of a store that holds no read of the library's making (a mock, or
//     a store of an application's own). An answer that carries an impersonation record
//     there is refused: a session is impersonated only where the library's own store
//     says so.
//
// The store's answer is held to the session that was asked for (heldToTheSessionAsked):
// it is that session, by its id, and where the library's own read found the session it
// is that row's account under that row's username. A wrapper adds to an ordinary
// session; it does not answer one person's cookie with another person's session, which
// the start of an impersonated session would otherwise record and the end hand back.
//
// Nor does it say who the session's checks are made for, or keep a session going that
// the library's own store has ended (heldToTheLibrarysRow): a principal the store's
// answer carries is not taken, and where the library's own read found the session,
// whether it was signed out and when it was last used are that row's.
//
// A failure of the library's own read that is not one of those is the request's
// failure: the session is not handed to the wrapper to decide.
func (s *BaseSession) session(ctx context.Context, sessionID impulse.UUID) (*sessioninfo.SessionData, error) {
	own, err := sealed.Session(ctx, s.Storage, sessionID)
	switch {
	case sealed.SessionIsOwn(s.Storage):
		if err != nil {
			return nil, errors.Wrap(err, "sessionstorage.BaseStore.Session()")
		}

		return own, nil
	case err == nil && own.Impersonation != nil:
		return own, nil
	case err != nil && !httpio.HasNotFound(err) && !errors.Is(err, sealed.ErrSealed):
		return nil, errors.Wrap(err, "sealed.Session()")
	case err != nil:
		// The library's own store does not hold the session, or the store has no read
		// of the library's making: there is no row of the library's to hold the answer to.
		own = nil
	}

	sess, err := s.Storage.Session(ctx, sessionID)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.BaseStore.Session()")
	}
	if sess == nil || sess.SessionInfo == nil {
		return nil, errors.Newf("the store's Session answered session %s with no session and no error", sessionID)
	}
	if sess.Impersonation != nil {
		return nil, errors.Newf(
			"the store's Session answered session %s with an impersonation record that the session library's own store does not hold: "+
				"a session is impersonated only when the library started it (StartImpersonatedSession) over a store the library made, or one that embeds it",
			sessionID,
		)
	}
	if err := heldToTheSessionAsked(sessionID, own, sess); err != nil {
		return nil, err
	}

	return heldToTheLibrarysRow(own, sess), nil
}

// heldToTheSessionAsked refuses a store's answer for an ordinary session that is not
// the session the request's cookie names. The answer carries that session's id. And
// where the library's own store holds the session (own, nil when it does not), the
// answer names the account and the username that row does.
//
// Without this a store that wraps the library's, a read cache keyed by something other
// than the session's id for one, could answer one person's cookie with another person's
// live session. The request would run as that person, and a session that acts as
// someone else started from it would record them as the actor and, when it ended, hand
// the browser their own session's cookie.
func heldToTheSessionAsked(sessionID impulse.UUID, own, answered *sessioninfo.SessionData) error {
	if answered.ID != sessionID {
		return errors.Newf(
			"the store's Session answered session %s with session %s: the session of a request is the one its cookie names, "+
				"so a store that wraps the library's answers a session with that session",
			sessionID, answered.ID,
		)
	}
	if own == nil {
		return nil
	}
	if answered.UserID != own.UserID || answered.Username != own.Username {
		return errors.Newf(
			"the store's Session answered session %s as the session of another account or under another username than the session library's own store holds it under: "+
				"a store that wraps the library's adds to an ordinary session and does not change whose it is",
			sessionID,
		)
	}

	return nil
}

// heldToTheLibrarysRow answers a store's ordinary session with what a store that wraps
// the library's does not decide put back. Such a store adds to an ordinary session (its
// custom data, an answer from a cache); two things it does not say:
//   - Who the session's checks are made for. The Principal is set at validation and
//     never stored, so one a store's answer carries is not the library's and is cleared:
//     the principal of an ordinary session comes from the resolver, or from the
//     session's own user.
//   - That a session the library's own store has ended is still live. Where the
//     library's own read found the session (own, nil when it did not), the session is
//     expired when that row is, whatever the answer says, and it was last used when
//     that row says it was, which is what the idle timeout counts from. A cache in front
//     of the store therefore keeps no signed-out or idle session working. A store's
//     answer still ends a session the row holds live, as an error from it does.
//
// The answer is copied before it is changed, so a cache keeps what it held.
func heldToTheLibrarysRow(own, answered *sessioninfo.SessionData) *sessioninfo.SessionData {
	held := *answered
	held.Principal = accesstypes.Principal{}
	if own != nil {
		info := *answered.SessionInfo
		info.Expired = info.Expired || own.Expired
		info.UpdatedAt = own.UpdatedAt
		held.SessionInfo = &info
	}

	return &held
}

// Establish puts the validated session in ctx, resolves its principal, and stamps the
// request's evidence on the evidence span and on the request's log entry and every
// line logged within it: the end user, an impersonation's attributes, a token's
// attributes, and the principal a resolver changed. ValidateSession calls it once the
// cookie's session row has been checked; a machine outlet's token validation calls it
// with the session its token stands in for.
func (s *BaseSession) Establish(ctx context.Context, evidence trace.Span, sessData *sessioninfo.SessionData) (context.Context, error) {
	ctx = context.WithValue(ctx, sessioninfo.CtxSessionInfo, sessData)

	if err := s.resolvePrincipal(ctx, sessData); err != nil {
		return ctx, err
	}

	// enduser.id is the OpenTelemetry convention for the authenticated end user: the
	// session's effective identity, on every validated request.
	evidence.SetAttributes(attribute.String(attrEndUserID, sessData.Username))

	// Add user to logging context — and, for an impersonated session or a token, the
	// evidence attributes on the request entry and every line logged within it;
	// likewise the principal when a resolver changed it.
	l := logger.FromCtx(ctx).AddRequestAttribute("username", sessData.Username)
	attrs := l.WithAttributes().AddAttribute("username", sessData.Username)
	if imp := sessData.Impersonation; imp != nil {
		for _, a := range imp.Attributes() {
			l = l.AddRequestAttribute(a.Key, a.Value)
			attrs = attrs.AddAttribute(a.Key, a.Value)
		}
	}
	if tok := sessData.Token; tok != nil {
		for _, a := range tok.Attributes() {
			l = l.AddRequestAttribute(a.Key, a.Value)
			attrs = attrs.AddAttribute(a.Key, a.Value)
		}
		evidence.SetAttributes(spanAttributes(tok.Attributes())...)
	}
	if p := sessData.Principal; p != (accesstypes.Principal{}) {
		kind, name := principalKindName(p)
		l.AddRequestAttribute(sessioninfo.AttrPrincipalKind, kind).AddRequestAttribute(sessioninfo.AttrPrincipal, name)
		attrs = attrs.AddAttribute(sessioninfo.AttrPrincipalKind, kind).AddAttribute(sessioninfo.AttrPrincipal, name)
		evidence.SetAttributes(attribute.String(sessioninfo.AttrPrincipalKind, kind), attribute.String(sessioninfo.AttrPrincipal, name))
	}

	return logger.NewCtx(ctx, attrs.Logger()), nil
}

// resolvePrincipal runs the configured PrincipalResolver for the validated session in
// ctx and records its choice on sessData.Principal when it differs from the default
// subject. A session that acts as someone else is never asked about: whom it acts as is
// its declaration's and its record's, and the resolver is the application's code, which
// decides nothing of such a session. A resolver error is a server error, never an
// unauthorized one — the session is valid; the application could not decide what it acts
// as.
func (s *BaseSession) resolvePrincipal(ctx context.Context, sessData *sessioninfo.SessionData) error {
	if s.PrincipalResolver == nil {
		return nil
	}
	if sessData.Impersonation != nil {
		return nil
	}

	principal, err := s.PrincipalResolver(ctx)
	if err != nil {
		return errors.Wrap(err, "PrincipalResolver()")
	}
	if principal == (accesstypes.Principal{}) || principal == sessioninfo.PrincipalFromCtx(ctx) {
		return nil
	}
	sessData.Principal = principal

	return nil
}

// principalKindName renders a principal as its evidence attributes.
func principalKindName(p accesstypes.Principal) (kind, name string) {
	if role, ok := p.Role(); ok {
		return sessioninfo.PrincipalKindRole, string(role)
	}
	user, _ := p.User()

	return sessioninfo.PrincipalKindUser, string(user)
}

// Authenticated is the handler reports if the session is authenticated
func (s *BaseSession) Authenticated() http.HandlerFunc {
	type response struct {
		Authenticated bool                   `json:"authenticated"`
		Username      string                 `json:"username"`
		Impersonation *ImpersonationResponse `json:"impersonation,omitempty"`
	}

	return s.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := s.ValidateSessionAPI(ctx)
		if err != nil {
			if httpio.HasUnauthorized(err) {
				return httpio.NewEncoder(w).Ok(response{})
			}

			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		sessInfo := sessioninfo.FromCtx(ctx)
		imp, _ := sessioninfo.ImpersonationFromCtx(ctx)

		// set response values
		res := response{
			Authenticated: true,
			Username:      sessInfo.Username,
			Impersonation: NewImpersonationResponse(imp),
		}

		return httpio.NewEncoder(w).Ok(res)
	})
}

// Logout destroys the current session
func (s *BaseSession) Logout() http.HandlerFunc {
	return s.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		if err := s.LogoutAPI(ctx); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(nil)
	})
}

// SetXSRFToken sets the XSRF Token
func (s *BaseSession) SetXSRFToken(next http.Handler) http.Handler {
	return s.Handle(func(w http.ResponseWriter, r *http.Request) error {
		set, err := s.CookieHandler.RefreshXSRFTokenCookie(w, r, sessioninfo.IDFromRequest(r))
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(r.Context(), err)
		}

		if set && !internalcookie.SafeMethods.Contain(r.Method) {
			// Cookie was not present and request requires XSRF Token, so
			// redirect request to try again now that the XSRF Token Cookie is set.
			// The request URI is sanitized so a crafted "//host/path" cannot redirect
			// off-site.
			internalcookie.RedirectToReturnURL(w, r.RequestURI, http.StatusTemporaryRedirect)

			return nil
		}

		next.ServeHTTP(w, r)

		return nil
	})
}

// ValidateXSRFToken validates the XSRF Token
func (s *BaseSession) ValidateXSRFToken(next http.Handler) http.Handler {
	return s.Handle(func(w http.ResponseWriter, r *http.Request) error {
		// Validate XSRFToken for non-safe
		if !internalcookie.SafeMethods.Contain(r.Method) {
			hasValidXSRFToken, err := s.CookieHandler.HasValidXSRFToken(r)
			if err != nil {
				return httpio.NewEncoder(w).ClientMessage(r.Context(), err)
			}

			if !hasValidXSRFToken {
				// Token validation failed
				return httpio.NewEncoder(w).ClientMessage(r.Context(), httpio.NewForbiddenMessage("invalid XSRF token"))
			}
		}

		next.ServeHTTP(w, r)

		return nil
	})
}
