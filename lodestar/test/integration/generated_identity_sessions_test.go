// Demonstrates: auth.generated-identity, auth.trusted-device.
package integration

// generated_identity_sessions_test: the sessions as the generated management of the crew
// auth lists them for the Sessions page and for the list of live sessions under an
// account (CrewListedSessions), each saying whether it is live by the session library's
// own rule, and the count of the browsers remembered for each account on the accounts
// list (CrewListedUsers' trustedDevices), by the rule the person's own account page
// counts them by.
//
// The suites drive the lists as served, on the Spanner emulator: each is a query the
// generator writes in Spanner's SQL. The rows a sign-in does not make (a session idle
// past the timeout, one held as someone else, a remembered browser) are written straight
// to the tables with the times each case needs.

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// crewListedSessionsPath is the route of the crew's sessions as a page lists them.
const crewListedSessionsPath = "crew-listed-sessions"

// listedSessions reads the crew's sessions as the browser's user, filtered by the
// filter, in the order served.
func listedSessions(ctx context.Context, t *testing.T, b *browser, filter string) []map[string]any {
	t.Helper()

	status, body := b.do(ctx, http.MethodGet, consoleAPI+"/"+crewListedSessionsPath+"?limit=200&filter="+url.QueryEscape(filter), nil)
	assertStatus(t, status, http.StatusOK, body)

	return decodeRows(t, body)
}

