package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

// TestAuthAPI_RemoveSessionUserPassword pins the removal an administrator or the
// application makes: the store removes the password under its own rule, the name's
// sign-in attempts are reset, and no session of the user is ended; the store's refusal
// of a user's last means of sign-in reaches the caller as it is, and resets nothing.
func TestAuthAPI_RemoveSessionUserPassword(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	pat := func() *sessionstorage.SessionUser {
		return &sessionstorage.SessionUser{ID: userID, Username: "Pat", PasswordHash: hashed(t, "kept"), PasswordChangeRequired: true}
	}

	t.Run("the password is removed, the name's attempts are reset and the user's sessions stand", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		gomock.InOrder(
			f.store.EXPECT().User(gomock.Any(), userID).Return(pat(), nil),
			f.store.EXPECT().RemoveUserPassword(gomock.Any(), userID, gomock.Len(0)).Return(nil),
		)

		// The mock refuses a call it was not told of: no session is destroyed.
		if err := f.auth.API().RemoveSessionUserPassword(context.Background(), userID); err != nil {
			t.Fatalf("RemoveSessionUserPassword() error = %v", err)
		}
		if _, _, clears := f.counts(); clears != 1 {
			t.Errorf("the name's sign-in attempts were cleared %d times, want once", clears)
		}
	})

	t.Run("a user with no identity link keeps the password, and the name's attempts stay", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().User(gomock.Any(), userID).Return(pat(), nil)
		f.store.EXPECT().RemoveUserPassword(gomock.Any(), userID, gomock.Len(0)).Return(httpio.NewConflictMessageWithError(sessionstorage.ErrLastSignInMethod, "last"))

		err := f.auth.API().RemoveSessionUserPassword(context.Background(), userID)
		if !errors.Is(err, sessionstorage.ErrLastSignInMethod) || !httpio.HasConflict(err) || httpio.Message(err) != "last" {
			t.Errorf("RemoveSessionUserPassword() error = %v, want the store's ErrLastSignInMethod (Conflict) as it is", err)
		}
		if _, _, clears := f.counts(); clears != 0 {
			t.Errorf("the name's sign-in attempts were cleared %d times, want none: nothing was removed", clears)
		}
	})

	t.Run("a user that does not exist is not found, and nothing is removed", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().User(gomock.Any(), userID).Return(nil, httpio.NewNotFoundMessage("no user"))

		if err := f.auth.API().RemoveSessionUserPassword(context.Background(), userID); !httpio.HasNotFound(err) {
			t.Errorf("RemoveSessionUserPassword() error = %v, want NotFound", err)
		}
	})
}

