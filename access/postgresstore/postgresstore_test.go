package postgresstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/access/internal/storetest"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNew_naming(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		opts      []Option
		wantTable string
		wantErr   bool
	}{
		{name: "defaults", wantTable: `"AccessRoles"`},
		{name: "store name", opts: []Option{WithStore("AdminPortal")}, wantTable: `"AccessAdminPortalRoles"`},
		{name: "prefix override", opts: []Option{WithPrefix("Acl"), WithStore("Portal")}, wantTable: `"AclPortalRoles"`},
		{name: "invalid prefix", opts: []Option{WithPrefix("bad prefix")}, wantErr: true},
		{name: "empty prefix", opts: []Option{WithPrefix("")}, wantErr: true},
		{name: "invalid store name", opts: []Option{WithStore("drop table")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store, err := New(nil, tt.opts...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			ddl := strings.Join(store.DDL(), "\n")
			if !strings.Contains(ddl, "CREATE TABLE "+tt.wantTable+" (") {
				t.Errorf("DDL() does not create table %s:\n%s", tt.wantTable, ddl)
			}
		})
	}
}

func TestStore_conformance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store, database := prepareStore(ctx, t)
	storetest.Run(t, store, roleWriter(database, store))
}

func TestStore_conformance_namedStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store, database := prepareStore(ctx, t, WithStore("AdminPortal"))
	storetest.Run(t, store, roleWriter(database, store))
}

// TestStore_isolation proves separate stores in one database cannot see each
// other's rows: cross-store leakage is structurally impossible, not filtered.
func TestStore_isolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	adminStore, db := prepareStore(ctx, t, WithStore("AdminPortal"))
	partnerStore, err := New(db.Pool, WithStore("PartnerPortal"))
	if err != nil {
		t.Fatalf("postgresstore.New(): %v", err)
	}
	if err := applyDDL(ctx, db, partnerStore); err != nil {
		t.Fatalf("applying DDL: %v", err)
	}

	if err := adminStore.InsertRole(ctx, accesstypes.DomainPolicyScope("tenant1"), "Editor", "Edits the records of the tenant."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	if err := adminStore.InsertUserRole(ctx, accesstypes.DomainPolicyScope("tenant1"), "alice", "Editor", accesstypes.Period{}, ""); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}

	if exists, err := partnerStore.RoleExists(ctx, accesstypes.DomainPolicyScope("tenant1"), "Editor"); err != nil || exists {
		t.Errorf("RoleExists() on sibling store = (%v, %v), want (false, nil)", exists, err)
	}
	records, err := partnerStore.ReadPolicy(ctx)
	if err != nil {
		t.Fatalf("ReadPolicy() error = %v", err)
	}
	if len(records.Roles) != 0 || len(records.Grants) != 0 || len(records.Memberships) != 0 {
		t.Errorf("ReadPolicy() on sibling store returned rows: %+v", records)
	}
}

// TestStore_readsWhileATransactionIsOpen pins what the store's reads do while a
// transaction of the application's is open on the same pool, under the same context:
// they succeed. The store reads on a connection of its own, and the PostgreSQL client
// marks no context, so nothing refuses the read as the Spanner client refuses one
// under a transaction's context. The permission engine reads on its own context either
// way, and a check made inside a transaction right after a policy write sees the write
// (TestEngine_checksInsideAnOpenTransaction).
func TestStore_readsWhileATransactionIsOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store, db := prepareStore(ctx, t)
	scope := accesstypes.DomainPolicyScope("tenant1")
	if err := store.InsertRole(ctx, scope, "Editor", "Edits the records of the tenant."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("pgxpool.Pool.Begin() error = %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Errorf("pgx.Tx.Rollback() error = %v", err)
		}
	}()
	if _, err := tx.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("pgx.Tx.Exec() error = %v", err)
	}

	records, err := store.ReadPolicy(ctx)
	if err != nil {
		t.Fatalf("ReadPolicy() while a transaction is open error = %v, want the policy", err)
	}
	if len(records.Roles) != 1 {
		t.Errorf("ReadPolicy() while a transaction is open read %d roles, want the one committed", len(records.Roles))
	}
	if roles, err := store.ListRoles(ctx, scope); err != nil || len(roles) != 1 {
		t.Errorf("ListRoles() while a transaction is open = (%v, %v), want the one role", roles, err)
	}
}

// TestEngine_checksInsideAnOpenTransaction runs the engine's suite on PostgreSQL: a
// role is taken away, and the first check after the engine learns of it is made inside
// an open transaction of another request. The check sees the role gone: the engine
// reloads on its own context, on a connection of the store's own, beside the
// transaction.
func TestEngine_checksInsideAnOpenTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store, database := prepareStore(ctx, t)
	storetest.RunEngine(t, store, openTransaction(database))
}

