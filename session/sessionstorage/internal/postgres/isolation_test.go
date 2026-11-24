package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/drivertest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// driverAtDefault is a driver on a pool of its own to the test's database whose sessions
// default to the isolation level given (default_transaction_isolation), and the pool: the
// database's default level is not the driver's to count on, so a transaction of the
// driver that leaves its level to the connection runs at that one.
func driverAtDefault(ctx context.Context, t *testing.T, database *pgxpool.Pool, level string) (*SessionStorageDriver, *pgxpool.Pool) {
	t.Helper()

	config := database.Config()
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["default_transaction_isolation"] = level
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig() error = %v", err)
	}
	t.Cleanup(pool.Close)
	d := NewSessionStorageDriver(pool)
	d.EnableAuth()

	return d, pool
}

// seenWaitingOnALock waits until a session of the test's database waits on a lock, as a
// statement of the driver does while the test holds a lock it needs. It fails the test
// where done is closed first: the driver answered without waiting for the lock.
func seenWaitingOnALock(t *testing.T, database *pgxpool.Pool, done <-chan struct{}) {
	t.Helper()

	ctx := t.Context()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatal("the driver answered before it was seen waiting on a lock")
		default:
		}
		var waiting bool
		if err := database.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock')`).Scan(&waiting); err != nil {
			t.Fatalf("pgxpool.Pool.QueryRow() pg_stat_activity error = %v", err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no session of the database was seen waiting on a lock within 30 seconds")
}

// TestSessionStorageDriver_beginsAtReadCommitted proves that the driver's transactions are
// at read committed whatever default the connection sets: on a pool whose sessions default
// to repeatable read, and to serializable, a statement outside a transaction reads the
// default the pool was given, and a statement inside Transact reads read committed.
func TestSessionStorageDriver_beginsAtReadCommitted(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, authSources(drivertest.Auth)...)
	if err != nil {
		t.Fatalf("prepareDatabase() error = %v", err)
	}
	for _, level := range []string{"repeatable read", "serializable"} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			d, pool := driverAtDefault(ctx, t, db.Pool, level)
			var staged string
			if err := pool.QueryRow(ctx, `SELECT current_setting('default_transaction_isolation')`).Scan(&staged); err != nil {
				t.Fatalf("pgxpool.Pool.QueryRow() default_transaction_isolation error = %v", err)
			}
			if staged != level {
				t.Fatalf("the pool's sessions default to %q, want %q: the case would prove nothing", staged, level)
			}
			var got string
			if err := d.Transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT current_setting('transaction_isolation')`).Scan(&got)
			}); err != nil {
				t.Fatalf("Transact() error = %v", err)
			}
			if got != "read committed" {
				t.Errorf("Transact() on a connection whose default is %s runs at %q, want read committed", level, got)
			}
		})
	}
}

// TestSessionStorageDriver_LockUserIn_seesTheLinkItWaitedFor proves that a link decision
// that waited for another's lock on the user reads the link the other wrote, on a
// connection whose sessions default to repeatable read: the read after the lock is a
// statement of its own at read committed, which reads what has committed when it starts.
// A transaction that read the snapshot its first statement took, before it waited, would
// find no link.
func TestSessionStorageDriver_LockUserIn_seesTheLinkItWaitedFor(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, authSources(drivertest.Auth)...)
	if err != nil {
		t.Fatalf("prepareDatabase() error = %v", err)
	}
	d := NewSessionStorageDriver(db.Pool)
	d.EnableAuth()
	user, err := d.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "turns@lakeside.edu"}, nil)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	identity := &sessioninfo.Identity{Method: sessioninfo.MethodWorkOS, Connection: "conn_lakeside", Subject: "idp_turns", Email: "turns@lakeside.edu"}
	turns, _ := driverAtDefault(ctx, t, db.Pool, "repeatable read")

	first, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	t.Cleanup(func() {
		_ = first.Rollback(context.WithoutCancel(ctx))
	})
	if _, err := d.LockUserIn(ctx, first, user.ID); err != nil {
		t.Fatalf("LockUserIn() in the first transaction error = %v", err)
	}

	var (
		read      []*dbtype.SessionIdentity
		secondErr error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		secondErr = turns.Transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := turns.LockUserIn(ctx, tx, user.ID); err != nil {
				return err
			}
			links, err := turns.IdentitiesByUserIn(ctx, tx, user.ID)
			read = links

			return err
		})
	}()
	seenWaitingOnALock(t, db.Pool, done)
	if err := d.LinkIdentityIn(ctx, first, user.ID, identity, ""); err != nil {
		t.Fatalf("LinkIdentityIn() error = %v", err)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	<-done

	if secondErr != nil {
		t.Fatalf("the link decision that waited ended with %v, want no error", secondErr)
	}
	if len(read) != 1 || read[0].Connection != identity.Connection || read[0].Subject != identity.Subject {
		t.Errorf("the link decision that waited read %+v, want the first's link and no other", read)
	}
}
