package resource

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// featureHistoryRouter mounts the flags' list, a flag's read and a flag's history the way
// the generated routes do, with the route parameter captured.
func featureHistoryRouter(a *featureTestApp) http.Handler {
	r := chi.NewRouter()
	r.Use(httpio.WithParams)
	r.Get("/api/"+FeatureFlagsRoute, FeatureFlagsHandler(a, nil))
	r.Get("/api/"+FeatureFlagsRoute+"/{"+string(FeatureFlagNameParam)+"}", FeatureFlagHandler(a, nil))
	r.Get("/api/"+FeatureFlagsRoute+"/{"+string(FeatureFlagNameParam)+"}/"+FeatureFlagChangesRoute, FeatureFlagChangesHandler(a, nil))

	return r
}

// featureFieldGrants scripts a user holding the permission on the flags' resource and on
// the named fields of a flag alone.
func featureFieldGrants(perm accesstypes.Permission, fields ...string) *fakeUserPermissions {
	resources := []accesstypes.Resource{FeatureFlagsResource}
	for _, field := range fields {
		resources = append(resources, accesstypes.Resource("FeatureFlags."+field))
	}

	return &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{perm: resources}}
}

// ignoreChangedAt drops the commit timestamps from a comparison of a flag's history.
var ignoreChangedAt = cmpopts.IgnoreFields(FeatureFlagChange{}, "ChangedAt")

