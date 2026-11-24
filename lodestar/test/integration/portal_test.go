package integration

// portal_test (design plan §9): a client under the portal prefix lists only its company's
// missions and its own filed calls; a refit, ship, or squadron route under the prefix 404s
// as a non-member; StandDown fires on an open own-company mission and refuses on another
// company's; the portal digest names the portal's members and no others; the portal's
// user-domains lists the sectors where the ClientPortal role is held; the client's
// statement is the portal-only manual resource.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
)

// The seeded contact records, matching schema/devseed: Cleo's own and another company's.
const (
	contactCleoID     = "31000000-0000-4000-8000-000000000001"
	contactSurveyorID = "31000000-0000-4000-8000-000000000002"
)

// Demonstrates: outlet.session, outlet.isolation, @subjectValue.second-anchor, create-form-narrowing, tenancy.user-domains.
func TestClientPortal(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	// The portal grants this suite proves, each pinned to the roles file (conditions-proven).
	provesGrant(t, members.Roles(), "ClientAccount", "List", "ClientContacts", "userId = subject")
	provesGrant(t, members.Roles(), "ClientAccount", "Read", "ClientContacts", "userId = subject")
	provesGrant(t, members.Roles(), "ClientPortal", "List", "Missions", "client = subject.client")
	provesGrant(t, members.Roles(), "ClientPortal", "Read", "Missions", "client = subject.client")
	provesGrant(t, members.Roles(), "ClientPortal", "List", "DistressCalls", "filedBy = subject")
	provesGrant(t, members.Roles(), "ClientPortal", "Read", "DistressCalls", "filedBy = subject")

	t.Run("cleo sees only Halvard's missions, with the portal width", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, clientUser, http.MethodGet, portalPath(anvil, "missions?limit=200"), "")
		assertStatus(t, status, http.StatusOK, body)
		rows := decodeRows(t, body)
		want := append([]string{missionHaulerID, missionPodID, missionTowID, missionQuarantineID}, missionIDs(12, 16, 20, 24, 28, 32)...)
		slices.Sort(want)
		if got := idsOf(t, rows); !slices.Equal(got, want) {
			t.Errorf("rows = %v, want Halvard's %v", got, want)
		}
		for _, row := range rows {
			for _, hidden := range []string{"assignedSquadronId", "notes", "settlement", "bookedByUserId"} {
				if _, ok := row[hidden]; ok {
					t.Errorf("portal row %v carries %s, outside the portal grant's width", row["id"], hidden)
				}
			}
			for _, shown := range []string{"title", "kindId", "statusId", "hazard", "fee", "deadline"} {
				if _, ok := row[shown]; !ok {
					t.Errorf("portal row %v lacks %s", row["id"], shown)
				}
			}
		}
	})

	t.Run("cleo pages her tracker from the descriptor's default", func(t *testing.T) {
		t.Parallel()

		rr := doRequestRecordedAs(t, h, clientUser, http.MethodGet, portalPath(anvil, "missions?limit=4"), "")
		assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())
		if rows := decodeRows(t, rr.Body.Bytes()); len(rows) != 4 {
			t.Errorf("rows = %d, want 4", len(rows))
		}
		if next := linkRelations(t, rr.Header().Get("Link"))["next"]; next == "" || next[:len(portalAPI)] != portalAPI {
			t.Errorf("next = %q, want a portal-prefixed page", next)
		}
	})

	t.Run("cleo reads back the calls she filed and nobody else's", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, clientUser, http.MethodGet, portalPath(anvil, "distress-calls?limit=100"), "")
		assertStatus(t, status, http.StatusOK, body)
		want := []string{callBeaconID, "d0000000-0000-4000-8000-000000000004", "d0000000-0000-4000-8000-000000000007", "d0000000-0000-4000-8000-000000000010", "d0000000-0000-4000-8000-000000000013"}
		if got := idsOf(t, decodeRows(t, body)); !slices.Equal(got, want) {
			t.Errorf("calls = %v, want only the calls Cleo filed %v", got, want)
		}
	})

	t.Run("non-members 404 under the portal prefix", func(t *testing.T) {
		t.Parallel()

		for _, target := range []string{
			portalPath(anvil, "refits"),
			portalPath(anvil, "ships"),
			portalPath(anvil, "squadrons"),
			portalPath(bastion, "sorties"),
			portalAPI + "/pilots",
		} {
			status, body := doRequestAs(t, h, clientUser, http.MethodGet, target, "")
			if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
				t.Errorf("%s: status = %d, want 404/405: %s", target, status, body)
			}
		}
	})

	t.Run("the portal digest names the portal's members", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, clientUser, http.MethodGet, portalAPI+"/permission-digest?domain="+anvil, "")
		assertStatus(t, status, http.StatusOK, body)
		var digest accesstypes.PermissionDigest
		if err := json.Unmarshal(body, &digest); err != nil {
			t.Fatalf("decoding digest: %v", err)
		}
		if got := digest["Missions"]["List"]; got != accesstypes.DigestConditional {
			t.Errorf("digest[Missions][List] = %q, want conditional (client = subject.client)", got)
		}
		if got := digest["DistressCalls"]["Create"]; got != accesstypes.DigestGranted {
			t.Errorf("digest[DistressCalls][Create] = %q, want granted", got)
		}
		if got := digest["StandDownMission"]["Execute"]; got != accesstypes.DigestConditional {
			t.Errorf("digest[StandDownMission][Execute] = %q, want conditional", got)
		}
		for _, absent := range []accesstypes.Resource{"Refits", "Ships", "Squadrons", "Sorties", "ClaimMission"} {
			if _, ok := digest[absent]; ok {
				t.Errorf("digest carries %s; the portal role holds nothing on it", absent)
			}
		}

		status, body = doRequestAs(t, h, clientUser, http.MethodGet, portalAPI+"/permission-digest", "")
		assertStatus(t, status, http.StatusOK, body)
		if err := json.Unmarshal(body, &digest); err != nil {
			t.Fatalf("decoding global digest: %v", err)
		}
		if got := digest["ClientContacts"]["Read"]; got != accesstypes.DigestConditional {
			t.Errorf("digest[ClientContacts][Read] = %q, want conditional (userId = subject)", got)
		}
	})

	t.Run("the portal's user-domains lists where the directory placed the portal role", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, clientUser, http.MethodGet, portalAPI+"/user-domains", "")
		assertStatus(t, status, http.StatusOK, body)
		var domains []accesstypes.Domain
		if err := unmarshalDomains(body, &domains); err != nil {
			t.Fatalf("decoding domains: %v", err)
		}
		if !slices.Equal(domains, sectors) {
			t.Errorf("domains = %v, want %v (the membership is held in every sector)", domains, sectors)
		}
	})

	t.Run("cleo reads her own contact record and no other", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, clientUser, http.MethodGet, portalAPI+"/client-contacts", "")
		assertStatus(t, status, http.StatusOK, body)
		rows := decodeRows(t, body)
		if len(rows) != 1 || rows[0]["displayName"] != "Client Cleo" {
			t.Errorf("contacts = %v, want Cleo's own record", rows)
		}
	})

	t.Run("cleo reads Halvard's mission and not Meridian's", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, clientUser, http.MethodGet, portalPath(anvil, "missions/"+missionHaulerID), "")
		assertStatus(t, status, http.StatusOK, body)
		status, body = doRequestAs(t, h, clientUser, http.MethodGet, portalPath(anvil, "missions/"+missionConvoyID), "")
		assertStatus(t, status, http.StatusNotFound, body)
	})

	t.Run("cleo reads the call she filed and not the cadet's", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, clientUser, http.MethodGet, portalPath(anvil, "distress-calls/"+callBeaconID), "")
		assertStatus(t, status, http.StatusOK, body)
		status, body = doRequestAs(t, h, clientUser, http.MethodGet, portalPath(anvil, "distress-calls/"+callDebrisID), "")
		assertStatus(t, status, http.StatusNotFound, body)
	})

	t.Run("cleo reads her own contact record and not the surveyor's", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, clientUser, http.MethodGet, portalAPI+"/client-contacts/"+contactCleoID, "")
		assertStatus(t, status, http.StatusOK, body)
		status, body = doRequestAs(t, h, clientUser, http.MethodGet, portalAPI+"/client-contacts/"+contactSurveyorID, "")
		assertStatus(t, status, http.StatusNotFound, body)
	})
}

