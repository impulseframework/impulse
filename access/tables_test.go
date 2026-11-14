package access

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestTablesWithPrefix pins the one rule a policy store's tables are named by, and the
// names it refuses: both stores must accept the same ones, and a name is never quoted
// into a statement.
func TestTablesWithPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prefix  string
		store   string
		want    Tables
		wantErr bool
	}{
		{
			name:   "the default prefix and the empty store name",
			prefix: DefaultTablePrefix,
			want:   Tables{Roles: "AccessRoles", UserRoles: "AccessUserRoles", RoleGrants: "AccessRoleGrants", UserRolesByUser: "AccessUserRolesByUser"},
		},
		{
			name:   "an application's prefix",
			prefix: "Crew",
			want:   Tables{Roles: "CrewRoles", UserRoles: "CrewUserRoles", RoleGrants: "CrewRoleGrants", UserRolesByUser: "CrewUserRolesByUser"},
		},
		{
			name:   "a store name follows the prefix",
			prefix: "Access", store: "Billing",
			want: Tables{Roles: "AccessBillingRoles", UserRoles: "AccessBillingUserRoles", RoleGrants: "AccessBillingRoleGrants", UserRolesByUser: "AccessBillingUserRolesByUser"},
		},
		{name: "an empty prefix is refused", prefix: "", wantErr: true},
		{name: "a prefix that starts with a digit is refused", prefix: "1Crew", wantErr: true},
		{name: "a prefix with a space is refused", prefix: "drop table", wantErr: true},
		{name: "a store name with a space is refused", prefix: "Access", store: "drop table", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := TablesWithPrefix(tt.prefix, tt.store)
			if (err != nil) != tt.wantErr {
				t.Fatalf("TablesWithPrefix(%q, %q) error = %v, wantErr %v", tt.prefix, tt.store, err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("TablesWithPrefix(%q, %q) (-want +got):\n%s", tt.prefix, tt.store, diff)
			}
		})
	}
}
