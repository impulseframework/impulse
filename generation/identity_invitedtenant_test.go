package generation

import (
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// citadelTenantSource is a resources package whose tenant record carries every kind of
// field the reading before the load meets, beside its directories record. An invitation
// carries the name people call the citadel by, a field the create may leave out, a type
// another package declares, a type the package itself declares, a yes or no, a column
// with a default and a list; it carries neither the key, the directory-only switch, the
// secret, the write-only field, the field the server owns nor the generated column.
const citadelTenantSource = `package resources

import (
	"cloud.google.com/go/civil"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// CitadelKind is a type the package declares.
	CitadelKind string

	// Citadel is the tenant record.
	//
	// @resource
	// @tenant
	Citadel struct {
		ID impulse.UUID ` + "`spanner:\"Id\"`" + `
		// Name is what people call the citadel.
		//
		// @tenantName
		Name     string      ` + "`spanner:\"Name\"`" + `
		Motto    *string     ` + "`spanner:\"Motto\"`" + `
		Founded  civil.Date  ` + "`spanner:\"Founded\"`" + `
		Kind     CitadelKind ` + "`spanner:\"Kind\"`" + `
		Walled   bool        ` + "`spanner:\"Walled\"`" + `
		Garrison int64       ` + "`spanner:\"Garrison\"`" + `
		Towers   []string    ` + "`spanner:\"Towers\"`" + `
		// @directoryOnly
		Closed     bool             ` + "`spanner:\"Closed\"`" + `
		Passphrase *resource.Secret ` + "`spanner:\"Passphrase\" conditions:\"secret\"`" + `
		Cipher     *string          ` + "`spanner:\"Cipher\" conditions:\"input_only\"`" + `
		Sealed     string           ` + "`spanner:\"Sealed\" conditions:\"output_only\"`" + `
		Search     string           ` + "`spanner:\"Search\"`" + `
	}

	// CitadelDirectory is the directories record, with the role its people start with.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	CitadelDirectory struct {
		ID impulse.UUID ` + "`spanner:\"Id\"`" + `
		// @domain
		CitadelID impulse.UUID ` + "`spanner:\"CitadelId\"`" + `
		// @directoryMethod
		Method string ` + "`spanner:\"Method\"`" + `
		// @directoryConnection
		Connection string ` + "`spanner:\"Connection\"`" + `
		// @directoryDefaultSeat
		DefaultSeat *string ` + "`spanner:\"DefaultSeat\"`" + `
	}
)
`

// citadelsTable is the tenant record's table as the schema holds it: Motto, Towers, the
// secret and the write-only column take NULL, Garrison has a default, and Search is a
// generated column.
func citadelsTable() *tableMetadata {
	return &tableMetadata{PkCount: 1, Columns: map[string]columnMeta{
		"Id":         {IsPrimaryKey: true, SpannerType: "STRING(36)"},
		"Name":       {SpannerType: "STRING(MAX)"},
		"Motto":      {SpannerType: "STRING(MAX)", IsNullable: true},
		"Founded":    {SpannerType: "DATE"},
		"Kind":       {SpannerType: "STRING(16)"},
		"Walled":     {SpannerType: "BOOL"},
		"Garrison":   {SpannerType: "INT64", HasDefault: true},
		"Towers":     {SpannerType: "ARRAY<STRING(MAX)>", IsNullable: true},
		"Closed":     {SpannerType: "BOOL"},
		"Passphrase": {SpannerType: "BYTES(MAX)", IsNullable: true},
		"Cipher":     {SpannerType: "STRING(MAX)", IsNullable: true},
		"Sealed":     {SpannerType: "STRING(MAX)"},
		"Search":     {SpannerType: "STRING(MAX)", GenerationExpression: "LOWER(Name)"},
	}}
}

// oneFieldCitadel is a resources package whose tenant record carries the key, a name
// and one more field, written as given.
func oneFieldCitadel(imports, field string) string {
	return "package resources\n\nimport (\n\t\"github.com/impulseframework/impulse\"\n" + imports + ")\n\n" +
		"// Citadel is the tenant record.\n//\n// @resource\n// @tenant\ntype Citadel struct {\n" +
		"\tID impulse.UUID `spanner:\"Id\"`\n\tName string `spanner:\"Name\"`\n" + field + "}\n"
}

// Test_tenantInvitationIn pins the reading of the tenant record before the packages are
// loaded, off its syntax and the schema: which fields an invitation carries, each with
// its type as another package writes it, its name on the wire and whether the record's
// create requires it; the name people call a tenant by; the directory-only switch; and
// whether the directories record keeps the role its people start with. A field the
// reading does not know stops it, with the reason in words, and so does a package the
// generated sources could not import.
func Test_tenantInvitationIn(t *testing.T) {
	t.Parallel()

	extra := columnMeta{SpannerType: "STRING(MAX)"}
	tests := []struct {
		name    string
		source  string
		columns map[string]columnMeta
		noTable bool
		want    *identityTenantInvitation
		wantWhy string
	}{
		{
			name:   "the record with every kind of field",
			source: citadelTenantSource,
			want: &identityTenantInvitation{
				fields: []identityTenantField{
					{Name: "Name", Type: "string", Wire: "name", Required: true, Text: true},
					{Name: "Motto", Type: "*string", Wire: "motto"},
					{Name: "Founded", Type: "civil.Date", Wire: "founded", Required: true, Text: true},
					// A type the resources package declares is written with the package.
					{Name: "Kind", Type: "resources.CitadelKind", Wire: "kind", Required: true, Text: true},
					// A yes or no the create requires is no text a form must fill: its zero is
					// an answer.
					{Name: "Walled", Type: "bool", Wire: "walled", Required: true},
					// A column with a default is not required.
					{Name: "Garrison", Type: "int64", Wire: "garrison"},
					{Name: "Towers", Type: "[]string", Wire: "towers"},
				},
				nameField: "Name", nameColumn: "Name",
				directoryOnly: &identityTenantField{Name: "Closed", Type: "bool", Wire: "closed"},
				seats:         true,
				// The packages of the carried fields alone: neither the key's nor the
				// secret's.
				imports: []string{`"cloud.google.com/go/civil"`, `"example.com/acme/beacon/pkg/0/resources"`},
			},
		},
		{
			name:   "a record that marks no name, declares no switch and has no directories record",
			source: oneFieldCitadel("", ""),
			want:   &identityTenantInvitation{fields: []identityTenantField{{Name: "Name", Type: "string", Wire: "name", Required: true, Text: true}}},
		},
		{
			name:    "a record whose table the schema does not hold is read as nothing",
			source:  oneFieldCitadel("", ""),
			noTable: true,
		},
		{
			name:    "a field carrying an annotation the form does not draw",
			source:  oneFieldCitadel("", "\t// @enumerate(Kinds)\n\tKind string `spanner:\"Kind\"`\n"),
			columns: map[string]columnMeta{"Kind": extra},
			wantWhy: "field Kind carries @enumerate, which an invitation's form does not draw",
		},
		{
			name:    "a field the record's create function writes",
			source:  oneFieldCitadel("", "\tCode string `spanner:\"Code\" default_create_fn:\"NewCode\"`\n"),
			columns: map[string]columnMeta{"Code": extra},
			wantWhy: "field Code is written by a function of the record, which an invitation's form does not draw",
		},
		{
			name:    "a field the record's update function writes",
			source:  oneFieldCitadel("", "\tStamp string `spanner:\"Stamp\" output_only_update_fn:\"Now\"`\n"),
			columns: map[string]columnMeta{"Stamp": extra},
			wantWhy: "field Stamp is written by a function of the record, which an invitation's form does not draw",
		},
		{
			name:    "an embedded struct",
			source:  oneFieldCitadel("", "\tBase\n"),
			wantWhy: "the record embeds a struct or declares several fields on one line",
		},
		{
			name:    "several fields on one line",
			source:  oneFieldCitadel("", "\tNorth, South string `spanner:\"North\"`\n"),
			columns: map[string]columnMeta{"North": extra},
			wantWhy: "the record embeds a struct or declares several fields on one line",
		},
		{
			name:    "a field that names no column of the table",
			source:  oneFieldCitadel("", "\tMoat string `spanner:\"Moat\"`\n"),
			wantWhy: "field Moat names no column of the table",
		},
		{
			name:    "a type the reading does not walk",
			source:  oneFieldCitadel("", "\tGrid [3]string `spanner:\"Grid\"`\n"),
			columns: map[string]columnMeta{"Grid": extra},
			wantWhy: "field Grid has a type another package cannot name",
		},
		{
			name:    "a map",
			source:  oneFieldCitadel("", "\tTags map[string]string `spanner:\"Tags\"`\n"),
			columns: map[string]columnMeta{"Tags": extra},
			wantWhy: "field Tags has a type another package cannot name",
		},
		{
			name:    "a qualifier the file imports under no such name",
			source:  oneFieldCitadel("", "\tWhen civil.Date `spanner:\"When\"`\n"),
			columns: map[string]columnMeta{"When": extra},
			wantWhy: "field When has a type another package cannot name",
		},
		{
			name:    "a generic type of an imported package, over a type the package declares",
			source:  oneFieldCitadel("\t\"github.com/impulseframework/impulse/resource\"\n", "\tSeal resource.Key[Vault] `spanner:\"Seal\"`\n"),
			columns: map[string]columnMeta{"Seal": extra},
			want: &identityTenantInvitation{
				fields: []identityTenantField{
					{Name: "Name", Type: "string", Wire: "name", Required: true, Text: true},
					{Name: "Seal", Type: "resource.Key[resources.Vault]", Wire: "seal", Required: true, Text: true},
				},
				imports: []string{`"example.com/acme/beacon/pkg/12/resources"`, `"github.com/impulseframework/impulse/resource"`},
			},
		},
		{
			name:    "an import whose last element is a major version is read by the element before it",
			source:  oneFieldCitadel("\t\"example.com/lib/money/v5\"\n", "\tDues *money.Amount `spanner:\"Dues\"`\n"),
			columns: map[string]columnMeta{"Dues": {SpannerType: "NUMERIC", IsNullable: true}},
			want: &identityTenantInvitation{
				fields: []identityTenantField{
					{Name: "Name", Type: "string", Wire: "name", Required: true, Text: true},
					{Name: "Dues", Type: "*money.Amount", Wire: "dues"},
				},
				imports: []string{`"example.com/lib/money/v5"`},
			},
		},
		{
			name:    "an import under an alias keeps the alias, in the type and in the import the sources carry",
			source:  oneFieldCitadel("\tcv \"cloud.google.com/go/civil\"\n", "\tSince cv.Date `spanner:\"Since\"`\n"),
			columns: map[string]columnMeta{"Since": {SpannerType: "DATE"}},
			want: &identityTenantInvitation{
				fields: []identityTenantField{
					{Name: "Name", Type: "string", Wire: "name", Required: true, Text: true},
					{Name: "Since", Type: "cv.Date", Wire: "since", Required: true, Text: true},
				},
				imports: []string{`cv "cloud.google.com/go/civil"`},
			},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The package's place in the module names its import path, and its files are
			// read where the test wrote them: the working directory is the process's,
			// and other tests of the package move it.
			place := filepath.Join("pkg", strconv.Itoa(i), "resources")
			dir := filepath.Join(t.TempDir(), place)
			writeResourcesPackage(t, dir, map[string]string{"citadels.go": tt.source})
			r := identityFixtureGenerator(t, crewIdentityTables...)
			r.resource = packageDir(place)
			if !tt.noTable {
				table := citadelsTable()
				for name, column := range tt.columns {
					table.Columns[name] = column
				}
				r.tableMap["Citadels"] = table
			}

			got, why, err := r.tenantInvitationIn(dir, "Citadel")
			if err != nil {
				t.Fatalf("tenantInvitationIn() error = %v", err)
			}
			if why != tt.wantWhy {
				t.Errorf("tenantInvitationIn() says %q, want %q", why, tt.wantWhy)
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(identityTenantInvitation{})); diff != "" {
				t.Errorf("tenantInvitationIn() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("a resources package outside the module the generator runs in", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(t.TempDir(), "resources")
		writeResourcesPackage(t, dir, map[string]string{"citadels.go": oneFieldCitadel("", "")})
		r := identityFixtureGenerator(t, crewIdentityTables...)
		r.resource = packageDir(dir)
		r.tableMap["Citadels"] = citadelsTable()

		got, why, err := r.tenantInvitationIn(dir, "Citadel")
		if err != nil || got != nil || why != "the resources package is outside the module the generator runs in" {
			t.Errorf("tenantInvitationIn() = %v, %q, %v; want nothing read and the reason", got, why, err)
		}
	})
}

// Test_identityTenantField_PointerType pins the type the list of invited tenants
// carries a field under: a field the caller may not read on the tenant record is absent
// there, so every field can be.
func Test_identityTenantField_PointerType(t *testing.T) {
	t.Parallel()

	for typ, want := range map[string]string{
		"string":                "*string",
		"*string":               "*string",
		"[]string":              "[]string",
		"civil.Date":            "*civil.Date",
		"resources.CitadelKind": "*resources.CitadelKind",
	} {
		if got := (identityTenantField{Type: typ}).PointerType(); got != want {
			t.Errorf("PointerType() of %s = %s, want %s", typ, got, want)
		}
	}
}

var runsOfBlanks = regexp.MustCompile(`[ \t]+`)

// squeezed is a source with every run of spaces and tabs as one space, so a fragment
// reads the same however the formatter aligned the lines around it.
func squeezed(source string) string {
	return runsOfBlanks.ReplaceAllString(source, " ")
}

// identitySource reads a written source under the identity tree, holds it to Go's
// syntax, and answers it squeezed; "" for a file the run did not write.
func identitySource(t *testing.T, file string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join("pkg", "identity", file))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", file, err)
	}
	if _, err := format.Source(content); err != nil {
		t.Fatalf("%s does not parse: %v\n%s", file, err, content)
	}

	return squeezed(string(content))
}

// invitedTenantFiles are the files of the invitation that makes a tenant, by path from
// the identity tree's root.
func invitedTenantFiles(t *testing.T) []string {
	t.Helper()

	var got []string
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		name := filepath.Base(file)
		if strings.HasPrefix(name, "invite_") && !strings.HasSuffix(name, "_user.go") || strings.Contains(name, "_invited_") {
			got = append(got, file)
		}
	}

	return got
}

