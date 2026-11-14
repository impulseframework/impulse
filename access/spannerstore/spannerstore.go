// Package spannerstore implements access.Store over Cloud Spanner.
//
// Its sibling github.com/impulseframework/impulse/access/postgresstore serves
// PostgreSQL-backed deployments through the application's pgx pool.
//
// Each Store owns three tables named {Prefix}{Store}{Roles|UserRoles|
// RoleGrants} — defaults yield AccessRoles, AccessUserRoles, AccessRoleGrants.
// Rows are partitioned by where the policy is held, persisted as the
// structural column triple (Kind, Axis, Domain): Kind names the global
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
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
)

var _ access.Store = (*Store)(nil)

// Column and query-parameter names shared by the prepared statements and
// mutations.
const (
	colKind        = "Kind"
	colAxis        = "Axis"
	colDomain      = "Domain"
	colRole        = "Role"
	colDescription = "Description"
	colUser        = "User"
	colPermission  = "Permission"
	colResource    = "Resource"
	colField       = "Field"
	colCondition   = "Condition"
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

type tableNames struct {
	roles      string
	userRoles  string
	roleGrants string
	userIndex  string
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
		userIndex:  tables.UserRolesByUser,
	}, nil
}

// Store implements access.Store over a Spanner client.
type Store struct {
	client *spanner.Client
	names  tableNames

	// Statements are built once here — table names are identifiers, not bind
	// parameters — so no query text is assembled at call sites.
	sqlDeleteRole          string
	sqlRoleHasMembers      string
	sqlRoleExists          string
	sqlListRoles           string
	sqlListUserRoles       string
	sqlListUserMemberships string
	sqlListRoleUsers       string
	sqlListRoleGrants      string
	sqlReadRoles           string
	sqlReadGrants          string
	sqlReadMemberships     string
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

		sqlDeleteRole:          fmt.Sprintf("DELETE FROM %s WHERE Kind = @kind AND Axis = @axis AND Domain = @domain AND Role = @role", names.roles),
		sqlRoleHasMembers:      fmt.Sprintf("SELECT 1 FROM %s WHERE Kind = @kind AND Axis = @axis AND Domain = @domain AND Role = @role LIMIT 1", names.userRoles),
		sqlRoleExists:          fmt.Sprintf("SELECT 1 FROM %s WHERE Kind = @kind AND Axis = @axis AND Domain = @domain AND Role = @role", names.roles),
		sqlListRoles:           fmt.Sprintf("SELECT Role, Description FROM %s WHERE Kind = @kind AND Axis = @axis AND Domain = @domain ORDER BY Role", names.roles),
		sqlListUserRoles:       fmt.Sprintf("SELECT Role FROM %s WHERE Kind = @kind AND Axis = @axis AND Domain = @domain AND User = @user ORDER BY Role", names.userRoles),
		sqlListUserMemberships: fmt.Sprintf("SELECT Kind, Axis, Domain, Role FROM %s@{FORCE_INDEX=%s} WHERE User = @user ORDER BY Kind, Axis, Domain, Role", names.userRoles, names.userIndex),
		sqlListRoleUsers:       fmt.Sprintf("SELECT User FROM %s WHERE Kind = @kind AND Axis = @axis AND Domain = @domain AND Role = @role ORDER BY User", names.userRoles),
		sqlListRoleGrants:      fmt.Sprintf("SELECT Permission, Resource, Field, Condition FROM %s WHERE Kind = @kind AND Axis = @axis AND Domain = @domain AND Role = @role ORDER BY Permission, Resource, Field, Condition", names.roleGrants),
		sqlReadRoles:           fmt.Sprintf("SELECT Kind, Axis, Domain, Role FROM %s", names.roles),
		sqlReadGrants:          fmt.Sprintf("SELECT Kind, Axis, Domain, Role, Permission, Resource, Field, Condition FROM %s", names.roleGrants),
		sqlReadMemberships:     fmt.Sprintf("SELECT Kind, Axis, Domain, User, Role FROM %s", names.userRoles),
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
	kindCheck := func(table string) string {
		return fmt.Sprintf("CONSTRAINT %sKind CHECK (Kind IN ('%s', '%s', '%s'))", table, policy.KindGlobal, policy.KindDomain, policy.KindEvery)
	}

	return []string{
		fmt.Sprintf(`CREATE TABLE %s (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  Domain STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  Description STRING(MAX) NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  %s,
) PRIMARY KEY (Kind, Axis, Domain, Role)`, n.roles, kindCheck(n.roles)),
		fmt.Sprintf(`CREATE TABLE %s (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  Domain STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  User STRING(320) NOT NULL,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  %s,
) PRIMARY KEY (Kind, Axis, Domain, Role, User)`, n.userRoles, kindCheck(n.userRoles)),
		fmt.Sprintf(`CREATE INDEX %s ON %s (User, Kind, Axis, Domain)`, n.userIndex, n.userRoles),
		fmt.Sprintf(`CREATE TABLE %s (
  Kind STRING(16) NOT NULL,
  Axis STRING(128) NOT NULL,
  Domain STRING(128) NOT NULL,
  Role STRING(128) NOT NULL,
  Permission STRING(64) NOT NULL,
  Resource STRING(128) NOT NULL,
  Field STRING(128) NOT NULL,
  Condition STRING(MAX) NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  %s,
) PRIMARY KEY (Kind, Axis, Domain, Role, Permission, Resource, Field, Condition),
  INTERLEAVE IN PARENT %s ON DELETE CASCADE`, n.roleGrants, kindCheck(n.roleGrants), n.roles),
	}
}

