package postgresstore

import (
	"context"
	"fmt"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/jackc/pgx/v5"
)

// The roles held on one record of a tenant: two tables beside the memberships, named
// {Prefix}{Store}{RecordUserRoles|RecordGroupRoles}. A row names the record by its
// tenant ("Axis", "DomainId"), its kind ("Resource") and its key ("RecordId"), then the
// role and the person or the group, so one record's holders are one range of the key. A
// group's row names its group by a foreign key, as a group's role does.

// recordStatements are the record tables' statements, built once with the store's table
// names.
type recordStatements struct {
	insertUser   string
	deleteUser   string
	insertGroup  string
	deleteGroup  string
	listUsers    string
	listGroups   string
	deleteUsers  string
	deleteGroups string
	readUsers    string
	readGroups   string
}

func newRecordStatements(n *tableNames) recordStatements {
	return recordStatements{
		insertUser: fmt.Sprintf(`insert into %s ("Axis", "DomainId", "Resource", "RecordId", "Role", "UserId", "StartsAt", "EndsAt") values ($1, $2, $3, $4, $5, $6, $7, $8)
on conflict ("Axis", "DomainId", "Resource", "RecordId", "Role", "UserId") do update set %s`, n.recordUserRoles, replacePeriod),
		deleteUser: fmt.Sprintf(`delete from %s where "Axis" = $1 and "DomainId" = $2 and "Resource" = $3 and "RecordId" = $4 and "Role" = $5 and "UserId" = $6`, n.recordUserRoles),
		insertGroup: fmt.Sprintf(`insert into %s ("Axis", "DomainId", "Resource", "RecordId", "Role", "GroupId", "StartsAt", "EndsAt") values ($1, $2, $3, $4, $5, $6, $7, $8)
on conflict ("Axis", "DomainId", "Resource", "RecordId", "Role", "GroupId") do update set %s`, n.recordGroupRoles, replacePeriod),
		deleteGroup: fmt.Sprintf(`delete from %s where "Axis" = $1 and "DomainId" = $2 and "Resource" = $3 and "RecordId" = $4 and "Role" = $5 and "GroupId" = $6`, n.recordGroupRoles),
		listUsers: fmt.Sprintf(`select "UserId", "Role", "StartsAt", "EndsAt" from %s where "Axis" = $1 and "DomainId" = $2 and "Resource" = $3 and "RecordId" = $4 and %s order by "UserId", "Role"`,
			n.recordUserRoles, standing("")),
		listGroups: fmt.Sprintf(`select r."GroupId", g."Name", r."Role", r."StartsAt", r."EndsAt"
from %s r join %s g on g."Id" = r."GroupId"
where r."Axis" = $1 and r."DomainId" = $2 and r."Resource" = $3 and r."RecordId" = $4 and %s order by g."Name", r."GroupId", r."Role"`, n.recordGroupRoles, n.groups, standing("r.")),
		deleteUsers:  fmt.Sprintf(`delete from %s where "Axis" = $1 and "DomainId" = $2 and "Resource" = $3 and "RecordId" = $4`, n.recordUserRoles),
		deleteGroups: fmt.Sprintf(`delete from %s where "Axis" = $1 and "DomainId" = $2 and "Resource" = $3 and "RecordId" = $4`, n.recordGroupRoles),
		readUsers:    fmt.Sprintf(`select "Axis", "DomainId", "Resource", "RecordId", "UserId", "Role", "StartsAt", "EndsAt" from %s`, n.recordUserRoles),
		readGroups:   fmt.Sprintf(`select "Axis", "DomainId", "Resource", "RecordId", "GroupId", "Role", "StartsAt", "EndsAt" from %s`, n.recordGroupRoles),
	}
}