// TestClientPortalActions is the mutating half: filing a call, and standing down, which
// the mission's history then holds.
//
// Demonstrates: pii, @transition.multi-from, execute-condition.
func TestClientPortalActions(t *testing.T) {
	t.Parallel()

	_, _, h := demoWorld(t)

	// The grant this suite proves, pinned to the roles file (conditions-proven).
	provesGrant(t, members.Roles(), "ClientPortal", "Execute", "StandDownMission", "client = subject.client")

	t.Run("cleo files a call with her contact: the one PII field a client writes", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, clientUser, http.MethodPatch, portalAPI+"/resources",
			fmt.Sprintf(`[{"op":"add","path":%q,"value":{"summary":"Hauler drifting off the lane","severity":4,"callerContact":"cleo@halvard.example"}}]`, opPath(anvil, "distress-calls")))
		assertStatus(t, status, http.StatusOK, body)
		ids, _ := decodeRow(t, body)["distressCalls"].([]any)
		if len(ids) != 1 {
			t.Fatalf("created ids = %v, want one call id: %s", ids, body)
		}
		status, body = doRequestAs(t, h, clientUser, http.MethodGet, portalPath(anvil, fmt.Sprintf("distress-calls/%s", ids[0])), "")
		assertStatus(t, status, http.StatusOK, body)
		row := decodeRow(t, body)
		if row["callerContact"] != "cleo@halvard.example" {
			t.Errorf("callerContact = %v, want the contact Cleo wrote", row["callerContact"])
		}
		if cn, _ := row["caseNumber"].(string); len(cn) < 4 || cn[:3] != "DC-" {
			t.Errorf("caseNumber = %v, want a server-issued DC- number", row["caseNumber"])
		}
	})

	t.Run("stand down fires on an own-company open mission, refuses another company's, and lands in the mission's history", func(t *testing.T) {
		t.Parallel()

		status, body := doRequestAs(t, h, clientUser, http.MethodPost, portalPath(anvil, "stand-down-mission"),
			fmt.Sprintf(`{"missionId":%q}`, missionConvoyID)) // Meridian's
		assertStatus(t, status, http.StatusForbidden, body)
		status, body = doRequestAs(t, h, clientUser, http.MethodPost, portalPath(anvil, "stand-down-mission"),
			fmt.Sprintf(`{"missionId":%q}`, missionHaulerID)) // Halvard's, open
		assertStatus(t, status, http.StatusOK, body)

		status, body = doRequestAs(t, h, clientUser, http.MethodGet, portalPath(anvil, "missions/"+missionHaulerID+"/changes"), "")
		assertStatus(t, status, http.StatusOK, body)
		var history struct {
			Changes []struct {
				Fields []struct {
					Field string          `json:"field"`
					New   json.RawMessage `json:"new"`
				} `json:"fields"`
			} `json:"changes"`
		}
		if err := json.Unmarshal(body, &history); err != nil {
			t.Fatalf("decoding the hauler's history: %v: %s", err, body)
		}
		var stoodDown bool
		if len(history.Changes) > 0 {
			for _, field := range history.Changes[0].Fields {
				if field.Field == "statusId" && string(field.New) == `"stood_down"` {
					stoodDown = true
				}
			}
		}
		if !stoodDown {
			t.Errorf("the hauler's history = %s, want the stand-down newest", body)
		}
		// Meridian's convoy is not Cleo's company's: its history is not hers to read.
		status, body = doRequestAs(t, h, clientUser, http.MethodGet, portalPath(anvil, "missions/"+missionConvoyID+"/changes"), "")
		assertStatus(t, status, http.StatusNotFound, body)
	})
}
