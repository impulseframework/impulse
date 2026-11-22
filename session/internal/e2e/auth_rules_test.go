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
	"github.com/impulseframework/impulse/session/oidc"
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
// application's sign-up hook writes as the new user's membership row. password says the
// password method is registered beside the directories: a person who holds a password
// then confirms it before a directory identity is joined to their account, and with no
// password method nobody is asked for one. provider says another identity provider is
// registered beside them, behind the generic OpenID Connect method under the standard
// rule, invite only.
type rulesShape struct {
	tenants        azure.TenantSet
	entraAccounts  accounts
	entraRule      bool
	entraRoles     bool
	workspaces     google.WorkspaceSet
	googleAccounts accounts
	googleRule     bool
	defaultRole    string
	password       bool
	provider       bool
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
	// provider is the other identity provider's fake, at /provider, nil unless the
	// shape registers one.
	provider *oidctest.FakeIDP
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

func (r rolesNeverAsked) TenantsAdministered(context.Context, accesstypes.User) ([]accesstypes.Domain, error) {
	r.t.Error("the role store was asked which tenants a user administers, want the sign-in refused first")

	return nil, nil
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
		session.MailSettings{Sender: recorder, Application: "Lakeside", LoginURL: "https://app.example/login"},
		session.WithIdentityLinked(h.identityLinked), session.WithPendingHook(h.pendingHook))
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
	if shape.password {
		session.PasswordSignIn(auth)
	}

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

	var providerIDP *oidctest.FakeIDP
	var provider *oidc.Method
	if shape.provider {
		providerIDP = oidctest.NewFakeIDP(t, "provider-client")
		provider = oidc.SignIn[Tx](auth, "provider", nil, &oidc.Config{
			Issuer: providerIDP.Server.URL, ClientID: "provider-client", RedirectURL: "https://app.example/provider/callback",
			RoleSync: signin.DisableRoleSync(),
		})
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
		if provider != nil {
			r.Get("/provider/login", provider.Login())
			r.Get("/provider/callback", provider.Callback())
		}
	})
	server := httptest.NewTLSServer(r)
	t.Cleanup(server.Close)

	return &rulesApp[Tx]{
		authApp: &authApp[Tx]{db: db, rows: be.rows, server: server, api: auth.API(), hooks: h, mail: recorder, verified: store.VerifyUserEmail, setEmail: store.SetUserEmail},
		entra:   entraIDP, google: googleIDP, provider: providerIDP, store: store,
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

// entraSignIn runs an Entra round trip with claims in b and returns where the completed
// sign-in sent the browser. Entra does not vouch for an address, so a person whose
// account holds no confirmed one is held for a mailed code first: the helper answers it
// with the address in claims, as the person would. entraRoundTrip is the round trip
// alone, for the scenarios about what holds a sign-in.
func (a *rulesApp[Tx]) entraSignIn(ctx context.Context, b *browser, claims map[string]any) *url.URL {
	b.t.Helper()

	to := a.entraRoundTrip(ctx, b, claims)
	if to.Path != "/login" || to.Query().Get("pending") != "mfa" {
		return to
	}
	address, _ := claims["email"].(string)

	return a.confirmAddress(ctx, b, to, address)
}

// entraRoundTrip runs an Entra round trip with claims in b and returns where the
// callback sent the browser.
func (a *rulesApp[Tx]) entraRoundTrip(ctx context.Context, b *browser, claims map[string]any) *url.URL {
	b.t.Helper()

	return b.oidcSignIn(ctx, a.entra, "/entra", claims)
}

// entraPerson signs a person in through Entra once, in a browser of their own, and
// answers the code that confirms their address: the scenarios about a second factor
// start from a person whose address is confirmed, so what holds their next sign-in is
// the policy alone.
func (a *rulesApp[Tx]) entraPerson(ctx context.Context, t *testing.T, claims map[string]any) {
	t.Helper()

	first := a.browser(t)
	first.prime(ctx)
	address, _ := claims["email"].(string)
	a.confirmAddress(ctx, first, a.entraRoundTrip(ctx, first, claims), address)
}

// googleSignIn runs a Google round trip with claims in b and returns where the
// callback sent the browser.
func (a *rulesApp[Tx]) googleSignIn(ctx context.Context, b *browser, claims map[string]any) *url.URL {
	b.t.Helper()

	return b.oidcSignIn(ctx, a.google, "/google", claims)
}

// providerSignIn runs a round trip through the other identity provider with claims in b
// and returns where the callback sent the browser.
func (a *rulesApp[Tx]) providerSignIn(ctx context.Context, b *browser, claims map[string]any) *url.URL {
	b.t.Helper()

	return b.oidcSignIn(ctx, a.provider, "/provider", claims)
}

// createUser creates a user without a password and without an address: pre-created
// for an external identity whose method vouches for the sign-in name, or for a
// directory of the user's one tenant. Through any other method the first sign-in is
// linked on the code mailed to the address the account holds (createUserAt).
func (a *authApp[Tx]) createUser(ctx context.Context, t *testing.T, username string) impulse.UUID {
	t.Helper()

	id, err := a.api.CreateSessionUser(ctx, &session.CreateUserRequest{Username: username})
	if err != nil {
		t.Fatalf("CreateSessionUser(%q) error = %v", username, err)
	}

	return id
}

// createUserAt creates a user without a password who holds email, unconfirmed, as an
// administrator enters it: an account made ahead of its person's first sign-in through
// a directory, which waits for the code mailed to that address before it is linked on
// the sign-in name.
func (a *authApp[Tx]) createUserAt(ctx context.Context, t *testing.T, username, email string) impulse.UUID {
	t.Helper()

	id, err := a.api.CreateSessionUser(ctx, &session.CreateUserRequest{Username: username, Email: &email})
	if err != nil {
		t.Fatalf("CreateSessionUser(%q, %q) error = %v", username, email, err)
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
			name: "open to sign-up: a pre-created user with a password confirms with it before the link is made, matched without regard to case and keeping its spelling, and a pre-created user without a password or an identity is linked once the code mailed to the address the account holds is entered",
			run:  seamRulesMembersPreCreated[Tx],
		},
		{
			name: "a registration that synchronizes roles: a token asserting no application role is refused when the registration synchronizes roles, before any row is written or any role is read",
			run:  seamRulesMembersNoRoles[Tx],
		},
		{
			name: "invite only, a method with no row: a pre-created user is linked on the code mailed to the account's address, one with a password confirms, and a stranger is refused with not_invited and a guest with guest_account, both without a row",
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
			name: "personal accounts beside one tenant, invite only: a pre-created person signs in with a Microsoft personal account under the consumer tenant, on the code mailed to the account's address, and a work account of an unlisted tenant is refused without a row",
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
			name: "an account that cannot hold its address: a Gmail sign-in that reaches an account made ahead under the address as its sign-in name, while another account holds the address confirmed, is refused and links nothing; an identity linked before is refused the same once another account holds the address confirmed and its own holds none",
			run:  seamRulesAccountCannotHoldItsAddress[Tx],
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
		{
			name: "a Microsoft person's first sign-in is held for a mailed code whatever the token and the policy say: nothing is mailed before the person gives an address, no other factor answers the hold, the code confirms the address, and the next sign-in is not held; a Google person, whose address Google vouches for, is never held",
			run:  seamRulesAddressConfirmedFirst[Tx],
		},
		{
			name: "an invitation accepted through a directory that sends no address waits for the code mailed to the invited address: nobody is made before it, no address is typed, a code mailed for another invitation accepts nothing, and the acceptance makes the account with the invited address confirmed",
			run:  seamRulesInvitationByCode[Tx],
		},
		{
			name: "an invitation's code entered through a directory that sends no address, under the name of an account made ahead that holds another address, is refused with invitation_address: no session, nothing linked, the account's address as it was and the invitation standing",
			run:  seamRulesInvitationCodeUnderAnotherAccount[Tx],
		},
		{
			name: "the code mailed for an invitation accepts that invitation and proves nothing else: entered once the invitation was withdrawn, through a method open to sign-up, it signs nobody in and makes no account",
			run:  seamRulesInvitationCodeWithoutItsInvitation[Tx],
		},
		{
			name: "an invitation accepted through a directory that sends an address it did not verify waits for the code as one that sends none: a directory that says the invited address is its person's makes no account and spends no invitation, one that says another address is not refused for it, and the person who reads the invited mailbox accepts with the code",
			run:  seamRulesInvitationUnverifiedAddress[Tx],
		},
		{
			name: "a verified Google sign-in for an address another account holds unconfirmed, through a method open to sign-up, makes a second account beside the first: the first keeps the address, unconfirmed, and gets no link, and the second holds the Google identity and no address",
			run:  seamRulesVerifiedBesideUnconfirmed[Tx],
		},
		{
			name: "another identity provider that says it verified the address confirms nothing: the person's first sign-in to an account named by its address, which nobody has confirmed, waits for the mailed code, nothing is mailed before they ask, the code confirms the address, and the next sign-in is the provider's alone",
			run:  seamRulesProviderConfirmsNothing[Tx],
		},
		{
			name: "another identity provider's word for an address reaches nobody's account: a person it says holds an address another account holds confirmed is a stranger, refused without a row or a link",
			run:  seamRulesProviderReachesNoAccount[Tx],
		},
		{
			name: "a sign-in held while every provider's word for an address was kept keeps none when it is finished: the row says the provider verified the address, the application's own factor answers the step, and the sign-in waits for the mailed code with no session and no address written; the code confirms the address the person gives",
			run:  seamRulesHeldSignInKeepsNoProvidersWord[Tx],
		},
		{
			name: "Google vouches for an address only where it is the address's authority: a Gmail address and a Workspace account's are confirmed by the sign-in with nothing mailed, and the first sign-in of a Google account with an outside address and no hd waits for the mailed code",
			run:  seamRulesGoogleVouchesForItsOwn[Tx],
		},
		{
			name: "a first link on a sign-in name the method does not vouch for waits for the account's code: a Google account made with an outside address, under the name of an account that holds the address confirmed, starts no session and gains no link, the step takes no other address, and the person who reads the code at the account's address is linked and signed in",
			run:  seamRulesNameLinkWaitsForCode[Tx],
		},
		{
			name: "a first link that waits leaves nothing behind: the sign-in that never finishes writes no link, the account's own person signs in through their directory on the code mailed to them, and the earlier identity is refused",
			run:  seamRulesNameLinkLeavesNothing[Tx],
		},
		{
			name: "an account that holds no address is linked on no name: the sign-in is refused with identity_rejected, nothing waits, nothing is mailed and no address is taken",
			run:  seamRulesNameLinkNeedsAddress[Tx],
		},
		{
			name: "an account whose address is taken away while a first link waits takes no address from the sign-in that waits: the step refuses the address given, nothing is mailed, and nobody is linked or signed in",
			run:  seamRulesNameLinkTakesNoAddress[Tx],
		},
		{
			name: "a code mailed before the account's address is taken away is no longer good: the step offers no code at the address the account held, the code is answered code_expired, nobody is linked or signed in, and the address is not written back",
			run:  seamRulesNameLinkCodeOutlivesNoAddress[Tx],
		},
		{
			name: "a sign-in name that moves to another account while a first link waits links nobody: the code mailed to the first account's address is refused at the completion, and neither account gains a link or a session",
			run:  seamRulesNameLinkAfterTheNameMoved[Tx],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.run(t.Context(), t, be)
		})
	}
}

