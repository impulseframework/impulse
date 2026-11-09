package access

import (
	"context"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

var _ policyStore = (*storeManager)(nil)

// storeManager implements the policyStore seam over a typed Store. It is the
// one shared code path between store implementations and the management API:
// it owns resource/field splitting and the dot invariant, composes derived
// queries from the store's primitives, and fires the policy-change signal
// after every successful write.
type storeManager struct {
	store Store

	// onPolicyChange, when set, is called after every successful policy write
	// so the snapshot evaluator can refresh without waiting out the heartbeat.
	onPolicyChange func()
}

func newStoreManager(store Store) *storeManager {
	return &storeManager{store: store}
}

func (m *storeManager) notifyPolicyChange() {
	if m.onPolicyChange != nil {
		m.onPolicyChange()
	}
}

func (m *storeManager) addUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error {
	if err := m.store.InsertUserRole(ctx, scope, user, role); err != nil {
		return errors.Wrapf(err, "access.Store.InsertUserRole(): role %q to %q", role, user)
	}
	m.notifyPolicyChange()

	return nil
}

func (m *storeManager) deleteUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error {
	if err := m.store.DeleteUserRole(ctx, scope, user, role); err != nil {
		return errors.Wrapf(err, "access.Store.DeleteUserRole(): role %q from %q", role, user)
	}
	m.notifyPolicyChange()

	return nil
}

func (m *storeManager) userRoles(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User) ([]accesstypes.Role, error) {
	roles, err := m.store.ListUserRoles(ctx, scope, user)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListUserRoles(): user %q", user)
	}

	return roles, nil
}

func (m *storeManager) userMemberships(ctx context.Context, user accesstypes.User) ([]policy.Membership, error) {
	memberships, err := m.store.ListUserMemberships(ctx, user)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListUserMemberships(): user %q", user)
	}

	return memberships, nil
}

func (m *storeManager) addRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, description string) error {
	if err := m.store.InsertRole(ctx, scope, role, description); err != nil {
		return errors.Wrapf(err, "access.Store.InsertRole(): role %q in scope %q", role, scope)
	}
	m.notifyPolicyChange()

	return nil
}

// roles lists the custom roles held in scope with their descriptions, in
// the store's order (by name).
func (m *storeManager) roles(ctx context.Context, scope accesstypes.PolicyScope) ([]RoleInfo, error) {
	rows, err := m.store.ListRoles(ctx, scope)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListRoles(): scope %q", scope)
	}

	roles := make([]RoleInfo, 0, len(rows))
	for _, row := range rows {
		roles = append(roles, RoleInfo{Name: row.Name, Description: row.Description})
	}

	return roles, nil
}

// deleteRole is scoped to (scope, role). Grants cascade in the store;
// memberships held in the scope block the delete, checked by the store in the
// delete's own transaction, backstopping the caller's has-users guard.
func (m *storeManager) deleteRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	deleted, err := m.store.DeleteRole(ctx, scope, role)
	if err != nil {
		return false, errors.Wrapf(err, "access.Store.DeleteRole(): role %q in scope %q", role, scope)
	}
	if deleted {
		m.notifyPolicyChange()
	}

	return deleted, nil
}

func (m *storeManager) roleExists(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	exists, err := m.store.RoleExists(ctx, scope, role)
	if err != nil {
		return false, errors.Wrapf(err, "access.Store.RoleExists(): role %q in scope %q", role, scope)
	}

	return exists, nil
}

func (m *storeManager) roleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]accesstypes.User, error) {
	users, err := m.store.ListRoleUsers(ctx, scope, role)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListRoleUsers(): role %q", role)
	}

	return users, nil
}

func (m *storeManager) addGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource accesstypes.Resource, condition string) error {
	base, field, err := splitGrantResource(resource)
	if err != nil {
		return err
	}
	if err := m.store.InsertGrant(ctx, scope, role, perm, base, field, condition); err != nil {
		return errors.Wrapf(err, "access.Store.InsertGrant(): %q on %q for role %q", perm, resource, role)
	}
	m.notifyPolicyChange()

	return nil
}

// addGrants persists the rows as one store write and signals one policy
// change; no rows is a no-op.
func (m *storeManager) addGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, rows []GrantRow) error {
	if len(rows) == 0 {
		return nil
	}

	grants, err := roleGrantRows(rows)
	if err != nil {
		return err
	}
	if err := m.store.InsertGrants(ctx, scope, role, grants); err != nil {
		return errors.Wrapf(err, "access.Store.InsertGrants(): %d grants for role %q", len(grants), role)
	}
	m.notifyPolicyChange()

	return nil
}

// changeGrants applies a role's removals and additions as one store write and
// signals one policy change; nothing to change is a no-op.
func (m *storeManager) changeGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, removals, additions []GrantRow) error {
	if len(removals) == 0 && len(additions) == 0 {
		return nil
	}

	remove, err := roleGrantRows(removals)
	if err != nil {
		return err
	}
	add, err := roleGrantRows(additions)
	if err != nil {
		return err
	}
	if err := m.store.ChangeGrants(ctx, scope, role, remove, add); err != nil {
		return errors.Wrapf(err, "access.Store.ChangeGrants(): removing %d and adding %d grants for role %q", len(remove), len(add), role)
	}
	m.notifyPolicyChange()

	return nil
}

