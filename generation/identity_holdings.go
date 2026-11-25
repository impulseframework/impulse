package generation

import (
	"fmt"
	"slices"
	"strings"
)

// What is held and why, in an auth's generated management. Beside the declarations over
// the permission store's tables, the generated management declares the lists a page
// says it from, each a view like a tenant's lists (identity_lists.go):
//
//   - a person's roles as one list, each with where it comes from: a membership of the
//     person's own, with its period and the reason it was given for, or a group the
//     person is in, which the row names. A tenant's list of the roles held there holds
//     both kinds of row, and the whole application's is a view beside the memberships
//     table (<Auth>HeldRole);
//   - the groups with what each holds: how many people and which roles, a line each. A
//     tenant's list of its groups carries the two, and the whole application's is a
//     view beside the groups table (<Auth>ListedGroup), which the whole application's
//     methods name a group by;
//   - the accounts with what each holds, a line for each role, and until when a sign-in
//     is locked: a tenant's list of its people carries the two, and the whole
//     application's is a view beside the users table (<Auth>ListedUser), which the whole
//     application's methods name a person by;
//   - who manages each account, where the application has a tenant record: the accounts'
//     view says whether a tenant does and which (managedByTenant, managingTenant), read as
//     a tenant's list of its people reads managedHere (identity_lists.go), and the whole
//     application's sign-in identities are a view beside the identities table that
//     carries the same two (<Auth>ListedIdentity);
//   - the roles a group holds, each with how many people lose it when the group stops
//     holding it: a tenant's list carries the count, and the whole application's is a
//     view beside the group roles table (<Auth>ListedGroupRole);
//   - the roles held for a set time with a reason (<Auth>TakenRole), which the Break
//     glass page lists, the method a person takes one by (Take<Auth>Role), and the two
//     lists its form picks from: the roles (<Auth>TakeableRole) and the lengths
//     (<Auth>TakeLength);
//   - the custom roles' grants, each with why the permission engine leaves it out where
//     it does (<Auth>ListedRoleGrant): a computed list in the methods package over the
//     role grants table, which no route lists itself.
//
// The counts read the present from the database's clock, as every list does. What a
// group holds is what has not ended, a row whose period has not begun among it; who loses
// a role is counted among what counts now, since nobody loses what they do not hold yet.

// The suffixes of the structs declared here, each after the auth's name.
const (
	heldRoleSuffix        = "HeldRole"
	listedGroupSuffix     = "ListedGroup"
	listedUserSuffix      = "ListedUser"
	listedIdentitySuffix  = "ListedIdentity"
	listedGroupRoleSuffix = "ListedGroupRole"
	takenRoleSuffix       = "TakenRole"
	takeableRoleSuffix    = "TakeableRole"
	takeLengthSuffix      = "TakeLength"
	listedRoleGrantSuffix = "ListedRoleGrant"
)

// The keys of the lists and the method of the Break glass page, as the description
// carries them.
const (
	identityListTakenRoles = "takenRoles"
	identityMethodTakeRole = "takeRole"
)

// listsIdentities reports whether the instance declares the whole application's list of
// the sign-in identities that says who manages each account: the application has a tenant
// record, and the schema holds the accounts, the memberships and the identities.
func (i *identityInstance) listsIdentities() bool {
	return i.tenant != nil && i.has(identityUsers) && i.has(identityMemberships) && i.has(identityIdentities)
}

// listsHeldRoles reports whether the instance declares the whole application's one list
// of a person's roles: the schema holds the accounts and the memberships.
func (i *identityInstance) listsHeldRoles() bool {
	return i.has(identityUsers) && i.has(identityMemberships)
}

// takesRoles reports whether a person of the instance takes a role for a set time: the
// application gives the auth's roles by hand, and the schema holds the accounts and the
// memberships. An auth whose directory writes its roles gives none by hand, so nobody
// takes one there.
func (i *identityInstance) takesRoles() bool {
	return !i.rolesSynced && i.has(identityUsers) && i.has(identityMemberships)
}

