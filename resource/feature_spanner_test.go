package resource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-chi/chi/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/spxscan"
)

// featureDatabase creates a database on the shared emulator migrated with the feature
// flag tables, dropped when the test ends, and the resource client over it.
func featureDatabase(t *testing.T, name string) (*initiator.SpannerDB, Client) {
	t.Helper()

	container := spannerEmulator(t)
	ctx := context.Background()
	db, err := container.CreateDatabase(ctx, name)
	if err != nil {
		t.Fatalf("initiator.SpannerContainer.CreateDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropDatabase(context.Background()); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.MigrateUp("file://testdata/features/schema"); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}

	return db, NewSpannerClient(db.Client)
}

// featureRows reads the flags table as the tests compare it.
func featureRows(t *testing.T, db *initiator.SpannerDB) []FeatureFlag {
	t.Helper()

	var rows []FeatureFlag
	if err := spxscan.Select(t.Context(), db.Single(), &rows, spanner.Statement{SQL: "SELECT Name, Description, Enabled, UpdatedAt, UpdatedBy FROM FeatureFlags ORDER BY Name"}); err != nil {
		t.Fatalf("spxscan.Select() error = %v", err)
	}

	return rows
}

// featureChange is an audit row as the tests compare it.
type featureChange struct {
	Name      string    `spanner:"Name"`
	ChangedAt time.Time `spanner:"ChangedAt"`
	Enabled   bool      `spanner:"Enabled"`
	ChangedBy string    `spanner:"ChangedBy"`
}

// featureChanges reads the audit table in writing order.
func featureChanges(t *testing.T, db *initiator.SpannerDB) []featureChange {
	t.Helper()

	var rows []featureChange
	if err := spxscan.Select(t.Context(), db.Single(), &rows, spanner.Statement{SQL: "SELECT Name, ChangedAt, Enabled, ChangedBy FROM FeatureFlagChanges ORDER BY ChangedAt"}); err != nil {
		t.Fatalf("spxscan.Select() error = %v", err)
	}

	return rows
}

// ignoreUpdatedAt drops the commit timestamps from a comparison.
var ignoreUpdatedAt = cmpopts.IgnoreFields(FeatureFlag{}, "UpdatedAt")

// TestMigrateFeatures pins the deploy step over the emulator, as a sequence of
// migrations on one table: a declared flag the table lacks is inserted disabled, a held
// flag keeps its value and takes the declared description, an undeclared flag is deleted,
// and the audit rows outlive it all.
func TestMigrateFeatures(t *testing.T) {
	t.Parallel()

	db, client := featureDatabase(t, "migrate-features")
	ctx := t.Context()
	process := ProcessEvent("MigrateFeatures")

	steps := []struct {
		name     string
		declared []FeatureDeclaration
		// before runs ahead of the migration: what the world did meanwhile.
		before      func(t *testing.T)
		wantRows    []FeatureFlag
		wantChanges int
		wantErr     string
	}{
		{
			name:     "two declared flags are inserted disabled with their descriptions",
			declared: []FeatureDeclaration{{Name: "debriefs", Description: "Mission debriefs.", Constant: "Debriefs"}, {Name: "beacons", Description: "Beacons.", Constant: "Beacons"}},
			wantRows: []FeatureFlag{
				{Name: "beacons", Description: "Beacons.", UpdatedBy: process},
				{Name: "debriefs", Description: "Mission debriefs.", UpdatedBy: process},
			},
		},
		{
			name:     "a flipped flag keeps its value and takes the new description; the undeclared flag goes, its audit stays",
			declared: []FeatureDeclaration{{Name: "debriefs", Description: "Mission debriefs, reworded.", Constant: "Debriefs"}, {Name: "cargo", Description: "Cargo.", Constant: "Cargo"}},
			before: func(t *testing.T) {
				t.Helper()
				if err := SetFeatureEnabled(ctx, client, "debriefs", true, "harbormaster (test)"); err != nil {
					t.Fatalf("SetFeatureEnabled() error = %v", err)
				}
			},
			wantRows: []FeatureFlag{
				{Name: "cargo", Description: "Cargo.", UpdatedBy: process},
				{Name: "debriefs", Description: "Mission debriefs, reworded.", Enabled: true, UpdatedBy: "harbormaster (test)"},
			},
			wantChanges: 1,
		},
		{
			name:        "a migration declaring nothing empties the table and leaves the audit",
			declared:    nil,
			wantRows:    []FeatureFlag{},
			wantChanges: 1,
		},
		{
			name:        "a malformed declaration is refused before anything is written",
			declared:    []FeatureDeclaration{{Name: "Bad", Constant: "Bad"}},
			wantRows:    []FeatureFlag{},
			wantChanges: 1,
			wantErr:     "is not a feature name",
		},
	}

	// The steps are one migration history on one table, so they run in order, each
	// named in what it reports.
	for _, step := range steps {
		if step.before != nil {
			step.before(t)
		}
		err := MigrateFeatures(ctx, client, step.declared)
		if step.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), step.wantErr) {
				t.Fatalf("%s: MigrateFeatures() error = %v, want containing %q", step.name, err, step.wantErr)
			}
		} else if err != nil {
			t.Fatalf("%s: MigrateFeatures() error = %v", step.name, err)
		}

		if diff := cmp.Diff(step.wantRows, featureRows(t, db), ignoreUpdatedAt, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("%s: FeatureFlags mismatch (-want +got):\n%s", step.name, diff)
		}
		if got := len(featureChanges(t, db)); got != step.wantChanges {
			t.Errorf("%s: FeatureFlagChanges rows = %d, want %d", step.name, got, step.wantChanges)
		}
		for _, row := range featureRows(t, db) {
			if row.UpdatedAt.IsZero() {
				t.Errorf("%s: flag %s carries no commit timestamp", step.name, row.Name)
			}
		}
	}
}

