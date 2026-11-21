package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

// The invitation that makes a tenant.
//
// Staff bring an organization into a tenanted application by inviting one person. The
// invitation names no tenant: it carries the tenant's details as staff entered them, and
// the invited person's first sign-in makes the tenant from them, seats the person as its
// first administrator and, where they signed in through an organization's directory (a
// Google Workspace, a Microsoft Entra tenant), makes that the tenant's directory, all in
// the transaction that consumes the invitation. The invited person enters nothing and
// changes nothing by accepting.
//
// The tenant record is the application's, so the write is too: the generator writes a
// TenantMaker over the record's own create patch, and the application hands it to the
// auth whose people the record's outlets sign in (the generated client takes it). The
// rules here are what stands around the write: who may send one, what the role a person
// from the tenant's directory starts with may be, and what a refused acceptance says.

// NewTenant is a tenant to make from an invitation, as a TenantMaker takes it.
type NewTenant struct {
	// Fields are the tenant record's fields as staff entered them: a JSON object keyed
	// by the record's Go field names, which the generated invitation method writes and
	// the generated maker reads. A field marked secret is never among them.
	Fields json.RawMessage
	// DirectoryOnly says the tenant is entered through its directory only. It is set
	// on the tenant where the record declares the switch, and only together with a
	// directory: the session library accepts such an invitation through an
	// organization's directory alone.
	DirectoryOnly bool
	// Directory is the organization's directory the accepting person signed in
	// through, which becomes the tenant's one directory; the zero value where they
	// came by password or with a personal account, and at the check a send makes.
	Directory signin.Directory
	// DefaultRole is the role a person from the tenant's directory starts with: the
	// directory row's default seat. Empty for none.
	DefaultRole accesstypes.Role
	// Source is what the change events of the tenant's rows name as their source: the
	// person who accepted and the invitation, since the write is made at a sign-in,
	// before any session stands.
	Source string
	// Caller is set at the check a send makes, and nil at an acceptance: the request's
	// caller, whose grants the record's create is held to (the Create grant on the
	// tenant record and on each field, with every condition), so a detail the sender
	// could not have written by creating the tenant is refused when the invitation is
	// sent. At an acceptance the write is the application's own.
	Caller *resource.Caller
}

// TenantMaker makes a tenant from an invitation's details. The generator writes one over
// the tenant record (New<Record>Maker in the handler package), and an application writes
// none.
type TenantMaker interface {
	// Make buffers the tenant's row in the caller's transaction, by the rules the
	// record's create applies, and answers the tenant's id as the domain of the roles
	// held in it. With a directory it buffers the tenant's directory row too, with the
	// default role, held to the rules of a tenant's directory: a directory another
	// tenant has is refused as a sign-in is (sessioninfo.RefusedOrganizationHasTenant),
	// and nothing is written. What the instance owes after the commit (its roster
	// rereading the tenants, the word to the other instances) it registers on the
	// transaction (resource.AfterCommit).
	Make(ctx context.Context, txn resource.ReadWriteTransaction, tenant *NewTenant) (accesstypes.Domain, error)
	// SeatsDefaultRole reports whether the tenant's directory keeps a default role: the
	// directories record carries the column. An invitation names a starting role only
	// where it does.
	SeatsDefaultRole() bool
	// DeclaresDirectoryOnly reports whether the tenant record declares the
	// directory-only switch. An invitation says directory only where it does.
	DeclaresDirectoryOnly() bool
}

// WithTenantMaker hands the manager what makes a tenant from an accepted invitation
// (TenantMaker). The generated client of the auth a tenant is made through passes the
// one the application built with the generated New<Record>Maker; a manager without one
// sends no tenant invitation and accepts none.
func WithTenantMaker(maker TenantMaker) Option {
	return func(m *Manager) {
		m.tenants = maker
	}
}

