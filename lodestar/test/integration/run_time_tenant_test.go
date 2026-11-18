// Demonstrates: tenancy.run-time-tenant, tenancy.tenant-record, tenancy.creator-seat, tenancy.invited-tenant.
package integration

// This suite pins the tenant roster keeping up without a restart, a migrate job or a new
// login. Two instances of the application run over one database, the real engines
// provisioned from the shipped role files, one in-memory live service (live.Fake, which
// fans a signal out to every subscription) and a started roster per instance, the shape a
// deployment has. Nobody charts a sector directly: a create operation on Sectors is
// refused whoever sends it, saying how a sector is made, and writes and signals nothing.
// Sector Dawn is made the way every sector is, by the acceptance of an invitation, which
// the crew's identity client runs on the first instance as the session library runs it
// at the invited person's first sign-in: in one transaction the generated maker writes
// Dawn from the invitation's details, under an id the record's create generates, and the
// person is made its marshal (SectorMarshal, the roles that administer a sector: the
// first person in is its first administrator). The suite serves no session manager, so
// it runs the acceptance itself, with the surveyor as the invited person, who holds
// SectorCrew in every sector besides. After the commit that instance's roster rereads its
// table and the tenants kind is signaled: her chart lights Dawn and Dawn's wings answer
// her; a second acceptance that would make another sector named Dawn is refused, the
// name being one sector's; the marshal's chart does not light it and Dawn's routes
// answer her 404, a sector that does not exist for her; the portal's client, whose
// directory role is held in every sector, opens Dawn's portal at once, as the governor
// and the archivist see Dawn with nothing written for them; and the second instance
// serves Dawn at its next request, its roster reloaded on the signal. The steps run in
// order, each named in its failure, since every one reads what the ones before it wrote.
// The served suite (TestInvitedSector) plays the invitation itself, from the mail to the
// sign-in.

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/resource/live"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The suite's cast, the shipped personas: the surveyor is the person invited to found
// Dawn; the marshal holds Anvil alone; the governor and the archivist are held in every
// sector, as the client's directory role is.
const (
	tenantSurveyor  accesstypes.User = "surveyor"
	tenantMarshal   accesstypes.User = "marshal"
	tenantGovernor  accesstypes.User = "governor"
	tenantArchivist accesstypes.User = "archivist"

	userDomainsRoute       = consoleAPI + "/user-domains"
	portalUserDomainsRoute = portalAPI + "/user-domains"
	sectorsRoute           = consoleAPI + "/sectors"
	resourcesRoute         = consoleAPI + "/resources"

	// chartDawn is the consolidated patch that would create the sector directly, and
	// chartDawnUnderAKey the same naming a key in the operation's path: both are
	// refused, since nobody creates a sector directly.
	chartDawn          = `[{"op":"add","path":"/sectors","value":{"name":"Dawn","region":"Outer frontier","established":"2226-10-02"}}]`
	chartDawnUnderAKey = `[{"op":"add","path":"/sectors/dawn","value":{"name":"Dawn","region":"Outer frontier","established":"2226-10-02"}}]`

	// sectorNotCreatedDirectly is what a create operation on Sectors is told.
	sectorNotCreatedDirectly = "a sector is not created directly: invite one person, and the sector is made when that person first signs in"

	// dawnInvited is the sector an invitation carries, as the crew's generated invite
	// method writes it on the invitation's row: what people will call the sector, and
	// its details, the Sector record's fields under their own names.
	dawnInvited = `{"fields":{"Name":"Dawn","Region":"Outer frontier","Established":"2226-10-02"}}`
)

// assertRefusedCreate pins what a create operation on Sectors answers: how a sector is
// made.
func assertRefusedCreate(t *testing.T, step string, body []byte) {
	t.Helper()

	if !strings.Contains(string(body), sectorNotCreatedDirectly) {
		t.Errorf("%s: answered %s, want it to say %q", step, body, sectorNotCreatedDirectly)
	}
}

