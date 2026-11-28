//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// organizationAccountSentence is what a password is told at an invitation that is
// accepted through an organization's directory alone.
const organizationAccountSentence = "This invitation is accepted with your organization's account, not with a password or a personal account. Sign in with your organization's account (Google Workspace or Microsoft Entra)."

// inviteSector sends the invitation that makes a sector, as the browser's person, and
// answers the status, the body and, when it was sent, the invitation.
func inviteSector(ctx context.Context, t *testing.T, b *browser, value map[string]any) (status int, body []byte, answer invited) {
	t.Helper()

	status, body = b.do(ctx, http.MethodPost, crewMethod("invite-crew-sector"), jsonBody(t, value))
	if status == http.StatusOK {
		if err := json.Unmarshal(body, &answer); err != nil || answer.ID == "" || !answer.Sent {
			t.Fatalf("invite-crew-sector answered %s (%v), want the invitation's id and the word that its message went", body, err)
		}
	}

	return status, body, answer
}

// sectorNamed answers the sector's row as the browser's person lists it, nil when no
// sector has the name.
func sectorNamed(ctx context.Context, t *testing.T, b *browser, name string) map[string]any {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, consoleAPI+"/sectors?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "name") == name {
			return row
		}
	}

	return nil
}

// invitedSectorRow answers the standing invitation's row of the list, nil when the list
// does not hold it.
func invitedSectorRow(ctx context.Context, t *testing.T, b *browser, id string) map[string]any {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, invitedSectorsPath+"?limit=200", nil)
	assertStatus(t, status, http.StatusOK, body)
	for _, row := range decodeRows(t, body) {
		if cell[string](t, row, "id") == id {
			return row
		}
	}

	return nil
}

// openInvitation follows a mailed link as a person not signed in does: the browser
// takes its XSRF token, then shows the invitation page what the link is.
func openInvitation(ctx context.Context, t *testing.T, b *browser, token string) map[string]any {
	t.Helper()

	if status, body := b.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the invitation: status %d: %s", status, body)
	}
	status, body := b.do(ctx, http.MethodPost, consoleAPI+"/user/invitation", jsonBody(t, map[string]string{"token": token}))
	if status != http.StatusOK {
		t.Fatalf("opening the invitation: status %d: %s", status, body)
	}
	var page map[string]any
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("the invitation page's answer %s: %v", body, err)
	}

	return page
}

// foundSector makes a sector the way one comes to exist, for a suite that needs a sector
// of its own: Headquarters sends the invitation with the sector's details, the invited
// person follows the mailed link, signs up with a password and completes the second
// factor. It answers the new sector's id; its founder is its first marshal.
func foundSector(ctx context.Context, t *testing.T, s *served, hq *browser, name, region, established, username string) string {
	t.Helper()

	email := username + "@" + strings.ToLower(name) + ".example"
	status, body, _ := inviteSector(ctx, t, hq, map[string]any{"email": email, "sectorName": name, "sectorRegion": region, "sectorEstablished": established})
	assertStatus(t, status, http.StatusOK, body)
	_, token := lastMailTo(t, s, email, "invitation")
	founder := newBrowser(t, s, consoleAPI)
	openInvitation(ctx, t, founder, token)
	if status, body := founder.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": username, "password": signUpPassword})); status != http.StatusOK {
		t.Fatalf("signing up at the invitation that makes %s: status %d: %s", name, status, body)
	}
	founder.secondFactor(ctx, username)
	made := sectorNamed(ctx, t, hq, name)
	if made == nil {
		t.Fatalf("no sector %s after its invitation was accepted", name)
	}

	return cell[string](t, made, "id")
}

// mailLinksTo counts the crew's mail links written for an address, of every kind.
func mailLinksTo(ctx context.Context, t *testing.T, s *served, email string) int64 {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{SQL: "SELECT COUNT(*) FROM CrewSessionMailLinks WHERE Email = @email", Params: map[string]any{"email": email}})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the mail links to %s: %v", email, err)
	}
	var count int64
	if err := row.Columns(&count); err != nil {
		t.Fatalf("counting the mail links to %s: %v", email, err)
	}

	return count
}

