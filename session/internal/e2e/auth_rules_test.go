//go:build !skipAuth

// The sign-in rules through the real sign-in flow on both backends, shape by rule: the
// standard account rule under each choice an administrator makes for a method (open to
// sign-up, or invite only) and a custom rule beside it, the organization slots of both
// methods, the policy at every session start and the provider-initiated logout across
// the tenants of one registration.

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/azure"
	"github.com/impulseframework/impulse/session/google"
	"github.com/impulseframework/impulse/session/internal/oidctest"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
)

// otherTenant is an Entra tenant no registration in these scenarios lists.
const otherTenant = "33333333-3333-3333-3333-333333333333"

// accounts is what an administrator chose for a registration in a scenario: the
// method's row of the sign-in methods table, which the scenario's application writes
// before the first sign-in.
type accounts int

const (
	// inviteOnly is a method with no row: a person with no account needs an invitation.
	inviteOnly accounts = iota
	// openSignUp is a method whose row says it is open to sign-up, with no default
	// role.
	openSignUp
)

// rulesShape is what one scenario registers: the Entra method under tenants with what
// was chosen for it, a custom rule beside it when entraRule is set (the hooks' Azure
// resolver) and role synchronization when entraRoles is set; the Google method under
// workspaces with what was chosen for it and a custom rule when googleRule is set.
// defaultRole is the default role of the open methods' rows, "" for none, which the
// application's sign-up hook writes as the new user's membership row.
type rulesShape struct {
	tenants        azure.TenantSet
	entraAccounts  accounts
	entraRule      bool
	entraRoles     bool
	workspaces     google.WorkspaceSet
	googleAccounts accounts
	googleRule     bool
	defaultRole    string
}

// oneOrganization is the shape of today's staff application: one tenant and one
// Workspace, each under chosen.
func oneOrganization(chosen accounts) *rulesShape {
	return &rulesShape{
		tenants: azure.Tenant(azureTenant), entraAccounts: chosen,
		workspaces: google.Workspace("lakeside", hostedDomain), googleAccounts: chosen,
	}
}

// rulesApp is an application of one shape: its Entra registration at /entra against a
// fake shaped like the Microsoft identity platform, its Google registration at /google
// against a fake Google.
type rulesApp[Tx any] struct {
	*authApp[Tx]
	entra, google *oidctest.FakeIDP
	// store is the Auth's store, where a scenario writes what an administrator chooses
	// for a method while the application runs.
	store *sessionstorage.Auth[Tx]
}

// rolesNeverAsked is the role store of a registration that synchronizes roles in a
// scenario where the sign-in must be refused before any role is read or written.
type rolesNeverAsked struct {
	t *testing.T
}

func (r rolesNeverAsked) UserRoles(context.Context, accesstypes.User, ...accesstypes.PolicyScope) (accesstypes.RoleCollection, error) {
	r.t.Error("the role store was asked for a user's roles, want the sign-in refused first")

	return accesstypes.RoleCollection{}, nil
}

func (r rolesNeverAsked) ResolveRole(context.Context, accesstypes.PolicyScope, string) (accesstypes.Role, bool, error) {
	r.t.Error("the role store was asked to resolve a role, want the sign-in refused first")

	return "", false, nil
}

func (r rolesNeverAsked) AddUserRoles(context.Context, accesstypes.PolicyScope, accesstypes.User, ...accesstypes.Role) error {
	r.t.Error("the role store was asked to add roles, want the sign-in refused first")

	return nil
}

func (r rolesNeverAsked) DeleteUserRoles(context.Context, accesstypes.PolicyScope, accesstypes.User, ...accesstypes.Role) error {
	r.t.Error("the role store was asked to remove roles, want the sign-in refused first")

	return nil
}

func newRulesApp[Tx any](ctx context.Context, t *testing.T, be backend[Tx], shape *rulesShape) *rulesApp[Tx] {
	t.Helper()

	db, store := be.newStore(ctx, t)
	a := rulesAppOver(t, be, shape, db, store)

	// An administrator opens a method for the audience its registration has, so the
	// methods are opened once they are registered, and a method the shape does not
	// register has nothing to open.
	registered := map[string]accounts{"entra": shape.entraAccounts}
	if !shape.entraRoles {
		registered["google"] = shape.googleAccounts
	}
	for name, chosen := range registered {
		if chosen == openSignUp {
			openMethod(ctx, t, store, a.api, name, shape.defaultRole)
		}
	}

	return a
}

// release is the application of a later release over the same database: its
// registrations are shape's, and the rows of the sign-in methods table stand as an
// administrator wrote them under the release before. Nothing is opened here, whatever
// shape says was chosen: a release changes code, and the choice is a row.
func (a *rulesApp[Tx]) release(t *testing.T, be backend[Tx], shape *rulesShape) *rulesApp[Tx] {
	t.Helper()

	return rulesAppOver(t, be, shape, a.db, a.store)
}

// rulesAppOver builds the application of shape over a database that is there: the Auth,
// its registrations against two fakes of their own, and the server.
func rulesAppOver[Tx any](t *testing.T, be backend[Tx], shape *rulesShape, db appDB, store *sessionstorage.Auth[Tx]) *rulesApp[Tx] {
	t.Helper()

	h := &hooks[Tx]{rows: be.rows}
	entraIDP := oidctest.NewFakeEntra(t, "entra-client")
	googleIDP := oidctest.NewFakeIDP(t, "google-client")

	recorder := &mail.Recorder{}
	auth, err := session.NewAuth[appSessionData, appUserData](withCustomData(t, store, h), cookieKey, session.NoSuperAdministrators(),
		session.WithIdentityLinked(h.identityLinked), session.WithPendingHook(h.pendingHook),
		session.WithMail(recorder, session.MailSettings{Application: "Lakeside", LoginURL: "https://app.example/login"}))
	if err != nil {
		t.Fatalf("session.NewAuth() error = %v", err)
	}
	auth.SetSignInPolicy(h.signInPolicy)
	// The default role of an open method is written as the new user's membership row,
	// in the transaction that makes the account, where the session's custom data
	// resolver reads it back for the first session.
	auth.SetSignUpSeated(func(ctx context.Context, tx Tx, userID impulse.UUID, _, _, role string) (func(ctx context.Context), error) {
		return nil, be.rows.addMember(ctx, tx, userID, role)
	})

	var entraRule azure.AccountRule[Tx]
	if shape.entraRule {
		entraRule = h.azureResolver
	}
	roleSync := signin.DisableRoleSync()
	if shape.entraRoles {
		roleSync = signin.RoleSync(rolesNeverAsked{t: t})
	}
	entra := azure.SignIn(auth, "entra", entraRule, &azure.Config{
		RoleSync: roleSync, Authority: entraIDP.Server.URL, Tenants: shape.tenants,
		ClientID: "entra-client", ClientSecret: "secret", RedirectURL: "https://app.example/entra/callback",
	})
	var googleRule google.AccountRule[Tx]
	if shape.googleRule {
		googleRule = h.googleResolver
	}
	// A registration that synchronizes roles stands alone on its Auth, so the Google
	// method is registered beside the Entra one only where the Entra one leaves the
	// roles to the application.
	var workspace *google.Method
	if !shape.entraRoles {
		googleIssuerMu.Lock()
		issuer := google.IssuerURL
		google.IssuerURL = googleIDP.Server.URL
		workspace = google.SignIn(auth, "google", googleRule, &google.Config{
			RoleSync: google.DisableRoleSync(), Workspaces: shape.workspaces,
			ClientID: "google-client", ClientSecret: "secret", RedirectURL: "https://app.example/google/callback",
		})
		google.IssuerURL = issuer
		googleIssuerMu.Unlock()
	}

	r := chi.NewRouter()
	mountAuth(r, auth, func(r chi.Router) {
		r.Get("/entra/login", entra.Login())
		r.Get("/entra/callback", entra.Callback())
		r.Get("/entra/logout", entra.FrontChannelLogout())
		if workspace != nil {
			r.Get("/google/login", workspace.Login())
			r.Get("/google/callback", workspace.Callback())
		}
	})
	server := httptest.NewTLSServer(r)
	t.Cleanup(server.Close)

	return &rulesApp[Tx]{
		authApp: &authApp[Tx]{db: db, rows: be.rows, server: server, api: auth.API(), hooks: h, mail: recorder},
		entra:   entraIDP, google: googleIDP, store: store,
	}
}

