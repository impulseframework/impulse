package deploy_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/auth/members"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/auth/staff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/router"
)

// TestFirstAdministratorRoles_areInTheRoleFile holds the foothold to the role file: every
// role the first administrator's invitation gives is one the auth's file defines, at the
// scope it is held in, so the invitation names nothing its acceptance could not give.
func TestFirstAdministratorRoles_areInTheRoleFile(t *testing.T) {
	t.Parallel()

	staffGlobal, staffEveryDomain := staff.FirstAdministratorRoles()
	_, membersEveryDomain := members.FirstAdministratorRoles()
	tests := []struct {
		name  string
		roles access.RoleFile
		// global and domain are the roles given in the global partition and in every
		// domain.
		global, domain []accesstypes.Role
	}{
		{name: "staff", roles: staff.Roles(), global: staffGlobal, domain: staffEveryDomain},
		{name: "members", roles: members.Roles(), domain: membersEveryDomain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := tt.roles.Parse()
			if err != nil {
				t.Fatalf("access.RoleFile.Parse() error = %v", err)
			}
			defined := func(roles []*access.Role) map[accesstypes.Role]bool {
				names := make(map[accesstypes.Role]bool, len(roles))
				for _, r := range roles {
					names[r.Name] = true
				}

				return names
			}
			global, domain := defined(parsed.Roles.Global), defined(parsed.Roles.Domain)
			for _, r := range tt.global {
				if !global[r] {
					t.Errorf("%s is given in the global partition and the role file defines no global role by that name", r)
				}
			}
			for _, r := range tt.domain {
				if !domain[r] {
					t.Errorf("%s is given in every domain and the role file defines no domain role by that name", r)
				}
			}
		})
	}
}

// TestAdministratorRoles_areHeldByTheFirstAdministrator holds the foothold's two lists
// together: every role that counts as an administrator's, which the rule on the last
// administrator judges by, is one the first administrator's invitation gives, where it is
// held. So the foothold is an administrator, and never the person the rule passes over.
func TestAdministratorRoles_areHeldByTheFirstAdministrator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		administrator, first func() (global, everyDomain []accesstypes.Role)
	}{
		{name: "staff", administrator: staff.AdministratorRoles, first: staff.FirstAdministratorRoles},
		{name: "members", administrator: members.AdministratorRoles, first: members.FirstAdministratorRoles},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			global, everyDomain := tt.administrator()
			firstGlobal, firstEveryDomain := tt.first()
			for _, r := range global {
				if !slices.Contains(firstGlobal, r) {
					t.Errorf("%s counts as an administrator's role in the global partition and the first administrator's invitation does not give it", r)
				}
			}
			for _, r := range everyDomain {
				if !slices.Contains(firstEveryDomain, r) {
					t.Errorf("%s counts as an administrator's role in every domain and the first administrator's invitation does not give it", r)
				}
			}
		})
	}
}

// TestRoles_validateAgainstTheCollection runs each auth's embedded role file through the
// validation access.New performs before the engine opens (the grammar, every condition
// against the generated collection, each grant at its role's scope) and pins the warnings
// the deploy would print. Every expected set is empty: a warning here is accepted by
// adding its typed value (access.GrantWarning, access.ConcealingKeyWarning) to the row's
// wantWarnings, so the acceptance is recorded in code, or removed by fixing the grant it
// names. Each auth's file is validated with the others' beside it, since a subject set
// any auth's roles read is read. A new auth adds a row.
func TestRoles_validateAgainstTheCollection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		roles        access.RoleFile
		wantWarnings []access.Warning
	}{
		{name: staff.Name, roles: staff.Roles()},
		{name: members.Name, roles: members.Roles()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			roles, err := tt.roles.Parse()
			if err != nil {
				t.Fatalf("access.RoleFile.Parse() error = %v", err)
			}

			// The file is validated as its auth's: the check looks at the subject sets
			// that describe that auth's people, and at no other auth's.
			warnings, err := access.ValidateRoles(router.Collection(), roles, tt.name)
			if err != nil {
				t.Fatalf("access.ValidateRoles() error = %v", err)
			}
			if len(warnings) == 0 && len(tt.wantWarnings) == 0 {
				return
			}
			if !reflect.DeepEqual(tt.wantWarnings, warnings) {
				t.Errorf("access.ValidateRoles() warnings = %d, want %d:", len(warnings), len(tt.wantWarnings))
				for _, w := range warnings {
					t.Logf("got:  %s", w)
				}
				for _, w := range tt.wantWarnings {
					t.Logf("want: %s", w)
				}
			}
		})
	}
}
