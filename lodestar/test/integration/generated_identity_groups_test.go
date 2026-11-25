// Demonstrates: auth.groups, auth.generated-identity, auth.tenant-methods, execute-condition.request-field.
package integration

// generated_identity_groups_test: the groups of people the generated management of the
// crew auth serves, because the schema holds the permission store's group tables.
//
// A group holds roles the way a person does, and a person in it holds them wherever the
// group does. A sector's group is made and managed under the sector's address by the
// sector's roles, and holds roles there alone. A group's delete takes its members and the
// roles it holds with it, after a question that counts them. The suite drives the methods as served, against the real
// engine over the shipped role file: what a member may do changes with the group's
// roles and with their place in it, at their next request.
//
// The people are the ones of the lists' suite (ada and fay Cadets at Anvil, bo a Pilot at
// Anvil and at Bastion), the cast's Marshal of Anvil, and the governor, who runs
// headquarters. The cast's own groups, Anvil's deck crew and its bay keepers, are in every
// world; the deckhand's account and the sentry's are not in this one, so the lists hold
// the people the test makes.

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// The sector's lists over groups, as the generated routes name them.
const (
	sectorGroupsPath            = "crew-sector-groups"
	sectorGroupPeoplePath       = "crew-sector-group-users"
	sectorGroupRolesPath        = "crew-sector-group-roles"
	sectorRolesThroughGroupPath = "crew-sector-user-group-roles"
)

// namesOf reads one cell of every row, sorted.
func namesOf(t *testing.T, rows []map[string]any, key string) []string {
	t.Helper()

	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, cell[string](t, row, key))
	}
	slices.Sort(names)

	return names
}

