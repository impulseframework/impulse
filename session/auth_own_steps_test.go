package session

import (
	"context"
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

// TestAuthAPI_anApplicationReportsNoStepOfTheLibrarys holds the two calls an application
// reports its own authentication through, CompletePending and StartAuthenticatedSession,
// to the application's own steps. An event under a method that records a step the
// library runs itself (the mailed code, a remembered browser standing in for it, a
// connection's attestation, a tenant's word for its directory) is refused as an event
// without a method is, before a pending sign-in is consumed, the user is read or
// anything is written: the library's rules read those events as its own proof, so the
// mailed code's event is what links a first sign-in made on a name alone and what
// answers the hold for an unconfirmed address.
func TestAuthAPI_anApplicationReportsNoStepOfTheLibrarys(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	tests := []struct {
		name   string
		method sessioninfo.AuthMethod
	}{
		{name: "the mailed code", method: sessioninfo.MethodEmailCode},
		{name: "a remembered browser", method: sessioninfo.MethodTrustedDevice},
		{name: "a connection's attestation by evidence", method: sessioninfo.MethodAttestedByEvidence},
		{name: "a connection's attestation by declaration", method: sessioninfo.MethodAttestedByDeclaration},
		{name: "a tenant's word for its directory", method: sessioninfo.MethodVouchedByTenant},
	}
	// refused asserts err is the refusal of an event under method, which names the
	// method and says what the application reports instead.
	refused := func(t *testing.T, call string, err error, method sessioninfo.AuthMethod) {
		t.Helper()

		if !httpio.HasBadRequest(err) {
			t.Fatalf("%s error = %v, want a bad request: an application reports no step the library runs itself", call, err)
		}
		if message := httpio.Message(err); !strings.Contains(message, `"`+string(method)+`"`) || !strings.Contains(message, "a name of its own") {
			t.Errorf("%s message = %q, want it to name the method %q and say the application reports its step under a name of its own", call, message, method)
		}
	}
	for _, tt := range tests {
		t.Run("CompletePending refuses "+tt.name+", and the pending sign-in stays as it was", func(t *testing.T) {
			t.Parallel()

			f := newAuthFixture(t, gomock.NewController(t))
			// The wait names the method as a factor it accepts, so nothing but the rule
			// under test stands between the event and the completion.
			wait := &signin.PendingSignInError{Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(userID), Username: "pat", Factors: []sessioninfo.AuthMethod{tt.method}}
			password := &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()}
			cookies, _ := f.hold(t, &signInAttempt{identity: password, reason: sessioninfo.ReasonLogin, userID: userID}, wait)
			consumed := false
			f.store.EXPECT().ConsumePendingSignIn(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, []byte) error {
				consumed = true

				return httpio.NewNotFoundMessage("no live pending sign-in")
			}).AnyTimes()

			var err error
			f.serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err = f.auth.API().CompletePending(r.Context(), w, sessioninfo.AuthEvent{Method: tt.method})
			}), http.MethodPost, "/mfa", nil, cookies)

			refused(t, "CompletePending()", err, tt.method)
			if consumed {
				t.Error("the pending sign-in was consumed, want it left for the step the library runs")
			}
		})
		t.Run("StartAuthenticatedSession refuses "+tt.name+" before the user is read", func(t *testing.T) {
			t.Parallel()

			f := newAuthFixture(t, gomock.NewController(t))
			read := false
			f.store.EXPECT().User(gomock.Any(), userID).DoAndReturn(func(context.Context, impulse.UUID) (*sessionstorage.SessionUser, error) {
				read = true

				return nil, httpio.NewNotFoundMessage("no user")
			}).AnyTimes()

			// The application's own step comes first: the refusal is of the event the
			// library records itself, wherever it stands among the events.
			_, err := f.auth.API().StartAuthenticatedSession(context.Background(), httptest.NewRecorder(), userID, []sessioninfo.AuthEvent{{Method: "magic-link"}, {Method: tt.method}})

			refused(t, "StartAuthenticatedSession()", err, tt.method)
			if read {
				t.Error("the user was read, want the events refused first")
			}
		})
	}
}
