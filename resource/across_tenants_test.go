package resource

// These tests pin the rendering of a list across tenants for both databases:
// a set of one tenant byte for byte as the tenant's own route renders it,
// several tenants with the same roles, several with different roles (a row
// predicate, a field shown in one tenant and hidden in another, a mask), a
// subject set following the row, a tenant the session does not reach, a super
// administrator, a record bound through a join path, the search box, and what
// the list refuses. The database tests beside this file run the same shapes.

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// ownRows is the grants of a tenant in which the person's role reads the crates
// they own, every field under that one condition.
func ownRows() accesstypes.Decisions {
	const owned = "owner = subject"

	return accesstypes.Decisions{
		acrossCrates:             conditionalOn(acrossCrates, owned),
		acrossCrates + ".label":  conditionalOn(acrossCrates+".label", owned),
		acrossCrates + ".owner":  conditionalOn(acrossCrates+".owner", owned),
		acrossCrates + ".team":   conditionalOn(acrossCrates+".team", owned),
		acrossCrates + ".weight": conditionalOn(acrossCrates+".weight", owned),
		acrossCrates + ".value":  conditionalOn(acrossCrates+".value", owned),
	}
}

// differingRoles is the person the tests of differing roles run as: every grant
// outright in north; in south the crates they own, and no grant on the value at
// all; in east every crate, the value of the crates of their own crews alone.
// They also hold every grant in west, which the session does not reach.
func differingRoles() *acrossPermissions {
	south := ownRows()
	south[acrossCrates+".value"] = accesstypes.Denied()

	return &acrossPermissions{
		reach: []accesstypes.Domain{"east", "north", "south"},
		grants: map[accesstypes.Domain]accesstypes.Decisions{
			"north": everyGrant(),
			"south": south,
			"east":  {acrossCrates + ".value": conditionalOn(acrossCrates+".value", "team IN subject.crews")},
			"west":  everyGrant(),
		},
	}
}

// The fragments the pinned statements are written from, for each database.
// crews is the membership test `team IN subject.crews` as a list across tenants
// renders it: the crew's yard is compared to the yard of the crate being read.
const (
	spannerCrews  = "CASE WHEN `Crates`.`Team` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `YardCrews` `ca1` WHERE `ca1`.`UserId` = @subject AND `ca1`.`Team` = `Crates`.`Team` AND `ca1`.`Yard` = `Crates`.`Yard`) END"
	postgresCrews = `CASE WHEN "Crates"."Team" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "YardCrews" "ca1" WHERE "ca1"."UserId" = @subject AND "ca1"."Team" = "Crates"."Team" AND "ca1"."Yard" = "Crates"."Yard") END`

	// The value's cells are shown in east under the crews test and in north
	// outright; south, where no grant covers the value, has no branch.
	spannerValueShown  = "((`Crates`.`Yard` IN UNNEST(@_c1) AND " + spannerCrews + ") OR `Crates`.`Yard` IN UNNEST(@_c2))"
	postgresValueShown = `(("Crates"."Yard" = ANY(@_c1) AND ` + postgresCrews + `) OR "Crates"."Yard" = ANY(@_c2))`

	// A row is admitted in east and north outright, and in south where the
	// person owns it.
	spannerRows  = "(`Crates`.`Yard` IN UNNEST(@_c1) OR `Crates`.`Yard` IN UNNEST(@_c2) OR (`Crates`.`Yard` IN UNNEST(@_c3) AND (`Crates`.`Owner` = @subject)))"
	postgresRows = `("Crates"."Yard" = ANY(@_c1) OR "Crates"."Yard" = ANY(@_c2) OR ("Crates"."Yard" = ANY(@_c3) AND ("Crates"."Owner" = @subject)))`
)

// TestAcrossTenants_setOfOne pins that a list across tenants whose set is one
// tenant is that tenant's own list: the statement and its parameters are byte
// for byte what the tenant's route renders for the same request, for the page
// and for the count, on both databases, and each row is stamped with the one
// tenant without the statement selecting it.
func TestAcrossTenants_setOfOne(t *testing.T) {
	t.Parallel()

	sets := map[string]struct {
		perms  func() *acrossPermissions
		narrow string
	}{
		"the session reaches one tenant": {
			perms: func() *acrossPermissions {
				p := differingRoles()
				p.reach = []accesstypes.Domain{"east"}

				return p
			},
		},
		"the request narrows the set to one tenant": {
			perms:  differingRoles,
			narrow: "east",
		},
		"the person holds the List grant in one tenant of the three reached": {
			perms: func() *acrossPermissions {
				p := differingRoles()
				p.grants["north"] = accesstypes.Decisions{acrossCrates: accesstypes.Denied()}
				p.grants["south"] = accesstypes.Decisions{acrossCrates: accesstypes.Denied()}

				return p
			},
		},
	}
	requests := map[string]url.Values{
		"the default page":             {},
		"a count":                      {"count": {"true"}},
		"columns and a sort":           {"columns": {"id,label"}, "sort": {"value:desc"}},
		"a filter on a masked column":  {"filter": {"value:gte:100,weight:lt:50"}},
		"the search box":               {"filter": {`*:search:"ap pl"`}},
		"the whole list with no order": {"limit": {"all"}, "columns": {"id,value"}},
	}

	for setName, set := range sets {
		for requestName, query := range requests {
			for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
				t.Run(setName+"/"+requestName+"/"+string(dbType), func(t *testing.T) {
					t.Parallel()

					own := "/crates"
					if len(query) > 0 {
						own += "?" + query.Encode()
					}
					narrowed := url.Values{}
					for name, values := range query {
						narrowed[name] = values
					}
					if set.narrow != "" {
						narrowed.Set(domainsParam, set.narrow)
					}
					across := "/crates"
					if len(narrowed) > 0 {
						across += "?" + narrowed.Encode()
					}

					qSet, got := renderAcross(t, dbType, across, set.perms())
					want := renderTenant(t, dbType, own, set.perms(), "east")

					if got.SQL != want.SQL {
						t.Errorf("the statement is not the tenant's own:\n got %s\nwant %s", got.SQL, want.SQL)
					}
					if diff := cmp.Diff(want.Params, got.Params); diff != "" {
						t.Errorf("the parameters are not the tenant's own (-want +got):\n%s", diff)
					}
					if got.maskedNamesColumn != want.maskedNamesColumn || got.optimizerVersion != want.optimizerVersion || len(got.cursorColumns) != len(want.cursorColumns) {
						t.Errorf("the statement's envelope is not the tenant's own: masked %q/%q, optimizer %q/%q, cursor columns %d/%d",
							got.maskedNamesColumn, want.maskedNamesColumn, got.optimizerVersion, want.optimizerVersion, len(got.cursorColumns), len(want.cursorColumns))
					}
					if got.tenant != "east" || got.tenantColumn != "" {
						t.Errorf("tenant = %q, tenantColumn = %q, want every row stamped east and no column selected", got.tenant, got.tenantColumn)
					}

					gotCount, err := qSet.countStmt(dbType)
					if err != nil {
						t.Fatalf("QuerySet.countStmt() error = %v", err)
					}
					ownSet, err := crateDecoder(t).Decode(acrossRequest(t, own), set.perms(), accesstypes.DomainScope("east"))
					if err != nil {
						t.Fatalf("QueryDecoder.Decode() error = %v", err)
					}
					if err := ownSet.checkPermissions(t.Context(), dbType); err != nil {
						t.Fatalf("QuerySet.checkPermissions() error = %v", err)
					}
					wantCount, err := ownSet.countStmt(dbType)
					if err != nil {
						t.Fatalf("QuerySet.countStmt() error = %v", err)
					}
					if gotCount.SQL != wantCount.SQL {
						t.Errorf("the count is not the tenant's own:\n got %s\nwant %s", gotCount.SQL, wantCount.SQL)
					}
					if diff := cmp.Diff(wantCount.Params, gotCount.Params); diff != "" {
						t.Errorf("the count's parameters are not the tenant's own (-want +got):\n%s", diff)
					}
				})
			}
		}
	}
}

