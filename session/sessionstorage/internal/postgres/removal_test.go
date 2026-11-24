package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/drivertest"
	"github.com/jackc/pgx/v5"
)

// PostgreSQL has no row deletion policy, so the store removes the rows that are over as
// it writes: a sign-in removes the sessions idle more than 30 days, a recorded failure
// the sign-in attempts rows that stopped counting more than a day before, and a link's
// insert the mail links that expired more than 30 days before. These tests stage a row's
// age with SQL through the pool.

// hundredYears is the window and the lock the library keeps its counts under that no
// time resets.
const hundredYears = 100 * 365 * 24 * time.Hour

func TestAuth_SignInRemovesIdleSessions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true, AuthEvents: true})
	idle31 := signInAs(ctx, t, in, "pat@lakeside.edu")
	active29 := signInAs(ctx, t, in, "sam@lakeside.edu")
	if got := countAuthEvents(ctx, t, in.Raw, idle31); got != 1 {
		t.Fatalf("auth events of the session to be removed = %d, want its sign-in's 1", got)
	}
	age(ctx, t, in.Raw, `UPDATE "Sessions" SET "CreatedAt" = now() - interval '744 hours', "UpdatedAt" = now() - interval '744 hours' WHERE "Id" = $1`, idle31)
	age(ctx, t, in.Raw, `UPDATE "Sessions" SET "CreatedAt" = now() - interval '744 hours', "UpdatedAt" = now() - interval '696 hours' WHERE "Id" = $1`, active29)

	fresh := signInAs(ctx, t, in, "kim@lakeside.edu")

	for _, tt := range []struct {
		name string
		id   impulse.UUID
		want bool
	}{
		{name: "a session made and last active 31 days ago", id: idle31, want: false},
		{name: "a session made 31 days ago and active 29 days ago", id: active29, want: true},
		{name: "the new session", id: fresh, want: true},
	} {
		if got := exists(ctx, t, in.Raw, `"Sessions"`, tt.id); got != tt.want {
			t.Errorf("%s is there after another person's sign-in = %v, want %v", tt.name, got, tt.want)
		}
	}
	if got := countAuthEvents(ctx, t, in.Raw, idle31); got != 0 {
		t.Errorf("auth events of the removed session = %d, want 0: they go with it", got)
	}
}

// TestAuth_SignInStandsWhenItsRemovalFails holds a due session by a foreign key with no
// cascade, which the removal cannot delete through: the sign-in that carries the removal
// still signs the person in, and the due session stays until the holding row goes.
func TestAuth_SignInStandsWhenItsRemovalFails(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	due := signInAs(ctx, t, in, "pat@lakeside.edu")
	age(ctx, t, in.Raw, `UPDATE "Sessions" SET "CreatedAt" = now() - interval '744 hours', "UpdatedAt" = now() - interval '744 hours' WHERE "Id" = $1`, due)
	age(ctx, t, in.Raw, `CREATE TABLE "SessionNotes" ("SessionId" uuid NOT NULL REFERENCES "Sessions" ("Id"))`)
	age(ctx, t, in.Raw, `INSERT INTO "SessionNotes" ("SessionId") VALUES ($1)`, due)

	held := signInAs(ctx, t, in, "sam@lakeside.edu")
	if !exists(ctx, t, in.Raw, `"Sessions"`, held) {
		t.Error("the session of the sign-in whose removal failed is not there, want it signed in")
	}
	if !exists(ctx, t, in.Raw, `"Sessions"`, due) {
		t.Error("the due session a foreign key with no cascade holds is gone, want it kept")
	}

	age(ctx, t, in.Raw, `DELETE FROM "SessionNotes"`)
	signInAs(ctx, t, in, "kim@lakeside.edu")
	if exists(ctx, t, in.Raw, `"Sessions"`, due) {
		t.Error("the due session is there after the sign-in that follows the holding row's delete, want it removed")
	}
}

