package resource

// These tests pin the second grant a name beside an id takes: the List grant on
// the pointed-to resource's name field, asked where that resource's grants are
// held (the request's tenant, or the global scope for a resource of no tenant)
// and only for the names a request brings into play. Without it a request that
// names the name is refused, a request that names no columns is given the id
// alone, and a declared order by it orders as if no row had a name; under a
// condition the name is given where the condition holds for the pointed-to row.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// The grants the names of the passes' fixture take.
const (
	squadronNameGrant = accesstypes.Resource("Squadrons.name")
	crewNameGrant     = accesstypes.Resource("Crews.name")
	makerNameGrant    = accesstypes.Resource("Makers.name")
)

// postgresSquadronLookup and postgresLevelLookup are squadronLookup and
// levelLookup as PostgreSQL is asked for them.
const (
	postgresSquadronLookup = `(SELECT "ca1"."Name" FROM "Squadrons" "ca1" WHERE "ca1"."Id" = "HangarPasses"."SquadronId" AND "ca1"."SectorId" = @domain)`
	postgresLevelLookup    = `(SELECT "ca1"."Description" FROM "PassLevels" "ca1" WHERE "ca1"."Id" = "HangarPasses"."LevelId")`
)

// grantAsked is one Check the stub was asked: the scope, the permission and the
// resources.
type grantAsked struct {
	scope     accesstypes.Scope
	perm      accesstypes.Permission
	resources []accesstypes.Resource
}

// grantByScope says one decision of the stub's: the scope, the permission and
// the resource it is the answer for.
type grantByScope struct {
	scope accesstypes.Scope
	perm  accesstypes.Permission
	res   accesstypes.Resource
}

// nameGrantPermissions answers Check by scope, permission and resource from its
// table, and grants what the table does not name, or, with ownOnly, denies every
// resource but the hangar passes and their fields. It records every call.
type nameGrantPermissions struct {
	renderStubPermissions

	decisions map[grantByScope]accesstypes.Decision
	ownOnly   bool

	mu    sync.Mutex
	asked []grantAsked
}

func (p *nameGrantPermissions) Check(_ context.Context, _ accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	p.mu.Lock()
	p.asked = append(p.asked, grantAsked{scope: scope, perm: perm, resources: slices.Clone(resources)})
	p.mu.Unlock()

	out := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		decision, named := p.decisions[grantByScope{scope: scope, perm: perm, res: res}]
		switch {
		case named:
			out[res] = decision
		case p.ownOnly && res != "HangarPasses" && !strings.HasPrefix(string(res), "HangarPasses."):
			out[res] = accesstypes.Denied()
		default:
			out[res] = accesstypes.Granted()
		}
	}

	return out, nil
}

// askedFor returns the calls that named the resource.
func (p *nameGrantPermissions) askedFor(res accesstypes.Resource) []grantAsked {
	p.mu.Lock()
	defer p.mu.Unlock()

	var calls []grantAsked
	for _, call := range p.asked {
		if slices.Contains(call.resources, res) {
			calls = append(calls, call)
		}
	}

	return calls
}

// inSector1 is the scope the passes' requests are made in.
var inSector1 = accesstypes.DomainScope("sector-1")

// deniedInSector1 is a stub that denies the List grant on each resource in
// sector-1 and grants everything else.
func deniedInSector1(resources ...accesstypes.Resource) *nameGrantPermissions {
	decisions := make(map[grantByScope]accesstypes.Decision, len(resources))
	for _, res := range resources {
		decisions[grantByScope{scope: inSector1, perm: accesstypes.List, res: res}] = accesstypes.Denied()
	}

	return &nameGrantPermissions{decisions: decisions}
}

