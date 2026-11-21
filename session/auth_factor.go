package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"maps"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/session/cookie"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// The second factor the library runs. A sign-in the policy holds for MFA
// (signin.RequireMFA) waits as a pending identity; when the policy accepts the
// library's email code (no factor named, or sessioninfo.MethodEmailCode among them),
// the pending handlers mail the user a one-time code and complete the sign-in when the
// person enters it, recording an email-code event. A sign-in whose connection attests
// to a second factor (sessioninfo.Attestation), or whose browser is a trusted device of
// the user, is never held: the attestation or the trusted-device event stands in for
// the step.
//
// The code goes to the address the account holds, which is its confirmed address from
// the first code on: nothing puts an address on an account that holds a confirmed one
// but its own reader, by a link mailed to it (Email()), so a code is never sent where an
// administrator, or anyone but the person, pointed it. An account that has confirmed no
// address yet confirms one at this step, and it is the one way such an account is mailed
// anything: the code goes to the address it was created with, or to the one the person
// gives here when it has none (EnrollEmail), and entering the code writes the address on
// the account, confirmed. A person who comes through a directory that does not vouch for
// their address is held for this step at their first sign-in whatever the policy asks
// (signinflow.addressHold). An invitation's link only opens the invitation, so its
// acceptance is held here wherever the sign-in does not itself prove the invited
// address, and the code goes to the invited address and to no other (codeDestination).
// Two such waits name no account, and nobody is made, linked or changed before the code
// is entered: an invitation accepted through an identity that asserts no address, or
// one its method did not verify, and a sign-up with a password through an invitation
// (invitationSignUp), whose account is made once the code is entered. The third names
// its account: a password account, or one the application signs in, that holds no
// address. The code of such a wait counts for that invitation's acceptance, and the
// codes sent for it are counted under the invitation and no address
// (invitationCodeLimitKey). An administrator's reset (AuthAPI.ResetFactors) forgets the person's trusted
// devices and tells the person; with the email code there is nothing else to reset, and
// a lost mailbox is answered by an administrator asking for the address to be changed,
// which the new address confirms (AuthAPI.RequestAddressChange).
// See "The second factor" in the README.

// factorSettings are the settings of the library's second factor.
type factorSettings struct {
	// codeTimeout is how long a mailed code is accepted.
	codeTimeout time.Duration
	// trustedPeriod is how long a remembered browser stands in for the factor.
	trustedPeriod time.Duration
	// trustedCookie names the cookie that carries the trusted device's token.
	trustedCookie string
}

// The defaults of the second factor.
const (
	defaultEmailCodeTimeout    = 10 * time.Minute
	defaultTrustedDevicePeriod = 30 * 24 * time.Hour
	defaultTrustedDeviceCookie = "auth-trusted"
	// emailCodeLength is the digits of a code: six, a million codes, guessed at most
	// emailCodeAttempts times before the code dies.
	emailCodeLength = 6
	// emailCodeAttempts is how many guesses the code sent is given: the wrong code that
	// takes the last one kills it, and a guess past them is not judged.
	emailCodeAttempts = 5
	// trustedTokenSize is the trusted device token's size in bytes.
	trustedTokenSize = 32
)

var defaultFactorSettings = factorSettings{codeTimeout: defaultEmailCodeTimeout, trustedPeriod: defaultTrustedDevicePeriod, trustedCookie: defaultTrustedDeviceCookie}

// validate refuses settings the factor cannot run with.
func (f *factorSettings) validate() error {
	if f.codeTimeout <= 0 {
		return errors.New("WithEmailCodeTimeout: the timeout must be positive")
	}
	if f.trustedPeriod <= 0 {
		return errors.New("WithTrustedDevicePeriod: the period must be positive")
	}

	return nil
}

// WithEmailCodeTimeout sets how long a mailed sign-in code is accepted. (default: 10m)
func WithEmailCodeTimeout(d time.Duration) AuthOption {
	return authOption{apply: func(s *authSettings) { s.factors.codeTimeout = d }}
}

// WithTrustedDevicePeriod sets how long a browser the person asked to be remembered
// after a second factor stands in for it. (default: 30 days)
func WithTrustedDevicePeriod(d time.Duration) AuthOption {
	return authOption{apply: func(s *authSettings) { s.factors.trustedPeriod = d }}
}

// WithTrustedDeviceCookieName sets the name of the cookie that carries a trusted
// device's token. (default: "auth-trusted")
func WithTrustedDeviceCookieName(name string) AuthOption {
	return authOption{apply: func(s *authSettings) { s.factors.trustedCookie = name }}
}

// offersEmailCode reports whether a RequireMFA answer naming factors takes the
// library's email code: no factor named, or the code among them.
func offersEmailCode(factors []sessioninfo.AuthMethod) bool {
	return len(factors) == 0 || slices.Contains(factors, sessioninfo.MethodEmailCode)
}

// emailCodeAnswer is the email code's state as the pending status reports it.
type emailCodeAnswer struct {
	// CodeEmail is the masked address the code goes to, "" when the user holds none.
	CodeEmail string `json:"codeEmail,omitempty"`
	// EnrollEmail says the user holds no address: the page asks for one and posts it
	// to EnrollEmail().
	EnrollEmail bool `json:"enrollEmail,omitempty"`
	// CodeSent says a code stands, and CodeExpiresAt when it stops being accepted.
	CodeSent      bool       `json:"codeSent"`
	CodeExpiresAt *time.Time `json:"codeExpiresAt,omitempty"`
}

