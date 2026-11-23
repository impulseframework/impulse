package identity

import (
	"context"
	"slices"
	"strconv"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// What every sign-in asks for.
//
// Whether everyone is asked for a second factor when they sign in, and for how long a
// browser a person asked to be remembered skips it, are two choices an administrator
// makes in the running application, once for the whole auth. They are the one row of
// the auth's sign-in settings table, which the session library reads in the sign-in's
// own transaction; an auth with no row asks everyone, and remembers a browser for
// session.DefaultRememberedBrowserDays. The rules here are the ones the choices are made
// under: the second factor is required or not, and nothing is refused either way; a
// browser is remembered for one of the lengths the list offers (RememberLengths) and no
// other. The methods the generator writes over them carry the grant that says who may
// make the choices, and the generated patch keeps who made each and when.

// SignInSettingsRow is the row of the sign-in settings table, as the rules read and
// write it.
type SignInSettingsRow struct {
	// RequireSecondFactor says everyone is asked for a second factor when they sign in.
	RequireSecondFactor bool
	// RememberBrowserDays is for how many days a browser a person asked to be remembered
	// skips the second factor, 0 for browsers that are not remembered.
	RememberBrowserDays int64
}

// The names of the two lines the Sign-in page lists the settings as.
const (
	// SettingSecondFactor is the line that says whether everyone is asked for a second
	// factor.
	SettingSecondFactor = "second-factor"
	// SettingRememberedBrowser is the line that says for how long a remembered browser
	// skips it.
	SettingRememberedBrowser = "remembered-browser"
)

// SignInSetting is one line of the sign-in settings, as the Sign-in page lists them:
// each says one of the two choices in words, and both carry the row's two fields, which
// the page's actions read.
type SignInSetting struct {
	// Name is SettingSecondFactor or SettingRememberedBrowser.
	Name string
	// Position is the line's place on the page: the second factor 1, the remembered
	// browser 2.
	Position int64
	// Says is the line's choice as a sentence a person reads.
	Says string
	// RequireSecondFactor and RememberBrowserDays are the row's, the same on both lines.
	RequireSecondFactor bool
	RememberBrowserDays int64
}

// RememberLength is one length of time a browser can be remembered for, as the list an
// administrator picks from names it.
type RememberLength struct {
	// Name is the length as the request names it, and as the list shows it.
	Name string
	// Days is the length in days, 0 for browsers that are not remembered.
	Days int64
}

// RememberLengths lists the lengths of time a browser can be remembered for, shortest
// first, beginning with browsers that are not remembered: what the list an
// administrator picks from offers, and the names SetRememberedBrowsers takes.
func RememberLengths() []RememberLength {
	return []RememberLength{
		{Name: "Not remembered", Days: 0},
		{Name: "1 day", Days: 1},
		{Name: "7 days", Days: 7},
		{Name: "14 days", Days: 14},
		{Name: "30 days", Days: 30},
		{Name: "60 days", Days: 60},
		{Name: "90 days", Days: 90},
	}
}

// SignInSettings lists the auth's sign-in settings as the Sign-in page's two lines, the
// second factor first, from the row or, where there is none, from the defaults the
// session library reads in its place: the second factor required, and a browser
// remembered for session.DefaultRememberedBrowserDays. reader is the caller's
// transaction or the application's client: the row is read as committed.
func (m *Manager) SignInSettings(ctx context.Context, reader resource.ReadOnlyTransaction) ([]SignInSetting, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	row, err := m.store.SignInSettingsRow(ctx, reader)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.SignInSettingsRow()")
	}
	if row == nil {
		row = &SignInSettingsRow{RequireSecondFactor: true, RememberBrowserDays: session.DefaultRememberedBrowserDays}
	}

	return []SignInSetting{
		{
			Name: SettingSecondFactor, Position: 1, Says: m.secondFactorSays(row),
			RequireSecondFactor: row.RequireSecondFactor, RememberBrowserDays: row.RememberBrowserDays,
		},
		{
			Name: SettingRememberedBrowser, Position: 2, Says: rememberedBrowserSays(row.RememberBrowserDays),
			RequireSecondFactor: row.RequireSecondFactor, RememberBrowserDays: row.RememberBrowserDays,
		},
	}, nil
}

// secondFactorSays is the second factor's line in words. While the second factor is not
// required, the line says how people then sign in, by what the auth registers
// (withoutSecondFactor).
func (m *Manager) secondFactorSays(row *SignInSettingsRow) string {
	switch {
	case !row.RequireSecondFactor:
		line, _ := m.withoutSecondFactor()

		return line
	case row.RememberBrowserDays > 0:
		return "Everyone is asked for a code mailed to their address when they sign in, unless their directory already asked for one or this browser is remembered."
	default:
		return "Everyone is asked for a code mailed to their address when they sign in, unless their directory already asked for one."
	}
}

