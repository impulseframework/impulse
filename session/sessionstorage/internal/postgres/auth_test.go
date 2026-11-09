package postgres

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/drivertest"
	"github.com/jackc/pgx/v5"
)

// TestAuth runs the shared users conformance suite against the PostgreSQL driver,
// on the shipped auth migrations.
func TestAuth(t *testing.T) {
	t.Parallel()

	drivertest.RunAuth(t, &drivertest.AuthHarness{
		New:                newAuthInstance,
		CountAuthEvents:    countAuthEvents,
		DeleteSession:      deleteSession,
		Recorded:           recorded,
		Record:             record,
		PendingConsumedAt:  pendingConsumedAt,
		MailLinkConsumedAt: mailLinkConsumedAt,
	})
}

// authSources maps an users suite schema to this package's migration sources.
func authSources(schema drivertest.AuthSchema) []string {
	legacy := []string{"file://../../../schema/postgresql/migrations", "file://../../../schema/postgresql/impersonation/migrations"}
	switch schema {
	case drivertest.LegacySessions:
		return legacy
	case drivertest.Auth:
		return append(legacy, "file://../../../schema/postgresql/auth/migrations")
	case drivertest.AuthWithAppTables:
		return append(legacy, "file://../../../schema/postgresql/auth/migrations", "file://testdata/auth_test/app_tables")
	case drivertest.AuthUsernameIsEmail:
		return append(legacy, "file://../../../schema/postgresql/auth/migrations", "file://testdata/auth_test/app_tables", "file://testdata/auth_test/username_is_email")
	default:
		panic("unknown schema")
	}
}

func newAuthInstance(ctx context.Context, t *testing.T, schema drivertest.AuthSchema, cfg drivertest.AuthConfig) *drivertest.AuthInstance {
	t.Helper()

	conn, err := prepareDatabase(ctx, t, authSources(schema)...)
	if err != nil {
		t.Fatalf("prepareDatabase() error = %v", err)
	}

	d := NewSessionStorageDriver(conn.Pool)
	if cfg.Auth {
		d.EnableAuth()
	}
	if schema == drivertest.AuthUsernameIsEmail {
		d.SetUsernameIsEmail(true)
	}
	if cfg.AuthEvents {
		d.SetAuthEvents(&AuthEventsConfig{TableName: "SessionAuthEvents"})
	}
	if cfg.Impersonation {
		d.SetImpersonation(&ImpersonationConfig{TableName: "SessionImpersonations"})
	}
	if cfg.CustomData != nil {
		d.SetCustomSessionData(&CustomSessionDataConfig{
			TableName: "SessionCustomData",
			Codec:     mustCodec(reflect.TypeFor[drivertest.CustomStringData]()),
			Resolver: func(ctx context.Context, txn pgx.Tx, req *sessioninfo.NewSessionRequest) (any, error) {
				return cfg.CustomData(ctx, hookTx{txn}, req)
			},
		})
	}

	return &drivertest.AuthInstance{
		Driver: d, Raw: conn.Pool,
		SignIn: drivertest.NewSignIn(d, cfg, func(txn pgx.Tx) drivertest.HookTx { return hookTx{txn} }),
		Accept: drivertest.NewAccept(d, cfg),
	}
}

func countAuthEvents(ctx context.Context, t *testing.T, raw any, sessionID impulse.UUID) int {
	t.Helper()

	var n int
	if err := queryer(t, raw).QueryRow(ctx, `SELECT COUNT(*) FROM "SessionAuthEvents" WHERE "SessionId" = $1`, sessionID).Scan(&n); err != nil {
		t.Fatalf("QueryRow().Scan() error = %v", err)
	}

	return n
}

func pendingConsumedAt(ctx context.Context, t *testing.T, raw any, tokenHash []byte) *time.Time {
	t.Helper()

	var consumedAt *time.Time
	if err := queryer(t, raw).QueryRow(ctx, `SELECT "ConsumedAt" FROM "SessionPendingSignIns" WHERE "TokenHash" = $1`, tokenHash).Scan(&consumedAt); err != nil {
		t.Fatalf("QueryRow().Scan() error = %v", err)
	}

	return consumedAt
}

func deleteSession(ctx context.Context, t *testing.T, raw any, sessionID impulse.UUID) {
	t.Helper()

	if _, err := queryer(t, raw).Exec(ctx, `DELETE FROM "Sessions" WHERE "Id" = $1`, sessionID); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
}

// hookTx is drivertest.HookTx over a hook's transaction.
type hookTx struct {
	txn pgx.Tx
}

func (x hookTx) Record(ctx context.Context, key, value string) error {
	_, err := x.txn.Exec(ctx, `INSERT INTO "HookRecords" ("RecordKey", "RecordValue") VALUES ($1, $2)
		ON CONFLICT ("RecordKey") DO UPDATE SET "RecordValue" = EXCLUDED."RecordValue"`, key, value)
	if err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec()")
	}

	return nil
}

func (x hookTx) Recorded(ctx context.Context, key string) (value string, found bool, err error) {
	err = x.txn.QueryRow(ctx, `SELECT "RecordValue" FROM "HookRecords" WHERE "RecordKey" = $1`, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}

	return value, true, nil
}

func (x hookTx) UserExists(ctx context.Context, userID impulse.UUID) (bool, error) {
	var exists bool
	if err := x.txn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM "SessionUsers" WHERE "Id" = $1)`, userID).Scan(&exists); err != nil {
		return false, errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}

	return exists, nil
}

func recorded(ctx context.Context, t *testing.T, raw any, key string) (string, bool) {
	t.Helper()

	var value string
	err := queryer(t, raw).QueryRow(ctx, `SELECT "RecordValue" FROM "HookRecords" WHERE "RecordKey" = $1`, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("QueryRow().Scan() error = %v", err)
	}

	return value, true
}

func record(ctx context.Context, t *testing.T, raw any, key, value string) {
	t.Helper()

	if _, err := queryer(t, raw).Exec(ctx, `INSERT INTO "HookRecords" ("RecordKey", "RecordValue") VALUES ($1, $2)
		ON CONFLICT ("RecordKey") DO UPDATE SET "RecordValue" = EXCLUDED."RecordValue"`, key, value); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
}

func mailLinkConsumedAt(ctx context.Context, t *testing.T, raw any, tokenHash []byte) *time.Time {
	t.Helper()

	var consumedAt *time.Time
	if err := queryer(t, raw).QueryRow(ctx, `SELECT "ConsumedAt" FROM "SessionMailLinks" WHERE "TokenHash" = $1`, tokenHash).Scan(&consumedAt); err != nil {
		t.Fatalf("QueryRow().Scan() error = %v", err)
	}

	return consumedAt
}
