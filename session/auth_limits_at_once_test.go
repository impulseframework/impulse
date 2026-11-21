package session

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/attempts"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// The mail limit and the password attempt limit under requests made at once. Each
// request takes its place under the limit before the thing the limit bounds is done, a
// message sent or a password compared, in the store's one step, so no more than the
// limit are ever sent or compared, however many requests arrive together.

// TestAuth_limitMail_askedAtOnce holds the mail limit under requests made at once,
// through every caller that mails a code, a link or a notice to an address: of a dozen
// asked together, as many as the limit are mailed and every other is refused with
// too_many_emails.
func TestAuth_limitMail_askedAtOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// ask gives the fixture what the caller reads and returns the address the mail
		// goes to and the request made a dozen times at once.
		ask func(f *authFixture, links *mailLinks) (email string, request func(ctx context.Context) error)
	}{
		{
			name: "a sign-up's link",
			ask: func(f *authFixture, links *mailLinks) (string, func(ctx context.Context) error) {
				links.expectAccounts(f)

				return "new@lakeside.edu", func(ctx context.Context) error {
					return f.auth.startSignUp(ctx, "new@lakeside.edu")
				}
			},
		},
		{
			name: "the notice to the holder of an address a sign-up names",
			ask: func(f *authFixture, links *mailLinks) (string, func(ctx context.Context) error) {
				links.holds("pat@lakeside.edu", pat(impulse.Must(impulse.NewUUID())))
				links.expectAccounts(f)

				return "pat@lakeside.edu", func(ctx context.Context) error {
					return f.auth.startSignUp(ctx, "pat@lakeside.edu")
				}
			},
		},
		{
			name: "the verification link of an address a person adds",
			ask: func(f *authFixture, links *mailLinks) (string, func(ctx context.Context) error) {
				links.expectAccounts(f)
				user := &sessioninfo.UserInfo{ID: impulse.Must(impulse.NewUUID()), Username: "pat"}

				return "new@lakeside.edu", func(ctx context.Context) error {
					return f.auth.sendVerification(ctx, user, "new@lakeside.edu")
				}
			},
		},
		{
			name: "the notice to the holder of an address a person adds",
			ask: func(f *authFixture, links *mailLinks) (string, func(ctx context.Context) error) {
				links.holds("taken@lakeside.edu", &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "sam", Email: strPtr("taken@lakeside.edu")})
				links.expectAccounts(f)
				user := &sessioninfo.UserInfo{ID: impulse.Must(impulse.NewUUID()), Username: "pat"}

				return "taken@lakeside.edu", func(ctx context.Context) error {
					return f.auth.sendVerification(ctx, user, "taken@lakeside.edu")
				}
			},
		},
		{
			name: "a sign-in code",
			ask: func(f *authFixture, _ *mailLinks) (string, func(ctx context.Context) error) {
				f.store.EXPECT().InsertFactorCode(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				pending := &pendingState{ID: impulse.Must(impulse.NewUUID())}

				return "pat@lakeside.edu", func(ctx context.Context) error {
					_, err := f.auth.sendEmailCode(ctx, pending, "pat@lakeside.edu")

					return err
				}
			},
		},
		{
			name: "the notice to the holder of an address given at the sign-in code's step",
			ask: func(f *authFixture, links *mailLinks) (string, func(ctx context.Context) error) {
				userID := impulse.Must(impulse.NewUUID())
				f.store.EXPECT().User(gomock.Any(), userID).Return(pat(userID), nil).AnyTimes()
				links.holds("taken@lakeside.edu", &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "sam", Email: strPtr("taken@lakeside.edu")})
				links.expectAccounts(f)
				pending := &pendingState{ID: impulse.Must(impulse.NewUUID()), UserID: impulse.NullUUIDFromUUID(userID), Identity: sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()}}

				return "taken@lakeside.edu", func(ctx context.Context) error {
					_, err := f.auth.enrollEmail(ctx, pending, "taken@lakeside.edu")

					return err
				}
			},
		},
		{
			name: "the code of a first password",
			ask: func(f *authFixture, _ *mailLinks) (string, func(ctx context.Context) error) {
				userID := impulse.Must(impulse.NewUUID())
				f.store.EXPECT().User(gomock.Any(), userID).Return(directoryPerson(userID), nil).AnyTimes()

				return "pat@lakeside.edu", func(ctx context.Context) error {
					_, err := f.auth.sendFirstPasswordCode(ctx, userID)

					return err
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, recorder, links := newMailFixture(t, gomock.NewController(t))
			email, request := tt.ask(f, links)
			// Every request is held at the first thing it asks the store about the
			// address's mail, its count or a read of its record, until all of them have
			// asked: none has mailed or been answered when the last one arrives.
			hold := newTogether()
			f.afterFailure = hold.arrive
			f.afterRead = func(ctx context.Context, name string) {
				if name == mailLimitKey(email) {
					hold.arrive(ctx)
				}
			}

			answers := fire(func(ctx context.Context, _ int) error {
				return request(ctx)
			})

			mailed, refused := 0, 0
			for n, err := range answers {
				switch {
				case err == nil:
					mailed++
				case sessioninfo.LoginRefusalCodeOf(err) == sessioninfo.RefusedTooManyEmails && httpio.HasTooManyRequests(err):
					refused++
				default:
					t.Errorf("request %d was answered %v, want it mailed or refused with too_many_emails", n, err)
				}
			}
			if got := len(recorder.Messages()); got != mailLimit.Failures {
				t.Errorf("%d messages were sent for %d requests made at once, want %d: the limit", got, guessesAtOnce, mailLimit.Failures)
			}
			if mailed != mailLimit.Failures || refused != guessesAtOnce-mailLimit.Failures {
				t.Errorf("%d requests were answered as mailed and %d refused, want %d and %d", mailed, refused, mailLimit.Failures, guessesAtOnce-mailLimit.Failures)
			}
			record := f.attemptsRecord(mailLimitKey(email))
			if record == nil || record.Failures != int64(mailLimit.Failures) || record.LockedUntil == nil {
				t.Errorf("the address's record = %+v, want %d counted and the address held", record, mailLimit.Failures)
			}
		})
	}
}

