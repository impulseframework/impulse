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
// it writes: a write that inserts a session removes the sessions idle more than 30 days, a
// recorded failure the sign-in attempts rows that stopped counting more than a day before,
// a link's insert the mail links that expired more than 30 days before, and a write that
// inserts a token, a trusted device, a device code or a pending sign-in the rows of its
// own table that expired more than 30 days before (the first two) or a day before (the
// last two). These tests stage a row's age with SQL through the pool.

// TestSessions_InsertRemovesIdleSessions proves that a store on the base set alone, which
// signs nobody in (a Preauth store's Login), removes the sessions idle more than 30 days
// as it inserts a session.
func TestSessions_InsertRemovesIdleSessions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newImpersonationInstance(ctx, t, drivertest.Sessions, drivertest.Config{})
	idle31 := insertSessionAs(ctx, t, in.Driver, "pat")
	active29 := insertSessionAs(ctx, t, in.Driver, "sam")
	live := insertSessionAs(ctx, t, in.Driver, "kim")
	age(ctx, t, in.Raw, `UPDATE "Sessions" SET "CreatedAt" = now() - interval '744 hours', "UpdatedAt" = now() - interval '744 hours' WHERE "Id" = $1`, idle31)
	age(ctx, t, in.Raw, `UPDATE "Sessions" SET "CreatedAt" = now() - interval '744 hours', "UpdatedAt" = now() - interval '696 hours' WHERE "Id" = $1`, active29)

	fresh := insertSessionAs(ctx, t, in.Driver, "lee")

	for _, tt := range []struct {
		name string
		id   impulse.UUID
		want bool
	}{
		{name: "a session made and last active 31 days ago", id: idle31, want: false},
		{name: "a session made 31 days ago and active 29 days ago", id: active29, want: true},
		{name: "a session in use", id: live, want: true},
		{name: "the new session", id: fresh, want: true},
	} {
		if got := exists(ctx, t, in.Raw, `"Sessions"`, tt.id); got != tt.want {
			t.Errorf("%s is there after another session's insert = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestSessions_ImpersonatedInsertRemovesIdleSessions proves that an impersonated session's
// insert removes the sessions idle more than 30 days, as any other session's does.
func TestSessions_ImpersonatedInsertRemovesIdleSessions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newImpersonationInstance(ctx, t, drivertest.Sessions, drivertest.Config{Impersonation: true})
	idle := insertSessionAs(ctx, t, in.Driver, "pat")
	age(ctx, t, in.Raw, `UPDATE "Sessions" SET "CreatedAt" = now() - interval '744 hours', "UpdatedAt" = now() - interval '744 hours' WHERE "Id" = $1`, idle)

	role := "PartnerViewer"
	id, err := in.Driver.InsertImpersonatedSession(ctx, &dbtype.InsertSession{Username: "alice@example.com"},
		&sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonImpersonation, Username: "alice@example.com"},
		&dbtype.InsertImpersonation{ActorUsername: "alice@example.com", PrincipalKind: dbtype.PrincipalKindRole, PrincipalRole: &role, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("InsertImpersonatedSession() error = %v", err)
	}
	if !exists(ctx, t, in.Raw, `"Sessions"`, id) {
		t.Error("the impersonated session is not there")
	}
	if exists(ctx, t, in.Raw, `"Sessions"`, idle) {
		t.Error("a session made and last active 31 days ago is there after an impersonated session's insert, want it removed")
	}
}

// TestAuth_InsertedSessionRemovesIdleSessions proves that a session an Auth store inserts
// without a sign-in (one the application starts) removes the sessions idle more than 30
// days, with their auth events.
func TestAuth_InsertedSessionRemovesIdleSessions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true, AuthEvents: true})
	idle := signInAs(ctx, t, in, "pat@lakeside.edu")
	if got := countAuthEvents(ctx, t, in.Raw, idle); got != 1 {
		t.Fatalf("auth events of the session to be removed = %d, want its sign-in's 1", got)
	}
	age(ctx, t, in.Raw, `UPDATE "Sessions" SET "CreatedAt" = now() - interval '744 hours', "UpdatedAt" = now() - interval '744 hours' WHERE "Id" = $1`, idle)

	user := createTestUser(ctx, t, in.Driver, "sam@lakeside.edu")
	id, err := in.Driver.InsertSession(ctx, &dbtype.InsertSession{Username: "sam@lakeside.edu"}, &sessioninfo.NewSessionRequest{
		Reason: sessioninfo.ReasonLogin, Username: "sam@lakeside.edu", UserID: user.ID,
		Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()},
	})
	if err != nil {
		t.Fatalf("InsertSession() error = %v", err)
	}
	if !exists(ctx, t, in.Raw, `"Sessions"`, id) {
		t.Error("the inserted session is not there")
	}
	if exists(ctx, t, in.Raw, `"Sessions"`, idle) {
		t.Error("a session made and last active 31 days ago is there after a session's insert with no sign-in, want it removed")
	}
	if got := countAuthEvents(ctx, t, in.Raw, idle); got != 0 {
		t.Errorf("auth events of the removed session = %d, want 0: they go with it", got)
	}
}

