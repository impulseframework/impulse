package integration

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewhandlers"
	"github.com/impulseframework/impulse/resource/live"
)

// The live half of an auth's generated management: a commit of one of its methods is
// published with the live derivations the application hands the auth's handlers
// (crewhandlers.New), so a view that reads the auth's tables is told of the change and
// its open pages refetch.
//
// Lodestar's own derivations hold such views: the lists a sector keeps of the crew
// (CrewSectorUsers, CrewSectorUserRoles, CrewSectorInvitations, CrewSectorIdentities),
// which the generated management declares and records in crewhandlers.LiveDerivations,
// one of the values the application's assembly merges. The test holds that path whole,
// from a method's commit through the application's own route to the tab that has a
// sector's list open. It also holds the hand-over by itself, with a fixture in place of the
// application's derivations: one derived resource over the crew's memberships, the
// table named as text, which is how the record of a view of the application's own names
// a table of an auth (the type over it is internal to the generated tree).

// crewSeatBoard is the fixture's derived resource: a board of who is seated where,
// which an application would declare as a view of its own over the crew's memberships.
const crewSeatBoard = accesstypes.Resource("CrewSeatBoards")

// The sector's lists of the crew, as the generated management names them.
const (
	crewSectorPeople     = accesstypes.Resource("CrewSectorUsers")
	crewSectorRoles      = accesstypes.Resource("CrewSectorUserRoles")
	crewSectorIdentities = accesstypes.Resource("CrewSectorIdentities")
)

// newIdentityWorldWithLive is newIdentityWorld over a live service the test reads: a
// world of the test's own with the real engines, since the method checks the role it
// gives against the roles the store holds.
func newIdentityWorldWithLive(t *testing.T, svc live.Service) *identityWorld {
	t.Helper()
	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	crewEngine, err := openEngine(db, crew.TablePrefix, crew.Roles())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = crewEngine.Close() })
	membersEngine, err := openEngine(db, members.TablePrefix, members.Roles())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = membersEngine.Close() })
	if err := provisionDemoAccess(ctx, crewEngine, membersEngine); err != nil {
		t.Fatal(err)
	}
	a := app.New(&testConfigurer{db: db, access: crewEngine, membersAccess: membersEngine, live: svc})

	return &identityWorld{db: db, app: a, h: withHandWrittenRoutes(a), engine: crewEngine}
}

