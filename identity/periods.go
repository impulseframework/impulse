package identity

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/tracer"
)

// The period of an assignment. A person's role, a person's place in a group and a
// group's role may each carry a start and an end (accesstypes.Period), kept with the
// assignment's row, and count only between them. The permission engine and the lists
// apply the period wherever they read an assignment; what is held here is the user
// management's half:
//
//   - changing a period is assigning again: the rule that gives a role, a place or a
//     group's role gives it the period handed, which replaces the one it had, and the
//     rule that gives it with none makes it stand until it is removed. A period that is
//     over already is refused: ending an assignment now is removing it;
//   - an assignment that has ended is nothing a person holds: it keeps no account, it
//     places nobody in a tenant, it makes nobody a tenant's administrator, and it keeps no
//     group from being deleted. One that has not begun is held: the person is known and
//     will hold it;
//   - an ended assignment goes at the person's next sign-in (SignedIn, ClearEnded), and
//     nothing runs on a schedule;
//   - nobody lengthens their own holding: the caller's own role, their own place in a
//     group and a role of a group they are in are given a longer period by someone else
//     who may (refuseLongerForOneself). A role taken for a set time is the one exception
//     (TakeRole): taking it again gives it the new time, and nothing is refused.
//
// A person's role carries the reason it was given for beside its period, one line of
// plain text (access.ValidateReason). The two are given together and replaced together:
// a role given again carries the reason of the new request, none where the request gave
// none, and nothing edits a reason by itself (reasons.go).
//
// The rules that refuse read the present from the wall clock. The removal of what has
// ended reads it from the database's clock, the one the lists and the sign-in rule read:
// a row is removed only once the database says its period is over (EndedAssignments).

// EndedByItself is the source a removal carries when nobody made it: the assignment's
// period ended, and the row is removed at the person's next sign-in. A change record of
// the row, where the application keeps one, says so in place of a person's act.
const EndedByItself = "the assignment ended by itself"

// PeriodStore is what the rules of an assignment's period need of one auth's tables,
// which the generated package of the auth implements over its own typed queries and
// patches. An implementation holds no rule: it reads and writes the two instants.
type PeriodStore interface {
	// SetMembershipPeriod buffers the period and the reason on a membership row,
	// committed or written earlier in this transaction: both columns of the period and
	// the reason are written, an empty reason as none.
	SetMembershipPeriod(ctx context.Context, txn resource.ReadWriteTransaction, key *access.MembershipKey, period accesstypes.Period, reason string) error
	// SetGroupMemberPeriod buffers the period on the row of a person's place in a group.
	SetGroupMemberPeriod(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupMemberKey, period accesstypes.Period) error
	// SetGroupRolePeriod buffers the period on the row of a group's role.
	SetGroupRolePeriod(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupRoleKey, period accesstypes.Period) error
	// UserPeriods reads every assignment of a person's own, each with its period: their
	// memberships, wherever each is held, each with its reason too, and their places in
	// groups. It reads through
	// any transaction, a read of its own among them, so a sign-in with nothing to clear
	// opens no transaction.
	UserPeriods(ctx context.Context, txn resource.ReadOnlyTransaction, userID impulse.UUID) (*UserPeriods, error)
	// GroupPeriods reads every member of a group and every role it holds, each with its
	// period.
	GroupPeriods(ctx context.Context, txn resource.ReadOnlyTransaction, groupID impulse.UUID) (*GroupPeriods, error)
	// EndedAssignments reads the person's own assignments whose period the database
	// says is over: the memberships and the places in groups whose end is at or before
	// the database's present, never the application server's. It reads through any
	// transaction, a read of its own among them, so a sign-in with nothing to clear
	// opens no transaction.
	EndedAssignments(ctx context.Context, txn resource.ReadOnlyTransaction, userID impulse.UUID) (memberships []access.MembershipKey, places []access.GroupMemberKey, err error)
	// RemoveEnded buffers the removal of memberships and places whose period ended,
	// each under the source EndedByItself in place of a person's act.
	RemoveEnded(ctx context.Context, txn resource.ReadWriteTransaction, memberships []access.MembershipKey, places []access.GroupMemberKey) error
}

// UserPeriods is the periods of a person's own assignments: an assignment with no
// period is there with the zero Period. Reasons holds the reason of each membership
// given with one; a membership with none is absent from it.
type UserPeriods struct {
	Memberships map[access.MembershipKey]accesstypes.Period
	Places      map[access.GroupMemberKey]accesstypes.Period
	Reasons     map[access.MembershipKey]string
}