// comparisons holds the password guesses of a test at the comparison of the password
// until every guess has either reached it or been answered without it, and counts the
// ones that reached it: the guesses that were judged. The guesses are then in flight at
// once whatever order the Auth works in: none that is judged has been answered when the
// last one arrives.
type comparisons struct {
	mu       sync.Mutex
	guesses  int
	reached  int
	answered int
	all      chan struct{}
	once     sync.Once
}

// holdComparisons wraps the fixture's password comparison to hold guesses of them.
func holdComparisons(f *authFixture, guesses int) *comparisons {
	c := &comparisons{guesses: guesses, all: make(chan struct{})}
	compare := f.auth.methods.compare
	f.auth.methods.compare = func(hash *securehash.Hash, password string) (bool, error) {
		c.arrive(func() { c.reached++ })

		return compare(hash, password)
	}

	return c
}

// arrive counts a guess by count and holds it until every guess of the test is
// accounted for. A guess that is held was not answered yet, so a guess answered while
// the others are held is one that never reached the comparison. A guess the others
// never join goes on after a while, so a change that loses a guess fails the test's
// counts and does not hang it.
func (c *comparisons) arrive(count func()) {
	c.mu.Lock()
	count()
	if c.reached+c.answered >= c.guesses {
		c.once.Do(func() { close(c.all) })
	}
	c.mu.Unlock()
	select {
	case <-c.all:
	case <-time.After(10 * time.Second):
	}
}

// answer counts a guess that was answered.
func (c *comparisons) answer() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.answered++
	if c.reached+c.answered >= c.guesses {
		c.once.Do(func() { close(c.all) })
	}
}

// judged is how many guesses reached the comparison.
func (c *comparisons) judged() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.reached
}

// guessAtOnce makes guessesAtOnce password guesses at once, held at the comparison, and
// returns how many were judged and how each was answered: accepted, told the password
// is wrong, or refused with too_many_attempts.
func guessAtOnce(t *testing.T, f *authFixture, guess func(ctx context.Context, n int) error) (judged, accepted, wrong, refused int) {
	t.Helper()

	held := holdComparisons(f, guessesAtOnce)
	answers := fire(func(ctx context.Context, n int) error {
		defer held.answer()

		return guess(ctx, n)
	})
	for n, err := range answers {
		switch {
		case err == nil:
			accepted++
		case sessioninfo.LoginRefusalCodeOf(err) == sessioninfo.RefusedTooManyAttempts && httpio.HasTooManyRequests(err):
			refused++
		case httpio.HasUnauthorized(err) || httpio.HasBadRequest(err):
			wrong++
		default:
			t.Errorf("guess %d was answered %v, want it accepted, told the password is wrong or refused with too_many_attempts", n, err)
		}
	}

	return held.judged(), accepted, wrong, refused
}

