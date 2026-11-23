package attempts

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

// fakeStore is a Store over a map, applying the rule as the drivers do, in one step, that
// counts its calls.
type fakeStore struct {
	mu      sync.Mutex
	now     func() time.Time
	records map[string]*dbtype.SignInAttempts
	writes  int
	clears  int
	fail    error
	// beforeClear, when set, is called as a clear arrives, before the record is deleted:
	// where a test makes another attempt while a clear is on its way.
	beforeClear func()
	// afterRecord, when set, is called once a failure was counted, before the store
	// answers: where a test holds attempts made at once.
	afterRecord func()
}

func newFakeStore(now func() time.Time) *fakeStore {
	return &fakeStore{now: now, records: map[string]*dbtype.SignInAttempts{}}
}

func (s *fakeStore) RecordSignInFailure(_ context.Context, name string, limit dbtype.SignInAttemptLimit) (*dbtype.SignInAttempts, error) {
	record, err := s.count(name, limit)
	if err == nil && s.afterRecord != nil {
		s.afterRecord()
	}

	return record, err
}

// count counts a failure of name in one step, as the drivers' transaction does, and
// returns a copy of the record as written.
func (s *fakeStore) count(name string, limit dbtype.SignInAttemptLimit) (*dbtype.SignInAttempts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.writes++
	if s.fail != nil {
		return nil, s.fail
	}
	next := s.records[name].Failed(name, s.now(), limit)
	s.records[name] = next
	copied := *next

	return &copied, nil
}

func (s *fakeStore) ClearSignInAttempts(ctx context.Context, name string) error {
	// A store does nothing for a request that was canceled.
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, "context.Context.Err()")
	}
	if s.beforeClear != nil {
		s.beforeClear()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clears++
	delete(s.records, name)

	return nil
}

func (s *fakeStore) record(name string) *dbtype.SignInAttempts {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.records[name]
}

// asked is how often the store was asked to count a failure.
func (s *fakeStore) asked() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writes
}

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.t = c.t.Add(d)
}

var testLimit = dbtype.SignInAttemptLimit{Failures: 3, Window: 15 * time.Minute, Lock: 10 * time.Minute}

func newLimiter(t *testing.T) (*Limiter, *fakeStore, *clock) {
	t.Helper()

	c := &clock{t: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	store := newFakeStore(c.now)
	l := New(testLimit, store)
	l.SetClock(c.now)

	return l, store, c
}

// take takes an attempt that must be given a place, and returns it.
func take(ctx context.Context, t *testing.T, l *Limiter, name string, client Client) *Attempt {
	t.Helper()

	taken, wait, err := l.Take(ctx, name, client)
	if err != nil || taken == nil {
		t.Fatalf("Take(%q) = %v, %v, %v; want the attempt taken", name, taken, wait, err)
	}

	return taken
}

// fail runs one failed attempt: it takes its place, and its password proves wrong, so
// the place stays taken.
func fail(ctx context.Context, t *testing.T, l *Limiter, name string, client Client) {
	t.Helper()

	take(ctx, t, l, name, client)
}

// succeed runs one attempt with the right password: it takes its place and gives it back.
func succeed(ctx context.Context, t *testing.T, l *Limiter, name string, client Client) {
	t.Helper()

	if err := l.Succeeded(ctx, take(ctx, t, l, name, client)); err != nil {
		t.Fatalf("Succeeded(%q) error = %v", name, err)
	}
}

// refused takes an attempt that must be refused, and returns the wait.
func refused(ctx context.Context, t *testing.T, l *Limiter, name string, client Client) time.Duration {
	t.Helper()

	taken, wait, err := l.Take(ctx, name, client)
	if err != nil || taken != nil {
		t.Fatalf("Take(%q) = %v, %v, %v; want the attempt refused", name, taken, wait, err)
	}

	return wait
}

// TestNormalize pins the key a name's attempts are counted under: a name with an @ in it
// with its ASCII capitals lowercased and every other character as written, any other in
// Unicode NFC and case-folded. The letters from outside ASCII are written as escapes so
// that each is the code point it is named for.
func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "an address's ASCII capitals are lowercased", in: "Pat@Lakeside.EDU", want: "pat@lakeside.edu"},
		{name: "an address keeps the Kelvin sign", in: "Kim@Lakeside.EDU", want: "Kim@lakeside.edu"},
		{name: "an address keeps the sharp s", in: "straße@lakeside.edu", want: "straße@lakeside.edu"},
		{name: "an address keeps an accented letter in two code points", in: "josé@Lakeside.edu", want: "josé@lakeside.edu"},
		{name: "an address keeps a capital letter from outside ASCII", in: "Émilie@lakeside.edu", want: "Émilie@lakeside.edu"},
		{name: "a name with no @ in it is case-folded, the Kelvin sign to a k", in: "Kim", want: "kim"},
		{name: "a name with no @ in it is case-folded, the sharp s to ss", in: "Straße", want: "strasse"},
		{name: "a kind's capitals before an address are lowercased", in: "MAIL:Émilie@lakeside.edu", want: "mail:Émilie@lakeside.edu"},
		{name: "a name with no @ in it is one name in a composed and a decomposed spelling", in: "résumé", want: "résumé"},
		{name: "a folded name stays as it is", in: "pat", want: "pat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%+q) = %+q, want %+q", tt.in, got, tt.want)
			}
		})
	}
}

