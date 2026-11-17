//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/resource"
)

// What the crew's Sign-in page lists and its dialog shows for the password method, in
// the server's words: the fleet has sectors, so a person who signs up charts one.
const (
	crewInviteOnlySays = "Invite only: a person signs in once they are invited."
	crewPasswordWarns  = "Anyone with an email address will be able to sign up and create a sector of their own, without an invitation."
	crewPasswordButton = "Open to anyone with an email address"
)

// TestSignUpChoice_openedAndClosedAtRunTime proves, on the served stack, that whether a
// sign-in method of the crew admits a person with no account and no invitation is an
// administrator's choice in the running application, and nothing in code.
//
// Every method starts invite only: the list the Sign-in page draws says so for each, the
// password method's sign-up routes are not found, the create-sector route refuses, and
// nothing is written. The governor, at Headquarters, opens the password method through
// the generated OpenCrewSignUp, which declares a warning: asked with X-Warning, the
// method's own route says who will be admitted and what they get, with the words for the
// confirm button, and writes nothing. A cadet, who holds no grant, reads neither the list
// nor the warning and is refused both methods. Once the method is open the list says so
// in the warning's words, the change record names the governor, and a stranger signs up
// from a mailed link, holds nothing, and charts a sector of their own. Made invite only
// again, the routes are not found, the create-sector route refuses the next person, and
// the person who signed up keeps their account and their sector and signs in as before.
//
// Demonstrates: @warning, auth.open-sign-up.
func TestSignUpChoice_openedAndClosedAtRunTime(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	governorID := crewAccountID(ctx, t, hq, "governor")
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")

	// Invite only, as every method starts: one row for each method the crew registers,
	// each with who it could admit.
	methods := crewSignInMethods(ctx, t, hq)
	if names := slices.Sorted(maps.Keys(methods)); !slices.Equal(names, []string{"google", "password", "sign-up"}) {
		t.Fatalf("the crew's sign-in methods = %v, want google, password and sign-up", names)
	}
	for _, method := range methods {
		if cell[bool](t, method, "openSignUp") || cell[string](t, method, "says") != crewInviteOnlySays {
			t.Errorf("the method %v before anyone chose, want invite only", method)
		}
	}
	if admits := cell[string](t, methods["sign-up"], "admits"); admits != "Anyone with a Google account" {
		t.Errorf("the sign-up registration admits %q, want anyone with a Google account: how wide its registration is", admits)
	}
	stranger := newBrowser(t, s, consoleAPI)
	if status, body := stranger.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the sign-up: status %d: %s", status, body)
	}
	accounts := tableRows(ctx, t, s, "CrewSessionUsers")
	if status, body := stranger.do(ctx, http.MethodPost, consoleAPI+"/user/sign-up", jsonBody(t, map[string]string{"email": "rover@example.com"})); status != http.StatusNotFound {
		t.Errorf("POST sign-up while the password method is invite only: status %d, want 404: %s", status, body)
	}
	if after := tableRows(ctx, t, s, "CrewSessionUsers"); after != accounts {
		t.Errorf("crew accounts after the refused sign-up = %d, want the %d there were: nothing is written", after, accounts)
	}
	if rows := tableRows(ctx, t, s, "CrewSessionSignInMethods"); rows != 0 {
		t.Errorf("rows of the sign-in methods table before anyone chose = %d, want none: no row is invite only", rows)
	}

	// The warning, in the server's words, and nothing written by asking.
	status, body := askCrewWarning(ctx, hq, openSignUpRoute, `{"name":"password"}`)
	var warning resource.Warning
	if err := json.Unmarshal(body, &warning); status != http.StatusOK || err != nil || warning.Consequence != crewPasswordWarns || warning.Confirm != crewPasswordButton {
		t.Fatalf("the warning of opening the password method: status %d: %s, want %q on the button %q", status, body, crewPasswordWarns, crewPasswordButton)
	}
	if rows := tableRows(ctx, t, s, "CrewSessionSignInMethods"); rows != 0 {
		t.Fatalf("rows of the sign-in methods table after the warning was asked = %d, want none: asking writes nothing", rows)
	}

	// A cadet holds no grant: no list, no warning, and both methods are refused.
	if status, body := cadet.do(ctx, http.MethodGet, consoleAPI+"/crew-sign-in-methods", nil); status != http.StatusForbidden {
		t.Errorf("GET crew-sign-in-methods as the cadet: status %d, want 403: %s", status, body)
	}
	if status, body := askCrewWarning(ctx, cadet, openSignUpRoute, `{"name":"password"}`); status != http.StatusForbidden {
		t.Errorf("the warning as the cadet: status %d, want 403: %s", status, body)
	}
	for _, route := range []string{openSignUpRoute, makeInviteOnlyRoute} {
		if status, body := cadet.do(ctx, http.MethodPost, route, []byte(`{"name":"password"}`)); status != http.StatusForbidden {
			t.Errorf("POST %s as the cadet: status %d, want 403: %s", route, status, body)
		}
	}

	// Opened: the list says so, in the warning's words, and the change record keeps who
	// opened the method.
	openCrewSignUp(ctx, t, hq, "password")
	methods = crewSignInMethods(ctx, t, hq)
	if !cell[bool](t, methods["password"], "openSignUp") || cell[string](t, methods["password"], "says") != "Open: "+crewPasswordWarns {
		t.Errorf("the password method once open = %v, want it open, saying what the warning said", methods["password"])
	}
	if cell[bool](t, methods["google"], "openSignUp") || cell[bool](t, methods["sign-up"], "openSignUp") {
		t.Errorf("the other methods once the password method is open = %v, want each invite only still", methods)
	}
	if sources := changeSources(ctx, t, s, "CrewSessionSignInMethods"); len(sources) != 1 || !strings.HasPrefix(sources[0], "governor ("+governorID+"), session ") {
		t.Errorf("who changed the crew's sign-in methods = %q, want the one change, by the governor", sources)
	}

	// A stranger signs up from the mailed link, holds nothing, and charts a sector.
	const email, username = "rover@example.com", "rover"
	if status, body := stranger.do(ctx, http.MethodPost, consoleAPI+"/user/sign-up", jsonBody(t, map[string]string{"email": email})); status != http.StatusOK {
		t.Fatalf("POST sign-up once the method is open: status %d: %s", status, body)
	}
	_, token := lastMailTo(t, s, email, "signUp")
	if status, body := stranger.do(ctx, http.MethodPost, consoleAPI+"/user/sign-up/complete", jsonBody(t, map[string]string{"token": token, "username": username, "password": signUpPassword})); status != http.StatusOK {
		t.Fatalf("POST sign-up/complete: status %d: %s", status, body)
	}
	stranger.secondFactor(ctx, username)
	if got := stranger.domains(ctx); len(got) != 0 {
		t.Fatalf("user-domains after the sign-up = %v, want none: a person who signs up holds no role", got)
	}
	status, body = stranger.do(ctx, http.MethodPost, createSectorRoute, []byte(chartEmber))
	if status != http.StatusOK {
		t.Fatalf("POST %s as the person who signed up: status %d: %s", createSectorRoute, status, body)
	}
	ember := createdKey(t, "charting Ember", body)

	// A second stranger asks for a link while the method is open, and holds it.
	late := newBrowser(t, s, consoleAPI)
	if status, body := late.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session: status %d: %s", status, body)
	}
	if status, body := late.do(ctx, http.MethodPost, consoleAPI+"/user/sign-up", jsonBody(t, map[string]string{"email": "drift@example.com"})); status != http.StatusOK {
		t.Fatalf("POST sign-up for the second stranger: status %d: %s", status, body)
	}
	_, lateToken := lastMailTo(t, s, "drift@example.com", "signUp")

	// Invite only again: a second change in the record, nobody signs up, the held link
	// creates nothing, and the person who signed up keeps their account and sector.
	makeCrewInviteOnly(ctx, t, hq, "password")
	if methods = crewSignInMethods(ctx, t, hq); cell[bool](t, methods["password"], "openSignUp") || cell[string](t, methods["password"], "says") != crewInviteOnlySays {
		t.Errorf("the password method once made invite only = %v, want invite only", methods["password"])
	}
	if sources := changeSources(ctx, t, s, "CrewSessionSignInMethods"); len(sources) != 2 {
		t.Errorf("changes to the crew's sign-in methods = %q, want two: opened, then made invite only", sources)
	}
	accounts = tableRows(ctx, t, s, "CrewSessionUsers")
	if status, body := late.do(ctx, http.MethodPost, consoleAPI+"/user/sign-up/complete", jsonBody(t, map[string]string{"token": lateToken, "username": "drift", "password": signUpPassword})); status != http.StatusNotFound {
		t.Errorf("completing a sign-up mailed while the method was open, after it was closed: status %d, want 404: %s", status, body)
	}
	if after := tableRows(ctx, t, s, "CrewSessionUsers"); after != accounts {
		t.Errorf("crew accounts after the refused sign-up = %d, want the %d there were", after, accounts)
	}
	if got, want := stranger.domains(ctx), []string{ember}; !slices.Equal(got, want) {
		t.Errorf("the sectors of the person who signed up, once the method is invite only = %v, want %v: they keep what they have", got, want)
	}
	again := newBrowser(t, s, consoleAPI)
	if status, body := again.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session: status %d: %s", status, body)
	}
	if status, body := again.login(ctx, username, signUpPassword); status != http.StatusOK {
		t.Errorf("the person who signed up, signing in once the method is invite only: status %d, want 200: %s", status, body)
	}
}