// holdingSources are the source files of the whole application's lists of what is held
// and of the taking of a role: the views in the declarations package, and in the methods
// package the list of the custom roles' grants, the method and the two lists its form
// picks from. The groups with what each
// holds are written only where the auth keeps groups: each of the two files needs the
// group tables, and a file is written only where the instance holds every table it needs
// (writeIdentitySources). The sign-in identities with who manages each account are
// written only where the application has a tenant record: with none there is no tenant
// to name.
func (r *resourceGenerator) holdingSources(inst *identityInstance) []identitySourceFile {
	view := func(suffix, template string, needs ...identityTable) identitySourceFile {
		return identitySourceFile{dir: inst.resources, name: fileStem(r.pluralize(inst.auth + suffix)), template: template, needs: needs}
	}
	listed := append(identityGroupTables(), identityUsers, identityMemberships)
	files := []identitySourceFile{
		view(heldRoleSuffix, identityHeldRolesTemplate, identityUsers, identityMemberships),
		view(listedUserSuffix, identityListedUsersTemplate, identityUsers, identityMemberships),
		view(listedGroupSuffix, identityListedGroupsTemplate, listed...),
		view(listedGroupRoleSuffix, identityListedGroupRolesTemplate, listed...),
		{dir: inst.methods, name: fileStem(r.pluralize(inst.auth + listedRoleGrantSuffix)), template: identityListedRoleGrantsTemplate, needs: []identityTable{identityRoleGrants}},
	}
	if inst.listsIdentities() {
		files = append(files, view(listedIdentitySuffix, identityListedIdentitiesTemplate, identityUsers, identityMemberships, identityIdentities))
	}
	if inst.takesRoles() {
		needs := []identityTable{identityUsers, identityMemberships}
		files = append(files,
			view(takenRoleSuffix, identityTakenRolesTemplate, needs...),
			identitySourceFile{dir: inst.methods, name: fileStem(r.pluralize(inst.auth + takeableRoleSuffix)), template: identityTakeableRolesTemplate, needs: needs},
			identitySourceFile{dir: inst.methods, name: fileStem(r.pluralize(inst.auth + takeLengthSuffix)), template: identityTakeLengthsTemplate, needs: needs},
			identitySourceFile{dir: inst.methods, name: fileStem(inst.takeRoleMethod()), template: identityTakeRoleTemplate, needs: needs},
		)
	}

	return files
}

// holdingNames are the names of the structs declared here, the ones a tenant's list must
// not take.
func (i *identityInstance) holdingNames() []string {
	names := make([]string, 0, 9)
	for _, suffix := range []string{heldRoleSuffix, listedGroupSuffix, listedUserSuffix, listedIdentitySuffix, listedGroupRoleSuffix, takenRoleSuffix, takeableRoleSuffix, takeLengthSuffix, listedRoleGrantSuffix} {
		names = append(names, i.auth+suffix)
	}

	return names
}

// takeRoleMethod names the method a person takes a role for a set time by.
func (i *identityInstance) takeRoleMethod() string {
	return "Take" + i.auth + "Role"
}

// HeldRole is the struct of the one list of the roles people hold.
func (d *identitySourceData) HeldRole() string {
	return d.auth + heldRoleSuffix
}

// ListedGroup is the struct of the groups as a list shows them, each with what it holds.
func (d *identitySourceData) ListedGroup() string {
	return d.auth + listedGroupSuffix
}

// ListedUser is the struct of the accounts as a list shows them, each with what it holds.
func (d *identitySourceData) ListedUser() string {
	return d.auth + listedUserSuffix
}

// ListedIdentity is the struct of the sign-in identities as the whole application's list
// shows them, each with who manages the person's account.
func (d *identitySourceData) ListedIdentity() string {
	return d.auth + listedIdentitySuffix
}

// ListedGroupRole is the struct of the roles the groups hold as a list shows them, each
// with who loses it.
func (d *identitySourceData) ListedGroupRole() string {
	return d.auth + listedGroupRoleSuffix
}

