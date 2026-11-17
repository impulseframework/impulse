package live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// One subscription over a set of tenants: a list across tenants is one record per tab
// and list however many tenants it covers, one tenant's list is the same record with a
// set of one, and the publisher's question, who is subscribed to this list in this
// tenant, is answered from the sets.

// reaching is grantTable with a session that reaches the given tenants.
type reaching struct {
	grantTable
	reach []accesstypes.Domain
}

func (r *reaching) Domains(context.Context) ([]accesstypes.Domain, error) {
	return r.reach, nil
}

// acrossList is a decoded list across tenants as the handler hands it to the live layer.
type acrossList struct {
	set      []accesstypes.Domain
	narrowed []accesstypes.Domain
	err      error
}

func (acrossList) Resource() accesstypes.Resource {
	return "Hangars"
}

func (l acrossList) TenantsListed(context.Context) (set, narrowed []accesstypes.Domain, err error) {
	return l.set, l.narrowed, l.err
}

func TestSubscription_acrossTenants(t *testing.T) {
	t.Parallel()

	across := func(set, narrowed []accesstypes.Domain) Subscription {
		sub := *AcrossTenantsSubscription("Hangars", set, narrowed)
		sub.Principal, sub.Tab = "dispatcher", "t1"

		return sub
	}
	oneTenant := Subscription{Principal: "dispatcher", Tab: "t1", Resource: "Hangars", Domain: "anvil"}

	t.Run("one tenant's list keeps the id it always had", func(t *testing.T) {
		t.Parallel()

		sum := sha256.Sum256([]byte("dispatcher|t1|Hangars||anvil"))
		if got, want := oneTenant.ID(), hex.EncodeToString(sum[:])[:32]; got != want {
			t.Errorf("ID() = %s, want %s, the first 32 hex characters of sha256(principal|tab|resource|key|domain)", got, want)
		}
	})

	ids := []struct {
		name string
		a, b Subscription
		same bool
	}{
		{name: "the set is not part of the identity: a renewal that comes to another set writes the same document", a: across([]accesstypes.Domain{"anvil", "cinder"}, nil), b: across([]accesstypes.Domain{"anvil"}, nil), same: true},
		{name: "nor is the order the set was worked out in", a: across([]accesstypes.Domain{"cinder", "anvil"}, nil), b: across([]accesstypes.Domain{"anvil", "cinder"}, nil), same: true},
		{name: "the page's filter on the tenant is: two pages narrowed differently are two lists", a: across([]accesstypes.Domain{"anvil"}, []accesstypes.Domain{"anvil"}), b: across([]accesstypes.Domain{"anvil"}, nil)},
		{name: "a filter is the same list in any order", a: across(nil, []accesstypes.Domain{"cinder", "anvil"}), b: across(nil, []accesstypes.Domain{"anvil", "cinder"}), same: true},
		{name: "a filter naming no tenant is not the list of every tenant", a: across(nil, []accesstypes.Domain{}), b: across(nil, nil)},
		{name: "a list across tenants whose set is one tenant is not that tenant's own list", a: across([]accesstypes.Domain{"anvil"}, nil), b: oneTenant},
	}
	for _, tt := range ids {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.a.ID() == tt.b.ID(); got != tt.same {
				t.Errorf("ID() equal = %v, want %v (%s vs %s)", got, tt.same, tt.a.ID(), tt.b.ID())
			}
		})
	}

	sets := []struct {
		name        string
		sub         Subscription
		wantTenants []accesstypes.Domain
		// wantIn is what the publisher is answered in anvil: nil when anvil is not in the set.
		wantIn *Subscription
	}{
		{
			name:        "one tenant's list is the set of that tenant",
			sub:         oneTenant,
			wantTenants: []accesstypes.Domain{"anvil"},
			wantIn:      &Subscription{Principal: "dispatcher", Tab: "t1", Resource: "Hangars", Domain: "anvil"},
		},
		{
			name:        "a global list is the empty domain alone",
			sub:         Subscription{Principal: "dispatcher", Tab: "t1", Resource: "Ships"},
			wantTenants: []accesstypes.Domain{""},
		},
		{
			name:        "a row is the empty domain alone, whatever domain it was read in",
			sub:         Subscription{Principal: "dispatcher", Tab: "t1", Resource: "Hangars", Key: "h1", Domain: "anvil"},
			wantTenants: []accesstypes.Domain{""},
		},
		{
			name:        "a list across tenants is its set, sorted, each tenant once, answered as each tenant's list",
			sub:         across([]accesstypes.Domain{"cinder", "anvil", "cinder"}, nil),
			wantTenants: []accesstypes.Domain{"anvil", "cinder"},
			wantIn:      &Subscription{Principal: "dispatcher", Tab: "t1", Resource: "Hangars", Domain: "anvil"},
		},
		{
			name:        "a list across tenants that does not cover the tenant is not answered there",
			sub:         across([]accesstypes.Domain{"bastion", "cinder"}, nil),
			wantTenants: []accesstypes.Domain{"bastion", "cinder"},
		},
		{
			name:        "a set on a subscription that is not a list across tenants is not a set: its domain is",
			sub:         Subscription{Principal: "dispatcher", Tab: "t1", Resource: "Hangars", Domain: "anvil", Domains: []accesstypes.Domain{"bastion"}},
			wantTenants: []accesstypes.Domain{"anvil"},
			wantIn:      &Subscription{Principal: "dispatcher", Tab: "t1", Resource: "Hangars", Domain: "anvil"},
		},
	}
	for _, tt := range sets {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.wantTenants, tt.sub.Tenants()); diff != "" {
				t.Errorf("Tenants() mismatch (-want +got):\n%s", diff)
			}
			got, ok := tt.sub.InTenant("anvil")
			if ok != (tt.wantIn != nil) {
				t.Fatalf("InTenant(anvil) ok = %v, want %v", ok, tt.wantIn != nil)
			}
			if tt.wantIn != nil {
				if diff := cmp.Diff(*tt.wantIn, got); diff != "" {
					t.Errorf("InTenant(anvil) mismatch (-want +got):\n%s", diff)
				}
			}
			if got, want := len(tt.sub.PerTenant()), len(tt.wantTenants); got != want {
				t.Errorf("len(PerTenant()) = %d, want %d, one for each tenant of the set", got, want)
			}
		})
	}
}

