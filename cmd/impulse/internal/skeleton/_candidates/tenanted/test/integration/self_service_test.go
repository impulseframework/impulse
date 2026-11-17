package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
)

// The create-your-tenant route's answer while every sign-in method is invite only, and
// what the Sign-in page's dialog shows before an administrator opens the password
// method.
const (
	signUpClosed      = "sign-up is not open, so you cannot create a tenant of your own: ask an administrator of the application to create one for you, or to invite you to one"
	passwordOpenWarns = "Anyone with an email address will be able to sign up and create a tenant of their own, without an invitation."
)

// TestSelfService_aSignedInStrangerCreatesTheirTenant proves who creates a tenant of
// their own. Nobody does while every sign-in method of the staff auth is invite only,
// which is how each starts: the generated create-your-tenant route refuses in words that
// say what to do, and writes nothing. An administrator opens a method to sign-up through
// the generated OpenStaffSignUp, whose warning says who will be admitted and what they
// get; a tenant's administrator, who holds no grant on it, is refused, and so is a
// request that names a default role, since a person who signs up here creates a tenant
// and holds no role. From then on a person who signs in holding no tenant creates one
// through the route and stands in it at once, seated as its administrator in the same
// call; whoever already holds a tenant is refused, the administrator included, since an
// administrator creates tenants through the record's own create path (tenants_test.go)
// and never here; and the body is held to the record, so a refused body writes nothing.
// Made invite only again, the route refuses the next person and the tenant created
// meanwhile stays its creator's. The same route, read with GET, tells a page whether the
// caller may create a tenant now, by the two facts the creation refuses on, so that the
// page offers it only to a person the route would serve: true for a person who holds no
// tenant while a method is open, and false for everyone else at every step.
func TestSelfService_aSignedInStrangerCreatesTheirTenant(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	newcomer := signedIn(ctx, t, s, createLogin(ctx, t, s, "newcomer"))
	another := signedIn(ctx, t, s, createLogin(ctx, t, s, "another"))
	admin := signedIn(ctx, t, s, adminUser)
	member := signedIn(ctx, t, s, memberUser)

	// Before: the newcomer holds nothing.
	if domains := userDomains(ctx, t, newcomer); len(domains) != 0 {
		t.Fatalf("user-domains before = %v, want none", domains)
	}

	// Invite only, as every method starts: nobody creates a tenant of their own, and the
	// refusal says what to do.
	if status, body := newcomer.do(ctx, http.MethodPost, "/api/create-tenant", []byte(`{"name":"West"}`)); status != http.StatusForbidden || !strings.Contains(string(body), signUpClosed) {
		t.Fatalf("POST /api/create-tenant while every method is invite only: status %d, want 403 saying sign-up is not open: %s", status, body)
	}
	if names := slices.Sorted(maps.Values(tenantNames(ctx, t, admin))); !slices.Equal(names, []string{"North", "South"}) {
		t.Fatalf("the tenants after the refused create = %v, want North and South alone", names)
	}
	if creationOffered(ctx, t, newcomer) {
		t.Error("GET /api/create-tenant while every method is invite only says a tenant can be created, and the route refuses it")
	}

	// The administrator opens the password method. The warning says who will be admitted,
	// in the server's words; a tenant's administrator holds no grant on the method and
	// reads no warning of it; a default role is refused here.
	status, body := askWarning(ctx, admin, "/api/open-staff-sign-up", `{"name":"password"}`)
	var warning resource.Warning
	if err := json.Unmarshal(body, &warning); status != http.StatusOK || err != nil || warning.Consequence != passwordOpenWarns || warning.Confirm != "Open to anyone with an email address" {
		t.Fatalf("the warning of opening the password method: status %d: %s, want %q", status, body, passwordOpenWarns)
	}
	if status, body := askWarning(ctx, member, "/api/open-staff-sign-up", `{"name":"password"}`); status != http.StatusForbidden {
		t.Errorf("the warning as a tenant's member: status %d, want 403: %s", status, body)
	}
	if status, body := member.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(`{"name":"password"}`)); status != http.StatusForbidden {
		t.Errorf("POST /api/open-staff-sign-up as a tenant's member: status %d, want 403: %s", status, body)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(`{"name":"password","defaultRole":"Administrator"}`)); status != http.StatusBadRequest {
		t.Errorf("POST /api/open-staff-sign-up naming a default role: status %d, want 400: a person who signs up creates a tenant: %s", status, body)
	}
	if status, body := newcomer.do(ctx, http.MethodPost, "/api/create-tenant", []byte(`{"name":"West"}`)); status != http.StatusForbidden {
		t.Fatalf("POST /api/create-tenant after the warning and the refused saves: status %d, want 403: nothing was opened: %s", status, body)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/open-staff-sign-up", []byte(`{"name":"password"}`)); status != http.StatusNoContent {
		t.Fatalf("POST /api/open-staff-sign-up: status %d: %s", status, body)
	}

	// Open: the page of a person who holds no tenant offers the creation, and the page of
	// a person who holds one does not, the administrator included.
	offers := []struct {
		name    string
		browser *browser
		want    bool
	}{
		{name: "the newcomer, who holds no tenant", browser: newcomer, want: true},
		{name: "the other stranger", browser: another, want: true},
		{name: "the member of a tenant", browser: member, want: false},
		{name: "the administrator, who holds every tenant", browser: admin, want: false},
	}
	for _, offer := range offers {
		if got := creationOffered(ctx, t, offer.browser); got != offer.want {
			t.Errorf("GET /api/create-tenant as %s, once a method is open: offered = %v, want %v", offer.name, got, offer.want)
		}
	}

	// The newcomer names their tenant, by its name alone, and is answered the id the
	// route generated for it.
	status, body = newcomer.do(ctx, http.MethodPost, "/api/create-tenant", []byte(`{"name":"West"}`))
	if status != http.StatusOK {
		t.Fatalf("POST /api/create-tenant: status %d: %s", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("POST /api/create-tenant answered %s: %v", body, err)
	}
	if _, err := impulse.UUIDFromString(created.ID); err != nil {
		t.Fatalf("POST /api/create-tenant answered the key %q, want an id the route generated: %v", created.ID, err)
	}
	west := created.ID
	westSegment := "/api/tenants/" + west + "/announcements"

	// At once: the newcomer stands in the tenant (the seat is written and the engine
	// answers it before the route answers), its segment serves them, and the
	// administrator, holding the domain role in every tenant, sees it too.
	if domains := userDomains(ctx, t, newcomer); !slices.Equal(domains, []string{west}) {
		t.Errorf("user-domains after = %v, want [%s]", domains, west)
	}
	if creationOffered(ctx, t, newcomer) {
		t.Error("GET /api/create-tenant as the newcomer, who holds a tenant now, says another can be created")
	}
	reads := []struct {
		name       string
		browser    *browser
		wantStatus int
	}{
		{name: "the newcomer reads their tenant's segment", browser: newcomer, wantStatus: http.StatusOK},
		{name: "the administrator reads it", browser: admin, wantStatus: http.StatusOK},
		{name: "the member holds nothing there and is answered not-found", browser: member, wantStatus: http.StatusNotFound},
	}
	for _, read := range reads {
		if status, body := read.browser.do(ctx, http.MethodGet, westSegment, nil); status != read.wantStatus {
			t.Errorf("%s: GET %s: status %d, want %d: %s", read.name, westSegment, status, read.wantStatus, body)
		}
	}

	// Refused: one tenant per person, and only a person holding none creates one.
	refusals := []struct {
		name       string
		browser    *browser
		body       string
		wantStatus int
	}{
		{name: "the newcomer, holding a tenant now, is refused a second", browser: newcomer, body: `{"name":"West again"}`, wantStatus: http.StatusForbidden},
		{name: "the member of a tenant is refused", browser: member, body: `{"name":"East"}`, wantStatus: http.StatusForbidden},
		{name: "the administrator is refused: an administrator creates tenants through the record, never here", browser: admin, body: `{"name":"East"}`, wantStatus: http.StatusForbidden},
		{name: "a body without the name is refused", browser: another, body: `{}`, wantStatus: http.StatusBadRequest},
		{name: "a body that names a key is refused: the route generates it", browser: another, body: `{"id":"east","name":"East"}`, wantStatus: http.StatusBadRequest},
		{name: "a name a tenant carries already is refused", browser: another, body: `{"name":"North"}`, wantStatus: http.StatusConflict},
	}
	for _, refusal := range refusals {
		if status, body := refusal.browser.do(ctx, http.MethodPost, "/api/create-tenant", []byte(refusal.body)); status != refusal.wantStatus {
			t.Errorf("%s: POST /api/create-tenant %s: status %d, want %d: %s", refusal.name, refusal.body, status, refusal.wantStatus, body)
		}
	}
	// None of the refused bodies wrote a row, and the other stranger still holds nothing.
	names := slices.Sorted(maps.Values(tenantNames(ctx, t, admin)))
	if !slices.Equal(names, []string{"North", "South", "West"}) {
		t.Errorf("the administrator's tenants after the refusals = %v, want North, South and West alone", names)
	}
	if domains := userDomains(ctx, t, another); len(domains) != 0 {
		t.Errorf("user-domains of the other stranger = %v, want none", domains)
	}

	// Invite only again: the next person is refused, and West stays its creator's.
	if status, body := admin.do(ctx, http.MethodPost, "/api/make-staff-invite-only", []byte(`{"name":"password"}`)); status != http.StatusNoContent {
		t.Fatalf("POST /api/make-staff-invite-only: status %d: %s", status, body)
	}
	if status, body := another.do(ctx, http.MethodPost, "/api/create-tenant", []byte(`{"name":"East"}`)); status != http.StatusForbidden || !strings.Contains(string(body), signUpClosed) {
		t.Errorf("POST /api/create-tenant once invite only again: status %d, want 403 saying sign-up is not open: %s", status, body)
	}
	if domains := userDomains(ctx, t, newcomer); !slices.Equal(domains, []string{west}) {
		t.Errorf("the newcomer's tenants once invite only again = %v, want [%s]: they keep what they have", domains, west)
	}
	if creationOffered(ctx, t, another) {
		t.Error("GET /api/create-tenant once invite only again says a tenant can be created, and the route refuses it")
	}
}

