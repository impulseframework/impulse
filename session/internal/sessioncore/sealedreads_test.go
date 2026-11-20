package sessioncore

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/mockstore"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/sessionstorage/mock/mock_sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// wrappedUsers is a store an application wraps around one the library made: it embeds
// the store, so it has the library's reads of an account by promotion, and it declares
// a User and a UserByUserName of its own, as a wrapper may. Each answers another account
// than the one asked for, and asked records that it was asked.
type wrappedUsers struct {
	*mockstore.AuthStore[struct{}]
	other *sessionstorage.SessionUser
	asked []string
}

// User is the wrapper's own answer, in front of the store it embeds.
func (w *wrappedUsers) User(context.Context, impulse.UUID) (*sessionstorage.SessionUser, error) {
	w.asked = append(w.asked, "User")

	return w.other, nil
}

// UserByUserName is the wrapper's own answer, in front of the store it embeds.
func (w *wrappedUsers) UserByUserName(context.Context, string) (*sessionstorage.SessionUser, error) {
	w.asked = append(w.asked, "UserByUserName")

	return w.other, nil
}

// TestUserIdentity_theAccountIsTheStoredOne holds whom an impersonated session acts as
// to the library's own store. The account a user principal names, and whether an actor
// from another application carries the name of an account here, are read through the
// library's own reads, which a store that wraps the library's has by promotion and
// cannot replace: the wrapper's User and UserByUserName are never asked, so a session
// asked to act as one person cannot be made to act as another. A store with none of the
// library's reads answers neither.
func TestUserIdentity_theAccountIsTheStoredOne(t *testing.T) {
	t.Parallel()

	bobID := impulse.Must(impulse.NewUUID())
	mallory := &sessionstorage.SessionUser{ID: impulse.Must(impulse.NewUUID()), Username: "mallory"}

	t.Run("a user principal resolves to the account the library's store holds, and the wrapper is never asked", func(t *testing.T) {
		t.Parallel()
		inner := mockstore.NewAuthStore[struct{}](gomock.NewController(t))
		inner.EXPECT().User(gomock.Any(), bobID).Return(&sessionstorage.SessionUser{ID: bobID, Username: "bob"}, nil)
		storage := &wrappedUsers{AuthStore: inner, other: mallory}

		username, userID, err := UserIdentity(storage).User(context.Background(), accesstypes.User(bobID.String()))
		if err != nil {
			t.Fatalf("IdentityResolver.User() error = %v, want none", err)
		}
		if username != "bob" || userID != bobID {
			t.Errorf("IdentityResolver.User() = (%q, %v), want (bob, %v): the account the library's own row names", username, userID, bobID)
		}
		if len(storage.asked) != 0 {
			t.Errorf("the wrapper was asked %v, want it never asked", storage.asked)
		}
	})

	t.Run("an actor's name is looked up in the library's store, and the wrapper is never asked", func(t *testing.T) {
		t.Parallel()
		inner := mockstore.NewAuthStore[struct{}](gomock.NewController(t))
		inner.EXPECT().UserByUserName(gomock.Any(), "alice").Return(nil, httpio.NewNotFoundMessage("no such user"))
		storage := &wrappedUsers{AuthStore: inner, other: mallory}

		if err := UserIdentity(storage).Actor(context.Background(), "alice"); err != nil {
			t.Fatalf("IdentityResolver.Actor() error = %v, want none: the library's store holds no account of that name", err)
		}
		if len(storage.asked) != 0 {
			t.Errorf("the wrapper was asked %v, want it never asked", storage.asked)
		}
	})

	t.Run("a store with none of the library's reads resolves nobody", func(t *testing.T) {
		t.Parallel()
		storage := mock_sessionstorage.NewMockAuthStore[struct{}](gomock.NewController(t))

		if _, _, err := UserIdentity(storage).User(context.Background(), accesstypes.User(bobID.String())); !errors.Is(err, sessionstorage.ErrImpersonationSealed) {
			t.Errorf("IdentityResolver.User() error = %v, want %v", err, sessionstorage.ErrImpersonationSealed)
		}
		if err := UserIdentity(storage).Actor(context.Background(), "alice"); !errors.Is(err, sessionstorage.ErrImpersonationSealed) {
			t.Errorf("IdentityResolver.Actor() error = %v, want %v", err, sessionstorage.ErrImpersonationSealed)
		}
	})
}
