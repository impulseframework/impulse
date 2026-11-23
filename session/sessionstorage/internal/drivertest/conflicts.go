package drivertest

import (
	"context"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

// testConflictsInWords holds a driver to what it answers for a sign-in identity that is
// linked already: a Conflict in one plain sentence, the same on both databases, that
// carries neither the directory's connection nor the identity's subject, which the
// refusal's cause keeps for the server's log.
func testConflictsInWords(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	identity := lakeside("idp_linked_once")
	pat := createUser(ctx, t, in.Driver, "pat")
	if _, err := in.Driver.LinkIdentity(ctx, pat.ID, identity, ""); err != nil {
		t.Fatalf("LinkIdentity() of an identity no account holds error = %v", err)
	}
	sam := createUser(ctx, t, in.Driver, "sam")

	_, err := in.Driver.LinkIdentity(ctx, sam.ID, identity, "")
	if !httpio.HasConflict(err) {
		t.Fatalf("LinkIdentity() of an identity linked already error = %v, want a Conflict saying %q", err, dbtype.SignInIdentityAlreadyLinkedMessage)
	}
	messages := httpio.Messages(err)
	if len(messages) != 1 || messages[0] != dbtype.SignInIdentityAlreadyLinkedMessage {
		t.Errorf("LinkIdentity() of an identity linked already: the person is told %q, want only %q", messages, dbtype.SignInIdentityAlreadyLinkedMessage)
	}
	for _, word := range []string{identity.Connection, identity.Subject} {
		if strings.Contains(strings.Join(messages, " "), word) {
			t.Errorf("LinkIdentity() of an identity linked already: the person is told %q, which carries %q", messages, word)
		}
		if !strings.Contains(err.Error(), word) {
			t.Errorf("LinkIdentity() of an identity linked already: the error's text, which the server's log prints, is %q: it does not carry %q", err.Error(), word)
		}
	}
}
