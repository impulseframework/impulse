package session

import (
	"net/http"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/passwordstrength"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// The password strength question. A page that sets a password asks the server how strong
// the password is while the person types it, and the server answers with the judgment the
// path that sets it holds it to (strongEnough), so the bar the page draws and the refusal
// the person may meet agree. The question reads no session, no account and no link: it is
// asked at an invitation, a sign-up and a reset link, where the person has no session, and
// it is judged against the details the page sends, so its answer says nothing about any
// account. The path that sets the password judges against the account's own details and has
// the last word.

// strengthAskLimit bounds the questions one client asks: with attempts.ClientMultiple, the
// 3,000th question within five minutes from one address or under one cookie is refused and
// holds the client five minutes, and a question during the hold extends it. A person typing
// asks once a keystroke or so; a client that asks far more is trying passwords against the
// judgment, which costs the server a judgment each time. The count is a limiter of its own
// (signInMethods.asks), so it shares nothing with sign-in attempts, and asking never locks a
// sign-in.
var strengthAskLimit = sessionstorage.SignInAttemptLimit{Failures: 300, Window: 5 * time.Minute, Lock: 5 * time.Minute}

// strengthAskRefused is what a client the ask limit holds is told.
const strengthAskRefused = "Too many passwords have been checked from here. Wait a few minutes, then go on."

// strengthAnswer is the strength handler's answer: where the password stands on the scale
// from 0 to 100, where the application's minimum sits on it, whether the password has
// reached it, and, while it has not, the sentence that says why.
type strengthAnswer struct {
	StrongEnough bool   `json:"strongEnough"`
	Strength     int    `json:"strength"`
	Minimum      int    `json:"minimum"`
	Reason       string `json:"reason,omitempty"`
}

// passwordStrengthHandler is the password method's strength handler. Each question is
// counted against the client first, before its body is read, and a client the ask limit
// holds is answered 429 with too_many_attempts and the wait, with nothing judged. Otherwise
// the password is judged against the details the request carries under the method's minimum.
// An absent password is the empty one, which is answered at strength 0. Neither the password
// nor the details are put in a log line, an error or a span.
func (a *Auth[S, U, Tx]) passwordStrengthHandler() http.HandlerFunc {
	type request struct {
		Password string `json:"password"`
		Username string `json:"username"`
		Name     string `json:"name"`
		Email    string `json:"email"`
	}
	decoder := sessioncore.NewDecoder[request]()

	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		if wait := a.methods.asks.Count(a.clientOf(r)); wait > 0 {
			return writeSignInError(ctx, w, sessioninfo.NewTooManyAttemptsRefusalSaying(wait, strengthAskRefused))
		}

		req, err := decoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		who := passwordstrength.Details{Username: req.Username, Name: req.Name, Email: req.Email}
		judgment := passwordstrength.Judge(req.Password, who, a.methods.minimumStrength)
		answer := strengthAnswer{
			StrongEnough: judgment.StrongEnough, Strength: judgment.Strength, Minimum: passwordstrength.Mark,
			Reason: judgment.Reason.Sentence(),
		}
		if err := httpio.NewEncoder(w).Ok(answer); err != nil {
			return errors.Wrap(err, "httpio.Encoder.Ok()")
		}

		return nil
	})
}
