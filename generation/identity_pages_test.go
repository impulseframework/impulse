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
	// The auth whose people the tenant record's outlets sign in, in an application
	// whose roles it gives and whose schema keeps its invitations: staff bring a tenant
	// in by an invitation, so its pages know the three pieces.
	inviting := &identityInstance{
		auth: "Crew", outlets: []string{"console"}, createsTenant: true,
		tables: map[identityTable]bool{identityMailLinks: true, identityMemberships: true},
		tenant: &identityTenant{name: "Sector", table: "Sectors", key: "Id", invitation: &identityTenantInvitation{}},
	}

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
				"CrewSignInMethods", "CrewSignInWays",
				"CrewSectorUsers", "CrewSectorUserRoles", "CrewSectorInvitations", "CrewSectorSignInWays",
				"DisableCrewUser", "EndCrewSession", "EndCrewUserSessions", "UnlockCrewSignIn", "UnlinkCrewSessionIdentity", "CreateCrewUser",
				"RemoveCrewPassword", "OpenCrewSignUp", "MakeCrewInviteOnly",
				// The way back in: the link an administrator sends from a person's row, on
				// each side, and the switch for a person's own request, which is the whole
				// application's alone.
				"SendCrewUserPasswordResetLink", "SetCrewSelfServiceReset", "SendCrewSectorUserPasswordResetLink",
				"DisableCrewSectorUser", "AssignCrewSectorRole", "RemoveCrewSectorRole", "RevokeCrewSectorUserTrustedDevices", "RemoveCrewSectorPassword",
				// Served by the outlet and no part of the auth's management; the tenant record
				// is named, since a page reads a tenant's name from it.
				"Ships", "HailShip", "Sectors",
				// The invitation that makes a tenant is no part of this auth's management:
				// its people create no tenant, so the names are another declaration's.
				"CrewInvitedSectors", "InviteCrewSector", "WithdrawCrewInvitedSector",
			},
			want: []*tsAPIIdentity{{
				Auth: "Crew", Label: "Crew", Tenant: "Sector", TenantResource: "Sectors",
				Sides: []*tsAPIIdentitySide{
					{
						Scope: accesstypes.GlobalPermissionScope,
						Lists: []identityPart{
							part("users", "CrewSessionUsers"), part("roles", "CrewUserRoles"), part("identities", "CrewSessionIdentities"),
							part("signInWays", "CrewSignInWays"),
							part("invitations", "CrewSessionMailLinks"), part("sessions", "CrewSessions"), part("signInAttempts", "CrewSessionSignInAttempts"),
							// The auth's sign-in methods, each open to sign-up or invite only. An
							// application with tenants picks no default role, so it lists no roles
							// to pick one from.
							part("signInMethods", "CrewSignInMethods"),
						},
						Methods: []identityPart{
							part("create", "CreateCrewUser"), part("disable", "DisableCrewUser"),
							part("sendPasswordResetLink", "SendCrewUserPasswordResetLink"), part("endUserSessions", "EndCrewUserSessions"),
							part("unlinkIdentity", "UnlinkCrewSessionIdentity"), part("removePassword", "RemoveCrewPassword"),
							part("endSession", "EndCrewSession"), part("unlockSignIn", "UnlockCrewSignIn"),
							part("openSignUp", "OpenCrewSignUp"), part("makeInviteOnly", "MakeCrewInviteOnly"),
							part("setSelfServiceReset", "SetCrewSelfServiceReset"),
						},
					},
					{
						Scope: accesstypes.DomainPermissionScope,
						// The outlet serves no list of the identities a tenant's people hold; how each
						// signs in is a list of its own, named where the outlet serves it.
						Lists: []identityPart{
							part("users", "CrewSectorUsers"), part("roles", "CrewSectorUserRoles"), part("signInWays", "CrewSectorSignInWays"),
							part("invitations", "CrewSectorInvitations"),
						},
						Methods: []identityPart{
							part("assignRole", "AssignCrewSectorRole"), part("removeRole", "RemoveCrewSectorRole"), part("disable", "DisableCrewSectorUser"),
							part("sendPasswordResetLink", "SendCrewSectorUserPasswordResetLink"),
							part("revokeTrustedDevices", "RevokeCrewSectorUserTrustedDevices"), part("removePassword", "RemoveCrewSectorPassword"),
						},
					},
				},
			}},
		},
		{
			name:      "the auth that brings a tenant in by invitation names the list of the ones that stand and the two methods",
			outlet:    "console",
			instances: []*identityInstance{inviting},
			served:    []string{"CrewSessionUsers", "CrewInvitedSectors", "InviteCrewSector", "WithdrawCrewInvitedSector", "CrewSectorUsers", "Sectors"},
			want: []*tsAPIIdentity{{
				Auth: "Crew", Label: "Crew", Tenant: "Sector", TenantResource: "Sectors",
				Sides: []*tsAPIIdentitySide{
					{
						Scope:   accesstypes.GlobalPermissionScope,
						Lists:   []identityPart{part("users", "CrewSessionUsers"), part("invitedTenants", "CrewInvitedSectors")},
						Methods: []identityPart{part("inviteTenant", "InviteCrewSector"), part("withdrawInvitedTenant", "WithdrawCrewInvitedSector")},
					},
					// A tenant's own side carries none of it: no tenant brings another in.
					{Scope: accesstypes.DomainPermissionScope, Lists: []identityPart{part("users", "CrewSectorUsers")}},
				},
			}},
		},
		{
			name:      "an application with no tenant record has the whole application's side alone",
			outlet:    "console",
			instances: []*identityInstance{solo},
			served:    []string{"StaffSessionUsers", "DisableStaffUser", "StaffSignInMethods", "StaffSignUpRoles", "OpenStaffSignUp", "MakeStaffInviteOnly"},
			want: []*tsAPIIdentity{{
				Auth: "Staff", Label: "Staff",
				Sides: []*tsAPIIdentitySide{{
					Scope: accesstypes.GlobalPermissionScope,
					// Without tenants a person who signs up is given a role, picked from the
					// roles the side lists for it.
					Lists:   []identityPart{part("users", "StaffSessionUsers"), part("signInMethods", "StaffSignInMethods"), part("signUpRoles", "StaffSignUpRoles")},
					Methods: []identityPart{part("disable", "DisableStaffUser"), part("openSignUp", "OpenStaffSignUp"), part("makeInviteOnly", "MakeStaffInviteOnly")},
				}},
			}},
		},
		{
			name:      "a side whose people the outlet does not serve is left out, and a tenant record it does not serve is not named",
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
		Auth: "Crew", Label: "Crew", Tenant: "Sector", TenantResource: "Sectors",
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
		"  identity: [\n    {\n      auth: 'Crew',\n      label: 'Crew',\n      tenant: 'Sector',\n      tenantResource: Resources.Sectors,\n      sides: [",
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