// citadelGenerator is the two-auth fixture over a schema that holds every table of the
// crew's management and the tenant record's, in an application whose resources package
// is the given source.
func citadelGenerator(t *testing.T, source string, mutate func(r *resourceGenerator)) *resourceGenerator {
	t.Helper()

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Citadels"] = citadelsTable()
	if mutate != nil {
		mutate(r)
	}
	writeResourcesPackage(t, "pkg/resources", map[string]string{"citadels.go": source})

	return r
}

// Test_prepareIdentity_invitedTenant pins the sources of the invitation that makes a
// tenant, as the run writes them before the packages are loaded: the method staff send
// one with, whose request is the tenant record's own fields; the list of the ones that
// stand, which shows a field only to a caller who may read it on the tenant record; the
// method that withdraws one; and the client, which is not built without what makes the
// tenant. A secret, a write-only field and a field the server owns are in none of them.
func Test_prepareIdentity_invitedTenant(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	if err := citadelGenerator(t, citadelTenantSource, nil).prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	// The tenant record is served on the default outlet, the crew's, so the crew's
	// management carries the three; the members sign in on the portal and carry none.
	want := []string{
		"crewidentity/crew_invited_citadels.go",
		"crewidentity/invite_crew_citadel.go",
		"crewidentity/withdraw_crew_invited_citadel.go",
	}
	if diff := cmp.Diff(want, invitedTenantFiles(t)); diff != "" {
		t.Errorf("the sources of the invitation that makes a tenant (-want +got):\n%s", diff)
	}
	notCarried := []string{"Passphrase", "Cipher", "Sealed", "Search", "CitadelID ", "CitadelClosed"}

	invite := identitySource(t, "crewidentity/invite_crew_citadel.go")
	assertContains(t, "invite_crew_citadel.go", invite, []string{
		generationHeader,
		"// @rpc\n // @outlet(default)\n // @class(access)\n InviteCrewCitadel struct {",
		"Email string `validate:\"required\"`\n Name *string\n",
		// The record's fields, each under the record's name, required as the create
		// requires them.
		"CitadelName string `validate:\"required\"`\n",
		"CitadelMotto *string\n",
		"CitadelFounded civil.Date `validate:\"required\"`\n",
		"CitadelKind resources.CitadelKind `validate:\"required\"`\n",
		"CitadelWalled bool\n",
		"CitadelGarrison int64\n",
		"CitadelTowers []string\n",
		// What the invitation says beyond the record's fields, each named for a grant's
		// condition: no Create grant covers either.
		"// @attribute(directoryOnly)\n CitadelDirectoryOnly bool\n",
		"// @attribute(role)\n DefaultRole *string\n",
		"\"cloud.google.com/go/civil\"",
		"\"example.com/acme/beacon/pkg/resources\"",
		// A required field is carried as given, and any other where it was given.
		"fields[\"Name\"] = m.CitadelName\n",
		"if !reflect.ValueOf(m.CitadelMotto).IsZero() {\n fields[\"Motto\"] = m.CitadelMotto\n }",
		"fields[\"Walled\"] = m.CitadelWalled\n",
		"if !reflect.ValueOf(m.CitadelGarrison).IsZero() {\n fields[\"Garrison\"] = m.CitadelGarrison\n }",
		// The invitation page names the tenant by the field the record marks.
		"TenantName: fmt.Sprint(m.CitadelName),",
		"DirectoryOnly: m.CitadelDirectoryOnly,",
		"DefaultRole: m.DefaultRole,",
		"func (m *InviteCrewCitadel) Execute(ctx context.Context, reader resource.Client, client *Client) (*InviteCrewCitadelResult, error) {",
	}, append([]string{"CitadelWalled bool `validate", "fields[\"Closed\"]"}, notCarried...))
	// The details are checked, as the record's create would check them, before anything
	// is written.
	assertInOrder(t, invite,
		"client.manager.CheckInvitedTenant(ctx, reader, invitation)",
		"reader.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {",
		"client.manager.InviteTenant(ctx, txn, invitation, func(sent bool) {",
	)

	invited := identitySource(t, "crewidentity/crew_invited_citadels.go")
	assertContains(t, "crew_invited_citadels.go", invited, []string{
		generationHeader,
		"// @computed\n// @suppress(readHandler)\n// @outlet(default)\n// @order(CreatedAt asc)\ntype CrewInvitedCitadel struct {",
		"// @primarykey\n ID impulse.UUID `spanner:\"Id\"`",
		// Every field of the record is one the caller may be denied, so each can be
		// absent, the switch among them.
		"CitadelName *string `spanner:\"CitadelName\"`",
		"CitadelMotto *string `spanner:\"CitadelMotto\"`",
		"CitadelFounded *civil.Date `spanner:\"CitadelFounded\"`",
		"CitadelKind *resources.CitadelKind `spanner:\"CitadelKind\"`",
		"CitadelWalled *bool `spanner:\"CitadelWalled\"`",
		"CitadelTowers []string `spanner:\"CitadelTowers\"`",
		"CitadelDirectoryOnly *bool `spanner:\"CitadelDirectoryOnly\"`",
		"DefaultRole *string `spanner:\"DefaultRole\"`",
		"return \"CrewInvitedCitadels\"",
		// The tag a grant on the tenant record names each field by, the switch's too.
		"var crewInvitedCitadelFields = map[string]accesstypes.Tag{\n \"Name\": \"name\",\n \"Motto\": \"motto\",\n \"Founded\": \"founded\",\n \"Kind\": \"kind\",\n \"Walled\": \"walled\",\n \"Garrison\": \"garrison\",\n \"Towers\": \"towers\",\n \"Closed\": \"closed\",\n}",
		"invited, err := client.manager.InvitedTenants(ctx, reader)",
		"readable, err := crewInvitedCitadelReadable(ctx, query.Caller())",
		// The caller's List grant on the tenant record's field, held without a
		// condition, shows the field; a caller nobody bound reads none.
		"if caller.IsZero() || len(crewInvitedCitadelFields) == 0 {\n return readable, nil\n }",
		"record := accesstypes.Resource(\"Citadels\")",
		"asked = append(asked, record.ResourceWithTag(tag))",
		"decisions, err := caller.Check(ctx, accesstypes.List, asked...)",
		"readable[field] = judged && decision.IsGranted()",
		"if readable[\"Closed\"] {\n directoryOnly := inv.DirectoryOnly\n row.CitadelDirectoryOnly = &directoryOnly\n }",
		"if value, ok := carried[\"Name\"]; ok && readable[\"Name\"] {\n if err := json.Unmarshal(value, &r.CitadelName); err != nil {",
		"if value, ok := carried[\"Towers\"]; ok && readable[\"Towers\"] {",
	}, append([]string{"carried[\"Closed\"]"}, notCarried...))

	withdraw := identitySource(t, "crewidentity/withdraw_crew_invited_citadel.go")
	assertContains(t, "withdraw_crew_invited_citadel.go", withdraw, []string{
		generationHeader,
		"// @rpc\n// @outlet(default)\n// @answers(204)\n// @class(access)\ntype WithdrawCrewInvitedCitadel struct {",
		"InvitationID impulse.UUID\n}",
		"func (m *WithdrawCrewInvitedCitadel) Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) error {",
		"client.manager.WithdrawTenantInvitation(ctx, txn, m.InvitationID)",
	}, nil)

	// The crew's client is not built without what makes a citadel; the members', whose
	// people make none, is built as before.
	assertContains(t, "crewidentity/client.go", identitySource(t, "crewidentity/client.go"), []string{
		"func NewClient(controller access.Controller, sessions identity.SessionAPI, tenants identity.TenantMaker, opts ...identity.Option) (*Client, error) {",
		"if tenants == nil {\n return nil, errors.New(\"crewidentity.NewClient(): tenants is nil; pass the generated NewCitadelMaker of the application's handler package, which makes a citadel from an accepted invitation\")\n }",
		"opts = append(opts, identity.WithTenantMaker(tenants))",
	}, nil)
	assertContains(t, "membersidentity/client.go", identitySource(t, "membersidentity/client.go"), []string{
		"func NewClient(controller access.Controller, sessions identity.SessionAPI, opts ...identity.Option) (*Client, error) {",
	}, []string{"TenantMaker", "tenants"})

	// The store writes the tenant an invitation makes with the invitation's row, and
	// reads the ones that stand past the declarations; a schema with no mail links
	// table reads none.
	assertContains(t, "crewidentity/store.go", identitySource(t, "crewidentity/store.go"), []string{
		"newTenant, err := row.NewTenantColumn()",
		"if newTenant != nil {\n columns[\"NewTenant\"] = *newTenant\n }",
		"func (store) InvitedTenants(ctx context.Context, reader resource.ReadOnlyTransaction) ([]identity.InvitedTenantRow, error) {",
		"SQL: \"SELECT Id, Email, Name, NewTenant, InvitedByUserId, CreatedAt, ExpiresAt FROM CrewSessionMailLinks WHERE Kind = @kind AND ConsumedAt IS NULL AND NewTenant IS NOT NULL ORDER BY CreatedAt, Id\",",
		"Params: map[string]any{\"kind\": string(sessionstorage.MailLinkInvitation)},",
	}, nil)
	assertContains(t, "membersidentity/store.go", identitySource(t, "membersidentity/store.go"), []string{
		"func (store) InvitedTenants(context.Context, resource.ReadOnlyTransaction) ([]identity.InvitedTenantRow, error) {\n return nil, nil\n}",
	}, []string{"NewTenant IS NOT NULL", "NewTenantColumn"})
}

