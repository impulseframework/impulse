package session

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// An address a person types at the mailed code's step, their account holding none, is
// shown to be theirs by the code mailed there and by nothing else. These tests hold the
// codes entered for such an address to one count, the address's own, across every
// account that typed it and every sign-in: twenty-five wrong codes, and then the address
// takes no more and is mailed none from the step. The right code within them is accepted
// and starts the count again. An account's own code, mailed to the address it holds, and
// an invitation's code take no place in it.

// addressSpentSentence is what a person is told of an address that takes no more codes.
const addressSpentSentence = "This email address can't be confirmed with a code any more. Ask an administrator to add it to your account."

// typedAddress is the address the accounts of these tests type at the step, and
// typedAddressKey its count's key as the store is handed it.
const (
	typedAddress    = "lee@lakeside.edu"
	typedAddressKey = "ADDRESS-GUESS:lee@lakeside.edu"
)

// addressTypists are two accounts that hold no address, pat and sam, each in a password
// sign-in held for the mailed code.
type addressTypists struct {
	f        *authFixture
	recorder *mail.Recorder
	codes    *factorCodes
	pat, sam *pendingState
	// claims are the addresses the sign-ins that reached the store handed it to confirm,
	// one for each sign-in; the store refuses every sign-in.
	claims []*sessionstorage.CodeAddress
}

// newAddressTypists makes the two accounts and their sign-ins.
func newAddressTypists(t *testing.T) *addressTypists {
	t.Helper()

	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	links.expectAccounts(f)
	w := &addressTypists{f: f, recorder: recorder, codes: expectFactorCodes(f)}
	wait := func(username string) *pendingState {
		user := &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: username}
		f.store.EXPECT().User(gomock.Any(), user.ID).Return(user, nil).AnyTimes()

		return &pendingState{
			ID: impulse.Must(impulse.NewUUID()), TokenHash: []byte(username + "'s pending token"), Step: sessioninfo.PendingMFA,
			Username: username, UserID: impulse.NullUUIDFromUUID(user.ID), Identity: sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()},
		}
	}
	w.pat, w.sam = wait("pat"), wait("sam")
	f.store.EXPECT().ConsumePendingSignIn(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	f.store.EXPECT().SignIn(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ *sessioninfo.NewSessionRequest, rules sessionstorage.SignInRules[mockTx]) (impulse.UUID, error) {
			w.claims = append(w.claims, rules.CodeAddress)

			return impulse.NilUUID, errors.New("the store of this test starts no session")
		}).AnyTimes()

	return w
}

// mailCode has the step mail a code to the address typed, in the sign-in wait, and
// returns the code.
func (w *addressTypists) mailCode(t *testing.T, wait *pendingState, typed string) string {
	t.Helper()

	if _, err := w.f.auth.enrollEmail(t.Context(), wait, typed); err != nil {
		t.Fatalf("enrollEmail(%q) error = %v, want the code mailed", typed, err)
	}
	msgs := w.recorder.Messages()
	last := msgs[len(msgs)-1]
	m := signInCodeSubject.FindStringSubmatch(last.Subject)
	if last.To != typed || m == nil {
		t.Fatalf("the last message = %q to %q, want the sign-in code mailed to %q", last.Subject, last.To, typed)
	}

	return m[1] + m[2]
}

// enter enters code in the sign-in wait and returns the refusal it was answered with. A
// code judged right reaches the store's sign-in, which refuses it as a failure of the
// server.
func (w *addressTypists) enter(t *testing.T, wait *pendingState, code string) sessioninfo.LoginRefusalCode {
	t.Helper()

	_, err := w.f.auth.completeWithEmailCode(t.Context(), httptest.NewRecorder(), wait, code, false)
	if err == nil {
		t.Fatal("completeWithEmailCode() started a session, want the store of this test to start none")
	}

	return sessioninfo.LoginRefusalCodeOf(err)
}

// guesses is the address's count of wrong codes, 0 for none.
func (w *addressTypists) guesses() int64 {
	record := w.f.attemptsRecord(typedAddressKey)
	if record == nil {
		return 0
	}

	return record.Failures
}

// assertAddressSpent fails unless err is the refusal of an address that takes no more
// codes: 401, address_spent, in its words.
func assertAddressSpent(t *testing.T, err error, what string) {
	t.Helper()

	if sessioninfo.LoginRefusalCodeOf(err) != sessioninfo.RefusedAddressSpent || !httpio.HasUnauthorized(err) || httpio.Message(err) != addressSpentSentence {
		t.Errorf("%s = %v, want 401 address_spent saying %q", what, err, addressSpentSentence)
	}
}

