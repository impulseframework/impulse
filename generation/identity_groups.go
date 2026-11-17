package generation

import (
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// Groups of people in an auth's generated management. Where the application gives the
// auth's roles by hand and the schema holds the permission store's three group tables,
// the generated management declares them and serves what manages them:
//
//   - the declarations over the groups, their members and the roles each group holds.
//     The members' declaration carries the subject set of a person's groups, so a grant's
//     condition and another table's subject set read the groups a caller is in
//     (subject.groups) with nothing special in the lowering;
//   - the lists a page draws from: the people of a group with their names, and the roles
//     each person holds through a group, for the whole application and, where the
//     application has a tenant record, one tenant's own groups, their people and their
//     roles;
//   - the methods that create, describe and delete a group, put a person in one and take
//     one out, and give a group a role and take it away, each a call into the identity
//     package's rules. A tenant's methods reach the groups kept in the tenant alone.
//
// An auth whose directory writes its roles keeps no groups: the directory's groups do
// that job there, so nothing of this is written for it, whatever tables the schema holds.

// The kinds of the group tables.
const (
	identityGroups       identityTable = "groups"
	identityGroupMembers identityTable = "groupMembers"
	identityGroupRoles   identityTable = "groupRoles"
)

// identityGroupTables are the group tables, which a policy store keeps together.
func identityGroupTables() []identityTable {
	return []identityTable{identityGroups, identityGroupMembers, identityGroupRoles}
}

// defaultGroupSet is the name of the subject set of a person's groups, where one auth
// of the application keeps groups.
const defaultGroupSet = "groups"

// The suffixes of the lists over groups, each what the list holds: the people of a group,
// the roles a person holds through a group, and, for one tenant, its own groups and the
// roles they hold.
const (
	listGroupsSuffix        = "Group"
	listGroupPeopleSuffix   = "GroupUser"
	listGroupRolesSuffix    = "GroupRole"
	listThroughGroupsSuffix = "UserGroupRole"
)

// settleGroupTables holds the instance's group tables to the rule that a policy store
// keeps them together, and takes them out of an instance that keeps no groups: an auth
// whose directory writes its roles, and one whose schema holds no memberships to fold a
// group's roles into.
func (i *identityInstance) settleGroupTables() error {
	held := 0
	for _, kind := range identityGroupTables() {
		if i.has(kind) {
			held++
		}
	}
	if held != 0 && held != len(identityGroupTables()) {
		return errors.Newf("Auth(%q): the schema holds some of %s, %s and %s and not the others; they are the permission store's tables of the groups of people under the auth's prefix and are migrated together",
			i.importPath, i.policy.Groups, i.policy.GroupMembers, i.policy.GroupRoles)
	}
	if i.rolesSynced || !i.has(identityMemberships) {
		for _, kind := range identityGroupTables() {
			delete(i.tables, kind)
		}
	}

	return nil
}

// keepsGroups reports whether the instance manages groups of people: the application
// gives the auth's roles by hand, and the schema holds the group tables.
func (i *identityInstance) keepsGroups() bool {
	return i.has(identityGroups)
}

// nameGroupSets names the subject set of a person's groups for each instance that keeps
// groups. subject.<name> is one namespace for the whole application, so the one auth
// that keeps groups declares "groups", and where several do each declares its own, the
// auth's name before it (crewGroups, staffGroups).
func nameGroupSets(instances []*identityInstance) {
	var keeping []*identityInstance
	for _, inst := range instances {
		if inst.keepsGroups() {
			keeping = append(keeping, inst)
		}
	}
	for _, inst := range keeping {
		inst.groupSet = defaultGroupSet
		if len(keeping) > 1 {
			inst.groupSet = strings.ToLower(inst.auth[:1]) + inst.auth[1:] + "Groups"
		}
	}
}

// groupListName names a list over groups: the whole application's after the auth's name
// (CrewGroupUser), and a tenant's after the auth's and the tenant record's
// (CrewSectorGroupUser).
func (i *identityInstance) groupListName(suffix string, tenant bool) string {
	if tenant {
		return i.listName(suffix)
	}

	return i.auth + suffix
}

// groupLists are the lists over groups the instance carries: the whole application's
// people of a group and roles held through groups, and one tenant's groups, their
// people, their roles and the roles its people hold through a group.
func (i *identityInstance) groupLists() []identitySourceFile {
	if !i.keepsGroups() {
		return nil
	}
	needs := append(identityGroupTables(), identityUsers)
	list := func(name, template string, tenant bool) identitySourceFile {
		return identitySourceFile{dir: i.resources, name: name, template: template, needs: needs, tenant: tenant}
	}
	files := []identitySourceFile{
		list(i.groupListName(listGroupPeopleSuffix, false), identityGroupUsersTemplate, false),
		list(i.groupListName(listThroughGroupsSuffix, false), identityUserGroupRolesTemplate, false),
	}
	if i.tenant != nil {
		files = append(files,
			list(i.groupListName(listGroupsSuffix, true), identityTenantGroupsTemplate, true),
			list(i.groupListName(listGroupPeopleSuffix, true), identityGroupUsersTemplate, true),
			list(i.groupListName(listGroupRolesSuffix, true), identityTenantGroupRolesTemplate, true),
			list(i.groupListName(listThroughGroupsSuffix, true), identityUserGroupRolesTemplate, true),
		)
	}

	return files
}

// groupListSources are the source files of the instance's lists over groups, each named
// for its list in the plural.
func (r *resourceGenerator) groupListSources(inst *identityInstance) []identitySourceFile {
	files := inst.groupLists()
	for i := range files {
		files[i].name = fileStem(r.pluralize(files[i].name))
	}

	return files
}

// groupListNames are the names of the instance's lists over groups, the ones a
// declaration or another list must not take.
func (i *identityInstance) groupListNames() []string {
	files := i.groupLists()
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.name)
	}

	return names
}

