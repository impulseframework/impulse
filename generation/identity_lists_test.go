package generation

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// A tenant's lists of an auth's generated management: the tenant record read before the
// packages load, the lists an instance carries, their names and sources, and what a run
// does with them when the tenant record goes.

// writeResourcesPackage writes a resources package of one file into dir, for the read of
// the tenant record off its syntax.
func writeResourcesPackage(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%s) error = %v", name, err)
		}
	}
}

// shipTenantSource declares Ship the tenant record, keyed by an id its create path
// generates, in the grouped form the resources packages use.
const shipTenantSource = `package resources

import "github.com/impulseframework/impulse"

type (
	// Ship is the tenant record.
	//
	// @resource
	// @tenant
	Ship struct {
		ID impulse.UUID ` + "`spanner:\"Id\"`" + `
	}

	// Dock is a table of the application's.
	//
	// @resource
	Dock struct {
		ID string ` + "`spanner:\"Id\"`" + `
	}
)
`

// Test_tenantRecordNameIn pins the read of the tenant record off a package's syntax: the
// struct whose comment carries @tenant as the extraction scans it, in either declaration
// form and with arguments; nothing for a package without one, a directory with no
// package and no directory at all; the files the default build leaves out not read; and
// the refusals.
func Test_tenantRecordNameIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		files   map[string]string
		noDir   bool
		want    string
		wantErr string
	}{
		{name: "no directory", noDir: true},
		{name: "a directory with no Go file", files: map[string]string{"notes.txt": "nothing"}},
		{name: "a grouped declaration", files: map[string]string{"ships.go": shipTenantSource}, want: "Ship"},
		{
			name: "a declaration of its own, the comment on the declaration, with a creation shape",
			files: map[string]string{"sectors.go": `package resources

import "github.com/impulseframework/impulse"

// Sector is the tenant record.
//
// @resource
// @tenant
type Sector struct {
	ID impulse.UUID
}
`},
			want: "Sector",
		},
		{
			name: "two annotations on one line",
			files: map[string]string{"sectors.go": `package resources

import "github.com/impulseframework/impulse"

// @resource @tenant
type Sector struct {
	ID impulse.UUID
}
`},
			want: "Sector",
		},
		{
			name: "no tenant record: a tenant directory, prose that names the annotation, and a type that is no struct",
			files: map[string]string{"sectors.go": `package resources

type (
	// SectorDirectory is the directory a sector declares.
	//
	// @resource
	// @tenantDirectory
	SectorDirectory struct {
		ID string
	}

	// Sector would be the @tenant record of another application.
	//
	// @resource
	Sector struct {
		ID string
	}

	// @tenant
	Key string
)
`},
		},
		{
			name: "a comment the scanner refuses is passed over",
			files: map[string]string{"sectors.go": `package resources

import "github.com/impulseframework/impulse"

// @resource
// @tenant
// @noSuchAnnotation
type Sector struct {
	ID impulse.UUID
}
`},
		},
		{
			name: "a test file and a file the build leaves out are not read",
			files: map[string]string{
				"docks.go":        "package resources\n\n// @resource\ntype Dock struct {\n\tID string\n}\n",
				"sectors_test.go": "package resources\n\nimport \"github.com/impulseframework/impulse\"\n\n// @resource\n// @tenant\ntype Sector struct {\n\tID impulse.UUID\n}\n",
				"ignored.go":      "//go:build ignore\n\npackage resources\n\nimport \"github.com/impulseframework/impulse\"\n\n// @resource\n// @tenant\ntype Region struct {\n\tID impulse.UUID\n}\n",
			},
		},
		{
			name: "two tenant records",
			files: map[string]string{
				"ships.go":   shipTenantSource,
				"sectors.go": "package resources\n\nimport \"github.com/impulseframework/impulse\"\n\n// @resource\n// @tenant\ntype Sector struct {\n\tID impulse.UUID\n}\n",
			},
			wantErr: "structs Sector and Ship each carry @tenant; an application has one tenant record",
		},
		{
			name:    "a file that does not parse",
			files:   map[string]string{"ships.go": "package resources\n\ntype Ship struct {\n"},
			wantErr: "parser.ParseFile()",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "resources")
			if !tt.noDir {
				writeResourcesPackage(t, dir, tt.files)
			}
			got, err := tenantRecordNameIn(dir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("tenantRecordNameIn() error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("tenantRecordNameIn() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("tenantRecordNameIn() = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("no directory named", func(t *testing.T) {
		t.Parallel()

		if got, err := tenantRecordNameIn(""); err != nil || got != "" {
			t.Errorf("tenantRecordNameIn(\"\") = %q, %v, want nothing", got, err)
		}
	})
}

// shipsTable is the tenant record's table as the schema holds it: one key, the
// generated id, which the schema stores as its text in a STRING column.
func shipsTable() *tableMetadata {
	return &tableMetadata{PkCount: 1, Columns: map[string]columnMeta{
		"Id":   {IsPrimaryKey: true, SpannerType: "STRING(36)"},
		"Name": {SpannerType: "STRING(MAX)"},
	}}
}

// Test_identityInstance_refuseListNamesTaken pins the refusal of a tenant record whose
// name gives one of a tenant's lists the name of a declaration over one of the auth's
// tables, and that an ordinary record and an application with none pass.
func Test_identityInstance_refuseListNamesTaken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tenant  *identityTenant
		wantErr string
	}{
		{name: "an application with no tenant record"},
		{name: "a record whose lists meet no declaration", tenant: &identityTenant{name: "Ship"}},
		{
			name:    "a record named Session names a tenant's people as the users table's declaration is named",
			tenant:  &identityTenant{name: "Session"},
			wantErr: "the tenant record Session gives a list a tenant keeps of the auth's people the name CrewSessionUser",
		},
		{
			name:    "a record named User names the roles a tenant gives as the memberships table's declaration is named",
			tenant:  &identityTenant{name: "User"},
			wantErr: "the tenant record User gives a list a tenant keeps of the auth's people the name CrewUserRole",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inst := &identityInstance{auth: "Crew", importPath: "example.com/acme/beacon/pkg/auth/crew", tenant: tt.tenant}
			err := inst.refuseListNamesTaken()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("refuseListNamesTaken() error = %v, want none", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("refuseListNamesTaken() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// Test_readIdentityTenant pins the tenant record as the sources name it: the struct, its
// table by the generator's plural, and the table's one key off the schema, a STRING
// column holding the generated id's text; nil for an application with none; and the
// refusals of a record the lists cannot be written over.
func Test_readIdentityTenant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		table   *tableMetadata
		want    *identityTenant
		wantErr string
	}{
		{name: "no tenant record", source: "package resources\n\n// @resource\ntype Dock struct {\n\tID string\n}\n"},
		{
			// The fixture's package is written outside the module the generator runs in,
			// so the record's fields are not read for an invitation, and the record says
			// why (Test_tenantInvitationIn reads one inside the module).
			name: "the record, its table and its key", source: shipTenantSource, table: shipsTable(),
			want: &identityTenant{name: "Ship", table: "Ships", key: "Id", outlets: []string{"default"}, word: "ship", unreadable: "the resources package is outside the module the generator runs in"},
		},
		{name: "a record whose table the schema does not hold", source: shipTenantSource, wantErr: "struct Ship is the tenant record (@tenant) and the schema holds no table Ships"},
		{
			name:   "a record keyed by two columns",
			source: shipTenantSource,
			table: &tableMetadata{PkCount: 2, Columns: map[string]columnMeta{
				"Fleet": {IsPrimaryKey: true, SpannerType: "STRING(64)"},
				"Id":    {IsPrimaryKey: true, SpannerType: "STRING(64)"},
			}},
			wantErr: "its table Ships has 2 key columns; the tenant record has one key",
		},
		{
			name:    "a record whose key column is no STRING, so it holds no id's text",
			source:  shipTenantSource,
			table:   &tableMetadata{PkCount: 1, Columns: map[string]columnMeta{"Id": {IsPrimaryKey: true, SpannerType: "INT64"}}},
			wantErr: "its key Ships.Id is INT64; the tenant record's key is an id the create path generates, kept as its text, the domain in every tenant-scoped URL",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "resources")
			writeResourcesPackage(t, dir, map[string]string{"fixture.go": tt.source})
			r := identityFixtureGenerator(t, crewIdentityTables...)
			r.resource = packageDir(dir)
			if tt.table != nil {
				r.tableMap["Ships"] = tt.table
			}

			got, err := r.readIdentityTenant()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("readIdentityTenant() error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("readIdentityTenant() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(identityTenant{})); diff != "" {
				t.Errorf("readIdentityTenant() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_identityInstance_lists pins which lists an instance carries: none without a
// tenant record, and each only where the schema holds the tables it reads.
func Test_identityInstance_lists(t *testing.T) {
	t.Parallel()

	ship := &identityTenant{name: "Ship", table: "Ships", key: "Id", outlets: []string{"default"}}
	tests := []struct {
		name   string
		tenant *identityTenant
		tables []identityTable
		want   []string
	}{
		{name: "no tenant record", tables: []identityTable{identityUsers, identityMemberships, identityMailLinks}},
		{name: "every table", tenant: ship, tables: []identityTable{identityUsers, identityMemberships, identityMailLinks}, want: []string{"CrewShipUser", "CrewShipUserRole", "CrewShipHeldRole", "CrewShipInvitation"}},
		{name: "no mail links", tenant: ship, tables: []identityTable{identityUsers, identityMemberships}, want: []string{"CrewShipUser", "CrewShipUserRole", "CrewShipHeldRole"}},
		{name: "no memberships: nobody holds a role in a tenant", tenant: ship, tables: []identityTable{identityUsers, identityMailLinks}, want: []string{"CrewShipInvitation"}},
		{name: "identity links: the identities of the tenant's people", tenant: ship, tables: []identityTable{identityUsers, identityMemberships, identityIdentities}, want: []string{"CrewShipUser", "CrewShipUserRole", "CrewShipHeldRole", "CrewShipIdentity"}},
		{name: "identity links and no memberships: no people to list the identities of", tenant: ship, tables: []identityTable{identityUsers, identityIdentities}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inst := &identityInstance{auth: "Crew", tenant: tt.tenant, tables: make(map[identityTable]bool)}
			for _, kind := range tt.tables {
				inst.tables[kind] = true
			}
			var got []string
			for _, list := range inst.lists() {
				got = append(got, inst.listName(list.suffix))
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("lists() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_identityInstance_tenantMethodSources pins which methods a tenant is given: none
// without a tenant record, each only where the list whose rows it names is declared, and
// each under the conditions the whole application's method of the same act is written
// under.
func Test_identityInstance_tenantMethodSources(t *testing.T) {
	t.Parallel()

	ship := &identityTenant{name: "Ship", table: "Ships", key: "Id", outlets: []string{"default"}}
	every := []identityTable{identityUsers, identityMemberships, identityMailLinks, identitySessions, identityTrustedDevices}
	account := []string{"disable_crew_ship_user", "enable_crew_ship_user", "delete_crew_ship_user", "end_crew_ship_user_sessions", "revoke_crew_ship_user_trusted_devices", "set_crew_ship_user_name"}
	named := []string{"rename_crew_ship_user", "set_crew_ship_user_email"}
	tests := []struct {
		name   string
		inst   identityInstance
		tables []identityTable
		want   []string
	}{
		{name: "no tenant record", inst: identityInstance{password: true}, tables: every},
		{
			name:   "a password auth with every table",
			inst:   identityInstance{tenant: ship, password: true},
			tables: every,
			want: slices.Concat(account, []string{"require_crew_ship_password_change", "send_crew_ship_user_password_reset_link"}, named,
				[]string{"assign_crew_ship_role", "remove_crew_ship_role", "invite_crew_ship_user", "withdraw_crew_ship_invitation"}),
		},
		{
			name:   "a directory that admits its own: no password is changed or reset",
			inst:   identityInstance{tenant: ship},
			tables: every,
			want:   slices.Concat(account, named, []string{"assign_crew_ship_role", "remove_crew_ship_role", "invite_crew_ship_user", "withdraw_crew_ship_invitation"}),
		},
		{
			name:   "a directory that writes the roles: a tenant gives, takes and invites nothing",
			inst:   identityInstance{tenant: ship, rolesSynced: true},
			tables: every,
			want:   slices.Concat(account, named),
		},
		{
			name:   "usernames that are the addresses: no rename, and no address set by hand",
			inst:   identityInstance{tenant: ship, password: true, usernameIsEmail: true},
			tables: every,
			want: slices.Concat(account, []string{"require_crew_ship_password_change", "send_crew_ship_user_password_reset_link"},
				[]string{"assign_crew_ship_role", "remove_crew_ship_role", "invite_crew_ship_user", "withdraw_crew_ship_invitation"}),
		},
		{
			name:   "no mail links: nothing is invited or withdrawn",
			inst:   identityInstance{tenant: ship},
			tables: []identityTable{identityUsers, identityMemberships, identitySessions, identityTrustedDevices},
			want:   slices.Concat(account, named, []string{"assign_crew_ship_role", "remove_crew_ship_role"}),
		},
		{
			name:   "identity links: an identity of the tenant's people is unlinked",
			inst:   identityInstance{tenant: ship, rolesSynced: true},
			tables: append(slices.Clone(every), identityIdentities),
			want:   slices.Concat(account, named, []string{"unlink_crew_ship_session_identity"}),
		},
		{
			name:   "no memberships: no list of a tenant's people, so no method names one",
			inst:   identityInstance{tenant: ship, password: true},
			tables: []identityTable{identityUsers, identityMailLinks, identitySessions, identityTrustedDevices},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inst := tt.inst
			inst.auth, inst.tables = "Crew", make(map[identityTable]bool)
			for _, kind := range tt.tables {
				inst.tables[kind] = true
			}
			var got []string
			for _, file := range inst.tenantMethodSources() {
				if !file.tenant {
					t.Errorf("%s is not marked a tenant's method", file.name)
				}
				// A file is written where the schema holds every table it needs.
				if !slices.ContainsFunc(file.needs, func(kind identityTable) bool { return !inst.has(kind) }) {
					got = append(got, file.name)
				}
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("tenantMethodSources() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_identityTenantUsersTemplate_accountColumns holds the people's list to the columns
// of the users declaration: a column added to one without the other fails here.
func Test_identityTenantUsersTemplate_accountColumns(t *testing.T) {
	t.Parallel()

	field := regexp.MustCompile(`(?m)^\t\t(\w+)\s+(\S+)\s+` + "`" + `spanner:"(\w+)"`)
	columns := func(source string, leaveOut ...string) []string {
		var out []string
		for _, m := range field.FindAllStringSubmatch(source, -1) {
			if !slices.Contains(leaveOut, m[1]) {
				out = append(out, m[1]+" "+m[2]+" "+m[3])
			}
		}

		return out
	}
	account := columns(identityUsersTemplate)
	if len(account) == 0 {
		t.Fatal("no field found in identityUsersTemplate: the pattern no longer reads the template")
	}
	listed := columns(identityTenantUsersTemplate, "DomainID", "HeldElsewhere", "HeldGlobally", "HeldHere", "HeldInEveryTenant", "OutsideDirectory", "CannotEnter", "AdministratorWithPassword")
	if diff := cmp.Diff(account, listed); diff != "" {
		t.Errorf("the people's list against the users declaration (-declaration +list):\n%s", diff)
	}
	// Every column of the list is selected under its own name.
	for _, column := range columns(identityTenantUsersTemplate) {
		name := column[strings.LastIndex(column, " ")+1:]
		if !strings.Contains(identityTenantUsersTemplate, " AS "+name) {
			t.Errorf("the people's query selects nothing AS %s", name)
		}
	}
}

// Test_holdIdentityTenant pins the check of the tenant record the sources were written
// for against the one the load found.
func Test_holdIdentityTenant(t *testing.T) {
	t.Parallel()

	ship := &identityTenant{name: "Ship", table: "Ships", key: "Id", outlets: []string{"default"}}
	tests := []struct {
		name     string
		written  *identityTenant
		tenanted bool
		wantErr  string
	}{
		{name: "no record either way"},
		{name: "the same record", written: ship, tenanted: true},
		{name: "written for a record the load does not find", written: ship, wantErr: "were written for the tenant record Ship"},
		{name: "written for none, and the load finds one", tenanted: true, wantErr: "were written for no tenant record"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := userFixtureGenerator(t, tt.tenanted)
			r.identityTree = "pkg/identity"
			r.identity = []*identityInstance{{auth: "Crew", tenant: tt.written}}
			err := r.holdIdentityTenant()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("holdIdentityTenant() error = %v", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "fault of the generator") {
				t.Errorf("holdIdentityTenant() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}

	t.Run("a run with no generated management holds nothing", func(t *testing.T) {
		t.Parallel()

		if err := userFixtureGenerator(t, true).holdIdentityTenant(); err != nil {
			t.Errorf("holdIdentityTenant() error = %v", err)
		}
	})
}

// Test_sqlStringLiteral pins the spelling of a library constant in a list's query, and
// the refusal of one that could end the literal.
func Test_sqlStringLiteral(t *testing.T) {
	t.Parallel()

	if got := sqlStringLiteral("every"); got != "'every'" {
		t.Errorf("sqlStringLiteral() = %s, want 'every'", got)
	}
	if got := sqlStringLiteral(""); got != "''" {
		t.Errorf("sqlStringLiteral() of the empty axis = %s, want ''", got)
	}
	for _, value := range []string{"o'clock", `back\slash`, "two\nlines", "tick`"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("sqlStringLiteral(%q) did not panic", value)
				}
			}()
			_ = sqlStringLiteral(value)
		}()
	}
}

// Test_tenantRecordIn_outlets pins the outlets the tenant record is read as served on:
// what its @outlet names, and the default outlet when it names none.
func Test_tenantRecordIn_outlets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		outlet string
		want   []string
	}{
		{name: "no @outlet: the default outlet", want: []string{"default"}},
		{name: "the outlets it names, in the order written", outlet: "\t// @outlet(portal, default)\n", want: []string{"portal", "default"}},
		{name: "one outlet that is not the default", outlet: "\t// @outlet(portal)\n", want: []string{"portal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := "package resources\n\nimport \"github.com/impulseframework/impulse\"\n\ntype (\n\t// Ship is the tenant record.\n\t//\n\t// @resource\n\t// @tenant\n" + tt.outlet +
				"\tShip struct {\n\t\tID impulse.UUID `spanner:\"Id\"`\n\t}\n)\n"
			dir := filepath.Join(t.TempDir(), "resources")
			writeResourcesPackage(t, dir, map[string]string{"fixture.go": source})

			name, outlets, _, err := tenantRecordIn(dir)
			if err != nil {
				t.Fatalf("tenantRecordIn() error = %v", err)
			}
			if name != "Ship" {
				t.Fatalf("tenantRecordIn() name = %q, want Ship", name)
			}
			if diff := cmp.Diff(tt.want, outlets); diff != "" {
				t.Errorf("tenantRecordIn() outlets mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_tenantRecordIn_label pins the label the tenant record is read as declaring for one
// tenant before the packages load, and the word a sentence says for a tenant from it:
// the label where the record declares one, else the record's name in words.
func Test_tenantRecordIn_label(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		record    string
		tenant    string
		wantLabel string
		wantWord  string
	}{
		{name: "no label: the record's name in words", record: "Customer", tenant: "@tenant", wantWord: "customer"},
		{name: "a record of two words", record: "ServiceArea", tenant: "@tenant", wantWord: "service area"},
		{name: "a record whose name ends in a plural acronym keeps its letters", record: "PartnerAPIs", tenant: "@tenant", wantWord: "partner APIs"},
		{name: "a record named by a plural acronym alone keeps it as written", record: "FAQs", tenant: "@tenant", wantWord: "FAQs"},
		{name: "a label of two words", record: "Tenant", tenant: `@tenant(label: "Service district")`, wantLabel: "Service district", wantWord: "service district"},
		{name: "a label that opens with an initialism keeps it", record: "Tenant", tenant: `@tenant(label: "HQ unit")`, wantLabel: "HQ unit", wantWord: "HQ unit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := "package resources\n\nimport \"github.com/impulseframework/impulse\"\n\ntype (\n\t// " + tt.record + " is the tenant record.\n\t//\n\t// @resource\n\t// " + tt.tenant + "\n" +
				"\t" + tt.record + " struct {\n\t\tID impulse.UUID `spanner:\"Id\"`\n\t}\n)\n"
			dir := filepath.Join(t.TempDir(), "resources")
			writeResourcesPackage(t, dir, map[string]string{"fixture.go": source})

			name, _, label, err := tenantRecordIn(dir)
			if err != nil {
				t.Fatalf("tenantRecordIn() error = %v", err)
			}
			if name != tt.record || label != tt.wantLabel {
				t.Fatalf("tenantRecordIn() = %q with the label %q, want %q with %q", name, label, tt.record, tt.wantLabel)
			}
			if got := sentenceWord(name, label); got != tt.wantWord {
				t.Errorf("sentenceWord(%q, %q) = %q, want %q", name, label, got, tt.wantWord)
			}
		})
	}
}

// Test_identitySourceData_TenantWord pins the word the generated client hands the
// identity package for a tenant (identity.WithTenantWord): the tenant record's, and none
// in an application with no tenant record, whose client passes no word.
func Test_identitySourceData_TenantWord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tenant *identityTenant
		want   string
	}{
		{name: "no tenant record"},
		{name: "the record's word", tenant: &identityTenant{name: "Customer", table: "Customers", word: "customer"}, want: "customer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := &identitySourceData{identityInstance: &identityInstance{auth: "People", tenant: tt.tenant}}
			if got := d.TenantWord(); got != tt.want {
				t.Errorf("TenantWord() = %q, want %q", got, tt.want)
			}
		})
	}
	// The client's constructor passes the word first, so a caller's own option is the one kept.
	if !strings.Contains(identityClientTemplate, "opts = append([]identity.Option{identity.WithTenantWord({{ printf \"%q\" .TenantWord }})}, opts...)") {
		t.Error("identityClientTemplate no longer passes identity.WithTenantWord ahead of the caller's options")
	}
}

// Test_identitySourceData_TenantPicker pins where the field that says which tenant a role
// is given in gets a picker of the tenant record: where the record is served on every
// outlet the instance's methods are on, since a picker lists a resource of the method's
// own outlet. Anywhere else the field stays typed.
func Test_identitySourceData_TenantPicker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tenant  *identityTenant
		outlets []string
		want    string
	}{
		{name: "no tenant record", outlets: []string{"default"}},
		{name: "the record is on the methods' one outlet", tenant: &identityTenant{name: "Ship", table: "Ships", outlets: []string{"default"}}, outlets: []string{"default"}, want: "Ships"},
		{name: "the record is on both of the methods' outlets", tenant: &identityTenant{name: "Ship", table: "Ships", outlets: []string{"default", "portal"}}, outlets: []string{"portal", "default"}, want: "Ships"},
		{name: "the methods are also on an outlet the record is not on", tenant: &identityTenant{name: "Ship", table: "Ships", outlets: []string{"default"}}, outlets: []string{"portal", "default"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := &identitySourceData{identityInstance: &identityInstance{auth: "Crew", tenant: tt.tenant, outlets: tt.outlets}}
			if got := d.TenantPicker(); got != tt.want {
				t.Errorf("TenantPicker() = %q, want %q", got, tt.want)
			}
		})
	}
}
