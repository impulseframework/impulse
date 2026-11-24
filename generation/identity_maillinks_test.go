package generation

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// mailLinkStateSQL is the column that says what became of a link, as both lists of the
// mailed links write it over the alias l.
const mailLinkStateSQL = "  CASE\n" +
	"    WHEN l.MailFailedAt IS NOT NULL THEN 'notMailed'\n" +
	"    WHEN l.WithdrawnAt IS NOT NULL THEN 'withdrawn'\n" +
	"    WHEN l.ConsumedAt IS NOT NULL THEN 'accepted'\n" +
	"    WHEN l.ExpiresAt <= CURRENT_TIMESTAMP() THEN 'expired'\n" +
	"    ELSE 'open'\n" +
	"  END AS State"

// Test_prepareIdentity_listedMailLinks pins the list of the links an auth mailed that a
// page reads: a view beside the mail links table's declaration that repeats its
// columns, says of each link what became of it (the first of not mailed, withdrawn,
// accepted, expired and open), names who sent an invitation by their name or, where they
// have none, their username, and says whether the invitation makes a tenant; it filters
// by the kind, the state, the address and when the link was sent, and lists the newest
// first. Withdraw and Send again name a row of it, and the description names it for the
// invitations. A tenant's list of its invitations says the same of each.
func Test_prepareIdentity_listedMailLinks(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	crew := []string{
		"// @virtual\n\t// @outlet(default)\n\t// @order(CreatedAt desc)\n\t// @page(default: 25, max: 200)\n\t// @rowWord(mailed link)\n\tCrewListedMailLink struct {",
		"// CrewListedMailLink is one link the Crew auth mailed as a list shows it: a\n\t// row of CrewSessionMailLinks with the columns CrewSessionMailLink serves of it, what\n\t// became of it, and who sent it.",
		"// @primarykey\n\t\tID impulse.UUID `spanner:\"Id\" index:\"true\"`",
		"Kind string `spanner:\"Kind\" index:\"true\"`",
		"State string `spanner:\"State\" index:\"true\"`",
		"Email string `spanner:\"Email\" index:\"true\"`",
		"// @attribute(role)\n\t\t// @roleOf(Crew)\n\t\tRoleNames []string `spanner:\"RoleNames\"`",
		"SentBy *string `spanner:\"SentBy\"`",
		"MakesTenant bool `spanner:\"MakesTenant\"`",
		"CreatedAt time.Time `spanner:\"CreatedAt\" index:\"true\"`",
		"MailFailedAt *time.Time `spanner:\"MailFailedAt\"`\n\t\tWithdrawnAt *time.Time `spanner:\"WithdrawnAt\"`\n\t}",
		"const crewListedMailLinkQuery = `\nSELECT\n  l.Id AS Id,\n  l.Kind AS Kind,\n" + mailLinkStateSQL + ",\n  l.Email AS Email,",
		"  COALESCE(NULLIF(iu.Name, ''), iu.Username) AS SentBy,\n  (l.NewTenant IS NOT NULL) AS MakesTenant,",
		"FROM CrewSessionMailLinks l\nLEFT JOIN CrewSessionUsers iu ON iu.Id = l.InvitedByUserId\n`",
		"func (CrewListedMailLink) Subquery() (query string, params map[string]any) {\n\treturn crewListedMailLinkQuery, nil\n}",
	}
	assertContains(t, "the crew auth's list of its mailed links", squashSpace(readTreeFile(t, "internal/crewresources/crew_listed_mail_links.go")), squashEach(crew), []string{"WHERE"})

	// Withdraw and Send again name a row of the list a page shows.
	for _, file := range []string{"crewidentity/withdraw_crew_invitation.go", "crewidentity/resend_crew_invitation.go"} {
		assertContains(t, file, squashSpace(readTreeFile(t, file)), squashEach([]string{"// @target(CrewListedMailLink)\n\t\tInvitationID impulse.UUID"}), []string{"@target(CrewSessionMailLink)"})
	}

	// A tenant's invitations say what became of each and who sent it, and keep the fact
	// the grants' conditions read.
	tenant := []string{
		"State\n\t// says what became of the invitation (open, accepted, withdrawn, expired,\n\t// notMailed), and SentBy who sent it.",
		"SentBy *string `spanner:\"SentBy\"`",
		"WithdrawnAt *time.Time `spanner:\"WithdrawnAt\"`",
		"State string `spanner:\"State\" index:\"true\"`",
		"// @attribute(pending)\n\t\tPending bool `spanner:\"Pending\" index:\"true\"`",
		"  COALESCE(NULLIF(iu.Name, ''), iu.Username) AS SentBy,",
		"  l.WithdrawnAt AS WithdrawnAt,\n" + mailLinkStateSQL + ",\n  (l.ConsumedAt IS NULL AND l.ExpiresAt > CURRENT_TIMESTAMP()) AS Pending",
		"FROM CrewSessionMailLinks l\nLEFT JOIN CrewSessionUsers iu ON iu.Id = l.InvitedByUserId\nWHERE l.Kind = 'invitation' AND l.TenantId IS NOT NULL",
	}
	assertContains(t, "a ship's invitations", squashSpace(readTreeFile(t, "internal/crewresources/crew_ship_invitations.go")), squashEach(tenant), []string{"MakesTenant", "NewTenant"})
}

