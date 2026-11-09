package sessionstorage

import (
	"context"
	"reflect"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	gomock "go.uber.org/mock/gomock"
)

func TestPasswordAuth_User(t *testing.T) {
	t.Parallel()
	userID := impulse.Must(impulse.NewUUID())
	tests := []struct {
		name     string
		id       impulse.UUID
		prepare  func(mockDB *Mockdb)
		wantUser *dbtype.SessionUser
		wantErr  bool
	}{
		{
			name: "success",
			id:   userID,
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().User(gomock.Any(), userID).Return(&dbtype.SessionUser{ID: userID, Username: "test"}, nil)
			},
			wantUser: &dbtype.SessionUser{ID: userID, Username: "test"},
		},
		{
			name: "failure",
			id:   userID,
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().User(gomock.Any(), userID).Return(nil, errors.New("db error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			storage := &userStorage{
				sessionStorage: sessionStorage{
					db: mockDB,
				},
			}
			if tt.prepare != nil {
				tt.prepare(mockDB)
			}
			gotUser, err := storage.User(context.Background(), tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("userStorage.User() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotUser, tt.wantUser) {
				t.Errorf("userStorage.User() = %v, want %v", gotUser, tt.wantUser)
			}
		})
	}
}

func TestPasswordAuth_UserByUserName(t *testing.T) {
	t.Parallel()
	userID := impulse.Must(impulse.NewUUID())
	tests := []struct {
		name     string
		username string
		prepare  func(mockDB *Mockdb)
		wantUser *dbtype.SessionUser
		wantErr  bool
	}{
		{
			name:     "success",
			username: "test",
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().UserByUserName(gomock.Any(), "test").Return(&dbtype.SessionUser{ID: userID, Username: "test"}, nil)
			},
			wantUser: &dbtype.SessionUser{ID: userID, Username: "test"},
		},
		{
			name:     "failure",
			username: "test",
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().UserByUserName(gomock.Any(), "test").Return(nil, errors.New("db error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			storage := &userStorage{
				sessionStorage: sessionStorage{
					db: mockDB,
				},
			}
			if tt.prepare != nil {
				tt.prepare(mockDB)
			}
			gotUser, err := storage.UserByUserName(context.Background(), tt.username)
			if (err != nil) != tt.wantErr {
				t.Errorf("userStorage.UserByUserName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotUser, tt.wantUser) {
				t.Errorf("userStorage.UserByUserName() = %v, want %v", gotUser, tt.wantUser)
			}
		})
	}
}

func TestPasswordAuth_CreateUser(t *testing.T) {
	t.Parallel()
	userID := impulse.Must(impulse.NewUUID())
	hash, err := securehash.New(securehash.Argon2()).Hash("password")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		username string
		hash     *securehash.Hash
		prepare  func(mockDB *Mockdb)
		wantUser *dbtype.SessionUser
		wantErr  bool
	}{
		{
			name:     "success",
			username: "test",
			hash:     hash,
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().CreateUser(gomock.Any(), &dbtype.InsertSessionUser{Username: "test", PasswordHash: hash}, gomock.Nil()).Return(&dbtype.SessionUser{ID: userID, Username: "test"}, nil)
			},
			wantUser: &dbtype.SessionUser{ID: userID, Username: "test"},
		},
		{
			name:     "failure",
			username: "test",
			hash:     hash,
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().CreateUser(gomock.Any(), &dbtype.InsertSessionUser{Username: "test", PasswordHash: hash}, gomock.Nil()).Return(nil, errors.New("db error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			storage := &userStorage{
				sessionStorage: sessionStorage{
					db: mockDB,
				},
			}
			if tt.prepare != nil {
				tt.prepare(mockDB)
			}
			gotUser, err := storage.CreateUser(t.Context(), &dbtype.InsertSessionUser{Username: tt.username, PasswordHash: tt.hash}, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("userStorage.CreateUser() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotUser, tt.wantUser) {
				t.Errorf("userStorage.CreateUser() = %v, want %v", gotUser, tt.wantUser)
			}
		})
	}
}

