//go:build skipAuth

package integration

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestGeneratedIdentitySectorInvitationsAndPassword holds the methods of a sector that
// need the real session manager, on the served stack: an invitation into the sector
// (written in the method's transaction, mailed once it has committed, accepted into the
// sector and no other), its withdrawal, and a temporary password.
//
// The Sector Marshal invites in every role but the Marshal's own
// (new.role != 'SectorMarshal'), withdraws an invitation that stands and gives no
// Marshal's role (pending = true AND role != 'SectorMarshal'), and sets a temporary
// password for a person the sector holds alone. The roles an invitation gives are a list
// column of its row, so the withdrawal's condition is judged once for each of them: an
// invitation Headquarters sent in the Marshal's role, alone or among others, is not the
// Marshal's to withdraw.
//
// Demonstrates: auth.tenant-methods, auth.generated-identity, condition.list-attribute.
func TestGeneratedIdentitySectorInvitationsAndPassword(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	// The Execute grants this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "InviteCrewSectorUser", "new.role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "WithdrawCrewSectorInvitation", "pending = true AND role != 'SectorMarshal'")
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "SetCrewSectorTemporaryPassword", "heldElsewhere = false AND heldGlobally = false")

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")
	marshalID := crewAccountID(ctx, t, hq, "marshal")
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")

	post := func(b *browser, sector, method, body string) (int, []byte) {
		return b.do(ctx, http.MethodPost, sectorPath(sector, method), []byte(body))
	}
	consumed := func(id string) bool {
		return readColumn[spanner.NullTime](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{id}, "ConsumedAt").Valid
	}

	// An invitation the grant or the method refuses mails nobody.
	for name, tt := range map[string]struct {
		body string
		want int
	}{
		"in the Marshal's own role":            {body: `{"email":"refused@example.com","roles":["SectorMarshal"]}`, want: http.StatusForbidden},
		"with the Marshal's role among others": {body: `{"email":"refused@example.com","roles":["Cadet","SectorMarshal"]}`, want: http.StatusForbidden},
		"with no role":                         {body: `{"email":"refused@example.com"}`, want: http.StatusForbidden},
		"in a role of the whole application":   {body: `{"email":"refused@example.com","roles":["ClientBrowser"]}`, want: http.StatusBadRequest},
		"to something that is no address":      {body: `{"email":"refused at example","roles":["Cadet"]}`, want: http.StatusBadRequest},
	} {
		if status, body := post(marshal, anvil, "invite-crew-sector-user", tt.body); status != tt.want {
			t.Errorf("an invitation %s: status %d, want %d: %s", name, status, tt.want, body)
		}
	}
	if status, body := post(cadet, anvil, "invite-crew-sector-user", `{"email":"refused@example.com","roles":["Cadet"]}`); status != http.StatusForbidden {
		t.Errorf("the cadet's invitation: status %d, want 403: %s", status, body)
	}
	for _, message := range s.mail.Messages() {
		if strings.HasPrefix(message.To, "refused") {
			t.Errorf("a refused invitation mailed %s: %q", message.To, message.Subject)
		}
	}

	// The Marshal's invitation is into Anvil: the request names no sector, and the row
	// carries the one the request was made under.
	const email, username = "recruit@example.com", "recruit"
	status, body := post(marshal, anvil, "invite-crew-sector-user", `{"email":"Recruit <`+email+`>","roles":["Cadet"]}`)
	assertStatus(t, status, http.StatusOK, body)
	var answer invited
	if err := json.Unmarshal(body, &answer); err != nil || answer.ID == "" || !answer.Sent {
		t.Fatalf("invite-crew-sector-user answered %s (%v), want the invitation's id and the word that its message went", body, err)
	}
	if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{answer.ID}, "Tenant"); got.StringVal != anvil {
		t.Errorf("the invitation's tenant = %v, want %s", got, anvil)
	}
	if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{answer.ID}, "InvitedBy"); got.StringVal != marshalID {
		t.Errorf("the invitation's sender = %v, want the Marshal (%s)", got, marshalID)
	}
	_, token := lastMailTo(t, s, email, "invitation")

	// The sector's list holds it, standing, and its row answers that the Marshal may
	// withdraw it.
	status, body = marshal.do(ctx, http.MethodGet, sectorPath(anvil, sectorInvitationsPath+"/"+answer.ID+"?capabilities=Execute"), nil)
	assertStatus(t, status, http.StatusOK, body)
	listed := decodeRow(t, body)
	if cell[string](t, listed, "email") != email || !cell[bool](t, listed, "pending") {
		t.Errorf("the invitation as Anvil's list reads it = %v, want it standing for %s", listed, email)
	}
	caps, _ := listed["zzCapabilities"].(map[string]any)
	if methods, _ := caps["Execute"].([]any); !slices.Contains(methods, any("WithdrawCrewSectorInvitation")) {
		t.Errorf("the standing invitation's Execute answer %v leaves out WithdrawCrewSectorInvitation", methods)
	}

	// Whoever follows the link and signs up holds the role in Anvil, and nowhere else.
	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the invitation: status %d: %s", status, body)
	}
	if status, body := stranger.do(ctx, http.MethodPost, consoleAPI+"/user/invitation", jsonBody(t, map[string]string{"token": token})); status != http.StatusOK {
		t.Fatalf("opening the invitation: status %d: %s", status, body)
	}
	if status, body := stranger.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": username, "password": signUpPassword})); status != http.StatusOK {
		t.Fatalf("signing up at the invitation: status %d: %s", status, body)
	}
	stranger.secondFactor(ctx, username)
	if got, want := crewHolds(t, s.db, username), []string{"domain/anvil Cadet"}; !slices.Equal(got, want) {
		t.Errorf("the invited person holds %v, want %v", got, want)
	}
	if !consumed(answer.ID) {
		t.Error("the accepted invitation still stands")
	}
	// An accepted invitation stands no more, so the Marshal's grant no longer reaches it.
	if status, body := post(marshal, anvil, "withdraw-crew-sector-invitation", `{"invitationId":"`+answer.ID+`"}`); status != http.StatusForbidden {
		t.Errorf("withdrawing an accepted invitation: status %d, want 403: %s", status, body)
	}

	// A withdrawal: the invitation is the sector's own and stands.
	status, body = post(marshal, anvil, "invite-crew-sector-user", `{"email":"regret@example.com","roles":["Cadet"]}`)
	assertStatus(t, status, http.StatusOK, body)
	var regret invited
	if err := json.Unmarshal(body, &regret); err != nil || regret.ID == "" {
		t.Fatalf("invite-crew-sector-user answered %s (%v)", body, err)
	}
	withdraw := func(b *browser, sector, id string) (int, []byte) {
		return post(b, sector, "withdraw-crew-sector-invitation", `{"invitationId":"`+id+`"}`)
	}
	if status, body := withdraw(cadet, anvil, regret.ID); status != http.StatusForbidden {
		t.Errorf("the cadet's withdrawal: status %d, want 403: %s", status, body)
	}
	status, body = withdraw(marshal, anvil, regret.ID)
	assertStatus(t, status, http.StatusNoContent, body)
	if !consumed(regret.ID) {
		t.Error("the withdrawn invitation still stands")
	}
	if status, body := withdraw(marshal, anvil, regret.ID); status != http.StatusForbidden {
		t.Errorf("a second withdrawal: status %d, want 403, since the invitation stands no more: %s", status, body)
	}

	// An invitation into another sector, and one into the application, are no rows of
	// Anvil's list: the Marshal does not find them.
	atBastion := inviteCrew(ctx, t, hq, `{"email":"elsewhere@example.com","roles":["Pilot"],"domain":"bastion"}`)
	intoApplication := inviteCrew(ctx, t, hq, `{"email":"staff@example.com","roles":["ClientBrowser"]}`)
	for name, id := range map[string]string{"another sector's": atBastion.ID, "the application's": intoApplication.ID} {
		if status, body := withdraw(marshal, anvil, id); status != http.StatusNotFound {
			t.Errorf("withdrawing %s invitation at Anvil: status %d, want 404: %s", name, status, body)
		}
		if consumed(id) {
			t.Errorf("%s invitation was withdrawn from Anvil", name)
		}
	}

	// An invitation into Anvil that gives the Marshal's own role is a row of Anvil's list
	// and is not the Marshal's to withdraw: the grant's condition is judged once for each
	// role the invitation gives, and every one has to pass. The row's Execute answer says
	// so before the Marshal tries, and Headquarters, whose grant carries no condition,
	// withdraws it.
	asMarshal := inviteCrew(ctx, t, hq, `{"email":"successor@example.com","roles":["SectorMarshal"],"domain":"anvil"}`)
	amongOthers := inviteCrew(ctx, t, hq, `{"email":"deputy@example.com","roles":["Cadet","SectorMarshal"],"domain":"anvil"}`)
	asPilot := inviteCrew(ctx, t, hq, `{"email":"wingmate@example.com","roles":["Pilot","Cadet"],"domain":"anvil"}`)
	withdrawable := func(id string) bool {
		status, body := marshal.do(ctx, http.MethodGet, sectorPath(anvil, sectorInvitationsPath+"/"+id+"?capabilities=Execute"), nil)
		assertStatus(t, status, http.StatusOK, body)
		caps, _ := decodeRow(t, body)["zzCapabilities"].(map[string]any)
		methods, _ := caps["Execute"].([]any)

		return slices.Contains(methods, any("WithdrawCrewSectorInvitation"))
	}
	for name, id := range map[string]string{"in the Marshal's role": asMarshal.ID, "with the Marshal's role among others": amongOthers.ID} {
		if withdrawable(id) {
			t.Errorf("the invitation %s answers that the Marshal may withdraw it", name)
		}
		if status, body := withdraw(marshal, anvil, id); status != http.StatusForbidden {
			t.Errorf("the Marshal's withdrawal of the invitation %s: status %d, want 403: %s", name, status, body)
		}
		if consumed(id) {
			t.Errorf("the invitation %s was withdrawn by the Marshal", name)
		}
	}
	if !withdrawable(asPilot.ID) {
		t.Error("the invitation in two roles the Marshal gives does not answer that the Marshal may withdraw it")
	}
	status, body = withdraw(marshal, anvil, asPilot.ID)
	assertStatus(t, status, http.StatusNoContent, body)
	if !consumed(asPilot.ID) {
		t.Error("the invitation in two roles the Marshal gives still stands")
	}
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/withdraw-crew-invitation", []byte(`{"invitationId":"`+asMarshal.ID+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	if !consumed(asMarshal.ID) {
		t.Error("the invitation in the Marshal's role still stands after Headquarters withdrew it")
	}

	// A temporary password for a person Anvil holds alone: the recruit, who signed up
	// with a password of their own. Their session ends and the next sign-in is held to
	// a change of password.
	recruitID := crewAccountID(ctx, t, hq, username)
	if status, body := post(marshal, anvil, "set-crew-sector-temporary-password", `{"userId":"`+recruitID+`","password":""}`); status != http.StatusBadRequest {
		t.Errorf("an empty temporary password: status %d, want 400: %s", status, body)
	}
	status, body = post(marshal, anvil, "set-crew-sector-temporary-password", `{"userId":"`+recruitID+`","password":"first-day-pass"}`)
	assertStatus(t, status, http.StatusNoContent, body)
	if got := readColumn[bool](ctx, t, s.db, "CrewSessionUsers", spanner.Key{recruitID}, "PasswordChangeRequired"); !got {
		t.Error("the recruit is not marked to change the password")
	}
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user-domains", nil); status != http.StatusUnauthorized {
		t.Errorf("the recruit's session after the temporary password: status %d, want 401: %s", status, body)
	}
	status, body = stranger.login(ctx, username, "first-day-pass")
	assertStatus(t, status, http.StatusOK, body)
	if heldForFactor(t, body) {
		stranger.secondFactor(ctx, username)
	}
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user-domains", nil); status != http.StatusForbidden || !strings.Contains(string(body), string(sessioninfo.RefusedPasswordChangeRequired)) {
		t.Errorf("the recruit signed in with the temporary password: status %d, want 403 naming %s: %s", status, sessioninfo.RefusedPasswordChangeRequired, body)
	}
	// The requirement stands for a person who holds a password, and the Marshal's grant
	// reaches it for the same people.
	status, body = post(marshal, anvil, "require-crew-sector-password-change", `{"userId":"`+recruitID+`"}`)
	assertStatus(t, status, http.StatusNoContent, body)

	// Once the recruit holds a role at Bastion too, the account is not Anvil's alone:
	// the Marshal sets no password for them.
	if status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/assign-crew-role", []byte(`{"username":"`+username+`","role":"Pilot","domain":"bastion"}`)); status != http.StatusNoContent {
		t.Fatalf("seating the recruit at Bastion: status %d: %s", status, body)
	}
	if status, body := post(marshal, anvil, "set-crew-sector-temporary-password", `{"userId":"`+recruitID+`","password":"second-day-pass"}`); status != http.StatusForbidden {
		t.Errorf("a temporary password for a person who holds a role at Bastion too: status %d, want 403: %s", status, body)
	}
}
