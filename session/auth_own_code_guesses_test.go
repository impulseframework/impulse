package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/attempts"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// The code mailed to the address an account holds, the account's own, shows the
// mailbox's reader, and a person who reached the code step without reading that mailbox
// could have a new code mailed every few minutes and guess on. These tests hold the
// codes entered for such an account to one count, the account's own, across every
// sign-in that waits on its own code: twenty-five wrong codes, and then the account
// takes no more and is mailed none from the step. The right code within them is
// accepted and starts the count again, and so does a password chosen at a reset link
// mailed to the address. A typed address's code, an invitation's code and a code
// entered while none stands take no place in it, and neither do the codes of another
// account whose address reads alike.

// codesSpentSentence is what a person is told of an account whose address takes no
// more codes.
const codesSpentSentence = "Too many wrong codes were entered for this account. Reset your password, or ask an administrator to unlock your sign-in."

// ownAddress is the address the account of these tests holds, as an administrator
// spelled it.
const ownAddress = "Pat@Lakeside.edu"

// ownCodeRow is the key of the count of the wrong codes entered for the account with
// id, written out as the sign-in attempts table keeps it.
func ownCodeRow(id impulse.UUID) string {
	return "CODE-GUESS:" + id.String()
}

// ownCodeWaits is pat, an account that holds an address nobody has confirmed, in two
// sign-ins held for the code mailed there: a password sign-in, and a sign-in through
// an identity that is not linked to the account, which waits for the account's own code
// with nothing linked (signinflow's nameAloneHold).
type ownCodeWaits struct {
	f        *authFixture
	recorder *mail.Recorder
	codes    *factorCodes
	// id is pat's.
	id                 impulse.UUID
	password, unlinked *pendingState
	// claims are the addresses the sign-ins that reached the store handed it to confirm,
	// one for each sign-in; the store refuses every sign-in.
	claims []*sessionstorage.CodeAddress
}

// newOwnCodeWaits makes the account and its two sign-ins.
func newOwnCodeWaits(t *testing.T) *ownCodeWaits {
	t.Helper()

	w := newOwnCodeFixture(t)
	w.id, w.password = w.account("pat", ownAddress)
	w.unlinked = w.wait(w.id, "pat", sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "google", Subject: "g-1", SignInName: "pat", Email: "pat@corp.example"})

	return w
}

// newOwnCodeFixture makes the fixture the accounts of these tests sign in on, with no
// account yet: its store starts no session.
func newOwnCodeFixture(t *testing.T) *ownCodeWaits {
	t.Helper()

	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	links.expectAccounts(f)
	w := &ownCodeWaits{f: f, recorder: recorder, codes: expectFactorCodes(f)}
	f.store.EXPECT().ConsumePendingSignIn(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	f.store.EXPECT().SignIn(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ *sessioninfo.NewSessionRequest, rules sessionstorage.SignInRules[mockTx]) (impulse.UUID, error) {
			w.claims = append(w.claims, rules.CodeAddress)

			return impulse.NilUUID, errors.New("the store of this test starts no session")
		}).AnyTimes()

	return w
}

// account makes an account named username that holds the address email, nobody having
// confirmed it, and returns its id and a password sign-in of it held for its own code.
func (w *ownCodeWaits) account(username, email string) (impulse.UUID, *pendingState) {
	user := &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: username, Email: new(email)}
	w.f.store.EXPECT().User(gomock.Any(), user.ID).Return(user, nil).AnyTimes()

	return user.ID, w.wait(user.ID, username, sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: user.ID.String()})
}

// wait is a sign-in of the account with id through identity, held for the account's
// own code.
func (w *ownCodeWaits) wait(id impulse.UUID, username string, identity sessioninfo.Identity) *pendingState {
	return &pendingState{
		ID: impulse.Must(impulse.NewUUID()), TokenHash: []byte(identity.Subject + "'s pending token"), Step: sessioninfo.PendingMFA,
		Username: username, UserID: impulse.NullUUIDFromUUID(id), Identity: identity,
	}
}

