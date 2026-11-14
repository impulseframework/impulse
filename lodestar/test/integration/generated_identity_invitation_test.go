//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
	"github.com/impulseframework/impulse/resource"
	"google.golang.org/grpc/codes"
)

// invited is the generated invite method's answer.
type invited struct {
	ID   string `json:"id"`
	Sent bool   `json:"sent"`
}

// inviteCrew posts the generated invite method as the browser and decodes its answer.
func inviteCrew(ctx context.Context, t *testing.T, b *browser, body string) invited {
	t.Helper()

	status, respBody := b.do(ctx, http.MethodPost, consoleAPI+"/invite-crew-user", []byte(body))
	assertStatus(t, status, http.StatusOK, respBody)
	var answer invited
	if err := json.Unmarshal(respBody, &answer); err != nil || answer.ID == "" {
		t.Fatalf("invite-crew-user answered %s (%v), want the invitation's id", respBody, err)
	}

	return answer
}

// TestGeneratedIdentityInvitations holds the invitations of the generated management on
// the served stack, with the real session manager and its mail: the invitation is
// written in the method's transaction and its message goes once that has committed,
// the answer says whether it went, an invitation whose message could not be sent is
// withdrawn, an invitation gives a role, the person who follows the link holds it, and
// a withdrawal holds to invitations.
//
// Demonstrates: auth.generated-identity.
func TestGeneratedIdentityInvitations(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	governorID := crewAccountID(ctx, t, hq, "governor")
	consumed := func(id string) bool {
		return readColumn[spanner.NullTime](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{id}, "ConsumedAt").Valid
	}

	// A cadet holds no grant on the method; an invitation gives a role, which exists
	// where the invitation gives it.
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	if status, body := cadet.do(ctx, http.MethodPost, consoleAPI+"/invite-crew-user", []byte(`{"email":"ensign@example.com","roles":["ClientBrowser"]}`)); status != http.StatusForbidden {
		t.Errorf("the cadet's invitation: status %d, want 403: %s", status, body)
	}
	for name, body := range map[string]string{
		"without a role":                       `{"email":"ensign@example.com"}`,
		"with a role that does not exist":      `{"email":"ensign@example.com","roles":["NoSuchRole"]}`,
		"with a global role into a sector":     `{"email":"ensign@example.com","roles":["ClientBrowser"],"domain":"anvil"}`,
		"to something that is no address":      `{"email":"ensign at example","roles":["ClientBrowser"]}`,
		"into a sector named by an empty name": `{"email":"ensign@example.com","roles":["Pilot"],"domain":""}`,
	} {
		if status, respBody := hq.do(ctx, http.MethodPost, consoleAPI+"/invite-crew-user", []byte(body)); status != http.StatusBadRequest {
			t.Errorf("an invitation %s: status %d, want 400: %s", name, status, respBody)
		}
	}
	for _, message := range s.mail.Messages() {
		if message.To == "ensign@example.com" {
			t.Errorf("a refused invitation mailed the ensign: %q", message.Subject)
		}
	}

	// The invitation into the application: written and mailed, the answer says it
	// went, the row is the session library's, and the list shows it standing.
	const email, username = "ensign@example.com", "ensign"
	answer := inviteCrew(ctx, t, hq, `{"email":"Ensign <`+email+`>","roles":["ClientBrowser"]}`)
	if !answer.Sent {
		t.Fatalf("the invitation's answer says the message did not go: %+v", answer)
	}
	if got := readColumn[string](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{answer.ID}, "Kind"); got != "invitation" {
		t.Errorf("the stored link's kind = %q, want invitation", got)
	}
	if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{answer.ID}, "InvitedBy"); got.StringVal != governorID {
		t.Errorf("the invitation's sender = %v, want the governor (%s)", got, governorID)
	}
	if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{answer.ID}, "Tenant"); got.Valid {
		t.Errorf("the invitation's tenant = %v, want none: it is into the application", got)
	}
	status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-mail-links/"+answer.ID, nil)
	assertStatus(t, status, http.StatusOK, body)
	listed := decodeRow(t, body)
	if cell[string](t, listed, "email") != email || cell[string](t, listed, "kind") != "invitation" || listed["consumedAt"] != nil {
		t.Errorf("the invitation as the generated resource reads it = %v, want it standing for %s", listed, email)
	}
	if _, hashed := listed["tokenHash"]; hashed {
		t.Errorf("the generated resource serves the token's hash: %v", listed)
	}
	message, token := lastMailTo(t, s, email, "invitation")
	if message.Subject != "You are invited to Lodestar" {
		t.Errorf("the invitation's subject = %q", message.Subject)
	}

	// The person follows the link on a browser of their own, creates a login with a
	// password, and holds the role: the acceptance is the session library's, unchanged.
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
	if sess := stranger.session(ctx); sess["authenticated"] != true || sess["username"] != username {
		t.Fatalf("the session after the sign-up = %v, want %s signed in", sess, username)
	}
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/clients?limit=1", nil); status != http.StatusOK {
		t.Errorf("the invited person's client list: status %d, want 200 by the role the invitation gave: %s", status, body)
	}
	if !consumed(answer.ID) {
		t.Error("the accepted invitation still stands")
	}

	// An invitation into a sector carries the sector.
	sector := inviteCrew(ctx, t, hq, `{"email":"wingman@example.com","roles":["Pilot"],"domain":"anvil"}`)
	if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{sector.ID}, "Tenant"); got.StringVal != "anvil" {
		t.Errorf("the sector invitation's tenant = %v, want anvil", got)
	}

	// The sector's own list of its invitations holds it, standing, and no other
	// sector's does; once withdrawn it is still the sector's row, and stands no more.
	sectorInvitation := func(sectorID string) (int, map[string]any) {
		status, body := hq.do(ctx, http.MethodGet, sectorPath(sectorID, "crew-sector-invitations/"+sector.ID), nil)
		if status != http.StatusOK {
			return status, nil
		}

		return status, decodeRow(t, body)
	}
	status, listedAtAnvil := sectorInvitation("anvil")
	if status != http.StatusOK {
		t.Fatalf("the invitation in Anvil's list: status %d, want 200", status)
	}
	if cell[string](t, listedAtAnvil, "email") != "wingman@example.com" || !cell[bool](t, listedAtAnvil, "pending") || cell[string](t, listedAtAnvil, "invitedBy") != governorID {
		t.Errorf("the invitation as Anvil's list reads it = %v, want it standing for wingman@example.com, sent by the governor", listedAtAnvil)
	}
	if status, _ := sectorInvitation("bastion"); status != http.StatusNotFound {
		t.Errorf("Anvil's invitation in Bastion's list: status %d, want 404", status)
	}
	if status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/withdraw-crew-invitation", []byte(`{"invitationId":"`+sector.ID+`"}`)); status != http.StatusNoContent {
		t.Fatalf("withdrawing the sector invitation: status %d: %s", status, body)
	}
	if status, withdrawnAtAnvil := sectorInvitation("anvil"); status != http.StatusOK || cell[bool](t, withdrawnAtAnvil, "pending") {
		t.Errorf("the withdrawn invitation in Anvil's list: status %d, row %v, want it listed and no longer pending", status, withdrawnAtAnvil)
	}

	// A message that cannot be sent: the method still answers, the answer says the
	// message did not go, and the invitation is withdrawn.
	s.mail.Fail(errors.New("relay down"))
	unsent := inviteCrew(ctx, t, hq, `{"email":"lost@example.com","roles":["ClientBrowser"]}`)
	s.mail.Fail(nil)
	if unsent.Sent {
		t.Errorf("the answer of an invitation whose message failed says it went: %+v", unsent)
	}
	if !consumed(unsent.ID) {
		t.Error("the invitation whose message failed still stands, want it withdrawn")
	}

	// A withdrawal: the link is dead, a second one finds nothing, and a link that
	// verifies an address is not an invitation and is left as it is.
	withdrawn := inviteCrew(ctx, t, hq, `{"email":"regret@example.com","roles":["ClientBrowser"]}`)
	_, deadToken := lastMailTo(t, s, "regret@example.com", "invitation")
	withdraw := func(id string) (int, []byte) {
		return hq.do(ctx, http.MethodPost, consoleAPI+"/withdraw-crew-invitation", []byte(`{"invitationId":"`+id+`"}`))
	}
	if status, body := cadet.do(ctx, http.MethodPost, consoleAPI+"/withdraw-crew-invitation", []byte(`{"invitationId":"`+withdrawn.ID+`"}`)); status != http.StatusForbidden {
		t.Errorf("the cadet's withdrawal: status %d, want 403: %s", status, body)
	}
	status, body = withdraw(withdrawn.ID)
	assertStatus(t, status, http.StatusNoContent, body)
	if !consumed(withdrawn.ID) {
		t.Error("the withdrawn invitation still stands")
	}
	other := newBrowser(t, s, consoleAPI)
	if status, body := other.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the dead link: status %d: %s", status, body)
	}
	if status, body := other.do(ctx, http.MethodPost, consoleAPI+"/user/invitation", jsonBody(t, map[string]string{"token": deadToken})); status != http.StatusNotFound {
		t.Errorf("opening a withdrawn invitation: status %d, want 404: %s", status, body)
	}
	if status, body := withdraw(withdrawn.ID); status != http.StatusNotFound {
		t.Errorf("a second withdrawal: status %d, want 404: %s", status, body)
	}
	verification := impulse.Must(impulse.NewUUID())
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("CrewSessionMailLinks", map[string]any{
		"Id": verification.String(), "TokenHash": []byte(strings.Repeat("v", 32)), "Kind": "verification", "Email": "governor@example.com",
		"UserId": governorID, "CreatedAt": spanner.CommitTimestamp, "ExpiresAt": time.Now().Add(time.Hour),
	})}); err != nil {
		t.Fatalf("writing a verification link: %v", err)
	}
	if status, body := withdraw(verification.String()); status != http.StatusNotFound {
		t.Errorf("withdrawing a verification link as an invitation: status %d, want 404: %s", status, body)
	}
	if consumed(verification.String()) {
		t.Error("the verification link was consumed by the withdrawal of an invitation")
	}

	// In a transaction of the application's own: an invitation beside a write that
	// fails is never written and never mailed.
	identityClient := s.crew.Identity()
	var result *crewidentity.InviteCrewUserResult
	err := resource.NewSpannerClient(s.db.Client).ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		if result, err = (&crewidentity.InviteCrewUser{Email: "never@example.com", Roles: []string{"ClientBrowser"}}).Execute(ctx, txn, identityClient); err != nil {
			return err
		}

		return errors.New("the application's own write failed")
	})
	if err == nil {
		t.Fatal("a failing transaction committed")
	}
	if _, err := s.db.Single().ReadRow(ctx, "CrewSessionMailLinks", spanner.Key{result.ID.String()}, []string{"Id"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("the invitation of a transaction that rolled back: err = %v, want not found", err)
	}
	if result.Sent {
		t.Error("the answer of a transaction that rolled back says the message went")
	}
	for _, message := range s.mail.Messages() {
		if message.To == "never@example.com" {
			t.Errorf("a transaction that rolled back mailed its invitation: %q", message.Subject)
		}
	}
	// And one that commits: the answer is read once ExecuteFunc has returned.
	err = resource.NewSpannerClient(s.db.Client).ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		result, err = (&crewidentity.InviteCrewUser{Email: "aboard@example.com", Roles: []string{"ClientBrowser"}}).Execute(ctx, txn, identityClient)
		if err == nil && result.Sent {
			t.Error("the answer says the message went before the transaction committed")
		}

		return err
	})
	if err != nil {
		t.Fatalf("an invitation in a transaction of the application's own: %v", err)
	}
	if !result.Sent {
		t.Error("the answer of a committed invitation does not say the message went")
	}
	if _, token := lastMailTo(t, s, "aboard@example.com", "invitation"); token == "" {
		t.Error("the committed invitation's message carries no token")
	}
}
