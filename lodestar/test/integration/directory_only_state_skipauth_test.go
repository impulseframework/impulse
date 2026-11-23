//go:build skipAuth

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"testing"
)

// TestDirectoryOnlyState_saysHowTheSectorStands proves on the served stack that the read
// of a sector's directory-only switch, which the Sign-in settings page draws from, names
// the sector's directory as people know it: Cinder's is the fleet's Google Workspace,
// whose identities carry the connection example. It also counts the sector's people as
// the warning of the method that turns the switch on counts them. The governor signs in
// through Cinder's directory, his password confirming the identity that carries his
// login, so he may turn Cinder's switch on and ask its warning. The warning's sentence
// counts the people of every auth Cinder seats and names the ones he may list, the
// crew's; the portal's members are counted and not named to him. The read says as many
// are kept out as the sentence counts, of at least as many who hold a role in Cinder.
func TestDirectoryOnlyState_saysHowTheSectorStands(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	if _, err := s.crew.SeedCast(ctx, io.Discard); err != nil {
		t.Fatalf("SeedCast() error = %v", err)
	}
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")

	status, body := governor.do(ctx, http.MethodGet, sectorPath(cinder, "directory-only"), nil)
	var read struct {
		Directory string `json:"directory"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &read) != nil {
		t.Fatalf("reading Cinder's switch as the governor: status %d: %s", status, body)
	}
	if want := "Google Workspace example"; read.Directory != want {
		t.Errorf("the directory Cinder's switch names = %q, want %q", read.Directory, want)
	}

	through := newBrowser(t, s, consoleAPI)
	if status, body := through.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the governor's directory login: status %d: %s", status, body)
	}
	status, location := through.loginConsoleGoogle(ctx, "governor")
	if want := consoleLogin + "?pending=confirmation&returnUrl=" + url.QueryEscape(consolePage); status != http.StatusFound || location != want {
		t.Fatalf("the governor's console Google login: status %d to %q, want %d to %q", status, location, http.StatusFound, want)
	}
	confirmation, err := json.Marshal(map[string]string{"password": personaPassword})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := through.do(ctx, http.MethodPost, consoleAPI+"/user/pending/confirm", confirmation); status != http.StatusOK {
		t.Fatalf("confirming the governor's directory sign-in with his password: status %d: %s", status, body)
	}

	status, body = through.do(ctx, http.MethodGet, sectorPath(cinder, "directory-only"), nil)
	var counted struct {
		People *struct {
			Seated  int `json:"seated"`
			Outside int `json:"outside"`
		} `json:"people"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &counted) != nil || counted.People == nil {
		t.Fatalf("reading Cinder's switch as the governor, through its directory: status %d, want 200 with the people counted: %s", status, body)
	}
	res := through.doWith(ctx, http.MethodPost, sectorPath(cinder, "make-sector-directory-only"), []byte(`{}`), map[string]string{"X-Warning": "true"})
	body, err = io.ReadAll(res.Body)
	if closeErr := res.Body.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	var warning struct {
		Consequence string   `json:"consequence"`
		Names       []string `json:"names"`
	}
	if res.StatusCode != http.StatusOK || json.Unmarshal(body, &warning) != nil {
		t.Fatalf("asking what directory only would do to Cinder, through its directory: status %d: %s", res.StatusCode, body)
	}
	said := regexp.MustCompile(`(\d+) (?:people|person) seated here signs? in another way and will lose access\.`).FindStringSubmatch(warning.Consequence)
	if said == nil || len(warning.Names) == 0 {
		t.Fatalf("the warning of turning Cinder directory only = %q naming %v, want how many people lose access and the ones the governor may list", warning.Consequence, warning.Names)
	}
	outside, err := strconv.Atoi(said[1])
	if err != nil {
		t.Fatal(err)
	}
	if counted.People.Outside != outside {
		t.Errorf("the read says %d of Cinder's people would be kept out, and the warning counts %d (%q), want the same people", counted.People.Outside, outside, warning.Consequence)
	}
	if len(warning.Names) > outside {
		t.Errorf("the warning names %d people (%v) and counts %d, want the names among the people counted", len(warning.Names), warning.Names, outside)
	}
	if counted.People.Seated < counted.People.Outside {
		t.Errorf("the read says %d people hold a role in Cinder and %d of them would be kept out, want at least as many holding a role as kept out", counted.People.Seated, counted.People.Outside)
	}
}