// nameLinkShape is the shape of the scenarios about a first link made on a sign-in name
// alone: the tenant and any Google account, a Workspace's or not, each invite only, and
// no password method, so no password stands between a sign-in and the account under
// its name.
func nameLinkShape() *rulesShape {
	return &rulesShape{
		tenants: azure.Tenant(azureTenant), entraAccounts: inviteOnly,
		workspaces: google.AnyGoogleAccount(), googleAccounts: inviteOnly,
	}
}

// outsideGoogleClaims are the ID token claims of a Google account made with an address
// outside Google: no hd, and an email_verified that says Google mailed the address
// once, to whoever made the account.
func outsideGoogleClaims(sub, email string) map[string]any {
	return map[string]any{"sub": sub, "email": email, "email_verified": true}
}

// seamRulesNameLinkWaitsForCode is the scenario "a first link on a sign-in name the
// method does not vouch for waits for the account's code".
func seamRulesNameLinkWaitsForCode[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, nameLinkShape())
	alice := a.createUser(ctx, t, "alice@corp.example")
	a.confirmedAddress(ctx, t, alice, "alice@corp.example")

	// Anyone can make a Google account under an address outside Google, and Google
	// names the account by it. The name finds alice's account, which has no password
	// and no identity, and nothing but the name ties the two: the sign-in waits for
	// the code mailed to the address the account holds, with no session, no link, and
	// nothing mailed until the code is asked for.
	b := a.browser(t)
	b.prime(ctx)
	claims := outsideGoogleClaims("g-sub-other", "alice@corp.example")
	assertRedirect(t, a.googleSignIn(ctx, b, claims), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if links := a.links(ctx, t, alice.String()); len(links) != 0 {
		t.Fatalf("identity links while the sign-in waits = %d, want none: the link is written once the code is entered", len(links))
	}
	if sent := a.mail.Messages(); len(sent) != 0 {
		t.Fatalf("mail before the code was asked for = %d messages, want none", len(sent))
	}

	// The step offers the account's address, masked, and takes no other; the
	// application's own factor does not answer for the code.
	if status := b.pendingStatus(ctx); status.EmailCode == nil || status.EmailCode.EnrollEmail || status.EmailCode.CodeEmail != "a•••@corp.example" {
		t.Errorf("the step's email code = %+v, want the code offered at the account's address and no address asked for", status.EmailCode)
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/email", map[string]string{"email": "mallory@elsewhere.example"}); status == http.StatusOK {
		t.Errorf("giving the step another address = %d %s, want it refused: the code goes to the address the account holds", status, body)
	}
	if status, body := b.do(ctx, http.MethodPost, "/mfa", nil); status == http.StatusNoContent || status == http.StatusAccepted {
		t.Errorf("the application's own factor on a sign-in that waits for the account's code = %d %s, want it refused", status, body)
	}
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)

	// The person who reads the account's mail enters the code: the link is written,
	// and the session is the account's.
	a.answerAddressCode(ctx, b, "alice@corp.example")
	if v := signedInAs(ctx, t, b, "alice@corp.example"); v.UserID != alice.String() {
		t.Errorf("session user = %s, want the account under the name, %s", v.UserID, alice)
	}
	if links := a.links(ctx, t, alice.String()); len(links) != 1 || links[0].Subject != "g-sub-other" {
		t.Errorf("identity links after the code = %d, want the one the code admitted", len(links))
	}

	// The identity is linked: the person's next sign-in is Google's alone.
	next := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, next, claims), "/home", url.Values{})
	signedInAs(ctx, t, next, "alice@corp.example")
}

