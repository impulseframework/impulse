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
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The rules that judge who is asking. A generated handler stamps the caller of the
// request on the context before the method runs (resource.CallerFrom), and a rule that
// keeps a person from acting on themselves reads who that is from there. In a session
// that views as someone else the caller is the person viewed as, whose grants the
// request runs with, so these rules hold a view to what that person may do to
// themselves; the rule on a person's own account judges the real person behind the
// session as well (actorIs). A method an application runs in a transaction of its own,
// outside any request, has no caller: what it does is the application's act, and these
// rules let it through.
//
// The rule on a person's own account is one rule with two reaches. Nobody disables or
// deletes themselves, in any session (refuseOwnAccount). And in a session that acts as
// someone else, nobody is the subject of anything they do: the real person behind the
// session changes nothing of their own account, a role, a place in a group, a password,
// an address, a name, a session, a token, whatever the session's mask keeps
// (refuseImpersonatorAsSubject, which an application's own action asks as
// RefuseImpersonatorAsSubject). There the second reach answers for the first as well:
// disabling, enabling and deleting the real person's own account are refused in one
// sentence form and logged with one line. A person who acts as an administrator would
// otherwise give themselves what the administrator may give and keep it after the
// session ends.
// Every method of the Manager that changes something of a person asks the rule, of the
// person's id or of the key the method reaches them by (a session, a name in the
// sign-in attempts table, an invitation's address), which a test that lists the
// Manager's methods holds. The rule reaches past the account to a record of the
// application's that decides what a person may do, a clearance or a place in a team:
// the generated write of such a record asks it about the person the row names, before
// the write and after it (RefuseImpersonatorAsRowSubject), about a set of theirs the row
// names (RefuseImpersonatorAsSetSubject), and about the row a value of theirs is read
// through (RefuseImpersonatorAsReadThroughSubject).
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

// actorIs reports whether the person acting in the request is the person of the account
// in this auth. The person acting is the real person behind the session (callerActor):
// the caller, or, in a session that views as someone else, whoever started it, and never
// the person viewed as. As for callerIs, a request bound to another auth is another
// auth's person, and an actor who has no account here, a person of another application,
// is nobody of this auth.
func (m *Manager) actorIs(ctx context.Context, userID impulse.UUID) bool {
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() {
		return false
	}
	if auth, bound := resource.AuthFrom(ctx); bound && auth != m.auth {
		return false
	}
	actor, local := callerActor(ctx, caller)

	return local && actor == User(userID)
}

// refuseOwnAccount refuses an act on the account of the person acting: nobody disables or
// deletes themselves, since the one who did could not undo it. The person acting is the
// real person behind the session, so "your own account" is the account of whoever is at
// the keyboard in every case. act names what was asked ("disable", "delete").
//
// In a session that acts as someone else the rule that answers is the one that refuses
// the enabling of the real person's own account there, in its sentence and with its log
// line (refuseImpersonatorAsSubject): the three switches of an account refuse the person
// who impersonates the same way. It is asked first, and reads the session where no
// generated route stamped a caller, so a method that calls this one needs no other. In a
// session of the person's own the refusal is the plain one (actorIs). A person who views
// as someone else is refused the account they view as too, and told so (callerIs): a
// session that views as a person does no more than that person may, and nobody disables
// or deletes themselves.
func (m *Manager) refuseOwnAccount(ctx context.Context, userID impulse.UUID, act string) error {
	if err := m.refuseImpersonatorAsSubject(ctx, userID, act+" your own account"); err != nil {
		return err
	}
	if m.actorIs(ctx, userID) {
		return httpio.NewForbiddenMessagef("you cannot %s your own account", act)
	}
	if m.callerIs(ctx, userID) {
		return httpio.NewForbiddenMessagef("you cannot %s the account you are viewing as", act)
	}

	return nil
}

// impersonatorOf is the real person behind a request that runs in a session established
// as someone or something else: the account, by its id, of whoever started the session,
// as its record names them (the actor callerActor reads). ok is false outside such a
// session, and for an actor who has no account here, a person of another application,
// whom no account of this application can be. The session is read off the request
// itself, so a rule that asks holds in a handler an application wrote too, where no
// generated route stamped a caller.
func impersonatorOf(ctx context.Context) (actor impulse.UUID, ok bool) {
	sess, found := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	if !found || sess.Impersonation == nil || !sess.Impersonation.ActorID.Valid {
		return impulse.NilUUID, false
	}

	return sess.Impersonation.ActorID.UUID, true
}

