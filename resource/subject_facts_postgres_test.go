package resource

import (
	"context"
	"slices"
	"testing"

	initiator "github.com/impulseframework/impulse/db"
	"github.com/jackc/pgx/v5"
)

// TestSubjectFactStatement_onPostgres runs the PostgreSQL rendering of the
// statements that read the facts about the caller on a real PostgreSQL, over the
// semantic fixture's anchors. The semantic differential proves the reading on the
// Spanner emulator over generated worlds; this is the same statements' text in the
// other dialect, over the shapes an anchor takes: a value, a value with none, a row
// in another partition, another caller's row, and a dotted path that reaches a row,
// reaches a row with no value, and reaches no row.
func TestSubjectFactStatement_onPostgres(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("requires a PostgreSQL container")
	}
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
	db, err := container.CreateDatabase(ctx, "subject-facts")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)

	for _, ddl := range []string{
		`CREATE TABLE "Hubs" ("Id" text PRIMARY KEY, "Region" text)`,
		`CREATE TABLE "Memberships" ("Id" text PRIMARY KEY, "UserId" text, "Depot" text, "Team" text, "Tier" bigint, "HubId" text)`,
		`CREATE TABLE "Profiles" ("UserId" text PRIMARY KEY, "Nickname" text, "Quota" bigint, "HomeHubId" text)`,
		`INSERT INTO "Hubs" ("Id", "Region") VALUES ('h-west', 'west'), ('h-unnamed', NULL)`,
		`INSERT INTO "Memberships" ("Id", "UserId", "Depot", "Team", "Tier", "HubId") VALUES
			('m1', 'erin', 'north', 'alpha', 1, 'h-west'),
			('m2', 'erin', 'north', 'beta', NULL, NULL),
			('m3', 'erin', 'north', NULL, 3, 'h-unnamed'),
			('m4', 'erin', 'north', 'alpha', 3, 'h-gone'),
			('m5', 'erin', 'south', 'gamma', 9, 'h-west'),
			('m6', 'omar', 'north', 'delta', 7, 'h-west')`,
		`INSERT INTO "Profiles" ("UserId", "Nickname", "Quota", "HomeHubId") VALUES
			('erin', 'E', 5, 'h-west'),
			('omar', NULL, NULL, 'h-unnamed')`,
	} {
		if _, err := db.Exec(ctx, ddl); err != nil {
			t.Fatalf("%s: %v", ddl, err)
		}
	}

	collection := semanticCollection(t)
	tests := []struct {
		name        string
		fact        string
		set         bool
		partitioned bool
		subject     string
		// want is the non-null values the statement yields, sorted as text.
		want []string
		// wantRows is how many rows the statement yields in all.
		wantRows int
	}{
		{name: "a set in the request's partition", fact: "teams", set: true, partitioned: true, subject: "erin", want: []string{"alpha", "alpha", "beta"}, wantRows: 4},
		{name: "a set of numbers", fact: "tiers", set: true, partitioned: true, subject: "erin", want: []string{"1", "3", "3"}, wantRows: 4},
		{name: "a dotted set: a row reached, a row with no value, no row", fact: "regions", set: true, partitioned: true, subject: "erin", want: []string{"west"}, wantRows: 4},
		{name: "a set in a global request crosses the partitions", fact: "teams", set: true, subject: "erin", want: []string{"alpha", "alpha", "beta", "gamma"}, wantRows: 5},
		{name: "a set of a caller with no anchor row is empty", fact: "teams", set: true, partitioned: true, subject: "nobody", wantRows: 0},
		{name: "a value", fact: "nickname", partitioned: true, subject: "erin", want: []string{"E"}, wantRows: 1},
		{name: "a number value", fact: "quota", partitioned: true, subject: "erin", want: []string{"5"}, wantRows: 1},
		{name: "a dotted value", fact: "homeRegion", partitioned: true, subject: "erin", want: []string{"west"}, wantRows: 1},
		{name: "a value the anchor row does not hold is one row with none", fact: "nickname", partitioned: true, subject: "omar", wantRows: 1},
		{name: "a dotted value that reaches a row with none", fact: "homeRegion", partitioned: true, subject: "omar", wantRows: 1},
		{name: "a value of a caller with no anchor row is one row with none", fact: "quota", partitioned: true, subject: "nobody", wantRows: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sql, registry, err := subjectFactStatement(collection, PostgresDBType, tt.partitioned, tt.fact, tt.set)
			if err != nil {
				t.Fatalf("subjectFactStatement() error = %v", err)
			}
			args := pgx.NamedArgs{}
			for _, name := range registry.referencedNames() {
				switch name {
				case subjectParamName:
					args[name] = tt.subject
				case domainParamName:
					args[name] = "north"
				}
			}
			// The value is read as text here so one scan serves every type; the
			// typed read is the Spanner client's, proven on the emulator.
			result, err := db.Query(ctx, `SELECT "fact"::text FROM (`+sql+`) AS "facts"`, args)
			if err != nil {
				t.Fatalf("running %s: %v", sql, err)
			}
			var (
				got  []string
				rows int
			)
			for result.Next() {
				var value *string
				if err := result.Scan(&value); err != nil {
					t.Fatalf("scanning: %v", err)
				}
				rows++
				if value != nil {
					got = append(got, *value)
				}
			}
			if err := result.Err(); err != nil {
				t.Fatalf("reading the rows of %s: %v", sql, err)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("PostgreSQL yields %v, want %v\n%s", got, tt.want, sql)
			}
			if rows != tt.wantRows {
				t.Errorf("PostgreSQL yields %d rows, want %d\n%s", rows, tt.wantRows, sql)
			}
		})
	}
}