// latestCode is a copy of the code last recorded.
func (w *addressTypists) latestCode() sessionstorage.FactorCode {
	w.codes.mu.Lock()
	defer w.codes.mu.Unlock()

	return *w.codes.rows[len(w.codes.rows)-1]
}

func TestAuth_addressGuesses(t *testing.T) {
	t.Parallel()

	t.Run("the twenty-fifth wrong code entered for a typed address, across the two accounts that typed it, is answered as the address spent, and every code entered or asked for after it is too, compared with nothing and mailed nothing", addressSpentByWrongCodes)
	t.Run("a right code within the twenty-five is accepted and starts the address's count again", addressRightCodeWithinBudget)
	t.Run("an account's own code and an invitation's code take no place in an address's count", addressGuessesNotCounted)
}

func addressSpentByWrongCodes(t *testing.T) {
	t.Parallel()
	w := newAddressTypists(t)

	// Five codes, the most the address is mailed within fifteen minutes, typed in turn
	// by pat and by sam, sam in capitals, and five wrong guesses at each: four are
	// wrong, the fifth ends its code. The last of them is the address's twenty-fifth.
	for sent := 1; sent <= 5; sent++ {
		wait, typed := w.pat, typedAddress
		if sent%2 == 0 {
			wait, typed = w.sam, "Lee@Lakeside.EDU"
		}
		wrong := notCode(w.mailCode(t, wait, typed))
		for guess := 1; guess <= 5; guess++ {
			want := sessioninfo.RefusedInvalidCode
			switch {
			case sent == 5 && guess == 5:
				want = sessioninfo.RefusedAddressSpent
			case guess == 5:
				want = sessioninfo.RefusedCodeExpired
			}
			if got := w.enter(t, wait, wrong); got != want {
				t.Fatalf("wrong guess #%d at code #%d = %q, want %q", guess, sent, got, want)
			}
		}
	}
	record := w.f.attemptsRecord(typedAddressKey)
	if horizon := w.f.now().Add(99 * 365 * 24 * time.Hour); record == nil || record.Failures != 25 || record.LockedUntil == nil ||
		!record.LockedUntil.After(horizon) || !record.WindowEndsAt.After(horizon) {
		t.Fatalf("the address's count under %q = %+v, want twenty-five and the address closed to codes for longer than any sign-in lives", typedAddressKey, record)
	}

	// Half an hour later, the address's mail could be sent again. A code that stands
	// for it, in either account's sign-in, is not compared, the right one included:
	// it takes none of the code's own attempts and is not consumed.
	w.f.advance(30 * time.Minute)
	for _, wait := range []*pendingState{w.pat, w.sam} {
		if err := w.codes.insert(t.Context(), &sessionstorage.FactorCode{
			PendingID: wait.ID, Email: typedAddress, CodeHash: hashEmailCode(wait.ID, standingCode), ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		_, err := w.f.auth.completeWithEmailCode(t.Context(), httptest.NewRecorder(), wait, standingCode, false)
		assertAddressSpent(t, err, "the right code of a spent address in "+wait.Username+"'s sign-in")
		if row := w.latestCode(); row.Attempts != 0 || row.ConsumedAt != nil {
			t.Errorf("the code of a spent address after it was entered = %d attempt(s), consumed %v; want it untouched", row.Attempts, row.ConsumedAt != nil)
		}
	}
	if len(w.claims) != 0 {
		t.Errorf("%d sign-in(s) reached the store, want none", len(w.claims))
	}

	// A code asked for the address is refused by either account, and nothing is mailed,
	// made or counted: not the code, and not the address's mail.
	_, writes, _ := w.f.counts()
	mailed, made := len(w.recorder.Messages()), len(w.codes.sentTo())
	for _, wait := range []*pendingState{w.pat, w.sam} {
		_, err := w.f.auth.enrollEmail(t.Context(), wait, "LEE@lakeside.edu")
		assertAddressSpent(t, err, "a code asked for a spent address in "+wait.Username+"'s sign-in")
	}
	if got := len(w.recorder.Messages()); got != mailed {
		t.Errorf("%d message(s) mailed for a spent address, want none", got-mailed)
	}
	if got := len(w.codes.sentTo()); got != made {
		t.Errorf("%d code(s) made for a spent address, want none", got-made)
	}
	if _, got, _ := w.f.counts(); got != writes {
		t.Errorf("%d count(s) recorded when a code was asked for a spent address, want none: not under %q", got-writes, mailLimitKey(typedAddress))
	}

	// Another address the same account types is mailed its code.
	w.mailCode(t, w.pat, "pat@lakeside.edu")
}

func addressRightCodeWithinBudget(t *testing.T) {
	t.Parallel()
	w := newAddressTypists(t)

	// Four codes ended by five wrong guesses each, and four wrong guesses at the fifth:
	// twenty-four wrong codes, the last of them in sam's sign-in.
	var code string
	for sent := 1; sent <= 5; sent++ {
		wait := w.pat
		if sent%2 == 0 || sent == 5 {
			wait = w.sam
		}
		code = w.mailCode(t, wait, typedAddress)
		for guess := 1; guess <= 5 && (sent < 5 || guess < 5); guess++ {
			want := sessioninfo.RefusedInvalidCode
			if guess == 5 {
				want = sessioninfo.RefusedCodeExpired
			}
			if got := w.enter(t, wait, notCode(code)); got != want {
				t.Fatalf("wrong guess #%d at code #%d = %q, want %q", guess, sent, got, want)
			}
		}
	}
	if got := w.guesses(); got != 24 {
		t.Fatalf("the address's count = %d, want 24", got)
	}

	// The twenty-fifth, the right one, is accepted: the sign-in reaches the store with
	// the address to confirm, and the count starts again.
	if got := w.enter(t, w.sam, code); got != sessioninfo.RefusedInternalError {
		t.Fatalf("the right code after twenty-four wrong ones = %q, want it accepted and the sign-in handed to the store", got)
	}
	if len(w.claims) != 1 || w.claims[0] == nil || w.claims[0].Email != typedAddress {
		t.Errorf("the sign-ins that reached the store handed it %+v, want one confirming %s", w.claims, typedAddress)
	}
	if record := w.f.attemptsRecord(typedAddressKey); record != nil {
		t.Errorf("the address's count after its right code = %+v, want none", record)
	}

	// pat's wrong code afterwards is the first of a new count.
	if got := w.enter(t, w.pat, notCode(w.mailCode(t, w.pat, typedAddress))); got != sessioninfo.RefusedInvalidCode {
		t.Errorf("a wrong code after the count started again = %q, want %q", got, sessioninfo.RefusedInvalidCode)
	}
	if got := w.guesses(); got != 1 {
		t.Errorf("the address's count = %d, want 1", got)
	}
}

func addressGuessesNotCounted(t *testing.T) {
	t.Parallel()

	t.Run("the code mailed to the address an account holds, which nobody has confirmed", func(t *testing.T) {
		t.Parallel()
		f, _, _ := newMailFixture(t, gomock.NewController(t))
		userID := impulse.Must(impulse.NewUUID())
		f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "lee", Email: new(typedAddress)}, nil).AnyTimes()
		pending := &pendingState{
			ID: impulse.Must(impulse.NewUUID()), Step: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(userID),
			Identity: sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()},
		}
		code := expectFactorCode(f, pending)
		code.row.Email = typedAddress

		_, err := f.auth.completeWithEmailCode(t.Context(), httptest.NewRecorder(), pending, notCode(standingCode), false)
		if got := sessioninfo.LoginRefusalCodeOf(err); got != sessioninfo.RefusedInvalidCode {
			t.Fatalf("a wrong code of the account's own = %v, want %q", err, sessioninfo.RefusedInvalidCode)
		}
		if record := f.attemptsRecord(typedAddressKey); record != nil {
			t.Errorf("the address's count = %+v, want none: the account holds the address", record)
		}
	})

	t.Run("the code mailed for an invitation to an account that holds no address", func(t *testing.T) {
		t.Parallel()
		w := newNamedInvitedWait(t)

		_, err := w.f.auth.completeWithEmailCode(w.carriesInvited, httptest.NewRecorder(), w.pending, notCode(w.code), false)
		if got := sessioninfo.LoginRefusalCodeOf(err); got != sessioninfo.RefusedInvalidCode {
			t.Fatalf("a wrong code of the invitation's = %v, want %q", err, sessioninfo.RefusedInvalidCode)
		}
		if w.entered(w.invited) != 1 {
			t.Errorf("the invitation's count = %d, want the code counted there", w.entered(w.invited))
		}
		if record := w.f.attemptsRecord(addressGuessKey(w.invited.Email)); record != nil {
			t.Errorf("the invited address's count = %+v, want none: the code is the invitation's", record)
		}
	})
}