// sortEnded puts the assignments a removal names in a stable order of writes.
func sortEnded(memberships []access.MembershipKey, places []access.GroupMemberKey) {
	slices.SortFunc(memberships, func(a, b access.MembershipKey) int {
		return strings.Compare(a.Kind+"/"+a.Axis+"/"+a.Domain+"/"+string(a.Role), b.Kind+"/"+b.Axis+"/"+b.Domain+"/"+string(b.Role))
	})
	slices.SortFunc(places, func(a, b access.GroupMemberKey) int { return strings.Compare(string(a.Group), string(b.Group)) })
}

// GroupPeriods is the periods of a group's members and of the roles it holds.
type GroupPeriods struct {
	Members map[accesstypes.User]accesstypes.Period
	Roles   map[access.GroupRoleKey]accesstypes.Period
}

// RequestPeriod answers the period a request's two optional times name, each nil for
// none: the start is the first instant an assignment counts and the end the first it no
// longer does. A period that ends at or before its start never counts and is a Bad
// Request. The generated methods that give a role, a place in a group or a group's role
// build their period with it.
func RequestPeriod(startsAt, endsAt *time.Time) (accesstypes.Period, error) {
	period, err := accesstypes.NewPeriod(startsAt, endsAt)
	if err != nil {
		return accesstypes.Period{}, httpio.NewBadRequestMessage(err.Error())
	}

	return period, nil
}

// refuseEnded refuses a period that is over already: an assignment given with it would
// count for nothing from the start. what names the assignment in the refusal.
func refuseEnded(period accesstypes.Period, what string) error {
	if period.Ended(time.Now()) {
		return httpio.NewBadRequestMessagef("the period ended at %s, which is past, so %s would count for nothing: give a later end, or take it away to end it now",
			period.End.Format(time.RFC3339), what)
	}

	return nil
}

// AssignRoleFor is Tenant.AssignRole with the period the role is held for and the reason
// it is given for (see Manager.AssignRoleFor).
func (t *Tenant) AssignRoleFor(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, role accesstypes.Role, period accesstypes.Period, reason string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := t.manager.RequireUser(ctx, txn, userID); err != nil {
		return err
	}

	return t.manager.AssignRoleFor(ctx, txn, t.scope(), userID, role, period, reason)
}

// AddGroupMemberFor buffers a person's place in a group of the tenant, for the period
// (see Manager.AddGroupMemberFor). The person is one the tenant already has: a row of
// its own list of the auth's people, held here as the generated frame holds the person
// of a role given in the tenant, since the frame of this method locates the group. An
// account from outside the tenant is not found there (requireTenantPerson), and is put
// into a tenant's group by whoever manages the whole application's people, through the
// whole application's method: a tenant's group holds any account, and a tenant's
// administrator puts in the ones the tenant has.
func (t *Tenant) AddGroupMemberFor(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID, period accesstypes.Period) error {
	if err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return err
	}
	if err := t.requireTenantPerson(ctx, txn, userID); err != nil {
		return err
	}

	return t.manager.AddGroupMemberFor(ctx, txn, groupID, userID, period)
}

// GiveGroupRoleFor is Tenant.GiveGroupRole with the period the group holds the role for
// (see Manager.GiveGroupRoleFor).
func (t *Tenant) GiveGroupRoleFor(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, role accesstypes.Role, period accesstypes.Period) error {
	if err := t.requireTenantGroup(ctx, txn, groupID); err != nil {
		return err
	}

	return t.manager.GiveGroupRoleFor(ctx, txn, groupID, t.scope(), role, period)
}

// AssignRole is AssignRoleFor with no period and no reason: the membership stands until
// it is removed, and one the person held for a period loses the period and the reason it
// was given for.
func (m *Manager) AssignRole(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role) error {
	var none accesstypes.Period

	return m.AssignRoleFor(ctx, txn, scope, userID, role, none, "")
}

// AddGroupMember is AddGroupMemberFor with no period: the person is in the group until
// they are taken out, and a place they had for a period loses the period.
func (m *Manager) AddGroupMember(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) error {
	var none accesstypes.Period

	return m.AddGroupMemberFor(ctx, txn, groupID, userID, none)
}

// GiveGroupRole is GiveGroupRoleFor with no period: the group holds the role until it is
// taken away, and a role it held for a period loses the period.
func (m *Manager) GiveGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	var none accesstypes.Period

	return m.GiveGroupRoleFor(ctx, txn, groupID, scope, role, none)
}

