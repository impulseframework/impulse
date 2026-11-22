package access

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

var _ Store = (*fakeStore)(nil)

type fakeRoleKey struct {
	scope accesstypes.PolicyScope
	role  accesstypes.Role
}

type fakeMembership struct {
	scope accesstypes.PolicyScope
	user  accesstypes.User
	role  accesstypes.Role
}

// fakeChange is the rows of one ChangeGrants call.
type fakeChange struct {
	removals  []policy.RoleGrant
	additions []policy.RoleGrant
}

type fakeGrant struct {
	scope     accesstypes.PolicyScope
	role      accesstypes.Role
	perm      accesstypes.Permission
	resource  string
	field     string
	condition string
}

// fakeStore is an in-memory Store honoring the documented contract: idempotent
// inserts, no-op deletes of absent rows, memberships written by role name
// alone, FK-style enforcement of the role parent for grants, member-blocked
// cascade-granted role deletes, and sorted listings. A grant's condition is
// part of the grant row's identity, matching the stores' contract. A role
// row holds the description it was created with. An assignment's row holds its
// period, replaced when the assignment is given again, and the listings leave
// out the ones outside their period at the store's present. A membership's row
// holds its reason too, replaced with the period (fake_store_holdings_test.go).
type fakeStore struct {
	mu          sync.Mutex
	roles       map[fakeRoleKey]string
	memberships map[fakeMembership]accesstypes.Period
	// reasons holds the reason of each membership given with one.
	reasons map[fakeMembership]string
	grants  map[fakeGrant]bool
	// clock is the store's present, the wall clock when nil: what a database's
	// own clock is to the stores.
	clock func() time.Time
	// writes counts the calls that wrote a row or removed one, so a test can
	// pin that a period began or ended with nothing written.
	writes int
	// groups are the groups of people (fake_store_groups_test.go).
	groups fakeGroups
	// batchWrites counts InsertGrants calls, so tests can pin that a bulk
	// write reached the store as one call.
	batchWrites int
	// changeWrites counts ChangeGrants calls and lastChange keeps the last
	// call's rows, so tests can pin that a role's grant set changed as one
	// call; deleteCalls counts DeleteGrant calls, which that path never makes.
	changeWrites int
	lastChange   fakeChange
	deleteCalls  int

	// failWith, when set, makes every method return this error.
	failWith error
}

// setFail makes every subsequent method call return err (nil restores
// normal operation). Safe to call concurrently with engine goroutines.
func (f *fakeStore) setFail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWith = err
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		roles:       make(map[fakeRoleKey]string),
		memberships: make(map[fakeMembership]accesstypes.Period),
		reasons:     make(map[fakeMembership]string),
		grants:      make(map[fakeGrant]bool),
	}
}

// now is the store's present. The caller holds the lock.
func (f *fakeStore) now() time.Time {
	if f.clock != nil {
		return f.clock()
	}

	return time.Now()
}

func (f *fakeStore) ReadPolicy(_ context.Context) (*policy.Records, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}

	records := &policy.Records{}
	for r := range f.roles {
		records.Roles = append(records.Roles, policy.Role{Scope: r.scope, Name: r.role})
	}
	for g := range f.grants {
		records.Grants = append(records.Grants, policy.Grant{
			Scope:     g.scope,
			Subject:   policy.Subject{Kind: policy.SubjectRole, Name: string(g.role)},
			Perm:      g.perm,
			Resource:  g.resource,
			Field:     g.field,
			Condition: g.condition,
		})
	}
	for m, period := range f.memberships {
		records.Memberships = append(records.Memberships, policy.Membership{
			Scope:  m.scope,
			Member: policy.Subject{Kind: policy.SubjectUser, Name: string(m.user)},
			Role:   m.role,
			Period: period,
		})
	}
	f.groupRecords(records)

	return records, nil
}

func (f *fakeStore) InsertUserRole(_ context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	f.insertUserRole(scope, user, role, period, reason)

	return nil
}

func (f *fakeStore) InsertCustomUserRole(_ context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	if !f.customRoleHeld(scope, role) {
		return errors.Wrapf(policy.ErrNoRole, "role %q in scope %s", role, scope)
	}
	f.insertUserRole(scope, user, role, period, reason)

	return nil
}

// insertUserRole writes the membership with its period and its reason; the caller holds
// mu.
func (f *fakeStore) insertUserRole(scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period, reason string) {
	f.memberships[fakeMembership{scope, user, role}] = period
	delete(f.reasons, fakeMembership{scope, user, role})
	if reason != "" {
		f.reasons[fakeMembership{scope, user, role}] = reason
	}
	f.writes++
}

// customRoleHeld reports whether a holding of the custom role in scope stands on a role
// row: the scope's own, or every domain's for one domain. The caller holds mu.
func (f *fakeStore) customRoleHeld(scope accesstypes.PolicyScope, role accesstypes.Role) bool {
	if f.hasRole(scope, role) {
		return true
	}
	_, oneDomain := scope.Domain()

	return oneDomain && f.hasRole(accesstypes.EveryDomainPolicyScope(), role)
}

