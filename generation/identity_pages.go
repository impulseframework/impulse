package generation

import (
	"slices"
	"strings"
	"unicode"

	"github.com/impulseframework/impulse/accesstypes"
)

// The generated API description names an auth's generated management, so that the pages
// over it are drawn from the description and no application writes one.
//
// GenerateIdentity writes an auth's management as ordinary resources and methods, named
// for the auth and, on a tenant's side, for the application's tenant record
// (CrewSessionUsers, CrewSectorUsers, DisableCrewSectorUser). A page that draws them has to know which
// resource holds the people and which method disables one, and the names differ in every
// application. The description therefore carries, for each auth, the lists and the
// methods of each side under the key a page knows each by (users, roles, disable). The
// names are taken from where the generator names them (the declarations, the lists, the
// method sources), and a side names only what the outlet serves: a method the auth's
// sign-in methods do not give, or a list the schema has no table for, is absent, and a
// page draws nothing for it. The groups of people are named the same way, where the
// auth keeps them.

// The keys of the lists of a side.
const (
	identityListUsers = "users"
	identityListRoles = "roles"
	// The list the side's methods that give a role pick it from (identity_givable.go,
	// and a tenant's in identity_tenant.go).
	identityListRolesToGive    = "rolesToGive"
	identityListIdentities     = "identities"
	identityListSignInWays     = "signInWays"
	identityListInvitations    = "invitations"
	identityListSessions       = "sessions"
	identityListSignInAttempts = "signInAttempts"
	identityListSignInMethods  = "signInMethods"
	// What every sign-in asks for, and the lengths a browser is remembered for
	// (identity_signinsettings.go).
	identityListSignInSettings  = "signInSettings"
	identityListRememberLengths = "rememberLengths"
	identityListSignUpRoles     = "signUpRoles"
	// The invitations that make a tenant and still stand (identity_invitedtenant.go).
	identityListInvitedTenants = "invitedTenants"
	// The lists over the groups of people (identity_groups.go): the groups, the people
	// of a group, the roles a group holds, and the roles people hold through a group.
	identityListGroups        = "groups"
	identityListGroupUsers    = "groupUsers"
	identityListGroupRoles    = "groupRoles"
	identityListThroughGroups = "rolesThroughGroups"
	// The tenant's directory, on a tenant's side: the record a tenant's one directory is
	// a row of, which the Sign-in settings page sets and removes.
	identityListDirectories = "directories"
)

// identityPart is one list or method of a side: the key a page knows it by and the name
// the generator gave it.
type identityPart struct {
	Key  string
	Name string
}

// tsAPIIdentity is one auth's generated management as the descriptor carries it: the
// auth's name and label, the tenant record's name where the application has one, and
// the sides the outlet serves, the whole application's first.
type tsAPIIdentity struct {
	Auth   string
	Label  string
	Tenant string
	// TenantResource is the tenant record's resource where the outlet serves it: a page
	// that lists a role or an invitation across tenants reads the tenant's name from it,
	// since the row carries the tenant's key, an id nobody reads. Empty where the outlet
	// does not serve the record, and the page shows the key.
	TenantResource string
	// TenantDirectoryOnlyField is the tenant record's directory-only field as the wire
	// names it, where the outlet serves the record (TenantResource) and the record
	// declares the switch: a tenant's Sign-in settings page reads it from the tenant's
	// own row for a reader who holds none of the tenant's sign-in methods. Empty
	// otherwise.
	TenantDirectoryOnlyField string
	// RolesFrom is the directories that decide the auth's roles, in the words of its
	// login page's buttons ("Google", "Google or Microsoft"), where a directory writes
	// them: the auth keeps no groups of its own, and the Groups page says whose groups
	// do that job. Empty where the application gives the roles by hand.
	RolesFrom string
	Sides     []*tsAPIIdentitySide
}

// tsAPIIdentitySide is one side of an auth's management: the whole application's (the
// global scope) or a tenant's (the domain scope), with its lists and its methods.
type tsAPIIdentitySide struct {
	Scope   accesstypes.PermissionScope
	Lists   []identityPart
	Methods []identityPart
}

