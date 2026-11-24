package drivertest

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/internal/signinflow"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// NewSignInSettingsIn returns an instance's SignInSettingsIn over d: the sign-in flow's
// read of the sign-in settings row (signinflow.Driver.SignInSettingsIn), run in a
// transaction of d's.
func NewSignInSettingsIn[Tx any](d signinflow.Driver[Tx]) func(ctx context.Context) (*dbtype.SignInSettings, error) {
	return func(ctx context.Context) (*dbtype.SignInSettings, error) {
		var settings *dbtype.SignInSettings
		if err := d.Transact(ctx, func(ctx context.Context, tx Tx) error {
			var err error
			settings, err = d.SignInSettingsIn(ctx, tx)
			if err != nil {
				return errors.Wrap(err, "signinflow.Driver.SignInSettingsIn()")
			}

			return nil
		}); err != nil {
			return nil, errors.Wrap(err, "signinflow.Driver.Transact()")
		}

		return settings, nil
	}
}

// SeedSignInSettings writes the sign-in settings row cfg starts the instance with,
// through the driver; a harness calls it once its driver is built. It writes nothing for
// NoSettingsRow, nor where schema holds no auth migrations, whose database has no such
// table.
func SeedSignInSettings(ctx context.Context, t *testing.T, d AuthDriver, schema AuthSchema, cfg *AuthConfig) {
	t.Helper()

	if schema == WithoutAuth || cfg.Settings == NoSettingsRow {
		return
	}
	required := cfg.Settings == RequiredRow
	if err := d.SetRequireSecondFactor(ctx, required); err != nil {
		t.Fatalf("SetRequireSecondFactor(%t) error = %v", required, err)
	}
}

func testSignInSettingsOneField(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Settings: NoSettingsRow})

	// No row is what an Auth starts with: both reads answer nil.
	wantSignInSettings(ctx, t, in, "no write", nil)

	// Each write sets its one field. The first makes the row, with the other field at
	// its default; the rest leave the other field as it stands.
	steps := []struct {
		name  string
		write func() error
		want  *dbtype.SignInSettings
	}{
		{name: "SetRequireSecondFactor(false) on no row", write: func() error { return in.Driver.SetRequireSecondFactor(ctx, false) }, want: &dbtype.SignInSettings{RequireSecondFactor: false, RememberBrowserDays: 30}},
		{name: "SetRememberBrowserDays(7)", write: func() error { return in.Driver.SetRememberBrowserDays(ctx, 7) }, want: &dbtype.SignInSettings{RequireSecondFactor: false, RememberBrowserDays: 7}},
		{name: "SetRequireSecondFactor(true)", write: func() error { return in.Driver.SetRequireSecondFactor(ctx, true) }, want: &dbtype.SignInSettings{RequireSecondFactor: true, RememberBrowserDays: 7}},
	}
	for _, step := range steps {
		if err := step.write(); err != nil {
			t.Fatalf("%s error = %v", step.name, err)
		}
		wantSignInSettings(ctx, t, in, step.name, step.want)
	}

	// A first write of the days makes the row with the second factor required.
	other := h.New(ctx, t, Auth, AuthConfig{Auth: true, Settings: NoSettingsRow})
	if err := other.Driver.SetRememberBrowserDays(ctx, 0); err != nil {
		t.Fatalf("SetRememberBrowserDays(0) error = %v", err)
	}
	wantSignInSettings(ctx, t, other, "SetRememberBrowserDays(0) on no row", &dbtype.SignInSettings{RequireSecondFactor: true, RememberBrowserDays: 0})
}

