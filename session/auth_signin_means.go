package session

import (
	"context"
	"net/http"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// SignInMeans is what a user signs in with: whether they hold a password, and the
// directory sign-ins linked to them. It is what a page needs to offer the removal of a
// password only where it can happen (a password is held, and something else signs the
// person in) and to say what the removal leaves. The password's hash is no part of it.
type SignInMeans struct {
	// Password reports whether the user holds a password.
	Password bool `json:"password"`
	// Directories are the sign-in methods the login page offers that an identity linked
	// to the user signs in through, in the page's order. It is empty, never nil, when
	// none does: the user holds no identity link, or only links of a method the page no
	// longer offers, which the store still counts as a means of sign-in.
	Directories []LinkedDirectory `json:"directories"`
}

// LinkedDirectory is a sign-in method of the login page that an identity linked to a
// user signs in through: the registration's name and the label its button carries, as
// the methods list gives them (MethodList). A link names its method and not its
// registration, so where the page lists two registrations of one method (two Entra
// tenants), a link of that method answers both.
type LinkedDirectory struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// SignInMeans answers what a user signs in with (see SignInMeans the type): for an
// administrator's page, or a flow of the application's own. The caller guards who may
// ask about whom. A user that does not exist is a NotFound.
func (p *AuthAPI[S, U, Tx]) SignInMeans(ctx context.Context, userID impulse.UUID) (*SignInMeans, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	a := p.auth
	user, err := a.storage.User(ctx, userID)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	links, err := a.storage.IdentitiesByUser(ctx, userID)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.IdentitiesByUser()")
	}

	return a.signInMeansOf(user, links), nil
}

// signInMeansOf is what user signs in with, given the identities linked to them: the
// listed external registrations whose method one of the links carries, in the page's
// order and under the labels the methods list gave them.
func (a *Auth[S, U, Tx]) signInMeansOf(user *sessionstorage.SessionUser, links []*sessionstorage.SessionIdentity) *SignInMeans {
	linked := make(map[sessioninfo.AuthMethod]bool, len(links))
	for _, link := range links {
		linked[link.Method] = true
	}
	m := a.registered()
	means := &SignInMeans{Password: user.PasswordHash != nil, Directories: []LinkedDirectory{}}
	for _, name := range m.listed {
		if external, ok := m.external[name]; ok && linked[external.Name] {
			means.Directories = append(means.Directories, LinkedDirectory{Name: name, Label: m.labels[name]})
		}
	}

	return means
}

// SignInMeans is the handler a signed-in person's account page reads what they sign in
// with from: {"password", "directories": [{"name", "label"}]} (see SignInMeans the
// type), for the session's user and nobody else. The page hides the change and the
// removal of a password from a person who holds none, offers the removal only where a
// directory sign-in is linked, and names what the removal leaves by its label. The
// server's own refusals stand whatever the page shows.
//
// The handler validates the session itself, as the name change does, so it is routed
// beside it, outside ValidateSession, and its path is named to the page as the methods
// list's SignInMeans. A session that acts for someone else is answered for the person
// it acts as, as the session read is.
func (a *Auth[S, U, Tx]) SignInMeans() http.HandlerFunc {
	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		if _, validated := ctx.Value(sessioninfo.CtxUserInfo).(*sessioninfo.UserInfo); !validated {
			var err error
			if ctx, err = a.validateRequest(ctx); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
		}
		means, err := a.API().SignInMeans(ctx, sessioninfo.UserFromCtx(ctx).ID)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(means)
	}))
}
