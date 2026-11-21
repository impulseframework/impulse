// Demonstrates: condition.time-of-day, condition.day-of-week, condition.local-zone.
package integration

// temporal_test (design plan §9): the shipped shift personas pin fixed views only by
// pairing — at any hour exactly one of Dara and Nadia sees the hangar deck — so the
// suite also provisions shift roles through the real engine with windows computed
// around its own instant: the on-shift role's Refits list answers and the off-shift
// role's refuses at any hour; a wrap-around window holds across midnight; dayOfWeek
// refuses on a computed weekend and admits on a computed weekday; the digest reads
// conditional for every windowed grant; a local condition in an app that wires no
// zone fails its first check loudly.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// timeOfDayWindow renders a two-sided timeOfDay window covering
// [center-halfWidth, center+halfWidth) in the given wall clock, in the OR form when
// it crosses midnight — so the expression is correct at any hour the suite runs.
func timeOfDayWindow(zoneArg string, center time.Time, halfWidth time.Duration) string {
	start := center.Add(-halfWidth).Format("15:04")
	end := center.Add(halfWidth).Format("15:04")
	if start < end {
		return fmt.Sprintf("timeOfDay(now, %s) >= '%s' AND timeOfDay(now, %s) < '%s'", zoneArg, start, zoneArg, end)
	}

	return fmt.Sprintf("(timeOfDay(now, %s) >= '%s' OR timeOfDay(now, %s) < '%s')", zoneArg, start, zoneArg, end)
}

var dayNames = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// dayNeighborhood renders a dayOfWeek membership covering the instant's day and both
// neighbors, so a suite straddling midnight stays inside it.
func dayNeighborhood(zoneArg string, at time.Time) string {
	day := int(at.Weekday())

	return fmt.Sprintf("dayOfWeek(now, %s) IN ('%s', '%s', '%s')", zoneArg, dayNames[(day+6)%7], dayNames[day], dayNames[(day+1)%7])
}

// awayDays renders a dayOfWeek membership that excludes the instant's day and both
// neighbors — a computed weekend the instant can never fall on.
func awayDays(zoneArg string, at time.Time) string {
	day := int(at.Weekday())

	return fmt.Sprintf("dayOfWeek(now, %s) IN ('%s', '%s')", zoneArg, dayNames[(day+3)%7], dayNames[(day+4)%7])
}

