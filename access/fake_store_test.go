package access

import (
	"context"
	"slices"
	"strings"
	"sync"

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
// row holds the description it was created with.
type fakeStore struct {
	mu          sync.Mutex
	roles       map[fakeRoleKey]string
	memberships map[fakeMembership]bool
	grants      map[fakeGrant]bool
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
		memberships: make(map[fakeMembership]bool),
		grants:      make(map[fakeGrant]bool),
	}
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
	for m := range f.memberships {
		records.Memberships = append(records.Memberships, policy.Membership{
			Scope:  m.scope,
			Member: policy.Subject{Kind: policy.SubjectUser, Name: string(m.user)},
			Role:   m.role,
		})
	}

	return records, nil
}

func (f *fakeStore) InsertUserRole(_ context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	f.memberships[fakeMembership{scope, user, role}] = true

	return nil
}

func (f *fakeStore) DeleteUserRole(_ context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	delete(f.memberships, fakeMembership{scope, user, role})

	return nil
}

func (f *fakeStore) ListUserRoles(_ context.Context, scope accesstypes.PolicyScope, user accesstypes.User) ([]accesstypes.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	roles := make([]accesstypes.Role, 0)
	for m := range f.memberships {
		if m.scope == scope && m.user == user {
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
	for m := range f.memberships {
		if m.user == user {
			memberships = append(memberships, policy.Membership{Scope: m.scope, Member: policy.Subject{Kind: policy.SubjectUser, Name: string(user)}, Role: m.role})
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
	for m := range f.memberships {
		if m.scope == scope && m.role == role {
			users = append(users, m.user)
		}
	}
	slices.Sort(users)

	return users, nil
}

// InsertRole creates the role with its description; an existing role is left
// as it is, description included.
func (f *fakeStore) InsertRole(_ context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, description string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	if _, exists := f.roles[fakeRoleKey{scope, role}]; exists {
		return nil
	}
	f.roles[fakeRoleKey{scope, role}] = description

	return nil
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
	for m := range f.memberships {
		if m.scope == scope && m.role == role {
			return false, errors.Newf("role %q in scope %q still has members", role, scope)
		}
	}
	if !f.hasRole(scope, role) {
		return false, nil
	}
	delete(f.roles, fakeRoleKey{scope, role})
	for g := range f.grants {
		if g.scope == scope && g.role == role {
			delete(f.grants, g)
		}
	}

	return true, nil
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
		return errors.Newf("role %q does not exist in scope %q", role, scope)
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
		return errors.Newf("role %q does not exist in scope %q", role, scope)
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
		return errors.Newf("role %q does not exist in scope %q", role, scope)
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
