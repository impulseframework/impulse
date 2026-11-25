package integration

// group_member_removal_warning_test: taking a person out of a group asks first, in the
// server's words. The two methods the generated management of the crew auth writes for
// it, RemoveCrewGroupMember for the whole application and RemoveCrewSectorGroupMember
// under a sector, declare a warning with no word to type, asked with X-Warning: true,
// which writes nothing. It says who leaves which group. Where the place is the last thing
// the person holds, the account stays, holding nothing, and the warning says nothing of
// it.

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/impulseframework/impulse/resource"
)

// The routes of the two removals, and the words of the warning's buttons.
const (
	removeGroupMemberPath       = consoleAPI + "/remove-crew-group-member"
	removeSectorGroupMemberPath = "remove-crew-sector-group-member"
	groupRemovalConfirm         = "Remove from the group"
)

// inCrewGroup reports whether the person under the name has a place in the group.
func (w *tenantListsWorld) inCrewGroup(t *testing.T, group, name string) bool {
	t.Helper()

	return rowsStanding(t, w, "SELECT COUNT(*) FROM CrewGroupMembers WHERE GroupId = @group AND UserId = @user", map[string]any{"group": group, "user": w.ids[name]}) == 1
}

// TestGroupMemberRemovalWarning holds the question before a person is taken out of a
// group: the warning names the person and the group, and writes nothing; where the place
// is the last thing the person holds it says nothing of the account, with the plain
// button, and the account stays, holding nothing, with its sign-in identity and its
// session; a sector's group is asked of through the sector's own method; and
// a caller the call refuses is refused the warning in the same words.
func TestGroupMemberRemovalWarning(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	hq := func(method, body string) (int, []byte) {
		return doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/"+method, body)
	}
	inSector := func(method, body string) (int, []byte) {
		return doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, method), body)
	}
	done := func(status int, body []byte) {
		t.Helper()
		assertStatus(t, status, http.StatusNoContent, body)
	}
	made := func(status int, body []byte) string {
		t.Helper()
		assertStatus(t, status, http.StatusOK, body)

		return cell[string](t, decodeRow(t, body), "id")
	}
	member := func(group, name string) string {
		return fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, w.ids[name])
	}

	// The night watch is the whole fleet's group and holds the Engineer's role at Anvil.
	// kit is a Cadet at Anvil and is in it too; lee holds nothing but the place in it.
	watch := made(hq("create-crew-group", `{"name":"Night watch","description":"The fleet's crew on watch at night."}`))
	done(hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer","domain":%q}`, watch, anvil)))
	w.seat(ctx, t, "kit", "Cadet", anvil)
	w.ids["lee"] = string(makeCrewAccount(ctx, t, w.db, "lee"))
	leeSession := w.signedInBy(ctx, t, "lee")
	done(hq("add-crew-group-member", member(watch, "kit")))
	done(hq("add-crew-group-member", member(watch, "lee")))

	// The hangar hands are Anvil's group, and ned, a Cadet at Anvil, is in it, where the
	// Marshal put him.
	hangar := made(inSector("create-crew-sector-group", `{"name":"Hangar hands","description":"The crew who work Anvil's hangar deck."}`))
	done(inSector("give-crew-sector-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer"}`, hangar)))
	w.seat(ctx, t, "ned", "Cadet", anvil)
	done(inSector("add-crew-sector-group-member", member(hangar, "ned")))

	// The bridge watch is Anvil's group too, and holds the Marshal's role there, which
	// headquarters gave it; headquarters put ora in it. A Marshal may not take the role
	// from a fellow Marshal, so the Marshal may not take ora out.
	bridge := made(inSector("create-crew-sector-group", `{"name":"Bridge watch","description":"The crew who stand Anvil's bridge."}`))
	done(hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"SectorMarshal","domain":%q}`, bridge, anvil)))
	w.seat(ctx, t, "ora", "Cadet", anvil)
	done(hq("add-crew-group-member", member(bridge, "ora")))

	t.Run("a person who holds a role of their own: the warning names the group and writes nothing", func(t *testing.T) {
		t.Parallel()
		status, body := w.askRoleRemoval(t, "governor", removeGroupMemberPath, member(watch, "kit"))
		assertStatus(t, status, http.StatusOK, body)
		want := resource.Warning{
			Consequence: "kit is removed from the group Night watch. They keep the roles they hold themselves and lose the group's.",
			Confirm:     groupRemovalConfirm,
		}
		if got := decodeWarning(t, body); !reflect.DeepEqual(got, want) {
			t.Errorf("the warning = %+v, want %+v", got, want)
		}
		if !w.inCrewGroup(t, watch, "kit") {
			t.Fatal("kit is out of the night watch after the warning, want kept: the warning writes nothing")
		}
		status, body = doRequestAs(t, w.h, "governor", http.MethodPost, removeGroupMemberPath, member(watch, "kit"))
		assertStatus(t, status, http.StatusNoContent, body)
		if w.inCrewGroup(t, watch, "kit") {
			t.Error("kit is still in the night watch after the removal")
		}
		if !crewAccountExists(t, w.db, w.ids["kit"]) {
			t.Error("kit's account is gone, want it kept")
		}
	})
	t.Run("the place is all the person holds: the warning says nothing of the account, and the account stays", func(t *testing.T) {
		t.Parallel()
		status, body := w.askRoleRemoval(t, "governor", removeGroupMemberPath, member(watch, "lee"))
		assertStatus(t, status, http.StatusOK, body)
		want := resource.Warning{
			Consequence: "lee is removed from the group Night watch. They keep the roles they hold themselves and lose the group's.",
			Confirm:     groupRemovalConfirm,
		}
		if got := decodeWarning(t, body); !reflect.DeepEqual(got, want) {
			t.Errorf("the warning = %+v, want %+v: no note on the account and the plain button", got, want)
		}
		if !w.inCrewGroup(t, watch, "lee") {
			t.Fatal("lee's place is gone after the warning, want it kept: the warning writes nothing")
		}
		status, body = doRequestAs(t, w.h, "governor", http.MethodPost, removeGroupMemberPath, member(watch, "lee"))
		assertStatus(t, status, http.StatusNoContent, body)
		w.accountKept(t, "lee", leeSession)
	})
	t.Run("a sector's group through the sector's own method", func(t *testing.T) {
		t.Parallel()
		status, body := w.askRoleRemoval(t, "marshal", sectorPath(anvil, removeSectorGroupMemberPath), member(hangar, "ned"))
		assertStatus(t, status, http.StatusOK, body)
		want := resource.Warning{
			Consequence: "ned is removed from the group Hangar hands. They keep the roles they hold themselves and lose the group's.",
			Confirm:     groupRemovalConfirm,
		}
		if got := decodeWarning(t, body); !reflect.DeepEqual(got, want) {
			t.Errorf("the warning = %+v, want %+v", got, want)
		}
		if !w.inCrewGroup(t, hangar, "ned") {
			t.Error("ned is out of the hangar hands after the warning, want kept: the warning writes nothing")
		}
	})
	t.Run("a caller the call refuses is refused the warning in the same words", func(t *testing.T) {
		t.Parallel()
		const refused = "Taking a person out of the group Bridge watch takes away the role Sector marshal, which you may not take from them."
		status, body := w.askRoleRemoval(t, "marshal", sectorPath(anvil, removeSectorGroupMemberPath), member(bridge, "ora"))
		assertStatus(t, status, http.StatusForbidden, body)
		if got := refusalMessage(t, body); got != refused {
			t.Errorf("the warning answered %q, want %q", got, refused)
		}
		status, body = doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, removeSectorGroupMemberPath), member(bridge, "ora"))
		assertStatus(t, status, http.StatusForbidden, body)
		if got := refusalMessage(t, body); got != refused {
			t.Errorf("the call answered %q, want %q", got, refused)
		}
		if !w.inCrewGroup(t, bridge, "ora") {
			t.Error("ora is out of the bridge watch after the refused warning and call, want kept")
		}
	})
}
