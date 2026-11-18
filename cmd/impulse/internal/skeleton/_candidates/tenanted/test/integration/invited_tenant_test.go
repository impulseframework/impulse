package integration

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
)

const (
	// notCreatedDirectly is what the Tenant record's create answers, whoever asks.
	notCreatedDirectly = "a tenant is not created directly: invite one person, and the tenant is made when that person first signs in"
	// invitedPassword is the password an invited person signs up with.
	invitedPassword = "a-long-password"
)

// sentInvitation is what the method that sends an invitation answers: the invitation's
// id and whether its message went.
type sentInvitation struct {
	ID   string `json:"id"`
	Sent bool   `json:"sent"`
}

// inviteTenant sends the invitation that makes a tenant, as the browser's person, and
// answers the status, the body and, when it was sent, the invitation.
func inviteTenant(ctx context.Context, t *testing.T, b *browser, value map[string]any) (status int, body []byte, sent sentInvitation) {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	status, body = b.do(ctx, http.MethodPost, "/api/invite-staff-tenant", encoded)
	if status == http.StatusOK {
		if err := json.Unmarshal(body, &sent); err != nil || sent.ID == "" || !sent.Sent {
			t.Fatalf("invite-staff-tenant answered %s (%v), want the invitation's id and the word that its message went", body, err)
		}
	}

	return status, body, sent
}

// mailedInvitation reads the token of the last invitation link the staff auth mailed to
// the address.
func mailedInvitation(t *testing.T, s *served, email string) string {
	t.Helper()

	link := regexp.MustCompile(`http://tenanted\.test/login\?invitation=([A-Za-z0-9_-]+)`)
	messages := s.mail.Messages()
	for _, m := range slices.Backward(messages) {
		if !strings.Contains(m.To, email) {
			continue
		}
		if match := link.FindStringSubmatch(m.Text); match != nil {
			return match[1]
		}
	}
	t.Fatalf("no invitation link was mailed to %s (%d messages)", email, len(messages))

	return ""
}

// showInvitation follows a mailed link as a person not signed in does: the browser takes
// its XSRF token, then shows the invitation page what the link is. It answers the status
// and the page as the server describes it, or the refusal's body under "refusal".
func showInvitation(ctx context.Context, t *testing.T, b *browser, token string) (status int, page map[string]any) {
	t.Helper()

	if status, body := b.do(ctx, http.MethodGet, "/api/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET /api/user/session before the invitation: status %d: %s", status, body)
	}
	status, body := b.do(ctx, http.MethodPost, "/api/user/invitation", []byte(`{"token":"`+token+`"}`))
	if status != http.StatusOK {
		return status, map[string]any{"refusal": string(body)}
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("the invitation page's answer is not an object: %v: %s", err, body)
	}

	return status, page
}

// listed reads a list route as the browser's person and answers its rows.
func listed(ctx context.Context, t *testing.T, b *browser, path string) []map[string]any {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, status, body)
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("GET %s is not a list: %v: %s", path, err, body)
	}

	return rows
}

// invitedTenant answers the standing invitation's row of the list of the invitations
// that make a tenant, nil when the list does not hold it.
func invitedTenant(ctx context.Context, t *testing.T, b *browser, id string) map[string]any {
	t.Helper()

	for _, row := range listed(ctx, t, b, "/api/staff-invited-tenants?limit=200") {
		if row["id"] == id {
			return row
		}
	}

	return nil
}

// tenantNamed answers the tenant's row as the browser's person lists the Tenant record,
// nil when no tenant has the name.
func tenantNamed(ctx context.Context, t *testing.T, b *browser, name string) map[string]any {
	t.Helper()

	for _, row := range listed(ctx, t, b, "/api/tenants?limit=200") {
		if row["name"] == name {
			return row
		}
	}

	return nil
}