// ReadPolicy reads roles, grants and memberships inside one read-only
// transaction, so the row sets observe the same store state.
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
		if err := row.Columns(&kind, &axis, &domain, &user, &role); err != nil {
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
		})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.ReadOnlyTransaction.Query() memberships")
	}

	return records, nil
}

// insertIgnoreExists applies an insert mutation, treating "row already
// exists" as the documented no-op.
func (s *Store) insertIgnoreExists(ctx context.Context, m *spanner.Mutation, wrap string) error {
	if _, err := s.client.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		if spanner.ErrCode(err) == codes.AlreadyExists {
			return nil
		}

		return errors.Wrap(err, wrap)
	}

	return nil
}

// InsertUserRole adds one user-role membership; adding an existing membership
// is a no-op. The role is named by name alone: no role row is required, since
// the release's default roles have none.
func (s *Store) InsertUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	m := spanner.Insert(s.names.userRoles,
		[]string{colKind, colAxis, colDomain, colRole, colUser, colCreatedAt},
		[]any{kind, axis, domain, string(role), string(user), spanner.CommitTimestamp})

	return s.insertIgnoreExists(ctx, m, "spanner.Client.Apply() insert user role")
}

// DeleteUserRole removes one user-role membership; removing an absent
// membership is a no-op.
func (s *Store) DeleteUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	m := spanner.Delete(s.names.userRoles, spanner.Key{kind, axis, domain, string(role), string(user)})
	if _, err := s.client.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		return errors.Wrap(err, "spanner.Client.Apply() delete user role")
	}

	return nil
}

// ListUserRoles returns the user's roles in scope, sorted.
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

// ListUserMemberships returns every membership the user holds, wherever it
// is held, read through the user index and sorted by scope then role.
func (s *Store) ListUserMemberships(ctx context.Context, user accesstypes.User) ([]policy.Membership, error) {
	stmt := spanner.Statement{SQL: s.sqlListUserMemberships, Params: map[string]any{paramUser: string(user)}}
	memberships := make([]policy.Membership, 0)
	err := s.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var kind, axis, domain, role string
		if err := row.Columns(&kind, &axis, &domain, &role); err != nil {
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
		})

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.Client.Single().Query() user memberships")
	}

	return memberships, nil
}

// ListRoleUsers returns the role's members in scope, sorted.
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
// as it is.
func (s *Store) InsertRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, description string) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	m := spanner.Insert(s.names.roles,
		[]string{colKind, colAxis, colDomain, colRole, colDescription, colUpdatedAt},
		[]any{kind, axis, domain, string(role), description, spanner.CommitTimestamp})

	return s.insertIgnoreExists(ctx, m, "spanner.Client.Apply() insert role")
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
// is known. Its grants cascade (interleaved ON DELETE CASCADE); memberships
// held in the scope block the delete, checked in the delete's own read-write
// transaction since nothing in the schema ties a membership to a role row.
func (s *Store) DeleteRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	kind, axis, domain := policy.ScopeColumns(scope)
	params := map[string]any{paramKind: kind, paramAxis: axis, paramDomain: domain, paramRole: string(role)}
	var deleted bool
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		iter := txn.Query(ctx, spanner.Statement{SQL: s.sqlRoleHasMembers, Params: params})
		defer iter.Stop()
		if _, err := iter.Next(); err == nil {
			return errors.Newf("role %q in scope %s still has members", role, scope)
		} else if !errors.Is(err, iterator.Done) {
			return errors.Wrap(err, "spanner.RowIterator.Next() role members")
		}

		count, err := txn.Update(ctx, spanner.Statement{SQL: s.sqlDeleteRole, Params: params})
		if err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.Update()")
		}
		deleted = count > 0

		return nil
	})
	if err != nil {
		return false, errors.Wrap(err, "spanner.Client.ReadWriteTransaction() delete role")
	}

	return deleted, nil
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
// row. The (scope, role) parent row must exist.
func (s *Store) InsertGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field, condition string) error {
	kind, axis, domain := policy.ScopeColumns(scope)
	m := spanner.Insert(s.names.roleGrants,
		[]string{colKind, colAxis, colDomain, colRole, colPermission, colResource, colField, colCondition, colUpdatedAt},
		[]any{kind, axis, domain, string(role), string(perm), resource, field, condition, spanner.CommitTimestamp})

	return s.insertIgnoreExists(ctx, m, "insert grant")
}

// changeGrantsChunk bounds the rows one ChangeGrants transaction carries, well
// inside Spanner's per-commit mutation limit at this table's width.
const changeGrantsChunk = 1000

// InsertGrants adds the role's grant rows as one write: a change with no
// removals. The (scope, role) parent row must exist.
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
// (scope, role) parent row must exist for the additions.
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
		_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
			keys := make([]spanner.KeySet, 0, len(chunk))
			for _, c := range chunk {
				if !c.remove {
					keys = append(keys, key(c.grant))
				}
			}

			present := make(map[policy.RoleGrant]struct{}, len(keys))
			if len(keys) > 0 {
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
			return errors.Wrap(err, "spanner.Client.ReadWriteTransaction() change grants")
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
