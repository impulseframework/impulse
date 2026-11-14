// Demonstrates: live.signals, auth.generated-identity.
package integration

// This suite pins the policy kind of the one signal channel. Two instances of the
// application run over one database, each over its own crew engine on the crew store,
// both engines wired with the policy signal over one in-memory live service (live.Fake,
// which fans a signal out to every subscription) and their heartbeat pinned to an hour,
// so only the signal can carry a change between them. The marshal gives the cadet the
// harbormaster's role at Anvil through the first instance (the crew's generated
// management: AssignCrewSectorRole, Execute in the sector, held by SectorMarshal for
// every role but its own); the engine of the instance that wrote announces the policy
// kind once the membership has committed, and the second instance serves the cadet the
// fleet board at its next request, with no restart and no heartbeat; the role taken
// away, it refuses again. The border holds on the way: the cadet cannot give herself a
// role, the marshal cannot mint a marshal, and Bastion is a sector that does not exist
// for the marshal. The steps run in order, each named in its failure,
// since every one reads what the ones before it wrote. The walkthrough plays the same
// scenario against the Firestore emulator with a second server process.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/auth"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
	"github.com/impulseframework/impulse/resource/live"
)

// The suite's cast, the shipped personas: the marshal holds SectorMarshal at Anvil and
// with it the sector's role methods; the cadet holds no List on Ships until she is
// given the harbormaster's role.
const (
	policyMarshal accesstypes.User = "marshal"
	policyCadet   accesstypes.User = "cadet"
	policyHollis  accesstypes.User = "harbormaster"

	harbormasterRole accesstypes.Role = "Harbormaster"
	marshalRole      accesstypes.Role = "SectorMarshal"

	// policyHeartbeat is the engines' heartbeat in the suite: far beyond the settle
	// bound, so a reload on the second instance can only have come from the signal.
	policyHeartbeat = time.Hour
)

// The sector's two role methods of the crew's generated management, by route.
const (
	assignSectorRoleMethod = "assign-crew-sector-role"
	removeSectorRoleMethod = "remove-crew-sector-role"
)

// sectorRoleBody is the request of a sector's role method: the person by their
// account's id, and the role.
func sectorRoleBody(user impulse.UUID, role accesstypes.Role) string {
	return fmt.Sprintf(`{"userId":%q,"role":%q}`, user, role)
}

// newPolicyEngine opens a crew engine over the store with the policy signal and the
// suite's heartbeat, and waits for its first snapshot as crew.New does, which starts its
// watch on the policy kind.
func newPolicyEngine(ctx context.Context, t *testing.T, db *initiator.SpannerDB, signal access.ChangeSignal) *access.Client {
	t.Helper()

	engine, err := openEngineWith(db, crew.TablePrefix, crew.Roles(), access.WithChangeSignal(signal), access.WithHeartbeatInterval(policyHeartbeat))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Errorf("access.Client.Close() error = %v", err)
		}
	})
	if err := engine.WaitReady(ctx); err != nil {
		t.Fatalf("access.Client.WaitReady() error = %v", err)
	}

	return engine
}

// startPolicyInstance builds an App over the database, its own crew engine, the shared
// members engine and the shared live service, starts it, and serves it through the test
// router with the hand-written routes, where the generated management's routes are
// mounted with every other. The crew's identity client stands over the instance's own
// engine, so a membership written on one instance is announced by that instance's
// engine, as production's is.
func startPolicyInstance(t *testing.T, name string, db *initiator.SpannerDB, crewEngine *access.Client, membersEngine access.Controller, svc live.Service) *flagInstance {
	t.Helper()

	a := app.New(&testConfigurer{
		db:            db,
		access:        crewEngine,
		membersAccess: membersEngine,
		live:          svc,
	})
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}

	return &flagInstance{name: name, h: withHandWrittenRoutes(a)}
}

