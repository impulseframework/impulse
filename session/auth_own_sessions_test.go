package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// The User-Agent headers of the browsers these tests sign in from.
const (
	chromeOnLinuxAgent    = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	firefoxOnWindowsAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0"
)

// TestAuth_signIn_keepsTheBrowser proves a sign-in hands the store the name of the
// browser it came from, as StartSession worked it out from the request's User-Agent
// header, and none where the request carries no header.
func TestAuth_signIn_keepsTheBrowser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userAgent string
		want      string
	}{
		{name: "a sign-in from Chrome on Linux keeps that name", userAgent: chromeOnLinuxAgent, want: "Chrome on Linux"},
		{name: "a sign-in with no User-Agent header keeps none", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newAuthFixture(t, gomock.NewController(t))
			userID, sessionID := newIDs()
			f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: hashed(t, "pw")}, nil)
			got := "no sign-in"
			f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) { got = req.Browser }, nil)

			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/login", strings.NewReader(`{"username":"pat","password":"pw"}`))
			r.Header.Set("Content-Type", "application/json")
			if tt.userAgent != "" {
				r.Header.Set("User-Agent", tt.userAgent)
			}
			rr := httptest.NewRecorder()
			f.auth.StartSession(f.password.Login()).ServeHTTP(rr, r)
			if rr.Code != http.StatusOK {
				t.Fatalf("Login() = %d %s, want 200", rr.Code, rr.Body.String())
			}
			if got != tt.want {
				t.Errorf("the sign-in's request carries the browser %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAuth_ChangeUserPassword_keepsTheBrowser proves the session a person's own password
// change continues them in keeps the name of the browser the change was made from.
func TestAuth_ChangeUserPassword_keepsTheBrowser(t *testing.T) {
	t.Parallel()

	userID, sessionID := newIDs()
	f := newAuthFixture(t, gomock.NewController(t))
	old := hashed(t, "the old one")
	f.store.EXPECT().Session(gomock.Any(), sessionID).Return(&sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID),
	}, nil)
	f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: old}, nil).Times(2)
	f.store.EXPECT().ChangeUserPassword(gomock.Any(), userID, old, gomock.Not(gomock.Nil())).Return(nil)
	got := "no session"
	createSession(f.store, impulse.Must(impulse.NewUUID()), func(req *sessioninfo.NewSessionRequest) { got = req.Browser })

	r := passwordChangeRequest(sessionID, map[string]string{"oldPassword": "the old one", "newPassword": "the new one, chosen with care"})
	r.Header.Set("User-Agent", firefoxOnWindowsAgent)
	r = r.WithContext(withBrowser(r.Context(), r))
	rr := httptest.NewRecorder()
	f.password.ChangeUserPassword().ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("ChangeUserPassword() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	if got != "Firefox on Windows" {
		t.Errorf("the continued session's request carries the browser %q, want %q", got, "Firefox on Windows")
	}
}

// ownSessionsCase is a signed-in person of the own-session handlers' tests, with the
// helpers that make their requests.
type ownSessionsCase struct {
	t         *testing.T
	f         *authFixture
	userID    impulse.UUID
	sessionID impulse.UUID
}

func newOwnSessionsCase(t *testing.T) *ownSessionsCase {
	t.Helper()

	userID, sessionID := newIDs()

	return &ownSessionsCase{t: t, f: newAuthFixture(t, gomock.NewController(t)), userID: userID, sessionID: sessionID}
}

// signedIn expects the session's validation: the request's session, live, and its user.
func (c *ownSessionsCase) signedIn() {
	c.f.store.EXPECT().Session(gomock.Any(), c.sessionID).Return(&sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: c.sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(c.userID),
	}, nil)
	c.f.store.EXPECT().User(gomock.Any(), c.userID).Return(&sessionstorage.SessionUser{ID: c.userID, Username: "pat"}, nil)
}

// ctx is the context of a request made in the case's session.
func (c *ownSessionsCase) ctx() context.Context {
	return context.WithValue(context.Background(), sessioninfo.CTXSessionID, c.sessionID)
}

// serve runs h on a request with ctx and body (none where it is empty).
func (c *ownSessionsCase) serve(h http.Handler, ctx context.Context, method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(ctx, method, "/api/user/sessions", strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)

	return rr
}

