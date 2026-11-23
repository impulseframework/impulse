package drivertest

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/internal/signinflow"
)

// NewSignInSettingsIn returns an instance's SignInSettingsIn over d: read, the driver's
// read of the sign-in settings row inside a transaction, run in a transaction of d's.
func NewSignInSettingsIn[Tx any](
	d signinflow.Driver[Tx], read func(ctx context.Context, tx Tx) (*dbtype.SignInSettings, error),
) func(ctx context.Context) (*dbtype.SignInSettings, error) {
	return func(ctx context.Context) (*dbtype.SignInSettings, error) {
		var settings *dbtype.SignInSettings
		if err := d.Transact(ctx, func(ctx context.Context, tx Tx) error {
			var err error
			settings, err = read(ctx, tx)
			if err != nil {
				return errors.Wrap(err, "SignInSettingsIn()")
			}

			return nil
		}); err != nil {
			return nil, errors.Wrap(err, "signinflow.Driver.Transact()")
		}

		return settings, nil
	}
}

func testSignInSettingsOneField(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})

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
	other := h.New(ctx, t, Auth, AuthConfig{Auth: true})
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
