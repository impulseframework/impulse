// recordmembership.go is the management of the roles held on one record of a tenant: a
// person or a group given a role on one record (a project) holds the role's grants on
// that record and what it reaches, and nowhere else of the tenant (snapshot_records.go).
// Only a domain role the role file marks for the record's kind (Role.HeldOn) is given
// there, on a record the permission collection marks as one that holds roles, and a
// group's only where the group is kept in the record's tenant. The library never looks
// the record up: that the key names a row is the caller's, as for every label the store
// holds.

package access

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/words"
	"github.com/impulseframework/impulse/tracer"
)

// RecordMembershipKey is the key of one row of a person's roles on records
// (<Prefix><Store>RecordUserRoles), its columns in key order: the record's tenant (Axis
// and Domain), its kind (Resource) and its key (Record), the role in the spelling the
// store holds it, and the person as the engine names them.
type RecordMembershipKey struct {
	Axis     string
	Domain   string
	Resource string
	Record   string
	Role     accesstypes.Role
	User     accesstypes.User
}

// RecordGroupRoleKey is the key of one row of a group's roles on records
// (<Prefix><Store>RecordGroupRoles), its columns in key order: the record as
// RecordMembershipKey spells it, the role, and the group by its id.
type RecordGroupRoleKey struct {
	Axis     string
	Domain   string
	Resource string
	Record   string
	Role     accesstypes.Role
	Group    accesstypes.Group
}

// NewRecordMembershipKey spells user's role on the record as the store's row key. It
// checks nothing: it is the form for reading a row or deleting one. A row to be written
// comes from UserManager.RecordMembership, which holds it to the rule first.
func NewRecordMembershipKey(at accesstypes.RecordScope, user accesstypes.User, role accesstypes.Role) RecordMembershipKey {
	axis, domain, resource, record := policy.RecordColumns(at)

	return RecordMembershipKey{Axis: axis, Domain: domain, Resource: resource, Record: record, Role: role, User: user}
}

// NewRecordGroupRoleKey spells group's role on the record as the store's row key. It
// checks nothing: it is the form for reading a row or deleting one. A row to be written
// comes from UserManager.RecordGroupRole, which holds it to the rule first.
func NewRecordGroupRoleKey(group accesstypes.Group, at accesstypes.RecordScope, role accesstypes.Role) RecordGroupRoleKey {
	axis, domain, resource, record := policy.RecordColumns(at)

	return RecordGroupRoleKey{Axis: axis, Domain: domain, Resource: resource, Record: record, Role: role, Group: group}
}

// RecordHolder is one role held on a record, as the listing of the record's holders
// returns it: a person's (User) or a group's (Group, with the group's name), exactly one
// of the two set, and the period it counts in.
type RecordHolder struct {
	User      accesstypes.User
	Group     accesstypes.Group
	GroupName string
	Role      accesstypes.Role
	Period    accesstypes.Period
}

// The two who a role on a record is for: a person, or a group.
const (
	noPersonNamed = "No person is named. Name the person the role is for."
	noGroupNamed  = "No group is named. Name the group the role is for."
)

// refuseRecordRole holds a role to be given on the record to the rule, in this order:
// the record's tenant, kind and key are named, the kind is one the collection marks, the
// role is a domain role the role file marks for the kind, and the person or the group is
// named (noOne says which sentence). Each refusal is a 400 in words a person can act on.
func (u *userManager) refuseRecordRole(at accesstypes.RecordScope, role accesstypes.Role, named bool, noOne string) error {
	if at.Domain() == "" {
		return httpio.NewBadRequestMessage("No tenant is named. Name the tenant the record is in.")
	}
	one, _ := recordWords(u.collection, at.Resource())
	if !holdsRoles(u.collection, at.Resource()) {
		return httpio.NewBadRequestMessagef("No role can be held on one %s. Give the role in the tenant instead.", one)
	}
	if at.Record() == "" {
		return httpio.NewBadRequestMessagef("No %s is named. Name the %s the role is held on.", one, one)
	}
	if !slices.Contains(u.defaults.heldOn(role), at.Resource()) {
		return httpio.NewBadRequestMessagef("%s is not a role held on %s.", role, words.WithArticle(one))
	}
	if !named {
		return httpio.NewBadRequestMessage(noOne)
	}

	return nil
}

// refuseRecordGroup refuses a group kept anywhere but the record's tenant: a role on a
// record is held by a person or by a group of that tenant.
func (u *userManager) refuseRecordGroup(at accesstypes.RecordScope, kept accesstypes.PolicyScope) error {
	if kept == accesstypes.DomainPolicyScope(at.Domain()) {
		return nil
	}
	one, _ := recordWords(u.collection, at.Resource())
	where := "kept in another tenant"
	if kept.IsGlobal() {
		where = "kept for the whole application"
	}

	return httpio.NewBadRequestMessagef("A role on one %s is given to a person or to a group of the %s's tenant. This group is %s.", one, one, where)
}

// AssignRecordUserRole gives user the role on the record for the period, replacing the
// period of one the person holds there. The role is one the role file marks for the
// record's kind (Role.HeldOn), on a record the collection marks, and the period one the
// role can carry (ValidatePeriod); anything else is a 400.
func (u *userManager) AssignRecordUserRole(ctx context.Context, at accesstypes.RecordScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := u.refuseRecordRole(at, role, user != "", noPersonNamed); err != nil {
		return err
	}
	if err := ValidatePeriod(role, period); err != nil {
		return err
	}

	return u.store.assignRecordUserRole(ctx, at, user, role, period)
}