// decodePasses decodes a request for the passes of sector-1 under the stub and
// runs its read checks.
func decodePasses(t *testing.T, perm accesstypes.Permission, paging Paging, target string, perms UserPermissions, dbType DBType) (*QuerySet[lookupPass], error) {
	t.Helper()

	resSet, err := NewSet[lookupPass, lookupPassRequest](perm)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	decoder, err := NewQueryDecoder[lookupPass, lookupPassRequest](resSet)
	if err != nil {
		t.Fatalf("NewQueryDecoder() error = %v", err)
	}
	decoder.collection = lookupCollection(t)
	if perm == accesstypes.List {
		key, err := NewCursorKey("dGVzdC1jdXJzb3Ita2V5LW1hdGVyaWFsLTMyLWJ5dGVzIQ==")
		if err != nil {
			t.Fatalf("NewCursorKey() error = %v", err)
		}
		decoder = decoder.WithCursorKey(key).WithPaging(paging)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	qSet, err := decoder.Decode(req, perms, inSector1)
	if err != nil {
		t.Fatalf("QueryDecoder.Decode(%s) error = %v", target, err)
	}

	err = qSet.checkPermissions(t.Context(), dbType)

	return qSet, err
}

// passesStatement decodes a passes request that must pass its checks and
// renders its statement.
func passesStatement(t *testing.T, paging Paging, target string, perms UserPermissions, dbType DBType) (qSet *QuerySet[lookupPass], sql string) {
	t.Helper()

	qSet, err := decodePasses(t, accesstypes.List, paging, target, perms, dbType)
	if err != nil {
		t.Fatalf("checkPermissions(%s, %s) error = %v", dbType, target, err)
	}
	stmt, err := qSet.stmt(dbType)
	if err != nil {
		t.Fatalf("stmt(%s, %s) error = %v", dbType, target, err)
	}

	return qSet, oneLine(stmt.SQL)
}

// shows reports whether the query returns the name field.
func shows[Resource Resourcer](qSet *QuerySet[Resource], field accesstypes.Field) bool {
	return slices.ContainsFunc(qSet.Names(), func(n RequestedName) bool { return n.Field == field })
}

// bothDatabases is each database with the text a case expects of it.
func bothDatabases(spanner, postgres string) map[DBType]string {
	return map[DBType]string{SpannerDBType: spanner, PostgresDBType: postgres}
}

// TestNameGrant_shown pins when a name is given and when its grant is asked: a
// name the request names is given with the grant, and the grant is asked with
// List in the scope its resource's grants are held in; a name that takes no
// grant, and a request that names no name, asks none.
func TestNameGrant_shown(t *testing.T) {
	t.Parallel()

	t.Run("a name asked for with its grant: the lookup, the grant asked in the tenant", func(t *testing.T) {
		t.Parallel()

		for dbType, want := range bothDatabases(squadronLookup+" AS zzNameSquadronName", postgresSquadronLookup+` AS "zzNameSquadronName"`) {
			perms := &nameGrantPermissions{}
			_, sql := passesStatement(t, Paging{}, "/?columns=id,squadronName&sort=id&limit=2", perms, dbType)
			if !strings.Contains(sql, want) {
				t.Errorf("%s statement =\n%s\nwant it to select:\n%s", dbType, sql, want)
			}
			calls := perms.askedFor(squadronNameGrant)
			if len(calls) != 1 || calls[0].scope != inSector1 || calls[0].perm != accesstypes.List {
				t.Errorf("%s calls naming %s = %+v, want one, with List in sector-1", dbType, squadronNameGrant, calls)
			}
		}
	})

	t.Run("an enumeration table's name, everything but the passes denied: given, no grant asked of the table", func(t *testing.T) {
		t.Parallel()

		for dbType, want := range bothDatabases(levelLookup+" AS zzNameLevelName", postgresLevelLookup+` AS "zzNameLevelName"`) {
			perms := &nameGrantPermissions{ownOnly: true}
			_, sql := passesStatement(t, Paging{}, "/?columns=id,levelName&sort=id&limit=2", perms, dbType)
			if !strings.Contains(sql, want) {
				t.Errorf("%s statement =\n%s\nwant it to select:\n%s", dbType, sql, want)
			}
			perms.mu.Lock()
			for _, call := range perms.asked {
				if slices.ContainsFunc(call.resources, func(res accesstypes.Resource) bool { return strings.HasPrefix(string(res), "PassLevels") }) {
					t.Errorf("%s asked %+v, want no call naming PassLevels", dbType, call)
				}
			}
			perms.mu.Unlock()
		}
	})

	t.Run("a name of a resource of no tenant asked for: its grant asked in the global scope", func(t *testing.T) {
		t.Parallel()

		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			perms := &nameGrantPermissions{}
			decoder := acrossDecoder[gradedCrate, gradedCrateRequest](t, Paging{Order: []SortField{{Field: "Label", Direction: SortAscending}}, DefaultLimit: 3})
			qSet, err := decoder.Decode(acrossRequest(t, "/crates?columns=id,makerName"), perms, accesstypes.DomainScope("north"))
			if err != nil {
				t.Fatalf("QueryDecoder.Decode() error = %v", err)
			}
			if err := qSet.checkPermissions(t.Context(), dbType); err != nil {
				t.Fatalf("checkPermissions(%s) error = %v", dbType, err)
			}
			calls := perms.askedFor(makerNameGrant)
			if len(calls) != 1 || !calls[0].scope.IsGlobal() || calls[0].perm != accesstypes.List {
				t.Errorf("%s calls naming %s = %+v, want one, with List in the global scope", dbType, makerNameGrant, calls)
			}
			if !shows(qSet, "MakerName") {
				t.Errorf("%s Names() = %v, want the maker's name", dbType, qSet.Names())
			}
		}
	})

	t.Run("no name asked for, sorted by or given: no grant of a name asked", func(t *testing.T) {
		t.Parallel()

		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			perms := &nameGrantPermissions{}
			passesStatement(t, Paging{}, "/?columns=id,levelId&sort=id&limit=2", perms, dbType)
			perms.mu.Lock()
			for _, call := range perms.asked {
				for _, res := range call.resources {
					if res != "HangarPasses" && !strings.HasPrefix(string(res), "HangarPasses.") {
						t.Errorf("%s asked %+v, want no call for a name's grant", dbType, call)
					}
				}
			}
			perms.mu.Unlock()
		}
	})
}

