// groups.go is the management of groups of people: a group holds roles the way a person
// does, and a person in a group holds the group's roles wherever the group holds them,
// until the person leaves the group or the group loses the role. The manager's own
// methods each commit by themselves; an application that writes a group's rows in a
// transaction of its own (the generated management of an auth does) takes from here what
// stays the store's: how a row is keyed, and the rules a group and a group's role are
// held to (NewGroup, UserManager.GroupRole).

package access

import (
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/tracer"
)

// maxGroupName is the longest name a group takes, the width of the stores' column.
const maxGroupName = 128

// GroupInfo is one group of people as UserManager lists it: the id the store names it
// by, where it is kept (the global partition or one tenant domain), and the name and the
// description people read it by.
type GroupInfo struct {
	ID          accesstypes.Group
	Scope       accesstypes.PolicyScope
	Name        string
	Description string
}

// RoleHolder is one person who holds a role in a scope now, as UserManager.RoleHolders
// lists them, with how they hold it: by a membership of their own held there (Own),
// through the groups they are in that hold the role there (Groups), or both. Removing the
// person's own membership leaves what they hold through a group, which goes when they
// leave the group or the group loses the role.
type RoleHolder struct {
	User   accesstypes.User
	Own    bool
	Groups []GroupInfo
}

// GroupRow is the row of a group to be created (<Prefix><Store>Groups) less its id,
// which whoever writes the row generates: where the group is kept, in the stores'
// encoding of a scope, and its name and description as the rules leave them.
type GroupRow struct {
	Kind        string
	Axis        string
	Domain      string
	Name        string
	Description string
}

// GroupMemberKey is the key of one row of the group members table
// (<Prefix><Store>GroupMembers): the group, by its id, and the person as the engine
// names them.
type GroupMemberKey struct {
	Group accesstypes.Group
	User  accesstypes.User
}

// GroupRoleKey is the key of one row of the group roles table
// (<Prefix><Store>GroupRoles), its columns in key order: the group, by its id, where the
// role is held, in the stores' encoding of the scope, and the role in the spelling the
// store holds it.
type GroupRoleKey struct {
	Group  accesstypes.Group
	Kind   string
	Axis   string
	Domain string
	Role   accesstypes.Role
}

// NewGroup holds a group to be created to its rules and answers its row, for a caller
// that writes it inside a transaction of its own instead of through
// UserManager.AddGroup. A group is kept for the whole application or in one tenant, never
// in every tenant; its name is one to 128 characters with no control character, kept as
// given less the space around it; and it carries a description, one or two plain
// sentences saying who is in it. That the name is free where the group is kept is the
// store's to hold: the table's unique index refuses a second group of the name there.
func NewGroup(scope accesstypes.PolicyScope, name, description string) (GroupRow, error) {
	if scope.IsEveryDomain() {
		return GroupRow{}, httpio.NewBadRequestMessage("A group is kept for the whole application or in one tenant, never in every tenant. Name the tenant, or none.")
	}
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return GroupRow{}, httpio.NewBadRequestMessage("The group has no name. Give it the name people will know it by.")
	case utf8.RuneCountInString(name) > maxGroupName:
		return GroupRow{}, httpio.NewBadRequestMessagef("The group's name is longer than %d characters. Shorten it.", maxGroupName)
	case strings.ContainsFunc(name, unicode.IsControl):
		return GroupRow{}, httpio.NewBadRequestMessage("The group's name holds a character that cannot be shown. Type the name again.")
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return GroupRow{}, httpio.NewBadRequestMessage("The group has no description. Say who is in it in one or two plain sentences.")
	}
	kind, axis, domain := policy.ScopeColumns(scope)

	return GroupRow{Kind: kind, Axis: axis, Domain: domain, Name: name, Description: description}, nil
}

// GroupScope answers where a group is kept from the columns of its row, as a caller that
// read the row in a transaction of its own holds them. Columns no store writes for a
// group are refused.
func GroupScope(kind, axis, domain string) (accesstypes.PolicyScope, error) {
	scope, err := policy.GroupFromColumns(kind, axis, domain)
	if err != nil {
		return accesstypes.PolicyScope{}, errors.Wrap(err, "policy.GroupFromColumns()")
	}

	return scope, nil
}

