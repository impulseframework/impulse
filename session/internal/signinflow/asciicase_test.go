package signinflow

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestSameAddress_foldsOnlyASCIICase holds the comparison of two addresses to the case
// of their ASCII letters. Unicode's case folding takes the Kelvin sign (U+212A) for k,
// so under it an address written with one reads as the ASCII address, which it is not:
// it is another mailbox, and nothing an address decides is given to it.
func TestSameAddress_foldsOnlyASCIICase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "the same address", a: "kim@kirkwood.edu", b: "kim@kirkwood.edu", want: true},
		{name: "ASCII letters in another case, with space around", a: " Kim@Kirkwood.EDU ", b: "kim@kirkwood.edu", want: true},
		{name: "a name written with the Kelvin sign is another address", a: "Kim@kirkwood.edu", b: "kim@kirkwood.edu"},
		{name: "a domain written with the Kelvin sign is another address", a: "kim@Kirkwood.edu", b: "kim@kirkwood.edu"},
		{name: "an address written with the Kelvin sign is itself in another ASCII case", a: "Kim@Kirkwood.edu", b: "Kim@kirkwood.EDU", want: true},
		{name: "an empty address is nobody's", a: "", b: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := sameAddress(tt.a, tt.b); got != tt.want {
				t.Errorf("sameAddress(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			if got := sameAddress(tt.b, tt.a); got != tt.want {
				t.Errorf("sameAddress(%q, %q) = %v, want %v", tt.b, tt.a, got, tt.want)
			}
		})
	}
}

// emailNames is the in-memory driver on a store whose usernames are its people's
// addresses.
type emailNames struct {
	*memDriver
}

// UsernameIsEmail reports that the store's usernames are the addresses.
func (emailNames) UsernameIsEmail() bool {
	return true
}

// TestInvitedAddress_foldsOnlyASCIICase holds the address a new user is created with,
// on a store whose usernames are the addresses, verified only when the username is the
// address the invitation went to: in another ASCII case it is, and written with the
// Kelvin sign (U+212A) it is another address, which the invitation's mail never reached.
func TestInvitedAddress_foldsOnlyASCIICase(t *testing.T) {
	t.Parallel()

	const invited = "kim@kirkwood.edu"
	tests := []struct {
		name     string
		username string
		want     bool
	}{
		{name: "the invited address", username: invited, want: true},
		{name: "the invited address in another ASCII case", username: " Kim@Kirkwood.EDU", want: true},
		{name: "a name written with the Kelvin sign", username: "Kim@kirkwood.edu"},
		{name: "a domain written with the Kelvin sign", username: "kim@Kirkwood.edu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &sessioninfo.NewSessionRequest{Invitation: &sessioninfo.Invitation{Email: invited}}
			insert := &dbtype.InsertSessionUser{Username: tt.username}
			if err := invitedAddress[*memTx](context.Background(), emailNames{&memDriver{}}, nil, req, insert); err != nil {
				t.Fatalf("invitedAddress() error = %v", err)
			}
			if insert.EmailVerified != tt.want {
				t.Errorf("invitedAddress() verified = %v for the username %q, want %v", insert.EmailVerified, tt.username, tt.want)
			}
		})
	}
}
