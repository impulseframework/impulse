package session

import (
	"context"
	"net/http"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// A sign-up is how a person creates their own account with a password, while the
// password method is open to it: they give their address, follow the link mailed to it,
// and set a password; the account is created with the address verified, since the
// person just proved they read it, and with the default role the method's row names.
// Whether the method is open is an administrator's choice in the running application,
// its row of the sign-in methods table, read as each request arrives: until it is
// opened the method is invite only, and every sign-up route answers 404. An address an
// account already holds is pointed at that account by mail, and the screen says
// nothing. See "Sign-up" in the README.

// SignUpHandlers are the HTTP handlers of sign-up.
type SignUpHandlers struct {
	start    http.HandlerFunc
	open     http.HandlerFunc
	complete http.HandlerFunc
}

// Start accepts {"email"} and mails that address a link to finish the sign-up. It
// answers {} whatever the address's state, so the screen reveals nothing: an address
// an account already holds is told so by mail. An address that is not one is 400; a
// mailbox sent five links within fifteen minutes and using none is refused with
// too_many_emails and the wait; 404 while the password method is not open to sign-up.
// Route it behind StartSession and ValidateXSRFToken.
func (h *SignUpHandlers) Start() http.HandlerFunc { return h.start }

// Open accepts {"token"}, the token the mailed link carried, and answers {"email",
// "usernameIsEmail"}: the address, and whether the account's username is the address
// or the person chooses one. A token whose link is no longer live is 404, as is every
// token while the password method is not open to sign-up.
func (h *SignUpHandlers) Open() http.HandlerFunc { return h.open }

// Complete accepts {"token", "password", "username"}, creates the account with the
// address verified and the method's default role, and signs the person in, answering as
// the password login does ({"mfaIsRequired"}). username is the address when the
// password method declares the username as the email address, and chosen otherwise. A
// taken username or address is 409, a password under eight characters 400, a token
// whose link is no longer live 404, and a sign-up through a method that was closed
// since the link was mailed 404: the link is spent, the account made and the role
// given in one transaction that reads the method's row, or none of it is. Route it
// beside the password login, behind ValidateXSRFToken.
func (h *SignUpHandlers) Complete() http.HandlerFunc { return h.complete }

// SignUp returns the sign-up handlers. It panics on an Auth without mail, as a wiring
// mistake. Every application with the password method and mail routes them: whether
// sign-up is offered is an administrator's choice in the running application, read as
// each request arrives.
func (a *Auth[S, U, Tx]) SignUp() *SignUpHandlers {
	a.requireMail("SignUp()")

	return &SignUpHandlers{
		start:    a.serving(a.signUpStart()),
		open:     a.serving(a.signUpOpen()),
		complete: a.serving(a.signUpComplete()),
	}
}

// passwordSignUpOpen reports whether the password method is open to sign-up now: it is
// registered, and an administrator opened it (its row of the sign-in methods table). A
// method with no row is invite only.
func (a *Auth[S, U, Tx]) passwordSignUpOpen(ctx context.Context) (bool, error) {
	if !a.registered().passwordRegistered() {
		return false, nil
	}
	chosen, err := a.signInMethodRow(ctx, string(sessioninfo.MethodPassword))
	if err != nil {
		return false, err
	}

	return chosen.Open(), nil
}

// signInMethodRow reads what an administrator chose for the sign-in method registered
// under name: its row of the sign-in methods table, nil for a method with none.
func (a *Auth[S, U, Tx]) signInMethodRow(ctx context.Context, name string) (*sessionstorage.SignInMethod, error) {
	rows, err := a.storage.SignInMethods(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.SignInMethods()")
	}
	for _, row := range rows {
		if row.Name == name {
			return row, nil
		}
	}

	return nil, nil
}

// refuseSignUpClosed answers a sign-up request while the password method is not open
// to sign-up, and reports whether it did.
func (a *Auth[S, U, Tx]) refuseSignUpClosed(ctx context.Context, w http.ResponseWriter) (refused bool, err error) {
	open, err := a.passwordSignUpOpen(ctx)
	if err != nil {
		return true, httpio.NewEncoder(w).ClientMessage(ctx, err)
	}
	if !open {
		return true, httpio.NewEncoder(w).ClientMessage(ctx, signinflow.SignUpClosed())
	}

	return false, nil
}

// signUpStart is SignUp().Start().
func (a *Auth[S, U, Tx]) signUpStart() http.HandlerFunc {
	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		if refused, err := a.refuseSignUpClosed(ctx, w); refused {
			return err
		}
		req, err := emailDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := a.startSignUp(ctx, req.Email); err != nil {
			return writeSignInError(ctx, w, err)
		}

		return httpio.NewEncoder(w).Ok(struct{}{})
	})
}

