package integration

// This suite pins the live list across tenants over the demo world through an in-memory
// live service (live.Fake) and the real permission engines. The list across sectors is
// plain unless the page asks: asked, the liaison's one request registers one subscription
// over the sectors her list reads, Anvil and Cinder, and none of Bastion, where she holds
// no role. After a write the publisher asks what it always asked, who is subscribed to
// this list in this sector, and is answered from the sets: a mission changed in Anvil or
// in Cinder reaches her change set as that sector's list, a mission changed in Bastion
// never does. The renewal works her set out again, keeps no subscription over a set of
// sectors for a resource that is not declared for it, and drops the subscription of a
// person who may list missions in no sector.
//
// Demonstrates: list.across-tenants.live.

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/resource/live"
)

const liveTabLiaison = "tab-liaison-1"

func TestAcrossTenants_live(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedCastAccounts(ctx, db); err != nil {
		t.Fatal(err)
	}
	sharedWorld(t)
	fake := live.NewFake()
	sharedMu.RLock()
	h := withHandWrittenRoutes(app.New(&testConfigurer{db: db, access: sharedCrew, membersAccess: sharedMembers, live: fake}))
	sharedMu.RUnlock()

	// write changes a mission's notes as the governor, who is marshal of every sector.
	write := func(sector, mission, notes string) {
		t.Helper()

		status, body := doRequestAs(t, h, "governor", http.MethodPatch, "/console/api/resources",
			fmt.Sprintf(`[{"op":"patch","path":%q,"value":{"notes":%q}}]`, opPath(sector, "missions/"+mission), notes))
		assertStatus(t, status, http.StatusOK, body)
	}
	liaisonChanges := func() []string {
		return changeNames(fake.Changes(principalID("liaison")))
	}

	// Deliberately not a table: each step reads the record the previous one left.

	// Plain unless the page asks: a request without the subscribe header registers
	// nothing, and a write in a sector she lists reaches nobody.
	status, body := doRequestAs(t, h, "liaison", http.MethodGet, missionsAcross, "")
	assertStatus(t, status, http.StatusOK, body)
	write(anvil, missionHaulerID, "plain page open")
	if subs := fake.Subscriptions(); len(subs) != 0 {
		t.Fatalf("subscriptions after a plain request = %v, want none", subs)
	}
	if got := liaisonChanges(); len(got) != 0 {
		t.Fatalf("her change set after a write while her page was plain = %v, want empty", got)
	}

	// Asked for live: one subscription over the sectors her list reads, beside it the
	// list of sectors its sector names are read from, and the answer to a versioned
	// request is the browser's to keep for the window.
	rr := doRequestRecordedWithHeaders(t, h, "liaison", http.MethodGet, versioned(missionsAcross, liveSeed), "", liveHeaders(liveTabLiaison))
	assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())
	if got := rr.Header().Get("Cache-Control"); got != live.CacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, live.CacheControl)
	}
	if rows := decodeRows(t, rr.Body.Bytes()); len(rows) != 15 {
		t.Errorf("rows = %d, want her fifteen", len(rows))
	}
	everySector := acrossName(principalID("liaison"), liveTabLiaison, nil, anvil, cinder)
	sectorNames := fmt.Sprintf("%s|%s|Sectors||", principalID("liaison"), liveTabLiaison)
	if got, want := acrossSubscriptionNames(fake.Subscriptions()), []string{everySector, sectorNames}; !slices.Equal(got, want) {
		t.Fatalf("the record after her live request = %v, want %v", got, want)
	}

	// A mission changes in each sector. Hers holds the two she lists, each as that
	// sector's list, and nothing of Bastion.
	write(anvil, missionHaulerID, "hull breach confirmed")
	write(bastion, missionBeaconID, "mast crew accounted for")
	write(cinder, missionSweepID, "ledger closed")
	want := []string{
		"list|Missions||" + anvil + "|false",
		"list|Missions||" + cinder + "|false",
	}
	if got := liaisonChanges(); !slices.Equal(got, want) {
		t.Errorf("her change set = %v, want %v", got, want)
	}

	// The renewal carries the list as one subscription and works its set out again; a
	// narrowed page is another list of the same tab, over its own set.
	renewal := fmt.Sprintf(`{"tab":%q,"subscriptions":[{"resource":"Missions","acrossTenants":true},{"resource":"Missions","acrossTenants":true,"domains":%q}]}`, liveTabLiaison, bastion+","+cinder)
	rr = doRequestRecordedWithHeaders(t, h, "liaison", http.MethodPost, consoleAPI+"/"+live.RenewRoute, renewal, liveHeaders(liveTabLiaison))
	assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())
	want = []string{everySector, acrossName(principalID("liaison"), liveTabLiaison, []accesstypes.Domain{bastion, cinder}, cinder), sectorNames}
	slices.Sort(want)
	if got := acrossSubscriptionNames(fake.Subscriptions()); !slices.Equal(got, want) {
		t.Errorf("the record after her renewal = %v, want %v", got, want)
	}

	// Squadrons are not declared for listing across sectors, though she may list them in
	// Anvil: a renewal that asks for them as one subscription over her sectors is not
	// kept, and the record holds what it held.
	undeclared := fmt.Sprintf(`{"tab":%q,"subscriptions":[{"resource":"Squadrons","acrossTenants":true},{"resource":"Missions","acrossTenants":true}]}`, liveTabLiaison)
	rr = doRequestRecordedWithHeaders(t, h, "liaison", http.MethodPost, consoleAPI+"/"+live.RenewRoute, undeclared, liveHeaders(liveTabLiaison))
	assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())
	if got := rr.Body.String(); !containsAll(got, `"dropped":[{"resource":"Squadrons","acrossTenants":true}]`, `"kept":[{"resource":"Missions","acrossTenants":true}]`) {
		t.Errorf("the renewal answered %s, want the squadrons dropped and the missions kept", got)
	}
	if got := acrossSubscriptionNames(fake.Subscriptions()); !slices.Equal(got, want) {
		t.Errorf("the record after a renewal naming an undeclared resource = %v, want %v", got, want)
	}

	// The yeoman may list missions in no sector: her live request registers nothing, and
	// a renewal of the list drops it.
	rr = doRequestRecordedWithHeaders(t, h, "yeoman", http.MethodGet, versioned(missionsAcross, liveSeed), "", liveHeaders("tab-yeoman-1"))
	assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())
	rr = doRequestRecordedWithHeaders(t, h, "yeoman", http.MethodPost, consoleAPI+"/"+live.RenewRoute, `{"tab":"tab-yeoman-1","subscriptions":[{"resource":"Missions","acrossTenants":true}]}`, liveHeaders("tab-yeoman-1"))
	assertStatus(t, rr.Code, http.StatusOK, rr.Body.Bytes())
	for _, sub := range fake.Subscriptions() {
		if sub.Principal == principalID("yeoman") {
			t.Errorf("the record holds a subscription of the yeoman, who may list missions in no sector: %v", sub)
		}
	}
	if got := rr.Body.String(); !containsAll(got, `"dropped":[{"resource":"Missions","acrossTenants":true}]`, `"kept":[]`) {
		t.Errorf("the yeoman's renewal answered %s, want the list dropped and nothing kept", got)
	}
}

// acrossName spells the subscription of a list of missions across sectors: who holds it,
// the tab, the page's filter on the sector (nil for every sector) and the set it covers.
func acrossName(principal, tab string, narrowed []accesstypes.Domain, set ...accesstypes.Domain) string {
	filter := "every sector"
	if narrowed != nil {
		filter = fmt.Sprintf("only %v", narrowed)
	}

	return fmt.Sprintf("%s|%s|Missions|%s|over %v", principal, tab, filter, set)
}

// acrossSubscriptionNames spells the record's subscriptions, sorted: a list across
// sectors by acrossName, and any other as subscriptionNames spells it.
func acrossSubscriptionNames(subs []live.Subscription) []string {
	names := make([]string, 0, len(subs))
	for _, sub := range subs {
		if sub.Across == nil {
			names = append(names, subscriptionNames([]live.Subscription{sub})...)

			continue
		}
		names = append(names, acrossName(sub.Principal, sub.Tab, sub.Across.Narrowed, sub.Across.Domains...))
	}
	slices.Sort(names)

	return names
}

// containsAll reports whether s holds every part.
func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}

	return true
}
