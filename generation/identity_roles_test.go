package generation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test_refuseReservedRole pins that the generator writes no management for an auth
// whose role file defines a role under the name the framework keeps for the Super
// Administrator role, in the global roles or a tenant's and in any case, and that it
// reads the file where the auth package lives in the main module.
func Test_refuseReservedRole(t *testing.T) {
	const module = "example.com/beacon"
	tests := []struct {
		name string
		// importPath is the auth package, and roles its role file; empty writes none.
		importPath, roles string
		wantErr           string
	}{
		{
			name:       "a role file that defines its own roles",
			importPath: module + "/pkg/auth/staff",
			roles:      `{"roles": {"global": [{"name": "Administrator", "description": "Manages people.", "permissions": {}}], "domain": [{"name": "Auditor", "description": "Reads.", "permissions": {}}]}}`,
		},
		{
			name:       "a global role under the reserved name",
			importPath: module + "/pkg/auth/staff",
			roles:      `{"roles": {"global": [{"name": "SuperAdministrator", "description": "Runs everything.", "permissions": {}}]}}`,
			wantErr:    `Auth("example.com/beacon/pkg/auth/staff"): pkg/auth/staff/roles.json defines a role named SuperAdministrator, and the name is reserved`,
		},
		{
			name:       "a tenant's role under the name in another case",
			importPath: module + "/pkg/auth/staff",
			roles:      `{"roles": {"domain": [{"name": "Superadministrator", "description": "Runs a tenant.", "permissions": {}}]}}`,
			wantErr:    "defines a role named Superadministrator, and the name is reserved",
		},
		{
			name:       "no role file yet",
			importPath: module + "/pkg/auth/staff",
		},
		{
			name:       "a file that does not parse is the engine's to refuse",
			importPath: module + "/pkg/auth/staff",
			roles:      `{"roles": {"global": [`,
		},
		{
			name:       "an auth package of another module is not read",
			importPath: "example.com/other/pkg/auth/staff",
			roles:      `{"roles": {"global": [{"name": "SuperAdministrator", "description": "Another module's.", "permissions": {}}]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The file is read relative to the module root, the generator's directory.
			t.Chdir(t.TempDir())
			if tt.roles != "" {
				dir := filepath.Join("pkg", "auth", "staff")
				if err := os.MkdirAll(dir, 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, identityRolesFile), []byte(tt.roles), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			r := &resourceGenerator{client: &client{modulePathRoot: module}}

			err := r.refuseReservedRole(&identityInstance{importPath: tt.importPath})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("refuseReservedRole() error = %v, want none", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("refuseReservedRole() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}
