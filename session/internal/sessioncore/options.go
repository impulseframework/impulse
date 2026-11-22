package sessioncore

import (
	"time"

	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/basesession"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

// The option interfaces of the session types. Each is sealed by an unexported marker
// method, so only the option types below satisfy them; the root package aliases them
// and builds its options from these types.
type (
	// AuthOption configures an Auth session.
	AuthOption interface{ isAuthOption() }
	// PreauthOption configures a Preauth session.
	PreauthOption interface{ isPreauthOption() }
	// PasswordOption configures the password method.
	PasswordOption interface{ isPasswordOption() }
)

// AuthOptionMarker makes the type that embeds it an AuthOption: the root package's own
// Auth settings option embeds it.
type AuthOptionMarker struct{}

func (AuthOptionMarker) isAuthOption() {}

// CookieOption is a cookie client option. It configures every session type.
type CookieOption internalcookie.Option

func (CookieOption) isAuthOption()     {}
func (CookieOption) isPreauthOption()  {}
func (CookieOption) isPasswordOption() {}

// BaseSessionOption configures the base session. It configures every session type.
//
// It is an opaque value: a struct whose one field is unexported, so a program outside
// the session library can pass one on and can do nothing else with it. It cannot write
// one, convert a function into one, read the function inside or call it, and no type
// the program can name appears in it. The option was a function over the base session
// before, and that type was the way in: a generic function instantiated with it
// (func capture[O ~func(P), P any]() O) had the compiler hand the program the base
// session's type, and the option it returned was called with the session the
// constructor had built, from which the program started an impersonated session of
// its own choosing around the application's declaration. NewBaseSessionOption alone
// makes an option that sets something, and only the session library's directory can
// import it.
type BaseSessionOption struct {
	set func(*basesession.BaseSession)
}

// NewBaseSessionOption returns the option that hands the base session under
// construction to set. The session package's With functions are built on it.
func NewBaseSessionOption(set func(*basesession.BaseSession)) BaseSessionOption {
	return BaseSessionOption{set: set}
}

func (BaseSessionOption) isAuthOption()     {}
func (BaseSessionOption) isPreauthOption()  {}
func (BaseSessionOption) isPasswordOption() {}

// apply sets what the option sets on the base session. It reports false for the zero
// option, which no With function made: a program reaches it with a declared variable
// (var option session.BaseSessionOption), and it sets nothing.
func (o BaseSessionOption) apply(base *basesession.BaseSession) bool {
	if o.set == nil {
		return false
	}
	o.set(base)

	return true
}

// PasswordSettings are the password method's settings: how passwords are hashed,
// whether a hash made with an older algorithm is upgraded at login, the attempt limit
// failed attempts are held to, and how strong a password must be to be set.
type PasswordSettings struct {
	Hasher      *securehash.SecureHasher
	AutoUpgrade bool
	Attempts    dbtype.SignInAttemptLimit
	// MinimumStrength is how strong a password must be to be set, in bits of the
	// library's estimate of how hard it is to guess.
	MinimumStrength int
	// UsernameIsEmail says the username is the user's email address: the users table
	// derives the address from the username, a rename is refused, and a verified
	// address is written as the username.
	UsernameIsEmail bool
}

// DefaultAttemptLimit is the attempt limit before any option: five failed attempts on
// a name within fifteen minutes lock the name for fifteen minutes.
var DefaultAttemptLimit = dbtype.SignInAttemptLimit{Failures: 5, Window: 15 * time.Minute, Lock: 15 * time.Minute}

// DefaultMinimumStrength is the minimum strength before any option, in bits of the
// library's estimate of how hard a password is to guess.
const DefaultMinimumStrength = 30

// DefaultPasswordSettings returns the settings before any option: Argon2, upgrading,
// the default attempt limit, the default minimum strength.
func DefaultPasswordSettings() PasswordSettings {
	return PasswordSettings{Hasher: securehash.New(securehash.Argon2()), AutoUpgrade: true, Attempts: DefaultAttemptLimit, MinimumStrength: DefaultMinimumStrength}
}

// Apply applies opt when it is a password settings option and reports whether it was
// one; a cookie or base session option is not, and is left to the caller.
func (s *PasswordSettings) Apply(opt PasswordOption) bool {
	o, ok := opt.(passwordOption)
	if ok {
		o(s)
	}

	return ok
}

// passwordOption is a password settings option. It operates on the non-generic
// settings struct so one option applies to every instantiation.
type passwordOption func(*PasswordSettings)

func (passwordOption) isPasswordOption() {}

// AutoUpgradeHashes controls whether password hashes are upgraded at login. (default: true)
func AutoUpgradeHashes(a bool) PasswordOption {
	return passwordOption(func(s *PasswordSettings) {
		s.AutoUpgrade = a
	})
}

// HashAlgorithm controls the hashing algorithm. (default: securehash.Argon2())
func HashAlgorithm(hasher securehash.HashAlgorithm) PasswordOption {
	return passwordOption(func(s *PasswordSettings) {
		s.Hasher = securehash.New(hasher)
	})
}

// AttemptLimit sets the attempt limit: failures failed attempts on a name within
// window lock the name for lock. (default: DefaultAttemptLimit)
func AttemptLimit(failures int, window, lock time.Duration) PasswordOption {
	return passwordOption(func(s *PasswordSettings) {
		s.Attempts = dbtype.SignInAttemptLimit{Failures: failures, Window: window, Lock: lock}
	})
}

// MinimumPasswordStrength sets how strong a password must be to be set, in bits of the
// library's estimate of how hard it is to guess. (default: DefaultMinimumStrength)
func MinimumPasswordStrength(bits int) PasswordOption {
	return passwordOption(func(s *PasswordSettings) {
		s.MinimumStrength = bits
	})
}

// UsernameIsEmail declares that the username is the user's email address. (default: the
// two are apart)
func UsernameIsEmail() PasswordOption {
	return passwordOption(func(s *PasswordSettings) {
		s.UsernameIsEmail = true
	})
}
