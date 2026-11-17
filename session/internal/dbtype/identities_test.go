package dbtype

import (
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestUnlinkKeepsSignIn pins the judgment both drivers hold the unlink of a
// password-less user's identity to: it goes while another of the user's identities is
// one the caller judges a means of sign-in, and is refused otherwise, in words that
// say whether the user has no other identity or none that signs them in. The unlinked
// identity itself keeps nobody's way in, and a caller that judges nothing keeps every
// identity.
func TestUnlinkKeepsSignIn(t *testing.T) {
	t.Parallel()

	link := func(connection string) *SessionIdentity {
		return &SessionIdentity{ID: impulse.Must(impulse.NewUUID()), Method: sessioninfo.MethodWorkOS, Connection: connection}
	}
	counts, alsoCounts, stale, alsoStale := link("open"), link("open"), link("closed"), link("closed")
	open := func(method sessioninfo.AuthMethod, connection string) bool {
		return method == sessioninfo.MethodWorkOS && connection == "open"
	}
	const (
		noOther       = "the user has no other means of sign-in: link another directory sign-in first, or give the user a password"
		noOtherCounts = "none of the other identities linked to the user can sign them in any more, so this one is their only means of sign-in: link a directory sign-in the login page offers first, or give the user a password"
	)

	tests := []struct {
		name     string
		links    []*SessionIdentity
		unlinked *SessionIdentity
		signsIn  func(method sessioninfo.AuthMethod, connection string) bool
		// want is the refusal's message, empty for an unlink that goes through.
		want string
	}{
		{name: "another identity that signs the user in remains", links: []*SessionIdentity{counts, alsoCounts}, unlinked: counts, signsIn: open},
		{name: "an identity that signs nobody in goes from beside one that counts", links: []*SessionIdentity{stale, counts}, unlinked: stale, signsIn: open},
		{name: "one that counts is enough, whatever the others are", links: []*SessionIdentity{counts, stale, alsoCounts}, unlinked: counts, signsIn: open},
		{name: "the user's one identity stays", links: []*SessionIdentity{counts}, unlinked: counts, signsIn: open, want: noOther},
		{name: "the last identity that counts stays while one that signs nobody in remains", links: []*SessionIdentity{counts, stale}, unlinked: counts, signsIn: open, want: noOtherCounts},
		{name: "identities that sign nobody in keep nobody's way in, however many", links: []*SessionIdentity{stale, alsoStale}, unlinked: stale, signsIn: open, want: noOtherCounts},
		{name: "a caller that judges nothing keeps every identity", links: []*SessionIdentity{counts, alsoCounts}, unlinked: counts, want: noOtherCounts},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := UnlinkKeepsSignIn(tt.links, tt.unlinked.ID, tt.signsIn)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("UnlinkKeepsSignIn() error = %v, want the unlink to go through", err)
				}

				return
			}
			if !errors.Is(err, ErrLastSignInMethod) || !httpio.HasConflict(err) || httpio.Message(err) != tt.want {
				t.Errorf("UnlinkKeepsSignIn() error = %v, want ErrLastSignInMethod (Conflict) saying %q", err, tt.want)
			}
		})
	}
}
