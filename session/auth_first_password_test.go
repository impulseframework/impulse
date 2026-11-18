package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// chosenPassword is a password the first password's rule accepts.
const chosenPassword = "a harbor of my own"

// directoryPerson is a signed-in person who has only ever signed in through a
// directory: no password, and the address the directory asserted, confirmed.
func directoryPerson(userID impulse.UUID) *sessionstorage.SessionUser {
	confirmed := time.Now().Add(-time.Hour)

	return &sessionstorage.SessionUser{ID: userID, Username: "pat", Email: strPtr("pat@lakeside.edu"), EmailVerifiedAt: &confirmed}
}

// firstPasswordCodeIn is the code the first password's mail carries, as a person would
// type it from the subject line.
func firstPasswordCodeIn(t *testing.T, msg mail.Message) string {
	t.Helper()

	m := regexp.MustCompile(`^Your code to choose a Harbor password is (\d{3}) (\d{3})$`).FindStringSubmatch(msg.Subject)
	if m == nil {
		t.Fatalf("the message's subject = %q, want the first password's code", msg.Subject)
	}

	return m[1] + m[2]
}

// sendFirstPasswordCode asks for the code as the session and returns the one mailed.
func sendFirstPasswordCode(t *testing.T, f *authFixture, recorder *mail.Recorder, sessionID impulse.UUID) string {
	t.Helper()

	before := len(recorder.Messages())
	rr := f.asUser(f.auth.FirstPassword().SendCode(), sessionID, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("SendCode() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	msgs := recorder.Messages()
	if len(msgs) != before+1 {
		t.Fatalf("SendCode() sent %d messages, want one", len(msgs)-before)
	}

	return firstPasswordCodeIn(t, msgs[len(msgs)-1])
}

// expectFirstPassword makes the store take the first password of user as the real one
// does: written while the user holds none, a Conflict once they hold one. It returns
// how many were written.
func expectFirstPassword(f *authFixture, user *sessionstorage.SessionUser) *int {
	written := new(int)
	f.store.EXPECT().SetFirstUserPassword(gomock.Any(), user.ID, gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, hash *securehash.Hash) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if user.PasswordHash != nil {
			return httpio.NewConflictMessageWithError(sessionstorage.ErrPasswordHeld, "the user already has a password")
		}
		user.PasswordHash = hash
		*written++

		return nil
	}).AnyTimes()

	return written
}

func TestAuth_FirstPassword(t *testing.T) {
	t.Parallel()

	t.Run("a person with no password is mailed a code at their confirmed address and chooses a password with it: no session ends, and the session records a password event", firstPasswordCase1)
	t.Run("no first password without the code: none sent, a wrong one, another account's, and one that expired", firstPasswordCase2)
	t.Run("five wrong codes end the code and hold the account's first password, whatever is asked meanwhile", firstPasswordCase3)
	t.Run("an account that holds a password is refused the code and the password", firstPasswordCase4)
	t.Run("an account with no confirmed address is refused the code, and told to confirm one", firstPasswordCase5)
	t.Run("an Auth with no password method refuses both", firstPasswordCase6)
	t.Run("the password is held to the rule a sign-up's is, and a refused one leaves the code standing", firstPasswordCase7)
	t.Run("a new code replaces the one that stood, and a code writes one password", firstPasswordCase8)
	t.Run("a mailbox is sent five codes it does not use, then refused", firstPasswordCase9)
	t.Run("an Auth that records no auth events writes the password and records none; an Auth without mail has no first password handlers", firstPasswordCase10)
	t.Run("a code counts for the mailbox it was mailed to: once the account's confirmed address is another, or none, it writes no password", firstPasswordCase11)
}

