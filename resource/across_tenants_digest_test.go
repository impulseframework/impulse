package resource

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// These tests pin who a page that lists a record across tenants is for: whoever may
// list the record in at least one tenant their session reaches, and nobody else. The
// global permission digest says it, one entry for each record declared for it, so the
// page's menu entry and its route ask the digest and the application names no
// permission of its own.

// acrossGate puts each resource behind a feature flag of its name, every flag off.
func acrossGate(off ...accesstypes.Resource) (FeatureGates, *FeatureSet) {
	gates := make(FeatureGates, len(off))
	for _, res := range off {
		gates[res] = Feature(res)
	}

	return gates, featureSetOf(nil)
}

func TestListedInSomeTenant(t *testing.T) {
	t.Parallel()

	denied := accesstypes.Decisions{acrossCrates: accesstypes.Denied(), acrossSeals: accesstypes.Denied(), acrossBins: accesstypes.Denied()}
	tests := []struct {
		name      string
		perms     *acrossPermissions
		resources []accesstypes.Resource
		want      []accesstypes.Resource
		wantAsked []accesstypes.Domain
	}{
		{
			name:      "a record listed in the first tenant reached is answered there, and no other tenant is asked",
			perms:     sameRoles(),
			resources: []accesstypes.Resource{acrossCrates},
			want:      []accesstypes.Resource{acrossCrates},
			wantAsked: []accesstypes.Domain{"east"},
		},
		{
			name: "a record listed in one tenant alone is found there, each tenant asked until it is",
			perms: &acrossPermissions{
				reach:  []accesstypes.Domain{"east", "north", "south"},
				grants: map[accesstypes.Domain]accesstypes.Decisions{"east": denied, "north": denied, "south": {acrossSeals: accesstypes.Denied(), acrossBins: accesstypes.Denied()}},
			},
			resources: []accesstypes.Resource{acrossSeals, acrossCrates, acrossBins},
			want:      []accesstypes.Resource{acrossCrates},
			wantAsked: []accesstypes.Domain{"east", "north", "south"},
		},
		{
			name: "a grant under a condition lists the record",
			perms: &acrossPermissions{
				reach:  []accesstypes.Domain{"north"},
				grants: map[accesstypes.Domain]accesstypes.Decisions{"north": {acrossCrates: conditionalOn(acrossCrates, "owner = subject")}},
			},
			resources: []accesstypes.Resource{acrossCrates},
			want:      []accesstypes.Resource{acrossCrates},
			wantAsked: []accesstypes.Domain{"north"},
		},
		{
			name: "a record listed only in a tenant the session does not reach is not listed",
			perms: &acrossPermissions{
				reach:  []accesstypes.Domain{"north"},
				grants: map[accesstypes.Domain]accesstypes.Decisions{"north": denied, "west": everyGrant()},
			},
			resources: []accesstypes.Resource{acrossCrates},
			want:      []accesstypes.Resource{},
			wantAsked: []accesstypes.Domain{"north"},
		},
		{
			name:      "a session that reaches no tenant lists nothing",
			perms:     &acrossPermissions{grants: map[accesstypes.Domain]accesstypes.Decisions{"north": everyGrant()}},
			resources: []accesstypes.Resource{acrossCrates},
			want:      []accesstypes.Resource{},
		},
		{
			name:  "no record declared asks nothing",
			perms: sameRoles(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ListedInSomeTenant(t.Context(), tt.perms, tt.resources)
			if err != nil {
				t.Fatalf("ListedInSomeTenant() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ListedInSomeTenant() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantAsked, tt.perms.asked); diff != "" {
				t.Errorf("the tenants asked mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPermissionDigestHandler_listsAcrossTenants(t *testing.T) {
	t.Parallel()

	nowhere := accesstypes.Decisions{acrossCrates: accesstypes.Denied(), acrossSeals: accesstypes.Denied(), acrossBins: accesstypes.Denied()}
	granted := map[accesstypes.Permission]accesstypes.DigestState{ListAcrossTenants: accesstypes.DigestGranted}
	tests := []struct {
		name   string
		perms  *acrossPermissions
		target string
		opts   func(t *testing.T) []DigestOption
		want   accesstypes.PermissionDigest
	}{
		{
			name:   "the global digest answers every declared record the person may list in a tenant they reach",
			perms:  sameRoles(),
			target: "/permission-digest",
			want:   accesstypes.PermissionDigest{acrossBins: granted, acrossCrates: granted, acrossSeals: granted},
		},
		{
			name: "a record the person may list in no tenant they reach is absent",
			perms: &acrossPermissions{
				reach:  []accesstypes.Domain{"north", "south"},
				grants: map[accesstypes.Domain]accesstypes.Decisions{"north": {acrossSeals: accesstypes.Denied(), acrossBins: accesstypes.Denied()}, "south": nowhere, "west": everyGrant()},
			},
			target: "/permission-digest",
			want:   accesstypes.PermissionDigest{acrossCrates: granted},
		},
		{
			name:   "a person who may list nothing anywhere holds none",
			perms:  &acrossPermissions{reach: []accesstypes.Domain{"north"}, grants: map[accesstypes.Domain]accesstypes.Decisions{"north": nowhere}},
			target: "/permission-digest",
			want:   accesstypes.PermissionDigest{},
		},
		{
			name:   "a tenant's digest speaks for that tenant alone",
			perms:  sameRoles(),
			target: "/permission-digest?domain=north",
			want:   accesstypes.PermissionDigest{},
		},
		{
			name:   "a record behind a feature flag that is off is left out with the rest of its entries",
			perms:  sameRoles(),
			target: "/permission-digest",
			opts: func(t *testing.T) []DigestOption {
				t.Helper()
				gates, features := acrossGate(acrossSeals)

				return []DigestOption{WithFeatureGates(gates, features)}
			},
			want: accesstypes.PermissionDigest{acrossBins: granted, acrossCrates: granted},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := []DigestOption{WithListsAcrossTenants(acrossCollection(t))}
			if tt.opts != nil {
				opts = append(opts, tt.opts(t)...)
			}
			handler := PermissionDigestHandler(func(*http.Request) UserPermissions { return tt.perms }, opts...)
			rr := httptest.NewRecorder()
			handler(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (%s)", rr.Code, rr.Body)
			}
			var got accesstypes.PermissionDigest
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the digest mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("without the option the digest is the engine's", func(t *testing.T) {
		t.Parallel()

		handler := PermissionDigestHandler(func(*http.Request) UserPermissions { return sameRoles() })
		rr := httptest.NewRecorder()
		handler(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/permission-digest", http.NoBody))
		var got accesstypes.PermissionDigest
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if diff := cmp.Diff(accesstypes.PermissionDigest{}, got); diff != "" {
			t.Errorf("the digest mismatch (-want +got):\n%s", diff)
		}
	})
}