// openMethod writes the row of a method an administrator opened to sign-up, with role
// as its default role, "" for none. The row is opened for the audience the method's
// registration has, as the generated management writes it: a row opened for another
// audience opens nothing.
func openMethod[Tx any](
	ctx context.Context, t *testing.T, store *sessionstorage.Auth[Tx], api *session.AuthAPI[appSessionData, appUserData, Tx], name, role string,
) {
	t.Helper()

	row := &sessionstorage.SignInMethod{Name: name, OpenSignUp: true}
	for _, method := range api.SignInMethods() {
		if method.Name == name {
			audience := method.Audience.Key()
			row.OpenedFor = &audience
		}
	}
	if row.OpenedFor == nil {
		t.Fatalf("no sign-in method is registered under %q", name)
	}
	if role != "" {
		row.DefaultRole = &role
	}
	if err := store.SetSignInMethod(ctx, row); err != nil {
		t.Fatalf("SetSignInMethod(%q) error = %v", name, err)
	}
}

// closeMethod writes the row of a method an administrator made invite only again.
func closeMethod[Tx any](ctx context.Context, t *testing.T, store *sessionstorage.Auth[Tx], name string) {
	t.Helper()

	if err := store.SetSignInMethod(ctx, &sessionstorage.SignInMethod{Name: name}); err != nil {
		t.Fatalf("SetSignInMethod(%q) error = %v", name, err)
	}
}

// entraSignIn runs an Entra round trip with claims in b and returns where the callback
// sent the browser.
func (a *rulesApp[Tx]) entraSignIn(ctx context.Context, b *browser, claims map[string]any) *url.URL {
	b.t.Helper()

	return b.oidcSignIn(ctx, a.entra, "/entra", claims)
}

// googleSignIn runs a Google round trip with claims in b and returns where the
// callback sent the browser.
func (a *rulesApp[Tx]) googleSignIn(ctx context.Context, b *browser, claims map[string]any) *url.URL {
	b.t.Helper()

	return b.oidcSignIn(ctx, a.google, "/google", claims)
}

// createUser creates a user without a password: pre-created for an external identity.
func (a *authApp[Tx]) createUser(ctx context.Context, t *testing.T, username string) impulse.UUID {
	t.Helper()

	id, err := a.api.CreateSessionUser(ctx, &session.CreateUserRequest{Username: username})
	if err != nil {
		t.Fatalf("CreateSessionUser(%q) error = %v", username, err)
	}

	return id
}

// links lists the identity links of the user whose id the session view carries.
func (a *authApp[Tx]) links(ctx context.Context, t *testing.T, userID string) []*sessionstorage.SessionIdentity {
	t.Helper()

	links, err := a.api.Identities(ctx, impulse.Must(impulse.UUIDFromString(userID)))
	if err != nil {
		t.Fatalf("Identities(%s) error = %v", userID, err)
	}

	return links
}

// assertNoRow fails unless b has no session and no user exists under username: a
// refusal before any row was written.
func (a *authApp[Tx]) assertNoRow(ctx context.Context, t *testing.T, b *browser, username string) {
	t.Helper()

	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if a.db.hasUser(ctx, t, username) {
		t.Errorf("a user exists under %q, want none written", username)
	}
}

// signedInAs fails unless b is signed in under username, and returns the session view.
func signedInAs(ctx context.Context, t *testing.T, b *browser, username string) *sessionView {
	t.Helper()

	v := b.view(ctx)
	if v.Username != username {
		t.Errorf("session username = %q, want %q", v.Username, username)
	}

	return v
}

// assertOneLink fails unless links is exactly one link, of connection with tenant as
// its tenant key.
func assertOneLink(t *testing.T, links []*sessionstorage.SessionIdentity, connection, tenant string) {
	t.Helper()

	got := make([]string, 0, len(links))
	for _, l := range links {
		key := "<nil>"
		if l.Tenant != nil {
			key = *l.Tenant
		}
		got = append(got, string(l.Method)+":"+l.Connection+":"+l.Subject+"@"+key)
	}
	if len(links) != 1 || links[0].Connection != connection || links[0].Tenant == nil || *links[0].Tenant != tenant {
		t.Errorf("identity links = %v, want one of connection %q with tenant key %q", got, connection, tenant)
	}
}

