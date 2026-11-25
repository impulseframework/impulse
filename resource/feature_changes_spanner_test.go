package resource

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-chi/chi/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/spxscan"
)

// featureHistoryRouter mounts the flags' list, a flag's read and a flag's history the way
// the generated routes do, with the route parameter captured.
func featureHistoryRouter(a *featureTestApp) http.Handler {
	return featureHistoryRouterOver(a, nil)
}

// featureHistoryRouterOver is featureHistoryRouter with the collection the handlers
// render a grant's condition from.
func featureHistoryRouterOver(a *featureTestApp, collection *GeneratedCollection) http.Handler {
	r := chi.NewRouter()
	r.Use(httpio.WithParams)
	r.Get("/api/"+FeatureFlagsRoute, FeatureFlagsHandler(a, collection))
	r.Get("/api/"+FeatureFlagsRoute+"/{"+string(FeatureFlagNameParam)+"}", FeatureFlagHandler(a, collection))
	r.Get("/api/"+FeatureFlagsRoute+"/{"+string(FeatureFlagNameParam)+"}/"+FeatureFlagChangesRoute, FeatureFlagChangesHandler(a, collection))

	return r
}

// featureConditionCollection is a collection whose FeatureFlags resource declares the
// flag's name as an attribute, so that a grant's condition can tell one flag from
// another. No application's collection declares it today (the generator registers the
// flags with no attribute), so no role file can write such a condition yet; the history
// is held to a condition all the same, since it serves what the flag's own row would.
func featureConditionCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{{
		Name:        FeatureFlagsResource,
		Scope:       accesstypes.GlobalPermissionScope,
		Permissions: []accesstypes.Permission{accesstypes.List, accesstypes.Read},
		Attributes:  []AttributeData{{Name: "name", Column: "Name", Type: AttributeTypeString}},
	}}})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// featureFieldGrants scripts a user holding List on the flags' resource and on the named
// fields of a flag alone.
func featureFieldGrants(fields ...string) *fakeUserPermissions {
	resources := []accesstypes.Resource{FeatureFlagsResource}
	for _, field := range fields {
		resources = append(resources, accesstypes.Resource("FeatureFlags."+field))
	}

	return &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{accesstypes.List: resources}}
}

// ignoreChangedAt drops the commit timestamps from a comparison of a flag's history.
var ignoreChangedAt = cmpopts.IgnoreFields(FeatureFlagChange{}, "ChangedAt")