// TestFoldWide pins the wide reading of an address that the counts of its mail are kept
// under: Unicode NFC and case-folded, so some addresses that only read alike are one.
func TestFoldWide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "ASCII capitals are lowercased", in: "Pat@Lakeside.EDU", want: "pat@lakeside.edu"},
		{name: "the Kelvin sign folds to a k", in: "Kim@Lakeside.EDU", want: "kim@lakeside.edu"},
		{name: "the sharp s folds to ss", in: "straße@lakeside.edu", want: "strasse@lakeside.edu"},
		{name: "an accented letter in two code points is composed", in: "josé@Lakeside.edu", want: "josé@lakeside.edu"},
		{name: "a capital letter from outside ASCII is folded", in: "Émilie@lakeside.edu", want: "émilie@lakeside.edu"},
		{name: "a name with no @ in it is folded the same way", in: "Kim", want: "kim"},
		{name: "the sharp s in a name with no @ in it folds to ss", in: "Straße", want: "strasse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := FoldWide(tt.in); got != tt.want {
				t.Errorf("FoldWide(%+q) = %+q, want %+q", tt.in, got, tt.want)
			}
		})
	}
}

// TestOwnKey pins what keeps the library's own counts apart from the names people sign in
// under: a key of the library's is spelled in capitals, so no name normalizes to one,
// whatever a person types, the key's own text and its capitals among it.
func TestOwnKey(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{KindMail, KindCode, KindInvitation, KindInvitationGuess, KindAddressGuess, KindCodeGuess, KindReset, KindFirstPassword} {
		key := OwnKey(kind, "pat@lakeside.edu")
		if !IsOwnKey(key) {
			t.Errorf("IsOwnKey(%q) = false, want the library's own count recognized", key)
		}
		for _, typed := range []string{key, strings.ToLower(key), strings.ToUpper(key), " " + key} {
			name := Normalize(typed)
			if name == key || IsOwnKey(name) {
				t.Errorf("a sign-in under the name %q is counted under %q, which is the library's own count %q", typed, name, key)
			}
		}
	}
	// A name with an @ in it keeps its letters from outside ASCII as written, and the
	// capitals of a kind before it are lowercased all the same.
	if typed := "MAIL:\u00c9milie@lakeside.edu"; IsOwnKey(Normalize(typed)) {
		t.Errorf("a sign-in under the name %+q is counted under %+q, which is one of the library's own counts", typed, Normalize(typed))
	}
	for _, name := range []string{"pat", "pat@lakeside.edu", "mail:pat@lakeside.edu", "reset:pat@lakeside.edu", "invitation:0f9a1c2e", "invitation-guess:0f9a1c2e", "address-guess:pat@lakeside.edu", "code-guess:pat@lakeside.edu", "first-password:0f9a1c2e", "MAILROOM", ""} {
		if IsOwnKey(name) {
			t.Errorf("IsOwnKey(%q) = true, want a name's count", name)
		}
	}
}

