package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"go.uber.org/mock/gomock"
)

// invitedTenantCookie is the cookie that carries the tenant an accepted invitation
// seated a person in to the first session read after the acceptance.
const invitedTenantCookie = defaultInvitationCookieName + "-seated"

// lakesideTenant is the tenant the invitations of these tests seat a person in.
const lakesideTenant = "7b0c2f1e-9a4d-4c3b-8e51-2d6f0a9b4c17"

func TestAuth_InvitedTenant(t *testing.T) {
	t.Parallel()

	t.Run("a sign-in that accepts an invitation into a tenant has the first session read name the tenant, and no later one", authInvitedTenantSignIn)
	t.Run("a signed-in person who accepts an invitation into a tenant is told the tenant by the next session read", authInvitedTenantByHand)
	t.Run("a sign-in that accepts an invitation that makes a tenant has the first session read name the tenant it made", authInvitedTenantMade)
	t.Run("a signed-in person who accepts an invitation that makes a tenant is told the tenant it made", authInvitedTenantMadeByHand)
	t.Run("an invitation into the application, one a sign-in did not accept and one whose hook named no tenant it made name no tenant", authInvitedTenantNone)
	t.Run("another person's session is told nothing, and the browser stops carrying the tenant", authInvitedTenantOtherPerson)
	t.Run("an impersonated session is told nothing, and the tenant is left for the person's own session", authInvitedTenantImpersonated)
}

// sessionRead is the session read of sessionID with cookies.
func sessionRead(f *authFixture, sessionID impulse.UUID, cookies []*http.Cookie) *httptest.ResponseRecorder {
	return f.asUser(f.auth.Authenticated(), sessionID, nil, cookies)
}

// carried are the cookies of rr a browser would send back: every cookie it set that it
// did not delete.
func carried(rr *httptest.ResponseRecorder) []*http.Cookie {
	var kept []*http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Value != "" {
			kept = append(kept, c)
		}
	}

	return kept
}

func authInvitedTenantSignIn(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: lakesideTenant, RoleNames: []string{"Teacher"}}); err != nil {
		t.Fatal(err)
	}
	cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))
	created := &sessionstorage.SessionUser{ID: userID, Username: "patl", Email: strPtr("pat@lakeside.edu")}
	f.store.EXPECT().CreateUser(gomock.Any(), gomock.Any(), nil).Return(created, nil)
	f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) {
		req.Invitation.Accepted = true
	}, nil)

	rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", map[string]string{"password": "correct horse", "username": "patl"}, cookies)
	if rr.Code != http.StatusOK {
		t.Fatalf("SignUp() = %d %s, want a session", rr.Code, rr.Body.String())
	}
	if c := cookieNamed(rr, invitedTenantCookie); c == nil || c.Value == "" || !c.HttpOnly {
		t.Fatalf("SignUp() set the cookie %+v, want the tenant the invitation seated the person in carried to the session read, out of the page's reach", c)
	}

	f.expectSession(sessionID, created)
	read := sessionRead(f, sessionID, carried(rr))
	if got := decoded(t, read)["invitedTenant"]; got != lakesideTenant {
		t.Errorf("the first session read answers invitedTenant = %v, want %s", got, lakesideTenant)
	}
	if c := cookieNamed(read, invitedTenantCookie); c == nil || c.Value != "" {
		t.Error("the first session read left the cookie, want it deleted: the tenant is told once")
	}
	if got, told := decoded(t, sessionRead(f, sessionID, carried(read)))["invitedTenant"]; told {
		t.Errorf("a later session read answers invitedTenant = %v, want none", got)
	}
}

func authInvitedTenantByHand(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.expectSession(sessionID, &sessionstorage.SessionUser{ID: userID, Username: "pat"})
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: lakesideTenant, RoleNames: []string{"Teacher"}}); err != nil {
		t.Fatal(err)
	}
	cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))
	f.store.EXPECT().AcceptInvitation(gomock.Any(), userID, "pat", "", gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ impulse.UUID, _, _ string, inv *sessioninfo.Invitation, _ signinflow.InvitationAcceptor[mockTx]) error {
			inv.Accepted = true

			return nil
		})

	rr := f.asUser(f.auth.Invitation().Accept(), sessionID, nil, cookies)
	if rr.Code != http.StatusOK {
		t.Fatalf("Accept() = %d %s, want accepted", rr.Code, rr.Body.String())
	}
	read := sessionRead(f, sessionID, carried(rr))
	if got := decoded(t, read)["invitedTenant"]; got != lakesideTenant {
		t.Errorf("the session read after Accept() answers invitedTenant = %v, want %s", got, lakesideTenant)
	}
	if got, told := decoded(t, sessionRead(f, sessionID, carried(read)))["invitedTenant"]; told {
		t.Errorf("a later session read answers invitedTenant = %v, want none", got)
	}
}

