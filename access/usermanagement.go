package access

import (
	"context"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/tracer"
)

var _ UserManager = &userManager{}

// userManager implements UserManager on top of the store's management seam
// (managementStore), the release's default roles and the evaluator. Validation (role existence,
// empty-input checks, the default roles' read-only rule) lives here; the
// store only persists and queries policy. Scopes are opaque partition labels
// — nothing here validates tenant existence, and no operation enumerates
// tenants: the application owns its tenant list.
type userManager struct {
	store     managementStore
	defaults  *defaultRoles
	evaluator evaluator
	// tenantAdministrators are the roles whose holders administer a tenant
	// (WithTenantAdministratorRoles), which TenantsAdministered reads the
	// store by.
	tenantAdministrators []accesstypes.Role
	// collection is what the running release declares (the collection New is
	// given), which a grant written to a role is held to (requireDeclared).
	collection PermissionCollection
}

// newUserManager creates a userManager over the policy store, the release's
// default roles (nil for none), the evaluator the effective-permission
// listings answer from, the roles whose holders administer a tenant (none
// where the application named none) and the release's permission collection,
// which a grant written to a role is held to.
func newUserManager(store managementStore, defaults *defaultRoles, evaluator evaluator, tenantAdministrators []accesstypes.Role, collection PermissionCollection) *userManager {
	return &userManager{
		store:                store,
		defaults:             defaults,
		evaluator:            evaluator,
		tenantAdministrators: slices.Clone(tenantAdministrators),
		collection:           collection,
	}
}

// AddRoleUsers assigns role to users, the memberships held in scope, each with
// no period. Errors if role doesn't exist in scope.
func (u *userManager) AddRoleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, users ...accesstypes.User) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := refuseSuperAdministratorOutsideGlobal(scope, role); err != nil {
		return err
	}
	roleFound, err := u.roleExists(ctx, scope, role)
	if err != nil {
		return err
	}
	if !roleFound {
		return httpio.NewNotFoundMessagef("role %q is not a valid role. Please check that the role exists.", string(role))
	}

	for _, user := range users {
		if user == "" {
			return httpio.NewBadRequestMessage("user cannot be empty string")
		}

		if err := u.store.addUserRole(ctx, scope, user, role, accesstypes.Period{}); err != nil {
			return err
		}
	}

	return nil
}

// AddUserRoles assigns roles to user, the memberships held in scope, each with
// no period and no reason: a role the user holds for a period is theirs until
// it is removed, and the reason it was given for goes with the period. Errors
// if any role doesn't exist in scope.
func (u *userManager) AddUserRoles(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, roles ...accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	return u.assignUserRoles(ctx, scope, user, accesstypes.Period{}, "", roles...)
}

// assignUserRoles gives user each role in scope for the period and with the
// reason, once every role is one the user may be given there for it and the
// reason is one a role carries.
func (u *userManager) assignUserRoles(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, period accesstypes.Period, reason string, roles ...accesstypes.Role) error {
	reason, err := ValidateReason(reason)
	if err != nil {
		return err
	}
	for _, role := range roles {
		if err := refuseSuperAdministratorOutsideGlobal(scope, role); err != nil {
			return err
		}
		if err := ValidatePeriod(role, period); err != nil {
			return err
		}
		roleFound, err := u.roleExists(ctx, scope, role)
		if err != nil {
			return err
		}
		if !roleFound {
			return httpio.NewNotFoundMessagef("role %q is not a valid role. Please check that the role exists.", role)
		}
	}

	if user == "" {
		return httpio.NewBadRequestMessage("user cannot be empty string")
	}

	for _, role := range roles {
		if err := u.store.assignUserRole(ctx, scope, user, role, period, reason); err != nil {
			return err
		}
	}

	return nil
}