// makeTenant makes a tenant the way one comes to exist, for a suite that needs a tenant
// of its own: the administrator sends the invitation with the tenant's name, the invited
// person follows the mailed link, signs up with a password and completes the second
// factor. It answers the new tenant's id and the browser of its founder, who is its
// first administrator and signs in as username.
func makeTenant(ctx context.Context, t *testing.T, s *served, admin *browser, name, username string) (tenant string, founder *browser) {
	t.Helper()

	email := username + "@" + strings.ToLower(name) + ".example"
	if status, body, _ := inviteTenant(ctx, t, admin, map[string]any{"email": email, "tenantName": name}); status != http.StatusOK {
		t.Fatalf("inviting the person who makes %s: status %d: %s", name, status, body)
	}
	founder = newBrowser(t, s)
	if status, page := showInvitation(ctx, t, founder, mailedInvitation(t, s, email)); status != http.StatusOK {
		t.Fatalf("opening the invitation that makes %s: status %d: %v", name, status, page)
	}
	accept, err := json.Marshal(map[string]string{"username": username, "password": invitedPassword})
	if err != nil {
		t.Fatal(err)
	}
	status, body := founder.do(ctx, http.MethodPost, "/api/user/invitation/sign-up", accept)
	if status != http.StatusOK {
		t.Fatalf("signing up at the invitation that makes %s: status %d: %s", name, status, body)
	}
	if heldForFactor(t, body) {
		founder.secondFactor(ctx, username)
	}
	made := tenantNamed(ctx, t, admin, name)
	if made == nil {
		t.Fatalf("no tenant %s after its invitation was accepted", name)
	}
	id, ok := made["id"].(string)
	if !ok {
		t.Fatalf("the tenant %s as listed = %v, want its id", name, made)
	}

	return id, founder
}

