package session

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/impulseframework/impulse/httpio"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/tracer"
)

// MethodList is what an Auth tells its login page (Methods): the sign-in methods the
// page offers, each with the path the application mounted it at, and the pending
// sign-in routes. The application writes it where it wires the routes, so the paths
// the page is told are the paths the router serves.
type MethodList struct {
	// Methods are the methods the page offers, in the order it renders them. Each
	// names a registration on the Auth; a method that is registered but not listed
	// here is not offered.
	Methods []AdvertisedMethod
	// Pending are the paths of the pending sign-in handlers (Pending().Status(),
	// ConfirmWithPassword() and Cancel()), all three or none. The page needs them once a
	// sign-in waits for a confirmation or the application's MFA step.
	Pending PendingRoutes
	// ChangePassword is the path of the password method's ChangeUserPassword handler,
	// which the page needs when a sign-in finds the user must change their password.
	// It is required when the password method is listed, since a user under the change
	// mark changes the password there, and refused when it is not.
	ChangePassword string
	// RemovePassword is the path of the password method's RemoveUserPassword handler,
	// where a signed-in person's account page removes their password once they sign in
	// through a directory, or none. It is refused when the password method is not
	// listed.
	RemovePassword string
	// FirstPassword are the paths of the first password handlers
	// (FirstPassword().SendCode() and Set()), where the account page of a signed-in
	// person who holds no password mails them a code and takes the password they
	// choose with it; both or none. They are refused when the password method is not
	// listed.
	FirstPassword FirstPasswordRoutes
	// ForgetBrowser is the path of the Auth's ForgetBrowser handler, where a signed-in
	// person's account page forgets the browser the second factor remembered for them,
	// or none. The page shows the action only where the session says the browser is
	// remembered for the person.
	ForgetBrowser string
	// ChangeName is the path of the Auth's ChangeName handler, where a signed-in
	// person's account page writes their own name, or none.
	ChangeName string
	// SignInMeans is the path of the Auth's SignInMeans handler, where a signed-in
	// person's account page reads what they sign in with (whether they hold a password,
	// and the directory sign-ins that can sign them in), or none. The read answers in
	// this list's labels. Without it the page offers the change of a password to
	// everyone the password method serves and the removal to nobody, since it cannot
	// say what a removal would leave the person.
	SignInMeans string
	// ChangeLook is the path of the Auth's ChangeLook handler, where a signed-in
	// person's pages keep the look the person chose, or none.
	ChangeLook string
	// Invitation are the paths of the invitation handlers (Invitation().Open(),
	// Accept() and SignUp()), or none. Open and Accept go together; SignUp is named
	// when the password method is listed and refused when it is not.
	Invitation InvitationRoutes
	// Email are the paths of the email address handlers (Email().Add() and Verify()),
	// both or none.
	Email EmailRoutes
	// SignUp are the paths of the sign-up handlers (SignUp().Start(), Open() and
	// Complete()), all three or none; they need the password method listed. The page
	// is told them only while an administrator has the password method open to
	// sign-up, which the answer reads as each request arrives.
	SignUp SignUpRoutes
	// PasswordReset are the paths of the password reset handlers
	// (PasswordReset().Request(), Open() and Complete()), all three or none; they need
	// the password method listed. The page is always told Open and Complete, where a
	// link an administrator sent is followed, and is told Request, behind its "Forgot
	// your password?", only while an administrator has self-service reset turned on,
	// which the answer reads as each request arrives.
	PasswordReset PasswordResetRoutes
}

// PasswordResetRoutes are the paths of the password reset handlers, as a MethodList
// names them.
type PasswordResetRoutes struct {
	Request  string
	Open     string
	Complete string
}

// InvitationRoutes are the paths of the invitation handlers, as a MethodList names
// them.
type InvitationRoutes struct {
	Open   string
	Accept string
	SignUp string
}

// EmailRoutes are the paths of the email address handlers, as a MethodList names them:
// Add and Verify, a signed-in person's own change, both or none, and Confirm, where the
// reader of an address an administrator asked to put on an account confirms it, which
// goes with them or is left out.
type EmailRoutes struct {
	Add     string
	Verify  string
	Confirm string
}