// emailCodeState is the email code's state for the pending MFA wait p.
func (a *Auth[S, U, Tx]) emailCodeState(ctx context.Context, p *pendingState) (*emailCodeAnswer, error) {
	code, err := a.storage.FactorCode(ctx, p.ID)
	if err != nil && !httpio.HasNotFound(err) {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.FactorCode()")
	}
	answer := &emailCodeAnswer{}
	// A code whose attempts are spent does not stand, though its row is live until the
	// guess that took the last one consumes it: no guess is judged against it any more
	// (verifyEmailCode), so the page asks for a new one, as it does where that guess's
	// request ended before it consumed the code. Neither does a code mailed to an
	// address the account no longer holds (codeStands): the page offers a new one at
	// the address the account holds now.
	if code != nil && code.Attempts < emailCodeAttempts {
		stands, err := a.standingCodeStands(ctx, p, code.Email)
		if err != nil {
			return nil, err
		}
		if stands {
			answer.CodeSent, answer.CodeExpiresAt, answer.CodeEmail = true, &code.ExpiresAt, maskAddress(code.Email)

			return answer, nil
		}
	}
	email, _, err := a.codeDestination(ctx, p)
	if err != nil {
		return nil, err
	}
	if email == "" {
		answer.EnrollEmail = true
	} else {
		answer.CodeEmail = maskAddress(email)
	}

	return answer, nil
}

// waitsUser is the user the pending MFA wait p names, as the store holds them now.
func (a *Auth[S, U, Tx]) waitsUser(ctx context.Context, p *pendingState) (*sessionstorage.SessionUser, error) {
	if !p.UserID.Valid {
		return nil, errors.New("a pending MFA wait names its user")
	}
	user, err := a.storage.User(ctx, p.UserID.UUID)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}

	return user, nil
}

// codeDestination is the address the pending MFA wait's code goes to, "" for none, and
// the invitation the code accepts, nil where the code is the account's own.
//
// An account that holds an address is sent its code there: its confirmed address, or,
// for an account that has confirmed none yet, the address it was created with, which
// this code confirms. The code is the account's own, counted with its sign-in codes.
//
// A code that goes to an invited address is the invitation's: the address of the
// invitation the browser carries, mailed there and nowhere else, with nobody typing an
// address for it. A wait that names no user is always such a wait: the acceptance of an
// invitation through a directory that asserts no address, or one it did not verify
// (signinflow's invitedCodeWait), or a sign-up with a password through one
// (invitationSignUp), both held before anybody is made or linked. A browser of such a
// wait that no longer carries a live invitation has nothing to wait for, and is
// answered as a wait that ran out is. An account that holds no address, in a browser
// that carries a live invitation, is sent the invitation's code too: the link alone
// does not accept, and the code is what shows the invited address to be the person's
// (signinflow's namedCodeWait).
//
// An account that holds no address in a browser that carries no invitation has no
// destination: the person gives an address (enrollEmail).
func (a *Auth[S, U, Tx]) codeDestination(ctx context.Context, p *pendingState) (email string, inv *sessioninfo.Invitation, err error) {
	if p.UserID.Valid {
		user, err := a.waitsUser(ctx, p)
		if err != nil {
			return "", nil, err
		}
		if user.Email != nil {
			return *user.Email, nil, nil
		}
	}
	inv, err = a.invitationOf(ctx)
	if err != nil {
		return "", nil, err
	}
	if inv == nil {
		if !p.UserID.Valid {
			return "", nil, errPendingExpired()
		}

		return "", nil, nil
	}

	return inv.Email, inv, nil
}

// standingCodeStands reports whether a code mailed to email is still good for the
// pending MFA wait p. The code of a wait that names its user is held to the address
// the account holds now (codeStands). A wait that names none is an invitation's
// acceptance, held before anybody is made or linked (codeDestination): there is no
// account whose address its code could be held to. Its code was mailed to the invited
// address, and counts for that invitation's acceptance alone, which the sign-in that
// carries the code holds it to.
func (a *Auth[S, U, Tx]) standingCodeStands(ctx context.Context, p *pendingState, email string) (bool, error) {
	if !p.UserID.Valid {
		return true, nil
	}
	user, err := a.waitsUser(ctx, p)
	if err != nil {
		return false, err
	}

	return a.codeStands(ctx, p, user, email)
}

// codeStands reports whether a code mailed to email is still good for the pending MFA
// wait p, whose user the store holds as user now. A code is good only for the address
// the account holds when it is entered, confirmed or not: a code mailed to an address
// an administrator has since taken off the account, replaced or moved to another
// account is dead, as one that expired is, and the person asks for another, which goes
// to the address the account holds now.
//
// An account that holds no address takes one at this step from its own person
// (EnrollEmail): the person who proved the account's password, or whose identity is
// linked to the account (signInIsTheUsers). The code mailed to the address they gave
// is good while no other account holds that address. A sign-in that waits to be linked
// gives the account no address, so once its account holds none its code is dead. The
// step keeps no record of whose the address was when the code was mailed, so on the
// account's own person's sign-in a code mailed to an address since taken off the
// account is read as one mailed to an address the person gave, which that person may
// give at this step. The code that accepts an invitation for an account that holds no
// address (codeDestination) is held the same way: it was mailed to the invited address,
// and is good on the account's own person's sign-in while no other account holds that
// address.
func (a *Auth[S, U, Tx]) codeStands(ctx context.Context, p *pendingState, user *sessionstorage.SessionUser, email string) (bool, error) {
	if user.Email != nil {
		return strings.EqualFold(*user.Email, email), nil
	}
	theirs, err := a.signInIsTheUsers(ctx, p)
	if err != nil || !theirs {
		return false, err
	}
	holder, err := a.storage.UserByEmail(ctx, email)
	if err != nil {
		if httpio.HasNotFound(err) {
			return true, nil
		}

		return false, errors.Wrap(err, "sessionstorage.AuthStore.UserByEmail()")
	}

	return holder.ID == user.ID, nil
}

