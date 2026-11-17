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

// removal is what a person loses by an act of their own, as the rule on a tenant's last
// administrator reads it: a role they hold by a membership of their own, or one they
// hold through a group, which goes when they leave the group or when the group loses
// the role.
type removal struct {
	// user is the person who loses the role, the request's caller, and role the role.
	user accesstypes.User
	role accesstypes.Role
	// group is the group the role is held through, empty for a membership of the
	// person's own; wholeGroup says the group itself loses the role, so everyone in it
	// does, and not the one person their place.
	group      accesstypes.Group
	wholeGroup bool
	// unnamed is the refusal where the application named no administrator roles, and
	// before what the person does last ("remove your own"), for the refusal of the last
	// administrator.
	unnamed string
	before  string
}

// takes reports whether the removal takes the holding away: the membership of the
// person's own held in the scope, the person's holdings through the group they leave,
// or everyone's holding of the role through the group that loses it there.
func (r *removal) takes(h *administratorHolding, scope accesstypes.PolicyScope) bool {
	switch {
	case r.group == "":
		return h.group == "" && h.user == r.user && h.role == r.role && h.scope == scope
	case r.wholeGroup:
		return h.group == r.group && h.role == r.role && h.scope == scope
	default:
		return h.group == r.group && h.user == r.user
	}
}

// administratorHolding is one way a person administers a tenant now: an administrator
// role they hold in the tenant or in every tenant, by a membership of their own (group
// is empty) or through a group.
type administratorHolding struct {
	user  accesstypes.User
	scope accesstypes.PolicyScope
	role  accesstypes.Role
	group accesstypes.Group
}

// refuseLastAdministrator judges a person's removal of a role from themselves in a
// tenant: a membership of their own they remove, a group that gives them the role and
// that they leave, and a role they take from a group they are in. The role goes unless
// it is an administrator role of the tenant and nobody else would hold one there after:
// a tenant is never left without an administrator of its own, and a person who gives a
// role up otherwise is given it back by one who remains. The holders counted are the
// tenant's and the ones who hold the role in every tenant, by a membership of their own
// or through a group, each inside its period (administratorHoldings): a holder through
// a group is a holder. The person keeps the tenant's administration too where they hold
// the same role another way, in every tenant or through another group. Without the
// administrator roles (WithAdministratorRoles) a person removes no role from themselves
// in a tenant or in every tenant.
//
// A role held in the global partition goes: the application's last administrator is its
// last super administrator, whose role is kept by a rule of its own, whoever asks
// (refuseLastSuperAdministrator), and a super administrator gives any other role back.
// A role held in every tenant is no administrator role of one tenant, and goes.
func (m *Manager) refuseLastAdministrator(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, gone *removal) error {
	if scope.IsGlobal() {
		return nil
	}
	if m.administrators == nil {
		return httpio.NewForbiddenMessage(gone.unnamed)
	}
	if _, inDomain := scope.Domain(); !inDomain {
		return nil
	}
	if !slices.Contains(m.administrators.domain, gone.role) {
		return nil
	}
	holdings, err := m.administratorHoldings(ctx, txn, scope)
	if err != nil {
		return err
	}
	for i := range holdings {
		if gone.takes(&holdings[i], scope) {
			continue
		}
		// Another person administers the tenant, or the person keeps the same role
		// another way: in every tenant, or through a group that is not the one they
		// leave.
		if holdings[i].user != gone.user || holdings[i].role == gone.role {
			return nil
		}
	}

	// The tenant's key is an id nobody reads, so the sentence does not print it; it
	// says the application's word for a tenant.
	return httpio.NewForbiddenMessagef("you are the last administrator of this %s: give another person the role before you %s", m.tenantWord, gone.before)
}

// administratorHoldings lists every way a person administers the tenant scope names
// now: for each administrator role, held in the tenant or in every tenant, the people
// who hold it by a membership of their own that counts (countingHolders), and the
// people in a group that holds it there, where the group's role and the person's place
// both count. A group kept in one tenant gives its roles in that tenant alone, so a row
// of such a group anywhere else, written past the rules, makes nobody an administrator.
func (m *Manager) administratorHoldings(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope) ([]administratorHolding, error) {
	now := time.Now()
	var holdings []administratorHolding
	for _, held := range []accesstypes.PolicyScope{scope, accesstypes.EveryDomainPolicyScope()} {
		for _, role := range m.administrators.domain {
			at := access.NewMembershipKey(held, "", role)
			key := access.RoleKey{Kind: at.Kind, Axis: at.Axis, Domain: at.Domain, Role: role}
			own, err := m.countingHolders(ctx, txn, &key)
			if err != nil {
				return nil, err
			}
			for _, user := range own {
				holdings = append(holdings, administratorHolding{user: user, scope: held, role: role, group: ""})
			}
			groups, err := m.store.GroupsHolding(ctx, txn, &key)
			if err != nil {
				return nil, errors.Wrap(err, "identity.Store.GroupsHolding()")
			}
			for _, groupID := range groups {
				members, err := m.countingMembers(ctx, txn, groupID, held, role, now)
				if err != nil {
					return nil, err
				}
				for _, user := range members {
					holdings = append(holdings, administratorHolding{user: user, scope: held, role: role, group: GroupID(groupID)})
				}
			}
		}
	}

	return holdings, nil
}

// countingMembers lists the people who hold the role in the scope through the group
// now: the people whose place counts, where the group's role counts and is one the
// group gives its members.
func (m *Manager) countingMembers(
	ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, scope accesstypes.PolicyScope, role accesstypes.Role, now time.Time,
) ([]accesstypes.User, error) {
	group, found, err := m.store.Group(ctx, txn, groupID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.Group()")
	}
	if !found {
		return nil, nil
	}
	kept, err := group.kept()
	if err != nil {
		return nil, err
	}
	if _, inTenant := kept.Domain(); inTenant && kept != scope {
		return nil, nil
	}
	holding, err := m.groupHolding(ctx, txn, groupID, now)
	if err != nil {
		return nil, err
	}
	if !holding.periods.Roles[access.NewGroupRoleKey(GroupID(groupID), scope, role)].Contains(now) {
		return nil, nil
	}
	members := make([]accesstypes.User, 0, len(holding.members))
	for _, user := range holding.members {
		if holding.periods.Members[user].Contains(now) {
			members = append(members, user)
		}
	}

	return members, nil
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
