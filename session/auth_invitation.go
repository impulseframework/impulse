package session

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/cookie"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/internal/passwordstrength"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/tracer"
)

// An invitation is a mailed link that brings a person into the application, or into
// one of its tenants, with the roles it carries. The link only opens the invitation: a
// mailed link can be forwarded, so holding one proves nothing about who reads the
// mailbox it was sent to. The invitation is accepted by a sign-in that proves the
// invited address: one whose method verified it, one of an account that holds it
// confirmed, and, everywhere else, one that enters a fresh code the library mails to
// the invited address (a directory that sends no address or an unverified one, a
// sign-up with a password, a password account that holds no address). The login page
// opens the invitation (Invitation().Open()), which sets an encrypted cookie holding
// its token, so the sign-in that follows, external round trip included, carries it;
// the store accepts it once the user is known and the address proven, and the
// application's hook gives the roles in the same transaction, which it is handed. A
// person already signed in whose account holds the invited address confirmed accepts
// with Accept(). See "Invitations" in the README.
//
// An invitation may make a tenant instead of seating the person in one
// (InvitationRequest.NewTenant): staff bring an organization into the application by
// inviting one person, and that person's first sign-in makes the tenant. Such an
// invitation names no tenant and carries no roles; it carries the tenant's name, which
// the invitation page shows, whether the tenant is entered through its directory only,
// which the library holds the acceptance to, and the application's own details, which
// the library stores and hands to the acceptance hook with how the person signed in.

// invitationCookieKey is the invitation cookie's value holding the token.
const invitationCookieKey cookie.Key = "invitation"

// seatedCookieSuffix ends the name of the cookie that carries the tenant an accepted
// invitation seated a person in to the session read after it: the invitation cookie's
// name with it.
const seatedCookieSuffix = "-seated"

// seatedTenantKey and seatedUserKey are that cookie's values: the tenant, and the user
// the invitation seated in it.
const (
	seatedTenantKey cookie.Key = "tenant"
	seatedUserKey   cookie.Key = "user"
)

// invitationCtxKey is the context key for the hash of the browser's invitation token,
// set by StartSession.
type invitationCtxKey struct{}

// withInvitationCookie returns ctx with the hash of the invitation token from the
// invitation cookie, if the browser carries one.
func (a *Auth[S, U, Tx]) withInvitationCookie(ctx context.Context, r *http.Request) context.Context {
	cval, found, err := a.invitationCookies.Cookie().Read(r, a.settings.invitationCookie)
	if err != nil || !found {
		return ctx
	}
	token, err := cval.GetString(invitationCookieKey)
	if err != nil {
		return ctx
	}
	hash, ok := linkTokenHash(token)
	if !ok {
		logger.FromCtx(ctx).Warn("invitation cookie ignored: not a link token")

		return ctx
	}

	return context.WithValue(ctx, invitationCtxKey{}, hash)
}

// invitationTokenHash returns the hash of the browser's invitation token from ctx.
func invitationTokenHash(ctx context.Context) ([]byte, bool) {
	hash, ok := ctx.Value(invitationCtxKey{}).([]byte)

	return hash, ok
}

// invitationOf returns the live invitation the browser carries, or nil when it
// carries none or its invitation is no longer live.
func (a *Auth[S, U, Tx]) invitationOf(ctx context.Context) (*sessioninfo.Invitation, error) {
	hash, ok := invitationTokenHash(ctx)
	if !ok {
		return nil, nil
	}
	link, err := a.storage.MailLink(ctx, hash)
	if err != nil {
		if httpio.HasNotFound(err) {
			return nil, nil
		}

		return nil, errors.Wrap(err, "sessionstorage.AuthStore.MailLink()")
	}
	if link.Kind != sessionstorage.MailLinkInvitation {
		return nil, nil
	}

	return invitationOf(link), nil
}

// invitationStandsFor reports whether an invitation that stands was mailed to email:
// one the store holds as live (not used, not withdrawn, not run out), whatever the
// browser carries. An invitation that has taken its twenty-five codes still stands, and
// counts here.
func (a *Auth[S, U, Tx]) invitationStandsFor(ctx context.Context, email string) (bool, error) {
	links, err := a.storage.LiveMailLinks(ctx, sessionstorage.MailLinkInvitation)
	if err != nil {
		return false, errors.Wrap(err, "sessionstorage.AuthStore.LiveMailLinks()")
	}
	for _, link := range links {
		if sameAddress(strings.TrimSpace(link.Email), email) {
			return true, nil
		}
	}

	return false, nil
}

// invitationOf renders a mail link row as the invitation a sign-in carries.
func invitationOf(link *sessionstorage.MailLink) *sessioninfo.Invitation {
	return &sessioninfo.Invitation{
		ID: link.ID, Email: link.Email, Name: link.Name, TenantID: link.TenantID, EveryTenant: link.EveryTenant, RoleNames: link.RoleNames,
		InvitedByUserID: link.InvitedByUserID, CreatedAt: link.CreatedAt, ExpiresAt: link.ExpiresAt, NewTenant: cloneNewTenant(link.NewTenant),
	}
}

// cloneNewTenant is the caller's own copy of the tenant an invitation makes, nil for
// none.
func cloneNewTenant(tenant *sessioninfo.NewTenant) *sessioninfo.NewTenant {
	if tenant == nil {
		return nil
	}
	clone := *tenant
	clone.Details = slices.Clone(tenant.Details)

	return &clone
}