// DeleteRoleUsers removes users from role, the memberships held in scope.
// Errors if role doesn't exist.
func (u *userManager) DeleteRoleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, users ...accesstypes.User) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	roleFound, err := u.roleExists(ctx, scope, role)
	if err != nil {
		return err
	}
	if !roleFound {
		return httpio.NewNotFoundMessagef("role %q is not a valid role. Please check that the role exists.", string(role))
	}

	for _, user := range users {
		if err := u.store.deleteUserRole(ctx, scope, user, role); err != nil {
			return err
		}
	}

	return nil
}

// DeleteAllRolePermissions removes all permissions from a custom role held in
// scope, scope-wide and resource-specific alike.
func (u *userManager) DeleteAllRolePermissions(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := u.requireCustomRole(ctx, scope, role, "Permissions cannot be removed from a role that doesn't exist"); err != nil {
		return err
	}

	perms, err := u.store.roleGrants(ctx, scope, role)
	if err != nil {
		return errors.Wrap(err, "roleGrants()")
	}

	for permission, grants := range perms {
		if grants.ScopeWide {
			if err := u.store.removeScopeWideGrant(ctx, scope, role, permission); err != nil {
				return err
			}
		}
		for _, resource := range grants.Resources {
			if err := u.store.removeGrants(ctx, scope, role, permission, resource); err != nil {
				return err
			}
		}
	}

	return nil
}

// DeleteUserRoles removes user's memberships of roles held in scope. The
// operation succeeds regardless of whether the roles were previously assigned
// to the user.
func (u *userManager) DeleteUserRoles(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, roles ...accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	for _, role := range roles {
		if err := u.store.deleteUserRole(ctx, scope, user, role); err != nil {
			return err
		}
	}

	return nil
}

// UserRoles returns the roles user holds now, keyed by where each is held: the
// memberships of their own and the roles of every group they are in
// (heldThroughGroups), less the ones outside their period. Every place with no
// scopes given, the roles held in each given scope otherwise.
func (u *userManager) UserRoles(ctx context.Context, user accesstypes.User, scopes ...accesstypes.PolicyScope) (accesstypes.RoleCollection, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	userRoles := make(accesstypes.RoleCollection)
	if len(scopes) == 0 {
		memberships, err := u.store.userMemberships(ctx, user)
		if err != nil {
			return nil, err
		}
		for i := range memberships {
			userRoles[memberships[i].Scope] = append(userRoles[memberships[i].Scope], memberships[i].Role)
		}
	}

	for _, scope := range scopes {
		roles, err := u.store.userRoles(ctx, scope, user)
		if err != nil {
			return nil, err
		}

		userRoles[scope] = roles
	}
	if err := u.heldThroughGroups(ctx, user, userRoles, scopes); err != nil {
		return nil, err
	}

	return userRoles, nil
}

// UserPermissions returns user's effective permissions in each scope,
// answered from the policy snapshot. At least one scope is required: access
// holds no tenant list of its own.
func (u *userManager) UserPermissions(ctx context.Context, user accesstypes.User, scopes ...accesstypes.Scope) (accesstypes.UserPermissionCollection, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if len(scopes) == 0 {
		return nil, httpio.NewBadRequestMessage("at least one scope is required")
	}

	userPermissions := make(accesstypes.UserPermissionCollection)
	for _, scope := range scopes {
		permissions, err := u.evaluator.userPermissions(ctx, user, scope)
		if err != nil {
			return nil, err
		}

		userPermissions[scope] = permissions
	}

	return userPermissions, nil
}