// seamRulesNameLinkTakesNoAddress is the scenario "an account whose address is taken
// away while a first link waits takes no address from the sign-in that waits".
func seamRulesNameLinkTakesNoAddress[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, nameLinkShape())
	alice := a.createUser(ctx, t, "alice@corp.example")
	a.confirmedAddress(ctx, t, alice, "alice@corp.example")

	// A Google account made under alice's name, with an address outside Google, waits
	// for the code mailed to the address alice's account holds.
	b := a.browser(t)
	b.prime(ctx)
	assertRedirect(t, a.googleSignIn(ctx, b, outsideGoogleClaims("g-sub-other", "alice@corp.example")), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})

	// An administrator takes the address off the account while the sign-in waits. The
	// step has no address to mail the code to, and takes none from the person who
	// waits: nothing but the name ties them to the account.
	if err := a.setEmail(ctx, alice, nil); err != nil {
		t.Fatalf("SetUserEmail() error = %v", err)
	}
	if status, _ := b.do(ctx, http.MethodPost, "/pending/email", map[string]string{"email": "mallory@elsewhere.example"}); status != http.StatusConflict {
		t.Fatalf("giving the step an address = %d, want 409: the sign-in is not yet the account's", status)
	}
	if sent := a.mail.Messages(); len(sent) != 0 {
		t.Errorf("mail after the address was refused = %d messages, want none", len(sent))
	}
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if links := a.links(ctx, t, alice.String()); len(links) != 0 {
		t.Errorf("identity links of the account = %d, want none", len(links))
	}
	if _, err := a.api.UserByEmail(ctx, "mallory@elsewhere.example"); !httpio.HasNotFound(err) {
		t.Errorf("UserByEmail() error = %v, want NotFound: the address given is written on no account", err)
	}
}

