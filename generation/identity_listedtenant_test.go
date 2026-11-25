package generation

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/generation/parser"
	"golang.org/x/tools/go/packages"
)

// Test_tenantInvitationIn_key pins what the reading before the load keeps of the tenant
// record for the list of tenants: the key's field, its type as another package writes it
// and its column, with the import its type needs kept apart from the fields', and each
// field's column. A key of two columns, and one whose type another package cannot name,
// stop the reading with the reason in words.
func Test_tenantInvitationIn_key(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		source         string
		keys           []string
		wantKey        identityTenantField
		wantKeyImports []string
		wantColumns    map[string]string
		wantWhy        string
	}{
		{
			name:           "the record's key and each field's column",
			source:         citadelTenantSource,
			wantKey:        identityTenantField{Name: "ID", Type: "impulse.UUID", Column: "Id"},
			wantKeyImports: []string{`"github.com/impulseframework/impulse"`},
			wantColumns: map[string]string{
				"Name": "Name", "Motto": "Motto", "Founded": "Founded", "Kind": "Kind", "Walled": "Walled",
				"Garrison": "Garrison", "Towers": "Towers", "Closed": "Closed",
			},
		},
		{
			name:   "a field read under a column of another name",
			source: oneFieldCitadel("", "\tSeat string `spanner:\"SeatName\"`\n"),
			wantKey: identityTenantField{
				Name: "ID", Type: "impulse.UUID", Column: "Id",
			},
			wantKeyImports: []string{`"github.com/impulseframework/impulse"`},
			wantColumns:    map[string]string{"Name": "Name", "Seat": "SeatName"},
		},
		{
			name:    "a key of two columns",
			source:  oneFieldCitadel("", ""),
			keys:    []string{"Id", "Name"},
			wantWhy: "the record's key is more than one column",
		},
		{
			name:    "a key whose type another package cannot name",
			source:  strings.Replace(oneFieldCitadel("", ""), "ID impulse.UUID", "ID [16]byte", 1),
			wantWhy: "the record's key has a type another package cannot name",
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			place := filepath.Join("pkg", "key"+strconv.Itoa(i), "resources")
			dir := filepath.Join(t.TempDir(), place)
			writeResourcesPackage(t, dir, map[string]string{"citadels.go": tt.source})
			r := identityFixtureGenerator(t, crewIdentityTables...)
			r.resource = packageDir(place)
			table := citadelsTable()
			table.Columns["SeatName"] = columnMeta{SpannerType: "STRING(MAX)"}
			for _, key := range tt.keys {
				column := table.Columns[key]
				column.IsPrimaryKey = true
				table.Columns[key] = column
			}
			r.tableMap["Citadels"] = table

			got, why, err := r.tenantInvitationIn(dir, "Citadel")
			if err != nil {
				t.Fatalf("tenantInvitationIn() error = %v", err)
			}
			if why != tt.wantWhy {
				t.Errorf("tenantInvitationIn() says %q, want %q", why, tt.wantWhy)
			}
			if tt.wantWhy != "" {
				if got != nil {
					t.Errorf("tenantInvitationIn() = %v, want nothing read", got)
				}

				return
			}
			if diff := cmp.Diff(tt.wantKey, got.key); diff != "" {
				t.Errorf("the key (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantKeyImports, got.keyImports); diff != "" {
				t.Errorf("the key's imports (-want +got):\n%s", diff)
			}
			columns := map[string]string{}
			for _, field := range got.fields {
				columns[field.Name] = field.Column
			}
			if flag := got.directoryOnly; flag != nil {
				columns[flag.Name] = flag.Column
			}
			if diff := cmp.Diff(tt.wantColumns, columns); diff != "" {
				t.Errorf("the fields' columns (-want +got):\n%s", diff)
			}
		})
	}
}

// citadelSinceSource is citadelTenantSource with a field the record marks as when a
// citadel was made, written by the database with the row (RaisedAt, which the wire cannot
// write), and markedCitadel adds its column to the record's table.
var citadelSinceSource = strings.Replace(strings.Replace(citadelTenantSource,
	"import (\n", "import (\n\t\"time\"\n\n", 1),
	"\t\t// @directoryOnly\n",
	"\t\t// Raised is when the citadel was made.\n\t\t//\n\t\t// @tenantSince\n\t\tRaised time.Time `spanner:\"RaisedAt\" conditions:\"output_only\" default_create_fn:\"resource.CommitTimestamp\"`\n\t\t// @directoryOnly\n", 1)

// markedCitadel adds the column of the field citadelSinceSource marks to the record's
// table.
func markedCitadel(r *resourceGenerator) {
	r.tableMap["Citadels"].Columns["RaisedAt"] = columnMeta{SpannerType: "TIMESTAMP"}
}