// TestFeatureFlagChangesHandler pins a flag's history over the emulator: its flips
// newest first, each with who made it by the name they sign in under, to a List grant on
// FeatureFlags and to no other grant; an empty list for a flag nobody has flipped; an
// unknown flag not found. Each flip's row keeps who made it in the columns a change
// record uses, those the value fills and no other. It pins what the list and the read
// serve beside it: who last flipped each flag, a person by name, a process by its name,
// a person in an RPC method with the method, and nobody for a flag only the deploy step
// has written.
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
		{Name: "autopilot", Description: "Autopilot steers a ship between waypoints.", Constant: "Autopilot"},
	}); err != nil {
		t.Fatalf("MigrateFeatures() error = %v", err)
	}
	// Three flips of one flag, in order: a process's, a person's, and one made in a
	// session acting as someone else, which names the person acting first.
	flips := []struct {
		enabled bool
		source  eventsource.EventSource
	}{
		{enabled: true, source: eventsource.ProcessEvent("bootstrap")},
		{enabled: false, source: eventsource.UserEvent(gale)},
		{enabled: true, source: eventsource.UserEvent(behindRey)},
	}
	for _, flip := range flips {
		if err := SetFeatureEnabled(gale, client, "cargo_manifest", flip.enabled, flip.source); err != nil {
			t.Fatalf("SetFeatureEnabled() error = %v", err)
		}
	}
	// A flag only a process has flipped: its row is a process's, as an unflipped flag's is.
	if err := SetFeatureEnabled(gale, client, "telemetry", true, eventsource.ProcessEvent("bootstrap")); err != nil {
		t.Fatalf("SetFeatureEnabled() error = %v", err)
	}
	// A flag an RPC method of the application flipped for a person: the row names the
	// method too.
	inStartPilot := eventsource.UserEvent(WithRPC(gale, "StartPilot"))
	if err := SetFeatureEnabled(gale, client, "autopilot", true, inStartPilot); err != nil {
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
				{Enabled: true, ChangedBy: "morgan, acting as rey"},
				{Enabled: false, ChangedBy: "gale"},
				{Enabled: true, ChangedBy: "the process bootstrap"},
			}},
		},
		{
			name:       "a flip made in an RPC method names the method",
			perms:      featureGrants(accesstypes.List),
			target:     "/api/" + FeatureFlagsRoute + "/autopilot/" + FeatureFlagChangesRoute,
			wantStatus: http.StatusOK,
			want:       FeatureFlagChangesResponse{Changes: []FeatureFlagChange{{Enabled: true, ChangedBy: "gale, through the RPC StartPilot"}}},
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
		// A grant on who last flipped a flag that holds for one flag alone: the cell is
		// masked on every other flag's row.
		whoOfDebriefsAlone := featureFieldGrants("description", "enabled", "updatedAt")
		whoOfDebriefsAlone.conditions = map[accesstypes.Resource]string{"FeatureFlags.updatedBy": "name = 'debriefs'"}
		// A grant to list the flags that reaches one flag alone: the condition stands on
		// the resource and on every field of it, the way one conditional role grants.
		debriefsAlone := featureFieldGrants(every...)
		debriefsAlone.conditions = map[accesstypes.Resource]string{FeatureFlagsResource: "name = 'debriefs'"}
		for _, field := range every {
			debriefsAlone.conditions[accesstypes.Resource("FeatureFlags."+field)] = "name = 'debriefs'"
		}
		fieldTests := []struct {
			name       string
			perms      UserPermissions
			target     string
			wantStatus int
			wantKeys   []string
		}{
			{name: "every field of the flag: when, what and who", perms: featureFieldGrants(every...), target: history, wantStatus: http.StatusOK, wantKeys: []string{"changedAt", "changedBy", "enabled"}},
			{name: "a grant without who last flipped the flag leaves who out of every flip", perms: featureFieldGrants("description", "enabled", "updatedAt"), target: history, wantStatus: http.StatusOK, wantKeys: []string{"changedAt", "enabled"}},
			{name: "a grant without when leaves when out", perms: featureFieldGrants("enabled", "updatedBy"), target: history, wantStatus: http.StatusOK, wantKeys: []string{"changedBy", "enabled"}},
			{name: "a grant whose condition masks who last flipped this flag leaves who out", perms: whoOfDebriefsAlone, target: history, wantStatus: http.StatusOK, wantKeys: []string{"changedAt", "enabled"}},
			{name: "a grant that shows none of the three is refused: the answer would say only how often the flag was flipped", perms: featureFieldGrants("description"), target: history, wantStatus: http.StatusForbidden},
			{name: "a grant on the flag's name alone is refused the same way", perms: featureFieldGrants(), target: history, wantStatus: http.StatusForbidden},
			{name: "a grant to list the flags that does not reach this flag finds no such flag", perms: debriefsAlone, target: history, wantStatus: http.StatusNotFound},
			{name: "columns asked as of the list narrow nothing: the history is the flag's whole", perms: featureFieldGrants(every...), target: history + "?columns=name", wantStatus: http.StatusOK, wantKeys: []string{"changedAt", "changedBy", "enabled"}},
		}
		for _, tt := range fieldTests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				handler := featureHistoryRouterOver(&featureTestApp{client: client, perms: tt.perms, features: features}, featureConditionCollection(t))
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, httptest.NewRequestWithContext(gale, http.MethodGet, tt.target, http.NoBody))
				if rr.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d (body %s)", rr.Code, tt.wantStatus, rr.Body.String())
				}
				if tt.wantStatus != http.StatusOK {
					if strings.Contains(rr.Body.String(), `"changes"`) {
						t.Errorf("a refusal carries flips: %s", rr.Body.String())
					}
					if tt.wantStatus == http.StatusForbidden {
						// The person is told one sentence, with no account's id, no table's
						// name and no column's.
						var refusal struct {
							Message string `json:"message"`
						}
						if err := json.Unmarshal(rr.Body.Bytes(), &refusal); err != nil {
							t.Fatalf("json.Unmarshal() error = %v: %s", err, rr.Body.String())
						}
						if want := "You may not see this flag's history here."; refusal.Message != want {
							t.Errorf("the refusal says %q, want %q", refusal.Message, want)
						}
					}

					return
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

	t.Run("each flip's row keeps who made it in the columns the value fills, and no other", func(t *testing.T) {
		t.Parallel()

		var rows []struct {
			Name string `spanner:"Name"`
			eventSourceColumns
		}
		stmt := spanner.Statement{SQL: "SELECT Name, " + strings.Join(eventsource.ColumnNames(), ", ") + " FROM FeatureFlagChanges WHERE Name IN UNNEST(@names) ORDER BY ChangedAt"}
		stmt.Params = map[string]any{"names": []string{"cargo_manifest", "autopilot"}}
		txn := client.ReadOnlyTransaction()
		defer txn.Close()
		if err := spxscan.Select(t.Context(), txn.SpannerReadOnlyTransaction(), &rows, stmt); err != nil {
			t.Fatalf("spxscan.Select() error = %v", err)
		}
		sources := []eventsource.EventSource{flips[0].source, flips[1].source, flips[2].source, inStartPilot}
		if len(rows) != len(sources) {
			t.Fatalf("FeatureFlagChanges holds %d rows of the two flags, want %d", len(rows), len(sources))
		}
		for i, source := range sources {
			if diff := cmp.Diff(whoColumnsOf(source), rows[i].eventSourceColumns); diff != "" {
				t.Errorf("row %d (%s) mismatch (-want +got):\n%s", i, source, diff)
			}
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
		want := []FeatureFlagChange{{Enabled: true, ChangedBy: "morgan, acting as rey"}, {Enabled: false, ChangedBy: "gale"}}
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
		// When each was last flipped: the time of its newest change record, and null for
		// the flag nobody has flipped, whose row holds only when it was declared.
		var stamps []struct {
			Name      string     `json:"name"`
			UpdatedAt *time.Time `json:"updatedAt"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &stamps); err != nil {
			t.Fatalf("json.Unmarshal() error = %v: %s", err, rr.Body.String())
		}
		txn := client.ReadOnlyTransaction()
		defer txn.Close()
		for _, stamp := range stamps {
			newest, _, err := readFeatureFlagChanges(t.Context(), txn, client.DBType(), Feature(stamp.Name), 1)
			if err != nil {
				t.Fatalf("readFeatureFlagChanges() error = %v", err)
			}
			switch {
			case len(newest) == 0 && stamp.UpdatedAt != nil:
				t.Errorf("%s: updatedAt = %v, want null: nobody has flipped it", stamp.Name, stamp.UpdatedAt)
			case len(newest) == 1 && (stamp.UpdatedAt == nil || !stamp.UpdatedAt.Equal(newest[0].ChangedAt)):
				t.Errorf("%s: updatedAt = %v, want the time of its newest flip, %v", stamp.Name, stamp.UpdatedAt, newest[0].ChangedAt)
			}
		}
		if !strings.Contains(rr.Body.String(), `"updatedAt":null`) {
			t.Errorf("the flag nobody has flipped does not serve a null updatedAt: %s", rr.Body.String())
		}
		want := []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Enabled     bool   `json:"enabled"`
			UpdatedBy   string `json:"updatedBy"`
		}{
			{Name: "autopilot", Description: "Autopilot steers a ship between waypoints.", Enabled: true, UpdatedBy: "gale, through the RPC StartPilot"},
			{Name: "cargo_manifest", Description: "Cargo manifest shows a ship's cargo bays.", Enabled: true, UpdatedBy: "morgan, acting as rey"},
			// Nobody has flipped it: the deploy step that declared it is no one's flip.
			{Name: "debriefs", Description: "Debriefs lets a crew write debriefs.", UpdatedBy: ""},
			// A process flipped it, which the row says as the history does.
			{Name: "telemetry", Description: "Telemetry reports a ship's position.", Enabled: true, UpdatedBy: "the process bootstrap"},
		}
		if diff := cmp.Diff(want, rows); diff != "" {
			t.Errorf("list mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("one flag read by name serves who last flipped it the same way", func(t *testing.T) {
		t.Parallel()

		handler := featureHistoryRouter(&featureTestApp{client: client, perms: featureGrants(accesstypes.Read), features: features})
		for name, want := range map[string]string{"autopilot": "gale, through the RPC StartPilot", "cargo_manifest": "morgan, acting as rey", "debriefs": "", "telemetry": "the process bootstrap"} {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequestWithContext(gale, http.MethodGet, "/api/"+FeatureFlagsRoute+"/"+name, http.NoBody))
			if rr.Code != http.StatusOK {
				t.Fatalf("%s: status = %d, want %d (body %s)", name, rr.Code, http.StatusOK, rr.Body.String())
			}
			var row struct {
				UpdatedBy *string    `json:"updatedBy"`
				UpdatedAt *time.Time `json:"updatedAt"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &row); err != nil {
				t.Fatalf("%s: json.Unmarshal() error = %v: %s", name, err, rr.Body.String())
			}
			if row.UpdatedBy == nil || *row.UpdatedBy != want {
				t.Errorf("%s: updatedBy = %v, want %q (body %s)", name, row.UpdatedBy, want, rr.Body.String())
			}
			if flipped := want != ""; flipped != (row.UpdatedAt != nil) {
				t.Errorf("%s: updatedAt = %v, want a time only for a flag somebody has flipped (body %s)", name, row.UpdatedAt, rr.Body.String())
			}
		}
	})

	t.Run("the list serves either fact of the last flip to a grant that holds that one alone", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			name    string
			field   string
			without string
		}{
			{name: "when, to a grant without who", field: "updatedAt", without: "updatedBy"},
			{name: "who, to a grant without when", field: "updatedBy", without: "updatedAt"},
		} {
			handler := featureHistoryRouter(&featureTestApp{client: client, perms: featureFieldGrants("enabled", tt.field), features: features})
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequestWithContext(gale, http.MethodGet, "/api/"+FeatureFlagsRoute, http.NoBody))
			if rr.Code != http.StatusOK {
				t.Fatalf("%s: status = %d, want %d (body %s)", tt.name, rr.Code, http.StatusOK, rr.Body.String())
			}
			var rows []map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
				t.Fatalf("%s: json.Unmarshal() error = %v: %s", tt.name, err, rr.Body.String())
			}
			for _, row := range rows {
				if _, served := row[tt.without]; served {
					t.Errorf("%s: %v carries %s, which the grant leaves out", tt.name, row["name"], tt.without)
				}
				if row["name"] != "cargo_manifest" {
					continue
				}
				// The flag a person last flipped: the fact the grant holds is the flip's.
				if got, _ := row[tt.field].(string); got == "" {
					t.Errorf("%s: cargo_manifest serves %s = %v, want its last flip's (body %s)", tt.name, tt.field, row[tt.field], rr.Body.String())
				}
			}
		}
	})

	t.Run("a list asked to sort on when a flag was last flipped, or by whom, is refused, and any other sort orders the flags", func(t *testing.T) {
		t.Parallel()

		lists := featureGrants(accesstypes.List)
		sortTests := []struct {
			name       string
			perms      UserPermissions
			query      string
			wantStatus int
			// wantSays is a part of the refusal; wantNames the flags in the order served.
			wantSays  []string
			wantNames []string
		}{
			{name: "no sort asked: by name", perms: lists, wantStatus: http.StatusOK, wantNames: []string{"autopilot", "cargo_manifest", "debriefs", "telemetry"}},
			{name: "the description, last first", perms: lists, query: "?sort=description:desc", wantStatus: http.StatusOK, wantNames: []string{"telemetry", "debriefs", "cargo_manifest", "autopilot"}},
			{name: "whether it is on, then the name", perms: lists, query: "?sort=enabled,name", wantStatus: http.StatusOK, wantNames: []string{"debriefs", "autopilot", "cargo_manifest", "telemetry"}},
			{
				name: "when it was last flipped", perms: lists, query: "?sort=updatedAt", wantStatus: http.StatusBadRequest,
				wantSays: []string{"the flags cannot be sorted on updatedAt", "are worked out from the flag", "sort on name, description or enabled"},
			},
			{
				name: "who last flipped it, last first", perms: lists, query: "?sort=updatedBy:desc", wantStatus: http.StatusBadRequest,
				wantSays: []string{"the flags cannot be sorted on updatedBy", "are worked out from the flag", "sort on name, description or enabled"},
			},
			{
				name: "the name first and when after it", perms: lists, query: "?sort=name,updatedAt:desc", wantStatus: http.StatusBadRequest,
				wantSays: []string{"the flags cannot be sorted on updatedAt"},
			},
			{
				name: "with the columns narrowed to the name", perms: lists, query: "?columns=name&sort=updatedBy", wantStatus: http.StatusBadRequest,
				wantSays: []string{"the flags cannot be sorted on updatedBy"},
			},
			// What is wrong with a request is said before the caller's grant is asked, as it
			// is of a sort on a field the flags do not have.
			{
				name: "a caller who may not list the flags is told the same", perms: featureGrants(), query: "?sort=updatedAt", wantStatus: http.StatusBadRequest,
				wantSays: []string{"the flags cannot be sorted on updatedAt"},
			},
			{
				name: "as that caller is told of a sort on a field the flags do not have", perms: featureGrants(), query: "?sort=nosuch", wantStatus: http.StatusBadRequest,
				wantSays: []string{"unknown sort field: nosuch"},
			},
			{
				name: "and a caller whose grant leaves out when a flag was last flipped is told it cannot be sorted on, not that they may not see it", perms: featureFieldGrants("enabled"), query: "?sort=updatedAt", wantStatus: http.StatusBadRequest,
				wantSays: []string{"the flags cannot be sorted on updatedAt"},
			},
			{name: "and is refused as one when the sort is one the list takes", perms: featureGrants(), query: "?sort=description", wantStatus: http.StatusForbidden},
		}
		for _, tt := range sortTests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				handler := featureHistoryRouter(&featureTestApp{client: client, perms: tt.perms, features: features})
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, httptest.NewRequestWithContext(gale, http.MethodGet, "/api/"+FeatureFlagsRoute+tt.query, http.NoBody))
				if rr.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d (body %s)", rr.Code, tt.wantStatus, rr.Body.String())
				}
				for _, says := range tt.wantSays {
					if !strings.Contains(rr.Body.String(), says) {
						t.Errorf("the refusal does not say %q: %s", says, rr.Body.String())
					}
				}
				if tt.wantStatus != http.StatusOK {
					if strings.Contains(rr.Body.String(), "cargo_manifest") {
						t.Errorf("a refusal carries flags: %s", rr.Body.String())
					}

					return
				}
				var rows []struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
					t.Fatalf("json.Unmarshal() error = %v: %s", err, rr.Body.String())
				}
				names := make([]string, 0, len(rows))
				for _, row := range rows {
					names = append(names, row.Name)
				}
				if diff := cmp.Diff(tt.wantNames, names); diff != "" {
					t.Errorf("order mismatch (-want +got):\n%s", diff)
				}
			})
		}
	})
}

