package session

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// A password reset is how a person who holds a password gets back in without it: a link
// mailed to the address their account holds confirmed, which whoever follows it chooses
// a new password at. The link is the whole of it. Nothing about the account changes
// when it is mailed: the old password signs in until the link is used. The link works
// once and stands for MailSettings.LinkTimeout; using it writes the new password, ends
// every session of the person, as every change of a password does, and nothing else.
//
// Three callers ask for one. An administrator asks for a person (AuthAPI.SendPasswordReset),
// whatever the application chose for self-service, and the message says whose
// administrator asked. The person asks for themselves at the sign-in page
// (PasswordReset().Request()), and only where an administrator has turned that on: it
// is a setting of the running application, kept with the password method's row of the
// sign-in methods table (SelfServiceReset), read as each request arrives, and closed
// until someone opens it. A person who asks is answered the same whether or not the
// address has an account, and an address with none is mailed nothing.
//
// A link is mailed to a confirmed address alone, and only to an account that holds a
// password: an account whose person signs in through a directory and never chose a
// password has none to reset, and a link would hand whoever reads the mailbox a way in
// that the directory does not govern. A link counts only while the account still holds
// the address it was mailed to, confirmed, and still holds a password. Every link
// counts against the one limit on mail to an address (mailLimit), whoever asked for it.
// See "Password reset" in the README.

// PasswordResetHandlers are the HTTP handlers of a password reset.
type PasswordResetHandlers struct {
	request  http.HandlerFunc
	open     http.HandlerFunc
	complete http.HandlerFunc
}

// Request accepts {"email"} from a person at the sign-in page and mails that address a
// link to reset the password of the account that holds it confirmed. It answers {}
// whatever the address's state, and takes as long, so the screen reveals nothing: an
// address no account holds, one an account holds unconfirmed, and one whose account has
// no password are mailed nothing. An address that is not one is 400; a mailbox sent
// five links and codes within fifteen minutes and using none is refused with
// too_many_emails and the wait, whether or not it has an account; and every request is
// 404 while an administrator has not turned self-service reset on. Route it behind
// StartSession and ValidateXSRFToken.
func (h *PasswordResetHandlers) Request() http.HandlerFunc {
	return h.request
}

// Open accepts {"token"}, the token the mailed link carried, and answers {"username"}:
// the name the account signs in under, which the person who forgot their password may
// have forgotten too, and which following the link entitles them to. A token whose link
// is no longer live, or whose account no longer holds the address the link was mailed
// to or a password, is 404. It answers whatever self-service is set to, since an
// administrator's link is followed here too.
func (h *PasswordResetHandlers) Open() http.HandlerFunc {
	return h.open
}

// Complete accepts {"token", "password"}, writes the password as the account's and
// ends every session of the person, and answers {"username"}. The person is not signed
// in: they sign in with the new password as they would any day, so every rule of a
// sign-in is asked of them. The link is spent whether or not the write lands. A
// password under eight characters is 400 and spends nothing; a token whose link is no
// longer live is 404; a password that was changed or removed while the person was
// choosing is 409. Route it beside the password login, behind ValidateXSRFToken.
func (h *PasswordResetHandlers) Complete() http.HandlerFunc {
	return h.complete
}

// PasswordReset returns the password reset handlers. It panics on an Auth without mail,
// as a wiring mistake. Every application with the password method and mail routes them:
// whether a person may ask for their own link is an administrator's choice in the
// running application, read as each request arrives.
func (a *Auth[S, U, Tx]) PasswordReset() *PasswordResetHandlers {
	a.requireMail("PasswordReset()")

	return &PasswordResetHandlers{
		request:  a.serving(a.passwordResetRequest()),
		open:     a.serving(a.passwordResetOpen()),
		complete: a.serving(a.passwordResetComplete()),
	}
}