// authInvitedTenantMade holds the landing of an invitation that makes a tenant: it
// names no tenant, so the application's acceptance hook says which tenant it made
// (Invitation.MadeTenantID), and the first session read names that tenant.
func authInvitedTenantMade(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", NewTenant: &sessioninfo.NewTenant{Name: "Lakeside"}}); err != nil {
		t.Fatal(err)
	}
	cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))
	created := &sessionstorage.SessionUser{ID: userID, Username: "patl", Email: strPtr("pat@lakeside.edu")}
	f.store.EXPECT().CreateUser(gomock.Any(), gomock.Any(), nil).Return(created, nil)
	f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) {
		// What the store and the application's hook leave on the invitation: accepted,
		// and the tenant the hook made.
		req.Invitation.Accepted = true
		req.Invitation.MadeTenantID = lakesideTenant
	}, nil)

	rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", map[string]string{"password": "correct horse", "username": "patl"}, cookies)
	if rr.Code != http.StatusOK {
		t.Fatalf("SignUp() = %d %s, want a session", rr.Code, rr.Body.String())
	}
	if c := cookieNamed(rr, invitedTenantCookie); c == nil || c.Value == "" || !c.HttpOnly {
		t.Fatalf("SignUp() set the cookie %+v, want the tenant the invitation made carried to the session read, out of the page's reach", c)
	}

	f.expectSession(sessionID, created)
	read := sessionRead(f, sessionID, carried(rr))
	if got := decoded(t, read)["invitedTenant"]; got != lakesideTenant {
		t.Errorf("the first session read answers invitedTenant = %v, want %s, the tenant the invitation made", got, lakesideTenant)
	}
	if got, told := decoded(t, sessionRead(f, sessionID, carried(read)))["invitedTenant"]; told {
		t.Errorf("a later session read answers invitedTenant = %v, want none", got)
	}
}

// authInvitedTenantMadeByHand is authInvitedTenantMade for a person who accepts while
// signed in.
func authInvitedTenantMadeByHand(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.expectSession(sessionID, &sessionstorage.SessionUser{ID: userID, Username: "pat"})
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", NewTenant: &sessioninfo.NewTenant{Name: "Lakeside"}}); err != nil {
		t.Fatal(err)
	}
	cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))
	f.store.EXPECT().AcceptInvitation(gomock.Any(), userID, "pat", "", gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ impulse.UUID, _, _ string, inv *sessioninfo.Invitation, _ signinflow.InvitationAcceptor[mockTx]) error {
			inv.Accepted = true
			inv.MadeTenantID = lakesideTenant

			return nil
		})

	rr := f.asUser(f.auth.Invitation().Accept(), sessionID, nil, cookies)
	if rr.Code != http.StatusOK {
		t.Fatalf("Accept() = %d %s, want accepted", rr.Code, rr.Body.String())
	}
	read := sessionRead(f, sessionID, carried(rr))
	if got := decoded(t, read)["invitedTenant"]; got != lakesideTenant {
		t.Errorf("the session read after Accept() answers invitedTenant = %v, want %s, the tenant the invitation made", got, lakesideTenant)
	}
}

func authInvitedTenantNone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		request  InvitationRequest
		accepted bool
	}{
		{name: "an invitation into the application", request: InvitationRequest{RoleNames: []string{"Teacher"}}, accepted: true},
		{name: "an invitation that had died by the sign-in", request: InvitationRequest{TenantID: lakesideTenant, RoleNames: []string{"Teacher"}}},
		{
			name:    "an invitation that makes a tenant, accepted by a hook that named none",
			request: InvitationRequest{NewTenant: &sessioninfo.NewTenant{Name: "Lakeside"}}, accepted: true,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			userID, sessionID := newIDs()
			f, recorder, links := newMailFixture(t, gomock.NewController(t))
			request := tt.request
			request.Email = "pat@lakeside.edu"
			if _, err := f.auth.API().Invite(context.Background(), &request); err != nil {
				t.Fatal(err)
			}
			cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))
			created := &sessionstorage.SessionUser{ID: userID, Username: "patl", Email: strPtr("pat@lakeside.edu")}
			f.store.EXPECT().CreateUser(gomock.Any(), gomock.Any(), nil).Return(created, nil)
			f.expectSignIn(sessionID, func(req *sessioninfo.NewSessionRequest) {
				req.Invitation.Accepted = tt.accepted
			}, nil)

			rr := f.serve(f.auth.Invitation().SignUp(), http.MethodPost, "/invitation/sign-up", map[string]string{"password": "correct horse", "username": "patl"}, cookies)
			if rr.Code != http.StatusOK {
				t.Fatalf("SignUp() = %d %s, want a session", rr.Code, rr.Body.String())
			}
			if c := cookieNamed(rr, invitedTenantCookie); c != nil {
				t.Errorf("SignUp() set the cookie %+v, want none: no tenant was entered by the invitation", c)
			}
			f.expectSession(sessionID, created)
			if got, told := decoded(t, sessionRead(f, sessionID, carried(rr)))["invitedTenant"]; told {
				t.Errorf("the session read answers invitedTenant = %v, want none", got)
			}
		})
	}
}

