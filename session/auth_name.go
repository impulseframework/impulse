package session

import (
	"context"
	"net/http"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// nameRequest is the request that carries a person's name.
type nameRequest struct {
	Name string `json:"name"`
}

var nameDecoder = sessioncore.NewDecoder[nameRequest]()

// ChangeName is the handler a signed-in person's account page calls to write their own
// name: it accepts {"name"} and answers {"name"}, the name as it is kept (without the
// space around it), absent when the person now holds none. A name of nothing but
// space takes the name off; one longer than a user row keeps is a 400. The name is
// what pages show for the person in place of the username, which stays what
// identifies them: changing it renames nobody and ends no session.
//
// The handler validates the session itself, as the password change does, so it is
// routed beside it, outside ValidateSession; an impersonated session is refused, since
// the name is the person's own to write. A name a directory asserts at a later
// sign-in never replaces the one written here; after the name is taken off, the next
// such sign-in fills it again.
func (a *Auth[S, U, Tx]) ChangeName() http.HandlerFunc {
	type response struct {
		Name *string `json:"name,omitempty"`
	}

	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		if _, validated := ctx.Value(sessioninfo.CtxUserInfo).(*sessioninfo.UserInfo); !validated {
			var err error
			if ctx, err = a.validateRequest(ctx); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
		}
		if err := sessioncore.RefuseImpersonated(ctx, a.baseSession, "ChangeName", true); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		req, err := nameDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		user := sessioninfo.UserFromCtx(ctx)
		if err := a.storage.SetUserName(ctx, user.ID, &req.Name); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "sessionstorage.AuthStore.SetUserName()"))
		}
		stored, err := a.storage.User(ctx, user.ID)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "sessionstorage.AuthStore.User()"))
		}

		return httpio.NewEncoder(w).Ok(response{Name: stored.Name})
	}))
}

// SetSessionUserName writes a person's name on a user, or takes it off (nil, or nothing
// but space): an operator's entry. The name is kept without the space around it; one
// longer than sessioninfo.MaxNameLength is a BadRequest, and a user that does not exist
// a NotFound. It replaces whatever the user held, and stands until it is written again:
// a later sign-in through a directory fills a name only where the user holds none.
func (p *AuthAPI[S, U, Tx]) SetSessionUserName(ctx context.Context, userID impulse.UUID, name *string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := p.auth.storage.SetUserName(ctx, userID, name); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.SetUserName()")
	}

	return nil
}