// selfServiceResetOpen reports whether a person may ask for their own reset link now:
// the password method is registered, and an administrator turned self-service reset on
// (its row of the sign-in methods table). A method with no row offers none.
func (a *Auth[S, U, Tx]) selfServiceResetOpen(ctx context.Context) (bool, error) {
	if !a.registered().passwordRegistered() {
		return false, nil
	}
	chosen, err := a.signInMethodRow(ctx, string(sessioninfo.MethodPassword))
	if err != nil {
		return false, err
	}

	return chosen.ResetsOwnPassword(), nil
}

// errSelfServiceResetClosed is the answer to a person's own request while self-service
// reset is turned off.
func errSelfServiceResetClosed() error {
	return httpio.NewNotFoundMessage("Resetting your own password is turned off here. Ask an administrator to send you a reset link")
}

// errNoPasswordToReset is the refusal of an administrator's link for an account that
// gets none. It says both causes in one sentence, so an administrator who may send the
// link without managing the account learns neither of the two for certain.
func errNoPasswordToReset() error {
	return httpio.NewConflictMessage("No link was sent: the account has no password to reset, or no confirmed email address to send the link to")
}

// passwordResetRequest is PasswordReset().Request().
func (a *Auth[S, U, Tx]) passwordResetRequest() http.HandlerFunc {
	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		open, err := a.selfServiceResetOpen(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if !open {
			return httpio.NewEncoder(w).ClientMessage(ctx, errSelfServiceResetClosed())
		}
		req, err := emailDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := a.requestOwnReset(ctx, req.Email); err != nil {
			return writeSignInError(ctx, w, err)
		}

		return httpio.NewEncoder(w).Ok(struct{}{})
	})
}

// requestOwnReset takes a person's own request for a reset link to email. The request
// takes its place under the address's mail limit first, and its refusal there is the
// one answer that is not {}: it is given whether or not the address has an account.
// Whether an account holds the address is then read, and the link mailed, once the
// request is answered (later), so neither the answer nor the time it takes says which
// it was. A failure there is logged: the person asks again.
func (a *Auth[S, U, Tx]) requestOwnReset(ctx context.Context, email string) error {
	if _, err := a.mailOf(); err != nil {
		return err
	}
	bare, err := address(email)
	if err != nil {
		return err
	}
	if err := a.limitMail(ctx, bare); err != nil {
		return err
	}
	a.later(ctx, func(ctx context.Context) {
		if err := a.mailOwnReset(ctx, bare); err != nil {
			logger.FromCtx(ctx).Error(errors.Wrap(err, "a password reset link a person asked for was not mailed"))
		}
	})

	return nil
}

// mailOwnReset mails the reset link a person asked for to email, when an account holds
// the address confirmed and holds a password; any other address is mailed nothing.
func (a *Auth[S, U, Tx]) mailOwnReset(ctx context.Context, email string) error {
	user, err := a.storage.UserByEmail(ctx, email)
	if err != nil {
		if httpio.HasNotFound(err) {
			return nil
		}

		return errors.Wrap(err, "sessionstorage.AuthStore.UserByEmail()")
	}
	if !resettable(user, email) {
		return nil
	}

	return a.mailReset(ctx, user, AskedByPerson())
}

// resettable reports whether a reset link mailed to email counts for user: the account
// holds a password to reset, and holds email as its confirmed address. It is asked when
// a link is mailed and again when it is followed, so a link mailed to an address the
// account has replaced since, or for a password the account no longer holds, is dead.
func resettable(user *sessionstorage.SessionUser, email string) bool {
	return user != nil && user.PasswordHash != nil && user.EmailVerified() && strings.EqualFold(*user.Email, email)
}

