package access

// These tests pin the existence-probe warning: which grants raise it (a
// conditional Delete, Update, or targeted Execute in a role that can neither
// Read nor List the row it checks), which do not, that it is per role, that
// it is a warning and never a rejection, and that ValidateRoles answers
// without a store.

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
)

// probeCollection is the fixture vocabulary: one domain-scoped row resource
// with every CRUD permission, a domain-scoped method with that resource as its
// @target, and a global method with no target.
type probeCollection struct{}

func (probeCollection) List() map[accesstypes.Permission][]accesstypes.Resource {
	return map[accesstypes.Permission][]accesstypes.Resource{
		"Create":  {"Missions", "Missions.hazard", "Missions.state"},
		"Read":    {"Missions", "Missions.hazard", "Missions.state"},
		"List":    {"Missions", "Missions.hazard", "Missions.state"},
		"Update":  {"Missions", "Missions.hazard", "Missions.state"},
		"Delete":  {"Missions"},
		"Execute": {"ClaimMission", "IssueBulletin"},
	}
}

func (probeCollection) Scope(res accesstypes.Resource) accesstypes.PermissionScope {
	switch {
	case res == "IssueBulletin":
		return accesstypes.GlobalPermissionScope
	case res == "ClaimMission", strings.HasPrefix(string(res), "Missions"):
		return accesstypes.DomainPermissionScope
	default:
		return ""
	}
}

func (probeCollection) IsResourceImmutable(accesstypes.PermissionScope, accesstypes.Resource) bool {
	return false
}

func (probeCollection) AttributeComparisonType(_ accesstypes.PermissionScope, res accesstypes.Resource, name string) (accesstypes.AttributeType, bool) {
	if res != "Missions" {
		return "", false
	}
	switch name {
	case "hazard":
		return accesstypes.AttributeTypeNumber, true
	case "state":
		return accesstypes.AttributeTypeString, true
	default:
		return "", false
	}
}

func (probeCollection) AttributeIsColumn(_ accesstypes.PermissionScope, res accesstypes.Resource, _ string) bool {
	return res == "Missions"
}

func (probeCollection) SubjectSetComparisonType(string) (accesstypes.AttributeType, bool) {
	return "", false
}

func (probeCollection) SubjectValueComparisonType(string) (accesstypes.AttributeType, bool) {
	return "", false
}

func (probeCollection) IsComputedResource(accesstypes.PermissionScope, accesstypes.Resource) bool {
	return false
}

func (probeCollection) MethodTarget(_ accesstypes.PermissionScope, method accesstypes.Resource) (accesstypes.Resource, bool) {
	if method == "ClaimMission" {
		return "Missions", true
	}

	return "", false
}

func (probeCollection) ConcealingKeys(accesstypes.PermissionScope, accesstypes.Resource) (order, keys []accesstypes.Tag) {
	return nil, nil
}

