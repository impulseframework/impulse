package session

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/signin"
	gomock "go.uber.org/mock/gomock"
)

// TestAuthAPI_UnlinkIdentity_recorded pins the record of an identity unlinked through
// the API: done to the link's account, by who acts, naming the directory the link came
// through as the registrations name it, and by the method's own name where nothing
// names it. An id no link holds is NotFound, and nothing is unlinked.
func TestAuthAPI_UnlinkIdentity_recorded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// connectionName is the name the registration gives the link's connection, nil
		// for none.
		connectionName func(string) string
		wantDirectory  string
	}{
		{name: "a directory the registration names", connectionName: func(string) string { return "Lakeside" }, wantDirectory: "Lakeside"},
		{name: "a directory nothing names", wantDirectory: "bare"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			admin, userID, identityID := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
			f := newAuthFixture(t, gomock.NewController(t))
			f.auth.RegisterExternal(&signin.Registration[mockTx]{
				Name: "provider", Method: "bare",
				Build: func(signin.StateCookies) (*signin.Method, error) {
					return &signin.Method{
						Name: "bare", LoginURL: func() string { return "/login" }, ConnectionName: tt.connectionName,
						Complete: func(context.Context, http.ResponseWriter, *http.Request) (*signin.Verified, error) {
							return nil, errors.New("no round trip in this test")
						},
					}, nil
				},
			})
			f.store.EXPECT().IdentityByID(gomock.Any(), identityID).Return(&sessionstorage.SessionIdentity{ID: identityID, UserID: userID, Method: "bare", Connection: "c"}, nil)
			f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil)
			var got *auditlog.Record
			f.store.EXPECT().UnlinkIdentity(gomock.Any(), identityID, gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ impulse.UUID, _ func(sessioninfo.AuthMethod, string) bool, record *auditlog.Record) error {
					got = record

					return nil
				})

			if err := f.auth.API().UnlinkIdentity(adminSession(admin), identityID); err != nil {
				t.Fatalf("UnlinkIdentity() error = %v", err)
			}
			want := wantAccount(auditlog.ActIdentityUnlinked, userID, "pat")
			want.OldValue = tt.wantDirectory
			checkRecord(t, "UnlinkIdentity", got, want, byPerson(admin, "ada"))
		})
	}

	t.Run("an id no link holds", func(t *testing.T) {
		t.Parallel()

		f := newAuthFixture(t, gomock.NewController(t))
		identityID := impulse.Must(impulse.NewUUID())
		f.store.EXPECT().IdentityByID(gomock.Any(), identityID).Return(nil, httpio.NewNotFoundMessage("no such link"))

		if err := f.auth.API().UnlinkIdentity(context.Background(), identityID); !httpio.HasNotFound(err) {
			t.Errorf("UnlinkIdentity() of an id no link holds error = %v, want NotFound", err)
		}
	})
}