func TestAuth_FailureRemovesEndedAttempts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	now := time.Now().Truncate(time.Microsecond)
	short := dbtype.SignInAttemptLimit{Failures: 3, Window: 15 * time.Minute, Lock: 10 * time.Minute}
	kept := dbtype.SignInAttemptLimit{Failures: 1, Window: hundredYears, Lock: hundredYears}
	record := func(name string, at time.Time, limit dbtype.SignInAttemptLimit) {
		t.Helper()

		if _, err := in.Driver.RecordSignInFailure(ctx, name, at, limit); err != nil {
			t.Fatalf("RecordSignInFailure(%q) error = %v", name, err)
		}
	}
	// Ended an hour before now; locked for a hundred years 400 days before now; ended
	// two days and three quarters before now, recorded last so that no failure but the
	// one under test comes after it.
	record("recent@lakeside.edu", now.Add(-75*time.Minute), short)
	record("CODE-GUESS:pat@lakeside.edu", now.Add(-400*24*time.Hour), kept)
	record("ended@lakeside.edu", now.Add(-3*24*time.Hour), short)

	record("other@lakeside.edu", now, short)

	if _, err := in.Driver.SignInAttempts(ctx, "ended@lakeside.edu"); !httpio.HasNotFound(err) {
		t.Errorf("SignInAttempts() of a row that ended more than a day before error = %v, want NotFound: removed", err)
	}
	if r, err := in.Driver.SignInAttempts(ctx, "recent@lakeside.edu"); err != nil || r.Failures != 1 {
		t.Errorf("SignInAttempts() of a row that ended an hour before = %+v, %v; want it kept", r, err)
	}
	r, err := in.Driver.SignInAttempts(ctx, "CODE-GUESS:pat@lakeside.edu")
	if err != nil {
		t.Fatalf("SignInAttempts() of a row under a hundred-year lock error = %v, want it kept", err)
	}
	if _, locked := r.Locked(now); !locked {
		t.Errorf("SignInAttempts() of a row under a hundred-year lock = %+v, want it still locked at %v", r, now)
	}
}

func TestAuth_FailureOnAnEndedNameOpensANewWindow(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	now := time.Now().Truncate(time.Microsecond)
	limit := dbtype.SignInAttemptLimit{Failures: 3, Window: 15 * time.Minute, Lock: 10 * time.Minute}
	for range 2 {
		if _, err := in.Driver.RecordSignInFailure(ctx, "pat@lakeside.edu", now.Add(-3*24*time.Hour), limit); err != nil {
			t.Fatalf("RecordSignInFailure() error = %v", err)
		}
	}

	got, err := in.Driver.RecordSignInFailure(ctx, "pat@lakeside.edu", now, limit)
	if err != nil {
		t.Fatalf("RecordSignInFailure() on the name whose own row ended error = %v", err)
	}
	want := &dbtype.SignInAttempts{Name: "pat@lakeside.edu", Failures: 1, WindowEndsAt: now.Add(limit.Window), Counted: true, UpdatedAt: got.UpdatedAt}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("RecordSignInFailure() on the name whose own row ended mismatch (-want +got):\n%s", diff)
	}
}

// TestAuth_FailureStandsWhenItsRemovalFails records a failure on a table whose EndsAt
// column is gone, so the removal cannot read it: the failure is recorded all the same.
func TestAuth_FailureStandsWhenItsRemovalFails(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	age(ctx, t, in.Raw, `ALTER TABLE "SessionSignInAttempts" DROP COLUMN "EndsAt"`)
	now := time.Now().Truncate(time.Microsecond)
	limit := dbtype.SignInAttemptLimit{Failures: 3, Window: 15 * time.Minute, Lock: 10 * time.Minute}

	got, err := in.Driver.RecordSignInFailure(ctx, "pat@lakeside.edu", now, limit)
	if err != nil {
		t.Fatalf("RecordSignInFailure() whose removal fails error = %v, want the failure recorded", err)
	}
	if got.Failures != 1 {
		t.Errorf("RecordSignInFailure() whose removal fails = %+v, want the first failure", got)
	}
	if r, err := in.Driver.SignInAttempts(ctx, "pat@lakeside.edu"); err != nil || r.Failures != 1 {
		t.Errorf("SignInAttempts() after a failure whose removal failed = %+v, %v; want the failure read back", r, err)
	}
}