// TenantInvitation is an invitation that makes a tenant, to send.
type TenantInvitation struct {
	// Email is the address the invitation is mailed to.
	Email string
	// Name is the name the sender gives the person, nil for none.
	Name *string
	// TenantName is what people will call the tenant. The invitation page shows it to
	// the invited person before they sign in; nothing else of the tenant is shown there.
	TenantName string
	// Fields are the tenant record's fields as staff entered them (NewTenant.Fields).
	Fields json.RawMessage
	// DirectoryOnly says the tenant is entered through its directory only, so the
	// invitation is accepted through an organization's directory alone.
	DirectoryOnly bool
	// DefaultRole is the role a person from the tenant's directory starts with, nil for
	// none: the tenant then admits its directory's people by invitation.
	DefaultRole *string
}

// tenantDetails is what an invitation that makes a tenant keeps beside the tenant's name
// and whether it is directory only, which the session library reads: the details the
// session library stores and hands back at the acceptance, and serves nowhere.
type tenantDetails struct {
	DefaultRole string          `json:"defaultRole,omitempty"`
	Fields      json.RawMessage `json:"fields,omitempty"`
}

// CheckInvitedTenant holds the details of a tenant invitation to the rules the tenant
// record's create applies, for the request's caller, before the invitation is sent: the
// tenant is made exactly as the acceptance will make it, armed with the caller, in a
// transaction of its own that is rolled back. A caller without the Create grant on the
// tenant record, or on a field the details name, is refused as the record's create would
// refuse them (Forbidden), and so is a detail a condition on the grant does not admit.
// Nothing is written, and nothing is owed after it.
//
// It runs outside the transaction that writes the invitation: the generated method calls
// it first. A call outside any request is the application's own act and is held to
// nothing here.
func (m *Manager) CheckInvitedTenant(ctx context.Context, client resource.Client, inv *TenantInvitation) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.requireTenantMaker(); err != nil {
		return err
	}
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() {
		return nil
	}
	err := client.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if _, err := m.tenants.Make(ctx, txn, &NewTenant{
			Fields: inv.Fields, DirectoryOnly: false, Directory: signin.Directory{Method: "", Connection: ""}, DefaultRole: "", Source: "the check of an invitation, rolled back", Caller: caller,
		}); err != nil {
			return errors.Wrap(err, "identity.TenantMaker.Make()")
		}

		return resource.ErrDryRun
	})
	if err != nil && !resource.DryRunRolledBack(err) {
		return errors.Wrap(err, "resource.Client.ExecuteFunc()")
	}

	return nil
}