// TestNameGrant_refused pins the refusals of a request that names a name the
// person may not list, in the order the read checks run and in the words of
// any field the person may not read.
func TestNameGrant_refused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		perm     accesstypes.Permission
		target   string
		perms    *nameGrantPermissions
		sentence string
		cause    string
	}{
		{
			name:     "a name asked for as a column",
			perm:     accesstypes.List,
			target:   "/?columns=id,squadronName&sort=id&limit=2",
			perms:    deniedInSector1(squadronNameGrant),
			sentence: "You may not list the squadron name of hangar passes here.",
			cause:    "scope (sector-1), user (u1) cannot read the name squadronName: (List) on Squadrons.name is denied",
		},
		{
			name:     "a name sorted by",
			perm:     accesstypes.List,
			target:   "/?columns=id&sort=squadronName&limit=2",
			perms:    deniedInSector1(squadronNameGrant),
			sentence: "You may not sort or filter hangar passes by the squadron name here.",
			cause:    "scope (sector-1), user (u1) cannot sort or filter on squadronName: (List) on Squadrons.name is denied",
		},
		{
			name:     "a name sorted by whose id is denied too: the id's refusal first",
			perm:     accesstypes.List,
			target:   "/?columns=id&sort=squadronName&limit=2",
			perms:    deniedInSector1("HangarPasses.squadronId", squadronNameGrant),
			sentence: "You may not sort or filter hangar passes by the squadron id here.",
			cause:    "(List) on HangarPasses.squadronId is denied",
		},
		{
			name:     "a name asked for on a row's page: the List grant asked",
			perm:     accesstypes.Read,
			target:   "/?columns=id,squadronName",
			perms:    deniedInSector1(squadronNameGrant),
			sentence: "You may not see the squadron name of hangar passes here.",
			cause:    "cannot read the name squadronName: (List) on Squadrons.name is denied",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
				_, err := decodePasses(t, tt.perm, Paging{}, tt.target, tt.perms, dbType)
				plainAnswer(t, err, httpio.HasForbidden, tt.sentence, tt.cause)
			}
		})
	}
}

