//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"google.golang.org/api/iterator"
)

// The routes of a sector's word for its directory's second factor, and the words the
// server says them in for Cinder, whose directory is the fleet's Workspace: example in the
// word's own sentences, and by its method's name too where a sentence says how to sign in.
const (
	vouchRoute          = "vouch-for-sector-directory-second-factor"
	stopVouchRoute      = "stop-vouching-for-sector-directory-second-factor"
	cinderDirectory     = "example"
	cinderDirectoryName = "Google Workspace " + cinderDirectory
	vouchWarns          = "You are saying that " + cinderDirectory + " asks its people for a second factor when they sign in. This application will stop asking them for a code."
	vouchButton         = "Stop asking people who sign in through " + cinderDirectory + " for a code"
	cinderVouches       = "This sector says that " + cinderDirectory + " asks its people for a second factor when they sign in, so they are not asked for a code."
	cinderAsksNobody    = "This application asks nobody for a second factor now, so there is nothing to turn off for people who sign in through " + cinderDirectory + "."
)

// TestDirectorySecondFactor proves on the served stack that a sector vouches for its own
// directory's second factor: only its marshal signed in through the directory says so,
// behind the warning; the directory's people are then not asked for the mailed code, in
// that sector and in another where they hold a role; a password sign-in and the governor
// are asked as before; and taken back, the directory's people are asked again at their
// next sign-in. The Workspace of these stacks attests to nothing, so what is not asked is
// not asked on the sector's word alone.
//
// A sector with no directory has nothing to vouch for, a role without the grant is
// refused the methods and the read, and where the application asks nobody for a second
// factor there is nothing to turn off. Each part has a served stack of its own, so the
// parts run side by side.
//
// Demonstrates: tenancy.directory-second-factor.
func TestDirectorySecondFactor(t *testing.T) {
	t.Parallel()

	t.Run("said through the directory, the word reaches its people wherever they work, and taken back holds from the next sign-in", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		s := newServedWith(ctx, t, servedOptions{crewNotAdministered: true})
		hq := newBrowser(t, s, consoleAPI)
		hq.signIn(ctx, "governor")

		// 1. Before anyone vouches, Cinder's directory seats the navigator at his first
		// sign-in through it, and he is held for the mailed code.
		first := primedBrowser(ctx, t, s)
		heldForCode := consoleLogin + "?pending=mfa&returnUrl=" + url.QueryEscape(consolePage)
		if status, location := first.loginConsoleGoogle(ctx, navigatorUser); status != http.StatusFound || location != heldForCode {
			t.Fatalf("the navigator's first sign-in through the Workspace before Cinder vouches: status %d to %q, want %d to %q", status, location, http.StatusFound, heldForCode)
		}
		first.secondFactor(ctx, navigatorUser)

		// 2. The governor makes the rigger a marshal of Cinder. Signed in with a password,
		// she is refused the word, warning and call, with the condition and what to do.
		atCinder := jsonBody(t, map[string]any{"userId": crewUserID(ctx, t, hq, "rigger").String(), "role": "SectorMarshal", "domain": cinder})
		if status, body := hq.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), atCinder); status != http.StatusNoContent {
			t.Fatalf("giving the rigger the marshal's role in Cinder: status %d: %s", status, body)
		}
		byPassword := newBrowser(t, s, consoleAPI)
		byPassword.signIn(ctx, "rigger")
		const onlyThrough = "Only a person who is signed in through this sector's directory, " + cinderDirectoryName + ", can say that it asks its people for a second factor. You signed in with a password. Sign in through " + cinderDirectoryName + " and try again."
		for _, warned := range []bool{true, false} {
			if status, body := askSectorMethod(ctx, t, byPassword, cinder, vouchRoute, warned); status != http.StatusConflict || refusalOf(t, body) != onlyThrough {
				t.Errorf("the rigger's word for Cinder signed in with a password (warning %t): status %d: %s, want %d %q", warned, status, body, http.StatusConflict, onlyThrough)
			}
		}
		if cinderVouchesRow(ctx, t, s) {
			t.Fatal("Cinder's directory row says the sector vouches after the refused call, want it as it was")
		}
		before := len(changeSources(ctx, t, s, "SectorDirectories"))

		// 3. Through the Workspace, her first time (she confirms the link with her password,
		// then gives the mailed code), she is warned in the item's words and says it.
		through := primedBrowser(ctx, t, s)
		confirming := consoleLogin + "?pending=confirmation&returnUrl=" + url.QueryEscape(consolePage)
		if status, location := through.loginConsoleGoogle(ctx, "rigger"); status != http.StatusFound || location != confirming {
			t.Fatalf("the rigger's first sign-in through the Workspace: status %d to %q, want %d to %q", status, location, http.StatusFound, confirming)
		}
		if status, body := through.do(ctx, http.MethodPost, consoleAPI+"/user/pending/confirm", jsonBody(t, map[string]string{"password": personaPassword})); status != http.StatusOK || !heldForFactor(t, body) {
			t.Fatalf("confirming the rigger's Workspace sign-in with her password: status %d: %s, want 200 holding the sign-in for the code", status, body)
		}
		through.secondFactor(ctx, "rigger")
		status, body := askSectorMethod(ctx, t, through, cinder, vouchRoute, true)
		var warning resource.Warning
		if err := json.Unmarshal(body, &warning); status != http.StatusOK || err != nil || warning.Consequence != vouchWarns || warning.Confirm != vouchButton {
			t.Fatalf("the warning of Cinder's word: status %d: %s, want %q on the button %q", status, body, vouchWarns, vouchButton)
		}
		methods, err := os.ReadFile(filepath.Join("..", "..", "web", "console", "src", "app", "core", "service", "zz_gen_methods.ts"))
		if err != nil {
			t.Fatal(err)
		}
		if want := "[Methods.VouchForSectorDirectorySecondFactor]: {\n    route: '" + vouchRoute + "',\n    dryRun: true,\n    warning: { typed: 'vouch' },"; !strings.Contains(string(methods), want) {
			t.Errorf("the console's metadata of the method does not name the word to type: want %q", want)
		}
		if status, body := askSectorMethod(ctx, t, through, cinder, vouchRoute, false); status != http.StatusOK {
			t.Fatalf("the rigger says Cinder's word through the Workspace: status %d: %s", status, body)
		}
		if part := secondFactorPart(ctx, t, through, cinder); part == nil || !part.Vouched || part.Says != cinderVouches {
			t.Errorf("Cinder's sign-in settings after the word = %+v, want vouched with %q", part, cinderVouches)
		}

		// 4. The navigator, given a role in Anvil too, signs in through the Workspace with no
		// step in between; his session records the sector's word under the Workspace's
		// connection, and he works in Anvil.
		inAnvil := jsonBody(t, map[string]any{"userId": crewUserID(ctx, t, hq, navigatorUser).String(), "role": "SectorCrew", "domain": anvil})
		if status, body := hq.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), inAnvil); status != http.StatusNoContent {
			t.Fatalf("giving the navigator a role in Anvil: status %d: %s", status, body)
		}
		nav := primedBrowser(ctx, t, s)
		if status, location := nav.loginConsoleGoogle(ctx, navigatorUser); status != http.StatusFound || location != consolePage {
			t.Errorf("the navigator's sign-in through the Workspace once Cinder vouches: status %d to %q, want %d to %q with no wait", status, location, http.StatusFound, consolePage)
		}
		if method, connection := lastCrewAuthEvent(ctx, t, s, navigatorUser); method != string(sessioninfo.MethodVouchedByTenant) || connection != "example" {
			t.Errorf("the last event of the navigator's sign-in = %q under %q, want %q under %q", method, connection, sessioninfo.MethodVouchedByTenant, "example")
		}
		if status, body := nav.do(ctx, http.MethodGet, sectorPath(anvil, "wings"), nil); status != http.StatusOK {
			t.Errorf("the navigator's request in Anvil, where he also holds a role: status %d: %s, want 200", status, body)
		}

		// 5. A password sign-in is still asked, and 6. so is the governor through the
		// Workspace.
		cadet := primedBrowser(ctx, t, s)
		if status, body := cadet.login(ctx, "cadet", personaPassword); status != http.StatusOK || !heldForFactor(t, body) {
			t.Errorf("the cadet's password sign-in once Cinder vouches: status %d: %s, want 200 holding the sign-in for the code", status, body)
		}
		governor := primedBrowser(ctx, t, s)
		if status, location := governor.loginConsoleGoogle(ctx, "governor"); status != http.StatusFound || location != confirming {
			t.Fatalf("the governor's sign-in through the Workspace: status %d to %q, want %d to %q", status, location, http.StatusFound, confirming)
		}
		if status, body := governor.do(ctx, http.MethodPost, consoleAPI+"/user/pending/confirm", jsonBody(t, map[string]string{"password": personaPassword})); status != http.StatusOK || !heldForFactor(t, body) {
			t.Errorf("the governor's Workspace sign-in once Cinder vouches: status %d: %s, want 200 holding the sign-in for the code", status, body)
		}

		// 7. Taken back on a plain call, the navigator's next sign-in is held again.
		if status, body := askSectorMethod(ctx, t, byPassword, cinder, stopVouchRoute, true); status != http.StatusBadRequest {
			t.Errorf("a request for the warning of taking Cinder's word back: status %d: %s, want %d: it declares none", status, body, http.StatusBadRequest)
		}
		if status, body := askSectorMethod(ctx, t, byPassword, cinder, stopVouchRoute, false); status != http.StatusOK {
			t.Fatalf("the rigger takes Cinder's word back, signed in with a password: status %d: %s", status, body)
		}
		again := primedBrowser(ctx, t, s)
		if status, location := again.loginConsoleGoogle(ctx, navigatorUser); status != http.StatusFound || location != heldForCode {
			t.Errorf("the navigator's sign-in through the Workspace once the word is taken back: status %d to %q, want %d to %q", status, location, http.StatusFound, heldForCode)
		}
		if part := secondFactorPart(ctx, t, byPassword, cinder); part == nil || part.Vouched {
			t.Errorf("Cinder's sign-in settings once the word is taken back = %+v, want not vouched", part)
		}

		// 8. The change record names the rigger for both changes, and the method each ran.
		riggerID := crewUserID(ctx, t, hq, "rigger").String()
		sources := changeSources(ctx, t, s, "SectorDirectories")[before:]
		if len(sources) != 2 {
			t.Fatalf("the changes of Cinder's directory row = %+v, want two: the word said and taken back", sources)
		}
		for i, method := range []string{"VouchForSectorDirectorySecondFactor", "StopVouchingForSectorDirectorySecondFactor"} {
			if differs := sources[i].differs(map[string]string{"ActorUsername": "rigger", "ActorUserId": riggerID, "SessionId": columnSet, "Rpc": method}); differs != "" {
				t.Errorf("who made change %d of Cinder's directory row: %s; want the rigger, her session and the method", i+1, differs)
			}
		}
	})

	t.Run("a sector with no directory has nothing to vouch for, and a role without the grant is refused", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		s := newServedWith(ctx, t, servedOptions{crewNotAdministered: true})
		hq := newBrowser(t, s, consoleAPI)
		hq.signIn(ctx, "governor")

		// 9. Anvil has no directory.
		const nothingToVouch = "This sector has no directory, so there is no directory to vouch for. Set its directory first: sign in through your organization's directory and make it this sector's directory."
		for _, warned := range []bool{true, false} {
			if status, body := askSectorMethod(ctx, t, hq, anvil, vouchRoute, warned); status != http.StatusConflict || refusalOf(t, body) != nothingToVouch {
				t.Errorf("the governor's word for Anvil (warning %t): status %d: %s, want %d %q", warned, status, body, http.StatusConflict, nothingToVouch)
			}
		}
		const nothingToTakeBack = "This sector has no directory, so there is nothing to take back."
		if status, body := askSectorMethod(ctx, t, hq, anvil, stopVouchRoute, false); status != http.StatusConflict || refusalOf(t, body) != nothingToTakeBack {
			t.Errorf("the governor takes Anvil's word back: status %d: %s, want %d %q", status, body, http.StatusConflict, nothingToTakeBack)
		}

		// 10. The navigator holds SectorCrew in Cinder, which grants neither method.
		nav := primedBrowser(ctx, t, s)
		heldForCode := consoleLogin + "?pending=mfa&returnUrl=" + url.QueryEscape(consolePage)
		if status, location := nav.loginConsoleGoogle(ctx, navigatorUser); status != http.StatusFound || location != heldForCode {
			t.Fatalf("the navigator's first sign-in through the Workspace: status %d to %q, want %d to %q", status, location, http.StatusFound, heldForCode)
		}
		nav.secondFactor(ctx, navigatorUser)
		for _, route := range []string{vouchRoute, stopVouchRoute} {
			for _, warned := range []bool{true, false} {
				if route == stopVouchRoute && warned {
					continue
				}
				if status, body := askSectorMethod(ctx, t, nav, cinder, route, warned); status != http.StatusForbidden {
					t.Errorf("the navigator asks %s in Cinder (warning %t): status %d: %s, want %d", route, warned, status, body, http.StatusForbidden)
				}
			}
		}
		if status, body := nav.do(ctx, http.MethodGet, sectorPath(cinder, "sign-in-settings"), nil); status != http.StatusForbidden {
			t.Errorf("the navigator reads Cinder's sign-in settings: status %d: %s, want %d", status, body, http.StatusForbidden)
		}
		if cinderVouchesRow(ctx, t, s) {
			t.Error("Cinder's directory row says the sector vouches after the refused calls, want it as it was")
		}
	})

	t.Run("where the application asks nobody for a second factor, there is nothing to turn off", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		s := newServedWith(ctx, t, servedOptions{crewNotAdministered: true})
		hq := settingCase(ctx, t, s)
		atCinder := jsonBody(t, map[string]any{"userId": crewUserID(ctx, t, hq, "rigger").String(), "role": "SectorMarshal", "domain": cinder})
		if status, body := hq.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), atCinder); status != http.StatusNoContent {
			t.Fatalf("giving the rigger the marshal's role in Cinder: status %d: %s", status, body)
		}

		// 11. The governor stops requiring the crew's second factor.
		runSettingMethod(ctx, t, hq, stopRequiringCrewFactorRoute, `{}`)
		through := primedBrowser(ctx, t, s)
		confirming := consoleLogin + "?pending=confirmation&returnUrl=" + url.QueryEscape(consolePage)
		if status, location := through.loginConsoleGoogle(ctx, "rigger"); status != http.StatusFound || location != confirming {
			t.Fatalf("the rigger's first sign-in through the Workspace: status %d to %q, want %d to %q", status, location, http.StatusFound, confirming)
		}
		if status, body := through.do(ctx, http.MethodPost, consoleAPI+"/user/pending/confirm", jsonBody(t, map[string]string{"password": personaPassword})); status != http.StatusOK {
			t.Fatalf("confirming the rigger's Workspace sign-in with her password: status %d: %s", status, body)
		}
		for _, warned := range []bool{true, false} {
			if status, body := askSectorMethod(ctx, t, through, cinder, vouchRoute, warned); status != http.StatusConflict || refusalOf(t, body) != cinderAsksNobody {
				t.Errorf("the rigger's word for Cinder where nobody is asked (warning %t): status %d: %s, want %d %q", warned, status, body, http.StatusConflict, cinderAsksNobody)
			}
		}
		if part := secondFactorPart(ctx, t, through, cinder); part == nil || part.Asked || part.Says != cinderAsksNobody {
			t.Errorf("Cinder's sign-in settings where nobody is asked = %+v, want not asked with %q", part, cinderAsksNobody)
		}
	})
}

