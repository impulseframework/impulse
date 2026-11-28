package resource

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// heldPostgresKeepers is heldKeepers as PostgreSQL reads it: the query names its table
// and columns quoted, as the statement names the two columns it reads.
type heldPostgresKeepers struct{}

func (heldPostgresKeepers) declaration() TenantsToEnter {
	return TenantsToEnter{
		Query:  `SELECT "KeeperId" AS "RowKey", "Tenant" AS "Tenant" FROM "KeeperPosts" WHERE "Kind" = @postKind`,
		Params: map[string]any{"postKind": "keeper"},
		Match:  map[string]string{"RowKey": "Id"},
	}
}

// heldPostgresPairs is a declaration of PostgreSQL that names a row by two columns, its
// id and its name.
type heldPostgresPairs struct{}

func (heldPostgresPairs) declaration() TenantsToEnter {
	return TenantsToEnter{
		Query: `SELECT "KeeperId" AS "RowKey", "KeeperName" AS "RowName", "Tenant" AS "Tenant" FROM "KeeperNamedPosts"`,
		Match: map[string]string{"RowKey": "Id", "RowName": "Name"},
	}
}

// heldPostgres makes a PostgreSQL database with the rows of heldResources and the posts
// that name each one's tenants: k1 holds a post in bastion and one in anvil, k2 one in
// anvil, k3 none, k4 a post in bastion of a kind the query does not read, and k5, sealed,
// none. The posts named by a keeper's id and name name bastion for k1 by both, for k2 by
// its id and another name, and for k3 by another id and its name.
func heldPostgres(t *testing.T) *pgxpool.Pool {
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
	db, err := container.CreateDatabase(ctx, "caller-enters")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)

	for _, statement := range []string{
		`CREATE TABLE "heldResources" ("Id" text PRIMARY KEY, "Name" text NOT NULL, "Station" text NOT NULL, "Owner" text NOT NULL, "State" text NOT NULL)`,
		`CREATE TABLE "KeeperPosts" ("KeeperId" text NOT NULL, "Tenant" text NOT NULL, "Kind" text NOT NULL)`,
		`INSERT INTO "heldResources" ("Id", "Name", "Station", "Owner", "State") VALUES
			('k1', 'First', 'testDomain', 'u1', 'open'),
			('k2', 'Second', 'testDomain', 'u1', 'open'),
			('k3', 'Third', 'testDomain', 'u1', 'open'),
			('k4', 'Fourth', 'testDomain', 'u1', 'open'),
			('k5', 'Fifth', 'testDomain', 'u1', 'sealed')`,
		`INSERT INTO "KeeperPosts" ("KeeperId", "Tenant", "Kind") VALUES
			('k1', 'bastion', 'keeper'),
			('k1', 'anvil', 'keeper'),
			('k2', 'anvil', 'keeper'),
			('k4', 'bastion', 'visitor')`,
		`CREATE TABLE "KeeperNamedPosts" ("KeeperId" text NOT NULL, "KeeperName" text NOT NULL, "Tenant" text NOT NULL)`,
		`INSERT INTO "KeeperNamedPosts" ("KeeperId", "KeeperName", "Tenant") VALUES
			('k1', 'First', 'bastion'),
			('k2', 'Other', 'bastion'),
			('k9', 'Third', 'bastion')`,
	} {
		if _, err := db.Exec(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	return db.Pool
}

// TestQuerySet_callerEnters_onPostgres runs the Execute affordance of held methods as it
// is rendered for PostgreSQL on a real PostgreSQL: a row offers RetireKeeper (a plain
// method) and SealKeeper (a transition from open) only where none of the row's tenants
// is one the session does not enter, and SealKeeper only on an open row; PruneKeepers,
// which names no row and is offered on the rows, as RetireKeeper. A row named by two
// columns is held back only by a row of the query that agrees with it on both.
func TestQuerySet_callerEnters_onPostgres(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("requires a PostgreSQL container")
	}
	db := heldPostgres(t)

	keepers := func(t *testing.T, perms UserPermissions) (*Statement, error) {
		t.Helper()

		return heldStmt[heldResource[heldPostgresKeepers]](t, PostgresDBType, heldCollection(t, heldResourceName, true), perms)
	}
	both := []string{"RetireKeeper", "SealKeeper"}
	tests := []struct {
		name      string
		stmt      func(t *testing.T, perms UserPermissions) (*Statement, error)
		grants    accesstypes.Decisions
		unentered []accesstypes.Domain
		want      map[string][]string
	}{
		{
			name:      "a session that does not enter bastion is offered neither on the row whose tenants include bastion",
			stmt:      keepers,
			unentered: []accesstypes.Domain{"bastion"},
			want:      map[string][]string{"k1": {}, "k2": both, "k3": both, "k4": both, "k5": {"RetireKeeper"}},
		},
		{
			name:      "one that enters neither anvil nor bastion is offered neither on either row that names one",
			stmt:      keepers,
			unentered: []accesstypes.Domain{"anvil", "bastion"},
			want:      map[string][]string{"k1": {}, "k2": {}, "k3": both, "k4": both, "k5": {"RetireKeeper"}},
		},
		{
			name:      "a method that names no row, offered on the rows, is listed on the rows whose tenants the session enters and on no other",
			stmt:      keepers,
			grants:    accesstypes.Decisions{"PruneKeepers": accesstypes.Granted(), "SealKeeper": accesstypes.Granted()},
			unentered: []accesstypes.Domain{"bastion"},
			want: map[string][]string{
				"k1": {}, "k2": {"PruneKeepers", "SealKeeper"}, "k3": {"PruneKeepers", "SealKeeper"}, "k4": {"PruneKeepers", "SealKeeper"}, "k5": {"PruneKeepers"},
			},
		},
		{
			name: "a row named by two columns is held back by a post that agrees on both, and not by one that agrees on one",
			stmt: func(t *testing.T, perms UserPermissions) (*Statement, error) {
				t.Helper()

				return heldStmt[heldResource[heldPostgresPairs]](t, PostgresDBType, heldCollection(t, heldResourceName, true), perms)
			},
			unentered: []accesstypes.Domain{"bastion"},
			want:      map[string][]string{"k1": {}, "k2": both, "k3": both, "k4": both, "k5": {"RetireKeeper"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			grants := tt.grants
			if grants == nil {
				grants = accesstypes.Decisions{"RetireKeeper": accesstypes.Granted(), "SealKeeper": accesstypes.Granted()}
			}
			perms := heldPermissions{
				capStubPermissions: capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: grants}},
				unentered:          tt.unentered,
				asked:              &atomic.Int32{},
			}
			stmt, err := tt.stmt(t, perms)
			if err != nil {
				t.Fatalf("QuerySet.stmt() error = %v", err)
			}
			got := map[string][]string{}
			for _, row := range runOnPostgres(t, db, stmt, heldResourceName) {
				values, ok := row[capabilityChecksColumnName].([]any)
				if !ok {
					t.Fatalf("row %v: %s = %T, want an array", row["Id"], capabilityChecksColumnName, row[capabilityChecksColumnName])
				}
				checks := make([]bool, 0, len(values))
				for _, value := range values {
					check, ok := value.(bool)
					if !ok {
						t.Fatalf("row %v: a check is %T, want a bool", row["Id"], value)
					}
					checks = append(checks, check)
				}
				methods, _ := stmt.capabilityPlan.assemble(checks)[accesstypes.Execute].([]string)
				got[fmt.Sprint(row["Id"])] = methods
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the methods each row offers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