// InviteTenant buffers an invitation that makes a tenant in the caller's transaction and
// has its link mailed once the transaction has committed, as Invite does for an
// invitation into a tenant that exists (sent is told how the send went). It answers the
// invitation's id. The invitation names no tenant and carries no role: whoever accepts it
// is seated with the roles that administer a tenant (WithAdministratorRoles) in the
// tenant their first sign-in makes.
//
// The role a person from the tenant's directory starts with gives a role to people
// later, so it is held to what giving it directly needs: it is a role of a tenant, never
// the Super Administrator role, and one the request's caller could give a person in a
// tenant (RoleGrants.MayGive); a role the caller may not give is Forbidden, naming it. A
// starting role, and directory only, are refused where the application's records keep
// neither.
//
// Where a person belongs to one tenant (WithOneTenantPerPerson), an address whose account
// already belongs to a tenant is refused, as an invitation into a second tenant is: a
// Conflict that says the address has an account and never where.
//
// The details are the caller's to check first (CheckInvitedTenant): this writes them as
// given.
func (m *Manager) InviteTenant(ctx context.Context, txn resource.ReadWriteTransaction, inv *TenantInvitation, sent func(sent bool)) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.requireTenantMaker(); err != nil {
		return impulse.NilUUID, err
	}
	email := strings.TrimSpace(inv.Email)
	if email == "" {
		return impulse.NilUUID, httpio.NewBadRequestMessage("an address is required: the invitation is mailed to it")
	}
	tenantName := strings.TrimSpace(inv.TenantName)
	if tenantName == "" {
		return impulse.NilUUID, httpio.NewBadRequestMessagef("the %s's name is required: the invitation page shows it to the person invited", m.tenantWord)
	}
	if inv.DirectoryOnly && !m.tenants.DeclaresDirectoryOnly() {
		return impulse.NilUUID, httpio.NewBadRequestMessagef("a %s here cannot be directory only: the application's %s record declares no such switch", m.tenantWord, m.tenantWord)
	}
	role, err := m.startingRole(ctx, txn, inv.DefaultRole)
	if err != nil {
		return impulse.NilUUID, err
	}
	details, err := json.Marshal(tenantDetails{DefaultRole: string(role), Fields: inv.Fields})
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "json.Marshal()")
	}
	req := &session.InvitationRequest{
		Email: email, Name: "", TenantID: "", EveryTenant: false, RoleNames: nil, InvitedByUserID: m.callerAccount(ctx, txn),
		NewTenant: &sessioninfo.NewTenant{Name: tenantName, DirectoryOnly: inv.DirectoryOnly, Details: details},
	}
	if inv.Name != nil {
		req.Name = *inv.Name
	}

	prepared, err := m.sessions.PrepareInvitation(ctx, req)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "identity.SessionAPI.PrepareInvitation()")
	}
	row := prepared.Row()
	if err := m.refuseImpersonatorsAddress(ctx, txn, row.Email); err != nil {
		return impulse.NilUUID, err
	}
	if err := m.refuseInvitedWithATenant(ctx, txn, row.Email); err != nil {
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

// requireTenantMaker refuses a manager that makes no tenant: it was built without a
// maker, and without the roles a tenant's first administrator is seated with.
func (m *Manager) requireTenantMaker() error {
	if m.tenants == nil {
		return errors.Newf(
			"the %s auth makes no %s from an invitation: its manager was built without identity.WithTenantMaker "+
				"(the generated client of the auth that serves the tenant record takes the generated New<Record>Maker)",
			m.auth, m.tenantWord,
		)
	}
	if m.administrators == nil || len(m.administrators.domain) == 0 {
		return errors.Newf("the %s auth names no role that administers a %s (identity.WithAdministratorRoles): the person who accepts an invitation that makes one is seated with them", m.auth, m.tenantWord)
	}

	return nil
}

// callerAccount answers the request's caller as the sender an invitation records: the
// id of their account, where they are a user of this auth, and null otherwise. One who
// came through another auth is no user of this one, and the column references this
// auth's users. A read that fails records nobody: who sent an invitation is a note.
func (m *Manager) callerAccount(ctx context.Context, txn resource.ReadWriteTransaction) impulse.NullUUID {
	nobody := impulse.NullUUID{UUID: impulse.NilUUID, Valid: false}
	caller := resource.CallerFrom(ctx)
	if caller.IsZero() {
		return nobody
	}
	id, ok := UserID(caller.Permissions.User())
	if !ok {
		return nobody
	}
	if _, found, err := m.store.Username(ctx, txn, id); err != nil || !found {
		return nobody
	}

	return impulse.NullUUIDFromUUID(id)
}

// startingRole holds the role a person from the new tenant's directory starts with to
// its rules, and answers it; "" for an invitation that names none. The role is given
// later, at the first sign-in of each of the directory's people, with nobody signed in
// to judge it, so it is judged now: the request's caller could give it to a person in a
// tenant, it exists as a role of every tenant (a custom role is made in a tenant, and
// this one has none yet), and it is not the Super Administrator role. The tenant does
// not exist yet, so the question is asked for a tenant no row names: a grant that holds
// for named tenants alone does not admit it. The caller's grants answer first, as on
// every rule that reads other rows: a caller who may give no role is told so whatever
// they named, and learns nothing of which names are roles.
func (m *Manager) startingRole(ctx context.Context, txn resource.ReadWriteTransaction, named *string) (accesstypes.Role, error) {
	if named == nil || strings.TrimSpace(*named) == "" {
		return "", nil
	}
	role := accesstypes.Role(strings.TrimSpace(*named))
	if !m.tenants.SeatsDefaultRole() {
		return "", httpio.NewBadRequestMessagef("a %s's directory keeps no starting role here, so the invitation names none: leave the role empty", m.tenantWord)
	}
	if err := m.mayStartWith(ctx, txn, role); err != nil {
		return "", err
	}
	if role == accesstypes.SuperAdministrator {
		return "", httpio.NewBadRequestMessagef("the %s role is held for the whole application alone, and is never the role a %s's people start with", string(role), m.tenantWord)
	}
	exists, err := m.roleExistsIn(ctx, txn, role, []accesstypes.PolicyScope{accesstypes.EveryDomainPolicyScope()})
	if err != nil {
		return "", err
	}
	if !exists {
		return "", httpio.NewBadRequestMessagef("role %q is no role of a %s: the people of the new %s's directory start with one of its roles", role, m.tenantWord, m.tenantWord)
	}

	return role, nil
}

// mayStartWith answers whether the request's caller could give the role to a person in
// a tenant, nil when they could. A call outside any request is the application's own
// act and is not judged; a request that reaches a manager built without the grants
// cannot be judged, which is an error and never a pass. A caller who could not is told
// so in one sentence that names no role; the role is named in the refusal's cause, kept
// for the server's log.
func (m *Manager) mayStartWith(ctx context.Context, txn resource.ReadWriteTransaction, role accesstypes.Role) error {
	if resource.CallerFrom(ctx).IsZero() {
		return nil
	}
	if m.grants == nil {
		return errors.New("a request names the role the people of a new tenant's directory start with and the manager holds no role grants to judge it by: " +
			"build it with identity.WithRoleGrants, as the generated client does")
	}
	placeholder, err := impulse.NewUUID()
	if err != nil {
		return errors.Wrap(err, "impulse.NewUUID()")
	}
	may, err := m.grants.MayGive(ctx, txn, accesstypes.DomainPolicyScope(accesstypes.Domain(placeholder.String())), impulse.NilUUID, role)
	if err != nil {
		return errors.Wrap(err, "identity.RoleGrants.MayGive()")
	}
	if !may {
		return httpio.NewForbiddenMessageWithError(
			errors.Newf("the people of the new %s's directory would start with the role %s, which you may not give: name a role you may give, or none", m.tenantWord, string(role)),
			fmt.Sprintf("The people of the new %s's directory would start with a role you may not give.", m.tenantWord),
		)
	}

	return nil
}

// refuseInvitedWithATenant is the rule of one tenant per person on an invitation that
// makes a tenant: where the application declared the rule, an address whose account
// already belongs to a tenant is refused, since the account would belong to two, unless
// it is one of the application's own people's. The account is the one that has confirmed
// the address, as refuseInvitedElsewhere reads it; an address no account has confirmed is
// invited, and whoever accepts is held to the rule then (AssignRole).
func (m *Manager) refuseInvitedWithATenant(ctx context.Context, txn resource.ReadWriteTransaction, email string) error {
	if m.oneTenant == nil {
		return nil
	}
	userID, found, err := m.store.UserByVerifiedEmail(ctx, txn, email)
	if err != nil {
		return errors.Wrap(err, "identity.Store.UserByVerifiedEmail()")
	}
	if !found {
		return nil
	}
	held, err := m.holds(ctx, txn, userID)
	if err != nil {
		return err
	}
	if !outsideTenants(held) && tenantCount(held) > 0 {
		return sessioninfo.NewLoginRefusal(sessioninfo.RefusedOtherTenant, httpio.NewConflictMessagef(
			"an account already holds the address %s and belongs to a %s, and a person belongs to one %s here: the invitation is not sent", email, m.tenantWord, m.tenantWord,
		))
	}

	return nil
}

// acceptTenantInvitation is the acceptance of an invitation that makes a tenant, inside
// the transaction that consumes it: the tenant is made from the invitation's details,
// with the directory the person signed in through where it is an organization's, and the
// person is seated with the roles that administer a tenant. Each write is in the
// session library's transaction or none is: a refusal fails it, so no tenant is made, a
// person the sign-in would have created is not, and the invitation stands.
//
// The session library has already refused a directory-only invitation that came by
// password or with a personal account (sessioninfo.RefusedOrganizationAccountRequired);
// it is refused here too, so a tenant that is directory only is never made without its
// directory whatever called the hook. A directory another tenant has is the maker's
// refusal (sessioninfo.RefusedOrganizationHasTenant), and a person who already belongs to
// a tenant, where a person belongs to one, is the membership's (AssignRole).
//
// The tenant made is noted on the invitation (sessioninfo.Invitation.MadeTenantID),
// which is how the session library learns where to open the application for the
// person: the invitation itself names no tenant. A refusal notes none.
func (m *Manager) acceptTenantInvitation(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username string, inv *sessioninfo.Invitation) (accesstypes.Domain, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.requireTenantMaker(); err != nil {
		return "", err
	}
	var details tenantDetails
	if len(inv.NewTenant.Details) > 0 {
		if err := json.Unmarshal(inv.NewTenant.Details, &details); err != nil {
			return "", errors.Wrapf(err, "the invitation %s to %s carries details that do not read", inv.ID, inv.Email)
		}
	}
	directory := signin.Directory{Method: inv.SignInMethod, Connection: inv.SignInConnection}
	if !directory.Organization() {
		directory = signin.Directory{Method: "", Connection: ""}
		if inv.NewTenant.DirectoryOnly {
			return "", sessioninfo.NewLoginRefusal(sessioninfo.RefusedOrganizationAccountRequired, httpio.NewUnauthorizedMessageWithError(signin.ErrOrganizationAccountRequired,
				"This invitation is accepted with your organization's account, not with a password or a personal account. Sign in with your organization's account (Google Workspace or Microsoft Entra)."))
		}
	}
	domain, err := m.tenants.Make(ctx, txn, &NewTenant{
		Fields: details.Fields, DirectoryOnly: inv.NewTenant.DirectoryOnly, Directory: directory, DefaultRole: accesstypes.Role(details.DefaultRole),
		Source: fmt.Sprintf("%s (%s), invitation %s", username, userID, inv.ID), Caller: nil,
	})
	if err != nil {
		return "", errors.Wrapf(err, "the invitation to %s makes a %s", inv.Email, m.tenantWord)
	}
	for _, role := range m.administrators.domain {
		if err := m.AssignRole(ctx, txn, accesstypes.DomainPolicyScope(domain), userID, role); err != nil {
			return "", errors.Wrapf(err, "the invitation to %s seats %q as the new %s's administrator with the role %s", inv.Email, username, m.tenantWord, role)
		}
	}
	// The session library opens the application for the person in the tenant their
	// invitation made: it is told which here, once everything is buffered.
	inv.MadeTenantID = string(domain)

	return domain, nil
}

// tenantInvitationAccepted answers the session library's hook for an invitation that
// makes a tenant (InvitationAccepted hands it every invitation that carries one).
func (m *Manager) tenantInvitationAccepted(
	ctx context.Context, auth string, tx *spanner.ReadWriteTransaction, userID impulse.UUID, username string, inv *sessioninfo.Invitation,
) (func(ctx context.Context), error) {
	txn := resource.NewSpannerReadWriteTransaction(tx)
	domain, err := m.acceptTenantInvitation(m.oneTenant.within(ctx, tx), txn, userID, username, inv)
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context) {
		m.oneTenant.forget(tx)
		txn.Committed(ctx)
		logger.FromCtx(ctx).Infof("%s: %q accepted the invitation to %s: the %s %s was made, and they administer it", auth, username, inv.Email, m.tenantWord, domain)
	}, nil
}

