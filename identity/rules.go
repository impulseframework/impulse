package identity

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
)

// The rules that judge who is asking. A generated handler stamps the caller of the
// request on the context before the method runs (resource.CallerFrom), and a rule that
// keeps a person from acting on themselves reads who that is from there. A method an
// application runs in a transaction of its own, outside any request, has no caller:
// what it does is the application's act, and these rules let it through.
//
// A caller is named as the permission store names a person, by the id of their account
// (User), and no two accounts share an id, in one auth or across two. An outlet that
// signs its people in through one auth can serve the management of another's
// (generation.Manages): the caller there is another auth's person, whose id is no
// account of this one, so nobody on such an outlet is ever acting on themselves.

// callerIs reports whether the request's caller is the person of the account in this
// auth: the request came through this auth, and the caller's id is the account's. The
// auth a request came through is the one its outlet's generated routes bound
// (resource.AuthFrom). A request bound to another auth is another auth's person. A
// caller with no auth bound, which no generated route produces, is judged by the id
// alone, so the rule errs toward refusing.
func (m *Manager) callerIs(ctx context.Context, userID impulse.UUID) bool {
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() {
		return false
	}
	if auth, bound := resource.AuthFrom(ctx); bound && auth != m.auth {
		return false
	}

	return caller.Permissions.User() == User(userID)
}

// refuseOwnAccount refuses an act on the caller's own account: nobody disables or
// deletes themselves, since the one who did could not undo it. act names what was
// asked ("disable", "delete").
func (m *Manager) refuseOwnAccount(ctx context.Context, userID impulse.UUID, act string) error {
	if m.callerIs(ctx, userID) {
		return httpio.NewForbiddenMessagef("you cannot %s your own account", act)
	}

	return nil
}

// refuseLastAdministrator judges a person's removal of a role from themselves in a
// tenant. The role goes unless it is an administrator role of the tenant and nobody
// else would hold one there after: a tenant is never left without an administrator of
// its own, and a person who gives a role up otherwise is given it back by one who
// remains. The holders counted are the tenant's and the ones who hold the role in every
// tenant, the person's own membership in every tenant among them. Without the
// administrator roles (WithAdministratorRoles) a person removes no role from themselves
// in a tenant or in every tenant.
//
// A role held in the global partition goes: the application's last administrator is its
// last super administrator, whose role is kept by a rule of its own, whoever asks
// (refuseLastSuperAdministrator), and a super administrator gives any other role back.
// A role held in every tenant is no administrator role of one tenant, and goes.
func (m *Manager) refuseLastAdministrator(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, key *access.MembershipKey) error {
	if scope.IsGlobal() {
		return nil
	}
	if m.administrators == nil {
		return httpio.NewForbiddenMessage("you cannot remove a role from yourself")
	}
	if _, inDomain := scope.Domain(); !inDomain {
		return nil
	}
	roles := m.administrators.domain
	if !slices.Contains(roles, key.Role) {
		return nil
	}
	for _, held := range []accesstypes.PolicyScope{scope, accesstypes.EveryDomainPolicyScope()} {
		for _, role := range roles {
			at := access.NewMembershipKey(held, "", role)
			holders, err := m.store.RoleHolders(ctx, txn, &access.RoleKey{Kind: at.Kind, Axis: at.Axis, Domain: at.Domain, Role: role})
			if err != nil {
				return errors.Wrap(err, "identity.Store.RoleHolders()")
			}
			for _, holder := range holders {
				if holder != key.User {
					return nil
				}
				// The person's own membership of the same role in every tenant
				// keeps them the tenant's administrator.
				if role == key.Role && held != scope {
					return nil
				}
			}
		}
	}

	// The tenant's key is an id nobody reads, so the sentence does not print it; it
	// says the application's word for a tenant.
	return httpio.NewForbiddenMessagef("you are the last administrator of this %s: give another person the role before you remove your own", m.tenantWord)
}

// deleteAccount buffers the removal of a user: every session they hold is ended first
// (EndUserSessions), then their identity links go, then the row. Everything is read
// before anything is buffered. The memberships are the caller's to remove.
//
// Shared rule: the session library's DeleteSessionUser, which destroys the sessions
// and deletes the links with the user.
func (m *Manager) deleteAccount(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	links, err := m.store.Identities(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Identities()")
	}
	if err := m.endUserSessions(ctx, txn, userID); err != nil {
		return err
	}
	for _, link := range links {
		if err := m.store.DeleteIdentity(ctx, txn, link.ID); err != nil {
			return errors.Wrap(err, "identity.Store.DeleteIdentity()")
		}
	}
	if err := m.store.DeleteUser(ctx, txn, userID); err != nil {
		return errors.Wrap(err, "identity.Store.DeleteUser()")
	}

	return nil
}

// deleteWithLastRole applies the rule on an account nothing admits, once a membership
// is removed: a person who then holds no role anywhere, in the global scope, in a
// tenant or in every tenant, is deleted with their identity links and sessions. Under
// a directory's open rule the next sign-in creates the row again, and under password
// sign-up the person signs up again; a person who holds a role somewhere stays. held
// is every membership the person held before the removal, and removed the one that
// goes. An id no user holds has no account to delete.
func (m *Manager) deleteWithLastRole(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, held []access.MembershipKey, removed *access.MembershipKey) error {
	if slices.ContainsFunc(held, func(key access.MembershipKey) bool { return key != *removed }) {
		return nil
	}
	username, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return nil
	}
	if err := m.deleteAccount(ctx, txn, userID); err != nil {
		return err
	}
	logger.FromCtx(ctx).Infof("identity: %q (%s) holds no role anywhere after the removal of %s, so the account is deleted with its identity links and sessions", username, userID, removed.Role)

	return nil
}
