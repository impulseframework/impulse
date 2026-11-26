package generation

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
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

	content, err := os.ReadFile(filepath.Join("pkg", "identity", file))
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
		// A group the tenant's delete takes takes its roles on records, whose change
		// history records the delete as the request's person's.
		"patch := crewresources.NewCrewGroupDeletePatch(id).WithEverythingUnder().WithLibraryRowsAlone()\n\tif err := patch.Buffer(ctx, txn, deletedBy(ctx)...); err != nil {",
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
		assertContains(t, "the crew's store", readIdentityFile(t, "crewidentity/store.go"), squashEach([]string{
			"func (store) RecordRoles(context.Context, resource.ReadWriteTransaction, accesstypes.RecordScope) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {\n\treturn nil, nil, nil\n}",
			"patch := crewresources.NewCrewGroupDeletePatch(id).WithEverythingUnder().WithLibraryRowsAlone()\n\tif err := patch.Buffer(ctx, txn); err != nil {",
		}), []string{"NewCrewRecordUserRole"})
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

// voyagesTable is the fixture's table of voyages, keyed by one id column, each under its
// ship.
func voyagesTable() *tableMetadata {
	return &tableMetadata{PkCount: 1, Columns: map[string]columnMeta{
		"Id":     {IsPrimaryKey: true, SpannerType: "STRING(36)"},
		"ShipId": {SpannerType: "STRING(36)"},
	}}
}

// sharingGenerator is the fixture's run with the voyages' table, so the key of a voyage is
// read (heldRecordKeyType) and a voyage's sharing is written.
func sharingGenerator(t *testing.T) *resourceGenerator {
	t.Helper()

	r := heldRecordsGenerator(t)
	r.tableMap["Voyages"] = voyagesTable()

	return r
}

// sharingFiles are the files of the generated tree of a voyage's sharing.
func sharingFiles(t *testing.T) []string {
	t.Helper()

	var files []string
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		if strings.Contains(file, "voyage") {
			files = append(files, file)
		}
	}

	return files
}

