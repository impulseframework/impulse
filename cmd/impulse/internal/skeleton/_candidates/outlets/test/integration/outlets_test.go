package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// portalPage is the browser page a portal login returns to.
const portalPage = "/portal/dashboard"

// TestPortalOutlet drives the portal the way its browser application does: sign in
// through the directory under the portal prefix, read the portal's tenant list and
// digest, list the portal's member resource, confirm a console-only resource is not on
// the portal at all, and confirm the member's session is a stranger to the console.
//
// The client was given the role ahead of this first sign-in. A role is held by an
// account, so the client is a user without a password, created under the name the
// directory presents (the harness), and the sign-in links the directory's identity to
// that account: the session is the account's and holds its role.
func TestPortalOutlet(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	b := newBrowser(t, s, portalAPI)

	if status, body := b.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before login: status %d: %s", status, body)
	}
	if n := identityLinks(ctx, t, s, s.clientID); n != 0 {
		t.Fatalf("the client's account holds %d identity links before the first sign-in, want none", n)
	}
	// The deployment administers the directory's tenant (the harness declares it), so
	// the sign-in carries the tenant's second factor: the callback opens the session and
	// sends the browser to the page, asking for no sign-in code.
	if status, location := b.loginPortal(ctx, clientUser); status != http.StatusFound || location != portalPage {
		t.Fatalf("portal login: status %d to %q, want %d to %q", status, location, http.StatusFound, portalPage)
	}
	status, body := b.do(ctx, http.MethodGet, portalAPI+"/user/session", nil)
	if status != http.StatusOK {
		t.Fatalf("GET session after login: status %d: %s", status, body)
	}
	var sess struct {
		Authenticated bool   `json:"authenticated"`
		Username      string `json:"username"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatal(err)
	}
	if !sess.Authenticated || sess.Username != clientUser {
		t.Fatalf("session after login = %+v, want authenticated as %s", sess, clientUser)
	}
	// The first sign-in linked the directory's identity to the account the role was given
	// to, which the cases below then act as: north is the account's tenant.
	if n := identityLinks(ctx, t, s, s.clientID); n != 1 {
		t.Fatalf("the client's account holds %d identity links after the first sign-in, want the directory's one", n)
	}

	tests := []struct {
		name   string
		target string
		want   int
	}{
		{name: "the portal serves the session's tenants", target: portalAPI + "/user-domains", want: http.StatusOK},
		{name: "the portal serves the tenant digest", target: portalAPI + "/permission-digest?domain=" + north, want: http.StatusOK},
		{name: "announcements are a portal member", target: portalAPI + "/tenants/" + north + "/announcements", want: http.StatusOK},
		{name: "the tenant record is console-only", target: portalAPI + "/tenants", want: http.StatusNotFound},
		{name: "readings are devices-only", target: portalAPI + "/tenants/" + north + "/readings", want: http.StatusNotFound},
		{name: "a member's session is a stranger to the console", target: consoleAPI + "/tenants/" + north + "/announcements", want: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := b.do(ctx, http.MethodGet, tt.target, nil)
			if status != tt.want {
				t.Errorf("GET %s: status %d, want %d: %s", tt.target, status, tt.want, body)
			}
		})
	}
}

// TestPortalOutlet_aRoleUnderANameAdmitsNobody proves a role is held by an account and
// by nothing else. A membership written under the name a person will sign in with, for
// a person no user was created for, is held by nobody: the directory proves who they
// are, the tenants that declare the directory find a stranger, and since the seeded
// declarations name no default seat the stranger is refused by invitation only, with no
// account made. The portal's client, who has the account the harness created, is
// admitted on the same stack (TestPortalOutlet).
func TestPortalOutlet_aRoleUnderANameAdmitsNobody(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	const stranger = "stranger"
	if err := s.members.UserManager().AddUserRoles(ctx, accesstypes.DomainPolicyScope(north), stranger, "TenantAdministrator"); err != nil {
		t.Fatalf("AddUserRoles(%s, north) in the members store error = %v", stranger, err)
	}

	b := newBrowser(t, s, portalAPI)
	if status, body := b.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before login: status %d: %s", status, body)
	}
	if status, location := b.loginPortal(ctx, stranger); status != http.StatusFound || !strings.Contains(location, "code="+string(sessioninfo.RefusedInvitationOnly)) {
		t.Errorf("a directory sign-in under a name that holds a membership and no account: status %d to %q, want %d to the login page naming %s", status, location, http.StatusFound, sessioninfo.RefusedInvitationOnly)
	}
	if status, body := b.do(ctx, http.MethodGet, portalAPI+"/user-domains", nil); status != http.StatusUnauthorized {
		t.Errorf("GET user-domains after the refused sign-in: status %d, want 401: %s", status, body)
	}
	if accountExists(ctx, t, s, membersUsers, stranger) {
		t.Error("the refused sign-in made an account, want none: the tenants admit a stranger by invitation only")
	}
}

// TestConsoleSessionOnPortal signs a staff login in on the console and confirms its
// session opens nothing on the portal: the two outlets bind to two auths.
func TestConsoleSessionOnPortal(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	b := newBrowser(t, s, consoleAPI)
	b.signIn(ctx, adminUser)

	tests := []struct {
		name   string
		target string
		want   int
	}{
		{name: "the console serves the staff login", target: consoleAPI + "/tenants/" + north + "/announcements", want: http.StatusOK},
		{name: "the staff session is a stranger to the portal", target: portalAPI + "/tenants/" + north + "/announcements", want: http.StatusUnauthorized},
		{name: "the portal's session route sees no one", target: portalAPI + "/user-domains", want: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := b.do(ctx, http.MethodGet, tt.target, nil)
			if status != tt.want {
				t.Errorf("GET %s: status %d, want %d: %s", tt.target, status, tt.want, body)
			}
		})
	}
}

// TestDevicesOutlet drives the devices outlet: a staff login issues an application
// token for it on the console, and the token binds each request to that person, who
// then goes through the same tenancy and permission checks as a browser; without a
// token the staff auth issued the surface is closed.
func TestDevicesOutlet(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	token := issueToken(ctx, t, s, memberUser)

	tests := []struct {
		name   string
		token  string
		target string
		want   int
	}{
		{name: "the token opens a tenant its person holds a grant in", token: token, target: "/devices/tenants/" + north + "/readings", want: http.StatusOK},
		{name: "a tenant without a foothold is concealed", token: token, target: "/devices/tenants/" + south + "/readings", want: http.StatusNotFound},
		{name: "announcements are not on the devices outlet", token: token, target: "/devices/tenants/" + north + "/announcements", want: http.StatusNotFound},
		{name: "a token the staff auth never issued is refused", token: "not-a-token", target: "/devices/tenants/" + north + "/readings", want: http.StatusUnauthorized},
		{name: "no token is refused", token: "", target: "/devices/tenants/" + north + "/readings", want: http.StatusUnauthorized},
		{name: "readings are not on the console outlet", token: token, target: consoleAPI + "/tenants/" + north + "/readings", want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := machine(ctx, t, s, tt.token, http.MethodGet, tt.target)
			if status != tt.want {
				t.Errorf("GET %s: status %d, want %d: %s", tt.target, status, tt.want, body)
			}
		})
	}
}

// TestDevicesOutletDeviceAuthorization starts a command-line client's device authorization
// on the devices outlet and confirms where it sends the person: the console's device
// page (staff.Settings.DevicePage), with the code the person types already in the query,
// which the console's device page reads to look the request up.
func TestDevicesOutletDeviceAuthorization(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.server.URL+"/devices/oauth/device/authorize", strings.NewReader(`{"client_id":"suite","outlet":"devices"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST oauth/device/authorize: status %d: %s", resp.StatusCode, body)
	}
	// The device flow's answers are the RFC's, snake_case: read as a map.
	var started map[string]any
	if err := json.Unmarshal(body, &started); err != nil {
		t.Fatalf("decoding the device authorization: %v: %s", err, body)
	}
	userCode, _ := started["user_code"].(string)
	if userCode == "" {
		t.Fatalf("device authorization = %s, want a user code", body)
	}
	if got, _ := started["verification_uri"].(string); got != "/console/device" {
		t.Errorf("verification_uri = %q, want the console's device page /console/device", got)
	}
	if got, _ := started["verification_uri_complete"].(string); got != "/console/device?userCode="+userCode {
		t.Errorf("verification_uri_complete = %q, want /console/device?userCode=%s", got, userCode)
	}
}

