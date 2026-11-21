package drivertest

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The write of the address a mailed sign-in code proved is taken only while the user
// still holds the address the code was checked against (VerifyUserEmailIfHeld). These
// cases pin it on both store shapes: written while the row holds what was held, and
// otherwise a Conflict with nothing written, so a change an administrator made in
// between stands.

func testVerifyUserEmailIfHeld(ctx context.Context, t *testing.T, h *AuthHarness) {
	t.Run("a store with an address column", func(t *testing.T) {
		testVerifyIfHeldWithAddressColumn(ctx, t, h)
	})
	t.Run("a store whose usernames are the addresses", func(t *testing.T) {
		testVerifyIfHeldUsernameIsEmail(ctx, t, h)
	})
}

func testVerifyIfHeldWithAddressColumn(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	pat := createUserHolding(ctx, t, in.Driver, "pat", "Pat@Lakeside.edu")
	sam := createUser(ctx, t, in.Driver, "sam")

	// Written while the row holds what was held, verified at the database's time: the
	// address the code was checked against, here in another spelling of its letters, or
	// none.
	for _, tt := range []struct {
		name  string
		id    impulse.UUID
		held  *string
		email string
	}{
		{name: "the address that was held", id: pat.ID, held: strPtr("Pat@Lakeside.edu"), email: "pat@lakeside.edu"},
		{name: "no address, as was held", id: sam.ID, email: "sam@lakeside.edu"},
	} {
		w := startWindow(h.ClockLead)
		if err := in.Driver.VerifyUserEmailIfHeld(ctx, tt.id, tt.held, tt.email); err != nil {
			t.Fatalf("VerifyUserEmailIfHeld() while the row holds %s error = %v", tt.name, err)
		}
		w.done()
		written := mustUser(ctx, t, in.Driver, tt.id)
		assertEmail(t, "User() after VerifyUserEmailIfHeld() while the row holds "+tt.name, written, strPtr(tt.email), true)
		w.assertStamped(t, "User().EmailVerifiedAt", *written.EmailVerifiedAt)
	}

	// Refused with a Conflict, and nothing written, once the row holds another address
	// than was held, one where none was held or none where one was held, or the user is
	// gone; an address another user holds is refused as VerifyUserEmail refuses it.
	alex := createUserHolding(ctx, t, in.Driver, "alex", "alex@lakeside.edu")
	bo := createUser(ctx, t, in.Driver, "bo")
	gone := impulse.Must(impulse.NewUUID())
	for _, tt := range []struct {
		name  string
		id    impulse.UUID
		held  *string
		email string
		// keeps is the address the row still holds, unconfirmed, nil for none.
		keeps *string
	}{
		{name: "the row holds another address", id: alex.ID, held: strPtr("alex.old@lakeside.edu"), email: "alex.old@lakeside.edu", keeps: strPtr("alex@lakeside.edu")},
		{name: "the row holds an address where none was held", id: alex.ID, email: "alex.new@lakeside.edu", keeps: strPtr("alex@lakeside.edu")},
		{name: "the row holds none where one was held", id: bo.ID, held: strPtr("bo@lakeside.edu"), email: "bo@lakeside.edu"},
		{name: "another user holds the address", id: bo.ID, email: "ALEX@lakeside.edu"},
		{name: "the user is gone", id: gone, email: "gone@lakeside.edu"},
	} {
		if err := in.Driver.VerifyUserEmailIfHeld(ctx, tt.id, tt.held, tt.email); !httpio.HasConflict(err) {
			t.Errorf("VerifyUserEmailIfHeld() when %s error = %v, want Conflict", tt.name, err)
		}
		if tt.id != gone {
			assertEmail(t, "User() after the refused write when "+tt.name, mustUser(ctx, t, in.Driver, tt.id), tt.keeps, false)
		}
	}
}

func testVerifyIfHeldUsernameIsEmail(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, AuthUsernameIsEmail, AuthConfig{Auth: true})
	const named = "Pat@lakeside.edu"
	pat := createUser(ctx, t, in.Driver, named)
	createUser(ctx, t, in.Driver, "alex@lakeside.edu")
	sessionID, err := in.Driver.InsertSession(ctx, newInsertSession(pat.Username), &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: pat.Username, UserID: pat.ID})
	if err != nil {
		t.Fatalf("InsertSession() error = %v", err)
	}

	// Refused with a Conflict, renaming nothing, once the row holds another address than
	// was held or one where none was held (a row here always holds its username), or the
	// user is gone; an address another user holds is refused as VerifyUserEmail refuses
	// it.
	for _, tt := range []struct {
		name  string
		id    impulse.UUID
		held  *string
		email string
	}{
		{name: "the row holds another address", id: pat.ID, held: strPtr("pat.old@lakeside.edu"), email: "pat.lee@lakeside.edu"},
		{name: "the row holds an address where none was held", id: pat.ID, email: "pat.lee@lakeside.edu"},
		{name: "another user holds the address", id: pat.ID, held: strPtr(named), email: "ALEX@lakeside.edu"},
		{name: "the user is gone", id: impulse.Must(impulse.NewUUID()), held: strPtr("gone@lakeside.edu"), email: "gone.new@lakeside.edu"},
	} {
		if err := in.Driver.VerifyUserEmailIfHeld(ctx, tt.id, tt.held, tt.email); !httpio.HasConflict(err) {
			t.Errorf("VerifyUserEmailIfHeld() when %s error = %v, want Conflict", tt.name, err)
		}
	}
	kept := mustUser(ctx, t, in.Driver, pat.ID)
	if kept.Username != named {
		t.Errorf("User().Username = %q after the refused writes, want %q: nothing renamed", kept.Username, named)
	}
	assertEmail(t, "User() after the refused writes", kept, strPtr(named), false)
	if sess := mustSession(ctx, t, in.Driver, sessionID); sess.Username != named {
		t.Errorf("Session().Username = %q after the refused writes, want %q: nothing renamed", sess.Username, named)
	}

	// Written while the row holds what was held: the user and its live sessions are
	// renamed, and the address verified at the database's time.
	w := startWindow(h.ClockLead)
	if err := in.Driver.VerifyUserEmailIfHeld(ctx, pat.ID, strPtr(named), "pat.lee@lakeside.edu"); err != nil {
		t.Fatalf("VerifyUserEmailIfHeld() while the row holds the address that was held error = %v", err)
	}
	w.done()
	renamed := mustUser(ctx, t, in.Driver, pat.ID)
	if renamed.Username != "pat.lee@lakeside.edu" {
		t.Errorf("User().Username = %q after the write, want the address", renamed.Username)
	}
	assertEmail(t, "User() after the write", renamed, strPtr("pat.lee@lakeside.edu"), true)
	w.assertStamped(t, "User().EmailVerifiedAt", *renamed.EmailVerifiedAt)
	if sess := mustSession(ctx, t, in.Driver, sessionID); sess.Username != "pat.lee@lakeside.edu" {
		t.Errorf("Session().Username = %q after the write, want the new name", sess.Username)
	}
}

// createUserHolding creates a user holding email, unconfirmed.
func createUserHolding(ctx context.Context, t *testing.T, d AuthDriver, username, email string) *dbtype.SessionUser {
	t.Helper()

	user, err := d.CreateUser(ctx, &dbtype.InsertSessionUser{Username: username, Email: strPtr(email)}, nil)
	if err != nil {
		t.Fatalf("CreateUser(%q, %q) error = %v", username, email, err)
	}

	return user
}