func firstPasswordCase1(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	user := directoryPerson(userID)
	f.expectSession(sessionID, user)
	written := expectFirstPassword(f, user)
	var events []sessioninfo.AuthEvent
	f.store.EXPECT().AppendAuthEvent(gomock.Any(), sessionID, gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, event *sessioninfo.AuthEvent) error {
		events = append(events, *event)

		return nil
	})

	rr := f.asUser(f.auth.FirstPassword().SendCode(), sessionID, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("SendCode() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	if got := decoded(t, rr); got["codeEmail"] != "p•••@lakeside.edu" || got["codeExpiresAt"] == nil {
		t.Errorf("SendCode() answered %v, want the masked address and when the code expires", got)
	}
	row := links.last(t)
	if row.Kind != sessionstorage.MailLinkFirstPassword || row.UserID.UUID != userID || row.Email != "pat@lakeside.edu" {
		t.Errorf("stored code = %+v, want a first-password code for pat at the confirmed address", row)
	}
	if lifetime := time.Until(row.ExpiresAt); lifetime > defaultEmailCodeTimeout || lifetime < defaultEmailCodeTimeout-time.Minute {
		t.Errorf("the code is good for %s, want the mailed code's ten minutes", lifetime)
	}
	msgs := recorder.Messages()
	if len(msgs) != 1 || msgs[0].To != "pat@lakeside.edu" {
		t.Fatalf("messages = %+v, want the code mailed to the confirmed address", msgs)
	}
	code := firstPasswordCodeIn(t, msgs[0])
	if strings.Contains(string(row.TokenHash), code) {
		t.Error("the stored row holds the code itself, want its hash")
	}
	if record := f.attemptsRecord("mail:pat@lakeside.edu"); record == nil || record.Failures != 1 {
		t.Errorf("the address's mail count after SendCode() = %+v, want one", record)
	}

	// The person types the code as the mail shows it, spaced.
	rr = f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": code[:3] + " " + code[3:], "password": chosenPassword}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("Set() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	if *written != 1 {
		t.Fatalf("the store wrote %d first passwords, want one", *written)
	}
	if _, err := f.auth.registered().comparePassword(user.PasswordHash, chosenPassword); err != nil {
		t.Errorf("the hash written does not match the password chosen: %v", err)
	}
	// The mock refuses a call it was not told of: no session was destroyed and none
	// started.
	if diff := cmp.Diff([]sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword}}, events); diff != "" {
		t.Errorf("auth events recorded on the session mismatch (-want +got):\n%s", diff)
	}
	if row := links.last(t); row.ConsumedAt == nil {
		t.Error("the code still stands after it wrote the password, want it ended")
	}
	if record := f.attemptsRecord("mail:pat@lakeside.edu"); record != nil {
		t.Errorf("the address's mail count after the code was used = %+v, want none", record)
	}
}

func firstPasswordCase2(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	user := directoryPerson(userID)
	f.expectSession(sessionID, user)
	// The mock refuses SetFirstUserPassword: no case here writes a password.
	set := func(code string) *httptest.ResponseRecorder {
		return f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": code, "password": chosenPassword}, nil)
	}

	if rr := set("123456"); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "Ask for a new one") {
		t.Errorf("Set() with no code sent = %d %s, want 409 saying to ask for one", rr.Code, rr.Body.String())
	}
	if rr := set(""); rr.Code != http.StatusConflict {
		t.Errorf("Set() with no code at all = %d %s, want 409", rr.Code, rr.Body.String())
	}

	// Another account's code is no code of this one.
	other := impulse.Must(impulse.NewUUID())
	expires := time.Now().Add(time.Minute).Truncate(time.Microsecond)
	links.insert(&sessionstorage.MailLink{
		TokenHash: hashFirstPasswordCode(other, expires, "654321"), Kind: sessionstorage.MailLinkFirstPassword, Email: "x@lakeside.edu",
		UserID: impulse.NullUUIDFromUUID(other), ExpiresAt: expires,
	})
	if rr := set("654321"); rr.Code != http.StatusConflict {
		t.Errorf("Set() with another account's code = %d %s, want 409: no code stands for this account", rr.Code, rr.Body.String())
	}
	if links.rows[0].ConsumedAt != nil {
		t.Error("the other account's code was ended, want it untouched")
	}

	code := sendFirstPasswordCode(t, f, recorder, sessionID)
	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}
	if rr := set(wrong); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "not the code we sent") {
		t.Errorf("Set() with a wrong code = %d %s, want 400 saying it is not the code sent", rr.Code, rr.Body.String())
	}
	if rr := set(""); rr.Code != http.StatusBadRequest {
		t.Errorf("Set() with no code while one stands = %d %s, want 400", rr.Code, rr.Body.String())
	}
	if record := f.attemptsRecord(firstPasswordKey(userID)); record == nil || record.Failures != 2 {
		t.Errorf("the account's wrong codes = %+v, want two counted", record)
	}

	// The code expires: it stands for nothing, and it is the right one.
	standing := links.last(t)
	links.mu.Lock()
	standing.ExpiresAt = time.Now().Add(-time.Second)
	links.mu.Unlock()
	if rr := set(code); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "expired") {
		t.Errorf("Set() with the code once it expired = %d %s, want 409 saying it expired", rr.Code, rr.Body.String())
	}
	if user.PasswordHash != nil {
		t.Error("the account holds a password, want none: no request carried a code that stands")
	}
}

