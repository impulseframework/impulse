package session

import (
	"context"
	"encoding/base64"
	"fmt"
	netmail "net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/attempts"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// The mail the Auth sends: an invitation, the verification of an address a person
// adds, the link a sign-up starts from, the link that resets a password, the link that
// confirms an address an administrator asked to put on an account, and the notices that
// tell a person their account changed, which carry no link. Each of the others is one
// plain-text message with one link in it, to the login page with the link's token in
// the URL's fragment, which the page posts back to the route that completes the step. A message reads as
// instructions: what the person did, the link to open and how long it is good for,
// then, under headings of their own, what to do if it was not them and a word to keep
// the link to themselves. The library writes to the mail.Sender interface and knows
// nothing of the mail system behind it; see "Sending mail" in the README.

// MailSettings is how the Auth sends mail: through Sender, as the application, with
// links to its login page. NewAuth takes it as an argument, since every Auth sends
// mail: its invitations (AuthAPI.Invite, Invitation()), the add-address step (Email()),
// sign-up (SignUp()), password resets and the codes of its sign-ins all go by mail.
// NewAuth refuses a nil sender, an empty application name and a login URL that is not
// an absolute http or https URL.
type MailSettings struct {
	// Sender delivers the messages: mail/workspace is the Google Workspace relay, and
	// mail.Log writes them to the log in development.
	Sender mail.Sender
	// Application is the application's name as the messages say it ("Harbor").
	Application string
	// LoginURL is the absolute URL of the application's login page
	// (https://harbor.example.com/login), where every mailed link leads, its token in
	// the fragment, which a browser sends to no server: #invitation=<token> for an
	// invitation, #verify=<token> for an address verification, #signUp=<token> for a
	// sign-up, #reset=<token> for a password reset and #address=<token> for an address
	// an administrator asked to change. The page takes the token out of the address
	// and posts it to the matching route.
	LoginURL string
	// InvitationTimeout is how long an invitation stands; zero is seven days.
	InvitationTimeout time.Duration
	// LinkTimeout is how long a verification or sign-up link stands; zero is a day.
	LinkTimeout time.Duration
}

// Default mail link lifetimes.
const (
	defaultInvitationTimeout = 7 * 24 * time.Hour
	defaultLinkTimeout       = 24 * time.Hour
)

// mailLimit bounds the sign-in codes and links that are sent and not used: five within
// fifteen minutes, then none for fifteen minutes. It is kept in the sign-in attempts
// table, as a name's failed attempts are, in three counts. The codes of an address's
// own sign-ins are counted by themselves under the address (codeLimitKey): only a
// person past the first step of the account that holds the address has one sent. The
// codes that accept an invitation are counted under that invitation
// (invitationCodeLimitKey) and under no address: whoever holds the invitation's link
// has them sent, and a mailed link can be forwarded, so a stranger who holds one runs
// up the invitation's count alone. Everything else is counted together under the
// address (mailLimitKey), and anybody who knows the address can run that count up: a
// sign-up started for it, a link asked to add it to an account, a code asked for it by
// a person whose account holds no address yet. Kept apart, nothing a stranger asks for
// in a person's name, or with a link mailed to them, holds up the codes that person
// signs in with. A code or link that is used resets the counts it was sent under
// (clearMailLimit, clearCodeLimit, clearInvitationCodeLimit), so a person who uses what
// they are sent never meets the limit; it stops mail asked for over and over and never
// used.
var mailLimit = sessionstorage.SignInAttemptLimit{Failures: 5, Window: 15 * time.Minute, Lock: 15 * time.Minute}

// mailLimitKey is the address's key in the sign-in attempts table: the address folded
// as the users table folds one (attempts.Normalize), so every spelling that finds the
// same account's address is counted under the one key, and under a kind no name a
// person signs in under can spell (attempts.OwnKey).
func mailLimitKey(email string) string {
	return attempts.OwnKey(attempts.KindMail, attempts.Normalize(email))
}

// codeLimitKey is the key of the address's sign-in codes in the sign-in attempts table,
// folded as mailLimitKey folds it and under a kind of its own.
func codeLimitKey(email string) string {
	return attempts.OwnKey(attempts.KindCode, attempts.Normalize(email))
}

// invitationCodeLimitKey is the key, in the sign-in attempts table, of the codes sent to
// accept the invitation inv: the invitation's id under a kind of its own, so the count
// is that invitation's and no address's.
func invitationCodeLimitKey(inv *sessioninfo.Invitation) string {
	return attempts.OwnKey(attempts.KindInvitation, inv.ID.String())
}

// mailSettings are the Auth's mail settings once validated.
type mailSettings struct {
	sender   mail.Sender
	settings MailSettings
}

// validate refuses settings the Auth cannot send with and fills the defaults.
func (m *mailSettings) validate() error {
	if m.sender == nil {
		return errors.New("MailSettings: the sender is nil: every Auth sends mail, so set Sender to a mail.Sender, such as workspace.New")
	}
	if strings.TrimSpace(m.settings.Application) == "" {
		return errors.New("MailSettings: the application name is empty: the messages name the application")
	}
	u, err := url.Parse(m.settings.LoginURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Fragment != "" {
		return errors.Newf("MailSettings: the login URL %q is not an absolute http or https URL without a fragment: the mailed links lead to the login page", m.settings.LoginURL)
	}
	if m.settings.InvitationTimeout <= 0 {
		m.settings.InvitationTimeout = defaultInvitationTimeout
	}
	if m.settings.LinkTimeout <= 0 {
		m.settings.LinkTimeout = defaultLinkTimeout
	}

	return nil
}

// link renders the login page URL that carries token under param, in the URL's
// fragment. A browser keeps a fragment to itself: it is in no request line, so the
// token reaches no server's or proxy's request log, and in no Referer header. The page
// reads the token from the fragment, takes it out of the address at once, and posts it
// in the body of the request that uses it. Every kind of mailed link is built here.
func (m *mailSettings) link(param, token string) string {
	return m.settings.LoginURL + "#" + param + "=" + url.QueryEscape(token)
}

// goodFor renders how long a link is good for in everyday words: minutes under an
// hour, whole hours under two days, days from there. A part of a unit is dropped, so
// the message never promises more than the link gives; under a minute reads as one. A
// person knows when the message reached them, so a duration tells them whether the
// link still stands where a deadline in UTC would not.
func goodFor(d time.Duration) string {
	const day = 24 * time.Hour
	switch {
	case d < time.Hour:
		return count(int(max(d, time.Minute)/time.Minute), "minute")
	case d < 2*day:
		return count(int(d/time.Hour), "hour")
	default:
		return count(int(d/day), "day")
	}
}

// count renders n of unit in words: "1 hour", "6 hours".
func count(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}

	return fmt.Sprintf("%d %ss", n, unit)
}

// invitationMessage is the mail an invitation sends: the invitation, the link that
// accepts it and how long the link is good for (lifetime), then what to do if no
// invitation was expected and a word to keep the link to oneself.
func (m *mailSettings) invitationMessage(to, token string, lifetime time.Duration) mail.Message {
	app := m.settings.Application

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("You are invited to %s", app),
		Text: fmt.Sprintf(`You are invited to %s.

To accept the invitation, open this link:
%s

The link is good for %s.

Didn't expect this?
If you were not expecting an invitation, ignore this message. Nothing changes.

Keep the link to yourself
Anyone who opens the link can accept the invitation in your place. Don't forward it.
`, app, m.link("invitation", token), goodFor(lifetime)),
	}
}

