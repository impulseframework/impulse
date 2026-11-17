package spannerstore

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
)

// The groups of people: three tables beside the memberships, named
// {Prefix}{Store}{Groups|GroupMembers|GroupRoles}. A group is a row under the
// id its creator generated, kept in the global partition or in one domain
// (Kind, Axis, DomainId, as a membership spells where it is held, less the
// every-domain kind); its name is held to one group where it is kept by a
// unique index. A member is a (group, user) row and a group's role a (group,
// scope, role) row; each names its group by a foreign key, so a member or a
// role of a group that is not there cannot be written and a group with either
// cannot be deleted.

// Column and query-parameter names of the group tables.
const (
	colID      = "Id"
	colName    = "Name"
	colGroupID = "GroupId"

	paramGroup       = "group"
	paramDescription = "description"
)

// groupStatements are the group tables' statements, built once with the
// store's table names.
type groupStatements struct {
	setDescription string
	deleteGroup    string
	hasMembers     string
	hasRoles       string
	deleteEnded    []string
	group          string
	listGroups     string
	listMembers    string
	listUserGroups string
	listRoles      string
	listUserRoles  string
	readGroups     string
	readMembers    string
	readRoles      string
}

func newGroupStatements(n *tableNames) groupStatements {
	return groupStatements{
		setDescription: fmt.Sprintf("UPDATE %s SET Description = @description, UpdatedAt = PENDING_COMMIT_TIMESTAMP() WHERE Id = @group", n.groups),
		deleteGroup:    fmt.Sprintf("DELETE FROM %s WHERE Id = @group", n.groups),
		hasMembers:     fmt.Sprintf("SELECT 1 FROM %s WHERE GroupId = @group AND %s LIMIT 1", n.groupMembers, standing("")),
		hasRoles:       fmt.Sprintf("SELECT 1 FROM %s WHERE GroupId = @group AND %s LIMIT 1", n.groupRoles, standing("")),
		deleteEnded: []string{
			fmt.Sprintf("DELETE FROM %s WHERE GroupId = @group AND %s", n.groupMembers, ended),
			fmt.Sprintf("DELETE FROM %s WHERE GroupId = @group AND %s", n.groupRoles, ended),
		},
		group: fmt.Sprintf("SELECT Id, Kind, Axis, DomainId, Name, Description FROM %s WHERE Id = @group", n.groups),
		listGroups: fmt.Sprintf("SELECT Id, Kind, Axis, DomainId, Name, Description FROM %s@{FORCE_INDEX=%s} WHERE Kind = @kind AND Axis = @axis AND DomainId = @domain ORDER BY Name",
			n.groups, n.groupsByName),
		listMembers: fmt.Sprintf("SELECT UserId FROM %s WHERE GroupId = @group AND %s ORDER BY UserId", n.groupMembers, counting("")),
		listUserGroups: fmt.Sprintf("SELECT g.Id, g.Kind, g.Axis, g.DomainId, g.Name, g.Description FROM %s@{FORCE_INDEX=%s} m JOIN %s g ON g.Id = m.GroupId WHERE m.UserId = @user AND %s ORDER BY g.Id",
			n.groupMembers, n.groupMembersIndex, n.groups, counting("m.")),
		listRoles: fmt.Sprintf("SELECT Kind, Axis, DomainId, Role, StartsAt, EndsAt FROM %s WHERE GroupId = @group AND %s ORDER BY Kind, Axis, DomainId, Role", n.groupRoles, counting("")),
		listUserRoles: fmt.Sprintf(`SELECT g.Id, g.Kind, g.Axis, g.DomainId, r.Kind, r.Axis, r.DomainId, r.Role
FROM %s@{FORCE_INDEX=%s} m JOIN %s g ON g.Id = m.GroupId JOIN %s r ON r.GroupId = m.GroupId
WHERE m.UserId = @user AND %s AND %s ORDER BY r.Kind, r.Axis, r.DomainId, r.Role, g.Id`, n.groupMembers, n.groupMembersIndex, n.groups, n.groupRoles, counting("m."), counting("r.")),
		readGroups:  fmt.Sprintf("SELECT Id, Kind, Axis, DomainId FROM %s", n.groups),
		readMembers: fmt.Sprintf("SELECT GroupId, UserId, StartsAt, EndsAt FROM %s", n.groupMembers),
		readRoles:   fmt.Sprintf("SELECT GroupId, Kind, Axis, DomainId, Role, StartsAt, EndsAt FROM %s", n.groupRoles),
	}
}

