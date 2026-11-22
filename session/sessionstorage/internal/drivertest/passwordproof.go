package drivertest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// provenWrite is one of the three password writes that follow a proof of the password:
// the hash upgrade a sign-in makes, a user's own change and a user's own removal.
type provenWrite struct {
	name string
	// write makes the write for userID on proof of proven. replacement is the hash an
	// upgrade or a change writes; a removal writes none.
	write func(ctx context.Context, d AuthDriver, userID impulse.UUID, proven, replacement *securehash.Hash) error
}

// provenWrites are the three password writes that follow a proof.
func provenWrites() []provenWrite {
	return []provenWrite{
		{
			name: "the hash upgrade of a sign-in",
			write: func(ctx context.Context, d AuthDriver, userID impulse.UUID, proven, replacement *securehash.Hash) error {
				return d.UpgradeUserPasswordHash(ctx, userID, proven, replacement)
			},
		},
		{
			name: "a user's own change",
			write: func(ctx context.Context, d AuthDriver, userID impulse.UUID, proven, replacement *securehash.Hash) error {
				return d.ChangeUserPassword(ctx, userID, proven, replacement)
			},
		},
		{
			name: "a user's own removal",
			write: func(ctx context.Context, d AuthDriver, userID impulse.UUID, proven, _ *securehash.Hash) error {
				standing, err := linkIDs(ctx, d, userID)
				if err != nil {
					return err
				}

				return d.RemoveProvenUserPassword(ctx, userID, proven, standing)
			},
		},
	}
}

// passwordHashes hashes each password once. A test that makes many users shares them:
// a hash takes long enough that one per user would be most of the test.
func passwordHashes(t *testing.T, passwords ...string) []*securehash.Hash {
	t.Helper()

	hasher := securehash.New(securehash.Argon2())
	hashes := make([]*securehash.Hash, 0, len(passwords))
	for _, password := range passwords {
		hash, err := hasher.Hash(password)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, hash)
	}

	return hashes
}

// linkedPasswordUser creates a user that holds hash and one identity, so the password
// is not the user's last means of sign-in and a removal can go through.
func linkedPasswordUser(ctx context.Context, t *testing.T, d AuthDriver, name string, hash *securehash.Hash) *dbtype.SessionUser {
	t.Helper()

	user, err := d.CreateUser(ctx, &dbtype.InsertSessionUser{Username: name + "@lakeside.edu", PasswordHash: hash}, nil)
	if err != nil {
		t.Fatalf("CreateUser(%q) error = %v", name, err)
	}
	mustLink(ctx, t, d, user.ID, lakeside("idp_"+name))

	return user
}

// markedPasswordUser is linkedPasswordUser under the mark that the password must be
// changed. No write of the driver's sets the mark: it is written directly, as an
// application's account management writes it when an administrator requires a new
// password.
func markedPasswordUser(ctx context.Context, t *testing.T, h *AuthHarness, in *AuthInstance, name string, hash *securehash.Hash) *dbtype.SessionUser {
	t.Helper()

	user := linkedPasswordUser(ctx, t, in.Driver, name, hash)
	h.RequirePasswordChange(ctx, t, in.Raw, user.ID)

	return mustUser(ctx, t, in.Driver, user.ID)
}

