package authz

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/go-playground/validator/v10"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/pkg/auth/staff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/pkg/identity/staffidentity"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/pkg/router"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/identity"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/resource/live"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// testUser is the assumed identity every request carries; the suites script what it is
// permitted to do per test via grants. The generated matrix addresses one tenant, the
// generated router's TestDomain, which newTestHandler adds to the test application's
// tenant roster.
const testUser = "authz-user"

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

// TenantAdministratorRoles answers what the staff auth's generated identity client asks
// where it is built, the roles whose holders administer a tenant: the staff auth's own,
// as its engine is told them.
func (f *fakeAccess) TenantAdministratorRoles() []accesstypes.Role {
	_, tenantAdministrators := staff.AdministratorRoles()

	return tenantAdministrators
}

// UserManager answers the one question a generated handler asks the engine's user
// manager: the roles that exist in a tenant, which the Auth's generated management
// lists for a role picker. Every other method panics through the embedded nil
// interface, as the Controller's do.
func (f *fakeAccess) UserManager() access.UserManager {
	return fakeUserManager{}
}

// fakeUserManager scripts the roles of a scope: one, whatever the scope.
type fakeUserManager struct {
	access.UserManager
}

func (fakeUserManager) Roles(context.Context, accesstypes.PolicyScope) ([]access.RoleInfo, error) {
	return []access.RoleInfo{{Name: "Scripted", Description: "A scripted role."}}, nil
}

// UserHasGrants answers the concealed-domain foothold question the generated DomainGuard
// and the consolidated dispatcher ask after the tenant roster: the scripted table is
// domain-blind, so any grant at all is a foothold in the matrix's domain, and a case
// carrying no grants has no foothold and is answered as if the domain did not exist, per
// the generated suite's contract.
func (f *fakeAccess) UserHasGrants(_ context.Context, _ accesstypes.User, _ accesstypes.Scope) (bool, error) {
	return len(f.g) > 0, nil
}

func (f *fakeAccess) CheckUserResources(_ context.Context, _ accesstypes.Environment, _ accesstypes.User, _ accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	return f.decide(perm, resources), nil
}

func (f *fakeAccess) CheckRoleResources(_ context.Context, _ accesstypes.Environment, _ accesstypes.Role, _ accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	return f.decide(perm, resources), nil
}

func (f *fakeAccess) decide(perm accesstypes.Permission, resources []accesstypes.Resource) accesstypes.Decisions {
	decisions := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
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
	live    *live.Fake
	tenants *resource.TenantRoster
}

