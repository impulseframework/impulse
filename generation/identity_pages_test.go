package generation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// Test_identityOn pins what the generated API description says of an auth's generated
// management: each side's lists and methods under the key a page knows each by, named
// as the generator names them, and only where the outlet's description carries them.
func Test_identityOn(t *testing.T) {
	t.Parallel()

	crew := &identityInstance{auth: "Crew", outlets: []string{"console", "admin"}, tenant: &identityTenant{name: "Sector", table: "Sectors", key: "Id"}}
	solo := &identityInstance{auth: "Staff", outlets: []string{"console"}}

	names := func(names ...string) (resources []*tsAPIResource, methods []*tsAPIMethod) {
		for _, name := range names {
			switch {
			case strings.HasSuffix(name, "s") && !strings.HasPrefix(name, "End") && !strings.HasPrefix(name, "Revoke"):
				resources = append(resources, &tsAPIResource{Name: name})
			default:
				methods = append(methods, &tsAPIMethod{Name: name})
			}
		}

		return resources, methods
	}
	part := func(key, name string) identityPart {
		return identityPart{Key: key, Name: name}
	}

	tests := []struct {
		name      string
		outlet    string
		instances []*identityInstance
		served    []string
		want      []*tsAPIIdentity
	}{
		{
			name:      "both sides, each with what the outlet serves, the names the generator gave",
			outlet:    "console",
			instances: []*identityInstance{crew},
			served: []string{
				"CrewSessionUsers", "CrewUserRoles", "CrewSessionIdentities", "CrewSessionMailLinks", "CrewSessions", "CrewSessionSignInAttempts",
				"CrewSectorUsers", "CrewSectorUserRoles", "CrewSectorInvitations",
				"DisableCrewUser", "EndCrewSession", "EndCrewUserSessions", "UnlockCrewSignIn", "UnlinkCrewSessionIdentity", "CreateCrewUser",
				"DisableCrewSectorUser", "AssignCrewSectorRole", "RemoveCrewSectorRole", "RevokeCrewSectorUserTrustedDevices",
				// Served by the outlet and no part of the auth's management.
				"Ships", "HailShip",
			},
			want: []*tsAPIIdentity{{
				Auth: "Crew", Label: "Crew", Tenant: "Sector",
				Sides: []*tsAPIIdentitySide{
					{
						Scope: accesstypes.GlobalPermissionScope,
						Lists: []identityPart{
							part("users", "CrewSessionUsers"), part("roles", "CrewUserRoles"), part("identities", "CrewSessionIdentities"),
							part("invitations", "CrewSessionMailLinks"), part("sessions", "CrewSessions"), part("signInAttempts", "CrewSessionSignInAttempts"),
						},
						Methods: []identityPart{
							part("create", "CreateCrewUser"), part("disable", "DisableCrewUser"), part("endUserSessions", "EndCrewUserSessions"),
							part("unlinkIdentity", "UnlinkCrewSessionIdentity"), part("endSession", "EndCrewSession"), part("unlockSignIn", "UnlockCrewSignIn"),
						},
					},
					{
						Scope: accesstypes.DomainPermissionScope,
						// The schema holds no identities table for this auth: the tenant keeps no list of them.
						Lists: []identityPart{part("users", "CrewSectorUsers"), part("roles", "CrewSectorUserRoles"), part("invitations", "CrewSectorInvitations")},
						Methods: []identityPart{
							part("assignRole", "AssignCrewSectorRole"), part("removeRole", "RemoveCrewSectorRole"), part("disable", "DisableCrewSectorUser"),
							part("revokeTrustedDevices", "RevokeCrewSectorUserTrustedDevices"),
						},
					},
				},
			}},
		},
		{
			name:      "an application with no tenant record has the whole application's side alone",
			outlet:    "console",
			instances: []*identityInstance{solo},
			served:    []string{"StaffSessionUsers", "DisableStaffUser"},
			want: []*tsAPIIdentity{{
				Auth: "Staff", Label: "Staff",
				Sides: []*tsAPIIdentitySide{{
					Scope:   accesstypes.GlobalPermissionScope,
					Lists:   []identityPart{part("users", "StaffSessionUsers")},
					Methods: []identityPart{part("disable", "DisableStaffUser")},
				}},
			}},
		},
		{
			name:      "a side whose people the outlet does not serve is left out",
			outlet:    "console",
			instances: []*identityInstance{crew},
			served:    []string{"CrewSessionUsers", "CrewSectorUserRoles", "DisableCrewSectorUser"},
			want: []*tsAPIIdentity{{
				Auth: "Crew", Label: "Crew", Tenant: "Sector",
				Sides: []*tsAPIIdentitySide{{Scope: accesstypes.GlobalPermissionScope, Lists: []identityPart{part("users", "CrewSessionUsers")}}},
			}},
		},
		{
			name:      "an outlet that does not serve the auth's management carries nothing of it",
			outlet:    "portal",
			instances: []*identityInstance{crew, solo},
			served:    []string{"CrewSessionUsers", "StaffSessionUsers"},
			want:      nil,
		},
		{
			name:      "an auth none of whose people the outlet serves is left out",
			outlet:    "console",
			instances: []*identityInstance{solo},
			served:    []string{"DisableStaffUser"},
			want:      nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data := &tsAPIData{}
			data.Resources, data.Methods = names(tt.served...)
			gen := &typescriptGenerator{client: &client{identity: tt.instances}, outletName: tt.outlet}

			if diff := cmp.Diff(tt.want, gen.identityOn(data), cmp.AllowUnexported(identityPart{})); diff != "" {
				t.Errorf("identityOn() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_typescriptAPITemplate_identity pins the block as the template writes it.
func Test_typescriptAPITemplate_identity(t *testing.T) {
	t.Parallel()

	data := &tsAPIData{Identity: []*tsAPIIdentity{{
		Auth: "Crew", Label: "Crew", Tenant: "Sector",
		Sides: []*tsAPIIdentitySide{
			{
				Scope:   accesstypes.GlobalPermissionScope,
				Lists:   []identityPart{{Key: "users", Name: "CrewSessionUsers"}, {Key: "sessions", Name: "CrewSessions"}},
				Methods: []identityPart{{Key: "disable", Name: "DisableCrewUser"}},
			},
			{Scope: accesstypes.DomainPermissionScope, Lists: []identityPart{{Key: "users", Name: "CrewSectorUsers"}}},
		},
	}}}
	c := &client{}
	out, err := c.generateTemplateOutput("typescriptAPITemplate", typescriptAPITemplate, data)
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	for _, want := range []string{
		"  identity: [\n    {\n      auth: 'Crew',\n      label: 'Crew',\n      tenant: 'Sector',\n      sides: [",
		"          scope: 'global',\n          lists: { users: Resources.CrewSessionUsers, sessions: Resources.CrewSessions },\n          methods: { disable: Methods.DisableCrewUser },",
		"          scope: 'domain',\n          lists: { users: Resources.CrewSectorUsers },\n          methods: {  },",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("typescriptAPITemplate output missing %q:\n%s", want, out)
		}
	}
}

// Test_authLabel pins the words a page shows for an auth.
func Test_authLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		auth string
		want string
	}{
		{name: "one word", auth: "Crew", want: "Crew"},
		{name: "two words", auth: "FleetStaff", want: "Fleet staff"},
		{name: "an initialism stays together", auth: "HQStaff", want: "HQ staff"},
		{name: "a trailing initialism stays together", auth: "StaffHQ", want: "Staff HQ"},
		{name: "empty", auth: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := authLabel(tt.auth); got != tt.want {
				t.Errorf("authLabel(%q) = %q, want %q", tt.auth, got, tt.want)
			}
		})
	}
}
