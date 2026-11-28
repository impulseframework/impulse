package postgresstore

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/access/internal/storetest"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
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
			// The roles held on a record are named by the same rule.
			base := strings.TrimSuffix(tt.wantTable, `Roles"`)
			for _, table := range []string{base + `RecordUserRoles"`, base + `RecordGroupRoles"`} {
				if !strings.Contains(ddl, "CREATE TABLE "+table+" (") {
					t.Errorf("DDL() does not create table %s:\n%s", table, ddl)
				}
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

// TestStore_auditRecords runs the shared audit phase on PostgreSQL: the store named the
// audit records table, made by auditlog.PostgresDDL, and a store of the same tables that
// names none.
func TestStore_auditRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const table = "SessionAuditRecords"
	recording, database := prepareStore(ctx, t, WithAuditRecords(table))
	for _, stmt := range auditlog.PostgresDDL(table) {
		if _, err := database.Exec(ctx, stmt); err != nil {
			t.Fatalf("executing DDL statement %q: %v", stmt, err)
		}
	}
	plain, err := New(database.Pool)
	if err != nil {
		t.Fatalf("postgresstore.New(): %v", err)
	}

	storetest.RunAuditRecords(t, recording, plain, func(t *testing.T) []storetest.AuditRow {
		t.Helper()
		rows, err := database.Query(t.Context(), `SELECT "Act", COALESCE("Process", ''), COALESCE("Directory", ''), "SubjectId",
			COALESCE("RoleName", '') FROM "`+table+`" ORDER BY "CreatedAt"`)
		if err != nil {
			t.Fatalf("read the audit records: %v", err)
		}
		defer rows.Close()
		var got []storetest.AuditRow
		for rows.Next() {
			var r storetest.AuditRow
			if err := rows.Scan(&r.Act, &r.Process, &r.Directory, &r.SubjectID, &r.RoleName); err != nil {
				t.Fatalf("read the audit records: %v", err)
			}
			got = append(got, r)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read the audit records: %v", err)
		}

		return got
	})
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
	if err := adminStore.InsertUserRole(ctx, accesstypes.DomainPolicyScope("tenant1"), "alice", "Editor", accesstypes.Period{}, "", nil); err != nil {
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
		{name: "a person's roles on a record lead with the record", stmt: 10, want: []string{
			`CREATE TABLE "AccessRecordUserRoles" (`,
			`"Resource" TEXT NOT NULL`, `"RecordId" TEXT NOT NULL`, `"UserId" TEXT NOT NULL`,
			`PRIMARY KEY ("Axis", "DomainId", "Resource", "RecordId", "Role", "UserId")`,
		}},
		{name: "a person's roles on records by user", stmt: 11, want: []string{`CREATE INDEX "AccessRecordUserRolesByUserId" ON "AccessRecordUserRoles" ("UserId", "Axis", "DomainId")`}},
		{name: "a group's role on a record needs the group", stmt: 12, want: []string{
			`CREATE TABLE "AccessRecordGroupRoles" (`,
			`"GroupId" TEXT NOT NULL`,
			`CONSTRAINT "AccessRecordGroupRolesGroup" FOREIGN KEY ("GroupId") REFERENCES "AccessGroups" ("Id")`,
			`PRIMARY KEY ("Axis", "DomainId", "Resource", "RecordId", "Role", "GroupId")`,
		}},
		{name: "a group's roles on records by group", stmt: 13, want: []string{`CREATE INDEX "AccessRecordGroupRolesByGroupId" ON "AccessRecordGroupRoles" ("GroupId")`}},
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

// turnGroup is the group the turn tests write under and delete, kept in turnScope.
const turnGroup = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-0000000000a1")

var turnScope = accesstypes.DomainPolicyScope("tenant1")

// TestStore_DeleteRole_takesItsTurnWithAGive pins that a role's deletion locks the
// role's row before it looks for who holds the role, and looks after the lock: a
// membership written by a transaction that read the row for key share, as an
// application's own transaction gives a role, and still open when the delete starts,
// is seen once it commits, and the delete refuses. A delete that looked before the
// lock, or under a snapshot taken before it, would delete the role from under the
// membership.
func TestStore_DeleteRole_takesItsTurnWithAGive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, database := prepareStore(ctx, t)
	if err := store.InsertRole(ctx, turnScope, turnRole, "Keeps the wards."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	turns := repeatableReadStore(ctx, t, database.Pool)

	kind, axis, domain := policy.ScopeColumns(turnScope)
	give := lockRow(ctx, t, database.Pool, roleRowSQL(store, "key share"), kind, axis, domain, turnRole)
	if _, err := give.Exec(ctx, store.sqlInsertUserRole, kind, axis, domain, turnRole, "alice", nil, nil, nil); err != nil {
		t.Fatalf("pgx.Tx.Exec() insert user role error = %v", err)
	}
	var (
		deleted bool
		err     error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		deleted, err = turns.DeleteRole(ctx, turnScope, turnRole)
	}()
	waitingOnALock(t, database.Pool, done)
	if err := give.Commit(ctx); err != nil {
		t.Fatalf("pgx.Tx.Commit() error = %v", err)
	}
	<-done

	if deleted || !errors.Is(err, policy.ErrStillAssigned) {
		t.Errorf("DeleteRole() of a role given while it waited = (%v, %v), want it refused with policy.ErrStillAssigned", deleted, err)
	}
	if exists, err := store.RoleExists(ctx, turnScope, turnRole); err != nil || !exists {
		t.Errorf("RoleExists() after the refusal = (%v, %v), want the role there", exists, err)
	}
	if users, err := store.ListRoleUsers(ctx, turnScope, turnRole); err != nil || len(users) != 1 {
		t.Errorf("ListRoleUsers() after the refusal = (%v, %v), want alice", users, err)
	}
}

// TestStore_DeleteRole_takesItsTurnWithAGroupsDelete pins that the deletion of a role
// held in every domain reads for key share the row of a group whose ended roles of the
// name it removes before it removes any of them: while a transaction holds the group's
// row for update, as a group's deletion does before it removes the same ended roles, the
// role's deletion waits on it. A deletion that removed the group's ended roles first
// could hold one of them while the group's deletion holds the other, and the database
// would end one of the two with a deadlock.
func TestStore_DeleteRole_takesItsTurnWithAGroupsDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, database := prepareStore(ctx, t)
	every := accesstypes.EveryDomainPolicyScope()
	if err := store.InsertRole(ctx, every, turnRole, "Keeps the wards."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	group := &policy.GroupRow{Group: policy.Group{ID: turnGroup, Scope: accesstypes.GlobalPolicyScope()}, Name: "Wards", Description: "Who keeps the wards."}
	if taken, err := store.InsertGroup(ctx, group); err != nil || taken {
		t.Fatalf("InsertGroup() = (%v, %v), want it written", taken, err)
	}
	now := time.Now()
	ended := accesstypes.Period{Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)}
	// tenant2 holds no role of the name of its own: the group's role there reads
	// every domain's row, and goes with it.
	for _, scope := range []accesstypes.PolicyScope{every, accesstypes.DomainPolicyScope("tenant2")} {
		if err := store.InsertGroupRole(ctx, turnGroup, scope, turnRole, ended); err != nil {
			t.Fatalf("InsertGroupRole() in %s error = %v", scope, err)
		}
	}
	turns := repeatableReadStore(ctx, t, database.Pool)

	deletion := lockRow(ctx, t, database.Pool, store.groups.lockGroup, turnGroup)
	var (
		deleted bool
		err     error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		deleted, err = turns.DeleteRole(ctx, every, turnRole)
	}()
	waitingOnALock(t, database.Pool, done)
	if err := deletion.Rollback(ctx); err != nil {
		t.Fatalf("pgx.Tx.Rollback() error = %v", err)
	}
	<-done

	if !deleted || err != nil {
		t.Errorf("DeleteRole(every domain) once the group's deletion rolled back = (%v, %v), want (true, nil)", deleted, err)
	}
	if held := heldUnder(ctx, t, store); len(held) != 0 {
		t.Errorf("ReadPolicy() after the role was deleted holds %v, want no role of the name for the group", held)
	}
}

// TestStore_writeUnderAParent_takesItsTurnWithItsDelete pins that a write under a role
// or a group reads its parent's row for key share before it writes, and reads it after
// the lock: where a transaction that holds the row deletes it, the write waits, finds
// the parent gone, answers with the parent's sentinel and writes nothing. A write that
// left the parent to the foreign key would not wait where it gives an ended row again,
// and would answer the database's error where the database's default level is
// repeatable read; one that read the parent before the lock would write under a parent
// that is gone.
func TestStore_writeUnderAParent_takesItsTurnWithItsDelete(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ended := accesstypes.Period{Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)}
	grant := policy.RoleGrant{Perm: "Read", Resource: "Wards"}

	tests := []struct {
		name string
		// group says the parent is turnGroup's row; otherwise it is turnRole's.
		group bool
		// endedRow writes the ended row the write gives again, before the parent is locked.
		endedRow func(ctx context.Context, store *Store) error
		write    func(ctx context.Context, store *Store) error
		want     error
	}{
		{
			name: "InsertCustomUserRole under its role",
			write: func(ctx context.Context, store *Store) error {
				return store.InsertCustomUserRole(ctx, turnScope, "alice", turnRole, accesstypes.Period{}, "", nil)
			},
			want: policy.ErrNoRole,
		},
		{
			name: "InsertCustomGroupRole under its role",
			write: func(ctx context.Context, store *Store) error {
				return store.InsertCustomGroupRole(ctx, turnGroup, turnScope, turnRole, accesstypes.Period{})
			},
			want: policy.ErrNoRole,
		},
		{
			name:  "InsertCustomGroupRole under its group",
			group: true,
			write: func(ctx context.Context, store *Store) error {
				return store.InsertCustomGroupRole(ctx, turnGroup, turnScope, turnRole, accesstypes.Period{})
			},
			want: policy.ErrNoGroup,
		},
		{
			name:  "InsertGroupMember of a new member",
			group: true,
			write: func(ctx context.Context, store *Store) error {
				return store.InsertGroupMember(ctx, turnGroup, "alice", accesstypes.Period{})
			},
			want: policy.ErrNoGroup,
		},
		{
			name:  "InsertGroupMember of an ended member given again",
			group: true,
			endedRow: func(ctx context.Context, store *Store) error {
				return store.InsertGroupMember(ctx, turnGroup, "alice", ended)
			},
			write: func(ctx context.Context, store *Store) error {
				return store.InsertGroupMember(ctx, turnGroup, "alice", accesstypes.Period{})
			},
			want: policy.ErrNoGroup,
		},
		{
			name:  "InsertGroupRole of a new role",
			group: true,
			write: func(ctx context.Context, store *Store) error {
				return store.InsertGroupRole(ctx, turnGroup, turnScope, turnRole, accesstypes.Period{})
			},
			want: policy.ErrNoGroup,
		},
		{
			name:  "InsertGroupRole of an ended role given again",
			group: true,
			endedRow: func(ctx context.Context, store *Store) error {
				return store.InsertGroupRole(ctx, turnGroup, turnScope, turnRole, ended)
			},
			write: func(ctx context.Context, store *Store) error {
				return store.InsertGroupRole(ctx, turnGroup, turnScope, turnRole, accesstypes.Period{})
			},
			want: policy.ErrNoGroup,
		},
		{
			name: "InsertGrant",
			write: func(ctx context.Context, store *Store) error {
				return store.InsertGrant(ctx, turnScope, turnRole, grant.Perm, grant.Resource, grant.Field, grant.Condition)
			},
			want: policy.ErrNoRole,
		},
		{
			name: "ChangeGrants with an addition",
			write: func(ctx context.Context, store *Store) error {
				return store.ChangeGrants(ctx, turnScope, turnRole, nil, []policy.RoleGrant{grant})
			},
			want: policy.ErrNoRole,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store, database := prepareStore(ctx, t)
			turnRoleAndGroup(ctx, t, store)
			if tt.endedRow != nil {
				if err := tt.endedRow(ctx, store); err != nil {
					t.Fatalf("writing the ended row: error = %v", err)
				}
			}
			turns := repeatableReadStore(ctx, t, database.Pool)

			parent := roleParent(store)
			if tt.group {
				parent = groupParent(store)
			}
			deletion := lockRow(ctx, t, database.Pool, parent.lock, parent.args...)
			var err error
			done := make(chan struct{})
			go func() {
				defer close(done)
				err = tt.write(ctx, turns)
			}()
			waitingOnALock(t, database.Pool, done)
			for _, sql := range parent.deletes {
				if _, err := deletion.Exec(ctx, sql, parent.args...); err != nil {
					t.Fatalf("pgx.Tx.Exec() delete the parent error = %v", err)
				}
			}
			if err := deletion.Commit(ctx); err != nil {
				t.Fatalf("pgx.Tx.Commit() error = %v", err)
			}
			<-done

			if !errors.Is(err, tt.want) {
				t.Errorf("the write under a parent deleted while it waited: error = %v, want %v", err, tt.want)
			}
			if held := heldUnder(ctx, t, store); len(held) != 0 {
				t.Errorf("ReadPolicy() after the refusal holds %v, want nothing under the role or the group", held)
			}
		})
	}
}

