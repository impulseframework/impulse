package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/go-playground/validator/v10"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/auth/members"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/auth/staff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/identity/membersidentity"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/identity/staffidentity"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/router"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/resource/live"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/mail"
)

const (
	migrationsSource = "file://../../schema/migrations"
	devSeedSource    = "file://../../schema/devseed"

	// The development tenants, matching schema/devseed: a tenant's key is an id, and
	// the seed gives the two fixed ones.
	north = "11111111-1111-4111-8111-111111111111"
	south = "22222222-2222-4222-8222-222222222222"

	// The development identities the served suites act as, by the usernames they sign
	// in with: the staff logins admin (Administrator, and TenantAdministrator in every
	// tenant) and member (TenantAdministrator and DeviceOperator in north only, so a
	// token of theirs reads the readings there through the devices outlet); and the
	// portal's client, a member of the members auth who signs in through the simulated
	// directory and holds TenantAdministrator in north. The roles are held by the ids of
	// their accounts (served.adminID, served.memberID, served.clientID), which is how a
	// permission store names a person.
	adminUser     = "admin"
	memberUser    = "member"
	clientUser    = "client"
	adminPassword = "password"

	// The browser outlets' API prefixes.
	consoleAPI = "/console/api"
	portalAPI  = "/portal/api"

	// directoryTenant is the Microsoft tenant the portal's directory registration admits
	// in the served suites, by its tenant id: the connection of a member's identity link.
	directoryTenant = "00000000-0000-0000-0000-000000000000"
)

// servedConfigurer implements app.Configurer over the real dependencies: the test
// database, the real permission engine, and a real session manager, so the suites
// exercise the same served stack main composes.
type servedConfigurer struct {
	db      *initiator.SpannerDB
	auth    *staff.Auth
	members *members.Auth
	live    *live.Fake
	tenants *resource.TenantRoster
}

// TenantRoster is the tenant roster over the test database, read from the Tenants table
// the development seed filled and kept current as production's data level keeps it: a
// tenant written through the API reaches it after the commit, and the served stack
// filters it by the foothold answer of the engine of the auth the request came through.
func (c *servedConfigurer) TenantRoster() *resource.TenantRoster {
	return c.tenants
}

// TenantDeclarations is the roster's copy of what each tenant declares about who may
// enter it, as the served application reads it.
func (c *servedConfigurer) TenantDeclarations() resource.TenantDeclarations {
	return c.tenants.Declarations
}

func (c *servedConfigurer) ResourceClient() resource.Client {
	return resource.NewSpannerClient(c.db.Client)
}

// CursorKey seals the cursors the suites' paged lists issue; any key serves a test process.
func (c *servedConfigurer) CursorKey() *resource.CursorKey {
	key, err := resource.NewCursorKey(base64.StdEncoding.EncodeToString([]byte("skeleton-test-cursor-key-material!!")))
	if err != nil {
		panic(err)
	}

	return key
}

func (c *servedConfigurer) Access() access.Controller { return c.auth.Access() }

func (c *servedConfigurer) Staff() *staff.Auth { return c.auth }

// StaffIdentity is the staff auth's identity client, which the test auth built when it
// opened as the served application's does, so the suites drive the generated management
// as the served application does.
func (c *servedConfigurer) StaffIdentity() *staffidentity.Client { return c.auth.Identity() }

func (c *servedConfigurer) Members() *members.Auth { return c.members }

// MembersIdentity is the members auth's identity client, which the test members auth
// built the same way.
func (c *servedConfigurer) MembersIdentity() *membersidentity.Client { return c.members.Identity() }

func (c *servedConfigurer) Validator() *validator.Validate { return validator.New() }

func (c *servedConfigurer) LogExporter() logger.Exporter { return logger.NewConsoleExporter() }

// AppVersion is dev: the served stack's version check answers every release, so no
// suite's request is refused for the release it carries.
func (c *servedConfigurer) AppVersion() string {
	return "dev"
}

func (c *servedConfigurer) ConsoleDist() string { return "" }

func (c *servedConfigurer) PortalDist() string { return "" }

// Live is the in-memory live service the auths' permission engines signal policy
// changes through: the live service is required in every application.
func (c *servedConfigurer) Live() live.Service {
	return c.live
}

// LiveOrigins names no change feed origin: the suites serve no live pages.
func (c *servedConfigurer) LiveOrigins() []string {
	return nil
}

