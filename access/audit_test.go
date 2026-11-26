package access

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
)

// Test_userManager_directorySync pins the records of the role changes a directory asks
// for at a sign-in: where the context carries the directory's sync, each role given and
// each role taken is handed to the store as one record, done to the person the sync
// names, by the directory sign-in process and the directory, naming the one role, where
// it is held and the tenant by the name the sync gives it (its key where it gives
// none). A role taken that the person did not hold records nothing, and a change with
// no sync in its context records nothing.
func Test_userManager_directorySync(t *testing.T) {
	t.Parallel()

	m, store := seededManager(t)
	const userID = "0190b000-0000-7000-8000-0000000016a1"
	sync := auditlog.DirectorySync{
		Directory: "Acme", UserID: userID, Username: "pat",
		TenantName: func(tenant accesstypes.Domain) string {
			if tenant == "tenant1" {
				return "Tenant One"
			}

			return ""
		},
	}
	ctx := auditlog.WithDirectorySync(context.Background(), sync)

	if err := m.AddUserRoles(ctx, tenant1Policy, userID, "Editor", "Viewer"); err != nil {
		t.Fatalf("AddUserRoles() error = %v", err)
	}
	if err := m.DeleteUserRoles(ctx, tenant1Policy, userID, "Editor"); err != nil {
		t.Fatalf("DeleteUserRoles() error = %v", err)
	}
	if err := m.DeleteUserRoles(ctx, tenant2Policy, userID, "Viewer"); err != nil {
		t.Fatalf("DeleteUserRoles() of a role the person does not hold error = %v", err)
	}
	if err := m.AddUserRoles(context.Background(), tenant2Policy, userID, "Viewer"); err != nil {
		t.Fatalf("AddUserRoles() with no sync error = %v", err)
	}

	row := func(act auditlog.Act, role string) *auditlog.Record {
		return &auditlog.Record{
			Act: act, Directory: "Acme", SubjectKind: auditlog.SubjectAccount, SubjectID: userID, SubjectName: "pat",
			TenantID: "tenant1", TenantName: "Tenant One", RoleNames: []string{role}, RoleScope: auditlog.RoleScopeTenant,
		}
	}
	want := []*auditlog.Record{row(auditlog.ActRoleGiven, "Editor"), row(auditlog.ActRoleGiven, "Viewer"), row(auditlog.ActRoleTaken, "Editor")}
	got := store.recordsWritten()
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(auditlog.Record{}, "Source")); diff != "" {
		t.Fatalf("the records the store was handed (-want +got):\n%s", diff)
	}
	for _, r := range got {
		if diff := cmp.Diff(map[string]any{"Process": auditlog.ProcessDirectorySignIn}, r.Source.Columns()); diff != "" {
			t.Errorf("who acted, in the record of %s (-want +got):\n%s", r.Act, diff)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("the record of %s does not validate: %v", r.Act, err)
		}
		stored := auditlog.Stored{Directory: r.Directory}
		if who := stored.Sentences("").Who; who != "the directory Acme" {
			t.Errorf("the record of %s reads as done by %q, want the directory Acme", r.Act, who)
		}
	}
}

// Test_directoryRoleRecord_tenantName pins the tenant a record names: by the name the
// sync gives it, by its key where the sync gives none or has no namer, and none for a
// role held in the whole application or in every tenant.
func Test_directoryRoleRecord_tenantName(t *testing.T) {
	t.Parallel()

	named := func(accesstypes.Domain) string { return "Tenant One" }
	unnamed := func(accesstypes.Domain) string { return "" }
	tests := []struct {
		name      string
		namer     func(accesstypes.Domain) string
		scope     accesstypes.PolicyScope
		wantScope auditlog.RoleScope
		wantID    string
		wantName  string
	}{
		{name: "a tenant the sync names", namer: named, scope: tenant1Policy, wantScope: auditlog.RoleScopeTenant, wantID: "tenant1", wantName: "Tenant One"},
		{name: "a tenant the sync names nothing", namer: unnamed, scope: tenant1Policy, wantScope: auditlog.RoleScopeTenant, wantID: "tenant1", wantName: "tenant1"},
		{name: "a sync with no namer", scope: tenant1Policy, wantScope: auditlog.RoleScopeTenant, wantID: "tenant1", wantName: "tenant1"},
		{name: "the whole application", namer: named, scope: accesstypes.GlobalPolicyScope(), wantScope: auditlog.RoleScopeGlobal},
		{name: "every tenant", namer: named, scope: accesstypes.EveryDomainPolicyScope(), wantScope: auditlog.RoleScopeEveryTenant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sync := auditlog.DirectorySync{Directory: "Acme", UserID: "u", Username: "pat", TenantName: tt.namer}
			r := directoryRoleRecord(&sync, auditlog.ActRoleGiven, tt.scope, "Editor")
			if r.RoleScope != tt.wantScope || r.TenantID != tt.wantID || r.TenantName != tt.wantName {
				t.Errorf("the record holds the place %q in %q (%q), want %q in %q (%q)", r.RoleScope, r.TenantID, r.TenantName, tt.wantScope, tt.wantID, tt.wantName)
			}
		})
	}
}