func TestLimiter_NameLock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, c *clock)
	}{
		{
			name: "the attempt that reaches the limit is taken and locks the name, and the next attempt is refused from memory with the wait and the store not asked",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, _ *clock) {
				for range testLimit.Failures - 1 {
					if taken := take(ctx, t, l, "Pat", Client{Address: "10.0.0.1"}); taken.Lock != 0 {
						t.Fatalf("Take() before the limit says the name is locked for %v, want no lock", taken.Lock)
					}
				}
				if taken := take(ctx, t, l, "Pat", Client{Address: "10.0.0.1"}); taken.Lock != testLimit.Lock {
					t.Errorf("Take() of the attempt that reaches the limit says the name is locked for %v, want the lock period %v", taken.Lock, testLimit.Lock)
				}
				asked := store.asked()
				if wait := refused(ctx, t, l, "pat", Client{Address: "10.0.0.1"}); wait != testLimit.Lock {
					t.Errorf("Take() after the limit waits %v, want the lock period %v", wait, testLimit.Lock)
				}
				if store.asked() != asked {
					t.Errorf("the store was asked %d times during the lock, want none beyond %d", store.asked(), asked)
				}
			},
		},
		{
			name: "a lock in the store is learned by the first attempt, which is not counted, refused from memory within the read window, and asked for again past it",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, c *clock) {
				lockedUntil := c.now().Add(4 * time.Minute)
				locked := dbtype.SignInAttempts{Name: "pat", Failures: 3, WindowEndsAt: c.now().Add(time.Hour), LockedUntil: &lockedUntil}
				store.records[Normalize("pat")] = &locked
				if wait := refused(ctx, t, l, "pat", Client{}); wait != 4*time.Minute {
					t.Fatalf("Take() waits %v, want the store's wait of 4m", wait)
				}
				asked := store.asked()
				c.advance(readTTL - time.Second)
				if wait := refused(ctx, t, l, "pat", Client{}); wait != 4*time.Minute-readTTL+time.Second {
					t.Errorf("Take() within the read window waits %v, want the rest of the lock", wait)
				}
				if store.asked() != asked {
					t.Errorf("the store was asked %d times, want %d: within the read window the lock answers from memory", store.asked(), asked)
				}
				c.advance(time.Minute)
				if wait := refused(ctx, t, l, "pat", Client{}); wait != 3*time.Minute-readTTL+time.Second {
					t.Errorf("Take() a minute later waits %v, want the rest of the lock", wait)
				}
				if store.asked() != asked+1 {
					t.Errorf("the store was asked %d times, want %d: past the read window it is asked again", store.asked(), asked+1)
				}
				if r := store.record(Normalize("pat")); r.Failures != locked.Failures || !r.LockedUntil.Equal(lockedUntil) {
					t.Errorf("the name's record after the refused attempts = %+v, want it as it was: an attempt during the lock is not counted", r)
				}
			},
		},
		{
			name: "a lock cleared in the store is gone from memory once the read window has passed",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, c *clock) {
				for range testLimit.Failures {
					fail(ctx, t, l, "pat", Client{})
				}
				if wait := refused(ctx, t, l, "pat", Client{}); wait != testLimit.Lock {
					t.Fatalf("Take() after the limit waits %v, want the lock period", wait)
				}
				// An administrator deletes the row, as another instance or the
				// application's own transaction does: this limiter is not told.
				store.mu.Lock()
				delete(store.records, Normalize("pat"))
				store.mu.Unlock()
				c.advance(readTTL - time.Second)
				if wait := refused(ctx, t, l, "pat", Client{}); wait == 0 {
					t.Error("Take() within the read window waits nothing, want the remembered lock still")
				}
				c.advance(time.Second)
				// The name starts again from nothing: one attempt does not lock it.
				if taken := take(ctx, t, l, "pat", Client{}); taken.Lock != 0 {
					t.Errorf("Take() once the read window has passed says the name is locked for %v, want no lock", taken.Lock)
				}
				if got := store.record(Normalize("pat")); got == nil || got.Failures != 1 {
					t.Errorf("the name's record after the clear and one attempt = %+v, want one failure", got)
				}
			},
		},
		{
			name: "the lock passes with time and the name may try again",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, c *clock) {
				for range testLimit.Failures {
					fail(ctx, t, l, "pat", Client{})
				}
				c.advance(testLimit.Lock + time.Second)
				take(ctx, t, l, "pat", Client{})
			},
		},
		{
			name: "failures outside the window do not add up",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, c *clock) {
				fail(ctx, t, l, "pat", Client{})
				fail(ctx, t, l, "pat", Client{})
				c.advance(testLimit.Window + time.Second)
				fail(ctx, t, l, "pat", Client{})
				if r := store.record(Normalize("pat")); r == nil || r.Failures != 1 {
					t.Errorf("record = %+v, want one failure in a new window", r)
				}
				take(ctx, t, l, "pat", Client{})
			},
		},
		{
			name: "a failure learned after its attempt was given back is counted, and the one that reaches the limit locks the name",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, _ *clock) {
				for i := range testLimit.Failures {
					succeed(ctx, t, l, "pat", Client{})
					lock, err := l.Failed(ctx, "pat", Client{})
					if err != nil {
						t.Fatalf("Failed() error = %v", err)
					}
					// Each right password before it reset the name, so each failure is the first.
					if r := store.record(Normalize("pat")); lock != 0 || r == nil || r.Failures != 1 {
						t.Fatalf("Failed() %d = %v with the record %+v, want no lock and one failure", i+1, lock, r)
					}
				}
				for range testLimit.Failures - 2 {
					if lock, err := l.Failed(ctx, "pat", Client{}); err != nil || lock != 0 {
						t.Fatalf("Failed() before the limit = %v, %v; want no lock", lock, err)
					}
				}
				if lock, err := l.Failed(ctx, "pat", Client{}); err != nil || lock != testLimit.Lock {
					t.Errorf("Failed() at the limit = %v, %v; want the lock period", lock, err)
				}
				if wait := refused(ctx, t, l, "pat", Client{}); wait != testLimit.Lock {
					t.Errorf("Take() after the failures reached the limit waits %v, want the lock period", wait)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l, store, c := newLimiter(t)
			tt.run(t.Context(), t, l, store, c)
		})
	}
}