// withoutSecondFactor says how people sign in while the second factor is not required,
// by what the auth registers, judged in this order: a method whose directory writes the
// roles, where the directory is the authority on who is a super administrator and the
// library asks them for no code of its own (the session library lets such a method
// stand alone on its Auth); methods none of which is the password; and anything else, a
// password method or no method at all. line is the second factor's line on the Sign-in
// page, and consequence the warning's before the setting is turned off. It reads
// nothing: the methods are the ones the code registered.
func (m *Manager) withoutSecondFactor() (line, consequence string) {
	registered := m.sessions.SignInMethods()
	switch {
	case slices.ContainsFunc(registered, func(method session.RegisteredMethod) bool { return method.SyncsRoles }):
		return "Not required: people sign in through their directory alone, a super administrator among them.",
			"People will sign in through their directory alone, a super administrator among them."
	case len(registered) > 0 && !slices.ContainsFunc(registered, func(method session.RegisteredMethod) bool { return method.Method == sessioninfo.MethodPassword }):
		return "Not required: people sign in through their directory alone. A super administrator is always asked for a code.",
			"People will sign in through their directory alone."
	default:
		return "Not required: people sign in with a password alone. A super administrator is always asked for a code.",
			"People will sign in with a password alone."
	}
}

// rememberedBrowserSays is the remembered browser's line in words.
func rememberedBrowserSays(days int64) string {
	switch days {
	case 0:
		return "Browsers are not remembered."
	case 1:
		return "A remembered browser skips the code for 1 day."
	default:
		return "A remembered browser skips the code for " + strconv.FormatInt(days, 10) + " days."
	}
}

// RequireSecondFactor buffers an administrator's choice that everyone is asked for a
// second factor when they sign in, in the caller's transaction: the one field of the
// row, with the days a browser is remembered left as they stand. The session library
// reads the row at every sign-in, so the choice holds from the commit on, with no
// restart. A setting that already requires it is written again, and nothing is refused.
func (m *Manager) RequireSecondFactor(ctx context.Context, txn resource.ReadWriteTransaction) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.store.SetRequireSecondFactor(ctx, txn, true); err != nil {
		return errors.Wrap(err, "identity.Store.SetRequireSecondFactor()")
	}

	return nil
}

// StopRequiringSecondFactorWarning says what no longer requiring a second factor would
// do, in the words a person reads before they confirm it, and the words for the button
// that does it: how people will then sign in, by what the auth registers. It writes
// nothing.
func (m *Manager) StopRequiringSecondFactorWarning(ctx context.Context, _ resource.ReadWriteTransaction) (*resource.Warning, error) {
	_, span := tracer.Start(ctx)
	defer span.End()

	_, consequence := m.withoutSecondFactor()

	return &resource.Warning{
		Consequence: consequence,
		Names:       nil,
		Also:        nil,
		Confirm:     "Stop requiring it",
	}, nil
}

// StopRequiringSecondFactor buffers an administrator's choice that nobody is asked for a
// second factor by the setting, in the caller's transaction: the one field of the row,
// with the days a browser is remembered left as they stand. From the commit on, a
// sign-in the application's rule allows gets its session with no code; a super
// administrator is still asked, except where a directory writes the auth's roles, and so
// is anyone the rule holds for a factor of its own. A setting that already says so is
// written again, and nothing is refused.
func (m *Manager) StopRequiringSecondFactor(ctx context.Context, txn resource.ReadWriteTransaction) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.store.SetRequireSecondFactor(ctx, txn, false); err != nil {
		return errors.Wrap(err, "identity.Store.SetRequireSecondFactor()")
	}

	return nil
}

// SetRememberedBrowsers buffers an administrator's choice of how long a browser a person
// asked to be remembered skips the second factor, in the caller's transaction: the days
// of the length named (RememberLengths), in the one field of the row, with whether the
// second factor is required left as it stands. A shorter length holds at once, for every
// browser already remembered; a longer one lengthens no browser past the end it was
// remembered until. A name that is not one of the lengths is refused, and nothing is
// written.
func (m *Manager) SetRememberedBrowsers(ctx context.Context, txn resource.ReadWriteTransaction, length string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	for _, l := range RememberLengths() {
		if l.Name != length {
			continue
		}
		if err := m.store.SetRememberBrowserDays(ctx, txn, l.Days); err != nil {
			return errors.Wrap(err, "identity.Store.SetRememberBrowserDays()")
		}

		return nil
	}

	return httpio.NewBadRequestMessage("That is not one of the lengths a browser can be remembered for. Pick one from the list.")
}
