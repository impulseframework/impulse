package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

// The fixture's resources: a table that carries its tenant's key (line 8), one that
// reaches its tenant through a row of that table, a computed resource scoped to a
// tenant with no table of its own, and the directories record.
const (
	keyedResource = `package resources

type (
	// Announcement is posted within one tenant.
	//
	// @resource
	// @permissionScope(domain)
	Announcement struct {
		ID string ` + "`spanner:\"Id\"`" + `
		// @domain
		TenantID string ` + "`spanner:\"TenantId\"`" + `
	}
)
`
	throughResource = `package resources

type (
	// Reply answers an announcement, and reaches its tenant through it.
	//
	// @resource
	// @permissionScope(domain)
	Reply struct {
		ID string ` + "`spanner:\"Id\"`" + `
		// @domain(via: TenantID)
		AnnouncementID string ` + "`spanner:\"AnnouncementId\"`" + `
	}
)
`
	computedResource = `package resources

type (
	// Digest is computed for one tenant and has no table.
	//
	// @computed
	// @permissionScope(domain)
	Digest struct {
		TenantID string ` + "`spanner:\"TenantId\"`" + `
	}
)
`
	directoriesResource = `package resources

type (
	// TenantDirectory is a tenant's directory.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	TenantDirectory struct {
		ID string ` + "`spanner:\"Id\"`" + `
		// @domain
		TenantID string ` + "`spanner:\"TenantId\"`" + `
	}
)
`
	tenantsTable = "CREATE TABLE Tenants (\n  Id STRING(36) NOT NULL,\n  Name STRING(MAX) NOT NULL,\n) PRIMARY KEY (Id);\n"
	// announcementsTable is the sample table as the skeletons write it, with the rule.
	announcementsTable = `-- The rows of a tenant-scoped table go with the tenant; a table copied from this one keeps the rule.
CREATE TABLE Announcements (
  Id STRING(36) NOT NULL,
  TenantId STRING(36) NOT NULL,
  Title STRING(MAX) NOT NULL,

  CONSTRAINT CK_Announcements_Id CHECK (REGEXP_CONTAINS(Id, r'^[0-9a-f]{8}-[0-9a-f]{4}$')),
  CONSTRAINT FK_Announcements_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants(Id) ON DELETE CASCADE,
) PRIMARY KEY (Id);

CREATE INDEX AnnouncementsByTenantIdTitle ON Announcements(TenantId, Title);
`
)

