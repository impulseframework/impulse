package identity

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

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

// A person's own tenants. A signed-in person reads the tenants they hold something in,
// with the roles they hold in each, and leaves one at their own word: everything they
// hold in the tenant goes, the tenant's last administrator stays, and an account left
// holding nothing is deleted with its sign-in identities and sessions.

// The sentences a person is told about leaving a tenant themselves. Each %s is the
// application's word for a tenant (WithTenantWord); none names the tenant's key or who
// administers it.
const (
	// lastAdministratorLeavingMessage is said to a person who administers the tenant
	// when nobody else who can sign in does.
	lastAdministratorLeavingMessage = "You are the last administrator of this %s. Give another person the role before you leave."

	// leavingUnnamedMessage is said where the application named no administrator roles
	// (WithAdministratorRoles): nobody gives up their own roles in a tenant then.
	leavingUnnamedMessage = "You cannot leave this %s yourself. Ask one of its administrators to remove you."

	// notInTenantMessage is said of a tenant the person holds nothing in now, and of one
	// that does not exist, the two alike.
	notInTenantMessage = "You hold no role in that %s."

	// leavingChangedMessage is said where the leaving would no longer do what the list
	// told the person: it would delete the account they were told stays, or keep the one
	// they were told goes.
	leavingChangedMessage = "What you hold changed while this page was open. Look at the list again before you leave."

	// noTenantNamedMessage is said of a request that names no tenant.
	noTenantNamedMessage = "Name the %s to leave."
)

// PersonTenant is one tenant a person holds something in, as their own account page
// lists it: the tenant's key, the roles they hold there in words (words.RoleInWords),
// sorted and each once, why they cannot leave it where they cannot (empty where they
// can), and whether leaving it deletes their account, which it does when the person then
// holds nothing that has not ended.
type PersonTenant struct {
	Domain         accesstypes.Domain
	Roles          []string
	Refusal        string
	DeletesAccount bool
}

// leaving is the judgment of a person's leaving one tenant, which the list and the act
// both read, so the two cannot disagree: the rows that go, why the person may not leave
// (empty where they may), and whether the account goes with the rows. memberships and
// places are the rows in the tenant; endedMemberships and endedPlaces the rows outside
// it, all of them ended, that go with the account, and none where the account stays.
type leaving struct {
	memberships      []access.MembershipKey
	places           []access.GroupMemberKey
	endedMemberships []access.MembershipKey
	endedPlaces      []access.GroupMemberKey
	refusal          string
	deletesAccount   bool
}

// PersonTenants answers the tenants the person holds something in, in the tenant
// itself, sorted by their keys and never nil: a role of their own held in the tenant,
// or a place in a group kept in the tenant, that has not ended (one whose period has not
// begun counts, as standing counts it). A role held in every tenant, a role for the
// whole application and a place in a group kept for the whole application list no
// tenant: they are not the tenant's to give, and leaving it cannot take them.
//
// Each tenant carries the roles the person holds there: their own memberships in it, and
// the roles each group of the tenant they are in holds there whose period has not ended.
// It carries the judgment of leaving it too (LeaveTenant), so the page says before the
// person acts whether they may and whether their account goes. What the person holds is
// read as committed.
func (m *Manager) PersonTenants(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) ([]PersonTenant, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	held, err := m.tenantsHeld(ctx, txn, userID)
	if err != nil {
		return nil, err
	}
	tenants := make([]PersonTenant, 0, len(held))
	for _, domain := range slices.Sorted(maps.Keys(held)) {
		judged, err := m.judgeLeaving(ctx, txn, userID, domain)
		if err != nil {
			return nil, err
		}
		tenants = append(tenants, PersonTenant{Domain: domain, Roles: held[domain], Refusal: judged.refusal, DeletesAccount: judged.deletesAccount})
	}

	return tenants, nil
}