// FirstPasswordRoutes are the paths of the first password handlers, as a MethodList
// names them.
type FirstPasswordRoutes struct {
	SendCode string
	Set      string
}

// SignUpRoutes are the paths of the sign-up handlers, as a MethodList names them.
type SignUpRoutes struct {
	Start    string
	Open     string
	Complete string
}

// AdvertisedMethod is one method of a MethodList.
type AdvertisedMethod struct {
	// Name is the name the method was registered under on the Auth ("password", or
	// the application's name for an external registration).
	Name string
	// Label is what the page shows for the method.
	Label string
	// Path is the path the method's Login handler is mounted at, a path in the
	// application in canonical form (see SanitizeReturnURL): /api/user/google/login.
	Path string
}

// PendingRoutes are the paths of the pending sign-in handlers, as a MethodList names
// them: Status, Confirm and Cancel together, and the email code's SendCode, VerifyCode
// and EnrollEmail together or not at all, which the page needs when a sign-in waits
// for the library's second factor (see "The second factor" in the README).
type PendingRoutes struct {
	Status      string
	Confirm     string
	Cancel      string
	SendCode    string
	VerifyCode  string
	EnrollEmail string
}

// methodsAnswer is what Methods writes.
type methodsAnswer struct {
	Methods        []signin.Advertisement `json:"methods"`
	Pending        *pendingAnswer         `json:"pending,omitempty"`
	ChangePassword string                 `json:"changePassword,omitempty"`
	RemovePassword string                 `json:"removePassword,omitempty"`
	FirstPassword  *firstPasswordAnswer   `json:"firstPassword,omitempty"`
	ForgetBrowser  string                 `json:"forgetBrowser,omitempty"`
	ChangeName     string                 `json:"changeName,omitempty"`
	SignInMeans    string                 `json:"signInMeans,omitempty"`
	ChangeLook     string                 `json:"changeLook,omitempty"`
	// UsernameIsEmail says the password method's username is the email address, so
	// the page asks for the address.
	UsernameIsEmail bool              `json:"usernameIsEmail,omitempty"`
	Invitation      *invitationAnswer `json:"invitation,omitempty"`
	Email           *emailAnswer      `json:"email,omitempty"`
	SignUp          *signUpAnswer     `json:"signUp,omitempty"`
	// PasswordReset is the password reset block: where a reset link is followed, and,
	// while self-service reset is on, where a person asks for one.
	PasswordReset *passwordResetRoutesAnswer `json:"passwordReset,omitempty"`
}

// passwordResetRoutesAnswer is the password reset block of a methods answer. Request is
// absent while an administrator has not turned self-service reset on, and the page then
// offers no "Forgot your password?".
type passwordResetRoutesAnswer struct {
	Request  string `json:"request,omitempty"`
	Open     string `json:"open"`
	Complete string `json:"complete"`
}

// invitationAnswer is the invitation block of a methods answer.
type invitationAnswer struct {
	Open   string `json:"open"`
	Accept string `json:"accept"`
	SignUp string `json:"signUp,omitempty"`
	// Standing is the live invitation the browser carries, absent when it carries
	// none or its link is dead.
	Standing *standingInvitationAnswer `json:"standing,omitempty"`
}

// standingInvitationAnswer is the invitation the browser carries, as the invitation
// block of a methods answer reports it: what Invitation().Open() answered when the
// page opened it, less what the block already says.
type standingInvitationAnswer struct {
	Email string `json:"email"`
	// TenantID and TenantName are the tenant the invitation seats the person in and
	// what people call it, as Invitation().Open() answers them.
	TenantID   string `json:"tenantId,omitempty"`
	TenantName string `json:"tenantName,omitempty"`
	// NewTenant says the invitation makes a tenant, whose name TenantName then is, and
	// DirectoryOnly that it is accepted through an organization's directory alone, so
	// the page offers no account with a password for it.
	NewTenant       bool      `json:"newTenant"`
	DirectoryOnly   bool      `json:"directoryOnly"`
	ExpiresAt       time.Time `json:"expiresAt"`
	UsernameIsEmail bool      `json:"usernameIsEmail"`
	// Methods are the names of the listed external registrations through which the
	// invitation can be accepted for its address (invitedAccount), empty when there is
	// none.
	Methods []string `json:"methods"`
	// AccountExists says an account already holds the invited address, and HasPassword
	// that it signs in with a password: the page offers that account's password form,
	// and offers to create an account only where none holds the address. Whoever
	// holds the link is told this much, so the page can offer the right form: the
	// link proves nothing of who holds it, and nothing is accepted by it.
	AccountExists bool `json:"accountExists"`
	HasPassword   bool `json:"hasPassword"`
}