// Test_prepareIdentity_invitedTenant_shapes pins what the sources carry of a record
// that marks less, and which auths carry them at all: the one whose people the tenant
// record's outlets sign in, where the application gives its roles and the schema keeps
// its invitations and its memberships.
func Test_prepareIdentity_invitedTenant_shapes(t *testing.T) {
	// As Test_prepareIdentity: each case's working directory is the process's, no
	// t.Parallel.
	oldClient := "func NewClient(controller access.Controller, sessions identity.SessionAPI, opts ...identity.Option) (*Client, error) {"

	t.Run("a record that marks no name, declares no switch and has no directories record", func(t *testing.T) {
		t.Chdir(t.TempDir())

		if err := citadelGenerator(t, oneFieldCitadel("", ""), nil).prepareIdentity(); err != nil {
			t.Fatalf("prepareIdentity() error = %v", err)
		}
		// The invitation page says the application's word for one tenant, and the
		// invitation says neither directory only nor a role to start with. The record's
		// one field is a plain type, so the sources import nothing for it.
		assertContains(t, "invite_crew_citadel.go", identitySource(t, "crewidentity/invite_crew_citadel.go"),
			[]string{"CitadelName string `validate:\"required\"`\n", "TenantName: fmt.Sprint(\"citadel\"),"},
			[]string{"DirectoryOnly", "DefaultRole", "@attribute(role)", "@attribute(directoryOnly)", "pkg/resources\""})
		assertContains(t, "crew_invited_citadels.go", identitySource(t, "crewidentity/crew_invited_citadels.go"),
			[]string{"var crewInvitedCitadelFields = map[string]accesstypes.Tag{\n \"Name\": \"name\",\n}"},
			[]string{"DirectoryOnly", "DefaultRole"})
	})

	t.Run("a field's type reached through an alias is written through it, and the written source resolves it", func(t *testing.T) {
		t.Chdir(t.TempDir())

		source := oneFieldCitadel("\tcv \"cloud.google.com/go/civil\"\n", "\tSince cv.Date `spanner:\"Since\"`\n")
		r := citadelGenerator(t, source, func(r *resourceGenerator) {
			r.tableMap["Citadels"].Columns["Since"] = columnMeta{SpannerType: "DATE"}
		})
		if err := r.prepareIdentity(); err != nil {
			t.Fatalf("prepareIdentity() error = %v", err)
		}
		for _, file := range []string{"crewidentity/invite_crew_citadel.go", "crewidentity/crew_invited_citadels.go"} {
			assertContains(t, file, identitySource(t, file), []string{"cv \"cloud.google.com/go/civil\"", "cv.Date"}, nil)
		}
	})

	none := []struct {
		name   string
		source string
		mutate func(r *resourceGenerator)
	}{
		{
			name:   "an auth whose directory writes its roles invites nobody",
			source: citadelTenantSource,
			mutate: func(r *resourceGenerator) {
				r.defaultOutlet.auth = &outletAuth{importPath: crewAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
			},
		},
		{
			name:   "a schema that keeps no mailed links sends no invitation",
			source: citadelTenantSource,
			mutate: func(r *resourceGenerator) { delete(r.tableMap, "CrewSessionMailLinks") },
		},
		{
			name:   "a schema that keeps no memberships seats no first administrator",
			source: citadelTenantSource,
			mutate: func(r *resourceGenerator) {
				// The permission store's two tables are migrated together.
				delete(r.tableMap, "CrewUserRoles")
				delete(r.tableMap, "CrewRoles")
				delete(r.tableMap, "CrewRoleGrants")
			},
		},
		{
			name:   "an application with no tenant record",
			source: "package resources\n\n// Dock is a table of the application's.\n//\n// @resource\ntype Dock struct {\n\tID string `spanner:\"Id\"`\n}\n",
		},
		{
			name:   "a tenant record the crew's outlets do not serve",
			source: strings.Replace(citadelTenantSource, "// @resource\n\t// @tenant\n", "// @resource\n\t// @tenant\n\t// @outlet(droids)\n", 1),
		},
	}
	for _, tt := range none {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			if err := citadelGenerator(t, tt.source, tt.mutate).prepareIdentity(); err != nil {
				t.Fatalf("prepareIdentity() error = %v", err)
			}
			if got := invitedTenantFiles(t); len(got) != 0 {
				t.Errorf("the sources of the invitation that makes a tenant = %v, want none", got)
			}
			if client := identitySource(t, "crewidentity/client.go"); client != "" {
				assertContains(t, "crewidentity/client.go", client, []string{oldClient}, []string{"TenantMaker"})
			}
		})
	}
}

