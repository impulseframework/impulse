package drivertest

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// testFirstPassword pins SetFirstUserPassword, the write of the password a person who
// has none chooses for themselves: it lands for a user who holds no password, with the
// change mark clear, and ends no session of theirs; a user who holds a password, one
// they chose or a temporary one, keeps it with its mark (ErrPasswordHeld, a Conflict),
// so the write is no way around the old password a change asks for; an unknown user is
// NotFound; and of two first passwords asked at once one lands and the other finds it
// held, since the absent hash is decided by the write itself on PostgreSQL and in the
// writing transaction on Spanner.
func testFirstPassword(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	hashes := passwordHashes(t, "chosen", "another", "temporary")
	chosen, another, temporary := hashes[0], hashes[1], hashes[2]

	pat := createUser(ctx, t, in.Driver, "pat@lakeside.edu")
	mustLink(ctx, t, in.Driver, pat.ID, lakeside("idp_pat"))
	session, err := in.Driver.InsertSession(ctx, newInsertSession(pat.Username), &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: pat.Username, UserID: pat.ID})
	if err != nil {
		t.Fatalf("InsertSession() error = %v", err)
	}

	if err := in.Driver.SetFirstUserPassword(ctx, pat.ID, chosen); err != nil {
		t.Fatalf("SetFirstUserPassword() for a user who holds no password error = %v", err)
	}
	if got := mustUser(ctx, t, in.Driver, pat.ID); !sameHash(got.PasswordHash, chosen) || got.PasswordChangeRequired {
		t.Errorf("User() after the first password = %+v, want the chosen hash and no change mark", got)
	}
	assertSessionEnded(ctx, t, in.Driver, "the user's session, after their first password", session, false)

	// A second first password is none: the one that stands is changed with itself.
	err = in.Driver.SetFirstUserPassword(ctx, pat.ID, another)
	if !errors.Is(err, dbtype.ErrPasswordHeld) || !httpio.HasConflict(err) {
		t.Errorf("SetFirstUserPassword() for a user who holds the password they chose error = %v, want ErrPasswordHeld (Conflict)", err)
	}
	if got := mustUser(ctx, t, in.Driver, pat.ID); !sameHash(got.PasswordHash, chosen) {
		t.Errorf("User() after the refused write = %+v, want the password as it was", got)
	}

	marked := markedPasswordUser(ctx, t, h, in, "marked", temporary)
	err = in.Driver.SetFirstUserPassword(ctx, marked.ID, another)
	if !errors.Is(err, dbtype.ErrPasswordHeld) || !httpio.HasConflict(err) {
		t.Errorf("SetFirstUserPassword() for a user who must change the password they hold error = %v, want ErrPasswordHeld (Conflict)", err)
	}
	if got := mustUser(ctx, t, in.Driver, marked.ID); !sameHash(got.PasswordHash, temporary) || !got.PasswordChangeRequired {
		t.Errorf("User() after the refused write = %+v, want the password and its mark as they were", got)
	}

	if err := in.Driver.SetFirstUserPassword(ctx, impulse.Must(impulse.NewUUID()), chosen); !httpio.HasNotFound(err) {
		t.Errorf("SetFirstUserPassword() for an unknown user error = %v, want NotFound", err)
	}

	// Two first passwords asked at once: one lands. Several users are raced, since one
	// race may fall the right way by chance.
	const rounds = 8
	for round := range rounds {
		user := createUser(ctx, t, in.Driver, fmt.Sprintf("race-%d@lakeside.edu", round))
		var (
			wg        sync.WaitGroup
			first     error
			second    error
			landed    int
			refusedAs int
		)
		wg.Go(func() {
			first = in.Driver.SetFirstUserPassword(ctx, user.ID, chosen)
		})
		wg.Go(func() {
			second = in.Driver.SetFirstUserPassword(ctx, user.ID, another)
		})
		wg.Wait()
		for _, err := range []error{first, second} {
			switch {
			case err == nil:
				landed++
			case errors.Is(err, dbtype.ErrPasswordHeld):
				refusedAs++
			}
		}
		if landed != 1 || refusedAs != 1 {
			t.Fatalf("round %d: two first passwords asked at once ended with errors %v and %v, want one written and the other ErrPasswordHeld", round, first, second)
		}
		got := mustUser(ctx, t, in.Driver, user.ID)
		if wantChosen := first == nil; sameHash(got.PasswordHash, chosen) != wantChosen || sameHash(got.PasswordHash, another) == wantChosen {
			t.Errorf("round %d: the user holds the first write's password = %t, the second's = %t; want the one whose write landed (the first: %t)",
				round, sameHash(got.PasswordHash, chosen), sameHash(got.PasswordHash, another), wantChosen)
		}
	}
}