// checkMadeSectorRows holds the rows of a sector's making, in the whole application's
// audit log as Headquarters reads it, to the sector's name: each row in the sector is in
// it by its name and none is done to it by its id, though the roster learns of the
// sector only after the commit that makes it. The acts are the ones its making records,
// in any order (they share the making's transaction).
func checkMadeSectorRows(ctx context.Context, t *testing.T, hq *browser, id, name string, wantActs []string) {
	t.Helper()

	var acts []string
	for _, row := range auditRows(ctx, t, hq, "", "") {
		if text(row, "tenantId") != id {
			continue
		}
		acts = append(acts, text(row, "act"))
		if text(row, "tenantName") != name || text(row, "subjectName") == id {
			t.Errorf("a row of %s's making = %v, want it in the sector by its name and done to nothing by its id %s", name, row, id)
		}
	}
	slices.Sort(acts)
	if !slices.Equal(acts, wantActs) {
		t.Errorf("the acts of %s's making = %v, want %v", name, acts, wantActs)
	}
}

// TestInvitedSector holds the way a sector comes to exist, on the served stack:
// Headquarters brings an organization in by inviting one person, and that person's first
// sign-in makes the sector. Nobody charts a sector directly.
//
// The invitation carries the Sector record's fields as Headquarters entered them, and
// the role the people of the sector's directory start with. Sending it takes the Create
// grant on Sectors (the Surveyor's and Headquarters'), and the details are held to the
// record's own create when it is sent. While it waits, the mailed message says nothing of
// the sector, the invitation page shows its name and the invited address, and the list
// of the invitations that stand shows the details. The acceptance creates the sector,
// seats the person as its marshal and, where they came through an organization's
// directory, makes that directory the sector's own with the starting role, in one
// transaction:
//
//   - accepted with a password, the sector has no directory;
//   - an invitation that says directory only is refused a password, with what to sign in
//     with, and nothing is made;
//   - a first sign-in through a directory another sector has is refused, and nothing is
//     made;
//   - accepted through the fleet's Google Workspace once no sector has it, the sector is
//     made with that Workspace as its directory.
//
// A sector's name is one sector's (the unique index on Sectors.Name), so an invitation
// whose sector would take a name that is taken is refused when it is sent. Two
// invitations may wait with the same new name: the first acceptance takes it, and the
// second is refused at its first sign-in, told the name is taken, with no sector made,
// no role given and the invitation standing for Headquarters to withdraw. A person who
// was signing up with a password then is left no account (the session library makes
// the account in the transaction that accepts the invitation), and signs up again from
// the invitation Headquarters sends next.
//
// The rows of a sector's making in the audit log (the founder's role, the acceptance, and
// where it came through a directory the directory's three) name the sector by its name,
// never by its id.
//
// Demonstrates: tenancy.invited-tenant, auth.invitation.
func TestInvitedSector(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	// The conditional grant this suite proves, pinned to the role file: the Surveyor
	// invites, and never with a sector closed to its directory, which no Create grant
	// on the record covers.
	provesGrant(t, crew.Roles(), "Surveyor", "Execute", "InviteCrewSector", "new.directoryOnly = false")

	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	surveyor := newBrowser(t, s, consoleAPI)
	surveyor.signIn(ctx, "surveyor")
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")

	consumed := func(id string) bool {
		return readColumn[spanner.NullTime](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{id}, "ConsumedAt").Valid
	}
	sectorNames := func() []string {
		status, body := governor.do(ctx, http.MethodGet, consoleAPI+"/sectors?limit=200", nil)
		assertStatus(t, status, http.StatusOK, body)
		var names []string
		for _, row := range decodeRows(t, body) {
			names = append(names, cell[string](t, row, "name"))
		}

		return names
	}
	seeded := []string{"Anvil", "Bastion", "Cinder"}

	// Nobody creates a sector directly: the create operation answers as an operation on
	// a resource the route does not know answers, 400, whoever asks and whatever they
	// hold, and says how a sector is made.
	chart := []byte(`[{"op":"add","path":"/sectors","value":{"name":"Dawn","region":"Outer frontier","established":"2226-10-02"}}]`)
	for name, b := range map[string]*browser{"Headquarters": governor, "the surveyor": surveyor, "a marshal": marshal} {
		status, body := b.do(ctx, http.MethodPatch, consoleAPI+"/resources", chart)
		if status != http.StatusBadRequest || !strings.Contains(string(body), "a sector is not created directly: invite one person, and the sector is made when that person first signs in") {
			t.Errorf("a direct create of a sector by %s: status %d, want 400 saying how a sector is made: %s", name, status, body)
		}
	}
	if got := sectorNames(); !slices.Equal(got, seeded) {
		t.Fatalf("the sectors after the refused creates = %v, want %v", got, seeded)
	}

	// Sending takes the method's grant and the Create grant on Sectors: a marshal holds
	// neither. The details are held to the record when the invitation is sent, and the
	// starting role to what the sender could give a person directly: the surveyor gives
	// no role, so names none.
	dawn := map[string]any{"email": "Dawn Founder <founder@dawn.example>", "name": "Dawn Founder", "sectorName": "Dawn", "sectorRegion": "Outer frontier", "sectorEstablished": "2226-10-02"}
	with := func(base map[string]any, key string, value any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		if value == nil {
			delete(out, key)
		} else {
			out[key] = value
		}

		return out
	}
	for name, tt := range map[string]struct {
		by   *browser
		body map[string]any
		want int
	}{
		"by a marshal":                                     {by: marshal, body: dawn, want: http.StatusForbidden},
		"with no name for the sector":                      {by: governor, body: with(dawn, "sectorName", nil), want: http.StatusBadRequest},
		"with a date that is no date":                      {by: governor, body: with(dawn, "sectorEstablished", "soon"), want: http.StatusBadRequest},
		"to something that is no address":                  {by: governor, body: with(dawn, "email", "founder at dawn"), want: http.StatusBadRequest},
		"with a starting role that is no role":             {by: governor, body: with(dawn, "defaultRole", "Stowaway"), want: http.StatusBadRequest},
		"with a starting role of the whole application":    {by: governor, body: with(dawn, "defaultRole", "Headquarters"), want: http.StatusBadRequest},
		"with a starting role the sender could not give":   {by: surveyor, body: with(dawn, "defaultRole", "SectorCrew"), want: http.StatusForbidden},
		"that says directory only, by the surveyor":        {by: surveyor, body: with(dawn, "sectorDirectoryOnly", true), want: http.StatusForbidden},
		"with a field the invitation's form does not have": {by: governor, body: with(dawn, "sectorId", "11111111-1111-4111-8111-111111111111"), want: http.StatusBadRequest},
	} {
		if status, body, _ := inviteSector(ctx, t, tt.by, tt.body); status != tt.want {
			t.Errorf("an invitation %s: status %d, want %d: %s", name, status, tt.want, body)
		}
	}
	for _, message := range s.mail.Messages() {
		if strings.Contains(message.To, "dawn.example") {
			t.Fatalf("a refused invitation mailed %s: %q", message.To, message.Subject)
		}
	}
	// A name a sector holds is refused when the invitation is sent, where Headquarters
	// can give another: nothing is mailed and no invitation is written.
	status, body, _ := inviteSector(ctx, t, governor, with(with(dawn, "sectorName", "Anvil"), "email", "founder@second-anvil.example"))
	if status != http.StatusConflict || !strings.Contains(string(body), "a sector with the name Anvil already exists: give the new one another") {
		t.Errorf("an invitation whose sector would take Anvil's name: status %d, want 409 saying the name is taken: %s", status, body)
	}
	if got := mailLinksTo(ctx, t, s, "founder@second-anvil.example"); got != 0 {
		t.Errorf("the refused invitation wrote %d mail links, want none", got)
	}
	for _, message := range s.mail.Messages() {
		if strings.Contains(message.To, "second-anvil.example") {
			t.Errorf("the refused invitation mailed %s: %q", message.To, message.Subject)
		}
	}

	// The surveyor's invitation, with no starting role. The row names no sector and
	// carries no role: it carries the sector to make.
	status, body, sent := inviteSector(ctx, t, surveyor, dawn)
	assertStatus(t, status, http.StatusOK, body)
	if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{sent.ID}, "TenantId"); got.Valid {
		t.Errorf("the invitation's tenant = %v, want none: no sector exists yet", got)
	}
	if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{sent.ID}, "NewTenant"); !strings.Contains(got.StringVal, `"name":"Dawn"`) {
		t.Errorf("the invitation's new tenant = %v, want the sector Dawn", got)
	}
	if got := sectorNames(); !slices.Equal(got, seeded) {
		t.Fatalf("the sectors once the invitation is sent = %v, want %v: no sector exists until the first sign-in", got, seeded)
	}

	// The mailed message names the application and carries the link, and says nothing
	// of the sector.
	message, token := lastMailTo(t, s, "founder@dawn.example", "invitation")
	for _, detail := range []string{"Dawn", "Outer frontier", "2226"} {
		if text := strings.ReplaceAll(message.Subject+"\n"+message.Text, "Dawn Founder", ""); strings.Contains(text, detail) {
			t.Errorf("the mailed invitation carries the sector's detail %q:\n%s", detail, text)
		}
	}

	// The list of the invitations that stand shows the details to the people who may
	// read them on the Sector record, and is not the marshal's to read.
	row := invitedSectorRow(ctx, t, governor, sent.ID)
	if row == nil {
		t.Fatalf("the list of invited sectors does not hold the invitation %s", sent.ID)
	}
	if cell[string](t, row, "email") != "founder@dawn.example" || cell[string](t, row, "sectorName") != "Dawn" || cell[string](t, row, "sectorRegion") != "Outer frontier" || cell[string](t, row, "sectorEstablished") != "2226-10-02" || cell[bool](t, row, "sectorDirectoryOnly") {
		t.Errorf("the invitation as Headquarters lists it = %v, want Dawn's details", row)
	}
	if status, body := marshal.do(ctx, http.MethodGet, invitedSectorsPath, nil); status != http.StatusForbidden {
		t.Errorf("the marshal's list of invited sectors: status %d, want 403: %s", status, body)
	}

	// The invitation page, before the sign-in: the sector's name and the invited
	// address, and nothing else of the sector.
	founder := newBrowser(t, s, consoleAPI)
	page := openInvitation(ctx, t, founder, token)
	if page["newTenant"] != true || page["tenantName"] != "Dawn" || page["email"] != "founder@dawn.example" || page["directoryOnly"] != false || page["signUp"] != true {
		t.Errorf("the invitation page = %v, want a new sector Dawn for founder@dawn.example, open to a password", page)
	}
	if text, _ := json.Marshal(page); strings.Contains(string(text), "Outer frontier") || strings.Contains(string(text), "2226") {
		t.Errorf("the invitation page carries the sector's details: %s", text)
	}

	// Accepted with a password: the sector exists with the invitation's details and no
	// directory, the person is its marshal, and their chart lights it at their next
	// request.
	if status, body := founder.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": "founder", "password": signUpPassword})); status != http.StatusOK {
		t.Fatalf("signing up at the invitation: status %d: %s", status, body)
	}
	founder.secondFactor(ctx, "founder")
	made := sectorNamed(ctx, t, governor, "Dawn")
	if made == nil {
		t.Fatalf("the sectors after the acceptance = %v, want Dawn among them", sectorNames())
	}
	dawnID := cell[string](t, made, "id")
	if cell[string](t, made, "region") != "Outer frontier" || cell[string](t, made, "established") != "2226-10-02" || cell[bool](t, made, "directoryOnly") {
		t.Errorf("Dawn as made = %v, want the invitation's details and no directory-only switch", made)
	}
	if rows := directories(ctx, t, governor, dawnID); len(rows) != 0 {
		t.Errorf("Dawn's directory = %+v, want none: the founder signed up with a password", rows)
	}
	if got, want := crewHolds(t, s.db, "founder"), []string{"domain/" + dawnID + " SectorMarshal"}; !slices.Equal(got, want) {
		t.Errorf("the founder holds %v, want %v", got, want)
	}
	if got := founder.domains(ctx); !slices.Equal(got, []string{dawnID}) {
		t.Errorf("the founder's chart = %v, want Dawn (%s) alone, at the first request after the sign-in", got, dawnID)
	}
	checkMadeSectorRows(ctx, t, governor, dawnID, "Dawn", []string{"invitationAccepted", "roleGiven"})
	if !consumed(sent.ID) {
		t.Error("the accepted invitation still stands")
	}
	if row := invitedSectorRow(ctx, t, governor, sent.ID); row != nil {
		t.Errorf("the list of invited sectors still holds the accepted invitation: %v", row)
	}
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("withdraw-crew-invited-sector"), jsonBody(t, map[string]string{"invitationId": sent.ID})); status != http.StatusNotFound {
		t.Errorf("withdrawing an accepted invitation: status %d, want 404: %s", status, body)
	}

	// An invitation that says directory only, with the role the directory's people start
	// with. Headquarters gives roles, so may name one.
	ember := map[string]any{"email": "ember-founder@example.com", "sectorName": "Ember", "sectorRegion": "Near frontier", "sectorEstablished": "2226-10-20", "sectorDirectoryOnly": true, "defaultRole": "SectorCrew"}
	status, body, closed := inviteSector(ctx, t, governor, ember)
	assertStatus(t, status, http.StatusOK, body)
	_, closedToken := lastMailTo(t, s, "ember-founder@example.com", "invitation")
	row = invitedSectorRow(ctx, t, governor, closed.ID)
	if row == nil || !cell[bool](t, row, "sectorDirectoryOnly") || cell[string](t, row, "defaultRole") != "SectorCrew" {
		t.Errorf("the directory-only invitation as Headquarters lists it = %v, want it directory only with SectorCrew to start with", row)
	}
	nothingMade := func(step string) {
		t.Helper()
		if got := sectorNames(); !slices.Equal(got, []string{"Anvil", "Bastion", "Cinder", "Dawn"}) {
			t.Errorf("%s: the sectors = %v, want no Ember", step, got)
		}
		if consumed(closed.ID) {
			t.Errorf("%s: the invitation no longer stands", step)
		}
		for _, username := range []string{"ember-founder", "ember-founder@example.com"} {
			if row := crewAccount(ctx, t, governor, username); row != nil {
				t.Errorf("%s: an account was made for the refused person: %v", step, row)
			}
		}
	}

	// The page says so and offers no account with a password; a sign-up with one is
	// refused, with what to sign in with, and nothing is made.
	emberFounder := newBrowser(t, s, consoleAPI)
	page = openInvitation(ctx, t, emberFounder, closedToken)
	if page["newTenant"] != true || page["tenantName"] != "Ember" || page["directoryOnly"] != true || page["signUp"] != false {
		t.Errorf("the directory-only invitation's page = %v, want a new sector Ember, directory only, with no sign-up by password", page)
	}
	status, body = emberFounder.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": "ember-founder", "password": signUpPassword}))
	var refused struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &refused); err != nil || status != http.StatusUnauthorized || refused.Code != string(sessioninfo.RefusedOrganizationAccountRequired) || refused.Message != organizationAccountSentence {
		t.Errorf("a password at a directory-only invitation: status %d %s (%v), want 401 %s saying what to sign in with", status, body, err, sessioninfo.RefusedOrganizationAccountRequired)
	}
	nothingMade("after the password was refused")

	// The fleet's Google Workspace is Cinder's directory: a first sign-in through it is
	// refused, the person told their organization already has a sector, and nothing is
	// made.
	status, location := emberFounder.loginConsoleGoogle(ctx, "ember-founder@example.com")
	if want := consoleLogin + "?code=" + string(sessioninfo.RefusedOrganizationHasTenant); status != http.StatusFound || location != want {
		t.Fatalf("accepting through a directory Cinder has: status %d to %q, want %d to %q", status, location, http.StatusFound, want)
	}
	if sess := emberFounder.session(ctx); sess["authenticated"] != false {
		t.Errorf("the session after the refusal = %v, want none", sess)
	}
	nothingMade("after the sign-in through Cinder's directory was refused")

	// Cinder gives its directory up, and the same link, followed again through the
	// Workspace, makes Ember: the invitation's details, directory only, that Workspace
	// as its directory with the starting role, and the person as its marshal.
	rows := directories(ctx, t, governor, cinder)
	if len(rows) != 1 {
		t.Fatalf("Cinder's directory = %+v, want the seeded fleet directory alone", rows)
	}
	if status, body := governor.do(ctx, http.MethodPatch, consoleAPI+"/resources", []byte(`[{"op":"remove","path":"/sectors/`+cinder+`/sector-directories/`+rows[0].ID+`"}]`)); status != http.StatusOK {
		t.Fatalf("removing Cinder's directory: status %d: %s", status, body)
	}
	emberFounder = newBrowser(t, s, consoleAPI)
	openInvitation(ctx, t, emberFounder, closedToken)
	if status, location := emberFounder.loginConsoleGoogle(ctx, "ember-founder@example.com"); status != http.StatusFound || location != consolePage {
		t.Fatalf("accepting through the Workspace no sector has: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
	}
	made = sectorNamed(ctx, t, governor, "Ember")
	if made == nil {
		t.Fatalf("the sectors after the acceptance through the Workspace = %v, want Ember among them", sectorNames())
	}
	emberID := cell[string](t, made, "id")
	if cell[string](t, made, "region") != "Near frontier" || cell[string](t, made, "established") != "2226-10-20" || !cell[bool](t, made, "directoryOnly") {
		t.Errorf("Ember as made = %v, want the invitation's details, directory only", made)
	}
	got := directories(ctx, t, governor, emberID)
	if len(got) != 1 || got[0].Method != rows[0].Method || got[0].Connection != rows[0].Connection || got[0].DefaultSeat == nil || *got[0].DefaultSeat != "SectorCrew" {
		t.Errorf("Ember's directory = %+v, want the Workspace the founder came through (%s %s) with SectorCrew to start with", got, rows[0].Method, rows[0].Connection)
	}
	if held := rolesHeldInSector(ctx, t, governor, emberID, "ember-founder@example.com"); !slices.Equal(held, []string{"SectorMarshal"}) {
		t.Errorf("the founder's roles at Ember = %v, want SectorMarshal alone", held)
	}
	if got := emberFounder.domains(ctx); !slices.Equal(got, []string{emberID}) {
		t.Errorf("the founder's chart = %v, want Ember (%s) alone", got, emberID)
	}
	checkMadeSectorRows(ctx, t, governor, emberID, "Ember", []string{"directoryOnlyOn", "directoryOpened", "directoryTaken", "invitationAccepted", "roleGiven"})
	if !consumed(closed.ID) {
		t.Error("the accepted invitation still stands")
	}

	// To change the details, Headquarters withdraws the invitation and sends another:
	// the withdrawn link is dead, and the list no longer holds it.
	flint := map[string]any{"email": "founder@flint.example", "sectorName": "Flint", "sectorRegion": "Mid frontier", "sectorEstablished": "2226-11-02"}
	status, body, regret := inviteSector(ctx, t, governor, flint)
	assertStatus(t, status, http.StatusOK, body)
	_, regretToken := lastMailTo(t, s, "founder@flint.example", "invitation")
	if status, body := marshal.do(ctx, http.MethodPost, crewMethod("withdraw-crew-invited-sector"), jsonBody(t, map[string]string{"invitationId": regret.ID})); status != http.StatusForbidden {
		t.Errorf("the marshal's withdrawal: status %d, want 403: %s", status, body)
	}
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("withdraw-crew-invited-sector"), jsonBody(t, map[string]string{"invitationId": regret.ID})); status != http.StatusNoContent {
		t.Fatalf("withdrawing the invitation: status %d, want 204: %s", status, body)
	}
	if !consumed(regret.ID) {
		t.Error("the withdrawn invitation still stands")
	}
	if row := invitedSectorRow(ctx, t, governor, regret.ID); row != nil {
		t.Errorf("the list of invited sectors still holds the withdrawn invitation: %v", row)
	}
	late := newBrowser(t, s, consoleAPI)
	if status, body := late.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the withdrawn link: status %d: %s", status, body)
	}
	if status, body := late.do(ctx, http.MethodPost, consoleAPI+"/user/invitation", jsonBody(t, map[string]string{"token": regretToken})); status == http.StatusOK {
		t.Errorf("opening a withdrawn invitation: status %d, want a refusal: %s", status, body)
	}
	if sectorNamed(ctx, t, governor, "Flint") != nil {
		t.Error("a withdrawn invitation made the sector Flint")
	}

	// Two invitations wait with the same new name: no sector holds it yet, so both are
	// sent and both stand. The first acceptance takes the name. The second person's
	// sign-up waits for its code as every sign-up through an invitation does, and the
	// code entered, their first sign-in is refused and told why: no second sector is
	// made, no role is given, no session opens, and the invitation stands until
	// Headquarters withdraws it.
	gale := map[string]any{"sectorName": "Gale", "sectorRegion": "Windward reach", "sectorEstablished": "2226-11-09"}
	status, body, first := inviteSector(ctx, t, governor, with(gale, "email", "first@gale.example"))
	assertStatus(t, status, http.StatusOK, body)
	_, firstToken := lastMailTo(t, s, "first@gale.example", "invitation")
	status, body, second := inviteSector(ctx, t, governor, with(gale, "email", "second@gale.example"))
	assertStatus(t, status, http.StatusOK, body)
	_, secondToken := lastMailTo(t, s, "second@gale.example", "invitation")
	if invitedSectorRow(ctx, t, governor, first.ID) == nil || invitedSectorRow(ctx, t, governor, second.ID) == nil {
		t.Fatal("the list of invited sectors does not hold both invitations for Gale")
	}
	galeFirst := newBrowser(t, s, consoleAPI)
	openInvitation(ctx, t, galeFirst, firstToken)
	if status, body := galeFirst.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": "gale-first", "password": signUpPassword})); status != http.StatusOK {
		t.Fatalf("the first acceptance for Gale: status %d: %s", status, body)
	}
	galeFirst.secondFactor(ctx, "gale-first")
	if sectorNamed(ctx, t, governor, "Gale") == nil {
		t.Fatalf("the sectors after the first acceptance = %v, want Gale among them", sectorNames())
	}
	galeSecond := newBrowser(t, s, consoleAPI)
	openInvitation(ctx, t, galeSecond, secondToken)
	if status, body := galeSecond.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": "gale-second", "password": signUpPassword})); status != http.StatusOK || !heldForFactor(t, body) {
		t.Fatalf("the second person's sign-up for Gale: status %d, want 200 held for the code: %s", status, body)
	}
	if row := crewAccount(ctx, t, governor, "gale-second"); row != nil {
		t.Errorf("the second person's account before the code = %v, want none: the link alone makes nobody", row)
	}
	status, body = galeSecond.enterCode(ctx, "gale-second")
	t.Logf("the second person for Gale is answered %d %s", status, body)
	if status != http.StatusConflict || !strings.Contains(string(body), "a sector with the name Gale already exists: give the new one another") {
		t.Errorf("the second acceptance for Gale: status %d, want 409 saying the name is taken: %s", status, body)
	}
	if sess := galeSecond.session(ctx); sess["authenticated"] != false {
		t.Errorf("the second person's session after the refusal = %v, want none", sess)
	}
	if got := crewHolds(t, s.db, "gale-second"); len(got) != 0 {
		t.Errorf("the person refused Gale holds %v, want nothing", got)
	}
	if row := crewAccount(ctx, t, governor, "gale-second"); row != nil {
		t.Errorf("the refused sign-up left the account %v, want none: the account is made with the acceptance or not at all", row)
	}
	if got := sectorNames(); !slices.Equal(got, []string{"Anvil", "Bastion", "Cinder", "Dawn", "Ember", "Gale"}) {
		t.Errorf("the sectors after the refused acceptance = %v, want one Gale", got)
	}
	if consumed(second.ID) {
		t.Error("the refused invitation no longer stands")
	}
	if status, body := governor.do(ctx, http.MethodPost, crewMethod("withdraw-crew-invited-sector"), jsonBody(t, map[string]string{"invitationId": second.ID})); status != http.StatusNoContent {
		t.Errorf("withdrawing the invitation that can no longer be accepted: status %d, want 204: %s", status, body)
	}

	// Headquarters sends the person another, with a name that is free. The sign-up
	// that was refused left them no account, so they sign up again from this one: the
	// sector is made and they are its marshal.
	status, body, _ = inviteSector(ctx, t, governor, map[string]any{"email": "second@gale.example", "sectorName": "Squall", "sectorRegion": "Windward reach", "sectorEstablished": "2226-11-09"})
	assertStatus(t, status, http.StatusOK, body)
	_, squallToken := lastMailTo(t, s, "second@gale.example", "invitation")
	galeSecond = newBrowser(t, s, consoleAPI)
	openInvitation(ctx, t, galeSecond, squallToken)
	if status, body := galeSecond.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": "gale-second", "password": signUpPassword})); status != http.StatusOK || !heldForFactor(t, body) {
		t.Fatalf("signing up again at the next invitation: status %d, want 200 held for the code: %s", status, body)
	}
	galeSecond.secondFactor(ctx, "gale-second")
	squall := sectorNamed(ctx, t, governor, "Squall")
	if squall == nil {
		t.Fatalf("the sectors after the next invitation was accepted = %v, want Squall among them", sectorNames())
	}
	if got, want := crewHolds(t, s.db, "gale-second"), []string{"domain/" + cell[string](t, squall, "id") + " SectorMarshal"}; !slices.Equal(got, want) {
		t.Errorf("the person holds %v once the next invitation is accepted, want %v", got, want)
	}
}