func firstPasswordCase3(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	user := directoryPerson(userID)
	f.expectSession(sessionID, user)
	set := func(code string) *httptest.ResponseRecorder {
		return f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": code, "password": chosenPassword}, nil)
	}

	code := sendFirstPasswordCode(t, f, recorder, sessionID)
	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}
	for attempt := 1; attempt < emailCodeAttempts; attempt++ {
		if rr := set(wrong); rr.Code != http.StatusBadRequest {
			t.Fatalf("wrong code %d = %d %s, want 400", attempt, rr.Code, rr.Body.String())
		}
	}
	rr := set(wrong)
	if rr.Code != http.StatusTooManyRequests || decoded(t, rr)["code"] != string(sessioninfo.RefusedTooManyAttempts) || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("the fifth wrong code = %d %s, want 429 too_many_attempts and the wait", rr.Code, rr.Body.String())
	}
	if got := decoded(t, rr)["message"]; got != "Too many wrong codes were entered. Ask for a new code in 15 minutes" {
		t.Errorf("the fifth wrong code said %q, want it to say wrong codes were what was held and when to ask again", got)
	}
	if links.last(t).ConsumedAt == nil {
		t.Error("the code stands after five wrong ones, want it ended")
	}

	// The account is held: the right code is taken no more, and no new one is mailed.
	if rr := set(code); rr.Code != http.StatusTooManyRequests {
		t.Errorf("Set() with the right code while the account is held = %d %s, want 429", rr.Code, rr.Body.String())
	}
	if rr := f.asUser(f.auth.FirstPassword().SendCode(), sessionID, nil, nil); rr.Code != http.StatusTooManyRequests || decoded(t, rr)["code"] != string(sessioninfo.RefusedTooManyAttempts) {
		t.Errorf("SendCode() while the account is held = %d %s, want 429 too_many_attempts", rr.Code, rr.Body.String())
	}
	if got := len(recorder.Messages()); got != 1 {
		t.Errorf("%d messages were sent, want the first code alone", got)
	}
	if user.PasswordHash != nil {
		t.Error("the account holds a password, want none")
	}
}

func firstPasswordCase4(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	user := directoryPerson(userID)
	user.PasswordHash = hashed(t, "the one I have")
	f.expectSession(sessionID, user)
	// A code that would stand for the account, were it asked for before the password
	// was set: it writes nothing over the password.
	expires := time.Now().Add(time.Minute).Truncate(time.Microsecond)
	links.insert(&sessionstorage.MailLink{
		TokenHash: hashFirstPasswordCode(userID, expires, "123456"), Kind: sessionstorage.MailLinkFirstPassword, Email: "pat@lakeside.edu",
		UserID: impulse.NullUUIDFromUUID(userID), ExpiresAt: expires,
	})

	rr := f.asUser(f.auth.FirstPassword().SendCode(), sessionID, nil, nil)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "already has a password") {
		t.Errorf("SendCode() for an account that holds a password = %d %s, want 409 saying it has one", rr.Code, rr.Body.String())
	}
	rr = f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": "123456", "password": chosenPassword}, nil)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "already has a password") {
		t.Errorf("Set() for an account that holds a password = %d %s, want 409 saying it has one", rr.Code, rr.Body.String())
	}
	if got := len(recorder.Messages()); got != 0 {
		t.Errorf("%d messages were sent, want none", got)
	}
}

