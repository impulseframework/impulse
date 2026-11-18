package shape

import (
	"fmt"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
)

// Width is how wide a sign-in method is open: whom its provider could sign in, which
// the method's registration fixes in code. It is whom opening the method to sign-up
// admits.
type Width struct {
	// Words says whom the method admits, as the end of "admits ...".
	Words string
	// Limited reports a method that admits the people of the organizations it names and
	// nobody else: opened to sign-up it is Directory members, where a method anyone can
	// use is Open sign-up.
	Limited bool
	// Read is false for a method whose width the code does not say in a form impulse
	// reads.
	Read bool
}

// How many organizations a directory method's slot names: one, the ones it lists, or
// any.
const (
	organizationsOne    = "one"
	organizationsListed = "listed"
	organizationsAny    = "any"
)

// The words for the methods that have no organization slot.
const (
	passwordAdmits = "anyone with an email address"
	providerAdmits = "the accounts of one organization's identity provider"
	personaAdmits  = "the personas of the test environments' persona provider"
)

// organizationWidth is the width of a Microsoft or Google method from what its slot
// names: how many organizations (one, listed, any, or none), and whether it admits the
// accounts outside every organization, Microsoft personal accounts or any Google
// account.
func organizationWidth(kind, organizations string, accounts bool) Width {
	organization, outside := "Microsoft Entra tenant", "Microsoft personal accounts"
	if kind == app.MethodGoogle {
		organization, outside = "Google Workspace", "any Google account"
	}
	words := ""
	switch organizations {
	case organizationsOne:
		words = "the accounts of one " + organization
	case organizationsListed:
		words = "the accounts of the " + organization + "s it lists"
	case organizationsAny:
		words = "the accounts of any " + organization
	}
	switch {
	case words == "" && !accounts:
		return Width{Words: "", Limited: false, Read: false}
	case words == "", kind == app.MethodGoogle && organizations == organizationsAny && accounts:
		// Any Google account takes in every Workspace's.
		words = outside
	case accounts:
		words += ", and " + outside
	}

	return Width{Words: words, Limited: !accounts && organizations != organizationsAny, Read: true}
}

// SlotWidth is the width of a method impulse is about to write, from its kind and its
// organization slot.
func SlotWidth(m *authpkg.Method) Width {
	slot := m.Slot
	if slot == "" {
		slot = authpkg.SlotOne
	}
	switch m.Kind {
	case app.MethodPassword:
		return Width{Words: passwordAdmits, Limited: false, Read: true}
	case app.MethodOIDC:
		return Width{Words: providerAdmits, Limited: true, Read: true}
	case app.MethodPersona:
		return Width{Words: personaAdmits, Limited: true, Read: true}
	case app.MethodAzure, app.MethodGoogle:
	default:
		return Width{Words: "", Limited: false, Read: false}
	}
	switch slot {
	case authpkg.SlotOne:
		return organizationWidth(m.Kind, organizationsOne, false)
	case authpkg.SlotListed:
		return organizationWidth(m.Kind, organizationsListed, false)
	case authpkg.SlotAnyWorkspace:
		return organizationWidth(m.Kind, organizationsAny, false)
	case authpkg.SlotAny:
		// A Google method's any is any Google account; a Microsoft method's is any tenant.
		return organizationWidth(m.Kind, organizationsAny, m.Kind == app.MethodGoogle)
	case authpkg.SlotPersonal:
		return organizationWidth(m.Kind, "", true)
	case authpkg.SlotOnePersonal, authpkg.SlotMixed:
		return organizationWidth(m.Kind, organizationsOne, true)
	case authpkg.SlotAnyPersonal:
		return organizationWidth(m.Kind, organizationsAny, true)
	default:
		return Width{Words: "", Limited: false, Read: false}
	}
}

// ReadWidth is the width of a method an application registers, as the scan read it.
func ReadWidth(m *app.Method) Width {
	switch m.Kind {
	case app.MethodPassword:
		return Width{Words: passwordAdmits, Limited: false, Read: true}
	case app.MethodOIDC:
		return Width{Words: providerAdmits, Limited: true, Read: true}
	case app.MethodPersona:
		return Width{Words: personaAdmits, Limited: true, Read: true}
	case app.MethodAzure:
		if m.Tenants == app.TenantsPersonal {
			return organizationWidth(m.Kind, "", true)
		}

		return organizationWidth(m.Kind, m.Tenants, m.PersonalAccounts)
	case app.MethodGoogle:
		if m.Workspaces == app.WorkspacesAccounts {
			return organizationWidth(m.Kind, "", true)
		}

		return organizationWidth(m.Kind, m.Workspaces, m.GoogleAccounts)
	default:
		return Width{Words: "", Limited: false, Read: false}
	}
}

