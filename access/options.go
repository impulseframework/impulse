package access

import (
	"log"
	"time"
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
