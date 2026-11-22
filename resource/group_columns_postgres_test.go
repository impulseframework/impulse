package resource

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// groupPostgres makes a PostgreSQL database with the group fixture's tables in
// PostgreSQL's own types (a yard's key is a uuid there, so the reads' cast to text is
// exercised), the fixture's groups and one share in each yard.
func groupPostgres(t *testing.T) *pgxpool.Pool {
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
	db, err := container.CreateDatabase(ctx, "group-columns")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)

	for _, statement := range []string{
		`CREATE TABLE "CastGroups" ("Id" text PRIMARY KEY, "DomainId" text NOT NULL, "Name" text NOT NULL)`,
		`CREATE TABLE "GroupShares" ("Id" text PRIMARY KEY, "Yard" uuid NOT NULL, "GroupId" text REFERENCES "CastGroups" ("Id"), "Note" text NOT NULL)`,
		`INSERT INTO "CastGroups" ("Id", "DomainId", "Name") VALUES
			('` + groupOfNorth + `', '` + groupYardNorth + `', 'North crew'),
			('` + groupOfSouth + `', '` + groupYardSouth + `', 'South crew'),
			('` + groupOfEveryone + `', '', 'Everyone')`,
		`INSERT INTO "GroupShares" ("Id", "Yard", "Note") VALUES
			('north-share', '` + groupYardNorth + `', 'stored'),
			('south-share', '` + groupYardSouth + `', 'stored')`,
	} {
		if _, err := db.Exec(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	return db.Pool
}

// TestGroupColumn_check_onPostgres runs the rule's reads as they are rendered for
// PostgreSQL on a real PostgreSQL, and holds the same cases the Spanner emulator runs
// to the same answers. The package writes no row on PostgreSQL yet, so the reads run
// through the driver.
func TestGroupColumn_check_onPostgres(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("requires a PostgreSQL container")
	}
	db := groupPostgres(t)
	read := func(ctx context.Context, stmt *Statement) (string, bool, error) {
		var value *string
		if err := db.QueryRow(ctx, stmt.SQL, pgx.NamedArgs(stmt.Params)).Scan(&value); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", false, nil
			}

			return "", false, errors.Wrap(err, "pgx.Row.Scan()")
		}
		if value == nil {
			return "", true, nil
		}

		return *value, true, nil
	}
	ctx := context.Background()

	onRow := groupShare{}.GroupColumns()[0]
	for _, tt := range groupShareCases() {
		t.Run("yard on the row: "+tt.name, func(t *testing.T) {
			t.Parallel()

			tt.judge(t, onRow.check(ctx, PostgresDBType, read, tt.anchor, tt.group))
		})
	}

	throughParent := groupSeal{}.GroupColumns()[0]
	for _, tt := range []groupCase{
		{name: "a group kept in the parent's yard is written", anchor: "north-share", group: groupOfNorth},
		{name: "a group kept in another yard is refused", anchor: "north-share", group: groupOfSouth, refused: true},
		{name: "a group kept for the whole application is refused", anchor: "north-share", group: groupOfEveryone, refused: true},
		{name: "an id that is no group's is refused in the same words", anchor: "north-share", group: groupOfNobody, refused: true},
		{name: "the other yard's own group is written under its share", anchor: "south-share", group: groupOfSouth},
		{name: "a parent that is not there is left to the foreign key", anchor: "no-share", group: groupOfSouth},
	} {
		t.Run("yard through a parent: "+tt.name, func(t *testing.T) {
			t.Parallel()

			tt.judge(t, throughParent.check(ctx, PostgresDBType, read, tt.anchor, tt.group))
		})
	}
}