// pendingConfirmation fails unless the sign-in that sent the browser to redirect waits
// for a password confirmation, then confirms it with password and returns the view.
func confirmWithPassword(ctx context.Context, t *testing.T, b *browser, redirect *url.URL, password string) *sessionView {
	t.Helper()

	assertRedirect(t, redirect, "/login", url.Values{"pending": {"confirmation"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/confirm", map[string]string{"password": password})

	return b.view(ctx)
}

// TestAuthSeams_SignInRules runs the sign-in rule scenarios on each backend.
func TestAuthSeams_SignInRules(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		runSignInRuleSeams(t, postgresBackend)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		runSignInRuleSeams(t, spannerBackend)
	})
}

func runSignInRuleSeams[Tx any](t *testing.T, be backend[Tx]) {
	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, be backend[Tx])
	}{
		{
			name: "open to sign-up: a stranger is provisioned under the sign-in name with the tenant id as the link's tenant key, signs in through the link afterwards, and keeps the username after the directory renames the person",
			run:  seamRulesMembersProvision[Tx],
		},
		{
			name: "open to sign-up: a pre-created user with a password confirms with it before the link is made, matched without regard to case and keeping its spelling, and a pre-created user without a password or an identity is linked outright",
			run:  seamRulesMembersPreCreated[Tx],
		},
		{
			name: "a registration that synchronizes roles: a token asserting no application role is refused when the registration synchronizes roles, before any row is written or any role is read",
			run:  seamRulesMembersNoRoles[Tx],
		},
		{
			name: "invite only, a method with no row: a pre-created user is linked, one with a password confirms, and a stranger is refused with not_invited and a guest with guest_account, both without a row",
			run:  seamRulesInvitedUsersOnly[Tx],
		},
		{
			name: "open to sign-up with a default role: a stranger gets an account that holds the role from its first session, a person with an account and a person with an invitation sign in as they do through an invite-only method and are given no default role, and once the method is made invite only again the next stranger is refused with not_invited and nothing is written, while the people who have accounts keep them",
			run:  seamRulesOpenDefaultRole[Tx],
		},
		{
			name: "an invitation carried by a directory sign-in is accepted only by the invited address: another Workspace account is refused with invitation_address, nothing written and the invitation standing; the invited address then signs in through it, provisioned through the invite-only method, and the invitation is consumed",
			run:  seamRulesInvitationAddress[Tx],
		},
		{
			name: "any tenant and any Google account, open to sign-up: a work or school account of a tenant the application never heard of and a Google account outside any Workspace get accounts keyed by where they came from, a personal Microsoft account is refused, and an unverified Google email is refused, each before any user is resolved",
			run:  seamRulesOpenSignUp[Tx],
		},
		{
			name: "personal accounts beside one tenant, invite only: a pre-created person signs in with a Microsoft personal account under the consumer tenant, and a work account of an unlisted tenant is refused without a row",
			run:  seamRulesPersonalAccounts[Tx],
		},
		{
			name: "listed tenants: each listed tenant is admitted and the link carries the name the registration gives it, a person from the other tenant is a new identity, the same sign-in name across tenants is refused by the cross-connection guard, and an unlisted tenant is refused without a row",
			run:  seamRulesListedTenants[Tx],
		},
		{
			name: "a custom rule runs first: it refuses with its own code before any row, hands the rest to the standard rule with a tenant key of its own, and never runs again once the identity is linked",
			run:  seamRulesCustomRuleFirst[Tx],
		},
		{
			name: "the policy decides every session start: a provisioning sign-in it denies starts no session (the account the rule made stays, for the policy to decide again), the MFA completion is decided again, and a session the application starts through the API is decided too",
			run:  seamRulesPolicyEveryStart[Tx],
		},
		{
			name: "an Entra front-channel logout on a registration serving two tenants ends the sessions under the issuer it names and no other, refuses an issuer outside the slot, and one naming no issuer, as Entra sends it, ends the sessions under whichever of the slot's issuers carry the sid",
			run:  seamRulesLogoutAcrossTenants[Tx],
		},
		{
			name: "listed Workspaces: the hd claim selects the entry and the link carries its name as connection and tenant key, a person from the other Workspace is a new identity, and a Workspace outside the list is refused without a row",
			run:  seamRulesListedWorkspaces[Tx],
		},
		{
			name: "a token whose amr names mfa attests by evidence, so a policy requiring the email code proceeds with an attested-by-evidence event; a token without it, and a Google sign-in, are held for the code",
			run:  seamRulesAttestedByEvidence[Tx],
		},
		{
			name: "a Workspace and a tenant declared Attesting() attest by declaration, so the policy proceeds with an attested-by-declaration event under the connection; evidence still wins where the token carries it",
			run:  seamRulesAttestedByDeclaration[Tx],
		},
		{
			name: "opened for one tenant: a later release lists a second tenant under the label of the first, and the method is invite only for both until an administrator opens it again, while a person who has an account signs in as before",
			run:  seamRulesOpenedForOneTenant[Tx],
		},
		{
			name: "any Workspace, invite only: a pre-created person signs in from a Workspace the application never named under its domain as connection, a stranger from another Workspace is refused with not_invited, and an account outside any Workspace is refused",
			run:  seamRulesAnyWorkspace[Tx],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.run(t.Context(), t, be)
		})
	}
}

// seamRulesMembersProvision is the scenario "open to sign-up: a stranger is provisioned".
func seamRulesMembersProvision[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(openSignUp))

	b := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, b, azureClaims(azureTenant, "oid-pat", "p.lee@lakeside.edu")), "/home", url.Values{})
	v := signedInAs(ctx, t, b, "p.lee@lakeside.edu")
	// The fake Entra asserts mfa in every token: the session records the evidence
	// after the sign-in's own event.
	assertEventsSeen(t, v, "azure:"+azureTenant, "attested-by-evidence:"+azureTenant)
	assertOneLink(t, a.links(ctx, t, v.UserID), azureTenant, azureTenant)

	// The identity is linked: the next sign-in goes through the link, and the
	// directory's new spelling of the name changes nothing.
	again := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, again, azureClaims(azureTenant, "oid-pat", "pat.lee@lakeside.edu")), "/home", url.Values{})
	if got := again.view(ctx); got.UserID != v.UserID || got.Username != "p.lee@lakeside.edu" {
		t.Errorf("second sign-in = user %s %q, want %s %q: the link decides, and the username is never renamed", got.UserID, got.Username, v.UserID, "p.lee@lakeside.edu")
	}
	if got := a.db.username(ctx, t, impulse.Must(impulse.UUIDFromString(v.UserID))); got != "p.lee@lakeside.edu" {
		t.Errorf("stored username after the rename = %q, want %q", got, "p.lee@lakeside.edu")
	}
	if links := a.links(ctx, t, v.UserID); len(links) != 1 {
		t.Errorf("identity links after the second sign-in = %d, want the one", len(links))
	}
}

// seamRulesMembersPreCreated is the scenario "open to sign-up: a pre-created user".
func seamRulesMembersPreCreated[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(openSignUp))
	pat := a.seed(ctx, t, "pat@lakeside.edu")
	kim := a.createUser(ctx, t, "kim@lakeside.edu")

	b := a.browser(t)
	b.prime(ctx)
	redirect := a.entraSignIn(ctx, b, azureClaims(azureTenant, "oid-pat", "Pat@Lakeside.edu"))
	if links := a.links(ctx, t, pat.String()); len(links) != 0 {
		t.Errorf("identity links before the confirmation = %d, want none", len(links))
	}
	v := confirmWithPassword(ctx, t, b, redirect, seedPassword)
	if v.UserID != pat.String() || v.Username != "pat@lakeside.edu" {
		t.Errorf("confirmed session = user %s %q, want %s %q", v.UserID, v.Username, pat, "pat@lakeside.edu")
	}
	assertEventsSeen(t, v, "azure:"+azureTenant, "attested-by-evidence:"+azureTenant, "link-confirmation")
	assertOneLink(t, a.links(ctx, t, pat.String()), azureTenant, azureTenant)

	k := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, k, azureClaims(azureTenant, "oid-kim", "kim@lakeside.edu")), "/home", url.Values{})
	if kv := signedInAs(ctx, t, k, "kim@lakeside.edu"); kv.UserID != kim.String() {
		t.Errorf("session user = %s, want the pre-created %s", kv.UserID, kim)
	}
	assertOneLink(t, a.links(ctx, t, kim.String()), azureTenant, azureTenant)
}

