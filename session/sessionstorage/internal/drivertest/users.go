package drivertest

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// testUserAccounts pins the user row as the management of an auth's users reads it: the
// password change mark travels with the insert, CreatedAt is the database's time at
// the insert, LastSignInAt is nil before the first sign-in, and the users list orders
// by username without regard to case, each user as stored.
func testUserAccounts(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, AuthEvents: true})
	hasher := securehash.New(securehash.Argon2())
	temporary, err := hasher.Hash("temporary")
	if err != nil {
		t.Fatal(err)
	}

	// An operator creates a user with a temporary password: the mark is stored with the
	// insert, CreatedAt is the commit's time, and there is no sign-in yet.
	w := startWindow(h.ClockLead)
	pat, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "Pat@lakeside.edu", PasswordHash: temporary, PasswordChangeRequired: true}, nil)
	w.done()
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if !pat.PasswordChangeRequired || pat.LastSignInAt != nil || pat.Username != "Pat@lakeside.edu" {
		t.Errorf("CreateUser() = %+v, want the mark, the username as given and no sign-in", pat)
	}
	w.assertStamped(t, "CreateUser().CreatedAt", pat.CreatedAt)
	alex := createUser(ctx, t, in.Driver, "alex@lakeside.edu")
	if alex.PasswordChangeRequired || alex.PasswordHash != nil {
		t.Errorf("CreateUser() of a password-less user = %+v, want no mark", alex)
	}

	// The list is every user by username, without regard to case, as stored.
	users, err := in.Driver.Users(ctx)
	if err != nil {
		t.Fatalf("Users() error = %v", err)
	}
	if len(users) != 2 || users[0].ID != alex.ID || users[1].ID != pat.ID {
		t.Fatalf("Users() = %v, want alex then Pat", usernames(users))
	}
	if !users[1].PasswordChangeRequired || !users[1].CreatedAt.Equal(pat.CreatedAt) || users[1].LastSignInAt != nil || !sameHash(users[1].PasswordHash, temporary) {
		t.Errorf("Users()[1] = %+v, want Pat as created", users[1])
	}

	// A seed chooses the id of the user it creates: the row carries that id, and an id
	// another user holds is refused, so no second user comes to hold it.
	chosen := impulse.Must(impulse.NewUUID())
	sam, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{ID: impulse.NullUUIDFromUUID(chosen), Username: "sam@lakeside.edu"}, nil)
	if err != nil {
		t.Fatalf("CreateUser() under a chosen id error = %v", err)
	}
	if sam.ID != chosen {
		t.Errorf("CreateUser() under a chosen id = %s, want %s", sam.ID, chosen)
	}
	if read, err := in.Driver.User(ctx, chosen); err != nil || read.Username != "sam@lakeside.edu" {
		t.Errorf("User(the chosen id) = %+v, %v; want sam", read, err)
	}
	if _, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{ID: impulse.NullUUIDFromUUID(chosen), Username: "second@lakeside.edu"}, nil); err == nil {
		t.Error("CreateUser() under an id another user holds error = nil, want a refusal")
	}
	if _, err := in.Driver.UserByUserName(ctx, "second@lakeside.edu"); !httpio.HasNotFound(err) {
		t.Errorf("UserByUserName(second) error = %v, want NotFound: the refused insert left no user", err)
	}
}

