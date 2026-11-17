package session

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// The invitation that makes a tenant, at the Auth: what the API stores and refuses,
// what the invitation page is told, what the mailed message leaves out, and how the
// two handlers that accept without a directory sign-in are held to a tenant entered
// through its directory only.

// lakesideDetails is what staff entered for the tenant beside its name, as the
// application hands it over: the library stores it and reads none of it.
const lakesideDetails = `{"Phone":"555-0100","Plan":"district"}`

// lakesideHigh is the tenant an invitation makes in these tests.
func lakesideHigh(directoryOnly bool) *sessioninfo.NewTenant {
	return &sessioninfo.NewTenant{Name: "Lakeside High", DirectoryOnly: directoryOnly, Details: json.RawMessage(lakesideDetails)}
}

const (
	wantNamesNoTenant = "An invitation that makes a tenant names no tenant and carries no roles: the tenant does not exist yet, and whoever accepts it becomes its first administrator."
	wantNeedsName     = "An invitation that makes a tenant needs the tenant's name: the invited person is shown it before they sign in."
)

// refusedTenantRequests are the requests that make a tenant and are no invitation of
// that kind, with the refusal each is answered.
func refusedTenantRequests() []struct {
	name string
	req  *InvitationRequest
	want string
} {
	return []struct {
		name string
		req  *InvitationRequest
		want string
	}{
		{name: "no name", req: &InvitationRequest{Email: "pat@lakeside.edu", NewTenant: &sessioninfo.NewTenant{Details: json.RawMessage(lakesideDetails)}}, want: wantNeedsName},
		{name: "a name of spaces", req: &InvitationRequest{Email: "pat@lakeside.edu", NewTenant: &sessioninfo.NewTenant{Name: " \t "}}, want: wantNeedsName},
		{name: "a tenant named beside the one to make", req: &InvitationRequest{Email: "pat@lakeside.edu", TenantID: "riverside", NewTenant: lakesideHigh(false)}, want: wantNamesNoTenant},
		{name: "roles beside the tenant to make", req: &InvitationRequest{Email: "pat@lakeside.edu", RoleNames: []string{"Teacher"}, NewTenant: lakesideHigh(false)}, want: wantNamesNoTenant},
	}
}