// seamRulesMembersNoRoles is the scenario "a registration that synchronizes roles".
func seamRulesMembersNoRoles[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	shape := oneOrganization(openSignUp)
	shape.entraRoles = true
	a := newRulesApp(ctx, t, be, shape)

	b := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, b, azureClaims(azureTenant, "oid-x", "x@lakeside.edu")), "/login", url.Values{"code": {"no_roles"}})
	a.assertNoRow(ctx, t, b, "x@lakeside.edu")
}

// seamRulesInvitedUsersOnly is the scenario "invite only, a method with no row".
func seamRulesInvitedUsersOnly[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(inviteOnly))
	kim := a.createUser(ctx, t, "kim@lakeside.edu")
	pat := a.seed(ctx, t, "pat@lakeside.edu")

	k := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, k, azureClaims(azureTenant, "oid-kim", "kim@lakeside.edu")), "/home", url.Values{})
	if kv := signedInAs(ctx, t, k, "kim@lakeside.edu"); kv.UserID != kim.String() {
		t.Errorf("session user = %s, want the pre-created %s", kv.UserID, kim)
	}

	p := a.browser(t)
	p.prime(ctx)
	if pv := confirmWithPassword(ctx, t, p, a.entraSignIn(ctx, p, azureClaims(azureTenant, "oid-pat", "pat@lakeside.edu")), seedPassword); pv.UserID != pat.String() {
		t.Errorf("confirmed session user = %s, want %s", pv.UserID, pat)
	}

	s := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, s, azureClaims(azureTenant, "oid-stranger", "stranger@lakeside.edu")), "/login", url.Values{"code": {"not_invited"}})
	a.assertNoRow(ctx, t, s, "stranger@lakeside.edu")

	g := a.browser(t)
	guest := azureClaims(azureTenant, "oid-guest", "visitor_gmail.com#EXT#@lakeside.onmicrosoft.com")
	assertRedirect(t, a.entraSignIn(ctx, g, guest), "/login", url.Values{"code": {"guest_account"}})
	a.assertNoRow(ctx, t, g, "visitor_gmail.com#EXT#@lakeside.onmicrosoft.com")
}

// seamRulesInvitationAddress is the scenario "an invitation carried by a directory
// sign-in is accepted only by the invited address".
func seamRulesInvitationAddress[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(inviteOnly))
	inv, err := a.api.Invite(ctx, &session.InvitationRequest{Email: "pat@lakeside.edu", Name: "  Pat Invited "})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	if inv.Name != "Pat Invited" {
		t.Errorf("Invite() name = %q, want the name without the space around it", inv.Name)
	}
	token := invitationToken(t, a.mail)

	// The browser opens the link: it carries the invitation from here on.
	b := a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": token}, b.xsrfToken())

	// Another account of the Workspace follows the link: refused as not the invited
	// address, no account created, the invitation still standing.
	assertRedirect(t, a.googleSignIn(ctx, b, googleClaims("sub-kim", "kim@lakeside.edu")), "/login", url.Values{"code": {"invitation_address"}})
	a.assertNoRow(ctx, t, b, "kim@lakeside.edu")
	if standing := a.standingInvitations(ctx, t); len(standing) != 1 || standing[0].ID != inv.ID || standing[0].Name != "Pat Invited" {
		t.Errorf("standing invitations after the refused sign-in = %v, want the one invitation still standing, with its name", standing)
	}

	// The invited address, in any letter case, accepts it: provisioned through the
	// invite-only method, signed in, the invitation consumed.
	assertRedirect(t, a.googleSignIn(ctx, b, googleClaims("sub-pat", "Pat@Lakeside.edu")), "/home", url.Values{})
	signedInAs(ctx, t, b, "Pat@Lakeside.edu")
	if standing := a.standingInvitations(ctx, t); len(standing) != 0 {
		t.Errorf("standing invitations after the acceptance = %v, want none", standing)
	}
	// The directory asserted no name, so the new account takes the invitation's.
	if got := b.authenticatedName(ctx); got != "Pat Invited" {
		t.Errorf("the new account's name = %q, want the invitation's", got)
	}

	// A person signed in accepts by hand only an invitation to their own address: one
	// for another address is refused, the invitation left standing; their own, in any
	// letter case, is accepted.
	other, err := a.api.Invite(ctx, &session.InvitationRequest{Email: "sam@lakeside.edu"})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": lastInvitationToken(t, a.mail)}, b.xsrfToken())
	b.expect(ctx, http.StatusForbidden, http.MethodPost, "/invitation/accept", nil, b.xsrfToken())
	if standing := a.standingInvitations(ctx, t); len(standing) != 1 || standing[0].ID != other.ID {
		t.Errorf("standing invitations after the refused acceptance = %v, want the other address's still standing", standing)
	}
	if _, err := a.api.Invite(ctx, &session.InvitationRequest{Email: "PAT@lakeside.edu", Name: "Patricia"}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": lastInvitationToken(t, a.mail)}, b.xsrfToken())
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation/accept", nil, b.xsrfToken())
	if standing := a.standingInvitations(ctx, t); len(standing) != 1 || standing[0].ID != other.ID {
		t.Errorf("standing invitations after the acceptance by hand = %v, want only the other address's", standing)
	}
	// The account holds a name, so the second invitation's is not written over it.
	if got := b.authenticatedName(ctx); got != "Pat Invited" {
		t.Errorf("the account's name after accepting a second invitation = %q, want the one it held", got)
	}
	if _, err := a.api.Invite(ctx, &session.InvitationRequest{Email: "long@lakeside.edu", Name: strings.Repeat("n", 201)}); !httpio.HasBadRequest(err) {
		t.Errorf("Invite() with a name too long to keep: error = %v, want a bad request", err)
	}
}

