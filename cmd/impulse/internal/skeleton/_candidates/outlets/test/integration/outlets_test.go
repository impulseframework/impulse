package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
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
	// The directory vouches for no address and the client's account holds none, so the
	// first sign-in waits for the code that confirms the address the person gives
	// (loginPortal answers it) and then goes on to the page.
	if status, location := b.loginPortal(ctx, clientUser); status != http.StatusFound || location != portalPage {
		t.Fatalf("portal login: status %d to %q, want %d to %q", status, location, http.StatusFound, portalPage)
	}
	if !addressConfirmed(ctx, t, s, s.clientID, factorAddress(clientUser)) {
		t.Fatalf("the client's account does not hold %s as its confirmed address after the first sign-in: the code confirms the address the person gave", factorAddress(clientUser))
	}
	// The deployment administers the directory's tenant (the harness declares it), so
	// a sign-in carries the tenant's second factor: once the account holds its confirmed
	// address the callback opens the session and sends the browser to the page, asking
	// for no sign-in code and mailing nothing.
	mailed := len(s.membersMail.Messages())
	later := newBrowser(t, s, portalAPI)
	if status, body := later.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the second login: status %d: %s", status, body)
	}
	if status, location := later.loginPortal(ctx, clientUser); status != http.StatusFound || location != portalPage {
		t.Fatalf("the client's second portal login: status %d to %q, want %d straight to %q", status, location, http.StatusFound, portalPage)
	}
	if n := len(s.membersMail.Messages()); n != mailed {
		t.Fatalf("the client's second portal login mailed %d messages, want none: the address is confirmed and the tenant's own second factor is carried", n-mailed)
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
// are, the tenant whose directory it is finds a stranger, and since the seeded
// directory names no default role the stranger is refused by invitation only, with no
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

// TestPortalOutlet_theFirstAdministratorIsInvited proves the members auth's foothold as
// far as the simulated directory shows it: the deployment invites the first
// administrator, makes no account ahead of them, and nobody else gets in. The members
// auth's people sign in through a directory and the application gives its roles, so the
// release has an invitation mailed to the address the placement names, while nobody of
// the auth holds the Super Administrator role: the portal's client, an account that
// holds no such role, stops nothing. The application's tenants have directories, and the
// first administrator belongs to no tenant, so no tenant's directory vouches for them:
// the invitation does. Another person of the same directory who signs in first is a
// stranger without the link, and with it the invitation is for another address; neither
// makes an account, and the invitation stands. An account somebody created ahead under
// a name, for the whole application, is nobody's invitation either, and its name links
// nothing; holding the Super Administrator role, it is somebody who administers the
// auth, so from then on a release invites nobody and a fresh invitation is refused.
//
// The acceptance itself is not played here. An invitation is accepted by the account
// whose address it was mailed to, and the simulated Microsoft directory asserts no
// address for anyone, so nobody accepts an invitation through it. The invited person's
// first sign-in through a directory, linked and holding the roles, is proven where the
// directory asserts an address: the tenanted skeleton's google registration, and the
// session library's own suites on both databases.
func TestPortalOutlet_theFirstAdministratorIsInvited(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	const administrator = "first.administrator@north.example"
	s := newServedInviting(ctx, t, administrator)

	// The release mailed one invitation and made no account.
	if sent := s.membersMail.Messages(); len(sent) != 1 || sent[0].To != administrator {
		t.Fatalf("the release mailed %d message(s), want one, to %s: a message's text holds its link and is not printed", len(sent), administrator)
	}
	if accountExists(ctx, t, s, membersUsers, administrator) {
		t.Fatal("the release made an account for the members auth's first administrator, want none ahead of the person")
	}

	// Another person of the directory signs in first, with no link: a stranger.
	const other = "someone.else@north.example"
	stranger := newBrowser(t, s, portalAPI)
	if status, body := stranger.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before login: status %d: %s", status, body)
	}
	if status, location := stranger.loginPortal(ctx, other); status != http.StatusFound || !strings.Contains(location, "code="+string(sessioninfo.RefusedInvitationOnly)) {
		t.Errorf("another person of the directory signing in first, with no link: status %d to %q, want %d to the login page naming %s", status, location, http.StatusFound, sessioninfo.RefusedInvitationOnly)
	}
	// The same person with the link: the invitation is for another address.
	stranger = newBrowser(t, s, portalAPI)
	if status, body := stranger.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before login: status %d: %s", status, body)
	}
	if status, body := stranger.do(ctx, http.MethodPost, portalAPI+"/user/invitation", []byte(`{"token":"`+mailedMembersInvitation(t, s, administrator)+`"}`)); status != http.StatusOK {
		t.Fatalf("opening the invitation as another person: status %d: %s", status, body)
	}
	if status, location := stranger.loginPortal(ctx, other); status != http.StatusFound || !strings.Contains(location, "code="+string(sessioninfo.RefusedInvitationAddress)) {
		t.Errorf("another person of the directory accepting the first administrator's invitation: status %d to %q, want %d to the login page naming %s", status, location, http.StatusFound, sessioninfo.RefusedInvitationAddress)
	}
	if accountExists(ctx, t, s, membersUsers, other) {
		t.Error("an account was made for the person the invitation was not for")
	}
	if standing, err := s.membersAuth.Session().API().Invitations(ctx); err != nil || len(standing) != 1 || standing[0].Email != administrator {
		t.Errorf("the invitations that stand after the refused sign-ins = %+v, %v, want the first administrator's alone", standing, err)
	}

	// The next release finds an account in the members auth, the portal's client, who
	// holds no Super Administrator role: nobody administers the auth, so the first
	// administrator is invited still, by the invitation that stands, and nothing is
	// mailed.
	if !accountExists(ctx, t, s, membersUsers, clientUser) {
		t.Fatalf("the members auth has no account for %s, want the portal's client there", clientUser)
	}
	again, err := s.membersAuth.InviteAdministrator(ctx, administrator)
	if err != nil || again.Outcome != session.FirstAdministratorStanding || again.Email != administrator {
		t.Errorf("the release beside an account that holds no role = %+v, %v, want the first administrator's invitation left standing", again, err)
	}
	if sent := len(s.membersMail.Messages()); sent != 1 {
		t.Errorf("%d message(s) mailed in all, want the first release's one", sent)
	}

	// An account an administrator created ahead under a name, with a role for the whole
	// application and no password, is nobody's invitation. The directory presents the
	// name and nothing vouches for the account, so the sign-in is refused as a stranger's
	// is and links nothing.
	const createdAhead = "created.ahead@north.example"
	ahead, err := s.membersAuth.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: createdAhead})
	if err != nil {
		t.Fatalf("CreateSessionUser(%s) in the members auth error = %v", createdAhead, err)
	}
	if err := s.members.UserManager().AddUserRoles(ctx, accesstypes.GlobalPolicyScope(), accesstypes.User(ahead.String()), accesstypes.SuperAdministrator); err != nil {
		t.Fatalf("AddUserRoles(%s) in the members store error = %v", createdAhead, err)
	}
	aheadBrowser := newBrowser(t, s, portalAPI)
	if status, body := aheadBrowser.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before login: status %d: %s", status, body)
	}
	if status, location := aheadBrowser.loginPortal(ctx, createdAhead); status != http.StatusFound || !strings.Contains(location, "code="+string(sessioninfo.RefusedInvitationOnly)) {
		t.Errorf("a directory sign-in under the name of an account created ahead for the whole application: status %d to %q, want %d to the login page naming %s", status, location, http.StatusFound, sessioninfo.RefusedInvitationOnly)
	}
	if n := identityLinks(ctx, t, s, ahead.String()); n != 0 {
		t.Errorf("the account created ahead holds %d identity links after the refused sign-in, want none", n)
	}

	// That account holds the Super Administrator role, so somebody administers the
	// members auth: the next release invites nobody, and a fresh invitation is refused.
	// The invitation that stands for the address still named is left to be accepted.
	held, err := s.membersAuth.InviteAdministrator(ctx, administrator)
	if err != nil || held.Outcome != session.FirstAdministratorRoleHeld || len(held.Withdrawn) != 0 {
		t.Errorf("the release once an account holds the Super Administrator role = %+v, %v, want nobody invited and nothing withdrawn", held, err)
	}
	if _, err := s.membersAuth.ReinviteAdministrator(ctx, administrator); !errors.Is(err, session.ErrFirstAdministratorRoleHeld) {
		t.Errorf("members.Auth.ReinviteAdministrator() once an account holds the Super Administrator role: error = %v, want %v", err, session.ErrFirstAdministratorRoleHeld)
	}
	if sent := len(s.membersMail.Messages()); sent != 1 {
		t.Errorf("%d message(s) mailed in all, want the first release's one", sent)
	}
}

// mailedMembersInvitation reads the token of the invitation link the members auth mailed
// to the address, which opens the portal's login page.
func mailedMembersInvitation(t *testing.T, s *served, email string) string {
	t.Helper()

	link := regexp.MustCompile(`http://outlets\.test/portal/login\?invitation=([A-Za-z0-9_-]+)`)
	for _, m := range s.membersMail.Messages() {
		if m.To != email {
			continue
		}
		if match := link.FindStringSubmatch(m.Text); match != nil {
			return match[1]
		}
	}
	t.Fatalf("no invitation link was mailed to %s by the members auth (%d messages)", email, len(s.membersMail.Messages()))

	return ""
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

// addressConfirmed reports whether the members auth holds address for the account as
// its confirmed one.
func addressConfirmed(ctx context.Context, t *testing.T, s *served, account, address string) bool {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, spanner.Statement{
		SQL:    "SELECT COUNT(*) FROM " + membersUsers + " WHERE Id = @account AND Email = @address AND EmailVerifiedAt IS NOT NULL",
		Params: map[string]any{"account": account, "address": address},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("reading the confirmed address of the account %s: %v", account, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the confirmed address of the account %s: %v", account, err)
	}

	return n > 0
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
