package generation

import (
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// Groups of people in an auth's generated management. Where the application gives the
// auth's roles by hand and the schema holds the permission store's three group tables,
// the generated management declares them and serves what manages them:
//
//   - the declarations over the groups, their members and the roles each group holds.
//     The members' declaration carries the subject set of a person's groups, so a grant's
//     condition and another table's subject set read the groups a caller is in
//     (subject.crewGroups for the auth crew) with nothing special in the lowering. The
//     set names its own way to a group's tenant, so in a tenant's request it holds that
//     tenant's groups alone;
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

// groupTenantColumn is the column of an auth's groups table that holds the tenant a
// group is kept in, empty for a group kept for the whole application: the permission
// store writes it, and the declaration over the table names it DomainID
// (identityGroupsTemplate). It is where the path of a set's domain: ends when it ends
// on the table (checkSubjectDomainEnds), the members' own set among them.
const groupTenantColumn = "DomainId"

// The suffixes of the lists over groups, each what the list holds: the people of a group,
// the roles a person holds through a group, and, for one tenant, its own groups and the
// roles they hold.
const (
	listGroupsSuffix        = "Group"
	listGroupPeopleSuffix   = "GroupUser"
	listGroupRolesSuffix    = "GroupRole"
	listThroughGroupsSuffix = "UserGroupRole"
)

// settleGroupTables takes the group tables out of an instance whose auth keeps no groups,
// as the auth's store says (recordRoleStore): an auth whose directory writes its roles,
// one whose schema holds no memberships to fold a group's roles into, and one whose schema
// holds no group tables. The instance's set of a person's groups is the store's.
func (i *identityInstance) settleGroupTables(store *resource.RecordRoleStoreData) {
	if store.GroupSet == "" {
		for _, kind := range identityGroupTables() {
			delete(i.tables, kind)
		}
	}
	i.groupSet = store.GroupSet
}

// keepsGroups reports whether the instance manages groups of people: the application
// gives the auth's roles by hand, and the schema holds the group tables.
func (i *identityInstance) keepsGroups() bool {
	return i.has(identityGroups)
}

// recordRoleStore is where auth keeps the roles held on records, read from the schema by
// the auth's prefix (TablePrefix): its table of people's roles held on records and, where
// the auth keeps groups, its table of groups' roles held on records, its groups' and
// their members' tables, and the subject set of a person's groups. An auth keeps groups
// where the schema holds its three group tables beside its memberships and no directory
// writes its roles; some of the three tables without the others is refused, since the
// permission store migrates them together. The set of a person's groups is named after
// its auth (crewGroups, staffGroups): subject.<name> is one namespace for the whole
// application, and a name that never depends on how many auths keep groups never collides
// when two sites meet and never changes when a second auth arrives. newIdentityInstance
// and the registration of the roles held on records (collectRecordRoleRegistrations) both
// call it, so a run that generates the auth's management and a run that does not say the
// same of the auth.
func (r *resourceGenerator) recordRoleStore(auth *outletAuth) (resource.RecordRoleStoreData, error) {
	prefix := caser.ToPascal(auth.packageName())
	policy, err := access.TablesWithPrefix(prefix, "")
	if err != nil {
		return resource.RecordRoleStoreData{}, errors.Wrapf(err, "Auth(%q): access.TablesWithPrefix()", auth.importPath)
	}
	store := resource.RecordRoleStoreData{Auth: auth.packageName(), UserRoles: policy.RecordUserRoles}
	held := 0
	for _, table := range []string{policy.Groups, policy.GroupMembers, policy.GroupRoles} {
		if _, ok := r.tableMap[table]; ok {
			held++
		}
	}
	if held != 0 && held != 3 {
		return resource.RecordRoleStoreData{}, errors.Newf("Auth(%q): the schema holds some of %s, %s and %s and not the others; they are the permission store's tables of the groups of people under the auth's prefix and are migrated together",
			auth.importPath, policy.Groups, policy.GroupMembers, policy.GroupRoles)
	}
	if _, memberships := r.tableMap[policy.UserRoles]; held == 0 || !memberships || auth.syncsRoles() {
		return store, nil
	}
	store.GroupRoles, store.GroupSet = policy.RecordGroupRoles, strings.ToLower(prefix[:1])+prefix[1:]+"Groups"
	store.GroupMembers, store.Groups = policy.GroupMembers, policy.Groups

	return store, nil
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
	// The answers the rules of a group ask of the role file are one file for both sides,
	// which an instance that picks a default role is written too (roleGrantsSource).
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

// TenantPeople reports whether one tenant keeps lists of the instance's people and of
// the roles they hold there: the application has a tenant record and the schema holds
// the tables the two lists read. The tenant's methods that give a role and take one
// away name their rows.
func (d *identitySourceData) TenantPeople() bool {
	return d.tenant != nil && d.hasList(listPeopleSuffix) && d.hasList(listRolesSuffix)
}

// TenantSubject names whose method a tenant's method is, whichever side the file is
// written for: the auth's name and the tenant record's (CrewSector).
func (d *identitySourceData) TenantSubject() string {
	return d.subject(true)
}

// RoleMethods names the methods whose grants judge a change to a group, as a comment
// reads them: the tenant's for a tenant's method, the whole application's otherwise.
func (d *identitySourceData) RoleMethods() string {
	subject := d.subject(d.ForTenant)

	return "Assign" + subject + "Role and Remove" + subject + "Role"
}

// GroupSet is the name of the subject set of a person's groups.
func (d *identitySourceData) GroupSet() string { return d.groupSet }

// SubjectGroup is the struct whose row a method names a group by: the list of the
// groups a page shows, the whole application's (identity_holdings.go) or the tenant's
// own. A method so names the row the page offers it on, and the server answers for that
// row whether the caller may run it.
func (d *identitySourceData) SubjectGroup() string {
	if d.ForTenant {
		return d.groupListName(listGroupsSuffix, true)
	}

	return d.ListedGroup()
}

// TenantGroup is the struct of the groups kept in one tenant.
func (d *identitySourceData) TenantGroup() string {
	return d.groupListName(listGroupsSuffix, true)
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