// testPasswordReplaced pins ReplaceUserPassword, the write of a password set at the end
// of a flow that proved who the person is by other means: the hash and the mark land,
// cleared for a password the user chose and set for one that must be changed,
// whatever the row held, and every session of the user ends in the same transaction,
// no other user's; an unknown user is NotFound; and a replacement whose ending of the
// sessions fails writes no password, so no failure leaves the new password in place and
// a session alive. It runs with the impersonation table attached and without it, the
// two ways the sessions are ended.
func testPasswordReplaced(ctx context.Context, t *testing.T, h *AuthHarness) {
	hashes := passwordHashes(t, "temporary", "chosen", "another")
	temporary, chosen, another := hashes[0], hashes[1], hashes[2]

	for _, impersonation := range []bool{false, true} {
		t.Run(fmt.Sprintf("impersonation attached: %t", impersonation), func(t *testing.T) {
			in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Impersonation: impersonation})
			start := func(user *dbtype.SessionUser) impulse.UUID {
				t.Helper()
				id, err := in.Driver.InsertSession(ctx, newInsertSession(user.Username), &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: user.Username, UserID: user.ID})
				if err != nil {
					t.Fatalf("InsertSession() error = %v", err)
				}

				return id
			}
			pat, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "pat@lakeside.edu", PasswordHash: temporary, PasswordChangeRequired: true}, nil)
			if err != nil {
				t.Fatalf("CreateUser() error = %v", err)
			}
			other := passwordUser(ctx, t, in.Driver, "other@lakeside.edu")
			first, second, others := start(pat), start(pat), start(other)

			// A password the user chose: the new hash and the cleared mark land, and the
			// user's sessions end with them.
			if err := in.Driver.ReplaceUserPassword(ctx, pat.ID, chosen, false); err != nil {
				t.Fatalf("ReplaceUserPassword() error = %v", err)
			}
			if changed := mustUser(ctx, t, in.Driver, pat.ID); changed.PasswordChangeRequired || !sameHash(changed.PasswordHash, chosen) || !changed.CreatedAt.Equal(pat.CreatedAt) {
				t.Errorf("User() after the replacement = %+v, want the chosen hash, no mark and the creation time kept", changed)
			}
			assertSessionEnded(ctx, t, in.Driver, "the user's first session, after the replacement", first, true)
			assertSessionEnded(ctx, t, in.Driver, "the user's second session, after the replacement", second, true)
			assertSessionEnded(ctx, t, in.Driver, "another user's session, after the replacement", others, false)

			// It is replaced under the mark: the mark is set again with the temporary hash,
			// and a session started since ends.
			later := start(pat)
			if err := in.Driver.ReplaceUserPassword(ctx, pat.ID, temporary, true); err != nil {
				t.Fatalf("ReplaceUserPassword() error = %v", err)
			}
			if marked := mustUser(ctx, t, in.Driver, pat.ID); !marked.PasswordChangeRequired || !sameHash(marked.PasswordHash, temporary) {
				t.Errorf("User() after the replacement under the mark = %+v, want the temporary hash and the mark", marked)
			}
			assertSessionEnded(ctx, t, in.Driver, "the user's session, after the replacement under the mark", later, true)

			// An unknown user is told so, and nobody's session ends for it.
			if err := in.Driver.ReplaceUserPassword(ctx, impulse.Must(impulse.NewUUID()), chosen, false); !httpio.HasNotFound(err) {
				t.Errorf("ReplaceUserPassword() of an unknown user error = %v, want NotFound", err)
			}
			assertSessionEnded(ctx, t, in.Driver, "another user's session, after the replacement of an unknown user's password", others, false)

			// The sessions cannot be ended: the password is not written either.
			const table = "NoSuchTable"
			if err := in.Driver.SetSessionTableName(table); err != nil {
				t.Fatalf("SetSessionTableName() error = %v", err)
			}
			err = in.Driver.ReplaceUserPassword(ctx, pat.ID, another, false)
			if err == nil || !strings.Contains(err.Error(), table) {
				t.Fatalf("ReplaceUserPassword() where the sessions cannot be ended error = %v, want the failure of the sessions table, naming %s", err, table)
			}
			if got := mustUser(ctx, t, in.Driver, pat.ID); !sameHash(got.PasswordHash, temporary) || !got.PasswordChangeRequired {
				t.Errorf("User() after the failed replacement holds another password or mark than before it, want both as they were: the replacement is one transaction")
			}
		})
	}
}

// assertSessionEnded fails unless the session is ended or stands as want says: a
// replacement of a password ends its user's sessions in the transaction that writes
// the password, and no other user's.
func assertSessionEnded(ctx context.Context, t *testing.T, d AuthDriver, what string, id impulse.UUID, want bool) {
	t.Helper()

	if got := mustSession(ctx, t, d, id).Expired; got != want {
		t.Errorf("%s: ended = %t, want %t", what, got, want)
	}
}

