package sessionstorage

import (
	"context"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	gomock "go.uber.org/mock/gomock"
)

type testCustomData struct{ Role string }

func Test_sessionStorage_NewSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		username   string
		prepare    func(*Mockdb)
		wantErr    bool
		expectedID impulse.UUID
	}{
		{
			name:     "successful session creation",
			username: "test_user",
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().
					InsertSession(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, insertSession *dbtype.InsertSession, req *sessioninfo.NewSessionRequest) (impulse.UUID, error) {
						if insertSession.Username != "test_user" {
							return impulse.NilUUID, errors.Newf("unexpected username %q", insertSession.Username)
						}
						if req.Reason != sessioninfo.ReasonPreauth || req.Username != "test_user" {
							return impulse.NilUUID, errors.Newf("unexpected request %+v", req)
						}

						return impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")), nil
					}).
					Times(1)
			},
			expectedID: impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
		},
		{
			name:     "failed session creation",
			username: "test_user",
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().
					InsertSession(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(impulse.NilUUID, errors.New("insert failed")).
					Times(1)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			storage := &sessionStorage{
				db: mockDB,
			}

			if tt.prepare != nil {
				tt.prepare(mockDB)
			}

			id, err := storage.NewSession(context.Background(), tt.username, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewSession() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if id != tt.expectedID {
				t.Errorf("NewSession() id = %v, expectedID = %v", id, tt.expectedID)
			}
		})
	}
}

func Test_sessionStorage_Session(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sessionID  impulse.UUID
		prepare    func(*Mockdb)
		wantErr    bool
		expectedSI *sessioninfo.SessionData
	}{
		{
			name:      "successful session retrieval",
			sessionID: impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().
					Session(gomock.Any(), gomock.Any()).
					Return(&dbtype.SessionData{
						Session: &dbtype.Session{
							ID:        impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
							Username:  "test_user",
							CreatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
							UpdatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
							Expired:   false,
						},
					}, nil).
					Times(1)
			},
			expectedSI: &sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{
					ID:        impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
					Username:  "test_user",
					CreatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
					UpdatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
					Expired:   false,
				},
			},
		},
		{
			name:      "successful session retrieval with custom data",
			sessionID: impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().
					Session(gomock.Any(), gomock.Any()).
					Return(&dbtype.SessionData{
						Session: &dbtype.Session{
							ID:        impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
							Username:  "test_user",
							CreatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
							UpdatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
							Expired:   false,
						},
						CustomData: map[string]any{"Role": "admin"},
					}, nil).
					Times(1)
			},
			expectedSI: &sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{
					ID:        impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
					Username:  "test_user",
					CreatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
					UpdatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
					Expired:   false,
				},
				CustomData: map[string]any{"Role": "admin"},
			},
		},
		{
			name:      "successful session retrieval with decoded custom data",
			sessionID: impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().
					Session(gomock.Any(), gomock.Any()).
					Return(&dbtype.SessionData{
						Session: &dbtype.Session{
							ID:        impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
							Username:  "test_user",
							CreatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
							UpdatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
							Expired:   false,
						},
						CustomData: testCustomData{Role: "admin"},
					}, nil).
					Times(1)
			},
			expectedSI: &sessioninfo.SessionData{
				SessionInfo: &sessioninfo.SessionInfo{
					ID:        impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
					Username:  "test_user",
					CreatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
					UpdatedAt: impulse.Must(time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")),
					Expired:   false,
				},
				CustomData: testCustomData{Role: "admin"},
			},
		},
		{
			sessionID: impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().
					Session(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("session not found")).
					Times(1)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			storage := &sessionStorage{
				db: mockDB,
			}

			if tt.prepare != nil {
				tt.prepare(mockDB)
			}

			si, err := storage.Session(context.Background(), tt.sessionID)
			if (err != nil) != tt.wantErr {
				t.Errorf("Session() error = %v, wantErr = %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.expectedSI, si, principalComparer); diff != "" {
				t.Errorf("Session() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_sessionStorage_UpdateSessionActivity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sessionID impulse.UUID
		prepare   func(*Mockdb)
		wantErr   bool
	}{
		{
			name:      "successful session activity update",
			sessionID: impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().
					UpdateSessionActivity(gomock.Any(), gomock.Any()).
					Return(nil).
					Times(1)
			},
		},
		{
			name:      "failed session activity update",
			sessionID: impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().
					UpdateSessionActivity(gomock.Any(), gomock.Any()).
					Return(errors.New("update failed")).
					Times(1)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			storage := &sessionStorage{
				db: mockDB,
			}

			if tt.prepare != nil {
				tt.prepare(mockDB)
			}

			err := storage.UpdateSessionActivity(context.Background(), tt.sessionID)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateSessionActivity() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func Test_sessionStorage_DestroySession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sessionID impulse.UUID
		prepare   func(*Mockdb)
		wantErr   bool
	}{
		{
			name:      "successful session destruction",
			sessionID: impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().
					DestroySession(gomock.Any(), gomock.Any()).
					Return(nil).
					Times(1)
			},
		},
		{
			name:      "failed session destruction",
			sessionID: impulse.Must(impulse.UUIDFromString("123e4567-e89b-12d3-a456-426614174000")),
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().
					DestroySession(gomock.Any(), gomock.Any()).
					Return(errors.New("destroy failed")).
					Times(1)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			storage := &sessionStorage{
				db: mockDB,
			}

			if tt.prepare != nil {
				tt.prepare(mockDB)
			}

			err := storage.DestroySession(context.Background(), tt.sessionID)
			if (err != nil) != tt.wantErr {
				t.Errorf("DestroySession() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