// TestGeneratedIdentitySessionsSayWhichAreLive holds whether a session is live, on the
// served stack with the real session manager and its timeout: a session a sign-in made
// is live; one idle past the timeout is neither live nor ended; one ended is not live;
// one held as the person whose impersonation record has ended, or is past its time, is
// not live, and one whose record stands is. The list under a person reads the live ones
// alone, and the Sessions page filtered by the username reads every one of the person's,
// the most recently used first. A row offers the method that ends it, and once ended says
// so. A role that holds neither list of the sessions is refused the one a page shows as
// it is refused the table's.
func TestGeneratedIdentitySessionsSayWhichAreLive(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	pilot := newBrowser(t, s, consoleAPI)
	pilot.signIn(ctx, "pilot")
	pilotID := crewAccountID(ctx, t, hq, "pilot")
	signedIn := listedSessions(ctx, t, hq, "userId:eq:"+pilotID)
	if len(signedIn) != 1 {
		t.Fatalf("the pilot's sessions after one sign-in = %v, want the one", signedIn)
	}
	signedInID := cell[string](t, signedIn[0], "id")

	// The rows no sign-in makes, each used a minute before the next: the session idle
	// past the stack's timeout, the one ended, and three held as the pilot by the
	// governor, whose records have ended, are past their time and stand.
	now := time.Now().UTC()
	type written struct {
		name     string
		used     time.Time
		expired  bool
		record   bool
		ended    bool
		lasts    time.Duration
		wantLive bool
	}
	cases := []written{
		{name: "ended", used: now.Add(-time.Minute), expired: true},
		{name: "held as the pilot, its record ended", used: now.Add(-2 * time.Minute), record: true, ended: true, lasts: time.Hour},
		{name: "held as the pilot, its record past its time", used: now.Add(-3 * time.Minute), record: true, lasts: -time.Minute},
		{name: "held as the pilot, its record standing", used: now.Add(-4 * time.Minute), record: true, lasts: time.Hour, wantLive: true},
		{name: "idle past the timeout", used: now.Add(-servedSessionTimeout - time.Minute)},
	}
	ids := make(map[string]string, len(cases))
	var rows []*spanner.Mutation
	for _, c := range cases {
		id := impulse.Must(impulse.NewUUID()).String()
		ids[c.name] = id
		rows = append(rows, spanner.InsertMap("CrewSessions", map[string]any{
			"Id": id, "Username": "pilot", "UserId": pilotID, "Expired": c.expired,
			"AuthenticatedAt": c.used, "CreatedAt": c.used, "UpdatedAt": c.used,
		}))
		if !c.record {
			continue
		}
		record := map[string]any{
			"SessionId": id, "ActorUsername": "governor", "PrincipalKind": "User", "PrincipalUserId": pilotID, "PrincipalUsername": "pilot",
			"Reason": "walkthrough", "StartedAt": c.used, "ExpiresAt": now.Add(c.lasts),
		}
		if c.ended {
			record["EndedAt"] = now.Add(-30 * time.Second)
			record["EndReason"] = string(sessioninfo.ImpersonationEndedByRelease)
		}
		rows = append(rows, spanner.InsertMap("CrewSessionImpersonations", record))
	}
	if _, err := s.db.Apply(ctx, rows); err != nil {
		t.Fatalf("writing the pilot's sessions: %v", err)
	}

	listed := rowsByID(t, listedSessions(ctx, t, hq, "userId:eq:"+pilotID), "id")
	if row := listed[signedInID]; row == nil || cell[bool](t, row, "live") != true || cell[bool](t, row, "expired") != false {
		t.Errorf("the session the pilot's sign-in made = %v, want live and not ended", row)
	}
	for _, c := range cases {
		row := listed[ids[c.name]]
		if row == nil {
			t.Errorf("the pilot's session %s is not listed", c.name)

			continue
		}
		if got := cell[bool](t, row, "live"); got != c.wantLive {
			t.Errorf("the pilot's session %s says live = %t, want %t", c.name, got, c.wantLive)
		}
		if got := cell[bool](t, row, "expired"); got != c.expired {
			t.Errorf("the pilot's session %s says expired = %t, want %t", c.name, got, c.expired)
		}
	}

	// The list under the pilot reads the live ones alone; the Sessions page filtered by
	// the username reads all six, the most recently used first.
	var live []string
	for _, row := range listedSessions(ctx, t, hq, "userId:eq:"+pilotID+",live:eq:true") {
		live = append(live, cell[string](t, row, "id"))
	}
	if want := []string{signedInID, ids["held as the pilot, its record standing"]}; !slices.Equal(live, want) {
		t.Errorf("the pilot's live sessions = %v, want %v", live, want)
	}
	var every []string
	for _, row := range listedSessions(ctx, t, hq, "username:eq:pilot") {
		every = append(every, cell[string](t, row, "id"))
	}
	want := []string{signedInID}
	for _, c := range cases {
		want = append(want, ids[c.name])
	}
	if !slices.Equal(every, want) {
		t.Errorf("the pilot's sessions on the Sessions page = %v, want %v, the most recently used first", every, want)
	}

	// The row offers headquarters the method that ends the session, and the session's
	// row says it ended once it has.
	status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/"+crewListedSessionsPath+"/"+signedInID+"?capabilities=Execute", nil)
	assertStatus(t, status, http.StatusOK, body)
	caps, _ := decodeRow(t, body)["zzCapabilities"].(map[string]any)
	if offered, _ := caps["Execute"].([]any); !slices.Contains(offered, any("EndCrewSession")) {
		t.Errorf("the pilot's live session offers headquarters %v, want EndCrewSession among them", offered)
	}
	status, body = hq.do(ctx, http.MethodPost, consoleAPI+"/end-crew-session", []byte(`{"sessionId":"`+signedInID+`"}`))
	assertStatus(t, status, http.StatusNoContent, body)
	status, body = hq.do(ctx, http.MethodGet, consoleAPI+"/"+crewListedSessionsPath+"/"+signedInID, nil)
	assertStatus(t, status, http.StatusOK, body)
	if row := decodeRow(t, body); cell[bool](t, row, "expired") != true || cell[bool](t, row, "live") != false {
		t.Errorf("the pilot's session after end-crew-session = %v, want ended and not live", row)
	}

	// The cadet's role holds neither list of the sessions: the list a page shows refuses
	// her as the table's does.
	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	table, _ := cadet.do(ctx, http.MethodGet, consoleAPI+"/crew-sessions?limit=200", nil)
	status, body = cadet.do(ctx, http.MethodGet, consoleAPI+"/"+crewListedSessionsPath+"?limit=200", nil)
	if status != http.StatusForbidden || table != http.StatusForbidden {
		t.Errorf("the cadet's list of the sessions: status %d, and of the table's %d, want both 403: %s", status, table, body)
	}
}