// recordDDL returns the record tables' schema statements.
func (s *Store) recordDDL() []string {
	n := s.names
	quote := func(name string) string { return pgx.Identifier{name}.Sanitize() }

	return []string{
		fmt.Sprintf(`CREATE TABLE %s (
  "Axis" TEXT NOT NULL,
  "DomainId" TEXT NOT NULL,
  "Resource" TEXT NOT NULL,
  "RecordId" TEXT NOT NULL,
  "Role" TEXT NOT NULL,
  "UserId" TEXT NOT NULL,
  "StartsAt" TIMESTAMPTZ,
  "EndsAt" TIMESTAMPTZ,
  "CreatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("Axis", "DomainId", "Resource", "RecordId", "Role", "UserId")
)`, n.recordUserRoles),
		fmt.Sprintf(`CREATE INDEX %s ON %s ("UserId", "Axis", "DomainId")`, quote(n.rawRecordUserIndex), n.recordUserRoles),
		fmt.Sprintf(`CREATE TABLE %s (
  "Axis" TEXT NOT NULL,
  "DomainId" TEXT NOT NULL,
  "Resource" TEXT NOT NULL,
  "RecordId" TEXT NOT NULL,
  "Role" TEXT NOT NULL,
  "GroupId" TEXT NOT NULL,
  "StartsAt" TIMESTAMPTZ,
  "EndsAt" TIMESTAMPTZ,
  "CreatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("Axis", "DomainId", "Resource", "RecordId", "Role", "GroupId"),
  CONSTRAINT %s FOREIGN KEY ("GroupId") REFERENCES %s ("Id")
)`, n.recordGroupRoles, quote(n.rawRecordGroupRoles+"Group"), n.groups),
		fmt.Sprintf(`CREATE INDEX %s ON %s ("GroupId")`, quote(n.rawRecordGroupIndex), n.recordGroupRoles),
	}
}

// recordArgs are the statement arguments that name one record, then what follows.
func recordArgs(at accesstypes.RecordScope, more ...any) []any {
	axis, domain, resource, record := policy.RecordColumns(at)

	return append([]any{axis, domain, resource, record}, more...)
}

// readRecords reads the roles held on records, a person's and a group's, into the
// records, inside the policy read's transaction.
func (s *Store) readRecords(ctx context.Context, tx pgx.Tx, records *policy.Records) error {
	for _, read := range []struct {
		sql  string
		kind policy.SubjectKind
		what string
	}{
		{sql: s.recordRoles.readUsers, kind: policy.SubjectUser, what: "record user roles"},
		{sql: s.recordRoles.readGroups, kind: policy.SubjectGroup, what: "record group roles"},
	} {
		rows, err := tx.Query(ctx, read.sql)
		if err != nil {
			return errors.Wrapf(err, "pgx.Tx.Query() %s", read.what)
		}
		held, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.RecordMembership, error) {
			var m policy.RecordMembership
			var axis, domain, resource, record, member string
			var start, end *time.Time
			if err := row.Scan(&axis, &domain, &resource, &record, &member, &m.Role, &start, &end); err != nil {
				return policy.RecordMembership{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
			}
			at, err := policy.RecordFromColumns(axis, domain, resource, record)
			if err != nil {
				return policy.RecordMembership{}, errors.Wrap(err, "policy.RecordFromColumns()")
			}
			m.Scope = at
			m.Member = policy.Subject{Kind: read.kind, Name: member}
			m.Period = periodOf(start, end)

			return m, nil
		})
		if err != nil {
			return errors.Wrapf(err, "pgx.CollectRows() %s", read.what)
		}
		records.RecordMemberships = append(records.RecordMemberships, held...)
	}

	return nil
}

// InsertRecordUserRole gives one role a person holds on a record its period: the role is
// added where the person does not hold it there, and given the new period where they do.
// The role is named by name alone.
func (s *Store) InsertRecordUserRole(ctx context.Context, at accesstypes.RecordScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period) error {
	if _, err := s.pool.Exec(ctx, s.recordRoles.insertUser, recordArgs(at, role, user, period.StartPtr(), period.EndPtr())...); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() assign record user role")
	}

	return nil
}

