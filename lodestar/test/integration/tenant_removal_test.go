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
	"reflect"
	"slices"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
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
	removalSeatedAgain   = "This sector is open to its directory and this person signs in through it, so they are given the starting role again the next time they sign in. A role with no privileges keeps them out."
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

// askRemovalWarning asks the removal of the person for its warning as Anvil's marshal,
// under Anvil's address: the answer's status and body.
func (w *tenantListsWorld) askRemovalWarning(t *testing.T, name string) (status int, body []byte) {
	t.Helper()

	rr := doRequestRecordedWithHeaders(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, sectorRemovalPath), w.removalBody(name), map[string]string{resource.WarningHeader: "true"})

	return rr.Code, rr.Body.Bytes()
}

// removeFromSector calls the removal of the person as the user, under Anvil's address:
// the answer's status and body.
func (w *tenantListsWorld) removeFromSector(t *testing.T, user accesstypes.User, name string) (status int, body []byte) {
	t.Helper()

	return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(anvil, sectorRemovalPath), w.removalBody(name))
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

	status, body := doRequestAs(t, h, user, http.MethodGet, consoleAPI+"/"+crewListedAccountsPath+"/"+id+"?capabilities=Execute", "")
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
	// the Accounts list; wren holds Crew common for the whole fleet and nothing in a sector,
	// so no sector manages her account.
	w.seat(ctx, t, "kit", "Pilot", anvil)
	w.giveCrewRole(ctx, t, "kit", "Cadet", accesstypes.DomainPolicyScope(anvil))
	w.giveCrewRole(ctx, t, "kit", "Pilot", accesstypes.DomainPolicyScope(bastion))
	w.seat(ctx, t, "mara", "SectorMarshal", anvil)
	w.seat(ctx, t, "lou", "Cadet", anvil)
	w.seat(ctx, t, "hal", "Cadet", bastion)
	w.giveCrewRole(ctx, t, "hal", "Headquarters", accesstypes.GlobalPolicyScope())
	w.ids["wren"] = string(makeCrewAccount(ctx, t, w.db, "wren"))
	w.giveCrewRole(ctx, t, "wren", "CrewCommon", accesstypes.GlobalPolicyScope())

	kitHolds := []string{"domain/" + anvil + " Cadet", "domain/" + anvil + " Pilot", "domain/" + bastion + " Pilot"}
	if got := crewHeldBy(t, w.db, w.ids["kit"]); !slices.Equal(got, kitHolds) {
		t.Fatalf("kit holds %v before the removal, want %v", got, kitHolds)
	}

	t.Run("the warning names the roles that go and writes nothing", func(t *testing.T) {
		status, body := w.askRemovalWarning(t, "kit")
		assertStatus(t, status, http.StatusOK, body)
		want := resource.Warning{
			Consequence: "kit is removed from this sector: every role they hold in it is taken away.",
			Names:       []string{"Cadet", "Pilot"},
			Confirm:     removalConfirmSector,
			Also:        []resource.WarningNote{{Says: removalKeepsAccount}},
		}
		if got := decodeWarning(t, body); !reflect.DeepEqual(got, want) {
			t.Errorf("the warning = %+v, want %+v", got, want)
		}
		if got := crewHeldBy(t, w.db, w.ids["kit"]); !slices.Equal(got, kitHolds) {
			t.Errorf("kit holds %v after the warning, want %v: the warning writes nothing", got, kitHolds)
		}
	})
	t.Run("one call takes both of the sector's roles and keeps the other sector's", func(t *testing.T) {
		status, body := w.removeFromSector(t, "marshal", "kit")
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
		if methods := executeAnswer(t, w.h, "governor", bastion, sectorPeoplePath, w.ids["kit"]); !slices.Contains(methods, "RemoveCrewSectorUser") {
			t.Errorf("kit's row among Bastion's people lists %v, want RemoveCrewSectorUser: kit still holds Pilot there", methods)
		}
		if !crewAccountExists(t, w.db, w.ids["kit"]) {
			t.Error("kit's account is gone, want it kept: kit holds a role at Bastion")
		}
	})
	t.Run("a person who holds nothing else is told the account goes, and it goes", func(t *testing.T) {
		status, body := w.askRemovalWarning(t, "ada")
		assertStatus(t, status, http.StatusOK, body)
		if got := decodeWarning(t, body).Also; !reflect.DeepEqual(got, []resource.WarningNote{{Says: removalDeletes}}) {
			t.Errorf("the warning's notes = %v, want the account's deletion said", got)
		}
		if !crewAccountExists(t, w.db, w.ids["ada"]) {
			t.Fatal("ada's account is gone after the warning, want it kept: the warning writes nothing")
		}
		status, body = w.removeFromSector(t, "marshal", "ada")
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

		// On the Accounts list hal's own row is held beside wren's: no sector manages either
		// account, so the condition on Headquarters' grants holds on both rows, and only the
		// rule on one's own account tells the two apart.
		accountActs := []string{"DeleteCrewUser", "DisableCrewUser"}
		own = accountExecuteAnswer(t, w.h, "hal", w.ids["hal"])
		other = accountExecuteAnswer(t, w.h, "hal", w.ids["wren"])
		for _, method := range accountActs {
			if slices.Contains(own, method) {
				t.Errorf("hal's own row on the Accounts list lists %s: %v", method, own)
			}
			if !slices.Contains(other, method) {
				t.Errorf("wren's row on the Accounts list lists %v, want %s among them", other, method)
			}
		}

		status, body := w.removeFromSector(t, "mara", "mara")
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
		status, body := w.askRemovalWarning(t, "di")
		assertStatus(t, status, http.StatusNotFound, body)
		status, body = w.removeFromSector(t, "marshal", "di")
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
		status, body := w.removeFromSector(t, "marshal", "cy")
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

		status, body := w.askRemovalWarning(t, "mara")
		assertStatus(t, status, http.StatusForbidden, body)
		if got := refusalMessage(t, body); got != removalTakesMarshal {
			t.Errorf("the warning answered %q, want %q", got, removalTakesMarshal)
		}
		status, body = w.removeFromSector(t, "marshal", "mara")
		assertStatus(t, status, http.StatusForbidden, body)
		if got := refusalMessage(t, body); got != removalTakesMarshal {
			t.Errorf("the call answered %q, want %q", got, removalTakesMarshal)
		}
		if got := crewHeldBy(t, w.db, w.ids["mara"]); !slices.Equal(got, []string{"domain/" + anvil + " SectorMarshal"}) {
			t.Errorf("mara holds %v after the refused call, want her role kept", got)
		}
	})
}