// TestSessions_InsertStandsWhenItsRemovalFails holds a due session by a foreign key with
// no cascade, which the removal cannot delete through: the insert that carries the
// removal still inserts its session, and the due session stays until the holding row
// goes.
func TestSessions_InsertStandsWhenItsRemovalFails(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newImpersonationInstance(ctx, t, drivertest.Sessions, drivertest.Config{})
	due := insertSessionAs(ctx, t, in.Driver, "pat")
	age(ctx, t, in.Raw, `UPDATE "Sessions" SET "CreatedAt" = now() - interval '744 hours', "UpdatedAt" = now() - interval '744 hours' WHERE "Id" = $1`, due)
	age(ctx, t, in.Raw, `CREATE TABLE "SessionNotes" ("SessionId" uuid NOT NULL REFERENCES "Sessions" ("Id"))`)
	age(ctx, t, in.Raw, `INSERT INTO "SessionNotes" ("SessionId") VALUES ($1)`, due)

	held := insertSessionAs(ctx, t, in.Driver, "sam")
	if !exists(ctx, t, in.Raw, `"Sessions"`, held) {
		t.Error("the session whose insert's removal failed is not there, want it inserted")
	}
	if !exists(ctx, t, in.Raw, `"Sessions"`, due) {
		t.Error("the due session a foreign key with no cascade holds is gone, want it kept")
	}

	age(ctx, t, in.Raw, `DELETE FROM "SessionNotes"`)
	insertSessionAs(ctx, t, in.Driver, "kim")
	if exists(ctx, t, in.Raw, `"Sessions"`, due) {
		t.Error("the due session is there after the insert that follows the holding row's delete, want it removed")
	}
}

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

