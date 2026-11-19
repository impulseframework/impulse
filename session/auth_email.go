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
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/tracer"
)

// A user's email address is trusted for nothing until the person has proven they read
// it, and the user record carries the time that happened. An account's address changes
// in one way from either side: the new address confirms it, by a link mailed to it, and
// until then the account keeps the address it has and nothing is mailed to that one to
// ask. The person changes their own while signed in (Email().Add, Verify): the link
// counts only for the account that asked, so an address never attaches to an account
// its follower does not control. An administrator asks for the change of a person's
// (AuthAPI.RequestAddressChange), and the reader of the new address confirms it
// (Email().Confirm), signed in or not: the person whose mailbox was lost cannot sign in
// to confirm. Once an address is replaced, the one it replaced is told so
// (addressChangedMessage), which is the one message it gets. The address of a person
// who signs in through an organization's directory is the directory's to say and is
// changed from neither side (errDirectoryAddress): a new address in the directory is a
// new account. A Google sign-in writes the address Google verified, and the code a
// sign-in is held for writes the one it was mailed to. See "Users and their email
// address" in the README.

// EmailHandlers are the HTTP handlers of a user's email address.
type EmailHandlers struct {
	add     http.HandlerFunc
	verify  http.HandlerFunc
	confirm http.HandlerFunc
}

// Add accepts {"email"} from the signed-in user and mails that address a link to
// confirm it; nothing is written until the link is followed. It answers {} whatever
// the address's state, so the screen reveals nothing: an address another account
// holds is told so by mail, and an address the user already holds verified gets no
// mail. An address that is not one is 400; a mailbox sent five links within fifteen
// minutes and using none is refused with too_many_emails and the wait. Route it behind
// ValidateSession.
func (h *EmailHandlers) Add() http.HandlerFunc { return h.add }

// Verify accepts {"token"}, the token the mailed link carried, and writes the address
// on the signed-in user as verified, replacing whatever they held; when the password
// method declares the username as the email address, the user is renamed to it with
// their live sessions. It answers {"email"}. A token whose link is not the user's own,
// or is no longer live, is 404; an address another user has taken meanwhile is 409.
// Route it behind ValidateSession.
func (h *EmailHandlers) Verify() http.HandlerFunc { return h.verify }

// Confirm accepts {"token"}, the token of the link mailed to an address an
// administrator asked to put on an account, and writes that address on the account,
// confirmed, replacing the one it held, which is then told that it was replaced. It
// answers {"email"}. Whoever reads the new mailbox confirms, signed in or not: the link
// is what the administrator's request waits for. A token whose link is no longer live
// is 404, as is one for an account that has come to sign in through an organization's
// directory since; an address another user has taken meanwhile is 409. Route it beside
// the login routes, behind StartSession and ValidateXSRFToken.
func (h *EmailHandlers) Confirm() http.HandlerFunc {
	return h.confirm
}

// Email returns the email address handlers. It panics on an Auth without mail, as a
// wiring mistake.
func (a *Auth[S, U, Tx]) Email() *EmailHandlers {
	a.requireMail("Email()")

	return &EmailHandlers{
		add:     a.serving(a.emailAdd()),
		verify:  a.serving(a.emailVerify()),
		confirm: a.serving(a.emailConfirm()),
	}
}

// errDirectoryAddress is the refusal of a change to the address of a person who signs
// in through an organization's directory.
func errDirectoryAddress() error {
	return httpio.NewConflictMessage("This account signs in through an organization's directory, and its email address is the directory's: it is not changed here. " +
		"A new address in the directory is a new account")
}

// directoryPerson reports whether userID signs in through an organization's directory:
// an identity linked to the account came through one (signin.Directory.Organization). A
// personal account, which is no organization's, does not count.
func (a *Auth[S, U, Tx]) directoryPerson(ctx context.Context, userID impulse.UUID) (bool, error) {
	links, err := a.storage.IdentitiesByUser(ctx, userID)
	if err != nil {
		return false, errors.Wrap(err, "sessionstorage.AuthStore.IdentitiesByUser()")
	}
	for _, link := range links {
		if (signin.Directory{Method: link.Method, Connection: link.Connection}).Organization() {
			return true, nil
		}
	}

	return false, nil
}

// holdsAddress reports whether an account holds a confirmed address a message can be
// sent to. A confirmed value that is no address counts as none, as it does for the code
// of a first password (sendFirstPasswordCode): the simulated build's directories vouch
// for a person under their login name.
func holdsAddress(email *string, confirmed bool) bool {
	if email == nil || !confirmed {
		return false
	}
	_, err := address(*email)

	return err == nil
}

