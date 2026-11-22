package session

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/internal/workos"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// proofOf is the proof of a password compared against hash.
func proofOf(t *testing.T, hash *securehash.Hash) sessionstorage.PasswordProof {
	t.Helper()

	proof, err := sessionstorage.ProofOfPassword(hash)
	if err != nil || proof == nil {
		t.Fatalf("ProofOfPassword() = %x, %v; want a proof", proof, err)
	}

	return proof
}

// passwordChanged is the store's refusal of an insert whose password proof the user no
// longer answers to, as the sign-in flow returns it.
func passwordChanged() error {
	return errors.Wrap(httpio.NewConflictMessageWithError(sessionstorage.ErrPasswordChanged, "the password has changed since it was checked: check it again and retry"), "signinflow.SignIn()")
}

// wrongPasswordAnswer is what the login answers a wrong password with: the status and
// the body a sign-in refused for a password the user no longer holds must repeat.
func wrongPasswordAnswer(t *testing.T) (status int, body []byte) {
	t.Helper()

	f := newAuthFixture(t, gomock.NewController(t))
	f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(&sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "pat", PasswordHash: hashed(t, "pw")}, nil)
	rr := f.login("pat", "wrong", "10.0.0.1", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong password = %d %s, want 401", rr.Code, rr.Body.String())
	}

	return rr.Code, rr.Body.Bytes()
}

// TestAuth_PasswordProof_Login pins that a password login rests on the proof of the
// password it compared against: the store is handed that proof with the sign-in, and
// with the pending sign-in the login is held as, so it inserts neither once the user
// holds another password; and a login the store refuses that way is answered as a wrong
// password is, in the same status and body, and counted as one, with no cookie of a
// session written.
func TestAuth_PasswordProof_Login(t *testing.T) {
	t.Parallel()

	userID, sessionID := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
	hash := hashed(t, "pw")
	pat := func() *sessionstorage.SessionUser {
		return &sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: hash}
	}
	wrongStatus, wrongBody := wrongPasswordAnswer(t)
	assertRefusedAsWrong := func(t *testing.T, f *authFixture, rr *httptest.ResponseRecorder) {
		t.Helper()

		if rr.Code != wrongStatus || !bytes.Equal(rr.Body.Bytes(), wrongBody) {
			t.Errorf("login = %d %s, want what a wrong password gets: %d %s", rr.Code, rr.Body.String(), wrongStatus, wrongBody)
		}
		if c := cookieNamed(rr, internalcookie.XSRFCookieName); c != nil {
			t.Errorf("login set the cookie %v, want none of a session", c)
		}
		if r := f.attemptsRecord("pat"); r == nil || r.Failures != 1 {
			t.Errorf("attempts under the name = %+v, want one failure", r)
		}
	}

	t.Run("the sign-in is handed to the store with the proof of the password that matched", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(pat(), nil)
		f.expectSignIn(sessionID, nil, nil)

		if rr := f.login("pat", "pw", "10.0.0.1", ""); rr.Code != http.StatusOK {
			t.Fatalf("login = %d %s, want 200", rr.Code, rr.Body.String())
		}
		if want := proofOf(t, hash); !bytes.Equal(f.signedIn.proven, want) {
			t.Errorf("SignIn() given the proof %x, want %x: the proof of the hash the password was compared against", f.signedIn.proven, want)
		}
	})

	t.Run("after a hash upgrade, the proof is of the hash the upgrade wrote", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		outdated, err := securehash.New(securehash.Bcrypt()).Hash("pw")
		if err != nil {
			t.Fatal(err)
		}
		f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: outdated}, nil)
		var written *securehash.Hash
		f.store.EXPECT().UpgradeUserPasswordHash(gomock.Any(), userID, outdated, gomock.Not(gomock.Nil())).
			DoAndReturn(func(_ context.Context, _ impulse.UUID, _, upgraded *securehash.Hash) error {
				written = upgraded

				return nil
			})
		f.expectSignIn(sessionID, nil, nil)

		if rr := f.login("pat", "pw", "10.0.0.1", ""); rr.Code != http.StatusOK {
			t.Fatalf("login = %d %s, want 200", rr.Code, rr.Body.String())
		}
		if want := proofOf(t, written); !bytes.Equal(f.signedIn.proven, want) {
			t.Errorf("SignIn() given the proof %x, want %x: the row now holds the upgraded hash", f.signedIn.proven, want)
		}
	})

	t.Run("an upgrade that finds the password changed refuses the login, and no sign-in reaches the store", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		outdated, err := securehash.New(securehash.Bcrypt()).Hash("pw")
		if err != nil {
			t.Fatal(err)
		}
		f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: outdated}, nil)
		f.store.EXPECT().UpgradeUserPasswordHash(gomock.Any(), userID, outdated, gomock.Not(gomock.Nil())).Return(passwordChanged())

		// The mock refuses a SignIn it was not told of.
		assertRefusedAsWrong(t, f, f.login("pat", "pw", "10.0.0.1", ""))
	})

	t.Run("a sign-in the store refuses because the password has changed is answered as a wrong password is", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(pat(), nil)
		f.expectSignIn(sessionID, nil, func(*sessioninfo.NewSessionRequest) error {
			return passwordChanged()
		})

		assertRefusedAsWrong(t, f, f.login("pat", "pw", "10.0.0.1", ""))
		if len(f.touched) != 0 || len(f.signedInCalls) != 0 {
			t.Errorf("touched = %v, signed-in calls = %d; want neither for a refused sign-in", f.touched, len(f.signedInCalls))
		}
	})

	t.Run("a login held for a second step is held with the proof", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(pat(), nil)
		f.expectSignIn(sessionID, nil, func(*sessioninfo.NewSessionRequest) error {
			return &signin.PendingSignInError{Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(userID), Username: "pat"}
		})
		pendingID := f.expectHold("pat")

		if rr := f.login("pat", "pw", "10.0.0.1", ""); rr.Code != http.StatusOK {
			t.Fatalf("login = %d %s, want 200 with the step to take", rr.Code, rr.Body.String())
		}
		if got, want := f.held[pendingID].PasswordProof, proofOf(t, hash); !bytes.Equal(got, want) {
			t.Errorf("HoldSignIn() given the proof %x, want %x", got, want)
		}
	})

	t.Run("a hold the store refuses because the password has changed is answered as a wrong password is", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(pat(), nil)
		f.expectSignIn(sessionID, nil, func(*sessioninfo.NewSessionRequest) error {
			return &signin.PendingSignInError{Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(userID), Username: "pat"}
		})
		f.store.EXPECT().HoldSignIn(gomock.Any(), gomock.Any()).Return(impulse.NilUUID, passwordChanged())

		rr := f.login("pat", "pw", "10.0.0.1", "")
		assertRefusedAsWrong(t, f, rr)
		if c := cookieNamed(rr, f.auth.settings.pendingCookie); c != nil {
			t.Errorf("login set the pending cookie %v, want none: nothing was held", c)
		}
	})
}