// TestAcrossTenants_rendering pins the statement of a list over several tenants
// on both databases.
func TestAcrossTenants_rendering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		target       string
		perms        func() *acrossPermissions
		wantSpanner  string
		wantPostgres string
		wantParams   map[string]any
		// wantAsked is every tenant the read checks asked about, when the case
		// is about which they ask.
		wantAsked []accesstypes.Domain
	}{
		{
			name:   "several tenants with the same roles are one group: the tenant's own statement over the set, each row naming its tenant",
			target: "/crates",
			perms: func() *acrossPermissions {
				return &acrossPermissions{
					reach:  []accesstypes.Domain{"east", "north"},
					grants: map[accesstypes.Domain]accesstypes.Decisions{"north": everyGrant(), "east": everyGrant()},
				}
			},
			wantSpanner:  "SELECT Id, Label, Owner, Team, Weight, Value, `Crates`.`Yard` AS zzTenant FROM Crates WHERE (`Crates`.`Yard` IN UNNEST(@domains)) ORDER BY `Weight` ASC, `Id` ASC LIMIT 4",
			wantPostgres: `SELECT "Id", "Label", "Owner", "Team", "Weight", "Value", "Crates"."Yard" AS "zzTenant" FROM Crates WHERE ("Crates"."Yard" = ANY(@domains)) ORDER BY "Weight" ASC, "Id" ASC LIMIT 4`,
			wantParams:   map[string]any{"domains": []string{"east", "north"}},
		},
		{
			name:   "several tenants with the same conditional role are one group: its condition once, with no branch",
			target: "/crates",
			perms: func() *acrossPermissions {
				return &acrossPermissions{
					reach:  []accesstypes.Domain{"east", "north"},
					grants: map[accesstypes.Domain]accesstypes.Decisions{"north": ownRows(), "east": ownRows()},
				}
			},
			wantSpanner:  "SELECT Id, Label, Owner, Team, Weight, Value, `Crates`.`Yard` AS zzTenant FROM Crates WHERE (`Crates`.`Yard` IN UNNEST(@domains)) AND (`Crates`.`Owner` = @subject) ORDER BY `Weight` ASC, `Id` ASC LIMIT 4",
			wantPostgres: `SELECT "Id", "Label", "Owner", "Team", "Weight", "Value", "Crates"."Yard" AS "zzTenant" FROM Crates WHERE ("Crates"."Yard" = ANY(@domains)) AND ("Crates"."Owner" = @subject) ORDER BY "Weight" ASC, "Id" ASC LIMIT 4`,
			wantParams:   map[string]any{"domains": []string{"east", "north"}, "subject": "erin"},
		},
		{
			// The three shapes at once: a row predicate in south alone, the value
			// shown outright in north, under a subject set in east and hidden in
			// south, and the mask naming it where it is not shown. The subject
			// set's yard is the row's yard, never a parameter.
			name:   "several tenants with different roles: a branch per group for the rows, the hidden field and the mask",
			target: "/crates",
			perms:  differingRoles,
			wantSpanner: "SELECT Id, Label, Owner, Team, Weight, " +
				"CASE WHEN " + spannerValueShown + " THEN Value ELSE @_c4 END AS Value, " +
				"IF(" + spannerValueShown + ", ARRAY<STRING>[], ['value']) AS zzMaskedFields, " +
				"`Crates`.`Yard` AS zzTenant FROM Crates " +
				"WHERE (`Crates`.`Yard` IN UNNEST(@domains)) AND " + spannerRows + " " +
				"ORDER BY `Weight` ASC, `Id` ASC LIMIT 4",
			wantPostgres: `SELECT "Id", "Label", "Owner", "Team", "Weight", ` +
				`CASE WHEN ` + postgresValueShown + ` THEN "Value" ELSE @_c4 END AS "Value", ` +
				`ARRAY_REMOVE(ARRAY[CASE WHEN ` + postgresValueShown + ` THEN NULL ELSE 'value' END], NULL) AS "zzMaskedFields", ` +
				`"Crates"."Yard" AS "zzTenant" FROM Crates ` +
				`WHERE ("Crates"."Yard" = ANY(@domains)) AND ` + postgresRows + ` ` +
				`ORDER BY "Weight" ASC, "Id" ASC LIMIT 4`,
			wantParams: map[string]any{
				"domains": []string{"east", "north", "south"}, "subject": "erin",
				"_c1": []string{"east"}, "_c2": []string{"north"}, "_c3": []string{"south"}, "_c4": (*int64)(nil),
			},
			wantAsked: []accesstypes.Domain{"east", "north", "south"},
		},
		{
			// The sort runs over the visible projection: NULL where the value is
			// masked, in the tenant that hides it as in the one whose condition
			// the row does not meet. The cursor's copy is the same projection.
			name:   "a sort on a field hidden in one tenant orders by its visible projection",
			target: "/crates?columns=id,label&sort=value:desc",
			perms:  differingRoles,
			wantSpanner: "SELECT Id, Label, CASE WHEN " + spannerValueShown + " THEN `Value` END AS zzCursorValue, `Crates`.`Yard` AS zzTenant FROM Crates " +
				"WHERE (`Crates`.`Yard` IN UNNEST(@domains)) AND " + spannerRows + " " +
				"ORDER BY CASE WHEN " + spannerValueShown + " THEN `Value` END DESC, `Id` ASC LIMIT 4",
			wantPostgres: `SELECT "Id", "Label", CASE WHEN ` + postgresValueShown + ` THEN "Value" END AS "zzCursorValue", "Crates"."Yard" AS "zzTenant" FROM Crates ` +
				`WHERE ("Crates"."Yard" = ANY(@domains)) AND ` + postgresRows + ` ` +
				`ORDER BY CASE WHEN ` + postgresValueShown + ` THEN "Value" END DESC, "Id" ASC LIMIT 4`,
			wantParams: map[string]any{
				"domains": []string{"east", "north", "south"}, "subject": "erin",
				"_c1": []string{"east"}, "_c2": []string{"north"}, "_c3": []string{"south"},
			},
		},
		{
			name:   "a filter on a field hidden in one tenant tests its visible projection",
			target: "/crates?columns=id,weight&filter=value:gte:100",
			perms:  differingRoles,
			wantSpanner: "SELECT Id, Weight, `Crates`.`Yard` AS zzTenant FROM Crates " +
				"WHERE CASE WHEN " + spannerValueShown + " THEN `Value` END >= @_p1 AND (`Crates`.`Yard` IN UNNEST(@domains)) AND " + spannerRows + " " +
				"ORDER BY `Weight` ASC, `Id` ASC LIMIT 4",
			wantPostgres: `SELECT "Id", "Weight", "Crates"."Yard" AS "zzTenant" FROM Crates ` +
				`WHERE CASE WHEN ` + postgresValueShown + ` THEN "Value" END >= @_p1 AND ("Crates"."Yard" = ANY(@domains)) AND ` + postgresRows + ` ` +
				`ORDER BY "Weight" ASC, "Id" ASC LIMIT 4`,
			wantParams: map[string]any{
				"domains": []string{"east", "north", "south"}, "subject": "erin", "_p1": 100,
				"_c1": []string{"east"}, "_c2": []string{"north"}, "_c3": []string{"south"},
			},
		},
		{
			// West is named by the request and granted to the person, and the
			// session does not reach it: nothing is asked about it and nothing
			// names it. South is reached and narrowed away.
			name:   "a tenant the session does not reach contributes nothing: the filter on the tenant only narrows the reach",
			target: "/crates?columns=id,weight&domains=west,north,east",
			perms:  differingRoles,
			wantSpanner: "SELECT Id, Weight, `Crates`.`Yard` AS zzTenant FROM Crates " +
				"WHERE (`Crates`.`Yard` IN UNNEST(@domains)) ORDER BY `Weight` ASC, `Id` ASC LIMIT 4",
			wantPostgres: `SELECT "Id", "Weight", "Crates"."Yard" AS "zzTenant" FROM Crates ` +
				`WHERE ("Crates"."Yard" = ANY(@domains)) ORDER BY "Weight" ASC, "Id" ASC LIMIT 4`,
			wantParams: map[string]any{"domains": []string{"east", "north"}},
			wantAsked:  []accesstypes.Domain{"east", "north"},
		},
		{
			name:   "a tenant in which the person holds no List grant on the record is not in the set",
			target: "/crates?columns=id,weight",
			perms: func() *acrossPermissions {
				p := differingRoles()
				p.grants["south"] = accesstypes.Decisions{acrossCrates: accesstypes.Denied()}

				return p
			},
			wantSpanner: "SELECT Id, Weight, `Crates`.`Yard` AS zzTenant FROM Crates " +
				"WHERE (`Crates`.`Yard` IN UNNEST(@domains)) ORDER BY `Weight` ASC, `Id` ASC LIMIT 4",
			wantPostgres: `SELECT "Id", "Weight", "Crates"."Yard" AS "zzTenant" FROM Crates ` +
				`WHERE ("Crates"."Yard" = ANY(@domains)) ORDER BY "Weight" ASC, "Id" ASC LIMIT 4`,
			wantParams: map[string]any{"domains": []string{"east", "north"}},
			wantAsked:  []accesstypes.Domain{"east", "north", "south"},
		},
	}

	for _, tt := range tests {
		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			t.Run(tt.name+"/"+string(dbType), func(t *testing.T) {
				t.Parallel()

				perms := tt.perms()
				_, stmt := renderAcross(t, dbType, tt.target, perms)
				want := tt.wantSpanner
				if dbType == PostgresDBType {
					want = tt.wantPostgres
				}
				if got := normalizeSQL(stmt.SQL); got != want {
					t.Errorf("QuerySet.stmt() SQL =\n%s\nwant\n%s", got, want)
				}
				if diff := cmp.Diff(tt.wantParams, stmt.Params); diff != "" {
					t.Errorf("QuerySet.stmt() params mismatch (-want +got):\n%s", diff)
				}
				if stmt.tenantColumn != tenantColumnName || stmt.tenant != "" {
					t.Errorf("tenantColumn = %q, tenant = %q, want each row's tenant read from %s", stmt.tenantColumn, stmt.tenant, tenantColumnName)
				}
				if tt.wantAsked != nil {
					if diff := cmp.Diff(tt.wantAsked, perms.asked); diff != "" {
						t.Errorf("the read checks asked about the wrong tenants (-want +got):\n%s", diff)
					}
				}
			})
		}
	}
}