// impersonator is impersonatorOf for this auth's accounts. As for callerIs and actorIs,
// a request bound to another auth is another auth's person, whose id is no account of
// this one: on an outlet that signs its people in through one auth and serves the
// management of another, nobody who impersonates is a person of the auth managed.
func (m *Manager) impersonator(ctx context.Context) (actor impulse.UUID, ok bool) {
	if auth, bound := resource.AuthFrom(ctx); bound && auth != m.auth {
		return impulse.NilUUID, false
	}

	return impersonatorOf(ctx)
}

// refuseImpersonatorAsSubject is RefuseImpersonatorAsSubject as the Manager's own
// methods ask it, for an account of this auth (impersonator).
func (m *Manager) refuseImpersonatorAsSubject(ctx context.Context, userID impulse.UUID, act string) error {
	actor, acting := m.impersonator(ctx)
	if !acting || actor != userID {
		return nil
	}

	return impersonatorRefused(ctx, actor, act)
}

// RefuseImpersonatorAsSubject refuses an act whose subject is the real person behind a
// session that acts as someone else: in such a session nobody is the subject of what
// they do. subject is the account the act names, by its id, and act says what was
// asked, as the end of "you cannot" ("give yourself a role", "change your own email
// address"). It answers nil outside an impersonated session, and for any other person:
// the same act on someone else goes through, and the person's own row is still listed
// and read, since the rule is for changes.
//
// It is the rule that nobody disables or deletes themselves, widened for the person who
// impersonates to everything that changes their account. A person who acts as an
// administrator holds, for the length of the session, what the administrator may do; an
// act on their own account would leave them holding it afterward. The rule holds
// whatever the session's permission mask keeps, and whether or not the application
// declared the kind of impersonation, since it reads nothing but the session's record.
//
// Every method of the Manager that changes something of a person asks it, so the
// generated management is held to it without a line in the application, and so does
// the generated write of a record that decides what a person may do, through
// RefuseImpersonatorAsRowSubject and the two rules beside it for a row that reaches the
// person without naming them: a pass given to a team they are in is refused
// (RefuseImpersonatorAsSetSubject), and so is their own team moved to another division
// (RefuseImpersonatorAsReadThroughSubject). An application calls it in an action of its
// own that names a person, before the action changes anything. The person is named by
// the id of their account, which is no other account's, in one auth or across two, so
// the id alone says whether the person named is the one acting:
//
//	if err := identity.RefuseImpersonatorAsSubject(ctx, m.PilotID, "assign yourself a mission"); err != nil {
//		return errors.Wrap(err, "identity.RefuseImpersonatorAsSubject()")
//	}
//
// What it cannot see is whatever reaches the person by another road: a role given to a
// group they are in, an invitation to an address they hold and never confirmed on the
// account, an act on a colleague who returns the favor. These are stated limits: a
// session does each of them unless its mask refuses it. An application closes them
// through its mask. A second account of the person's own is one more: the rule knows
// the real person by the account they signed in with, so another account of theirs is
// someone else's to it, and a person who acts as an administrator can enable it again,
// or disable or delete it, as the administrator can. No class refuses those three; an
// application that must hold this refuses them
// by name in the kind's mask (accesstypes.RefuseAction). The unlock knows the account by
// its username, its id and the address it confirmed, so a count the library keeps under
// an address the account holds unconfirmed (the mail of a first password or of an
// address change asked for) is cleared like anyone's. A mask that refuses the class
// of the actions that change who may do what (accesstypes.ClassAccess) refuses the
// library's share of those and the application's with it: the generator gives the
// class to the writes of every record that carries a subject value or a subject set,
// to the change of a record such a value is read through, and to the delete of the
// tenant record, which clears the tenant (ClearTenant). A record the generator
// does not class is refused by name (accesstypes.RefuseWrites).
func RefuseImpersonatorAsSubject(ctx context.Context, subject impulse.UUID, act string) error {
	if !ImpersonatorIs(ctx, subject) {
		return nil
	}

	return impersonatorRefused(ctx, subject, act)
}

// ImpersonatorIs reports whether the account is the real person's behind the request's
// session, where the session acts as someone else: the question
// RefuseImpersonatorAsSubject refuses by, for a route that answers whether an act is
// offered. A page offers a person only what they may do, so the route that says whether
// to offer an act asks this, and the act itself asks RefuseImpersonatorAsSubject, which
// says why and logs the refusal. It is false outside an impersonated session, and for
// an actor who has no account here.
func ImpersonatorIs(ctx context.Context, subject impulse.UUID) bool {
	actor, acting := impersonatorOf(ctx)

	return acting && actor == subject
}

// impersonatorRefused is the refusal of RefuseImpersonatorAsSubject and of the rules
// that reach the person by another key, logged as what it is: the real person behind an
// impersonated session asked for something whose subject is themselves. The sentence
// and the line are the ones the session API's own three account switches give
// (sessioninfo.ImpersonatorRefusal).
func impersonatorRefused(ctx context.Context, actor impulse.UUID, act string) error {
	line, refusal := sessioninfo.ImpersonatorRefusal(actor, act)
	logger.FromCtx(ctx).Info(line)

	return refusal
}

