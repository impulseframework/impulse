package identity

import (
	"context"
	"slices"
	"strings"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
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
// ways: they accept an invitation, which names roles, and they sign in through the
// directory of a tenant that names a default role while they hold nothing in the
// tenant. Each role is written in the transaction of the step that causes it: the
// invitation is spent and its roles given together, or neither; where the sign-in links
// the person's identity to their account, the link is made and the account given its
// role together, or neither.
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
// invitation gives its roles in the place it stores and nowhere else: its tenant, every
// tenant for one that says so, and the global scope for one that names neither, which
// is an invitation into the whole application. Invite held its roles to that place when
// it wrote the invitation. The first administrator's invitation alone gives roles in
// two places, the whole application and every tenant, each role where the
// application's first-administrator setup declares it (WithFirstAdministratorRoles).
// A role that can no longer be given there (the role file changed since) fails
// the transaction: the invitation stands, and a person the sign-in would have created
// is not created. An invitation that makes a tenant carries no role: its acceptance
// makes the tenant and seats the person as its first administrator
// (acceptTenantInvitation).
//
// auth names the Auth in the log line a given role leaves.
func (m *Manager) InvitationAccepted(auth string) session.InvitationAcceptedHook[*spanner.ReadWriteTransaction] {
	return func(ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, username string, inv *sessioninfo.Invitation) (func(ctx context.Context), error) {
		// What the acceptance writes is the invitation's act, whoever accepts it.
		ctx = actingOn(actingAs(ctx, eventsource.ProcessEvent(auditlog.ProcessInvitation), ""), userID, username)
		if inv.NewTenant != nil {
			return m.tenantInvitationAccepted(ctx, auth, tx, userID, username, inv)
		}
		txn := resource.NewSpannerReadWriteTransaction(tx)
		if err := m.giveInvitedRoles(m.oneTenant.within(ctx, tx), txn, userID, username, inv); err != nil {
			return nil, err
		}
		if err := m.recordAccepted(ctx, txn, userID, username, inv.TenantID); err != nil {
			return nil, err
		}

		return func(ctx context.Context) {
			m.oneTenant.forget(tx)
			ctx, announcement := m.announcing(ctx)
			txn.Committed(ctx)
			logger.FromCtx(ctx).Infof("%s: %q accepted the invitation to %s and holds %s", auth, username, inv.Email, strings.Join(inv.RoleNames, ", "))
			announcement.row(m.tables.Users, userID).row(m.tables.MailLinks, inv.ID).whole(m.tables.Identities).announce(ctx)
		}, nil
	}
}

// DirectorySeated answers the session library's hook for a directory's default role
// (session.Auth.SetDirectoryAdmission): the default role of the tenant whose directory
// the person signed in through is buffered as a membership of the person in the tenant,
// in the sign-in's transaction, at every sign-in through the directory: whether the
// account is new, stood before, or was tied to the directory before the tenant took it,
// and whatever the person holds in other tenants. The role is given to a person who
// holds nothing in the tenant: no role of their own and no place in a group kept in the
// tenant, a row whose period has ended counting for nothing and one whose period has
// not begun counting. One who holds anything there, by an invitation or by an
// administrator's hand, keeps what they hold and is given nothing. A person an
// administrator removes from the tenant is given the default role again the next time
// they sign in through the directory: disabling their account keeps one person out
// while the tenant is open, and making the tenant invite only keeps everyone out who is
// not invited. A role that cannot be given fails the transaction, so no account is left
// holding nothing and no link is left that gave nothing.
//
// Where the application holds a person to one tenant (WithOneTenantPerPerson), a person
// who signs in through the directory of an open tenant and belongs to another tenant is
// moved: they are given the default role in the directory's tenant, and every role and
// every place in a group they held in the other tenant is taken away in the same
// transaction. Their account, password, sign-in identities and address are untouched.
// It happens also when they were the other tenant's last administrator, which a Super
// Administrator then repairs. The application's own people, who hold a role for the
// whole application or in every tenant, are seated and keep everything. An invitation
// into a second tenant is refused as before. The hook cannot tell a directory's default
// seat from a seat a registration's own account rule names (signin.Seats): it moves the
// person for either.
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
		given, taken, err := m.giveDirectoryRoles(m.oneTenant.within(actingOn(ctx, userID, username), tx), txn, userID, username, seats)
		if err != nil {
			return nil, err
		}

		return func(ctx context.Context) {
			m.oneTenant.forget(tx)
			ctx, announcement := m.announcing(ctx)
			txn.Committed(ctx)
			for _, seat := range given {
				logger.FromCtx(ctx).Infof("%s: %q entered %s through its directory and holds %s", auth, username, seat.Tenant, seat.Role)
				// A move takes a person into one tenant, the one whose seat was given.
				for _, from := range taken {
					logger.FromCtx(ctx).Infof("%s: %q signed in through the directory of %s and belongs to it now: everything they held in %s was taken away", auth, username, seat.Tenant, from)
				}
			}
			announcement.row(m.tables.Users, userID).whole(m.tables.Identities).announce(ctx)
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

// recordAccepted buffers the row of an invitation accepted: done to the account that
// accepted it, in the tenant the invitation is into ("" for none) or the one it made.
func (m *Manager) recordAccepted(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username, tenant string) error {
	accepted := accountRecord(auditlog.ActInvitationAccepted, userID, username)
	accepted.TenantID = tenant

	return m.record(ctx, txn, accepted)
}

// giveInvitedRoles buffers the memberships an accepted invitation gives the person:
// userID is who holds them, and username what an error reads. Each role is given in
// the place the invitation stores and nowhere else (invitedPlace). The place is the
// sender's choice, kept on the invitation when it was sent (Invite), and the roles that
// exist when it is accepted do not move it: a role that has gone from that place since
// is not looked for in another, and a role of the same name that another place has
// gained is not given in its stead.
//
// The first administrator's invitation is the one exception, and only where the
// application declared the roles it gives by where they are held
// (WithFirstAdministratorRoles): each role is given where the declaration says
// (firstAdministratorRoles.places).
func (m *Manager) giveInvitedRoles(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username string, inv *sessioninfo.Invitation) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	stored := invitedPlace(inv)
	declared := m.firstAdministrator != nil && firstAdministratorInvitation(inv)
	for _, name := range inv.RoleNames {
		role := accesstypes.Role(name)
		places := []rolePlace{stored}
		if declared {
			places = m.firstAdministrator.places(role, stored)
		}
		for _, place := range places {
			if err := m.giveInvitedRole(ctx, txn, userID, username, inv, role, place); err != nil {
				return err
			}
		}
	}

	return nil
}

// giveInvitedRole buffers one role of an invitation in one place. The membership write
// answers NotFound for a role that does not exist where it is asked for, with nothing
// written, which fails the acceptance. A role the person holds there already is left as
// it stands (giveUnlessHeld).
func (m *Manager) giveInvitedRole(
	ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username string, inv *sessioninfo.Invitation, role accesstypes.Role, place rolePlace,
) error {
	if err := m.giveUnlessHeld(ctx, txn, place.scope, userID, role); err != nil {
		return errors.Wrapf(err, "the invitation to %s gives %q the role %s %s, and nowhere else", inv.Email, username, role, place.where)
	}

	return nil
}

// rolePlace is a place an invitation's role is given in: the scope it is held in, and
// the place as an error says it.
type rolePlace struct {
	scope accesstypes.PolicyScope
	where string
}

// invitedPlace is the place an invitation stores for its roles: the invitation's
// tenant, every tenant for an invitation that says so (EveryTenant), and the global
// scope for one that names neither, which is an invitation into the whole application.
func invitedPlace(inv *sessioninfo.Invitation) rolePlace {
	switch {
	case inv.TenantID != "":
		return rolePlace{scope: accesstypes.DomainPolicyScope(accesstypes.Domain(inv.TenantID)), where: "in " + inv.TenantID}
	case inv.EveryTenant:
		return everyTenantPlace()
	default:
		return rolePlace{scope: accesstypes.GlobalPolicyScope(), where: "in the whole application"}
	}
}

// everyTenantPlace is every tenant as a place a role is given in.
func everyTenantPlace() rolePlace {
	return rolePlace{scope: accesstypes.EveryDomainPolicyScope(), where: "in every tenant"}
}

// firstAdministratorInvitation reports whether inv is the deployment's own invitation,
// the first administrator's, by the rule the session library tells it from every other
// by (session.AuthAPI.InviteFirstAdministrator): an invitation into the whole
// application that gives the Super Administrator role and that no user of the auth
// sent. A user who invites to that role is a super administrator of the auth and is
// named on the row (Invite), and an invitation into a tenant, into every tenant, or
// one that makes a tenant is never the deployment's.
func firstAdministratorInvitation(inv *sessioninfo.Invitation) bool {
	return !inv.InvitedByUserID.Valid && inv.TenantID == "" && !inv.EveryTenant && inv.NewTenant == nil &&
		slices.Contains(inv.RoleNames, string(accesstypes.SuperAdministrator))
}

// places are the places the first administrator's invitation gives role in, where
// stored is the place the invitation stores, the whole application: every tenant for a
// role the declaration names there, both places for one it names in both, and the
// whole application for every other, which is the Super Administrator role, a role the
// declaration names for the whole application, and a role it does not name at all.
func (f *firstAdministratorRoles) places(role accesstypes.Role, stored rolePlace) []rolePlace {
	switch {
	case !slices.Contains(f.everyDomain, role):
		return []rolePlace{stored}
	case slices.Contains(f.global, role):
		return []rolePlace{stored, everyTenantPlace()}
	default:
		return []rolePlace{everyTenantPlace()}
	}
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

// giveDirectoryRoles buffers the memberships a tenant's directory row gives a person
// who signs in through the directory and holds nothing in the tenant, each a role in a
// tenant, and answers the seats it gave and the tenants it took the person from:
// userID is who holds them, and username what an error reads. What the person holds is
// read as committed, in the caller's transaction, once for the call, as the rule of one
// tenant per person reads it (holds): a role of their own, or a place in a group kept
// in the tenant. A row whose period has ended is nothing the person holds, whether or
// not it still stands: a person whose only rows in the tenant have ended is given the
// seat, as one who holds none, and no reader waits for the row to be removed. A row
// that has not begun is one the person will hold, and is not given a seat beside it.
//
// Where a person belongs to one tenant (WithOneTenantPerPerson), a person who belongs
// to another tenant and holds nothing outside the tenants is moved to the seat's
// tenant: every membership of their own and every place in a group held in another
// tenant, whatever its period, is deleted, with no rule asked, and the seat is given
// under every rule a role given by hand is held to but the one-tenant rule's reading of
// what is committed, which still sees the rows just deleted (movingInto). The
// application's own people, who hold a role for the whole application or in every
// tenant, are given the seat and keep everything.
//
// The seat's directory acts, by the name the auth's registrations give it
// (directoryNamed): the seat's row is a role given in its form auditlog.FormSeat, in
// the seat's tenant, and each row a move deletes is recorded in the tenant it was taken
// from, in the form auditlog.FormMove, naming the seat's tenant (takeOtherTenants).
func (m *Manager) giveDirectoryRoles(
	ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, username string, seats []signin.DirectorySeat,
) (given []signin.DirectorySeat, taken []accesstypes.Domain, err error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if len(seats) == 0 {
		return nil, nil, nil
	}
	rows, err := m.readHeld(ctx, txn, userID)
	if err != nil {
		return nil, nil, err
	}
	held := rows.placingAt(time.Now())
	for _, seat := range seats {
		scope := accesstypes.DomainPolicyScope(seat.Tenant)
		if inTenant(held, scope) {
			continue
		}
		directory := m.directoryNamed(seat.Directory)
		assigning := seatingAs(ctx, directory)
		if m.oneTenant != nil && !outsideTenants(held) && inAnotherTenant(held, scope) {
			from, err := m.takeOtherTenants(actingAs(ctx, eventsource.ProcessEvent(auditlog.ProcessDirectorySignIn), directory), txn, rows, seat.Tenant)
			if err != nil {
				return nil, nil, err
			}
			for _, tenant := range from {
				if !slices.Contains(taken, tenant) {
					taken = append(taken, tenant)
				}
			}
			assigning = movingInto(assigning, userID, scope)
		}
		if err := m.AssignRole(assigning, txn, scope, userID, seat.Role); err != nil {
			return nil, nil, errors.Wrapf(err, "the directory's default role gives %q the role %s in %s", username, seat.Role, seat.Tenant)
		}
		given = append(given, seat)
	}
	if len(taken) > 0 {
		if err := m.policyChangesAtCommit(txn); err != nil {
			return nil, nil, err
		}
	}

	return given, taken, nil
}

// takeOtherTenants buffers the deletion of every row of rows held in one tenant other than
// tenant (tenantOf): each membership of the person's own and each place in a group,
// whatever its period, and each role on one of that tenant's records that has not ended,
// and answers the tenants it took rows from, each once. No rule is asked and nothing is
// mailed: a row held for the whole application, in every tenant or on another axis is no
// tenant's, and stays. Each deletion of a membership or a place is recorded by what acts
// in ctx (actingAs), in the tenant the row was taken from, in the form auditlog.FormMove,
// naming tenant, the one that seats the person, by the name the application gives it (its
// key where it gives none): a membership as a role taken, a place as a removal from the
// group, the group by its name.
func (m *Manager) takeOtherTenants(ctx context.Context, txn resource.ReadWriteTransaction, rows *heldRows, tenant accesstypes.Domain) ([]accesstypes.Domain, error) {
	person, err := m.accountSubject(ctx, txn, rows.userID)
	if err != nil {
		return nil, err
	}
	person.Form, person.SeatingTenantID = auditlog.FormMove, string(tenant)
	if person.SeatingTenantName = m.sessions.TenantName(tenant); person.SeatingTenantName == "" {
		person.SeatingTenantName = string(tenant)
	}
	var from []accesstypes.Domain
	takenFrom := func(held accesstypes.Domain) {
		if !slices.Contains(from, held) {
			from = append(from, held)
		}
	}
	for i := range rows.own {
		held, ok := tenantOf(rows.own[i].Kind, rows.own[i].Axis, rows.own[i].Domain)
		if !ok || held == tenant {
			continue
		}
		if err := m.store.DeleteMembership(ctx, txn, &rows.own[i]); err != nil {
			return nil, errors.Wrap(err, "identity.Store.DeleteMembership()")
		}
		taken := *person
		taken.Act, taken.RoleNames = auditlog.ActRoleTaken, []string{string(rows.own[i].Role)}
		taken.RoleScope, taken.TenantID = auditlog.RoleScopeTenant, string(held)
		if err := m.record(ctx, txn, &taken); err != nil {
			return nil, err
		}
		takenFrom(held)
	}
	for _, place := range rows.places {
		held, ok := tenantOf(place.Kind, place.Axis, place.Domain)
		if !ok || held == tenant {
			continue
		}
		key := access.GroupMemberKey{Group: GroupID(place.ID), User: User(rows.userID)}
		if err := m.store.DeleteGroupMember(ctx, txn, &key); err != nil {
			return nil, errors.Wrap(err, "identity.Store.DeleteGroupMember()")
		}
		out := *person
		out.Act, out.GroupID, out.TenantID = auditlog.ActGroupMemberRemoved, string(key.Group), string(held)
		if out.GroupName, _, err = m.groupWhere(ctx, txn, key.Group); err != nil {
			return nil, err
		}
		if err := m.record(ctx, txn, &out); err != nil {
			return nil, err
		}
		takenFrom(held)
	}
	for i := range rows.records {
		key := &rows.records[i]
		if key.Axis != "" || key.Domain == "" || key.Domain == string(tenant) {
			continue
		}
		if err := m.store.DeleteRecordMembership(ctx, txn, key); err != nil {
			return nil, errors.Wrap(err, "identity.Store.DeleteRecordMembership()")
		}
		takenFrom(accesstypes.Domain(key.Domain))
	}

	return from, nil
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