// Test_tenantInvitationIn_since pins what the reading before the load keeps of the field
// the tenant record marks as when a tenant was made: its name, its name on the wire and its
// column. A marked field the wire cannot write is carried by no invitation, and one it can
// write is carried as any field; a record that marks none keeps none.
func Test_tenantInvitationIn_since(t *testing.T) {
	t.Parallel()

	timestamp := columnMeta{SpannerType: "TIMESTAMP"}
	tests := []struct {
		name       string
		source     string
		columns    map[string]columnMeta
		wantSince  *identityTenantField
		wantFields []string
	}{
		{
			name:       "a marked field the wire cannot write is kept, and carried by no invitation",
			source:     oneFieldCitadel("\t\"time\"\n", "\t// @tenantSince\n\tRaised time.Time `spanner:\"RaisedAt\" conditions:\"output_only\"`\n"),
			columns:    map[string]columnMeta{"RaisedAt": timestamp},
			wantSince:  &identityTenantField{Name: "Raised", Wire: "raised", Column: "RaisedAt"},
			wantFields: []string{"Name"},
		},
		{
			name:       "a marked field the wire can write is kept, and carried as any field",
			source:     oneFieldCitadel("\t\"time\"\n", "\t// @tenantSince\n\tOpened *time.Time `spanner:\"Opened\"`\n"),
			columns:    map[string]columnMeta{"Opened": {SpannerType: "TIMESTAMP", IsNullable: true}},
			wantSince:  &identityTenantField{Name: "Opened", Wire: "opened", Column: "Opened"},
			wantFields: []string{"Name", "Opened"},
		},
		{
			name:       "a record that marks none keeps none",
			source:     oneFieldCitadel("\t\"time\"\n", "\tOpened time.Time `spanner:\"Opened\"`\n"),
			columns:    map[string]columnMeta{"Opened": timestamp},
			wantFields: []string{"Name", "Opened"},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			place := filepath.Join("pkg", "since"+strconv.Itoa(i), "resources")
			dir := filepath.Join(t.TempDir(), place)
			writeResourcesPackage(t, dir, map[string]string{"citadels.go": tt.source})
			r := identityFixtureGenerator(t, crewIdentityTables...)
			r.resource = packageDir(place)
			table := citadelsTable()
			for name, column := range tt.columns {
				table.Columns[name] = column
			}
			r.tableMap["Citadels"] = table

			got, why, err := r.tenantInvitationIn(dir, "Citadel")
			if err != nil || why != "" {
				t.Fatalf("tenantInvitationIn() says %q, error = %v; want the record read", why, err)
			}
			if diff := cmp.Diff(tt.wantSince, got.since); diff != "" {
				t.Errorf("the since field (-want +got):\n%s", diff)
			}
			fields := make([]string, 0, len(got.fields))
			for _, field := range got.fields {
				fields = append(fields, field.Name)
			}
			if diff := cmp.Diff(tt.wantFields, fields); diff != "" {
				t.Errorf("the carried fields (-want +got):\n%s", diff)
			}
		})
	}
}

// listedCitadelFile is where the run writes the crew's list of citadels, by path from the
// identity tree's root.
const listedCitadelFile = "internal/crewresources/crew_listed_citadels.go"

// listedCitadelQuery is the query constant of the crew's list of citadels, out of the
// written source: from its declaration to the closing quote.
func listedCitadelQuery(t *testing.T, source string) string {
	t.Helper()

	start := strings.Index(source, "const crewListedCitadelQuery = `")
	if start < 0 {
		t.Fatalf("the list declares no query:\n%s", source)
	}
	end := strings.Index(source[start+len("const crewListedCitadelQuery = `"):], "`")

	return source[start : start+len("const crewListedCitadelQuery = `")+end]
}

