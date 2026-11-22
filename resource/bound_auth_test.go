package resource

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// boundThrough is ctx as a handler behind BindAuth(name) sees it.
func boundThrough(t *testing.T, ctx context.Context, name string) context.Context {
	t.Helper()

	var bound context.Context
	handler := BindAuth(name)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		bound = r.Context()
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil))
	if bound == nil {
		t.Fatalf("BindAuth(%q) did not pass the request on", name)
	}

	return bound
}

// The binds a test puts in front of a request: nothing, the generated routes' bind
// (WithAuth) and a group's (BindAuth, through its middleware).
func bindNothing(_ *testing.T, ctx context.Context) context.Context {
	return ctx
}

func withAuthOf(name string) func(*testing.T, context.Context) context.Context {
	return func(_ *testing.T, ctx context.Context) context.Context {
		return WithAuth(ctx, name)
	}
}

func bindAuthOf(name string) func(*testing.T, context.Context) context.Context {
	return func(t *testing.T, ctx context.Context) context.Context {
		return boundThrough(t, ctx, name)
	}
}

// TestBindAuth pins the one record of the auth a request came through: what AuthFrom
// and BoundAuth answer after each order of the generated routes' bind (WithAuth) and a
// group's (BindAuth). A group's bind stands: WithAuth leaves it as it is, and a group's
// bind marks a name WithAuth gave as the group's. A second bind that names another auth
// is refused (TestBindAuth_anotherAuth).
func TestBindAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		before    func(*testing.T, context.Context) context.Context
		call      func(*testing.T, context.Context) context.Context
		wantAuth  string
		wantOK    bool
		wantBound string
	}{
		{
			name:   "the generated routes' bind names the auth, and no group bound the request",
			before: bindNothing, call: withAuthOf("crew"), wantAuth: "crew", wantOK: true,
		},
		{
			name:   "the generated routes' bind renames a request no group bound",
			before: withAuthOf("crew"), call: withAuthOf("members"), wantAuth: "members", wantOK: true,
		},
		{
			name:   "a group's bind names the auth, and the group bound the request",
			before: bindNothing, call: bindAuthOf("crew"), wantAuth: "crew", wantOK: true, wantBound: "crew",
		},
		{
			name:   "the generated routes' bind leaves a request a group bound as it is",
			before: bindAuthOf("crew"), call: withAuthOf("members"), wantAuth: "crew", wantOK: true, wantBound: "crew",
		},
		{
			name:   "a second group's bind of the same auth passes the request on",
			before: bindAuthOf("crew"), call: bindAuthOf("crew"), wantAuth: "crew", wantOK: true, wantBound: "crew",
		},
		{
			name:   "a group's bind of the auth the generated routes bound marks it the group's",
			before: withAuthOf("crew"), call: bindAuthOf("crew"), wantAuth: "crew", wantOK: true, wantBound: "crew",
		},
		{
			name:   "an empty name binds nothing",
			before: bindNothing, call: bindAuthOf(""),
		},
		{
			name:   "an empty name leaves a group's bind as it is",
			before: bindAuthOf("crew"), call: bindAuthOf(""), wantAuth: "crew", wantOK: true, wantBound: "crew",
		},
		{
			name:   "an empty name leaves the generated routes' bind as it is",
			before: withAuthOf("crew"), call: bindAuthOf(""), wantAuth: "crew", wantOK: true,
		},
		{
			name:   "a context nothing bound carries no auth",
			before: bindNothing, call: bindNothing,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := tt.call(t, tt.before(t, context.Background()))
			if auth, ok := AuthFrom(ctx); auth != tt.wantAuth || ok != tt.wantOK {
				t.Errorf("AuthFrom() = %q, %v, want %q, %v", auth, ok, tt.wantAuth, tt.wantOK)
			}
			if got := BoundAuth(ctx); got != tt.wantBound {
				t.Errorf("BoundAuth() = %q, want %q", got, tt.wantBound)
			}
		})
	}
}

