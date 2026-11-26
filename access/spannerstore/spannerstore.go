// Package spannerstore implements access.Store over Cloud Spanner.
//
// Its sibling github.com/impulseframework/impulse/access/postgresstore serves
// PostgreSQL-backed deployments through the application's pgx pool.
//
// Each Store owns three tables named {Prefix}{Store}{Roles|UserRoles|
// RoleGrants} — defaults yield AccessRoles, AccessUserRoles, AccessRoleGrants —
// and the three of the groups of people (groups.go).
// Rows are partitioned by where the policy is held, persisted as the
// structural column triple (Kind, Axis, DomainId): Kind names the global
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
// runs DDL — Spanner schema changes are slow admin-API operations and apps own
// their schema lifecycle).
package spannerstore

import (
	"context"
	"fmt"
	"slices"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/internal/spannertxn"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
)

var _ access.Store = (*Store)(nil)

// Column and query-parameter names shared by the prepared statements and
// mutations.
const (
	colKind        = "Kind"
	colAxis        = "Axis"
	colDomain      = "DomainId"
	colRole        = "Role"
	colDescription = "Description"
	colUser        = "UserId"
	colPermission  = "Permission"
	colResource    = "Resource"
	colField       = "Field"
	colCondition   = "Condition"
	colStartsAt    = "StartsAt"
	colEndsAt      = "EndsAt"
	colReason      = "Reason"
	colCreatedAt   = "CreatedAt"
	colUpdatedAt   = "UpdatedAt"

	paramKind   = "kind"
	paramAxis   = "axis"
	paramDomain = "domain"
	paramRole   = "role"
	paramUser   = "user"
)

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

type tableNames struct {
	roles      string
	userRoles  string
	roleGrants string
	userIndex  string

	groups            string
	groupMembers      string
	groupRoles        string
	groupsByName      string
	groupMembersIndex string
	groupRolesIndex   string

	// auditRecords is the audit records table (WithAuditRecords), "" for none.
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

	return tableNames{
		roles:      tables.Roles,
		userRoles:  tables.UserRoles,
		roleGrants: tables.RoleGrants,
		userIndex:  tables.UserRolesByUserID,

		groups:            tables.Groups,
		groupMembers:      tables.GroupMembers,
		groupRoles:        tables.GroupRoles,
		groupsByName:      tables.GroupsByName,
		groupMembersIndex: tables.GroupMembersByUserID,
		groupRolesIndex:   tables.GroupRolesByScope,

		auditRecords: c.auditRecords,
	}, nil
}

// Store implements access.Store over a Spanner client. Its read-write transactions run at
// the serializable isolation level (spannertxn.Serializable), whatever level the application
// chose for its client. Several of them read and then write another row, and rely on Spanner
// serializing the read with what another transaction writes: a role's insert with its
// twin's, a role's or a group's deletion with a membership given at the same moment, a
// custom role's holding with the role's deletion. At repeatable read, which checks only
// two writes to one row, both would commit.
type Store struct {
	client *spanner.Client
	names  tableNames

	// Statements are built once here — table names are identifiers, not bind
	// parameters — so no query text is assembled at call sites.
	sqlDeleteRole          string
	sqlRoleHasMembers      string
	sqlDeleteEndedMembers  string
	sqlHeldInDomain        string
	sqlEndedInDomain       string
	sqlRoleExists          string
	sqlListRoles           string
	sqlReadMeetingRoles    string
	sqlListUserRoles       string
	sqlListUserMemberships string
	sqlListUserAssignments string
	sqlListRoleUsers       string
	sqlListRoleGrants      string
	sqlReadRoles           string
	sqlReadGrants          string
	sqlReadMemberships     string

	// groups are the statements of the group tables (groups.go).
	groups groupStatements
}

