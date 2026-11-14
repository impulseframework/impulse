package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// A tenant's methods. An auth's tables are the whole application's: an account is one
// row whatever tenants its person holds a role in. A tenant manages the people who hold
// a role in it through methods of its own, each served under the tenant's address
// behind an Execute grant of the tenant's roles. What makes such a method the tenant's
// is held here, whatever the role file says:
//
//   - the tenant is the one the request was checked in (resource.CallerFrom), never a
//     value of the request, so no request names another tenant;
//   - a role is given and taken in the tenant alone, and a role a person holds in every
//     tenant is not one tenant's to take;
//   - a person is created, and an address invited, into the tenant.
//
// A method that names a person names a row of the tenant's own list, which the generated
// frame locates within the tenant before the method runs: a person who holds no role in
// the tenant is not found there. What a tenant may do to a person who also holds a role
// outside it is the role file's to say, in a condition on the method's grant that reads
// the facts of the row.

// Tenant is the rules as the methods of one tenant apply them.
type Tenant struct {
	manager *Manager
	domain  accesstypes.Domain
}

// Tenant answers the rules for the tenant a method runs for: the one its request was
// checked in. A tenant's method acts for a request, so one run outside any request, in
// a transaction of the application's own, has no tenant and is an error: there the
// application runs the whole application's method and names the tenant itself.
func (m *Manager) Tenant(ctx context.Context) (*Tenant, error) {
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() {
		return nil, errors.New("a tenant's method ran outside a request, so it has no tenant: in a transaction of the application's own, run the whole application's method and name the tenant")
	}
	domain, ok := caller.Scope.Domain()
	if !ok {
		return nil, errors.Newf("a tenant's method ran for a request checked in the scope %s, which names no tenant", caller.Scope)
	}

	return &Tenant{manager: m, domain: domain}, nil
}

// TenantRoles lists the roles that exist in one tenant, by name: the tenant roles of the
// release's role file, and the custom roles held in the tenant and in every tenant
// (access.UserManager.Roles). They are the roles a membership of the tenant may name,
// so the list is what a role is picked from. Which of them a person may give is the
// role file's to say, in a condition on the grant of the method that gives one.
func (m *Manager) TenantRoles(ctx context.Context, domain accesstypes.Domain) ([]access.RoleInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if domain == "" {
		return nil, errors.New("the roles of a tenant were asked for and no tenant was named")
	}
	roles, err := m.access.UserManager().Roles(ctx, accesstypes.DomainPolicyScope(domain))
	if err != nil {
		return nil, errors.Wrap(err, "access.UserManager.Roles()")
	}

	return roles, nil
}

// Domain is the tenant.
func (t *Tenant) Domain() accesstypes.Domain {
	return t.domain
}

// scope is where the tenant's own memberships are held.
func (t *Tenant) scope() accesstypes.PolicyScope {
	return accesstypes.DomainPolicyScope(t.domain)
}

// AssignRole buffers the membership that gives a person a role in the tenant (see
// Manager.AssignRole, whose rule on the role it is held to). The person is named by the
// id of their account.
func (t *Tenant) AssignRole(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	username, found, err := t.manager.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}

	return t.manager.AssignRole(ctx, txn, t.scope(), username, role)
}

// RemoveRole buffers the removal of a role a person holds in the tenant, under the rules
// of Manager.RemoveRole: the last administrator of the tenant keeps the role, and a
// person left with no role anywhere is deleted. The person's id and the role are the key
// of the row in the tenant's list of the roles held there.
//
// The role goes where the tenant holds a membership of its own for it. A role the person
// holds in every tenant alone is a row of the tenant's list too, and is not the tenant's
// to take: it is refused as a Conflict, and goes for the whole application or not at
// all. A role the person holds neither way is no error.
func (t *Tenant) RemoveRole(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if role == "" {
		return httpio.NewBadRequestMessage("the role to take away is empty")
	}
	username, found, err := t.manager.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}

	key := access.NewMembershipKey(t.scope(), accesstypes.User(username), role)
	own, err := t.manager.store.HasMembership(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.HasMembership()")
	}
	if own {
		return t.manager.removeMembership(ctx, txn, t.scope(), username, &key)
	}

	every := access.NewMembershipKey(accesstypes.EveryDomainPolicyScope(), accesstypes.User(username), role)
	everywhere, err := t.manager.store.HasMembership(ctx, txn, &every)
	if err != nil {
		return errors.Wrap(err, "identity.Store.HasMembership()")
	}
	if everywhere {
		return httpio.NewConflictMessagef("%s holds the role %s in every tenant, which is not one tenant's to take away", username, role)
	}

	return nil
}

// CreateUser buffers a new user who holds the roles in the tenant (see
// Manager.CreateUser). The roles are held in the tenant whatever scope the account
// names.
func (t *Tenant) CreateUser(ctx context.Context, txn resource.ReadWriteTransaction, account *NewAccount) (impulse.UUID, error) {
	inTenant := *account
	inTenant.Scope = t.scope()

	return t.manager.CreateUser(ctx, txn, &inTenant)
}

// Invite buffers an invitation into the tenant and has its link mailed once the
// transaction has committed (see Manager.Invite). The roles are held in the tenant
// whatever tenant the invitation names.
func (t *Tenant) Invite(ctx context.Context, txn resource.ReadWriteTransaction, inv *Invitation, sent func(sent bool)) (impulse.UUID, error) {
	domain := string(t.domain)
	inTenant := *inv
	inTenant.Domain = &domain

	return t.manager.Invite(ctx, txn, &inTenant, sent)
}