func TestAuth_InviteNewTenant(t *testing.T) {
	t.Parallel()

	t.Run("an invitation that makes a tenant is stored with what staff entered, the name without the space around it, and is answered and listed with it", func(t *testing.T) {
		t.Parallel()
		f, _, links := newMailFixture(t, gomock.NewController(t))
		req := &InvitationRequest{Email: "pat@lakeside.edu", NewTenant: &sessioninfo.NewTenant{Name: "  Lakeside High ", DirectoryOnly: true, Details: json.RawMessage(lakesideDetails)}}

		inv, err := f.auth.API().Invite(context.Background(), req)
		if err != nil {
			t.Fatalf("Invite() error = %v", err)
		}
		row := links.last(t)
		if row.Kind != sessionstorage.MailLinkInvitation || row.TenantID != "" || len(row.RoleNames) != 0 {
			t.Errorf("stored link = %+v, want an invitation that names no tenant and carries no roles", row)
		}
		if diff := cmp.Diff(lakesideHigh(true), row.NewTenant); diff != "" {
			t.Errorf("stored tenant mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(lakesideHigh(true), inv.NewTenant); diff != "" {
			t.Errorf("Invite() tenant mismatch (-want +got):\n%s", diff)
		}
		// What is stored is the library's own copy: the caller's request changing
		// afterwards changes no invitation.
		req.NewTenant.Details[2] = 'X'
		if got := string(links.last(t).NewTenant.Details); got != lakesideDetails {
			t.Errorf("stored details after the caller changed its request = %s, want %s", got, lakesideDetails)
		}
		live, err := f.auth.API().Invitations(context.Background())
		if err != nil || len(live) != 1 {
			t.Fatalf("Invitations() = %v, %v; want the one standing", live, err)
		}
		if diff := cmp.Diff(lakesideHigh(true), live[0].NewTenant); diff != "" {
			t.Errorf("Invitations() tenant mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a tenant with no name, and one beside a tenant named or roles, is refused in words: nothing stored or mailed", func(t *testing.T) {
		t.Parallel()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		for _, tt := range refusedTenantRequests() {
			if _, err := f.auth.API().Invite(context.Background(), tt.req); !httpio.HasBadRequest(err) || httpio.Message(err) != tt.want {
				t.Errorf("Invite() with %s error = %v, want BadRequest %q", tt.name, err, tt.want)
			}
		}
		if len(links.rows) != 0 || len(recorder.Messages()) != 0 {
			t.Errorf("rows = %d, messages = %d after the refusals; want nothing stored or mailed", len(links.rows), len(recorder.Messages()))
		}
	})
}

func TestAuth_PrepareInvitationNewTenant(t *testing.T) {
	t.Parallel()

	t.Run("the row to store carries the tenant, the name without the space around it, and is a copy", func(t *testing.T) {
		t.Parallel()
		f, _, _ := newMailFixture(t, gomock.NewController(t))

		prepared, err := f.auth.API().PrepareInvitation(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", NewTenant: &sessioninfo.NewTenant{Name: " Lakeside High", Details: json.RawMessage(lakesideDetails)}})
		if err != nil {
			t.Fatalf("PrepareInvitation() error = %v", err)
		}
		row := prepared.Row()
		if row.TenantID != "" || len(row.RoleNames) != 0 {
			t.Errorf("Row() = %+v, want a row that names no tenant and carries no roles", row)
		}
		if diff := cmp.Diff(lakesideHigh(false), row.NewTenant); diff != "" {
			t.Errorf("Row() tenant mismatch (-want +got):\n%s", diff)
		}
		// The column the caller writes is the document the drivers read back.
		column, err := row.NewTenantColumn()
		if want := `{"name":"Lakeside High","details":` + lakesideDetails + `}`; err != nil || column == nil || *column != want {
			t.Errorf("Row().NewTenantColumn() = %v, %v; want %s", column, err, want)
		}
		row.NewTenant.Name, row.NewTenant.Details[2] = "Riverside High", 'X'
		if diff := cmp.Diff(lakesideHigh(false), prepared.Row().NewTenant); diff != "" {
			t.Errorf("Row() after the caller changed its copy: tenant mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a tenant with no name, and one beside a tenant named or roles, is refused in words", func(t *testing.T) {
		t.Parallel()
		f, _, _ := newMailFixture(t, gomock.NewController(t))
		for _, tt := range refusedTenantRequests() {
			if _, err := f.auth.API().PrepareInvitation(context.Background(), tt.req); !httpio.HasBadRequest(err) || httpio.Message(err) != tt.want {
				t.Errorf("PrepareInvitation() with %s error = %v, want BadRequest %q", tt.name, err, tt.want)
			}
		}
	})

	t.Run("the row of an invitation that makes no tenant writes the column null", func(t *testing.T) {
		t.Parallel()
		f, _, _ := newMailFixture(t, gomock.NewController(t))
		prepared, err := f.auth.API().PrepareInvitation(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: "lakeside", RoleNames: []string{"Teacher"}})
		if err != nil {
			t.Fatalf("PrepareInvitation() error = %v", err)
		}
		if column, err := prepared.Row().NewTenantColumn(); err != nil || column != nil {
			t.Errorf("Row().NewTenantColumn() = %v, %v; want null", column, err)
		}
	})
}

// TestAuth_TenantInvitationMessage pins what the mailed message says of the tenant an
// invitation makes: nothing. A message is read by whoever holds the mailbox, and by
// every relay on the way, before anyone has signed in; the tenant's name is shown on
// the invitation page, to the browser that holds the link.
func TestAuth_TenantInvitationMessage(t *testing.T) {
	t.Parallel()

	settings := &mailSettings{settings: testMail}
	carriesNothing := func(t *testing.T, got mail.Message) {
		t.Helper()

		want := settings.invitationMessage("pat@lakeside.edu", tokenIn(t, got.Text, "invitation"), 7*24*time.Hour)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("the message of an invitation that makes a tenant is not the message of any invitation (-want +got):\n%s", diff)
		}
		for _, detail := range []string{"Lakeside High", "555-0100", "district"} {
			if strings.Contains(got.Subject+"\n"+got.Text, detail) {
				t.Errorf("the message carries %q of the tenant, want no detail of it:\n%s\n%s", detail, got.Subject, got.Text)
			}
		}
	}

	t.Run("an invitation the library stores and sends", func(t *testing.T) {
		t.Parallel()
		f, recorder, _ := newMailFixture(t, gomock.NewController(t))
		if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", NewTenant: lakesideHigh(true)}); err != nil {
			t.Fatalf("Invite() error = %v", err)
		}
		msgs := recorder.Messages()
		if len(msgs) != 1 {
			t.Fatalf("messages = %+v, want one invitation", msgs)
		}
		carriesNothing(t, msgs[0])
	})

	t.Run("an invitation the caller stores and the library sends", func(t *testing.T) {
		t.Parallel()
		f, recorder, links := newMailFixture(t, gomock.NewController(t))
		prepared, err := f.auth.API().PrepareInvitation(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", NewTenant: lakesideHigh(true)})
		if err != nil {
			t.Fatalf("PrepareInvitation() error = %v", err)
		}
		row := prepared.Row()
		links.insert(&sessionstorage.MailLink{ID: row.ID, TokenHash: row.TokenHash, Kind: sessionstorage.MailLinkInvitation, Email: row.Email, NewTenant: row.NewTenant, ExpiresAt: row.ExpiresAt})
		if err := prepared.Send(context.Background()); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		msgs := recorder.Messages()
		if len(msgs) != 1 {
			t.Fatalf("messages = %+v, want one invitation", msgs)
		}
		carriesNothing(t, msgs[0])
	})
}

// tenantInvitation invites pat to make Lakeside High and opens the invitation in the
// browser, returning the cookies the browser then carries and what Open answered.
func tenantInvitation(t *testing.T, f *authFixture, recorder *mail.Recorder, links *mailLinks, directoryOnly bool) (cookies []*http.Cookie, opened map[string]any) {
	t.Helper()

	links.expectAccounts(f)
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", NewTenant: lakesideHigh(directoryOnly)}); err != nil {
		t.Fatalf("Invite() error = %v", err)
	}
	msgs := recorder.Messages()
	rr := f.serve(f.auth.Invitation().Open(), http.MethodPost, "/invitation", map[string]string{"token": tokenIn(t, msgs[len(msgs)-1].Text, "invitation")}, nil)
	if rr.Code != http.StatusOK || cookieNamed(rr, defaultInvitationCookieName) == nil {
		t.Fatalf("Open() = %d %s, want 200 and the invitation cookie", rr.Code, rr.Body.String())
	}

	return rr.Result().Cookies(), decoded(t, rr)
}

func TestAuth_InvitationNewTenant(t *testing.T) {
	t.Parallel()

	t.Run("opening an invitation that makes a tenant answers that it does, the tenant's name as the invitation carries it and no id, and offers an account with a password", authNewTenantOpen)
	t.Run("opening one entered through its directory only says so and offers no account with a password; the methods listing reports the standing invitation the same", authNewTenantOpenDirectoryOnly)
	t.Run("an invitation that makes no tenant answers that it makes none", authNewTenantOpenPlain)
	t.Run("a sign-up from an invitation entered through its directory only is refused before any account is created, the invitation standing and the browser still carrying it", authNewTenantSignUpRefused)
	t.Run("a sign-up from an invitation that makes a tenant anyone may enter creates the account, and its sign-in carries the tenant", authNewTenantSignUp)
	t.Run("a sign-up whose sign-in did not accept the invitation leaves no account: the invitation stands, and the account made for it is deleted", authNewTenantSignUpNotAccepted)
	t.Run("a sign-up whose sign-in accepted the invitation and was refused afterwards keeps the account: the tenant is made, and the account is its administrator's", authNewTenantSignUpAcceptedThenRefused)
	t.Run("a signed-in person's acceptance tells the store how the session was established", authNewTenantAcceptEstablished)
	t.Run("an acceptance the store refuses for a session that came through no organization's directory is 403 saying what to do, the browser still carrying the invitation", authNewTenantAcceptRefused)
	t.Run("an acceptance the application refuses because the person's organization already has a tenant is 403 saying what to do and nothing of that tenant, the browser still carrying the invitation", authNewTenantAcceptOrganizationHasTenant)
}

func authNewTenantOpen(t *testing.T) {
	t.Parallel()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))

	_, body := tenantInvitation(t, f, recorder, links, false)

	if body["newTenant"] != true || body["tenantName"] != "Lakeside High" || body["tenantId"] != nil {
		t.Errorf("Open() = %v, want newTenant true, the tenant's name Lakeside High and no id", body)
	}
	if body["directoryOnly"] != false || body["signUp"] != true {
		t.Errorf("Open() = %v, want directoryOnly false and an account with a password offered", body)
	}
	if raw, err := json.Marshal(body); err != nil || body["details"] != nil || strings.Contains(string(raw), "555-0100") {
		t.Errorf("Open() = %s (%v), want none of the tenant's details: they are the acceptance hook's alone", raw, err)
	}
}

func authNewTenantOpenDirectoryOnly(t *testing.T) {
	t.Parallel()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))

	cookies, body := tenantInvitation(t, f, recorder, links, true)

	if body["newTenant"] != true || body["tenantName"] != "Lakeside High" || body["directoryOnly"] != true {
		t.Errorf("Open() = %v, want newTenant true, the tenant's name and directoryOnly true", body)
	}
	if body["signUp"] != false {
		t.Errorf("Open() = %v, want signUp false: an account with a password could not accept the invitation", body)
	}
	if raw, err := json.Marshal(body); err != nil || strings.Contains(string(raw), "555-0100") {
		t.Errorf("Open() = %s (%v), want none of the tenant's details", raw, err)
	}
	standing, _ := invitationBlock(t, f, invitationList, cookies)["standing"].(map[string]any)
	if standing["newTenant"] != true || standing["tenantName"] != "Lakeside High" || standing["directoryOnly"] != true || standing["tenantId"] != nil {
		t.Errorf("the methods listing reports standing = %v, want newTenant true, the tenant's name, directoryOnly true and no id", standing)
	}
}

func authNewTenantOpenPlain(t *testing.T) {
	t.Parallel()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	links.expectAccounts(f)
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: "lakeside"}); err != nil {
		t.Fatal(err)
	}

	body := decoded(t, f.serve(f.auth.Invitation().Open(), http.MethodPost, "/invitation", map[string]string{"token": tokenIn(t, recorder.Messages()[0].Text, "invitation")}, nil))

	if body["newTenant"] != false || body["directoryOnly"] != false || body["signUp"] != true {
		t.Errorf("Open() = %v, want newTenant false, directoryOnly false and an account with a password offered", body)
	}
}