// firstPasswordAnswer is the first password block of a methods answer.
type firstPasswordAnswer struct {
	SendCode string `json:"sendCode"`
	Set      string `json:"set"`
}

// emailAnswer is the email block of a methods answer.
type emailAnswer struct {
	Add     string `json:"add"`
	Verify  string `json:"verify"`
	Confirm string `json:"confirm,omitempty"`
}

// signUpAnswer is the sign-up block of a methods answer.
type signUpAnswer struct {
	Start    string `json:"start"`
	Open     string `json:"open"`
	Complete string `json:"complete"`
}

// pendingAnswer is the pending block of a methods answer.
type pendingAnswer struct {
	Status      string `json:"status"`
	Confirm     string `json:"confirm"`
	Cancel      string `json:"cancel"`
	SendCode    string `json:"sendCode,omitempty"`
	VerifyCode  string `json:"verifyCode,omitempty"`
	EnrollEmail string `json:"enrollEmail,omitempty"`
}

// Methods answers the login page with the sign-in methods it offers: for each listed
// method its name, the method behind it, how it starts (a form for the password
// method, a redirect for an external one), its label and its path, in the list's
// order, the pending sign-in routes when the list names them, and the password
// change route when the password method is listed, with the route that removes the
// password when the list names it. When the list names the invitation
// routes and the browser carries a live invitation (the cookie Invitation().Open()
// set), the invitation block also reports it under "standing": the address it was
// sent to, its tenant (for an invitation that makes a tenant, "newTenant" with the
// tenant's name, and "directoryOnly" where it is accepted through an organization's
// directory alone), when it stops being accepted, whether an account created from
// it takes the address as its username, and the names of the listed external
// registrations that could admit the address (see Invitation().Open()), so a page
// that arrives without the mailed link's token, as it does when an external sign-in
// was refused and sent it back, still shows the invitation and hides the methods that
// would refuse the person. A browser without the cookie, or whose invitation is no
// longer live, gets the block without it. The answer is public: route it beside the
// login routes, behind StartSession and before ValidateSession, and it needs no XSRF
// token. The sign-up block is in the answer only while the password method is open to
// sign-up: an administrator's choice in the running application, which the answer
// reads as each request arrives, so a page loaded after the method is opened offers
// to sign up and one loaded after it is closed does not.
//
//	{"methods": [{"name": "password", "method": "password", "kind": "password", "label": "Password", "path": "/api/user/password/login"},
//	             {"name": "google", "method": "google", "kind": "redirect", "label": "Google", "path": "/api/user/google/login"}],
//	 "pending": {"status": "/api/user/pending", "confirm": "/api/user/pending/confirm", "cancel": "/api/user/pending/cancel"},
//	 "changePassword": "/api/user/password",
//	 "invitation": {"open": "/api/user/invitation", "accept": "/api/user/invitation/accept", "signUp": "/api/user/invitation/sign-up",
//	                "standing": {"email": "pat@lakeside.edu", "tenant": "", "expiresAt": "2026-10-31T20:00:00Z", "usernameIsEmail": false, "methods": ["google"]}}}
//
// Methods is called where the routes are wired, after every method is registered, and
// panics on a wiring mistake, the way a registration does: an empty list, a method
// that is not registered on this Auth, a name listed twice, a method without a label
// or with a path that is not a canonical path in the application, a pending block
// with some of its paths, the password method without the password change route, or
// that route or the remove-password route without the password method.
func (a *Auth[S, U, Tx]) Methods(list *MethodList) http.HandlerFunc {
	answer := a.advertise(list)

	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		if answer.Invitation == nil && answer.SignUp == nil && answer.PasswordReset == nil {
			return httpio.NewEncoder(w).Ok(answer)
		}

		ctx, span := tracer.Start(r.Context())
		defer span.End()

		now := answer
		if answer.SignUp != nil {
			open, err := a.passwordSignUpOpen(ctx)
			if err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
			now = answer.withSignUp(open)
		}
		if answer.PasswordReset != nil {
			open, err := a.selfServiceResetOpen(ctx)
			if err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
			now = now.withOwnReset(open)
		}
		if now.Invitation == nil {
			return httpio.NewEncoder(w).Ok(now)
		}
		standing, err := a.standingInvitation(ctx, r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(now.withStanding(standing))
	}))
}