// AddRole creates a custom role held in scope, with the description saying
// what it is for. The scope is an opaque partition label: its validity is
// the caller's business. The name follows the one grammar every role name
// does — letters and digits starting with a capital letter — and a name
// outside it is refused the way the role file refuses one; a default role's
// name is taken, and a role without a description is refused.
func (u *userManager) AddRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, description string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if role == "" {
		return httpio.NewBadRequestMessage("role cannot be empty string")
	}
	if !roleNamePattern.MatchString(string(role)) {
		return httpio.NewBadRequestMessagef("role %s: %s", string(role), roleNameRule)
	}
	if strings.TrimSpace(description) == "" {
		return httpio.NewBadRequestMessagef("role %q carries no description; a role carries one or two plain sentences saying what it is for", string(role))
	}
	if isSuperAdministratorName(role) {
		return httpio.NewConflictMessagef("role %s: %s; choose another name", string(role), superAdministratorReserved)
	}
	if u.defaults.exists(scope, role) {
		return httpio.NewConflictMessagef("role %q is one of the release's default roles; a custom role cannot take its name", string(role))
	}

	// A name is taken under any spelling: a directory names roles without regard
	// to case, so two roles that differ only in case could never be told apart.
	held, roleDoesExist, err := u.ResolveRole(ctx, scope, string(role))
	if err != nil {
		return err
	}
	if roleDoesExist && held == role {
		return httpio.NewConflictMessagef("role %q already exists", string(role))
	}
	if roleDoesExist {
		return httpio.NewConflictMessagef("role %q differs only in case from the role %q held in scope %s; a directory names roles without regard to case, so one spelling is held", string(role), string(held), scope)
	}

	if err := u.store.addRole(ctx, scope, role, description); err != nil {
		return err
	}

	return nil
}

// Roles returns the roles that exist in scope, sorted by name, each with its
// description: the default roles of scope's kind, described by the role
// file, and the custom roles held in scope, and in every domain for one
// domain, described as AddRole was told. A name is listed once however many
// places hold it, with the description of the first place that does: a
// default before a custom role it shadows, the scope's own custom role
// before one held in every domain.
func (u *userManager) Roles(ctx context.Context, scope accesstypes.PolicyScope) ([]RoleInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	roles := u.defaults.list(scope)
	customs, err := u.store.roles(ctx, scope)
	if err != nil {
		return nil, err
	}
	roles = append(roles, customs...)
	if _, oneDomain := scope.Domain(); oneDomain {
		everywhere, err := u.store.roles(ctx, accesstypes.EveryDomainPolicyScope())
		if err != nil {
			return nil, err
		}
		roles = append(roles, everywhere...)
	}

	return listedOnce(roles), nil
}

// listedOnce keeps the first entry of each name, in the order the places
// were asked, and sorts the result by name.
func listedOnce(roles []RoleInfo) []RoleInfo {
	seen := make(map[accesstypes.Role]struct{}, len(roles))
	once := make([]RoleInfo, 0, len(roles))
	for _, role := range roles {
		if _, dup := seen[role.Name]; dup {
			continue
		}
		seen[role.Name] = struct{}{}
		once = append(once, role)
	}
	slices.SortFunc(once, compareRoleInfo)

	return once
}

// DeleteRole removes a custom role held in scope with its grants. It refuses
// a default role, a role users are assigned to in scope, and a role a group
// holds there, each now or for a period that has not begun; assignments of it
// that have ended, a person's and a group's, go with it. A role deleted from
// under a group would leave the group's row behind, and a role of the name
// made again would at once be held by everyone in the group.
func (u *userManager) DeleteRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if u.defaults.exists(scope, role) {
		return false, httpio.NewBadRequestMessagef("role %q is one of the release's default roles; it cannot be deleted", string(role))
	}

	if hasUsers, err := u.hasUsersAssigned(ctx, scope, role); err != nil {
		return false, errors.Wrap(err, "client.hasUsersAssigned()")
	} else if hasUsers {
		return false, httpio.NewBadRequestMessagef("Users assigned to the role. You cannot delete a role that has users assigned")
	}

	deleted, err := u.store.deleteRole(ctx, scope, role)
	if errors.Is(err, policy.ErrHeldByGroup) {
		return false, httpio.NewBadRequestMessagef("A group holds the role, now or for a period that has not begun yet. You cannot delete a role a group holds: take it away from the group first")
	}
	if errors.Is(err, policy.ErrStillAssigned) {
		// The listing above reads who holds the role now: what stands in the way
		// is an assignment whose period has not begun.
		return false, httpio.NewBadRequestMessagef("Users are assigned to the role for a period that has not begun yet. You cannot delete a role that has users assigned")
	}
	if err != nil {
		return false, err
	}

	return deleted, nil
}

