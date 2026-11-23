package signin

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// SuperAdministratorCheck reports whether user, a person as the permission store names
// them (the id of their account), holds the Super Administrator role
// (accesstypes.SuperAdministrator). An Auth asks it of every person a sign-in would
// admit (session.NewAuth is handed it, in session.SuperAdministratorsBy), inside the
// sign-in's transaction and with its context, so a check that reads a database reads
// outside that transaction.
type SuperAdministratorCheck func(ctx context.Context, user accesstypes.User) (bool, error)

// SuperAdministrators answers the check over the role store: the person's memberships
// in the global partition, read through roles, the access library's user manager, and
// judged by the one test of who is a super administrator
// (accesstypes.RoleCollection.HoldsSuperAdministrator), the test the tenant policy asks
// too. On Spanner pass the manager wrapped by resource.SignInRoles, as to TenantPolicy,
// so the read is made outside the sign-in's transaction. A nil roles is a wiring
// mistake and panics.
func SuperAdministrators(roles UserRoleManager) SuperAdministratorCheck {
	if roles == nil {
		panic("signin: SuperAdministrators was given no role store: pass the access library's user manager")
	}

	return func(ctx context.Context, user accesstypes.User) (bool, error) {
		held, err := roles.UserRoles(ctx, user, accesstypes.GlobalPolicyScope())
		if err != nil {
			return false, errors.Wrap(err, "UserRoleManager.UserRoles()")
		}

		return held.HoldsSuperAdministrator(), nil
	}
}

// SuperAdministratorFactor returns the rule an Auth decides a sign-in by once it knows
// who its super administrators are: policy, the application's own rule (nil for none,
// which allows), and after it the library's: a super administrator signs in with a
// second factor, whatever the application asks of anyone else. A sign-in the policy
// allows is held for the library's second factor, the code it mails
// (sessioninfo.MethodEmailCode), when the person holds the Super Administrator role;
// it then waits for the code unless the sign-in already carries a second factor, as
// for any account: the directory's attestation, or a browser the person asked to be
// remembered, for the period the library remembers one. A decision of the policy that
// is no plain allow stands as it is: a refusal, and a hold for a factor the policy
// names itself. The check's error fails the sign-in.
//
// The library's rule stands down for a sign-in through a method that synchronizes
// roles (the request carries the role names the directory asserted, RoleNames, which
// is never nil for such a method): there the directory is the authority on who is a
// super administrator, as on every role, and how its people prove who they are is the
// directory's to say. The application's own rule is asked of that person as of anyone,
// and what it decides stands; so is the Auth's sign-in setting, which asks no second
// factor of them while it asks none of anyone. The check is not asked.
func SuperAdministratorFactor[Tx any](policy Policy[Tx], check SuperAdministratorCheck) Policy[Tx] {
	if check == nil {
		panic("signin: SuperAdministratorFactor was given no check: pass SuperAdministrators over the access library's user manager")
	}

	return func(ctx context.Context, tx Tx, req *sessioninfo.NewSessionRequest) (*Decision, error) {
		decision := Allow()
		if policy != nil {
			var err error
			decision, err = policy(ctx, tx, req)
			if err != nil {
				return nil, err
			}
		}
		if decision == nil || decision.Outcome() != AllowSignIn {
			return decision, nil
		}
		if req.RoleNames != nil {
			return decision, nil
		}
		super, err := check(ctx, accesstypes.User(req.UserID.String()))
		if err != nil {
			return nil, errors.Wrap(err, "signin.SuperAdministratorCheck()")
		}
		if super {
			return RequireMFA(sessioninfo.MethodEmailCode), nil
		}

		return decision, nil
	}
}
