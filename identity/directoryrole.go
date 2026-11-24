package identity

import (
	"context"
	"fmt"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/tracer"
)

// The refusal of a role the caller could not give by hand as the role everyone from a
// tenant's directory gets. directoryRoleNotGiven is the one sentence the caller is told,
// with the role in words (roleInWords), then the word for a tenant: it is the role the
// request named, so it reads the same of a role the tenant holds and of one it does
// not. directoryRoleNotGivenCause is the refusal's cause, kept for the server's log:
// the role's code name, then the word for a tenant.
const (
	directoryRoleNotGiven      = "You may not give the role %s in this %s, so you cannot make it the role everyone from its directory gets."
	directoryRoleNotGivenCause = "you may not give the role %s in this %s, so you cannot make it the role everyone from its directory gets: " +
		"choose a role you may give, or ask someone who may give this one"
)

// DirectoryRole holds the role a tenant is opened to its directory's people with to its
// rules, in the caller's transaction, and writes nothing. The role is given to every
// person the directory vouches for when they sign in through it and hold nothing in the
// tenant (DirectorySeated), to people nobody named, so the person who names it is held to what
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
			return httpio.NewForbiddenMessageWithError(
				errors.Newf(directoryRoleNotGivenCause, string(role), m.tenantWord),
				fmt.Sprintf(directoryRoleNotGiven, roleInWords(role), m.tenantWord),
			)
		}
	}
	exists, err := m.roleExistsIn(ctx, txn, role, []accesstypes.PolicyScope{scope})
	if err != nil {
		return err
	}
	if !exists {
		const (
			said  = "%s is not a role of this %s. Choose one of the roles the picker lists."
			cause = "%q is not a role of this %s: choose one of the roles the picker lists"
		)

		return httpio.NewBadRequestMessageWithErrorf(errors.Newf(cause, string(role), m.tenantWord), said, roleInWords(role), m.tenantWord)
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

// RolesToGive lists the roles a person picks from to give one in a tenant by hand with
// one of the tenant's methods (method: the one that gives a person a role, the create
// or the invitation that starts a person with one, or the one that gives a group a
// role, by the name its grant is written under), as the method's dialog offers them:
// the roles that exist in the tenant (TenantRoles) that the request's caller could
// give there with that method (RoleGrants.CouldGive), and no other. A dialog offers
// what its own method takes from the caller: a role the list leaves out is one the
// method refuses this caller, whoever it is given to, so a role file says which roles
// a person hands out once, on each method's grant, and each method's list follows its
// own. A call outside any request is the application's own and lists every role of the
// tenant; a manager a request reaches without the role grants cannot judge it, which
// is an error and never a list.
//
// It is a list's read, called outside any transaction, and opens none: the question
// reads no row (DirectoryRoles asks for nobody in particular, which may).
func (m *Manager) RolesToGive(ctx context.Context, method accesstypes.Resource, tenant accesstypes.Domain) ([]access.RoleInfo, error) {
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
		// A tenant's administrator's role is not offered to a caller the tenant does not
		// admit while it is directory only: the methods refuse it (entersToChange).
		if enters, err := m.entersToChange(ctx, scope, role, nil); err != nil || !enters {
			return false, err
		}
		could, err := m.grants.CouldGive(ctx, method, scope, role)
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

// SeatedAgainNote is the note a removal's warning carries where the tenant is open to its
// directory and the person signs in through it: the directory gives a person who holds
// nothing in the tenant its starting role at every sign-in, so the removal holds until
// their next one. It speaks of the tenant in the application's word for one, and reads
// nothing; whether the tenant is open and the person signs in through its directory is
// the generated identity client's to answer (SeatedAgainBySignIn).
func (t *Tenant) SeatedAgainNote() resource.WarningNote {
	return resource.WarningNote{
		Says: fmt.Sprintf(
			"This %s is open to its directory and this person signs in through it, so they are given the starting role again the next time they sign in. A role with no privileges keeps them out.",
			t.manager.tenantWord,
		),
		Names: nil,
	}
}