func TestValidateRoles_grantWarnings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		roles ScopedRoles
		want  []Warning
	}{
		{
			name: "a conditional Delete without Read or List warns",
			roles: ScopedRoles{Domain: []*Role{{Name: "Paymaster", Description: "Settles the open missions.", Permissions: map[accesstypes.Permission][]Grant{
				"Delete": {{Resource: "Missions", Condition: "state = 'open'"}},
			}}}},
			want: []Warning{
				GrantWarning{Role: "Paymaster", Scope: accesstypes.DomainPermissionScope, Permission: "Delete", Resource: "Missions", Row: "Missions", Condition: "state = 'open'"},
			},
		},
		{
			name: "Read on the resource under any condition closes it",
			roles: ScopedRoles{Domain: []*Role{{Name: "Paymaster", Description: "Settles the open missions.", Permissions: map[accesstypes.Permission][]Grant{
				"Read":   {{Resource: "Missions", Fields: []accesstypes.Tag{"state"}, Condition: "hazard < 3"}},
				"Delete": {{Resource: "Missions", Condition: "state = 'open'"}},
			}}}},
		},
		{
			name: "List alone is a read path",
			roles: ScopedRoles{Domain: []*Role{{Name: "Paymaster", Description: "Settles the open missions.", Permissions: map[accesstypes.Permission][]Grant{
				"List":   {{Resource: "Missions", Fields: []accesstypes.Tag{"state"}}},
				"Delete": {{Resource: "Missions", Condition: "state = 'open'"}},
			}}}},
		},
		{
			name: "an unconditional Delete reveals existence only by succeeding",
			roles: ScopedRoles{Domain: []*Role{{Name: "Paymaster", Description: "Settles the open missions.", Permissions: map[accesstypes.Permission][]Grant{
				"Delete": {{Resource: "Missions"}},
			}}}},
		},
		{
			name: "a conditional Update warns once for its base row, not once per field",
			roles: ScopedRoles{Domain: []*Role{{Name: "Paymaster", Description: "Settles the open missions.", Permissions: map[accesstypes.Permission][]Grant{
				"Update": {{Resource: "Missions", Fields: []accesstypes.Tag{"hazard", "state"}, Condition: "state = 'open'"}},
			}}}},
			want: []Warning{
				GrantWarning{Role: "Paymaster", Scope: accesstypes.DomainPermissionScope, Permission: "Update", Resource: "Missions", Row: "Missions", Condition: "state = 'open'"},
			},
		},
		{
			name: "each conditional grant on the resource is its own warning",
			roles: ScopedRoles{Domain: []*Role{{Name: "Paymaster", Description: "Settles the open missions.", Permissions: map[accesstypes.Permission][]Grant{
				"Delete": {
					{Resource: "Missions", Condition: "state = 'open'"},
					{Resource: "Missions", Condition: "hazard < 3"},
				},
			}}}},
			want: []Warning{
				GrantWarning{Role: "Paymaster", Scope: accesstypes.DomainPermissionScope, Permission: "Delete", Resource: "Missions", Row: "Missions", Condition: "hazard < 3"},
				GrantWarning{Role: "Paymaster", Scope: accesstypes.DomainPermissionScope, Permission: "Delete", Resource: "Missions", Row: "Missions", Condition: "state = 'open'"},
			},
		},
		{
			name: "a conditional Execute on a targeted method warns for the @target row",
			roles: ScopedRoles{Domain: []*Role{{Name: "Cadet", Description: "Trains on the missions.", Permissions: map[accesstypes.Permission][]Grant{
				"Execute": {{Resource: "ClaimMission", Condition: "hazard < 3"}},
			}}}},
			want: []Warning{
				GrantWarning{Role: "Cadet", Scope: accesstypes.DomainPermissionScope, Permission: "Execute", Resource: "ClaimMission", Row: "Missions", Condition: "hazard < 3"},
			},
		},
		{
			name: "Read on the @target closes it",
			roles: ScopedRoles{Domain: []*Role{{Name: "Cadet", Description: "Trains on the missions.", Permissions: map[accesstypes.Permission][]Grant{
				"Read":    {{Resource: "Missions", Fields: []accesstypes.Tag{"hazard"}}},
				"Execute": {{Resource: "ClaimMission", Condition: "hazard < 3"}},
			}}}},
		},
		{
			name: "a method without a @target locates no row",
			roles: ScopedRoles{Global: []*Role{{Name: "BulletinOfficer", Description: "Posts the bulletins.", Permissions: map[accesstypes.Permission][]Grant{
				"Execute": {{Resource: "IssueBulletin", Condition: "now < '2027-06-30T00:00:00Z'"}},
			}}}},
		},
		{
			name: "Read held by another role does not close it: the check is per role",
			roles: ScopedRoles{Domain: []*Role{
				{Name: "CrewCommon", Description: "Reads what every crew shares.", Permissions: map[accesstypes.Permission][]Grant{
					"Read": {{Resource: "Missions", Fields: []accesstypes.Tag{"state"}}},
				}},
				{Name: "Paymaster", Description: "Settles the open missions.", Permissions: map[accesstypes.Permission][]Grant{
					"Delete": {{Resource: "Missions", Condition: "state = 'open'"}},
				}},
			}},
			want: []Warning{
				GrantWarning{Role: "Paymaster", Scope: accesstypes.DomainPermissionScope, Permission: "Delete", Resource: "Missions", Row: "Missions", Condition: "state = 'open'"},
			},
		},
		{
			name: "warnings follow declaration order across roles, then permission order within one",
			roles: ScopedRoles{Domain: []*Role{
				{Name: "Paymaster", Description: "Settles the open missions.", Permissions: map[accesstypes.Permission][]Grant{
					"Update":  {{Resource: "Missions", Fields: []accesstypes.Tag{"state"}, Condition: "state = 'open'"}},
					"Execute": {{Resource: "ClaimMission", Condition: "hazard < 3"}},
					"Delete":  {{Resource: "Missions", Condition: "state = 'open'"}},
				}},
				{Name: "Cadet", Description: "Trains on the missions.", Permissions: map[accesstypes.Permission][]Grant{
					"Execute": {{Resource: "ClaimMission", Condition: "hazard < 3"}},
				}},
			}},
			want: []Warning{
				GrantWarning{Role: "Paymaster", Scope: accesstypes.DomainPermissionScope, Permission: "Delete", Resource: "Missions", Row: "Missions", Condition: "state = 'open'"},
				GrantWarning{Role: "Paymaster", Scope: accesstypes.DomainPermissionScope, Permission: "Execute", Resource: "ClaimMission", Row: "Missions", Condition: "hazard < 3"},
				GrantWarning{Role: "Paymaster", Scope: accesstypes.DomainPermissionScope, Permission: "Update", Resource: "Missions", Row: "Missions", Condition: "state = 'open'"},
				GrantWarning{Role: "Cadet", Scope: accesstypes.DomainPermissionScope, Permission: "Execute", Resource: "ClaimMission", Row: "Missions", Condition: "hazard < 3"},
			},
		},
		{
			name: "an empty configuration raises nothing and provisions no role",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ValidateRoles(probeCollection{}, &RoleConfig{Roles: tt.roles})
			if err != nil {
				t.Fatalf("ValidateRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ValidateRoles() warnings mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateRoles_errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		roles   ScopedRoles
		wantErr string
	}{
		{
			name: "a grant on an unknown resource",
			roles: ScopedRoles{Domain: []*Role{{Name: "Paymaster", Description: "Settles the open missions.", Permissions: map[accesstypes.Permission][]Grant{
				"Delete": {{Resource: "Nowhere"}},
			}}}},
			wantErr: "does not require a permission or does not exist",
		},
		{
			name: "a condition naming an attribute the resource lacks",
			roles: ScopedRoles{Domain: []*Role{{Name: "Paymaster", Description: "Settles the open missions.", Permissions: map[accesstypes.Permission][]Grant{
				"Delete": {{Resource: "Missions", Condition: "owner = subject"}},
			}}}},
			wantErr: "is not an attribute of Missions",
		},
		{
			name: "a role declared at both scopes",
			roles: ScopedRoles{
				Global: []*Role{{Name: "Paymaster"}},
				Domain: []*Role{{Name: "Paymaster"}},
			},
			wantErr: "declared in both the global and domain roles",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			warnings, err := ValidateRoles(probeCollection{}, &RoleConfig{Roles: tt.roles})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateRoles() error = %v, want one containing %q", err, tt.wantErr)
			}
			if warnings != nil {
				t.Errorf("ValidateRoles() warnings = %v, want none beside an error", warnings)
			}
		})
	}
}

