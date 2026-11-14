package resource

import (
	"context"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// The creation of a tenant: the person who creates one is its first administrator. The
// generated write paths (the tenant record's create, and the create-your-tenant route of
// @tenant(creation: self-service) or both) give the person the administrator roles in
// the transaction that writes the tenant's row, through the auth's generated identity
// client, so a tenant never stands without its administrator. What is left for after
// the commit is here: the answer waits until the person's next request would stand in
// the tenant.

// administratorVisibleTimeout bounds the wait for the permission engine to answer a
// membership that was just committed: the engine is told at the commit and reloads
// before its next answer, the roster has reread the tenants, and the route answers only
// once the person's next request would see the tenant.
const administratorVisibleTimeout = 10 * time.Second

// administratorVisiblePoll is how often the engine is asked during the wait.
const administratorVisiblePoll = 25 * time.Millisecond

// AwaitFirstAdministrator returns once the permission engine answers the session's user
// a foothold in the tenant whose row and administrator membership the request just
// committed, bounded by administratorVisibleTimeout, so the answer the route gives is
// one the person's next request stands on. The generated write paths call it after the
// commit and once this instance's roster lists the tenant, since the question it asks is
// the one the next request asks. An error says the engine did not answer in time: the
// tenant and the membership are committed all the same, so the caller logs it and
// answers the request.
func AwaitFirstAdministrator(ctx context.Context, perms UserPermissions, domain accesstypes.Domain) error {
	deadline := time.Now().Add(administratorVisibleTimeout)
	for {
		has, err := perms.HasGrants(ctx, accesstypes.DomainScope(domain))
		if err != nil {
			return errors.Wrap(err, "resource.UserPermissions.HasGrants()")
		}
		if has {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.Newf("the first administrator of %s: the membership is committed but the permission engine does not answer it after %s", domain, administratorVisibleTimeout)
		}
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx.Err(), "waiting for the first administrator's membership")
		case <-time.After(administratorVisiblePoll):
		}
	}
}
