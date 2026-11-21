package identity

import (
	"context"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// The reason of a person's role, and the taking of a role for a set time.
//
// A person's role carries the reason it was given for: one line of plain text
// (access.ValidateReason), kept with the membership's row beside its period and shown
// wherever the roles a person holds are listed, beside the role and when it ends. So a
// tenant's administrator reads why someone from outside holds a role with them. The
// rules of it are few:
//
//   - the reason is optional where a role is given the ordinary way (AssignRoleFor),
//     and required where a person takes a role themselves for a set time (TakeRole);
//   - it is never edited afterwards. Giving the role again gives it the reason of the
//     new request in place of the one it had, with the period, and a request with no
//     reason leaves it with none;
//   - it goes with the row: once a role is taken away, or has ended and been cleared,
//     nothing here holds the reason any more.
//
// Taking a role (TakeRole) is the act of the Break glass page: a person who may give
// themselves a role in a place gives it to themselves for one of the lengths offered
// and says why. It writes an ordinary membership, held to every rule of one, and adds
// no power: it is judged by the grant of the method that gives a role, asked for the
// caller themselves (RoleGrants.MayGive), which a super administrator always holds. It
// ends by itself, as any role with an end does, and ending it early is taking the role
// away, by whoever may.

// RequestReason answers the reason a request's optional field names, nothing for a field
// left out. The generated methods that give a person a role hand it to the rules, which
// hold it to its shape (access.ValidateReason).
func RequestReason(reason *string) string {
	if reason == nil {
		return ""
	}

	return *reason
}

// TakeLength is one length of time a role is taken for, as the request names it.
type TakeLength string

// The lengths a role is taken for. A longer need is an ordinary role with an end, given
// from the people pages.
const (
	TakeOneHour    TakeLength = "1 hour"
	TakeFourHours  TakeLength = "4 hours"
	TakeEightHours TakeLength = "8 hours"
	TakeOneDay     TakeLength = "1 day"
)

// TakeLengths lists the lengths a role is taken for, shortest first: what the list the
// length is picked from holds.
func TakeLengths() []TakeLength {
	return []TakeLength{TakeOneHour, TakeFourHours, TakeEightHours, TakeOneDay}
}

// Duration answers how long the length lasts; ok is false for a length that is not one
// of the ones offered.
func (l TakeLength) Duration() (lasts time.Duration, ok bool) {
	switch l {
	case TakeOneHour:
		return time.Hour, true
	case TakeFourHours:
		return 4 * time.Hour, true
	case TakeEightHours:
		return 8 * time.Hour, true
	case TakeOneDay:
		return 24 * time.Hour, true
	default:
		return 0, false
	}
}

// TakeRole gives the request's caller the role in the scope for a set time, with the
// reason they give, and has the permission engine told once the transaction has
// committed: the act of the Break glass page. The membership it writes is an ordinary
// one, held to every rule AssignRoleFor holds one to, and counts from now until the
// length is over, when it ends by itself; ending it early is taking the role away. The
// period it writes starts now and ends when the length is over, so the lists say from
// when to when the role was held.
//
// What is the taking's own:
//
//   - the role goes to the caller and nobody else. The request names no person, so no
//     request takes a role for another. The caller is the person the request was checked
//     as, and has an account in this auth, read in the caller's transaction, since a
//     role is held by an account; a session that views as someone else takes nothing,
//     for the person viewed as or for the person behind it;
//   - the length is one of the ones offered (TakeLengths). A longer need is an ordinary
//     role with an end;
//   - the reason is required: one line that says why the role is needed
//     (access.ValidateReason), kept with the membership and shown wherever the person's
//     roles are listed;
//   - it gives nobody a power that giving themselves the role directly would not. The
//     caller is asked what giving themselves the role in the scope needs, by the grant
//     of the method that gives a role (RoleGrants.MayGive): a super administrator always
//     may, and anyone else as the role file says. A manager built without the role
//     grants cannot judge it, which is an error and never a pass;
//   - taking a role the caller holds there already writes over it: the new time and the
//     new reason take the old ones' place, and nothing is refused. The rule that nobody
//     lengthens their own holding does not hold a taking, whose length is the caller's
//     own to choose and capped by the lengths offered.
//
// The Super Administrator role is not taken: it takes no period (access.ValidatePeriod).
func (m *Manager) TakeRole(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, role accesstypes.Role, length TakeLength, reason string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	userID, err := m.taker(ctx)
	if err != nil {
		return err
	}
	lasts, offered := length.Duration()
	if !offered {
		return httpio.NewBadRequestMessagef("a role is taken for %s, %s, %s or %s, and %q is none of them: a longer need is an ordinary role with an end, given from the people pages",
			TakeOneHour, TakeFourHours, TakeEightHours, TakeOneDay, string(length))
	}
	reason, err = access.ValidateReason(reason)
	if err != nil {
		return errors.Wrap(err, "access.ValidateReason()")
	}
	if reason == "" {
		return httpio.NewBadRequestMessage("say why you need the role: a role taken for a set time carries a reason, one line that the people it is taken with can read")
	}
	if m.grants == nil {
		return errors.New("a request takes a role and the manager holds no role grants to judge it by: build it with identity.WithRoleGrants, as the generated client does")
	}
	if _, found, err := m.store.Username(ctx, txn, userID); err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	} else if !found {
		return httpio.NewForbiddenMessage("A role is taken for your own account, and this session's account is no longer one of this application's. Sign in again.")
	}
	may, err := m.grants.MayGive(ctx, txn, scope, userID, role)
	if err != nil {
		return errors.Wrap(err, "identity.RoleGrants.MayGive()")
	}
	if !may {
		return httpio.NewForbiddenMessagef("you may not give yourself the role %s there, so you cannot take it: someone who may give the role gives it to you", string(role))
	}
	from := m.now().UTC().Truncate(time.Microsecond)
	period := accesstypes.Period{Start: from, End: from.Add(lasts)}

	return m.assignRole(ctx, txn, scope, userID, role, &given{period: period, reason: reason, lengthens: true})
}