// seamRulesNameLinkCodeOutlivesNoAddress is the scenario "a code mailed before the
// account's address is taken away is no longer good".
func seamRulesNameLinkCodeOutlivesNoAddress[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, nameLinkShape())
	alice := a.createUser(ctx, t, "alice@corp.example")
	a.confirmedAddress(ctx, t, alice, "alice@corp.example")

	// A Google account made under alice's name, with an address outside Google, waits
	// for the code mailed to the address alice's account holds. The person asks for
	// the code, and it is mailed there.
	b := a.browser(t)
	b.prime(ctx)
	assertRedirect(t, a.googleSignIn(ctx, b, outsideGoogleClaims("g-sub-other", "alice@corp.example")), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	code := a.lastCode(t, "alice@corp.example")
	if status := b.pendingStatus(ctx); status.EmailCode == nil || !status.EmailCode.CodeSent {
		t.Fatalf("the step's email code after the code was mailed = %+v, want the code standing", status.EmailCode)
	}

	// An administrator learns the mailbox is no longer alice's and takes the address
	// off the account. The code was mailed to an address the account no longer holds:
	// the step offers it no more, and entering it is answered as a code that expired.
	if err := a.setEmail(ctx, alice, nil); err != nil {
		t.Fatalf("SetUserEmail() error = %v", err)
	}
	if status := b.pendingStatus(ctx); status.EmailCode == nil || status.EmailCode.CodeSent || status.EmailCode.CodeEmail != "" {
		t.Errorf("the step's email code after the address was taken off the account = %+v, want no code standing and no address offered", status.EmailCode)
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"code_expired"`) {
		t.Fatalf("the code mailed before the address was taken off the account = %d %s, want 401 code_expired", status, body)
	}

	// Nobody is signed in or linked, and the address the administrator took away is
	// not written back.
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if links := a.links(ctx, t, alice.String()); len(links) != 0 {
		t.Errorf("identity links of the account = %d, want none", len(links))
	}
	if _, err := a.api.UserByEmail(ctx, "alice@corp.example"); !httpio.HasNotFound(err) {
		t.Errorf("UserByEmail() error = %v, want NotFound: the address the administrator took away is on no account", err)
	}
}

// seamRulesNameLinkAfterTheNameMoved is the scenario "a sign-in name that moves to
// another account while a first link waits links nobody".
func seamRulesNameLinkAfterTheNameMoved[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, nameLinkShape())
	dana := a.createUser(ctx, t, "dana@corp.example")
	a.confirmedAddress(ctx, t, dana, "dana@corp.example")

	// The sign-in waits for the code mailed to the address dana's account holds, and
	// the person asks for it.
	b := a.browser(t)
	b.prime(ctx)
	assertRedirect(t, a.googleSignIn(ctx, b, outsideGoogleClaims("g-sub-moved", "dana@corp.example")), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)

	// While it waits, dana's account is renamed and another account takes the name.
	if err := a.api.ChangeSessionUserUsername(ctx, dana, "dana.old@corp.example"); err != nil {
		t.Fatalf("ChangeSessionUserUsername() error = %v", err)
	}
	bob := a.createUser(ctx, t, "dana@corp.example")
	a.confirmedAddress(ctx, t, bob, "bob@corp.example")

	// The code answers for dana's address. The name finds bob's account now, whose
	// address nobody answered for: the completion is refused, and the identity is
	// linked to neither account.
	if status, _ := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": a.lastCode(t, "dana@corp.example")}); status == http.StatusOK {
		t.Fatalf("the code's verification after the name moved = %d, want it refused", status)
	}
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if links := a.links(ctx, t, bob.String()); len(links) != 0 {
		t.Errorf("identity links of the account that took the name = %d, want none: nobody answered for its address", len(links))
	}
	if links := a.links(ctx, t, dana.String()); len(links) != 0 {
		t.Errorf("identity links of the account the wait was for = %d, want none: the name is no longer its own", len(links))
	}
}

// seamRulesNameLinkLeavesNothing is the scenario "a first link that waits leaves nothing
// behind".
func seamRulesNameLinkLeavesNothing[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, nameLinkShape())
	alice := a.createUserAt(ctx, t, "alice@corp.example", "alice@corp.example")

	// An administrator made alice's account ahead of her, with her address, which
	// nobody has confirmed. Somebody else's Google account under that address waits for
	// the code, which goes to her address and which they never read: the sign-in never
	// finishes, and it leaves no link on her account.
	b := a.browser(t)
	b.prime(ctx)
	other := outsideGoogleClaims("g-sub-other", "alice@corp.example")
	assertRedirect(t, a.googleSignIn(ctx, b, other), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if links := a.links(ctx, t, alice.String()); len(links) != 0 {
		t.Fatalf("identity links after a held sign-in that never finished = %d, want none", len(links))
	}

	// Alice's own first sign-in, through her organization's directory, waits for the
	// code too, and she reads it: her identity is the account's one link, and the code
	// confirmed her address.
	own := a.browser(t)
	own.prime(ctx)
	assertRedirect(t, a.entraSignIn(ctx, own, azureClaims(azureTenant, "oid-alice", "alice@corp.example")), "/home", url.Values{})
	if v := signedInAs(ctx, t, own, "alice@corp.example"); v.UserID != alice.String() {
		t.Errorf("session user = %s, want alice's account %s", v.UserID, alice)
	}
	assertOneLink(t, a.links(ctx, t, alice.String()), azureTenant, azureTenant)
	if email, verified := own.authenticatedEmail(ctx); email != "alice@corp.example" || !verified {
		t.Errorf("alice's address = %q (confirmed %v), want alice@corp.example confirmed by the code", email, verified)
	}

	// The account signs in through her directory now: the other identity, of another
	// connection, is refused, with her address confirmed as it was without.
	again := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, again, other), "/login", url.Values{"code": {"identity_rejected"}})
	again.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	assertOneLink(t, a.links(ctx, t, alice.String()), azureTenant, azureTenant)
}

// seamRulesNameLinkNeedsAddress is the scenario "an account that holds no address is
// linked on no name".
func seamRulesNameLinkNeedsAddress[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, nameLinkShape())
	alice := a.createUser(ctx, t, "alice@corp.example")

	// The account's name reads as an address, and the account holds none: there is
	// nowhere to mail a code that would show the person reads the account's mail, and a
	// code mailed to an address typed at the sign-in would show only that they read
	// their own. The sign-in is refused, and nothing waits for an address.
	b := a.browser(t)
	b.prime(ctx)
	assertRedirect(t, a.googleSignIn(ctx, b, outsideGoogleClaims("g-sub-other", "alice@corp.example")), "/login", url.Values{"code": {"identity_rejected"}})
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if status, body := b.do(ctx, http.MethodPost, "/pending/email", map[string]string{"email": "mallory@elsewhere.example"}); status == http.StatusOK {
		t.Errorf("giving an address after the refusal = %d %s, want it refused: no sign-in waits", status, body)
	}
	if links := a.links(ctx, t, alice.String()); len(links) != 0 {
		t.Errorf("identity links after the refusal = %d, want none", len(links))
	}
	if sent := a.mail.Messages(); len(sent) != 0 {
		t.Errorf("mail after the refusal = %d messages, want none", len(sent))
	}
}

// seamRulesAddressConfirmedFirst is the scenario "a Microsoft person's first sign-in is
// held for a mailed code".
func seamRulesAddressConfirmedFirst[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(openSignUp))

	// The token asserts mfa and the policy asks for nothing: the address alone holds
	// the sign-in. The account is there, without an address, and nothing was mailed.
	b := a.browser(t)
	b.prime(ctx)
	claims := azureClaims(azureTenant, "oid-pat", "pat@lakeside.edu")
	assertRedirect(t, a.entraRoundTrip(ctx, b, claims), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if sent := a.mail.Messages(); len(sent) != 0 {
		t.Fatalf("mail before the person gave an address = %+v, want none: the directory's word for an address is not a confirmation", sent)
	}

	// The application's own factor does not answer it: the code is the one factor.
	if status, body := b.do(ctx, http.MethodPost, "/mfa", nil); status == http.StatusNoContent || status == http.StatusAccepted {
		t.Errorf("the application's own factor on a sign-in held for the address = %d %s, want it refused", status, body)
	}
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)

	a.answerAddressCode(ctx, b, "pat@lakeside.edu")
	v := signedInAs(ctx, t, b, "pat@lakeside.edu")
	assertEventsSeen(t, v, "azure:"+azureTenant, "attested-by-evidence:"+azureTenant, "email-code")
	if email, verified := b.authenticatedEmail(ctx); email != "pat@lakeside.edu" || !verified {
		t.Errorf("the account's address = %q (confirmed %v), want pat@lakeside.edu confirmed by the code", email, verified)
	}

	// The address is confirmed: the person's next sign-in is the directory's alone.
	next := a.browser(t)
	assertRedirect(t, a.entraRoundTrip(ctx, next, claims), "/home", url.Values{})
	assertEventsSeen(t, signedInAs(ctx, t, next, "pat@lakeside.edu"), "azure:"+azureTenant, "attested-by-evidence:"+azureTenant)

	// Google vouches for the address it gives: the account is made with it confirmed,
	// and the first sign-in is not held.
	g := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, g, googleClaims("g-sub-sam", "sam@lakeside.edu")), "/home", url.Values{})
	if email, verified := g.authenticatedEmail(ctx); email != "sam@lakeside.edu" || !verified {
		t.Errorf("the Google person's address = %q (confirmed %v), want sam@lakeside.edu confirmed at sign-up", email, verified)
	}
}

// seamRulesInvitationByCode is the scenario "an invitation accepted through a directory
// that sends no address waits for the code mailed to the invited address". Microsoft
// Entra ID sends a person's address only where the registration is set up to, so such a
// sign-in has nothing to compare with the invited address: the mailed code is the
// proof that the person reads the invited mailbox.
func seamRulesInvitationByCode[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(inviteOnly))
	const (
		invited  = "nell@north.example"
		signedAs = "nell@lakeside.edu"
		another  = "omar@north.example"
	)
	if _, err := a.api.Invite(ctx, &session.InvitationRequest{Email: invited, Name: "Nell"}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	token := invitationToken(t, a.mail)
	b := a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": token}, b.xsrfToken())

	// Microsoft presents her under her organization's name and sends no address. The
	// sign-in is held: nobody is made, nothing is mailed until the page asks, and the
	// invitation stands.
	claims := azureClaims(azureTenant, "oid-nell", signedAs)
	delete(claims, "email")
	mailed := len(a.mail.Messages())
	assertRedirect(t, a.entraRoundTrip(ctx, b, claims), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	a.assertNoRow(ctx, t, b, signedAs)
	if n := len(a.mail.Messages()) - mailed; n != 0 {
		t.Errorf("%d message(s) mailed by the held sign-in, want none before the page asks for the code", n)
	}
	if standing := a.standingInvitations(ctx, t); len(standing) != 1 {
		t.Fatalf("standing invitations while the sign-in waits = %d, want the one invitation", len(standing))
	}

	// The page is told where the code goes, masked, and is asked for no address; one
	// typed anyway is refused, since the code goes to the invited address alone.
	status := string(b.expect(ctx, http.StatusOK, http.MethodGet, "/pending", nil))
	if !strings.Contains(status, `"factors":["email-code"]`) || !strings.Contains(status, `"codeEmail":"n•••@north.example"`) || strings.Contains(status, `"enrollEmail":true`) {
		t.Errorf("pending status = %s, want the email code alone, going to the invited address, with no address asked for", status)
	}
	if code, body := b.do(ctx, http.MethodPost, "/pending/email", map[string]string{"email": "mallory@elsewhere.example"}); code != http.StatusConflict || !strings.Contains(string(body), "goes to the invited address") {
		t.Errorf("an address typed for the code = %d %s, want 409 saying the code goes to the invited address", code, body)
	}
	if n := len(a.mail.Messages()) - mailed; n != 0 {
		t.Errorf("%d message(s) mailed for the address typed, want none", n)
	}

	// A code mailed for this invitation accepts no other: with another invitation
	// opened in the browser, her code proves nothing about its address.
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	code := a.lastCode(t, invited)
	if _, err := a.api.Invite(ctx, &session.InvitationRequest{Email: another}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": lastInvitationToken(t, a.mail)}, b.xsrfToken())
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); status == http.StatusOK && strings.Contains(string(body), `"mfaIsRequired":false`) {
		t.Fatalf("her code with another address's invitation in the browser = %d %s, want no sign-in", status, body)
	}
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	a.assertNoRow(ctx, t, b, signedAs)
	if standing := a.standingInvitations(ctx, t); len(standing) != 2 {
		t.Fatalf("standing invitations after the code met another invitation = %d, want both still standing", len(standing))
	}

	// With her own invitation in the browser again, a fresh sign-in and its code accept
	// it: the account is made with the invited address confirmed and the invitation's
	// name, and she is signed in.
	b = a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": token}, b.xsrfToken())
	assertRedirect(t, a.entraRoundTrip(ctx, b, claims), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": a.lastCode(t, invited)}); !strings.Contains(string(body), `"mfaIsRequired":false`) {
		t.Fatalf("code verification = %s, want the sign-in completed", body)
	}
	v := signedInAs(ctx, t, b, signedAs)
	assertEventsSeen(t, v, "azure:"+azureTenant, "attested-by-evidence:"+azureTenant, "email-code")
	if email, verified := b.authenticatedEmail(ctx); email != invited || !verified {
		t.Errorf("the new account's address = %q (confirmed %v), want the invited address confirmed by the code", email, verified)
	}
	if got := b.authenticatedName(ctx); got != "Nell" {
		t.Errorf("the new account's name = %q, want the invitation's", got)
	}
	if standing := a.standingInvitations(ctx, t); len(standing) != 1 || standing[0].Email != another {
		t.Errorf("standing invitations after the acceptance = %+v, want the other address's alone", standing)
	}

	// Her next sign-in is the directory's alone: the account holds a confirmed address.
	next := a.browser(t)
	assertRedirect(t, a.entraRoundTrip(ctx, next, claims), "/home", url.Values{})
}

// seamRulesInvitationCodeUnderAnotherAccount: vera's account was made ahead under
// vera@lakeside.edu and holds that address, unconfirmed. An invitation is mailed to
// nell@north.example, and somebody who holds it signs in through Microsoft under vera's
// name, which sends no address. The code mailed to the invited address shows only that
// the person reads that mailbox: the invitation is not vera's account's own, so the code
// links nothing to it.
func seamRulesInvitationCodeUnderAnotherAccount[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(inviteOnly))
	const (
		invited = "nell@north.example"
		name    = "vera@lakeside.edu"
	)
	vera := a.createUserAt(ctx, t, name, name)
	if _, err := a.api.Invite(ctx, &session.InvitationRequest{Email: invited, Name: "Nell"}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	b := a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": invitationToken(t, a.mail)}, b.xsrfToken())

	// The sign-in is held for the code mailed to the invited address.
	claims := azureClaims(azureTenant, "oid-vera", name)
	delete(claims, "email")
	assertRedirect(t, a.entraRoundTrip(ctx, b, claims), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)

	// The right code is refused as an acceptance with another address is.
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": a.lastCode(t, invited)}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"invitation_address"`) {
		t.Errorf("the invitation's code under vera's name = %d %s, want 401 invitation_address", status, body)
	}
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if links := a.links(ctx, t, vera.String()); len(links) != 0 {
		t.Errorf("identity links of vera's account = %d, want none", len(links))
	}
	address := name
	a.holds(ctx, t, vera, &address)
	if standing := a.standingInvitations(ctx, t); len(standing) != 1 || standing[0].Email != invited {
		t.Errorf("standing invitations after the refusal = %+v, want the invitation to %s still standing", standing, invited)
	}
}

