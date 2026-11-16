package resource

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/jackc/pgx/v5/pgconn"
)

// postgresUniqueViolation is PostgreSQL's code for a row a unique index refuses.
const postgresUniqueViolation = "23505"

// TestTenantDirectoryRules_onPostgres runs the two rules of a tenant's directory on a
// real PostgreSQL, as far as the package reaches there: it has no write path on
// PostgreSQL, so what is proven is the schema's half and the rules' reads. The
// directories table carries the two unique indexes the Spanner fixture carries, and a
// second directory for a station and a directory another station has are each refused
// at the insert; and the PostgreSQL rendering of the statements the rules read with
// answers the rows that decide each refusal, and the directory-only flag, over the same
// rows the emulator test holds.
func TestTenantDirectoryRules_onPostgres(t *testing.T) {
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
	db, err := container.CreateDatabase(ctx, "directory-rules")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)

	for _, ddl := range []string{
		`CREATE TABLE "Stations" ("Id" text PRIMARY KEY, "Name" text NOT NULL, "DirectoryOnly" boolean NOT NULL DEFAULT FALSE)`,
		`CREATE TABLE "StationDirectories" ("Id" text PRIMARY KEY, "StationId" text NOT NULL REFERENCES "Stations" ("Id"), "Method" text NOT NULL, "Connection" text NOT NULL, "DefaultSeat" text)`,
		`CREATE UNIQUE INDEX "StationDirectoriesByStationId" ON "StationDirectories" ("StationId")`,
		`CREATE UNIQUE INDEX "StationDirectoriesByMethodConnection" ON "StationDirectories" ("Method", "Connection")`,
		`INSERT INTO "Stations" ("Id", "Name", "DirectoryOnly") VALUES ('alpha', 'Station alpha', TRUE), ('beta', 'Station beta', FALSE), ('gamma', 'Station gamma', FALSE)`,
		`INSERT INTO "StationDirectories" ("Id", "StationId", "Method", "Connection") VALUES
			('alpha-0', 'alpha', 'google', 'frontier.example'),
			('beta-0', 'beta', 'azure', '6f2c1b7e-0000-4000-8000-000000000001')`,
	} {
		if _, err := db.Exec(ctx, ddl); err != nil {
			t.Fatalf("%s: %v", ddl, err)
		}
	}

	refused := []struct {
		name   string
		insert string
		index  string
	}{
		{
			name:   "a second directory for a station is refused by the index on the station",
			insert: `INSERT INTO "StationDirectories" ("Id", "StationId", "Method", "Connection") VALUES ('alpha-1', 'alpha', 'google', 'elsewhere.example')`,
			index:  "StationDirectoriesByStationId",
		},
		{
			name:   "a directory another station has is refused by the index on the method and the connection",
			insert: `INSERT INTO "StationDirectories" ("Id", "StationId", "Method", "Connection") VALUES ('gamma-0', 'gamma', 'google', 'frontier.example')`,
			index:  "StationDirectoriesByMethodConnection",
		},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := db.Exec(ctx, tt.insert)
			var refusal *pgconn.PgError
			if !errors.As(err, &refusal) || refusal.Code != postgresUniqueViolation || refusal.ConstraintName != tt.index {
				t.Fatalf("the insert's error = %v, want a unique violation of %s", err, tt.index)
			}
		})
	}

	statements, err := tenantDirectoryStatements(PostgresDBType, stationsTable, stationsKey, stationColumns)
	if err != nil {
		t.Fatalf("tenantDirectoryStatements() error = %v", err)
	}
	held := []struct {
		name       string
		station    string
		method     string
		connection string
		want       []heldDirectory
	}{
		{name: "a station with no directory and a directory no station has: nothing stands in the way", station: "gamma", method: "google", connection: "elsewhere.example"},
		{
			name: "a station that has a directory: its row", station: "beta", method: "google", connection: "elsewhere.example",
			want: []heldDirectory{{Domain: "beta", Method: "azure", Connection: "6f2c1b7e-0000-4000-8000-000000000001"}},
		},
		{
			name: "a directory another station has: that station's row", station: "gamma", method: "google", connection: "frontier.example",
			want: []heldDirectory{{Domain: "alpha", Method: "google", Connection: "frontier.example"}},
		},
		{
			name: "both: the station's own row first", station: "beta", method: "google", connection: "frontier.example",
			want: []heldDirectory{{Domain: "beta", Method: "azure", Connection: "6f2c1b7e-0000-4000-8000-000000000001"}, {Domain: "alpha", Method: "google", Connection: "frontier.example"}},
		},
	}
	for _, tt := range held {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := db.Query(ctx, statements.Held, tt.station, tt.method, tt.connection)
			if err != nil {
				t.Fatalf("running %s: %v", statements.Held, err)
			}
			var got []heldDirectory
			for result.Next() {
				var row heldDirectory
				if err := result.Scan(&row.Domain, &row.Method, &row.Connection); err != nil {
					t.Fatalf("scanning: %v", err)
				}
				got = append(got, row)
			}
			if err := result.Err(); err != nil {
				t.Fatalf("reading the rows of %s: %v", statements.Held, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("PostgreSQL yields (-want +got):\n%s\n%s", diff, statements.Held)
			}
		})
	}

	for station, want := range map[string]bool{"alpha": true, "beta": false} {
		t.Run("the directory-only flag of "+station, func(t *testing.T) {
			t.Parallel()

			var got bool
			if err := db.QueryRow(ctx, statements.DirectoryOnly, station).Scan(&got); err != nil {
				t.Fatalf("running %s: %v", statements.DirectoryOnly, err)
			}
			if got != want {
				t.Errorf("PostgreSQL yields directory only = %v for %s, want %v", got, station, want)
			}
		})
	}
}