// mailLinksByTenantColumns are the columns of the mail links table its index by tenant
// holds: the key (TenantId, Kind, CreatedAt), the table's key (Id) and the stored
// columns, as the session library's Spanner migrations, the auth package's migration and
// the example application's baseline write the index.
var mailLinksByTenantColumns = []string{"Id", "TenantId", "Kind", "CreatedAt", "Email", "Name", "RoleNames", "InvitedByUserId", "ExpiresAt", "ConsumedAt", "MailFailedAt", "WithdrawnAt"}

// Test_prepareIdentity_tenantMailLinksReadTheIndex holds a tenant's list of its
// invitations to the mail links index by tenant: every column of the table the list's
// query reads is one the index holds, so a page of the list reads its rows from the
// index and no row of the table.
func Test_prepareIdentity_tenantMailLinksReadTheIndex(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	source := readTreeFile(t, "internal/crewresources/crew_ship_invitations.go")
	start := strings.Index(source, "const crewShipInvitationQuery = `")
	if start < 0 {
		t.Fatal("the ship's invitations hold no query")
	}
	query := source[start+len("const crewShipInvitationQuery = `"):]
	query = query[:strings.Index(query, "`")]
	read := regexp.MustCompile(`\bl\.([A-Za-z]+)`).FindAllStringSubmatch(query, -1)
	if len(read) == 0 {
		t.Fatalf("the ship's invitations read no column of the mail links table: %s", query)
	}
	for _, column := range read {
		if !slices.Contains(mailLinksByTenantColumns, column[1]) {
			t.Errorf("the tenant's invitations read l.%s, which the mail links index by tenant does not hold: add the column to the index's STORING list in the session library's Spanner migrations, in the auth package's migration and in the example application's baseline, and then to this test", column[1])
		}
	}
}

// TestIdentityListedMailLinks_withoutUsers pins the list of the mailed links of an auth
// whose schema holds no users table: nobody is named as a link's sender, and nothing is
// joined.
func TestIdentityListedMailLinks_withoutUsers(t *testing.T) {
	t.Parallel()

	c := &client{}
	out, err := c.generateTemplateOutput("identityListedMailLinksTemplate", identityListedMailLinksTemplate, announceData(false, identityMailLinks))
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	assertContains(t, "the list", squashSpace(string(out)), squashEach([]string{
		"  CAST(NULL AS STRING) AS SentBy,\n  (l.NewTenant IS NOT NULL) AS MakesTenant,",
		"FROM CrewSessionMailLinks l\n`",
	}), []string{"JOIN", "iu."})
}

// Test_resourceGenerator_mailLinkListSources pins where the list of an auth's mailed
// links is written: one file in the declarations package, named as the views beside the
// tables are, which a schema without the mail links table is written none of
// (writeIdentitySources writes a file only where the instance holds every table it
// needs).
func Test_resourceGenerator_mailLinkListSources(t *testing.T) {
	t.Parallel()

	r := identityFixtureGenerator(t)
	inst := &identityInstance{auth: "Crew", resources: "pkg/identity/internal/crewresources"}
	want := []identitySourceFile{{
		dir: "pkg/identity/internal/crewresources", name: "crew_listed_mail_links",
		template: identityListedMailLinksTemplate, needs: []identityTable{identityMailLinks},
	}}
	if diff := cmp.Diff(want, r.mailLinkListSources(inst), cmp.AllowUnexported(identitySourceFile{})); diff != "" {
		t.Errorf("mailLinkListSources() (-want +got):\n%s", diff)
	}
}