func TestSubscribeAcrossTenants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		list   acrossList
		want   []Subscription
	}{
		{
			name:   "a page that asks for live registers one subscription over the set, however many tenants",
			header: "tab-1",
			list:   acrossList{set: []accesstypes.Domain{"cinder", "anvil"}},
			want: []Subscription{
				{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Hangars", AcrossTenants: true, Domains: []accesstypes.Domain{"anvil", "cinder"}},
			},
		},
		{
			name:   "the page's filter on the tenant is kept with it",
			header: "tab-1",
			list:   acrossList{set: []accesstypes.Domain{"cinder"}, narrowed: []accesstypes.Domain{"cinder", "bastion"}},
			want: []Subscription{
				{Principal: "crew|dispatcher", Tab: "tab-1", Resource: "Hangars", AcrossTenants: true, Narrowed: []accesstypes.Domain{"bastion", "cinder"}, Domains: []accesstypes.Domain{"cinder"}},
			},
		},
		{
			name: "a list across tenants is plain unless the page asks: no header, nothing registered",
			list: acrossList{set: []accesstypes.Domain{"anvil", "cinder"}},
		},
		{
			name:   "a list that reads no tenant registers nothing: nothing can change it",
			header: "tab-1",
			list:   acrossList{set: []accesstypes.Domain{}},
		},
		{
			name:   "a set that cannot be worked out registers nothing and the request goes on",
			header: "tab-1",
			list:   acrossList{err: errors.New("the grants are down")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := NewFake()
			r := httptest.NewRequestWithContext(withSession(t.Context(), crewAuth, "dispatcher"), http.MethodGet, "/hangars", http.NoBody)
			if tt.header != "" {
				r.Header.Set(SubscribeHeader, tt.header)
			}

			SubscribeAcrossTenants(r.Context(), r, fake, tt.list)

			if diff := cmp.Diff(tt.want, fake.Subscriptions(), cmpopts.IgnoreFields(Subscription{}, "Expiry"), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("registered subscriptions mismatch (-want +got):\n%s", diff)
			}
			for _, sub := range fake.Subscriptions() {
				if left := time.Until(sub.Expiry); left <= 0 || left > SubscriptionTTL {
					t.Errorf("the subscription expires in %v, want within %v", left, SubscriptionTTL)
				}
			}
		})
	}
}

