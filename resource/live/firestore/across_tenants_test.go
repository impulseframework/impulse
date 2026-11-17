package firestore_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/resource/live"
	livefirestore "github.com/impulseframework/impulse/resource/live/firestore"
)

// One subscription over a set of tenants, in the store: a list across tenants is one
// document whose set of tenants is the array the lookup asks with array-contains, so the
// publisher's question, who is subscribed to this list in this tenant, finds it from
// every tenant of its set and from no other, with the expiry filter in the same query.

// acrossSub is a person's list across tenants of the test's resource over the set.
func acrossSub(t *testing.T, res accesstypes.Resource, who, tab string, set, narrowed []accesstypes.Domain) live.Subscription {
	t.Helper()

	sub := *live.AcrossTenantsSubscription(res, set, narrowed)
	sub.Principal, sub.Tab = principalFor(t, who), tab

	return sub
}

// acrossLookups runs the set lookups against a service: the cases a store must answer
// for the subscription over a set of tenants to stand. The emulator runs them with no
// index; a real database answers them only with the indexes of firestore.indexes.json.
func acrossLookups(t *testing.T, svc *livefirestore.Service, now time.Time) {
	t.Helper()

	res := resourceFor(t)
	inTenant := func(who, tab string, domain accesstypes.Domain) live.Subscription {
		return live.Subscription{Principal: principalFor(t, who), Tab: tab, Resource: res, Domain: domain}
	}
	expired := acrossSub(t, res, "late", "t5", []accesstypes.Domain{"anvil", "cinder"}, nil)
	expired.Expiry = now.Add(-time.Second)
	register(t, svc, now,
		acrossSub(t, res, "liaison", "t1", []accesstypes.Domain{"cinder", "anvil"}, nil),
		acrossSub(t, res, "auditor", "t2", []accesstypes.Domain{"bastion", "cinder", "dawn"}, nil),
		inTenant("marshal", "t3", "anvil"),
		inTenant("bosun", "t4", "bastion"),
		expired,
		live.Subscription{Principal: principalFor(t, "engineer"), Tab: "t6", Resource: res, Key: "h1", Domain: "anvil"},
	)
	sortByPrincipal := cmpopts.SortSlices(func(a, b live.Subscription) bool {
		if a.Principal != b.Principal {
			return a.Principal < b.Principal
		}

		return a.Domain < b.Domain
	})

	lists := []struct {
		domain accesstypes.Domain
		want   []live.Subscription
	}{
		// Each list across tenants is found from every tenant of its set, as that
		// tenant's list, beside the tenant's own list; the expired one from none.
		{domain: "anvil", want: []live.Subscription{inTenant("liaison", "t1", "anvil"), inTenant("marshal", "t3", "anvil")}},
		{domain: "bastion", want: []live.Subscription{inTenant("auditor", "t2", "bastion"), inTenant("bosun", "t4", "bastion")}},
		{domain: "cinder", want: []live.Subscription{inTenant("auditor", "t2", "cinder"), inTenant("liaison", "t1", "cinder")}},
		{domain: "dawn", want: []live.Subscription{inTenant("auditor", "t2", "dawn")}},
		// A tenant in nobody's set, and the empty domain, where only the row is kept.
		{domain: "ember", want: []live.Subscription{}},
		{domain: "", want: []live.Subscription{}},
	}
	for _, tt := range lists {
		got, err := svc.SubscribersOfList(t.Context(), res, tt.domain)
		if err != nil {
			t.Fatalf("SubscribersOfList(%q) error = %v", tt.domain, err)
		}
		if diff := cmp.Diff(tt.want, got, ignoreExpiry, sortByPrincipal, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("SubscribersOfList(%q) mismatch (-want +got):\n%s", tt.domain, diff)
		}
	}

	// The bulk path's one lookup answers each list once for each tenant of its set.
	whole, err := svc.SubscribersOfResource(t.Context(), res)
	if err != nil {
		t.Fatalf("SubscribersOfResource() error = %v", err)
	}
	wantWhole := []live.Subscription{
		inTenant("auditor", "t2", "bastion"), inTenant("auditor", "t2", "cinder"), inTenant("auditor", "t2", "dawn"),
		inTenant("bosun", "t4", "bastion"),
		{Principal: principalFor(t, "engineer"), Tab: "t6", Resource: res, Key: "h1"},
		inTenant("liaison", "t1", "anvil"), inTenant("liaison", "t1", "cinder"),
		inTenant("marshal", "t3", "anvil"),
	}
	if diff := cmp.Diff(wantWhole, whole, ignoreExpiry, sortByPrincipal); diff != "" {
		t.Errorf("SubscribersOfResource() mismatch (-want +got):\n%s", diff)
	}

	// The row is found as it always was.
	rows, err := svc.SubscribersOfRow(t.Context(), res, "h1")
	if err != nil {
		t.Fatalf("SubscribersOfRow() error = %v", err)
	}
	if diff := cmp.Diff([]live.Subscription{{Principal: principalFor(t, "engineer"), Tab: "t6", Resource: res, Key: "h1"}}, rows, ignoreExpiry); diff != "" {
		t.Errorf("SubscribersOfRow() mismatch (-want +got):\n%s", diff)
	}

	// A renewal that came to another set is the same document: Anvil is out of the
	// liaison's set and Dawn is in it, from the next write on.
	register(t, svc, now, acrossSub(t, res, "liaison", "t1", []accesstypes.Domain{"cinder", "dawn"}, nil))
	stored, err := livefirestore.StoredSubscriptions(t.Context(), svc, principalFor(t, "liaison"))
	if err != nil {
		t.Fatalf("StoredSubscriptions() error = %v", err)
	}
	if stored != 1 {
		t.Errorf("the record holds %d documents for the liaison after the renewal, want 1: one per tab and list, however many tenants", stored)
	}
	for domain, want := range map[accesstypes.Domain]bool{"anvil": false, "cinder": true, "dawn": true} {
		got, err := svc.SubscribersOfList(t.Context(), res, domain)
		if err != nil {
			t.Fatalf("SubscribersOfList(%q) error = %v", domain, err)
		}
		found := false
		for _, sub := range got {
			found = found || sub.Principal == principalFor(t, "liaison")
		}
		if found != want {
			t.Errorf("after the renewal, the liaison's list is found in %s = %v, want %v", domain, found, want)
		}
	}

	// A page narrowed to tenants it names is another list of the same tab: a second
	// document, found in its own set alone.
	register(t, svc, now, acrossSub(t, res, "liaison", "t1", []accesstypes.Domain{"cinder"}, []accesstypes.Domain{"cinder", "bastion"}))
	stored, err = livefirestore.StoredSubscriptions(t.Context(), svc, principalFor(t, "liaison"))
	if err != nil {
		t.Fatalf("StoredSubscriptions() error = %v", err)
	}
	if stored != 2 {
		t.Errorf("the record holds %d documents for the liaison with a narrowed page open, want 2", stored)
	}
}

