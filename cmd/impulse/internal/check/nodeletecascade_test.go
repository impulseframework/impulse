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
// reaches its tenant through a row of that table, one that reaches it through a row of
// that one (two links before the tenant's key), a computed resource scoped to a tenant
// with no table of its own, and the directories record.
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
	throughTwoResource = `package resources

type (
	// Reaction marks a reply, and reaches its tenant through the reply and its
	// announcement.
	//
	// @resource
	// @permissionScope(domain)
	Reaction struct {
		ID string ` + "`spanner:\"Id\"`" + `
		// @domain(via: AnnouncementID.TenantID)
		ReplyID string ` + "`spanner:\"ReplyId\"`" + `
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
	// repliesTable and reactionsTable are the two tables that reach their tenant through
	// another row, each with the rule on its own link.
	repliesTable   = "CREATE TABLE Replies (\n  Id STRING(36) NOT NULL,\n  AnnouncementId STRING(36) NOT NULL,\n  CONSTRAINT FK_Replies_AnnouncementId FOREIGN KEY (AnnouncementId) REFERENCES Announcements(Id) ON DELETE CASCADE,\n) PRIMARY KEY (Id);\n"
	reactionsTable = "CREATE TABLE Reactions (\n  Id STRING(36) NOT NULL,\n  ReplyId STRING(36) NOT NULL,\n  CONSTRAINT FK_Reactions_ReplyId FOREIGN KEY (ReplyId) REFERENCES Replies(Id) ON DELETE CASCADE,\n) PRIMARY KEY (Id);\n"
	tenantsTable   = "CREATE TABLE Tenants (\n  Id STRING(36) NOT NULL,\n  Name STRING(MAX) NOT NULL,\n) PRIMARY KEY (Id);\n"
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

// lensMarkResource is a second global record, whose table references the first's.
const lensMarkResource = `package resources

