package resources

import "github.com/impulseframework/impulse/accesstypes"

// ShipsLogEntries is the ship's log page's resource: a hand-written, SECTOR-scoped list
// handler over the DataChangeEvents table the change-tracked resources (Missions,
// Refits, Ships) write into. There is no generated handler (the table is library
// infrastructure, not a schema resource), so the permission registration the generator
// cannot derive is declared manually, with the scope argument: @manualAddResource(List,
// domain) puts List into the generated permission collection in the domain scope
// (the role file validates against it when the auth opens, the TypeScript permission
// constants include it) and the handler in the app package checks it. It carries no
// outlet annotation: the route is mounted on the default outlet only, so the portal's
// TypeScript target does not carry it.
//
// Demonstrates: @manualAddResource, @manualAddResource.scope, change-tracking.
//
// @manualAddResource(List, domain)
const ShipsLogEntries accesstypes.Resource = "ShipsLogEntries"

// ClientStatements is the portal's per-company statement over the change log: bookings,
// fee changes, completions, settlements. It is the first manual registration that names
// a non-default outlet: @outlet(portal) keeps it in the portal's constants and drops it
// from the console's, and the hand-written route under the portal prefix scopes itself
// to the caller's company through ClientContacts, since a manual resource has no
// bindings for a condition to reference. Its grant is Granted, never Conditional.
//
// Demonstrates: @manualAddResource.outlet, @manualAddResource.scope.
//
// @manualAddResource(List, domain)
// @outlet(portal)
const ClientStatements accesstypes.Resource = "ClientStatements"

// ViewAsUser is the grant to view the console as another person: Execute on it lets its
// holder start a session of the kind "view", which reads what that person reads and
// writes nothing (the kind's widest mask is List and Read). It is the grant to look, and
// no more: acting as a person takes ActAsUser, a grant of its own. It is the first
// manual EXECUTE registration; the constant reaches the generated TypeScript Methods
// constants, so the crew roster checks Execute on it by the generated name, never a
// hand-typed string.
//
// Demonstrates: @manualAddResource.execute, impersonation.view-as.
//
// @manualAddResource(Execute)
const ViewAsUser accesstypes.Resource = "ViewAsUser"

// ActAsUser is the grant to act as another person: Execute on it lets its holder start a
// session of the kind "act", which does what that person may do, except the credential
// actions (the kind's widest mask refuses the class credential, so the session cannot
// change how the person signs in). To let someone act as a person is to give them what
// that person holds, less what the mask refuses, so the role file gives this grant on
// purpose and to few.
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
