package sessioncore

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/session/internal/basesession"
	"github.com/impulseframework/impulse/session/internal/testkey"
	"github.com/impulseframework/impulse/session/sessionstorage/mock/mock_sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// timeoutOption sets the session timeout the way session.WithSessionTimeout does.
func timeoutOption(d time.Duration) BaseSessionOption {
	return NewBaseSessionOption(func(b *basesession.BaseSession) {
		b.SessionTimeout = d
	})
}

func TestNewBaseSession_SessionTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options []BaseSessionOption
		want    time.Duration
		wantErr string
	}{
		{name: "the default builds", want: DefaultSessionTimeout},
		{name: "30 days builds", options: []BaseSessionOption{timeoutOption(30 * 24 * time.Hour)}, want: 30 * 24 * time.Hour},
		{
			name:    "30 days and a nanosecond is refused",
			options: []BaseSessionOption{timeoutOption(30*24*time.Hour + time.Nanosecond)},
			wantErr: fmt.Sprintf(
				"a session timeout of %s is longer than 30 days, and a session's row is removed 30 days after its last activity: "+
					"pass session.WithSessionTimeout a timeout of 30 days or less", 30*24*time.Hour+time.Nanosecond),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := mock_sessionstorage.NewMockBaseStore(gomock.NewController(t))

			got, _, err := NewBaseSession(store, testkey.CookieKey, tt.options)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewBaseSession() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("NewBaseSession() error = %v", err)
			}
			if got.SessionTimeout != tt.want {
				t.Errorf("NewBaseSession().SessionTimeout = %v, want %v", got.SessionTimeout, tt.want)
			}
		})
	}
}