// TestInvitedTenant_theFirstSignInMakesTheTenant proves how a tenant comes to exist: an
// administrator brings an organization in by inviting one person, and that person's
// first sign-in makes the tenant. Nobody creates a tenant directly: the record's create
// answers 400 with how one is made, whoever asks.
//
// Sending the invitation takes the method's grant and the Create grant on the Tenant
// record (the Administrator role's), and the tenant's name is held to the record's own
// create when it is sent: a name a tenant holds is refused there, where the sender can
// give another. While the invitation waits no tenant exists; the list of the invitations
// that stand shows it, and the invitation page shows the invited person the tenant's
// name and their address. Accepted with a password, the tenant exists under an id the
// acceptance generated, with the invitation's name and no directory, the person is its
// administrator (TenantAdministrator, the role TenantAdministratorRoles names), and it is
// served under its segment at once: the acceptance's commit updated the roster. The
// accepted invitation is no longer in the list and cannot be withdrawn. The name is a
// field like any other from then on: changed, it is the name every tenant list shows.
func TestInvitedTenant_theFirstSignInMakesTheTenant(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)
	member := signedIn(ctx, t, s, memberUser)
	tenants := func() []string {
		return slices.Sorted(maps.Values(tenantNames(ctx, t, admin)))
	}

	// Nobody creates a tenant directly: the create answers 400 saying how a tenant is
	// made, for the administrator who holds Create on the record and for the member who
	// holds nothing on it alike, with or without a key in the path, and writes nothing.
	for name, b := range map[string]*browser{"the administrator": admin, "the member": member} {
		for _, add := range []string{
			`[{"op":"add","path":"/tenants","value":{"name":"East"}}]`,
			`[{"op":"add","path":"/tenants/east","value":{"name":"East"}}]`,
		} {
			status, body := b.do(ctx, http.MethodPatch, "/api/resources", []byte(add))
			if status != http.StatusBadRequest || !strings.Contains(string(body), notCreatedDirectly) {
				t.Errorf("%s by %s: status %d, want 400 saying how a tenant is made: %s", add, name, status, body)
			}
		}
	}
	if got := tenants(); !slices.Equal(got, []string{"North", "South"}) {
		t.Fatalf("the tenants after the refused creates = %v, want North and South alone", got)
	}

	// Sending: the member holds neither grant; the tenant's name is required, and a
	// name a tenant holds is refused when the invitation is sent. Nothing is mailed.
	east := map[string]any{"email": "founder@east.example", "name": "East Founder", "tenantName": "East"}
	for name, tt := range map[string]struct {
		by       *browser
		body     map[string]any
		want     int
		wantBody string
	}{
		"by the member":             {by: member, body: east, want: http.StatusForbidden},
		"with no name for a tenant": {by: admin, body: map[string]any{"email": "founder@east.example"}, want: http.StatusBadRequest},
		"with a name a tenant holds": {
			by: admin, body: map[string]any{"email": "founder@east.example", "tenantName": "North"},
			want: http.StatusConflict, wantBody: "a tenant with the name North already exists: give the new one another",
		},
	} {
		if status, body, _ := inviteTenant(ctx, t, tt.by, tt.body); status != tt.want || !strings.Contains(string(body), tt.wantBody) {
			t.Errorf("an invitation %s: status %d, want %d saying %q: %s", name, status, tt.want, tt.wantBody, body)
		}
	}
	for _, m := range s.mail.Messages() {
		if strings.Contains(m.To, "east.example") {
			t.Fatalf("a refused invitation mailed %s: %q", m.To, m.Subject)
		}
	}

	// The administrator's invitation. No tenant exists until the first sign-in; the
	// list of the invitations that stand shows it, and is not the member's to read.
	status, body, sent := inviteTenant(ctx, t, admin, east)
	if status != http.StatusOK {
		t.Fatalf("the administrator's invitation: status %d: %s", status, body)
	}
	if got := tenants(); !slices.Equal(got, []string{"North", "South"}) {
		t.Fatalf("the tenants once the invitation is sent = %v, want North and South alone: no tenant exists until the first sign-in", got)
	}
	row := invitedTenant(ctx, t, admin, sent.ID)
	if row == nil || row["email"] != "founder@east.example" || row["tenantName"] != "East" || row["tenantDirectoryOnly"] != false {
		t.Errorf("the invitation as the administrator lists it = %v, want East for founder@east.example, not directory only", row)
	}
	if status, body := member.do(ctx, http.MethodGet, "/api/staff-invited-tenants", nil); status != http.StatusForbidden {
		t.Errorf("the member's list of invited tenants: status %d, want 403: %s", status, body)
	}

	// The invitation page, before the sign-in: the tenant's name and the invited
	// address, open to a password.
	founder := newBrowser(t, s)
	status, page := showInvitation(ctx, t, founder, mailedInvitation(t, s, "founder@east.example"))
	if status != http.StatusOK || page["newTenant"] != true || page["tenantName"] != "East" || page["email"] != "founder@east.example" || page["directoryOnly"] != false || page["signUp"] != true {
		t.Fatalf("the invitation page: status %d: %v, want a new tenant East for founder@east.example, open to a password", status, page)
	}

	// Accepted with a password: the tenant exists with the invitation's name and no
	// directory, under an id the acceptance generated, and the person is its
	// administrator.
	status, body = founder.do(ctx, http.MethodPost, "/api/user/invitation/sign-up", []byte(`{"username":"founder","password":"`+invitedPassword+`"}`))
	if status != http.StatusOK {
		t.Fatalf("signing up at the invitation: status %d: %s", status, body)
	}
	if heldForFactor(t, body) {
		founder.secondFactor(ctx, "founder")
	}
	made := tenantNamed(ctx, t, admin, "East")
	if made == nil {
		t.Fatalf("the tenants after the acceptance = %v, want East among them", tenants())
	}
	eastID, _ := made["id"].(string)
	if _, err := impulse.UUIDFromString(eastID); err != nil {
		t.Fatalf("the new tenant's key is %q, want an id the acceptance generated: %v", eastID, err)
	}
	if made["directoryOnly"] != false {
		t.Errorf("East as made = %v, want no directory-only switch", made)
	}
	if rows := listed(ctx, t, admin, "/api/tenants/"+eastID+"/tenant-directories"); len(rows) != 0 {
		t.Errorf("East's directory = %v, want none: the founder signed up with a password", rows)
	}
	if roles := tenantRolesOf(ctx, t, admin, eastID, "founder"); !slices.Equal(roles, []string{"TenantAdministrator"}) {
		t.Errorf("the founder's roles in East = %v, want TenantAdministrator alone: the acceptance seats the person", roles)
	}
	if domains := userDomains(ctx, t, founder); !slices.Equal(domains, []string{eastID}) {
		t.Errorf("the founder's tenants = %v, want East (%s) alone", domains, eastID)
	}

	// Served at once under its segment, for its founder and for the administrator, who
	// holds the tenant's role in every tenant, and concealed from the member, who holds
	// nothing there. The administrator's tenant list names it.
	eastSegment := "/api/tenants/" + eastID + "/announcements"
	for name, read := range map[string]struct {
		browser *browser
		want    int
	}{
		"the founder":       {browser: founder, want: http.StatusOK},
		"the administrator": {browser: admin, want: http.StatusOK},
		"the member":        {browser: member, want: http.StatusNotFound},
	} {
		if status, body := read.browser.do(ctx, http.MethodGet, eastSegment, nil); status != read.want {
			t.Errorf("GET %s as %s: status %d, want %d: %s", eastSegment, name, status, read.want, body)
		}
	}
	if name := tenantNames(ctx, t, admin)[eastID]; name != "East" {
		t.Errorf("the administrator's tenant list names the new tenant %q, want East", name)
	}

	// The accepted invitation is no longer in the list, and is not withdrawn.
	if row := invitedTenant(ctx, t, admin, sent.ID); row != nil {
		t.Errorf("the list of invited tenants still holds the accepted invitation: %v", row)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/withdraw-staff-invited-tenant", []byte(`{"invitationId":"`+sent.ID+`"}`)); status != http.StatusNotFound {
		t.Errorf("withdrawing an accepted invitation: status %d, want 404: %s", status, body)
	}

	// The name is a field like any other: changed, it is the name every tenant list
	// shows at once, and the tenant's key and segment are what they were.
	rename := `[{"op":"patch","path":"/tenants/` + eastID + `","value":{"name":"Far East"}}]`
	if status, body := admin.do(ctx, http.MethodPatch, "/api/resources", []byte(rename)); status != http.StatusOK {
		t.Fatalf("renaming the tenant: status %d: %s", status, body)
	}
	if name := tenantNames(ctx, t, admin)[eastID]; name != "Far East" {
		t.Errorf("the administrator's tenant list names the renamed tenant %q, want Far East", name)
	}
	if status, body := founder.do(ctx, http.MethodGet, eastSegment, nil); status != http.StatusOK {
		t.Errorf("GET %s after the rename: status %d, want 200: %s", eastSegment, status, body)
	}
}