// TestAuth_AWriteRemovesTheExpiredRowsOfItsTable stages one row of each of the four
// tables at each of five ages and proves that the next write of a row of a table removes
// the rows of that table past its period and no other: a token and a trusted device 30
// days after it expired, a device code and a pending sign-in a day after, and a pending
// sign-in's code with it.
func TestAuth_AWriteRemovesTheExpiredRowsOfItsTable(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	user := createTestUser(ctx, t, in.Driver, "pat@lakeside.edu")
	staged := []struct {
		name string
		// hours is how long ago the row expired; 0 leaves it expiring in an hour.
		hours int
	}{
		{name: "that expired 744 hours ago (31 days)", hours: 744},
		{name: "that expired 696 hours ago (29 days)", hours: 696},
		{name: "that expired 25 hours ago", hours: 25},
		{name: "that expired 23 hours ago", hours: 23},
		{name: "that expires in an hour"},
	}
	// Every row goes in live, then the ages are staged: an insert removes the rows of its
	// table that are past their period, so an aged row would not outlast the next call.
	rows := make([]drivertest.Expiring, len(staged))
	for i := range staged {
		rows[i] = drivertest.InsertExpiring(ctx, t, in.Driver, user.ID, byte(i+1), time.Now().Add(time.Hour))
	}
	for i, row := range staged {
		if row.hours == 0 {
			continue
		}
		for table, id := range expiringIDs(&rows[i]) {
			age(ctx, t, in.Raw, fmt.Sprintf(`UPDATE %s SET "ExpiresAt" = now() - interval '%d hours' WHERE "Id" = $1`, table, row.hours), id)
		}
	}

	// n 0xff keeps the write's rows apart from the staged ones.
	written := drivertest.InsertExpiring(ctx, t, in.Driver, user.ID, 0xff, time.Now().Add(time.Hour))

	for _, tt := range []struct {
		name  string
		table string
		// due says, of each staged row, whether the write removes it.
		due []bool
	}{
		{name: "tokens", table: `"SessionTokens"`, due: []bool{true, false, false, false, false}},
		{name: "trusted devices", table: `"SessionTrustedDevices"`, due: []bool{true, false, false, false, false}},
		{name: "device codes", table: `"SessionDeviceCodes"`, due: []bool{true, true, true, false, false}},
		{name: "pending sign-ins", table: `"SessionPendingSignIns"`, due: []bool{true, true, true, false, false}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			for i, row := range staged {
				if got := exists(ctx, t, in.Raw, tt.table, expiringIDs(&rows[i])[tt.table]); got == tt.due[i] {
					t.Errorf("a row %s is there after the next insert = %v, want %v", row.name, got, !tt.due[i])
				}
			}
			if !exists(ctx, t, in.Raw, tt.table, expiringIDs(&written)[tt.table]) {
				t.Error("the row the write under test inserted is not there")
			}
			if tt.table != `"SessionPendingSignIns"` {
				return
			}
			for i, row := range staged {
				if got := exists(ctx, t, in.Raw, `"SessionFactorCodes"`, rows[i].FactorCode); got == tt.due[i] {
					t.Errorf("the code under a pending sign-in %s is there after the next insert = %v, want %v: it goes with its pending sign-in", row.name, got, !tt.due[i])
				}
			}
		})
	}
}

// TestAuth_RefreshRemovesExpiredTokens proves that a refresh that rotated its pair
// removes the tokens that expired more than 30 days ago, and that one that inserted
// nothing, a reuse, removes nothing.
func TestAuth_RefreshRemovesExpiredTokens(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	user := createTestUser(ctx, t, in.Driver, "pat@lakeside.edu")
	access, refresh := testFamily(user.ID, 1, 2)
	aged := testToken(dbtype.TokenApplication, user.ID, 3)
	insertTestTokens(ctx, t, in.Driver, access, refresh, aged)
	ageToken(ctx, t, in.Raw, aged.ID)

	if err := in.Driver.ExchangeRefreshToken(ctx, refresh.TokenHash, time.Now(), testToken(dbtype.TokenAccess, impulse.NilUUID, 4), testToken(dbtype.TokenRefresh, impulse.NilUUID, 5)); err != nil {
		t.Fatalf("ExchangeRefreshToken() error = %v", err)
	}
	if exists(ctx, t, in.Raw, `"SessionTokens"`, aged.ID) {
		t.Error("a token that expired 31 days ago is there after a refresh that rotated, want it removed")
	}

	second := testToken(dbtype.TokenApplication, user.ID, 6)
	insertTestTokens(ctx, t, in.Driver, second)
	ageToken(ctx, t, in.Raw, second.ID)
	if err := in.Driver.ExchangeRefreshToken(ctx, refresh.TokenHash, time.Now(), testToken(dbtype.TokenAccess, impulse.NilUUID, 7), testToken(dbtype.TokenRefresh, impulse.NilUUID, 8)); !errors.Is(err, dbtype.ErrTokenReuse) {
		t.Fatalf("ExchangeRefreshToken() of the rotated token error = %v, want ErrTokenReuse", err)
	}
	if !exists(ctx, t, in.Raw, `"SessionTokens"`, second.ID) {
		t.Error("a token that expired 31 days ago is gone after a reuse, want it kept: an exchange that inserted nothing removes nothing")
	}
}

