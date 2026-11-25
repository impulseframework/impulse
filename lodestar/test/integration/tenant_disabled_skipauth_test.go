//go:build skipAuth

package integration

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
)

// TestTenantDisabled_aDisabledSectorShutsItsCrewOut proves on the served stack what a
// disabled sector is. Anvil seats a cadet who holds no other sector, the liaison, who
// holds Cinder too, its own marshal, the surveyor, who holds a role in every sector,
// and the governor, a super administrator. Whether a sector is disabled is an ordinary
// field of the record: the surveyor, who corrects a sector's other fields, is refused
// it, and the adjutant, made Headquarters and no super administrator, disables Anvil. From their next request its crew hold nothing in it: the liaison, in
// the session she held before, the marshal, signed in again with her password, and the
// surveyor are answered as by a sector that does not exist, and the liaison's and the
// marshal's charts list Anvil with the one reason ("Anvil is disabled."), the
// marshal's with no word of a way in. The governor still enters it, and his chart
// lists it as a place. The cadet's next sign-in is admitted as a person who holds no
// sector is at the console, with nothing to enter and Anvil listed with its reason;
// the client, whose company's directory names her roles in every sector, signs in to
// the portal and is told the same. Nothing anybody holds in Anvil is touched: its
// roles and groups are as many as before. Enabled again, the liaison and the marshal
// enter it as before. A second sector, founded for the test and disabled, is deleted
// by the adjutant, who is no super administrator and cannot enter it: the delete asks
// the record's grant, and the sector's own door is not its question.
//
// Demonstrates: tenancy.disabled.
func TestTenantDisabled_aDisabledSectorShutsItsCrewOut(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")

	// The cadet holds a role in Anvil and nothing else, global roles included, so a
	// sign-in of hers is decided by Anvil alone.
	const cadet = "anvil-cadet"
	password := personaPassword
	cadetID, err := s.crew.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: cadet, Password: &password})
	if err != nil {
		t.Fatalf("CreateSessionUser(%s) error = %v", cadet, err)
	}
	for _, role := range []string{"Cadet", "SectorCrew"} {
		body := fmt.Appendf(nil, `{"userId":%q,"role":%q,"domain":%q}`, cadetID.String(), role, anvil)
		if status, answer := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), body); status != http.StatusNoContent {
			t.Fatalf("the governor giving the cadet %s in Anvil: status %d, want 204: %s", role, status, answer)
		}
	}
	// Headquarters disables a sector and enables it again. The adjutant is made
	// Headquarters, the role Lodestar ships, and is no super administrator.
	giveCrewRole(ctx, t, s, "Headquarters", "adjutant")
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "adjutant")

	signedIn := map[string]*browser{"the governor": governor}
	for name, username := range map[string]string{"the liaison": "liaison", "the marshal": "marshal", "the surveyor": "surveyor", "the cadet": cadet} {
		b := newBrowser(t, s, consoleAPI)
		b.signIn(ctx, username)
		signedIn[name] = b
	}
	wings := sectorPath(anvil, "wings")
	for name, b := range signedIn {
		if status, body := b.do(ctx, http.MethodGet, wings, nil); status != http.StatusOK {
			t.Fatalf("GET Anvil's wings as %s before Anvil is disabled: status %d, want 200: %s", name, status, body)
		}
	}
	// What a sector that does not exist answers, which a disabled sector answers its crew.
	_, nowhere := signedIn["the liaison"].do(ctx, http.MethodGet, sectorPath("d0000000-0000-4000-8000-00000000d15a", "wings"), nil)
	// The rows of what the crew hold in Anvil: their roles, the sector's groups, and the
	// groups' roles.
	held := func() map[string]int64 {
		t.Helper()

		return map[string]int64{
			"CrewUserRoles":  rowsWhere(ctx, t, s, "CrewUserRoles", "DomainId", anvil),
			"CrewGroups":     rowsWhere(ctx, t, s, "CrewGroups", "DomainId", anvil),
			"CrewGroupRoles": rowsWhere(ctx, t, s, "CrewGroupRoles", "DomainId", anvil),
		}
	}
	heldBefore := held()
	disabled := func() bool {
		t.Helper()

		return readColumn[bool](ctx, t, s.db, "Sectors", spanner.Key{anvil}, "Disabled")
	}

	// The surveyor holds Update on the sector's other fields and not on this one.
	disable := recordOp("patch", "/sectors/"+anvil, `{"disabled":true}`)
	if status, answer := writeRecords(ctx, signedIn["the surveyor"], disable); status != http.StatusForbidden {
		t.Errorf("the surveyor disabling Anvil: status %d, want 403: she holds no Update on the field: %s", status, answer)
	}
	if disabled() {
		t.Fatal("Anvil is disabled after the surveyor's refused update, want it as it was")
	}
	setSectorDisabled(ctx, t, hq, anvil, true)
	if !disabled() {
		t.Fatal("Anvil is not disabled after Headquarters' update")
	}

	// Its crew hold nothing in it from their next request.
	const reason = "Anvil is disabled."
	shutOut := func(name string) {
		t.Helper()
		status, body := signedIn[name].do(ctx, http.MethodGet, wings, nil)
		if status != http.StatusNotFound || string(body) != string(nowhere) {
			t.Errorf("GET Anvil's wings as %s once Anvil is disabled: status %d %s, want 404 %s, as a sector that does not exist", name, status, body, nowhere)
		}
	}
	shutOut("the liaison")
	if got := signedIn["the liaison"].domains(ctx); !slices.Equal(got, []string{cinder}) {
		t.Errorf("the liaison's chart once Anvil is disabled = %v, want Cinder alone", got)
	}
	if got := signedIn["the liaison"].closed(ctx); len(got) != 1 || got[anvil] != reason {
		t.Errorf("the sectors the liaison's chart lists as not available = %v, want Anvil alone, with %q", got, reason)
	}
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")
	signedIn["the marshal"] = marshal
	shutOut("the marshal")
	if got := marshal.closed(ctx); len(got) != 1 || got[anvil] != reason {
		t.Errorf("the sectors the marshal's chart lists as not available = %v, want Anvil alone, with %q and no word of a way in", got, reason)
	}
	shutOut("the surveyor")
	if status, body := governor.do(ctx, http.MethodGet, wings, nil); status != http.StatusOK {
		t.Errorf("GET Anvil's wings as the governor once Anvil is disabled: status %d, want 200: a super administrator still enters it: %s", status, body)
	}
	if got := governor.domains(ctx); !slices.Contains(got, anvil) {
		t.Errorf("the governor's chart once Anvil is disabled = %v, want Anvil among his places", got)
	}
	if got := governor.closed(ctx); len(got) != 0 {
		t.Errorf("the sectors the governor's chart lists as not available = %v, want none", got)
	}

	// A person whose every sector is disabled signs in as a person who holds none.
	again := newBrowser(t, s, consoleAPI)
	again.signIn(ctx, cadet)
	if got := again.domains(ctx); len(got) != 0 {
		t.Errorf("the cadet's chart at a sign-in once Anvil is disabled = %v, want none", got)
	}
	if got := again.closed(ctx); len(got) != 1 || got[anvil] != reason {
		t.Errorf("the sectors the cadet's chart lists as not available = %v, want Anvil alone, with %q", got, reason)
	}
	client := newBrowser(t, s, portalAPI)
	if status, body := client.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET %s/user/session before the client's login: status %d: %s", portalAPI, status, body)
	}
	if status, location := client.loginPortal(ctx, clientUser, "ClientPortal"); status != http.StatusFound || location != portalPage {
		t.Fatalf("the client's portal login once Anvil is disabled: status %d to %q, want %d to %q", status, location, http.StatusFound, portalPage)
	}
	if got := client.closed(ctx); got[anvil] != reason {
		t.Errorf("the sectors the client's portal lists as not available = %v, want Anvil among them, with %q", got, reason)
	}
	if status, body := client.do(ctx, http.MethodGet, portalPath(anvil, "missions"), nil); status != http.StatusNotFound {
		t.Errorf("GET Anvil's tracker as the client once Anvil is disabled: status %d, want 404: %s", status, body)
	}

	if got := held(); !maps.Equal(got, heldBefore) {
		t.Errorf("the rows of what the crew hold in Anvil once it is disabled = %v, want %v: nothing is touched", got, heldBefore)
	}

	// Enabled again, everyone has what they had.
	setSectorDisabled(ctx, t, hq, anvil, false)
	for _, name := range []string{"the liaison", "the marshal"} {
		if status, body := signedIn[name].do(ctx, http.MethodGet, wings, nil); status != http.StatusOK {
			t.Errorf("GET Anvil's wings as %s once Anvil is enabled again: status %d, want 200: %s", name, status, body)
		}
		if got := signedIn[name].domains(ctx); !slices.Contains(got, anvil) {
			t.Errorf("%s's chart once Anvil is enabled again = %v, want Anvil among the places", name, got)
		}
		if got := signedIn[name].closed(ctx); len(got) != 0 {
			t.Errorf("the sectors %s's chart lists as not available once Anvil is enabled again = %v, want none", name, got)
		}
	}
	if got := held(); !maps.Equal(got, heldBefore) {
		t.Errorf("the rows of what the crew hold in Anvil once it is enabled again = %v, want %v", got, heldBefore)
	}

	// A person who cannot enter a disabled sector still deletes it: the adjutant, given
	// the suite's role that deletes a sector, deletes Drift once it is disabled.
	const remover accesstypes.Role = "SectorRemover"
	sectors := accesstypes.Resource("Sectors")
	manager, global := s.access.UserManager(), accesstypes.GlobalPolicyScope()
	if err := manager.AddRole(ctx, global, remover, "Deletes a sector the fleet has left."); err != nil {
		t.Fatalf("access.UserManager.AddRole(%s) error = %v", remover, err)
	}
	if err := manager.AddRoleGrants(ctx, global, remover, access.GrantRow{Permission: accesstypes.Delete, Resource: sectors}); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants(%s) error = %v", remover, err)
	}
	adjutant := account(t, "adjutant")
	if err := manager.AddUserRoles(ctx, global, adjutant, remover); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(adjutant, %s) error = %v", remover, err)
	}
	if d := decisionFor(ctx, t, s.access.ForUser(adjutant), accesstypes.GlobalScope(), accesstypes.Delete, sectors, time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("Delete on Sectors for the adjutant holding %s = %s, want granted", remover, d)
	}
	drift := foundSector(ctx, t, s, governor, "Drift", "Far frontier", "2226-12-01", "drift-founder")
	setSectorDisabled(ctx, t, hq, drift, true)
	if status, answer := writeRecords(ctx, hq, recordOp("remove", "/sectors/"+drift, `{"everythingUnder":true}`)); status != http.StatusOK {
		t.Fatalf("the adjutant deleting Drift once it is disabled: status %d, want 200: %s", status, answer)
	}
	if n := rowsWhere(ctx, t, s, "Sectors", "Id", drift); n != 0 {
		t.Errorf("Sectors holds %d row(s) of Drift after the delete, want none", n)
	}
}

// setSectorDisabled disables the sector, or enables it again, through the record's
// update in the browser's session, and fails the test unless it answers 200.
func setSectorDisabled(ctx context.Context, t *testing.T, b *browser, sector string, disabled bool) {
	t.Helper()

	if status, answer := writeRecords(ctx, b, recordOp("patch", "/sectors/"+sector, fmt.Sprintf(`{"disabled":%t}`, disabled))); status != http.StatusOK {
		t.Fatalf("setting the disabled field of sector %s to %t: status %d, want 200: %s", sector, disabled, status, answer)
	}
}