// TestAuth_PasswordProof_Pending pins that a pending sign-in a password started
// completes only on the proof it was held with: the completion hands the store that
// proof, so it inserts the session only while the user still holds the password, and a
// completion the store refuses that way is answered as a wrong password is. A pending
// sign-in no password proved is completed with no proof and held to none.
func TestAuth_PasswordProof_Pending(t *testing.T) {
	t.Parallel()

	userID, sessionID := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
	proof := proofOf(t, hashed(t, "pw"))
	password := &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()}
	mfa := &signin.PendingSignInError{Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(userID), Username: "pat"}
	complete := func(f *authFixture, cookies []*http.Cookie) error {
		var err error
		f.serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err = f.auth.API().CompletePending(r.Context(), w, sessioninfo.AuthEvent{Method: "totp"})
		}), http.MethodPost, "/mfa", nil, cookies)

		return err
	}

	t.Run("the completion hands the store the proof the sign-in was held with", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		cookies, pendingID := f.hold(t, &signInAttempt{identity: password, reason: sessioninfo.ReasonLogin, userID: userID, username: "pat", proof: proof}, mfa)
		f.expectConsume(pendingID)
		f.expectSignIn(sessionID, nil, nil)

		if err := complete(f, cookies); err != nil {
			t.Fatalf("CompletePending() error = %v", err)
		}
		if !bytes.Equal(f.signedIn.proven, proof) {
			t.Errorf("SignIn() given the proof %x, want %x: the one the pending sign-in carried", f.signedIn.proven, proof)
		}
	})

	t.Run("a completion the store refuses because the password has changed is refused as a wrong password is", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		cookies, pendingID := f.hold(t, &signInAttempt{identity: password, reason: sessioninfo.ReasonLogin, userID: userID, username: "pat", proof: proof}, mfa)
		f.expectConsume(pendingID)
		f.expectSignIn(sessionID, nil, func(*sessioninfo.NewSessionRequest) error {
			return passwordChanged()
		})

		err := complete(f, cookies)
		if !httpio.HasUnauthorized(err) || httpio.Message(err) != "The username or password is wrong. Check what you entered and try again" || !errors.Is(err, sessionstorage.ErrPasswordChanged) {
			t.Fatalf("CompletePending() error = %v, want Unauthorized \"The username or password is wrong. Check what you entered and try again\" over ErrPasswordChanged", err)
		}
		if f.held[pendingID].ConsumedAt == nil {
			t.Error("the pending sign-in stands, want it consumed: the person signs in again")
		}
		if len(f.signedInCalls) != 0 {
			t.Errorf("signed-in calls = %d, want none", len(f.signedInCalls))
		}
	})

	t.Run("a pending sign-in no password proved completes with no proof", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		workos.SignIn(f.auth, "workos", resolveAll[workos.Identity](), &workos.Config{APIKey: "sk", ClientID: "client", RedirectURL: "https://app/sso/callback"})
		identity := &sessioninfo.Identity{Method: sessioninfo.MethodWorkOS, Connection: "conn", Subject: "idp", Email: "pat@lakeside.edu"}
		cookies, pendingID := f.hold(t, &signInAttempt{identity: identity, reason: sessioninfo.ReasonLogin, registration: "workos"}, mfa)
		if got := f.held[pendingID].PasswordProof; got != nil {
			t.Fatalf("HoldSignIn() given the proof %x, want none for a sign-in no password proved", got)
		}
		f.expectConsume(pendingID)
		f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) { req.UserID, req.Username = userID, "pat" }, nil)

		if err := complete(f, cookies); err != nil {
			t.Fatalf("CompletePending() error = %v", err)
		}
		if f.signedIn.proven != nil {
			t.Errorf("SignIn() given the proof %x, want none", f.signedIn.proven)
		}
	})
}