// TestDDL_keys pins the shape of the tables: Kind leads every key and is
// checked against the three values the store writes; Axis is the second key
// column of every table, declared NOT NULL and written "" for the default
// axis, the same key order as the Spanner store, so a single-axis
// application's rows stay correct unchanged when it later declares an axis;
// UserRoles stands alone, indexed by user, since a membership names a role
// with no row; and RoleGrants keeps its cascading foreign key to Roles so a
// custom role's grants go with it.
func TestDDL_keys(t *testing.T) {
	t.Parallel()

	store, err := New(nil)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	ddl := store.DDL()

	tests := []struct {
		name string
		stmt int
		want []string
	}{
		{name: "roles", stmt: 0, want: []string{
			`"Kind" TEXT NOT NULL`, `"Axis" TEXT NOT NULL`, `"Description" TEXT NOT NULL`,
			`CONSTRAINT "AccessRolesKind" CHECK ("Kind" IN ('global', 'domain', 'every'))`,
			`PRIMARY KEY ("Kind", "Axis", "DomainId", "Role")`,
		}},
		{name: "user roles stand alone", stmt: 1, want: []string{
			`"Kind" TEXT NOT NULL`, `"Axis" TEXT NOT NULL`,
			`CONSTRAINT "AccessUserRolesKind" CHECK ("Kind" IN ('global', 'domain', 'every'))`,
			`PRIMARY KEY ("Kind", "Axis", "DomainId", "Role", "UserId")`,
		}},
		{name: "user roles by user", stmt: 2, want: []string{`CREATE INDEX "AccessUserRolesByUserId" ON "AccessUserRoles" ("UserId", "Kind", "Axis", "DomainId")`}},
		{name: "role grants cascade from roles", stmt: 3, want: []string{
			`"Kind" TEXT NOT NULL`, `"Axis" TEXT NOT NULL`,
			`CONSTRAINT "AccessRoleGrantsKind" CHECK ("Kind" IN ('global', 'domain', 'every'))`,
			`PRIMARY KEY ("Kind", "Axis", "DomainId", "Role", "Permission", "Resource", "Field", "Condition")`,
			`FOREIGN KEY ("Kind", "Axis", "DomainId", "Role") REFERENCES "AccessRoles" ("Kind", "Axis", "DomainId", "Role") ON DELETE CASCADE`,
		}},
		{name: "groups are kept globally or in one domain", stmt: 4, want: []string{
			`"Id" TEXT NOT NULL`, `"Name" TEXT NOT NULL`, `"Description" TEXT NOT NULL`,
			`CONSTRAINT "AccessGroupsKind" CHECK ("Kind" IN ('global', 'domain'))`,
			`PRIMARY KEY ("Id")`,
		}},
		{name: "a group's name is one group's where it is kept", stmt: 5, want: []string{`CREATE UNIQUE INDEX "AccessGroupsByName" ON "AccessGroups" ("Kind", "Axis", "DomainId", "Name")`}},
		{name: "a member needs the group", stmt: 6, want: []string{
			`CONSTRAINT "AccessGroupMembersGroup" FOREIGN KEY ("GroupId") REFERENCES "AccessGroups" ("Id")`,
			`PRIMARY KEY ("GroupId", "UserId")`,
		}},
		{name: "group members by user", stmt: 7, want: []string{`CREATE INDEX "AccessGroupMembersByUserId" ON "AccessGroupMembers" ("UserId", "GroupId")`}},
		{name: "a group's role needs the group", stmt: 8, want: []string{
			`CONSTRAINT "AccessGroupRolesKind" CHECK ("Kind" IN ('global', 'domain', 'every'))`,
			`CONSTRAINT "AccessGroupRolesGroup" FOREIGN KEY ("GroupId") REFERENCES "AccessGroups" ("Id")`,
			`PRIMARY KEY ("GroupId", "Kind", "Axis", "DomainId", "Role")`,
		}},
		{name: "group roles by where they are held", stmt: 9, want: []string{`CREATE INDEX "AccessGroupRolesByScope" ON "AccessGroupRoles" ("Kind", "Axis", "DomainId", "Role")`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, want := range tt.want {
				if !strings.Contains(ddl[tt.stmt], want) {
					t.Errorf("DDL()[%d] lacks %q:\n%s", tt.stmt, want, ddl[tt.stmt])
				}
			}
		})
	}
}

// turnRole is the custom role the turn tests give and delete, in turnScope.
const turnRole = accesstypes.Role("Warden")

var turnScope = accesstypes.DomainPolicyScope("tenant1")

