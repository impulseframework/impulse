package resource

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/signin"
)

// SignInRoles adapts the access library's user manager to the sign-in policy
// (signin.TenantPolicy) on Spanner. The policy reads the person's memberships inside
// the sign-in's read-write transaction, with the transaction's context, and the
// Spanner client refuses a read made with that context as a nested transaction, so
// the manager handed to the policy directly fails every sign-in the policy decides.
// The adapter reads the memberships outside the transaction, under the sign-in's
// deadline and cancellation and nothing else of its context, as the committed
// memberships, which is what the policy decides over on PostgreSQL too, and it
// reads which tenants the person administers the same way. The manager's other
// operations pass through unchanged.
func SignInRoles(manager signin.UserRoleManager) signin.UserRoleManager {
	return &signInRoles{UserRoleManager: manager}
}

// signInRoles is SignInRoles's adapter: the manager, with UserRoles and
// TenantsAdministered read outside the calling context's transaction.
type signInRoles struct {
	signin.UserRoleManager
}

// UserRoles lists the user's memberships outside the context's transaction.
func (r *signInRoles) UserRoles(ctx context.Context, user accesstypes.User, scopes ...accesstypes.PolicyScope) (accesstypes.RoleCollection, error) {
	outside, done := outsideTransaction(ctx)
	defer done()

	held, err := r.UserRoleManager.UserRoles(outside, user, scopes...)
	if err != nil {
		return nil, errors.Wrap(err, "signin.UserRoleManager.UserRoles()")
	}

	return held, nil
}

// TenantsAdministered lists the tenants the user administers outside the context's
// transaction.
func (r *signInRoles) TenantsAdministered(ctx context.Context, user accesstypes.User) ([]accesstypes.Domain, error) {
	outside, done := outsideTransaction(ctx)
	defer done()

	tenants, err := r.UserRoleManager.TenantsAdministered(outside, user)
	if err != nil {
		return nil, errors.Wrap(err, "signin.UserRoleManager.TenantsAdministered()")
	}

	return tenants, nil
}

// outsideTransaction derives a context that carries ctx's deadline and cancellation
// and none of its values, so a Spanner read under it is no nested transaction. done
// releases what the derivation holds; the caller calls it when the read is over.
func outsideTransaction(ctx context.Context) (outside context.Context, done func()) {
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		outside, cancel = context.WithDeadline(context.Background(), deadline)
	} else {
		outside, cancel = context.WithCancel(context.Background())
	}
	stop := context.AfterFunc(ctx, cancel)

	return outside, func() {
		stop()
		cancel()
	}
}