// maskAddress renders an address for a screen that must not spell it out: the first
// character of the local part, then dots, then the domain (p•••@lakeside.edu).
func maskAddress(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" {
		return "•••"
	}

	return local[:1] + "•••@" + domain
}

// mfaWait returns the browser's pending identity when it waits for MFA and the policy
// accepts the email code; otherwise the refusal the handlers answer.
func (a *Auth[S, U, Tx]) mfaWait(ctx context.Context) (*pendingState, error) {
	p, err := a.pending(ctx)
	if err != nil {
		return nil, err
	}
	if p.Step != sessioninfo.PendingMFA {
		return nil, httpio.NewConflictMessage("the pending sign-in waits for a password confirmation, not a second factor")
	}
	if !offersEmailCode(p.Factors) {
		return nil, httpio.NewConflictMessagef("the sign-in policy accepts %v for the MFA step, not the email code", p.Factors)
	}

	return p, nil
}

// newEmailCode returns a fresh code of emailCodeLength digits.
func newEmailCode() (string, error) {
	limit := big.NewInt(1)
	for range emailCodeLength {
		limit.Mul(limit, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", errors.Wrap(err, "rand.Int()")
	}
	code := n.String()
	for len(code) < emailCodeLength {
		code = "0" + code
	}

	return code, nil
}

// hashEmailCode is the hash the code row keeps: the code under the pending sign-in it
// belongs to, so a hash tells nothing about another pending sign-in's code.
func hashEmailCode(pendingID impulse.UUID, code string) []byte {
	sum := sha256.Sum256([]byte(pendingID.String() + ":" + code))

	return sum[:]
}

// sendEmailCode mails a fresh code for p to email and records it, replacing the code
// standing. message is the mail the code goes in, which says what the code does: the
// sign-in code's (codeMessage), or the invitation's own for a code that accepts one
// (invitationCodeMessage). limit takes the code's place under the count it is held to
// before anything is mailed: the sign-in codes' own for the address an account holds
// (limitCode), the invitation's for a code that accepts one (limitInvitationCode), and
// the address's other mail for an address somebody typed (limitMail).
func (a *Auth[S, U, Tx]) sendEmailCode(
	ctx context.Context, p *pendingState, email string,
	message func(to, code string, lifetime time.Duration) mail.Message, limit func(ctx context.Context) error,
) (*emailCodeAnswer, error) {
	if err := limit(ctx); err != nil {
		return nil, err
	}
	code, err := newEmailCode()
	if err != nil {
		return nil, err
	}
	lifetime := a.settings.factors.codeTimeout
	row := &sessionstorage.FactorCode{PendingID: p.ID, Email: email, CodeHash: hashEmailCode(p.ID, code), ExpiresAt: time.Now().Add(lifetime)}
	if err := a.storage.InsertFactorCode(ctx, row); err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.InsertFactorCode()")
	}
	if err := a.send(ctx, message(email, code, lifetime)); err != nil {
		if cerr := a.storage.ConsumeFactorCode(ctx, row.ID); cerr != nil && !httpio.HasNotFound(cerr) {
			logger.FromCtx(ctx).Error(errors.Wrap(cerr, "sessionstorage.AuthStore.ConsumeFactorCode()"))
		}

		return nil, err
	}

	return &emailCodeAnswer{CodeEmail: maskAddress(email), CodeSent: true, CodeExpiresAt: &row.ExpiresAt}, nil
}

// errNoAddress is the answer of a code asked for a user who holds no address.
func errNoAddress() error {
	return httpio.NewConflictMessage("the user holds no email address: give one to send the code to")
}

// sendCodeToUser mails the pending wait's user the code at the address they hold, or,
// for a wait that accepts an invitation, at the invited address (codeDestination).
// Nobody types that address. Only a person past the account's first step is sent the
// account's own code, which is held to the count of the address's sign-in codes
// (limitCode). The holder of an invitation's link is sent the invitation's, which is
// held to that invitation's count (limitInvitationCode): a link can be forwarded, and
// what its holder asks for uses up nothing of the invited person's own sign-in codes.
// The invitation's code comes in a mail of its own (invitationCodeMessage), which says
// that it accepts an invitation and what to do if the person did not open it.
func (a *Auth[S, U, Tx]) sendCodeToUser(ctx context.Context, p *pendingState) (*emailCodeAnswer, error) {
	m := a.settings.mail
	email, inv, err := a.codeDestination(ctx, p)
	if err != nil {
		return nil, err
	}
	if email == "" {
		return nil, errNoAddress()
	}
	if inv != nil {
		return a.sendEmailCode(ctx, p, email, m.invitationCodeMessage, func(ctx context.Context) error {
			return a.limitInvitationCode(ctx, inv)
		})
	}

	return a.sendEmailCode(ctx, p, email, m.codeMessage, func(ctx context.Context) error {
		return a.limitCode(ctx, email)
	})
}

