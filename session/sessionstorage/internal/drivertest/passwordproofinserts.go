package drivertest

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// laterWrite is a write that replaces or removes a user's password after a sign-in
// proved it: what a sign-in on its way, or one held for a second step, may lose to.
type laterWrite struct {
	name string
	// write makes the write for a user whose row holds held. replacement is the hash a
	// change or a temporary password writes; a removal writes none.
	write func(ctx context.Context, d AuthDriver, userID impulse.UUID, held, replacement *securehash.Hash) error
}

// laterWrites are the three ways a password stops being the one a sign-in proved: the
// person changes it, an operator sets a temporary one, and it is removed.
func laterWrites() []laterWrite {
	return []laterWrite{
		{
			name: "the person's own change",
			write: func(ctx context.Context, d AuthDriver, userID impulse.UUID, held, replacement *securehash.Hash) error {
				return d.ChangeUserPassword(ctx, userID, held, replacement)
			},
		},
		{
			name: "a temporary password an operator sets",
			write: func(ctx context.Context, d AuthDriver, userID impulse.UUID, _, replacement *securehash.Hash) error {
				return d.ReplaceUserPassword(ctx, userID, replacement, true)
			},
		},
		{
			name: "a removal",
			write: func(ctx context.Context, d AuthDriver, userID impulse.UUID, _, _ *securehash.Hash) error {
				return removeStanding(ctx, d, userID)
			},
		},
	}
}

// heldProof is the proof of the password userID's row holds now, as a sign-in that just
// compared a password against it carries.
func heldProof(ctx context.Context, t *testing.T, d AuthDriver, userID impulse.UUID) dbtype.PasswordProof {
	t.Helper()

	proof, err := dbtype.ProofOf(mustUser(ctx, t, d, userID).PasswordHash)
	if err != nil || proof == nil {
		t.Fatalf("ProofOf() = %x, %v; want the proof of the password the user holds", proof, err)
	}

	return proof
}

// passwordRequest is the sign-in of user's password identity.
func passwordRequest(user *dbtype.SessionUser) *sessioninfo.NewSessionRequest {
	return &sessioninfo.NewSessionRequest{
		Reason: sessioninfo.ReasonLogin, Username: user.Username, UserID: user.ID,
		Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()},
	}
}

// passwordPending is a password sign-in of user held for a second step, resting on
// proof.
func passwordPending(user *dbtype.SessionUser, hash []byte, proof dbtype.PasswordProof) *dbtype.PendingSignIn {
	p := newPendingSignIn(impulse.NullUUIDFromUUID(user.ID), hash)
	p.Registration, p.Username = "", user.Username
	p.Identity = sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()}
	p.Events = []sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword, At: p.Events[0].At}}
	p.RoleNames, p.ProviderSessionID, p.ProviderSessionIssuer, p.ReturnURL = nil, "", "", ""
	p.PasswordProof = proof

	return p
}

// assertPasswordChanged fails unless err is the refusal of an insert whose password
// proof the user no longer answers to.
func assertPasswordChanged(t *testing.T, what string, err error) {
	t.Helper()

	if !errors.Is(err, dbtype.ErrPasswordChanged) || !httpio.HasConflict(err) {
		t.Fatalf("%s error = %v, want ErrPasswordChanged (Conflict): the user no longer holds the password that was proved", what, err)
	}
}

