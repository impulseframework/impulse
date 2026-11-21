// Demonstrates: auth.reasons, auth.generated-identity.
package integration

// generated_identity_reasons_test: the reason a role is held for and the taking of a role
// for a set time, as the generated management of the crew auth serves them for the Break
// glass page.
//
// A role of a person's own carries the reason it was given for, and a person takes a role
// themselves for a set time with a reason: the role is theirs from then until the length
// is over, listed with who, where, why and until when, read by the sector's Marshal
// beside the role, and ended early by taking it away.
//
// The suites drive the lists and the method as served, against the real engine over the
// shipped role file, on the Spanner emulator: the lists are queries the generator writes
// in Spanner's SQL, and the generated management has no other database. The people are
// the cast's: the governor, who runs headquarters and is the fleet's super administrator,
// the Marshal of Anvil, and the relief officer, whose tour at Anvil has an end and a
// reason.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// The lists of the Break glass page, as the generated routes name them: the roles held
// for a set time, and the two lists the form of a taking picks from.
const (
	takenRolesPath    = "crew-taken-roles"
	takeableRolesPath = "crew-takeable-roles"
	takeLengthsPath   = "crew-take-lengths"
)

// text reads a cell that holds text or nothing.
func text(row map[string]any, key string) string {
	value, _ := row[key].(string)

	return value
}

// endNow writes an end that is over already on a row of one of the permission store's
// tables, straight into the database: the methods refuse a period that is over, and the
// lists read the tables, so this is how a test has a row that has ended and still stands.
func endNow(t *testing.T, db *initiator.SpannerDB, table string, key map[string]any) {
	t.Helper()

	row := map[string]any{"StartsAt": time.Now().UTC().Add(-2 * time.Hour), "EndsAt": time.Now().UTC().Add(-time.Hour)}
	for column, value := range key {
		row[column] = value
	}
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{spanner.UpdateMap(table, row)}); err != nil {
		t.Fatalf("ending the row %v of %s: %v", key, table, err)
	}
}

// engineerAtAnvil reads the period and the reason of the Engineer's role a person holds
// at Anvil, the one role the cases of this suite take, straight from the database; found
// is false where there is no such row.
func engineerAtAnvil(t *testing.T, db *initiator.SpannerDB, userID string) (period accesstypes.Period, reason string, found bool) {
	t.Helper()

	iter := db.Client.Single().Query(t.Context(), spanner.Statement{
		SQL:    "SELECT StartsAt, EndsAt, Reason FROM CrewUserRoles WHERE Kind = 'domain' AND Axis = '' AND DomainId = @sector AND Role = 'Engineer' AND UserId = @user",
		Params: map[string]any{"sector": anvil, "user": userID},
	})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		return accesstypes.Period{}, "", false
	}
	var starts, ends spanner.NullTime
	var why spanner.NullString
	if err := row.Columns(&starts, &ends, &why); err != nil {
		t.Fatalf("reading the Engineer's role of %s at Anvil: %v", userID, err)
	}

	return accesstypes.Period{Start: starts.Time, End: ends.Time}, why.StringVal, true
}

// withoutGrant is the role file with the grant the role holds on the resource taken out,
// for a suite that proves what the grant decides. It fails where the role holds no such
// grant, so it never hands back the file unchanged.
func withoutGrant(t *testing.T, roles access.RoleFile, role, permission, res string) access.RoleFile {
	t.Helper()

	var file map[string]map[string][]map[string]any
	if err := json.Unmarshal(roles, &file); err != nil {
		t.Fatalf("reading the role file: %v", err)
	}
	taken := false
	for _, set := range file["roles"] {
		for _, entry := range set {
			if entry["name"] != role {
				continue
			}
			permissions, _ := entry["permissions"].(map[string]any)
			grants, _ := permissions[permission].([]any)
			kept := make([]any, 0, len(grants))
			for _, g := range grants {
				if grant, ok := g.(map[string]any); ok && grant["resource"] == res {
					taken = true

					continue
				}
				kept = append(kept, g)
			}
			permissions[permission] = kept
		}
	}
	if !taken {
		t.Fatalf("the %s role holds no %s grant on %s to take out", role, permission, res)
	}
	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("writing the role file: %v", err)
	}

	return raw
}

