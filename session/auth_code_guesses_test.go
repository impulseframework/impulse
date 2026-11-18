package session

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// guessesAtOnce is how many guesses the tests of a code's limit make at once: more
// than twice the attempts either code is given.
const guessesAtOnce = 12

// guessKey is the context key a guess of these tests travels under: its number.
type guessKey struct{}

// together holds every guess of a test at the first thing it writes to the store, the
// attempt it takes or the write of its judgment, until all of them have reached
// theirs. The guesses are then in flight at once whatever order the Auth works in:
// each read the code while it stood, and none has been answered.
type together struct {
	mu      sync.Mutex
	arrived map[int]bool
	all     chan struct{}
}

// newTogether returns the hold of guessesAtOnce guesses.
func newTogether() *together {
	return &together{arrived: make(map[int]bool, guessesAtOnce), all: make(chan struct{})}
}

// arrive holds the guess of ctx until every guess has arrived. A guess arrives once: a
// later write of the same guess is not held, and neither is a context with no guess.
func (g *together) arrive(ctx context.Context) {
	guess, ok := ctx.Value(guessKey{}).(int)
	if !ok {
		return
	}
	g.mu.Lock()
	if !g.arrived[guess] {
		g.arrived[guess] = true
		if len(g.arrived) == guessesAtOnce {
			close(g.all)
		}
	}
	g.mu.Unlock()
	<-g.all
}

// fire makes guessesAtOnce guesses in parallel and returns what each was answered.
func fire(guess func(ctx context.Context, n int) error) []error {
	answers := make([]error, guessesAtOnce)
	var wg sync.WaitGroup
	for n := range guessesAtOnce {
		wg.Go(func() {
			answers[n] = guess(context.WithValue(context.Background(), guessKey{}, n), n)
		})
	}
	wg.Wait()

	return answers
}

// factorCodeRow is the factor codes table of one code as the store holds it: the
// attempt is counted and answered in one step, the consume takes the first asker, and
// neither touches a code that was consumed.
type factorCodeRow struct {
	mu       sync.Mutex
	row      sessionstorage.FactorCode
	together *together
	// taken counts the attempts the store counted, and consumes the consumes it was
	// asked for, whatever it answered.
	taken    int
	consumes atomic.Int64
}

// expectFactorCode gives the fixture's store one code standing for pending.
func expectFactorCode(f *authFixture, pending *pendingState, code string) *factorCodeRow {
	c := &factorCodeRow{
		row: sessionstorage.FactorCode{
			ID: impulse.Must(impulse.NewUUID()), PendingID: pending.ID, Email: "pat@lakeside.edu",
			CodeHash: hashEmailCode(pending.ID, code), ExpiresAt: time.Now().Add(time.Hour),
		},
		together: newTogether(),
	}
	f.store.EXPECT().FactorCode(gomock.Any(), pending.ID).DoAndReturn(c.read).AnyTimes()
	f.store.EXPECT().RecordFactorCodeAttempt(gomock.Any(), c.row.ID).DoAndReturn(c.attempt).AnyTimes()
	f.store.EXPECT().ConsumeFactorCode(gomock.Any(), c.row.ID).DoAndReturn(c.consume).AnyTimes()

	return c
}

func (c *factorCodeRow) read(context.Context, impulse.UUID) (*sessionstorage.FactorCode, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.row.ConsumedAt != nil {
		return nil, httpio.NewNotFoundMessage("no live factor code")
	}
	row := c.row

	return &row, nil
}

func (c *factorCodeRow) attempt(ctx context.Context, _ impulse.UUID) (int, error) {
	attempts, err := c.count()
	if err != nil {
		return 0, err
	}
	c.together.arrive(ctx)

	return attempts, nil
}

// count counts an attempt on the row and answers which it was, in one step.
func (c *factorCodeRow) count() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.row.ConsumedAt != nil {
		return 0, httpio.NewNotFoundMessage("no live factor code")
	}
	c.taken++
	c.row.Attempts = c.taken

	return c.taken, nil
}