// Test_prepareIdentity_listedTenant pins the source of the whole application's list of
// tenants, as the run writes it before the packages are loaded: a view over the tenant
// record's table that declares the table as where its rows are written, keyed and ordered
// as the record is, with the record's fields the invitation carries, when the citadel was
// made, read from the column the record marks, and the three facts; and the crew's
// client, which hands the list the roles that administer a citadel. The members, whose
// people make no citadel, have no such list.
func Test_prepareIdentity_listedTenant(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := citadelGenerator(t, citadelSinceSource, func(r *resourceGenerator) {
		markedCitadel(r)
		for _, table := range crewGroupTables {
			r.tableMap[table] = &tableMetadata{}
		}
	})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	listed := identitySource(t, listedCitadelFile)
	assertContains(t, listedCitadelFile, listed, []string{
		generationHeader,
		"package crewresources",
		"// @virtual\n // @rowsOf(Citadels)\n // @outlet(default)\n // @order(Name asc)\n // @page(default: 25, max: 200)\n // @rowWord(citadel)\n CrewListedCitadel struct {",
		// The record's key, under its own name and type, and its fields the invitation
		// carries, each read by its column; the key and the name are what the list
		// filters by.
		"// @primarykey\n ID impulse.UUID `spanner:\"Id\" index:\"true\"`\n",
		"Name string `spanner:\"Name\" index:\"true\"`\n",
		"Motto *string `spanner:\"Motto\"`\n",
		"Founded civil.Date `spanner:\"Founded\"`\n",
		"Kind resources.CitadelKind `spanner:\"Kind\"`\n",
		"Walled bool `spanner:\"Walled\"`\n",
		"Garrison int64 `spanner:\"Garrison\"`\n",
		"Towers []string `spanner:\"Towers\"`\n",
		"Closed bool `spanner:\"Closed\"`\n",
		// The three facts, and when the citadel was made under the list's own name.
		"People int64 `spanner:\"People\"`\n Since *time.Time `spanner:\"Since\"`\n Directory *string `spanner:\"Directory\"`\n Administrators []string `spanner:\"Administrators\"`\n }",
		"\"time\"",
		"\"cloud.google.com/go/civil\"",
		"\"example.com/acme/beacon/pkg/resources\"",
		"\"github.com/impulseframework/impulse\"",
		"var crewListedCitadelAdministrators atomic.Pointer[[]string]",
		"func SetCrewListedCitadelAdministratorRoles(roles []accesstypes.Role) {",
		"func (CrewListedCitadel) Subquery() (query string, params map[string]any) {",
		"return crewListedCitadelQuery, map[string]any{\"tenantAdministratorRoles\": administrators}",
		// The struct's comment says what each fact is.
		"// CrewListedCitadel is one citadel as the whole application's list shows it: a row of\n // Citadels with the record's own fields, and three facts no table holds.",
		"a role of\n // their own, a place in one of its groups, or a sign-in through its directory.",
		"// A person who is there only through a role held in every citadel, or through a group\n // kept for the whole application, is not counted.\n",
		"// Directory is the kind of directory its people sign in through, and of a Google\n // Workspace its connection too: the Workspace's domain, or the name the application's",
		"or through one of its groups. A person whose account is disabled is not named:\n // a disabled account holds its roles and administers nothing. Since is when the\n // citadel was made: the record's Raised, which it marks @tenantSince. The list filters by the name. It is a view over the\n // record's table, so a row opens, is changed and is deleted as the record's own.",
		"A person whose account is disabled is not named",
		"// crewListedCitadelQuery selects every citadel, each with the people it holds, when it\n// was made, its directory and its administrators.",
	}, []string{"Passphrase", "Cipher", "Sealed", "Search", "DefaultRole", "Raised time.Time", "first role"})

	query := listedCitadelQuery(t, listed)
	assertContains(t, "the list's query", query, []string{
		"SELECT\n s.Id AS Id,\n s.Name AS Name,\n s.Motto AS Motto,\n s.Founded AS Founded,\n s.Kind AS Kind,\n s.Walled AS Walled,\n s.Garrison AS Garrison,\n s.Towers AS Towers,\n s.Closed AS Closed,\n",
		// People: the tenant's own memberships, the places in its own groups and the
		// identities under its directory, each that counts now.
		"(SELECT COUNT(DISTINCT p.UserId) FROM (\n SELECT m.UserId AS UserId\n FROM CrewUserRoles m\n WHERE m.Kind = 'domain' AND m.Axis = '' AND m.DomainId = s.Id\n AND " + countingSQL("m") + "\n UNION DISTINCT\n",
		"FROM CrewGroups g\n JOIN CrewGroupMembers gm ON gm.GroupId = g.Id\n WHERE g.Kind = 'domain' AND g.Axis = '' AND g.DomainId = s.Id\n AND " + countingSQL("gm") + "\n UNION DISTINCT\n",
		// Since: the column the record marks, read where the facts are.
		"FROM CitadelDirectories dr\n JOIN CrewSessionIdentities dl ON dl.Method = dr.Method AND dl.Connection = dr.Connection\n WHERE dr.CitadelId = s.Id\n ) p) AS People,\n s.RaisedAt AS Since,\n (SELECT CASE d.Method",
		// Directory: the kind, and a Google Workspace's domain.
		"(SELECT CASE d.Method\n WHEN 'google' THEN IF(IFNULL(d.Connection, '') = '', 'Google Workspace', CONCAT('Google Workspace ', d.Connection))\n WHEN 'azure' THEN 'Microsoft Entra tenant'\n WHEN 'workos' THEN 'WorkOS connection'\n WHEN 'oidc' THEN 'OpenID Connect provider'\n ELSE 'Directory'\n END\n FROM CitadelDirectories d\n WHERE d.CitadelId = s.Id\n LIMIT 1) AS Directory,",
		// Administrators: by name, or username, a line each, through the tenant's own
		// memberships and groups.
		"ARRAY(\n SELECT COALESCE(NULLIF(u.Name, ''), u.Username) AS Line\n FROM CrewSessionUsers u\n WHERE u.Id IN (\n",
		"WHERE ta.Kind = 'domain' AND ta.Axis = '' AND ta.DomainId = s.Id\n AND ta.Role IN UNNEST(@tenantAdministratorRoles)\n AND " + countingSQL("ta") + "\n UNION DISTINCT\n",
		"WHERE tg.Kind = 'domain' AND tg.Axis = '' AND tg.DomainId = s.Id\n AND tr.Kind = 'domain' AND tr.Axis = '' AND tr.DomainId = s.Id\n AND tr.Role IN UNNEST(@tenantAdministratorRoles)\n AND " + countingSQL("tr") + "\n AND " + countingSQL("tm") + "\n )\n AND NOT u.Disabled\n ORDER BY Line\n ) AS Administrators\nFROM Citadels s\n",
		// No account that is disabled is named: it holds its roles and administers nothing.
		"\n )\n AND NOT u.Disabled\n ORDER BY Line\n ) AS Administrators",
	}, []string{
		// No row held in every tenant and no group kept for the whole application counts,
		// and nothing is read from the memberships for when the citadel was made.
		"'every'", "'global'", "CROSS JOIN", "MIN(", "CreatedAt",
	})
	// Only a Google Workspace's connection is said: the CASE reads the connection in
	// its one branch, and the directory is read by the tenant's column alone.
	if got := strings.Count(query, "d.Connection"); got != 2 {
		t.Errorf("the directory's connection is read %d times, want twice, in the Google Workspace branch alone:\n%s", got, query)
	}
	// Whether an account is disabled is read once, for the administrators alone: People
	// still counts a disabled person.
	if got := strings.Count(listed, "NOT u.Disabled"); got != 1 {
		t.Errorf("the file reads NOT u.Disabled %d times, want once, in the administrators' array:\n%s", got, listed)
	}

	// The crew's client hands the list the roles that administer a citadel; the members'
	// carries no list.
	assertContains(t, "crewidentity/client.go", identitySource(t, "crewidentity/client.go"), []string{
		"crewresources.SetCrewListedCitadelAdministratorRoles(manager.TenantAdministratorRoles())",
	}, nil)
	assertContains(t, "membersidentity/client.go", identitySource(t, "membersidentity/client.go"), nil, []string{"ListedCitadel"})
	if got := identitySource(t, "internal/membersresources/members_listed_citadels.go"); got != "" {
		t.Errorf("the members' list of citadels was written, though their people make none")
	}
}