// madeTenant says what an invitation's answers say of the tenant it makes: that it
// makes one, and whether it is accepted through an organization's directory alone.
// Both are false for an invitation that makes none.
func madeTenant(tenant *sessioninfo.NewTenant) (makes, directoryOnly bool) {
	if tenant == nil {
		return false, false
	}

	return true, tenant.DirectoryOnly
}

// invitedTenantName is what people call the tenant an invitation leads to: the name
// the invitation carries for a tenant it makes, and the application's name for the
// tenant it seats the person in (tenantName), "" where neither says.
func (a *Auth[S, U, Tx]) invitedTenantName(tenantID string, tenant *sessioninfo.NewTenant) string {
	if tenant != nil {
		return tenant.Name
	}

	return a.tenantName(tenantID)
}

// endInvitation deletes the invitation cookie once a session stands, whether the
// sign-in accepted the invitation or found it dead; the latter is logged.
func (a *Auth[S, U, Tx]) endInvitation(ctx context.Context, w http.ResponseWriter, inv *sessioninfo.Invitation) {
	if _, ok := invitationTokenHash(ctx); !ok {
		return
	}
	if inv != nil && !inv.Accepted {
		logger.FromCtx(ctx).Warnf("the invitation %s the sign-in carried had expired or was already used: nothing was granted", inv.ID)
	}
	a.invitationCookies.Cookie().Delete(w, a.settings.invitationCookie, a.invitationCookies.Domain)
}

// seatedCookie names the cookie that carries the tenant an accepted invitation seated
// a person in from the acceptance to the first session read after it.
func (a *Auth[S, U, Tx]) seatedCookie() string {
	return a.settings.invitationCookie + seatedCookieSuffix
}

// noteSeated has the browser carry the tenant the accepted invitation inv seated
// userID in, so that the first session read after the acceptance names it
// (Authenticated's invitedTenant) and the application opens for the person in the
// tenant they were invited to, whatever else they hold. The tenant is the one the
// invitation names, or the one its acceptance made, which the application's
// acceptance hook tells the library (sessioninfo.Invitation.MadeTenantID). Nothing is
// written for an invitation that was not accepted, or that seated the person in no
// tenant the library knows: one into the application's global surface, and one that
// makes a tenant whose hook did not say which. The cookie is the server's alone to
// read, and lives as long as a pending sign-in may wait, since an acceptance can come
// a step before its session.
func (a *Auth[S, U, Tx]) noteSeated(w http.ResponseWriter, userID impulse.UUID, inv *sessioninfo.Invitation) {
	if inv == nil || !inv.Accepted || inv.SeatedTenantID() == "" || userID == impulse.NilUUID {
		return
	}
	cval := cookie.NewValues().SetString(seatedTenantKey, inv.SeatedTenantID()).SetString(seatedUserKey, userID.String())
	a.invitationCookies.Cookie().WritePersistentCookie(w, a.seatedCookie(), a.invitationCookies.Domain, true, http.SameSiteLaxMode, a.settings.pendingTimeout, cval)
}

// takeSeated answers the tenant the browser carries from an acceptance by userID, ""
// for none, and has the browser stop carrying it: the tenant is told once, to the
// first session read after the acceptance. A tenant carried for another user, from an
// acceptance whose sign-in was never finished on this browser, is told to nobody and
// goes too.
func (a *Auth[S, U, Tx]) takeSeated(w http.ResponseWriter, r *http.Request, userID impulse.UUID) string {
	cval, found, err := a.invitationCookies.Cookie().Read(r, a.seatedCookie())
	if err != nil || !found {
		return ""
	}
	a.invitationCookies.Cookie().Delete(w, a.seatedCookie(), a.invitationCookies.Domain)
	seated, err := cval.GetString(seatedUserKey)
	if err != nil || seated != userID.String() {
		return ""
	}
	tenant, err := cval.GetString(seatedTenantKey)
	if err != nil {
		return ""
	}

	return tenant
}

// InvitationHandlers are the HTTP handlers of invitations.
type InvitationHandlers struct {
	open   http.HandlerFunc
	accept http.HandlerFunc
	signUp http.HandlerFunc
}

// Open accepts {"token"}, the token the mailed link carried, and answers {"email",
// "name", "tenantId", "expiresAt", "signUp", "usernameIsEmail", "methods",
// "accountExists", "hasPassword"}: the address the invitation was sent to, the person's
// name as the inviter gave it (absent for none), the tenant it seats the person in
// (absent for the application's global surface), when it stops being accepted, whether
// the person may create an account with a password here (the password method is
// registered), whether that account's username is the address, the names of the listed
// external registrations that could admit the address (as the methods listing advertises
// them; empty when none could), so the page hides the methods that would refuse the
// person, and whether an account already holds the address and signs in with a password.
// An invitation that makes a tenant answers "newTenant" true and the tenant's name as the
// invitation carries it ("tenantName"; it has no id yet), and "directoryOnly" true where
// it is accepted through an organization's directory alone: "signUp" is then false, since
// an account with a password could not accept it, and the page offers the directory
// buttons. It sets the invitation cookie, so the sign-in that follows carries the
// invitation. A token whose invitation is no longer live is 404. Route it behind
// StartSession and ValidateXSRFToken.
func (h *InvitationHandlers) Open() http.HandlerFunc { return h.open }

