package session

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/session/internal/attempts"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// A person's first password. A person who has only ever signed in through a directory
// holds no password, and the change of a password asks for the old one, so they could
// never give themselves one. FirstPassword() is how they do: signed in, they ask for a
// code, which is mailed to the address their account holds confirmed, and they choose
// the password with the code. The code ties the password to that mailbox: whoever asks
// must read it to finish, and a code counts only while the address it was mailed to is
// still the one the account holds confirmed. It is as strong as that address is, which
// a signed-in person replaces with a link mailed to the new one (Email()). It is the
// only way to a first password a person has; an account that holds
// a password changes it with the old one (PasswordMethod.ChangeUserPassword), and one
// who has lost theirs follows a reset link (AuthAPI.SendPasswordReset). See "A person's
// first password" in the README.

// firstPasswordAttempts bounds the wrong codes entered for an account's first password:
// five within fifteen minutes end the code that stands and hold the account's first
// password for fifteen minutes, whatever codes are asked for meanwhile. It is kept in
// the sign-in attempts table under the account (firstPasswordKey), as a name's failed
// passwords are, so a session of the person guesses a code no faster than a stranger
// guesses a password at the login.
var firstPasswordAttempts = sessionstorage.SignInAttemptLimit{Failures: emailCodeAttempts, Window: 15 * time.Minute, Lock: 15 * time.Minute}

// firstPasswordKey is the account's key in the sign-in attempts table, under a kind no
// name a person signs in under can spell (attempts.OwnKey).
func firstPasswordKey(userID impulse.UUID) string {
	return attempts.OwnKey(attempts.KindFirstPassword, userID.String())
}

// hashFirstPasswordCode is the hash a first-password code's row keeps where a link's
// row keeps its token's: the code under the account it was mailed for and the instant
// it stops being accepted, so a hash tells nothing about another account's code and no
// two rows of one account hold the same hash.
func hashFirstPasswordCode(userID impulse.UUID, expiresAt time.Time, code string) []byte {
	sum := sha256.Sum256([]byte("first-password:" + userID.String() + ":" + strconv.FormatInt(expiresAt.UnixMicro(), 10) + ":" + code))

	return sum[:]
}

// FirstPasswordHandlers are the HTTP handlers of a person's first password.
type FirstPasswordHandlers struct {
	sendCode http.HandlerFunc
	set      http.HandlerFunc
}

// SendCode takes no body. It mails the signed-in user, who holds no password, a code at
// the address their account holds confirmed, replacing the code that stood, and
// answers {"codeEmail", "codeExpiresAt"}: the address masked and when the code stops
// being accepted. It is refused (409) for an account that holds a password, which is
// changed with the old one; for an account with no confirmed address, since the code
// has nowhere to go that the person has shown is theirs (they add and confirm one
// first, Email()); and on an Auth with no password method. A mailbox sent five codes
// within fifteen minutes and using none is refused with too_many_emails and the wait,
// and an account held after five wrong codes with too_many_attempts and the wait. It
// is refused in an impersonated session. Route it behind ValidateSession.
func (h *FirstPasswordHandlers) SendCode() http.HandlerFunc {
	return h.sendCode
}

// Set accepts {"code", "password"} from the signed-in user who holds no password and
// writes the password, with the code SendCode mailed. The password is held to the
// judgment a sign-up's is (400 when the judgment refuses it). A code that is not the
// one sent is 400, and the fifth wrong one within fifteen minutes ends the code and
// holds the account's first password for fifteen minutes (429, too_many_attempts and
// the wait); a code entered when none stands, because none was sent or the one sent
// expired, was used or was ended, is 409. It is refused (409) for an account that holds
// a password and on an Auth with no password method, and in an impersonated session.
// No session of the user ends, this one among them; where the Auth records auth
// events, this session records a password event beside the ones it had. It answers {}.
// Route it behind ValidateSession.
func (h *FirstPasswordHandlers) Set() http.HandlerFunc {
	return h.set
}

// FirstPassword returns the handlers of a person's first password. On an Auth with no
// password method the handlers refuse every request: there is no password to choose.
func (a *Auth[S, U, Tx]) FirstPassword() *FirstPasswordHandlers {
	return &FirstPasswordHandlers{
		sendCode: a.serving(a.firstPasswordSendCode()),
		set:      a.serving(a.firstPasswordSet()),
	}
}