// InvitedTenant is an invitation that makes a tenant and still stands, as staff see it.
type InvitedTenant struct {
	// ID is the invitation's id, Email the address it was mailed to, and Name the name
	// its sender gave the person, "" for none.
	ID    impulse.UUID
	Email string
	Name  string
	// TenantName is what people will call the tenant.
	TenantName string
	// DirectoryOnly says the tenant will be entered through its directory only.
	DirectoryOnly bool
	// DefaultRole is the role a person from the tenant's directory starts with, "" for
	// none.
	DefaultRole string
	// Fields are the tenant record's fields as staff entered them (NewTenant.Fields).
	Fields json.RawMessage
	// InvitedByUserID is the user who sent it, null when unknown.
	InvitedByUserID impulse.NullUUID
	// CreatedAt is when it was sent, ExpiresAt when it stops standing.
	CreatedAt time.Time
	ExpiresAt time.Time
}

// InvitedTenantRow is an invitation that makes a tenant as the store reads it off the
// mail links table: the row's own columns, and the document its NewTenant column holds
// (session.InvitationRow.NewTenantColumn), which the manager reads.
type InvitedTenantRow struct {
	ID              impulse.UUID
	Email           string
	Name            string
	NewTenant       string
	InvitedByUserID impulse.NullUUID
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

// InvitedTenants lists the invitations that make a tenant and still stand: sent, not
// accepted, not withdrawn and not expired, oldest first. reader is the caller's
// transaction or the application's client.
func (m *Manager) InvitedTenants(ctx context.Context, reader resource.ReadOnlyTransaction) ([]*InvitedTenant, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	rows, err := m.store.InvitedTenants(ctx, reader)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.InvitedTenants()")
	}
	now := m.now()
	invited := make([]*InvitedTenant, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if !now.Before(row.ExpiresAt) {
			continue
		}
		var tenant sessioninfo.NewTenant
		if err := json.Unmarshal([]byte(row.NewTenant), &tenant); err != nil {
			return nil, errors.Wrapf(err, "the invitation %s names a tenant that does not read", row.ID)
		}
		var details tenantDetails
		if len(tenant.Details) > 0 {
			if err := json.Unmarshal(tenant.Details, &details); err != nil {
				return nil, errors.Wrapf(err, "the invitation %s carries details that do not read", row.ID)
			}
		}
		invited = append(invited, &InvitedTenant{
			ID: row.ID, Email: row.Email, Name: row.Name, TenantName: tenant.Name, DirectoryOnly: tenant.DirectoryOnly,
			DefaultRole: details.DefaultRole, Fields: details.Fields, InvitedByUserID: row.InvitedByUserID, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt,
		})
	}

	return invited, nil
}

// WithdrawTenantInvitation buffers the withdrawal of an invitation that makes a tenant
// and still stands, in the caller's transaction: its link is then dead, and staff who
// want other details send another. It holds to these invitations alone: an invitation
// into a tenant that exists, a link of another kind, and one that no longer stands are
// not found.
func (m *Manager) WithdrawTenantInvitation(ctx context.Context, txn resource.ReadWriteTransaction, invitationID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	standing, err := m.InvitedTenants(ctx, txn)
	if err != nil {
		return err
	}
	stands := false
	for _, inv := range standing {
		stands = stands || inv.ID == invitationID
	}
	if !stands {
		return httpio.NewNotFoundMessagef("no invitation %q that makes a %s stands: it was accepted, withdrawn or has expired, or it never was one", invitationID, m.tenantWord)
	}
	if err := m.store.ConsumeMailLink(ctx, txn, invitationID); err != nil {
		return errors.Wrap(err, "identity.Store.ConsumeMailLink()")
	}

	return nil
}
