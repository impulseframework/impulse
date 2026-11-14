package integration

// A grant on a method may test the method's own request fields, as a grant on a create
// tests the row being written.
//
// The method declares the fields a condition may read with a bare @attribute(name) on
// the field (the crew's AssignCrewRole.Role, CreateCrewUser.Roles, InviteCrewUser.Roles,
// each as `role`; SetSectorPilotClearance.Clearance as `clearance`). A grant's condition
// reads one as new.<name>.
//
// The roles below exist only in the copy of the crew's role file this test opens its
// engine with; nothing here touches pkg/auth/crew/roles.json.
//
// Demonstrates: execute-condition.request-field.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
)

// requestFieldRole is one role the test adds to the crew's role file, in a copy the test holds.
type requestFieldRole struct {
	domain bool
	name   string
	// permissions is the role's permissions block as the role file writes it.
	permissions map[string][]map[string]any
}

// execute is an Execute grant on a method under a condition ("" for none).
func execute(method, cond string) map[string]any {
	grant := map[string]any{"resource": method}
	if cond != "" {
		grant["condition"] = cond
	}

	return grant
}

// sectorPilotsRead lets a role of the test list and read the sector's pilots, so its rows
// answer what the role may run against them.
func sectorPilotsRead() map[string]any {
	return map[string]any{"resource": "SectorPilots", "fields": []string{"clearance", "displayName", "fliesElsewhere", "sectorId", "squadrons"}}
}

const (
	// The conditions the test's grants carry.
	condNotHeadquarters = "new.role != 'Headquarters'"
	condPlainRoles      = "new.role IN ('CrewCommon', 'ClientBrowser')"
	condRowAndRequest   = "fliesElsewhere = false AND new.clearance <= 3"
	condRequestOnly     = "new.clearance <= 3"
)

// requestFieldRoles are the roles the test adds.
func requestFieldRoles() []requestFieldRole {
	return []requestFieldRole{
		// A delegate at headquarters: gives any role but the headquarters' own, and
		// creates or invites people into the two plain roles alone.
		{name: "FieldDelegate", permissions: map[string][]map[string]any{"Execute": {
			execute("AssignCrewRole", condNotHeadquarters),
			execute("CreateCrewUser", condPlainRoles),
			execute("InviteCrewUser", condPlainRoles),
		}}},
		// Rates a sector's own pilots, up to clearance 3: a fact of the row and a
		// request field in one condition.
		{domain: true, name: "FieldRater", permissions: map[string][]map[string]any{
			"List": {sectorPilotsRead()}, "Read": {sectorPilotsRead()},
			"Execute": {execute(setSectorPilotClearance, condRowAndRequest)},
		}},
		// Rates any pilot of the sector, up to clearance 3: a request field alone on a
		// method with a target.
		{domain: true, name: "FieldCapper", permissions: map[string][]map[string]any{
			"List": {sectorPilotsRead()}, "Read": {sectorPilotsRead()},
			"Execute": {execute(setSectorPilotClearance, condRequestOnly)},
		}},
	}
}

// requestFieldRoleFile is the crew's role file with the given roles added.
func requestFieldRoleFile(t *testing.T, roles ...requestFieldRole) access.RoleFile {
	t.Helper()

	var file map[string]map[string][]map[string]any
	if err := json.Unmarshal(crew.Roles(), &file); err != nil {
		t.Fatalf("the crew's role file does not parse: %v", err)
	}
	for _, role := range roles {
		scope := "global"
		if role.domain {
			scope = "domain"
		}
		file["roles"][scope] = append(file["roles"][scope], map[string]any{
			"name":        role.name,
			"description": "A role of this test.",
			"permissions": role.permissions,
		})
	}
	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshaling the test's role file: %v", err)
	}

	return raw
}

