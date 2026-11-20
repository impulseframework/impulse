package generation

import (
	"slices"
	"testing"
)

// Test_prepareIdentity_givableRoles pins the list the whole application's role fields
// pick from, in an application without tenants: the roles to give, named by the role
// alone, listed for the whole application by the manager (GivableRoles) for the one
// method the request's filter names and for no request that names none, and named by
// the role field of every method that gives one by hand, after where the role is held.
// An auth whose directory writes the roles gives none by hand, and is written no list.
func Test_prepareIdentity_givableRoles(t *testing.T) {
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, crewGroupTables, membersIdentityTables)...)
	r.extraOutlets[1].auth = &outletAuth{importPath: membersAuthPath, methods: []AuthMethod{AzureMethod("directory").SyncsRoles()}}
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	assertFileContains(t, "crewidentity/crew_givable_roles.go",
		[]string{
			generationHeader,
			"// @computed\n// @suppress(readHandler)\n// @outlet(default)\n// @order(Name asc)\ntype CrewGivableRole struct {",
			"// @primarykey\n\t// @attribute(name)\n\tName        string `spanner:\"Name\"`\n\tDescription string `spanner:\"Description\"`\n",
			// The method a role is listed for is a field a request filters by, and no key:
			// a picker's value is the role's name still.
			"\tMethod string `spanner:\"Method\" allow_filter:\"true\"`\n}",
			`return "CrewGivableRoles"`,
			"func ListCrewGivableRole(ctx context.Context, qSet *resource.QuerySet[CrewGivableRole], db resource.Client, client *Client) iter.Seq2[*CrewGivableRole, error] {",
			`places, err := identity.GivingPlaces(qSet.Resource(), "", nil)`,
			// The list is one method's: the filter names it, among the methods whose role
			// field picks from the list, and the condition is taken out of what the
			// generated handler applies over the rows.
			"method, named, err := identity.GivingMethod(qSet.Resource(), []accesstypes.Resource{\n\t\t\tAssignCrewRole{}.Method(),\n\t\t\tCreateCrewUser{}.Method(),\n\t\t\t" +
				"InviteCrewUser{}.Method(),\n\t\t\tGiveCrewGroupRole{}.Method(),\n\t\t}, qSet.Filter().Take(\"Method\"))",
			// A request that names no method is listed no role, after its places were read:
			// a filter that is wrong is refused whether a method is named or not.
			"\t\tif !named {\n\t\t\treturn\n\t\t}\n\t\tfor _, place := range places {\n\t\t\troles, err := client.manager.GivableRoles(ctx, db, method, place)",
			"row := &CrewGivableRole{Name: string(role.Name), Description: role.Description, Method: string(method)}",
			"//\tAssignCrewRole, CreateCrewUser, InviteCrewUser, GiveCrewGroupRole\n",
			"(method:eq:AssignCrewRole)",
		},
		// Without tenants the list names no place.
		[]string{"Domain", "EveryDomain", "Take(\"Domain\")"},
	)
	for _, file := range []string{"crewidentity/assign_crew_role.go", "crewidentity/create_crew_user.go", "crewidentity/invite_crew_user.go", "crewidentity/give_crew_group_role.go"} {
		assertFileContains(t, file, []string{"// @enumerate(CrewGivableRoles)"}, nil)
	}
	if slices.Contains(identityTreeFiles(t, "pkg/identity"), "membersidentity/members_givable_roles.go") {
		t.Error("membersidentity/members_givable_roles.go is written, and a directory writes the members' roles: nobody gives one by hand")
	}
}

// Test_prepareIdentity_givableRolesWithTenants pins the same list in an application
// with tenants: it names the place each role is held in, lists one tenant's roles for a
// filter on the tenant and otherwise the whole application's and every tenant's, and
// the methods ask for the tenant before the role.
func Test_prepareIdentity_givableRolesWithTenants(t *testing.T) {
	t.Chdir(t.TempDir())

	r := identityFixtureGenerator(t, slices.Concat(crewEveryTable, membersIdentityTables)...)
	r.resource = "pkg/resources"
	r.tableMap["Ships"] = shipsTable()
	writeResourcesPackage(t, "pkg/resources", map[string]string{"ships.go": shipTenantSource})
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}

	assertFileContains(t, "crewidentity/crew_givable_roles.go",
		[]string{
			"Domain string `spanner:\"Domain\" allow_filter:\"true\"`",
			"EveryDomain bool `spanner:\"EveryDomain\" allow_filter:\"true\"`",
			`places, err := identity.GivingPlaces(qSet.Resource(), "ship", qSet.Filter().Take("Domain"))`,
			"row.Domain, row.EveryDomain = string(domain), place.IsEveryDomain()",
		},
		nil,
	)
	assertFileContains(t, "crewidentity/invite_crew_user.go",
		[]string{"// @enumerate(Ships)\n\t\tDomain *string\n\t\t// Roles are the roles the person is given", "// @enumerate(CrewGivableRoles)\n\t\tRoles []string"},
		nil,
	)
	// A tenant's own methods pick from the tenant's roles still.
	assertFileContains(t, "crewidentity/invite_crew_ship_user.go", []string{"// @enumerate(CrewShipRoles)"}, []string{"CrewGivableRoles"})
}
