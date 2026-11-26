package access

import (
	"context"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

type fakeGroupMember struct {
	group accesstypes.Group
	user  accesstypes.User
}

type fakeGroupRole struct {
	group accesstypes.Group
	scope accesstypes.PolicyScope
	role  accesstypes.Role
}

// fakeGroups is the fake store's group tables, honoring the stores' contract:
// a name held to one group where it is kept, members and roles that need the
// group's row, and a delete that takes the group's members and roles. A member's row
// and a role's each hold the period the assignment counts in. The zero value
// is ready.
type fakeGroups struct {
	rows    map[accesstypes.Group]policy.GroupRow
	members map[fakeGroupMember]accesstypes.Period
	held    map[fakeGroupRole]accesstypes.Period
}

func (g *fakeGroups) ready() {
	if g.rows == nil {
		g.rows = make(map[accesstypes.Group]policy.GroupRow)
		g.members = make(map[fakeGroupMember]accesstypes.Period)
		g.held = make(map[fakeGroupRole]accesstypes.Period)
	}
}

// groupRecords adds the groups, their members and the roles they hold to the
// records of a policy read. The caller holds the lock.
func (f *fakeStore) groupRecords(records *policy.Records) {
	f.groups.ready()
	for _, row := range f.groups.rows {
		records.Groups = append(records.Groups, row.Group)
	}
	for m, period := range f.groups.members {
		records.GroupMembers = append(records.GroupMembers, policy.GroupMember{Group: m.group, User: m.user, Period: period})
	}
	for r, period := range f.groups.held {
		records.Memberships = append(records.Memberships, policy.Membership{
			Scope:  r.scope,
			Member: policy.Subject{Kind: policy.SubjectGroup, Name: string(r.group)},
			Role:   r.role,
			Period: period,
		})
	}
}

// locked runs fn under the store's lock with the group tables ready, or
// answers the scripted failure.
func (f *fakeStore) locked(fn func() error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	f.groups.ready()

	return fn()
}

func (f *fakeStore) InsertGroup(_ context.Context, group *policy.GroupRow) (bool, error) {
	var taken bool
	err := f.locked(func() error {
		if _, _, _, err := policy.GroupColumns(group.Scope); err != nil {
			return errors.Wrap(err, "policy.GroupColumns()")
		}
		if _, ok := f.groups.rows[group.ID]; ok {
			taken = true

			return nil
		}
		for _, row := range f.groups.rows {
			if row.Scope == group.Scope && row.Name == group.Name {
				taken = true

				return nil
			}
		}
		f.groups.rows[group.ID] = *group

		return nil
	})

	return taken, err
}

func (f *fakeStore) SetGroupDescription(_ context.Context, group accesstypes.Group, description string) (bool, error) {
	var found bool
	err := f.locked(func() error {
		row, ok := f.groups.rows[group]
		if !ok {
			return nil
		}
		row.Description = description
		f.groups.rows[group] = row
		found = true

		return nil
	})

	return found, err
}

func (f *fakeStore) DeleteGroup(_ context.Context, group accesstypes.Group) (bool, error) {
	var deleted bool
	err := f.locked(func() error {
		// The members and the roles go with the group, whatever their periods.
		for m := range f.groups.members {
			if m.group == group {
				delete(f.groups.members, m)
			}
		}
		for r := range f.groups.held {
			if r.group == group {
				delete(f.groups.held, r)
			}
		}
		f.deleteGroupRecords(group)
		_, deleted = f.groups.rows[group]
		delete(f.groups.rows, group)

		return nil
	})

	return deleted, err
}

func (f *fakeStore) Group(_ context.Context, group accesstypes.Group) (*policy.GroupRow, bool, error) {
	var row *policy.GroupRow
	err := f.locked(func() error {
		if found, ok := f.groups.rows[group]; ok {
			row = &found
		}

		return nil
	})

	return row, row != nil, err
}

func (f *fakeStore) ListGroups(_ context.Context, scope accesstypes.PolicyScope) ([]policy.GroupRow, error) {
	groups := make([]policy.GroupRow, 0)
	err := f.locked(func() error {
		for _, row := range f.groups.rows {
			if row.Scope == scope {
				groups = append(groups, row)
			}
		}
		slices.SortFunc(groups, func(a, b policy.GroupRow) int { return strings.Compare(a.Name, b.Name) })

		return nil
	})

	return groups, err
}

func (f *fakeStore) InsertGroupMember(_ context.Context, group accesstypes.Group, user accesstypes.User, period accesstypes.Period) error {
	return f.locked(func() error {
		if _, ok := f.groups.rows[group]; !ok {
			return errors.Wrapf(policy.ErrNoGroup, "group %s", group)
		}
		f.groups.members[fakeGroupMember{group, user}] = period
		f.writes++

		return nil
	})
}

func (f *fakeStore) DeleteGroupMember(_ context.Context, group accesstypes.Group, user accesstypes.User) error {
	return f.locked(func() error {
		delete(f.groups.members, fakeGroupMember{group, user})

		return nil
	})
}

func (f *fakeStore) ListGroupMembers(_ context.Context, group accesstypes.Group) ([]accesstypes.User, error) {
	users := make([]accesstypes.User, 0)
	err := f.locked(func() error {
		for m, period := range f.groups.members {
			if m.group == group && period.Contains(f.now()) {
				users = append(users, m.user)
			}
		}
		slices.Sort(users)

		return nil
	})

	return users, err
}

func (f *fakeStore) ListUserGroups(_ context.Context, user accesstypes.User) ([]policy.GroupRow, error) {
	groups := make([]policy.GroupRow, 0)
	err := f.locked(func() error {
		for m, period := range f.groups.members {
			if m.user == user && period.Contains(f.now()) {
				groups = append(groups, f.groups.rows[m.group])
			}
		}
		slices.SortFunc(groups, func(a, b policy.GroupRow) int { return strings.Compare(string(a.ID), string(b.ID)) })

		return nil
	})

	return groups, err
}

func (f *fakeStore) InsertGroupRole(_ context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	return f.locked(func() error {
		if _, ok := f.groups.rows[group]; !ok {
			return errors.Wrapf(policy.ErrNoGroup, "group %s", group)
		}
		f.groups.held[fakeGroupRole{group, scope, role}] = period
		f.writes++

		return nil
	})
}

func (f *fakeStore) InsertCustomGroupRole(_ context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	return f.locked(func() error {
		if !f.customRoleHeld(scope, role) {
			return errors.Wrapf(policy.ErrNoRole, "role %q in scope %s", role, scope)
		}
		if _, ok := f.groups.rows[group]; !ok {
			return errors.Wrapf(policy.ErrNoGroup, "group %s", group)
		}
		f.groups.held[fakeGroupRole{group, scope, role}] = period
		f.writes++

		return nil
	})
}

func (f *fakeStore) DeleteGroupRole(_ context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	return f.locked(func() error {
		delete(f.groups.held, fakeGroupRole{group, scope, role})

		return nil
	})
}

func (f *fakeStore) ListGroupRoles(_ context.Context, group accesstypes.Group) ([]policy.Membership, error) {
	roles := make([]policy.Membership, 0)
	err := f.locked(func() error {
		for r, period := range f.groups.held {
			if r.group == group && period.Contains(f.now()) {
				roles = append(roles, policy.Membership{Scope: r.scope, Member: policy.Subject{Kind: policy.SubjectGroup, Name: string(group)}, Role: r.role, Period: period})
			}
		}
		slices.SortFunc(roles, func(a, b policy.Membership) int {
			if c := policy.CompareScopes(a.Scope, b.Scope); c != 0 {
				return c
			}

			return strings.Compare(string(a.Role), string(b.Role))
		})

		return nil
	})

	return roles, err
}

func (f *fakeStore) ListRoleGroups(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]policy.GroupRow, error) {
	groups := make([]policy.GroupRow, 0)
	err := f.locked(func() error {
		for r, held := range f.groups.held {
			if r.scope == scope && r.role == role && held.Contains(f.now()) {
				groups = append(groups, f.groups.rows[r.group])
			}
		}
		slices.SortFunc(groups, func(a, b policy.GroupRow) int {
			if c := strings.Compare(a.Name, b.Name); c != 0 {
				return c
			}

			return strings.Compare(string(a.ID), string(b.ID))
		})

		return nil
	})

	return groups, err
}

func (f *fakeStore) ListUserGroupRoles(_ context.Context, user accesstypes.User) ([]policy.GroupRole, error) {
	roles := make([]policy.GroupRole, 0)
	err := f.locked(func() error {
		for m, in := range f.groups.members {
			if m.user != user || !in.Contains(f.now()) {
				continue
			}
			for r, held := range f.groups.held {
				if r.group == m.group && held.Contains(f.now()) {
					roles = append(roles, policy.GroupRole{Group: f.groups.rows[m.group].Group, Scope: r.scope, Role: r.role})
				}
			}
		}
		slices.SortFunc(roles, func(a, b policy.GroupRole) int {
			if c := policy.CompareScopes(a.Scope, b.Scope); c != 0 {
				return c
			}
			if c := strings.Compare(string(a.Role), string(b.Role)); c != 0 {
				return c
			}

			return strings.Compare(string(a.Group.ID), string(b.Group.ID))
		})

		return nil
	})

	return roles, err
}
