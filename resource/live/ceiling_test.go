package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// The ceiling on the tenants one subscription holds (MaxSubscriptionTenants): a list
// across tenants up to it is one live subscription, a list across more is served plain,
// and a subscription whose set grew past it is dropped at its renewal, with the tab's
// others kept. A subscription the record does not take is dropped the same way.

// tenantKeys is n tenants' keys, each as long as the id a tenant is keyed by.
func tenantKeys(n int) []accesstypes.Domain {
	keys := make([]accesstypes.Domain, 0, n)
	for i := range n {
		keys = append(keys, accesstypes.Domain(fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)))
	}

	return keys
}

// reachingMany is a person who holds every grant, in a session that reaches the given
// tenants.
type reachingMany struct {
	grantAll
	reach []accesstypes.Domain
}

func (r *reachingMany) Domains(context.Context) ([]accesstypes.Domain, error) {
	return r.reach, nil
}

// refusing is a record that does not take the subscriptions to the named resource at a
// renewal, and writes the others.
type refusing struct {
	*Fake
	resource accesstypes.Resource
}

func (r *refusing) Renew(ctx context.Context, subs []Subscription) ([]Refusal, error) {
	taken := make([]Subscription, 0, len(subs))
	var refused []Refusal
	for i := range subs {
		if subs[i].Resource == r.resource {
			refused = append(refused, Refusal{Subscription: subs[i], Err: errors.New("the store judged the document invalid")})

			continue
		}
		taken = append(taken, subs[i])
	}

	return refused, r.Register(ctx, taken)
}

func TestSubscribeAcrossTenants_ceiling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// tenants is how many tenants the list reads for the person.
		tenants   int
		wantPlain bool
		// wantHeld is how many tenants the one registered subscription holds; none
		// when nothing is registered.
		wantHeld  int
		wantCache string
	}{
		{
			name:      "a set at the ceiling is one subscription over all of it, and the answer is a live one",
			tenants:   MaxSubscriptionTenants,
			wantHeld:  MaxSubscriptionTenants,
			wantCache: CacheControl,
		},
		{
			name:      "a set one tenant past the ceiling registers nothing, and the list is served plain",
			tenants:   MaxSubscriptionTenants + 1,
			wantPlain: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := NewFake()
			r := httptest.NewRequestWithContext(withSession(t.Context(), crewAuth, "dispatcher"), http.MethodGet, "/hangars?"+VersionParam+"=1794890922227000", http.NoBody)
			r.Header.Set(SubscribeHeader, "tab-1")

			liveness := SubscribeAcrossTenants(r.Context(), httptest.NewRecorder(), r, fake, acrossList{set: tenantKeys(tt.tenants)})

			if got := liveness.Plain(); got != tt.wantPlain {
				t.Errorf("Plain() = %v, want %v", got, tt.wantPlain)
			}
			// The page's answer: what the generated handler marks the response with.
			w := httptest.NewRecorder()
			liveness.SetCacheControl(w, r)
			if got := w.Header().Get("Cache-Control"); got != tt.wantCache {
				t.Errorf("Cache-Control = %q, want %q: a plain list's answer is not the browser's to keep", got, tt.wantCache)
			}
			subs := fake.Subscriptions()
			if tt.wantHeld == 0 {
				if len(subs) != 0 {
					t.Fatalf("the record holds %d subscriptions, want none", len(subs))
				}

				return
			}
			if len(subs) != 1 || subs[0].Across == nil {
				t.Fatalf("the record holds %d subscriptions, want the one over the set", len(subs))
			}
			if got := len(subs[0].Across.Domains); got != tt.wantHeld {
				t.Errorf("the subscription holds %d tenants, want %d", got, tt.wantHeld)
			}
		})
	}
}

// renewAs posts a renewal for the liaison's tab and decodes its answer.
func renewAs(t *testing.T, record Service, perms *reachingMany, body string) (status int, resp RenewResponse, raw string) {
	t.Helper()

	r := httptest.NewRequestWithContext(withSession(t.Context(), crewAuth, "liaison"), http.MethodPost, "/"+RenewRoute, strings.NewReader(body))
	w := httptest.NewRecorder()
	RenewHandler(record, permissionsFor(perms), WithListsAcrossTenants(declaredLists{"Hangars"})).ServeHTTP(w, r)
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decoding the renewal's answer: %v: %s", err, w.Body.String())
		}
	}

	return w.Code, resp, w.Body.String()
}