// ListedRoleGrant is the struct of the custom roles' grants as a list shows them, each
// with whether the permission engine leaves it out.
func (d *identitySourceData) ListedRoleGrant() string {
	return d.auth + listedRoleGrantSuffix
}

// TakenRole is the struct of the roles held for a set time with a reason.
func (d *identitySourceData) TakenRole() string {
	return d.auth + takenRoleSuffix
}

// TakeableRole is the struct of the roles a person may take for a set time.
func (d *identitySourceData) TakeableRole() string {
	return d.auth + takeableRoleSuffix
}

// TakeLength is the struct of the lengths of time a role is taken for.
func (d *identitySourceData) TakeLength() string {
	return d.auth + takeLengthSuffix
}

// TakeRoleMethod names the method a person takes a role for a set time by.
func (d *identitySourceData) TakeRoleMethod() string {
	return d.takeRoleMethod()
}

// TenantUserGroupRole is the struct of the roles a tenant's people hold through a group.
func (d *identitySourceData) TenantUserGroupRole() string {
	return d.groupListName(listThroughGroupsSuffix, true)
}

// TakesRoles reports whether a person of the instance takes a role for a set time.
func (d *identitySourceData) TakesRoles() bool {
	return d.takesRoles()
}

// HasTenant reports whether the application has a tenant record.
func (d *identitySourceData) HasTenant() bool {
	return d.tenant != nil
}

// GroupGives is the SQL test that the group read under group gives its members the role
// of the row read under role, as the permission engine gives it: never the Super
// Administrator role, and for a group kept in one tenant the roles held in that tenant
// alone.
func (d *identitySourceData) GroupGives(group, role string) string {
	return fmt.Sprintf("%[2]s.Role != %[3]s AND (%[1]s.Kind = %[4]s OR (%[2]s.Kind = %[1]s.Kind AND %[2]s.Axis = %[1]s.Axis AND %[2]s.DomainId = %[1]s.DomainId))",
		group, role, d.SuperAdministratorRole(), d.KindGlobal())
}

// holdsWhere is the SQL test that the row read under held, a membership or a group's
// role, is held where the group's role read under role is held, or over it: in the same
// place, or in every tenant where the role is held in one.
func (d *identitySourceData) holdsWhere(held, role string) string {
	return fmt.Sprintf("%[1]s.Axis = %[2]s.Axis AND ((%[1]s.Kind = %[2]s.Kind AND %[1]s.DomainId = %[2]s.DomainId) OR (%[2]s.Kind = %[3]s AND %[1]s.Kind = %[4]s))",
		held, role, d.KindDomain(), d.KindEvery())
}

// Losing is the SQL of how many people lose a group's role when the group stops holding
// it, as one column of a select: the people whose place in the group counts now, where
// the group's role (the row read under role, of the group read under group) counts now
// and is one the group gives, who hold the role there in no other way. Another way is a
// membership of the person's own that counts now, or another role row of a group the
// person is in now that the group gives, held in the same place or, for a role held in
// one tenant, in every tenant: the same group's role held in every tenant among them.
// Nobody loses a role whose period has not begun or has ended, or one the group gives
// nobody.
func (d *identitySourceData) Losing(role, group string) string {
	lines := []string{
		"(SELECT COUNT(*) FROM " + d.policy.GroupMembers + " lm",
		"   WHERE lm.GroupId = " + role + ".GroupId",
		"     AND " + d.Counting("lm"),
		"     AND " + d.Counting(role),
		"     AND " + d.GroupGives(group, role),
		"     AND NOT EXISTS (",
		"       SELECT 1 FROM " + d.policy.UserRoles + "@{FORCE_INDEX=" + d.policy.UserRolesByUserID + "} lo",
		"       WHERE lo.UserId = lm.UserId AND lo.Role = " + role + ".Role",
		"         AND " + d.Counting("lo"),
		"         AND " + d.holdsWhere("lo", role),
		"     )",
		"     AND NOT EXISTS (",
		"       SELECT 1 FROM " + d.policy.GroupMembers + "@{FORCE_INDEX=" + d.policy.GroupMembersByUserID + "} lp",
		"       JOIN " + d.policy.GroupRoles + " lr ON lr.GroupId = lp.GroupId",
		"       JOIN " + d.policy.Groups + " lg ON lg.Id = lp.GroupId",
		"       WHERE lp.UserId = lm.UserId AND lr.Role = " + role + ".Role",
		"         AND " + d.Counting("lp"),
		"         AND " + d.Counting("lr"),
		"         AND " + d.holdsWhere("lr", role),
		"         AND " + d.GroupGives("lg", "lr"),
		"         AND NOT (lr.GroupId = " + role + ".GroupId AND lr.Kind = " + role + ".Kind AND lr.Axis = " + role + ".Axis AND lr.DomainId = " + role + ".DomainId)",
		"     )",
		"  ) AS Losing",
	}

	return "  " + strings.Join(lines, "\n")
}