// TestAuth_RedemptionRemovesExpiredTokens proves that the poll that redeems an approved
// device code removes the tokens that expired more than 30 days ago, and that a poll
// that issued nothing removes nothing.
func TestAuth_RedemptionRemovesExpiredTokens(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	user := createTestUser(ctx, t, in.Driver, "pat@lakeside.edu")
	aged := testToken(dbtype.TokenApplication, user.ID, 1)
	insertTestTokens(ctx, t, in.Driver, aged)
	ageToken(ctx, t, in.Raw, aged.ID)
	code := &dbtype.DeviceCode{
		ID: impulse.Must(impulse.NewUUID()), DeviceCodeHash: testTokenHash(2), UserCode: "BCDFGHJK", ClientID: "corral", Outlet: "droids",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	if err := in.Driver.InsertDeviceCode(ctx, code); err != nil {
		t.Fatalf("InsertDeviceCode() error = %v", err)
	}

	_, status, err := in.Driver.RedeemDeviceCode(ctx, code.DeviceCodeHash, time.Now(), 5*time.Second, testToken(dbtype.TokenAccess, impulse.NilUUID, 3), testToken(dbtype.TokenRefresh, impulse.NilUUID, 4))
	if err != nil || status != dbtype.DevicePending {
		t.Fatalf("RedeemDeviceCode() of a pending code = %q, %v; want pending", status, err)
	}
	if !exists(ctx, t, in.Raw, `"SessionTokens"`, aged.ID) {
		t.Error("a token that expired 31 days ago is gone after a poll that issued nothing, want it kept")
	}

	if err := in.Driver.ApproveDeviceCode(ctx, code.ID, user.ID, time.Now()); err != nil {
		t.Fatalf("ApproveDeviceCode() error = %v", err)
	}
	_, status, err = in.Driver.RedeemDeviceCode(ctx, code.DeviceCodeHash, time.Now(), 5*time.Second, testToken(dbtype.TokenAccess, impulse.NilUUID, 5), testToken(dbtype.TokenRefresh, impulse.NilUUID, 6))
	if err != nil || status != dbtype.DeviceApproved {
		t.Fatalf("RedeemDeviceCode() of an approved code = %q, %v; want approved", status, err)
	}
	if exists(ctx, t, in.Raw, `"SessionTokens"`, aged.ID) {
		t.Error("a token that expired 31 days ago is there after the poll that issued the tokens, want it removed")
	}
}

// TestAuth_ARotatedRefreshTokenOutlastsTheRemovalWhileItCouldBeReused proves that the
// removal reads a token's expiry and not its revocation: a refresh token rotated and
// not yet expired is still there after a write that removed the expired tokens, and
// presenting it again is caught as a reuse that revokes its family's current pair.
func TestAuth_ARotatedRefreshTokenOutlastsTheRemovalWhileItCouldBeReused(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	user := createTestUser(ctx, t, in.Driver, "pat@lakeside.edu")
	access1, refresh1 := testFamily(user.ID, 1, 2)
	insertTestTokens(ctx, t, in.Driver, access1, refresh1)
	access2, refresh2 := testToken(dbtype.TokenAccess, impulse.NilUUID, 3), testToken(dbtype.TokenRefresh, impulse.NilUUID, 4)
	if err := in.Driver.ExchangeRefreshToken(ctx, refresh1.TokenHash, time.Now(), access2, refresh2); err != nil {
		t.Fatalf("ExchangeRefreshToken() error = %v", err)
	}
	aged := testToken(dbtype.TokenApplication, user.ID, 5)
	insertTestTokens(ctx, t, in.Driver, aged)
	ageToken(ctx, t, in.Raw, aged.ID)

	insertTestTokens(ctx, t, in.Driver, testToken(dbtype.TokenApplication, user.ID, 6))
	if exists(ctx, t, in.Raw, `"SessionTokens"`, aged.ID) {
		t.Fatal("a token that expired 31 days ago is there after a token's insert, want it removed")
	}
	rotated, err := in.Driver.TokenState(ctx, refresh1.TokenHash)
	if err != nil {
		t.Fatalf("TokenState() of the rotated refresh token after the removal error = %v, want it there until it expires", err)
	}
	if rotated.RevokedAt == nil || rotated.RevokedReason != dbtype.TokenRevokedRotated {
		t.Errorf("the rotated refresh token is revoked at %v (%q), want revoked as rotated", rotated.RevokedAt, rotated.RevokedReason)
	}

	if err := in.Driver.ExchangeRefreshToken(ctx, refresh1.TokenHash, time.Now(), testToken(dbtype.TokenAccess, impulse.NilUUID, 7), testToken(dbtype.TokenRefresh, impulse.NilUUID, 8)); !errors.Is(err, dbtype.ErrTokenReuse) {
		t.Fatalf("ExchangeRefreshToken() of the rotated token presented again error = %v, want ErrTokenReuse", err)
	}
	for _, tok := range []*dbtype.Token{access2, refresh2} {
		got, err := in.Driver.TokenState(ctx, tok.TokenHash)
		if err != nil {
			t.Fatalf("TokenState(%s) of the family's current pair error = %v", tok.Kind, err)
		}
		if got.RevokedAt == nil || got.RevokedReason != dbtype.TokenRevokedReuse {
			t.Errorf("the family's current %s token is revoked at %v (%q), want revoked as reuse", tok.Kind, got.RevokedAt, got.RevokedReason)
		}
	}
}

// TestAuth_RefreshStandsWhenItsRemovalFails holds a due token by a foreign key with no
// cascade, which the removal cannot delete through: the refresh that carries the removal
// still rotates the pair, and the due token stays.
func TestAuth_RefreshStandsWhenItsRemovalFails(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	user := createTestUser(ctx, t, in.Driver, "pat@lakeside.edu")
	access, refresh := testFamily(user.ID, 1, 2)
	due := testToken(dbtype.TokenApplication, user.ID, 3)
	insertTestTokens(ctx, t, in.Driver, access, refresh, due)
	ageToken(ctx, t, in.Raw, due.ID)
	age(ctx, t, in.Raw, `CREATE TABLE "TokenNotes" ("TokenId" uuid NOT NULL REFERENCES "SessionTokens" ("Id"))`)
	age(ctx, t, in.Raw, `INSERT INTO "TokenNotes" ("TokenId") VALUES ($1)`, due.ID)

	next, nextRefresh := testToken(dbtype.TokenAccess, impulse.NilUUID, 4), testToken(dbtype.TokenRefresh, impulse.NilUUID, 5)
	if err := in.Driver.ExchangeRefreshToken(ctx, refresh.TokenHash, time.Now(), next, nextRefresh); err != nil {
		t.Fatalf("ExchangeRefreshToken() whose removal fails error = %v, want the pair rotated", err)
	}
	for _, tok := range []*dbtype.Token{next, nextRefresh} {
		if !exists(ctx, t, in.Raw, `"SessionTokens"`, tok.ID) {
			t.Errorf("the new %s token of the refresh whose removal failed is not there, want it inserted", tok.Kind)
		}
	}
	if !exists(ctx, t, in.Raw, `"SessionTokens"`, due.ID) {
		t.Error("the due token a foreign key with no cascade holds is gone, want it kept")
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
		{table: `"SessionTokens"`, removal: tokensRemoval, index: "SessionTokens_ExpiresAt_idx"},
		{table: `"SessionTrustedDevices"`, removal: trustedDevicesRemoval, index: "SessionTrustedDevices_ExpiresAt_idx"},
		{table: `"SessionDeviceCodes"`, removal: deviceCodesRemoval, index: "SessionDeviceCodes_ExpiresAt_idx"},
		{table: `"SessionPendingSignIns"`, removal: pendingSignInsRemoval, index: "SessionPendingSignIns_ExpiresAt_idx"},
	} {
		if plan := removalPlan(ctx, t, tx, tt.removal, tt.table, tt.args...); !strings.Contains(plan, tt.index) {
			t.Errorf("the removal from %s does not read the index %s; the plan:\n%s", tt.table, tt.index, plan)
		}
	}

	// The sessions' day and its index are the base set's: a store on the base set alone,
	// a Preauth store's, reads the due sessions by the index too.
	t.Run("the sessions over the base set alone", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		base, err := prepareDatabase(ctx, t, "file://../../../schema/postgresql/migrations")
		if err != nil {
			t.Fatalf("prepareDatabase() of the base set alone error = %v", err)
		}
		baseTx, err := base.Begin(ctx)
		if err != nil {
			t.Fatalf("Begin() error = %v", err)
		}
		// Rolled back on every path: the transaction only reads plans.
		defer func() { _ = baseTx.Rollback(ctx) }()
		if _, err := baseTx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
			t.Fatalf("SET LOCAL enable_seqscan error = %v", err)
		}
		if plan := removalPlan(ctx, t, baseTx, sessionsRemoval, `"Sessions"`); !strings.Contains(plan, "Sessions_UpdatedOn_idx") {
			t.Errorf("the removal from \"Sessions\" over the base set alone does not read the index Sessions_UpdatedOn_idx; the plan:\n%s", plan)
		}
	})
}