// New creates a Store on the given Spanner client. It validates the naming
// options and prepares statement text; it does not touch the database.
func New(client *spanner.Client, opts ...Option) (*Store, error) {
	names, err := resolveNames(opts)
	if err != nil {
		return nil, err
	}

	return &Store{
		client: client,
		names:  names,

		sqlDeleteRole:          fmt.Sprintf("DELETE FROM %s WHERE Kind = @kind AND Axis = @axis AND DomainId = @domain AND Role = @role", names.roles),
		sqlRoleHasMembers:      fmt.Sprintf("SELECT 1 FROM %s WHERE Kind = @kind AND Axis = @axis AND DomainId = @domain AND Role = @role AND %s LIMIT 1", names.userRoles, standing("")),
		sqlDeleteEndedMembers:  fmt.Sprintf("DELETE FROM %s WHERE Kind = @kind AND Axis = @axis AND DomainId = @domain AND Role = @role AND %s", names.userRoles, ended),
		sqlHeldInDomain:        fmt.Sprintf("SELECT u.DomainId FROM %s u WHERE %s AND %s ORDER BY u.DomainId LIMIT 1", names.userRoles, readsEveryDomainRow(names.roles), standing("u.")),
		sqlEndedInDomain:       fmt.Sprintf("DELETE FROM %s u WHERE %s AND u.%s", names.userRoles, readsEveryDomainRow(names.roles), ended),
		sqlRoleExists:          fmt.Sprintf("SELECT 1 FROM %s WHERE Kind = @kind AND Axis = @axis AND DomainId = @domain AND Role = @role", names.roles),
		sqlListRoles:           fmt.Sprintf("SELECT Role, Description FROM %s WHERE Kind = @kind AND Axis = @axis AND DomainId = @domain ORDER BY Role", names.roles),
		sqlReadMeetingRoles:    fmt.Sprintf("SELECT Kind, Axis, DomainId, Role FROM %s WHERE Axis = @axis AND ((Kind = @kind AND DomainId = @domain) OR (@kind != '%s' AND Kind = '%s') OR (@kind = '%s' AND Kind = '%s'))", names.roles, policy.KindGlobal, policy.KindEvery, policy.KindEvery, policy.KindDomain),
		sqlListUserRoles:       fmt.Sprintf("SELECT Role FROM %s WHERE Kind = @kind AND Axis = @axis AND DomainId = @domain AND UserId = @user AND %s ORDER BY Role", names.userRoles, counting("")),
		sqlListUserMemberships: fmt.Sprintf("SELECT Kind, Axis, DomainId, Role, StartsAt, EndsAt FROM %s@{FORCE_INDEX=%s} WHERE UserId = @user AND %s ORDER BY Kind, Axis, DomainId, Role", names.userRoles, names.userIndex, counting("")),
		sqlListUserAssignments: fmt.Sprintf("SELECT Kind, Axis, DomainId, Role, StartsAt, EndsAt, Reason FROM %s@{FORCE_INDEX=%s} WHERE UserId = @user AND %s ORDER BY Kind, Axis, DomainId, Role", names.userRoles, names.userIndex, standing("")),
		sqlListRoleUsers:       fmt.Sprintf("SELECT UserId FROM %s WHERE Kind = @kind AND Axis = @axis AND DomainId = @domain AND Role = @role AND %s ORDER BY UserId", names.userRoles, counting("")),
		sqlListRoleGrants:      fmt.Sprintf("SELECT Permission, Resource, Field, Condition FROM %s WHERE Kind = @kind AND Axis = @axis AND DomainId = @domain AND Role = @role ORDER BY Permission, Resource, Field, Condition", names.roleGrants),
		sqlReadRoles:           fmt.Sprintf("SELECT Kind, Axis, DomainId, Role FROM %s", names.roles),
		sqlReadGrants:          fmt.Sprintf("SELECT Kind, Axis, DomainId, Role, Permission, Resource, Field, Condition FROM %s", names.roleGrants),
		sqlReadMemberships:     fmt.Sprintf("SELECT Kind, Axis, DomainId, UserId, Role, StartsAt, EndsAt FROM %s", names.userRoles),

		groups: newGroupStatements(&names),
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
	return fmt.Sprintf("(%[1]sStartsAt IS NULL OR %[1]sStartsAt <= CURRENT_TIMESTAMP()) AND %[2]s", alias, standing(alias))
}

// standing is the predicate of an assignment that has not ended.
func standing(alias string) string {
	return fmt.Sprintf("(%[1]sEndsAt IS NULL OR %[1]sEndsAt > CURRENT_TIMESTAMP())", alias)
}

// ended is the predicate of an assignment whose end has passed.
const ended = "EndsAt <= CURRENT_TIMESTAMP()"

// readsEveryDomainRow is the predicate of a holding, under the alias u, that reads the
// every-domain row of @role: one held in one domain of @axis that names the role, where
// the domain holds no row of the exact name in roles, the roles table, of its own.
func readsEveryDomainRow(roles string) string {
	return fmt.Sprintf("u.Kind = '%s' AND u.Axis = @axis AND u.Role = @role AND NOT EXISTS (SELECT 1 FROM %s r WHERE r.Kind = u.Kind AND r.Axis = u.Axis AND r.DomainId = u.DomainId AND r.Role = u.Role)",
		policy.KindDomain, roles)
}

// periodOf reassembles an assignment's period from its two columns.
func periodOf(start, end spanner.NullTime) accesstypes.Period {
	var period accesstypes.Period
	if start.Valid {
		period.Start = start.Time.UTC()
	}
	if end.Valid {
		period.End = end.Time.UTC()
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
	kindCheck := func(table string) string {
		return fmt.Sprintf("CONSTRAINT %sKind CHECK (Kind IN ('%s', '%s', '%s'))", table, policy.KindGlobal, policy.KindDomain, policy.KindEvery)
	}

	return append([]string{
		fmt.Sprintf(`CREATE TABLE %s (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  Description STRING(MAX) NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  %s,
) PRIMARY KEY (Kind, Axis, DomainId, Role)`, n.roles, kindCheck(n.roles)),
		fmt.Sprintf(`CREATE TABLE %s (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  UserId STRING(320) NOT NULL,
  StartsAt TIMESTAMP,
  EndsAt TIMESTAMP,
  Reason STRING(MAX),
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  %s,
) PRIMARY KEY (Kind, Axis, DomainId, Role, UserId)`, n.userRoles, kindCheck(n.userRoles)),
		fmt.Sprintf(`CREATE INDEX %s ON %s (UserId, Kind, Axis, DomainId)`, n.userIndex, n.userRoles),
		fmt.Sprintf(`CREATE TABLE %s (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  DomainId STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  Permission STRING(64) NOT NULL,
  Resource STRING(128) NOT NULL,
  Field STRING(128) NOT NULL,
  Condition STRING(MAX) NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  %s,
) PRIMARY KEY (Kind, Axis, DomainId, Role, Permission, Resource, Field, Condition),
  INTERLEAVE IN PARENT %s ON DELETE CASCADE`, n.roleGrants, kindCheck(n.roleGrants), n.roles),
	}, s.groupDDL()...)
}

// ReadPolicy reads roles, grants, memberships and the groups of people inside
// one read-only transaction, so the row sets observe the same store state.
func (s *Store) ReadPolicy(ctx context.Context) (*policy.Records, error) {
	txn := s.client.ReadOnlyTransaction()
	defer txn.Close()

	records := &policy.Records{}

	err := txn.Query(ctx, spanner.Statement{SQL: s.sqlReadRoles}).Do(func(row *spanner.Row) error {
		var kind, axis, domain, role string
		if err := row.Columns(&kind, &axis, &domain, &role); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		records.Roles = append(records.Roles, policy.Role{Scope: scope, Name: accesstypes.Role(role)})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.ReadOnlyTransaction.Query() roles")
	}

	err = txn.Query(ctx, spanner.Statement{SQL: s.sqlReadGrants}).Do(func(row *spanner.Row) error {
		var kind, axis, domain, role, perm, resource, field, condition string
		if err := row.Columns(&kind, &axis, &domain, &role, &perm, &resource, &field, &condition); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		records.Grants = append(records.Grants, policy.Grant{
			Scope:     scope,
			Subject:   policy.Subject{Kind: policy.SubjectRole, Name: role},
			Perm:      accesstypes.Permission(perm),
			Resource:  resource,
			Field:     field,
			Condition: condition,
		})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.ReadOnlyTransaction.Query() grants")
	}

	err = txn.Query(ctx, spanner.Statement{SQL: s.sqlReadMemberships}).Do(func(row *spanner.Row) error {
		var kind, axis, domain, user, role string
		var start, end spanner.NullTime
		if err := row.Columns(&kind, &axis, &domain, &user, &role, &start, &end); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		records.Memberships = append(records.Memberships, policy.Membership{
			Scope:  scope,
			Member: policy.Subject{Kind: policy.SubjectUser, Name: user},
			Role:   accesstypes.Role(role),
			Period: periodOf(start, end),
		})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.ReadOnlyTransaction.Query() memberships")
	}
	if err := s.readGroups(ctx, txn, records); err != nil {
		return nil, err
	}

	return records, nil
}

// given is one column an assignment is given beside its period, with the value
// it is given: a membership's reason.
type given struct {
	column string
	value  any
}

// requirement is a row an assignment stands on, read in the assignment's transaction
// before the assignment's own row: it answers an error where the row is not there, and
// the assignment writes nothing.
type requirement func(ctx context.Context, txn *spanner.ReadWriteTransaction) error

// customRoleHeld is the requirement of a custom role's holding in scope: a row of the
// role where the holding reads it, the scope's own or, for one domain, every domain's.
// Where neither is there it answers policy.ErrNoRole wrapped.
func (s *Store) customRoleHeld(scope accesstypes.PolicyScope, role accesstypes.Role) requirement {
	scopes := []accesstypes.PolicyScope{scope}
	if _, oneDomain := scope.Domain(); oneDomain {
		scopes = append(scopes, accesstypes.EveryDomainPolicyScope())
	}

	return func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		for _, at := range scopes {
			kind, axis, domain := policy.ScopeColumns(at)
			switch _, err := txn.ReadRow(ctx, s.names.roles, spanner.Key{kind, axis, domain, string(role)}, []string{colRole}); {
			case err == nil:
				return nil
			case spanner.ErrCode(err) != codes.NotFound:
				return errors.Wrap(err, "spanner.ReadWriteTransaction.ReadRow() role")
			}
		}

		return errors.Wrapf(policy.ErrNoRole, "role %q in scope %s", role, scope)
	}
}

// assign writes one assignment row with its period and what else it is given
// with: the row is inserted where its key is free, and where it is taken the
// period and the columns given with it are replaced and the rest of the row,
// its CreatedAt among it, left as it was. keyColumns and key name the row; the
// read and the write share one transaction, so two assignments of one key
// settle on one period. Each of needs is read first in the same transaction: the
// first that answers an error is answered, and nothing is written. The record of
// the act is written with the row (auditMutations).
func (s *Store) assign(
	ctx context.Context, table string, keyColumns []string, key spanner.Key, period accesstypes.Period, needs []requirement, record *auditlog.Record, wrap string,
	with ...given,
) error {
	audit, err := s.auditMutations(record)
	if err != nil {
		return err
	}
	values := append(slices.Clone([]any(key)), period.StartPtr(), period.EndPtr())
	columns := append(slices.Clone(keyColumns), colStartsAt, colEndsAt)
	for _, g := range with {
		columns = append(columns, g.column)
		values = append(values, g.value)
	}
	_, err = spannertxn.Serializable(ctx, s.client, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		for _, need := range needs {
			if err := need(ctx, txn); err != nil {
				return err
			}
		}
		var m *spanner.Mutation
		switch _, err := txn.ReadRow(ctx, table, key, keyColumns[:1]); {
		case err == nil:
			m = spanner.Update(table, columns, values)
		case spanner.ErrCode(err) == codes.NotFound:
			m = spanner.Insert(table, append(columns, colCreatedAt), append(values, spanner.CommitTimestamp))
		default:
			return errors.Wrap(err, "spanner.ReadWriteTransaction.ReadRow()")
		}

		if err := txn.BufferWrite(append([]*spanner.Mutation{m}, audit...)); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
		}

		return nil
	})
	if err != nil {
		return errors.Wrap(err, wrap)
	}

	return nil
}

// InsertUserRole gives one user-role membership its period and its reason: the
// membership is added where the user does not hold the role, and given the new
// period and the new reason where they do, an empty reason as none. The role
// is named by name alone: no role row is required, since the release's default
// roles have none.
func (s *Store) InsertUserRole(
	ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string, record *auditlog.Record,
) error {
	return s.assignUserRole(ctx, scope, user, role, period, reason, nil, record)
}

// InsertCustomUserRole writes what InsertUserRole writes, only while the roles table
// holds a row of the custom role where the membership reads it: the scope's own or,
// for one domain, every domain's. The read of that row and the write are one
// read-write transaction at the serializable isolation level, so a DeleteRole of the
// row at the same moment either reads the membership and refuses, or deletes the row
// and this transaction, run again, finds it gone and wraps policy.ErrNoRole.
func (s *Store) InsertCustomUserRole(
	ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string, record *auditlog.Record,
) error {
	return s.assignUserRole(ctx, scope, user, role, period, reason, []requirement{s.customRoleHeld(scope, role)}, record)
}

// assignUserRole writes one user-role membership with its period and its reason
// once each of needs is there, and the record of the act with it.
func (s *Store) assignUserRole(
	ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string, needs []requirement,
	record *auditlog.Record,
) error {
	kind, axis, domain := policy.ScopeColumns(scope)

	return s.assign(ctx, s.names.userRoles,
		[]string{colKind, colAxis, colDomain, colRole, colUser},
		spanner.Key{kind, axis, domain, string(role), string(user)},
		period, needs, record, "spannertxn.Serializable() assign user role",
		given{column: colReason, value: spanner.NullString{StringVal: reason, Valid: reason != ""}})
}

// DeleteUserRole removes one user-role membership; removing an absent
// membership is a no-op. The record of the act is written with the removal, in its
// transaction, which reads the membership first: a membership that is not there is
// removed by nobody, and no record is written.
func (s *Store) DeleteUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, record *auditlog.Record) error {
	audit, err := s.auditMutations(record)
	if err != nil {
		return err
	}
	kind, axis, domain := policy.ScopeColumns(scope)
	key := spanner.Key{kind, axis, domain, string(role), string(user)}
	m := spanner.Delete(s.names.userRoles, key)
	if len(audit) == 0 {
		if _, err := s.client.Apply(ctx, []*spanner.Mutation{m}); err != nil {
			return errors.Wrap(err, "spanner.Client.Apply() delete user role")
		}

		return nil
	}
	_, err = spannertxn.Serializable(ctx, s.client, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		switch _, err := txn.ReadRow(ctx, s.names.userRoles, key, []string{colRole}); {
		case spanner.ErrCode(err) == codes.NotFound:
			return nil
		case err != nil:
			return errors.Wrap(err, "spanner.ReadWriteTransaction.ReadRow()")
		}
		if err := txn.BufferWrite(append([]*spanner.Mutation{m}, audit...)); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
		}

		return nil
	})
	if err != nil {
		return errors.Wrap(err, "spannertxn.Serializable() delete user role")
	}

	return nil
}

