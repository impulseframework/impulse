// Package postgresstore implements access.Store over PostgreSQL, riding the
// application's existing connection pool.
//
// Its sibling github.com/impulseframework/impulse/access/spannerstore serves Cloud
// Spanner-backed deployments.
//
// Each Store owns three tables named {Prefix}{Store}{Roles|UserRoles|
// RoleGrants} — defaults yield AccessRoles, AccessUserRoles, AccessRoleGrants —
// and the three of the groups of people (groups.go), and the two of the roles held on
// one record of a tenant (records.go).
// Rows are partitioned by where the policy is held, persisted as the
// structural column triple ("Kind", "Axis", "DomainId"): Kind names the global
// partition, one domain or every domain — never a distinguished domain value
// — and the axis is the name of the axis the domain belongs to, "" for the
// default axis, which is every scope today. The Roles table holds custom
// roles only, each with the description its creator gave it; a membership
// names a role by name alone, since the release's
// default roles have no row, so UserRoles stands alone with an index led by
// the user for the no-scope listing. Separate tables per store make
// cross-store leakage structurally impossible: there is no store-key WHERE
// clause to forget. DDL returns the tables' canonical schema rendered with the
// configured names; apps copy it into a migration file (the library never
// runs DDL — apps own their schema lifecycle).
package postgresstore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ access.Store = (*Store)(nil)

// Option configures a Store.
type Option func(*config)

type config struct {
	store        string
	prefix       string
	auditRecords string
}

// WithStore names the policy store this client owns; it becomes part of the
// table names ({Prefix}{Store}Roles, ...). Apps with several independent
// permission stores in one database give each its own name; the default is
// the empty name.
func WithStore(store string) Option {
	return func(c *config) { c.store = store }
}

// WithPrefix overrides the shared leading table-name prefix (default
// "Access"), which keeps every access table contiguous in a sorted listing.
func WithPrefix(prefix string) Option {
	return func(c *config) { c.prefix = prefix }
}

// WithAuditRecords names the table the store writes the record of a person's role
// given or taken in: the auth's audit records table, as session.TablesWithPrefix names
// it (AuditRecords), in the database the store writes. Each of the three writes of a
// person's role (InsertUserRole, InsertCustomUserRole, DeleteUserRole) writes the record
// it is handed in its own transaction. With no table named, no record is written.
func WithAuditRecords(table string) Option {
	return func(c *config) { c.auditRecords = table }
}

// tableNames holds the three quoted table identifiers plus the raw (unquoted)
// names for DDL object naming.
type tableNames struct {
	roles      string
	userRoles  string
	roleGrants string

	rawRoles      string
	rawUserRoles  string
	rawRoleGrants string
	rawUserIndex  string

	groups       string
	groupMembers string
	groupRoles   string

	rawGroups            string
	rawGroupMembers      string
	rawGroupRoles        string
	rawGroupsByName      string
	rawGroupMembersIndex string
	rawGroupRolesIndex   string

	recordUserRoles  string
	recordGroupRoles string

	rawRecordUserRoles  string
	rawRecordGroupRoles string
	rawRecordUserIndex  string
	rawRecordGroupIndex string

	// auditRecords is the audit records table (WithAuditRecords), unquoted, "" for none.
	auditRecords string
}

func resolveNames(opts []Option) (tableNames, error) {
	c := config{prefix: access.DefaultTablePrefix}
	for _, opt := range opts {
		opt(&c)
	}
	tables, err := access.TablesWithPrefix(c.prefix, c.store)
	if err != nil {
		return tableNames{}, errors.Wrap(err, "access.TablesWithPrefix()")
	}
	n := tableNames{
		rawRoles:      tables.Roles,
		rawUserRoles:  tables.UserRoles,
		rawRoleGrants: tables.RoleGrants,
		rawUserIndex:  tables.UserRolesByUserID,

		rawGroups:            tables.Groups,
		rawGroupMembers:      tables.GroupMembers,
		rawGroupRoles:        tables.GroupRoles,
		rawGroupsByName:      tables.GroupsByName,
		rawGroupMembersIndex: tables.GroupMembersByUserID,
		rawGroupRolesIndex:   tables.GroupRolesByScope,

		rawRecordUserRoles:  tables.RecordUserRoles,
		rawRecordGroupRoles: tables.RecordGroupRoles,
		rawRecordUserIndex:  tables.RecordUserRolesByUserID,
		rawRecordGroupIndex: tables.RecordGroupRolesByGroupID,
		auditRecords:        c.auditRecords,
	}
	n.groups = pgx.Identifier{n.rawGroups}.Sanitize()
	n.groupMembers = pgx.Identifier{n.rawGroupMembers}.Sanitize()
	n.groupRoles = pgx.Identifier{n.rawGroupRoles}.Sanitize()
	n.roles = pgx.Identifier{n.rawRoles}.Sanitize()
	n.userRoles = pgx.Identifier{n.rawUserRoles}.Sanitize()
	n.roleGrants = pgx.Identifier{n.rawRoleGrants}.Sanitize()
	n.recordUserRoles = pgx.Identifier{n.rawRecordUserRoles}.Sanitize()
	n.recordGroupRoles = pgx.Identifier{n.rawRecordGroupRoles}.Sanitize()

	return n, nil
}