func TestRenewHandler_ceiling(t *testing.T) {
	t.Parallel()

	const body = `{"tab":"tab-1","subscriptions":[{"resource":"Ships","domain":"anvil"},{"resource":"Hangars","acrossTenants":true},{"resource":"Ships","key":"s1","domain":"anvil"}]}`
	across := SubscriptionRequest{Resource: "Hangars", AcrossTenants: true}
	list := SubscriptionRequest{Resource: "Ships", Domain: "anvil"}
	row := SubscriptionRequest{Resource: "Ships", Key: "s1", Domain: "anvil"}

	tests := []struct {
		name string
		// tenants is how many tenants the session reaches now.
		tenants     int
		wantKept    []SubscriptionRequest
		wantDropped []SubscriptionRequest
		// wantHeld is how many tenants the renewed subscription over the set holds;
		// none when it is dropped.
		wantHeld int
	}{
		{
			name:        "a set that has come to the ceiling is kept, over all of it",
			tenants:     MaxSubscriptionTenants,
			wantKept:    []SubscriptionRequest{list, across, row},
			wantDropped: []SubscriptionRequest{},
			wantHeld:    MaxSubscriptionTenants,
		},
		{
			name:        "a set that has grown past the ceiling is dropped, and the tab's others are kept and extended",
			tenants:     MaxSubscriptionTenants + 1,
			wantKept:    []SubscriptionRequest{list, row},
			wantDropped: []SubscriptionRequest{across},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := NewFake()
			status, resp, raw := renewAs(t, fake, &reachingMany{reach: tenantKeys(tt.tenants)}, body)
			if status != http.StatusOK {
				t.Fatalf("status = %d: %s", status, raw)
			}
			if diff := cmp.Diff(tt.wantKept, resp.Kept); diff != "" {
				t.Errorf("kept mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantDropped, resp.Dropped); diff != "" {
				t.Errorf("dropped mismatch (-want +got):\n%s", diff)
			}
			held := 0
			for _, sub := range fake.Subscriptions() {
				if left := time.Until(sub.Expiry); left <= 0 || left > SubscriptionTTL {
					t.Errorf("the %s subscription expires in %v, want it extended to within %v", sub.Resource, left, SubscriptionTTL)
				}
				if sub.Across != nil {
					held = len(sub.Across.Domains)
				}
			}
			if got := len(fake.Subscriptions()); got != len(tt.wantKept) {
				t.Errorf("the record holds %d subscriptions, want the %d kept", got, len(tt.wantKept))
			}
			if held != tt.wantHeld {
				t.Errorf("the subscription over the set holds %d tenants, want %d", held, tt.wantHeld)
			}
		})
	}
}

// TestRenewHandler_recordRefusesOne pins that a subscription the record does not take
// stops none of the others: it is dropped, and the rest of the tab's renewal is kept,
// written and answered. A failure of the record as a whole still fails the renewal.
func TestRenewHandler_recordRefusesOne(t *testing.T) {
	t.Parallel()

	const body = `{"tab":"tab-1","subscriptions":[{"resource":"Ships","domain":"anvil"},{"resource":"Refits","domain":"anvil"},{"resource":"Ships","key":"s1","domain":"anvil"}]}`
	list := SubscriptionRequest{Resource: "Ships", Domain: "anvil"}
	refits := SubscriptionRequest{Resource: "Refits", Domain: "anvil"}
	row := SubscriptionRequest{Resource: "Ships", Key: "s1", Domain: "anvil"}
	perms := &reachingMany{reach: []accesstypes.Domain{"anvil"}}

	t.Run("the refused one is dropped, and the others are kept and extended", func(t *testing.T) {
		t.Parallel()

		fake := NewFake()
		status, resp, raw := renewAs(t, &refusing{Fake: fake, resource: "Refits"}, perms, body)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want the renewal answered for the subscriptions the record took: %s", status, raw)
		}
		if diff := cmp.Diff([]SubscriptionRequest{list, row}, resp.Kept); diff != "" {
			t.Errorf("kept mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]SubscriptionRequest{refits}, resp.Dropped); diff != "" {
			t.Errorf("dropped mismatch (-want +got):\n%s", diff)
		}
		if left := time.Until(resp.ExpiresAt); left <= 0 || left > SubscriptionTTL {
			t.Errorf("the kept subscriptions expire in %v, want within %v", left, SubscriptionTTL)
		}
		written := make([]string, 0, 2)
		for _, sub := range fake.Subscriptions() {
			written = append(written, string(sub.Resource)+"|"+sub.Key)
		}
		slices.Sort(written)
		if diff := cmp.Diff([]string{"Ships|", "Ships|s1"}, written); diff != "" {
			t.Errorf("the record holds (-want +got):\n%s", diff)
		}
	})

	t.Run("the record out of reach fails the renewal as a whole, which the tab makes again", func(t *testing.T) {
		t.Parallel()

		fake := NewFake()
		fake.Err = errors.New("the record is out of reach")
		status, _, raw := renewAs(t, fake, perms, body)
		if status != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500: %s", status, raw)
		}
	})
}
