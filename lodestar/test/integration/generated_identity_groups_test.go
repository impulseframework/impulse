// Demonstrates: auth.groups, auth.generated-identity, auth.tenant-methods, execute-condition.request-field, @subjectSet.global.
package integration

// generated_identity_groups_test: the groups of people the generated management of the
// crew auth serves, because the schema holds the permission store's group tables.
//
// A group holds roles the way a person does, and a person in it holds them wherever the
// group does. A sector's group is made and managed under the sector's address by the
// sector's roles, and holds roles there alone; a group is deleted only once it has no
// member and holds no role. The suite drives the methods as served, against the real
// engine over the shipped role file: what a member may do changes with the group's
// roles and with their place in it, at their next request.
//
// The people are the ones of the lists' suite (ada and fay Cadets at Anvil, bo a Pilot at
// Anvil and at Bastion), the cast's Marshal of Anvil, and the governor, who runs
// headquarters. The cast's own group, Anvil's deck crew, is in every world; the
// deckhand's account is not in this one, so the lists hold the people the test makes.

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

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
// the groups they are in themselves (group IN subject.groups), a group of one sector is
// not found in another and holds no role there, no group holds the Super Administrator
// role, a place in a group keeps an account as a role does, and a group that still has a
// member or a role is not deleted.
func TestGeneratedIdentityGroups(t *testing.T) {
	t.Parallel()
	w := newTenantListsWorld(t)

	// The grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "GiveCrewSectorGroupRole", "new.role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "TakeCrewSectorGroupRole", "role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorCrew", "List", "CrewSectorGroups", "group IN subject.groups")
	provesGrant(t, crew.Roles(), "SectorCrew", "Read", "CrewSectorGroups", "group IN subject.groups")
	provesGrant(t, crew.Roles(), "SectorCrew", "List", "CrewSectorGroupUsers", "group IN subject.groups")

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

	if got, want := namesOf(t, list("marshal", anvil, sectorGroupsPath), "name"), []string{"Deck crew", "Hangar hands"}; !slices.Equal(got, want) {
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
	if !strings.Contains(string(body), "never to a group") {
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
	// sector crew's grant is held to subject.groups, the set the members table declares.
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

	// A group goes only once it is emptied.
	status, body = post("marshal", anvil, "delete-crew-sector-group", fmt.Sprintf(`{"groupId":%q}`, group))
	assertStatus(t, status, http.StatusConflict, body)

	// A place in a group keeps an account as a role of its own does: fay joins, loses
	// her own role and stays; taken out of the group she holds nothing, and her account
	// goes. She holds a role for the whole application too, which is taken first.
	status, body = post("marshal", anvil, "add-crew-sector-group-member", member("fay"))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/remove-crew-role", fmt.Sprintf(`{"userId":%q,"role":"CrewCommon"}`, w.ids["fay"]))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = post("marshal", anvil, "remove-crew-sector-role", fmt.Sprintf(`{"userId":%q,"role":"Cadet"}`, w.ids["fay"]))
	assertStatus(t, status, http.StatusNoContent, body)
	if !crewAccountExists(t, w.db, w.ids["fay"]) {
		t.Fatal("fay's account went with her last role of her own, and she is still in a group")
	}
	if got := refits("fay"); got != http.StatusOK {
		t.Errorf("fay lists the refits through the group alone: status = %d, want 200", got)
	}
	status, body = post("marshal", anvil, "remove-crew-sector-group-member", member("fay"))
	assertStatus(t, status, http.StatusNoContent, body)
	if crewAccountExists(t, w.db, w.ids["fay"]) {
		t.Error("fay's account stands, and she holds no role and no place in a group")
	}

	// A role taken from the group is no longer its member's; a member taken out keeps
	// what she holds herself.
	status, body = post("marshal", anvil, "take-crew-sector-group-role", role("Engineer"))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := refits("ada"); got != http.StatusForbidden {
		t.Errorf("ada lists the refits after the group lost the role: status = %d, want 403", got)
	}
	status, body = post("marshal", anvil, "remove-crew-sector-group-member", member("ada"))
	assertStatus(t, status, http.StatusNoContent, body)
	if !crewAccountExists(t, w.db, w.ids["ada"]) {
		t.Error("ada's account went with her place in the group, and she holds a role of her own")
	}
	if got, want := crewHolds(t, w.db, "ada"), []string{"domain/" + anvil + " Cadet"}; !slices.Equal(got, want) {
		t.Errorf("ada holds %v, want %v", got, want)
	}

	// Emptied, the group is deleted.
	status, body = post("marshal", anvil, "delete-crew-sector-group", fmt.Sprintf(`{"groupId":%q}`, group))
	assertStatus(t, status, http.StatusConflict, body)
	status, body = post("marshal", anvil, "take-crew-sector-group-role", role("SectorCrew"))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = post("marshal", anvil, "delete-crew-sector-group", fmt.Sprintf(`{"groupId":%q}`, group))
	assertStatus(t, status, http.StatusNoContent, body)
	if got, want := namesOf(t, list("marshal", anvil, sectorGroupsPath), "name"), []string{"Deck crew"}; !slices.Equal(got, want) {
		t.Errorf("Anvil's groups after the delete = %v, want %v", got, want)
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
	if !strings.Contains(string(body), "never to a group") {
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

	// Headquarters takes the role away and empties the group, then deletes it.
	status, body = hq("delete-crew-group", fmt.Sprintf(`{"groupId":%q}`, group))
	assertStatus(t, status, http.StatusConflict, body)
	status, body = hq("take-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Engineer","domain":%q}`, group, cinder))
	assertStatus(t, status, http.StatusNoContent, body)
	if got := cinderRefits(); got != http.StatusNotFound {
		t.Errorf("bo lists Cinder's refits after the group lost the role: status = %d, want 404", got)
	}
	status, body = hq("remove-crew-group-member", fmt.Sprintf(`{"groupId":%q,"userId":%q}`, group, w.ids["bo"]))
	assertStatus(t, status, http.StatusNoContent, body)
	if !crewAccountExists(t, w.db, w.ids["bo"]) {
		t.Error("bo's account went with his place in the fleet's group, and he holds roles of his own")
	}
	status, body = hq("delete-crew-group", fmt.Sprintf(`{"groupId":%q}`, group))
	assertStatus(t, status, http.StatusNoContent, body)
}
