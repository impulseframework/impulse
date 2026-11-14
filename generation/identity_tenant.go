package generation

import "slices"

// A tenant's methods of an auth's generated management. Beside the lists one tenant
// keeps of an auth's people (identity_lists.go), the generated management writes the
// methods the tenant changes them through. Each is a method of the tenant's scope,
// served under the tenant's address behind an Execute grant of the tenant's roles, and
// each that names something names a row of one of the lists, so the generated frame
// locates the row within the tenant and judges the grant's condition against it.
//
// A tenant's method is named as the whole application's is, with the tenant record's
// name after the auth's (DisableCrewUser, DisableCrewSectorUser). The rules are the
// identity package's, the same ones the whole application's method runs under; what
// makes the method the tenant's is in identity.Tenant.

// subject names whose methods a file holds: the auth's for the whole application, and
// the auth's with the tenant record's name after it for a tenant. It is the one place a
// tenant's methods are named.
func (i *identityInstance) subject(tenant bool) string {
	if tenant {
		return i.auth + i.tenant.name
	}

	return i.auth
}

// tenantRoleChoiceSuffix is the suffix of the computed resource that lists the roles
// that exist in one tenant, after the auth's name and the tenant record's
// (CrewSectorRole, the roles one Sector gives).
const tenantRoleChoiceSuffix = "Role"

// unlinkIdentityMethod names the method that unlinks a sign-in identity: the whole
// application's is named for the declaration over the identities table
// (UnlinkCrewSessionIdentity), and a tenant's is that name with the tenant record's
// after the auth's, as every method of a tenant is (UnlinkCrewSectorSessionIdentity).
func (i *identityInstance) unlinkIdentityMethod(tenant bool) string {
	return "Unlink" + i.subject(tenant) + "SessionIdentity"
}

// UnlinkIdentity is the method the file's template writes (unlinkIdentityMethod).
func (d *identitySourceData) UnlinkIdentity() string {
	return d.unlinkIdentityMethod(d.ForTenant)
}

// picksRoles reports whether the instance declares the roles that exist in one tenant:
// where there is a tenant record and the roles are given by hand. The roles of an auth
// whose directory writes them are picked by nobody.
func (i *identityInstance) picksRoles() bool {
	return i.tenant != nil && !i.rolesSynced && i.has(identityMemberships)
}

// tenantComputedSources are the source files of the computed resources one tenant
// reads. A computed resource's file is named for the resource, in the plural.
func (r *resourceGenerator) tenantComputedSources(inst *identityInstance) []identitySourceFile {
	if !inst.picksRoles() {
		return nil
	}

	return []identitySourceFile{{
		dir:      inst.methods,
		name:     fileStem(r.pluralize(inst.auth + inst.tenant.name + tenantRoleChoiceSuffix)),
		template: identityTenantRoleChoicesTemplate,
		needs:    []identityTable{identityMemberships},
		tenant:   true,
	}}
}

// TenantRoleChoice is the struct of the roles that exist in a tenant.
func (d *identitySourceData) TenantRoleChoice() string {
	return d.auth + d.tenant.name + tenantRoleChoiceSuffix
}

// hasList reports whether the instance carries the tenant's list of the suffix.
func (i *identityInstance) hasList(suffix string) bool {
	return slices.ContainsFunc(i.lists(), func(list identityList) bool { return list.suffix == suffix })
}

// tenantMethodSources are the source files of the methods one tenant runs: none without
// a tenant record, and each only where the list whose rows it names is declared and the
// whole application's method of the same act is written.
func (i *identityInstance) tenantMethodSources() []identitySourceFile {
	if i.tenant == nil {
		return nil
	}
	subject := i.subject(true)
	method := func(name, template string, needs ...identityTable) identitySourceFile {
		return identitySourceFile{dir: i.methods, name: fileStem(name), template: template, needs: needs, tenant: true}
	}

	var files []identitySourceFile
	if i.hasList(listPeopleSuffix) {
		files = append(files,
			method("Disable"+subject+"User", identityDisableUserTemplate, identityUsers),
			method("Enable"+subject+"User", identityEnableUserTemplate, identityUsers),
			method("Delete"+subject+"User", identityDeleteUserTemplate, identityUsers),
			method("End"+subject+"UserSessions", identityEndUserSessionsTemplate, identitySessions),
			method("Revoke"+subject+"UserTrustedDevices", identityRevokeTrustedDevicesTemplate, identityTrustedDevices),
		)
		if i.password {
			files = append(files,
				method("Require"+subject+"PasswordChange", identityRequirePasswordChangeTemplate, identityUsers),
				method("Set"+subject+"TemporaryPassword", identitySetTemporaryPasswordTemplate, identityUsers),
			)
		}
		if !i.usernameIsEmail {
			files = append(files,
				method("Rename"+subject+"User", identityRenameUserTemplate, identityUsers),
				method("Set"+subject+"UserEmail", identitySetUserEmailTemplate, identityUsers),
			)
		}
		if i.createsUsers() {
			files = append(files, method("Create"+subject+"User", identityTenantCreateUserTemplate, identityUsers, identityMemberships))
		}
		if !i.rolesSynced {
			files = append(files, method("Assign"+subject+"Role", identityTenantAssignRoleTemplate, identityMemberships))
		}
	}
	if i.hasList(listIdentitiesSuffix) {
		files = append(files, method(i.unlinkIdentityMethod(true), identityUnlinkIdentityTemplate, identityIdentities))
	}
	if !i.rolesSynced && i.hasList(listRolesSuffix) {
		files = append(files, method("Remove"+subject+"Role", identityTenantRemoveRoleTemplate, identityMemberships))
	}
	if !i.rolesSynced && i.hasList(listInvitationsSuffix) {
		files = append(files,
			method("Invite"+subject+"User", identityTenantInviteUserTemplate, identityMailLinks, identityMemberships),
			method("Withdraw"+subject+"Invitation", identityWithdrawInvitationTemplate, identityMailLinks, identityMemberships),
		)
	}

	return files
}

// Subject names whose method the file's template writes (identityInstance.subject).
func (d *identitySourceData) Subject() string {
	return d.subject(d.ForTenant)
}

// SubjectUser is the struct whose row a method names a person by: the users table's for
// the whole application, and the tenant's list of people for a tenant.
func (d *identitySourceData) SubjectUser() string {
	if d.ForTenant {
		return d.TenantUser()
	}

	return d.User()
}

// SubjectIdentity is the struct whose row a method names an identity by: the identities
// table's for the whole application, and the tenant's list of its people's identities
// for a tenant.
func (d *identitySourceData) SubjectIdentity() string {
	if d.ForTenant {
		return d.TenantIdentity()
	}

	return d.Identity()
}

// SubjectMailLink is the struct whose row a method names an invitation by: the mail
// links table's for the whole application, and the tenant's list of invitations for a
// tenant.
func (d *identitySourceData) SubjectMailLink() string {
	if d.ForTenant {
		return d.TenantMailLink()
	}

	return d.MailLink()
}