// testProvenSignIn pins the session a sign-in inserts on the strength of a password
// proof: it is inserted while the user still holds the password that was proved, and
// once that password was changed, reset or removed (the write committing after the
// sign-in compared the password and before it inserts) the sign-in ends with
// ErrPasswordChanged in the transaction that would have inserted, before the policy is
// asked, with nothing written. A sign-in no password proved is held to nothing, and
// the proof of the password the user holds after the write goes in.
func testProvenSignIn(ctx context.Context, t *testing.T, h *AuthHarness) {
	asked := 0
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Policy: func(context.Context, HookTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
		asked++

		return signin.Allow(), nil
	}})
	hashes := passwordHashes(t, "original", "replacement")
	original, replacement := hashes[0], hashes[1]

	standing := linkedPasswordUser(ctx, t, in.Driver, "standing", original, false)
	id, err := in.SignIn(ctx, newInsertSession(standing.Username), passwordRequest(standing), heldProof(ctx, t, in.Driver, standing.ID))
	if err != nil {
		t.Fatalf("SignIn() on proof of the password the user holds error = %v", err)
	}
	if sess := mustSession(ctx, t, in.Driver, id); sess.Expired || asked != 1 {
		t.Fatalf("the session is expired = %t and the policy was asked %d time(s), want a live session the policy decided once", sess.Expired, asked)
	}

	for i, w := range laterWrites() {
		t.Run(w.name, func(t *testing.T) {
			user := linkedPasswordUser(ctx, t, in.Driver, fmt.Sprintf("in-flight-%d", i), original, false)
			// The sign-in read the user and compared the password; the write commits
			// before the session is inserted.
			proof := heldProof(ctx, t, in.Driver, user.ID)
			if err := w.write(ctx, in.Driver, user.ID, mustUser(ctx, t, in.Driver, user.ID).PasswordHash, replacement); err != nil {
				t.Fatalf("%s error = %v", w.name, err)
			}

			before := asked
			_, err := in.SignIn(ctx, newInsertSession(user.Username), passwordRequest(user), proof)
			assertPasswordChanged(t, "SignIn() on proof of the password that was replaced", err)
			if asked != before {
				t.Errorf("the policy was asked %d time(s) for the refused sign-in, want none: the proof is judged first", asked-before)
			}

			// A sign-in no password proved (the application's own) is held to nothing.
			if _, err := in.SignIn(ctx, newInsertSession(user.Username), passwordRequest(user)); err != nil {
				t.Errorf("SignIn() with no proof error = %v, want it to go in", err)
			}
			// The password the user holds now signs in.
			if now := mustUser(ctx, t, in.Driver, user.ID).PasswordHash; now != nil {
				if _, err := in.SignIn(ctx, newInsertSession(user.Username), passwordRequest(user), heldProof(ctx, t, in.Driver, user.ID)); err != nil {
					t.Errorf("SignIn() on proof of the password the user holds now error = %v, want it to go in", err)
				}
			}
		})
	}
}