// withSignUp is the answer as a page reads it now: with its sign-up block while the
// password method is open to sign-up, and without it, as a copy, while it is not. The
// answer built at wiring is shared by every request and carries the block.
func (m *methodsAnswer) withSignUp(open bool) *methodsAnswer {
	if open {
		return m
	}
	answer := *m
	answer.SignUp = nil

	return &answer
}

// withOwnReset is the answer as a page reads it now: with the path a person asks for
// their own reset link at while self-service reset is on, and without it, as a copy,
// while it is off. The paths a link is followed at stay either way.
func (m *methodsAnswer) withOwnReset(open bool) *methodsAnswer {
	if open {
		return m
	}
	answer := *m
	reset := *m.PasswordReset
	reset.Request = ""
	answer.PasswordReset = &reset

	return &answer
}

// standingInvitation is the live invitation the browser carries, for the invitation
// block of the methods answer: the cookie Invitation().Open() set, read as the sign-in
// handlers read it, and the store asked whether its link still stands. It is nil when
// the browser carries no cookie and when the link is dead (expired, accepted or
// revoked). usernameIsEmail and methods are what Open() answers for the same
// invitation.
func (a *Auth[S, U, Tx]) standingInvitation(ctx context.Context, r *http.Request) (*standingInvitationAnswer, error) {
	if _, ok := invitationTokenHash(ctx); !ok {
		// StartSession reads the cookie onto the context; a listing routed without it
		// reads the cookie here.
		ctx = a.withInvitationCookie(ctx, r)
	}
	inv, err := a.invitationOf(ctx)
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, nil
	}

	invited, err := a.invitedAccount(ctx, inv.Email)
	if err != nil {
		return nil, err
	}

	makes, directoryOnly := madeTenant(inv.NewTenant)

	return &standingInvitationAnswer{
		Email: inv.Email, TenantID: inv.TenantID, TenantName: a.invitedTenantName(inv.TenantID, inv.NewTenant), NewTenant: makes, DirectoryOnly: directoryOnly,
		ExpiresAt: inv.ExpiresAt, UsernameIsEmail: a.registered().usernameIsEmail(), Methods: invited.methods,
		AccountExists: invited.exists, HasPassword: invited.hasPassword,
	}, nil
}

// withStanding is the answer with standing reported in its invitation block: the
// answer itself when there is nothing to report, otherwise a copy, since the answer
// built at wiring is shared by every request.
func (m *methodsAnswer) withStanding(standing *standingInvitationAnswer) *methodsAnswer {
	if standing == nil {
		return m
	}
	answer := *m
	invitation := *m.Invitation
	invitation.Standing = standing
	answer.Invitation = &invitation

	return &answer
}