// fakeSignals is a SignalSubscriber and Signaler for the tests: the subscription's
// onSignal is kept and run on a Signal of its kind, and the signals are remembered.
type fakeSignals struct {
	mu         sync.Mutex
	onSignal   func()
	subscribed []SignalKind
	signals    []SignalKind
	err        error
}

func (f *fakeSignals) Subscribe(kind SignalKind, onSignal func()) (func(), error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribed = append(f.subscribed, kind)
	f.onSignal = onSignal

	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.onSignal = nil
	}, nil
}

func (f *fakeSignals) Signal(_ context.Context, kind SignalKind) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	f.signals = append(f.signals, kind)
	onSignal := f.onSignal
	f.mu.Unlock()
	if onSignal != nil && kind == KindFeatures {
		onSignal()
	}

	return nil
}

// Signals returns the kinds signaled so far.
func (f *fakeSignals) Signals() []SignalKind {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]SignalKind(nil), f.signals...)
}

// eventually polls until the condition holds or the deadline passes.
func eventually(t *testing.T, within time.Duration, condition func() bool) bool {
	t.Helper()

	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}

	return condition()
}

// TestFeatureSet_Follow pins how a FeatureSet stays current: a signal of the features
// kind rereads the table at once, and the backstop rereads it without one.
func TestFeatureSet_Follow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// signal says whether the test signals the features kind after the flip.
		signal   bool
		backstop time.Duration
		// within bounds how soon the copy must show the flip.
		within time.Duration
	}{
		{name: "a signal of the features kind rereads the table at once", signal: true, backstop: time.Hour, within: 2 * time.Second},
		{name: "the backstop rereads the table without a signal", backstop: 50 * time.Millisecond, within: 2 * time.Second},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, client := featureDatabase(t, "follow-"+strconv.Itoa(i))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if err := MigrateFeatures(ctx, client, []FeatureDeclaration{{Name: "debriefs", Description: "Debriefs.", Constant: "Debriefs"}}); err != nil {
				t.Fatalf("MigrateFeatures() error = %v", err)
			}
			features, err := LoadFeatures(ctx, client, WithFeatureBackstop(tt.backstop))
			if err != nil {
				t.Fatalf("LoadFeatures() error = %v", err)
			}
			if features.Enabled("debriefs") {
				t.Fatal("a freshly migrated flag is on; it is inserted disabled")
			}

			signals := &fakeSignals{}
			if err := features.Follow(ctx, signals); err != nil {
				t.Fatalf("Follow() error = %v", err)
			}
			if diff := cmp.Diff([]SignalKind{KindFeatures}, signals.subscribed); diff != "" {
				t.Errorf("subscribed kinds mismatch (-want +got):\n%s", diff)
			}

			if err := SetFeatureEnabled(ctx, client, "debriefs", true, "harbormaster (test)"); err != nil {
				t.Fatalf("SetFeatureEnabled() error = %v", err)
			}
			if tt.signal {
				if err := signals.Signal(ctx, KindFeatures); err != nil {
					t.Fatalf("Signal() error = %v", err)
				}
			}
			if !eventually(t, tt.within, func() bool { return features.Enabled("debriefs") }) {
				t.Fatalf("the copy did not show the flip within %s", tt.within)
			}
			flag, ok := features.Flag("debriefs")
			if !ok || flag.UpdatedBy != "harbormaster (test)" || flag.UpdatedAt.IsZero() {
				t.Errorf("Flag() = %+v, %v; want the flipped row with its commit timestamp and principal", flag, ok)
			}
		})
	}
}