// TestAuth_passwordGuessesAtOnce holds the password attempt limit under guesses made at
// once: each guess takes its attempt, under the name and under the client's keys, before
// the password is compared, so no more guesses than the limit are ever compared with a
// password, however many arrive together; a right password gives its attempt back and
// resets the name, so a person's own sign-ins leave nothing held.
func TestAuth_passwordGuessesAtOnce(t *testing.T) {
	t.Parallel()

	const limit = 3
	userID := impulse.Must(impulse.NewUUID())
	hash := hashed(t, "pw")
	user := func(name string) *sessionstorage.SessionUser {
		return &sessionstorage.SessionUser{ID: userID, Username: name, PasswordHash: hash}
	}
	// everyone makes the store hold a user with the password "pw" under every name.
	everyone := func(f *authFixture) {
		f.store.EXPECT().UserByUserName(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, name string) (*sessionstorage.SessionUser, error) {
			return user(name), nil
		}).AnyTimes()
	}
	check := func(ctx context.Context, f *authFixture, name, password string, client attempts.Client) error {
		_, _, err := f.auth.checkCredentials(ctx, name, password, client)

		return err
	}
	held := func(t *testing.T, f *authFixture, name string) {
		t.Helper()
		if r := f.attemptsRecord(name); r == nil || r.Failures != limit || r.LockedUntil == nil {
			t.Errorf("the name's record = %+v, want %d failures and the name held", r, limit)
		}
	}
	// together holds every guess at the count it takes in the store until all of them
	// have taken theirs, for the cases whose guesses all name one name: none was
	// answered, so none found the name held in memory, and the store's answer alone
	// decides which are compared.
	together := func(f *authFixture) {
		f.afterFailure = newTogether().arrive
	}

	tests := []struct {
		name string
		// failures is the name limit's failures the fixture's password method is registered with.
		failures int
		run      func(t *testing.T, f *authFixture)
	}{
		{
			name:     "twelve wrong passwords for one name at once: three are compared, the limit, the rest are refused unjudged, and the name is held",
			failures: limit,
			run: func(t *testing.T, f *authFixture) {
				everyone(f)
				together(f)
				judged, accepted, wrong, refused := guessAtOnce(t, f, func(ctx context.Context, _ int) error {
					return check(ctx, f, "pat", "wrong", attempts.Client{})
				})
				if judged != limit {
					t.Errorf("%d of %d wrong passwords entered at once were compared, want %d: the limit", judged, guessesAtOnce, limit)
				}
				if accepted != 0 || wrong != limit || refused != guessesAtOnce-limit {
					t.Errorf("%d accepted, %d told the password is wrong, %d refused; want 0, %d and %d", accepted, wrong, refused, limit, guessesAtOnce-limit)
				}
				held(t, f, "pat")
			},
		},
		{
			name:     "twelve wrong passwords at once for a name no user holds: three are compared with the dummy hash and the rest are refused unjudged",
			failures: limit,
			run: func(t *testing.T, f *authFixture) {
				f.store.EXPECT().UserByUserName(gomock.Any(), "nobody").Return(nil, httpio.NewNotFoundMessage("no such user")).AnyTimes()
				together(f)
				judged, accepted, wrong, refused := guessAtOnce(t, f, func(ctx context.Context, _ int) error {
					return check(ctx, f, "nobody", "pw", attempts.Client{})
				})
				if judged != limit || accepted != 0 || wrong != limit || refused != guessesAtOnce-limit {
					t.Errorf("%d compared, %d accepted, %d told the password is wrong, %d refused; want %d, 0, %d and %d", judged, accepted, wrong, refused, limit, limit, guessesAtOnce-limit)
				}
				held(t, f, "nobody")
			},
		},
		{
			name:     "twelve wrong proofs of a signed-in person's password at once: three are compared and the rest are refused unjudged",
			failures: limit,
			run: func(t *testing.T, f *authFixture) {
				together(f)
				judged, accepted, wrong, refused := guessAtOnce(t, f, func(ctx context.Context, _ int) error {
					isWrong, err := f.auth.provePassword(ctx, user("pat"), "wrong", attempts.Client{})
					if isWrong {
						return httpio.NewBadRequestMessageWithError(err, "Password incorrect")
					}

					return err
				})
				if judged != limit || accepted != 0 || wrong != limit || refused != guessesAtOnce-limit {
					t.Errorf("%d compared, %d accepted, %d told the password is wrong, %d refused; want %d, 0, %d and %d", judged, accepted, wrong, refused, limit, limit, guessesAtOnce-limit)
				}
				held(t, f, "pat")
			},
		},
		{
			name:     "twelve entries of the right password at once: three are compared and accepted, the rest are refused, and nothing stays held against the name",
			failures: limit,
			run: func(t *testing.T, f *authFixture) {
				everyone(f)
				together(f)
				judged, accepted, wrong, refused := guessAtOnce(t, f, func(ctx context.Context, _ int) error {
					return check(ctx, f, "pat", "pw", attempts.Client{})
				})
				if judged != limit || accepted != limit || wrong != 0 || refused != guessesAtOnce-limit {
					t.Errorf("%d compared, %d accepted, %d told the password is wrong, %d refused; want %d, %d, 0 and %d", judged, accepted, wrong, refused, limit, limit, guessesAtOnce-limit)
				}
				if r := f.attemptsRecord("pat"); r != nil {
					t.Errorf("after the right password the name's record = %+v, want none", r)
				}
				// The person is not held by their own sign-ins: the next one goes through,
				// and so do as many more as the limit, one after another.
				for i := range limit + 1 {
					if err := check(context.Background(), f, "pat", "pw", attempts.Client{}); err != nil {
						t.Fatalf("sign-in %d after the right passwords entered at once error = %v, want it accepted", i+1, err)
					}
				}
			},
		},
		{
			name:     "twelve wrong passwords at once from one address and cookie, each for another name: ten are compared, the client's limit, the rest are refused unjudged, and the client is held",
			failures: 1,
			run: func(t *testing.T, f *authFixture) {
				everyone(f)
				client := attempts.Client{Address: "10.0.0.1", Cookie: "tok"}
				judged, accepted, wrong, refused := guessAtOnce(t, f, func(ctx context.Context, n int) error {
					return check(ctx, f, fmt.Sprintf("name-%d", n), "wrong", client)
				})
				if judged != attempts.ClientMultiple || accepted != 0 || wrong != attempts.ClientMultiple || refused != guessesAtOnce-attempts.ClientMultiple {
					t.Errorf("%d compared, %d accepted, %d told the password is wrong, %d refused; want %d, 0, %d and %d",
						judged, accepted, wrong, refused, attempts.ClientMultiple, attempts.ClientMultiple, guessesAtOnce-attempts.ClientMultiple)
				}
				// The client is held for any name, by its address and by its cookie.
				for _, c := range []attempts.Client{{Address: "10.0.0.1"}, {Cookie: "tok"}} {
					if err := check(context.Background(), f, "another", "pw", c); sessioninfo.LoginRefusalCodeOf(err) != sessioninfo.RefusedTooManyAttempts {
						t.Errorf("a sign-in from the held client %+v error = %v, want too_many_attempts", c, err)
					}
				}
			},
		},
		{
			name:     "twelve right passwords at once from one address and cookie, each for another name: ten are compared and accepted, and nothing stays held against the client",
			failures: 1,
			run: func(t *testing.T, f *authFixture) {
				everyone(f)
				client := attempts.Client{Address: "10.0.0.1", Cookie: "tok"}
				judged, accepted, wrong, refused := guessAtOnce(t, f, func(ctx context.Context, n int) error {
					return check(ctx, f, fmt.Sprintf("name-%d", n), "pw", client)
				})
				if judged != attempts.ClientMultiple || accepted != attempts.ClientMultiple || wrong != 0 || refused != guessesAtOnce-attempts.ClientMultiple {
					t.Errorf("%d compared, %d accepted, %d told the password is wrong, %d refused; want %d, %d, 0 and %d",
						judged, accepted, wrong, refused, attempts.ClientMultiple, attempts.ClientMultiple, guessesAtOnce-attempts.ClientMultiple)
				}
				// People who share an address are not held by their own sign-ins: more of
				// them than the client's limit sign in from it, one after another.
				for i := range 2 * attempts.ClientMultiple {
					if err := check(context.Background(), f, fmt.Sprintf("later-%d", i), "pw", client); err != nil {
						t.Fatalf("sign-in %d from the address after the right passwords entered at once error = %v, want it accepted", i+1, err)
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newAuthFixtureWith(t, gomock.NewController(t), []PasswordOption{AttemptLimit(tt.failures, 15*time.Minute, testLock)})

			tt.run(t, f)
		})
	}
}
