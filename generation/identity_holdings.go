package generation

// The roles held for a set time, in an auth's generated management. Beside the
// declarations over the permission store's tables, the generated management declares the
// roles held for a set time with a reason (<Auth>TakenRole), a view like a tenant's
// lists (identity_lists.go) which the Break glass page lists, the method a person takes
// one by (Take<Auth>Role), and the two lists its form picks from: the roles
// (<Auth>TakeableRole) and the lengths (<Auth>TakeLength).

// The suffixes of the structs declared here, each after the auth's name.
const (
	takenRoleSuffix    = "TakenRole"
	takeableRoleSuffix = "TakeableRole"
	takeLengthSuffix   = "TakeLength"
)

// The keys of the lists and the method of the Break glass page, as the description
// carries them.
const (
	identityListTakenRoles = "takenRoles"
	identityMethodTakeRole = "takeRole"
)

// takesRoles reports whether a person of the instance takes a role for a set time: the
// application gives the auth's roles by hand, and the schema holds the accounts and the
// memberships. An auth whose directory writes its roles gives none by hand, so nobody
// takes one there.
func (i *identityInstance) takesRoles() bool {
	return !i.rolesSynced && i.has(identityUsers) && i.has(identityMemberships)
}

// holdingSources are the source files of the roles held for a set time and of the taking
// of one: the view in the declarations package, and the method and the two lists its
// form picks from in the methods package.
func (r *resourceGenerator) holdingSources(inst *identityInstance) []identitySourceFile {
	view := func(suffix, template string, needs ...identityTable) identitySourceFile {
		return identitySourceFile{dir: inst.resources, name: fileStem(r.pluralize(inst.auth + suffix)), template: template, needs: needs}
	}
	var files []identitySourceFile
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
	names := make([]string, 0, 3)
	for _, suffix := range []string{takenRoleSuffix, takeableRoleSuffix, takeLengthSuffix} {
		names = append(names, i.auth+suffix)
	}

	return names
}

// takeRoleMethod names the method a person takes a role for a set time by.
func (i *identityInstance) takeRoleMethod() string {
	return "Take" + i.auth + "Role"
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

// TakesRoles reports whether a person of the instance takes a role for a set time.
func (d *identitySourceData) TakesRoles() bool {
	return d.takesRoles()
}

// HasTenant reports whether the application has a tenant record.
func (d *identitySourceData) HasTenant() bool {
	return d.tenant != nil
}