func authInvitedTenantOtherPerson(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	otherID, otherSession := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.expectSession(sessionID, &sessionstorage.SessionUser{ID: userID, Username: "pat"})
	f.expectSession(otherSession, &sessionstorage.SessionUser{ID: otherID, Username: "sam"})
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: lakesideTenant, RoleNames: []string{"Teacher"}}); err != nil {
		t.Fatal(err)
	}
	cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))
	f.store.EXPECT().AcceptInvitation(gomock.Any(), userID, "pat", "", gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ impulse.UUID, _, _ string, inv *sessioninfo.Invitation, _ signinflow.InvitationAcceptor[mockTx]) error {
			inv.Accepted = true

			return nil
		})
	rr := f.asUser(f.auth.Invitation().Accept(), sessionID, nil, cookies)
	if rr.Code != http.StatusOK {
		t.Fatalf("Accept() = %d %s, want accepted", rr.Code, rr.Body.String())
	}

	// Sam signs in on the browser Pat accepted on, before Pat's next session read.
	read := sessionRead(f, otherSession, carried(rr))
	if got, told := decoded(t, read)["invitedTenant"]; told {
		t.Errorf("another person's session read answers invitedTenant = %v, want none", got)
	}
	if c := cookieNamed(read, invitedTenantCookie); c == nil || c.Value != "" {
		t.Error("another person's session read left the cookie, want it deleted")
	}
}

func authInvitedTenantImpersonated(t *testing.T) {
	t.Parallel()
	userID, sessionID := newIDs()
	_, actingSession := newIDs()
	f, recorder, links := newMailFixture(t, gomock.NewController(t))
	f.expectSession(sessionID, &sessionstorage.SessionUser{ID: userID, Username: "pat"})
	// Sam acts as Pat on the browser Pat accepted on.
	f.store.EXPECT().Session(gomock.Any(), actingSession).DoAndReturn(func(context.Context, impulse.UUID) (*sessioninfo.SessionData, error) {
		return &sessioninfo.SessionData{
			SessionInfo:   &sessioninfo.SessionInfo{ID: actingSession, Username: "pat", UpdatedAt: time.Now()},
			UserID:        impulse.NullUUIDFromUUID(userID),
			Impersonation: &sessioninfo.Impersonation{Actor: "sam", ExpiresAt: time.Now().Add(time.Hour)},
		}, nil
	}).AnyTimes()
	if _, err := f.auth.API().Invite(context.Background(), &InvitationRequest{Email: "pat@lakeside.edu", TenantID: lakesideTenant, RoleNames: []string{"Teacher"}}); err != nil {
		t.Fatal(err)
	}
	cookies := invitationCookies(t, f, links, tokenIn(t, recorder.Messages()[0].Text, "invitation"))
	f.store.EXPECT().AcceptInvitation(gomock.Any(), userID, "pat", "", gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ impulse.UUID, _, _ string, inv *sessioninfo.Invitation, _ signinflow.InvitationAcceptor[mockTx]) error {
			inv.Accepted = true

			return nil
		})
	rr := f.asUser(f.auth.Invitation().Accept(), sessionID, nil, cookies)
	if rr.Code != http.StatusOK {
		t.Fatalf("Accept() = %d %s, want accepted", rr.Code, rr.Body.String())
	}

	acting := sessionRead(f, actingSession, carried(rr))
	if got := decoded(t, acting); got["authenticated"] != true || got["impersonation"] == nil {
		t.Fatalf("the impersonated session read = %v, want an authenticated session that acts for someone", got)
	}
	if got, told := decoded(t, acting)["invitedTenant"]; told {
		t.Errorf("an impersonated session read answers invitedTenant = %v, want none", got)
	}
	if c := cookieNamed(acting, invitedTenantCookie); c != nil {
		t.Errorf("an impersonated session read set the cookie %+v, want it left as it was for the person's own session", c)
	}
	if got := decoded(t, sessionRead(f, sessionID, carried(rr)))["invitedTenant"]; got != lakesideTenant {
		t.Errorf("the person's own session read afterwards answers invitedTenant = %v, want %s", got, lakesideTenant)
	}
}
