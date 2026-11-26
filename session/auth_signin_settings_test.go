package session

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"go.uber.org/mock/gomock"
)

// TestAuthAPI_SignInSettings pins the read of the sign-in settings: the row as it
// stands, and the defaults where nobody has set them.
func TestAuthAPI_SignInSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  *sessionstorage.SignInSettings
		want SignInSettings
	}{
		{name: "no row: the second factor required, a browser remembered for 30 days", want: SignInSettings{RequireSecondFactor: true, RememberBrowserDays: 30}},
		{name: "a row: as it stands", row: &sessionstorage.SignInSettings{RequireSecondFactor: false, RememberBrowserDays: 7}, want: SignInSettings{RequireSecondFactor: false, RememberBrowserDays: 7}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newAuthFixture(t, gomock.NewController(t))
			f.signInSettings = tt.row
			got, err := f.auth.API().SignInSettings(t.Context())
			if err != nil {
				t.Fatalf("SignInSettings() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("SignInSettings() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestAuthAPI_SetSecondFactorRequired pins that the setter writes the one field through
// the store.
func TestAuthAPI_SetSecondFactorRequired(t *testing.T) {
	t.Parallel()

	for _, required := range []bool{false, true} {
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().SetRequireSecondFactor(gomock.Any(), required, aRecord()).Return(nil)
		if err := f.auth.API().SetSecondFactorRequired(t.Context(), required); err != nil {
			t.Errorf("SetSecondFactorRequired(%t) error = %v", required, err)
		}
	}
}

// TestAuthAPI_SetRememberedBrowserDays pins the range of the days a browser is
// remembered for: 0 to 90 are written, and days outside it are a bad request that
// writes nothing.
func TestAuthAPI_SetRememberedBrowserDays(t *testing.T) {
	t.Parallel()

	for _, days := range []int64{0, 7, MaxRememberedBrowserDays} {
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().SetRememberBrowserDays(gomock.Any(), days, aRecord()).Return(nil)
		if err := f.auth.API().SetRememberedBrowserDays(t.Context(), days); err != nil {
			t.Errorf("SetRememberedBrowserDays(%d) error = %v", days, err)
		}
	}

	const want = "A browser is remembered for 0 to 90 days. 0 means browsers are not remembered."
	for _, days := range []int64{-1, MaxRememberedBrowserDays + 1} {
		// The mock fails the test on a SetRememberBrowserDays call: none is expected.
		f := newAuthFixture(t, gomock.NewController(t))
		err := f.auth.API().SetRememberedBrowserDays(t.Context(), days)
		if !httpio.HasBadRequest(err) || httpio.Message(err) != want {
			t.Errorf("SetRememberedBrowserDays(%d) error = %v (message %q), want a bad request saying %q", days, err, httpio.Message(err), want)
		}
	}
}

// TestAuth_emailCodeState_rememberOffered pins that the step's status offers to
// remember the browser while browsers are remembered, and not where the days are 0.
func TestAuth_emailCodeState_rememberOffered(t *testing.T) {
	t.Parallel()

	const address = "pat@lakeside.edu"
	userID := impulse.Must(impulse.NewUUID())
	for _, tt := range []struct {
		days int64
		want bool
	}{
		{days: DefaultRememberedBrowserDays, want: true},
		{days: 1, want: true},
		{days: 0, want: false},
	} {
		f, _, _ := newMailFixture(t, gomock.NewController(t))
		f.signInSettings = &sessionstorage.SignInSettings{RequireSecondFactor: true, RememberBrowserDays: tt.days}
		holds := address
		f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: address, Email: &holds}, nil).AnyTimes()
		pending := &pendingState{
			ID: impulse.Must(impulse.NewUUID()), Step: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(userID),
			Identity: sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: userID.String()},
		}
		expectFactorCode(f, pending)

		got, err := f.auth.emailCodeState(context.Background(), pending)
		if err != nil {
			t.Fatalf("emailCodeState() with %d days error = %v", tt.days, err)
		}
		if got.RememberOffered != tt.want {
			t.Errorf("emailCodeState() with %d days = rememberOffered %v, want %v", tt.days, got.RememberOffered, tt.want)
		}
	}
}
