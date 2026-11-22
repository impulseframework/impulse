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

	decoder := acrossDecoder[gradedCrate, gradedCrateRequest](t, Paging{Order: []SortField{{Field: "Label", Direction: SortAscending}}, DefaultLimit: 3})
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
