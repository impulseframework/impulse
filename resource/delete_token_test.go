package resource

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// TestDeleteTokensDDL pins the statements of the DeleteTokens table for Spanner, none for
// PostgreSQL, and holds the delete fixture's migration to them.
func TestDeleteTokensDDL(t *testing.T) {
	t.Parallel()

	want := []string{
		`CREATE TABLE DeleteTokens (
  Id STRING(MAX) NOT NULL,
  TableName STRING(MAX) NOT NULL,
  RowKey STRING(MAX) NOT NULL,
  SessionId STRING(MAX) NOT NULL,
  PrincipalUser STRING(MAX),
  PrincipalRole STRING(MAX),
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  ExpiresAt TIMESTAMP NOT NULL,
) PRIMARY KEY (Id)`,
		"ALTER TABLE DeleteTokens ADD ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 1 DAY))",
	}
	if diff := cmp.Diff(want, DeleteTokensDDL(SpannerDBType)); diff != "" {
		t.Errorf("DeleteTokensDDL(SpannerDBType) (-want +got):\n%s", diff)
	}
	if got := DeleteTokensDDL(PostgresDBType); got != nil {
		t.Errorf("DeleteTokensDDL(PostgresDBType) = %q, want none", got)
	}
	fixture, err := os.ReadFile("testdata/deletecascade/schema/000004_delete_tokens_fixture.up.sql")
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	for _, statement := range want {
		if !strings.Contains(string(fixture), statement+";") {
			t.Errorf("the delete fixture's migration does not hold the statement:\n%s", statement)
		}
	}
}

// TestDeleteTokenLife pins how long a delete's token holds: five minutes, the owner's
// number.
func TestDeleteTokenLife(t *testing.T) {
	t.Parallel()

	if DeleteTokenLife != 5*time.Minute {
		t.Errorf("DeleteTokenLife = %s, want 5m0s", DeleteTokenLife)
	}
}

// TestDeleteTokenFrom pins what a delete reads of the token its request's frame stamped:
// nothing and false where none was stamped, and the token and true where one was, an
// empty one among them.
func TestDeleteTokenFrom(t *testing.T) {
	t.Parallel()

	type read struct {
		Token    string
		Declared bool
	}
	tests := []struct {
		name string
		ctx  context.Context
		want read
	}{
		{name: "nothing stamped", ctx: t.Context(), want: read{Token: "", Declared: false}},
		{name: "an empty token stamped", ctx: WithDeleteToken(t.Context(), ""), want: read{Token: "", Declared: true}},
		{name: "a token stamped", ctx: WithDeleteToken(t.Context(), "t-1"), want: read{Token: "t-1", Declared: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			token, declared := DeleteTokenFrom(tt.ctx)
			if got := (read{Token: token, Declared: declared}); got != tt.want {
				t.Errorf("DeleteTokenFrom() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
