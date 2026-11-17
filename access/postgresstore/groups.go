package postgresstore

import (
	"context"
	"fmt"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The groups of people: three tables beside the memberships, named
// {Prefix}{Store}{Groups|GroupMembers|GroupRoles}. A group is a row under the
// id its creator generated, kept in the global partition or in one domain
// ("Kind", "Axis", "DomainId", as a membership spells where it is held, less
// the every-domain kind); its name is held to one group where it is kept by a
// unique index. A member is a (group, user) row and a group's role a (group,
// scope, role) row; each names its group by a foreign key, so a member or a
// role of a group that is not there cannot be written and a group with either
// cannot be deleted.

// groupStatements are the group tables' statements, built once with the
// store's table names.
type groupStatements struct {
	insertGroup    string
	setDescription string
	deleteGroup    string
	hasMembers     string
	hasRoles       string
	group          string
	listGroups     string
	insertMember   string
	deleteMember   string
	listMembers    string
	listUserGroups string
	insertRole     string
	deleteRole     string
	listRoles      string
	listUserRoles  string
	readGroups     string
	readMembers    string
	readRoles      string
}

func newGroupStatements(n *tableNames) groupStatements {
	return groupStatements{
		insertGroup:    fmt.Sprintf(`insert into %s ("Id", "Kind", "Axis", "DomainId", "Name", "Description") values ($1, $2, $3, $4, $5, $6)`, n.groups),
		setDescription: fmt.Sprintf(`update %s set "Description" = $2, "UpdatedAt" = now() where "Id" = $1`, n.groups),
		deleteGroup:    fmt.Sprintf(`delete from %s where "Id" = $1`, n.groups),
		hasMembers:     fmt.Sprintf(`select exists(select 1 from %s where "GroupId" = $1)`, n.groupMembers),
		hasRoles:       fmt.Sprintf(`select exists(select 1 from %s where "GroupId" = $1)`, n.groupRoles),
		group:          fmt.Sprintf(`select "Id", "Kind", "Axis", "DomainId", "Name", "Description" from %s where "Id" = $1`, n.groups),
		listGroups:     fmt.Sprintf(`select "Id", "Kind", "Axis", "DomainId", "Name", "Description" from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 order by "Name"`, n.groups),
		insertMember:   fmt.Sprintf(`insert into %s ("GroupId", "UserId") values ($1, $2) on conflict do nothing`, n.groupMembers),
		deleteMember:   fmt.Sprintf(`delete from %s where "GroupId" = $1 and "UserId" = $2`, n.groupMembers),
		listMembers:    fmt.Sprintf(`select "UserId" from %s where "GroupId" = $1 order by "UserId"`, n.groupMembers),
		listUserGroups: fmt.Sprintf(`select g."Id", g."Kind", g."Axis", g."DomainId", g."Name", g."Description" from %s m join %s g on g."Id" = m."GroupId" where m."UserId" = $1 order by g."Id"`,
			n.groupMembers, n.groups),
		insertRole: fmt.Sprintf(`insert into %s ("GroupId", "Kind", "Axis", "DomainId", "Role") values ($1, $2, $3, $4, $5) on conflict do nothing`, n.groupRoles),
		deleteRole: fmt.Sprintf(`delete from %s where "GroupId" = $1 and "Kind" = $2 and "Axis" = $3 and "DomainId" = $4 and "Role" = $5`, n.groupRoles),
		listRoles:  fmt.Sprintf(`select "Kind", "Axis", "DomainId", "Role" from %s where "GroupId" = $1 order by "Kind", "Axis", "DomainId", "Role"`, n.groupRoles),
		listUserRoles: fmt.Sprintf(`select g."Id", g."Kind", g."Axis", g."DomainId", r."Kind", r."Axis", r."DomainId", r."Role"
from %s m join %s g on g."Id" = m."GroupId" join %s r on r."GroupId" = m."GroupId"
where m."UserId" = $1 order by r."Kind", r."Axis", r."DomainId", r."Role", g."Id"`, n.groupMembers, n.groups, n.groupRoles),
		readGroups:  fmt.Sprintf(`select "Id", "Kind", "Axis", "DomainId" from %s`, n.groups),
		readMembers: fmt.Sprintf(`select "GroupId", "UserId" from %s`, n.groupMembers),
		readRoles:   fmt.Sprintf(`select "GroupId", "Kind", "Axis", "DomainId", "Role" from %s`, n.groupRoles),
	}
}

// groupDDL returns the group tables' schema statements. A group's Kind is
// checked against the two places a group is kept, and a group's role's against
// the three a role is held in.
func (s *Store) groupDDL() []string {
	n := s.names
	quote := func(name string) string { return pgx.Identifier{name}.Sanitize() }

	return []string{
		fmt.Sprintf(`CREATE TABLE %s (
  "Id" TEXT NOT NULL,
  "Kind" TEXT NOT NULL,
  "Axis" TEXT NOT NULL,
  "DomainId" TEXT NOT NULL,
  "Name" TEXT NOT NULL,
  "Description" TEXT NOT NULL,
  "UpdatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("Id"),
  CONSTRAINT %s CHECK ("Kind" IN ('%s', '%s'))
)`, n.groups, quote(n.rawGroups+"Kind"), policy.KindGlobal, policy.KindDomain),
		fmt.Sprintf(`CREATE UNIQUE INDEX %s ON %s ("Kind", "Axis", "DomainId", "Name")`, quote(n.rawGroupsByName), n.groups),
		fmt.Sprintf(`CREATE TABLE %s (
  "GroupId" TEXT NOT NULL,
  "UserId" TEXT NOT NULL,
  "CreatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("GroupId", "UserId"),
  CONSTRAINT %s FOREIGN KEY ("GroupId") REFERENCES %s ("Id")
)`, n.groupMembers, quote(n.rawGroupMembers+"Group"), n.groups),
		fmt.Sprintf(`CREATE INDEX %s ON %s ("UserId", "GroupId")`, quote(n.rawGroupMembersIndex), n.groupMembers),
		fmt.Sprintf(`CREATE TABLE %s (
  "GroupId" TEXT NOT NULL,
  "Kind" TEXT NOT NULL,
  "Axis" TEXT NOT NULL,
  "DomainId" TEXT NOT NULL,
  "Role" TEXT NOT NULL,
  "CreatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("GroupId", "Kind", "Axis", "DomainId", "Role"),
  CONSTRAINT %s CHECK ("Kind" IN ('%s', '%s', '%s')),
  CONSTRAINT %s FOREIGN KEY ("GroupId") REFERENCES %s ("Id")
)`, n.groupRoles, quote(n.rawGroupRoles+"Kind"), policy.KindGlobal, policy.KindDomain, policy.KindEvery, quote(n.rawGroupRoles+"Group"), n.groups),
	}
}

// readGroups reads the groups, their members and the roles they hold into the
// records, inside the policy read's transaction.
func (s *Store) readGroups(ctx context.Context, tx pgx.Tx, records *policy.Records) error {
	groupRows, err := tx.Query(ctx, s.groups.readGroups)
	if err != nil {
		return errors.Wrap(err, "pgx.Tx.Query() groups")
	}
	records.Groups, err = pgx.CollectRows(groupRows, func(row pgx.CollectableRow) (policy.Group, error) {
		var id, kind, axis, domain string
		if err := row.Scan(&id, &kind, &axis, &domain); err != nil {
			return policy.Group{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.GroupFromColumns(kind, axis, domain)
		if err != nil {
			return policy.Group{}, errors.Wrap(err, "policy.GroupFromColumns()")
		}

		return policy.Group{ID: accesstypes.Group(id), Scope: scope}, nil
	})
	if err != nil {
		return errors.Wrap(err, "pgx.CollectRows() groups")
	}

	memberRows, err := tx.Query(ctx, s.groups.readMembers)
	if err != nil {
		return errors.Wrap(err, "pgx.Tx.Query() group members")
	}
	records.GroupMembers, err = pgx.CollectRows(memberRows, func(row pgx.CollectableRow) (policy.GroupMember, error) {
		var m policy.GroupMember
		if err := row.Scan(&m.Group, &m.User); err != nil {
			return policy.GroupMember{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}

		return m, nil
	})
	if err != nil {
		return errors.Wrap(err, "pgx.CollectRows() group members")
	}

	roleRows, err := tx.Query(ctx, s.groups.readRoles)
	if err != nil {
		return errors.Wrap(err, "pgx.Tx.Query() group roles")
	}
	roles, err := pgx.CollectRows(roleRows, func(row pgx.CollectableRow) (policy.Membership, error) {
		var m policy.Membership
		var group, kind, axis, domain string
		if err := row.Scan(&group, &kind, &axis, &domain, &m.Role); err != nil {
			return policy.Membership{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return policy.Membership{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		m.Scope = scope
		m.Member = policy.Subject{Kind: policy.SubjectGroup, Name: group}

		return m, nil
	})
	if err != nil {
		return errors.Wrap(err, "pgx.CollectRows() group roles")
	}
	records.Memberships = append(records.Memberships, roles...)

	return nil
}

// InsertGroup creates the group's row. An id already taken, or a name another
// group kept in the same place holds, is reported as taken and writes nothing.
func (s *Store) InsertGroup(ctx context.Context, group *policy.GroupRow) (bool, error) {
	kind, axis, domain, err := policy.GroupColumns(group.Scope)
	if err != nil {
		return false, errors.Wrap(err, "policy.GroupColumns()")
	}
	if _, err := s.pool.Exec(ctx, s.groups.insertGroup, group.ID, kind, axis, domain, group.Name, group.Description); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return true, nil
		}

		return false, errors.Wrap(err, "pgxpool.Pool.Exec() insert group")
	}

	return false, nil
}

// SetGroupDescription replaces the group's description and reports whether
// the group was there.
func (s *Store) SetGroupDescription(ctx context.Context, group accesstypes.Group, description string) (bool, error) {
	tag, err := s.pool.Exec(ctx, s.groups.setDescription, group, description)
	if err != nil {
		return false, errors.Wrap(err, "pgxpool.Pool.Exec() describe group")
	}

	return tag.RowsAffected() > 0, nil
}

// DeleteGroup deletes the group's row and reports whether it was there. A
// group that still has a member or a role is refused, checked in the delete's
// own transaction; the foreign keys of the two tables stand behind the check.
func (s *Store) DeleteGroup(ctx context.Context, group accesstypes.Group) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, errors.Wrap(err, "pgxpool.Pool.Begin() delete group")
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	for _, check := range []struct{ sql, what string }{{s.groups.hasMembers, "members"}, {s.groups.hasRoles, "roles"}} {
		var held bool
		if err := tx.QueryRow(ctx, check.sql, group).Scan(&held); err != nil {
			return false, errors.Wrapf(err, "pgx.Tx.QueryRow() group %s", check.what)
		}
		if held {
			return false, errors.Newf("group %s still has %s", group, check.what)
		}
	}
	tag, err := tx.Exec(ctx, s.groups.deleteGroup, group)
	if err != nil {
		return false, errors.Wrap(err, "pgx.Tx.Exec() delete group")
	}
	if err := tx.Commit(ctx); err != nil {
		return false, errors.Wrap(err, "pgx.Tx.Commit() delete group")
	}

	return tag.RowsAffected() > 0, nil
}

// Group returns the group kept under the id; found is false where none is.
func (s *Store) Group(ctx context.Context, group accesstypes.Group) (*policy.GroupRow, bool, error) {
	rows, err := s.queryGroups(ctx, s.groups.group, group)
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

	return s.queryGroups(ctx, s.groups.listGroups, kind, axis, domain)
}

// ListUserGroups returns the groups the user is in, sorted by id.
func (s *Store) ListUserGroups(ctx context.Context, user accesstypes.User) ([]policy.GroupRow, error) {
	return s.queryGroups(ctx, s.groups.listUserGroups, user)
}

// queryGroups runs a statement that selects whole group rows.
func (s *Store) queryGroups(ctx context.Context, sql string, args ...any) ([]policy.GroupRow, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() groups")
	}
	groups, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.GroupRow, error) {
		var g policy.GroupRow
		var kind, axis, domain string
		if err := row.Scan(&g.ID, &kind, &axis, &domain, &g.Name, &g.Description); err != nil {
			return policy.GroupRow{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.GroupFromColumns(kind, axis, domain)
		if err != nil {
			return policy.GroupRow{}, errors.Wrap(err, "policy.GroupFromColumns()")
		}
		g.Scope = scope

		return g, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() groups")
	}

	return groups, nil
}

// InsertGroupMember adds one person to a group; adding one who is there is a
// no-op. The group's row must exist.
func (s *Store) InsertGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User) error {
	if _, err := s.pool.Exec(ctx, s.groups.insertMember, group, user); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() insert group member")
	}

	return nil
}

// DeleteGroupMember removes one person from a group; removing one who is not
// there is a no-op.
func (s *Store) DeleteGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User) error {
	if _, err := s.pool.Exec(ctx, s.groups.deleteMember, group, user); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() delete group member")
	}

	return nil
}

