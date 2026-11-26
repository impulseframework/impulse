package session

import (
	"context"
	"net/http"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_SyncRolesIsToldTheDirectory pins what a sign-in tells the permission store
// about the role changes it asks for: SyncRoles is handed a context that carries the
// directory's sync (auditlog.DirectorySyncFrom), naming the directory the identity came
// through as the registrations name it, by the method's own name where nothing names
// it, the account by its id and its username at the sign-in, and each tenant by the name
// the Auth gives it.
func TestAuth_SyncRolesIsToldTheDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// connectionName is the name the registration gives the identity's connection,
		// nil for none.
		connectionName func(string) string
		wantDirectory  string
	}{
		{name: "a directory the registration names", connectionName: func(string) string { return "Acme" }, wantDirectory: "Acme"},
		{name: "a directory nothing names", wantDirectory: "bare"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			userID := impulse.Must(impulse.NewUUID())
			f := newAuthFixtureWithoutPassword(t, gomock.NewController(t))
			f.auth.SetTenantNames(namedTenants{accesstypes.Domain("acme"): "Acme Freight"})
			var (
				told   auditlog.DirectorySync
				carry  bool
				synced accesstypes.User
			)
			h := f.auth.RegisterExternal(&signin.Registration[mockTx]{
				Name: "provider", Method: "bare",
				Build: func(signin.StateCookies) (*signin.Method, error) {
					return &signin.Method{
						Name:           "bare",
						LoginURL:       func() string { return "/login" },
						ConnectionName: tt.connectionName,
						Complete: func(context.Context, http.ResponseWriter, *http.Request) (*signin.Verified, error) {
							return &signin.Verified{
								Identity:  &sessioninfo.Identity{Method: "bare", Connection: "c", Subject: "s", SignInName: "pat"},
								RoleNames: []string{"Editor"},
								ReturnURL: "/next",
							}, nil
						},
						SyncRoles: func(ctx context.Context, user accesstypes.User, _ []string) error {
							told, carry = auditlog.DirectorySyncFrom(ctx)
							synced = user

							return nil
						},
					}, nil
				},
			})
			f.expectSignIn(impulse.Must(impulse.NewUUID()), nil, func(req *sessioninfo.NewSessionRequest) error {
				req.UserID, req.Username = userID, "pat.lee"

				return nil
			})

			rr := f.serve(h.Callback, http.MethodGet, "/provider/callback?code=c&state=s", nil, nil)

			if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/next" {
				t.Fatalf("response = %d %q, want 302 /next", rr.Code, rr.Header().Get("Location"))
			}
			if synced != accesstypes.User(userID.String()) || !carry {
				t.Fatalf("SyncRoles() was handed %q and a context that carries a directory's sync %v, want %s and true", synced, carry, userID)
			}
			if told.Directory != tt.wantDirectory || told.UserID != userID.String() || told.Username != "pat.lee" {
				t.Errorf("the sync names the directory %q and the account %q (%q), want %q and %s (pat.lee)", told.Directory, told.UserID, told.Username, tt.wantDirectory, userID)
			}
			if told.TenantName == nil {
				t.Fatalf("the sync names no tenant, want the Auth's names")
			}
			if got := told.TenantName("acme"); got != "Acme Freight" {
				t.Errorf("the sync names the tenant acme %q, want Acme Freight", got)
			}
		})
	}
}
