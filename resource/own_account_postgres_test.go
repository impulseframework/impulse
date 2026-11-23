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

// ownPostgres makes a PostgreSQL database with three rows of heldResources, one for each
// account of the own-account fixtures: the session's, owned by the third person; the
// real person's behind a session that views as someone else, owned by nobody; and the
// third person's, owned by the session's account and holding a post in bastion.
func ownPostgres(t *testing.T) *pgxpool.Pool {
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
	db, err := container.CreateDatabase(ctx, "own-account")
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	t.Cleanup(db.Close)

	for _, statement := range []string{
		`CREATE TABLE "heldResources" ("Id" text PRIMARY KEY, "Name" text NOT NULL, "Station" text NOT NULL, "Owner" text, "State" text NOT NULL)`,
		`CREATE TABLE "KeeperPosts" ("KeeperId" text NOT NULL, "Tenant" text NOT NULL, "Kind" text NOT NULL)`,
		`INSERT INTO "heldResources" ("Id", "Name", "Station", "Owner", "State") VALUES
			('` + ownSessionAccount + `', 'Session', 'testDomain', '` + ownOtherAccount + `', 'open'),
			('` + ownActorAccount + `', 'Actor', 'testDomain', NULL, 'open'),
			('` + ownOtherAccount + `', 'Other', 'testDomain', '` + ownSessionAccount + `', 'open')`,
		`INSERT INTO "KeeperPosts" ("KeeperId", "Tenant", "Kind") VALUES ('` + ownOtherAccount + `', 'bastion', 'keeper')`,
	} {
		if _, err := db.Exec(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	return db.Pool
}

// TestQuerySet_notOnOwnAccount_onPostgres runs the Execute affordance of methods a row
// keeps off the caller's own account (@notOnOwnAccount) as it is rendered for PostgreSQL
// on a real PostgreSQL: the row of the session's account leaves DisableKeeper out and
// lists GreetKeeper, which has no mark; a session that views as someone else leaves it
// out of the viewed person's row and the real person's; a row whose column is NULL lists
// it; and RetireKeeper, also offered only to a caller whose session enters the row's
// tenants, is listed only where both hold.
func TestQuerySet_notOnOwnAccount_onPostgres(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("requires a PostgreSQL container")
	}
	db := ownPostgres(t)

	tests := []struct {
		name        string
		user, actor string
		grants      accesstypes.Decisions
		unentered   []accesstypes.Domain
		want        map[string][]string
	}{
		{
			name:   "the row of the session's account leaves the marked method out and lists the unmarked one",
			user:   ownSessionAccount,
			grants: accesstypes.Decisions{"DisableKeeper": accesstypes.Granted(), "GreetKeeper": accesstypes.Granted()},
			want: map[string][]string{
				ownSessionAccount: {"GreetKeeper"},
				ownActorAccount:   {"DisableKeeper", "GreetKeeper"},
				ownOtherAccount:   {"DisableKeeper", "GreetKeeper"},
			},
		},
		{
			name:   "a session that views as someone else leaves it out of the viewed person's row and the real person's",
			user:   ownSessionAccount,
			actor:  ownActorAccount,
			grants: accesstypes.Decisions{"DisableKeeper": accesstypes.Granted()},
			want:   map[string][]string{ownSessionAccount: {}, ownActorAccount: {}, ownOtherAccount: {"DisableKeeper"}},
		},
		{
			name:   "a row whose column is NULL lists the method",
			user:   ownSessionAccount,
			grants: accesstypes.Decisions{"ReassignKeeper": accesstypes.Granted()},
			want:   map[string][]string{ownSessionAccount: {"ReassignKeeper"}, ownActorAccount: {"ReassignKeeper"}, ownOtherAccount: {}},
		},
		{
			name:      "a method with @callerEnters and the mark is listed only where the session enters the row's tenants and the row is not its own",
			user:      ownSessionAccount,
			grants:    accesstypes.Decisions{"RetireKeeper": accesstypes.Granted()},
			unentered: []accesstypes.Domain{"bastion"},
			want:      map[string][]string{ownSessionAccount: {}, ownActorAccount: {"RetireKeeper"}, ownOtherAccount: {}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			perms := heldPermissions{
				capStubPermissions: capStubPermissions{byPerm: map[accesstypes.Permission]accesstypes.Decisions{accesstypes.Execute: tt.grants}},
				unentered:          tt.unentered,
				asked:              &atomic.Int32{},
			}
			stmt, err := ownStmt[heldResource[heldPostgresKeepers]](t, ownSession(t, tt.user, tt.actor), PostgresDBType, ownCollection(t, heldResourceName), perms)
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