// seamRulesOpenDefaultRole is the scenario "open to sign-up with a default role".
func seamRulesOpenDefaultRole[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	shape := oneOrganization(openSignUp)
	shape.defaultRole = "Member"
	a := newRulesApp(ctx, t, be, shape)
	kim := a.createUser(ctx, t, "kim@lakeside.edu")

	// A person with no account and no invitation: the account is made, and its first
	// session already reads the default role, written in the transaction that made the
	// account.
	b := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, b, azureClaims(azureTenant, "oid-pat", "pat@lakeside.edu")), "/home", url.Values{})
	pat := signedInAs(ctx, t, b, "pat@lakeside.edu")
	if got := b.sessionData(ctx).Partner; got != "Member" {
		t.Errorf("the new account holds %q, want the method's default role Member", got)
	}

	// A person an administrator created an account for is linked to it, and holds what
	// the administrator gave them: no default role.
	k := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, k, azureClaims(azureTenant, "oid-kim", "kim@lakeside.edu")), "/home", url.Values{})
	if kv := signedInAs(ctx, t, k, "kim@lakeside.edu"); kv.UserID != kim.String() {
		t.Errorf("session user = %s, want the pre-created %s", kv.UserID, kim)
	}
	if got := k.sessionData(ctx).Partner; got != "" {
		t.Errorf("the pre-created account holds %q, want nothing given by the method", got)
	}

	// A person with an invitation gets the account the invitation makes: the
	// invitation is consumed, and the method's default role is not given beside it.
	if _, err := a.api.Invite(ctx, &session.InvitationRequest{Email: "sam@lakeside.edu"}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	i := a.browser(t)
	i.prime(ctx)
	i.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": invitationToken(t, a.mail)}, i.xsrfToken())
	assertRedirect(t, a.googleSignIn(ctx, i, googleClaims("sub-sam", "sam@lakeside.edu")), "/home", url.Values{})
	signedInAs(ctx, t, i, "sam@lakeside.edu")
	if standing := a.standingInvitations(ctx, t); len(standing) != 0 {
		t.Errorf("standing invitations after the acceptance = %v, want none", standing)
	}
	if got := i.sessionData(ctx).Partner; got != "" {
		t.Errorf("the invited account holds %q, want what the invitation gives and no default role", got)
	}

	// An administrator makes both methods invite only again: the next stranger is
	// refused and nothing is written for them.
	closeMethod(ctx, t, a.store, "entra")
	closeMethod(ctx, t, a.store, "google")
	stranger := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, stranger, azureClaims(azureTenant, "oid-lee", "lee@lakeside.edu")), "/login", url.Values{"code": {"not_invited"}})
	a.assertNoRow(ctx, t, stranger, "lee@lakeside.edu")
	g := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, g, googleClaims("sub-lee", "lee@lakeside.edu")), "/login", url.Values{"code": {"not_invited"}})
	a.assertNoRow(ctx, t, g, "lee@lakeside.edu")

	// The people who have accounts keep them, and what they hold.
	again := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, again, azureClaims(azureTenant, "oid-pat", "pat@lakeside.edu")), "/home", url.Values{})
	if got := signedInAs(ctx, t, again, "pat@lakeside.edu"); got.UserID != pat.UserID {
		t.Errorf("sign-in after the method was made invite only = user %s, want the account %s it made while open", got.UserID, pat.UserID)
	}
	if got := again.sessionData(ctx).Partner; got != "Member" {
		t.Errorf("the account holds %q after the method was made invite only, want the role it was given", got)
	}
	kimAgain := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, kimAgain, azureClaims(azureTenant, "oid-kim", "kim@lakeside.edu")), "/home", url.Values{})
	signedInAs(ctx, t, kimAgain, "kim@lakeside.edu")

	// An invitation is the way in through an invite-only method, as it always was.
	if _, err := a.api.Invite(ctx, &session.InvitationRequest{Email: "lee@lakeside.edu"}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	invited := a.browser(t)
	invited.prime(ctx)
	invited.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": lastInvitationToken(t, a.mail)}, invited.xsrfToken())
	assertRedirect(t, a.entraSignIn(ctx, invited, azureClaims(azureTenant, "oid-lee", "lee@lakeside.edu")), "/home", url.Values{})
	signedInAs(ctx, t, invited, "lee@lakeside.edu")
}

// invitationToken is the token of the one invitation the recorder holds.
func invitationToken(t *testing.T, recorder *mail.Recorder) string {
	t.Helper()

	messages := recorder.Messages()
	if len(messages) != 1 {
		t.Fatalf("mailed messages = %d, want the one invitation", len(messages))
	}

	return tokenInMessage(t, messages[0].Text)
}

// lastInvitationToken is the token of the invitation the recorder received last.
func lastInvitationToken(t *testing.T, recorder *mail.Recorder) string {
	t.Helper()

	messages := recorder.Messages()
	if len(messages) == 0 {
		t.Fatal("mailed messages = 0, want an invitation")
	}

	return tokenInMessage(t, messages[len(messages)-1].Text)
}

// tokenInMessage is the invitation token in a mailed message's link.
func tokenInMessage(t *testing.T, text string) string {
	t.Helper()

	m := regexp.MustCompile(`https://app\.example/login\?invitation=([A-Za-z0-9_-]+)`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no invitation link in the message:\n%s", text)
	}

	return m[1]
}

// standingInvitations lists the invitations that stand.
func (a *authApp[Tx]) standingInvitations(ctx context.Context, t *testing.T) []*sessioninfo.Invitation {
	t.Helper()

	standing, err := a.api.Invitations(ctx)
	if err != nil {
		t.Fatalf("Invitations() error = %v", err)
	}

	return standing
}

// seamRulesOpenSignUp is the scenario "any tenant and any Google account, open to sign-up".
func seamRulesOpenSignUp[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, &rulesShape{
		tenants: azure.AnyTenant(), entraAccounts: openSignUp,
		workspaces: google.AnyGoogleAccount(), googleAccounts: openSignUp,
	})

	b := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, b, azureClaims(otherTenant, "oid-z", "someone@contoso.example")), "/home", url.Values{})
	v := signedInAs(ctx, t, b, "someone@contoso.example")
	assertOneLink(t, a.links(ctx, t, v.UserID), otherTenant, otherTenant)

	personal := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, personal, azureClaims(azure.ConsumerTenantID, "oid-p", "pat@outlook.example")), "/login", url.Values{"code": {"personal_account"}})
	a.assertNoRow(ctx, t, personal, "pat@outlook.example")

	g := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, g, map[string]any{"sub": "g-sub-pat", "email": "pat@gmail.example", "email_verified": true}), "/home", url.Values{})
	gv := signedInAs(ctx, t, g, "pat@gmail.example")
	assertOneLink(t, a.links(ctx, t, gv.UserID), google.AnyAccountConnection, google.AnyAccountConnection)

	u := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, u, map[string]any{"sub": "g-sub-u", "email": "u@gmail.example", "email_verified": false}), "/login", url.Values{"code": {"email_not_verified"}})
	a.assertNoRow(ctx, t, u, "u@gmail.example")
}

