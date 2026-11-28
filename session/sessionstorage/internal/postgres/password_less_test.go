package postgres

import (
	"testing"

	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/drivertest"
)

// TestSessionStorageDriver_PasswordLessUser proves a password-less user (a user
// provisioned for external sign-in) round-trips on the shipped schema with the auth
// set: the user is created and read with no hash, and gains a first password.
func TestSessionStorageDriver_PasswordLessUser(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, authSources(drivertest.Auth)...)
	if err != nil {
		t.Fatalf("prepareDatabase() error = %v", err)
	}
	driver := NewSessionStorageDriver(db.Pool)
	driver.EnableAuth()

	user, err := driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "sso-only@example.com"}, nil)
	if err != nil {
		t.Fatalf("CreateUser() without a password error = %v", err)
	}
	assertHash := func(want bool) {
		t.Helper()

		got, err := driver.User(ctx, user.ID)
		if err != nil {
			t.Fatalf("User() error = %v", err)
		}
		if (got.PasswordHash != nil) != want {
			t.Errorf("User().PasswordHash = %v, want set %v", got.PasswordHash, want)
		}
	}
	assertHash(false)

	hash, err := securehash.New(securehash.Argon2()).Hash("password")
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.SetFirstUserPassword(ctx, user.ID, hash, nil); err != nil {
		t.Fatalf("SetFirstUserPassword() error = %v", err)
	}
	assertHash(true)
}
