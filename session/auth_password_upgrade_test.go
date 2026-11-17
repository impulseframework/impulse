package session

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_HashUpgradeFollowsTheProof pins the hash upgrade a sign-in makes: the store
// is handed the hash the password was just compared against beside the new one, so it
// writes only while the user still holds that hash, and a sign-in whose upgrade finds
// the password changed, reset or removed since the user was read goes on as it would
// have, with nothing written.
func TestAuth_HashUpgradeFollowsTheProof(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	// A hash of another algorithm than the password method's is one to upgrade.
	outdated, err := securehash.New(securehash.Bcrypt()).Hash("pw")
	if err != nil {
		t.Fatal(err)
	}
	pat := func() *sessionstorage.SessionUser {
		return &sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: outdated}
	}

	t.Run("the upgrade names the hash the sign-in compared against", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(pat(), nil)
		var written *securehash.Hash
		f.store.EXPECT().UpgradeUserPasswordHash(gomock.Any(), userID, outdated, gomock.Not(gomock.Nil())).
			DoAndReturn(func(_ context.Context, _ impulse.UUID, _, hash *securehash.Hash) error {
				written = hash

				return nil
			})

		got, err := f.auth.API().ValidateCredentials(context.Background(), "pat", "pw")
		if err != nil || got != userID {
			t.Fatalf("ValidateCredentials() = %s, %v; want the user", got, err)
		}
		if upgrade, err := f.auth.methods.compare(written, "pw"); err != nil || upgrade {
			t.Errorf("the hash written compares to the password with error = %v, upgrade = %t; want the same password under the method's own algorithm", err, upgrade)
		}
	})

	t.Run("an upgrade that finds the password changed since the user was read writes nothing, and the sign-in goes on", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(pat(), nil)
		f.store.EXPECT().UpgradeUserPasswordHash(gomock.Any(), userID, outdated, gomock.Not(gomock.Nil())).
			Return(httpio.NewConflictMessageWithError(sessionstorage.ErrPasswordChanged, "the password has changed since it was checked: check it again and retry"))

		got, err := f.auth.API().ValidateCredentials(context.Background(), "pat", "pw")
		if err != nil || got != userID {
			t.Errorf("ValidateCredentials() = %s, %v; want the user: the comparison stands, and the next sign-in upgrades the hash it finds", got, err)
		}
	})

	t.Run("an upgrade the store fails is logged, and the sign-in goes on", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(pat(), nil)
		f.store.EXPECT().UpgradeUserPasswordHash(gomock.Any(), userID, outdated, gomock.Not(gomock.Nil())).Return(errors.New("the database is away"))

		if got, err := f.auth.API().ValidateCredentials(context.Background(), "pat", "pw"); err != nil || got != userID {
			t.Errorf("ValidateCredentials() = %s, %v; want the user", got, err)
		}
	})

	t.Run("a hash that needs no upgrade is not written", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		// The mock refuses a call it was not told of: no hash is written.
		f.store.EXPECT().UserByUserName(gomock.Any(), "pat").Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: hashed(t, "pw")}, nil)

		if got, err := f.auth.API().ValidateCredentials(context.Background(), "pat", "pw"); err != nil || got != userID {
			t.Errorf("ValidateCredentials() = %s, %v; want the user", got, err)
		}
	})
}
