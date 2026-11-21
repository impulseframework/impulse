package identity

import (
	"context"
	"slices"
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
	// Name is the name the sender gives the person, nil for none. Whoever accepts the
	// invitation takes it where their account holds no name: a new account that the
	// sign-in asserts no name for, and an existing one without a name.
	Name *string
	// Domain is the tenant the roles are held in, nil for an invitation that names
	// none: with EveryDomain unset too, the invitation is into the whole application,
	// and every role is one of the global scope.
	Domain *string
	// EveryDomain says every role is one of the domain kind, held in every tenant: the
	// sender's own word for where the roles go, which the invitation is held to. It is
	// not set with Domain.
	EveryDomain bool
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
// role is deleted by the rule on an account nothing admits. An invitation says where
// its roles go, as a role given by hand does (Scope), and every role must exist there:
// in the tenant for an invitation into one, in every tenant for one that says so
// (EveryDomain), and in the global scope for one that names neither, which is an
// invitation into the whole application. A role that exists only in the tenants is
// refused there with what to choose: nobody is seated in every tenant who was not
// invited there in so many words. The row keeps the place (the tenant, the mark for
// every tenant, or neither), and the acceptance gives each role there and nowhere else
// (giveInvitedRoles), so a role of every tenant whose name a role of the global scope
// carries too is invited like any other: the person is given the tenants' one. The
// request's caller is recorded as its sender where they are a user of the auth. An
// invitation that gives the Super Administrator role is written by a caller who holds
// it (requireSuperAdministrator); the acceptance then gives what it wrote.
//
// Where a person belongs to one tenant (WithOneTenantPerPerson), an invitation into a
// tenant is refused when a user already holds the address and holds a role in another
// tenant and none outside the tenants: a Conflict that says the address has an account
// and never where. An address no user holds is invited, and whoever accepts is held to
// the rule then (AssignRole).
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
	req := &session.InvitationRequest{
		Email: email, Name: "", TenantID: "", EveryTenant: inv.EveryDomain, RoleNames: make([]string, 0, len(roles)), NewTenant: nil,
		InvitedByUserID: impulse.NullUUID{UUID: impulse.NilUUID, Valid: false},
	}
	if inv.Name != nil {
		req.Name = *inv.Name
	}
	scope, err := Scope(inv.Domain, inv.EveryDomain)
	if err != nil {
		return impulse.NilUUID, err
	}
	if inv.Domain != nil {
		req.TenantID = *inv.Domain
	}
	for _, role := range roles {
		if err := m.refuseUninvitable(ctx, txn, inv, role, scope); err != nil {
			return impulse.NilUUID, err
		}
		req.RoleNames = append(req.RoleNames, string(role))
	}
	// The caller is named by the id of their account. One who came through another
	// auth is no user of this one, and the row names nobody: the column references this
	// auth's users.
	if caller := resource.CallerFrom(ctx); !caller.IsZero() {
		if id, ok := UserID(caller.Permissions.User()); ok {
			_, found, err := m.store.Username(ctx, txn, id)
			if err != nil {
				return impulse.NilUUID, errors.Wrap(err, "identity.Store.Username()")
			}
			if found {
				req.InvitedByUserID = impulse.NullUUIDFromUUID(id)
			}
		}
	}

	prepared, err := m.sessions.PrepareInvitation(ctx, req)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "identity.SessionAPI.PrepareInvitation()")
	}
	row := prepared.Row()
	if err := m.refuseImpersonatorsAddress(ctx, txn, row.Email); err != nil {
		return impulse.NilUUID, err
	}
	if err := m.refuseInvitedElsewhere(ctx, txn, inv.Domain, row.Email); err != nil {
		return impulse.NilUUID, err
	}
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

// distinctRoles returns the roles a request names, each once, in the order given: a
// role named twice is one membership, and two inserts of it would fail the commit.
func distinctRoles(roles []accesstypes.Role) []accesstypes.Role {
	distinct := make([]accesstypes.Role, 0, len(roles))
	for _, role := range roles {
		if !slices.Contains(distinct, role) {
			distinct = append(distinct, role)
		}
	}

	return distinct
}

// refuseUninvitable refuses a role the invitation cannot give in the scope it says: one
// that does not exist there, with what to choose where it is a role of the tenants and
// the invitation names neither a tenant nor every tenant (refuseAbsentRole), the Super
// Administrator role from a caller who does not hold it, and a tenant's role from a
// caller who may not change who holds it there.
func (m *Manager) refuseUninvitable(ctx context.Context, txn resource.ReadWriteTransaction, inv *Invitation, role accesstypes.Role, scope accesstypes.PolicyScope) error {
	exists, err := m.roleExistsIn(ctx, txn, role, []accesstypes.PolicyScope{scope})
	if err != nil {
		return err
	}
	if !exists {
		return m.refuseAbsentRole(ctx, txn, role, scope)
	}
	if role == accesstypes.SuperAdministrator {
		if err := m.requireSuperAdministrator(ctx, txn, "invites a person to the Super Administrator role"); err != nil {
			return err
		}
	}
	if inv.Domain != nil {
		if err := m.requireEnteringToChange(ctx, scope, role, nil); err != nil {
			return err
		}
	}

	return nil
}

// refuseAbsentRole answers the refusal of a role that does not exist in the scope an
// invitation says. An invitation that names neither a tenant nor every tenant is into
// the whole application; for a role that exists in the tenants the refusal says so and
// what to choose, since the sender most likely left the place out.
func (m *Manager) refuseAbsentRole(ctx context.Context, txn resource.ReadWriteTransaction, role accesstypes.Role, scope accesstypes.PolicyScope) error {
	if scope.IsGlobal() {
		inTenants, err := m.roleExistsIn(ctx, txn, role, []accesstypes.PolicyScope{accesstypes.EveryDomainPolicyScope()})
		if err != nil {
			return err
		}
		if inTenants {
			return httpio.NewBadRequestMessagef(
				"%q is a role held in a %s, and this invitation names none. Choose the %s it is for, or every %s",
				role, m.tenantWord, m.tenantWord, m.tenantWord,
			)
		}
	}

	return httpio.NewBadRequestMessagef("role %q does not exist where the invitation gives it", role)
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
// not found either. Nor is an invitation that makes a tenant: it is withdrawn by its own
// method, under its own grant (WithdrawTenantInvitation), so the grant that withdraws an
// invitation into the application does not reach it.
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
	makesTenant, err := m.makesTenant(ctx, txn, invitationID)
	if err != nil {
		return err
	}
	if makesTenant {
		return httpio.NewNotFoundMessagef("invitation %q does not exist", invitationID)
	}
	if err := m.store.ConsumeMailLink(ctx, txn, invitationID); err != nil {
		return errors.Wrap(err, "identity.Store.ConsumeMailLink()")
	}

	return nil
}

// makesTenant reports whether the standing invitation of the id is one that makes a
// tenant. Only an auth that is handed a tenant maker sends such invitations, so no
// other auth reads for them.
func (m *Manager) makesTenant(ctx context.Context, txn resource.ReadOnlyTransaction, invitationID impulse.UUID) (bool, error) {
	if m.tenants == nil {
		return false, nil
	}
	standing, err := m.store.InvitedTenants(ctx, txn)
	if err != nil {
		return false, errors.Wrap(err, "identity.Store.InvitedTenants()")
	}
	for i := range standing {
		if standing[i].ID == invitationID {
			return true, nil
		}
	}

	return false, nil
}