func TestAuth_LinkInsertRemovesExpiredLinks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	live := insertLink(ctx, t, in.Driver, "live", time.Now().Add(time.Hour))
	expired29 := insertLink(ctx, t, in.Driver, "expired29", time.Now().Add(-29*24*time.Hour))
	expired31 := insertDueLink(ctx, t, in.Raw, "expired31")

	insertLink(ctx, t, in.Driver, "another", time.Now().Add(time.Hour))

	for _, tt := range []struct {
		name string
		link *dbtype.MailLink
		want bool
	}{
		{name: "a link that expired 31 days ago", link: expired31, want: false},
		{name: "a link that expired 29 days ago", link: expired29, want: true},
		{name: "a live link", link: live, want: true},
	} {
		if got := exists(ctx, t, in.Raw, `"SessionMailLinks"`, tt.link.ID); got != tt.want {
			t.Errorf("%s is there after another link's insert = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestAuth_LinkInsertRemovesAHundredDueLinksOldestFirst(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	// The link i expired 31 days and i seconds ago: the higher i, the older.
	age(ctx, t, in.Raw, `INSERT INTO "SessionMailLinks" ("Id", "TokenHash", "Kind", "Email", "CreatedAt", "ExpiresAt")
		SELECT gen_random_uuid(), decode(md5('due' || i), 'hex'), 'sign-up', 'pat' || i || '@lakeside.edu', now() - interval '40 days', now() - interval '31 days' - i * interval '1 second'
		FROM generate_series(1, 150) i`)
	due := func() []string {
		t.Helper()

		rows, err := queryer(t, in.Raw).Query(ctx, `SELECT "Email" FROM "SessionMailLinks" WHERE "ExpiresAt" < now() - interval '30 days' ORDER BY "ExpiresAt" DESC`)
		if err != nil {
			t.Fatalf("Query() error = %v", err)
		}
		emails, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("pgx.CollectRows() error = %v", err)
		}

		return emails
	}

	insertLink(ctx, t, in.Driver, "first", time.Now().Add(time.Hour))
	var newest []string
	for i := 1; i <= 50; i++ {
		newest = append(newest, fmt.Sprintf("pat%d@lakeside.edu", i))
	}
	if diff := cmp.Diff(newest, due()); diff != "" {
		t.Errorf("due links after one insert mismatch, want the 50 that expired last (-want +got):\n%s", diff)
	}

	insertLink(ctx, t, in.Driver, "second", time.Now().Add(time.Hour))
	if got := due(); len(got) != 0 {
		t.Errorf("due links after the second insert = %d, want none", len(got))
	}
}

func TestAuth_LinkInsertSkipsAHeldRow(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	held := insertDueLink(ctx, t, in.Raw, "held")
	tx, err := queryer(t, in.Raw).BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM "SessionMailLinks" WHERE "Id" = $1 FOR UPDATE`, held.ID); err != nil {
		t.Fatalf("SELECT FOR UPDATE error = %v", err)
	}

	short, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := in.Driver.InsertMailLink(short, newLink("while-held", time.Now().Add(time.Hour))); err != nil {
		t.Fatalf("InsertMailLink() while another transaction holds a due row error = %v, want it to go on without the row", err)
	}
	if !exists(ctx, t, in.Raw, `"SessionMailLinks"`, held.ID) {
		t.Error("a due row another transaction holds was removed, want it left")
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	insertLink(ctx, t, in.Driver, "after", time.Now().Add(time.Hour))
	if exists(ctx, t, in.Raw, `"SessionMailLinks"`, held.ID) {
		t.Error("a due row is there after the next insert that follows its release, want it removed")
	}
}

// TestAuth_LinkInsertStandsWhenItsRemovalFails holds a due link by a foreign key with no
// cascade, which the removal cannot delete through: the insert that carries the removal
// still inserts its link, and the due link stays.
func TestAuth_LinkInsertStandsWhenItsRemovalFails(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	due := insertDueLink(ctx, t, in.Raw, "due")
	age(ctx, t, in.Raw, `CREATE TABLE "LinkNotes" ("LinkId" uuid NOT NULL REFERENCES "SessionMailLinks" ("Id"))`)
	age(ctx, t, in.Raw, `INSERT INTO "LinkNotes" ("LinkId") VALUES ($1)`, due.ID)

	link := newLink("new", time.Now().Add(time.Hour))
	if err := in.Driver.InsertMailLink(ctx, link); err != nil {
		t.Fatalf("InsertMailLink() whose removal fails error = %v, want the link inserted", err)
	}
	if !exists(ctx, t, in.Raw, `"SessionMailLinks"`, link.ID) {
		t.Error("the link whose insert's removal failed is not there, want it inserted")
	}
	if !exists(ctx, t, in.Raw, `"SessionMailLinks"`, due.ID) {
		t.Error("the due link a foreign key with no cascade holds is gone, want it kept")
	}
}

// txQueryer is a transaction the test hands a driver as its connection: what the driver
// sends runs in the transaction, and a transaction the driver begins is a savepoint of it,
// at the transaction's own level.
type txQueryer struct {
	pgx.Tx
}

// BeginTx opens a savepoint of the transaction; the options are the transaction's.
//
//nolint:gocritic // The signature is Queryer's, which takes the options by value as pgx's pool does.
func (q txQueryer) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	savepoint, err := q.Begin(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "pgx.Tx.Begin()")
	}

	return savepoint, nil
}

// TestAuth_ActivityUpdateWritesNoIndex proves what the sessions removal's index rests on:
// the store's activity update moves UpdatedAt and leaves the day of it where it was, so
// it changes no indexed column, and PostgreSQL makes it a heap-only update, one that
// writes no index entry, while the row's page has room, as it has here. The
// transaction's own counters read the one update.
func TestAuth_ActivityUpdateWritesNoIndex(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	id := signInAs(ctx, t, in, "pat@lakeside.edu")

	tx, err := queryer(t, in.Raw).BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	// Rolled back on every path: the transaction only reads its own counters.
	defer func() { _ = tx.Rollback(ctx) }()
	if err := NewSessionStorageDriver(txQueryer{tx}).UpdateSessionActivity(ctx, id); err != nil {
		t.Fatalf("UpdateSessionActivity() error = %v", err)
	}

	var updated, heapOnly int64
	if err := tx.QueryRow(ctx, `SELECT n_tup_upd, n_tup_hot_upd FROM pg_stat_xact_user_tables WHERE relid = '"Sessions"'::regclass`).Scan(&updated, &heapOnly); err != nil {
		t.Fatalf("QueryRow().Scan() error = %v", err)
	}
	if updated != 1 || heapOnly != 1 {
		t.Errorf("the activity update's counters: %d updated, %d heap-only; want 1 and 1, an update that writes no index", updated, heapOnly)
	}
}

// TestAuth_RemovalsReadTheirIndexes proves that each removal reads the rows that are due
// by an index: the removal runs with every recorded failure, and without one a flood of
// typed names would have each failure read the whole table. Sequential scans are switched
// off for the plan, so the planner takes the index wherever it can.
func TestAuth_RemovalsReadTheirIndexes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, authSources(drivertest.Auth)...)
	if err != nil {
		t.Fatalf("prepareDatabase() error = %v", err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	// Rolled back on every path: the transaction only reads plans.
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("SET LOCAL enable_seqscan error = %v", err)
	}

	for _, tt := range []struct {
		table   string
		removal removal
		args    []any
		index   string
	}{
		{table: `"Sessions"`, removal: sessionsRemoval, index: "Sessions_UpdatedOn_idx"},
		{table: `"SessionSignInAttempts"`, removal: attemptsRemoval, args: []any{time.Now()}, index: "SessionSignInAttempts_EndsAt_idx"},
		{table: `"SessionMailLinks"`, removal: mailLinksRemoval, index: "SessionMailLinks_ExpiresAt_idx"},
	} {
		rows, err := tx.Query(ctx, "EXPLAIN (COSTS OFF) "+tt.removal.statement(tt.table), tt.args...)
		if err != nil {
			t.Fatalf("EXPLAIN of the removal from %s error = %v", tt.table, err)
		}
		lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("pgx.CollectRows() error = %v", err)
		}
		if plan := strings.Join(lines, "\n"); !strings.Contains(plan, tt.index) {
			t.Errorf("the removal from %s does not read the index %s; the plan:\n%s", tt.table, tt.index, plan)
		}
	}
}

// signInAs makes an account for username and signs it in, as a person signs in by
// password, and returns the new session's id.
func signInAs(ctx context.Context, t *testing.T, in *drivertest.AuthInstance, username string) impulse.UUID {
	t.Helper()

	user, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: username}, nil)
	if err != nil {
		t.Fatalf("CreateUser(%q) error = %v", username, err)
	}
	id, err := in.SignIn(ctx, &dbtype.InsertSession{Username: username}, &sessioninfo.NewSessionRequest{
		Reason: sessioninfo.ReasonLogin, Username: username, UserID: user.ID,
		Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()},
	})
	if err != nil {
		t.Fatalf("SignIn(%q) error = %v", username, err)
	}

	return id
}

// age runs a statement that stages rows' ages, directly.
func age(ctx context.Context, t *testing.T, raw any, statement string, args ...any) {
	t.Helper()

	if _, err := queryer(t, raw).Exec(ctx, statement, args...); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
}

// exists says whether table holds a row whose "Id" is id, read directly.
func exists(ctx context.Context, t *testing.T, raw any, table string, id impulse.UUID) bool {
	t.Helper()

	var found bool
	if err := queryer(t, raw).QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE "Id" = $1)`, table), id).Scan(&found); err != nil {
		t.Fatalf("QueryRow().Scan() error = %v", err)
	}

	return found
}

