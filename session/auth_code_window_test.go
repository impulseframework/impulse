package session

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_ACodeWritesOnlyWhileTheAccountHoldsWhatItHeld holds the address a mailed
// sign-in code confirms to what the account held. The account is read before the code
// is consumed and again after it: a code whose account holds another address at the
// second read than at the first is dead, and a completion refused for it consumes no
// pending sign-in, hands the store no sign-in and starts no session. Otherwise the code
// step writes nothing: the sign-in hands the store the address to confirm with the
// session (SignInRules.CodeAddress), held to what the second read held, and the store's
// answer is the completion's, so an administrator's change that lands while the code is
// being entered stands. The store's mock refuses every call it was not told to expect,
// so no address is written outside the store's sign-in.
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
		// dead says the code is found dead before the wait is consumed. Otherwise the
		// sign-in hands the store the address wantAddress (the one mailed to where it is
		// "") to confirm, held to wantHeld (nil: no address), and the store answers
		// signedIn: nil for a session.
		dead        bool
		wantHeld    *string
		wantAddress string
		signedIn    error
		// want is the completion's refusal, "" for a session.
		want sessioninfo.LoginRefusalCode
	}{
		{
			name:  "the account held an address nobody had confirmed when the code was checked and holds none once it is consumed: the code is dead",
			first: held, mailedTo: held, dead: true,
			want: sessioninfo.RefusedCodeExpired,
		},
		{
			name:     "the account holds no address at either read: the address the person who proved its password gave is handed to the store, held to none",
			mailedTo: given,
		},
		{
			name:  "the account holds an address nobody has confirmed at both reads: it is handed to the store, held to itself",
			first: held, second: held, mailedTo: held, wantHeld: strPtr(held),
		},
		{
			name:  "the account holds the code's address in another spelling of its ASCII letters at both reads: it is handed to the store as the account spells it",
			first: "Pat@Lakeside.edu", second: "Pat@Lakeside.edu", mailedTo: held, wantHeld: strPtr("Pat@Lakeside.edu"), wantAddress: "Pat@Lakeside.edu",
		},
		{
			name:  "the store found the account's address changed after the second read: the code is dead",
			first: held, second: held, mailedTo: held, wantHeld: strPtr(held),
			signedIn: signinflow.Refusal("", sessioninfo.RefusedCodeExpired, errors.New("the user no longer holds the address"), "no sign-in code stands: ask for a new one"),
			want:     sessioninfo.RefusedCodeExpired,
		},
		{
			name:  "the store's sign-in failed otherwise: the server's error",
			first: held, second: held, mailedTo: held, wantHeld: strPtr(held), signedIn: errors.New("the database is gone"),
			want: sessioninfo.RefusedInternalError,
		},
		{
			name:  "the account holds a confirmed address at both reads: it is handed to the store, which writes nothing on a confirmed account",
			first: held, second: held, confirmed: true, mailedTo: held, wantHeld: strPtr(held),
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
			code := expectFactorCode(f, pending)
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
			if !tt.dead {
				f.store.EXPECT().ConsumePendingSignIn(gomock.Any(), pending.TokenHash).Return(nil)
				f.expectSignIn(impulse.Must(impulse.NewUUID()), func(req *sessioninfo.NewSessionRequest) { req.UserID, req.Username = userID, "pat" },
					func(*sessioninfo.NewSessionRequest) error { return tt.signedIn })
			}

			outcome, err := f.auth.completeWithEmailCode(context.Background(), httptest.NewRecorder(), pending, standingCode, false)

			if tt.want == "" {
				if err != nil || outcome == nil || outcome.pending != nil {
					t.Errorf("completeWithEmailCode() = %+v, %v; want a session", outcome, err)
				}
			} else if got := sessioninfo.LoginRefusalCodeOf(err); err == nil || got != tt.want {
				t.Errorf("completeWithEmailCode() error = %v (code %q), want the refusal %q", err, got, tt.want)
			}
			if len(consumedAtRead) < 2 || consumedAtRead[0] || !consumedAtRead[len(consumedAtRead)-1] {
				t.Errorf("the account was read with the code consumed at each read: %v; want it read before the code was consumed and again after", consumedAtRead)
			}
			if tt.dead {
				return
			}
			address := tt.mailedTo
			if tt.wantAddress != "" {
				address = tt.wantAddress
			}
			want := &sessionstorage.CodeAddress{Held: tt.wantHeld, Email: address}
			if f.signedIn == nil {
				t.Fatal("no sign-in reached the store, want the one that confirms the code's address")
			}
			if diff := cmp.Diff(want, f.signedIn.codeAddress); diff != "" {
				t.Errorf("the address the sign-in handed the store to confirm (-want +got):\n%s", diff)
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
