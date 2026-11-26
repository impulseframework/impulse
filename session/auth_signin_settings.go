package session

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/tracer"
)

// SignInSettings is what an administrator chose, in the running application, for the
// whole Auth: whether everyone is asked for a second factor when they sign in, and for
// how many days a browser a person asked to be remembered skips the code (0 for
// browsers that are not remembered). An Auth that nobody has set them for asks everyone
// for a second factor and remembers a browser for DefaultRememberedBrowserDays.
type SignInSettings struct {
	RequireSecondFactor bool
	RememberBrowserDays int64
}

// The sign-in settings' fixed values: the one name the table's row takes, the days a
// browser is remembered for until an administrator changes them, and the most it can be
// remembered for.
const (
	SignInSettingsRowName              = dbtype.SignInSettingsName
	DefaultRememberedBrowserDays int64 = dbtype.DefaultRememberBrowserDays
	MaxRememberedBrowserDays     int64 = dbtype.MaxRememberBrowserDays
)

// SignInSettings reads the Auth's sign-in settings, with the defaults where nobody has
// set them: the second factor required, and a browser remembered for
// DefaultRememberedBrowserDays.
func (p *AuthAPI[S, U, Tx]) SignInSettings(ctx context.Context) (SignInSettings, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	settings, err := p.auth.storage.SignInSettings(ctx)
	if err != nil {
		return SignInSettings{}, errors.Wrap(err, "sessionstorage.AuthStore.SignInSettings()")
	}

	return SignInSettings{RequireSecondFactor: settings.SecondFactorRequired(), RememberBrowserDays: settings.RememberDays()}, nil
}

// SetSecondFactorRequired says, in the running application, whether everyone is asked
// for a second factor when they sign in. It is read at every sign-in, after the
// application's sign-in rule: while it is required, a sign-in the rule allows (or any,
// where the Auth has no rule) is held for the library's mailed code, unless it already
// carries a second factor (sessioninfo.AuthMethod.SecondFactor): the mailed code, a
// connection's attestation, or a step the application ran under a name of its own. A
// refusal stands, and so does a hold of the rule's own, with the factors it names,
// which turning it off never removes. It writes the one field and leaves the days a
// browser is remembered as they stand.
func (p *AuthAPI[S, U, Tx]) SetSecondFactorRequired(ctx context.Context, required bool) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := p.auth.storage.SetRequireSecondFactor(ctx, required, nil); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.SetRequireSecondFactor()")
	}

	return nil
}

// SetRememberedBrowserDays sets, in the running application, for how many days a browser
// a person asked to be remembered skips the code: 0 to MaxRememberedBrowserDays, 0 for
// browsers that are not remembered. A shorter period holds at once, for every browser
// already remembered; a longer one lengthens no browser past the end date written when
// it was remembered. Days outside that range are a bad request, and nothing is written.
// It writes the one field and leaves whether the second factor is required as it
// stands.
func (p *AuthAPI[S, U, Tx]) SetRememberedBrowserDays(ctx context.Context, days int64) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if days < 0 || days > MaxRememberedBrowserDays {
		return httpio.NewBadRequestMessage("A browser is remembered for 0 to 90 days. 0 means browsers are not remembered.")
	}
	if err := p.auth.storage.SetRememberBrowserDays(ctx, days, nil); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.SetRememberBrowserDays()")
	}

	return nil
}
