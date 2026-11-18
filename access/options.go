package access

import (
	"log"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
)

// Option configures optional Client behavior.
type Option func(*clientOptions)

type clientOptions struct {
	signal            ChangeSignal
	heartbeatInterval time.Duration
	onReloadError     func(error)
	collection        PermissionCollection
	roleFile          RoleFile
	roleFileSet       bool
	superMethods      []accesstypes.Resource
	// tenantAdministrators are the roles whose holders administer a tenant
	// (WithTenantAdministratorRoles).
	tenantAdministrators []accesstypes.Role
}

func defaultClientOptions() *clientOptions {
	return &clientOptions{
		heartbeatInterval: defaultHeartbeatInterval,
		onReloadError:     logReloadError,
	}
}

// logReloadError is the default reload-error hook: every background failure
// and every policy finding reaches the log, so none is silent.
func logReloadError(err error) {
	log.Printf("access: %v", err)
}

// WithChangeSignal wires a push hint that propagates policy changes between
// instances in near-realtime. This is the intended configuration: the
// application adapts its one signal channel through ChangeSignalFunc. Without
// it, changes propagate within one heartbeat interval.
func WithChangeSignal(s ChangeSignal) Option {
	return func(o *clientOptions) {
		o.signal = s
	}
}

// WithHeartbeatInterval overrides how often the policy store is re-read for
// changes (default 1m). It bounds cross-instance staleness when no
// ChangeSignal is configured, and otherwise backstops a broken signal.
// Non-positive values keep the default.
func WithHeartbeatInterval(d time.Duration) Option {
	return func(o *clientOptions) {
		if d > 0 {
			o.heartbeatInterval = d
		}
	}
}

// WithReloadErrorHandler replaces the hook that receives background failures
// (policy reloads and change-signal announce/watch errors) and what a load
// found in the store that this release cannot use as written: a grant it
// skipped (*SkippedGrant), a custom role a default shadows (*ShadowedRole),
// memberships naming a role nothing defines (*OrphanedMembership). While
// reloads fail the Client keeps serving the last good policy snapshot, so this
// hook is where persistent staleness and the findings become visible. The
// default writes each one to the standard log; wire alerting here when the
// log is not enough.
func WithReloadErrorHandler(f func(error)) Option {
	return func(o *clientOptions) {
		if f != nil {
			o.onReloadError = f
		}
	}
}

// WithPermissionCollection tells the Client what the running release declares:
// its permissions, resources and fields. A stored grant naming one the release
// does not declare (after a rollback, or once a release drops a resource) is
// then left out of the snapshot and reported as a SkippedGrant instead of
// being carried as a grant nothing can check. Without it, names are not
// checked; conditions are checked either way. WithDefaultRoles sets the
// collection too, so an application with a role file does not need this.
func WithPermissionCollection(c PermissionCollection) Option {
	return func(o *clientOptions) {
		o.collection = c
	}
}

// WithDefaultRoles gives the Client the release's default roles: the role
// file embedded in the application binary, validated against the collection
// the release declares. The default roles are policy that travels with the
// release — the store holds no row for them: the snapshot compiles them from
// the file, a global role held in the global partition and a domain role held
// in every tenant domain, a membership names one by name alone, and the
// management surface lists them beside the store's custom roles, refuses to
// create, change or delete them, and answers their grants from the file.
//
// New parses and validates the file: a file that does not parse, declares a
// role twice or at both scopes, grants what the collection does not declare,
// or carries a condition the vocabulary refuses fails New, so the release
// does not start. The shapes the file may carry but probably should not (see
// Warning) do not fail it; Client.CheckPolicy returns them for the deploy's
// migrate step to print. The collection must not be nil.
func WithDefaultRoles(collection PermissionCollection, file RoleFile) Option {
	return func(o *clientOptions) {
		o.collection = collection
		o.roleFile = file
		o.roleFileSet = true
	}
}

// WithSuperAdministratorMethods names the methods that give and take the
// auth's roles for the whole application: the role methods of the auth's
// generated management (its identity package's SuperAdministratorMethods).
// The Super Administrator role, which every Client defines
// (accesstypes.SuperAdministrator), holds Execute on each of them under no
// condition, whatever the role file grants anybody: a super administrator
// gives and takes any role of the auth for any person, in the global
// partition and in every tenant, so one who can sign in can take the power
// to fix what is broken. With a collection (WithDefaultRoles,
// WithPermissionCollection) New refuses a method the release does not declare
// at the global scope with Execute. Without the option the role is defined
// and holds nothing of its own.
func WithSuperAdministratorMethods(methods ...accesstypes.Resource) Option {
	return func(o *clientOptions) {
		o.superMethods = methods
	}
}

// WithTenantAdministratorRoles names the roles whose holders administer a
// tenant: the domain roles the auth's package names as an administrator's
// (the every-tenant list of its AdministratorRoles). A person who holds one
// of them in a tenant itself, by a membership of their own in the tenant's
// domain or through a group kept in that tenant, administers the tenant
// (Client.UserAdministersTenant, UserManager.TenantsAdministered), and a
// tenant that admits its people through its directory only still admits its
// own administrators with a password and a second factor: the sign-in rule
// and the session's reach into the tenant both ask this Client. A role held
// in every tenant, or through a group kept for the whole application,
// administers no tenant in this sense, so the application's own people are
// kept out of a directory-only tenant with everyone else. The names are not
// checked against the role file: a name no role carries administers nothing.
// Without the option nobody administers a tenant, and a directory-only
// tenant admits nobody by a password.
func WithTenantAdministratorRoles(roles ...accesstypes.Role) Option {
	return func(o *clientOptions) {
		o.tenantAdministrators = roles
	}
}
