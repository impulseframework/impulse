package integration

// role_removal_warning_test: taking one role away from a person asks first, in the
// server's words. The two methods the generated management of the crew auth writes for
// it, RemoveCrewRole for the whole application and RemoveCrewSectorRole under a sector,
// declare a warning with no word to type, asked with X-Warning: true, which writes
// nothing. It says whose role goes and where it is held. Where the role is the last thing
// the person holds, the account stays, holding nothing, and the warning says nothing of
// it.
//
// The harness's session API names no sector (noSessionAPI), so a role held in one sector
// is said to be held "in this sector", by the application's word for a sector alone,
// whichever of the two methods is asked.

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// The routes of the two removals, and the sentences the served stack answers with.
const (
	removeRolePath          = consoleAPI + "/remove-crew-role"
	removeSectorRolePath    = "remove-crew-sector-role"
	roleRemovalConfirm      = "Take the role away"
	onlySuperAdministrators = "Only a super administrator takes the Super administrator role away, and you are not one."
)

// askRoleRemoval asks the method at the target for its warning as the user: the
// answer's status and body.
func (w *tenantListsWorld) askRoleRemoval(t *testing.T, user accesstypes.User, target, body string) (status int, answer []byte) {
	t.Helper()

	rr := doRequestRecordedWithHeaders(t, w.h, user, http.MethodPost, target, body, map[string]string{resource.WarningHeader: "true"})

	return rr.Code, rr.Body.Bytes()
}

// roleBody is a removal's request body naming the person and the role, and the sector
// where one is named.
func (w *tenantListsWorld) roleBody(name, role, sector string) string {
	if sector == "" {
		return fmt.Sprintf(`{"userId":%q,"role":%q}`, w.ids[name], role)
	}

	return fmt.Sprintf(`{"userId":%q,"role":%q,"domain":%q}`, w.ids[name], role, sector)
}