// verificationMessage is the mail that confirms an address a signed-in person adds. It
// takes for granted that they added it: open the link (the page asks for the sign-in
// itself when none stands), and the link is good for lifetime; then what to do if it
// was not them. The link works only for the account that added the address, so there
// is no word about keeping it private.
func (m *mailSettings) verificationMessage(to, token string, lifetime time.Duration) mail.Message {
	app := m.settings.Application

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("Confirm your email address for %s", app),
		Text: fmt.Sprintf(`You added this address to your %s account.

To confirm it, open this link:
%s

The link is good for %s.

Didn't do this?
If you did not add this address, ignore this message. Nothing changes.
`, app, m.link("verify", token), goodFor(lifetime)),
	}
}

// addressHeldMessage is the mail sent when a signed-in person adds an address that
// another account already holds: the screen says nothing, the mailbox's owner learns
// what happened (the address belongs to one account, someone tried to add it to a
// different one, nothing changed), is told that if it was them they have two accounts,
// which one the address is on now and how to move it to the other, then what to do if
// it was not them.
func (m *mailSettings) addressHeldMessage(to string) mail.Message {
	app := m.settings.Application

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("Your email address and %s", app),
		Text: fmt.Sprintf(`This address already belongs to a %s account, and someone just tried to add it to a different one. Nothing was changed: an address can belong to one account only.

If that was you, you have two %s accounts, and this address can be on only one of them. Sign in to the account it is on now.
If you would rather use it on the other account, change the address on this one first, then add it there.

Didn't do this?
If you did not try to add this address, ignore this message. Nothing changes.
`, app, app),
	}
}

