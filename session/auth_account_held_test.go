package session

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/mockstore"
	"github.com/impulseframework/impulse/session/internal/workos"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	"go.uber.org/mock/gomock"
)

// accountWrapper is a store an application wrote over the library's: it embeds the
// library's store and declares a User and a UserByUserName of its own, which answer
// what a case tells them to and never ask the store beneath them, as a store that keeps
// the accounts it has read would.
type accountWrapper struct {
	*mockstore.AuthStore[mockTx]

	answer *sessionstorage.SessionUser
}

// User is the wrapper's own read of an account, in front of the library's.
func (w *accountWrapper) User(context.Context, impulse.UUID) (*sessionstorage.SessionUser, error) {
	return w.answer, nil
}

// UserByUserName is the wrapper's own read of an account by its name, in front of the
// library's: the same answer.
func (w *accountWrapper) UserByUserName(context.Context, string) (*sessionstorage.SessionUser, error) {
	return w.answer, nil
}

// TestAuth_sessionUser_heldToTheLibrarysAccount holds the account of an ordinary
// session to the library's own store where a store wraps it. The wrapper's User is the
// one asked, and its answer is the account the session names: the id that was asked
// for, the username the library's own row holds, and an account that is disabled if
// either the row or the wrapper says so. What else the wrapper answers of the account
// is taken, and an account the library's store does not hold is the wrapper's whole.
func TestAuth_sessionUser_heldToTheLibrarysAccount(t *testing.T) {
	t.Parallel()

	patID := impulse.Must(impulse.NewUUID())
	samID := impulse.Must(impulse.NewUUID())
	added := "Pat of the wrapper"

	tests := []struct {
		name string
		// own is what the library's own store holds, and ownErr its error; with
		// neither, the library's own read must not be asked.
		own    *sessionstorage.SessionUser
		ownErr error
		// answer is the wrapper's User.
		answer           *sessionstorage.SessionUser
		wantUnauthorized bool
		wantErr          bool
		wantName         string
	}{
		{
			name:     "the wrapper's answer for the account asked is taken, with what it adds",
			own:      &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			answer:   &sessionstorage.SessionUser{ID: patID, Username: "pat", Name: &added},
			wantName: added,
		},
		{
			name:             "a wrapper cannot answer with another account: the library's own row is not asked",
			answer:           &sessionstorage.SessionUser{ID: samID, Username: "sam"},
			wantUnauthorized: true,
		},
		{
			name:             "a wrapper cannot answer under another username than the library's own row holds",
			own:              &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			answer:           &sessionstorage.SessionUser{ID: patID, Username: "sam"},
			wantUnauthorized: true,
		},
		{
			name:             "a wrapper cannot keep a disabled account signed in: the library's own row says it is disabled",
			own:              &sessionstorage.SessionUser{ID: patID, Username: "pat", Disabled: true},
			answer:           &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			wantUnauthorized: true,
		},
		{
			name:             "a wrapper still disables an account the library's own row holds enabled",
			own:              &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			answer:           &sessionstorage.SessionUser{ID: patID, Username: "pat", Disabled: true},
			wantUnauthorized: true,
		},
		{
			name:     "an account the library's store does not hold is the wrapper's to answer",
			ownErr:   httpio.NewNotFoundMessagef("user id %q does not exist", patID),
			answer:   &sessionstorage.SessionUser{ID: patID, Username: "pat", Name: &added},
			wantName: added,
		},
		{
			name:    "a read of the library's own that fails is the request's failure",
			ownErr:  errors.New("the database is away"),
			answer:  &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newAuthFixture(t, gomock.NewController(t))
			if tt.own != nil || tt.ownErr != nil {
				f.store.EXPECT().User(gomock.Any(), patID).Return(tt.own, tt.ownErr)
			}
			f.auth.storage = &accountWrapper{AuthStore: f.store, answer: tt.answer}

			sess := &sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{ID: impulse.Must(impulse.NewUUID()), Username: "pat"},
				UserID:      impulse.NullUUIDFromUUID(patID),
			}
			got, err := f.auth.sessionUser(context.Background(), sess)
			if httpio.HasUnauthorized(err) != tt.wantUnauthorized || (err != nil) != (tt.wantUnauthorized || tt.wantErr) {
				t.Fatalf("sessionUser() error = %v, want unauthorized %v, want another error %v", err, tt.wantUnauthorized, tt.wantErr)
			}
			if err != nil {
				return
			}
			name := ""
			if got.Name != nil {
				name = *got.Name
			}
			if got.ID != patID || got.Username != "pat" || name != tt.wantName {
				t.Errorf("sessionUser() = %+v, named %q, want the account %s, pat, named %q", got, name, patID, tt.wantName)
			}
		})
	}
}

