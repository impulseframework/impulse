package generation

import (
	"go/format"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/session"
)

// Test_identityUsersTemplate_administeredTenants pins what the users table's file says
// of the tenants a person administers, where the tenant record declares the
// directory-only switch: the query, a row for each tenant a person holds one of the
// engine's administrator roles in, in the tenant itself and in an assignment that has
// not ended; the roles, set by the client; and the account's TenantsToEnter over its
// id. Where the auth keeps groups the query also reads a role a group kept in the tenant
// holds there; where the record declares no switch, the file says nothing of it.
func Test_identityUsersTemplate_administeredTenants(t *testing.T) {
	t.Parallel()

	directory := &identityTenantDirectory{OnlyColumn: "DirectoryOnly", Table: "SectorDirectories", TenantColumn: "SectorId", MethodColumn: "Method", ConnectionColumn: "Connection"}
	data := func(directory *identityTenantDirectory, groups bool) *identitySourceData {
		inst := &identityInstance{auth: "Crew", tenant: &identityTenant{name: "Sector", table: "Sectors", key: "Id", word: "sector", directory: directory}, tables: map[identityTable]bool{identityUsers: true, identityMemberships: true}}
		d := &identitySourceData{
			Source: "Auth", Package: "crewresources", identityInstance: inst, ResourcesPackage: "crewresources", Outlets: "default",
			Tables: session.Tables{Users: "CrewSessionUsers"},
			Policy: access.Tables{UserRoles: "CrewUserRoles"},
		}
		if groups {
			d.groupSet = "crewGroups"
			d.tables[identityGroups], d.tables[identityGroupMembers], d.tables[identityGroupRoles] = true, true, true
			d.Policy.Groups, d.Policy.GroupMembers, d.Policy.GroupRoles = "CrewGroups", "CrewGroupMembers", "CrewGroupRoles"
		}

		return d
	}
	own := "const crewSessionUserAdministeredTenantsQuery = `\n" +
		"SELECT ta.UserId AS RowKey, ta.DomainId AS Tenant\n" +
		"FROM CrewUserRoles ta\n" +
		"WHERE ta.Kind = 'domain' AND ta.Axis = ''\n" +
		"  AND ta.Role IN UNNEST(@enteringAdministratorRoles)\n" +
		"  AND (ta.EndsAt IS NULL OR ta.EndsAt > CURRENT_TIMESTAMP())\n"
	declared := []string{
		"var crewSessionUserAdministrators atomic.Pointer[[]string]",
		"func SetCrewSessionUserAdministratorRoles(roles []accesstypes.Role) {",
		"administrators := []string{}",
		"Params: map[string]any{\"enteringAdministratorRoles\": administrators},",
		"func (CrewSessionUser) TenantsToEnter() resource.TenantsToEnter {\n\treturn crewSessionUserTenantsToEnter(\"Id\")\n}",
		"\"sync/atomic\"", "\"github.com/impulseframework/impulse/accesstypes\"", "\"github.com/impulseframework/impulse/resource\"",
	}
	tests := []struct {
		name            string
		data            *identitySourceData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "a tenant record that declares the switch, an auth that keeps no groups: one SELECT, of the person's own memberships",
			data: data(directory, false),
			wantContains: append([]string{
				// A role that has not begun counts, as the refusal counts it (Standing).
				own + "`",
			}, declared...),
			wantNotContains: []string{"UNION", "StartsAt"},
		},
		{
			name: "an auth that keeps groups also reads a role a group kept in the tenant holds there",
			data: data(directory, true),
			wantContains: append([]string{
				own + "UNION DISTINCT\n" +
					"SELECT tm.UserId AS RowKey, tg.DomainId AS Tenant\n" +
					"FROM CrewGroupRoles tr\n" +
					"JOIN CrewGroups tg ON tg.Id = tr.GroupId\n" +
					"JOIN CrewGroupMembers tm ON tm.GroupId = tr.GroupId\n" +
					"WHERE tg.Kind = 'domain' AND tg.Axis = ''\n" +
					"  AND tr.Kind = 'domain' AND tr.Axis = '' AND tr.DomainId = tg.DomainId\n" +
					"  AND tr.Role IN UNNEST(@enteringAdministratorRoles)\n" +
					"  AND (tr.EndsAt IS NULL OR tr.EndsAt > CURRENT_TIMESTAMP())\n" +
					"  AND (tm.EndsAt IS NULL OR tm.EndsAt > CURRENT_TIMESTAMP())\n`",
			}, declared...),
			wantNotContains: []string{"StartsAt"},
		},
		{
			name:            "a tenant record that declares no switch",
			data:            data(nil, true),
			wantContains:    []string{"CrewSessionUser struct {"},
			wantNotContains: []string{"AdministeredTenants", "enteringAdministratorRoles", "AdministratorRoles", "TenantsToEnter", "sync/atomic", "accesstypes", "impulse/resource\""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := (&client{}).generateTemplateOutput("identityUsersTemplate", identityUsersTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(out); err != nil {
				t.Fatalf("the rendered users table does not parse: %v\n%s", err, out)
			}
			assertContains(t, "the users table", string(out), tt.wantContains, tt.wantNotContains)
		})
	}
}