// What each of shapes 1 to 4 is, said of one sign-in method, as a refusal says it.
const (
	inviteOnlyIs       = "in Invite only the application gives the roles, to the people an administrator invites"
	directoryMembersIs = "Directory members is a method limited to the organizations it names, opened to sign-up so that everyone in the organization's directory gets an account, with the application giving the roles"
	directoryRolesIs   = "in Directory roles the directory of one organization gives the roles"
	openSignUpIs       = "Open sign-up is a method anyone can use, opened to sign-up, with the application giving the roles"
)

// is says what the shape is, of one sign-in method.
func (s Shape) is() string {
	switch s.Number {
	case inviteOnly:
		return inviteOnlyIs
	case directoryMembers:
		return directoryMembersIs
	case directoryRoles:
		return directoryRolesIs
	default:
		return openSignUpIs
	}
}

// contradicts is the refusal of a choice given beside --shape that the shape rules
// out: it names both, says what each means, and says what to do.
func (s Shape) contradicts(what, means, instead string) error {
	return errors.Newf("%s and %s contradict each other: %s, and %s. %s", s.Flag(), what, s.is(), means, instead)
}

// The flags a method's choices are given with, as a refusal names them.
const (
	authorityFlag = "--authority"
	slotFlag      = "--slot"
)

// Method fills in, for one sign-in method, the choices the shape fixes and the command
// line left open: who gives the roles (Authority) and how wide the method is open
// (Slot). A choice the command line made is kept where the shape allows it. It refuses
// a method the shape cannot be, and a choice given beside --shape that contradicts it,
// naming both. A kind impulse does not compose is left for the command to refuse. Only
// shapes 1 to 4 have a method's choices to fill in.
func (s Shape) Method(m *authpkg.Method) error {
	switch m.Kind {
	case app.MethodPassword:
		return s.passwordMethod()
	case app.MethodPersona:
		return s.personaMethod(m)
	case app.MethodAzure, app.MethodGoogle, app.MethodOIDC:
	default:
		return nil
	}
	if err := s.fillAuthority(m); err != nil {
		return err
	}
	if m.Kind == app.MethodOIDC {
		if s.Number == openSignUp {
			return s.contradicts("an identity provider's method (oidc)", "an identity provider's registration admits "+providerAdmits+" and nobody else",
				"Take "+numbered(directoryMembers).Flag()+", or open the password method, a Microsoft method or a Google method")
		}

		return nil
	}

	return s.fillSlot(m)
}

// passwordMethod judges the password method under the shape: it has no directory, and
// it admits anyone, so it is Invite only or Open sign-up and nothing is filled in.
func (s Shape) passwordMethod() error {
	const what = "the password method"
	switch s.Number {
	case directoryMembers:
		return s.contradicts(what, "the password method admits "+passwordAdmits,
			"Take a directory method (azure, google or oidc), or "+numbered(inviteOnly).Flag()+" or "+numbered(openSignUp).Flag())
	case directoryRoles:
		return s.contradicts(what, "the password method has no directory", "Take a directory method (azure, google or oidc)")
	default:
		return nil
	}
}

// personaMethod judges the persona method under the shape: the sign-in of the test
// environments is left invite only, and its provider says nothing about roles, so the
// application gives them.
func (s Shape) personaMethod(m *authpkg.Method) error {
	if s.Number == inviteOnly {
		if m.Authority == "" {
			m.Authority = app.AuthorityApplication
		}

		return nil
	}

	return s.contradicts("the persona method", "the persona method is the sign-in of the test environments, left invite only, whose accounts are the cast's and whose provider says nothing about roles",
		"Take "+numbered(inviteOnly).Flag()+", or leave --"+FlagName+" out")
}

// fillAuthority settles who gives a directory method's roles: the directory in
// Directory roles, the application in the other three.
func (s Shape) fillAuthority(m *authpkg.Method) error {
	want := app.AuthorityApplication
	if s.Number == directoryRoles {
		want = app.AuthorityDirectory
	}
	switch m.Authority {
	case "", want:
		m.Authority = want

		return nil
	case app.AuthorityDirectory:
		return s.contradicts(authorityFlag+" "+m.Authority, authorityFlag+" directory has the directory give the roles to everyone it signs in",
			"Take "+numbered(directoryRoles).Flag()+", or leave "+authorityFlag+" out")
	case app.AuthorityApplication:
		return s.contradicts(authorityFlag+" "+m.Authority, authorityFlag+" application has the application give them",
			"Take "+numbered(inviteOnly).Flag()+", "+numbered(directoryMembers).Flag()+" or "+numbered(openSignUp).Flag()+", or leave "+authorityFlag+" out")
	default:
		// Not an authority: the command says so.
		return nil
	}
}