// Test_prepareIdentity_recordSharing pins what an instance writes for the sharing of a
// record its role file holds a role on: the tenant's four methods that give a person or a
// group a role on one voyage and take it away, each naming the voyage's row by its key
// field's type and picking a person from the tenant's people and a group from its groups;
// the list the give methods pick a role from, fed by the identity package; and the view of
// who holds what on a voyage, over both tables. The generated grants answer the list's
// question for the two give methods. An instance whose role file holds no role on the
// record writes none of it, and one that keeps no groups writes no group pair.
func Test_prepareIdentity_recordSharing(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": voyageRecordSource})
	writeRoleFile(t, crewAuthPath, navigatorOnVoyages)
	writeRoleFile(t, membersAuthPath, deckhandOnNothing)
	if err := sharingGenerator(t).prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	want := []string{
		"crewidentity/crew_voyage_roles.go",
		"crewidentity/give_crew_group_role_on_voyage.go",
		"crewidentity/give_crew_role_on_voyage.go",
		"crewidentity/take_crew_group_role_on_voyage.go",
		"crewidentity/take_crew_role_on_voyage.go",
		"internal/crewresources/crew_voyage_holders.go",
	}
	if got := sharingFiles(t); !slices.Equal(got, want) {
		t.Errorf("the files of a voyage's sharing = %q, want %q", got, want)
	}

	assertContains(t, "the give method", readIdentityFile(t, "crewidentity/give_crew_role_on_voyage.go"), squashEach([]string{
		generationHeader, "package crewidentity",
		"// GiveCrewRoleOnVoyage gives a person of one Ship a role on one Voyage of the tenant, for a period where the request names one.\n//\n" +
			"// @rpc\n// @permissionScope(domain)\n// @outlet(default)\n// @answers(204)\n// @class(access)\ntype GiveCrewRoleOnVoyage struct {",
		"// @target(Voyage)\n\tRecordID impulse.UUID",
		"// @enumerate(CrewShipUsers)\n\tUserID impulse.UUID",
		"// @attribute(role)\n\t// @enumerate(CrewVoyageRoles)\n\tRole string `validate:\"required\"`",
		"StartsAt *time.Time", "EndsAt *time.Time",
		"if err := tenant.GiveRoleOn(ctx, txn, \"Voyages\", m.RecordID.String(), m.UserID, accesstypes.Role(m.Role), period); err != nil {",
	}), []string{"\"fmt\""})
	assertContains(t, "the take method", readIdentityFile(t, "crewidentity/take_crew_role_on_voyage.go"), squashEach([]string{
		"// TakeCrewRoleOnVoyage takes away a role a person holds on one Voyage of one Ship.",
		"// @answers(204)\n// @class(access)\ntype TakeCrewRoleOnVoyage struct {",
		// A grant's condition reads the role taken as new.role, as the give's reads the role given.
		"// @target(Voyage)\n\tRecordID impulse.UUID\n\tUserID impulse.UUID\n\t// Role is the role taken away. A grant's condition reads it as new.role, so a role file\n\t// can limit which roles the grant's holder takes away.\n\t//\n\t// @attribute(role)\n\tRole string `validate:\"required\"`\n}",
		"if err := tenant.TakeRoleOn(ctx, txn, \"Voyages\", m.RecordID.String(), m.UserID, accesstypes.Role(m.Role)); err != nil {",
	}), nil)
	assertContains(t, "the group's give method", readIdentityFile(t, "crewidentity/give_crew_group_role_on_voyage.go"), squashEach([]string{
		"// GiveCrewGroupRoleOnVoyage gives a group of one Ship a role on one Voyage of the tenant, for a period where the request names one.",
		"// @target(Voyage)\n\tRecordID impulse.UUID",
		"// @enumerate(CrewShipGroups)\n\tGroupID impulse.UUID",
		"// @enumerate(CrewVoyageRoles)\n\tRole string",
		"if err := tenant.GiveGroupRoleOn(ctx, txn, \"Voyages\", m.RecordID.String(), m.GroupID, accesstypes.Role(m.Role), period); err != nil {",
	}), nil)
	assertContains(t, "the group's take method", readIdentityFile(t, "crewidentity/take_crew_group_role_on_voyage.go"), squashEach([]string{
		"// @target(Voyage)\n\tRecordID impulse.UUID\n\tGroupID impulse.UUID\n\t// Role is the role taken away. A grant's condition reads it as new.role, so a role file\n\t// can limit which roles the grant's holder takes away.\n\t//\n\t// @attribute(role)\n\tRole string `validate:\"required\"`\n}",
		"if err := tenant.TakeGroupRoleOn(ctx, txn, \"Voyages\", m.RecordID.String(), m.GroupID, accesstypes.Role(m.Role)); err != nil {",
	}), nil)
	assertContains(t, "the roles to give on a voyage", readIdentityFile(t, "crewidentity/crew_voyage_roles.go"), squashEach([]string{
		"// @computed\n// @permissionScope(domain)\n// @suppress(readHandler)\n// @outlet(default)\n// @order(Name asc)\n// @rowWord(role)\ntype CrewVoyageRole struct {",
		"Method string `spanner:\"Method\" allow_filter:\"true\"`\n\t// RecordID is the key, as text, of the Voyage the role is listed for, the one\n\t// the request's filter names. The list is under its record by it.\n\tRecordID string `spanner:\"RecordId\" allow_filter:\"true\"`\n}",
		"// under its record, by the record each row is listed for: a grant on it held on one\n// Voyage lists the roles to give on that Voyage alone",
		"return \"CrewVoyageRoles\"",
		"record, recordNamed, err := identity.GivingRecord(qSet.Resource(), qSet.Filter().Take(\"RecordID\"))",
		"method, named, err := identity.GivingMethod(qSet.Resource(), []accesstypes.Resource{\n\t\t\tGiveCrewRoleOnVoyage{}.Method(),\n\t\t\tGiveCrewGroupRoleOnVoyage{}.Method(),\n\t\t}, qSet.Filter().Take(\"Method\"))",
		"if !named || !recordNamed {\n\t\t\treturn\n\t\t}",
		"roles, err := client.manager.GivableRecordRoles(ctx, db, method, \"Voyages\")",
		"Method: string(method), RecordID: record}, nil) {",
	}), []string{"gives nothing on it"})
	assertContains(t, "who holds what on a voyage", readIdentityFile(t, "internal/crewresources/crew_voyage_holders.go"), squashEach([]string{
		"// @virtual\n\t// @permissionScope(domain)\n\t// @outlet(default)\n\t// @order(Name asc, Role asc)\n\t// @page(default: 25, max: 200)\n\t// @rowWord(holder)\n\tCrewVoyageHolder struct {",
		"// @primarykey\n\t\tRecordID string `spanner:\"RecordId\" index:\"true\"`\n\t\t// @primarykey\n\t\tMemberKind string `spanner:\"MemberKind\" index:\"true\"`\n" +
			"\t\t// @primarykey\n\t\tMemberID impulse.UUID `spanner:\"MemberId\" index:\"true\"`\n\t\t// @primarykey\n\t\t// @roleOf(Crew)\n\t\tRole string `spanner:\"Role\" index:\"true\"`\n" +
			"\t\t// @domain\n\t\tDomainID string `spanner:\"DomainId\" index:\"true\"`\n\t\tName string `spanner:\"Name\" index:\"true\"`\n" +
			"\t\tStartsAt *time.Time `spanner:\"StartsAt\"`\n\t\tEndsAt *time.Time `spanner:\"EndsAt\"`\n\t}",
		"'person' AS MemberKind,\n r.UserId AS MemberId,", "COALESCE(NULLIF(u.Name, ''), u.Username) AS Name,",
		"FROM CrewRecordUserRoles r\nJOIN CrewSessionUsers u ON u.Id = r.UserId\nWHERE r.Axis = '' AND r.Resource = 'Voyages'\n AND (r.EndsAt IS NULL OR r.EndsAt > CURRENT_TIMESTAMP())\nUNION ALL",
		"'group' AS MemberKind,\n r.GroupId AS MemberId,", "g.Name AS Name,",
		"FROM CrewRecordGroupRoles r\nJOIN CrewGroups g ON g.Id = r.GroupId\nWHERE r.Axis = '' AND r.Resource = 'Voyages'\n AND (r.EndsAt IS NULL OR r.EndsAt > CURRENT_TIMESTAMP())\n`",
	}), nil)
	// The list a role on a voyage is picked from asks the generated grants which roles
	// the dialog's method takes from the caller.
	assertContains(t, "the crew's grants", readIdentityFile(t, "crewidentity/grants.go"), squashEach([]string{
		"case GiveCrewRoleOnVoyage{}.Method():\n\t\t\treturn couldGiveBy(ctx, caller, method, &GiveCrewRoleOnVoyage{Role: string(role)})",
		"case GiveCrewGroupRoleOnVoyage{}.Method():\n\t\t\treturn couldGiveBy(ctx, caller, method, &GiveCrewGroupRoleOnVoyage{Role: string(role)})",
		"is no method of a tenant that gives a role by hand: AssignCrewShipRole, InviteCrewShipUser, GiveCrewShipGroupRole, GiveCrewRoleOnVoyage, GiveCrewGroupRoleOnVoyage\"",
	}), nil)
	// The tenant's list of its roles is asked for the tenant's own methods alone.
	assertContains(t, "the roles of a ship", readIdentityFile(t, "crewidentity/crew_ship_roles.go"), nil, []string{"Voyage"})

	// The members hold a role on a voyage too, and keep no groups: a person's pair, the
	// list of roles for that one method and the view over the people's table alone.
	t.Run("an instance that keeps no groups", func(t *testing.T) {
		writeRoleFile(t, membersAuthPath, strings.ReplaceAll(navigatorOnVoyages, "Navigator", "Lookout"))
		if err := sharingGenerator(t).prepareIdentity(); err != nil {
			t.Fatalf("prepareIdentity() error = %v", err)
		}
		var members []string
		for _, file := range sharingFiles(t) {
			if strings.Contains(file, "members") {
				members = append(members, file)
			}
		}
		want := []string{
			"internal/membersresources/members_voyage_holders.go",
			"membersidentity/give_members_role_on_voyage.go",
			"membersidentity/members_voyage_roles.go",
			"membersidentity/take_members_role_on_voyage.go",
		}
		if !slices.Equal(members, want) {
			t.Errorf("the members' files of a voyage's sharing = %q, want %q", members, want)
		}
		assertContains(t, "the members' roles to give on a voyage", readIdentityFile(t, "membersidentity/members_voyage_roles.go"), squashEach([]string{
			"[]accesstypes.Resource{\n\t\t\tGiveMembersRoleOnVoyage{}.Method(),\n\t\t}",
		}), []string{"GroupRoleOnVoyage"})
		assertContains(t, "who holds what on a members' voyage", readIdentityFile(t, "internal/membersresources/members_voyage_holders.go"),
			squashEach([]string{"FROM MembersRecordUserRoles r"}), []string{"UNION ALL", "MembersRecordGroupRoles"})
	})

	// Nobody's role file holds a role on a voyage: nothing of its sharing is written.
	t.Run("a role file that names no record", func(t *testing.T) {
		writeRoleFile(t, crewAuthPath, deckhandOnNothing)
		writeRoleFile(t, membersAuthPath, deckhandOnNothing)
		if err := sharingGenerator(t).prepareIdentity(); err != nil {
			t.Fatalf("prepareIdentity() error = %v", err)
		}
		if got := sharingFiles(t); len(got) != 0 {
			t.Errorf("the files of a voyage's sharing = %q, want none", got)
		}
		assertContains(t, "the crew's grants", readIdentityFile(t, "crewidentity/grants.go"), nil, []string{"Voyage"})
	})
}