// NewGroupRoleKey spells the role a group holds in scope as the store's row key. It
// checks nothing: it is the form for reading a row or deleting one. A row to be written
// comes from UserManager.GroupRole, which holds it to the rule first.
func NewGroupRoleKey(group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role) GroupRoleKey {
	kind, axis, domain := policy.ScopeColumns(scope)

	return GroupRoleKey{Group: group, Kind: kind, Axis: axis, Domain: domain, Role: role}
}

// Scope answers where the key's role is held.
func (k *GroupRoleKey) Scope() (accesstypes.PolicyScope, error) {
	scope, err := policy.ScopeFromColumns(k.Kind, k.Axis, k.Domain)
	if err != nil {
		return accesstypes.PolicyScope{}, errors.Wrap(err, "policy.ScopeFromColumns()")
	}

	return scope, nil
}

// refuseGroupRole refuses a role a group kept where kept says may not hold in scope
// (groupRoleRefusal), in words a person can act on.
func refuseGroupRole(kept, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	if role == accesstypes.SuperAdministrator {
		return httpio.NewBadRequestMessagef("The %s role is given to a person, never to a group. Only a super administrator makes another, one person at a time.", roleInWords(role))
	}
	if groupRoleRefusal(kept, scope, role) != "" {
		return httpio.NewBadRequestMessage("A group kept in one tenant holds roles in that tenant alone. Give the role there, or give it to a group kept for the whole application.")
	}

	return nil
}

// GroupMayHold reports whether a group kept where kept says may hold role in scope: the
// rule every write of a group's role holds its row to (groupRoleRefusal). A list of the
// roles to offer a group asks it, so that the list and the write follow one statement
// of the rule and a role is offered exactly where the write would take it.
func GroupMayHold(kept, scope accesstypes.PolicyScope, role accesstypes.Role) bool {
	return groupRoleRefusal(kept, scope, role) == ""
}

// GroupRole answers the key of the row that says group holds role in scope, for a caller
// that writes it inside a transaction of its own instead of through AddGroupRoles. kept
// is where the group is kept, which the caller read with the group's row in that
// transaction. The row is held to the rule AddGroupRoles holds its own to: the role is
// one a group may hold there (never the Super Administrator role; for a group kept in one
// tenant, that tenant alone), and it exists in scope under exactly that spelling, read
// through held as UserManager.Membership reads it. The caller commits the row and then
// tells the engine with Controller.PolicyChanged.
func (u *userManager) GroupRole(ctx context.Context, group accesstypes.Group, kept, scope accesstypes.PolicyScope, role accesstypes.Role, held RoleReader) (GroupRoleKey, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if held == nil {
		return GroupRoleKey{}, errors.New("access.UserManager.GroupRole(): the reader of the roles table is nil; pass one over the transaction the group's role is written in")
	}
	if group == "" {
		return GroupRoleKey{}, httpio.NewBadRequestMessage("No group is named. Name the group the role is for.")
	}
	if err := refuseGroupRole(kept, scope, role); err != nil {
		return GroupRoleKey{}, err
	}
	found, err := u.roleExistsBy(ctx, scope, role, func(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
		kind, axis, domain := policy.ScopeColumns(scope)
		exists, err := held(ctx, RoleKey{Kind: kind, Axis: axis, Domain: domain, Role: role})
		if err != nil {
			return false, errors.Wrapf(err, "access.RoleReader(): role %q in scope %q", role, scope)
		}

		return exists, nil
	})
	if err != nil {
		return GroupRoleKey{}, err
	}
	if !found {
		return GroupRoleKey{}, roleNotFound(role)
	}

	return NewGroupRoleKey(group, scope, role), nil
}

// AddGroup creates a group of people kept in scope and answers the id the store names it
// by. The group is held to NewGroup's rules, and a name another group kept in the same
// place holds is refused.
func (u *userManager) AddGroup(ctx context.Context, scope accesstypes.PolicyScope, name, description string) (accesstypes.Group, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	row, err := NewGroup(scope, name, description)
	if err != nil {
		return "", err
	}
	id, err := impulse.NewUUID()
	if err != nil {
		return "", errors.Wrap(err, "impulse.NewUUID()")
	}
	group := &policy.GroupRow{Group: policy.Group{ID: accesstypes.Group(id.String()), Scope: scope}, Name: row.Name, Description: row.Description}
	taken, err := u.store.addGroup(ctx, group)
	if err != nil {
		return "", err
	}
	if taken {
		return "", httpio.NewConflictMessagef("A group named %q already exists there. Choose another name.", row.Name)
	}

	return group.ID, nil
}