// auditMutations answers the mutation that writes record, the record of the act of the
// write it goes with, in the audit records table (WithAuditRecords): its columns
// (auditlog.Record.Columns) and CreatedAt the commit's time. A nil record, or a store
// that names no table, writes none. A record that does not validate is an error, and
// the act writes nothing.
func (s *Store) auditMutations(record *auditlog.Record) ([]*spanner.Mutation, error) {
	if record == nil || s.names.auditRecords == "" {
		return nil, nil
	}
	columns, err := record.Columns()
	if err != nil {
		return nil, errors.Wrap(err, "auditlog.Record.Columns()")
	}
	columns[colCreatedAt] = spanner.CommitTimestamp

	return []*spanner.Mutation{spanner.InsertMap(s.names.auditRecords, columns)}, nil
}

// ListUserRoles returns the roles the user holds in scope now, sorted.
func (s *Store) ListUserRoles(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User) ([]accesstypes.Role, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	stmt := spanner.Statement{SQL: s.sqlListUserRoles, Params: map[string]any{paramKind: kind, paramAxis: axis, paramDomain: domain, paramUser: string(user)}}
	values, err := s.queryStrings(ctx, stmt)
	if err != nil {
		return nil, errors.Wrap(err, "user roles")
	}
	roles := make([]accesstypes.Role, 0, len(values))
	for _, v := range values {
		roles = append(roles, accesstypes.Role(v))
	}

	return roles, nil
}

