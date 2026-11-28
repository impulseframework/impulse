package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// postLogin posts a password login for username and returns the answer's status,
// headers and body, so a scenario can read the headers a refusal carries.
func (b *browser) postLogin(ctx context.Context, username, password string) (status int, header http.Header, body []byte) {
	b.t.Helper()

	payload, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		b.t.Fatalf("json.Marshal() error = %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base.String()+"/login", bytes.NewReader(payload))
	if err != nil {
		b.t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(xsrfHeaderName, b.xsrfToken())
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatalf("http.Client.Do() error = %v", err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatalf("io.ReadAll() error = %v", err)
	}

	return resp.StatusCode, resp.Header, body
}

// lockForCases is the lock the attempt-limit cases configure. Every request a case makes
// while the name must still be locked has to land inside the lock, and a lock of two
// seconds ran out between two such requests on a slow runner. This one outlasts the
// requests of the longest case several times over, and each case waits it out once.
const lockForCases = 20 * time.Second

// assertTooManyAttempts checks a refused login: 429, the code and the wait in the body,
// the Retry-After header agreeing, and nothing about the name.
func assertTooManyAttempts(t *testing.T, status int, header http.Header, body []byte, maxWait time.Duration) {
	t.Helper()

	if status != http.StatusTooManyRequests {
		t.Fatalf("login = %d %s, want 429", status, body)
	}
	var answer struct {
		Message    string `json:"message"`
		Code       string `json:"code"`
		RetryAfter int    `json:"retryAfter"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", body, err)
	}
	if answer.Code != string(sessioninfo.RefusedTooManyAttempts) || answer.RetryAfter < 1 || time.Duration(answer.RetryAfter)*time.Second > maxWait {
		t.Errorf("refusal = %+v, want %s with a wait of at most %v", answer, sessioninfo.RefusedTooManyAttempts, maxWait)
	}
	if got := header.Get("Retry-After"); got != strconv.Itoa(answer.RetryAfter) {
		t.Errorf("Retry-After = %q, want %d, the wait in the body", got, answer.RetryAfter)
	}
	if bytes.Contains(body, []byte("lakeside")) {
		t.Errorf("refusal body = %s, want nothing about the name", body)
	}
}

func seamPasswordAttemptLimit[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	a.seed(ctx, t, "pat@lakeside.edu")
	b := a.browser(t)
	b.prime(ctx)

	// Two failures reach the limit; the third attempt, the right password, is refused
	// without being checked, and the browser holds no session.
	for i := range 2 {
		if status, _ := b.passwordLogin(ctx, "wrong"); status != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, status)
		}
	}
	status, header, body := b.postLogin(ctx, "pat@lakeside.edu", seedPassword)
	assertTooManyAttempts(t, status, header, body, lockForCases)
	if authenticated, _ := b.passwordChangeRequired(ctx); authenticated {
		t.Error("a session was established by the refused attempt")
	}

	// The lock is the name's, in the store: another browser is refused too, and so is
	// a name no user has after its own failures.
	other := a.browser(t)
	other.prime(ctx)
	status, header, body = other.postLogin(ctx, "Pat@Lakeside.edu", seedPassword)
	assertTooManyAttempts(t, status, header, body, lockForCases)
	for i := range 2 {
		if status, _, body := other.postLogin(ctx, "nobody@lakeside.edu", "guess"); status != http.StatusUnauthorized {
			t.Fatalf("unknown name attempt %d = %d %s, want 401", i+1, status, body)
		}
	}
	status, header, body = other.postLogin(ctx, "nobody@lakeside.edu", "guess")
	assertTooManyAttempts(t, status, header, body, lockForCases)

	// The lock passes; the right password signs in and resets the count, so one
	// failure after it does not lock the name again.
	time.Sleep(lockForCases + 200*time.Millisecond)
	if status, mfa := b.passwordLogin(ctx, seedPassword); status != http.StatusOK || mfa {
		t.Fatalf("login after the lock = %d (mfaIsRequired %v), want 200", status, mfa)
	}
	if status, _ := b.passwordLogin(ctx, "wrong"); status != http.StatusUnauthorized {
		t.Errorf("a failure after the success = %d, want 401", status)
	}
	if status, _ := b.passwordLogin(ctx, seedPassword); status != http.StatusOK {
		t.Errorf("the right password after one failure = %d, want 200: the count started over at the success", status)
	}
}

// seamPasswordGuessesAtOnce: the password attempt limit holds under guesses made at
// once, through the real handlers and the database's own counting. A dozen wrong
// passwords for one name sent together each ask for an attempt before the password is
// compared: three are compared and told what a wrong password is told, every other is
// refused with too_many_attempts unjudged, and none is answered with a failure of the
// server. Once the lock has passed, a dozen copies of the right password sent together
// are each signed in or refused as an attempt past the limit is, and the person signs
// in.
func seamPasswordGuessesAtOnce[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	a.seed(ctx, t, "pat@lakeside.edu")
	b := a.browser(t)
	b.prime(ctx)

	const (
		guesses = 12
		limit   = 3
	)
	// atOnce enters password for pat from a dozen copies of b's cookies together and
	// returns what each was answered.
	atOnce := func(password string) (statuses []int, bodies []string) {
		copies := make([]*browser, guesses)
		for i := range copies {
			copies[i] = b.clone()
		}
		statuses, bodies = make([]int, guesses), make([]string, guesses)
		failed := make([]error, guesses)
		var wg sync.WaitGroup
		for i, c := range copies {
			wg.Go(func() {
				status, body, err := c.send(ctx, http.MethodPost, "/login", map[string]string{"username": "pat@lakeside.edu", "password": password})
				statuses[i], bodies[i], failed[i] = status, string(body), err
			})
		}
		wg.Wait()
		for i, err := range failed {
			if err != nil {
				t.Fatalf("guess %d of %d at once: %v", i, guesses, err)
			}
		}

		return statuses, bodies
	}

	statuses, bodies := atOnce("wrong")
	wrong, refused := 0, 0
	for i, status := range statuses {
		switch {
		case status == http.StatusUnauthorized && strings.Contains(bodies[i], "The username or password is wrong. Check what you entered and try again"):
			wrong++
		case status == http.StatusTooManyRequests && strings.Contains(bodies[i], `"code":"too_many_attempts"`):
			refused++
		default:
			t.Errorf("wrong password %d of %d at once = %d %s, want 401 The username or password is wrong. Check what you entered and try again or 429 too_many_attempts", i, guesses, status, bodies[i])
		}
	}
	if wrong != limit || refused != guesses-limit {
		t.Errorf("%d wrong passwords at once: %d were compared and told so and %d were refused unjudged, want %d and %d", guesses, wrong, refused, limit, guesses-limit)
	}

	// The lock passes. The right password, entered from a dozen copies of the browser
	// at once: each is signed in or refused as an attempt past the limit is, and at
	// least one is signed in.
	time.Sleep(lockForCases + 200*time.Millisecond)
	statuses, bodies = atOnce(seedPassword)
	accepted := 0
	for i, status := range statuses {
		switch {
		case status == http.StatusOK:
			accepted++
		case status == http.StatusTooManyRequests && strings.Contains(bodies[i], `"code":"too_many_attempts"`):
		default:
			t.Errorf("right password %d of %d at once = %d %s, want 200 or 429 too_many_attempts", i, guesses, status, bodies[i])
		}
	}
	if accepted == 0 {
		t.Errorf("%d copies of the right password at once: none was signed in, want at least one", guesses)
	}
}

func seamSignedInHook[Tx any](ctx context.Context, t *testing.T, a *authApp[Tx], _ *fakeWorkOS) {
	t.Helper()

	pat := a.seed(ctx, t, "pat@lakeside.edu")
	b := a.browser(t)
	b.prime(ctx)

	// A wrong password and a sign-in the policy holds for MFA establish nothing.
	if status, _ := b.passwordLogin(ctx, "wrong"); status != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", status)
	}
	a.hooks.set(func(h *hooks[Tx]) {
		h.policy = func(*sessioninfo.NewSessionRequest) *signin.Decision { return signin.RequireMFA() }
	})
	if status, mfa := b.passwordLogin(ctx, seedPassword); status != http.StatusOK || !mfa {
		t.Fatalf("held login = %d (mfaIsRequired %v), want 200 with MFA required", status, mfa)
	}
	if got := a.hooks.signedInSessions(); len(got) != 0 {
		t.Fatalf("SignedInHook told of %v before any session was established", got)
	}

	// The MFA completion establishes the session: the hook is told once, of that
	// session, with the user and both steps.
	b.expect(ctx, http.StatusNoContent, http.MethodPost, "/mfa", nil)
	v := b.view(ctx)
	if diff := cmp.Diff([]string{v.SessionID}, a.hooks.signedInSessions()); diff != "" {
		t.Errorf("SignedInHook sessions mismatch (-want +got):\n%s", diff)
	}
	a.hooks.mu.Lock()
	established := a.hooks.established
	a.hooks.mu.Unlock()
	if len(established) != 1 || established[0].UserID != pat || established[0].Username != "pat@lakeside.edu" || established[0].Reason != sessioninfo.ReasonStepUp || len(established[0].Events) != 2 {
		t.Errorf("SignedInHook call = %+v, want pat's step-up session with the password and the MFA events", established)
	}
}
