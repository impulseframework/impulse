//go:build skipAuth

package integration

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The two sentences a person whose every sector is directory only reads on the login
// page when their sign-in is refused: one after a password, one after a sign-in through
// Google or Microsoft with a personal account, which used no password.
const (
	directoryOnlyPasswordSentence = "Your organization does not sign in with a password here. Please sign in the way your organization has set up."
	directoryOnlyPersonalSentence = "Your organization admits only people who sign in with the account it gave them. Sign in with your organization's account, not a personal one."
)

// loginMessageEntry matches one entry of the browser library's table of login
// sentences: a refusal code and the sentence the login page shows for it, in either
// quote, on the code's line or on the one after.
var loginMessageEntry = regexp.MustCompile(`(?m)^\s*([a-z_]+):\s*(?:'((?:[^'\\\n]|\\.)*)'|"((?:[^"\\\n]|\\.)*)"),$`)

// loginSentence answers the sentence the console's login page shows for a refusal code,
// and whether it holds one: the page is the browser library's, which maps each code the
// session library sends to its sentence in one table (DEFAULT_LOGIN_MESSAGES), and
// Lodestar's console adds no sentence of its own. A code the table does not hold draws
// nothing on the page.
func loginSentence(t *testing.T, code string) (sentence string, held bool) {
	t.Helper()

	source, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "projects", "resource-angular", "types", "tokens.ts"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	_, table, found := strings.Cut(string(source), "export const DEFAULT_LOGIN_MESSAGES")
	if !found {
		t.Fatal("the browser library's tokens.ts declares no DEFAULT_LOGIN_MESSAGES")
	}
	table, _, _ = strings.Cut(table, "\n};")
	for _, entry := range loginMessageEntry.FindAllStringSubmatch(table, -1) {
		if entry[1] != code {
			continue
		}
		if entry[2] != "" {
			return strings.ReplaceAll(entry[2], `\'`, `'`), true
		}

		return strings.ReplaceAll(entry[3], `\"`, `"`), true
	}

	return "", false
}

// TestDirectoryOnlyRefusal_saysHowThePersonSignedIn holds what a person reads on the
// login page when every sector that seats them is directory only and their sign-in is
// refused, on the served stack: the words follow how they signed in. Bastion is
// directory only.
//
//   - after a password: the bosun, seated at Bastion alone, signs in with his password
//     and is told his organization does not sign in with a password here;
//   - after Google with a personal account: a person who signed up with a personal
//     Google account and was then seated at Bastion alone signs in through Google
//     again. They used no password, so the page names none: it says the organization
//     admits only people who sign in with the account it gave them, and to sign in with
//     the organization's account.
//
// Each case reads the code the server refuses with and the sentence the login page
// holds for that code. The refusal names nothing of the person or of the sector: a code
// per case, and the page's sentence.
//
// Demonstrates: tenancy.directory-only, auth.login-refusal-code.
func TestDirectoryOnlyRefusal_saysHowThePersonSignedIn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	reads := func(t *testing.T, how, code, want string) {
		t.Helper()
		got, held := loginSentence(t, code)
		if !held {
			t.Fatalf("the login page holds no sentence for the code %q the server refused %s with: it would show nothing", code, how)
		}
		if got != want {
			t.Errorf("the login page's sentence after %s (code %q) = %q, want %q", how, code, got, want)
		}
	}

	t.Run("after a password", func(t *testing.T) {
		t.Parallel()

		bosun := newBrowser(t, s, consoleAPI)
		if status, body := bosun.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
			t.Fatalf("GET session before the bosun's login: status %d: %s", status, body)
		}
		status, body := bosun.login(ctx, "bosun", personaPassword)
		if status != http.StatusUnauthorized {
			t.Fatalf("the bosun's password sign-in: status %d, want 401: %s", status, body)
		}
		var refusal struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(body, &refusal); err != nil {
			t.Fatalf("the bosun's refusal %s: %v", body, err)
		}
		if refusal.Code != string(sessioninfo.RefusedDirectoryOnly) {
			t.Errorf("the bosun's refusal code = %q, want %q", refusal.Code, sessioninfo.RefusedDirectoryOnly)
		}
		reads(t, "a password", refusal.Code, directoryOnlyPasswordSentence)
	})

	t.Run("after Google with a personal account", func(t *testing.T) {
		t.Parallel()

		const drifter = "drifter"
		openCrewSignUp(ctx, t, hq, crew.SignUpRegistration)
		first := newBrowser(t, s, consoleAPI)
		if status, body := first.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
			t.Fatalf("GET session before the drifter's sign-up: status %d: %s", status, body)
		}
		held := consoleLogin + "?pending=mfa&returnUrl=" + url.QueryEscape(consolePage)
		if status, location := first.loginConsoleSignUp(ctx, drifter); status != http.StatusFound || location != held {
			t.Fatalf("the drifter's sign-up with a personal Google account: status %d to %q, want %d to %q", status, location, http.StatusFound, held)
		}
		first.secondFactor(ctx, drifter)
		if got := first.domains(ctx); len(got) != 0 {
			t.Fatalf("the drifter's sectors after the sign-up = %v, want none", got)
		}
		if got := sessionDirectory(t, first.session(ctx)); got != nil {
			t.Fatalf("the drifter's session reports the directory %+v, want none: a personal account is no organization's directory", got)
		}

		// The governor seats the drifter at Bastion alone, which is directory only.
		seat := jsonBody(t, map[string]string{"userId": crewAccountID(ctx, t, hq, drifter), "role": "SectorCrew", "domain": bastion})
		if status, body := hq.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), seat); status != http.StatusNoContent {
			t.Fatalf("seating the drifter at Bastion: status %d: %s", status, body)
		}

		again := newBrowser(t, s, consoleAPI)
		if status, body := again.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
			t.Fatalf("GET session before the drifter's next sign-in: status %d: %s", status, body)
		}
		status, location := again.loginConsoleSignUp(ctx, drifter)
		if status != http.StatusFound {
			t.Fatalf("the drifter's sign-in through Google once Bastion alone seats them: status %d, want %d to the login page", status, http.StatusFound)
		}
		target, err := url.Parse(location)
		if err != nil || target.Path != consoleLogin {
			t.Fatalf("the drifter's refused sign-in went to %q (%v), want the login page %s with the refusal's code", location, err, consoleLogin)
		}
		code := target.Query().Get("code")
		if code != string(sessioninfo.RefusedDirectoryOnlyPersonalAccount) {
			t.Errorf("the drifter's refusal code = %q, want %q: they used no password", code, sessioninfo.RefusedDirectoryOnlyPersonalAccount)
		}
		if query := target.Query(); len(query) != 1 {
			t.Errorf("the refusal's address carries %v, want the code alone: nothing of the person or of the sector", query)
		}
		reads(t, "a sign-in through Google with a personal account", code, directoryOnlyPersonalSentence)
	})
}
