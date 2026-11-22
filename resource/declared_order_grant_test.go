package resource

// These tests pin what a list's declared order is read under, where the request
// states no sort: a field of the order the person may not read orders their
// list as if no row had a value in it, so the order of their rows tells them
// nothing of it; a field they hold under a condition orders by what they may
// see, a masked cell sorting as a cell with no value, as a sort by it does.
// Nothing is refused, and a request that states its own sort asks nothing of
// the declared order.

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// closedTerm matches a term of the order a person may not read, as the
// statement writes it in the column's place.
var closedTerm = regexp.MustCompile("CASE WHEN FALSE THEN [`\"][A-Za-z]+[`\"] END")

// namesColumn reports whether the statement names the column, quoted or bare,
// outside the alias the cursor selects a copy under.
func namesColumn(sql, column string) bool {
	return regexp.MustCompile(`\b` + column + `\b`).MatchString(sql)
}

// TestDeclaredOrder_fieldTheReaderMayNotRead pins a declared order by a field
// of the list's own on the passes of sector-1.
func TestDeclaredOrder_fieldTheReaderMayNotRead(t *testing.T) {
	t.Parallel()

	byLevel := Paging{Order: []SortField{{Field: "LevelID", Direction: SortAscending}}}

	t.Run("the level id denied: ordered as if no row had a level, and the column read nowhere", func(t *testing.T) {
		t.Parallel()

		want := bothDatabases(
			"ORDER BY CASE WHEN FALSE THEN `LevelId` END ASC, `Id` ASC",
			`ORDER BY CASE WHEN FALSE THEN "LevelId" END ASC, "Id" ASC`,
		)
		for _, target := range []string{"/?columns=id&limit=2", "/?limit=2"} {
			for dbType, order := range want {
				qSet, sql := passesStatement(t, byLevel, target, deniedInSector1("HangarPasses.levelId"), dbType)
				if !strings.Contains(sql, order) {
					t.Errorf("%s %s statement =\n%s\nwant it to order:\n%s", dbType, target, sql, order)
				}
				if cut := closedTerm.ReplaceAllString(sql, ""); namesColumn(cut, "LevelId") {
					t.Errorf("%s %s statement, its closed terms cut out =\n%s\nwant it never to name LevelId", dbType, target, cut)
				}
				if fields := qSet.Fields(); slices.Contains(fields, "LevelID") {
					t.Errorf("%s %s Fields() = %v, want the level id not selected", dbType, target, fields)
				}
			}
		}
	})

	t.Run("the squadron id held under a condition: read as a sort by it is", func(t *testing.T) {
		t.Parallel()

		perms := func() *nameGrantPermissions {
			return &nameGrantPermissions{decisions: map[grantByScope]accesstypes.Decision{
				{scope: inSector1, perm: accesstypes.List, res: "HangarPasses.squadronId"}: conditionalOn("HangarPasses.squadronId", "level = 'visit'"),
			}}
		}
		bySquadron := Paging{Order: []SortField{{Field: "SquadronID", Direction: SortAscending}}}
		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			declared := passesStmt(t, bySquadron, "/?columns=id&limit=2", perms(), dbType)
			sorted := passesStmt(t, Paging{}, "/?columns=id&limit=2&sort=squadronId", perms(), dbType)
			if diff := cmp.Diff(oneLine(sorted.SQL), oneLine(declared.SQL)); diff != "" {
				t.Errorf("%s the declared order's statement mismatch, against the sort's (-sort +declared):\n%s", dbType, diff)
			}
			if diff := cmp.Diff(sorted.Params, declared.Params); diff != "" {
				t.Errorf("%s the declared order's parameters mismatch, against the sort's (-sort +declared):\n%s", dbType, diff)
			}
		}
	})

	t.Run("a sort stated, the level id denied: the declared order's field not asked", func(t *testing.T) {
		t.Parallel()

		want := bothDatabases("ORDER BY `Id` ASC", `ORDER BY "Id" ASC`)
		for dbType, order := range want {
			perms := deniedInSector1("HangarPasses.levelId")
			_, sql := passesStatement(t, byLevel, "/?columns=id&sort=id&limit=2", perms, dbType)
			if calls := perms.askedFor("HangarPasses.levelId"); len(calls) != 0 {
				t.Errorf("%s calls naming HangarPasses.levelId = %+v, want none", dbType, calls)
			}
			if !strings.Contains(sql, order) || namesColumn(sql, "LevelId") {
				t.Errorf("%s statement =\n%s\nwant it to order:\n%s\nand never to name LevelId", dbType, sql, order)
			}
		}
	})

	t.Run("the squadron id denied under an order by the id and its name: both closed, the id asked once", func(t *testing.T) {
		t.Parallel()

		byBoth := Paging{Order: []SortField{{Field: "SquadronID", Direction: SortAscending}, {Field: "SquadronName", Direction: SortAscending}}}
		want := bothDatabases(
			"ORDER BY CASE WHEN FALSE THEN `SquadronId` END ASC, CAST(NULL AS STRING) ASC, `Id` ASC",
			`ORDER BY CASE WHEN FALSE THEN "SquadronId" END ASC, CAST(NULL AS TEXT) ASC, "Id" ASC`,
		)
		for dbType, order := range want {
			perms := deniedInSector1("HangarPasses.squadronId")
			_, sql := passesStatement(t, byBoth, "/?columns=id&limit=2", perms, dbType)
			if !strings.Contains(sql, order) {
				t.Errorf("%s statement =\n%s\nwant it to order:\n%s", dbType, sql, order)
			}
			if strings.Contains(sql, "Squadrons") {
				t.Errorf("%s statement =\n%s\nwant it never to read Squadrons", dbType, sql)
			}
			if calls := perms.askedFor("HangarPasses.squadronId"); len(calls) != 1 {
				t.Errorf("%s calls naming HangarPasses.squadronId = %+v, want one", dbType, calls)
			}
		}
	})
}

