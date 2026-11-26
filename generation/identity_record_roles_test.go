package generation

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// voyageRecordSource declares Ship the tenant record and Voyage a ship's record a role can
// be held on (@holdsRoles), in the grouped form the resources packages use.
const voyageRecordSource = `package resources

import "github.com/impulseframework/impulse"

type (
	// Ship is the tenant record.
	//
	// @resource
	// @tenant
	Ship struct {
		ID impulse.UUID ` + "`spanner:\"Id\"`" + `
	}

	// Voyage is one ship's voyage, which a role can be held on.
	//
	// @resource
	// @permissionScope(domain)
	// @holdsRoles
	Voyage struct {
		ID impulse.UUID ` + "`spanner:\"Id\"`" + `
		// @domain
		ShipID string ` + "`spanner:\"ShipId\"`" + `
	}
)
`

// The role files of the fixture's two auths: the crew's Navigator can be held on one
// voyage, and the members' Deckhand on none.
const (
	navigatorOnVoyages = `{"roles": {"domain": [{"name": "Navigator", "description": "Plots a voyage.", "permissions": {}, "heldOn": ["Voyages"]}]}}`
	deckhandOnNothing  = `{"roles": {"domain": [{"name": "Deckhand", "description": "Works the deck.", "permissions": {}}]}}`
)

// writeRoleFile writes the role file of an auth package of the fixture's module.
func writeRoleFile(t *testing.T, importPath, content string) {
	t.Helper()

	dir, ok := (&resourceGenerator{client: &client{modulePathRoot: "example.com/acme/beacon"}}).moduleDir(importPath)
	if !ok {
		t.Fatalf("the auth %q is no package of the fixture's module", importPath)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, identityRolesFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// heldRecordsGenerator is the fixture's run: both auths' tables with the tables of the
// roles held on a record, the crew's groups, and the ships the voyages are under.
func heldRecordsGenerator(t *testing.T) *resourceGenerator {
	t.Helper()

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables,
		[]string{"CrewRecordUserRoles", "CrewRecordGroupRoles", "MembersRecordUserRoles", "MembersRecordGroupRoles"})...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()

	return r
}

// readIdentityFile reads one file of the generated tree of the fixture's run.
func readIdentityFile(t *testing.T, file string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join("pkg/identity", file))
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", file, err)
	}

	return squashSpace(string(content))
}

// recordRoleFiles are the files of the generated tree that declare the tables of the
// roles held on a record.
func recordRoleFiles(t *testing.T) []string {
	t.Helper()

	var files []string
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		if strings.Contains(file, "_record_") {
			files = append(files, file)
		}
	}

	return files
}