// pageLists names the structs of the lists of a side by what each holds. The whole
// application's are the declarations over the auth's tables; a tenant's are the lists
// the tenant keeps of them (listName). A person's roles are the one list that says where
// each comes from, on both sides (identity_holdings.go). The whole application's
// identities are the list that says who manages each account where the application has
// a tenant record, and the declaration over the table where it has none. A tenant's side
// also names the directories record, where the application declares one, for the page of
// a tenant's sign-in settings. The sessions are the list that says which of them are live
// (identity_sessions.go).
func (i *identityInstance) pageLists(tenant bool) []identityPart {
	data := &identitySourceData{identityInstance: i}
	if tenant {
		parts := []identityPart{
			{identityListUsers, i.listName(listPeopleSuffix)},
			{identityListRoles, i.listName(listHeldRolesSuffix)},
			{identityListRolesToGive, data.TenantRoleChoice()},
			{identityListIdentities, i.listName(listIdentitiesSuffix)},
			{identityListSignInWays, i.signInWayName(true)},
			{identityListInvitations, i.listName(listInvitationsSuffix)},
			{identityListGroups, i.groupListName(listGroupsSuffix, true)},
			{identityListGroupUsers, i.groupListName(listGroupPeopleSuffix, true)},
			{identityListGroupRoles, i.groupListName(listGroupRolesSuffix, true)},
			{identityListThroughGroups, i.groupListName(listThroughGroupsSuffix, true)},
		}
		if i.tenant != nil && i.tenant.directories != "" {
			parts = append(parts, identityPart{identityListDirectories, i.tenant.directories})
		}

		return parts
	}

	// A person's roles are one list, and the accounts are listed with what each holds,
	// where the schema holds what they read, and the groups and their roles are listed
	// with what each holds (identity_holdings.go).
	users, roles, groups, groupRoles := data.User(), data.Membership(), data.Group(), data.GroupRole()
	if i.listsHeldRoles() {
		users, roles = data.ListedUser(), data.HeldRole()
	}
	identities := data.Identity()
	if i.listsIdentities() {
		identities = data.ListedIdentity()
	}
	if i.keepsGroups() {
		groups, groupRoles = data.ListedGroup(), data.ListedGroupRole()
	}

	parts := []identityPart{
		{identityListUsers, users},
		{identityListRoles, roles},
		{identityListRolesToGive, data.GivableRole()},
		{identityListIdentities, identities},
		{identityListSignInWays, i.signInWayName(false)},
		{identityListInvitations, data.ListedMailLink()},
		{identityListSessions, data.ListedSession()},
		{identityListSignInAttempts, data.SignInAttempt()},
		{identityListSignInMethods, data.SignInMethod()},
		{identityListSignInSettings, data.SignInSetting()},
		{identityListRememberLengths, data.RememberLength()},
		{identityListSignUpRoles, data.SignUpRole()},
		{identityListGroups, groups},
		{identityListGroupUsers, i.groupListName(listGroupPeopleSuffix, false)},
		{identityListGroupRoles, groupRoles},
		{identityListThroughGroups, i.groupListName(listThroughGroupsSuffix, false)},
		{identityListTakenRoles, data.TakenRole()},
	}
	if i.invitesTenants() {
		parts = append(parts, identityPart{identityListInvitedTenants, i.invitedTenantName()})
	}

	return parts
}

// pageMethods names the methods of a side by what each does, as the method sources
// name them (writeIdentitySources, tenantMethodSources): the act, then whose it is, the
// auth's for the whole application and the auth's with the tenant record's name for a
// tenant. The methods that end one session and unlock a sign-in are the whole
// application's alone, and the one that removes a person from a tenant is a tenant's
// alone. A tenant's side of an application that declares a directories
// record also names the tenant record's methods that set how the tenant's people enter
// (tenantdirectoryrole.go, tenantdirectoryonly.go, tenantdirectoryfactor.go).
func (i *identityInstance) pageMethods(tenant bool) []identityPart {
	subject := i.subject(tenant)
	parts := []identityPart{
		{"invite", "Invite" + subject + "User"},
		{"withdrawInvitation", "Withdraw" + subject + "Invitation"},
		{"resendInvitation", "Resend" + subject + "Invitation"},
		{"assignRole", "Assign" + subject + "Role"},
		{"removeRole", "Remove" + subject + "Role"},
		{"disable", "Disable" + subject + "User"},
		{"enable", "Enable" + subject + "User"},
		{"delete", "Delete" + subject + "User"},
		{"rename", "Rename" + subject + "User"},
		{"setName", "Set" + subject + "UserName"},
		{"setEmail", "Set" + subject + "UserEmail"},
		{"sendPasswordResetLink", "Send" + subject + "UserPasswordResetLink"},
		{"requirePasswordChange", "Require" + subject + "PasswordChange"},
		{"endUserSessions", "End" + subject + "UserSessions"},
		{"revokeTrustedDevices", "Revoke" + subject + "UserTrustedDevices"},
		{"unlinkIdentity", i.unlinkIdentityMethod(tenant)},
		{"removePassword", i.removePasswordMethod(tenant)},
		{"createGroup", "Create" + subject + "Group"},
		{"describeGroup", "Describe" + subject + "Group"},
		{"deleteGroup", "Delete" + subject + "Group"},
		{"addGroupMember", "Add" + subject + "GroupMember"},
		{"removeGroupMember", "Remove" + subject + "GroupMember"},
		{"giveGroupRole", "Give" + subject + "GroupRole"},
		{"takeGroupRole", "Take" + subject + "GroupRole"},
	}
	if !tenant {
		data := &identitySourceData{identityInstance: i}
		parts = append(parts,
			identityPart{"endSession", "End" + data.Session()},
			identityPart{"unlockSignIn", "Unlock" + i.auth + "SignIn"},
			identityPart{"openSignUp", "Open" + i.auth + "SignUp"},
			identityPart{"makeInviteOnly", "Make" + i.auth + "InviteOnly"},
			identityPart{"setSelfServiceReset", "Set" + i.auth + "SelfServiceReset"},
			identityPart{"requireSecondFactor", "Require" + i.auth + "SecondFactor"},
			identityPart{"stopRequiringSecondFactor", "StopRequiring" + i.auth + "SecondFactor"},
			identityPart{"setRememberedBrowsers", "Set" + i.auth + "RememberedBrowsers"},
			identityPart{identityMethodTakeRole, i.takeRoleMethod()},
		)
		if i.invitesTenants() {
			parts = append(parts,
				identityPart{"inviteTenant", i.inviteTenantMethod()},
				identityPart{"withdrawInvitedTenant", i.withdrawInvitedTenantMethod()},
			)
		}
	}
	if tenant {
		parts = append(parts, identityPart{"removeFromTenant", "Remove" + subject + "User"})
	}
	if tenant && i.tenant != nil && i.tenant.directories != "" {
		parts = append(parts,
			identityPart{"openToDirectory", openToDirectoryMethodName(i.tenant.name)},
			identityPart{"makeDirectoryInviteOnly", makeInviteOnlyMethodName(i.tenant.name)},
			identityPart{"makeDirectoryOnly", makeDirectoryOnlyMethodName(i.tenant.name)},
			identityPart{"endDirectoryOnly", endDirectoryOnlyMethodName(i.tenant.name)},
			identityPart{"vouchForDirectorySecondFactor", vouchForDirectoryMethodName(i.tenant.name)},
			identityPart{"stopVouchingForDirectorySecondFactor", stopVouchingForDirectoryMethodName(i.tenant.name)},
		)
	}

	return parts
}