// acceptSectorInvitation runs the acceptance of an invitation that makes a sector, as the
// session library runs it at the invited person's first sign-in: the crew identity
// client's hook inside one read-write transaction, then, once that has committed, the
// function the hook answered, which rereads the instance's roster and signals the
// tenants kind. The person signed in with a password, so the sector gets no directory.
// A refusal of the hook is answered as the hook gave it.
func acceptSectorInvitation(ctx context.Context, t *testing.T, db *initiator.SpannerDB, client *crewidentity.Client, userID impulse.UUID, username accesstypes.User, name, details string) error {
	t.Helper()

	id, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID() error = %v", err)
	}
	invitation := &sessioninfo.Invitation{
		ID: id, Email: string(username) + "@mail.example",
		NewTenant:    &sessioninfo.NewTenant{Name: name, Details: json.RawMessage(details)},
		SignInMethod: sessioninfo.MethodPassword,
	}
	hook := client.InvitationAccepted()
	var (
		committed func(context.Context)
		refused   error
	)
	if _, err := db.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		committed, refused = hook(ctx, tx, userID, string(username), invitation)

		return refused
	}); err != nil {
		if refused != nil {
			// The hook's own refusal, as it gave it: the transaction wrote nothing.
			return refused
		}

		return errors.Wrap(err, "spanner.Client.ReadWriteTransaction()")
	}
	committed(ctx)

	return nil
}

// sortedDomains lists the domains in the order the user-domains route lists them: by
// the domain, which is a sector's id.
func sortedDomains(domains ...accesstypes.Domain) []accesstypes.Domain {
	slices.Sort(domains)

	return domains
}

// startTenantInstance builds an App over the database, the real engines, the shared live
// service, a roster of its own started over that service, as a served instance's data
// configuration starts it, and starts the App. It answers the instance and the crew's
// identity client that stands over the instance's engine, its roster and the live
// service, as the crew auth builds it: an accepted invitation makes a sector through it,
// with the generated maker over that roster and that service.
func startTenantInstance(t *testing.T, name string, db *initiator.SpannerDB, crewEngine *access.Client, membersEngine access.Controller, svc live.Service) (*flagInstance, *crewidentity.Client) {
	t.Helper()

	roster := app.NewSectorRoster(resource.NewSpannerClient(db.Client), resource.WithTenantSignals(svc))
	if err := roster.Start(t.Context()); err != nil {
		t.Fatalf("resource.TenantRoster.Start() error = %v", err)
	}
	configurer := &testConfigurer{
		db:            db,
		access:        crewEngine,
		membersAccess: membersEngine,
		live:          svc,
		tenants:       roster,
	}
	a := app.New(configurer)
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}

	return &flagInstance{name: name, h: withHandWrittenRoutes(a)}, configurer.CrewIdentity()
}

// assertDomains pins the chart a user-domains answer draws, in the sorted order the
// route lists.
func assertDomains(want ...accesstypes.Domain) func(t *testing.T, step string, body []byte) {
	return func(t *testing.T, step string, body []byte) {
		t.Helper()

		var got []accesstypes.Domain
		if err := unmarshalDomains(body, &got); err != nil {
			t.Fatalf("%s: decoding the domains: %v: %s", step, err, body)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: domains = %v, want %v", step, got, want)
		}
	}
}

// assertNoRows pins a list answered empty: Dawn is charted, not yet crewed or flown.
func assertNoRows(t *testing.T, step string, body []byte) {
	t.Helper()

	if got := len(decodeRows(t, body)); got != 0 {
		t.Errorf("%s: rows = %d, want none: %s", step, got, body)
	}
}

// assertSectorNames pins the sector list by name, the record's declared order.
func assertSectorNames(want ...string) func(t *testing.T, step string, body []byte) {
	return func(t *testing.T, step string, body []byte) {
		t.Helper()

		var got []string
		for _, row := range decodeRows(t, body) {
			got = append(got, cell[string](t, row, "name"))
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: sectors = %v, want %v", step, got, want)
		}
	}
}

