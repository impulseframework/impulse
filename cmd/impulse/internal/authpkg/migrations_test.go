package authpkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrations_listIndexes holds the three indexes a tenant's generated lists read
// through: the users by the exact username, the identities by their person and the mail
// links by tenant, each carrying the columns its list shows. Without them the same lists
// are served by reading the tables whole (measured on Cloud Spanner, 2026-11-13), so a
// change that drops a column from one is a change to what a page reads.
func TestMigrations_listIndexes(t *testing.T) {
	t.Parallel()

	const usersIndex = "CREATE UNIQUE INDEX StaffSessionUsersByUsername ON StaffSessionUsers(Username) STORING (Name, Email, EmailVerifiedAt, Disabled, PasswordChangeRequired, CreatedAt, LastSignInAt);"
	tests := []struct {
		name            string
		usernameIsEmail bool
		// address is the address column as the users table declares it.
		address string
	}{
		{
			name:    "an address held apart from the username is a column a person or an operator writes",
			address: "  Email              STRING(MAX),\n  NormalizedEmail    STRING(MAX) AS (CAST(LOWER(CAST(Email AS BYTES)) AS STRING)) STORED,\n",
		},
		{
			// The session library reads Email on every user it reads, and so does the
			// generated management: the column is there in this shape too, derived.
			name:            "where the username is the address the database derives the address column from it",
			usernameIsEmail: true,
			address:         "  Email              STRING(MAX) AS (Username) STORED,\n  NormalizedEmail    STRING(MAX) AS (CAST(LOWER(CAST(Username AS BYTES)) AS STRING)) STORED,\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var up, down strings.Builder
			for _, m := range Migrations("Staff", "staff", tt.usernameIsEmail) {
				up.WriteString(m.Up)
				down.WriteString(m.Down)
			}
			for _, statement := range []string{
				tt.address,
				usersIndex,
				"CREATE INDEX StaffSessionIdentitiesByUserId ON StaffSessionIdentities(UserId) STORING (Method, Connection, Subject, Tenant, EmailAtLink, CreatedAt, LastUsedAt);",
				"CREATE NULL_FILTERED INDEX StaffSessionMailLinksByTenantId ON StaffSessionMailLinks(TenantId, Kind, CreatedAt DESC) STORING (Email, Name, RoleNames, InvitedByUserId, ExpiresAt, ConsumedAt);",
				// The pending sign-ins keep the proof of the password a sign-in rests on,
				// which the session library reads and writes on every row.
				"    Username              STRING(MAX) NOT NULL,\n    PasswordProof         BYTES(32),\n",
			} {
				if !strings.Contains(up.String(), statement) {
					t.Errorf("the migrations lack %q", statement)
				}
			}
			for _, statement := range []string{
				"DROP INDEX StaffSessionUsersByUsername;",
				"DROP INDEX StaffSessionIdentitiesByUserId;",
				"DROP INDEX StaffSessionMailLinksByTenantId;",
			} {
				if !strings.Contains(down.String(), statement) {
					t.Errorf("the down migrations lack %q", statement)
				}
			}
			// Each index is dropped before its table.
			for index, table := range map[string]string{
				"DROP INDEX StaffSessionUsersByUsername;":     "DROP TABLE StaffSessionUsers;",
				"DROP INDEX StaffSessionMailLinksByTenantId;": "DROP TABLE StaffSessionMailLinks;",
				"DROP INDEX StaffSessionIdentitiesByUserId;":  "DROP TABLE StaffSessionIdentities;",
			} {
				if strings.Index(down.String(), index) > strings.Index(down.String(), table) {
					t.Errorf("%q comes after %q", index, table)
				}
			}
		})
	}
}

