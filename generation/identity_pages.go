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
// page draws nothing for it.

// The keys of the lists of a side.
const (
	identityListUsers          = "users"
	identityListRoles          = "roles"
	identityListIdentities     = "identities"
	identityListInvitations    = "invitations"
	identityListSessions       = "sessions"
	identityListSignInAttempts = "signInAttempts"
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
	Sides  []*tsAPIIdentitySide
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
// the tenant keeps of them (listName).
func (i *identityInstance) pageLists(tenant bool) []identityPart {
	if tenant {
		return []identityPart{
			{identityListUsers, i.listName(listPeopleSuffix)},
			{identityListRoles, i.listName(listRolesSuffix)},
			{identityListIdentities, i.listName(listIdentitiesSuffix)},
			{identityListInvitations, i.listName(listInvitationsSuffix)},
		}
	}
	data := &identitySourceData{identityInstance: i}

	return []identityPart{
		{identityListUsers, data.User()},
		{identityListRoles, data.Membership()},
		{identityListIdentities, data.Identity()},
		{identityListInvitations, data.MailLink()},
		{identityListSessions, data.Session()},
		{identityListSignInAttempts, data.SignInAttempt()},
	}
}

// pageMethods names the methods of a side by what each does, as the method sources
// name them (writeIdentitySources, tenantMethodSources): the act, then whose it is, the
// auth's for the whole application and the auth's with the tenant record's name for a
// tenant. The methods that end one session and unlock a sign-in are the whole
// application's alone.
func (i *identityInstance) pageMethods(tenant bool) []identityPart {
	subject := i.subject(tenant)
	parts := []identityPart{
		{"create", "Create" + subject + "User"},
		{"invite", "Invite" + subject + "User"},
		{"withdrawInvitation", "Withdraw" + subject + "Invitation"},
		{"assignRole", "Assign" + subject + "Role"},
		{"removeRole", "Remove" + subject + "Role"},
		{"disable", "Disable" + subject + "User"},
		{"enable", "Enable" + subject + "User"},
		{"delete", "Delete" + subject + "User"},
		{"rename", "Rename" + subject + "User"},
		{"setEmail", "Set" + subject + "UserEmail"},
		{"setTemporaryPassword", "Set" + subject + "TemporaryPassword"},
		{"requirePasswordChange", "Require" + subject + "PasswordChange"},
		{"endUserSessions", "End" + subject + "UserSessions"},
		{"revokeTrustedDevices", "Revoke" + subject + "UserTrustedDevices"},
		{"unlinkIdentity", i.unlinkIdentityMethod(tenant)},
	}
	if !tenant {
		data := &identitySourceData{identityInstance: i}
		parts = append(parts,
			identityPart{"endSession", "End" + data.Session()},
			identityPart{"unlockSignIn", "Unlock" + i.auth + "SignIn"},
		)
	}

	return parts
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
		identity := &tsAPIIdentity{Auth: inst.auth, Label: authLabel(inst.auth)}
		sides := []bool{false}
		if inst.tenant != nil {
			identity.Tenant = inst.tenant.name
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
