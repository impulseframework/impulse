//go:build !skipAuth

// The invitation that makes a tenant through the real handlers on both backends: what
// the invitation page is told, the acceptance hook handed the tenant's details and how
// the person signed in, and a tenant entered through its directory only accepted by
// nothing but a sign-in through an organization's directory.

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/google"
	"github.com/impulseframework/impulse/session/internal/oidctest"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

// organizationAccountSentence is what a person is told when an invitation that is
// accepted through an organization's directory alone is accepted any other way.
const organizationAccountSentence = "This invitation is accepted with your organization's account, not with a password or a personal account. Sign in with your organization's account (Google Workspace or Microsoft Entra)."

// takenDirectory is the name of the tenant the application's hook refuses to make in
// these scenarios, as it refuses one whose directory another tenant has.
const takenDirectory = "Riverside High"

// tenantInvitationApp is an application whose staff bring an organization in by
// inviting one person: the password method beside a Google registration that admits
// the Lakeside Workspace and, as nobody's organization, any other Google account. Its
// acceptance hook keeps what it was handed.
type tenantInvitationApp[Tx any] struct {
	*authApp[Tx]
	google *oidctest.FakeIDP
	// store is the Auth's store, where a scenario reads what the library counted.
	store *sessionstorage.Auth[Tx]

	mu sync.Mutex
	// made are the invitations the hook was handed, as it was handed them.
	made []sessioninfo.Invitation
}

func newTenantInvitationApp[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) *tenantInvitationApp[Tx] {
	t.Helper()

	h := &hooks[Tx]{rows: be.rows}
	db, store := be.newStore(ctx, t)
	googleIDP := oidctest.NewFakeIDP(t, "google-client")
	recorder := &mail.Recorder{}
	auth, err := session.NewAuth[appSessionData, appUserData](withCustomData(t, store, h), cookieKey, session.NoSuperAdministrators(),
		session.MailSettings{Sender: recorder, Application: "Lakeside", LoginURL: "https://app.example/login"})
	if err != nil {
		t.Fatalf("session.NewAuth() error = %v", err)
	}
	a := &tenantInvitationApp[Tx]{google: googleIDP, store: store}
	auth.SetSignInPolicy(h.signInPolicy)
	auth.SetInvitationAccepted(a.accepted)
	password := session.PasswordSignIn(auth)
	// No rule of the application's own: the standard account rule decides.
	var rule google.AccountRule[Tx]
	googleIssuerMu.Lock()
	issuer := google.IssuerURL
	google.IssuerURL = googleIDP.Server.URL
	workspace := google.SignIn(auth, "google", rule, &google.Config{
		RoleSync: google.DisableRoleSync(), Workspaces: google.Workspaces(google.Workspace("lakeside", hostedDomain), google.AnyGoogleAccount()),
		ClientID: "google-client", ClientSecret: "secret", RedirectURL: "https://app.example/google/callback",
	})
	google.IssuerURL = issuer
	googleIssuerMu.Unlock()

	r := chi.NewRouter()
	mountAuth(r, auth, func(r chi.Router) {
		r.With(auth.ValidateXSRFToken).Post("/login", password.Login())
		r.With(auth.ValidateXSRFToken).Post("/invitation/sign-up", auth.Invitation().SignUp())
		r.Get("/google/login", workspace.Login())
		r.Get("/google/callback", workspace.Callback())
	})
	server := httptest.NewTLSServer(r)
	t.Cleanup(server.Close)
	a.authApp = &authApp[Tx]{db: db, rows: be.rows, server: server, api: auth.API(), hooks: h, mail: recorder, verified: store.VerifyUserEmail}

	return a
}

// accepted is the application's acceptance hook: it keeps what it was handed, and
// refuses to make the tenant whose directory another tenant has, as an application's
// own hook does, in the words the person is shown.
func (a *tenantInvitationApp[Tx]) accepted(_ context.Context, _ Tx, _ impulse.UUID, _ string, inv *sessioninfo.Invitation) (func(context.Context), error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if inv.NewTenant != nil && inv.NewTenant.Name == takenDirectory {
		return nil, sessioninfo.NewLoginRefusal(sessioninfo.RefusedOrganizationHasTenant, httpio.NewConflictMessage("Your organization already has a tenant here. Ask its administrator to add you."))
	}
	handed := *inv
	if inv.NewTenant != nil {
		tenant := *inv.NewTenant
		handed.NewTenant = &tenant
	}
	a.made = append(a.made, handed)

	return nil, nil
}

// handed are the invitations the hook was handed so far.
func (a *tenantInvitationApp[Tx]) handed() []sessioninfo.Invitation {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([]sessioninfo.Invitation(nil), a.made...)
}