// AddRolePermission grants permission to role scope-wide: the permission is
// held with no resource attachment. A permission the running release does not
// have is refused (requireDeclaredPermission).
func (u *userManager) AddRolePermission(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := u.requireCustomRole(ctx, scope, role, "Permissions cannot be added to a role that doesn't exist"); err != nil {
		return err
	}
	if err := u.requireDeclaredPermission(permission); err != nil {
		return err
	}

	if err := u.store.addScopeWideGrant(ctx, scope, role, permission); err != nil {
		return err
	}

	return nil
}

// DeleteRolePermission removes a scope-wide permission from role.
func (u *userManager) DeleteRolePermission(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := u.requireCustomRole(ctx, scope, role, "Permissions cannot be removed from a role that doesn't exist"); err != nil {
		return err
	}

	if err := u.store.removeScopeWideGrant(ctx, scope, role, permission); err != nil {
		return err
	}

	return nil
}

// AddRolePermissionResources grants resource-specific permissions to role.
func (u *userManager) AddRolePermissionResources(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission, resources ...accesstypes.Resource) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := u.requireCustomRole(ctx, scope, role, "Permissions cannot be added to a role that doesn't exist"); err != nil {
		return err
	}

	rows := make([]GrantRow, 0, len(resources))
	for _, resource := range resources {
		rows = append(rows, GrantRow{Permission: permission, Resource: resource})
	}

	return u.addGrantRows(ctx, scope, role, rows)
}

// AddRoleGrants writes the grants to role as one store write.
func (u *userManager) AddRoleGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, grants ...GrantRow) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := u.requireCustomRole(ctx, scope, role, "Permissions cannot be added to a role that doesn't exist"); err != nil {
		return err
	}

	return u.addGrantRows(ctx, scope, role, grants)
}

// addGrantRows validates the rows and hands them to the store as one write.
func (u *userManager) addGrantRows(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, rows []GrantRow) error {
	if err := requireResources(rows); err != nil {
		return err
	}
	if err := u.requireDeclared(scope, role, rows); err != nil {
		return err
	}

	if err := u.store.addGrants(ctx, scope, role, rows); err != nil {
		return err
	}

	return nil
}

// ChangeRoleGrants removes the removals from role and adds the additions to it
// as one store write.
func (u *userManager) ChangeRoleGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, removals, additions []GrantRow) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := u.requireCustomRole(ctx, scope, role, "Permissions cannot be changed on a role that doesn't exist"); err != nil {
		return err
	}
	if err := requireResources(removals); err != nil {
		return err
	}
	if err := requireResources(additions); err != nil {
		return err
	}
	// A removal is held to nothing: a grant the release no longer declares is one
	// to take away.
	if err := u.requireDeclared(scope, role, additions); err != nil {
		return err
	}

	if err := u.store.changeGrants(ctx, scope, role, removals, additions); err != nil {
		return err
	}

	return nil
}

// requireResources refuses a grant row that names no resource.
func requireResources(rows []GrantRow) error {
	for _, row := range rows {
		if row.Resource == "" {
			return httpio.NewBadRequestMessage("resource cannot be empty string")
		}
	}

	return nil
}

