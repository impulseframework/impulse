package session

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_codeStands_foldsOnlyASCIICase holds a mailed sign-in code to the address the
// account holds in the case of its ASCII letters alone. Unicode's case folding takes the
// Kelvin sign (U+212A) for k, so under it a code mailed to an address written with one
// would be good for an account that holds the ASCII address, another mailbox.
func TestAuth_codeStands_foldsOnlyASCIICase(t *testing.T) {
	t.Parallel()

	const holds = "kim@kirkwood.edu"
	tests := []struct {
		name     string
		mailedTo string
		want     bool
	}{
		{name: "the address the account holds", mailedTo: holds, want: true},
		{name: "the address in another ASCII case", mailedTo: "Kim@Kirkwood.EDU", want: true},
		{name: "a name written with the Kelvin sign", mailedTo: "Kim@kirkwood.edu"},
		{name: "a domain written with the Kelvin sign", mailedTo: "kim@Kirkwood.edu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f, _, _ := newMailFixture(t, gomock.NewController(t))
			user := &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "kim", Email: strPtr(holds)}
			got, err := f.auth.codeStands(context.Background(), nil, user, tt.mailedTo)
			if err != nil {
				t.Fatalf("codeStands() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("codeStands() = %v for a code mailed to %q on an account that holds %q, want %v", got, tt.mailedTo, holds, tt.want)
			}
		})
	}
}

// TestResettable_foldsOnlyASCIICase holds a password reset link to the confirmed address
// of its account in the case of its ASCII letters alone: a link mailed to an address
// written with the Kelvin sign (U+212A) resets nothing on the account that holds the
// ASCII address.
func TestResettable_foldsOnlyASCIICase(t *testing.T) {
	t.Parallel()

	const holds = "kim@kirkwood.edu"
	confirmed := time.Now().Add(-time.Hour)
	tests := []struct {
		name     string
		mailedTo string
		want     bool
	}{
		{name: "the address the account holds confirmed", mailedTo: holds, want: true},
		{name: "the address in another ASCII case", mailedTo: "Kim@Kirkwood.EDU", want: true},
		{name: "a name written with the Kelvin sign", mailedTo: "Kim@kirkwood.edu"},
		{name: "a domain written with the Kelvin sign", mailedTo: "kim@Kirkwood.edu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			user := &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "kim", PasswordHash: hashed(t, "pw"), Email: strPtr(holds), EmailVerifiedAt: &confirmed}
			if got := resettable(user, tt.mailedTo); got != tt.want {
				t.Errorf("resettable() = %v for a link mailed to %q on an account that holds %q, want %v", got, tt.mailedTo, holds, tt.want)
			}
		})
	}
}

// TestAuth_claimCodeAddress_tellsARenameByTheKelvinSign holds the rename hook of a store
// whose usernames are the addresses to the case of ASCII letters alone: a sign-in code
// that confirms an address written with the Kelvin sign (U+212A) on an account named
// with the ASCII one renames the user, and the application is told.
func TestAuth_claimCodeAddress_tellsARenameByTheKelvinSign(t *testing.T) {
	t.Parallel()

	const named, written = "kim@kirkwood.edu", "\u212Aim@kirkwood.edu"
	userID := impulse.Must(impulse.NewUUID())
	f, _, _ := newMailFixture(t, gomock.NewController(t), UsernameIsEmail())
	user := &sessionstorage.SessionUser{ID: userID, Username: named, Email: strPtr(written)}
	f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil)
	f.store.EXPECT().VerifyUserEmailIfHeld(gomock.Any(), userID, user.Email, written).Return(nil)
	var renamed []string
	f.auth.SetUserRenamed(func(_ context.Context, _ impulse.UUID, from, to string) error {
		renamed = append(renamed, from+" -> "+to)

		return nil
	})

	if err := f.auth.claimCodeAddress(context.Background(), &pendingState{UserID: impulse.NullUUIDFromUUID(userID)}, user, written); err != nil {
		t.Fatalf("claimCodeAddress() error = %v", err)
	}
	if len(renamed) != 1 || renamed[0] != named+" -> "+written {
		t.Errorf("rename hook calls = %q, want the one rename: the address confirmed is another than the username", renamed)
	}
}

// TestAuth_addressChange_foldsOnlyASCIICase holds an administrator's change of an
// address to the case of ASCII letters alone. An address written with the Kelvin sign
// (U+212A) is another address than the ASCII one the account holds confirmed: the change
// is mailed its link, not dropped as no change, and on a store whose usernames are the
// addresses the link's confirmation renames the user and tells the application, and the
// address it replaced is told.
func TestAuth_addressChange_foldsOnlyASCIICase(t *testing.T) {
	t.Parallel()

	const holds, asked = "kim@lakeside.edu", "\u212Aim@lakeside.edu"
	userID, _ := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t), UsernameIsEmail())
	confirmed := time.Now().Add(-time.Hour)
	user := &sessionstorage.SessionUser{ID: userID, Username: holds, PasswordHash: hashed(t, "pw"), Email: strPtr(holds), EmailVerifiedAt: &confirmed}
	f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()
	links.expectAccounts(f)
	var renamed []string
	f.auth.SetUserRenamed(func(_ context.Context, _ impulse.UUID, from, to string) error {
		renamed = append(renamed, from+" -> "+to)

		return nil
	})

	if err := f.auth.API().RequestAddressChange(context.Background(), userID, asked, AskedByApplication()); err != nil {
		t.Fatalf("RequestAddressChange() error = %v", err)
	}
	msgs := recorder.Messages()
	if len(msgs) != 1 || msgs[0].To != asked {
		t.Fatalf("messages after the request = %d, want one link at the address asked for: it is another address than the one the account holds", len(msgs))
	}
	token := tokenIn(t, msgs[0].Text, "address")
	f.store.EXPECT().VerifyUserEmail(gomock.Any(), userID, asked).Return(nil)
	if rr := f.serve(f.auth.Email().Confirm(), http.MethodPost, "/email/confirm", map[string]string{"token": token}, nil); rr.Code != http.StatusOK {
		t.Fatalf("Confirm() = %d, want 200", rr.Code)
	}
	if len(renamed) != 1 || renamed[0] != holds+" -> "+asked {
		t.Errorf("rename hook calls = %q, want the one rename", renamed)
	}
	if msgs := recorder.Messages(); len(msgs) != 2 || msgs[1].To != holds {
		t.Errorf("messages after the confirmation = %d, want the link and then the notice of the change at the address the account held", len(msgs))
	}
}
