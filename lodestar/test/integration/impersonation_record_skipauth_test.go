//go:build skipAuth

package integration

// impersonation_record_skipauth_test: a record that decides what a person may do is
// never the real person's own to write while they act as someone else, proven on the
// served stack over Spanner. A Pilot row carries the clearance and the fee limit the
// policy reads as subject.clearance and subject.feeLimit; a PilotCertifications row, a
// SquadronMemberships row and a HangarPasses row each put a value in one of the person's
// subject sets. The generated write of each asks the identity library's rule
// (identity.RefuseImpersonatorAsRowSubject) about the person the row names, before the
// write and after it. The kind that acts as a person refuses those records' writes by
// name as well, whoever the row names, so a stand-in changes nobody's standing.

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"google.golang.org/api/iterator"
)

// recordOp is one operation of the consolidated patch: add, patch, remove or add-or-skip
// at a record's path, with the fields it writes as a JSON object, "" for a remove, which
// writes none.
func recordOp(kind, path, value string) string {
	if value == "" {
		return fmt.Sprintf(`{"op":%q,"path":%q}`, kind, path)
	}

	return fmt.Sprintf(`{"op":%q,"path":%q,"value":%s}`, kind, path, value)
}

// writeRecords sends the operations as one request of the console's consolidated patch.
func writeRecords(ctx context.Context, b *browser, ops ...string) (status int, answer string) {
	b.t.Helper()

	status, body := b.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte("["+strings.Join(ops, ",")+"]"))

	return status, string(body)
}

// standing reads every row of the records that decide what a person may do, straight
// from the database, one line a row: the pilots with their clearance and fee limit, the
// certifications, the places in a squadron, the hangar passes, the client contacts, and
// the wing of each squadron. Two readings that are equal mean nobody's standing changed.
func standing(ctx context.Context, t *testing.T, s *served) []string {
	t.Helper()

	var rows []string
	for _, sql := range []string{
		`SELECT CONCAT('Pilots ', Id, ' user ', UserId, ' clearance ', CAST(Clearance AS STRING), ' fee limit ', CAST(FeeLimit AS STRING)) FROM Pilots`,
		`SELECT CONCAT('PilotCertifications ', UserId, ' ', CertificationId) FROM PilotCertifications`,
		`SELECT CONCAT('SquadronMemberships ', SquadronId, ' ', UserId) FROM SquadronMemberships`,
		`SELECT CONCAT('HangarPasses ', Id, ' hangar ', HangarId, ' user ', IFNULL(UserId, 'none'), ' squadron ', IFNULL(SquadronId, 'none'), ' level ', LevelId) FROM HangarPasses`,
		`SELECT CONCAT('ClientContacts ', Id, ' user ', UserId, ' client ', ClientId) FROM ClientContacts`,
		`SELECT CONCAT('Squadrons ', Id, ' wing ', WingId) FROM Squadrons`,
	} {
		iter := s.db.Single().Query(ctx, spanner.Statement{SQL: sql})
		for {
			row, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				iter.Stop()
				t.Fatalf("reading %s: %v", sql, err)
			}
			var line string
			if err := row.Columns(&line); err != nil {
				iter.Stop()
				t.Fatalf("reading a row of %s: %v", sql, err)
			}
			rows = append(rows, line)
		}
		iter.Stop()
	}
	slices.Sort(rows)

	return rows
}

// standingChanges lists the rows one reading of standing has and the other lacks, for a
// failure to say what a write that should have been refused changed.
func standingChanges(before, after []string) string {
	var changes []string
	for _, row := range before {
		if !slices.Contains(after, row) {
			changes = append(changes, "gone: "+row)
		}
	}
	for _, row := range after {
		if !slices.Contains(before, row) {
			changes = append(changes, "new: "+row)
		}
	}

	return strings.Join(changes, "; ")
}

// giveCrewRole gives each persona the role for the whole application, as the governor
// signed in on a browser of his own. A persona signs in after it to hold the role.
func giveCrewRole(ctx context.Context, t *testing.T, s *served, role string, usernames ...accesstypes.User) {
	t.Helper()

	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	for _, username := range usernames {
		body := fmt.Appendf(nil, `{"userId":%q,"role":%q}`, account(t, username), role)
		if status, answer := governor.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), body); status != http.StatusNoContent {
			t.Fatalf("the governor giving %s the %s role: status %d, want 204: %s", username, role, status, answer)
		}
	}
}

