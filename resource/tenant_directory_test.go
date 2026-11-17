package resource

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// establishedBy is a request context whose session was established by events.
func establishedBy(events ...sessioninfo.AuthEvent) context.Context {
	return context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{Username: "pat"}, AuthEvents: events,
	})
}

// TestSessionDirectory pins what "the directory I am signed in through" names: the
// method and connection of the session's first auth event when it came through a
// directory, and a Conflict that says how to sign in instead for every other session.
func TestSessionDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  context.Context
		want signin.Directory
		// wantRefusal is a part of the Conflict's sentence; empty for none.
		wantRefusal string
	}{
		{
			name: "a sign-in through a Google Workspace names the Workspace",
			ctx:  establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: "acme.example"}, sessioninfo.AuthEvent{Method: sessioninfo.MethodEmailCode}),
			want: signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "acme.example"},
		},
		{
			name: "a personal account is answered as it is, for the rules to refuse by name",
			ctx:  establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection}),
			want: signin.Directory{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection},
		},
		{
			name:        "a password session is refused, saying so",
			ctx:         establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword}),
			wantRefusal: "You are signed in with a password. A directory is set by signing in through it",
		},
		{
			name:        "an impersonated session is refused: it came through no directory",
			ctx:         establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice@example.com"}),
			wantRefusal: "This session was not signed in through an organization's directory.",
		},
		{
			name:        "a session that records no events is refused",
			ctx:         establishedBy(),
			wantRefusal: "This session was not signed in through an organization's directory.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := SessionDirectory(tt.ctx)
			if tt.wantRefusal != "" {
				if !httpio.HasConflict(err) || !strings.Contains(httpio.Message(err), tt.wantRefusal) {
					t.Fatalf("SessionDirectory() error = %v, want a Conflict saying %q", err, tt.wantRefusal)
				}

				return
			}
			if err != nil {
				t.Fatalf("SessionDirectory() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("SessionDirectory() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestTenantDirectoryStatements pins the statements the rules read with, in both
// dialects: the rows that are the tenant's or carry the directory, the tenant's own
// first, and the tenant's directory-only flag, which a record with no such column
// renders nothing for.
func TestTenantDirectoryStatements(t *testing.T) {
	t.Parallel()

	columns := &TenantDeclarationColumns{DirectoryOnly: "DirectoryOnly", DirectoriesTable: "StationDirectories", TenantColumn: "StationId", MethodColumn: "Method", ConnectionColumn: "Connection"}
	noFlag := &TenantDeclarationColumns{DirectoriesTable: "StationDirectories", TenantColumn: "StationId", MethodColumn: "Method", ConnectionColumn: "Connection"}

	tests := []struct {
		name    string
		dbType  DBType
		columns *TenantDeclarationColumns
		want    directoryRuleStatements
		wantErr bool
	}{
		{
			name:    "Spanner quotes with backticks and takes named parameters",
			dbType:  SpannerDBType,
			columns: columns,
			want: directoryRuleStatements{
				Held:          "SELECT CAST(`StationId` AS STRING) AS Domain, `Method` AS Method, `Connection` AS Connection FROM `StationDirectories` WHERE CAST(`StationId` AS STRING) = @domain OR (`Method` = @method AND `Connection` = @connection) ORDER BY CASE WHEN CAST(`StationId` AS STRING) = @domain THEN 0 ELSE 1 END, CAST(`StationId` AS STRING)",
				DirectoryOnly: "SELECT `DirectoryOnly` AS DirectoryOnly FROM `Stations` WHERE CAST(`Id` AS STRING) = @domain",
			},
		},
		{
			name:    "Postgres quotes with double quotes and takes the tenant, the method and the connection as $1, $2 and $3",
			dbType:  PostgresDBType,
			columns: columns,
			want: directoryRuleStatements{
				Held:          `SELECT CAST("StationId" AS TEXT) AS "Domain", "Method" AS "Method", "Connection" AS "Connection" FROM "StationDirectories" WHERE CAST("StationId" AS TEXT) = $1 OR ("Method" = $2 AND "Connection" = $3) ORDER BY CASE WHEN CAST("StationId" AS TEXT) = $1 THEN 0 ELSE 1 END, CAST("StationId" AS TEXT)`,
				DirectoryOnly: `SELECT "DirectoryOnly" AS "DirectoryOnly" FROM "Stations" WHERE CAST("Id" AS TEXT) = $1`,
			},
		},
		{
			name:    "a record with no directory-only column reads no flag",
			dbType:  SpannerDBType,
			columns: noFlag,
			want: directoryRuleStatements{
				Held: "SELECT CAST(`StationId` AS STRING) AS Domain, `Method` AS Method, `Connection` AS Connection FROM `StationDirectories` WHERE CAST(`StationId` AS STRING) = @domain OR (`Method` = @method AND `Connection` = @connection) ORDER BY CASE WHEN CAST(`StationId` AS STRING) = @domain THEN 0 ELSE 1 END, CAST(`StationId` AS STRING)",
			},
		},
		{name: "an unknown database type is refused", dbType: DBType("oracle"), columns: columns, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tenantDirectoryStatements(tt.dbType, "Stations", "Id", tt.columns)
			if (err != nil) != tt.wantErr {
				t.Fatalf("tenantDirectoryStatements() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("tenantDirectoryStatements() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDirectoryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		directory signin.Directory
		want      string
	}{
		{directory: signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "acme.example"}, want: "Google Workspace acme.example"},
		{directory: signin.Directory{Method: sessioninfo.MethodAzure, Connection: "6f2c1b7e-3d4a-4c1e-9b2a-1f0e8d7c6b5a"}, want: "Microsoft Entra tenant 6f2c1b7e-3d4a-4c1e-9b2a-1f0e8d7c6b5a"},
		{directory: signin.Directory{Method: sessioninfo.MethodWorkOS, Connection: "conn_01"}, want: "WorkOS connection conn_01"},
		{directory: signin.Directory{Method: sessioninfo.MethodOIDC, Connection: "idp"}, want: "OpenID Connect provider idp"},
		{directory: signin.Directory{Method: "saml", Connection: "idp"}, want: "Directory idp"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := DirectoryName(tt.directory); got != tt.want {
				t.Errorf("DirectoryName() = %q, want %q", got, tt.want)
			}
		})
	}
}
