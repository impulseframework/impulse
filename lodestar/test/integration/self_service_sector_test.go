// Demonstrates: tenancy.self-service.
package integration

// This suite pins how a person who signed up charts a sector of her own, over two
// instances with the real engines, the shape a deployment has. Every sign-in method of
// the crew starts invite only, and while each is the create-sector route refuses the
// pioneer in words that say what to do; the marshal holds no grant on the method that
// opens one and is refused it, and the governor, at Headquarters, opens the sign-up
// registration (the choice is a row both instances read in the request's transaction).
// The pioneer, a crew login holding no role and no sector (the sign-up
// registration's person, on no manifest), then charts sector Ember through the generated
// create-sector route, by the record's fields without its key: the route writes the row
// under an id it generates, which its answer carries, and, in the same transaction, makes
// her Ember's marshal through the crew's generated identity client (the engine answers
// the membership before the route answers), adds Ember to the writing instance's roster
// and signals the tenants kind. A body that names a key is refused, and so is a name a
// sector carries already.
// Her chart lights Ember and Ember's flight deck answers her at once; a second
// sector is refused her, as the route is refused the marshal and the governor, who hold
// sectors already (an administrator of one sector never creates another here;
// headquarters' way is an invitation, run_time_tenant_test.go); the governor
// and the client, held in every sector, see Ember with nothing written for them; and the
// second instance serves Ember at its next request. The steps run in order, each named in
// its failure, since every one reads what the ones before it wrote.
//
// A second test sends two creations from one person at once, one to each instance, and
// pins that one sector is written: one sector per person is decided in the transaction
// that writes the sector, and not only by what the permission engine answered before.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/resource/live"
)

const (
	// pioneerUser is the sign-up registration's person: a crew login on no manifest,
	// holding nothing, whose one way to a sector is to chart her own.
	pioneerUser accesstypes.User = "pioneer"

	createSectorRoute = consoleAPI + "/create-sector"
	// openSignUpRoute and makeInviteOnlyRoute are the generated methods that open a
	// sign-in method of the crew to sign-up and make it invite only again.
	openSignUpRoute     = consoleAPI + "/open-crew-sign-up"
	makeInviteOnlyRoute = consoleAPI + "/make-crew-invite-only"
	// signUpClosed is the create-sector route's answer while every method is invite only.
	signUpClosed = "Sign-up is not open, so you cannot create a sector of your own. Ask an administrator of the application to create one for you, or to invite you to one."

	// chartEmber is the create-sector body: the record's fields without its key, which
	// the route generates.
	chartEmber = `{"name":"Ember","region":"Near frontier","established":"2226-10-20"}`
	// chartEmberAgain is a second sector of the pioneer's, refused: one sector per person.
	chartEmberAgain = `{"name":"Ember Two","region":"Near frontier","established":"2226-10-21"}`
	// secondSectorRefused is the create-sector route's answer to a person who holds a
	// sector already.
	secondSectorRefused = "You already hold a role in a sector, so you cannot create a sector of your own."

	// atOnceWithin bounds each wait of the test that sends two creations at once: for a
	// request to reach its body, and for its answer once the body is let go, which waits
	// on a transaction the database may run again and on the engine's answer.
	atOnceWithin = 30 * time.Second
)

// startSelfServiceInstance is an instance over the real crew engine, the way the served
// stack builds it, so the membership the create-sector route writes through the crew's
// identity client reaches the engine every instance checks against.
func startSelfServiceInstance(t *testing.T, name string, db *initiator.SpannerDB, crewEngine, membersEngine *access.Client, svc live.Service) *flagInstance {
	t.Helper()

	roster := app.NewSectorRoster(resource.NewSpannerClient(db.Client), resource.WithTenantSignals(svc))
	if err := roster.Start(t.Context()); err != nil {
		t.Fatalf("resource.TenantRoster.Start() error = %v", err)
	}
	a := app.New(&testConfigurer{db: db, access: crewEngine, membersAccess: membersEngine, live: svc, tenants: roster})
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}

	return &flagInstance{name: name, h: withHandWrittenRoutes(a)}
}

// createdKey reads the create-sector answer: the new sector's key, an id the route
// generated.
func createdKey(t *testing.T, step string, body []byte) string {
	t.Helper()

	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("%s: answered %s: %v", step, body, err)
	}
	if _, err := impulse.UUIDFromString(got.ID); err != nil {
		t.Fatalf("%s: answered the key %q, want an id the route generated: %v", step, got.ID, err)
	}

	return got.ID
}