// testProvenPasswordWrites pins the three writes that follow a proof while the row holds
// the hash that was proved: the upgrade writes the new hash and leaves the change mark
// as it is, the change writes the new hash and clears the mark, the removal takes the
// hash and the mark. The removal keeps its refusal of a user's last means of sign-in. An
// unknown user is NotFound, and a write with no proven hash writes nothing, to a user
// that holds a password and to one that holds none alike: nothing was proved.
func testProvenPasswordWrites(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	hashes := passwordHashes(t, "temporary", "replacement")
	temporary, replacement := hashes[0], hashes[1]

	upgraded := markedPasswordUser(ctx, t, h, in, "upgraded", temporary)
	if err := in.Driver.UpgradeUserPasswordHash(ctx, upgraded.ID, mustUser(ctx, t, in.Driver, upgraded.ID).PasswordHash, replacement); err != nil {
		t.Fatalf("UpgradeUserPasswordHash() on proof of the hash the row holds error = %v", err)
	}
	if got := mustUser(ctx, t, in.Driver, upgraded.ID); !sameHash(got.PasswordHash, replacement) || !got.PasswordChangeRequired {
		t.Errorf("User() after the upgrade = %+v, want the new hash and the change mark as it was, set", got)
	}

	changed := markedPasswordUser(ctx, t, h, in, "changed", temporary)
	if err := in.Driver.ChangeUserPassword(ctx, changed.ID, mustUser(ctx, t, in.Driver, changed.ID).PasswordHash, replacement); err != nil {
		t.Fatalf("ChangeUserPassword() on proof of the hash the row holds error = %v", err)
	}
	if got := mustUser(ctx, t, in.Driver, changed.ID); !sameHash(got.PasswordHash, replacement) || got.PasswordChangeRequired {
		t.Errorf("User() after the change = %+v, want the new hash and no change mark", got)
	}

	removed := markedPasswordUser(ctx, t, h, in, "removed", temporary)
	if err := in.Driver.RemoveProvenUserPassword(ctx, removed.ID, mustUser(ctx, t, in.Driver, removed.ID).PasswordHash, standingLinks(ctx, t, in.Driver, removed.ID)); err != nil {
		t.Fatalf("RemoveProvenUserPassword() on proof of the hash the row holds error = %v", err)
	}
	if got := mustUser(ctx, t, in.Driver, removed.ID); got.PasswordHash != nil || got.PasswordChangeRequired {
		t.Errorf("User() after the removal = %+v, want no password and no change mark", got)
	}

	// The proof does not lift the removal's own rule: with no identity the password is
	// the user's one means of sign-in, and it stays.
	alone, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "alone@lakeside.edu", PasswordHash: temporary}, nil)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	err = in.Driver.RemoveProvenUserPassword(ctx, alone.ID, mustUser(ctx, t, in.Driver, alone.ID).PasswordHash, nil)
	if !errors.Is(err, dbtype.ErrLastSignInMethod) || !httpio.HasConflict(err) {
		t.Errorf("RemoveProvenUserPassword() of a user with no identity error = %v, want ErrLastSignInMethod (Conflict)", err)
	}
	if got := mustUser(ctx, t, in.Driver, alone.ID); !sameHash(got.PasswordHash, temporary) {
		t.Errorf("User() after the refused removal = %+v, want the password as it was", got)
	}

	held := markedPasswordUser(ctx, t, h, in, "held", temporary)
	passwordLess := createUser(ctx, t, in.Driver, "passwordless@lakeside.edu")
	mustLink(ctx, t, in.Driver, passwordLess.ID, lakeside("idp_passwordless"))
	for _, w := range provenWrites() {
		if err := w.write(ctx, in.Driver, impulse.Must(impulse.NewUUID()), temporary, replacement); !httpio.HasNotFound(err) {
			t.Errorf("%s for an unknown user error = %v, want NotFound", w.name, err)
		}

		err := w.write(ctx, in.Driver, held.ID, nil, replacement)
		if !errors.Is(err, dbtype.ErrPasswordChanged) || !httpio.HasConflict(err) {
			t.Errorf("%s with no proven hash error = %v, want ErrPasswordChanged (Conflict)", w.name, err)
		}
		if got := mustUser(ctx, t, in.Driver, held.ID); !sameHash(got.PasswordHash, temporary) || !got.PasswordChangeRequired {
			t.Errorf("User() after %s with no proven hash = %+v, want the password and its mark as they were", w.name, got)
		}

		err = w.write(ctx, in.Driver, passwordLess.ID, nil, replacement)
		if !errors.Is(err, dbtype.ErrPasswordChanged) || !httpio.HasConflict(err) {
			t.Errorf("%s with no proven hash, for a user that holds no password, error = %v, want ErrPasswordChanged (Conflict)", w.name, err)
		}
		if got := mustUser(ctx, t, in.Driver, passwordLess.ID); got.PasswordHash != nil {
			t.Errorf("User() after %s with no proven hash = %+v, want the user still without a password", w.name, got)
		}
	}
}