// removalPlan is the plan the removal's statement over table has in tx.
func removalPlan(ctx context.Context, t *testing.T, tx pgx.Tx, r removal, table string, args ...any) string {
	t.Helper()

	rows, err := tx.Query(ctx, "EXPLAIN (COSTS OFF) "+r.statement(table), args...)
	if err != nil {
		t.Fatalf("EXPLAIN of the removal from %s error = %v", table, err)
	}
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("pgx.CollectRows() error = %v", err)
	}

	return strings.Join(lines, "\n")
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

// insertSessionAs inserts a session of username through d, as a Preauth store's Login
// does, and returns its id.
func insertSessionAs(ctx context.Context, t *testing.T, d drivertest.Driver, username string) impulse.UUID {
	t.Helper()

	id, err := d.InsertSession(ctx, &dbtype.InsertSession{Username: username}, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: username})
	if err != nil {
		t.Fatalf("InsertSession(%q) error = %v", username, err)
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

// createTestUser makes an account for username through the driver.
func createTestUser(ctx context.Context, t *testing.T, d drivertest.AuthDriver, username string) *dbtype.SessionUser {
	t.Helper()

	user, err := d.CreateUser(ctx, &dbtype.InsertSessionUser{Username: username}, nil)
	if err != nil {
		t.Fatalf("CreateUser(%q) error = %v", username, err)
	}

	return user
}

// testTokenHash is a distinct 32-byte hash for the test's token n.
func testTokenHash(n byte) []byte {
	hash := make([]byte, 32)
	hash[0] = n

	return hash
}

// testToken is a token of kind for userID on the droids outlet, holding the hash of n,
// that expires in an hour.
func testToken(kind dbtype.TokenKind, userID impulse.UUID, n byte) *dbtype.Token {
	return &dbtype.Token{
		ID: impulse.Must(impulse.NewUUID()), TokenHash: testTokenHash(n), Kind: kind, UserID: userID, Outlet: "droids",
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

// testFamily is a device grant's access and refresh tokens for userID, in one family,
// issued to the corral client.
func testFamily(userID impulse.UUID, accessN, refreshN byte) (access, refresh *dbtype.Token) {
	family := impulse.NullUUIDFromUUID(impulse.Must(impulse.NewUUID()))
	access = testToken(dbtype.TokenAccess, userID, accessN)
	refresh = testToken(dbtype.TokenRefresh, userID, refreshN)
	for _, tok := range []*dbtype.Token{access, refresh} {
		tok.FamilyID, tok.ClientID = family, "corral"
	}

	return access, refresh
}

// insertTestTokens inserts each token through the driver.
func insertTestTokens(ctx context.Context, t *testing.T, d drivertest.AuthDriver, tokens ...*dbtype.Token) {
	t.Helper()

	for _, tok := range tokens {
		if err := d.InsertToken(ctx, tok); err != nil {
			t.Fatalf("InsertToken(%s) error = %v", tok.Kind, err)
		}
	}
}

// ageToken stages the token id as expired 744 hours (31 days) ago, directly.
func ageToken(ctx context.Context, t *testing.T, raw any, id impulse.UUID) {
	t.Helper()

	age(ctx, t, raw, `UPDATE "SessionTokens" SET "ExpiresAt" = now() - interval '744 hours' WHERE "Id" = $1`, id)
}

// expiringIDs is each row of e, by its table's name.
func expiringIDs(e *drivertest.Expiring) map[string]impulse.UUID {
	return map[string]impulse.UUID{
		`"SessionTokens"`: e.Token, `"SessionTrustedDevices"`: e.TrustedDevice,
		`"SessionDeviceCodes"`: e.DeviceCode, `"SessionPendingSignIns"`: e.PendingSignIn,
	}
}
