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

// TestBindAuth pins what a request carries of the auth it came through: the name its
// group bound, and nothing where the name is empty or no group bound one.
func TestBindAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  func(t *testing.T) context.Context
		want string
	}{
		{
			name: "a request a group bound carries the auth's name",
			ctx: func(t *testing.T) context.Context {
				return boundThrough(t, context.Background(), "staff")
			},
			want: "staff",
		},
		{
			name: "an empty name binds nothing",
			ctx: func(t *testing.T) context.Context {
				return boundThrough(t, context.Background(), "")
			},
			want: "",
		},
		{
			name: "a context no group bound carries no auth",
			ctx: func(*testing.T) context.Context {
				return context.Background()
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := BoundAuth(tt.ctx(t)); got != tt.want {
				t.Errorf("BoundAuth() = %q, want %q", got, tt.want)
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