// wantSignInSettings checks that a plain read and a read inside a transaction both
// answer want after the step named after, with a row's UpdatedAt set.
func wantSignInSettings(ctx context.Context, t *testing.T, in *AuthInstance, after string, want *dbtype.SignInSettings) {
	t.Helper()

	opts := cmpopts.IgnoreFields(dbtype.SignInSettings{}, "UpdatedAt")
	got, err := in.Driver.SignInSettings(ctx)
	if err != nil {
		t.Fatalf("SignInSettings() after %s error = %v", after, err)
	}
	if diff := cmp.Diff(want, got, opts); diff != "" {
		t.Errorf("SignInSettings() after %s mismatch (-want +got):\n%s", after, diff)
	}
	if got != nil && got.UpdatedAt.IsZero() {
		t.Errorf("SignInSettings() after %s UpdatedAt is zero, want the database's time at the write", after)
	}
	gotIn, err := in.SignInSettingsIn(ctx)
	if err != nil {
		t.Fatalf("SignInSettingsIn() after %s error = %v", after, err)
	}
	if diff := cmp.Diff(got, gotIn); diff != "" {
		t.Errorf("SignInSettingsIn() after %s mismatch, want what SignInSettings() reads (-SignInSettings +SignInSettingsIn):\n%s", after, diff)
	}
}

func testSignInSettingsMissingTable(ctx context.Context, t *testing.T, h *AuthHarness) {
	const table = "NoSuchSignInSettings"
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	if err := in.Driver.SetSignInSettingsTableName(table); err != nil {
		t.Fatalf("SetSignInSettingsTableName() error = %v", err)
	}

	// A missing table is never read as "no row", which would be a second factor
	// required of everyone however an administrator set it.
	_, err := in.Driver.SignInSettings(ctx)
	wantMissingTableError(t, "SignInSettings", table, err)
	_, err = in.SignInSettingsIn(ctx)
	wantMissingTableError(t, "SignInSettingsIn", table, err)
}

// heldFor checks that a sign-in of user ended in a wait for a second factor, and that
// the wait names exactly the factors given (none, for a wait that names no factor).
func heldFor(t *testing.T, err error, user *dbtype.SessionUser, factors ...sessioninfo.AuthMethod) {
	t.Helper()

	var pending *signin.PendingSignInError
	if !errors.As(err, &pending) {
		t.Fatalf("sign-in error = %v, want a PendingSignInError for %v", err, factors)
	}
	want := signin.PendingSignInError{Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(user.ID), Username: user.Username, Factors: factors}
	if diff := cmp.Diff(want, *pending, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("PendingSignInError mismatch (-want +got):\n%s", diff)
	}
}

func testSecondFactorSetting(_ context.Context, t *testing.T, h *AuthHarness) {
	tests := []struct {
		name string
		// policy is the sign-in rule's answer, nil for an Auth with no rule; row the
		// sign-in settings row the Auth holds.
		policy *signin.Decision
		row    SettingsRow
		// wantFactors are the factors the sign-in is held for; wantSession says it gets
		// a session, wantRefused that it is refused.
		wantFactors []sessioninfo.AuthMethod
		wantSession bool
		wantRefused bool
	}{
		{name: "no rule, the setting required: held for the mailed code", row: RequiredRow, wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}},
		{name: "no rule, no row: held for the mailed code, as an Auth starts", row: NoSettingsRow, wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}},
		{name: "no rule, the setting off: a session", row: NotRequiredRow, wantSession: true},
		{name: "a rule that allows, the setting required: held for the mailed code", policy: signin.Allow(), row: RequiredRow, wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}},
		{name: "a rule that allows, no row: held for the mailed code", policy: signin.Allow(), row: NoSettingsRow, wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}},
		{name: "a rule that allows, the setting off: a session", policy: signin.Allow(), row: NotRequiredRow, wantSession: true},
		{name: "a rule that holds for its own factor, the setting required: held for that factor alone", policy: signin.RequireMFA("totp"), row: RequiredRow, wantFactors: []sessioninfo.AuthMethod{"totp"}},
		{name: "a rule that holds for its own factor, the setting off: held for that factor all the same", policy: signin.RequireMFA("totp"), row: NotRequiredRow, wantFactors: []sessioninfo.AuthMethod{"totp"}},
		{name: "a rule that denies, the setting required: refused", policy: signin.Deny(""), row: RequiredRow, wantRefused: true},
		{name: "a rule that denies, the setting off: refused", policy: signin.Deny(""), row: NotRequiredRow, wantRefused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			cfg := AuthConfig{Auth: true, Settings: tt.row}
			if tt.policy != nil {
				cfg.Policy = func(context.Context, HookTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
					return tt.policy, nil
				}
			}
			in := h.New(ctx, t, Auth, cfg)
			user := createUser(ctx, t, in.Driver, "jane@lakeside.edu")
			mustLink(ctx, t, in.Driver, user.ID, lakeside("idp_jane"))

			_, _, err := signIn(ctx, in, lakeside("idp_jane"), sessioninfo.ReasonLogin)
			switch {
			case tt.wantSession:
				if err != nil {
					t.Errorf("sign-in error = %v, want a session", err)
				}
			case tt.wantRefused:
				assertRefused(t, err, sessioninfo.RefusedByPolicy, signin.ErrSignInDenied)
			default:
				heldFor(t, err, user, tt.wantFactors...)
			}
		})
	}
}

