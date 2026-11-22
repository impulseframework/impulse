// Demonstrates: auth.role-picker, auth.generated-identity, execute-condition.request-field.
package integration

// generated_identity_givable_roles_test: the roles the whole application's management
// picks a role from (CrewGivableRoles), the list the role field of Create, Invite,
// Assign and the group's Give a role names, each for itself, so the Accounts page brings
// a person in with a role chosen and not typed, and each dialog offers what its own
// method takes.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// TestGeneratedIdentityGivableRoles holds the list the whole application's dialogs pick
// a role from. The world runs over the crew's role file with limits put on the
// Headquarters role's grants of the three methods that give one: none of them gives
// Adjutant, and only the invitation gives Surveyor. The list is asked for one method at
// a time, by a filter that names it. A person who holds Headquarters and is no super
// administrator is then offered, by each method and for the whole application, every
// role but Adjutant and the Super Administrator role, and Surveyor by the invitation
// alone: it is in the invitation's list and in no other method's. A request that names
// no method is listed no role, and one that names a method any other way, or a method
// that picks no role from the list, is refused with the names to send.
// The list says where each role is held: the whole application's roles and every
// sector's when no sector is named, one sector's when one is, each row with its place;
// a filter on everyDomain narrows it as the dialog does. A super administrator is
// offered the Super Administrator role, a person with no grant on the list is refused,
// and the methods hold what the list said: the invitation's check passes Surveyor and
// refuses Adjutant, and Assign, which gives a role the limits leave alone, refuses
// both. This suite serves no session manager, and its stand-in for one keeps each
// invitation it is handed before it fails (preparedInvitations), so the invitation the
// check let through is told from any other failure: it answers 500 and the stand-in
// was handed it, with the role, where a refused check answers 403 and hands it nothing.
//
// The method that gives a group a role is asked with the group beside it, and is then
// listed the roles that group may hold: a group kept in Anvil is offered Anvil's roles
// in Anvil and no role anywhere else, no group is offered the Super Administrator
// role, and the method takes what the list offered and refuses what it left out. Asked
// with no group the method is listed no role.
//
// The cases share the world and run in parallel. Two writes are among them: one gives
// the Headquarters person a role that carries no grant to give any, and one gives a
// group nobody is in a role in Anvil, so neither changes what another case reads.
func TestGeneratedIdentityGivableRoles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	const notGiven, invitedOnly = "Adjutant", "Surveyor"
	// The three methods whose role field picks from the list, as a request names them.
	const assign, invite, giveGroup = "AssignCrewRole", "InviteCrewUser", "GiveCrewGroupRole"
	limit := fmt.Sprintf("new.role != '%s' AND new.role != '%s'", notGiven, invitedOnly)
	roles := crew.Roles()
	for _, method := range []string{assign, giveGroup} {
		roles = withCondition(t, roles, "Headquarters", "Execute", method, limit)
	}
	roles = withCondition(t, roles, "Headquarters", "Execute", invite, fmt.Sprintf("new.role != '%s'", notGiven))
	w := buildIdentityWorldOver(t, true, roles)

	// A person who holds Headquarters, and the Super Administrator role nowhere.
	deskID := w.seedCrewUser(ctx, t, "hqdesk", "Headquarters", "CrewCommon")
	desk := userOf(deskID)

	// The groups of the cases: one kept for the whole application, which holds a role
	// anywhere a person can, and one kept in Anvil.
	newGroup := func(name, in string) string {
		t.Helper()
		request := map[string]any{"name": name, "description": "A group the roles to give are listed for."}
		if in != "" {
			request["domain"] = in
		}
		sent, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		status, body := doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/create-crew-group", string(sent))
		assertStatus(t, status, http.StatusOK, body)

		return cell[string](t, decodeRow(t, body), "id")
	}
	fleetStaff, nightWatch := newGroup("Fleet staff", ""), newGroup("Night watch", anvil)

	const rolesPath = consoleAPI + "/crew-givable-roles?limit=all"
	type place struct {
		domain      string
		everyDomain bool
	}
	// list is the list as one method's dialog asks it: the method named, then what the
	// case filters by.
	list := func(t *testing.T, user accesstypes.User, method, filter string) map[place][]string {
		t.Helper()
		by := "method:eq:" + method
		// The group's method lists one group's roles: the whole application's group,
		// where the case names no other.
		if method == giveGroup && !strings.Contains(filter, "group:") {
			by += ",group:eq:" + fleetStaff
		}
		if filter != "" {
			by += "," + filter
		}
		status, body := doRequestAs(t, w.h, user, http.MethodGet, rolesPath+"&filter="+url.QueryEscape(by), "")
		assertStatus(t, status, http.StatusOK, body)
		held := map[place][]string{}
		for _, row := range decodeRows(t, body) {
			name, _ := row["name"].(string)
			domain, _ := row["domain"].(string)
			every, _ := row["everyDomain"].(bool)
			if description, _ := row["description"].(string); description == "" {
				t.Errorf("%s is listed with no description: %v", name, row)
			}
			if listedFor, _ := row["method"].(string); listedFor != method {
				t.Errorf("%s is listed for the method %q, want %s, the one the request names: %v", name, listedFor, method, row)
			}
			at := place{domain: domain, everyDomain: every}
			held[at] = append(held[at], name)
		}

		return held
	}
	whole, everySector, inAnvil := place{}, place{everyDomain: true}, place{domain: anvil}

	t.Run("for the whole application: every role the method's grant lets the caller give", func(t *testing.T) {
		t.Parallel()
		held := list(t, desk, assign, "")
		for _, role := range []string{"Headquarters", "CrewCommon"} {
			if !slices.Contains(held[whole], role) {
				t.Errorf("the whole application's roles offered = %v, want %s among them", held[whole], role)
			}
		}
		for _, role := range []string{notGiven, invitedOnly, string(accesstypes.SuperAdministrator), "SectorMarshal"} {
			if slices.Contains(held[whole], role) {
				t.Errorf("the whole application's roles offered = %v, want no %s", held[whole], role)
			}
		}
		for _, role := range []string{"SectorMarshal", "Cadet", "Pilot"} {
			if !slices.Contains(held[everySector], role) {
				t.Errorf("the roles offered in every sector = %v, want %s among them", held[everySector], role)
			}
		}
		if slices.Contains(held[everySector], "Headquarters") {
			t.Errorf("the roles offered in every sector = %v, want no role of the whole application", held[everySector])
		}
		for at := range held {
			if at != whole && at != everySector {
				t.Errorf("a row is listed at %+v with no sector named: %v", at, held[at])
			}
		}
	})

	// The role one method alone takes is offered by that method's dialog and by no
	// other's: each list is its own method's grant, and never the widest of the three.
	t.Run("a role the invitation alone gives is in the invitation's list and in no other method's", func(t *testing.T) {
		t.Parallel()
		invited := list(t, desk, invite, "")
		for _, role := range []string{invitedOnly, "Headquarters", "CrewCommon"} {
			if !slices.Contains(invited[whole], role) {
				t.Errorf("the roles the invitation offers for the whole application = %v, want %s among them", invited[whole], role)
			}
		}
		if slices.Contains(invited[whole], notGiven) {
			t.Errorf("the roles the invitation offers for the whole application = %v, want no %s: its grant refuses the role", invited[whole], notGiven)
		}
		for _, method := range []string{assign, giveGroup} {
			held := list(t, desk, method, "")
			if slices.Contains(held[whole], invitedOnly) {
				t.Errorf("%s offers %v for the whole application, want no %s: its own grant refuses the role, whatever the invitation's takes", method, held[whole], invitedOnly)
			}
			if !slices.Contains(held[whole], "Headquarters") {
				t.Errorf("%s offers %v for the whole application, want Headquarters among them", method, held[whole])
			}
		}
	})

	// The dialog that gives a group a role offers what the group itself may hold, by the
	// rule the method keeps. The governor asks, a super administrator: nobody is offered
	// more.
	t.Run("a group kept in Anvil is offered Anvil's roles in Anvil alone, and no group the Super Administrator role", func(t *testing.T) {
		t.Parallel()
		ofDeck := "group:eq:" + nightWatch
		for _, elsewhere := range []string{"", ",everyDomain:eq:false", ",everyDomain:eq:true", ",domain:eq:" + bastion} {
			if held := list(t, "governor", giveGroup, ofDeck+elsewhere); len(held) != 0 {
				t.Errorf("the group kept in Anvil, asked with %q, is offered %v, want no role: it holds roles in Anvil alone", elsewhere, held)
			}
		}
		held := list(t, "governor", giveGroup, ofDeck+",domain:eq:"+anvil)
		if len(held) != 1 {
			t.Fatalf("the group kept in Anvil, asked for Anvil, is offered roles at %v, want Anvil's alone", held)
		}
		for _, role := range []string{"SectorMarshal", "Cadet", "Pilot"} {
			if !slices.Contains(held[inAnvil], role) {
				t.Errorf("Anvil's roles offered the group kept there = %v, want %s among them", held[inAnvil], role)
			}
		}
		// The method takes what the list offered, and refuses what it left out.
		give := func(where string) (int, []byte) {
			return doRequestAs(t, w.h, "governor", http.MethodPost, consoleAPI+"/give-crew-group-role", fmt.Sprintf(`{"groupId":%q,"role":"Cadet",%s}`, nightWatch, where))
		}
		status, body := give(fmt.Sprintf(`"domain":%q`, anvil))
		assertStatus(t, status, http.StatusNoContent, body)
		status, body = give(`"everyDomain":true`)
		assertStatus(t, status, http.StatusBadRequest, body)

		// A group kept for the whole application is offered a role anywhere a person is,
		// and never the Super Administrator role, which the governor is offered for a person.
		staff := list(t, "governor", giveGroup, "")
		if !slices.Contains(staff[whole], "Headquarters") || !slices.Contains(staff[everySector], "Cadet") {
			t.Errorf("the whole application's group is offered %v, want the whole application's roles and every sector's", staff)
		}
		for at, names := range staff {
			if slices.Contains(names, string(accesstypes.SuperAdministrator)) {
				t.Errorf("the whole application's group is offered the Super Administrator role at %+v: %v", at, names)
			}
		}
	})

	t.Run("the group's method asked with no group is listed no role, and a group named any other way is refused", func(t *testing.T) {
		t.Parallel()
		ask := func(filter string) (int, []byte) {
			return doRequestAs(t, w.h, "governor", http.MethodGet, rolesPath+"&filter="+url.QueryEscape(filter), "")
		}
		status, body := ask("method:eq:" + giveGroup)
		assertStatus(t, status, http.StatusOK, body)
		if rows := decodeRows(t, body); len(rows) != 0 {
			t.Errorf("%s asked with no group is listed %v, want no role", giveGroup, rows)
		}
		for _, filter := range []string{
			"method:eq:" + assign + ",group:eq:" + nightWatch,
			"group:eq:" + nightWatch,
			"method:eq:" + giveGroup + ",group:eq:deck-crew",
			"method:eq:" + giveGroup + ",group:in:(" + nightWatch + "," + fleetStaff + ")",
		} {
			if status, body := ask(filter); status != http.StatusBadRequest || !strings.Contains(string(body), "group:eq:") {
				t.Errorf("filter %q: status %d, want 400 with the filter to send: %s", filter, status, body)
			}
		}
		// A group that does not exist is listed no role, as a group kept in another
		// sector is: the list never says whether an id is a group's. The id here is a
		// sector's, in the whole service's roles and in Anvil's.
		for _, where := range []string{"", ",domain:eq:" + anvil} {
			status, body = ask("method:eq:" + giveGroup + ",group:eq:" + cinder + where)
			assertStatus(t, status, http.StatusOK, body)
			if rows := decodeRows(t, body); len(rows) != 0 {
				t.Errorf("%s asked with an id that is no group's (%q) is listed %v, want no role", giveGroup, where, rows)
			}
		}
	})

	t.Run("a request that names no method is listed no role", func(t *testing.T) {
		t.Parallel()
		for _, tt := range []struct {
			user   accesstypes.User
			filter string
		}{
			{user: desk},
			{user: desk, filter: "domain:eq:" + anvil},
			{user: desk, filter: "everyDomain:eq:false"},
			// A filter that joins its conditions by OR names no method the list can read
			// apart from the rest.
			{user: desk, filter: "method:eq:" + assign + "|method:eq:" + invite},
			{user: "governor"},
		} {
			target := rolesPath
			if tt.filter != "" {
				target += "&filter=" + url.QueryEscape(tt.filter)
			}
			status, body := doRequestAs(t, w.h, tt.user, http.MethodGet, target, "")
			assertStatus(t, status, http.StatusOK, body)
			if rows := decodeRows(t, body); len(rows) != 0 {
				t.Errorf("%s, filter %q: listed %d roles with no method named, want none: %s", tt.user, tt.filter, len(rows), body)
			}
		}
	})

	t.Run("a filter that names a method any other way is refused, with the names to send", func(t *testing.T) {
		t.Parallel()
		const methods = "AssignCrewRole, InviteCrewUser, GiveCrewGroupRole"
		for _, filter := range []string{
			// A method that picks no role from the list: one that gives none, and a
			// sector's own, whose list is the sector's.
			"method:eq:SetCrewUserName",
			"method:eq:AssignCrewSectorRole",
			"method:in:(" + assign + "," + invite + ")",
			"method:ne:" + assign,
			"method:eq:" + assign + ",method:eq:" + invite,
		} {
			status, body := doRequestAs(t, w.h, desk, http.MethodGet, rolesPath+"&filter="+url.QueryEscape(filter), "")
			if status != http.StatusBadRequest || !strings.Contains(string(body), methods) || !strings.Contains(string(body), "method:eq:") {
				t.Errorf("filter %q: status %d, want 400 naming the methods (%s) and the filter to send: %s", filter, status, methods, body)
			}
		}
	})

	t.Run("in one sector: the sector's roles, each naming the sector", func(t *testing.T) {
		t.Parallel()
		held := list(t, desk, assign, "domain:eq:"+anvil)
		if len(held) != 1 || len(held[inAnvil]) == 0 {
			t.Fatalf("the roles listed are held at %v, want Anvil's alone", held)
		}
		for _, role := range []string{"SectorMarshal", "Cadet", "Pilot"} {
			if !slices.Contains(held[inAnvil], role) {
				t.Errorf("Anvil's roles offered = %v, want %s among them", held[inAnvil], role)
			}
		}
		if slices.Contains(held[inAnvil], "Headquarters") {
			t.Errorf("Anvil's roles offered = %v, want no role of the whole application", held[inAnvil])
		}
		if !slices.IsSorted(held[inAnvil]) {
			t.Errorf("Anvil's roles are not listed by name: %v", held[inAnvil])
		}
	})

	t.Run("a filter on everyDomain narrows the list to one of the two places, as the dialog asks it", func(t *testing.T) {
		t.Parallel()
		// The invitation's dialog asks "In every sector" as the others do.
		for _, method := range []string{assign, invite, giveGroup} {
			if held := list(t, desk, method, "everyDomain:eq:false"); len(held) != 1 || !slices.Contains(held[whole], "Headquarters") {
				t.Errorf("%s, everyDomain false lists %v, want the whole application's roles alone", method, held)
			}
			if held := list(t, desk, method, "everyDomain:eq:true"); len(held) != 1 || !slices.Contains(held[everySector], "Cadet") {
				t.Errorf("%s, everyDomain true lists %v, want every sector's roles alone", method, held)
			}
		}
	})

	t.Run("a super administrator is offered the Super Administrator role, for the whole application alone", func(t *testing.T) {
		t.Parallel()
		held := list(t, "governor", assign, "")
		if !slices.Contains(held[whole], string(accesstypes.SuperAdministrator)) {
			t.Errorf("the governor's whole-application roles = %v, want the Super Administrator role among them", held[whole])
		}
		if slices.Contains(held[everySector], string(accesstypes.SuperAdministrator)) {
			t.Errorf("the governor's roles in every sector = %v, want no Super Administrator role", held[everySector])
		}
	})

	t.Run("a person with no grant on the list is refused it", func(t *testing.T) {
		t.Parallel()
		status, body := doRequestAs(t, w.h, "cadet", http.MethodGet, rolesPath, "")
		assertStatus(t, status, http.StatusForbidden, body)
		status, body = doRequestAs(t, w.h, "cadet", http.MethodGet, rolesPath+"&filter="+url.QueryEscape("method:eq:"+assign), "")
		assertStatus(t, status, http.StatusForbidden, body)
	})

	t.Run("a filter that names sectors any other way is refused, saying what to send, whether a method is named or not", func(t *testing.T) {
		t.Parallel()
		for _, filter := range []string{"domain:ne:" + anvil, "method:eq:" + assign + ",domain:ne:" + anvil} {
			status, body := doRequestAs(t, w.h, desk, http.MethodGet, rolesPath+"&filter="+url.QueryEscape(filter), "")
			assertStatus(t, status, http.StatusBadRequest, body)
		}
	})

	t.Run("a role the list offers by the invitation alone passes the invitation's check and no other method's, and the one it left out passes none", func(t *testing.T) {
		t.Parallel()
		// Each invitation goes to an address of its own, which the stand-in for the
		// session manager keeps it under.
		passed, refused := "surveyor-"+identitySuffix()+"@lakeside.example", "adjutant-"+identitySuffix()+"@lakeside.example"
		invitation := func(address, role string) string {
			return fmt.Sprintf(`{"email":%q,"roles":[%q]}`, address, role)
		}
		assignment := func(role string) string {
			return fmt.Sprintf(`{"userId":%q,"role":%q}`, deskID.String(), role)
		}
		tests := []struct {
			name string
			path string
			body string
			want int
			// invited is the address an invitation goes to, and prepared the roles the
			// stand-in for the session manager is handed for it: none where the request
			// is refused before the link is made.
			invited  string
			prepared []string
		}{
			{
				name:     "the invitation's check passes the role only it gives: the request reaches the step that makes the link, with the role, and fails there alone, in the suite's stand-in for the session manager",
				path:     "/invite-crew-user",
				body:     invitation(passed, invitedOnly),
				want:     http.StatusInternalServerError,
				invited:  passed,
				prepared: []string{invitedOnly},
			},
			{
				name:    "the invitation's check refuses the role no method gives, before any link is made",
				path:    "/invite-crew-user",
				body:    invitation(refused, notGiven),
				want:    http.StatusForbidden,
				invited: refused,
			},
			{
				name: "Assign gives a role the limits leave alone",
				path: "/assign-crew-role",
				body: assignment("BulletinOfficer"),
				want: http.StatusNoContent,
			},
			{
				name: "Assign refuses the role only the invitation gives",
				path: "/assign-crew-role",
				body: assignment(invitedOnly),
				want: http.StatusForbidden,
			},
			{
				name: "Assign refuses the role no method gives",
				path: "/assign-crew-role",
				body: assignment(notGiven),
				want: http.StatusForbidden,
			},
		}
		for _, tt := range tests {
			status, body := doRequestAs(t, w.h, desk, http.MethodPost, consoleAPI+tt.path, tt.body)
			if status != tt.want {
				t.Errorf("%s: status %d, want %d: %s", tt.name, status, tt.want, body)
			}
			if tt.invited == "" {
				continue
			}
			var prepared [][]string
			for _, req := range preparedInvitations.take(tt.invited) {
				prepared = append(prepared, req.RoleNames)
			}
			switch {
			case tt.prepared == nil && len(prepared) != 0:
				t.Errorf("%s: the session manager's stand-in was handed the invitation with %v, want it never reached", tt.name, prepared)
			case tt.prepared != nil && (len(prepared) != 1 || !slices.Equal(prepared[0], tt.prepared)):
				t.Errorf("%s: the session manager's stand-in was handed %v for %s, want one invitation with %v: a 500 that never reached it is another failure", tt.name, prepared, tt.invited, tt.prepared)
			}
		}
	})
}

// withCondition is the role file with the condition put on the grant the role holds on
// the resource, for a suite that proves what a condition there does. It fails where the
// role holds no such grant, or holds it under a condition already, so it never hands
// back the file unchanged nor changes a condition the file says.
func withCondition(t *testing.T, roles access.RoleFile, role, permission, res, condition string) access.RoleFile {
	t.Helper()

	var file looseRoleFile
	if err := json.Unmarshal(roles, &file); err != nil {
		t.Fatalf("reading the role file: %v", err)
	}
	put := false
	for _, set := range file.Roles {
		for _, entry := range set {
			if entry["name"] != role {
				continue
			}
			permissions, _ := entry["permissions"].(map[string]any)
			grants, _ := permissions[permission].([]any)
			for _, g := range grants {
				grant, ok := g.(map[string]any)
				if !ok || grant["resource"] != res {
					continue
				}
				if _, conditional := grant["condition"]; conditional {
					t.Fatalf("the %s role's %s grant on %s carries a condition already", role, permission, res)
				}
				grant["condition"] = condition
				put = true
			}
		}
	}
	if !put {
		t.Fatalf("the %s role holds no %s grant on %s to put a condition on", role, permission, res)
	}
	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("writing the role file: %v", err)
	}

	return raw
}