// firstPasswordCodeAnswer is what SendCode answers: where the code went, masked, and
// when it stops being accepted.
type firstPasswordCodeAnswer struct {
	CodeEmail     string    `json:"codeEmail"`
	CodeExpiresAt time.Time `json:"codeExpiresAt"`
}

// firstPasswordSendCode is FirstPassword().SendCode().
func (a *Auth[S, U, Tx]) firstPasswordSendCode() http.HandlerFunc {
	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.validateRequest(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := sessioncore.RefuseImpersonated(ctx, a.baseSession, "FirstPassword().SendCode()", true); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		answer, err := a.sendFirstPasswordCode(ctx, sessioninfo.UserFromCtx(ctx).ID)
		if err != nil {
			return writeSignInError(ctx, w, err)
		}

		return httpio.NewEncoder(w).Ok(answer)
	})
}

// firstPasswordSet is FirstPassword().Set().
func (a *Auth[S, U, Tx]) firstPasswordSet() http.HandlerFunc {
	type request struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	decoder := sessioncore.NewDecoder[request]()

	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.validateRequest(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := sessioncore.RefuseImpersonated(ctx, a.baseSession, "FirstPassword().Set()", true); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		req, err := decoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := a.setFirstPassword(ctx, sessioninfo.UserFromCtx(ctx).ID, req.Code, req.Password); err != nil {
			return writeSignInError(ctx, w, err)
		}

		return httpio.NewEncoder(w).Ok(struct{}{})
	})
}

// errNoPasswordMethod is the refusal of a first password on an Auth that signs nobody
// in with a password.
func errNoPasswordMethod() error {
	return httpio.NewConflictMessage("Nobody signs in to this application with a password, so there is none to choose")
}

// errPasswordHeld is the refusal of a first password for an account that holds one.
func errPasswordHeld() error {
	return httpio.NewConflictMessage("Your account already has a password. To change it, enter the one you have and the new one")
}

// errNoConfirmedAddress is the refusal of a first password's code for an account with
// no confirmed address, or with one no message can be sent to.
func errNoConfirmedAddress() error {
	return httpio.NewConflictMessage("Your account has no confirmed email address, and the code to choose a password is sent to one. " +
		"Add your email address, confirm it from the link we send you, then choose your password")
}

// errTooManyFirstPasswordCodes is the refusal of an account whose first password is
// held after too many wrong codes: too_many_attempts and the wait, in words that say a
// code was what was wrong and when to ask for another. The wait is said in whole
// minutes, rounded up, so the person is never told to come back too soon.
func errTooManyFirstPasswordCodes(wait time.Duration) error {
	minutes := int((wait + time.Minute - 1) / time.Minute)

	return sessioninfo.NewTooManyAttemptsRefusalSaying(wait, "Too many wrong codes were entered. Ask for a new code in "+count(minutes, "minute"))
}

// errFirstPasswordCodeDead is the refusal of a code entered when none stands.
func errFirstPasswordCodeDead() error {
	return httpio.NewConflictMessage("That code has expired or was already used. Ask for a new one")
}

// firstPasswordUser reads the account a first password is asked for and refuses the
// ones that get none: every account of an Auth with no password method, and an account
// that holds a password.
func (a *Auth[S, U, Tx]) firstPasswordUser(ctx context.Context, userID impulse.UUID) (*sessionstorage.SessionUser, error) {
	if !a.registered().passwordRegistered() {
		return nil, errNoPasswordMethod()
	}
	user, err := a.storage.User(ctx, userID)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	if user.PasswordHash != nil {
		return nil, errPasswordHeld()
	}

	return user, nil
}

// firstPasswordHeld refuses a request for an account whose first password is held after
// too many wrong codes, with too_many_attempts and the wait.
func (a *Auth[S, U, Tx]) firstPasswordHeld(ctx context.Context, userID impulse.UUID) error {
	record, err := a.storage.SignInAttempts(ctx, firstPasswordKey(userID))
	if err != nil && !httpio.HasNotFound(err) {
		return errors.Wrap(err, "sessionstorage.AuthStore.SignInAttempts()")
	}
	if wait, locked := record.Locked(time.Now()); locked {
		return errTooManyFirstPasswordCodes(wait)
	}

	return nil
}