// TestGeneratedIdentityAccountsCountTrustedDevices holds the count of the browsers
// remembered for each account on the accounts list: a device that stands counts, and
// one revoked, one past its time, one remembered longer ago than the days the sign-in
// settings say and another person's do not; an account with none counts none; with
// browsers not remembered nobody counts any; and with no settings row the session
// library's 30 days hold. On the served stack the count is what the person's own account
// page says of their remembered browsers.
func TestGeneratedIdentityAccountsCountTrustedDevices(t *testing.T) {
	t.Parallel()

	t.Run("by the sign-in settings", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		w := newTenantListsWorld(t)

		now := time.Now().UTC()
		day := 24 * time.Hour
		var devices []*spanner.Mutation
		device := func(name string, remembered, expires time.Duration, revoked bool) {
			id := impulse.Must(impulse.NewUUID()).String()
			hash := sha256.Sum256([]byte(id))
			columns := map[string]any{
				"Id": id, "UserId": w.ids[name], "TokenHash": hash[:], "CreatedAt": now.Add(-remembered), "ExpiresAt": now.Add(expires),
			}
			if revoked {
				columns["RevokedAt"] = now.Add(-time.Hour)
			}
			devices = append(devices, spanner.InsertMap("CrewSessionTrustedDevices", columns))
		}
		// Ada's device that stands, one forgotten, one past its time and one remembered
		// eight days ago; bo's that stands; cy's remembered 29 and 31 days ago, neither
		// past its time; di has none.
		device("ada", day, 29*day, false)
		device("ada", day, 29*day, true)
		device("ada", 2*day, -time.Hour, false)
		device("ada", 8*day, 22*day, false)
		device("bo", day, 29*day, false)
		device("cy", 29*day, day, false)
		device("cy", 31*day, day, false)
		if _, err := w.db.Apply(ctx, devices); err != nil {
			t.Fatalf("writing the devices: %v", err)
		}
		settings := func(days int64) {
			t.Helper()
			if _, err := w.db.Apply(ctx, []*spanner.Mutation{spanner.InsertOrUpdateMap("CrewSessionSignInSettings", map[string]any{
				"Name": session.SignInSettingsRowName, "RequireSecondFactor": true, "RememberBrowserDays": days, "UpdatedAt": spanner.CommitTimestamp,
			})}); err != nil {
				t.Fatalf("writing the sign-in settings: %v", err)
			}
		}
		counts := func(when string, want map[string]float64) {
			t.Helper()
			listed := listedAccounts(t, w.h, "governor", "")
			for name, n := range want {
				row, ok := listed[name]
				if !ok {
					t.Fatalf("the accounts list does not carry %s", name)
				}
				if got := cell[float64](t, row, "trustedDevices"); got != n {
					t.Errorf("%s, %s's row counts %v trusted devices, want %v", when, name, got, n)
				}
			}
		}

		settings(7)
		counts("with a browser remembered for 7 days", map[string]float64{"ada": 1, "bo": 1, "cy": 0, "di": 0})
		settings(0)
		counts("with browsers not remembered", map[string]float64{"ada": 0, "bo": 0, "cy": 0, "di": 0})
		if _, err := w.db.Apply(ctx, []*spanner.Mutation{spanner.Delete("CrewSessionSignInSettings", spanner.Key{session.SignInSettingsRowName})}); err != nil {
			t.Fatalf("deleting the sign-in settings: %v", err)
		}
		counts("with no settings row", map[string]float64{"ada": 2, "bo": 1, "cy": 1, "di": 0})
	})

	t.Run("as the person's own account page counts them", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		s := newServed(ctx, t)

		// The cadet's first sign-in on the stack asks for her browser to be remembered.
		hq := newBrowser(t, s, consoleAPI)
		hq.signIn(ctx, "governor")
		cadet := newBrowser(t, s, consoleAPI)
		cadet.signIn(ctx, "cadet")
		// Her row, then where she is signed in as her own account page reads it, one
		// after the other.
		status, body := hq.do(ctx, http.MethodGet, consoleAPI+"/"+crewListedAccountsPath+"?limit=200&filter="+url.QueryEscape("username:eq:cadet"), nil)
		ownStatus, ownBody := cadet.do(ctx, http.MethodGet, consoleAPI+"/user/sessions", nil)
		assertStatus(t, status, http.StatusOK, body)
		assertStatus(t, ownStatus, http.StatusOK, ownBody)
		rows := decodeRows(t, body)
		if len(rows) != 1 {
			t.Fatalf("the accounts filtered by the cadet's username = %v, want her row", rows)
		}
		got, own := cell[float64](t, rows[0], "trustedDevices"), cell[float64](t, decodeRow(t, ownBody), "rememberedBrowsers")
		if got != own || own != 1 {
			t.Errorf("the cadet's row counts %v trusted devices and her own account page %v, want 1 on both", got, own)
		}
	})
}