// settleMembershipPeriod gives a membership the period and the reason: the row was
// written by the caller (inserted) or is there already, with the period and the reason
// it had. It reports whether either changed. A membership given again with the period
// and the reason it has is left exactly as it is. own says the membership is the
// request's caller's own, who gives it no longer a period than it has
// (refuseLongerForOneself).
func (m *Manager) settleMembershipPeriod(
	ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, key *access.MembershipKey, given *given, inserted, own bool,
) (bool, error) {
	var had accesstypes.Period
	var why string
	if !inserted {
		periods, err := m.store.UserPeriods(ctx, txn, userID)
		if err != nil {
			return false, errors.Wrap(err, "identity.Store.UserPeriods()")
		}
		had, why = periods.Memberships[*key], periods.Reasons[*key]
		if err := refuseLongerForOneself(own, had, given.period, "your own role"); err != nil {
			return false, err
		}
	}
	if had.Equal(given.period) && why == given.reason {
		return false, nil
	}
	if err := m.store.SetMembershipPeriod(ctx, txn, key, given.period, given.reason); err != nil {
		return false, errors.Wrap(err, "identity.Store.SetMembershipPeriod()")
	}

	return true, nil
}

// settlePlacePeriod gives a person's place in a group the period, as
// settleMembershipPeriod gives a membership its own. own says the place is the request's
// caller's own.
func (m *Manager) settlePlacePeriod(
	ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, key *access.GroupMemberKey, period accesstypes.Period, inserted, own bool,
) (bool, error) {
	var had accesstypes.Period
	if !inserted {
		periods, err := m.store.UserPeriods(ctx, txn, userID)
		if err != nil {
			return false, errors.Wrap(err, "identity.Store.UserPeriods()")
		}
		had = periods.Places[*key]
		if err := refuseLongerForOneself(own, had, period, "your own place in a group"); err != nil {
			return false, err
		}
	}
	if had.Equal(period) {
		return false, nil
	}
	if err := m.store.SetGroupMemberPeriod(ctx, txn, key, period); err != nil {
		return false, errors.Wrap(err, "identity.Store.SetGroupMemberPeriod()")
	}

	return true, nil
}

// settleGroupRolePeriod gives a group's role the period, as settleMembershipPeriod gives
// a membership its own. own says the request's caller is in the group, so the role is
// one of the caller's own holdings.
func (m *Manager) settleGroupRolePeriod(
	ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, key *access.GroupRoleKey, period accesstypes.Period, inserted, own bool,
) (bool, error) {
	var had accesstypes.Period
	if !inserted {
		periods, err := m.store.GroupPeriods(ctx, txn, groupID)
		if err != nil {
			return false, errors.Wrap(err, "identity.Store.GroupPeriods()")
		}
		had = periods.Roles[*key]
		if err := refuseLongerForOneself(own, had, period, "a role of a group you are in"); err != nil {
			return false, err
		}
	}
	if had.Equal(period) {
		return false, nil
	}
	if err := m.store.SetGroupRolePeriod(ctx, txn, key, period); err != nil {
		return false, errors.Wrap(err, "identity.Store.SetGroupRolePeriod()")
	}

	return true, nil
}

// standing answers what a person holds that has not ended: the memberships of their
// own and the groups they are in, less the ones whose period is over. An assignment
// whose period has not begun stands.
func (m *Manager) standing(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]access.MembershipKey, []GroupPlace, error) {
	own, err := m.store.Memberships(ctx, txn, userID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "identity.Store.Memberships()")
	}
	places, err := m.store.UserGroups(ctx, txn, userID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "identity.Store.UserGroups()")
	}
	periods, err := m.store.UserPeriods(ctx, txn, userID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "identity.Store.UserPeriods()")
	}
	now := time.Now()
	own = slices.DeleteFunc(own, func(key access.MembershipKey) bool { return periods.Memberships[key].Ended(now) })
	places = slices.DeleteFunc(places, func(place GroupPlace) bool {
		return periods.Places[access.GroupMemberKey{Group: GroupID(place.ID), User: User(userID)}].Ended(now)
	})

	return own, places, nil
}

// keeps answers everything that keeps a person's account: every membership of their
// own and a key for each group they are in, as the tables hold them, whatever their
// periods. The rule on an account nothing admits reads it: an assignment that has ended
// counts for nothing and still keeps its account, which goes when nothing at all is left
// of what was given to it, by an act of somebody's and never by a period ending.
func (m *Manager) keeps(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]access.MembershipKey, error) {
	kept, err := m.store.Memberships(ctx, txn, userID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.Memberships()")
	}
	places, err := m.store.UserGroups(ctx, txn, userID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.UserGroups()")
	}
	for _, place := range places {
		kept = append(kept, access.MembershipKey{Kind: place.Kind, Axis: place.Axis, Domain: place.Domain, Role: "", User: User(userID)})
	}

	return kept, nil
}