// firstPasswordCodes lists the codes that stand for userID's first password, oldest
// first: one, unless two were asked for at once.
func (a *Auth[S, U, Tx]) firstPasswordCodes(ctx context.Context, userID impulse.UUID) ([]*sessionstorage.MailLink, error) {
	links, err := a.storage.LiveMailLinks(ctx, sessionstorage.MailLinkFirstPassword)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.LiveMailLinks()")
	}
	var standing []*sessionstorage.MailLink
	for _, link := range links {
		if link.UserID.Valid && link.UserID.UUID == userID {
			standing = append(standing, link)
		}
	}

	return standing, nil
}

// endFirstPasswordCodes ends the codes that stand for userID's first password. A code
// that ended meanwhile is no failure.
func (a *Auth[S, U, Tx]) endFirstPasswordCodes(ctx context.Context, codes []*sessionstorage.MailLink) error {
	for _, code := range codes {
		if err := a.storage.RevokeMailLink(ctx, code.ID); err != nil && !httpio.HasNotFound(err) {
			return errors.Wrap(err, "sessionstorage.AuthStore.RevokeMailLink()")
		}
	}

	return nil
}

// sendFirstPasswordCode mails userID the code of their first password at the address
// the account holds confirmed, and records it in place of the code that stood. The
// address's mail is limited as a link's is.
func (a *Auth[S, U, Tx]) sendFirstPasswordCode(ctx context.Context, userID impulse.UUID) (*firstPasswordCodeAnswer, error) {
	m := a.settings.mail
	user, err := a.firstPasswordUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !user.EmailVerified() {
		return nil, errNoConfirmedAddress()
	}
	email := *user.Email
	// An address no message can be sent to is no address for the code: the simulated
	// build's Google directory vouches for a person under their login name. The account is
	// told what one with no confirmed address is, which says how to add one.
	if _, err := address(email); err != nil {
		return nil, errNoConfirmedAddress()
	}
	if err := a.firstPasswordHeld(ctx, user.ID); err != nil {
		return nil, err
	}
	if err := a.limitMail(ctx, email); err != nil {
		return nil, err
	}
	standing, err := a.firstPasswordCodes(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if err := a.endFirstPasswordCodes(ctx, standing); err != nil {
		return nil, err
	}
	code, err := newEmailCode()
	if err != nil {
		return nil, err
	}
	lifetime := a.settings.factors.codeTimeout
	// The hash names the instant the code expires, so the instant is one both databases
	// keep whole.
	expiresAt := time.Now().Add(lifetime).Truncate(time.Microsecond)
	row := &sessionstorage.MailLink{
		TokenHash: hashFirstPasswordCode(user.ID, expiresAt, code), Kind: sessionstorage.MailLinkFirstPassword, Email: email,
		UserID: impulse.NullUUIDFromUUID(user.ID), ExpiresAt: expiresAt,
	}
	if err := a.storage.InsertMailLink(ctx, row); err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.InsertMailLink()")
	}
	if err := a.send(ctx, m.firstPasswordMessage(email, code, lifetime)); err != nil {
		if rerr := a.storage.FailMailLink(ctx, row.ID); rerr != nil && !httpio.HasNotFound(rerr) {
			logger.FromCtx(ctx).Error(errors.Wrap(rerr, "sessionstorage.AuthStore.FailMailLink()"))
		}

		return nil, err
	}

	return &firstPasswordCodeAnswer{CodeEmail: maskAddress(email), CodeExpiresAt: expiresAt}, nil
}

// setFirstPassword gives userID the password they chose, on the code mailed for it. The
// account is read again and held to what the code's request was held to, a confirmed
// address among it, the password to the rule a sign-up's is, and the code is ended
// before the password is written, so one code writes one password; the store writes
// only while the account still holds none.
func (a *Auth[S, U, Tx]) setFirstPassword(ctx context.Context, userID impulse.UUID, code, password string) error {
	user, err := a.firstPasswordUser(ctx, userID)
	if err != nil {
		return err
	}
	if !user.EmailVerified() {
		return errNoConfirmedAddress()
	}
	who := detailsOf(user)
	if err := a.strongEnough(password, who); err != nil {
		return err
	}
	if err := a.firstPasswordHeld(ctx, user.ID); err != nil {
		return err
	}
	standing, err := a.proveFirstPasswordCode(ctx, user.ID, *user.Email, code)
	if err != nil {
		return err
	}
	hash, err := a.hashToSet(password, who)
	if err != nil {
		return err
	}
	if err := a.storage.SetFirstUserPassword(ctx, user.ID, hash); err != nil {
		if errors.Is(err, sessionstorage.ErrPasswordHeld) {
			return errPasswordHeld()
		}

		return errors.Wrap(err, "sessionstorage.AuthStore.SetFirstUserPassword()")
	}
	// A reset link mailed for a password the account held before does not reset this one.
	a.endResetLinks(ctx, user.ID)
	// The code reached the person and the password is theirs: what was counted against
	// the mailbox and the name's passwords counts against nothing, as the account's
	// codes do since the code was proven.
	a.clearMailLimit(ctx, standing.Email)
	a.clearAttempts(ctx, user.Username)
	a.recordFirstPassword(ctx, user)

	return nil
}