// LeaveTenant buffers the person's leaving of one of their tenants, at their own word, in
// the caller's transaction, and has the permission engine told once the transaction has
// committed: the person's next request is served without anything the tenant gave them.
// It answers whether the account went with it.
//
// Every row the person has in the tenant goes, ended or not: each membership of their
// own held in it and each place in a group kept in it. What reaches the tenant another
// way stays: a role held in every tenant, a role for the whole application and a place
// in a group kept for the whole application. The person is refused, and nothing is
// written, where:
//
//   - they act as someone else (refuseImpersonatorAsSubject);
//   - the request names no tenant (Bad Request);
//   - they hold nothing in the tenant now, which is also the answer for a tenant that
//     does not exist (Not Found);
//   - they administer the tenant and nobody else who can sign in does, judged for the
//     whole leaving, or the application named no administrator roles (Forbidden);
//   - deletesAccount, what the page told them, is no longer true either way (Conflict),
//     so nobody's account goes having agreed to less and nobody keeps one they were told
//     goes.
//
// The account goes when the person, after the leaving, holds nothing that has not
// ended, with its sign-in identities and every session, in the same transaction: a row
// held anywhere else that has not ended keeps it, one whose period has not begun among
// them, and a row that has ended elsewhere keeps nothing against the person's own leaving
// and goes with the account. No notice is mailed: the person left themselves. The rule
// that keeps a directory-only tenant's administrators to callers who enter it is not
// asked: giving up one's own roles makes nobody an administrator.
//
// What the person holds is read as committed: a second leaving in one transaction does
// not see the first. Leave one tenant per transaction.
func (m *Manager) LeaveTenant(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, domain accesstypes.Domain, deletesAccount bool) (deleted bool, err error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := m.refuseImpersonatorAsSubject(ctx, userID, "leave "+withArticle(m.tenantWord)); err != nil {
		return false, err
	}
	if domain == "" {
		return false, httpio.NewBadRequestMessagef(noTenantNamedMessage, m.tenantWord)
	}
	held, err := m.tenantsHeld(ctx, txn, userID)
	if err != nil {
		return false, err
	}
	if _, in := held[domain]; !in {
		return false, httpio.NewNotFoundMessagef(notInTenantMessage, m.tenantWord)
	}
	judged, err := m.judgeLeaving(ctx, txn, userID, domain)
	if err != nil {
		return false, err
	}
	if judged.refusal != "" {
		return false, httpio.NewForbiddenMessage(judged.refusal)
	}
	if judged.deletesAccount != deletesAccount {
		return false, httpio.NewConflictMessage(leavingChangedMessage)
	}
	if err := m.applyLeaving(ctx, txn, userID, judged); err != nil {
		return false, err
	}
	logger.FromCtx(ctx).Infof("identity: %s left the %s %s themselves: %d role(s) of their own and %d place(s) in its groups are removed",
		User(userID), m.tenantWord, domain, len(judged.memberships), len(judged.places))
	if len(judged.endedMemberships)+len(judged.endedPlaces) > 0 {
		logger.FromCtx(ctx).Infof("identity: %s holds nothing that has not ended after leaving, so %d role(s) and %d place(s) that had ended elsewhere go with the account",
			User(userID), len(judged.endedMemberships), len(judged.endedPlaces))
	}
	if err := m.policyChangesAtCommit(txn); err != nil {
		return false, err
	}

	return judged.deletesAccount, nil
}

// OwnTenants answers the session library's own-tenants hook (session.Auth.SetOwnTenants)
// over auth's tables: List runs PersonTenants and Leave runs LeaveTenant, each in a
// transaction of the hook's own on client, the application's database, and answers what
// it answered, field for field. An error is wrapped with auth's name, as SignedIn wraps
// its own; a refusal stays the refusal it is, so the person's page is answered its status
// and words.
func (m *Manager) OwnTenants(auth string, client resource.Client) session.OwnTenantsHook {
	return session.OwnTenantsHook{
		List: func(ctx context.Context, userID impulse.UUID) ([]session.OwnTenant, error) {
			var held []PersonTenant
			err := client.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				held, err = m.PersonTenants(ctx, txn, userID)

				return err
			})
			if err != nil {
				return nil, errors.Wrapf(err, "%s: reading what %s holds in each %s", auth, User(userID), m.tenantWord)
			}
			tenants := make([]session.OwnTenant, 0, len(held))
			for _, t := range held {
				tenants = append(tenants, session.OwnTenant{Domain: t.Domain, Roles: t.Roles, Refusal: t.Refusal, DeletesAccount: t.DeletesAccount})
			}

			return tenants, nil
		},
		Leave: func(ctx context.Context, userID impulse.UUID, domain accesstypes.Domain, deletesAccount bool) (bool, error) {
			var deleted bool
			err := client.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				deleted, err = m.LeaveTenant(ctx, txn, userID, domain, deletesAccount)

				return err
			})
			if err != nil {
				return false, errors.Wrapf(err, "%s: %s leaving the %s %q", auth, User(userID), m.tenantWord, domain)
			}

			return deleted, nil
		},
	}
}

