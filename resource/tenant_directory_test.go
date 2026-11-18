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

// TestDirectoryWords pins how a sentence names a directory to a person: by the name a
// sign-in registration gives it, a name from any auth before a label from another;
// where none names it, by the label of the button a person signs in to it with; and
// where no registration answers, by what the directory alone says. It never says a
// Microsoft Entra tenant's id. DirectoryName is the same words with the kind of
// directory before a name.
func TestDirectoryWords(t *testing.T) {
	t.Parallel()

	const tenantID = "6f2c1b7e-3d4a-4c1e-9b2a-1f0e8d7c6b5a"
	entra := signin.Directory{Method: sessioninfo.MethodAzure, Connection: tenantID}
	answers := func(name, label string) DirectoryNamer {
		return func(signin.Directory) (string, string) {
			return name, label
		}
	}
	tests := []struct {
		name      string
		directory signin.Directory
		namers    []DirectoryNamer
		wantWords string
		wantName  string
	}{
		{
			name: "a tenant a registration names is its name", directory: entra, namers: []DirectoryNamer{answers("Lakeside", "Staff")},
			wantWords: "Lakeside", wantName: "Microsoft Entra tenant Lakeside",
		},
		{
			name: "a tenant no registration names is said by the label of its button", directory: entra, namers: []DirectoryNamer{answers("", "Microsoft")},
			wantWords: "your organization's Microsoft directory", wantName: "your organization's Microsoft directory",
		},
		{
			name: "a label that ends in the word directory is not given it twice", directory: entra, namers: []DirectoryNamer{answers("", "Staff directory")},
			wantWords: "your organization's Staff directory", wantName: "your organization's Staff directory",
		},
		{
			name: "a name from one auth is said before a label from another", directory: entra, namers: []DirectoryNamer{answers("", "Microsoft"), answers("Lakeside", "Portal")},
			wantWords: "Lakeside", wantName: "Microsoft Entra tenant Lakeside",
		},
		{
			name: "the first label is the one said", directory: entra, namers: []DirectoryNamer{nil, answers("", ""), answers("", "Microsoft"), answers("", "Portal")},
			wantWords: "your organization's Microsoft directory", wantName: "your organization's Microsoft directory",
		},
		{
			name: "a tenant no registration takes is said by its kind, never by its id", directory: entra, namers: []DirectoryNamer{answers("", "")},
			wantWords: "your organization's Microsoft directory", wantName: "your organization's Microsoft directory",
		},
		{name: "with nobody to ask, a tenant is said by its kind", directory: entra, wantWords: "your organization's Microsoft directory", wantName: "your organization's Microsoft directory"},
		{
			name: "with nobody to ask, a Workspace is its connection, the application's name for it or its domain", directory: signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "acme.example"},
			wantWords: "acme.example", wantName: "Google Workspace acme.example",
		},
		{
			name: "with nobody to ask, a provider the application named is its name", directory: signin.Directory{Method: sessioninfo.MethodOIDC, Connection: "idp"},
			wantWords: "idp", wantName: "OpenID Connect provider idp",
		},
		{
			name: "with nobody to ask, a provider known by its issuer is the issuer's host", directory: signin.Directory{Method: sessioninfo.MethodOIDC, Connection: "https://id.acme.example/realms/staff"},
			wantWords: "id.acme.example", wantName: "OpenID Connect provider id.acme.example",
		},
		{
			name: "with nobody to ask, a connection that is an id of another kind is not said", directory: signin.Directory{Method: sessioninfo.MethodWorkOS, Connection: "conn_01"},
			wantWords: "your organization's directory", wantName: "your organization's directory",
		},
		{
			name: "a method the library does not know", directory: signin.Directory{Method: "saml", Connection: "idp"},
			wantWords: "your organization's directory", wantName: "your organization's directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := DirectoryWords(tt.directory, tt.namers...); got != tt.wantWords {
				t.Errorf("DirectoryWords() = %q, want %q", got, tt.wantWords)
			}
			if got := DirectoryName(tt.directory, tt.namers...); got != tt.wantName {
				t.Errorf("DirectoryName() = %q, want %q", got, tt.wantName)
			}
			for _, got := range []string{DirectoryWords(tt.directory, tt.namers...), DirectoryName(tt.directory, tt.namers...)} {
				if strings.Contains(got, tenantID) || strings.Contains(got, "conn_01") {
					t.Errorf("%q says the connection, want words a person knows", got)
				}
			}
		})
	}

	if got := startSentence(DirectoryName(entra)); got != "Your organization's Microsoft directory" {
		t.Errorf("the words at the start of a sentence = %q, want them to start in upper case", got)
	}
	if got := startSentence(DirectoryName(signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "acme.example"})); got != "Google Workspace acme.example" {
		t.Errorf("a name at the start of a sentence = %q, want it as it is", got)
	}
}