func authNewTenantSignUpRefused(t *testing.T) {
	t.Parallel()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	cookies, _ := tenantInvitation(t, f, recorder, links, true)

	// The mocked store expects no CreateUser and no SignIn: either call fails the test.
	rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", map[string]string{"password": "correct horse", "username": "patl"}, cookies)

	body := decoded(t, rr)
	if rr.Code != http.StatusUnauthorized || body["code"] != string(sessioninfo.RefusedOrganizationAccountRequired) || body["message"] != signinflow.OrganizationAccountMessage {
		t.Errorf("SignUp() = %d %s, want 401 organization_account_required saying what to sign in with", rr.Code, rr.Body.String())
	}
	if cookieNamed(rr, internalcookie.AuthCookieName) != nil {
		t.Error("SignUp() set a session cookie, want nobody signed in")
	}
	if c := cookieNamed(rr, defaultInvitationCookieName); c != nil && c.Value == "" {
		t.Error("SignUp() deleted the invitation cookie, want it kept: the invitation stands for the directory sign-in")
	}
	if live, err := f.auth.API().Invitations(context.Background()); err != nil || len(live) != 1 {
		t.Errorf("Invitations() after the refusal = %v, %v; want the one still standing", live, err)
	}
}

func authNewTenantSignUp(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	cookies, _ := tenantInvitation(t, f, recorder, links, false)
	f.store.EXPECT().CreateUser(gomock.Any(), gomock.Any(), nil).Return(&sessionstorage.SessionUser{ID: userID, Username: "patl", Email: strPtr("pat@lakeside.edu")}, nil)
	f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) {
		if req.Invitation == nil || req.Invitation.ID != links.rows[0].ID {
			t.Fatalf("SignIn() given the invitation %+v, want the opened one", req.Invitation)
		}
		if diff := cmp.Diff(lakesideHigh(false), req.Invitation.NewTenant); diff != "" {
			t.Errorf("SignIn() given the invitation's tenant (-want +got):\n%s", diff)
		}
		req.Invitation.Accepted = true
	}, nil)

	if rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", map[string]string{"password": "correct horse", "username": "patl"}, cookies); rr.Code != http.StatusOK || cookieNamed(rr, internalcookie.AuthCookieName) == nil {
		t.Errorf("SignUp() = %d %s, want a session", rr.Code, rr.Body.String())
	}
}

