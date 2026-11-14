package identity

import (
	"context"
	"slices"
	"strings"

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
// keeps a person from acting on themselves reads the name from there. A method an
// application runs in a transaction of its own, outside any request, has no caller:
// what it does is the application's act, and these rules let it through.

// callerIs reports whether the request's caller is the person of the username, names
// compared without case as the permission engine's callers are.
func callerIs(ctx context.Context, username string) bool {
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() {
		return false
	}

	return sameUser(string(caller.Permissions.User()), username)
}

// sameUser compares two usernames as one person's.
func sameUser(a, b string) bool {
	return strings.EqualFold(a, b)
}

// refuseOwnAccount refuses an act on the caller's own account: nobody disables or
// deletes themselves, since the one who did could not undo it. act names what was
// asked ("disable", "delete").
func (m *Manager) refuseOwnAccount(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, act string) error {
	username, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if found && callerIs(ctx, username) {
		return httpio.NewForbiddenMessagef("you cannot %s your own account", act)
	}

	return nil
}

// refuseLastAdministrator judges a person's removal of a role from themselves. The role
// goes unless it is an administrator role where it is held and nobody else would hold
// one there after: the application is never left without an administrator, nor a tenant
// without one of its own, and a person who gives a role up otherwise is given it back
// by one who remains. In a tenant the holders counted are the tenant's and the ones who
// hold the role in every tenant, the person's own membership in every tenant among
// them. A role held in every tenant is no administrator role of the application, and
// goes. Without the administrator roles (WithAdministratorRoles) a person removes no
// role from themselves.
func (m *Manager) refuseLastAdministrator(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, key *access.MembershipKey) error {
	if m.administrators == nil {
		return httpio.NewForbiddenMessage("you cannot remove a role from yourself")
	}
	domain, inDomain := scope.Domain()
	var (
		roles  []accesstypes.Role
		scopes []accesstypes.PolicyScope
	)
	switch {
	case scope.IsGlobal():
		roles, scopes = m.administrators.global, []accesstypes.PolicyScope{scope}
	case inDomain:
		roles, scopes = m.administrators.domain, []accesstypes.PolicyScope{scope, accesstypes.EveryDomainPolicyScope()}
	default:
		return nil
	}
	if !slices.Contains(roles, key.Role) {
		return nil
	}
	for _, held := range scopes {
		for _, role := range roles {
			at := access.NewMembershipKey(held, "", role)
			holders, err := m.store.RoleHolders(ctx, txn, &access.RoleKey{Kind: at.Kind, Axis: at.Axis, Domain: at.Domain, Role: role})
			if err != nil {
				return errors.Wrap(err, "identity.Store.RoleHolders()")
			}
			for _, holder := range holders {
				if !sameUser(string(holder), string(key.User)) {
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
	if inDomain {
		return httpio.NewForbiddenMessagef("you are the last administrator of %s: give another person the role before you remove your own", domain)
	}

	return httpio.NewForbiddenMessage("you are the last administrator: give another person the role before you remove your own")
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
		if err := m.store.DeleteIdentity(ctx, txn, link); err != nil {
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
// goes. A username no user holds has no account to delete.
func (m *Manager) deleteWithLastRole(ctx context.Context, txn resource.ReadWriteTransaction, username string, held []access.MembershipKey, removed *access.MembershipKey) error {
	if slices.ContainsFunc(held, func(key access.MembershipKey) bool { return key != *removed }) {
		return nil
	}
	userID, found, err := m.store.UserID(ctx, txn, username)
	if err != nil {
		return errors.Wrap(err, "identity.Store.UserID()")
	}
	if !found {
		return nil
	}
	if err := m.deleteAccount(ctx, txn, userID); err != nil {
		return err
	}
	logger.FromCtx(ctx).Infof("identity: %q holds no role anywhere after the removal of %s, so the account is deleted with its identity links and sessions", username, removed.Role)

	return nil
}

// moveMemberships buffers the move of every membership held under one username to
// another: the permission store names a person by username, so a person renamed keeps
// their roles only if the rows follow the name. A membership the new name holds already
// is left as it is. It answers whether anything moved, for the word to the engine.
func (m *Manager) moveMemberships(ctx context.Context, txn resource.ReadWriteTransaction, from, to string) (moved bool, err error) {
	held, err := m.store.Memberships(ctx, txn, from)
	if err != nil {
		return false, errors.Wrap(err, "identity.Store.Memberships()")
	}
	// Every read comes before the first write: which of them the new name holds.
	insert := make([]access.MembershipKey, 0, len(held))
	for _, key := range held {
		key.User = accesstypes.User(to)
		exists, err := m.store.HasMembership(ctx, txn, &key)
		if err != nil {
			return false, errors.Wrap(err, "identity.Store.HasMembership()")
		}
		if !exists {
			insert = append(insert, key)
		}
	}
	for _, key := range held {
		if err := m.store.DeleteMembership(ctx, txn, &key); err != nil {
			return false, errors.Wrap(err, "identity.Store.DeleteMembership()")
		}
	}
	for _, key := range insert {
		if err := m.store.InsertMembership(ctx, txn, &key); err != nil {
			return false, errors.Wrap(err, "identity.Store.InsertMembership()")
		}
	}

	return len(held) > 0, nil
}