// mailReset stores a reset link for user and mails it to the address the account holds
// confirmed, saying who asked. A message that could not be sent withdraws the link, so
// none stands that nobody holds.
func (a *Auth[S, U, Tx]) mailReset(ctx context.Context, user *sessionstorage.SessionUser, by AskedBy) error {
	m, err := a.mailOf()
	if err != nil {
		return err
	}
	token, hash, err := linkToken()
	if err != nil {
		return err
	}
	email, lifetime := *user.Email, m.settings.LinkTimeout
	link := &sessionstorage.MailLink{
		TokenHash: hash, Kind: sessionstorage.MailLinkPasswordReset, Email: email, UserID: impulse.NullUUIDFromUUID(user.ID), ExpiresAt: time.Now().Add(lifetime),
	}
	if err := a.storage.InsertMailLink(ctx, link); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.InsertMailLink()")
	}
	if err := a.send(ctx, m.passwordResetMessage(email, token, lifetime, by, a.administratorOf(m, by))); err != nil {
		if rerr := a.storage.RevokeMailLink(ctx, link.ID); rerr != nil && !httpio.HasNotFound(rerr) {
			logger.FromCtx(ctx).Error(errors.Wrap(rerr, "sessionstorage.AuthStore.RevokeMailLink()"))
		}

		return err
	}

	return nil
}

// SendPasswordReset mails userID a link to reset their password, asked for by an
// administrator: by says whose (AskedByTenant, AskedByApplication), and the message
// says it. It does not read the self-service setting, which is about what a person may
// ask for themselves. Nothing about the account changes until the person uses the link.
// The link goes to the address the account holds confirmed, and to an account that
// holds a password: any other account is refused (Conflict), in one sentence that names
// both causes. An address sent five links and codes within fifteen minutes and using
// none is refused with a *sessioninfo.LoginRefusal carrying RefusedTooManyEmails and
// the wait (sessioninfo.RetryAfterOf), whoever asked for them. Who may ask for whom is
// the caller's to decide before it calls. It needs mail (WithMail) and the password
// method.
func (p *AuthAPI[S, U, Tx]) SendPasswordReset(ctx context.Context, userID impulse.UUID, by AskedBy) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	a := p.auth
	if _, err := a.mailOf(); err != nil {
		return err
	}
	if !a.registered().passwordRegistered() {
		return httpio.NewConflictMessage("Nobody signs in to this application with a password, so there is none to reset")
	}
	user, err := a.storage.User(ctx, userID)
	if err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	if !user.EmailVerified() || !resettable(user, *user.Email) {
		return errNoPasswordToReset()
	}
	if err := a.limitMail(ctx, *user.Email); err != nil {
		return err
	}

	return a.mailReset(ctx, user, by)
}

// resetLink reads the live reset link token names and the account it is for, and
// answers NotFound, as a dead link is, for a link whose account is gone or no longer
// holds the address the link was mailed to, confirmed, and a password.
func (a *Auth[S, U, Tx]) resetLink(ctx context.Context, token string) (link *sessionstorage.MailLink, user *sessionstorage.SessionUser, err error) {
	link, err = a.liveLink(ctx, token, sessionstorage.MailLinkPasswordReset)
	if err != nil {
		return nil, nil, err
	}
	if !link.UserID.Valid {
		return nil, nil, errLinkDead()
	}
	user, err = a.storage.User(ctx, link.UserID.UUID)
	if err != nil {
		if httpio.HasNotFound(err) {
			return nil, nil, errLinkDead()
		}

		return nil, nil, errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	if !resettable(user, link.Email) {
		return nil, nil, errLinkDead()
	}

	return link, user, nil
}

// passwordResetAnswer is what Open and Complete answer: the name the account signs in
// under.
type passwordResetAnswer struct {
	Username string `json:"username"`
}

// passwordResetOpen is PasswordReset().Open().
func (a *Auth[S, U, Tx]) passwordResetOpen() http.HandlerFunc {
	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		req, err := tokenDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		_, user, err := a.resetLink(ctx, req.Token)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(passwordResetAnswer{Username: user.Username})
	})
}