func firstPasswordCase5(t *testing.T) {
	t.Parallel()

	for name, user := range map[string]func(userID impulse.UUID) *sessionstorage.SessionUser{
		"no address": func(userID impulse.UUID) *sessionstorage.SessionUser {
			return &sessionstorage.SessionUser{ID: userID, Username: "pat"}
		},
		"an address nobody confirmed": func(userID impulse.UUID) *sessionstorage.SessionUser {
			return &sessionstorage.SessionUser{ID: userID, Username: "pat", Email: strPtr("pat@lakeside.edu")}
		},
		"a confirmed address no message can be sent to, as a simulated directory leaves one": func(userID impulse.UUID) *sessionstorage.SessionUser {
			person := directoryPerson(userID)
			person.Email = strPtr("pat")

			return person
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			userID, sessionID := newIDs()
			f, recorder, links := newMailFixture(t, gomock.NewController(t))
			f.expectSession(sessionID, user(userID))

			rr := f.asUser(f.auth.FirstPassword().SendCode(), sessionID, nil, nil)
			if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "no confirmed email address") || !strings.Contains(rr.Body.String(), "confirm it from the link") {
				t.Errorf("SendCode() = %d %s, want 409 saying the account has no confirmed address and how to confirm one", rr.Code, rr.Body.String())
			}
			if got := len(recorder.Messages()); got != 0 || len(links.rows) != 0 {
				t.Errorf("%d messages were sent and %d codes stored, want none", got, len(links.rows))
			}
		})
	}
}

func firstPasswordCase6(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, _ := newMailFixtureWithoutPassword(t, gomock.NewController(t))
	f.expectSession(sessionID, directoryPerson(userID))

	rr := f.asUser(f.auth.FirstPassword().SendCode(), sessionID, nil, nil)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "Nobody signs in to this application with a password") {
		t.Errorf("SendCode() on an Auth with no password method = %d %s, want 409 saying nobody signs in with one", rr.Code, rr.Body.String())
	}
	rr = f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": "123456", "password": chosenPassword}, nil)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "Nobody signs in to this application with a password") {
		t.Errorf("Set() on an Auth with no password method = %d %s, want 409 saying nobody signs in with one", rr.Code, rr.Body.String())
	}
	if got := len(recorder.Messages()); got != 0 {
		t.Errorf("%d messages were sent, want none", got)
	}
}

func firstPasswordCase7(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	user := directoryPerson(userID)
	f.expectSession(sessionID, user)
	written := expectFirstPassword(f, user)
	f.store.EXPECT().AppendAuthEvent(gomock.Any(), sessionID, gomock.Any()).Return(nil)

	code := sendFirstPasswordCode(t, f, recorder, sessionID)
	rr := f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": code, "password": "short"}, nil)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "at least 8 characters") {
		t.Errorf("Set() with a short password = %d %s, want 400 saying how long a password is", rr.Code, rr.Body.String())
	}
	if *written != 0 || links.last(t).ConsumedAt != nil {
		t.Fatalf("after the refused password: %d written, the code ended = %t; want none written and the code standing", *written, links.last(t).ConsumedAt != nil)
	}
	if record := f.attemptsRecord(firstPasswordKey(userID)); record != nil {
		t.Errorf("the refused password counted as a wrong code: %+v", record)
	}
	rr = f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": code, "password": chosenPassword}, nil)
	if rr.Code != http.StatusOK || *written != 1 {
		t.Errorf("Set() with the same code and a password long enough = %d %s (%d written), want it written", rr.Code, rr.Body.String(), *written)
	}
}

