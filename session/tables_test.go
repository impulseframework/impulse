package session

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
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
				PendingSignIns: "CrewSessionPendingSignIns", SignInAttempts: "CrewSessionSignInAttempts",
				MailLinks: "CrewSessionMailLinks", FactorCodes: "CrewSessionFactorCodes", TrustedDevices: "CrewSessionTrustedDevices",
				Tokens: "CrewSessionTokens", DeviceCodes: "CrewSessionDeviceCodes",
				AuthEvents: "CrewSessionAuthEvents", Impersonations: "CrewSessionImpersonations",
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
		PendingSignIns: "SessionPendingSignIns", SignInAttempts: "SessionSignInAttempts",
		MailLinks: "SessionMailLinks", FactorCodes: "SessionFactorCodes", TrustedDevices: "SessionTrustedDevices",
		Tokens: "SessionTokens", DeviceCodes: "SessionDeviceCodes",
		AuthEvents: "SessionAuthEvents", Impersonations: "SessionImpersonations",
	}
	if diff := cmp.Diff(want, DefaultTables()); diff != "" {
		t.Errorf("DefaultTables() (-want +got):\n%s", diff)
	}
}
