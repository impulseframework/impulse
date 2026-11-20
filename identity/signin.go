package identity

import (
	"context"
	"strings"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/tracer"
)

// The roles a sign-in gives.
//
// A person comes to hold a role at a sign-in, with no administrator signed in, in two
// ways: they accept an invitation, which names roles, and they sign in for the first
// time through the directory of a tenant that names a default role. Each role is
// written in the transaction of the step that causes it: the invitation is spent and
// its roles given together, or neither; the person's identity is linked to their
// account and the account given its role together, or neither.
// The session library runs a hook inside that transaction and hands it the transaction
// (session.InvitationAcceptedHook, session.DirectorySeatedHook); the two functions here
// answer those hooks over the manager's own membership write (AssignRole), so a role a
// sign-in gives is held to the rule a role given by hand is.
//
// The session library's transaction is the Spanner client's, and the manager takes the
// resource package's: resource.NewSpannerReadWriteTransaction wraps the one as the
// other. A transaction wrapped this way has no executor, so what the membership write
// registered for after the commit, the word to the permission engine that its
// memberships changed, runs when the wrapper's Committed is called: each hook answers
// that as its committed function, which the session library calls once the transaction
// has committed and before the sign-in policy decides, so the policy finds the
// membership. Spanner runs a transaction's closure again when it aborts, and each run
// wraps its own transaction, so only the run that committed tells the engine.
//
// Inside a hook everything is read and written through the transaction it was handed:
// the Spanner emulator allows one read-write transaction at a time, and a second one,
// or a read through another client, would wait on this one for good.
//
// Where a person belongs to one tenant (WithOneTenantPerPerson), the two hooks of one
// sign-in share what they have given under the transaction they were both handed, so
// the roles of one sign-in are held to the rule together (signInTenants).

// InvitationAccepted answers the session library's hook for an accepted invitation
// (session.Auth.SetInvitationAccepted): the roles the invitation carries are buffered
// as memberships of the person in the transaction that consumes the invitation. An
// invitation into a tenant gives its roles in that tenant; one that stores no tenant
// gives each role where that role is held, the global scope for a role of the global
// kind and every tenant for one of the domain kind. Invite held its roles to one of
// the two when it wrote the invitation: the whole application's where the sender named
// neither a tenant nor every tenant, the tenants' where the sender said every tenant.
// A role that can no longer be given (the role file changed since) fails
// the transaction: the invitation stands, and a person the sign-in would have created
// is not created. An invitation that makes a tenant carries no role: its acceptance
// makes the tenant and seats the person as its first administrator
// (acceptTenantInvitation).
//
// auth names the Auth in the log line a given role leaves.
func (m *Manager) InvitationAccepted(auth string) session.InvitationAcceptedHook[*spanner.ReadWriteTransaction] {
	return func(ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, username string, inv *sessioninfo.Invitation) (func(ctx context.Context), error) {
		if inv.NewTenant != nil {
			return m.tenantInvitationAccepted(ctx, auth, tx, userID, username, inv)
		}
		txn := resource.NewSpannerReadWriteTransaction(tx)
		if err := m.giveInvitedRoles(m.oneTenant.within(ctx, tx), txn, userID, username, inv); err != nil {
			return nil, err
		}

		return func(ctx context.Context) {
			m.oneTenant.forget(tx)
			txn.Committed(ctx)
			logger.FromCtx(ctx).Infof("%s: %q accepted the invitation to %s and holds %s", auth, username, inv.Email, strings.Join(inv.RoleNames, ", "))
		}, nil
	}
}

// DirectorySeated answers the session library's hook for a directory's default role
// (session.Auth.SetDirectoryAdmission): the default role of the tenant whose directory
// the person signed in through is buffered as a membership of the person in the tenant,
// in the transaction that links the person's identity to their account, which is their
// first sign-in through the directory whether the account is made in it or stood
// before. The role is given to a person who holds no role in the tenant: one who holds
// a role there, by an invitation or by an administrator's hand, keeps what they hold
// and is given nothing, so a link that is deleted and made again does not add the
// default role beside the role the person was given since. A role that cannot be given
// fails the transaction, so no account is left holding nothing and no link is left
// that gave nothing.
//
// auth names the Auth in the log line a given role leaves.
//
// The hook's other half answers whether a tenant is the only place a person holds
// anything, read in the same transaction: the session library asks it before it links
// an identity, on its sign-in name alone, to an account that has no password and has
// never signed in, which a tenant's directory vouches for only when the account is the
// tenant's alone. It reads what places a person anywhere (holds): the roles of their
// own and their places in groups, one whose period has not begun counted where it will
// count and one whose period has ended left out. The account is the tenant's alone when
// all of it is held in the tenant itself and some of it is: a role in another tenant, in
// every tenant or outside the tenants, or a place in a group kept anywhere else, makes
// it more than one tenant's, and the link would hand the tenant's directory all of it.
// It is the fact a tenant's own methods are held to before they change a person's name,
// address or password (the list's heldElsewhere and heldGlobally).
func (m *Manager) DirectorySeated(auth string) session.DirectorySeatedHook[*spanner.ReadWriteTransaction] {
	seat := func(ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, username string, seats []signin.DirectorySeat) (func(ctx context.Context), error) {
		txn := resource.NewSpannerReadWriteTransaction(tx)
		given, err := m.giveDirectoryRoles(m.oneTenant.within(ctx, tx), txn, userID, username, seats)
		if err != nil {
			return nil, err
		}

		return func(ctx context.Context) {
			m.oneTenant.forget(tx)
			txn.Committed(ctx)
			for _, seat := range given {
				logger.FromCtx(ctx).Infof("%s: %q entered %s through its directory and holds %s", auth, username, seat.Tenant, seat.Role)
			}
		}, nil
	}
	onlyIn := func(ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, tenant accesstypes.Domain) (bool, error) {
		held, err := m.holds(ctx, resource.NewSpannerReadWriteTransaction(tx), userID)
		if err != nil {
			return false, err
		}

		return onlyInTenant(held, accesstypes.DomainPolicyScope(tenant)), nil
	}

	return session.DirectorySeatedHook[*spanner.ReadWriteTransaction]{Seat: seat, OnlyIn: onlyIn}
}