// DescribeGroup replaces a group's description.
func (u *userManager) DescribeGroup(ctx context.Context, group accesstypes.Group, description string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	description = strings.TrimSpace(description)
	if description == "" {
		return httpio.NewBadRequestMessage("The group has no description. Say who is in it in one or two plain sentences.")
	}
	found, err := u.store.describeGroup(ctx, group, description)
	if err != nil {
		return err
	}
	if !found {
		return groupNotFound(group)
	}

	return nil
}

// DeleteGroup removes a group that has no member and holds no role. A group that still
// has either is refused until it is emptied, so nobody loses a role by a group's
// deletion: the people are removed and the roles taken first, each by its own act. A
// member or a role whose period has ended gives nobody anything and goes with the group;
// one whose period has not begun stands in the way like one that counts now.
func (u *userManager) DeleteGroup(ctx context.Context, group accesstypes.Group) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	members, err := u.store.groupUsers(ctx, group)
	if err != nil {
		return err
	}
	roles, err := u.store.groupRoles(ctx, group)
	if err != nil {
		return err
	}
	if len(members) > 0 || len(roles) > 0 {
		return httpio.NewConflictMessageWithError(
			errors.Newf("the group still has %d member(s) and holds %d role(s): remove its members and take its roles away, then delete it", len(members), len(roles)),
			"The group still has a member or holds a role. Remove its members and take its roles away, then delete it.",
		)
	}
	deleted, err := u.store.deleteGroup(ctx, group)
	if errors.Is(err, policy.ErrStillAssigned) {
		// The listings read what counts now: what stands in the way is a member
		// or a role whose period has not begun, or one given since they were read.
		return httpio.NewConflictMessage("The group has a member or holds a role, now or for a period that has not begun yet. Remove its members and take its roles away, then delete it.")
	}
	if err != nil {
		return err
	}
	if !deleted {
		return groupNotFound(group)
	}

	return nil
}

// Groups returns the groups kept in scope, sorted by name. The groups a tenant sees
// are its own: a group kept for the whole application is listed in the global scope
// alone, whichever tenants it holds roles in.
func (u *userManager) Groups(ctx context.Context, scope accesstypes.PolicyScope) ([]GroupInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	rows, err := u.store.groups(ctx, scope)
	if err != nil {
		return nil, err
	}

	return groupInfos(rows), nil
}

// UserGroups returns the groups user is in, wherever each is kept, sorted by id.
func (u *userManager) UserGroups(ctx context.Context, user accesstypes.User) ([]GroupInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	rows, err := u.store.userGroups(ctx, user)
	if err != nil {
		return nil, err
	}

	return groupInfos(rows), nil
}

func groupInfos(rows []policy.GroupRow) []GroupInfo {
	groups := make([]GroupInfo, 0, len(rows))
	for _, row := range rows {
		groups = append(groups, GroupInfo{ID: row.ID, Scope: row.Scope, Name: row.Name, Description: row.Description})
	}

	return groups
}

// AddGroupUsers puts users in group, each with no period. Errors if the group does not
// exist or a user is empty. A person who is in the group for a period is in it until
// they are taken out.
func (u *userManager) AddGroupUsers(ctx context.Context, group accesstypes.Group, users ...accesstypes.User) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	return u.assignGroupUsers(ctx, group, accesstypes.Period{}, users...)
}

// assignGroupUsers puts each user in group for the period.
func (u *userManager) assignGroupUsers(ctx context.Context, group accesstypes.Group, period accesstypes.Period, users ...accesstypes.User) error {
	if _, err := u.requireGroup(ctx, group); err != nil {
		return err
	}
	if slices.Contains(users, "") {
		return httpio.NewBadRequestMessage("No person is named. Name the person to put in the group.")
	}
	if err := period.Validate(); err != nil {
		return httpio.NewBadRequestMessageWithError(err, PeriodNeverCountsMessage)
	}
	for _, user := range users {
		if err := u.store.addGroupUser(ctx, group, user, period); err != nil {
			if errors.Is(err, policy.ErrNoGroup) {
				// The group was deleted since requireGroup found it.
				return groupNotFound(group)
			}

			return err
		}
	}

	return nil
}

// DeleteGroupUsers takes users out of group. A person who is not in it is no error.
func (u *userManager) DeleteGroupUsers(ctx context.Context, group accesstypes.Group, users ...accesstypes.User) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	for _, user := range users {
		if err := u.store.deleteGroupUser(ctx, group, user); err != nil {
			return err
		}
	}

	return nil
}