// TestLimiter_NameResetAndCache covers what resets a name and how the store is asked.
func TestLimiter_NameResetAndCache(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, c *clock)
	}{
		{
			name: "a right password gives its attempt back and resets the name: the record goes, with the failures before it, and a name with no failures is left with none",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, _ *clock) {
				fail(ctx, t, l, "pat", Client{})
				fail(ctx, t, l, "pat", Client{})
				succeed(ctx, t, l, "pat", Client{})
				if store.clears != 1 || store.record(Normalize("pat")) != nil {
					t.Errorf("clears = %d, record = %v; want the record deleted once", store.clears, store.record(Normalize("pat")))
				}
				succeed(ctx, t, l, "sam", Client{})
				if store.record(Normalize("sam")) != nil {
					t.Errorf("record after a right password with no failure before it = %v, want none: the attempt was given back", store.record(Normalize("sam")))
				}
			},
		},
		{
			name: "a right password for a name that only reads like another leaves the other's count: each has its own",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, _ *clock) {
				// The Kelvin sign (U+212A) for the k: another address, so another name.
				const kim, alike = "kim@lakeside.edu", "\u212aim@lakeside.edu"
				fail(ctx, t, l, kim, Client{})
				fail(ctx, t, l, kim, Client{})
				fail(ctx, t, l, alike, Client{})
				succeed(ctx, t, l, alike, Client{})
				if r := store.record(Normalize(kim)); r == nil || r.Failures != 2 {
					t.Errorf("record of %+q after a right password for %+q = %+v, want its two failures", kim, alike, r)
				}
				if r := store.record(Normalize(alike)); r != nil {
					t.Errorf("record of %+q after its right password = %+v, want none", alike, r)
				}
			},
		},
		{
			name: "the right password that takes the attempt at the limit lifts the lock it earned: the next attempt goes on",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, _ *clock) {
				for range testLimit.Failures - 1 {
					fail(ctx, t, l, "pat", Client{})
				}
				taken := take(ctx, t, l, "pat", Client{})
				if taken.Lock != testLimit.Lock {
					t.Fatalf("Take() of the attempt that reaches the limit says the name is locked for %v, want the lock period", taken.Lock)
				}
				if wait := refused(ctx, t, l, "pat", Client{}); wait != testLimit.Lock {
					t.Fatalf("Take() while the attempt at the limit is compared waits %v, want the lock period", wait)
				}
				if err := l.Succeeded(ctx, taken); err != nil {
					t.Fatalf("Succeeded() error = %v", err)
				}
				if got := take(ctx, t, l, "pat", Client{}); got.Lock != 0 || store.record(Normalize("pat")).Failures != 1 {
					t.Errorf("Take() after the right password = lock %v with the record %+v, want the first attempt of a name with no failures", got.Lock, store.record(Normalize("pat")))
				}
			},
		},
		{
			name: "an attempt made while a right password's clear is on its way leaves no lock behind in memory: the next attempt goes on",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, _ *clock) {
				for range testLimit.Failures - 1 {
					fail(ctx, t, l, "pat", Client{})
				}
				taken := take(ctx, t, l, "pat", Client{})
				// The store still holds the lock the attempt at the limit earned when
				// another attempt arrives.
				store.beforeClear = func() {
					refused(ctx, t, l, "pat", Client{})
				}
				if err := l.Succeeded(ctx, taken); err != nil {
					t.Fatalf("Succeeded() error = %v", err)
				}
				store.beforeClear = nil
				take(ctx, t, l, "pat", Client{})
			},
		},
		{
			name: "a right password whose request was canceled gives its attempt back all the same",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, _ *clock) {
				request, cancel := context.WithCancel(ctx)
				taken := take(request, t, l, "pat", Client{})
				cancel()
				if err := l.Succeeded(request, taken); err != nil {
					t.Fatalf("Succeeded() of a canceled request error = %v, want the attempt given back", err)
				}
				if r := store.record(Normalize("pat")); r != nil {
					t.Errorf("record after the right password of a canceled request = %+v, want none", r)
				}
			},
		},
		{
			name: "a clear resets the name whatever memory holds",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, _ *clock) {
				for range testLimit.Failures {
					fail(ctx, t, l, "pat", Client{})
				}
				if err := l.Clear(ctx, "pat"); err != nil {
					t.Fatalf("Clear() error = %v", err)
				}
				take(ctx, t, l, "pat", Client{})
				if store.clears != 1 {
					t.Errorf("clears = %d, want 1", store.clears)
				}
			},
		},
		{
			name: "a name spelled in another case is the same name",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, _ *clock) {
				fail(ctx, t, l, "Pat@Lakeside.edu", Client{})
				fail(ctx, t, l, "pat@lakeside.EDU", Client{})
				fail(ctx, t, l, "PAT@LAKESIDE.EDU", Client{})
				if wait := refused(ctx, t, l, "pat@lakeside.edu", Client{}); wait == 0 {
					t.Error("Take() waits nothing after three failures under one name in three spellings, want the lock")
				}
			},
		},
		{
			name: "every attempt that goes on is counted in the store: nothing but a lock answers from memory",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, _ *clock) {
				fail(ctx, t, l, "pat", Client{})
				fail(ctx, t, l, "pat", Client{})
				if store.asked() != 2 || store.record(Normalize("pat")).Failures != 2 {
					t.Errorf("the store was asked %d times and holds %+v, want each attempt counted", store.asked(), store.record(Normalize("pat")))
				}
			},
		},
		{
			name: "a store failure is reported, and the attempt it refused leaves nothing counted against the client",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, _ *clock) {
				client := Client{Address: "10.0.0.1", Cookie: "tok"}
				store.fail = errors.New("store down")
				for range ClientMultiple * testLimit.Failures {
					if taken, _, err := l.Take(ctx, "pat", client); err == nil || taken != nil {
						t.Fatalf("Take() = %v, %v; want the store's failure and no attempt", taken, err)
					}
				}
				store.fail = nil
				take(ctx, t, l, "pat", client)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l, store, c := newLimiter(t)
			tt.run(t.Context(), t, l, store, c)
		})
	}
}