// refuseDirectoryAddress refuses a change of the address userID holds when the person
// signs in through an organization's directory. A directory person who holds no
// confirmed address a message can be sent to (holdsAddress) is given their first: that
// changes none.
func (a *Auth[S, U, Tx]) refuseDirectoryAddress(ctx context.Context, userID impulse.UUID, holds bool) error {
	if !holds {
		return nil
	}
	directory, err := a.directoryPerson(ctx, userID)
	if err != nil {
		return err
	}
	if directory {
		return errDirectoryAddress()
	}

	return nil
}

// noticeAddressChanged tells the address an account held, confirmed, that replacement
// took its place. It is the one message the old address gets about the change, and it
// asks nothing of it. An account that held no confirmed address a message can be sent to
// (holdsAddress) has nobody to tell. A message that could not be sent is logged: the
// change stands.
func (a *Auth[S, U, Tx]) noticeAddressChanged(ctx context.Context, old *string, wasConfirmed bool, replacement string) {
	if !holdsAddress(old, wasConfirmed) || strings.EqualFold(*old, replacement) {
		return
	}
	m, err := a.mailOf()
	if err != nil {
		return
	}
	if err := a.send(ctx, m.addressChangedMessage(*old, replacement)); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "the notice that an account's email address changed was not sent"))
	}
}

// emailRequest is a request that carries an address.
type emailRequest struct {
	Email string `json:"email"`
}

var emailDecoder = sessioncore.NewDecoder[emailRequest]()

// emailAdd is Email().Add().
func (a *Auth[S, U, Tx]) emailAdd() http.HandlerFunc {
	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.validateRequest(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := sessioncore.RefuseImpersonated(ctx, a.baseSession, "Email().Add()", true); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		req, err := emailDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := a.sendVerification(ctx, sessioninfo.UserFromCtx(ctx), req.Email); err != nil {
			return writeSignInError(ctx, w, err)
		}

		return httpio.NewEncoder(w).Ok(struct{}{})
	})
}

// sendVerification mails user's chosen address its verification link, or the notice
// that another account holds it; an address the user already holds verified gets
// nothing.
func (a *Auth[S, U, Tx]) sendVerification(ctx context.Context, user *sessioninfo.UserInfo, email string) error {
	m, err := a.mailOf()
	if err != nil {
		return err
	}
	bare, err := address(email)
	if err != nil {
		return err
	}
	if err := a.refuseDirectoryAddress(ctx, user.ID, holdsAddress(user.Email, user.EmailVerified())); err != nil {
		return err
	}
	if err := a.limitMail(ctx, bare); err != nil {
		return err
	}
	holder, err := a.storage.UserByEmail(ctx, bare)
	if err != nil && !httpio.HasNotFound(err) {
		return errors.Wrap(err, "sessionstorage.AuthStore.UserByEmail()")
	}
	switch {
	case holder != nil && holder.ID != user.ID:
		return a.send(ctx, m.addressHeldMessage(bare))
	case holder != nil && holder.EmailVerified():
		return nil
	}
	token, hash, err := linkToken()
	if err != nil {
		return err
	}
	lifetime := m.settings.LinkTimeout
	link := &sessionstorage.MailLink{
		TokenHash: hash, Kind: sessionstorage.MailLinkVerification, Email: bare, UserID: impulse.NullUUIDFromUUID(user.ID),
		ExpiresAt: time.Now().Add(lifetime),
	}
	if err := a.storage.InsertMailLink(ctx, link); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.InsertMailLink()")
	}

	return a.send(ctx, m.verificationMessage(bare, token, lifetime))
}

