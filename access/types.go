package access

import "github.com/impulseframework/impulse/accesstypes"

// PermissionsListFunc returns available permissions.
type PermissionsListFunc func() []accesstypes.Permission

// RoleInfo is one role as UserManager.Roles lists it: the name it is held
// under and the description saying what it is for — the role file's for a
// default role, the one AddRole was given for a custom role.
type RoleInfo struct {
	Name        accesstypes.Role `json:"name"`
	Description string           `json:"description"`
}

// GrantRow is one grant as the store holds it: Permission on Resource — a bare
// resource name, or Resource.field for one field — limited by Condition, ""
// being unconditional. AddRoleGrants takes rows; the role file's authored
// Grants expand into the same shape.
type GrantRow struct {
	Permission accesstypes.Permission
	Resource   accesstypes.Resource
	Condition  string
}

// RoleGrant is one grant of a permission on a resource as UserManager.RoleGrants lists
// it: the condition the resource is granted under, and whether the role holds the
// grant. A default role's grants are the release's, and it holds each. A custom role's
// are the rows the policy store holds for it, and a row the running release cannot use
// is one a load of the policy leaves out: the role does not hold it, and a check is
// denied what the row alone would permit. Such a row is listed with why, so a page that
// lists a role's grants has the reason in hand where it would show the row.
type RoleGrant struct {
	// Condition is the condition the resource is granted under; "" is the
	// unconditional grant.
	Condition string
	// Skipped is empty for a grant the role holds. For a row a load of the policy
	// leaves out it is why, in the sentence the load reports for the row
	// (SkippedGrant) and the deploy check lists (Client.CheckPolicy).
	Skipped string
}