func (c *factorCodeRow) consume(ctx context.Context, _ impulse.UUID) error {
	c.together.arrive(ctx)
	c.consumes.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.row.ConsumedAt != nil {
		return httpio.NewNotFoundMessage("no live factor code")
	}
	now := time.Now()
	c.row.ConsumedAt = &now

	return nil
}

// TestAuth_verifyEmailCode_guessesAtOnce holds the mailed sign-in code's limit under
// guesses made at once: each guess takes its attempt before it is judged, so no more
// guesses than the code is given are ever compared with it, however many arrive
// together.
func TestAuth_verifyEmailCode_guessesAtOnce(t *testing.T) {
	t.Parallel()

	const sent = "314159"

	t.Run("twelve guesses of the right code at once: five are judged, one is accepted and the rest find no code standing", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		pending := &pendingState{ID: impulse.Must(impulse.NewUUID())}
		code := expectFactorCode(f, pending, sent)

		answers := fire(func(ctx context.Context, _ int) error {
			_, err := f.auth.verifyEmailCode(ctx, pending, sent)

			return err
		})

		// A guess of the right code that was judged asked the store to consume the code.
		if judged := code.consumes.Load(); judged != emailCodeAttempts {
			t.Errorf("%d of %d guesses made at once were judged, want %d: the attempts the code is given", judged, guessesAtOnce, emailCodeAttempts)
		}
		if code.taken != guessesAtOnce {
			t.Errorf("%d attempts were taken, want %d: one for each guess", code.taken, guessesAtOnce)
		}
		accepted := 0
		for n, err := range answers {
			switch {
			case err == nil:
				accepted++
			case sessioninfo.LoginRefusalCodeOf(err) != sessioninfo.RefusedCodeExpired:
				t.Errorf("guess %d was answered %v, want it accepted or told no code stands", n, err)
			}
		}
		if accepted != 1 {
			t.Errorf("%d guesses were accepted, want one", accepted)
		}
	})

	t.Run("twelve wrong guesses at once: each takes an attempt, four are told the code is wrong, the fifth kills the code and the rest are not judged", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		pending := &pendingState{ID: impulse.Must(impulse.NewUUID())}
		code := expectFactorCode(f, pending, sent)

		answers := fire(func(ctx context.Context, _ int) error {
			_, err := f.auth.verifyEmailCode(ctx, pending, "271828")

			return err
		})

		if code.taken != guessesAtOnce {
			t.Errorf("%d attempts were taken, want %d: one for each guess", code.taken, guessesAtOnce)
		}
		// Only the guess that took the last attempt consumes the code: a guess past the
		// limit touches nothing.
		if consumes := code.consumes.Load(); consumes != 1 {
			t.Errorf("the store was asked to consume the code %d times, want once, by the guess that took the last attempt", consumes)
		}
		wrong, dead := 0, 0
		for n, err := range answers {
			switch sessioninfo.LoginRefusalCodeOf(err) {
			case sessioninfo.RefusedInvalidCode:
				wrong++
			case sessioninfo.RefusedCodeExpired:
				dead++
			default:
				t.Errorf("guess %d was answered %v, want a refusal of the code", n, err)
			}
		}
		if wrong != emailCodeAttempts-1 || dead != guessesAtOnce-emailCodeAttempts+1 {
			t.Errorf("%d guesses were told the code is wrong and %d that none stands, want %d and %d", wrong, dead, emailCodeAttempts-1, guessesAtOnce-emailCodeAttempts+1)
		}
		if code.row.ConsumedAt == nil {
			t.Error("the code stands after its attempts were spent")
		}
	})
}

