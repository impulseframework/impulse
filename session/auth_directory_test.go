package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	"go.uber.org/mock/gomock"
)

// TestAuth_AuthenticatedDirectory proves the session read answers the organization's
// directory a session was established through, which is what a page offers the person
// to make a tenant's directory, and leaves it out for every session that came through
// none: a password, a personal account and an impersonation are offered nothing.
func TestAuth_AuthenticatedDirectory(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	sessionID := impulse.Must(impulse.NewUUID())
	for _, tt := range []struct {
		label  string
		events []sessioninfo.AuthEvent
		want   any
	}{
		{
			label:  "a sign-in through a Google Workspace answers the Workspace",
			events: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodGoogle, Connection: "acme.example"}},
			want:   map[string]any{"method": "google", "connection": "acme.example"},
		},
		{
			label:  "a sign-in through a Microsoft Entra tenant answers the tenant, whatever step followed",
			events: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure, Connection: "6f2c1b7e-3d4a-4c1e-9b2a-1f0e8d7c6b5a"}, {Method: sessioninfo.MethodEmailCode}},
			want:   map[string]any{"method": "azure", "connection": "6f2c1b7e-3d4a-4c1e-9b2a-1f0e8d7c6b5a"},
		},
		{label: "a password answers none", events: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword}}},
		{label: "any Google account answers none", events: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection}}},
		{label: "a Microsoft personal account answers none", events: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodAzure, Connection: signin.MicrosoftPersonalAccountsConnection}}},
		{label: "an impersonation answers none", events: []sessioninfo.AuthEvent{{Method: sessioninfo.MethodImpersonation, Connection: "alice@example.com"}}},
		{label: "a session that records no events answers none"},
	} {
		t.Run(tt.label, func(t *testing.T) {
			t.Parallel()
			f := newAuthFixture(t, gomock.NewController(t))
			f.store.EXPECT().Session(gomock.Any(), sessionID).Return(&sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID),
				AuthEvents: tt.events,
			}, nil)
			f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil)

			rr := httptest.NewRecorder()
			ctx := context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
			f.auth.Authenticated().ServeHTTP(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/user/session", http.NoBody))
			var body map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || rr.Code != http.StatusOK {
				t.Fatalf("Authenticated() = %d %s (%v), want 200", rr.Code, rr.Body.String(), err)
			}
			if diff := cmp.Diff(tt.want, body["directory"]); diff != "" {
				t.Errorf("Authenticated() directory mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
