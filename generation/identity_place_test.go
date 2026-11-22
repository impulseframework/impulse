package generation

import (
	"slices"
	"testing"
)

// Test_prepareIdentity_placeFields pins what the whole application's methods say of a
// place in an application without tenants: nothing. A role is held and a group is kept
// for the whole application, so no request carries a field for one tenant (Domain) or
// for every tenant (EveryDomain) that a caller could fill, each method works in the
// global scope, and the answers of the role file's grants are a no for any other scope
// rather than a question put to a method that gives nothing there.
func Test_prepareIdentity_placeFields(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables)...)
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	global := "scope := accesstypes.GlobalPolicyScope()"
	elsewhere := "if !scope.IsGlobal() {\n\t\treturn false, nil\n\t}\n\t"
	noPlace := []string{"Domain", "identity.Scope(", "scopeFields"}
	for file, want := range map[string][]string{
		"crewidentity/assign_crew_role.go": {
			"type AssignCrewRole struct {\n\tUserID impulse.UUID\n\t// Role is the role the person is given,", global,
			"client.manager.AssignRoleFor(ctx, txn, scope, m.UserID, accesstypes.Role(m.Role), period, identity.RequestReason(m.Reason))",
		},
		"crewidentity/remove_crew_role.go": {
			"type RemoveCrewRole struct {\n\tUserID impulse.UUID\n\tRole   string `validate:\"required\"`\n}", global,
		},
		"crewidentity/invite_crew_user.go": {
			"Name  *string\n\t\t// Roles are the roles the person is given,",
			"invitation := &identity.Invitation{Email: m.Email, Name: m.Name}",
		},
		"crewidentity/create_crew_group.go": {
			"CreateCrewGroup struct {\n\t\tName        string `validate:\"required\"`\n\t\tDescription string `validate:\"required\"`\n\t}",
			"client.manager.CreateGroup(ctx, txn, accesstypes.GlobalPolicyScope(), m.Name, m.Description)",
		},
		"crewidentity/give_crew_group_role.go": {
			"type GiveCrewGroupRole struct {\n\t// @target(CrewListedGroup)\n\tGroupID impulse.UUID\n\t// Role is the role the group is given,", global,
			"client.manager.GiveGroupRoleFor(ctx, txn, m.GroupID, scope, accesstypes.Role(m.Role), period)",
		},
		"crewidentity/take_crew_group_role.go": {
			"type TakeCrewGroupRole struct {\n\tGroupID impulse.UUID\n\tRole    string `validate:\"required\"`\n}", global,
			"client.manager.TakeGroupRole(ctx, txn, m.GroupID, scope, accesstypes.Role(m.Role))",
		},
		"crewidentity/grants.go": {
			elsewhere + "gate, err := caller.ExecuteGate(ctx, AssignCrewRole{}.Method(), &AssignCrewRole{UserID: userID, Role: string(role)})",
			elsewhere + "gate, err := caller.ExecuteGate(ctx, RemoveCrewRole{}.Method(), &RemoveCrewRole{UserID: userID, Role: string(role)})",
			elsewhere + "switch method {\n\tcase AssignCrewRole{}.Method():\n\t\treturn couldGiveBy(ctx, caller, method, &AssignCrewRole{Role: string(role)})\n",
			"return couldGiveBy(ctx, caller, method, &InviteCrewUser{Roles: []string{string(role)}})",
			"return couldGiveBy(ctx, caller, method, &GiveCrewGroupRole{Role: string(role)})",
		},
	} {
		assertFileContains(t, file, want, noPlace)
	}
}

// Test_prepareIdentity_placeFieldsWithTenants pins the same methods in an application
// with tenants: each request says where (one tenant, every tenant, or neither for the
// whole application), the method reads its scope from that, and the grants ask each
// method with the place the scope names.
func Test_prepareIdentity_placeFieldsWithTenants(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	place := "scope, err := identity.Scope(m.Domain, m.EveryDomain)"
	fields := ", Domain: domain, EveryDomain: everyDomain})"
	noTenants := []string{"The application has no tenants"}
	for file, want := range map[string][]string{
		"crewidentity/assign_crew_role.go": {"Domain      *string\n\tEveryDomain bool\n", place},
		"crewidentity/remove_crew_role.go": {"Role        string `validate:\"required\"`\n\tDomain      *string\n\tEveryDomain bool\n}", place},
		"crewidentity/invite_crew_user.go": {
			"Domain      *string\n\t\tEveryDomain bool\n",
			"invitation := &identity.Invitation{Email: m.Email, Name: m.Name, Domain: m.Domain, EveryDomain: m.EveryDomain}",
		},
		// A group is kept in one place: its request has no "every tenant".
		"crewidentity/create_crew_group.go": {
			"scope, err := identity.Scope(m.Domain, false)", "client.manager.CreateGroup(ctx, txn, scope, m.Name, m.Description)",
		},
		"crewidentity/give_crew_group_role.go": {"Domain      *string\n\tEveryDomain bool\n", place},
		"crewidentity/take_crew_group_role.go": {"Role        string `validate:\"required\"`\n\tDomain      *string\n\tEveryDomain bool\n}", place},
		"crewidentity/grants.go": {
			"domain, everyDomain := scopeFields(scope)",
			"func scopeFields(scope accesstypes.PolicyScope) (domain *string, everyDomain bool) {",
			"&AssignCrewRole{UserID: userID, Role: string(role)" + fields,
			"&RemoveCrewRole{UserID: userID, Role: string(role)" + fields,
			"&AssignCrewRole{Role: string(role)" + fields,
			"&InviteCrewUser{Roles: []string{string(role)}" + fields,
			"&GiveCrewGroupRole{Role: string(role)" + fields,
		},
	} {
		assertFileContains(t, file, want, noTenants)
	}
}