// TestBindAuth_anotherAuth pins the refusal of a group's bind that names another auth
// than the one the request is bound to, by a group or by the generated routes: the
// request fails with a 500, the next handler does not run, and the cause logged names
// both auths.
func TestBindAuth_anotherAuth(t *testing.T) {
	t.Parallel()

	const want = "this request is bound to the crew auth, and a route under it asks resource.BindAuth to bind it to the members auth. " +
		"A request comes through one auth, the one its outlet's group binds: take the second resource.BindAuth out of the route, " +
		"or serve the route from the members auth's own outlet"
	tests := []struct {
		name   string
		before func(*testing.T, context.Context) context.Context
	}{
		{name: "a request a group bound to another auth is refused", before: bindAuthOf("crew")},
		{name: "a request the generated routes bound to another auth is refused", before: withAuthOf("crew")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := tt.before(t, context.Background())
			ran := false
			handler := BindAuth("members")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				ran = true
			}))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil))

			if rr.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
			}
			if ran {
				t.Error("the next handler ran, want the request refused before it")
			}
			_, err := bindAuth(ctx, "members")
			if err == nil {
				t.Fatal("bindAuth() error = nil, want the refusal")
			}
			if !strings.HasSuffix(err.Error(), "error="+want) {
				t.Errorf("bindAuth() error = %v, want the cause %q", err, want)
			}
		})
	}
}