func (f *fakeStore) DeleteUserRole(_ context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	delete(f.memberships, fakeMembership{scope, user, role})
	delete(f.reasons, fakeMembership{scope, user, role})
	f.writes++

	return nil
}

func (f *fakeStore) ListUserRoles(_ context.Context, scope accesstypes.PolicyScope, user accesstypes.User) ([]accesstypes.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	roles := make([]accesstypes.Role, 0)
	for m, period := range f.memberships {
		if m.scope == scope && m.user == user && period.Contains(f.now()) {
			roles = append(roles, m.role)
		}
	}
	slices.Sort(roles)

	return roles, nil
}

func (f *fakeStore) ListUserMemberships(_ context.Context, user accesstypes.User) ([]policy.Membership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	memberships := make([]policy.Membership, 0)
	for m, period := range f.memberships {
		if m.user == user && period.Contains(f.now()) {
			memberships = append(memberships, policy.Membership{Scope: m.scope, Member: policy.Subject{Kind: policy.SubjectUser, Name: string(user)}, Role: m.role, Period: period})
		}
	}
	slices.SortFunc(memberships, func(a, b policy.Membership) int {
		if c := policy.CompareScopes(a.Scope, b.Scope); c != 0 {
			return c
		}

		return strings.Compare(string(a.Role), string(b.Role))
	})

	return memberships, nil
}

func (f *fakeStore) ListRoleUsers(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]accesstypes.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	users := make([]accesstypes.User, 0)
	for m, period := range f.memberships {
		if m.scope == scope && m.role == role && period.Contains(f.now()) {
			users = append(users, m.user)
		}
	}
	slices.Sort(users)

	return users, nil
}

// InsertRole creates the role with its description; an existing role is left
// as it is, description included. A name that differs only in case from a
// custom role held where the two are read side by side is refused with a
// *policy.CaseTwinError, under the lock that makes it one step with the write.
func (f *fakeStore) InsertRole(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, description string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	if _, exists := f.roles[fakeRoleKey{scope, role}]; exists {
		return nil
	}
	held := make([]policy.Role, 0, len(f.roles))
	for key := range f.roles {
		held = append(held, policy.Role{Scope: key.scope, Name: key.role})
	}
	if twin, found := policy.CaseTwin(held, scope, role); found {
		return errors.Wrapf(&policy.CaseTwinError{Held: twin}, "role %q in scope %s", role, scope)
	}
	f.roles[fakeRoleKey{scope, role}] = description

	return nil
}

// writeRoleUnchecked writes a role row with no rule: the pair of custom roles
// that differ only in case a store written before the rule may hold, which
// InsertRole no longer makes.
func (f *fakeStore) writeRoleUnchecked(scope accesstypes.PolicyScope, role accesstypes.Role, description string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.roles[fakeRoleKey{scope, role}] = description
}

func (f *fakeStore) ListRoles(_ context.Context, scope accesstypes.PolicyScope) ([]policy.RoleRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	roles := make([]policy.RoleRow, 0)
	for k, description := range f.roles {
		if k.scope == scope {
			roles = append(roles, policy.RoleRow{Name: k.role, Description: description})
		}
	}
	slices.SortFunc(roles, func(a, b policy.RoleRow) int {
		return strings.Compare(string(a.Name), string(b.Name))
	})

	return roles, nil
}

// hasRole reports whether the role row exists; the caller holds mu.
func (f *fakeStore) hasRole(scope accesstypes.PolicyScope, role accesstypes.Role) bool {
	_, exists := f.roles[fakeRoleKey{scope, role}]

	return exists
}

func (f *fakeStore) DeleteRole(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return false, f.failWith
	}
	for m, period := range f.memberships {
		if m.scope == scope && m.role == role && !period.Ended(f.now()) {
			return false, errors.Wrapf(policy.ErrStillAssigned, "role %q in scope %q still has members", role, scope)
		}
	}
	for r, period := range f.groups.held {
		if r.scope == scope && r.role == role && !period.Ended(f.now()) {
			return false, errors.Wrapf(policy.ErrHeldByGroup, "role %q in scope %q", role, scope)
		}
	}
	if err := f.refuseHeldInDomain(scope, role); err != nil {
		return false, err
	}
	if !f.hasRole(scope, role) {
		return false, nil
	}
	delete(f.roles, fakeRoleKey{scope, role})
	// The memberships of the role that have ended go with it, a person's and a
	// group's, and for a role held in every domain the ones held in one domain
	// that read its row.
	for m := range f.memberships {
		if m.role == role && (m.scope == scope || f.readsEveryDomainRow(scope, m.scope, role)) {
			delete(f.memberships, m)
		}
	}
	for r := range f.groups.held {
		if r.role == role && (r.scope == scope || f.readsEveryDomainRow(scope, r.scope, role)) {
			delete(f.groups.held, r)
		}
	}
	for g := range f.grants {
		if g.scope == scope && g.role == role {
			delete(f.grants, g)
		}
	}

	return true, nil
}

