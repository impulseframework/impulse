package shape

import (
	"fmt"
	"strings"

	"github.com/go-playground/errors/v5"
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

// RefuseDirectoryMethods refuses a starting point without tenant directories (shapes 5
// and 7) for an application that already has a sign-in method a tenant's people could
// come through their organization's directory by: with tenants laid in, the application
// would be the shape with tenant directories. methods are those methods as
// DirectoryMethods lists them.
func (s Shape) RefuseDirectoryMethods(methods []string) error {
	if s.TenantDirectories() || len(methods) == 0 {
		return nil
	}
	with := numbered(s.Number + 1)

	return errors.Newf("%s: in %s a tenant's people come by invitation alone, and the application registers %s, through which a tenant's people could come by their organization's directory, so with tenants it would be %s. Take %s, or run the command without --%s",
		s.Flag(), s.Name, strings.Join(methods, " and "), with, with.Flag(), FlagName)
}

// TenantsSay is what a command given one of shapes 5 to 8 tells its author once the
// tenants are laid in: whether a person belongs to one tenant, how a tenant's people get
// in, and what is still to choose, by the author or while the application runs. auths
// names the auths whose roles the application gives, and directoryMethods the methods a
// tenant's people could come through their organization's directory by (none in a new
// application, whose first auth signs in with a password).
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
		way = fmt.Sprintf("A tenant's people come by invitation alone: no sign-in method goes through an organization's directory, so no tenant takes one, though the tenant record declares a tenant's directory. A directory method added later with the application giving the roles (impulse add method) makes the application %s.", numbered(s.Number+1))
	case len(directoryMethods) > 0:
		way = fmt.Sprintf("A tenant's people can come through its organization's directory, by %s: %s.", strings.Join(directoryMethods, " and "), chosen)
	default:
		way = fmt.Sprintf("A tenant's people come through its organization's directory, and no sign-in method goes through one yet. Add the one the tenants' organizations use, wide enough for all of them, with the application giving the roles: impulse add method %[1]s google --slot any-workspace --authority application for Google Workspace, or impulse add method %[1]s azure --slot any --authority application for Microsoft Entra. Until then impulse audit reads the application as %[2]s. With the method there, %[3]s.", auth, numbered(s.Number-1), chosen)
	}

	return []string{people, way}
}
