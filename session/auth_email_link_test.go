package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// linkHandler is one of the two handlers a mailed link that confirms an address is
// followed through: Verify, for a person's own link followed signed in, and Confirm,
// for an administrator's link followed by whoever reads the mailbox.
type linkHandler struct {
	name   string
	kind   sessionstorage.MailLinkKind
	follow func(f *authFixture, sessionID impulse.UUID, token string) *httptest.ResponseRecorder
}

var (
	verifyHandler = linkHandler{name: "Verify", kind: sessionstorage.MailLinkVerification, follow: func(f *authFixture, sessionID impulse.UUID, token string) *httptest.ResponseRecorder {
		return f.asUser(f.auth.Email().Verify(), sessionID, map[string]string{"token": token}, nil)
	}}
	confirmHandler = linkHandler{name: "Confirm", kind: sessionstorage.MailLinkAddressChange, follow: func(f *authFixture, _ impulse.UUID, token string) *httptest.ResponseRecorder {
		return f.serve(f.auth.Email().Confirm(), http.MethodPost, "/email/confirm", map[string]string{"token": token}, nil)
	}}
	linkHandlers = []linkHandler{verifyHandler, confirmHandler}
)

// linkCase is a mail fixture with one account signed in, whose row the store answers a
// confirmation from (mailLinks.expectConfirmation).
type linkCase struct {
	f                 *authFixture
	recorder          *mail.Recorder
	links             *mailLinks
	userID, sessionID impulse.UUID
	row               *sessionstorage.SessionUser
}

// newLinkCase is a linkCase whose account is the row user makes, signed in.
func newLinkCase(t *testing.T, user func(impulse.UUID) *sessionstorage.SessionUser, passwordOptions ...PasswordOption) *linkCase {
	t.Helper()

	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t), passwordOptions...)
	row := user(userID)
	f.expectSession(sessionID, row)

	return &linkCase{f: f, recorder: recorder, links: links, userID: userID, sessionID: sessionID, row: row}
}

// mailed stores a live link of the handler's kind to email for the case's account,
// mailed at at, and returns its token.
func (c *linkCase) mailed(t *testing.T, h linkHandler, email string, at time.Time) string {
	t.Helper()

	token, hash, err := linkToken()
	if err != nil {
		t.Fatal(err)
	}
	c.links.insert(&sessionstorage.MailLink{
		TokenHash: hash, Kind: h.kind, Email: email, UserID: impulse.NullUUIDFromUUID(c.userID), CreatedAt: at, ExpiresAt: time.Now().Add(time.Hour),
	})

	return token
}

