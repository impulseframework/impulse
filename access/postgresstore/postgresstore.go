// Package postgresstore implements access.Store over PostgreSQL, riding the
// application's existing connection pool.
//
// Its sibling github.com/impulseframework/impulse/access/spannerstore serves Cloud
// Spanner-backed deployments.
//
// Each Store owns three tables named {Prefix}{Store}{Roles|UserRoles|
// RoleGrants} — defaults yield AccessRoles, AccessUserRoles, AccessRoleGrants.
// Rows are partitioned by where the policy is held, persisted as the
// structural column triple ("Kind", "Axis", "Domain"): Kind names the global
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
	"regexp"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ access.Store = (*Store)(nil)

const defaultPrefix = "Access"

var (
	// prefixPattern keeps concatenated table names valid identifiers in both
	// supported stores without quoting gymnastics (Spanner cannot quote its
	// way around invalid identifiers, and the two stores must accept the same
	// naming options).
	prefixPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	storePattern  = regexp.MustCompile(`^[A-Za-z0-9]*$`)
)

// Option configures a Store.
type Option func(*config)

type config struct {
	store  string
	prefix string
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
}

func resolveNames(opts []Option) (tableNames, error) {
	c := config{prefix: defaultPrefix}
	for _, opt := range opts {
		opt(&c)
	}

	if !prefixPattern.MatchString(c.prefix) {
		return tableNames{}, errors.Newf("invalid table prefix %q: must match %s", c.prefix, prefixPattern)
	}
	if !storePattern.MatchString(c.store) {
		return tableNames{}, errors.Newf("invalid store name %q: must match %s", c.store, storePattern)
	}

	base := c.prefix + c.store
	n := tableNames{
		rawRoles:      base + "Roles",
		rawUserRoles:  base + "UserRoles",
		rawRoleGrants: base + "RoleGrants",
		rawUserIndex:  base + "UserRolesByUser",
	}
	n.roles = pgx.Identifier{n.rawRoles}.Sanitize()
	n.userRoles = pgx.Identifier{n.rawUserRoles}.Sanitize()
	n.roleGrants = pgx.Identifier{n.rawRoleGrants}.Sanitize()

	return n, nil
}