// emailVerify is Email().Verify().
func (a *Auth[S, U, Tx]) emailVerify() http.HandlerFunc {
	type response struct {
		Email string `json:"email"`
	}

	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.validateRequest(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := sessioncore.RefuseImpersonated(ctx, a.baseSession, "Email().Verify()", true); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		req, err := tokenDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		user := sessioninfo.UserFromCtx(ctx)
		link, err := a.liveLink(ctx, req.Token, sessionstorage.MailLinkVerification)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		// A link counts only for the account that asked for it.
		if !link.UserID.Valid || link.UserID.UUID != user.ID {
			return httpio.NewEncoder(w).ClientMessage(ctx, errLinkDead())
		}
		// A person who came to sign in through a directory since they asked keeps the
		// address they hold: the link stands unused and dies with its time.
		if err := a.refuseDirectoryAddress(ctx, user.ID, holdsAddress(user.Email, user.EmailVerified())); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := a.storage.ConsumeMailLink(ctx, link.TokenHash); err != nil {
			if httpio.HasNotFound(err) {
				err = errLinkDead()
			}

			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := a.storage.VerifyUserEmail(ctx, user.ID, link.Email); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		// The link reached the person: the address's mail count is reset.
		a.clearMailLimit(ctx, link.Email)
		logger.FromCtx(ctx).Infof("user %s verified the email address %s", user.ID, link.Email)
		// The address it replaced is told, and asked nothing.
		a.noticeAddressChanged(ctx, user.Email, user.EmailVerified(), link.Email)
		// Where the username is the address, the verification renamed the user: the
		// application moves what it keys by the old name.
		if a.registered().usernameIsEmail() && user.Username != link.Email && a.userRenamed != nil {
			if err := a.userRenamed(ctx, user.ID, user.Username, link.Email); err != nil {
				logger.FromCtx(ctx).Errorf("user %s was renamed from %q to %q by verifying the address, and the rename hook failed; run the hook's work by hand: %v", user.ID, user.Username, link.Email, err)

				return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "session.UserRenamedHook()"))
			}
		}

		return httpio.NewEncoder(w).Ok(response{Email: link.Email})
	})
}

// emailHint is the address a directory asserted at one of the user's sign-ins, for a
// user who holds no address: the prefill of the add-address step, trusted for
// nothing. It is "" when the user holds an address or no identity asserted one.
func (a *Auth[S, U, Tx]) emailHint(ctx context.Context, user *sessioninfo.UserInfo) string {
	if user == nil || user.ID.IsNil() || user.Email != nil {
		return ""
	}
	identities, err := a.storage.IdentitiesByUser(ctx, user.ID)
	if err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.IdentitiesByUser()"))

		return ""
	}
	for _, identity := range identities {
		if identity.EmailAtLink != nil && *identity.EmailAtLink != "" {
			return *identity.EmailAtLink
		}
	}

	return ""
}

// RequestAddressChange asks for the address of userID to be changed to email, as an
// administrator does: by says whose (AskedByTenant, AskedByApplication). Nothing about
// the account changes here. A link is mailed to the new address, and the account takes
// it, confirmed, when its reader follows the link (Email().Confirm()); until then the
// account keeps the address it has, every code and link still goes there, and nothing is
// mailed to it about the change. Once the new address has confirmed, the old one is told
// it was replaced.
//
// The address of a person who signs in through an organization's directory is refused a
// change (Conflict): it is the directory's. The call answers the same whether or not
// another account holds the new address, which is then told so by mail and sent no link,
// so the caller learns nothing about an address that is not their person's. An address
// the account already holds confirmed is nothing to change and mails nothing. A value
// that is no address is a BadRequest, and an address sent five links and codes within
// fifteen minutes and using none is refused with a *sessioninfo.LoginRefusal carrying
// RefusedTooManyEmails and the wait. Who may ask for whom is the caller's to decide
// before it calls. It needs mail (WithMail).
func (p *AuthAPI[S, U, Tx]) RequestAddressChange(ctx context.Context, userID impulse.UUID, email string, by AskedBy) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	a := p.auth
	m, err := a.mailOf()
	if err != nil {
		return err
	}
	bare, err := address(email)
	if err != nil {
		return err
	}
	user, err := a.storage.User(ctx, userID)
	if err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	confirmed := user.EmailVerified()
	if err := a.refuseDirectoryAddress(ctx, user.ID, holdsAddress(user.Email, confirmed)); err != nil {
		return err
	}
	if confirmed && strings.EqualFold(*user.Email, bare) {
		return nil
	}
	if err := a.limitMail(ctx, bare); err != nil {
		return err
	}
	// One change stands at a time: asking again, for a mistyped address put right,
	// withdraws the links asked for before.
	a.endLinks(ctx, sessionstorage.MailLinkAddressChange, user.ID)
	holder, err := a.storage.UserByEmail(ctx, bare)
	if err != nil && !httpio.HasNotFound(err) {
		return errors.Wrap(err, "sessionstorage.AuthStore.UserByEmail()")
	}
	if holder != nil && holder.ID != user.ID {
		return a.send(ctx, m.addressHeldMessage(bare))
	}
	token, hash, err := linkToken()
	if err != nil {
		return err
	}
	lifetime := m.settings.LinkTimeout
	link := &sessionstorage.MailLink{
		TokenHash: hash, Kind: sessionstorage.MailLinkAddressChange, Email: bare, UserID: impulse.NullUUIDFromUUID(user.ID), ExpiresAt: time.Now().Add(lifetime),
	}
	if err := a.storage.InsertMailLink(ctx, link); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.InsertMailLink()")
	}
	if err := a.send(ctx, m.addressChangeMessage(bare, token, lifetime, a.administratorOf(m, by))); err != nil {
		if rerr := a.storage.RevokeMailLink(ctx, link.ID); rerr != nil && !httpio.HasNotFound(rerr) {
			logger.FromCtx(ctx).Error(errors.Wrap(rerr, "sessionstorage.AuthStore.RevokeMailLink()"))
		}

		return err
	}

	return nil
}