// ListUserMemberships returns every membership the user holds now, wherever
// it is held, read through the user index and sorted by scope then role.
func (s *Store) ListUserMemberships(ctx context.Context, user accesstypes.User) ([]policy.Membership, error) {
	stmt := spanner.Statement{SQL: s.sqlListUserMemberships, Params: map[string]any{paramUser: string(user)}}
	memberships := make([]policy.Membership, 0)
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
		memberships = append(memberships, policy.Membership{
			Scope:  scope,
			Member: policy.Subject{Kind: policy.SubjectUser, Name: string(user)},
			Role:   accesstypes.Role(role),
			Period: periodOf(start, end),
		})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.Client.Single().Query() user memberships")
	}

	return memberships, nil
}

// ListUserAssignments returns every membership of the user's own that has not
// ended, wherever it is held, each with its period and its reason, read through
// the user index and sorted by scope then role.
func (s *Store) ListUserAssignments(ctx context.Context, user accesstypes.User) ([]policy.Assignment, error) {
	stmt := spanner.Statement{SQL: s.sqlListUserAssignments, Params: map[string]any{paramUser: string(user)}}
	assignments := make([]policy.Assignment, 0)
	err := s.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var kind, axis, domain, role string
		var start, end spanner.NullTime
		var reason spanner.NullString
		if err := row.Columns(&kind, &axis, &domain, &role, &start, &end, &reason); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		assignments = append(assignments, policy.Assignment{
			Membership: policy.Membership{
				Scope:  scope,
				Member: policy.Subject{Kind: policy.SubjectUser, Name: string(user)},
				Role:   accesstypes.Role(role),
				Period: periodOf(start, end),
			},
			Reason: reason.StringVal,
		})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.Client.Single().Query() user assignments")
	}

	return assignments, nil
}

