//go:build skipAuth

package integration

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
)

// This suite holds what a sign-in clears, on the served stack. A role given for a period
// counts inside it alone: the moment it ends, the engine, the session's list of sectors
// and a sector's list of its people leave it out, and nothing is written. The row itself
// goes when its person next signs in: the crew auth sets the generated identity client's
// hook for a sign-in that stands (crewidentity.Client.SignedIn on
// session.Auth.SetSignedInHook), which removes the person's ended roles in a transaction
// of its own, once the session is committed. It removes those rows and nothing else,
// announces no change of the policy, since a role that has ended already counts for
// nothing, and leaves the account, also when the ended role was the last thing the
// person held.
//
// The person is the cast's relief officer, who holds the cadet's roles at Anvil for a
// tour. The tour is brought to its end through the engine, with the period of a tour
// that ended an hour ago: nothing served gives a period that is over already.

// rolesStanding counts the rows the crew's memberships table holds for the account, of
// the kind given, read straight from the database.
func rolesStanding(ctx context.Context, t *testing.T, s *served, user accesstypes.User, kind string) int64 {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, spanner.Statement{
		SQL:    "SELECT COUNT(*) FROM CrewUserRoles WHERE UserId = @user AND Kind = @kind",
		Params: map[string]any{"user": string(user), "kind": kind},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the roles of %s: %v", user, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of the roles of %s: %v", user, err)
	}

	return n
}

// TestSignInPeriods_endedRolesGoAtTheNextSignIn follows the relief officer from her tour
// to the sign-in after it: an open session loses the roles at its next request, her
// sectors and Anvil's people leave her out while the rows stand, and her next sign-in
// removes them and nothing else.
//
// Demonstrates: auth.periods.
func TestSignInPeriods_endedRolesGoAtTheNextSignIn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	loadCast()
	id, ok := crewAccounts["relief"]
	if !ok {
		t.Fatalf("%s names no crew account under the username relief", usersPath)
	}
	relief := accesstypes.User(id.String())
	manager := s.access.UserManager()
	const (
		ofTheFleet = "global"
		ofASector  = "domain"
	)
	board := sectorPath(anvil, "missions")
	people := func(b *browser) []string {
		t.Helper()
		status, body := b.do(ctx, http.MethodGet, sectorPath(anvil, sectorPeoplePath+"?limit=200"), nil)
		if status != http.StatusOK {
			t.Fatalf("listing Anvil's people: status %d: %s", status, body)
		}

		return idsOf(t, decodeRows(t, body))
	}
	account := func() bool {
		t.Helper()

		return countRowsOf(ctx, t, s, "CrewSessionUsers", "Id", id.String()) == 1
	}

	// On her tour. Her sectors hold Anvil, its board answers her, and the Marshal's list
	// of Anvil's people holds her. A sign-in with nothing to clear clears nothing.
	rae := newBrowser(t, s, consoleAPI)
	rae.signIn(ctx, "relief")
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "marshal")
	if got := rae.domains(ctx); !slices.Contains(got, anvil) {
		t.Fatalf("the relief officer's sectors on her tour = %v, want Anvil among them", got)
	}
	if status, body := rae.do(ctx, http.MethodGet, board, nil); status != http.StatusOK {
		t.Fatalf("Anvil's board on her tour: status %d: %s", status, body)
	}
	if got := people(hq); !slices.Contains(got, id.String()) {
		t.Fatalf("Anvil's people on her tour = %v, want the relief officer among them", got)
	}
	if fleet, sector := rolesStanding(ctx, t, s, relief, ofTheFleet), rolesStanding(ctx, t, s, relief, ofASector); fleet != 2 || sector != 2 {
		t.Fatalf("the relief officer's roles after a sign-in on her tour = %d for the fleet and %d at a sector, want 2 and 2: nothing had ended", fleet, sector)
	}

	// The tour ends. Her open session loses Anvil at its next request, as it does when a
	// role is taken away: the sector is one she holds nothing in, so it is concealed.
	over := accesstypes.Period{Start: time.Now().UTC().Add(-2 * time.Hour), End: time.Now().UTC().Add(-time.Hour)}
	for _, role := range []accesstypes.Role{"Cadet", "SectorCrew"} {
		if err := manager.AssignUserRole(ctx, accesstypes.DomainPolicyScope(anvil), relief, role, over); err != nil {
			t.Fatalf("access.UserManager.AssignUserRole(%s) error = %v", role, err)
		}
	}
	deadline := time.Now().Add(settleWithin)
	for {
		status, body := rae.do(ctx, http.MethodGet, board, nil)
		if status == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Anvil's board after her tour: status %d, want 404 for a sector she holds nothing in: %s", status, body)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The rows stand, and everything that reads them leaves them out: her sectors, and
	// Anvil's list of its people.
	if got := rolesStanding(ctx, t, s, relief, ofASector); got != 2 {
		t.Fatalf("the rows of her tour after it ended = %d, want 2: nothing removes a row when its period ends", got)
	}
	if got := rae.domains(ctx); slices.Contains(got, anvil) {
		t.Errorf("the relief officer's sectors after her tour = %v, want Anvil gone", got)
	}
	if got := people(hq); slices.Contains(got, id.String()) {
		t.Errorf("Anvil's people after her tour = %v, want the relief officer gone", got)
	}

	// Her next sign-in removes the two rows and nothing else. The policy is not
	// announced: no instance has anything to reload.
	announced := policySignals(s.live)
	again := newBrowser(t, s, consoleAPI)
	again.signIn(ctx, "relief")
	if fleet, sector := rolesStanding(ctx, t, s, relief, ofTheFleet), rolesStanding(ctx, t, s, relief, ofASector); fleet != 2 || sector != 0 {
		t.Errorf("the relief officer's roles after her next sign-in = %d for the fleet and %d at a sector, want 2 and 0: the ended rows go and nothing else", fleet, sector)
	}
	if got := policySignals(s.live); got != announced {
		t.Errorf("the policy was announced %d time(s) by the sign-in that cleared her ended roles, want none", got-announced)
	}
	if sess := again.session(ctx); sess["authenticated"] != true || sess["username"] != "relief" {
		t.Errorf("the session of the sign-in that cleared her ended roles = %v, want authenticated as relief", sess)
	}

	// Her roles for the fleet end too, and she holds nothing. Lodestar's crew admits a
	// person who holds no sector, so she signs in; the sign-in clears the last rows she
	// had, and her account stays.
	for _, role := range []accesstypes.Role{"CrewCommon", "ClientBrowser"} {
		if err := manager.AssignUserRole(ctx, accesstypes.GlobalPolicyScope(), relief, role, over); err != nil {
			t.Fatalf("access.UserManager.AssignUserRole(%s) error = %v", role, err)
		}
	}
	if got := rolesStanding(ctx, t, s, relief, ofTheFleet); got != 2 {
		t.Fatalf("the rows of her roles for the fleet after they ended = %d, want 2", got)
	}
	last := newBrowser(t, s, consoleAPI)
	last.signIn(ctx, "relief")
	if got := countRowsOf(ctx, t, s, "CrewUserRoles", "UserId", string(relief)); got != 0 {
		t.Errorf("the relief officer's roles after the sign-in that found every one ended = %d, want none", got)
	}
	if !account() {
		t.Error("the relief officer's account went with her last ended role: a sign-in removes ended roles and never the account")
	}
	if sess := last.session(ctx); sess["authenticated"] != true || sess["username"] != "relief" {
		t.Errorf("the session of a person whose every role had ended = %v, want authenticated as relief", sess)
	}
}