// advertise builds the methods answer from list, checking each entry against the
// registry under the methods lock without closing registration: the first request
// closes it, as for every handler. The names listed are remembered as the methods the
// login page offers (signInMethods.listed), and the list itself with its labels and
// its sign-in means route (signInMethods.lists), so a read served beside it answers in
// its words.
func (a *Auth[S, U, Tx]) advertise(list *MethodList) *methodsAnswer {
	if list == nil || len(list.Methods) == 0 {
		panic("session: Methods is given no sign-in method: list every method the login page offers, each with its label and the path its Login handler is mounted at")
	}

	a.methods.mu.Lock()
	defer a.methods.mu.Unlock()

	answer := &methodsAnswer{Methods: make([]signin.Advertisement, 0, len(list.Methods))}
	seen := make(map[string]bool, len(list.Methods))
	for _, m := range list.Methods {
		switch {
		case m.Name == "":
			panic("session: Methods lists a sign-in method without a name: name the registration the page offers")
		case !a.methods.registered[m.Name]:
			panic(fmt.Sprintf("session: Methods lists the sign-in method %q, which is not registered on this Auth: register it before the routes are wired, or drop it from the list", m.Name))
		case seen[m.Name]:
			panic(fmt.Sprintf("session: Methods lists the sign-in method %q twice: list each method once", m.Name))
		case m.Label == "":
			panic(fmt.Sprintf("session: Methods lists the sign-in method %q without a label: give it the words the login page shows", m.Name))
		case !advertisedPath(m.Path):
			panic(fmt.Sprintf("session: Methods lists the sign-in method %q at %q, which is not a canonical path in the application: "+
				"give it the path its Login handler is mounted at, such as /api/user/%s/login", m.Name, m.Path, m.Name))
		}
		seen[m.Name] = true

		ad := signin.Advertisement{Name: m.Name, Method: sessioninfo.MethodPassword, Kind: signin.KindPassword, Label: m.Label, Path: m.Path}
		if external, ok := a.methods.external[m.Name]; ok {
			ad.Method, ad.Kind = external.Name, signin.KindRedirect
		}
		answer.Methods = append(answer.Methods, ad)
	}
	listing := methodsListing{labels: make(map[string]string, len(list.Methods)), signInMeans: list.SignInMeans}
	for _, m := range list.Methods {
		if !slices.Contains(a.methods.listed, m.Name) {
			a.methods.listed = append(a.methods.listed, m.Name)
		}
		if a.methods.labels == nil {
			a.methods.labels = make(map[string]string, len(list.Methods))
		}
		if _, labeled := a.methods.labels[m.Name]; !labeled {
			a.methods.labels[m.Name] = m.Label
		}
		listing.labels[m.Name] = m.Label
	}
	a.methods.lists = append(a.methods.lists, listing)
	passwordListed := seen[string(sessioninfo.MethodPassword)]
	answer.Pending = pendingAnswerOf(&list.Pending)
	answer.ChangePassword = changePasswordPathOf(list, passwordListed)
	answer.RemovePassword = removePasswordPathOf(list, passwordListed)
	answer.FirstPassword = firstPasswordAnswerOf(list.FirstPassword, passwordListed)
	answer.ForgetBrowser = forgetBrowserPathOf(list)
	answer.ChangeName = changeNamePathOf(list)
	answer.SignInMeans = signInMeansPathOf(list)
	answer.ChangeLook = changeLookPathOf(list)
	answer.UsernameIsEmail = passwordListed && a.methods.usernameIsEmail()
	answer.Invitation = invitationAnswerOf(list.Invitation, passwordListed)
	answer.Email = emailAnswerOf(list.Email)
	answer.SignUp = signUpAnswerOf(list.SignUp, passwordListed)
	answer.PasswordReset = passwordResetAnswerOf(list.PasswordReset, passwordListed)

	return answer
}

// passwordResetAnswerOf validates the password reset routes: none, or all three as
// canonical paths with the password method listed.
func passwordResetAnswerOf(p PasswordResetRoutes, passwordListed bool) *passwordResetRoutesAnswer {
	if p == (PasswordResetRoutes{}) {
		return nil
	}
	if !advertisedPath(p.Request) || !advertisedPath(p.Open) || !advertisedPath(p.Complete) {
		panic("session: Methods names some of the password reset routes: name all three (PasswordReset().Request(), Open() and Complete()), each a canonical path in the application, or none")
	}
	if !passwordListed {
		panic("session: Methods names the password reset routes without the password method: a reset chooses a new password, so list the password method or drop the routes")
	}

	return &passwordResetRoutesAnswer{Request: p.Request, Open: p.Open, Complete: p.Complete}
}