// Test_prepareIdentity_newTenantColumn pins the refusal of a schema whose mail links
// table lacks the column an invitation that makes a tenant is kept in, for the auth that
// sends them, with the statement that adds it, before any source is written; an auth
// that sends none is asked for nothing.
func Test_prepareIdentity_newTenantColumn(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := citadelGenerator(t, citadelTenantSource, func(r *resourceGenerator) {
		r.tableMap["CrewSessionMailLinks"] = &tableMetadata{Columns: map[string]columnMeta{"Email": {SpannerType: "STRING(MAX)"}, everyTenantColumn: {SpannerType: "BOOL"}}}
	})
	want := `Auth("example.com/acme/beacon/pkg/auth/crew"): the table CrewSessionMailLinks has no column NewTenant, which an invitation that makes a citadel is kept in, and staff bring a citadel in by such an invitation alone; add it as the session library's schema does (ALTER TABLE CrewSessionMailLinks ADD COLUMN NewTenant STRING(MAX))`
	if err := r.prepareIdentity(); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("prepareIdentity() error = %v, want one containing %q", err, want)
	}

	// The members' people make no tenant, so their mail links table is asked for no
	// column: with the crew's in place the run passes whatever the members' holds.
	r = citadelGenerator(t, citadelTenantSource, func(r *resourceGenerator) {
		r.tableMap["MembersSessionMailLinks"] = &tableMetadata{Columns: map[string]columnMeta{everyTenantColumn: {SpannerType: "BOOL"}, mailFailedColumn: {SpannerType: "TIMESTAMP", IsNullable: true}}}
	})
	if err := r.prepareIdentity(); err != nil {
		t.Errorf("prepareIdentity() with the members' mail links table lacking the column: error = %v", err)
	}
}