// countingHolders lists who holds the role where the key says it is held and counts as
// its holder now: the holders the store lists, less the ones whose membership is outside
// its period. The rule on a tenant's last administrator reads it: an administrator whose
// role has ended, or has not begun, administers nothing.
func (m *Manager) countingHolders(ctx context.Context, txn resource.ReadWriteTransaction, key *access.RoleKey) ([]accesstypes.User, error) {
	holders, err := m.store.RoleHolders(ctx, txn, key)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.RoleHolders()")
	}
	now := time.Now()
	counting := make([]accesstypes.User, 0, len(holders))
	for _, holder := range holders {
		id, ok := UserID(holder)
		if !ok {
			counting = append(counting, holder)

			continue
		}
		periods, err := m.store.UserPeriods(ctx, txn, id)
		if err != nil {
			return nil, errors.Wrap(err, "identity.Store.UserPeriods()")
		}
		held := access.MembershipKey{Kind: key.Kind, Axis: key.Axis, Domain: key.Domain, Role: key.Role, User: holder}
		if periods.Memberships[held].Contains(now) {
			counting = append(counting, holder)
		}
	}

	return counting, nil
}

// ClearEnded buffers the removal of the person's assignments whose period has ended:
// their own memberships and their places in groups. It answers how many rows go.
//
// What has ended is the database's to say, by its own clock, the one the lists and the
// sign-in rule read (EndedAssignments): a server whose clock runs fast removes nothing
// that still counts. The read that decides runs in the caller's transaction, the one
// that buffers the removals, so a row is removed by the transaction that read it as
// ended: the database fails the commit of a transaction whose read a concurrent change
// of the period made stale, and a period that has ended stays ended.
//
// It is nobody's act, so the rules of a removal do not judge it. It removes the ended
// rows and nothing else: the account stays, also when the ended assignment was the last
// thing the person held; the rule on a tenant's last administrator is not asked, since
// an ended role administers nothing; each row goes under the source EndedByItself; and
// the permission engine is told nothing, since an ended assignment already counts for
// nothing and no instance has anything to reload.
func (m *Manager) ClearEnded(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) (int, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	memberships, places, err := m.store.EndedAssignments(ctx, txn, userID)
	if err != nil {
		return 0, errors.Wrap(err, "identity.Store.EndedAssignments()")
	}
	if len(memberships)+len(places) == 0 {
		return 0, nil
	}
	sortEnded(memberships, places)
	if err := m.store.RemoveEnded(ctx, txn, memberships, places); err != nil {
		return 0, errors.Wrap(err, "identity.Store.RemoveEnded()")
	}
	for _, key := range memberships {
		logger.FromCtx(ctx).Infof("identity: the role %s of %s, held in %s, ended by itself and its row is removed", key.Role, key.User, heldWhere(key.Kind, key.Domain))
	}
	for _, key := range places {
		logger.FromCtx(ctx).Infof("identity: the place of %s in the group %s ended by itself and its row is removed", key.User, key.Group)
	}

	return len(memberships) + len(places), nil
}

// heldWhere says where a row is held, for a log line.
func heldWhere(kind, domain string) string {
	if domain != "" {
		return kind + " " + domain
	}

	return kind
}

// SignedIn answers the session library's hook for a sign-in that stands
// (session.Auth.SetSignedInHook): the person's assignments whose period has ended are
// removed, in a transaction of the hook's own on client, the application's database.
//
// The session library calls the hook once the transaction that made the session has
// committed, and logs an error it answers while the session stands, so the removal can
// neither fail a sign-in nor undo one. A sign-in with nothing to clear costs one read and
// opens no transaction. A person whose every assignment has ended is refused at the
// sign-in as one who holds nothing, so nothing is cleared for them: the rows do nothing
// and go with the account.
func (m *Manager) SignedIn(auth string, client resource.Client) session.SignedInHook {
	return func(ctx context.Context, signedIn *session.SignedIn) error {
		memberships, places, err := m.store.EndedAssignments(ctx, client, signedIn.UserID)
		if err != nil {
			return errors.Wrapf(err, "%s: reading the assignments of %q that have ended", auth, signedIn.Username)
		}
		if len(memberships)+len(places) == 0 {
			return nil
		}
		var cleared int
		err = client.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			var err error
			cleared, err = m.ClearEnded(ctx, txn, signedIn.UserID)

			return err
		})
		if err != nil {
			return errors.Wrapf(err, "%s: removing the assignments of %q that have ended", auth, signedIn.Username)
		}
		logger.FromCtx(ctx).Infof("%s: %d assignment(s) of %q had ended and went at their sign-in", auth, cleared, signedIn.Username)

		return nil
	}
}