// refuseImpersonatorsSession is refuseImpersonatorAsSubject for a session named by its
// id: one of the real person's own sessions, the ones they signed in to themselves, is
// not ended from a session that acts as someone else. A session established as someone
// or something else is none of them, whoever it acts as, the real person themselves
// included: its row names the person acted as, or nobody's account where it acts as a
// role, and its impersonation record says who started it. So the person who started
// one ends it like any other, here and by the session library's own way out, and a
// session somebody else started as them is theirs to end too.
func (m *Manager) refuseImpersonatorsSession(ctx context.Context, txn resource.ReadWriteTransaction, sessionID impulse.UUID) error {
	actor, acting := m.impersonator(ctx)
	if !acting {
		return nil
	}
	own, err := m.store.LiveSessions(ctx, txn, actor)
	if err != nil {
		return errors.Wrap(err, "identity.Store.LiveSessions()")
	}
	if !slices.Contains(own, sessionID) {
		return nil
	}
	if _, impersonated, err := m.store.Impersonation(ctx, txn, sessionID); err != nil {
		return errors.Wrap(err, "identity.Store.Impersonation()")
	} else if impersonated {
		return nil
	}

	return impersonatorRefused(ctx, actor, "end a session of your own")
}

// refuseImpersonatorsName is refuseImpersonatorAsSubject for a row of the sign-in
// attempts table named as the list shows it: the count kept under the real person's own
// username, and the library's own counts for their account, the mail and the reset links
// their confirmed address is sent and the guesses at the code of their first password
// (session.AccountAttemptKeys), are not cleared from a session that acts as someone
// else. A count kept under an address the account holds and has not confirmed is not
// known here: the store answers the confirmed address alone.
func (m *Manager) refuseImpersonatorsName(ctx context.Context, txn resource.ReadWriteTransaction, name string) error {
	actor, acting := m.impersonator(ctx)
	if !acting {
		return nil
	}
	username, found, err := m.store.Username(ctx, txn, actor)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	if !found {
		return nil
	}
	email, _, err := m.store.ConfirmedEmail(ctx, txn, actor)
	if err != nil {
		return errors.Wrap(err, "identity.Store.ConfirmedEmail()")
	}
	if !slices.Contains(session.AccountAttemptKeys(actor, username, email), session.SignInAttemptKey(name)) {
		return nil
	}

	return impersonatorRefused(ctx, actor, "unlock your own sign-in")
}

// refuseImpersonatorsAddress is refuseImpersonatorAsSubject for an invitation, which
// names a person by an address: the account that would accept it is the one that has
// confirmed the address, as the rule of one tenant per person reads it
// (refuseInvitedElsewhere), and an invitation to the real person's own account is not
// sent from a session that acts as someone else. email is the bare address, as the
// session library prepared it. An address nobody confirmed on the account says nothing
// of who reads its mail, so the rule does not see it.
func (m *Manager) refuseImpersonatorsAddress(ctx context.Context, txn resource.ReadWriteTransaction, email string) error {
	actor, acting := m.impersonator(ctx)
	if !acting {
		return nil
	}
	holder, found, err := m.store.UserByVerifiedEmail(ctx, txn, email)
	if err != nil {
		return errors.Wrap(err, "identity.Store.UserByVerifiedEmail()")
	}
	if !found || holder != actor {
		return nil
	}

	return impersonatorRefused(ctx, actor, "invite yourself")
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
	// person's own, and kept is where that group is kept, nil for a membership;
	// wholeGroup says the group itself loses the role, so everyone in it does, and not
	// the one person their place.
	group      accesstypes.Group
	kept       *accesstypes.PolicyScope
	wholeGroup bool
	// unnamed is the refusal where the application named no administrator roles, and
	// before what the person does last ("remove your own"), for the refusal of the last
	// administrator.
	unnamed string
	before  string
}

// takes reports whether the removal takes the holding away: the membership of the
// person's own held in the tenant, the person's holdings through the group they leave,
// or everyone's holding of the role through the group that loses it there.
func (r *removal) takes(h *administratorHolding) bool {
	switch {
	case r.group == "":
		return h.group == "" && h.user == r.user && h.role == r.role
	case r.wholeGroup:
		return h.group == r.group && h.role == r.role
	default:
		return h.group == r.group && h.user == r.user
	}
}