// Store implements access.Store over the application's pgx pool. A role's row and a
// group's row are the locks of what stands on them. A write of a custom role's holding,
// of a grant, of a group's member or of a group's role runs in a read-committed
// transaction that first reads its parent's row for key share, and writes nothing where
// the row is not there. The deletion of a role or of a group runs in a read-committed
// transaction that first locks its row for update and then looks for what stands on it.
// Of a write and a deletion at the same moment, the deletion reads the new row and
// refuses, or the write finds its parent gone. The transactions name read committed
// whatever the database's default: each statement then reads what had committed when it
// started, which a snapshot taken before the wait would not.
type Store struct {
	pool  *pgxpool.Pool
	names tableNames

	// Statements are built once here — table names are identifiers, not bind
	// parameters — so no query text is assembled at call sites.
	sqlInsertRole          string
	sqlLockRoleName        string
	sqlShareHoldingRoles   string
	sqlShareRole           string
	sqlLockRole            string
	sqlReadMeetingRoles    string
	sqlDeleteRole          string
	sqlRoleHasMembers      string
	sqlDeleteEndedMembers  string
	sqlHeldInDomain        string
	sqlEndedInDomain       string
	sqlRoleExists          string
	sqlListRoles           string
	sqlInsertUserRole      string
	sqlListUserAssignments string
	sqlDeleteUserRole      string
	sqlListUserRoles       string
	sqlListUserMemberships string
	sqlListRoleUsers       string
	sqlInsertGrant         string
	sqlDeleteGrant         string
	sqlDeleteGrants        string
	sqlListRoleGrants      string
	sqlReadRoles           string
	sqlReadGrants          string
	sqlReadMemberships     string

	// groups are the statements of the group tables (groups.go).
	groups groupStatements
	// recordRoles are the statements of the record tables (records.go).
	recordRoles recordStatements
}