// invitationAnswerOf validates the invitation routes: none, or open and accept as
// canonical paths, with sign-up exactly when the password method is listed.
func invitationAnswerOf(p InvitationRoutes, passwordListed bool) *invitationAnswer {
	if p == (InvitationRoutes{}) {
		return nil
	}
	if !advertisedPath(p.Open) || !advertisedPath(p.Accept) {
		panic(fmt.Sprintf("session: Methods is given the invitation routes %+v: name the open and accept paths together, each a canonical path in the application", p))
	}
	switch {
	case passwordListed && !advertisedPath(p.SignUp):
		panic(fmt.Sprintf("session: Methods lists the password method with the invitation sign-up route %q: give it the path Invitation().SignUp() is mounted at, "+
			"which the login page needs for an invited person who creates an account with a password", p.SignUp))
	case !passwordListed && p.SignUp != "":
		panic(fmt.Sprintf("session: Methods is given the invitation sign-up route %q but lists no password method: drop it, or list the method", p.SignUp))
	}

	return &invitationAnswer{Open: p.Open, Accept: p.Accept, SignUp: p.SignUp}
}

// emailAnswerOf validates the email routes: none, or both as canonical paths.
func emailAnswerOf(p EmailRoutes) *emailAnswer {
	if p == (EmailRoutes{}) {
		return nil
	}
	if !advertisedPath(p.Add) || !advertisedPath(p.Verify) || (p.Confirm != "" && !advertisedPath(p.Confirm)) {
		panic(fmt.Sprintf("session: Methods is given the email routes %+v: name the add and verify paths together, and the confirm path with them or not at all, "+
			"each a canonical path in the application", p))
	}

	return &emailAnswer{Add: p.Add, Verify: p.Verify, Confirm: p.Confirm}
}

// signUpAnswerOf validates the sign-up routes: none, or all three as canonical paths,
// beside a listed password method.
func signUpAnswerOf(p SignUpRoutes, passwordListed bool) *signUpAnswer {
	if p == (SignUpRoutes{}) {
		return nil
	}
	if !passwordListed {
		panic(fmt.Sprintf("session: Methods is given the sign-up routes %+v but lists no password method: a person signs up with a password, so list the method, or drop the routes", p))
	}
	if !advertisedPath(p.Start) || !advertisedPath(p.Open) || !advertisedPath(p.Complete) {
		panic(fmt.Sprintf("session: Methods is given the sign-up routes %+v: name the start, open and complete paths together, each a canonical path in the application", p))
	}

	return &signUpAnswer{Start: p.Start, Open: p.Open, Complete: p.Complete}
}

// changePasswordPathOf validates the password change route: present and canonical with
// the password method, absent without it.
func changePasswordPathOf(list *MethodList, passwordListed bool) string {
	switch {
	case passwordListed && !advertisedPath(list.ChangePassword):
		panic(fmt.Sprintf("session: Methods lists the password method with the password change route %q: "+
			"give it the path its ChangeUserPassword handler is mounted at, such as /api/user/password, "+
			"which the login page needs when a sign-in finds the user must change their password", list.ChangePassword))
	case !passwordListed && list.ChangePassword != "":
		panic(fmt.Sprintf("session: Methods is given the password change route %q but lists no password method: drop it, or list the method", list.ChangePassword))
	}

	return list.ChangePassword
}

// removePasswordPathOf validates the remove-your-password route: none, or a canonical
// path beside a listed password method.
func removePasswordPathOf(list *MethodList, passwordListed bool) string {
	switch {
	case list.RemovePassword == "":
		return ""
	case !passwordListed:
		panic(fmt.Sprintf("session: Methods is given the remove-your-password route %q but lists no password method: drop it, or list the method", list.RemovePassword))
	case !advertisedPath(list.RemovePassword):
		panic(fmt.Sprintf("session: Methods is given the remove-your-password route %q, which is not a canonical path in the application: "+
			"give it the path the password method's RemoveUserPassword handler is mounted at, such as /api/user/password/remove", list.RemovePassword))
	}

	return list.RemovePassword
}