// Test_prepareIdentity_recordRoles pins what an instance writes for the roles held on one
// record: an instance whose role file holds a role on a record the application marks
// declares the two tables, a person's and a group's, as a tenant's rows with no route of
// their own, each write leaving its change event, the group's delete takes the group's
// roles on records with its other roles, and the store reads and writes the rows through
// the two declarations. An instance whose role file names no record declares neither,
// though the schema holds the tables, and its store reads nothing and refuses a write,
// saying what is missing; one that holds records and keeps no groups refuses a group's
// role on a record as it refuses every group's write.
func Test_prepareIdentity_recordRoles(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": voyageRecordSource})
	writeRoleFile(t, crewAuthPath, navigatorOnVoyages)
	writeRoleFile(t, membersAuthPath, deckhandOnNothing)
	if err := heldRecordsGenerator(t).prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	want := []string{
		"internal/crewresources/crew_record_group_roles.go",
		"internal/crewresources/crew_record_user_roles.go",
	}
	if got := recordRoleFiles(t); !slices.Equal(got, want) {
		t.Errorf("the declarations of the roles held on a record = %q, want %q", got, want)
	}

	assertContains(t, "the people's declaration", readIdentityFile(t, "internal/crewresources/crew_record_user_roles.go"), squashEach([]string{
		generationHeader, "package crewresources",
		"// @resource\n\t// @suppress(listHandler)\n\t// @suppress(readHandler)\n\t// @suppress(patchHandler)\n\t// @permissionScope(domain)\n\t// @outlet(default)\n" +
			"\t// @rowWord(role on a record, plural: roles on records)\n\tCrewRecordUserRole struct {",
		// The key's fields in the table's key order; the Resource column is named apart
		// from the method every declaration carries.
		"Axis string `spanner:\"Axis\"`\n\t\t// @domain\n\t\tDomainID string `spanner:\"DomainId\"`\n\t\tResourceName string `spanner:\"Resource\"`\n" +
			"\t\tRecordID string `spanner:\"RecordId\"`\n\t\t// @roleOf(Crew)\n\t\tRole string `spanner:\"Role\"`\n\t\tUserID string `spanner:\"UserId\"`\n" +
			"\t\tStartsAt *time.Time `spanner:\"StartsAt\"`\n\t\tEndsAt *time.Time `spanner:\"EndsAt\"`\n\t\tCreatedAt time.Time `spanner:\"CreatedAt\"`\n\t}",
		"func (CrewRecordUserRole) Config() resource.Config {\n\treturn defaultConfig().SetTrackChanges(true)\n}",
	}), nil)
	assertContains(t, "the groups' declaration", readIdentityFile(t, "internal/crewresources/crew_record_group_roles.go"), squashEach([]string{
		"// @permissionScope(domain)", "// @rowWord(role on a record, plural: roles on records)\n\tCrewRecordGroupRole struct {",
		"\t\t// @roleOf(Crew)\n\t\tRole string `spanner:\"Role\"`\n\t\tGroupID impulse.UUID `spanner:\"GroupId\"`\n\t\tStartsAt *time.Time",
		"func (CrewRecordGroupRole) Config() resource.Config {\n\treturn defaultConfig().SetTrackChanges(true)\n}",
	}), nil)
	// The group's delete takes its roles on records with its members and its roles.
	assertContains(t, "the groups", readIdentityFile(t, "internal/crewresources/crew_groups.go"),
		squashEach([]string{"// @deleteCascade(CrewGroupMember, CrewGroupRole, CrewRecordGroupRole)"}), nil)

	assertContains(t, "the crew's store", readIdentityFile(t, "crewidentity/store.go"), squashEach([]string{
		"func (store) SetRecordMembership(ctx context.Context, txn resource.ReadWriteTransaction, key *access.RecordMembershipKey, period accesstypes.Period) error {",
		"create := crewresources.NewCrewRecordUserRoleCreatePatch(key.Axis, key.Domain, key.Resource, key.Record, string(key.Role), string(key.User)).\n" +
			"\t\tSetStartsAt(period.StartPtr()).\n\t\tSetEndsAt(period.EndPtr()).\n\t\tSetCreatedAt(spanner.CommitTimestamp).\n\t\tOrSkip()",
		"if err := create.Buffer(ctx, txn, eventsource.UserEvent(ctx)); err != nil {",
		"update := crewresources.NewCrewRecordUserRoleUpdatePatch(key.Axis, key.Domain, key.Resource, key.Record, string(key.Role), string(key.User)).\n" +
			"\t\tSetStartsAt(period.StartPtr()).\n\t\tSetEndsAt(period.EndPtr())",
		"func (store) DeleteRecordMembership(ctx context.Context, txn resource.ReadWriteTransaction, key *access.RecordMembershipKey) error {",
		"patch := crewresources.NewCrewRecordUserRoleDeletePatch(key.Axis, key.Domain, key.Resource, key.Record, string(key.Role), string(key.User))\n" +
			"\tif err := patch.Buffer(ctx, txn, deletedBy(ctx)...); err != nil {\n\t\tif httpio.HasNotFound(err) {\n\t\t\treturn nil\n\t\t}",
		"func (store) SetRecordGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, key *access.RecordGroupRoleKey, period accesstypes.Period) error {",
		"crewresources.NewCrewRecordGroupRoleCreatePatch(key.Axis, key.Domain, key.Resource, key.Record, string(key.Role), id)",
		"func (store) DeleteRecordGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, key *access.RecordGroupRoleKey) error {",
		"func (store) RecordRoles(ctx context.Context, txn resource.ReadWriteTransaction, at accesstypes.RecordScope) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {",
		"SetAxis(record.Axis).SetDomainID(record.Domain).SetResourceName(record.Resource).SetRecordID(record.Record)",
		"func (store) TenantRecordRoles(ctx context.Context, txn resource.ReadWriteTransaction, domain accesstypes.Domain) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {",
		"func (store) RecordMemberships(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]access.RecordMembershipKey, error) {",
		// Read in the tables' key order.
		"Sort(crewresources.NewCrewRecordUserRoleSort().Axis().DomainID().ResourceName().RecordID().Role().UserID())",
		"Sort(crewresources.NewCrewRecordGroupRoleSort().Axis().DomainID().ResourceName().RecordID().Role().GroupID())",
	}), []string{"errNoRecordRoles"})

	// The members' role file holds no role on a record: nothing is declared, the store
	// reads nothing and refuses a write.
	assertContains(t, "the members' store", readIdentityFile(t, "membersidentity/store.go"), squashEach([]string{
		"var errNoRecordRoles = errors.New(\"no record of this application holds roles: mark one with @holdsRoles and name it in a role's heldOn\")",
		"func (store) SetRecordMembership(context.Context, resource.ReadWriteTransaction, *access.RecordMembershipKey, accesstypes.Period) error {\n\treturn errNoRecordRoles\n}",
		"func (store) DeleteRecordMembership(context.Context, resource.ReadWriteTransaction, *access.RecordMembershipKey) error {\n\treturn errNoRecordRoles\n}",
		"func (store) SetRecordGroupRole(context.Context, resource.ReadWriteTransaction, *access.RecordGroupRoleKey, accesstypes.Period) error {\n\treturn errNoRecordRoles\n}",
		"func (store) DeleteRecordGroupRole(context.Context, resource.ReadWriteTransaction, *access.RecordGroupRoleKey) error {\n\treturn errNoRecordRoles\n}",
		"func (store) RecordRoles(context.Context, resource.ReadWriteTransaction, accesstypes.RecordScope) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {\n\treturn nil, nil, nil\n}",
		"func (store) TenantRecordRoles(context.Context, resource.ReadWriteTransaction, accesstypes.Domain) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {\n\treturn nil, nil, nil\n}",
		"func (store) RecordMemberships(context.Context, resource.ReadWriteTransaction, impulse.UUID) ([]access.RecordMembershipKey, error) {\n\treturn nil, nil\n}",
	}), []string{"NewMembersRecordUserRole", "NewMembersRecordGroupRole"})

	// The members hold a role on a voyage too, and keep no groups: their people's roles
	// on a record are declared and kept, and a group's is refused as every group's write
	// is.
	t.Run("an instance that keeps no groups", func(t *testing.T) {
		writeRoleFile(t, membersAuthPath, strings.ReplaceAll(navigatorOnVoyages, "Navigator", "Lookout"))
		if err := heldRecordsGenerator(t).prepareIdentity(); err != nil {
			t.Fatalf("prepareIdentity() error = %v", err)
		}
		want := []string{
			"internal/crewresources/crew_record_group_roles.go",
			"internal/crewresources/crew_record_user_roles.go",
			"internal/membersresources/members_record_user_roles.go",
		}
		if got := recordRoleFiles(t); !slices.Equal(got, want) {
			t.Errorf("the declarations of the roles held on a record = %q, want %q", got, want)
		}
		assertContains(t, "the members' store", readIdentityFile(t, "membersidentity/store.go"), squashEach([]string{
			"create := membersresources.NewMembersRecordUserRoleCreatePatch(key.Axis, key.Domain, key.Resource, key.Record, string(key.Role), string(key.User)).",
			"func (store) SetRecordGroupRole(context.Context, resource.ReadWriteTransaction, *access.RecordGroupRoleKey, accesstypes.Period) error {\n\treturn errNoGroups\n}",
			"func (store) DeleteRecordGroupRole(context.Context, resource.ReadWriteTransaction, *access.RecordGroupRoleKey) error {\n\treturn errNoGroups\n}",
		}), []string{"errNoRecordRoles", "NewMembersRecordGroupRole"})
	})

	// The crew's role file names no record either: what was written for it goes.
	t.Run("a role file that names no record", func(t *testing.T) {
		writeRoleFile(t, crewAuthPath, deckhandOnNothing)
		writeRoleFile(t, membersAuthPath, deckhandOnNothing)
		if err := heldRecordsGenerator(t).prepareIdentity(); err != nil {
			t.Fatalf("prepareIdentity() error = %v", err)
		}
		if got := recordRoleFiles(t); len(got) != 0 {
			t.Errorf("the declarations of the roles held on a record = %q, want none", got)
		}
		assertContains(t, "the groups", readIdentityFile(t, "internal/crewresources/crew_groups.go"),
			squashEach([]string{"// @deleteCascade(CrewGroupMember, CrewGroupRole)"}), nil)
		assertContains(t, "the crew's store", readIdentityFile(t, "crewidentity/store.go"),
			squashEach([]string{"func (store) RecordRoles(context.Context, resource.ReadWriteTransaction, accesstypes.RecordScope) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {\n\treturn nil, nil, nil\n}"}),
			[]string{"NewCrewRecordUserRole"})
	})
}

