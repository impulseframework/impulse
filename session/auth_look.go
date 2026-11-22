package session

import (
	"net/http"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// lookRequest is the request that carries the look a person chose.
type lookRequest struct {
	Scheme  string `json:"scheme"`
	Palette string `json:"palette"`
}

var lookDecoder = sessioncore.NewDecoder[lookRequest]()

// ChangeLook is the handler a signed-in person's pages call to keep the look the person
// chose, so it follows them to every browser they sign in from: it accepts {"scheme",
// "palette"} and answers the look as it is kept, each half absent when the person now
// holds none. The scheme is light, dark or device, under which a page is drawn light or
// dark as the device it is on is set; the palette is an id as the application names its
// palettes, which the session library does not know, so a page drops one its application
// no longer offers. A scheme that is none of the three, or a palette id longer than a
// user row keeps, is a 400. The look replaces the one held as a whole: a half left out
// is taken off.
//
// The handler validates the session itself, as the password change does, so it is
// routed beside it, outside ValidateSession; an impersonated session is refused, since
// the look is the person's own to choose. Nothing else writes it: no operator's entry
// and no method of the generated user management.
func (a *Auth[S, U, Tx]) ChangeLook() http.HandlerFunc {
	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		if _, validated := ctx.Value(sessioninfo.CtxUserInfo).(*sessioninfo.UserInfo); !validated {
			var err error
			if ctx, err = a.validateRequest(ctx); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
		}
		if err := sessioncore.RefuseImpersonated(ctx, a.baseSession, "ChangeLook", true); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		req, err := lookDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		user := sessioninfo.UserFromCtx(ctx)
		if err := a.storage.SetUserLook(ctx, user.ID, sessioninfo.Look{Scheme: req.Scheme, Palette: req.Palette}); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "sessionstorage.AuthStore.SetUserLook()"))
		}
		stored, err := a.storage.User(ctx, user.ID)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "sessionstorage.AuthStore.User()"))
		}

		return httpio.NewEncoder(w).Ok(stored.Look())
	}))
}