func authNewTenantSignUpNotAccepted(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	cookies, _ := tenantInvitation(t, f, recorder, links, false)
	f.store.EXPECT().CreateUser(gomock.Any(), gomock.Any(), nil).Return(&sessionstorage.SessionUser{ID: userID, Username: "patl", Email: strPtr("pat@lakeside.edu")}, nil)
	// The application's hook refuses to make the tenant: the sign-in's first transaction
	// keeps nothing, so the invitation stands.
	f.expectSignIn(sessionID, nil, func(*sessioninfo.NewSessionRequest) error {
		return sessioninfo.NewLoginRefusal(sessioninfo.RefusedOrganizationHasTenant, httpio.NewConflictMessage("Your organization is already set up here."))
	})
	f.store.EXPECT().DeleteUser(gomock.Any(), userID).Return(nil)

	rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", map[string]string{"password": "correct horse", "username": "patl"}, cookies)

	if body := decoded(t, rr); rr.Code != http.StatusUnauthorized || body["code"] != string(sessioninfo.RefusedOrganizationHasTenant) {
		t.Errorf("SignUp() = %d %s, want 401 on the hook's code", rr.Code, rr.Body.String())
	}
	if cookieNamed(rr, internalcookie.AuthCookieName) != nil {
		t.Error("SignUp() set a session cookie, want nobody signed in")
	}
	if row := links.rows[0]; row.ConsumedAt != nil {
		t.Error("the invitation was consumed, want it standing")
	}
}