// served is one running instance of the application under test.
type served struct {
	server *httptest.Server
	// access is the staff auth's engine, members the members auth's, and membersAuth
	// the members auth itself, for a suite that creates a member as an operator would;
	// staffAuth is the staff auth, for a suite that creates a staff login of its own
	// beside the development ones.
	access      *access.Client
	members     *access.Client
	membersAuth *members.Auth
	staffAuth   *staff.Auth
	// adminID and memberID are the ids of the staff logins' accounts, and clientID the
	// id of the portal's client's account in the members auth: what each auth's
	// permission store holds their roles under and what the methods of the auth's
	// generated management name them by. The usernames are what they sign in with.
	adminID, memberID, clientID string
	// db is the test database, for the suites that read a table directly.
	db *initiator.SpannerDB
	// tenants is the roster the stack serves from, for a suite that writes what a
	// tenant declares in code, as a sign-in would, and reloads it as a write path does.
	tenants *resource.TenantRoster
	// mail is the recorder the staff auth sends its mail to, where a suite reads the
	// sign-in code a sign-in waits for.
	mail *mail.Recorder
	// membersMail is the recorder the members auth sends its mail to: the invitation of
	// its first administrator, where a suite invites one.
	membersMail *mail.Recorder
	// codeMu lets one sign-in at a time run its second factor on the stack, so the code a
	// browser reads from the recorder is its own, and trusted holds, per login, the token
	// of the browser the login asked to be remembered at their first sign-in, which their
	// later sign-ins carry (browser.signIn).
	codeMu  sync.Mutex
	trusted map[string]*http.Cookie
}

// newServed provisions the database the way the deployment does (the schema and the
// development tenants, then each auth opened over it with its embedded role file validated
// against the collection), creates the development identities as the bootstrap does (each
// user, then its roles, held by the id of the user's account), and serves the full router.
// The server is allocated before the members auth so the auth's directory callback can
// name the server's own address.
func newServed(ctx context.Context, t *testing.T) *served {
	t.Helper()

	return newServedInviting(ctx, t, "")
}

