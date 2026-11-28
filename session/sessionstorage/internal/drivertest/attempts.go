package drivertest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

// The sign-in attempts table holds the failed password attempts on a name within a
// window and the lock they earned, keyed by the name and never by a user row. These
// cases check, on every backend, that a failure is recorded under the one rule
// (dbtype.SignInAttempts.Failed) at the time the caller names, with the database's
// time on the write; that an unknown name is recorded as a known one; that a clear
// deletes the record; that concurrent failures each count, and as many of them as the
// limit are told they were counted (Counted) and no more; and that a driver whose table
// is not there says so instead of counting nothing.

// attemptsOpts compares records but for UpdatedAt, which the database writes and the
// cases check apart.
var attemptsOpts = cmp.Options{cmpopts.IgnoreFields(dbtype.SignInAttempts{}, "UpdatedAt")}

// attemptLimit is the limit these cases record under.
var attemptLimit = dbtype.SignInAttemptLimit{Failures: 3, Window: 15 * time.Minute, Lock: 10 * time.Minute}

func testSignInAttemptsRecorded(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	createUser(ctx, t, in.Driver, "pat@lakeside.edu")
	now := time.Now().Truncate(time.Microsecond)

	for _, name := range []string{"pat@lakeside.edu", "nobody@lakeside.edu"} {
		t.Run(name, func(t *testing.T) {
			if _, err := in.Driver.SignInAttempts(ctx, name); !httpio.HasNotFound(err) {
				t.Errorf("SignInAttempts() before any failure error = %v, want NotFound", err)
			}

			// The first failure opens the window; the second counts in it.
			w := startWindow(h.ClockLead)
			first, err := in.Driver.RecordSignInFailure(ctx, name, now, attemptLimit)
			w.done()
			if err != nil {
				t.Fatalf("RecordSignInFailure() error = %v", err)
			}
			want := &dbtype.SignInAttempts{Name: name, Failures: 1, WindowEndsAt: now.Add(attemptLimit.Window), Counted: true}
			if diff := cmp.Diff(want, first, attemptsOpts); diff != "" {
				t.Errorf("RecordSignInFailure() mismatch (-want +got):\n%s", diff)
			}
			w.assertStamped(t, "RecordSignInFailure().UpdatedAt", first.UpdatedAt)
			second, err := in.Driver.RecordSignInFailure(ctx, name, now.Add(time.Minute), attemptLimit)
			if err != nil {
				t.Fatalf("RecordSignInFailure() error = %v", err)
			}
			want.Failures = 2
			if diff := cmp.Diff(want, second, attemptsOpts); diff != "" {
				t.Errorf("second RecordSignInFailure() mismatch (-want +got):\n%s", diff)
			}
			read, err := in.Driver.SignInAttempts(ctx, name)
			if err != nil {
				t.Fatalf("SignInAttempts() error = %v", err)
			}
			// A record read back is the one written, but for Counted: the answer to the
			// call that recorded a failure, and no column.
			wantRead := *second
			wantRead.Counted = false
			if diff := cmp.Diff(&wantRead, read); diff != "" {
				t.Errorf("SignInAttempts() mismatch (-want +got):\n%s", diff)
			}

			// The failure that reaches the limit locks the name and is counted itself; one
			// during the lock is not counted, and changes nothing but the write's time.
			at := now.Add(2 * time.Minute)
			locked, err := in.Driver.RecordSignInFailure(ctx, name, at, attemptLimit)
			if err != nil {
				t.Fatalf("RecordSignInFailure() error = %v", err)
			}
			lockedUntil := at.Add(attemptLimit.Lock)
			want.Failures, want.LockedUntil = 3, &lockedUntil
			if diff := cmp.Diff(want, locked, attemptsOpts); diff != "" {
				t.Errorf("locking RecordSignInFailure() mismatch (-want +got):\n%s", diff)
			}
			during, err := in.Driver.RecordSignInFailure(ctx, name, at.Add(time.Minute), attemptLimit)
			if err != nil {
				t.Fatalf("RecordSignInFailure() error = %v", err)
			}
			want.Counted = false
			if diff := cmp.Diff(want, during, attemptsOpts); diff != "" {
				t.Errorf("RecordSignInFailure() during the lock mismatch (-want +got):\n%s", diff)
			}
			if !during.UpdatedAt.After(locked.UpdatedAt) {
				t.Errorf("UpdatedAt during the lock = %v, want it moved past %v", during.UpdatedAt, locked.UpdatedAt)
			}
		})
	}
}

