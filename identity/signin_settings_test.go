package identity

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
)

// newSettingsManager builds a manager of an auth that registers the methods, over the
// store.
func newSettingsManager(t *testing.T, store Store, methods []session.RegisteredMethod) *Manager {
	t.Helper()

	m, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, fakeSessions{methods: methods})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m
}

// passwordAlone is an auth that registers the password method and no other.
func passwordAlone() []session.RegisteredMethod {
	return registeredMethods()[:1]
}

// TestManager_SignInSettings pins the Sign-in page's two lines in each state the row
// can be in, the second factor first, both carrying the row's fields, and the defaults
// the session library reads where there is no row, for an auth that registers the
// password alone.
func TestManager_SignInSettings(t *testing.T) {
	t.Parallel()

	const (
		required    = "Everyone is asked for a code mailed to their address when they sign in, unless their directory already asked for one or this browser is remembered."
		notRemember = "Everyone is asked for a code mailed to their address when they sign in, unless their directory already asked for one."
		notRequired = "Not required: people sign in with a password alone. A super administrator is always asked for a code."
	)
	tests := []struct {
		name           string
		row            *SignInSettingsRow
		wantSecond     string
		wantRemembered string
		wantRequired   bool
		wantDays       int64
	}{
		{
			name: "required, a browser remembered for days", row: &SignInSettingsRow{RequireSecondFactor: true, RememberBrowserDays: 14},
			wantSecond: required, wantRemembered: "A remembered browser skips the code for 14 days.", wantRequired: true, wantDays: 14,
		},
		{
			name: "required, browsers not remembered", row: &SignInSettingsRow{RequireSecondFactor: true, RememberBrowserDays: 0},
			wantSecond: notRemember, wantRemembered: "Browsers are not remembered.", wantRequired: true, wantDays: 0,
		},
		{
			name: "not required, a browser remembered for one day", row: &SignInSettingsRow{RequireSecondFactor: false, RememberBrowserDays: 1},
			wantSecond: notRequired, wantRemembered: "A remembered browser skips the code for 1 day.", wantRequired: false, wantDays: 1,
		},
		{
			name: "not required, browsers not remembered", row: &SignInSettingsRow{RequireSecondFactor: false, RememberBrowserDays: 0},
			wantSecond: notRequired, wantRemembered: "Browsers are not remembered.", wantRequired: false, wantDays: 0,
		},
		{
			name: "no row: the defaults", row: nil,
			wantSecond: required, wantRemembered: "A remembered browser skips the code for 30 days.", wantRequired: true, wantDays: session.DefaultRememberedBrowserDays,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newSettingsManager(t, &fakeStore{settingsRow: tt.row}, passwordAlone())
			var got []SignInSetting
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				got, err = m.SignInSettings(ctx, txn)

				return err
			}); err != nil {
				t.Fatalf("SignInSettings() error = %v", err)
			}
			want := []SignInSetting{
				{Name: SettingSecondFactor, Position: 1, Says: tt.wantSecond, RequireSecondFactor: tt.wantRequired, RememberBrowserDays: tt.wantDays},
				{Name: SettingRememberedBrowser, Position: 2, Says: tt.wantRemembered, RequireSecondFactor: tt.wantRequired, RememberBrowserDays: tt.wantDays},
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("SignInSettings() (-want +got):\n%s", diff)
			}
		})
	}

	// The defaults are the session library's: the day count is its constant.
	if session.DefaultRememberedBrowserDays != 30 {
		t.Errorf("session.DefaultRememberedBrowserDays = %d, and the no-row case above says 30 days", session.DefaultRememberedBrowserDays)
	}
}