// enrollEmail mails the code to the address a user without one gives. An address
// another account holds is told so by mail, and the screen is answered as sent. Both
// are mail somebody asked for by typing an address, and are held to that address's
// count of such mail (limitMail), never to the count of its own sign-in codes. A wait
// that accepts an invitation takes no address: its code goes to the invited one
// (codeDestination), whatever is typed. A sign-in that is not yet the account's gives
// none (identityIsTheUsers).
func (a *Auth[S, U, Tx]) enrollEmail(ctx context.Context, p *pendingState, email string) (*emailCodeAnswer, error) {
	m := a.settings.mail
	bare, err := address(email)
	if err != nil {
		return nil, err
	}
	held, inv, err := a.codeDestination(ctx, p)
	if err != nil {
		return nil, err
	}
	if inv != nil {
		return nil, errInvitedCodeAddress()
	}
	if held != "" {
		return nil, httpio.NewConflictMessage("the user already holds an email address: the code goes there")
	}
	if err := a.identityIsTheUsers(ctx, p); err != nil {
		return nil, err
	}
	holder, err := a.storage.UserByEmail(ctx, bare)
	if err != nil && !httpio.HasNotFound(err) {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.UserByEmail()")
	}
	if holder != nil && holder.ID != p.UserID.UUID {
		if err := a.limitMail(ctx, bare); err != nil {
			return nil, err
		}
		if err := a.send(ctx, m.addressHeldMessage(bare)); err != nil {
			return nil, err
		}

		return &emailCodeAnswer{CodeEmail: maskAddress(bare), CodeSent: true}, nil
	}

	return a.sendEmailCode(ctx, p, bare, m.codeMessage, func(ctx context.Context) error {
		return a.limitMail(ctx, bare)
	})
}

// errInvitedCodeAddress is the answer to an address given for a wait that accepts an
// invitation.
func errInvitedCodeAddress() error {
	return httpio.NewConflictMessage("the code that accepts an invitation goes to the invited address: no address is given for it")
}

// identityIsTheUsers refuses an address at the mailed code's step of a pending sign-in
// that is not yet its user's. The step takes an address from a person the sign-in
// already ties to the account: one who proved the account's password, and one whose
// identity is linked to the account, as a directory's person is from their first
// sign-in. A sign-in that waits to be linked on the code mailed to the account's
// address has nothing else that ties it to the account: where the account's address
// was taken away during the wait, an address the person gives would have them
// answer for a mailbox of their own choosing and be linked to the account on its code.
// So the step takes none, and the person signs in again once the account holds an
// address.
func (a *Auth[S, U, Tx]) identityIsTheUsers(ctx context.Context, p *pendingState) error {
	theirs, err := a.signInIsTheUsers(ctx, p)
	if err != nil {
		return err
	}
	if theirs {
		return nil
	}

	return httpio.NewConflictMessage("the account holds no email address, and this sign-in is not yet linked to the account: ask an administrator to give the account its address, then sign in again")
}

// signInIsTheUsers reports whether the pending sign-in p is already its user's: the
// person proved the account's password, the application started the sign-in for the
// user itself, or the sign-in's identity is linked to the user. A sign-in that waits to
// be linked is not.
func (a *Auth[S, U, Tx]) signInIsTheUsers(ctx context.Context, p *pendingState) (bool, error) {
	if namesItsUser(&p.Identity) {
		return true, nil
	}
	link, err := a.storage.Identity(ctx, p.Identity.Method, p.Identity.Connection, p.Identity.Subject)
	if err != nil && !httpio.HasNotFound(err) {
		return false, errors.Wrap(err, "sessionstorage.AuthStore.Identity()")
	}

	return link != nil && link.UserID == p.UserID.UUID, nil
}

// errInvalidCode and errCodeExpired are the refusals of a code that is not the one
// sent, and of a code entered once the one sent died.
func errInvalidCode() error {
	return sessioninfo.NewLoginRefusal(sessioninfo.RefusedInvalidCode, httpio.NewUnauthorizedMessage("the code is not the one sent"))
}

func errCodeExpired() error {
	return sessioninfo.NewLoginRefusal(sessioninfo.RefusedCodeExpired, httpio.NewUnauthorizedMessage("no sign-in code stands: ask for a new one"))
}

// verifyEmailCode checks code against the one standing for p and consumes it: a wrong
// code counts, and the code that took its last attempt is consumed. A code entered
// while none stands, because none was sent or the one sent expired or died, is
// refused as expired. It returns the address the code went to.
//
// A guess takes its attempt before it is judged: the store counts it on the code's row
// in one statement and answers which attempt it was, and a guess past the limit is not
// compared with the code at all, nor does it touch the code. Guesses made at once are
// each counted, so no more than the limit are ever judged, however many arrive
// together; the right code spends an attempt like any other, on a code it then
// consumes, and of several right guesses within the limit one is accepted.
func (a *Auth[S, U, Tx]) verifyEmailCode(ctx context.Context, p *pendingState, code string) (string, error) {
	standing, err := a.storage.FactorCode(ctx, p.ID)
	if err != nil {
		if httpio.HasNotFound(err) {
			return "", errCodeExpired()
		}

		return "", errors.Wrap(err, "sessionstorage.AuthStore.FactorCode()")
	}
	attempts, err := a.storage.RecordFactorCodeAttempt(ctx, standing.ID)
	if err != nil {
		if httpio.HasNotFound(err) {
			return "", errCodeExpired()
		}

		return "", errors.Wrap(err, "sessionstorage.AuthStore.RecordFactorCodeAttempt()")
	}
	if attempts > emailCodeAttempts {
		// Past the limit: the guess is not judged and touches nothing. The guess that
		// took the last attempt consumes the code, right or wrong, and a consume from
		// here would race the right code of a guess within the limit.
		return "", errCodeExpired()
	}
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if subtle.ConstantTimeCompare(standing.CodeHash, hashEmailCode(p.ID, code)) != 1 {
		if attempts >= emailCodeAttempts {
			if err := a.storage.ConsumeFactorCode(ctx, standing.ID); err != nil && !httpio.HasNotFound(err) {
				return "", errors.Wrap(err, "sessionstorage.AuthStore.ConsumeFactorCode()")
			}

			return "", errCodeExpired()
		}

		return "", errInvalidCode()
	}
	if err := a.storage.ConsumeFactorCode(ctx, standing.ID); err != nil {
		if httpio.HasNotFound(err) {
			return "", errCodeExpired()
		}

		return "", errors.Wrap(err, "sessionstorage.AuthStore.ConsumeFactorCode()")
	}

	return standing.Email, nil
}