// administratorHolding is one way a person administers a tenant now: an administrator
// role they hold in the tenant itself, by a membership of their own (group is empty) or
// through a group kept in the tenant.
type administratorHolding struct {
	user  accesstypes.User
	role  accesstypes.Role
	group accesstypes.Group
}

// refuseLastAdministrator judges a person's removal of a role from themselves in a
// tenant: a membership of their own they remove, a group that gives them the role and
// that they leave, and a role they take from a group they are in. The role goes unless
// it is an administrator role of the tenant and nobody else would hold one there after:
// a tenant is never left without an administrator of its own, and a person who gives a
// role up otherwise is given it back by one who remains. The holders counted are the
// tenant's own, the ones the permission engine counts (access.AdministersTenant): the
// people who hold the role in the tenant itself, by a membership of their own or
// through a group kept in the tenant, each inside its period (administratorHoldings).
// A holder through a group of the tenant is a holder. The person keeps the tenant's
// administration too where they hold the same role there another way, by a membership
// beside the group or through another group of the tenant. Without the administrator
// roles (WithAdministratorRoles) a person removes no role from themselves in a tenant
// or in every tenant.
//
// A person who holds the role in every tenant, or through a group kept for the whole
// application, is no administrator of the tenant's own: a tenant that admits its people
// through its directory only keeps them out with everyone else, so they do not stand in
// for the tenant's last administrator, and the tenant's last one keeps the role while
// they are all it would have. What such a holding gives is not the tenant's
// administration to lose, so it goes: a role held in every tenant, and a role a group
// kept for the whole application holds in the tenant, which a person in the group takes
// from it or leaves behind.
//
// A role held in the global partition goes: the application's last administrator is its
// last super administrator, whose role is kept by a rule of its own, whoever asks
// (refuseLastSuperAdministrator), and a super administrator gives any other role back.
func (m *Manager) refuseLastAdministrator(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, gone *removal) error {
	if scope.IsGlobal() {
		return nil
	}
	if m.administrators == nil {
		return httpio.NewForbiddenMessage(gone.unnamed)
	}
	if _, counts := access.AdministersTenant(m.administrators.domain, scope, gone.role, gone.kept); !counts {
		return nil
	}
	holdings, err := m.administratorHoldings(ctx, txn, scope)
	if err != nil {
		return err
	}
	for i := range holdings {
		if gone.takes(&holdings[i]) {
			continue
		}
		// Another person administers the tenant, or the person keeps the same role
		// there another way: by a membership of their own, or through a group of the
		// tenant that is not the one they leave.
		if holdings[i].user != gone.user || holdings[i].role == gone.role {
			return nil
		}
	}

	// The tenant's key is an id nobody reads, so the sentence does not print it; it
	// says the application's word for a tenant.
	return httpio.NewForbiddenMessagef("you are the last administrator of this %s: give another person the role before you %s", m.tenantWord, gone.before)
}

// administratorHoldings lists every way a person administers the tenant scope names
// now: for each administrator role held in the tenant itself, the people who hold it by
// a membership of their own that counts (countingHolders), and the people in a group
// kept in the tenant that holds it there, where the group's role and the person's place
// both count (countingMembers). A role held in every tenant is not read: its holder is
// no administrator of the tenant's own (access.AdministersTenant).
func (m *Manager) administratorHoldings(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope) ([]administratorHolding, error) {
	now := time.Now()
	var holdings []administratorHolding
	for _, role := range m.administrators.domain {
		at := access.NewMembershipKey(scope, "", role)
		key := access.RoleKey{Kind: at.Kind, Axis: at.Axis, Domain: at.Domain, Role: role}
		own, err := m.countingHolders(ctx, txn, &key)
		if err != nil {
			return nil, err
		}
		for _, user := range own {
			holdings = append(holdings, administratorHolding{user: user, role: role, group: ""})
		}
		groups, err := m.store.GroupsHolding(ctx, txn, &key)
		if err != nil {
			return nil, errors.Wrap(err, "identity.Store.GroupsHolding()")
		}
		for _, groupID := range groups {
			members, err := m.countingMembers(ctx, txn, groupID, scope, role, now)
			if err != nil {
				return nil, err
			}
			for _, user := range members {
				holdings = append(holdings, administratorHolding{user: user, role: role, group: GroupID(groupID)})
			}
		}
	}

	return holdings, nil
}

// countingMembers lists the people the role makes administrators of the tenant scope
// names through the group now: the people whose place counts, where the group's role
// counts and the group is kept in the tenant. A group kept anywhere else makes nobody an
// administrator of the tenant's own, whatever it holds there: one kept for the whole
// application seats the application's own people, and a row of another tenant's group,
// written past the rules, gives nothing here (access.AdministersTenant).
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
	if _, counts := access.AdministersTenant(m.administrators.domain, scope, role, &kept); !counts {
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
