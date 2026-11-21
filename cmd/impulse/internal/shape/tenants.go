package shape

import (
	"fmt"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
)

// OneTenantFlag is the flag that says a person belongs to one tenant, which a starting
// point of shapes 5 to 8 fills in.
const OneTenantFlag = "one-tenant-per-person"

// counterpart is the shape that differs from this one in whether a person belongs to
// one tenant: Tenant-owned users, invite only and Global users, invite only are each
// other's, and so are the two with tenant directories.
func (s Shape) counterpart() Shape {
	switch s.Number {
	case tenantOwnedInviteOnly:
		return numbered(globalInviteOnly)
	case tenantOwnedByDirectory:
		return numbered(globalEachTenantChooses)
	case globalInviteOnly:
		return numbered(tenantOwnedInviteOnly)
	default:
		return numbered(tenantOwnedByDirectory)
	}
}

// OneTenantChoice settles whether a person belongs to one tenant under the shape: the
// shape's own answer, which fills the choice in where the command line left it open.
// given says the command line made the choice itself, with value; one that says the
// opposite of the shape is refused, naming both.
func (s Shape) OneTenantChoice(given, value bool) (bool, error) {
	want := s.OneTenantPerPerson()
	if !given || value == want {
		return want, nil
	}
	flag, means, shapeMeans := "--"+OneTenantFlag, "holds a person to one tenant", "a person can be in several tenants"
	if want {
		flag, means, shapeMeans = "--"+OneTenantFlag+"=false", "lets a person be in several", "a person belongs to one tenant"
	}

	return false, errors.Newf("%s and %s contradict each other: in %s %s, and %s %s. Take %s, or leave --%s out",
		s.Flag(), flag, s.Name, shapeMeans, flag, means, s.counterpart().Flag(), OneTenantFlag)
}

// DirectoriesFlag is the flag that says whether a tenant can have a directory of its
// own, which a starting point of shapes 5 to 8 fills in.
const DirectoriesFlag = "tenant-directories"

// DirectoriesChoice settles whether a tenant can have a directory of its own under the
// shape: the shape's own answer, which fills the choice in where the command line left
// it open. given says the command line made the choice itself, with value; one that
// says the opposite of the shape is refused, naming both. The choice is the
// application's and no sign-in method's: an application whose tenants have no
// directories is the invite-only shape with or without a method through an
// organization's directory whose roles the application gives.
func (s Shape) DirectoriesChoice(given, value bool) (bool, error) {
	want := s.TenantDirectories()
	if !given || value == want {
		return want, nil
	}
	if want {
		return false, errors.Newf("%s and --%s=false contradict each other: in %s a tenant's people come through the tenant's own directory, and --%s=false leaves the tenants without directories. Take %s, or leave --%s out",
			s.Flag(), DirectoriesFlag, s.Name, DirectoriesFlag, numbered(s.Number-1).Flag(), DirectoriesFlag)
	}

	return false, errors.Newf("%s and --%s contradict each other: in %s a tenant's people come by invitation alone and no tenant has a directory, and --%s lets a tenant have one. Take %s, or leave --%s out",
		s.Flag(), DirectoriesFlag, s.Name, DirectoriesFlag, numbered(s.Number+1).Flag(), DirectoriesFlag)
}

// TenantsSay is what a command given one of shapes 5 to 8 tells its author once the
// tenants are laid in: whether a person belongs to one tenant, how a tenant's people get
// in, and what is still to choose, by the author or while the application runs. auths
// names the auths whose roles the application gives, and directoryMethods the methods
// that go through an organization's directory with the application giving the roles
// (none in a new application, whose first auth signs in with a password): the methods a
// tenant's people come through its directory by where the tenants have directories,
// and a way for an invited person to sign in where they have none.
func (s Shape) TenantsSay(auths, directoryMethods []string) []string {
	auth := "<auth>"
	if len(auths) > 0 {
		auth = auths[0]
	}
	people := "A person can be in several tenants: each tenant invites them and controls the role it gave, and no identity client is built with identity.WithOneTenantPerPerson()."
	if s.OneTenantPerPerson() {
		people = "A person belongs to one tenant: the identity client of every auth whose roles the application gives is built with identity.WithOneTenantPerPerson(), so a role in a second tenant is refused however it would be given."
	}
	const chosen = "a tenant's administrator, signed in through their organization's directory, makes it the tenant's on the tenant's Sign-in page, and chooses there whether its people get an account without an invitation; both are chosen while the application runs"
	var way string
	switch {
	case !s.TenantDirectories():
		// The methods are named as a sentence's subject, and again as what the reading
		// is the same with and without.
		methods, with := "A Google, Microsoft or other provider's method added with impulse add method, the application giving its roles, is", "with it as without it"
		if len(directoryMethods) > 0 {
			named := strings.Join(directoryMethods, " and ")
			methods = strings.ToUpper(named[:1]) + named[1:] + " is"
			if len(directoryMethods) > 1 {
				methods, with = strings.ToUpper(named[:1])+named[1:]+" are each", "with them as without them"
			}
		}
		way = fmt.Sprintf("A tenant's people come by invitation alone: the application has no tenant directories (the tenant record declares none and carries no directory-only switch), so no tenant takes a directory, none is opened to one, and none is made directory only. %s a way for an invited person to sign in and nothing more, and impulse audit reads the application as %s %s. Going to %s is the author's act, laying the tenant directories in (the directories record and its table, the tenant record's directory-only column, each auth's directory admission), which no impulse add method does by itself.", methods, s, with, numbered(s.Number+1))
	case len(directoryMethods) > 0:
		way = fmt.Sprintf("A tenant's people can come through its organization's directory, by %s: %s.", strings.Join(directoryMethods, " and "), chosen)
	default:
		way = fmt.Sprintf("A tenant's people come through its organization's directory, and no sign-in method goes through one yet. Add the one the tenants' organizations use, wide enough for all of them, with the application giving the roles: impulse add method %[1]s google --slot any-workspace --authority application for Google Workspace, or impulse add method %[1]s azure --slot any --authority application for Microsoft Entra. Until then impulse audit reads the application as %[2]s. With the method there, %[3]s.", auth, numbered(s.Number-1), chosen)
	}

	return []string{people, way}
}