// TestMachineOutlet_ValidateToken_heldToTheLibrarysAccount holds the account of an
// application token to the library's own store where a store wraps it, as the account
// of an ordinary session is held. The wrapper's User is the one asked, and its answer
// is the account the token names: the id that was asked for, the username the
// library's own row holds, and an account that is disabled if either the row or the
// wrapper says so. An account the library's store does not hold is the wrapper's whole.
func TestMachineOutlet_ValidateToken_heldToTheLibrarysAccount(t *testing.T) {
	t.Parallel()

	patID := impulse.Must(impulse.NewUUID())
	samID := impulse.Must(impulse.NewUUID())
	const invalidToken = `Bearer realm="droids", error="invalid_token"`

	tests := []struct {
		name string
		// own is what the library's own store holds, and ownErr its error; with
		// neither, the library's own read must not be asked.
		own    *sessionstorage.SessionUser
		ownErr error
		// answer is the wrapper's User.
		answer     *sessionstorage.SessionUser
		wantStatus int
	}{
		{
			name:       "the wrapper's answer for the account asked is taken",
			own:        &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			answer:     &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "a wrapper cannot run a token as another account: the library's own row is not asked",
			answer:     &sessionstorage.SessionUser{ID: samID, Username: "sam"},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "a wrapper cannot answer under another username than the library's own row holds",
			own:        &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			answer:     &sessionstorage.SessionUser{ID: patID, Username: "sam"},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "a wrapper cannot keep a disabled account's token working: the library's own row says it is disabled",
			own:        &sessionstorage.SessionUser{ID: patID, Username: "pat", Disabled: true},
			answer:     &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "a wrapper still disables an account the library's own row holds enabled",
			own:        &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			answer:     &sessionstorage.SessionUser{ID: patID, Username: "pat", Disabled: true},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "an account the library's store does not hold is the wrapper's to answer",
			ownErr:     httpio.NewNotFoundMessagef("user id %q does not exist", patID),
			answer:     &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "a read of the library's own that fails is the request's failure",
			ownErr:     errors.New("the database is away"),
			answer:     &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newTokenFixture(t, gomock.NewController(t))
			f.user = pat(patID)
			secret, tok := f.liveToken(sessionstorage.TokenApplication, droids)
			f.store.EXPECT().Token(gomock.Any(), tok.TokenHash).Return(tok, nil)
			f.store.EXPECT().TouchToken(gomock.Any(), tok.ID).Return(nil).AnyTimes()
			if tt.own != nil || tt.ownErr != nil {
				f.store.EXPECT().User(gomock.Any(), patID).Return(tt.own, tt.ownErr)
			}
			f.auth.storage = &accountWrapper{AuthStore: f.store, answer: tt.answer}

			var ranAs *sessioninfo.UserInfo
			rr := f.bearer(f.outlet.ValidateToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ranAs = sessioninfo.UserFromRequest(r)
				w.WriteHeader(http.StatusOK)
			})), "Bearer "+secret)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tt.wantStatus, rr.Body)
			}
			if tt.wantStatus == http.StatusUnauthorized {
				if got := rr.Header().Get("WWW-Authenticate"); got != invalidToken {
					t.Errorf("WWW-Authenticate = %q, want %q", got, invalidToken)
				}
			}
			if tt.wantStatus != http.StatusOK {
				if ranAs != nil {
					t.Errorf("the handler ran on a refused request, as %+v", ranAs)
				}

				return
			}
			if ranAs == nil || ranAs.ID != patID || ranAs.Username != "pat" {
				t.Errorf("the request ran as %+v, want the account %s, pat", ranAs, patID)
			}
		})
	}
}