// actingBrowser signs the persona in on a browser of its own and starts the impersonated
// session the body asks for.
func actingBrowser(ctx context.Context, t *testing.T, s *served, username, body string) *browser {
	t.Helper()

	b := newBrowser(t, s, consoleAPI)
	b.signIn(ctx, username)
	mint(ctx, t, b, body)

	return b
}

// recordWrite is one write of a record that decides what a person may do, sent from an
// impersonated session, and what the answer says when the rule is what refuses it.
type recordWrite struct {
	name string
	by   *browser
	ops  []string
	says string
}

// TestImpersonation_ownRecordUnderARole proves the rule where no mask stands in front of
// it: a session that acts as a role, whose mask refuses nothing. The cadet, given the
// grant to act as a role, acts as Headquarters, the role that keeps the registry of
// pilots, and as the Sector Marshal, who keeps the squadrons and the hangar passes. A
// write of a record that decides what the cadet may do is refused, with the sentence
// that says they are acting as someone else: a higher clearance and fee limit on their
// own Pilot row (the sequence the review of the mask gave), that row handed to someone
// else or removed, a certification, a place in a squadron and a hangar pass of their
// own, and a batch that carries one such write. The yeoman, who has no Pilot row, is
// refused a veteran's row moved onto their account and a row made for it. Nothing in the
// database changes. The same write for another person goes through, and once the session
// is ended, a person's own row is theirs to write again.
//
// Demonstrates: impersonation.never-the-subject, impersonation.act-as-role.
func TestImpersonation_ownRecordUnderARole(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	cadet, yeoman, pilot := string(account(t, "cadet")), string(account(t, "yeoman")), string(account(t, "pilot"))
	giveCrewRole(ctx, t, s, "CommandStaff", "cadet", "yeoman")

	const headquartersRole = `{"kind":"role","principal":"Headquarters","reason":"covering the headquarters desk"}`
	headquarters := actingBrowser(ctx, t, s, "cadet", headquartersRole)
	sectorMarshal := actingBrowser(ctx, t, s, "cadet", `{"kind":"role","principal":"SectorMarshal","reason":"covering the marshal's desk"}`)
	yeomanAtHeadquarters := actingBrowser(ctx, t, s, "yeoman", headquartersRole)

	ownPilot, ownCertification := "/pilots/"+pilotCassID, "/pilot-certifications/"+cadet+"/hazmat"
	refused := []recordWrite{
		{
			name: "a higher clearance and fee limit on their own Pilot row", by: headquarters, says: "change your own pilot record",
			ops: []string{recordOp("patch", ownPilot, `{"clearance":5,"feeLimit":999999}`)},
		},
		{
			name: "their own Pilot row handed to someone else", by: headquarters, says: "change your own pilot record",
			ops: []string{recordOp("patch", ownPilot, fmt.Sprintf(`{"userId":%q}`, yeoman))},
		},
		{
			name: "their own Pilot row removed", by: headquarters, says: "remove your own pilot record",
			ops: []string{recordOp("remove", ownPilot, "")},
		},
		{
			name: "a certification of their own", by: headquarters, says: "add your own pilot certification record",
			ops: []string{recordOp("add", ownCertification, `{}`)},
		},
		{
			name: "a certification of their own, added unless it is there", by: headquarters, says: "add your own pilot certification record",
			ops: []string{recordOp("add-or-skip", ownCertification, `{}`)},
		},
		{
			name: "a batch that rates another pilot and then their own row", by: headquarters, says: "change your own pilot record",
			ops: []string{recordOp("patch", "/pilots/"+pilotPaxID, `{"clearance":4}`), recordOp("patch", ownPilot, `{"clearance":5}`)},
		},
		{
			name: "a place in a squadron for themselves", by: sectorMarshal, says: "add your own squadron membership record",
			ops: []string{recordOp("add", opPath(anvil, "squadron-memberships/"+squadronTongsID+"/"+cadet), `{}`)},
		},
		{
			name: "a hangar pass for themselves", by: sectorMarshal, says: "add your own hangar pass record",
			ops: []string{recordOp("add", opPath(anvil, "hangar-passes"), fmt.Sprintf(`{"hangarId":%q,"userId":%q,"levelId":"run"}`, hangarQuarantineID, cadet))},
		},
		{
			name: "a veteran's Pilot row moved onto their own account", by: yeomanAtHeadquarters, says: "make another pilot record your own",
			ops: []string{recordOp("patch", "/pilots/"+pilotVelaID, fmt.Sprintf(`{"userId":%q}`, yeoman))},
		},
		{
			name: "a Pilot row made for their own account", by: yeomanAtHeadquarters, says: "add your own pilot record",
			ops: []string{recordOp("add", "/pilots", fmt.Sprintf(`{"userId":%q,"displayName":"Yeoman Yarrow","clearance":5,"feeLimit":999999}`, yeoman))},
		},
	}
	before := standing(ctx, t, s)
	for _, write := range refused {
		status, answer := writeRecords(ctx, write.by, write.ops...)
		if want := actingAsSomeoneElse + write.says + ":"; status != http.StatusForbidden || !strings.Contains(answer, want) {
			t.Errorf("acting as a role, %s: status %d, want 403 saying %q: %s", write.name, status, want, answer)
		}
		if after := standing(ctx, t, s); !slices.Equal(after, before) {
			t.Errorf("acting as a role, %s: the write changed what a person may do: %s", write.name, standingChanges(before, after))
			before = after
		}
	}

	// The rule is for a person's own row. The same writes for another person go
	// through: the role holds them, and no mask stands in front of a role.
	others := []recordWrite{
		{name: "another pilot's clearance", by: headquarters, ops: []string{recordOp("patch", "/pilots/"+pilotPaxID, `{"clearance":4}`)}},
		{name: "another pilot's certification", by: headquarters, ops: []string{recordOp("add", "/pilot-certifications/"+pilot+"/hazmat", `{}`)}},
		{name: "another person's place in a squadron", by: sectorMarshal, ops: []string{recordOp("add", opPath(anvil, "squadron-memberships/"+squadronTongsID+"/"+yeoman), `{}`)}},
		{
			name: "another person's hangar pass", by: sectorMarshal,
			ops: []string{recordOp("add", opPath(anvil, "hangar-passes"), fmt.Sprintf(`{"hangarId":%q,"userId":%q,"levelId":"run"}`, hangarQuarantineID, yeoman))},
		},
	}
	for _, write := range others {
		if status, answer := writeRecords(ctx, write.by, write.ops...); status != http.StatusOK {
			t.Errorf("acting as a role, %s: status %d, want 200: %s", write.name, status, answer)
		}
	}

	// Each session ended, the cadet's clearance is the one the registry gave them, and
	// neither a certification nor a Pilot row was made for an account that had none.
	for _, b := range []*browser{headquarters, sectorMarshal, yeomanAtHeadquarters} {
		if status, answer := b.do(ctx, http.MethodPost, consoleAPI+"/impersonate/end", nil); status != http.StatusOK {
			t.Fatalf("ending a session that acts as a role: status %d, want 200: %s", status, answer)
		}
	}
	if got := readColumn[int64](ctx, t, s.db, "Pilots", spanner.Key{pilotCassID}, "Clearance"); got != 2 {
		t.Errorf("the cadet's clearance after the sessions = %d, want the 2 the registry gave them", got)
	}
	for _, row := range standing(ctx, t, s) {
		if strings.HasPrefix(row, "PilotCertifications "+cadet+" ") || strings.Contains(row, " user "+yeoman+" clearance ") {
			t.Errorf("after the sessions the database holds %q, which a refused write made", row)
		}
	}

	// The governor holds headquarters himself. Acting as the role, his own Pilot row is
	// refused him; the session ended, the same request from the same browser goes
	// through, because the rule is for a session that acts as someone else.
	governor := actingBrowser(ctx, t, s, "governor", headquartersRole)
	raise := recordOp("patch", "/pilots/"+pilotGreerID, `{"clearance":4}`)
	if status, answer := writeRecords(ctx, governor, raise); status != http.StatusForbidden || !strings.Contains(answer, actingAsSomeoneElse+"change your own pilot record:") {
		t.Errorf("the governor acting as Headquarters, his own Pilot row: status %d, want 403 saying he is acting as someone else: %s", status, answer)
	}
	if status, answer := governor.do(ctx, http.MethodPost, consoleAPI+"/impersonate/end", nil); status != http.StatusOK {
		t.Fatalf("the governor ending his session: status %d, want 200: %s", status, answer)
	}
	if status, answer := writeRecords(ctx, governor, raise); status != http.StatusOK {
		t.Errorf("the governor in his own session again, his own Pilot row: status %d, want 200: %s", status, answer)
	}
	if got := readColumn[int64](ctx, t, s.db, "Pilots", spanner.Key{pilotGreerID}, "Clearance"); got != 4 {
		t.Errorf("the governor's clearance after his own write = %d, want 4", got)
	}
}

