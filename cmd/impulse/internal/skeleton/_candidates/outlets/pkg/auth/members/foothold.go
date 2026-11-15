package members

import (
	"context"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
)

// AdministratorRoles are the roles that count as an administrator's, by where they are
// held: the ones whose holder can give a role back. A person removes one from themselves
// only while another person holds one where it is held (identity.WithAdministratorRoles),
// so nobody is the last to leave and the auth always has someone who manages its people.
// A role that manages nothing of people is not among them, whoever holds it.
func AdministratorRoles() (global, everyDomain []accesstypes.Role) {
	return nil, []accesstypes.Role{"TenantAdministrator"}
}

// FirstAdministratorRoles are the roles the first administrator is seeded with, by where
// they are held: the administrator's roles and what else the development identities hold
// (cmd/bootstrap/users.json), so the foothold can do what the development logins can.
func FirstAdministratorRoles() (global, everyDomain []accesstypes.Role) {
	return nil, []accesstypes.Role{"TenantAdministrator"}
}

// SeedAdministrator creates the first administrator under signInName when the auth's users
// table holds no rows and reports whether it did: the foothold a deployed environment
// starts from. The directory proves who someone is and the application assigns the roles
// (signin.DisableRoleSync), and a role is held by an account, so the user is created,
// password-less, under the sign-in name the directory presents (the user principal name on
// Entra, the email on Google), which the placement names per environment, and holds the
// administrator roles from the start (seedFoothold). The person's first sign-in through
// the directory links to it. Once a user exists nothing is seeded: every user after the
// first comes from the administration surface. An empty name is refused while the table is
// empty. The development bootstrap seeds the development identities instead.
func (a *Auth) SeedAdministrator(ctx context.Context, signInName string) (bool, error) {
	empty, err := a.usersTableEmpty(ctx)
	if err != nil {
		return false, err
	}
	if !empty {
		return false, nil
	}
	if signInName == "" {
		return false, errors.Newf("the %s auth's users table holds no rows and no first administrator is named: a deployment names one per environment, and the migrate command seeds nothing without it", Name)
	}
	if err := a.seedFoothold(ctx, &session.CreateUserRequest{Username: signInName}); err != nil {
		return false, err
	}

	return true, nil
}

// seedFoothold seeds the first administrator into an empty users table: the roles first,
// then the user who holds them. A role is held by the id of an account, so the seed
// chooses the id (session.SeedUserID, the same on every run), writes the administrator
// roles under it, and creates the user under it last. The user's row is what says the
// seed is done: a run that stops before it leaves the table empty, and the next deploy
// writes the same memberships again, each left as it is where it exists, and creates the
// user. Once a user exists nothing is seeded, whoever that user is and whatever it holds:
// the seed never gives a role to an account it did not create.
func (a *Auth) seedFoothold(ctx context.Context, req *session.CreateUserRequest) error {
	id := session.SeedUserID(Name, req.Username)
	if err := a.assignAdministrator(ctx, id); err != nil {
		return err
	}
	req.ID = impulse.NullUUIDFromUUID(id)
	if _, err := a.handlers.API().CreateSessionUser(ctx, req); err != nil {
		return errors.Wrapf(err, "session.AuthAPI.CreateSessionUser(): %s", req.Username)
	}

	return nil
}

// assignAdministrator writes the first administrator's memberships, held by the id of the
// user's account, each where its roles are held; a membership that exists is left as it is.
func (a *Auth) assignAdministrator(ctx context.Context, userID impulse.UUID) error {
	global, everyDomain := FirstAdministratorRoles()
	manager := a.access.UserManager()
	user := accesstypes.User(userID.String())
	if len(global) > 0 {
		if err := manager.AddUserRoles(ctx, accesstypes.GlobalPolicyScope(), user, global...); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddUserRoles(): %s in the global partition", userID)
		}
	}
	if len(everyDomain) > 0 {
		if err := manager.AddUserRoles(ctx, accesstypes.EveryDomainPolicyScope(), user, everyDomain...); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddUserRoles(): %s in every domain", userID)
		}
	}

	return nil
}

// usersTableEmpty reads one row of the users table, any row, and reports there is none.
func (a *Auth) usersTableEmpty(ctx context.Context) (bool, error) {
	rows := 0
	iter := a.db.Single().ReadWithOptions(ctx, usersTable, cloudspanner.AllKeys(), []string{"Id"}, &cloudspanner.ReadOptions{Limit: 1})
	if err := iter.Do(func(*cloudspanner.Row) error {
		rows++

		return nil
	}); err != nil {
		return false, errors.Wrapf(err, "spanner.RowIterator.Do(): %s", usersTable)
	}

	return rows == 0, nil
}
