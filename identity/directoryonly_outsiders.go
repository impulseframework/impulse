package identity

import (
	"context"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// The application's own people do not get into a directory-only tenant by their own act.
//
// A tenant that admits its people through its directory only keeps everyone else out,
// the application's own team with them and a super administrator excepted, and its own
// administrators keep a way in with a password and a second factor
// (signin.TenantDeclaration.AdmitsAdministrator). That way in is for a person given
// the administrator's role in the tenant itself. A person who gives roles for the whole
// application would otherwise make it true of themselves: give themselves the role, or
// set the password or the address of a person who holds it, and walk in.
//
// So one judgment stands over every act that could (callerEnters): the request's caller
// enters the tenant with the session they hold. It is the reach the tenant's own routes
// are guarded by (resource.UserPermissions.Reaches): a tenant that is not directory
// only admits every session, and one that is admits a session established through its
// directory, a super administrator's, and its own administrator's come with a password
// and a second factor. A session that views the application as someone else is
// established by its impersonation and enters no directory-only tenant unless the real
// person behind it is a super administrator, so viewing as a tenant's administrator
// gains nothing. A caller the tenant does not admit is refused:
//
//   - giving or taking one of the tenant's administrator roles in the tenant, to a
//     person (AssignRoleFor, RemoveRole, CreateUser, which starts a person with roles)
//     or to a group (GiveGroupRoleFor, TakeGroupRole);
//   - putting a person in, or taking one out of, a group that holds such a role there
//     (AddGroupMemberFor, RemoveGroupMember);
//   - an invitation that carries such a role there (Invite);
//   - for a person who holds such a role there, of their own or through a group:
//     setting or removing their password, requiring a change of it, renaming them,
//     changing their address, unlinking a sign-in identity, disabling or enabling the
//     account, and deleting it (requireEnteringFor).
//
// A role held in every tenant, or for the whole application, is no way in and is not
// judged here. The roles are the ones the permission engine answers who administers a
// tenant by (TenantAdministratorRoles), so the judgment covers exactly what the way in
// is decided on. A call outside any request is the application's own act and is not
// judged.

// callerEnters is the judgment: whether the request's caller enters the tenant with the
// session they hold. A call outside any request is the application's own and enters.
func (m *Manager) callerEnters(ctx context.Context, tenant accesstypes.Domain) (bool, error) {
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() {
		return true, nil
	}
	enters, err := caller.Permissions.Reaches(ctx, tenant)
	if err != nil {
		return false, errors.Wrap(err, "resource.UserPermissions.Reaches()")
	}

	return enters, nil
}

// administratorsRoleIn answers the tenant whose administrator's role the role is in the
// scope: one tenant's own scope, and a role the permission engine names as a tenant's
// administrator's. ok is false for every other role and every other scope. The engine is
// asked only of a role held in one tenant.
func (m *Manager) administratorsRoleIn(scope accesstypes.PolicyScope, role accesstypes.Role) (tenant accesstypes.Domain, ok bool) {
	tenant, inDomain := scope.Domain()
	if !inDomain || tenant == "" || scope.Axis() != "" {
		return "", false
	}
	if !slices.Contains(m.TenantAdministratorRoles(), role) {
		return "", false
	}

	return tenant, true
}

// entersToChange reports whether the request's caller may change who holds the role in
// the scope, as far as this rule goes: any role that is not a tenant's administrator's,
// and an administrator's role of a tenant the caller enters. What a page offers asks it
// (RolesToGive), and what a method refuses asks the same.
func (m *Manager) entersToChange(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	if resource.CallerFrom(ctx).IsZero() {
		return true, nil
	}
	tenant, guarded := m.administratorsRoleIn(scope, role)
	if !guarded {
		return true, nil
	}

	return m.callerEnters(ctx, tenant)
}

// requireEnteringToChange refuses a change to who holds the role in the scope unless
// the request's caller enters the tenant (entersToChange), in a sentence that says who
// makes the change.
func (m *Manager) requireEnteringToChange(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	enters, err := m.entersToChange(ctx, scope, role)
	if err != nil {
		return err
	}
	if enters {
		return nil
	}

	return httpio.NewForbiddenMessagef(
		"%[1]s is a role that administers a %[2]s, and this %[2]s admits only people who sign in through its own directory, which your sign-in does not enter. "+
			"Who holds the role there is changed by one of its administrators, or by a super administrator",
		string(role), m.tenantWord)
}

// requireEnteringFor refuses a change to how a person signs in, to where their codes
// are mailed or to whether their account stands, when the person administers a tenant
// the request's caller does not enter (callerEnters): a password of the caller's
// choosing or an address of theirs would hand the caller the person's way into the
// tenant. The person administers a tenant where they hold one of its administrator
// roles in the tenant itself, by a membership of their own or through a group, in an
// assignment that has not ended: one that has not begun counts, since the person will
// hold it.
func (m *Manager) requireEnteringFor(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	if resource.CallerFrom(ctx).IsZero() {
		return nil
	}
	tenants, err := m.tenantsAdministeredBy(ctx, txn, userID)
	if err != nil {
		return err
	}
	for _, tenant := range tenants {
		enters, err := m.callerEnters(ctx, tenant)
		if err != nil {
			return err
		}
		if !enters {
			return httpio.NewForbiddenMessagef(
				"This person administers a %[1]s that admits only people who sign in through its own directory, which your sign-in does not enter. "+
					"Their account is changed by one of that %[1]s's administrators, or by a super administrator",
				m.tenantWord)
		}
	}

	return nil
}

// tenantsAdministeredBy lists the tenants whose administrator's role the person holds
// in the tenant itself, of their own or through a group they are in, less what has
// ended (standing, groupHolding), as the transaction reads it.
func (m *Manager) tenantsAdministeredBy(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]accesstypes.Domain, error) {
	own, places, err := m.standing(ctx, txn, userID)
	if err != nil {
		return nil, err
	}
	var tenants []accesstypes.Domain
	add := func(scope accesstypes.PolicyScope, role accesstypes.Role) {
		if tenant, guarded := m.administratorsRoleIn(scope, role); guarded && !slices.Contains(tenants, tenant) {
			tenants = append(tenants, tenant)
		}
	}
	for i := range own {
		if scope, held := tenantScope(&own[i]); held {
			add(scope, own[i].Role)
		}
	}
	now := time.Now()
	for _, place := range places {
		holding, err := m.groupHolding(ctx, txn, place.ID, now)
		if err != nil {
			return nil, err
		}
		for i := range holding.roles {
			scope, err := holding.roles[i].Scope()
			if err != nil {
				return nil, errors.Wrap(err, "access.GroupRoleKey.Scope()")
			}
			add(scope, holding.roles[i].Role)
		}
	}

	return tenants, nil
}

// tenantScope answers the scope of a membership held in one tenant; held is false for
// one held for the whole application or in every tenant.
func tenantScope(key *access.MembershipKey) (scope accesstypes.PolicyScope, held bool) {
	if key.Domain == "" || !inOneTenant(key) {
		return accesstypes.PolicyScope{}, false
	}
	scope = accesstypes.DomainPolicyScope(accesstypes.Domain(key.Domain))
	if in := access.NewMembershipKey(scope, key.User, key.Role); in.Axis != key.Axis {
		return accesstypes.PolicyScope{}, false
	}

	return scope, true
}