func TestGrantWarning_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		warning GrantWarning
		want    string
	}{
		{
			name:    "a write on the row resource itself",
			warning: GrantWarning{Role: "Paymaster", Scope: accesstypes.DomainPermissionScope, Permission: "Delete", Resource: "Missions", Row: "Missions", Condition: "state = 'open'"},
			want: `role Paymaster: Delete on Missions is granted under "state = 'open'" without Read or List on Missions: ` +
				`a Forbidden answer tells the caller a Missions row exists where a read would answer NotFound. ` +
				`Grant Read or List on Missions in this role or in a role assigned with it, or accept the disclosure; ` +
				`a Read whose condition is narrower than this one leaks the same way.`,
		},
		{
			name:    "an Execute names the method and its @target",
			warning: GrantWarning{Role: "Cadet", Scope: accesstypes.DomainPermissionScope, Permission: "Execute", Resource: "ClaimMission", Row: "Missions", Condition: "hazard < 3"},
			want: `role Cadet: Execute on ClaimMission is granted under "hazard < 3" without Read or List on its @target Missions: ` +
				`a Forbidden answer tells the caller a Missions row exists where a read would answer NotFound. ` +
				`Grant Read or List on Missions in this role or in a role assigned with it, or accept the disclosure; ` +
				`a Read whose condition is narrower than this one leaks the same way.`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.warning.String(); got != tt.want {
				t.Errorf("GrantWarning.String() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