// crewSignInMethods reads the crew's sign-in methods as the Sign-in page lists them,
// each row under its method's registration name.
func crewSignInMethods(ctx context.Context, t *testing.T, b *browser) map[string]map[string]any {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, consoleAPI+"/crew-sign-in-methods", nil)
	assertStatus(t, status, http.StatusOK, body)
	methods := map[string]map[string]any{}
	for _, row := range decodeRows(t, body) {
		methods[cell[string](t, row, "name")] = row
	}

	return methods
}

// askCrewWarning asks a method for its warning in place of running it, the way the
// library's dialog does: the request goes to the method's own route with X-Warning.
func askCrewWarning(ctx context.Context, b *browser, route, body string) (status int, respBody []byte) {
	b.t.Helper()

	resp := b.doWith(ctx, http.MethodPost, route, []byte(body), map[string]string{resource.WarningHeader: "true"})
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}

	return resp.StatusCode, respBody
}

// openCrewSignUp opens a sign-in method of the crew to sign-up as the browser, through
// the generated method an administrator's Sign-in page runs.
func openCrewSignUp(ctx context.Context, t *testing.T, b *browser, method string) {
	t.Helper()

	if status, body := b.do(ctx, http.MethodPost, openSignUpRoute, jsonBody(t, map[string]string{"name": method})); status != http.StatusNoContent {
		t.Fatalf("POST %s for %s: status %d: %s", openSignUpRoute, method, status, body)
	}
}

