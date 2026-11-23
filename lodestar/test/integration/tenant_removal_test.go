package integration

// tenant_removal_test: a sector's administrator removes a person from the sector in one
// call, RemoveCrewSectorUser, which the generated management of the crew auth writes
// because the application has a tenant record and gives roles by hand.
//
// The call takes every role of the person's own held in the sector and their place in
// each of its groups, and nothing held elsewhere; the account goes with it only where the
// person holds nothing else. It declares a warning, asked with X-Warning: true, which
// says what goes in the server's words and writes nothing. It names a row of the sector's
// people (CrewSectorUsers), so a person of another sector is not found, and nobody
// removes themselves with it (@notOnOwnAccount). Each role it takes is held to the grant
// of the method that takes one role away (RemoveCrewSectorRole), so a Marshal removes no
// other Marshal.
//
// The people are the ones of the lists' suite (generated_identity_lists_test): ada a
// Cadet at Anvil alone, cy an Archivist in every sector, di a Veteran at Bastion; the
// test seats more of its own.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/resource"
)

// The removal's route under a sector, and the sentences the served stack answers with.
const (
	sectorRemovalPath    = "remove-crew-sector-user"
	removalKeepsAccount  = "Their account stays, because they hold something outside this sector."
	removalDeletes       = "They hold nothing anywhere else, so their account is deleted with its sign-in identities and sessions."
	removingYourself     = "You cannot remove yourself from this sector here. Leave it from your own account page."
	nothingToRemoveHere  = "This person holds nothing in this sector itself, so there is nothing to remove here."
	removalTakesMarshal  = "Removing this person from the sector takes away the role Sector marshal, which you may not take from them."
	removalConfirmSector = "Remove from this sector"
)

// giveCrewRole gives the person the role in the scope, in a transaction of its own.
func (w *tenantListsWorld) giveCrewRole(ctx context.Context, t *testing.T, name, role string, scope accesstypes.PolicyScope) {
	t.Helper()

	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if err := w.app.CrewIdentity().AssignRole(ctx, txn, scope, accesstypes.User(w.ids[name]), accesstypes.Role(role)); err != nil {
			return fmt.Errorf("crewidentity.Client.AssignRole(%s, %s): %w", name, role, err)
		}

		return nil
	}); err != nil {
		t.Fatalf("giving %s the role %s: %v", name, role, err)
	}
}

// askRemovalWarning asks the removal of the person for its warning as the user, under
// the sector's address: the answer's status and body.
func (w *tenantListsWorld) askRemovalWarning(t *testing.T, user accesstypes.User, sector, name string) (int, []byte) {
	t.Helper()

	rr := doRequestRecordedWithHeaders(t, w.h, user, http.MethodPost, sectorPath(sector, sectorRemovalPath), w.removalBody(name), map[string]string{resource.WarningHeader: "true"})

	return rr.Code, rr.Body.Bytes()
}

// removeFromSector calls the removal of the person as the user, under the sector's
// address: the answer's status and body.
func (w *tenantListsWorld) removeFromSector(t *testing.T, user accesstypes.User, sector, name string) (int, []byte) {
	t.Helper()

	return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(sector, sectorRemovalPath), w.removalBody(name))
}

// removalBody is the removal's request body naming the person.
func (w *tenantListsWorld) removalBody(name string) string {
	return fmt.Sprintf(`{"userId":%q}`, w.ids[name])
}

// decodeWarning reads a warning's answer.
func decodeWarning(t *testing.T, body []byte) resource.Warning {
	t.Helper()

	var warning resource.Warning
	if err := json.Unmarshal(body, &warning); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", body, err)
	}

	return warning
}

// accountExecuteAnswer is the methods the Accounts list's row of the person lists for
// the user, asked with capabilities=Execute.
func accountExecuteAnswer(t *testing.T, h http.Handler, user accesstypes.User, id string) []string {
	t.Helper()

	status, body := doRequestAs(t, h, user, http.MethodGet, consoleAPI+"/crew-session-users/"+id+"?capabilities=Execute", "")
	assertStatus(t, status, http.StatusOK, body)
	caps, _ := decodeRow(t, body)["zzCapabilities"].(map[string]any)
	listed, _ := caps["Execute"].([]any)
	methods := make([]string, 0, len(listed))
	for _, method := range listed {
		methods = append(methods, fmt.Sprint(method))
	}
	slices.Sort(methods)

	return methods
}

