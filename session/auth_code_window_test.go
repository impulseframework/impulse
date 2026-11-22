package session

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_ACodeWritesOnlyWhileTheAccountHoldsWhatItHeld holds the write of the address a
// mailed sign-in code confirms to what the account held. The account is read before the
// code is consumed and again after it: a code whose account holds another address at the
// second read than at the first is dead, and the write is taken only while the account
// still holds what the second read held, so an administrator's change that lands while
// the code is being entered stands. A completion refused for either consumes no pending
// sign-in, writes nothing and starts no session: the store's mock refuses every call it
// was not told to expect.
func TestAuth_ACodeWritesOnlyWhileTheAccountHoldsWhatItHeld(t *testing.T) {
	t.Parallel()

	const held, given = "pat@lakeside.edu", "pat.lee@lakeside.edu"
	userID := impulse.Must(impulse.NewUUID())
	passwords := sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()}
	tests := []struct {
		name string
		// first and second are the address the account holds at the read before the code
		// is consumed and at the read after it, "" for none; confirmed says its person has
		// confirmed it.
		first, second string
		confirmed     bool
		// mailedTo is the address the code went to.
		mailedTo string
		// write says the completion writes the address, conditional on wantHeld (nil: no
		// address); wantAddress is the address written where it is not the one mailed to,
		// and written is what the store answers the write.
		write       bool
		wantHeld    *string
		wantAddress string
		written     error
		// want is the completion's refusal: code_expired for a dead code, the server's
		// error for a write that failed, and otherwise the refusal of the pending
		// sign-in's consume, where this test ends a completion that goes on.
		want sessioninfo.LoginRefusalCode
	}{
		{
			name:  "the account held an address nobody had confirmed when the code was checked and holds none once it is consumed: the code is dead",
			first: held, mailedTo: held,
			want: sessioninfo.RefusedCodeExpired,
		},
		{
			name:     "the account holds no address at either read: the address the person who proved its password gave is written while it still holds none",
			mailedTo: given, write: true,
			want: sessioninfo.RefusedPendingExpired,
		},
		{
			name:  "the account holds an address nobody has confirmed at both reads: it is written while the account still holds it",
			first: held, second: held, mailedTo: held, write: true, wantHeld: strPtr(held),
			want: sessioninfo.RefusedPendingExpired,
		},
		{
			name:  "the account holds the code's address in another spelling of its ASCII letters at both reads: it is confirmed as the account spells it",
			first: "Pat@Lakeside.edu", second: "Pat@Lakeside.edu", mailedTo: held, write: true, wantHeld: strPtr("Pat@Lakeside.edu"), wantAddress: "Pat@Lakeside.edu",
			want: sessioninfo.RefusedPendingExpired,
		},
		{
			name:  "the account's address changed after the second read, before the write: the code is dead",
			first: held, second: held, mailedTo: held, write: true, wantHeld: strPtr(held), written: httpio.NewConflictMessage("the user no longer holds the address"),
			want: sessioninfo.RefusedCodeExpired,
		},
		{
			name:  "the write failed otherwise: the server's error",
			first: held, second: held, mailedTo: held, write: true, wantHeld: strPtr(held), written: errors.New("the database is gone"),
			want: sessioninfo.RefusedInternalError,
		},
		{
			name:  "the account holds a confirmed address at both reads: nothing is written",
			first: held, second: held, confirmed: true, mailedTo: held,
			want: sessioninfo.RefusedPendingExpired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f, _, _ := newMailFixture(t, gomock.NewController(t))
			pending := &pendingState{
				ID: impulse.Must(impulse.NewUUID()), TokenHash: []byte("the pending token's hash"), Step: sessioninfo.PendingMFA,
				UserID: impulse.NullUUIDFromUUID(userID), Username: "pat", Identity: passwords,
			}
			code := expectFactorCode(f, pending, "123456")
			code.row.Email = tt.mailedTo
			// consumedAtRead says, for each read of the account, whether the code had been
			// consumed by then.
			var consumedAtRead []bool
			f.store.EXPECT().User(gomock.Any(), userID).DoAndReturn(func(context.Context, impulse.UUID) (*sessionstorage.SessionUser, error) {
				code.mu.Lock()
				consumedAtRead = append(consumedAtRead, code.row.ConsumedAt != nil)
				code.mu.Unlock()
				holds := tt.first
				if len(consumedAtRead) > 1 {
					holds = tt.second
				}

				return accountHolding(userID, holds, tt.confirmed), nil
			}).AnyTimes()
			f.store.EXPECT().UserByEmail(gomock.Any(), gomock.Any()).Return(nil, httpio.NewNotFoundMessage("no user holds the address")).AnyTimes()
			if tt.write {
				address := tt.mailedTo
				if tt.wantAddress != "" {
					address = tt.wantAddress
				}
				f.store.EXPECT().VerifyUserEmailIfHeld(gomock.Any(), userID, tt.wantHeld, address).Return(tt.written)
			}
			if tt.want == sessioninfo.RefusedPendingExpired {
				f.store.EXPECT().ConsumePendingSignIn(gomock.Any(), pending.TokenHash).Return(httpio.NewNotFoundMessage("no live pending sign-in"))
			}

			_, err := f.auth.completeWithEmailCode(context.Background(), httptest.NewRecorder(), pending, "123456", false)

			if got := sessioninfo.LoginRefusalCodeOf(err); err == nil || got != tt.want {
				t.Errorf("completeWithEmailCode() error = %v (code %q), want the refusal %q", err, got, tt.want)
			}
			if len(consumedAtRead) < 2 || consumedAtRead[0] || !consumedAtRead[len(consumedAtRead)-1] {
				t.Errorf("the account was read with the code consumed at each read: %v; want it read before the code was consumed and again after", consumedAtRead)
			}
		})
	}
}

// accountHolding is the account userID as a read of it answers: holding the address
// holds ("" for none), confirmed or not.
func accountHolding(userID impulse.UUID, holds string, confirmed bool) *sessionstorage.SessionUser {
	user := &sessionstorage.SessionUser{ID: userID, Username: "pat"}
	if holds != "" {
		user.Email = strPtr(holds)
		if confirmed {
			at := time.Now().Add(-time.Hour)
			user.EmailVerifiedAt = &at
		}
	}

	return user
}