// listed expects the store's list of the person's live sessions to answer rows.
func (c *ownSessionsCase) listed(rows ...*sessionstorage.OwnSession) {
	c.f.store.EXPECT().OwnSessions(gomock.Any(), c.userID, gomock.Any()).Return(rows, nil)
}

// unexpired expects the store's list of every session of the person's own that is not
// ended, whenever it was last used (no cut), to answer rows.
func (c *ownSessionsCase) unexpired(rows ...*sessionstorage.OwnSession) {
	c.f.store.EXPECT().OwnSessions(gomock.Any(), c.userID, time.Time{}).Return(rows, nil)
}

// timedOut is a moment before the session timeout of the case's Auth: a session last used
// then is no longer live.
func (c *ownSessionsCase) timedOut() time.Time {
	return time.Now().Add(-2 * c.f.auth.baseSession.SessionTimeout)
}

// row is a session of the person's own as the store lists it.
func row(id impulse.UUID, browser string, createdAt, updatedAt time.Time) *sessionstorage.OwnSession {
	s := &sessionstorage.OwnSession{ID: id, CreatedAt: createdAt, UpdatedAt: updatedAt}
	if browser != "" {
		s.Browser = &browser
	}

	return s
}

// messageOf is the message of a refusal's body.
func messageOf(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", rr.Body.String(), err)
	}

	return body.Message
}

// TestAuth_OwnSessions proves the list of where a person is signed in: the request's
// own session first and marked current, the rest in the store's order, each by its
// browser's name where it has one, with the count of the person's remembered browsers.
func TestAuth_OwnSessions(t *testing.T) {
	t.Parallel()

	t.Run("the request's session comes first and is current, the others follow in the store's order, and the remembered browsers are counted", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()
		signedIn := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
		windows, unnamed := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
		var activeSince time.Time
		before := time.Now()
		c.f.store.EXPECT().OwnSessions(gomock.Any(), c.userID, gomock.Any()).DoAndReturn(func(_ context.Context, _ impulse.UUID, since time.Time) ([]*sessionstorage.OwnSession, error) {
			activeSince = since

			return []*sessionstorage.OwnSession{
				row(windows, "Firefox on Windows", signedIn, signedIn.Add(2*time.Hour)),
				row(c.sessionID, "Chrome on Android", signedIn.Add(time.Hour), signedIn.Add(90*time.Minute)),
				row(unnamed, "", signedIn, signedIn.Add(time.Minute)),
			}, nil
		})
		c.f.store.EXPECT().TrustedDeviceCount(gomock.Any(), c.userID).Return(2, nil)

		rr := c.serve(c.f.auth.OwnSessions(), c.ctx(), http.MethodGet, "")
		after := time.Now()
		if rr.Code != http.StatusOK {
			t.Fatalf("OwnSessions() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		var got struct {
			Sessions           []map[string]any `json:"sessions"`
			RememberedBrowsers int              `json:"rememberedBrowsers"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", rr.Body.String(), err)
		}
		at := func(t time.Time) string { return t.Format(time.RFC3339Nano) }
		want := []map[string]any{
			{"id": c.sessionID.String(), "browser": "Chrome on Android", "phone": true, "signedInAt": at(signedIn.Add(time.Hour)), "lastUsedAt": at(signedIn.Add(90 * time.Minute)), "current": true},
			{"id": windows.String(), "browser": "Firefox on Windows", "phone": false, "signedInAt": at(signedIn), "lastUsedAt": at(signedIn.Add(2 * time.Hour)), "current": false},
			{"id": unnamed.String(), "phone": false, "signedInAt": at(signedIn), "lastUsedAt": at(signedIn.Add(time.Minute)), "current": false},
		}
		if len(got.Sessions) != len(want) {
			t.Fatalf("OwnSessions() listed %d sessions, want %d: %s", len(got.Sessions), len(want), rr.Body.String())
		}
		for i := range want {
			for key, value := range want[i] {
				if got.Sessions[i][key] != value {
					t.Errorf("OwnSessions() sessions[%d].%s = %v, want %v", i, key, got.Sessions[i][key], value)
				}
			}
			if len(got.Sessions[i]) != len(want[i]) {
				t.Errorf("OwnSessions() sessions[%d] = %v, want exactly %v", i, got.Sessions[i], want[i])
			}
		}
		if got.RememberedBrowsers != 2 {
			t.Errorf("OwnSessions() rememberedBrowsers = %d, want 2", got.RememberedBrowsers)
		}
		timeout := c.f.auth.baseSession.SessionTimeout
		if activeSince.Before(before.Add(-timeout)) || activeSince.After(after.Add(-timeout)) {
			t.Errorf("OwnSessions() asked the store for the sessions used since %v, want now less the session timeout (%v)", activeSince, timeout)
		}
	})

	t.Run("a person the store lists nothing for gets an empty list, never null", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()
		c.listed()
		c.f.store.EXPECT().TrustedDeviceCount(gomock.Any(), c.userID).Return(0, nil)

		rr := c.serve(c.f.auth.OwnSessions(), c.ctx(), http.MethodGet, "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"sessions":[]`) || !strings.Contains(rr.Body.String(), `"rememberedBrowsers":0`) {
			t.Errorf("OwnSessions() = %d %s, want 200 with an empty list and no remembered browser", rr.Code, rr.Body.String())
		}
	})
}