// Store implements access.Store over the application's pgx pool.
type Store struct {
	pool  *pgxpool.Pool
	names tableNames

	// Statements are built once here — table names are identifiers, not bind
	// parameters — so no query text is assembled at call sites.
	sqlInsertRole          string
	sqlDeleteRole          string
	sqlRoleHasMembers      string
	sqlRoleExists          string
	sqlListRoles           string
	sqlInsertUserRole      string
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

		sqlInsertRole:          fmt.Sprintf(`insert into %s ("Kind", "Axis", "Domain", "Role", "Description") values ($1, $2, $3, $4, $5) on conflict do nothing`, names.roles),
		sqlDeleteRole:          fmt.Sprintf(`delete from %s where "Kind" = $1 and "Axis" = $2 and "Domain" = $3 and "Role" = $4`, names.roles),
		sqlRoleHasMembers:      fmt.Sprintf(`select exists(select 1 from %s where "Kind" = $1 and "Axis" = $2 and "Domain" = $3 and "Role" = $4)`, names.userRoles),
		sqlRoleExists:          fmt.Sprintf(`select exists(select 1 from %s where "Kind" = $1 and "Axis" = $2 and "Domain" = $3 and "Role" = $4)`, names.roles),
		sqlListRoles:           fmt.Sprintf(`select "Role", "Description" from %s where "Kind" = $1 and "Axis" = $2 and "Domain" = $3 order by "Role"`, names.roles),
		sqlInsertUserRole:      fmt.Sprintf(`insert into %s ("Kind", "Axis", "Domain", "Role", "User") values ($1, $2, $3, $4, $5) on conflict do nothing`, names.userRoles),
		sqlDeleteUserRole:      fmt.Sprintf(`delete from %s where "Kind" = $1 and "Axis" = $2 and "Domain" = $3 and "Role" = $4 and "User" = $5`, names.userRoles),
		sqlListUserRoles:       fmt.Sprintf(`select "Role" from %s where "Kind" = $1 and "Axis" = $2 and "Domain" = $3 and "User" = $4 order by "Role"`, names.userRoles),
		sqlListUserMemberships: fmt.Sprintf(`select "Kind", "Axis", "Domain", "Role" from %s where "User" = $1 order by "Kind", "Axis", "Domain", "Role"`, names.userRoles),
		sqlListRoleUsers:       fmt.Sprintf(`select "User" from %s where "Kind" = $1 and "Axis" = $2 and "Domain" = $3 and "Role" = $4 order by "User"`, names.userRoles),
		sqlInsertGrant: fmt.Sprintf(
			`insert into %s ("Kind", "Axis", "Domain", "Role", "Permission", "Resource", "Field", "Condition") values ($1, $2, $3, $4, $5, $6, $7, $8) on conflict do nothing`, names.roleGrants),
		sqlDeleteGrant: fmt.Sprintf(
			`delete from %s where "Kind" = $1 and "Axis" = $2 and "Domain" = $3 and "Role" = $4 and "Permission" = $5 and "Resource" = $6 and "Field" = $7 and "Condition" = $8`, names.roleGrants),
		sqlDeleteGrants: fmt.Sprintf(
			`delete from %s where "Kind" = $1 and "Axis" = $2 and "Domain" = $3 and "Role" = $4 and "Permission" = $5 and "Resource" = $6 and "Field" = $7`, names.roleGrants),
		sqlListRoleGrants: fmt.Sprintf(
			`select "Permission", "Resource", "Field", "Condition" from %s where "Kind" = $1 and "Axis" = $2 and "Domain" = $3 and "Role" = $4 order by "Permission", "Resource", "Field", "Condition"`, names.roleGrants),
		sqlReadRoles:       fmt.Sprintf(`select "Kind", "Axis", "Domain", "Role" from %s`, names.roles),
		sqlReadGrants:      fmt.Sprintf(`select "Kind", "Axis", "Domain", "Role", "Permission", "Resource", "Field", "Condition" from %s`, names.roleGrants),
		sqlReadMemberships: fmt.Sprintf(`select "Kind", "Axis", "Domain", "User", "Role" from %s`, names.userRoles),
	}, nil
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

	return []string{
		fmt.Sprintf(`CREATE TABLE %s (
  "Kind" TEXT NOT NULL,
  "Axis" TEXT NOT NULL,
  "Domain" TEXT NOT NULL,
  "Role" TEXT NOT NULL,
  "Description" TEXT NOT NULL,
  "UpdatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("Kind", "Axis", "Domain", "Role"),
  %s
)`, n.roles, kindCheck(n.rawRoles)),
		fmt.Sprintf(`CREATE TABLE %s (
  "Kind" TEXT NOT NULL,
  "Axis" TEXT NOT NULL,
  "Domain" TEXT NOT NULL,
  "Role" TEXT NOT NULL,
  "User" TEXT NOT NULL,
  "CreatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("Kind", "Axis", "Domain", "Role", "User"),
  %s
)`, n.userRoles, kindCheck(n.rawUserRoles)),
		fmt.Sprintf(`CREATE INDEX %s ON %s ("User", "Kind", "Axis", "Domain")`, quote(n.rawUserIndex), n.userRoles),
		fmt.Sprintf(`CREATE TABLE %s (
  "Kind" TEXT NOT NULL,
  "Axis" TEXT NOT NULL,
  "Domain" TEXT NOT NULL,
  "Role" TEXT NOT NULL,
  "Permission" TEXT NOT NULL,
  "Resource" TEXT NOT NULL,
  "Field" TEXT NOT NULL,
  "Condition" TEXT NOT NULL,
  "UpdatedAt" TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY ("Kind", "Axis", "Domain", "Role", "Permission", "Resource", "Field", "Condition"),
  FOREIGN KEY ("Kind", "Axis", "Domain", "Role") REFERENCES %s ("Kind", "Axis", "Domain", "Role") ON DELETE CASCADE,
  %s
)`, n.roleGrants, n.roles, kindCheck(n.rawRoleGrants)),
	}
}