// seamRulesInvitationCodeWithoutItsInvitation is the scenario "the code mailed for an
// invitation accepts that invitation and proves nothing else". The method is open to
// sign-up, so the person would be given an account with no invitation at all, and
// would then be held until they confirmed an address of their own. The code they hold
// was mailed for the invitation: once the invitation is withdrawn it is the proof of
// no sign-in, so it makes no account and stands for no confirmed address.
func seamRulesInvitationCodeWithoutItsInvitation[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(openSignUp))
	const (
		invited  = "pia@north.example"
		signedAs = "pia@lakeside.edu"
	)
	if _, err := a.api.Invite(ctx, &session.InvitationRequest{Email: invited}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	b := a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": invitationToken(t, a.mail)}, b.xsrfToken())
	claims := azureClaims(azureTenant, "oid-pia", signedAs)
	delete(claims, "email")
	assertRedirect(t, a.entraRoundTrip(ctx, b, claims), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	code := a.lastCode(t, invited)

	standing := a.standingInvitations(ctx, t)
	if len(standing) != 1 {
		t.Fatalf("standing invitations = %d, want the one", len(standing))
	}
	if err := a.api.RevokeInvitation(ctx, standing[0].ID); err != nil {
		t.Fatalf("RevokeInvitation() error = %v", err)
	}
	if status, body := b.do(ctx, http.MethodPost, "/pending/code/verify", map[string]any{"code": code}); status != http.StatusUnauthorized || !strings.Contains(string(body), `"invitation_expired"`) {
		t.Errorf("the code entered once its invitation was withdrawn = %d %s, want 401 with invitation_expired", status, body)
	}
	a.assertNoRow(ctx, t, b, signedAs)
}

// seamRulesInvitationUnverifiedAddress is the scenario "an invitation accepted through a
// directory that sends an address it did not verify waits for the code". Where the
// registration is set up to send one, Microsoft Entra ID sends the address the
// directory's administrator wrote for the person and verifies none of it, and anybody
// can run a directory. Somebody who holds an invitation's link and whose directory
// says the invited address is theirs would otherwise be given what the invitation
// carries: the account under the invited address, and its roles. So the address the
// token carries is read as none, whichever it is, and the mailed code decides.
func seamRulesInvitationUnverifiedAddress[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(inviteOnly))
	const (
		invited  = "nell@north.example"
		stranger = "mallory@lakeside.edu"
		signedAs = "nell@lakeside.edu"
	)
	if _, err := a.api.Invite(ctx, &session.InvitationRequest{Email: invited, Name: "Nell"}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	token := invitationToken(t, a.mail)
	mailed := len(a.mail.Messages())

	// Somebody else holds the link. Their directory says the invited address is theirs,
	// and then, in another browser, that another one is. Either way the sign-in is held
	// for the code, which goes to the invited mailbox: nobody is made, nothing is mailed
	// before the page asks, and the invitation stands.
	for _, claimed := range []string{invited, "mallory@elsewhere.example"} {
		b := a.browser(t)
		b.prime(ctx)
		b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": token}, b.xsrfToken())
		claims := azureClaims(azureTenant, "oid-mallory", stranger)
		claims["email"] = claimed
		assertRedirect(t, a.entraRoundTrip(ctx, b, claims), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
		b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
		a.assertNoRow(ctx, t, b, stranger)
		status := string(b.expect(ctx, http.StatusOK, http.MethodGet, "/pending", nil))
		if !strings.Contains(status, `"factors":["email-code"]`) || !strings.Contains(status, `"codeEmail":"n•••@north.example"`) || strings.Contains(status, `"enrollEmail":true`) {
			t.Errorf("pending status of a directory that says %s = %s, want the email code alone, going to the invited address, with no address asked for", claimed, status)
		}
		if standing := a.standingInvitations(ctx, t); len(standing) != 1 {
			t.Fatalf("standing invitations after a directory said %s = %d, want the one invitation", claimed, len(standing))
		}
	}
	if n := len(a.mail.Messages()) - mailed; n != 0 {
		t.Errorf("%d message(s) mailed by the held sign-ins, want none before a page asks for the code", n)
	}

	// The person who reads the invited mailbox accepts with the code, though her
	// directory says an address of its own for her: the account is made with the invited
	// address confirmed, and the invitation is spent.
	b := a.browser(t)
	b.prime(ctx)
	b.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": token}, b.xsrfToken())
	claims := azureClaims(azureTenant, "oid-nell", signedAs)
	assertRedirect(t, a.entraRoundTrip(ctx, b, claims), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
	b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/send", nil)
	if body := b.expect(ctx, http.StatusOK, http.MethodPost, "/pending/code/verify", map[string]any{"code": a.lastCode(t, invited)}); !strings.Contains(string(body), `"mfaIsRequired":false`) {
		t.Fatalf("code verification = %s, want the sign-in completed", body)
	}
	signedInAs(ctx, t, b, signedAs)
	if email, verified := b.authenticatedEmail(ctx); email != invited || !verified {
		t.Errorf("the new account's address = %q (confirmed %v), want the invited address confirmed by the code", email, verified)
	}
	if standing := a.standingInvitations(ctx, t); len(standing) != 0 {
		t.Errorf("standing invitations after the acceptance = %+v, want none", standing)
	}
	if a.db.hasUser(ctx, t, stranger) {
		t.Errorf("a user exists under %q, want none: the directory's word for the invited address made no account", stranger)
	}
}

// seamRulesProviderConfirmsNothing is the scenario "another identity provider that says
// it verified the address confirms nothing". The account is named by its address, and
// the provider signs the person in under that name: were the provider's word for the
// address kept, the name would be a vouched one, the link would be made at once and
// the sign-in would not be held. So the wait this scenario sees stands on the rule
// that no provider but Google vouches for an address, and on no other. The library
// drops the word twice, and the scenario fails only with both gone: the generic method
// fills none in on the identity it verifies, and the core takes it off whatever a
// method fills in.
func seamRulesProviderConfirmsNothing[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	shape := oneOrganization(inviteOnly)
	shape.provider = true
	a := newRulesApp(ctx, t, be, shape)
	cadet := a.createUserAt(ctx, t, "cadet@lakeside.edu", "cadet@lakeside.edu")

	// The token says the address is verified (email_verified), and nobody has confirmed
	// the address the account was created with: the sign-in waits for the code mailed
	// there, and nothing was mailed before the person asks.
	b := a.browser(t)
	b.prime(ctx)
	claims := gatewayClaims("p-cadet", "cadet@lakeside.edu", "cadet@lakeside.edu")
	held := a.providerSignIn(ctx, b, claims)
	if sent := a.mail.Messages(); len(sent) != 0 {
		t.Fatalf("mail after the provider's sign-in = %d messages, want none: the provider's word for an address is not a confirmation", len(sent))
	}
	assertRedirect(t, a.confirmAddress(ctx, b, held, "cadet@lakeside.edu"), "/home", url.Values{})
	if v := signedInAs(ctx, t, b, "cadet@lakeside.edu"); v.UserID != cadet.String() {
		t.Errorf("session user = %s, want the pre-created cadet %s", v.UserID, cadet)
	}
	if email, verified := b.authenticatedEmail(ctx); email != "cadet@lakeside.edu" || !verified {
		t.Errorf("the person's address = %q (confirmed %v), want cadet@lakeside.edu confirmed by the code", email, verified)
	}

	// The account holds a confirmed address now: the next sign-in is the provider's
	// alone.
	next := a.browser(t)
	assertRedirect(t, a.providerSignIn(ctx, next, claims), "/home", url.Values{})
	signedInAs(ctx, t, next, "cadet@lakeside.edu")
}

// seamRulesHeldSignInKeepsNoProvidersWord is the scenario "a sign-in held while every
// provider's word for an address was kept keeps none when it is finished".
func seamRulesHeldSignInKeepsNoProvidersWord[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(openSignUp))

	// A person's first sign-in through a directory: the account is made and linked,
	// with no address, and the sign-in waits.
	b := a.browser(t)
	b.prime(ctx)
	claims := azureClaims(azureTenant, "oid-pat", "pat@lakeside.edu")
	assertRedirect(t, a.entraRoundTrip(ctx, b, claims), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})

	// The row as a release that kept every provider's word stored one. The provider
	// said the address is verified, so the row says so, and that release held the
	// sign-in for the application's own second factor, not for the address.
	a.db.heldWithProvidersWord(ctx, t)

	// The person passes the application's factor under the release that keeps no
	// provider's word. The row's word is not kept: the sign-in waits again, for the
	// mailed code, and the provider's word has started no session and written its
	// address on no account.
	b.expect(ctx, http.StatusAccepted, http.MethodPost, "/mfa", nil)
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if _, err := a.api.UserByEmail(ctx, "pat@lakeside.edu"); !httpio.HasNotFound(err) {
		t.Fatalf("UserByEmail() error = %v, want NotFound: the provider's word writes its address on no account", err)
	}
	if sent := a.mail.Messages(); len(sent) != 0 {
		t.Errorf("mail before the person gave an address = %d messages, want none", len(sent))
	}

	// The code confirms the address the person gives, and the sign-in is theirs.
	a.answerAddressCode(ctx, b, "pat@lakeside.edu")
	signedInAs(ctx, t, b, "pat@lakeside.edu")
	if email, verified := b.authenticatedEmail(ctx); email != "pat@lakeside.edu" || !verified {
		t.Errorf("the account's address = %q (confirmed %v), want pat@lakeside.edu confirmed by the code", email, verified)
	}
}

// seamRulesProviderReachesNoAccount is the scenario "another identity provider's word
// for an address reaches nobody's account".
func seamRulesProviderReachesNoAccount[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	shape := oneOrganization(inviteOnly)
	shape.provider = true
	a := newRulesApp(ctx, t, be, shape)
	pat := a.createUser(ctx, t, "pat")
	a.confirmedAddress(ctx, t, pat, "pat@lakeside.edu")

	// The provider's administrators can mark any address verified. The person it names
	// by pat's address, under a name no account has, is nobody the application knows:
	// the invite-only method refuses them, and pat's account gains no link.
	b := a.browser(t)
	assertRedirect(t, a.providerSignIn(ctx, b, gatewayClaims("p-mallory", "mallory", "pat@lakeside.edu")), "/login", url.Values{"code": {"not_invited"}})
	a.assertNoRow(ctx, t, b, "mallory")
	if links := a.links(ctx, t, pat.String()); len(links) != 0 {
		t.Errorf("identity links of the person who holds the address = %d, want none: the provider's word for the address joins nobody to the account", len(links))
	}
}

// seamRulesGoogleVouchesForItsOwn is the scenario "Google vouches for an address only
// where it is the address's authority".
func seamRulesGoogleVouchesForItsOwn[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, &rulesShape{
		tenants: azure.Tenant(azureTenant), entraAccounts: inviteOnly,
		workspaces: google.AnyGoogleAccount(), googleAccounts: openSignUp,
	})

	// Google is the authority for a Gmail address and for a Workspace account's (the
	// token carries hd), and its word confirms those at the sign-in: the account is
	// made with the address confirmed and nothing is mailed.
	gmail := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, gmail, map[string]any{"sub": "g-sub-sam", "email": "sam@gmail.com", "email_verified": true}), "/home", url.Values{})
	if email, verified := gmail.authenticatedEmail(ctx); email != "sam@gmail.com" || !verified {
		t.Errorf("the Gmail person's address = %q (confirmed %v), want sam@gmail.com confirmed by Google's word", email, verified)
	}
	workspace := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, workspace, googleClaimsAt("hilltop.edu", "g-sub-ann", "ann@hilltop.edu")), "/home", url.Values{})
	if email, verified := workspace.authenticatedEmail(ctx); email != "ann@hilltop.edu" || !verified {
		t.Errorf("the Workspace person's address = %q (confirmed %v), want ann@hilltop.edu confirmed by Google's word", email, verified)
	}
	if sent := a.mail.Messages(); len(sent) != 0 {
		t.Errorf("mail after the Gmail and the Workspace sign-in = %d messages, want none", len(sent))
	}

	// Any other Google account's address is one somebody else answers for (a Google
	// account made with an outside address: the token carries no hd). Google's word
	// for it confirms nothing, so the first sign-in waits for the code, as through any
	// other provider, and the code confirms the address.
	outside := a.browser(t)
	outside.prime(ctx)
	held := a.googleSignIn(ctx, outside, map[string]any{"sub": "g-sub-lee", "email": "lee@outside.example", "email_verified": true})
	assertRedirect(t, a.confirmAddress(ctx, outside, held, "lee@outside.example"), "/home", url.Values{})
	signedInAs(ctx, t, outside, "lee@outside.example")
	if email, verified := outside.authenticatedEmail(ctx); email != "lee@outside.example" || !verified {
		t.Errorf("the outside Google person's address = %q (confirmed %v), want lee@outside.example confirmed by the code", email, verified)
	}
}