// Test_identityInstance_recordWaysOf pins that a shared record's holders' view and its
// roles to give are under the record, each by its record with no hop (the view's RecordId
// column, the computed list's RecordID field), and that the instance's other lists are not.
func Test_identityInstance_recordWaysOf(t *testing.T) {
	t.Parallel()

	crew := sharingInstance("Crew", "Ship", "Voyage", true)
	attribute := resource.RecordWayAttribute("Voyages", 0)
	attributes, ways := crew.recordWaysOf("CrewVoyageHolder")
	if diff := cmp.Diff([]resource.AttributeData{{Name: attribute, Column: "RecordId", Type: resource.AttributeTypeString}}, attributes); diff != "" {
		t.Errorf("the holders' attributes (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]resource.RecordWayData{{Record: "Voyages", Attribute: attribute}}, ways); diff != "" {
		t.Errorf("the holders' ways (-want +got):\n%s", diff)
	}
	attributes, ways = crew.recordWaysOf("CrewVoyageRole")
	if diff := cmp.Diff([]resource.AttributeData{{Name: attribute, Column: "RecordID", Type: resource.AttributeTypeString}}, attributes); diff != "" {
		t.Errorf("the roles to give's attributes (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]resource.RecordWayData{{Record: "Voyages", Attribute: attribute}}, ways); diff != "" {
		t.Errorf("the roles to give's ways (-want +got):\n%s", diff)
	}
	for _, other := range []string{"CrewShipUser", "CrewShipRole"} {
		if attributes, ways := crew.recordWaysOf(other); len(attributes)+len(ways) != 0 {
			t.Errorf("%s is under a record: %v, %v", other, attributes, ways)
		}
	}
}

// sharingInstance is an instance of the auth that keeps the roles held on the record, in
// an application whose tenant record is tenant, with groups where groups is true: the
// tables every list and method of the record's sharing reads.
func sharingInstance(auth, tenant, record string, groups bool) *identityInstance {
	inst := &identityInstance{
		auth: auth, outlets: []string{"console"}, tenant: &identityTenant{name: tenant, table: tenant + "s", key: "Id"},
		tables:      map[identityTable]bool{identityUsers: true, identityMemberships: true, identityRecordUserRoles: true},
		heldRecords: []identityHeldRecord{{name: record, resource: accesstypes.Resource(record + "s"), keyType: "impulse.UUID"}},
	}
	if groups {
		for _, kind := range append(identityGroupTables(), identityRecordGroupRoles) {
			inst.tables[kind] = true
		}
	}

	return inst
}

// Test_identityOn_sharing pins what the generated API description says of a record's
// sharing, on a tenant's side: the record, its words, its two lists and its methods, the
// group's pair where the auth keeps groups, and nothing where the outlet does not serve
// the record, a list or a person's method.
func Test_identityOn_sharing(t *testing.T) {
	t.Parallel()

	_, byName, err := recordRolesResources(t, recordRolesTables())
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}
	resources := slices.Collect(maps.Values(byName))
	people := []string{"StaffListedUsers", "StaffRealmUsers", "Projects", "StaffProjectHolders", "StaffProjectRoles"}
	methods := []string{"GiveStaffRoleOnProject", "TakeStaffRoleOnProject", "GiveStaffGroupRoleOnProject", "TakeStaffGroupRoleOnProject"}
	tests := []struct {
		name    string
		groups  bool
		served  []string
		methods []string
		want    []tsAPIRecordSharing
	}{
		{
			name: "an auth that keeps groups", groups: true, served: people, methods: methods,
			want: []tsAPIRecordSharing{{
				Record: "Projects", One: "project", Many: "projects", Holders: "StaffProjectHolders", Roles: "StaffProjectRoles",
				Give: "GiveStaffRoleOnProject", Take: "TakeStaffRoleOnProject", GiveGroup: "GiveStaffGroupRoleOnProject", TakeGroup: "TakeStaffGroupRoleOnProject",
			}},
		},
		{
			name: "an auth that keeps none", served: people, methods: methods,
			want: []tsAPIRecordSharing{{
				Record: "Projects", One: "project", Many: "projects", Holders: "StaffProjectHolders", Roles: "StaffProjectRoles",
				Give: "GiveStaffRoleOnProject", Take: "TakeStaffRoleOnProject",
			}},
		},
		{name: "an outlet that does not serve the record", groups: true, served: slices.DeleteFunc(slices.Clone(people), func(name string) bool { return name == "Projects" }), methods: methods},
		{name: "an outlet that does not serve the holders", groups: true, served: people[:3], methods: methods},
		{name: "an outlet that does not serve a person's method", groups: true, served: people, methods: methods[1:]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data := &tsAPIData{}
			for _, name := range tt.served {
				data.Resources = append(data.Resources, &tsAPIResource{Name: name})
			}
			for _, name := range tt.methods {
				data.Methods = append(data.Methods, &tsAPIMethod{Name: name})
			}
			run := &client{resources: resources, identity: []*identityInstance{sharingInstance("Staff", "Realm", "Project", tt.groups)}}
			got := (&typescriptGenerator{client: run, outletName: "console"}).identityOn(data)
			if len(got) != 1 || len(got[0].Sides) != 2 {
				t.Fatalf("identityOn() = %v, want the staff's two sides", got)
			}
			if diff := cmp.Diff(tt.want, got[0].Sides[1].Sharing); diff != "" {
				t.Errorf("the tenant's sharing (-want +got):\n%s", diff)
			}
			if got[0].Sides[0].Sharing != nil {
				t.Errorf("the whole application's side shares %v, want nothing", got[0].Sides[0].Sharing)
			}
		})
	}
}