// assertOffered checks the answer of GET on the create-sector route: whether the caller
// may chart a sector of their own now, as a page reads it before it offers the creation.
func assertOffered(want bool) func(t *testing.T, step string, body []byte) {
	return func(t *testing.T, step string, body []byte) {
		t.Helper()
		var got struct {
			Offered *bool `json:"offered"`
		}
		if err := json.Unmarshal(body, &got); err != nil || got.Offered == nil || *got.Offered != want {
			t.Errorf("%s: answered %s (%v), want offered = %v", step, body, err, want)
		}
	}
}

// TestSelfServiceSector_pioneerChartsHerOwn plays the scenario in order over two instances.
func TestSelfServiceSector_pioneerChartsHerOwn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	// The engines are the shipped role files over this database, provisioned with the
	// personas' memberships; the pioneer is on no manifest and holds nothing.
	crewEngine := newAccessClient(t, db, crew.Roles())
	membersEngine, err := openEngine(db, members.TablePrefix, members.Roles())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := membersEngine.Close(); err != nil {
			t.Errorf("access.Client.Close() error = %v", err)
		}
	})
	if err := provisionDemoAccess(ctx, crewEngine, membersEngine); err != nil {
		t.Fatal(err)
	}

	// The pioneer's account, as the sign-up registration leaves it: a row of the crew's
	// users table and nothing else. The seat the route gives her is held by its id.
	makeCrewAccount(ctx, t, db, pioneerUser)

	fake := live.NewFake()
	first := startSelfServiceInstance(t, "the first instance", db, crewEngine, membersEngine, fake)
	second := startSelfServiceInstance(t, "the second instance", db, crewEngine, membersEngine, fake)

	// The steps before Ember exists, and the create, whose answer carries Ember's key:
	// the id the route generated, which every later step addresses Ember by.
	var ember string
	charting := []flagStep{
		{name: "before: the pioneer's chart is dark, a crew login holding nothing", on: first, user: pioneerUser, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains()},
		{name: "before: the sector list carries the three seeded sectors on the first instance", on: first, user: tenantSurveyor, method: http.MethodGet, target: sectorsRoute, wantStatus: http.StatusOK, check: assertSectorNames("Anvil", "Bastion", "Cinder")},
		{name: "before: and the governor's chart on the second lights the same three", on: second, user: tenantGovernor, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(anvil, bastion, cinder)},
		{
			name: "invite only, as every sign-in method starts: the pioneer is refused, and told what to do", on: first, user: pioneerUser, method: http.MethodPost, target: createSectorRoute, body: chartEmber, wantStatus: http.StatusForbidden,
			check: func(t *testing.T, step string, body []byte) {
				t.Helper()
				if !strings.Contains(string(body), signUpClosed) {
					t.Errorf("%s: answered %s, want %q", step, body, signUpClosed)
				}
			},
		},
		{name: "and her page is told so before it offers anything: no sector can be charted now", on: first, user: pioneerUser, method: http.MethodGet, target: createSectorRoute, wantStatus: http.StatusOK, check: assertOffered(false)},
		{name: "the marshal holds no grant on the method that opens one, and is refused it", on: first, user: tenantMarshal, method: http.MethodPost, target: openSignUpRoute, body: `{"name":"sign-up"}`, wantStatus: http.StatusForbidden},
		{name: "a default role is refused the governor: a person who signs up charts a sector and holds no role", on: first, user: tenantGovernor, method: http.MethodPost, target: openSignUpRoute, body: `{"name":"sign-up","defaultRole":"CrewCommon"}`, wantStatus: http.StatusBadRequest},
		{name: "still refused the pioneer on the second instance: nothing was opened", on: second, user: pioneerUser, method: http.MethodPost, target: createSectorRoute, body: chartEmber, wantStatus: http.StatusForbidden},
		{name: "the governor opens the sign-up registration", on: first, user: tenantGovernor, method: http.MethodPost, target: openSignUpRoute, body: `{"name":"sign-up"}`, wantStatus: http.StatusNoContent},
		{name: "open: the pioneer's page is told a sector can be charted now, on either instance", on: second, user: pioneerUser, method: http.MethodGet, target: createSectorRoute, wantStatus: http.StatusOK, check: assertOffered(true)},
		{name: "open: the marshal's page is told none can, since she holds Anvil", on: first, user: tenantMarshal, method: http.MethodGet, target: createSectorRoute, wantStatus: http.StatusOK, check: assertOffered(false)},
		{name: "the marshal holds Anvil and is refused: only a person holding no sector charts one here", on: first, user: tenantMarshal, method: http.MethodPost, target: createSectorRoute, body: chartEmber, wantStatus: http.StatusForbidden},
		{name: "the governor, marshal in every sector, is refused too: an administrator of one sector never creates another here", on: first, user: tenantGovernor, method: http.MethodPost, target: createSectorRoute, body: chartEmber, wantStatus: http.StatusForbidden},
		{name: "a body without the name is refused the pioneer, and writes nothing", on: first, user: pioneerUser, method: http.MethodPost, target: createSectorRoute, body: `{"region":"Near frontier","established":"2226-10-20"}`, wantStatus: http.StatusBadRequest},
		{name: "a body that names a key is refused her: the route generates it", on: first, user: pioneerUser, method: http.MethodPost, target: createSectorRoute, body: `{"id":"ember","name":"Ember","region":"Near frontier","established":"2226-10-20"}`, wantStatus: http.StatusBadRequest},
		{name: "a name a sector carries already is refused her", on: first, user: pioneerUser, method: http.MethodPost, target: createSectorRoute, body: `{"name":"Anvil","region":"Home","established":"2226-01-01"}`, wantStatus: http.StatusConflict},
		{name: "still dark after the refusals", on: first, user: pioneerUser, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains()},
		{name: "and the refusals wrote no sector: the list is the seeded three", on: first, user: tenantSurveyor, method: http.MethodGet, target: sectorsRoute, wantStatus: http.StatusOK, check: assertSectorNames("Anvil", "Bastion", "Cinder")},
		{
			name: "the pioneer charts sector Ember: the row, her marshal's seat and the roster in one call, the answer carrying the id the route generated, and one signal of the tenants kind", on: first, user: pioneerUser, method: http.MethodPost, target: createSectorRoute, body: chartEmber, wantStatus: http.StatusOK,
			check: func(t *testing.T, step string, body []byte) {
				t.Helper()
				ember = createdKey(t, step, body)
				if got := fake.Signals(); !slices.Equal(got, []live.Kind{live.KindTenants}) {
					t.Errorf("%s: signals = %v, want [%s]", step, got, live.KindTenants)
				}
			},
		},
	}
	for i := range charting {
		runFlagStep(t, &charting[i])
	}

	charted := sortedDomains(anvil, bastion, cinder, accesstypes.Domain(ember))
	steps := []flagStep{
		{name: "charted: her chart lights Ember at once, seated as its marshal before the route answered", on: first, user: pioneerUser, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(accesstypes.Domain(ember))},
		{name: "charted: Ember's flight deck answers her, empty", on: first, user: pioneerUser, method: http.MethodGet, target: sectorPath(ember, "missions"), wantStatus: http.StatusOK, check: assertNoRows},
		{name: "charted: the sector list carries Ember, in name order", on: first, user: tenantSurveyor, method: http.MethodGet, target: sectorsRoute, wantStatus: http.StatusOK, check: assertSectorNames("Anvil", "Bastion", "Cinder", "Ember")},
		{name: "charted: her page is told no second sector can be charted", on: first, user: pioneerUser, method: http.MethodGet, target: createSectorRoute, wantStatus: http.StatusOK, check: assertOffered(false)},
		{name: "a second sector is refused her: one sector per person", on: first, user: pioneerUser, method: http.MethodPost, target: createSectorRoute, body: chartEmberAgain, wantStatus: http.StatusForbidden},
		{name: "the marshal's chart does not light Ember", on: first, user: tenantMarshal, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(anvil)},
		{name: "Ember's routes answer the marshal 404: a sector that does not exist for her", on: first, user: tenantMarshal, method: http.MethodGet, target: sectorPath(ember, "missions"), wantStatus: http.StatusNotFound},
		{name: "the governor, marshal in every sector, sees Ember with nothing written for him", on: first, user: tenantGovernor, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(charted...)},
		{name: "the client's portal lists Ember at once: the directory's role is held in every sector", on: first, user: clientUser, method: http.MethodGet, target: portalUserDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(charted...)},
		{name: "the surveyor's chart lights Ember too, SectorCrew held in every sector", on: first, user: tenantSurveyor, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(charted...)},
		{name: "the second instance serves Ember at its next request: its roster reloaded on the tenants kind's signal", on: second, user: pioneerUser, method: http.MethodGet, target: sectorPath(ember, "missions"), wantStatus: http.StatusOK, settle: true, check: assertNoRows},
		{name: "and her chart there lights Ember", on: second, user: pioneerUser, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(accesstypes.Domain(ember))},
		{name: "and the seeded sectors stay dark for her there: SectorMarshal is hers in Ember alone", on: second, user: pioneerUser, method: http.MethodGet, target: sectorPath(anvil, "missions"), wantStatus: http.StatusNotFound},
	}
	for i := range steps {
		runFlagStep(t, &steps[i])
	}
}