// TestFeatureFlagChangesHandler pins a flag's history over the emulator: its flips
// newest first, each with who made it by the name they sign in under, to a List grant on
// FeatureFlags and to no other grant; an empty list for a flag nobody has flipped; an
// unknown flag not found. It pins what the list and the read serve beside it: who last
// flipped each flag, a person by name and a process as it was recorded, and nobody for
// a flag only the deploy step has written.
func TestFeatureFlagChangesHandler(t *testing.T) {
	t.Parallel()

	_, client := featureDatabase(t, "feature-flag-changes")
	gale := accountCtx("gale", aliceID, nil)
	behindRey := accountCtx("rey", bobID, &sessioninfo.Impersonation{
		Actor: "morgan", ActorID: impulse.NullUUIDFromUUID(aliceID), Principal: accesstypes.UserPrincipal(bobUser),
	})
	if err := MigrateFeatures(gale, client, []FeatureDeclaration{
		{Name: "debriefs", Description: "Debriefs lets a crew write debriefs.", Constant: "Debriefs"},
		{Name: "cargo_manifest", Description: "CargoManifest shows a ship's cargo bays.", Constant: "CargoManifest"},
		{Name: "telemetry", Description: "Telemetry reports a ship's position.", Constant: "Telemetry"},
	}); err != nil {
		t.Fatalf("MigrateFeatures() error = %v", err)
	}
	// Three flips of one flag, in order: a process's, a person's, and one made through an
	// impersonated session, which is its actor's.
	flips := []struct {
		enabled bool
		source  string
	}{
		{enabled: true, source: ProcessEvent("bootstrap")},
		{enabled: false, source: UserEvent(gale)},
		{enabled: true, source: UserEvent(behindRey)},
	}
	for _, flip := range flips {
		if err := SetFeatureEnabled(gale, client, "cargo_manifest", flip.enabled, flip.source); err != nil {
			t.Fatalf("SetFeatureEnabled() error = %v", err)
		}
	}
	// A flag only a process has flipped: its row is a process's, as an unflipped flag's is.
	if err := SetFeatureEnabled(gale, client, "telemetry", true, ProcessEvent("bootstrap")); err != nil {
		t.Fatalf("SetFeatureEnabled() error = %v", err)
	}
	features, err := LoadFeatures(gale, client)
	if err != nil {
		t.Fatalf("LoadFeatures() error = %v", err)
	}

	history := "/api/" + FeatureFlagsRoute + "/cargo_manifest/" + FeatureFlagChangesRoute
	tests := []struct {
		name       string
		perms      UserPermissions
		target     string
		wantStatus int
		want       FeatureFlagChangesResponse
	}{
		{
			name:       "a flag's flips, newest first, to a List grant on the flags",
			perms:      featureGrants(accesstypes.List),
			target:     history,
			wantStatus: http.StatusOK,
			want: FeatureFlagChangesResponse{Changes: []FeatureFlagChange{
				{Enabled: true, ChangedBy: "morgan"},
				{Enabled: false, ChangedBy: "gale"},
				{Enabled: true, ChangedBy: "Process bootstrap"},
			}},
		},
		{
			name:       "a flag nobody has flipped answers an empty list, never null",
			perms:      featureGrants(accesstypes.List),
			target:     "/api/" + FeatureFlagsRoute + "/debriefs/" + FeatureFlagChangesRoute,
			wantStatus: http.StatusOK,
			want:       FeatureFlagChangesResponse{Changes: []FeatureFlagChange{}},
		},
		{name: "a flag the table does not hold is not found", perms: featureGrants(accesstypes.List), target: "/api/" + FeatureFlagsRoute + "/unknown/" + FeatureFlagChangesRoute, wantStatus: http.StatusNotFound},
		{name: "a Read grant alone is not the grant that lists the flags", perms: featureGrants(accesstypes.Read), target: history, wantStatus: http.StatusForbidden},
		{name: "an Execute grant on the flip alone is forbidden", perms: featureGrants(accesstypes.Execute), target: history, wantStatus: http.StatusForbidden},
		{name: "no grant is forbidden", perms: featureGrants(), target: history, wantStatus: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := featureHistoryRouter(&featureTestApp{client: client, perms: tt.perms, features: features})
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequestWithContext(gale, http.MethodGet, tt.target, http.NoBody))
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			var raw struct {
				Changes json.RawMessage `json:"changes"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
				t.Fatalf("json.Unmarshal() error = %v: %s", err, rr.Body.String())
			}
			if string(raw.Changes) == "null" || len(raw.Changes) == 0 {
				t.Errorf("changes = %s, want a list", raw.Changes)
			}
			var got FeatureFlagChangesResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v: %s", err, rr.Body.String())
			}
			if diff := cmp.Diff(tt.want, got, ignoreChangedAt); diff != "" {
				t.Errorf("history mismatch (-want +got):\n%s", diff)
			}
			for i, change := range got.Changes {
				if change.ChangedAt.IsZero() {
					t.Errorf("change %d carries no time", i)
				}
				if i > 0 && change.ChangedAt.After(got.Changes[i-1].ChangedAt) {
					t.Errorf("change %d is newer than the one before it: the history is newest first", i)
				}
			}
		})
	}

	t.Run("each flip carries the facts the caller may see of the flag, and no more", func(t *testing.T) {
		t.Parallel()

		every := []string{"description", "enabled", "updatedAt", "updatedBy"}
		fieldTests := []struct {
			name     string
			fields   []string
			target   string
			wantKeys []string
		}{
			{name: "every field of the flag: when, what and who", fields: every, target: history, wantKeys: []string{"changedAt", "changedBy", "enabled"}},
			{name: "a grant without who last flipped the flag leaves who out of every flip", fields: []string{"description", "enabled", "updatedAt"}, target: history, wantKeys: []string{"changedAt", "enabled"}},
			{name: "a grant without when leaves when out", fields: []string{"enabled", "updatedBy"}, target: history, wantKeys: []string{"changedBy", "enabled"}},
			{name: "a grant on the flag's name alone reads that it was flipped and nothing of the flips", fields: nil, target: history, wantKeys: []string{}},
			{name: "columns asked as of the list narrow nothing: the history is the flag's whole", fields: every, target: history + "?columns=name", wantKeys: []string{"changedAt", "changedBy", "enabled"}},
		}
		for _, tt := range fieldTests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				handler := featureHistoryRouter(&featureTestApp{client: client, perms: featureFieldGrants(accesstypes.List, tt.fields...), features: features})
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, httptest.NewRequestWithContext(gale, http.MethodGet, tt.target, http.NoBody))
				if rr.Code != http.StatusOK {
					t.Fatalf("status = %d, want %d (body %s)", rr.Code, http.StatusOK, rr.Body.String())
				}
				var got struct {
					Changes []map[string]any `json:"changes"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
					t.Fatalf("json.Unmarshal() error = %v: %s", err, rr.Body.String())
				}
				if len(got.Changes) != len(flips) {
					t.Fatalf("changes = %d, want %d (body %s)", len(got.Changes), len(flips), rr.Body.String())
				}
				for i, change := range got.Changes {
					keys := slices.Sorted(maps.Keys(change))
					if diff := cmp.Diff(tt.wantKeys, keys, cmpopts.EquateEmpty()); diff != "" {
						t.Errorf("change %d carries the wrong facts (-want +got):\n%s", i, diff)
					}
				}
			})
		}
	})

	t.Run("a read asked for fewer flips than the flag has answers the newest and says there are more", func(t *testing.T) {
		t.Parallel()

		txn := client.ReadOnlyTransaction()
		defer txn.Close()
		got, more, err := readFeatureFlagChanges(t.Context(), txn, client.DBType(), "cargo_manifest", 2)
		if err != nil {
			t.Fatalf("readFeatureFlagChanges() error = %v", err)
		}
		want := []FeatureFlagChange{{Enabled: true, ChangedBy: "morgan"}, {Enabled: false, ChangedBy: "gale"}}
		if diff := cmp.Diff(want, got, ignoreChangedAt); diff != "" || !more {
			t.Errorf("readFeatureFlagChanges() more = %v, mismatch (-want +got):\n%s", more, diff)
		}
		all, more, err := readFeatureFlagChanges(t.Context(), txn, client.DBType(), "cargo_manifest", 3)
		if err != nil {
			t.Fatalf("readFeatureFlagChanges() error = %v", err)
		}
		if len(all) != 3 || more {
			t.Errorf("readFeatureFlagChanges() = %d flips, more = %v; want every flip and no more", len(all), more)
		}
	})

	t.Run("the list serves who last flipped each flag, and a description in a person's words", func(t *testing.T) {
		t.Parallel()

		handler := featureHistoryRouter(&featureTestApp{client: client, perms: featureGrants(accesstypes.List), features: features})
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequestWithContext(gale, http.MethodGet, "/api/"+FeatureFlagsRoute, http.NoBody))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d (body %s)", rr.Code, http.StatusOK, rr.Body.String())
		}
		var rows []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Enabled     bool   `json:"enabled"`
			UpdatedBy   string `json:"updatedBy"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
			t.Fatalf("json.Unmarshal() error = %v: %s", err, rr.Body.String())
		}
		want := []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Enabled     bool   `json:"enabled"`
			UpdatedBy   string `json:"updatedBy"`
		}{
			{Name: "cargo_manifest", Description: "Cargo manifest shows a ship's cargo bays.", Enabled: true, UpdatedBy: "morgan"},
			// Nobody has flipped it: the deploy step that declared it is no one's flip.
			{Name: "debriefs", Description: "Debriefs lets a crew write debriefs.", UpdatedBy: ""},
			// A process flipped it, which the row says as the history does.
			{Name: "telemetry", Description: "Telemetry reports a ship's position.", Enabled: true, UpdatedBy: "Process bootstrap"},
		}
		if diff := cmp.Diff(want, rows); diff != "" {
			t.Errorf("list mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("one flag read by name serves who last flipped it the same way", func(t *testing.T) {
		t.Parallel()

		handler := featureHistoryRouter(&featureTestApp{client: client, perms: featureGrants(accesstypes.Read), features: features})
		for name, want := range map[string]string{"cargo_manifest": "morgan", "debriefs": "", "telemetry": "Process bootstrap"} {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequestWithContext(gale, http.MethodGet, "/api/"+FeatureFlagsRoute+"/"+name, http.NoBody))
			if rr.Code != http.StatusOK {
				t.Fatalf("%s: status = %d, want %d (body %s)", name, rr.Code, http.StatusOK, rr.Body.String())
			}
			var row struct {
				UpdatedBy *string `json:"updatedBy"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &row); err != nil {
				t.Fatalf("%s: json.Unmarshal() error = %v: %s", name, err, rr.Body.String())
			}
			if row.UpdatedBy == nil || *row.UpdatedBy != want {
				t.Errorf("%s: updatedBy = %v, want %q (body %s)", name, row.UpdatedBy, want, rr.Body.String())
			}
		}
	})
}