// Accept accepts the invitation the browser carries for the signed-in user, whose
// account holds the invited address confirmed: the invitation is consumed and the
// application's hook grants what it names, in one transaction. An account that holds
// another address is refused (403, naming the invited address), and so is one that
// holds no address, or holds the invited one and has not confirmed it: a session
// cannot wait for the code that would prove the address, so the answer says to sign
// in again from the invitation's link, where the code is mailed. The
// hook is told how the session was established (its first auth event), and an
// invitation that makes a tenant entered through its directory only is refused (403,
// naming what to do) for a session that came through no organization's directory.
// Every refusal leaves the invitation standing. It answers {"accepted": true}; 404
// when the browser carries no live invitation. An invitation into a tenant has the
// next session read name the tenant (Authenticated's invitedTenant). Route it behind
// ValidateSession.
func (h *InvitationHandlers) Accept() http.HandlerFunc { return h.accept }

// SignUp accepts {"password", "username"} and starts the invited person's account with
// a password. Nothing is made yet: the link only opened the invitation, so the sign-up
// waits, as a pending sign-in does, for the code the library mails to the invited
// address, and it answers as a password login held for a second factor does
// ({"mfaIsRequired": true}). Entering the code (Pending().VerifyCode()) makes the
// account, with the invited address as its own, verified, signs the person in and
// accepts the invitation. username is the address when the password method declares
// the username as the email address, and chosen otherwise. A taken username or
// address is 409, a password the judgment refuses 400, no password method or no
// live invitation 404. An invitation that makes a tenant entered through its
// directory only is refused as a sign-in is (organization_account_required). Route it
// beside the password login, behind StartSession and ValidateXSRFToken, and route the
// pending handlers, which take the code.
func (h *InvitationHandlers) SignUp() http.HandlerFunc { return h.signUp }

// Invitation returns the invitation handlers.
func (a *Auth[S, U, Tx]) Invitation() *InvitationHandlers {
	return &InvitationHandlers{
		open:   a.serving(a.invitationOpen()),
		accept: a.serving(a.invitationAccept()),
		signUp: a.serving(a.invitationSignUp()),
	}
}

// invitationOpen is Invitation().Open().
func (a *Auth[S, U, Tx]) invitationOpen() http.HandlerFunc {
	type response struct {
		Email string `json:"email"`
		// Name is the person's name as the inviter gave it, absent for none: the page
		// sends it with the password it asks the server about (invitedAccountState).
		Name string `json:"name,omitempty"`
		// TenantID is the tenant the invitation seats the person in, and TenantName what
		// people call it (SetTenantNames); both absent for the application's global
		// surface, and the name absent where no namer knows the tenant.
		TenantID   string `json:"tenantId,omitempty"`
		TenantName string `json:"tenantName,omitempty"`
		// NewTenant says the invitation makes a tenant, whose name TenantName then is
		// and which has no id yet, and DirectoryOnly that it is accepted through an
		// organization's directory alone.
		NewTenant       bool      `json:"newTenant"`
		DirectoryOnly   bool      `json:"directoryOnly"`
		ExpiresAt       time.Time `json:"expiresAt"`
		SignUp          bool      `json:"signUp"`
		UsernameIsEmail bool      `json:"usernameIsEmail"`
		Methods         []string  `json:"methods"`
		// AccountExists and HasPassword: whether an account already holds the invited
		// address, and whether it signs in with a password (see standingInvitationAnswer).
		AccountExists bool `json:"accountExists"`
		HasPassword   bool `json:"hasPassword"`
	}

	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		req, err := tokenDecoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		link, err := a.liveLink(ctx, req.Token, sessionstorage.MailLinkInvitation)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		// Lax, so the cookie also reaches the login page an external method's callback
		// redirects to; the sign-in it joins is behind the method's own checks.
		cval := cookie.NewValues().SetString(invitationCookieKey, req.Token)
		a.invitationCookies.Cookie().WritePersistentCookie(w, a.settings.invitationCookie, a.invitationCookies.Domain, true, http.SameSiteLaxMode,
			time.Until(link.ExpiresAt), cval)
		methods := a.registered()
		invited, err := a.invitedAccount(ctx, link.Email)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		makes, directoryOnly := madeTenant(link.NewTenant)

		return httpio.NewEncoder(w).Ok(response{
			Email: link.Email, Name: link.Name, TenantID: link.TenantID, TenantName: a.invitedTenantName(link.TenantID, link.NewTenant), NewTenant: makes, DirectoryOnly: directoryOnly,
			ExpiresAt: link.ExpiresAt, SignUp: methods.passwordRegistered() && !directoryOnly, UsernameIsEmail: methods.usernameIsEmail(),
			Methods: invited.methods, AccountExists: invited.exists, HasPassword: invited.hasPassword,
		})
	})
}

// invitedAccountState is what an invitation's answers say of the invited address: whether an
// account already holds it, verified or not, whether that account signs in with a
// password, and the external registrations through which the invitation can be
// accepted. What the login page shows the invitee turns on it: the password form for an
// account with one, the account form where none holds the address, and a button for
// each registration named. Whoever holds the link is told it, and the name the inviter
// gave beside it: the link was mailed to the address, what it tells of the account is
// no more than which form to show, and the name is what the page sends with the
// password it asks the server about, so the bar is judged against what the acceptance
// judges the password against.
type invitedAccountState struct {
	exists      bool
	hasPassword bool
	methods     []string
}