// completeWithEmailCode completes the pending MFA wait p once code proved the person
// reads email at the address the account holds, or, for a wait that accepts an
// invitation, at the invited address: the pending identity is consumed, the address is
// written on the user verified when it was not, and the session starts with an
// email-code event. A code mailed to an address the account no longer holds completes
// nothing (claimCodeAddress). A sign-up through an invitation makes its account here,
// the code entered (invitedSignUpAttempt). With remember, a session that starts makes
// the browser a trusted device of the user.
//
// A code that accepts an invitation takes its place in the invitation's count of codes
// entered before it is judged (takeInvitationGuess): twenty-five are judged for one
// invitation, and the wrong one that took the last place is answered, as every code
// after it is, with invitation_spent.
func (a *Auth[S, U, Tx]) completeWithEmailCode(ctx context.Context, w http.ResponseWriter, p *pendingState, code string, remember bool) (*signInOutcome, error) {
	last, err := a.takeInvitationGuess(ctx, p)
	if err != nil {
		return nil, err
	}
	email, err := a.verifyEmailCode(ctx, p, code)
	if err != nil {
		if refused := sessioninfo.LoginRefusalCodeOf(err); last && (refused == sessioninfo.RefusedInvalidCode || refused == sessioninfo.RefusedCodeExpired) {
			return nil, errInvitationSpent()
		}

		return nil, err
	}
	// The code reached the person: the counts it was sent under are reset.
	a.clearCodeLimit(ctx, email)
	a.clearMailLimit(ctx, email)
	a.clearInvitationCodeOf(ctx, email)

	at, err := a.attemptWithCode(ctx, p, email)
	if err != nil {
		return nil, err
	}
	outcome, err := a.signIn(ctx, w, at)
	if err != nil {
		return nil, err
	}
	if outcome.pending == nil {
		// The person proved who they are: the name's failed attempts are reset.
		if p.Username != "" {
			a.clearAttempts(ctx, p.Username)
		}
		if remember {
			a.rememberDevice(ctx, w, outcome.userID)
		}
	}

	return outcome, nil
}

// clearInvitationCodeOf resets the count of the codes sent to accept the invitation
// the browser carries, once a code mailed to its address, email, was entered. It is
// bookkeeping after the proof: a failure is logged, and the sign-in goes on.
func (a *Auth[S, U, Tx]) clearInvitationCodeOf(ctx context.Context, email string) {
	inv, err := a.invitationOf(ctx)
	if err != nil {
		logger.FromCtx(ctx).Error(err)

		return
	}
	if inv != nil && strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(inv.Email)) {
		a.clearInvitationCodeLimit(ctx, inv)
	}
}

// attemptWithCode is the sign-in that completes the pending MFA wait p once the code
// mailed to email was entered: the wait is consumed, so it completes once, and the
// sign-in carries the address the code proved. A wait that names its user holds the
// code to the address the account holds now, and writes that address on the account
// first where the account has confirmed none (claimCodeAddress).
// A wait that names none is an invitation's acceptance: the sign-in that carries the
// code makes or finds the account and writes the address with the acceptance, and the
// code counts for that acceptance alone. The account of a sign-up through an invitation
// is made here (invitedSignUpAttempt).
func (a *Auth[S, U, Tx]) attemptWithCode(ctx context.Context, p *pendingState, email string) (*signInAttempt, error) {
	if signsUpInvited(p) {
		return a.invitedSignUpAttempt(ctx, p, email)
	}
	if p.UserID.Valid {
		if err := a.claimCodeAddress(ctx, p, email); err != nil {
			return nil, err
		}
	}
	if err := a.consumePending(ctx, p); err != nil {
		return nil, err
	}
	at, err := a.pendingAttempt(p, sessioninfo.ReasonStepUp, sessioninfo.AuthEvent{Method: sessioninfo.MethodEmailCode})
	if err != nil {
		return nil, err
	}
	at.codeEmail, at.acceptsInvitation = email, !p.UserID.Valid

	return at, nil
}

// claimCodeAddress holds the code the person entered for the pending MFA wait p to the
// address the account holds now, and writes that address onto the user, verified, when
// the user has confirmed none: the first code confirms the address the account was
// made with or the person gave. A code is good only for the address the account holds
// when it is entered (codeStands): one mailed to an address the account no longer
// holds, confirmed or not, is dead, as one that expired is (the person asks for
// another, which goes to the address the account holds now), on every wait, and
// nothing is written, so an administrator's change of the address stands. An account
// that holds a confirmed address keeps it: a code proves the person, and a confirmed
// address is replaced only by the link that confirms a new one. The write is
// bookkeeping after the proof: a failure of it is logged, and the sign-in goes on.
func (a *Auth[S, U, Tx]) claimCodeAddress(ctx context.Context, p *pendingState, email string) error {
	user, err := a.waitsUser(ctx, p)
	if err != nil {
		return err
	}
	stands, err := a.codeStands(ctx, p, user, email)
	if err != nil {
		return err
	}
	if !stands {
		logger.FromCtx(ctx).Infof("the sign-in code user %s entered was mailed to %s, an address the account no longer holds: the code is dead, and nothing is written", user.ID, email)

		return errCodeExpired()
	}
	if user.EmailVerified() {
		return nil
	}
	if err := a.storage.VerifyUserEmail(ctx, user.ID, email); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.VerifyUserEmail()"))

		return nil
	}
	logger.FromCtx(ctx).Infof("user %s verified the email address %s by the sign-in code", user.ID, email)
	if a.registered().usernameIsEmail() && !strings.EqualFold(user.Username, email) && a.userRenamed != nil {
		if err := a.userRenamed(ctx, user.ID, user.Username, email); err != nil {
			logger.FromCtx(ctx).Errorf("user %s was renamed from %q to %q by the sign-in code, and the rename hook failed; run the hook's work by hand: %v", user.ID, user.Username, email, err)
		}
	}

	return nil
}