// TestAuthAPI_CreateApplicationToken_heldToTheLibrarysAccount holds the issue of an
// application token to the library's own store where a store wraps it, as the use of a
// token is held. The wrapper's User is the one asked, and its answer is the account the
// request names: the id that was asked for, and an account that is disabled if either
// the library's own row or the wrapper says so, so a disabled account is issued no
// token, where it was issued one that its first request refused. An account the
// library's store does not hold is the wrapper's whole.
func TestAuthAPI_CreateApplicationToken_heldToTheLibrarysAccount(t *testing.T) {
	t.Parallel()

	patID := impulse.Must(impulse.NewUUID())
	samID := impulse.Must(impulse.NewUUID())

	tests := []struct {
		name string
		// own is what the library's own store holds, and ownErr its error; with
		// neither, the library's own read must not be asked.
		own    *sessionstorage.SessionUser
		ownErr error
		// answer is the wrapper's User.
		answer *sessionstorage.SessionUser
		// issued says a token is made for the account asked, and refusedAsDisabled that
		// the issue is refused as a disabled account's is; with neither, the issue fails.
		issued            bool
		refusedAsDisabled bool
	}{
		{
			name:   "the wrapper's answer for the account asked is taken",
			own:    &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			answer: &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			issued: true,
		},
		{
			name:   "a wrapper cannot have a token made for another account: the library's own row is not asked",
			answer: &sessionstorage.SessionUser{ID: samID, Username: "sam"},
		},
		{
			name:              "a wrapper cannot have a disabled account issued a token: the library's own row says it is disabled",
			own:               &sessionstorage.SessionUser{ID: patID, Username: "pat", Disabled: true},
			answer:            &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			refusedAsDisabled: true,
		},
		{
			name:              "a wrapper still disables an account, and the library's own row is not asked",
			answer:            &sessionstorage.SessionUser{ID: patID, Username: "pat", Disabled: true},
			refusedAsDisabled: true,
		},
		{
			name:   "an account the library's store does not hold is the wrapper's to answer",
			ownErr: httpio.NewNotFoundMessagef("user id %q does not exist", patID),
			answer: &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			issued: true,
		},
		{
			name:   "a read of the library's own that fails is the issue's failure",
			ownErr: errors.New("the database is away"),
			answer: &sessionstorage.SessionUser{ID: patID, Username: "pat"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newTokenFixture(t, gomock.NewController(t))
			if tt.own != nil || tt.ownErr != nil {
				f.store.EXPECT().User(gomock.Any(), patID).Return(tt.own, tt.ownErr)
			}
			var stored []*sessionstorage.Token
			f.store.EXPECT().InsertToken(gomock.Any(), gomock.Any(), gomock.Nil()).DoAndReturn(func(_ context.Context, tok *sessionstorage.Token, _ *auditlog.Record) error {
				stored = append(stored, tok)

				return nil
			}).AnyTimes()
			f.auth.storage = &accountWrapper{AuthStore: f.store, answer: tt.answer}

			issued, err := f.auth.API().CreateApplicationToken(context.Background(), &ApplicationTokenRequest{UserID: patID, Outlet: droids, Name: "laptop"})
			if tt.issued {
				if err != nil {
					t.Fatalf("CreateApplicationToken() error = %v", err)
				}
				if len(stored) != 1 || stored[0].UserID != patID || issued.Token.UserID != patID {
					t.Errorf("stored %d token(s), issued for %s; want one, for the account asked (%s)", len(stored), issued.Token.UserID, patID)
				}

				return
			}
			if err == nil {
				t.Fatalf("CreateApplicationToken() issued a token for the account %s, want none issued", issued.Token.UserID)
			}
			if got := httpio.HasBadRequest(err) && httpio.Message(err) == "the user is disabled"; got != tt.refusedAsDisabled {
				t.Errorf("CreateApplicationToken() error = %v; refused as a disabled account's issue is: %t, want %t", err, got, tt.refusedAsDisabled)
			}
			if len(stored) != 0 {
				t.Errorf("%d token(s) stored on an issue that was not made, for the account %s", len(stored), stored[0].UserID)
			}
		})
	}
}