// ReadPolicy reads roles, grants and memberships inside one repeatable-read
// transaction, so the row sets observe the same store state.
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
		if err := row.Scan(&kind, &axis, &domain, &user, &m.Role); err != nil {
			return policy.Membership{}, errors.Wrap(err, "pgx.CollectableRow.Scan()")
		}
		scope, err := policy.ScopeFromColumns(kind, axis, domain)
		if err != nil {
			return policy.Membership{}, errors.Wrap(err, "policy.ScopeFromColumns()")
		}
		m.Scope = scope
		m.Member = policy.Subject{Kind: policy.SubjectUser, Name: user}

		return m, nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "pgx.CollectRows() memberships")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, errors.Wrap(err, "pgx.Tx.Commit()")
	}

	return records, nil
}

// InsertUserRole adds one user-role membership; adding an existing membership
// is a no-op. The role is named by name alone: no role row is required, since
// the release's default roles have none.
func (s *Store) InsertUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	if _, err := s.pool.Exec(ctx, s.sqlInsertUserRole, kind, axis, domain, role, user); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() insert user role")
	}

	return nil
}

// DeleteUserRole removes one user-role membership; removing an absent
// membership is a no-op.
func (s *Store) DeleteUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	if _, err := s.pool.Exec(ctx, s.sqlDeleteUserRole, kind, axis, domain, role, user); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() delete user role")
	}

	return nil
}

// ListUserRoles returns the user's roles in scope, sorted.
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

// ListUserMemberships returns every membership the user holds, wherever it
// is held, read through the user index and sorted by scope then role.
func (s *Store) ListUserMemberships(ctx context.Context, user accesstypes.User) ([]policy.Membership, error) {
	rows, err := s.pool.Query(ctx, s.sqlListUserMemberships, user)
	if err != nil {
		return nil, errors.Wrap(err, "pgxpool.Pool.Query() user memberships")
	}
	memberships, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (policy.Membership, error) {
		var kind, axis, domain string
		m := policy.Membership{Member: policy.Subject{Kind: policy.SubjectUser, Name: string(user)}}
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
		return nil, errors.Wrap(err, "pgx.CollectRows() user memberships")
	}

	return memberships, nil
}

// ListRoleUsers returns the role's members in scope, sorted.
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
// as it is.
func (s *Store) InsertRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, description string) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	if _, err := s.pool.Exec(ctx, s.sqlInsertRole, kind, axis, domain, role, description); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() insert role")
	}

	return nil
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
// database; memberships held in the scope block the delete, checked in the
// delete's own transaction since nothing in the schema ties a membership to a
// role row.
func (s *Store) DeleteRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, errors.Wrap(err, "pgxpool.Pool.Begin() delete role")
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	var hasMembers bool
	if err := tx.QueryRow(ctx, s.sqlRoleHasMembers, kind, axis, domain, role).Scan(&hasMembers); err != nil {
		return false, errors.Wrap(err, "pgx.Tx.QueryRow() role members")
	}
	if hasMembers {
		return false, errors.Newf("role %q in scope %s still has members", role, scope)
	}
	tag, err := tx.Exec(ctx, s.sqlDeleteRole, kind, axis, domain, role)
	if err != nil {
		return false, errors.Wrap(err, "pgx.Tx.Exec() delete role")
	}
	if err := tx.Commit(ctx); err != nil {
		return false, errors.Wrap(err, "pgx.Tx.Commit() delete role")
	}

	return tag.RowsAffected() > 0, nil
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
// row. The (scope, role) parent row must exist.
func (s *Store) InsertGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field, condition string) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	if _, err := s.pool.Exec(ctx, s.sqlInsertGrant, kind, axis, domain, role, perm, resource, field, condition); err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Exec() insert grant")
	}

	return nil
}

// InsertGrants adds the role's grant rows as one write: a change with no
// removals. The (scope, role) parent row must exist.
func (s *Store) InsertGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, grants []policy.RoleGrant) error {
	return s.ChangeGrants(ctx, scope, role, nil, grants)
}

// ChangeGrants removes the role's removals and adds its additions in one
// transaction, as one batched round trip: a failure rolls the whole change
// back. Each insert ignores a conflict, so present rows are untouched and the
// additions are idempotent like InsertGrant. The (scope, role) parent row must
// exist for the additions.
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

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return errors.Wrap(err, "pgxpool.Pool.Begin() change grants")
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

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
