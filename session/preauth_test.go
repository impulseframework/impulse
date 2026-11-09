package session

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/cookie"
	"github.com/impulseframework/impulse/session/internal/basesession"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/mock/mock_cookie"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage/mock/mock_sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

func TestPreauthAPI_Login(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		username   string
		customData []*testData
		prepare    func(*mock_sessionstorage.MockPreauthStore, *mock_cookie.MockHandler)
		wantErr    bool
		expectedID impulse.UUID
	}{
		{
			name:     "successful session creation and cookie set",
			username: "test_user",
			prepare: func(mockStorage *mock_sessionstorage.MockPreauthStore, mockCookies *mock_cookie.MockHandler) {
				// Mock the session creation
				mockStorage.EXPECT().
					NewSession(gomock.Any(), "test_user", gomock.Nil()).
					Return(impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")), nil).
					Times(1)

				// Simulate cookie setting
				mockCookies.EXPECT().
					NewAuthCookie(gomock.Any(), true, gomock.Any()).
					DoAndReturn(func(w http.ResponseWriter, _ bool, sessionID impulse.UUID) (*cookie.Values, error) {
						http.SetCookie(w, &http.Cookie{
							Name:     "auth",
							Value:    sessionID.String(),
							Path:     "/",
							Secure:   true,
							HttpOnly: true,
							SameSite: http.SameSiteStrictMode,
						})
						return cookie.NewValues().SetString(internalcookie.SessionID, sessionID.String()), nil
					}).
					Times(1)

				mockCookies.EXPECT().
					CreateXSRFTokenCookie(gomock.Any(), gomock.Any()).
					Return().
					Times(1)
			},
			expectedID: impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
		},
		{
			name:       "successful session creation with caller-supplied custom data",
			username:   "test_user",
			customData: []*testData{{Tenant: "tenant-1"}},
			prepare: func(mockStorage *mock_sessionstorage.MockPreauthStore, mockCookies *mock_cookie.MockHandler) {
				mockStorage.EXPECT().
					NewSession(gomock.Any(), "test_user", gomock.Any()).
					DoAndReturn(func(_ context.Context, _ string, customData any) (impulse.UUID, error) {
						if diff := cmp.Diff(&testData{Tenant: "tenant-1"}, customData); diff != "" {
							return impulse.NilUUID, errors.New("unexpected customData: " + diff)
						}

						return impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")), nil
					}).
					Times(1)

				mockCookies.EXPECT().
					NewAuthCookie(gomock.Any(), true, gomock.Any()).
					DoAndReturn(func(_ http.ResponseWriter, _ bool, sessionID impulse.UUID) (*cookie.Values, error) {
						return cookie.NewValues().SetString(internalcookie.SessionID, sessionID.String()), nil
					}).
					Times(1)

				mockCookies.EXPECT().
					CreateXSRFTokenCookie(gomock.Any(), gomock.Any()).
					Return().
					Times(1)
			},
			expectedID: impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
		},
		{
			name:     "failed session creation",
			username: "test_user",
			prepare: func(mockStorage *mock_sessionstorage.MockPreauthStore, _ *mock_cookie.MockHandler) {
				// Simulate a failure in session creation
				mockStorage.EXPECT().
					NewSession(gomock.Any(), "test_user", gomock.Nil()).
					Return(impulse.NilUUID, errors.New("storage error")).
					Times(1)
			},
			wantErr: true,
		},
		{
			name:       "fails when more than one customData value is provided",
			username:   "test_user",
			customData: []*testData{{Tenant: "tenant-1"}, {Tenant: "tenant-2"}},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Setup the mock controller
			ctrl := gomock.NewController(t)
			mockStorage := newPreauthStoreMock(ctrl)
			mockCookies := mock_cookie.NewMockHandler(ctrl)

			// Prepare the mock expectations
			if tt.prepare != nil {
				tt.prepare(mockStorage, mockCookies)
			}

			// Setup request and response recorder
			w := httptest.NewRecorder()

			// Create the PreauthSession instance with mocked dependencies
			preauth := &Preauth[testData]{
				storage: mockStorage,
				baseSession: &basesession.BaseSession{
					CookieHandler: mockCookies,
					Storage:       mockStorage,
				},
			}

			// Call Login and capture the result
			id, err := preauth.API().Login(context.Background(), w, tt.username, tt.customData...)

			// Validate the results
			if (err != nil) != tt.wantErr {
				t.Errorf("Login() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if id != tt.expectedID && !tt.wantErr {
				t.Errorf("Login() id = %v, expectedID = %v", id, tt.expectedID)
			}

			// Ensure cookies are only set on success
			if tt.wantErr {
				resp := w.Result()
				defer resp.Body.Close()
				if len(resp.Cookies()) > 0 {
					t.Error("expected no cookies to be set on failure, but found some")
				}
			}
		})
	}
}

func TestPreauthAPI_UpdateCustomSessionData(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())

	tests := []struct {
		name    string
		prepare func(*mock_sessionstorage.MockPreauthStore)
		wantErr bool
	}{
		{
			name: "success mutating through the erased callback",
			prepare: func(storage *mock_sessionstorage.MockPreauthStore) {
				storage.EXPECT().UpdateCustomSessionData(gomock.Any(), sessionID, gomock.Any()).
					DoAndReturn(func(_ context.Context, _ impulse.UUID, mutate func(data any) error) error {
						data := &testData{Tenant: "tenant-1"}
						if err := mutate(data); err != nil {
							return err
						}
						if data.Tenant != "tenant-2" {
							return errors.New("typed mutate callback did not apply: " + data.Tenant)
						}

						return nil
					})
			},
		},
		{
			name: "fails on storage error",
			prepare: func(storage *mock_sessionstorage.MockPreauthStore) {
				storage.EXPECT().UpdateCustomSessionData(gomock.Any(), sessionID, gomock.Any()).Return(errors.New("db error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)

			storage := newPreauthStoreMock(ctrl)
			preauth := &Preauth[testData]{storage: storage, baseSession: &basesession.BaseSession{Storage: storage}}

			if tt.prepare != nil {
				tt.prepare(storage)
			}

			err := preauth.API().UpdateCustomSessionData(t.Context(), sessionID, func(data *testData) error {
				data.Tenant = "tenant-2"

				return nil
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("PreauthAPI.UpdateCustomSessionData() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPreauthAPI_CustomData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		customData any
		wantErr    bool
		want       testData
	}{
		{
			name:       "returns the typed custom session data",
			customData: &testData{Tenant: "tenant-1"},
			want:       testData{Tenant: "tenant-1"},
		},
		{
			name:    "errors when the session has no custom data",
			wantErr: true,
		},
		{
			name:       "errors on a type mismatch",
			customData: &otherTestData{Partner: "partner-1"},
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			preauth := &Preauth[testData]{baseSession: &basesession.BaseSession{}}
			ctx := context.WithValue(t.Context(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{},
				CustomData:  tt.customData,
			})

			got, err := preauth.API().CustomData(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("PreauthAPI.CustomData() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("PreauthAPI.CustomData() mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