// TestInvitedTenant_withdrawnMakesNothing proves an invitation that makes a tenant is
// withdrawn while it waits: the list of the invitations that stand holds it until then
// and not after, the mailed link is dead from then on, no tenant is made, and a second
// withdrawal finds nothing to withdraw.
func TestInvitedTenant_withdrawnMakesNothing(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := signedIn(ctx, t, s, adminUser)

	status, body, sent := inviteTenant(ctx, t, admin, map[string]any{"email": "founder@west.example", "tenantName": "West"})
	if status != http.StatusOK {
		t.Fatalf("the administrator's invitation: status %d: %s", status, body)
	}
	token := mailedInvitation(t, s, "founder@west.example")
	if row := invitedTenant(ctx, t, admin, sent.ID); row == nil {
		t.Fatalf("the list of invited tenants does not hold the invitation %s", sent.ID)
	}

	withdraw := []byte(`{"invitationId":"` + sent.ID + `"}`)
	if status, body := admin.do(ctx, http.MethodPost, "/api/withdraw-staff-invited-tenant", withdraw); status != http.StatusNoContent {
		t.Fatalf("withdrawing the invitation: status %d, want 204: %s", status, body)
	}
	if row := invitedTenant(ctx, t, admin, sent.ID); row != nil {
		t.Errorf("the list of invited tenants still holds the withdrawn invitation: %v", row)
	}

	// The link is dead: the invitation page is refused, and a sign-up through it makes
	// no account and no tenant.
	founder := newBrowser(t, s)
	if status, page := showInvitation(ctx, t, founder, token); status == http.StatusOK {
		t.Errorf("opening the withdrawn invitation: status %d: %v, want it refused", status, page)
	}
	if status, body := founder.do(ctx, http.MethodPost, "/api/user/invitation/sign-up", []byte(`{"username":"founder","password":"`+invitedPassword+`"}`)); status == http.StatusOK {
		t.Errorf("signing up at the withdrawn invitation: status %d, want it refused: %s", status, body)
	}
	if accountExists(ctx, t, s, "founder") {
		t.Error("the withdrawn invitation made an account")
	}
	if made := tenantNamed(ctx, t, admin, "West"); made != nil {
		t.Errorf("the withdrawn invitation made a tenant: %v", made)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/withdraw-staff-invited-tenant", withdraw); status != http.StatusNotFound {
		t.Errorf("withdrawing the invitation a second time: status %d, want 404: %s", status, body)
	}
}