// TestImpersonation_ownRecordActingAsAPerson proves the same on a session that acts as a
// person, and that the kind keeps the promise its role makes: a stand-in does what the
// person may do, except change how they sign in or who may do what. The cadet and the
// yeoman, given the grant to act as a person, act as the governor, who holds
// headquarters and is the marshal of every sector. The sequence the review of the mask
// gave and its variants are each refused, and nothing in the database changes: a higher
// clearance and fee limit on the cadet's own Pilot row, that row handed on or removed, a
// certification, a place in a squadron and a hangar pass of their own, a veteran's row
// moved onto the yeoman's account and a row made for it. The kind's mask names the
// records that decide what a person may do, so the same writes for another person are
// refused too: a pilot's clearance, by the record and by the action that rates a pilot;
// a certification given and taken; a place in a squadron given and taken; a hangar pass
// given to a person and to a squadron, changed and taken; a client's contact moved to
// another client; and a squadron changed, its wing or its name, because a squadron's row
// says which wing every member is in and a mask names a record's permission, not one
// plain field of it. What the mask does not name it keeps: a wing is added.
//
// Demonstrates: impersonation.never-the-subject, impersonation.act-as-user, impersonation.grant-mask, impersonation.mask.
func TestImpersonation_ownRecordActingAsAPerson(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	cadet, yeoman, pilot := string(account(t, "cadet")), string(account(t, "yeoman")), string(account(t, "pilot"))
	giveCrewRole(ctx, t, s, "StandIn", "cadet", "yeoman")
	asGovernor := actingBrowser(ctx, t, s, "cadet", actAs(t, "governor", "standing in for the governor", ""))
	yeomanAsGovernor := actingBrowser(ctx, t, s, "yeoman", actAs(t, "governor", "standing in for the governor", ""))

	const (
		passOfTongs    = "61000000-0000-4000-8000-000000000001" // Tongs' run of Anvil Dock One
		passOfHammer   = "61000000-0000-4000-8000-000000000004" // Hammer's run of the Quarantine Bay
		contactOfCleo  = "31000000-0000-4000-8000-000000000001" // Client Cleo, a contact of Halvard
		ownPilot       = "/pilots/" + pilotCassID
		hangarPassPath = "hangar-passes"
	)
	refused := []recordWrite{
		// The review's sequence and its variants: the real person's own standing.
		{name: "a higher clearance and fee limit on their own Pilot row", by: asGovernor, ops: []string{recordOp("patch", ownPilot, `{"clearance":5,"feeLimit":999999}`)}},
		{name: "their own Pilot row handed to someone else", by: asGovernor, ops: []string{recordOp("patch", ownPilot, fmt.Sprintf(`{"userId":%q}`, yeoman))}},
		{name: "their own Pilot row removed", by: asGovernor, ops: []string{recordOp("remove", ownPilot, "")}},
		{name: "a certification of their own", by: asGovernor, ops: []string{recordOp("add", "/pilot-certifications/"+cadet+"/hazmat", `{}`)}},
		{name: "a place in a squadron for themselves", by: asGovernor, ops: []string{recordOp("add", opPath(anvil, "squadron-memberships/"+squadronTongsID+"/"+cadet), `{}`)}},
		{
			name: "a hangar pass for themselves", by: asGovernor,
			ops: []string{recordOp("add", opPath(anvil, hangarPassPath), fmt.Sprintf(`{"hangarId":%q,"userId":%q,"levelId":"run"}`, hangarQuarantineID, cadet))},
		},
		{name: "a veteran's Pilot row moved onto their own account", by: yeomanAsGovernor, ops: []string{recordOp("patch", "/pilots/"+pilotVelaID, fmt.Sprintf(`{"userId":%q}`, yeoman))}},
		{
			name: "a Pilot row made for their own account", by: yeomanAsGovernor,
			ops: []string{recordOp("add", "/pilots", fmt.Sprintf(`{"userId":%q,"displayName":"Yeoman Yarrow","clearance":5,"feeLimit":999999}`, yeoman))},
		},
		// What the kind's mask names: another person's standing.
		{name: "another pilot's clearance", by: asGovernor, ops: []string{recordOp("patch", "/pilots/"+pilotPaxID, `{"clearance":4}`)}},
		{name: "another pilot's certification given", by: asGovernor, ops: []string{recordOp("add", "/pilot-certifications/"+pilot+"/hazmat", `{}`)}},
		{name: "another pilot's certification taken", by: asGovernor, ops: []string{recordOp("remove", "/pilot-certifications/"+pilot+"/salvage", "")}},
		{name: "another person's place in a squadron given", by: asGovernor, ops: []string{recordOp("add", opPath(anvil, "squadron-memberships/"+squadronTongsID+"/"+yeoman), `{}`)}},
		{name: "another person's place in a squadron taken", by: asGovernor, ops: []string{recordOp("remove", opPath(anvil, "squadron-memberships/"+squadronTongsID+"/"+pilot), "")}},
		{
			name: "a hangar pass given to another person", by: asGovernor,
			ops: []string{recordOp("add", opPath(anvil, hangarPassPath), fmt.Sprintf(`{"hangarId":%q,"userId":%q,"levelId":"run"}`, hangarQuarantineID, yeoman))},
		},
		{
			name: "a hangar pass given to a squadron", by: asGovernor,
			ops: []string{recordOp("add", opPath(anvil, hangarPassPath), fmt.Sprintf(`{"hangarId":%q,"squadronId":%q,"levelId":"visit"}`, hangarAnvilDockID, squadronHammerID))},
		},
		{name: "a squadron's hangar pass changed", by: asGovernor, ops: []string{recordOp("patch", opPath(anvil, hangarPassPath+"/"+passOfTongs), `{"levelId":"visit"}`)}},
		{name: "a squadron's hangar pass taken", by: asGovernor, ops: []string{recordOp("remove", opPath(anvil, hangarPassPath+"/"+passOfHammer), "")}},
		{name: "a client's contact moved to another client", by: asGovernor, ops: []string{recordOp("patch", "/client-contacts/"+contactOfCleo, fmt.Sprintf(`{"clientId":%q}`, clientMeridianID))}},
		{name: "a squadron's wing written", by: asGovernor, ops: []string{recordOp("patch", opPath(anvil, "squadrons/"+squadronHammerID), fmt.Sprintf(`{"wingId":%q}`, wingForgeID))}},
		{name: "a squadron renamed", by: asGovernor, ops: []string{recordOp("patch", opPath(anvil, "squadrons/"+squadronHammerID), `{"name":"Hammer Reforged"}`)}},
	}
	before := standing(ctx, t, s)
	for _, write := range refused {
		if status, answer := writeRecords(ctx, write.by, write.ops...); status != http.StatusForbidden {
			t.Errorf("acting as the governor, %s: status %d, want 403: %s", write.name, status, answer)
		}
		if after := standing(ctx, t, s); !slices.Equal(after, before) {
			t.Errorf("acting as the governor, %s: the write changed what a person may do: %s", write.name, standingChanges(before, after))
			before = after
		}
	}

	// The action that rates a pilot changes what a person may do as the record's write
	// does, and carries the class access: the kind refuses it whoever the pilot is.
	rating := fmt.Appendf(nil, `{"pilotId":%q,"clearance":3}`, pilotVelaID)
	if status, answer := asGovernor.do(ctx, http.MethodPost, sectorPath(anvil, "set-sector-pilot-clearance"), rating); status != http.StatusForbidden {
		t.Errorf("acting as the governor, rating another pilot: status %d, want 403: %s", status, answer)
	}
	if got := readColumn[int64](ctx, t, s.db, "Pilots", spanner.Key{pilotVelaID}, "Clearance"); got != 5 {
		t.Errorf("the veteran's clearance after the refused rating = %d, want the 5 the registry gave them", got)
	}

	// The mask refuses what it names and keeps the rest: the session that may not
	// change a squadron adds a wing, which no person's standing is read from.
	if status, answer := writeRecords(ctx, asGovernor, recordOp("add", opPath(anvil, "wings"), `{"name":"Reserve Wing"}`)); status != http.StatusOK {
		t.Errorf("acting as the governor, a wing added: status %d, want 200: the mask names no write of Wings: %s", status, answer)
	}

	// The sessions ended, the cadet's clearance is the one the registry gave them.
	for _, b := range []*browser{asGovernor, yeomanAsGovernor} {
		if status, answer := b.do(ctx, http.MethodPost, consoleAPI+"/impersonate/end", nil); status != http.StatusOK {
			t.Fatalf("ending a session that acts as a person: status %d, want 200: %s", status, answer)
		}
	}
	if got := readColumn[int64](ctx, t, s.db, "Pilots", spanner.Key{pilotCassID}, "Clearance"); got != 2 {
		t.Errorf("the cadet's clearance after the sessions = %d, want the 2 the registry gave them", got)
	}
}