// mailCode has the step mail the account's own code in the sign-in wait, to pat's
// address, and returns the code.
func (w *ownCodeWaits) mailCode(t *testing.T, wait *pendingState) string {
	t.Helper()

	return w.mailCodeTo(t, wait, ownAddress)
}

// mailCodeTo has the step mail the account's own code in the sign-in wait, which must
// mail it to email, and returns the code.
func (w *ownCodeWaits) mailCodeTo(t *testing.T, wait *pendingState, email string) string {
	t.Helper()

	if _, err := w.f.auth.sendCodeToUser(t.Context(), wait); err != nil {
		t.Fatalf("sendCodeToUser() error = %v, want the code mailed", err)
	}
	msgs := w.recorder.Messages()
	last := msgs[len(msgs)-1]
	m := signInCodeSubject.FindStringSubmatch(last.Subject)
	if last.To != email || m == nil {
		t.Fatalf("the last message = %q to %q, want the sign-in code mailed to %q", last.Subject, last.To, email)
	}

	return m[1] + m[2]
}

// enter enters code in the sign-in wait and returns the refusal it was answered with. A
// code judged right reaches the store's sign-in, which refuses it as a failure of the
// server.
func (w *ownCodeWaits) enter(t *testing.T, wait *pendingState, code string) sessioninfo.LoginRefusalCode {
	t.Helper()

	_, err := w.f.auth.completeWithEmailCode(t.Context(), httptest.NewRecorder(), wait, code, false)
	if err == nil {
		t.Fatal("completeWithEmailCode() started a session, want the store of this test to start none")
	}

	return sessioninfo.LoginRefusalCodeOf(err)
}

// guesses is pat's count of wrong codes, 0 for none.
func (w *ownCodeWaits) guesses() int64 {
	return ownCodeGuesses(w.f, w.id)
}

// ownCodeGuesses is the fixture's count of the wrong codes entered for the account with
// id, 0 for none.
func ownCodeGuesses(f *authFixture, id impulse.UUID) int64 {
	record := f.attemptsRecord(ownCodeRow(id))
	if record == nil {
		return 0
	}

	return record.Failures
}

// assertCodesSpent fails unless err is the refusal of an account that takes no more
// codes: 401, codes_spent, in its words.
func assertCodesSpent(t *testing.T, err error, what string) {
	t.Helper()

	if sessioninfo.LoginRefusalCodeOf(err) != sessioninfo.RefusedCodesSpent || !httpio.HasUnauthorized(err) || httpio.Message(err) != codesSpentSentence {
		t.Errorf("%s = %v, want 401 codes_spent saying %q", what, err, codesSpentSentence)
	}
}

// latestCode is a copy of the code last recorded.
func (w *ownCodeWaits) latestCode() sessionstorage.FactorCode {
	w.codes.mu.Lock()
	defer w.codes.mu.Unlock()

	return *w.codes.rows[len(w.codes.rows)-1]
}

func TestAuth_ownCodeGuesses(t *testing.T) {
	t.Parallel()

	t.Run("the twenty-fifth wrong code entered for the address an account holds, across its sign-ins, is answered as the codes spent, and every code entered or asked for after it is refused, nothing compared and nothing mailed", ownCodesSpentByWrongCodes)
	t.Run("a right code within the twenty-five is accepted and starts the count again", ownCodeRightWithinBudget)
	t.Run("a typed address's code, an invitation's code and a code entered while none stands take no place in the count", ownCodeGuessesNotCounted)
	t.Run("a password chosen at a reset link starts the count again", ownCodeGuessesResetByLink)
	t.Run("two accounts whose addresses read alike have a count each: one's wrong codes take no place in the other's, one's right code leaves the other's count as it was, and one's spent count leaves the other mailed its code", ownCodeCountIsTheAccounts)
}