// giveInvitedRoles buffers the memberships an accepted invitation gives the person:
// userID is who holds them, and username what an error reads.
func (m *Manager) giveInvitedRoles(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username string, inv *sessioninfo.Invitation) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	for _, name := range inv.RoleNames {
		if err := m.giveInvitedRole(ctx, txn, userID, username, inv, accesstypes.Role(name)); err != nil {
			return err
		}
	}

	return nil
}

// giveInvitedRole buffers one role of an invitation: in the invitation's tenant, or,
// for an invitation that stores no tenant, in the global scope for a role that exists
// there and in every tenant otherwise. The stored invitation does not tell one into
// the whole application from one into every tenant, so the role's kind does: Invite
// held each role to the place the sender named, a role of the whole application for
// the one and a role of the tenants with no global namesake for the other. The
// membership write answers NotFound for a role that does not exist where it is asked
// for, with nothing written, which is how the kind is told. A role the person holds
// there already is left as it stands (giveUnlessHeld).
func (m *Manager) giveInvitedRole(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username string, inv *sessioninfo.Invitation, role accesstypes.Role) error {
	if inv.TenantID != "" {
		if err := m.giveUnlessHeld(ctx, txn, accesstypes.DomainPolicyScope(accesstypes.Domain(inv.TenantID)), userID, role); err != nil {
			return errors.Wrapf(err, "the invitation to %s gives %q the role %s in %s", inv.Email, username, role, inv.TenantID)
		}

		return nil
	}
	err := m.giveUnlessHeld(ctx, txn, accesstypes.GlobalPolicyScope(), userID, role)
	if err == nil {
		return nil
	}
	if !httpio.HasNotFound(err) {
		return errors.Wrapf(err, "the invitation to %s gives %q the role %s", inv.Email, username, role)
	}
	if err := m.giveUnlessHeld(ctx, txn, accesstypes.EveryDomainPolicyScope(), userID, role); err != nil {
		return errors.Wrapf(err, "the invitation to %s gives %q the role %s, which exists neither globally nor in every tenant", inv.Email, username, role)
	}

	return nil
}

// giveUnlessHeld buffers the membership an accepted invitation gives, with no period,
// unless the person holds the role in the scope already: a membership whose period has
// not ended is left exactly as it is, its period included. An invitation gives a person
// what they do not hold; it does not turn a role an administrator gave until Friday
// into one that stands for good, nor one given from next month into one held today. A
// role whose period has ended is nothing the person holds, whether or not its row still
// stands, and is given afresh. The membership is read as committed.
func (m *Manager) giveUnlessHeld(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role) error {
	key := access.NewMembershipKey(scope, User(userID), role)
	held, err := m.store.HasMembership(ctx, txn, &key)
	if err != nil {
		return errors.Wrap(err, "identity.Store.HasMembership()")
	}
	if held {
		periods, err := m.store.UserPeriods(ctx, txn, userID)
		if err != nil {
			return errors.Wrap(err, "identity.Store.UserPeriods()")
		}
		if !periods.Memberships[key].Ended(time.Now()) {
			return nil
		}
	}

	return m.AssignRole(ctx, txn, scope, userID, role)
}

// giveDirectoryRoles buffers the memberships a tenant's directory row gives a person at
// their first sign-in through the directory, each a role in a tenant, and answers the
// seats it gave: userID is who holds them, and username what an error reads. A seat in
// a tenant where the person holds a role already is not given: what the person holds
// is read as committed, in the caller's transaction, once for the call. A role whose
// period has ended is nothing the person holds, whether or not its row still stands: a
// person whose only role in the tenant has ended is given the seat, as one who holds
// none, and no reader waits for the row to be removed. A role that has not begun is one
// the person will hold, and is not given a seat beside it.
func (m *Manager) giveDirectoryRoles(
	ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username string, seats []signin.DirectorySeat,
) (given []signin.DirectorySeat, err error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if len(seats) == 0 {
		return nil, nil
	}
	held, _, err := m.standing(ctx, txn, userID)
	if err != nil {
		return nil, err
	}
	for _, seat := range seats {
		scope := accesstypes.DomainPolicyScope(seat.Tenant)
		if inTenant(held, scope) {
			continue
		}
		if err := m.AssignRole(ctx, txn, scope, userID, seat.Role); err != nil {
			return nil, errors.Wrapf(err, "the directory's default role gives %q the role %s in %s", username, seat.Role, seat.Tenant)
		}
		given = append(given, seat)
	}

	return given, nil
}

// onlyInTenant reports whether everything held is held in the tenant scope names, and
// something is.
func onlyInTenant(held []access.MembershipKey, scope accesstypes.PolicyScope) bool {
	want := access.NewMembershipKey(scope, "", "")
	for i := range held {
		if held[i].Kind != want.Kind || held[i].Axis != want.Axis || held[i].Domain != want.Domain {
			return false
		}
	}

	return len(held) > 0
}

// inTenant reports whether one of the memberships is held in the tenant scope names.
func inTenant(held []access.MembershipKey, scope accesstypes.PolicyScope) bool {
	want := access.NewMembershipKey(scope, "", "")
	for i := range held {
		if held[i].Kind == want.Kind && held[i].Axis == want.Axis && held[i].Domain == want.Domain {
			return true
		}
	}

	return false
}
