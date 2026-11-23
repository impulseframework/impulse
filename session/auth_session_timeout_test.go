package session

import (
	"testing"
	"time"

	"github.com/impulseframework/impulse/session/internal/mockstore"
	gomock "go.uber.org/mock/gomock"
)

// TestAuthAPI_SessionTimeout pins how long the Auth's API says a session may sit idle,
// for a list that says which sessions are live: the library's default of ten minutes
// where the application set no timeout, and what WithSessionTimeout set where it did.
func TestAuthAPI_SessionTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options []AuthOption
		want    time.Duration
	}{
		{name: "no timeout option", options: nil, want: 10 * time.Minute},
		{name: "a timeout the application set", options: []AuthOption{WithSessionTimeout(3 * time.Minute)}, want: 3 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := mockstore.NewAuthStore[mockTx](gomock.NewController(t))
			store.EXPECT().CustomUserDataType().Return(nil).AnyTimes()
			store.EXPECT().SetUsernameIsEmail(gomock.Any()).AnyTimes()
			a, err := NewAuth[NoCustomData, NoCustomData](store, cookieKey, NoSuperAdministrators(), quietMail(), tt.options...)
			if err != nil {
				t.Fatalf("NewAuth() error = %v", err)
			}
			if got := a.API().SessionTimeout(); got != tt.want {
				t.Errorf("SessionTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}