func TestPasswordAuth_SetUserUsername(t *testing.T) {
	t.Parallel()
	userID := impulse.Must(impulse.NewUUID())
	username := "<username>"
	tests := []struct {
		name     string
		id       impulse.UUID
		username string
		prepare  func(mockDB *Mockdb)
		wantErr  bool
	}{
		{
			name:     "success",
			id:       userID,
			username: username,
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().SetUserUsername(gomock.Any(), userID, username).Return(nil)
			},
		},
		{
			name:     "failure",
			id:       userID,
			username: username,
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().SetUserUsername(gomock.Any(), userID, username).Return(errors.New("db error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			storage := &userStorage{
				sessionStorage: sessionStorage{
					db: mockDB,
				},
			}
			if tt.prepare != nil {
				tt.prepare(mockDB)
			}
			if err := storage.SetUserUsername(context.Background(), tt.id, tt.username); (err != nil) != tt.wantErr {
				t.Errorf("userStorage.SetUserUsername() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPasswordAuth_SetUserPasswordHash(t *testing.T) {
	t.Parallel()
	userID := impulse.Must(impulse.NewUUID())
	hash, err := securehash.New(securehash.Argon2()).Hash("password")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		id      impulse.UUID
		hash    *securehash.Hash
		prepare func(mockDB *Mockdb)
		wantErr bool
	}{
		{
			name: "success",
			id:   userID,
			hash: hash,
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().SetUserPasswordHash(gomock.Any(), userID, hash).Return(nil)
			},
		},
		{
			name: "failure",
			id:   userID,
			hash: hash,
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().SetUserPasswordHash(gomock.Any(), userID, hash).Return(errors.New("db error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			storage := &userStorage{
				sessionStorage: sessionStorage{
					db: mockDB,
				},
			}
			if tt.prepare != nil {
				tt.prepare(mockDB)
			}
			if err := storage.SetUserPasswordHash(context.Background(), tt.id, tt.hash); (err != nil) != tt.wantErr {
				t.Errorf("userStorage.SetUserPasswordHash() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPassword_DeactivateUser(t *testing.T) {
	t.Parallel()

	someErr := errors.New("some error")
	userID, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID(): error = %v, expected: nil", err)
	}

	tests := []struct {
		name    string
		id      impulse.UUID
		mock    func(m *Mockdb)
		wantErr bool
		err     error
	}{
		{
			name: "success",
			id:   userID,
			mock: func(m *Mockdb) {
				m.EXPECT().DeactivateUser(gomock.Any(), userID).Return(nil)
			},
		},
		{
			name: "error",
			id:   userID,
			mock: func(m *Mockdb) {
				m.EXPECT().DeactivateUser(gomock.Any(), userID).Return(someErr)
			},
			wantErr: true,
			err:     someErr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockdb := NewMockdb(ctrl)
			tt.mock(mockdb)

			p := &userStorage{
				sessionStorage: sessionStorage{
					db: mockdb,
				},
			}

			if err := p.DeactivateUser(context.Background(), tt.id); (err != nil) != tt.wantErr {
				t.Errorf("userStorage.DeactivateUser() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPassword_DeleteUser(t *testing.T) {
	t.Parallel()

	someErr := errors.New("some error")
	userID, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID(): error = %v, expected: nil", err)
	}

	tests := []struct {
		name    string
		id      impulse.UUID
		mock    func(m *Mockdb)
		wantErr bool
		err     error
	}{
		{
			name: "success",
			id:   userID,
			mock: func(m *Mockdb) {
				m.EXPECT().DeleteUser(gomock.Any(), userID).Return(nil)
			},
		},
		{
			name: "error",
			id:   userID,
			mock: func(m *Mockdb) {
				m.EXPECT().DeleteUser(gomock.Any(), userID).Return(someErr)
			},
			wantErr: true,
			err:     someErr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockdb := NewMockdb(ctrl)
			tt.mock(mockdb)

			p := &userStorage{
				sessionStorage: sessionStorage{
					db: mockdb,
				},
			}

			if err := p.DeleteUser(context.Background(), tt.id); (err != nil) != tt.wantErr {
				t.Errorf("userStorage.DeleteUser() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPassword_ActivateUser(t *testing.T) {
	t.Parallel()

	someErr := errors.New("some error")
	userID, err := impulse.NewUUID()
	if err != nil {
		t.Fatalf("impulse.NewUUID(): error = %v, expected: nil", err)
	}

	tests := []struct {
		name    string
		id      impulse.UUID
		mock    func(m *Mockdb)
		wantErr bool
		err     error
	}{
		{
			name: "success",
			id:   userID,
			mock: func(m *Mockdb) {
				m.EXPECT().ActivateUser(gomock.Any(), userID).Return(nil)
			},
		},
		{
			name: "error",
			id:   userID,
			mock: func(m *Mockdb) {
				m.EXPECT().ActivateUser(gomock.Any(), userID).Return(someErr)
			},
			wantErr: true,
			err:     someErr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockdb := NewMockdb(ctrl)
			tt.mock(mockdb)

			p := &userStorage{
				sessionStorage: sessionStorage{
					db: mockdb,
				},
			}

			if err := p.ActivateUser(context.Background(), tt.id); (err != nil) != tt.wantErr {
				t.Errorf("userStorage.ActivateUser() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPassword_DestroyAllUserSessions(t *testing.T) {
	t.Parallel()

	someErr := errors.New("some error")
	username := "testuser"

	tests := []struct {
		name    string
		mock    func(m *Mockdb)
		wantErr bool
		err     error
	}{
		{
			name: "success",
			mock: func(m *Mockdb) {
				m.EXPECT().DestroyAllUserSessions(gomock.Any(), username).Return(nil)
			},
		},
		{
			name: "error",
			mock: func(m *Mockdb) {
				m.EXPECT().DestroyAllUserSessions(gomock.Any(), username).Return(someErr)
			},
			wantErr: true,
			err:     someErr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockdb := NewMockdb(ctrl)
			tt.mock(mockdb)

			p := &userStorage{
				sessionStorage: sessionStorage{
					db: mockdb,
				},
			}

			if err := p.DestroyAllUserSessions(context.Background(), username); (err != nil) != tt.wantErr {
				t.Errorf("userStorage.DestroyAllUserSessions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPasswordAuth_UpdateCustomSessionData(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.NewUUID())

	tests := []struct {
		name    string
		prepare func(mockDB *Mockdb)
		wantErr bool
	}{
		{
			name: "success passes the erased mutate through",
			prepare: func(mockDB *Mockdb) {
				// The session-exists / not-expired check lives inside the driver's
				// read-modify-write transaction, so the storage layer only delegates.
				mockDB.EXPECT().UpdateCustomSessionData(gomock.Any(), sessionID, gomock.Any()).
					DoAndReturn(func(_ context.Context, _ impulse.UUID, mutate func(any) error) error {
						// The storage layer must pass the callback through unchanged.
						data := &testCustomData{}
						if err := mutate(data); err != nil {
							return err
						}
						if data.Role != "mutated" {
							return errors.New("mutate did not run")
						}

						return nil
					})
			},
		},
		{
			name: "fails on db update error",
			prepare: func(mockDB *Mockdb) {
				mockDB.EXPECT().UpdateCustomSessionData(gomock.Any(), sessionID, gomock.Any()).Return(errors.New("db error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockDB := NewMockdb(ctrl)
			storage := &userStorage{
				sessionStorage: sessionStorage{
					db: mockDB,
				},
			}
			if tt.prepare != nil {
				tt.prepare(mockDB)
			}
			err := storage.UpdateCustomSessionData(context.Background(), sessionID, func(data any) error {
				typed, ok := data.(*testCustomData)
				if !ok {
					return errors.New("unexpected data type")
				}
				typed.Role = "mutated"

				return nil
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("userStorage.UpdateCustomSessionData() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
