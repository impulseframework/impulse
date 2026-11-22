package resource

// These tests pin the name a list across tenants gives beside an id: a row gives
// the name only where its tenant shows the person the id, whether or not the
// request named the id among its columns; a name whose id no tenant of the set
// shows is refused as the id would be; and a request that names no columns is
// given the name beside every id it returns, under the same rule.

import (
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// The grade's lookup and the tests of a row's tenant, as a statement over north
// and south renders them on each database. north's tenants bind first.
const (
	spannerGradeLookup  = "(SELECT `ca1`.`Description` FROM `CrateGrades` `ca1` WHERE `ca1`.`Id` = `Crates`.`GradeId`)"
	postgresGradeLookup = `(SELECT "ca1"."Description" FROM "CrateGrades" "ca1" WHERE "ca1"."Id" = "Crates"."GradeId")`
	spannerInNorth      = "`Crates`.`Yard` IN UNNEST(@_c1)"
	postgresInNorth     = `"Crates"."Yard" = ANY(@_c1)`
	spannerInSouth      = "`Crates`.`Yard` IN UNNEST(@_c2)"
	postgresInSouth     = `"Crates"."Yard" = ANY(@_c2)`
)

// gradedRoles is a person who reaches north and south, holds every grant in
// north, and in south holds what grade says of the crates' grade id, every
// other grant outright.
func gradedRoles(grade accesstypes.Decision) *acrossPermissions {
	return &acrossPermissions{
		reach: []accesstypes.Domain{"north", "south"},
		grants: map[accesstypes.Domain]accesstypes.Decisions{
			"north": everyGrant(),
			"south": {acrossCrates + ".gradeId": grade},
		},
	}
}

// renderGraded decodes a graded crates list asked across tenants, runs its read
// checks and renders its statement for the database.
func renderGraded(t *testing.T, dbType DBType, target string, perms UserPermissions) (qSet *QuerySet[gradedCrate], sql string) {
	t.Helper()

	return renderGradedIn(t, dbType, Paging{Order: []SortField{{Field: "Label", Direction: SortAscending}}, DefaultLimit: 3}, target, perms)
}

// renderGradedIn is renderGraded under the declared paging.
func renderGradedIn(t *testing.T, dbType DBType, paging Paging, target string, perms UserPermissions) (qSet *QuerySet[gradedCrate], sql string) {
	t.Helper()

	decoder := acrossDecoder[gradedCrate, gradedCrateRequest](t, paging)
	qSet, err := decoder.DecodeAcrossTenants(acrossRequest(t, target), perms)
	if err != nil {
		t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
	}
	if err = qSet.checkPermissions(t.Context(), dbType); err != nil {
		t.Fatalf("QuerySet.checkPermissions(%s) error = %v", dbType, err)
	}
	stmt, err := qSet.stmt(dbType)
	if err != nil {
		t.Fatalf("QuerySet.stmt(%s) error = %v", dbType, err)
	}

	return qSet, oneLine(stmt.SQL)
}

// TestAcrossTenants_names pins the grade's name on a list over north and south
// for both databases: where the statement selects it, and what it is selected
// as.
func TestAcrossTenants_names(t *testing.T) {
	t.Parallel()

	denied, owned := accesstypes.Denied(), conditionalOn(acrossCrates+".gradeId", "owner = subject")
	tests := []struct {
		name         string
		target       string
		perms        *acrossPermissions
		wantSpanner  string
		wantPostgres string
	}{
		{
			name:         "a name asked for beside an id that was not, the id hidden in one tenant: the name where the tenant shows the id",
			target:       "/crates?columns=id,gradeName",
			perms:        gradedRoles(denied),
			wantSpanner:  "CASE WHEN " + spannerInNorth + " THEN " + spannerGradeLookup + " END AS zzNameGradeName",
			wantPostgres: "CASE WHEN " + postgresInNorth + " THEN " + postgresGradeLookup + ` END AS "zzNameGradeName"`,
		},
		{
			name:         "the same, the id shown under a condition in one tenant: the name where the condition holds there",
			target:       "/crates?columns=id,gradeName",
			perms:        gradedRoles(owned),
			wantSpanner:  "CASE WHEN (" + spannerInNorth + " OR (" + spannerInSouth + " AND `Crates`.`Owner` = @subject)) THEN " + spannerGradeLookup + " END AS zzNameGradeName",
			wantPostgres: "CASE WHEN (" + postgresInNorth + " OR (" + postgresInSouth + ` AND "Crates"."Owner" = @subject)) THEN ` + postgresGradeLookup + ` END AS "zzNameGradeName"`,
		},
		{
			name:         "no columns asked, the id shown in every tenant: the name beside it",
			target:       "/crates",
			perms:        gradedRoles(accesstypes.Granted()),
			wantSpanner:  ", " + spannerGradeLookup + " AS zzNameGradeName",
			wantPostgres: ", " + postgresGradeLookup + ` AS "zzNameGradeName"`,
		},
		{
			name:         "no columns asked, the id hidden in one tenant: the name where the tenant shows the id",
			target:       "/crates",
			perms:        gradedRoles(denied),
			wantSpanner:  "CASE WHEN " + spannerInNorth + " THEN " + spannerGradeLookup + " END AS zzNameGradeName",
			wantPostgres: "CASE WHEN " + postgresInNorth + " THEN " + postgresGradeLookup + ` END AS "zzNameGradeName"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for dbType, want := range map[DBType]string{SpannerDBType: tt.wantSpanner, PostgresDBType: tt.wantPostgres} {
				qSet, sql := renderGraded(t, dbType, tt.target, tt.perms)
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to select the name as:\n%s", dbType, sql, want)
				}
				if !slices.ContainsFunc(qSet.Names(), func(n RequestedName) bool { return n.Field == "GradeName" && n.JSONName == "gradeName" }) {
					t.Errorf("%s Names() = %v, want the grade's name", dbType, qSet.Names())
				}
			}
		})
	}

	t.Run("a name whose id no tenant of the set shows is refused as the id would be", func(t *testing.T) {
		t.Parallel()

		perms := gradedRoles(denied)
		perms.grants["north"] = accesstypes.Decisions{acrossCrates + ".gradeId": denied}
		decoder := acrossDecoder[gradedCrate, gradedCrateRequest](t, Paging{Order: []SortField{{Field: "Label", Direction: SortAscending}}, DefaultLimit: 3})
		qSet, err := decoder.DecodeAcrossTenants(acrossRequest(t, "/crates?columns=id,gradeName"), perms)
		if err != nil {
			t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
		}
		plainAnswer(t, qSet.checkPermissions(t.Context(), SpannerDBType), httpio.HasForbidden,
			"You may not list the grade id of crates anywhere.",
			"user (erin) does not have (List) on Crates.gradeId in any tenant the list reads", "erin", "(List)", "Crates")
	})
}

// TestAcrossTenants_namesDeclaredOrder pins a declared order by the grade's name
// on a list over north and south that does not ask for the grade id: a row
// sorts by the name only where its tenant shows the person the id, and where no
// tenant of the set shows it nothing is refused and no row sorts by a name.
func TestAcrossTenants_namesDeclaredOrder(t *testing.T) {
	t.Parallel()

	byGrade := Paging{Order: []SortField{{Field: "GradeName", Direction: SortAscending}}, DefaultLimit: 3}
	denied := accesstypes.Denied()
	nowhere := gradedRoles(denied)
	nowhere.grants["north"] = accesstypes.Decisions{acrossCrates + ".gradeId": denied}
	tests := []struct {
		name         string
		perms        *acrossPermissions
		wantSpanner  string
		wantPostgres string
	}{
		{
			name:         "the grade id hidden in south: ordered by the name where the row is north's",
			perms:        gradedRoles(denied),
			wantSpanner:  "ORDER BY CASE WHEN " + spannerInNorth + " THEN " + spannerGradeLookup + " END ASC",
			wantPostgres: "ORDER BY CASE WHEN " + postgresInNorth + " THEN " + postgresGradeLookup + " END ASC",
		},
		{
			name:         "the grade id hidden in north and south: no refusal, no row ordered by a name",
			perms:        nowhere,
			wantSpanner:  "ORDER BY CASE WHEN FALSE THEN " + spannerGradeLookup + " END ASC",
			wantPostgres: "ORDER BY CASE WHEN FALSE THEN " + postgresGradeLookup + " END ASC",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for dbType, want := range map[DBType]string{SpannerDBType: tt.wantSpanner, PostgresDBType: tt.wantPostgres} {
				_, sql := renderGradedIn(t, dbType, byGrade, "/crates?columns=id", tt.perms)
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to order:\n%s", dbType, sql, want)
				}
			}
		})
	}
}

// shelvedCrate is the graded crates' record with one more id beside a name: the
// shelf a crate stands on, a row of the shelves, which belong to a tenant. The
// generator writes no such name for a record declared @listAcrossTenants; a
// record written by hand may describe one.
type shelvedCrate struct {
	ID      string `spanner:"Id"      postgres:"Id"`
	Yard    string `spanner:"Yard"    postgres:"Yard"`
	Label   string `spanner:"Label"   postgres:"Label"`
	GradeID string `spanner:"GradeId" postgres:"GradeId"`
	MakerID string `spanner:"MakerId" postgres:"MakerId"`
	ShelfID string `spanner:"ShelfId" postgres:"ShelfId"`
}

// Resource names the resource in grants and routes.
func (shelvedCrate) Resource() accesstypes.Resource {
	return acrossCrates
}

// DefaultConfig is the resource's configuration.
func (shelvedCrate) DefaultConfig() Config {
	return Config{}
}

// NameFields describes the grade's name and the maker's as gradedCrate does, and
// the shelf's name, held to the request's tenant by the shelves' Yard.
func (shelvedCrate) NameFields() []NameField {
	return append(gradedCrate{}.NameFields(),
		NameField{Field: "ShelfName", Key: "ShelfID", Resource: acrossShelves, Tag: "name", Table: "Shelves", KeyColumn: "Id", NameColumn: "Name", TenantColumn: "Yard"},
	)
}

// shelvedCrateRequest is shelvedCrate's list request type.
type shelvedCrateRequest struct {
	ID        string  `json:"id"        index:"true"     perm:"-"`
	Yard      string  `json:"-"`
	Label     string  `json:"label"     index:"true"`
	GradeID   string  `json:"gradeId"   index:"true"`
	GradeName *string `json:"gradeName" nameof:"GradeID"`
	MakerID   string  `json:"makerId"   index:"true"`
	MakerName *string `json:"makerName" nameof:"MakerID"`
	ShelfID   string  `json:"shelfId"   index:"true"`
	ShelfName *string `json:"shelfName" nameof:"ShelfID"`
}

// TestAcrossTenants_nameOfATenantsRow pins a record that describes the name of a
// tenant's row: its list across tenants is refused at decode, naming the name
// field and the resource it names a row of, whatever the request asks for; the
// tenant's own list of the same record is not.
func TestAcrossTenants_nameOfATenantsRow(t *testing.T) {
	t.Parallel()

	const want = "resource Crates cannot be listed across tenants with the name field ShelfName: it is the name of a row of Shelves, which belongs to a tenant, and a list across tenants looks up no tenant's names; take it out of the resource's NameFields() (the generator writes none for a resource declared @listAcrossTenants)"
	paging := Paging{Order: []SortField{{Field: "Label", Direction: SortAscending}}, DefaultLimit: 3}
	for _, target := range []string{"/crates", "/crates?columns=id,label"} {
		decoder := acrossDecoder[shelvedCrate, shelvedCrateRequest](t, paging)
		_, err := decoder.DecodeAcrossTenants(acrossRequest(t, target), sameRoles())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("DecodeAcrossTenants(%s) error = %v, want %q", target, err, want)
		}
		if _, err := decoder.Decode(acrossRequest(t, target), sameRoles(), accesstypes.DomainScope("north")); err != nil {
			t.Errorf("Decode(%s) in north error = %v, want none", target, err)
		}
	}
}

// TestAcrossTenants_Permitted pins Permitted on a list across tenants, which no
// one scope gates: it answers false and says to ask TenantsListed, even of a
// person whose grants the zero scope would find; the tenant's own list is
// permitted as it was.
func TestAcrossTenants_Permitted(t *testing.T) {
	t.Parallel()

	const want = "resource.QuerySet.Permitted(): the Crates query is a list across tenants, which no one scope gates; QuerySet.TenantsListed answers the tenants it reads"
	// A person whose grants a check asked in the zero scope finds: the empty
	// tenant stands for the grants held in every tenant.
	everywhere := &acrossPermissions{
		reach:  []accesstypes.Domain{"north"},
		grants: map[accesstypes.Domain]accesstypes.Decisions{"": everyGrant(), "north": everyGrant()},
	}

	across, err := crateDecoder(t).DecodeAcrossTenants(acrossRequest(t, "/crates"), everywhere)
	if err != nil {
		t.Fatalf("DecodeAcrossTenants() error = %v", err)
	}
	permitted, err := across.Permitted(t.Context())
	if permitted || err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Permitted() across tenants = %v, %v; want false, %q", permitted, err, want)
	}

	own, err := crateDecoder(t).Decode(acrossRequest(t, "/crates"), everywhere, accesstypes.DomainScope("north"))
	if err != nil {
		t.Fatalf("Decode() in north error = %v", err)
	}
	if permitted, err := own.Permitted(t.Context()); !permitted || err != nil {
		t.Errorf("Permitted() in north = %v, %v; want true, nil", permitted, err)
	}
}
