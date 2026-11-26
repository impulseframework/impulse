package session

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// usernameRequest is the request that carries a username a person typed.
type usernameRequest struct {
	Username string `json:"username"`
}

var usernameDecoder = sessioncore.NewDecoder[usernameRequest]()

// errUsernameIsAddress is the refusal of a person's own change of their username, and of
// the question whether a name is free, where the usernames are the addresses.
func errUsernameIsAddress() error {
	return httpio.NewConflictMessage("You sign in with your email address, so your username changes when you change the address.")
}

// usernameHeldMessage is the refusal of a username someone else holds.
const usernameHeldMessage = "Someone else has that username."

// readsAsAddress reports whether a username reads as an email address: whether it has
// an @ (U+0040) in it. Both databases match a username with an @ in it by the case of
// its ASCII letters alone and any other by canonical normalization and case folding,
// and under neither does another character become an @, so a name without one is found
// by no address and by no sign-in name that has one.
func readsAsAddress(name string) bool {
	return strings.Contains(name, "@")
}

// usernameAddressRefusal is the refusal of a username a person typed for themselves that
// reads as an email address and is not their own. own is the address the account holds
// confirmed, or the address it is being made with, "" for none. It is nil for a name with
// no @ in it, and for own the same but for the case of its ASCII letters (sameAddress);
// any other name is a 400, given before the store is asked, so the answer is the same
// whether or not an account holds the name. No address is written into the words.
func usernameAddressRefusal(name, own string) error {
	if !readsAsAddress(name) {
		return nil
	}
	if own == "" {
		return httpio.NewBadRequestMessage("A username with an @ in it has to be your own email address, and your account has no confirmed address yet. " +
			"Confirm your address first, or choose a name without an @.")
	}
	if sameAddress(name, own) {
		return nil
	}

	return httpio.NewBadRequestMessage("A username with an @ in it has to be your own email address. Type your own address, or choose a name without an @.")
}

// confirmedAddressOf is the address user holds confirmed, "" for none: the own address
// of usernameAddressRefusal for a signed-in person.
func confirmedAddressOf(user *sessioninfo.UserInfo) string {
	if !user.EmailVerified() {
		return ""
	}

	return *user.Email
}

// ChangeUsername is the handler a signed-in person's account page calls to change their
// own username: it accepts {"username"} and answers {"username"}, the username as it is
// kept. There are two rules. No other account holds the name, matched as a sign-in
// matches it. And a name with an @ in it is the address the person's account holds,
// confirmed, in any case of its ASCII letters: any other is a 400 before the store is
// asked, so the answer does not tell whether an account holds the name
// (usernameAddressRefusal). The space around the name is cut, a name of nothing but
// space is a 400, and one someone else holds a 409. The person's own name in another
// case is theirs to write. The person's live sessions follow the name, so they stay
// signed in, and they sign in under the new name from then on. Where the usernames are
// the addresses (UsernameIsEmail) the change is a 409 and nothing is read or written:
// the username changes when the person confirms a new address. A name with a control
// character in it, or longer than 200 characters, is a 400 before the store is asked:
// neither database keeps every such name.
//
// The handler validates the session itself, as the name change does, so it is routed
// beside it, outside ValidateSession; an impersonated session is refused, since the
// username is the person's own to change.
func (a *Auth[S, U, Tx]) ChangeUsername() http.HandlerFunc {
	type response struct {
		Username string `json:"username"`
	}

	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.ownSessionRequest(ctx, "ChangeUsername")
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if a.registered().usernameIsEmail() {
			return httpio.NewEncoder(w).ClientMessage(ctx, errUsernameIsAddress())
		}
		req, err := usernameDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		user := sessioninfo.UserFromCtx(ctx)
		name := strings.TrimSpace(req.Username)
		if name == "" {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessage("Type a username."))
		}
		if err := sessioninfo.UsernameRefusal(name); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if name == user.Username {
			return httpio.NewEncoder(w).Ok(response{Username: name})
		}
		if err := usernameAddressRefusal(name, confirmedAddressOf(user)); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		free, err := a.usernameFreeFor(ctx, name, user)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if !free {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewConflictMessage(usernameHeldMessage))
		}
		renamed := accountRecord(auditlog.ActUsernameChanged, a.acting(ctx), user.ID, name)
		renamed.OldValue, renamed.NewValue = user.Username, name
		if err := a.storage.SetUserUsername(ctx, user.ID, name, renamed); err != nil {
			// Someone took the name between the lookup and the rename.
			if httpio.HasConflict(err) {
				return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewConflictMessageWithError(err, usernameHeldMessage))
			}

			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "sessionstorage.AuthStore.SetUserUsername()"))
		}
		stored, err := a.storage.User(ctx, user.ID)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "sessionstorage.AuthStore.User()"))
		}

		return httpio.NewEncoder(w).Ok(response{Username: stored.Username})
	}))
}

// UsernameFree is the handler a signed-in person's account page asks, as the person
// types a username, whether they may take it: it accepts {"username"} and answers
// {"free"}, true for a name no one holds or the person holds themselves (in any case),
// false for a name someone else holds or one of nothing but space. It says nothing else:
// not who holds a name, and no id. The question is a POST, so a typed name is in no URL
// and no request log. Where the usernames are the addresses it is a 409, as the change
// is, and a name the change refuses as one the store cannot keep is a 400 in the same
// words. A name with an @ in it that is not the person's own confirmed address is a 400
// in the change's words, and nothing is looked up.
//
// The handler validates the session itself, as ChangeUsername does, and refuses an
// impersonated session.
func (a *Auth[S, U, Tx]) UsernameFree() http.HandlerFunc {
	type response struct {
		Free bool `json:"free"`
	}

	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.ownSessionRequest(ctx, "UsernameFree")
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if a.registered().usernameIsEmail() {
			return httpio.NewEncoder(w).ClientMessage(ctx, errUsernameIsAddress())
		}
		req, err := usernameDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		name := strings.TrimSpace(req.Username)
		if name == "" {
			return httpio.NewEncoder(w).Ok(response{Free: false})
		}
		if err := sessioninfo.UsernameRefusal(name); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		user := sessioninfo.UserFromCtx(ctx)
		if name != user.Username {
			if err := usernameAddressRefusal(name, confirmedAddressOf(user)); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
		}
		free, err := a.usernameFreeFor(ctx, name, user)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(response{Free: free})
	}))
}

// usernameFreeFor reports whether user may take name: no user holds it, as a sign-in
// looks a name up, or user holds it themselves.
func (a *Auth[S, U, Tx]) usernameFreeFor(ctx context.Context, name string, user *sessioninfo.UserInfo) (bool, error) {
	holder, err := a.storage.UserByUserName(ctx, name)
	if err != nil {
		if httpio.HasNotFound(err) {
			return true, nil
		}

		return false, errors.Wrap(err, "sessionstorage.AuthStore.UserByUserName()")
	}

	return holder.ID == user.ID, nil
}