// TestFanout_acrossTenants pins what the publisher, asking what it always asked, is
// answered from the sets: a write in a tenant reaches the list across tenants of a
// person whose set holds the tenant beside that tenant's own list, and never one whose
// set does not, so a page is not woken by a change in a tenant the person cannot enter.
func TestFanout_acrossTenants(t *testing.T) {
	t.Parallel()

	const hangars = accesstypes.Resource("Hangars")
	register := func(t *testing.T) *Fake {
		t.Helper()

		fake := NewFake()
		liaison := AcrossTenantsSubscription(hangars, []accesstypes.Domain{"anvil", "cinder"}, nil).Normalized()
		liaison.Principal, liaison.Tab = "liaison", "t1"
		subs := []Subscription{
			liaison,
			{Principal: "marshal", Tab: "t2", Resource: hangars, Domain: "anvil"},
			{Principal: "bosun", Tab: "t3", Resource: hangars, Domain: "bastion"},
			// The liaison also has Anvil's own list open in another tab.
			{Principal: "liaison", Tab: "t4", Resource: hangars, Domain: "anvil"},
		}
		for i := range subs {
			subs[i].Expiry = time.Now().Add(SubscriptionTTL)
		}
		if err := fake.Register(t.Context(), subs); err != nil {
			t.Fatalf("Register() error = %v", err)
		}

		return fake
	}
	list := func(domain accesstypes.Domain) ChangeDocument {
		return ChangeDocument{Kind: ListChange, Resource: hangars, Domain: domain}
	}
	whole := ChangeDocument{Kind: ResourceChange, Resource: hangars}

	tests := []struct {
		name        string
		domain      accesstypes.Domain
		rows        []resource.RowChange
		want        Changes
		wantLookups int
	}{
		{
			name:   "a write in a tenant of the set: the list across tenants hears it once, beside the tenant's own list",
			domain: "anvil",
			rows:   []resource.RowChange{{Key: "h1"}},
			want:   Changes{"liaison": {list("anvil")}, "marshal": {list("anvil")}},
			// The list's subscribers and the row's: what one tenant's write always cost.
			wantLookups: 2,
		},
		{
			name:        "a write in another tenant of the set names that tenant's list",
			domain:      "cinder",
			rows:        []resource.RowChange{{Key: "h7"}},
			want:        Changes{"liaison": {list("cinder")}},
			wantLookups: 2,
		},
		{
			name:        "a write in a tenant outside the set never reaches it",
			domain:      "bastion",
			rows:        []resource.RowChange{{Key: "h4"}},
			want:        Changes{"bosun": {list("bastion")}},
			wantLookups: 2,
		},
		{
			name:        "over the threshold, the one lookup of the resource answers each set tenant by tenant",
			domain:      "cinder",
			rows:        manyRowChanges(BulkThreshold + 1),
			want:        Changes{"liaison": {whole}},
			wantLookups: 1,
		},
		{
			name:        "and a bulk write outside the set reaches only that tenant's list",
			domain:      "bastion",
			rows:        manyRowChanges(BulkThreshold + 1),
			want:        Changes{"bosun": {whole}},
			wantLookups: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := register(t)
			got, err := Fanout(t.Context(), fake, tt.domain, Writes{Rows: map[accesstypes.Resource][]resource.RowChange{hangars: tt.rows}})
			if err != nil {
				t.Fatalf("Fanout() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Fanout() mismatch (-want +got):\n%s", diff)
			}
			if got := fake.Lookups(); got != tt.wantLookups {
				t.Errorf("record lookups = %d, want %d", got, tt.wantLookups)
			}
		})
	}
}

// manyRowChanges is n written rows of one resource.
func manyRowChanges(n int) []resource.RowChange {
	rows := make([]resource.RowChange, 0, n)
	for i := range n {
		rows = append(rows, resource.RowChange{Key: "h" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)})
	}

	return rows
}

