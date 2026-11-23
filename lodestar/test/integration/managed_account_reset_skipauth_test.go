//go:build skipAuth

package integration

// managed_account_reset_skipauth_test: the reset link of managed_account_test on the
// served stack, which mails it.

import (
	"net/http"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/securehash"
)

// TestManagedAccountResetLink holds the reset link to the person's row on the served
// stack, which mails it: a person who signs in through no sector's directory and holds
// a role in one sector alone is that sector's to disable and enable; once they hold a
// role in a second sector, neither sector disables them, and each still sends them the
// reset link, one mail to their address each.
func TestManagedAccountResetLink(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	// The stack names the sectors to the crew auth, which says the sector in the mail.
	s := newServedWith(ctx, t, servedOptions{namesTenants: true})

	// The grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "SectorMarshal", "Execute", "SendCrewSectorUserPasswordResetLink", "managedHere = true OR managedByAnotherDirectory = false")

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")

	// rue holds a password and an address she confirmed, and the Cadet's role at Anvil.
	const address = "rue@example.com"
	rue := string(makeCrewAccountAt(ctx, t, s.db, "rue", address))
	hash, err := securehash.New(securehash.Bcrypt()).Hash("rue keeps a long passphrase")
	if err != nil {
		t.Fatalf("hashing rue's password: %v", err)
	}
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{spanner.UpdateMap("CrewSessionUsers", map[string]any{
		"Id": rue, "PasswordHash": hash, "EmailVerifiedAt": spanner.CommitTimestamp,
	})}); err != nil {
		t.Fatalf("writing rue's password and confirmed address: %v", err)
	}
	give := func(sector string) {
		t.Helper()
		if status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/assign-crew-role", []byte(`{"userId":"`+rue+`","role":"Cadet","domain":"`+sector+`"}`)); status != http.StatusNoContent {
			t.Fatalf("giving rue the Cadet's role in %s: status %d: %s", sector, status, body)
		}
	}
	give(anvil)
	target := []byte(`{"userId":"` + rue + `"}`)
	disabled := func() bool {
		return readColumn[bool](ctx, t, s.db, "CrewSessionUsers", spanner.Key{rue}, "Disabled")
	}

	// Anvil is the one sector rue holds a role in, so it manages the account.
	status, body := marshal.do(ctx, http.MethodPost, sectorPath(anvil, "disable-crew-sector-user"), target)
	assertStatus(t, status, http.StatusNoContent, body)
	if !disabled() {
		t.Error("rue is not disabled after Anvil's call")
	}
	status, body = marshal.do(ctx, http.MethodPost, sectorPath(anvil, "enable-crew-sector-user"), target)
	assertStatus(t, status, http.StatusNoContent, body)
	if disabled() {
		t.Error("rue is still disabled after Anvil's call")
	}

	// Once rue holds a role at Bastion too, no sector manages the account.
	give(bastion)
	for _, by := range []struct {
		name   string
		caller *browser
		sector string
	}{
		{name: "Anvil's marshal", caller: marshal, sector: anvil},
		{name: "the governor at Bastion", caller: hq, sector: bastion},
	} {
		if status, body := by.caller.do(ctx, http.MethodPost, sectorPath(by.sector, "disable-crew-sector-user"), target); status != http.StatusForbidden {
			t.Errorf("%s disabling rue: status %d, want 403: %s", by.name, status, body)
		}
		if disabled() {
			t.Fatalf("rue is disabled after %s's refused call", by.name)
		}
		sentBefore := len(s.mail.Messages())
		status, body := by.caller.do(ctx, http.MethodPost, sectorPath(by.sector, "send-crew-sector-user-password-reset-link"), target)
		if sent, reason := mailingAnswer(t, status, body); !sent {
			t.Errorf("%s's reset link to rue was not sent: %s", by.name, reason)
		}
		if n := len(mailedSince(s, sentBefore, address)); n != 1 {
			t.Errorf("%s's reset link mailed rue %d messages, want 1", by.name, n)
		}
	}
}