// ListRoleUsers returns the people who hold the role in scope now, sorted.
func (s *Store) ListRoleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]accesstypes.User, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	stmt := spanner.Statement{SQL: s.sqlListRoleUsers, Params: map[string]any{paramKind: kind, paramAxis: axis, paramDomain: domain, paramRole: string(role)}}
	values, err := s.queryStrings(ctx, stmt)
	if err != nil {
		return nil, errors.Wrap(err, "role users")
	}
	users := make([]accesstypes.User, 0, len(values))
	for _, v := range values {
		users = append(users, accesstypes.User(v))
	}

	return users, nil
}

// InsertRole creates the (scope, role) row with its description;
// re-inserting an existing role is a no-op that leaves the stored description
// as it is. A name that differs only in case from a custom role held where the
// two are read side by side is refused with a *policy.CaseTwinError. The read of
// the roles the name is held to and the insert are one read-write transaction at
// the serializable isolation level, which Spanner serializes with any other that
// wrote a row the read reads, so of two inserts of names alike but for case one
// commits first and the other, run again, reads its row.
func (s *Store) InsertRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, description string) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	stmt := spanner.Statement{SQL: s.sqlReadMeetingRoles, Params: map[string]any{paramKind: kind, paramAxis: axis, paramDomain: domain}}
	m := spanner.Insert(s.names.roles,
		[]string{colKind, colAxis, colDomain, colRole, colDescription, colUpdatedAt},
		[]any{kind, axis, domain, string(role), description, spanner.CommitTimestamp})
	_, err := spannertxn.Serializable(ctx, s.client, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		held, err := meetingRoles(ctx, txn, stmt)
		if err != nil {
			return err
		}
		if slices.Contains(held, policy.Role{Scope: scope, Name: role}) {
			return nil
		}
		if twin, found := policy.CaseTwin(held, scope, role); found {
			return errors.Wrapf(&policy.CaseTwinError{Held: twin}, "role %q in scope %s", role, scope)
		}
		if err := txn.BufferWrite([]*spanner.Mutation{m}); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
		}

		return nil
	})
	if err != nil {
		return errors.Wrap(err, "spannertxn.Serializable() insert role")
	}

	return nil
}

