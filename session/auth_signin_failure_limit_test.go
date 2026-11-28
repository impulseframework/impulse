package session

import (
	"testing"
	"time"

	"github.com/impulseframework/impulse/session/internal/mockstore"
	gomock "go.uber.org/mock/gomock"
)

// TestAuthAPI_SignInFailureLimit pins how many failed sign-ins the Auth's API says lock a
// name, for the list of the failed sign-ins: the library's default where no password
// method is registered, or the method set no limit, and what AttemptLimit set where it
// did. The list asks at each query, and may ask before the password method is
// registered: asking leaves registration open, and the answer follows the registration.
func TestAuthAPI_SignInFailureLimit(t *testing.T) {
	t.Parallel()

	// newAuth is an Auth with no sign-in method registered yet.
	newAuth := func(t *testing.T) *Auth[NoCustomData, NoCustomData, mockTx] {
		t.Helper()

		store := mockstore.NewAuthStore[mockTx](gomock.NewController(t))
		store.EXPECT().CustomUserDataType().Return(nil).AnyTimes()
		store.EXPECT().SetUsernameIsEmail(gomock.Any()).AnyTimes()
		a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey, NoSuperAdministrators(), quietMail())
		if err != nil {
			t.Fatalf("NewAuth() error = %v", err)
		}

		return a
	}
	tests := []struct {
		name     string
		password bool
		options  []PasswordOption
		want     int
	}{
		{name: "no password method registered", want: 5},
		{name: "the password method registered without a limit", password: true, want: 5},
		{name: "the password method registered with the limit the application set", password: true, options: []PasswordOption{AttemptLimit(8, time.Hour, time.Hour)}, want: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newAuth(t)
			if tt.password {
				PasswordSignIn(a, tt.options...)
			}
			if got := a.API().SignInFailureLimit(); got != tt.want {
				t.Errorf("SignInFailureLimit() = %d, want %d", got, tt.want)
			}
		})
	}

	t.Run("asked before the password method is registered and again after", func(t *testing.T) {
		t.Parallel()

		a := newAuth(t)
		if got := a.API().SignInFailureLimit(); got != 5 {
			t.Errorf("SignInFailureLimit() before the password method = %d, want the default 5", got)
		}
		// A read that closed registration would make this registration panic.
		PasswordSignIn(a, AttemptLimit(8, time.Hour, time.Hour))
		if got := a.API().SignInFailureLimit(); got != 8 {
			t.Errorf("SignInFailureLimit() after the password method = %d, want the 8 it set", got)
		}
	})
}