// TestNameGrant_leftOut pins what a person without a name's grant is given
// where the request did not name the name: no columns asked, the ids without
// that name; a declared order by it, the rows in the order of the order's other
// terms, the pointed-to table never read.
func TestNameGrant_leftOut(t *testing.T) {
	t.Parallel()

	t.Run("no columns asked: the other names, not this one", func(t *testing.T) {
		t.Parallel()

		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			qSet, sql := passesStatement(t, Paging{}, "/?sort=id&limit=2", deniedInSector1(squadronNameGrant), dbType)
			for _, want := range []string{"zzNameCrewName", "zzNameLevelName"} {
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to select %s", dbType, sql, want)
				}
			}
			if strings.Contains(sql, "zzNameSquadronName") {
				t.Errorf("%s statement =\n%s\nwant it without the squadron's name", dbType, sql)
			}
			if shows(qSet, "SquadronName") {
				t.Errorf("%s Names() = %v, want no squadron name", dbType, qSet.Names())
			}
		}
	})

	t.Run("a declared order by the name: ordered as if no row had one", func(t *testing.T) {
		t.Parallel()

		paging := Paging{Order: []SortField{{Field: "SquadronName", Direction: SortAscending}}}
		for dbType, want := range bothDatabases("ORDER BY CAST(NULL AS STRING) ASC, `Id` ASC", `ORDER BY CAST(NULL AS TEXT) ASC, "Id" ASC`) {
			qSet, sql := passesStatement(t, paging, "/?columns=id&limit=2", deniedInSector1(squadronNameGrant), dbType)
			if !strings.Contains(sql, want) {
				t.Errorf("%s statement =\n%s\nwant it to order:\n%s", dbType, sql, want)
			}
			if strings.Contains(sql, "Squadrons") {
				t.Errorf("%s statement =\n%s\nwant it never to read the squadrons", dbType, sql)
			}
			if shows(qSet, "SquadronName") {
				t.Errorf("%s Names() = %v, want no squadron name", dbType, qSet.Names())
			}
		}
	})
}

// TestNameGrant_conditional pins a name held under a condition: its lookup
// takes the condition, on the pointed-to row, last in its WHERE, wherever the
// statement names it.
func TestNameGrant_conditional(t *testing.T) {
	t.Parallel()

	perms := &nameGrantPermissions{decisions: map[grantByScope]accesstypes.Decision{
		{scope: inSector1, perm: accesstypes.List, res: squadronNameGrant}: conditionalOn(squadronNameGrant, "rank = 1"),
	}}
	lookups := bothDatabases(
		"(SELECT `ca1`.`Name` FROM `Squadrons` `ca1` WHERE `ca1`.`Id` = `HangarPasses`.`SquadronId` AND `ca1`.`SectorId` = @domain AND (`ca1`.`Rank` = @_c1))",
		`(SELECT "ca1"."Name" FROM "Squadrons" "ca1" WHERE "ca1"."Id" = "HangarPasses"."SquadronId" AND "ca1"."SectorId" = @domain AND ("ca1"."Rank" = @_c1))`,
	)
	for dbType, lookup := range lookups {
		qSet, err := decodePasses(t, accesstypes.List, Paging{}, "/?columns=id,squadronName&sort=squadronName&limit=2", perms, dbType)
		if err != nil {
			t.Fatalf("checkPermissions(%s) error = %v", dbType, err)
		}
		stmt, err := qSet.stmt(dbType)
		if err != nil {
			t.Fatalf("stmt(%s) error = %v", dbType, err)
		}
		sql := oneLine(stmt.SQL)
		alias := " AS zzNameSquadronName"
		if dbType == PostgresDBType {
			alias = ` AS "zzNameSquadronName"`
		}
		for _, want := range []string{lookup + alias, "ORDER BY " + lookup + " ASC"} {
			if !strings.Contains(sql, want) {
				t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
			}
		}
		if stmt.Params["_c1"] != int64(1) {
			t.Errorf("%s params = %v, want the rank 1 bound as _c1", dbType, stmt.Params)
		}
	}
}