// Test_prepareIdentity_listedTenant_shapes pins what the list of tenants carries of a
// record that marks less, of an auth that keeps less, of the field shapes another package
// writes, and which auths declare the list at all.
func Test_prepareIdentity_listedTenant_shapes(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
	// As Test_prepareIdentity: each case's working directory is the process's, no
	// t.Parallel.
	t.Run("a record that marks no name nor when it was made, declares no switch and has no directories record", func(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
		t.Chdir(t.TempDir())

		if err := citadelGenerator(t, oneFieldCitadel("", ""), nil).prepareIdentity(); err != nil {
			t.Fatalf("prepareIdentity() error = %v", err)
		}
		listed := identitySource(t, listedCitadelFile)
		// Ordered by the key, no column filters by a name, there is no directory to say
		// and no Since: two facts, and nothing of the time package.
		assertContains(t, listedCitadelFile, listed, []string{
			"// @order(ID asc)",
			"// @primarykey\n ID impulse.UUID `spanner:\"Id\" index:\"true\"`\n Name string `spanner:\"Name\"`\n People int64 `spanner:\"People\"`\n Administrators []string `spanner:\"Administrators\"`\n }",
			"and two facts no table holds.",
			"a role of\n // their own.\n",
			"own. A person whose account is disabled is not named:\n // a disabled account holds its roles and administers nothing. It is a view over the",
			"// crewListedCitadelQuery selects every citadel, each with the people it holds and its administrators.",
		}, []string{"Directory", "Closed", "CitadelDirectories", "The list filters by the name", "a place in one of its groups", "or through a group", "Since", "\"time\"", "was made"})
		// The auth keeps no groups: no group is read for its people or its administrators,
		// and no account that is disabled is named.
		assertContains(t, "the list's query", listedCitadelQuery(t, listed), []string{"\n )\n AND NOT u.Disabled\n ORDER BY Line\n ) AS Administrators"}, []string{"CrewGroups", "CrewGroupMembers", "CrewGroupRoles", "UNION DISTINCT", "JOIN"})
	})

	t.Run("a record that marks the column saying a tenant is disabled", func(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
		t.Chdir(t.TempDir())

		r := citadelGenerator(t, oneFieldCitadel("", "\t// @tenantDisabled\n\tDisabled bool `spanner:\"Disabled\"`\n"), func(r *resourceGenerator) {
			r.tableMap["Citadels"].Columns["Disabled"] = columnMeta{SpannerType: "BOOL", HasDefault: true}
		})
		if err := r.prepareIdentity(); err != nil {
			t.Fatalf("prepareIdentity() error = %v", err)
		}
		// The list carries whether each citadel is disabled, under the record's own name
		// for it, as a field of its struct and a column of its query.
		listed := identitySource(t, listedCitadelFile)
		assertContains(t, listedCitadelFile, listed, []string{
			"Name string `spanner:\"Name\"`\n Disabled bool `spanner:\"Disabled\"`\n People int64 `spanner:\"People\"`\n",
		}, nil)
		assertContains(t, "the list's query", listedCitadelQuery(t, listed), []string{"SELECT\n s.Id AS Id,\n s.Name AS Name,\n s.Disabled AS Disabled,\n (SELECT COUNT(DISTINCT p.UserId)"}, nil)
	})

	t.Run("a record that declares a directory, for an auth that keeps no identities", func(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
		t.Chdir(t.TempDir())

		r := citadelGenerator(t, citadelTenantSource, func(r *resourceGenerator) { delete(r.tableMap, "CrewSessionIdentities") })
		if err := r.prepareIdentity(); err != nil {
			t.Fatalf("prepareIdentity() error = %v", err)
		}
		// The list says the directory, and nobody is one of a citadel's people by an
		// identity the schema does not keep.
		query := listedCitadelQuery(t, identitySource(t, listedCitadelFile))
		assertContains(t, "the list's query", query, []string{"FROM CitadelDirectories d\n WHERE d.CitadelId = s.Id\n LIMIT 1) AS Directory,"}, []string{"CrewSessionIdentities", "dr."})
	})

	t.Run("the four field shapes come through as written, with their imports", func(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
		t.Chdir(t.TempDir())

		source := oneFieldCitadel("\t\"cloud.google.com/go/civil\"\n",
			"\tOpened *civil.Date `spanner:\"Opened\"`\n\tHeads *int64 `spanner:\"Heads\"`\n\tRating float32 `spanner:\"Rating\"`\n\tGrade impulse.NullEnum[Grade] `spanner:\"Grade\"`\n") +
			"\n// Grade is a type the package declares.\ntype Grade string\n"
		r := citadelGenerator(t, source, func(r *resourceGenerator) {
			columns := r.tableMap["Citadels"].Columns
			columns["Opened"] = columnMeta{SpannerType: "DATE", IsNullable: true}
			columns["Heads"] = columnMeta{SpannerType: "INT64", IsNullable: true}
			columns["Rating"] = columnMeta{SpannerType: "FLOAT32"}
			columns["Grade"] = columnMeta{SpannerType: "STRING(16)", IsNullable: true}
		})
		if err := r.prepareIdentity(); err != nil {
			t.Fatalf("prepareIdentity() error = %v", err)
		}
		assertContains(t, listedCitadelFile, identitySource(t, listedCitadelFile), []string{
			"Opened *civil.Date `spanner:\"Opened\"`\n",
			"Heads *int64 `spanner:\"Heads\"`\n",
			"Rating float32 `spanner:\"Rating\"`\n",
			"Grade impulse.NullEnum[resources.Grade] `spanner:\"Grade\"`\n",
			"\"cloud.google.com/go/civil\"",
			"\"example.com/acme/beacon/pkg/resources\"",
			"\"github.com/impulseframework/impulse\"",
		}, nil)
	})

	// The list says when a citadel was made as Since, once, whatever the record calls the
	// column and whether the wire writes it; a record that marks none carries a field
	// named Since as any field.
	since := []struct {
		name    string
		imports string
		field   string
		column  columnMeta
		want    []string
		notWant []string
	}{
		{
			name:    "a marked field the wire can write is the list's Since, and not a column of its own",
			imports: "\t\"time\"\n",
			field:   "\t// @tenantSince\n\tOpened time.Time `spanner:\"Opened\"`\n",
			column:  columnMeta{SpannerType: "TIMESTAMP"},
			want:    []string{"Name string `spanner:\"Name\"`\n People int64 `spanner:\"People\"`\n Since *time.Time `spanner:\"Since\"`\n Administrators", "s.Opened AS Since,", "\"time\"", "the record's Opened, which it marks @tenantSince."},
			notWant: []string{"Opened time.Time", "AS Opened"},
		},
		{
			name:    "a marked field named Since is the list's Since alone",
			imports: "\t\"time\"\n",
			field:   "\t// @tenantSince\n\tSince time.Time `spanner:\"Since\"`\n",
			column:  columnMeta{SpannerType: "TIMESTAMP"},
			want:    []string{"Name string `spanner:\"Name\"`\n People int64 `spanner:\"People\"`\n Since *time.Time `spanner:\"Since\"`\n Administrators", ") p) AS People,\n s.Since AS Since,\n ARRAY(", "the record's Since, which it marks @tenantSince."},
		},
		{
			name:    "a field named Since on a record that marks none is carried under its own name",
			field:   "\tSince string `spanner:\"Since\"`\n",
			column:  columnMeta{SpannerType: "STRING(MAX)"},
			want:    []string{"Name string `spanner:\"Name\"`\n Since string `spanner:\"Since\"`\n People int64 `spanner:\"People\"`\n Administrators", "s.Name AS Name,\n s.Since AS Since,\n (SELECT COUNT"},
			notWant: []string{"*time.Time", "\"time\"", "was made"},
		},
	}
	for _, tt := range since {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			r := citadelGenerator(t, oneFieldCitadel(tt.imports, tt.field), func(r *resourceGenerator) {
				r.tableMap["Citadels"].Columns["Opened"] = tt.column
				r.tableMap["Citadels"].Columns["Since"] = tt.column
			})
			if err := r.prepareIdentity(); err != nil {
				t.Fatalf("prepareIdentity() error = %v", err)
			}
			listed := identitySource(t, listedCitadelFile)
			assertContains(t, listedCitadelFile, listed, tt.want, tt.notWant)
			if got := strings.Count(listed, "Since *time.Time") + strings.Count(listed, "Since string"); got > 1 {
				t.Errorf("the list declares Since %d times, want once:\n%s", got, listed)
			}
			if got := strings.Count(listed, "AS Since,"); got != 1 {
				t.Errorf("the query reads Since %d times, want once:\n%s", got, listed)
			}
		})
	}

	none := []struct {
		name   string
		source string
		mutate func(r *resourceGenerator)
	}{
		{
			name:   "an auth whose directory writes its roles makes no tenant",
			source: citadelTenantSource,
			mutate: func(r *resourceGenerator) {
				r.defaultOutlet.auth = &outletAuth{importPath: crewAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
			},
		},
		{
			name:   "a schema that keeps no mailed links invites no tenant",
			source: citadelTenantSource,
			mutate: func(r *resourceGenerator) { delete(r.tableMap, "CrewSessionMailLinks") },
		},
		{
			name:   "a tenant record no outlet of the crew's management serves",
			source: strings.Replace(citadelTenantSource, "// @resource\n\t// @tenant\n", "// @resource\n\t// @tenant\n\t// @outlet(droids)\n", 1),
		},
	}
	for _, tt := range none { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			if err := citadelGenerator(t, tt.source, tt.mutate).prepareIdentity(); err != nil {
				t.Fatalf("prepareIdentity() error = %v", err)
			}
			if got := identitySource(t, listedCitadelFile); got != "" {
				t.Errorf("the list of citadels was written:\n%s", got)
			}
			if client := identitySource(t, "crewidentity/client.go"); client != "" {
				assertContains(t, "crewidentity/client.go", client, nil, []string{"ListedCitadel"})
			}
		})
	}
}