// askSectorMethod asks one of a sector's own methods as the browser's user, for its
// warning where warned and as a call otherwise, and answers the status and the body.
func askSectorMethod(ctx context.Context, t *testing.T, b *browser, sector, route string, warned bool) (status int, body []byte) {
	t.Helper()

	var headers map[string]string
	if warned {
		headers = map[string]string{"X-Warning": "true"}
	}
	res := b.doWith(ctx, http.MethodPost, sectorPath(sector, route), []byte(`{}`), headers)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	return res.StatusCode, body
}

// directorySecondFactor is the second factor's part of a sector's sign-in settings, as
// the read says it.
type directorySecondFactor struct {
	Vouched bool   `json:"vouched"`
	Asked   bool   `json:"asked"`
	Says    string `json:"says"`
}

// secondFactorPart reads a sector's sign-in settings as the browser's user and answers
// their second factor's part, nil where the read says none.
func secondFactorPart(ctx context.Context, t *testing.T, b *browser, sector string) *directorySecondFactor {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, sectorPath(sector, "sign-in-settings"), nil)
	var read struct {
		SecondFactor *directorySecondFactor `json:"secondFactor"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &read) != nil {
		t.Fatalf("reading the sign-in settings of %s: status %d: %s", sector, status, body)
	}

	return read.SecondFactor
}

// cinderVouchesRow reads Cinder's directory row as the table holds it: whether the
// sector's word stands.
func cinderVouchesRow(ctx context.Context, t *testing.T, s *served) bool {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{
		SQL:    "SELECT AsksSecondFactor FROM SectorDirectories WHERE SectorId = @sector",
		Params: map[string]any{"sector": cinder},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("reading Cinder's directory row: %v", err)
	}
	var vouches bool
	if err := row.Columns(&vouches); err != nil {
		t.Fatalf("reading Cinder's directory row: %v", err)
	}

	return vouches
}

// lastCrewAuthEvent reads the last auth event of the newest crew session of username: its
// method and the connection it was recorded under.
func lastCrewAuthEvent(ctx context.Context, t *testing.T, s *served, username string) (method, connection string) {
	t.Helper()

	iter := s.db.Single().Query(ctx, spanner.Statement{
		SQL: `SELECT e.Method, e.Connection FROM CrewSessionAuthEvents e
			WHERE e.SessionId = (SELECT Id FROM CrewSessions WHERE Username = @username ORDER BY CreatedAt DESC LIMIT 1)
			ORDER BY e.Seq DESC LIMIT 1`,
		Params: map[string]any{"username": username},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if stderrors.Is(err, iterator.Done) {
		return "", ""
	}
	if err != nil {
		t.Fatalf("reading the last auth event of %s: %v", username, err)
	}
	var recorded spanner.NullString
	if err := row.Columns(&method, &recorded); err != nil {
		t.Fatalf("reading the last auth event of %s: %v", username, err)
	}

	return method, recorded.StringVal
}
