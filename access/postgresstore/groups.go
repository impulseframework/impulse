package postgresstore

import (
	"context"
	"fmt"
	"time"

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
	lockGroup      string
	shareGroup     string
	hasMembers     string
	hasRoles       string
	deleteEnded    []string
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
	listRoleGroups string
	countMembersIn string
	listRolesIn    string
	roleHeld       string
	deleteEndedOf  string
	shareEndedOf   string
	heldInDomain   string
	endedInDomain  string
	readGroups     string
	readMembers    string
	readRoles      string
}

func newGroupStatements(n *tableNames) groupStatements {
	return groupStatements{
		insertGroup:    fmt.Sprintf(`insert into %s ("Id", "Kind", "Axis", "DomainId", "Name", "Description") values ($1, $2, $3, $4, $5, $6)`, n.groups),
		setDescription: fmt.Sprintf(`update %s set "Description" = $2, "UpdatedAt" = now() where "Id" = $1`, n.groups),
		deleteGroup:    fmt.Sprintf(`delete from %s where "Id" = $1`, n.groups),
		lockGroup:      fmt.Sprintf(`select 1 from %s where "Id" = $1 for update`, n.groups),
		shareGroup:     fmt.Sprintf(`select 1 from %s where "Id" = $1 for key share`, n.groups),
		hasMembers:     fmt.Sprintf(`select exists(select 1 from %s where "GroupId" = $1 and %s)`, n.groupMembers, standing("")),
		hasRoles:       fmt.Sprintf(`select exists(select 1 from %s where "GroupId" = $1 and %s)`, n.groupRoles, standing("")),
		deleteEnded: []string{
			fmt.Sprintf(`delete from %s where "GroupId" = $1 and %s`, n.groupMembers, ended),
			fmt.Sprintf(`delete from %s where "GroupId" = $1 and %s`, n.groupRoles, ended),
		},
		group:      fmt.Sprintf(`select "Id", "Kind", "Axis", "DomainId", "Name", "Description" from %s where "Id" = $1`, n.groups),
		listGroups: fmt.Sprintf(`select "Id", "Kind", "Axis", "DomainId", "Name", "Description" from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 order by "Name"`, n.groups),
		insertMember: fmt.Sprintf(`insert into %s ("GroupId", "UserId", "StartsAt", "EndsAt") values ($1, $2, $3, $4)
on conflict ("GroupId", "UserId") do update set %s`, n.groupMembers, replacePeriod),
		deleteMember: fmt.Sprintf(`delete from %s where "GroupId" = $1 and "UserId" = $2`, n.groupMembers),
		listMembers:  fmt.Sprintf(`select "UserId" from %s where "GroupId" = $1 and %s order by "UserId"`, n.groupMembers, counting("")),
		listUserGroups: fmt.Sprintf(`select g."Id", g."Kind", g."Axis", g."DomainId", g."Name", g."Description" from %s m join %s g on g."Id" = m."GroupId" where m."UserId" = $1 and %s order by g."Id"`,
			n.groupMembers, n.groups, counting("m.")),
		insertRole: fmt.Sprintf(`insert into %s ("GroupId", "Kind", "Axis", "DomainId", "Role", "StartsAt", "EndsAt") values ($1, $2, $3, $4, $5, $6, $7)
on conflict ("GroupId", "Kind", "Axis", "DomainId", "Role") do update set %s`, n.groupRoles, replacePeriod),
		deleteRole: fmt.Sprintf(`delete from %s where "GroupId" = $1 and "Kind" = $2 and "Axis" = $3 and "DomainId" = $4 and "Role" = $5`, n.groupRoles),
		listRoles: fmt.Sprintf(`select "Kind", "Axis", "DomainId", "Role", "StartsAt", "EndsAt" from %s where "GroupId" = $1 and %s order by "Kind", "Axis", "DomainId", "Role"`,
			n.groupRoles, counting("")),
		listUserRoles: fmt.Sprintf(`select g."Id", g."Kind", g."Axis", g."DomainId", r."Kind", r."Axis", r."DomainId", r."Role"
from %s m join %s g on g."Id" = m."GroupId" join %s r on r."GroupId" = m."GroupId"
where m."UserId" = $1 and %s and %s order by r."Kind", r."Axis", r."DomainId", r."Role", g."Id"`, n.groupMembers, n.groups, n.groupRoles, counting("m."), counting("r.")),
		listRoleGroups: fmt.Sprintf(`select g."Id", g."Kind", g."Axis", g."DomainId", g."Name", g."Description"
from %s r join %s g on g."Id" = r."GroupId"
where r."Kind" = $1 and r."Axis" = $2 and r."DomainId" = $3 and r."Role" = $4 and %s order by g."Name", g."Id"`, n.groupRoles, n.groups, counting("r.")),
		countMembersIn: fmt.Sprintf(`select m."GroupId", count(*)
from %s g join %s m on m."GroupId" = g."Id"
where g."Kind" = $1 and g."Axis" = $2 and g."DomainId" = $3 and %s group by m."GroupId"`, n.groups, n.groupMembers, standing("m.")),
		listRolesIn: fmt.Sprintf(`select r."GroupId", r."Kind", r."Axis", r."DomainId", r."Role", r."StartsAt", r."EndsAt"
from %s g join %s r on r."GroupId" = g."Id"
where g."Kind" = $1 and g."Axis" = $2 and g."DomainId" = $3 and %s order by r."GroupId", r."Kind", r."Axis", r."DomainId", r."Role"`, n.groups, n.groupRoles, standing("r.")),
		roleHeld:      fmt.Sprintf(`select exists(select 1 from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 and %s)`, n.groupRoles, standing("")),
		deleteEndedOf: fmt.Sprintf(`delete from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 and %s`, n.groupRoles, ended),
		// The rows of the groups that hold a role of the name whose end has passed,
		// wherever they hold it, for key share.
		shareEndedOf: fmt.Sprintf(`select 1 from %s g where g."Id" in (select r."GroupId" from %s r where r."Role" = $1 and r.%s) order by g."Id" for key share of g`,
			n.groups, n.groupRoles, ended),
		// The group roles held in one domain that read a role's every-domain row.
		heldInDomain:  fmt.Sprintf(`select u."DomainId" from %s u where %s and %s order by u."DomainId" limit 1`, n.groupRoles, readsEveryDomainRow(n.roles), standing("u.")),
		endedInDomain: fmt.Sprintf(`delete from %s u where %s and u.%s`, n.groupRoles, readsEveryDomainRow(n.roles), ended),
		readGroups:    fmt.Sprintf(`select "Id", "Kind", "Axis", "DomainId" from %s`, n.groups),
		readMembers:   fmt.Sprintf(`select "GroupId", "UserId", "StartsAt", "EndsAt" from %s`, n.groupMembers),
		readRoles:     fmt.Sprintf(`select "GroupId", "Kind", "Axis", "DomainId", "Role", "StartsAt", "EndsAt" from %s`, n.groupRoles),
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
  "StartsAt" TIMESTAMPTZ,
  "EndsAt" TIMESTAMPTZ,
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
  "StartsAt" TIMESTAMPTZ,
  "EndsAt" TIMESTAMPTZ,
  "CreatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("GroupId", "Kind", "Axis", "DomainId", "Role"),
  CONSTRAINT %s CHECK ("Kind" IN ('%s', '%s', '%s')),
  CONSTRAINT %s FOREIGN KEY ("GroupId") REFERENCES %s ("Id")
)`, n.groupRoles, quote(n.rawGroupRoles+"Kind"), policy.KindGlobal, policy.KindDomain, policy.KindEvery, quote(n.rawGroupRoles+"Group"), n.groups),
		fmt.Sprintf(`CREATE INDEX %s ON %s ("Kind", "Axis", "DomainId", "Role")`, quote(n.rawGroupRolesIndex), n.groupRoles),
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
		var start, end *time.Time
		if err := row.Scan(&m.Group, &m.User, &start, &end); err != nil {
			return policy.GroupMember{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		m.Period = periodOf(start, end)

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
		var start, end *time.Time
		if err := row.Scan(&group, &kind, &axis, &domain, &m.Role, &start, &end); err != nil {
			return policy.Membership{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return policy.Membership{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		m.Scope = scope
		m.Member = policy.Subject{Kind: policy.SubjectGroup, Name: group}
		m.Period = periodOf(start, end)

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
// group that still has a member or a role that has not ended is refused,
// checked in the delete's own transaction; the ended ones go with the group,
// and the foreign keys of the two tables stand behind the check. The
// transaction first locks the group's row, which a member's or a role's insert
// for the group waits on and holds a lock against: of a delete and such an
// insert at the same moment, the delete reads the member or the role and
// refuses, or the insert finds the group gone.
func (s *Store) DeleteGroup(ctx context.Context, group accesstypes.Group) (bool, error) {
	// Read committed whatever the database's default, as InsertRole: the checks
	// after the lock see what the insert that held the group's row before it
	// committed.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, errors.Wrap(err, "pgxpool.Pool.BeginTx() delete group")
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	// A group with no row locks nothing, and the delete below answers false.
	if _, err := tx.Exec(ctx, s.groups.lockGroup, group); err != nil {
		return false, errors.Wrap(err, "pgx.Tx.Exec() lock group")
	}
	for _, check := range []struct{ sql, what string }{{s.groups.hasMembers, "members"}, {s.groups.hasRoles, "roles"}} {
		var held bool
		if err := tx.QueryRow(ctx, check.sql, group).Scan(&held); err != nil {
			return false, errors.Wrapf(err, "pgx.Tx.QueryRow() group %s", check.what)
		}
		if held {
			return false, errors.Wrapf(policy.ErrStillAssigned, "group %s still has %s", group, check.what)
		}
	}
	for _, sql := range s.groups.deleteEnded {
		if _, err := tx.Exec(ctx, sql, group); err != nil {
			return false, errors.Wrap(err, "pgx.Tx.Exec() ended rows")
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

// ListRoleGroups returns the groups that hold the role in scope now, sorted by
// name then id.
func (s *Store) ListRoleGroups(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]policy.GroupRow, error) {
	kind, axis, domain := policy.ScopeColumns(scope)

	return s.queryGroups(ctx, s.groups.listRoleGroups, kind, axis, domain, role)
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

// InsertGroupMember gives one person's place in a group its period: the person
// is added where they are not in the group, and their place given the new
// period where they are. It reads the group's row for key share before it
// writes, as writeUnder says, and wraps policy.ErrNoGroup where the row is not
// there.
func (s *Store) InsertGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User, period accesstypes.Period) error {
	return s.writeUnder(ctx, "assign group member", s.groups.insertMember, []any{group, user, period.StartPtr(), period.EndPtr()}, s.groupRow(group))
}

// groupRow is the parent of a group's member and of a group's role: the group's
// row.
func (s *Store) groupRow(group accesstypes.Group) parent {
	return parent{
		sql:  s.groups.shareGroup,
		args: []any{group},
		gone: func() error {
			return errors.Wrapf(policy.ErrNoGroup, "group %s", group)
		},
	}
}

// DeleteGroupMember removes one person from a group; removing one who is not
// there is a no-op.
func (s *Store) DeleteGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User) error {
	if _, err := s.pool.Exec(ctx, s.groups.deleteMember, group, user); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() delete group member")
	}

	return nil
}

// ListGroupMembers returns the people in the group now, sorted.
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

// InsertGroupRole gives one role a group holds in scope its period: the role is
// added where the group does not hold it there, and given the new period where
// it does. It reads the group's row for key share before it writes, as
// writeUnder says, and wraps policy.ErrNoGroup where the row is not there. The
// role is named by name alone.
func (s *Store) InsertGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	kind, axis, domain := policy.ScopeColumns(scope)

	return s.writeUnder(ctx, "assign group role", s.groups.insertRole, []any{group, kind, axis, domain, role, period.StartPtr(), period.EndPtr()}, s.groupRow(group))
}

// InsertCustomGroupRole writes what InsertGroupRole writes, only while the roles
// table holds a row of the custom role where the group's role reads it, as
// InsertCustomUserRole reads it. It reads the role's rows for key share and then
// the group's row, as writeUnder says: where the role's are not there it wraps
// policy.ErrNoRole, whether or not the group is, and where the group's is not,
// policy.ErrNoGroup.
func (s *Store) InsertCustomGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	kind, axis, domain := policy.ScopeColumns(scope)

	return s.writeUnder(ctx, "assign group role", s.groups.insertRole, []any{group, kind, axis, domain, role, period.StartPtr(), period.EndPtr()}, s.customRole(scope, role), s.groupRow(group))
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

// ListGroupRoles returns the roles the group holds now, sorted by scope then
// role.
func (s *Store) ListGroupRoles(ctx context.Context, group accesstypes.Group) ([]policy.Membership, error) {
	rows, err := s.pool.Query(ctx, s.groups.listRoles, group)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() group roles")
	}
	roles, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.Membership, error) {
		var kind, axis, domain string
		var start, end *time.Time
		m := policy.Membership{Member: policy.Subject{Kind: policy.SubjectGroup, Name: string(group)}}
		if err := row.Scan(&kind, &axis, &domain, &m.Role, &start, &end); err != nil {
			return policy.Membership{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return policy.Membership{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		m.Scope = scope
		m.Period = periodOf(start, end)

		return m, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() group roles")
	}

	return roles, nil
}

// CountGroupMembersIn returns how many people each group kept in scope has: the
// places in it that have not ended, the ones that count now and the ones that
// have not begun, which are what stands in the way of the group's deletion. A
// group with none is not in the answer.
func (s *Store) CountGroupMembersIn(ctx context.Context, scope accesstypes.PolicyScope) (map[accesstypes.Group]int, error) {
	kind, axis, domain, err := policy.GroupColumns(scope)
	if err != nil {
		return nil, errors.Wrap(err, "policy.GroupColumns()")
	}
	rows, err := s.pool.Query(ctx, s.groups.countMembersIn, kind, axis, domain)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() group member counts")
	}
	counts := make(map[accesstypes.Group]int)
	var group accesstypes.Group
	var count int
	if _, err := pgx.ForEachRow(rows, []any{&group, &count}, func() error {
		counts[group] = count

		return nil
	}); err != nil {
		return nil, errors.Wrap(err, "pgx.ForEachRow() group member counts")
	}

	return counts, nil
}

// ListGroupRolesIn returns the roles the groups kept in scope hold that have
// not ended, each with its group and its period, sorted by group, then by
// where the role is held, then by role.
func (s *Store) ListGroupRolesIn(ctx context.Context, scope accesstypes.PolicyScope) ([]policy.Membership, error) {
	kind, axis, domain, err := policy.GroupColumns(scope)
	if err != nil {
		return nil, errors.Wrap(err, "policy.GroupColumns()")
	}
	rows, err := s.pool.Query(ctx, s.groups.listRolesIn, kind, axis, domain)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() group roles in a scope")
	}
	roles, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.Membership, error) {
		var group, kind, axis, domain string
		var start, end *time.Time
		var m policy.Membership
		if err := row.Scan(&group, &kind, &axis, &domain, &m.Role, &start, &end); err != nil {
			return policy.Membership{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		held, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return policy.Membership{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		m.Scope = held
		m.Member = policy.Subject{Kind: policy.SubjectGroup, Name: group}
		m.Period = periodOf(start, end)

		return m, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() group roles in a scope")
	}

	return roles, nil
}

// ListUserGroupRoles returns every role held now by a group the user is in
// now, each with its group, read through the members' index by user.
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