// invitedAccount answers what holds the address an invitation was mailed to
// (invitedAccountState) and the registrations that can accept it: those that could admit the
// address (admittingMethodNames), for an address no account holds, and for an account
// that has a password or that nobody signs in to yet, since a password confirms a new
// identity's link and a first identity is linked by the invitation itself. An account
// with no password that already signs in through an identity is linked no identity of
// another connection, so only the registrations it signs in through are named
// (usedMethodNames): the page offers no button whose sign-in the rules would refuse.
func (a *Auth[S, U, Tx]) invitedAccount(ctx context.Context, email string) (*invitedAccountState, error) {
	admitting := a.admittingMethodNames(email)
	user, err := a.storage.UserByEmail(ctx, email)
	if err != nil {
		if httpio.HasNotFound(err) {
			return &invitedAccountState{exists: false, hasPassword: false, methods: admitting}, nil
		}

		return nil, errors.Wrap(err, "sessionstorage.AuthStore.UserByEmail()")
	}
	if user.PasswordHash != nil {
		return &invitedAccountState{exists: true, hasPassword: true, methods: admitting}, nil
	}
	links, err := a.storage.IdentitiesByUser(ctx, user.ID)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.IdentitiesByUser()")
	}
	if len(links) == 0 {
		return &invitedAccountState{exists: true, hasPassword: false, methods: admitting}, nil
	}

	return &invitedAccountState{exists: true, hasPassword: false, methods: a.usedMethodNames(admitting, links)}, nil
}

// usedMethodNames are the names among admitting of the registrations that one of links
// signs its person in through (signsInThrough), in admitting's order. The list is
// empty, never nil, when none does.
func (a *Auth[S, U, Tx]) usedMethodNames(admitting []string, links []*sessionstorage.SessionIdentity) []string {
	used := []string{}
	for _, name := range admitting {
		through := func(link *sessionstorage.SessionIdentity) bool {
			return slices.Contains(a.signsInThrough(link.Method, link.Connection), name)
		}
		if slices.ContainsFunc(links, through) {
			used = append(used, name)
		}
	}

	return used
}

// liveLink returns the live link of kind that token names, or a NotFound error.
func (a *Auth[S, U, Tx]) liveLink(ctx context.Context, token string, kind sessionstorage.MailLinkKind) (*sessionstorage.MailLink, error) {
	hash, ok := linkTokenHash(token)
	if !ok {
		return nil, errLinkDead()
	}
	link, err := a.storage.MailLink(ctx, hash)
	if err != nil {
		if httpio.HasNotFound(err) {
			return nil, errLinkDead()
		}

		return nil, errors.Wrap(err, "sessionstorage.AuthStore.MailLink()")
	}
	if link.Kind != kind {
		return nil, errLinkDead()
	}

	return link, nil
}

// invitationAccept is Invitation().Accept().
func (a *Auth[S, U, Tx]) invitationAccept() http.HandlerFunc {
	type response struct {
		Accepted bool `json:"accepted"`
	}

	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		ctx, err := a.validateRequest(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := sessioncore.RefuseImpersonated(ctx, a.baseSession, "Invitation().Accept()", true); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		inv, err := a.invitationOf(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if inv == nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewNotFoundMessage("the browser carries no live invitation"))
		}
		user := sessioninfo.UserFromCtx(ctx)
		// How the session was established is how the person signed in: the acceptance
		// is held to it and the hook reads it.
		if established, ok := sessioninfo.DataFromCtx(ctx).EstablishedBy(); ok {
			inv.SignInMethod, inv.SignInConnection = established.Method, established.Connection
		}
		if err := a.storage.AcceptInvitation(ctx, user.ID, user.Username, addressOf(user.Email), inv, a.acceptInvitation); err != nil {
			// A dead invitation is nothing to carry any more. The hook's own error,
			// whatever it is (a role that is not found, say), leaves the invitation
			// standing and the browser carrying it.
			if errors.Is(err, signin.ErrInvitationExpired) {
				a.invitationCookies.Cookie().Delete(w, a.settings.invitationCookie, a.invitationCookies.Domain)
			}
			// The person signed in holds another address, or came through no
			// organization's directory where the invitation asks for one: the invitation
			// stands, and the browser keeps carrying it.
			if sentence := standingRefusalSentence(err, inv); sentence != "" {
				return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewForbiddenMessageWithError(err, sentence))
			}

			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		a.invitationCookies.Cookie().Delete(w, a.settings.invitationCookie, a.invitationCookies.Domain)
		a.noteSeated(w, user.ID, inv)

		return httpio.NewEncoder(w).Ok(response{Accepted: true})
	})
}

// addressOf is the address a user holds, verified or not, "" for none.
func addressOf(email *string) string {
	if email == nil {
		return ""
	}

	return *email
}

// standingRefusalSentence is what a signed-in person is told when their acceptance of
// inv by hand was refused for who they are signed in as, the invitation left standing:
// the invited address where they hold another (invitation_address), to sign in again
// from the invitation's link where their account has not confirmed the invited address
// (invitation_address with signin.ErrInvitationCode among its causes), to sign in with
// their organization's account where the invitation asks for one
// (organization_account_required), and that their organization is set up already where
// the application refused to make a second tenant of it (organization_has_tenant). ""
// for any other error.
func standingRefusalSentence(err error, inv *sessioninfo.Invitation) string {
	var refusal *sessioninfo.LoginRefusal
	if !errors.As(err, &refusal) {
		return ""
	}
	if refusal.Code() == sessioninfo.RefusedInvitationAddress {
		if errors.Is(err, signin.ErrInvitationCode) {
			return signinflow.InvitedCodeMessage(inv)
		}

		return signinflow.InvitedAddressMessage(inv)
	}
	if refusal.Code() == sessioninfo.RefusedOrganizationAccountRequired {
		return signinflow.OrganizationAccountMessage
	}
	if refusal.Code() == sessioninfo.RefusedOrganizationHasTenant {
		return signinflow.OrganizationHasTenantMessage
	}

	return ""
}

