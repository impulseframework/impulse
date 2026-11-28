package session

import (
	"context"
	"encoding/json"
	"fmt"
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

// The sentences a person reads when a change of their username is refused.
const (
	usernameIsAddressWords = "You sign in with your email address, so your username changes when you change the address."
	typeAUsernameWords     = "Type a username."
	usernameHeldWords      = "Someone else has that username."
	oneLineWords           = "A username is text on one line, with no tab or other control character."
	atMost200Words         = "A username is at most 200 characters."
	ownAddressWords        = "A username with an @ in it has to be your own email address. Type your own address, or choose a name without an @."
	noConfirmedWords       = "A username with an @ in it has to be your own email address, and your account has no confirmed address yet. Confirm your address first, or choose a name without an @."
)

// usernameCase is a person signed in as pat, of the username handlers' tests.
type usernameCase struct {
	t         *testing.T
	f         *authFixture
	userID    impulse.UUID
	sessionID impulse.UUID
}

func newUsernameCase(t *testing.T, passwordOptions ...PasswordOption) *usernameCase {
	t.Helper()

	userID, sessionID := newIDs()

	return &usernameCase{t: t, f: newAuthFixtureWith(t, gomock.NewController(t), passwordOptions), userID: userID, sessionID: sessionID}
}

// signedIn expects the session's validation: the request's session, live, and its user,
// pat. It returns the user's read, so a test that reads the user again orders the reads.
func (c *usernameCase) signedIn() *gomock.Call {
	return c.signedInAs(&sessionstorage.SessionUser{Username: "pat"})
}

// signedInAs expects the session's validation as signedIn does, with user, given the
// case's user ID, as the request's user. It returns the user's read.
func (c *usernameCase) signedInAs(user *sessionstorage.SessionUser) *gomock.Call {
	row := *user
	row.ID = c.userID
	c.f.store.EXPECT().Session(gomock.Any(), c.sessionID).Return(&sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: c.sessionID, Username: row.Username, UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(c.userID),
	}, nil)

	return c.f.store.EXPECT().User(gomock.Any(), c.userID).Return(&row, nil)
}

// post runs h on a POST with body in the case's session.
func (c *usernameCase) post(h http.Handler, body string) *httptest.ResponseRecorder {
	return c.postIn(h, context.WithValue(context.Background(), sessioninfo.CTXSessionID, c.sessionID), body)
}

// postIn runs h on a POST with body and ctx.
func (c *usernameCase) postIn(h http.Handler, ctx context.Context, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/user/username", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)

	return rr
}

// holder makes the store's lookup of name answer a user with id, or none for a nil id.
func (c *usernameCase) holder(name string, id *impulse.UUID) {
	call := c.f.store.EXPECT().UserByUserName(gomock.Any(), name)
	if id == nil {
		call.Return(nil, httpio.NewNotFoundMessagef("username %q does not exist", name))

		return
	}
	call.Return(&sessionstorage.SessionUser{ID: *id, Username: strings.ToLower(name)}, nil)
}

// usernameOf decodes {"username"} from an answer.
func usernameOf(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", rr.Body.String(), err)
	}

	return body.Username
}