// ByDirectoryMethods are the sign-in methods Tenant-owned users, by directory, starts
// with: a Google method open to any Google Workspace and a Microsoft method open to any
// Microsoft Entra tenant, neither with personal accounts, each registered under its kind
// with the application giving the roles. It is the one way the shape starts: a tenant is
// then the directory of whichever organization its founder signs in from, and another
// width is the author's change to what was written.
func ByDirectoryMethods() []authpkg.Method {
	return []authpkg.Method{
		{Kind: app.MethodGoogle, Name: app.MethodGoogle, Authority: app.AuthorityApplication, Slot: authpkg.SlotAnyWorkspace},
		{Kind: app.MethodAzure, Name: app.MethodAzure, Authority: app.AuthorityApplication, Slot: authpkg.SlotAny},
	}
}

// StartsWithoutPassword is what impulse new says of the first auth of an application
// that starts from a shape with no password sign-in (NoPassword): why it has none, what
// stands in its place, and how a development run signs a person in. persona is the
// login of the persona a development run of Tenant-owned users, by directory, signs its
// administrator in as.
func (s Shape) StartsWithoutPassword(auth, persona string) string {
	if s.Number == directoryRoles {
		return fmt.Sprintf("The %s auth has no password sign-in and invites no first administrator: the directory gives every role, the Super Administrator's included, so nothing in the application gives one. A development run signs a person in through the simulated directory: the person APP_USERNAME names in the environment template, with the roles APP_ROLES names there.", auth)
	}

	return fmt.Sprintf("The %s auth has no password sign-in: every person of a tenant signs in through the tenant's own directory, so a password could enter no tenant. It registers a Google method open to any Google Workspace and a Microsoft method open to any Microsoft Entra tenant, neither with personal accounts, which is how the shape is typically used; another width works, and is your change to what was written. Register the application at both providers before it deploys, since each method needs a client id and secret of its own. The application's own people, its first administrator among them, come by invitation and accept it through either method. The persona method beside them is the sign-in of the test environments, and a development run signs its administrator in through it, as the persona %s.", auth, persona)
}

// WritesMethods is what impulse add tenancy says of an auth it writes the methods of
// Tenant-owned users, by directory, on (ByDirectoryMethods): what they are, and what the
// author owes each provider before the application deploys.
func (s Shape) WritesMethods(auth string) string {
	return fmt.Sprintf("The %s auth registered neither a Google nor a Microsoft method, so the command writes both: a Google method open to any Google Workspace and a Microsoft method open to any Microsoft Entra tenant, neither with personal accounts, the application giving the roles, which is how %s is typically used. Another width works, and is your change to what was written. Register the application at both providers before it deploys, since each method needs a client id and secret of its own.", auth, s)
}

// PasswordKeepsOutside is what impulse add tenancy says of an auth it starts as
// Tenant-owned users, by directory, that registers the password method: the method
// stays, since no command removes one, and what its staying means and the hand step
// that takes it out. dir and file are the auth's package directory and the file of its
// Auth.
func (s Shape) PasswordKeepsOutside(auth, dir, file string) string {
	return fmt.Sprintf("The %s auth keeps its password method, since no command removes a sign-in method, and the method keeps the application outside %s: in that shape every person of a tenant signs in through the tenant's own directory, so a password could enter no tenant, and impulse audit says so until it is gone. Taking it out is a hand step (%s).", auth, s, passwordSteps(dir, file))
}