// TestMethodRequestFieldsValidation records what the role-file validation says of an
// Execute grant whose condition reads a request field.
func TestMethodRequestFieldsValidation(t *testing.T) {
	t.Parallel()

	one := func(domain bool, method, cond string) requestFieldRole {
		return requestFieldRole{domain: domain, name: "FieldOne", permissions: map[string][]map[string]any{"Execute": {execute(method, cond)}}}
	}
	cases := []struct {
		name string
		role requestFieldRole
		// wantErr is a part of the refusal; "" means the file is accepted.
		wantErr string
	}{
		{name: "no target, a request field alone", role: one(false, "AssignCrewRole", condNotHeadquarters)},
		{name: "no target, a list field", role: one(false, "InviteCrewUser", condPlainRoles)},
		{name: "a target, a fact of the row and a request field", role: one(true, setSectorPilotClearance, condRowAndRequest)},
		{name: "a target, a request field alone", role: one(true, setSectorPilotClearance, condRequestOnly)},
		{name: "a field the method does not declare", role: one(false, "AssignCrewRole", "new.username != 'governor'"), wantErr: `condition reads new.username, which AssignCrewRole does not declare as a request field; declare @attribute(username) on the method's field`},
		{name: "a field of another method", role: one(false, "AssignCrewRole", "new.clearance <= 3"), wantErr: `condition reads new.clearance, which AssignCrewRole does not declare as a request field`},
		{name: "a literal of another type", role: one(true, setSectorPilotClearance, "new.clearance = 'high'"), wantErr: `clearance is a number attribute and cannot compare against the string "high"`},
		{name: "no target, a row attribute beside the request field", role: one(false, "AssignCrewRole", "new.role != 'Headquarters' AND disabled = false"), wantErr: `execute permissions on a method without a @target row check at decode time`},
		{name: "a request field against the caller's roles", role: one(false, "AssignCrewRole", "new.role IN subject.ownRoles"), wantErr: `a request field (new.) compares against literals`},
		{name: "a method with no declared field keeps today's refusal", role: one(false, "DisableCrewUser", "new.userId != 'x'"), wantErr: `condition reads new.userId, which DisableCrewUser does not declare as a request field`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config, err := requestFieldRoleFile(t, tt.role).Parse()
			if err != nil {
				t.Fatalf("RoleFile.Parse() error = %v", err)
			}
			_, err = access.ValidateRoles(router.Collection(), config)
			t.Logf("access.ValidateRoles() error = %v", err)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("the file is refused, want accepted")
			case tt.wantErr != "" && err == nil:
				t.Errorf("the file is accepted, want a refusal with %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("the refusal lacks %q", tt.wantErr)
			}
		})
	}
}

// Who the tests call as.
const (
	fieldDelegate = accesstypes.User("field-delegate") // FieldDelegate at headquarters
	fieldBoth     = accesstypes.User("field-both")     // FieldDelegate and Headquarters
	fieldRater    = accesstypes.User("field-rater")    // FieldRater at Anvil
	fieldCapper   = accesstypes.User("field-capper")   // FieldCapper at Anvil
	fieldTwo      = accesstypes.User("field-two")      // FieldRater and WingCommander at Anvil
	fieldMarshal  = accesstypes.User("field-marshal")  // FieldRater and SectorMarshal at Anvil
)

// newRequestFieldWorld is newIdentityWorld with the crew's engine opened over the test's role
// file, and the test's people seated.
func newRequestFieldWorld(t *testing.T) *identityWorld {
	t.Helper()
	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	crewEngine, err := openEngine(db, crew.TablePrefix, requestFieldRoleFile(t, requestFieldRoles()...))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = crewEngine.Close() })
	membersEngine, err := openEngine(db, members.TablePrefix, members.Roles())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = membersEngine.Close() })
	if err := provisionDemoAccess(ctx, crewEngine, membersEngine); err != nil {
		t.Fatal(err)
	}

	users := crewEngine.UserManager()
	seats := []struct {
		user  accesstypes.User
		scope accesstypes.PolicyScope
		roles []accesstypes.Role
	}{
		{fieldDelegate, accesstypes.GlobalPolicyScope(), []accesstypes.Role{"FieldDelegate"}},
		{fieldBoth, accesstypes.GlobalPolicyScope(), []accesstypes.Role{"FieldDelegate", "Headquarters"}},
		{fieldRater, accesstypes.DomainPolicyScope(anvil), []accesstypes.Role{"FieldRater"}},
		{fieldCapper, accesstypes.DomainPolicyScope(anvil), []accesstypes.Role{"FieldCapper"}},
		{fieldTwo, accesstypes.DomainPolicyScope(anvil), []accesstypes.Role{"FieldRater", "WingCommander"}},
		{fieldMarshal, accesstypes.DomainPolicyScope(anvil), []accesstypes.Role{"FieldRater", "SectorMarshal"}},
	}
	for _, seat := range seats {
		if err := users.AddUserRoles(ctx, seat.scope, seat.user, seat.roles...); err != nil {
			t.Fatalf("seating %s: %v", seat.user, err)
		}
	}
	if err := waitForDecision(ctx, crewEngine, fieldMarshal, anvil, accesstypes.List, "SectorPilots"); err != nil {
		t.Fatalf("the test's seats never took effect: %v", err)
	}

	a := app.New(&testConfigurer{db: db, access: crewEngine, membersAccess: membersEngine})

	return &identityWorld{db: db, app: a, h: withHandWrittenRoutes(a), engine: crewEngine}
}

