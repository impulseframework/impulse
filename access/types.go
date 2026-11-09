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