// directoryLink gives the case's account an identity link through an organization's
// directory.
func (c *linkCase) directoryLink(t *testing.T) {
	linkIdentities(t, c.userID, &sessionstorage.SessionIdentity{UserID: c.userID, Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu", Subject: "g-1"})
}

// holding is an account named pat that holds email (none for nil), confirmed at
// confirmedAt (nil for not confirmed).
func holding(email *string, confirmedAt *time.Time) func(impulse.UUID) *sessionstorage.SessionUser {
	return func(userID impulse.UUID) *sessionstorage.SessionUser {
		return &sessionstorage.SessionUser{ID: userID, Username: "pat", Email: email, EmailVerifiedAt: confirmedAt}
	}
}

func TestAuth_ConfirmByLink(t *testing.T) {
	t.Parallel()

	t.Run("Verify refuses a directory person who holds a confirmed address with the directory's sentence, and writes for a directory person who holds none and for a person of no directory", authConfirmByLinkCase1)
	t.Run("Confirm answers a directory person who holds a confirmed address, and an account that confirmed an address after the link was mailed, as a dead link; one confirmed before it is written", authConfirmByLinkCase2)
	t.Run("both answer the store's NotFound as a dead link and its Conflict as 409", authConfirmByLinkCase3)
	t.Run("both tell the address the store answered as held and confirmed, and nobody where it answered none or one not confirmed", authConfirmByLinkCase4)
	t.Run("with usernames as addresses, both tell the rename hook the username the store answered and the new address whenever the store says renamed, a change of letter case included, and not when it says not renamed", authConfirmByLinkCase5)
	t.Run("neither consumes the link, writes the address nor reads the identity links outside the store's one confirmation", authConfirmByLinkCase6)
}

func authConfirmByLinkCase1(t *testing.T) {
	t.Parallel()
	hourAgo := time.Now().Add(-time.Hour)

	for _, tt := range []struct {
		name      string
		user      func(impulse.UUID) *sessionstorage.SessionUser
		directory bool
		want      int
	}{
		{name: "a directory person who holds a confirmed address", user: holding(strPtr("pat@lakeside.edu"), &hourAgo), directory: true, want: http.StatusConflict},
		{name: "a directory person who holds no address", user: holding(nil, nil), directory: true, want: http.StatusOK},
		{name: "a person of no directory who holds a confirmed address", user: holding(strPtr("pat@lakeside.edu"), &hourAgo), want: http.StatusOK},
	} {
		c := newLinkCase(t, tt.user)
		if tt.directory {
			c.directoryLink(t)
		}
		c.links.expectConfirmation(c.f, c.userID, "pat.lee@lakeside.edu", c.row)
		rr := verifyHandler.follow(c.f, c.sessionID, c.mailed(t, verifyHandler, "pat.lee@lakeside.edu", time.Now()))
		if rr.Code != tt.want {
			t.Errorf("Verify() by %s = %d %s, want %d", tt.name, rr.Code, rr.Body.String(), tt.want)
		}
		if tt.want == http.StatusConflict && decoded(t, rr)["message"] != httpio.Message(errDirectoryAddress()) {
			t.Errorf("Verify() by %s says %q, want the directory's sentence", tt.name, decoded(t, rr)["message"])
		}
		if consumed := c.links.rows[0].ConsumedAt != nil; consumed != (tt.want == http.StatusOK) {
			t.Errorf("Verify() by %s left the link consumed %v, want consumed only with a write", tt.name, consumed)
		}
	}
}

func authConfirmByLinkCase2(t *testing.T) {
	t.Parallel()
	hourAgo, now := time.Now().Add(-time.Hour), time.Now()

	for _, tt := range []struct {
		name      string
		user      func(impulse.UUID) *sessionstorage.SessionUser
		directory bool
		want      int
	}{
		{name: "a directory person who holds a confirmed address", user: holding(strPtr("pat@lakeside.edu"), &hourAgo), directory: true, want: http.StatusNotFound},
		{name: "an account that confirmed an address after the link was mailed", user: holding(strPtr("pat@hilltop.example"), &now), want: http.StatusNotFound},
		{name: "an account that confirmed its address before the link was mailed", user: holding(strPtr("pat@lakeside.edu"), &hourAgo), want: http.StatusOK},
	} {
		c := newLinkCase(t, tt.user)
		if tt.directory {
			c.directoryLink(t)
		}
		c.links.expectConfirmation(c.f, c.userID, "pat.lee@lakeside.edu", c.row)
		rr := confirmHandler.follow(c.f, c.sessionID, c.mailed(t, confirmHandler, "pat.lee@lakeside.edu", time.Now().Add(-time.Minute)))
		if rr.Code != tt.want {
			t.Errorf("Confirm() for %s = %d %s, want %d", tt.name, rr.Code, rr.Body.String(), tt.want)
		}
		if tt.want == http.StatusNotFound && decoded(t, rr)["message"] != httpio.Message(errLinkDead()) {
			t.Errorf("Confirm() for %s says %q, want the dead link's sentence", tt.name, decoded(t, rr)["message"])
		}
		if consumed := c.links.rows[0].ConsumedAt != nil; consumed != (tt.want == http.StatusOK) {
			t.Errorf("Confirm() for %s left the link consumed %v, want consumed only with a write", tt.name, consumed)
		}
	}
}

func authConfirmByLinkCase3(t *testing.T) {
	t.Parallel()
	hourAgo := time.Now().Add(-time.Hour)

	for _, h := range linkHandlers {
		// The account is gone by the store's transaction.
		c := newLinkCase(t, holding(strPtr("pat@lakeside.edu"), &hourAgo))
		c.links.expectConfirmation(c.f, c.userID, "pat.lee@lakeside.edu", nil)
		rr := h.follow(c.f, c.sessionID, c.mailed(t, h, "pat.lee@lakeside.edu", time.Now()))
		if rr.Code != http.StatusNotFound || decoded(t, rr)["message"] != httpio.Message(errLinkDead()) {
			t.Errorf("%s() answered NotFound by the store = %d %s, want 404 with the dead link's sentence", h.name, rr.Code, rr.Body.String())
		}

		// Another account holds the address by then.
		c = newLinkCase(t, holding(strPtr("pat@lakeside.edu"), &hourAgo))
		c.links.holds("taken@lakeside.edu", &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "sam"})
		c.links.expectConfirmation(c.f, c.userID, "taken@lakeside.edu", c.row)
		if rr := h.follow(c.f, c.sessionID, c.mailed(t, h, "taken@lakeside.edu", time.Now())); rr.Code != http.StatusConflict {
			t.Errorf("%s() answered Conflict by the store = %d %s, want 409", h.name, rr.Code, rr.Body.String())
		}
	}
}

func authConfirmByLinkCase4(t *testing.T) {
	t.Parallel()
	hourAgo := time.Now().Add(-time.Hour)

	for _, h := range linkHandlers {
		for _, tt := range []struct {
			name string
			user func(impulse.UUID) *sessionstorage.SessionUser
			// told is the address the notice goes to, "" for none.
			told string
		}{
			{name: "a confirmed address", user: holding(strPtr("pat@lakeside.edu"), &hourAgo), told: "pat@lakeside.edu"},
			{name: "no address", user: holding(nil, nil)},
			{name: "an address not confirmed", user: holding(strPtr("pat.old@lakeside.edu"), nil)},
		} {
			c := newLinkCase(t, tt.user)
			c.links.expectConfirmation(c.f, c.userID, "pat.lee@lakeside.edu", c.row)
			if rr := h.follow(c.f, c.sessionID, c.mailed(t, h, "pat.lee@lakeside.edu", time.Now())); rr.Code != http.StatusOK {
				t.Fatalf("%s() of an account holding %s = %d %s, want 200", h.name, tt.name, rr.Code, rr.Body.String())
			}
			msgs := c.recorder.Messages()
			switch {
			case tt.told == "" && len(msgs) != 0:
				t.Errorf("%s() of an account holding %s mailed %+v, want nothing", h.name, tt.name, msgs)
			case tt.told != "" && (len(msgs) != 1 || msgs[0].To != tt.told || msgs[0].Subject != "The email address of your Harbor account changed"):
				t.Errorf("%s() of an account holding %s mailed %+v, want the notice at %s", h.name, tt.name, msgs, tt.told)
			}
		}
	}
}

func authConfirmByLinkCase5(t *testing.T) {
	t.Parallel()

	for _, h := range linkHandlers {
		for _, tt := range []struct {
			name string
			to   string
			// renamed is the hook's call, "" for none.
			renamed string
		}{
			{name: "another address", to: "pat.lee@lakeside.edu", renamed: "pat.now@lakeside.edu -> pat.lee@lakeside.edu"},
			{name: "the username in other letter case", to: "PAT.NOW@lakeside.edu", renamed: "pat.now@lakeside.edu -> PAT.NOW@lakeside.edu"},
			{name: "the username as it is", to: "pat.now@lakeside.edu"},
		} {
			// The session names the account as it was named when it signed in; the store
			// answers the name its row holds in the confirmation's transaction.
			c := newLinkCase(t, func(userID impulse.UUID) *sessionstorage.SessionUser {
				return &sessionstorage.SessionUser{ID: userID, Username: "pat@lakeside.edu"}
			}, UsernameIsEmail())
			var renamed []string
			c.f.auth.SetUserRenamed(func(_ context.Context, id impulse.UUID, from, to string) error {
				if id != c.userID {
					t.Errorf("rename hook given user %s, want %s", id, c.userID)
				}
				renamed = append(renamed, from+" -> "+to)

				return nil
			})
			stored := &sessionstorage.SessionUser{ID: c.userID, Username: "pat.now@lakeside.edu", Email: strPtr("pat.now@lakeside.edu")}
			c.links.expectConfirmation(c.f, c.userID, tt.to, stored)
			if rr := h.follow(c.f, c.sessionID, c.mailed(t, h, tt.to, time.Now())); rr.Code != http.StatusOK {
				t.Fatalf("%s() of %s = %d %s, want 200", h.name, tt.name, rr.Code, rr.Body.String())
			}
			switch {
			case tt.renamed == "" && len(renamed) != 0:
				t.Errorf("%s() of %s called the rename hook %q, want no call", h.name, tt.name, renamed)
			case tt.renamed != "" && (len(renamed) != 1 || renamed[0] != tt.renamed):
				t.Errorf("%s() of %s called the rename hook %q, want %q", h.name, tt.name, renamed, tt.renamed)
			}
		}
	}
}

func authConfirmByLinkCase6(t *testing.T) {
	t.Parallel()
	hourAgo := time.Now().Add(-time.Hour)

	for _, h := range linkHandlers {
		// The mocked store has no expectation for VerifyUserEmail, so a call fails the
		// test; the link's consumption and the identity links are counted. The account
		// holds a confirmed address, which a read of its own would look past to the
		// identity links.
		c := newLinkCase(t, holding(strPtr("pat@lakeside.edu"), &hourAgo))
		c.links.expectConfirmation(c.f, c.userID, "pat.lee@lakeside.edu", c.row)
		token := c.mailed(t, h, "pat.lee@lakeside.edu", time.Now())
		reads := identityReadsOf(c.userID).Load()
		if rr := h.follow(c.f, c.sessionID, token); rr.Code != http.StatusOK {
			t.Fatalf("%s() = %d %s, want 200", h.name, rr.Code, rr.Body.String())
		}
		if c.links.consumes != 0 {
			t.Errorf("%s() asked the store to consume the link %d times on its own, want none: the confirmation consumes it", h.name, c.links.consumes)
		}
		if n := identityReadsOf(c.userID).Load() - reads; n != 0 {
			t.Errorf("%s() read the account's identity links %d times on its own, want none: the confirmation reads them", h.name, n)
		}
	}
}
