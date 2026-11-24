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
	// The auth of an application whose tenants have a directory, a row of the record the
	// package declares for it.
	directed := &identityInstance{auth: "Crew", outlets: []string{"console"}, tenant: &identityTenant{name: "Sector", table: "Sectors", key: "Id", directories: "SectorDirectory"}}

	names := func(names ...string) (resources []*tsAPIResource, methods []*tsAPIMethod) {
		for _, name := range names {
			switch {
			case strings.HasSuffix(name, "s") && !strings.HasPrefix(name, "End") && !strings.HasPrefix(name, "Revoke") && !strings.HasPrefix(name, "Set"):
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
				"CrewSessionUsers", "CrewUserRoles", "CrewSessionIdentities", "CrewSessionMailLinks", "CrewSessions", "CrewListedSessions", "CrewSessionSignInAttempts",
				"CrewSignInMethods", "CrewSignInWays",
				// A tenant's roles are the one list that says where each comes from, and the
				// list of the roles people hold themselves is no part of the description.
				"CrewSectorUsers", "CrewSectorUserRoles", "CrewSectorHeldRoles", "CrewSectorInvitations", "CrewSectorSignInWays",
				"DisableCrewUser", "EndCrewSession", "EndCrewUserSessions", "UnlockCrewSignIn", "UnlinkCrewSessionIdentity",
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
							part("invitations", "CrewSessionMailLinks"), part("sessions", "CrewListedSessions"), part("signInAttempts", "CrewSessionSignInAttempts"),
							// The auth's sign-in methods, each open to sign-up or invite only. An
							// application with tenants picks no default role, so it lists no roles
							// to pick one from.
							part("signInMethods", "CrewSignInMethods"),
						},
						Methods: []identityPart{
							part("disable", "DisableCrewUser"),
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
							part("users", "CrewSectorUsers"), part("roles", "CrewSectorHeldRoles"), part("signInWays", "CrewSectorSignInWays"),
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
			name:      "each side names the list its methods that give a role pick it from, where the outlet serves it",
			outlet:    "console",
			instances: []*identityInstance{crew},
			served:    []string{"CrewSessionUsers", "CrewGivableRoles", "AssignCrewRole", "CrewSectorUsers", "CrewSectorRoles", "AssignCrewSectorRole"},
			want: []*tsAPIIdentity{{
				Auth: "Crew", Label: "Crew", Tenant: "Sector",
				Sides: []*tsAPIIdentitySide{
					{
						Scope:   accesstypes.GlobalPermissionScope,
						Lists:   []identityPart{part("users", "CrewSessionUsers"), part("rolesToGive", "CrewGivableRoles")},
						Methods: []identityPart{part("assignRole", "AssignCrewRole")},
					},
					{
						Scope:   accesstypes.DomainPermissionScope,
						Lists:   []identityPart{part("users", "CrewSectorUsers"), part("rolesToGive", "CrewSectorRoles")},
						Methods: []identityPart{part("assignRole", "AssignCrewSectorRole")},
					},
				},
			}},
		},
		{
			name:      "the method that removes a person from a tenant is named on the tenant's side alone",
			outlet:    "console",
			instances: []*identityInstance{crew},
			// RemoveCrewUser is a method by the name the whole application's side would give
			// it, which that side does not name.
			served: []string{"CrewSessionUsers", "DeleteCrewUser", "RemoveCrewUser", "CrewSectorUsers", "DeleteCrewSectorUser", "RemoveCrewSectorUser"},
			want: []*tsAPIIdentity{{
				Auth: "Crew", Label: "Crew", Tenant: "Sector",
				Sides: []*tsAPIIdentitySide{
					{
						Scope:   accesstypes.GlobalPermissionScope,
						Lists:   []identityPart{part("users", "CrewSessionUsers")},
						Methods: []identityPart{part("delete", "DeleteCrewUser")},
					},
					{
						Scope:   accesstypes.DomainPermissionScope,
						Lists:   []identityPart{part("users", "CrewSectorUsers")},
						Methods: []identityPart{part("delete", "DeleteCrewSectorUser"), part("removeFromTenant", "RemoveCrewSectorUser")},
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
			name:      "a tenant's side names the directories record, the four methods that set how its people enter and the two of its word for its directory's second factor, and the whole application's side none",
			outlet:    "console",
			instances: []*identityInstance{directed},
			served: []string{
				"CrewSessionUsers", "CrewSectorUsers", "SectorDirectories",
				"OpenSectorToDirectory", "MakeSectorInviteOnly", "MakeSectorDirectoryOnly", "EndSectorDirectoryOnly",
				"VouchForSectorDirectorySecondFactor", "StopVouchingForSectorDirectorySecondFactor",
			},
			want: []*tsAPIIdentity{{
				Auth: "Crew", Label: "Crew", Tenant: "Sector",
				Sides: []*tsAPIIdentitySide{
					{Scope: accesstypes.GlobalPermissionScope, Lists: []identityPart{part("users", "CrewSessionUsers")}},
					{
						Scope: accesstypes.DomainPermissionScope,
						Lists: []identityPart{part("users", "CrewSectorUsers"), part("directories", "SectorDirectories")},
						Methods: []identityPart{
							part("openToDirectory", "OpenSectorToDirectory"), part("makeDirectoryInviteOnly", "MakeSectorInviteOnly"),
							part("makeDirectoryOnly", "MakeSectorDirectoryOnly"), part("endDirectoryOnly", "EndSectorDirectoryOnly"),
							part("vouchForDirectorySecondFactor", "VouchForSectorDirectorySecondFactor"),
							part("stopVouchingForDirectorySecondFactor", "StopVouchingForSectorDirectorySecondFactor"),
						},
					},
				},
			}},
		},
		{
			name:      "a tenant's side names only the methods that set how its people enter that the outlet serves",
			outlet:    "console",
			instances: []*identityInstance{directed},
			served:    []string{"CrewSessionUsers", "CrewSectorUsers", "SectorDirectories", "MakeSectorDirectoryOnly", "EndSectorDirectoryOnly"},
			want: []*tsAPIIdentity{{
				Auth: "Crew", Label: "Crew", Tenant: "Sector",
				Sides: []*tsAPIIdentitySide{
					{Scope: accesstypes.GlobalPermissionScope, Lists: []identityPart{part("users", "CrewSessionUsers")}},
					{
						Scope:   accesstypes.DomainPermissionScope,
						Lists:   []identityPart{part("users", "CrewSectorUsers"), part("directories", "SectorDirectories")},
						Methods: []identityPart{part("makeDirectoryOnly", "MakeSectorDirectoryOnly"), part("endDirectoryOnly", "EndSectorDirectoryOnly")},
					},
				},
			}},
		},
		{
			name:      "an application whose tenants have no directories names neither the record nor the methods",
			outlet:    "console",
			instances: []*identityInstance{crew},
			served: []string{
				"CrewSessionUsers", "CrewSectorUsers", "SectorDirectories",
				"OpenSectorToDirectory", "MakeSectorInviteOnly", "MakeSectorDirectoryOnly", "EndSectorDirectoryOnly",
				"VouchForSectorDirectorySecondFactor", "StopVouchingForSectorDirectorySecondFactor",
			},
			want: []*tsAPIIdentity{{
				Auth: "Crew", Label: "Crew", Tenant: "Sector",
				Sides: []*tsAPIIdentitySide{
					{Scope: accesstypes.GlobalPermissionScope, Lists: []identityPart{part("users", "CrewSessionUsers")}},
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
			name:      "an auth with a password and no choice of who may sign up has the Sign-in page for the reset switch alone",
			outlet:    "console",
			instances: []*identityInstance{crew},
			served:    []string{"CrewSessionUsers", "CrewSignInMethods", "SetCrewSelfServiceReset"},
			want: []*tsAPIIdentity{{
				Auth: "Crew", Label: "Crew", Tenant: "Sector",
				Sides: []*tsAPIIdentitySide{{
					Scope:   accesstypes.GlobalPermissionScope,
					Lists:   []identityPart{part("users", "CrewSessionUsers"), part("signInMethods", "CrewSignInMethods")},
					Methods: []identityPart{part("setSelfServiceReset", "SetCrewSelfServiceReset")},
				}},
			}},
		},
		{
			name:      "what every sign-in asks for is two lists after the sign-in methods and three methods after the reset switch",
			outlet:    "console",
			instances: []*identityInstance{crew},
			served: []string{
				"CrewSessionUsers", "CrewSignInMethods", "CrewSignInSettings", "CrewRememberLengths",
				"SetCrewSelfServiceReset", "RequireCrewSecondFactor", "StopRequiringCrewSecondFactor", "SetCrewRememberedBrowsers",
			},
			want: []*tsAPIIdentity{{
				Auth: "Crew", Label: "Crew", Tenant: "Sector",
				Sides: []*tsAPIIdentitySide{{
					Scope: accesstypes.GlobalPermissionScope,
					Lists: []identityPart{
						part("users", "CrewSessionUsers"), part("signInMethods", "CrewSignInMethods"),
						part("signInSettings", "CrewSignInSettings"), part("rememberLengths", "CrewRememberLengths"),
					},
					Methods: []identityPart{
						part("setSelfServiceReset", "SetCrewSelfServiceReset"), part("requireSecondFactor", "RequireCrewSecondFactor"),
						part("stopRequiringSecondFactor", "StopRequiringCrewSecondFactor"), part("setRememberedBrowsers", "SetCrewRememberedBrowsers"),
					},
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

// Test_identityOn_tenantDirectoryOnlyField pins the tenant record's directory-only field
// in the identity block: named as the wire names it where the outlet serves the record
// and the record declares the switch, and absent where the record declares none or the
// outlet does not serve it.
func Test_identityOn_tenantDirectoryOnlyField(t *testing.T) {
	t.Parallel()

	staff := &identityInstance{auth: "Staff", outlets: []string{"console"}, tenant: &identityTenant{name: "Harbor", table: "Harbors", key: "Id", directories: "HarborDirectory"}}
	tests := []struct {
		name         string
		switched     bool
		served       []string
		wantResource string
		wantField    string
	}{
		{name: "a record that declares the switch, on an outlet that serves it", switched: true, served: []string{"StaffHarborUsers", "Harbors"}, wantResource: "Harbors", wantField: "directoryOnly"},
		{name: "a record that declares no switch", served: []string{"StaffHarborUsers", "Harbors"}, wantResource: "Harbors"},
		{name: "an outlet that does not serve the record", switched: true, served: []string{"StaffHarborUsers"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{tableMap: tenantFixtureTables(), identity: []*identityInstance{staff}}
			resources, err := c.structsToResources(tenantFixtureStructs(t, fixtureStructs(loadFixture(t, "tenantfixture")), "Harbor", "HarborDirectory"))
			if err != nil {
				t.Fatalf("structsToResources() error = %v", err)
			}
			if !tt.switched {
				for _, res := range resources {
					res.DirectoryOnlyField = nil
				}
			}
			c.resources = resources
			data := &tsAPIData{}
			for _, name := range tt.served {
				data.Resources = append(data.Resources, &tsAPIResource{Name: name})
			}
			gen := &typescriptGenerator{client: c, outletName: "console"}

			got := gen.identityOn(data)
			if len(got) != 1 {
				t.Fatalf("identityOn() = %d auths, want 1", len(got))
			}
			if got[0].TenantResource != tt.wantResource || got[0].TenantDirectoryOnlyField != tt.wantField {
				t.Errorf("identityOn() TenantResource = %q, TenantDirectoryOnlyField = %q, want %q and %q", got[0].TenantResource, got[0].TenantDirectoryOnlyField, tt.wantResource, tt.wantField)
			}
		})
	}
}

// Test_typescriptAPITemplate_identity pins the block as the template writes it.
func Test_typescriptAPITemplate_identity(t *testing.T) {
	t.Parallel()

	data := &tsAPIData{Identity: []*tsAPIIdentity{{
		Auth: "Crew", Label: "Crew", Tenant: "Sector", TenantResource: "Sectors", TenantDirectoryOnlyField: "directoryOnly",
		Sides: []*tsAPIIdentitySide{
			{
				Scope:   accesstypes.GlobalPermissionScope,
				Lists:   []identityPart{{Key: "users", Name: "CrewSessionUsers"}, {Key: "sessions", Name: "CrewListedSessions"}},
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
		"  identity: [\n    {\n      auth: 'Crew',\n      label: 'Crew',\n      tenant: 'Sector',\n      tenantResource: Resources.Sectors,\n      tenantDirectoryOnlyField: 'directoryOnly',\n      sides: [",
		"          scope: 'global',\n          lists: { users: Resources.CrewSessionUsers, sessions: Resources.CrewListedSessions },\n          methods: { disable: Methods.DisableCrewUser },",
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