// Test_prepareIdentity_everyTenantColumn pins the refusal of a schema whose mail links
// table lacks the column an invitation keeps its mark for every tenant in, with the
// statement that adds it, before the auth's store is written, for every auth that keeps
// mail links: the generated store writes the column on every invitation. An auth whose
// schema keeps no mail links is asked for nothing.
func Test_prepareIdentity_everyTenantColumn(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	lacking := &tableMetadata{Columns: map[string]columnMeta{newTenantColumn: {SpannerType: "STRING(MAX)", IsNullable: true}}}
	for _, tt := range []struct {
		table string
		store string
		want  string
	}{
		{
			table: "CrewSessionMailLinks",
			store: "crewidentity/store.go",
			want:  `Auth("example.com/acme/beacon/pkg/auth/crew"): the table CrewSessionMailLinks has no column EveryTenant, which says an invitation's roles are held in every tenant: an accepted invitation gives each role in the place its row keeps and nowhere else; add it as the session library's schema does (ALTER TABLE CrewSessionMailLinks ADD COLUMN EveryTenant BOOL NOT NULL DEFAULT (false))`,
		},
		{
			table: "MembersSessionMailLinks",
			store: "membersidentity/store.go",
			want:  `the table MembersSessionMailLinks has no column EveryTenant, which says an invitation's roles are held in every tenant`,
		},
	} {
		r := citadelGenerator(t, citadelTenantSource, func(r *resourceGenerator) { r.tableMap[tt.table] = lacking })
		if err := r.prepareIdentity(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("prepareIdentity() with %s lacking the column: error = %v, want one containing %q", tt.table, err, tt.want)
		}
		if got := identitySource(t, tt.store); got != "" {
			t.Errorf("%s was written though %s lacks the column, want no store", tt.store, tt.table)
		}
	}

	// An auth whose schema keeps no mail links writes no invitation.
	r := citadelGenerator(t, citadelTenantSource, func(r *resourceGenerator) { delete(r.tableMap, "CrewSessionMailLinks") })
	if err := r.prepareIdentity(); err != nil {
		t.Errorf("prepareIdentity() with no mail links table: error = %v", err)
	}
}