// TestMigrations_normalizedUsername holds the form a username is unique in and looked up
// by, for both shapes of the address columns: one with an @ in it by the case of its
// ASCII letters alone, as the session library compares two addresses, and any other in
// Unicode's normalization and case folding, the session library's own schema letter for
// letter.
func TestMigrations_normalizedUsername(t *testing.T) {
	t.Parallel()

	file := filepath.Join("..", "..", "..", "..", "session", "schema", "spanner", "migrations", "000002_SessionUsers.up.sql")
	library, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", file, err)
	}
	var columns []string
	for _, line := range strings.SplitAfter(string(library), "\n") {
		if strings.HasPrefix(line, "  NormalizedUsername ") {
			columns = append(columns, line)
		}
	}
	if len(columns) != 1 {
		t.Fatalf("%s has %d lines of the NormalizedUsername column, want 1", file, len(columns))
	}
	column := columns[0]
	for _, tt := range []struct {
		name            string
		usernameIsEmail bool
	}{
		{name: "an address held apart from the username"},
		{name: "where the username is the address", usernameIsEmail: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var up strings.Builder
			for _, m := range Migrations("Staff", "staff", tt.usernameIsEmail) {
				up.WriteString(m.Up)
			}
			if !strings.Contains(up.String(), column) {
				t.Errorf("the migrations lack %q", column)
			}
		})
	}
}

// TestMigrations_rowDeletionPolicies holds the row deletion policies of the three tables
// that would otherwise keep every row ever written, and the column the sign-in attempts
// table's policy is counted from: the same statements as the session library's shipped
// sets, under the auth's prefix.
func TestMigrations_rowDeletionPolicies(t *testing.T) {
	t.Parallel()

	var up strings.Builder
	for _, m := range Migrations("Staff", "staff", false) {
		up.WriteString(m.Up)
	}
	for _, statement := range []string{
		"ALTER TABLE StaffSessions ADD ROW DELETION POLICY (OLDER_THAN(UpdatedAt, INTERVAL 30 DAY));",
		"    EndsAt       TIMESTAMP AS (GREATEST(WindowEndsAt, IFNULL(LockedUntil, WindowEndsAt))) STORED,\n) PRIMARY KEY (Name);",
		"ALTER TABLE StaffSessionSignInAttempts ADD ROW DELETION POLICY (OLDER_THAN(EndsAt, INTERVAL 1 DAY));",
		"ALTER TABLE StaffSessionMailLinks ADD ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 30 DAY));",
	} {
		if !strings.Contains(up.String(), statement) {
			t.Errorf("the migrations lack %q", statement)
		}
	}
}

// TestMigrations_signInSettings holds the sign-in settings table beside the sign-in
// methods table: the session library's SessionSignInSettings migration under the auth's
// prefix, its one name held by its CHECK, and its DROP directly before the methods
// table's.
func TestMigrations_signInSettings(t *testing.T) {
	t.Parallel()

	var up, down strings.Builder
	for _, m := range Migrations("Staff", "staff", false) {
		up.WriteString(m.Up)
		down.WriteString(m.Down)
	}
	const table = `CREATE TABLE StaffSessionSignInSettings (
    Name                STRING(MAX) NOT NULL,
    RequireSecondFactor BOOL NOT NULL,
    RememberBrowserDays INT64 NOT NULL,
    UpdatedAt           TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
    CONSTRAINT CK_StaffSessionSignInSettingsName CHECK (Name = 'auth'),
) PRIMARY KEY (Name);`
	if !strings.Contains(up.String(), table) {
		t.Errorf("the migrations lack %q", table)
	}
	if methods, settings := strings.Index(up.String(), "CREATE TABLE StaffSessionSignInMethods ("), strings.Index(up.String(), table); settings < methods {
		t.Errorf("the sign-in settings table is created at %d, before the sign-in methods table at %d, want it directly after", settings, methods)
	}
	const drops = "DROP TABLE StaffSessionSignInSettings;\nDROP TABLE StaffSessionSignInMethods;"
	if !strings.Contains(down.String(), drops) {
		t.Errorf("the down migrations lack %q", drops)
	}
}
