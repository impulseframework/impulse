package integration

// This suite pins the reach-time admission on the served surface: a sector is in a
// session's chart (user-domains) only when the session holds a grant there and the sector's
// declaration admits the way the session was established, and a direct request for a
// sector the session may not enter answers as a sector that does not exist, the
// concealed-sector guard's own refusal. A sector's declaration restricts its own door
// alone. The harness scripts the declarations: Bastion admits its people through its
// directory only, Cinder has a directory and is not directory only, Anvil declares
// nothing. The harness's sessions record no sign-in, which a directory-only sector
// refuses and every other sector admits.
//
// The tallyman is a crew member this suite provisions for itself, SectorCrew in Anvil and
// Bastion and nothing in the global partition. He charts Anvil alone and Bastion is not
// there for him: it stays on his chart as not available, with the reason, since he holds a
// seat in it. A role in the global partition exempts nobody: the pilot, who holds
// CrewCommon there and a role at Bastion, charts Anvil alone too. Cinder's directory
// closes it to nobody: the hazard analyst charts it. One person is exempt from a
// sector's declaration, a super administrator: the governor, who holds the role the
// framework defines in every auth, charts every sector and enters Bastion.

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// tallyman is the suite's own crew member: held in two sectors, with no global role.
const tallyman accesstypes.User = "tallyman"

func TestTenantAdmission_sectorDeclarationsDecideTheChart(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	crewEngine := newAccessClient(t, db, crew.Roles())
	membersEngine, err := openEngine(db, members.TablePrefix, members.Roles())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := membersEngine.Close(); err != nil {
			t.Errorf("access.Client.Close() error = %v", err)
		}
	})
	if err := provisionDemoAccess(ctx, crewEngine, membersEngine); err != nil {
		t.Fatal(err)
	}
	if err := assignDemoRoles(ctx, crewEngine, tallyman, demoRoles{Domains: map[string][]accesstypes.Role{"Anvil": {"SectorCrew"}, "Bastion": {"SectorCrew"}}}); err != nil {
		t.Fatal(err)
	}
	if err := waitForDecision(ctx, crewEngine, tallyman, bastion, accesstypes.List, "Wings"); err != nil {
		t.Fatal(err)
	}

	declarations := map[accesstypes.Domain]*signin.TenantDeclaration{
		bastion: {Directories: []signin.Directory{{Method: sessioninfo.MethodGoogle, Connection: "relay-workspace"}}, DirectoryOnly: true},
		cinder:  {Directories: []signin.Directory{{Method: sessioninfo.MethodGoogle, Connection: "frontier-workspace"}}},
	}
	h := withHandWrittenRoutes(newAppWithDeclarations(db, crewEngine, membersEngine, func(_ context.Context, sector accesstypes.Domain) (*signin.TenantDeclaration, error) {
		return declarations[sector], nil
	}))
	bastionDigest := consoleAPI + "/permission-digest?domain=" + bastion
	ghostWing := `[{"op":"add","path":"` + opPath(bastion, "wings") + `","value":{"name":"Ghost Wing"}}]`

	tests := []struct {
		name       string
		user       accesstypes.User
		method     string
		target     string
		body       string
		wantStatus int
		wantChart  []accesstypes.Domain
		// wantUnavailable is the sectors the chart lists as not available to the session,
		// each with what its reason says; checked wherever wantChart is.
		wantUnavailable map[string]string
	}{
		{
			name: "the tallyman's chart lights Anvil alone, and lists Bastion as not available, with the reason: it admits its people through its directory only", user: tallyman, method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK,
			wantChart:       []accesstypes.Domain{anvil},
			wantUnavailable: map[string]string{bastion: "Bastion admits only people who sign in through its own directory. This session was not signed in through it."},
		},
		{name: "Anvil's wings answer the tallyman", user: tallyman, method: http.MethodGet, target: sectorPath(anvil, "wings"), wantStatus: http.StatusOK},
		{name: "Bastion is not there for the tallyman: the guard refuses a sector the session may not enter as one that does not exist", user: tallyman, method: http.MethodGet, target: sectorPath(bastion, "wings"), wantStatus: http.StatusNotFound},
		{name: "nor through the consolidated patch, whose descent answers as it does an unknown sector", user: tallyman, method: http.MethodPatch, target: resourcesRoute, body: ghostWing, wantStatus: http.StatusBadRequest},
		{name: "Bastion's digest is empty for the tallyman", user: tallyman, method: http.MethodGet, target: bastionDigest, wantStatus: http.StatusOK},
		{name: "the hazard analyst's chart lights Anvil and Cinder: Cinder's directory closes it to nobody", user: "hazards", method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, wantChart: []accesstypes.Domain{anvil, cinder}},
		{
			name: "the pilot holds CrewCommon in the global partition, which exempts nobody, so Bastion is closed to him too", user: "pilot", method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK,
			wantChart:       []accesstypes.Domain{anvil},
			wantUnavailable: map[string]string{bastion: "Bastion admits only people who sign in through its own directory. This session was not signed in through it."},
		},
		{name: "and Bastion is not there for the pilot", user: "pilot", method: http.MethodGet, target: sectorPath(bastion, "wings"), wantStatus: http.StatusNotFound},
		{name: "the governor, a super administrator, charts every sector", user: "governor", method: http.MethodGet, target: userDomainsRoute, wantStatus: http.StatusOK, wantChart: []accesstypes.Domain{anvil, bastion, cinder}},
		{name: "and enters Bastion", user: "governor", method: http.MethodGet, target: sectorPath(bastion, "wings"), wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, h, tt.user, tt.method, tt.target, tt.body)
			assertStatus(t, status, tt.wantStatus, body)
			if tt.wantChart != nil {
				var got []accesstypes.Domain
				if err := unmarshalDomains(body, &got); err != nil {
					t.Fatalf("decoding the chart %s: %v", body, err)
				}
				if !slices.Equal(got, tt.wantChart) {
					t.Errorf("chart = %v, want %v", got, tt.wantChart)
				}
				unavailable, err := unavailableDomains(body)
				if err != nil {
					t.Fatalf("decoding the chart %s: %v", body, err)
				}
				want := tt.wantUnavailable
				if want == nil {
					want = map[string]string{}
				}
				if !maps.Equal(unavailable, want) {
					t.Errorf("the sectors the chart lists as not available = %v, want %v", unavailable, want)
				}
			}
			if tt.target == bastionDigest {
				var digest accesstypes.PermissionDigest
				if err := json.Unmarshal(body, &digest); err != nil {
					t.Fatalf("decoding the digest %s: %v", body, err)
				}
				if len(digest) != 0 {
					t.Errorf("Bastion's digest for the tallyman = %v, want empty", digest)
				}
			}
		})
	}
}