func testSecondFactorSettingTurnedOffAndOn(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Settings: RequiredRow})
	user := createUser(ctx, t, in.Driver, "jane@lakeside.edu")
	mustLink(ctx, t, in.Driver, user.ID, lakeside("idp_jane"))

	_, _, err := signIn(ctx, in, lakeside("idp_jane"), sessioninfo.ReasonLogin)
	heldFor(t, err, user, sessioninfo.MethodEmailCode)

	// The setting is read at each sign-in: turned off, the next sign-in gets its
	// session, with nothing restarted.
	if err := in.Driver.SetRequireSecondFactor(ctx, false); err != nil {
		t.Fatalf("SetRequireSecondFactor(false) error = %v", err)
	}
	if _, _, err := signIn(ctx, in, lakeside("idp_jane"), sessioninfo.ReasonLogin); err != nil {
		t.Errorf("sign-in with the setting turned off error = %v, want a session", err)
	}

	if err := in.Driver.SetRequireSecondFactor(ctx, true); err != nil {
		t.Fatalf("SetRequireSecondFactor(true) error = %v", err)
	}
	_, _, err = signIn(ctx, in, lakeside("idp_jane"), sessioninfo.ReasonLogin)
	heldFor(t, err, user, sessioninfo.MethodEmailCode)
}