// Test_identityDeleteCosts_recordRoles pins what the delete of a row of each table of the
// roles held on a record costs: the delete, an entry out of each index besides the key,
// and the change record the declarations write. The group's table costs nothing for an
// instance that keeps no groups.
func Test_identityDeleteCosts_recordRoles(t *testing.T) {
	t.Parallel()

	r := identityFixtureGenerator(t, slices.Concat(crewIdentityTables, crewGroupTables, membersIdentityTables,
		[]string{"CrewRecordUserRoles", "CrewRecordGroupRoles", "MembersRecordUserRoles", "MembersRecordGroupRoles"})...)
	for table, indexes := range map[string]int{"CrewRecordUserRoles": 1, "CrewRecordGroupRoles": 1, "MembersRecordUserRoles": 1, "MembersRecordGroupRoles": 1} {
		r.tableMap[table].Indexes = []indexMeta{{Name: "PRIMARY_KEY", PrimaryKey: true}}
		for range indexes {
			r.tableMap[table].Indexes = append(r.tableMap[table].Indexes, indexMeta{Name: table + "ByUserId"})
		}
	}
	instances, err := r.identityInstances()
	if err != nil {
		t.Fatalf("identityInstances() error = %v", err)
	}
	want := map[string][]identityDeleteCost{
		"Crew": {
			{Field: "RecordMembership", Cost: "resource.DeleteCost{IndexEntries: 1}.Mutations(true)"},
			{Field: "RecordGroupRole", Cost: "resource.DeleteCost{IndexEntries: 1}.Mutations(true)"},
		},
		"Members": {
			{Field: "RecordMembership", Cost: "resource.DeleteCost{IndexEntries: 1}.Mutations(true)"},
		},
	}
	for _, inst := range instances {
		var got []identityDeleteCost
		for _, cost := range inst.deleteCosts {
			if strings.HasPrefix(cost.Field, "Record") {
				got = append(got, cost)
			}
		}
		if !slices.Equal(got, want[inst.auth]) {
			t.Errorf("the %s instance's costs of the roles held on a record = %v, want %v", inst.auth, got, want[inst.auth])
		}
	}
}
