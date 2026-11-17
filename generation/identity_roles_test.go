package generation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test_reservedRoleIn pins what the generator reads of an auth's role file: a role under
// the name the framework keeps for the Super Administrator role is refused, in the global
// roles or a tenant's and in any case, and nothing else of the file is judged.
func Test_reservedRoleIn(t *testing.T) {
	t.Parallel()

	const auth = "example.com/beacon/pkg/auth/staff"
	tests := []struct {
		name string
		// roles is the role file's content; empty writes no file.
		roles   string
		wantErr string
	}{
		{
			name:  "a role file that defines its own roles",
			roles: `{"roles": {"global": [{"name": "Administrator", "description": "Manages people.", "permissions": {}}], "domain": [{"name": "Auditor", "description": "Reads.", "permissions": {}}]}}`,
		},
		{
			name:    "a global role under the reserved name",
			roles:   `{"roles": {"global": [{"name": "SuperAdministrator", "description": "Runs everything.", "permissions": {}}]}}`,
			wantErr: "roles.json defines a role named SuperAdministrator, and the name is reserved: every auth has the Super Administrator role, which the framework defines",
		},
		{
			name:    "a tenant's role under the name in another case",
			roles:   `{"roles": {"domain": [{"name": "Superadministrator", "description": "Runs a tenant.", "permissions": {}}]}}`,
			wantErr: "defines a role named Superadministrator, and the name is reserved",
		},
		{
			name: "no role file yet",
		},
		{
			name:  "a file that does not parse is the engine's to refuse",
			roles: `{"roles": {"global": [`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file := filepath.Join(t.TempDir(), identityRolesFile)
			if tt.roles != "" {
				if err := os.WriteFile(file, []byte(tt.roles), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			err := reservedRoleIn(auth, file)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("reservedRoleIn() error = %v, want none", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), auth)):
				t.Errorf("reservedRoleIn() error = %v, want one naming the auth and containing %q", err, tt.wantErr)
			}
		})
	}
}

// Test_resourceGenerator_moduleDir pins where the generator looks for an auth package:
// under the main module, by its import path, and nowhere for a package of another module.
func Test_resourceGenerator_moduleDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		module, importPath string
		wantDir            string
		wantOK             bool
	}{
		{name: "a package of the module", module: "example.com/beacon", importPath: "example.com/beacon/pkg/auth/staff", wantDir: filepath.Join("pkg", "auth", "staff"), wantOK: true},
		{name: "the module's root package", module: "example.com/beacon", importPath: "example.com/beacon", wantDir: ".", wantOK: true},
		{name: "a package of another module", module: "example.com/beacon", importPath: "example.com/other/pkg/auth/staff"},
		{name: "a module whose path only starts the same", module: "example.com/beacon", importPath: "example.com/beacons/pkg/auth/staff"},
		{name: "no module path known", importPath: "example.com/beacon/pkg/auth/staff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &resourceGenerator{client: &client{modulePathRoot: tt.module}}
			dir, ok := r.moduleDir(tt.importPath)
			if dir != tt.wantDir || ok != tt.wantOK {
				t.Errorf("moduleDir(%q) = %q, %v, want %q, %v", tt.importPath, dir, ok, tt.wantDir, tt.wantOK)
			}
		})
	}
}