// DeleteRecordUserRole removes one role a person holds on a record; removing one they do
// not hold there is a no-op.
func (s *Store) DeleteRecordUserRole(ctx context.Context, at accesstypes.RecordScope, user accesstypes.User, role accesstypes.Role) error {
	if _, err := s.pool.Exec(ctx, s.recordRoles.deleteUser, recordArgs(at, role, user)...); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() delete record user role")
	}

	return nil
}

// InsertRecordGroupRole gives one role a group holds on a record its period: the role is
// added where the group does not hold it there, and given the new period where it does.
// It reads the group's row for key share before it writes, as writeUnder says, and wraps
// policy.ErrNoGroup where the row is not there. The role is named by name alone.
func (s *Store) InsertRecordGroupRole(ctx context.Context, group accesstypes.Group, at accesstypes.RecordScope, role accesstypes.Role, period accesstypes.Period) error {
	return s.writeUnder(ctx, "assign record group role", s.recordRoles.insertGroup, recordArgs(at, role, group, period.StartPtr(), period.EndPtr()), nil, s.groupRow(group))
}

// DeleteRecordGroupRole removes one role a group holds on a record; removing one it does
// not hold there is a no-op.
func (s *Store) DeleteRecordGroupRole(ctx context.Context, group accesstypes.Group, at accesstypes.RecordScope, role accesstypes.Role) error {
	if _, err := s.pool.Exec(ctx, s.recordRoles.deleteGroup, recordArgs(at, role, group)...); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() delete record group role")
	}

	return nil
}

// ListRecordRoles returns who holds what on the record and has not ended, one whose
// period has not begun among them: the people's first, by user then role, then the
// groups', by group name, then id, then role. Both are read in one repeatable-read
// transaction.
func (s *Store) ListRecordRoles(ctx context.Context, at accesstypes.RecordScope) ([]policy.RecordAssignment, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.BeginTx() record roles")
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	userRows, err := tx.Query(ctx, s.recordRoles.listUsers, recordArgs(at)...)
	if err != nil {
		return nil, errors.Wrap(err, "pgx.Tx.Query() record user roles")
	}
	held, err := pgx.CollectRows(userRows, func(row pgx.CollectableRow) (policy.RecordAssignment, error) {
		var a policy.RecordAssignment
		var start, end *time.Time
		if err := row.Scan(&a.Member.Name, &a.Role, &start, &end); err != nil {
			return policy.RecordAssignment{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		a.Scope = at
		a.Member.Kind = policy.SubjectUser
		a.Period = periodOf(start, end)

		return a, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() record user roles")
	}
	groupRows, err := tx.Query(ctx, s.recordRoles.listGroups, recordArgs(at)...)
	if err != nil {
		return nil, errors.Wrap(err, "pgx.Tx.Query() record group roles")
	}
	groups, err := pgx.CollectRows(groupRows, func(row pgx.CollectableRow) (policy.RecordAssignment, error) {
		var a policy.RecordAssignment
		var start, end *time.Time
		if err := row.Scan(&a.Member.Name, &a.GroupName, &a.Role, &start, &end); err != nil {
			return policy.RecordAssignment{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		a.Scope = at
		a.Member.Kind = policy.SubjectGroup
		a.Period = periodOf(start, end)

		return a, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() record group roles")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, errors.Wrap(err, "pgx.Tx.Commit() record roles")
	}

	return append(held, groups...), nil
}

// DeleteRecordRoles removes every role held on the record, a person's and a group's,
// whatever their periods, in one transaction.
func (s *Store) DeleteRecordRoles(ctx context.Context, at accesstypes.RecordScope) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return errors.Wrap(err, "pgxpool.Pool.BeginTx() delete record roles")
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	for _, sql := range []string{s.recordRoles.deleteUsers, s.recordRoles.deleteGroups} {
		if _, err := tx.Exec(ctx, sql, recordArgs(at)...); err != nil {
			return errors.Wrap(err, "pgx.Tx.Exec() delete record roles")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.Wrap(err, "pgx.Tx.Commit() delete record roles")
	}

	return nil
}