// requireDeclared refuses a grant a role file could not give the role, before
// it is written to a custom role held in scope: the whole of the rule the role
// file's grants are held to when the engine opens (expandRoleGrants), so a role
// made at run time holds nothing a default role could not. The rule is
// grantRule, and each part of it is said here in its own sentence
// (brokenRule.badRequest).
//
// The names come first: a permission on a resource or a field the permission
// collection does not list under it, and an action no role is given, so a role
// carries no name nobody declared.
//
// Then what a listed name can still break, in the role file's own sentences
// (misplacedGrant, validateGrantCondition): the resource is of the role's
// scope, a global resource for a role held globally and a domain's for one
// held in a domain or in every domain; Update is not given on what the release
// marks immutable, a grant of all a resource's fields reaching no such field
// (snapshot.allFieldsLeaveOut); a condition parses and reads only the attributes the
// resource declares and the subject vocabulary the application declares, a
// field's condition being held to its resource's attributes; and, in an engine
// whose role file was handed over under an auth's name, the condition reads no
// set of another auth's people (validateGrantSetAuth), since the custom role is
// a role of that auth.
//
// A load of the policy holds a role's stored grant to the same rule
// (snapshot.intern): a grant refused here is one the load would skip and
// report, for the same reason. A stored grant can still break the rule, after
// a rollback, once a release drops a resource or an attribute, or when a row
// is written straight to the store, and that is the grant the load skips.
//
// A name the store cannot hold (splitGrantResource) is left to the store, which
// refuses it in its own words.
func (u *userManager) requireDeclared(scope accesstypes.PolicyScope, role accesstypes.Role, rows []GrantRow) error {
	if len(rows) == 0 {
		return nil
	}

	rule := newGrantRule(u.collection, u.defaults.authName())
	for _, row := range rows {
		base, field, err := splitGrantResource(row.Resource)
		if err != nil {
			continue
		}
		grant := &policy.Grant{
			Scope:     scope,
			Subject:   policy.Subject{Kind: policy.SubjectRole, Name: string(role)},
			Perm:      row.Permission,
			Resource:  base,
			Field:     field,
			Condition: row.Condition,
		}
		if broken := rule.refusal(grant, parseAuthoredCondition); broken != nil {
			return broken.badRequest(role, row.Permission, row.Resource, base)
		}
	}

	return nil
}

// badRequest is the refusal of a write that would give role the grant: permission on
// asked, the resource as the caller named it, whose base resource is base. Each part
// of the rule is said in its own sentence: the names as what was asked and why, with
// what a role is given; the place and the condition in the role file's sentences,
// which name the role.
func (b *brokenRule) badRequest(role accesstypes.Role, permission accesstypes.Permission, asked accesstypes.Resource, base string) error {
	switch b.part {
	case noRoleActionPart:
		return httpio.NewBadRequestMessagef(
			"%s on %s cannot be given to a role: %s. Nothing asks for a grant of it, so the grant would do nothing",
			permission, asked, plainText(b.reason),
		)
	case namesPart:
		return httpio.NewBadRequestMessagef(
			"%s on %s cannot be given to a role: %s. A role is given a permission the application declares on a resource or a field; check the permission and the name",
			permission, asked, plainText(b.reason),
		)
	case conditionPart:
		err := grantConditionError(b.reason, role, permission, accesstypes.Resource(base))

		return httpio.NewBadRequestMessageWithError(err, plainText(err))
	default:
		return httpio.NewBadRequestMessageWithError(b.reason, plainText(b.reason))
	}
}

// requireDeclaredPermission is requireDeclared for a scope-wide grant, which
// names a permission and nothing else: the permission is one the running
// release has, which is one its permission collection lists. A role given a
// permission no release has would show it in the role's editor and hold nothing
// by it. The rule is the one a load of the policy holds a stored scope-wide
// grant to (unlistedName), so a grant refused here is one a load would skip; a
// removal is held to nothing.
func (u *userManager) requireDeclaredPermission(permission accesstypes.Permission) error {
	if err := unlistedName(u.collection.List(), &policy.Grant{Perm: permission}); err != nil {
		return httpio.NewBadRequestMessagef(
			"%s cannot be given to a role: %s. A role is given a permission the application declares; check the permission's name",
			permission, plainText(err),
		)
	}

	return nil
}

