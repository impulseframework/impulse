package session

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/mockstore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"go.uber.org/mock/gomock"
)

// accountWrapper is a store an application wrote over the library's: it embeds the
// library's store and declares a User of its own, which answers what a case tells it
// to and never asks the store beneath it, as a store that keeps the accounts it has
// read would.
type accountWrapper struct {
	*mockstore.AuthStore[mockTx]

	answer *sessionstorage.SessionUser
}

// User is the wrapper's own read of an account, in front of the library's.
func (w *accountWrapper) User(context.Context, impulse.UUID) (*sessionstorage.SessionUser, error) {
	return w.answer, nil
}

// TestAuth_sessionUser_heldToTheLibrarysAccount holds the account of an ordinary
// session to the library's own store where a store wraps it. The wrapper's User is the
// one asked, and its answer is the account the session names: the id that was asked
// for, the username the library's own row holds, and an account that is disabled if
// either the row or the wrapper says so. What else the wrapper answers of the account
// is taken, and an account the library's store does not hold is the wrapper's whole.
func TestAuth_sessionUser_heldToTheLibrarysAccount(t *testing.T) {
	t.Parallel()

	patID := impulse.Must(impulse.NewUUID())
	samID := impulse.Must(impulse.NewUUID())
	added := "Pat of the wrapper"

	tests := []struct {
		name string
		// own is what the library's own store holds, and ownErr its error; with
		// neither, the library's own read must not be asked.
		own    *sessionstorage.SessionUser
		ownErr error
		// answer is the wrapper's User.
		answer           *sessionstorage.SessionUser
		wantUnauthorized bool
		wantErr          bool
		wantName         string
	}{
		{
			name:     "the wrapper's answer for the account asked is taken, with what it adds",
			own:      &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			answer:   &sessionstorage.SessionUser{ID: patID, Username: "pat", Name: &added},
			wantName: added,
		},
		{
			name:             "a wrapper cannot answer with another account: the library's own row is not asked",
			answer:           &sessionstorage.SessionUser{ID: samID, Username: "sam"},
			wantUnauthorized: true,
		},
		{
			name:             "a wrapper cannot answer under another username than the library's own row holds",
			own:              &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			answer:           &sessionstorage.SessionUser{ID: patID, Username: "sam"},
			wantUnauthorized: true,
		},
		{
			name:             "a wrapper cannot keep a disabled account signed in: the library's own row says it is disabled",
			own:              &sessionstorage.SessionUser{ID: patID, Username: "pat", Disabled: true},
			answer:           &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			wantUnauthorized: true,
		},
		{
			name:             "a wrapper still disables an account the library's own row holds enabled",
			own:              &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			answer:           &sessionstorage.SessionUser{ID: patID, Username: "pat", Disabled: true},
			wantUnauthorized: true,
		},
		{
			name:     "an account the library's store does not hold is the wrapper's to answer",
			ownErr:   httpio.NewNotFoundMessagef("user id %q does not exist", patID),
			answer:   &sessionstorage.SessionUser{ID: patID, Username: "pat", Name: &added},
			wantName: added,
		},
		{
			name:    "a read of the library's own that fails is the request's failure",
			ownErr:  errors.New("the database is away"),
			answer:  &sessionstorage.SessionUser{ID: patID, Username: "pat"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newAuthFixture(t, gomock.NewController(t))
			if tt.own != nil || tt.ownErr != nil {
				f.store.EXPECT().User(gomock.Any(), patID).Return(tt.own, tt.ownErr)
			}
			f.auth.storage = &accountWrapper{AuthStore: f.store, answer: tt.answer}

			sess := &sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{ID: impulse.Must(impulse.NewUUID()), Username: "pat"},
				UserID:      impulse.NullUUIDFromUUID(patID),
			}
			got, err := f.auth.sessionUser(context.Background(), sess)
			if httpio.HasUnauthorized(err) != tt.wantUnauthorized || (err != nil) != (tt.wantUnauthorized || tt.wantErr) {
				t.Fatalf("sessionUser() error = %v, want unauthorized %v, want another error %v", err, tt.wantUnauthorized, tt.wantErr)
			}
			if err != nil {
				return
			}
			name := ""
			if got.Name != nil {
				name = *got.Name
			}
			if got.ID != patID || got.Username != "pat" || name != tt.wantName {
				t.Errorf("sessionUser() = %+v, named %q, want the account %s, pat, named %q", got, name, patID, tt.wantName)
			}
		})
	}
}