// The trusted device cookie's value: one entry per user the browser was remembered
// for, under trustedDevicesKey, keyed by the user's id (inside the encrypted value, so
// nothing about who used the browser is readable from it), each holding the token and
// when it was written. Two people who share a browser, or one person with two accounts,
// are each remembered: a sign-in reads the signing-in user's entry alone and a held
// sign-in drops that user's entry alone, so one person's remembering never forgets
// another's. The browser holds at most trustedDevicesPerBrowser entries; remembering
// one more drops the oldest.
const (
	trustedDevicesKey        cookie.Key = "devices"
	trustedDevicesPerBrowser int        = 5
)

// trustedEntry is one user's token in the trusted device cookie: the token, and when
// the entry was written, which decides the oldest at the cap.
type trustedEntry struct {
	Token string `json:"token"`
	At    int64  `json:"at"`
}

// trustedDevices is what the browser's trusted device cookie carries, as StartSession
// read it: the entries by user id.
type trustedDevices struct {
	entries map[string]trustedEntry
}

// trustedCtxKey is the context key for the browser's trusted devices, set by
// StartSession.
type trustedCtxKey struct{}

// withTrustedDeviceCookie returns ctx with the browser's trusted devices, if it carries
// a trusted device cookie naming any.
func (a *Auth[S, U, Tx]) withTrustedDeviceCookie(ctx context.Context, r *http.Request) context.Context {
	cval, found, err := a.trustedCookies.Cookie().Read(r, a.settings.factors.trustedCookie)
	if err != nil || !found {
		return ctx
	}
	devices := &trustedDevices{entries: map[string]trustedEntry{}}
	if err := cval.Get(trustedDevicesKey, &devices.entries); err != nil {
		devices.entries = map[string]trustedEntry{}
	}
	if len(devices.entries) == 0 {
		return ctx
	}

	return context.WithValue(ctx, trustedCtxKey{}, devices)
}

// trustedDevicesOf returns the browser's trusted devices from ctx.
func trustedDevicesOf(ctx context.Context) (*trustedDevices, bool) {
	d, ok := ctx.Value(trustedCtxKey{}).(*trustedDevices)

	return d, ok
}

// trustedTokenHash is the hash of a token as the cookie carries it, nil when it is not
// a device token.
func trustedTokenHash(token string) []byte {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != trustedTokenSize {
		return nil
	}

	return hashPendingToken(raw)
}

// trustedHashFor returns the hash of the token the browser carries for userID: the
// user's entry, whose row trustedDeviceOf checks is that user's.
func trustedHashFor(ctx context.Context, userID impulse.UUID) ([]byte, bool) {
	d, ok := trustedDevicesOf(ctx)
	if !ok {
		return nil, false
	}
	if e, ok := d.entries[userID.String()]; ok {
		if h := trustedTokenHash(e.Token); h != nil {
			return h, true
		}
	}

	return nil, false
}

// trustedDeviceOf is the sign-in flow's trusted device check: whether the browser
// carries, for userID, the token of a live trusted device of theirs. An entry naming a
// dead device, or another user's, is no device.
func (a *Auth[S, U, Tx]) trustedDeviceOf(ctx context.Context, userID impulse.UUID) (bool, error) {
	hash, ok := trustedHashFor(ctx, userID)
	if !ok {
		return false, nil
	}
	device, err := a.storage.TrustedDevice(ctx, hash)
	if err != nil {
		if httpio.HasNotFound(err) {
			return false, nil
		}

		return false, errors.Wrap(err, "sessionstorage.AuthStore.TrustedDevice()")
	}

	return device.UserID == userID, nil
}

// touchTrustedDevice moves the last use of the trusted device the browser carries for
// userID once it stood in for the factor (events carry a trusted-device event). It is
// bookkeeping: a failure is logged.
func (a *Auth[S, U, Tx]) touchTrustedDevice(ctx context.Context, userID impulse.UUID, events []sessioninfo.AuthEvent) {
	hash, ok := trustedHashFor(ctx, userID)
	if !ok || !slices.ContainsFunc(events, func(e sessioninfo.AuthEvent) bool { return e.Method == sessioninfo.MethodTrustedDevice }) {
		return
	}
	device, err := a.storage.TrustedDevice(ctx, hash)
	if err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.TrustedDevice()"))

		return
	}
	if err := a.storage.TouchTrustedDevice(ctx, device.ID); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.TouchTrustedDevice()"))
	}
}

// rememberDevice makes the browser a trusted device of userID for the trusted device
// period: a row with the token's hash, and the user's entry in the cookie, beside the
// other users' entries the browser carries (the oldest dropped at the cap). A failure
// is logged; the session stands and the next sign-in asks for the factor again.
func (a *Auth[S, U, Tx]) rememberDevice(ctx context.Context, w http.ResponseWriter, userID impulse.UUID) {
	raw := make([]byte, trustedTokenSize)
	if _, err := rand.Read(raw); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "rand.Read()"))

		return
	}
	period := a.settings.factors.trustedPeriod
	device := &sessionstorage.TrustedDevice{UserID: userID, TokenHash: hashPendingToken(raw), ExpiresAt: time.Now().Add(period)}
	if err := a.storage.InsertTrustedDevice(ctx, device); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.InsertTrustedDevice()"))

		return
	}
	var carried map[string]trustedEntry
	if d, ok := trustedDevicesOf(ctx); ok {
		carried = d.entries
	}
	a.writeTrustedCookie(ctx, w, trustedEntriesWith(carried, userID, trustedEntry{Token: base64.RawURLEncoding.EncodeToString(raw), At: time.Now().Unix()}))
}