// waitForSubscribers blocks until the fake holds the wanted subscriptions to the kind:
// an engine's watch subscribes on its own goroutine once the engine starts.
func waitForSubscribers(t *testing.T, fake *live.Fake, kind live.Kind, want int) {
	t.Helper()

	deadline := time.Now().Add(settleWithin)
	for fake.Subscribers(kind) != want {
		if time.Now().After(deadline) {
			t.Fatalf("subscriptions to the %s kind = %d, want %d", kind, fake.Subscribers(kind), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// policySignals counts the policy kind's signals the fake received.
func policySignals(fake *live.Fake) int {
	n := 0
	for _, kind := range fake.Signals() {
		if kind == live.KindPolicy {
			n++
		}
	}

	return n
}

// TestPolicySignal_roleReachesSecondInstance plays the scenario in order over two
// instances.
func TestPolicySignal_roleReachesSecondInstance(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	// One live service, one policy signal over it, two crew engines over one store: the
	// shape two instances of the application have.
	fake := live.NewFake()
	policySignal := auth.PolicySignal(fake)
	writer := newPolicyEngine(ctx, t, db, policySignal)
	follower := newPolicyEngine(ctx, t, db, policySignal)
	waitForSubscribers(t, fake, live.KindPolicy, 2)

	// The personas' memberships are written through the writer, which announces each;
	// the follower, whose first snapshot may predate them, learns them the same way the
	// scenario's membership travels, and the marshal's authority over Anvil's roles on
	// it is the precondition. The personas' accounts are rows of the one database, so
	// the cadet's id is one on both instances.
	membersEngine, err := openEngine(db, members.TablePrefix, members.Roles())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := membersEngine.Close(); err != nil {
			t.Errorf("access.Client.Close() error = %v", err)
		}
	})
	if err := provisionDemoAccess(ctx, writer, membersEngine); err != nil {
		t.Fatal(err)
	}
	if err := waitForDecision(ctx, follower, policyMarshal, anvil, accesstypes.Execute, "AssignCrewSectorRole"); err != nil {
		t.Fatalf("the follower never learned the marshal's authority over the sector's roles from the writer's signals: %v", err)
	}
	cadet := seedCrewAccounts(ctx, t, db)[policyCadet]

	first := startPolicyInstance(t, "the first instance", db, writer, membersEngine, fake)
	second := startPolicyInstance(t, "the second instance", db, follower, membersEngine, fake)
	signalsBefore := policySignals(fake)
	assign, remove, rolesHeld := sectorPath(anvil, assignSectorRoleMethod), sectorPath(anvil, removeSectorRoleMethod), sectorPath(anvil, sectorRolesPath+"?limit=200")

	steps := []flagStep{
		{name: "before: the cadet holds no List on Ships on the second instance", on: second, user: policyCadet, method: http.MethodGet, target: sectorPath(anvil, "ships"), wantStatus: http.StatusForbidden},
		{
			name: "before: the marshal lists the roles held at Anvil, the cadet's among them", on: first, user: policyMarshal, method: http.MethodGet, target: rolesHeld, wantStatus: http.StatusOK,
			check: func(t *testing.T, step string, body []byte) {
				t.Helper()
				assertRolesHeld(t, step, body, policyCadet, "Cadet", "SectorCrew")
				assertRolesHeld(t, step, body, policyHollis, string(harbormasterRole), "SectorCrew")
			},
		},
		{name: "the cadet cannot give herself a role: no Execute on the sector's role method", on: first, user: policyCadet, method: http.MethodPost, target: assign, body: sectorRoleBody(cadet, harbormasterRole), wantStatus: http.StatusForbidden},
		{name: "the marshal cannot mint a marshal: the grant's condition on the role", on: first, user: policyMarshal, method: http.MethodPost, target: assign, body: sectorRoleBody(cadet, marshalRole), wantStatus: http.StatusForbidden},
		{name: "Bastion does not exist for the marshal: the concealed-sector guard", on: first, user: policyMarshal, method: http.MethodPost, target: sectorPath(bastion, assignSectorRoleMethod), body: sectorRoleBody(cadet, harbormasterRole), wantStatus: http.StatusNotFound},
		{
			name: "the marshal gives the cadet the harbormaster's role at Anvil: one signal of the policy kind", on: first, user: policyMarshal, method: http.MethodPost, target: assign, body: sectorRoleBody(cadet, harbormasterRole), wantStatus: http.StatusNoContent,
			check: func(t *testing.T, step string, _ []byte) {
				t.Helper()
				// The engine announces after the commit on its own goroutine.
				deadline := time.Now().Add(settleWithin)
				for policySignals(fake) <= signalsBefore {
					if time.Now().After(deadline) {
						t.Fatalf("%s: no signal of the %s kind followed the write", step, live.KindPolicy)
					}
					time.Sleep(20 * time.Millisecond)
				}
			},
		},
		{
			name: "given: the list of the roles held carries it", on: first, user: policyMarshal, method: http.MethodGet, target: rolesHeld, wantStatus: http.StatusOK,
			check: func(t *testing.T, step string, body []byte) {
				t.Helper()
				assertRolesHeld(t, step, body, policyCadet, "Cadet", string(harbormasterRole), "SectorCrew")
			},
		},
		{name: "given: the instance that wrote serves her the fleet board at once", on: first, user: policyCadet, method: http.MethodGet, target: sectorPath(anvil, "ships"), wantStatus: http.StatusOK},
		{name: "given: the second instance serves her the fleet board at its next request, with no restart and no heartbeat", on: second, user: policyCadet, method: http.MethodGet, target: sectorPath(anvil, "ships"), wantStatus: http.StatusOK, settle: true},
		{name: "the marshal takes the role away", on: first, user: policyMarshal, method: http.MethodPost, target: remove, body: sectorRoleBody(cadet, harbormasterRole), wantStatus: http.StatusNoContent},
		{name: "taken away: the instance that wrote refuses again", on: first, user: policyCadet, method: http.MethodGet, target: sectorPath(anvil, "ships"), wantStatus: http.StatusForbidden},
		{name: "taken away: the second instance refuses at its next request", on: second, user: policyCadet, method: http.MethodGet, target: sectorPath(anvil, "ships"), wantStatus: http.StatusForbidden, settle: true},
	}
	for i := range steps {
		runFlagStep(t, &steps[i])
	}
}