// DeleteRecordUserRoles takes the roles user holds on the record away. A role the person
// does not hold there is no error, and the role need not be one held on records any
// more: a row of a role since taken out of the role file's heldOn goes like any other.
func (u *userManager) DeleteRecordUserRoles(ctx context.Context, at accesstypes.RecordScope, user accesstypes.User, roles ...accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if user == "" {
		return httpio.NewBadRequestMessage(noPersonNamed)
	}
	for _, role := range roles {
		if err := u.store.deleteRecordUserRole(ctx, at, user, role); err != nil {
			return err
		}
	}

	return nil
}

// AssignRecordGroupRole gives group the role on the record for the period, replacing the
// period of one the group holds there. It is held to the rule AssignRecordUserRole is,
// and the group must be kept in the record's tenant, read from the group's row; a group
// that is not there is answered as AssignGroupRole answers it.
func (u *userManager) AssignRecordGroupRole(ctx context.Context, group accesstypes.Group, at accesstypes.RecordScope, role accesstypes.Role, period accesstypes.Period) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := u.refuseRecordRole(at, role, group != "", noGroupNamed); err != nil {
		return err
	}
	if err := ValidatePeriod(role, period); err != nil {
		return err
	}
	row, err := u.requireGroup(ctx, group)
	if err != nil {
		return err
	}
	if err := u.refuseRecordGroup(at, row.Scope); err != nil {
		return err
	}
	// A group deleted since its row was read is answered as one that is not there.
	if err := u.store.assignRecordGroupRole(ctx, group, at, role, period); err != nil {
		if errors.Is(err, policy.ErrNoGroup) {
			return groupNotFound(group)
		}

		return err
	}

	return nil
}

// DeleteRecordGroupRoles takes the roles group holds on the record away. A role the
// group does not hold there is no error, and the role need not be one held on records
// any more.
func (u *userManager) DeleteRecordGroupRoles(ctx context.Context, group accesstypes.Group, at accesstypes.RecordScope, roles ...accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if group == "" {
		return httpio.NewBadRequestMessage(noGroupNamed)
	}
	for _, role := range roles {
		if err := u.store.deleteRecordGroupRole(ctx, group, at, role); err != nil {
			return err
		}
	}

	return nil
}

// RecordHolders returns who holds what on the record and has not ended, one whose period
// has not begun among them: the people's first, by person then role, then the groups', by
// the group's name.
func (u *userManager) RecordHolders(ctx context.Context, at accesstypes.RecordScope) ([]RecordHolder, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	held, err := u.store.recordRoles(ctx, at)
	if err != nil {
		return nil, err
	}
	holders := make([]RecordHolder, 0, len(held))
	for i := range held {
		h := RecordHolder{Role: held[i].Role, Period: held[i].Period}
		if held[i].Member.Kind == policy.SubjectGroup {
			h.Group, h.GroupName = accesstypes.Group(held[i].Member.Name), held[i].GroupName
		} else {
			h.User = accesstypes.User(held[i].Member.Name)
		}
		holders = append(holders, h)
	}

	return holders, nil
}

// DeleteRecordRoles takes every role held on the record away, a person's and a group's,
// whatever their periods: what a record's deletion does.
func (u *userManager) DeleteRecordRoles(ctx context.Context, at accesstypes.RecordScope) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	return u.store.deleteRecordRoles(ctx, at)
}

// RolesHeldOn lists the roles that can be held on a record of the kind, with their
// descriptions, sorted by name: the domain roles the role file marks for it.
func (u *userManager) RolesHeldOn(record accesstypes.Resource) []RoleInfo {
	return u.defaults.rolesHeldOn(record)
}

// RecordMembership answers the key of the row that says user holds role on the record,
// for a caller that writes it inside a transaction of its own instead of through
// AssignRecordUserRole. The row is held to the rule AssignRecordUserRole holds its own
// to, less the period, which a caller that writes one asks ValidatePeriod about. The
// caller commits the row and then tells the engine with Controller.PolicyChanged.
func (u *userManager) RecordMembership(ctx context.Context, at accesstypes.RecordScope, user accesstypes.User, role accesstypes.Role) (RecordMembershipKey, error) {
	_, span := tracer.Start(ctx)
	defer span.End()

	if err := u.refuseRecordRole(at, role, user != "", noPersonNamed); err != nil {
		return RecordMembershipKey{}, err
	}

	return NewRecordMembershipKey(at, user, role), nil
}

// RecordGroupRole answers the key of the row that says group holds role on the record,
// for a caller that writes it inside a transaction of its own instead of through
// AssignRecordGroupRole. kept is where the group is kept, which the caller read with the
// group's row in that transaction. The row is held to the rule AssignRecordGroupRole
// holds its own to, less the period. The caller commits the row and then tells the
// engine with Controller.PolicyChanged.
func (u *userManager) RecordGroupRole(ctx context.Context, group accesstypes.Group, kept accesstypes.PolicyScope, at accesstypes.RecordScope, role accesstypes.Role) (RecordGroupRoleKey, error) {
	_, span := tracer.Start(ctx)
	defer span.End()

	if err := u.refuseRecordRole(at, role, group != "", noGroupNamed); err != nil {
		return RecordGroupRoleKey{}, err
	}
	if err := u.refuseRecordGroup(at, kept); err != nil {
		return RecordGroupRoleKey{}, err
	}

	return NewRecordGroupRoleKey(group, at, role), nil
}