// TestTenantRemoval holds the removal of a person from a sector: the warning says what
// goes and writes nothing; the call takes every role of the person's in the sector and
// nothing elsewhere, and the account only where nothing else is held; the caller's own
// row offers no removal and the call on oneself is refused; a person of another sector is
// not found; a person who holds a role in every sector alone has nothing to remove here;
// and a Marshal removes no other Marshal.
func TestTenantRemoval(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// kit holds two roles at Anvil and one at Bastion; mara is a second Marshal of Anvil
	// with an account; lou a Cadet of Anvil alone; hal holds Headquarters, which acts on
	// the Accounts list.
	w.seat(ctx, t, "kit", "Pilot", anvil)
	w.giveCrewRole(ctx, t, "kit", "Cadet", accesstypes.DomainPolicyScope(anvil))
	w.giveCrewRole(ctx, t, "kit", "Pilot", accesstypes.DomainPolicyScope(bastion))
	w.seat(ctx, t, "mara", "SectorMarshal", anvil)
	w.seat(ctx, t, "lou", "Cadet", anvil)
	w.seat(ctx, t, "hal", "Cadet", bastion)
	w.giveCrewRole(ctx, t, "hal", "Headquarters", accesstypes.GlobalPolicyScope())

	kitHolds := []string{"domain/" + anvil + " Cadet", "domain/" + anvil + " Pilot", "domain/" + bastion + " Pilot"}
	if got := crewHeldBy(t, w.db, w.ids["kit"]); !slices.Equal(got, kitHolds) {
		t.Fatalf("kit holds %v before the removal, want %v", got, kitHolds)
	}

	t.Run("the warning names the roles that go and writes nothing", func(t *testing.T) {
		status, body := w.askRemovalWarning(t, "marshal", anvil, "kit")
		assertStatus(t, status, http.StatusOK, body)
		want := resource.Warning{
			Consequence: "kit is removed from this sector: every role they hold in it is taken away.",
			Names:       []string{"Cadet", "Pilot"},
			Confirm:     removalConfirmSector,
			Also:        []resource.WarningNote{{Says: removalKeepsAccount}},
		}
		if diff := cmp.Diff(want, decodeWarning(t, body)); diff != "" {
			t.Errorf("the warning (-want +got):\n%s", diff)
		}
		if got := crewHeldBy(t, w.db, w.ids["kit"]); !slices.Equal(got, kitHolds) {
			t.Errorf("kit holds %v after the warning, want %v: the warning writes nothing", got, kitHolds)
		}
	})
	t.Run("one call takes both of the sector's roles and keeps the other sector's", func(t *testing.T) {
		status, body := w.removeFromSector(t, "marshal", anvil, "kit")
		assertStatus(t, status, http.StatusNoContent, body)
		want := []string{"domain/" + bastion + " Pilot"}
		if got := crewHeldBy(t, w.db, w.ids["kit"]); !slices.Equal(got, want) {
			t.Errorf("kit holds %v after the removal, want %v", got, want)
		}
		if _, listed := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)[w.ids["kit"]]; listed {
			t.Error("kit is still among Anvil's people after the removal")
		}
		if _, listed := sectorList(t, w.h, "governor", bastion, sectorPeoplePath)[w.ids["kit"]]; !listed {
			t.Error("kit is not among Bastion's people after the removal from Anvil")
		}
		if !crewAccountExists(t, w.db, w.ids["kit"]) {
			t.Error("kit's account is gone, want it kept: kit holds a role at Bastion")
		}
	})
	t.Run("a person who holds nothing else is told the account goes, and it goes", func(t *testing.T) {
		status, body := w.askRemovalWarning(t, "marshal", anvil, "ada")
		assertStatus(t, status, http.StatusOK, body)
		if got := decodeWarning(t, body).Also; !cmp.Equal(got, []resource.WarningNote{{Says: removalDeletes}}) {
			t.Errorf("the warning's notes = %v, want the account's deletion said", got)
		}
		if !crewAccountExists(t, w.db, w.ids["ada"]) {
			t.Fatal("ada's account is gone after the warning, want it kept: the warning writes nothing")
		}
		status, body = w.removeFromSector(t, "marshal", anvil, "ada")
		assertStatus(t, status, http.StatusNoContent, body)
		if crewAccountExists(t, w.db, w.ids["ada"]) {
			t.Error("ada's account is still there after the removal, want it deleted")
		}
		if got := crewHeldBy(t, w.db, w.ids["ada"]); len(got) != 0 {
			t.Errorf("ada's id holds %v after the removal, want nothing", got)
		}
	})
	t.Run("the caller's own row offers none of the acts on oneself, and the call on oneself is refused", func(t *testing.T) {
		ownAccount := []string{"DeleteCrewSectorUser", "DisableCrewSectorUser", "RemoveCrewSectorUser"}
		// mara and lou each hold one role at Anvil and nothing else, so the grants'
		// conditions on the row hold for both: only the rule on one's own account tells
		// the two rows apart.
		own := executeAnswer(t, w.h, "mara", anvil, sectorPeoplePath, w.ids["mara"])
		other := executeAnswer(t, w.h, "mara", anvil, sectorPeoplePath, w.ids["lou"])
		for _, method := range ownAccount {
			if slices.Contains(own, method) {
				t.Errorf("mara's own row among Anvil's people lists %s: %v", method, own)
			}
			if !slices.Contains(other, method) {
				t.Errorf("lou's row among Anvil's people lists %v, want %s among them", other, method)
			}
		}

		accountActs := []string{"DeleteCrewUser", "DisableCrewUser"}
		own = accountExecuteAnswer(t, w.h, "hal", w.ids["hal"])
		other = accountExecuteAnswer(t, w.h, "hal", w.ids["lou"])
		for _, method := range accountActs {
			if slices.Contains(own, method) {
				t.Errorf("hal's own row on the Accounts list lists %s: %v", method, own)
			}
			if !slices.Contains(other, method) {
				t.Errorf("lou's row on the Accounts list lists %v, want %s among them", other, method)
			}
		}

		status, body := w.removeFromSector(t, "mara", anvil, "mara")
		assertStatus(t, status, http.StatusForbidden, body)
		if got := refusalMessage(t, body); got != removingYourself {
			t.Errorf("the removal of oneself answered %q, want %q", got, removingYourself)
		}
		if got := crewHeldBy(t, w.db, w.ids["mara"]); !slices.Equal(got, []string{"domain/" + anvil + " SectorMarshal"}) {
			t.Errorf("mara holds %v after the refused call, want her role kept", got)
		}
	})
	t.Run("a person of another sector is not found under this sector's address", func(t *testing.T) {
		before := crewHeldBy(t, w.db, w.ids["di"])
		status, body := w.askRemovalWarning(t, "marshal", anvil, "di")
		assertStatus(t, status, http.StatusNotFound, body)
		status, body = w.removeFromSector(t, "marshal", anvil, "di")
		assertStatus(t, status, http.StatusNotFound, body)
		if got := crewHeldBy(t, w.db, w.ids["di"]); !slices.Equal(got, before) {
			t.Errorf("di holds %v after a call that found no row, want %v kept", got, before)
		}
	})
	t.Run("a person who holds a role in every sector alone has nothing to remove here", func(t *testing.T) {
		people := sectorList(t, w.h, "marshal", anvil, sectorPeoplePath)
		cy := people[w.ids["cy"]]
		if cy["heldInEveryTenant"] != true || cy["heldHere"] != false {
			t.Errorf("cy's row reads heldInEveryTenant = %v and heldHere = %v, want true and false", cy["heldInEveryTenant"], cy["heldHere"])
		}
		if bo := people[w.ids["bo"]]; bo["heldHere"] != true || bo["heldInEveryTenant"] != false {
			t.Errorf("bo's row reads heldHere = %v and heldInEveryTenant = %v, want true and false", bo["heldHere"], bo["heldInEveryTenant"])
		}
		before := crewHeldBy(t, w.db, w.ids["cy"])
		status, body := w.removeFromSector(t, "marshal", anvil, "cy")
		assertStatus(t, status, http.StatusNotFound, body)
		if got := refusalMessage(t, body); got != nothingToRemoveHere {
			t.Errorf("the removal of cy answered %q, want %q", got, nothingToRemoveHere)
		}
		if got := crewHeldBy(t, w.db, w.ids["cy"]); !slices.Equal(got, before) {
			t.Errorf("cy holds %v after the refused call, want %v kept", got, before)
		}
	})
	t.Run("a Marshal removes no other Marshal: the warning and the call are refused", func(t *testing.T) {
		// The grant the refusal reads: the method that takes one role away keeps the
		// Marshal's own role from a Marshal (conditions-proven).
		provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "RemoveCrewSectorRole", "role != 'SectorMarshal' AND everyDomain = false")

		status, body := w.askRemovalWarning(t, "marshal", anvil, "mara")
		assertStatus(t, status, http.StatusForbidden, body)
		if got := refusalMessage(t, body); got != removalTakesMarshal {
			t.Errorf("the warning answered %q, want %q", got, removalTakesMarshal)
		}
		status, body = w.removeFromSector(t, "marshal", anvil, "mara")
		assertStatus(t, status, http.StatusForbidden, body)
		if got := refusalMessage(t, body); got != removalTakesMarshal {
			t.Errorf("the call answered %q, want %q", got, removalTakesMarshal)
		}
		if got := crewHeldBy(t, w.db, w.ids["mara"]); !slices.Equal(got, []string{"domain/" + anvil + " SectorMarshal"}) {
			t.Errorf("mara holds %v after the refused call, want her role kept", got)
		}
	})
}