// featureTestApp is the FeatureApp the handler tests hand the library: a database
// client, scripted permissions, and the FeatureSet.
type featureTestApp struct {
	client   Client
	perms    UserPermissions
	features *FeatureSet
}

func (a *featureTestApp) Validator() ValidatorFunc { return nil }

func (a *featureTestApp) UserPermissions(*http.Request) UserPermissions { return a.perms }

func (a *featureTestApp) ResourceClient() Client { return a.client }

func (a *featureTestApp) CursorKey() *CursorKey { return nil }

func (a *featureTestApp) FeatureSet() *FeatureSet { return a.features }

// featureGrants scripts a user holding the given permissions on the flags' resource
// and method.
func featureGrants(perms ...accesstypes.Permission) *fakeUserPermissions {
	granted := make(map[accesstypes.Permission][]accesstypes.Resource, len(perms))
	for _, perm := range perms {
		granted[perm] = []accesstypes.Resource{
			FeatureFlagsResource, SetFeatureMethod,
			"FeatureFlags.description", "FeatureFlags.enabled", "FeatureFlags.updatedAt", "FeatureFlags.updatedBy",
		}
	}

	return &fakeUserPermissions{granted: granted}
}

// featureRouter mounts the three flag routes the way the generated routes do, with the
// route parameter captured.
func featureRouter(a *featureTestApp, signals Signaler) http.Handler {
	r := chi.NewRouter()
	r.Use(httpio.WithParams)
	r.Get("/api/"+FeatureFlagsRoute, FeatureFlagsHandler(a, nil))
	r.Get("/api/"+FeatureFlagsRoute+"/{"+string(FeatureFlagNameParam)+"}", FeatureFlagHandler(a, nil))
	r.Post("/api/"+SetFeatureRoute, SetFeatureHandler(a, nil, signals))

	return r
}

// observingSignaler is a signaler that tells a test of each signal as it is sent, before
// the signal answers, with the context it is sent under.
type observingSignaler func(ctx context.Context, kind SignalKind)

func (o observingSignaler) Signal(ctx context.Context, kind SignalKind) error {
	o(ctx, kind)

	return nil
}

// endingClient is a client whose reads end the request once a test has armed it: the
// request's context is canceled as a read begins, as it is when the page that asked
// closes while the server reads.
type endingClient struct {
	Client
	end atomic.Pointer[context.CancelFunc]
}

func (c *endingClient) ReadOnlyTransaction() ReadOnlyTransactionCloser {
	if end := c.end.Load(); end != nil {
		(*end)()
	}

	return c.Client.ReadOnlyTransaction()
}

