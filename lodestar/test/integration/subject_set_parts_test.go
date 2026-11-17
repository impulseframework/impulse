// Demonstrates: @subjectSet.who, @subjectSet.where, @subjectSet.on-set.
package integration

// The armorer's suite: a persona whose whole view of the hangars comes through the two
// sets the hangar passes yield. He holds no pass that counts in his own name; his one
// tie is his squadron, Tongs, so every row he reaches he reaches through a set that
// follows another set (SquadronID IN subject.squadrons), and what he reaches is decided
// by the pass's level and its expiry (the where: of each set).
//
// The seeded passes, read for him (schema/devseed):
//
//	1  Tongs runs Anvil Dock One, standing
//	2  Tongs visits the Quarantine Bay, standing
//	3  the armorer himself ran the Quarantine Bay until 2026-01-01
//	4  Hammer runs the Quarantine Bay
//	5  Portcullis visits Bastion Slip (Bastion's row)
//
// So subject.hangarsOpen is the dock and the bay, and subject.hangarsRun is the dock.

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

const (
	armorer = accesstypes.User("armorer")

	// The armorer's own pass to run the Quarantine Bay, which expired before the seed.
	passArmorerQuarantineID = "61000000-0000-4000-8000-000000000003"
)

// provesArmorerGrants pins the three grants the suite proves to the role file
// (conditions-proven).
func provesArmorerGrants(t *testing.T) {
	t.Helper()

	provesGrant(t, crew.Roles(), "Armorer", "List", "Hangars", "hangar IN subject.hangarsOpen")
	provesGrant(t, crew.Roles(), "Armorer", "Read", "Hangars", "hangar IN subject.hangarsOpen")
	provesGrant(t, crew.Roles(), "Armorer", "Update", "Hangars", "hangar IN subject.hangarsRun")
}

// TestSubjectSetParts_reads holds what the armorer sees: the hangars a pass opens to his
// squadron and no other, with the keyed read deciding the same way as the list.
func TestSubjectSetParts_reads(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)
	provesArmorerGrants(t)

	t.Run("the list is the hangars open to him: both through Tongs, at either level", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, armorer, http.MethodGet, sectorPath(anvil, "hangars"), "")
		assertStatus(t, status, http.StatusOK, body)
		want := []string{hangarAnvilDockID, hangarQuarantineID}
		if got := idsOf(t, decodeRows(t, body)); !slices.Equal(got, want) {
			t.Errorf("hangars = %v, want %v", got, want)
		}
	})

	reads := []struct {
		name   string
		hangar string
	}{
		{name: "he reads the dock his squadron runs", hangar: hangarAnvilDockID},
		{name: "he reads the bay his squadron visits", hangar: hangarQuarantineID},
	}
	for _, tt := range reads {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, h, armorer, http.MethodGet, sectorPath(anvil, "hangars/"+tt.hangar), "")
			assertStatus(t, status, http.StatusOK, body)
		})
	}

	t.Run("a sector where he holds no role is concealed, whatever its passes say", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, armorer, http.MethodGet, sectorPath(bastion, "hangars"), "")
		assertStatus(t, status, http.StatusNotFound, body)
	})
}

// TestSubjectSetParts_writes is the mutating half, on a world of its own: the run level
// decides who re-zones a hangar, a pass that has expired counts for nothing until it is
// extended, and a membership that ends takes every hangar with it. Each step changes
// what the next one reads, so the steps are one test and each failure names its step.
func TestSubjectSetParts_writes(t *testing.T) {
	t.Parallel()

	ctx, db, h := demoWorld(t)
	provesArmorerGrants(t)

	rezone := func(step, hangar, zone string, wantStatus int) {
		t.Helper()

		status, body := doRequestAs(t, h, armorer, http.MethodPatch, sectorPath(anvil, "hangars"),
			fmt.Sprintf(`[{"op":"patch","path":"/%s","value":{"zone":%q}}]`, hangar, zone))
		if status != wantStatus {
			t.Fatalf("%s: status = %d, want %d: %s", step, status, wantStatus, body)
		}
	}
	zoneOf := func(hangar string) string {
		t.Helper()

		return readColumn[string](ctx, t, db, "Hangars", spanner.Key{hangar}, "Zone")
	}

	rezone("he re-zones the dock, which Tongs holds at the run level", hangarAnvilDockID, "apron", http.StatusOK)
	if got := zoneOf(hangarAnvilDockID); got != "apron" {
		t.Errorf("the dock's zone = %q, want apron", got)
	}

	rezone("he cannot re-zone the bay: Tongs visits it, Hammer runs it, and his own pass has expired", hangarQuarantineID, "dock", http.StatusForbidden)
	if got := zoneOf(hangarQuarantineID); got != "quarantine" {
		t.Errorf("the bay's zone = %q after the refused write, want quarantine", got)
	}

	until := time.Now().UTC().AddDate(0, 1, 0)
	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.UpdateMap("HangarPasses", map[string]any{"Id": passArmorerQuarantineID, "ExpiresAt": until})}); err != nil {
		t.Fatalf("extending his own pass: %v", err)
	}
	rezone("his own pass counts again once it is extended", hangarQuarantineID, "isolation", http.StatusOK)

	if _, err := db.Apply(ctx, []*spanner.Mutation{spanner.Delete("SquadronMemberships", spanner.Key{squadronTongsID, string(account(t, armorer))})}); err != nil {
		t.Fatalf("removing the armorer from Tongs: %v", err)
	}
	status, body := doRequestAs(t, h, armorer, http.MethodGet, sectorPath(anvil, "hangars"), "")
	assertStatus(t, status, http.StatusOK, body)
	if got, want := idsOf(t, decodeRows(t, body)), []string{hangarQuarantineID}; !slices.Equal(got, want) {
		t.Errorf("out of Tongs, the hangars open to him = %v, want %v: the bay alone, by his own pass", got, want)
	}
	rezone("out of Tongs, the dock the squadron runs is no longer his", hangarAnvilDockID, "dock", http.StatusForbidden)
}