// gatedBody is a request body that says when the handler first reads it and holds that
// read until the test lets it go, so a test holds two requests at one point of a
// handler: past everything the handler decides before it reads a body.
type gatedBody struct {
	body    io.Reader
	once    sync.Once
	reached chan struct{}
	release <-chan struct{}
}

// newGatedBody is the body held until release is closed.
func newGatedBody(body string, release <-chan struct{}) *gatedBody {
	return &gatedBody{body: strings.NewReader(body), reached: make(chan struct{}), release: release}
}

// Read says, at the first read, that the handler has reached the body, and waits there
// until the test releases it.
func (g *gatedBody) Read(p []byte) (int, error) {
	g.once.Do(func() {
		close(g.reached)
		<-g.release
	})

	return g.body.Read(p) //nolint:wrapcheck // a reader passes its source's error on, io.EOF among them
}

// TestSelfServiceSector_twoAtOnce pins one sector per person against two creations one
// person sends at once, one to each instance. Each request is held at its body, which the
// route reads after the request's permissions have answered that the person holds no
// sector, so both are past that answer before either writes anything; then both are let
// go. The route reads what the person holds again in the transaction that writes the
// sector, from the crew's own tables, so one creation commits and the other is refused
// in the words a second sector is always refused in. One sector is written: her chart
// lights one, on both instances, the sector list gains one, and the permission store
// holds her as the marshal of that one and as nothing anywhere else.
func TestSelfServiceSector_twoAtOnce(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	crewEngine := newAccessClient(t, db, crew.Roles())
	membersEngine, err := openEngine(db, members.TablePrefix, members.Roles())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := membersEngine.Close(); err != nil {
			t.Errorf("access.Client.Close() error = %v", err)
		}
	})
	if err := provisionDemoAccess(ctx, crewEngine, membersEngine); err != nil {
		t.Fatal(err)
	}
	pioneer := makeCrewAccount(ctx, t, db, pioneerUser)

	fake := live.NewFake()
	first := startSelfServiceInstance(t, "the first instance", db, crewEngine, membersEngine, fake)
	second := startSelfServiceInstance(t, "the second instance", db, crewEngine, membersEngine, fake)

	opening := []flagStep{
		{name: "the governor opens the sign-up registration", on: first, user: tenantGovernor, method: http.MethodPost, target: openSignUpRoute, body: `{"name":"sign-up"}`, wantStatus: http.StatusNoContent},
		{name: "open: the pioneer, holding nothing, may chart a sector on the first instance", on: first, user: pioneerUser, method: http.MethodGet, target: createSectorRoute, wantStatus: http.StatusOK, check: assertOffered(true)},
		{name: "open: and on the second", on: second, user: pioneerUser, method: http.MethodGet, target: createSectorRoute, wantStatus: http.StatusOK, check: assertOffered(true)},
	}
	for i := range opening {
		runFlagStep(t, &opening[i])
	}

	// Two creations of the pioneer's, each held at its body: the route has asked the
	// permissions what she holds, and neither request has written anything.
	posts := []struct {
		sector string
		on     *flagInstance
		body   string
	}{
		{sector: "Ember", on: first, body: chartEmber},
		{sector: "Ember Two", on: second, body: chartEmberAgain},
	}
	release := make(chan struct{})
	answers := make(chan creationAnswer, len(posts))
	gates := make([]*gatedBody, len(posts))
	for i, post := range posts {
		gates[i] = newGatedBody(post.body, release)
		req := httptest.NewRequestWithContext(sessionContext(t, pioneerUser), http.MethodPost, createSectorRoute, gates[i])
		req.Header.Set("Content-Type", "application/json")
		go func() {
			rr := httptest.NewRecorder()
			post.on.h.ServeHTTP(rr, req)
			answers <- creationAnswer{sector: post.sector, on: post.on.name, status: rr.Code, body: rr.Body.Bytes()}
		}()
	}
	for i, gate := range gates {
		select {
		case <-gate.reached:
		case got := <-answers:
			close(release)
			t.Fatalf("the creation of %s on %s answered %d before both requests had reached their bodies: %s", got.sector, got.on, got.status, got.body)
		case <-time.After(atOnceWithin):
			close(release)
			t.Fatalf("the creation of %s on %s did not reach its body within %s", posts[i].sector, posts[i].on.name, atOnceWithin)
		}
	}
	close(release)

	var created, refused []creationAnswer
	for range posts {
		select {
		case got := <-answers:
			switch got.status {
			case http.StatusOK:
				created = append(created, got)
			case http.StatusForbidden:
				refused = append(refused, got)
			default:
				t.Fatalf("the creation of %s on %s answered %d, want 200 or 403: %s", got.sector, got.on, got.status, got.body)
			}
		case <-time.After(atOnceWithin):
			t.Fatalf("a creation did not answer within %s of both bodies being let go", atOnceWithin)
		}
	}
	if len(created) != 1 || len(refused) != 1 {
		t.Fatalf("two creations at once from one person: %d created and %d refused, want one of each: created %s, refused %s", len(created), len(refused), answersText(created), answersText(refused))
	}
	if !strings.Contains(string(refused[0].body), secondSectorRefused) {
		t.Errorf("the creation of %s on %s was refused with %s, want %q", refused[0].sector, refused[0].on, refused[0].body, secondSectorRefused)
	}
	key := accesstypes.Domain(createdKey(t, "the creation of "+created[0].sector, created[0].body))

	// One sector was written, and it is hers.
	steps := []flagStep{
		{name: "after: her chart lights the one sector she charted", on: first, user: pioneerUser, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(key)},
		{name: "after: the sector list gained one sector, the one whose creation was answered", on: first, user: tenantSurveyor, method: http.MethodGet, target: sectorsRoute, wantStatus: http.StatusOK, check: assertSectorNames("Anvil", "Bastion", "Cinder", created[0].sector)},
		{name: "after: the second instance serves it at its next request", on: second, user: pioneerUser, method: http.MethodGet, target: sectorPath(string(key), "missions"), wantStatus: http.StatusOK, settle: true, check: assertNoRows},
		{name: "after: and her chart there lights the same one sector", on: second, user: pioneerUser, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(key)},
		{name: "after: a third creation is refused her as the second was", on: second, user: pioneerUser, method: http.MethodPost, target: createSectorRoute, body: `{"name":"Ember Three","region":"Near frontier","established":"2226-10-22"}`, wantStatus: http.StatusForbidden},
	}
	for i := range steps {
		runFlagStep(t, &steps[i])
	}

	// The permission store seats her once: the marshal of that sector, and nothing in
	// any other place.
	held, err := crewEngine.UserManager().UserRoles(ctx, pioneer)
	if err != nil {
		t.Fatalf("access.UserManager.UserRoles(the pioneer) error = %v", err)
	}
	if seat := accesstypes.DomainPolicyScope(key); len(held) != 1 || !slices.Equal(held[seat], []accesstypes.Role{"SectorMarshal"}) {
		t.Errorf("the pioneer holds %v in the permission store, want SectorMarshal in sector %s and nothing else: two creations at once seat her once", held, key)
	}
}

// creationAnswer is the answer one of two creations sent at once came back with.
type creationAnswer struct {
	sector string
	on     string
	status int
	body   []byte
}

// answersText names each answer by its sector, its instance and its body, for a failure.
func answersText(answers []creationAnswer) string {
	texts := make([]string, 0, len(answers))
	for i := range answers {
		texts = append(texts, answers[i].sector+" on "+answers[i].on+": "+string(answers[i].body))
	}

	return "[" + strings.Join(texts, "; ") + "]"
}
