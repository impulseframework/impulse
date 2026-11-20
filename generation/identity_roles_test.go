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

// Test_retiredGrantsIn pins the refusal of a role file that still lets a role run a method
// the generated management no longer has: the direct create of an account and the
// temporary password, in the application and in a tenant. The refusal names the role, the
// method and what took its place, and a method the application declares under the name
// itself is its own.
func Test_retiredGrantsIn(t *testing.T) {
	t.Parallel()

	const auth = "example.com/beacon/pkg/auth/staff"
	tenanted := &identityInstance{auth: "Staff", tenant: &identityTenant{name: "Site"}}
	plain := &identityInstance{auth: "Staff"}
	tests := []struct {
		name string
		inst *identityInstance
		// roles is the role file's content; empty writes no file.
		roles    string
		declared map[string]bool
		wantErrs []string
	}{
		{
			name:  "a role file that invites and mails reset links",
			inst:  tenanted,
			roles: `{"roles": {"global": [{"name": "Administrator", "description": "Manages people.", "permissions": {"Execute": [{"resource": "InviteStaffUser"}, {"resource": "SendStaffUserPasswordResetLink"}]}}], "domain": [{"name": "SiteManager", "description": "Manages a site.", "permissions": {"Execute": [{"resource": "InviteStaffSiteUser"}]}}]}}`,
		},
		{
			name:  "a global role that still creates accounts",
			inst:  plain,
			roles: `{"roles": {"global": [{"name": "Administrator", "description": "Manages people.", "permissions": {"Execute": [{"resource": "CreateStaffUser"}]}}]}}`,
			wantErrs: []string{
				"roles.json: the role Administrator is granted Execute on CreateStaffUser, and the generated management has no such method any more: an account is added by an invitation, and nobody creates one for someone else; grant InviteStaffUser to the role instead, or remove the grant",
			},
		},
		{
			name:  "a global role that still sets a temporary password, on one of its fields",
			inst:  plain,
			roles: `{"roles": {"global": [{"name": "Administrator", "description": "Manages people.", "permissions": {"Execute": [{"resource": "SetStaffTemporaryPassword", "fields": ["password"]}]}}]}}`,
			wantErrs: []string{
				"the role Administrator is granted Execute on SetStaffTemporaryPassword, and the generated management has no such method any more: a person chooses their own password through a reset link, and nobody sets one for someone else; grant SendStaffUserPasswordResetLink to the role instead, or remove the grant",
			},
		},
		{
			name:  "a tenant's role that still does both, every grant in one refusal",
			inst:  tenanted,
			roles: `{"roles": {"domain": [{"name": "SiteManager", "description": "Manages a site.", "permissions": {"Execute": [{"resource": "CreateStaffSiteUser"}, {"resource": "SetStaffSiteTemporaryPassword"}]}}]}}`,
			wantErrs: []string{
				"the role SiteManager is granted Execute on CreateStaffSiteUser, and the generated management has no such method any more: an account is added by an invitation, and nobody creates one for someone else; grant InviteStaffSiteUser to the role instead, or remove the grant",
				"the role SiteManager is granted Execute on SetStaffSiteTemporaryPassword, and the generated management has no such method any more: a person chooses their own password through a reset link, and nobody sets one for someone else; grant SendStaffSiteUserPasswordResetLink to the role instead, or remove the grant",
			},
		},
		{
			name:     "a method the application declares under the name is its own",
			inst:     plain,
			roles:    `{"roles": {"global": [{"name": "Administrator", "description": "Manages people.", "permissions": {"Execute": [{"resource": "CreateStaffUser"}]}}]}}`,
			declared: map[string]bool{"CreateStaffUser": true},
		},
		{
			name:  "a tenant's name means nothing to an auth without tenants",
			inst:  plain,
			roles: `{"roles": {"global": [{"name": "Administrator", "description": "Manages people.", "permissions": {"Execute": [{"resource": "CreateStaffSiteUser"}]}}]}}`,
		},
		{
			name: "no role file yet",
			inst: tenanted,
		},
		{
			name:  "a file that does not parse is the engine's to refuse",
			inst:  tenanted,
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

			err := retiredGrantsIn(auth, file, tt.inst.retiredMethods(), tt.declared)
			if len(tt.wantErrs) == 0 {
				if err != nil {
					t.Errorf("retiredGrantsIn() error = %v, want none", err)
				}

				return
			}
			if err == nil {
				t.Fatalf("retiredGrantsIn() error = nil, want one containing %q", tt.wantErrs)
			}
			if !strings.Contains(err.Error(), auth) {
				t.Errorf("retiredGrantsIn() error = %v, want one naming the auth %q", err, auth)
			}
			for _, want := range tt.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("retiredGrantsIn() error = %v, want one containing %q", err, want)
				}
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
