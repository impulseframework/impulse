package resources

import "github.com/impulseframework/impulse/accesstypes"

// ViewAsUser is the grant to view the console as another person: Execute on it lets its
// holder start a session of the kind "view", which reads what that person reads and
// writes nothing (the kind's widest mask is List and Read). It is the grant to look, and
// no more: acting as a person takes ActAsUser, a grant of its own. It is the first
// manual EXECUTE registration; the constant reaches the generated TypeScript Methods
// constants, so the crew roster checks Execute on it by the generated name, never a
// hand-typed string.
//
// Demonstrates: @manualAddResource, @manualAddResource.execute, impersonation.view-as.
//
// @manualAddResource(Execute)
const ViewAsUser accesstypes.Resource = "ViewAsUser"

// ActAsUser is the grant to act as another person: Execute on it lets its holder start a
// session of the kind "act", which does what that person may do, except change how
// anyone signs in or who may do what: the kind's widest mask refuses the classes
// credential and access, and the writes of the records that decide what a person may do
// (app/impersonate.go). To let someone act as a person is to give them what that person
// holds, less what the mask refuses, so the role file gives this grant on purpose and
// to few.
//
// Demonstrates: @manualAddResource.execute, impersonation.act-as-user, impersonation.grant-mask.
//
// @manualAddResource(Execute)
const ActAsUser accesstypes.Resource = "ActAsUser"

// AssumeRole is the grant to act as a role: Execute on it lets its holder start a
// session of the kind "role", which holds what the role holds, with subject still bound
// to the actor.
//
// Demonstrates: @manualAddResource.execute, impersonation.act-as-role.
//
// @manualAddResource(Execute)
const AssumeRole accesstypes.Resource = "AssumeRole"