func firstPasswordCase8(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	user := directoryPerson(userID)
	f.expectSession(sessionID, user)
	written := expectFirstPassword(f, user)
	f.store.EXPECT().AppendAuthEvent(gomock.Any(), sessionID, gomock.Any()).Return(nil)
	set := func(code string) *httptest.ResponseRecorder {
		return f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": code, "password": chosenPassword}, nil)
	}

	first := sendFirstPasswordCode(t, f, recorder, sessionID)
	second := sendFirstPasswordCode(t, f, recorder, sessionID)
	if links.rows[0].ConsumedAt == nil || links.rows[1].ConsumedAt != nil {
		t.Fatalf("after a second code: the first ended = %t, the second ended = %t; want the first ended and the second standing", links.rows[0].ConsumedAt != nil, links.rows[1].ConsumedAt != nil)
	}
	if first != second {
		if rr := set(first); rr.Code != http.StatusBadRequest {
			t.Errorf("Set() with the code a newer one replaced = %d %s, want 400: it is not the code that stands", rr.Code, rr.Body.String())
		}
	}
	if rr := set(second); rr.Code != http.StatusOK || *written != 1 {
		t.Fatalf("Set() with the code that stands = %d %s (%d written), want it written", rr.Code, rr.Body.String(), *written)
	}
	// The account holds a password now: the code writes no second one.
	if rr := set(second); rr.Code != http.StatusConflict || *written != 1 {
		t.Errorf("Set() with a used code = %d %s (%d written), want 409 and one password written", rr.Code, rr.Body.String(), *written)
	}
}

func firstPasswordCase9(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, _ := newMailFixture(t, gomock.NewController(t))
	f.expectSession(sessionID, directoryPerson(userID))

	for range mailLimit.Failures {
		sendFirstPasswordCode(t, f, recorder, sessionID)
	}
	rr := f.asUser(f.auth.FirstPassword().SendCode(), sessionID, nil, nil)
	if rr.Code != http.StatusTooManyRequests || decoded(t, rr)["code"] != string(sessioninfo.RefusedTooManyEmails) {
		t.Errorf("SendCode() past the address's limit = %d %s, want 429 too_many_emails", rr.Code, rr.Body.String())
	}
	if got := len(recorder.Messages()); got != mailLimit.Failures {
		t.Errorf("%d messages were sent, want %d", got, mailLimit.Failures)
	}
}

func firstPasswordCase10(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, _ := newMailFixture(t, gomock.NewController(t))
	user := directoryPerson(userID)
	f.expectSession(sessionID, user)
	written := expectFirstPassword(f, user)
	f.store.EXPECT().AppendAuthEvent(gomock.Any(), sessionID, gomock.Any()).Return(sessionstorage.ErrNoAuthEvents)

	code := sendFirstPasswordCode(t, f, recorder, sessionID)
	rr := f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": code, "password": chosenPassword}, nil)
	if rr.Code != http.StatusOK || *written != 1 {
		t.Errorf("Set() on an Auth that records no auth events = %d %s (%d written), want the password written", rr.Code, rr.Body.String(), *written)
	}

	plain := newAuthFixture(t, gomock.NewController(t))
	if recovered := registrationPanic(func() { plain.auth.FirstPassword() }); recovered == nil || !strings.Contains(fmt.Sprint(recovered), "FirstPassword() needs mail") {
		t.Errorf("FirstPassword() on an Auth without mail panic = %v, want it to say the handlers need mail", recovered)
	}
}