// seamRulesMembersProvision is the scenario "open to sign-up: a stranger is provisioned".
func seamRulesMembersProvision[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(openSignUp))

	b := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, b, azureClaims(azureTenant, "oid-pat", "p.lee@lakeside.edu")), "/home", url.Values{})
	v := signedInAs(ctx, t, b, "p.lee@lakeside.edu")
	// The fake Entra asserts mfa in every token: the session records the evidence
	// after the sign-in's own event, and then the code that confirmed the address of
	// a person Entra does not vouch for the address of.
	assertEventsSeen(t, v, "azure:"+azureTenant, "attested-by-evidence:"+azureTenant, "email-code")
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
	shape := oneOrganization(openSignUp)
	shape.password = true
	a := newRulesApp(ctx, t, be, shape)
	pat := a.seed(ctx, t, "pat@lakeside.edu")
	a.confirmedAddress(ctx, t, pat, "pat@lakeside.edu")
	kim := a.createUserAt(ctx, t, "kim@lakeside.edu", "kim@lakeside.edu")

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
	shape := oneOrganization(inviteOnly)
	shape.password = true
	a := newRulesApp(ctx, t, be, shape)
	kim := a.createUserAt(ctx, t, "kim@lakeside.edu", "kim@lakeside.edu")
	pat := a.seed(ctx, t, "pat@lakeside.edu")
	a.confirmedAddress(ctx, t, pat, "pat@lakeside.edu")

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
	// The invitation was used: its token no longer opens it, and the browser that
	// carried it, signed in to the account it seated, has nothing to accept.
	b.expect(ctx, http.StatusNotFound, http.MethodPost, "/invitation", map[string]string{"token": token}, b.xsrfToken())
	b.expect(ctx, http.StatusNotFound, http.MethodPost, "/invitation/accept", nil, b.xsrfToken())

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
	kim := a.createUserAt(ctx, t, "kim@lakeside.edu", "kim@lakeside.edu")

	// A person with no account and no invitation: the account is made, and its first
	// session already reads the default role, written in the transaction that made the
	// account.
	b := a.browser(t)
	assertRedirect(t, a.entraSignIn(ctx, b, azureClaims(azureTenant, "oid-pat", "pat@lakeside.edu")), "/home", url.Values{})
	pat := signedInAs(ctx, t, b, "pat@lakeside.edu")
	if got := b.sessionData(ctx).Partner; got != "Member" {
		t.Errorf("the new account holds %q, want the method's default role Member", got)
	}

	// A person whose account was created ahead is linked to it, and holds what the
	// account was given: no default role.
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
	i.expect(ctx, http.StatusOK, http.MethodPost, "/invitation", map[string]string{"token": lastInvitationToken(t, a.mail)}, i.xsrfToken())
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

	m := regexp.MustCompile(`https://app\.example/login#invitation=([A-Za-z0-9_-]+)`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no invitation link in the message that opens %q: a message's text holds its link and is not printed", strings.SplitN(text, "\n", 2)[0])
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

	// Google is not the authority for the address of an account outside any Workspace
	// that is no Gmail address, so that person's first sign-in waits for the mailed code.
	g := a.browser(t)
	g.prime(ctx)
	assertRedirect(t, a.confirmAddress(ctx, g, a.googleSignIn(ctx, g, map[string]any{"sub": "g-sub-pat", "email": "pat@gmail.example", "email_verified": true}), "pat@gmail.example"), "/home", url.Values{})
	gv := signedInAs(ctx, t, g, "pat@gmail.example")
	assertOneLink(t, a.links(ctx, t, gv.UserID), google.AnyAccountConnection, google.AnyAccountConnection)

	u := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, u, map[string]any{"sub": "g-sub-u", "email": "u@gmail.example", "email_verified": false}), "/login", url.Values{"code": {"email_not_verified"}})
	a.assertNoRow(ctx, t, u, "u@gmail.example")
}