// signUpMessage is the mail a sign-up starts from: the link to open and choose a
// password at and how long it is good for (lifetime), then what to do if no account
// was asked for and a word to keep the link to oneself.
func (m *mailSettings) signUpMessage(to, token string, lifetime time.Duration) mail.Message {
	app := m.settings.Application

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("Finish creating your %s account", app),
		Text: fmt.Sprintf(`You asked to create a %s account with this address.

To finish, open this link and choose your password:
%s

The link is good for %s.

Didn't do this?
If you did not ask for an account, ignore this message. Nothing changes.

Keep the link to yourself
Anyone who opens the link can create the account in your place. Don't forward it.
`, app, m.link("signUp", token), goodFor(lifetime)),
	}
}

// accountExistsMessage is the mail sent when a sign-up names an address an account
// already holds: the person is pointed at their account by mail and told what to do if
// it was not them, and the screen says nothing.
func (m *mailSettings) accountExistsMessage(to string) mail.Message {
	app := m.settings.Application

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("You already have a %s account", app),
		Text: fmt.Sprintf(`You asked to create a %s account with this address, but there is already an account for it.

Sign in to %s to use it. If you forgot how, ask your administrator.

Didn't do this?
If you did not ask for an account, ignore this message. Nothing changes.
`, app, app),
	}
}

// AskedBy says who asked for a link the Auth mails to a person about their account, as
// the message says it: the person themselves (the zero value), an administrator of one
// tenant, or an administrator of the application. A person is told who set a change of
// their account in motion, so a link they did not expect says where it came from.
type AskedBy struct {
	// Administrator says an administrator asked, and not the person.
	Administrator bool
	// Tenant is the tenant whose administrator asked, by its key; "" for an
	// administrator of the application. The message names the tenant as the Auth's
	// tenant names do (SetTenantNames), and names the application where they have no
	// name for it.
	Tenant string
}

// AskedByPerson is the asker of a link a person asked for themselves.
func AskedByPerson() AskedBy {
	return AskedBy{Administrator: false, Tenant: ""}
}

// AskedByApplication is the asker of a link an administrator of the application asked
// for.
func AskedByApplication() AskedBy {
	return AskedBy{Administrator: true, Tenant: ""}
}

// AskedByTenant is the asker of a link an administrator of the tenant asked for.
func AskedByTenant(tenant string) AskedBy {
	return AskedBy{Administrator: true, Tenant: tenant}
}

// administratorOf is whose administrator asked, as a message says it after "An
// administrator of": the tenant by its name, and the application where the asker is the
// application's own or the tenant has no name the Auth knows.
func (a *Auth[S, U, Tx]) administratorOf(m *mailSettings, by AskedBy) string {
	if name := a.tenantName(by.Tenant); name != "" {
		return name
	}

	return m.settings.Application
}