// TestSetFeatureHandler_reloadsBeforeItSignals pins the order of a flip's last two
// steps, on the Spanner emulator: this instance's copy holds the flip before the other
// instances are told, so a request that comes while the signal is being sent, which
// waits for the channel, is answered from the flip; and the signal is sent whatever
// became of the reload and of the request. A request that ends while this instance
// reloads, the flip committed, still signals, under a context the request's end did not
// reach and with the signal's own timeout on it.
func TestSetFeatureHandler_reloadsBeforeItSignals(t *testing.T) {
	t.Parallel()

	// clientClosedRequest is the status of a request whose client went away.
	const clientClosedRequest = 499
	tests := []struct {
		name string
		// reloadFails says the database fails the reads of the copy once it is loaded.
		reloadFails bool
		// requestEnds says the request's context is canceled as the reload's read begins.
		requestEnds bool
		wantStatus  int
		// wantOnAtSignal is what this instance's copy says of the flag as the signal is sent.
		wantOnAtSignal bool
	}{
		{name: "the copy holds the flip as the signal is sent", wantStatus: http.StatusOK, wantOnAtSignal: true},
		{name: "a reload that fails still signals the other instances", reloadFails: true, wantStatus: http.StatusInternalServerError, wantOnAtSignal: false},
		{name: "a request that ends during the reload still signals the other instances", requestEnds: true, wantStatus: clientClosedRequest, wantOnAtSignal: false},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, client := featureDatabase(t, "set-feature-order-"+strconv.Itoa(i))
			ctx := sessionCtx("harbormaster", nil)
			if err := MigrateFeatures(ctx, client, []FeatureDeclaration{{Name: "debriefs", Description: "Debriefs.", Constant: "Debriefs"}}); err != nil {
				t.Fatalf("MigrateFeatures() error = %v", err)
			}
			failing := &failingClient{Client: client}
			ending := &endingClient{Client: failing}
			features, err := LoadFeatures(ctx, ending)
			if err != nil {
				t.Fatalf("LoadFeatures() error = %v", err)
			}
			failing.fail.Store(tt.reloadFails)
			request, endRequest := context.WithCancel(ctx)
			defer endRequest()
			if tt.requestEnds {
				ending.end.Store(&endRequest)
			}
			var (
				signaled     []SignalKind
				onAtSignal   bool
				signalEnded  error
				signalWithin time.Duration
				signalBound  bool
			)
			signals := observingSignaler(func(ctx context.Context, kind SignalKind) {
				signaled = append(signaled, kind)
				onAtSignal = features.Enabled("debriefs")
				signalEnded = ctx.Err()
				var deadline time.Time
				deadline, signalBound = ctx.Deadline()
				signalWithin = time.Until(deadline)
			})
			handler := featureRouter(&featureTestApp{client: client, perms: featureGrants(accesstypes.Execute), features: features}, signals)

			req := httptest.NewRequestWithContext(request, http.MethodPost, "/api/"+SetFeatureRoute, strings.NewReader(`{"name":"debriefs","enabled":true}`))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if got := request.Err() != nil; got != tt.requestEnds {
				t.Fatalf("the request ended under the handler = %v, want %v", got, tt.requestEnds)
			}
			if !slices.Equal(signaled, []SignalKind{KindFeatures}) {
				t.Fatalf("signals sent = %v, want the features kind once", signaled)
			}
			if onAtSignal != tt.wantOnAtSignal {
				t.Errorf("as the flip's signal was sent, this instance's copy said the flag on = %v, want %v", onAtSignal, tt.wantOnAtSignal)
			}
			if signalEnded != nil {
				t.Errorf("the flip's signal was sent under a context that had ended (%v): the other instances are not told of a flip that is committed", signalEnded)
			}
			if !signalBound || signalWithin <= 0 || signalWithin > FeatureSignalTimeout {
				t.Errorf("the flip's signal was sent with %s left (deadline set: %v), want the signal's own timeout of %s", signalWithin, signalBound, FeatureSignalTimeout)
			}
		})
	}
}