// Test_identityInstance_refuseInvitationNamesTaken pins the refusal of a tenant record
// whose name gives the method that invites one the name of the method that invites a
// person, and that it holds only for an auth that sends such invitations.
func Test_identityInstance_refuseInvitationNamesTaken(t *testing.T) {
	t.Parallel()

	instance := func(record string, creates bool) *identityInstance {
		return &identityInstance{
			importPath: crewAuthPath, auth: "Crew", createsTenant: creates,
			tables: map[identityTable]bool{identityMailLinks: true, identityMemberships: true},
			tenant: &identityTenant{name: record, invitation: &identityTenantInvitation{}},
		}
	}
	want := `Auth("example.com/acme/beacon/pkg/auth/crew"): the tenant record User gives the method that invites one the name InviteCrewUser, which is the method that invites a person into the application; the tenant record takes another name`
	if err := instance("User", true).refuseInvitationNamesTaken(); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("refuseInvitationNamesTaken() of a record named User = %v, want %q", err, want)
	}
	if err := instance("Citadel", true).refuseInvitationNamesTaken(); err != nil {
		t.Errorf("refuseInvitationNamesTaken() of a record named Citadel = %v", err)
	}
	if err := instance("User", false).refuseInvitationNamesTaken(); err != nil {
		t.Errorf("refuseInvitationNamesTaken() of an auth that sends no such invitation = %v", err)
	}
	if err := (&identityInstance{auth: "Crew"}).refuseInvitationNamesTaken(); err != nil {
		t.Errorf("refuseInvitationNamesTaken() of an application with no tenant record = %v", err)
	}
}

// Test_identityInstance_invitesTenants pins which auth carries the invitation that
// makes a tenant, and the names of its three pieces.
func Test_identityInstance_invitesTenants(t *testing.T) {
	t.Parallel()

	every := map[identityTable]bool{identityMailLinks: true, identityMemberships: true}
	read := &identityTenant{name: "Citadel", invitation: &identityTenantInvitation{}}
	tests := []struct {
		name       string
		inst       *identityInstance
		wantMakes  bool
		wantInvite bool
	}{
		{name: "the auth whose people the tenant record's outlets sign in", inst: &identityInstance{auth: "Crew", createsTenant: true, tables: every, tenant: read}, wantMakes: true, wantInvite: true},
		{name: "a record whose fields the reading could not carry", inst: &identityInstance{auth: "Crew", createsTenant: true, tables: every, tenant: &identityTenant{name: "Citadel", unreadable: "an embedded struct"}}, wantMakes: true},
		{name: "an auth whose people create no tenant", inst: &identityInstance{auth: "Crew", tables: every, tenant: read}},
		{name: "an auth whose directory writes its roles", inst: &identityInstance{auth: "Crew", createsTenant: true, rolesSynced: true, tables: every, tenant: read}},
		{name: "no tenant record", inst: &identityInstance{auth: "Crew", createsTenant: true, tables: every}},
		{name: "no mail links table", inst: &identityInstance{auth: "Crew", createsTenant: true, tables: map[identityTable]bool{identityMemberships: true}, tenant: read}},
		{name: "no memberships table", inst: &identityInstance{auth: "Crew", createsTenant: true, tables: map[identityTable]bool{identityMailLinks: true}, tenant: read}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.inst.makesTenants(); got != tt.wantMakes {
				t.Errorf("makesTenants() = %v, want %v", got, tt.wantMakes)
			}
			if got := tt.inst.invitesTenants(); got != tt.wantInvite {
				t.Errorf("invitesTenants() = %v, want %v", got, tt.wantInvite)
			}
			if got := (&identitySourceData{identityInstance: tt.inst}).InvitesTenants(); got != tt.wantInvite {
				t.Errorf("InvitesTenants() = %v, want %v", got, tt.wantInvite)
			}
			r := &resourceGenerator{client: &client{}}
			if got := len(r.invitedTenantSources(tt.inst)); (got == 3) != tt.wantInvite || (got != 0 && got != 3) {
				t.Errorf("invitedTenantSources() names %d files, want 3 where the auth invites and none elsewhere", got)
			}
		})
	}

	inst := tests[0].inst
	if got := []string{inst.inviteTenantMethod(), inst.invitedTenantName(), inst.withdrawInvitedTenantMethod()}; !slices.Equal(got, []string{"InviteCrewCitadel", "CrewInvitedCitadel", "WithdrawCrewInvitedCitadel"}) {
		t.Errorf("the three pieces are named %v", got)
	}
}

