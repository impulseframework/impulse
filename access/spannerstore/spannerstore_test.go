package spannerstore

import (
	"context"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/access/internal/storetest"
	"github.com/impulseframework/impulse/accesstypes"
	"google.golang.org/grpc/codes"
)

func TestNew_naming(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		opts      []Option
		wantTable string
		wantErr   bool
	}{
		{name: "defaults", wantTable: "AccessRoles"},
		{name: "store name", opts: []Option{WithStore("AdminPortal")}, wantTable: "AccessAdminPortalRoles"},
		{name: "prefix override", opts: []Option{WithPrefix("Acl"), WithStore("Portal")}, wantTable: "AclPortalRoles"},
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

	store, _ := prepareStore(ctx, t)
	storetest.Run(t, store)
}

func TestStore_conformance_namedStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store, _ := prepareStore(ctx, t, WithStore("AdminPortal"))
	storetest.Run(t, store)
}

// TestStore_isolation proves separate stores in one database cannot see each
// other's rows: cross-store leakage is structurally impossible, not filtered.
func TestStore_isolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	adminStore, db := prepareStore(ctx, t, WithStore("AdminPortal"))
	partnerStore, err := New(db.Client, WithStore("PartnerPortal"))
	if err != nil {
		t.Fatalf("spannerstore.New(): %v", err)
	}
	if err := applyDDL(t, db, partnerStore); err != nil {
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

// TestStore_readsUnderATransactionsContext pins what the store's reads do under the
// context of a read-write transaction of the application's: the Spanner client refuses
// them (FailedPrecondition, "Cloud Spanner does not support nested transactions"),
// whichever client the transaction is on, since it marks the context it hands the
// transaction's function. The store reads on a client of its own, outside any
// transaction, so a caller reads the policy and lists roles before it opens one. It is
// why the permission engine never reads the store under a caller's context: a check
// made inside a transaction is answered from a reload on the engine's own
// (TestEngine_checksInsideAnOpenTransaction).
func TestStore_readsUnderATransactionsContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store, db := prepareStore(ctx, t)
	scope := accesstypes.DomainPolicyScope("tenant1")
	if err := store.InsertRole(ctx, scope, "Editor", "Edits the records of the tenant."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	if _, err := store.ReadPolicy(ctx); err != nil {
		t.Fatalf("ReadPolicy() outside a transaction error = %v", err)
	}

	var readErr, listErr error
	if _, err := db.ReadWriteTransaction(ctx, func(ctx context.Context, _ *spanner.ReadWriteTransaction) error {
		_, readErr = store.ReadPolicy(ctx)
		_, listErr = store.ListRoles(ctx, scope)

		return nil
	}); err != nil {
		t.Fatalf("ReadWriteTransaction() error = %v", err)
	}
	for name, err := range map[string]error{"ReadPolicy()": readErr, "ListRoles()": listErr} {
		if spanner.ErrCode(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "does not support nested transactions") {
			t.Errorf("%s under a transaction's context error = %v, want the client's refusal of a nested transaction (FailedPrecondition)", name, err)
		}
	}
}

// TestEngine_checksInsideAnOpenTransaction runs the engine's suite on Spanner: a role
// is taken away, and the first check after the engine learns of it is made inside an
// open read-write transaction of another request, under the context the Spanner client
// refuses a read of the store's under. The check sees the role gone, since the engine
// reloads on its own context, beside the transaction and not under it.
func TestEngine_checksInsideAnOpenTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store, database := prepareStore(ctx, t)
	storetest.RunEngine(t, store, openTransaction(database))
}

// TestDDL_keys pins the shape of the tables: Kind leads every key and is
// checked against the three values the store writes; Axis is the second key
// column of every table, declared NOT NULL and written "" for the default
// axis — a Spanner primary key cannot change after the fact, so the key order
// shipped here is what lets a single-axis application's rows stay correct
// unchanged when it later declares an axis; UserRoles stands alone, indexed by
// user, since a membership names a role with no row; and RoleGrants stays
// interleaved in Roles so a custom role's grants go with it.
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
			"Kind STRING(16) NOT NULL", "Axis STRING(128) NOT NULL", "Description STRING(MAX) NOT NULL",
			"CONSTRAINT AccessRolesKind CHECK (Kind IN ('global', 'domain', 'every'))",
			"PRIMARY KEY (Kind, Axis, DomainId, Role)",
		}},
		{name: "user roles stand alone", stmt: 1, want: []string{
			"Kind STRING(16) NOT NULL", "Axis STRING(128) NOT NULL",
			"CONSTRAINT AccessUserRolesKind CHECK (Kind IN ('global', 'domain', 'every'))",
			"PRIMARY KEY (Kind, Axis, DomainId, Role, UserId)",
		}},
		{name: "user roles by user", stmt: 2, want: []string{"CREATE INDEX AccessUserRolesByUserId ON AccessUserRoles (UserId, Kind, Axis, DomainId)"}},
		{name: "role grants interleaved in roles", stmt: 3, want: []string{
			"Kind STRING(16) NOT NULL", "Axis STRING(128) NOT NULL",
			"CONSTRAINT AccessRoleGrantsKind CHECK (Kind IN ('global', 'domain', 'every'))",
			"PRIMARY KEY (Kind, Axis, DomainId, Role, Permission, Resource, Field, Condition)",
			"INTERLEAVE IN PARENT AccessRoles ON DELETE CASCADE",
		}},
		{name: "groups are kept globally or in one domain", stmt: 4, want: []string{
			"Id STRING(36) NOT NULL", "Name STRING(128) NOT NULL", "Description STRING(MAX) NOT NULL",
			"CONSTRAINT AccessGroupsKind CHECK (Kind IN ('global', 'domain'))",
			"PRIMARY KEY (Id)",
		}},
		{name: "a group's name is one group's where it is kept", stmt: 5, want: []string{"CREATE UNIQUE INDEX AccessGroupsByName ON AccessGroups (Kind, Axis, DomainId, Name)"}},
		{name: "a member needs the group", stmt: 6, want: []string{
			"CONSTRAINT AccessGroupMembersGroup FOREIGN KEY (GroupId) REFERENCES AccessGroups (Id)",
			"PRIMARY KEY (GroupId, UserId)",
		}},
		{name: "group members by user", stmt: 7, want: []string{"CREATE INDEX AccessGroupMembersByUserId ON AccessGroupMembers (UserId, GroupId)"}},
		{name: "a group's role needs the group", stmt: 8, want: []string{
			"CONSTRAINT AccessGroupRolesKind CHECK (Kind IN ('global', 'domain', 'every'))",
			"CONSTRAINT AccessGroupRolesGroup FOREIGN KEY (GroupId) REFERENCES AccessGroups (Id)",
			"PRIMARY KEY (GroupId, Kind, Axis, DomainId, Role)",
		}},
		{name: "group roles by where they are held", stmt: 9, want: []string{"CREATE INDEX AccessGroupRolesByScope ON AccessGroupRoles (Kind, Axis, DomainId, Role)"}},
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