// seamRulesVerifiedBesideUnconfirmed is the scenario "a verified Google sign-in for an
// address another account holds unconfirmed". It records what happens today, which
// nobody has chosen. An account was created ahead under a username of its own, with an
// address entered for it, which its person has not confirmed. Then the person who
// holds that address at Google signs in through a method open to sign-up. An address
// held unconfirmed finds no account: whoever typed it may know the account's password,
// so finding it would put a proven identity on an account somebody else can enter. The
// sign-in therefore makes a second account, under the address as its username. One
// address has one holder, so the second account is written none, and the first keeps
// it, unconfirmed, with the roles it was given. Refusing the sign-in, or moving the
// address to the account that proved it, are the other ways out; whoever chooses one
// changes this scenario with the rule.
func seamRulesVerifiedBesideUnconfirmed[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, &rulesShape{
		tenants: azure.AnyTenant(), entraAccounts: inviteOnly,
		workspaces: google.AnyGoogleAccount(), googleAccounts: openSignUp,
	})
	// A Gmail address: Google vouches for it, and for no address from anywhere else.
	const address = "pat@gmail.com"
	first := a.seedWithEmail(ctx, t, "pat", address)

	b := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, b, map[string]any{"sub": "g-sub-pat", "email": address, "email_verified": true}), "/home", url.Values{})
	second := signedInAs(ctx, t, b, address)
	if second.UserID == first.String() {
		t.Fatalf("the Google sign-in entered %s, the account that holds the address unconfirmed, want an account of its own", first)
	}
	assertOneLink(t, a.links(ctx, t, second.UserID), google.AnyAccountConnection, google.AnyAccountConnection)
	if links := a.links(ctx, t, first.String()); len(links) != 0 {
		t.Errorf("the first account has %d identity links, want none: nothing proved it is the Google person's", len(links))
	}

	if email, confirmed := a.address(ctx, t, "pat"); email != address || confirmed {
		t.Errorf("the first account holds %q, confirmed %t, want %q, still unconfirmed", email, confirmed, address)
	}
	if email, _ := a.address(ctx, t, address); email != "" {
		t.Errorf("the second account holds the address %q, want none: one address has one holder", email)
	}
}