// New creates a Store on the application's existing pool. It validates the
// naming options and prepares statement text; it does not touch the database.
func New(pool *pgxpool.Pool, opts ...Option) (*Store, error) {
	names, err := resolveNames(opts)
	if err != nil {
		return nil, err
	}

	return &Store{
		pool:  pool,
		names: names,

		sqlInsertRole:         fmt.Sprintf(`insert into %s ("Kind", "Axis", "DomainId", "Role", "Description") values ($1, $2, $3, $4, $5) on conflict do nothing`, names.roles),
		sqlLockRoleName:       `select pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		sqlShareHoldingRoles:  fmt.Sprintf(`select 1 from %s where "Axis" = $2 and "Role" = $4 and (("Kind" = $1 and "DomainId" = $3) or ($1 = '%s' and "Kind" = '%s' and "DomainId" = '')) for key share`, names.roles, policy.KindDomain, policy.KindEvery),
		sqlShareRole:          fmt.Sprintf(`select 1 from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 for key share`, names.roles),
		sqlLockRole:           fmt.Sprintf(`select 1 from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 for update`, names.roles),
		sqlReadMeetingRoles:   fmt.Sprintf(`select "Kind", "Axis", "DomainId", "Role" from %s where "Axis" = $1 and (("Kind" = $2 and "DomainId" = $3) or ($2 <> '%s' and "Kind" = '%s') or ($2 = '%s' and "Kind" = '%s'))`, names.roles, policy.KindGlobal, policy.KindEvery, policy.KindEvery, policy.KindDomain),
		sqlDeleteRole:         fmt.Sprintf(`delete from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4`, names.roles),
		sqlRoleHasMembers:     fmt.Sprintf(`select exists(select 1 from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 and %s)`, names.userRoles, standing("")),
		sqlDeleteEndedMembers: fmt.Sprintf(`delete from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 and %s`, names.userRoles, ended),
		sqlHeldInDomain:       fmt.Sprintf(`select u."DomainId" from %s u where %s and %s order by u."DomainId" limit 1`, names.userRoles, readsEveryDomainRow(names.roles), standing("u.")),
		sqlEndedInDomain:      fmt.Sprintf(`delete from %s u where %s and u.%s`, names.userRoles, readsEveryDomainRow(names.roles), ended),
		sqlRoleExists:         fmt.Sprintf(`select exists(select 1 from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4)`, names.roles),
		sqlListRoles:          fmt.Sprintf(`select "Role", "Description" from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 order by "Role"`, names.roles),
		sqlInsertUserRole: fmt.Sprintf(`insert into %s ("Kind", "Axis", "DomainId", "Role", "UserId", "StartsAt", "EndsAt", "Reason") values ($1, $2, $3, $4, $5, $6, $7, $8)
on conflict ("Kind", "Axis", "DomainId", "Role", "UserId") do update set %s, "Reason" = excluded."Reason"`, names.userRoles, replacePeriod),
		sqlListUserAssignments: fmt.Sprintf(`select "Kind", "Axis", "DomainId", "Role", "StartsAt", "EndsAt", "Reason" from %s where "UserId" = $1 and %s order by "Kind", "Axis", "DomainId", "Role"`, names.userRoles, standing("")),
		sqlDeleteUserRole:      fmt.Sprintf(`delete from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 and "UserId" = $5`, names.userRoles),
		sqlListUserRoles:       fmt.Sprintf(`select "Role" from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "UserId" = $4 and %s order by "Role"`, names.userRoles, counting("")),
		sqlListUserMemberships: fmt.Sprintf(`select "Kind", "Axis", "DomainId", "Role", "StartsAt", "EndsAt" from %s where "UserId" = $1 and %s order by "Kind", "Axis", "DomainId", "Role"`, names.userRoles, counting("")),
		sqlListRoleUsers:       fmt.Sprintf(`select "UserId" from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 and %s order by "UserId"`, names.userRoles, counting("")),
		sqlInsertGrant: fmt.Sprintf(
			`insert into %s ("Kind", "Axis", "DomainId", "Role", "Permission", "Resource", "Field", "Condition") values ($1, $2, $3, $4, $5, $6, $7, $8) on conflict do nothing`, names.roleGrants),
		sqlDeleteGrant: fmt.Sprintf(
			`delete from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 and "Permission" = $5 and "Resource" = $6 and "Field" = $7 and "Condition" = $8`, names.roleGrants),
		sqlDeleteGrants: fmt.Sprintf(
			`delete from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 and "Permission" = $5 and "Resource" = $6 and "Field" = $7`, names.roleGrants),
		sqlListRoleGrants: fmt.Sprintf(
			`select "Permission", "Resource", "Field", "Condition" from %s where "Kind" = $1 and "Axis" = $2 and "DomainId" = $3 and "Role" = $4 order by "Permission", "Resource", "Field", "Condition"`, names.roleGrants),
		sqlReadRoles:       fmt.Sprintf(`select "Kind", "Axis", "DomainId", "Role" from %s`, names.roles),
		sqlReadGrants:      fmt.Sprintf(`select "Kind", "Axis", "DomainId", "Role", "Permission", "Resource", "Field", "Condition" from %s`, names.roleGrants),
		sqlReadMemberships: fmt.Sprintf(`select "Kind", "Axis", "DomainId", "UserId", "Role", "StartsAt", "EndsAt" from %s`, names.userRoles),

		groups:      newGroupStatements(&names),
		recordRoles: newRecordStatements(&names),
	}, nil
}

// An assignment (a person's role, a person's place in a group, a group's role)
// carries a period in two nullable columns, StartsAt and EndsAt, and is read
// in one of three ways at the database's present. It counts from its start
// until its end: every listing of what a person or a group holds reads the
// ones that count, so an assignment outside its period is left out while its
// row is still there. It stands, counting now or later, until its end: a
// deletion that assignments block reads the ones that stand. It has ended once
// its end has passed, never to count again: the deletion takes those rows with
// it, so an assignment nobody removed never stands in a deletion's way.

// counting is the predicate of an assignment that counts now. alias is the
// table's alias with its dot, "" for none.
func counting(alias string) string {
	return fmt.Sprintf(`(%[1]s"StartsAt" is null or %[1]s"StartsAt" <= now()) and %[2]s`, alias, standing(alias))
}

// standing is the predicate of an assignment that has not ended.
func standing(alias string) string {
	return fmt.Sprintf(`(%[1]s"EndsAt" is null or %[1]s"EndsAt" > now())`, alias)
}

const (
	// ended is the predicate of an assignment whose end has passed.
	ended = `"EndsAt" <= now()`
	// replacePeriod is what an assignment of a key that is taken does: the
	// period is replaced and the rest of the row, its CreatedAt among it, left
	// as it was.
	replacePeriod = `"StartsAt" = excluded."StartsAt", "EndsAt" = excluded."EndsAt"`
)

// readsEveryDomainRow is the predicate of a holding, under the alias u, that reads the
// every-domain row of the role $2: one held in one domain of the axis $1 that names the
// role, where the domain holds no row of the exact name in roles, the roles table, of
// its own.
func readsEveryDomainRow(roles string) string {
	return fmt.Sprintf(`u."Kind" = '%s' and u."Axis" = $1 and u."Role" = $2 and not exists (select 1 from %s r where r."Kind" = u."Kind" and r."Axis" = u."Axis" and r."DomainId" = u."DomainId" and r."Role" = u."Role")`,
		policy.KindDomain, roles)
}

// periodOf reassembles an assignment's period from its two columns.
func periodOf(start, end *time.Time) accesstypes.Period {
	var period accesstypes.Period
	if start != nil {
		period.Start = start.UTC()
	}
	if end != nil {
		period.End = end.UTC()
	}

	return period
}

// DDL returns the canonical schema statements for this Store's tables,
// rendered with the configured names. Copy them into the application's
// migration file; the tests execute exactly these statements, so the shipped
// DDL is the tested DDL. Kind is checked in the database against the three
// values the store writes, so a row can never claim a partition the store
// does not read.
func (s *Store) DDL() []string {
	n := s.names
	quote := func(name string) string { return pgx.Identifier{name}.Sanitize() }
	kindCheck := func(rawTable string) string {
		return fmt.Sprintf(`CONSTRAINT %s CHECK ("Kind" IN ('%s', '%s', '%s'))`, quote(rawTable+"Kind"), policy.KindGlobal, policy.KindDomain, policy.KindEvery)
	}

	return append([]string{
		fmt.Sprintf(`CREATE TABLE %s (
  "Kind" TEXT NOT NULL,
  "Axis" TEXT NOT NULL,
  "DomainId" TEXT NOT NULL,
  "Role" TEXT NOT NULL,
  "Description" TEXT NOT NULL,
  "UpdatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("Kind", "Axis", "DomainId", "Role"),
  %s
)`, n.roles, kindCheck(n.rawRoles)),
		fmt.Sprintf(`CREATE TABLE %s (
  "Kind" TEXT NOT NULL,
  "Axis" TEXT NOT NULL,
  "DomainId" TEXT NOT NULL,
  "Role" TEXT NOT NULL,
  "UserId" TEXT NOT NULL,
  "StartsAt" TIMESTAMPTZ,
  "EndsAt" TIMESTAMPTZ,
  "Reason" TEXT,
  "CreatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("Kind", "Axis", "DomainId", "Role", "UserId"),
  %s
)`, n.userRoles, kindCheck(n.rawUserRoles)),
		fmt.Sprintf(`CREATE INDEX %s ON %s ("UserId", "Kind", "Axis", "DomainId")`, quote(n.rawUserIndex), n.userRoles),
		fmt.Sprintf(`CREATE TABLE %s (
  "Kind" TEXT NOT NULL,
  "Axis" TEXT NOT NULL,
  "DomainId" TEXT NOT NULL,
  "Role" TEXT NOT NULL,
  "Permission" TEXT NOT NULL,
  "Resource" TEXT NOT NULL,
  "Field" TEXT NOT NULL,
  "Condition" TEXT NOT NULL,
  "UpdatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("Kind", "Axis", "DomainId", "Role", "Permission", "Resource", "Field", "Condition"),
  FOREIGN KEY ("Kind", "Axis", "DomainId", "Role") REFERENCES %s ("Kind", "Axis", "DomainId", "Role") ON DELETE CASCADE,
  %s
)`, n.roleGrants, n.roles, kindCheck(n.rawRoleGrants)),
	}, slices.Concat(s.groupDDL(), s.recordDDL())...)
}

// ReadPolicy reads roles, grants, memberships, the groups of people and the roles held
// on records inside one repeatable-read transaction, so the row sets observe the same
// store state.
func (s *Store) ReadPolicy(ctx context.Context) (*policy.Records, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.BeginTx()")
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	records := &policy.Records{}

	roleRows, err := tx.Query(ctx, s.sqlReadRoles)
	if err != nil {
		return nil, errors.Wrap(err, "pgx.Tx.Query() roles")
	}
	records.Roles, err = pgx.CollectRows(roleRows, func(row pgx.CollectableRow) (policy.Role, error) {
		var kind, axis, domain string
		var r policy.Role
		if err := row.Scan(&kind, &axis, &domain, &r.Name); err != nil {
			return policy.Role{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return policy.Role{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		r.Scope = scope

		return r, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() roles")
	}

	grantRows, err := tx.Query(ctx, s.sqlReadGrants)
	if err != nil {
		return nil, errors.Wrap(err, "pgx.Tx.Query() grants")
	}
	records.Grants, err = pgx.CollectRows(grantRows, func(row pgx.CollectableRow) (policy.Grant, error) {
		var g policy.Grant
		var kind, axis, domain, role string
		if err := row.Scan(&kind, &axis, &domain, &role, &g.Perm, &g.Resource, &g.Field, &g.Condition); err != nil {
			return policy.Grant{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return policy.Grant{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		g.Scope = scope
		g.Subject = policy.Subject{Kind: policy.SubjectRole, Name: role}

		return g, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() grants")
	}

	memberRows, err := tx.Query(ctx, s.sqlReadMemberships)
	if err != nil {
		return nil, errors.Wrap(err, "pgx.Tx.Query() memberships")
	}
	records.Memberships, err = pgx.CollectRows(memberRows, func(row pgx.CollectableRow) (policy.Membership, error) {
		var m policy.Membership
		var kind, axis, domain, user string
		var start, end *time.Time
		if err := row.Scan(&kind, &axis, &domain, &user, &m.Role, &start, &end); err != nil {
			return policy.Membership{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return policy.Membership{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		m.Scope = scope
		m.Member = policy.Subject{Kind: policy.SubjectUser, Name: user}
		m.Period = periodOf(start, end)

		return m, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() memberships")
	}
	if err := s.readGroups(ctx, tx, records); err != nil {
		return nil, err
	}
	if err := s.readRecords(ctx, tx, records); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, errors.Wrap(err, "pgx.Tx.Commit()")
	}

	return records, nil
}

// InsertUserRole gives one user-role membership its period and its reason: the
// membership is added where the user does not hold the role, and given the new
// period and the new reason where they do, an empty reason as none. The role
// is named by name alone: no role row is required, since the release's default
// roles have none. A record of the act is written with the membership, in one
// transaction (writeUnder); with none it is one statement.
func (s *Store) InsertUserRole(
	ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string, record *auditlog.Record,
) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	var why *string
	if reason != "" {
		why = &reason
	}
	if s.records(record) {
		return s.writeUnder(ctx, "assign user role", s.sqlInsertUserRole, []any{kind, axis, domain, role, user, period.StartPtr(), period.EndPtr(), why}, record)
	}
	if _, err := s.pool.Exec(ctx, s.sqlInsertUserRole, kind, axis, domain, role, user, period.StartPtr(), period.EndPtr(), why); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() assign user role")
	}

	return nil
}

// InsertCustomUserRole writes what InsertUserRole writes, only while the roles table
// holds a row of the custom role where the membership reads it: the scope's own or,
// for one domain, every domain's. It reads those rows for key share before it writes,
// as writeUnder says, and wraps policy.ErrNoRole where none is there.
func (s *Store) InsertCustomUserRole(
	ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string, record *auditlog.Record,
) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	var why *string
	if reason != "" {
		why = &reason
	}

	return s.writeUnder(ctx, "assign user role", s.sqlInsertUserRole, []any{kind, axis, domain, role, user, period.StartPtr(), period.EndPtr(), why}, record, s.customRole(scope, role))
}

// parent is the row, or the rows, a write stands on: the statement that reads them for
// key share, its arguments, and the error the write answers where the statement reads
// no row.
type parent struct {
	sql  string
	args []any
	gone func() error
}

// customRole is the parent of a holding of a custom role in scope: the roles-table rows
// the holding reads, the scope's own and, for one domain, every domain's. One row is
// enough, and every row there is read.
func (s *Store) customRole(scope accesstypes.PolicyScope, role accesstypes.Role) parent {
	kind, axis, domain := policy.ScopeColumns(scope)

	return parent{
		sql:  s.sqlShareHoldingRoles,
		args: []any{kind, axis, domain, role},
		gone: func() error {
			return errors.Wrapf(policy.ErrNoRole, "role %q in scope %s", role, scope)
		},
	}
}

// ownRole is the parent of a grant: the role's own row, (scope, role) exactly. Every
// domain's row does not stand in for one domain's.
func (s *Store) ownRole(scope accesstypes.PolicyScope, role accesstypes.Role) parent {
	kind, axis, domain := policy.ScopeColumns(scope)

	return parent{
		sql:  s.sqlShareRole,
		args: []any{kind, axis, domain, role},
		gone: func() error {
			return errors.Wrapf(policy.ErrNoRole, "role %q in scope %s", role, scope)
		},
	}
}

// writeUnder writes one row that stands on parents, by the insert statement and its
// arguments, in a read-committed transaction whose first statements read the parents'
// rows for key share, in the order given. Where a parent's statement reads no row it
// writes nothing and answers that parent's error. A deletion of a parent locks its row
// for update before it looks for what stands on it, so the two take turns: the deletion
// that comes second reads the row written and refuses, and the write that comes second
// reads no row. The record of the act, nil for none, is written after the row, in the
// same transaction (writeRecord).
func (s *Store) writeUnder(ctx context.Context, what, insert string, args []any, record *auditlog.Record, parents ...parent) error {
	// Read committed whatever the database's default, as InsertRole: the read of a
	// parent that waited on a deletion reads what the deletion committed.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return errors.Wrapf(err, "pgxpool.Pool.BeginTx() %s", what)
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	if err := shareParents(ctx, tx, parents); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, insert, args...); err != nil {
		return errors.Wrapf(err, "pgx.Tx.Exec() %s", what)
	}
	if err := s.writeRecord(ctx, tx, record); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.Wrapf(err, "pgx.Tx.Commit() %s", what)
	}

	return nil
}

// records reports whether the store writes record: one is handed, and the store names
// the audit records table (WithAuditRecords).
func (s *Store) records(record *auditlog.Record) bool {
	return record != nil && s.names.auditRecords != ""
}

// writeRecord inserts record, the record of the act of the write tx makes, in the audit
// records table, inside tx: its columns (auditlog.Record.Columns), and CreatedAt the
// statement's time (auditlog.PostgresInsert). A record the store does not write
// (records) inserts nothing. A record that does not validate is an error, and the act's
// transaction does not commit.
func (s *Store) writeRecord(ctx context.Context, tx pgx.Tx, record *auditlog.Record) error {
	if !s.records(record) {
		return nil
	}
	columns, err := record.Columns()
	if err != nil {
		return errors.Wrap(err, "auditlog.Record.Columns()")
	}
	statement, args := auditlog.PostgresInsert(s.names.auditRecords, columns)
	if _, err := tx.Exec(ctx, statement, args...); err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec() audit record")
	}

	return nil
}

// shareParents reads, inside tx, each parent's rows for key share, in the order given,
// and answers the error of the first parent whose statement reads no row. A select's
// command tag counts the rows it read.
func shareParents(ctx context.Context, tx pgx.Tx, parents []parent) error {
	for _, p := range parents {
		tag, err := tx.Exec(ctx, p.sql, p.args...)
		if err != nil {
			return errors.Wrap(err, "pgx.Tx.Exec() read parent for key share")
		}
		if tag.RowsAffected() == 0 {
			return p.gone()
		}
	}

	return nil
}

// DeleteUserRole removes one user-role membership; removing an absent
// membership is a no-op. A record of the act is written in the removal's transaction,
// after a delete that removed a row: a membership that is not there is removed by
// nobody, and no record is written. With no record it is one statement.
func (s *Store) DeleteUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, record *auditlog.Record) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	if !s.records(record) {
		if _, err := s.pool.Exec(ctx, s.sqlDeleteUserRole, kind, axis, domain, role, user); err != nil {
			return errors.Wrap(err, "pgxpool.Pool.Exec() delete user role")
		}

		return nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return errors.Wrap(err, "pgxpool.Pool.BeginTx() delete user role")
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()
	tag, err := tx.Exec(ctx, s.sqlDeleteUserRole, kind, axis, domain, role, user)
	if err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec() delete user role")
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if err := s.writeRecord(ctx, tx, record); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.Wrap(err, "pgx.Tx.Commit() delete user role")
	}

	return nil
}

// ListUserRoles returns the roles the user holds in scope now, sorted.
func (s *Store) ListUserRoles(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User) ([]accesstypes.Role, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	rows, err := s.pool.Query(ctx, s.sqlListUserRoles, kind, axis, domain, user)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() user roles")
	}
	roles, err := pgx.CollectRows(rows, pgx.RowTo[accesstypes.Role])
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() user roles")
	}

	return roles, nil
}

// ListUserMemberships returns every membership the user holds now, wherever
// it is held, read through the user index and sorted by scope then role.
func (s *Store) ListUserMemberships(ctx context.Context, user accesstypes.User) ([]policy.Membership, error) {
	rows, err := s.pool.Query(ctx, s.sqlListUserMemberships, user)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() user memberships")
	}
	memberships, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.Membership, error) {
		var kind, axis, domain string
		var start, end *time.Time
		m := policy.Membership{Member: policy.Subject{Kind: policy.SubjectUser, Name: string(user)}}
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
		return nil, errors.Wrap(err, "pgx.CollectRows() user memberships")
	}

	return memberships, nil
}

// ListUserAssignments returns every membership of the user's own that has not
// ended, wherever it is held, each with its period and its reason, sorted by
// scope then role.
func (s *Store) ListUserAssignments(ctx context.Context, user accesstypes.User) ([]policy.Assignment, error) {
	rows, err := s.pool.Query(ctx, s.sqlListUserAssignments, user)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() user assignments")
	}
	assignments, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.Assignment, error) {
		var kind, axis, domain string
		var start, end *time.Time
		var reason *string
		a := policy.Assignment{Membership: policy.Membership{Member: policy.Subject{Kind: policy.SubjectUser, Name: string(user)}}}
		if err := row.Scan(&kind, &axis, &domain, &a.Role, &start, &end, &reason); err != nil {
			return policy.Assignment{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return policy.Assignment{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		a.Scope = scope
		a.Period = periodOf(start, end)
		if reason != nil {
			a.Reason = *reason
		}

		return a, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() user assignments")
	}

	return assignments, nil
}

// ListRoleUsers returns the people who hold the role in scope now, sorted.
func (s *Store) ListRoleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]accesstypes.User, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	rows, err := s.pool.Query(ctx, s.sqlListRoleUsers, kind, axis, domain, role)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() role users")
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[accesstypes.User])
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() role users")
	}

	return users, nil
}

// InsertRole creates the (scope, role) row with its description;
// re-inserting an existing role is a no-op that leaves the stored description
// as it is. A name that differs only in case from a custom role held where the
// two are read side by side is refused with a *policy.CaseTwinError. The read of
// the roles the name is held to and the insert are one transaction, which first
// takes an advisory lock keyed by the roles table and the folded name: two
// inserts of names alike but for case, in whatever scope, take turns, and the
// second reads the first's row.
func (s *Store) InsertRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, description string) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	// Read committed whatever the database's default: each statement reads what
	// has committed when it starts, so the read after the lock sees the row of
	// the insert that held the lock before it.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return errors.Wrap(err, "pgxpool.Pool.BeginTx() insert role")
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	if _, err := tx.Exec(ctx, s.sqlLockRoleName, s.names.rawRoles, policy.FoldRoleName(string(role))); err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec() lock role name")
	}
	held, err := s.meetingRoles(ctx, tx, kind, axis, domain)
	if err != nil {
		return err
	}
	if slices.Contains(held, policy.Role{Scope: scope, Name: role}) {
		return nil
	}
	if twin, found := policy.CaseTwin(held, scope, role); found {
		return errors.Wrapf(&policy.CaseTwinError{Held: twin}, "role %q in scope %s", role, scope)
	}
	if _, err := tx.Exec(ctx, s.sqlInsertRole, kind, axis, domain, role, description); err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec() insert role")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.Wrap(err, "pgx.Tx.Commit() insert role")
	}

	return nil
}

// meetingRoles reads, inside tx, the custom roles a new name held where the
// columns say could meet (policy.RoleNamesMeet): those of the same place, every
// domain's for one domain, and every domain's and each domain's for every
// domain.
func (s *Store) meetingRoles(ctx context.Context, tx pgx.Tx, kind, axis, domain string) ([]policy.Role, error) {
	rows, err := tx.Query(ctx, s.sqlReadMeetingRoles, axis, kind, domain)
	if err != nil {
		return nil, errors.Wrap(err, "pgx.Tx.Query() roles")
	}
	roles, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.Role, error) {
		var rowKind, rowAxis, rowDomain string
		var r policy.Role
		if err := row.Scan(&rowKind, &rowAxis, &rowDomain, &r.Name); err != nil {
			return policy.Role{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.ScopeFromColumns(rowKind, rowAxis, rowDomain)
		if err != nil {
			return policy.Role{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		r.Scope = scope

		return r, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() roles")
	}

	return roles, nil
}

// ListRoles returns the scope's roles with their descriptions, sorted by
// name.
func (s *Store) ListRoles(ctx context.Context, scope accesstypes.PolicyScope) ([]policy.RoleRow, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	rows, err := s.pool.Query(ctx, s.sqlListRoles, kind, axis, domain)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() roles")
	}
	roles, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.RoleRow, error) {
		var r policy.RoleRow
		if err := row.Scan(&r.Name, &r.Description); err != nil {
			return policy.RoleRow{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}

		return r, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() roles")
	}

	return roles, nil
}

// DeleteRole deletes the (scope, role) row. Its grants cascade in the
// database. Memberships held in the scope that have not ended block the
// delete, and so does a group that holds the role there, now or for a period
// that has not begun: each is checked in the delete's own transaction, since
// nothing in the schema ties a membership or a group's role to a role row. The
// ended ones, a person's and a group's, go with the role. The transaction first
// locks the role's row for update, which a holding of the role written by the
// store, or by a transaction that reads the row for key share, waits on and holds
// a lock against: of such a holding and the delete at the same moment, the checks
// read the holding and refuse, or the holding finds the row gone. Before it
// removes a group's ended roles it reads the rows of the groups that hold them for
// key share, as a write under a group does, so it takes its turn with a DeleteGroup
// of one of them.
func (s *Store) DeleteRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	// Read committed whatever the database's default, as InsertRole: the checks after
	// the lock see the holding of the transaction that held the row before it.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, errors.Wrap(err, "pgxpool.Pool.BeginTx() delete role")
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	// A role with no row locks nothing: the checks below run all the same, and the
	// delete answers false where they pass.
	if _, err := tx.Exec(ctx, s.sqlLockRole, kind, axis, domain, role); err != nil {
		return false, errors.Wrap(err, "pgx.Tx.Exec() lock role")
	}

	var hasMembers bool
	if err := tx.QueryRow(ctx, s.sqlRoleHasMembers, kind, axis, domain, role).Scan(&hasMembers); err != nil {
		return false, errors.Wrap(err, "pgx.Tx.QueryRow() role members")
	}
	if hasMembers {
		return false, errors.Wrapf(policy.ErrStillAssigned, "role %q in scope %s still has members", role, scope)
	}
	var held bool
	if err := tx.QueryRow(ctx, s.groups.roleHeld, kind, axis, domain, role).Scan(&held); err != nil {
		return false, errors.Wrap(err, "pgx.Tx.QueryRow() role groups")
	}
	if held {
		return false, errors.Wrapf(policy.ErrHeldByGroup, "role %q in scope %s", role, scope)
	}
	if scope.IsEveryDomain() {
		if err := s.refuseHeldInDomain(ctx, tx, axis, role); err != nil {
			return false, err
		}
	}
	tag, err := tx.Exec(ctx, s.sqlDeleteRole, kind, axis, domain, role)
	if err != nil {
		return false, errors.Wrap(err, "pgx.Tx.Exec() delete role")
	}
	if tag.RowsAffected() > 0 {
		// The memberships of the role that have ended go with it. Where no row of
		// the role was there the name may be a default role's, whose memberships
		// are not this delete's to touch.
		if _, err := tx.Exec(ctx, s.sqlDeleteEndedMembers, kind, axis, domain, role); err != nil {
			return false, errors.Wrap(err, "pgx.Tx.Exec() ended memberships")
		}
		// The groups whose ended roles go next are read for key share first, as a
		// write under a group reads its row. A DeleteGroup locks its group's row for
		// update before it removes its ended roles in one statement, where this delete
		// of a role held in every domain removes them in two: the two take turns on
		// the group's row, and neither holds a group's role the other waits for.
		if _, err := tx.Exec(ctx, s.groups.shareEndedOf, role); err != nil {
			return false, errors.Wrap(err, "pgx.Tx.Exec() groups of ended roles")
		}
		if _, err := tx.Exec(ctx, s.groups.deleteEndedOf, kind, axis, domain, role); err != nil {
			return false, errors.Wrap(err, "pgx.Tx.Exec() ended group roles")
		}
		if scope.IsEveryDomain() {
			for _, sql := range []string{s.sqlEndedInDomain, s.groups.endedInDomain} {
				if _, err := tx.Exec(ctx, sql, axis, role); err != nil {
					return false, errors.Wrap(err, "pgx.Tx.Exec() ended holdings in one domain")
				}
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, errors.Wrap(err, "pgx.Tx.Commit() delete role")
	}

	return tag.RowsAffected() > 0, nil
}

// refuseHeldInDomain refuses, inside the transaction of a deletion of a role held in
// every domain, while a person's or a group's holding in one domain reads the role's
// row: one that names the role, has not ended, and whose domain holds no role of the
// name of its own. The refusal wraps policy.ErrHeldInDomain and names the lowest such
// domain, a person's before a group's.
func (s *Store) refuseHeldInDomain(ctx context.Context, tx pgx.Tx, axis string, role accesstypes.Role) error {
	for _, holder := range []struct {
		sql, who string
	}{
		{sql: s.sqlHeldInDomain, who: "a person"},
		{sql: s.groups.heldInDomain, who: "a group"},
	} {
		var domain string
		switch err := tx.QueryRow(ctx, holder.sql, axis, role).Scan(&domain); {
		case err == nil:
			return errors.Wrapf(policy.ErrHeldInDomain, "role %q: %s holds it in the domain %q", role, holder.who, domain)
		case !errors.Is(err, pgx.ErrNoRows):
			return errors.Wrap(err, "pgx.Tx.QueryRow() holdings in one domain")
		}
	}

	return nil
}

// RoleExists reports whether the (scope, role) row exists.
func (s *Store) RoleExists(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	var exists bool
	if err := s.pool.QueryRow(ctx, s.sqlRoleExists, kind, axis, domain, role).Scan(&exists); err != nil {
		return false, errors.Wrap(err, "pgxpool.Pool.QueryRow() role exists")
	}

	return exists, nil
}

// InsertGrant adds one grant row; the condition is part of the row's identity
// ("" = unconditional), so re-inserting an existing row is a no-op and a
// different condition on the same (permission, resource, field) is a second
// row. It is ChangeGrants with the one addition: the (scope, role) parent row
// must be there, and where it is not, policy.ErrNoRole is wrapped.
func (s *Store) InsertGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field, condition string) error {
	return s.ChangeGrants(ctx, scope, role, nil, []policy.RoleGrant{{Perm: perm, Resource: resource, Field: field, Condition: condition}})
}

// InsertGrants adds the role's grant rows as one write: a change with no
// removals. The (scope, role) parent row must exist, and where it does not,
// policy.ErrNoRole is wrapped.
func (s *Store) InsertGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, grants []policy.RoleGrant) error {
	return s.ChangeGrants(ctx, scope, role, nil, grants)
}

// ChangeGrants removes the role's removals and adds its additions in one
// read-committed transaction: a failure rolls the whole change back. Each insert
// ignores a conflict, so present rows are untouched and the additions are
// idempotent. Where there are additions, the transaction first reads the (scope,
// role) parent row for key share, as writeUnder reads a parent, and where it is not
// there wraps policy.ErrNoRole and writes nothing; the change then goes as one
// batched round trip.
func (s *Store) ChangeGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, removals, additions []policy.RoleGrant) error {
	if len(removals) == 0 && len(additions) == 0 {
		return nil
	}

	kind, axis, domain := policy.ScopeColumns(scope)
	batch := &pgx.Batch{}
	for _, g := range removals {
		batch.Queue(s.sqlDeleteGrant, kind, axis, domain, role, g.Perm, g.Resource, g.Field, g.Condition)
	}
	for _, g := range additions {
		batch.Queue(s.sqlInsertGrant, kind, axis, domain, role, g.Perm, g.Resource, g.Field, g.Condition)
	}

	// Read committed whatever the database's default, as writeUnder.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return errors.Wrap(err, "pgxpool.Pool.BeginTx() change grants")
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	if len(additions) > 0 {
		if err := shareParents(ctx, tx, []parent{s.ownRole(scope, role)}); err != nil {
			return err
		}
	}
	results := tx.SendBatch(ctx, batch)
	for range batch.Len() {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()

			return errors.Wrap(err, "pgx.BatchResults.Exec() change grants")
		}
	}
	if err := results.Close(); err != nil {
		return errors.Wrap(err, "pgx.BatchResults.Close() change grants")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.Wrap(err, "pgx.Tx.Commit() change grants")
	}

	return nil
}

// DeleteGrant removes one grant row; removing an absent grant is a no-op.
func (s *Store) DeleteGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field, condition string) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	if _, err := s.pool.Exec(ctx, s.sqlDeleteGrant, kind, axis, domain, role, perm, resource, field, condition); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() delete grant")
	}

	return nil
}

// DeleteGrants removes every condition's row for the (permission, resource,
// field); removing absent rows is a no-op.
func (s *Store) DeleteGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field string) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	if _, err := s.pool.Exec(ctx, s.sqlDeleteGrants, kind, axis, domain, role, perm, resource, field); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() delete grants")
	}

	return nil
}

// ListRoleGrants returns the role's grant rows in scope, sorted.
func (s *Store) ListRoleGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]policy.RoleGrant, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	rows, err := s.pool.Query(ctx, s.sqlListRoleGrants, kind, axis, domain, role)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() role grants")
	}
	grants, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.RoleGrant, error) {
		var g policy.RoleGrant
		if err := row.Scan(&g.Perm, &g.Resource, &g.Field, &g.Condition); err != nil {
			return policy.RoleGrant{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}

		return g, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() role grants")
	}

	return grants, nil
}
