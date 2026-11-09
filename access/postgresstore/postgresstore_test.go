package postgresstore

import (
	"context"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/access/internal/storetest"
	"github.com/impulseframework/impulse/accesstypes"
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
	if err := adminStore.InsertUserRole(ctx, accesstypes.DomainPolicyScope("tenant1"), "alice", "Editor"); err != nil {
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
			`PRIMARY KEY ("Kind", "Axis", "Domain", "Role")`,
		}},
		{name: "user roles stand alone", stmt: 1, want: []string{
			`"Kind" TEXT NOT NULL`, `"Axis" TEXT NOT NULL`,
			`CONSTRAINT "AccessUserRolesKind" CHECK ("Kind" IN ('global', 'domain', 'every'))`,
			`PRIMARY KEY ("Kind", "Axis", "Domain", "Role", "User")`,
		}},
		{name: "user roles by user", stmt: 2, want: []string{`CREATE INDEX "AccessUserRolesByUser" ON "AccessUserRoles" ("User", "Kind", "Axis", "Domain")`}},
		{name: "role grants cascade from roles", stmt: 3, want: []string{
			`"Kind" TEXT NOT NULL`, `"Axis" TEXT NOT NULL`,
			`CONSTRAINT "AccessRoleGrantsKind" CHECK ("Kind" IN ('global', 'domain', 'every'))`,
			`PRIMARY KEY ("Kind", "Axis", "Domain", "Role", "Permission", "Resource", "Field", "Condition")`,
			`FOREIGN KEY ("Kind", "Axis", "Domain", "Role") REFERENCES "AccessRoles" ("Kind", "Axis", "Domain", "Role") ON DELETE CASCADE`,
		}},
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