func authNewTenantSignUpAcceptedThenRefused(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	cookies, _ := tenantInvitation(t, f, recorder, links, false)
	f.store.EXPECT().CreateUser(gomock.Any(), gomock.Any(), nil).Return(&sessionstorage.SessionUser{ID: userID, Username: "patl", Email: strPtr("pat@lakeside.edu")}, nil)
	// The acceptance committed, and the sign-in was refused after it: the invitation is
	// spent and the tenant made. The mocked store expects no DeleteUser: a call fails
	// the test.
	f.expectSignIn(sessionID, nil, func(*sessioninfo.NewSessionRequest) error {
		now := time.Now()
		links.rows[0].ConsumedAt = &now

		return sessioninfo.NewLoginRefusal(sessioninfo.RefusedByPolicy, httpio.NewUnauthorizedMessage("the sign-in policy refused the sign-in"))
	})

	rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", map[string]string{"password": "correct horse", "username": "patl"}, cookies)

	if rr.Code != http.StatusUnauthorized || cookieNamed(rr, internalcookie.AuthCookieName) != nil {
		t.Errorf("SignUp() = %d %s, want the refusal and no session", rr.Code, rr.Body.String())
	}
}

// expectSessionThrough makes sessionID a live session of user for the store, one that
// records how it was established, oldest event first.
func (f *authFixture) expectSessionThrough(sessionID impulse.UUID, user *sessionstorage.SessionUser, events ...sessioninfo.AuthEvent) {
	session := &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: user.Username, UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(user.ID), AuthEvents: events,
	}
	f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session, nil).AnyTimes()
	f.store.EXPECT().User(gomock.Any(), user.ID).Return(user, nil).AnyTimes()
}