// seamRulesPersonalAccounts is the scenario "personal accounts beside one tenant, invite only".
func seamRulesPersonalAccounts[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, &rulesShape{
		tenants: azure.Tenants(azure.Tenant(azureTenant), azure.PersonalAccounts()), entraAccounts: inviteOnly,
		workspaces: google.Workspace("lakeside", hostedDomain), googleAccounts: inviteOnly,
	})
	pat := a.createUser(ctx, t, "pat@outlook.example")

	b := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, b, azureClaims(azure.ConsumerTenantID, "oid-p", "pat@outlook.example")), "/home", url.Values{})
	if v := signedInAs(ctx, t, b, "pat@outlook.example"); v.UserID != pat.String() {
		t.Errorf("session user = %s, want the pre-created %s", v.UserID, pat)
	}
	assertOneLink(t, a.links(ctx, t, pat.String()), azure.ConsumerTenantID, azure.ConsumerTenantID)

	o := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, o, azureClaims(otherTenant, "oid-o", "o@other.example")), "/login", url.Values{"code": {"unlisted_tenant"}})
	a.assertNoRow(ctx, t, o, "o@other.example")
}

// seamRulesListedTenants is the scenario "listed tenants".
func seamRulesListedTenants[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, &rulesShape{
		tenants: azure.Tenants(azure.NamedTenant(azureTenant, "lakeside"), azure.Tenant(riversideTenant)), entraAccounts: openSignUp,
		workspaces: google.Workspace("lakeside", hostedDomain), googleAccounts: openSignUp,
	})

	lake := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, lake, azureClaims(azureTenant, "oid-pat", "p.lee@lakeside.edu")), "/home", url.Values{})
	lv := signedInAs(ctx, t, lake, "p.lee@lakeside.edu")
	assertOneLink(t, a.links(ctx, t, lv.UserID), azureTenant, "lakeside")

	river := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, river, azureClaims(riversideTenant, "oid-river", "river@riverside.edu")), "/home", url.Values{})
	rv := signedInAs(ctx, t, river, "river@riverside.edu")
	if rv.UserID == lv.UserID {
		t.Error("the riverside person signed in as the lakeside user, want a new identity")
	}
	assertOneLink(t, a.links(ctx, t, rv.UserID), riversideTenant, riversideTenant)

	// The same object id and sign-in name in the other tenant is another identity:
	// it would link the password-less lakeside user across connections, which
	// the guard refuses.
	same := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, same, azureClaims(riversideTenant, "oid-pat", "p.lee@lakeside.edu")), "/login", url.Values{"code": {"identity_rejected"}})
	same.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if links := a.links(ctx, t, lv.UserID); len(links) != 1 {
		t.Errorf("lakeside user's links after the refused cross-tenant sign-in = %d, want the one", len(links))
	}

	o := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, o, azureClaims(otherTenant, "oid-o", "o@other.example")), "/login", url.Values{"code": {"unlisted_tenant"}})
	a.assertNoRow(ctx, t, o, "o@other.example")
}

// seamRulesOpenedForOneTenant is the scenario "opened for one tenant". An administrator
// opens the Entra method while its registration admits one tenant. A later release lists
// a second tenant beside it and gives it the label the first one has, so the two
// registrations read alike in words. They admit different people: the row was opened for
// the first tenant's and counts for nobody of the second, whom nobody opened it for. The
// method is invite only until it is opened again, which an administrator does reading
// whom it admits now. A row that kept its audience in the earlier form, by the names of
// its organizations, is the row that let the second tenant in: it opens nothing.
func seamRulesOpenedForOneTenant[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	first := newRulesApp(ctx, t, be, &rulesShape{
		tenants: azure.Tenant(azureTenant), entraAccounts: openSignUp,
		workspaces: google.Workspace("lakeside", hostedDomain), googleAccounts: inviteOnly,
	})
	pat := first.browser(t)
	assertRedirect(t, first.entraSignIn(ctx, pat, azureClaims(azureTenant, "oid-pat", "pat@lakeside.edu")), "/home", url.Values{})
	patView := signedInAs(ctx, t, pat, "pat@lakeside.edu")

	// The label a tenant listed by its id alone has, as the page says it.
	var label string
	for _, method := range first.api.SignInMethods() {
		if method.Name == "entra" {
			label = method.Audience.Organizations[0]
		}
	}
	second := first.release(t, be, &rulesShape{
		tenants:    azure.Tenants(azure.Tenant(azureTenant), azure.NamedTenant(riversideTenant, label)),
		workspaces: google.Workspace("lakeside", hostedDomain), googleAccounts: inviteOnly,
	})

	// A stranger of the second tenant, and one of the first: nobody opened the method
	// for the people it admits now.
	river := second.browser(t)
	assertRedirect(t, second.entraSignIn(ctx, river, azureClaims(riversideTenant, "oid-river", "river@riverside.edu")), "/login", url.Values{"code": {"not_invited"}})
	second.assertNoRow(ctx, t, river, "river@riverside.edu")
	kim := second.browser(t)
	assertRedirect(t, second.entraSignIn(ctx, kim, azureClaims(azureTenant, "oid-kim", "kim@lakeside.edu")), "/login", url.Values{"code": {"not_invited"}})
	second.assertNoRow(ctx, t, kim, "kim@lakeside.edu")

	// A person who has an account is not signing up.
	again := second.browser(t)
	assertRedirect(t, second.entraSignIn(ctx, again, azureClaims(azureTenant, "oid-pat", "pat@lakeside.edu")), "/home", url.Values{})
	if v := signedInAs(ctx, t, again, "pat@lakeside.edu"); v.UserID != patView.UserID {
		t.Errorf("session user after the release = %s, want the account %s made before it", v.UserID, patView.UserID)
	}

	// Opened again, for the people the registration admits now.
	openMethod(ctx, t, second.store, second.api, "entra", "")
	assertRedirect(t, second.entraSignIn(ctx, river, azureClaims(riversideTenant, "oid-river", "river@riverside.edu")), "/home", url.Values{})
	signedInAs(ctx, t, river, "river@riverside.edu")

	// The row as it was kept before an audience was compared by what identifies its
	// organizations: their names, each once, which the two registrations share. No
	// migration reads such a row, and it opens the method for nobody.
	earlier := earlierAudienceKey(t, second.api, "entra")
	if err := second.store.SetSignInMethod(ctx, &sessionstorage.SignInMethod{Name: "entra", OpenSignUp: true, OpenedFor: &earlier}); err != nil {
		t.Fatalf("SetSignInMethod(%q) error = %v", "entra", err)
	}
	for _, stranger := range []struct{ tenant, oid, email string }{
		{tenant: riversideTenant, oid: "oid-lee", email: "lee@riverside.edu"},
		{tenant: azureTenant, oid: "oid-sam", email: "sam@lakeside.edu"},
	} {
		b := second.browser(t)
		assertRedirect(t, second.entraSignIn(ctx, b, azureClaims(stranger.tenant, stranger.oid, stranger.email)), "/login", url.Values{"code": {"not_invited"}})
		second.assertNoRow(ctx, t, b, stranger.email)
	}
}