// TestGeneratedIdentityTakeRole holds the taking of a role for a set time, the act of
// the Break glass page: the governor takes a role in a sector with a reason, and holds
// it from then until the length is over; the reason is required and the length is one
// of the ones offered; the list of the roles held for a set time says who, where, why
// and until when; the sector's Marshal reads why beside the role; taking again writes the
// new time and the new reason over the old ones, where giving himself a longer time the
// ordinary way is refused; the Marshal ends it early by taking the role away; and a role
// whose time is over is listed as ended while its row stands.
func TestGeneratedIdentityTakeRole(t *testing.T) {
	t.Parallel()
	w := newIdentityWorld(t)

	governor, found := accountID("governor")
	if !found {
		t.Fatal("the cast has no governor")
	}
	take := func(user accesstypes.User, body string) (int, []byte) {
		return doRequestAs(t, w.h, user, http.MethodPost, consoleAPI+"/take-crew-role", body)
	}
	taking := func(role, length, reason string) string {
		return fmt.Sprintf(`{"domain":%q,"role":%q,"length":%q,"reason":%q}`, anvil, role, length, reason)
	}
	list := func(user accesstypes.User, path string) []map[string]any {
		t.Helper()
		status, body := doRequestAs(t, w.h, user, http.MethodGet, path, "")
		assertStatus(t, status, http.StatusOK, body)

		return decodeRows(t, body)
	}
	everyTaken := func(ended bool) []map[string]any {
		t.Helper()

		return list("governor", fmt.Sprintf("%s/%s?limit=200&filter=ended:eq:%v", consoleAPI, takenRolesPath, ended))
	}
	// The governor's own rows of that list: the cast holds other roles for a set time.
	taken := func(ended bool) []map[string]any {
		t.Helper()

		var own []map[string]any
		for _, row := range everyTaken(ended) {
			if text(row, "userId") == governor.String() {
				own = append(own, row)
			}
		}

		return own
	}

	// The list holds every role held for a set time with a reason, however it was given:
	// the relief officer's tour at Anvil, given the ordinary way with an end and a
	// reason, is on it before anyone takes a role.
	var tour []string
	for _, row := range everyTaken(false) {
		if text(row, "username") == "relief" && text(row, "reason") != "" && text(row, "domainId") == anvil {
			tour = append(tour, text(row, "role"))
		}
	}
	slices.Sort(tour)
	if want := []string{"Cadet", "SectorCrew"}; !slices.Equal(tour, want) {
		t.Errorf("the relief officer's roles held for a set time at Anvil = %v, want %v", tour, want)
	}
	if rows := taken(false); len(rows) != 0 {
		t.Fatalf("the governor's roles held for a set time before he takes one = %v, want none", rows)
	}

	// What the form picks from: the lengths, shortest first, and the roles the governor
	// may take, the ones that exist in every sector and never the Super Administrator's.
	if got, want := namesInOrder(t, list("governor", consoleAPI+"/"+takeLengthsPath)), []string{"1 hour", "4 hours", "8 hours", "1 day"}; !slices.Equal(got, want) {
		t.Errorf("the lengths a role is taken for = %v, want %v", got, want)
	}
	roles := namesOf(t, list("governor", consoleAPI+"/"+takeableRolesPath+"?limit=200"), "name")
	for _, name := range []string{"Engineer", "SectorMarshal"} {
		if !slices.Contains(roles, name) {
			t.Errorf("the roles the governor may take leave out %s: %v", name, roles)
		}
	}
	for _, name := range []string{string(accesstypes.SuperAdministrator), "Headquarters"} {
		if slices.Contains(roles, name) {
			t.Errorf("the roles the governor may take hold %s, which is no role of a sector to take: %v", name, roles)
		}
	}

	// The Marshal holds no grant on the method, and takes nothing.
	const why = "Inspecting Anvil's hangar after the collision"
	status, body := take("marshal", taking("Engineer", "1 hour", why))
	assertStatus(t, status, http.StatusForbidden, body)
	// The reason is required, and the length is one of the ones offered.
	for _, tt := range []struct {
		name, body, says string
	}{
		{name: "no reason", body: fmt.Sprintf(`{"domain":%q,"role":"Engineer","length":"1 hour"}`, anvil)},
		{name: "a reason of spaces alone", body: taking("Engineer", "1 hour", "   "), says: "say why you need the role"},
		{name: "a reason of two lines", body: taking("Engineer", "1 hour", "Inspecting\nthe hangar"), says: "one line of plain text"},
		{name: "a length that is not offered", body: taking("Engineer", "2 days", why), says: "a longer need is an ordinary role with an end"},
		{name: "the Super Administrator role", body: fmt.Sprintf(`{"domain":%q,"role":%q,"length":"1 hour","reason":%q}`, anvil, accesstypes.SuperAdministrator, why)},
	} {
		status, body = take("governor", tt.body)
		assertStatus(t, status, http.StatusBadRequest, body)
		if !strings.Contains(string(body), tt.says) {
			t.Errorf("%s: the refusal = %s, want it to say %q", tt.name, body, tt.says)
		}
	}
	if _, _, held := engineerAtAnvil(t, w.db, governor.String()); held {
		t.Fatal("the governor holds the Engineer's role at Anvil after the refused takings")
	}

	// He takes the Engineer's role at Anvil for an hour.
	before := time.Now().UTC()
	status, body = take("governor", taking("Engineer", "1 hour", why))
	assertStatus(t, status, http.StatusNoContent, body)
	period, reason, held := engineerAtAnvil(t, w.db, governor.String())
	if !held || reason != why {
		t.Fatalf("the membership the taking wrote: held = %v, reason = %q, want it held for %q", held, reason, why)
	}
	if period.Start.Before(before.Add(-time.Second)) || period.Start.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("the role taken starts at %s, want now", period.Start)
	}
	if got := period.End.Sub(period.Start); got != time.Hour {
		t.Errorf("the role taken lasts %s, want 1h0m0s", got)
	}
	rows := taken(false)
	if len(rows) != 1 || text(rows[0], "username") != "governor" || text(rows[0], "role") != "Engineer" || text(rows[0], "reason") != why ||
		text(rows[0], "kind") != "domain" || text(rows[0], "domainId") != anvil {
		t.Fatalf("the roles held right now = %v, want the governor's Engineer at Anvil with its reason", rows)
	}
	if got := periodOf(t, rows[0]); !got.Equal(period) {
		t.Errorf("the period the list says = %+v, want the membership's %+v", got, period)
	}
	if got := taken(true); len(got) != 0 {
		t.Errorf("the roles whose time is over = %v, want none", got)
	}
	// The sector's Marshal reads why he holds a role with her, beside when it ends.
	var seen bool
	for _, row := range list("marshal", sectorPath(anvil, sectorRolesPath+"?limit=200&filter=userId:eq:"+governor.String())) {
		if text(row, "role") != "Engineer" {
			continue
		}
		seen = true
		if text(row, "reason") != why || !periodOf(t, row).Equal(period) {
			t.Errorf("the Marshal's row of the governor's Engineer = %v, want the reason %q and the period %+v", row, why, period)
		}
	}
	if !seen {
		t.Error("Anvil's list of roles leaves out the role the governor took there")
	}

	// Taking it again writes the new time and the new reason over the old ones: one
	// row, and nothing refused, though it lasts longer than before.
	const again = "The inspection found more to look at"
	status, body = take("governor", taking("Engineer", "4 hours", again))
	assertStatus(t, status, http.StatusNoContent, body)
	longer, reason, _ := engineerAtAnvil(t, w.db, governor.String())
	if reason != again || longer.End.Sub(longer.Start) != 4*time.Hour || !longer.End.After(period.End) {
		t.Errorf("the membership after the second taking: %+v for %q, want four hours from now for %q", longer, reason, again)
	}
	if rows := taken(false); len(rows) != 1 || text(rows[0], "reason") != again {
		t.Errorf("the roles held right now after the second taking = %v, want the one row with the new reason", rows)
	}
	// Giving himself a longer time the ordinary way is refused as it is for anyone.
	status, body = doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/assign-crew-role",
		periodBody(t, map[string]any{"userId": governor.String(), "role": "Engineer", "domain": anvil, "reason": again}, time.Time{}, longer.End.Add(time.Hour)))
	assertStatus(t, status, http.StatusForbidden, body)
	if !strings.Contains(string(body), "You cannot make your own role last longer than it was given for.") {
		t.Errorf("the refusal to lengthen his own role the ordinary way = %s", body)
	}

	// Ending it early is taking the role away, by whoever may: the sector's Marshal.
	status, body = doRequestAs(t, w.h, "marshal", http.MethodPost, sectorPath(anvil, "remove-crew-sector-role"), fmt.Sprintf(`{"userId":%q,"role":"Engineer"}`, governor))
	assertStatus(t, status, http.StatusNoContent, body)
	if rows := taken(false); len(rows) != 0 {
		t.Errorf("the roles held right now after the Marshal ended it = %v, want none", rows)
	}

	// A role whose time is over is listed as ended while its row stands, with from when
	// to when and why, and the lists of what people hold leave it out.
	status, body = take("governor", taking("Engineer", "1 hour", why))
	assertStatus(t, status, http.StatusNoContent, body)
	endNow(t, w.db, "CrewUserRoles", map[string]any{"Kind": "domain", "Axis": "", "DomainId": anvil, "Role": "Engineer", "UserId": governor.String()})
	if rows := taken(false); len(rows) != 0 {
		t.Errorf("the roles held right now once the time is over = %v, want none", rows)
	}
	rows = taken(true)
	if len(rows) != 1 || text(rows[0], "reason") != why || text(rows[0], "role") != "Engineer" {
		t.Fatalf("the roles whose time is over = %v, want the governor's Engineer with its reason", rows)
	}
	if got := periodOf(t, rows[0]); got.Start.IsZero() || !got.End.Before(time.Now().UTC()) {
		t.Errorf("the ended row says %+v, want from when to when, the end past", got)
	}
	for _, row := range list("marshal", sectorPath(anvil, sectorRolesPath+"?limit=200&filter=userId:eq:"+governor.String())) {
		if text(row, "role") == "Engineer" {
			t.Errorf("Anvil's list of roles holds the governor's Engineer after its time was over: %v", row)
		}
	}
}

