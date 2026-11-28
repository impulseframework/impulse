package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/auditlog"
)

// TestTablesWithPrefix pins the one rule for the names of an Auth kept under a prefix:
// the library's own name for each table, led by the prefix, and a prefix that leaves no
// plain identifier refused.
func TestTablesWithPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prefix  string
		want    Tables
		wantErr string
	}{
		{
			name:   "no prefix answers the library's own names",
			prefix: "",
			want:   DefaultTables(),
		},
		{
			name:   "a prefix leads every name",
			prefix: "Crew",
			want: Tables{
				Sessions: "CrewSessions", Users: "CrewSessionUsers", Identities: "CrewSessionIdentities",
				PendingSignIns: "CrewSessionPendingSignIns", SignInAttempts: "CrewSessionSignInAttempts", SignInMethods: "CrewSessionSignInMethods",
				SignInSettings: "CrewSessionSignInSettings", MailLinks: "CrewSessionMailLinks", FactorCodes: "CrewSessionFactorCodes", TrustedDevices: "CrewSessionTrustedDevices",
				Tokens: "CrewSessionTokens", DeviceCodes: "CrewSessionDeviceCodes",
				AuthEvents: "CrewSessionAuthEvents", Impersonations: "CrewSessionImpersonations",
				AuditRecords: "CrewSessionAuditRecords",
			},
		},
		{
			name:    "a prefix that starts with a digit is refused",
			prefix:  "1Crew",
			wantErr: `table prefix "1Crew"`,
		},
		{
			name:    "a prefix with a character no identifier carries is refused",
			prefix:  "Crew-",
			wantErr: `table prefix "Crew-"`,
		},
		{
			name:    "a prefix that leaves a name over the identifier's length is refused",
			prefix:  strings.Repeat("A", 120),
			wantErr: "table prefix",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := TablesWithPrefix(tt.prefix)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("TablesWithPrefix(%q) error = %v, want one naming %s", tt.prefix, err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("TablesWithPrefix(%q) error = %v", tt.prefix, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("TablesWithPrefix(%q) (-want +got):\n%s", tt.prefix, diff)
			}
		})
	}
}

// TestDefaultTables pins the library's own names: the ones its schema sets create.
func TestDefaultTables(t *testing.T) {
	t.Parallel()

	want := Tables{
		Sessions: "Sessions", Users: "SessionUsers", Identities: "SessionIdentities",
		PendingSignIns: "SessionPendingSignIns", SignInAttempts: "SessionSignInAttempts", SignInMethods: "SessionSignInMethods",
		SignInSettings: "SessionSignInSettings", MailLinks: "SessionMailLinks", FactorCodes: "SessionFactorCodes", TrustedDevices: "SessionTrustedDevices",
		Tokens: "SessionTokens", DeviceCodes: "SessionDeviceCodes",
		AuthEvents: "SessionAuthEvents", Impersonations: "SessionImpersonations",
		AuditRecords: "SessionAuditRecords",
	}
	if diff := cmp.Diff(want, DefaultTables()); diff != "" {
		t.Errorf("DefaultTables() (-want +got):\n%s", diff)
	}
}

// TestAuditRecordsSchema holds each schema set's audit records migration to the auditlog
// package's statements for the table, comments, blank lines and the PostgreSQL
// transaction's BEGIN and COMMIT aside: the files are what an application that applies
// the library's schema sets gets, and the functions are what every other carrier copies.
func TestAuditRecordsSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want []string
	}{
		{
			name: "Spanner",
			file: filepath.Join("schema", "spanner", "auth", "migrations", "000023_SessionAuditRecords.up.sql"),
			want: auditlog.SpannerDDL(DefaultTables().AuditRecords, auditlog.KeepForever()),
		},
		{
			name: "PostgreSQL",
			file: filepath.Join("schema", "postgresql", "auth", "migrations", "000022_SessionAuditRecords.up.sql"),
			want: auditlog.PostgresDDL(DefaultTables().AuditRecords),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, migrationStatements(t, tt.file)); diff != "" {
				t.Errorf("%s (-auditlog +file):\n%s", tt.file, diff)
			}
		})
	}
}

// migrationStatements answers the statements of a migration file, each without its
// semicolon: its comment lines, its blank lines and a transaction's BEGIN and COMMIT left
// out.
func migrationStatements(t *testing.T, file string) []string {
	t.Helper()

	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", file, err)
	}
	var kept []string
	for _, line := range strings.Split(string(content), "\n") {
		switch trimmed := strings.TrimSpace(line); {
		case trimmed == "", strings.HasPrefix(trimmed, "--"), trimmed == "BEGIN;", trimmed == "COMMIT;":
		default:
			kept = append(kept, line)
		}
	}
	var statements []string
	for _, statement := range strings.Split(strings.Join(kept, "\n"), ";") {
		if statement = strings.TrimSpace(statement); statement != "" {
			statements = append(statements, statement)
		}
	}

	return statements
}