// testProvenPendingSignIn pins the pending sign-in a password started: its row keeps
// the proof of that password, is inserted only while the user still holds it, and the
// completion, which signs in on the proof the row carries, is refused once the password
// was changed, reset or removed while the sign-in waited. A pending sign-in no password
// proved carries none and is untouched by the same writes.
func testProvenPendingSignIn(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	hashes := passwordHashes(t, "original", "replacement")
	original, replacement := hashes[0], hashes[1]

	// Held and completed while the password stands.
	kept := linkedPasswordUser(ctx, t, in.Driver, "kept", original, false)
	keptProof := heldProof(ctx, t, in.Driver, kept.ID)
	if err := in.Driver.InsertPendingSignIn(ctx, passwordPending(kept, tokenHash(1), keptProof)); err != nil {
		t.Fatalf("InsertPendingSignIn() on proof of the password the user holds error = %v", err)
	}
	row, err := in.Driver.PendingSignInByToken(ctx, tokenHash(1), time.Now())
	if err != nil || !bytes.Equal(row.PasswordProof, keptProof) {
		t.Fatalf("PendingSignInByToken() = %+v, %v; want the row with the proof it was held with", row, err)
	}
	if _, err := in.SignIn(ctx, newInsertSession(kept.Username), passwordRequest(kept), row.PasswordProof); err != nil {
		t.Fatalf("SignIn() on the proof the row carries, the password standing, error = %v", err)
	}

	writes := laterWrites()
	for n := byte(0); int(n) < len(writes); n++ {
		w := writes[n]
		t.Run(w.name, func(t *testing.T) {
			user := linkedPasswordUser(ctx, t, in.Driver, fmt.Sprintf("held-%d", n), original, false)
			proof := heldProof(ctx, t, in.Driver, user.ID)
			if err := in.Driver.InsertPendingSignIn(ctx, passwordPending(user, tokenHash(10+n), proof)); err != nil {
				t.Fatalf("InsertPendingSignIn() error = %v", err)
			}
			// A directory sign-in of the same person waits too, on no password.
			sso := newPendingSignIn(impulse.NullUUIDFromUUID(user.ID), tokenHash(20+n))
			if err := in.Driver.InsertPendingSignIn(ctx, sso); err != nil {
				t.Fatalf("InsertPendingSignIn() of a sign-in no password proved error = %v", err)
			}

			if err := w.write(ctx, in.Driver, user.ID, mustUser(ctx, t, in.Driver, user.ID).PasswordHash, replacement); err != nil {
				t.Fatalf("%s error = %v", w.name, err)
			}

			// The completion signs in on the proof the row carries.
			held, err := in.Driver.PendingSignInByToken(ctx, tokenHash(10+n), time.Now())
			if err != nil || !bytes.Equal(held.PasswordProof, proof) {
				t.Fatalf("PendingSignInByToken() = %+v, %v; want the row with its proof", held, err)
			}
			_, err = in.SignIn(ctx, newInsertSession(user.Username), passwordRequest(user), held.PasswordProof)
			assertPasswordChanged(t, "the completion's SignIn() on the proof the row carries", err)

			// The same proof holds no new pending sign-in either.
			late := passwordPending(user, tokenHash(30+n), proof)
			assertPasswordChanged(t, "InsertPendingSignIn() on proof of the password that was replaced", in.Driver.InsertPendingSignIn(ctx, late))
			if _, err := in.Driver.PendingSignInByToken(ctx, late.TokenHash, time.Now()); !httpio.HasNotFound(err) {
				t.Errorf("PendingSignInByToken() of the refused hold error = %v, want NotFound: nothing was inserted", err)
			}

			// The pending sign-in no password proved carries no proof and completes.
			waiting, err := in.Driver.PendingSignInByToken(ctx, sso.TokenHash, time.Now())
			if err != nil || waiting.PasswordProof != nil {
				t.Fatalf("PendingSignInByToken() of the sign-in no password proved = %+v, %v; want the row, with no proof", waiting, err)
			}
			if _, err := in.SignIn(ctx, newInsertSession(user.Username), passwordRequest(user), waiting.PasswordProof); err != nil {
				t.Errorf("SignIn() of the pending sign-in no password proved error = %v, want it untouched by the write", err)
			}
		})
	}

	// A proof is of some user's password: a row that names none is refused, and one that
	// names an unknown user is NotFound, with nothing inserted either way.
	orphan := passwordPending(kept, tokenHash(40), keptProof)
	orphan.UserID = impulse.NullUUID{}
	if err := in.Driver.InsertPendingSignIn(ctx, orphan); err == nil || errors.Is(err, dbtype.ErrPasswordChanged) {
		t.Errorf("InsertPendingSignIn() with a proof and no user error = %v, want a refusal that says the row names no user", err)
	}
	unknown := passwordPending(kept, tokenHash(41), keptProof)
	unknown.UserID = impulse.NullUUIDFromUUID(impulse.Must(impulse.NewUUID()))
	if err := in.Driver.InsertPendingSignIn(ctx, unknown); !httpio.HasNotFound(err) {
		t.Errorf("InsertPendingSignIn() with a proof and an unknown user error = %v, want NotFound", err)
	}
	for _, p := range []*dbtype.PendingSignIn{orphan, unknown} {
		if _, err := in.Driver.PendingSignInByToken(ctx, p.TokenHash, time.Now()); !httpio.HasNotFound(err) {
			t.Errorf("PendingSignInByToken() of a refused hold error = %v, want NotFound", err)
		}
	}
}