// invitationSignUp is Invitation().SignUp().
func (a *Auth[S, U, Tx]) invitationSignUp() http.HandlerFunc {
	type request struct {
		Password string `json:"password"`
		Username string `json:"username"`
	}
	decoder := sessioncore.NewDecoder[request]()

	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		req, err := decoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if !a.registered().passwordRegistered() {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewNotFoundMessage("an account with a password is not offered here"))
		}
		inv, err := a.invitationOf(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if inv == nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewNotFoundMessage("the browser carries no live invitation"))
		}
		// An account with a password could not accept an invitation that is accepted
		// through an organization's directory alone: it is refused as its sign-in would
		// be, before anything is held or mailed.
		if _, directoryOnly := madeTenant(inv.NewTenant); directoryOnly {
			return writeSignInError(ctx, w, signinflow.Refusal("", sessioninfo.RefusedOrganizationAccountRequired, signin.ErrOrganizationAccountRequired, signinflow.OrganizationAccountMessage))
		}
		pending, err := a.holdInvitedSignUp(ctx, w, inv, req.Username, req.Password)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return a.answerSignIn(ctx, w, r, &signInOutcome{pending: pending})
	})
}

// heldSignUp is what a sign-up through an invitation keeps until the code mailed to the
// invited address is entered: the invitation it was started for, the username the
// account will have and the hash of the password the person chose. It travels in the
// encrypted pending cookie, beside the token of the wait it belongs to, and nowhere
// else: no account exists before the code, and the pending row holds none of it.
type heldSignUp struct {
	InvitationID impulse.UUID     `json:"invitationId"`
	Username     string           `json:"username"`
	PasswordHash *securehash.Hash `json:"passwordHash"`
}

// heldSignUpCtxKey is the context key for what the browser's pending cookie keeps of a
// sign-up through an invitation, set by StartSession.
type heldSignUpCtxKey struct{}

// heldSignUpOf returns what the browser's pending cookie keeps of a sign-up through an
// invitation, from ctx.
func heldSignUpOf(ctx context.Context) (*heldSignUp, bool) {
	held, ok := ctx.Value(heldSignUpCtxKey{}).(*heldSignUp)

	return held, ok && held != nil && held.PasswordHash != nil && held.Username != ""
}

// signsUpInvited reports whether the pending identity p is a sign-up through an
// invitation that waits for its code: a password identity that names nobody, since the
// account is made once the code is entered.
func signsUpInvited(p *pendingState) bool {
	return !p.UserID.Valid && p.Identity.Method == sessioninfo.MethodPassword
}

// holdInvitedSignUp holds a sign-up with a password through the invitation inv as the
// browser's pending identity, waiting for the code mailed to the invited address. The
// link only opened the invitation, and whoever holds it may have been forwarded it, so
// nothing is made on its strength: no account, no address held as verified and no
// acceptance. The sign-up is checked as the account's creation will be (the password's
// strength, a username chosen, the address and the username free, 409 otherwise), so the
// person is told before any code is mailed; the creation itself is held to the same
// again. The wait names nobody, its code goes to the invited address alone
// (codeDestination), and what the account is made from waits in the pending cookie
// (heldSignUp).
func (a *Auth[S, U, Tx]) holdInvitedSignUp(ctx context.Context, w http.ResponseWriter, inv *sessioninfo.Invitation, username, password string) (*pendingState, error) {
	insert, err := a.newAccount(inv.Email, username, password, inv.Name)
	if err != nil {
		return nil, err
	}
	if err := a.accountFree(ctx, insert); err != nil {
		return nil, err
	}
	wait := &signin.PendingSignInError{Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUID{}, Username: "", Tenant: "", Factors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}}

	return a.holdPending(ctx, w, &signInAttempt{
		identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Email: inv.Email},
		reason:   sessioninfo.ReasonLogin,
		sameSite: sessioncore.SameSiteStrict,
		signUp:   &heldSignUp{InvitationID: inv.ID, Username: insert.Username, PasswordHash: insert.PasswordHash},
	}, wait)
}

// accountFree answers 409, in the words the account's creation would, where another
// account holds the address or the username of insert.
func (a *Auth[S, U, Tx]) accountFree(ctx context.Context, insert *sessionstorage.InsertSessionUser) error {
	switch _, err := a.storage.UserByEmail(ctx, *insert.Email); {
	case err == nil:
		return httpio.NewConflictMessagef("the email address %q is already in use", *insert.Email)
	case !httpio.HasNotFound(err):
		return errors.Wrap(err, "sessionstorage.AuthStore.UserByEmail()")
	}
	switch _, err := a.storage.UserByUserName(ctx, insert.Username); {
	case err == nil:
		return httpio.NewConflictMessagef("username %q already exists", insert.Username)
	case !httpio.HasNotFound(err):
		return errors.Wrap(err, "sessionstorage.AuthStore.UserByUserName()")
	}

	return nil
}