// inviteTenant sends email an invitation that makes the tenant name, entered through
// its directory only when directoryOnly says so, and returns the invitation.
func (a *tenantInvitationApp[Tx]) inviteTenant(ctx context.Context, t *testing.T, email, name string, directoryOnly bool) *sessioninfo.Invitation {
	t.Helper()

	inv, err := a.api.Invite(ctx, &session.InvitationRequest{Email: email, NewTenant: &sessioninfo.NewTenant{Name: name, DirectoryOnly: directoryOnly, Details: json.RawMessage(`{"Phone":"555-0100"}`)}})
	if err != nil {
		t.Fatalf("Invite(%s) error = %v", email, err)
	}

	return inv
}

// openInvitation opens the invitation mailed last in b and returns the answer.
func (a *tenantInvitationApp[Tx]) openInvitation(ctx context.Context, t *testing.T, b *browser) map[string]any {
	t.Helper()

	var opened map[string]any
	if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": lastInvitationToken(t, a.mail)}, b.xsrfToken()), &opened); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	return opened
}

// googleSignIn runs a Google round trip with claims in b and returns where the
// callback sent the browser.
func (a *tenantInvitationApp[Tx]) googleSignIn(ctx context.Context, b *browser, claims map[string]any) *url.URL {
	b.t.Helper()

	return b.oidcSignIn(ctx, a.google, "/google", claims)
}

// personalGoogleClaims are the ID token claims of a verified Google account that is in
// no Workspace: no hd claim.
func personalGoogleClaims(sub, email string) map[string]any {
	return map[string]any{"sub": sub, "email": email, "email_verified": true}
}

// assertStanding fails unless exactly the invitations want stand.
func (a *tenantInvitationApp[Tx]) assertStanding(ctx context.Context, t *testing.T, when string, want ...*sessioninfo.Invitation) {
	t.Helper()

	standing := a.standingInvitations(ctx, t)
	got := make(map[impulse.UUID]bool, len(standing))
	for _, inv := range standing {
		got[inv.ID] = true
	}
	if len(standing) != len(want) {
		t.Errorf("standing invitations %s = %d, want %d", when, len(standing), len(want))
	}
	for _, inv := range want {
		if !got[inv.ID] {
			t.Errorf("the invitation to %s does not stand %s, want it standing", inv.Email, when)
		}
	}
}

// TestAuthSeams_TenantInvitation runs the tenant invitation scenarios on each backend.
func TestAuthSeams_TenantInvitation(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		runTenantInvitationSeams(t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		runTenantInvitationSeams(t, spannerBackend)
	})
}

func runTenantInvitationSeams[Tx any](t *testing.T, be backend[Tx]) {
	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, be backend[Tx])
	}{
		{
			name: "a tenant entered through its directory only: the page is told so and offered no account with a password, a sign-up with a password and a personal Google account are refused with organization_account_required and nothing written, and the Workspace account accepts it, the hook handed the tenant's details and the Workspace",
			run:  seamTenantInvitationDirectoryOnly[Tx],
		},
		{
			name: "a tenant anyone may enter is accepted by an account made with a password once the code mailed to the invited address is entered, the hook handed the password as how the person signed in",
			run:  seamTenantInvitationByPassword[Tx],
		},
		{
			name: "a signed-in person accepts by hand as their session was established: a password session is refused a directory-only tenant in words, the invitation standing, and a Workspace session accepts it",
			run:  seamTenantInvitationByHand[Tx],
		},
		{
			name: "the hook refusing to make the tenant ends the sign-in on its code, with nothing of the sign-in written and the invitation standing",
			run:  seamTenantInvitationHookRefuses[Tx],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.run(t.Context(), t, be)
		})
	}
}

// lakesideTenant is the tenant the scenarios' invitations make, as the hook is handed
// it.
func lakesideTenant(directoryOnly bool) *sessioninfo.NewTenant {
	return &sessioninfo.NewTenant{Name: "Lakeside High", DirectoryOnly: directoryOnly, Details: json.RawMessage(`{"Phone":"555-0100"}`)}
}