// RolesToTake lists the roles a person picks from to take one for a set time, as the
// form of the Break glass page offers them. In an application with tenants (tenants)
// they are the roles that exist in every tenant, the role file's and the custom ones
// held in every tenant: the list is read before a tenant is picked, so a custom role one
// tenant alone holds is not on it and is given the ordinary way, with an end. In an
// application without tenants they are the roles held for the whole application. The
// Super Administrator role is never among them: it takes no end.
//
// A page offers what the caller may do: the list holds the roles the request's caller
// could give by hand where the role is taken (RoleGrants.CouldGive), and no other. A
// call outside any request is the application's own and lists them all; a manager a
// request reaches without the role grants cannot judge it, which is an error and never
// a list. TakeRole then judges the role for the place the request names.
func (m *Manager) RolesToTake(ctx context.Context, tenants bool) ([]access.RoleInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	scope := accesstypes.GlobalPolicyScope()
	if tenants {
		scope = accesstypes.EveryDomainPolicyScope()
	}
	roles, err := m.access.UserManager().Roles(ctx, scope)
	if err != nil {
		return nil, errors.Wrap(err, "access.UserManager.Roles()")
	}
	judged := !resource.CallerFrom(ctx).IsZero()
	if judged && m.grants == nil {
		return nil, errors.New("a request lists the roles to take and the manager holds no role grants to judge it by: build it with identity.WithRoleGrants, as the generated client does")
	}

	return rolesOffered(roles, func(role accesstypes.Role) (bool, error) {
		if role == accesstypes.SuperAdministrator {
			return false, nil
		}
		if !judged {
			return true, nil
		}
		could, err := m.grants.CouldGive(ctx, scope, role)
		if err != nil {
			return false, errors.Wrap(err, "identity.RoleGrants.CouldGive()")
		}

		return could, nil
	})
}

// taker answers the account a taken role goes to: the request's caller, a person of this
// auth, acting as themselves. A call outside any request has no person to give the role
// to; a caller who came through another auth, or who has no account here, holds no role
// of this auth; and a session that views as someone else is refused, since the role
// would go to the person viewed as while someone else asked for it.
func (m *Manager) taker(ctx context.Context) (impulse.UUID, error) {
	user, person := m.callerUser(ctx)
	if !person {
		return impulse.NilUUID, httpio.NewForbiddenMessage("A role is taken by a person signed in to this application's own accounts, and this request comes from none.")
	}
	userID, account := UserID(user)
	if !account {
		return impulse.NilUUID, httpio.NewForbiddenMessage("A role is taken for your own account, and this session acts for none.")
	}
	if sess, found := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData); found && sess.Impersonation != nil {
		return impulse.NilUUID, httpio.NewForbiddenMessage("A role is taken by the person themselves, and this session views as someone else. End it and take the role as yourself.")
	}

	return userID, nil
}