// seamRulesPersonalAccounts is the scenario "personal accounts beside one tenant, invite only".
func seamRulesPersonalAccounts[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, &rulesShape{
		tenants: azure.Tenants(azure.Tenant(azureTenant), azure.PersonalAccounts()), entraAccounts: inviteOnly,
		workspaces: google.Workspace("lakeside", hostedDomain), googleAccounts: inviteOnly,
	})
	pat := a.createUserAt(ctx, t, "pat@outlook.example", "pat@outlook.example")

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
	assertRedirect(t, a.entraRoundTrip(ctx, mfa, passwordOnlyClaims("oid-m", "mfa@lakeside.edu")), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
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

// passwordOnlyClaims are azureClaims of the one tenant whose amr names the password
// alone: Entra ran no second factor.
func passwordOnlyClaims(oid, username string) map[string]any {
	claims := azureClaims(azureTenant, oid, username)
	claims["amr"] = []string{"pwd"}

	return claims
}

// seamRulesAttestedByEvidence is the scenario "a token whose amr names mfa attests by evidence".
func seamRulesAttestedByEvidence[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, oneOrganization(openSignUp))
	a.hooks.set(func(h *hooks[Tx]) { h.policy = requireEmailCodeTx })
	a.entraPerson(ctx, t, azureClaims(azureTenant, "oid-a", "ann@lakeside.edu"))
	a.entraPerson(ctx, t, passwordOnlyClaims("oid-p", "pat@lakeside.edu"))

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
	assertRedirect(t, a.entraRoundTrip(ctx, held, passwordOnlyClaims("oid-p", "pat@lakeside.edu")), "/login", url.Values{"pending": {"mfa"}, "returnUrl": {"/home"}})
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
	a.entraPerson(ctx, t, passwordOnlyClaims("oid-p", "pat@lakeside.edu"))
	a.entraPerson(ctx, t, azureClaims(azureTenant, "oid-a", "ann@lakeside.edu"))

	// The Workspace the application administers vouches for its people.
	workspace := a.browser(t)
	workspace.prime(ctx)
	assertRedirect(t, a.googleSignIn(ctx, workspace, googleClaims("g-sub-sam", "sam@lakeside.edu")), "/home", url.Values{})
	assertEventsSeen(t, signedInAs(ctx, t, workspace, "sam@lakeside.edu"), "google:lakeside", "attested-by-declaration:lakeside")

	// So does the tenant, where the token carries no evidence.
	declared := a.browser(t)
	declared.prime(ctx)
	assertRedirect(t, a.entraSignIn(ctx, declared, passwordOnlyClaims("oid-p", "pat@lakeside.edu")), "/home", url.Values{})
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

// seamRulesAccountCannotHoldItsAddress is the scenario "an account that cannot hold its
// address": Google's word for a Gmail address spares a sign-in the mailed code because
// the address is written on the account as the session starts, and where another
// account holds that address, confirmed, it is written on nobody. The sign-in is
// refused, so nobody is signed in to an account with no confirmed address.
func seamRulesAccountCannotHoldItsAddress[Tx any](ctx context.Context, t *testing.T, be backend[Tx]) {
	a := newRulesApp(ctx, t, be, nameLinkShape())

	// One account holds the address, confirmed, under another name, and a second was
	// made ahead with the address as its sign-in name and nothing else. Google vouches
	// for the name, so the sign-in reaches the second, which cannot take the address:
	// it is refused with what any sign-in the rules reject reads, and links nothing.
	pat := a.createUser(ctx, t, "pat")
	a.confirmedAddress(ctx, t, pat, "pat@gmail.com")
	duplicate := a.createUser(ctx, t, "pat@gmail.com")
	b := a.browser(t)
	b.prime(ctx)
	assertRedirect(t, a.googleSignIn(ctx, b, outsideGoogleClaims("g-sub-pat", "pat@gmail.com")), "/login", url.Values{"code": {"identity_rejected"}})
	b.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	for name, id := range map[string]impulse.UUID{"the account that holds the address": pat, "the account under the name": duplicate} {
		if links := a.links(ctx, t, id.String()); len(links) != 0 {
			t.Errorf("identity links of %s after the refusal = %d, want none", name, len(links))
		}
	}

	// An identity linked before meets the same refusal. The person signed in while
	// nobody else held the address, which Google's word wrote on the account. An
	// administrator then took it off and confirmed it on another account, so the next
	// sign-in would leave the first with no confirmed address.
	lee := a.createUser(ctx, t, "lee@gmail.com")
	first := a.browser(t)
	assertRedirect(t, a.googleSignIn(ctx, first, outsideGoogleClaims("g-sub-lee", "lee@gmail.com")), "/home", url.Values{})
	if email, verified := first.authenticatedEmail(ctx); email != "lee@gmail.com" || !verified {
		t.Fatalf("the address after the first sign-in = %q (confirmed %v), want lee@gmail.com confirmed by Google's word", email, verified)
	}
	if err := a.setEmail(ctx, lee, nil); err != nil {
		t.Fatalf("SetUserEmail(nil) error = %v", err)
	}
	other := a.createUser(ctx, t, "lee")
	a.confirmedAddress(ctx, t, other, "lee@gmail.com")
	again := a.browser(t)
	again.prime(ctx)
	assertRedirect(t, a.googleSignIn(ctx, again, outsideGoogleClaims("g-sub-lee", "lee@gmail.com")), "/login", url.Values{"code": {"identity_rejected"}})
	again.expect(ctx, http.StatusUnauthorized, http.MethodGet, "/whoami", nil)
	if sent := a.mail.Messages(); len(sent) != 0 {
		t.Errorf("mail after the refusals = %d messages, want none", len(sent))
	}
}
