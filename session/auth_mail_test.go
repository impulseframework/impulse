package session

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/azure"
	"github.com/impulseframework/impulse/session/google"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/internal/mockstore"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/mock/mock_azure"
	"github.com/impulseframework/impulse/session/mock/mock_signin"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// mailLinks is the mail links table as the mocked store holds it.
type mailLinks struct {
	mu   sync.Mutex
	rows []*sessionstorage.MailLink
	// accounts are the users the store reports as holding an address (UserByEmail),
	// keyed by the address in lower case; none hold any until a test says so (holds).
	accounts map[string]*sessionstorage.SessionUser
	once     sync.Once
	// beforeRevoke, when set, is called with a revoke's context before the table is
	// touched: where a test holds guesses made at once. revokes counts the revokes
	// the store was asked for, whatever it answered.
	beforeRevoke func(ctx context.Context)
	revokes      int

	// unmailed holds the ids of the links the store was told could not be mailed.
	unmailed map[impulse.UUID]bool
}

// notMailed reports whether the store was told the link id could not be mailed.
func (m *mailLinks) notMailed(id impulse.UUID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.unmailed[id]
}

// holds makes the store report user as the account holding email.
func (m *mailLinks) holds(email string, user *sessionstorage.SessionUser) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.accounts == nil {
		m.accounts = map[string]*sessionstorage.SessionUser{}
	}
	m.accounts[strings.ToLower(email)] = user
}

// expectAccounts gives the fixture's store the users-by-address lookup the invitation
// answers make (UserByEmail), answered from accounts, NotFound for an address none
// holds; once per fixture, so a test may call it freely.
func (m *mailLinks) expectAccounts(f *authFixture) {
	m.once.Do(func() {
		f.store.EXPECT().UserByEmail(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, email string) (*sessionstorage.SessionUser, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if u := m.accounts[strings.ToLower(email)]; u != nil {
				row := *u

				return &row, nil
			}

			return nil, httpio.NewNotFoundMessage("no user holds the address")
		}).AnyTimes()
	})
}

// expectMailLinks gives the fixture's store a mail links table and returns it.
func expectMailLinks(f *authFixture) *mailLinks {
	links := &mailLinks{}
	f.store.EXPECT().InsertMailLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, l *sessionstorage.MailLink) error {
		links.mu.Lock()
		defer links.mu.Unlock()
		l.ID = impulse.Must(impulse.NewUUID())
		l.CreatedAt = time.Now()
		row := *l
		links.rows = append(links.rows, &row)

		return nil
	}).AnyTimes()
	f.store.EXPECT().MailLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, hash []byte) (*sessionstorage.MailLink, error) {
		if l := links.live(hash); l != nil {
			row := *l

			return &row, nil
		}

		return nil, httpio.NewNotFoundMessage("no live mail link")
	}).AnyTimes()
	f.store.EXPECT().ConsumeMailLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, hash []byte) error {
		links.mu.Lock()
		defer links.mu.Unlock()
		for _, l := range links.rows {
			if bytes.Equal(l.TokenHash, hash) && l.ConsumedAt == nil {
				now := time.Now()
				l.ConsumedAt = &now

				return nil
			}
		}

		return httpio.NewNotFoundMessage("no live mail link")
	}).AnyTimes()
	f.store.EXPECT().RevokeMailLink(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id impulse.UUID) error {
		if links.beforeRevoke != nil {
			links.beforeRevoke(ctx)
		}
		links.mu.Lock()
		defer links.mu.Unlock()
		links.revokes++
		for _, l := range links.rows {
			if l.ID == id && l.ConsumedAt == nil {
				now := time.Now()
				l.ConsumedAt = &now

				return nil
			}
		}

		return httpio.NewNotFoundMessage("no live mail link")
	}).AnyTimes()
	f.store.EXPECT().FailMailLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id impulse.UUID) error {
		links.mu.Lock()
		defer links.mu.Unlock()
		for _, l := range links.rows {
			if l.ID != id || l.ConsumedAt != nil {
				continue
			}
			now := time.Now()
			l.ConsumedAt = &now
			if links.unmailed == nil {
				links.unmailed = make(map[impulse.UUID]bool)
			}
			links.unmailed[id] = true

			return nil
		}

		return httpio.NewNotFoundMessage("no live mail link")
	}).AnyTimes()
	f.store.EXPECT().LiveMailLinks(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, kind sessionstorage.MailLinkKind) ([]*sessionstorage.MailLink, error) {
		links.mu.Lock()
		defer links.mu.Unlock()
		var live []*sessionstorage.MailLink
		for _, l := range links.rows {
			if l.Kind == kind && l.ConsumedAt == nil && time.Now().Before(l.ExpiresAt) {
				row := *l
				live = append(live, &row)
			}
		}

		return live, nil
	}).AnyTimes()

	return links
}

// live is the live row holding hash, or nil.
func (m *mailLinks) live(hash []byte) *sessionstorage.MailLink {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.rows {
		if bytes.Equal(l.TokenHash, hash) && l.ConsumedAt == nil && time.Now().Before(l.ExpiresAt) {
			return l
		}
	}

	return nil
}

// last is the last row inserted.
func (m *mailLinks) last(t *testing.T) *sessionstorage.MailLink {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.rows) == 0 {
		t.Fatal("no mail link was inserted")
	}

	return m.rows[len(m.rows)-1]
}

// insert adds a row by hand, as an earlier call would have.
func (m *mailLinks) insert(l *sessionstorage.MailLink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l.ID.IsNil() {
		l.ID = impulse.Must(impulse.NewUUID())
	}
	m.rows = append(m.rows, l)
}

// testMail is the fixture's mail settings, without their sender.
var testMail = MailSettings{Application: "Harbor", LoginURL: "https://harbor.test/login"}

// mailThrough is the fixture's mail settings with sender to send through.
func mailThrough(sender mail.Sender) MailSettings {
	settings := testMail
	settings.Sender = sender

	return settings
}

// quietMail is the mail of an Auth whose test reads nothing it sends: the fixture's
// settings over a recorder of its own.
func quietMail() MailSettings {
	return mailThrough(&mail.Recorder{})
}

// newMailFixture is an Auth fixture that sends mail to a recorder, with the password
// method registered under passwordOptions.
func newMailFixture(t *testing.T, ctrl *gomock.Controller, passwordOptions ...PasswordOption) (*authFixture, *mail.Recorder, *mailLinks) {
	t.Helper()

	recorder := &mail.Recorder{}
	f := newAuthFixtureOf(t, ctrl, recorder, true, passwordOptions)

	return f, recorder, expectMailLinks(f)
}

// newMailFixtureWithoutPassword is the mail fixture with no password method: the Auth a
// registration that synchronizes roles stands alone on.
func newMailFixtureWithoutPassword(t *testing.T, ctrl *gomock.Controller) (*authFixture, *mail.Recorder, *mailLinks) {
	t.Helper()

	recorder := &mail.Recorder{}
	f := newAuthFixtureOf(t, ctrl, recorder, false, nil)

	return f, recorder, expectMailLinks(f)
}