// crewRolesOf reads the global roles the crew's memberships hold for a username.
func crewRolesOf(t *testing.T, w *identityWorld, username string) []string {
	t.Helper()

	var roles []string
	iter := w.db.Single().Query(t.Context(), spanner.Statement{
		SQL:    "SELECT Role FROM CrewUserRoles WHERE User = @user AND Kind = 'global' ORDER BY Role",
		Params: map[string]any{"user": username},
	})
	defer iter.Stop()
	if err := iter.Do(func(row *spanner.Row) error {
		var role string
		if err := row.Column(0, &role); err != nil {
			return err
		}
		roles = append(roles, role)

		return nil
	}); err != nil {
		t.Fatalf("reading %s's roles: %v", username, err)
	}

	return roles
}

// TestMethodRequestFieldsNoTarget holds a method with no target row whose grant reads a
// request field: allowed where the condition holds, refused in the words a caller with
// no grant hears where it does not, before the body runs.
func TestMethodRequestFieldsNoTarget(t *testing.T) {
	t.Parallel()
	w := newRequestFieldWorld(t)

	const refusal = "does not have Execute on [AssignCrewRole]"
	fires := []struct {
		name       string
		user       accesstypes.User
		role       string
		wantStatus int
		wantBody   string
		// wantRoles are the veteran's global roles after the call.
		wantRoles []string
	}{
		{name: "the delegate gives a role the condition admits", user: fieldDelegate, role: "BulletinOfficer", wantStatus: http.StatusNoContent, wantRoles: []string{"BulletinOfficer", "ClientBrowser", "CrewCommon"}},
		{name: "the role the condition names is refused, in the words of no grant", user: fieldDelegate, role: "Headquarters", wantStatus: http.StatusForbidden, wantBody: refusal, wantRoles: []string{"BulletinOfficer", "ClientBrowser", "CrewCommon"}},
		{name: "a caller with no grant hears the same words", user: "cadet", role: "BulletinOfficer", wantStatus: http.StatusForbidden, wantBody: refusal, wantRoles: []string{"BulletinOfficer", "ClientBrowser", "CrewCommon"}},
		{name: "a role that does not exist passes the grant and is the body's to refuse", user: fieldDelegate, role: "NoSuchRole", wantStatus: http.StatusNotFound, wantBody: "NoSuchRole", wantRoles: []string{"BulletinOfficer", "ClientBrowser", "CrewCommon"}},
		{name: "two grants, one with no condition: the role the condition names is given", user: fieldBoth, role: "Headquarters", wantStatus: http.StatusNoContent, wantRoles: []string{"BulletinOfficer", "ClientBrowser", "CrewCommon", "Headquarters"}},
	}
	for _, tt := range fires {
		t.Run(tt.name, func(t *testing.T) {
			status, body := doRequestAs(t, w.h, tt.user, http.MethodPost, consoleAPI+"/assign-crew-role", fmt.Sprintf(`{"username":"veteran","role":%q}`, tt.role))
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantBody != "" && !strings.Contains(string(body), tt.wantBody) {
				t.Errorf("the answer %s lacks %q", body, tt.wantBody)
			}
			if got := crewRolesOf(t, w, "veteran"); !slices.Equal(got, tt.wantRoles) {
				t.Errorf("the veteran's global roles = %v, want %v", got, tt.wantRoles)
			}
		})
	}
}

