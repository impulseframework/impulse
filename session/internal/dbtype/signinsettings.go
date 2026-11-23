package dbtype

import "time"

// SignInSettingsName is the one name a row of the sign-in settings table takes: the table
// holds the whole Auth's settings, at most one row, and its CHECK holds it to this name.
const SignInSettingsName = "auth"

// DefaultRememberBrowserDays is how many days a remembered browser skips the second
// factor while the sign-in settings table holds no row, and MaxRememberBrowserDays the
// most days a row may say.
const (
	DefaultRememberBrowserDays int64 = 30
	MaxRememberBrowserDays     int64 = 90
)

// SignInSettings is the row of the sign-in settings table: what an administrator chose,
// in the running application, for the whole Auth. RequireSecondFactor says everyone is
// asked for a second factor when they sign in, and RememberBrowserDays for how many days
// a browser a person asked to be remembered skips the code, 0 for browsers that are not
// remembered. UpdatedAt is the database's time at the write. No row requires the second
// factor and remembers a browser for DefaultRememberBrowserDays, so a nil row answers
// those defaults.
type SignInSettings struct {
	RequireSecondFactor bool
	RememberBrowserDays int64
	UpdatedAt           time.Time
}

// SecondFactorRequired reports whether everyone is asked for a second factor when they
// sign in. No row requires it, so a nil row answers true.
func (s *SignInSettings) SecondFactorRequired() bool {
	return s == nil || s.RequireSecondFactor
}

// RememberDays is how many days a browser a person asked to be remembered skips the
// code, 0 for browsers that are not remembered. A nil row answers
// DefaultRememberBrowserDays.
func (s *SignInSettings) RememberDays() int64 {
	if s == nil {
		return DefaultRememberBrowserDays
	}

	return s.RememberBrowserDays
}

// RememberPeriod is RememberDays as a length of time: 0 for browsers that are not
// remembered.
func (s *SignInSettings) RememberPeriod() time.Duration {
	return time.Duration(s.RememberDays()) * 24 * time.Hour
}