// meetingRoles reads, through txn, the custom roles a new name held where stmt's
// columns say could meet (policy.RoleNamesMeet): those of the same place, every
// domain's for one domain, and every domain's and each domain's for every
// domain.
func meetingRoles(ctx context.Context, txn *spanner.ReadWriteTransaction, stmt spanner.Statement) ([]policy.Role, error) {
	var roles []policy.Role
	err := txn.Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var kind, axis, domain, role string
		if err := row.Columns(&kind, &axis, &domain, &role); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		roles = append(roles, policy.Role{Scope: scope, Name: accesstypes.Role(role)})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.ReadWriteTransaction.Query() roles")
	}

	return roles, nil
}

// ListRoles returns the scope's roles with their descriptions, sorted by
// name.
func (s *Store) ListRoles(ctx context.Context, scope accesstypes.PolicyScope) ([]policy.RoleRow, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	stmt := spanner.Statement{SQL: s.sqlListRoles, Params: map[string]any{paramKind: kind, paramAxis: axis, paramDomain: domain}}
	roles := make([]policy.RoleRow, 0)
	err := s.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var name, description string
		if err := row.Columns(&name, &description); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		roles = append(roles, policy.RoleRow{Name: accesstypes.Role(name), Description: description})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.Client.Single().Query() roles")
	}

	return roles, nil
}

// DeleteRole deletes the (scope, role) row through DML so the affected count
// is known. Its grants cascade (interleaved ON DELETE CASCADE). Memberships
// held in the scope that have not ended block the delete, and so does a group
// that holds the role there, now or for a period that has not begun: each is
// checked in the delete's own read-write transaction, since nothing in the
// schema ties a membership or a group's role to a role row. The ended ones, a
// person's and a group's, go with the role.
func (s *Store) DeleteRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	params := map[string]any{paramKind: kind, paramAxis: axis, paramDomain: domain, paramRole: string(role)}
	var deleted bool
	_, err := spannertxn.Serializable(ctx, s.client, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		if members, err := anyRow(ctx, txn, spanner.Statement{SQL: s.sqlRoleHasMembers, Params: params}); err != nil {
			return errors.Wrap(err, "role members")
		} else if members {
			return errors.Wrapf(policy.ErrStillAssigned, "role %q in scope %s still has members", role, scope)
		}
		if held, err := anyRow(ctx, txn, spanner.Statement{SQL: s.groups.roleHeld, Params: params}); err != nil {
			return errors.Wrap(err, "role groups")
		} else if held {
			return errors.Wrapf(policy.ErrHeldByGroup, "role %q in scope %s", role, scope)
		}
		if scope.IsEveryDomain() {
			if err := s.refuseHeldInDomain(ctx, txn, role, params); err != nil {
				return err
			}
		}

		count, err := txn.Update(ctx, spanner.Statement{SQL: s.sqlDeleteRole, Params: params})
		if err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.Update()")
		}
		deleted = count > 0
		if !deleted {
			// No row of the role: the name may be a default role's, whose
			// memberships are not this delete's to touch.
			return nil
		}
		if _, err := txn.Update(ctx, spanner.Statement{SQL: s.sqlDeleteEndedMembers, Params: params}); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.Update() ended memberships")
		}
		if _, err := txn.Update(ctx, spanner.Statement{SQL: s.groups.deleteEndedOf, Params: params}); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.Update() ended group roles")
		}
		if scope.IsEveryDomain() {
			for _, sql := range []string{s.sqlEndedInDomain, s.groups.endedInDomain} {
				if _, err := txn.Update(ctx, spanner.Statement{SQL: sql, Params: params}); err != nil {
					return errors.Wrap(err, "spanner.ReadWriteTransaction.Update() ended holdings in one domain")
				}
			}
		}

		return nil
	})
	if err != nil {
		return false, errors.Wrap(err, "spannertxn.Serializable() delete role")
	}

	return deleted, nil
}

