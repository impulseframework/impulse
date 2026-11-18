package generation

// How a person signs in, as an auth's generated management reads it. A person's
// password is removed only while an identity linked to them can sign them in, and which
// identities can is the session library's judgment: the login page's registrations and
// the connections each takes, which no table holds. The generated management serves
// that judgment as a list, the password a person holds and the identities that count,
// so a page offers the removal where it would go through and nowhere else.
//
// The list is a computed resource declared beside the methods, one for the whole
// application and one a tenant reads, named as the methods are: for the auth, and for
// the auth with the tenant record's name after it (CrewSignInWay, CrewSectorSignInWay).
// It is written where the method that removes a password is: for an auth that declares
// the password method, over a schema that holds the identities.

// signInWaySuffix is the suffix of the computed resource that lists how a person signs
// in, after the subject's name (identityInstance.subject).
const signInWaySuffix = "SignInWay"

// identitySignIn is one sign-in registration of an auth as its login page offers it:
// the name it is registered under, and the words its button carries.
type identitySignIn struct {
	Name  string
	Label string
}

// signInWayName names the struct of the list of how a person signs in.
func (i *identityInstance) signInWayName(tenant bool) string {
	return i.subject(tenant) + signInWaySuffix
}

// listsSignInWays reports whether the instance declares the list of how a person signs
// in for a side: where the method that removes a password is written
// (writeIdentitySources, tenantMethodSources). A tenant's needs the tenant's own list of
// people, which holds the read to the tenant, and is declared with the tenant's list of
// their identities, as the removal is.
func (i *identityInstance) listsSignInWays(tenant bool) bool {
	if !i.password || !i.has(identityUsers) || !i.has(identityIdentities) {
		return false
	}
	if tenant {
		return i.tenant != nil && i.hasList(listPeopleSuffix) && i.hasList(listIdentitiesSuffix)
	}

	return true
}

// signInWaySources are the source files of the lists of how a person signs in. A
// computed resource's file is named for the resource, in the plural. The whole
// application's file holds what the two share, and a tenant's is never written without
// it (listsSignInWays).
func (r *resourceGenerator) signInWaySources(inst *identityInstance) []identitySourceFile {
	var files []identitySourceFile
	for _, tenant := range []bool{false, true} {
		if !inst.listsSignInWays(tenant) {
			continue
		}
		files = append(files, identitySourceFile{
			dir:      inst.methods,
			name:     fileStem(r.pluralize(inst.signInWayName(tenant))),
			template: identitySignInWaysTemplate,
			needs:    []identityTable{identityUsers, identityIdentities},
			tenant:   tenant,
		})
	}

	return files
}

// SignInWay is the struct the file's template writes (signInWayName).
func (d *identitySourceData) SignInWay() string {
	return d.signInWayName(d.ForTenant)
}

// RemovePassword is the method that removes a password on the file's side
// (removePasswordMethod).
func (d *identitySourceData) RemovePassword() string {
	return d.removePasswordMethod(d.ForTenant)
}

// SignIns are the auth's sign-in registrations, the password method among them, in
// declaration order: the list of how a person signs in names each way by the words the
// login page's button carries for the registration it comes through.
func (d *identitySourceData) SignIns() []identitySignIn {
	return d.signIns
}