// proveFirstPasswordCode checks code against the one that stands for userID's first
// password and ends it, returning the code's row. A wrong code counts against the
// account (firstPasswordAttempts), and the one that reaches the limit ends the code
// that stands and is answered with too_many_attempts and the wait. A code entered while
// none stands is errFirstPasswordCodeDead, and so is one that another request used
// first. A guess takes its attempt before it is judged, in the store's one transaction
// (dbtype.SignInAttempts.Counted), so guesses made at once are each counted and no more
// than the limit are ever compared with the code; the right code's attempt is given
// back. A code counts for the mailbox it was sent to: confirmed is the address the
// account holds confirmed now, and a code mailed to another, one the account held when
// it asked and has replaced since, is ended and dead, whatever was entered.
func (a *Auth[S, U, Tx]) proveFirstPasswordCode(ctx context.Context, userID impulse.UUID, confirmed, code string) (*sessionstorage.MailLink, error) {
	codes, err := a.firstPasswordCodes(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(codes) == 0 {
		return nil, errFirstPasswordCodeDead()
	}
	// The newest is the one the person was sent last.
	standing := codes[len(codes)-1]
	if !sameAddress(standing.Email, confirmed) {
		if err := a.endFirstPasswordCodes(ctx, codes); err != nil {
			return nil, err
		}

		return nil, errFirstPasswordCodeDead()
	}
	// The guess takes its attempt before it is judged: the store counts it against the
	// account in one transaction and says whether it was counted, and a guess that was
	// not, because the limit was reached already, is not compared with the code.
	record, err := a.storage.RecordSignInFailure(ctx, firstPasswordKey(userID), firstPasswordAttempts)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.RecordSignInFailure()")
	}
	wait, locked := record.Locked(time.Now())
	if !record.Counted {
		return nil, errTooManyFirstPasswordCodes(wait)
	}
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if subtle.ConstantTimeCompare(standing.TokenHash, hashFirstPasswordCode(userID, standing.ExpiresAt, code)) != 1 {
		if locked {
			if err := a.endFirstPasswordCodes(ctx, codes); err != nil {
				return nil, err
			}

			return nil, errTooManyFirstPasswordCodes(wait)
		}

		return nil, httpio.NewBadRequestMessage("That is not the code we sent. Check it and try again")
	}
	if err := a.storage.RevokeMailLink(ctx, standing.ID); err != nil {
		if httpio.HasNotFound(err) {
			return nil, errFirstPasswordCodeDead()
		}

		return nil, errors.Wrap(err, "sessionstorage.AuthStore.RevokeMailLink()")
	}
	// The attempt the right code took is given back with the wrong ones before it:
	// nothing is held against an account for a code its person entered.
	if err := a.storage.ClearSignInAttempts(ctx, firstPasswordKey(userID)); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.ClearSignInAttempts()"))
	}

	return standing, nil
}

// recordFirstPassword records that user chose their first password in the session of
// the request: a password event on it, beside the events it had, where the Auth records
// auth events. It is bookkeeping after the write: a failure is logged, and the password
// stands.
func (a *Auth[S, U, Tx]) recordFirstPassword(ctx context.Context, user *sessionstorage.SessionUser) {
	logger.FromCtx(ctx).Infof("user %s chose their first password", user.ID)
	current, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	if !ok || current == nil || current.SessionInfo == nil {
		return
	}
	err := a.storage.AppendAuthEvent(ctx, current.ID, &sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword})
	if err != nil && !errors.Is(err, sessionstorage.ErrNoAuthEvents) {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.AppendAuthEvent()"))
	}
}