func TestLimiter_ClientKeys(t *testing.T) {
	t.Parallel()

	clientLimit := ClientMultiple * testLimit.Failures

	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, c *clock)
	}{
		{
			name: "early retries during a name's lock count against the client's address and cookie, not the name",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore, _ *clock) {
				client := Client{Address: "10.0.0.1", Cookie: "tok"}
				for range testLimit.Failures {
					fail(ctx, t, l, "pat", client)
				}
				locked := *store.record(Normalize("pat"))
				for range clientLimit {
					if wait := refused(ctx, t, l, "pat", client); wait == 0 {
						t.Fatal("Take() during the lock waits nothing, want a wait")
					}
				}
				if r := store.record(Normalize("pat")); *r != locked {
					t.Errorf("the name's record changed during the retries: %+v, want %+v", r, locked)
				}
				// The client is now locked on its own: another name from it is refused
				// with the store not asked, and each retry extends the client's lock.
				asked := store.asked()
				if wait := refused(ctx, t, l, "sam", client); wait != testLimit.Lock {
					t.Errorf("Take() of another name from the locked client waits %v, want the lock period", wait)
				}
				if store.asked() != asked {
					t.Errorf("the store was asked %d times for a locked client, want none beyond %d", store.asked(), asked)
				}
			},
		},
		{
			name: "a client's failures across names lock the client at the client's limit, and a right password for one name leaves the client's count as it was",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, _ *clock) {
				client := Client{Address: "10.0.0.1"}
				for i := range clientLimit - 1 {
					fail(ctx, t, l, fmt.Sprintf("name-%d", i), client)
				}
				// One short of the client's limit: a right password takes the place at
				// the limit, is compared, and gives the place back.
				succeed(ctx, t, l, "another", client)
				succeed(ctx, t, l, "name-0", client)
				fail(ctx, t, l, "last", client)
				if wait := refused(ctx, t, l, "yet-another", client); wait != testLimit.Lock {
					t.Errorf("Take() at the client's limit waits %v, want the lock period", wait)
				}
			},
		},
		{
			name: "right passwords from one address never hold it: more of them than the client's limit, one after another",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, _ *clock) {
				client := Client{Address: "10.0.0.1", Cookie: "tok"}
				for i := range 3 * clientLimit {
					succeed(ctx, t, l, fmt.Sprintf("name-%d", i), client)
				}
			},
		},
		{
			name: "the right password that takes the place at the client's limit lifts the lock it earned, and a wrong one keeps it",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, _ *clock) {
				client := Client{Cookie: "tok"}
				for i := range clientLimit - 1 {
					fail(ctx, t, l, fmt.Sprintf("name-%d", i), client)
				}
				taken := take(ctx, t, l, "right", client)
				if wait := refused(ctx, t, l, "meanwhile", client); wait != testLimit.Lock {
					t.Fatalf("Take() while the attempt at the client's limit is compared waits %v, want the lock period", wait)
				}
				if err := l.Succeeded(ctx, taken); err != nil {
					t.Fatalf("Succeeded() error = %v", err)
				}
				fail(ctx, t, l, "wrong", client)
				if wait := refused(ctx, t, l, "after", client); wait != testLimit.Lock {
					t.Errorf("Take() after the wrong password at the client's limit waits %v, want the lock period", wait)
				}
			},
		},
		{
			name: "once a client's lock has passed while its window is open, a right password goes on and leaves the client as it was, and a wrong one locks it again",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, c *clock) {
				client := Client{Address: "10.0.0.1"}
				for i := range clientLimit {
					fail(ctx, t, l, fmt.Sprintf("name-%d", i), client)
				}
				// The lock is shorter than the window: it passes with the count still at
				// the limit.
				c.advance(testLimit.Lock + time.Second)
				for i := range 3 {
					succeed(ctx, t, l, fmt.Sprintf("right-%d", i), client)
				}
				fail(ctx, t, l, "wrong", client)
				if wait := refused(ctx, t, l, "after", client); wait != testLimit.Lock {
					t.Errorf("Take() after a wrong password past the client's limit waits %v, want the lock period", wait)
				}
			},
		},
		{
			name: "a right password still being compared when wrong ones reach the client's limit takes its place off the count, and the lock the count no longer earns is lifted",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, _ *clock) {
				client := Client{Address: "10.0.0.1"}
				slow := take(ctx, t, l, "slow", client)
				for i := range clientLimit - 1 {
					fail(ctx, t, l, fmt.Sprintf("name-%d", i), client)
				}
				if wait := refused(ctx, t, l, "meanwhile", client); wait != testLimit.Lock {
					t.Fatalf("Take() at the client's limit waits %v, want the lock period", wait)
				}
				if err := l.Succeeded(ctx, slow); err != nil {
					t.Fatalf("Succeeded() error = %v", err)
				}
				// One short of the limit in wrong passwords: the next one reaches it.
				fail(ctx, t, l, "last", client)
				if wait := refused(ctx, t, l, "after", client); wait != testLimit.Lock {
					t.Errorf("Take() after the client's limit of wrong passwords waits %v, want the lock period", wait)
				}
			},
		},
		{
			name: "a lock a later failure earned is not lifted by a right password whose own lock had passed",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, c *clock) {
				client := Client{Address: "10.0.0.1"}
				for i := range clientLimit - 1 {
					fail(ctx, t, l, fmt.Sprintf("name-%d", i), client)
				}
				// The attempt at the limit locks the client, and is still being compared
				// when its lock passes and a failure past the limit locks the client again.
				slow := take(ctx, t, l, "slow", client)
				c.advance(testLimit.Lock + time.Second)
				if _, err := l.Failed(ctx, "another", client); err != nil {
					t.Fatalf("Failed() error = %v", err)
				}
				if err := l.Succeeded(ctx, slow); err != nil {
					t.Fatalf("Succeeded() error = %v", err)
				}
				if wait := refused(ctx, t, l, "after", client); wait != testLimit.Lock {
					t.Errorf("Take() after the failure past the client's limit waits %v, want the lock period", wait)
				}
			},
		},
		{
			name: "an attempt given back after its window ended takes nothing off the next window's count",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, c *clock) {
				client := Client{Address: "10.0.0.1"}
				early := take(ctx, t, l, "slow", client)
				c.advance(testLimit.Window + time.Second)
				for i := range clientLimit - 1 {
					fail(ctx, t, l, fmt.Sprintf("name-%d", i), client)
				}
				if err := l.Succeeded(ctx, early); err != nil {
					t.Fatalf("Succeeded() error = %v", err)
				}
				// The new window holds one short of the limit still: the next wrong
				// password reaches it.
				fail(ctx, t, l, "last", client)
				if wait := refused(ctx, t, l, "after", client); wait != testLimit.Lock {
					t.Errorf("Take() after the client's limit of wrong passwords in one window waits %v, want the lock period", wait)
				}
			},
		},
		{
			name: "a client's lock extends with every retry while the name's wait is reported when longer",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, c *clock) {
				client := Client{Cookie: "tok"}
				for range clientLimit {
					fail(ctx, t, l, fmt.Sprintf("n%d", c.now().UnixNano()), client)
					c.advance(time.Millisecond)
				}
				c.advance(testLimit.Lock - time.Minute)
				if wait := refused(ctx, t, l, "x", client); wait != testLimit.Lock {
					t.Errorf("Take() during the client's lock waits %v, want it extended to the full lock period", wait)
				}
				c.advance(testLimit.Lock + time.Second)
				take(ctx, t, l, "x", client)
			},
		},
		{
			name: "a client cycling cookies is held by its address; one with no keys is held by the name alone",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, _ *clock) {
				for i := range clientLimit {
					fail(ctx, t, l, fmt.Sprintf("n%d", i), Client{Address: "10.0.0.1", Cookie: fmt.Sprintf("c%d", i)})
				}
				if wait := refused(ctx, t, l, "x", Client{Address: "10.0.0.1", Cookie: "fresh"}); wait == 0 {
					t.Error("Take() from the address with a fresh cookie waits nothing, want the address's lock")
				}
				take(ctx, t, l, "x", Client{})
			},
		},
		{
			name: "the memory is bounded: a flood of names evicts the oldest entries and holds no more than the cap",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore, _ *clock) {
				for i := range cacheSize + 100 {
					take(ctx, t, l, fmt.Sprintf("flood-%d", i), Client{Address: fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255)})
				}
				l.mu.Lock()
				n := len(l.entries)
				_, first := l.entries[nameKey(Normalize("flood-0"))]
				l.mu.Unlock()
				if n > cacheSize {
					t.Errorf("entries = %d, want at most %d", n, cacheSize)
				}
				if first {
					t.Error("the first name of the flood is still in memory, want it evicted")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l, store, c := newLimiter(t)
			tt.run(t.Context(), t, l, store, c)
		})
	}
}