// TestGeneratedIdentityGroups holds a sector's group from its creation to its removal: a
// role the group is given is every member's at their next request and nobody else's, a
// role its Marshal may not give a person is not given to a group either, a person sees
// the groups they are in themselves (group IN subject.crewGroups), a group of one
// sector is not found in another and holds no role there, no group holds the Super
// Administrator role, and a group's delete is asked first, with what the delete takes
// counted, and once confirmed takes the group with its members and its roles, the
// members' accounts left standing.
func TestGeneratedIdentityGroups(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	// The grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "GiveCrewSectorGroupRole", "new.role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "TakeCrewSectorGroupRole", "role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorCrew", "List", "CrewSectorGroups", "group IN subject.crewGroups")
	provesGrant(t, crew.Roles(), "SectorCrew", "Read", "CrewSectorGroups", "group IN subject.crewGroups")
	provesGrant(t, crew.Roles(), "SectorCrew", "List", "CrewSectorGroupUsers", "group IN subject.crewGroups")

	post := func(user accesstypes.User, sector, method, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, sectorPath(sector, method), body)
	}
	list := func(user accesstypes.User, sector, path string) []map[string]any {
		status, body := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(sector, path+"?limit=200"), "")
		assertStatus(t, status, http.StatusOK, body)

		return decodeRows(t, body)
	}
	refits := func(user accesstypes.User) int {
		status, _ := doRequestAs(t, w.h, user, http.MethodGet, sectorPath(anvil, "refits"), "")

		return status
	}

	// The Marshal makes a group in her sector. It has no member and holds no role.
	status, body := post("marshal", anvil, "create-crew-sector-group", `{"name":"Hangar hands","description":"The crew who work Anvil's hangar deck."}`)
	assertStatus(t, status, http.StatusOK, body)
	group := cell[string](t, decodeRow(t, body), "id")
	member := func(name string) string { return fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, w.ids[name]) }
	role := func(name string) string { return fmt.Sprintf(`{"groupId":%q,"role":%q}`, group, name) }

	if got, want := namesOf(t, list("marshal", anvil, sectorGroupsPath), "name"), []string{"Bay keepers", "Deck crew", "Hangar hands"}; !slices.Equal(got, want) {
		t.Errorf("Anvil's groups = %v, want %v", got, want)
	}
	if got := list("governor", bastion, sectorGroupsPath); len(got) != 0 {
		t.Errorf("Bastion's groups = %v, want none: a sector's group is that sector's alone", got)
	}
	status, body = post("marshal", anvil, "create-crew-sector-group", `{"name":"Hangar hands","description":"A second group under the name."}`)
	assertStatus(t, status, http.StatusConflict, body)

	// A Cadet has no hangar deck, and the group gives none yet.
	if got := refits("ada"); got != http.StatusForbidden {
		t.Fatalf("ada lists the refits before anything is given: status = %d, want 403", got)
	}
	status, body = post("marshal", anvil, "add-crew-sector-group-member", member("ada"))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := refits("ada"); got != http.StatusForbidden {
		t.Errorf("ada lists the refits in a group that holds no role: status = %d, want 403", got)
	}

	// The group is given the Engineer's role in the sector: its member holds it at her
	// next request, and a Cadet who is not in the group does not.
	status, body = post("marshal", anvil, "give-crew-sector-group-role", role("Engineer"))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = post("marshal", anvil, "give-crew-sector-group-role", role("SectorCrew"))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := refits("ada"); got != http.StatusOK {
		t.Errorf("ada lists the refits through the group: status = %d, want 200", got)
	}
	if got := refits("fay"); got != http.StatusForbidden {
		t.Errorf("fay, who is in no group, lists the refits: status = %d, want 403", got)
	}

	// What the Marshal may not give a person she may not give a group, and the roles a
	// group may never hold are refused whoever asks: the Super Administrator role, and
	// for a sector's group a role in another sector.
	status, body = post("marshal", anvil, "give-crew-sector-group-role", role("SectorMarshal"))
	assertStatus(t, status, http.StatusForbidden, body)
	status, body = doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/give-crew-group-role", role(string(accesstypes.SuperAdministrator)))
	assertStatus(t, status, http.StatusBadRequest, body)
	if !strings.Contains(string(body), "The Super administrator role is given to a person, never to a group. Only a super administrator makes another, one person at a time.") {
		t.Errorf("the refusal of the Super Administrator role for a sector's group = %s, want it to say the role is never a group's", body)
	}
	status, body = doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Pilot","domain":%q}`, group, bastion))
	assertStatus(t, status, http.StatusBadRequest, body)
	status, body = doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Pilot","everyDomain":true}`, group))
	assertStatus(t, status, http.StatusBadRequest, body)

	// The sector's lists say who is in the group, what it holds, and what each person
	// holds through it, beside the roles each holds by a membership of their own.
	if got, want := namesOf(t, list("marshal", anvil, sectorGroupPeoplePath), "username"), []string{"ada"}; !slices.Equal(got, want) {
		t.Errorf("the people of Anvil's groups = %v, want %v", got, want)
	}
	var held []string
	for _, row := range list("marshal", anvil, sectorGroupRolesPath) {
		held = append(held, cell[string](t, row, "groupName")+" "+cell[string](t, row, "role"))
	}
	slices.Sort(held)
	if want := []string{"Deck crew Cadet", "Deck crew SectorCrew", "Hangar hands Engineer", "Hangar hands SectorCrew"}; !slices.Equal(held, want) {
		t.Errorf("the roles of Anvil's groups = %v, want %v", held, want)
	}
	var through []string
	for _, row := range list("marshal", anvil, sectorRolesThroughGroupPath) {
		through = append(through, cell[string](t, row, "username")+" "+cell[string](t, row, "groupName")+" "+cell[string](t, row, "role"))
	}
	slices.Sort(through)
	if want := []string{"ada Hangar hands Engineer", "ada Hangar hands SectorCrew"}; !slices.Equal(through, want) {
		t.Errorf("the roles held through Anvil's groups = %v, want %v", through, want)
	}
	if rows := sectorRoles(t, w.h, "marshal", anvil); rows[w.ids["ada"]+"/Engineer"] != nil {
		t.Error("the roles ada holds by a membership of her own list the Engineer's, which she holds through the group alone")
	}

	// A person sees the groups they are in themselves, with the people in them: the
	// sector crew's grant is held to subject.crewGroups, the set the members table declares.
	if got, want := namesOf(t, list("ada", anvil, sectorGroupsPath), "name"), []string{"Hangar hands"}; !slices.Equal(got, want) {
		t.Errorf("the groups ada sees = %v, want %v: her own and no other", got, want)
	}
	if got, want := namesOf(t, list("ada", anvil, sectorGroupPeoplePath), "username"), []string{"ada"}; !slices.Equal(got, want) {
		t.Errorf("the people ada sees in her groups = %v, want %v", got, want)
	}
	if got := list("cadet", anvil, sectorGroupsPath); len(got) != 0 {
		t.Errorf("the cadet, who is in no group, sees the groups %v, want none", got)
	}
	// Reading one group is held to the same set: ada reads the group she is in, by its
	// id, and neither she nor the cadet reads a group they are not in, which answers as
	// a group that is not there.
	var deckCrew string
	for _, row := range list("marshal", anvil, sectorGroupsPath) {
		if cell[string](t, row, "name") == "Deck crew" {
			deckCrew = cell[string](t, row, "id")
		}
	}
	if deckCrew == "" {
		t.Fatal("Anvil's groups do not list the Deck crew")
	}
	readGroup := func(user accesstypes.User, id string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodGet, sectorPath(anvil, sectorGroupsPath+"/"+id), "")
	}
	status, body = readGroup("ada", group)
	assertStatus(t, status, http.StatusOK, body)
	if got := cell[string](t, decodeRow(t, body), "name"); got != "Hangar hands" {
		t.Errorf("the group ada reads by its id is named %q, want Hangar hands", got)
	}
	status, body = readGroup("ada", deckCrew)
	assertStatus(t, status, http.StatusNotFound, body)
	status, body = readGroup("cadet", group)
	assertStatus(t, status, http.StatusNotFound, body)

	// A group of Anvil is not found under Bastion's address, whoever asks.
	status, body = post("governor", bastion, "describe-crew-sector-group", fmt.Sprintf(`{"groupId":%q,"description":"Described from another sector."}`, group))
	assertStatus(t, status, http.StatusNotFound, body)
	status, body = post("marshal", anvil, "describe-crew-sector-group", fmt.Sprintf(`{"groupId":%q,"description":"Works the hangar deck on the day shift."}`, group))
	assertStatus(t, status, http.StatusNoContent, body)

	// A role taken from the group is no longer its member's; a member taken out keeps
	// what she holds herself.
	status, body = post("marshal", anvil, "take-crew-sector-group-role", role("Engineer"))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := refits("ada"); got != http.StatusForbidden {
		t.Errorf("ada lists the refits after the group lost the role: status = %d, want 403", got)
	}
	status, body = post("marshal", anvil, "remove-crew-sector-group-member", member("ada"))
	assertStatus(t, status, http.StatusNoContent, body)
	if got, want := crewHolds(t, w.db, "ada"), []string{"domain/" + anvil + " Cadet"}; !slices.Equal(got, want) {
		t.Errorf("ada holds %v, want %v", got, want)
	}

	// The group is given the Engineer's role again and two members, ada and fay, and fay
	// gives up her own roles: she holds the refits through the group alone.
	status, body = post("marshal", anvil, "give-crew-sector-group-role", role("Engineer"))
	assertStatus(t, status, http.StatusNoContent, body)
	for _, name := range []string{"ada", "fay"} {
		status, body = post("marshal", anvil, "add-crew-sector-group-member", member(name))
		assertStatus(t, status, http.StatusNoContent, body)
	}
	status, body = doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/remove-crew-role", fmt.Sprintf(`{"userId":%q,"role":"CrewCommon"}`, w.ids["fay"]))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = post("marshal", anvil, "remove-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Cadet"}`, w.ids["fay"]))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := refits("fay"); got != http.StatusOK {
		t.Errorf("fay lists the refits through the group alone: status = %d, want 200", got)
	}
	standing := func() (members, roles int64) {
		t.Helper()

		return rowsStanding(t, w, "SELECT COUNT(*) FROM CrewGroupMembers WHERE GroupId = @group", map[string]any{"group": group}),
			rowsStanding(t, w, "SELECT COUNT(*) FROM CrewGroupRoles WHERE GroupId = @group", map[string]any{"group": group})
	}
	deleteGroup := func(everythingUnder bool) (int, []byte) {
		return post("marshal", anvil, "delete-crew-sector-group", fmt.Sprintf(`{"groupId":%q,"everythingUnder":%t}`, group, everythingUnder))
	}

	// Asked without saying it takes what is under it, the delete answers the question a
	// person is asked first, with the group's members and roles counted, and nothing goes.
	status, body = deleteGroup(false)
	assertStatus(t, status, http.StatusPreconditionRequired, body)
	if got, want := refusalMessage(t, body), "Delete this group and everything under it: 2 roles, 2 members. This can't be undone."; got != want {
		t.Errorf("the question = %q, want %q", got, want)
	}
	if members, roles := standing(); members != 2 || roles != 2 {
		t.Errorf("the group has %d members and %d roles after the question, want 2 and 2: the question deletes nothing", members, roles)
	}

	// Confirmed, the group goes with its members and its roles. Each member's account
	// stands: ada keeps the role she holds herself, fay holds nothing, and neither holds
	// the group's roles at her next request; Anvil answers fay, who holds nothing there,
	// as a sector that does not exist.
	status, body = deleteGroup(true)
	assertStatus(t, status, http.StatusNoContent, body)
	if got, want := namesOf(t, list("marshal", anvil, sectorGroupsPath), "name"), []string{"Bay keepers", "Deck crew"}; !slices.Equal(got, want) {
		t.Errorf("Anvil's groups after the delete = %v, want %v", got, want)
	}
	if members, roles := standing(); members != 0 || roles != 0 {
		t.Errorf("the deleted group leaves %d members and %d roles, want none", members, roles)
	}
	for name, want := range map[string]int{"ada": http.StatusForbidden, "fay": http.StatusNotFound} {
		if !crewAccountExists(t, w.db, w.ids[name]) {
			t.Errorf("%s's account went with the group, want it kept", name)
		}
		if got := refits(accesstypes.User(name)); got != want {
			t.Errorf("%s lists the refits after the group went: status = %d, want %d", name, got, want)
		}
	}
	if got, want := crewHolds(t, w.db, "ada"), []string{"domain/" + anvil + " Cadet"}; !slices.Equal(got, want) {
		t.Errorf("ada holds %v after the group went, want %v", got, want)
	}
	if got := crewHeldBy(t, w.db, w.ids["fay"]); len(got) != 0 {
		t.Errorf("fay holds %v after the group went, want nothing", got)
	}
}