// TestMethodRequestFieldsList holds a list field: the condition is judged once per
// element, and one element that fails refuses the call.
func TestMethodRequestFieldsList(t *testing.T) {
	t.Parallel()
	w := newRequestFieldWorld(t)

	suffix := identitySuffix()
	fires := []struct {
		name       string
		method     string
		body       string
		wantStatus int
		wantBody   string
	}{
		{name: "create: every role is one the condition admits", method: "create-crew-user", body: `{"username":"ada-` + suffix + `","roles":["CrewCommon","ClientBrowser"]}`, wantStatus: http.StatusOK},
		{name: "create: one role of two fails", method: "create-crew-user", body: `{"username":"bea-` + suffix + `","roles":["CrewCommon","Headquarters"]}`, wantStatus: http.StatusForbidden, wantBody: "does not have Execute on [CreateCrewUser]"},
		{name: "create: the one role fails", method: "create-crew-user", body: `{"username":"cy-` + suffix + `","roles":["Headquarters"]}`, wantStatus: http.StatusForbidden, wantBody: "does not have Execute on [CreateCrewUser]"},
		{name: "create: no role at all is no value, which never permits", method: "create-crew-user", body: `{"username":"dee-` + suffix + `","roles":[]}`, wantStatus: http.StatusForbidden, wantBody: "does not have Execute on [CreateCrewUser]"},
		{name: "create: the field left out is no value too", method: "create-crew-user", body: `{"username":"eli-` + suffix + `"}`, wantStatus: http.StatusForbidden, wantBody: "does not have Execute on [CreateCrewUser]"},
		{name: "invite: one role of two fails, before any invitation is written", method: "invite-crew-user", body: `{"email":"fay-` + suffix + `@example.com","roles":["ClientBrowser","Headquarters"]}`, wantStatus: http.StatusForbidden, wantBody: "does not have Execute on [InviteCrewUser]"},
	}
	for _, tt := range fires {
		t.Run(tt.name, func(t *testing.T) {
			status, body := doRequestAs(t, w.h, fieldDelegate, http.MethodPost, consoleAPI+"/"+tt.method, tt.body)
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantBody != "" && !strings.Contains(string(body), tt.wantBody) {
				t.Errorf("the answer %s lacks %q", body, tt.wantBody)
			}
		})
	}

	if got, want := crewRolesOf(t, w, "ada-"+suffix), []string{"ClientBrowser", "CrewCommon"}; !slices.Equal(got, want) {
		t.Errorf("the created person's roles = %v, want %v", got, want)
	}
	for _, refused := range []string{"bea-", "cy-", "dee-", "eli-"} {
		if got := crewRolesOf(t, w, refused+suffix); len(got) != 0 {
			t.Errorf("%s was refused and holds roles %v", refused+suffix, got)
		}
	}
}