// TestTenantRemoval_seatedAgainBySignIn holds what the removal's warning says of a sector
// open to its directory: Cinder is open to the fleet's Workspace with a starting role, so
// removing a person who signs in through the Workspace says they are given the role again
// at their next sign-in, and that a role with no privileges keeps them out. A person with
// no sign-in through it is told nothing of the kind, and neither is the first once Cinder
// is invite only. The warning writes nothing.
func TestTenantRemoval_seatedAgainBySignIn(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// ida signs in through the fleet's Workspace, Cinder's directory: her identity is
	// written as the Google sign-in writes one. jo, seated at Cinder by hand, has none.
	w.seat(ctx, t, "ida", "Pilot", cinder)
	w.seat(ctx, t, "jo", "Pilot", cinder)
	id, err := impulse.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("CrewSessionIdentities", map[string]any{
		"Id": id.String(), "UserId": w.ids["ida"], "Method": "google", "Connection": "example", "Subject": "workspace-subject-of-ida",
		"CreatedAt": spanner.CommitTimestamp, "LastUsedAt": spanner.CommitTimestamp,
	})}); err != nil {
		t.Fatalf("writing ida's identity through the Workspace: %v", err)
	}
	// seatedAgain says whether the warning of removing the person from Cinder, asked by
	// the governor, holds the note.
	seatedAgain := func(t *testing.T, name string) bool {
		t.Helper()
		rr := doRequestRecordedWithHeaders(t, w.h, tenantGovernor, http.MethodPost, sectorPath(cinder, sectorRemovalPath), w.removalBody(name), map[string]string{resource.WarningHeader: "true"})
		assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())

		return slices.ContainsFunc(decodeWarning(t, rr.Body.Bytes()).Also, func(note resource.WarningNote) bool { return note.Says == removalSeatedAgain })
	}
	held := []string{"domain/" + cinder + " Pilot"}

	if !seatedAgain(t, "ida") {
		t.Errorf("the warning of removing ida from Cinder holds no note %q, want it: Cinder is open and she signs in through its directory", removalSeatedAgain)
	}
	if got := crewHeldBy(t, w.db, w.ids["ida"]); !slices.Equal(got, held) {
		t.Errorf("ida holds %v after the warning, want %v: the warning writes nothing", got, held)
	}
	if seatedAgain(t, "jo") {
		t.Errorf("the warning of removing jo from Cinder holds %q, want none: jo has no sign-in through its directory", removalSeatedAgain)
	}
	if status, body := doRequestAs(t, w.h, tenantGovernor, http.MethodPost, sectorPath(cinder, "make-sector-invite-only"), `{}`); status != http.StatusOK {
		t.Fatalf("making Cinder invite only: status %d: %s", status, body)
	}
	if seatedAgain(t, "ida") {
		t.Errorf("the warning of removing ida from Cinder once it is invite only holds %q, want none", removalSeatedAgain)
	}
}