// invitedSignUpAttempt makes the account of the sign-up through an invitation p holds,
// once the code mailed to the invited address was entered (email is where the code
// went), and returns the account's first sign-in, which accepts the invitation. The
// account is made from what the sign-up kept in the pending cookie (heldSignUp) and
// holds the invited address, verified: the code is what proved it. Nothing is made
// where the browser no longer carries what the sign-up kept, or carries another
// invitation than the one it was started for (answered as a wait that ran out), where
// the invitation is no longer live (invitation_expired), or where the code was mailed
// to another address than the invited one (answered as a code that died). The wait is
// consumed before the account is made, so it completes once.
func (a *Auth[S, U, Tx]) invitedSignUpAttempt(ctx context.Context, p *pendingState, email string) (*signInAttempt, error) {
	held, ok := heldSignUpOf(ctx)
	if !ok {
		return nil, errPendingExpired()
	}
	inv, err := a.invitationOf(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case inv == nil:
		return nil, errInvitationEnded()
	case inv.ID != held.InvitationID:
		return nil, errPendingExpired()
	case !sameAddress(strings.TrimSpace(email), strings.TrimSpace(inv.Email)):
		return nil, errCodeExpired()
	}
	if err := a.consumePending(ctx, p); err != nil {
		return nil, err
	}
	insert := &sessionstorage.InsertSessionUser{Username: held.Username, PasswordHash: held.PasswordHash, Email: &inv.Email, EmailVerified: true}
	if inv.Name != "" {
		insert.Name = &inv.Name
	}
	user, err := a.storage.CreateUser(ctx, insert, nil)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.CreateUser()")
	}

	// The account's first sign-in rests on the password it was just made with, records
	// the code after the password, and is the invitation's acceptance or it is nothing.
	at, err := a.passwordAttempt(user)
	if err != nil {
		return nil, err
	}
	at.identity.Email, at.identity.EmailVerified = inv.Email, true
	at.reason = sessioninfo.ReasonStepUp
	at.events = append(pendingEvents(p), sessioninfo.AuthEvent{Method: sessioninfo.MethodEmailCode})
	at.returnURL, at.fromPending = p.ReturnURL, true
	at.codeInvitation = impulse.NullUUIDFromUUID(inv.ID)

	return at, nil
}

// newAccount is the account a sign-up or an invitation's sign-up makes, as a row to
// write: the password the person chose, the address they proved they read, verified
// (by following the link they asked to be mailed at a sign-up, by entering the code
// mailed to the invited address at an invitation's), and the username, which is the
// address when the password method declares them one and the person's choice
// otherwise. name is the person's name as the invitation carried it, "" for none and
// for a sign-up, which carries none. The password is judged against the username the
// account will have, as every later judgment of it is (detailsOf).
func (a *Auth[S, U, Tx]) newAccount(email, username, password, name string) (*sessionstorage.InsertSessionUser, error) {
	if a.registered().usernameIsEmail() {
		username = email
	}
	who := passwordstrength.Details{Username: username, Name: name, Email: email}
	if err := a.strongEnough(password, who); err != nil {
		return nil, err
	}
	if username == "" {
		return nil, httpio.NewBadRequestMessage("choose a username")
	}
	hash, err := a.hashToSet(password, who)
	if err != nil {
		return nil, err
	}
	insert := &sessionstorage.InsertSessionUser{Username: username, PasswordHash: hash, Email: &email, EmailVerified: true}
	if name != "" {
		insert.Name = &name
	}

	return insert, nil
}

// passwordAttempt is the sign-in of a user who just set their password: a password
// identity naming the user, resting on the password the user was created with, so the
// session, or the pending sign-in it is held as, is the holder's of that password only.
func (a *Auth[S, U, Tx]) passwordAttempt(user *sessionstorage.SessionUser) (*signInAttempt, error) {
	proof, err := sessionstorage.ProofOfPassword(user.PasswordHash)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.ProofOfPassword()")
	}

	return &signInAttempt{
		identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()},
		reason:   sessioninfo.ReasonLogin,
		userID:   user.ID,
		username: user.Username,
		proof:    proof,
		sameSite: sessioncore.SameSiteStrict,
	}, nil
}

// InvitationRequest is an invitation to send.
type InvitationRequest struct {
	// Email is the address to invite.
	Email string
	// Name is the person's name as the inviter gives it, "" for none: the account the
	// acceptance creates carries it unless the sign-in asserts a name of its own, and
	// an existing account that holds none takes it.
	Name string
	// TenantID is the tenant the invitation seats the person in, "" for an invitation
	// that names no one tenant.
	TenantID string
	// EveryTenant says the roles are held in every tenant. With TenantID it is the
	// place the invitation stores for its roles: the tenant TenantID names, every
	// tenant when EveryTenant is set, the application's global surface when neither
	// is. The acceptance gives each role in that place and nowhere else. A request
	// that sets it and names a tenant, or makes one, is a BadRequest.
	EveryTenant bool
	// RoleNames are the roles the acceptance grants, as the application's hook
	// understands them.
	RoleNames []string
	// InvitedByUserID is the user sending the invitation, null when unknown.
	InvitedByUserID impulse.NullUUID
	// NewTenant is the tenant the invitation makes when it is accepted, nil for an
	// invitation into a tenant that exists or into the application's global surface.
	// An invitation that makes a tenant names none and carries no roles (TenantID is
	// "" and RoleNames empty): the application's hook makes the tenant from the
	// details and seats the person in it. The tenant's name is required, since the
	// invitation page shows it.
	NewTenant *sessioninfo.NewTenant
}

// onePlace holds a request to one place for its roles: a request for every tenant that
// names a tenant too, or makes one, is a BadRequest, since the acceptance gives each
// role in the place the invitation stores and nowhere else.
func (req *InvitationRequest) onePlace() error {
	if !req.EveryTenant {
		return nil
	}
	if req.TenantID != "" {
		return httpio.NewBadRequestMessage("An invitation gives its roles in one tenant or in every tenant, not in both. Name the tenant, or every tenant.")
	}
	if req.NewTenant != nil {
		return httpio.NewBadRequestMessage("An invitation that makes a tenant gives no roles in every tenant: " +
			"whoever accepts it becomes the new tenant's first administrator. Send a second invitation for every tenant.")
	}

	return nil
}