// refuseHeldInDomain refuses, inside the transaction of a deletion of a role held in
// every domain, while a person's or a group's holding in one domain reads the role's
// row: one that names the role, has not ended, and whose domain holds no role of the
// name of its own. The refusal wraps policy.ErrHeldInDomain and names the lowest such
// domain, a person's before a group's.
func (s *Store) refuseHeldInDomain(ctx context.Context, txn *spanner.ReadWriteTransaction, role accesstypes.Role, params map[string]any) error {
	for _, holder := range []struct {
		sql, who string
	}{
		{sql: s.sqlHeldInDomain, who: "a person"},
		{sql: s.groups.heldInDomain, who: "a group"},
	} {
		var domain string
		found := false
		err := txn.Query(ctx, spanner.Statement{SQL: holder.sql, Params: params}).Do(func(row *spanner.Row) error {
			if err := row.Columns(&domain); err != nil {
				return errors.Wrap(err, "spanner.Row.Columns()")
			}
			found = true

			return nil
		})
		if err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.Query() holdings in one domain")
		}
		if found {
			return errors.Wrapf(policy.ErrHeldInDomain, "role %q: %s holds it in the domain %q", role, holder.who, domain)
		}
	}

	return nil
}

// anyRow reports whether a statement of the transaction answers a row.
func anyRow(ctx context.Context, txn *spanner.ReadWriteTransaction, stmt spanner.Statement) (bool, error) {
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

// RoleExists reports whether the (scope, role) row exists.
func (s *Store) RoleExists(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	stmt := spanner.Statement{SQL: s.sqlRoleExists, Params: map[string]any{paramKind: kind, paramAxis: axis, paramDomain: domain, paramRole: string(role)}}
	iter := s.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	if _, err := iter.Next(); err != nil {
		if errors.Is(err, iterator.Done) {
			return false, nil
		}

		return false, errors.Wrap(err, "spanner.RowIterator.Next() role exists")
	}

	return true, nil
}

// InsertGrant adds one grant row; the condition is part of the row's identity
// ("" = unconditional), so re-inserting an existing row is a no-op and a
// different condition on the same (permission, resource, field) is a second
// row. It is a change with the one addition: the (scope, role) parent row must
// exist, and where it does not, policy.ErrNoRole is wrapped.
func (s *Store) InsertGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field, condition string) error {
	return s.ChangeGrants(ctx, scope, role, nil, []policy.RoleGrant{{Perm: perm, Resource: resource, Field: field, Condition: condition}})
}

// changeGrantsChunk bounds the rows one ChangeGrants transaction carries, well
// inside Spanner's per-commit mutation limit at this table's width.
const changeGrantsChunk = 1000

// InsertGrants adds the role's grant rows as one write: a change with no
// removals. The (scope, role) parent row must exist, and where it does not,
// policy.ErrNoRole is wrapped.
func (s *Store) InsertGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, grants []policy.RoleGrant) error {
	return s.ChangeGrants(ctx, scope, role, nil, grants)
}

// grantChange is one row of a ChangeGrants call.
type grantChange struct {
	grant  policy.RoleGrant
	remove bool
}