// GroupPeople is the SQL of how many people a group has, as one column of a select: the
// places in the group read under group that have not ended.
func (d *identitySourceData) GroupPeople(group string) string {
	return "  (SELECT COUNT(*) FROM " + d.policy.GroupMembers + " cm WHERE cm.GroupId = " + group + ".Id AND " + d.Standing("cm") + ") AS People"
}

// GroupRoleLines is the SQL of the roles a group holds, a line each, as one column of a
// select: the roles of the group read under group that have not ended, by name. A role
// held where the group is kept is its name alone, and one held somewhere else says
// where, in the application's word for a tenant: in every tenant, or in one, by the
// name people call the tenant by where the tenant record marks one and by its key
// otherwise.
func (d *identitySourceData) GroupRoleLines(group string) string {
	if d.tenant == nil {
		return "  ARRAY(SELECT cr.Role FROM " + d.policy.GroupRoles + " cr WHERE cr.GroupId = " + group + ".Id AND " + d.Standing("cr") + " ORDER BY cr.Role) AS Roles"
	}
	named, join := "cr.DomainId", ""
	if column := d.tenantNameColumn(); column != "" {
		named = "COALESCE(CAST(ct." + column + " AS STRING), cr.DomainId)"
		join = "\n    LEFT JOIN " + d.tenant.table + " ct ON ct." + d.tenant.key + " = cr.DomainId"
	}
	lines := []string{
		"ARRAY(",
		"    SELECT CASE",
		"      WHEN cr.Kind = " + group + ".Kind AND cr.Axis = " + group + ".Axis AND cr.DomainId = " + group + ".DomainId THEN cr.Role",
		"      WHEN cr.Kind = " + d.KindEvery() + " THEN CONCAT(cr.Role, " + sqlStringLiteral(" in every "+d.tenant.word) + ")",
		"      WHEN cr.Kind = " + d.KindDomain() + " THEN CONCAT(cr.Role, " + sqlStringLiteral(" in "+d.tenant.word+" ") + ", " + named + ")",
		"      ELSE CONCAT(cr.Role, ' for the whole application')",
		"    END",
		"    FROM " + d.policy.GroupRoles + " cr" + join,
		"    WHERE cr.GroupId = " + group + ".Id AND " + d.Standing("cr"),
		"    ORDER BY cr.Role, cr.Kind, cr.DomainId",
		"  ) AS Roles",
	}

	return "  " + strings.Join(lines, "\n")
}