// tokenIn is the token the link in body carries under param.
func tokenIn(t *testing.T, body, param string) string {
	t.Helper()

	m := regexp.MustCompile(`https://harbor\.test/login#` + param + `=([A-Za-z0-9_-]+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no link with #%s= in the message that opens %q: a message's text holds its link and is not printed", param, strings.SplitN(body, "\n", 2)[0])
	}

	return m[1]
}

// TestMailSettings_linkTokenInTheFragment holds every kind of mailed link to carrying
// its token in the URL's fragment and nowhere else. A browser sends a fragment to no
// server, so a link a person opens writes its token to no request log; a token in the
// query or the path would be in the first request line the browser sends.
func TestMailSettings_linkTokenInTheFragment(t *testing.T) {
	t.Parallel()

	const token = "tok_9-Z"
	for _, loginURL := range []string{"https://harbor.test/login", "https://harbor.test/login?theme=dark"} {
		m := &mailSettings{settings: MailSettings{Application: "Harbor", LoginURL: loginURL}}
		for _, tt := range []struct {
			kind    string
			message mail.Message
		}{
			{kind: "invitation", message: m.invitationMessage("pat@lakeside.edu", token, time.Hour)},
			{kind: "verify", message: m.verificationMessage("pat@lakeside.edu", token, time.Hour)},
			{kind: "signUp", message: m.signUpMessage("pat@lakeside.edu", token, time.Hour)},
			{kind: "reset", message: m.passwordResetMessage("pat@lakeside.edu", token, time.Hour, AskedByPerson(), "")},
			{kind: "reset", message: m.passwordResetMessage("pat@lakeside.edu", token, time.Hour, AskedBy{Administrator: true, Tenant: ""}, "Harbor")},
			{kind: "address", message: m.addressChangeMessage("pat@lakeside.edu", token, time.Hour, "Sam")},
		} {
			var links []*url.URL
			for _, word := range strings.Fields(tt.message.Text) {
				if link, err := url.Parse(word); err == nil && link.Scheme == "https" {
					links = append(links, link)
				}
			}
			if len(links) != 1 {
				t.Errorf("the %s message under %s carries %d links, want one", tt.kind, loginURL, len(links))

				continue
			}
			link := links[0]
			if got, want := link.Fragment, tt.kind+"="+token; got != want {
				t.Errorf("the %s link's fragment = %q, want %q", tt.kind, got, want)
			}
			if strings.Contains(link.RawQuery, token) || strings.Contains(link.Path, token) || link.Query().Has(tt.kind) {
				t.Errorf("the %s link carries its token outside the fragment (path %q, query %q), want it in the fragment alone: a browser sends the rest to the server", tt.kind, link.Path, link.RawQuery)
			}
			if got, want := strings.SplitN(link.String(), "#", 2)[0], loginURL; got != want {
				t.Errorf("the %s link leads to %q, want the login page %q", tt.kind, got, want)
			}
		}
	}
}

func TestMailSettings_Messages(t *testing.T) {
	t.Parallel()

	m := &mailSettings{settings: testMail}
	for _, tt := range []struct {
		name string
		got  mail.Message
		want mail.Message
	}{
		{
			name: "an invitation: the instruction and link, how long it is good for, then what to do if it was not expected and to keep the link",
			got:  m.invitationMessage("pat@lakeside.edu", "tok_1", 7*24*time.Hour),
			want: mail.Message{To: "pat@lakeside.edu", Subject: "You are invited to Harbor", Text: `You are invited to Harbor.

To accept the invitation, open this link:
https://harbor.test/login#invitation=tok_1

The link is good for 7 days.

Didn't expect this?
If you were not expecting an invitation, ignore this message. Nothing changes.

Keep the link to yourself
Anyone who opens the link can accept the invitation in your place. Don't forward it.
`},
		},
		{
			name: "an address confirmation takes for granted the person added the address",
			got:  m.verificationMessage("pat@lakeside.edu", "tok_2", 6*time.Hour),
			want: mail.Message{To: "pat@lakeside.edu", Subject: "Confirm your email address for Harbor", Text: `You added this address to your Harbor account.

To confirm it, open this link:
https://harbor.test/login#verify=tok_2

The link is good for 6 hours.

Didn't do this?
If you did not add this address, ignore this message. Nothing changes.
`},
		},
		{
			name: "an address another account holds: no link, the account that has it, and what to do if it was not them",
			got:  m.addressHeldMessage("pat@lakeside.edu"),
			want: mail.Message{To: "pat@lakeside.edu", Subject: "Your email address and Harbor", Text: `This address already belongs to a Harbor account, and someone just tried to add it to a different one. Nothing was changed: an address can belong to one account only.

If that was you, you have two Harbor accounts, and this address can be on only one of them. Sign in to the account it is on now.
If you would rather use it on the other account, change the address on this one first, then add it there.

Didn't do this?
If you did not try to add this address, ignore this message. Nothing changes.
`},
		},
		{
			name: "a sign-up link: open it and choose a password, how long it is good for, then the two notices",
			got:  m.signUpMessage("new@lakeside.edu", "tok_3", 6*time.Hour),
			want: mail.Message{To: "new@lakeside.edu", Subject: "Finish creating your Harbor account", Text: `You asked to create a Harbor account with this address.

To finish, open this link and choose your password:
https://harbor.test/login#signUp=tok_3

The link is good for 6 hours.

Didn't do this?
If you did not ask for an account, ignore this message. Nothing changes.

Keep the link to yourself
Anyone who opens the link can create the account in your place. Don't forward it.
`},
		},
		{
			name: "a sign-in code: the code spaced for reading, how long it is good for, then what to do if no sign-in was started and to keep the code",
			got:  m.codeMessage("pat@lakeside.edu", "483920", 10*time.Minute),
			want: mail.Message{To: "pat@lakeside.edu", Subject: "Your Harbor sign-in code is 483 920", Text: `Your sign-in code for Harbor is:

483 920

Enter it on the sign-in page. The code is good for 10 minutes.

Didn't do this?
If you did not start a sign-in, ignore this message and change your password: someone else has it.

Keep the code to yourself
Nobody from Harbor will ask you for it. Don't share it.
`},
		},
		{
			name: "the code that accepts an invitation: the code spaced for reading, what it accepts, where to enter it and how long it is good for, then what it means if they did not open the invitation and that only they enter it, with no word of a password",
			got:  m.invitationCodeMessage("pat@lakeside.edu", "483920", 10*time.Minute),
			want: mail.Message{To: "pat@lakeside.edu", Subject: "Your code to accept your invitation to Harbor is 483 920", Text: `Your code to accept your invitation to Harbor is:

483 920

It accepts the invitation to Harbor that was sent to this address. Enter it on the page that asked you for it. The code is good for 10 minutes.

Didn't do this?
If you did not open your invitation yourself, someone else has its link. Don't give the code to anyone, and tell the person who invited you.

Keep the code to yourself
Only you should enter it. Nobody from Harbor will ask you for it. Don't share it.
`},
		},
		{
			name: "the code of a first password: the code spaced for reading, where to enter it and how long it is good for, then what to do if they did not ask and to keep the code",
			got:  m.firstPasswordMessage("pat@lakeside.edu", "483920", 10*time.Minute),
			want: mail.Message{To: "pat@lakeside.edu", Subject: "Your code to choose a Harbor password is 483 920", Text: `Your code to choose a password for your Harbor account is:

483 920

Enter it on your account page, with the password you want. The code is good for 10 minutes.

Didn't do this?
If you did not ask to choose a password, ignore this message: without the code nothing changes. Someone asked from a browser where you are signed in, so sign out of the browsers you do not use.

Keep the code to yourself
Nobody from Harbor will ask you for it. Don't share it.
`},
		},
		{
			name: "an account that already exists: no link, sign in instead, and what to do if it was not them",
			got:  m.accountExistsMessage("pat@lakeside.edu"),
			want: mail.Message{To: "pat@lakeside.edu", Subject: "You already have a Harbor account", Text: `You asked to create a Harbor account with this address, but there is already an account for it.

Sign in to Harbor to use it. If you forgot how, ask your administrator.

Didn't do this?
If you did not ask for an account, ignore this message. Nothing changes.
`},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, tt.got); diff != "" {
				t.Errorf("message mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGoodFor(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{d: 10 * time.Second, want: "1 minute"},
		{d: time.Minute, want: "1 minute"},
		{d: 15 * time.Minute, want: "15 minutes"},
		{d: 59*time.Minute + 20*time.Second, want: "59 minutes"},
		{d: 59*time.Minute + 40*time.Second, want: "59 minutes"},
		{d: time.Hour, want: "1 hour"},
		{d: 89 * time.Minute, want: "1 hour"},
		{d: 90 * time.Minute, want: "1 hour"},
		{d: 6 * time.Hour, want: "6 hours"},
		{d: 24 * time.Hour, want: "24 hours"},
		{d: 47 * time.Hour, want: "47 hours"},
		{d: 47*time.Hour + 40*time.Minute, want: "47 hours"},
		{d: 48 * time.Hour, want: "2 days"},
		{d: 7 * 24 * time.Hour, want: "7 days"},
		{d: 7*24*time.Hour + 11*time.Hour, want: "7 days"},
		{d: 7*24*time.Hour + 13*time.Hour, want: "7 days"},
	} {
		t.Run(tt.d.String()+" reads as "+tt.want, func(t *testing.T) {
			t.Parallel()
			if got := goodFor(tt.d); got != tt.want {
				t.Errorf("goodFor(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// decoded is the JSON object a response carries.
func decoded(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", rr.Body.String(), err)
	}

	return body
}

// signedIn is a request of the session sessionID, carrying cookies, served by h
// straight: the handler validates the session itself, and the invitation cookie is
// read as StartSession reads it.
func (f *authFixture) asUser(h http.Handler, sessionID impulse.UUID, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
	payload := strings.NewReader("")
	if body != nil {
		raw, _ := json.Marshal(body)
		payload = strings.NewReader(string(raw))
	}
	r := httptest.NewRequestWithContext(context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID), http.MethodPost, "/", payload)
	r.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	r = r.WithContext(f.auth.withInvitationCookie(r.Context(), r))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)

	return rr
}

// sessionAtEachRead answers a read of sessionID with a session of user that was active at the
// moment of the read, established by events, oldest first. A test that hands the store
// one session for several reads answers them with it: a session built once, when the
// test was set up, has gone idle past the interval the activity update is held to (five
// seconds) by the time a slow machine reads it, and the update is a call no test here
// expects.
func sessionAtEachRead(sessionID impulse.UUID, user *sessionstorage.SessionUser, events ...sessioninfo.AuthEvent) func(context.Context, impulse.UUID) (*sessioninfo.SessionData, error) {
	return func(context.Context, impulse.UUID) (*sessioninfo.SessionData, error) {
		return &sessioninfo.SessionData{
			SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: user.Username, UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(user.ID), AuthEvents: events,
		}, nil
	}
}

// expectSession makes sessionID a live session of user for the store.
func (f *authFixture) expectSession(sessionID impulse.UUID, user *sessionstorage.SessionUser) {
	f.store.EXPECT().Session(gomock.Any(), sessionID).DoAndReturn(sessionAtEachRead(sessionID, user)).AnyTimes()
	f.store.EXPECT().User(gomock.Any(), user.ID).Return(user, nil).AnyTimes()
}

func TestAuth_Invite(t *testing.T) {
	t.Parallel()

	t.Run("an invitation stores its row and mails a link to the login page that carries the token", authInviteCase1)
	t.Run("a message that cannot be sent withdraws the invitation", authInviteCase2)
	t.Run("an address that is not one is refused", authInviteCase3)
	t.Run("an address no offered sign-in method could admit is refused, nothing stored or mailed", authInviteCase4)
}

func authInviteCase1(t *testing.T) {
	t.Parallel()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	inviter := impulse.Must(impulse.NewUUID())

	inv, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "Pat Lee <pat@lakeside.edu>", TenantID: "lakeside", RoleNames: []string{"Teacher"}, InvitedByUserID: impulse.NullUUIDFromUUID(inviter)})
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	row := links.last(t)
	if row.Kind != sessionstorage.MailLinkInvitation || row.Email != "pat@lakeside.edu" || row.TenantID != "lakeside" || row.RoleNames[0] != "Teacher" || row.InvitedByUserID.UUID != inviter {
		t.Errorf("stored link = %+v, want the invitation as requested, the address bare", row)
	}
	if inv.ID != row.ID || inv.Email != row.Email || inv.TenantID != "lakeside" || !inv.ExpiresAt.After(time.Now().Add(6*24*time.Hour)) {
		t.Errorf("Invite() = %+v, want the stored invitation standing seven days", inv)
	}
	msgs := recorder.Messages()
	if len(msgs) != 1 || msgs[0].To != "pat@lakeside.edu" || msgs[0].Subject != "You are invited to Harbor" {
		t.Fatalf("messages = %+v, want one invitation to pat", msgs)
	}
	if hash, ok := linkTokenHash(tokenIn(t, msgs[0].Text, "invitation")); !ok || !bytes.Equal(hash, row.TokenHash) {
		t.Error("the mailed token does not hash to the stored row's token hash")
	}
	if live, err := f.auth.API().Invitations(context.Background()); err != nil || len(live) != 1 || live[0].ID != inv.ID {
		t.Errorf("Invitations() = %v, %v; want the one standing", live, err)
	}
	if err := f.auth.API().RevokeInvitation(context.Background(), inv.ID); err != nil {
		t.Fatalf("RevokeInvitation() error = %v", err)
	}
	if live, err := f.auth.API().Invitations(context.Background()); err != nil || len(live) != 0 {
		t.Errorf("Invitations() after the revocation = %v, %v; want none", live, err)
	}
}

func authInviteCase2(t *testing.T) {
	t.Parallel()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	recorder.Err = errors.New("relay down")

	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu"}); err == nil || !strings.Contains(err.Error(), "could not be sent") {
		t.Fatalf("Invite() error = %v, want the message not sent", err)
	}
	if row := links.last(t); row.ConsumedAt == nil {
		t.Error("the invitation stands after the send failed, want it withdrawn")
	}
}

func authInviteCase3(t *testing.T) {
	t.Parallel()
	f, _, _ := newMailFixture(t, gomock.NewController(t))
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat at lakeside"}); !httpio.HasBadRequest(err) {
		t.Errorf("Invite() of a bad address error = %v, want BadRequest", err)
	}
}

func authInviteCase4(t *testing.T) {
	t.Parallel()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	google.SignIn(f.auth, "google", resolveAll[google.Identity](), &google.Config{RoleSync: google.DisableRoleSync(), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/google/callback", Workspaces: google.Workspace("lakeside", "lakeside.edu")})
	// The page offers Google alone: the password method is registered for operators, not listed.
	f.auth.Methods(&MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "Google", Path: "/api/user/google/login"}}})

	_, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "Pat Lee <Pat@Elsewhere.Example>", TenantID: "lakeside"})
	if want := "No sign-in method of this application accepts an address at elsewhere.example."; !httpio.HasBadRequest(err) || httpio.Message(err) != want {
		t.Errorf("Invite() error = %v, want BadRequest %q", err, want)
	}
	if len(links.rows) != 0 || len(recorder.Messages()) != 0 {
		t.Errorf("rows = %d, messages = %d after the refusal; want nothing stored or mailed", len(links.rows), len(recorder.Messages()))
	}

	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: "lakeside"}); err != nil {
		t.Fatalf("Invite() of an address Google admits error = %v", err)
	}
	if len(links.rows) != 1 || len(recorder.Messages()) != 1 {
		t.Errorf("rows = %d, messages = %d; want the invitation stored and mailed", len(links.rows), len(recorder.Messages()))
	}
}

// TestAuth_PrepareInvitation pins the invitation a caller stores itself: the checks
// Invite makes, the row to store with the hash of the token the message carries, and
// nothing stored or mailed until the caller does the one and asks for the other; a
// message that cannot be sent withdraws the row the caller stored.
func TestAuth_PrepareInvitation(t *testing.T) {
	t.Parallel()

	t.Run("the row is the caller's to store, and Send mails the link whose token it hashes", func(t *testing.T) {
		t.Parallel()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		inviter := impulse.Must(impulse.NewUUID())

		prepared, err := f.auth.API().PrepareInvitation(context.Background(), &InvitationRequest{Email: "Pat Lee <pat@lakeside.edu>", TenantID: "lakeside", RoleNames: []string{"Teacher"}, InvitedByUserID: impulse.NullUUIDFromUUID(inviter)})
		if err != nil {
			t.Fatalf("PrepareInvitation() error = %v", err)
		}
		if len(links.rows) != 0 || len(recorder.Messages()) != 0 {
			t.Fatalf("rows = %d, messages = %d after the preparation; want nothing stored or mailed", len(links.rows), len(recorder.Messages()))
		}
		row := prepared.Row()
		if row.ID.IsNil() || row.Email != "pat@lakeside.edu" || row.TenantID != "lakeside" || len(row.RoleNames) != 1 || row.RoleNames[0] != "Teacher" || row.InvitedByUserID.UUID != inviter || !row.ExpiresAt.After(time.Now().Add(6*24*time.Hour)) {
			t.Errorf("Row() = %+v, want the invitation as requested, the address bare, standing seven days", row)
		}
		// The caller stores the row; the fixture's table stands for its transaction.
		links.insert(&sessionstorage.MailLink{ID: row.ID, TokenHash: row.TokenHash, Kind: sessionstorage.MailLinkInvitation, Email: row.Email, TenantID: row.TenantID, RoleNames: row.RoleNames, InvitedByUserID: row.InvitedByUserID, ExpiresAt: row.ExpiresAt})
		if err := prepared.Send(context.Background()); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		msgs := recorder.Messages()
		if len(msgs) != 1 || msgs[0].To != "pat@lakeside.edu" || msgs[0].Subject != "You are invited to Harbor" {
			t.Fatalf("messages = %+v, want one invitation to pat", msgs)
		}
		if hash, ok := linkTokenHash(tokenIn(t, msgs[0].Text, "invitation")); !ok || !bytes.Equal(hash, row.TokenHash) {
			t.Error("the mailed token does not hash to the row's token hash")
		}
		if live, err := f.auth.API().Invitations(context.Background()); err != nil || len(live) != 1 || live[0].ID != row.ID {
			t.Errorf("Invitations() = %v, %v; want the one the caller stored", live, err)
		}
		// The row answered is a copy: changing it does not change the invitation.
		row.RoleNames[0] = "Principal"
		if again := prepared.Row(); again.RoleNames[0] != "Teacher" {
			t.Errorf("Row() after the caller changed its copy = %+v, want the invitation unchanged", again)
		}
	})

	t.Run("a message that cannot be sent withdraws the row the caller stored", func(t *testing.T) {
		t.Parallel()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		prepared, err := f.auth.API().PrepareInvitation(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", RoleNames: []string{"Teacher"}})
		if err != nil {
			t.Fatalf("PrepareInvitation() error = %v", err)
		}
		row := prepared.Row()
		links.insert(&sessionstorage.MailLink{ID: row.ID, TokenHash: row.TokenHash, Kind: sessionstorage.MailLinkInvitation, Email: row.Email, RoleNames: row.RoleNames, ExpiresAt: row.ExpiresAt})
		recorder.Fail(errors.New("relay down"))

		if err := prepared.Send(context.Background()); err == nil || !strings.Contains(err.Error(), "could not be sent") {
			t.Fatalf("Send() error = %v, want the message not sent", err)
		}
		if stored := links.last(t); stored.ConsumedAt == nil {
			t.Error("the invitation stands after the send failed, want it withdrawn")
		}
	})

	t.Run("an address that is not one, and one no method could admit, are refused", func(t *testing.T) {
		t.Parallel()
		f, recorder, _ := newMailFixture(t, gomock.NewController(t))
		if _, err := f.auth.API().PrepareInvitation(context.Background(), &InvitationRequest{Email: "pat at lakeside"}); !httpio.HasBadRequest(err) {
			t.Errorf("PrepareInvitation() of a bad address error = %v, want BadRequest", err)
		}
		google.SignIn(f.auth, "google", resolveAll[google.Identity](), &google.Config{RoleSync: google.DisableRoleSync(), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/google/callback", Workspaces: google.Workspace("lakeside", "lakeside.edu")})
		f.auth.Methods(&MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "Google", Path: "/api/user/google/login"}}})
		_, err := f.auth.API().PrepareInvitation(context.Background(), &InvitationRequest{Email: "pat@elsewhere.example"})
		if want := "No sign-in method of this application accepts an address at elsewhere.example."; !httpio.HasBadRequest(err) || httpio.Message(err) != want {
			t.Errorf("PrepareInvitation() error = %v, want BadRequest %q", err, want)
		}
		if len(recorder.Messages()) != 0 {
			t.Errorf("messages = %d after the refusals, want none", len(recorder.Messages()))
		}
	})
}

// invitationCookies opens the invitation link.last for the browser and returns the
// cookies it set.
func invitationCookies(t *testing.T, f *authFixture, links *mailLinks, token string) []*http.Cookie {
	links.expectAccounts(f)
	t.Helper()

	rr := f.serve(f.auth.Invitation().Open(), http.MethodPost, "/invitation", map[string]string{"token": token}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("Open() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	if cookieNamed(rr, defaultInvitationCookieName) == nil {
		t.Fatal("Open() set no invitation cookie")
	}
	_ = links

	return rr.Result().Cookies()
}

func TestAuth_Invitation(t *testing.T) {
	t.Parallel()

	t.Run("opening an invitation answers what it is and sets the cookie; a dead or foreign token is 404", authInvitationCase1)
	t.Run("a signed-in person accepts the invitation the browser carries, in the store's transaction with the hook, and the cookie goes", authInvitationCase2)
	t.Run("a sign-up from an invitation refuses a short password, a missing username and a missing invitation", authInvitationCase4)
	t.Run("opening an invitation names the listed registrations that could admit its address, none as an empty list", authInvitationCase6)
	t.Run("opening an invitation names its tenant by the application's namer, and by nothing for a tenant the namer does not know", authInvitationCase7)
	t.Run("an invitation names every registration that could admit its address, and for an account with no password that signs in through an identity, only the registrations it signs in through", authInvitationCase8)
}

// namedTenants is a TenantNamer over a fixed map, as the application's tenant roster is
// one over its copy.
type namedTenants map[accesstypes.Domain]string

func (n namedTenants) Name(tenant accesstypes.Domain) string {
	return n[tenant]
}

func authInvitationCase7(t *testing.T) {
	t.Parallel()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	links.expectAccounts(f)
	f.auth.SetTenantNames(namedTenants{"7b0c2f1e-9a4d-4c3b-8e51-2d6f0a9b4c17": "Lakeside High"})
	opened := func(tenantID string) map[string]any {
		t.Helper()
		if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: tenantID, RoleNames: []string{"Teacher"}}); err != nil {
			t.Fatal(err)
		}
		messages := recorder.Messages()
		token := tokenIn(t, messages[len(messages)-1].Text, "invitation")
		rr := f.serve(f.auth.Invitation().Open(), http.MethodPost, "/invitation", map[string]string{"token": token}, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("Open() = %d %s, want 200", rr.Code, rr.Body.String())
		}

		return decoded(t, rr)
	}

	// The person reading the screen is not signed in and can read no tenant record: the
	// answer carries the name beside the key, and the screen shows the name.
	if body := opened("7b0c2f1e-9a4d-4c3b-8e51-2d6f0a9b4c17"); body["tenantId"] != "7b0c2f1e-9a4d-4c3b-8e51-2d6f0a9b4c17" || body["tenantName"] != "Lakeside High" {
		t.Errorf("Open() = %v, want the tenant's id and its name Lakeside High", body)
	}
	// A tenant the namer does not know is named by nothing; its key is still the key.
	if body := opened("0e9d7c6b-5a4f-4e3d-9c2b-1a0f9e8d7c6b"); body["tenantId"] != "0e9d7c6b-5a4f-4e3d-9c2b-1a0f9e8d7c6b" || body["tenantName"] != nil {
		t.Errorf("Open() = %v, want the tenant's id and no name", body)
	}
	// An invitation to the application's global surface names no tenant at all.
	if body := opened(""); body["tenantId"] != nil || body["tenantName"] != nil {
		t.Errorf("Open() = %v, want no tenant", body)
	}
}

func TestAuth_InvitationKeptOnRefusedSignIn(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	// The directory writes the roles, so its registration stands alone on the Auth.
	f, recorder, links := newMailFixtureWithoutPassword(t, ctrl)
	hookCalls := 0
	f.auth.SetInvitationAccepted(func(context.Context, mockTx, impulse.UUID, string, *sessioninfo.Invitation) (func(context.Context), error) {
		hookCalls++

		return nil, nil
	})
	// The directory asserts a role the application does not know: the sign-in is
	// refused with no_roles. The mocked store expects no SignIn: nothing is written.
	roles := mock_signin.NewMockUserRoleManager(ctrl)
	roles.EXPECT().ResolveRole(gomock.Any(), gomock.Any(), "Nobody").Return(accesstypes.Role(""), false, nil).AnyTimes()
	authn := mock_azure.NewMockAuthenticator(ctrl)
	authn.EXPECT().LoginURL().Return("/login").AnyTimes()
	authn.EXPECT().Verify(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ http.ResponseWriter, _ *http.Request, claims any) (string, string, error) {
			raw := `{"iss":"` + entraIssuer(tenantLake) + `","tid":"` + tenantLake + `","oid":"oid-pat","preferred_username":"pat@lakeside.edu","email":"pat@lakeside.edu","roles":["Nobody"]}`
			if err := json.Unmarshal([]byte(raw), claims); err != nil {
				t.Fatal(err)
			}

			return "/next", "sid", nil
		})
	entra := azure.SignIn(f.auth, "azure", resolveAll[azure.Identity](), &azure.Config{RoleSync: signin.RoleSync(roles), Authority: testAuthority, Tenants: azure.Tenant(tenantLake), Authenticator: authn})
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: "lakeside", RoleNames: []string{"Teacher"}}); err != nil {
		t.Fatal(err)
	}
	cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))

	rr := f.serve(entra.Callback(), http.MethodGet, "/azure/callback?code=c&state=s", nil, cookies)

	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/login?code=no_roles" {
		t.Errorf("callback = %d %q, want 302 /login?code=no_roles", rr.Code, rr.Header().Get("Location"))
	}
	if c := cookieNamed(rr, defaultInvitationCookieName); c != nil {
		t.Errorf("the refused sign-in wrote the invitation cookie %+v, want it left alone", c)
	}
	if hookCalls != 0 {
		t.Errorf("the invitation hook ran %d times for a refused sign-in, want never", hookCalls)
	}
	if live, err := f.auth.API().Invitations(context.Background()); err != nil || len(live) != 1 {
		t.Errorf("Invitations() after the refused sign-in = %v, %v; want the one still standing", live, err)
	}
	// The listing names the Auth's one method, the directory's registration.
	directoryList := &MethodList{Methods: []AdvertisedMethod{{Name: "azure", Label: "Lakeside", Path: "/api/user/azure/login"}}, Invitation: InvitationRoutes{Open: "/api/user/invitation", Accept: "/api/user/invitation/accept"}}
	standing, _ := invitationBlock(t, f, directoryList, cookies)["standing"].(map[string]any)
	if standing["email"] != "pat@lakeside.edu" {
		t.Errorf("the methods listing after the refused sign-in reports standing = %v, want pat's invitation", standing)
	}
}

// directoryList is the login page of a directoryFixture: two Google Workspaces, then
// the password method, with the invitation routes.
var directoryList = &MethodList{
	Methods: []AdvertisedMethod{
		{Name: "google-riverside", Label: "Riverside", Path: "/api/user/google-riverside/login"},
		{Name: "google-lakeside", Label: "Lakeside", Path: "/api/user/google-lakeside/login"},
		{Name: "password", Label: "Password", Path: "/api/user/password/login"},
	},
	ChangePassword: "/api/user/password",
	Invitation:     InvitationRoutes{Open: "/api/user/invitation", Accept: "/api/user/invitation/accept", SignUp: "/api/user/invitation/sign-up"},
}

// newDirectoryFixture is a mail fixture whose login page lists two Google
// registrations, one Workspace each, beside the password method, with an Entra
// registration that is registered but not listed.
func newDirectoryFixture(t *testing.T) (*authFixture, *mail.Recorder, *mailLinks) {
	t.Helper()

	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	for _, w := range []struct{ name, domain string }{{"lakeside", "lakeside.edu"}, {"riverside", "riverside.edu"}} {
		google.SignIn(f.auth, "google-"+w.name, resolveAll[google.Identity](), &google.Config{RoleSync: google.DisableRoleSync(), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/" + w.name + "/callback", Workspaces: google.Workspace(w.name, w.domain)})
	}
	azure.SignIn(f.auth, "lakeside-entra", resolveAll[azure.Identity](), &azure.Config{RoleSync: signin.DisableRoleSync(), Authority: testAuthority, Tenants: azure.Tenant(tenantLake), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/entra/callback"})
	f.auth.Methods(directoryList)

	return f, recorder, links
}

func authInvitationCase6(t *testing.T) {
	t.Parallel()
	f, recorder, links := newDirectoryFixture(t)
	links.expectAccounts(f)
	for i, tt := range []struct {
		email string
		want  []any
	}{
		{email: "pat@lakeside.edu", want: []any{"google-lakeside"}},
		{email: "pat@elsewhere.example", want: []any{}},
	} {
		if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: tt.email}); err != nil {
			t.Fatalf("Invite(%s) error = %v", tt.email, err)
		}
		rr := f.serve(f.auth.Invitation().Open(), http.MethodPost, "/invitation", map[string]string{"token": tokenIn(t, recorder.Messages()[i].Text, "invitation")}, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("Open() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		if diff := cmp.Diff(tt.want, decoded(t, rr)["methods"]); diff != "" {
			t.Errorf("Open() for %s: methods mismatch (-want +got):\n%s", tt.email, diff)
		}
	}
}

// authInvitationCase8 holds the methods an invitation names to what the sign-in rules
// accept for the invited address. Two registrations could admit pat@lakeside.edu here,
// Lakeside's Google Workspace and its Entra tenant. Both are named while no account
// holds the address, for an account with a password (the password confirms a new
// identity's link) and for an account nobody signs in to yet (its first identity is
// linked by the invitation). An account with no password that signs in through an
// identity is linked no identity of another connection, so only the registrations it
// signs in through are named, and none when its identity is of a registration that does
// not admit the address. Open() and the methods answer's invitation block say the same.
func authInvitationCase8(t *testing.T) {
	t.Parallel()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	for _, w := range []struct{ name, domain string }{{"lakeside", "lakeside.edu"}, {"riverside", "riverside.edu"}} {
		google.SignIn(f.auth, "google-"+w.name, resolveAll[google.Identity](), &google.Config{RoleSync: google.DisableRoleSync(), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/" + w.name + "/callback", Workspaces: google.Workspace(w.name, w.domain)})
	}
	azure.SignIn(f.auth, "lakeside-entra", resolveAll[azure.Identity](), &azure.Config{RoleSync: signin.DisableRoleSync(), Authority: testAuthority, Tenants: azure.Tenant(tenantLake), ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app/entra/callback"})
	list := &MethodList{
		Methods: []AdvertisedMethod{
			{Name: "google-riverside", Label: "Riverside", Path: "/api/user/google-riverside/login"},
			{Name: "google-lakeside", Label: "Lakeside", Path: "/api/user/google-lakeside/login"},
			{Name: "lakeside-entra", Label: "Lakeside staff", Path: "/api/user/lakeside-entra/login"},
			{Name: "password", Label: "Password", Path: "/api/user/password/login"},
		},
		ChangePassword: "/api/user/password",
		Invitation:     InvitationRoutes{Open: "/api/user/invitation", Accept: "/api/user/invitation/accept", SignUp: "/api/user/invitation/sign-up"},
	}
	f.auth.Methods(list)
	links.expectAccounts(f)
	const invited = "pat@lakeside.edu"
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: invited}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	token := tokenIn(t, recorder.Messages()[0].Text, "invitation")
	cookies := invitationCookies(t, f, links, token)

	both := []any{"google-lakeside", "lakeside-entra"}
	through := func(method sessioninfo.AuthMethod, connection string) []*sessionstorage.SessionIdentity {
		return []*sessionstorage.SessionIdentity{{ID: impulse.Must(impulse.NewUUID()), Method: method, Connection: connection, Subject: "subject"}}
	}
	for _, tt := range []struct {
		name     string
		account  bool
		password bool
		links    []*sessionstorage.SessionIdentity
		want     []any
	}{
		{name: "no account holds the address", want: both},
		{name: "an account with a password that signs in through the Entra tenant", account: true, password: true, links: through(sessioninfo.MethodAzure, tenantLake), want: both},
		{name: "an account with no password that nobody signs in to yet", account: true, want: both},
		{name: "an account with no password that signs in through the Entra tenant", account: true, links: through(sessioninfo.MethodAzure, tenantLake), want: []any{"lakeside-entra"}},
		{name: "an account with no password that signs in through the Workspace", account: true, links: through(sessioninfo.MethodGoogle, "lakeside"), want: []any{"google-lakeside"}},
		{name: "an account with no password that signs in through another Workspace, which does not admit the address", account: true, links: through(sessioninfo.MethodGoogle, "riverside"), want: []any{}},
	} {
		if tt.account {
			user := &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "pat"}
			if tt.password {
				user.PasswordHash = hashed(t, "pw")
			}
			links.holds(invited, user)
			linkIdentities(t, user.ID, tt.links...)
		}
		rr := f.serve(f.auth.Invitation().Open(), http.MethodPost, "/invitation", map[string]string{"token": token}, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: Open() = %d, want 200", tt.name, rr.Code)
		}
		if diff := cmp.Diff(tt.want, decoded(t, rr)["methods"]); diff != "" {
			t.Errorf("%s: Open() methods mismatch (-want +got):\n%s", tt.name, diff)
		}
		standing, _ := invitationBlock(t, f, list, cookies)["standing"].(map[string]any)
		if diff := cmp.Diff(tt.want, standing["methods"]); diff != "" {
			t.Errorf("%s: the methods answer's standing invitation: methods mismatch (-want +got):\n%s", tt.name, diff)
		}
	}
}

func authInvitationCase1(t *testing.T) {
	t.Parallel()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	links.expectAccounts(f)
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: "lakeside", RoleNames: []string{"Teacher"}}); err != nil {
		t.Fatal(err)
	}
	token := tokenIn(t, recorder.Messages()[0].Text, "invitation")

	rr := f.serve(f.auth.Invitation().Open(), http.MethodPost, "/invitation", map[string]string{"token": token}, nil)
	body := decoded(t, rr)
	if rr.Code != http.StatusOK || body["email"] != "pat@lakeside.edu" || body["tenantId"] != "lakeside" || body["signUp"] != true || body["usernameIsEmail"] != false {
		t.Errorf("Open() = %d %s, want the invitation with sign-up offered", rr.Code, rr.Body.String())
	}
	if body["accountExists"] != false || body["hasPassword"] != false {
		t.Errorf("Open() = %s, want accountExists false and hasPassword false while no account holds the address", rr.Body.String())
	}
	links.holds("pat@lakeside.edu", &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "pat", PasswordHash: hashed(t, "pw")})
	if body := decoded(t, f.serve(f.auth.Invitation().Open(), http.MethodPost, "/invitation", map[string]string{"token": token}, nil)); body["accountExists"] != true || body["hasPassword"] != true {
		t.Errorf("Open() = %v, want accountExists true and hasPassword true once an account with a password holds the address", body)
	}
	links.holds("pat@lakeside.edu", &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "pat"})
	if body := decoded(t, f.serve(f.auth.Invitation().Open(), http.MethodPost, "/invitation", map[string]string{"token": token}, nil)); body["accountExists"] != true || body["hasPassword"] != false {
		t.Errorf("Open() = %v, want accountExists true and hasPassword false for an account without a password", body)
	}
	if c := cookieNamed(rr, defaultInvitationCookieName); c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || !c.Expires.After(time.Now().Add(6*24*time.Hour)) {
		t.Errorf("invitation cookie = %+v, want HttpOnly, Lax and persistent", c)
	}
	if rr := f.serve(f.auth.Invitation().Open(), http.MethodPost, "/invitation", map[string]string{"token": "not-a-token"}, nil); rr.Code != http.StatusNotFound {
		t.Errorf("Open() of a bad token = %d, want 404", rr.Code)
	}
	if err := f.auth.API().RevokeInvitation(context.Background(), links.rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if rr := f.serve(f.auth.Invitation().Open(), http.MethodPost, "/invitation", map[string]string{"token": token}, nil); rr.Code != http.StatusNotFound {
		t.Errorf("Open() of a revoked invitation = %d, want 404", rr.Code)
	}
}

func authInvitationCase2(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.expectSession(sessionID, &sessionstorage.SessionUser{ID: userID, Username: "pat"})
	hookCalls := 0
	f.auth.SetInvitationAccepted(func(_ context.Context, _ mockTx, _ impulse.UUID, username string, _ *sessioninfo.Invitation) (func(context.Context), error) {
		if username != "pat" {
			t.Errorf("hook given username %q, want pat", username)
		}
		hookCalls++

		return nil, nil
	})
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", RoleNames: []string{"Admin"}}); err != nil {
		t.Fatal(err)
	}
	cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))
	f.store.EXPECT().AcceptInvitation(gomock.Any(), userID, "pat", "", gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, id impulse.UUID, username, _ string, inv *sessioninfo.Invitation, accept signinflow.InvitationAcceptor[mockTx]) error {
			if inv.ID != links.rows[0].ID || inv.RoleNames[0] != "Admin" {
				t.Errorf("AcceptInvitation() given %+v, want the opened invitation", inv)
			}
			if accept == nil {
				t.Error("AcceptInvitation() given no hook, want the Auth's")
			} else if _, err := accept(ctx, mockTx{}, id, username, inv); err != nil {
				t.Errorf("hook error = %v", err)
			}
			inv.Accepted = true

			return nil
		})

	rr := f.asUser(f.auth.Invitation().Accept(), sessionID, nil, cookies)
	if rr.Code != http.StatusOK || decoded(t, rr)["accepted"] != true || hookCalls != 1 {
		t.Errorf("Accept() = %d %s (hook calls %d), want accepted through the hook", rr.Code, rr.Body.String(), hookCalls)
	}
	if c := cookieNamed(rr, defaultInvitationCookieName); c == nil || c.Value != "" {
		t.Error("Accept() left the invitation cookie, want it deleted")
	}
	if rr := f.asUser(f.auth.Invitation().Accept(), sessionID, nil, nil); rr.Code != http.StatusNotFound {
		t.Errorf("Accept() without an invitation = %d, want 404", rr.Code)
	}

	// The store refuses the acceptance for another address: 403 naming the invited
	// address, the invitation standing and the browser still carrying it.
	f.store.EXPECT().AcceptInvitation(gomock.Any(), userID, "pat", "", gomock.Any(), gomock.Any()).Return(
		signinflow.Refusal("", sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress, "This invitation is for pat@lakeside.edu. Sign in with that account."))
	rr = f.asUser(f.auth.Invitation().Accept(), sessionID, nil, cookies)
	if rr.Code != http.StatusForbidden || decoded(t, rr)["message"] != "This invitation is for pat@lakeside.edu. Sign in with that account." {
		t.Errorf("Accept() for another address = %d %s, want 403 naming the invited address", rr.Code, rr.Body.String())
	}
	if c := cookieNamed(rr, defaultInvitationCookieName); c != nil && c.Value == "" {
		t.Error("Accept() for another address deleted the invitation cookie, want it kept")
	}

	// The hook failed in the store's transaction, with an error of its own that is a
	// NotFound (a role that no longer exists): the invitation stands, so the browser
	// keeps carrying it, and the answer is the hook's.
	f.store.EXPECT().AcceptInvitation(gomock.Any(), userID, "pat", "", gomock.Any(), gomock.Any()).Return(
		errors.Wrap(httpio.NewNotFoundMessage("role \"Admin\" is not a valid role"), "InvitationAcceptor()"))
	rr = f.asUser(f.auth.Invitation().Accept(), sessionID, nil, cookies)
	if rr.Code != http.StatusNotFound || decoded(t, rr)["message"] != "role \"Admin\" is not a valid role" {
		t.Errorf("Accept() with a failing hook = %d %s, want the hook's own answer", rr.Code, rr.Body.String())
	}
	if c := cookieNamed(rr, defaultInvitationCookieName); c != nil && c.Value == "" {
		t.Error("Accept() with a failing hook deleted the invitation cookie, want it kept: the invitation stands")
	}

	// The invitation died meanwhile: 404, and the cookie goes.
	f.store.EXPECT().AcceptInvitation(gomock.Any(), userID, "pat", "", gomock.Any(), gomock.Any()).Return(
		httpio.NewNotFoundMessageWithError(signin.ErrInvitationExpired, signinflow.InvitationEnded))
	rr = f.asUser(f.auth.Invitation().Accept(), sessionID, nil, cookies)
	if rr.Code != http.StatusNotFound {
		t.Errorf("Accept() of an invitation that died = %d, want 404", rr.Code)
	}
	if c := cookieNamed(rr, defaultInvitationCookieName); c == nil || c.Value != "" {
		t.Error("Accept() of an invitation that died left the invitation cookie, want it deleted")
	}
}

func authInvitationCase4(t *testing.T) {
	t.Parallel()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu"}); err != nil {
		t.Fatal(err)
	}
	cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))
	for _, tt := range []struct {
		name    string
		body    map[string]string
		cookies []*http.Cookie
		want    int
	}{
		{name: "short password", body: map[string]string{"password": "short", "username": "patl"}, cookies: cookies, want: http.StatusBadRequest},
		{name: "no username", body: map[string]string{"password": "correct horse"}, cookies: cookies, want: http.StatusBadRequest},
		{name: "no invitation", body: map[string]string{"password": "correct horse", "username": "patl"}, want: http.StatusNotFound},
	} {
		if rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", tt.body, tt.cookies); rr.Code != tt.want {
			t.Errorf("SignUp() with %s = %d %s, want %d", tt.name, rr.Code, rr.Body.String(), tt.want)
		}
	}
}

func TestAuth_Email(t *testing.T) {
	t.Parallel()

	t.Run("adding an address mails it a verification link, and following the link writes the address verified and resets the address's mail count", authEmailCase1)
	t.Run("a link counts only for the account that asked", authEmailCase2)
	t.Run("an address another account holds is told so by mail, the screen says nothing; a mailbox is sent five mails it does not use, then refused", authEmailCase3)
	t.Run("where the username is the address, a verified new address renames the user and the rename hook moves what the application keys by it", authEmailCase5)
}

func authEmailCase1(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.expectSession(sessionID, pat(userID))
	f.store.EXPECT().UserByEmail(gomock.Any(), "Pat@Lakeside.edu").Return(nil, httpio.NewNotFoundMessage("none"))

	rr := f.asUser(f.auth.Email().Add(), sessionID, map[string]string{"email": "Pat@Lakeside.edu"}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("Add() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	row := links.last(t)
	if row.Kind != sessionstorage.MailLinkVerification || row.UserID.UUID != userID || row.Email != "Pat@Lakeside.edu" {
		t.Errorf("stored link = %+v, want a verification of the address for pat", row)
	}
	msgs := recorder.Messages()
	if len(msgs) != 1 || msgs[0].Subject != "Confirm your email address for Harbor" || msgs[0].To != "Pat@Lakeside.edu" {
		t.Fatalf("messages = %+v, want the verification", msgs)
	}
	token := tokenIn(t, msgs[0].Text, "verify")
	if record := f.attemptsRecord("MAIL:pat@lakeside.edu"); record == nil || record.Failures != 1 {
		t.Errorf("the address's mail count after Add() = %+v, want one", record)
	}

	f.store.EXPECT().VerifyUserEmail(gomock.Any(), userID, "Pat@Lakeside.edu").Return(nil)
	rr = f.asUser(f.auth.Email().Verify(), sessionID, map[string]string{"token": token}, nil)
	if rr.Code != http.StatusOK || decoded(t, rr)["email"] != "Pat@Lakeside.edu" {
		t.Errorf("Verify() = %d %s, want the address", rr.Code, rr.Body.String())
	}
	if record := f.attemptsRecord("MAIL:pat@lakeside.edu"); record != nil {
		t.Errorf("the address's mail count after the link was followed = %+v, want none", record)
	}
	if rr := f.asUser(f.auth.Email().Verify(), sessionID, map[string]string{"token": token}, nil); rr.Code != http.StatusNotFound {
		t.Errorf("second Verify() = %d, want 404: the link was used", rr.Code)
	}
}

func authEmailCase2(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, _, links := newMailFixture(t, gomock.NewController(t))
	f.expectSession(sessionID, pat(userID))
	other := impulse.Must(impulse.NewUUID())
	token, hash, err := linkToken()
	if err != nil {
		t.Fatal(err)
	}
	links.insert(&sessionstorage.MailLink{TokenHash: hash, Kind: sessionstorage.MailLinkVerification, Email: "x@lakeside.edu", UserID: impulse.NullUUIDFromUUID(other), ExpiresAt: time.Now().Add(time.Hour)})

	if rr := f.asUser(f.auth.Email().Verify(), sessionID, map[string]string{"token": token}, nil); rr.Code != http.StatusNotFound {
		t.Errorf("Verify() of another account's link = %d, want 404", rr.Code)
	}
	if row := links.rows[0]; row.ConsumedAt != nil {
		t.Error("the other account's link was consumed, want it untouched")
	}
}

func authEmailCase3(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.expectSession(sessionID, pat(userID))
	other := impulse.Must(impulse.NewUUID())
	f.store.EXPECT().UserByEmail(gomock.Any(), "taken@lakeside.edu").Return(&sessionstorage.SessionUser{ID: other, Username: "sam", Email: strPtr("taken@lakeside.edu")}, nil).AnyTimes()

	for i := range 5 {
		if rr := f.asUser(f.auth.Email().Add(), sessionID, map[string]string{"email": "taken@lakeside.edu"}, nil); rr.Code != http.StatusOK {
			t.Fatalf("Add() %d = %d %s, want 200", i+1, rr.Code, rr.Body.String())
		}
	}
	msgs := recorder.Messages()
	if len(msgs) != 5 || msgs[0].Subject != "Your email address and Harbor" || !strings.Contains(msgs[0].Text, "already belongs to a Harbor account") {
		t.Errorf("messages = %+v, want five notices to the holder", msgs)
	}
	if len(links.rows) != 0 {
		t.Error("a link was stored for an address another account holds, want none")
	}
	rr := f.asUser(f.auth.Email().Add(), sessionID, map[string]string{"email": "taken@lakeside.edu"}, nil)
	if body := refusal(t, rr); rr.Code != http.StatusTooManyRequests || body.Code != sessioninfo.RefusedTooManyEmails || body.RetryAfter < 1 {
		t.Errorf("sixth Add() = %d %s, want 429 too_many_emails with the wait", rr.Code, rr.Body.String())
	}
	if got := len(recorder.Messages()); got != 5 {
		t.Errorf("messages after the refusal = %d, want still five", got)
	}
	if rr := f.asUser(f.auth.Email().Add(), sessionID, map[string]string{"email": "not an address"}, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("Add() of a bad address = %d, want 400", rr.Code)
	}
}

func authEmailCase5(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, _ := newMailFixture(t, gomock.NewController(t), UsernameIsEmail())
	f.expectSession(sessionID, &sessionstorage.SessionUser{ID: userID, Username: "pat@lakeside.edu"})
	f.store.EXPECT().UserByEmail(gomock.Any(), "pat.lee@lakeside.edu").Return(nil, httpio.NewNotFoundMessage("none"))
	var renamed []string
	f.auth.SetUserRenamed(func(_ context.Context, id impulse.UUID, from, to string) error {
		if id != userID {
			t.Errorf("rename hook given user %s, want %s", id, userID)
		}
		renamed = append(renamed, from+" -> "+to)

		return nil
	})

	if rr := f.asUser(f.auth.Email().Add(), sessionID, map[string]string{"email": "pat.lee@lakeside.edu"}, nil); rr.Code != http.StatusOK {
		t.Fatalf("Add() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	token := tokenIn(t, recorder.Messages()[0].Text, "verify")
	f.store.EXPECT().VerifyUserEmail(gomock.Any(), userID, "pat.lee@lakeside.edu").Return(nil)
	if rr := f.asUser(f.auth.Email().Verify(), sessionID, map[string]string{"token": token}, nil); rr.Code != http.StatusOK {
		t.Fatalf("Verify() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	if len(renamed) != 1 || renamed[0] != "pat@lakeside.edu -> pat.lee@lakeside.edu" {
		t.Errorf("rename hook calls = %v, want the one rename", renamed)
	}
}

// passwordMethodRows answers the store's sign-in methods table with the password
// method's row: open with role as its default role when open says so, and no row at
// all otherwise, which is a method that is invite only.
func passwordMethodRows(f *authFixture, open bool, role string) {
	var rows []*sessionstorage.SignInMethod
	if open {
		audience := passwordAudience().Key()
		row := &sessionstorage.SignInMethod{Name: "password", OpenSignUp: true, OpenedFor: &audience}
		if role != "" {
			row.DefaultRole = &role
		}
		rows = append(rows, row)
	}
	f.store.EXPECT().SignInMethods(gomock.Any()).Return(rows, nil).AnyTimes()
}

func TestAuth_SignUp(t *testing.T) {
	t.Parallel()

	t.Run("while the method is open a sign-up mails a link, the link opens, and completing it makes the account through the store's one transaction, signs in and resets the address's mail count", authSignUpCase1)
	t.Run("an address an account holds is pointed at it by mail, and the screen says nothing", authSignUpCase2)
	t.Run("while the method is invite only every sign-up route answers 404: nothing is mailed, stored or created", authSignUpCase3)
	t.Run("a completion the store refuses because the method was closed since the link was mailed is 404, and nobody is signed in", authSignUpCase4)
	t.Run("the login page is told the sign-up routes only while the method is open, read as each request arrives", authSignUpCase5)
}

func authSignUpCase1(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	passwordMethodRows(f, true, "Member")
	f.store.EXPECT().UserByEmail(gomock.Any(), "new@lakeside.edu").Return(nil, httpio.NewNotFoundMessage("none"))

	if rr := f.serve(f.auth.SignUp().Start(), http.MethodPost, "/sign-up", map[string]string{"email": "new@lakeside.edu"}, nil); rr.Code != http.StatusOK {
		t.Fatalf("Start() = %d %s, want 200", rr.Code, rr.Body.String())
	}
	msgs := recorder.Messages()
	if len(msgs) != 1 || msgs[0].Subject != "Finish creating your Harbor account" {
		t.Fatalf("messages = %+v, want the sign-up link", msgs)
	}
	token := tokenIn(t, msgs[0].Text, "signUp")
	link := links.last(t)
	if link.Kind != sessionstorage.MailLinkSignUp || link.Email != "new@lakeside.edu" {
		t.Errorf("stored link = %+v, want a sign-up for the address", link)
	}
	if record := f.attemptsRecord("MAIL:new@lakeside.edu"); record == nil || record.Failures != 1 {
		t.Errorf("the address's mail count after Start() = %+v, want one", record)
	}

	rr := f.serve(f.auth.SignUp().Open(), http.MethodPost, "/sign-up/open", map[string]string{"token": token}, nil)
	if body := decoded(t, rr); rr.Code != http.StatusOK || body["email"] != "new@lakeside.edu" || body["usernameIsEmail"] != false {
		t.Errorf("Open() = %d %s, want the address and a chosen username", rr.Code, rr.Body.String())
	}

	// The account is made by the store's sign-up, one transaction that reads the
	// method's row, spends the link, writes the user and gives the default role: the
	// handler creates no user and consumes no link of its own.
	var created *securehash.Hash
	f.store.EXPECT().SignUp(gomock.Any(), "password", passwordAudience().Key(), gomock.Any(), link.ID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, u *sessionstorage.InsertSessionUser, _ impulse.UUID, _ signinflow.SignUpSeater[mockTx]) (impulse.UUID, error) {
			if u.Username != "newbie" || u.PasswordHash == nil || u.Email == nil || *u.Email != "new@lakeside.edu" || !u.EmailVerified {
				t.Errorf("SignUp() given %+v, want the chosen username, a hash and the address verified", u)
			}
			created = u.PasswordHash
			// A role is held by an account's id, and a seed writes roles under an id it
			// derives before the user exists (SeedUserID). A sign-up that chose the id it
			// is created under, from the name or otherwise, could take roles written
			// there; the store generates it, under which nothing is held.
			if u.ID.Valid {
				t.Errorf("SignUp() given the id %s, want none: the store generates a new account's id", u.ID.UUID)
			}

			return userID, nil
		})
	f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) {
		if req.UserID != userID || req.Username != "newbie" || req.Identity.Method != sessioninfo.MethodPassword || req.Invitation != nil {
			t.Errorf("SignIn() given %+v, want the new user's password identity", req)
		}
		// The address the link verified is the identity's, so the store holds an
		// invitation the browser carries to it: one mailed to another address is refused.
		if req.Identity.Email != "new@lakeside.edu" || !req.Identity.EmailVerified {
			t.Errorf("SignIn() given the identity %+v, want the address the sign-up verified", req.Identity)
		}
	}, nil)
	rr = f.serve(f.auth.SignUp().Complete(), http.MethodPost, "/sign-up/complete", map[string]string{"token": token, "password": "correct horse", "username": "newbie"}, nil)
	if rr.Code != http.StatusOK || cookieNamed(rr, internalcookie.AuthCookieName) == nil {
		t.Errorf("Complete() = %d %s, want a session", rr.Code, rr.Body.String())
	}
	// The sign-in rests on the password the account was created with, as a login rests
	// on the one it checked: the session is inserted only while the account still holds
	// it, so a password changed between the account and its session ends the sign-up's
	// sign-in as it ends any other.
	if want := proofOf(t, created); f.signedIn == nil || !bytes.Equal(f.signedIn.proven, want) {
		t.Errorf("SignIn() after a sign-up given the proof %x, want %x: the proof of the password the account was created with", f.signedIn.proven, want)
	}
	if record := f.attemptsRecord("MAIL:new@lakeside.edu"); record != nil {
		t.Errorf("the address's mail count after the sign-up was completed = %+v, want none", record)
	}
}

func authSignUpCase2(t *testing.T) {
	t.Parallel()
	userID, _ := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	passwordMethodRows(f, true, "")
	f.store.EXPECT().UserByEmail(gomock.Any(), "pat@lakeside.edu").Return(&sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil)

	if rr := f.serve(f.auth.SignUp().Start(), http.MethodPost, "/sign-up", map[string]string{"email": "pat@lakeside.edu"}, nil); rr.Code != http.StatusOK || rr.Body.String() != "{}\n" {
		t.Errorf("Start() = %d %s, want 200 and nothing said", rr.Code, rr.Body.String())
	}
	if msgs := recorder.Messages(); len(msgs) != 1 || msgs[0].Subject != "You already have a Harbor account" {
		t.Errorf("messages = %+v, want the notice", msgs)
	}
	if len(links.rows) != 0 {
		t.Error("a link was stored for a held address, want none")
	}
}

func authSignUpCase3(t *testing.T) {
	t.Parallel()
	audience, another := passwordAudience().Key(), signin.Audience{Organizations: []string{"lakeside.edu"}}.Key()
	// The password method's audience as a row kept it before the key compared what
	// identifies whom a method admits: the names of its organizations, of which it has
	// none, and the words for its accounts.
	earlier := `{"organizations":[],"accounts":"an email address"}`

	tests := []struct {
		name string
		rows []*sessionstorage.SignInMethod
	}{
		{name: "no row"},
		{name: "a row that says not open", rows: []*sessionstorage.SignInMethod{{Name: "password"}}},
		{name: "another method's row", rows: []*sessionstorage.SignInMethod{{Name: "google", OpenSignUp: true, OpenedFor: &audience}}},
		{name: "a row opened for another audience than the password method's", rows: []*sessionstorage.SignInMethod{{Name: "password", OpenSignUp: true, OpenedFor: &another}}},
		{name: "a row that names no audience", rows: []*sessionstorage.SignInMethod{{Name: "password", OpenSignUp: true}}},
		{name: "a row that kept the password method's own audience in the earlier form, by its words", rows: []*sessionstorage.SignInMethod{{Name: "password", OpenSignUp: true, OpenedFor: &earlier}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, recorder, links := newMailFixture(t, gomock.NewController(t))
			f.store.EXPECT().SignInMethods(gomock.Any()).Return(tt.rows, nil).AnyTimes()
			// A link mailed while the method was open still stands.
			token, hash, err := linkToken()
			if err != nil {
				t.Fatal(err)
			}
			links.insert(&sessionstorage.MailLink{TokenHash: hash, Kind: sessionstorage.MailLinkSignUp, Email: "new@lakeside.edu", ExpiresAt: time.Now().Add(time.Hour)})

			if rr := f.serve(f.auth.SignUp().Start(), http.MethodPost, "/sign-up", map[string]string{"email": "new@lakeside.edu"}, nil); rr.Code != http.StatusNotFound {
				t.Errorf("Start() while the method is invite only = %d %s, want 404", rr.Code, rr.Body.String())
			}
			if rr := f.serve(f.auth.SignUp().Open(), http.MethodPost, "/sign-up/open", map[string]string{"token": token}, nil); rr.Code != http.StatusNotFound {
				t.Errorf("Open() of a live link while the method is invite only = %d %s, want 404", rr.Code, rr.Body.String())
			}
			if msgs := recorder.Messages(); len(msgs) != 0 {
				t.Errorf("messages = %+v, want nothing mailed", msgs)
			}
			if len(links.rows) != 1 || links.rows[0].ConsumedAt != nil {
				t.Errorf("mail links = %d (the first consumed %v), want the one link, untouched", len(links.rows), links.rows[0].ConsumedAt)
			}
		})
	}
}

func authSignUpCase4(t *testing.T) {
	t.Parallel()
	f, _, links := newMailFixture(t, gomock.NewController(t))
	token, hash, err := linkToken()
	if err != nil {
		t.Fatal(err)
	}
	links.insert(&sessionstorage.MailLink{TokenHash: hash, Kind: sessionstorage.MailLinkSignUp, Email: "new@lakeside.edu", ExpiresAt: time.Now().Add(time.Hour)})
	// The store reads the method's row in the transaction that would make the account,
	// and refuses: the handler asks nobody else, creates no user and signs nobody in.
	f.store.EXPECT().SignUp(gomock.Any(), "password", passwordAudience().Key(), gomock.Any(), links.last(t).ID, gomock.Any()).Return(impulse.NilUUID, signinflow.SignUpClosed())

	rr := f.serve(f.auth.SignUp().Complete(), http.MethodPost, "/sign-up/complete", map[string]string{"token": token, "password": "correct horse", "username": "newbie"}, nil)

	// The mocked store refuses a call it was not told of: no sign-in follows.
	if rr.Code != http.StatusNotFound {
		t.Errorf("Complete() through a closed method = %d %s, want 404", rr.Code, rr.Body.String())
	}
	if record := f.attemptsRecord("MAIL:new@lakeside.edu"); record != nil {
		t.Errorf("the address's mail count = %+v, want it untouched by a refused sign-up", record)
	}
}

func authSignUpCase5(t *testing.T) {
	t.Parallel()
	f, _, _ := newMailFixture(t, gomock.NewController(t))
	open := false
	f.store.EXPECT().SignInMethods(gomock.Any()).DoAndReturn(func(context.Context) ([]*sessionstorage.SignInMethod, error) {
		if !open {
			return nil, nil
		}

		audience := passwordAudience().Key()

		return []*sessionstorage.SignInMethod{{Name: "password", OpenSignUp: true, OpenedFor: &audience}}, nil
	}).AnyTimes()
	h := f.auth.Methods(&MethodList{
		Methods:        []AdvertisedMethod{{Name: "password", Label: "Password", Path: "/api/user/password/login"}},
		ChangePassword: "/api/user/password",
		SignUp:         SignUpRoutes{Start: "/api/user/sign-up", Open: "/api/user/sign-up/open", Complete: "/api/user/sign-up/complete"},
	})

	rr := f.serve(h, http.MethodGet, "/api/user/methods", nil, nil)
	if body := decoded(t, rr); rr.Code != http.StatusOK || body["signUp"] != nil {
		t.Errorf("Methods() while the method is invite only = %d %s, want no sign-up block", rr.Code, rr.Body.String())
	}

	open = true
	rr = f.serve(h, http.MethodGet, "/api/user/methods", nil, nil)
	want := map[string]any{"start": "/api/user/sign-up", "open": "/api/user/sign-up/open", "complete": "/api/user/sign-up/complete"}
	if body := decoded(t, rr); rr.Code != http.StatusOK || !reflect.DeepEqual(body["signUp"], want) {
		t.Errorf("Methods() once the method is open = %d %s, want the sign-up routes %v", rr.Code, rr.Body.String(), want)
	}

	open = false
	rr = f.serve(h, http.MethodGet, "/api/user/methods", nil, nil)
	if body := decoded(t, rr); rr.Code != http.StatusOK || body["signUp"] != nil {
		t.Errorf("Methods() once the method is invite only again = %d %s, want no sign-up block", rr.Code, rr.Body.String())
	}
}

func TestNewAuth_Mail(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		settings MailSettings
		want     string
	}{
		{name: "no mail settings at all", settings: MailSettings{}, want: "sender is nil"},
		{name: "no sender", settings: testMail, want: "sender is nil"},
		{name: "no application name", settings: MailSettings{Sender: &mail.Recorder{}, LoginURL: "https://harbor.test/login"}, want: "application name is empty"},
		{name: "a relative login URL", settings: MailSettings{Sender: &mail.Recorder{}, Application: "Harbor", LoginURL: "/login"}, want: "not an absolute"},
		{name: "a login URL with a fragment", settings: MailSettings{Sender: &mail.Recorder{}, Application: "Harbor", LoginURL: "https://harbor.test/#/login"}, want: "not an absolute"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewAuth[NoCustomData, NoCustomData](newAuthStoreMock(gomock.NewController(t)), cookieKey, NoSuperAdministrators(), tt.settings)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("NewAuth() error = %v, want %q", err, tt.want)
			}
		})
	}

	t.Run("the invitation cookie must differ from the session and pending cookies", func(t *testing.T) {
		t.Parallel()
		if _, err := NewAuth[NoCustomData, NoCustomData](newAuthStoreMock(gomock.NewController(t)), cookieKey, NoSuperAdministrators(), quietMail(), WithInvitationCookieName(defaultPendingCookieName)); err == nil {
			t.Error("NewAuth() with the pending cookie's name for the invitation cookie error = nil, want a refusal")
		}
	})
}

func TestAuth_ClaimVerifiedEmail(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	for _, tt := range []struct {
		name     string
		identity *sessioninfo.Identity
		claims   int
	}{
		{name: "a method that verified the address writes it", identity: &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Email: "pat@lakeside.edu", EmailVerified: true}, claims: 1},
		{name: "an address the method did not verify is not written", identity: &sessioninfo.Identity{Method: sessioninfo.MethodAzure, Email: "pat@lakeside.edu"}},
		{name: "no address, nothing", identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			store := mockstore.NewAuthStore[mockTx](ctrl)
			store.EXPECT().CustomUserDataType().Return(nil).AnyTimes()
			store.EXPECT().ClaimVerifiedEmail(gomock.Any(), userID, "pat@lakeside.edu").Return(true, nil).Times(tt.claims)
			a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey, NoSuperAdministrators(), quietMail())
			if err != nil {
				t.Fatal(err)
			}

			a.claimVerifiedEmail(context.Background(), userID, tt.identity)
		})
	}
}

func strPtr(s string) *string { return &s }

// newIDs gives a case its own user and session ids.
func newIDs() (userID, sessionID impulse.UUID) {
	return impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
}

// pat is the signed-in user of the email cases.
func pat(userID impulse.UUID) *sessionstorage.SessionUser {
	return &sessionstorage.SessionUser{ID: userID, Username: "pat"}
}
