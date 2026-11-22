//go:build skipAuth

package integration

// impersonation_record_skipauth_test: a record that decides what a person may do is
// never the real person's own to write while they act as someone else, proven on the
// served stack over Spanner. A Pilot row carries the clearance and the fee limit the
// policy reads as subject.clearance and subject.feeLimit; a PilotCertifications row, a
// SquadronMemberships row and a HangarPasses row each put a value in one of the person's
// subject sets. The generated write of each asks the identity library's rule
// (identity.RefuseImpersonatorAsRowSubject) about the person the row names, before the
// write and after it. A row that reaches a person without naming them is held to the
// same rule: a hangar pass given to a squadron the real person is in
// (identity.RefuseImpersonatorAsSetSubject), and their own squadron moved to another
// wing (identity.RefuseImpersonatorAsReadThroughSubject). The kind that acts as a person
// refuses those records' writes by their class as well, whoever the row names, and keeps
// one record out of that refusal, the hangar passes: a stand-in gives other people and
// other squadrons a pass, and never themselves or a squadron they are in. The delete of
// a sector carries the same class, since a sector that goes takes every role held in it.
//
// The rule reads the columns that name a person a row is about. A column that records
// who made a row is not one of them, and the last test here pins what such a column
// holds for a row made in a session that acts as a role, and what follows from it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/access"
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
		`SELECT CONCAT('HangarPasses ', Id, ' hangar ', HangarId, ' user ', IFNULL(UserId, 'none'), ' squadron ', IFNULL(SquadronId, 'none'), ' group ', IFNULL(GroupId, 'none'), ' level ', LevelId) FROM HangarPasses`,
		`SELECT CONCAT('ClientContacts ', Id, ' user ', UserId, ' client ', ClientId) FROM ClientContacts`,
		`SELECT CONCAT('Squadrons ', Id, ' wing ', WingId) FROM Squadrons`,
	} {
		iter := s.db.Single().Query(ctx, spanner.Statement{SQL: sql})
		for {
			row, err := iter.Next()
			if errors.Is(err, iterator.Done) {
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
// own Pilot row (what a person who acts as an administrator would otherwise keep after
// the session ends), that row handed to someone else or removed, a certification, a place in a squadron and a hangar pass of their
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
		if want := actingAsSomeoneElse + write.says + "."; status != http.StatusForbidden || !strings.Contains(answer, want) {
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
	if status, answer := writeRecords(ctx, governor, raise); status != http.StatusForbidden || !strings.Contains(answer, actingAsSomeoneElse+"change your own pilot record.") {
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
// headquarters and is the marshal of every sector. Each write that would raise the real
// person's own standing is refused, and nothing in the database changes: a higher
// clearance and fee limit on the cadet's own Pilot row, that row handed on or removed, a
// certification, a place in a squadron and a hangar pass of their own, a veteran's row
// moved onto the yeoman's account and a row made for it. The kind's mask refuses the
// class access and names no record to refuse: the generator gives that class to the
// writes of every record that decides what a person may do, so the same writes for
// another person are refused too: a pilot's clearance, by the record and by the action
// that rates a pilot; a certification given and taken; a place in a squadron given and
// taken; a client's contact moved to another client; and a squadron changed, its wing
// or its name, because a squadron's row says which wing every member is in and the
// class is on the record's change, not on one plain field of it. A write that carries
// no class the mask keeps: a wing is added. The hangar passes are the one record the
// kind keeps out of the class, and TestImpersonation_ownSetActingAsAPerson has them.
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
		contactOfCleo  = "31000000-0000-4000-8000-000000000001" // Client Cleo, a contact of Halvard
		ownPilot       = "/pilots/" + pilotCassID
		hangarPassPath = "hangar-passes"
	)
	refused := []recordWrite{
		// The real person's own standing, raised, handed on or removed.
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
		// What the class access covers: another person's standing.
		{name: "another pilot's clearance", by: asGovernor, ops: []string{recordOp("patch", "/pilots/"+pilotPaxID, `{"clearance":4}`)}},
		{name: "another pilot's certification given", by: asGovernor, ops: []string{recordOp("add", "/pilot-certifications/"+pilot+"/hazmat", `{}`)}},
		{name: "another pilot's certification taken", by: asGovernor, ops: []string{recordOp("remove", "/pilot-certifications/"+pilot+"/salvage", "")}},
		{name: "another person's place in a squadron given", by: asGovernor, ops: []string{recordOp("add", opPath(anvil, "squadron-memberships/"+squadronTongsID+"/"+yeoman), `{}`)}},
		{name: "another person's place in a squadron taken", by: asGovernor, ops: []string{recordOp("remove", opPath(anvil, "squadron-memberships/"+squadronTongsID+"/"+pilot), "")}},
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

	// The mask refuses the class and keeps the rest: the session that may not change a
	// squadron adds a wing, which no person's standing is read from.
	if status, answer := writeRecords(ctx, asGovernor, recordOp("add", opPath(anvil, "wings"), `{"name":"Reserve Wing"}`)); status != http.StatusOK {
		t.Errorf("acting as the governor, a wing added: status %d, want 200: no write of Wings carries a class: %s", status, answer)
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

// TestImpersonation_sectorDeletedActingAsAPerson proves a session that acts as a person
// under the kind that refuses the class access cannot delete a sector. A sector that
// goes takes with it every role held in it, its groups, the custom roles made in it and
// the invitations into it, and each of those done alone is an access action; the
// generator gives the delete of the tenant record that class, so the kind refuses it
// whatever the person acted as may do. No role the release ships deletes a sector, so
// the suite makes one that does and gives it to the governor, as the suite of a deleted
// sector does. The cadet, standing in for the governor, is refused the delete of a
// sector founded for the test: the sector stands, and so does every role held in the
// crew. The governor, in a session of his own, then deletes it: the grant was there all
// along, and the class is what refused.
//
// Demonstrates: impersonation.mask, impersonation.act-as-user, tenancy.deleted-tenant.
func TestImpersonation_sectorDeletedActingAsAPerson(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")

	const (
		closer          accesstypes.Role = "SectorCloser"
		sectorsResource                  = accesstypes.Resource("Sectors")
	)
	manager, global := s.access.UserManager(), accesstypes.GlobalPolicyScope()
	if err := manager.AddRole(ctx, global, closer, "Closes a sector the fleet has left."); err != nil {
		t.Fatalf("access.UserManager.AddRole(%s) error = %v", closer, err)
	}
	if err := manager.AddRoleGrants(ctx, global, closer, access.GrantRow{Permission: accesstypes.Delete, Resource: sectorsResource}); err != nil {
		t.Fatalf("access.UserManager.AddRoleGrants(%s) error = %v", closer, err)
	}
	governor := account(t, "governor")
	if err := manager.AddUserRoles(ctx, global, governor, closer); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(governor, %s) error = %v", closer, err)
	}
	if d := decisionFor(ctx, t, s.access.ForUser(governor), accesstypes.GlobalScope(), accesstypes.Delete, sectorsResource, time.Now().Add(15*time.Second)); !d.IsGranted() {
		t.Fatalf("Delete on Sectors for the governor holding %s = %s, want granted before the sector is made", closer, d)
	}
	forge := foundSector(ctx, t, s, hq, "Forge", "Far frontier", "2226-11-20", "forge-founder")

	giveCrewRole(ctx, t, s, "StandIn", "cadet")
	asGovernor := actingBrowser(ctx, t, s, "cadet", actAs(t, "governor", "standing in for the governor", ""))
	remove := recordOp("remove", "/sectors/"+forge, "")
	sectors, roles := tableRows(ctx, t, s, "Sectors"), tableRows(ctx, t, s, "CrewUserRoles")
	if status, answer := writeRecords(ctx, asGovernor, remove); status != http.StatusForbidden {
		t.Errorf("acting as the governor, a sector deleted: status %d, want 403: the delete of a sector carries the class access: %s", status, answer)
	}
	if got := tableRows(ctx, t, s, "Sectors"); got != sectors {
		t.Errorf("Sectors holds %d row(s) after the refused delete, want the %d it held", got, sectors)
	}
	if got := tableRows(ctx, t, s, "CrewUserRoles"); got != roles {
		t.Errorf("CrewUserRoles holds %d row(s) after the refused delete, want the %d it held: nobody's role went", got, roles)
	}
	if status, answer := asGovernor.do(ctx, http.MethodPost, consoleAPI+"/impersonate/end", nil); status != http.StatusOK {
		t.Fatalf("ending the session that acts as the governor: status %d, want 200: %s", status, answer)
	}

	// The governor himself holds the grant, and his own session carries no mask.
	if status, answer := writeRecords(ctx, hq, remove); status != http.StatusOK {
		t.Fatalf("the governor deleting the sector in his own session: status %d, want 200: the grant is his: %s", status, answer)
	}
	if got := tableRows(ctx, t, s, "Sectors"); got != sectors-1 {
		t.Errorf("Sectors holds %d row(s) after the governor's delete, want %d: the sector is gone", got, sectors-1)
	}
}

// The two hangar passes of the seed the tests of a set write: one given to Tongs and one
// given to Hammer.
const (
	passOfTongs  = "61000000-0000-4000-8000-000000000001" // Tongs' run of Anvil Dock One
	passOfHammer = "61000000-0000-4000-8000-000000000004" // Hammer's run of the Quarantine Bay
)

// wingNamed reads the id of the wing with the name, straight from the database.
func wingNamed(ctx context.Context, t *testing.T, s *served, name string) string {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{SQL: `SELECT Id FROM Wings WHERE Name = @name`, Params: map[string]any{"name": name}})
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		t.Fatalf("reading the wing named %q: %v", name, err)
	}
	var id string
	if err := row.Columns(&id); err != nil {
		t.Fatalf("reading the id of the wing named %q: %v", name, err)
	}

	return id
}

// oneID reads the id the statement selects, straight from the database: what names the
// row a test goes on to write, where the seed or a method gave it an id of its own.
func oneID(ctx context.Context, t *testing.T, s *served, what, sql string, params map[string]any) string {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{SQL: sql, Params: params})
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		t.Fatalf("reading the id of %s: %v", what, err)
	}
	var id string
	if err := row.Columns(&id); err != nil {
		t.Fatalf("reading the id of %s: %v", what, err)
	}

	return id
}

// TestImpersonation_ownSetActingAsAPerson proves the rule for a row that reaches a person
// without naming them, and the exception the act kind keeps. The pilot flies with Tongs
// and is in one of Anvil's groups, the Deck runners, which the governor makes and gives
// a pass before the session starts.
// Given the grant to act as a person, the pilot acts as the marshal, whose daily work a
// hangar pass is: the kind refuses the class access and keeps the writes of HangarPasses
// out of it. A pass given to another person and to another squadron goes through, and so
// do a change and a removal of another squadron's pass. A pass given to the pilot's own
// account is refused by the rule for a row that names a person, and a pass given to
// Tongs, changed for Tongs or taken from Tongs by the rule for a set the real person is
// in, each with its sentence, alone or last in a batch, and nothing in the database
// changes. A pass that names a group is held the same way: given to the Deck runners,
// changed for them or taken from them it is refused with the sentence for a group, alone
// or last in a batch, and the same four writes for the Bay keepers, a group of Anvil's
// the pilot is not in, go through.
//
// A request narrows the kind and never widens it. One that refuses the class access
// again and names nothing to keep drops the exception: the session it starts is refused
// a pass for anyone, by the mask. One that names a record the kind does not keep is
// refused with nothing started.
//
// The kind refuses every change of a squadron, so the rule for the row a person's
// standing is read through shows where no mask stands in front of it: the pilot, given
// the grant to act as a role, acts as the Sector Marshal. The session's record names the
// pilot as the person who started it. Tongs moved to another wing is refused with the
// rule's sentence; Tongs renamed is not asked about; Hammer, which the pilot does not
// fly with, moves. The rule for a set holds there too: a pass for Tongs is refused under
// the role, whose mask refuses nothing.
//
// Demonstrates: impersonation.never-the-subject, impersonation.act-as-user, impersonation.act-as-role, impersonation.grant-mask, impersonation.mask.
func TestImpersonation_ownSetActingAsAPerson(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	pilot, yeoman := string(account(t, "pilot")), string(account(t, "yeoman"))
	giveCrewRole(ctx, t, s, "StandIn", "pilot")
	giveCrewRole(ctx, t, s, "CommandStaff", "pilot")

	passes := opPath(anvil, "hangar-passes")
	pass := func(holder, id, level string) string {
		return recordOp("add", passes, fmt.Sprintf(`{"hangarId":%q,%q:%q,"levelId":%q}`, hangarQuarantineID, holder, id, level))
	}

	// The pilot's own group: the governor, in a session of his own, makes a group of
	// Anvil's, puts the pilot in it and gives it a pass to visit the Quarantine Bay. The
	// Bay keepers are a group of the seed's the pilot is not in, with a pass of their own
	// to run the same bay.
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	made, group := governor.do(ctx, http.MethodPost, sectorPath(anvil, "create-crew-sector-group"), []byte(`{"name":"Deck runners","description":"The people who run Anvil's dock."}`))
	if made != http.StatusOK {
		t.Fatalf("the governor making a group of Anvil's: status %d, want 200: %s", made, group)
	}
	deckRunners := cell[string](t, decodeRow(t, group), "id")
	if status, answer := governor.do(ctx, http.MethodPost, crewMethod("add-crew-group-member"), fmt.Appendf(nil, `{"groupId":%q,"userId":%q}`, deckRunners, pilot)); status != http.StatusNoContent {
		t.Fatalf("the governor putting the pilot in the group: status %d, want 204: %s", status, answer)
	}
	if status, answer := writeRecords(ctx, governor, pass("groupId", deckRunners, "visit")); status != http.StatusOK {
		t.Fatalf("the governor giving the pilot's group a pass: status %d, want 200: %s", status, answer)
	}
	passOfDeckRunners := oneID(ctx, t, s, "the pass of the pilot's group", `SELECT Id FROM HangarPasses WHERE GroupId = @group`, map[string]any{"group": deckRunners})
	bayKeepers := oneID(ctx, t, s, "the Bay keepers, a group of Anvil's",
		`SELECT Id FROM CrewGroups WHERE Name = 'Bay keepers' AND Kind = 'domain' AND DomainId = @sector`, map[string]any{"sector": anvil})

	asMarshal := actingBrowser(ctx, t, s, "pilot", actAs(t, "marshal", "standing in for the marshal", ""))

	// What the exception keeps: a pass for another person, for a squadron the pilot does
	// not fly with and for a group the pilot is not in, given, changed and taken, and a
	// batch of two such passes.
	ofBayKeepers := " hangar " + hangarQuarantineID + " user none squadron none group " + bayKeepers
	kept := []struct {
		name string
		ops  []string
		want string
		gone string
	}{
		{name: "a pass given to another person", ops: []string{pass("userId", yeoman, "run")}, want: " hangar " + hangarQuarantineID + " user " + yeoman + " squadron none group none level run"},
		{
			name: "a pass given to another squadron", ops: []string{pass("squadronId", squadronHammerID, "visit")},
			want: " hangar " + hangarQuarantineID + " user none squadron " + squadronHammerID + " group none level visit",
		},
		{
			name: "another squadron's pass changed", ops: []string{recordOp("patch", passes+"/"+passOfHammer, `{"levelId":"visit"}`)},
			want: "HangarPasses " + passOfHammer + " hangar " + hangarQuarantineID + " user none squadron " + squadronHammerID + " group none level visit",
		},
		{name: "another squadron's pass taken", ops: []string{recordOp("remove", passes+"/"+passOfHammer, "")}, gone: "HangarPasses " + passOfHammer + " "},
		{name: "a pass given to a group they are not in", ops: []string{pass("groupId", bayKeepers, "visit")}, want: ofBayKeepers + " level visit"},
		{
			name: "the pass of a group they are not in changed", ops: []string{recordOp("patch", passes+"/"+passBayKeepersQuarantineID, `{"levelId":"visit"}`)},
			want: "HangarPasses " + passBayKeepersQuarantineID + ofBayKeepers + " level visit",
		},
		{
			name: "the pass of a group they are not in taken", ops: []string{recordOp("remove", passes+"/"+passBayKeepersQuarantineID, "")},
			gone: "HangarPasses " + passBayKeepersQuarantineID + " ",
		},
		{
			name: "a batch that gives another person a pass and then a group they are not in", ops: []string{pass("userId", yeoman, "visit"), pass("groupId", bayKeepers, "run")},
			want: ofBayKeepers + " level run",
		},
	}
	holds := func(rows []string, part string) bool {
		return slices.ContainsFunc(rows, func(row string) bool {
			return strings.HasPrefix(row, "HangarPasses ") && strings.Contains(row, part)
		})
	}
	for _, write := range kept {
		if status, answer := writeRecords(ctx, asMarshal, write.ops...); status != http.StatusOK {
			t.Errorf("acting as the marshal, %s: status %d, want 200: the kind keeps the writes of HangarPasses: %s", write.name, status, answer)

			continue
		}
		rows := standing(ctx, t, s)
		if write.want != "" && !holds(rows, write.want) {
			t.Errorf("acting as the marshal, %s: the database holds no pass reading %q", write.name, write.want)
		}
		if write.gone != "" && holds(rows, write.gone) {
			t.Errorf("acting as the marshal, %s: the database still holds the pass %q", write.name, write.gone)
		}
	}

	// What the rule refuses whatever the mask keeps: a pass that reaches the real person,
	// by their account, through the squadron they fly with or through a group they are
	// in.
	refused := []recordWrite{
		{name: "a pass given to their own account", by: asMarshal, says: "add your own hangar pass record", ops: []string{pass("userId", pilot, "run")}},
		{name: "a pass given to their own squadron", by: asMarshal, says: "add a hangar pass record for one of your own squadrons", ops: []string{pass("squadronId", squadronTongsID, "run")}},
		{
			name: "their own squadron's pass changed", by: asMarshal, says: "change a hangar pass record of one of your own squadrons",
			ops: []string{recordOp("patch", passes+"/"+passOfTongs, `{"levelId":"visit"}`)},
		},
		{
			name: "their own squadron's pass taken", by: asMarshal, says: "remove a hangar pass record of one of your own squadrons",
			ops: []string{recordOp("remove", passes+"/"+passOfTongs, "")},
		},
		{
			name: "a batch that gives another squadron a pass and then their own", by: asMarshal, says: "add a hangar pass record for one of your own squadrons",
			ops: []string{pass("squadronId", squadronHammerID, "run"), pass("squadronId", squadronTongsID, "run")},
		},
		{name: "a pass given to their own group", by: asMarshal, says: "add a hangar pass record for one of your own crew groups", ops: []string{pass("groupId", deckRunners, "run")}},
		{
			name: "their own group's pass changed", by: asMarshal, says: "change a hangar pass record of one of your own crew groups",
			ops: []string{recordOp("patch", passes+"/"+passOfDeckRunners, `{"levelId":"run"}`)},
		},
		{
			name: "their own group's pass taken", by: asMarshal, says: "remove a hangar pass record of one of your own crew groups",
			ops: []string{recordOp("remove", passes+"/"+passOfDeckRunners, "")},
		},
		{
			name: "a batch that gives a group they are not in a pass and then their own", by: asMarshal, says: "add a hangar pass record for one of your own crew groups",
			ops: []string{pass("groupId", bayKeepers, "visit"), pass("groupId", deckRunners, "run")},
		},
	}
	before := standing(ctx, t, s)
	for _, write := range refused {
		status, answer := writeRecords(ctx, write.by, write.ops...)
		if want := actingAsSomeoneElse + write.says + "."; status != http.StatusForbidden || !strings.Contains(answer, want) {
			t.Errorf("acting as the marshal, %s: status %d, want 403 saying %q: %s", write.name, status, want, answer)
		}
		if after := standing(ctx, t, s); !slices.Equal(after, before) {
			t.Errorf("acting as the marshal, %s: the write changed what a person may do: %s", write.name, standingChanges(before, after))
			before = after
		}
	}

	// A request that refuses the class again and names nothing to keep drops the
	// exception: the mask refuses a pass for anyone, and the answer is the mask's, not
	// the rule's.
	const withoutPasses = "everything except access actions and credential actions"
	narrowed := newBrowser(t, s, consoleAPI)
	narrowed.signIn(ctx, "pilot")
	if got := mint(ctx, t, narrowed, actAs(t, "marshal", "standing in, with no passes", `,"refuseClasses":["access"]`)); got.Mask != withoutPasses {
		t.Errorf("a request that refuses the class access and keeps nothing = %q, want %q", got.Mask, withoutPasses)
	}
	if status, answer := writeRecords(ctx, narrowed, pass("userId", yeoman, "visit")); status != http.StatusForbidden || strings.Contains(answer, actingAsSomeoneElse) {
		t.Errorf("a session that dropped the exception, a pass given to another person: status %d, want 403 from the mask: %s", status, answer)
	}
	if after := standing(ctx, t, s); !slices.Equal(after, before) {
		t.Errorf("a session that dropped the exception wrote a pass: %s", standingChanges(before, after))
	}

	// A request that names a record the kind does not keep is refused, never trimmed,
	// and the browser's session stays the pilot's own.
	widened := newBrowser(t, s, consoleAPI)
	widened.signIn(ctx, "pilot")
	if status, answer := widened.impersonate(ctx, actAs(t, "marshal", "standing in, with the registry", `,"refuseClasses":["access"],"exceptWrites":["Pilots"]`)); status != http.StatusForbidden {
		t.Errorf("a request that keeps the writes of Pilots out of the class access: status %d, want 403: %s", status, answer)
	}
	if info := widened.session(ctx); info["username"] != "pilot" || info["impersonation"] != nil {
		t.Errorf("the session after the refused request = %v, want the pilot's own", info)
	}

	// The rule for the row a person's standing is read through, where no mask stands in
	// front of it: the pilot acts as the Sector Marshal, a role.
	asRole := actingBrowser(ctx, t, s, "pilot", `{"kind":"role","principal":"SectorMarshal","reason":"covering the marshal's desk"}`)
	if record, _ := asRole.session(ctx)["impersonation"].(map[string]any); record["actor"] != "pilot" {
		t.Fatalf("the record of the session that acts as the Sector Marshal = %v, want the actor pilot", record)
	}
	if status, answer := writeRecords(ctx, asRole, recordOp("add", opPath(anvil, "wings"), `{"name":"Reserve Wing"}`)); status != http.StatusOK {
		t.Fatalf("acting as the Sector Marshal, a wing added: status %d, want 200: %s", status, answer)
	}
	reserve := wingNamed(ctx, t, s, "Reserve Wing")
	move := func(squadron string) string {
		return recordOp("patch", opPath(anvil, "squadrons/"+squadron), fmt.Sprintf(`{"wingId":%q}`, reserve))
	}
	before = standing(ctx, t, s)
	status, answer := writeRecords(ctx, asRole, move(squadronTongsID))
	if want := actingAsSomeoneElse + "change the wing id of the squadron record that decides your own wings."; status != http.StatusForbidden || !strings.Contains(answer, want) {
		t.Errorf("acting as the Sector Marshal, their own squadron moved to another wing: status %d, want 403 saying %q: %s", status, want, answer)
	}
	status, answer = writeRecords(ctx, asRole, pass("squadronId", squadronTongsID, "run"))
	if want := actingAsSomeoneElse + "add a hangar pass record for one of your own squadrons."; status != http.StatusForbidden || !strings.Contains(answer, want) {
		t.Errorf("acting as the Sector Marshal, a pass given to their own squadron: status %d, want 403 saying %q: %s", status, want, answer)
	}
	if after := standing(ctx, t, s); !slices.Equal(after, before) {
		t.Errorf("acting as the Sector Marshal, the refused writes changed what a person may do: %s", standingChanges(before, after))
	}
	if status, answer := writeRecords(ctx, asRole, recordOp("patch", opPath(anvil, "squadrons/"+squadronTongsID), `{"name":"Tongs Reforged"}`)); status != http.StatusOK {
		t.Errorf("acting as the Sector Marshal, their own squadron renamed: status %d, want 200: a rename moves nobody's standing, so the rule is not asked: %s", status, answer)
	}
	if got := readColumn[string](ctx, t, s.db, "Squadrons", spanner.Key{squadronTongsID}, "Name"); got != "Tongs Reforged" {
		t.Errorf("the name of the pilot's squadron after the rename = %q, want Tongs Reforged", got)
	}
	if status, answer := writeRecords(ctx, asRole, move(squadronHammerID)); status != http.StatusOK {
		t.Errorf("acting as the Sector Marshal, a squadron they do not fly with moved to another wing: status %d, want 200: %s", status, answer)
	}
	if rows := standing(ctx, t, s); !slices.Contains(rows, "Squadrons "+squadronHammerID+" wing "+reserve) || !slices.Contains(rows, "Squadrons "+squadronTongsID+" wing "+wingForgeID) {
		t.Errorf("after the two moves the squadrons read %v, want Hammer in the new wing and Tongs where it was", rows)
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

// bookedMission reads the id of the mission with the title and the account its booker
// column names, straight from the database.
func bookedMission(ctx context.Context, t *testing.T, s *served, title string) (id, booker string) {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{
		SQL:    `SELECT Id, BookedByUserId FROM Missions WHERE Title = @title`,
		Params: map[string]any{"title": title},
	})
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		t.Fatalf("reading the mission titled %q: %v", title, err)
	}
	if err := row.Columns(&id, &booker); err != nil {
		t.Fatalf("reading the columns of the mission titled %q: %v", title, err)
	}

	return id, booker
}

// TestImpersonation_authorUnderARole pins what the application does today with a row
// made in a session that acts as a role, so that a change of mind is seen here. A role
// is not anyone, so the column that records who made a row holds the person who started
// the session, and a grant that compares that column to the requester (bookedBy =
// subject) is theirs once the session has ended: an author's rights over a row that was
// made with the role's powers.
//
// The flight lead, whose own role books no mission, books one acting as the Sector
// Marshal. It is stamped with the flight lead's account. The session ended, the flight
// lead reads it and finds it in the list, by the Flight Lead's grant for a mission the
// requester booked; the mission the marshal booked with the same values in her own
// session is not found. The booking agent, whose fee limit is 25000, books a mission of
// 90000 acting as the Sector Marshal and, the session ended, stands it down by the
// Booking Agent's grant for a mission the requester booked; the marshal's is refused.
//
// An application that does not want this refuses the record's create under the role
// kind's mask, or stamps the column with nobody in a session that acts as a role.
//
// Demonstrates: impersonation.act-as-role.
func TestImpersonation_authorUnderARole(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	lead, booking, marshal := string(account(t, "lead")), string(account(t, "booking")), string(account(t, "marshal"))
	giveCrewRole(ctx, t, s, "CommandStaff", "lead", "booking")

	const (
		marshalRole  = `{"kind":"role","principal":"SectorMarshal","reason":"covering the marshal's desk"}`
		byTheLead    = "Booked by the flight lead at the marshal's desk"
		byTheAgent   = "Booked by the booking agent at the marshal's desk"
		byTheMarshal = "Booked by the marshal herself"
		alone        = "Booked with a person's own role"
	)
	book := func(title string, fee int) string {
		value := fmt.Sprintf(`{"clientId":%q,"kindId":"courier","title":%q,"hazard":1,"fee":%d,"deadline":%q}`, clientHalvardID, title, fee, deadline(365))

		return recordOp("add", opPath(anvil, "missions"), value)
	}

	// In a session of their own neither books these: the Flight Lead's role books no
	// mission, and the Booking Agent's books none over the agent's fee limit.
	ownLead, ownAgent := newBrowser(t, s, consoleAPI), newBrowser(t, s, consoleAPI)
	ownLead.signIn(ctx, "lead")
	ownAgent.signIn(ctx, "booking")
	if status, answer := writeRecords(ctx, ownLead, book(alone, 100)); status != http.StatusForbidden {
		t.Fatalf("the flight lead booking a mission in their own session: status %d, want 403: %s", status, answer)
	}
	if status, answer := writeRecords(ctx, ownAgent, book(alone, 90000)); status != http.StatusForbidden {
		t.Fatalf("the booking agent booking a mission over their fee limit in their own session: status %d, want 403: %s", status, answer)
	}

	// Acting as the Sector Marshal each books one, and the marshal books one in her own
	// session with the values of the flight lead's.
	leadAsMarshal := actingBrowser(ctx, t, s, "lead", marshalRole)
	agentAsMarshal := actingBrowser(ctx, t, s, "booking", marshalRole)
	herself := newBrowser(t, s, consoleAPI)
	herself.signIn(ctx, "marshal")
	for _, booked := range []struct {
		name string
		by   *browser
		op   string
	}{
		{name: "the flight lead acting as the Sector Marshal", by: leadAsMarshal, op: book(byTheLead, 100)},
		{name: "the booking agent acting as the Sector Marshal", by: agentAsMarshal, op: book(byTheAgent, 90000)},
		{name: "the marshal in her own session", by: herself, op: book(byTheMarshal, 100)},
	} {
		if status, answer := writeRecords(ctx, booked.by, booked.op); status != http.StatusOK {
			t.Fatalf("%s booking a mission: status %d, want 200: %s", booked.name, status, answer)
		}
	}

	// The stamp is the person who started the session, never the role.
	leadsMission, leadsBooker := bookedMission(ctx, t, s, byTheLead)
	agentsMission, agentsBooker := bookedMission(ctx, t, s, byTheAgent)
	marshalsMission, marshalsBooker := bookedMission(ctx, t, s, byTheMarshal)
	if leadsBooker != lead {
		t.Errorf("the mission the flight lead booked acting as a role names %q as its booker, want the flight lead's account %q", leadsBooker, lead)
	}
	if agentsBooker != booking {
		t.Errorf("the mission the booking agent booked acting as a role names %q as its booker, want the booking agent's account %q", agentsBooker, booking)
	}
	if marshalsBooker != marshal {
		t.Errorf("the mission the marshal booked names %q as its booker, want the marshal's account %q", marshalsBooker, marshal)
	}

	for _, b := range []*browser{leadAsMarshal, agentAsMarshal} {
		if status, answer := b.do(ctx, http.MethodPost, consoleAPI+"/impersonate/end", nil); status != http.StatusOK {
			t.Fatalf("ending a session that acts as a role: status %d, want 200: %s", status, answer)
		}
	}

	// The sessions ended, each person holds an author's rights over the row the role's
	// powers made, and none over the marshal's.
	if status, answer := leadAsMarshal.do(ctx, http.MethodGet, sectorPath(anvil, "missions/"+leadsMission), nil); status != http.StatusOK {
		t.Errorf("the flight lead, in their own session again, reading the mission they booked acting as a role: status %d, want 200: %s", status, answer)
	}
	if status, answer := leadAsMarshal.do(ctx, http.MethodGet, sectorPath(anvil, "missions/"+marshalsMission), nil); status != http.StatusNotFound {
		t.Errorf("the flight lead reading the mission the marshal booked: status %d, want 404: %s", status, answer)
	}
	status, listed := leadAsMarshal.do(ctx, http.MethodGet, sectorPath(anvil, "missions?columns=id&limit=200"), nil)
	if status != http.StatusOK {
		t.Fatalf("the flight lead listing missions: status %d, want 200: %s", status, listed)
	}
	if list := string(listed); !strings.Contains(list, leadsMission) || strings.Contains(list, marshalsMission) {
		t.Errorf("the flight lead's list of missions: want the one they booked acting as a role (%s) and not the marshal's (%s): %s", leadsMission, marshalsMission, list)
	}

	standDown := func(mission string) []byte {
		return fmt.Appendf(nil, `{"missionId":%q}`, mission)
	}
	if status, answer := agentAsMarshal.do(ctx, http.MethodPost, sectorPath(anvil, "stand-down-mission"), standDown(marshalsMission)); status != http.StatusForbidden {
		t.Errorf("the booking agent standing down the mission the marshal booked: status %d, want 403: %s", status, answer)
	}
	if status, answer := agentAsMarshal.do(ctx, http.MethodPost, sectorPath(anvil, "stand-down-mission"), standDown(agentsMission)); status != http.StatusOK {
		t.Errorf("the booking agent, in their own session again, standing down the mission they booked acting as a role: status %d, want 200: %s", status, answer)
	}
	if got := readColumn[string](ctx, t, s.db, "Missions", spanner.Key{agentsMission}, "StatusId"); got != "stood_down" {
		t.Errorf("the mission the booking agent booked acting as a role is %q after they stood it down, want stood_down", got)
	}
	if got := readColumn[string](ctx, t, s.db, "Missions", spanner.Key{marshalsMission}, "StatusId"); got != "open" {
		t.Errorf("the mission the marshal booked is %q, want open: nobody but its booker stood it down", got)
	}
}