// testProvenLinkConfirmed pins the link a password confirmation makes: it is made while
// the user still holds the password that confirmed it, and once that password was
// changed, reset or removed the link's transaction ends with ErrPasswordChanged and
// links nothing.
func testProvenLinkConfirmed(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	hashes := passwordHashes(t, "original", "replacement")
	original, replacement := hashes[0], hashes[1]

	standing := linkedPasswordUser(ctx, t, in.Driver, "standing", original, false)
	confirmed := riverside("idp_standing")
	if err := in.LinkConfirmed(ctx, standing.ID, standing.Username, confirmed, "", heldProof(ctx, t, in.Driver, standing.ID)); err != nil {
		t.Fatalf("LinkConfirmed() on proof of the password the user holds error = %v", err)
	}
	if link, err := in.Driver.Identity(ctx, confirmed.Method, confirmed.Connection, confirmed.Subject); err != nil || link.UserID != standing.ID {
		t.Fatalf("Identity() = %+v, %v; want the confirmed link", link, err)
	}

	for i, w := range laterWrites() {
		t.Run(w.name, func(t *testing.T) {
			user := linkedPasswordUser(ctx, t, in.Driver, fmt.Sprintf("confirming-%d", i), original, false)
			proof := heldProof(ctx, t, in.Driver, user.ID)
			if err := w.write(ctx, in.Driver, user.ID, mustUser(ctx, t, in.Driver, user.ID).PasswordHash, replacement); err != nil {
				t.Fatalf("%s error = %v", w.name, err)
			}

			identity := riverside(fmt.Sprintf("idp_confirming_%d", i))
			assertPasswordChanged(t, "LinkConfirmed() on proof of the password that was replaced", in.LinkConfirmed(ctx, user.ID, user.Username, identity, "", proof))
			if _, err := in.Driver.Identity(ctx, identity.Method, identity.Connection, identity.Subject); !httpio.HasNotFound(err) {
				t.Errorf("Identity() after the refused link error = %v, want NotFound: nothing was linked", err)
			}
		})
	}
}

// testProvenSignInRacesPasswordWrite asks a sign-in on proof of a password and the
// write that replaces that password at once. Whichever order they commit in, no session
// that rests on the replaced password is left alive: the sign-in is refused
// (ErrPasswordChanged), or its session was inserted first and the write's ending of the
// user's sessions, which follows the write, found it. The comparison and the insert are
// one transaction that holds the user's row against the write. Several users are raced
// for each write, since one race may fall the right way by chance.
func testProvenSignInRacesPasswordWrite(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	hashes := passwordHashes(t, "original", "replacement")
	original, replacement := hashes[0], hashes[1]

	// The person's own change and an operator's temporary password each write the
	// password and end the sessions in one transaction.
	writes := laterWrites()[:2]
	const rounds = 6
	for i, w := range writes {
		for round := range rounds {
			user := linkedPasswordUser(ctx, t, in.Driver, fmt.Sprintf("sign-in-race-%d-%d", i, round), original, false)
			read := mustUser(ctx, t, in.Driver, user.ID).PasswordHash
			proof := heldProof(ctx, t, in.Driver, user.ID)

			var (
				wg        sync.WaitGroup
				sessionID impulse.UUID
				signInErr error
				writeErr  error
			)
			wg.Go(func() {
				sessionID, signInErr = in.SignIn(ctx, newInsertSession(user.Username), passwordRequest(user), proof)
			})
			wg.Go(func() {
				writeErr = w.write(ctx, in.Driver, user.ID, read, replacement)
			})
			wg.Wait()

			if writeErr != nil {
				t.Fatalf("round %d: %s error = %v, want the write to land whatever sign-in is on its way", round, w.name, writeErr)
			}
			if signInErr != nil {
				if !errors.Is(signInErr, dbtype.ErrPasswordChanged) {
					t.Fatalf("round %d: SignIn() error = %v, want it to go in or find the password changed", round, signInErr)
				}

				continue
			}
			if !mustSession(ctx, t, in.Driver, sessionID).Expired {
				t.Errorf("round %d: a session on proof of the password %s replaced is alive after both committed, want it refused or ended", round, w.name)
			}
		}
	}
}
