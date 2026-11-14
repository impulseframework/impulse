package app

import (
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/membersidentity"
)

// The management of the two auths' people is generated (pkg/identity, the generator
// program's GenerateIdentity): each auth's lists and methods are served by generated
// handlers that reach the auth's identity client through the accessors below. The
// console serves both auths' (the generator program's Manages), so headquarters
// administers both populations from one place; the portal serves the members' alone.
// What a role may do is the role file's to say, in the same grammar as every other
// resource and method: SectorMarshal gives and takes the roles of its sector's people,
// every role but its own (new.role != 'SectorMarshal'), and Headquarters holds both
// populations across the application. The crew's roles are all the application's,
// whichever way a person signs in; the members' are their directory's at every sign-in
// (members.go's RoleSync, the registration declared SyncsRoles), so the members'
// management has no method that gives or takes a role.
//
// Demonstrates: auth.generated-identity.

// CrewIdentity is the crew auth's identity client: what the generated methods that manage
// the crew's people run over (pkg/identity/crewidentity), on their routes and in a
// transaction the application opens itself.
func (a *App) CrewIdentity() *crewidentity.Client {
	return a.crewIdentity
}

// MembersIdentity is the members auth's identity client, the members' counterpart of
// CrewIdentity.
func (a *App) MembersIdentity() *membersidentity.Client {
	return a.membersIdentity
}
