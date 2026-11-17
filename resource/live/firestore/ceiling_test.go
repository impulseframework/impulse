package firestore_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource/live"
	livefirestore "github.com/impulseframework/impulse/resource/live/firestore"
)

// tenantKeys is n tenants' keys, each as long as the id a tenant is keyed by, so a
// document over them is the size a real one is.
func tenantKeys(n int) []accesstypes.Domain {
	keys := make([]accesstypes.Domain, 0, n)
	for i := range n {
		keys = append(keys, accesstypes.Domain(fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)))
	}

	return keys
}

// TestService_ceiling pins that the record holds a subscription over as many tenants as
// one subscription may hold (live.MaxSubscriptionTenants): one document, written, found
// by the publisher's lookup from a tenant of its set as that tenant's list, found from
// no tenant outside it, and written again by a renewal. The emulator keeps no index, so
// this is the proof of the document and the lookup; the count of index entries a real
// database allows is the arithmetic the constant's comment gives.
func TestService_ceiling(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	svc := newService(t, fixedClock(now))
	res := resourceFor(t)
	set := tenantKeys(live.MaxSubscriptionTenants)
	register(t, svc, now, acrossSub(t, res, "liaison", "t1", set, nil))

	for _, domain := range []accesstypes.Domain{set[0], set[len(set)/2], set[len(set)-1]} {
		got, err := svc.SubscribersOfList(t.Context(), res, domain)
		if err != nil {
			t.Fatalf("SubscribersOfList(%s) error = %v", domain, err)
		}
		want := []live.Subscription{{Principal: principalFor(t, "liaison"), Tab: "t1", Resource: res, Domain: domain}}
		if diff := cmp.Diff(want, got, ignoreExpiry); diff != "" {
			t.Errorf("SubscribersOfList(%s) mismatch (-want +got):\n%s", domain, diff)
		}
	}
	outside, err := svc.SubscribersOfList(t.Context(), res, "99999999-0000-4000-8000-000000000000")
	if err != nil {
		t.Fatalf("SubscribersOfList() error = %v", err)
	}
	if len(outside) != 0 {
		t.Errorf("a tenant outside the set finds %d subscriptions, want none", len(outside))
	}

	renewed := acrossSub(t, res, "liaison", "t1", set, nil)
	renewed.Expiry = now.Add(live.SubscriptionTTL + live.RenewInterval)
	if refused, err := svc.Renew(t.Context(), []live.Subscription{renewed}); err != nil || len(refused) != 0 {
		t.Fatalf("Renew() refused %d subscriptions, error = %v, want neither", len(refused), err)
	}
	stored, err := livefirestore.StoredSubscriptions(t.Context(), svc, principalFor(t, "liaison"))
	if err != nil {
		t.Fatalf("StoredSubscriptions() error = %v", err)
	}
	if stored != 1 {
		t.Errorf("the record holds %d documents for the liaison, want the one", stored)
	}
}

// TestService_Renew_refusedDocument pins that a document the store refuses stops none
// of the others in its batch: the refused one is answered to the renewal with the
// store's reason, and the rest are written with their fresh expiry. The refused
// document here carries a value over the size the store allows, and the emulator
// answers the whole request it travels in as invalid, the list sent beside it
// included: the list is written all the same, alone, and the refusal is the one
// document's.
func TestService_Renew_refusedDocument(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	svc := newService(t, fixedClock(now))
	res := resourceFor(t)
	who := principalFor(t, "marshal")
	expiry := now.Add(live.SubscriptionTTL)
	list := live.Subscription{Principal: who, Tab: "t1", Resource: res, Domain: "anvil", Expiry: expiry}
	oversized := live.Subscription{Principal: who, Tab: "t1", Resource: res, Key: strings.Repeat("k", 1<<20+1024), Expiry: expiry}
	row := live.Subscription{Principal: who, Tab: "t1", Resource: res, Key: "k1", Expiry: expiry}

	refused, err := svc.Renew(t.Context(), []live.Subscription{list, oversized, row})
	if err != nil {
		t.Fatalf("Renew() error = %v, want the batch answered document by document", err)
	}
	for i := range refused {
		t.Logf("refused: domain %q, a key of %d bytes: %.300s", refused[i].Subscription.Domain, len(refused[i].Subscription.Key), refused[i].Err)
	}
	if len(refused) != 1 || refused[0].Subscription.ID() != oversized.ID() || refused[0].Err == nil {
		t.Fatalf("Renew() refused %d subscriptions, want the oversized one alone, with the store's reason", len(refused))
	}

	lists, err := svc.SubscribersOfList(t.Context(), res, "anvil")
	if err != nil {
		t.Fatalf("SubscribersOfList() error = %v", err)
	}
	if diff := cmp.Diff([]live.Subscription{list}, lists); diff != "" {
		t.Errorf("the list written beside the refused document (-want +got):\n%s", diff)
	}
	rows, err := svc.SubscribersOfRow(t.Context(), res, "k1")
	if err != nil {
		t.Fatalf("SubscribersOfRow() error = %v", err)
	}
	if diff := cmp.Diff([]live.Subscription{row}, rows); diff != "" {
		t.Errorf("the row written beside the refused document (-want +got):\n%s", diff)
	}
	stored, err := livefirestore.StoredSubscriptions(t.Context(), svc, who)
	if err != nil {
		t.Fatalf("StoredSubscriptions() error = %v", err)
	}
	if stored != 2 {
		t.Errorf("the record holds %d documents, want the two the store took", stored)
	}
}
