//go:build skipAuth

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session"
)

// The sentence a person is told about leaving a sector themselves, as the identity
// package words it for the crew's sectors.
const lastMarshalLeaving = "You are the last administrator of this sector. Give another person the role before you leave."

// ownTenantsRoutes are the two routes of a person's own sectors as the methods answer
// names them for the person's account page.
type ownTenantsRoutes struct {
	List  string `json:"list"`
	Leave string `json:"leave"`
}

// TestOwnTenants_aPersonLeavesASector proves on the served stack, the crew's sectors
// named as production names them, that a person reads the sectors they hold something in
// on their account page and leaves one at their own word. The methods answer names the
// two routes. The hazard analyst, crew in Anvil and in Cinder, reads both by the
// sectors' names, with the roles held in each in words, and leaves Cinder: her next
// request in Cinder is answered as a stranger's (the cadet, who never held anything
// there), her sectors no longer name it, and her session and account stand, Anvil still
// hers. The marshal, Anvil's only administrator, is told why she cannot leave it before
// she asks, and is refused in the same words when she does; nothing of hers goes.
//
// A person whose one sector is all their account holds leaves it as anyone does, and is
// told nothing more. The account stays, holding nothing, with its sign-in identities, and
// the session the person left from stands: their account page lists no sector.
func TestOwnTenants_aPersonLeavesASector(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{namesTenants: true})
	hazards := newBrowser(t, s, consoleAPI)
	hazards.signIn(ctx, "hazards")

	status, body := hazards.do(ctx, http.MethodGet, consoleAPI+"/user/methods", nil)
	assertStatus(t, status, http.StatusOK, body)
	var methods struct {
		OwnTenants ownTenantsRoutes `json:"ownTenants"`
	}
	if err := json.Unmarshal(body, &methods); err != nil {
		t.Fatalf("the methods answer is not JSON: %v: %s", err, body)
	}
	if want := (ownTenantsRoutes{List: consoleAPI + "/user/tenants", Leave: consoleAPI + "/user/tenants/leave"}); methods.OwnTenants != want {
		t.Fatalf("the methods answer names a person's sectors at %+v, want %+v: %s", methods.OwnTenants, want, body)
	}
	routes := methods.OwnTenants

	// The hazard analyst's two sectors, by name, each with the roles she holds there.
	const (
		anvilCrew  = `{"domain":"` + anvil + `","name":"Anvil","roles":["Hazard analyst","Sector crew"]}`
		cinderCrew = `{"domain":"` + cinder + `","name":"Cinder","roles":["Hazard analyst","Sector crew"]}`
	)
	if got, want := ownTenantsAnswer(ctx, t, hazards, routes.List), `{"tenants":[`+anvilCrew+`,`+cinderCrew+`]}`; got != want {
		t.Fatalf("the hazard analyst's sectors = %s, want %s", got, want)
	}
	if status, body := hazards.do(ctx, http.MethodGet, sectorPath(cinder, "wings"), nil); status != http.StatusOK {
		t.Fatalf("GET Cinder's wings as the hazard analyst before she leaves: status %d, want 200: %s", status, body)
	}
	if got := hazards.domains(ctx); !slices.Contains(got, cinder) {
		t.Fatalf("the hazard analyst's sectors before she leaves = %v, want Cinder among them", got)
	}

	// She leaves Cinder.
	if got := leaveTenant(ctx, t, hazards, routes.Leave, cinder, http.StatusOK); got != "" {
		t.Fatalf("leaving Cinder answered %s, want no body", got)
	}
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	strangerStatus, strangerBody := cadet.do(ctx, http.MethodGet, sectorPath(cinder, "wings"), nil)
	if strangerStatus == http.StatusOK {
		t.Fatalf("GET Cinder's wings as the cadet, who holds nothing there: status 200, want a refusal: %s", strangerBody)
	}
	if status, body := hazards.do(ctx, http.MethodGet, sectorPath(cinder, "wings"), nil); status != strangerStatus || !bytes.Equal(body, strangerBody) {
		t.Errorf("GET Cinder's wings as the hazard analyst after she left: status %d %s, want a stranger's answer, %d %s", status, body, strangerStatus, strangerBody)
	}
	if got := hazards.domains(ctx); slices.Contains(got, cinder) || !slices.Contains(got, anvil) {
		t.Errorf("the hazard analyst's sectors after she left Cinder = %v, want Anvil and not Cinder", got)
	}
	if got := hazards.session(ctx)["authenticated"]; got != true {
		t.Errorf("the hazard analyst's session after she left Cinder: authenticated = %v, want true", got)
	}
	if status, body := hazards.do(ctx, http.MethodGet, sectorPath(anvil, "wings"), nil); status != http.StatusOK {
		t.Errorf("GET Anvil's wings as the hazard analyst after she left Cinder: status %d, want 200: %s", status, body)
	}
	if got, want := ownTenantsAnswer(ctx, t, hazards, routes.List), `{"tenants":[`+anvilCrew+`]}`; got != want {
		t.Errorf("the hazard analyst's sectors after she left Cinder = %s, want %s", got, want)
	}
	if got := rowsWhere(ctx, t, s, "CrewSessionUsers", "Id", string(account(t, "hazards"))); got != 1 {
		t.Errorf("the hazard analyst's account rows after she left Cinder = %d, want 1", got)
	}

	// Anvil's only marshal is told before she asks, and refused when she does.
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")
	marshalsAnvil := `{"tenants":[{"domain":"` + anvil + `","name":"Anvil","roles":["Sector marshal"],"refusal":"` + lastMarshalLeaving + `"}]}`
	if got := ownTenantsAnswer(ctx, t, marshal, routes.List); got != marshalsAnvil {
		t.Errorf("the marshal's sectors = %s, want %s", got, marshalsAnvil)
	}
	if got := leaveTenant(ctx, t, marshal, routes.Leave, anvil, http.StatusForbidden); !strings.Contains(got, lastMarshalLeaving) {
		t.Errorf("the marshal leaving Anvil was refused saying %s, want %q", got, lastMarshalLeaving)
	}
	if got := ownTenantsAnswer(ctx, t, marshal, routes.List); got != marshalsAnvil {
		t.Errorf("the marshal's sectors after she was refused = %s, want %s", got, marshalsAnvil)
	}

	// A person whose one sector is all their account holds.
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	const loner = "loner"
	lonerID := seedSignInAccount(ctx, t, s, loner)
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/assign-crew-role", []byte(`{"userId":"`+lonerID.String()+`","role":"SectorCrew","domain":"`+anvil+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("CrewSessionIdentities", map[string]any{
		"Id": newID(t), "UserId": lonerID.String(), "Method": "google", "Connection": "example", "Subject": "loner-subject",
		"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
	})}); err != nil {
		t.Fatalf("writing the loner's sign-in identity: %v", err)
	}
	lonerBrowser := newBrowser(t, s, consoleAPI)
	lonerBrowser.signIn(ctx, loner)
	lonersAnvil := `{"tenants":[{"domain":"` + anvil + `","name":"Anvil","roles":["Sector crew"]}]}`
	if got := ownTenantsAnswer(ctx, t, lonerBrowser, routes.List); got != lonersAnvil {
		t.Fatalf("the loner's sectors = %s, want %s", got, lonersAnvil)
	}
	if got := leaveTenant(ctx, t, lonerBrowser, routes.Leave, anvil, http.StatusOK); got != "" {
		t.Fatalf("the loner leaving Anvil answered %s, want no body", got)
	}
	if got := rowsWhere(ctx, t, s, "CrewSessionUsers", "Id", lonerID.String()); got != 1 {
		t.Errorf("the loner's account rows after leaving the last sector = %d, want 1", got)
	}
	if got := rowsWhere(ctx, t, s, "CrewSessionIdentities", "UserId", lonerID.String()); got != 1 {
		t.Errorf("the loner's sign-in identities after leaving the last sector = %d, want 1", got)
	}
	if got := rowsWhere(ctx, t, s, "CrewUserRoles", "UserId", lonerID.String()); got != 0 {
		t.Errorf("the loner's memberships after leaving the last sector = %d, want 0", got)
	}
	if got := lonerBrowser.session(ctx)["authenticated"]; got != true {
		t.Errorf("the loner's session after leaving the last sector: authenticated = %v, want true", got)
	}
	if got := ownTenantsAnswer(ctx, t, lonerBrowser, routes.List); got != `{"tenants":[]}` {
		t.Errorf("the loner's sectors after leaving the last sector = %s, want none", got)
	}
}

// ownTenantsAnswer reads the browser's person's sectors from the route the methods answer
// names, as their account page does, and answers the answer as the server wrote it.
func ownTenantsAnswer(ctx context.Context, t *testing.T, b *browser, list string) string {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, list, nil)
	assertStatus(t, status, http.StatusOK, body)

	return strings.TrimSpace(string(body))
}

// leaveTenant asks to leave the sector as the browser's person, fails the test unless the
// answer has the status wanted, and answers the answer as the server wrote it.
func leaveTenant(ctx context.Context, t *testing.T, b *browser, leave, sector string, want int) string {
	t.Helper()

	request, err := json.Marshal(map[string]any{"domain": sector})
	if err != nil {
		t.Fatal(err)
	}
	status, body := b.do(ctx, http.MethodPost, leave, request)
	assertStatus(t, status, want, body)

	return strings.TrimSpace(string(body))
}

// seedSignInAccount makes an account of the crew's on the served stack through the
// session library's API, as the stack makes the cast's: holding nothing, with the cast's
// password, so the person signs in as a persona does. It answers the account's id.
func seedSignInAccount(ctx context.Context, t *testing.T, s *served, username string) impulse.UUID {
	t.Helper()

	password := personaPassword
	id, err := s.crew.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: username, Password: &password})
	if err != nil {
		t.Fatalf("CreateSessionUser(%s) error = %v", username, err)
	}

	return id
}
