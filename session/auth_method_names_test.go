package session

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// librarySteps are the methods the library records its own steps under: the mailed
// code, a remembered browser standing in for it, and a connection's attestation.
var librarySteps = []struct {
	name   string
	method sessioninfo.AuthMethod
}{
	{name: "the mailed code", method: sessioninfo.MethodEmailCode},
	{name: "a remembered browser", method: sessioninfo.MethodTrustedDevice},
	{name: "a connection's attestation by evidence", method: sessioninfo.MethodAttestedByEvidence},
	{name: "a connection's attestation by declaration", method: sessioninfo.MethodAttestedByDeclaration},
}

// ownMethodRegistration is the registration of an application's own redirect method
// under the name "partner": registered as the method registered, it builds a method
// named built, whose round trip verifies identity.
func ownMethodRegistration(registered, built sessioninfo.AuthMethod, identity *sessioninfo.Identity) *signin.Registration[mockTx] {
	return &signin.Registration[mockTx]{
		Name: "partner", Method: registered,
		Build: func(signin.StateCookies) (*signin.Method, error) {
			return &signin.Method{
				Name:     built,
				LoginURL: func() string { return "/login" },
				Complete: func(context.Context, http.ResponseWriter, *http.Request) (*signin.Verified, error) {
					return &signin.Verified{Identity: identity, ReturnURL: "/next"}, nil
				},
			}, nil
		},
	}
}

// storeNeverSignsIn has the fixture's store answer every sign-in it is asked for with a
// refusal, and returns where the last request it was asked with is kept: nil while the
// store was never asked.
func storeNeverSignsIn(f *authFixture) **sessioninfo.NewSessionRequest {
	asked := new(*sessioninfo.NewSessionRequest)
	f.store.EXPECT().SignIn(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req *sessioninfo.NewSessionRequest, _ sessionstorage.SignInRules[mockTx]) (impulse.UUID, error) {
			*asked = req

			return impulse.NilUUID, httpio.NewUnauthorizedMessage("the store signs nobody in in this test")
		}).AnyTimes()

	return asked
}

// TestAuth_aMethodTakesNoNameOfTheLibrarysSteps proves a sign-in method is never
// registered as one of the methods the library records its own steps under. A sign-in's
// first auth event carries its method's name, so a method registered as the mailed
// code's would record the mailed code with every sign-in, and the rules that read that
// event as proof the code was entered (the first link made on a name alone, the hold
// for an unconfirmed address) would pass a person nothing was mailed to. A registration
// also has one method name: one that registers as one method and builds another is
// refused, so the name its identities are held to is the name it is listed under.
func TestAuth_aMethodTakesNoNameOfTheLibrarysSteps(t *testing.T) {
	t.Parallel()

	for _, tt := range librarySteps {
		t.Run("registered as "+tt.name, func(t *testing.T) {
			t.Parallel()

			a := newBareAuth(t, gomock.NewController(t))
			got := registrationPanic(func() {
				a.RegisterExternal(ownMethodRegistration(tt.method, tt.method, &sessioninfo.Identity{Method: tt.method, Connection: "partner", Subject: "s-1"}))
			})

			said := fmt.Sprint(got)
			if got == nil || !strings.Contains(said, `registered as the method "`+string(tt.method)+`"`) || !strings.Contains(said, "a name of its own") {
				t.Errorf("the registration panicked with %v, want it refused: the method is named after a step the library records itself, and is told to take a name of its own", got)
			}
			if got == nil && len(a.registered().external) != 0 {
				t.Errorf("the method is registered as %q: its sign-ins would each record the library's step", tt.method)
			}
		})
	}

	t.Run("a registration that builds a method under another name than the one it registers as", func(t *testing.T) {
		t.Parallel()

		a := newBareAuth(t, gomock.NewController(t))
		got := registrationPanic(func() {
			a.RegisterExternal(ownMethodRegistration("partner", sessioninfo.MethodAzure, &sessioninfo.Identity{Method: "partner", Connection: "partner", Subject: "s-1"}))
		})

		if said := fmt.Sprint(got); got == nil || !strings.Contains(said, `registered as the method "partner" and builds a method named "azure"`) {
			t.Errorf("the registration panicked with %v, want it refused: a registration has one method name", got)
		}
	})

	t.Run("an application's own method under a name of its own is registered", func(t *testing.T) {
		t.Parallel()

		a := newBareAuth(t, gomock.NewController(t))
		if got := registrationPanic(func() {
			a.RegisterExternal(ownMethodRegistration("partner", "partner", &sessioninfo.Identity{Method: "partner", Connection: "partner", Subject: "s-1"}))
		}); got != nil {
			t.Errorf("the registration panicked with %v, want the application's own method registered", got)
		}
	})
}