// TestRenewHandler_acrossTenants pins the renewal of a list across tenants: the set is
// worked out again from the session's reach, the person's grants and the filter the
// renewal carries, and written under the same document.
func TestRenewHandler_acrossTenants(t *testing.T) {
	t.Parallel()

	// The liaison reaches three sectors and lists hangars in two of them.
	grants := map[string]accesstypes.Decision{
		"List|anvil|Hangars":  accesstypes.Granted(),
		"List|cinder|Hangars": accesstypes.Conditional(accesstypes.ConditionGroup{}),
		"List|dawn|Hangars":   accesstypes.Granted(),
	}
	perms := func(reach ...accesstypes.Domain) *reaching {
		return &reaching{grantTable: grantTable{decisions: grants}, reach: reach}
	}
	renew := func(t *testing.T, fake *Fake, perms resource.UserPermissions, body string) (int, RenewResponse, string) {
		t.Helper()

		r := httptest.NewRequestWithContext(withSession(t.Context(), crewAuth, "liaison"), http.MethodPost, "/"+RenewRoute, strings.NewReader(body))
		w := httptest.NewRecorder()
		RenewHandler(fake, permissionsFor(perms)).ServeHTTP(w, r)
		var resp RenewResponse
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decoding the renewal's answer: %v: %s", err, w.Body.String())
			}
		}

		return w.Code, resp, w.Body.String()
	}
	ignoreExpiry := cmpopts.IgnoreFields(Subscription{}, "Expiry")

	t.Run("the set is the tenants reached in which the person may list the resource", func(t *testing.T) {
		t.Parallel()

		fake := NewFake()
		status, resp, body := renew(t, fake, perms("anvil", "bastion", "cinder"), `{"tab":"tab-1","subscriptions":[{"resource":"Hangars","acrossTenants":true}]}`)
		if status != http.StatusOK {
			t.Fatalf("status = %d: %s", status, body)
		}
		if diff := cmp.Diff([]SubscriptionRequest{{Resource: "Hangars", AcrossTenants: true}}, resp.Kept); diff != "" {
			t.Errorf("kept mismatch (-want +got):\n%s", diff)
		}
		want := []Subscription{{Principal: "crew|liaison", Tab: "tab-1", Resource: "Hangars", AcrossTenants: true, Domains: []accesstypes.Domain{"anvil", "cinder"}}}
		if diff := cmp.Diff(want, fake.Subscriptions(), ignoreExpiry); diff != "" {
			t.Errorf("the record mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a tenant gained or lost since is in or out of the set from this renewal on, in the same document", func(t *testing.T) {
		t.Parallel()

		fake := NewFake()
		body := `{"tab":"tab-1","subscriptions":[{"resource":"Hangars","acrossTenants":true}]}`
		if status, _, answer := renew(t, fake, perms("anvil", "bastion", "cinder"), body); status != http.StatusOK {
			t.Fatalf("status = %d: %s", status, answer)
		}
		// Her seat in Anvil ends and she is seated in Dawn.
		if status, _, answer := renew(t, fake, perms("bastion", "cinder", "dawn"), body); status != http.StatusOK {
			t.Fatalf("status = %d: %s", status, answer)
		}
		want := []Subscription{{Principal: "crew|liaison", Tab: "tab-1", Resource: "Hangars", AcrossTenants: true, Domains: []accesstypes.Domain{"cinder", "dawn"}}}
		if diff := cmp.Diff(want, fake.Subscriptions(), ignoreExpiry); diff != "" {
			t.Errorf("the record mismatch (-want +got):\n%s", diff)
		}
		// A write in Anvil no longer reaches her; one in Dawn does.
		for domain, wantReached := range map[accesstypes.Domain]bool{"anvil": false, "dawn": true} {
			subs, err := fake.SubscribersOfList(t.Context(), "Hangars", domain)
			if err != nil {
				t.Fatalf("SubscribersOfList() error = %v", err)
			}
			if got := len(subs) == 1; got != wantReached {
				t.Errorf("SubscribersOfList(%s) = %v, want reached = %v", domain, subs, wantReached)
			}
		}
	})

	t.Run("the page's filter travels with the renewal and narrows the set", func(t *testing.T) {
		t.Parallel()

		fake := NewFake()
		status, resp, body := renew(t, fake, perms("anvil", "bastion", "cinder"), `{"tab":"tab-1","subscriptions":[
			{"resource":"Hangars","acrossTenants":true},
			{"resource":"Hangars","acrossTenants":true,"domains":"cinder,bastion"}]}`)
		if status != http.StatusOK {
			t.Fatalf("status = %d: %s", status, body)
		}
		if len(resp.Kept) != 2 || len(resp.Dropped) != 0 {
			t.Errorf("kept %v, dropped %v, want both kept", resp.Kept, resp.Dropped)
		}
		want := []Subscription{
			{Principal: "crew|liaison", Tab: "tab-1", Resource: "Hangars", AcrossTenants: true, Domains: []accesstypes.Domain{"anvil", "cinder"}},
			{Principal: "crew|liaison", Tab: "tab-1", Resource: "Hangars", AcrossTenants: true, Narrowed: []accesstypes.Domain{"bastion", "cinder"}, Domains: []accesstypes.Domain{"cinder"}},
		}
		if diff := cmp.Diff(want, fake.Subscriptions(), ignoreExpiry, cmpopts.SortSlices(func(a, b Subscription) bool { return len(a.Narrowed) < len(b.Narrowed) })); diff != "" {
			t.Errorf("the record mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a set that came to no tenant is dropped, and nothing is written", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			perms *reaching
			sub   string
			want  SubscriptionRequest
		}{
			{name: "she reaches no tenant in which she may list it", perms: perms("bastion"), sub: `{"resource":"Hangars","acrossTenants":true}`, want: SubscriptionRequest{Resource: "Hangars", AcrossTenants: true}},
			{name: "the filter names only a tenant she does not reach", perms: perms("anvil", "cinder"), sub: `{"resource":"Hangars","acrossTenants":true,"domains":"dawn"}`, want: SubscriptionRequest{Resource: "Hangars", AcrossTenants: true, Domains: "dawn"}},
			{name: "a resource the grants do not know", perms: perms("anvil", "cinder"), sub: `{"resource":"Unknown","acrossTenants":true}`, want: SubscriptionRequest{Resource: "Unknown", AcrossTenants: true}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				fake := NewFake()
				status, resp, body := renew(t, fake, tt.perms, `{"tab":"tab-1","subscriptions":[`+tt.sub+`]}`)
				if status != http.StatusOK {
					t.Fatalf("status = %d: %s", status, body)
				}
				if diff := cmp.Diff([]SubscriptionRequest{tt.want}, resp.Dropped); diff != "" {
					t.Errorf("dropped mismatch (-want +got):\n%s", diff)
				}
				if len(resp.Kept) != 0 || len(fake.Subscriptions()) != 0 {
					t.Errorf("kept %v, the record %v, want neither", resp.Kept, fake.Subscriptions())
				}
			})
		}
	})

	t.Run("a renewal that mixes the two shapes is refused, naming the subscription", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			sub  string
			want string
		}{
			{name: "a list across tenants naming a domain", sub: `{"resource":"Hangars","acrossTenants":true,"domain":"anvil"}`, want: "the Hangars subscription across tenants names a key or a domain: a list across tenants names neither, and narrows itself with domains"},
			{name: "a list across tenants naming a key", sub: `{"resource":"Hangars","acrossTenants":true,"key":"h1"}`, want: "the Hangars subscription across tenants names a key or a domain"},
			{name: "one tenant's list carrying domains", sub: `{"resource":"Hangars","domain":"anvil","domains":"cinder"}`, want: "the Hangars subscription carries domains and is not a list across tenants: one tenant's list names its tenant in domain"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				fake := NewFake()
				status, _, body := renew(t, fake, perms("anvil", "cinder"), `{"tab":"tab-1","subscriptions":[`+tt.sub+`]}`)
				if status != http.StatusBadRequest || !strings.Contains(body, tt.want) {
					t.Errorf("status = %d, body = %s, want 400 naming %q", status, body, tt.want)
				}
				if len(fake.Subscriptions()) != 0 {
					t.Errorf("the record holds %v, want nothing written", fake.Subscriptions())
				}
			})
		}
	})
}