// TestGeneratedIdentityFleetGroups holds a group kept for the whole fleet, which
// headquarters manages: it holds a role in one sector, in every sector or for the whole
// fleet, its members are people of each sector it holds a role in, and its roles there
// are not that sector's to take away.
func TestGeneratedIdentityFleetGroups(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	hq := func(method, body string) (int, []byte) {
		return doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/"+method, body)
	}
	cinderRefits := func() int {
		status, _ := doRequestAs(t, w.h, "bo", http.MethodGet, sectorPath(cinder, "refits"), "")

		return status
	}
	status, body := hq("create-crew-group", `{"name":"Fleet inspectors","description":"Headquarters' inspectors, sent to a sector at a time."}`)
	assertStatus(t, status, http.StatusOK, body)
	group := cell[string](t, decodeRow(t, body), "id")

	// bo is a Pilot at Anvil and at Bastion and holds nothing at Cinder, nor for the
	// whole fleet.
	if got := cinderRefits(); got != http.StatusNotFound {
		t.Fatalf("bo lists Cinder's refits before the group holds anything there: status = %d, want 404 (the sector is concealed)", got)
	}
	status, body = hq("add-crew-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, w.ids["bo"]))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer","domain":%q}`, group, cinder))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := cinderRefits(); got != http.StatusOK {
		t.Errorf("bo lists Cinder's refits through the fleet's group: status = %d, want 200", got)
	}

	// The fleet's group holds roles wherever a person can, the Super Administrator role
	// excepted: it is given to a person, so that only a super administrator makes
	// another and a place in a group makes none.
	status, body = hq("give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":%q}`, group, accesstypes.SuperAdministrator))
	assertStatus(t, status, http.StatusBadRequest, body)
	if !strings.Contains(string(body), "The Super administrator role is given to a person, never to a group. Only a super administrator makes another, one person at a time.") {
		t.Errorf("the refusal of the Super Administrator role for the fleet's group = %s, want it to say the role is never a group's", body)
	}

	// Cinder lists bo among its people, as one of the fleet's own, and the role among
	// what its people hold through a group; the group itself is no sector's.
	people := sectorList(t, w.h, "governor", cinder, sectorPeoplePath)
	row := people[w.ids["bo"]]
	if row == nil {
		t.Fatalf("Cinder's people leave out bo, who holds a role there through a group: %v", people)
	}
	if !cell[bool](t, row, "heldGlobally") {
		t.Errorf("bo's row at Cinder says heldGlobally = false, and he is in a group kept for the whole fleet: %v", row)
	}
	status, body = doRequestAs(t, w.h, "governor", http.MethodGet, sectorPath(cinder, sectorRolesThroughGroupPath+"?limit=200"), "")
	assertStatus(t, status, http.StatusOK, body)
	var through []string
	for _, row := range decodeRows(t, body) {
		through = append(through, fmt.Sprintf("%s %s %s global=%v", cell[string](t, row, "username"), cell[string](t, row, "groupName"), cell[string](t, row, "role"), cell[bool](t, row, "globalGroup")))
	}
	if want := []string{"bo Fleet inspectors Engineer global=true"}; !slices.Equal(through, want) {
		t.Errorf("the roles held through a group at Cinder = %v, want %v", through, want)
	}
	status, body = doRequestAs(t, w.h, "governor", http.MethodGet, sectorPath(cinder, sectorGroupsPath+"?limit=200"), "")
	assertStatus(t, status, http.StatusOK, body)
	if got := decodeRows(t, body); len(got) != 0 {
		t.Errorf("Cinder's groups = %v, want none: a group kept for the whole fleet is no sector's", got)
	}
	status, body = doRequestAs(t, w.h, "governor", http.MethodPost, sectorPath(cinder, "take-crew-sector-group-role"), fmt.Sprintf(`{"groupId":%q,"role":"Engineer"}`, group))
	assertStatus(t, status, http.StatusNotFound, body)

	// Headquarters' delete of the group asks first, counting its member and its role,
	// and takes nothing.
	status, body = hq("delete-crew-group", fmt.Sprintf(`{"groupId":%q}`, group))
	assertStatus(t, status, http.StatusPreconditionRequired, body)
	if got, want := refusalMessage(t, body), "Delete this group and everything under it: 1 role, 1 member. This can't be undone."; got != want {
		t.Errorf("the question = %q, want %q", got, want)
	}
	if got := cinderRefits(); got != http.StatusOK {
		t.Errorf("bo lists Cinder's refits after the question: status = %d, want 200: the question deletes nothing", got)
	}

	// Headquarters takes the role away and the member out, and the group, which holds
	// nothing, is deleted at once.
	status, body = hq("take-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer","domain":%q}`, group, cinder))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := cinderRefits(); got != http.StatusNotFound {
		t.Errorf("bo lists Cinder's refits after the group lost the role: status = %d, want 404", got)
	}
	status, body = hq("remove-crew-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, w.ids["bo"]))
	assertStatus(t, status, http.StatusNoContent, body)
	if !crewAccountExists(t, w.db, w.ids["bo"]) {
		t.Error("bo's account went with his place in the fleet's group, want it kept")
	}
	status, body = hq("delete-crew-group", fmt.Sprintf(`{"groupId":%q}`, group))
	assertStatus(t, status, http.StatusNoContent, body)
}

// TestGeneratedIdentityGroupDeleteRefusals holds what refuses a group's delete, asked with
// everything under it: what taking each role from the group asks, and what the caller may
// do to a member and to a role. A group whose going would leave a sector without an
// administrator of its own is refused in the sentence that taking the role from the group
// gives. A caller who may delete a group and may not take a person out of one is refused
// a group that has a member, and deletes one that has none; a caller who may not take a
// role from a group is refused one that holds a role, and deletes one that holds none.
// Nothing goes with a refusal. No role the release ships holds the grant to delete a
// group without the other two, so the suite makes two that do, custom roles of the whole
// application.
func TestGeneratedIdentityGroupDeleteRefusals(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)
	ctx := t.Context()

	hq := func(user accesstypes.User, method, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, consoleAPI+"/"+method, body)
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
	deleteGroup := func(user accesstypes.User, group string) (int, []byte) {
		return hq(user, "delete-crew-group", fmt.Sprintf(`{"groupId":%q,"everythingUnder":true}`, group))
	}
	standing := func(group string) (members, roles int64) {
		t.Helper()

		return rowsStanding(t, w, "SELECT COUNT(*) FROM CrewGroupMembers WHERE GroupId = @group", map[string]any{"group": group}),
			rowsStanding(t, w, "SELECT COUNT(*) FROM CrewGroupRoles WHERE GroupId = @group", map[string]any{"group": group})
	}
	refused := func(status int, body []byte, want, group string, members, roles int64) {
		t.Helper()
		assertStatus(t, status, http.StatusForbidden, body)
		if got := refusalMessage(t, body); got != want {
			t.Errorf("the refusal = %q, want %q", got, want)
		}
		if m, r := standing(group); m != members || r != roles {
			t.Errorf("the group has %d members and %d roles after the refusal, want %d and %d: nothing goes", m, r, members, roles)
		}
	}

	// A group that holds a sector's last administrator's role, deleted by that
	// administrator: Bastion's bridge holds the Marshal's role there, and hal, who holds
	// Headquarters, is in it. Nobody else holds the role in Bastion itself.
	w.seat(ctx, t, "hal", "Cadet", bastion)
	w.giveCrewRole(ctx, t, "hal", "Headquarters", accesstypes.GlobalPolicyScope())
	bridge := made(doRequestAs(t, w.h, "governor", http.MethodPost, sectorPath(bastion, "create-crew-sector-group"), `{"name":"Bastion bridge","description":"The crew who stand Bastion's bridge."}`))
	done(hq("governor", "give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"SectorMarshal","domain":%q}`, bridge, bastion)))
	done(hq("governor", "add-crew-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, bridge, w.ids["hal"])))
	status, body := deleteGroup("hal", bridge)
	refused(status, body, "You are the last administrator of this sector. Give another person the role before you take it from the group that gives you yours.", bridge, 1, 1)

	// A caller who may not take a person out of a group, or a role from one.
	const (
		noMembers accesstypes.Role = "GroupClearerOfRoles"
		noRoles   accesstypes.Role = "GroupClearerOfMembers"
	)
	global := accesstypes.GlobalPolicyScope()
	manager := w.engine.UserManager()
	for role, grants := range map[accesstypes.Role][]accesstypes.Resource{
		noMembers: {"DeleteCrewGroup", "TakeCrewGroupRole"},
		noRoles:   {"DeleteCrewGroup", "RemoveCrewGroupMember"},
	} {
		if err := manager.AddRole(ctx, global, role, "Deletes the fleet's groups."); err != nil {
			t.Fatalf("access.UserManager.AddRole(%s) error = %v", role, err)
		}
		for _, grant := range grants {
			if err := manager.AddRoleGrants(ctx, global, role, access.GrantRow{Permission: accesstypes.Execute, Resource: grant}); err != nil {
				t.Fatalf("access.UserManager.AddRoleGrants(%s, %s) error = %v", role, grant, err)
			}
		}
	}
	w.ids["pat"] = string(makeCrewAccount(ctx, t, w.db, "pat"))
	w.ids["quin"] = string(makeCrewAccount(ctx, t, w.db, "quin"))
	for name, role := range map[string]accesstypes.Role{"pat": noMembers, "quin": noRoles} {
		user := accesstypes.User(w.ids[name])
		if err := manager.AddUserRoles(ctx, global, user, role); err != nil {
			t.Fatalf("access.UserManager.AddUserRoles(%s, %s) error = %v", name, role, err)
		}
		if d := decisionFor(ctx, t, w.engine.ForUser(user), accesstypes.GlobalScope(), accesstypes.Execute, "DeleteCrewGroup", time.Now().Add(15*time.Second)); !d.IsGranted() {
			t.Fatalf("Execute on DeleteCrewGroup for %s holding %s = %s, want granted", name, role, d)
		}
	}

	// Four of the fleet's groups: one with a member, one holding a role, one with
	// both, and one with neither.
	group := func(name string, member, role bool) string {
		t.Helper()
		id := made(hq("governor", "create-crew-group", fmt.Sprintf(`{"name":%q,"description":"A group the delete is asked of."}`, name)))
		if member {
			done(hq("governor", "add-crew-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, id, w.ids["bo"])))
		}
		if role {
			done(hq("governor", "give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer","domain":%q}`, id, anvil)))
		}

		return id
	}
	withMember, withRole, withBoth := group("Sweepers", true, false), group("Lamplighters", false, true), group("Riggers", true, true)

	const (
		hasMembers = "This group has members, and you can't remove people from a group, so it can't be deleted."
		holdsRoles = "This group holds roles, and you can't take a role from a group, so it can't be deleted."
	)
	status, body = deleteGroup("pat", withMember)
	refused(status, body, hasMembers, withMember, 1, 0)
	status, body = deleteGroup("pat", withBoth)
	refused(status, body, hasMembers, withBoth, 1, 1)
	status, body = deleteGroup("quin", withRole)
	refused(status, body, holdsRoles, withRole, 0, 1)
	status, body = deleteGroup("quin", withBoth)
	refused(status, body, holdsRoles, withBoth, 1, 1)

	// Each deletes the group that has nothing it may not take.
	done(deleteGroup("pat", withRole))
	done(deleteGroup("quin", withMember))
	for _, id := range []string{withRole, withMember} {
		if m, r := standing(id); m != 0 || r != 0 {
			t.Errorf("the deleted group %s leaves %d members and %d roles, want none", id, m, r)
		}
	}
	if !crewAccountExists(t, w.db, w.ids["bo"]) {
		t.Error("bo's account went with the group he was in, want it kept")
	}
}