// TestAuth_EndOwnSession proves a person signs out of one browser of their own: one of
// their sessions that is not ended ends, whether or not it was used within the timeout;
// one the store does not hold, and the browser the person is using, end nothing.
func TestAuth_EndOwnSession(t *testing.T) {
	t.Parallel()

	t.Run("a session of the person's own ends", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()
		other := impulse.Must(impulse.NewUUID())
		c.unexpired(row(c.sessionID, "Chrome on Linux", time.Now(), time.Now()), row(other, "Safari on iPhone", time.Now(), time.Now()))
		c.f.store.EXPECT().DestroySession(gomock.Any(), other).Return(nil)

		rr := c.serve(c.f.auth.EndOwnSession(), c.ctx(), http.MethodPost, `{"sessionId":"`+other.String()+`"}`)
		if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
			t.Errorf("EndOwnSession() = %d %s, want 200 with no body", rr.Code, rr.Body.String())
		}
	})

	t.Run("a session of the person's own that timed out ends too, so nothing can bring it back", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()
		idle := impulse.Must(impulse.NewUUID())
		c.unexpired(row(c.sessionID, "Chrome on Linux", time.Now(), time.Now()), row(idle, "Safari on iPhone", c.timedOut(), c.timedOut()))
		c.f.store.EXPECT().DestroySession(gomock.Any(), idle).Return(nil)

		rr := c.serve(c.f.auth.EndOwnSession(), c.ctx(), http.MethodPost, `{"sessionId":"`+idle.String()+`"}`)
		if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
			t.Errorf("EndOwnSession() = %d %s, want 200 with no body", rr.Code, rr.Body.String())
		}
	})

	t.Run("a session the store does not hold is already signed out, and nothing ends", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()
		c.unexpired(row(c.sessionID, "Chrome on Linux", time.Now(), time.Now()))

		rr := c.serve(c.f.auth.EndOwnSession(), c.ctx(), http.MethodPost, `{"sessionId":"`+impulse.Must(impulse.NewUUID()).String()+`"}`)
		if rr.Code != http.StatusNotFound || messageOf(t, rr) != "That browser is already signed out." {
			t.Errorf("EndOwnSession() = %d %s, want 404 saying the browser is already signed out", rr.Code, rr.Body.String())
		}
	})

	t.Run("the browser the person is using is not ended here", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()

		rr := c.serve(c.f.auth.EndOwnSession(), c.ctx(), http.MethodPost, `{"sessionId":"`+c.sessionID.String()+`"}`)
		if rr.Code != http.StatusBadRequest || messageOf(t, rr) != "This is the browser you are using. Use Sign out to leave it." {
			t.Errorf("EndOwnSession() = %d %s, want 400 pointing at Sign out", rr.Code, rr.Body.String())
		}
	})

	t.Run("a body with a field the request does not have is refused", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()

		rr := c.serve(c.f.auth.EndOwnSession(), c.ctx(), http.MethodPost, `{"sessionId":"`+impulse.Must(impulse.NewUUID()).String()+`","all":true}`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("EndOwnSession() = %d %s, want 400", rr.Code, rr.Body.String())
		}
	})
}