// makerApart answers the global scope from its own table, granting what the
// table does not name, and every tenant's scope as the crates' stub does.
type makerApart struct {
	*acrossPermissions

	global accesstypes.Decisions
}

func (p *makerApart) Check(ctx context.Context, env accesstypes.Environment, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	if !scope.IsGlobal() {
		return p.acrossPermissions.Check(ctx, env, scope, perm, resources...)
	}
	out := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		if decision, named := p.global[res]; named {
			out[res] = decision
		} else {
			out[res] = accesstypes.Granted()
		}
	}

	return out, nil
}

// TestNameGrant_acrossTenants pins a name's grant on a list over north and
// south, both of which grant everything, where the grant on the maker's name,
// held globally, is denied: named, it is refused in the words of a list across
// tenants; not named, it is left out.
func TestNameGrant_acrossTenants(t *testing.T) {
	t.Parallel()

	makerDenied := func() *makerApart {
		return &makerApart{
			acrossPermissions: &acrossPermissions{
				reach:  []accesstypes.Domain{"north", "south"},
				grants: map[accesstypes.Domain]accesstypes.Decisions{"north": everyGrant(), "south": everyGrant()},
			},
			global: accesstypes.Decisions{makerNameGrant: accesstypes.Denied()},
		}
	}
	decode := func(t *testing.T, target string) *QuerySet[gradedCrate] {
		t.Helper()

		decoder := acrossDecoder[gradedCrate, gradedCrateRequest](t, Paging{Order: []SortField{{Field: "Label", Direction: SortAscending}}, DefaultLimit: 3})
		qSet, err := decoder.DecodeAcrossTenants(acrossRequest(t, target), makerDenied())
		if err != nil {
			t.Fatalf("QueryDecoder.DecodeAcrossTenants() error = %v", err)
		}

		return qSet
	}

	refusals := []struct {
		name     string
		target   string
		sentence string
		cause    string
	}{
		{
			name:     "named in the columns",
			target:   "/crates?columns=id,makerName",
			sentence: "You may not list the maker name of crates anywhere.",
			cause:    "scope (global), user (erin) cannot read the name makerName: (List) on Makers.name is denied",
		},
		{
			name:     "sorted by",
			target:   "/crates?columns=id&sort=makerName",
			sentence: "You may not sort or filter crates by the maker name anywhere.",
			cause:    "scope (global), user (erin) cannot sort or filter on makerName: (List) on Makers.name is denied",
		},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
				plainAnswer(t, decode(t, tt.target).checkPermissions(t.Context(), dbType), httpio.HasForbidden, tt.sentence, tt.cause)
			}
		})
	}

	t.Run("no columns asked: the grade's name, not the maker's", func(t *testing.T) {
		t.Parallel()

		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			qSet := decode(t, "/crates")
			if err := qSet.checkPermissions(t.Context(), dbType); err != nil {
				t.Fatalf("checkPermissions(%s) error = %v", dbType, err)
			}
			stmt, err := qSet.stmt(dbType)
			if err != nil {
				t.Fatalf("stmt(%s) error = %v", dbType, err)
			}
			sql := oneLine(stmt.SQL)
			if !strings.Contains(sql, "zzNameGradeName") || strings.Contains(sql, "zzNameMakerName") {
				t.Errorf("%s statement =\n%s\nwant it to select the grade's name and not the maker's", dbType, sql)
			}
			if !shows(qSet, "GradeName") || shows(qSet, "MakerName") {
				t.Errorf("%s Names() = %v, want the grade's name alone", dbType, qSet.Names())
			}
		}
	})
}