// provisionTemporalAccess opens an engine whose role file is the scripted shift roles,
// validated the way the production auths validate theirs, assigns the users their roles
// at Anvil, and returns the live engine.
func provisionTemporalAccess(ctx context.Context, t *testing.T, db *initiator.SpannerDB, conf *access.RoleConfig, users map[accesstypes.User][]accesstypes.Role, probe accesstypes.User) *access.Client {
	t.Helper()

	file, err := json.Marshal(conf)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	client := newAccessClient(t, db, file)
	for user, roles := range users {
		if err := client.UserManager().AddUserRoles(ctx, accesstypes.DomainPolicyScope(anvil), user, roles...); err != nil {
			t.Fatalf("AddUserRoles(%s) error = %v", user, err)
		}
	}

	opsClock, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	checker := client.ForUser(probe)
	deadline := time.Now().Add(15 * time.Second)
	for {
		decisions, err := checker.Check(ctx, accesstypes.NewEnvironment().WithNow(time.Now()).WithZone(opsClock),
			accesstypes.DomainScope(anvil), accesstypes.List, "Refits")
		if err == nil && !decisions["Refits"].IsDenied() {
			return client
		}
		if time.Now().After(deadline) {
			t.Fatalf("policy snapshot never became visible; last decisions: %v (err %v)", decisions, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestTemporalWindows(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}

	opsClock, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	nowLocal := time.Now().In(opsClock)

	refitFields := []accesstypes.Tag{"shipId", "statusId", "estimate"}
	taskFields := []accesstypes.Tag{"notes"}
	conf := &access.RoleConfig{Roles: access.ScopedRoles{Domain: []*access.Role{
		{
			Name:        "OnShift",
			Description: "Works the refits during the shift.",
			Permissions: map[accesstypes.Permission][]access.Grant{
				accesstypes.List: {{
					Resource: "Refits", Fields: refitFields,
					Condition: timeOfDayWindow("local", nowLocal, 2*time.Hour) + " AND " + dayNeighborhood("'UTC'", time.Now().UTC()),
				}},
				accesstypes.Update: {{Resource: "RefitTasks", Fields: taskFields, Condition: dayNeighborhood("local", nowLocal) + " AND state = 'in_refit'"}},
			},
		},
		{
			Name:        "OffShift",
			Description: "Works the refits off the shift.",
			Permissions: map[accesstypes.Permission][]access.Grant{
				accesstypes.List: {{
					Resource: "Refits", Fields: refitFields,
					Condition: timeOfDayWindow("'America/Denver'", nowLocal.Add(12*time.Hour), 2*time.Hour),
				}},
				accesstypes.Update: {{Resource: "RefitTasks", Fields: taskFields, Condition: awayDays("local", nowLocal) + " AND state = 'in_refit'"}},
			},
		},
		{
			// A window straddling midnight around the instant: the wrap-around OR
			// form must hold across the boundary.
			Name:        "Midnight",
			Description: "Works the refits across midnight.",
			Permissions: map[accesstypes.Permission][]access.Grant{
				accesstypes.List: {{
					Resource: "Refits", Fields: refitFields,
					Condition: timeOfDayWindow("local", nowLocal, 11*time.Hour),
				}},
			},
		},
	}}}

	client := provisionTemporalAccess(ctx, t, db, conf, map[accesstypes.User][]accesstypes.Role{
		"shift-on":       {"OnShift"},
		"shift-off":      {"OffShift"},
		"shift-midnight": {"Midnight"},
	}, "shift-on")
	h := newTestAppWithAccess(db, client)

	tests := []struct {
		name       string
		user       accesstypes.User
		method     string
		target     string
		body       string
		wantStatus int
		wantRows   bool
		wantDigest accesstypes.DigestState
	}{
		{
			name:       "the on-shift window is open now",
			user:       "shift-on",
			target:     sectorPath(anvil, "refits"),
			wantStatus: http.StatusOK,
			wantRows:   true,
		},
		{
			name:       "the off-shift window is closed now",
			user:       "shift-off",
			target:     sectorPath(anvil, "refits"),
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "a wrap-around window holds across midnight",
			user:       "shift-midnight",
			target:     sectorPath(anvil, "refits"),
			wantStatus: http.StatusOK,
			wantRows:   true,
		},
		{
			name:       "dayOfWeek admits on a computed weekday beside a state term",
			user:       "shift-on",
			method:     http.MethodPatch,
			target:     "/console/api/resources",
			body:       fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"notes":"checked on shift"}}]`, opPath(anvil, "refit-tasks/"+refitSamaritanID+"/2")),
			wantStatus: http.StatusOK,
		},
		{
			name:       "the state term beside a folded temporal term still refuses",
			user:       "shift-on",
			method:     http.MethodPatch,
			target:     "/console/api/resources",
			body:       fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"notes":"not in refit"}}]`, opPath(anvil, "refit-tasks/"+refitMuleID+"/1")),
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "dayOfWeek refuses on a computed weekend",
			user:       "shift-off",
			method:     http.MethodPatch,
			target:     "/console/api/resources",
			body:       fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"notes":"weekend"}}]`, opPath(anvil, "refit-tasks/"+refitSamaritanID+"/2")),
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "the on-shift digest reads conditional at any hour",
			user:       "shift-on",
			target:     "/console/api/permission-digest?domain=" + anvil,
			wantStatus: http.StatusOK,
			wantDigest: accesstypes.DigestConditional,
		},
		{
			name:       "the off-shift digest reads conditional at any hour",
			user:       "shift-off",
			target:     "/console/api/permission-digest?domain=" + anvil,
			wantStatus: http.StatusOK,
			wantDigest: accesstypes.DigestConditional,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			status, body := doRequestAs(t, h, tt.user, method, tt.target, tt.body)
			assertStatus(t, status, tt.wantStatus, body)

			if tt.wantRows {
				if rows := decodeRows(t, body); len(rows) == 0 {
					t.Error("list returned no refits; Anvil has four")
				}
			}
			if tt.wantDigest != "" {
				var digest accesstypes.PermissionDigest
				if err := json.Unmarshal(body, &digest); err != nil {
					t.Fatalf("decoding digest: %v", err)
				}
				if got := digest["Refits"]["List"]; got != tt.wantDigest {
					t.Errorf("digest[Refits][List] = %q, want %q", got, tt.wantDigest)
				}
			}
		})
	}
}

// settledAttempts is how many times settled asks before it fails the test. A real clock
// needs two at most: the boundaries these tests cross are hours apart, and a request takes
// a moment.
const settledAttempts = 3

// settled makes the requests in ask and returns the side of a clock boundary they were
// served on: the day shift or the night, a weekday or the weekend. The server reads the
// clock for itself while it serves a request, so a test that reads the clock once and asks
// afterwards expects the wrong answer whenever the boundary falls between the two. settled
// reads the side before and after the requests: readings that agree are the side every
// request between them was served on; readings that differ mean the requests straddled the
// boundary, and it asks again. ask is given the number of the attempt, starting at one,
// for a request that must differ from the one before it.
func settled[Side comparable](t *testing.T, now func() time.Time, side func(time.Time) Side, ask func(attempt int)) Side {
	t.Helper()

	for attempt := 1; ; attempt++ {
		before := side(now())
		ask(attempt)
		if after := side(now()); after == before {
			return after
		}
		if attempt == settledAttempts {
			t.Fatalf("the clock crossed a boundary during each of %d attempts; no request was served on one side of it", settledAttempts)
		}
	}
}

// onWeekend returns the side of the week an instant falls on in the zone: true on a
// Saturday or a Sunday.
func onWeekend(zone *time.Location) func(time.Time) bool {
	return func(at time.Time) bool {
		day := at.In(zone).Weekday()

		return day == time.Saturday || day == time.Sunday
	}
}

// TestSettledAsksAgainAcrossABoundary pins the helper the clock's tests stand on, with a
// scripted clock: a request that begins on Friday and ends on Saturday is asked again, and
// the side returned is the one the second request was served on.
func TestSettledAsksAgainAcrossABoundary(t *testing.T) {
	t.Parallel()

	opsClock, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	beforeMidnight := time.Date(2026, time.November, 20, 23, 59, 59, 0, opsClock)
	afterMidnight := beforeMidnight.Add(2 * time.Second)

	tests := []struct {
		name string
		// crossesDuring is the attempt during which the clock passes midnight; zero is never.
		crossesDuring int
		wantWeekend   bool
		wantAsks      int
	}{
		{name: "a request served within one day is asked once", wantWeekend: false, wantAsks: 1},
		{name: "a request that straddles midnight is asked again", crossesDuring: 1, wantWeekend: true, wantAsks: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clock := beforeMidnight
			now := func() time.Time {
				return clock
			}
			asks := 0
			weekend := settled(t, now, onWeekend(opsClock), func(attempt int) {
				asks++
				if attempt == tt.crossesDuring {
					clock = afterMidnight
				}
			})
			if weekend != tt.wantWeekend {
				t.Errorf("settled() weekend = %t, want %t: the side must be the one the last request was served on", weekend, tt.wantWeekend)
			}
			if asks != tt.wantAsks {
				t.Errorf("settled() asked %d times, want %d", asks, tt.wantAsks)
			}
		})
	}
}

// TestShiftPairIsComplementary pins the shipped personas: at any hour exactly one of
// Dockmaster Dara (06:00–18:00 local) and Night Watch Nadia (18:00–06:00 Denver) sees
// the hangar deck.
func TestShiftPairIsComplementary(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	// The shipped shift grants this suite proves, each pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "Dockmaster", "List", "Refits", "timeOfDay(now, local) >= '06:00' AND timeOfDay(now, local) < '18:00'")
	provesGrant(t, crew.Roles(), "Dockmaster", "Read", "Refits", "timeOfDay(now, local) >= '06:00' AND timeOfDay(now, local) < '18:00'")
	provesGrant(t, crew.Roles(), "NightWatch", "List", "Refits", "timeOfDay(now, 'America/Denver') >= '18:00' OR timeOfDay(now, 'America/Denver') < '06:00'")
	provesGrant(t, crew.Roles(), "NightWatch", "Read", "Refits", "timeOfDay(now, 'America/Denver') >= '18:00' OR timeOfDay(now, 'America/Denver') < '06:00'")

	opsClock, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	// Both grants turn at 06:00 and 18:00 of the operations clock. A pair of requests with
	// one of those between them would find both shifts on the deck, or neither, so each
	// pair is asked through settled.
	onDayShift := func(at time.Time) bool {
		hour := at.In(opsClock).Hour()

		return hour >= 6 && hour < 18
	}

	var dara, nadia int
	var daraBody, nadiaBody []byte
	settled(t, time.Now, onDayShift, func(int) {
		dara, daraBody = doRequestAs(t, h, "dock", http.MethodGet, sectorPath(anvil, "refits"), "")
		nadia, nadiaBody = doRequestAs(t, h, "watch", http.MethodGet, sectorPath(anvil, "refits"), "")
	})
	if (dara == http.StatusOK) == (nadia == http.StatusOK) {
		t.Fatalf("dara = %d (%s), nadia = %d (%s): exactly one shift must see the deck", dara, daraBody, nadia, nadiaBody)
	}

	// The same pairing on a single refit: the row condition is row-free, so the read is
	// admitted or not found by the clock alone.
	var daraRow, nadiaRow int
	var daraRowBody, nadiaRowBody []byte
	settled(t, time.Now, onDayShift, func(int) {
		daraRow, daraRowBody = doRequestAs(t, h, "dock", http.MethodGet, sectorPath(anvil, "refits/"+refitLanternID), "")
		nadiaRow, nadiaRowBody = doRequestAs(t, h, "watch", http.MethodGet, sectorPath(anvil, "refits/"+refitLanternID), "")
	})
	if (daraRow == http.StatusOK) == (nadiaRow == http.StatusOK) {
		t.Fatalf("dara = %d (%s), nadia = %d (%s): exactly one shift must read the Lantern's refit", daraRow, daraRowBody, nadiaRow, nadiaRowBody)
	}
}

// TestNightWatchWeekdayTasks pins the night watch's shipped write grant: notes on a task
// whose refit is in the bay are admitted on a weekday of the operations clock and refused
// on a weekend, and a task of a refit outside the bay is refused on any day. The expected
// status is computed from the grant and the clock the request was served under, never from
// an observed answer.
func TestNightWatchWeekdayTasks(t *testing.T) {
	t.Parallel()

	_, _, h := demoWorld(t)

	// The grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, crew.Roles(), "NightWatch", "Update", "RefitTasks", "dayOfWeek(now, local) NOT IN ('sat', 'sun') AND state = 'in_refit'")

	opsClock, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		target string
		// wantStatus is the status the grant gives a request served on that side of the week.
		wantStatus func(weekend bool) int
	}{
		{
			name:   "a task of a ship in the refit bay follows the day of the week",
			target: "refit-tasks/" + refitSamaritanID + "/2",
			wantStatus: func(weekend bool) int {
				if weekend {
					return http.StatusForbidden
				}

				return http.StatusOK
			},
		},
		{
			name:   "a task of an inspected ship not yet in refit is refused on any day",
			target: "refit-tasks/" + refitMuleID + "/1",
			wantStatus: func(bool) int {
				return http.StatusForbidden
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The day is read around the request, never once before it: a request made
			// just before midnight may be served just after. Each attempt writes a note of
			// its own, so a second one is a change to make and not the first one repeated.
			var status int
			var body []byte
			weekend := settled(t, time.Now, onWeekend(opsClock), func(attempt int) {
				status, body = doRequestAs(t, h, "watch", http.MethodPatch, "/console/api/resources",
					fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"notes":"checked on watch, attempt %d"}}]`, opPath(anvil, tt.target), attempt))
			})
			assertStatus(t, status, tt.wantStatus(weekend), body)
		})
	}
}

// TestLocalZoneMustBeWired pins the fail-loud posture: a `local` condition checked
// through an Environment that carries no zone is an error, never a silent decision.
func TestLocalZoneMustBeWired(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	_, _, client := sharedWorld(t)
	checker := client.ForUser(account(t, "dock"))

	if _, err := checker.Check(ctx, accesstypes.EnvironmentAt(time.Now()), accesstypes.DomainScope(anvil), accesstypes.List, "Refits"); err == nil {
		t.Fatal("Check() with no zone succeeded; want a loud error for an unresolvable local")
	}
}
