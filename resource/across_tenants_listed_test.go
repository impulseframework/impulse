package resource

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// TestTenantsListed pins the set of tenants a live subscription to a list across
// tenants is held over: the tenants the session reaches, narrowed by the page's
// filter on the tenant, in which the person holds the List grant on the record. The
// list's own request and the renewal's re-check must come to the same set, so each
// case asks both.
func TestTenantsListed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		target       string
		perms        func() *acrossPermissions
		wantSet      []accesstypes.Domain
		wantNarrowed []accesstypes.Domain
	}{
		{
			name:    "every tenant the session reaches in which the person may list the record",
			target:  "/crates",
			perms:   sameRoles,
			wantSet: []accesstypes.Domain{"east", "north", "south"},
		},
		{
			name:    "a tenant in which the person holds no List grant is left out",
			target:  "/crates",
			perms:   noListInSouth,
			wantSet: []accesstypes.Domain{"east", "north"},
		},
		{
			name:    "a tenant in which the grant carries a condition is in the set: the rows decide",
			target:  "/crates",
			perms:   differingRoles,
			wantSet: []accesstypes.Domain{"east", "north", "south"},
		},
		{
			name:         "the page's filter on the tenant narrows the set, and travels as sent",
			target:       "/crates?domains=south,north",
			perms:        sameRoles,
			wantSet:      []accesstypes.Domain{"north", "south"},
			wantNarrowed: []accesstypes.Domain{"north", "south"},
		},
		{
			name:         "a tenant the session does not reach is never in the set, named or not",
			target:       "/crates?domains=west,east",
			perms:        sameRoles,
			wantSet:      []accesstypes.Domain{"east"},
			wantNarrowed: []accesstypes.Domain{"east", "west"},
		},
		{
			name:         "a filter that names only a tenant out of reach leaves no tenant",
			target:       "/crates?domains=west",
			perms:        sameRoles,
			wantSet:      []accesstypes.Domain{},
			wantNarrowed: []accesstypes.Domain{"west"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			qSet, err := crateDecoder(t).DecodeAcrossTenants(acrossRequest(t, tt.target), tt.perms())
			if err != nil {
				t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
			}
			set, narrowed, err := qSet.TenantsListed(t.Context())
			if err != nil {
				t.Fatalf("QuerySet.TenantsListed() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantSet, set); diff != "" {
				t.Errorf("QuerySet.TenantsListed() set mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantNarrowed, narrowed); diff != "" {
				t.Errorf("QuerySet.TenantsListed() narrowed mismatch (-want +got):\n%s", diff)
			}

			// The renewal carries the filter back and comes to the same set.
			perms := tt.perms()
			again, err := TenantsListed(t.Context(), perms, acrossCrates, narrowed)
			if err != nil {
				t.Fatalf("TenantsListed() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantSet, again); diff != "" {
				t.Errorf("TenantsListed() mismatch (-want +got):\n%s", diff)
			}
			// A tenant out of the session's reach is not even asked about.
			if slices.Contains(perms.asked, "west") {
				t.Errorf("TenantsListed() asked the grants of west, a tenant the session does not reach: %v", perms.asked)
			}
		})
	}
}

// TestTenantsListed_notAcrossTenants pins that one tenant's own list has no set to
// hold a subscription over: its subscription is the tenant's.
func TestTenantsListed_notAcrossTenants(t *testing.T) {
	t.Parallel()

	qSet, err := crateDecoder(t).Decode(acrossRequest(t, "/crates"), sameRoles(), accesstypes.DomainScope("north"))
	if err != nil {
		t.Fatalf("QueryDecoder.Decode() error = %v", err)
	}
	_, _, err = qSet.TenantsListed(t.Context())
	want := "resource.QuerySet.TenantsListed(): the Crates query is not a list across tenants; QueryDecoder.DecodeAcrossTenants decodes one"
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("QuerySet.TenantsListed() error = %v, want %q", err, want)
	}
}
