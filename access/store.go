package access

import (
	"context"

	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// Store is the persistence seam for policy data: three typed tables (custom
// roles, user-role memberships, role grants) and the three of the groups of
// people (the groups, their members, the roles each group holds), each row held where its
// accesstypes.PolicyScope says — the global partition, one tenant domain or
// every tenant domain, persisted as the structural column triple (Kind, Axis,
// Domain) — plus one normalized read feeding the snapshot compiler. The
// release's default roles have no rows: the store holds only what is written
// at run time. Implementations are thin — each method is one SQL statement
// against one store's tables; everything smarter (validation, resource/field
// splitting, change signaling, snapshot compilation) lives in the access
// package, one shared code path for every store.
//
// The interface is sealed by construction: ReadPolicy's signature references
// this module's internal/policy package, so only packages inside this module
// can implement it. Use the provided implementations:
//
//	spannerstore.New(client, opts...)   // Cloud Spanner
//	postgresstore.New(pool, opts...)    // PostgreSQL, rides the app's pgx pool
//
// Store values hold bare names only — no marshal prefixes, no sentinel
// values. All values are opaque labels to the store: referential validity of
// domains, users, resources and role names belongs to the callers that write
// them. Where a row is held is stored structurally: the kind column names the
// global partition, one domain or every domain, never a distinguished domain
// value, so any domain string is ordinary tenant data. The axis column names
// the axis a domain belongs to and is the empty string for the default axis —
// the only axis a scope can name today, so every row carries "" and a later
// axis declaration changes no stored row. A row under any other axis fails
// the policy read rather than folding into the default.
//
// Contracts every implementation provides:
//   - Inserts are idempotent: re-inserting an existing row is a no-op, not an
//     error. Deletes of absent rows are no-ops.
//   - A membership names a role by name alone: InsertUserRole writes it
//     whether or not a role row exists, since the role may be one of the
//     release's defaults, which have no row. The access package checks that
//     the name resolves before it writes.
//   - DeleteRole is scoped to (scope, role): it cascades the role's grants,
//     refuses with an error while memberships held in that scope still name
//     the role (checked in the same transaction as the delete), and reports
//     whether a row was actually deleted.
//   - InsertGrant requires the (scope, role) row to exist (DB-enforced
//     parent interleave or foreign key): a grant is a custom role's.
//   - ChangeGrants applies a role's removals and additions in one transaction,
//     so a grant whose condition changed (one row removed, one added) is never
//     seen with neither row.
//   - List results are sorted for deterministic output.
//   - ReadPolicy reads roles, grants, memberships, groups and their members
//     with snapshot consistency: the row sets observe the same store state.
//   - A group's member and a group's role each require the group's row
//     (DB-enforced foreign key), and DeleteGroup refuses a group that still
//     has a member or a role: a group goes once it is emptied, so no person
//     loses a role by a group's deletion.
type Store interface {
	// ReadPolicy returns the store's complete policy content as normalized
	// records for the snapshot compiler: the custom roles, their grants and
	// the memberships.
	ReadPolicy(ctx context.Context) (*policy.Records, error)

	// Membership
	InsertUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error
	DeleteUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error
	ListUserRoles(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User) ([]accesstypes.Role, error)
	// ListUserMemberships returns every membership the user holds, wherever
	// it is held, sorted by scope then role: the no-scope listing, read
	// through the user index.
	ListUserMemberships(ctx context.Context, user accesstypes.User) ([]policy.Membership, error)
	ListRoleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]accesstypes.User, error)

	// Roles. A role row carries the description its creator gave it, as
	// opaque text: InsertRole writes it with the row and, for a (scope, role)
	// that exists, is the documented no-op, so the stored description stays
	// as it is. ListRoles returns each role with its description, sorted by
	// name.
	InsertRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, description string) error
	ListRoles(ctx context.Context, scope accesstypes.PolicyScope) ([]policy.RoleRow, error)
	DeleteRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error)
	RoleExists(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error)

	// Grants. resource and field are stored as separate columns: field "" is
	// an endpoint grant, "*" an all-fields wildcard, anything else one field.
	// resource "" (with field "") is a scope-wide grant — the permission held
	// with no resource attachment; real resource names are validated non-empty
	// above this seam, so "" is structurally unreachable from data.
	// condition is the grant's condition as opaque expression text, "" when
	// unconditional, and is part of the row's identity: one (role,
	// permission, resource, field) holds one row per condition, so a role may
	// grant the same permission on one resource under several conditions.
	// InsertGrant is idempotent per row. DeleteGrant removes exactly one row;
	// DeleteGrants removes every condition's row for the (permission,
	// resource, field).
	InsertGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field, condition string) error
	// InsertGrants adds the role's grant rows as one write per store round
	// trip instead of one per row: rows already present are left as they are
	// and the rest are inserted, so the call is idempotent like InsertGrant and
	// an overlap with existing rows is not an error. A row repeated in the
	// list is written once; an empty list is a no-op. Same parent-row
	// requirement as InsertGrant.
	InsertGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, grants []policy.RoleGrant) error
	// ChangeGrants removes the role's removals and adds its additions as one
	// transaction: a reader sees the role's grants before the change or after
	// it, never between, and a failure leaves them as they were. Additions
	// already present are left as they are, as in InsertGrants; removals of
	// absent rows are no-ops; nothing to change is a no-op. Same parent-row
	// requirement as InsertGrant for the additions.
	ChangeGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, removals, additions []policy.RoleGrant) error
	DeleteGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field, condition string) error
	DeleteGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field string) error
	ListRoleGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]policy.RoleGrant, error)

	// Groups of people. A group is kept in the global partition or in one
	// domain, under the id its creator generated, and its name is held to one
	// group where it is kept (a unique index). InsertGroup is no idempotent
	// insert: an id or a name already taken there is an error, reported by
	// taken. SetGroupDescription and DeleteGroup report whether the group was
	// there; DeleteGroup refuses with an error while the group has a member or
	// a role, checked in the delete's own transaction. ListGroups returns the
	// groups kept in scope, sorted by name, and Group one by its id.
	InsertGroup(ctx context.Context, group *policy.GroupRow) (taken bool, err error)
	SetGroupDescription(ctx context.Context, group accesstypes.Group, description string) (bool, error)
	DeleteGroup(ctx context.Context, group accesstypes.Group) (bool, error)
	Group(ctx context.Context, group accesstypes.Group) (*policy.GroupRow, bool, error)
	ListGroups(ctx context.Context, scope accesstypes.PolicyScope) ([]policy.GroupRow, error)

	// A group's members. Inserts are idempotent and deletes of absent rows
	// no-ops, as a membership's are; the group's row must exist for an insert.
	// ListGroupMembers is sorted by user, and ListUserGroups returns the groups
	// the user is in, wherever each is kept, sorted by id, read through the
	// index led by the user.
	InsertGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User) error
	DeleteGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User) error
	ListGroupMembers(ctx context.Context, group accesstypes.Group) ([]accesstypes.User, error)
	ListUserGroups(ctx context.Context, user accesstypes.User) ([]policy.GroupRow, error)

	// The roles a group holds, each held where its scope says, as a
	// membership's is. Inserts are idempotent and deletes of absent rows
	// no-ops; the group's row must exist for an insert, and the role is named
	// by name alone. ListGroupRoles is sorted by scope then role.
	// ListUserGroupRoles returns every role held by a group the user is in,
	// each with its group and where the group is kept: what the person holds
	// through groups, as one read.
	InsertGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role) error
	DeleteGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role) error
	ListGroupRoles(ctx context.Context, group accesstypes.Group) ([]policy.Membership, error)
	ListUserGroupRoles(ctx context.Context, user accesstypes.User) ([]policy.GroupRole, error)
}