// groupDDL returns the group tables' schema statements. A group's Kind is
// checked against the two places a group is kept, and a group's role's against
// the three a role is held in.
func (s *Store) groupDDL() []string {
	n := s.names

	return []string{
		fmt.Sprintf(`CREATE TABLE %s (
  Id STRING(36) NOT NULL,
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Name STRING(128) NOT NULL,
  Description STRING(MAX) NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT %sKind CHECK (Kind IN ('%s', '%s')),
) PRIMARY KEY (Id)`, n.groups, n.groups, policy.KindGlobal, policy.KindDomain),
		fmt.Sprintf(`CREATE UNIQUE INDEX %s ON %s (Kind, Axis, DomainId, Name)`, n.groupsByName, n.groups),
		fmt.Sprintf(`CREATE TABLE %s (
  GroupId STRING(36) NOT NULL,
  UserId STRING(320) NOT NULL,
  StartsAt TIMESTAMP,
  EndsAt TIMESTAMP,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT %sGroup FOREIGN KEY (GroupId) REFERENCES %s (Id),
) PRIMARY KEY (GroupId, UserId)`, n.groupMembers, n.groupMembers, n.groups),
		fmt.Sprintf(`CREATE INDEX %s ON %s (UserId, GroupId)`, n.groupMembersIndex, n.groupMembers),
		fmt.Sprintf(`CREATE TABLE %s (
  GroupId STRING(36) NOT NULL,
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  StartsAt TIMESTAMP,
  EndsAt TIMESTAMP,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT %sKind CHECK (Kind IN ('%s', '%s', '%s')),
  CONSTRAINT %sGroup FOREIGN KEY (GroupId) REFERENCES %s (Id),
) PRIMARY KEY (GroupId, Kind, Axis, DomainId, Role)`, n.groupRoles, n.groupRoles, policy.KindGlobal, policy.KindDomain, policy.KindEvery, n.groupRoles, n.groups),
		fmt.Sprintf(`CREATE INDEX %s ON %s (Kind, Axis, DomainId, Role)`, n.groupRolesIndex, n.groupRoles),
	}
}

// readGroups reads the groups, their members and the roles they hold into the
// records, inside the policy read's transaction.
func (s *Store) readGroups(ctx context.Context, txn *spanner.ReadOnlyTransaction, records *policy.Records) error {
	err := txn.Query(ctx, spanner.Statement{SQL: s.groups.readGroups}).Do(func(row *spanner.Row) error {
		var id, kind, axis, domain string
		if err := row.Columns(&id, &kind, &axis, &domain); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		scope, err := policy.GroupFromColumns(kind, axis, domain)
		if err != nil {
			return errors.Wrap(err, "policy.GroupFromColumns()")
		}
		records.Groups = append(records.Groups, policy.Group{ID: accesstypes.Group(id), Scope: scope})

		return nil
	})
	if err != nil {
		return errors.Wrap(err, "spanner.ReadOnlyTransaction.Query() groups")
	}

	err = txn.Query(ctx, spanner.Statement{SQL: s.groups.readMembers}).Do(func(row *spanner.Row) error {
		var group, user string
		var start, end spanner.NullTime
		if err := row.Columns(&group, &user, &start, &end); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		records.GroupMembers = append(records.GroupMembers, policy.GroupMember{Group: accesstypes.Group(group), User: accesstypes.User(user), Period: periodOf(start, end)})

		return nil
	})
	if err != nil {
		return errors.Wrap(err, "spanner.ReadOnlyTransaction.Query() group members")
	}

	err = txn.Query(ctx, spanner.Statement{SQL: s.groups.readRoles}).Do(func(row *spanner.Row) error {
		var group, kind, axis, domain, role string
		var start, end spanner.NullTime
		if err := row.Columns(&group, &kind, &axis, &domain, &role, &start, &end); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		records.Memberships = append(records.Memberships, policy.Membership{
			Scope:  scope,
			Member: policy.Subject{Kind: policy.SubjectGroup, Name: group},
			Role:   accesstypes.Role(role),
			Period: periodOf(start, end),
		})

		return nil
	})
	if err != nil {
		return errors.Wrap(err, "spanner.ReadOnlyTransaction.Query() group roles")
	}

	return nil
}

