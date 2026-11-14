package identity

import (
	"context"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/tracer"
)

// Invitation is an invitation to send: an address, and the roles whoever accepts it is
// given.
type Invitation struct {
	// Email is the address the invitation is mailed to.
	Email string
	// Domain is the tenant the roles are held in, nil for an invitation into the
	// application: a role of the global scope is then held there, and a role of the
	// domain kind in every tenant.
	Domain *string
	// Roles are the roles the acceptance gives: at least one.
	Roles []accesstypes.Role
}

// Invite buffers an invitation in the caller's transaction and has its link mailed once
// the transaction has committed. It answers the invitation's id. The session library
// prepares the invitation (the address check, the token, the time the link stands for)
// and keeps the token, the message and the sender; the row is written here, beside
// whatever else the transaction writes, so an invitation and the application's own
// rows are committed together or not at all.
//
// A message cannot be taken back, so it is not sent until the rows are committed: the
// send is a step after the commit (resource.AfterCommit). sent is told how it went,
// after the commit and before the transaction's ExecuteFunc returns: true once the
// message is with the sender, false when it could not be sent, in which case the
// session library has withdrawn the invitation, so no link stands that nobody holds,
// and the caller invites again. A transaction that does not commit sends nothing and
// never calls sent. sent may be nil.
//
// An invitation gives a role, at least one: a person an invitation admits holding no
// role is deleted by the rule on an account nothing admits. Every role must exist
// where the acceptance seats it: in the tenant for an invitation into one, and in the
// global scope or in every tenant for an invitation into the application. The
// request's caller is recorded as its sender where they are a user of the auth.
//
// Shared rule: the session library's Invite, which stores the row and mails the link in
// its own storage and answers a failed send as an error.
func (m *Manager) Invite(ctx context.Context, txn resource.ReadWriteTransaction, inv *Invitation, sent func(sent bool)) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	email := strings.TrimSpace(inv.Email)
	if email == "" {
		return impulse.NilUUID, httpio.NewBadRequestMessage("an address is required: the invitation is mailed to it")
	}
	roles := distinctRoles(inv.Roles)
	if len(roles) == 0 {
		return impulse.NilUUID, httpio.NewBadRequestMessage("at least one role is required: the roles are what the invitation gives")
	}
	req := &session.InvitationRequest{Email: email, Tenant: "", RoleNames: make([]string, 0, len(roles)), InvitedBy: impulse.NullUUID{UUID: impulse.NilUUID, Valid: false}}
	scopes := []accesstypes.PolicyScope{accesstypes.GlobalPolicyScope(), accesstypes.EveryDomainPolicyScope()}
	if inv.Domain != nil {
		scope, err := Scope(inv.Domain, false)
		if err != nil {
			return impulse.NilUUID, err
		}
		req.Tenant, scopes = *inv.Domain, []accesstypes.PolicyScope{scope}
	}
	for _, role := range roles {
		exists, err := m.roleExistsIn(ctx, txn, role, scopes)
		if err != nil {
			return impulse.NilUUID, err
		}
		if !exists {
			return impulse.NilUUID, httpio.NewBadRequestMessagef("role %q does not exist where the invitation gives it", role)
		}
		req.RoleNames = append(req.RoleNames, string(role))
	}
	if caller := resource.CallerFrom(ctx); !caller.IsZero() {
		id, found, err := m.store.UserID(ctx, txn, string(caller.Permissions.User()))
		if err != nil {
			return impulse.NilUUID, errors.Wrap(err, "identity.Store.UserID()")
		}
		if found {
			req.InvitedBy = impulse.NullUUIDFromUUID(id)
		}
	}

	prepared, err := m.sessions.PrepareInvitation(ctx, req)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "identity.SessionAPI.PrepareInvitation()")
	}
	row := prepared.Row()
	if err := m.store.InsertInvitation(ctx, txn, row); err != nil {
		return impulse.NilUUID, errors.Wrap(err, "identity.Store.InsertInvitation()")
	}
	if err := resource.AfterCommit(txn, func(ctx context.Context) {
		err := prepared.Send(ctx)
		if err != nil {
			logger.FromCtx(ctx).Errorf("identity: the invitation %s to %s is committed and its message could not be sent, so it is withdrawn: %v", row.ID, row.Email, err)
		}
		if sent != nil {
			sent(err == nil)
		}
	}); err != nil {
		return impulse.NilUUID, errors.Wrap(err, "resource.AfterCommit()")
	}

	return row.ID, nil
}

// roleExistsIn reports whether the role exists in one of the scopes, by the rule a
// membership's role is held to (access.UserManager.Membership): a role of the release's
// role file, or a custom role read from the roles table in the transaction.
func (m *Manager) roleExistsIn(ctx context.Context, txn resource.ReadWriteTransaction, role accesstypes.Role, scopes []accesstypes.PolicyScope) (bool, error) {
	for _, scope := range scopes {
		// The rule answers a membership's key and wants a holder for it; nothing is
		// written, so the holder is only a name that is not empty.
		_, err := m.access.UserManager().Membership(ctx, scope, "the invited person", role, func(ctx context.Context, key access.RoleKey) (bool, error) {
			held, err := m.store.HasRole(ctx, txn, &key)
			if err != nil {
				return false, errors.Wrap(err, "identity.Store.HasRole()")
			}

			return held, nil
		})
		if err == nil {
			return true, nil
		}
		if !httpio.HasNotFound(err) {
			return false, errors.Wrap(err, "access.UserManager.Membership()")
		}
	}

	return false, nil
}

// WithdrawInvitation buffers the withdrawal of an invitation that stands, in the
// caller's transaction: its link is then dead. It holds to invitations alone: the mail
// links table also keeps the links that verify an address and that start a sign-up,
// and one of those under the id is not found, as an invitation that does not exist is.
// An invitation that was accepted, withdrawn or has expired no longer stands and is
// not found either.
//
// Shared rule: the session library's RevokeInvitation, which consumes the live link of
// the id whatever its kind.
func (m *Manager) WithdrawInvitation(ctx context.Context, txn resource.ReadWriteTransaction, invitationID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	link, found, err := m.store.MailLink(ctx, txn, invitationID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.MailLink()")
	}
	if !found || !link.Invitation {
		return httpio.NewNotFoundMessagef("invitation %q does not exist", invitationID)
	}
	if link.Consumed || !m.now().Before(link.ExpiresAt) {
		return httpio.NewNotFoundMessagef("invitation %q no longer stands: it was accepted, withdrawn or has expired", invitationID)
	}
	if err := m.store.ConsumeMailLink(ctx, txn, invitationID); err != nil {
		return errors.Wrap(err, "identity.Store.ConsumeMailLink()")
	}

	return nil
}
