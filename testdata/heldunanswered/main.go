// Command heldunanswered is a program that does not compile, and is kept for that: it
// is an application's own permissions, written by hand, which list the tenants the
// session stands in and do not say which tenants the session's person holds. A rule
// that counts a person's tenants (the generated create-your-tenant route's, that a
// person creates one) must not read the session's list for the person's: a permission
// mask takes a tenant off the first and not off the second. So what the person holds is
// a method of resource.UserPermissions, and permissions that do not answer it are not
// the request's permissions. The resource package's tests type-check the program and
// pin the refusal (TestHeldDomains_unansweredDoesNotCompile).
//
// The program is kept outside the library's packages on purpose: there an application
// is.
package main

import (
	"context"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// listed is the application's permissions: every method the interface asked for before
// it asked what the person holds.
type listed struct {
	user    accesstypes.User
	tenants []accesstypes.Domain
}

// Check denies everything: the program is about the tenants.
func (listed) Check(_ context.Context, _ accesstypes.Environment, _ accesstypes.Scope, _ accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	decisions := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		decisions[res] = accesstypes.Denied()
	}

	return decisions, nil
}

// PermissionDigest is empty.
func (listed) PermissionDigest(context.Context, accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	return accesstypes.PermissionDigest{}, nil
}

// HasGrants reports a foothold in each tenant listed.
func (l listed) HasGrants(_ context.Context, scope accesstypes.Scope) (bool, error) {
	domain, ok := scope.Domain()
	if !ok {
		return false, nil
	}
	for _, tenant := range l.tenants {
		if tenant == domain {
			return true, nil
		}
	}

	return false, nil
}

// Domains lists the tenants the session stands in.
func (l listed) Domains(context.Context) ([]accesstypes.Domain, error) {
	return l.tenants, nil
}

// Reaches admits the session to every tenant.
func (listed) Reaches(context.Context, accesstypes.Domain) (bool, error) {
	return true, nil
}

// User is the session's person.
func (l listed) User() accesstypes.User {
	return l.user
}

func main() {
	// Refused: the permissions do not say what the person holds.
	var perms resource.UserPermissions = listed{user: "dana", tenants: []accesstypes.Domain{"riverside"}}
	_ = perms
}
