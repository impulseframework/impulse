package integration

import (
	"context"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/signaltest"
	"github.com/impulseframework/impulse/access/postgresstore"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/db"
)

// policyKind is the kind the adapter publishes and follows on the in-memory
// channel, standing in for the policy kind the application's channel declares.
const policyKind = "policy"

// policySignal adapts the shared in-memory channel the way an application
// adapts its own: announce publishes the policy kind; watch subscribes to it
// and blocks until ctx ends, so the subscription's stop runs when Watch
// returns.
func policySignal(channel *signaltest.Channel) access.ChangeSignal {
	return access.ChangeSignalFunc(
		func(ctx context.Context) error {
			return channel.Signal(ctx, policyKind)
		},
		func(ctx context.Context, onChange func()) error {
			stop, err := channel.Subscribe(policyKind, onChange)
			if err != nil {
				return errors.Wrap(err, "signaltest.Channel.Subscribe()")
			}
			defer stop()
			<-ctx.Done()

			return nil
		},
	)
}

// waitFor polls until check passes or the deadline expires.
func waitFor(t *testing.T, timeout time.Duration, msg string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// newTestClient builds a Client instance over the shared typed-store tables;
// a nil signal configures none.
func newTestClient(t *testing.T, database *db.PostgresDatabase, name string, signal access.ChangeSignal, heartbeat time.Duration) *access.Client {
	t.Helper()

	opts := []access.Option{
		access.WithHeartbeatInterval(heartbeat),
		access.WithReloadErrorHandler(func(err error) { t.Logf("%s reload error: %v", name, err) }),
	}
	if signal != nil {
		opts = append(opts, access.WithChangeSignal(signal))
	}
	store, err := postgresstore.New(database.Pool)
	if err != nil {
		t.Fatalf("postgresstore.New() error = %v", err)
	}
	client, err := access.New(store, opts...)
	if err != nil {
		t.Fatalf("access.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})

	return client
}

// Test_Client_policyPropagation proves the full chain on a real store: a
// policy write on one client instance reaches another instance's snapshot
// through the typed policy tables, via each of the two propagation paths —
// the change signal (the adapter over one in-memory channel, the heartbeat
// pinned out of the picture) and the heartbeat alone (poll-only deployments,
// no signal configured).
var (
	tenant1       = accesstypes.DomainScope("tenant1")
	tenant1Policy = accesstypes.DomainPolicyScope("tenant1")
)

func Test_Client_policyPropagation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		withSignal        bool
		heartbeatInterval time.Duration
	}{
		{
			name:              "through the change signal",
			withSignal:        true,
			heartbeatInterval: time.Hour,
		},
		{
			name:              "through the heartbeat alone",
			withSignal:        false,
			heartbeatInterval: 500 * time.Millisecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			database, err := prepareDatabase(ctx, t)
			if err != nil {
				t.Fatalf("prepareDatabase() error = %v", err)
			}

			// One store schema, shared by both client instances — the app's
			// migration applies the store's own DDL.
			schemaStore, err := postgresstore.New(database.Pool)
			if err != nil {
				t.Fatalf("postgresstore.New() error = %v", err)
			}
			for _, stmt := range schemaStore.DDL() {
				if _, err := database.Exec(ctx, stmt); err != nil {
					t.Fatalf("executing DDL: %v", err)
				}
			}

			// One channel stands in for the application's: both instances
			// publish and subscribe on it.
			channel := &signaltest.Channel{}
			var signal access.ChangeSignal
			if tt.withSignal {
				signal = policySignal(channel)
			}
			writer := newTestClient(t, database, "writer", signal, tt.heartbeatInterval)
			reader := newTestClient(t, database, "reader", signal, tt.heartbeatInterval)

			readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := writer.WaitReady(readyCtx); err != nil {
				t.Fatalf("writer WaitReady() error = %v", err)
			}
			if err := reader.WaitReady(readyCtx); err != nil {
				t.Fatalf("reader WaitReady() error = %v", err)
			}
			if tt.withSignal {
				// Both clients' watches must be subscribed before the writes,
				// or the announces could be lost (and the pinned heartbeat
				// would hide them).
				waitFor(t, 15*time.Second, "the clients never subscribed to the policy kind", func() bool {
					return channel.Subscribers(policyKind) == 2
				})
			}

			mgr := writer.UserManager()
			if err := mgr.AddRole(ctx, tenant1Policy, "Editor", "Edits the records of the tenant."); err != nil {
				t.Fatalf("AddRole() error = %v", err)
			}
			if err := mgr.AddRolePermissionResources(ctx, tenant1Policy, "Editor", "Read", "employees"); err != nil {
				t.Fatalf("AddRolePermissionResources() error = %v", err)
			}
			if err := mgr.AddRoleUsers(ctx, tenant1Policy, "Editor", "erin"); err != nil {
				t.Fatalf("AddRoleUsers() error = %v", err)
			}

			env := accesstypes.NewEnvironment()

			// Read-your-writes on the writing instance.
			if decisions, err := writer.CheckUserResources(ctx, env, "erin", tenant1, "Read", "employees"); err != nil || !decisions["employees"].IsGranted() {
				t.Fatalf("writer CheckUserResources() = (%v, %v), want granted immediately", decisions, err)
			}

			// Cross-instance propagation.
			waitFor(t, 15*time.Second, "policy change never reached the reader instance", func() bool {
				decisions, err := reader.CheckUserResources(ctx, env, "erin", tenant1, "Read", "employees")
				if err != nil {
					t.Fatalf("reader CheckUserResources() error = %v", err)
				}

				return decisions["employees"].IsGranted()
			})

			if got := len(channel.Signals()); (got > 0) != tt.withSignal {
				t.Fatalf("channel saw %d signals, want some = %v", got, tt.withSignal)
			}
		})
	}
}