// TestMethodRequestFieldsTarget holds a method with a target row: a condition that mixes a
// fact of the row and a request field, one that reads the request field alone, what the
// row's Execute answer says of each, and two grants held together.
func TestMethodRequestFieldsTarget(t *testing.T) {
	t.Parallel()
	w := newRequestFieldWorld(t)
	ctx := t.Context()

	envelopes := []struct {
		name string
		user accesstypes.User
		want map[string]bool
	}{
		// The request field is not known when the row is read: its part of the
		// condition counts as one that may hold, and the row's part still decides.
		{name: "a fact of the row and a request field: listed where the fact holds", user: fieldRater, want: map[string]bool{pilotVelaID: true, pilotLeadID: true, pilotWildeID: true, pilotDunnID: false, pilotPaxID: false}},
		{name: "a request field alone: listed on every row", user: fieldCapper, want: map[string]bool{pilotVelaID: true, pilotLeadID: true, pilotWildeID: true, pilotDunnID: true, pilotPaxID: true}},
	}
	for _, tt := range envelopes {
		t.Run("the row's Execute answer, "+tt.name, func(t *testing.T) {
			rows := sectorPilots(t, w.h, tt.user, anvil, "&capabilities=Execute")
			if len(rows) != len(tt.want) {
				t.Errorf("the list holds %d pilots, want %d", len(rows), len(tt.want))
			}
			for id, want := range tt.want {
				caps, _ := rows[id]["zzCapabilities"].(map[string]any)
				methods, _ := caps["Execute"].([]any)
				if got := slices.Contains(methods, any(setSectorPilotClearance)); got != want {
					t.Errorf("pilot %s: Execute answer %v lists the method = %v, want %v", id, methods, got, want)
				}
			}
		})
	}

	refused := func(pilot string) string {
		return "SetSectorPilotClearance may not run against SectorPilot " + pilot
	}
	// The fires run in order: each allowed one writes the registry, and the next reads it.
	fires := []struct {
		name          string
		user          accesstypes.User
		sector        string
		pilot         string
		clearance     int
		wantStatus    int
		wantBody      string
		wantClearance int64
	}{
		{name: "the fact holds and the request field passes", user: fieldRater, sector: anvil, pilot: pilotVelaID, clearance: 3, wantStatus: http.StatusNoContent, wantClearance: 3},
		{name: "the fact holds and the request field fails: the uniform refusal", user: fieldRater, sector: anvil, pilot: pilotVelaID, clearance: 4, wantStatus: http.StatusForbidden, wantBody: refused(pilotVelaID), wantClearance: 3},
		{name: "the request field passes and the fact fails: the same refusal", user: fieldRater, sector: anvil, pilot: pilotPaxID, clearance: 2, wantStatus: http.StatusForbidden, wantBody: refused(pilotPaxID), wantClearance: 3},
		{name: "both fail: the same refusal", user: fieldRater, sector: anvil, pilot: pilotPaxID, clearance: 5, wantStatus: http.StatusForbidden, wantBody: refused(pilotPaxID), wantClearance: 3},
		{name: "another sector's pilot is not found, though the request field passes", user: fieldRater, sector: anvil, pilot: pilotCassID, clearance: 2, wantStatus: http.StatusNotFound, wantBody: "SectorPilot " + pilotCassID + " does not exist", wantClearance: 2},
		{name: "and is not found where the request field fails: the refusal does not say a row is there", user: fieldRater, sector: anvil, pilot: pilotCassID, clearance: 5, wantStatus: http.StatusNotFound, wantBody: "SectorPilot " + pilotCassID + " does not exist", wantClearance: 2},
		{name: "the role is held at Anvil alone: Bastion is no sector of the caller's", user: fieldRater, sector: bastion, pilot: pilotCassID, clearance: 2, wantStatus: http.StatusNotFound, wantBody: "unknown domain", wantClearance: 2},
		{name: "a request field alone: a shared pilot, the field passes", user: fieldCapper, sector: anvil, pilot: pilotPaxID, clearance: 1, wantStatus: http.StatusNoContent, wantClearance: 1},
		{name: "a request field alone: the field fails, the uniform refusal", user: fieldCapper, sector: anvil, pilot: pilotPaxID, clearance: 4, wantStatus: http.StatusForbidden, wantBody: refused(pilotPaxID), wantClearance: 1},
		{name: "a request field alone: another sector's pilot is not found", user: fieldCapper, sector: anvil, pilot: pilotCassID, clearance: 4, wantStatus: http.StatusNotFound, wantBody: "SectorPilot " + pilotCassID + " does not exist", wantClearance: 2},
		// Two conditional grants are either-or: the Wing Commander's (the fact alone)
		// admits what the rater's (the fact and clearance up to 3) does not.
		{name: "two grants with conditions: the other grant admits clearance 5", user: fieldTwo, sector: anvil, pilot: pilotVelaID, clearance: 5, wantStatus: http.StatusNoContent, wantClearance: 5},
		{name: "two grants with conditions: neither admits a shared pilot", user: fieldTwo, sector: anvil, pilot: pilotPaxID, clearance: 2, wantStatus: http.StatusForbidden, wantBody: refused(pilotPaxID), wantClearance: 1},
		// A grant with no condition from another role settles it: nothing is judged.
		{name: "two grants, one with no condition: a shared pilot, clearance 5", user: fieldMarshal, sector: anvil, pilot: pilotPaxID, clearance: 5, wantStatus: http.StatusNoContent, wantClearance: 5},
		{name: "a clearance out of range is still the body's to refuse", user: fieldMarshal, sector: anvil, pilot: pilotPaxID, clearance: 9, wantStatus: http.StatusBadRequest, wantBody: "clearance 9 is not a clearance", wantClearance: 5},
	}
	for _, tt := range fires {
		t.Run(tt.name, func(t *testing.T) {
			status, body := doRequestAs(t, w.h, tt.user, http.MethodPost, sectorPath(tt.sector, "set-sector-pilot-clearance"), fmt.Sprintf(`{"pilotId":%q,"clearance":%d}`, tt.pilot, tt.clearance))
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantBody != "" && !strings.Contains(string(body), tt.wantBody) {
				t.Errorf("the answer %s lacks %q", body, tt.wantBody)
			}
			if got := readColumn[int64](ctx, t, w.db, "Pilots", spanner.Key{tt.pilot}, "Clearance"); got != tt.wantClearance {
				t.Errorf("the registry's clearance for %s = %d, want %d", tt.pilot, got, tt.wantClearance)
			}
		})
	}
}

