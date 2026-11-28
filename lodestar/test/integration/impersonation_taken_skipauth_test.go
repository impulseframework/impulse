//go:build skipAuth

package integration

// impersonation_taken_skipauth_test: a row a record's delete takes with it is asked
// what its own removal is asked, so a person who acts as someone else does not remove
// their own record, or a record of a set they are in, by deleting the record it is
// under. Proven on the served stack over Spanner.

import (
	"fmt"
	"net/http"
	"testing"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"
)

// TestImpersonation_ownRecordTakenWithARecord proves that the delete of a record asks,
// of every row it would take with it, the question that row's own removal asks in a
// session that acts as someone else (RefuseImpersonatorAs<Name>Subject, registered on
// the taken row's delete). The pilot flies with Tongs and is given the grants to act as
// a role and as a person. Every row a case needs is written straight to the database,
// in Anvil where the record is a sector's, with ids of its own.
//
// A delete that would take the pilot's own row is refused whole, in that row's own
// sentence, sent plain and saying it takes everything under the record, and the record
// and every row under it stand: a client with a contact that is the pilot, acting as
// Headquarters; a wing with a squadron and the pilot's place in it, a hangar with a pass
// that names the pilot, and a hangar with a pass given to Tongs, acting as the Sector
// Marshal; and the last again acting as the marshal herself, the kind that acts as a
// person, which keeps the passes' writes out of its mask. The same deletes over another
// person's rows are asked the question and then go, with every row under them. The
// governor, acting as Headquarters, is refused the delete of a client whose contact he
// is; his session ended, the same delete goes through, because the rule is for a session
// that acts as someone else. The mask is asked of a taken row as well: the cadet,
// standing in for the governor (the kind whose mask refuses the class access), is
// refused the delete of a client whose contact names the yeoman, as the contact's own
// delete would be refused, and the client and the contact stand.
func TestImpersonation_ownRecordTakenWithARecord(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	pilot, yeoman, governorID := string(account(t, "pilot")), string(account(t, "yeoman")), string(account(t, "governor"))
	giveCrewRole(ctx, t, s, "StandIn", "pilot")
	giveCrewRole(ctx, t, s, "CommandStaff", "pilot")

	const (
		headquartersRole  = `{"kind":"role","principal":"Headquarters","reason":"covering the headquarters desk"}`
		sectorMarshalRole = `{"kind":"role","principal":"SectorMarshal","reason":"covering the marshal's desk"}`
		batch             = consoleAPI + "/resources"
		clients           = consoleAPI + "/clients"
	)
	headquarters := actingBrowser(ctx, t, s, "pilot", headquartersRole)
	sectorMarshal := actingBrowser(ctx, t, s, "pilot", sectorMarshalRole)
	asMarshal := actingBrowser(ctx, t, s, "pilot", actAs(t, "marshal", "standing in for the marshal", ""))
	hangars := sectorPath(anvil, "hangars")

	// Every row has an id of its own: a kind's three hex digits, then a count over the
	// test.
	written := 0
	id := func(kind string) string {
		written++

		return fmt.Sprintf("%s00000-0000-4000-8000-0000000e%04d", kind, written)
	}
	write := func(rows ...*spanner.Mutation) {
		t.Helper()

		if _, err := s.db.Apply(ctx, rows); err != nil {
			t.Fatalf("spanner.Apply(): %v", err)
		}
	}
	client := func(clientID string) *spanner.Mutation {
		return spanner.Insert("Clients", []string{"Id", "Name", "ContactName", "ContactEmail", "Trusted"}, []any{clientID, "Taken Outfit " + clientID[32:], "Taken Contact", "taken@example.com", false})
	}
	contact := func(contactID, clientID, userID string) *spanner.Mutation {
		return spanner.Insert("ClientContacts", []string{"Id", "UserId", "ClientId", "DisplayName"}, []any{contactID, userID, clientID, "Taken Contact"})
	}
	wing := func(wingID string) *spanner.Mutation {
		return spanner.Insert("Wings", []string{"Id", "SectorId", "Name"}, []any{wingID, anvil, "Taken Wing"})
	}
	squadron := func(squadronID, wingID string) *spanner.Mutation {
		return spanner.Insert("Squadrons", []string{"Id", "WingId", "Name"}, []any{squadronID, wingID, "Taken Squadron"})
	}
	membership := func(squadronID, userID string) *spanner.Mutation {
		return spanner.Insert("SquadronMemberships", []string{"SquadronId", "UserId"}, []any{squadronID, userID})
	}
	hangar := func(hangarID string) *spanner.Mutation {
		return spanner.Insert("Hangars", []string{"Id", "SectorId", "Name", "Zone"}, []any{hangarID, anvil, "Taken Dock", "dock"})
	}
	// pass is a pass into the hangar, held by a person or by a squadron (column).
	pass := func(passID, hangarID, column, holder string) *spanner.Mutation {
		return spanner.InsertMap("HangarPasses", map[string]any{"Id": passID, "SectorId": anvil, "HangarId": hangarID, column: holder, "LevelId": "visit"})
	}

	stands := func(row takenRow) bool {
		t.Helper()

		_, err := s.db.Single().ReadRow(ctx, row.table, row.key, []string{row.column})
		if err != nil && spanner.ErrCode(err) != codes.NotFound {
			t.Fatalf("ReadRow(%s, %v): %v", row.table, row.key, err)
		}

		return err == nil
	}
	remove := func(path string, everythingUnder bool) []byte {
		if !everythingUnder {
			return fmt.Appendf(nil, `[{"op":"remove","path":%q}]`, path)
		}

		return fmt.Appendf(nil, `[{"op":"remove","path":%q,"value":{"everythingUnder":true}}]`, path)
	}
	// removeOn is the remove that goes ahead on the token the question gave the session.
	removeOn := func(path, token string) []byte {
		return fmt.Appendf(nil, `[{"op":"remove","path":%q,"value":{"everythingUnder":true,"token":%q}}]`, path, token)
	}
	// refused sends the delete twice, plain and saying it takes everything under the
	// record: both are refused with the sentence, and every row stands.
	refused := func(name string, b *browser, target, path, says string, rows []takenRow) {
		t.Helper()

		for _, everythingUnder := range []bool{false, true} {
			status, body := b.do(ctx, http.MethodPatch, target, remove(path, everythingUnder))
			if want := actingAsSomeoneElse + says + "."; status != http.StatusForbidden || refusalMessage(t, body) != want {
				t.Errorf("%s (everythingUnder %v): status %d, want 403 saying %q: %s", name, everythingUnder, status, want, body)
			}
		}
		for _, row := range rows {
			if !stands(row) {
				t.Errorf("%s: %s %v is gone, though the delete was refused", name, row.table, row.key)
			}
		}
	}
	// askedThenTaken sends the delete plain, which is asked the question and takes
	// nothing, and then saying it takes everything under the record, which deletes every
	// row.
	askedThenTaken := func(name string, b *browser, target, path, question string, rows []takenRow) {
		t.Helper()

		status, body := b.do(ctx, http.MethodPatch, target, remove(path, false))
		if status != http.StatusPreconditionRequired || refusalMessage(t, body) != question {
			t.Errorf("%s, plain: status %d, want 428 asking %q: %s", name, status, question, body)
		}
		for _, row := range rows {
			if !stands(row) {
				t.Errorf("%s: %s %v is gone after the question, though the delete was not made", name, row.table, row.key)
			}
		}
		status, body = b.do(ctx, http.MethodPatch, target, removeOn(path, deleteToken(t, body)))
		if status != http.StatusOK {
			t.Errorf("%s, saying it takes everything under it on the question's token: status %d, want 200: %s", name, status, body)
		}
		for _, row := range rows {
			if stands(row) {
				t.Errorf("%s: %s %v stands after its record was deleted with everything under it", name, row.table, row.key)
			}
		}
	}

	// The pilot's own rows, under a record the session's role may delete.
	ownClient, ownContact := id("a10"), id("b10")
	ownWing, ownSquadron := id("a20"), id("b20")
	ownPassHangar, ownPass := id("a30"), id("b30")
	tongsHangar, tongsPass := id("a40"), id("b40")
	tongsHangarAsMarshal, tongsPassAsMarshal := id("a50"), id("b50")
	write(
		client(ownClient), contact(ownContact, ownClient, pilot),
		wing(ownWing), squadron(ownSquadron, ownWing), membership(ownSquadron, pilot),
		hangar(ownPassHangar), pass(ownPass, ownPassHangar, "UserId", pilot),
		hangar(tongsHangar), pass(tongsPass, tongsHangar, "SquadronId", squadronTongsID),
		hangar(tongsHangarAsMarshal), pass(tongsPassAsMarshal, tongsHangarAsMarshal, "SquadronId", squadronTongsID),
	)
	refused("acting as Headquarters, a client whose contact is the pilot", headquarters, clients, "/"+ownClient,
		"remove your own client contact record", []takenRow{rowByID("Clients", ownClient), rowByID("ClientContacts", ownContact)})
	refused("acting as the Sector Marshal, a wing with a squadron the pilot is in", sectorMarshal, batch, opPath(anvil, "wings/"+ownWing),
		"remove your own squadron membership record", []takenRow{
			rowByID("Wings", ownWing), rowByID("Squadrons", ownSquadron),
			{table: "SquadronMemberships", key: spanner.Key{ownSquadron, pilot}, column: "UserId"},
		})
	refused("acting as the Sector Marshal, a hangar with a pass that names the pilot", sectorMarshal, hangars, "/"+ownPassHangar,
		"remove your own hangar pass record", []takenRow{rowByID("Hangars", ownPassHangar), rowByID("HangarPasses", ownPass)})
	refused("acting as the Sector Marshal, a hangar with a pass given to Tongs", sectorMarshal, hangars, "/"+tongsHangar,
		"remove a hangar pass record of one of your own squadrons", []takenRow{rowByID("Hangars", tongsHangar), rowByID("HangarPasses", tongsPass)})
	refused("acting as the marshal, a hangar with a pass given to Tongs", asMarshal, hangars, "/"+tongsHangarAsMarshal,
		"remove a hangar pass record of one of your own squadrons", []takenRow{rowByID("Hangars", tongsHangarAsMarshal), rowByID("HangarPasses", tongsPassAsMarshal)})

	// The rule is for the pilot's own rows: the same deletes over another person's go.
	yeomansClient, yeomansContact := id("a60"), id("b60")
	hammerHangar, hammerPass := id("a70"), id("b70")
	write(
		client(yeomansClient), contact(yeomansContact, yeomansClient, yeoman),
		hangar(hammerHangar), pass(hammerPass, hammerHangar, "SquadronId", squadronHammerID),
	)
	askedThenTaken("acting as Headquarters, a client whose contact is the yeoman", headquarters, clients, "/"+yeomansClient,
		"Delete this client and everything under it: 1 client contact. This can't be undone.",
		[]takenRow{rowByID("Clients", yeomansClient), rowByID("ClientContacts", yeomansContact)})
	askedThenTaken("acting as the Sector Marshal, a hangar with a pass given to Hammer", sectorMarshal, hangars, "/"+hammerHangar,
		"Delete this hangar and everything under it: 1 hangar pass. This can't be undone.",
		[]takenRow{rowByID("Hangars", hammerHangar), rowByID("HangarPasses", hammerPass)})

	// The governor holds Headquarters himself. Acting as the role, a client whose
	// contact he is is refused him; his session ended, the same delete goes through.
	governorsClient, governorsContact := id("a80"), id("b80")
	write(client(governorsClient), contact(governorsContact, governorsClient, governorID))
	governorRows := []takenRow{rowByID("Clients", governorsClient), rowByID("ClientContacts", governorsContact)}
	governor := actingBrowser(ctx, t, s, "governor", headquartersRole)
	refused("the governor acting as Headquarters, a client whose contact he is", governor, clients, "/"+governorsClient,
		"remove your own client contact record", governorRows)
	if status, answer := governor.do(ctx, http.MethodPost, consoleAPI+"/impersonate/end", nil); status != http.StatusOK {
		t.Fatalf("the governor ending his session: status %d, want 200: %s", status, answer)
	}
	status, body := governor.do(ctx, http.MethodPatch, clients, remove("/"+governorsClient, false))
	assertStatus(t, status, http.StatusPreconditionRequired, body)
	if status, body := governor.do(ctx, http.MethodPatch, clients, removeOn("/"+governorsClient, deleteToken(t, body))); status != http.StatusOK {
		t.Errorf("the governor in his own session again, a client whose contact he is: status %d, want 200: %s", status, body)
	}
	for _, row := range governorRows {
		if stands(row) {
			t.Errorf("%s %v stands after the governor deleted its client with everything under it in his own session", row.table, row.key)
		}
	}

	// The cadet, given the grant to act as a person, stands in for the governor. The
	// kind's mask refuses the class access, which the contact's delete carries, so the
	// client is refused whoever its contact names.
	giveCrewRole(ctx, t, s, "StandIn", "cadet")
	cadetAsGovernor := actingBrowser(ctx, t, s, "cadet", actAs(t, "governor", "standing in for the governor", ""))
	maskedClient, maskedContact := id("a90"), id("b90")
	write(client(maskedClient), contact(maskedContact, maskedClient, yeoman))
	for _, everythingUnder := range []bool{false, true} {
		status, body := cadetAsGovernor.do(ctx, http.MethodPatch, clients, remove("/"+maskedClient, everythingUnder))
		if want := "This client has client contacts that you can't delete, so it can't be deleted."; status != http.StatusForbidden || refusalMessage(t, body) != want {
			t.Errorf("the cadet as the governor, a client whose contact is the yeoman (everythingUnder %v): status %d, want 403 saying %q: %s", everythingUnder, status, want, body)
		}
	}
	for _, row := range []takenRow{rowByID("Clients", maskedClient), rowByID("ClientContacts", maskedContact)} {
		if !stands(row) {
			t.Errorf("the cadet as the governor: %s %v is gone, though the delete was refused", row.table, row.key)
		}
	}
}
