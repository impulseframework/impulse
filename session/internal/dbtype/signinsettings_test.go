package dbtype

import (
	"testing"
	"time"
)

// TestSignInSettings pins how a row of the sign-in settings table is read, and how no row
// is: no row requires the second factor and remembers a browser for 30 days, and a row
// answers what it says, 0 days among it.
func TestSignInSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		row          *SignInSettings
		wantRequired bool
		wantDays     int64
		wantPeriod   time.Duration
	}{
		{name: "no row requires the second factor and remembers a browser for 30 days", row: nil, wantRequired: true, wantDays: 30, wantPeriod: 30 * 24 * time.Hour},
		{name: "a row that requires it for 7 days", row: &SignInSettings{RequireSecondFactor: true, RememberBrowserDays: 7}, wantRequired: true, wantDays: 7, wantPeriod: 7 * 24 * time.Hour},
		{name: "a row that requires none and remembers no browser", row: &SignInSettings{RequireSecondFactor: false, RememberBrowserDays: 0}, wantRequired: false, wantDays: 0, wantPeriod: 0},
		{name: "a row that requires none and remembers a browser for 90 days", row: &SignInSettings{RememberBrowserDays: 90}, wantRequired: false, wantDays: 90, wantPeriod: 90 * 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.row.SecondFactorRequired(); got != tt.wantRequired {
				t.Errorf("SignInSettings.SecondFactorRequired() = %v, want %v", got, tt.wantRequired)
			}
			if got := tt.row.RememberDays(); got != tt.wantDays {
				t.Errorf("SignInSettings.RememberDays() = %d, want %d", got, tt.wantDays)
			}
			if got := tt.row.RememberPeriod(); got != tt.wantPeriod {
				t.Errorf("SignInSettings.RememberPeriod() = %v, want %v", got, tt.wantPeriod)
			}
		})
	}
}