// applyLeaving buffers what a judged leaving takes: the account first where it goes
// (deleteLeftWithNothing), read while it is still there, then each membership and each
// place in the tenant, then each that had ended elsewhere and goes with the account.
func (m *Manager) applyLeaving(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, judged *leaving) error {
	if judged.deletesAccount {
		if err := m.deleteLeftWithNothing(ctx, txn, userID, "they left the last "+m.tenantWord+" they were in"); err != nil {
			return err
		}
	}
	for _, memberships := range [][]access.MembershipKey{judged.memberships, judged.endedMemberships} {
		for i := range memberships {
			if err := m.store.DeleteMembership(ctx, txn, &memberships[i]); err != nil {
				return errors.Wrap(err, "identity.Store.DeleteMembership()")
			}
		}
	}
	for _, places := range [][]access.GroupMemberKey{judged.places, judged.endedPlaces} {
		for i := range places {
			if err := m.store.DeleteGroupMember(ctx, txn, &places[i]); err != nil {
				return errors.Wrap(err, "identity.Store.DeleteGroupMember()")
			}
		}
	}

	return nil
}

// tenantsHeld answers the tenants the person holds something in now, in the tenant
// itself, each with the roles held there in words, sorted and each once, never nil: the
// tenants PersonTenants lists and LeaveTenant lets a person leave. A row whose period
// has ended gives nothing; a place in a group of the tenant lists the tenant though the
// group holds no role there now.
func (m *Manager) tenantsHeld(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) (map[accesstypes.Domain][]string, error) {
	own, places, err := m.standing(ctx, txn, userID)
	if err != nil {
		return nil, err
	}
	roles := map[accesstypes.Domain]map[string]struct{}{}
	hold := func(domain accesstypes.Domain) map[string]struct{} {
		if roles[domain] == nil {
			roles[domain] = map[string]struct{}{}
		}

		return roles[domain]
	}
	for _, key := range own {
		if domain, ok := tenantOf(key.Kind, key.Axis, key.Domain); ok {
			hold(domain)[roleInWords(key.Role)] = struct{}{}
		}
	}
	now := time.Now()
	for _, place := range places {
		domain, ok := tenantOf(place.Kind, place.Axis, place.Domain)
		if !ok {
			continue
		}
		named := hold(domain)
		holding, err := m.groupHolding(ctx, txn, place.ID, now)
		if err != nil {
			return nil, err
		}
		for _, key := range holding.roles {
			if where, ok := tenantOf(key.Kind, key.Axis, key.Domain); ok && where == domain {
				named[roleInWords(key.Role)] = struct{}{}
			}
		}
	}

	held := make(map[accesstypes.Domain][]string, len(roles))
	for domain, named := range roles {
		said := make([]string, 0, len(named))
		for word := range named {
			said = append(said, word)
		}
		slices.Sort(said)
		held[domain] = said
	}

	return held, nil
}