// passesStmt decodes a passes request that must pass its checks and renders
// its statement, text and parameters.
func passesStmt(t *testing.T, paging Paging, target string, perms UserPermissions, dbType DBType) *Statement {
	t.Helper()

	qSet, err := decodePasses(t, accesstypes.List, paging, target, perms, dbType)
	if err != nil {
		t.Fatalf("checkPermissions(%s, %s) error = %v", dbType, target, err)
	}
	stmt, err := qSet.stmt(dbType)
	if err != nil {
		t.Fatalf("stmt(%s, %s) error = %v", dbType, target, err)
	}

	return stmt
}

// weightRoles is a person who reaches the tenants and holds every grant in each
// of them, but what the table says of the crates' weight there.
func weightRoles(reach []accesstypes.Domain, weight map[accesstypes.Domain]accesstypes.Decision) func() *acrossPermissions {
	return func() *acrossPermissions {
		grants := make(map[accesstypes.Domain]accesstypes.Decisions, len(reach))
		for _, domain := range reach {
			grants[domain] = everyGrant()
			if decision, named := weight[domain]; named {
				grants[domain] = accesstypes.Decisions{acrossCrates + ".weight": decision}
			}
		}

		return &acrossPermissions{reach: reach, grants: grants}
	}
}

// TestAcrossTenants_declaredOrderHidden pins the crates' declared order by
// weight on a list over north and south that does not ask for the weight: a row
// sorts by its weight only where its tenant shows the person the weight, and
// where no tenant of the set shows it nothing is refused and no row sorts by
// it. A request's own sort by a weight no tenant shows is refused, as before.
func TestAcrossTenants_declaredOrderHidden(t *testing.T) {
	t.Parallel()

	both := []accesstypes.Domain{"north", "south"}
	denied := accesstypes.Denied()
	tests := []struct {
		name  string
		perms func() *acrossPermissions
		want  map[DBType]string
	}{
		{
			name:  "the weight hidden in south: ordered by it where the row is north's",
			perms: weightRoles(both, map[accesstypes.Domain]accesstypes.Decision{"south": denied}),
			want: bothDatabases(
				"ORDER BY CASE WHEN "+spannerInNorth+" THEN `Weight` END ASC",
				`ORDER BY CASE WHEN `+postgresInNorth+` THEN "Weight" END ASC`,
			),
		},
		{
			name:  "the weight hidden in north and south: no refusal, no row ordered by it",
			perms: weightRoles(both, map[accesstypes.Domain]accesstypes.Decision{"north": denied, "south": denied}),
			want: bothDatabases(
				"ORDER BY CASE WHEN FALSE THEN `Weight` END ASC",
				`ORDER BY CASE WHEN FALSE THEN "Weight" END ASC`,
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for dbType, want := range tt.want {
				_, stmt := renderAcross(t, dbType, "/crates?columns=id,label", tt.perms())
				if sql := oneLine(stmt.SQL); !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to order:\n%s", dbType, sql, want)
				}
			}
		})
	}

	t.Run("the weight hidden in north and south, a sort by it asked: refused", func(t *testing.T) {
		t.Parallel()

		perms := weightRoles(both, map[accesstypes.Domain]accesstypes.Decision{"north": denied, "south": denied})
		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			qSet, err := crateDecoder(t).DecodeAcrossTenants(acrossRequest(t, "/crates?columns=id,label&sort=weight"), perms())
			if err != nil {
				t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
			}
			err = qSet.checkPermissions(t.Context(), dbType)
			if !httpio.HasForbidden(err) {
				t.Errorf("%s checkPermissions() error = %v, want Forbidden", dbType, err)
			}
		}
	})
}