// identityLinks counts the identity links the members auth holds for the account, named
// by its id: the directory identities whose sign-ins are the account's.
func identityLinks(ctx context.Context, t *testing.T, s *served, account string) int64 {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, spanner.Statement{
		SQL:    "SELECT COUNT(*) FROM MembersSessionIdentities WHERE UserId = @account",
		Params: map[string]any{"account": account},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the identity links of the account %s: %v", account, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of the identity links of the account %s: %v", account, err)
	}

	return n
}

// issueToken signs the staff login in on the console and issues an application token
// for the devices outlet, as a person does on the console's token page.
func issueToken(ctx context.Context, t *testing.T, s *served, user string) string {
	t.Helper()

	b := newBrowser(t, s, consoleAPI)
	b.signIn(ctx, user)
	status, body := b.do(ctx, http.MethodPost, consoleAPI+"/user/tokens", []byte(`{"name":"suite","outlet":"devices"}`))
	if status != http.StatusOK {
		t.Fatalf("POST user/tokens: status %d: %s", status, body)
	}
	var issued struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &issued); err != nil {
		t.Fatalf("decoding the issued token: %v: %s", err, body)
	}
	if issued.Token == "" {
		t.Fatalf("the issued token has no secret: %s", body)
	}

	return issued.Token
}
