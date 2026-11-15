package integration

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session"
)

// TestSelfService_aSignedInStrangerCreatesTheirTenant proves the self-service half of the
// Tenant record's creation shape (@tenant(creation: both)): a person who signs in holding
// no tenant creates one through the generated create-your-tenant route and stands in it
// at once, seated as its administrator in the same call; whoever already holds a tenant
// is refused, the administrator included, since an administrator creates tenants through
// the record's own create path (tenants_test.go) and never here; and the body is held to
// the record, so a refused body writes nothing.
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

	// The newcomer names their tenant, by its name alone, and is answered the id the
	// route generated for it.
	status, body := newcomer.do(ctx, http.MethodPost, "/api/create-tenant", []byte(`{"name":"West"}`))
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