// TestAuth_SignInTakesNoIdentityUnderAnotherMethodsName proves a sign-in is refused when
// its identity is not under the method it came through. The identity's method becomes
// the sign-in's first auth event, so an identity under the mailed code's name would
// record the mailed code: attested, the sign-in reaches the store with that event and is
// linked on a name alone with nothing mailed; unattested, it is held with that event
// stored, and a completion with no step of the application's own passes the same rule.
// An identity under a directory's name through an application's own method would start
// a session that says the person signed in through that directory. Each is refused as
// the server's error before the store is asked, so nothing is resolved, linked or held.
func TestAuth_SignInTakesNoIdentityUnderAnotherMethodsName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity *sessioninfo.Identity
	}{
		{
			name: "an identity under the mailed code's name, attested by the provider's evidence",
			identity: &sessioninfo.Identity{
				Method: sessioninfo.MethodEmailCode, Connection: "partner", Subject: "s-1", SignInName: "alice@corp.example",
				Attestation: sessioninfo.AttestationOf([]string{"mfa"}, false), IDPAMR: []string{"mfa"},
			},
		},
		{
			name:     "an identity under the mailed code's name, with no attestation",
			identity: &sessioninfo.Identity{Method: sessioninfo.MethodEmailCode, Connection: "partner", Subject: "s-1", SignInName: "alice@corp.example"},
		},
		{
			name:     "an identity under a remembered browser's name",
			identity: &sessioninfo.Identity{Method: sessioninfo.MethodTrustedDevice, Connection: "partner", Subject: "s-1", SignInName: "alice@corp.example"},
		},
		{
			name:     "an identity under the name of an attestation by evidence",
			identity: &sessioninfo.Identity{Method: sessioninfo.MethodAttestedByEvidence, Connection: "partner", Subject: "s-1", SignInName: "alice@corp.example"},
		},
		{
			name:     "an identity under the name of an attestation by declaration",
			identity: &sessioninfo.Identity{Method: sessioninfo.MethodAttestedByDeclaration, Connection: "partner", Subject: "s-1", SignInName: "alice@corp.example"},
		},
		{
			name:     "an identity that says it came through a tenant's Microsoft Entra ID directory",
			identity: &sessioninfo.Identity{Method: sessioninfo.MethodAzure, Connection: tenantLake, Subject: "oid-1", SignInName: "alice@corp.example"},
		},
	}
	for _, tt := range tests {
		t.Run("the callback refuses "+tt.name, func(t *testing.T) {
			t.Parallel()

			f := newAuthFixture(t, gomock.NewController(t))
			h := f.auth.RegisterExternal(ownMethodRegistration("partner", "partner", tt.identity))
			asked := storeNeverSignsIn(f)

			rr := f.serve(h.Callback, http.MethodGet, "/partner/callback?code=c&state=s", nil, nil)

			if *asked != nil {
				t.Errorf("the store was asked to sign in the identity under %q with the events %v, want the sign-in refused before the store: the event would be recorded as the sign-in's own",
					(*asked).Identity.Method, (*asked).AuthEvents)
			}
			if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/login?code=internal_error" {
				t.Errorf("response = %d %q, want 302 /login?code=internal_error: the method's code is wrong, which is the server's error", rr.Code, rr.Header().Get("Location"))
			}
			if cookieNamed(rr, defaultPendingCookieName) != nil {
				t.Error("a pending cookie was written, want nothing held")
			}
		})
		t.Run("a completion refuses "+tt.name+", held by a release before the rule", func(t *testing.T) {
			t.Parallel()

			f := newAuthFixture(t, gomock.NewController(t))
			f.auth.RegisterExternal(ownMethodRegistration("partner", "partner", tt.identity))
			asked := storeNeverSignsIn(f)
			// The row is stored as a sign-in that waits for the mailed code stored it:
			// holdPending stores the identity it is given, with its event.
			userID := impulse.Must(impulse.NewUUID())
			wait := &signin.PendingSignInError{
				Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(userID), Username: "alice@corp.example", Factors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
			}
			cookies, pendingID := f.hold(t, &signInAttempt{identity: tt.identity, reason: sessioninfo.ReasonLogin, registration: "partner"}, wait)
			f.expectConsume(pendingID)

			// The application's pending page completes the sign-in with no step of its
			// own, which every check of the step-up events passes.
			var err error
			f.serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err = f.auth.API().CompletePending(r.Context(), w)
			}), http.MethodPost, "/pending/complete", nil, cookies)

			if *asked != nil {
				t.Errorf("the store was asked to sign in the identity under %q with the events %v, want the completion refused before the store",
					(*asked).Identity.Method, (*asked).AuthEvents)
			}
			if sessioninfo.LoginRefusalCodeOf(err) != sessioninfo.RefusedInternalError || !httpio.HasInternalServerError(err) {
				t.Fatalf("CompletePending() error = %v, want the server's error under the code internal_error", err)
			}
			if message := httpio.Message(err); !strings.Contains(message, `"`+string(tt.identity.Method)+`"`) {
				t.Errorf("CompletePending() message = %q, want it to name the method %q the identity is under", message, tt.identity.Method)
			}
		})
	}

	t.Run("an identity under its registration's own method is signed in", func(t *testing.T) {
		t.Parallel()

		f := newAuthFixture(t, gomock.NewController(t))
		h := f.auth.RegisterExternal(ownMethodRegistration("partner", "partner", &sessioninfo.Identity{Method: "partner", Connection: "partner", Subject: "s-1", SignInName: "alice@corp.example"}))
		f.expectSignIn(impulse.Must(impulse.NewUUID()), nil, func(req *sessioninfo.NewSessionRequest) error {
			req.UserID, req.Username = impulse.Must(impulse.NewUUID()), "alice@corp.example"

			return nil
		})

		rr := f.serve(h.Callback, http.MethodGet, "/partner/callback?code=c&state=s", nil, nil)

		if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/next" {
			t.Errorf("response = %d %q, want 302 /next", rr.Code, rr.Header().Get("Location"))
		}
	})
}