// TestTenantDeleteRule holds every table that carries a tenant's key to the delete rule
// on its foreign key to the tenant table: the check fails on a table that declares the
// key with no rule and on one that declares no key at all, in whichever migration says
// so last, and it judges neither the directories record, whose row the generated delete
// removes, nor a table that reaches its tenant through another row, nor a resource with
// no table. A table it cannot read is a warning that names it, never a pass.
func TestTenantDeleteRule(t *testing.T) {
	t.Parallel()

	site := program("pkg/resources",
		`generation.GenerateHandlers("app"),`,
		`generation.GenerateRoutes("pkg/router", "api"),`,
	)
	withoutRule := strings.Replace(announcementsTable, " ON DELETE CASCADE", "", 1)
	withoutKey := strings.Replace(announcementsTable, "  CONSTRAINT FK_Announcements_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants(Id) ON DELETE CASCADE,\n", "", 1)
	tenanted := func(files map[string]string) map[string]string {
		all := map[string]string{
			"cmd/generate/main.go":                          site,
			"pkg/resources/tenants.go":                      tenantRecord,
			"pkg/resources/announcements.go":                keyedResource,
			"schema/migrations/000001_Tenants.up.sql":       tenantsTable,
			"schema/migrations/000001_Tenants.down.sql":     "DROP TABLE Tenants;\n",
			"schema/migrations/000002_Announcements.up.sql": announcementsTable,
		}
		for rel, content := range files {
			if content == "" {
				delete(all, rel)

				continue
			}
			all[rel] = content
		}

		return all
	}

	tests := []struct {
		name        string
		files       map[string]string
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name:        "the sample table as the skeletons write it",
			files:       tenanted(nil),
			wantStatus:  Pass,
			wantSummary: "1 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants)",
		},
		{
			name:        "one table whose foreign key to the tenant table has no delete rule",
			files:       tenanted(map[string]string{"schema/migrations/000002_Announcements.up.sql": withoutRule}),
			wantStatus:  Fail,
			wantSummary: "1 table(s) that carry a tenant's key do not go with the tenant",
			wantDetails: []string{
				"pkg/resources/announcements.go:8: Announcement keeps its tenant's key in TenantId, and schema/migrations/000002_Announcements.up.sql declares FK_Announcements_TenantId from Announcements(TenantId) to Tenants with no delete rule, so the delete of a tenant that still holds one of the table's rows is refused; say ON DELETE CASCADE on that foreign key, so a tenant that is deleted takes the table's rows with it",
			},
		},
		{
			name:        "one table that keeps the key with no foreign key at all",
			files:       tenanted(map[string]string{"schema/migrations/000002_Announcements.up.sql": withoutKey}),
			wantStatus:  Fail,
			wantSummary: "1 table(s) that carry a tenant's key do not go with the tenant",
			wantDetails: []string{
				"pkg/resources/announcements.go:8: Announcement keeps its tenant's key in TenantId, and schema/migrations/000002_Announcements.up.sql declares no foreign key from Announcements(TenantId) to Tenants, so the rows of a tenant that is deleted stay behind; declare one with the delete rule (FOREIGN KEY (TenantId) REFERENCES Tenants(<its key>) ON DELETE CASCADE), so a tenant that is deleted takes the table's rows with it",
			},
		},
		{
			name: "a later migration gives the table the rule",
			files: tenanted(map[string]string{
				"schema/migrations/000002_Announcements.up.sql": withoutRule,
				"schema/migrations/000003_Rule.up.sql":          "ALTER TABLE Announcements DROP CONSTRAINT FK_Announcements_TenantId;\nALTER TABLE Announcements ADD CONSTRAINT FK_Announcements_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants (Id) ON DELETE CASCADE;\n",
			}),
			wantStatus:  Pass,
			wantSummary: "1 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants)",
		},
		{
			name: "a later migration takes the rule away",
			files: tenanted(map[string]string{
				"schema/migrations/000003_Rule.up.sql": "ALTER TABLE Announcements DROP CONSTRAINT FK_Announcements_TenantId;\nALTER TABLE Announcements ADD CONSTRAINT FK_Announcements_Tenant FOREIGN KEY (TenantId) REFERENCES Tenants (Id);\n",
			}),
			wantStatus:  Fail,
			wantSummary: "1 table(s) that carry a tenant's key do not go with the tenant",
			wantDetails: []string{
				"pkg/resources/announcements.go:8: Announcement keeps its tenant's key in TenantId, and schema/migrations/000003_Rule.up.sql declares FK_Announcements_Tenant from Announcements(TenantId) to Tenants with no delete rule, so the delete of a tenant that still holds one of the table's rows is refused; say ON DELETE CASCADE on that foreign key, so a tenant that is deleted takes the table's rows with it",
			},
		},
		{
			name: "a table interleaved in the tenant table with the rule",
			files: tenanted(map[string]string{
				"schema/migrations/000002_Announcements.up.sql": "CREATE TABLE Announcements (\n  TenantId STRING(36) NOT NULL,\n  Id STRING(36) NOT NULL,\n) PRIMARY KEY (TenantId, Id),\n  INTERLEAVE IN PARENT Tenants ON DELETE CASCADE;\n",
			}),
			wantStatus:  Pass,
			wantSummary: "1 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants)",
		},
		{
			name: "a table interleaved in the tenant table without it",
			files: tenanted(map[string]string{
				"schema/migrations/000002_Announcements.up.sql": "CREATE TABLE Announcements (\n  TenantId STRING(36) NOT NULL,\n  Id STRING(36) NOT NULL,\n) PRIMARY KEY (TenantId, Id),\n  INTERLEAVE IN PARENT Tenants ON DELETE NO ACTION;\n",
			}),
			wantStatus:  Fail,
			wantSummary: "1 table(s) that carry a tenant's key do not go with the tenant",
			wantDetails: []string{
				"pkg/resources/announcements.go:8: Announcement keeps its tenant's key in TenantId, and schema/migrations/000002_Announcements.up.sql interleaves Announcements in Tenants with no delete rule, so the delete of a tenant that still holds one of the table's rows is refused; say ON DELETE CASCADE on the interleave, so a tenant that is deleted takes the table's rows with it",
			},
		},
		{
			name: "a column that declares its own reference with the rule",
			files: tenanted(map[string]string{
				"schema/migrations/000002_Announcements.up.sql": "CREATE TABLE Announcements (\n  Id STRING(36) NOT NULL,\n  TenantId STRING(36) NOT NULL REFERENCES Tenants (Id) ON DELETE CASCADE,\n  PRIMARY KEY (Id)\n);\n",
			}),
			wantStatus:  Pass,
			wantSummary: "1 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants)",
		},
		{
			name: "the directories record, a table through another row and a computed resource are not judged",
			files: tenanted(map[string]string{
				"pkg/resources/tenant_directories.go":         directoriesResource,
				"pkg/resources/replies.go":                    throughResource,
				"pkg/resources/digests.go":                    computedResource,
				"schema/migrations/000003_Directories.up.sql": "CREATE TABLE TenantDirectories (\n  Id STRING(36) NOT NULL,\n  TenantId STRING(36) NOT NULL,\n  CONSTRAINT FK_TenantDirectories_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants(Id),\n) PRIMARY KEY (Id);\n",
				"schema/migrations/000004_Replies.up.sql":     "CREATE TABLE Replies (\n  Id STRING(36) NOT NULL,\n  AnnouncementId STRING(36) NOT NULL,\n  CONSTRAINT FK_Replies_AnnouncementId FOREIGN KEY (AnnouncementId) REFERENCES Announcements(Id),\n) PRIMARY KEY (Id);\n",
			}),
			wantStatus:  Pass,
			wantSummary: "1 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants); 1 reach their tenant through another row and go as that foreign key says, which is not judged",
		},
		{
			name:        "a table no migration creates is named, not passed",
			files:       tenanted(map[string]string{"schema/migrations/000002_Announcements.up.sql": ""}),
			wantStatus:  Warn,
			wantSummary: "0 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants); 1 could not be read",
			wantDetails: []string{
				"pkg/resources/announcements.go:8: no migration under file://schema/migrations creates Announcements, the table of Announcement, so its delete rule was not read",
			},
		},
		{
			name: "a table dropped by a later migration is not read as it was",
			files: tenanted(map[string]string{
				"schema/migrations/000003_Drop.up.sql": "DROP TABLE Announcements;\n",
			}),
			wantStatus:  Warn,
			wantSummary: "0 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants); 1 could not be read",
			wantDetails: []string{
				"pkg/resources/announcements.go:8: no migration under file://schema/migrations creates Announcements, the table of Announcement, so its delete rule was not read",
			},
		},
		{
			name: "a migration source that is not a file is named, and its tables are not judged",
			files: tenanted(map[string]string{
				"cmd/generate/main.go": strings.Replace(site, "file://schema/migrations", "gs://bucket/migrations", 1),
			}),
			wantStatus:  Warn,
			wantSummary: "0 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants); 1 could not be read",
			wantDetails: []string{
				"cmd/generate/main.go: migration source gs://bucket/migrations is not a file:// path, so the delete rules of its tables were not read",
			},
		},
		{
			name: "an application without tenants",
			files: map[string]string{
				"cmd/generate/main.go":    site,
				"pkg/resources/lenses.go": globalResource,
			},
			wantStatus:  Skip,
			wantSummary: "no tenant record",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			write := func(rel, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", "module example.com/harbor\n\ngo 1.26.6\n")
			for rel, content := range tt.files {
				write(rel, content)
			}

			a, err := app.Discover(root)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}
			got := tenantDeleteRule{}.Run(context.Background(), &Env{App: a})
			want := Result{Name: tenantDeleteRuleName, Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTenantDeleteRule_lodestar holds the demonstration application to the rule as it
// stands: its seven tables that carry a sector's key go with the sector, and the eight
// that reach their sector through another row are counted and not judged. A table added
// with a sector's key and no rule fails here before it fails in the pipeline.
func TestTenantDeleteRule_lodestar(t *testing.T) {
	t.Parallel()

	a, err := app.Discover(filepath.Join("..", "..", "..", "..", "lodestar"))
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}
	got := tenantDeleteRule{}.Run(context.Background(), &Env{App: a})
	want := Result{
		Name: tenantDeleteRuleName, Status: Pass,
		Summary: "7 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Sectors); 8 reach their tenant through another row and go as that foreign key says, which is not judged",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Run() mismatch (-want +got):\n%s", diff)
	}
}