// Test_identityInstance_listsTenants pins which auth declares the list of tenants, on
// which outlets, and its name.
func Test_identityInstance_listsTenants(t *testing.T) {
	t.Parallel()

	every := map[identityTable]bool{identityUsers: true, identityMailLinks: true, identityMemberships: true}
	tenant := func(outlets ...string) *identityTenant {
		return &identityTenant{name: "Citadel", outlets: outlets, invitation: &identityTenantInvitation{}}
	}
	tests := []struct {
		name        string
		inst        *identityInstance
		wantOutlets []string
	}{
		{
			name:        "the auth that invites, on the outlets that serve its management and the record, in its order",
			inst:        &identityInstance{auth: "Crew", createsTenant: true, outlets: []string{"console", "admin", "kiosk"}, tables: every, tenant: tenant("kiosk", "console")},
			wantOutlets: []string{"console", "kiosk"},
		},
		{
			name: "no outlet serves both",
			inst: &identityInstance{auth: "Crew", createsTenant: true, outlets: []string{"admin"}, tables: every, tenant: tenant("console")},
		},
		{
			name: "an auth that invites no tenant",
			inst: &identityInstance{auth: "Crew", outlets: []string{"console"}, tables: every, tenant: tenant("console")},
		},
		{
			name: "a schema that holds no accounts",
			inst: &identityInstance{auth: "Crew", createsTenant: true, outlets: []string{"console"}, tables: map[identityTable]bool{identityMailLinks: true, identityMemberships: true}, tenant: tenant("console")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want := tt.wantOutlets != nil
			if got := tt.inst.listsTenants(); got != want {
				t.Errorf("listsTenants() = %v, want %v", got, want)
			}
			if !want {
				return
			}
			if diff := cmp.Diff(tt.wantOutlets, tt.inst.listedTenantOutlets()); diff != "" {
				t.Errorf("listedTenantOutlets() (-want +got):\n%s", diff)
			}
			if got := tt.inst.listedTenantName(); got != "CrewListedCitadel" {
				t.Errorf("listedTenantName() = %q, want CrewListedCitadel", got)
			}
		})
	}
}