// TestLimiter_TakenAtOnce holds the limits under attempts made at once: the place is
// taken before the password is compared, in one step for the name (the store's) and in
// one step for the client's keys (the limiter's), so of attempts that arrive together as
// many as the limit are taken and no more.
func TestLimiter_TakenAtOnce(t *testing.T) {
	t.Parallel()

	clientLimit := ClientMultiple * testLimit.Failures
	// atOnce takes n attempts at once and returns how many were taken and how many refused.
	atOnce := func(ctx context.Context, t *testing.T, l *Limiter, n int, attempt func(i int) (string, Client)) (taken, refused int) {
		t.Helper()

		var (
			wg     sync.WaitGroup
			mu     sync.Mutex
			failed []error
		)
		for i := range n {
			wg.Go(func() {
				name, client := attempt(i)
				got, wait, err := l.Take(ctx, name, client)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err != nil:
					failed = append(failed, err)
				case got != nil:
					taken++
				case wait > 0:
					refused++
				default:
					failed = append(failed, errors.New("an attempt was refused with no wait"))
				}
			})
		}
		wg.Wait()
		if len(failed) != 0 {
			t.Fatalf("%d attempts at once: errors %v", n, failed)
		}

		return taken, refused
	}

	tests := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore)
	}{
		{
			name: "of a dozen attempts on one name at once, as many as the limit are taken and the rest are refused; the name is locked",
			run: func(ctx context.Context, t *testing.T, l *Limiter, store *fakeStore) {
				// Every attempt is held at the count it takes in the store until all of
				// them have taken theirs: none was answered, so none found the name locked
				// in memory, and the store's answer alone decides which go on.
				var counted sync.WaitGroup
				counted.Add(12)
				all := make(chan struct{})
				go func() {
					counted.Wait()
					close(all)
				}()
				// An attempt the others never join goes on after a while, so a change
				// that keeps one from the store fails the counts and does not hang.
				store.afterRecord = func() {
					counted.Done()
					select {
					case <-all:
					case <-time.After(10 * time.Second):
					}
				}
				taken, refused := atOnce(ctx, t, l, 12, func(int) (string, Client) {
					return "pat", Client{}
				})
				if store.asked() != 12 {
					t.Errorf("the store was asked %d times, want 12: each attempt asked for its place", store.asked())
				}
				if taken != testLimit.Failures || refused != 12-testLimit.Failures {
					t.Errorf("%d taken and %d refused, want %d and %d", taken, refused, testLimit.Failures, 12-testLimit.Failures)
				}
				if r := store.record(Normalize("pat")); r == nil || r.Failures != int64(testLimit.Failures) || r.LockedUntil == nil {
					t.Errorf("the name's record = %+v, want %d failures and the name locked", r, testLimit.Failures)
				}
			},
		},
		{
			name: "of attempts at once from one client, each on another name, as many as the client's limit are taken and the rest are refused",
			run: func(ctx context.Context, t *testing.T, l *Limiter, _ *fakeStore) {
				taken, refused := atOnce(ctx, t, l, clientLimit+10, func(i int) (string, Client) {
					return fmt.Sprintf("name-%d", i), Client{Address: "10.0.0.1", Cookie: "tok"}
				})
				if taken != clientLimit || refused != 10 {
					t.Errorf("%d taken and %d refused, want %d and 10", taken, refused, clientLimit)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l, store, _ := newLimiter(t)
			tt.run(t.Context(), t, l, store)
		})
	}
}