// makeCrewInviteOnly makes a sign-in method of the crew invite only again as the browser.
func makeCrewInviteOnly(ctx context.Context, t *testing.T, b *browser, method string) {
	t.Helper()

	if status, body := b.do(ctx, http.MethodPost, makeInviteOnlyRoute, jsonBody(t, map[string]string{"name": method})); status != http.StatusNoContent {
		t.Fatalf("POST %s for %s: status %d: %s", makeInviteOnlyRoute, method, status, body)
	}
}

// tableRows counts the rows of a table of the served stack's database.
func tableRows(ctx context.Context, t *testing.T, s *served, table string) int64 {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{SQL: "SELECT COUNT(*) FROM " + table})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("counting the rows of %s: %v", table, err)
	}
	var n int64
	if err := row.Columns(&n); err != nil {
		t.Fatalf("reading the count of %s: %v", table, err)
	}

	return n
}

// changeSources lists who made each change the change record holds for a table, oldest
// first: the person, the id of their account and their session.
func changeSources(ctx context.Context, t *testing.T, s *served, table string) []string {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{
		SQL:    "SELECT EventSource FROM DataChangeEvents WHERE TableName = @table ORDER BY EventTime",
		Params: map[string]any{"table": table},
	})
	var sources []string
	if err := iter.Do(func(row *spanner.Row) error {
		var source string
		if err := row.Columns(&source); err != nil {
			return err
		}
		sources = append(sources, source)

		return nil
	}); err != nil {
		t.Fatalf("reading the change events of %s: %v", table, err)
	}

	return sources
}
