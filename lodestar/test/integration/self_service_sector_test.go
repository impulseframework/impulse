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

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

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
	signUpClosed = "sign-up is not open, so you cannot create a sector of your own: ask an administrator of the application to create one for you, or to invite you to one"

	// chartEmber is the create-sector body: the record's fields without its key, which
	// the route generates.
	chartEmber = `{"name":"Ember","region":"Near frontier","established":"2226-10-20"}`
	// chartEmberAgain is a second sector of the pioneer's, refused: one sector per person.
	chartEmberAgain = `{"name":"Ember Two","region":"Near frontier","established":"2226-10-21"}`
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
		{name: "the marshal holds no grant on the method that opens one, and is refused it", on: first, user: tenantMarshal, method: http.MethodPost, target: openSignUpRoute, body: `{"name":"sign-up"}`, wantStatus: http.StatusForbidden},
		{name: "a default role is refused the governor: a person who signs up charts a sector and holds no role", on: first, user: tenantGovernor, method: http.MethodPost, target: openSignUpRoute, body: `{"name":"sign-up","defaultRole":"CrewCommon"}`, wantStatus: http.StatusBadRequest},
		{name: "still refused the pioneer on the second instance: nothing was opened", on: second, user: pioneerUser, method: http.MethodPost, target: createSectorRoute, body: chartEmber, wantStatus: http.StatusForbidden},
		{name: "the governor opens the sign-up registration", on: first, user: tenantGovernor, method: http.MethodPost, target: openSignUpRoute, body: `{"name":"sign-up"}`, wantStatus: http.StatusNoContent},
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