// Test_identityGroupTemplates_administeredTenants pins what the whole application's
// people of the groups and its listed groups say of the tenant a group makes its people
// administrators of, where the tenant record declares the directory-only switch: the
// query, a row for each group kept in a tenant that holds one of the engine's
// administrator roles there, in an assignment that has not ended; the helper over the
// roles the client sets; and each row's TenantsToEnter over the column that holds the
// group's id. The whole application's listed accounts answer it over the account's id.
// The tenant's people of its groups, and an application whose record declares no
// switch, say nothing of it.
func Test_identityGroupTemplates_administeredTenants(t *testing.T) {
	t.Parallel()

	directory := &identityTenantDirectory{OnlyColumn: "DirectoryOnly", Table: "SectorDirectories", TenantColumn: "SectorId", MethodColumn: "Method", ConnectionColumn: "Connection"}
	data := func(directory *identityTenantDirectory, forTenant bool) *identitySourceData {
		inst := &identityInstance{
			auth: "Crew", tenant: &identityTenant{name: "Sector", table: "Sectors", key: "Id", word: "sector", directory: directory}, groupSet: "crewGroups",
			tables: map[identityTable]bool{identityUsers: true, identityMemberships: true, identityGroups: true, identityGroupMembers: true, identityGroupRoles: true},
		}

		return &identitySourceData{
			Source: "Auth", Package: "crewresources", identityInstance: inst, ResourcesPackage: "crewresources", Outlets: "default", ForTenant: forTenant,
			Tables: session.Tables{Users: "CrewSessionUsers"},
			Policy: access.Tables{UserRoles: "CrewUserRoles", Groups: "CrewGroups", GroupMembers: "CrewGroupMembers", GroupRoles: "CrewGroupRoles"},
		}
	}
	none := []string{"AdministeredTenants", "enteringAdministratorRoles", "TenantsToEnter", "impulse/resource\""}
	tests := []struct {
		name            string
		template        string
		source          string
		data            *identitySourceData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:     "the whole application's people of the groups, with the switch declared",
			template: "identityGroupUsersTemplate", source: identityGroupUsersTemplate,
			data: data(directory, false),
			wantContains: []string{
				// The group's own role, held in the tenant the group is kept in, that has
				// not ended; one that has not begun counts, as the refusal counts it.
				"const crewGroupAdministeredTenantsQuery = `\n" +
					"SELECT tr.GroupId AS RowKey, tr.Kind AS Kind, tr.DomainId AS DomainId, tr.Role AS Role, tg.DomainId AS Tenant\n" +
					"FROM CrewGroupRoles tr\n" +
					"JOIN CrewGroups tg ON tg.Id = tr.GroupId\n" +
					"WHERE tg.Kind = 'domain' AND tg.Axis = ''\n" +
					"  AND tr.Kind = 'domain' AND tr.Axis = '' AND tr.DomainId = tg.DomainId\n" +
					"  AND tr.Role IN UNNEST(@enteringAdministratorRoles)\n" +
					"  AND (tr.EndsAt IS NULL OR tr.EndsAt > CURRENT_TIMESTAMP())\n`",
				"func crewGroupTenantsToEnter(column string) resource.TenantsToEnter {",
				// The roles, and the parameter they are bound under, are the people's.
				"enter := crewSessionUserTenantsToEnter(column)\n\tenter.Query = crewGroupAdministeredTenantsQuery\n",
				"func (CrewGroupUser) TenantsToEnter() resource.TenantsToEnter {\n\treturn crewGroupTenantsToEnter(\"GroupId\")\n}",
				"\"github.com/impulseframework/impulse/resource\"",
			},
			wantNotContains: []string{"StartsAt IS", "JOIN CrewGroupMembers tm", "func SetCrew"},
		},
		{
			name:     "the tenant's people of its groups say nothing of it",
			template: "identityGroupUsersTemplate", source: identityGroupUsersTemplate,
			data:            data(directory, true),
			wantContains:    []string{"CrewSectorGroupUser struct {"},
			wantNotContains: none,
		},
		{
			name:     "the people of the groups, with no switch declared",
			template: "identityGroupUsersTemplate", source: identityGroupUsersTemplate,
			data:            data(nil, false),
			wantContains:    []string{"CrewGroupUser struct {"},
			wantNotContains: none,
		},
		{
			name:     "the listed groups, with the switch declared",
			template: "identityListedGroupsTemplate", source: identityListedGroupsTemplate,
			data: data(directory, false),
			wantContains: []string{
				"func (CrewListedGroup) TenantsToEnter() resource.TenantsToEnter {\n\treturn crewGroupTenantsToEnter(\"Id\")\n}",
				"\"github.com/impulseframework/impulse/resource\"",
			},
		},
		{
			name:     "the listed groups, with no switch declared",
			template: "identityListedGroupsTemplate", source: identityListedGroupsTemplate,
			data:            data(nil, false),
			wantContains:    []string{"CrewListedGroup struct {"},
			wantNotContains: none,
		},
		{
			// The whole application's accounts answer it as the users table's
			// declaration does, over the column that holds the account's id.
			name:     "the listed accounts, with the switch declared",
			template: "identityListedUsersTemplate", source: identityListedUsersTemplate,
			data: data(directory, false),
			wantContains: []string{
				"func (CrewListedUser) TenantsToEnter() resource.TenantsToEnter {\n\treturn crewSessionUserTenantsToEnter(\"Id\")\n}",
				"\"github.com/impulseframework/impulse/resource\"",
			},
		},
		{
			name:     "the listed accounts, with no switch declared",
			template: "identityListedUsersTemplate", source: identityListedUsersTemplate,
			data:            data(nil, false),
			wantContains:    []string{"CrewListedUser struct {"},
			wantNotContains: none,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := (&client{}).generateTemplateOutput(tt.template, tt.source, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(out); err != nil {
				t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
			}
			assertContains(t, tt.template, string(out), tt.wantContains, tt.wantNotContains)
		})
	}
}

