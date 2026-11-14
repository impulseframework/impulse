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

// shipTenantSource declares Ship the tenant record, in the grouped form the resources
// packages use.
const shipTenantSource = `package resources

type (
	// Ship is the tenant record.
	//
	// @resource
	// @tenant
	Ship struct {
		ID string ` + "`spanner:\"Id\"`" + `
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

// Sector is the tenant record.
//
// @resource
// @tenant(creation: selfService)
type Sector struct {
	ID string
}
`},
			want: "Sector",
		},
		{
			name: "two annotations on one line",
			files: map[string]string{"sectors.go": `package resources

// @resource @tenant
type Sector struct {
	ID string
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

// @resource
// @tenant
// @noSuchAnnotation
type Sector struct {
	ID string
}
`},
		},
		{
			name: "a test file and a file the build leaves out are not read",
			files: map[string]string{
				"docks.go":        "package resources\n\n// @resource\ntype Dock struct {\n\tID string\n}\n",
				"sectors_test.go": "package resources\n\n// @resource\n// @tenant\ntype Sector struct {\n\tID string\n}\n",
				"ignored.go":      "//go:build ignore\n\npackage resources\n\n// @resource\n// @tenant\ntype Region struct {\n\tID string\n}\n",
			},
		},
		{
			name: "two tenant records",
			files: map[string]string{
				"ships.go":   shipTenantSource,
				"sectors.go": "package resources\n\n// @resource\n// @tenant\ntype Sector struct {\n\tID string\n}\n",
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

// shipsTable is the tenant record's table as the schema holds it: one string key.
func shipsTable() *tableMetadata {
	return &tableMetadata{PkCount: 1, Columns: map[string]columnMeta{
		"Id":   {IsPrimaryKey: true, SpannerType: "STRING(64)"},
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
// table by the generator's plural, and the table's one string key off the schema; nil
// for an application with none; and the refusals of a record the lists cannot be written
// over.
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
		{name: "the record, its table and its key", source: shipTenantSource, table: shipsTable(), want: &identityTenant{name: "Ship", table: "Ships", key: "Id"}},
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
			name:    "a record whose key is no string",
			source:  shipTenantSource,
			table:   &tableMetadata{PkCount: 1, Columns: map[string]columnMeta{"Id": {IsPrimaryKey: true, SpannerType: "INT64"}}},
			wantErr: "its key Ships.Id is INT64; the tenant record's key is a string",
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

	ship := &identityTenant{name: "Ship", table: "Ships", key: "Id"}
	tests := []struct {
		name   string
		tenant *identityTenant
		tables []identityTable
		want   []string
	}{
		{name: "no tenant record", tables: []identityTable{identityUsers, identityMemberships, identityMailLinks}},
		{name: "every table", tenant: ship, tables: []identityTable{identityUsers, identityMemberships, identityMailLinks}, want: []string{"CrewShipUser", "CrewShipUserRole", "CrewShipInvitation"}},
		{name: "no mail links", tenant: ship, tables: []identityTable{identityUsers, identityMemberships}, want: []string{"CrewShipUser", "CrewShipUserRole"}},
		{name: "no memberships: nobody holds a role in a tenant", tenant: ship, tables: []identityTable{identityUsers, identityMailLinks}, want: []string{"CrewShipInvitation"}},
		{name: "identity links: the identities of the tenant's people", tenant: ship, tables: []identityTable{identityUsers, identityMemberships, identityIdentities}, want: []string{"CrewShipUser", "CrewShipUserRole", "CrewShipIdentity"}},
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

	ship := &identityTenant{name: "Ship", table: "Ships", key: "Id"}
	every := []identityTable{identityUsers, identityMemberships, identityMailLinks, identitySessions, identityTrustedDevices}
	account := []string{"disable_crew_ship_user", "enable_crew_ship_user", "delete_crew_ship_user", "end_crew_ship_user_sessions", "revoke_crew_ship_user_trusted_devices"}
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
			want: slices.Concat(account, []string{"require_crew_ship_password_change", "set_crew_ship_temporary_password"}, named,
				[]string{"create_crew_ship_user", "assign_crew_ship_role", "remove_crew_ship_role", "invite_crew_ship_user", "withdraw_crew_ship_invitation"}),
		},
		{
			name:   "a directory that admits its own: nobody is created by hand, and no password is set",
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
			want: slices.Concat(account, []string{"require_crew_ship_password_change", "set_crew_ship_temporary_password"},
				[]string{"create_crew_ship_user", "assign_crew_ship_role", "remove_crew_ship_role", "invite_crew_ship_user", "withdraw_crew_ship_invitation"}),
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
	listed := columns(identityTenantUsersTemplate, "Domain", "HeldElsewhere", "HeldGlobally")
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

	ship := &identityTenant{name: "Ship", table: "Ships", key: "Id"}
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