// newTenant holds the tenant a request's invitation makes to what such an invitation
// is, and answers it as it is stored: the name without the space around it, and the
// caller's details copied. Nil for a request that makes none. A request that makes a
// tenant and names one, or carries roles, is a BadRequest, and so is a tenant with no
// name.
func (req *InvitationRequest) newTenant() (*sessioninfo.NewTenant, error) {
	if req.NewTenant == nil {
		return nil, nil
	}
	if req.TenantID != "" || len(req.RoleNames) > 0 {
		return nil, httpio.NewBadRequestMessage("An invitation that makes a tenant names no tenant and carries no roles: " +
			"the tenant does not exist yet, and whoever accepts it becomes its first administrator.")
	}
	tenant := cloneNewTenant(req.NewTenant)
	tenant.Name = strings.TrimSpace(tenant.Name)
	if tenant.Name == "" {
		return nil, httpio.NewBadRequestMessage("An invitation that makes a tenant needs the tenant's name: the invited person is shown it before they sign in.")
	}

	return tenant, nil
}

// Invite sends an invitation to req.Email: a link to the login page that stands for
// MailSettings.InvitationTimeout, which whoever follows it accepts by signing in or
// signing up. The request is the same whether an account holds the address or not;
// only the invitee's path differs, and the caller does not learn which. An address
// that no sign-in method the login page offers could admit (an address outside the
// Google Workspaces a registration names, say, with no password method offered) is
// refused with a message naming its domain, and nothing is stored or mailed, so the
// sender learns now what the invitee would learn at the sign-in. The invitation is
// returned as stored; a message that could not be sent is an error and the invitation
// is ended, not mailed.
func (p *AuthAPI[S, U, Tx]) Invite(ctx context.Context, req *InvitationRequest) (*sessioninfo.Invitation, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	m, email, err := p.auth.invitee(req.Email)
	if err != nil {
		return nil, err
	}
	name, err := invitedName(req.Name)
	if err != nil {
		return nil, err
	}
	if err := req.onePlace(); err != nil {
		return nil, err
	}
	newTenant, err := req.newTenant()
	if err != nil {
		return nil, err
	}
	token, hash, err := linkToken()
	if err != nil {
		return nil, err
	}
	lifetime := m.settings.InvitationTimeout
	link := &sessionstorage.MailLink{
		TokenHash: hash, Kind: sessionstorage.MailLinkInvitation, Email: email, Name: name, TenantID: req.TenantID, EveryTenant: req.EveryTenant, RoleNames: req.RoleNames,
		InvitedByUserID: req.InvitedByUserID, NewTenant: newTenant, ExpiresAt: time.Now().Add(lifetime),
	}
	if err := p.auth.storage.InsertMailLink(ctx, link); err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.InsertMailLink()")
	}
	if err := p.auth.sendInvitation(ctx, m, link.ID, email, token, lifetime); err != nil {
		return nil, err
	}

	return invitationOf(link), nil
}

// invitedName is the name an invitation carries as it is stored: without the space
// around it, "" for none. A name longer than a user row keeps is a BadRequest, so the
// inviter learns it now and not the invitee at the sign-in.
func invitedName(name string) (string, error) {
	clean, err := sessioninfo.CleanName(&name)
	if err != nil {
		return "", errors.Wrap(err, "sessioninfo.CleanName()")
	}
	if clean == nil {
		return "", nil
	}

	return *clean, nil
}

// invitee applies the checks an invitation's address is held to before anything is
// stored or mailed: the address is one, and a sign-in method the login page offers
// could admit it. It answers the mail settings and the address bare.
func (a *Auth[S, U, Tx]) invitee(to string) (*mailSettings, string, error) {
	m := a.settings.mail
	email, err := address(to)
	if err != nil {
		return nil, "", err
	}
	if !a.couldAdmitAddress(email) {
		return nil, "", httpio.NewBadRequestMessagef("No sign-in method of this application accepts an address at %s.", signin.AddressDomain(email))
	}

	return m, email, nil
}

// sendInvitation mails the stored invitation id its link. A message that could not be
// sent ends the invitation and marks it as not mailed, so no link stands that nobody
// holds and a list of the links says why this one ended, and is the error.
func (a *Auth[S, U, Tx]) sendInvitation(ctx context.Context, m *mailSettings, id impulse.UUID, email, token string, lifetime time.Duration) error {
	if err := a.send(ctx, m.invitationMessage(email, token, lifetime)); err != nil {
		if rerr := a.storage.FailMailLink(ctx, id); rerr != nil {
			logger.FromCtx(ctx).Error(errors.Wrap(rerr, "sessionstorage.AuthStore.FailMailLink()"))
		}

		return err
	}

	return nil
}

// InvitationRow is the row a prepared invitation is stored as, in the mail links table
// (Tables.MailLinks), for a caller that writes it in a transaction of its own. The
// caller writes every field to the column of its name (TokenHash as the bytes they
// are), Kind as sessionstorage.MailLinkInvitation, CreatedAt as the commit time, and
// leaves UserId and ConsumedAt null; TenantId is null for an invitation that names no
// one tenant (""), EveryTenant is written as it is, true or false, InvitedByUserID is
// null when unknown, and NewTenant null for an invitation that makes no tenant. The row
// is what the sign-in reads when the link is followed, so a row written otherwise is an
// invitation nobody can accept, or one whose roles are given in another place.
type InvitationRow struct {
	// ID is the invitation's id.
	ID impulse.UUID
	// TokenHash is the hash the link's token is found by. The token itself is in the
	// message alone; the hash is a secret of the table and is served nowhere.
	TokenHash []byte
	// Email is the invited address, bare.
	Email string
	// Name is the invited person's name as the inviter gave it, without the space
	// around it; "" for none.
	Name string
	// TenantID is the tenant the acceptance seats the person in, "" for an invitation
	// that names no one tenant.
	TenantID string
	// EveryTenant says the roles are held in every tenant; with TenantID "" and
	// EveryTenant false they are held in the application's global surface.
	EveryTenant bool
	// RoleNames are the roles the acceptance grants.
	RoleNames []string
	// InvitedByUserID is the user who sent the invitation, null when unknown.
	InvitedByUserID impulse.NullUUID
	// NewTenant is the tenant the invitation makes when it is accepted, nil for none.
	// The caller writes the NewTenant column as NewTenantColumn answers it: the
	// tenant's JSON document, and null when it is nil.
	NewTenant *sessioninfo.NewTenant
	// ExpiresAt is when the link stops being accepted.
	ExpiresAt time.Time
}

