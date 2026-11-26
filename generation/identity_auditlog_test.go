package generation

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// crewAuditRecordsTable is the crew auth's audit records table, which a fixture's schema
// holds or leaves out.
const crewAuditRecordsTable = "CrewSessionAuditRecords"

// auditLogFiles lists the files of the identity tree the audit log's part of the
// management wrote.
func auditLogFiles(t *testing.T) []string {
	t.Helper()

	var got []string
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		if strings.Contains(file, "audit_record") {
			got = append(got, file)
		}
	}

	return got
}

// Test_prepareIdentity_auditLog pins the audit log's part of an auth's management: the
// whole log, a computed resource beside the methods for the auth whose schema holds the
// table and for no other, which declares no write and no read of a row, filters by the
// five fields the table holds, and reads the log through the identity package; and the
// store's read, in both forms: the statement over the table where the schema holds it,
// and no rows where it does not.
func Test_prepareIdentity_auditLog(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, []string{crewAuditRecordsTable}, membersIdentityTables)...)
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	if diff := cmp.Diff([]string{"crewidentity/crew_listed_audit_records.go"}, auditLogFiles(t)); diff != "" {
		t.Errorf("the sources of the audit log (-want +got):\n%s", diff)
	}
	assertContains(t, "the crew auth's whole audit log", squashSpace(readTreeFile(t, "crewidentity/crew_listed_audit_records.go")), squashEach([]string{
		generationHeader,
		"// CrewListedAuditRecord is one row of the Crew auth's audit log, as a list shows it:\n// one act that changed an account, who holds what, or how people sign in, newest first.",
		"// It is a computed resource: Who, What and DoneTo are written by the library from the\n// row's parts as each row is read (auditlog.Stored.Sentences), so no query selects them,\n" +
			"// and the list is not filtered or sorted by them. It filters by At, Act, ActorUsername,\n// SubjectName and Role, which the table holds, sorts by At and by Role, and shows no\n" +
			"// total. The role file grants it like any resource.",
		"// No field holds a password, a token, a code or a link, and nothing writes the list.",
		"// @computed\n// @suppress(readHandler)\n// @outlet(default)\n// @order(At desc)\n// @page(default: 25, max: 200)\n// @rowWord(audit record)\ntype CrewListedAuditRecord struct {",
		"// @primarykey\n\tID            impulse.UUID `spanner:\"Id\"`",
		"At            time.Time    `spanner:\"At\" allow_filter:\"true\"`",
		"Act           string       `spanner:\"Act\" allow_filter:\"true\"`",
		"Who           string       `spanner:\"Who\"`",
		"What          string       `spanner:\"What\"`",
		"DoneTo        string       `spanner:\"DoneTo\"`",
		"ActorUsername *string      `spanner:\"ActorUsername\" allow_filter:\"true\"`",
		"SubjectKind   string       `spanner:\"SubjectKind\"`",
		"SubjectName   *string      `spanner:\"SubjectName\" allow_filter:\"true\"`",
		"// @enumerate(CrewListedUsers)\n\tUserID *impulse.UUID `spanner:\"UserId\"`",
		"// @roleOf(Crew)\n\tRole       *string `spanner:\"Role\" allow_filter:\"true\"`",
		"TenantID   *string `spanner:\"TenantId\"`",
		"TenantName *string `spanner:\"TenantName\"`",
		"Reason     *string `spanner:\"Reason\"`\n}",
		`return "CrewListedAuditRecords"`,
		"func ListCrewListedAuditRecord(ctx context.Context, qSet *resource.QuerySet[CrewListedAuditRecord], db resource.Client, client *Client) iter.Seq2[*CrewListedAuditRecord, error] {",
		"txn := db.ReadOnlyTransaction()\n\t\tdefer txn.Close()",
		`client.manager.AuditRecords(ctx, txn, identity.TakeAuditQuery(qSet, ""))`,
		"Who: line.Reads.Who, What: line.Reads.What, DoneTo: line.Reads.DoneTo",
	}), []string{"@resource", "@virtual", "@changedBy", "Patch", "Delete", "Buffer("})

	// The store reads the table by the identity package's statement where the schema
	// holds it, and answers no rows where it does not.
	assertContains(t, "the crew auth's store", squashSpace(readTreeFile(t, "crewidentity/store.go")), squashEach([]string{
		"func (store) AuditRecords(ctx context.Context, reader resource.ReadOnlyTransaction, query *identity.AuditQuery) iter.Seq2[*auditlog.Stored, error] {",
		`identity.AuditStatement("CrewSessionAuditRecords", query)`,
		"reader.SpannerReadOnlyTransaction().Query(ctx, spanner.Statement{SQL: sql, Params: params})",
		"OtherRoleNames    []spanner.NullString `spanner:\"OtherRoleNames\"`",
		"Source: eventsource.Stored{",
	}), nil)
	assertContains(t, "the members auth's store", squashSpace(readTreeFile(t, "membersidentity/store.go")), squashEach([]string{
		"// The schema holds no audit records table for the Members auth\n// (MembersSessionAuditRecords): its log has no rows.",
		"func (store) AuditRecords(context.Context, resource.ReadOnlyTransaction, *identity.AuditQuery) iter.Seq2[*auditlog.Stored, error] {\n\treturn func(func(*auditlog.Stored, error) bool) {}\n}",
	}), []string{"identity.AuditStatement("})
}