// Test_identityRoleTemplates_administeredTenants pins what the whole application's lists
// of the roles people hold, of the roles held for a set time and of the roles the groups
// hold say of the tenant a row's role administers, where the tenant record declares the
// directory-only switch: the query of the roles people hold of their own, a row for each
// that is one of the engine's administrator roles held in the tenant itself, named by the
// account, the kind, the tenant and the role; the helper over the roles the client sets;
// the two lists of a person's roles answering TenantsToEnter with it, by four columns and
// neither the group nor the axis; and the list of the groups' roles answering it with the
// group's query, by the group and the same three. With no switch declared, none of it; nor
// in the roles people hold where a directory writes the auth's roles, since the method that
// takes a role away is not written there.
func Test_identityRoleTemplates_administeredTenants(t *testing.T) {
	t.Parallel()

	directory := &identityTenantDirectory{OnlyColumn: "DirectoryOnly", Table: "SectorDirectories", TenantColumn: "SectorId", MethodColumn: "Method", ConnectionColumn: "Connection"}
	data := func(directory *identityTenantDirectory) *identitySourceData {
		inst := &identityInstance{
			auth: "Crew", tenant: &identityTenant{name: "Sector", table: "Sectors", key: "Id", word: "sector", directory: directory}, groupSet: "crewGroups",
			tables: map[identityTable]bool{identityUsers: true, identityMemberships: true, identityGroups: true, identityGroupMembers: true, identityGroupRoles: true},
		}

		return &identitySourceData{
			Source: "Auth", Package: "crewresources", identityInstance: inst, ResourcesPackage: "crewresources", Outlets: "default",
			Tables: session.Tables{Users: "CrewSessionUsers"},
			Policy: access.Tables{UserRoles: "CrewUserRoles", Groups: "CrewGroups", GroupMembers: "CrewGroupMembers", GroupRoles: "CrewGroupRoles"},
		}
	}
	// A directory that writes the auth's roles: no method takes a role away from a person.
	synced := data(directory)
	synced.rolesSynced = true
	imported := "\"github.com/impulseframework/impulse/resource\""
	none := []string{"AdministeredTenants", "enteringAdministratorRoles", "TenantsToEnter", "impulse/resource\""}
	personDoc := "// TenantsToEnter names the sector the row's role makes its holder an administrator of,\n" +
		"// where a person holds the role of their own in that sector itself: a caller whose\n" +
		"// session does not enter it is not offered the method that takes the role away.\n"
	tests := []struct {
		name            string
		template        string
		source          string
		data            *identitySourceData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:     "the roles people hold, with the switch declared",
			template: "identityHeldRolesTemplate", source: identityHeldRolesTemplate,
			data: data(directory),
			wantContains: []string{
				// A role of the person's own, held in the tenant itself, that has not
				// ended; one that has not begun counts, as the refusal counts it.
				"const crewHeldRoleAdministeredTenantsQuery = `\n" +
					"SELECT ta.UserId AS UserId, ta.Kind AS Kind, ta.DomainId AS DomainId, ta.Role AS Role, ta.DomainId AS Tenant\n" +
					"FROM CrewUserRoles ta\n" +
					"WHERE ta.Kind = 'domain' AND ta.Axis = ''\n" +
					"  AND ta.Role IN UNNEST(@enteringAdministratorRoles)\n" +
					"  AND (ta.EndsAt IS NULL OR ta.EndsAt > CURRENT_TIMESTAMP())\n`",
				// The roles, and the parameter they are bound under, are the people's;
				// four columns name the membership.
				"func crewHeldRoleTenantsToEnter() resource.TenantsToEnter {\n" +
					"\t// The roles are the people's: the same names, bound under the same parameter.\n" +
					"\tenter := crewSessionUserTenantsToEnter(\"UserId\")\n" +
					"\tenter.Query = crewHeldRoleAdministeredTenantsQuery\n" +
					"\tenter.Match = map[string]string{\"UserId\": \"UserId\", \"Kind\": \"Kind\", \"DomainId\": \"DomainId\", \"Role\": \"Role\"}\n" +
					"\n\treturn enter\n}",
				personDoc + "func (CrewHeldRole) TenantsToEnter() resource.TenantsToEnter {\n\treturn crewHeldRoleTenantsToEnter()\n}",
				imported,
			},
			// Neither the group a role comes through nor the axis names the membership.
			wantNotContains: []string{"\"GroupId\":", "\"Axis\":", "StartsAt IS"},
		},
		{
			name:     "the roles people hold, with no switch declared",
			template: "identityHeldRolesTemplate", source: identityHeldRolesTemplate,
			data:            data(nil),
			wantContains:    []string{"CrewHeldRole struct {"},
			wantNotContains: none,
		},
		{
			name:     "the roles people hold, of an auth whose directory writes its roles, with the switch declared",
			template: "identityHeldRolesTemplate", source: identityHeldRolesTemplate,
			data:            synced,
			wantContains:    []string{"CrewHeldRole struct {"},
			wantNotContains: none,
		},
		{
			name:     "the roles held for a set time, with the switch declared",
			template: "identityTakenRolesTemplate", source: identityTakenRolesTemplate,
			data: data(directory),
			wantContains: []string{
				personDoc + "func (CrewTakenRole) TenantsToEnter() resource.TenantsToEnter {\n\treturn crewHeldRoleTenantsToEnter()\n}",
				imported,
			},
			// The query and the helper are the held roles' file's.
			wantNotContains: []string{"AdministeredTenantsQuery", "func crewHeldRoleTenantsToEnter"},
		},
		{
			name:     "the roles held for a set time, with no switch declared",
			template: "identityTakenRolesTemplate", source: identityTakenRolesTemplate,
			data:            data(nil),
			wantContains:    []string{"CrewTakenRole struct {"},
			wantNotContains: none,
		},
		{
			name:     "the roles the groups hold, with the switch declared",
			template: "identityListedGroupRolesTemplate", source: identityListedGroupRolesTemplate,
			data: data(directory),
			wantContains: []string{
				"// TenantsToEnter names the sector the group is kept in, where the row's role is one\n" +
					"// of that sector's administrator roles held there: a caller whose session does not\n" +
					"// enter it is not offered the method that takes the role away from the group.\n" +
					"func (CrewListedGroupRole) TenantsToEnter() resource.TenantsToEnter {\n" +
					"\tenter := crewGroupTenantsToEnter(\"GroupId\")\n" +
					"\tenter.Match[\"Kind\"] = \"Kind\"\n" +
					"\tenter.Match[\"DomainId\"] = \"DomainId\"\n" +
					"\tenter.Match[\"Role\"] = \"Role\"\n" +
					"\n\treturn enter\n}",
				imported,
			},
			wantNotContains: []string{"enter.Match[\"Axis\"]", "AdministeredTenantsQuery"},
		},
		{
			name:     "the roles the groups hold, with no switch declared",
			template: "identityListedGroupRolesTemplate", source: identityListedGroupRolesTemplate,
			data:            data(nil),
			wantContains:    []string{"CrewListedGroupRole struct {"},
			wantNotContains: none,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := (&client{}).generateTemplateOutput(tt.template, tt.source, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(out); err != nil {
				t.Fatalf("the rendered file does not parse: %v\n%s", err, out)
			}
			assertContains(t, tt.template, string(out), tt.wantContains, tt.wantNotContains)
		})
	}
}

