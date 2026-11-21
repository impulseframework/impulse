package session

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_ACodeIsGoodOnlyForTheAddressTheAccountHolds holds the mailed sign-in code
// to the address the account holds when the code is entered. A code mailed to an address
// an administrator has since taken off the account, replaced or moved to another account
// is dead, as one that expired is, on a sign-in that waits to be linked and on one a
// password started: the completion stops once the code is consumed and before the
// pending sign-in is consumed or anything is linked or written, so nothing is written
// over the administrator's change. A code mailed to the address the account still holds
// confirms it, and an account that holds none takes the address its own person gave at
// the step.
func TestAuth_ACodeIsGoodOnlyForTheAddressTheAccountHolds(t *testing.T) {
	t.Parallel()

	const mailedTo, replacement = "alice@corp.example", "alice.new@corp.example"
	userID, another := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
	waitsToBeLinked := sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "google", Subject: "g-1", SignInName: mailedTo, Email: mailedTo}
	passwords := sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()}
	tests := []struct {
		name     string
		identity sessioninfo.Identity
		// linked says the identity is linked to the wait's user.
		linked bool
		// holds is the address the account holds when the code is entered, "" for none,
		// and confirmed whether its person has confirmed it.
		holds     string
		confirmed bool
		// heldByAnother says another account holds the address the code was mailed to.
		heldByAnother bool
		// wantDead is a code that is no longer good; otherwise the completion goes on,
		// and wantWritten is the address it writes on the account, confirmed, "" for
		// none.
		wantDead    bool
		wantWritten string
	}{
		{name: "a sign-in that waits to be linked, and the address was taken off the account", identity: waitsToBeLinked, wantDead: true},
		{name: "a sign-in that waits to be linked, and the address was replaced by one nobody has confirmed", identity: waitsToBeLinked, holds: replacement, wantDead: true},
		{name: "a sign-in that waits to be linked, and the address was replaced by a confirmed one", identity: waitsToBeLinked, holds: replacement, confirmed: true, wantDead: true},
		{name: "a sign-in that waits to be linked, and the address was moved to another account", identity: waitsToBeLinked, heldByAnother: true, wantDead: true},
		{name: "a password's sign-in, and the address was replaced by one nobody has confirmed", identity: passwords, holds: replacement, wantDead: true},
		{name: "a password's sign-in, and the address was moved to another account", identity: passwords, heldByAnother: true, wantDead: true},
		{name: "a linked identity's sign-in, and the address was moved to another account", identity: waitsToBeLinked, linked: true, heldByAnother: true, wantDead: true},
		{name: "the account still holds the address, which nobody has confirmed, and the code confirms it", identity: waitsToBeLinked, holds: mailedTo, wantWritten: mailedTo},
		{name: "the account holds the address under another spelling of its letters", identity: waitsToBeLinked, holds: "Alice@Corp.Example", wantWritten: mailedTo},
		{name: "the account holds the address confirmed, and nothing is written", identity: waitsToBeLinked, holds: mailedTo, confirmed: true},
		{name: "an account that holds no address takes the one the person who proved its password gave", identity: passwords, wantWritten: mailedTo},
		{name: "an account that holds no address takes the one the person its identity is linked to gave", identity: waitsToBeLinked, linked: true, wantWritten: mailedTo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f, _, _ := newMailFixture(t, gomock.NewController(t))
			user := &sessionstorage.SessionUser{ID: userID, Username: mailedTo}
			if tt.holds != "" {
				user.Email = &tt.holds
			}
			if tt.confirmed {
				at := time.Now().Add(-time.Hour)
				user.EmailVerifiedAt = &at
			}
			f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()
			f.store.EXPECT().UserByEmail(gomock.Any(), mailedTo).DoAndReturn(func(context.Context, string) (*sessionstorage.SessionUser, error) {
				if tt.heldByAnother {
					return &sessionstorage.SessionUser{ID: another, Username: "bob", Email: new(mailedTo)}, nil
				}

				return nil, httpio.NewNotFoundMessage("no user holds the address")
			}).AnyTimes()
			f.store.EXPECT().Identity(gomock.Any(), waitsToBeLinked.Method, waitsToBeLinked.Connection, waitsToBeLinked.Subject).DoAndReturn(
				func(context.Context, sessioninfo.AuthMethod, string, string) (*sessionstorage.SessionIdentity, error) {
					if !tt.linked {
						return nil, httpio.NewNotFoundMessage("no identity link")
					}

					return &sessionstorage.SessionIdentity{UserID: userID, Method: waitsToBeLinked.Method, Connection: waitsToBeLinked.Connection, Subject: waitsToBeLinked.Subject}, nil
				}).AnyTimes()
			pending := &pendingState{
				ID: impulse.Must(impulse.NewUUID()), TokenHash: []byte("the pending token's hash"), Step: sessioninfo.PendingMFA,
				UserID: impulse.NullUUIDFromUUID(userID), Username: mailedTo, Identity: tt.identity,
			}
			code := expectFactorCode(f, pending, "123456")
			code.row.Email = mailedTo
			// The completion of a code that is good goes on to consume the pending
			// sign-in, where this test ends it: the store answers that the row is gone. A
			// dead code reaches neither call, which the store's mock refuses.
			if !tt.wantDead {
				f.store.EXPECT().ConsumePendingSignIn(gomock.Any(), pending.TokenHash).Return(httpio.NewNotFoundMessage("no live pending sign-in"))
			}
			if tt.wantWritten != "" {
				f.store.EXPECT().VerifyUserEmailIfHeld(gomock.Any(), userID, user.Email, tt.wantWritten).Return(nil)
			}

			_, err := f.auth.completeWithEmailCode(context.Background(), httptest.NewRecorder(), pending, "123456", false)

			want := sessioninfo.RefusedPendingExpired
			if tt.wantDead {
				want = sessioninfo.RefusedCodeExpired
			}
			if got := sessioninfo.LoginRefusalCodeOf(err); got != want {
				t.Errorf("completeWithEmailCode() error = %v (code %q), want the refusal %q", err, got, want)
			}
			if code.row.ConsumedAt == nil {
				t.Error("the code stands after it was entered, want it consumed: a code is entered once")
			}
		})
	}
}