func testSecondFactorRequiredPassed(_ context.Context, t *testing.T, h *AuthHarness) {
	lakesideEvent := sessioninfo.AuthEvent{Method: sessioninfo.MethodWorkOS, Connection: "conn_lakeside", IDPAMR: []string{"mfa"}}
	tests := []struct {
		name              string
		rememberedBrowser bool
		reason            sessioninfo.NewSessionReason
		events            []sessioninfo.AuthEvent
		// wantEvents are the events the session records, each at the session's
		// creation.
		wantEvents []sessioninfo.AuthEvent
	}{
		{
			name:       "the directory's attestation to a second factor passes it",
			reason:     sessioninfo.ReasonLogin,
			events:     []sessioninfo.AuthEvent{lakesideEvent, {Method: sessioninfo.MethodAttestedByEvidence, Connection: "conn_lakeside"}},
			wantEvents: []sessioninfo.AuthEvent{lakesideEvent, {Method: sessioninfo.MethodAttestedByEvidence, Connection: "conn_lakeside"}},
		},
		{
			name:              "a browser the person asked to be remembered passes it, and the session records it",
			rememberedBrowser: true,
			reason:            sessioninfo.ReasonLogin,
			wantEvents:        []sessioninfo.AuthEvent{lakesideEvent, {Method: sessioninfo.MethodTrustedDevice}},
		},
		{
			name:       "the completion that carries the mailed code gets its session",
			reason:     sessioninfo.ReasonStepUp,
			events:     []sessioninfo.AuthEvent{lakesideEvent, {Method: sessioninfo.MethodEmailCode}},
			wantEvents: []sessioninfo.AuthEvent{lakesideEvent, {Method: sessioninfo.MethodEmailCode}},
		},
		{
			name:       "the completion that carries the application's own factor gets its session",
			reason:     sessioninfo.ReasonStepUp,
			events:     []sessioninfo.AuthEvent{lakesideEvent, {Method: "totp"}},
			wantEvents: []sessioninfo.AuthEvent{lakesideEvent, {Method: "totp"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			in := h.New(ctx, t, Auth, AuthConfig{Auth: true, AuthEvents: true, Settings: RequiredRow, RememberedBrowser: tt.rememberedBrowser})
			user := createUser(ctx, t, in.Driver, "jane@lakeside.edu")
			mustLink(ctx, t, in.Driver, user.ID, lakeside("idp_jane"))

			id, _, err := signInWithEvents(ctx, in, lakeside("idp_jane"), tt.reason, tt.events)
			if err != nil {
				t.Fatalf("sign-in error = %v, want a session", err)
			}
			sess := mustSession(ctx, t, in.Driver, id)
			want := make([]sessioninfo.AuthEvent, 0, len(tt.wantEvents))
			for _, e := range tt.wantEvents {
				e.At = sess.CreatedAt
				want = append(want, e)
			}
			if diff := cmp.Diff(want, sess.AuthEvents, eventsOpts); diff != "" {
				t.Errorf("Session().AuthEvents mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func testSecondFactorRequiredNotCarried(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, AuthEvents: true, Settings: RequiredRow})
	user := createUser(ctx, t, in.Driver, "jane@lakeside.edu")
	mustLink(ctx, t, in.Driver, user.ID, lakeside("idp_jane"))

	// The sign-in's own event records no second factor, so a completion that carries
	// nothing after it is held as a first sign-in is.
	lakesideEvent := sessioninfo.AuthEvent{Method: sessioninfo.MethodWorkOS, Connection: "conn_lakeside", IDPAMR: []string{"mfa"}}
	_, _, err := signInWithEvents(ctx, in, lakeside("idp_jane"), sessioninfo.ReasonStepUp, []sessioninfo.AuthEvent{lakesideEvent})
	heldFor(t, err, user, sessioninfo.MethodEmailCode)
}

// counted is read, counting in asked each time it is read.
func counted(
	asked *atomic.Int32, read func(context.Context, HookTx, signin.Directory) ([]signin.DeclaredDirectory, error),
) func(context.Context, HookTx, signin.Directory) ([]signin.DeclaredDirectory, error) {
	return func(ctx context.Context, tx HookTx, directory signin.Directory) ([]signin.DeclaredDirectory, error) {
		asked.Add(1)

		return read(ctx, tx, directory)
	}
}

func testSecondFactorVouchedByTenant(_ context.Context, t *testing.T, h *AuthHarness) {
	lakesideDirectory := signin.Directory{Method: sessioninfo.MethodWorkOS, Connection: "conn_lakeside"}
	lakesideEvent := sessioninfo.AuthEvent{Method: sessioninfo.MethodWorkOS, Connection: "conn_lakeside", IDPAMR: []string{"mfa"}}
	vouchedEvent := sessioninfo.AuthEvent{Method: sessioninfo.MethodVouchedByTenant, Connection: "conn_lakeside"}
	tests := []struct {
		name string
		// vouches is the tenant's word on its directory; policy the sign-in rule's
		// answer, nil for an Auth with no rule; row the sign-in settings row; password
		// signs in with a password in place of the directory; roleNames are the role
		// names the sign-in's method asserted, nil for a method that synchronizes none.
		vouches   bool
		policy    *signin.Decision
		row       SettingsRow
		password  bool
		roleNames []string
		// wantEvents are the session's events, nil for a sign-in that is held for
		// wantFactors; wantUnread says the directory reader is not asked.
		wantEvents  []sessioninfo.AuthEvent
		wantFactors []sessioninfo.AuthMethod
		wantUnread  bool
	}{
		{
			name:    "the tenant vouches and the setting asks everyone: a session that records the tenant's word after the sign-in",
			vouches: true, row: RequiredRow,
			wantEvents: []sessioninfo.AuthEvent{lakesideEvent, vouchedEvent},
		},
		{
			name: "the tenant declares the directory and does not vouch: held for the mailed code",
			row:  RequiredRow, wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
		},
		{
			name:    "the tenant vouches and the rule holds for the mailed code, as a super administrator's does: held for the mailed code",
			vouches: true, policy: signin.RequireMFA(sessioninfo.MethodEmailCode), row: RequiredRow,
			wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
		},
		{
			name:    "the tenant vouches and the rule holds for its own factor: held for that factor",
			vouches: true, policy: signin.RequireMFA("totp"), row: RequiredRow,
			wantFactors: []sessioninfo.AuthMethod{"totp"},
		},
		{
			name:    "the tenant vouches and the setting is off: a session with the sign-in's own event alone, and the word is not read",
			vouches: true, row: NotRequiredRow,
			wantEvents: []sessioninfo.AuthEvent{lakesideEvent}, wantUnread: true,
		},
		{
			name:    "a password sign-in while the tenant vouches: held for the mailed code, and the word is not read",
			vouches: true, row: RequiredRow, password: true,
			wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}, wantUnread: true,
		},
		{
			name:    "a sign-in through a method whose directory writes the roles while the tenant vouches: held for the mailed code, and the word is not read",
			vouches: true, row: RequiredRow, roleNames: []string{"Teacher"},
			wantFactors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}, wantUnread: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			var asked atomic.Int32
			declared := signin.DeclaredDirectory{Tenant: "lakeside", DefaultSeat: "Member", SecondFactor: tt.vouches}
			cfg := AuthConfig{
				Auth: true, AuthEvents: true, Settings: tt.row,
				Admission: &DirectoryAdmission{Directories: counted(&asked, declaringOnly(t, lakesideDirectory, declared))},
			}
			if tt.policy != nil {
				cfg.Policy = func(context.Context, HookTx, *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
					return tt.policy, nil
				}
			}
			in := h.New(ctx, t, Auth, cfg)

			var (
				user *dbtype.SessionUser
				id   impulse.UUID
				err  error
			)
			if tt.password {
				user = passwordUser(ctx, t, in.Driver, "pat@lakeside.edu")
				_, err = passwordSignIn(ctx, in, user, sessioninfo.ReasonLogin)
			} else {
				user = createUser(ctx, t, in.Driver, "jane@lakeside.edu")
				mustLink(ctx, t, in.Driver, user.ID, lakeside("idp_jane"))
				identity := lakeside("idp_jane")
				req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: identity.Email, Identity: identity, RoleNames: tt.roleNames}
				id, err = in.SignIn(ctx, newInsertSession(identity.Email), req)
			}
			if tt.wantEvents == nil {
				heldFor(t, err, user, tt.wantFactors...)
			} else {
				if err != nil {
					t.Fatalf("sign-in error = %v, want a session", err)
				}
				sess := mustSession(ctx, t, in.Driver, id)
				want := make([]sessioninfo.AuthEvent, 0, len(tt.wantEvents))
				for _, e := range tt.wantEvents {
					e.At = sess.CreatedAt
					want = append(want, e)
				}
				if diff := cmp.Diff(want, sess.AuthEvents, eventsOpts); diff != "" {
					t.Errorf("Session().AuthEvents mismatch (-want +got):\n%s", diff)
				}
			}
			if n := asked.Load(); tt.wantUnread && n != 0 {
				t.Errorf("the directory reader was asked %d times, want none", n)
			}
		})
	}

	// The word is read at each sign-in: taken back between two sign-ins the second is
	// held, and given again the next gets its session, with nothing restarted.
	t.Run("the word is read at each sign-in", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		var vouches atomic.Bool
		vouches.Store(true)
		read := func(_ context.Context, _ HookTx, directory signin.Directory) ([]signin.DeclaredDirectory, error) {
			if directory != lakesideDirectory {
				return nil, nil
			}

			return []signin.DeclaredDirectory{{Tenant: "lakeside", DefaultSeat: "Member", SecondFactor: vouches.Load()}}, nil
		}
		in := h.New(ctx, t, Auth, AuthConfig{Auth: true, AuthEvents: true, Settings: RequiredRow, Admission: &DirectoryAdmission{Directories: read}})
		user := createUser(ctx, t, in.Driver, "jane@lakeside.edu")
		mustLink(ctx, t, in.Driver, user.ID, lakeside("idp_jane"))

		if _, _, err := signIn(ctx, in, lakeside("idp_jane"), sessioninfo.ReasonLogin); err != nil {
			t.Errorf("sign-in while the tenant vouches error = %v, want a session", err)
		}
		vouches.Store(false)
		_, _, err := signIn(ctx, in, lakeside("idp_jane"), sessioninfo.ReasonLogin)
		heldFor(t, err, user, sessioninfo.MethodEmailCode)
		vouches.Store(true)
		if _, _, err := signIn(ctx, in, lakeside("idp_jane"), sessioninfo.ReasonLogin); err != nil {
			t.Errorf("sign-in once the tenant vouches again error = %v, want a session", err)
		}
	})
}

func testSecondFactorSettingUndecidedReason(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true, Settings: RequiredRow})
	user := createUser(ctx, t, in.Driver, "jane@lakeside.edu")
	mustLink(ctx, t, in.Driver, user.ID, lakeside("idp_jane"))

	// A session that continues one already decided is not decided again, by the rule
	// or by the setting.
	if _, _, err := signIn(ctx, in, lakeside("idp_jane"), sessioninfo.ReasonRegeneration); err != nil {
		t.Errorf("sign-in for %s with the setting required error = %v, want a session", sessioninfo.ReasonRegeneration, err)
	}
}