// TestAcrossTenants_count pins the count of a list over several tenants: the
// same WHERE as the page, the branches included, with no order and no page. The
// masked cell's filler is bound and not read, as the tenant's own count binds it.
func TestAcrossTenants_count(t *testing.T) {
	t.Parallel()

	wants := map[DBType]string{
		SpannerDBType:  "SELECT COUNT(*) FROM Crates WHERE (`Crates`.`Yard` IN UNNEST(@domains)) AND " + spannerRows,
		PostgresDBType: `SELECT COUNT(*) FROM Crates WHERE ("Crates"."Yard" = ANY(@domains)) AND ` + postgresRows,
	}
	for dbType, want := range wants {
		t.Run(string(dbType), func(t *testing.T) {
			t.Parallel()

			qSet, _ := renderAcross(t, dbType, "/crates?count=true", differingRoles())
			stmt, err := qSet.countStmt(dbType)
			if err != nil {
				t.Fatalf("QuerySet.countStmt() error = %v", err)
			}
			if got := normalizeSQL(stmt.SQL); got != want {
				t.Errorf("QuerySet.countStmt() SQL =\n%s\nwant\n%s", got, want)
			}
			wantParams := map[string]any{
				"domains": []string{"east", "north", "south"}, "subject": "erin",
				"_c1": []string{"east"}, "_c2": []string{"north"}, "_c3": []string{"south"}, "_c4": (*int64)(nil),
			}
			if diff := cmp.Diff(wantParams, stmt.Params); diff != "" {
				t.Errorf("QuerySet.countStmt() params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAcrossTenants_joinPath pins a record bound to its tenant through a join
// path: the tenancy predicate and each group's branch are the path's EXISTS
// chain ending at the tenant key in the array, and the row's tenant is the
// tenant the path resolves to.
func TestAcrossTenants_joinPath(t *testing.T) {
	t.Parallel()

	perms := func() *acrossPermissions {
		return &acrossPermissions{
			reach: []accesstypes.Domain{"east", "north", "south"},
			grants: map[accesstypes.Domain]accesstypes.Decisions{
				"north": everyGrant(),
				"south": everyGrant(),
				"east":  {acrossSeals + ".mark": conditionalOn(acrossSeals+".mark", "mark = 'wax'")},
			},
		}
	}

	const (
		spannerShown = "((EXISTS (SELECT 1 FROM `Crates` `ca1` WHERE `ca1`.`Id` = `Seals`.`CrateId` AND `ca1`.`Yard` IN UNNEST(@_c2)) AND `Seals`.`Mark` = @_c1) " +
			"OR EXISTS (SELECT 1 FROM `Crates` `ca2` WHERE `ca2`.`Id` = `Seals`.`CrateId` AND `ca2`.`Yard` IN UNNEST(@_c3)))"
		postgresShown = `((EXISTS (SELECT 1 FROM "Crates" "ca1" WHERE "ca1"."Id" = "Seals"."CrateId" AND "ca1"."Yard" = ANY(@_c2)) AND "Seals"."Mark" = @_c1) ` +
			`OR EXISTS (SELECT 1 FROM "Crates" "ca2" WHERE "ca2"."Id" = "Seals"."CrateId" AND "ca2"."Yard" = ANY(@_c3)))`
	)
	wants := map[DBType]string{
		SpannerDBType: "SELECT Id, CrateId, CASE WHEN " + spannerShown + " THEN Mark ELSE @_c4 END AS Mark, " +
			"IF(" + spannerShown + ", ARRAY<STRING>[], ['mark']) AS zzMaskedFields, " +
			"(SELECT `ca4`.`Yard` FROM `Crates` `ca4` WHERE `ca4`.`Id` = `Seals`.`CrateId`) AS zzTenant FROM Seals " +
			"WHERE (EXISTS (SELECT 1 FROM `Crates` `ca3` WHERE `ca3`.`Id` = `Seals`.`CrateId` AND `ca3`.`Yard` IN UNNEST(@domains))) " +
			"ORDER BY CASE WHEN " + spannerShown + " THEN `Mark` END ASC, `Id` ASC LIMIT 51",
		PostgresDBType: `SELECT "Id", "CrateId", CASE WHEN ` + postgresShown + ` THEN "Mark" ELSE @_c4 END AS "Mark", ` +
			`ARRAY_REMOVE(ARRAY[CASE WHEN ` + postgresShown + ` THEN NULL ELSE 'mark' END], NULL) AS "zzMaskedFields", ` +
			`(SELECT "ca4"."Yard" FROM "Crates" "ca4" WHERE "ca4"."Id" = "Seals"."CrateId") AS "zzTenant" FROM Seals ` +
			`WHERE (EXISTS (SELECT 1 FROM "Crates" "ca3" WHERE "ca3"."Id" = "Seals"."CrateId" AND "ca3"."Yard" = ANY(@domains))) ` +
			`ORDER BY CASE WHEN ` + postgresShown + ` THEN "Mark" END ASC, "Id" ASC LIMIT 51`,
	}
	for dbType, want := range wants {
		t.Run(string(dbType), func(t *testing.T) {
			t.Parallel()

			decoder := acrossDecoder[acrossSeal, acrossSealRequest](t, Paging{Order: []SortField{{Field: "Mark", Direction: SortAscending}}})
			qSet, err := decoder.DecodeAcrossTenants(acrossRequest(t, "/seals"), perms())
			if err != nil {
				t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
			}
			if err := qSet.checkPermissions(t.Context(), dbType); err != nil {
				t.Fatalf("QuerySet.checkPermissions() error = %v", err)
			}
			stmt, err := qSet.stmt(dbType)
			if err != nil {
				t.Fatalf("QuerySet.stmt() error = %v", err)
			}
			if got := normalizeSQL(stmt.SQL); got != want {
				t.Errorf("QuerySet.stmt() SQL =\n%s\nwant\n%s", got, want)
			}
			wantParams := map[string]any{
				"domains": []string{"east", "north", "south"},
				"_c1":     "wax", "_c2": []string{"east"}, "_c3": []string{"north", "south"}, "_c4": "",
			}
			if diff := cmp.Diff(wantParams, stmt.Params); diff != "" {
				t.Errorf("QuerySet.stmt() params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAcrossTenants_searchBox pins the search box of a list over several
// tenants: the crates' own field leaves the box in the tenant that hides it,
// the tags branch is filtered to the set, searched outright in north, under the
// child field's condition in east (its subject set following the tag's own
// yard), and not at all in south, where no grant covers the child field. On
// Spanner the box is a key set; on PostgreSQL it sits inline.
func TestAcrossTenants_searchBox(t *testing.T) {
	t.Parallel()

	perms := func() *acrossPermissions {
		return &acrossPermissions{
			reach: []accesstypes.Domain{"east", "north", "south"},
			grants: map[accesstypes.Domain]accesstypes.Decisions{
				"north": everyGrant(),
				"south": {acrossCrates + ".label": accesstypes.Denied(), "CrateTags.name": accesstypes.Denied()},
				"east":  {"CrateTags.name": conditionalOn("CrateTags.name", "team IN subject.crews")},
			},
		}
	}

	const (
		spannerTagCrews  = "CASE WHEN `CrateTags`.`Team` IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM `YardCrews` `ca1` WHERE `ca1`.`UserId` = @subject AND `ca1`.`Team` = `CrateTags`.`Team` AND `ca1`.`Yard` = `CrateTags`.`Yard`) END"
		postgresTagCrews = `CASE WHEN "CrateTags"."Team" IS NULL THEN NULL ELSE EXISTS (SELECT 1 FROM "YardCrews" "ca1" WHERE "ca1"."UserId" = @subject AND "ca1"."Team" = "CrateTags"."Team" AND "ca1"."Yard" = "CrateTags"."Yard") END`
	)
	wants := map[DBType]string{
		SpannerDBType: "SELECT Id, Weight, `Crates`.`Yard` AS zzTenant FROM (" +
			"SELECT `Crates`.`Id` AS zzSearchKey FROM Crates WHERE (`Crates`.`Yard` IN UNNEST(@domains)) " +
			"AND ((`Crates`.`Yard` IN UNNEST(@_c1) OR `Crates`.`Yard` IN UNNEST(@_c2)) AND SEARCH_SUBSTRING(`Label_Search`, @_p1)) " +
			"UNION DISTINCT " +
			"SELECT `CrateTags`.`CrateId` AS zzSearchKey FROM `CrateTags` WHERE (`CrateTags`.`Yard` IN UNNEST(@domains)) " +
			"AND ((`CrateTags`.`Yard` IN UNNEST(@_c1) AND " + spannerTagCrews + ") OR `CrateTags`.`Yard` IN UNNEST(@_c2)) AND SEARCH_SUBSTRING(`Name_Search`, @_p2)" +
			") AS zzSearchKey1 JOIN@{JOIN_METHOD=APPLY_JOIN} Crates ON `Crates`.`Id` = zzSearchKey1.zzSearchKey " +
			"WHERE (`Crates`.`Yard` IN UNNEST(@domains)) ORDER BY `Weight` ASC, `Id` ASC LIMIT 4",
		PostgresDBType: `SELECT "Id", "Weight", "Crates"."Yard" AS "zzTenant" FROM Crates WHERE (` +
			`(("Crates"."Yard" = ANY(@_c1) OR "Crates"."Yard" = ANY(@_c2)) AND "Label" ILIKE @_p1) ` +
			`OR EXISTS (SELECT 1 FROM "CrateTags" WHERE "CrateTags"."CrateId" = "Crates"."Id" AND "CrateTags"."Yard" = ANY(@domains) ` +
			`AND ((("CrateTags"."Yard" = ANY(@_c1) AND ` + postgresTagCrews + `) OR "CrateTags"."Yard" = ANY(@_c2)) AND "Name" ILIKE @_p2))` +
			`) AND ("Crates"."Yard" = ANY(@domains)) ORDER BY "Weight" ASC, "Id" ASC LIMIT 4`,
	}
	terms := map[DBType]string{SpannerDBType: "ap", PostgresDBType: "%ap%"}
	for dbType, want := range wants {
		t.Run(string(dbType), func(t *testing.T) {
			t.Parallel()

			_, stmt := renderAcross(t, dbType, "/crates?"+url.Values{"columns": {"id,weight"}, "filter": {`*:search:"ap"`}}.Encode(), perms())
			if got := normalizeSQL(stmt.SQL); got != want {
				t.Errorf("QuerySet.stmt() SQL =\n%s\nwant\n%s", got, want)
			}
			wantParams := map[string]any{
				"domains": []string{"east", "north", "south"}, "subject": "erin",
				"_c1": []string{"east"}, "_c2": []string{"north"}, "_p1": terms[dbType], "_p2": terms[dbType],
			}
			if diff := cmp.Diff(wantParams, stmt.Params); diff != "" {
				t.Errorf("QuerySet.stmt() params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAcrossTenants_positionalSearch pins the search box over a positional
// field across tenants. A positional field's match runs on the real column
// wherever a grant covers the field at all, so a tenant's own list conjoins
// nothing to it; across tenants the match is still held to the tenants that
// show the field, and a tenant in which no grant covers it matches nothing by
// it. The field's own condition stays out of the match, as in the tenant's list.
func TestAcrossTenants_positionalSearch(t *testing.T) {
	t.Parallel()

	hiddenInSouth := accesstypes.Decisions{acrossBins + ".code": accesstypes.Denied()}
	tests := []struct {
		name   string
		north  accesstypes.Decisions
		south  accesstypes.Decisions
		wants  map[DBType]string
		params map[string]any
	}{
		{
			name:  "hidden in one tenant, the match is held to the tenant that shows it",
			north: everyGrant(),
			south: hiddenInSouth,
			wants: map[DBType]string{
				SpannerDBType: "SELECT Id, CASE WHEN `Bins`.`Yard` IN UNNEST(@_c1) THEN `Code` END AS zzCursorCode, `Bins`.`Yard` AS zzTenant FROM Bins " +
					"WHERE (`Bins`.`Yard` IN UNNEST(@_c1) AND SEARCH_SUBSTRING(`Code_Search`, @_p1)) AND (`Bins`.`Yard` IN UNNEST(@domains)) " +
					"ORDER BY CASE WHEN `Bins`.`Yard` IN UNNEST(@_c1) THEN `Code` END ASC, `Id` ASC LIMIT 4",
				PostgresDBType: `SELECT "Id", CASE WHEN "Bins"."Yard" = ANY(@_c1) THEN "Code" END AS "zzCursorCode", "Bins"."Yard" AS "zzTenant" FROM Bins ` +
					`WHERE ("Bins"."Yard" = ANY(@_c1) AND "Code" ILIKE @_p1) AND ("Bins"."Yard" = ANY(@domains)) ` +
					`ORDER BY CASE WHEN "Bins"."Yard" = ANY(@_c1) THEN "Code" END ASC, "Id" ASC LIMIT 4`,
			},
			params: map[string]any{"domains": []string{"north", "south"}, "_c1": []string{"north"}},
		},
		{
			name:  "under a condition in one tenant and hidden in the other, the condition stays out of the match",
			north: accesstypes.Decisions{acrossBins + ".code": conditionalOn(acrossBins+".code", "code = 'a'")},
			south: hiddenInSouth,
			wants: map[DBType]string{
				SpannerDBType: "SELECT Id, CASE WHEN `Bins`.`Yard` IN UNNEST(@_c2) THEN `Code` END AS zzCursorCode, `Bins`.`Yard` AS zzTenant FROM Bins " +
					"WHERE (`Bins`.`Yard` IN UNNEST(@_c2) AND SEARCH_SUBSTRING(`Code_Search`, @_p1)) AND (`Bins`.`Yard` IN UNNEST(@domains)) " +
					"ORDER BY CASE WHEN `Bins`.`Yard` IN UNNEST(@_c2) THEN `Code` END ASC, `Id` ASC LIMIT 4",
				PostgresDBType: `SELECT "Id", CASE WHEN "Bins"."Yard" = ANY(@_c2) THEN "Code" END AS "zzCursorCode", "Bins"."Yard" AS "zzTenant" FROM Bins ` +
					`WHERE ("Bins"."Yard" = ANY(@_c2) AND "Code" ILIKE @_p1) AND ("Bins"."Yard" = ANY(@domains)) ` +
					`ORDER BY CASE WHEN "Bins"."Yard" = ANY(@_c2) THEN "Code" END ASC, "Id" ASC LIMIT 4`,
			},
			// The condition's literal is bound for the cell's projection, which
			// this request does not select.
			params: map[string]any{"domains": []string{"north", "south"}, "_c1": "a", "_c2": []string{"north"}},
		},
		{
			name:  "shown in both tenants, the match is bare",
			north: everyGrant(),
			south: everyGrant(),
			wants: map[DBType]string{
				SpannerDBType: "SELECT Id, Code AS zzCursorCode, `Bins`.`Yard` AS zzTenant FROM Bins " +
					"WHERE SEARCH_SUBSTRING(`Code_Search`, @_p1) AND (`Bins`.`Yard` IN UNNEST(@domains)) ORDER BY `Code` ASC, `Id` ASC LIMIT 4",
				PostgresDBType: `SELECT "Id", "Code" AS "zzCursorCode", "Bins"."Yard" AS "zzTenant" FROM Bins ` +
					`WHERE "Code" ILIKE @_p1 AND ("Bins"."Yard" = ANY(@domains)) ORDER BY "Code" ASC, "Id" ASC LIMIT 4`,
			},
			params: map[string]any{"domains": []string{"north", "south"}},
		},
	}
	terms := map[DBType]string{SpannerDBType: "ap", PostgresDBType: "%ap%"}
	for _, tt := range tests {
		for dbType, want := range tt.wants {
			t.Run(tt.name+" on "+string(dbType), func(t *testing.T) {
				t.Parallel()

				perms := &acrossPermissions{
					reach:  []accesstypes.Domain{"north", "south"},
					grants: map[accesstypes.Domain]accesstypes.Decisions{"north": tt.north, "south": tt.south},
				}
				decoder := acrossDecoder[acrossBin, acrossBinRequest](t, Paging{Order: []SortField{{Field: "Code", Direction: SortAscending}}, DefaultLimit: 3})
				target := "/bins?" + url.Values{"columns": {"id"}, "filter": {`*:search:"ap"`}}.Encode()
				qSet, err := decoder.DecodeAcrossTenants(acrossRequest(t, target), perms)
				if err != nil {
					t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
				}
				if err := qSet.checkPermissions(t.Context(), dbType); err != nil {
					t.Fatalf("QuerySet.checkPermissions() error = %v", err)
				}
				stmt, err := qSet.stmt(dbType)
				if err != nil {
					t.Fatalf("QuerySet.stmt() error = %v", err)
				}
				if got := normalizeSQL(stmt.SQL); got != want {
					t.Errorf("QuerySet.stmt() SQL =\n%s\nwant\n%s", got, want)
				}
				wantParams := map[string]any{"_p1": terms[dbType]}
				for name, value := range tt.params {
					wantParams[name] = value
				}
				if diff := cmp.Diff(wantParams, stmt.Params); diff != "" {
					t.Errorf("QuerySet.stmt() params mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}
}

// TestAcrossTenants_superAdministrator pins the set of a super administrator
// against the set of everyone else, through the session's own reach
// (SessionPermissions): each holds the same roles in the same two tenants, one of
// which admits its people through its directory alone. A password session of a
// person who is not a super administrator does not reach that tenant, a grant in
// the global partition or not, so the list reads the other tenant alone, as that
// tenant's own list; the super administrator's session reaches both, and the list
// reads both.
func TestAcrossTenants_superAdministrator(t *testing.T) {
	t.Parallel()

	declarations := func(_ context.Context, domain accesstypes.Domain) (*signin.TenantDeclaration, error) {
		if domain == "sealed" {
			return &signin.TenantDeclaration{Directories: []signin.Directory{{Method: sessioninfo.MethodGoogle, Connection: "acme"}}, DirectoryOnly: true}, nil
		}

		return nil, nil
	}
	roster := func(context.Context) ([]accesstypes.Domain, error) {
		return []accesstypes.Domain{"open", "sealed"}, nil
	}

	tests := []struct {
		name        string
		super       bool
		global      bool
		wantDomains []accesstypes.Domain
	}{
		{name: "a super administrator reads every tenant they hold the grant in", super: true, wantDomains: []accesstypes.Domain{"open", "sealed"}},
		{name: "a person who holds a grant in the global partition reads the tenants their session reaches", global: true, wantDomains: []accesstypes.Domain{"open"}},
		{name: "a person who holds roles in tenants alone reads the tenants their session reaches", wantDomains: []accesstypes.Domain{"open"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := sessionCtx("erin", nil, sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword})
			checker := &stubPermissions{user: "erin", domains: []accesstypes.Domain{"open", "sealed"}, global: tt.global, super: tt.super}
			perms := SessionPermissions(ctx, func(accesstypes.User) *stubPermissions { return checker }, RolePrincipalsUnsupported, roster, declarations, nil)

			req := acrossRequest(t, "/crates?columns=id").WithContext(ctx)
			qSet, err := crateDecoder(t).DecodeAcrossTenants(req, perms)
			if err != nil {
				t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
			}
			if err := qSet.checkPermissions(ctx, SpannerDBType); err != nil {
				t.Fatalf("QuerySet.checkPermissions() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantDomains, qSet.across.domains); diff != "" {
				t.Errorf("the list's tenants mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAcrossTenants_notDeclared pins that only a record declared for it is listed
// across tenants: the decode of a tenant's record the collection does not carry as
// declared is refused, saying what to declare, so neither the list nor a live
// subscription over its tenants is made.
func TestAcrossTenants_notDeclared(t *testing.T) {
	t.Parallel()

	decoder := acrossDecoder[acrossShelf, acrossShelfRequest](t, Paging{Order: []SortField{{Field: "ID", Direction: SortAscending}}, DefaultLimit: 3})
	_, err := decoder.DecodeAcrossTenants(acrossRequest(t, "/shelves"), differingRoles())
	if err == nil {
		t.Fatal("QueryDecoder.DecodeAcrossTenants() error = nil, want the refusal of a record that is not declared for it")
	}
	want := "resource Shelves cannot be listed across tenants: it is not declared for it; declare @listAcrossTenants on its struct and regenerate"
	if !strings.HasSuffix(err.Error(), want) {
		t.Errorf("QueryDecoder.DecodeAcrossTenants() error = %q, want it to end %q", err, want)
	}

	// The same record's own list in one tenant is decoded as ever.
	req := acrossRequest(t, "/yards/north/shelves")
	if _, err := decoder.Decode(req, differingRoles(), accesstypes.DomainScope("north")); err != nil {
		t.Errorf("QueryDecoder.Decode() error = %v, want the tenant's own list decoded", err)
	}
}

// TestAcrossTenants_refusals pins what a list across tenants refuses, and that
// a tenant's own route refuses the filter on the tenant.
func TestAcrossTenants_refusals(t *testing.T) {
	t.Parallel()

	valueNowhere := func() *acrossPermissions {
		p := differingRoles()
		for domain := range p.grants {
			p.grants[domain] = accesstypes.Decisions{acrossCrates + ".value": accesstypes.Denied()}
		}

		return p
	}

	decodeTests := []struct {
		name       string
		target     string
		wantStatus int
		wantErr    string
	}{
		{
			name:       "the capability envelope is refused, saying where it is answered",
			target:     "/crates?capabilities=Update",
			wantStatus: http.StatusBadRequest,
			wantErr:    "capabilities is not answered on a list across tenants: what a person may do with a row is decided in the row's own tenant; ask it on the row's page there",
		},
		{
			name:       "a filter on the tenant with an empty entry is refused",
			target:     "/crates?domains=north,,east",
			wantStatus: http.StatusBadRequest,
			wantErr:    "invalid domains value: it lists tenants by key, separated by commas, and one entry is empty",
		},
	}
	for _, tt := range decodeTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := crateDecoder(t).DecodeAcrossTenants(acrossRequest(t, tt.target), differingRoles())
			assertClientMessage(t, err, tt.wantStatus, tt.wantErr)
		})
	}

	// wantSaid is the sentence the caller is told; wantErr is the error's text, which the
	// server's log prints.
	checkTests := []struct {
		name     string
		target   string
		wantErr  string
		wantSaid string
	}{
		{
			name:     "a column no grant covers in any tenant of the set is refused as the tenant's own list refuses it",
			target:   "/crates?columns=id,value",
			wantErr:  "user (erin) does not have (List) on Crates.value in any tenant the list reads",
			wantSaid: "You may not list the value of crates anywhere.",
		},
		{
			name:     "a sort on a field no grant covers in any tenant of the set is refused",
			target:   "/crates?columns=id&sort=value",
			wantErr:  "user (erin) cannot sort or filter on value: (List) on Crates.value is denied in every tenant the list reads",
			wantSaid: "You may not sort or filter crates by the value anywhere.",
		},
		{
			name:     "a filter on a field no grant covers in any tenant of the set is refused",
			target:   "/crates?columns=id&filter=value:gte:1",
			wantErr:  "user (erin) cannot sort or filter on value: (List) on Crates.value is denied in every tenant the list reads",
			wantSaid: "You may not sort or filter crates by the value anywhere.",
		},
	}
	for _, tt := range checkTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			qSet, err := crateDecoder(t).DecodeAcrossTenants(acrossRequest(t, tt.target), valueNowhere())
			if err != nil {
				t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
			}
			plainAnswer(t, qSet.checkPermissions(t.Context(), SpannerDBType), httpio.HasForbidden, tt.wantSaid, tt.wantErr, "erin", "(List)", "Crates")
		})
	}

	t.Run("a field no grant covers in any tenant leaves the default projection, as it leaves the tenant's own", func(t *testing.T) {
		t.Parallel()

		qSet, _ := renderAcross(t, SpannerDBType, "/crates", valueNowhere())
		if diff := cmp.Diff([]accesstypes.Field{"ID", "Label", "Owner", "Team", "Weight"}, qSet.Fields()); diff != "" {
			t.Errorf("the projection mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a tenant's own route refuses the filter on the tenant", func(t *testing.T) {
		t.Parallel()

		_, err := crateDecoder(t).Decode(acrossRequest(t, "/crates?domains=north"), differingRoles(), accesstypes.DomainScope("north"))
		assertClientMessage(t, err, http.StatusBadRequest, "unknown query parameters: map[domains:[north]]")
	})

	t.Run("a record with no tenant binding is not listed across tenants", func(t *testing.T) {
		t.Parallel()

		resSet, err := NewSet[enforcementResource, enforcementReadRequest](accesstypes.List)
		if err != nil {
			t.Fatalf("NewSet() error = %v", err)
		}
		decoder, err := NewQueryDecoder[enforcementResource, enforcementReadRequest](resSet)
		if err != nil {
			t.Fatalf("NewQueryDecoder() error = %v", err)
		}
		decoder.collection = acrossCollection(t)
		_, err = decoder.DecodeAcrossTenants(acrossRequest(t, "/rows?limit=all"), differingRoles())
		if err == nil || !strings.Contains(err.Error(), "cannot be listed across tenants: it carries no tenant binding (@domain)") {
			t.Errorf("DecodeAcrossTenants() error = %v, want the missing tenant binding named", err)
		}
	})
}

// assertClientMessage holds an error to a client message of the status.
func assertClientMessage(t *testing.T, err error, wantStatus int, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("error = nil, want %d %q", wantStatus, want)
	}
	is := map[int]func(error) bool{http.StatusBadRequest: httpio.HasBadRequest, http.StatusForbidden: httpio.HasForbidden}[wantStatus]
	if !is(err) {
		t.Errorf("error = %v, want a %d", err, wantStatus)
	}
	if got := httpio.Message(err); got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// TestAcrossTenants_noTenant pins the list of a person who reaches no tenant
// they may list the record in: no statement runs, no row comes, the count is
// zero, and nothing is refused, so the answer never says which tenants exist.
func TestAcrossTenants_noTenant(t *testing.T) {
	t.Parallel()

	sets := map[string]struct {
		target string
		perms  *acrossPermissions
	}{
		"the session reaches no tenant": {
			target: "/crates?count=true",
			perms:  &acrossPermissions{grants: map[accesstypes.Domain]accesstypes.Decisions{"north": everyGrant()}},
		},
		"the request names only a tenant the session does not reach": {
			target: "/crates?count=true&domains=west",
			perms:  differingRoles(),
		},
		"the person holds the List grant in no tenant they reach": {
			target: "/crates?count=true",
			perms: &acrossPermissions{
				reach:  []accesstypes.Domain{"north"},
				grants: map[accesstypes.Domain]accesstypes.Decisions{"north": {acrossCrates: accesstypes.Denied()}},
			},
		},
	}
	for name, set := range sets {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			qSet, stmt := renderAcross(t, SpannerDBType, set.target, set.perms)
			if stmt != nil {
				t.Fatalf("the list rendered a statement: %s", stmt.SQL)
			}
			// A client with no database behind it: a query would panic, and none runs.
			client := NewSpannerClient(nil)
			total, err := qSet.Count(t.Context(), client)
			if err != nil || total != 0 {
				t.Errorf("QuerySet.Count() = %d, %v, want 0 and no query", total, err)
			}
			for row, err := range qSet.List(t.Context(), client) {
				t.Errorf("QuerySet.List() yielded %v, %v, want nothing", row, err)
			}
		})
	}
}

// TestAcrossTenants_cursor pins what a cursor of a list across tenants is bound
// to: the list across tenants and its filter on the tenant. The tenant's own
// route does not open it, the list across tenants does not open a tenant's, a
// changed filter on the tenant ends the walk, and a change in what the session
// reaches does not.
func TestAcrossTenants_cursor(t *testing.T) {
	t.Parallel()

	decoder := crateDecoder(t)
	seal := func(t *testing.T, qSet *QuerySet[acrossCrate]) string {
		t.Helper()

		weight, id := "20", "s1"
		token, err := qSet.cursorKey.seal(cursor{Query: qSet.queryHash(qSet.scope), Direction: pageNext, Keys: []*string{&weight, &id}})
		if err != nil {
			t.Fatalf("CursorKey.seal() error = %v", err)
		}

		return token
	}

	acrossSet, err := decoder.DecodeAcrossTenants(acrossRequest(t, "/crates?domains=east,north"), differingRoles())
	if err != nil {
		t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
	}
	across := seal(t, acrossSet)

	tenantSet, err := decoder.Decode(acrossRequest(t, "/crates"), differingRoles(), accesstypes.DomainScope("east"))
	if err != nil {
		t.Fatalf("QueryDecoder.Decode() error = %v", err)
	}
	tenant := seal(t, tenantSet)

	t.Run("the list across tenants opens its own cursor, the tenants named in any order", func(t *testing.T) {
		t.Parallel()

		if _, err := decoder.DecodeAcrossTenants(acrossRequest(t, "/crates?domains=north,east&cursor="+across), differingRoles()); err != nil {
			t.Errorf("DecodeAcrossTenants() error = %v", err)
		}
	})
	t.Run("the walk goes on when the session's reach changes", func(t *testing.T) {
		t.Parallel()

		perms := differingRoles()
		perms.reach = []accesstypes.Domain{"north"}
		if _, err := decoder.DecodeAcrossTenants(acrossRequest(t, "/crates?domains=east,north&cursor="+across), perms); err != nil {
			t.Errorf("DecodeAcrossTenants() error = %v", err)
		}
	})
	t.Run("a changed filter on the tenant ends the walk", func(t *testing.T) {
		t.Parallel()

		for _, target := range []string{"/crates?domains=east&cursor=" + across, "/crates?cursor=" + across} {
			_, err := decoder.DecodeAcrossTenants(acrossRequest(t, target), differingRoles())
			assertClientMessage(t, err, http.StatusBadRequest, "invalid cursor: restart the walk from the first page")
		}
	})
	t.Run("a tenant's own route does not open it, even the one tenant the list is narrowed to", func(t *testing.T) {
		t.Parallel()

		narrowed, err := decoder.DecodeAcrossTenants(acrossRequest(t, "/crates?domains=east"), differingRoles())
		if err != nil {
			t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
		}
		if err := narrowed.checkPermissions(t.Context(), SpannerDBType); err != nil {
			t.Fatalf("QuerySet.checkPermissions() error = %v", err)
		}
		for _, token := range []string{across, seal(t, narrowed)} {
			_, err := decoder.Decode(acrossRequest(t, "/crates?cursor="+token), differingRoles(), accesstypes.DomainScope("east"))
			assertClientMessage(t, err, http.StatusBadRequest, "invalid cursor: restart the walk from the first page")
		}
	})
	t.Run("the list across tenants does not open a tenant's own cursor", func(t *testing.T) {
		t.Parallel()

		_, err := decoder.DecodeAcrossTenants(acrossRequest(t, "/crates?domains=east&cursor="+tenant), differingRoles())
		assertClientMessage(t, err, http.StatusBadRequest, "invalid cursor: restart the walk from the first page")
	})
	t.Run("nor the cursor of a tenant whose key spells the list's own filter on the tenant", func(t *testing.T) {
		t.Parallel()

		for key, target := range map[accesstypes.Domain]string{"every tenant": "/crates?cursor=", "only:east": "/crates?domains=east&cursor="} {
			named, err := decoder.Decode(acrossRequest(t, "/crates"), differingRoles(), accesstypes.DomainScope(key))
			if err != nil {
				t.Fatalf("QueryDecoder.Decode() error = %v", err)
			}
			_, err = decoder.DecodeAcrossTenants(acrossRequest(t, target+seal(t, named)), differingRoles())
			assertClientMessage(t, err, http.StatusBadRequest, "invalid cursor: restart the walk from the first page")
		}
	})
}

// TestScanEnvelopeRow_tenant pins how a row of a list across tenants learns its
// tenant: from the reserved column where the statement selects it, and from the
// statement's one tenant where the set came to one.
func TestScanEnvelopeRow_tenant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		columns []string
		values  []any
		stmt    *Statement
		want    accesstypes.Domain
	}{
		{
			name:    "the reserved column names the row's tenant",
			columns: []string{"Id", "Label", tenantColumnName},
			values:  []any{"n1", "apple", "north"},
			stmt:    &Statement{tenantColumn: tenantColumnName},
			want:    "north",
		},
		{
			name:    "a set of one stamps its tenant on a row the statement selected none for",
			columns: []string{"Id", "Label", maskedNamesColumnName},
			values:  []any{"n1", "apple", []string{}},
			stmt:    &Statement{tenant: "east", maskedNamesColumn: maskedNamesColumnName},
			want:    "east",
		},
		{
			name:    "a tenant's own row carries none",
			columns: []string{"Id", "Label", maskedNamesColumnName},
			values:  []any{"n1", "apple", []string{}},
			stmt:    &Statement{maskedNamesColumn: maskedNamesColumnName},
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spannerRow, err := spanner.NewRow(tt.columns, tt.values)
			if err != nil {
				t.Fatalf("spanner.NewRow() error = %v", err)
			}
			if !envelopeScan(tt.stmt) {
				t.Fatal("envelopeScan() = false, want the envelope scan")
			}
			row, err := scanEnvelopeRow[acrossCrate](spannerRow, tt.stmt)
			if err != nil {
				t.Fatalf("scanEnvelopeRow() error = %v", err)
			}
			if row.Tenant() != tt.want {
				t.Errorf("Row.Tenant() = %q, want %q", row.Tenant(), tt.want)
			}
			if row.Data.ID != "n1" || row.Data.Label != "apple" {
				t.Errorf("Row.Data = %+v, want the row's own columns", row.Data)
			}
		})
	}
}