func ownCodesSpentByWrongCodes(t *testing.T) {
	t.Parallel()
	w := newOwnCodeWaits(t)

	// Five codes, the most the address is sent within fifteen minutes, in turn in the
	// password sign-in and in the one that is not linked, and five wrong guesses at each:
	// four are wrong, the fifth ends its code. The last of them is the account's
	// twenty-fifth.
	for sent := 1; sent <= 5; sent++ {
		wait := w.password
		if sent%2 == 0 {
			wait = w.unlinked
		}
		wrong := notCode(w.mailCode(t, wait))
		for guess := 1; guess <= 5; guess++ {
			want := sessioninfo.RefusedInvalidCode
			switch {
			case sent == 5 && guess == 5:
				want = sessioninfo.RefusedCodesSpent
			case guess == 5:
				want = sessioninfo.RefusedCodeExpired
			}
			if got := w.enter(t, wait, wrong); got != want {
				t.Fatalf("wrong guess #%d at code #%d = %q, want %q", guess, sent, got, want)
			}
		}
	}
	record := w.f.attemptsRecord(ownCodeRow(w.id))
	if horizon := w.f.now().Add(99 * 365 * 24 * time.Hour); record == nil || record.Failures != 25 || record.LockedUntil == nil ||
		!record.LockedUntil.After(horizon) || !record.WindowEndsAt.After(horizon) {
		t.Fatalf("the account's count under %q = %+v, want twenty-five and the account closed to codes for longer than any sign-in lives", ownCodeRow(w.id), record)
	}

	// Half an hour later, the address could be sent codes again. A code that stands for
	// it, in either sign-in, is not compared, the right one included: it takes none of
	// the code's own attempts and is not consumed.
	w.f.advance(30 * time.Minute)
	for _, wait := range []*pendingState{w.password, w.unlinked} {
		if err := w.codes.insert(t.Context(), &sessionstorage.FactorCode{
			PendingID: wait.ID, Email: ownAddress, CodeHash: hashEmailCode(wait.ID, standingCode), ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		_, err := w.f.auth.completeWithEmailCode(t.Context(), httptest.NewRecorder(), wait, standingCode, false)
		assertCodesSpent(t, err, "the right code of a spent account in the "+string(wait.Identity.Method)+" sign-in")
		if row := w.latestCode(); row.Attempts != 0 || row.ConsumedAt != nil {
			t.Errorf("the code of a spent account after it was entered = %d attempt(s), consumed %v; want it untouched", row.Attempts, row.ConsumedAt != nil)
		}
	}
	if len(w.claims) != 0 {
		t.Errorf("%d sign-in(s) reached the store, want none", len(w.claims))
	}

	// A code asked for in either sign-in is refused, and nothing is mailed, made or
	// counted: not the code, and not the address's count of sign-in codes.
	_, writes, _ := w.f.counts()
	mailed, made := len(w.recorder.Messages()), len(w.codes.sentTo())
	for _, wait := range []*pendingState{w.password, w.unlinked} {
		_, err := w.f.auth.sendCodeToUser(t.Context(), wait)
		assertCodesSpent(t, err, "a code asked for a spent account in the "+string(wait.Identity.Method)+" sign-in")
	}
	if got := len(w.recorder.Messages()); got != mailed {
		t.Errorf("%d message(s) mailed for a spent account, want none", got-mailed)
	}
	if got := len(w.codes.sentTo()); got != made {
		t.Errorf("%d code(s) made for a spent account, want none", got-made)
	}
	if _, got, _ := w.f.counts(); got != writes {
		t.Errorf("%d count(s) recorded when a code was asked for a spent account, want none: not under %q", got-writes, codeLimitKey(ownAddress))
	}
}

func ownCodeRightWithinBudget(t *testing.T) {
	t.Parallel()
	w := newOwnCodeWaits(t)

	// Four codes ended by five wrong guesses each, and four wrong guesses at the fifth:
	// twenty-four wrong codes, the last of them in the sign-in that is not linked.
	var code string
	for sent := 1; sent <= 5; sent++ {
		wait := w.password
		if sent%2 == 0 || sent == 5 {
			wait = w.unlinked
		}
		code = w.mailCode(t, wait)
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
		t.Fatalf("the account's count = %d, want 24", got)
	}

	// The twenty-fifth, the right one, is accepted: the sign-in reaches the store with
	// the address to confirm, and the count starts again.
	if got := w.enter(t, w.unlinked, code); got != sessioninfo.RefusedInternalError {
		t.Fatalf("the right code after twenty-four wrong ones = %q, want it accepted and the sign-in handed to the store", got)
	}
	if len(w.claims) != 1 || w.claims[0] == nil || w.claims[0].Email != ownAddress {
		t.Errorf("the sign-ins that reached the store handed it %+v, want one confirming %s", w.claims, ownAddress)
	}
	if record := w.f.attemptsRecord(ownCodeRow(w.id)); record != nil {
		t.Errorf("the account's count after its right code = %+v, want none", record)
	}

	// A wrong code in the password sign-in afterwards is the first of a new count.
	if got := w.enter(t, w.password, notCode(w.mailCode(t, w.password))); got != sessioninfo.RefusedInvalidCode {
		t.Errorf("a wrong code after the count started again = %q, want %q", got, sessioninfo.RefusedInvalidCode)
	}
	if got := w.guesses(); got != 1 {
		t.Errorf("the account's count = %d, want 1", got)
	}
}

func ownCodeGuessesNotCounted(t *testing.T) {
	t.Parallel()

	t.Run("the code mailed to an address typed by an account that holds none", func(t *testing.T) {
		t.Parallel()
		w := newAddressTypists(t)

		if got := w.enter(t, w.pat, notCode(w.mailCode(t, w.pat, ownAddress))); got != sessioninfo.RefusedInvalidCode {
			t.Fatalf("a wrong code for a typed address = %q, want %q", got, sessioninfo.RefusedInvalidCode)
		}
		if record := w.f.attemptsRecord(ownCodeRow(w.pat.UserID.UUID)); record != nil {
			t.Errorf("the count of the account's own codes = %+v, want none: the address was typed", record)
		}
	})

	t.Run("the code mailed for an invitation, entered once the account holds an address", func(t *testing.T) {
		t.Parallel()
		w := newNamedInvitedWait(t)
		w.user.Email = new(ownAddress)

		_, err := w.f.auth.completeWithEmailCode(w.carriesInvited, httptest.NewRecorder(), w.pending, notCode(w.code), false)
		if got := sessioninfo.LoginRefusalCodeOf(err); got != sessioninfo.RefusedInvalidCode {
			t.Fatalf("a wrong code of the invitation's = %v, want %q", err, sessioninfo.RefusedInvalidCode)
		}
		if w.entered(w.invited) != 1 {
			t.Errorf("the invitation's count = %d, want the code counted there", w.entered(w.invited))
		}
		if record := w.f.attemptsRecord(ownCodeRow(w.user.ID)); record != nil {
			t.Errorf("the count of the account's own codes = %+v, want none: the code is the invitation's", record)
		}
	})

	t.Run("a code entered while none stands", func(t *testing.T) {
		t.Parallel()
		w := newOwnCodeWaits(t)

		if got := w.enter(t, w.password, standingCode); got != sessioninfo.RefusedCodeExpired {
			t.Fatalf("a code entered while none stands = %q, want %q", got, sessioninfo.RefusedCodeExpired)
		}
		if record := w.f.attemptsRecord(ownCodeRow(w.id)); record != nil {
			t.Errorf("the count of the account's own codes = %+v, want none: no code stood", record)
		}
	})
}

func ownCodeGuessesResetByLink(t *testing.T) {
	t.Parallel()
	userID, _ := newIDs()
	f, recorder, _ := newMailFixture(t, gomock.NewController(t))
	expectFactorCodes(f)
	user := passwordPerson(t, userID)
	f.store.EXPECT().User(gomock.Any(), userID).Return(user, nil).AnyTimes()
	wait := &pendingState{
		ID: impulse.Must(impulse.NewUUID()), Step: sessioninfo.PendingMFA, Username: user.Username, UserID: impulse.NullUUIDFromUUID(userID),
		Identity: sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()},
	}
	key := ownCodeRow(userID)
	// Another account's count, spent too, which a proof of this account's mailbox does
	// not start again.
	another := ownCodeRow(impulse.Must(impulse.NewUUID()))
	for range 25 {
		f.countFailure(key, codeGuessLimit)
		f.countFailure(another, codeGuessLimit)
	}
	if _, err := f.auth.sendCodeToUser(t.Context(), wait); sessioninfo.LoginRefusalCodeOf(err) != sessioninfo.RefusedCodesSpent {
		t.Fatalf("a code asked for once the count is spent = %v, want %q", err, sessioninfo.RefusedCodesSpent)
	}

	token := mailedReset(t, f, recorder, userID)
	f.store.EXPECT().ChangeUserPassword(gomock.Any(), userID, user.PasswordHash, gomock.Not(gomock.Nil())).Return(nil)
	rr := f.serve(f.auth.PasswordReset().Complete(), http.MethodPost, "/reset/complete", map[string]string{"token": token, "password": chosenPassword}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("Complete() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	if record := f.attemptsRecord(key); record != nil {
		t.Errorf("the count of the account's own codes after a password was chosen at a reset link = %+v, want none", record)
	}
	if record := f.attemptsRecord(another); record == nil || record.Failures != 25 {
		t.Errorf("another account's count after this one's reset = %+v, want it as it was, at twenty-five", record)
	}
	mailed := len(recorder.Messages())
	if _, err := f.auth.sendCodeToUser(t.Context(), wait); err != nil {
		t.Fatalf("a code asked for after the reset = %v, want it mailed", err)
	}
	if got := recorder.Messages(); len(got) != mailed+1 || got[mailed].To != *user.Email {
		t.Errorf("messages after the code was asked for = %d, want one more, to %s", len(got)-mailed, *user.Email)
	}
}

func ownCodeCountIsTheAccounts(t *testing.T) {
	t.Parallel()

	// Two addresses that the mail counts' fold takes for one and the users table for
	// two: two mailboxes, two accounts.
	const patAddress, samAddress = "pat@glass.example", "pat@glaß.example"
	if attempts.Normalize(patAddress) != attempts.Normalize(samAddress) {
		t.Fatalf("attempts.Normalize() folds %q and %q apart, want them folded to one", patAddress, samAddress)
	}
	if sameAddress(patAddress, samAddress) {
		t.Fatalf("sameAddress(%q, %q) = true, want two addresses", patAddress, samAddress)
	}
	w := newOwnCodeFixture(t)
	patID, pat := w.account("pat", patAddress)
	samID, sam := w.account("sam", samAddress)

	// pat enters two wrong codes and sam one: a count each.
	wrong := notCode(w.mailCodeTo(t, pat, patAddress))
	for guess := 1; guess <= 2; guess++ {
		if got := w.enter(t, pat, wrong); got != sessioninfo.RefusedInvalidCode {
			t.Fatalf("pat's wrong guess #%d = %q, want %q", guess, got, sessioninfo.RefusedInvalidCode)
		}
	}
	samCode := w.mailCodeTo(t, sam, samAddress)
	if got := w.enter(t, sam, notCode(samCode)); got != sessioninfo.RefusedInvalidCode {
		t.Fatalf("sam's wrong guess = %q, want %q", got, sessioninfo.RefusedInvalidCode)
	}
	if patCount, samCount := ownCodeGuesses(w.f, patID), ownCodeGuesses(w.f, samID); patCount != 2 || samCount != 1 {
		t.Fatalf("the counts = pat %d, sam %d; want pat 2, sam 1", patCount, samCount)
	}

	// sam's right code starts sam's count again and leaves pat's as it was.
	if got := w.enter(t, sam, samCode); got != sessioninfo.RefusedInternalError {
		t.Fatalf("sam's right code = %q, want it accepted and the sign-in handed to the store", got)
	}
	if record := w.f.attemptsRecord(ownCodeRow(samID)); record != nil {
		t.Errorf("sam's count after sam's right code = %+v, want none", record)
	}
	if got := ownCodeGuesses(w.f, patID); got != 2 {
		t.Errorf("pat's count after sam's right code = %d, want 2", got)
	}

	// pat's count is spent: sam is still mailed a code, and pat is mailed none.
	for range 23 {
		w.f.countFailure(ownCodeRow(patID), codeGuessLimit)
	}
	w.mailCodeTo(t, sam, samAddress)
	_, err := w.f.auth.sendCodeToUser(t.Context(), pat)
	assertCodesSpent(t, err, "a code asked for pat once pat's count is spent")
}