// roleGrantRows splits each row's resource into the store's base and field.
func roleGrantRows(rows []GrantRow) ([]policy.RoleGrant, error) {
	grants := make([]policy.RoleGrant, 0, len(rows))
	for _, row := range rows {
		base, field, err := splitGrantResource(row.Resource)
		if err != nil {
			return nil, err
		}
		grants = append(grants, policy.RoleGrant{Perm: row.Permission, Resource: base, Field: field, Condition: row.Condition})
	}

	return grants, nil
}

// removeGrant removes exactly one grant row: the resource's row under the
// given condition ("" = the unconditional row).
func (m *storeManager) removeGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource accesstypes.Resource, condition string) error {
	base, field, err := splitGrantResource(resource)
	if err != nil {
		return err
	}
	if err := m.store.DeleteGrant(ctx, scope, role, perm, base, field, condition); err != nil {
		return errors.Wrapf(err, "access.Store.DeleteGrant(): %q on %q for role %q", perm, resource, role)
	}
	m.notifyPolicyChange()

	return nil
}

// removeGrants removes every grant row on the resource, whatever its
// condition.
func (m *storeManager) removeGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource accesstypes.Resource) error {
	base, field, err := splitGrantResource(resource)
	if err != nil {
		return err
	}
	if err := m.store.DeleteGrants(ctx, scope, role, perm, base, field); err != nil {
		return errors.Wrapf(err, "access.Store.DeleteGrants(): %q on %q for role %q", perm, resource, role)
	}
	m.notifyPolicyChange()

	return nil
}

// addScopeWideGrant persists a permission attached to no resource: the store
// row carries empty resource and field columns, a spot real resources can
// never occupy (their names are validated non-empty in splitGrantResource).
func (m *storeManager) addScopeWideGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission) error {
	if err := m.store.InsertGrant(ctx, scope, role, perm, "", "", ""); err != nil {
		return errors.Wrapf(err, "access.Store.InsertGrant(): scope-wide %q for role %q", perm, role)
	}
	m.notifyPolicyChange()

	return nil
}

func (m *storeManager) removeScopeWideGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission) error {
	if err := m.store.DeleteGrant(ctx, scope, role, perm, "", "", ""); err != nil {
		return errors.Wrapf(err, "access.Store.DeleteGrant(): scope-wide %q for role %q", perm, role)
	}
	m.notifyPolicyChange()

	return nil
}

func (m *storeManager) roleGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (accesstypes.RolePermissionCollection, error) {
	grants, err := m.store.ListRoleGrants(ctx, scope, role)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListRoleGrants(): role %q", role)
	}

	permissions := make(accesstypes.RolePermissionCollection, len(grants))
	for _, g := range grants {
		pg := permissions[g.Perm]
		if g.Resource == "" {
			pg.ScopeWide = true
		} else {
			pg.Resources = append(pg.Resources, joinResourceField(g.Resource, g.Field))
		}
		permissions[g.Perm] = pg
	}

	return permissions, nil
}

// roleGrantConditions returns the role's resource grants with the conditions
// each resource is granted under (sorted; "" is the unconditional grant),
// keyed by permission; scope-wide grants are structural (no resource) and not
// included.
func (m *storeManager) roleGrantConditions(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (map[accesstypes.Permission]map[accesstypes.Resource][]string, error) {
	grants, err := m.store.ListRoleGrants(ctx, scope, role)
	if err != nil {
		return nil, errors.Wrapf(err, "access.Store.ListRoleGrants(): role %q", role)
	}

	out := make(map[accesstypes.Permission]map[accesstypes.Resource][]string)
	for _, g := range grants {
		if g.Resource == "" {
			continue
		}
		if out[g.Perm] == nil {
			out[g.Perm] = make(map[accesstypes.Resource][]string)
		}
		res := joinResourceField(g.Resource, g.Field)
		out[g.Perm][res] = append(out[g.Perm][res], g.Condition)
	}
	for _, resources := range out {
		for _, conditions := range resources {
			slices.Sort(conditions)
		}
	}

	return out, nil
}

// splitGrantResource splits a declared resource into its stored (base, field)
// columns and enforces the dot invariant, fail-closed at declaration time: a
// Resource has at most one dot — 0 dots is a parent resource, 1 dot is a
// field on a parent — and both segments are non-empty and dot-free. '.' is
// reserved as the structural separator. The non-empty rule also keeps the
// stores' empty-resource encoding of scope-wide grants unreachable from data.
//
// A resource whose real name violates the rule must translate at declaration,
// never be stored ambiguous.
func splitGrantResource(resource accesstypes.Resource) (base, field string, err error) {
	s := string(resource)
	base, field = splitResourceField(s)
	switch {
	case base == "":
		return "", "", errors.Newf("invalid resource %q: empty parent segment", s)
	case strings.ContainsRune(base, '.'):
		return "", "", errors.Newf("invalid resource %q: at most one '.' is allowed (parent or parent.field)", s)
	case field == "" && strings.ContainsRune(s, '.'):
		return "", "", errors.Newf("invalid resource %q: empty field segment", s)
	}

	return base, field, nil
}

// joinResourceField reassembles a stored (base, field) pair into the dotted
// resource name the public API speaks.
func joinResourceField(base, field string) accesstypes.Resource {
	if field == "" {
		return accesstypes.Resource(base)
	}

	return accesstypes.Resource(base + "." + field)
}