// Test_identityInviteTenantTemplate_directory pins what the method that invites a
// tenant says of the person's directory: where the application declares a directories
// record, that the tenant takes the directory of the person who accepts; where it
// declares none, that no tenant takes one, whichever sign-in method the person came
// through. The method itself is the same in both.
func Test_identityInviteTenantTemplate_directory(t *testing.T) {
	t.Parallel()

	data := func(directoryOutlets []string) *identitySourceData {
		return &identitySourceData{
			Source: "Auth", Package: "crewidentity", Outlets: "default",
			identityInstance: &identityInstance{
				auth: "Crew", createsTenant: true,
				tables: map[identityTable]bool{identityMailLinks: true, identityMemberships: true},
				tenant: &identityTenant{
					name: "Citadel", word: "citadel", directoryOutlets: directoryOutlets,
					invitation: &identityTenantInvitation{fields: []identityTenantField{{Name: "Name", Type: "string", Required: true, Text: true}}, nameField: "Name"},
				},
			},
		}
	}
	const (
		takes   = "makes that the citadel's\n\t// directory. The invited person enters nothing"
		takesNo = "whichever sign-in method they came through: the application\n\t// declares no directories record, so no citadel takes a directory. The\n\t// invited person enters nothing"
	)
	tests := []struct {
		name            string
		data            *identitySourceData
		wantDirectories bool
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "an application that declares a directories record",
			data:            data([]string{"default"}),
			wantDirectories: true,
			wantContains:    []string{takes},
			wantNotContains: []string{"takes a directory", "whichever sign-in method"},
		},
		{
			name:            "an application that declares none",
			data:            data(nil),
			wantContains:    []string{takesNo},
			wantNotContains: []string{"makes that the citadel's", "Google Workspace", "Entra"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.data.TenantDirectories(); got != tt.wantDirectories {
				t.Errorf("TenantDirectories() = %v, want %v", got, tt.wantDirectories)
			}
			c := &client{}
			out, err := c.generateTemplateOutput("identityInviteTenantTemplate", identityInviteTenantTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(out); err != nil {
				t.Fatalf("the rendered method does not parse: %v\n%s", err, out)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(string(out), want) {
					t.Errorf("the rendered method lacks %q:\n%s", want, out)
				}
			}
			for _, not := range tt.wantNotContains {
				if strings.Contains(string(out), not) {
					t.Errorf("the rendered method says %q", not)
				}
			}
			// Each comment line is a line of its own: the choice eats no line break.
			if !strings.Contains(string(out), "makes the\n\t// citadel from the details given here") {
				t.Errorf("the comment's lines ran together:\n%s", out)
			}
		})
	}
}

// tenantFixtureDir is the tenant fixture's directory by its place beside this file: the
// working directory is the process's, and other tests of the package move it (client
// construction moves to the module's root), so a test that runs beside them reads the
// fixture's syntax by an absolute path.
func tenantFixtureDir(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}

	return filepath.Join(filepath.Dir(thisFile), "testdata", "tenantfixture")
}