// TestRunTimeTenant_chartedSectorReachesSecondInstance plays the scenario in order over
// two instances: a sector made at run time, by an invitation's acceptance, is served by
// both.
func TestRunTimeTenant_chartedSectorReachesSecondInstance(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	// The engines are the shipped role files over this database, provisioned with the
	// personas' memberships, so the surveyor's authority is the Surveyor role as it ships
	// and SectorCrew held in every sector, nothing scripted.
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

	// The personas' accounts, rows of the one database: the list of the roles held in a
	// sector shows the people who have one. One live service, two instances, each with a
	// roster started over it: Start loads the seeded sectors and subscribes to the tenants
	// kind.
	accounts := seedCrewAccounts(ctx, t, db)
	fake := live.NewFake()
	first, firstIdentity := startTenantInstance(t, "the first instance", db, crewEngine, membersEngine, fake)
	second, _ := startTenantInstance(t, "the second instance", db, crewEngine, membersEngine, fake)
	if got := fake.Subscribers(live.KindTenants); got != 2 {
		t.Fatalf("rosters subscribed to the %s kind = %d, want 2: Start follows the signals", live.KindTenants, got)
	}

	heldAsMarshal := func(t *testing.T, step string, body []byte) {
		t.Helper()
		assertRolesHeld(t, step, body, tenantSurveyor, string(marshalRole), "SectorCrew")
	}

	// The steps before Dawn exists. Nobody charts a sector directly: the create operation
	// is refused whoever sends it and whatever they hold, with how a sector is made, and
	// nothing is written or signaled.
	seeded := []accesstypes.Domain{anvil, bastion, cinder}
	before := []flagStep{
		{name: "before: the surveyor's chart lights the three seeded sectors, SectorCrew held in every one", on: first, user: tenantSurveyor, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(seeded...)},
		{name: "before: the sector list carries the three seeded sectors on the first instance", on: first, user: tenantSurveyor, method: http.MethodGet, target: sectorsRoute, wantStatus: http.StatusOK, check: assertSectorNames("Anvil", "Bastion", "Cinder")},
		{name: "before: and her chart on the second lights the same three", on: second, user: tenantSurveyor, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(seeded...)},
		{name: "the surveyor, who holds Create on Sectors, creates none directly", on: first, user: tenantSurveyor, method: http.MethodPatch, target: resourcesRoute, body: chartDawn, wantStatus: http.StatusBadRequest, check: assertRefusedCreate},
		{name: "nor does the marshal, who holds none: the refusal is the same", on: first, user: tenantMarshal, method: http.MethodPatch, target: resourcesRoute, body: chartDawn, wantStatus: http.StatusBadRequest, check: assertRefusedCreate},
		{name: "nor does a create that names a key in its path", on: first, user: tenantSurveyor, method: http.MethodPatch, target: resourcesRoute, body: chartDawnUnderAKey, wantStatus: http.StatusBadRequest, check: assertRefusedCreate},
		{name: "the refused creates wrote nothing: the sector list is the seeded three", on: first, user: tenantSurveyor, method: http.MethodGet, target: sectorsRoute, wantStatus: http.StatusOK, check: assertSectorNames("Anvil", "Bastion", "Cinder")},
	}
	for i := range before {
		runFlagStep(t, &before[i])
	}
	if got := fake.Signals(); len(got) != 0 {
		t.Fatalf("signals after the refused creates = %v, want none", got)
	}

	// The invited person's first sign-in makes Dawn, on the first instance: the sector
	// and her marshal's role in one transaction, then that instance's roster and one
	// signal of the tenants kind.
	if err := acceptSectorInvitation(ctx, t, db, firstIdentity, accounts[tenantSurveyor], tenantSurveyor, "Dawn", dawnInvited); err != nil {
		t.Fatalf("accepting the invitation that makes Dawn: %v", err)
	}
	if got := fake.Signals(); !slices.Equal(got, []live.Kind{live.KindTenants}) {
		t.Fatalf("signals after the acceptance = %v, want [%s]", got, live.KindTenants)
	}
	// A second invitation that carries the name Dawn is refused at its acceptance: a
	// sector's name is one sector's. Nothing is written and nothing is signaled.
	err = acceptSectorInvitation(ctx, t, db, firstIdentity, accounts[tenantMarshal], tenantMarshal, "Dawn", dawnInvited)
	if !httpio.HasConflict(err) || !strings.Contains(strings.Join(httpio.Messages(err), " "), "a sector with the name Dawn already exists") {
		t.Fatalf("accepting a second invitation that makes a sector named Dawn: error = %v, want a conflict saying the name is taken", err)
	}
	if got := fake.Signals(); !slices.Equal(got, []live.Kind{live.KindTenants}) {
		t.Fatalf("signals after the refused acceptance = %v, want the one of Dawn's making", got)
	}

	// Dawn's key is the id the record's create generated: every later step addresses
	// Dawn by it.
	var dawn string
	runFlagStep(t, &flagStep{
		name: "made: the sector list carries Dawn, in name order, once", on: first, user: tenantSurveyor, method: http.MethodGet, target: sectorsRoute, wantStatus: http.StatusOK,
		check: func(t *testing.T, step string, body []byte) {
			t.Helper()
			assertSectorNames("Anvil", "Bastion", "Cinder", "Dawn")(t, step, body)
			for _, row := range decodeRows(t, body) {
				if cell[string](t, row, "name") == "Dawn" {
					dawn = cell[string](t, row, "id")
				}
			}
			if _, err := impulse.UUIDFromString(dawn); err != nil {
				t.Fatalf("%s: Dawn's key is %q, want an id the record's create generated: %v", step, dawn, err)
			}
		},
	})

	// Dawn's list of the roles held, which carries its founder; the correction of the
	// sector once it is made, Update on Sectors; and the chart that lights Dawn.
	dawnRolesHeld := sectorPath(dawn, sectorRolesPath+"?limit=200")
	surveyDawn := `[{"op":"patch","path":"/sectors/` + dawn + `","value":{"region":"Far frontier"}}]`
	charted := sortedDomains(anvil, bastion, cinder, accesstypes.Domain(dawn))
	steps := []flagStep{
		{name: "made: her chart lights Dawn with no restart, no migrate job and no new login", on: first, user: tenantSurveyor, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(charted...)},
		{name: "made: Dawn's wings answer her on the instance that wrote, an empty list", on: first, user: tenantSurveyor, method: http.MethodGet, target: sectorPath(dawn, "wings"), wantStatus: http.StatusOK, check: assertNoRows},
		{name: "made: the surveyor corrects Dawn's region, Update on Sectors", on: first, user: tenantSurveyor, method: http.MethodPatch, target: resourcesRoute, body: surveyDawn, wantStatus: http.StatusOK},
		{name: "made: the acceptance made the surveyor Dawn's marshal in its own transaction, so Dawn's list of the roles held answers her and carries her as SectorMarshal", on: first, user: tenantSurveyor, method: http.MethodGet, target: dawnRolesHeld, wantStatus: http.StatusOK, settle: true, check: heldAsMarshal},
		{name: "made: the second instance's engine knows the membership too, one store under both", on: second, user: tenantSurveyor, method: http.MethodGet, target: dawnRolesHeld, wantStatus: http.StatusOK, settle: true, check: heldAsMarshal},
		{name: "the marshal's chart does not light Dawn: the refused acceptance seated nobody", on: first, user: tenantMarshal, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(anvil)},
		{name: "Dawn's routes answer the marshal 404: a sector that does not exist for her", on: first, user: tenantMarshal, method: http.MethodGet, target: sectorPath(dawn, "wings"), wantStatus: http.StatusNotFound},
		{name: "the governor, marshal in every sector, sees Dawn", on: first, user: tenantGovernor, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(charted...)},
		{name: "Dawn's flight deck answers the governor, empty", on: first, user: tenantGovernor, method: http.MethodGet, target: sectorPath(dawn, "missions"), wantStatus: http.StatusOK, check: assertNoRows},
		{name: "the archivist, held in every sector, sees Dawn", on: first, user: tenantArchivist, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(charted...)},
		{name: "the client's portal lists Dawn at once: the directory's role is held in every sector", on: first, user: clientUser, method: http.MethodGet, target: portalUserDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(charted...)},
		{name: "Dawn's tracker opens for her, empty", on: first, user: clientUser, method: http.MethodGet, target: portalPath(dawn, "missions"), wantStatus: http.StatusOK, check: assertNoRows},
		{name: "the second instance serves Dawn at its next request: its roster reloaded on the tenants kind's signal, not the backstop", on: second, user: tenantSurveyor, method: http.MethodGet, target: sectorPath(dawn, "wings"), wantStatus: http.StatusOK, settle: true, check: assertNoRows},
		{name: "and her chart there lights Dawn", on: second, user: tenantSurveyor, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, check: assertDomains(charted...)},
		{name: "and Dawn stays a sector that does not exist for the marshal there", on: second, user: tenantMarshal, method: http.MethodGet, target: sectorPath(dawn, "wings"), wantStatus: http.StatusNotFound},
	}
	for i := range steps {
		runFlagStep(t, &steps[i])
	}
}