func (c *testConfigurer) ResourceClient() resource.Client {
	return resource.NewSpannerClient(c.db.Client)
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

func (c *testConfigurer) Staff() *staff.Auth { return nil }

// TenantDeclarations is nil: the generated matrix drives every route with its own
// grants, and a nil reader lets every tenant admit every session.
func (c *testConfigurer) TenantDeclarations() resource.TenantDeclarations { return nil }

// noSessionAPI stands where the matrix serves no session manager: a denied case is
// answered before any method runs, so nothing here is ever hashed or mailed, the one
// list it is asked through (how a person signs in) is asked about nobody, and an
// invitation whose check passed is told so where its link would be made.
type noSessionAPI struct{}

func (noSessionAPI) PrepareInvitation(context.Context, *session.InvitationRequest) (session.PreparedInvitation, error) {
	return nil, errors.New("the authorization matrix serves no session manager")
}

// SignInMethods names no method: the matrix registers none, and a denied case reads none.
func (noSessionAPI) SignInMethods() []session.RegisteredMethod {
	return nil
}

func (noSessionAPI) SignsInThrough(sessioninfo.AuthMethod, string) []string {
	return nil
}

// DirectoryName names no directory: the matrix registers no sign-in method.
func (noSessionAPI) DirectoryName(sessioninfo.AuthMethod, string) (name, label string) {
	return "", ""
}

// TenantName names no tenant: the matrix gives the session library no tenant names.
func (noSessionAPI) TenantName(accesstypes.Domain) string {
	return ""
}

// SessionTimeout answers ten minutes, the session library's own default: this suite
// sets none.
func (noSessionAPI) SessionTimeout() time.Duration {
	return 10 * time.Minute
}

// SignInFailureLimit answers five failed sign-ins, the session library's own default:
// this suite sets none.
func (noSessionAPI) SignInFailureLimit() int {
	return 5
}

// RequestAddressChange and SendPasswordReset mail nobody: there is no session manager
// here to mail through, and each says so.
func (noSessionAPI) RequestAddressChange(context.Context, impulse.UUID, string, session.AskedBy) error {
	return errors.New("the authorization matrix serves no session manager")
}

func (noSessionAPI) SendPasswordReset(context.Context, impulse.UUID, session.AskedBy) error {
	return errors.New("the authorization matrix serves no session manager")
}

// EndPasswordResetLinks withdraws nothing, and the two notices tell nobody: no link was
// mailed here and no mail is sent.
func (noSessionAPI) EndPasswordResetLinks(context.Context, impulse.UUID) error {
	return nil
}

func (noSessionAPI) NoticeFactorReset(context.Context, impulse.UUID) error {
	return nil
}

func (noSessionAPI) NoticeRemovedFromTenant(context.Context, string, string, bool) error {
	return nil
}

// StaffIdentity is the staff auth's identity client over the scripted engine: the matrix
// drives the generated methods' routes as it drives every other, and a denied case is
// answered before any of them runs. The client makes the tenants, so it takes the
// generated maker over the suites' roster, and the roles a tenant's administrator holds,
// as the staff auth names them: what reaches the maker is the check of the details an
// invitation that makes a tenant carries, in a transaction that is rolled back.
func (c *testConfigurer) StaffIdentity() *staffidentity.Client {
	_, tenantAdministrators := staff.AdministratorRoles()
	client, err := staffidentity.NewClient(c.Access(), noSessionAPI{}, app.NewTenantMaker(c.TenantRoster(), c.Live()), identity.WithAdministratorRoles(tenantAdministrators))
	if err != nil {
		panic(err)
	}

	return client
}

func (c *testConfigurer) Validator() *validator.Validate {
	return validator.New()
}

func (c *testConfigurer) LogExporter() logger.Exporter {
	return logger.NewConsoleExporter()
}

// AppVersion is dev: the matrix drives the bare test router, which checks no version.
func (c *testConfigurer) AppVersion() string {
	return "dev"
}

// Environment is none: the suites run as production does, which names no environment.
func (c *testConfigurer) Environment() string {
	return ""
}

func (c *testConfigurer) ConsoleDist() string { return "" }

// Live is an in-memory live service: the live service is required in every
// application, so the suites' App starts with a fake nothing subscribes through.
func (c *testConfigurer) Live() live.Service {
	if c.live == nil {
		c.live = live.NewFake()
	}

	return c.live
}

// LiveOrigins names no change feed origin: the suites serve no live pages.
func (c *testConfigurer) LiveOrigins() []string {
	return nil
}

// TenantRoster is the suites' tenant roster: the generated matrix's one domain value,
// added rather than read, since the empty test schema holds no tenant row and nothing
// starts the roster. resource.SessionPermissions filters it by the case's grants, and
// under concealed domains a case carrying no grants has no foothold and is answered as
// if the domain did not exist, which the scripted engine's UserHasGrants decides.
func (c *testConfigurer) TenantRoster() *resource.TenantRoster {
	return c.tenants
}

// newTestHandler composes the pipeline under test: the application's generated
// handlers served through the generated test router, behind an identity-seeding
// wrapper that stands in for the session middleware.
func newTestHandler(t *testing.T, db *initiator.SpannerDB, g grants) http.Handler {
	t.Helper()

	roster := app.NewTenantRoster(resource.NewSpannerClient(db.Client))
	roster.Add(router.TestDomain)
	a := app.New(&testConfigurer{db: db, g: g, tenants: roster})
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