// TestGeneratedIdentityPublishesDerived follows three commits of the crew's generated
// methods to the tabs that are told of them. A person created through the application's
// own route writes an account and a membership, and the sector's lists that read those
// tables are told in every sector, since neither table names a sector whose rows it
// moved. A role given through handlers constructed with the fixture's derivations tells
// the fixture's board and nothing else: the handlers publish with what they were handed.
// And a role given through the application's route tells the sector's lists again.
func TestGeneratedIdentityPublishesDerived(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	fake := live.NewFake()
	w := newIdentityWorldWithLive(t, fake)

	// A tab in each sector has the sector's roles and the fixture's board open.
	expiry := time.Now().Add(time.Hour)
	if err := fake.Register(ctx, []live.Subscription{
		{Principal: principalID(liveHarbormaster), Tab: liveTabAnvil, Resource: crewSectorRoles, Domain: anvil, Expiry: expiry},
		{Principal: principalID(liveHarbormaster), Tab: liveTabAnvil, Resource: crewSeatBoard, Domain: anvil, Expiry: expiry},
		{Principal: principalID(liveBastionPilot), Tab: liveTabBastion, Resource: crewSectorRoles, Domain: bastion, Expiry: expiry},
		{Principal: principalID(liveBastionPilot), Tab: liveTabBastion, Resource: crewSeatBoard, Domain: bastion, Expiry: expiry},
	}); err != nil {
		t.Fatalf("live.Fake.Register() error = %v", err)
	}
	told := func(user accesstypes.User, res accesstypes.Resource) bool {
		return slices.Contains(changeNames(fake.Changes(principalID(user))), "resource|"+string(res)+"|||false")
	}
	lists := []live.WholeChange{{Resource: crewSectorIdentities, AllDomains: true}, {Resource: crewSectorRoles, AllDomains: true}, {Resource: crewSectorPeople, AllDomains: true}}

	// The application's own route, with Lodestar's derivations. A person is created
	// holding a role, so the commit writes the users table and the memberships table:
	// the sector's people, the sector's roles and the identities of the sector's people
	// all read them.
	username := "dana-" + identitySuffix()
	status, body := doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/create-crew-user", `{"username":"`+username+`","roles":["CrewCommon"]}`)
	assertStatus(t, status, http.StatusOK, body)
	if got := wholeChanges(fake.Publishes()); !slices.Equal(got, lists) {
		t.Fatalf("derived changes after the create = %v, want %v", got, lists)
	}
	for _, user := range []accesstypes.User{liveHarbormaster, liveBastionPilot} {
		if !told(user, crewSectorRoles) {
			t.Errorf("%s's change set = %v: the tab holding the sector's roles was not told of the new person", user, changeNames(fake.Changes(principalID(user))))
		}
		if told(user, crewSeatBoard) {
			t.Errorf("%s's change set = %v: the board is in the fixture's derivations, not in Lodestar's", user, changeNames(fake.Changes(principalID(user))))
		}
	}

	// The crew's handlers as the application constructs them, over the fixture's
	// derivations in place of its own: the commit tells the board, in each sector, and
	// no list of Lodestar's.
	before := len(fake.Publishes())
	r := chi.NewRouter()
	r.Use(httpio.WithParams)
	r.Post("/assign-crew-role", crewhandlers.New(w.app, derivations()).AssignCrewRole())
	status, body = doRequestAs(t, r, "governor", http.MethodPost, "/assign-crew-role", `{"username":"`+username+`","role":"Cadet","domain":"`+anvil+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	if got, want := wholeChanges(fake.Publishes()[before:]), []live.WholeChange{{Resource: crewSeatBoard, AllDomains: true}}; !slices.Equal(got, want) {
		t.Errorf("derived changes through the fixture's handlers = %v, want %v", got, want)
	}
	for _, user := range []accesstypes.User{liveHarbormaster, liveBastionPilot} {
		if !told(user, crewSeatBoard) {
			t.Errorf("%s's change set = %v: the board's tab was not told of the membership", user, changeNames(fake.Changes(principalID(user))))
		}
	}

	// The application's own route again: the commit writes the one membership, and the
	// sector's lists that read the memberships are told.
	before = len(fake.Publishes())
	status, body = doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role", `{"username":"`+username+`","role":"Cadet","domain":"`+bastion+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	publishes := fake.Publishes()
	if len(publishes) != before+1 {
		t.Fatalf("the application's route published %d times, want once", len(publishes)-before)
	}
	last := publishes[len(publishes)-1]
	if len(last.Writes.Rows["CrewUserRoles"]) != 1 {
		t.Errorf("the application's route published rows %v, want the one membership written", last.Writes.Rows)
	}
	if !slices.Equal(last.Writes.Whole, lists) {
		t.Errorf("the application's route published derived changes %v, want %v", last.Writes.Whole, lists)
	}
}

// derivations is the fixture's record: the board, read from the crew's memberships.
func derivations() *live.Derivations {
	return &live.Derivations{Resources: []live.DerivedResource{
		{Resource: crewSeatBoard, Tables: []live.SourceTable{{Name: "CrewUserRoles", Global: true}}},
	}}
}

// wholeChanges gathers the derived resources the publishes changed wholesale, in order.
func wholeChanges(publishes []live.FakePublish) []live.WholeChange {
	var whole []live.WholeChange
	for _, publish := range publishes {
		whole = append(whole, publish.Writes.Whole...)
	}

	return whole
}
