// Demonstrates: tenancy.invited-tenant, auth.role-picker.
package integration

// invited_sector_roles_test: the roles the invitation that makes a sector picks its
// starting role from (CrewInvitedSectorRoles), the list its defaultRole names, so the
// Invite a sector dialog offers the roles the invitation itself takes from its sender.

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// TestInvitedSectorRoles holds the list the invitation that makes a sector picks the
// starting role of the sector's directory people from. The world runs over the crew's
// role file with two limits on the Headquarters role: its grant to assign a role gives
// no Cadet, and its grant on the invitation sends none whose people start as Pilot. A
// person who holds Headquarters and is no super administrator is listed the roles held
// in every sector that they could give in a sector no row names yet and that the
// invitation's own grant lets them name, each with its description: SectorMarshal is
// among them, Cadet and Pilot are not, nor is any role of the whole application or the
// Super Administrator role. The invitation holds what the list said: it refuses Cadet in
// a sentence that names it, refuses Pilot, and passes SectorMarshal. This suite serves
// no session manager, and its stand-in for one keeps each invitation it is handed before
// it fails (preparedInvitations), so the invitation the check let through is told from
// any other failure: it answers 500 and the stand-in was handed it, with the sector it
// makes, where a refused check answers 403 and hands it nothing. A Surveyor, who may
// give no role, is listed none, and a person with no grant on the list is refused it.
//
// The cases share the world and run in parallel; the invitations' checks write nothing.
func TestInvitedSectorRoles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	const notGiven, notInvited, offered = "Cadet", "Pilot", "SectorMarshal"
	roles := crew.Roles()
	roles = withCondition(t, roles, "Headquarters", "Execute", "AssignCrewRole", fmt.Sprintf("new.role != '%s'", notGiven))
	roles = withCondition(t, roles, "Headquarters", "Execute", "InviteCrewSector", fmt.Sprintf("new.role != '%s'", notInvited))
	w := buildIdentityWorldOver(t, true, roles)

	// A person who holds Headquarters, and the Super Administrator role nowhere; one who
	// holds Surveyor, who invites and may give no role; and one who holds neither.
	desk := userOf(w.seedCrewUser(ctx, t, "hqdesk", "Headquarters", "CrewCommon"))
	chartroom := userOf(w.seedCrewUser(ctx, t, "chartroom", "Surveyor"))
	bystander := userOf(w.seedCrewUser(ctx, t, "bystander", "CrewCommon"))

	const rolesPath = consoleAPI + "/crew-invited-sector-roles?limit=all"
	// listed is the list as the invitation's dialog asks it, each role by its name with
	// its description.
	listed := func(t *testing.T, user accesstypes.User) map[string]string {
		t.Helper()
		status, body := doRequestAs(t, w.h, user, http.MethodGet, rolesPath, "")
		assertStatus(t, status, http.StatusOK, body)
		described := map[string]string{}
		for _, row := range decodeRows(t, body) {
			name, _ := row["name"].(string)
			described[name], _ = row["description"].(string)
		}

		return described
	}

	t.Run("Headquarters is listed the roles the invitation takes from them, each with its description", func(t *testing.T) {
		t.Parallel()
		described := listed(t, desk)
		if _, ok := described[offered]; !ok {
			t.Errorf("the roles listed = %v, want %s among them", described, offered)
		}
		for name, description := range described {
			if description == "" {
				t.Errorf("%s is listed with no description", name)
			}
		}
		absent := []string{notGiven, notInvited, "Headquarters", "CrewCommon", "Surveyor", string(accesstypes.SuperAdministrator)}
		for _, role := range absent {
			if _, ok := described[role]; ok {
				t.Errorf("the roles listed = %v, want no %s", described, role)
			}
		}
	})

	t.Run("the invitation takes the role the list offers and refuses the two it left out", func(t *testing.T) {
		t.Parallel()
		// Each invitation goes to an address of its own, which the stand-in for the
		// session manager keeps it under.
		address := func(role string) string {
			return strings.ToLower(role) + "-" + identitySuffix() + "@starting-roles.example"
		}
		tests := []struct {
			name     string
			role     string
			want     int
			wantBody string
			// prepared is the sector of the invitation the stand-in for the session
			// manager is handed, "" where the request is refused before the link is made.
			prepared string
		}{
			{
				name:     "a role they may not give is refused, in a sentence that names it",
				role:     notGiven,
				want:     http.StatusForbidden,
				wantBody: "The people of the new sector's directory would start with the role Cadet, which you may not give.",
			},
			{
				name: "a role the invitation's own grant refuses is refused",
				role: notInvited,
				want: http.StatusForbidden,
			},
			{
				name:     "the role the list offers passes the check: the request reaches the step that makes the link, with the sector, and fails there alone, in the suite's stand-in for the session manager",
				role:     offered,
				want:     http.StatusInternalServerError,
				prepared: "Dawn",
			},
		}
		for _, tt := range tests {
			invited := address(tt.role)
			body := fmt.Sprintf(`{"email":%q,"sectorName":"Dawn","sectorRegion":"Outer frontier","sectorEstablished":"2226-10-02","defaultRole":%q}`, invited, tt.role)
			status, got := doRequestAs(t, w.h, desk, http.MethodPost, inviteSectorPath, body)
			if status != tt.want || !strings.Contains(string(got), tt.wantBody) {
				t.Errorf("%s: status %d, want %d saying %q: %s", tt.name, status, tt.want, tt.wantBody, got)
			}
			var prepared []string
			for _, req := range preparedInvitations.take(invited) {
				if req.NewTenant == nil {
					t.Errorf("%s: the session manager's stand-in was handed an invitation that makes no sector: %+v", tt.name, req)

					continue
				}
				prepared = append(prepared, req.NewTenant.Name)
			}
			switch {
			case tt.prepared == "" && len(prepared) != 0:
				t.Errorf("%s: the session manager's stand-in was handed the invitation of %v, want it never reached", tt.name, prepared)
			case tt.prepared != "" && !slices.Equal(prepared, []string{tt.prepared}):
				t.Errorf("%s: the session manager's stand-in was handed the invitations of %v, want the one of %s: a 500 that never reached it is another failure", tt.name, prepared, tt.prepared)
			}
		}
	})

	t.Run("a Surveyor, who may give no role, is listed none", func(t *testing.T) {
		t.Parallel()
		if described := listed(t, chartroom); len(described) != 0 {
			t.Errorf("the roles listed = %v, want none", described)
		}
	})

	t.Run("a person with no grant on the list is refused it", func(t *testing.T) {
		t.Parallel()
		status, body := doRequestAs(t, w.h, bystander, http.MethodGet, rolesPath, "")
		assertStatus(t, status, http.StatusForbidden, body)
	})
}