// AddRoleGrant grants permission on one resource to role, limited by
// condition ("" is unconditional).
func (u *userManager) AddRoleGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission, resource accesstypes.Resource, condition string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := u.requireCustomRole(ctx, scope, role, "Permissions cannot be added to a role that doesn't exist"); err != nil {
		return err
	}
	if resource == "" {
		return httpio.NewBadRequestMessage("resource cannot be empty string")
	}
	if err := u.requireDeclared(scope, role, []GrantRow{{Permission: permission, Resource: resource, Condition: condition}}); err != nil {
		return err
	}

	if err := u.store.addGrant(ctx, scope, role, permission, resource, condition); err != nil {
		return err
	}

	return nil
}

// DeleteRolePermissionResources removes resource-specific permissions from role.
func (u *userManager) DeleteRolePermissionResources(
	ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission, resources ...accesstypes.Resource,
) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := u.requireCustomRole(ctx, scope, role, "Permissions cannot be removed from a role that doesn't exist"); err != nil {
		return err
	}

	for _, resource := range resources {
		if err := u.store.removeGrants(ctx, scope, role, permission, resource); err != nil {
			return err
		}
	}

	return nil
}

// DeleteRoleGrant removes one grant: permission on resource under condition
// ("" is the unconditional grant). Other conditions on the resource stay.
func (u *userManager) DeleteRoleGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, permission accesstypes.Permission, resource accesstypes.Resource, condition string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()
	u, announce := u.holdAnnouncement()
	defer announce()

	if err := u.requireCustomRole(ctx, scope, role, "Permissions cannot be removed from a role that doesn't exist"); err != nil {
		return err
	}

	if err := u.store.removeGrant(ctx, scope, role, permission, resource, condition); err != nil {
		return err
	}

	return nil
}

// RoleUsers returns the users who hold role in scope now, each once, sorted: by
// a membership of their own held there, and through a group that holds the
// role there (RoleHolders says which way).
func (u *userManager) RoleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]accesstypes.User, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	holders, err := u.RoleHolders(ctx, scope, role)
	if err != nil {
		return nil, err
	}
	users := make([]accesstypes.User, 0, len(holders))
	for i := range holders {
		users = append(users, holders[i].User)
	}

	return users, nil
}

// RoleHolders returns who holds role in scope now, sorted by user, each with
// how they hold it: by a membership of their own held there (Own), through
// the groups they are in that hold it there (Groups, sorted by name), or
// both. A group's role its members do not hold (groupRoleRefusal) makes
// nobody a holder.
func (u *userManager) RoleHolders(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]RoleHolder, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	own, err := u.store.roleUsers(ctx, scope, role)
	if err != nil {
		return nil, err
	}
	byUser := make(map[accesstypes.User]*RoleHolder, len(own))
	holder := func(user accesstypes.User) *RoleHolder {
		if _, listed := byUser[user]; !listed {
			byUser[user] = &RoleHolder{User: user, Own: false, Groups: nil}
		}

		return byUser[user]
	}
	for _, user := range own {
		holder(user).Own = true
	}
	groups, err := u.store.roleGroups(ctx, scope, role)
	if err != nil {
		return nil, err
	}
	for _, group := range groupInfos(groups) {
		if groupRoleRefusal(group.Scope, scope, role) != "" {
			continue
		}
		members, err := u.store.groupUsers(ctx, group.ID)
		if err != nil {
			return nil, err
		}
		for _, user := range members {
			h := holder(user)
			h.Groups = append(h.Groups, group)
		}
	}
	holders := make([]RoleHolder, 0, len(byUser))
	for _, h := range byUser {
		holders = append(holders, *h)
	}
	slices.SortFunc(holders, func(a, b RoleHolder) int { return strings.Compare(string(a.User), string(b.User)) })

	return holders, nil
}

