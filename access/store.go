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
//   - An assignment (a person's role, a person's place in a group, a group's
//     role) carries the period it counts in, two nullable instants. Its insert
//     gives the assignment that period: the row is written where the key is
//     free, and where it is taken the period is replaced and the rest of the
//     row left as it was, so assigning again is how a period changes and the
//     same period again is a no-op. A person's role carries a reason too,
//     opaque text that is empty for none: its insert writes the reason with
//     the period and replaces both where the key is taken, so the reason kept
//     is the one the role was last given with. No row is written when a
//     period begins or ends: the reads apply it. The listings of what a
//     person or a group holds return the assignments that count at the
//     database's present, so one outside its period is left out while its row
//     is still there; ReadPolicy returns every row with its period, for the
//     engine to apply at the instant of each check.
//   - A membership names a role by name alone, and so does a group's role.
//     InsertUserRole and InsertGroupRole write the row whether or not a role
//     row exists: they are how one of the release's default roles, which has
//     no row, is given. InsertCustomUserRole and InsertCustomGroupRole are how
//     a custom role is given: each reads the role's row where the holding
//     reads it (the scope's own, or every domain's for one domain) in the
//     transaction that writes the holding, writes nothing and wraps
//     policy.ErrNoRole where no such row is held, and takes its turn with a
//     DeleteRole of that row. Of a role given and deleted at the same moment,
//     either the holding is written and the delete refuses, or the role goes
//     and nothing is held.
//   - DeleteRole is scoped to (scope, role): it cascades the role's grants,
//     refuses with an error while memberships held in that scope that have
//     not ended still name the role (checked in the same transaction as the
//     delete), takes the ended ones with the role, and reports whether a row
//     was actually deleted. A role held in every domain is refused as well,
//     wrapping policy.ErrHeldInDomain, while a membership or a group's role
//     held in one domain names it, has not ended, and that domain holds no
//     custom role of the name itself; the ended ones of those go with the
//     role.
//   - InsertRole reads the roles a new name is held to and inserts in one
//     transaction that no other insert of a name alike but for case runs
//     beside: of two such names added at the same moment, one is held and the
//     other is refused.
//   - InsertGrant requires the (scope, role) row to exist (DB-enforced
//     parent interleave or foreign key): a grant is a custom role's; a write
//     for a role with no row wraps policy.ErrNoRole.
//   - ChangeGrants applies a role's removals and additions in one transaction,
//     so a grant whose condition changed (one row removed, one added) is never
//     seen with neither row.
//   - List results are sorted for deterministic output.
//   - ReadPolicy reads roles, grants, memberships, groups and their members
//     with snapshot consistency: the row sets observe the same store state.
//   - A group's member and a group's role each require the group's row
//     (DB-enforced foreign key), and DeleteGroup deletes the group with its
//     members and the roles it holds. A member or a role written to a group
//     with no row wraps policy.ErrNoGroup.
type Store interface {
	// ReadPolicy returns the store's complete policy content as normalized
	// records for the snapshot compiler: the custom roles, their grants and
	// the memberships.
	ReadPolicy(ctx context.Context) (*policy.Records, error)

	// Membership. InsertUserRole gives the membership its period and its
	// reason, replacing the period and the reason of one that is there; the
	// three listings return the memberships that count now.
	InsertUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) error
	// InsertCustomUserRole writes what InsertUserRole writes, only while the
	// roles table holds a row of the custom role where the membership reads
	// it, and wraps policy.ErrNoRole where it holds none.
	InsertCustomUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) error
	DeleteUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error
	ListUserRoles(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User) ([]accesstypes.Role, error)
	// ListUserMemberships returns every membership the user holds now,
	// wherever it is held, sorted by scope then role: the no-scope listing,
	// read through the user index.
	ListUserMemberships(ctx context.Context, user accesstypes.User) ([]policy.Membership, error)
	// ListUserAssignments returns every membership of the user's own that has
	// not ended, wherever it is held, each with its period and the reason it
	// was given for, sorted by scope then role: what the person holds or
	// will, and why. One whose period has not begun is listed; ReadPolicy
	// does not carry the reason, which is not policy.
	ListUserAssignments(ctx context.Context, user accesstypes.User) ([]policy.Assignment, error)
	ListRoleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]accesstypes.User, error)

	// Roles. A role row carries the description its creator gave it, as
	// opaque text: InsertRole writes it with the row and, for a (scope, role)
	// that exists, is the documented no-op, so the stored description stays
	// as it is. InsertRole refuses a name that differs only in the case of
	// its ASCII letters from a custom role held where the two names would be
	// read side by side (policy.CaseTwin): in the scope itself, for one domain
	// in every domain as well, and for every domain in each single domain as
	// well. The refusal is a *policy.CaseTwinError naming the role held.
	// ListRoles returns each role with its description, sorted by name.
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
	// there. DeleteGroup deletes the group with its members and the roles it
	// holds, in one transaction. ListGroups returns the groups kept in scope,
	// sorted by name, and Group one by its id.
	InsertGroup(ctx context.Context, group *policy.GroupRow) (taken bool, err error)
	SetGroupDescription(ctx context.Context, group accesstypes.Group, description string) (bool, error)
	DeleteGroup(ctx context.Context, group accesstypes.Group) (bool, error)
	Group(ctx context.Context, group accesstypes.Group) (*policy.GroupRow, bool, error)
	ListGroups(ctx context.Context, scope accesstypes.PolicyScope) ([]policy.GroupRow, error)

	// A group's members. An insert gives the person's place its period, as a
	// membership's does, and deletes of absent rows are no-ops; the group's row
	// must exist for an insert. ListGroupMembers returns the people in the
	// group now, sorted by user, and ListUserGroups the groups the user is in
	// now, wherever each is kept, sorted by id, read through the index led by
	// the user.
	InsertGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User, period accesstypes.Period) error
	DeleteGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User) error
	ListGroupMembers(ctx context.Context, group accesstypes.Group) ([]accesstypes.User, error)
	ListUserGroups(ctx context.Context, user accesstypes.User) ([]policy.GroupRow, error)

	// The roles a group holds, each held where its scope says, as a
	// membership's is. An insert gives the role its period and deletes of
	// absent rows are no-ops; the group's row must exist for an insert, and
	// the role is named by name alone. ListGroupRoles returns the roles the
	// group holds now, sorted by scope then role. ListUserGroupRoles returns
	// every role held now by a group the user is in now, each with its group
	// and where the group is kept: what the person holds through groups, as
	// one read. ListRoleGroups returns the groups that hold role in scope now,
	// sorted by name then id: the groups a role is held through. DeleteRole
	// refuses, wrapping policy.ErrHeldByGroup, while a group's row of the role
	// in the scope has not ended, checked in the delete's own transaction, and
	// takes the ended ones with the role; for a role held in every domain it
	// refuses, wrapping policy.ErrHeldInDomain, for a group's row in one domain
	// as the contract above says.
	// CountGroupMembersIn and ListGroupRolesIn say what the groups kept in
	// scope hold, for a listing of them: how many people each has, and the
	// roles each holds with their periods, sorted by group, then where the
	// role is held, then role. Both read what has not ended, an assignment
	// whose period has not begun among it. A group with no member is absent
	// from the counts.
	CountGroupMembersIn(ctx context.Context, scope accesstypes.PolicyScope) (map[accesstypes.Group]int, error)
	ListGroupRolesIn(ctx context.Context, scope accesstypes.PolicyScope) ([]policy.Membership, error)
	InsertGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error
	// InsertCustomGroupRole writes what InsertGroupRole writes, only while the
	// roles table holds a row of the custom role where the group's role reads
	// it, and wraps policy.ErrNoRole where it holds none.
	InsertCustomGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error
	DeleteGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role) error
	ListGroupRoles(ctx context.Context, group accesstypes.Group) ([]policy.Membership, error)
	ListUserGroupRoles(ctx context.Context, user accesstypes.User) ([]policy.GroupRole, error)
	ListRoleGroups(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]policy.GroupRow, error)
}