// TestAuth_PasswordRemoval_linksThatSignIn pins which of a person's identity links count
// when their password is to be removed, on an administrator's route and on the person's
// own: the links a sign-in is still possible through (signsInThrough) are named to the
// store, which removes the password only while one of them stands, and a link that
// signs nobody in is not named, so a person whose every link is of a method the page no
// longer offers, or of a connection no registration takes, keeps the password, and is
// told why.
func TestAuth_PasswordRemoval_linksThatSignIn(t *testing.T) {
	t.Parallel()

	userID, sessionID := impulse.Must(impulse.NewUUID()), impulse.Must(impulse.NewUUID())
	kept := hashed(t, "kept")
	link := func(method sessioninfo.AuthMethod, connection string) *sessionstorage.SessionIdentity {
		return &sessionstorage.SessionIdentity{ID: impulse.Must(impulse.NewUUID()), UserID: userID, Method: method, Connection: connection, Subject: "sub"}
	}
	fleet, river := link(sessioninfo.MethodGoogle, "fleet"), link(sessioninfo.MethodAzure, tenantRiver)
	// Links that sign nobody in: a method nothing is registered of, the tenant of a
	// registration the page does not list, and a Workspace the registration does not name.
	okta, unlisted, elsewhere := link(sessioninfo.MethodOIDC, "okta"), link(sessioninfo.MethodAzure, tenantOther), link(sessioninfo.MethodGoogle, "other")
	lastMeans := func() error {
		return httpio.NewConflictMessageWithError(sessionstorage.ErrLastSignInMethod, "the user has no other means of sign-in: link a directory sign-in first")
	}

	tests := []struct {
		name  string
		links []*sessionstorage.SessionIdentity
		// standing are the links the store is to be named, and refused whether the store
		// then refuses, as it does when none of them stands.
		standing []impulse.UUID
		refused  bool
		// wantAdmin is what the administrator's route says of a refusal.
		wantAdmin string
	}{
		{
			name:     "a link that signs the person in is named, and the links that do not are left out",
			links:    []*sessionstorage.SessionIdentity{okta, fleet, unlisted, elsewhere},
			standing: []impulse.UUID{fleet.ID},
		},
		{
			name:     "every link that signs the person in is named",
			links:    []*sessionstorage.SessionIdentity{fleet, river},
			standing: []impulse.UUID{fleet.ID, river.ID},
		},
		{
			name:      "a person whose links all sign nobody in keeps the password",
			links:     []*sessionstorage.SessionIdentity{okta, unlisted, elsewhere},
			refused:   true,
			wantAdmin: "none of the identities linked to the user can sign them in any more, so the password is their only means of sign-in: link a directory sign-in the login page offers first",
		},
		{
			name:      "a person with no link keeps the password",
			refused:   true,
			wantAdmin: "the user has no other means of sign-in: link a directory sign-in first",
		},
	}
	for _, tt := range tests {
		named := gomock.Len(0)
		if len(tt.standing) > 0 {
			named = gomock.Eq(tt.standing)
		}
		var result error
		if tt.refused {
			result = lastMeans()
		}

		t.Run("an administrator's removal: "+tt.name, func(t *testing.T) {
			t.Parallel()
			a, store := signInMeansAuth(t)
			store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: kept}, nil)
			store.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return(tt.links, nil)
			store.EXPECT().RemoveUserPassword(gomock.Any(), userID, named).Return(result)

			err := a.API().RemoveSessionUserPassword(context.Background(), userID)
			if !tt.refused {
				if err != nil {
					t.Fatalf("RemoveSessionUserPassword() error = %v", err)
				}

				return
			}
			if !errors.Is(err, sessionstorage.ErrLastSignInMethod) || !httpio.HasConflict(err) || httpio.Message(err) != tt.wantAdmin {
				t.Errorf("RemoveSessionUserPassword() error = %v, want ErrLastSignInMethod (Conflict) saying %q", err, tt.wantAdmin)
			}
		})

		t.Run("the person's own removal: "+tt.name, func(t *testing.T) {
			t.Parallel()
			a, password, store := directoriesAuthWithPassword(t)
			a.Methods(signInMeansList("/api/user/sign-in-means", "Fleet Google", "Lakeside"))
			store.EXPECT().Session(gomock.Any(), sessionID).
				Return(&sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID)}, nil)
			store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: kept}, nil).Times(2)
			store.EXPECT().IdentitiesByUser(gomock.Any(), userID).Return(tt.links, nil)
			store.EXPECT().RemoveProvenUserPassword(gomock.Any(), userID, kept, named).Return(result)

			r := httptest.NewRequestWithContext(context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID), http.MethodPost, "/api/user/password/remove", strings.NewReader(`{"password":"kept"}`))
			r.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			password.RemoveUserPassword().ServeHTTP(rr, r)
			if !tt.refused {
				if rr.Code != http.StatusOK {
					t.Fatalf("RemoveUserPassword() = %d %s, want 200", rr.Code, rr.Body.String())
				}

				return
			}
			want := "Your password is your only way to sign in: sign in through your directory first, which links it to your account, then remove the password"
			if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), want) {
				t.Errorf("RemoveUserPassword() = %d %s, want 409 saying %q", rr.Code, rr.Body.String(), want)
			}
		})
	}
}

// TestAuthAPI_UnlinkIdentity_judgesTheOtherLinks pins what an unlink hands the store:
// the judgment the removal of a password is held to (signsInThrough), which the store
// asks about the user's other links in the transaction that unlinks. A link a sign-in
// is still possible through counts, and a link that signs nobody in does not: a method
// nothing is registered of, the tenant of a registration the page does not list, and a
// Workspace the registration does not name. So the last link that counts is not
// unlinked from a user with no password while only such links would remain.
func TestAuthAPI_UnlinkIdentity_judgesTheOtherLinks(t *testing.T) {
	t.Parallel()

	type link struct {
		method     sessioninfo.AuthMethod
		connection string
	}
	want := map[link]bool{
		{sessioninfo.MethodGoogle, "fleet"}:    true,
		{sessioninfo.MethodAzure, tenantRiver}: true,
		{sessioninfo.MethodOIDC, "okta"}:       false,
		{sessioninfo.MethodAzure, tenantOther}: false,
		{sessioninfo.MethodGoogle, "other"}:    false,
	}

	a, store := signInMeansAuth(t)
	identityID := impulse.Must(impulse.NewUUID())
	got := make(map[link]bool, len(want))
	store.EXPECT().UnlinkIdentity(gomock.Any(), identityID, gomock.Not(gomock.Nil())).
		DoAndReturn(func(_ context.Context, _ impulse.UUID, signsIn func(method sessioninfo.AuthMethod, connection string) bool) error {
			for other := range want {
				got[other] = signsIn(other.method, other.connection)
			}

			return nil
		})

	if err := a.API().UnlinkIdentity(context.Background(), identityID); err != nil {
		t.Fatalf("UnlinkIdentity() error = %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the links the store was told count as a means of sign-in (-want +got):\n%s", diff)
	}
}