// Test_typescriptAPITemplate_sharing pins a tenant's sharing as the template writes it, and
// a side that shares nothing writes no sharing.
func Test_typescriptAPITemplate_sharing(t *testing.T) {
	t.Parallel()

	sharing := tsAPIRecordSharing{
		Record: "Projects", One: "project", Many: "projects", Holders: "StaffProjectHolders", Roles: "StaffProjectRoles",
		Give: "GiveStaffRoleOnProject", Take: "TakeStaffRoleOnProject", GiveGroup: "GiveStaffGroupRoleOnProject", TakeGroup: "TakeStaffGroupRoleOnProject",
	}
	data := &tsAPIData{Identity: []*tsAPIIdentity{{
		Auth: "Staff", Label: "Staff", Tenant: "Realm",
		Sides: []*tsAPIIdentitySide{
			{Scope: accesstypes.GlobalPermissionScope, Lists: []identityPart{{Key: "users", Name: "StaffSessionUsers"}}},
			{Scope: accesstypes.DomainPermissionScope, Lists: []identityPart{{Key: "users", Name: "StaffRealmUsers"}}, Sharing: []tsAPIRecordSharing{sharing}},
		},
	}}}
	out, err := (&client{}).generateTemplateOutput("typescriptAPITemplate", typescriptAPITemplate, data)
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	assertContains(t, "typescriptAPITemplate", string(out), []string{
		"          lists: { users: Resources.StaffRealmUsers },\n          methods: {  },\n          sharing: [\n            {\n" +
			"              record: Resources.Projects,\n              one: 'project',\n              many: 'projects',\n" +
			"              holders: Resources.StaffProjectHolders,\n              roles: Resources.StaffProjectRoles,\n" +
			"              give: Methods.GiveStaffRoleOnProject,\n              take: Methods.TakeStaffRoleOnProject,\n" +
			"              giveGroup: Methods.GiveStaffGroupRoleOnProject,\n              takeGroup: Methods.TakeStaffGroupRoleOnProject,\n            },\n          ],\n        },",
		"          lists: { users: Resources.StaffSessionUsers },\n          methods: {  },\n        },",
	}, nil)
}