// TestAuth_PasswordProof_Confirmation pins that the link and the session a password
// confirmation makes rest on the proof of the password that confirmed: the store is
// handed it with the link and with the sign-in, a sign-in the policy then holds for a
// factor is held with it, and a link or a sign-in the store refuses because the
// password has changed is answered as a wrong password is and counted as one.
func TestAuth_PasswordProof_Confirmation(t *testing.T) {
	t.Parallel()

	userID, sessionID := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
	hash := hashed(t, "pw")
	identity := &sessioninfo.Identity{Method: sessioninfo.MethodWorkOS, Connection: "conn", Subject: "idp", Email: "pat@lakeside.edu"}
	confirmation := &signin.PendingSignInError{Reason: sessioninfo.PendingConfirmation, UserID: impulse.NullUUIDFromUUID(userID), Username: "pat", Tenant: "lakeside"}
	wrongStatus, _ := wrongPasswordAnswer(t)
	// held is a fixture whose browser holds a sign-in through the directory that waits
	// for pat's password.
	held := func(t *testing.T) (*authFixture, []*http.Cookie, impulse.UUID) {
		t.Helper()

		f := newAuthFixture(t, gomock.NewController(t))
		workos.SignIn(f.auth, "workos", resolveAll[workos.Identity](), &workos.Config{APIKey: "sk", ClientID: "client", RedirectURL: "https://app/sso/callback"})
		cookies, pendingID := f.hold(t, &signInAttempt{identity: identity, reason: sessioninfo.ReasonLogin, registration: "workos"}, confirmation)
		if got := f.held[pendingID].PasswordProof; got != nil {
			t.Fatalf("HoldSignIn() given the proof %x, want none before any password is proved", got)
		}
		f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: hash}, nil)
		f.expectConsume(pendingID)

		return f, cookies, pendingID
	}
	confirm := func(f *authFixture, cookies []*http.Cookie) *httptest.ResponseRecorder {
		return f.serve(f.auth.Pending().ConfirmWithPassword(), http.MethodPost, "/pending/confirm", map[string]string{"password": "pw"}, cookies)
	}
	assertRefusedAsWrong := func(t *testing.T, f *authFixture, rr *httptest.ResponseRecorder) {
		t.Helper()

		if rr.Code != wrongStatus || refusalMessage(t, rr) != "The username or password is wrong. Check what you entered and try again" {
			t.Errorf("confirmation = %d %s, want %d \"The username or password is wrong. Check what you entered and try again\", as a wrong password gets", rr.Code, rr.Body.String(), wrongStatus)
		}
		if r := f.attemptsRecord("pat"); r == nil || r.Failures != 1 {
			t.Errorf("attempts under the name = %+v, want one failure", r)
		}
		if c := cookieNamed(rr, internalcookie.XSRFCookieName); c != nil {
			t.Errorf("confirmation set the cookie %v, want none of a session", c)
		}
	}

	t.Run("the link and the sign-in are handed to the store with the proof of the password that confirmed", func(t *testing.T) {
		t.Parallel()
		f, cookies, _ := held(t)
		f.store.EXPECT().LinkConfirmedIdentity(gomock.Any(), userID, "pat", identity, "lakeside", gomock.Any(), gomock.Any(), proofOf(t, hash)).Return(nil)
		f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) { req.UserID, req.Username = userID, "pat" }, nil)

		if rr := confirm(f, cookies); rr.Code != http.StatusOK {
			t.Fatalf("confirmation = %d %s, want 200", rr.Code, rr.Body.String())
		}
		if want := proofOf(t, hash); !bytes.Equal(f.signedIn.proven, want) {
			t.Errorf("SignIn() given the proof %x, want %x", f.signedIn.proven, want)
		}
	})

	t.Run("a sign-in the policy holds for a factor after the link is held with the proof", func(t *testing.T) {
		t.Parallel()
		f, cookies, _ := held(t)
		f.store.EXPECT().LinkConfirmedIdentity(gomock.Any(), userID, "pat", identity, "lakeside", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) { req.UserID, req.Username = userID, "pat" }, func(*sessioninfo.NewSessionRequest) error {
			return &signin.PendingSignInError{Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(userID), Username: "pat"}
		})
		again := f.expectHold("pat")

		if rr := confirm(f, cookies); rr.Code != http.StatusOK {
			t.Fatalf("confirmation = %d %s, want 200 with the step to take", rr.Code, rr.Body.String())
		}
		if got, want := f.held[again].PasswordProof, proofOf(t, hash); !bytes.Equal(got, want) {
			t.Errorf("HoldSignIn() given the proof %x, want %x: the factor completes only while the confirming password stands", got, want)
		}
	})

	t.Run("a link the store refuses because the password has changed links nothing and is answered as a wrong password is", func(t *testing.T) {
		t.Parallel()
		f, cookies, _ := held(t)
		f.store.EXPECT().LinkConfirmedIdentity(gomock.Any(), userID, "pat", identity, "lakeside", gomock.Any(), gomock.Any(), gomock.Any()).Return(passwordChanged())

		// The mock refuses a SignIn and an Identity read it was not told of: the refusal
		// is not taken for an identity linked meanwhile.
		assertRefusedAsWrong(t, f, confirm(f, cookies))
		if len(f.linked) != 0 {
			t.Errorf("linked = %v, want no link reported", f.linked)
		}
	})

	t.Run("a sign-in the store refuses because the password has changed after the link is answered as a wrong password is", func(t *testing.T) {
		t.Parallel()
		f, cookies, _ := held(t)
		f.store.EXPECT().LinkConfirmedIdentity(gomock.Any(), userID, "pat", identity, "lakeside", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) { req.UserID, req.Username = userID, "pat" }, func(*sessioninfo.NewSessionRequest) error {
			return passwordChanged()
		})

		assertRefusedAsWrong(t, f, confirm(f, cookies))
	})
}

// refusalMessage is the message of a refused step's JSON answer.
func refusalMessage(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()

	return refusal(t, rr).Message
}