// GroupUsers returns the people in group now, sorted.
func (u *userManager) GroupUsers(ctx context.Context, group accesstypes.Group) ([]accesstypes.User, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return u.store.groupUsers(ctx, group)
}

// AddGroupRoles gives group the roles, each held in scope. Errors if the group does not
// exist, if a role is one the group may not hold there (the Super Administrator role
// anywhere; for a group kept in one tenant, any role outside that tenant), or if a role
// doesn't exist in scope, as AddRoleUsers defines it. Each role is given with no period:
// one the group holds there for a period is the group's until it is taken away.
func (u *userManager) AddGroupRoles(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, roles ...accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	return u.assignGroupRoles(ctx, group, scope, accesstypes.Period{}, roles...)
}

// assignGroupRoles gives group each role in scope for the period, once every role is one
// the group may hold there.
func (u *userManager) assignGroupRoles(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, period accesstypes.Period, roles ...accesstypes.Role) error {
	row, err := u.requireGroup(ctx, group)
	if err != nil {
		return err
	}
	if err := period.Validate(); err != nil {
		return httpio.NewBadRequestMessageWithError(err, PeriodNeverCountsMessage)
	}
	for _, role := range roles {
		if err := refuseGroupRole(row.Scope, scope, role); err != nil {
			return err
		}
		found, err := u.roleExists(ctx, scope, role)
		if err != nil {
			return err
		}
		if !found {
			return roleNotFound(role)
		}
	}
	for _, role := range roles {
		if err := u.giveGroupRole(ctx, group, scope, role, period); err != nil {
			return err
		}
	}

	return nil
}

// giveGroupRole gives group the role in scope for the period: one of the release's
// default roles by its name, and a custom role only while its row is there. A custom
// role deleted since the call found it is answered as a role that does not exist, a
// group deleted since then as a group that does not exist, and nothing is given.
func (u *userManager) giveGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	var err error
	if u.defaults.exists(scope, role) {
		err = u.store.addGroupRole(ctx, group, scope, role, period)
	} else {
		err = u.store.addCustomGroupRole(ctx, group, scope, role, period)
	}
	switch {
	case errors.Is(err, policy.ErrNoRole):
		return roleNotFound(role)
	case errors.Is(err, policy.ErrNoGroup):
		return groupNotFound(group)
	}

	return err
}

// DeleteGroupRoles takes the roles group holds in scope away from it. A role the group
// does not hold there is no error, and the role need not exist any more.
func (u *userManager) DeleteGroupRoles(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, roles ...accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	for _, role := range roles {
		if err := u.store.deleteGroupRole(ctx, group, scope, role); err != nil {
			return err
		}
	}

	return nil
}

// GroupRoles returns the roles group holds as stored, keyed by where each is held.
func (u *userManager) GroupRoles(ctx context.Context, group accesstypes.Group) (accesstypes.RoleCollection, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	held, err := u.store.groupRoles(ctx, group)
	if err != nil {
		return nil, err
	}
	roles := make(accesstypes.RoleCollection)
	for i := range held {
		roles[held[i].Scope] = append(roles[held[i].Scope], held[i].Role)
	}

	return roles, nil
}

// requireGroup reads the group's row, or refuses a group that does not exist.
func (u *userManager) requireGroup(ctx context.Context, group accesstypes.Group) (*policy.GroupRow, error) {
	row, found, err := u.store.group(ctx, group)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, groupNotFound(group)
	}

	return row, nil
}

// heldThroughGroups adds to held the roles user holds through the groups they are in:
// each role of each such group that the group gives its members (groupRoleRefusal), held
// where the group holds it and listed once. scopes limits the answer to those places;
// none asks for every place.
func (u *userManager) heldThroughGroups(ctx context.Context, user accesstypes.User, held accesstypes.RoleCollection, scopes []accesstypes.PolicyScope) error {
	roles, err := u.store.userGroupRoles(ctx, user)
	if err != nil {
		return err
	}
	for _, r := range roles {
		if len(scopes) > 0 && !slices.Contains(scopes, r.Scope) {
			continue
		}
		if groupRoleRefusal(r.Group.Scope, r.Scope, r.Role) != "" || slices.Contains(held[r.Scope], r.Role) {
			continue
		}
		held[r.Scope] = append(held[r.Scope], r.Role)
	}
	for scope := range held {
		slices.Sort(held[scope])
	}

	return nil
}