// TestAuth_EndOtherSessions proves a person signs out of every browser but the one they
// are using, and is told how many that was.
func TestAuth_EndOtherSessions(t *testing.T) {
	t.Parallel()

	t.Run("every listed session but the request's own ends, and the answer counts them", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()
		first, second := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
		c.unexpired(row(first, "Firefox on Windows", time.Now(), time.Now()), row(c.sessionID, "Chrome on Linux", time.Now(), time.Now()), row(second, "", time.Now(), time.Now()))
		c.f.store.EXPECT().DestroySession(gomock.Any(), first).Return(nil)
		c.f.store.EXPECT().DestroySession(gomock.Any(), second).Return(nil)

		rr := c.serve(c.f.auth.EndOtherSessions(), c.ctx(), http.MethodPost, "")
		if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"ended":2}` {
			t.Errorf("EndOtherSessions() = %d %s, want 200 {\"ended\":2}", rr.Code, rr.Body.String())
		}
	})

	t.Run("a session that timed out ends with the live ones, and the live ones alone are counted", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()
		live, idle := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
		c.unexpired(row(c.sessionID, "Chrome on Linux", time.Now(), time.Now()), row(live, "Firefox on Windows", time.Now(), time.Now()), row(idle, "Safari on iPhone", c.timedOut(), c.timedOut()))
		c.f.store.EXPECT().DestroySession(gomock.Any(), live).Return(nil)
		c.f.store.EXPECT().DestroySession(gomock.Any(), idle).Return(nil)

		rr := c.serve(c.f.auth.EndOtherSessions(), c.ctx(), http.MethodPost, "")
		if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"ended":1}` {
			t.Errorf("EndOtherSessions() = %d %s, want 200 {\"ended\":1}", rr.Code, rr.Body.String())
		}
	})

	t.Run("others that all timed out end, and none is counted", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()
		idle := impulse.Must(impulse.NewUUID())
		c.unexpired(row(c.sessionID, "Chrome on Linux", time.Now(), time.Now()), row(idle, "Safari on iPhone", c.timedOut(), c.timedOut()))
		c.f.store.EXPECT().DestroySession(gomock.Any(), idle).Return(nil)

		rr := c.serve(c.f.auth.EndOtherSessions(), c.ctx(), http.MethodPost, "")
		if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"ended":0}` {
			t.Errorf("EndOtherSessions() = %d %s, want 200 {\"ended\":0}", rr.Code, rr.Body.String())
		}
	})

	t.Run("a person signed in nowhere else ends nothing", func(t *testing.T) {
		t.Parallel()
		c := newOwnSessionsCase(t)
		c.signedIn()
		c.unexpired(row(c.sessionID, "Chrome on Linux", time.Now(), time.Now()))

		rr := c.serve(c.f.auth.EndOtherSessions(), c.ctx(), http.MethodPost, "")
		if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"ended":0}` {
			t.Errorf("EndOtherSessions() = %d %s, want 200 {\"ended\":0}", rr.Code, rr.Body.String())
		}
	})
}