// Test_resourceGenerator_auditLogSources pins where the audit log's lists are written:
// the whole log beside the methods, and a tenant's beside it where the application has
// a tenant record, each named for its resource in the plural, and each written only where
// the schema holds the table (writeIdentitySources writes a file only where the instance
// holds every table it needs).
func Test_resourceGenerator_auditLogSources(t *testing.T) {
	t.Parallel()

	r := identityFixtureGenerator(t)
	whole := identitySourceFile{
		dir: "pkg/identity/crewidentity", name: "crew_listed_audit_records",
		template: identityListedAuditRecordsTemplate, needs: []identityTable{identityAuditRecords},
	}
	tests := []struct {
		name string
		inst *identityInstance
		want []identitySourceFile
	}{
		{name: "an application without tenants", inst: &identityInstance{auth: "Crew", methods: "pkg/identity/crewidentity"}, want: []identitySourceFile{whole}},
		{
			name: "an application with a tenant record",
			inst: &identityInstance{auth: "Crew", methods: "pkg/identity/crewidentity", tenant: &identityTenant{name: "Sector", table: "Sectors", key: "Id"}},
			want: []identitySourceFile{whole, {
				dir: "pkg/identity/crewidentity", name: "crew_sector_audit_records",
				template: identityTenantAuditRecordsTemplate, needs: []identityTable{identityAuditRecords}, tenant: true,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, r.auditLogSources(tt.inst), cmp.AllowUnexported(identitySourceFile{})); diff != "" {
				t.Errorf("auditLogSources() (-want +got):\n%s", diff)
			}
		})
	}
}

// TestIdentityTenantAuditRecords_template pins a tenant's log: the whole log's fields but
// the tenant's, under the tenant's scope, read for the tenant the request was checked in,
// and refused in a scope that names none.
func TestIdentityTenantAuditRecords_template(t *testing.T) {
	t.Parallel()

	data := announceData(false, identityUsers, identityAuditRecords)
	data.tenant = &identityTenant{name: "Sector", table: "Sectors", key: "Id", word: "sector"}
	data.ForTenant = true
	c := &client{}
	out, err := c.generateTemplateOutput("identityTenantAuditRecordsTemplate", identityTenantAuditRecordsTemplate, data)
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	assertContains(t, "a tenant's log", squashSpace(string(out)), squashEach([]string{
		"// CrewSectorAuditRecord is one row of the Crew auth's audit log done in one\n// sector, as the sector reads it",
		"// It is a tenant's list: served under the address of one sector, it lists the\n// rows done in the sector the request was checked in, and no other.",
		"// @computed\n// @permissionScope(domain)\n// @suppress(readHandler)\n// @outlet(default)\n// @order(At desc)\n// @page(default: 25, max: 200)\n// @rowWord(audit record)\ntype CrewSectorAuditRecord struct {",
		"// @roleOf(Crew)\n\tRole *string `spanner:\"Role\" allow_filter:\"true\"`",
		`return "CrewSectorAuditRecords"`,
		"domain, ok := qSet.Scope().Domain()\n\t\tif !ok {\n\t\t\tyield(nil, errors.Newf(\"%s lists the audit records of one tenant and was checked in a scope that names none\", qSet.Resource()))",
		"client.manager.AuditRecords(ctx, txn, identity.TakeAuditQuery(qSet, domain))",
		"// No field holds a password, a token, a code or a link, and nothing writes the list.",
	}), []string{"TenantID", "TenantName *string", "Patch", "Delete"})
}

// Test_identityOn_auditLog pins the description's key of the audit log: the whole log on
// the whole application's side and a tenant's on a tenant's, where the outlet serves
// each, and nothing where it serves neither.
func Test_identityOn_auditLog(t *testing.T) {
	t.Parallel()

	crew := &identityInstance{auth: "Crew", outlets: []string{"console"}, tenant: &identityTenant{name: "Sector", table: "Sectors", key: "Id"}}
	tests := []struct {
		name   string
		served []string
		want   []*tsAPIIdentitySide
	}{
		{
			name:   "both lists served",
			served: []string{"CrewSessionUsers", "CrewListedAuditRecords", "CrewSectorUsers", "CrewSectorAuditRecords"},
			want: []*tsAPIIdentitySide{
				{Scope: accesstypes.GlobalPermissionScope, Lists: []identityPart{{Key: "users", Name: "CrewSessionUsers"}, {Key: "auditLog", Name: "CrewListedAuditRecords"}}},
				{Scope: accesstypes.DomainPermissionScope, Lists: []identityPart{{Key: "users", Name: "CrewSectorUsers"}, {Key: "auditLog", Name: "CrewSectorAuditRecords"}}},
			},
		},
		{
			name:   "neither served",
			served: []string{"CrewSessionUsers", "CrewSectorUsers"},
			want: []*tsAPIIdentitySide{
				{Scope: accesstypes.GlobalPermissionScope, Lists: []identityPart{{Key: "users", Name: "CrewSessionUsers"}}},
				{Scope: accesstypes.DomainPermissionScope, Lists: []identityPart{{Key: "users", Name: "CrewSectorUsers"}}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data := &tsAPIData{}
			for _, name := range tt.served {
				data.Resources = append(data.Resources, &tsAPIResource{Name: name})
			}
			gen := &typescriptGenerator{client: &client{identity: []*identityInstance{crew}}, outletName: "console"}
			got := gen.identityOn(data)
			if len(got) != 1 {
				t.Fatalf("identityOn() = %d auths, want the crew's", len(got))
			}
			if diff := cmp.Diff(tt.want, got[0].Sides, cmp.AllowUnexported(identityPart{})); diff != "" {
				t.Errorf("identityOn() sides mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