// trustedEntriesWith returns the entries a browser carries with userID's entry set to
// entry, the oldest other entries dropped so that at most trustedDevicesPerBrowser
// remain. The browser's own map is left as it was.
func trustedEntriesWith(carried map[string]trustedEntry, userID impulse.UUID, entry trustedEntry) map[string]trustedEntry {
	entries := make(map[string]trustedEntry, len(carried)+1)
	maps.Copy(entries, carried)
	id := userID.String()
	entries[id] = entry
	for len(entries) > trustedDevicesPerBrowser {
		oldest := ""
		for other, e := range entries {
			if other != id && (oldest == "" || e.At < entries[oldest].At) {
				oldest = other
			}
		}
		delete(entries, oldest)
	}

	return entries
}

// writeTrustedCookie writes the trusted device cookie holding entries, for the trusted
// device period. Lax, so the cookie reaches the callback an external sign-in's
// provider redirects the browser to, where the sign-in is decided.
func (a *Auth[S, U, Tx]) writeTrustedCookie(ctx context.Context, w http.ResponseWriter, entries map[string]trustedEntry) {
	cval := cookie.NewValues()
	if err := cval.Set(trustedDevicesKey, entries); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "cookie.Values.Set()"))

		return
	}
	a.trustedCookies.Cookie().WritePersistentCookie(w, a.settings.factors.trustedCookie, a.trustedCookies.Domain, true, http.SameSiteLaxMode, a.settings.factors.trustedPeriod, cval)
}

// dropTrustedEntry takes userID's entry out of the trusted device cookie the browser
// carries, leaving the other users' entries as they are: the cookie is rewritten
// without it, or deleted when nothing is left. A browser carrying no entry for the
// user is left alone.
func (a *Auth[S, U, Tx]) dropTrustedEntry(ctx context.Context, w http.ResponseWriter, userID impulse.NullUUID) {
	d, ok := trustedDevicesOf(ctx)
	if !ok {
		return
	}
	entries := maps.Clone(d.entries)
	if userID.Valid {
		delete(entries, userID.String())
	}
	if len(entries) == len(d.entries) {
		return
	}
	if len(entries) == 0 {
		a.trustedCookies.Cookie().Delete(w, a.settings.factors.trustedCookie, a.trustedCookies.Domain)

		return
	}
	a.writeTrustedCookie(ctx, w, entries)
}

// pendingSendCode is Pending().SendCode().
func (a *Auth[S, U, Tx]) pendingSendCode() http.HandlerFunc {
	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		p, err := a.mfaWait(ctx)
		if err != nil {
			return writeSignInError(ctx, w, err)
		}
		answer, err := a.sendCodeToUser(ctx, p)
		if err != nil {
			return writeSignInError(ctx, w, err)
		}

		return httpio.NewEncoder(w).Ok(answer)
	})
}

// pendingEnrollEmail is Pending().EnrollEmail().
func (a *Auth[S, U, Tx]) pendingEnrollEmail() http.HandlerFunc {
	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		p, err := a.mfaWait(ctx)
		if err != nil {
			return writeSignInError(ctx, w, err)
		}
		req, err := emailDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		answer, err := a.enrollEmail(ctx, p, req.Email)
		if err != nil {
			return writeSignInError(ctx, w, err)
		}

		return httpio.NewEncoder(w).Ok(answer)
	})
}

// pendingVerifyCode is Pending().VerifyCode().
func (a *Auth[S, U, Tx]) pendingVerifyCode() http.HandlerFunc {
	type request struct {
		Code           string `json:"code"`
		RememberDevice bool   `json:"rememberDevice"`
	}
	decoder := sessioncore.NewDecoder[request]()

	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		p, err := a.mfaWait(ctx)
		if err != nil {
			return writeSignInError(ctx, w, err)
		}
		req, err := decoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		outcome, err := a.completeWithEmailCode(ctx, w, p, req.Code, req.RememberDevice)
		if err != nil {
			return writeSignInError(ctx, w, err)
		}

		return a.answerSignIn(ctx, w, r, outcome)
	})
}

// SendEmailCode mails the pending identity's user a one-time code for the second
// factor, to the address the user holds, replacing a code standing, and returns the
// masked address and when the code stops being accepted. The pending identity must
// wait for MFA and the policy accept the email code (Conflict otherwise), and the user
// must hold an address (Conflict: EnrollEmail takes one). It reads the pending
// identity StartSession found.
func (p *AuthAPI[S, U, Tx]) SendEmailCode(ctx context.Context) (codeEmail string, expiresAt time.Time, err error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	pending, err := p.auth.mfaWait(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	answer, err := p.auth.sendCodeToUser(ctx, pending)
	if err != nil {
		return "", time.Time{}, err
	}

	return answer.CodeEmail, *answer.CodeExpiresAt, nil
}

// EnrollEmail mails the code to email for a pending identity whose user holds no
// address; the address is written on the user, verified, when the code is entered. A
// user who holds an address is refused (Conflict). An address another account holds
// is told so by mail, and the call returns as if sent. See SendEmailCode.
func (p *AuthAPI[S, U, Tx]) EnrollEmail(ctx context.Context, email string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	pending, err := p.auth.mfaWait(ctx)
	if err != nil {
		return err
	}
	if _, err := p.auth.enrollEmail(ctx, pending, email); err != nil {
		return err
	}

	return nil
}

// VerifyEmailCode completes the pending identity when code is the one sent, recording
// the email-code event itself (CompletePending takes no such event from an
// application), and returns the session's id; with rememberDevice the browser is
// remembered as a trusted device of the user. A wrong code is a
// *sessioninfo.LoginRefusal carrying RefusedInvalidCode and leaves the code standing
// with one fewer attempt; a code that expired, was used, took its last attempt or was
// mailed to an address the account no longer holds, or entered before one was sent, is
// one carrying RefusedCodeExpired. If the sign-in must wait again, the error is a
// *signin.PendingSignInError.
func (p *AuthAPI[S, U, Tx]) VerifyEmailCode(ctx context.Context, w http.ResponseWriter, code string, rememberDevice bool) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	pending, err := p.auth.mfaWait(ctx)
	if err != nil {
		return impulse.NilUUID, err
	}
	outcome, err := p.auth.completeWithEmailCode(ctx, w, pending, code, rememberDevice)
	if err != nil {
		return impulse.NilUUID, err
	}
	if outcome.pending != nil {
		return impulse.NilUUID, stillPending(outcome.pending)
	}

	return outcome.sessionID, nil
}

