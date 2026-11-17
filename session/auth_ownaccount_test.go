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
// person changes their own account through are theirs alone: a session that views as
// the person, whatever its mask, is refused each one before it reads its body, and the
// store is asked for nothing but the session and its user. The address a person adds and
// verifies is where the code of their second factor is mailed, a token and an approved
// device act as them for as long as they live, and an accepted invitation gives them
// roles, so none of them is for whoever is looking at the account.
func TestAuth_ownAccountRoutes_refuseAViewOfThePerson(t *testing.T) {
	t.Parallel()

	userID, sessionID := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
	routes := []struct {
		operation string
		handler   func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler
		body      string
	}{
		{"Email().Add()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.Email().Add() }, `{"email":"mine@example.com"}`},
		{"Email().Verify()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.Email().Verify() }, `{"token":"t"}`},
		{"ApplicationTokens().Create()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.ApplicationTokens().Create() }, `{"name":"script","outlet":"droids"}`},
		{"ApplicationTokens().Revoke()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.ApplicationTokens().Revoke() }, `{"id":"` + userID.String() + `"}`},
		{"Device().Approve()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.Device().Approve() }, `{"userCode":"ABCD-EFGH"}`},
		{"Device().Deny()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.Device().Deny() }, `{"userCode":"ABCD-EFGH"}`},
		{"Invitation().Accept()", func(a *Auth[NoCustomData, NoCustomData, mockTx]) http.Handler { return a.Invitation().Accept() }, ``},
	}
	masks := map[string]accesstypes.PermissionMask{
		"a view with no mask": {},
		"a read-only view":    accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
	}
	for _, route := range routes {
		for name, mask := range masks {
			t.Run(route.operation+": "+name, func(t *testing.T) {
				t.Parallel()
				f := newAuthFixture(t, gomock.NewController(t), WithMail(&mail.Recorder{}, testMail))
				viewed := &sessioninfo.SessionData{
					SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID),
					Impersonation: &sessioninfo.Impersonation{SessionID: sessionID, Actor: "admin", Mask: mask, ExpiresAt: time.Now().Add(time.Hour)},
				}
				f.store.EXPECT().Session(gomock.Any(), sessionID).Return(viewed, nil)
				f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil)

				r := httptest.NewRequestWithContext(context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID), http.MethodPost, "/", strings.NewReader(route.body))
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
