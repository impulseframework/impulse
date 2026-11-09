package postgres

import (
	"reflect"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

// userTestData matches the users_test/custom_user_data UserCustomData table.
type userTestData struct {
	Locale     *string `db:"Locale"`
	Theme      *string `db:"Theme"`
	LoginCount *int64  `db:"LoginCount"`
}

// oidcUserTestData matches the sessions_test/oidc_users OIDCUserData table.
type oidcUserTestData struct {
	Email       *string `db:"Email"`
	DisplayName *string `db:"DisplayName"`
	Theme       *string `db:"Theme"`
}

// sessionUserRefData matches the sessions_test/oidc_users SessionCustomData table.
type sessionUserRefData struct {
	UserIDRef *string `db:"UserIdRef"`
}

var (
	dataUserID   = impulse.Must(impulse.UUIDFromString("27b43588-b743-4133-8730-e0439065a844"))
	noDataUserID = impulse.Must(impulse.UUIDFromString("54918893-2342-4621-8673-79520a84b84f"))
)

func userDataConfig() *CustomUserDataConfig {
	return &CustomUserDataConfig{
		TableName: "UserCustomData",
		Codec:     mustCodec(reflect.TypeFor[userTestData]()),
	}
}

func TestSessionStorageDriver_CreateUser_CustomUserData(t *testing.T) {
	t.Parallel()

	hash, err := securehash.New(securehash.Argon2()).Hash("password")
	if err != nil {
		t.Fatal(err)
	}

	sourceURL := []string{"file://../../../schema/postgresql/migrations", "file://testdata/users_test/custom_user_data"}

	tests := []struct {
		name           string
		withConfig     bool
		customData     any
		wantErr        bool
		postAssertions []string
	}{
		{
			name:       "user and custom data row land together",
			withConfig: true,
			customData: &userTestData{Locale: ptr("fr-FR"), LoginCount: ptr(int64(1))},
			postAssertions: []string{
				`SELECT COUNT(*) = 1 FROM "SessionUsers" WHERE "Username" = 'newUser'`,
				`SELECT COUNT(*) = 1 FROM "UserCustomData" d JOIN "SessionUsers" u ON d."UserId" = u."Id"
					WHERE u."Username" = 'newUser' AND d."Locale" = 'fr-FR' AND d."LoginCount" = 1 AND d."Theme" IS NULL`,
			},
		},
		{
			name:       "custom data without a config is rejected before any insert",
			withConfig: false,
			customData: &userTestData{Locale: ptr("fr-FR")},
			wantErr:    true,
			postAssertions: []string{
				`SELECT COUNT(*) = 0 FROM "SessionUsers" WHERE "Username" = 'newUser'`,
			},
		},
		{
			name:       "nil custom data performs a plain insert",
			withConfig: true,
			postAssertions: []string{
				`SELECT COUNT(*) = 1 FROM "SessionUsers" WHERE "Username" = 'newUser'`,
				`SELECT COUNT(*) = 0 FROM "UserCustomData" d JOIN "SessionUsers" u ON d."UserId" = u."Id" WHERE u."Username" = 'newUser'`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			conn, err := prepareDatabase(ctx, t, sourceURL...)
			if err != nil {
				t.Fatalf("prepareDatabase() error = %v, wantErr %v", err, false)
			}
			c := NewSessionStorageDriver(conn.Pool)
			if tt.withConfig {
				c.SetCustomUserData(userDataConfig())
			}

			_, err = c.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "newUser", PasswordHash: hash}, tt.customData)
			if (err != nil) != tt.wantErr {
				t.Errorf("SessionStorageDriver.CreateUser() error = %v, wantErr %v", err, tt.wantErr)
			}
			runAssertions(ctx, t, conn.Pool, tt.postAssertions)
		})
	}
}

