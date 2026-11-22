package dbtype

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

func TestNewSessionUserColumns_browser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		browser string
		want    *string
	}{
		{name: "the request's browser becomes the column", browser: "Chrome on Linux", want: strPtr("Chrome on Linux")},
		{name: "no browser is null", browser: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := NewSessionUserColumns(&sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Browser: tt.browser}).Browser
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("NewSessionUserColumns().Browser mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