// firstPasswordAnswerOf validates the first password routes: none, or both as canonical
// paths beside a listed password method.
func firstPasswordAnswerOf(p FirstPasswordRoutes, passwordListed bool) *firstPasswordAnswer {
	if p == (FirstPasswordRoutes{}) {
		return nil
	}
	if !passwordListed {
		panic(fmt.Sprintf("session: Methods is given the first password routes %+v but lists no password method: "+
			"a person chooses a first password where people sign in with one, so list the method, or drop the routes", p))
	}
	if !advertisedPath(p.SendCode) || !advertisedPath(p.Set) {
		panic(fmt.Sprintf("session: Methods is given the first password routes %+v: name the send-code and set paths together, each a canonical path in the application, "+
			"such as /api/user/password/first/code and /api/user/password/first", p))
	}

	return &firstPasswordAnswer{SendCode: p.SendCode, Set: p.Set}
}

// changeLookPathOf validates the keep-your-look route: none, or a canonical path.
func changeLookPathOf(list *MethodList) string {
	if list.ChangeLook != "" && !advertisedPath(list.ChangeLook) {
		panic(fmt.Sprintf("session: Methods is given the keep-your-look route %q, which is not a canonical path in the application: "+
			"give it the path its ChangeLook handler is mounted at, such as /api/user/look", list.ChangeLook))
	}

	return list.ChangeLook
}

// changeNamePathOf validates the change-your-name route: none, or a canonical path.
func changeNamePathOf(list *MethodList) string {
	if list.ChangeName != "" && !advertisedPath(list.ChangeName) {
		panic(fmt.Sprintf("session: Methods is given the change-your-name route %q, which is not a canonical path in the application: "+
			"give it the path its ChangeName handler is mounted at, such as /api/user/name", list.ChangeName))
	}

	return list.ChangeName
}

// signInMeansPathOf validates the route a person's sign-in means are read from: none, or
// a canonical path.
func signInMeansPathOf(list *MethodList) string {
	if list.SignInMeans != "" && !advertisedPath(list.SignInMeans) {
		panic(fmt.Sprintf("session: Methods is given the sign-in means route %q, which is not a canonical path in the application: "+
			"give it the path its SignInMeans handler is mounted at, such as /api/user/sign-in-means", list.SignInMeans))
	}

	return list.SignInMeans
}

// forgetBrowserPathOf validates the forget-this-browser route: none, or a canonical path.
func forgetBrowserPathOf(list *MethodList) string {
	if list.ForgetBrowser != "" && !advertisedPath(list.ForgetBrowser) {
		panic(fmt.Sprintf("session: Methods is given the forget-this-browser route %q, which is not a canonical path in the application: "+
			"give it the path its ForgetBrowser handler is mounted at, such as /api/user/browser/forget", list.ForgetBrowser))
	}

	return list.ForgetBrowser
}

// pendingAnswerOf validates the pending routes: none, or all three as canonical paths.
func pendingAnswerOf(p *PendingRoutes) *pendingAnswer {
	if *p == (PendingRoutes{}) {
		return nil
	}
	for _, path := range []string{p.Status, p.Confirm, p.Cancel} {
		if !advertisedPath(path) {
			panic(fmt.Sprintf("session: Methods is given the pending routes %+v: name the status, confirm and cancel paths together, each a canonical path in the application, or none", p))
		}
	}
	if p.SendCode != "" || p.VerifyCode != "" || p.EnrollEmail != "" {
		for _, path := range []string{p.SendCode, p.VerifyCode, p.EnrollEmail} {
			if !advertisedPath(path) {
				panic(fmt.Sprintf("session: Methods is given the pending routes %+v: name the send-code, verify-code and enroll-email paths together, "+
					"each a canonical path in the application, or none of the three", *p))
			}
		}
	}

	return &pendingAnswer{Status: p.Status, Confirm: p.Confirm, Cancel: p.Cancel, SendCode: p.SendCode, VerifyCode: p.VerifyCode, EnrollEmail: p.EnrollEmail}
}

// advertisedPath reports whether p is a path the page can be sent to: a canonical path
// in the application other than the root.
func advertisedPath(p string) bool {
	return p != "/" && internalcookie.SanitizeReturnURL(p) == p
}