// TestMethodRequestFieldsDigest holds what the permission digest says of a method whose
// grant reads a request field: conditional, the state a page reads before it draws the
// method's form, and what tells it to ask the server about a value with a dry run. A
// method the caller holds no grant on is not in the digest at all.
func TestMethodRequestFieldsDigest(t *testing.T) {
	t.Parallel()
	w := newRequestFieldWorld(t)

	probes := []struct {
		name   string
		user   accesstypes.User
		target string
		// want is the state the digest gives each method's Execute; "" is a method the
		// digest does not name.
		want map[string]string
	}{
		{name: "the delegate, at headquarters", user: fieldDelegate, target: consoleAPI + "/permission-digest", want: map[string]string{"AssignCrewRole": "conditional", "CreateCrewUser": "conditional", "InviteCrewUser": "conditional", "RemoveCrewRole": ""}},
		{name: "the rater, at Anvil", user: fieldRater, target: consoleAPI + "/permission-digest?domain=" + anvil, want: map[string]string{setSectorPilotClearance: "conditional"}},
		{name: "the capper, at Anvil", user: fieldCapper, target: consoleAPI + "/permission-digest?domain=" + anvil, want: map[string]string{setSectorPilotClearance: "conditional"}},
	}
	for _, tt := range probes {
		t.Run(tt.name, func(t *testing.T) {
			status, body := doRequestAs(t, w.h, tt.user, http.MethodGet, tt.target, "")
			assertStatus(t, status, http.StatusOK, body)
			var digest map[string]map[string]string
			if err := json.Unmarshal(body, &digest); err != nil {
				t.Fatalf("the digest does not parse: %v", err)
			}
			for method, want := range tt.want {
				if got := digest[method]["Execute"]; got != want {
					t.Errorf("the digest says %q of Execute on %s, want %q", got, method, want)
				}
			}
		})
	}
}

// TestMethodRequestFieldsValues holds two answers about a value: a string left out, and a list whose every element passes.
func TestMethodRequestFieldsValues(t *testing.T) {
	t.Parallel()
	w := newRequestFieldWorld(t)

	// A string field left out of the request is the empty string, a value: the
	// condition judges it, and the body is what refuses it.
	status, body := doRequestAs(t, w.h, fieldDelegate, http.MethodPost, consoleAPI+"/assign-crew-role", `{"username":"veteran"}`)
	t.Logf("assign-crew-role with no role: status %d: %s", status, body)
	if status == http.StatusForbidden {
		t.Errorf("a role left out was refused by the grant; the empty string is not the role the condition names")
	}

	// An invitation whose every role the condition admits.
	status, body = doRequestAs(t, w.h, fieldDelegate, http.MethodPost, consoleAPI+"/invite-crew-user", `{"email":"gil-`+identitySuffix()+`@example.com","roles":["ClientBrowser","CrewCommon"]}`)
	t.Logf("invite-crew-user with two admitted roles: status %d: %s", status, body)
	if status == http.StatusForbidden {
		t.Errorf("an invitation into two admitted roles was refused by the grant")
	}
}