// seamTenantInvitationDirectoryOnly is the scenario "a tenant entered through its
// directory only".
func seamTenantInvitationDirectoryOnly[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newTenantInvitationApp(ctx, t, be)
	inv := a.inviteTenant(ctx, t, "pat@lakeside.edu", "Lakeside High", true)

	b := a.browser(t)
	b.prime(ctx)
	opened := a.openInvitation(ctx, t, b)
	if opened["newTenant"] != true || opened["tenantName"] != "Lakeside High" || opened["tenantId"] != nil || opened["directoryOnly"] != true || opened["signUp"] != false {
		t.Errorf("the opened invitation = %v, want a new tenant named Lakeside High with no id, directory only, and no account with a password offered", opened)
	}

	// An account with a password: refused before the account is made.
	var refused struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(b.expect(ctx, http.StatusUnauthorized, http.MethodPost, "/invitation/sign-up", map[string]string{"username": "patl", "password": seedPassword}, b.xsrfToken()), &refused); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if refused.Code != string(sessioninfo.RefusedOrganizationAccountRequired) || refused.Message != organizationAccountSentence {
		t.Errorf("the refused sign-up = %+v, want organization_account_required saying what to sign in with", refused)
	}
	a.assertNoRow(ctx, t, b, "patl")

	// A Google account of the invited address that is in no Workspace: refused, no
	// account made.
	assertRedirect(t, a.googleSignIn(ctx, b, personalGoogleClaims("sub-personal", "pat@lakeside.edu")), "/login", url.Values{"code": {"organization_account_required"}})
	a.assertNoRow(ctx, t, b, "pat@lakeside.edu")
	a.assertStanding(ctx, t, "after the refusals", inv)
	if handed := a.handed(); len(handed) != 0 {
		t.Errorf("the hook was handed %d invitations by refused acceptances, want none", len(handed))
	}

	// The Workspace account of the invited address: accepted, the hook handed the
	// tenant as staff entered it and the Workspace the person signed in through.
	assertRedirect(t, a.googleSignIn(ctx, b, googleClaims("sub-pat", "pat@lakeside.edu")), "/home", url.Values{})
	v := signedInAs(ctx, t, b, "pat@lakeside.edu")
	a.assertStanding(ctx, t, "after the acceptance")
	handed := a.handed()
	if len(handed) != 1 {
		t.Fatalf("the hook was handed %d invitations, want the one", len(handed))
	}
	if diff := cmp.Diff(lakesideTenant(true), handed[0].NewTenant); diff != "" {
		t.Errorf("the tenant the hook was handed (-want +got):\n%s", diff)
	}
	if handed[0].SignInMethod != sessioninfo.MethodGoogle || handed[0].SignInConnection != "lakeside" {
		t.Errorf("the hook was told the person signed in through %q %q, want google lakeside", handed[0].SignInMethod, handed[0].SignInConnection)
	}
	if !(signin.Directory{Method: handed[0].SignInMethod, Connection: handed[0].SignInConnection}).Organization() {
		t.Error("what the hook was told is no organization's directory, want the Workspace as one")
	}
	assertEventsSeen(t, v, "google:lakeside")
}

// seamTenantInvitationByPassword is the scenario "a tenant anyone may enter is accepted
// by an account made with a password".
func seamTenantInvitationByPassword[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newTenantInvitationApp(ctx, t, be)
	a.inviteTenant(ctx, t, "pat@elsewhere.example", "Lakeside High", false)

	b := a.browser(t)
	b.prime(ctx)
	if opened := a.openInvitation(ctx, t, b); opened["newTenant"] != true || opened["directoryOnly"] != false || opened["signUp"] != true {
		t.Errorf("the opened invitation = %v, want a new tenant anyone may enter, with an account with a password offered", opened)
	}
	a.signUpInvited(ctx, b, "patl", "pat@elsewhere.example")
	signedInAs(ctx, t, b, "patl")
	a.assertStanding(ctx, t, "after the acceptance")

	handed := a.handed()
	if len(handed) != 1 {
		t.Fatalf("the hook was handed %d invitations, want the one", len(handed))
	}
	if diff := cmp.Diff(lakesideTenant(false), handed[0].NewTenant); diff != "" {
		t.Errorf("the tenant the hook was handed (-want +got):\n%s", diff)
	}
	if handed[0].SignInMethod != sessioninfo.MethodPassword || handed[0].SignInConnection != "" {
		t.Errorf("the hook was told the person signed in through %q %q, want a password and no connection", handed[0].SignInMethod, handed[0].SignInConnection)
	}
}