// TestRoleRemovalWarning holds the question before a role is taken away: the warning
// names the person, the role and where it is held, and writes nothing; where the role is
// the last thing the person holds it says nothing of the account, with the plain button,
// and the account stays, holding nothing, with its sign-in identity and its session; a
// role held in one sector is held "in this sector" through either
// method; and a caller the call refuses is refused the warning in the same words.
func TestRoleRemovalWarning(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	// gil holds two roles for the whole application and nothing else; jo two roles at
	// Anvil; sam holds the Super Administrator role, which only a super administrator
	// takes away; hal holds Headquarters, which takes roles away for the whole
	// application, and is no super administrator.
	w.ids["gil"] = string(makeCrewAccount(ctx, t, w.db, "gil"))
	w.giveCrewRole(ctx, t, "gil", "CrewCommon", accesstypes.GlobalPolicyScope())
	w.giveCrewRole(ctx, t, "gil", "BulletinOfficer", accesstypes.GlobalPolicyScope())
	gilSession := w.signedInBy(ctx, t, "gil")
	w.seat(ctx, t, "jo", "Pilot", anvil)
	w.giveCrewRole(ctx, t, "jo", "Cadet", accesstypes.DomainPolicyScope(anvil))
	w.seat(ctx, t, "sam", "Cadet", bastion)
	w.giveCrewRole(ctx, t, "sam", "SuperAdministrator", accesstypes.GlobalPolicyScope())
	w.seat(ctx, t, "hal", "Cadet", bastion)
	w.giveCrewRole(ctx, t, "hal", "Headquarters", accesstypes.GlobalPolicyScope())

	gilHolds := []string{"global/ BulletinOfficer", "global/ CrewCommon"}
	joHolds := []string{"domain/" + anvil + " Cadet", "domain/" + anvil + " Pilot"}
	for name, want := range map[string][]string{"gil": gilHolds, "jo": joHolds} {
		if got := crewHeldBy(t, w.db, w.ids[name]); !slices.Equal(got, want) {
			t.Fatalf("%s holds %v before the removals, want %v", name, got, want)
		}
	}

	t.Run("a role of two for the whole application: the warning names it and writes nothing", func(t *testing.T) {
		status, body := w.askRoleRemoval(t, "governor", removeRolePath, w.roleBody("gil", "CrewCommon", ""))
		assertStatus(t, status, http.StatusOK, body)
		want := resource.Warning{Consequence: "gil loses the role Crew common.", Confirm: roleRemovalConfirm}
		if got := decodeWarning(t, body); !reflect.DeepEqual(got, want) {
			t.Errorf("the warning = %+v, want %+v", got, want)
		}
		if got := crewHeldBy(t, w.db, w.ids["gil"]); !slices.Equal(got, gilHolds) {
			t.Errorf("gil holds %v after the warning, want %v: the warning writes nothing", got, gilHolds)
		}
		status, body = doRequestAs(t, w.h, "governor", http.MethodPost, removeRolePath, w.roleBody("gil", "CrewCommon", ""))
		assertStatus(t, status, http.StatusNoContent, body)
		if got, want := crewHeldBy(t, w.db, w.ids["gil"]), []string{"global/ BulletinOfficer"}; !slices.Equal(got, want) {
			t.Errorf("gil holds %v after the removal, want %v", got, want)
		}
		if !crewAccountExists(t, w.db, w.ids["gil"]) {
			t.Error("gil's account is gone, want it kept")
		}
	})
	t.Run("the last role: the warning says nothing of the account, and the account stays", func(t *testing.T) {
		status, body := w.askRoleRemoval(t, "governor", removeRolePath, w.roleBody("gil", "BulletinOfficer", ""))
		assertStatus(t, status, http.StatusOK, body)
		want := resource.Warning{Consequence: "gil loses the role Bulletin officer.", Confirm: roleRemovalConfirm}
		if got := decodeWarning(t, body); !reflect.DeepEqual(got, want) {
			t.Errorf("the warning = %+v, want %+v: no note on the account and the plain button", got, want)
		}
		status, body = doRequestAs(t, w.h, "governor", http.MethodPost, removeRolePath, w.roleBody("gil", "BulletinOfficer", ""))
		assertStatus(t, status, http.StatusNoContent, body)
		w.accountKept(t, "gil", gilSession)
	})
	t.Run("a role held in one sector, through the whole application's method, is held in this sector", func(t *testing.T) {
		status, body := w.askRoleRemoval(t, "governor", removeRolePath, w.roleBody("jo", "Pilot", anvil))
		assertStatus(t, status, http.StatusOK, body)
		want := resource.Warning{Consequence: "jo loses the role Pilot in this sector.", Confirm: roleRemovalConfirm}
		if got := decodeWarning(t, body); !reflect.DeepEqual(got, want) {
			t.Errorf("the warning = %+v, want %+v", got, want)
		}
		if got := crewHeldBy(t, w.db, w.ids["jo"]); !slices.Equal(got, joHolds) {
			t.Errorf("jo holds %v after the warning, want %v: the warning writes nothing", got, joHolds)
		}
	})
	t.Run("the sector's own method says this sector", func(t *testing.T) {
		status, body := w.askRoleRemoval(t, "marshal", sectorPath(anvil, removeSectorRolePath), fmt.Sprintf(`{"userId":%q,"role":"Cadet"}`, w.ids["jo"]))
		assertStatus(t, status, http.StatusOK, body)
		want := resource.Warning{Consequence: "jo loses the role Cadet in this sector.", Confirm: roleRemovalConfirm}
		if got := decodeWarning(t, body); !reflect.DeepEqual(got, want) {
			t.Errorf("the warning = %+v, want %+v", got, want)
		}
		if got := crewHeldBy(t, w.db, w.ids["jo"]); !slices.Equal(got, joHolds) {
			t.Errorf("jo holds %v after the warning, want %v: the warning writes nothing", got, joHolds)
		}
	})
	t.Run("a caller the call refuses is refused the warning in the same words", func(t *testing.T) {
		before := crewHeldBy(t, w.db, w.ids["sam"])
		status, body := w.askRoleRemoval(t, "hal", removeRolePath, w.roleBody("sam", "SuperAdministrator", ""))
		assertStatus(t, status, http.StatusForbidden, body)
		if got := refusalMessage(t, body); got != onlySuperAdministrators {
			t.Errorf("the warning answered %q, want %q", got, onlySuperAdministrators)
		}
		status, body = doRequestAs(t, w.h, "hal", http.MethodPost, removeRolePath, w.roleBody("sam", "SuperAdministrator", ""))
		assertStatus(t, status, http.StatusForbidden, body)
		if got := refusalMessage(t, body); got != onlySuperAdministrators {
			t.Errorf("the call answered %q, want %q", got, onlySuperAdministrators)
		}
		if got := crewHeldBy(t, w.db, w.ids["sam"]); !slices.Equal(got, before) {
			t.Errorf("sam holds %v after the refused warning and call, want %v kept", got, before)
		}
	})
}