// TestAuth_emailCodeState_aCodeForAnAddressTheAccountNoLongerHolds holds the step's
// status to the same rule: a code mailed to an address the account no longer holds does
// not stand, so the page offers a new one at the address the account holds now, or says
// that the account holds none.
func TestAuth_emailCodeState_aCodeForAnAddressTheAccountNoLongerHolds(t *testing.T) {
	t.Parallel()

	const mailedTo = "alice@corp.example"
	userID := impulse.Must(impulse.NewUUID())
	waitsToBeLinked := sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "google", Subject: "g-1", SignInName: mailedTo, Email: mailedTo}
	tests := []struct {
		name  string
		holds string
		want  emailCodeAnswer
	}{
		{name: "the account holds the address, and the code stands", holds: mailedTo, want: emailCodeAnswer{CodeEmail: "a•••@corp.example", CodeSent: true}},
		{name: "the address was replaced, and a new code is offered at the address the account holds", holds: "new@corp.example", want: emailCodeAnswer{CodeEmail: "n•••@corp.example"}},
		{name: "the address was taken off the account, and no code stands", want: emailCodeAnswer{EnrollEmail: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f, _, _ := newMailFixture(t, gomock.NewController(t))
			user := &sessionstorage.SessionUser{ID: userID, Username: mailedTo}
			if tt.holds != "" {
				user.Email = &tt.holds
			}
			f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()
			f.store.EXPECT().Identity(gomock.Any(), waitsToBeLinked.Method, waitsToBeLinked.Connection, waitsToBeLinked.Subject).
				Return(nil, httpio.NewNotFoundMessage("no identity link")).AnyTimes()
			pending := &pendingState{ID: impulse.Must(impulse.NewUUID()), Step: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(userID), Identity: waitsToBeLinked}
			code := expectFactorCode(f, pending, "123456")
			code.row.Email = mailedTo

			got, err := f.auth.emailCodeState(context.Background(), pending)
			if err != nil {
				t.Fatalf("emailCodeState() error = %v", err)
			}
			if got.CodeEmail != tt.want.CodeEmail || got.CodeSent != tt.want.CodeSent || got.EnrollEmail != tt.want.EnrollEmail || (got.CodeExpiresAt != nil) != tt.want.CodeSent {
				t.Errorf("emailCodeState() = %+v, want %+v (and the code's deadline only while it stands)", *got, tt.want)
			}
		})
	}
}