// readsEveryDomainRow reports whether a holding of role held in scope reads the row a
// deletion in deleted deletes, where that is every domain's: the holding is held in
// one domain that holds no role of the name of its own. The caller holds mu.
func (f *fakeStore) readsEveryDomainRow(deleted, scope accesstypes.PolicyScope, role accesstypes.Role) bool {
	_, oneDomain := scope.Domain()

	return deleted.IsEveryDomain() && oneDomain && !f.hasRole(scope, role)
}

// refuseHeldInDomain refuses the deletion of a role held in every domain while a
// holding of it in one domain that has not ended reads its row, naming the lowest such
// domain, a person's before a group's. The caller holds mu.
func (f *fakeStore) refuseHeldInDomain(scope accesstypes.PolicyScope, role accesstypes.Role) error {
	var people, groups []string
	for m, period := range f.memberships {
		if m.role == role && !period.Ended(f.now()) && f.readsEveryDomainRow(scope, m.scope, role) {
			domain, _ := m.scope.Domain()
			people = append(people, string(domain))
		}
	}
	for r, period := range f.groups.held {
		if r.role == role && !period.Ended(f.now()) && f.readsEveryDomainRow(scope, r.scope, role) {
			domain, _ := r.scope.Domain()
			groups = append(groups, string(domain))
		}
	}
	if len(people) > 0 {
		return errors.Wrapf(policy.ErrHeldInDomain, "role %q: a person holds it in the domain %q", role, slices.Min(people))
	}
	if len(groups) > 0 {
		return errors.Wrapf(policy.ErrHeldInDomain, "role %q: a group holds it in the domain %q", role, slices.Min(groups))
	}

	return nil
}

func (f *fakeStore) RoleExists(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return false, f.failWith
	}

	return f.hasRole(scope, role), nil
}

func (f *fakeStore) InsertGrant(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field, condition string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	if !f.hasRole(scope, role) {
		return errors.Wrapf(policy.ErrNoRole, "role %q in scope %s", role, scope)
	}
	f.grants[fakeGrant{scope, role, perm, resource, field, condition}] = true

	return nil
}

func (f *fakeStore) InsertGrants(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, grants []policy.RoleGrant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batchWrites++
	if f.failWith != nil {
		return f.failWith
	}
	if !f.hasRole(scope, role) {
		return errors.Wrapf(policy.ErrNoRole, "role %q in scope %s", role, scope)
	}
	for _, g := range grants {
		f.grants[fakeGrant{scope, role, g.Perm, g.Resource, g.Field, g.Condition}] = true
	}

	return nil
}

func (f *fakeStore) ChangeGrants(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, removals, additions []policy.RoleGrant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changeWrites++
	f.lastChange = fakeChange{removals: slices.Clone(removals), additions: slices.Clone(additions)}
	if f.failWith != nil {
		return f.failWith
	}
	if len(additions) > 0 && !f.hasRole(scope, role) {
		return errors.Wrapf(policy.ErrNoRole, "role %q in scope %s", role, scope)
	}
	for _, g := range removals {
		delete(f.grants, fakeGrant{scope, role, g.Perm, g.Resource, g.Field, g.Condition})
	}
	for _, g := range additions {
		f.grants[fakeGrant{scope, role, g.Perm, g.Resource, g.Field, g.Condition}] = true
	}

	return nil
}

func (f *fakeStore) DeleteGrant(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field, condition string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	if f.failWith != nil {
		return f.failWith
	}
	delete(f.grants, fakeGrant{scope, role, perm, resource, field, condition})

	return nil
}

func (f *fakeStore) DeleteGrants(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	for g := range f.grants {
		if g.scope == scope && g.role == role && g.perm == perm && g.resource == resource && g.field == field {
			delete(f.grants, g)
		}
	}

	return nil
}

func (f *fakeStore) ListRoleGrants(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]policy.RoleGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	grants := make([]policy.RoleGrant, 0)
	for g := range f.grants {
		if g.scope == scope && g.role == role {
			grants = append(grants, policy.RoleGrant{Perm: g.perm, Resource: g.resource, Field: g.field, Condition: g.condition})
		}
	}
	slices.SortFunc(grants, func(a, b policy.RoleGrant) int {
		if c := strings.Compare(string(a.Perm), string(b.Perm)); c != 0 {
			return c
		}
		if c := strings.Compare(a.Resource, b.Resource); c != 0 {
			return c
		}
		if c := strings.Compare(a.Field, b.Field); c != 0 {
			return c
		}

		return strings.Compare(a.Condition, b.Condition)
	})

	return grants, nil
}