// testProvenPasswordWriteAfterAnotherWrite holds each write that follows a proof to the
// hash that was proved. The user is read, as the caller reads it to compare the
// password; then another write of the password commits: a change, a removal; then the
// write is made on proof of the hash that was read. It writes nothing and answers
// ErrPasswordChanged (a Conflict), and the row holds what the other write left, hash
// and change mark: an upgrade does not put the old password back, and a removal does
// not take the password a change just wrote.
func testProvenPasswordWriteAfterAnotherWrite(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	hashes := passwordHashes(t, "original", "other", "replacement")
	original, other, replacement := hashes[0], hashes[1], hashes[2]

	// Each write between leaves a hash and a mark the write under test would not: the
	// users start under the mark, which a change and a removal clear.
	between := []struct {
		name string
		// commit makes the other write and answers the hash and the mark it leaves.
		commit func(userID impulse.UUID, read *securehash.Hash) (hash *securehash.Hash, marked bool, err error)
	}{
		{
			name: "a change",
			commit: func(userID impulse.UUID, read *securehash.Hash) (*securehash.Hash, bool, error) {
				return other, false, in.Driver.ChangeUserPassword(ctx, userID, read, other)
			},
		},
		{
			name: "a removal",
			commit: func(userID impulse.UUID, _ *securehash.Hash) (*securehash.Hash, bool, error) {
				return nil, false, removeStanding(ctx, in.Driver, userID)
			},
		},
	}

	for i, w := range provenWrites() {
		for j, b := range between {
			t.Run(fmt.Sprintf("%s after %s", w.name, b.name), func(t *testing.T) {
				user := markedPasswordUser(ctx, t, h, in, fmt.Sprintf("between-%d-%d", i, j), original)
				read := mustUser(ctx, t, in.Driver, user.ID).PasswordHash

				wantHash, wantMarked, err := b.commit(user.ID, read)
				if err != nil {
					t.Fatalf("%s error = %v", b.name, err)
				}

				err = w.write(ctx, in.Driver, user.ID, read, replacement)
				if !errors.Is(err, dbtype.ErrPasswordChanged) || !httpio.HasConflict(err) {
					t.Errorf("%s error = %v, want ErrPasswordChanged (Conflict)", w.name, err)
				}
				if got := mustUser(ctx, t, in.Driver, user.ID); !sameHash(got.PasswordHash, wantHash) || got.PasswordChangeRequired != wantMarked {
					t.Errorf("User() afterwards holds the proven hash = %t, the replacement = %t, no hash = %t, the change mark = %t; want the hash %s left and the change mark = %t",
						sameHash(got.PasswordHash, original), sameHash(got.PasswordHash, replacement), got.PasswordHash == nil, got.PasswordChangeRequired, b.name, wantMarked)
				}
			})
		}
	}
}

// testOwnChangeEndsSessions pins a user's own change of password as one transaction:
// the write that lands ends every session of the user, and no other user's; a change
// refused because the password changed meanwhile ends none; and a change whose ending of
// the sessions fails writes no password, so no failure leaves the password changed and
// a session alive. It runs with the impersonation table attached and without it, the
// two ways the sessions are ended.
func testOwnChangeEndsSessions(ctx context.Context, t *testing.T, h *AuthHarness) {
	hashes := passwordHashes(t, "old", "new", "another")
	old, replacement, another := hashes[0], hashes[1], hashes[2]

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
			pat := markedPasswordUser(ctx, t, h, in, "pat", old)
			other := linkedPasswordUser(ctx, t, in.Driver, "other", old)
			first, second, others := start(pat), start(pat), start(other)

			if err := in.Driver.ChangeUserPassword(ctx, pat.ID, mustUser(ctx, t, in.Driver, pat.ID).PasswordHash, replacement); err != nil {
				t.Fatalf("ChangeUserPassword() on proof of the hash the row holds error = %v", err)
			}
			if got := mustUser(ctx, t, in.Driver, pat.ID); !sameHash(got.PasswordHash, replacement) || got.PasswordChangeRequired {
				t.Errorf("User() after the change = %+v, want the new hash and no change mark", got)
			}
			for name, id := range map[string]impulse.UUID{"the user's first session": first, "the user's second session": second} {
				if !mustSession(ctx, t, in.Driver, id).Expired {
					t.Errorf("%s stands after the change, want it ended in the transaction that wrote the password", name)
				}
			}
			if mustSession(ctx, t, in.Driver, others).Expired {
				t.Error("another user's session was ended by the change")
			}

			// A change on proof of the password just replaced is refused, and ends nothing.
			later := start(pat)
			err := in.Driver.ChangeUserPassword(ctx, pat.ID, old, another)
			if !errors.Is(err, dbtype.ErrPasswordChanged) || !httpio.HasConflict(err) {
				t.Fatalf("ChangeUserPassword() on proof of a replaced password error = %v, want ErrPasswordChanged (Conflict)", err)
			}
			if mustSession(ctx, t, in.Driver, later).Expired {
				t.Error("a refused change ended the user's session")
			}

			// The sessions cannot be ended: the password is not written either.
			const table = "NoSuchTable"
			if err := in.Driver.SetSessionTableName(table); err != nil {
				t.Fatalf("SetSessionTableName() error = %v", err)
			}
			err = in.Driver.ChangeUserPassword(ctx, pat.ID, mustUser(ctx, t, in.Driver, pat.ID).PasswordHash, another)
			if err == nil || errors.Is(err, dbtype.ErrPasswordChanged) || !strings.Contains(err.Error(), table) {
				t.Fatalf("ChangeUserPassword() where the sessions cannot be ended error = %v, want the failure of the sessions table, naming %s", err, table)
			}
			if got := mustUser(ctx, t, in.Driver, pat.ID); !sameHash(got.PasswordHash, replacement) {
				t.Errorf("User() after the failed change holds another password than before it, want the password as it was: the change is one transaction")
			}
		})
	}
}