func TestService_acrossTenants_lookups(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	acrossLookups(t, newService(t, fixedClock(now)), now)
}

// TestService_acrossTenants_publish pins the change sets one write makes when a list
// across tenants is among the subscribers: the person hears a write in a tenant of
// their set once, as that tenant's list, and nothing of a write in any other tenant.
func TestService_acrossTenants_publish(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	svc := newService(t, fixedClock(now))
	res := resourceFor(t)
	register(t, svc, now,
		acrossSub(t, res, "liaison", "t1", []accesstypes.Domain{"anvil", "cinder"}, nil),
		live.Subscription{Principal: principalFor(t, "marshal"), Tab: "t2", Resource: res, Domain: "anvil"},
		live.Subscription{Principal: principalFor(t, "bosun"), Tab: "t3", Resource: res, Domain: "bastion"},
	)
	write := func(domain accesstypes.Domain, id string) live.Changes {
		t.Helper()

		changes, err := svc.Publish(t.Context(), domain, live.Writes{ID: id, Rows: map[accesstypes.Resource][]resource.RowChange{res: {{Key: "h1"}}}})
		if err != nil {
			t.Fatalf("Publish(%s) error = %v", domain, err)
		}

		return changes
	}
	list := func(domain accesstypes.Domain) []live.ChangeDocument {
		return []live.ChangeDocument{{Kind: live.ListChange, Resource: res, Domain: domain}}
	}

	if diff := cmp.Diff(live.Changes{principalFor(t, "liaison"): list("anvil"), principalFor(t, "marshal"): list("anvil")}, write("anvil", "write-1")); diff != "" {
		t.Errorf("a write in Anvil: changes mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(live.Changes{principalFor(t, "bosun"): list("bastion")}, write("bastion", "write-2")); diff != "" {
		t.Errorf("a write in Bastion, outside the liaison's set: changes mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(live.Changes{principalFor(t, "liaison"): list("cinder")}, write("cinder", "write-3")); diff != "" {
		t.Errorf("a write in Cinder: changes mismatch (-want +got):\n%s", diff)
	}

	// Her change set holds the two lists that changed, and nothing of Bastion.
	got, err := svc.Changes(t.Context(), principalFor(t, "liaison"), time.Time{})
	if err != nil {
		t.Fatalf("Changes() error = %v", err)
	}
	domains := make([]accesstypes.Domain, 0, len(got))
	for _, change := range got {
		domains = append(domains, change.Domain)
	}
	if diff := cmp.Diff([]accesstypes.Domain{"anvil", "cinder"}, domains, cmpopts.SortSlices(func(a, b accesstypes.Domain) bool { return a < b })); diff != "" {
		t.Errorf("the liaison's change set names (-want +got):\n%s", diff)
	}
}

// The environment a run against a real Firestore database reads: the project, and the
// database in it, which must carry the indexes of firestore.indexes.json. The run
// authenticates with the application default credentials.
const (
	realProjectEnv  = "LIVE_FIRESTORE_TEST_PROJECT"
	realDatabaseEnv = "LIVE_FIRESTORE_TEST_DATABASE"
)

// TestService_acrossTenants_lookups_realDatabase runs the set lookups against a real
// Firestore database, where a query that names no index it can use is refused: it is
// the proof that the (resource, domains array-contains, expiry) index serves the
// lookup. It is skipped unless the environment names the project.
func TestService_acrossTenants_lookups_realDatabase(t *testing.T) {
	project := os.Getenv(realProjectEnv)
	if project == "" {
		t.Skipf("set %s (and %s, for a database other than the default) to run the set lookups against a real Firestore database", realProjectEnv, realDatabaseEnv)
	}
	// The wall clock: a real database stamps nothing with the test's own.
	svc, err := livefirestore.New(t.Context(), livefirestore.Config{ProjectID: project, DatabaseID: os.Getenv(realDatabaseEnv)})
	if err != nil {
		t.Fatalf("firestore.New() error = %v", err)
	}
	t.Cleanup(func() {
		// The test's context is canceled before its cleanups run.
		ctx := context.WithoutCancel(t.Context())
		for _, who := range []string{"liaison", "auditor", "marshal", "bosun", "late", "engineer"} {
			if err := svc.UnsubscribeAll(ctx, principalFor(t, who)); err != nil {
				t.Errorf("UnsubscribeAll(%s) error = %v", who, err)
			}
		}
		if err := svc.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	acrossLookups(t, svc, time.Now())
}
