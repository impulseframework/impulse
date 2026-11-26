package auditlog

import (
	"context"

	"github.com/impulseframework/impulse/accesstypes"
)

// DirectorySync is what a sign-in tells the permission store about the role changes it is about to ask for.
type DirectorySync struct {
	Directory  string                                 // the directory, by the name the sign-in page gives it
	UserID     string                                 // the account
	Username   string                                 // its username at the sign-in
	TenantName func(tenant accesstypes.Domain) string // what the application calls a tenant; nil for none
}

type directorySyncKey struct{}

// WithDirectorySync answers the context with what a sign-in tells the permission store
// about the role changes it asks for in that context: the store records each role it
// gives or takes as the directory's act on the account.
func WithDirectorySync(ctx context.Context, sync DirectorySync) context.Context {
	return context.WithValue(ctx, directorySyncKey{}, sync)
}

// DirectorySyncFrom answers what a sign-in told the permission store about the role
// changes asked for in the context, and whether it told anything. A context that carries
// none records nothing.
func DirectorySyncFrom(ctx context.Context) (sync DirectorySync, ok bool) {
	sync, ok = ctx.Value(directorySyncKey{}).(DirectorySync)

	return sync, ok
}