// passwordResetMessage is the mail that carries a password reset link: who asked (the
// person, or an administrator of asker), the link to open and choose a new password at,
// how long it is good for (lifetime) and that the password stands until it is used,
// then what to do if it was not expected and a word to keep the link to oneself.
func (m *mailSettings) passwordResetMessage(to, token string, lifetime time.Duration, by AskedBy, asker string) mail.Message {
	app := m.settings.Application
	opening := fmt.Sprintf("You asked to reset the password of your %s account.", app)
	unexpected := "Didn't do this?\nIf you did not ask for this, ignore this message. Nothing changes."
	if by.Administrator {
		opening = fmt.Sprintf("An administrator of %s asked us to send you a link to reset the password of your %s account.", asker, app)
		unexpected = "Didn't expect this?\nIf you do not want a new password, ignore this message. Nothing changes."
	}

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("Reset your %s password", app),
		Text: fmt.Sprintf(`%s

To choose a new password, open this link:
%s

The link is good for %s and works once. Your password stays as it is until you use the link.

%s

Keep the link to yourself
Anyone who opens the link can choose a new password for your account. Don't forward it.
`, opening, m.link("reset", token), goodFor(lifetime), unexpected),
	}
}

// addressChangeMessage is the mail that confirms an address an administrator of asker
// asked to put on an account: what was asked, the link that confirms it and how long it
// is good for (lifetime), that the account keeps its address until then, what to do if
// it was not expected and a word to keep the link to oneself.
func (m *mailSettings) addressChangeMessage(to, token string, lifetime time.Duration, asker string) mail.Message {
	app := m.settings.Application

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("Confirm your new email address for %s", app),
		Text: fmt.Sprintf(`An administrator of %s asked to change the email address of a %s account to this one.

To confirm that this address is yours, open this link:
%s

The link is good for %s. The account keeps the address it has until you confirm.

Didn't expect this?
If you do not have a %s account, or do not want this change, ignore this message. Nothing changes.

Keep the link to yourself
Anyone who opens the link confirms the change in your place. Don't forward it.
`, asker, app, m.link("address", token), goodFor(lifetime), app),
	}
}

// addressChangedMessage is the notice sent to the address an account held when another
// replaced it: that it changed, to which address (masked, since the old mailbox may no
// longer be the person's alone), and what to do if the person did not change it.
func (m *mailSettings) addressChangedMessage(to, replacement string) mail.Message {
	app := m.settings.Application

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("The email address of your %s account changed", app),
		Text: fmt.Sprintf(`The email address of your %s account was changed to %s. Messages about your account now go there, and no longer to this address.

Didn't do this?
If you did not change your address and no administrator changed it for you, tell an administrator of %s right away: someone else may have your account.
`, app, maskAddress(replacement), app),
	}
}

// factorResetMessage is the notice sent when an administrator resets a person's second
// factor: every browser the person asked to be remembered is forgotten, and what that
// means at their next sign-in.
func (m *mailSettings) factorResetMessage(to string) mail.Message {
	app := m.settings.Application

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("Your %s sign-in was reset", app),
		Text: fmt.Sprintf(`An administrator reset the second step of your %s sign-in. Every browser you asked us to remember is forgotten, so the next time you sign in we will mail you a code.

Didn't expect this?
You do not need to do anything. If you want to know why, ask an administrator of %s.
`, app, app),
	}
}

