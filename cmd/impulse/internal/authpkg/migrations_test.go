package authpkg

import (
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

	const usersIndex = "CREATE UNIQUE INDEX StaffSessionUsersByUsername ON StaffSessionUsers(Username) STORING (Email, EmailVerifiedAt, Disabled, PasswordChangeRequired, CreatedAt, LastSignInAt);"
	tests := []struct {
		name            string
		usernameIsEmail bool
		// address is the address column as the users table declares it.
		address string
	}{
		{
			name:    "an address held apart from the username is a column a person or an operator writes",
			address: "  Email              STRING(MAX),\n  NormalizedEmail    STRING(MAX) AS (NORMALIZE_AND_CASEFOLD(Email)) STORED,\n",
		},
		{
			// The session library reads Email on every user it reads, and so does the
			// generated management: the column is there in this shape too, derived.
			name:            "where the username is the address the database derives the address column from it",
			usernameIsEmail: true,
			address:         "  Email              STRING(MAX) AS (Username) STORED,\n  NormalizedEmail    STRING(MAX) AS (NORMALIZE_AND_CASEFOLD(Username)) STORED,\n",
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
				"CREATE NULL_FILTERED INDEX StaffSessionMailLinksByTenant ON StaffSessionMailLinks(Tenant, Kind, CreatedAt DESC) STORING (Email, RoleNames, InvitedBy, ExpiresAt, ConsumedAt);",
			} {
				if !strings.Contains(up.String(), statement) {
					t.Errorf("the migrations lack %q", statement)
				}
			}
			for _, statement := range []string{
				"DROP INDEX StaffSessionUsersByUsername;",
				"DROP INDEX StaffSessionIdentitiesByUserId;",
				"DROP INDEX StaffSessionMailLinksByTenant;",
			} {
				if !strings.Contains(down.String(), statement) {
					t.Errorf("the down migrations lack %q", statement)
				}
			}
			// Each index is dropped before its table.
			for index, table := range map[string]string{
				"DROP INDEX StaffSessionUsersByUsername;":    "DROP TABLE StaffSessionUsers;",
				"DROP INDEX StaffSessionMailLinksByTenant;":  "DROP TABLE StaffSessionMailLinks;",
				"DROP INDEX StaffSessionIdentitiesByUserId;": "DROP TABLE StaffSessionIdentities;",
			} {
				if strings.Index(down.String(), index) > strings.Index(down.String(), table) {
					t.Errorf("%q comes after %q", index, table)
				}
			}
		})
	}
}
