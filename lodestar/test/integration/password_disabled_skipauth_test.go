//go:build skipAuth

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/lodestar/pkg/auth"
)

// TestPasswordDisabled_followsTheSectorsSwitch proves on the served stack that a person
// is told their password is disabled exactly where Bastion's directory-only switch
// refuses a password to them: in what they read of how they sign in, and in the first
// password they are refused.
//
// Bastion is directory only, and its directory is the persona provider of the test
// environments. The rookie, a cadet in every sector in the cast, is made one of Bastion's
// crew alone by the governor, and signs in through Bastion's directory. Every sector that
// seats him is directory only, he administers none of them and he holds no role outside
// them, so a password would not sign him in: the read of what he signs in with says his
// password is disabled, and he is refused the code of a first password and the password
// itself, with the reason, and nothing is mailed. The marshal's role at Bastion itself,
// which keeps its holder a way in with a password, makes the read say it is not; so does
// a role at Anvil, which is not directory only and admits him however he signs in; and
// so does the switch turned off, after which his request for the code is no longer
// refused for it. Each role taken back, the read says disabled again at once: the answer
// is the sign-in rule's, asked when the page reads it.
//
// Demonstrates: tenancy.directory-only, auth.persona.
func TestPasswordDisabled_followsTheSectorsSwitch(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{persona: &auth.Persona{Issuer: "https://persona.example.com", ClientID: "lodestar-pr12"}})
	if _, err := s.crew.SeedCast(ctx, io.Discard); err != nil {
		t.Fatalf("SeedCast() error = %v", err)
	}
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	rookieID := crewUserID(ctx, t, governor, "rookie").String()
	// role gives or takes a role of the rookie's as the governor, a super administrator.
	role := func(what, method string, body map[string]any) {
		t.Helper()

		if status, answer := governor.do(ctx, http.MethodPost, crewMethod(method), jsonBody(t, body)); status != http.StatusOK && status != http.StatusNoContent {
			t.Fatalf("%s as the governor: status %d: %s", what, status, answer)
		}
	}
	held := func(role, sector string) map[string]any {
		return map[string]any{"userId": rookieID, "role": role, "domain": sector}
	}

	// The rookie is one of Bastion's crew alone, and signs in through its directory.
	role("making the rookie a cadet at Bastion", "assign-crew-role", held("Cadet", bastion))
	role("taking the rookie's cadet role in every sector", "remove-crew-role", map[string]any{"userId": rookieID, "role": "Cadet", "everyDomain": true})
	rookie := newBrowser(t, s, consoleAPI)
	rookie.signInAsPersona(ctx, "rookie")

	disabled := func(state string, want bool) {
		t.Helper()

		status, body := rookie.do(ctx, http.MethodGet, consoleAPI+"/user/sign-in-means", nil)
		var means struct {
			PasswordDisabled *bool `json:"passwordDisabled"`
		}
		if status != http.StatusOK || json.Unmarshal(body, &means) != nil || means.PasswordDisabled == nil {
			t.Fatalf("reading what the rookie signs in with, %s: status %d: %s", state, status, body)
		}
		if *means.PasswordDisabled != want {
			t.Errorf("the rookie's password is disabled, %s = %v, want %v: %s", state, *means.PasswordDisabled, want, body)
		}
	}
	disabled("one of Bastion's crew alone while it is directory only", true)

	// He is refused a first password, the code and the password alike, before anything
	// is mailed: it would not sign him in.
	const refused = "Your organization does not sign in with a password here, so there is none to choose. Keep signing in the way your organization has set up"
	firstPassword := consoleAPI + "/user/first-password"
	s.codeMu.Lock()
	before := len(s.mail.Messages())
	status, body := rookie.do(ctx, http.MethodPost, firstPassword+"/code", nil)
	mailed := len(s.mail.Messages()) - before
	s.codeMu.Unlock()
	if status != http.StatusForbidden || !strings.Contains(string(body), refused) {
		t.Errorf("asking for the code of a first password as the rookie: status %d, want 403 saying %q: %s", status, refused, body)
	}
	if mailed != 0 {
		t.Errorf("asking for the code of a first password as the rookie mailed %d message(s), want none", mailed)
	}
	chosen := jsonBody(t, map[string]string{"code": "000000", "password": personaPassword})
	if status, body := rookie.do(ctx, http.MethodPost, firstPassword, chosen); status != http.StatusForbidden || !strings.Contains(string(body), refused) {
		t.Errorf("choosing a first password as the rookie: status %d, want 403 saying %q: %s", status, refused, body)
	}

	// Bastion's own marshal keeps the way in with a password.
	role("making the rookie a marshal of Bastion itself", "assign-crew-role", held("SectorMarshal", bastion))
	disabled("a marshal of Bastion itself", false)
	role("taking the marshal's role at Bastion back", "remove-crew-role", held("SectorMarshal", bastion))
	disabled("one of Bastion's crew alone again", true)

	// A sector that is not directory only admits him however he signs in.
	role("making the rookie a cadet at Anvil too", "assign-crew-role", held("Cadet", anvil))
	disabled("a cadet at Anvil too, which is not directory only", false)
	role("taking the cadet role at Anvil back", "remove-crew-role", held("Cadet", anvil))
	disabled("one of Bastion's crew alone once more", true)

	// The switch turned off, a password signs him in again.
	if status, body := governor.do(ctx, http.MethodPost, sectorPath(bastion, "end-sector-directory-only"), []byte(`{}`)); status != http.StatusOK {
		t.Fatalf("turning Bastion's directory only off as the governor: status %d, want 200: %s", status, body)
	}
	disabled("one of Bastion's crew alone, with its directory only turned off", false)
	if status, body := rookie.do(ctx, http.MethodPost, firstPassword+"/code", nil); status == http.StatusForbidden || strings.Contains(string(body), refused) {
		t.Errorf("asking for the code of a first password as the rookie, with Bastion's directory only turned off: status %d, want no refusal for a disabled password: %s", status, body)
	}
}
