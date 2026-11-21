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

// TestTenantDeleteRule holds every table that carries a tenant's key to the delete rule
// on its foreign key to the tenant table: the check fails on a table that declares the
// key with no rule and on one that declares no key at all, in whichever migration says
// so last, and it judges neither the directories record, whose row the generated delete
// removes, nor a resource with no table. A table it cannot read is a warning that names
// it, never a pass. The tables that reach their tenant through another row are held by
// TestTenantDeleteRule_throughAnotherRow.
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
			wantSummary: "1 table(s) that hold a tenant's rows do not go with the tenant",
			wantDetails: []string{
				"pkg/resources/announcements.go:8: Announcement keeps its tenant's key in TenantId, and schema/migrations/000002_Announcements.up.sql declares FK_Announcements_TenantId from Announcements(TenantId) to Tenants with no delete rule, so the delete of a tenant that still holds one of the table's rows is refused; say ON DELETE CASCADE on that foreign key, so a tenant that is deleted takes the table's rows with it",
			},
		},
		{
			name:        "one table that keeps the key with no foreign key at all",
			files:       tenanted(map[string]string{"schema/migrations/000002_Announcements.up.sql": withoutKey}),
			wantStatus:  Fail,
			wantSummary: "1 table(s) that hold a tenant's rows do not go with the tenant",
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
			wantSummary: "1 table(s) that hold a tenant's rows do not go with the tenant",
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
			wantSummary: "1 table(s) that hold a tenant's rows do not go with the tenant",
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
			name: "the directories record and a computed resource are not judged",
			files: tenanted(map[string]string{
				"pkg/resources/tenant_directories.go":         directoriesResource,
				"pkg/resources/digests.go":                    computedResource,
				"schema/migrations/000003_Directories.up.sql": "CREATE TABLE TenantDirectories (\n  Id STRING(36) NOT NULL,\n  TenantId STRING(36) NOT NULL,\n  CONSTRAINT FK_TenantDirectories_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants(Id),\n) PRIMARY KEY (Id);\n",
			}),
			wantStatus:  Pass,
			wantSummary: "1 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants)",
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

// TestTenantDeleteRule_throughAnotherRow holds a table that reaches its tenant through
// another row to the delete rule on every link of its way: its own foreign key to the
// row it names, that row's to the next, and the last one's to the tenant table. The
// check passes a way whose links all carry the rule (a foreign key with it, or an
// interleave with it), fails naming the first link that does not, for each table whose
// way passes it, and warns, never passes, where a link cannot be read: a table of the
// way no migration creates, a column that declares no foreign key, a field the path
// names that the row's struct does not hold.
func TestTenantDeleteRule_throughAnotherRow(t *testing.T) {
	t.Parallel()

	site := program("pkg/resources",
		`generation.GenerateHandlers("app"),`,
		`generation.GenerateRoutes("pkg/router", "api"),`,
	)
	const (
		announcements = "schema/migrations/000002_Announcements.up.sql"
		replies       = "schema/migrations/000003_Replies.up.sql"
		reactions     = "schema/migrations/000004_Reactions.up.sql"
		passed        = "1 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants); 2 table(s) that reach their tenant through another row go with it too (ON DELETE CASCADE on every link of the way)"
		reply         = "pkg/resources/replies.go:8: Reply reaches its tenant through another row (@domain(via: TenantID) on AnnouncementId),"
		reaction      = "pkg/resources/reactions.go:9: Reaction reaches its tenant through another row (@domain(via: AnnouncementID.TenantID) on ReplyId),"
	)
	chain := func(files map[string]string) map[string]string {
		all := map[string]string{
			"cmd/generate/main.go":                      site,
			"pkg/resources/tenants.go":                  tenantRecord,
			"pkg/resources/announcements.go":            keyedResource,
			"pkg/resources/replies.go":                  throughResource,
			"pkg/resources/reactions.go":                throughTwoResource,
			"schema/migrations/000001_Tenants.up.sql":   tenantsTable,
			"schema/migrations/000001_Tenants.down.sql": "DROP TABLE Tenants;\n",
			announcements:                               announcementsTable,
			replies:                                     repliesTable,
			reactions:                                   reactionsTable,
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
	without := func(table string) string {
		return strings.Replace(table, " ON DELETE CASCADE", "", 1)
	}

	tests := []struct {
		name        string
		files       map[string]string
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name:        "every link of both ways carries the rule",
			files:       chain(nil),
			wantStatus:  Pass,
			wantSummary: passed,
		},
		{
			name:        "the table's own link has no rule",
			files:       chain(map[string]string{reactions: without(reactionsTable)}),
			wantStatus:  Fail,
			wantSummary: "1 table(s) that hold a tenant's rows do not go with the tenant",
			wantDetails: []string{
				reaction + " and schema/migrations/000004_Reactions.up.sql declares FK_Reactions_ReplyId from Reactions(ReplyId) to Replies with no delete rule, so the delete of a tenant is refused while a row of Reactions still stands; say ON DELETE CASCADE on that foreign key, as on every link from Reactions to Tenants, so a tenant that is deleted takes Reaction's rows with it",
			},
		},
		{
			name:        "a link in the middle of the way has no rule: each table whose way passes it names it",
			files:       chain(map[string]string{replies: without(repliesTable)}),
			wantStatus:  Fail,
			wantSummary: "2 table(s) that hold a tenant's rows do not go with the tenant",
			wantDetails: []string{
				reaction + " and schema/migrations/000003_Replies.up.sql declares FK_Replies_AnnouncementId from Replies(AnnouncementId) to Announcements with no delete rule, so the delete of a tenant is refused while a row of Replies still stands; say ON DELETE CASCADE on that foreign key, as on every link from Reactions to Tenants, so a tenant that is deleted takes Reaction's rows with it",
				reply + " and schema/migrations/000003_Replies.up.sql declares FK_Replies_AnnouncementId from Replies(AnnouncementId) to Announcements with no delete rule, so the delete of a tenant is refused while a row of Replies still stands; say ON DELETE CASCADE on that foreign key, as on every link from Replies to Tenants, so a tenant that is deleted takes Reply's rows with it",
			},
		},
		{
			name:        "the last link, to the tenant table, has no rule: the table that carries the key and both that reach it fail",
			files:       chain(map[string]string{announcements: without(announcementsTable)}),
			wantStatus:  Fail,
			wantSummary: "3 table(s) that hold a tenant's rows do not go with the tenant",
			wantDetails: []string{
				"pkg/resources/announcements.go:8: Announcement keeps its tenant's key in TenantId, and schema/migrations/000002_Announcements.up.sql declares FK_Announcements_TenantId from Announcements(TenantId) to Tenants with no delete rule, so the delete of a tenant that still holds one of the table's rows is refused; say ON DELETE CASCADE on that foreign key, so a tenant that is deleted takes the table's rows with it",
				reaction + " and schema/migrations/000002_Announcements.up.sql declares FK_Announcements_TenantId from Announcements(TenantId) to Tenants with no delete rule, so the delete of a tenant is refused while a row of Announcements still stands; say ON DELETE CASCADE on that foreign key, as on every link from Reactions to Tenants, so a tenant that is deleted takes Reaction's rows with it",
				reply + " and schema/migrations/000002_Announcements.up.sql declares FK_Announcements_TenantId from Announcements(TenantId) to Tenants with no delete rule, so the delete of a tenant is refused while a row of Announcements still stands; say ON DELETE CASCADE on that foreign key, as on every link from Replies to Tenants, so a tenant that is deleted takes Reply's rows with it",
			},
		},
		{
			name: "the last column of the way declares no foreign key to the tenant table",
			files: chain(map[string]string{
				announcements:                strings.Replace(announcementsTable, "  CONSTRAINT FK_Announcements_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants(Id) ON DELETE CASCADE,\n", "", 1),
				reactions:                    "",
				"pkg/resources/reactions.go": "",
			}),
			wantStatus:  Fail,
			wantSummary: "2 table(s) that hold a tenant's rows do not go with the tenant",
			wantDetails: []string{
				"pkg/resources/announcements.go:8: Announcement keeps its tenant's key in TenantId, and schema/migrations/000002_Announcements.up.sql declares no foreign key from Announcements(TenantId) to Tenants, so the rows of a tenant that is deleted stay behind; declare one with the delete rule (FOREIGN KEY (TenantId) REFERENCES Tenants(<its key>) ON DELETE CASCADE), so a tenant that is deleted takes the table's rows with it",
				reply + " and schema/migrations/000002_Announcements.up.sql declares no foreign key from Announcements(TenantId) to Tenants, so the rows of a tenant that is deleted stay behind; where TenantId is the column that holds the tenant's key, declare one with the delete rule (FOREIGN KEY (TenantId) REFERENCES Tenants(<its key>) ON DELETE CASCADE), and where it names another row the path stops one field short of that key; so a tenant that is deleted takes Reply's rows with it",
			},
		},
		{
			name: "a link that is an interleave with the rule, beside a foreign key on the same column that says nothing",
			files: chain(map[string]string{
				reactions: "CREATE TABLE Reactions (\n  ReplyId STRING(36) NOT NULL,\n  Id STRING(36) NOT NULL,\n  CONSTRAINT FK_Reactions_ReplyId FOREIGN KEY (ReplyId) REFERENCES Replies(Id),\n) PRIMARY KEY (ReplyId, Id),\n  INTERLEAVE IN PARENT Replies ON DELETE CASCADE;\n",
			}),
			wantStatus:  Pass,
			wantSummary: passed,
		},
		{
			name: "a link that is an interleave alone, with the rule",
			files: chain(map[string]string{
				reactions: "CREATE TABLE Reactions (\n  ReplyId STRING(36) NOT NULL,\n  Id STRING(36) NOT NULL,\n) PRIMARY KEY (ReplyId, Id),\n  INTERLEAVE IN PARENT Replies ON DELETE CASCADE;\n",
			}),
			wantStatus:  Pass,
			wantSummary: passed,
		},
		{
			name: "a link that is an interleave without the rule",
			files: chain(map[string]string{
				reactions: "CREATE TABLE Reactions (\n  ReplyId STRING(36) NOT NULL,\n  Id STRING(36) NOT NULL,\n) PRIMARY KEY (ReplyId, Id),\n  INTERLEAVE IN PARENT Replies;\n",
			}),
			wantStatus:  Fail,
			wantSummary: "1 table(s) that hold a tenant's rows do not go with the tenant",
			wantDetails: []string{
				reaction + " and schema/migrations/000004_Reactions.up.sql interleaves Reactions in Replies with no delete rule, so the delete of a tenant is refused while a row of Reactions still stands; say ON DELETE CASCADE on the interleave, as on every link from Reactions to Tenants, so a tenant that is deleted takes Reaction's rows with it",
			},
		},
		{
			name: "a foreign key that writes its table's name in another case is followed",
			files: chain(map[string]string{
				reactions: strings.Replace(reactionsTable, "REFERENCES Replies(Id)", "REFERENCES replies (Id)", 1),
			}),
			wantStatus:  Pass,
			wantSummary: passed,
		},
		{
			name:        "a table of the way that no migration creates is named, not passed",
			files:       chain(map[string]string{replies: ""}),
			wantStatus:  Warn,
			wantSummary: "1 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants); 2 could not be read",
			wantDetails: []string{
				reaction + " no migration under file://schema/migrations creates Replies, a table of that way, so the delete rules of the way were not read",
				reply + " no migration under file://schema/migrations creates Replies, a table of that way, so the delete rules of the way were not read",
			},
		},
		{
			name: "a column of the way that declares no foreign key is named, not passed",
			files: chain(map[string]string{
				reactions: "CREATE TABLE Reactions (\n  Id STRING(36) NOT NULL,\n  ReplyId STRING(36) NOT NULL,\n) PRIMARY KEY (Id);\n",
			}),
			wantStatus:  Warn,
			wantSummary: "1 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants); 1 table(s) that reach their tenant through another row go with it too (ON DELETE CASCADE on every link of the way); 1 could not be read",
			wantDetails: []string{
				reaction + " and schema/migrations/000004_Reactions.up.sql declares no foreign key on Reactions(ReplyId) and interleaves Reactions in no table, so the row it names could not be followed and the delete rules of the way were not read",
			},
		},
		{
			name: "a field the path names that the row's struct does not hold is named, not passed",
			files: chain(map[string]string{
				"pkg/resources/reactions.go": strings.Replace(throughTwoResource, "via: AnnouncementID.TenantID", "via: ThreadID.TenantID", 1),
			}),
			wantStatus:  Warn,
			wantSummary: "1 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants); 1 table(s) that reach their tenant through another row go with it too (ON DELETE CASCADE on every link of the way); 1 could not be read",
			wantDetails: []string{
				"pkg/resources/reactions.go:9: Reaction reaches its tenant through another row (@domain(via: ThreadID.TenantID) on ReplyId), and no struct of pkg/resources whose table is Replies has a field ThreadID with a spanner tag (a struct declared in another directory is not read), so the way could not be followed past Replies and its delete rules were not read",
			},
		},
		{
			name:        "two links of one way have no rule: the table names the first, its own",
			files:       chain(map[string]string{reactions: without(reactionsTable), replies: without(repliesTable)}),
			wantStatus:  Fail,
			wantSummary: "2 table(s) that hold a tenant's rows do not go with the tenant",
			wantDetails: []string{
				reaction + " and schema/migrations/000004_Reactions.up.sql declares FK_Reactions_ReplyId from Reactions(ReplyId) to Replies with no delete rule, so the delete of a tenant is refused while a row of Reactions still stands; say ON DELETE CASCADE on that foreign key, as on every link from Reactions to Tenants, so a tenant that is deleted takes Reaction's rows with it",
				reply + " and schema/migrations/000003_Replies.up.sql declares FK_Replies_AnnouncementId from Replies(AnnouncementId) to Announcements with no delete rule, so the delete of a tenant is refused while a row of Replies still stands; say ON DELETE CASCADE on that foreign key, as on every link from Replies to Tenants, so a tenant that is deleted takes Reply's rows with it",
			},
		},
		{
			name: "a link a later migration adds with the rule is read",
			files: chain(map[string]string{
				reactions: "CREATE TABLE Reactions (\n  Id STRING(36) NOT NULL,\n  ReplyId STRING(36) NOT NULL,\n) PRIMARY KEY (Id);\n",
				"schema/migrations/000005_ReactionsReply.up.sql": "ALTER TABLE Reactions ADD CONSTRAINT FK_Reactions_ReplyId FOREIGN KEY (ReplyId) REFERENCES Replies(Id) ON DELETE CASCADE;\n",
			}),
			wantStatus:  Pass,
			wantSummary: passed,
		},
		{
			name: "a later migration that writes the table and the constraint in another case takes the rule away",
			files: chain(map[string]string{
				"schema/migrations/000005_ReactionsReply.up.sql": "ALTER TABLE reactions DROP CONSTRAINT fk_reactions_replyid;\nALTER TABLE reactions ADD CONSTRAINT FK_Reactions_ReplyId FOREIGN KEY (ReplyId) REFERENCES Replies(Id);\n",
			}),
			wantStatus:  Fail,
			wantSummary: "1 table(s) that hold a tenant's rows do not go with the tenant",
			wantDetails: []string{
				reaction + " and schema/migrations/000005_ReactionsReply.up.sql declares FK_Reactions_ReplyId from Reactions(ReplyId) to Replies with no delete rule, so the delete of a tenant is refused while a row of Reactions still stands; say ON DELETE CASCADE on that foreign key, as on every link from Reactions to Tenants, so a tenant that is deleted takes Reaction's rows with it",
			},
		},
		{
			name: "a foreign key that holds the column beside another is a link",
			files: chain(map[string]string{
				reactions: "CREATE TABLE Reactions (\n  Id STRING(36) NOT NULL,\n  ReplyId STRING(36) NOT NULL,\n  Kind STRING(16) NOT NULL,\n  CONSTRAINT FK_Reactions_Reply FOREIGN KEY (ReplyId, Kind) REFERENCES Replies(Id, Kind) ON DELETE CASCADE,\n) PRIMARY KEY (Id);\n",
			}),
			wantStatus:  Pass,
			wantSummary: passed,
		},
		{
			name: "a column two foreign keys hold is followed through the table whose struct holds the path's field",
			files: chain(map[string]string{
				reactions: strings.Replace(reactionsTable, "  CONSTRAINT FK_Reactions_ReplyId", "  CONSTRAINT FK_Reactions_Thread FOREIGN KEY (ReplyId) REFERENCES Threads(Id),\n  CONSTRAINT FK_Reactions_ReplyId", 1),
			}),
			wantStatus:  Pass,
			wantSummary: passed,
		},
		{
			name: "a table interleaved in the row it names with no rule fails, whatever a foreign key beside the interleave says",
			files: chain(map[string]string{
				reactions: "CREATE TABLE Reactions (\n  ReplyId STRING(36) NOT NULL,\n  Id STRING(36) NOT NULL,\n  CONSTRAINT FK_Reactions_ReplyId FOREIGN KEY (ReplyId) REFERENCES Replies(Id) ON DELETE CASCADE,\n) PRIMARY KEY (ReplyId, Id),\n  INTERLEAVE IN PARENT Replies;\n",
			}),
			wantStatus:  Fail,
			wantSummary: "1 table(s) that hold a tenant's rows do not go with the tenant",
			wantDetails: []string{
				reaction + " and schema/migrations/000004_Reactions.up.sql interleaves Reactions in Replies with no delete rule, so the delete of a tenant is refused while a row of Reactions still stands; say ON DELETE CASCADE on the interleave, as on every link from Reactions to Tenants, so a tenant that is deleted takes Reaction's rows with it",
			},
		},
		{
			name: "a column a foreign key holds is followed by the key, not by an interleave in another table",
			files: chain(map[string]string{
				reactions: "CREATE TABLE Reactions (\n  BoardId STRING(36) NOT NULL,\n  Id STRING(36) NOT NULL,\n  ReplyId STRING(36) NOT NULL,\n  CONSTRAINT FK_Reactions_ReplyId FOREIGN KEY (ReplyId) REFERENCES Replies(Id),\n) PRIMARY KEY (BoardId, Id),\n  INTERLEAVE IN PARENT Boards ON DELETE CASCADE;\n",
			}),
			wantStatus:  Fail,
			wantSummary: "1 table(s) that hold a tenant's rows do not go with the tenant",
			wantDetails: []string{
				reaction + " and schema/migrations/000004_Reactions.up.sql declares FK_Reactions_ReplyId from Reactions(ReplyId) to Replies with no delete rule, so the delete of a tenant is refused while a row of Reactions still stands; say ON DELETE CASCADE on that foreign key, as on every link from Reactions to Tenants, so a tenant that is deleted takes Reaction's rows with it",
			},
		},
		{
			name: "a way with a link without the rule beside a way that cannot be read: the finding, then the note",
			files: chain(map[string]string{
				replies:                      without(repliesTable),
				"pkg/resources/reactions.go": strings.Replace(throughTwoResource, "via: AnnouncementID.TenantID", "via: ThreadID.TenantID", 1),
			}),
			wantStatus:  Fail,
			wantSummary: "1 table(s) that hold a tenant's rows do not go with the tenant",
			wantDetails: []string{
				reply + " and schema/migrations/000003_Replies.up.sql declares FK_Replies_AnnouncementId from Replies(AnnouncementId) to Announcements with no delete rule, so the delete of a tenant is refused while a row of Replies still stands; say ON DELETE CASCADE on that foreign key, as on every link from Replies to Tenants, so a tenant that is deleted takes Reply's rows with it",
				"pkg/resources/reactions.go:9: Reaction reaches its tenant through another row (@domain(via: ThreadID.TenantID) on ReplyId), and no struct of pkg/resources whose table is Replies has a field ThreadID with a spanner tag (a struct declared in another directory is not read), so the way could not be followed past Replies and its delete rules were not read",
			},
		},
		{
			name: "a field that carries the path with no spanner tag is named, not passed",
			files: chain(map[string]string{
				"pkg/resources/replies.go":   strings.Replace(throughResource, " `spanner:\"AnnouncementId\"`", "", 1),
				reactions:                    "",
				"pkg/resources/reactions.go": "",
			}),
			wantStatus:  Warn,
			wantSummary: "1 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants); 1 could not be read",
			wantDetails: []string{
				"pkg/resources/replies.go:8: Reply reaches its tenant through another row (@domain(via: TenantID)), and the field that carries the annotation has no spanner tag, so the column its way starts on, and the delete rules of the way, were not read",
			},
		},
		{
			name: "a migration source that is not a file is named once, and no table of it is named besides",
			files: chain(map[string]string{
				"cmd/generate/main.go": strings.Replace(site, "file://schema/migrations", "gs://bucket/migrations", 1),
			}),
			wantStatus:  Warn,
			wantSummary: "0 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Tenants); 1 could not be read",
			wantDetails: []string{
				"cmd/generate/main.go: migration source gs://bucket/migrations is not a file:// path, so the delete rules of its tables were not read",
			},
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
// stands: its eight tables that carry a sector's key go with the sector, and so do the
// eight that reach their sector through another row, by the rule on every link of each
// one's way. A table added with a sector's key, or a link added to a way, with no rule
// fails here before it fails in the pipeline.
func TestTenantDeleteRule_lodestar(t *testing.T) {
	t.Parallel()

	a, err := app.Discover(filepath.Join("..", "..", "..", "..", "lodestar"))
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}
	got := tenantDeleteRule{}.Run(context.Background(), &Env{App: a})
	want := Result{
		Name: tenantDeleteRuleName, Status: Pass,
		Summary: "8 table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to Sectors); 8 table(s) that reach their tenant through another row go with it too (ON DELETE CASCADE on every link of the way)",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Run() mismatch (-want +got):\n%s", diff)
	}
}
