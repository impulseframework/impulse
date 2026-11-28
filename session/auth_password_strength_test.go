package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/session/internal/attempts"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/internal/mockstore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// strengthAuth is an Auth with the password method over a store that answers the
// constructor's probes and nothing more: the mock refuses any other call, so a question
// that read a session, an account or a link fails the test.
func strengthAuth(t *testing.T, options ...PasswordOption) (*Auth[NoCustomData, NoCustomData, mockTx], *PasswordMethod) {
	t.Helper()

	store := mockstore.NewAuthStore[mockTx](gomock.NewController(t))
	store.EXPECT().CustomUserDataType().Return(nil).AnyTimes()
	store.EXPECT().SetUsernameIsEmail(gomock.Any()).AnyTimes()
	a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey, NoSuperAdministrators(), quietMail())
	if err != nil {
		t.Fatalf("NewAuth() error = %v", err)
	}

	return a, PasswordSignIn(a, options...)
}

// askStrength posts body to the strength route from address, with the XSRF cookie token
// cookie when it is not empty, behind StartSession as a mounted route is, and with no
// session.
func askStrength(a *Auth[NoCustomData, NoCustomData, mockTx], m *PasswordMethod, body, address, cookie string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/user/password/strength", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = address + ":4242"
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: internalcookie.XSRFCookieName, Value: cookie, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
	rr := httptest.NewRecorder()
	a.StartSession(m.Strength()).ServeHTTP(rr, r)

	return rr
}

// strengthBody is a question's body.
func strengthBody(t *testing.T, fields map[string]string) string {
	t.Helper()

	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	return string(raw)
}

// strengthAnswered decodes a 200 answer of the strength route as a page reads it.
func strengthAnswered(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	if rr.Code != http.StatusOK {
		t.Fatalf("the strength route = %d %s, want 200", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", rr.Body.String(), err)
	}

	return got
}

// TestPasswordMethod_Strength holds the answer a page draws its bar from: where the
// password stands, where the minimum sits, whether it is reached, and the sentence that
// says why while it is not, from the request alone.
func TestPasswordMethod_Strength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields map[string]string
		// strength is where the password must stand: exactly, or within [low, high].
		low, high    int
		strongEnough bool
		reason       string
	}{
		{name: "a weak password", fields: map[string]string{"password": "xq"}, low: 1, high: 25, reason: tooEasyToGuess},
		{name: "a middling password: on the bar, under the minimum", fields: map[string]string{"password": weakPassword}, low: 26, high: 49, reason: tooEasyToGuess},
		{name: "a strong password", fields: map[string]string{"password": strongAt30}, low: 50, high: 100, strongEnough: true},
		{
			name:   "a password built on the username the page sends",
			fields: map[string]string{"password": "vexillumtrek", "username": "vexillumtrek", "name": "Other Person", "email": "other@example.com"},
			low:    0, high: 49, reason: builtOnDetails,
		},
		{name: "the same password with no details", fields: map[string]string{"password": "vexillumtrek"}, low: 50, high: 100, strongEnough: true},
		{name: "a password found in breaches", fields: map[string]string{"password": listedPassword}, low: 0, high: 0, reason: foundInBreaches},
		{name: "no password: the empty one", fields: map[string]string{"username": "pat"}, low: 0, high: 0, reason: tooEasyToGuess},
		{name: "an empty password", fields: map[string]string{"password": ""}, low: 0, high: 0, reason: tooEasyToGuess},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, m := strengthAuth(t)
			got := strengthAnswered(t, askStrength(a, m, strengthBody(t, tt.fields), "10.0.0.7", ""))
			strength, _ := got["strength"].(float64)
			if int(strength) < tt.low || int(strength) > tt.high {
				t.Errorf("strength = %v, want it within %d to %d (%v)", got["strength"], tt.low, tt.high, got)
			}
			if got["minimum"] != float64(50) {
				t.Errorf("minimum = %v, want 50, where the minimum sits on the scale", got["minimum"])
			}
			if got["strongEnough"] != tt.strongEnough || got["strongEnough"] != (strength >= 50) {
				t.Errorf("strongEnough = %v, want %v, and exactly when strength %v has reached the minimum", got["strongEnough"], tt.strongEnough, strength)
			}
			reason, given := got["reason"]
			switch {
			case tt.strongEnough && given:
				t.Errorf("reason = %v, want none for a password strong enough", reason)
			case !tt.strongEnough && reason != tt.reason:
				t.Errorf("reason = %v, want %q", reason, tt.reason)
			}
			fields := 3
			if !tt.strongEnough {
				fields = 4
			}
			if len(got) != fields {
				t.Errorf("the answer = %v, want strongEnough, strength and minimum, and reason only while it falls short", got)
			}
		})
	}
}

func TestPasswordMethod_Strength_theApplicationsMinimum(t *testing.T) {
	t.Parallel()

	body := strengthBody(t, map[string]string{"password": strongAt30})
	a, m := strengthAuth(t)
	atDefault := strengthAnswered(t, askStrength(a, m, body, "10.0.0.7", ""))
	a, m = strengthAuth(t, MinimumPasswordStrength(60))
	at60 := strengthAnswered(t, askStrength(a, m, body, "10.0.0.7", ""))

	if atDefault["strongEnough"] != true || at60["strongEnough"] != false {
		t.Errorf("strongEnough = %v at the default and %v at 60 bits, want true and false", atDefault["strongEnough"], at60["strongEnough"])
	}
	lower, _ := at60["strength"].(float64)
	higher, _ := atDefault["strength"].(float64)
	if lower >= higher {
		t.Errorf("strength = %v at 60 bits, want it under the %v at the default: the minimum moves where a password stands", lower, higher)
	}
	if at60["minimum"] != float64(50) || at60["reason"] != tooEasyToGuess {
		t.Errorf("the answer at 60 bits = %v, want the minimum at 50 and the sentence that says why", at60)
	}
}

