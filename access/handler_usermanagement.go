package access

import (
	"net/http"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/tracer"
)

// The HTTP management surface addresses tenant policy only: the {domain} URL
// parameter is data, so it always names one tenant domain, and the EveryDomain
// forms name every tenant domain structurally — there is no URL spelling for
// the global partition. Global memberships are the directory sync's
// (session.RoleSync) and global grants the release's; a deliberate global
// admin surface is Admin-UI-era work.
const (
	paramUser   httpio.ParamType = "user"
	paramDomain httpio.ParamType = "domain"
	paramRole   httpio.ParamType = "role"
)

// AddRole is the handler to add a custom role held in the domain. The
// request carries the role's name and its description, one or two plain
// sentences saying what the role is for; the manager refuses a role without
// one.
//
// Permissions Required: AddRole
func (a *HandlerClient) AddRole() http.HandlerFunc {
	type request struct {
		RoleName    accesstypes.Role `json:"roleName"`
		Description string           `json:"description"`
	}

	type response struct {
		Role        accesstypes.Role `json:"role"`
		Description string           `json:"description"`
	}

	decoder := newDecoder[request]()

	return a.handler(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		req, err := decoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).BadRequestWithError(ctx, err)
		}

		scope := accesstypes.DomainPolicyScope(httpio.Param[accesstypes.Domain](r, paramDomain))
		if err := a.manager.AddRole(ctx, scope, req.RoleName, req.Description); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		resp := &response{
			Role:        req.RoleName,
			Description: req.Description,
		}

		return httpio.NewEncoder(w).Ok(resp)
	})
}

// AddRoleUsers is the handler to assign a role to a list of users, the
// memberships held in the domain
//
// Permissions Required: AddRoleUsers
func (a *HandlerClient) AddRoleUsers() http.HandlerFunc {
	return a.addRoleUsers(func(r *http.Request) accesstypes.PolicyScope {
		return accesstypes.DomainPolicyScope(httpio.Param[accesstypes.Domain](r, paramDomain))
	})
}

// AddRoleUsersEveryDomain is the handler to assign a role to a list of users,
// the memberships held in every tenant domain
//
// Permissions Required: AddRoleUsers
func (a *HandlerClient) AddRoleUsersEveryDomain() http.HandlerFunc {
	return a.addRoleUsers(func(*http.Request) accesstypes.PolicyScope {
		return accesstypes.EveryDomainPolicyScope()
	})
}

func (a *HandlerClient) addRoleUsers(scopeOf func(*http.Request) accesstypes.PolicyScope) http.HandlerFunc {
	type request struct {
		Users []accesstypes.User `json:"users"`
	}

	decoder := newDecoder[request]()

	return a.handler(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		req, err := decoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).BadRequestWithError(ctx, err)
		}
		role := httpio.Param[accesstypes.Role](r, paramRole)

		if err := a.manager.AddRoleUsers(ctx, scopeOf(r), role, req.Users...); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return nil
	})
}

// DeleteRoleUsers is the handler to delete a list of users from a given role,
// the memberships held in the domain
//
// Permissions Required: DeleteRoleUsers
func (a *HandlerClient) DeleteRoleUsers() http.HandlerFunc {
	return a.deleteRoleUsers(func(r *http.Request) accesstypes.PolicyScope {
		return accesstypes.DomainPolicyScope(httpio.Param[accesstypes.Domain](r, paramDomain))
	})
}

// DeleteRoleUsersEveryDomain is the handler to delete a list of users from a
// given role, the memberships held in every tenant domain
//
// Permissions Required: DeleteRoleUsers
func (a *HandlerClient) DeleteRoleUsersEveryDomain() http.HandlerFunc {
	return a.deleteRoleUsers(func(*http.Request) accesstypes.PolicyScope {
		return accesstypes.EveryDomainPolicyScope()
	})
}

func (a *HandlerClient) deleteRoleUsers(scopeOf func(*http.Request) accesstypes.PolicyScope) http.HandlerFunc {
	type request struct {
		Users []accesstypes.User `json:"users"`
	}

	decoder := newDecoder[request]()

	return a.handler(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		req, err := decoder.Decode(r)
		if err != nil {
			return httpio.NewEncoder(w).BadRequestWithError(ctx, err)
		}
		role := httpio.Param[accesstypes.Role](r, paramRole)

		if err := a.manager.DeleteRoleUsers(ctx, scopeOf(r), role, req.Users...); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return nil
	})
}

// Roles is the handler to get the list of roles that exist in a given domain,
// each with its name and description: the release's domain default roles and
// the custom roles held there or in every domain
//
// Permissions Required: ListRoles
func (a *HandlerClient) Roles() http.HandlerFunc {
	type response struct {
		Roles []RoleInfo `json:"roles,omitempty"`
	}

	return a.handler(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		scope := accesstypes.DomainPolicyScope(httpio.Param[accesstypes.Domain](r, paramDomain))
		roles, err := a.manager.Roles(ctx, scope)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		res := &response{Roles: roles}

		return httpio.NewEncoder(w).Ok(res)
	})
}

// RoleUsers is the handler to the list of users for a given role, the
// memberships held in the domain
//
// Permissions Required: ListRoleUsers
func (a *HandlerClient) RoleUsers() http.HandlerFunc {
	return a.roleUsers(func(r *http.Request) accesstypes.PolicyScope {
		return accesstypes.DomainPolicyScope(httpio.Param[accesstypes.Domain](r, paramDomain))
	})
}

// RoleUsersEveryDomain is the handler to the list of users for a given role,
// the memberships held in every tenant domain
//
// Permissions Required: ListRoleUsers
func (a *HandlerClient) RoleUsersEveryDomain() http.HandlerFunc {
	return a.roleUsers(func(*http.Request) accesstypes.PolicyScope {
		return accesstypes.EveryDomainPolicyScope()
	})
}

func (a *HandlerClient) roleUsers(scopeOf func(*http.Request) accesstypes.PolicyScope) http.HandlerFunc {
	type response []accesstypes.User

	return a.handler(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		role := httpio.Param[accesstypes.Role](r, paramRole)

		roleUsers, err := a.manager.RoleUsers(ctx, scopeOf(r), role)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		resp := response(roleUsers)

		return httpio.NewEncoder(w).Ok(resp)
	})
}

// RolePermissions is the handler to the list of permissions a given role
// holds in the domain, default and custom roles alike
//
// Permissions Required: ListRolePermissions
func (a *HandlerClient) RolePermissions() http.HandlerFunc {
	type response accesstypes.RolePermissionCollection

	return a.handler(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		scope := accesstypes.DomainScope(httpio.Param[accesstypes.Domain](r, paramDomain))
		role := httpio.Param[accesstypes.Role](r, paramRole)

		rolePermissions, err := a.manager.RolePermissions(ctx, scope, role)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		resp := response(rolePermissions)

		return httpio.NewEncoder(w).Ok(resp)
	})
}

// DeleteRole is the handler to delete a custom role held in the domain
//
// Permissions Required: DeleteRole
func (a *HandlerClient) DeleteRole() http.HandlerFunc {
	return a.handler(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		scope := accesstypes.DomainPolicyScope(httpio.Param[accesstypes.Domain](r, paramDomain))
		role := httpio.Param[accesstypes.Role](r, paramRole)

		_, err := a.manager.DeleteRole(ctx, scope, role)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return nil
	})
}