// TestLimiter_Count holds the count of requests that are attempts on no name: the client's
// keys alone, at the client's limit, with the store never asked.
func TestLimiter_Count(t *testing.T) {
	t.Parallel()

	clientLimit := ClientMultiple * testLimit.Failures

	tests := []struct {
		name string
		run  func(t *testing.T, l *Limiter, store *fakeStore, c *clock)
	}{
		{
			name: "the request that reaches the client's limit is held for the lock, and every later one extends the hold",
			run: func(t *testing.T, l *Limiter, store *fakeStore, c *clock) {
				client := Client{Address: "10.0.0.1", Cookie: "tok"}
				for i := range clientLimit - 1 {
					if wait := l.Count(client); wait != 0 {
						t.Fatalf("Count() number %d waits %v, want nothing under the client's limit", i+1, wait)
					}
				}
				if wait := l.Count(client); wait != testLimit.Lock {
					t.Fatalf("Count() at the client's limit waits %v, want the lock period %v", wait, testLimit.Lock)
				}
				c.advance(time.Minute)
				if wait := l.Count(client); wait != testLimit.Lock {
					t.Errorf("Count() during the hold waits %v, want the lock period again: the hold is extended", wait)
				}
				// Past the end of the first hold, the one the later request extended still holds.
				c.advance(testLimit.Lock - 30*time.Second)
				if wait := l.Count(client); wait != testLimit.Lock {
					t.Errorf("Count() past the first hold's end waits %v, want the extended hold's lock period", wait)
				}
				if store.asked() != 0 {
					t.Errorf("the store was asked %d times, want none", store.asked())
				}
			},
		},
		{
			name: "a new window starts the count again",
			run: func(t *testing.T, l *Limiter, _ *fakeStore, c *clock) {
				client := Client{Address: "10.0.0.1"}
				for range clientLimit - 1 {
					l.Count(client)
				}
				c.advance(testLimit.Window)
				for i := range clientLimit - 1 {
					if wait := l.Count(client); wait != 0 {
						t.Fatalf("Count() number %d of the new window waits %v, want nothing: the window's count starts again", i+1, wait)
					}
				}
				if wait := l.Count(client); wait != testLimit.Lock {
					t.Errorf("Count() at the new window's limit waits %v, want the lock period", wait)
				}
			},
		},
		{
			name: "a client with no address and no cookie is never held",
			run: func(t *testing.T, l *Limiter, _ *fakeStore, _ *clock) {
				for i := range 3 * clientLimit {
					if wait := l.Count(Client{}); wait != 0 {
						t.Fatalf("Count() number %d of a client with no keys waits %v, want nothing", i+1, wait)
					}
				}
			},
		},
		{
			name: "a client held by one limiter's count is free on another, and its sign-in there is taken",
			run: func(t *testing.T, l *Limiter, store *fakeStore, c *clock) {
				client := Client{Address: "10.0.0.1", Cookie: "tok"}
				for range clientLimit {
					l.Count(client)
				}
				if wait := l.Count(client); wait == 0 {
					t.Fatal("Count() past the client's limit waits nothing, want the hold")
				}
				other := New(testLimit, store)
				other.SetClock(c.now)
				if wait := other.Count(client); wait != 0 {
					t.Errorf("Count() on another limiter waits %v, want nothing: the hold is the first limiter's", wait)
				}
				take(t.Context(), t, other, "pat", client)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l, store, c := newLimiter(t)
			tt.run(t, l, store, c)
		})
	}
}