func testTrustedDeviceRememberedAfter(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})
	pat := createUser(ctx, t, in.Driver, "pat@lakeside.edu")
	remembered := newTrustedDevice(pat.ID, tokenHash(1))
	lapsed := newTrustedDevice(pat.ID, tokenHash(2))
	lapsed.ExpiresAt = time.Now().Add(-time.Second)
	for _, d := range []*dbtype.TrustedDevice{remembered, lapsed} {
		if err := in.Driver.InsertTrustedDevice(ctx, d); err != nil {
			t.Fatalf("InsertTrustedDevice() error = %v", err)
		}
	}
	// The database stamps a device's CreatedAt as it is inserted: the cut is taken
	// either side of the stamp it read back.
	created := trustedDeviceState(ctx, t, in.Driver, remembered.TokenHash).CreatedAt
	before, after := created.Add(-time.Millisecond), created.Add(time.Millisecond)
	earliest := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	if _, err := in.Driver.TrustedDeviceByToken(ctx, remembered.TokenHash, time.Now(), before); err != nil {
		t.Errorf("TrustedDeviceByToken() remembered after a cut before it error = %v, want the device", err)
	}
	if _, err := in.Driver.TrustedDeviceByToken(ctx, remembered.TokenHash, time.Now(), after); !httpio.HasNotFound(err) {
		t.Errorf("TrustedDeviceByToken() remembered before the cut error = %v, want NotFound: the period has passed", err)
	}
	if _, err := in.Driver.TrustedDeviceByToken(ctx, lapsed.TokenHash, time.Now(), earliest); !httpio.HasNotFound(err) {
		t.Errorf("TrustedDeviceByToken() of a device past its end date error = %v, want NotFound however early the cut", err)
	}

	for _, c := range []struct {
		cut  time.Time
		want int
		why  string
	}{
		{cut: before, want: 1, why: "the device remembered after the cut, not the one past its end date"},
		{cut: after, want: 0, why: "no device remembered after the cut"},
		{cut: earliest, want: 1, why: "a device past its end date is not counted however early the cut"},
	} {
		n, err := in.Driver.TrustedDeviceCount(ctx, pat.ID, time.Now(), c.cut)
		if err != nil {
			t.Fatalf("TrustedDeviceCount() error = %v", err)
		}
		if n != c.want {
			t.Errorf("TrustedDeviceCount() with the cut at %v = %d, want %d: %s", c.cut, n, c.want, c.why)
		}
	}
}