// InsertGroup creates the group's row. An id already taken, or a name another
// group kept in the same place holds, is reported as taken and writes nothing.
func (s *Store) InsertGroup(ctx context.Context, group *policy.GroupRow) (bool, error) {
	kind, axis, domain, err := policy.GroupColumns(group.Scope)
	if err != nil {
		return false, errors.Wrap(err, "policy.GroupColumns()")
	}
	m := spanner.Insert(s.names.groups,
		[]string{colID, colKind, colAxis, colDomain, colName, colDescription, colUpdatedAt},
		[]any{string(group.ID), kind, axis, domain, group.Name, group.Description, spanner.CommitTimestamp})
	if _, err := s.client.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		if spanner.ErrCode(err) == codes.AlreadyExists {
			return true, nil
		}

		return false, errors.Wrap(err, "spanner.Client.Apply() insert group")
	}

	return false, nil
}

// SetGroupDescription replaces the group's description and reports whether
// the group was there.
func (s *Store) SetGroupDescription(ctx context.Context, group accesstypes.Group, description string) (bool, error) {
	var found bool
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		count, err := txn.Update(ctx, spanner.Statement{SQL: s.groups.setDescription, Params: map[string]any{paramGroup: string(group), paramDescription: description}})
		if err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.Update()")
		}
		found = count > 0

		return nil
	})
	if err != nil {
		return false, errors.Wrap(err, "spanner.Client.ReadWriteTransaction() describe group")
	}

	return found, nil
}

// DeleteGroup deletes the group's row and reports whether it was there. A
// group that still has a member or a role that has not ended is refused,
// checked in the delete's own transaction; the ended ones go with the group,
// and the foreign keys of the two tables stand behind the check.
func (s *Store) DeleteGroup(ctx context.Context, group accesstypes.Group) (bool, error) {
	params := map[string]any{paramGroup: string(group)}
	var deleted bool
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		for _, check := range []struct{ sql, what string }{{s.groups.hasMembers, "members"}, {s.groups.hasRoles, "roles"}} {
			held, err := queryHasRow(ctx, txn, spanner.Statement{SQL: check.sql, Params: params})
			if err != nil {
				return err
			}
			if held {
				return errors.Wrapf(policy.ErrStillAssigned, "group %s still has %s", group, check.what)
			}
		}
		for _, sql := range s.groups.deleteEnded {
			if _, err := txn.Update(ctx, spanner.Statement{SQL: sql, Params: params}); err != nil {
				return errors.Wrap(err, "spanner.ReadWriteTransaction.Update() ended rows")
			}
		}
		count, err := txn.Update(ctx, spanner.Statement{SQL: s.groups.deleteGroup, Params: params})
		if err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.Update()")
		}
		deleted = count > 0

		return nil
	})
	if err != nil {
		return false, errors.Wrap(err, "spanner.Client.ReadWriteTransaction() delete group")
	}

	return deleted, nil
}

// queryHasRow reports whether the statement yields a row.
func queryHasRow(ctx context.Context, txn *spanner.ReadWriteTransaction, stmt spanner.Statement) (bool, error) {
	iter := txn.Query(ctx, stmt)
	defer iter.Stop()
	if _, err := iter.Next(); err != nil {
		if errors.Is(err, iterator.Done) {
			return false, nil
		}

		return false, errors.Wrap(err, "spanner.RowIterator.Next()")
	}

	return true, nil
}

// Group returns the group kept under the id; found is false where none is.
func (s *Store) Group(ctx context.Context, group accesstypes.Group) (*policy.GroupRow, bool, error) {
	rows, err := s.queryGroups(ctx, spanner.Statement{SQL: s.groups.group, Params: map[string]any{paramGroup: string(group)}})
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}

	return &rows[0], true, nil
}

// ListGroups returns the groups kept in scope, sorted by name.
func (s *Store) ListGroups(ctx context.Context, scope accesstypes.PolicyScope) ([]policy.GroupRow, error) {
	kind, axis, domain := policy.ScopeColumns(scope)

	return s.queryGroups(ctx, spanner.Statement{SQL: s.groups.listGroups, Params: map[string]any{paramKind: kind, paramAxis: axis, paramDomain: domain}})
}

// ListUserGroups returns the groups the user is in, sorted by id.
func (s *Store) ListUserGroups(ctx context.Context, user accesstypes.User) ([]policy.GroupRow, error) {
	return s.queryGroups(ctx, spanner.Statement{SQL: s.groups.listUserGroups, Params: map[string]any{paramUser: string(user)}})
}

