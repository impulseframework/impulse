package members

import (
	"context"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
)

// AdministratorRoles are the roles that count as an administrator's, by where they are
// held: the ones whose holder can give a role back. A person removes one held in a tenant
// from themselves only while another person holds one there
// (identity.WithAdministratorRoles), so nobody is the last to leave and a tenant always
// has someone who manages its people. The application as a whole is kept by the Super
// Administrator role instead (accesstypes.SuperAdministrator), whose last holder keeps it
// whoever asks. A role that manages nothing of people is not among them, whoever holds it.
func AdministratorRoles() (global, everyDomain []accesstypes.Role) {
	return nil, []accesstypes.Role{"TenantAdministrator"}
}

// FirstAdministratorRoles are the application's roles the first administrator's
// invitation gives, by where they are held, beside the Super Administrator role every
// first administrator holds (firstAdministratorRoleNames): the administrator's roles and
// what else the development identities hold (cmd/bootstrap/users.json), so the first
// administrator can do what the development logins can.
func FirstAdministratorRoles() (global, everyDomain []accesstypes.Role) {
	return nil, []accesstypes.Role{"TenantAdministrator"}
}

// AdministratorVariable is the setting the placement names the auth's first
// administrator in, for each environment: the person's email address. The data level
// reads it, and the migrate command hands it to InviteAdministrator.
const AdministratorVariable = "APP_MEMBERS_ADMINISTRATOR"

// InviteAdministrator invites the auth's first administrator, the way into a deployed
// environment: while the auth has no account, the migrate command has an invitation
// mailed to email, the address the placement names (AdministratorVariable), which gives
// the Super Administrator role and the application's own roles for the first
// administrator (FirstAdministratorRoles). The person opens the link and signs in by a
// method of the auth, as everyone invited after them does.
// Through a directory the invitation is what ties the sign-in to the roles, whoever
// else the directory admits and however many organizations the method is open to, and
// the address the directory gives must be the invited one.
// No account is created ahead of them and no password is generated, so nothing stands
// that anybody else could sign in with. From there they invite the others. The
// development bootstrap creates its own logins instead (cmd/bootstrap/users.json).
//
// Every release runs it, and it answers what it did (session.FirstAdministrator, never
// the link): an invitation that stands for the address is left as it is and nothing is
// mailed; one that ran out (seven days) is replaced by a new one; one that stands for an
// address the placement no longer names, or for roles the release no longer names, is
// withdrawn, so its link no longer opens, and the address named now is mailed. Once the
// auth has an account nobody is invited: every person after the first comes from the
// administration surface. An invitation of the first administrator's that still stands
// then, for an address the placement no longer names, is withdrawn all the same.
//
// It refuses, with nothing mailed, while the auth has no account and the placement names
// no address, and while the application has no mailbox to send as: an invitation's link
// is never written to a log, since whoever opens it becomes the administrator, so the
// mailbox comes before the first release.
func (a *Auth) InviteAdministrator(ctx context.Context, email string) (*session.FirstAdministrator, error) {
	roles, err := firstAdministratorRoleNames()
	if err != nil {
		return nil, err
	}
	invited, err := a.handlers.API().InviteFirstAdministrator(ctx, &session.FirstAdministratorRequest{Email: email, RoleNames: roles})
	if err != nil {
		return nil, errors.Wrapf(err, "the %s auth's first administrator (%s)", Name, AdministratorVariable)
	}

	return invited, nil
}

// ReinviteAdministrator withdraws the first administrator's standing invitation and
// mails a fresh one to email, for a message that was lost: the earlier link no longer
// opens. It is what the migrate command's -reinvite-administrator runs, on demand from
// the deployment's operations. It refuses once the auth has an account, since somebody
// is then there to invite the others, and is InviteAdministrator in every other respect.
func (a *Auth) ReinviteAdministrator(ctx context.Context, email string) (*session.FirstAdministrator, error) {
	roles, err := firstAdministratorRoleNames()
	if err != nil {
		return nil, err
	}
	invited, err := a.handlers.API().ReinviteFirstAdministrator(ctx, &session.FirstAdministratorRequest{Email: email, RoleNames: roles})
	if err != nil {
		return nil, errors.Wrapf(err, "the %s auth's first administrator (%s)", Name, AdministratorVariable)
	}

	return invited, nil
}

// firstAdministratorRoleNames are the roles the first administrator's invitation
// carries: the role the framework defines in every auth (accesstypes.SuperAdministrator),
// held for the whole application, whose holder alone makes another, and the
// application's own (FirstAdministratorRoles). The acceptance gives each role where the
// role file holds it, a global role for the whole application and a domain role in every
// tenant, so the invitation names nothing the file does not define where
// FirstAdministratorRoles says it is held: a role the release does not ship there is
// refused here, before an invitation is mailed that nobody could accept.
func firstAdministratorRoleNames() ([]string, error) {
	defined, err := Roles().Parse()
	if err != nil {
		return nil, errors.Wrap(err, "access.RoleFile.Parse()")
	}
	defines := func(roles []*access.Role, role accesstypes.Role) bool {
		return slices.ContainsFunc(roles, func(r *access.Role) bool {
			return r.Name == role
		})
	}
	global, everyDomain := FirstAdministratorRoles()
	names := []string{string(accesstypes.SuperAdministrator)}
	for _, role := range global {
		if !defines(defined.Roles.Global, role) {
			return nil, errors.Newf("the %s auth's first administrator is given the role %q for the whole application (FirstAdministratorRoles), which the auth's role file does not define as a global role: name a role roles.json defines there", Name, role)
		}
		names = append(names, string(role))
	}
	for _, role := range everyDomain {
		if !defines(defined.Roles.Domain, role) || defines(defined.Roles.Global, role) {
			return nil, errors.Newf("the %s auth's first administrator is given the role %q in every tenant (FirstAdministratorRoles), which the auth's role file does not define as a domain role alone: name a role roles.json defines there", Name, role)
		}
		names = append(names, string(role))
	}

	return names, nil
}
