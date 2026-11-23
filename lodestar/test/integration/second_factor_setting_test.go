//go:build skipAuth

package integration

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"google.golang.org/api/iterator"
)

// The routes of the crew's three sign-in settings methods, which an administrator's
// Sign-in page runs, and the table their changes are kept in.
var (
	requireCrewFactorRoute       = crewMethod("require-crew-second-factor")
	stopRequiringCrewFactorRoute = crewMethod("stop-requiring-crew-second-factor")
	crewRememberedBrowsersRoute  = crewMethod("set-crew-remembered-browsers")
)

const crewSignInSettingsTable = "CrewSessionSignInSettings"

// What the warning of StopRequiringCrewSecondFactor says, in the server's words, and the
// words of its button.
const (
	stopRequiringWarns  = "People will sign in with a password alone."
	stopRequiringButton = "Stop requiring it"
)

// TestSecondFactorSetting proves on the served stack that whether everyone is asked for
// a second factor is a setting of the running application: an administrator changes it
// through the crew's generated methods, and the next sign-in follows it, with no restart.
// The governor runs the methods, which the crew's role file grants to Headquarters.
//
// Stopped, a password sign-in gets its session with no code, and required again the
// next one is held once more. Stopped, a super administrator is still asked, and so is
// a sector's own marshal signing in with a password where the sector is directory only,
// whom the tenant policy holds for the code whatever the setting says. Each change is
// kept with who made it. A person whose roles grant none of the methods is refused each,
// and the row is as it was. With browsers not remembered, the code's step offers no
// remembering, a browser that asks for it all the same is not remembered, and its next
// sign-in is asked again; with 7 days, a browser remembered then skips the code. Each
// case puts the setting back as it starts, required with a browser remembered for 30
// days, when it ends.
//
// Demonstrates: auth.second-factor.
func TestSecondFactorSetting(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)

	t.Run("stopped, a password sign-in gets its session with no code", func(t *testing.T) {
		hq := settingCase(ctx, t, s)

		held := primedBrowser(ctx, t, s)
		if status, body := held.login(ctx, "cadet", personaPassword); status != http.StatusOK || !heldForFactor(t, body) {
			t.Fatalf("the cadet's password sign-in while the second factor is required: status %d: %s, want 200 holding the sign-in for the code", status, body)
		}

		status, body := askCrewWarning(ctx, hq, stopRequiringCrewFactorRoute, `{}`)
		var warning resource.Warning
		if err := json.Unmarshal(body, &warning); status != http.StatusOK || err != nil || warning.Consequence != stopRequiringWarns || warning.Confirm != stopRequiringButton {
			t.Fatalf("the warning of no longer requiring the second factor: status %d: %s, want %q on the button %q", status, body, stopRequiringWarns, stopRequiringButton)
		}
		runSettingMethod(ctx, t, hq, stopRequiringCrewFactorRoute, `{}`)

		free := primedBrowser(ctx, t, s)
		if status, body := free.login(ctx, "cadet", personaPassword); status != http.StatusOK || heldForFactor(t, body) {
			t.Fatalf("the cadet's password sign-in once the second factor is not required: status %d: %s, want 200 with no code asked", status, body)
		}
		if sess := free.session(ctx); sess["authenticated"] != true || sess["username"] != "cadet" {
			t.Fatalf("the session once the second factor is not required = %v, want the cadet signed in", sess)
		}
		if got, want := authEvents(ctx, t, s, "cadet"), []string{string(sessioninfo.MethodPassword)}; !slices.Equal(got, want) {
			t.Errorf("the cadet's auth events with no code asked = %v, want %v", got, want)
		}

		runSettingMethod(ctx, t, hq, requireCrewFactorRoute, `{}`)
		again := primedBrowser(ctx, t, s)
		if status, body := again.login(ctx, "cadet", personaPassword); status != http.StatusOK || !heldForFactor(t, body) {
			t.Fatalf("the cadet's password sign-in once the second factor is required again: status %d: %s, want 200 holding the sign-in for the code", status, body)
		}
	})

	t.Run("stopped, a super administrator and a directory-only sector's own marshal are still asked", func(t *testing.T) {
		hq := settingCase(ctx, t, s)
		atBastion := jsonBody(t, map[string]any{"userId": crewUserID(ctx, t, hq, "rigger").String(), "role": "SectorMarshal", "domain": bastion})
		if status, body := hq.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), atBastion); status != http.StatusNoContent {
			t.Fatalf("giving the rigger the marshal's role at Bastion: status %d: %s", status, body)
		}
		runSettingMethod(ctx, t, hq, stopRequiringCrewFactorRoute, `{}`)

		for _, user := range []string{"governor", "rigger"} {
			b := primedBrowser(ctx, t, s)
			if status, body := b.login(ctx, user, personaPassword); status != http.StatusOK || !heldForFactor(t, body) {
				t.Errorf("the %s's password sign-in once the second factor is not required: status %d: %s, want 200 holding the sign-in for the code", user, status, body)
			}
		}
	})

	t.Run("each change is kept with who made it", func(t *testing.T) {
		hq := settingCase(ctx, t, s)
		governorID := crewAccountID(ctx, t, hq, "governor")
		before := len(changeSources(ctx, t, s, crewSignInSettingsTable))
		calls := []struct{ route, body string }{
			{stopRequiringCrewFactorRoute, `{}`},
			{requireCrewFactorRoute, `{}`},
			{crewRememberedBrowsersRoute, `{"length":"7 days"}`},
		}
		for _, call := range calls {
			runSettingMethod(ctx, t, hq, call.route, call.body)
		}

		sources := changeSources(ctx, t, s, crewSignInSettingsTable)[before:]
		if len(sources) != len(calls) {
			t.Fatalf("the changes of the crew's sign-in settings = %q, want %d, one for each method run", sources, len(calls))
		}
		for i, source := range sources {
			if !strings.HasPrefix(source, "governor ("+governorID+"), session ") {
				t.Errorf("who made the change of %s = %q, want the governor", calls[i].route, source)
			}
		}
	})

	t.Run("a person whose roles grant none of the methods is refused each", func(t *testing.T) {
		settingCase(ctx, t, s)
		cadet := newBrowser(t, s, consoleAPI)
		cadet.signIn(ctx, "cadet")
		row, changes := settingsRow(ctx, t, s), len(changeSources(ctx, t, s, crewSignInSettingsTable))

		for _, call := range []struct{ route, body string }{
			{stopRequiringCrewFactorRoute, `{}`},
			{requireCrewFactorRoute, `{}`},
			{crewRememberedBrowsersRoute, `{"length":"7 days"}`},
		} {
			if status, body := cadet.do(ctx, http.MethodPost, call.route, []byte(call.body)); status != http.StatusForbidden {
				t.Errorf("POST %s as the cadet: status %d, want 403: %s", call.route, status, body)
			}
		}
		if got := settingsRow(ctx, t, s); got != row {
			t.Errorf("the crew's sign-in settings row after the refusals = %s, want it as it was, %s", got, row)
		}
		if got := len(changeSources(ctx, t, s, crewSignInSettingsTable)); got != changes {
			t.Errorf("the changes of the crew's sign-in settings after the refusals = %d, want the %d there were", got, changes)
		}
	})

	t.Run("not remembered, a browser is asked each time; 7 days, a remembered browser is not", func(t *testing.T) {
		hq := settingCase(ctx, t, s)
		remembering := func(b *browser) {
			t.Helper()

			s.codeMu.Lock()
			defer s.codeMu.Unlock()
			b.secondFactorLocked(ctx, "pilot", true)
		}

		runSettingMethod(ctx, t, hq, crewRememberedBrowsersRoute, `{"length":"Not remembered"}`)
		forgotten := primedBrowser(ctx, t, s)
		if status, body := forgotten.login(ctx, "pilot", personaPassword); status != http.StatusOK || !heldForFactor(t, body) {
			t.Fatalf("the pilot's password sign-in: status %d: %s, want 200 holding the sign-in for the code", status, body)
		}
		if rememberOffered(ctx, t, forgotten) {
			t.Errorf("the code's step while browsers are not remembered offers to remember the browser, want no offer")
		}
		remembering(forgotten)
		if cookie := forgotten.trustedCookie(); cookie != nil {
			t.Errorf("the browser holds the remembering cookie %q after a code verified asking to be remembered while browsers are not remembered, want none", cookie.Name)
		}
		forgotten.logout(ctx)
		if status, body := forgotten.login(ctx, "pilot", personaPassword); status != http.StatusOK || !heldForFactor(t, body) {
			t.Fatalf("the same browser's next sign-in while browsers are not remembered: status %d: %s, want 200 holding the sign-in for the code", status, body)
		}

		runSettingMethod(ctx, t, hq, crewRememberedBrowsersRoute, `{"length":"7 days"}`)
		kept := primedBrowser(ctx, t, s)
		if status, body := kept.login(ctx, "pilot", personaPassword); status != http.StatusOK || !heldForFactor(t, body) {
			t.Fatalf("the pilot's password sign-in from another browser: status %d: %s, want 200 holding the sign-in for the code", status, body)
		}
		if !rememberOffered(ctx, t, kept) {
			t.Errorf("the code's step while a browser is remembered for 7 days offers no remembering, want the offer")
		}
		remembering(kept)
		if kept.trustedCookie() == nil {
			t.Fatal("the browser holds no remembering cookie after a code verified asking to be remembered for 7 days, want one")
		}
		kept.logout(ctx)
		if status, body := kept.login(ctx, "pilot", personaPassword); status != http.StatusOK || heldForFactor(t, body) {
			t.Fatalf("the remembered browser's next sign-in: status %d: %s, want 200 with no code asked", status, body)
		}
		if got, want := authEvents(ctx, t, s, "pilot"), []string{string(sessioninfo.MethodPassword), string(sessioninfo.MethodTrustedDevice)}; !slices.Equal(got, want) {
			t.Errorf("the remembered browser's auth events = %v, want %v", got, want)
		}
	})
}