// Test_identityInstance_refuseListedTenantNamesTaken pins the refusal of a tenant record
// whose name gives the list of tenants the name of another list of the auth, and of one
// with a field named as a column the list adds, each in its sentence, where the list is
// declared alone. Since is one of the list's own names only where the record marks when a
// tenant was made, and the marked field may itself be named Since.
func Test_identityInstance_refuseListedTenantNamesTaken(t *testing.T) {
	t.Parallel()

	instance := func(record string, invitation *identityTenantInvitation) *identityInstance {
		return &identityInstance{
			importPath: crewAuthPath, auth: "Crew", createsTenant: true, outlets: []string{"default"},
			tables: map[identityTable]bool{identityUsers: true, identityMailLinks: true, identityMemberships: true},
			tenant: &identityTenant{name: record, outlets: []string{"default"}, invitation: invitation},
		}
	}
	plain := func() *identityTenantInvitation {
		return &identityTenantInvitation{key: identityTenantField{Name: "ID"}, fields: []identityTenantField{{Name: "Name"}}}
	}
	named := func(record, list string) string {
		return `Auth("example.com/acme/beacon/pkg/auth/crew"): the tenant record ` + record + ` gives the list of tenants the name ` + list + `, which another list of the auth has; the tenant record takes another name`
	}
	field := func(name string) string {
		return `Auth("example.com/acme/beacon/pkg/auth/crew"): the tenant record Citadel has a field named ` + name + `, and the list of tenants generated for it (CrewListedCitadel) adds a column of that name (People, Directory and Administrators are its own); the field takes another name`
	}
	markedField := func(name string) string {
		return `Auth("example.com/acme/beacon/pkg/auth/crew"): the tenant record Citadel has a field named ` + name + `, and the list of tenants generated for it (CrewListedCitadel) adds a column of that name (People, Since, Directory and Administrators are its own); the field takes another name`
	}
	withField := func(name string) *identityTenantInvitation {
		invitation := plain()
		invitation.fields = append(invitation.fields, identityTenantField{Name: name})

		return invitation
	}
	marked := func(invitation *identityTenantInvitation, since string) *identityTenantInvitation {
		invitation.since = &identityTenantField{Name: since}

		return invitation
	}
	tests := []struct {
		name string
		inst *identityInstance
		want string
	}{
		{name: "a record named Group", inst: instance("Group", plain()), want: named("Group", "CrewListedGroup")},
		{name: "a record named GroupRole", inst: instance("GroupRole", plain()), want: named("GroupRole", "CrewListedGroupRole")},
		{name: "a record named RoleGrant", inst: instance("RoleGrant", plain()), want: named("RoleGrant", "CrewListedRoleGrant")},
		{name: "a field named People", inst: instance("Citadel", withField("People")), want: field("People")},
		{name: "a field named Administrators", inst: instance("Citadel", withField("Administrators")), want: field("Administrators")},
		{name: "a key named Since, where the record marks when a tenant was made", inst: instance("Citadel", marked(&identityTenantInvitation{key: identityTenantField{Name: "Since"}}, "Opened")), want: markedField("Since")},
		{name: "a field named Since beside the field the record marks", inst: instance("Citadel", marked(withField("Since"), "Opened")), want: markedField("Since")},
		{name: "a field named People, where the record marks when a tenant was made", inst: instance("Citadel", marked(withField("People"), "Opened")), want: markedField("People")},
		{name: "a field named Since, where the record marks none", inst: instance("Citadel", withField("Since"))},
		{name: "a marked field named Since, carried by the invitation", inst: instance("Citadel", marked(withField("Since"), "Since"))},
		{name: "a marked field named Since, carried by no invitation", inst: instance("Citadel", marked(plain(), "Since"))},
		{
			name: "a field saying whether a tenant is disabled named People",
			inst: instance("Citadel", &identityTenantInvitation{key: identityTenantField{Name: "ID"}, disabled: &identityTenantField{Name: "People"}}),
			want: field("People"),
		},
		{
			name: "a directory-only switch named Directory",
			inst: instance("Citadel", &identityTenantInvitation{key: identityTenantField{Name: "ID"}, directoryOnly: &identityTenantField{Name: "Directory"}}),
			want: field("Directory"),
		},
		{name: "a record whose names are its own", inst: instance("Citadel", plain())},
		{
			name: "a record named Group, where no list of tenants is declared",
			inst: &identityInstance{importPath: crewAuthPath, auth: "Crew", tenant: &identityTenant{name: "Group", invitation: plain()}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.inst.refuseListedTenantNamesTaken()
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("refuseListedTenantNamesTaken() = %v, want no refusal", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("refuseListedTenantNamesTaken() = %v, want %q", err, tt.want)
			}
		})
	}
}