// LensMark is a mark on a lens.
//
// @resource
type LensMark struct {
	ID     string ` + "`spanner:\"Id\"`" + `
	LensID string ` + "`spanner:\"LensId\"`" + `
}
`

// inCode is what every finding of the check says after naming the rule.
func inCode(link string) string {
	return "Rows are deleted in code only: a row the database deletes is not checked against anyone's grants, leaves no change record, is not announced to open pages and keeps its stored files. Take ON DELETE CASCADE off the " + link + ". Where a delete of the record should take these rows, declare it on the record (// @deleteCascade); a tenant's delete takes its rows in code as it is."
}

// TestNoDeleteCascade holds the schema to deleting no row by the database: the check
// fails on a foreign key or an interleave declared ON DELETE CASCADE whose target is a
// table a struct of the resource package declares, whichever migration says so last and
// with or without a tenant record, and counts, without judging, a rule on a link to any
// other table. A source it cannot read is a warning that names it, and a schema two sites
// read is judged once.
func TestNoDeleteCascade(t *testing.T) {
	t.Parallel()

	site := program("pkg/resources",
		`generation.GenerateHandlers("app"),`,
		`generation.GenerateRoutes("pkg/router", "api"),`,
	)
	withoutRule := strings.Replace(announcementsTable, " ON DELETE CASCADE", "", 1)
	tenanted := func(files map[string]string) map[string]string {
		all := map[string]string{
			"cmd/generate/main.go":                          site,
			"pkg/resources/tenants.go":                      tenantRecord,
			"pkg/resources/announcements.go":                keyedResource,
			"schema/migrations/000001_Tenants.up.sql":       tenantsTable,
			"schema/migrations/000001_Tenants.down.sql":     "DROP TABLE Tenants;\n",
			"schema/migrations/000002_Announcements.up.sql": withoutRule,
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
	const (
		passed     = "no foreign key or interleave has the database delete rows with one of the application's records"
		oneFound   = "1 foreign key(s) or interleave(s) have the database delete rows with one of the application's records"
		announced  = "schema/migrations/000002_Announcements.up.sql: Announcements declares FK_Announcements_TenantId to Tenants with ON DELETE CASCADE, so the database deletes the rows of Announcements with the row of Tenants they name. "
		interleave = "CREATE TABLE Announcements (\n  TenantId STRING(36) NOT NULL,\n  Id STRING(36) NOT NULL,\n) PRIMARY KEY (TenantId, Id),\n  INTERLEAVE IN PARENT Tenants ON DELETE CASCADE;\n"
	)

	tests := []struct {
		name        string
		files       map[string]string
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name:        "no rule anywhere",
			files:       tenanted(nil),
			wantStatus:  Pass,
			wantSummary: passed,
		},
		{
			name:        "a foreign key to a record with the rule",
			files:       tenanted(map[string]string{"schema/migrations/000002_Announcements.up.sql": announcementsTable}),
			wantStatus:  Fail,
			wantSummary: oneFound,
			wantDetails: []string{announced + inCode("foreign key")},
		},
		{
			name:        "an interleave in a record with the rule",
			files:       tenanted(map[string]string{"schema/migrations/000002_Announcements.up.sql": interleave}),
			wantStatus:  Fail,
			wantSummary: oneFound,
			wantDetails: []string{
				"schema/migrations/000002_Announcements.up.sql: Announcements is interleaved in Tenants with ON DELETE CASCADE, so the database deletes the rows of Announcements with the row of Tenants they are under. " + inCode("interleave"),
			},
		},
		{
			name: "a rule on a column's own line, as a PostgreSQL migration writes it",
			files: tenanted(map[string]string{
				"schema/migrations/000002_Announcements.up.sql": "CREATE TABLE Announcements (\n  Id STRING(36) NOT NULL,\n  TenantId STRING(36) NOT NULL REFERENCES Tenants (Id) ON DELETE CASCADE,\n  PRIMARY KEY (Id)\n);\n",
			}),
			wantStatus:  Fail,
			wantSummary: oneFound,
			wantDetails: []string{
				"schema/migrations/000002_Announcements.up.sql: Announcements declares its foreign key to Tenants with ON DELETE CASCADE, so the database deletes the rows of Announcements with the row of Tenants they name. " + inCode("foreign key"),
			},
		},
		{
			name: "a rule a later migration adds is named in that migration",
			files: tenanted(map[string]string{
				"schema/migrations/000003_Rule.up.sql": "ALTER TABLE Announcements DROP CONSTRAINT FK_Announcements_TenantId;\nALTER TABLE Announcements ADD CONSTRAINT FK_Announcements_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants (Id) ON DELETE CASCADE;\n",
			}),
			wantStatus:  Fail,
			wantSummary: oneFound,
			wantDetails: []string{strings.Replace(announced, "000002_Announcements", "000003_Rule", 1) + inCode("foreign key")},
		},
		{
			name: "a rule a later migration takes off",
			files: tenanted(map[string]string{
				"schema/migrations/000002_Announcements.up.sql": announcementsTable,
				"schema/migrations/000003_Rule.up.sql":          "ALTER TABLE Announcements DROP CONSTRAINT FK_Announcements_TenantId;\nALTER TABLE Announcements ADD CONSTRAINT FK_Announcements_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants (Id);\n",
			}),
			wantStatus:  Pass,
			wantSummary: passed,
		},
		{
			name: "a rule on a link to a table no struct declares is counted, not judged",
			files: tenanted(map[string]string{
				"schema/migrations/000003_Sessions.up.sql": "CREATE TABLE Users (\n  Id STRING(36) NOT NULL,\n) PRIMARY KEY (Id);\nCREATE TABLE Sessions (\n  Id STRING(36) NOT NULL,\n  UserId STRING(36) NOT NULL,\n  CONSTRAINT FK_Sessions_UserId FOREIGN KEY (UserId) REFERENCES Users(Id) ON DELETE CASCADE,\n) PRIMARY KEY (Id);\n",
			}),
			wantStatus:  Pass,
			wantSummary: passed + "; 1 ON DELETE CASCADE rule(s) on a link to a table the application declares no record for were not judged",
		},
		{
			name: "an application with no tenant record, with a rule between two records",
			files: map[string]string{
				"cmd/generate/main.go":                 site,
				"pkg/resources/lenses.go":              globalResource,
				"pkg/resources/lens_marks.go":          lensMarkResource,
				"schema/migrations/000001_Lens.up.sql": "CREATE TABLE Lenses (\n  Id STRING(36) NOT NULL,\n) PRIMARY KEY (Id);\nCREATE TABLE LensMarks (\n  Id STRING(36) NOT NULL,\n  LensId STRING(36) NOT NULL,\n  CONSTRAINT FK_LensMarks_LensId FOREIGN KEY (LensId) REFERENCES Lenses(Id) ON DELETE CASCADE,\n) PRIMARY KEY (Id);\n",
			},
			wantStatus:  Fail,
			wantSummary: oneFound,
			wantDetails: []string{
				"schema/migrations/000001_Lens.up.sql: LensMarks declares FK_LensMarks_LensId to Lenses with ON DELETE CASCADE, so the database deletes the rows of LensMarks with the row of Lenses they name. " + inCode("foreign key"),
			},
		},
		{
			name: "two findings come in table order",
			files: tenanted(map[string]string{
				"schema/migrations/000002_Announcements.up.sql": announcementsTable,
				"schema/migrations/000003_Replies.up.sql":       repliesTable,
			}),
			wantStatus:  Fail,
			wantSummary: "2 foreign key(s) or interleave(s) have the database delete rows with one of the application's records",
			wantDetails: []string{
				announced + inCode("foreign key"),
				"schema/migrations/000003_Replies.up.sql: Replies declares FK_Replies_AnnouncementId to Announcements with ON DELETE CASCADE, so the database deletes the rows of Replies with the row of Announcements they name. " + inCode("foreign key"),
			},
		},
		{
			name: "a migration source that is not a file is named",
			files: tenanted(map[string]string{
				"cmd/generate/main.go": strings.Replace(site, "file://schema/migrations", "gs://bucket/migrations", 1),
			}),
			wantStatus:  Warn,
			wantSummary: passed + "; 1 migration source(s) could not be read",
			wantDetails: []string{
				"cmd/generate/main.go: migration source gs://bucket/migrations is not a file:// path, so the delete rules of its tables were not read",
			},
		},
		{
			name: "two sites over one migration source report a finding once",
			files: map[string]string{
				"cmd/generate/console/main.go":                  siteProgram("console"),
				"cmd/generate/portal/main.go":                   siteProgram("portal"),
				"apps/console/pkg/resources/tenants.go":         tenantRecord,
				"apps/portal/pkg/resources/tenants.go":          tenantRecord,
				"schema/migrations/000001_Tenants.up.sql":       tenantsTable,
				"schema/migrations/000002_Announcements.up.sql": announcementsTable,
			},
			wantStatus:  Fail,
			wantSummary: oneFound,
			wantDetails: []string{announced + inCode("foreign key")},
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
			got := noDeleteCascade{}.Run(context.Background(), &Env{App: a})
			want := Result{Name: noDeleteCascadeName, Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestNoDeleteCascade_lodestar holds the demonstration application to the rule as it
// stands: no foreign key and no interleave of its records says ON DELETE CASCADE, and the
// rules it keeps are on links to the library's tables (sessions, role grants, the groups
// a hangar pass is held by), counted and not judged. A rule added on a link to a record
// fails here before it fails in the pipeline.
func TestNoDeleteCascade_lodestar(t *testing.T) {
	t.Parallel()

	a, err := app.Discover(filepath.Join("..", "..", "..", "..", "lodestar"))
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}
	got := noDeleteCascade{}.Run(context.Background(), &Env{App: a})
	want := Result{
		Name: noDeleteCascadeName, Status: Pass,
		Summary: "no foreign key or interleave has the database delete rows with one of the application's records; 17 ON DELETE CASCADE rule(s) on a link to a table the application declares no record for were not judged",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Run() mismatch (-want +got):\n%s", diff)
	}
}
