package identity

import (
	"context"
	"strings"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
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
// ways: they accept an invitation, which names roles, and they are a stranger from a
// directory a tenant declares with a default role. Each role is written in the
// transaction of the step that causes it: the invitation is spent and its roles given
// together, or neither; the account is made and given its role together, or neither.
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
// invitation into a tenant gives its roles in that tenant; one into the application
// gives each role where that role is held, the global scope for a role of the global
// kind and every tenant for one of the domain kind, as Invite checked when it wrote the
// invitation. A role that can no longer be given (the role file changed since) fails
// the transaction: the invitation stands, and a person the sign-in would have created
// is not created.
//
// auth names the Auth in the log line a given role leaves.
func (m *Manager) InvitationAccepted(auth string) session.InvitationAcceptedHook[*spanner.ReadWriteTransaction] {
	return func(ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, username string, inv *sessioninfo.Invitation) (func(ctx context.Context), error) {
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
// (session.Auth.SetDirectoryAdmission): each default role the tenants' directory rows
// name is buffered as a membership of the person in its tenant, in the transaction that
// creates the person. A role that cannot be given fails the transaction, so no account
// is left holding nothing.
//
// auth names the Auth in the log line a given role leaves.
func (m *Manager) DirectorySeated(auth string) session.DirectorySeatedHook[*spanner.ReadWriteTransaction] {
	return func(ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, username string, seats []signin.DirectorySeat) (func(ctx context.Context), error) {
		txn := resource.NewSpannerReadWriteTransaction(tx)
		if err := m.giveDirectoryRoles(m.oneTenant.within(ctx, tx), txn, userID, username, seats); err != nil {
			return nil, err
		}

		return func(ctx context.Context) {
			m.oneTenant.forget(tx)
			txn.Committed(ctx)
			for _, seat := range seats {
				logger.FromCtx(ctx).Infof("%s: %q entered %s through its directory and holds %s", auth, username, seat.Tenant, seat.Role)
			}
		}, nil
	}
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
// for an invitation into the application, in the global scope for a role that exists
// there and in every tenant otherwise. The membership write answers NotFound for a
// role that does not exist where it is asked for, with nothing written, which is how
// the kind is told.
func (m *Manager) giveInvitedRole(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username string, inv *sessioninfo.Invitation, role accesstypes.Role) error {
	if inv.TenantID != "" {
		if err := m.AssignRole(ctx, txn, accesstypes.DomainPolicyScope(accesstypes.Domain(inv.TenantID)), userID, role); err != nil {
			return errors.Wrapf(err, "the invitation to %s gives %q the role %s in %s", inv.Email, username, role, inv.TenantID)
		}

		return nil
	}
	err := m.AssignRole(ctx, txn, accesstypes.GlobalPolicyScope(), userID, role)
	if err == nil {
		return nil
	}
	if !httpio.HasNotFound(err) {
		return errors.Wrapf(err, "the invitation to %s gives %q the role %s", inv.Email, username, role)
	}
	if err := m.AssignRole(ctx, txn, accesstypes.EveryDomainPolicyScope(), userID, role); err != nil {
		return errors.Wrapf(err, "the invitation to %s gives %q the role %s, which exists neither globally nor in every tenant", inv.Email, username, role)
	}

	return nil
}

// giveDirectoryRoles buffers the memberships the tenants' directory rows give a person
// at their first sign-in, each a role in a tenant: userID is who holds them, and
// username what an error reads.
func (m *Manager) giveDirectoryRoles(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username string, seats []signin.DirectorySeat) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	for _, seat := range seats {
		if err := m.AssignRole(ctx, txn, accesstypes.DomainPolicyScope(seat.Tenant), userID, seat.Role); err != nil {
			return errors.Wrapf(err, "the directory's default role gives %q the role %s in %s", username, seat.Role, seat.Tenant)
		}
	}

	return nil
}