// NewTenantColumn renders the row's NewTenant as the mail links table's NewTenant
// column holds it: the tenant's JSON document, or nil, which is NULL, for an invitation
// that makes none. It is what the library's own storage writes for an invitation it
// stores, so a row a caller stores with it is read back as the same tenant.
func (r *InvitationRow) NewTenantColumn() (*string, error) {
	column, err := dbtype.NewTenantColumn(r.NewTenant)
	if err != nil {
		return nil, errors.Wrap(err, "dbtype.NewTenantColumn()")
	}

	return column, nil
}

// PreparedInvitation is an invitation checked and made, and neither stored nor mailed
// yet: the row to store, and the step that mails its link. The token, the message and
// the sender stay inside it. It is for a caller that stores the row in a transaction of
// its own beside other writes (the generated user management): the row is written
// there, and Send runs once that transaction has committed, since a message cannot be
// taken back if the commit fails. It is an interface so that code written over one is
// tested without an Auth; the Auth's is the only one that mails.
type PreparedInvitation interface {
	// Row answers the row to store, the caller's own copy.
	Row() *InvitationRow
	// Send mails the invitation's link to its address. Call it once the row is
	// committed. A message that could not be sent ends the invitation, not mailed, in
	// the Auth's own storage, so no link stands that nobody holds, and is the error;
	// the caller reports that the mail did not go, and the invitation is sent again
	// by inviting again.
	Send(ctx context.Context) error
}

// preparedInvitation is the Auth's PreparedInvitation.
type preparedInvitation struct {
	row  InvitationRow
	send func(ctx context.Context) error
}

// Row implements PreparedInvitation.
func (p *preparedInvitation) Row() *InvitationRow {
	row := p.row
	row.TokenHash = slices.Clone(p.row.TokenHash)
	row.RoleNames = slices.Clone(p.row.RoleNames)
	row.NewTenant = cloneNewTenant(p.row.NewTenant)

	return &row
}

// Send implements PreparedInvitation.
func (p *preparedInvitation) Send(ctx context.Context) error {
	return p.send(ctx)
}

// PrepareInvitation checks and makes an invitation to req.Email without storing or
// mailing it: the checks Invite makes (the address is one, and a sign-in method the
// login page offers could admit it), the token and its hash, and
// the time the link stands for (MailSettings.InvitationTimeout). The caller stores the
// row (PreparedInvitation.Row) in a transaction of its own and, once that has
// committed, mails the link (PreparedInvitation.Send). Invite is the two in the Auth's
// own storage.
func (p *AuthAPI[S, U, Tx]) PrepareInvitation(ctx context.Context, req *InvitationRequest) (PreparedInvitation, error) {
	_, span := tracer.Start(ctx)
	defer span.End()

	a := p.auth
	m, email, err := a.invitee(req.Email)
	if err != nil {
		return nil, err
	}
	name, err := invitedName(req.Name)
	if err != nil {
		return nil, err
	}
	if err := req.onePlace(); err != nil {
		return nil, err
	}
	newTenant, err := req.newTenant()
	if err != nil {
		return nil, err
	}
	token, hash, err := linkToken()
	if err != nil {
		return nil, err
	}
	id, err := impulse.NewUUID()
	if err != nil {
		return nil, errors.Wrap(err, "impulse.NewUUID()")
	}
	lifetime := m.settings.InvitationTimeout

	return &preparedInvitation{
		row: InvitationRow{
			ID: id, TokenHash: hash, Email: email, Name: name, TenantID: req.TenantID, EveryTenant: req.EveryTenant, RoleNames: slices.Clone(req.RoleNames),
			InvitedByUserID: req.InvitedByUserID, NewTenant: newTenant, ExpiresAt: time.Now().Add(lifetime),
		},
		send: func(ctx context.Context) error {
			return a.sendInvitation(ctx, m, id, email, token, lifetime)
		},
	}, nil
}

// Invitations lists the invitations that stand: sent, not accepted, not revoked and
// not expired, oldest first.
func (p *AuthAPI[S, U, Tx]) Invitations(ctx context.Context) ([]*sessioninfo.Invitation, error) {
	links, err := p.auth.storage.LiveMailLinks(ctx, sessionstorage.MailLinkInvitation)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.LiveMailLinks()")
	}
	invitations := make([]*sessioninfo.Invitation, len(links))
	for i, link := range links {
		invitations[i] = invitationOf(link)
	}

	return invitations, nil
}

// RevokeInvitation withdraws an invitation that stands; its link is then dead. One
// that no longer stands is NotFound.
func (p *AuthAPI[S, U, Tx]) RevokeInvitation(ctx context.Context, id impulse.UUID) error {
	if err := p.auth.storage.RevokeMailLink(ctx, id); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.RevokeMailLink()")
	}

	return nil
}