// fillSlot settles how wide a Microsoft or Google method is open: any width in Invite
// only, the organizations it names in Directory members, one organization in Directory
// roles, and anyone in Open sign-up, which is the slot filled in when none was given.
func (s Shape) fillSlot(m *authpkg.Method) error {
	width := SlotWidth(m)
	if !width.Read {
		// Not a slot of the kind: the command says so.
		return nil
	}
	given := slotFlag + " " + m.Slot
	admits := given + " admits " + width.Words
	switch s.Number {
	case directoryMembers:
		if !width.Limited {
			return s.contradicts(given, admits, "Take "+numbered(openSignUp).Flag()+", or "+slotFlag+" "+authpkg.SlotOne+" or "+authpkg.SlotListed)
		}
	case directoryRoles:
		if m.Slot != "" && m.Slot != authpkg.SlotOne {
			return s.contradicts(given, admits, "Leave "+slotFlag+" out: role synchronization follows one organization's roles")
		}
	case openSignUp:
		if m.Slot == "" {
			m.Slot = widestSlot(m.Kind)

			return nil
		}
		if width.Limited {
			return s.contradicts(given, admits+" and nobody else", "Take "+numbered(directoryMembers).Flag()+", or leave "+slotFlag+" out, which takes "+slotFlag+" "+widestSlot(m.Kind))
		}
	}

	return nil
}

// widestSlot is the slot of a kind that admits anyone: any tenant and Microsoft
// personal accounts, or any Google account.
func widestSlot(kind string) string {
	if kind == app.MethodGoogle {
		return authpkg.SlotAny
	}

	return authpkg.SlotAnyPersonal
}

// Opening is what an administrator's choice to open a sign-in method to sign-up does in
// the application the method is written into.
type Opening int

// The openings.
const (
	// OpensWithRole is an application without tenants: each person who signs up is given
	// the role the administrator names.
	OpensWithRole Opening = iota
	// OpensToCreateTenant is the auth of an application with tenants whose people create
	// a tenant of their own: a person who signs up holds no tenant and creates theirs.
	OpensToCreateTenant
	// NeverOpened is another auth of an application with tenants: a person who signed up
	// to it could be given no tenant, so no method of it is opened.
	NeverOpened
)

// What a starting point says of the choice made while the application runs.
const (
	chosenRunning = "Open is chosen on the application's Sign-in page while the application runs, by whoever holds the grant on it"
	staysClosed   = "It stays invite only in this application, which has tenants: this auth's people create no tenant of their own, so no method of it is opened to sign-up, and a person gets in by an invitation or through the directory of a tenant that names a default role"
)

// Says is what a command given the shape tells its author about the method it wrote:
// the choices the shape filled in, and what is chosen while the application runs.
func (s Shape) Says(m *authpkg.Method, opening Opening) string {
	name := m.Name
	if m.Kind == app.MethodPassword {
		name = app.PasswordRegistration
	}
	width := SlotWidth(m)
	admits := fmt.Sprintf("The %s method admits %s", name, width.Words)
	if width.Limited && m.Kind != app.MethodPersona {
		admits += " and nobody else"
	}
	if s.Number == directoryRoles {
		return admits + ", and its directory gives the roles: everyone the directory signs in becomes a user with the roles it names, so the method has no invite-only or open choice and stands alone in its auth."
	}
	admits += ", and the application gives the roles. "
	if s.Number == inviteOnly {
		return admits + "It starts invite only, as every method does, and Invite only is leaving it so: a person with no account needs an invitation."
	}
	if opening == NeverOpened {
		return admits + staysClosed + "."
	}
	var opened string
	switch {
	case opening == OpensToCreateTenant:
		opened = "a person who signs up through it holds no tenant and creates their own"
	case s.Number == directoryMembers:
		opened = "everyone in the organization's directory gets an account at their first sign-in, with the role the administrator names"
	default:
		opened = "anyone can sign up, and gets the role the administrator names"
	}

	return admits + "It starts invite only: " + chosenRunning + ", and from then " + opened + "."
}

// StandsAlone refuses Directory roles given more than one method: a method whose
// directory gives the roles is the authority for everyone its auth holds, so it is the
// one method of its auth. methods are the methods as the command line named them. The
// other shapes take any number of methods.
func (s Shape) StandsAlone(methods []string) error {
	if s.Number != directoryRoles || len(methods) < 2 {
		return nil
	}

	return s.contradicts(fmt.Sprintf("%d methods (%s)", len(methods), strings.Join(methods, ", ")),
		"a method whose directory gives the roles stands alone in its auth, since the directory is then the authority for everyone the auth holds",
		"Name the one directory method, and give another way in an auth of its own")
}

// NeedsDirectoryMethod is the refusal of a starting point that is a directory method's
// (Directory members, Directory roles) given to a command that writes none.
func (s Shape) NeedsDirectoryMethod(given string) error {
	return errors.Newf("%s needs a directory method, and %s: %s. Name one with --method azure:<registration>, google:<registration> or oidc:<registration>",
		s.Flag(), given, s.is())
}