// queryGroups runs a statement that selects whole group rows.
func (s *Store) queryGroups(ctx context.Context, stmt spanner.Statement) ([]policy.GroupRow, error) {
	groups := make([]policy.GroupRow, 0)
	err := s.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var id, kind, axis, domain, name, description string
		if err := row.Columns(&id, &kind, &axis, &domain, &name, &description); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		scope, err := policy.GroupFromColumns(kind, axis, domain)
		if err != nil {
			return errors.Wrap(err, "policy.GroupFromColumns()")
		}
		groups = append(groups, policy.GroupRow{Group: policy.Group{ID: accesstypes.Group(id), Scope: scope}, Name: name, Description: description})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.Client.Single().Query() groups")
	}

	return groups, nil
}

// InsertGroupMember gives one person's place in a group its period: the person
// is added where they are not in the group, and their place given the new
// period where they are. The group's row must exist.
func (s *Store) InsertGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User, period accesstypes.Period) error {
	return s.assign(ctx, s.names.groupMembers,
		[]string{colGroupID, colUser},
		spanner.Key{string(group), string(user)},
		period, "spanner.Client.ReadWriteTransaction() assign group member")
}

// DeleteGroupMember removes one person from a group; removing one who is not
// there is a no-op.
func (s *Store) DeleteGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User) error {
	m := spanner.Delete(s.names.groupMembers, spanner.Key{string(group), string(user)})
	if _, err := s.client.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		return errors.Wrap(err, "spanner.Client.Apply() delete group member")
	}

	return nil
}

// ListGroupMembers returns the people in the group now, sorted.
func (s *Store) ListGroupMembers(ctx context.Context, group accesstypes.Group) ([]accesstypes.User, error) {
	values, err := s.queryStrings(ctx, spanner.Statement{SQL: s.groups.listMembers, Params: map[string]any{paramGroup: string(group)}})
	if err != nil {
		return nil, err
	}
	users := make([]accesstypes.User, 0, len(values))
	for _, v := range values {
		users = append(users, accesstypes.User(v))
	}

	return users, nil
}

// InsertGroupRole gives one role a group holds in scope its period: the role is
// added where the group does not hold it there, and given the new period where
// it does. The group's row must exist; the role is named by name alone.
func (s *Store) InsertGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	kind, axis, domain := policy.ScopeColumns(scope)

	return s.assign(ctx, s.names.groupRoles,
		[]string{colGroupID, colKind, colAxis, colDomain, colRole},
		spanner.Key{string(group), kind, axis, domain, string(role)},
		period, "spanner.Client.ReadWriteTransaction() assign group role")
}

// DeleteGroupRole removes one role a group holds in scope; removing one it
// does not hold there is a no-op.
func (s *Store) DeleteGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	m := spanner.Delete(s.names.groupRoles, spanner.Key{string(group), kind, axis, domain, string(role)})
	if _, err := s.client.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		return errors.Wrap(err, "spanner.Client.Apply() delete group role")
	}

	return nil
}

// ListGroupRoles returns the roles the group holds now, sorted by scope then
// role.
func (s *Store) ListGroupRoles(ctx context.Context, group accesstypes.Group) ([]policy.Membership, error) {
	roles := make([]policy.Membership, 0)
	stmt := spanner.Statement{SQL: s.groups.listRoles, Params: map[string]any{paramGroup: string(group)}}
	err := s.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var kind, axis, domain, role string
		var start, end spanner.NullTime
		if err := row.Columns(&kind, &axis, &domain, &role, &start, &end); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		roles = append(roles, policy.Membership{
			Scope:  scope,
			Member: policy.Subject{Kind: policy.SubjectGroup, Name: string(group)},
			Role:   accesstypes.Role(role),
			Period: periodOf(start, end),
		})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.Client.Single().Query() group roles")
	}

	return roles, nil
}

// ListUserGroupRoles returns every role held now by a group the user is in
// now, each with its group, read through the members' index by user.
func (s *Store) ListUserGroupRoles(ctx context.Context, user accesstypes.User) ([]policy.GroupRole, error) {
	roles := make([]policy.GroupRole, 0)
	stmt := spanner.Statement{SQL: s.groups.listUserRoles, Params: map[string]any{paramUser: string(user)}}
	err := s.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var id, groupKind, groupAxis, groupDomain, kind, axis, domain, role string
		if err := row.Columns(&id, &groupKind, &groupAxis, &groupDomain, &kind, &axis, &domain, &role); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		kept, err := policy.GroupFromColumns(groupKind, groupAxis, groupDomain)
		if err != nil {
			return errors.Wrap(err, "policy.GroupFromColumns()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		roles = append(roles, policy.GroupRole{Group: policy.Group{ID: accesstypes.Group(id), Scope: kept}, Scope: scope, Role: accesstypes.Role(role)})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.Client.Single().Query() user group roles")
	}

	return roles, nil
}
