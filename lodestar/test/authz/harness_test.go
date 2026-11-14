// Demonstrates: authz-matrix.
package authz

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/membersidentity"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/resource/filestore"
	"github.com/impulseframework/impulse/resource/jobs"
	"github.com/impulseframework/impulse/resource/live"
	"github.com/impulseframework/impulse/resource/scheduled"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// testUser is the assumed identity every request carries; the suites script what it is
// permitted to do per test via grants.
const testUser = "authz-user"

// testDomain is the domain value the generated matrix addresses every domain-scoped
// route with; newTestHandler adds it to the application's tenant roster.
const testDomain accesstypes.Domain = "testDomain"

// fakeAccess scripts access.Controller's permission checks. Every Controller method
// the pipeline does not consume panics through the embedded nil interface, keeping the
// fake honest about what the pipeline actually draws on.
type fakeAccess struct {
	access.Controller
	g grants
}

func (f *fakeAccess) ForUser(user accesstypes.User) *access.UserChecker {
	return access.NewUserChecker(f, user)
}

func (f *fakeAccess) ForRole(role accesstypes.Role) *access.RoleChecker {
	return access.NewRoleChecker(f, role)
}

// UserManager answers the one question a generated handler asks the engine's user
// manager: the roles that exist in a sector, which the crew's generated management
// lists for a role picker (CrewSectorRoles). Every other method panics through
// the embedded nil interface, as the Controller's do.
func (f *fakeAccess) UserManager() access.UserManager {
	return fakeUserManager{}
}

// fakeUserManager scripts the roles of a scope: one, whatever the scope.
type fakeUserManager struct {
	access.UserManager
}

func (fakeUserManager) Roles(context.Context, accesstypes.PolicyScope) ([]access.RoleInfo, error) {
	return []access.RoleInfo{{Name: "Cadet", Description: "A scripted role."}}, nil
}

// UserHasGrants answers the concealed-domain foothold question the generated DomainGuard
// and the consolidated dispatcher ask after the tenant roster: the scripted table is
// domain-blind, so any grant at all is a foothold in the matrix's domain, and a case
// carrying no grants has no foothold and is answered as if the domain did not exist, per
// the generated suite's contract.
func (f *fakeAccess) UserHasGrants(_ context.Context, _ accesstypes.User, _ accesstypes.Scope) (bool, error) {
	return len(f.g) > 0, nil
}

func (f *fakeAccess) CheckUserResources(_ context.Context, _ accesstypes.Environment, _ accesstypes.User, _ accesstypes.Scope, perm accesstypes.Permission, names ...accesstypes.Resource) (accesstypes.Decisions, error) {
	return f.decide(perm, names), nil
}

func (f *fakeAccess) CheckRoleResources(_ context.Context, _ accesstypes.Environment, _ accesstypes.Role, _ accesstypes.Scope, perm accesstypes.Permission, names ...accesstypes.Resource) (accesstypes.Decisions, error) {
	return f.decide(perm, names), nil
}

func (f *fakeAccess) decide(perm accesstypes.Permission, names []accesstypes.Resource) accesstypes.Decisions {
	decisions := make(accesstypes.Decisions, len(names))
	for _, res := range names {
		if f.g[perm] {
			decisions[res] = accesstypes.Granted()
		} else {
			decisions[res] = accesstypes.Denied()
		}
	}

	return decisions
}

// testConfigurer implements app.Configurer over the test dependencies, so the App is
// assembled through the same seam main assembles it through. Session is nil: the App
// owns no router, these suites compose the API surface through router.NewTestRouter,
// and nothing on that path touches the session.
type testConfigurer struct {
	db      *initiator.SpannerDB
	g       grants
	stores  []resource.ClientOption
	live    *live.Fake
	tenants *resource.TenantRoster
}

func (c *testConfigurer) ResourceClient() resource.Client {
	return resource.NewSpannerClient(c.db.Client, c.stores...)
}

// CursorKey seals the cursors the suites' paged lists issue; any key serves a test process.
func (c *testConfigurer) CursorKey() *resource.CursorKey {
	key, err := resource.NewCursorKey(base64.StdEncoding.EncodeToString([]byte("skeleton-test-cursor-key-material!!")))
	if err != nil {
		panic(err)
	}

	return key
}

func (c *testConfigurer) Access() access.Controller {
	return &fakeAccess{g: c.g}
}

func (c *testConfigurer) Crew() *crew.Auth { return nil }

func (c *testConfigurer) Members() *members.Auth { return nil }

func (c *testConfigurer) MembersAccess() access.Controller { return nil }

func (c *testConfigurer) Validator() *validator.Validate {
	return validator.New()
}