// TestAuth_ChangeUsername proves a person's change of their own username: the one rule
// is that no one else holds the name; the space around a name is cut; a name in another
// case of the person's own is written; and where the usernames are the addresses
// nothing is read or written.
func TestAuth_ChangeUsername(t *testing.T) {
	t.Parallel()

	t.Run("where the usernames are the addresses the change is refused, and nothing is read or written", func(t *testing.T) {
		t.Parallel()
		c := newUsernameCase(t, UsernameIsEmail())
		c.signedIn()

		rr := c.post(c.f.auth.ChangeUsername(), `{"username":"robin"}`)
		if rr.Code != http.StatusConflict || messageOf(t, rr) != usernameIsAddressWords {
			t.Errorf("ChangeUsername() = %d %s, want 409 %q", rr.Code, rr.Body.String(), usernameIsAddressWords)
		}
	})

	t.Run("a name of nothing but space is refused", func(t *testing.T) {
		t.Parallel()
		c := newUsernameCase(t)
		c.signedIn()

		rr := c.post(c.f.auth.ChangeUsername(), `{"username":"   "}`)
		if rr.Code != http.StatusBadRequest || messageOf(t, rr) != typeAUsernameWords {
			t.Errorf("ChangeUsername() = %d %s, want 400 %q", rr.Code, rr.Body.String(), typeAUsernameWords)
		}
	})

	t.Run("the person's own username, with space around it, is answered and nothing is written", func(t *testing.T) {
		t.Parallel()
		c := newUsernameCase(t)
		c.signedIn()

		rr := c.post(c.f.auth.ChangeUsername(), `{"username":" pat "}`)
		if rr.Code != http.StatusOK || usernameOf(t, rr) != "pat" {
			t.Errorf("ChangeUsername() = %d %s, want 200 with pat", rr.Code, rr.Body.String())
		}
	})

	t.Run("a name someone else holds is refused, and nothing is written", func(t *testing.T) {
		t.Parallel()
		c := newUsernameCase(t)
		c.signedIn()
		sam := impulse.Must(impulse.NewUUID())
		c.holder("Sam", &sam)

		rr := c.post(c.f.auth.ChangeUsername(), `{"username":"Sam"}`)
		if rr.Code != http.StatusConflict || messageOf(t, rr) != usernameHeldWords {
			t.Errorf("ChangeUsername() = %d %s, want 409 %q", rr.Code, rr.Body.String(), usernameHeldWords)
		}
	})

	t.Run("a free name is written without the space around it, and answered as the store keeps it", func(t *testing.T) {
		t.Parallel()
		c := newUsernameCase(t)
		gomock.InOrder(
			c.signedIn(),
			c.f.store.EXPECT().UserByUserName(gomock.Any(), "Robin").Return(nil, httpio.NewNotFoundMessage("no such user")),
			c.f.store.EXPECT().SetUserUsername(gomock.Any(), c.userID, "Robin", aRecord()).Return(nil),
			c.f.store.EXPECT().User(gomock.Any(), c.userID).Return(&sessionstorage.SessionUser{ID: c.userID, Username: "Robin"}, nil),
		)

		rr := c.post(c.f.auth.ChangeUsername(), `{"username":"  Robin "}`)
		if rr.Code != http.StatusOK || usernameOf(t, rr) != "Robin" {
			t.Errorf("ChangeUsername() = %d %s, want 200 with Robin", rr.Code, rr.Body.String())
		}
	})

	t.Run("the person's own name in another case is written", func(t *testing.T) {
		t.Parallel()
		c := newUsernameCase(t)
		gomock.InOrder(
			c.signedIn(),
			c.f.store.EXPECT().UserByUserName(gomock.Any(), "PAT").Return(&sessionstorage.SessionUser{ID: c.userID, Username: "pat"}, nil),
			c.f.store.EXPECT().SetUserUsername(gomock.Any(), c.userID, "PAT", aRecord()).Return(nil),
			c.f.store.EXPECT().User(gomock.Any(), c.userID).Return(&sessionstorage.SessionUser{ID: c.userID, Username: "PAT"}, nil),
		)

		rr := c.post(c.f.auth.ChangeUsername(), `{"username":"PAT"}`)
		if rr.Code != http.StatusOK || usernameOf(t, rr) != "PAT" {
			t.Errorf("ChangeUsername() = %d %s, want 200 with PAT", rr.Code, rr.Body.String())
		}
	})

	t.Run("a name someone took meanwhile, which the store refuses as held, is refused in the same words", func(t *testing.T) {
		t.Parallel()
		c := newUsernameCase(t)
		c.signedIn()
		c.holder("robin", nil)
		c.f.store.EXPECT().SetUserUsername(gomock.Any(), c.userID, "robin", aRecord()).Return(httpio.NewConflictMessagef("username %q already exists", "robin"))

		rr := c.post(c.f.auth.ChangeUsername(), `{"username":"robin"}`)
		if rr.Code != http.StatusConflict || messageOf(t, rr) != usernameHeldWords {
			t.Errorf("ChangeUsername() = %d %s, want 409 %q", rr.Code, rr.Body.String(), usernameHeldWords)
		}
	})
}

