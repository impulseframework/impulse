package sessionstorage

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	gomock "go.uber.org/mock/gomock"
)

// TestSessionStorage_Session_User proves the user columns and auth events a
// driver reads reach the public SessionData: the user's ID, when it authenticated,
// and its events; a session without them maps to the zero values.
func TestSessionStorage_Session_User(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())
	userID := impulse.Must(impulse.NewUUID())
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := []sessioninfo.AuthEvent{{Method: sessioninfo.MethodWorkOS, Connection: "conn_1", IDPAMR: []string{"mfa"}, At: at}}

	tests := []struct {
		name string
		row  *dbtype.SessionData
		want *sessioninfo.SessionData
	}{
		{
			name: "a user session carries its user, authentication time and events",
			row:  &dbtype.SessionData{Session: &dbtype.Session{ID: sessionID}, UserID: impulse.NullUUIDFromUUID(userID), AuthenticatedAt: &at, AuthEvents: events},
			want: &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID}, UserID: impulse.NullUUIDFromUUID(userID), AuthenticatedAt: at, AuthEvents: events},
		},
		{
			name: "a session without them carries none",
			row:  &dbtype.SessionData{Session: &dbtype.Session{ID: sessionID}},
			want: &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)

			mockDB := NewMockdb(ctrl)
			mockDB.EXPECT().Session(gomock.Any(), sessionID).Return(tt.row, nil)

			got, err := (&sessionStorage{db: mockDB}).Session(t.Context(), sessionID)
			if err != nil {
				t.Fatalf("Session() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateComparable(accesstypes.Principal{})); diff != "" {
				t.Errorf("Session() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAuth_TrustedDevice_thePeriodAsItIsNow pins the cut the store hands the driver: a
// browser stands in only when it was remembered within the days the sign-in settings
// say now, so the driver is asked for one remembered after now less that period, and
// where browsers are not remembered (0 days) the driver is asked nothing and no browser
// stands in.
func TestAuth_TrustedDevice_thePeriodAsItIsNow(t *testing.T) {
	t.Parallel()

	tokenHash := []byte("token-hash")
	userID := impulse.Must(impulse.NewUUID())
	tests := []struct {
		name string
		row  *dbtype.SignInSettings
		want time.Duration // the period the driver is asked within; 0 asks the driver nothing
	}{
		{name: "no row: 30 days", want: 30 * 24 * time.Hour},
		{name: "a row of 7 days", row: &dbtype.SignInSettings{RememberBrowserDays: 7}, want: 7 * 24 * time.Hour},
		{name: "a row of 0 days: nothing stands in", row: &dbtype.SignInSettings{RememberBrowserDays: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			store := &Auth[struct{}]{userStorage: userStorage{sessionStorage: sessionStorage{db: mockDB}}}
			mockDB.EXPECT().SignInSettings(gomock.Any()).Return(tt.row, nil).Times(2)

			before := time.Now()
			cut := func(now, rememberedAfter time.Time) {
				t.Helper()
				if now.Before(before) || now.After(time.Now()) {
					t.Errorf("now = %v, want the time of the call", now)
				}
				if got := now.Sub(rememberedAfter); got != tt.want {
					t.Errorf("now less rememberedAfter = %v, want %v", got, tt.want)
				}
			}
			device := &TrustedDevice{ID: impulse.Must(impulse.NewUUID())}
			if tt.want != 0 {
				// With no expectation, the mock fails the test on a call to the driver.
				mockDB.EXPECT().TrustedDeviceByToken(gomock.Any(), tokenHash, gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ []byte, now, rememberedAfter time.Time) (*TrustedDevice, error) {
						cut(now, rememberedAfter)

						return device, nil
					})
				mockDB.EXPECT().TrustedDeviceCount(gomock.Any(), userID, gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ impulse.UUID, now, rememberedAfter time.Time) (int, error) {
						cut(now, rememberedAfter)

						return 2, nil
					})
			}

			got, err := store.TrustedDevice(t.Context(), tokenHash)
			count, countErr := store.TrustedDeviceCount(t.Context(), userID)
			if countErr != nil {
				t.Fatalf("TrustedDeviceCount() error = %v", countErr)
			}
			if tt.want == 0 {
				if !httpio.HasNotFound(err) || got != nil {
					t.Errorf("TrustedDevice() = %v, %v, want a not-found", got, err)
				}
				if count != 0 {
					t.Errorf("TrustedDeviceCount() = %d, want 0", count)
				}

				return
			}
			if err != nil || got != device {
				t.Errorf("TrustedDevice() = %v, %v, want the driver's device", got, err)
			}
			if count != 2 {
				t.Errorf("TrustedDeviceCount() = %d, want the driver's 2", count)
			}
		})
	}
}