func (c *testConfigurer) LogExporter() logger.Exporter {
	return logger.NewConsoleExporter()
}

// AppVersion is dev: the matrix drives the bare test router, which checks no version.
func (c *testConfigurer) AppVersion() string { return "dev" }

func (c *testConfigurer) ConsoleDist() string { return "" }

func (c *testConfigurer) PortalDist() string { return "" }

// DroidsAPIKey is unused by these suites: the matrix drives the bare test router, which
// carries no token check.
func (c *testConfigurer) DroidsAPIKey() string { return "authz-droids-key" }

// Scheduler is none: the matrix drives the bare test router, which mounts no scheduled
// route, and a missing guard refuses every scheduled call.
func (c *testConfigurer) Scheduler() *scheduled.Guard {
	return nil
}

// Jobs is a fake starter: no suite starts the job process, and a scheduled call that
// does records the start.
func (c *testConfigurer) Jobs() jobs.Starter {
	return jobs.NewFake()
}

// Live is an in-memory live service: the live service is required in every
// application, so the matrix's App starts with a fake nothing subscribes through.
func (c *testConfigurer) Live() live.Service {
	if c.live == nil {
		c.live = live.NewFake()
	}

	return c.live
}

// LiveOrigins names no change feed origin: the matrix serves no live pages.
func (c *testConfigurer) LiveOrigins() []string { return nil }

// noSessionAPI stands where the matrix serves no session manager: a denied case is
// answered before any method runs, so nothing here is ever hashed.
type noSessionAPI struct{}

func (noSessionAPI) HashPassword(string) (string, error) {
	return "", fmt.Errorf("the authorization matrix serves no session manager")
}

func (noSessionAPI) PrepareInvitation(context.Context, *session.InvitationRequest) (session.PreparedInvitation, error) {
	return nil, fmt.Errorf("the authorization matrix serves no session manager")
}

// CrewIdentity and MembersIdentity are the two auths' identity clients over the scripted
// engine: the matrix drives the generated methods' routes as it drives every other, and
// a denied case is answered before any of them runs.
func (c *testConfigurer) CrewIdentity() *crewidentity.Client {
	client, err := crewidentity.NewClient(c.Access(), noSessionAPI{})
	if err != nil {
		panic(err)
	}

	return client
}

func (c *testConfigurer) MembersIdentity() *membersidentity.Client {
	client, err := membersidentity.NewClient(c.Access(), noSessionAPI{})
	if err != nil {
		panic(err)
	}

	return client
}

// TenantRoster is the matrix's roster: the generated constructor over the test client,
// holding the one domain the generated matrix addresses, added by newTestHandler. The
// empty test schema holds no sector rows, so nothing is read and the roster is never
// started; under concealed domains a case carrying no grants has no foothold and is
// answered as if the domain did not exist, which the scripted engine's UserHasGrants
// decides.
func (c *testConfigurer) TenantRoster() *resource.TenantRoster {
	return c.tenants
}

// TenantDeclarations is nil: no sector declares anything, so the matrix's cases are
// decided by the roster and the scripted engine alone.
func (c *testConfigurer) TenantDeclarations() resource.TenantDeclarations {
	return nil
}

// newTestHandler composes the pipeline under test: the application's generated
// handlers served through the generated test router, behind an identity-seeding
// wrapper that stands in for the session middleware.
func newTestHandler(t *testing.T, db *initiator.SpannerDB, g grants) http.Handler {
	t.Helper()

	// The two memory stores production opens from APP_FILE_STORE and
	// APP_FILE_STORE_DOCUMENTS, wired as the data level wires them; the matrix never
	// stores a file, and the generated router requires both at start.
	stores := []resource.ClientOption{resource.WithFileStore(filestore.NewMem()), resource.WithNamedFileStore[resources.Documents](filestore.NewMem())}

	// The suite's domain value is added to the roster with Add, the generated matrix's
	// contract: the empty schema holds no tenant row for Start to read.
	tenants := app.NewSectorRoster(resource.NewSpannerClient(db.Client, stores...))
	tenants.Add(testDomain)

	a := app.New(&testConfigurer{db: db, g: g, stores: stores, tenants: tenants})
	// The App reads its feature flags as it is built, so a suite that flips a flag
	// before newTestHandler drives the App in that state; Start reports a copy that
	// could not be read and follows the table until the test ends.
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}

	return withIdentity(testUser, router.NewTestRouter(a))
}

// withIdentity seeds the session identity the way the session middleware would, making
// the pipeline's identity assumption explicit: these suites test authorization, never
// authentication.
func withIdentity(user string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
			SessionInfo: &sessioninfo.SessionInfo{Username: user},
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