// earlierAudienceKey is the audience the registration of a method has, as a row kept it
// before the key compared what identifies whom the method admits: the names of its
// organizations, each once and sorted, and the words for its accounts.
func earlierAudienceKey[Tx any](t *testing.T, api *session.AuthAPI[appSessionData, appUserData, Tx], name string) string {
	t.Helper()

	for _, method := range api.SignInMethods() {
		if method.Name != name {
			continue
		}
		organizations := slices.Compact(slices.Sorted(slices.Values(method.Audience.Organizations)))
		if organizations == nil {
			organizations = []string{}
		}
		key, err := json.Marshal(struct {
			Organizations []string `json:"organizations"`
			Accounts      string   `json:"accounts"`
		}{Organizations: organizations, Accounts: method.Audience.Accounts})
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}

		return string(key)
	}
	t.Fatalf("no sign-in method is registered under %q", name)

	return ""
}

// seamRulesCustomRuleFirst is the scenario "a custom rule runs first".
func seamRulesCustomRuleFirst[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	shape := oneOrganization(openSignUp)
	shape.entraRule = true
	a := newRulesApp(ctx, t, be, shape)
	a.hooks.set(func(h *hooks[Tx]) {
		h.resolveAzure = func(identity *azure.Identity) *signin.Resolution {
			if strings.HasSuffix(identity.PreferredUsername, "@blocked.example") {
				return signin.Reject("blocked")
			}

			return signin.Standard(signin.Tenant("partner-" + identity.Organization))
		}
	})

	blocked := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, blocked, azureClaims(azureTenant, "oid-b", "b@blocked.example")), "/login", url.Values{"code": {"blocked"}})
	a.assertNoRow(ctx, t, blocked, "b@blocked.example")

	b := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, b, azureClaims(azureTenant, "oid-pat", "p.lee@lakeside.edu")), "/home", url.Values{})
	v := signedInAs(ctx, t, b, "p.lee@lakeside.edu")
	assertOneLink(t, a.links(ctx, t, v.UserID), azureTenant, "partner-"+azureTenant)
	if resolved, _ := a.hooks.counts(); resolved != 2 {
		t.Errorf("the custom rule ran %d times, want 2: once per first sign-in", resolved)
	}

	again := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, again, azureClaims(azureTenant, "oid-pat", "p.lee@lakeside.edu")), "/home", url.Values{})
	if resolved, _ := a.hooks.counts(); resolved != 2 {
		t.Errorf("the custom rule ran %d times after a sign-in through the link, want still 2", resolved)
	}
}

// seamRulesPolicyEveryStart is the scenario "the policy decides every session start".
func seamRulesPolicyEveryStart[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(openSignUp))
	a.hooks.set(func(h *hooks[Tx]) {
		h.policy = func(req *sessioninfo.NewSessionRequest) *signin.Decision {
			switch {
			case req.User.Provisioned() && strings.HasPrefix(req.Username, "denied@"):
				return signin.Deny("sso_required")
			case req.Username == "mfa@lakeside.edu" && req.Reason == sessioninfo.ReasonLogin:
				return signin.RequireMFA()
			case req.Username == "mfa@lakeside.edu":
				return signin.Deny("step_denied")
			case req.Username == "kiosk@lakeside.edu":
				return signin.Deny("api_denied")
			}

			return signin.Allow()
		}
	})

	// The account rule's transaction commits before the policy's: the person's
	// account and link stay, and the policy decides their next sign-in again.
	denied := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, denied, azureClaims(azureTenant, "oid-d", "denied@lakeside.edu")), "/login", url.Values{"code": {"sso_required"}})
	denied.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if !a.db.hasUser(ctx, t, "denied@lakeside.edu") {
		t.Error("the account the rule provisioned is gone, want it kept: the policy decides sessions, not accounts")
	}

	// A token asserting the password alone: nothing attests, so the policy's
	// RequireMFA holds the sign-in (one asserting mfa would satisfy it).
	mfa := a.browser(t)
	mfa.prime(ctx)
	assertRedirect(t, a.entraSignIn(ctx, mfa, passwordOnlyClaims(azureTenant, "oid-m", "mfa@lakeside.edu")), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	if status, body := mfa.do(ctx, http.MethodPost, "/mfa", nil); status != http.StatusUnauthorized || !strings.Contains(string(body), "sign-in denied") {
		t.Errorf("MFA completion the policy denies = %d %s, want 401 with the policy's refusal", status, body)
	}
	mfa.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)

	kiosk := a.createUser(ctx, t, "kiosk@lakeside.edu")
	if _, err := a.api.StartAuthenticatedSession(ctx, httptest.NewRecorder(), kiosk, []sessioninfo.AuthEvent{{Method: "kiosk"}}); sessioninfo.LoginRefusalCodeOf(err) != "api_denied" {
		t.Errorf("StartAuthenticatedSession() error = %v, want the policy's refusal api_denied", err)
	}
}

// requireEmailCodeTx is the policy of an application whose every session carries a
// second factor, for the rules app: the library's email code, which an attestation
// stands in for.
func requireEmailCodeTx(*sessioninfo.NewSessionRequest) *signin.Decision {
	return signin.RequireMFA(sessioninfo.MethodEmailCode)
}

// passwordOnlyClaims are azureClaims whose amr names the password alone: Entra ran no
// second factor.
func passwordOnlyClaims(tid, oid, username string) map[string]any {
	claims := azureClaims(tid, oid, username)
	claims["amr"] = []string{"pwd"}

	return claims
}