// newServedInviting is newServed with the members auth's first administrator invited at
// membersAdministrator before anything else of the auth exists, as the migrate command
// invites one while nobody of the auth holds the Super Administrator role
// (members.Auth.InviteAdministrator); "" invites none. The invitation is mailed to the members auth's recorder
// (served.membersMail).
func newServedInviting(ctx context.Context, t *testing.T, membersAdministrator string) *served {
	t.Helper()

	db, err := prepareDatabase(ctx, t, migrationsSource, devSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	// The live service both engines signal policy changes through, the App follows its
	// feature flags from, and the tenant roster reloads on: in-memory in the suites.
	svc := live.NewFake()
	// The tenant roster, started over the seeded tenants as production's data level
	// starts it; a tenant an invitation makes in a suite reaches it after the commit.
	roster := app.NewTenantRoster(resource.NewSpannerClient(db.Client), resource.WithTenantSignals(svc))
	if err := roster.Start(ctx); err != nil {
		t.Fatalf("resource.TenantRoster.Start() error = %v", err)
	}
	// The recorder the staff auth's mail goes to: the suites read the sign-in codes from it.
	sent := &mail.Recorder{}
	staffAuth, err := staff.New(ctx, db.Client, &staff.Settings{
		Collection:         router.Collection(),
		Signals:            svc,
		CookieKey:          testCookieKey,
		SessionTimeout:     time.Minute,
		Mail:               sent,
		MailLoginURL:       "http://outlets.test/console/login",
		DevicePage:         "/console/device",
		TenantDeclarations: app.NewTenantDeclarationReader(),
		TenantDirectories:  app.NewTenantDirectoryReader(),
		// What makes a tenant from an accepted invitation, over the stack's roster and
		// live service, as production's data level hands it in.
		TenantMaker: app.NewTenantMaker(roster, svc),
	})
	if err != nil {
		t.Fatalf("staff.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := staffAuth.Close(); err != nil {
			t.Errorf("staff.Auth.Close() error = %v", err)
		}
	})
	accessClient := staffAuth.Access()

	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)

	// The members auth: under the skipAuth build tag the directory is simulated and the
	// login returns straight to the callback, so the callback is the registration detail
	// that matters, beside a well-formed tenant id, which the method validates at
	// registration whether or not the directory is simulated. The tenant is declared as
	// one the deployment administers, so a sign-in through the directory carries the
	// tenant's own second factor and opens its session at the callback, with no sign-in
	// code asked.
	membersSent := &mail.Recorder{}
	membersAuth, err := members.New(ctx, db.Client, &members.Settings{
		Collection:     router.Collection(),
		Signals:        svc,
		CookieKey:      testCookieKey,
		SessionTimeout: time.Minute,
		Mail:           membersSent,
		MailLoginURL:   "http://outlets.test/portal/login",
		LoginURL:       "/portal/login",
		Directory:      members.DirectorySettings{TenantID: directoryTenant, AdministeredTenants: directoryTenant, RedirectURL: "http://" + server.Listener.Addr().String() + portalAPI + "/user/directory/callback"},
		// The declaration reader the sign-in rule reads with; the development seed
		// makes the directory above north's.
		TenantDeclarations: app.NewTenantDeclarationReader(),
		TenantDirectories:  app.NewTenantDirectoryReader(),
	})
	if err != nil {
		t.Fatalf("members.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := membersAuth.Close(); err != nil {
			t.Errorf("members.Auth.Close() error = %v", err)
		}
	})
	membersAccess := membersAuth.Access()
	// The deployment's release invites while nobody of the members auth holds the Super
	// Administrator role.
	if membersAdministrator != "" {
		invited, err := membersAuth.InviteAdministrator(ctx, membersAdministrator)
		if err != nil || invited.Outcome != session.FirstAdministratorInvited || invited.Email != membersAdministrator {
			t.Fatalf("members.Auth.InviteAdministrator(%s) = %+v, %v, want the first administrator invited", membersAdministrator, invited, err)
		}
	}
	// The portal's client is a member: the directory proves who they are, so they hold
	// no password here. A role is held by an account, so the client is created as the
	// bootstrap creates a member: a user without a password under the name the directory
	// will present, which their first sign-in links to, and then the membership, held by
	// the id of that user's account, in the members store.
	client, err := membersAuth.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: clientUser})
	if err != nil {
		t.Fatalf("CreateSessionUser(%s) in the members auth error = %v", clientUser, err)
	}
	clientID := client.String()
	if err := membersAccess.UserManager().AddUserRoles(ctx, accesstypes.DomainPolicyScope(north), accesstypes.User(clientID), "TenantAdministrator"); err != nil {
		t.Fatalf("AddUserRoles(%s, north) error = %v", clientUser, err)
	}

	// The staff logins, each user first: a role is held by an account, and the
	// permission store names a person by the id of theirs, never by the username.
	passwordAuth := staffAuth.Session()
	password := adminPassword
	ids := make(map[string]string, 2)
	for _, login := range []string{adminUser, memberUser} {
		id, err := passwordAuth.API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: login, Password: &password})
		if err != nil {
			t.Fatalf("CreateSessionUser(%s) error = %v", login, err)
		}
		ids[login] = id.String()
	}
	// The memberships as the bootstrap identities hold them, under the ids of the
	// accounts: the administrator's domain role in every tenant domain, one membership,
	// the others in north alone.
	assignments := []struct {
		login string
		scope accesstypes.PolicyScope
		role  accesstypes.Role
	}{
		{adminUser, accesstypes.GlobalPolicyScope(), accesstypes.SuperAdministrator},
		{adminUser, accesstypes.GlobalPolicyScope(), "Administrator"},
		{adminUser, accesstypes.EveryDomainPolicyScope(), "TenantAdministrator"},
		{memberUser, accesstypes.DomainPolicyScope(north), "TenantAdministrator"},
		{memberUser, accesstypes.DomainPolicyScope(north), "DeviceOperator"},
	}
	for _, a := range assignments {
		if err := accessClient.UserManager().AddUserRoles(ctx, a.scope, accesstypes.User(ids[a.login]), a.role); err != nil {
			t.Fatalf("AddUserRoles(%s, %v) error = %v", a.login, a.scope, err)
		}
	}

	// The snapshot swap is asynchronous: wait until the last-provisioned assignment is
	// visible to the engine before serving.
	waitForDomains(ctx, t, accessClient, accesstypes.User(ids[memberUser]), []accesstypes.Domain{north})
	waitForDomains(ctx, t, membersAccess, accesstypes.User(clientID), []accesstypes.Domain{north})

	a := app.New(&servedConfigurer{db: db, auth: staffAuth, members: membersAuth, live: svc, tenants: roster})
	// The App reads its feature flags as it is built; Start reports a copy that could
	// not be read and follows the table until the test ends.
	if err := a.Start(ctx); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}
	server.Config.Handler = router.New(a, router.Hooks{})
	server.Start()

	return &served{server: server, access: accessClient, tenants: roster, members: membersAccess, membersAuth: membersAuth, staffAuth: staffAuth, adminID: ids[adminUser], memberID: ids[memberUser], clientID: clientID, db: db, mail: sent, membersMail: membersSent, trusted: map[string]*http.Cookie{}}
}

