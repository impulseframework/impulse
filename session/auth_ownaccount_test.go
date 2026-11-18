package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"go.uber.org/mock/gomock"
)

// TestAuth_ownAccountRoutes_refuseAViewOfThePerson pins that the routes a signed-in
// person changes their own account through are theirs alone: an impersonated session,
// whatever its mask and whether it views as a person or acts as a role, is refused each
// one before it reads its body, and the store is asked for nothing but the session and
// its user. Every request carries a body that does not parse, so a handler that read it
// first would answer 400. The address a person adds and verifies is where the code of
// their second factor is mailed, a token and an approved device act as them for as long
// as they live, an accepted invitation gives them roles, and a first password signs
// in as them from then on, so none of them is for whoever is looking at the account.
func TestAuth_ownAccountRoutes_refuseAViewOfThePerson(t *testing.T) {
	t.Parallel()

	userID, sessionID := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
	routes := []struct {
		operation string
		handler   func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler
	}{
		{"Email().Add()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.Email().Add() }},
		{"Email().Verify()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.Email().Verify() }},
		{"ApplicationTokens().Create()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.ApplicationTokens().Create() }},
		{"ApplicationTokens().Revoke()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.ApplicationTokens().Revoke() }},
		{"Device().Approve()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.Device().Approve() }},
		{"Device().Deny()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.Device().Deny() }},
		{"Invitation().Accept()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.Invitation().Accept() }},
		{"FirstPassword().SendCode()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.FirstPassword().SendCode() }},
		{"FirstPassword().Set()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.FirstPassword().Set() }},
	}
	readOnly := accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read)
	sessions := map[string]sessioninfo.Impersonation{
		"a view of the person with no mask": {Principal: accesstypes.UserPrincipal(accesstypes.User(userID.String()))},
		"a read-only view of the person":    {Principal: accesstypes.UserPrincipal(accesstypes.User(userID.String())), Mask: readOnly},
		"a session that acts as a role":     {Principal: accesstypes.RolePrincipal("Auditor")},
	}
	for _, route := range routes {
		for name, impersonation := range sessions {
			t.Run(route.operation+": "+name, func(t *testing.T) {
				t.Parallel()
				f := newAuthFixture(t, gomock.NewController(t), WithMail(&mail.Recorder{}, testMail))
				impersonation.SessionID, impersonation.Actor, impersonation.ExpiresAt = sessionID, "admin", time.Now().Add(time.Hour)
				viewed := &sessioninfo.SessionData{
					SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID),
					Impersonation: &impersonation,
				}
				f.store.EXPECT().Session(gomock.Any(), sessionID).Return(viewed, nil)
				// A session that acts as a role belongs to the actor, read by name.
				if impersonation.Principal.IsRole() {
					f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(&sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil)
				} else {
					f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil)
				}

				r := httptest.NewRequestWithContext(context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID), http.MethodPost, "/", strings.NewReader(`{"this does not parse`))
				r.Header.Set("Content-Type", "application/json")
				rr := httptest.NewRecorder()
				route.handler(f.auth).ServeHTTP(rr, r)

				want := route.operation + " is not permitted in an impersonated session"
				if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), want) {
					t.Errorf("%s in a session that views as the person = %d %s, want 403 saying %q", route.operation, rr.Code, rr.Body.String(), want)
				}
			})
		}
	}
}
