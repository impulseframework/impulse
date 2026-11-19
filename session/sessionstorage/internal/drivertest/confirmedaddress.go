package drivertest

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// testAddressConfirmedFirst runs the flow's hold for an unconfirmed address
// (signinflow.addressHold) against the row as each driver reads it in the sign-in's
// transaction (UserIn). A stranger an open method admits through a directory that does
// not vouch for their address is given an account and no session: the sign-in waits for
// the code. It waits again while the row holds no address, and while it holds one
// nobody confirmed; once the row holds a confirmed address the same identity signs in.
// A directory that vouches for the address is not held at all.
func testAddressConfirmedFirst(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Accounts: Open, ConfirmAddress: true})
	pat := entra("entra-pat", "pat@lakeside.edu")

	held := func(what string) *signin.PendingSignInError {
		t.Helper()
		_, _, err := signIn(ctx, in, pat, sessioninfo.ReasonLogin)
		var wait *signin.PendingSignInError
		if !errors.As(err, &wait) || wait.Reason != sessioninfo.PendingMFA || !wait.UserID.Valid {
			t.Fatalf("%s: sign-in error = %v, want the sign-in held for the code that confirms the address", what, err)
		}
		if len(wait.Factors) != 1 || wait.Factors[0] != sessioninfo.MethodEmailCode {
			t.Errorf("%s: the wait accepts %v, want the mailed code alone", what, wait.Factors)
		}

		return wait
	}

	// The first sign-in makes the account, which holds no address, and no session.
	wait := held("an account that holds no address")
	user := mustUser(ctx, t, in.Driver, wait.UserID.UUID)
	assertEmail(t, "User() after the held sign-in", user, nil, false)

	// An address on the row that nobody confirmed changes nothing.
	if err := in.Driver.SetUserEmail(ctx, user.ID, strPtr("pat@lakeside.edu")); err != nil {
		t.Fatalf("SetUserEmail() error = %v", err)
	}
	if again := held("an account that holds an unconfirmed address"); again.UserID.UUID != user.ID {
		t.Errorf("the second held sign-in names user %s, want the account the first made, %s", again.UserID.UUID, user.ID)
	}

	// The code's completion writes the address confirmed (VerifyUserEmail): from then
	// on the person signs in.
	if err := in.Driver.VerifyUserEmail(ctx, user.ID, "pat@lakeside.edu"); err != nil {
		t.Fatalf("VerifyUserEmail() error = %v", err)
	}
	if _, _, err := signIn(ctx, in, pat, sessioninfo.ReasonLogin); err != nil {
		t.Errorf("sign-in of an account that holds a confirmed address error = %v, want a session", err)
	}

	// A directory that vouches for the address is not held: the address is claimed as
	// the session starts.
	if _, _, err := signIn(ctx, in, google("google-sam", "sam@lakeside.edu"), sessioninfo.ReasonLogin); err != nil {
		t.Errorf("first sign-in through a directory that vouches for the address error = %v, want a session", err)
	}
}