// TestStore_DeleteRole_takesItsTurnWithAGive pins that a role's deletion takes the
// lock on the role's name a custom role's give takes, and reads after it: a membership
// written while the delete waits for the lock is seen, and the delete refuses. A
// delete that read before the lock, or under a snapshot taken before it, would delete
// the role from under the membership.
func TestStore_DeleteRole_takesItsTurnWithAGive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, database := prepareStore(ctx, t)
	if err := store.InsertRole(ctx, turnScope, turnRole, "Keeps the wards."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	turns := repeatableReadStore(ctx, t, database)

	lock := holdRoleNameLock(ctx, t, database, store, turnRole)
	var (
		deleted bool
		err     error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		deleted, err = turns.DeleteRole(ctx, turnScope, turnRole)
	}()
	waitingOnALock(t, database, done)
	if err := store.InsertUserRole(ctx, turnScope, "alice", turnRole, accesstypes.Period{}, ""); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}
	if err := lock.Commit(ctx); err != nil {
		t.Fatalf("pgx.Tx.Commit() error = %v", err)
	}
	<-done

	if deleted || !errors.Is(err, policy.ErrStillAssigned) {
		t.Errorf("DeleteRole() of a role given while it waited = (%v, %v), want it refused with policy.ErrStillAssigned", deleted, err)
	}
	if exists, err := store.RoleExists(ctx, turnScope, turnRole); err != nil || !exists {
		t.Errorf("RoleExists() after the refusal = (%v, %v), want the role there", exists, err)
	}
}

// TestStore_InsertCustomUserRole_takesItsTurnWithADelete pins that a custom role's give
// takes the lock on the role's name a role's deletion takes, and reads the role's row
// after it: a row deleted while the give waits for the lock is seen gone, and nothing
// is written.
func TestStore_InsertCustomUserRole_takesItsTurnWithADelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, database := prepareStore(ctx, t)
	if err := store.InsertRole(ctx, turnScope, turnRole, "Keeps the wards."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	turns := repeatableReadStore(ctx, t, database)

	lock := holdRoleNameLock(ctx, t, database, store, turnRole)
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		err = turns.InsertCustomUserRole(ctx, turnScope, "alice", turnRole, accesstypes.Period{}, "")
	}()
	waitingOnALock(t, database, done)
	kind, axis, domain := policy.ScopeColumns(turnScope)
	if _, err := lock.Exec(ctx, store.sqlDeleteRole, kind, axis, domain, turnRole); err != nil {
		t.Fatalf("pgx.Tx.Exec() delete role error = %v", err)
	}
	if err := lock.Commit(ctx); err != nil {
		t.Fatalf("pgx.Tx.Commit() error = %v", err)
	}
	<-done

	if !errors.Is(err, policy.ErrNoRole) {
		t.Errorf("InsertCustomUserRole() of a role deleted while it waited: error = %v, want policy.ErrNoRole", err)
	}
	if users, err := store.ListRoleUsers(ctx, turnScope, turnRole); err != nil || len(users) != 0 {
		t.Errorf("ListRoleUsers() after the refusal = (%v, %v), want nobody", users, err)
	}
}

// repeatableReadStore is a store on a pool of its own to the test's database whose
// sessions begin every transaction at repeatable read: the database's default level is
// not the store's to count on, so a transaction of the store that leaves its level to
// the database reads under a snapshot taken before it waited, and fails the test.
func repeatableReadStore(ctx context.Context, t *testing.T, database *db.PostgresDatabase) *Store {
	t.Helper()
	config := database.Config()
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig() error = %v", err)
	}
	t.Cleanup(pool.Close)
	store, err := New(pool)
	if err != nil {
		t.Fatalf("postgresstore.New() error = %v", err)
	}

	return store
}

// holdRoleNameLock begins a transaction on the test's database that takes the lock a
// store takes on the role's name, and returns it: a statement of the store that takes
// the lock waits until the transaction ends. A transaction the test has not ended is
// rolled back.
func holdRoleNameLock(ctx context.Context, t *testing.T, database *db.PostgresDatabase, store *Store, role accesstypes.Role) pgx.Tx {
	t.Helper()
	tx, err := database.Begin(ctx)
	if err != nil {
		t.Fatalf("pgxpool.Pool.Begin() error = %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	})
	if _, err := tx.Exec(ctx, store.sqlLockRoleName, store.names.rawRoles, policy.FoldRoleName(string(role))); err != nil {
		t.Fatalf("pgx.Tx.Exec() lock role name error = %v", err)
	}

	return tx
}

// waitingOnALock waits until a session of the test's database waits on a lock, as a
// statement of the store does while the test holds the role name's lock. It fails the
// test where done is closed first: the store answered without waiting for the lock.
func waitingOnALock(t *testing.T, database *db.PostgresDatabase, done <-chan struct{}) {
	t.Helper()
	ctx := t.Context()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatal("the store answered before it was seen waiting on the role name's lock")
		default:
		}
		var waiting bool
		if err := database.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where datname = current_database() and wait_event_type = 'Lock')`).Scan(&waiting); err != nil {
			t.Fatalf("pgxpool.Pool.QueryRow() pg_stat_activity error = %v", err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no session of the database was seen waiting on a lock within 30 seconds")
}