// removedFromTenantMessage is the notice sent when an administrator's act leaves a
// person with no role in a tenant: where they were removed from (tenant, "" where the
// Auth has no name for it), and that the account went with it when it did.
func (m *mailSettings) removedFromTenantMessage(to, tenant string, accountDeleted bool) mail.Message {
	app := m.settings.Application
	where := tenant
	if where == "" {
		where = app
	}
	text := fmt.Sprintf("An administrator removed you from %s. You no longer have access to it in %s.\n", where, app)
	if tenant == "" {
		text = fmt.Sprintf("An administrator removed your access in %s.\n", app)
	}
	if accountDeleted {
		text += fmt.Sprintf("\nIt was the only place you had access to, so your %s account was deleted with it.\n", app)
	}
	text += fmt.Sprintf("\nDidn't expect this?\nIf you think this is a mistake, ask an administrator of %s.\n", where)

	return mail.Message{To: to, Subject: fmt.Sprintf("You were removed from %s", where), Text: text}
}

// codeMessage is the mail the second factor sends: the one-time code, how long it is
// good for (lifetime), then what to do if no sign-in was started and a word to keep
// the code to oneself. The code is spaced in the middle for reading.
func (m *mailSettings) codeMessage(to, code string, lifetime time.Duration) mail.Message {
	app := m.settings.Application
	spaced := code
	if len(code) == emailCodeLength {
		spaced = code[:emailCodeLength/2] + " " + code[emailCodeLength/2:]
	}

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("Your %s sign-in code is %s", app, spaced),
		Text: fmt.Sprintf(`Your sign-in code for %s is:

%s

Enter it on the sign-in page. The code is good for %s.

Didn't do this?
If you did not start a sign-in, ignore this message and change your password: someone else has it.

Keep the code to yourself
Nobody from %s will ask you for it. Don't share it.
`, app, spaced, goodFor(lifetime), app),
	}
}

// invitationCodeMessage is the mail that carries the code an invitation is accepted
// with: the code, that it accepts the invitation sent to this address, where to enter it
// and how long it is good for (lifetime), then what it means if the person did not open
// the invitation themselves, and a word to keep the code to oneself. The code is mailed
// only once a browser that carries the invitation's link is at the code's step, so a
// person who did not open the invitation learns here that somebody else holds its link.
// Nothing in it speaks of a password: the person invited may have no account yet. The
// code is spaced in the middle for reading.
func (m *mailSettings) invitationCodeMessage(to, code string, lifetime time.Duration) mail.Message {
	app := m.settings.Application
	spaced := code
	if len(code) == emailCodeLength {
		spaced = code[:emailCodeLength/2] + " " + code[emailCodeLength/2:]
	}

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("Your code to accept your invitation to %s is %s", app, spaced),
		Text: fmt.Sprintf(`Your code to accept your invitation to %s is:

%s

It accepts the invitation to %s that was sent to this address. Enter it on the page that asked you for it. The code is good for %s.

Didn't do this?
If you did not open your invitation yourself, someone else has its link. Don't give the code to anyone, and tell the person who invited you.

Keep the code to yourself
Only you should enter it. Nobody from %s will ask you for it. Don't share it.
`, app, spaced, app, goodFor(lifetime), app),
	}
}

// firstPasswordMessage is the mail that carries the code of a person's first password:
// the code, where to enter it and how long it is good for (lifetime), then what to do
// if they did not ask: someone at a browser they left signed in asked, and without the
// code nothing changes. The code is spaced in the middle for reading.
func (m *mailSettings) firstPasswordMessage(to, code string, lifetime time.Duration) mail.Message {
	app := m.settings.Application
	spaced := code
	if len(code) == emailCodeLength {
		spaced = code[:emailCodeLength/2] + " " + code[emailCodeLength/2:]
	}

	return mail.Message{
		To:      to,
		Subject: fmt.Sprintf("Your code to choose a %s password is %s", app, spaced),
		Text: fmt.Sprintf(`Your code to choose a password for your %s account is:

%s

Enter it on your account page, with the password you want. The code is good for %s.

Didn't do this?
If you did not ask to choose a password, ignore this message: without the code nothing changes. Someone asked from a browser where you are signed in, so sign out of the browsers you do not use.

Keep the code to yourself
Nobody from %s will ask you for it. Don't share it.
`, app, spaced, goodFor(lifetime), app),
	}
}

