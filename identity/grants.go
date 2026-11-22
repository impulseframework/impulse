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

// A group gives nobody a power that giving the role directly would not.
//
// A role file says who gives a role and who takes one away, in the grants of the
// methods that do it, and can say which roles (a condition on the grant reads the role).
// A change to a group is a change to what people hold: a person put in a group is given
// each role the group holds, a person taken out loses each, a role given to a group is
// given to everyone in it, and one taken from it is taken from each of them. So each of
// the four changes is held to what the same change made directly needs, beside the grant
// of the group's own method, which says who manages groups at all:
//
//   - putting a person in a group needs, for each role the group holds, what giving that
//     person the role there needs;
//   - taking a person out needs, for each, what taking the role from them needs;
//   - giving a group a role needs what giving it to each person in the group needs;
//   - taking a role from a group needs what taking it from each of them needs.
//
// What a role change needs of the caller is the role file's to say, so the rules ask the
// auth's generated package (RoleGrants), which asks the permission engine for the grant
// of the method that gives a role, or takes one, with the role in question. An
// assignment whose period has ended gives nobody anything, and is not judged; one that
// has not begun is, since the person will hold it.

// RoleGrants answers what the request's caller may do to a person's role, by the grants
// of the methods that give a role and take one away. The generated package of the auth
// implements it over the request's own check (resource.Caller.ExecuteGate): for a request
// made under a tenant, the grant of the tenant's method; for one made for the whole
// application, the grant of the whole application's. An implementation holds no rule of
// its own.
//
// The default role of a sign-in method opened to sign-up is asked the same way
// (Manager.OpenSignUp), since opening the method gives the role to every person who
// signs up: for the whole application, of nobody in particular (the nil id, no account
// yet), and from the list of roles the dialog offers with no transaction (nil), which
// the whole application's method, naming no row, does not read.
type RoleGrants interface {
	// MayGive reports whether the caller may give the person the role in the scope: the
	// grant of the method that gives a role admits it for that role and that person.
	MayGive(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role) (bool, error)
	// MayTake reports whether the caller may take the role the person holds in the scope
	// away: the grant of the method that takes a role away admits it for that role.
	MayTake(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role) (bool, error)
	// CouldGive reports whether the caller could give the role in the scope by hand, to
	// somebody, with the method named: one of the methods a person gives a role with
	// (the one that gives a person a role, the invitation that starts a person with
	// one, the one that gives a group a role), by the name its grant is
	// written under. The answer is that method's own and no other's: each has its own
	// grant, and a role one of them takes from this caller another may refuse. It is the
	// question of a list a role is picked from (Manager.GivableRoles,
	// Manager.RolesToGive), asked for the method whose dialog picks, before anybody is
	// named: where what is left of the grant's condition reads the row the method names
	// (the person, the group), the role could be given to some of them, and the answer
	// is yes. It reads no row, and so takes no transaction. A method that gives no role
	// by hand where the request was made is an error and never an answer.
	CouldGive(ctx context.Context, method accesstypes.Resource, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error)
}

// WithRoleGrants hands the manager the answers of the role file's grants on the methods
// that give a role and take one away (RoleGrants), which the rules of a group read: a
// change to a group is held to what the role changes it makes need. The generated client
// passes its own; an application does not. A manager built without them refuses a change
// to a group that a request makes, since it could not judge it; a call outside any
// request is the application's own act and is not judged.
func WithRoleGrants(grants RoleGrants) Option {
	return func(m *Manager) {
		m.grants = grants
	}
}

// groupHolding is what a group gives now or will: the roles it holds and the people in
// it, less the ones whose period has ended, with the periods of each.
type groupHolding struct {
	roles   []access.GroupRoleKey
	members []accesstypes.User
	periods *GroupPeriods
}

// groupHolding reads what a group gives: the roles it holds and the people in it whose
// period has not ended, as committed. now is the instant the periods are read at.
func (m *Manager) groupHolding(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, now time.Time) (*groupHolding, error) {
	roles, err := m.store.GroupRoles(ctx, txn, groupID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.GroupRoles()")
	}
	members, err := m.store.GroupMembers(ctx, txn, groupID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.GroupMembers()")
	}
	periods, err := m.store.GroupPeriods(ctx, txn, groupID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.GroupPeriods()")
	}
	roles = slices.DeleteFunc(roles, func(key access.GroupRoleKey) bool { return periods.Roles[key].Ended(now) })
	members = slices.DeleteFunc(members, func(user accesstypes.User) bool { return periods.Members[user].Ended(now) })

	return &groupHolding{roles: roles, members: members, periods: periods}, nil
}

// judged reports whether a change to a group is one the role grants judge: it is made by
// a request. A manager a request reaches without the grants cannot judge it, which is an
// error and never a pass.
func (m *Manager) judged(ctx context.Context) (bool, error) {
	if resource.CallerFrom(ctx).IsZero() {
		return false, nil
	}
	if m.grants == nil {
		return false, errors.New("a request changes a group and the manager holds no role grants to judge it by: build it with identity.WithRoleGrants, as the generated client does")
	}

	return true, nil
}

// roleInWords names a role as a role picker shows it: its words in lower case with the
// first in upper case, so "SectorMarshal" is "Sector marshal". A sentence a person reads
// names a role this way, never by its code name, which the refusal's cause keeps for the
// server's log.
func roleInWords(role accesstypes.Role) string {
	return sentenceStart(inWords(string(role)))
}