// ListGroupMembers returns the group's members, sorted.
func (s *Store) ListGroupMembers(ctx context.Context, group accesstypes.Group) ([]accesstypes.User, error) {
	rows, err := s.pool.Query(ctx, s.groups.listMembers, group)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() group members")
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[accesstypes.User])
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() group members")
	}

	return users, nil
}

// InsertGroupRole adds one role a group holds in scope; adding one it holds
// there is a no-op. The group's row must exist; the role is named by name
// alone.
func (s *Store) InsertGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	if _, err := s.pool.Exec(ctx, s.groups.insertRole, group, kind, axis, domain, role); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() insert group role")
	}

	return nil
}

// DeleteGroupRole removes one role a group holds in scope; removing one it
// does not hold there is a no-op.
func (s *Store) DeleteGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	if _, err := s.pool.Exec(ctx, s.groups.deleteRole, group, kind, axis, domain, role); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() delete group role")
	}

	return nil
}

// ListGroupRoles returns the roles the group holds, sorted by scope then role.
func (s *Store) ListGroupRoles(ctx context.Context, group accesstypes.Group) ([]policy.Membership, error) {
	rows, err := s.pool.Query(ctx, s.groups.listRoles, group)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() group roles")
	}
	roles, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.Membership, error) {
		var kind, axis, domain string
		m := policy.Membership{Member: policy.Subject{Kind: policy.SubjectGroup, Name: string(group)}}
		if err := row.Scan(&kind, &axis, &domain, &m.Role); err != nil {
			return policy.Membership{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return policy.Membership{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		m.Scope = scope

		return m, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() group roles")
	}

	return roles, nil
}

// ListUserGroupRoles returns every role held by a group the user is in, each
// with its group, read through the members' index by user.
func (s *Store) ListUserGroupRoles(ctx context.Context, user accesstypes.User) ([]policy.GroupRole, error) {
	rows, err := s.pool.Query(ctx, s.groups.listUserRoles, user)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() user group roles")
	}
	roles, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.GroupRole, error) {
		var r policy.GroupRole
		var groupKind, groupAxis, groupDomain, kind, axis, domain string
		if err := row.Scan(&r.Group.ID, &groupKind, &groupAxis, &groupDomain, &kind, &axis, &domain, &r.Role); err != nil {
			return policy.GroupRole{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		kept, err := policy.GroupFromColumns(groupKind, groupAxis, groupDomain)
		if err != nil {
			return policy.GroupRole{}, errors.Wrap(err, "policy.GroupFromColumns()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return policy.GroupRole{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		r.Group.Scope, r.Scope = kept, scope

		return r, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() user group roles")
	}

	return roles, nil
}