// Test_holdIdentityTenantInvitation pins the check after the load: the fields the
// invitation's sources were written for are the ones the loaded record carries, a record
// the reading could not carry is refused with what stopped it, and a record whose create
// requires a secret, or a write-only field, is refused, naming the field: no invitation
// carries either, so the tenant could never be made.
func Test_holdIdentityTenantInvitation(t *testing.T) {
	t.Parallel()

	staff := func(r *resourceGenerator) { r.defaultOutlet.auth = openAuth("example.com/app/pkg/auth/staff") }
	tests := []struct {
		name    string
		structs []string
		// record mutates the loaded record, and written what the sources were written for.
		record  func(record *resourceInfo)
		written func(inst *identityInstance)
		// beside is another auth of the program, held after the one the case writes.
		beside  *identityInstance
		wantErr string
	}{
		{
			name:    "the reading and the load agree: the fields, what each is on the wire and whether the create requires it",
			structs: []string{"Citadel"},
		},
		{
			name:    "and on the directory-only switch",
			structs: []string{"Harbor", "HarborDirectory"},
		},
		{
			name:    "a record whose create requires a secret is refused, naming it",
			structs: []string{"Keep"},
			wantErr: "struct Keep: field Passphrase is a secret the record's create requires, and no invitation carries a secret: a keep made by invitation is given its secret afterward, so let the column take NULL or give it a default",
		},
		{
			name:    "a record whose create requires a write-only field is refused, naming it",
			structs: []string{"Redoubt"},
			wantErr: `struct Redoubt: field Cipher is write-only (conditions:"input_only") and the record's create requires it, and no invitation carries a value the server never returns: a redoubt made by invitation is given it afterward, so let the column take NULL or give it a default`,
		},
		{
			name:    "a record with a field the server owns, which no create supplies",
			structs: []string{"Station"},
		},
		{
			name:    "a @file key that takes no NULL trips nothing: it is the server's to write, and no create carries it",
			structs: []string{"Redoubt"},
			record: func(record *resourceInfo) {
				for _, field := range record.Fields {
					if field.Name() == "Cipher" {
						field.IsFileKey = true
					}
				}
			},
		},
		{
			name:    "a record the reading could not carry is refused with what stopped it",
			structs: []string{"Citadel"},
			written: func(inst *identityInstance) {
				inst.tenant.invitation, inst.tenant.unreadable = nil, "field Kind carries @enumerate, which an invitation's form does not draw"
			},
			wantErr: "struct Citadel: the invitation that makes a citadel cannot carry the record: field Kind carries @enumerate, which an invitation's form does not draw. Staff bring a citadel in by an invitation alone, so the record keeps to fields its form can draw",
		},
		{
			name:    "sources written for a field the load does not find are the generator's fault",
			structs: []string{"Citadel"},
			written: func(inst *identityInstance) {
				inst.tenant.invitation.fields = append(inst.tenant.invitation.fields, identityTenantField{Name: "Moat", Type: "string", Wire: "moat"})
			},
			wantErr: "this is a fault of the generator's reading before the load: report it",
		},
		{
			name:    "and so are sources written for a field the create requires where the load finds it optional",
			structs: []string{"Citadel"},
			written: func(inst *identityInstance) { inst.tenant.invitation.fields[1].Required = true },
			wantErr: "the invitation's sources were written for the fields [Name name required=true; Motto motto required=true; Founded founded required=true; Walled walled required=true] and the loaded record carries [Name name required=true; Motto motto required=false; Founded founded required=true; Walled walled required=true]",
		},
		{
			name:    "and sources written for no switch where the load finds one",
			structs: []string{"Harbor", "HarborDirectory"},
			written: func(inst *identityInstance) { inst.tenant.invitation.directoryOnly = nil },
			wantErr: "and the loaded record carries [Name name required=true; the directory-only switch DirectoryOnly directoryOnly]",
		},
		{
			name:    "where no auth invites, a create that requires a secret is the application's own to supply",
			structs: []string{"Keep"},
			written: func(inst *identityInstance) { inst.createsTenant = false },
		},
		{
			name:    "an auth that makes no tenant holds nothing: its sources carry no invitation",
			structs: []string{"Citadel"},
			written: func(inst *identityInstance) {
				inst.createsTenant = false
				inst.tenant.invitation, inst.tenant.unreadable = nil, "not read"
			},
		},
		{
			name:    "nor does it beside the auth that makes one: the record is held to that auth's sources alone",
			structs: []string{"Citadel"},
			beside: &identityInstance{
				importPath: "example.com/app/pkg/auth/members", auth: "Members",
				tables: map[identityTable]bool{identityMailLinks: true, identityMemberships: true},
				tenant: &identityTenant{name: "Citadel", word: "citadel", unreadable: "not read"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := invitedFixtureGenerator(t, staff, tt.structs...)
			record := r.tenantRecord()
			if tt.record != nil {
				tt.record(record)
			}
			// The sources are written from the fixture's own syntax, as a run reads the
			// resources package before it loads it.
			r.resource, r.modulePathRoot = "testdata/tenantfixture", "example.com/app"
			invitation, why, err := r.tenantInvitationIn(tenantFixtureDir(t), record.Name())
			if err != nil || invitation == nil {
				t.Fatalf("tenantInvitationIn() = %v, %q, %v; want the record read", invitation, why, err)
			}
			inst := &identityInstance{
				importPath: "example.com/app/pkg/auth/staff", auth: "Staff", createsTenant: true,
				tables: map[identityTable]bool{identityMailLinks: true, identityMemberships: true},
				tenant: &identityTenant{name: record.Name(), word: strings.ToLower(record.Name()), invitation: invitation},
			}
			if tt.written != nil {
				tt.written(inst)
			}
			r.identity = []*identityInstance{inst}
			if tt.beside != nil {
				r.identity = append(r.identity, tt.beside)
			}

			err = r.holdIdentityTenantInvitation()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("holdIdentityTenantInvitation() error = %v", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("holdIdentityTenantInvitation() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}

	t.Run("an application with no tenant record holds nothing", func(t *testing.T) {
		t.Parallel()

		if err := userFixtureGenerator(t, false).holdIdentityTenantInvitation(); err != nil {
			t.Errorf("holdIdentityTenantInvitation() error = %v", err)
		}
	})

	// The check runs where the run resolves the record across the kinds, once the
	// record is whole, so a record whose create requires a secret stops the run there.
	t.Run("the run's own step holds the invitation", func(t *testing.T) {
		t.Parallel()

		r := invitedFixtureGenerator(t, staff, "Keep")
		r.resource, r.modulePathRoot, r.identityTree = "testdata/tenantfixture", "example.com/app", "pkg/identity"
		invitation, why, err := r.tenantInvitationIn(tenantFixtureDir(t), "Keep")
		if err != nil || invitation == nil {
			t.Fatalf("tenantInvitationIn() = %v, %q, %v; want the record read", invitation, why, err)
		}
		r.identity = []*identityInstance{{
			importPath: "example.com/app/pkg/auth/staff", auth: "Staff", createsTenant: true,
			tables: map[identityTable]bool{identityMailLinks: true, identityMemberships: true},
			tenant: &identityTenant{name: "Keep", word: "keep", invitation: invitation},
		}}
		want := "struct Keep: field Passphrase is a secret the record's create requires, and no invitation carries a secret: a keep made by invitation is given its secret afterward"
		if err := r.resolveCrossKindDeclarations(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("resolveCrossKindDeclarations() error = %v, want one containing %q", err, want)
		}
	})
}
