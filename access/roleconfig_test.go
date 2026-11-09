// deployment provides the utilities to bootstrap the application with preset configuration
package access

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

func rows(triples ...string) grantSet {
	if len(triples)%3 != 0 {
		panic("rows takes (permission, resource, condition) triples")
	}
	set := make(grantSet)
	for len(triples) >= 3 {
		perm, res, condition := triples[0], triples[1], triples[2]
		set.add(accesstypes.Permission(perm), accesstypes.Resource(res), condition)
		triples = triples[3:]
	}

	return set
}

// Test_validateRoleNames pins the one grammar a role name follows — letters
// and digits starting with a capital letter — and the declaration rules:
// once, at one scope, no two names differing only in case.
func Test_validateRoleNames(t *testing.T) {
	t.Parallel()

	role := func(name accesstypes.Role) *Role { return &Role{Name: name, Description: aDescription} }

	tests := []struct {
		name    string
		roles   ScopedRoles
		wantErr string
	}{
		{
			name:  "distinct names at each scope pass",
			roles: ScopedRoles{Global: []*Role{role("VendorManager")}, Domain: []*Role{role("Reader")}},
		},
		{
			name:    "a name declared twice in one list is rejected",
			roles:   ScopedRoles{Domain: []*Role{role("Reader"), role("Reader")}},
			wantErr: "declared twice",
		},
		{
			name:    "a name declared at both scopes is rejected",
			roles:   ScopedRoles{Global: []*Role{role("Reader")}, Domain: []*Role{role("Reader")}},
			wantErr: "exactly one scope",
		},
		{
			name:  "Administrator is an ordinary name and passes",
			roles: ScopedRoles{Global: []*Role{role("Administrator")}},
		},
		{
			name:    "two names differing only in case in one list are rejected",
			roles:   ScopedRoles{Domain: []*Role{role("Pilot"), role("PIlot")}},
			wantErr: "differ only in case",
		},
		{
			name:  "a name with digits after its capital letter passes",
			roles: ScopedRoles{Domain: []*Role{role("Tier2Reader")}},
		},
		{
			name:    "an underscore is outside the grammar",
			roles:   ScopedRoles{Global: []*Role{role("Administrator_Global")}},
			wantErr: "role Administrator_Global: a role name is letters and digits starting with a capital letter, such as Administrator or TenantAdministrator; the scope is never part of the name",
		},
		{
			name:    "a lowercase start is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("reader")}},
			wantErr: "role reader: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "a space is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("Tenant Administrator")}},
			wantErr: "role Tenant Administrator: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "a dot is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("Reader.Domain")}},
			wantErr: "role Reader.Domain: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "a hyphen is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("Read-Only")}},
			wantErr: "role Read-Only: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "a digit start is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("2ndReader")}},
			wantErr: "role 2ndReader: a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "the empty name is outside the grammar",
			roles:   ScopedRoles{Domain: []*Role{role("")}},
			wantErr: "a role name is letters and digits starting with a capital letter",
		},
		{
			name:    "two names differing only in case across the scopes are rejected",
			roles:   ScopedRoles{Global: []*Role{role("Steward")}, Domain: []*Role{role("STEWARD")}},
			wantErr: "differ only in case",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateRoleNames(tt.roles)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRoleNames() error = %v, want nil", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateRoleNames() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// Test_validateRoleDescriptions pins that every role says what it is for: a
// description that is empty or whitespace alone is refused, naming the role.
func Test_validateRoleDescriptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		roles   ScopedRoles
		wantErr string
	}{
		{
			name:  "described roles at each scope pass",
			roles: ScopedRoles{Global: []*Role{{Name: "Steward", Description: "Runs the global method."}}, Domain: []*Role{{Name: "Reader", Description: "Reads widgets."}}},
		},
		{
			name:    "an empty description is refused",
			roles:   ScopedRoles{Domain: []*Role{{Name: "Reader"}}},
			wantErr: "role Reader: a role carries a description, one or two plain sentences saying what the role is for",
		},
		{
			name:    "a whitespace description is refused",
			roles:   ScopedRoles{Global: []*Role{{Name: "Steward", Description: " \n\t"}}},
			wantErr: "role Steward: a role carries a description",
		},
		{
			name:    "the first undescribed role is named, global roles first",
			roles:   ScopedRoles{Global: []*Role{{Name: "Steward", Description: "Runs the global method."}}, Domain: []*Role{{Name: "Reader", Description: "Reads widgets."}, {Name: "Clerk"}}},
			wantErr: "role Clerk: a role carries a description",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateRoleDescriptions(tt.roles)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRoleDescriptions() error = %v, want nil", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateRoleDescriptions() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