// creationOffered reads whether the browser's person may create a tenant of their own
// now, as the page that offers the creation reads it: GET on the creation's own route.
func creationOffered(ctx context.Context, t *testing.T, b *browser) bool {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/create-tenant", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/create-tenant: status %d: %s", status, body)
	}
	var answer struct {
		Offered *bool `json:"offered"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.Offered == nil {
		t.Fatalf("GET /api/create-tenant answered %s (%v), want whether a tenant can be created", body, err)
	}

	return *answer.Offered
}

// askWarning asks a method for its warning in place of running it, the way the library's
// dialog does: the request goes to the method's own route with X-Warning.
func askWarning(ctx context.Context, b *browser, path, body string) (status int, respBody []byte) {
	b.t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base+path, bytes.NewReader([]byte(body)))
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(resource.WarningHeader, "true")
	if token := b.xsrfToken(); token != "" {
		req.Header.Set("X-XSRF-TOKEN", token)
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

// createLogin creates a password login holding no role anywhere: a stranger to every
// tenant, with the development password.
func createLogin(ctx context.Context, t *testing.T, s *served, user string) string {
	t.Helper()

	password := adminPassword
	if _, err := s.auth.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: user, Password: &password}); err != nil {
		t.Fatalf("CreateSessionUser(%s) error = %v", user, err)
	}

	return user
}

// userDomains reads the browser's tenant list from the generated user-domains route.
func userDomains(ctx context.Context, t *testing.T, b *browser) []string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, "/api/user-domains", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/user-domains: status %d: %s", status, body)
	}
	var domains []string
	if err := unmarshalDomains(body, &domains); err != nil {
		t.Fatalf("user-domains is not a list: %v: %s", err, body)
	}

	return domains
}