// RolePermissions returns the permissions role holds in scope, answered from
// the policy snapshot.
func (u *userManager) RolePermissions(ctx context.Context, scope accesstypes.Scope, role accesstypes.Role) (accesstypes.RolePermissionCollection, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	roleFound, err := u.roleExists(ctx, scope.PolicyScope(), role)
	if err != nil {
		return nil, err
	}
	if !roleFound {
		return nil, httpio.NewNotFoundMessagef("role %s doesn't exist", role)
	}

	permissions, err := u.evaluator.rolePermissions(ctx, role, scope)
	if err != nil {
		return nil, err
	}

	return permissions, nil
}

// RoleGrants returns the role's resource grants, keyed by permission and
// resource, each with the condition the resource is granted under (sorted by
// it; "" is the unconditional grant). A custom role's stored row that a load of
// the policy leaves out is listed with why (RoleGrant.Skipped).
func (u *userManager) RoleGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (map[accesstypes.Permission]map[accesstypes.Resource][]RoleGrant, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if set, ok := u.defaults.grantsOf(scope, role); ok {
		return set.listed(), nil
	}

	held, err := u.customRoleScope(ctx, scope, role)
	if err != nil {
		return nil, err
	}
	if held == nil {
		return nil, httpio.NewNotFoundMessagef("role %s doesn't exist", role)
	}

	rows, err := u.store.roleResourceGrants(ctx, *held, role)
	if err != nil {
		return nil, err
	}

	return u.listStoredGrants(*held, role, rows), nil
}

// listStoredGrants renders a custom role's stored rows on resources in the shape
// RoleGrants speaks, each resource's grants sorted by condition. held is where the role
// is held, the scope a load of the policy reads its rows in.
//
// Each row is asked what a load of the policy asks of it (grantRule.unusable, with the
// load's reading of a condition), and a row the load leaves out is marked with the
// load's reason: the store holds it and the role does not, so a listing that showed it
// as any other row would show a grant nobody is given.
func (u *userManager) listStoredGrants(held accesstypes.PolicyScope, role accesstypes.Role, rows []policy.RoleGrant) map[accesstypes.Permission]map[accesstypes.Resource][]RoleGrant {
	rule := newGrantRule(u.collection, u.defaults.authName())
	out := make(map[accesstypes.Permission]map[accesstypes.Resource][]RoleGrant)
	for _, row := range rows {
		listed := RoleGrant{Condition: row.Condition}
		grant := &policy.Grant{
			Scope:     held,
			Subject:   policy.Subject{Kind: policy.SubjectRole, Name: string(role)},
			Perm:      row.Perm,
			Resource:  row.Resource,
			Field:     row.Field,
			Condition: row.Condition,
		}
		if reason := rule.unusable(grant, true, parseStoredCondition); reason != nil {
			listed.Skipped = plainText(reason)
		}
		if out[row.Perm] == nil {
			out[row.Perm] = make(map[accesstypes.Resource][]RoleGrant)
		}
		res := joinResourceField(row.Resource, row.Field)
		out[row.Perm][res] = append(out[row.Perm][res], listed)
	}
	for _, resources := range out {
		for _, grants := range resources {
			slices.SortFunc(grants, func(a, b RoleGrant) int {
				return strings.Compare(a.Condition, b.Condition)
			})
		}
	}

	return out
}