// judgeLeaving judges the person's leaving of the tenant: the rows that go, every
// membership of their own held in the tenant and every place of theirs in a group kept
// in it, whatever their periods; why they may not leave (leavingRefusal); and whether
// the account goes, which it does when everything the person holds that has not ended
// (holds) is in the tenant. Where it goes, every row of theirs outside the tenant, all
// of them ended, goes with it.
func (m *Manager) judgeLeaving(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, domain accesstypes.Domain) (*leaving, error) {
	own, err := m.store.Memberships(ctx, txn, userID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.Memberships()")
	}
	places, err := m.store.UserGroups(ctx, txn, userID)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.UserGroups()")
	}
	var memberships []access.MembershipKey
	for _, key := range own {
		if heldIn(key.Kind, key.Axis, key.Domain, domain) {
			memberships = append(memberships, key)
		}
	}
	var gone []access.GroupMemberKey
	for _, place := range places {
		if heldIn(place.Kind, place.Axis, place.Domain, domain) {
			gone = append(gone, access.GroupMemberKey{Group: GroupID(place.ID), User: User(userID)})
		}
	}
	refusal, err := m.leavingRefusal(ctx, txn, userID, domain)
	if err != nil {
		return nil, err
	}
	held, err := m.holds(ctx, txn, userID)
	if err != nil {
		return nil, err
	}
	judged := &leaving{memberships: memberships, places: gone, refusal: refusal}
	judged.deletesAccount = !slices.ContainsFunc(held, func(key access.MembershipKey) bool {
		return !heldIn(key.Kind, key.Axis, key.Domain, domain)
	})
	if !judged.deletesAccount {
		return judged, nil
	}
	for _, key := range own {
		if !heldIn(key.Kind, key.Axis, key.Domain, domain) {
			judged.endedMemberships = append(judged.endedMemberships, key)
		}
	}
	for _, place := range places {
		if !heldIn(place.Kind, place.Axis, place.Domain, domain) {
			judged.endedPlaces = append(judged.endedPlaces, access.GroupMemberKey{Group: GroupID(place.ID), User: User(userID)})
		}
	}

	return judged, nil
}

// leavingRefusal answers why the person may not leave the tenant, empty where they may.
// The rule is the one that keeps a tenant's last administrator (refuseLastAdministrator),
// judged for the whole leaving: a person who administers the tenant now
// (administratorHoldings), by a membership of their own or through a group of the
// tenant, leaves only where somebody else who can sign in does too. Every holding of the
// person's in the tenant goes with them, so none of theirs stands in for another. A
// person who holds an administrator role in every tenant, or through a group kept for
// the whole application, is nobody the tenant counts. Without the administrator roles
// (WithAdministratorRoles) nobody leaves a tenant themselves, as nobody removes their own
// role in one.
func (m *Manager) leavingRefusal(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID, domain accesstypes.Domain) (string, error) {
	if m.administrators == nil {
		return fmt.Sprintf(leavingUnnamedMessage, m.tenantWord), nil
	}
	holdings, err := m.administratorHoldings(ctx, txn, accesstypes.DomainPolicyScope(domain))
	if err != nil {
		return "", err
	}
	person := User(userID)
	if !slices.ContainsFunc(holdings, func(h administratorHolding) bool { return h.user == person }) {
		return "", nil
	}
	if slices.ContainsFunc(holdings, func(h administratorHolding) bool { return h.user != person }) {
		return "", nil
	}

	return fmt.Sprintf(lastAdministratorLeavingMessage, m.tenantWord), nil
}

// tenantOf answers the tenant a row is held in, from the row's kind, axis and domain: a
// domain on the default axis, as a tenant is read where its administrators are
// (access.AdministersTenant). ok is false for a row held anywhere else: for the whole
// application, in every tenant, or on another axis.
func tenantOf(kind, axis, domain string) (tenant accesstypes.Domain, ok bool) {
	if axis != "" || domain == "" {
		return "", false
	}
	tenant = accesstypes.Domain(domain)
	if kind != access.NewMembershipKey(accesstypes.DomainPolicyScope(tenant), "", "").Kind {
		return "", false
	}

	return tenant, true
}

// heldIn reports whether a row's kind, axis and domain say it is held in the tenant
// (tenantOf).
func heldIn(kind, axis, domain string, tenant accesstypes.Domain) bool {
	held, ok := tenantOf(kind, axis, domain)

	return ok && held == tenant
}