func TestSessionStorageDriver_CustomUserData(t *testing.T) {
	t.Parallel()

	sourceURL := []string{"file://../../../schema/postgresql/migrations", "file://testdata/users_test/custom_user_data"}

	tests := []struct {
		name       string
		withConfig bool
		userID     impulse.UUID
		want       any
		wantErr    bool
	}{
		{
			name:       "existing row scans into U",
			withConfig: true,
			userID:     dataUserID,
			want:       &userTestData{Locale: ptr("en-AU"), Theme: ptr("dark"), LoginCount: ptr(int64(3))},
		},
		{
			name:       "user without a row yields zero-value U",
			withConfig: true,
			userID:     noDataUserID,
			want:       &userTestData{},
		},
		{
			name:    "no config is an error",
			userID:  dataUserID,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			conn, err := prepareDatabase(ctx, t, sourceURL...)
			if err != nil {
				t.Fatalf("prepareDatabase() error = %v, wantErr %v", err, false)
			}
			c := NewSessionStorageDriver(conn.Pool)
			if tt.withConfig {
				c.SetCustomUserData(userDataConfig())
			}

			got, err := c.CustomUserData(ctx, tt.userID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SessionStorageDriver.CustomUserData() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("SessionStorageDriver.CustomUserData() mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestSessionStorageDriver_UpdateCustomUserData(t *testing.T) {
	t.Parallel()

	sourceURL := []string{"file://../../../schema/postgresql/migrations", "file://testdata/users_test/custom_user_data"}

	tests := []struct {
		name           string
		withConfig     bool
		userID         impulse.UUID
		mutate         func(data any) error
		wantErr        bool
		wantErrMsg     string
		postAssertions []string
	}{
		{
			name:       "read-modify-write preserves untouched fields",
			withConfig: true,
			userID:     dataUserID,
			mutate: func(data any) error {
				typed, ok := data.(*userTestData)
				if !ok {
					return errors.Newf("unexpected mutate data type %T", data)
				}
				typed.Theme = ptr("light")

				return nil
			},
			postAssertions: []string{
				`SELECT COUNT(*) = 1 FROM "UserCustomData"
					WHERE "UserId" = '27b43588-b743-4133-8730-e0439065a844'
					AND "Theme" = 'light' AND "Locale" = 'en-AU' AND "LoginCount" = 3`,
			},
		},
		{
			name:       "creates the row for a user without one",
			withConfig: true,
			userID:     noDataUserID,
			mutate: func(data any) error {
				typed, ok := data.(*userTestData)
				if !ok {
					return errors.Newf("unexpected mutate data type %T", data)
				}
				if typed.Locale != nil || typed.Theme != nil || typed.LoginCount != nil {
					return errors.New("expected zero-value U for a user without a row")
				}
				typed.Locale = ptr("de-DE")

				return nil
			},
			postAssertions: []string{
				`SELECT COUNT(*) = 1 FROM "UserCustomData"
					WHERE "UserId" = '54918893-2342-4621-8673-79520a84b84f' AND "Locale" = 'de-DE'`,
			},
		},
		{
			name:       "unknown user aborts with not found",
			withConfig: true,
			userID:     impulse.Must(impulse.UUIDFromString("99999999-9999-9999-9999-999999999999")),
			mutate:     func(any) error { return nil },
			wantErr:    true,
			wantErrMsg: `user id "99999999-9999-9999-9999-999999999999" does not exist`,
			postAssertions: []string{
				`SELECT COUNT(*) = 1 FROM "UserCustomData"`,
			},
		},
		{
			name:       "mutate error aborts with nothing written",
			withConfig: true,
			userID:     dataUserID,
			mutate:     func(any) error { return errors.New("mutate failure") },
			wantErr:    true,
			postAssertions: []string{
				`SELECT COUNT(*) = 1 FROM "UserCustomData"
					WHERE "UserId" = '27b43588-b743-4133-8730-e0439065a844' AND "Theme" = 'dark'`,
			},
		},
		{
			name:    "no config is an error",
			userID:  dataUserID,
			mutate:  func(any) error { return nil },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			conn, err := prepareDatabase(ctx, t, sourceURL...)
			if err != nil {
				t.Fatalf("prepareDatabase() error = %v, wantErr %v", err, false)
			}
			c := NewSessionStorageDriver(conn.Pool)
			if tt.withConfig {
				c.SetCustomUserData(userDataConfig())
			}

			err = c.UpdateCustomUserData(ctx, tt.userID, tt.mutate)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SessionStorageDriver.UpdateCustomUserData() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrMsg != "" && httpio.Message(err) != tt.wantErrMsg {
				t.Errorf("SessionStorageDriver.UpdateCustomUserData() error message = %s, want %s", httpio.Message(err), tt.wantErrMsg)
			}
			runAssertions(ctx, t, conn.Pool, tt.postAssertions)
		})
	}
}

func TestSessionStorageDriver_DeleteUser_CustomUserDataCascade(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	conn, err := prepareDatabase(ctx, t, "file://../../../schema/postgresql/migrations", "file://testdata/users_test/custom_user_data")
	if err != nil {
		t.Fatalf("prepareDatabase() error = %v, wantErr %v", err, false)
	}
	c := NewSessionStorageDriver(conn.Pool)
	c.SetCustomUserData(userDataConfig())

	runAssertions(ctx, t, conn.Pool, []string{`SELECT COUNT(*) = 1 FROM "UserCustomData"`})
	if err := c.DeleteUser(ctx, dataUserID); err != nil {
		t.Fatalf("SessionStorageDriver.DeleteUser() error = %v", err)
	}
	runAssertions(ctx, t, conn.Pool, []string{`SELECT COUNT(*) = 0 FROM "UserCustomData"`})
}

func TestSessionStorageDriver_UpdateCustomSessionData_SessionGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sessionID  impulse.UUID
		wantErrMsg string
	}{
		{
			name:       "unknown session aborts with not found",
			sessionID:  impulse.Must(impulse.UUIDFromString("99999999-9999-9999-9999-999999999999")),
			wantErrMsg: `session "99999999-9999-9999-9999-999999999999" not found`,
		},
		{
			name:       "expired session is rejected",
			sessionID:  impulse.Must(impulse.UUIDFromString("22222222-2222-2222-2222-222222222222")),
			wantErrMsg: "cannot update custom session data for an expired session",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			conn, err := prepareDatabase(ctx, t, "file://testdata/sessions_test/custom_columns_schema")
			if err != nil {
				t.Fatalf("prepareDatabase() error = %v, wantErr %v", err, false)
			}
			c := NewSessionStorageDriver(conn.Pool)
			c.SetCustomSessionData(&CustomSessionDataConfig{
				TableName: "SessionCustomData",
				Codec:     mustCodec(reflect.TypeFor[customStringData]()),
			})

			err = c.UpdateCustomSessionData(ctx, tt.sessionID, func(any) error { return nil })
			if err == nil {
				t.Fatal("SessionStorageDriver.UpdateCustomSessionData() error = nil, want error")
			}
			if httpio.Message(err) != tt.wantErrMsg {
				t.Errorf("SessionStorageDriver.UpdateCustomSessionData() error message = %s, want %s", httpio.Message(err), tt.wantErrMsg)
			}
			// The gate must abort before the write: the expired session's row is untouched.
			runAssertions(ctx, t, conn.Pool, []string{
				`SELECT COUNT(*) = 1 FROM "SessionCustomData"
					WHERE "SessionId" = '22222222-2222-2222-2222-222222222222' AND "CustomString" = 'viewer'`,
			})
		})
	}
}