func testSignInAttemptsWindowAndLockPass(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	now := time.Now().Truncate(time.Microsecond)

	tests := []struct {
		name string
		// before are the failures that set the record up, at their times.
		before []time.Duration
		// at is when the failure under test happens.
		at   time.Duration
		want func(name string, at time.Time) *dbtype.SignInAttempts
	}{
		{
			name:   "a failure after the window ended opens a new window",
			before: []time.Duration{0, time.Minute},
			at:     attemptLimit.Window + time.Second,
			want: func(name string, at time.Time) *dbtype.SignInAttempts {
				return &dbtype.SignInAttempts{Name: name, Failures: 1, WindowEndsAt: at.Add(attemptLimit.Window), Counted: true}
			},
		},
		{
			name:   "a failure after the lock passed opens a new window",
			before: []time.Duration{0, time.Minute, 2 * time.Minute},
			at:     2*time.Minute + attemptLimit.Lock + time.Second,
			want: func(name string, at time.Time) *dbtype.SignInAttempts {
				return &dbtype.SignInAttempts{Name: name, Failures: 1, WindowEndsAt: at.Add(attemptLimit.Window), Counted: true}
			},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := fmt.Sprintf("pat%d@lakeside.edu", i)
			for _, d := range tt.before {
				if _, err := in.Driver.RecordSignInFailure(ctx, name, now.Add(d), attemptLimit); err != nil {
					t.Fatalf("RecordSignInFailure() error = %v", err)
				}
			}
			at := now.Add(tt.at)
			got, err := in.Driver.RecordSignInFailure(ctx, name, at, attemptLimit)
			if err != nil {
				t.Fatalf("RecordSignInFailure() error = %v", err)
			}
			if diff := cmp.Diff(tt.want(name, at), got, attemptsOpts); diff != "" {
				t.Errorf("RecordSignInFailure() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// testSignInAttemptsEndsAt checks the column the row's removal is counted from: EndsAt is
// the end of the row's window or of its lock, whichever is later, worked out by the
// database and written by no store. A row removed a day after it never ends a lock
// early.
func testSignInAttemptsEndsAt(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	now := time.Now().Truncate(time.Microsecond)

	tests := []struct {
		name  string
		limit dbtype.SignInAttemptLimit
		want  time.Time
	}{
		{
			name:  "a row with no lock ends with its window",
			limit: dbtype.SignInAttemptLimit{Failures: 3, Window: 15 * time.Minute, Lock: time.Hour},
			want:  now.Add(15 * time.Minute),
		},
		{
			name:  "a lock that ends after the window ends the row",
			limit: dbtype.SignInAttemptLimit{Failures: 1, Window: 15 * time.Minute, Lock: time.Hour},
			want:  now.Add(time.Hour),
		},
		{
			name:  "a lock that ends before the window leaves the window's end",
			limit: dbtype.SignInAttemptLimit{Failures: 1, Window: time.Hour, Lock: 10 * time.Minute},
			want:  now.Add(time.Hour),
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := fmt.Sprintf("pat%d@lakeside.edu", i)
			if _, err := in.Driver.RecordSignInFailure(ctx, name, now, tt.limit); err != nil {
				t.Fatalf("RecordSignInFailure() error = %v", err)
			}
			if got := h.AttemptsEndsAt(ctx, t, in.Raw, name); !got.Equal(tt.want) {
				t.Errorf("EndsAt = %v, want %v", got, tt.want)
			}
		})
	}
}

func testSignInAttemptsCleared(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	now := time.Now()
	for range attemptLimit.Failures {
		if _, err := in.Driver.RecordSignInFailure(ctx, "pat@lakeside.edu", now, attemptLimit); err != nil {
			t.Fatalf("RecordSignInFailure() error = %v", err)
		}
	}
	if _, err := in.Driver.RecordSignInFailure(ctx, "sam@lakeside.edu", now, attemptLimit); err != nil {
		t.Fatalf("RecordSignInFailure() error = %v", err)
	}

	if err := in.Driver.ClearSignInAttempts(ctx, "pat@lakeside.edu"); err != nil {
		t.Fatalf("ClearSignInAttempts() error = %v", err)
	}
	if _, err := in.Driver.SignInAttempts(ctx, "pat@lakeside.edu"); !httpio.HasNotFound(err) {
		t.Errorf("SignInAttempts() after the clear error = %v, want NotFound", err)
	}
	if r, err := in.Driver.SignInAttempts(ctx, "sam@lakeside.edu"); err != nil || r.Failures != 1 {
		t.Errorf("SignInAttempts() of another name = %+v, %v; want it kept", r, err)
	}
	if err := in.Driver.ClearSignInAttempts(ctx, "unknown@lakeside.edu"); err != nil {
		t.Errorf("ClearSignInAttempts() of a name with no record error = %v, want none", err)
	}
	// The count starts over after a clear.
	if r, err := in.Driver.RecordSignInFailure(ctx, "pat@lakeside.edu", now.Add(time.Minute), attemptLimit); err != nil || r.Failures != 1 || r.LockedUntil != nil {
		t.Errorf("RecordSignInFailure() after the clear = %+v, %v; want one failure and no lock", r, err)
	}
}

// testSignInAttemptsKeyedExactly checks that a record is found under its name exactly as
// it was written, and under no other spelling. The session library keeps its own counts
// under keys spelled in capitals, which no name a person signs in under folds to, and
// that keeps a stranger's failed sign-ins away from them only while the table compares
// its keys exactly.
func testSignInAttemptsKeyedExactly(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	now := time.Now()
	const typed, own = "reset:pat@lakeside.edu", "RESET:pat@lakeside.edu"
	for range attemptLimit.Failures {
		if _, err := in.Driver.RecordSignInFailure(ctx, typed, now, attemptLimit); err != nil {
			t.Fatalf("RecordSignInFailure() error = %v", err)
		}
	}

	if _, err := in.Driver.SignInAttempts(ctx, own); !httpio.HasNotFound(err) {
		t.Errorf("SignInAttempts(%q) with %q locked error = %v, want NotFound: another spelling is another key", own, typed, err)
	}
	if r, err := in.Driver.RecordSignInFailure(ctx, own, now, attemptLimit); err != nil || r.Failures != 1 || r.LockedUntil != nil || !r.Counted {
		t.Errorf("RecordSignInFailure(%q) with %q locked = %+v, %v; want its first failure, counted and not locked", own, typed, r, err)
	}
	if err := in.Driver.ClearSignInAttempts(ctx, own); err != nil {
		t.Fatalf("ClearSignInAttempts() error = %v", err)
	}
	if r, err := in.Driver.SignInAttempts(ctx, typed); err != nil || r.Failures != int64(attemptLimit.Failures) || r.LockedUntil == nil {
		t.Errorf("SignInAttempts(%q) after %q was cleared = %+v, %v; want its failures and its lock kept", typed, own, r, err)
	}
}

func testSignInAttemptsConcurrent(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	limit := dbtype.SignInAttemptLimit{Failures: 100, Window: time.Hour, Lock: time.Hour}
	now := time.Now()

	const racers = 8
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed []error
	)
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := in.Driver.RecordSignInFailure(ctx, "pat@lakeside.edu", now, limit); err != nil {
				mu.Lock()
				failed = append(failed, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(failed) != 0 {
		t.Fatalf("%d concurrent RecordSignInFailure() calls: errors %v", racers, failed)
	}
	if r, err := in.Driver.SignInAttempts(ctx, "pat@lakeside.edu"); err != nil || r.Failures != racers {
		t.Errorf("SignInAttempts() after %d concurrent failures = %+v, %v; want each counted", racers, r, err)
	}
}

// testSignInAttemptsCountedAtOnce records more failures of one name at once than its
// limit and checks that the count is taken and checked in one step: every call
// succeeds, as many as the limit are told their failure was counted and no more, and
// the record stands at the limit, locked. A caller that judges a guess only when its
// failure was counted (a code's attempts) so judges no more than the limit, however
// many guesses arrive together.
func testSignInAttemptsCountedAtOnce(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	now := time.Now()

	const racers = 12
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		failed  []error
		counted int
	)
	for range racers {
		wg.Go(func() {
			record, err := in.Driver.RecordSignInFailure(ctx, "pat@lakeside.edu", now, attemptLimit)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				failed = append(failed, err)
			case record.Counted:
				counted++
			}
		})
	}
	wg.Wait()

	if len(failed) != 0 {
		t.Fatalf("%d concurrent RecordSignInFailure() calls: errors %v", racers, failed)
	}
	if counted != attemptLimit.Failures {
		t.Errorf("%d of %d failures recorded at once were told they were counted, want %d: the limit", counted, racers, attemptLimit.Failures)
	}
	r, err := in.Driver.SignInAttempts(ctx, "pat@lakeside.edu")
	if err != nil || r.Failures != int64(attemptLimit.Failures) || r.LockedUntil == nil {
		t.Errorf("SignInAttempts() after %d failures at once = %+v, %v; want %d failures and the name locked", racers, r, err, attemptLimit.Failures)
	}
}

// testSignInAttemptsRecordedBesideClears records failures of one name while the name is
// cleared, over and over and at once, and checks that no call fails: a failure recorded
// in the instant a clear deletes the name's row counts as the first after the clear. A
// right guess at a code takes its attempt and then clears the name, so two entries of
// the right code at once are a failure recorded beside a clear.
func testSignInAttemptsRecordedBesideClears(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	limit := dbtype.SignInAttemptLimit{Failures: 1000000, Window: time.Hour, Lock: time.Hour}
	now := time.Now()

	const (
		racers = 8
		rounds = 50
	)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed []error
	)
	for range racers {
		wg.Go(func() {
			for range rounds {
				_, err := in.Driver.RecordSignInFailure(ctx, "pat@lakeside.edu", now, limit)
				if err == nil {
					err = in.Driver.ClearSignInAttempts(ctx, "pat@lakeside.edu")
				}
				if err != nil {
					mu.Lock()
					failed = append(failed, err)
					mu.Unlock()

					return
				}
			}
		})
	}
	wg.Wait()

	if len(failed) != 0 {
		t.Fatalf("failures recorded beside clears of the name, %d at once: %d failed, the first with %v", racers, len(failed), failed[0])
	}
}

// testSignInAttemptsMissingTable points the driver at a sign-in attempts table that is
// not there and checks that every operation says so: an error naming the table, never a
// success, never the NotFound of a name with no record, and never the Spanner client's
// complaint about a transaction a failed read had ended, which is what taking a
// missing table for "no record yet" produced on every failed sign-in.
func testSignInAttemptsMissingTable(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	const table = "NoSuchTable"
	if err := in.Driver.SetSignInAttemptsTableName(table); err != nil {
		t.Fatalf("SetSignInAttemptsTableName() error = %v", err)
	}

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "SignInAttempts",
			run: func(t *testing.T) {
				_, err := in.Driver.SignInAttempts(ctx, "pat@lakeside.edu")
				wantMissingTableError(t, "SignInAttempts", table, err)
			},
		},
		{
			name: "RecordSignInFailure",
			run: func(t *testing.T) {
				_, err := in.Driver.RecordSignInFailure(ctx, "pat@lakeside.edu", time.Now(), attemptLimit)
				wantMissingTableError(t, "RecordSignInFailure", table, err)
			},
		},
		{
			name: "ClearSignInAttempts",
			run: func(t *testing.T) {
				err := in.Driver.ClearSignInAttempts(ctx, "pat@lakeside.edu")
				wantMissingTableError(t, "ClearSignInAttempts", table, err)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// wantMissingTableError asserts that err, the answer of operation op on a driver whose
// sign-in attempts table is table, says the table is missing: it is an error, it is not
// the NotFound of a name with no record, it names the table, and it is not the Spanner
// client's complaint about a write on a transaction a failed read had ended.
func wantMissingTableError(t *testing.T, op, table string, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s() error = nil, want one naming %s", op, table)
	}
	if httpio.HasNotFound(err) {
		t.Errorf("%s() error = %v, want the missing table told from a name with no record", op, err)
	}
	if !strings.Contains(err.Error(), table) {
		t.Errorf("%s() error = %v, want it to name %s", op, err, table)
	}
	if strings.Contains(err.Error(), "closed transaction") {
		t.Errorf("%s() error = %v, want the read's own error, not a write on the transaction it ended", op, err)
	}
}