// send delivers msg through the configured sender. A failure is reported to the person
// as the message not having been sent, never with the mail system's words.
func (a *Auth[S, U, Tx]) send(ctx context.Context, msg mail.Message) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := a.settings.mail.sender.Send(ctx, msg); err != nil {
		return httpio.NewInternalServerErrorMessageWithError(errors.Wrap(err, "mail.Sender.Send()"), "the message could not be sent")
	}

	return nil
}

// address validates an address a person or an operator gave and returns it bare: the
// part a message is sent to, without a display name.
func address(email string) (string, error) {
	parsed, err := netmail.ParseAddress(strings.TrimSpace(email))
	if err != nil || !strings.Contains(parsed.Address, "@") {
		return "", httpio.NewBadRequestMessagef("%q is not an email address", email)
	}

	return parsed.Address, nil
}

// optionalAddress is address for a value that may be absent.
func optionalAddress(email *string) (*string, error) {
	if email == nil {
		return nil, nil
	}
	bare, err := address(*email)
	if err != nil {
		return nil, err
	}

	return &bare, nil
}

// limitMail bounds the links, notices and codes sent to an address that anybody who
// knows the address can ask for (mailLimit, under mailLimitKey): the address's record
// in the sign-in attempts table counts each one, and an address at the limit is refused
// with too_many_emails and the wait. A request takes its place under the limit before
// anything is mailed: the store counts it in one transaction and says whether it was
// counted (dbtype.SignInAttempts.Counted), and a request that was not, because the
// address is at the limit already, mails nothing. Requests made at once are so each
// counted, and no more of them than the limit are ever mailed.
func (a *Auth[S, U, Tx]) limitMail(ctx context.Context, email string) error {
	return a.limitSent(ctx, mailLimitKey(email))
}

// limitCode bounds the sign-in codes sent to the address an account holds, as limitMail
// bounds the address's other mail, in a count of its own (codeLimitKey): what a
// stranger asks for in the address's name never uses up the codes its person signs in
// with.
func (a *Auth[S, U, Tx]) limitCode(ctx context.Context, email string) error {
	return a.limitSent(ctx, codeLimitKey(email))
}

// limitInvitationCode bounds the codes sent to accept the invitation inv, as limitMail
// bounds an address's mail, in the invitation's own count (invitationCodeLimitKey): the
// holder of a forwarded link uses up that invitation's codes and nothing of the invited
// person's own sign-in.
func (a *Auth[S, U, Tx]) limitInvitationCode(ctx context.Context, inv *sessioninfo.Invitation) error {
	return a.limitSent(ctx, invitationCodeLimitKey(inv))
}

// limitSent takes a message's place under the mail limit in the count kept under key,
// and refuses it with too_many_emails and the wait when the count is at the limit.
func (a *Auth[S, U, Tx]) limitSent(ctx context.Context, key string) error {
	record, err := a.storage.RecordSignInFailure(ctx, key, mailLimit)
	if err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.RecordSignInFailure()")
	}
	if !record.Counted {
		wait, _ := record.Locked(time.Now())

		return sessioninfo.NewTooManyEmailsRefusal(wait)
	}

	return nil
}

// clearMailLimit resets the address's count once a code or link sent to it is used:
// the mail reached the person, so what they asked for counts against nothing. It is
// bookkeeping after the proof: a failure is logged, and the request goes on.
func (a *Auth[S, U, Tx]) clearMailLimit(ctx context.Context, email string) {
	if err := a.storage.ClearSignInAttempts(ctx, mailLimitKey(email)); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.ClearSignInAttempts()"))
	}
}

// clearCodeLimit resets the count of the address's sign-in codes once one is used, as
// clearMailLimit resets the address's other mail.
func (a *Auth[S, U, Tx]) clearCodeLimit(ctx context.Context, email string) {
	if err := a.storage.ClearSignInAttempts(ctx, codeLimitKey(email)); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.ClearSignInAttempts()"))
	}
}

