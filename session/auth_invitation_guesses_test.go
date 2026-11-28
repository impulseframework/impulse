package session

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// The code mailed to an invited address is the only thing between the holder of an
// invitation's link and what the invitation gives. These tests hold the codes entered
// for an invitation to one count that never starts again: twenty-five, in however many
// sign-ins and over however long, and then the invitation takes no more codes and
// mails none.

// invitationSpentSentence is what a person is told of an invitation that takes no more
// codes.
const invitationSpentSentence = "This invitation can't be used any more. Ask the person who invited you to send a new one."

// invitationCode has the code of the wait the cookies carry mailed, which must be the
// invitation's, and returns it.
func invitationCode(t *testing.T, f *authFixture, recorder *mail.Recorder, cookies []*http.Cookie) string {
	t.Helper()

	mailed := len(recorder.Messages())
	if rr := f.serve(f.auth.Pending().SendCode(), http.MethodPost, "/pending/send-code", nil, cookies); rr.Code != http.StatusOK {
		t.Fatalf("SendCode() = %d %s, want the invitation's code sent", rr.Code, rr.Body.String())
	}
	msgs := recorder.Messages()
	if len(msgs) != mailed+1 {
		t.Fatalf("messages after SendCode() = %d, want %d: one code mailed", len(msgs), mailed+1)
	}

	return invitationCodeIn(t, msgs[len(msgs)-1])
}

// notCode is a code that is not code.
func notCode(code string) string {
	if code == "000000" {
		return "111111"
	}

	return "000000"
}

// assertGuess enters code in the wait the cookies carry, which must be refused with 401
// and want.
func assertGuess(t *testing.T, f *authFixture, cookies []*http.Cookie, code string, want sessioninfo.LoginRefusalCode, what string) {
	t.Helper()

	if rr := f.enterCode(cookies, code); rr.Code != http.StatusUnauthorized || refusalCode(t, rr) != want {
		t.Fatalf("%s = %d %s, want 401 %s", what, rr.Code, rr.Body.String(), want)
	}
}

// assertInvitationSpent fails unless the wait the cookies carry is refused its code,
// asked for and entered, in the words of an invitation that takes no more, with nothing
// mailed.
func assertInvitationSpent(t *testing.T, f *authFixture, recorder *mail.Recorder, cookies []*http.Cookie, when string) {
	t.Helper()

	mailed := len(recorder.Messages())
	rr := f.serve(f.auth.Pending().SendCode(), http.MethodPost, "/pending/send-code", nil, cookies)
	if rr.Code != http.StatusUnauthorized || refusalCode(t, rr) != sessioninfo.RefusedInvitationSpent || decoded(t, rr)["message"] != invitationSpentSentence {
		t.Errorf("SendCode() %s = %d %s, want 401 invitation_spent saying %q", when, rr.Code, rr.Body.String(), invitationSpentSentence)
	}
	if got := len(recorder.Messages()); got != mailed {
		t.Errorf("messages after SendCode() %s = %d, want %d: a spent invitation mails nothing", when, got, mailed)
	}
	rr = f.enterCode(cookies, "271828")
	if rr.Code != http.StatusUnauthorized || refusalCode(t, rr) != sessioninfo.RefusedInvitationSpent || decoded(t, rr)["message"] != invitationSpentSentence {
		t.Errorf("VerifyCode() %s = %d %s, want 401 invitation_spent saying %q", when, rr.Code, rr.Body.String(), invitationSpentSentence)
	}
}

func TestAuth_invitationGuesses(t *testing.T) {
	t.Parallel()

	t.Run("the twenty-fifth wrong code ends the invitation's codes: it is answered as spent, no more are mailed, and the right code is not taken", invitationSpentByWrongCodes)
	t.Run("the count holds across sign-ins and after fifteen minutes: five wrong codes in each of five sign-ins half an hour apart spend the invitation", invitationGuessesAcrossSignIns)
	t.Run("a right code before then still accepts: after twenty-four wrong codes the twenty-fifth, the right one, makes the account", invitationRightCodeWithinBudget)
}