// startSignUp mails email its sign-up link, or the notice that an account already
// holds it.
func (a *Auth[S, U, Tx]) startSignUp(ctx context.Context, email string) error {
	m, err := a.mailOf()
	if err != nil {
		return err
	}
	bare, err := address(email)
	if err != nil {
		return err
	}
	if err := a.limitMail(ctx, bare); err != nil {
		return err
	}
	if _, err := a.storage.UserByEmail(ctx, bare); err == nil {
		return a.send(ctx, m.accountExistsMessage(bare))
	} else if !httpio.HasNotFound(err) {
		return errors.Wrap(err, "sessionstorage.AuthStore.UserByEmail()")
	}
	token, hash, err := linkToken()
	if err != nil {
		return err
	}
	lifetime := m.settings.LinkTimeout
	link := &sessionstorage.MailLink{TokenHash: hash, Kind: sessionstorage.MailLinkSignUp, Email: bare, ExpiresAt: time.Now().Add(lifetime)}
	if err := a.storage.InsertMailLink(ctx, link); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.InsertMailLink()")
	}

	return a.send(ctx, m.signUpMessage(bare, token, lifetime))
}

// signUpOpen is SignUp().Open().
func (a *Auth[S, U, Tx]) signUpOpen() http.HandlerFunc {
	type response struct {
		Email           string `json:"email"`
		UsernameIsEmail bool   `json:"usernameIsEmail"`
	}

	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		methods := a.registered()
		if refused, err := a.refuseSignUpClosed(ctx, w); refused {
			return err
		}
		req, err := tokenDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		link, err := a.liveLink(ctx, req.Token, sessionstorage.MailLinkSignUp)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(response{Email: link.Email, UsernameIsEmail: methods.usernameIsEmail()})
	})
}

// signUpComplete is SignUp().Complete().
func (a *Auth[S, U, Tx]) signUpComplete() http.HandlerFunc {
	type request struct {
		Token    string `json:"token"`
		Password string `json:"password"`
		Username string `json:"username"`
	}
	decoder := sessioncore.NewDecoder[request]()

	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		if !a.registered().passwordRegistered() {
			return httpio.NewEncoder(w).ClientMessage(ctx, signinflow.SignUpClosed())
		}
		req, err := decoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		user, err := a.completeSignUp(ctx, req.Token, req.Username, req.Password)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		// The address the sign-up verified goes with the sign-in, as a password login
		// carries the account's own: an invitation the browser also carries is accepted
		// only where it was mailed to that address, and one mailed to another is refused
		// (invitation_address) and left standing for the person it invites.
		attempt, err := a.passwordAttempt(user)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		attempt.identity.Email, attempt.identity.EmailVerified = addressOf(user.Email), true
		outcome, err := a.signIn(ctx, w, attempt)
		if err != nil {
			return writeSignInError(ctx, w, err)
		}

		return a.answerSignIn(ctx, w, r, outcome)
	})
}

// completeSignUp creates the account a live sign-up link names, in one transaction
// that reads what an administrator chose for the password method: the method must be
// open to sign-up still, the link is consumed, the account is written, and the method's
// default role is given with it (the Auth's sign-up hook), or none of it happens. A
// link mailed while the method was open creates nothing once it is closed.
func (a *Auth[S, U, Tx]) completeSignUp(ctx context.Context, token, username, password string) (*sessionstorage.SessionUser, error) {
	link, err := a.liveLink(ctx, token, sessionstorage.MailLinkSignUp)
	if err != nil {
		return nil, err
	}
	insert, err := a.newAccount(link.Email, username, password, "")
	if err != nil {
		return nil, err
	}
	id, err := a.storage.SignUp(ctx, string(sessioninfo.MethodPassword), insert, link.ID, a.signUpSeated)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.SignUp()")
	}
	// The link reached the person: the address's mail count is reset.
	a.clearMailLimit(ctx, link.Email)

	// The hash goes with the user, so the sign-in that follows rests on the password the
	// account was just created with (passwordAttempt).
	return &sessionstorage.SessionUser{ID: id, Username: insert.Username, PasswordHash: insert.PasswordHash, Email: insert.Email}, nil
}
