package accesstypes

import "slices"

// Role represents a role in the authorization system
type Role string

// SuperAdministrator is the one role the framework defines in every auth: the way back
// in when everything else is broken. An application's role file cannot define a role of
// this name, and a custom role cannot take it. It is held in the global partition
// alone. What makes it special is fixed, whatever the role file says: no tenant's
// sign-in settings shut its holder out, its holder signs in with a second factor, its
// holder gives and takes any role of the auth, only a holder makes or removes another,
// and the last holder keeps the role and their account. Everything else its holder may
// do comes from the other roles they hold.
const SuperAdministrator Role = "SuperAdministrator"

// HoldsSuperAdministrator reports whether the memberships are a super administrator's:
// the Super Administrator role, held in the global partition, where alone it is held.
// It is the one test of who a tenant's sign-in settings do not bind: the sign-in rule
// asks it of the person signing in, and the session's reach into a tenant asks it of
// the session's actor, the real person behind an impersonated session. A membership of
// the name anywhere else makes nobody a super administrator.
func (c RoleCollection) HoldsSuperAdministrator() bool {
	return slices.Contains(c[GlobalPolicyScope()], SuperAdministrator)
}