// waitForDomains blocks until the engine's snapshot reports the user's footholds in
// exactly the expected tenants, asked the way the served stack asks (the development
// roster filtered by UserHasGrants, the user named by the id of their account): the
// store writes signal a reload, but the swap is asynchronous.
func waitForDomains(ctx context.Context, t *testing.T, client *access.Client, user accesstypes.User, want []accesstypes.Domain) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for {
		domains, err := footholds(ctx, client, user)
		if err == nil && slices.Equal(domains, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("policy snapshot never became visible; last domains for %s: %v (err %v)", user, domains, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// footholds lists the development tenants where the user, named by the id of their
// account, holds at least one grant.
func footholds(ctx context.Context, client *access.Client, user accesstypes.User) ([]accesstypes.Domain, error) {
	var domains []accesstypes.Domain
	for _, domain := range []accesstypes.Domain{north, south} {
		has, err := client.UserHasGrants(ctx, user, accesstypes.DomainScope(domain))
		if err != nil {
			return nil, errors.Wrap(err, "access.Client.UserHasGrants()")
		}
		if has {
			domains = append(domains, domain)
		}
	}

	return domains, nil
}

// testCookieKey signs session cookies in the suites; any 32 bytes will do.
const testCookieKey = "dGVzdC1jb29raWUta2V5LXRlc3QtY29va2llLWtleS0xMjM0NTY="

// outletXSRF names the XSRF cookie each session-serving outlet's auth issues: the browser
// on an outlet echoes that auth's cookie, as the outlet's web app does.
var outletXSRF = map[string]string{
	consoleAPI: staff.XSRFCookie,
	portalAPI:  members.XSRFCookie,
}

// outletTrusted names the cookie a remembered browser carries its trusted-device token
// in, per session-serving outlet.
var outletTrusted = map[string]string{
	consoleAPI: staff.TrustedDeviceCookie,
	portalAPI:  members.TrustedDeviceCookie,
}

// browser is one browser's view of the served application on one session-serving
// outlet: a cookie jar and the XSRF token the session middleware issued into it.
type browser struct {
	t      *testing.T
	base   string
	prefix string
	xsrf   string
	client *http.Client
	// trusted is the cookie a remembered browser carries its trusted-device token in.
	trusted string
	// served is the stack the browser is on, whose recorder holds the code a sign-in
	// waits for and whose trusted tokens a remembered browser carries.
	served *served
}

func newBrowser(t *testing.T, s *served, prefix string) *browser {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	xsrf, ok := outletXSRF[prefix]
	if !ok {
		t.Fatalf("no auth issues an XSRF cookie on %s", prefix)
	}

	return &browser{t: t, base: s.server.URL, prefix: prefix, xsrf: xsrf, client: &http.Client{Jar: jar}, trusted: outletTrusted[prefix], served: s}
}

// login posts the credentials to the outlet's login route and returns the status.
func (b *browser) login(ctx context.Context, user, password string) (status int, body []byte) {
	b.t.Helper()

	credentials, err := json.Marshal(map[string]string{"username": user, "password": password})
	if err != nil {
		b.t.Fatal(err)
	}

	return b.do(ctx, http.MethodPost, b.prefix+"/user/password/login", credentials)
}

// machine issues one request as a machine client carrying the bearer key (empty for an
// unauthenticated probe).
func machine(ctx context.Context, t *testing.T, s *served, token, method, path string) (status int, body []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, method, s.server.URL+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return resp.StatusCode, body
}

// do issues one request as this browser, carrying its cookies and XSRF token.
func (b *browser) do(ctx context.Context, method, path string, body []byte) (status int, respBody []byte) {
	b.t.Helper()

	return b.doWith(ctx, method, path, body, nil)
}

// doWith is do with headers of the request's own: a method asked for its warning
// (X-Warning), which answers what the call would do and saves nothing.
func (b *browser) doWith(ctx context.Context, method, path string, body []byte, headers map[string]string) (status int, respBody []byte) {
	b.t.Helper()

	req, err := http.NewRequestWithContext(ctx, method, b.base+path, bytes.NewReader(body))
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := b.xsrfToken(); token != "" {
		req.Header.Set("X-XSRF-TOKEN", token)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()

	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}

	return resp.StatusCode, respBody
}

// redirect issues one GET as this browser without following redirects and returns the
// status and the Location header. target is a path on the server or an absolute URL.
func (b *browser) redirect(ctx context.Context, target string) (status int, location string) {
	b.t.Helper()

	if !strings.HasPrefix(target, "http") {
		target = b.base + target
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		b.t.Fatal(err)
	}
	client := &http.Client{Jar: b.client.Jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		b.t.Fatal(err)
	}

	return resp.StatusCode, resp.Header.Get("Location")
}

// xsrfToken returns the XSRF cookie the session middleware issued, or empty before the
// first response.
func (b *browser) xsrfToken() string {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, b.base+b.prefix+"/user/session", http.NoBody)
	if err != nil {
		b.t.Fatal(err)
	}
	for _, cookie := range b.client.Jar.Cookies(req.URL) {
		if cookie.Name == b.xsrf {
			return cookie.Value
		}
	}

	return ""
}

// provesGrant names the conditional grant a test case proves. It parses the auth's role
// file (<auth>.Roles(), the file embedded in the auth package) and fails unless a grant for
// the role, permission, and resource carries exactly that condition text, so a case whose
// grant is gone or reworded fails here even when nobody ran impulse check, whose
// conditions-proven check reads these calls to find the conditional grants no case names.
// Write the file as the auth package's Roles() call and the coordinates as literals: the
// check reads them from the source.
//
//nolint:unparam // The role files' conditional grants are all one role's today; the next role's case names its own.
func provesGrant(t *testing.T, roles access.RoleFile, role accesstypes.Role, permission accesstypes.Permission, res accesstypes.Resource, condition string) {
	t.Helper()

	parsed, err := roles.Parse()
	if err != nil {
		t.Fatalf("parsing the role file: %v", err)
	}
	var conditions []string
	for _, r := range slices.Concat(parsed.Roles.Global, parsed.Roles.Domain) {
		if r.Name != role {
			continue
		}
		for _, g := range r.Permissions[permission] {
			if g.Resource != res {
				continue
			}
			if g.Condition == condition {
				return
			}
			conditions = append(conditions, g.Condition)
		}
	}
	t.Fatalf("no %s grant of the %s role on %s carries the condition %q (the file's conditions there: %q); the case proves a grant the file no longer carries", permission, role, res, condition, conditions)
}

// unmarshalDomains reads the user-domains answer, each tenant listed with its domain and
// its name, into the domains of the tenants the session reaches, in the order listed. A
// tenant the answer marks closed to the session is left out: the person holds a seat in
// it, and the session cannot enter it (closedDomains reads those).
func unmarshalDomains[T ~string](body []byte, domains *[]T) error {
	var listed []struct {
		Domain string `json:"domain"`
		Closed string `json:"closed"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		return errors.Wrap(err, "json.Unmarshal()")
	}
	out := make([]T, 0, len(listed))
	for _, entry := range listed {
		if entry.Closed != "" {
			continue
		}
		out = append(out, T(entry.Domain))
	}
	*domains = out

	return nil
}

// closedDomains reads the tenants the user-domains answer at path marks closed to the
// browser's session: each tenant's domain with the reason the server gives, a sentence.
// Empty, never nil, when the answer marks none.
func closedDomains(ctx context.Context, t *testing.T, b *browser, path string) map[string]string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, status, body)
	}
	var listed []struct {
		Domain string `json:"domain"`
		Closed string `json:"closed"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("user-domains is not a list: %v: %s", err, body)
	}
	closed := map[string]string{}
	for _, entry := range listed {
		if entry.Closed != "" {
			closed[entry.Domain] = entry.Closed
		}
	}

	return closed
}
