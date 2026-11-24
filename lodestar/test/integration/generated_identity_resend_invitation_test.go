//go:build skipAuth

package integration

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// TestGeneratedIdentityInvitationsSentAgain holds the sending again of an invitation on
// the served stack, with the real session manager and its mail, on both sides: an
// invitation that stands is sent again by the person who presses it, its new link is
// mailed and accepted, and the link mailed before is dead and its row says it was
// withdrawn; an invitation that has expired is sent again and its row is left as it is;
// one that was accepted is refused; and a sector's administrator does not find another
// sector's invitation.
//
// The Sector Marshal sends again an invitation that stands and gives no Marshal's role
// (pending = true AND role != 'SectorMarshal'), as the Marshal withdraws one.
//
// Demonstrates: auth.generated-identity, auth.tenant-methods.
func TestGeneratedIdentityInvitationsSentAgain(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	// The Execute grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "ResendCrewSectorInvitation", "pending = true AND role != 'SectorMarshal'")

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	governorID := crewAccountID(ctx, t, hq, "governor")
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")
	marshalID := crewAccountID(ctx, t, hq, "marshal")

	// ended reads when the link id was consumed and when it was withdrawn.
	ended := func(id string) (consumedAt, withdrawnAt spanner.NullTime) {
		consumedAt = readColumn[spanner.NullTime](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{id}, "ConsumedAt")
		withdrawnAt = readColumn[spanner.NullTime](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{id}, "WithdrawnAt")

		return consumedAt, withdrawnAt
	}
	// resend sends the invitation id again at the path and answers the new one.
	resend := func(b *browser, path, id string) invited {
		t.Helper()

		status, body := b.do(ctx, http.MethodPost, path, []byte(`{"invitationId":"`+id+`"}`))
		assertStatus(t, status, http.StatusOK, body)
		var answer invited
		if err := json.Unmarshal(body, &answer); err != nil || answer.ID == "" || !answer.Sent {
			t.Fatalf("sending the invitation again answered %s (%v), want the new invitation's id and the word that its message went", body, err)
		}
		if answer.ID == id {
			t.Fatalf("sending the invitation again answered its own id %s, want a new invitation", id)
		}

		return answer
	}
	// accept follows the link on a browser of its own and signs up under username.
	accept := func(token, username string) {
		t.Helper()

		stranger := newBrowser(t, s, consoleAPI)
		openInvitation(ctx, t, stranger, token)
		if status, body := stranger.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": username, "password": signUpPassword})); status != http.StatusOK {
			t.Fatalf("signing up at the invitation sent again: status %d: %s", status, body)
		}
		stranger.secondFactor(ctx, username)
	}
	// deadLink holds that the token's link opens no invitation, in the session
	// library's sentence for a dead link.
	deadLink := func(token string) {
		t.Helper()

		other := newBrowser(t, s, consoleAPI)
		if status, body := other.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
			t.Fatalf("GET session before the old link: status %d: %s", status, body)
		}
		status, body := other.do(ctx, http.MethodPost, consoleAPI+"/user/invitation", jsonBody(t, map[string]string{"token": token}))
		if status != http.StatusNotFound || refusalMessage(t, body) != "That link has expired or was already used. Ask for a new one." {
			t.Errorf("opening the link mailed before the invitation was sent again: status %d, %s; want 404 saying the link is dead", status, body)
		}
	}

	// The whole application's side: an invitation that stands, sent again by the
	// governor, with its name and its role.
	const email = "again@example.com"
	first := inviteCrew(ctx, t, hq, `{"email":"`+email+`","name":"Ada Again","roles":["ClientBrowser"]}`)
	_, oldToken := lastMailTo(t, s, email, "invitation")
	again := resend(hq, consoleAPI+"/resend-crew-invitation", first.ID)
	_, newToken := lastMailTo(t, s, email, "invitation")
	if newToken == oldToken {
		t.Fatal("the invitation sent again mailed the link mailed before, want a new one")
	}
	if consumedAt, withdrawnAt := ended(first.ID); !withdrawnAt.Valid || !withdrawnAt.Time.Equal(consumedAt.Time) {
		t.Errorf("the invitation sent again: WithdrawnAt = %v and ConsumedAt = %v, want one time in both: it was taken back", withdrawnAt, consumedAt)
	}
	if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{again.ID}, "InvitedByUserId"); got.StringVal != governorID {
		t.Errorf("the new invitation's sender = %v, want the governor (%s)", got, governorID)
	}
	if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{again.ID}, "Name"); got.StringVal != "Ada Again" {
		t.Errorf("the new invitation's name = %v, want the one the first carried", got)
	}
	deadLink(oldToken)
	accept(newToken, "again")
	if got, want := crewHolds(t, s.db, "again"), []string{"global/ ClientBrowser"}; !slices.Equal(got, want) {
		t.Errorf("the person who accepted the new link holds %v, want %v", got, want)
	}

	// An invitation that was accepted is refused.
	status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/resend-crew-invitation", []byte(`{"invitationId":"`+again.ID+`"}`))
	if status != http.StatusConflict || refusalMessage(t, body) != "This invitation was accepted. There is nothing to send again." {
		t.Errorf("sending an accepted invitation again: status %d, %s; want 409 saying there is nothing to send", status, body)
	}

	// An invitation that has expired is sent again, and its row is left as it is.
	late := inviteCrew(ctx, t, hq, `{"email":"late@example.com","roles":["ClientBrowser"]}`)
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{spanner.UpdateMap("CrewSessionMailLinks", map[string]any{
		"Id": late.ID, "ExpiresAt": time.Now().Add(-time.Hour),
	})}); err != nil {
		t.Fatalf("moving the invitation's expiry into the past: %v", err)
	}
	resend(hq, consoleAPI+"/resend-crew-invitation", late.ID)
	if consumedAt, withdrawnAt := ended(late.ID); consumedAt.Valid || withdrawnAt.Valid {
		t.Errorf("the expired invitation sent again: ConsumedAt = %v, WithdrawnAt = %v; want both null: it is left as it is", consumedAt, withdrawnAt)
	}

	// A sector's side: the Marshal sends again an invitation of Anvil that stands.
	const recruit = "recruit.again@example.com"
	status, body = marshal.do(ctx, http.MethodPost, sectorPath(anvil, "invite-crew-sector-user"), []byte(`{"email":"`+recruit+`","roles":["Cadet"]}`))
	assertStatus(t, status, http.StatusOK, body)
	var atAnvil invited
	if err := json.Unmarshal(body, &atAnvil); err != nil || atAnvil.ID == "" {
		t.Fatalf("invite-crew-sector-user answered %s (%v)", body, err)
	}
	_, oldToken = lastMailTo(t, s, recruit, "invitation")
	againAtAnvil := resend(marshal, sectorPath(anvil, "resend-crew-sector-invitation"), atAnvil.ID)
	_, newToken = lastMailTo(t, s, recruit, "invitation")
	if consumedAt, withdrawnAt := ended(atAnvil.ID); !withdrawnAt.Valid || !withdrawnAt.Time.Equal(consumedAt.Time) {
		t.Errorf("Anvil's invitation sent again: WithdrawnAt = %v and ConsumedAt = %v, want one time in both", withdrawnAt, consumedAt)
	}
	if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{againAtAnvil.ID}, "InvitedByUserId"); got.StringVal != marshalID {
		t.Errorf("Anvil's new invitation's sender = %v, want the Marshal (%s)", got, marshalID)
	}
	if got := readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionMailLinks", spanner.Key{againAtAnvil.ID}, "TenantId"); got.StringVal != anvil {
		t.Errorf("Anvil's new invitation's tenant = %v, want %s", got, anvil)
	}
	deadLink(oldToken)
	accept(newToken, "recruitagain")
	if got, want := crewHolds(t, s.db, "recruitagain"), []string{"domain/" + anvil + " Cadet"}; !slices.Equal(got, want) {
		t.Errorf("the person who accepted Anvil's new link holds %v, want %v", got, want)
	}

	// Another sector's invitation is no row of Anvil's list: the Marshal does not find it,
	// and it stands.
	atBastion := inviteCrew(ctx, t, hq, `{"email":"elsewhere.again@example.com","roles":["Pilot"],"domain":"`+bastion+`"}`)
	if status, body := marshal.do(ctx, http.MethodPost, sectorPath(anvil, "resend-crew-sector-invitation"), []byte(`{"invitationId":"`+atBastion.ID+`"}`)); status != http.StatusNotFound {
		t.Errorf("sending Bastion's invitation again at Anvil: status %d, want 404: %s", status, body)
	}
	if consumedAt, _ := ended(atBastion.ID); consumedAt.Valid {
		t.Error("Bastion's invitation was ended by a request made at Anvil")
	}
}
