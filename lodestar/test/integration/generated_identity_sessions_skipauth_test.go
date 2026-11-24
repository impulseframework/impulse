//go:build skipAuth

package integration

// generated_identity_sessions_skipauth_test: what the list of the crew's sessions says of
// each session beside whether it is live (CrewListedSessions): its state in one word, how
// it signed in, in the words of the login page's button for the method, the browser it
// was signed in from, and the account's present username. A sign-in through the persona
// provider needs the simulated provider, so the suite runs with -tags skipAuth.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/lodestar/pkg/auth"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// chromeOnLinux is the User-Agent header of Chrome on Linux, which the session library
// names "Chrome on Linux".
const chromeOnLinux = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// userAgentTransport sends each request with the User-Agent header of a browser, as the
// browser itself does.
type userAgentTransport struct {
	header string
	next   http.RoundTripper
}

// RoundTrip sends a copy of r carrying the header.
func (u userAgentTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", u.header)

	return u.next.RoundTrip(r)
}

// TestGeneratedIdentitySessionsSayStateAndHowSignedIn holds the list of the sessions on
// the served stack with the persona provider registered: a session idle past the
// timeout says timedOut, one signed out says ended, a signed-in one says live, and the
// state filters the list to the live one alone. A session signed in with the password
// says the password's label, one through the persona provider that registration's
// label, one the application starts through the Auth's own API "Started by the
// application", and one whose auth events are gone says nothing. The row carries the
// name of the browser the session was signed in from. Once the person is renamed, their
// sessions, the one made before the rename included, list under the new username, and
// the username filter finds them under it and no longer under the old one.
//
// Demonstrates: auth.generated-identity, auth.persona.
func TestGeneratedIdentitySessionsSayStateAndHowSignedIn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{persona: &auth.Persona{Issuer: "https://persona.example.com", ClientID: "lodestar-pr12"}})
	if _, err := s.crew.SeedCast(ctx, io.Discard); err != nil {
		t.Fatalf("SeedCast() error = %v", err)
	}
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	pilotID := crewAccountID(ctx, t, hq, "pilot")

	// The pilot signs in with the password from Chrome on Linux, and again from a browser
	// that then signs out.
	pilot := newBrowser(t, s, consoleAPI)
	pilot.client.Transport = userAgentTransport{header: chromeOnLinux, next: http.DefaultTransport}
	pilot.signIn(ctx, "pilot")
	live := listedSessions(ctx, t, hq, "userId:eq:"+pilotID)
	if len(live) != 1 {
		t.Fatalf("the pilot's sessions after one sign-in = %v, want the one", live)
	}
	liveID := cell[string](t, live[0], "id")
	signedOut := newBrowser(t, s, consoleAPI)
	signedOut.signIn(ctx, "pilot")
	var endedID string
	for _, row := range listedSessions(ctx, t, hq, "userId:eq:"+pilotID) {
		if id := cell[string](t, row, "id"); id != liveID {
			endedID = id
		}
	}
	if status, body := signedOut.do(ctx, http.MethodDelete, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the pilot's logout: status %d: %s", status, body)
	}
	// The signed-out session's auth events are removed, so it has no first event.
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{spanner.Delete("CrewSessionAuthEvents", spanner.Key{endedID}.AsPrefix())}); err != nil {
		t.Fatalf("removing the auth events of the pilot's signed-out session: %v", err)
	}

	// A session of the pilot's idle past the stack's timeout, written straight to the
	// table, since no sign-in makes one.
	idleID := impulse.Must(impulse.NewUUID()).String()
	idle := time.Now().UTC().Add(-servedSessionTimeout - time.Minute)
	if _, err := s.db.Apply(ctx, []*spanner.Mutation{spanner.InsertMap("CrewSessions", map[string]any{
		"Id": idleID, "Username": "pilot", "UserId": pilotID, "Expired": false,
		"AuthenticatedAt": idle, "CreatedAt": idle, "UpdatedAt": idle,
	})}); err != nil {
		t.Fatalf("writing the pilot's idle session: %v", err)
	}

	// The warden signs in through the persona provider, and the application starts a
	// session for the rookie through the Auth's own API. The start's second step is a
	// second factor of the application's own, which the Auth asks of every sign-in.
	warden := newBrowser(t, s, consoleAPI)
	warden.signInAsPersona(ctx, "warden")
	rookieID, err := impulse.UUIDFromString(crewAccountID(ctx, t, hq, "rookie"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.crew.Session().API().StartAuthenticatedSession(ctx, httptest.NewRecorder(), rookieID, []sessioninfo.AuthEvent{{Method: "magic-link"}, {Method: "magic-link-code"}}); err != nil {
		t.Fatalf("StartAuthenticatedSession() for the rookie: %v", err)
	}

	pilots := rowsByID(t, listedSessions(ctx, t, hq, "userId:eq:"+pilotID), "id")
	for _, c := range []struct {
		name, id, state string
	}{
		{name: "signed in", id: liveID, state: "live"},
		{name: "signed out", id: endedID, state: "ended"},
		{name: "idle past the timeout", id: idleID, state: "timedOut"},
	} {
		row := pilots[c.id]
		if row == nil {
			t.Errorf("the pilot's session %s is not listed", c.name)

			continue
		}
		if got := cell[string](t, row, "state"); got != c.state {
			t.Errorf("the pilot's session %s says state %q, want %q", c.name, got, c.state)
		}
	}
	var stateLive []string
	for _, row := range listedSessions(ctx, t, hq, "userId:eq:"+pilotID+",state:eq:live") {
		stateLive = append(stateLive, cell[string](t, row, "id"))
	}
	if want := []string{liveID}; !slices.Equal(stateLive, want) {
		t.Errorf("the pilot's sessions filtered by state:eq:live = %v, want %v", stateLive, want)
	}
	if row := pilots[liveID]; row != nil {
		if got := cell[string](t, row, "signedInWith"); got != "Password" {
			t.Errorf("the pilot's password sign-in says it signed in with %q, want %q", got, "Password")
		}
		if got := cell[string](t, row, "browser"); got != "Chrome on Linux" {
			t.Errorf("the pilot's session from Chrome on Linux says the browser %q, want %q", got, "Chrome on Linux")
		}
	}
	if row := pilots[endedID]; row != nil && row["signedInWith"] != nil {
		t.Errorf("the pilot's session with no auth event says it signed in with %v, want null", row["signedInWith"])
	}
	for _, c := range []struct {
		name, username, want string
	}{
		{name: "the warden's sign-in through the persona provider", username: "warden", want: "Persona"},
		{name: "the rookie's session the application started", username: "rookie", want: "Started by the application"},
	} {
		rows := listedSessions(ctx, t, hq, "username:eq:"+c.username)
		if len(rows) != 1 {
			t.Errorf("%s: the sessions listed under %s = %v, want the one", c.name, c.username, rows)

			continue
		}
		if got := cell[string](t, rows[0], "signedInWith"); got != c.want {
			t.Errorf("%s says it signed in with %q, want %q", c.name, got, c.want)
		}
	}

	// Headquarters renames the pilot: the sessions, the idle one written under the old
	// username among them, list and are found under the new one.
	const renamed = "pilot-renamed"
	status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/rename-crew-user", jsonBody(t, map[string]string{"userId": pilotID, "username": renamed}))
	assertStatus(t, status, http.StatusNoContent, body)
	var found []string
	for _, row := range listedSessions(ctx, t, hq, "username:eq:"+renamed) {
		found = append(found, cell[string](t, row, "id"))
		if got := cell[string](t, row, "username"); got != renamed {
			t.Errorf("the pilot's session %v says the username %q, want %q", row["id"], got, renamed)
		}
	}
	slices.Sort(found)
	want := []string{liveID, endedID, idleID}
	slices.Sort(want)
	if !slices.Equal(found, want) {
		t.Errorf("the sessions found by username:eq:%s = %v, want the pilot's %v", renamed, found, want)
	}
	if old := listedSessions(ctx, t, hq, "username:eq:pilot"); len(old) != 0 {
		t.Errorf("the sessions found by the pilot's old username = %v, want none", old)
	}
}