// emailConfirm is Email().Confirm().
func (a *Auth[S, U, Tx]) emailConfirm() http.HandlerFunc {
	type response struct {
		Email string `json:"email"`
	}

	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		req, err := tokenDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		email, err := a.confirmAddressChange(ctx, req.Token)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(response{Email: email})
	})
}

// confirmAddressChange writes the address a live address-change link names on the
// account it names, confirmed, and tells the address it replaced. The link is consumed
// by a conditional update before the write, so it confirms once. An account that has
// come to sign in through an organization's directory since the change was asked for
// keeps its address, and the link is answered as a dead one is.
func (a *Auth[S, U, Tx]) confirmAddressChange(ctx context.Context, token string) (string, error) {
	link, err := a.liveLink(ctx, token, sessionstorage.MailLinkAddressChange)
	if err != nil {
		return "", err
	}
	if !link.UserID.Valid {
		return "", errLinkDead()
	}
	user, err := a.storage.User(ctx, link.UserID.UUID)
	if err != nil {
		if httpio.HasNotFound(err) {
			return "", errLinkDead()
		}

		return "", errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	confirmed := user.EmailVerified()
	if err := a.refuseDirectoryAddress(ctx, user.ID, holdsAddress(user.Email, confirmed)); err != nil {
		return "", errLinkDead()
	}
	// The link changes the address the account held when it was mailed: once the
	// account confirmed an address after that, by another link, by the person's own
	// change or by a sign-in code, the link is of a change that was overtaken.
	if confirmed && user.EmailVerifiedAt.After(link.CreatedAt) {
		return "", errLinkDead()
	}
	if err := a.storage.ConsumeMailLink(ctx, link.TokenHash); err != nil {
		if httpio.HasNotFound(err) {
			return "", errLinkDead()
		}

		return "", errors.Wrap(err, "sessionstorage.AuthStore.ConsumeMailLink()")
	}
	if err := a.storage.VerifyUserEmail(ctx, user.ID, link.Email); err != nil {
		return "", errors.Wrap(err, "sessionstorage.AuthStore.VerifyUserEmail()")
	}
	// The link reached its reader: the address's mail count is reset.
	a.clearMailLimit(ctx, link.Email)
	a.endLinks(ctx, sessionstorage.MailLinkAddressChange, user.ID)
	logger.FromCtx(ctx).Infof("the email address of user %s was changed to %s, confirmed from the link an administrator's request mailed", user.ID, link.Email)
	a.noticeAddressChanged(ctx, user.Email, confirmed, link.Email)
	// Where the username is the address, the confirmation renamed the user: the
	// application moves what it keys by the old name.
	if a.registered().usernameIsEmail() && !strings.EqualFold(user.Username, link.Email) && a.userRenamed != nil {
		if err := a.userRenamed(ctx, user.ID, user.Username, link.Email); err != nil {
			logger.FromCtx(ctx).Errorf("user %s was renamed from %q to %q by confirming the address, and the rename hook failed; run the hook's work by hand: %v", user.ID, user.Username, link.Email, err)

			return "", errors.Wrap(err, "session.UserRenamedHook()")
		}
	}

	return link.Email, nil
}

// UserByEmail returns the user holding the address, verified or not, matched without
// regard to case, or a NotFound error.
func (p *AuthAPI[S, U, Tx]) UserByEmail(ctx context.Context, email string) (*sessionstorage.SessionUser, error) {
	user, err := p.auth.storage.UserByEmail(ctx, email)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.UserByEmail()")
	}

	return user, nil
}
