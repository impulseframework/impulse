package identity

import (
	"context"
	"sync"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// One tenant per person.
//
// In an application whose tenants own their people, a person belongs to one tenant: the
// tenant that created or invited them manages the account, which it could not do for a
// person another tenant holds too. The application says so with WithOneTenantPerPerson,
// and every way a person comes to hold a role in a tenant is then held to it. All of
// them write their membership through AssignRole: a role given by hand, an invitation
// accepted, and a directory's default role. A role is held by an account, never by a
// name, so an account that is created holds nothing, and a rename moves nothing. An
// invitation is refused when it is sent too (Invite), where an account another tenant
// holds has confirmed the address, so the sender is told and the invited person is not
// left with a link they cannot accept.
//
// The application's own people are outside the rule: a person who holds a role for the
// whole application or in every tenant is no tenant's to manage, whatever else they
// hold (a role file grants a tenant its account methods on a person it holds alone),
// so they are given a role in any tenant, as an operator who creates one customer's
// tenant after another is seated as each one's first administrator. Such a role is no tenant's
// either: it never counts as one, and it is never refused. So that nobody is left in
// several tenants with nothing outside them, the last role a person holds outside the
// tenants is not taken away while they hold roles in more than one (RemoveRole).
//
// A refusal never says which tenant holds the person. The rule reads what is committed:
// one transaction of the application's own gives a person roles in one tenant, as every
// generated method does, and the two hooks of a sign-in, which share one transaction,
// are judged together (signInTenants).

// WithOneTenantPerPerson says a person belongs to one tenant: a role in a tenant is
// refused for a person who holds a role in another (AssignRole), and an invitation into
// a tenant for an address whose account another tenant holds (Invite). A refusal is a
// Conflict that carries the login refusal code sessioninfo.RefusedOtherTenant, which a
// sign-in that would seat the person in a second tenant ends on. A person who holds a
// role for the whole application or in every tenant is the application's own and is
// never refused; the last such role is not taken from a person who holds roles in more
// than one tenant (RemoveRole). Without the option a person may hold roles in any
// number of tenants.
func WithOneTenantPerPerson() Option {
	return func(m *Manager) {
		m.oneTenant = &signInTenants{mu: sync.Mutex{}, notes: map[any]*signInNote{}}
	}
}

// refuseSecondTenant is the rule of one tenant per person on a membership about to be
// written: where the application declared it and the membership is held in one tenant,
// a person who holds a role in another tenant is refused, unless they are one of the
// application's own people. What the person holds is read as committed: the roles of
// their own and the groups they are in (holds), a place in a tenant's group counting as a
// role in that tenant, and, at a sign-in, as the hooks of the sign-in's transaction have
// given so far (signInTenants). A membership held for the whole application or in every
// tenant is not a tenant's and is never refused.
func (m *Manager) refuseSecondTenant(ctx context.Context, txn resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID) error {
	domain, inTenant := scope.Domain()
	if m.oneTenant == nil || !inTenant {
		return nil
	}
	held, err := m.holds(ctx, txn, userID)
	if err != nil {
		return err
	}
	if outsideTenants(held) {
		return nil
	}
	if inAnotherTenant(held, scope) || !m.oneTenant.give(ctx, userID, string(domain)) {
		return sessioninfo.NewLoginRefusal(sessioninfo.RefusedOtherTenant, httpio.NewConflictMessage(
			"this account already belongs to another tenant, and a person belongs to one tenant here: the role is not given",
		))
	}

	return nil
}

// refuseInvitedElsewhere is the rule of one tenant per person on an invitation about to
// be written: where the application declared it and the invitation is into a tenant, an
// address whose account holds a role in another tenant is refused, since that account is
// the one that would accept it, unless it is one of the application's own people's. The
// account is the one that has confirmed the address: an address somebody typed onto an
// account and nobody confirmed says nothing of who reads its mail, as a sign-in takes
// it, so a tenant cannot keep another from inviting an address by writing it on one of
// its accounts. An address no account has confirmed is invited: whoever accepts is held
// to the rule then (AssignRole). email is the bare address, as the session library
// prepared it.
func (m *Manager) refuseInvitedElsewhere(ctx context.Context, txn resource.ReadWriteTransaction, domain *string, email string) error {
	if m.oneTenant == nil || domain == nil {
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
	if !outsideTenants(held) && inAnotherTenant(held, accesstypes.DomainPolicyScope(accesstypes.Domain(*domain))) {
		return sessioninfo.NewLoginRefusal(sessioninfo.RefusedOtherTenant, httpio.NewConflictMessagef(
			"an account already holds the address %s and belongs to another tenant, and a person belongs to one tenant here: the invitation is not sent", email,
		))
	}

	return nil
}

// refuseLeavingSeveralTenants is the rule of one tenant per person on a role being taken
// away: where the application declared it, the last role a person holds outside the
// tenants (for the whole application, or in every tenant) is not taken while they hold
// roles in more than one tenant, since they would be left in several with nothing that
// makes them the application's own. held is what the person holds, as committed, the
// membership being removed among it. A role held in one tenant is always taken.
func (m *Manager) refuseLeavingSeveralTenants(held []access.MembershipKey, removed *access.MembershipKey) error {
	left := make([]access.MembershipKey, 0, len(held))
	for i := range held {
		if held[i].Kind == removed.Kind && held[i].Axis == removed.Axis && held[i].Role == removed.Role {
			// The membership being removed.
			continue
		}
		left = append(left, held[i])
	}

	return m.refuseSeveralTenantsLeft(removed, left)
}

// refuseSeveralTenantsLeft is the rule of refuseLeavingSeveralTenants on what a person
// is left holding: removed is what goes, a membership of their own or their place in a
// group, spelled as a membership held where the group is kept, and left everything else
// they hold. A place in a group kept for the whole application is a role held outside
// the tenants, so the last one is not taken from a person who holds roles in more than
// one tenant.
func (m *Manager) refuseSeveralTenantsLeft(removed *access.MembershipKey, left []access.MembershipKey) error {
	if m.oneTenant == nil || inOneTenant(removed) {
		return nil
	}
	if !outsideTenants(left) && tenantCount(left) > 1 {
		return httpio.NewConflictMessage(
			"this account holds roles in more than one tenant, which only a person who also holds a role for the whole application or in every tenant may: take the roles in all but one tenant away first",
		)
	}

	return nil
}

// inOneTenant reports whether a membership is held in one tenant. The other two places
// are outside the tenants, for the whole application and in every tenant, and are told
// by the kind of the row, never by a domain that is empty: a row's kind is what the
// stores read it by.
func inOneTenant(key *access.MembershipKey) bool {
	return key.Kind == access.NewMembershipKey(accesstypes.DomainPolicyScope(accesstypes.Domain(key.Domain)), "", "").Kind
}

// outsideTenants reports whether one of the memberships is held outside the tenants: for
// the whole application or in every tenant. Its holder is one of the application's own
// people.
func outsideTenants(held []access.MembershipKey) bool {
	for i := range held {
		if !inOneTenant(&held[i]) {
			return true
		}
	}

	return false
}

// tenantCount is the number of tenants the memberships are held in.
func tenantCount(held []access.MembershipKey) int {
	tenants := map[[2]string]struct{}{}
	for i := range held {
		if inOneTenant(&held[i]) {
			tenants[[2]string{held[i].Axis, held[i].Domain}] = struct{}{}
		}
	}

	return len(tenants)
}

// inAnotherTenant reports whether one of the memberships is held in a tenant other than
// the one scope names: on the same axis, in one domain, and not this one.
func inAnotherTenant(held []access.MembershipKey, scope accesstypes.PolicyScope) bool {
	want := access.NewMembershipKey(scope, "", "")
	for i := range held {
		if held[i].Kind == want.Kind && held[i].Axis == want.Axis && held[i].Domain != want.Domain {
			return true
		}
	}

	return false
}

// signInTransaction is the context key a sign-in hook names its transaction under.
type signInTransaction struct{}

// signInTenants is what the hooks of a sign-in share about the tenant each person was
// given a role in. The session library runs two hooks in the sign-in's one transaction,
// the directory's default role and then the accepted invitation; each reads what is
// committed, and neither reads what the other buffered, so a stranger from one tenant's
// directory who carries another tenant's invitation would be seated in both. The hooks
// are this manager's, so they note here what they give, under the transaction they were
// both handed: the Spanner client makes a transaction of its own for every attempt, so
// a note is never read by another attempt. A note is forgotten once its transaction has
// committed, and when the sign-in's context ends for one that did not.
type signInTenants struct {
	mu sync.Mutex
	// notes is what the hooks have given, by transaction.
	notes map[any]*signInNote
}

// signInNote is what the hooks of one sign-in's transaction have given.
type signInNote struct {
	// tenant is the tenant each account was given a role in, by the account's id.
	tenant map[impulse.UUID]string
	// stop ends the watch on the sign-in's context, which forgets the note of a
	// transaction that did not commit.
	stop func() bool
}

// within answers ctx naming transaction as the sign-in's, for the memberships a hook
// writes under it. The first hook of a transaction opens its note, which is forgotten
// when ctx ends; a context that has ended opens none, and its transaction writes
// nothing more.
func (s *signInTenants) within(ctx context.Context, transaction any) context.Context {
	if s == nil {
		return ctx
	}
	s.mu.Lock()
	if _, open := s.notes[transaction]; !open && ctx.Err() == nil {
		s.notes[transaction] = &signInNote{
			tenant: map[impulse.UUID]string{},
			stop:   context.AfterFunc(ctx, func() { s.forget(transaction) }),
		}
	}
	s.mu.Unlock()

	return context.WithValue(ctx, signInTransaction{}, transaction)
}

// give notes that the account is given a role in domain by the sign-in ctx belongs to, and
// reports false where the sign-in already gave the person a role in another tenant.
// Outside a sign-in there is no note to keep: a transaction of the application's own
// gives its roles in one tenant, as every generated method does. Nothing is noted for a
// transaction whose note is forgotten already: it committed, or its sign-in's context
// ended, and it writes nothing more.
func (s *signInTenants) give(ctx context.Context, userID impulse.UUID, domain string) bool {
	transaction := ctx.Value(signInTransaction{})
	if transaction == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	note := s.notes[transaction]
	if note == nil {
		return true
	}
	if first, noted := note.tenant[userID]; noted {
		return first == domain
	}
	note.tenant[userID] = domain

	return true
}

// forget drops what a transaction's hooks noted, and the watch on its context with it.
func (s *signInTenants) forget(transaction any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	note := s.notes[transaction]
	delete(s.notes, transaction)
	s.mu.Unlock()

	if note != nil {
		note.stop()
	}
}
