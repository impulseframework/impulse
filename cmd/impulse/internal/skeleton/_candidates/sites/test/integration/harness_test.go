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
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/go-playground/validator/v10"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	consoleapp "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/sites/apps/console/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/sites/apps/console/pkg/identity/staffidentity"
	consolerouter "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/sites/apps/console/pkg/router"
	portalapp "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/sites/apps/portal/app"
	portalrouter "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/sites/apps/portal/pkg/router"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/sites/pkg/auth/staff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/sites/pkg/config"
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

	// The development logins the served suites sign in as, by the usernames they sign in
	// with: admin holds Administrator and TenantAdministrator in every tenant, member
	// and the portal's client hold TenantAdministrator in north only. The roles are held
	// by the ids of the logins' accounts (served.adminID and served.memberID are the two
	// the suites name), which is how the permission store names a person.
	adminUser     = "admin"
	memberUser    = "member"
	clientUser    = "client"
	adminPassword = "open-the-console-first"
)

// servedConfigurer implements both sites' Configurer over the real dependencies: the
// test database, the real permission engine, and a real session manager, so the suites
// exercise the same served stack each site's main composes.
type servedConfigurer struct {
	db      *initiator.SpannerDB
	auth    *staff.Auth
	live    *live.Fake
	tenants *resource.TenantRoster
}

// TenantRoster is the tenant roster both sites' Apps share, read from the Tenants table
// the development seed filled and kept current as production's data level keeps it: a
// tenant written through the console's API reaches it after the commit, and each site's
// served stack filters it by the engine's foothold answer.
func (c *servedConfigurer) TenantRoster() *resource.TenantRoster {
	return c.tenants
}

// TenantDeclarations is the shared roster's copy of what each tenant declares about who
// may enter it, as both sites' served stacks read it.
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

func (c *servedConfigurer) Validator() *validator.Validate { return validator.New() }

func (c *servedConfigurer) LogExporter() logger.Exporter { return logger.NewConsoleExporter() }

// AppVersion is dev: the served stack's version check answers every release, so no
// suite's request is refused for the release it carries.
func (c *servedConfigurer) AppVersion() string {
	return "dev"
}

// Environment is none: the suites run as production does, which names no environment.
func (c *servedConfigurer) Environment() string {
	return ""
}

func (c *servedConfigurer) Dist() string { return "" }

// Live is the in-memory live service the auths' permission engines signal policy
// changes through: the live service is required in every application.
func (c *servedConfigurer) Live() live.Service {
	return c.live
}

// LiveOrigins names no change feed origin: the suites serve no live pages.
func (c *servedConfigurer) LiveOrigins() []string {
	return nil
}

// served is one running instance of the application under test: both sites over one
// database, one policy store, and one session store.
type served struct {
	console *httptest.Server
	portal  *httptest.Server
	access  *access.Client
	// auth is the staff auth both sites bind to, for a suite that creates a login of its
	// own beside the development ones.
	auth *staff.Auth
	// adminID and memberID are the ids of the development logins' accounts: what the
	// permission store holds their roles under and what the methods of the auth's
	// generated management name them by. The usernames are what they sign in with.
	adminID, memberID string
	// db is the test database, for the suites that read a table directly.
	db *initiator.SpannerDB
	// tenants is the roster the stack serves from, for a suite that writes what a
	// tenant declares in code, as a sign-in would, and reloads it as a write path does.
	tenants *resource.TenantRoster
	// mail is the recorder the staff auth sends its mail to, where a suite reads the
	// sign-in code a sign-in waits for.
	mail *mail.Recorder
	// codeMu lets one sign-in at a time run its second factor on the stack, so the code a
	// browser reads from the recorder is its own, and trusted holds, per login, the token
	// of the browser the login asked to be remembered at their first sign-in, which their
	// later sign-ins carry (browser.signIn).
	codeMu  sync.Mutex
	trusted map[string]*http.Cookie
}