// passwordResetComplete is PasswordReset().Complete().
func (a *Auth[S, U, Tx]) passwordResetComplete() http.HandlerFunc {
	type request struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	decoder := sessioncore.NewDecoder[request]()

	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		req, err := decoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		user, err := a.completeReset(ctx, req.Token, req.Password)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(passwordResetAnswer{Username: user.Username})
	})
}

// completeReset writes the password a person chose at a reset link. The password is
// held to the rule every password is before anything is spent. The link is then
// consumed by a conditional update, so of two requests with one token one goes on, and
// the store writes the password, clears the password change mark and ends every session
// of the person in one transaction, only while the account still holds the password it
// held when the link was followed: one changed or removed in that moment stands as it
// is. The name's failed sign-ins and the address's mail count are reset, and the other
// reset links that stand for the account are withdrawn: the person has their new
// password, so no older link is left to choose another with.
func (a *Auth[S, U, Tx]) completeReset(ctx context.Context, token, password string) (*sessionstorage.SessionUser, error) {
	if err := acceptedPassword(password); err != nil {
		return nil, err
	}
	link, user, err := a.resetLink(ctx, token)
	if err != nil {
		return nil, err
	}
	hash, err := a.hashToSet(password)
	if err != nil {
		return nil, err
	}
	if err := a.storage.ConsumeMailLink(ctx, link.TokenHash); err != nil {
		if httpio.HasNotFound(err) {
			return nil, errLinkDead()
		}

		return nil, errors.Wrap(err, "sessionstorage.AuthStore.ConsumeMailLink()")
	}
	if err := a.storage.ChangeUserPassword(ctx, user.ID, user.PasswordHash, hash); err != nil {
		if errors.Is(err, sessionstorage.ErrPasswordChanged) {
			return nil, httpio.NewConflictMessageWithError(err, "Your password was changed while you were choosing a new one, so this link did not change it. Sign in with your password, or ask for a new link")
		}

		return nil, errors.Wrap(err, "sessionstorage.AuthStore.ChangeUserPassword()")
	}
	a.clearAttempts(ctx, user.Username)
	// The link reached the person: the address's mail count is reset.
	a.clearMailLimit(ctx, link.Email)
	a.endResetLinks(ctx, user.ID)
	logger.FromCtx(ctx).Infof("user %s chose a new password from a reset link", user.ID)

	return user, nil
}

// endResetLinks withdraws the reset links that stand for userID. It is bookkeeping after
// the password is written: a failure is logged, and the links that stay die with their
// time.
func (a *Auth[S, U, Tx]) endResetLinks(ctx context.Context, userID impulse.UUID) {
	links, err := a.storage.LiveMailLinks(ctx, sessionstorage.MailLinkPasswordReset)
	if err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.LiveMailLinks()"))

		return
	}
	for _, link := range links {
		if !link.UserID.Valid || link.UserID.UUID != userID {
			continue
		}
		if err := a.storage.RevokeMailLink(ctx, link.ID); err != nil && !httpio.HasNotFound(err) {
			logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.RevokeMailLink()"))
		}
	}
}

// later runs work once the request that asked for it has been answered, on a context
// that outlives the request. It is for work whose outcome the answer must not carry and
// whose time the answer must not take: what a person's own request for a reset link
// does once it is counted. WaitForMail waits for it.
func (a *Auth[S, U, Tx]) later(ctx context.Context, work func(ctx context.Context)) {
	ctx = context.WithoutCancel(ctx)
	a.background.Go(func() {
		work(ctx)
	})
}

// WaitForMail returns once the mail the Auth is sending in the background has been
// handed to the sender: the reset links people asked for a moment ago, which are mailed
// after their request is answered. An application calls it as it shuts down, after it
// stopped serving, so no link a person was promised is lost; a test calls it before it
// reads what was mailed.
func (p *AuthAPI[S, U, Tx]) WaitForMail() {
	p.auth.background.Wait()
}
