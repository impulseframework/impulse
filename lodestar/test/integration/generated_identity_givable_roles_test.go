// Demonstrates: auth.role-picker, auth.generated-identity, execute-condition.request-field.
package integration

// generated_identity_givable_roles_test: the roles the whole application's management
// picks a role from (CrewGivableRoles), the list the role field of Create, Invite,
// Assign and the group's Give a role names, so the Accounts page brings a person in with
// a role chosen and not typed.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/resource"
)

// TestGeneratedIdentityGivableRoles holds the list the whole application's dialogs pick
// a role from. The world runs over the crew's role file with limits put on the
// Headquarters role's grants of the four methods that give one: none of them gives
// Adjutant, and only the invitation gives Surveyor. A person who holds Headquarters and
// is no super administrator is then offered, for the whole application, every role but
// Adjutant and the Super Administrator role: Surveyor stays, since one method takes it.
// The list says where each role is held: the whole application's roles and every
// sector's when no sector is named, one sector's when one is, each row with its place;
// a filter on everyDomain narrows it as the dialog does. A super administrator is
// offered the Super Administrator role, a person with no grant on the list is refused,
// and the methods hold what the list said: the invitation's check passes Surveyor and
// refuses Adjutant, and Assign, which gives a role the limits leave alone, refuses
// both. This suite serves no session manager, so the invitation the check let through
// fails where its link would be made (500): the status that tells the check was passed,
// since a refused check answers 403 and never reaches that step.
//
// The cases share the world and run in parallel. The one write among them gives the
// Headquarters person a role that carries no grant to give any, so it changes nothing
// another case reads.
func TestGeneratedIdentityGivableRoles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	const notGiven, invitedOnly = "Adjutant", "Surveyor"
	limit := fmt.Sprintf("new.role != '%s' AND new.role != '%s'", notGiven, invitedOnly)
	roles := crew.Roles()
	for _, method := range []string{"AssignCrewRole", "CreateCrewUser", "GiveCrewGroupRole"} {
		roles = withCondition(t, roles, "Headquarters", "Execute", method, limit)
	}
	roles = withCondition(t, roles, "Headquarters", "Execute", "InviteCrewUser", fmt.Sprintf("new.role != '%s'", notGiven))
	w := buildIdentityWorldOver(t, true, roles)

	// A person who holds Headquarters, and the Super Administrator role nowhere.
	var deskID impulse.UUID
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		deskID, err = w.createCrewUser(ctx, txn, "hqdesk", "Headquarters", "CrewCommon")

		return err
	}); err != nil {
		t.Fatal(err)
	}
	desk := knownAs(t, "hqdesk", deskID)

	const rolesPath = consoleAPI + "/crew-givable-roles?limit=all"
	type place struct {
		domain      string
		everyDomain bool
	}
	list := func(t *testing.T, user accesstypes.User, filter string) map[place][]string {
		t.Helper()
		target := rolesPath
		if filter != "" {
			target += "&filter=" + url.QueryEscape(filter)
		}
		status, body := doRequestAs(t, w.h, user, http.MethodGet, target, "")
		assertStatus(t, status, http.StatusOK, body)
		held := map[place][]string{}
		for _, row := range decodeRows(t, body) {
			name, _ := row["name"].(string)
			domain, _ := row["domain"].(string)
			every, _ := row["everyDomain"].(bool)
			if description, _ := row["description"].(string); description == "" {
				t.Errorf("%s is listed with no description: %v", name, row)
			}
			at := place{domain: domain, everyDomain: every}
			held[at] = append(held[at], name)
		}

		return held
	}
	whole, everySector, inAnvil := place{}, place{everyDomain: true}, place{domain: anvil}

	t.Run("for the whole application: every role the grants let the caller give, by any one of the methods", func(t *testing.T) {
		t.Parallel()
		held := list(t, desk, "")
		for _, role := range []string{"Headquarters", "CrewCommon", invitedOnly} {
			if !slices.Contains(held[whole], role) {
				t.Errorf("the whole application's roles offered = %v, want %s among them", held[whole], role)
			}
		}
		for _, role := range []string{notGiven, string(accesstypes.SuperAdministrator), "SectorMarshal"} {
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

	t.Run("in one sector: the sector's roles, each naming the sector", func(t *testing.T) {
		t.Parallel()
		held := list(t, desk, "domain:eq:"+anvil)
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
		if held := list(t, desk, "everyDomain:eq:false"); len(held) != 1 || !slices.Contains(held[whole], "Headquarters") {
			t.Errorf("everyDomain false lists %v, want the whole application's roles alone", held)
		}
		if held := list(t, desk, "everyDomain:eq:true"); len(held) != 1 || !slices.Contains(held[everySector], "Cadet") {
			t.Errorf("everyDomain true lists %v, want every sector's roles alone", held)
		}
	})

	t.Run("a super administrator is offered the Super Administrator role, for the whole application alone", func(t *testing.T) {
		t.Parallel()
		held := list(t, "governor", "")
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
	})

	t.Run("a filter that names sectors any other way is refused, saying what to send", func(t *testing.T) {
		t.Parallel()
		status, body := doRequestAs(t, w.h, desk, http.MethodGet, rolesPath+"&filter="+url.QueryEscape("domain:ne:"+anvil), "")
		assertStatus(t, status, http.StatusBadRequest, body)
	})

	t.Run("a role the list offers by the invitation alone passes the invitation's check and no other method's, and the one it left out passes none", func(t *testing.T) {
		t.Parallel()
		invitation := func(role string) string {
			return fmt.Sprintf(`{"email":"surveyor-%s@lakeside.example","roles":[%q]}`, identitySuffix(), role)
		}
		assignment := func(role string) string {
			return fmt.Sprintf(`{"userId":%q,"role":%q}`, deskID.String(), role)
		}
		tests := []struct {
			name string
			path string
			body string
			want int
		}{
			{
				name: "the invitation's check passes the role only it gives, and the suite has no session manager to make the link",
				path: "/invite-crew-user",
				body: invitation(invitedOnly),
				want: http.StatusInternalServerError,
			},
			{
				name: "the invitation's check refuses the role no method gives",
				path: "/invite-crew-user",
				body: invitation(notGiven),
				want: http.StatusForbidden,
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
		}
	})
}

// withCondition is the role file with the condition put on the grant the role holds on
// the resource, for a suite that proves what a condition there does. It fails where the
// role holds no such grant, or holds it under a condition already, so it never hands
// back the file unchanged nor changes a condition the file says.
func withCondition(t *testing.T, roles access.RoleFile, role, permission, res, condition string) access.RoleFile {
	t.Helper()

	var file map[string]map[string][]map[string]any
	if err := json.Unmarshal(roles, &file); err != nil {
		t.Fatalf("reading the role file: %v", err)
	}
	put := false
	for _, set := range file["roles"] {
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
