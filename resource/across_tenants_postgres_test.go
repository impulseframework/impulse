package resource

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// acrossPostgres makes a PostgreSQL database with the list across tenants'
// fixture: the tables of testdata/acrosstenants/schema in PostgreSQL's own
// types, and the fixture's rows.
func acrossPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()
	container, err := initiator.NewPostgresContainer(ctx, "16")
	if err != nil {
		t.Fatalf("initiator.NewPostgresContainer() error = %v", err)
	}
	t.Cleanup(func() {
		container.Close()
		if err := container.Terminate(context.Background()); err != nil {
			t.Error(err)
		}
	})
	db, err := container.CreateDatabase(ctx, "across-tenants")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)

	for _, ddl := range []string{
		`CREATE TABLE "Crates" ("Id" text PRIMARY KEY, "Yard" text NOT NULL, "Label" text NOT NULL, "Owner" text NOT NULL, "Team" text, "Weight" bigint NOT NULL, "Value" bigint)`,
		`CREATE TABLE "Seals" ("Id" text PRIMARY KEY, "CrateId" text NOT NULL, "Mark" text NOT NULL)`,
		`CREATE TABLE "YardCrews" ("Id" text PRIMARY KEY, "Yard" text NOT NULL, "UserId" text NOT NULL, "Team" text NOT NULL)`,
		`CREATE TABLE "CrateTags" ("Id" text PRIMARY KEY, "CrateId" text NOT NULL, "Yard" text NOT NULL, "Name" text NOT NULL, "Team" text)`,
	} {
		if _, err := db.Exec(ctx, ddl); err != nil {
			t.Fatalf("%s: %v", ddl, err)
		}
	}
	for _, row := range acrossCrateRows {
		if _, err := db.Exec(ctx, `INSERT INTO "Crates" ("Id", "Yard", "Label", "Owner", "Team", "Weight", "Value") VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			row.ID, row.Yard, row.Label, row.Owner, row.Team, row.Weight, row.Value); err != nil {
			t.Fatalf("inserting crate %s: %v", row.ID, err)
		}
	}
	for _, row := range acrossSealRows {
		if _, err := db.Exec(ctx, `INSERT INTO "Seals" ("Id", "CrateId", "Mark") VALUES ($1, $2, $3)`, row.ID, row.CrateID, row.Mark); err != nil {
			t.Fatalf("inserting seal %s: %v", row.ID, err)
		}
	}
	for _, row := range acrossCrewRows {
		if _, err := db.Exec(ctx, `INSERT INTO "YardCrews" ("Id", "Yard", "UserId", "Team") VALUES ($1, $2, $3, $4)`, row[0], row[1], row[2], row[3]); err != nil {
			t.Fatalf("inserting crew %s: %v", row[0], err)
		}
	}
	for _, row := range acrossTagRows {
		var team *string
		if row[4] != "" {
			team = &row[4]
		}
		if _, err := db.Exec(ctx, `INSERT INTO "CrateTags" ("Id", "CrateId", "Yard", "Name", "Team") VALUES ($1, $2, $3, $4, $5)`, row[0], row[1], row[2], row[3], team); err != nil {
			t.Fatalf("inserting tag %s: %v", row[0], err)
		}
	}

	return db.Pool
}

// postgresRow is one row a statement yielded on PostgreSQL, by output column.
type postgresRow map[string]any

// masked reports whether the row's mask list names the cell.
func (r postgresRow) masked(jsonName string) bool {
	names, _ := r[maskedNamesColumnName].([]any)

	return slices.Contains(names, any(jsonName))
}

// postgresSQL is a rendered list statement as PostgreSQL can run it. The
// rendering names the listed table bare after FROM and quoted everywhere else,
// which PostgreSQL reads as two different names, and the package has no
// PostgreSQL reader that runs a list yet; quoting the one name is the only
// change made to the statement.
func postgresSQL(t *testing.T, stmt *Statement, res accesstypes.Resource) string {
	t.Helper()

	bare := "FROM " + string(res) + "\n"
	if strings.Count(stmt.SQL, bare) != 1 {
		t.Fatalf("the statement names its table after FROM %d times, want once:\n%s", strings.Count(stmt.SQL, bare), stmt.SQL)
	}

	return strings.Replace(stmt.SQL, bare, `FROM "`+string(res)+`"`+"\n", 1)
}

// runOnPostgres runs a rendered statement and returns its rows by output column.
func runOnPostgres(t *testing.T, db *pgxpool.Pool, stmt *Statement, res accesstypes.Resource) []postgresRow {
	t.Helper()

	sql := postgresSQL(t, stmt, res)
	result, err := db.Query(t.Context(), sql, pgx.NamedArgs(stmt.Params))
	if err != nil {
		t.Fatalf("running %s: %v", sql, err)
	}
	defer result.Close()

	var rows []postgresRow
	for result.Next() {
		values, err := result.Values()
		if err != nil {
			t.Fatalf("reading a row of %s: %v", stmt.SQL, err)
		}
		row := make(postgresRow, len(values))
		for i, field := range result.FieldDescriptions() {
			row[field.Name] = values[i]
		}
		rows = append(rows, row)
	}
	if err := result.Err(); err != nil {
		t.Fatalf("reading the rows of %s: %v", stmt.SQL, err)
	}

	return rows
}

// boundaryOnPostgres spells a row's place in the list's order the way the
// cursor carries it (QuerySet.boundaryKeys): the cursor's own copy of a sort
// key where the statement selected one, NULL where the cell is masked, the
// cell otherwise.
func boundaryOnPostgres[Resource Resourcer](t *testing.T, qSet *QuerySet[Resource], row postgresRow) []*string {
	t.Helper()

	dbFields := qSet.rMeta.dbFieldMap(PostgresDBType)
	var keys []*string
	for _, sf := range qSet.Order() {
		column := dbFields[accesstypes.Field(sf.Field)].ColumnName
		value, copied := row[cursorColumnPrefix+column]
		if !copied {
			if row.masked(qSet.jsonName(accesstypes.Field(sf.Field))) {
				keys = append(keys, nil)

				continue
			}
			value = row[column]
		}
		switch v := value.(type) {
		case nil:
			keys = append(keys, nil)
		case string:
			keys = append(keys, &v)
		case int64:
			keys = append(keys, new(strconv.FormatInt(v, 10)))
		default:
			t.Fatalf("sort key %s arrived as %T", sf.Field, value)
		}
	}

	return keys
}

// walkOnPostgres renders and runs every page of a list across tenants on
// PostgreSQL, forward: each page's statement positioned after the last row of
// the page before, as the cursor positions it. It returns the pages' rows by
// output column, the tenant each row is in, and the count.
func walkOnPostgres[Resource Resourcer, Request any](
	t *testing.T, db *pgxpool.Pool, decoder *QueryDecoder[Resource, Request], target string, perms func() *acrossPermissions,
) (pages [][]postgresRow, fields []accesstypes.Field, total int64) {
	t.Helper()

	var position *cursor
	for {
		qSet, err := decoder.DecodeAcrossTenants(acrossRequest(t, target), perms())
		if err != nil {
			t.Fatalf("QueryDecoder.DecodeAcrossTenants(%s) error = %v", target, err)
		}
		qSet.cursor = position
		if err := qSet.checkPermissions(t.Context(), PostgresDBType); err != nil {
			t.Fatalf("QuerySet.checkPermissions(%s) error = %v", target, err)
		}
		fields = qSet.Fields()
		if qSet.readsNoTenant() {
			return pages, fields, 0
		}
		if position == nil {
			count, err := qSet.countStmt(PostgresDBType)
			if err != nil {
				t.Fatalf("QuerySet.countStmt(%s) error = %v", target, err)
			}
			countSQL := postgresSQL(t, count, qSet.Resource())
			if err := db.QueryRow(t.Context(), countSQL, pgx.NamedArgs(count.Params)).Scan(&total); err != nil {
				t.Fatalf("running %s: %v", countSQL, err)
			}
		}

		stmt, err := qSet.stmt(PostgresDBType)
		if err != nil {
			t.Fatalf("QuerySet.stmt(%s) error = %v", target, err)
		}
		rows := runOnPostgres(t, db, stmt, qSet.Resource())
		for _, row := range rows {
			if stmt.tenantColumn == "" {
				// A set of one: the statement is the tenant's own, and the reader
				// stamps its one tenant.
				row[tenantColumnName] = string(stmt.tenant)
			}
		}
		size := int(qSet.page.size)
		more := len(rows) > size
		if more {
			rows = rows[:size]
		}
		pages = append(pages, rows)
		if !more {
			return pages, fields, total
		}
		position = &cursor{Direction: pageNext, Keys: boundaryOnPostgres(t, qSet, rows[len(rows)-1])}
	}
}

// TestAcrossTenants_onPostgres runs the PostgreSQL rendering of the list across
// tenants on a real PostgreSQL: every page of each walk the Spanner emulator
// runs, forward, each positioned by the cursor predicate after the page before,
// with the count. The package has no PostgreSQL reader, so the statements run
// through the driver and the rows are read by output column.
func TestAcrossTenants_onPostgres(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("requires a PostgreSQL container")
	}
	db := acrossPostgres(t)

	for _, walk := range acrossWalks() {
		t.Run(walk.name, func(t *testing.T) {
			t.Parallel()

			pages, fields, total := walkOnPostgres(t, db, crateDecoder(t), walk.target, walk.perms)
			if total != walk.total {
				t.Errorf("the count = %d, want %d", total, walk.total)
			}
			asked := slices.Contains(fields, accesstypes.Field("Value"))
			var got [][]acrossAnswer
			for _, page := range pages {
				var answers []acrossAnswer
				for _, row := range page {
					answer := acrossAnswer{ID: fmt.Sprint(row["Id"]), Tenant: accesstypes.Domain(fmt.Sprint(row[tenantColumnName]))}
					switch {
					case !asked:
					case row.masked("value"):
						answer.Value = "masked"
					case row["Value"] == nil:
						answer.Value = "null"
					default:
						answer.Value = fmt.Sprint(row["Value"])
					}
					answers = append(answers, answer)
				}
				got = append(got, answers)
			}
			want := walk.pages
			if walk.postgresPages != nil {
				want = walk.postgresPages
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("the walk mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("a record bound through a join path names the tenant the path resolves to", func(t *testing.T) {
		t.Parallel()

		decoder := acrossDecoder[acrossSeal, acrossSealRequest](t, Paging{Order: []SortField{{Field: "Mark", Direction: SortAscending}}, DefaultLimit: 3})
		pages, _, total := walkOnPostgres(t, db, decoder, "/seals", sealRoles)
		if total != 4 {
			t.Errorf("the count = %d, want 4", total)
		}
		var got [][]acrossAnswer
		for _, page := range pages {
			var answers []acrossAnswer
			for _, row := range page {
				answer := acrossAnswer{ID: fmt.Sprint(row["Id"]), Tenant: accesstypes.Domain(fmt.Sprint(row[tenantColumnName])), Value: fmt.Sprint(row["Mark"])}
				if row.masked("mark") {
					answer.Value = "masked"
				}
				answers = append(answers, answer)
			}
			got = append(got, answers)
		}
		// k4's mark is tape, which east does not show: NULL to the sort, last
		// ascending on PostgreSQL.
		want := [][]acrossAnswer{
			{{"k2", "south", "tape"}, {"k1", "north", "wax"}, {"k3", "east", "wax"}},
			{{"k4", "east", "masked"}},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("the seals mismatch (-want +got):\n%s", diff)
		}
	})
}