// TestAuthAPI_anApplicationReportsNoSignInTheLibraryRuns holds the two calls an
// application reports its own authentication through, CompletePending and
// StartAuthenticatedSession, to the application's own steps: an event under the name of
// a sign-in method the library runs is refused. A session's first auth event says how
// the person signed in, and a tenant's declarations, an invitation's acceptance and the
// status answer read an event under a directory's method and connection as a sign-in
// through that directory. So an application that reported its own step as Microsoft
// Entra ID's, under a tenant's id, would start a session that meets a tenant's
// "directory only" condition with no sign-in through the directory. The names refused
// are a password's, those of the directory methods the library ships, registered on the
// Auth or not, and the method of every registration on the Auth, an application's own
// among them.
func TestAuthAPI_anApplicationReportsNoSignInTheLibraryRuns(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	tests := []struct {
		name  string
		event sessioninfo.AuthEvent
	}{
		{name: "a password", event: sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}},
		{name: "Microsoft Entra ID under a tenant's id", event: sessioninfo.AuthEvent{Method: sessioninfo.MethodAzure, Connection: tenantLake}},
		{name: "Google", event: sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: "example.com"}},
		{name: "WorkOS", event: sessioninfo.AuthEvent{Method: sessioninfo.MethodWorkOS, Connection: "conn_1"}},
		{name: "a generic OpenID Connect provider", event: sessioninfo.AuthEvent{Method: sessioninfo.MethodOIDC, Connection: "https://provider.example"}},
		{name: "the method of a registration on the Auth", event: sessioninfo.AuthEvent{Method: "partner", Connection: "partner"}},
	}
	// refused asserts err is the refusal of an event under method, which names the
	// method and says what the application does instead.
	refused := func(t *testing.T, call string, err error, method sessioninfo.AuthMethod) {
		t.Helper()

		if !httpio.HasBadRequest(err) {
			t.Fatalf("%s error = %v, want a bad request: an application reports no sign-in the library runs", call, err)
		}
		if message := httpio.Message(err); !strings.Contains(message, `"`+string(method)+`"`) || !strings.Contains(message, "a sign-in the library runs") ||
			!strings.Contains(message, "a name of its own") {
			t.Errorf("%s message = %q, want it to name the method %q, say the library runs that sign-in, and say the application reports its step under a name of its own", call, message, method)
		}
	}
	// partner is the application's own method on the Auth, whose name is refused as a
	// directory's is.
	partner := func(f *authFixture) {
		f.auth.RegisterExternal(ownMethodRegistration("partner", "partner", &sessioninfo.Identity{Method: "partner", Connection: "partner", Subject: "s-1"}))
	}
	for _, tt := range tests {
		t.Run("CompletePending refuses "+tt.name+", and the pending sign-in stays as it was", func(t *testing.T) {
			t.Parallel()

			f := newAuthFixture(t, gomock.NewController(t))
			partner(f)
			// The policy named no factors, so it accepts any: nothing but the rule under
			// test stands between the event and the completion.
			wait := &signin.PendingSignInError{Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(userID), Username: "pat"}
			password := &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()}
			cookies, _ := f.hold(t, &signInAttempt{identity: password, reason: sessioninfo.ReasonLogin, userID: userID}, wait)
			consumed := false
			f.store.EXPECT().ConsumePendingSignIn(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, []byte) error {
				consumed = true

				return httpio.NewNotFoundMessage("no live pending sign-in")
			}).AnyTimes()

			var err error
			f.serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err = f.auth.API().CompletePending(r.Context(), w, tt.event)
			}), http.MethodPost, "/mfa", nil, cookies)

			refused(t, "CompletePending()", err, tt.event.Method)
			if consumed {
				t.Error("the pending sign-in was consumed, want it left as it was")
			}
		})
		t.Run("StartAuthenticatedSession refuses "+tt.name+", and no session is asked for", func(t *testing.T) {
			t.Parallel()

			f := newAuthFixture(t, gomock.NewController(t))
			partner(f)
			read := false
			f.store.EXPECT().User(gomock.Any(), userID).DoAndReturn(func(context.Context, impulse.UUID) (*sessionstorage.SessionUser, error) {
				read = true

				return &sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil
			}).AnyTimes()
			asked := storeNeverSignsIn(f)

			// The event stands first, where it would be what the session says it was
			// established by.
			rr := httptest.NewRecorder()
			_, err := f.auth.API().StartAuthenticatedSession(context.Background(), rr, userID, []sessioninfo.AuthEvent{tt.event, {Method: "magic-link"}})

			if *asked != nil {
				t.Errorf("the store was asked for a session whose first event is %q under the connection %q: the session would say the person signed in through it, with no sign-in the library ran",
					(*asked).AuthEvents[0].Method, (*asked).AuthEvents[0].Connection)
			}
			refused(t, "StartAuthenticatedSession()", err, tt.event.Method)
			if read {
				t.Error("the user was read, want the events refused first")
			}
		})
	}

	t.Run("a name of the application's own that no registration holds is still reported", func(t *testing.T) {
		t.Parallel()

		f := newAuthFixture(t, gomock.NewController(t))
		partner(f)
		f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil)
		sessionID := impulse.Must(impulse.NewUUID())
		f.expectSignIn(sessionID, nil, nil)

		got, err := f.auth.API().StartAuthenticatedSession(context.Background(), httptest.NewRecorder(), userID, []sessioninfo.AuthEvent{{Method: "kiosk", Connection: "partner"}})
		if err != nil || got != sessionID {
			t.Errorf("StartAuthenticatedSession() = %s, %v, want the session %s: the application's own step under a name of its own", got, err, sessionID)
		}
	})
}
