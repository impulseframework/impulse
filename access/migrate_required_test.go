package access

// These tests pin the required-field warning: a role that may create a
// resource and, across all its Create grants there, may set no grant's field
// for one the create must be given is named once per resource with the fields
// it lacks; a role that covers them, under any condition, is not; the check
// is per role; and it is a warning, never a rejection.

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/accesstypes"
)

// requiredCollection is the probe vocabulary with the facts the warning reads:
// a create of Missions must be given its hazard and its state.
type requiredCollection struct {
	probeCollection
}

func (requiredCollection) RequiredOnCreate(_ accesstypes.PermissionScope, res accesstypes.Resource) []accesstypes.Tag {
	if res == "Missions" {
		return []accesstypes.Tag{"hazard", "state"}
	}

	return nil
}

func TestValidateRoles_requiredFieldWarnings(t *testing.T) {
	t.Parallel()

	domain := accesstypes.DomainPermissionScope
	dispatcher := func(permissions map[accesstypes.Permission][]Grant) *Role {
		return &Role{Name: "Dispatcher", Description: "Opens the missions.", Permissions: permissions}
	}

	tests := []struct {
		name  string
		roles ScopedRoles
		want  []Warning
	}{
		{
			name: "a Create grant that leaves out a field every create must be given warns, naming the field",
			roles: ScopedRoles{Domain: []*Role{dispatcher(map[accesstypes.Permission][]Grant{
				"Create": {{Resource: "Missions", Fields: []accesstypes.Tag{"hazard"}}},
			})}},
			want: []Warning{RequiredFieldWarning{Role: "Dispatcher", Scope: domain, Resource: "Missions", Fields: []accesstypes.Tag{"state"}}},
		},
		{
			name: "a Create grant that names no field leaves out every one of them",
			roles: ScopedRoles{Domain: []*Role{dispatcher(map[accesstypes.Permission][]Grant{
				"Create": {{Resource: "Missions"}},
			})}},
			want: []Warning{RequiredFieldWarning{Role: "Dispatcher", Scope: domain, Resource: "Missions", Fields: []accesstypes.Tag{"hazard", "state"}}},
		},
		{
			name: "a grant that covers them raises nothing",
			roles: ScopedRoles{Domain: []*Role{dispatcher(map[accesstypes.Permission][]Grant{
				"Create": {{Resource: "Missions", Fields: []accesstypes.Tag{"hazard", "state"}}},
			})}},
		},
		{
			name: "two Create grants of one role cover the fields between them, whatever their conditions",
			roles: ScopedRoles{Domain: []*Role{dispatcher(map[accesstypes.Permission][]Grant{
				"Create": {
					{Resource: "Missions", Fields: []accesstypes.Tag{"hazard"}, Condition: "new.hazard < 3"},
					{Resource: "Missions", Fields: []accesstypes.Tag{"state"}},
				},
			})}},
		},
		{
			name: "a role that may not create the resource is not asked",
			roles: ScopedRoles{Domain: []*Role{dispatcher(map[accesstypes.Permission][]Grant{
				"Update": {{Resource: "Missions", Fields: []accesstypes.Tag{"hazard"}}},
				"Read":   {{Resource: "Missions", Fields: []accesstypes.Tag{"hazard"}}},
			})}},
		},
		{
			name: "a field another role grants does not close it: the check is per role",
			roles: ScopedRoles{Domain: []*Role{
				dispatcher(map[accesstypes.Permission][]Grant{
					"Create": {{Resource: "Missions", Fields: []accesstypes.Tag{"hazard"}}},
				}),
				{Name: "Controller", Description: "Sets a mission's state.", Permissions: map[accesstypes.Permission][]Grant{
					"Create": {{Resource: "Missions", Fields: []accesstypes.Tag{"state"}}},
				}},
			}},
			want: []Warning{
				RequiredFieldWarning{Role: "Dispatcher", Scope: domain, Resource: "Missions", Fields: []accesstypes.Tag{"state"}},
				RequiredFieldWarning{Role: "Controller", Scope: domain, Resource: "Missions", Fields: []accesstypes.Tag{"hazard"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ValidateRoles(requiredCollection{}, &RoleConfig{Roles: tt.roles}, fixtureAuth)
			if err != nil {
				t.Fatalf("ValidateRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("ValidateRoles() warnings mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The warning says, in one line, what is granted, what it leaves out, what that
// does to a create, and the ways out.
func TestRequiredFieldWarning_String(t *testing.T) {
	t.Parallel()

	w := RequiredFieldWarning{Role: "Dispatcher", Scope: accesstypes.DomainPermissionScope, Resource: "Missions", Fields: []accesstypes.Tag{"hazard", "state"}}
	const want = "role Dispatcher: Create on Missions is granted without hazard, state, which every create of Missions must be given (the column takes no NULL and nothing fills it for the client): a create under this role alone is refused at commit, whatever the person submits. Grant Create on the field, give the column a default, or accept this warning where the same people hold another role that grants it."
	if got := w.String(); got != want {
		t.Errorf("RequiredFieldWarning.String() = %q, want %q", got, want)
	}
}