func (m *storeManager) addGroup(ctx context.Context, group *policy.GroupRow) (bool, error) {
	taken, err := m.store.InsertGroup(ctx, group)
	if err != nil {
		return false, errors.Wrapf(err, "access.Store.InsertGroup(): group %q", group.Name)
	}

	return taken, nil
}

func (m *storeManager) describeGroup(ctx context.Context, group accesstypes.Group, description string) (bool, error) {
	found, err := m.store.SetGroupDescription(ctx, group, description)
	if err != nil {
		return false, errors.Wrapf(err, "access.Store.SetGroupDescription(): group %s", group)
	}

	return found, nil
}

func (m *storeManager) deleteGroup(ctx context.Context, group accesstypes.Group) (bool, error) {
	deleted, err := m.store.DeleteGroup(ctx, group)
	if err != nil {
		return false, errors.Wrapf(err, "access.Store.DeleteGroup(): group %s", group)
	}
	if deleted {
		m.notifyPolicyChange()
	}

	return deleted, nil
}

func (m *storeManager) group(ctx context.Context, group accesstypes.Group) (*policy.GroupRow, bool, error) {
	row, found, err := m.store.Group(ctx, group)
	if err != nil {
		return nil, false, errors.Wrapf(err, "access.Store.Group(): group %s", group)
	}

	return row, found, nil
}

func (m *storeManager) groups(ctx context.Context, scope accesstypes.PolicyScope) ([]policy.GroupRow, error) {
	rows, err := m.store.ListGroups(ctx, scope)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListGroups(): scope %q", scope)
	}

	return rows, nil
}

func (m *storeManager) userGroups(ctx context.Context, user accesstypes.User) ([]policy.GroupRow, error) {
	rows, err := m.store.ListUserGroups(ctx, user)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListUserGroups(): user %q", user)
	}

	return rows, nil
}

func (m *storeManager) addGroupUser(ctx context.Context, group accesstypes.Group, user accesstypes.User, period accesstypes.Period) error {
	if err := m.store.InsertGroupMember(ctx, group, user, period); err != nil {
		return errors.Wrapf(err, "access.Store.InsertGroupMember(): %q to group %s", user, group)
	}
	m.notifyPolicyChange()

	return nil
}

func (m *storeManager) deleteGroupUser(ctx context.Context, group accesstypes.Group, user accesstypes.User) error {
	if err := m.store.DeleteGroupMember(ctx, group, user); err != nil {
		return errors.Wrapf(err, "access.Store.DeleteGroupMember(): %q from group %s", user, group)
	}
	m.notifyPolicyChange()

	return nil
}

func (m *storeManager) groupUsers(ctx context.Context, group accesstypes.Group) ([]accesstypes.User, error) {
	users, err := m.store.ListGroupMembers(ctx, group)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListGroupMembers(): group %s", group)
	}

	return users, nil
}

func (m *storeManager) addGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	if err := m.store.InsertGroupRole(ctx, group, scope, role, period); err != nil {
		return errors.Wrapf(err, "access.Store.InsertGroupRole(): role %q to group %s", role, group)
	}
	m.notifyPolicyChange()

	return nil
}

func (m *storeManager) addCustomGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	if err := m.store.InsertCustomGroupRole(ctx, group, scope, role, period); err != nil {
		return errors.Wrapf(err, "access.Store.InsertCustomGroupRole(): role %q to group %s", role, group)
	}
	m.notifyPolicyChange()

	return nil
}

func (m *storeManager) deleteGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	if err := m.store.DeleteGroupRole(ctx, group, scope, role); err != nil {
		return errors.Wrapf(err, "access.Store.DeleteGroupRole(): role %q from group %s", role, group)
	}
	m.notifyPolicyChange()

	return nil
}

func (m *storeManager) groupRoles(ctx context.Context, group accesstypes.Group) ([]policy.Membership, error) {
	roles, err := m.store.ListGroupRoles(ctx, group)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListGroupRoles(): group %s", group)
	}

	return roles, nil
}

func (m *storeManager) userGroupRoles(ctx context.Context, user accesstypes.User) ([]policy.GroupRole, error) {
	roles, err := m.store.ListUserGroupRoles(ctx, user)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListUserGroupRoles(): user %q", user)
	}

	return roles, nil
}

func (m *storeManager) roleGroups(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]policy.GroupRow, error) {
	groups, err := m.store.ListRoleGroups(ctx, scope, role)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListRoleGroups(): role %q", role)
	}

	return groups, nil
}