// tenantDirectoryOnlyWire is the tenant record's directory-only field as the wire names
// it, where the package's tenant record declares the switch; empty otherwise.
func (t *typescriptGenerator) tenantDirectoryOnlyWire() string {
	for _, res := range t.resources {
		if res.declaresDirectoryOnly() {
			return res.DirectoryOnlyField.WireName()
		}
	}

	return ""
}

// identityOn is the descriptor's identity block for the target's outlet: every auth
// whose generated management the outlet serves, each side holding the lists and the
// methods the outlet's description carries. A side whose list of people the outlet does
// not serve is left out, and an auth with no side left is left out with it.
func (t *typescriptGenerator) identityOn(data *tsAPIData) []*tsAPIIdentity {
	resources := make(map[string]bool, len(data.Resources))
	for _, res := range data.Resources {
		resources[res.Name] = true
	}
	methods := make(map[string]bool, len(data.Methods))
	for _, method := range data.Methods {
		methods[method.Name] = true
	}

	var out []*tsAPIIdentity
	for _, inst := range t.identity {
		if !slices.Contains(inst.outlets, t.targetOutlet()) {
			continue
		}
		identity := &tsAPIIdentity{Auth: inst.auth, Label: authLabel(inst.auth), RolesFrom: strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(inst.rolesFrom())}
		sides := []bool{false}
		if inst.tenant != nil {
			identity.Tenant = inst.tenant.name
			if name := t.pluralize(inst.tenant.name); resources[name] {
				identity.TenantResource = name
				identity.TenantDirectoryOnlyField = t.tenantDirectoryOnlyWire()
			}
			sides = append(sides, true)
		}
		for _, tenant := range sides {
			side := &tsAPIIdentitySide{Scope: accesstypes.GlobalPermissionScope}
			if tenant {
				side.Scope = accesstypes.DomainPermissionScope
			}
			for _, list := range inst.pageLists(tenant) {
				if name := t.pluralize(list.Name); resources[name] {
					side.Lists = append(side.Lists, identityPart{Key: list.Key, Name: name})
				}
			}
			if len(side.Lists) == 0 || side.Lists[0].Key != identityListUsers {
				continue
			}
			for _, method := range inst.pageMethods(tenant) {
				if methods[method.Name] {
					side.Methods = append(side.Methods, method)
				}
			}
			identity.Sides = append(identity.Sides, side)
		}
		if len(identity.Sides) > 0 {
			out = append(out, identity)
		}
	}

	return out
}

// authLabel is the words a page shows for an Auth: its PascalCase name split into
// words, the first kept as written, the rest lowercased unless they are initialisms
// (FleetStaff gives "Fleet staff", HQStaff gives "HQ staff").
func authLabel(auth string) string {
	runes := []rune(auth)
	var words []string
	start := 0
	for i := 1; i < len(runes); i++ {
		afterLower := unicode.IsUpper(runes[i]) && !unicode.IsUpper(runes[i-1])
		beforeLower := unicode.IsUpper(runes[i]) && unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
		if afterLower || beforeLower {
			words = append(words, string(runes[start:i]))
			start = i
		}
	}
	words = append(words, string(runes[start:]))
	for i := 1; i < len(words); i++ {
		if !isInitialism(words[i]) {
			words[i] = strings.ToLower(words[i])
		}
	}

	return strings.Join(words, " ")
}

// isInitialism reports whether the word is two or more capitals and nothing else.
func isInitialism(word string) bool {
	if len(word) < 2 {
		return false
	}
	for _, r := range word {
		if !unicode.IsUpper(r) {
			return false
		}
	}

	return true
}