// PersonRoleLine is the SQL of one line of the roles a person holds, the row read under
// held being one of them as the one list of a person's roles has it (HeldRole): the
// role's name, and where it is held when that is not the whole application, in the
// application's word for a tenant: in every tenant, or in one, by the name people call
// the tenant by where the tenant record marks one and by its key otherwise, read from the
// row PersonRoleTenant joins. With no tenant record every role is the whole
// application's, and a line is the role's name.
func (d *identitySourceData) PersonRoleLine(held string) string {
	if d.tenant == nil {
		return held + ".Role"
	}
	named := held + ".DomainId"
	if column := d.tenantNameColumn(); column != "" {
		named = "COALESCE(CAST(pt." + column + " AS STRING), " + held + ".DomainId)"
	}

	return "CASE WHEN " + held + ".Kind = " + d.KindEvery() + " THEN CONCAT(" + held + ".Role, " + sqlStringLiteral(" in every "+d.tenant.word) + ")" +
		" WHEN " + held + ".Kind = " + d.KindDomain() + " THEN CONCAT(" + held + ".Role, " + sqlStringLiteral(" in "+d.tenant.word+" ") + ", " + named + ")" +
		" ELSE " + held + ".Role END"
}

// PersonRoleTenant is the SQL written after the alias of the row read under held that
// joins the row of the tenant record's table PersonRoleLine reads a tenant's name from:
// the tenant the role is held in, left joined, so a role held in no one tenant keeps its
// line. It is nothing where there is no tenant record or the record marks no name.
func (d *identitySourceData) PersonRoleTenant(held string) string {
	if d.tenantNameColumn() == "" {
		return ""
	}

	return "\n    LEFT JOIN " + d.tenant.table + " pt ON pt." + d.tenant.key + " = " + held + ".DomainId"
}

// The keys of the sign-in attempts table's rows the session library keeps for an
// account's own failures under the account's id, each this prefix and the id: the
// guesses at the code of its first password and the wrong sign-in codes entered for it
// (session.AccountAttemptKeys; session/internal/attempts spells them and cannot be
// imported here).
const (
	firstPasswordAttemptPrefix = "FIRST-PASSWORD:"
	codeGuessAttemptPrefix     = "CODE-GUESS:"
)

// CountsFailedSignIns reports whether the instance's schema holds the sign-in attempts
// table, where the session library counts failed sign-ins and the locks they earn.
func (d *identitySourceData) CountsFailedSignIns() bool {
	return d.has(identitySignInAttempts)
}

// LockedUntil is the SQL of until when a person's sign-in is locked by failed tries, as
// one column of a select, user being the alias of the person's row of the users table:
// the latest lock still ahead among the rows of the sign-in attempts table kept under
// the keys session.AccountAttemptKeys answers for an account with no address, its
// normalized username and the two kept under its id. NULL for a person who is not
// locked.
func (d *identitySourceData) LockedUntil(user string) string {
	lines := []string{
		"(SELECT MAX(la.LockedUntil) FROM " + d.tableName(identitySignInAttempts) + " la",
		"   WHERE la.Name IN (" + user + ".NormalizedUsername, CONCAT(" + sqlStringLiteral(firstPasswordAttemptPrefix) + ", " + user + ".Id), CONCAT(" + sqlStringLiteral(codeGuessAttemptPrefix) + ", " + user + ".Id))",
		"     AND la.LockedUntil > CURRENT_TIMESTAMP()) AS LockedUntil",
	}

	return "  " + strings.Join(lines, "\n")
}

// tenantNameColumn is the column of the tenant record's table people call a tenant by
// (@tenantName), "" where the record marks none or its fields could not be read.
func (d *identitySourceData) tenantNameColumn() string {
	if d.tenant == nil || d.tenant.invitation == nil {
		return ""
	}

	return d.tenant.invitation.nameColumn
}

// rolesFrom is the words of the directories that decide the roles of an auth whose
// directory writes them, as its login page's buttons say them ("Google", "Google or
// Microsoft"): the registrations that write the roles of the people they sign in. It is
// "" for an auth whose roles the application gives by hand.
func (i *identityInstance) rolesFrom() string {
	var labels []string
	for _, signIn := range i.signIns {
		if !signIn.syncsRoles || signIn.Label == "" || slices.Contains(labels, signIn.Label) {
			continue
		}
		labels = append(labels, signIn.Label)
	}
	switch len(labels) {
	case 0:
		return ""
	case 1:
		return labels[0]
	default:
		return strings.Join(labels[:len(labels)-1], ", ") + " or " + labels[len(labels)-1]
	}
}