// ResolveRole returns the role scope holds under name, matched without regard
// to case, in the spelling the store holds it: a default role of scope's kind,
// a custom role held in scope, or — for one domain — a custom role held in
// every domain. The exact spelling wins when it exists; otherwise the one role
// that matches case-insensitively; two such roles with no exact match is an
// error, never a guess (AddRole and the role file refuse the pair, so only a
// store written before this rule can hold one).
func (u *userManager) ResolveRole(ctx context.Context, scope accesstypes.PolicyScope, name string) (accesstypes.Role, bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	roles, err := u.Roles(ctx, scope)
	if err != nil {
		return "", false, err
	}
	var matches []accesstypes.Role
	for _, role := range roles {
		if role.Name == accesstypes.Role(name) {
			return role.Name, true, nil
		}
		if strings.EqualFold(string(role.Name), name) {
			matches = append(matches, role.Name)
		}
	}
	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		return "", false, httpio.NewConflictMessagef("role %q matches %d roles in scope %s that differ only in case (%v); a directory names roles without regard to case, so rename all but one", name, len(matches), scope, matches)
	}
}

// refuseSuperAdministratorOutsideGlobal refuses a membership of the Super
// Administrator role held anywhere but the global partition: a tenant has no
// super administrator, and the role is no tenant's to give. Every write of a
// membership asks it first, so the refusal says the rule instead of answering
// that the role does not exist there.
func refuseSuperAdministratorOutsideGlobal(scope accesstypes.PolicyScope, role accesstypes.Role) error {
	if role != accesstypes.SuperAdministrator || scope.IsGlobal() {
		return nil
	}

	return httpio.NewBadRequestMessagef("the %s role is held for the whole application alone, never in a tenant or in every tenant: give it with no tenant named", string(role))
}

// roleExists reports whether role exists in scope under exactly that spelling:
// a default role of scope's kind, a custom role held in scope, or — for one
// domain — a custom role held in every domain. The membership and grant
// methods take a role as it is held; ResolveRole is where a name from outside
// finds its spelling.
func (u *userManager) roleExists(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	return u.roleExistsBy(ctx, scope, role, u.store.roleExists)
}

// roleReader reports whether the roles table holds the custom role in exactly the scope:
// the store's own read, or a caller's over its transaction (Membership).
type roleReader func(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error)

// roleExistsBy is roleExists with the read of the roles table the caller names, so the
// rule is one whoever reads.
func (u *userManager) roleExistsBy(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, held roleReader) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if u.defaults.exists(scope, role) {
		return true, nil
	}

	where, err := customRoleScopeBy(ctx, scope, role, held)
	if err != nil {
		return false, err
	}

	return where != nil, nil
}

// customRoleScope finds where the custom role a request in scope would see is
// held: scope itself, or every domain when scope is one domain; nil when no
// custom role of that name is held in either.
func (u *userManager) customRoleScope(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (*accesstypes.PolicyScope, error) {
	return customRoleScopeBy(ctx, scope, role, u.store.roleExists)
}

// customRoleScopeBy is customRoleScope with the read of the roles table the caller names.
func customRoleScopeBy(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, held roleReader) (*accesstypes.PolicyScope, error) {
	exists, err := held(ctx, scope, role)
	if err != nil {
		return nil, err
	}
	if exists {
		return &scope, nil
	}
	if _, oneDomain := scope.Domain(); !oneDomain {
		return nil, nil
	}
	everywhere := accesstypes.EveryDomainPolicyScope()
	exists, err = held(ctx, everywhere, role)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	return &everywhere, nil
}

// requireCustomRole errors unless role is a custom role held in scope itself:
// a default role's grants are the release's, and a custom role held in every
// domain is changed where it is held, not through one domain.
func (u *userManager) requireCustomRole(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, notFoundMsg string) error {
	if u.defaults.exists(scope, role) {
		return httpio.NewBadRequestMessagef("role %q is one of the release's default roles; its grants are the release's. Copy them into a custom role to change them", string(role))
	}

	exists, err := u.store.roleExists(ctx, scope, role)
	if err != nil {
		return err
	}
	if !exists {
		return httpio.NewNotFoundMessage(notFoundMsg)
	}

	return nil
}

func (u *userManager) hasUsersAssigned(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	users, err := u.store.roleUsers(ctx, scope, role)
	if err != nil {
		return false, errors.Wrap(err, "roleUsers()")
	}

	return len(users) > 0, nil
}