// refuseGivingThrough holds a change that gives people a group's roles to what giving
// each role directly needs: every person named is asked for every role named, and the
// first the caller may not give refuses the change. act says what was asked, as a
// sentence a person reads completes ("putting a person in the group Night shift gives
// them"). The person is told the act and the role it gives that they may not give, in
// one sentence that names the role in words (roleInWords): an administrator stopped by
// one of a group's roles needs to know which. The role's code name, and who gives it,
// is the refusal's cause, kept for the server's log.
func (m *Manager) refuseGivingThrough(ctx context.Context, txn resource.ReadWriteTransaction, kept accesstypes.PolicyScope, roles []access.GroupRoleKey, users []accesstypes.User, act string) error {
	return m.judgeThrough(ctx, txn, kept, roles, users, m.grants.MayGive, func(role accesstypes.Role) error {
		return httpio.NewForbiddenMessageWithError(
			errors.Newf("%s the role %s, which you may not give them: someone who may give the role does it", act, string(role)),
			sentenceStart(act)+" the role "+roleInWords(role)+", which you may not give them.",
		)
	})
}

// refuseTakingThrough holds a change that takes a group's roles from people to what
// taking each role away directly needs, as refuseGivingThrough holds one that gives
// them.
func (m *Manager) refuseTakingThrough(ctx context.Context, txn resource.ReadWriteTransaction, kept accesstypes.PolicyScope, roles []access.GroupRoleKey, users []accesstypes.User, act string) error {
	return m.judgeThrough(ctx, txn, kept, roles, users, m.grants.MayTake, func(role accesstypes.Role) error {
		return httpio.NewForbiddenMessageWithError(
			errors.Newf("%s the role %s, which you may not take from them: someone who may take the role away does it", act, string(role)),
			sentenceStart(act)+" the role "+roleInWords(role)+", which you may not take from them.",
		)
	})
}

// judgeThrough asks may for every role and every person, and answers refused for the
// first role the caller may not change for one of them. A person whose name is no
// account's id is nobody the role file speaks of, and is not asked for. kept is where
// the group is kept, which says whether a role it holds makes its people a tenant's
// administrators.
func (m *Manager) judgeThrough(
	ctx context.Context, txn resource.ReadWriteTransaction, kept accesstypes.PolicyScope, roles []access.GroupRoleKey, users []accesstypes.User,
	may func(context.Context, resource.ReadWriteTransaction, accesstypes.PolicyScope, impulse.UUID, accesstypes.Role) (bool, error),
	refused func(accesstypes.Role) error,
) error {
	for i := range roles {
		scope, err := roles[i].Scope()
		if err != nil {
			return errors.Wrap(err, "access.GroupRoleKey.Scope()")
		}
		// The administrator's role of a directory-only tenant is the tenant's to hand
		// out, whoever is in the group and though nobody is yet.
		if err := m.requireEnteringToChange(ctx, scope, roles[i].Role, &kept); err != nil {
			return err
		}
		for _, user := range users {
			userID, ok := UserID(user)
			if !ok {
				continue
			}
			allowed, err := may(ctx, txn, scope, userID, roles[i].Role)
			if err != nil {
				return errors.Wrap(err, "identity.RoleGrants")
			}
			if !allowed {
				return refused(roles[i].Role)
			}
		}
	}

	return nil
}

// callerUser answers the request's caller as a person of this auth: the name the
// permission store holds them under, the id of their account. ok is false outside a
// request and for a caller who came through another auth, who is nobody of this one
// (callerIs).
func (m *Manager) callerUser(ctx context.Context) (user accesstypes.User, ok bool) {
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() {
		return "", false
	}
	if auth, bound := resource.AuthFrom(ctx); bound && auth != m.auth {
		return "", false
	}

	return caller.Permissions.User(), true
}

// lengthens reports whether an assignment given the period next would count at an
// instant from now on at which it does not count with the period it had: a later end or
// none, a start that comes sooner while it has not begun, and any period at all once
// the one it had has ended.
func lengthens(had, next accesstypes.Period, now time.Time) bool {
	if had.Ended(now) {
		return !next.Ended(now)
	}
	if !had.End.IsZero() && (next.End.IsZero() || next.End.After(had.End)) {
		return true
	}

	return had.Upcoming(now) && (next.Start.IsZero() || next.Start.Before(had.Start))
}

// shortens reports whether an assignment that counts now would, given the period next,
// stop counting at an instant from now on at which it counts with the period it had: a
// start that has not come, and an end where it had none or one that came later. An
// assignment that does not count now, because its period is over or has not begun, is
// not shortened by any period: it gives nothing today that a new period could take.
func shortens(had, next accesstypes.Period, now time.Time) bool {
	if !had.Contains(now) {
		return false
	}
	if !next.Contains(now) {
		return true
	}

	return !next.End.IsZero() && (had.End.IsZero() || next.End.Before(had.End))
}

// refuseLongerForOneself is the rule that nobody lengthens their own holding: an act of
// the caller that would make something the caller holds last longer than it was given
// for is refused, and is done by someone else who may. own says the assignment is the
// caller's own (their role, their place in a group, or a role of a group they are in),
// and what names it in the refusal ("your own role"). The person is told that they
// cannot, in one sentence; who does it instead is the refusal's cause, kept for the
// server's log.
func refuseLongerForOneself(own bool, had, next accesstypes.Period, what string) error {
	if !own || !lengthens(had, next, time.Now()) {
		return nil
	}

	return httpio.NewForbiddenMessageWithErrorf(
		errors.Newf("you cannot make %s last longer than it was given for: someone else who may give it gives it a new period", what),
		"You cannot make %s last longer than it was given for.", what,
	)
}