// TestSessionPermissions_boundAuth pins that a request bound to an auth is answered
// only from that auth's permission engine. The checker forUser yields for the session's
// person is judged first, then, for a role principal, the checker forRole yields; where
// either answers from another auth's engine, or from one opened under no auth's name,
// every question of the request answers the refusal and User() is still the session's
// person. A request no group bound is answered as before, and no checker is asked which
// auth it answers from.
func TestSessionPermissions_boundAuth(t *testing.T) {
	t.Parallel()

	userCtx := accountCtx("alice", aliceID, nil)
	roleCtx := sessionCtx("alice", &sessioninfo.Impersonation{
		Actor: "alice", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.RolePrincipal("PartnerViewer"),
	})
	otherEngine := func(bound, engine string) string {
		return fmt.Sprintf(otherAuthEngineFmt, bound, engine, bound)
	}
	unnamedEngine := func(bound string) string {
		return fmt.Sprintf(unnamedEngineFmt, bound, bound)
	}
	const (
		roleChecker = iota
		noForRole
		forRoleYieldsNil
	)

	tests := []struct {
		name       string
		ctx        context.Context
		bound      string
		userAuth   string
		roleAuth   string
		forRole    int
		wantRefuse string
		// wantGranted is the answer of an unrefused check: the role checker of a role
		// principal with none fails closed.
		wantGranted bool
	}{
		{
			name: "a user's request no group bound is answered as before, whatever auth the engine was opened for",
			ctx:  userCtx, userAuth: "members", wantGranted: true,
		},
		{
			name: "a role's request no group bound is answered as before, whatever auth the engines were opened for",
			ctx:  roleCtx, userAuth: "members", roleAuth: "", wantGranted: true,
		},
		{
			name: "a user's request bound to the auth its engine was opened for is answered as before",
			ctx:  userCtx, bound: "staff", userAuth: "staff", wantGranted: true,
		},
		{
			name: "a user's request bound to one auth and asked of another auth's engine is refused",
			ctx:  userCtx, bound: "staff", userAuth: "members", wantRefuse: otherEngine("staff", "members"),
		},
		{
			name: "a user's request bound to an auth and asked of an engine opened under no auth's name is refused",
			ctx:  userCtx, bound: "staff", userAuth: "", wantRefuse: unnamedEngine("staff"),
		},
		{
			name: "a role's request bound to the auth both engines were opened for is answered as before",
			ctx:  roleCtx, bound: "staff", userAuth: "staff", roleAuth: "staff", wantGranted: true,
		},
		{
			name: "a role's request whose role checker answers from another auth's engine is refused",
			ctx:  roleCtx, bound: "staff", userAuth: "staff", roleAuth: "members", wantRefuse: otherEngine("staff", "members"),
		},
		{
			name: "a role's request whose person's checker answers from another auth's engine is refused",
			ctx:  roleCtx, bound: "staff", userAuth: "members", roleAuth: "staff", wantRefuse: otherEngine("staff", "members"),
		},
		{
			name: "the person's checker is judged before the role's",
			ctx:  roleCtx, bound: "staff", userAuth: "members", roleAuth: "", wantRefuse: otherEngine("staff", "members"),
		},
		{
			name: "a role's request bound to its person's auth with no role checker fails closed as before",
			ctx:  roleCtx, bound: "staff", userAuth: "staff", forRole: noForRole, wantGranted: false,
		},
		{
			name: "a role's request bound to its person's auth whose role checker is none fails closed as before",
			ctx:  roleCtx, bound: "staff", userAuth: "staff", forRole: forRoleYieldsNil, wantGranted: false,
		},
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{"tenant"}, nil
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var asked int
			forUser := func(user accesstypes.User) *stubPermissions {
				return &stubPermissions{user: user, auth: tt.userAuth, authAsked: &asked, domains: []accesstypes.Domain{"tenant"}}
			}
			ctx := tt.ctx
			if tt.bound != "" {
				ctx = boundThrough(t, ctx, tt.bound)
			}
			var perms UserPermissions
			switch tt.forRole {
			case noForRole:
				perms = SessionPermissions(ctx, forUser, (func(accesstypes.Role) RolePermissions)(nil), roster, nil, nil)
			case forRoleYieldsNil:
				perms = SessionPermissions(ctx, forUser, RolePrincipalsUnsupported, roster, nil, nil)
			default:
				forRole := func(role accesstypes.Role) *stubRolePermissions {
					return &stubRolePermissions{role: role, auth: tt.roleAuth, authAsked: &asked}
				}
				perms = SessionPermissions(ctx, forUser, forRole, roster, nil, nil)
			}

			if perms.User() != aliceUser {
				t.Errorf("User() = %q, want %q", perms.User(), aliceUser)
			}
			if tt.bound == "" && asked != 0 {
				t.Errorf("Auth() was asked %d times of a request no group bound, want none", asked)
			}
			if tt.wantRefuse == "" {
				decisions, err := perms.Check(context.Background(), accesstypes.NewEnvironment(), accesstypes.GlobalScope(), accesstypes.Update, "documents")
				if err != nil {
					t.Fatalf("Check() error = %v", err)
				}
				if got := decisions["documents"].IsGranted(); got != tt.wantGranted {
					t.Errorf("Check(Update) granted = %v, want %v", got, tt.wantGranted)
				}

				return
			}

			scope := accesstypes.DomainScope("tenant")
			_, checkErr := perms.Check(context.Background(), accesstypes.NewEnvironment(), scope, accesstypes.Update, "documents")
			_, digestErr := perms.PermissionDigest(context.Background(), scope)
			_, hasGrantsErr := perms.HasGrants(context.Background(), scope)
			_, domainsErr := perms.Domains(context.Background())
			_, reachesErr := perms.Reaches(context.Background(), "tenant")
			_, heldErr := perms.HeldDomains(context.Background())
			for method, err := range map[string]error{
				"Check": checkErr, "PermissionDigest": digestErr, "HasGrants": hasGrantsErr,
				"Domains": domainsErr, "Reaches": reachesErr, "HeldDomains": heldErr,
			} {
				if err == nil {
					t.Errorf("%s() error = nil, want the refusal", method)

					continue
				}
				if !strings.Contains(err.Error(), "error="+tt.wantRefuse) {
					t.Errorf("%s() error = %v, want the refusal %q", method, err, tt.wantRefuse)
				}
			}
		})
	}
}