// TestFeatureFlagLastFlipFollowsHistory pins that the list says of a flag's last flip
// what the flag's history says, whatever wrote the flag's row since: a flag flipped,
// taken out of the declarations and declared again is a new row, written by the deploy
// step, and its history, which outlives it, still holds the flip. The list serves that
// flip's time and person, never the deploy step's.
func TestFeatureFlagLastFlipFollowsHistory(t *testing.T) {
	t.Parallel()

	_, client := featureDatabase(t, "feature-flag-declared-again")
	gale := accountCtx("gale", aliceID, nil)
	beacons := []FeatureDeclaration{{Name: "beacons", Description: "Beacons.", Constant: "Beacons"}}
	if err := MigrateFeatures(gale, client, beacons); err != nil {
		t.Fatalf("MigrateFeatures() error = %v", err)
	}
	if err := SetFeatureEnabled(gale, client, "beacons", true, eventsource.UserEvent(gale)); err != nil {
		t.Fatalf("SetFeatureEnabled() error = %v", err)
	}
	for _, declared := range [][]FeatureDeclaration{nil, beacons} {
		if err := MigrateFeatures(gale, client, declared); err != nil {
			t.Fatalf("MigrateFeatures() error = %v", err)
		}
	}
	features, err := LoadFeatures(gale, client)
	if err != nil {
		t.Fatalf("LoadFeatures() error = %v", err)
	}
	handler := featureHistoryRouter(&featureTestApp{client: client, perms: featureGrants(accesstypes.List), features: features})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequestWithContext(gale, http.MethodGet, "/api/"+FeatureFlagsRoute+"/beacons/"+FeatureFlagChangesRoute, http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("history status = %d, want %d (body %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	var history FeatureFlagChangesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &history); err != nil {
		t.Fatalf("json.Unmarshal() error = %v: %s", err, rr.Body.String())
	}
	if len(history.Changes) != 1 || history.Changes[0].ChangedBy != "gale" {
		t.Fatalf("history = %+v, want the one flip, gale's", history.Changes)
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequestWithContext(gale, http.MethodGet, "/api/"+FeatureFlagsRoute, http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d (body %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	var rows []struct {
		Name      string     `json:"name"`
		Enabled   bool       `json:"enabled"`
		UpdatedAt *time.Time `json:"updatedAt"`
		UpdatedBy string     `json:"updatedBy"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("json.Unmarshal() error = %v: %s", err, rr.Body.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (body %s)", len(rows), rr.Body.String())
	}
	got := rows[0]
	if got.Enabled {
		t.Errorf("the flag declared again is on, want off: the deploy step declares a flag off")
	}
	if got.UpdatedBy != "gale" || got.UpdatedAt == nil || !got.UpdatedAt.Equal(history.Changes[0].ChangedAt) {
		t.Errorf("last flip = %v by %q, want the history's newest: %v by gale", got.UpdatedAt, got.UpdatedBy, history.Changes[0].ChangedAt)
	}
}