// TestImpersonation_ownRecordInOnesOwnSession proves the rule says nothing where nobody
// acts as someone else. The governor holds headquarters and is the marshal of every
// sector, and holds both grants to act as someone else besides. In a session of his own
// he writes the records that decide what he himself may do, as he always could: his own
// clearance and fee limit, a certification of his own given and taken, and a place in a
// squadron given and taken.
//
// Demonstrates: impersonation.never-the-subject.
func TestImpersonation_ownRecordInOnesOwnSession(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	himself := string(account(t, "governor"))

	certification := "/pilot-certifications/" + himself + "/hazmat"
	membership := opPath(anvil, "squadron-memberships/"+squadronTongsID+"/"+himself)
	writes := []struct {
		name string
		op   string
		want string
		gone string
	}{
		{
			name: "his own clearance and fee limit", op: recordOp("patch", "/pilots/"+pilotGreerID, `{"clearance":4,"feeLimit":250000}`),
			want: "Pilots " + pilotGreerID + " user " + himself + " clearance 4 fee limit 250000",
		},
		{name: "a certification of his own", op: recordOp("add", certification, `{}`), want: "PilotCertifications " + himself + " hazmat"},
		{name: "his own certification taken", op: recordOp("remove", certification, ""), gone: "PilotCertifications " + himself + " hazmat"},
		{name: "a place in a squadron for himself", op: recordOp("add", membership, `{}`), want: "SquadronMemberships " + squadronTongsID + " " + himself},
		{name: "his own place in a squadron taken", op: recordOp("remove", membership, ""), gone: "SquadronMemberships " + squadronTongsID + " " + himself},
	}
	for _, write := range writes {
		if status, answer := writeRecords(ctx, governor, write.op); status != http.StatusOK {
			t.Errorf("the governor in a session of his own, %s: status %d, want 200: %s", write.name, status, answer)

			continue
		}
		rows := standing(ctx, t, s)
		if write.want != "" && !slices.Contains(rows, write.want) {
			t.Errorf("the governor in a session of his own, %s: the database does not hold %q", write.name, write.want)
		}
		if write.gone != "" && slices.Contains(rows, write.gone) {
			t.Errorf("the governor in a session of his own, %s: the database still holds %q", write.name, write.gone)
		}
	}
}