// TestAuth_UsernameFree proves the question asked as a person types a username: whether
// they may take it, and nothing of who holds it.
func TestAuth_UsernameFree(t *testing.T) {
	t.Parallel()

	sam := impulse.Must(impulse.NewUUID())
	tests := []struct {
		name   string
		typed  string
		holder func(c *usernameCase) *impulse.UUID
		lookup bool
		want   string
	}{
		{name: "a name no one holds is free", typed: "robin", lookup: true, holder: func(*usernameCase) *impulse.UUID { return nil }, want: `{"free":true}`},
		{name: "a name someone else holds is not", typed: "sam", lookup: true, holder: func(*usernameCase) *impulse.UUID { return &sam }, want: `{"free":false}`},
		{name: "the person's own name in another case is free", typed: "PAT", lookup: true, holder: func(c *usernameCase) *impulse.UUID { return &c.userID }, want: `{"free":true}`},
		{name: "a name of nothing but space is not, and nothing is looked up", typed: "  ", want: `{"free":false}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newUsernameCase(t)
			c.signedIn()
			if tt.lookup {
				c.holder(tt.typed, tt.holder(c))
			}

			rr := c.post(c.f.auth.UsernameFree(), `{"username":"`+tt.typed+`"}`)
			if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != tt.want {
				t.Errorf("UsernameFree() = %d %s, want 200 %s", rr.Code, rr.Body.String(), tt.want)
			}
		})
	}

	t.Run("where the usernames are the addresses the question is refused, and nothing is looked up", func(t *testing.T) {
		t.Parallel()
		c := newUsernameCase(t, UsernameIsEmail())
		c.signedIn()

		rr := c.post(c.f.auth.UsernameFree(), `{"username":"robin"}`)
		if rr.Code != http.StatusConflict || messageOf(t, rr) != usernameIsAddressWords {
			t.Errorf("UsernameFree() = %d %s, want 409 %q", rr.Code, rr.Body.String(), usernameIsAddressWords)
		}
	})
}

// TestAuth_usernameHandlers_nameTheStoreKeeps proves both username handlers refuse, in
// words and before the store is asked, a name neither database keeps: one with a control
// character in it, or of more than 200 characters. A name of 200 characters, counted as
// characters and not bytes, goes on to the store.
func TestAuth_usernameHandlers_nameTheStoreKeeps(t *testing.T) {
	t.Parallel()

	handlers := []struct {
		name    string
		handler func(*Auth[NoCustomData, NoCustomData, mockTx]) http.HandlerFunc
		goesOn  string
	}{
		{name: "ChangeUsername", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).ChangeUsername, goesOn: "409 " + usernameHeldWords},
		{name: "UsernameFree", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).UsernameFree, goesOn: `200 {"free":false}`},
	}
	refused := []struct {
		name  string
		typed string
		code  int
		words string
	}{
		{name: "a name holding NUL is refused", typed: `pa\u0000t`, code: http.StatusBadRequest, words: oneLineWords},
		{name: "a name holding a line break is refused", typed: `pa\nt`, code: http.StatusBadRequest, words: oneLineWords},
		{name: "a name of 201 characters is refused", typed: strings.Repeat("a", 201), code: http.StatusBadRequest, words: atMost200Words},
	}
	for _, h := range handlers {
		for _, tt := range refused {
			t.Run(h.name+": "+tt.name+", and the store is not asked", func(t *testing.T) {
				t.Parallel()
				c := newUsernameCase(t)
				c.signedIn()

				rr := c.post(h.handler(c.f.auth), `{"username":"`+tt.typed+`"}`)
				if rr.Code != tt.code || messageOf(t, rr) != tt.words {
					t.Errorf("%s() = %d %s, want %d %q", h.name, rr.Code, rr.Body.String(), tt.code, tt.words)
				}
			})
		}

		t.Run(h.name+": a name of 200 characters goes on to the store", func(t *testing.T) {
			t.Parallel()
			c := newUsernameCase(t)
			c.signedIn()
			long := strings.Repeat("é", 200)
			sam := impulse.Must(impulse.NewUUID())
			c.holder(long, &sam)

			rr := c.post(h.handler(c.f.auth), `{"username":"`+long+`"}`)
			got := fmt.Sprintf("%d %s", rr.Code, strings.TrimSpace(rr.Body.String()))
			if rr.Code == http.StatusConflict {
				got = fmt.Sprintf("%d %s", rr.Code, messageOf(t, rr))
			}
			if got != h.goesOn {
				t.Errorf("%s() = %s, want %s: the name is looked up", h.name, got, h.goesOn)
			}
		})
	}
}

// TestAuth_usernameHandlers_addressAsUsername proves both username handlers hold a name
// with an @ in it to the person's own confirmed address, the same but for the case of its
// ASCII letters, and refuse any other in words before the store is asked, so the answer
// is the same whether or not an account holds the name. The person's current username,
// typed exactly as it is kept, is answered as before.
func TestAuth_usernameHandlers_addressAsUsername(t *testing.T) {
	t.Parallel()

	confirmedAt := time.Now()
	confirmed := func(username string) *sessionstorage.SessionUser {
		return &sessionstorage.SessionUser{Username: username, Email: strPtr("pat@lakeside.edu"), EmailVerifiedAt: &confirmedAt}
	}
	p1 := confirmed("pat")
	p2 := &sessionstorage.SessionUser{Username: "pat", Email: strPtr("pat@lakeside.edu")}
	p3 := &sessionstorage.SessionUser{Username: "pat"}
	p4 := confirmed("old@lakeside.edu")
	sam := impulse.Must(impulse.NewUUID())

	// A case's store expects the store's calls past the session's validation, in order
	// after read, the validation's read of the user; nil for none.
	type store func(c *usernameCase, read *gomock.Call)
	tests := []struct {
		name        string
		person      *sessionstorage.SessionUser
		typed       string
		change      string
		changeStore store
		free        string
		freeStore   store
	}{
		{
			name: "another's address is refused", person: p1, typed: "sam@lakeside.edu",
			change: "400 " + ownAddressWords, free: "400 " + ownAddressWords,
		},
		{
			name: "the person's own confirmed address in another case, which nobody holds, is taken", person: p1, typed: "Pat@Lakeside.EDU",
			change: `200 {"username":"Pat@Lakeside.EDU"}`,
			changeStore: func(c *usernameCase, read *gomock.Call) {
				gomock.InOrder(
					read,
					c.f.store.EXPECT().UserByUserName(gomock.Any(), "Pat@Lakeside.EDU").Return(nil, httpio.NewNotFoundMessage("no such user")),
					c.f.store.EXPECT().SetUserUsername(gomock.Any(), c.userID, "Pat@Lakeside.EDU", aRecord()).Return(nil),
					c.f.store.EXPECT().User(gomock.Any(), c.userID).Return(&sessionstorage.SessionUser{ID: c.userID, Username: "Pat@Lakeside.EDU"}, nil),
				)
			},
			free:      `200 {"free":true}`,
			freeStore: func(c *usernameCase, _ *gomock.Call) { c.holder("Pat@Lakeside.EDU", nil) },
		},
		{
			name: "the person's own confirmed address, which another account holds as its username, is held", person: p1, typed: "pat@lakeside.edu",
			change:      "409 " + usernameHeldWords,
			changeStore: func(c *usernameCase, _ *gomock.Call) { c.holder("pat@lakeside.edu", &sam) },
			free:        `200 {"free":false}`,
			freeStore:   func(c *usernameCase, _ *gomock.Call) { c.holder("pat@lakeside.edu", &sam) },
		},
		{
			name: "the person's own address, not confirmed, is refused", person: p2, typed: "pat@lakeside.edu",
			change: "400 " + noConfirmedWords, free: "400 " + noConfirmedWords,
		},
		{
			name: "an address, where the person holds none, is refused", person: p3, typed: "pat@lakeside.edu",
			change: "400 " + noConfirmedWords, free: "400 " + noConfirmedWords,
		},
		{
			name: "the person's own address with a Kelvin sign for its k is refused", person: p1, typed: "pat@la\u212Aeside.edu",
			change: "400 " + ownAddressWords, free: "400 " + ownAddressWords,
		},
		{
			name: "a name that opens with an @ is refused", person: p1, typed: "@pat",
			change: "400 " + ownAddressWords, free: "400 " + ownAddressWords,
		},
		{
			name: "a name that ends with an @ is refused", person: p1, typed: "pat@",
			change: "400 " + ownAddressWords, free: "400 " + ownAddressWords,
		},
		{
			name: "the person's current username with an @ in it, typed exactly, is answered as before", person: p4, typed: "old@lakeside.edu",
			change:    `200 {"username":"old@lakeside.edu"}`,
			free:      `200 {"free":true}`,
			freeStore: func(c *usernameCase, _ *gomock.Call) { c.holder("old@lakeside.edu", &c.userID) },
		},
		{
			name: "the person's current username with an @ in it, in another case, is a change and refused", person: p4, typed: "OLD@lakeside.edu",
			change: "400 " + ownAddressWords, free: "400 " + ownAddressWords,
		},
	}
	handlers := []struct {
		name    string
		handler func(*Auth[NoCustomData, NoCustomData, mockTx]) http.HandlerFunc
		want    func(tt int) (string, store)
	}{
		{name: "ChangeUsername", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).ChangeUsername, want: func(i int) (string, store) { return tests[i].change, tests[i].changeStore }},
		{name: "UsernameFree", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).UsernameFree, want: func(i int) (string, store) { return tests[i].free, tests[i].freeStore }},
	}
	for _, h := range handlers {
		for i, tt := range tests {
			t.Run(h.name+": "+tt.name, func(t *testing.T) {
				t.Parallel()
				c := newUsernameCase(t)
				read := c.signedInAs(tt.person)
				want, expect := h.want(i)
				// The mocked store refuses a call it was not told of: where expect is nil
				// the store is not asked.
				if expect != nil {
					expect(c, read)
				}

				rr := c.post(h.handler(c.f.auth), `{"username":"`+tt.typed+`"}`)
				got := fmt.Sprintf("%d %s", rr.Code, strings.TrimSpace(rr.Body.String()))
				if rr.Code >= http.StatusBadRequest {
					got = fmt.Sprintf("%d %s", rr.Code, messageOf(t, rr))
				}
				if got != want {
					t.Errorf("%s(%q) = %s, want %s", h.name, tt.typed, got, want)
				}
			})
		}
	}
}

// TestAuth_usernameHandlers_refuse proves both username handlers ask for a session of
// the person's own and a body of the request's fields alone.
func TestAuth_usernameHandlers_refuse(t *testing.T) {
	t.Parallel()

	handlers := []struct {
		name    string
		handler func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.HandlerFunc
	}{
		{name: "ChangeUsername", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).ChangeUsername},
		{name: "UsernameFree", handler: (*Auth[NoCustomData, NoCustomData, mockTx]).UsernameFree},
	}
	for _, h := range handlers {
		t.Run(h.name+": a request without a session is refused", func(t *testing.T) {
			t.Parallel()
			c := newUsernameCase(t)
			c.f.store.EXPECT().Session(gomock.Any(), c.sessionID).Return(nil, httpio.NewNotFoundMessage("no session"))

			if rr := c.post(h.handler(c.f.auth), `{"username":"robin"}`); rr.Code != http.StatusUnauthorized {
				t.Errorf("%s() = %d %s, want 401", h.name, rr.Code, rr.Body.String())
			}
		})
		t.Run(h.name+": a person under the password change mark is refused", func(t *testing.T) {
			t.Parallel()
			c := newUsernameCase(t)
			c.f.store.EXPECT().Session(gomock.Any(), c.sessionID).Return(&sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{ID: c.sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(c.userID),
			}, nil)
			c.f.store.EXPECT().User(gomock.Any(), c.userID).Return(&sessionstorage.SessionUser{ID: c.userID, Username: "pat", PasswordChangeRequired: true}, nil)

			if rr := c.post(h.handler(c.f.auth), `{"username":"robin"}`); rr.Code != http.StatusForbidden {
				t.Errorf("%s() = %d %s, want 403", h.name, rr.Code, rr.Body.String())
			}
		})
		t.Run(h.name+": a session someone else established acting as the person is refused", func(t *testing.T) {
			t.Parallel()
			c := newUsernameCase(t)
			ctx := impersonatedCtx(c.sessionID, "pat", &sessioninfo.Impersonation{SessionID: c.sessionID, Actor: "admin"})
			ctx = context.WithValue(ctx, sessioninfo.CtxUserInfo, &sessioninfo.UserInfo{ID: c.userID, Username: "pat"})

			rr := c.postIn(h.handler(c.f.auth), ctx, `{"username":"robin"}`)
			if rr.Code != http.StatusForbidden || messageOf(t, rr) != "You are acting as someone else, and this session cannot change their account." {
				t.Errorf("%s() = %d %s, want 403 saying the session cannot change their account", h.name, rr.Code, rr.Body.String())
			}
		})
		t.Run(h.name+": a body with a field the request does not have is refused", func(t *testing.T) {
			t.Parallel()
			c := newUsernameCase(t)
			c.signedIn()

			if rr := c.post(h.handler(c.f.auth), `{"username":"robin","userId":"x"}`); rr.Code != http.StatusBadRequest {
				t.Errorf("%s() = %d %s, want 400", h.name, rr.Code, rr.Body.String())
			}
		})
	}
}
