package resource

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	initiator "github.com/impulseframework/impulse/db"
	"github.com/jackc/pgx/v5"
)

// onRecordPostgresSchema is the fixture's schema on PostgreSQL, the tables and columns of
// the Spanner fixture (testdata/recordroles/schema).
var onRecordPostgresSchema = []string{
	`CREATE TABLE "Clients" ("Id" text PRIMARY KEY, "DomainId" text NOT NULL)`,
	`CREATE TABLE "Projects" ("Id" text PRIMARY KEY, "DomainId" text NOT NULL, "ClientId" text REFERENCES "Clients" ("Id"))`,
	`CREATE TABLE "Tasks" ("Id" text PRIMARY KEY, "DomainId" text NOT NULL, "ProjectId" text REFERENCES "Projects" ("Id"), "FormerProjectId" text REFERENCES "Projects" ("Id"))`,
	`CREATE TABLE "Comments" ("Id" text PRIMARY KEY, "DomainId" text NOT NULL, "TaskId" text REFERENCES "Tasks" ("Id"), "Body" text)`,
	`CREATE TABLE "Milestones" ("Id" text PRIMARY KEY, "DomainId" text NOT NULL, "ProjectId" text REFERENCES "Projects" ("Id"))`,
	`CREATE TABLE "Invoices" ("Id" text PRIMARY KEY, "DomainId" text NOT NULL)`,
	`CREATE TABLE "StaffGroups" ("Id" text PRIMARY KEY, "DomainId" text NOT NULL)`,
	`CREATE TABLE "StaffGroupMembers" ("GroupId" text NOT NULL REFERENCES "StaffGroups" ("Id"), "UserId" text NOT NULL, "StartsAt" timestamptz, "EndsAt" timestamptz, PRIMARY KEY ("GroupId", "UserId"))`,
	`CREATE TABLE "StaffRecordUserRoles" ("Axis" text NOT NULL, "DomainId" text NOT NULL, "Resource" text NOT NULL, "RecordId" text NOT NULL, "Role" text NOT NULL, "UserId" text NOT NULL, "StartsAt" timestamptz, "EndsAt" timestamptz, PRIMARY KEY ("Axis", "DomainId", "Resource", "RecordId", "Role", "UserId"))`,
	`CREATE TABLE "StaffRecordGroupRoles" ("Axis" text NOT NULL, "DomainId" text NOT NULL, "Resource" text NOT NULL, "RecordId" text NOT NULL, "Role" text NOT NULL, "GroupId" text NOT NULL REFERENCES "StaffGroups" ("Id"), "StartsAt" timestamptz, "EndsAt" timestamptz, PRIMARY KEY ("Axis", "DomainId", "Resource", "RecordId", "Role", "GroupId"))`,
}

// TestGeneratedCollection_RecordCondition_onPostgres holds the conditions a role held on a
// record counts under to the fixture's rows on a real PostgreSQL (askOnRecord, and a
// second site's, askOnRecordSecondSite), the same rows and the same expectations as on
// Spanner.
func TestGeneratedCollection_RecordCondition_onPostgres(t *testing.T) {
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
	db, err := container.CreateDatabase(ctx, "record-roles")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)

	for _, ddl := range onRecordPostgresSchema {
		if _, err := db.Exec(ctx, ddl); err != nil {
			t.Fatalf("%s: %v", ddl, err)
		}
	}
	for _, row := range onRecordRows() {
		columns := slices.Sorted(func(yield func(string) bool) {
			for column := range row.values {
				if !yield(column) {
					return
				}
			}
		})
		names := make([]string, 0, len(columns))
		places := make([]string, 0, len(columns))
		values := make([]any, 0, len(columns))
		for i, column := range columns {
			names = append(names, `"`+column+`"`)
			places = append(places, fmt.Sprintf("$%d", i+1))
			values = append(values, row.values[column])
		}
		insert := fmt.Sprintf(`INSERT INTO %q (%s) VALUES (%s)`, row.table, strings.Join(names, ", "), strings.Join(places, ", "))
		if _, err := db.Exec(ctx, insert, values...); err != nil {
			t.Fatalf("%s: %v", insert, err)
		}
	}

	postgresDB := onRecordDatabase{
		dialect: PostgreSQL,
		keys: func(t *testing.T, sql string, params map[string]any) []string {
			t.Helper()

			rows, err := db.Query(ctx, sql, pgx.NamedArgs(params))
			if err != nil {
				t.Fatalf("running %s: %v", sql, err)
			}
			keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				t.Fatalf("reading the rows of %s: %v", sql, err)
			}

			return keys
		},
		// A create's check compares the row's proposed values as bare parameters
		// (CASE WHEN @_c1 IS NULL ...), whose type PostgreSQL cannot infer when it prepares
		// the statement, so the check is sent with its values written in: the resource
		// package writes on Spanner, and this asks only what the condition means.
		holds: func(t *testing.T, sql string, params map[string]any) bool {
			t.Helper()

			var holds *bool
			if err := db.QueryRow(ctx, sql, pgx.QueryExecModeSimpleProtocol, pgx.NamedArgs(params)).Scan(&holds); err != nil {
				t.Fatalf("running %s: %v", sql, err)
			}

			return holds != nil && *holds
		},
	}
	askOnRecord(t, postgresDB)
	askOnRecordSecondSite(t, postgresDB)
}