// TestAuth_proveFirstPasswordCode_guessesAtOnce holds the limit on the first password's
// code under guesses made at once: each guess takes its attempt before it is judged, in
// the store's one step, so no more guesses than the limit are ever compared with the
// code, however many arrive together.
func TestAuth_proveFirstPasswordCode_guessesAtOnce(t *testing.T) {
	t.Parallel()

	// held is the fixture of one person with a first-password code mailed, every guess
	// of the test held together, and the code.
	held := func(t *testing.T) (f *authFixture, links *mailLinks, userID impulse.UUID, sent string) {
		t.Helper()
		userID, sessionID := newIDs()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		f.expectSession(sessionID, directoryPerson(userID))
		sent = sendFirstPasswordCode(t, f, recorder, sessionID)
		hold := newTogether()
		f.afterFailure = hold.arrive
		links.beforeRevoke = hold.arrive
		// The counters start at the guesses: mailing the code counted against the mailbox.
		links.revokes, f.attemptWrites = 0, 0

		return f, links, userID, sent
	}

	t.Run("twelve guesses of the right code at once: five are judged, one is accepted and nothing stays held against the account", func(t *testing.T) {
		t.Parallel()
		f, links, userID, sent := held(t)

		answers := fire(func(ctx context.Context, _ int) error {
			_, err := f.auth.proveFirstPasswordCode(ctx, userID, "pat@lakeside.edu", sent)

			return err
		})

		// A guess of the right code that was judged asked the store to end the code.
		if links.revokes != firstPasswordAttempts.Failures {
			t.Errorf("%d of %d guesses made at once were judged, want %d: the limit", links.revokes, guessesAtOnce, firstPasswordAttempts.Failures)
		}
		if f.attemptWrites != guessesAtOnce {
			t.Errorf("%d attempts were taken, want %d: one for each guess", f.attemptWrites, guessesAtOnce)
		}
		accepted, dead, tooMany := 0, 0, 0
		for n, err := range answers {
			switch {
			case err == nil:
				accepted++
			case httpio.HasConflict(err):
				dead++
			case httpio.HasTooManyRequests(err):
				tooMany++
			default:
				t.Errorf("guess %d was answered %v, want it accepted, told the code was used or told there were too many", n, err)
			}
		}
		limit := firstPasswordAttempts.Failures
		if accepted != 1 || dead != limit-1 || tooMany != guessesAtOnce-limit {
			t.Errorf("%d accepted, %d told the code was used, %d told there were too many; want 1, %d and %d", accepted, dead, tooMany, limit-1, guessesAtOnce-limit)
		}
		if record := f.attemptsRecord(firstPasswordKey(userID)); record != nil {
			t.Errorf("after the right code the account's record = %+v, want none", record)
		}
	})

	t.Run("twelve wrong guesses at once: each takes an attempt, four are told the code is wrong, the fifth ends the code and holds the account, and the rest are not judged", func(t *testing.T) {
		t.Parallel()
		f, links, userID, _ := held(t)

		answers := fire(func(ctx context.Context, _ int) error {
			_, err := f.auth.proveFirstPasswordCode(ctx, userID, "pat@lakeside.edu", "000000")

			return err
		})

		if f.attemptWrites != guessesAtOnce {
			t.Errorf("%d attempts were taken, want %d: one for each guess", f.attemptWrites, guessesAtOnce)
		}
		wrong, tooMany := 0, 0
		for n, err := range answers {
			switch {
			case httpio.HasBadRequest(err):
				wrong++
			case httpio.HasTooManyRequests(err):
				tooMany++
			default:
				t.Errorf("guess %d was answered %v, want it told the code is wrong or that there were too many", n, err)
			}
		}
		limit := firstPasswordAttempts.Failures
		if wrong != limit-1 || tooMany != guessesAtOnce-limit+1 {
			t.Errorf("%d told the code is wrong and %d that there were too many, want %d and %d", wrong, tooMany, limit-1, guessesAtOnce-limit+1)
		}
		// Only the guess that reached the limit ends the code: a guess past it touches nothing.
		if links.revokes != 1 || links.last(t).ConsumedAt == nil {
			t.Errorf("the store was asked to end the code %d times and the code ended = %t, want once and ended", links.revokes, links.last(t).ConsumedAt != nil)
		}
		record := f.attemptsRecord(firstPasswordKey(userID))
		if record == nil || record.Failures != int64(firstPasswordAttempts.Failures) || record.LockedUntil == nil {
			t.Errorf("the account's record = %+v, want %d failures and held", record, firstPasswordAttempts.Failures)
		}
	})
}
