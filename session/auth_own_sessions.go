package session

import (
	"context"
	"net/http"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/browsername"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// ownSession is one of a person's own live sessions as their account page lists it.
type ownSession struct {
	ID impulse.UUID `json:"id"`
	// Browser is the name of the browser the session was signed in from, absent where
	// it was not recognized.
	Browser string `json:"browser,omitempty"`
	// Phone reports whether the browser is a phone's (browsername.Phone).
	Phone      bool      `json:"phone"`
	SignedInAt time.Time `json:"signedInAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	// Current marks the session of the request: the browser the person is using.
	Current bool `json:"current"`
}

// endOwnSessionRequest names the session a person signs out of.
type endOwnSessionRequest struct {
	SessionID impulse.UUID `json:"sessionId"`
}

var endOwnSessionDecoder = sessioncore.NewDecoder[endOwnSessionRequest]()

// OwnSessions is the handler a signed-in person's account page lists where they are
// signed in from: {"sessions": [{"id", "browser", "phone", "signedInAt", "lastUsedAt",
// "current"}], "rememberedBrowsers"}. The sessions are the person's own live ones, as
// the library judges a request (not ended, and used within the session timeout), with
// the request's own first and marked current and the rest most recently used first;
// "browser" is the name of the browser a session was signed in from, absent where it
// was not recognized, and "phone" whether that is a phone. A session someone else
// established acting as the person is not theirs, and is not listed. "rememberedBrowsers"
// counts the browsers remembered for the person, so the page offers to forget them only
// where there are some.
//
// The handler validates the session itself, as the name change does, so it is routed
// beside it, outside ValidateSession; an impersonated session is refused, since where a
// person is signed in is theirs alone to read.
func (a *Auth[S, U, Tx]) OwnSessions() http.HandlerFunc {
	type response struct {
		Sessions           []ownSession `json:"sessions"`
		RememberedBrowsers int          `json:"rememberedBrowsers"`
	}

	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.ownSessionRequest(ctx, "OwnSessions")
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		user := sessioninfo.UserFromCtx(ctx)
		live, err := a.liveOwnSessions(ctx, user.ID)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		remembered, err := a.storage.TrustedDeviceCount(ctx, user.ID)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "sessionstorage.AuthStore.TrustedDeviceCount()"))
		}

		return httpio.NewEncoder(w).Ok(response{Sessions: ownSessionsOf(live, sessioninfo.IDFromCtx(ctx)), RememberedBrowsers: remembered})
	}))
}

// EndOwnSession is the handler a signed-in person's account page signs them out of one
// browser of their own with: it accepts {"sessionId"}, one of the sessions OwnSessions
// lists, ends it and answers 200 with no body. The browser the person is using is refused with a
// 400 (they sign out of it with the logout), and a session that is not among their own
// live ones with a 404: it has ended already, or is no session of theirs to end. No
// mail is sent: it is the person's own act, at their own browser.
//
// The handler validates the session itself, as OwnSessions does, and refuses an
// impersonated session.
func (a *Auth[S, U, Tx]) EndOwnSession() http.HandlerFunc {
	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.ownSessionRequest(ctx, "EndOwnSession")
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		req, err := endOwnSessionDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if req.SessionID == sessioninfo.IDFromCtx(ctx) {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessage("This is the browser you are using. Use Sign out to leave it."))
		}
		live, err := a.liveOwnSessions(ctx, sessioninfo.UserFromCtx(ctx).ID)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		listed := false
		for _, s := range live {
			if s.ID == req.SessionID {
				listed = true

				break
			}
		}
		if !listed {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewNotFoundMessage("That browser is already signed out."))
		}
		if err := a.storage.DestroySession(ctx, req.SessionID); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "sessionstorage.AuthStore.DestroySession()"))
		}

		return httpio.NewEncoder(w).Ok(nil)
	}))
}

// EndOtherSessions is the handler a signed-in person's account page signs them out of
// every browser but the one they are using with: every session OwnSessions lists but
// the request's own ends, and the answer is {"ended"}, how many did. A session someone
// else established acting as the person is not theirs, and stands. No mail is sent.
//
// The handler validates the session itself, as OwnSessions does, and refuses an
// impersonated session.
func (a *Auth[S, U, Tx]) EndOtherSessions() http.HandlerFunc {
	type response struct {
		Ended int `json:"ended"`
	}

	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.ownSessionRequest(ctx, "EndOtherSessions")
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		live, err := a.liveOwnSessions(ctx, sessioninfo.UserFromCtx(ctx).ID)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		current := sessioninfo.IDFromCtx(ctx)
		ended := 0
		for _, s := range live {
			if s.ID == current {
				continue
			}
			if err := a.storage.DestroySession(ctx, s.ID); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "sessionstorage.AuthStore.DestroySession()"))
			}
			ended++
		}

		return httpio.NewEncoder(w).Ok(response{Ended: ended})
	}))
}

// ForgetBrowsers is the handler a signed-in person's account page forgets every
// browser remembered for them with: each asks for the second factor again at their
// next sign-in from it, this one included, whose cookie loses the person's entry and
// keeps the other people's. It answers 200 with no body. No session ends and no mail is sent.
//
// The handler validates the session itself, as OwnSessions does, and refuses an
// impersonated session.
func (a *Auth[S, U, Tx]) ForgetBrowsers() http.HandlerFunc {
	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.ownSessionRequest(ctx, "ForgetBrowsers")
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		user := sessioninfo.UserFromCtx(ctx)
		if err := a.storage.RevokeTrustedDevices(ctx, user.ID); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "sessionstorage.AuthStore.RevokeTrustedDevices()"))
		}
		a.dropTrustedEntry(ctx, w, impulse.NullUUID{UUID: user.ID, Valid: true})

		return httpio.NewEncoder(w).Ok(nil)
	}))
}

// ownSessionRequest validates the request's session where the middleware has not, as
// the name change does, and refuses an impersonated session for operation: the
// person's own sessions and remembered browsers are theirs alone.
func (a *Auth[S, U, Tx]) ownSessionRequest(ctx context.Context, operation string) (context.Context, error) {
	if _, validated := ctx.Value(sessioninfo.CtxUserInfo).(*sessioninfo.UserInfo); !validated {
		var err error
		if ctx, err = a.validateRequest(ctx); err != nil {
			return ctx, err
		}
	}
	if err := sessioncore.RefuseImpersonated(ctx, a.baseSession, operation, true); err != nil {
		return ctx, err
	}

	return ctx, nil
}

// liveOwnSessions are userID's own live sessions, as the library judges a request:
// not ended, and used within the session timeout.
func (a *Auth[S, U, Tx]) liveOwnSessions(ctx context.Context, userID impulse.UUID) ([]*sessionstorage.OwnSession, error) {
	live, err := a.storage.OwnSessions(ctx, userID, time.Now().Add(-a.baseSession.SessionTimeout))
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.OwnSessions()")
	}

	return live, nil
}

// ownSessionsOf lists live as the page shows them: the session current first, marked,
// and the rest in the order given. It is empty, never nil, for none.
func ownSessionsOf(live []*sessionstorage.OwnSession, current impulse.UUID) []ownSession {
	listed := make([]ownSession, 0, len(live))
	for _, s := range live {
		entry := ownSession{ID: s.ID, SignedInAt: s.CreatedAt, LastUsedAt: s.UpdatedAt, Current: s.ID == current}
		if s.Browser != nil {
			entry.Browser = *s.Browser
			entry.Phone = browsername.Phone(*s.Browser)
		}
		if entry.Current {
			listed = append([]ownSession{entry}, listed...)

			continue
		}
		listed = append(listed, entry)
	}

	return listed
}