// groupMethodSources are the source files of the methods that manage groups: the whole
// application's, and one tenant's where the application has a tenant record.
func (i *identityInstance) groupMethodSources() []identitySourceFile {
	if !i.keepsGroups() {
		return nil
	}
	needs := append(identityGroupTables(), identityUsers)
	sides := []bool{false}
	if i.tenant != nil {
		sides = append(sides, true)
	}
	var files []identitySourceFile
	for _, tenant := range sides {
		subject := i.subject(tenant)
		method := func(name, template string) identitySourceFile {
			return identitySourceFile{dir: i.methods, name: fileStem(name), template: template, needs: needs, tenant: tenant}
		}
		files = append(files,
			method("Create"+subject+"Group", identityCreateGroupTemplate),
			method("Describe"+subject+"Group", identityDescribeGroupTemplate),
			method("Delete"+subject+"Group", identityDeleteGroupTemplate),
			method("Add"+subject+"GroupMember", identityAddGroupMemberTemplate),
			method("Remove"+subject+"GroupMember", identityRemoveGroupMemberTemplate),
			method("Give"+subject+"GroupRole", identityGiveGroupRoleTemplate),
			method("Take"+subject+"GroupRole", identityTakeGroupRoleTemplate),
		)
	}

	return files
}

// Singular names of the instance's declarations over the group tables.
func (d *identitySourceData) Group() string       { return d.auth + "Group" }
func (d *identitySourceData) GroupMember() string { return d.auth + "GroupMember" }
func (d *identitySourceData) GroupRole() string   { return d.auth + "GroupRole" }

// KeepsGroups reports whether the instance manages groups of people.
func (d *identitySourceData) KeepsGroups() bool { return d.keepsGroups() }

// GroupSet is the name of the subject set of a person's groups.
func (d *identitySourceData) GroupSet() string { return d.groupSet }

// SubjectGroup is the struct whose row a method names a group by: the groups table's
// for the whole application, and the tenant's list of its own groups for a tenant.
func (d *identitySourceData) SubjectGroup() string {
	if d.ForTenant {
		return d.groupListName(listGroupsSuffix, true)
	}

	return d.Group()
}

// SubjectGroupUser is the list of the people of a group the file is written for: every
// group's for the whole application, and the groups of one tenant for a tenant.
func (d *identitySourceData) SubjectGroupUser() string {
	return d.groupListName(listGroupPeopleSuffix, d.ForTenant)
}

// SubjectGroupRole is the struct whose row names a role a group holds: the group roles
// table's for the whole application, and the tenant's list of its groups' roles for a
// tenant.
func (d *identitySourceData) SubjectGroupRole() string {
	if d.ForTenant {
		return d.groupListName(listGroupRolesSuffix, true)
	}

	return d.GroupRole()
}

// SubjectUserGroupRole is the list of the roles people hold through a group the file is
// written for.
func (d *identitySourceData) SubjectUserGroupRole() string {
	return d.groupListName(listThroughGroupsSuffix, d.ForTenant)
}

// SuperAdministratorRole is the role no group gives, as a SQL string literal: the lists
// of what people hold through groups leave out a row that names it, as the permission
// engine does.
func (d *identitySourceData) SuperAdministratorRole() string {
	return sqlStringLiteral(string(accesstypes.SuperAdministrator))
}