// newLink is a sign-up link to an address named after token, expiring at expiresAt.
func newLink(token string, expiresAt time.Time) *dbtype.MailLink {
	return &dbtype.MailLink{
		ID: impulse.Must(impulse.NewUUID()), TokenHash: []byte(token), Kind: dbtype.MailLinkSignUp,
		Email: token + "@lakeside.edu", ExpiresAt: expiresAt,
	}
}

// insertLink inserts newLink(token, expiresAt) through the driver.
func insertLink(ctx context.Context, t *testing.T, d drivertest.AuthDriver, token string, expiresAt time.Time) *dbtype.MailLink {
	t.Helper()

	link := newLink(token, expiresAt)
	if err := d.InsertMailLink(ctx, link); err != nil {
		t.Fatalf("InsertMailLink(%q) error = %v", token, err)
	}

	return link
}

// insertDueLink inserts newLink(token, a time 31 days ago) directly: through the driver,
// the insert's own removal would take it.
func insertDueLink(ctx context.Context, t *testing.T, raw any, token string) *dbtype.MailLink {
	t.Helper()

	link := newLink(token, time.Now().Add(-31*24*time.Hour))
	age(ctx, t, raw, `INSERT INTO "SessionMailLinks" ("Id", "TokenHash", "Kind", "Email", "CreatedAt", "ExpiresAt") VALUES ($1, $2, $3, $4, now() - interval '40 days', $5)`,
		link.ID, link.TokenHash, string(link.Kind), link.Email, link.ExpiresAt)

	return link
}