// ResetFactors is an administrator's reset of a user's second factor: every browser
// the person asked to be remembered is forgotten, so their next sign-in from any of
// them asks for the factor, and the person is told by mail (NoticeFactorReset). The
// email code itself has nothing to reset: a person who lost their mailbox is given a
// new address by an administrator's request, which the new address confirms
// (RequestAddressChange). Live sessions are not touched; DestroyUserSessions ends them.
func (p *AuthAPI[S, U, Tx]) ResetFactors(ctx context.Context, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := p.auth.storage.RevokeTrustedDevices(ctx, userID); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.RevokeTrustedDevices()")
	}
	if err := p.NoticeFactorReset(ctx, userID); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "the notice that a person's second factor was reset was not sent"))
	}

	return nil
}

// NoticeFactorReset tells userID by mail that an administrator reset their second
// factor, at the address the account holds confirmed. A caller that resets the factor
// in a transaction of its own (the generated user management) calls it once that has
// committed; ResetFactors sends it itself. An account with no confirmed address is told
// nothing: a notice goes to a confirmed address alone. The notice is sent on an
// administrator's reset and on nothing else: a person who forgets a browser themselves
// is not written to.
func (p *AuthAPI[S, U, Tx]) NoticeFactorReset(ctx context.Context, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	m := p.auth.settings.mail
	user, err := p.auth.storage.User(ctx, userID)
	if err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	if !user.EmailVerified() {
		return nil
	}

	return p.auth.send(ctx, m.factorResetMessage(*user.Email))
}

// NoticeRemovedFromTenant tells a person by mail that an administrator removed them
// from a tenant: the act that left them with no role there. to is the address the
// account held confirmed when it was removed, which the caller read before the removal,
// since the account is gone when the removal took its last role (accountDeleted, which
// the notice then says); tenant is the tenant's key, named in the message as the Auth's
// tenant names do (SetTenantNames). The caller sends it once the removal has committed,
// and only for an administrator's act: a role that ends by itself and a person who
// leaves by themselves are not written to.
func (p *AuthAPI[S, U, Tx]) NoticeRemovedFromTenant(ctx context.Context, to, tenant string, accountDeleted bool) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	m := p.auth.settings.mail
	bare, err := address(to)
	if err != nil {
		return err
	}

	return p.auth.send(ctx, m.removedFromTenantMessage(bare, p.auth.tenantName(tenant), accountDeleted))
}

// ForgetBrowser is the handler a signed-in person's account page calls to forget this
// browser as a trusted device of theirs (AuthAPI.ForgetTrustedDevice): their next sign-in
// from it asks for the second factor again, and the other people the browser was
// remembered for keep theirs. It validates the session itself, as the password change
// does, so it is routed beside it, outside ValidateSession; an impersonated session is
// refused. A browser that is not remembered for the person answers 200 too: there is
// nothing to forget. The session read says whether there is (browserRemembered).
func (a *Auth[S, U, Tx]) ForgetBrowser() http.HandlerFunc {
	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		if _, validated := ctx.Value(sessioninfo.CtxUserInfo).(*sessioninfo.UserInfo); !validated {
			var err error
			if ctx, err = a.validate(ctx); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
		}
		if err := sessioncore.RefuseImpersonated(ctx, a.baseSession, "ForgetBrowser", true); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := a.API().ForgetTrustedDevice(ctx, w); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(nil)
	}))
}

// browserRemembered reports whether the browser of the request carries a remembered
// entry for the user: what the session read tells the account page. It reads the
// cookie StartSession read, not the device's row, so an entry whose device has since
// lapsed still answers true; forgetting it is then harmless.
func browserRemembered(ctx context.Context, userID impulse.UUID) bool {
	_, ok := trustedHashFor(ctx, userID)

	return ok
}

// ForgetTrustedDevice forgets the browser of the current request as a trusted device of
// the session's user: their device is revoked and their entry taken out of the cookie,
// so their next sign-in from it asks for the factor; the other people the browser was
// remembered for keep theirs. It reads the session StartSession validated, and a
// browser that is no trusted device of the user is left as it is.
func (p *AuthAPI[S, U, Tx]) ForgetTrustedDevice(ctx context.Context, w http.ResponseWriter) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	user, ok := ctx.Value(sessioninfo.CtxUserInfo).(*sessioninfo.UserInfo)
	if !ok || user == nil {
		return errors.New("ForgetTrustedDevice: the context carries no session user")
	}
	hash, ok := trustedHashFor(ctx, user.ID)
	if !ok {
		return nil
	}
	if err := p.auth.storage.RevokeTrustedDevice(ctx, hash); err != nil && !httpio.HasNotFound(err) {
		return errors.Wrap(err, "sessionstorage.AuthStore.RevokeTrustedDevice()")
	}
	p.auth.dropTrustedEntry(ctx, w, impulse.NullUUID{UUID: user.ID, Valid: true})

	return nil
}