// seamTenantInvitationByHand is the scenario "a signed-in person accepts by hand as
// their session was established".
func seamTenantInvitationByHand[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newTenantInvitationApp(ctx, t, be)

	// Sam signed in with a password: the invitation asks for an organization's account.
	a.seed(ctx, t, "sam")
	sam := a.browser(t)
	sam.prime(ctx)
	if status, _ := sam.loginAs(ctx, "sam"); status != http.StatusOK {
		t.Fatalf("password login = %d, want 200", status)
	}
	forSam := a.inviteTenant(ctx, t, "sam@lakeside.edu", "Lakeside High", true)
	a.openInvitation(ctx, t, sam)
	var refused struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(sam.expect(ctx, http.StatusForbidden, http.MethodPost, "/invitation/accept", nil, sam.xsrfToken()), &refused); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if refused.Message != organizationAccountSentence {
		t.Errorf("the refused acceptance says %q, want what to sign in with", refused.Message)
	}
	a.assertStanding(ctx, t, "after the refused acceptance", forSam)
	if handed := a.handed(); len(handed) != 0 {
		t.Errorf("the hook was handed %d invitations by the refused acceptance, want none", len(handed))
	}

	// Kim has an account and signed in through the Workspace: the same kind of
	// invitation, to Kim's address, is accepted by hand.
	a.createUser(ctx, t, "kim@lakeside.edu")
	kim := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, kim, googleClaims("sub-kim", "kim@lakeside.edu")), "/home", url.Values{})
	signedInAs(ctx, t, kim, "kim@lakeside.edu")
	a.inviteTenant(ctx, t, "kim@lakeside.edu", "Lakeside High", true)
	a.openInvitation(ctx, t, kim)
	kim.expect(ctx, http.StatusOK, http.MethodPost, "/invitation/accept", nil, kim.xsrfToken())
	a.assertStanding(ctx, t, "after the acceptance by hand", forSam)

	handed := a.handed()
	if len(handed) != 1 {
		t.Fatalf("the hook was handed %d invitations, want the one accepted", len(handed))
	}
	if handed[0].Email != "kim@lakeside.edu" || handed[0].SignInMethod != sessioninfo.MethodGoogle || handed[0].SignInConnection != "lakeside" {
		t.Errorf("the hook was handed the invitation to %s, signed in through %q %q; want kim's, through google lakeside", handed[0].Email, handed[0].SignInMethod, handed[0].SignInConnection)
	}
}

// seamTenantInvitationHookRefuses is the scenario "the hook refusing to make the tenant
// ends the sign-in on its code".
func seamTenantInvitationHookRefuses[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newTenantInvitationApp(ctx, t, be)
	inv := a.inviteTenant(ctx, t, "pat@lakeside.edu", takenDirectory, true)

	b := a.browser(t)
	b.prime(ctx)
	a.openInvitation(ctx, t, b)
	assertRedirect(t, a.googleSignIn(ctx, b, googleClaims("sub-pat", "pat@lakeside.edu")), "/login", url.Values{"code": {"organization_has_tenant"}})
	a.assertNoRow(ctx, t, b, "pat@lakeside.edu")
	a.assertStanding(ctx, t, "after the hook refused", inv)

	// An account with a password, where the invitation takes one: the sign-up waits for
	// the code mailed to the invited address with nobody made, and the code makes the
	// account in a step of its own before the sign-in that carries the invitation to
	// the hook. The refusal leaves the account, signed in nowhere, and the invitation
	// stands, as for any invitation whose acceptance fails at a sign-up.
	open := a.inviteTenant(ctx, t, "lee@riverside.edu", takenDirectory, false)
	lee := a.browser(t)
	lee.prime(ctx)
	a.openInvitation(ctx, t, lee)
	code := a.signUpHeld(ctx, lee, "lee", "lee@riverside.edu")
	var refused struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(lee.expect(ctx, http.StatusUnauthorized, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}), &refused); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if refused.Code != string(sessioninfo.RefusedOrganizationHasTenant) {
		t.Errorf("the refused sign-up carries the code %q, want %s", refused.Code, sessioninfo.RefusedOrganizationHasTenant)
	}
	lee.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if !a.db.hasUser(ctx, t, "lee") {
		t.Error("no user exists under lee after the refused sign-up, want the account the code made before the sign-in")
	}
	a.assertStanding(ctx, t, "after the hook refused the sign-up", inv, open)

	// A person signed in already, whose account holds the invited address confirmed,
	// accepts by hand: refused in words that say what to do and nothing of the tenant
	// their organization has, the invitation standing.
	a.confirmedAddress(ctx, t, a.seed(ctx, t, "sam"), "sam@lakeside.edu")
	sam := a.browser(t)
	sam.prime(ctx)
	if status, _ := sam.loginAs(ctx, "sam"); status != http.StatusOK {
		t.Fatalf("password login = %d, want 200", status)
	}
	forSam := a.inviteTenant(ctx, t, "sam@lakeside.edu", takenDirectory, false)
	a.openInvitation(ctx, t, sam)
	var byHand struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(sam.expect(ctx, http.StatusForbidden, http.MethodPost, "/invitation/accept", nil, sam.xsrfToken()), &byHand); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if byHand.Message != "Your organization is already set up in this application. Ask its administrator to invite you." {
		t.Errorf("the refused acceptance by hand says %q, want that the organization is set up already and what to do", byHand.Message)
	}
	a.assertStanding(ctx, t, "after the acceptance by hand was refused", inv, open, forSam)
	if handed := a.handed(); len(handed) != 0 {
		t.Errorf("the hook kept %d invitations it refused, want none", len(handed))
	}
}