// settingCase starts one case of the setting's suite: the governor signed in on a
// browser of the case's own, the browser answered, and the setting put back as it
// starts when the case ends, required with a browser remembered for 30 days.
func settingCase(ctx context.Context, t *testing.T, s *served) *browser {
	t.Helper()

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	t.Cleanup(func() {
		for _, call := range []struct{ route, body string }{
			{requireCrewFactorRoute, `{}`},
			{crewRememberedBrowsersRoute, `{"length":"30 days"}`},
		} {
			if status, body := hq.do(ctx, http.MethodPost, call.route, []byte(call.body)); status != http.StatusNoContent {
				t.Errorf("putting the setting back, POST %s: status %d: %s", call.route, status, body)
			}
		}
	})

	return hq
}

// runSettingMethod runs one of the crew's sign-in settings methods as the browser's
// user, failing the test unless it answers 204.
func runSettingMethod(ctx context.Context, t *testing.T, b *browser, route, body string) {
	t.Helper()

	if status, answer := b.do(ctx, http.MethodPost, route, []byte(body)); status != http.StatusNoContent {
		t.Fatalf("POST %s: status %d: %s", route, status, answer)
	}
}

// rememberOffered reads whether the code's step this browser's sign-in waits on offers
// to remember the browser, as the pending status says it.
func rememberOffered(ctx context.Context, t *testing.T, b *browser) bool {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, b.prefix+"/user/pending", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s/user/pending: status %d: %s", b.prefix, status, body)
	}
	var pending struct {
		EmailCode *struct {
			RememberOffered bool `json:"rememberOffered"`
		} `json:"emailCode"`
	}
	if err := json.Unmarshal(body, &pending); err != nil || pending.EmailCode == nil {
		t.Fatalf("decoding the pending sign-in: %v: %s, want the code's step", err, body)
	}

	return pending.EmailCode.RememberOffered
}

// settingsRow reads the crew's sign-in settings row as the table holds it, in words:
// both fields and when it was last written, or that there is none.
func settingsRow(ctx context.Context, t *testing.T, s *served) string {
	t.Helper()

	iter := s.db.Client.Single().Query(ctx, cloudspanner.Statement{
		SQL: "SELECT RequireSecondFactor, RememberBrowserDays, UpdatedAt FROM " + crewSignInSettingsTable,
	})
	defer iter.Stop()
	row, err := iter.Next()
	if stderrors.Is(err, iterator.Done) {
		return "no row"
	}
	if err != nil {
		t.Fatalf("reading the crew's sign-in settings row: %v", err)
	}
	var (
		required  bool
		days      int64
		updatedAt time.Time
	)
	if err := row.Columns(&required, &days, &updatedAt); err != nil {
		t.Fatalf("reading the crew's sign-in settings row's columns: %v", err)
	}

	return fmt.Sprintf("required %t, a browser remembered for %d days, written %s", required, days, updatedAt.UTC().Format(time.RFC3339Nano))
}
