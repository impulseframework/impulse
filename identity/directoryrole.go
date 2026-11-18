package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/tracer"
)

// directoryRoleNotGiven refuses a role the caller could not give by hand as the role
// everyone from a tenant's directory gets: the role, then the word for a tenant.
const directoryRoleNotGiven = "you may not give the role %s in this %s, so you cannot make it the role everyone from its directory gets: " +
	"choose a role you may give, or ask someone who may give this one"

// DirectoryRole holds the role a tenant is opened to its directory's people with to its
// rules, in the caller's transaction, and writes nothing. The role is given to every
// person the directory vouches for at their first sign-in through it
// (DirectorySeated), to people nobody named, so the person who names it is held to what
// giving it by hand needs:
//
//   - the role exists in the tenant, by the rule a membership's role is held to: a role
//     of the release's role file held in a tenant, or a custom role of this tenant;
//   - the caller could give it there directly: the grant of the method that gives a
//     role in the tenant admits it for that role (RoleGrants.MayGive). The grant is
//     asked for no person in particular, since the people are not known yet, so a grant
//     whose condition reads the person a role is given to admits no default role.
//
// A role the caller may not give is Forbidden, and the grant answers before the role is
// looked for, so the refusal says the same of a role the tenant holds and of one it
// does not: a caller learns of no role the grant keeps from them. A role the caller
// could give that the tenant does not hold is a BadRequest. Each is a sentence the
// person can act on. A call outside any request is the application's own act and is
// not judged by the grant; a manager a request reaches without the role grants cannot
// judge it, which is an error and never a pass.
func (m *Manager) DirectoryRole(ctx context.Context, txn resource.ReadWriteTransaction, tenant accesstypes.Domain, role accesstypes.Role) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	scope := accesstypes.DomainPolicyScope(tenant)
	judged, err := m.judged(ctx)
	if err != nil {
		return err
	}
	if judged {
		may, err := m.grants.MayGive(ctx, txn, scope, impulse.NilUUID, role)
		if err != nil {
			return errors.Wrap(err, "identity.RoleGrants.MayGive()")
		}
		if !may {
			return httpio.NewForbiddenMessagef(directoryRoleNotGiven, string(role), m.tenantWord)
		}
	}
	exists, err := m.roleExistsIn(ctx, txn, role, []accesstypes.PolicyScope{scope})
	if err != nil {
		return err
	}
	if !exists {
		return httpio.NewBadRequestMessagef("%q is not a role of this %s: choose one of the roles the picker lists", string(role), m.tenantWord)
	}

	return nil
}

// DirectoryRoles lists the roles a tenant may be opened to its directory's people with,
// as the dialog that opens it offers them: the roles that exist in the tenant
// (TenantRoles) that the request's caller could give there directly, by the question
// DirectoryRole asks of the grant of the method that gives a role (RoleGrants.MayGive,
// for no person in particular), and no other. A page offers what the caller may do: a
// role the list leaves out is one DirectoryRole refuses, and one it offers is one
// DirectoryRole takes. A call outside any request is the application's own and lists
// every role of the tenant; a manager a request reaches without the role grants cannot
// judge it, which is an error and never a list.
//
// It is a list's read and is called outside any transaction, with the database to ask
// in (db). The tenant's roles are read first, as TenantRoles reads them: the permission
// engine reads the custom ones on a client of its own, and the database refuses such a
// read under a transaction's context. The grant of a tenant's method may read a row, so
// the questions are then asked in one transaction of the list's own, which writes
// nothing.
func (m *Manager) DirectoryRoles(ctx context.Context, db resource.Executor, tenant accesstypes.Domain) ([]access.RoleInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	roles, err := m.TenantRoles(ctx, tenant)
	if err != nil {
		return nil, err
	}
	judged, err := m.judged(ctx)
	if err != nil {
		return nil, err
	}
	if !judged {
		return roles, nil
	}
	scope := accesstypes.DomainPolicyScope(tenant)
	var offered []access.RoleInfo
	if err := db.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		// The database may run the function again, so each run starts its own list.
		var err error
		offered, err = rolesOffered(roles, func(role accesstypes.Role) (bool, error) {
			may, err := m.grants.MayGive(ctx, txn, scope, impulse.NilUUID, role)
			if err != nil {
				return false, errors.Wrap(err, "identity.RoleGrants.MayGive()")
			}

			return may, nil
		})

		return err
	}); err != nil {
		return nil, errors.Wrap(err, "resource.Executor.ExecuteFunc()")
	}

	return offered, nil
}

// RolesToGive lists the roles a person picks from to give one in a tenant by hand, as
// the dialogs of the methods that give a role offer them: the roles that exist in the
// tenant (TenantRoles) that the request's caller could give there by one of those
// methods (RoleGrants.CouldGive), and no other. A page offers what the caller may do: a
// role the list leaves out is one every such method refuses this caller, whoever it is
// given to, so a role file says which roles a person hands out once, on the methods'
// grants, and the list follows. One list serves the pickers of all of them, so it holds
// a role that one of them takes and another refuses, which the method then says. A call
// outside any request is the application's own and lists every role of the tenant; a
// manager a request reaches without the role grants cannot judge it, which is an error
// and never a list.
//
// It is a list's read, called outside any transaction, and opens none: the question
// reads no row (DirectoryRoles asks for nobody in particular, which may).
func (m *Manager) RolesToGive(ctx context.Context, tenant accesstypes.Domain) ([]access.RoleInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	roles, err := m.TenantRoles(ctx, tenant)
	if err != nil {
		return nil, err
	}
	judged, err := m.judged(ctx)
	if err != nil {
		return nil, err
	}
	if !judged {
		return roles, nil
	}
	scope := accesstypes.DomainPolicyScope(tenant)

	return rolesOffered(roles, func(role accesstypes.Role) (bool, error) {
		could, err := m.grants.CouldGive(ctx, scope, role)
		if err != nil {
			return false, errors.Wrap(err, "identity.RoleGrants.CouldGive()")
		}

		return could, nil
	})
}

// rolesOffered are the roles of a tenant a list offers the caller: each role may
// answers yes for, in the order they came. The lists a role is picked from share it
// (DirectoryRoles, RolesToGive), each with its own question of the role file's grants.
func rolesOffered(roles []access.RoleInfo, may func(accesstypes.Role) (bool, error)) ([]access.RoleInfo, error) {
	offered := make([]access.RoleInfo, 0, len(roles))
	for _, role := range roles {
		yes, err := may(role.Name)
		if err != nil {
			return nil, err
		}
		if yes {
			offered = append(offered, role)
		}
	}

	return offered, nil
}

// DirectoryName answers what the auth's sign-in registrations call a directory, for a
// sentence that names it to a person: the name a registration that takes its connection
// gives it, and the label of the button a person signs in to it with, each "" where the
// registrations have none (SessionAPI.DirectoryName). It is a resource.DirectoryNamer:
// the generated frames hand it to the rules of a tenant's directory
// (resource.TenantDirectoryRules.NamedBy) and to the tenant list
// (resource.WithDirectoryNames), so a warning, a refusal and a closed tenant's reason
// say "Lakeside" or "your organization's Microsoft directory" and never a tenant id.
func (m *Manager) DirectoryName(directory signin.Directory) (name, label string) {
	return m.sessions.DirectoryName(directory.Method, directory.Connection)
}