// Test_prepareIdentity_callerEnters pins the eight methods that change how a person
// signs in, or whether their account stands, on a tree written for a tenant record that
// declares the directory-only switch: on both sides each carries @callerEnters after its
// scope's line, with the sentence that says so, and each names a row whose struct
// answers TenantsToEnter over the column that holds the account's id. The whole
// application's two methods that put a person in a group and take one out carry it too,
// for the auth that keeps groups, with a sentence of their own, and each names a row
// whose struct answers it over the column that holds the group's id; no tenant's group
// method and no other method carries it, and the client hands the rows the engine's
// roles. The whole application's two methods that take a role away name no row: each
// lists the rows a page runs it from, with a sentence of its own, and each listed
// struct answers TenantsToEnter; the tenant's two carry nothing of it. Written for a
// record that declares no switch, the tree says nothing of it.
func Test_prepareIdentity_callerEnters(t *testing.T) {
	// As Test_prepareIdentity: the working directory is the process's, no t.Parallel.
	t.Chdir(t.TempDir())

	// The members keep groups and the crew does not.
	withGroups := func(r *resourceGenerator) {
		for _, table := range membersGroupTables {
			r.tableMap[table] = &tableMetadata{}
		}
	}
	if err := citadelGenerator(t, citadelTenantSource, withGroups).prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() error = %v", err)
	}
	// read answers a written source with each line's indentation taken off: a method
	// declared in a type block carries its comment one level in.
	read := func(file string) string {
		lines := strings.Split(identitySource(t, file), "\n")
		for i := range lines {
			lines[i] = strings.TrimLeft(lines[i], " ")
		}

		return strings.Join(lines, "\n")
	}

	// Each of the eight, on the whole application's side and the tenant's.
	held := map[string]string{
		"crewidentity/delete_crew_user.go":                     "// @rpc\n// @callerEnters\n// @notOnOwnAccount(ID)\n// @outlet(",
		"crewidentity/disable_crew_user.go":                    "// @rpc\n// @callerEnters\n// @notOnOwnAccount(ID)\n// @outlet(",
		"crewidentity/enable_crew_user.go":                     "// @rpc\n// @callerEnters\n// @outlet(",
		"crewidentity/remove_crew_password.go":                 "// @rpc\n// @callerEnters\n// @outlet(",
		"crewidentity/rename_crew_user.go":                     "// @rpc\n// @callerEnters\n// @outlet(",
		"crewidentity/require_crew_password_change.go":         "// @rpc\n// @callerEnters\n// @outlet(",
		"crewidentity/set_crew_user_email.go":                  "// @rpc\n// @callerEnters\n// @outlet(",
		"crewidentity/unlink_crew_session_identity.go":         "// @rpc\n// @callerEnters\n// @outlet(",
		"crewidentity/delete_crew_citadel_user.go":             "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @notOnOwnAccount(ID)\n// @outlet(",
		"crewidentity/disable_crew_citadel_user.go":            "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @notOnOwnAccount(ID)\n// @outlet(",
		"crewidentity/enable_crew_citadel_user.go":             "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @outlet(",
		"crewidentity/remove_crew_citadel_password.go":         "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @outlet(",
		"crewidentity/rename_crew_citadel_user.go":             "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @outlet(",
		"crewidentity/require_crew_citadel_password_change.go": "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @outlet(",
		"crewidentity/set_crew_citadel_user_email.go":          "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @outlet(",
		"crewidentity/unlink_crew_citadel_session_identity.go": "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @outlet(",
		"membersidentity/delete_members_user.go":               "// @rpc\n// @callerEnters\n// @notOnOwnAccount(ID)\n// @outlet(",
		"membersidentity/disable_members_user.go":              "// @rpc\n// @callerEnters\n// @notOnOwnAccount(ID)\n// @outlet(",
		"membersidentity/enable_members_user.go":               "// @rpc\n// @callerEnters\n// @outlet(",
		"membersidentity/rename_members_user.go":               "// @rpc\n// @callerEnters\n// @outlet(",
		"membersidentity/set_members_user_email.go":            "// @rpc\n// @callerEnters\n// @outlet(",
		"membersidentity/delete_members_citadel_user.go":       "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @notOnOwnAccount(ID)\n// @outlet(",
		"membersidentity/disable_members_citadel_user.go":      "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @notOnOwnAccount(ID)\n// @outlet(",
		"membersidentity/enable_members_citadel_user.go":       "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @outlet(",
		"membersidentity/rename_members_citadel_user.go":       "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @outlet(",
		"membersidentity/set_members_citadel_user_email.go":    "// @rpc\n// @permissionScope(domain)\n// @callerEnters\n// @outlet(",
		// The whole application's two group methods, of the auth that keeps groups.
		"membersidentity/add_members_group_member.go":    "// @rpc\n// @callerEnters\n// @outlet(",
		"membersidentity/remove_members_group_member.go": "// @rpc\n// @callerEnters\n// @outlet(",
	}
	personNote := "// A row offers it only to a caller whose session enters every citadel the person\n// administers (@callerEnters); the method refuses anyone else.\n//\n// @rpc"
	groupNote := "// Where the group is kept in a citadel and holds one of that citadel's\n// administrator roles there, a row offers it only to a caller whose session enters the\n// citadel (@callerEnters); the method refuses anyone else.\n//\n// @rpc"
	// The removal's comment goes on to say that it asks first.
	groupRemovalNote := "// Where the group is kept in a citadel and holds one of that citadel's\n// administrator roles there, a row offers it only to a caller whose session enters the\n// citadel (@callerEnters); the method refuses anyone else.\n//\n" +
		"// The method declares a warning with no word to type: asked first, the server says who\n// leaves which group, and, where the place is the last thing the person holds, that the\n// account is deleted with it.\n//\n// @rpc"
	notes := map[string]string{
		"membersidentity/add_members_group_member.go":    groupNote,
		"membersidentity/remove_members_group_member.go": groupRemovalNote,
	}
	files := identityTreeFiles(t, "pkg/identity")
	var marked []string
	for _, file := range files {
		if strings.Contains(read(file), "\n// @callerEnters\n") {
			marked = append(marked, file)
		}
	}
	want := make([]string, 0, len(held))
	for file := range held {
		want = append(want, file)
	}
	slices.Sort(want)
	if diff := cmp.Diff(want, marked); diff != "" {
		t.Errorf("the methods that carry @callerEnters (-want +got):\n%s", diff)
	}

	// A method names its row by the row's struct, alone or with the column of the row's
	// key the field carries.
	target := regexp.MustCompile(`// @target\((\w+)(?:, key: \w+)?\)`)
	for file, annotations := range held {
		source := read(file)
		note, ok := notes[file]
		if !ok {
			note = personNote
		}
		for _, line := range []string{annotations, note} {
			if !strings.Contains(source, line) {
				t.Errorf("%s lacks %q:\n%s", file, line, source)
			}
		}
		// The row the method names answers TenantsToEnter in its one form, where the
		// generator's check of @callerEnters looks for it.
		named := target.FindStringSubmatch(source)
		if named == nil {
			t.Errorf("%s names no row by @target:\n%s", file, source)

			continue
		}
		declaration := "func (" + named[1] + ") TenantsToEnter() resource.TenantsToEnter {"
		declaring := slices.DeleteFunc(slices.Clone(files), func(other string) bool {
			return !strings.Contains(read(other), declaration)
		})
		if len(declaring) != 1 {
			t.Errorf("%s names %s, declared with TenantsToEnter in %v, want in one file", file, named[1], declaring)
		}
	}

	// The six structs answer it over the column that holds the account's id, and the
	// whole application's accounts, which its methods on a person name, are one of them.
	for file, lines := range map[string][]string{
		"internal/crewresources/crew_session_users.go": {
			"const crewSessionUserAdministeredTenantsQuery = `",
			"func SetCrewSessionUserAdministratorRoles(roles []accesstypes.Role) {",
			"func (CrewSessionUser) TenantsToEnter() resource.TenantsToEnter {\nreturn crewSessionUserTenantsToEnter(\"Id\")\n}",
		},
		"internal/crewresources/crew_session_identities.go": {"func (CrewSessionIdentity) TenantsToEnter() resource.TenantsToEnter {\nreturn crewSessionUserTenantsToEnter(\"UserId\")\n}"},
		"internal/crewresources/crew_listed_identities.go":  {"func (CrewListedIdentity) TenantsToEnter() resource.TenantsToEnter {\nreturn crewSessionUserTenantsToEnter(\"UserId\")\n}"},
		"internal/crewresources/crew_listed_users.go":       {"func (CrewListedUser) TenantsToEnter() resource.TenantsToEnter {\nreturn crewSessionUserTenantsToEnter(\"Id\")\n}"},
		"internal/crewresources/crew_citadel_users.go":      {"func (CrewCitadelUser) TenantsToEnter() resource.TenantsToEnter {\nreturn crewSessionUserTenantsToEnter(\"Id\")\n}"},
		"internal/crewresources/crew_citadel_identities.go": {"func (CrewCitadelIdentity) TenantsToEnter() resource.TenantsToEnter {\nreturn crewSessionUserTenantsToEnter(\"UserId\")\n}"},
		"crewidentity/client.go":                            {"crewresources.SetCrewSessionUserAdministratorRoles(manager.TenantAdministratorRoles())"},
		"membersidentity/client.go":                         {"membersresources.SetMembersSessionUserAdministratorRoles(manager.TenantAdministratorRoles())"},
		// The two group structs answer it over the column that holds the group's id.
		"internal/membersresources/members_group_users.go": {
			"const membersGroupAdministeredTenantsQuery = `",
			"func (MembersGroupUser) TenantsToEnter() resource.TenantsToEnter {\nreturn membersGroupTenantsToEnter(\"GroupId\")\n}",
		},
		"internal/membersresources/members_listed_groups.go": {"func (MembersListedGroup) TenantsToEnter() resource.TenantsToEnter {\nreturn membersGroupTenantsToEnter(\"Id\")\n}"},
		"internal/membersresources/members_listed_users.go":  {"func (MembersListedUser) TenantsToEnter() resource.TenantsToEnter {\nreturn membersSessionUserTenantsToEnter(\"Id\")\n}"},
	} {
		source := read(file)
		for _, line := range lines {
			if !strings.Contains(source, line) {
				t.Errorf("%s lacks %q:\n%s", file, line, source)
			}
		}
	}
	// The whole application's two methods that take a role away name no row: each lists
	// the rows a page runs it from, the roles held for a set time only where the auth's
	// people take one, and the group's only for the auth that keeps groups.
	roleNote := "// It names no row: a page runs it from a row of the roles people hold, filling the\n" +
		"// request from the row. Where the role is one of a citadel's administrator roles held\n" +
		"// in that citadel itself, such a row offers it only to a caller whose session enters\n" +
		"// the citadel (@callerEnters); the method refuses anyone else.\n//\n// The method declares a warning"
	groupRoleNote := "// Where the group is kept in a citadel and the role is one of that citadel's\n" +
		"// administrator roles held there, a row of the roles the groups hold offers it only to a\n" +
		"// caller whose session enters the citadel (@callerEnters); the method refuses anyone\n" +
		"// else.\n//\n// @rpc"
	offered := map[string][]string{
		"crewidentity/remove_crew_role.go":           {"// @rpc\n// @callerEnters(CrewHeldRole, CrewTakenRole)\n// @outlet(", roleNote},
		"membersidentity/remove_members_role.go":     {"// @rpc\n// @callerEnters(MembersHeldRole, MembersTakenRole)\n// @outlet(", roleNote},
		"membersidentity/take_members_group_role.go": {"// @rpc\n// @callerEnters(MembersListedGroupRole)\n// @outlet(", groupRoleNote},
	}
	var listing []string
	for _, file := range files {
		if strings.Contains(read(file), "// @callerEnters(") {
			listing = append(listing, file)
		}
	}
	wantListing := make([]string, 0, len(offered))
	for file := range offered {
		wantListing = append(wantListing, file)
	}
	slices.Sort(wantListing)
	if diff := cmp.Diff(wantListing, listing); diff != "" {
		t.Errorf("the methods that list the rows that offer them (-want +got):\n%s", diff)
	}
	listed := regexp.MustCompile(`// @callerEnters\(([\w, ]+)\)`)
	for file, lines := range offered {
		source := read(file)
		for _, line := range lines {
			if !strings.Contains(source, line) {
				t.Errorf("%s lacks %q:\n%s", file, line, source)
			}
		}
		named := listed.FindStringSubmatch(source)
		if named == nil {
			continue
		}
		for name := range strings.SplitSeq(named[1], ", ") {
			declaration := "func (" + name + ") TenantsToEnter() resource.TenantsToEnter {"
			declaring := slices.DeleteFunc(slices.Clone(files), func(other string) bool {
				return !strings.Contains(read(other), declaration)
			})
			if len(declaring) != 1 {
				t.Errorf("%s lists %s, declared with TenantsToEnter in %v, want in one file", file, name, declaring)
			}
		}
	}

	// An auth that keeps no groups reads no group.
	if source := identitySource(t, "internal/crewresources/crew_session_users.go"); strings.Contains(source, "UNION") {
		t.Errorf("the crew keeps no groups, and its query reads them:\n%s", source)
	}

	// Written for a tenant record that declares no switch, the tree says nothing of it.
	r := citadelGenerator(t, shipTenantSource, withGroups)
	r.tableMap["Ships"] = shipsTable()
	if err := r.prepareIdentity(); err != nil {
		t.Fatalf("prepareIdentity() without the switch error = %v", err)
	}
	for _, file := range identityTreeFiles(t, "pkg/identity") {
		source := identitySource(t, file)
		for _, word := range []string{"@callerEnters", "TenantsToEnter", "enteringAdministratorRoles", "AdministeredTenants", "SessionUserAdministrator"} {
			if strings.Contains(source, word) {
				t.Errorf("%s, written for a record that declares no switch, holds %q:\n%s", file, word, source)
			}
		}
	}
}
