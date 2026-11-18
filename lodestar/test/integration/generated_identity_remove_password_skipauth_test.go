//go:build skipAuth

// Demonstrates: auth.generated-identity, auth.multi-method.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"google.golang.org/grpc/codes"
)

// TestGeneratedIdentityRemovePassword holds the removal of a person's password on the
// served stack, with the real session manager and the fleet's simulated directory, both
// ways it is asked: by headquarters through the generated method, and by the person at
// the route beside their password change. Either way the password stays while the
// person holds no identity, since nothing else signs them in. Once the person has
// signed in through the directory, the removal goes through: the password no longer
// signs them in, refused as a wrong password is; the directory does, by the link; the
// sessions the person holds stand, the one the password established among them; and
// the requirement to change the password and the count of failed sign-ins go with it.
// A role with no grant on the method is refused, and the person's own removal asks for
// the password as proof. Headquarters reads how the person signs in from the list the
// generated management serves beside the method (CrewSignInWays), held in each state
// the pilot passes through: the page offers the removal from it.
//
// The person's account page offers the removal only where it can happen, from the read
// the methods answer names beside it: what the person signs in with. It is held here
// in each state the marshal passes through: a password alone, a password and the
// directory, the directory alone. The crew's login page offers two Google
// registrations, the fleet's Workspace and the open sign-up. The identity the fleet's
// directory linked carries the Workspace's connection, which the open registration
// writes for nobody, so it answers the Workspace's button alone: a sign-in through the
// other would not reach it. A browser with no session is answered nothing.
func TestGeneratedIdentityRemovePassword(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)
	userDomains := consoleAPI + "/user-domains"

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	remove := func(b *browser, userID string) (int, []byte) {
		return b.do(ctx, http.MethodPost, consoleAPI+"/remove-crew-password", []byte(`{"userId":"`+userID+`"}`))
	}
	hash := func(userID string) spanner.NullString {
		return readColumn[spanner.NullString](ctx, t, s.db, "CrewSessionUsers", spanner.Key{userID}, "PasswordHash")
	}
	// linkThroughDirectory signs the persona in through the fleet's directory. Their
	// login is a password persona's, so the sign-in waits for the password, which links
	// the identity and opens the session.
	linkThroughDirectory := func(user string) *browser {
		b := primedBrowser(ctx, t, s)
		status, location := b.loginConsoleGoogle(ctx, user)
		if want := consoleLogin + "?pending=confirmation&returnUrl=" + url.QueryEscape(consolePage); status != http.StatusFound || location != want {
			t.Fatalf("console Google login as %s: status %d to %q, want %d to %q", user, status, location, http.StatusFound, want)
		}
		confirmation, err := json.Marshal(map[string]string{"password": personaPassword})
		if err != nil {
			t.Fatal(err)
		}
		if status, body := b.do(ctx, http.MethodPost, consoleAPI+"/user/pending/confirm", confirmation); status != http.StatusOK {
			t.Fatalf("confirming the sign-in with %s's password: status %d: %s", user, status, body)
		}

		return b
	}
	stranger := primedBrowser(ctx, t, s)
	// What a wrong password gets, to hold a removed password's refusal against.
	wrongStatus, wrongBody := stranger.login(ctx, "cadet", "not the password")
	if wrongStatus != http.StatusUnauthorized {
		t.Fatalf("a wrong password: status %d, want 401: %s", wrongStatus, wrongBody)
	}

	// The pilot holds a password, the session it established, and no identity: the
	// password is the pilot's one way in, and headquarters does not remove it.
	pilot := newBrowser(t, s, consoleAPI)
	pilot.signIn(ctx, "pilot")
	pilotID := crewAccountID(ctx, t, hq, "pilot")
	status, body := remove(hq, pilotID)
	assertStatus(t, status, http.StatusConflict, body)
	if !strings.Contains(string(body), "link a directory sign-in first") {
		t.Errorf("remove-crew-password of a person with no identity = %s, want it to say what to do first", body)
	}
	if !hash(pilotID).Valid {
		t.Fatal("the pilot's password is gone after the refused removal")
	}
	// How a person signs in, as headquarters reads it: what each way comes through, the
	// password first. It is what the person's page offers the removal from.
	signsInThrough := func(state string, want ...string) {
		t.Helper()

		status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/crew-sign-in-ways?filter=userId:eq:"+pilotID, nil)
		assertStatus(t, status, http.StatusOK, body)
		var got []string
		for _, row := range decodeRows(t, body) {
			if isPassword := cell[string](t, row, "way") == "password"; isPassword != cell[bool](t, row, "password") {
				t.Errorf("the way %v of the pilot, holding %s: its key and its password mark disagree", row, state)
			}
			got = append(got, cell[string](t, row, "through"))
		}
		if !slices.Equal(got, want) {
			t.Errorf("how the pilot signs in, holding %s = %q, want %q", state, got, want)
		}
	}
	signsInThrough("a password alone", "Password")

	// The pilot signs in through the directory, which links it. The cadet's role carries
	// no grant on the method: refused, with nothing but the grant in the way.
	sso := linkThroughDirectory("pilot")
	// The answer for the pilot's row lists the method for headquarters: what the
	// person's page draws its Remove password button from.
	status, body = hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-users/"+pilotID+"?capabilities=Execute", nil)
	assertStatus(t, status, http.StatusOK, body)
	capabilities, _ := decodeRow(t, body)["zzCapabilities"].(map[string]any)
	offered, _ := capabilities["Execute"].([]any)
	if !slices.Contains(offered, any("RemoveCrewPassword")) {
		t.Errorf("the Execute answer for the pilot's row = %v, want it to list RemoveCrewPassword for headquarters", offered)
	}
	// The list says the same: the password, and the identity the directory linked, by
	// the words of the fleet's Google sign-in, the one of the login page's two that
	// takes it. The cadet's role carries no grant on the list either.
	signsInThrough("a password and the directory's identity", "Password", "Lodestar")
	if status, body := cadet.do(ctx, http.MethodGet, consoleAPI+"/crew-sign-in-ways?filter=userId:eq:"+pilotID, nil); status != http.StatusForbidden {
		t.Errorf("the cadet's read of how the pilot signs in: status %d, want 403: %s", status, body)
	}
	if status, body := remove(cadet, pilotID); status != http.StatusForbidden {
		t.Errorf("the cadet's remove-crew-password: status %d, want 403: %s", status, body)
	}
	if !hash(pilotID).Valid {
		t.Fatal("the pilot's password is gone after the cadet's refused removal")
	}

	// A change of the password is required of the pilot, and one failed sign-in is
	// counted under the name, when the password goes.
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/require-crew-password-change", []byte(`{"userId":"`+pilotID+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	if status, body := pilot.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusForbidden || !strings.Contains(string(body), string(sessioninfo.RefusedPasswordChangeRequired)) {
		t.Fatalf("the pilot's next request after require-crew-password-change: status %d, want 403 naming %s: %s", status, sessioninfo.RefusedPasswordChangeRequired, body)
	}
	if status, body := stranger.login(ctx, "pilot", "not the password"); status != http.StatusUnauthorized {
		t.Fatalf("a wrong password: status %d, want 401: %s", status, body)
	}
	if got := readColumn[int64](ctx, t, s.db, "CrewSessionSignInAttempts", spanner.Key{"pilot"}, "Failures"); got != 1 {
		t.Fatalf("the pilot's failed sign-ins = %d, want 1", got)
	}

	// Headquarters removes the password.
	status, body = remove(hq, pilotID)
	assertStatus(t, status, http.StatusNoContent, body)
	if got := hash(pilotID); got.Valid {
		t.Errorf("the pilot's password after remove-crew-password = %v, want none", got)
	}
	if readColumn[bool](ctx, t, s.db, "CrewSessionUsers", spanner.Key{pilotID}, "PasswordChangeRequired") {
		t.Error("the pilot is still marked to change a password they no longer hold")
	}
	if _, err := s.db.Single().ReadRow(ctx, "CrewSessionSignInAttempts", spanner.Key{"pilot"}, []string{"Failures"}); spanner.ErrCode(err) != codes.NotFound {
		t.Errorf("the pilot's count of failed sign-ins after the removal: err = %v, want not found", err)
	}
	// The list no longer holds the password, so the page offers no second removal.
	signsInThrough("the directory's identity alone", "Lodestar")
	// The sessions stand, the one the password established among them, and nothing is
	// asked of them now that the requirement went with the password.
	for name, b := range map[string]*browser{"the password established": pilot, "the directory established": sso} {
		if status, body := b.do(ctx, http.MethodGet, userDomains, nil); status != http.StatusOK {
			t.Errorf("the session %s after the removal: status %d, want 200: %s", name, status, body)
		}
	}
	// The password no longer signs the pilot in, and the refusal is a wrong password's.
	if status, body := stranger.login(ctx, "pilot", personaPassword); status != wrongStatus || !bytes.Equal(body, wrongBody) {
		t.Errorf("the pilot's password after the removal: status %d %s, want what a wrong password gets: %d %s", status, body, wrongStatus, wrongBody)
	}
	// The directory signs the pilot in, by the link, with nothing left to confirm.
	linked := primedBrowser(ctx, t, s)
	if status, location := linked.loginConsoleGoogle(ctx, "pilot"); status != http.StatusFound || location != consolePage {
		t.Fatalf("console Google login after the removal: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
	}
	if sess := linked.session(ctx); sess["authenticated"] != true || sess["username"] != "pilot" {
		t.Errorf("session through the directory after the removal = %v, want authenticated as pilot", sess)
	}

	// Nothing is left to remove, and the identity is now the pilot's last way in.
	if status, body := remove(hq, pilotID); status != http.StatusBadRequest {
		t.Errorf("remove-crew-password of a person with no password: status %d, want 400: %s", status, body)
	}
	status, body = hq.do(ctx, http.MethodGet, consoleAPI+"/crew-session-identities?limit=200&filter=userId:eq:"+pilotID, nil)
	assertStatus(t, status, http.StatusOK, body)
	identities := decodeRows(t, body)
	if len(identities) != 1 {
		t.Fatalf("the pilot's identities = %v, want the one the directory sign-in linked", identities)
	}
	unlink := []byte(`{"identityId":"` + cell[string](t, identities[0], "id") + `"}`)
	if status, body := hq.do(ctx, http.MethodPost, consoleAPI+"/unlink-crew-session-identity", unlink); status != http.StatusConflict {
		t.Errorf("unlinking the identity left after the removal: status %d, want 409: %s", status, body)
	}

	// A person removes their own where they change it: the route the methods answer
	// names, with the password as proof.
	status, body = stranger.do(ctx, http.MethodGet, consoleAPI+"/user/methods", nil)
	assertStatus(t, status, http.StatusOK, body)
	var methods struct {
		ChangePassword string `json:"changePassword"`
		RemovePassword string `json:"removePassword"`
		SignInMeans    string `json:"signInMeans"`
	}
	if err := json.Unmarshal(body, &methods); err != nil || methods.RemovePassword != consoleAPI+"/user/password/remove" {
		t.Fatalf("the methods answer names the removal at %q (%v), want %s/user/password/remove: %s", methods.RemovePassword, err, consoleAPI, body)
	}
	if methods.SignInMeans != consoleAPI+"/user/sign-in-means" {
		t.Fatalf("the methods answer names the read of what a person signs in with at %q, want %s/user/sign-in-means: %s", methods.SignInMeans, consoleAPI, body)
	}
	// signsInWith holds the read's whole answer for the browser's person: nothing of the
	// password but whether one is held.
	signsInWith := func(b *browser, state, want string) {
		t.Helper()

		status, body := b.do(ctx, http.MethodGet, methods.SignInMeans, nil)
		if got := strings.TrimSpace(string(body)); status != http.StatusOK || got != want {
			t.Errorf("what the marshal signs in with, holding %s: status %d %s, want 200 %s", state, status, got, want)
		}
	}
	const throughGoogle = `[{"name":"google","label":"Lodestar"}]`
	if status, body := stranger.do(ctx, http.MethodGet, methods.SignInMeans, nil); status != http.StatusUnauthorized || strings.Contains(string(body), "directories") {
		t.Errorf("the read of what a person signs in with, asked with no session: status %d, want 401 and nothing of anyone: %s", status, body)
	}
	own := func(b *browser, password string) (int, []byte) {
		proof, err := json.Marshal(map[string]string{"password": password})
		if err != nil {
			t.Fatal(err)
		}

		return b.do(ctx, http.MethodPost, methods.RemovePassword, proof)
	}
	marshal := newBrowser(t, s, consoleAPI)
	marshal.signIn(ctx, "marshal")
	marshalID := crewAccountID(ctx, t, hq, "marshal")
	signsInWith(marshal, "a password alone", `{"password":true,"directories":[]}`)
	status, body = own(marshal, personaPassword)
	assertStatus(t, status, http.StatusConflict, body)
	if !strings.Contains(string(body), "Your password is your only way to sign in") {
		t.Errorf("the marshal's own removal with no identity = %s, want it to say the password is the only way in", body)
	}
	linkThroughDirectory("marshal")
	signsInWith(marshal, "a password and the directory", `{"password":true,"directories":`+throughGoogle+`}`)
	if status, body := own(marshal, "not the password"); status != http.StatusBadRequest {
		t.Errorf("the marshal's own removal with a wrong password: status %d, want 400: %s", status, body)
	}
	if !hash(marshalID).Valid {
		t.Fatal("the marshal's password is gone after the refused removals")
	}
	status, body = own(marshal, personaPassword)
	assertStatus(t, status, http.StatusOK, body)
	if got := hash(marshalID); got.Valid {
		t.Errorf("the marshal's password after their own removal = %v, want none", got)
	}
	if sess := marshal.session(ctx); sess["authenticated"] != true || sess["username"] != "marshal" {
		t.Errorf("the marshal's session after their own removal = %v, want it to stand", sess)
	}
	signsInWith(marshal, "the directory alone", `{"password":false,"directories":`+throughGoogle+`}`)
	if status, body := stranger.login(ctx, "marshal", personaPassword); status != wrongStatus || !bytes.Equal(body, wrongBody) {
		t.Errorf("the marshal's password after the removal: status %d %s, want what a wrong password gets: %d %s", status, body, wrongStatus, wrongBody)
	}
}

// primedBrowser is a browser on the console that has loaded a page: it holds the
// anonymous session and the XSRF token a sign-in posts with.
func primedBrowser(ctx context.Context, t *testing.T, s *served) *browser {
	t.Helper()

	b := newBrowser(t, s, consoleAPI)
	if status, body := b.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("the session route before a login: status %d: %s", status, body)
	}

	return b
}