func invitationSpentByWrongCodes(t *testing.T) {
	t.Parallel()
	f, recorder, links, opened := invitedPat(t)
	codes := expectFactorCodes(f)
	pendingID, cookies := signUpInvited(t, f, opened, map[string]string{"password": "correct horse battery staple", "username": "patl"})
	inv := invitationOf(links.rows[0])

	// Five codes, the most that are mailed within fifteen minutes, and five wrong
	// guesses at each: four are wrong, the fifth ends its code. The last of them is the
	// invitation's twenty-fifth.
	for sent := 1; sent <= 5; sent++ {
		wrong := notCode(invitationCode(t, f, recorder, cookies))
		for guess := 1; guess <= 5; guess++ {
			want := sessioninfo.RefusedInvalidCode
			switch {
			case sent == 5 && guess == 5:
				want = sessioninfo.RefusedInvitationSpent
			case guess == 5:
				want = sessioninfo.RefusedCodeExpired
			}
			assertGuess(t, f, cookies, wrong, want, "wrong code of the invitation")
		}
	}
	record := f.attemptsRecord(invitationGuessKey(inv))
	if record == nil || record.Failures != 25 || record.LockedUntil == nil || !record.LockedUntil.After(inv.ExpiresAt) {
		t.Fatalf("the invitation's count of codes entered = %+v, want twenty-five and the invitation closed to codes for longer than it stands (%v)", record, inv.ExpiresAt)
	}
	assertInvitationSpent(t, f, recorder, cookies, "after twenty-five wrong codes")

	// Half an hour later the count of the codes mailed has started again. The count of
	// the codes entered has not.
	f.advance(30 * time.Minute)
	assertInvitationSpent(t, f, recorder, cookies, "half an hour after twenty-five wrong codes")

	// A code that stands is not taken, the right one included. The mocked store expects
	// no ConsumePendingSignIn, no CreateUser and no SignIn: any of them fails the test.
	if err := codes.insert(context.Background(), &sessionstorage.FactorCode{
		PendingID: pendingID, Email: "pat@lakeside.edu", CodeHash: hashEmailCode(pendingID, standingCode), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	assertGuess(t, f, cookies, standingCode, sessioninfo.RefusedInvitationSpent, "the right code of a spent invitation")
	if row := links.rows[0]; row.ConsumedAt != nil || f.held[pendingID].ConsumedAt != nil {
		t.Error("the right code of a spent invitation ended the invitation or the wait, want both left as they were")
	}

	// Another sign-in that carries the invitation meets the same count.
	_, again := signUpInvited(t, f, opened, map[string]string{"password": "correct horse battery staple", "username": "patl"})
	assertInvitationSpent(t, f, recorder, again, "in another sign-in that carries the invitation")
}

func invitationGuessesAcrossSignIns(t *testing.T) {
	t.Parallel()
	f, recorder, links, opened := invitedPat(t)
	expectFactorCodes(f)
	inv := invitationOf(links.rows[0])

	// The holder of the link signs up, has one code mailed, ends it with five wrong
	// guesses, waits out the count of the codes mailed and starts again.
	var first *sessionstorage.SignInAttempts
	for signIn := 1; signIn <= 5; signIn++ {
		_, cookies := signUpInvited(t, f, opened, map[string]string{"password": "correct horse battery staple", "username": "patl"})
		wrong := notCode(invitationCode(t, f, recorder, cookies))
		for guess := 1; guess <= 5; guess++ {
			want := sessioninfo.RefusedInvalidCode
			switch {
			case signIn == 5 && guess == 5:
				want = sessioninfo.RefusedInvitationSpent
			case guess == 5:
				want = sessioninfo.RefusedCodeExpired
			}
			assertGuess(t, f, cookies, wrong, want, "wrong code of the invitation")
		}
		record := f.attemptsRecord(invitationGuessKey(inv))
		if record == nil || record.Failures != int64(5*signIn) {
			t.Fatalf("the invitation's count of codes entered after sign-in #%d = %+v, want %d: one count across the sign-ins", signIn, record, 5*signIn)
		}
		if first == nil {
			first = record
		}
		if !record.WindowEndsAt.Equal(first.WindowEndsAt) {
			t.Fatalf("the invitation's count after sign-in #%d ends at %v, want %v as at the first: the count never starts again", signIn, record.WindowEndsAt, first.WindowEndsAt)
		}
		if sent := f.attemptsRecord(invitationCodeLimitKey(inv)); sent == nil || sent.Failures != 1 {
			t.Fatalf("the invitation's count of codes mailed in sign-in #%d = %+v, want one: that count started again", signIn, sent)
		}
		f.advance(30 * time.Minute)
	}

	_, cookies := signUpInvited(t, f, opened, map[string]string{"password": "correct horse battery staple", "username": "patl"})
	assertInvitationSpent(t, f, recorder, cookies, "in a sixth sign-in")
}

func invitationRightCodeWithinBudget(t *testing.T) {
	t.Parallel()
	_, sessionID := newIDs()
	f, recorder, links, opened := invitedPat(t)
	expectFactorCodes(f)
	pendingID, cookies := signUpInvited(t, f, opened, map[string]string{"password": "correct horse battery staple", "username": "patl"})
	inv := invitationOf(links.rows[0])

	// Four codes ended by five wrong guesses each, and four wrong guesses at the fifth:
	// twenty-four wrong codes.
	var code string
	for sent := 1; sent <= 5; sent++ {
		code = invitationCode(t, f, recorder, cookies)
		for guess := 1; guess <= 5 && (sent < 5 || guess < 5); guess++ {
			want := sessioninfo.RefusedInvalidCode
			if guess == 5 {
				want = sessioninfo.RefusedCodeExpired
			}
			assertGuess(t, f, cookies, notCode(code), want, "wrong code of the invitation")
		}
	}
	if record := f.attemptsRecord(invitationGuessKey(inv)); record == nil || record.Failures != 24 || record.LockedUntil != nil {
		t.Fatalf("the invitation's count of codes entered = %+v, want twenty-four and the invitation open to one more", record)
	}

	f.expectConsume(pendingID)
	f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) {
		if req.Invitation == nil || req.Invitation.ID != inv.ID || !req.Invitation.AddressProven {
			t.Fatalf("SignIn() given the invitation %+v, want the opened one with its address proven by the code", req.Invitation)
		}
		req.Invitation.Accepted = true
	}, nil)

	if rr := f.enterCode(cookies, code); rr.Code != http.StatusOK || decoded(t, rr)["mfaIsRequired"] != false {
		t.Errorf("VerifyCode() of the right code, the twenty-fifth entered = %d %s, want the account made and signed in", rr.Code, rr.Body.String())
	}
	if f.signedIn == nil || f.signedIn.newAccount == nil {
		t.Error("SignIn() given no account to make, want the sign-up's")
	}
}