// newServed provisions the database the way the deployment does (the schema and the
// development tenants, then the auth opened over it with its embedded role file validated
// against the union of the sites' collections), creates the development logins as the
// bootstrap does (each user, then its roles, held by the id of the user's account), and
// serves both sites' full routers.
func newServed(ctx context.Context, t *testing.T) *served {
	t.Helper()

	db, err := prepareDatabase(ctx, t, migrationsSource, devSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	collection, err := config.Collection()
	if err != nil {
		t.Fatalf("config.Collection() error = %v", err)
	}
	// The live service the engine signals policy changes through, both sites' Apps
	// follow their feature flags from, and the tenant roster reloads on: in-memory in
	// the suites.
	svc := live.NewFake()
	// The tenant roster, started over the seeded tenants as production's data level
	// starts it, with the console site's generated constructor as the data level builds
	// it; a tenant an invitation makes in a suite reaches it after the commit.
	roster := consoleapp.NewTenantRoster(resource.NewSpannerClient(db.Client), resource.WithTenantSignals(svc))
	if err := roster.Start(ctx); err != nil {
		t.Fatalf("resource.TenantRoster.Start() error = %v", err)
	}
	// The recorder the staff auth's mail goes to: the suites read the sign-in codes from it.
	sent := &mail.Recorder{}
	auth, err := staff.New(ctx, db.Client, &staff.Settings{
		Collection:         collection,
		Signals:            svc,
		CookieKey:          testCookieKey,
		SessionTimeout:     time.Minute,
		Mail:               sent,
		MailLoginURL:       "http://sites.test/login",
		TenantDeclarations: consoleapp.NewTenantDeclarationReader(),
		TenantDirectories:  consoleapp.NewTenantDirectoryReader(),
		// What makes a tenant from an accepted invitation, over the stack's roster and
		// live service, as production's data level hands it in.
		TenantMaker: consoleapp.NewTenantMaker(roster, svc),
	})
	if err != nil {
		t.Fatalf("staff.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := auth.Close(); err != nil {
			t.Errorf("staff.Auth.Close() error = %v", err)
		}
	})
	accessClient := auth.Access()

	// The development logins, each user first: a role is held by an account, and the
	// permission store names a person by the id of theirs, never by the username.
	passwordAuth := auth.Session()
	password := adminPassword
	ids := make(map[string]string, 3)
	for _, login := range []string{adminUser, memberUser, clientUser} {
		id, err := passwordAuth.API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: login, Password: &password})
		if err != nil {
			t.Fatalf("CreateSessionUser(%s) error = %v", login, err)
		}
		ids[login] = id.String()
	}
	// The memberships as the bootstrap identities hold them, under the ids of the
	// accounts: the administrator's domain role in every tenant domain, one membership,
	// and the member's in north alone.
	assignments := []struct {
		login string
		scope accesstypes.PolicyScope
		role  accesstypes.Role
	}{
		{adminUser, accesstypes.GlobalPolicyScope(), accesstypes.SuperAdministrator},
		{adminUser, accesstypes.GlobalPolicyScope(), "Administrator"},
		{adminUser, accesstypes.EveryDomainPolicyScope(), "TenantAdministrator"},
		{memberUser, accesstypes.DomainPolicyScope(north), "TenantAdministrator"},
		{clientUser, accesstypes.DomainPolicyScope(north), "TenantAdministrator"},
	}
	for _, a := range assignments {
		if err := accessClient.UserManager().AddUserRoles(ctx, a.scope, accesstypes.User(ids[a.login]), a.role); err != nil {
			t.Fatalf("AddUserRoles(%s, %v) error = %v", a.login, a.scope, err)
		}
	}

	// The snapshot swap is asynchronous: wait until the last-provisioned assignment is
	// visible to the engine before serving.
	waitForDomains(ctx, t, accessClient, accesstypes.User(ids[clientUser]), []accesstypes.Domain{north})

	conf := &servedConfigurer{db: db, auth: auth, live: svc, tenants: roster}
	// Each site's App reads its feature flags as it is built; Start reports a copy that
	// could not be read and follows the table until the test ends.
	consoleApp, portalApp := consoleapp.New(conf), portalapp.New(conf)
	if err := consoleApp.Start(ctx); err != nil {
		t.Fatalf("console app.Start() error = %v", err)
	}
	if err := portalApp.Start(ctx); err != nil {
		t.Fatalf("portal app.Start() error = %v", err)
	}
	console := httptest.NewServer(consolerouter.New(consoleApp, consolerouter.Hooks{}))
	t.Cleanup(console.Close)
	portal := httptest.NewServer(portalrouter.New(portalApp, portalrouter.Hooks{}))
	t.Cleanup(portal.Close)

	return &served{console: console, portal: portal, access: accessClient, auth: auth, tenants: roster, adminID: ids[adminUser], memberID: ids[memberUser], db: db, mail: sent, trusted: map[string]*http.Cookie{}}
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

// browser is one browser's view of the served application: a cookie jar and the XSRF
// token the session middleware issued into it.
type browser struct {
	t      *testing.T
	base   string
	client *http.Client
	// served is the stack the browser is on, whose recorder holds the code a sign-in
	// waits for and whose trusted tokens a remembered browser carries.
	served *served
}

// newBrowser opens a browser on one site of the stack.
func newBrowser(t *testing.T, s *served, site *httptest.Server) *browser {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	return &browser{t: t, base: site.URL, client: &http.Client{Jar: jar}, served: s}
}

// login posts the credentials to the login route and returns the status.
func (b *browser) login(ctx context.Context, user, password string) (status int, body []byte) {
	b.t.Helper()

	credentials, err := json.Marshal(map[string]string{"username": user, "password": password})
	if err != nil {
		b.t.Fatal(err)
	}

	return b.do(ctx, http.MethodPost, "/api/user/password/login", credentials)
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

// xsrfToken returns the XSRF cookie the session middleware issued, or empty before the
// first response.
func (b *browser) xsrfToken() string {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, b.base+"/api/user/session", http.NoBody)
	if err != nil {
		b.t.Fatal(err)
	}
	for _, cookie := range b.client.Jar.Cookies(req.URL) {
		if cookie.Name == staff.XSRFCookie {
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
// tenant the answer marks unavailable to the session is left out: the person holds a seat
// in it, and the session cannot enter it (unavailableDomains reads those).
func unmarshalDomains[T ~string](body []byte, domains *[]T) error {
	var listed []struct {
		Domain      string `json:"domain"`
		Unavailable string `json:"unavailable"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		return errors.Wrap(err, "json.Unmarshal()")
	}
	out := make([]T, 0, len(listed))
	for _, entry := range listed {
		if entry.Unavailable != "" {
			continue
		}
		out = append(out, T(entry.Domain))
	}
	*domains = out

	return nil
}

// unavailableDomains reads the tenants the user-domains answer at path marks unavailable
// to the browser's session: each tenant's domain with the reason the server gives, a
// sentence. Empty, never nil, when the answer marks none.
func unavailableDomains(ctx context.Context, t *testing.T, b *browser, path string) map[string]string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, status, body)
	}
	var listed []struct {
		Domain      string `json:"domain"`
		Unavailable string `json:"unavailable"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("user-domains is not a list: %v: %s", err, body)
	}
	unavailable := map[string]string{}
	for _, entry := range listed {
		if entry.Unavailable != "" {
			unavailable[entry.Domain] = entry.Unavailable
		}
	}

	return unavailable
}