// TestManager_secondFactorOff pins how the second factor's line while it is not
// required, and the warning before it is turned off, say people then sign in, by what
// the auth registers: a method whose directory writes the roles (the session library
// lets it stand alone on its Auth), methods none of which is the password, and anything
// else. The line while the second factor is required reads the same whatever the auth
// registers.
func TestManager_secondFactorOff(t *testing.T) {
	t.Parallel()

	const (
		required = "Everyone is asked for a code mailed to their address when they sign in, unless their directory already asked for one or this browser is remembered."

		directoryWritesLine        = "Not required: people sign in through their directory alone, a super administrator among them."
		directoryWritesConsequence = "People will sign in through their directory alone, a super administrator among them."
		directoryLine              = "Not required: people sign in through their directory alone. A super administrator is always asked for a code."
		directoryConsequence       = "People will sign in through their directory alone."
		passwordLine               = "Not required: people sign in with a password alone. A super administrator is always asked for a code."
		passwordConsequence        = "People will sign in with a password alone."
	)
	methods := registeredMethods()
	password, workspace, entra, partners := methods[0], methods[1], methods[3], methods[4]
	tests := []struct {
		name            string
		methods         []session.RegisteredMethod
		wantLine        string
		wantConsequence string
	}{
		{
			name: "a directory that writes the roles", methods: []session.RegisteredMethod{entra},
			wantLine: directoryWritesLine, wantConsequence: directoryWritesConsequence,
		},
		{
			name: "directories whose roles are the application's, and no password", methods: []session.RegisteredMethod{workspace, partners},
			wantLine: directoryLine, wantConsequence: directoryConsequence,
		},
		{
			name: "the password beside a directory whose roles are the application's", methods: []session.RegisteredMethod{password, workspace},
			wantLine: passwordLine, wantConsequence: passwordConsequence,
		},
		{
			name: "no method at all", methods: nil,
			wantLine: passwordLine, wantConsequence: passwordConsequence,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			says := func(row *SignInSettingsRow) string {
				t.Helper()

				m := newSettingsManager(t, &fakeStore{settingsRow: row}, tt.methods)
				var got []SignInSetting
				if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					var err error
					got, err = m.SignInSettings(ctx, txn)

					return err
				}); err != nil {
					t.Fatalf("SignInSettings() error = %v", err)
				}

				return got[0].Says
			}
			if got := says(&SignInSettingsRow{RequireSecondFactor: false, RememberBrowserDays: 14}); got != tt.wantLine {
				t.Errorf("SignInSettings() while not required says %q, want %q", got, tt.wantLine)
			}
			if got := says(&SignInSettingsRow{RequireSecondFactor: true, RememberBrowserDays: 14}); got != required {
				t.Errorf("SignInSettings() while required says %q, want %q", got, required)
			}

			m := newSettingsManager(t, &fakeStore{}, tt.methods)
			var warning *resource.Warning
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				warning, err = m.StopRequiringSecondFactorWarning(ctx, txn)

				return err
			}); err != nil {
				t.Fatalf("StopRequiringSecondFactorWarning() error = %v", err)
			}
			if diff := cmp.Diff(&resource.Warning{Consequence: tt.wantConsequence, Confirm: "Stop requiring it"}, warning); diff != "" {
				t.Errorf("StopRequiringSecondFactorWarning() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRememberLengths pins the lengths the list offers, in its order, each with its
// days, and that each is one the session library takes.
func TestRememberLengths(t *testing.T) {
	t.Parallel()

	want := []RememberLength{
		{Name: "Not remembered", Days: 0},
		{Name: "1 day", Days: 1},
		{Name: "7 days", Days: 7},
		{Name: "14 days", Days: 14},
		{Name: "30 days", Days: 30},
		{Name: "60 days", Days: 60},
		{Name: "90 days", Days: 90},
	}
	if diff := cmp.Diff(want, RememberLengths()); diff != "" {
		t.Errorf("RememberLengths() (-want +got):\n%s", diff)
	}
	for _, l := range RememberLengths() {
		if l.Days < 0 || l.Days > session.MaxRememberedBrowserDays {
			t.Errorf("RememberLengths(): %s is %d days, which the session library refuses", l.Name, l.Days)
		}
	}
}

// TestManager_signInSettingsWrites pins the three writes: each reaches the store with
// its one field, a setting that already says so is written again, the warning writes
// nothing and says the two strings for an auth that registers the password alone, and a
// length the list does not offer is refused with nothing written.
func TestManager_signInSettingsWrites(t *testing.T) {
	t.Parallel()

	store := &fakeStore{settingsRow: &SignInSettingsRow{RequireSecondFactor: true, RememberBrowserDays: 30}}
	m := newSettingsManager(t, store, passwordAlone())
	var warning *resource.Warning
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if err := m.RequireSecondFactor(ctx, txn); err != nil {
			return err
		}
		var err error
		if warning, err = m.StopRequiringSecondFactorWarning(ctx, txn); err != nil {
			return err
		}
		if err := m.StopRequiringSecondFactor(ctx, txn); err != nil {
			return err
		}
		if err := m.StopRequiringSecondFactor(ctx, txn); err != nil {
			return err
		}
		if err := m.SetRememberedBrowsers(ctx, txn, "7 days"); err != nil {
			return err
		}

		return m.SetRememberedBrowsers(ctx, txn, "Not remembered")
	}); err != nil {
		t.Fatalf("the writes error = %v", err)
	}
	want := []string{
		"set require second factor on=true",
		"set require second factor on=false",
		"set require second factor on=false",
		"set remember browser days=7",
		"set remember browser days=0",
	}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	wantWarning := &resource.Warning{Consequence: "People will sign in with a password alone.", Confirm: "Stop requiring it"}
	if diff := cmp.Diff(wantWarning, warning); diff != "" {
		t.Errorf("StopRequiringSecondFactorWarning() (-want +got):\n%s", diff)
	}

	store = &fakeStore{}
	m = newSignUpManager(t, store)
	for _, length := range []string{"3 days", "30", "", "30 Days"} {
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.SetRememberedBrowsers(ctx, txn, length)
		})
		if !httpio.HasBadRequest(err) || httpio.Message(err) != "That is not one of the lengths a browser can be remembered for. Pick one from the list." {
			t.Errorf("SetRememberedBrowsers(%q) error = %v, want a bad request in the refusal's sentence", length, err)
		}
	}
	if len(store.writes) != 0 {
		t.Errorf("a refused length wrote %q, want nothing", store.writes)
	}
}