// TestAuth_ForgetBrowsers proves a person forgets every browser remembered for them: the
// store revokes their devices, this browser's cookie loses their entry and keeps the
// others', and no session ends.
func TestAuth_ForgetBrowsers(t *testing.T) {
	t.Parallel()

	c := newOwnSessionsCase(t)
	c.signedIn()
	someoneElse := impulse.Must(impulse.NewUUID())
	h := &trustedCookieHarness{t: t, a: c.f.auth}
	_, mine := h.newToken()
	_, theirs := h.newToken()
	carried := h.entriesWritten(map[string]trustedEntry{
		c.userID.String():    {Token: mine, At: time.Now().Unix()},
		someoneElse.String(): {Token: theirs, At: time.Now().Unix()},
	})
	c.f.store.EXPECT().RevokeTrustedDevices(gomock.Any(), c.userID).Return(nil)

	r := httptest.NewRequestWithContext(c.ctx(), http.MethodPost, "/api/user/browser/forget-all", http.NoBody)
	for _, ck := range carried {
		r.AddCookie(ck)
	}
	r = r.WithContext(c.f.auth.withTrustedDeviceCookie(r.Context(), r))
	rr := httptest.NewRecorder()
	c.f.auth.ForgetBrowsers().ServeHTTP(rr, r)
	if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
		t.Fatalf("ForgetBrowsers() = %d %s, want 200 with no body", rr.Code, rr.Body.String())
	}
	entries, deleted, absent := h.entriesOf(rr.Result().Cookies())
	if deleted || absent {
		t.Fatalf("ForgetBrowsers() deleted %v, absent %v; want the cookie rewritten with the other person's entry", deleted, absent)
	}
	if _, ok := entries[c.userID.String()]; ok {
		t.Errorf("the cookie still holds the person's entry: %v", entries)
	}
	if entries[someoneElse.String()].Token != theirs {
		t.Errorf("the cookie lost the other person's entry: %v", entries)
	}
}

// TestAuth_ownSessionHandlers_refuse proves each of the four handlers asks for a
// session of the person's own: none without a session, none under the password change
// mark, and none for a session someone else established acting as the person.
func TestAuth_ownSessionHandlers_refuse(t *testing.T) {
	t.Parallel()

	handlers := []struct {
		name    string
		handler func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.HandlerFunc
		method  string
		body    func(other impulse.UUID) string
	}{
		{name: "OwnSessions", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).OwnSessions, method: http.MethodGet, body: func(impulse.UUID) string { return "" }},
		{
			name: "EndOwnSession", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).EndOwnSession, method: http.MethodPost,
			body: func(other impulse.UUID) string { return `{"sessionId":"` + other.String() + `"}` },
		},
		{name: "EndOtherSessions", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).EndOtherSessions, method: http.MethodPost, body: func(impulse.UUID) string { return "" }},
		{name: "ForgetBrowsers", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).ForgetBrowsers, method: http.MethodPost, body: func(impulse.UUID) string { return "" }},
	}
	for _, h := range handlers {
		t.Run(h.name+": a request without a session is refused", func(t *testing.T) {
			t.Parallel()
			c := newOwnSessionsCase(t)
			c.f.store.EXPECT().Session(gomock.Any(), c.sessionID).Return(nil, httpio.NewNotFoundMessage("no session"))

			rr := c.serve(h.handler(c.f.auth), c.ctx(), h.method, h.body(impulse.Must(impulse.NewUUID())))
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("%s() = %d %s, want 401", h.name, rr.Code, rr.Body.String())
			}
		})
		t.Run(h.name+": a person under the password change mark is refused", func(t *testing.T) {
			t.Parallel()
			c := newOwnSessionsCase(t)
			c.f.store.EXPECT().Session(gomock.Any(), c.sessionID).Return(&sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{ID: c.sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(c.userID),
			}, nil)
			c.f.store.EXPECT().User(gomock.Any(), c.userID).Return(&sessionstorage.SessionUser{ID: c.userID, Username: "pat", PasswordChangeRequired: true}, nil)

			rr := c.serve(h.handler(c.f.auth), c.ctx(), h.method, h.body(impulse.Must(impulse.NewUUID())))
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s() = %d %s, want 403", h.name, rr.Code, rr.Body.String())
			}
		})
		t.Run(h.name+": a session someone else established acting as the person is refused", func(t *testing.T) {
			t.Parallel()
			c := newOwnSessionsCase(t)
			ctx := impersonatedCtx(c.sessionID, "pat", &sessioninfo.Impersonation{SessionID: c.sessionID, Actor: "admin"})
			ctx = context.WithValue(ctx, sessioninfo.CtxUserInfo, &sessioninfo.UserInfo{ID: c.userID, Username: "pat"})

			rr := c.serve(h.handler(c.f.auth), ctx, h.method, h.body(impulse.Must(impulse.NewUUID())))
			if rr.Code != http.StatusForbidden || messageOf(t, rr) != "You are acting as someone else, and this session cannot change their account." {
				t.Errorf("%s() = %d %s, want 403 saying the session cannot change their account", h.name, rr.Code, rr.Body.String())
			}
		})
	}
}