// Test_prepareIdentity_listedTenantNamesTaken pins that the run refuses a record with a
// field named as a column the list of tenants adds before any source is written.
func Test_prepareIdentity_listedTenantNamesTaken(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := citadelGenerator(t, oneFieldCitadel("", "\tPeople int64 `spanner:\"People\"`\n"), func(r *resourceGenerator) {
		r.tableMap["Citadels"].Columns["People"] = columnMeta{SpannerType: "INT64"}
	})
	err := r.prepareIdentity()
	if err == nil || !strings.Contains(err.Error(), "the tenant record Citadel has a field named People, and the list of tenants generated for it (CrewListedCitadel) adds a column of that name") {
		t.Fatalf("prepareIdentity() error = %v, want the refusal of the field People", err)
	}
	if _, err := os.Stat(filepath.Join("pkg", "identity")); !os.IsNotExist(err) {
		t.Errorf("the refused run wrote the identity tree (os.Stat() error = %v)", err)
	}
}

// Test_listedTenant_rowsOf pins that the list of tenants the run writes is a view the
// generator takes: loaded beside the tenant record as the run loads the declarations
// packages, its key, its scope and its outlet are the record's, and its @rowsOf resolves to
// the record, which marks when a tenant was made.
func Test_listedTenant_rowsOf(t *testing.T) { //nolint:paralleltest // t.Chdir: the working directory belongs to the whole process
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	record := oneFieldCitadel("\t\"time\"\n", "\t// @tenantSince\n\tRaised time.Time `spanner:\"RaisedAt\" conditions:\"output_only\"`\n")
	r := citadelGenerator(t, record, markedCitadel)
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}
	listed, err := os.ReadFile(filepath.Join("pkg", "identity", listedCitadelFile))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	// The two packages are loaded from beside this file, inside the module, as files the
	// go command is told of and that are on no disk: the record's package and the list's.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "testdata", "listedtenantoverlay")
	recordDir, listDir := filepath.Join(root, "resources"), filepath.Join(root, "crewresources")
	config := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
		Dir:  filepath.Dir(thisFile),
		Overlay: map[string][]byte{
			filepath.Join(recordDir, "citadels.go"):           []byte(record),
			filepath.Join(listDir, "crew_listed_citadels.go"): listed,
		},
	}
	pkgs, err := packages.Load(config, recordDir, listDir)
	if err != nil {
		t.Fatalf("packages.Load() error = %v", err)
	}
	structs := map[string][]*parser.Struct{}
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			t.Fatalf("package %s: %v", pkg.PkgPath, e)
		}
		structs[pkg.Name] = parser.ParsePackage(pkg).Structs
	}

	c := r.client
	resources, err := c.structsToResources(structs["resources"])
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}
	views, err := c.structsToVirtualResources(structs["crewresources"])
	if err != nil {
		t.Fatalf("structsToVirtualResources() error = %v", err)
	}
	if len(resources) != 1 || len(views) != 1 {
		t.Fatalf("read %d records and %d views, want one each", len(resources), len(views))
	}
	c.resources = slices.Concat(resources, views)
	if err := c.resolveRowsOf(c.resources, nil); err != nil {
		t.Fatalf("resolveRowsOf() error = %v", err)
	}
	if field := resources[0].TenantSinceField; field == nil || field.Name() != "Raised" {
		t.Errorf("the record's TenantSinceField = %v, want Raised", field)
	}
	view := views[0]
	if view.RowsOf != "Citadels" || view.PermissionScope != resources[0].PermissionScope || view.PermissionScope == accesstypes.DomainPermissionScope {
		t.Errorf("the view's rows are of %q in the %q scope, want the record's, Citadels, in its %q scope", view.RowsOf, view.PermissionScope, resources[0].PermissionScope)
	}
}