// namesInOrder reads the name of every row, in the order served.
func namesInOrder(t *testing.T, rows []map[string]any) []string {
	t.Helper()

	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, cell[string](t, row, "name"))
	}

	return names
}

// TestGeneratedIdentityTakeRoleNeedsTheGiving holds who takes a role: beside the grant of
// the method, a person needs what giving themselves the role needs. Under the shipped
// role file a person of headquarters, who is no super administrator, takes a role; with
// the grant on the method that gives a role taken out of her role she holds the method
// and takes nothing, in words that say why.
func TestGeneratedIdentityTakeRoleNeedsTheGiving(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		roles access.RoleFile
		want  int
	}{
		{name: "the shipped role file: headquarters gives roles, so its people take them", roles: crew.Roles(), want: http.StatusNoContent},
		{
			name:  "headquarters without the grant that gives a role: the method alone takes nothing",
			roles: withoutGrant(t, crew.Roles(), "Headquarters", "Execute", "AssignCrewRole"), want: http.StatusForbidden,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := buildIdentityWorldOver(t, false, tt.roles)
			ctx := t.Context()

			// The request is made as the account's id: both cases run at once under one
			// test, and each has a hana of its own.
			hana := w.seedCrewUser(ctx, t, "hana", "Headquarters").String()

			status, body := doRequestAs(t, w.h, accesstypes.User(hana), http.MethodPost, consoleAPI+"/take-crew-role",
				fmt.Sprintf(`{"domain":%q,"role":"Engineer","length":"1 hour","reason":"Covering the hangar desk"}`, anvil))
			assertStatus(t, status, tt.want, body)
			_, _, held := engineerAtAnvil(t, w.db, hana)
			if held != (tt.want == http.StatusNoContent) {
				t.Errorf("hana holds the Engineer's role at Anvil: %v, and the taking was answered %d", held, status)
			}
			if tt.want == http.StatusForbidden && !strings.Contains(string(body), "you may not give yourself the role Engineer there") {
				t.Errorf("the refusal = %s, want it to say she may not give herself the role", body)
			}
		})
	}
}