// Test_placeTargetRoot pins where the frame of an instance's method reads its target row
// from: a record of the application's from the application's resources package, and a
// declaration of the instance's from the instance's own, as every frame reads it.
func Test_placeTargetRoot(t *testing.T) {
	t.Parallel()

	c, byName, err := recordRolesResources(t, recordRolesTables())
	if err != nil {
		t.Fatalf("structsToResources() error = %v", err)
	}
	c.resource = "pkg/resources"
	c.resources = slices.Collect(maps.Values(byName))
	staff := &identityInstance{auth: "Staff", resources: "pkg/identity/internal/staffresources"}
	// The invoices stand for one of the instance's own declarations.
	byName["Invoice"].identity = staff

	for _, tt := range []struct {
		root, want string
	}{{root: "Project", want: "resources"}, {root: "Invoice", want: ""}} {
		method := &rpcMethodInfo{identity: staff, Target: &rpcTarget{RootStruct: tt.root}}
		c.placeTargetRoot(method)
		if method.Target.RootPackage != tt.want {
			t.Errorf("a target %s: RootPackage = %q, want %q", tt.root, method.Target.RootPackage, tt.want)
		}
	}
}

// Test_prepareIdentity_recordPeople pins what the tenant's list of its people reads of the
// roles held on its records, where the role file holds one on a record the application
// marks: the people gain a branch of their own, everyone who holds such a role inside its
// period, which joins no table and which the list of names reads too; each row carries the
// records of the tenant the person holds a role on that has not ended, their own and
// through a group, as a column the list leaves out, and three facts over it, the count, the
// kind where there is one, and the record's name where there is one record and its kind
// declares a name. The list of an auth whose role file holds no role on a record reads
// none of it, and a kind that declares no name gives none.
func Test_prepareIdentity_recordPeople(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	named := strings.Replace(voyageRecordSource, "\t\t// @domain\n\t\tShipID string",
		"\t\t// @name\n\t\tName string `spanner:\"Name\"`\n\t\t// @domain\n\t\tShipID string", 1)
	if named == voyageRecordSource {
		t.Fatal("the fixture's voyage no longer reads as the test expects: no name field added")
	}
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": named})
	writeRoleFile(t, crewAuthPath, navigatorOnVoyages)
	writeRoleFile(t, membersAuthPath, deckhandOnNothing)
	if err := sharingGenerator(t).prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	people := readIdentityFile(t, "internal/crewresources/crew_ship_users.go")
	branch := "UNION DISTINCT SELECT rr.DomainId AS DomainId, rr.UserId AS UserId FROM CrewRecordUserRoles rr WHERE rr.Axis = '' AND " + countingSQL("rr") + " ) t"
	assertContains(t, "the crew's people", people, squashEach([]string{
		"RecordsHeld int64 `spanner:\"RecordsHeld\"` RecordsHeldKind string `spanner:\"RecordsHeldKind\"` RecordHeldName string `spanner:\"RecordHeldName\"`",
		"SELECT * EXCEPT (RecordsHeldOn),",
		"ARRAY( SELECT AS STRUCT hr.Resource AS Resource, hr.RecordId AS RecordId FROM ( SELECT o.Resource AS Resource, o.RecordId AS RecordId FROM CrewRecordUserRoles o" +
			" WHERE o.UserId = u.Id AND o.Axis = '' AND o.DomainId = t.DomainId AND " + standingSQL("o"),
		"UNION ALL SELECT gr.Resource AS Resource, gr.RecordId AS RecordId FROM CrewGroupMembers gm, UNNEST(ARRAY( SELECT AS STRUCT r.Resource AS Resource, r.RecordId AS RecordId" +
			" FROM CrewRecordGroupRoles r WHERE r.GroupId = gm.GroupId AND r.Axis = '' AND r.DomainId = t.DomainId AND " + standingSQL("r") + " )) AS gr" +
			" WHERE gm.UserId = u.Id AND " + standingSQL("gm"),
		"GROUP BY hr.Resource, hr.RecordId ORDER BY hr.Resource, hr.RecordId ) AS RecordsHeldOn,",
		"ARRAY_LENGTH(RecordsHeldOn) AS RecordsHeld,",
		"IF((SELECT COUNT(DISTINCT h.Resource) FROM UNNEST(RecordsHeldOn) h) = 1, RecordsHeldOn[SAFE_OFFSET(0)].Resource, '') AS RecordsHeldKind,",
		"IF(ARRAY_LENGTH(RecordsHeldOn) = 1, CASE RecordsHeldOn[SAFE_OFFSET(0)].Resource WHEN 'Voyages' THEN" +
			" COALESCE((SELECT n.Name FROM Voyages n WHERE n.Id = RecordsHeldOn[SAFE_OFFSET(0)].RecordId), '') ELSE '' END, '') AS RecordHeldName",
	}), nil)
	// The people's query and the names' read the same people.
	if got := strings.Count(people, squashSpace(branch)); got != 2 {
		t.Errorf("the people's branch of a role on a record is in %d queries of crew_ship_users.go, want the people's and the names':\n%s", got, people)
	}
	if facts := people[strings.Index(people, "ARRAY( SELECT AS STRUCT hr.Resource"):strings.Index(people, ") AS RecordsHeldOn,")]; strings.Contains(facts, " JOIN ") {
		t.Errorf("crew_ship_users.go joins a table in RecordsHeldOn, want none:\n%s", facts)
	}

	assertContains(t, "the members' people", readIdentityFile(t, "internal/membersresources/members_ship_users.go"), nil,
		[]string{"RecordsHeld", "RecordUserRoles", "RecordHeldName"})

	t.Run("a kind that declares no name", func(t *testing.T) {
		writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": voyageRecordSource})
		if err := sharingGenerator(t).prepareIdentity(); err != nil {
			t.Fatalf("prepareIdentity() error = %v", err)
		}
		assertContains(t, "the crew's people", readIdentityFile(t, "internal/crewresources/crew_ship_users.go"),
			[]string{"ARRAY_LENGTH(RecordsHeldOn) AS RecordsHeld,", "'' AS RecordHeldName"}, []string{"CASE RecordsHeldOn"})
	})
}
