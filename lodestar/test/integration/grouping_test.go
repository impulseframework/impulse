// Demonstrates: write-grouping, condition.old-vs-new.
package integration

// grouping_test (design plan §9): the dispatcher's two-grant PATCH. Grant A covers
// assignedSquadronId under `state IN ('open', 'claimed') AND new.assignedSquadron IN
// subject.squadrons`; grant B covers notes and deadline under `state NOT IN (...) AND
// new.deadline >= deadline`. One PATCH touching both field sets is checked as AND
// across the groups, a notes-only patch reads the untouched deadline as itself (the
// tautology), an assignment to a foreign squadron fails and the message says the
// failing group's columns in a person's words and that the value sent is what may not be
// set, and nothing partially commits.

import (
	"fmt"
	"net/http"
	"testing"

	"cloud.google.com/go/spanner"

	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

func TestDispatcherWriteGrouping(t *testing.T) {
	t.Parallel()

	ctx, db, h := demoWorld(t)

	// The two grants this suite proves, A then B, each pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "Dispatcher", "Update", "Missions", "state IN ('open', 'claimed') AND new.assignedSquadron IN subject.squadrons")
	provesGrant(t, crew.Roles(), "Dispatcher", "Update", "Missions", "state NOT IN ('completed', 'failed', 'stood_down') AND new.deadline >= deadline")

	patch := func(mission, value string) (int, []byte) {
		return doRequestAs(t, h, "dispatcher", http.MethodPatch, "/console/api/resources",
			fmt.Sprintf(`[{"op":"patch","path":%q,"value":%s}]`, opPath(anvil, "missions/"+mission), value))
	}

	// Deliberately not a table: each step reads the row the previous one left.

	// Both groups pass: Dunn flies with Hammer and Tongs, the hauler is open, and the
	// deadline moves out.
	status, body := patch(missionHaulerID, fmt.Sprintf(`{"assignedSquadronId":%q,"notes":"Hammer takes it","deadline":%q}`, squadronHammerID, deadline(16)))
	assertStatus(t, status, http.StatusOK, body)

	// Notes-only on a claimed mission: grant B's deadline term degenerates to the
	// tautology, grant A is not consulted because its columns are untouched.
	status, body = patch(missionCorvidID, `{"notes":"Client called twice"}`)
	assertStatus(t, status, http.StatusOK, body)

	// A deadline pulled in (the convoy is seeded at +25 days) fails the old-vs-new term,
	// and the refusal says the value is what may not be set: the mission would take a
	// later deadline.
	status, body = patch(missionConvoyID, `{"deadline":"`+deadline(24)+`"}`)
	assertStatus(t, status, http.StatusForbidden, body)
	if got, want := refusalMessage(t, body), "You may change the deadline of this mission, but not to this value."; got != want {
		t.Errorf("refusal = %q, want %q: the value sent is what the grant refuses", got, want)
	}
	// ...and pushed out passes, even on an underway mission (grant B has no state
	// restriction beyond the terminal states).
	status, body = patch(missionConvoyID, `{"deadline":"`+deadline(26)+`"}`)
	assertStatus(t, status, http.StatusOK, body)

	// Assignment to a squadron Dunn does not fly with fails grant A, and the refusal
	// says the group's column in a person's words and that the value is what may not be
	// set: an update's own conditions are asked before the squadron it names, so Ashfall,
	// a squadron of another sector, is refused by grant A and not as a row that is not
	// there. The notes in the same PATCH do not commit.
	before := readColumn[spanner.NullString](ctx, t, db, "Missions", spanner.Key{missionQuarantineID}, "Notes")
	status, body = patch(missionQuarantineID, fmt.Sprintf(`{"assignedSquadronId":%q,"notes":"partial?"}`, squadronAshfallID))
	assertStatus(t, status, http.StatusForbidden, body)
	if got, want := refusalMessage(t, body), "You may change the assigned squadron id of this mission, but not to this value."; got != want {
		t.Errorf("refusal = %q, want %q: the failing group's column in a person's words", got, want)
	}
	after := readColumn[spanner.NullString](ctx, t, db, "Missions", spanner.Key{missionQuarantineID}, "Notes")
	if before != after {
		t.Errorf("notes changed from %v to %v on a refused batch: nothing may partially commit", before, after)
	}

	// Assignment on an underway mission fails grant A's state term even to an own
	// squadron, and the refusal is the row's: no squadron sent would be taken.
	status, body = patch(missionConvoyID, fmt.Sprintf(`{"assignedSquadronId":%q}`, squadronTongsID))
	assertStatus(t, status, http.StatusForbidden, body)
	if got, want := refusalMessage(t, body), "You may change the assigned squadron id of some missions here, but not of this one."; got != want {
		t.Errorf("refusal = %q, want %q: the mission's state is what the grant refuses", got, want)
	}

	// Terminal missions are outside both grants (and outside the dispatcher's view).
	status, body = patch(missionPodID, `{"notes":"too late"}`)
	assertStatus(t, status, http.StatusForbidden, body)
}