// testUserLastSignIn pins LastSignInAt: nil before the first sign-in; the database's
// time at TouchUserSignIn, which the Auth calls once a sign-in has committed, as a write
// of its own (inside the sign-in's transaction the update would deadlock two sign-ins
// of one user on PostgreSQL); untouched by the flow itself and by a session the
// application starts; CreatedAt never moves.
func testUserLastSignIn(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, AuthEvents: true})
	pat := passwordUser(ctx, t, in.Driver, "pat@lakeside.edu")
	alex := createUser(ctx, t, in.Driver, "alex@lakeside.edu")
	if pat.LastSignInAt != nil {
		t.Fatalf("CreateUser().LastSignInAt = %v, want nil before the first sign-in", pat.LastSignInAt)
	}

	// The flow writes the session, not the user.
	req := &sessioninfo.NewSessionRequest{
		Reason: sessioninfo.ReasonLogin, Username: pat.Username, UserID: pat.ID,
		Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: pat.ID.String()},
	}
	sessionID, err := in.SignIn(ctx, newInsertSession(pat.Username), req)
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	if signedIn, err := in.Driver.User(ctx, pat.ID); err != nil || signedIn.LastSignInAt != nil {
		t.Errorf("User().LastSignInAt after the flow alone = %v, %v; want nil: the touch is the store's own write", signedIn.LastSignInAt, err)
	}

	// The touch stamps the database's time, after the session's.
	w := startWindow(h.ClockLead)
	if err := in.Driver.TouchUserSignIn(ctx, pat.ID); err != nil {
		t.Fatalf("TouchUserSignIn() error = %v", err)
	}
	w.done()
	touched, err := in.Driver.User(ctx, pat.ID)
	if err != nil {
		t.Fatalf("User() error = %v", err)
	}
	if touched.LastSignInAt == nil {
		t.Fatal("User().LastSignInAt = nil after the touch, want the database's time")
	}
	w.assertStamped(t, "User().LastSignInAt", *touched.LastSignInAt)
	if session := mustSession(ctx, t, in.Driver, sessionID); touched.LastSignInAt.Before(session.CreatedAt) {
		t.Errorf("User().LastSignInAt = %v, want no earlier than the session's creation %v", *touched.LastSignInAt, session.CreatedAt)
	}
	if !touched.CreatedAt.Equal(pat.CreatedAt) {
		t.Errorf("User().CreatedAt = %v after the touch, want %v untouched", touched.CreatedAt, pat.CreatedAt)
	}
	if err := in.Driver.TouchUserSignIn(ctx, impulse.Must(impulse.NewUUID())); !httpio.HasNotFound(err) {
		t.Errorf("TouchUserSignIn() of an unknown user error = %v, want NotFound", err)
	}

	// A session the application starts (a regeneration) is no sign-in.
	if _, err := in.Driver.InsertSession(ctx, newInsertSession(pat.Username), &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonRegeneration, Username: pat.Username, UserID: pat.ID}); err != nil {
		t.Fatalf("InsertSession() error = %v", err)
	}
	if regenerated, err := in.Driver.User(ctx, pat.ID); err != nil || !regenerated.LastSignInAt.Equal(*touched.LastSignInAt) {
		t.Errorf("User().LastSignInAt after a regeneration = %v, %v; want %v untouched", regenerated.LastSignInAt, err, *touched.LastSignInAt)
	}
	if never, err := in.Driver.User(ctx, alex.ID); err != nil || never.LastSignInAt != nil {
		t.Errorf("User() of a user who never signed in = %+v, %v; want no sign-in", never, err)
	}
}

// usernames lists users' usernames, for a failure message.
func usernames(users []*dbtype.SessionUser) []string {
	names := make([]string, len(users))
	for i, u := range users {
		names[i] = u.Username
	}

	return names
}

// sameHash reports whether two hashes are the same stored value.
func sameHash(got, want *securehash.Hash) bool {
	if got == nil || want == nil {
		return got == want
	}
	g, err := got.MarshalText()
	if err != nil {
		return false
	}
	w, err := want.MarshalText()
	if err != nil {
		return false
	}

	return bytes.Equal(g, w)
}