// clearInvitationCodeLimit resets the count of the codes sent to accept inv once one is
// used, as clearMailLimit resets an address's mail.
func (a *Auth[S, U, Tx]) clearInvitationCodeLimit(ctx context.Context, inv *sessioninfo.Invitation) {
	if err := a.storage.ClearSignInAttempts(ctx, invitationCodeLimitKey(inv)); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.ClearSignInAttempts()"))
	}
}

// minPasswordLength is the shortest password any path sets.
const minPasswordLength = 8

// acceptedPassword is the one rule for a password's shape: it refuses a password
// shorter than eight characters, counted as characters and not as the bytes that
// encode them. Every path that sets a password is held to it, since
// each hashes the password through hashToSet: a sign-up and an invitation's acceptance,
// a person's first password, their own change, the password a reset link ends in, one
// set at the end of a flow of the application's own, and an account created with one.
// A path that judges the password before other work calls it first as well, so a
// refused password costs nothing. The rules beyond the length are the application's,
// on its page.
func acceptedPassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLength {
		return httpio.NewBadRequestMessagef("the password is at least %d characters", minPasswordLength)
	}

	return nil
}

// hashToSet hashes a password a path is about to set, once it is one any path may set
// (acceptedPassword). It is the one way a new password's hash is made, so no path sets
// a password the rule would refuse. The hash a sign-in upgrades is of the password the
// person already has, and is not made here (upgradePasswordHash).
func (a *Auth[S, U, Tx]) hashToSet(password string) (*securehash.Hash, error) {
	if err := acceptedPassword(password); err != nil {
		return nil, err
	}
	hash, err := a.registered().hasher.Hash(password)
	if err != nil {
		return nil, errors.Wrap(err, "securehash.SecureHasher.Hash()")
	}

	return hash, nil
}

// claimVerifiedEmail writes the address a sign-in method asserts as verified (Google
// verifies the address itself, and the person has just proven they hold the account)
// onto the user as verified, when the user holds no verified address and no other user
// holds it. It is bookkeeping after the session stands: a failure is logged.
func (a *Auth[S, U, Tx]) claimVerifiedEmail(ctx context.Context, userID impulse.UUID, identity *sessioninfo.Identity) {
	if identity == nil || !identity.EmailVerified || identity.Email == "" {
		return
	}
	if _, err := a.storage.ClaimVerifiedEmail(ctx, userID, identity.Email); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.ClaimVerifiedEmail()"))
	}
}

// claimName writes the name a sign-in method asserts onto the user when the user holds
// none: a directory's name for the person fills an account that has no name and never
// replaces one, so a name the person wrote themselves stands. It is bookkeeping after
// the session stands: a failure is logged.
func (a *Auth[S, U, Tx]) claimName(ctx context.Context, userID impulse.UUID, identity *sessioninfo.Identity) {
	if identity == nil || identity.Name == "" {
		return
	}
	if _, err := a.storage.ClaimUserName(ctx, userID, identity.Name); err != nil {
		logger.FromCtx(ctx).Error(errors.Wrap(err, "sessionstorage.AuthStore.ClaimUserName()"))
	}
}

// linkToken is a mailed link's token as the mail carries it and its hash as the row
// keeps it; it is the pending identity's token, made the same way.
func linkToken() (token string, hash []byte, err error) {
	return newPendingToken()
}

// linkTokenHash is the hash of a token a person posted back, or false when it is not
// a token.
func linkTokenHash(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != pendingTokenSize {
		return nil, false
	}

	return hashPendingToken(raw), true
}

// decodeToken is the decoder of a request that carries a token.
type tokenRequest struct {
	Token string `json:"token"`
}

var tokenDecoder = sessioncore.NewDecoder[tokenRequest]()

// errLinkDead is the answer for a token whose link is no longer live.
func errLinkDead() error {
	return httpio.NewNotFoundMessage("That link has expired or was already used. Ask for a new one")
}