// TestPasswordSignIn_heldToTheLibrarysAccount holds a password sign-in to the library's
// own store where a store wraps it. The wrapper's UserByUserName is the one asked, and
// the account it answers is disabled if either its answer or the library's own row
// says so: the sign-in is refused there, with the code a disabled account's sign-in
// gets, and the store is asked for no session; the check of the credentials alone,
// which starts no session, is refused the same way. An account the library's store
// does not hold is the wrapper's whole.
func TestPasswordSignIn_heldToTheLibrarysAccount(t *testing.T) {
	t.Parallel()

	patID := impulse.Must(impulse.NewUUID())
	sessionID := impulse.Must(impulse.NewUUID())

	tests := []struct {
		name string
		// own is what the library's own store holds, and ownErr its error; with
		// neither, the library's own read must not be asked.
		own    *sessionstorage.SessionUser
		ownErr error
		// answeredDisabled is what the wrapper's UserByUserName says of the account.
		answeredDisabled bool
		wantRefused      bool
		wantErr          bool
	}{
		{
			name:        "a wrapper cannot have a password accepted for an account the library's own row holds disabled",
			own:         &sessionstorage.SessionUser{ID: patID, Username: "pat", Disabled: true},
			wantRefused: true,
		},
		{
			name:             "a wrapper still disables an account the library's own row holds enabled: the row is not asked",
			answeredDisabled: true,
			wantRefused:      true,
		},
		{
			name: "an account neither holds disabled signs in",
			own:  &sessionstorage.SessionUser{ID: patID, Username: "pat"},
		},
		{
			name:   "an account the library's store does not hold is the wrapper's to answer",
			ownErr: httpio.NewNotFoundMessagef("user id %q does not exist", patID),
		},
		{
			name:    "a read of the library's own that fails is the sign-in's failure",
			ownErr:  errors.New("the database is away"),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		wrapped := func(t *testing.T) *authFixture {
			t.Helper()

			f := newAuthFixture(t, gomock.NewController(t))
			if tt.own != nil || tt.ownErr != nil {
				f.store.EXPECT().User(gomock.Any(), patID).Return(tt.own, tt.ownErr)
			}
			f.auth.storage = &accountWrapper{
				AuthStore: f.store,
				answer:    &sessionstorage.SessionUser{ID: patID, Username: "pat", PasswordHash: hashed(t, "pw"), Disabled: tt.answeredDisabled},
			}

			return f
		}
		t.Run(tt.name+": the sign-in", func(t *testing.T) {
			t.Parallel()
			f := wrapped(t)
			wantStatus, wantCode := http.StatusOK, sessioninfo.LoginRefusalCode("")
			switch {
			case tt.wantRefused:
				wantStatus, wantCode = http.StatusUnauthorized, sessioninfo.RefusedUserDisabled
			case tt.wantErr:
				wantStatus = http.StatusInternalServerError
			default:
				f.expectSignIn(sessionID, nil, nil)
			}

			rr := f.serve(f.password.Login(), http.MethodPost, "/login", map[string]string{"username": "pat", "password": "pw"}, nil)

			if rr.Code != wantStatus {
				t.Fatalf("status = %d, want %d: %s", rr.Code, wantStatus, rr.Body.String())
			}
			if got := refusalCode(t, rr); got != wantCode {
				t.Errorf("refusal code = %q, want %q", got, wantCode)
			}
		})
		t.Run(tt.name+": the check of the credentials alone", func(t *testing.T) {
			t.Parallel()
			f := wrapped(t)

			got, err := f.auth.API().ValidateCredentials(context.Background(), "pat", "pw")

			switch {
			case tt.wantRefused:
				if !errors.Is(err, signin.ErrUserDisabled) {
					t.Errorf("ValidateCredentials() = %s, %v; want the refusal of a disabled account's sign-in", got, err)
				}
			case tt.wantErr:
				if err == nil || errors.Is(err, signin.ErrUserDisabled) || httpio.HasUnauthorized(err) {
					t.Errorf("ValidateCredentials() = %s, %v; want the failure of the library's own read", got, err)
				}
			default:
				if err != nil || got != patID {
					t.Errorf("ValidateCredentials() = %s, %v; want the account %s", got, err, patID)
				}
			}
		})
	}
}

// TestAuth_PendingConfirmWithPassword_heldToTheLibrarysAccount holds the password
// confirmation of a pending identity to the library's own store where a store wraps
// it: an account the library's own row holds disabled is refused before the pending
// identity is spent and before anything is linked, whatever the wrapper's User says of
// it. Without the hold the identity was linked to the disabled account, in the
// library's store, before the store refused the session.
func TestAuth_PendingConfirmWithPassword_heldToTheLibrarysAccount(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	identity := &sessioninfo.Identity{Method: sessioninfo.MethodWorkOS, Connection: "conn", Subject: "idp", Email: "pat@lakeside.edu"}
	confirmation := &signin.PendingSignInError{Reason: sessioninfo.PendingConfirmation, UserID: impulse.NullUUIDFromUUID(userID), Username: "pat", Tenant: "lakeside"}

	f := newAuthFixture(t, gomock.NewController(t))
	workos.SignIn(f.auth, "workos", resolveAll[workos.Identity](), &workos.Config{APIKey: "sk", ClientID: "client", RedirectURL: "https://app/sso/callback"})
	cookies, _ := f.hold(t, &signInAttempt{identity: identity, reason: sessioninfo.ReasonLogin, registration: "workos"}, confirmation)
	f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", Disabled: true}, nil)
	f.auth.storage = &accountWrapper{AuthStore: f.store, answer: &sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: hashed(t, "pw")}}

	rr := f.serve(f.auth.Pending().ConfirmWithPassword(), http.MethodPost, "/pending/confirm", map[string]string{"password": "pw"}, cookies)

	if rr.Code != http.StatusUnauthorized || refusalCode(t, rr) != sessioninfo.RefusedUserDisabled {
		t.Fatalf("the confirmation = %d %s, want 401 with the code %s", rr.Code, rr.Body.String(), sessioninfo.RefusedUserDisabled)
	}
	if len(f.linked) != 0 {
		t.Errorf("IdentityLinked reports = %d, want none: nothing is linked to a disabled account", len(f.linked))
	}
	if pendingDeleted(rr) {
		t.Error("the pending identity was spent on a confirmation that was refused for the account")
	}
}