// turnRoleAndGroup writes turnRole and turnGroup, both in turnScope.
func turnRoleAndGroup(ctx context.Context, t *testing.T, store *Store) {
	t.Helper()
	if err := store.InsertRole(ctx, turnScope, turnRole, "Keeps the wards."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	group := &policy.GroupRow{Group: policy.Group{ID: turnGroup, Scope: turnScope}, Name: "Wards", Description: "Who keeps the wards."}
	if taken, err := store.InsertGroup(ctx, group); err != nil || taken {
		t.Fatalf("InsertGroup() = (%v, %v), want it written", taken, err)
	}
}

// parentRow is the row a turn test locks and deletes: the statement that locks it for
// update, the statements that delete it with the rows under it, and the arguments of
// each.
type parentRow struct {
	lock    string
	deletes []string
	args    []any
}

// roleParent is turnRole's row in turnScope. Its grants go with it by the grants
// table's foreign key.
func roleParent(store *Store) parentRow {
	kind, axis, domain := policy.ScopeColumns(turnScope)

	return parentRow{lock: roleRowSQL(store, "update"), deletes: []string{store.sqlDeleteRole}, args: []any{kind, axis, domain, turnRole}}
}

// groupParent is turnGroup's row, deleted after its members and roles.
func groupParent(store *Store) parentRow {
	return parentRow{lock: store.groups.lockGroup, deletes: append(slices.Clone(store.groups.deleteHeld), store.groups.deleteGroup), args: []any{turnGroup}}
}

// roleRowSQL is a statement that reads a role's row, as the roles table keys it, and
// locks it at the strength named: "key share", as a write under the role does, or
// "update", as the role's deletion does.
func roleRowSQL(store *Store, strength string) string {
	return fmt.Sprintf(`select 1 from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 for %s`, store.names.roles, strength)
}

// heldUnder lists what the store holds under turnRole or turnGroup: a membership of
// turnRole, a role or a member of turnGroup, a grant of turnRole.
func heldUnder(ctx context.Context, t *testing.T, store *Store) []string {
	t.Helper()
	records, err := store.ReadPolicy(ctx)
	if err != nil {
		t.Fatalf("ReadPolicy() error = %v", err)
	}
	var held []string
	for i := range records.Memberships {
		if m := &records.Memberships[i]; m.Role == turnRole || m.Member.Name == string(turnGroup) {
			held = append(held, fmt.Sprintf("%s holds %s in %s", m.Member.Name, m.Role, m.Scope))
		}
	}
	for i := range records.GroupMembers {
		if m := &records.GroupMembers[i]; m.Group == turnGroup {
			held = append(held, fmt.Sprintf("%s is in %s", m.User, m.Group))
		}
	}
	for i := range records.Grants {
		if g := &records.Grants[i]; g.Subject.Name == string(turnRole) {
			held = append(held, fmt.Sprintf("%s may %s %s", g.Subject.Name, g.Perm, g.Resource))
		}
	}

	return held
}

// repeatableReadStore is a store on a pool of its own to the test's database whose
// sessions begin every transaction at repeatable read: the database's default level is
// not the store's to count on, so a transaction of the store that leaves its level to
// the database reads under a snapshot taken before it waited, and fails the test.
func repeatableReadStore(ctx context.Context, t *testing.T, database *pgxpool.Pool) *Store {
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

// lockRow begins a transaction on the test's database that runs the locking read of one
// row given, and returns it: a statement of the store whose lock on the row conflicts
// waits until the transaction ends. A transaction the test has not ended is rolled back.
func lockRow(ctx context.Context, t *testing.T, database *pgxpool.Pool, sql string, args ...any) pgx.Tx {
	t.Helper()
	tx, err := database.Begin(ctx)
	if err != nil {
		t.Fatalf("pgxpool.Pool.Begin() error = %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	})
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		t.Fatalf("pgx.Tx.Exec() lock the row error = %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("pgx.Tx.Exec() lock the row read %d rows, want 1", tag.RowsAffected())
	}

	return tx
}

// waitingOnALock waits until a session of the test's database waits on a lock, as a
// statement of the store does while the test holds a lock it needs. It fails the test
// where done is closed first: the store answered without waiting for the lock.
func waitingOnALock(t *testing.T, database *pgxpool.Pool, done <-chan struct{}) {
	t.Helper()
	ctx := t.Context()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatal("the store answered before it was seen waiting on a lock")
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

// TestStore_DeleteGroup_takesItsTurnWithAMember pins that a group's deletion locks the
// group's row before it deletes what the group holds, and deletes after the lock: a
// member written by a transaction still open when the delete starts is taken with the
// group once it commits. A delete that deleted the members before the lock, or under a
// snapshot taken before it, would miss the member, and the member's foreign key would
// refuse the group's delete.
func TestStore_DeleteGroup_takesItsTurnWithAMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, database := prepareStore(ctx, t)
	group := &policy.GroupRow{Group: policy.Group{ID: turnGroup, Scope: turnScope}, Name: "Wards", Description: "Who keeps the wards."}
	if taken, err := store.InsertGroup(ctx, group); err != nil || taken {
		t.Fatalf("InsertGroup() = (%v, %v), want it written", taken, err)
	}
	turns := repeatableReadStore(ctx, t, database.Pool)

	member, err := database.Begin(ctx)
	if err != nil {
		t.Fatalf("pgxpool.Pool.Begin() error = %v", err)
	}
	t.Cleanup(func() {
		_ = member.Rollback(context.WithoutCancel(ctx))
	})
	if _, err := member.Exec(ctx, store.groups.insertMember, turnGroup, "alice", nil, nil); err != nil {
		t.Fatalf("pgx.Tx.Exec() insert member error = %v", err)
	}
	var (
		deleted   bool
		deleteErr error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		deleted, deleteErr = turns.DeleteGroup(ctx, turnGroup)
	}()
	waitingOnALock(t, database.Pool, done)
	if err := member.Commit(ctx); err != nil {
		t.Fatalf("pgx.Tx.Commit() error = %v", err)
	}
	<-done

	if !deleted || deleteErr != nil {
		t.Errorf("DeleteGroup() of a group given a member while it waited = (%v, %v), want it deleted with the member", deleted, deleteErr)
	}
	if _, found, err := store.Group(ctx, turnGroup); err != nil || found {
		t.Errorf("Group() after the delete = (%v, %v), want the group gone", found, err)
	}
	if held := heldUnder(ctx, t, store); len(held) != 0 {
		t.Errorf("ReadPolicy() after the delete holds %v, want nothing of the group", held)
	}
}
