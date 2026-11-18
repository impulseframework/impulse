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
//     or to a group of the tenant's own (GiveGroupRoleFor, TakeGroupRole);
//   - putting a person in, or taking one out of, a group of the tenant's own that
//     holds such a role there (AddGroupMemberFor, RemoveGroupMember);
//   - an invitation that carries such a role there (Invite);
//   - for a person who holds such a role there, of their own or through a group of
//     the tenant's own: setting or removing their password, requiring a change of it,
//     renaming them, changing their address, unlinking a sign-in identity, disabling
//     or enabling the account, and deleting it (requireEnteringFor).
//
// What makes a person a tenant's administrator is the permission engine's to say, and
// the judgment asks the engine's own statement of it (access.AdministersTenant, over
// TenantAdministratorRoles): one of the roles the application names, held in the tenant
// itself by a membership of the person's own or by a group kept in that tenant. A role
// held in every tenant or for the whole application, and one a group kept for the whole
// application holds in the tenant, is no way in and is not judged here, and neither is
// an assignment that has ended, which gives nobody anything. So the judgment covers
// exactly what the way in is decided on. A call outside any request is the
// application's own act and is not judged.

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

// administratorsRoleIn answers the tenant the role makes its holder an administrator
// of, held in the scope by a membership of a person's own (kept is nil) or by a group
// kept where kept says. It is the permission engine's statement (access.AdministersTenant)
// over the roles the engine was built with, and ok is false for every holding the engine
// does not count.
func (m *Manager) administratorsRoleIn(scope accesstypes.PolicyScope, role accesstypes.Role, kept *accesstypes.PolicyScope) (tenant accesstypes.Domain, ok bool) {
	return access.AdministersTenant(m.TenantAdministratorRoles(), scope, role, kept)
}

// entersToChange reports whether the request's caller may change who holds the role in
// the scope, as far as this rule goes: any holding that makes nobody a tenant's
// administrator, and an administrator's role of a tenant the caller enters. kept is nil
// for a role of a person's own and where the group is kept for a group's. What a page
// offers asks it (RolesToGive), and what a method refuses asks the same.
func (m *Manager) entersToChange(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, kept *accesstypes.PolicyScope) (bool, error) {
	if resource.CallerFrom(ctx).IsZero() {
		return true, nil
	}
	tenant, guarded := m.administratorsRoleIn(scope, role, kept)
	if !guarded {
		return true, nil
	}

	return m.callerEnters(ctx, tenant)
}

// requireEnteringToChange refuses a change to who holds the role in the scope unless
// the request's caller enters the tenant (entersToChange), in a sentence that says who
// makes the change.
func (m *Manager) requireEnteringToChange(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, kept *accesstypes.PolicyScope) error {
	enters, err := m.entersToChange(ctx, scope, role, kept)
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
// roles in the tenant itself, by a membership of their own or through a group of the
// tenant's own, in an assignment that has not ended: one that has not begun counts,
// since the person will hold it. The caller enters every tenant the person administers,
// since the account is the way into each: a person who administers two is changed by
// someone both admit, and the refusal says so, where one of either tenant's
// administrators would be refused in turn.
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
		if enters {
			continue
		}
		if len(tenants) > 1 {
			return httpio.NewForbiddenMessagef(
				"This person administers more than one %[1]s, and one of them admits only people who sign in through its own directory, which your sign-in does not enter. "+
					"Their account is changed by someone whose sign-in enters every %[1]s they administer, or by a super administrator",
				m.tenantWord)
		}

		return httpio.NewForbiddenMessagef(
			"This person administers a %[1]s that admits only people who sign in through its own directory, which your sign-in does not enter. "+
				"Their account is changed by one of that %[1]s's administrators, or by a super administrator",
			m.tenantWord)
	}

	return nil
}

// tenantsAdministeredBy lists the tenants the person administers as the permission
// engine counts it (administratorsRoleIn): an administrator's role held in the tenant
// itself, of their own or through a group they are in that is kept in the tenant, less
// what has ended (standing, groupHolding), as the transaction reads it.
func (m *Manager) tenantsAdministeredBy(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]accesstypes.Domain, error) {
	own, places, err := m.standing(ctx, txn, userID)
	if err != nil {
		return nil, err
	}
	var tenants []accesstypes.Domain
	add := func(scope accesstypes.PolicyScope, role accesstypes.Role, kept *accesstypes.PolicyScope) {
		if tenant, guarded := m.administratorsRoleIn(scope, role, kept); guarded && !slices.Contains(tenants, tenant) {
			tenants = append(tenants, tenant)
		}
	}
	for i := range own {
		if scope, held := tenantScope(&own[i]); held {
			add(scope, own[i].Role, nil)
		}
	}
	now := time.Now()
	for _, place := range places {
		kept, err := access.GroupScope(place.Kind, place.Axis, place.Domain)
		if err != nil {
			return nil, errors.Wrap(err, "access.GroupScope()")
		}
		holding, err := m.groupHolding(ctx, txn, place.ID, now)
		if err != nil {
			return nil, err
		}
		for i := range holding.roles {
			scope, err := holding.roles[i].Scope()
			if err != nil {
				return nil, errors.Wrap(err, "access.GroupRoleKey.Scope()")
			}
			add(scope, holding.roles[i].Role, &kept)
		}
	}

	return tenants, nil
}

// membershipEnded reports whether the period of a membership the person holds is over,
// as the transaction reads it: such a membership gives the person nothing.
func (m *Manager) membershipEnded(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, key *access.MembershipKey) (bool, error) {
	periods, err := m.store.UserPeriods(ctx, txn, userID)
	if err != nil {
		return false, errors.Wrap(err, "identity.Store.UserPeriods()")
	}

	return periods.Memberships[*key].Ended(time.Now()), nil
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