// ChangeGrants removes the role's removals and adds its additions in one
// read-write transaction: the deletes and the inserts commit together, so a
// failure leaves the role's grants as they were. The rows already present among
// the additions are read back by key and only the missing ones are inserted, so
// present rows keep their UpdatedAt and the call is idempotent like
// InsertGrant. A change of more than changeGrantsChunk rows lands in that many
// transactions, removals first; a role's grants are far inside that. The
// (scope, role) parent row must exist for the additions: each transaction that
// carries one reads the row first, and where it is not there wraps
// policy.ErrNoRole and writes nothing.
func (s *Store) ChangeGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, removals, additions []policy.RoleGrant) error {
	if len(removals) == 0 && len(additions) == 0 {
		return nil
	}

	kind, axis, domain := policy.ScopeColumns(scope)
	columns := []string{colKind, colAxis, colDomain, colRole, colPermission, colResource, colField, colCondition, colUpdatedAt}
	keyColumns := []string{colPermission, colResource, colField, colCondition}
	key := func(g policy.RoleGrant) spanner.Key {
		return spanner.Key{kind, axis, domain, string(role), string(g.Perm), g.Resource, g.Field, g.Condition}
	}

	changes := make([]grantChange, 0, len(removals)+len(additions))
	for _, g := range removals {
		changes = append(changes, grantChange{grant: g, remove: true})
	}
	for _, g := range additions {
		changes = append(changes, grantChange{grant: g})
	}

	for chunk := range slices.Chunk(changes, changeGrantsChunk) {
		_, err := spannertxn.Serializable(ctx, s.client, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
			keys := make([]spanner.KeySet, 0, len(chunk))
			for _, c := range chunk {
				if !c.remove {
					keys = append(keys, key(c.grant))
				}
			}

			present := make(map[policy.RoleGrant]struct{}, len(keys))
			if len(keys) > 0 {
				switch _, err := txn.ReadRow(ctx, s.names.roles, spanner.Key{kind, axis, domain, string(role)}, []string{colRole}); {
				case spanner.ErrCode(err) == codes.NotFound:
					return errors.Wrapf(policy.ErrNoRole, "role %q in scope %s", role, scope)
				case err != nil:
					return errors.Wrap(err, "spanner.ReadWriteTransaction.ReadRow() role")
				}
				err := txn.Read(ctx, s.names.roleGrants, spanner.KeySets(keys...), keyColumns).Do(func(row *spanner.Row) error {
					var perm, resource, field, condition string
					if err := row.Columns(&perm, &resource, &field, &condition); err != nil {
						return errors.Wrap(err, "spanner.Row.Columns()")
					}
					present[policy.RoleGrant{Perm: accesstypes.Permission(perm), Resource: resource, Field: field, Condition: condition}] = struct{}{}

					return nil
				})
				if err != nil {
					return errors.Wrap(err, "spanner.ReadWriteTransaction.Read() role grants")
				}
			}

			mutations := make([]*spanner.Mutation, 0, len(chunk))
			for _, c := range chunk {
				if c.remove {
					mutations = append(mutations, spanner.Delete(s.names.roleGrants, key(c.grant)))

					continue
				}
				if _, skip := present[c.grant]; skip {
					continue
				}
				present[c.grant] = struct{}{}
				mutations = append(mutations, spanner.Insert(s.names.roleGrants, columns,
					[]any{kind, axis, domain, string(role), string(c.grant.Perm), c.grant.Resource, c.grant.Field, c.grant.Condition, spanner.CommitTimestamp}))
			}
			if len(mutations) == 0 {
				return nil
			}
			if err := txn.BufferWrite(mutations); err != nil {
				return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
			}

			return nil
		})
		if err != nil {
			return errors.Wrap(err, "spannertxn.Serializable() change grants")
		}
	}

	return nil
}

// DeleteGrant removes one grant row; removing an absent grant is a no-op.
func (s *Store) DeleteGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field, condition string) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	m := spanner.Delete(s.names.roleGrants, spanner.Key{kind, axis, domain, string(role), string(perm), resource, field, condition})
	if _, err := s.client.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		return errors.Wrap(err, "spanner.Client.Apply() delete grant")
	}

	return nil
}

// DeleteGrants removes every condition's row for the (permission, resource,
// field); removing absent rows is a no-op.
func (s *Store) DeleteGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field string) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	m := spanner.Delete(s.names.roleGrants, spanner.Key{kind, axis, domain, string(role), string(perm), resource, field}.AsPrefix())
	if _, err := s.client.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		return errors.Wrap(err, "spanner.Client.Apply() delete grants")
	}

	return nil
}

// ListRoleGrants returns the role's grant rows in scope, sorted.
func (s *Store) ListRoleGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]policy.RoleGrant, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	stmt := spanner.Statement{SQL: s.sqlListRoleGrants, Params: map[string]any{paramKind: kind, paramAxis: axis, paramDomain: domain, paramRole: string(role)}}
	grants := make([]policy.RoleGrant, 0)
	err := s.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var perm, resource, field, condition string
		if err := row.Columns(&perm, &resource, &field, &condition); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		grants = append(grants, policy.RoleGrant{Perm: accesstypes.Permission(perm), Resource: resource, Field: field, Condition: condition})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.Client.Single().Query() role grants")
	}

	return grants, nil
}

// queryStrings runs a single-column string query.
func (s *Store) queryStrings(ctx context.Context, stmt spanner.Statement) ([]string, error) {
	values := make([]string, 0)
	err := s.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var v string
		if err := row.Columns(&v); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		values = append(values, v)

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.Client.Single().Query()")
	}

	return values, nil
}