func TestPasswordMethod_Strength_askLimit(t *testing.T) {
	t.Parallel()

	const address, cookie = "10.0.0.8", "tok-strength"
	ctrl := gomock.NewController(t)
	f := newAuthFixture(t, ctrl)
	reads, writes, clears := f.counts()
	asks := attempts.ClientMultiple * strengthAskLimit.Failures
	for i := range asks - 1 {
		if rr := askStrength(f.auth, f.password, `{}`, address, cookie); rr.Code != http.StatusOK {
			t.Fatalf("question %d = %d %s, want 200 under the limit", i+1, rr.Code, rr.Body.String())
		}
	}

	rr := askStrength(f.auth, f.password, strengthBody(t, map[string]string{"password": strongAt30}), address, cookie)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("question %d = %d %s, want 429 at the limit", asks, rr.Code, rr.Body.String())
	}
	body := refusal(t, rr)
	seconds := int(strengthAskLimit.Lock.Seconds())
	want := refusalResponse{Message: strengthAskRefused, Code: sessioninfo.RefusedTooManyAttempts, RetryAfter: seconds}
	if body != want {
		t.Errorf("the refusal = %+v, want %+v", body, want)
	}
	if got := rr.Header().Get("Retry-After"); got != strconv.Itoa(seconds) {
		t.Errorf("Retry-After = %q, want %d", got, seconds)
	}
	if strings.Contains(rr.Body.String(), "strength") {
		t.Errorf("the refusal = %s, want nothing judged", rr.Body.String())
	}
	// Another address under the cookie is held too: the cookie is counted as well.
	if rr := askStrength(f.auth, f.password, `{}`, "10.0.0.9", cookie); rr.Code != http.StatusTooManyRequests {
		t.Errorf("a question under the held cookie from another address = %d, want 429", rr.Code)
	}

	if r, w, c := f.counts(); r != reads || w != writes || c != clears {
		t.Errorf("the questions read the attempts table %d, wrote it %d and cleared it %d times, want none", r-reads, w-writes, c-clears)
	}

	// A password login from the same client is not held: it is refused for its wrong
	// password, as any first attempt is.
	f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(&sessionstorage.SessionUser{Username: "pat", PasswordHash: hashed(t, "pw")}, nil)
	if rr := f.login("pat", "wrong", address, cookie); rr.Code != http.StatusUnauthorized || refusal(t, rr).Code != "" {
		t.Errorf("a login from the held client = %d %s, want 401 for the wrong password: asking holds no sign-in", rr.Code, rr.Body.String())
	}
}

func TestPasswordMethod_Strength_inNoLog(t *testing.T) {
	t.Parallel()

	output := captureLog(t)
	const password = "quillon-marrow-7741-sentinel"
	details := map[string]string{"username": "ostrander-quill", "name": "Ostrander Quill", "email": "ostrander-quill@example.com"}
	question := map[string]string{"password": password}
	for k, v := range details {
		question[k] = v
	}

	a, m := strengthAuth(t)
	if got := strengthAnswered(t, askStrength(a, m, strengthBody(t, question), "10.0.0.10", "tok-log")); got["strongEnough"] != true {
		t.Errorf("the answer = %v, want strong enough", got)
	}
	undecodable := `{"password":"` + password + `","username":` + details["username"]
	if rr := askStrength(a, m, undecodable, "10.0.0.10", "tok-log"); rr.Code != http.StatusBadRequest {
		t.Errorf("a body that does not decode = %d %s, want 400", rr.Code, rr.Body.String())
	} else if strings.Contains(rr.Body.String(), password) {
		t.Errorf("the answer to a body that does not decode = %s, want the password in no part of it", rr.Body.String())
	}
	a.methods.asks = attempts.New(sessionstorage.SignInAttemptLimit{Failures: 1, Window: strengthAskLimit.Window, Lock: strengthAskLimit.Lock}, nil)
	for range attempts.ClientMultiple - 1 {
		askStrength(a, m, `{}`, "10.0.0.10", "tok-log")
	}
	if rr := askStrength(a, m, strengthBody(t, question), "10.0.0.10", "tok-log"); rr.Code != http.StatusTooManyRequests {
		t.Errorf("the question at the limit = %d %s, want 429", rr.Code, rr.Body.String())
	}

	logged := output.String()
	if !strings.Contains(logged, "failed to decode request body") {
		t.Fatalf("the log holds no line of the body that does not decode, want one: the capture reads nothing\n%s", logged)
	}
	// The capture holds what every test running beside this one logs, refused logins among
	// them, so the question's line is found by the ask limit's sentence.
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, strengthAskRefused) {
			t.Errorf("the log holds a line of the question at the limit, want none (it is no refused login): %s", line)
		}
	}
	for _, said := range append([]string{password}, details["username"], details["name"], details["email"]) {
		if strings.Contains(logged, said) {
			t.Errorf("the log holds %q, want the password and the details in no line:\n%s", said, logged)
		}
	}
}