// TestSetFeatureHandler pins the flip over the emulator: the row and the audit row
// written in one transaction with the commit timestamp and the principal, the features
// kind signaled, the copy reloaded and answered from; an unknown name 404; a dry run
// that writes and signals nothing; a missing grant 403.
func TestSetFeatureHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		perms  UserPermissions
		body   string
		dryRun bool
		// warning is the X-Warning header the request carries, none where it is empty.
		warning     string
		wantStatus  int
		wantEnabled bool
		wantSignals []SignalKind
		wantChanges int
		wantBody    func(t *testing.T, body []byte)
	}{
		{
			name:        "a flip writes the row and the audit row, signals, reloads and answers the flag",
			perms:       featureGrants(accesstypes.Execute),
			body:        `{"name":"debriefs","enabled":true}`,
			wantStatus:  http.StatusOK,
			wantEnabled: true,
			wantSignals: []SignalKind{KindFeatures},
			wantChanges: 1,
			wantBody: func(t *testing.T, body []byte) {
				t.Helper()
				var got SetFeatureResult
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("json.Unmarshal() error = %v: %s", err, body)
				}
				if got.Name != "debriefs" || !got.Enabled || got.UpdatedAt.IsZero() {
					t.Errorf("answer = %+v, want the flipped flag with its commit timestamp", got)
				}
			},
		},
		{
			name:       "an unknown name is not found and writes nothing",
			perms:      featureGrants(accesstypes.Execute),
			body:       `{"name":"unknown","enabled":true}`,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "a dry run refuses as the real call would, then writes and signals nothing",
			perms:      featureGrants(accesstypes.Execute),
			body:       `{"name":"debriefs","enabled":true}`,
			dryRun:     true,
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				t.Helper()
				if strings.TrimSpace(string(body)) != "" {
					t.Errorf("a dry run answers no body, got %s", body)
				}
			},
		},
		{
			name:       "a dry run of an unknown name is not found",
			perms:      featureGrants(accesstypes.Execute),
			body:       `{"name":"unknown","enabled":true}`,
			dryRun:     true,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "without Execute the flip is forbidden",
			perms:      featureGrants(accesstypes.List),
			body:       `{"name":"debriefs","enabled":true}`,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "a request that asks for a warning is refused, since SetFeature declares none, and the flag is not set",
			perms:      featureGrants(accesstypes.Execute),
			body:       `{"name":"debriefs","enabled":true}`,
			warning:    "true",
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				t.Helper()
				if want := "SetFeature declares no warning: send the call without the X-Warning header"; !strings.Contains(string(body), want) {
					t.Errorf("the refusal = %s, want it to say %q", body, want)
				}
			},
		},
		{
			name:       "an X-Warning header that says anything but true is refused, and the flag is not set",
			perms:      featureGrants(accesstypes.Execute),
			body:       `{"name":"debriefs","enabled":true}`,
			warning:    "1",
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				t.Helper()
				if want := "the X-Warning header takes the value true, or is left off the request"; !strings.Contains(string(body), want) {
					t.Errorf("the refusal = %s, want it to say %q", body, want)
				}
			},
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db, client := featureDatabase(t, "set-feature-"+strconv.Itoa(i))
			ctx := sessionCtx("harbormaster", nil)
			if err := MigrateFeatures(ctx, client, []FeatureDeclaration{{Name: "debriefs", Description: "Debriefs.", Constant: "Debriefs"}}); err != nil {
				t.Fatalf("MigrateFeatures() error = %v", err)
			}
			features, err := LoadFeatures(ctx, client)
			if err != nil {
				t.Fatalf("LoadFeatures() error = %v", err)
			}
			signals := &fakeSignals{}
			handler := featureRouter(&featureTestApp{client: client, perms: tt.perms, features: features}, signals)

			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/"+SetFeatureRoute, strings.NewReader(tt.body))
			if tt.dryRun {
				req.Header.Set(DryRunHeader, "true")
			}
			if tt.warning != "" {
				req.Header.Set(WarningHeader, tt.warning)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantBody != nil {
				tt.wantBody(t, rr.Body.Bytes())
			}
			if got := features.Enabled("debriefs"); got != tt.wantEnabled {
				t.Errorf("Enabled() = %v after the call, want %v", got, tt.wantEnabled)
			}
			rows := featureRows(t, db)
			if len(rows) != 1 || rows[0].Enabled != tt.wantEnabled {
				t.Errorf("FeatureFlags = %+v, want the one flag with Enabled %v", rows, tt.wantEnabled)
			}
			if tt.wantEnabled && rows[0].UpdatedBy != UserEvent(ctx) {
				t.Errorf("UpdatedBy = %q, want the principal %q", rows[0].UpdatedBy, UserEvent(ctx))
			}
			changes := featureChanges(t, db)
			if len(changes) != tt.wantChanges {
				t.Errorf("FeatureFlagChanges = %+v, want %d rows", changes, tt.wantChanges)
			}
			for _, change := range changes {
				if change.Name != "debriefs" || !change.Enabled || change.ChangedBy != UserEvent(ctx) || change.ChangedAt.IsZero() {
					t.Errorf("audit row = %+v, want the flip with its commit timestamp and principal", change)
				}
			}
			if diff := cmp.Diff(tt.wantSignals, signals.Signals(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("signals mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestFeatureFlagHandlers pins the flags' list and read routes over the emulator: the
// rows by wire name to a List or Read grant, the sort by name, an unknown name 404, a
// missing grant 403.
func TestFeatureFlagHandlers(t *testing.T) {
	t.Parallel()

	db, client := featureDatabase(t, "feature-flag-routes")
	ctx := sessionCtx("harbormaster", nil)
	if err := MigrateFeatures(ctx, client, []FeatureDeclaration{
		{Name: "debriefs", Description: "Debriefs.", Constant: "Debriefs"},
		{Name: "beacons", Description: "Beacons.", Constant: "Beacons"},
	}); err != nil {
		t.Fatalf("MigrateFeatures() error = %v", err)
	}
	if err := SetFeatureEnabled(ctx, client, "beacons", true, "harbormaster (test)"); err != nil {
		t.Fatalf("SetFeatureEnabled() error = %v", err)
	}
	features, err := LoadFeatures(ctx, client)
	if err != nil {
		t.Fatalf("LoadFeatures() error = %v", err)
	}
	_ = db

	tests := []struct {
		name       string
		perms      UserPermissions
		target     string
		wantStatus int
		// wantNames are the names in the list's order; wantFields the wire names each
		// row carries.
		wantNames  []string
		wantFields []string
	}{
		{name: "the list, by name, every field to a List grant", perms: featureGrants(accesstypes.List), target: "/api/" + FeatureFlagsRoute, wantStatus: http.StatusOK, wantNames: []string{"beacons", "debriefs"}, wantFields: []string{"description", "enabled", "name", "updatedAt", "updatedBy"}},
		{name: "the list with the columns asked for", perms: featureGrants(accesstypes.List), target: "/api/" + FeatureFlagsRoute + "?columns=name,enabled", wantStatus: http.StatusOK, wantNames: []string{"beacons", "debriefs"}, wantFields: []string{"enabled", "name"}},
		{name: "the list without a List grant is forbidden", perms: featureGrants(accesstypes.Read), target: "/api/" + FeatureFlagsRoute, wantStatus: http.StatusForbidden},
		{name: "one flag to a Read grant", perms: featureGrants(accesstypes.Read), target: "/api/" + FeatureFlagsRoute + "/beacons", wantStatus: http.StatusOK, wantNames: []string{"beacons"}, wantFields: []string{"description", "enabled", "name", "updatedAt", "updatedBy"}},
		{name: "an unknown flag is not found", perms: featureGrants(accesstypes.Read), target: "/api/" + FeatureFlagsRoute + "/unknown", wantStatus: http.StatusNotFound},
		{name: "one flag without a Read grant is forbidden", perms: featureGrants(accesstypes.List), target: "/api/" + FeatureFlagsRoute + "/beacons", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := featureRouter(&featureTestApp{client: client, perms: tt.perms, features: features}, &fakeSignals{})
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, tt.target, http.NoBody))
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				return
			}

			var rows []map[string]any
			if strings.HasPrefix(strings.TrimSpace(rr.Body.String()), "[") {
				if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
					t.Fatalf("json.Unmarshal() error = %v: %s", err, rr.Body.String())
				}
			} else {
				var row map[string]any
				if err := json.Unmarshal(rr.Body.Bytes(), &row); err != nil {
					t.Fatalf("json.Unmarshal() error = %v: %s", err, rr.Body.String())
				}
				rows = []map[string]any{row}
			}
			names := make([]string, 0, len(rows))
			for _, row := range rows {
				name, _ := row["name"].(string)
				names = append(names, name)
				fields := make([]string, 0, len(row))
				for field := range row {
					fields = append(fields, field)
				}
				if diff := cmp.Diff(tt.wantFields, sortedStrings(fields)); diff != "" {
					t.Errorf("row %s fields mismatch (-want +got):\n%s", name, diff)
				}
			}
			if diff := cmp.Diff(tt.wantNames, names); diff != "" {
				t.Errorf("names mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// sortedStrings returns the strings in order.
func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}

	return out
}