// seamRulesAttestedByEvidence is the scenario "a token whose amr names mfa attests by evidence".
func seamRulesAttestedByEvidence[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(openSignUp))
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCodeTx })

	// Entra asserts mfa: the sign-in is never held, and the session records the
	// evidence after the sign-in's own event, under the same connection.
	attested := a.browser(t)
	attested.prime(ctx)
	assertRedirect(t, a.entraSignIn(ctx, attested, azureClaims(azureTenant, "oid-a", "ann@lakeside.edu")), "/home", url.Values{})
	assertEventsSeen(t, signedInAs(ctx, t, attested, "ann@lakeside.edu"), "azure:"+azureTenant, "attested-by-evidence:"+azureTenant)

	// Entra asserts the password alone: nothing vouches, the sign-in waits for the
	// code, and the status names the factor.
	held := a.browser(t)
	held.prime(ctx)
	assertRedirect(t, a.entraSignIn(ctx, held, passwordOnlyClaims(azureTenant, "oid-p", "pat@lakeside.edu")), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	held.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if body := held.expect(ctx, http.StatusOK, http.MethodGet, "/pending", nil); !strings.Contains(string(body), `"factors":["email-code"]`) {
		t.Errorf("pending status = %s, want the email code as the factor", body)
	}

	// Google puts no evidence in its token, and the Workspace is not declared: held.
	undeclared := a.browser(t)
	undeclared.prime(ctx)
	assertRedirect(t, a.googleSignIn(ctx, undeclared, googleClaims("g-sub-sam", "sam@lakeside.edu")), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	undeclared.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
}

// seamRulesAttestedByDeclaration is the scenario "a Workspace and a tenant declared Attesting() attest by declaration".
func seamRulesAttestedByDeclaration[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, &rulesShape{
		tenants: azure.Tenant(azureTenant).Attesting(), entraAccounts: openSignUp,
		workspaces: google.Workspace("lakeside", hostedDomain).Attesting(), googleAccounts: openSignUp,
	})
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCodeTx })

	// The Workspace the application administers vouches for its people.
	workspace := a.browser(t)
	workspace.prime(ctx)
	assertRedirect(t, a.googleSignIn(ctx, workspace, googleClaims("g-sub-sam", "sam@lakeside.edu")), "/home", url.Values{})
	assertEventsSeen(t, signedInAs(ctx, t, workspace, "sam@lakeside.edu"), "google:lakeside", "attested-by-declaration:lakeside")

	// So does the tenant, where the token carries no evidence.
	declared := a.browser(t)
	declared.prime(ctx)
	assertRedirect(t, a.entraSignIn(ctx, declared, passwordOnlyClaims(azureTenant, "oid-p", "pat@lakeside.edu")), "/home", url.Values{})
	assertEventsSeen(t, signedInAs(ctx, t, declared, "pat@lakeside.edu"), "azure:"+azureTenant, "attested-by-declaration:"+azureTenant)

	// Evidence in the token is recorded as such, declaration or not.
	evidence := a.browser(t)
	evidence.prime(ctx)
	assertRedirect(t, a.entraSignIn(ctx, evidence, azureClaims(azureTenant, "oid-a", "ann@lakeside.edu")), "/home", url.Values{})
	assertEventsSeen(t, signedInAs(ctx, t, evidence, "ann@lakeside.edu"), "azure:"+azureTenant, "attested-by-evidence:"+azureTenant)

	// The declaration reaches the sign-in's events on the second sign-in too, through
	// the link, and a session the SignedInHook is told of carries them.
	again := a.browser(t)
	again.prime(ctx)
	assertRedirect(t, a.googleSignIn(ctx, again, googleClaims("g-sub-sam", "sam@lakeside.edu")), "/home", url.Values{})
	assertEventsSeen(t, again.view(ctx), "google:lakeside", "attested-by-declaration:lakeside")
}

// seamRulesLogoutAcrossTenants is the scenario "an Entra front-channel logout on a registration serving two tenants ends the sessions under the issuer it names and no other, refuses an issuer outside the slot, and one naming no issuer, as Entra sends it, ends the sessions under whichever of the slot's issuers carry the sid".
func seamRulesLogoutAcrossTenants[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, &rulesShape{
		tenants: azure.Tenants(azure.Tenant(azureTenant), azure.Tenant(riversideTenant)), entraAccounts: openSignUp,
		workspaces: google.Workspace("lakeside", hostedDomain), googleAccounts: openSignUp,
	})

	lake := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, lake, withSID(azureClaims(azureTenant, "oid-a", "a@lakeside.edu"), "sid-shared")), "/home", url.Values{})
	river := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, river, withSID(azureClaims(riversideTenant, "oid-b", "b@riverside.edu"), "sid-shared")), "/home", url.Values{})
	lake.view(ctx)
	river.view(ctx)

	provider := a.browser(t)
	provider.expect(ctx, http.StatusBadRequest, http.MethodGet, "/entra/logout?sid=sid-shared&iss="+url.QueryEscape(a.entra.TenantIssuer(otherTenant)), nil)
	lake.view(ctx)
	river.view(ctx)

	provider.expect(ctx, http.StatusOK, http.MethodGet, "/entra/logout?sid=sid-shared&iss="+url.QueryEscape(a.entra.TenantIssuer(azureTenant)), nil)
	lake.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if v := river.view(ctx); v.Username != "b@riverside.edu" {
		t.Errorf("the riverside session after lakeside's logout = %+v, want it still signed in", v)
	}

	// Entra sends the sid alone: the riverside session, under the slot's other issuer, ends.
	provider.expect(ctx, http.StatusOK, http.MethodGet, "/entra/logout?sid=sid-shared", nil)
	river.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
}

// seamRulesListedWorkspaces is the scenario "listed Workspaces".
func seamRulesListedWorkspaces[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, &rulesShape{
		tenants: azure.Tenant(azureTenant), entraAccounts: openSignUp,
		workspaces:     google.Workspaces(google.Workspace("lakeside", hostedDomain), google.Workspace("riverside", riversideDomain)),
		googleAccounts: openSignUp,
	})

	lake := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, lake, googleClaimsAt(hostedDomain, "sub-pat", "pat@lakeside.edu")), "/home", url.Values{})
	lv := signedInAs(ctx, t, lake, "pat@lakeside.edu")
	assertEventsSeen(t, lv, "google:lakeside")
	assertOneLink(t, a.links(ctx, t, lv.UserID), "lakeside", "lakeside")

	river := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, river, googleClaimsAt(riversideDomain, "sub-sam", "sam@riverside.edu")), "/home", url.Values{})
	rv := signedInAs(ctx, t, river, "sam@riverside.edu")
	if rv.UserID == lv.UserID {
		t.Error("the riverside person signed in as the lakeside user, want a new identity")
	}
	assertOneLink(t, a.links(ctx, t, rv.UserID), "riverside", "riverside")

	o := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, o, googleClaimsAt("elsewhere.example", "sub-x", "x@elsewhere.example")), "/login", url.Values{"code": {"not_workspace_member"}})
	a.assertNoRow(ctx, t, o, "x@elsewhere.example")
}

// seamRulesAnyWorkspace is the scenario "any Workspace, invited users only".
func seamRulesAnyWorkspace[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, &rulesShape{
		tenants: azure.Tenant(azureTenant), entraAccounts: inviteOnly,
		workspaces: google.AnyWorkspace(), googleAccounts: inviteOnly,
	})
	pat := a.createUser(ctx, t, "pat@hilltop.edu")

	b := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, b, googleClaimsAt("Hilltop.edu", "sub-h", "pat@hilltop.edu")), "/home", url.Values{})
	if v := signedInAs(ctx, t, b, "pat@hilltop.edu"); v.UserID != pat.String() {
		t.Errorf("session user = %s, want the pre-created %s", v.UserID, pat)
	}
	assertOneLink(t, a.links(ctx, t, pat.String()), "hilltop.edu", "hilltop.edu")

	s := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, s, googleClaimsAt("elsewhere.example", "sub-x", "x@elsewhere.example")), "/login", url.Values{"code": {"not_invited"}})
	a.assertNoRow(ctx, t, s, "x@elsewhere.example")

	n := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, n, map[string]any{"sub": "sub-n", "email": "n@gmail.example", "email_verified": true}), "/login", url.Values{"code": {"not_workspace_member"}})
	a.assertNoRow(ctx, t, n, "n@gmail.example")
}
