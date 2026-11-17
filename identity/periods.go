package identity

import (
	"context"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
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
//     rule that gives it with none makes it stand until it is removed;
//   - an assignment that has ended is nothing a person holds: it keeps no account, it
//     places nobody in a tenant, it makes nobody a tenant's administrator, and it keeps no
//     group from being deleted. One that has not begun is held: the person is known and
//     will hold it;
//   - an ended assignment goes at the person's next sign-in (SignedIn, ClearEnded), and
//     nothing runs on a schedule.
//
// The rules read the present from the wall clock.

// EndedByItself is the source a removal carries when nobody made it: the assignment's
// period ended, and the row is removed at the person's next sign-in. A change record of
// the row, where the application keeps one, says so in place of a person's act.
const EndedByItself = "the assignment ended by itself"

// PeriodStore is what the rules of an assignment's period need of one auth's tables,
// which the generated package of the auth implements over its own typed queries and
// patches. An implementation holds no rule: it reads and writes the two instants.
type PeriodStore interface {
	// SetMembershipPeriod buffers the period on a membership row, committed or written
	// earlier in this transaction.
	SetMembershipPeriod(ctx context.Context, txn resource.ReadWriteTransaction, key *access.MembershipKey, period accesstypes.Period) error
	// SetGroupMemberPeriod buffers the period on the row of a person's place in a group.
	SetGroupMemberPeriod(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupMemberKey, period accesstypes.Period) error
	// SetGroupRolePeriod buffers the period on the row of a group's role.
	SetGroupRolePeriod(ctx context.Context, txn resource.ReadWriteTransaction, key *access.GroupRoleKey, period accesstypes.Period) error
	// UserPeriods reads every assignment of a person's own, each with its period: their
	// memberships, wherever each is held, and their places in groups. It reads through
	// any transaction, a read of its own among them, so a sign-in with nothing to clear
	// opens no transaction.
	UserPeriods(ctx context.Context, txn resource.ReadOnlyTransaction, userID impulse.UUID) (*UserPeriods, error)
	// GroupPeriods reads every member of a group and every role it holds, each with its
	// period.
	GroupPeriods(ctx context.Context, txn resource.ReadOnlyTransaction, groupID impulse.UUID) (*GroupPeriods, error)
	// RemoveEnded buffers the removal of memberships and places whose period ended,
	// each under the source EndedByItself in place of a person's act.
	RemoveEnded(ctx context.Context, txn resource.ReadWriteTransaction, memberships []access.MembershipKey, places []access.GroupMemberKey) error
}

// UserPeriods is the periods of a person's own assignments: an assignment with no
// period is there with the zero Period.
type UserPeriods struct {
	Memberships map[access.MembershipKey]accesstypes.Period
	Places      map[access.GroupMemberKey]accesstypes.Period
}

// ended answers the person's assignments whose period is over at the instant, sorted
// for a stable order of writes.
func (p *UserPeriods) ended(at time.Time) (memberships []access.MembershipKey, places []access.GroupMemberKey) {
	for key, period := range p.Memberships {
		if period.Ended(at) {
			memberships = append(memberships, key)
		}
	}
	for key, period := range p.Places {
		if period.Ended(at) {
			places = append(places, key)
		}
	}
	slices.SortFunc(memberships, func(a, b access.MembershipKey) int {
		return compareText(a.Kind+"/"+a.Axis+"/"+a.Domain+"/"+string(a.Role), b.Kind+"/"+b.Axis+"/"+b.Domain+"/"+string(b.Role))
	})
	slices.SortFunc(places, func(a, b access.GroupMemberKey) int { return compareText(string(a.Group), string(b.Group)) })

	return memberships, places
}

// compareText orders two strings.
func compareText(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// GroupPeriods is the periods of a group's members and of the roles it holds.
type GroupPeriods struct {
	Members map[accesstypes.User]accesstypes.Period
	Roles   map[access.GroupRoleKey]accesstypes.Period
}

// AssignRole is AssignRoleFor with no period: the membership stands until it is removed,
// and one the person held for a period loses the period.
func (m *Manager) AssignRole(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role) error {
	return m.AssignRoleFor(ctx, txn, scope, userID, role, accesstypes.Period{})
}

// AddGroupMember is AddGroupMemberFor with no period: the person is in the group until
// they are taken out, and a place they had for a period loses the period.
func (m *Manager) AddGroupMember(ctx context.Context, txn resource.ReadWriteTransaction, groupID, userID impulse.UUID) error {
	return m.AddGroupMemberFor(ctx, txn, groupID, userID, accesstypes.Period{})
}

// GiveGroupRole is GiveGroupRoleFor with no period: the group holds the role until it is
// taken away, and a role it held for a period loses the period.
func (m *Manager) GiveGroupRole(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	return m.GiveGroupRoleFor(ctx, txn, groupID, scope, role, accesstypes.Period{})
}

// settleMembershipPeriod gives a membership the period: the row was written by the
// caller (inserted) or is there already, with the period it had. It reports whether the
// period changed. A membership given again with the period it has is left exactly as it
// is.
func (m *Manager) settleMembershipPeriod(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, key *access.MembershipKey, period accesstypes.Period, inserted bool) (bool, error) {
	var had accesstypes.Period
	if !inserted {
		periods, err := m.store.UserPeriods(ctx, txn, userID)
		if err != nil {
			return false, errors.Wrap(err, "identity.Store.UserPeriods()")
		}
		had = periods.Memberships[*key]
	}
	if had.Equal(period) {
		return false, nil
	}
	if err := m.store.SetMembershipPeriod(ctx, txn, key, period); err != nil {
		return false, errors.Wrap(err, "identity.Store.SetMembershipPeriod()")
	}

	return true, nil
}

// settlePlacePeriod gives a person's place in a group the period, as
// settleMembershipPeriod gives a membership its own.
func (m *Manager) settlePlacePeriod(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, key *access.GroupMemberKey, period accesstypes.Period, inserted bool) (bool, error) {
	var had accesstypes.Period
	if !inserted {
		periods, err := m.store.UserPeriods(ctx, txn, userID)
		if err != nil {
			return false, errors.Wrap(err, "identity.Store.UserPeriods()")
		}
		had = periods.Places[*key]
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
// a membership its own.
func (m *Manager) settleGroupRolePeriod(ctx context.Context, txn resource.ReadWriteTransaction, groupID impulse.UUID, key *access.GroupRoleKey, period accesstypes.Period, inserted bool) (bool, error) {
	var had accesstypes.Period
	if !inserted {
		periods, err := m.store.GroupPeriods(ctx, txn, groupID)
		if err != nil {
			return false, errors.Wrap(err, "identity.Store.GroupPeriods()")
		}
		had = periods.Roles[*key]
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
// It is nobody's act, so the rules of a removal do not judge it. It removes the ended
// rows and nothing else: the account stays, also when the ended assignment was the last
// thing the person held; the rule on a tenant's last administrator is not asked, since
// an ended role administers nothing; each row goes under the source EndedByItself; and
// the permission engine is told nothing, since an ended assignment already counts for
// nothing and no instance has anything to reload.
func (m *Manager) ClearEnded(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) (int, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	periods, err := m.store.UserPeriods(ctx, txn, userID)
	if err != nil {
		return 0, errors.Wrap(err, "identity.Store.UserPeriods()")
	}
	memberships, places := periods.ended(time.Now())
	if len(memberships)+len(places) == 0 {
		return 0, nil
	}
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
		periods, err := m.store.UserPeriods(ctx, client, signedIn.UserID)
		if err != nil {
			return errors.Wrapf(err, "%s: reading the assignments of %q that have ended", auth, signedIn.Username)
		}
		if memberships, places := periods.ended(time.Now()); len(memberships)+len(places) == 0 {
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
