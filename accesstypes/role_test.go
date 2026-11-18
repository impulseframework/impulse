package accesstypes

import "testing"

// TestRoleCollection_HoldsSuperAdministrator pins the one test of who a tenant's sign-in
// settings do not bind: the role, held in the global partition and nowhere else.
func TestRoleCollection_HoldsSuperAdministrator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		held RoleCollection
		want bool
	}{
		{name: "the role in the global partition", held: RoleCollection{GlobalPolicyScope(): {"Administrator", SuperAdministrator}}, want: true},
		{name: "another global role", held: RoleCollection{GlobalPolicyScope(): {"Administrator"}}, want: false},
		{name: "a role held in every tenant domain alone", held: RoleCollection{EveryDomainPolicyScope(): {"Auditor"}}, want: false},
		{name: "the name held in every tenant domain", held: RoleCollection{EveryDomainPolicyScope(): {SuperAdministrator}}, want: false},
		{name: "the name held in one tenant", held: RoleCollection{DomainPolicyScope("lakeside"): {SuperAdministrator}}, want: false},
		{name: "the name in another case", held: RoleCollection{GlobalPolicyScope(): {"superadministrator"}}, want: false},
		{name: "no membership", held: RoleCollection{}, want: false},
		{name: "a nil collection", held: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.held.HoldsSuperAdministrator(); got != tt.want {
				t.Errorf("HoldsSuperAdministrator() = %v, want %v", got, tt.want)
			}
		})
	}
}