func authNewTenantAcceptEstablished(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name           string
		events         []sessioninfo.AuthEvent
		wantMethod     sessioninfo.AuthMethod
		wantConnection string
	}{
		{
			name:       "a session through a Google Workspace, stepped up with a mailed code since: the Workspace",
			events:     []sessioninfo.AuthEvent{{Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu"}, {Method: sessioninfo.MethodEmailCode}},
			wantMethod: sessioninfo.MethodGoogle, wantConnection: "lakeside.edu",
		},
		{
			name:       "a session by password: the password, no connection",
			events:     []sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword}},
			wantMethod: sessioninfo.MethodPassword,
		},
		{
			name: "a session that records no events: nothing, which is no organization's directory",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			userID, sessionID := newIDs()
			f, recorder, links := newMailFixture(t, gomock.NewController(t))
			f.expectSessionThrough(sessionID, &sessionstorage.SessionUser{ID: userID, Username: "pat"}, tt.events...)
			cookies, _ := tenantInvitation(t, f, recorder, links, true)
			f.store.EXPECT().AcceptInvitation(gomock.Any(), userID, "pat", "", gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ impulse.UUID, _, _ string, inv *sessioninfo.Invitation, _ signinflow.InvitationAcceptor[mockTx]) error {
					if inv.SignInMethod != tt.wantMethod || inv.SignInConnection != tt.wantConnection {
						t.Errorf("AcceptInvitation() told the session came through %q %q, want %q %q", inv.SignInMethod, inv.SignInConnection, tt.wantMethod, tt.wantConnection)
					}
					if diff := cmp.Diff(lakesideHigh(true), inv.NewTenant); diff != "" {
						t.Errorf("AcceptInvitation() given the invitation's tenant (-want +got):\n%s", diff)
					}
					inv.Accepted = true

					return nil
				})

			if rr := f.asUser(f.auth.Invitation().Accept(), sessionID, nil, cookies); rr.Code != http.StatusOK || decoded(t, rr)["accepted"] != true {
				t.Errorf("Accept() = %d %s, want accepted", rr.Code, rr.Body.String())
			}
		})
	}
}

func authNewTenantAcceptRefused(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.expectSessionThrough(sessionID, &sessionstorage.SessionUser{ID: userID, Username: "pat"}, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword})
	cookies, _ := tenantInvitation(t, f, recorder, links, true)
	// The store's transaction is where the rule is held (signinflow.AcceptInvitation);
	// the handler's part is the answer.
	f.store.EXPECT().AcceptInvitation(gomock.Any(), userID, "pat", "", gomock.Any(), gomock.Any()).Return(
		signinflow.Refusal("", sessioninfo.RefusedOrganizationAccountRequired, signin.ErrOrganizationAccountRequired, signinflow.OrganizationAccountMessage))

	rr := f.asUser(f.auth.Invitation().Accept(), sessionID, nil, cookies)

	if rr.Code != http.StatusForbidden || decoded(t, rr)["message"] != signinflow.OrganizationAccountMessage {
		t.Errorf("Accept() = %d %s, want 403 saying to sign in with the organization's account", rr.Code, rr.Body.String())
	}
	if c := cookieNamed(rr, defaultInvitationCookieName); c != nil && c.Value == "" {
		t.Error("Accept() deleted the invitation cookie, want it kept: the invitation stands")
	}
}

func authNewTenantAcceptOrganizationHasTenant(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.expectSessionThrough(sessionID, &sessionstorage.SessionUser{ID: userID, Username: "pat"}, sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu"})
	cookies, _ := tenantInvitation(t, f, recorder, links, false)
	// The application's hook refuses in its own words, which may name what it likes: the
	// answer is the library's sentence, which says nothing of the other tenant.
	f.store.EXPECT().AcceptInvitation(gomock.Any(), userID, "pat", "", gomock.Any(), gomock.Any()).Return(
		sessioninfo.NewLoginRefusal(sessioninfo.RefusedOrganizationHasTenant, httpio.NewConflictMessage("lakeside.edu already has the tenant Riverside High here")))

	rr := f.asUser(f.auth.Invitation().Accept(), sessionID, nil, cookies)

	if rr.Code != http.StatusForbidden || decoded(t, rr)["message"] != signinflow.OrganizationHasTenantMessage {
		t.Errorf("Accept() = %d %s, want 403 saying the organization is set up already and what to do", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "Riverside") {
		t.Errorf("Accept() answered %s, want nothing of the tenant the organization has", rr.Body.String())
	}
	if c := cookieNamed(rr, defaultInvitationCookieName); c != nil && c.Value == "" {
		t.Error("Accept() deleted the invitation cookie, want it kept: the invitation stands")
	}
}