// TestAuth_Methods_firstPassword pins the routes the account page is told: the paths of
// the first password handlers where the list names them, nothing where it does not,
// and a wiring mistake refused where the routes are wired.
func firstPasswordCase11(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		// change is what happens to the account between the code and the password.
		change   func(user *sessionstorage.SessionUser)
		wantSays string
		// wantEnded says the code is ended by the refusal.
		wantEnded bool
	}{
		{
			name: "the confirmed address was replaced after the code was mailed: the code is ended",
			change: func(user *sessionstorage.SessionUser) {
				user.Email = strPtr("elsewhere@lakeside.edu")
			},
			wantSays:  "Ask for a new one",
			wantEnded: true,
		},
		{
			name: "the address is no longer confirmed: the account is told to confirm one",
			change: func(user *sessionstorage.SessionUser) {
				user.EmailVerifiedAt = nil
			},
			wantSays: "no confirmed email address",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			userID, sessionID := newIDs()
			f, recorder, links := newMailFixture(t, gomock.NewController(t))
			user := directoryPerson(userID)
			f.expectSession(sessionID, user)
			written := expectFirstPassword(f, user)

			code := sendFirstPasswordCode(t, f, recorder, sessionID)
			tt.change(user)
			rr := f.asUser(f.auth.FirstPassword().Set(), sessionID, map[string]string{"code": code, "password": chosenPassword}, nil)
			if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), tt.wantSays) {
				t.Errorf("Set() with the code mailed before the change = %d %s, want 409 saying %q", rr.Code, rr.Body.String(), tt.wantSays)
			}
			if *written != 0 || user.PasswordHash != nil {
				t.Errorf("%d passwords were written, want none: the code was mailed to an address the account does not hold confirmed", *written)
			}
			if ended := links.last(t).ConsumedAt != nil; ended != tt.wantEnded {
				t.Errorf("the code is ended = %t, want %t", ended, tt.wantEnded)
			}
		})
	}
}

func TestAuth_Methods_firstPassword(t *testing.T) {
	t.Parallel()

	password := []AdvertisedMethod{{Name: "password", Label: "Password", Path: "/api/user/password/login"}}
	routes := FirstPasswordRoutes{SendCode: "/api/user/password/first/code", Set: "/api/user/password/first"}
	answered := func(t *testing.T, list *MethodList) map[string]any {
		t.Helper()

		rr := httptest.NewRecorder()
		registerThree(t).Methods(list).ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/user/methods", http.NoBody))
		if rr.Code != http.StatusOK {
			t.Fatalf("Methods() status = %d, want 200: %s", rr.Code, rr.Body.String())
		}

		var got map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		return got
	}

	t.Run("the list names the routes, and the answer carries them", func(t *testing.T) {
		t.Parallel()
		got := answered(t, &MethodList{Methods: password, ChangePassword: "/api/user/password/change", FirstPassword: routes})
		want := map[string]any{"sendCode": "/api/user/password/first/code", "set": "/api/user/password/first"}
		if diff := cmp.Diff(want, got["firstPassword"]); diff != "" {
			t.Errorf("Methods() firstPassword mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a list that names no routes answers none", func(t *testing.T) {
		t.Parallel()
		if got := answered(t, &MethodList{Methods: password, ChangePassword: "/api/user/password/change"}); got["firstPassword"] != nil {
			t.Errorf("Methods() answered firstPassword = %v, want none", got["firstPassword"])
		}
	})

	for _, tt := range []struct {
		name string
		list *MethodList
		want string
	}{
		{
			name: "a route off-site",
			list: &MethodList{Methods: password, ChangePassword: "/password", FirstPassword: FirstPasswordRoutes{SendCode: "https://evil.example/code", Set: "/password/first"}},
			want: "name the send-code and set paths together",
		},
		{
			name: "one route without the other",
			list: &MethodList{Methods: password, ChangePassword: "/password", FirstPassword: FirstPasswordRoutes{Set: "/password/first"}},
			want: "name the send-code and set paths together",
		},
		{
			name: "the routes without the password method",
			list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "Google", Path: "/login"}}, FirstPassword: routes},
			want: "lists no password method",
		},
	} {
		t.Run(tt.name+" is refused where the routes are wired", func(t *testing.T) {
			t.Parallel()
			a := registerThree(t)

			recovered := registrationPanic(func() { a.Methods(tt.list) })
			if msg, _ := recovered.(string); !strings.Contains(msg, tt.want) {
				t.Errorf("Methods() panic = %v, want it to contain %q", recovered, tt.want)
			}
		})
	}
}