// TestNameGrant_declaredOrderID pins the grant on the id a name of the declared
// order stands beside, where the request states no sort: an id the person may
// not read closes its name, so the list orders as if no row had one; an id held
// under a condition holds the name to it, whether or not the request asks for
// the id. Nothing is refused, and a request that states its own sort asks
// nothing of the declared order's ids.
func TestNameGrant_declaredOrderID(t *testing.T) {
	t.Parallel()

	bySquadron := Paging{Order: []SortField{{Field: "SquadronName", Direction: SortAscending}}}
	byLevel := Paging{Order: []SortField{{Field: "LevelName", Direction: SortAscending}}}

	tests := []struct {
		name      string
		paging    Paging
		perms     *nameGrantPermissions
		unread    string
		wantOrder map[DBType]string
	}{
		{
			name:      "the squadron's id denied: ordered as if no row had a squadron's name",
			paging:    bySquadron,
			perms:     deniedInSector1("HangarPasses.squadronId"),
			unread:    "Squadrons",
			wantOrder: bothDatabases("ORDER BY CAST(NULL AS STRING) ASC, `Id` ASC", `ORDER BY CAST(NULL AS TEXT) ASC, "Id" ASC`),
		},
		{
			name:      "the level's id denied: an enumeration's name closed too",
			paging:    byLevel,
			perms:     deniedInSector1("HangarPasses.levelId"),
			unread:    "PassLevels",
			wantOrder: bothDatabases("ORDER BY CAST(NULL AS STRING) ASC, `Id` ASC", `ORDER BY CAST(NULL AS TEXT) ASC, "Id" ASC`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for dbType, want := range tt.wantOrder {
				_, sql := passesStatement(t, tt.paging, "/?columns=id&limit=2", tt.perms, dbType)
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to order:\n%s", dbType, sql, want)
				}
				if strings.Contains(sql, tt.unread) {
					t.Errorf("%s statement =\n%s\nwant it never to read %s", dbType, sql, tt.unread)
				}
			}
		})
	}

	t.Run("the squadron's id held under a condition on the pass: the name ordered by and carried inside it", func(t *testing.T) {
		t.Parallel()

		perms := &nameGrantPermissions{decisions: map[grantByScope]accesstypes.Decision{
			{scope: inSector1, perm: accesstypes.List, res: "HangarPasses.squadronId"}: conditionalOn("HangarPasses.squadronId", "level = 'visit'"),
		}}
		held := bothDatabases(
			"CASE WHEN `HangarPasses`.`LevelId` = @_c1 THEN "+squadronLookup+" END",
			`CASE WHEN "HangarPasses"."LevelId" = @_c1 THEN `+postgresSquadronLookup+` END`,
		)
		for dbType, lookup := range held {
			qSet, err := decodePasses(t, accesstypes.List, bySquadron, "/?columns=id&limit=2", perms, dbType)
			if err != nil {
				t.Fatalf("checkPermissions(%s) error = %v", dbType, err)
			}
			stmt, err := qSet.stmt(dbType)
			if err != nil {
				t.Fatalf("stmt(%s) error = %v", dbType, err)
			}
			sql := oneLine(stmt.SQL)
			alias := " AS zzNameSquadronName"
			if dbType == PostgresDBType {
				alias = ` AS "zzNameSquadronName"`
			}
			for _, want := range []string{"ORDER BY " + lookup + " ASC", lookup + alias} {
				if !strings.Contains(sql, want) {
					t.Errorf("%s statement =\n%s\nwant it to contain:\n%s", dbType, sql, want)
				}
			}
			if stmt.Params["_c1"] != "visit" {
				t.Errorf("%s params = %v, want the level visit bound as _c1", dbType, stmt.Params)
			}
		}
	})

	t.Run("a sort stated, the squadron's id denied: no refusal, the declared order's id not asked", func(t *testing.T) {
		t.Parallel()

		for _, dbType := range []DBType{SpannerDBType, PostgresDBType} {
			perms := deniedInSector1("HangarPasses.squadronId")
			passesStatement(t, bySquadron, "/?columns=id&sort=id&limit=2", perms, dbType)
			if calls := perms.askedFor("HangarPasses.squadronId"); len(calls) != 0 {
				t.Errorf("%s calls naming HangarPasses.squadronId = %+v, want none", dbType, calls)
			}
		}
	})
}