// TestAuth_RemoveUserPassword pins the handler a person removes their own password
// through: it validates the session itself, as the password change does, asks for the
// password the person holds, hands the store the hash that password matched, so that
// password and no other is removed, and answers the refusals of a last means of sign-in
// and of a password that changed meanwhile in words the person can act on.
func TestAuth_RemoveUserPassword(t *testing.T) {
	t.Parallel()

	userID := impulse.Must(impulse.NewUUID())
	sessionID := impulse.Must(impulse.NewUUID())
	kept := hashed(t, "kept")
	pat := func(marked bool) *sessionstorage.SessionUser {
		return &sessionstorage.SessionUser{ID: userID, Username: "pat", PasswordHash: kept, PasswordChangeRequired: marked}
	}
	request := func(ctx context.Context, body string) *http.Request {
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/user/password/remove", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")

		return r
	}
	signedIn := func() context.Context {
		return context.WithValue(context.Background(), sessioninfo.CTXSessionID, sessionID)
	}
	session := func() *sessioninfo.SessionData {
		return &sessioninfo.SessionData{SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "pat", UpdatedAt: time.Now()}, UserID: impulse.NullUUIDFromUUID(userID)}
	}

	t.Run("the person's password is removed once they prove it, and their session is not ended", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		// The session's validation and the proof each read the user. The store is handed
		// the hash the proof was made against; the mock refuses the removal that names
		// the user alone, and a session destroyed.
		f.store.EXPECT().User(gomock.Any(), userID).Return(pat(false), nil).Times(2)
		f.store.EXPECT().RemoveProvenUserPassword(gomock.Any(), userID, kept, gomock.Len(0)).Return(nil)

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"kept"}`))
		if rr.Code != http.StatusOK {
			t.Fatalf("RemoveUserPassword() = %d %s, want 200", rr.Code, rr.Body.String())
		}
		// The password's match gave its attempt back, and the removal reset the name.
		if _, _, clears := f.counts(); clears != 2 {
			t.Errorf("the name's sign-in attempts were cleared %d times, want twice: by the password's match and by the removal", clears)
		}
	})

	t.Run("a password that changed after the person entered theirs is not removed, and the person is told to try again", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(pat(false), nil).Times(2)
		f.store.EXPECT().RemoveProvenUserPassword(gomock.Any(), userID, kept, gomock.Len(0)).
			Return(httpio.NewConflictMessageWithError(sessionstorage.ErrPasswordChanged, "the password has changed since it was checked: check it again and retry"))

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"kept"}`))
		want := "Your password has changed since you entered it, so it was not removed: try again with your current password"
		if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), want) {
			t.Errorf("RemoveUserPassword() of a password that changed meanwhile = %d %s, want 409 saying %q", rr.Code, rr.Body.String(), want)
		}
		// The password matched and gave its attempt back; the removal that was not made
		// clears nothing more.
		if _, _, clears := f.counts(); clears != 1 {
			t.Errorf("the name's sign-in attempts were cleared %d times, want once, by the password's match: nothing was removed", clears)
		}
	})

	t.Run("a person who must change their password cannot remove it instead: the session is refused as every own-account route refuses it", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		// The session's validation reads the user and finds the mark. The mock refuses
		// the read a proof would make and the removal: the password stands, and the
		// mark with it.
		f.store.EXPECT().User(gomock.Any(), userID).Return(pat(true), nil)

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"kept"}`))
		if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "Change your password to continue") {
			t.Errorf("RemoveUserPassword() under the password change mark = %d %s, want 403 saying to change the password", rr.Code, rr.Body.String())
		}
	})

	t.Run("a wrong password, or none, removes nothing", func(t *testing.T) {
		t.Parallel()
		for _, body := range []string{`{"password":"nope"}`, `{"password":""}`, `{}`} {
			f := newAuthFixture(t, gomock.NewController(t))
			f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
			f.store.EXPECT().User(gomock.Any(), userID).Return(pat(false), nil).Times(2)

			rr := httptest.NewRecorder()
			f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), body))
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Password incorrect") {
				t.Errorf("RemoveUserPassword(%s) = %d %s, want 400 saying the password is incorrect", body, rr.Code, rr.Body.String())
			}
		}
	})

	t.Run("a person who holds no password proves none, and nothing is removed", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(&sessionstorage.SessionUser{ID: userID, Username: "pat"}, nil).Times(2)

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"anything"}`))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("RemoveUserPassword() of a person with no password = %d %s, want 400", rr.Code, rr.Body.String())
		}
	})

	t.Run("a person with no directory link keeps the password, and is told what to do", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(session(), nil)
		f.store.EXPECT().User(gomock.Any(), userID).Return(pat(false), nil).Times(2)
		f.store.EXPECT().RemoveProvenUserPassword(gomock.Any(), userID, kept, gomock.Len(0)).
			Return(httpio.NewConflictMessageWithError(sessionstorage.ErrLastSignInMethod, "the user has no other means of sign-in: link a directory sign-in first"))

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"kept"}`))
		want := "Your password is your only way to sign in: sign in through your directory first, which links it to your account, then remove the password"
		if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), want) {
			t.Errorf("RemoveUserPassword() of a person with no link = %d %s, want 409 saying %q", rr.Code, rr.Body.String(), want)
		}
		// The password matched and gave its attempt back; the removal that was not made
		// clears nothing more.
		if _, _, clears := f.counts(); clears != 1 {
			t.Errorf("the name's sign-in attempts were cleared %d times, want once, by the password's match: nothing was removed", clears)
		}
	})

	t.Run("a request without a session removes nothing", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		f.store.EXPECT().Session(gomock.Any(), sessionID).Return(nil, httpio.NewNotFoundMessage("no session"))

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(signedIn(), `{"password":"kept"}`))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("RemoveUserPassword() = %d %s, want 401", rr.Code, rr.Body.String())
		}
	})

	t.Run("an impersonated session is refused: the password is the person's own to remove", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, gomock.NewController(t))
		ctx := impersonatedCtx(sessionID, "pat", &sessioninfo.Impersonation{SessionID: sessionID, Actor: "admin"})
		ctx = context.WithValue(ctx, sessioninfo.CtxUserInfo, &sessioninfo.UserInfo{ID: userID, Username: "pat"})

		rr := httptest.NewRecorder()
		f.password.RemoveUserPassword().ServeHTTP(rr, request(ctx, `{"password":"kept"}`))
		if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "not permitted in an impersonated session") {
			t.Errorf("RemoveUserPassword() = %d %s, want 403 naming the impersonated session", rr.Code, rr.Body.String())
		}
	})
}

// TestAuth_Methods_removePassword pins the route the account page is told: the path of
// the password method's RemoveUserPassword handler where the list names it, nothing
// where it does not, and a wiring mistake refused where the routes are wired.
func TestAuth_Methods_removePassword(t *testing.T) {
	t.Parallel()

	password := []AdvertisedMethod{{Name: "password", Label: "Password", Path: "/api/user/password/login"}}
	answered := func(t *testing.T, list *MethodList) map[string]any {
		t.Helper()

		rr := httptest.NewRecorder()
		registerThree(t).Methods(list).ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/user/methods", http.NoBody))
		if rr.Code != http.StatusOK {
			t.Fatalf("Methods() status = %d, want 200: %s", rr.Code, rr.Body.String())
		}

		var got map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		return got
	}

	t.Run("the list names the route, and the answer carries it", func(t *testing.T) {
		t.Parallel()
		got := answered(t, &MethodList{Methods: password, ChangePassword: "/api/user/password/change", RemovePassword: "/api/user/password/remove"})
		want := map[string]any{
			"methods":        []any{map[string]any{"name": "password", "method": "password", "kind": "password", "label": "Password", "path": "/api/user/password/login"}},
			"changePassword": "/api/user/password/change",
			"removePassword": "/api/user/password/remove",
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Methods() answer mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a list that names no route answers none", func(t *testing.T) {
		t.Parallel()
		if got := answered(t, &MethodList{Methods: password, ChangePassword: "/api/user/password/change"}); got["removePassword"] != nil {
			t.Errorf("Methods() answered removePassword = %v, want none", got["removePassword"])
		}
	})

	for _, tt := range []struct {
		name string
		list *MethodList
		want string
	}{
		{
			name: "the route off-site",
			list: &MethodList{Methods: password, ChangePassword: "/password", RemovePassword: "https://evil.example/remove"},
			want: "remove-your-password route",
		},
		{
			name: "the route without the password method",
			list: &MethodList{Methods: []AdvertisedMethod{{Name: "google", Label: "Google", Path: "/login"}}, RemovePassword: "/password/remove"},
			want: "lists no password method",
		},
	} {
		t.Run(tt.name+" is refused where the routes are wired", func(t *testing.T) {
			t.Parallel()
			a := registerThree(t)

			recovered := registrationPanic(func() { a.Methods(tt.list) })
			if msg, _ := recovered.(string); !strings.Contains(msg, tt.want) {
				t.Errorf("Methods() panic = %v, want it to contain %q", recovered, tt.want)
			}
		})
	}
}
