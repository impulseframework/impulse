package access

import (
	"context"
	"slices"
	"strings"

	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// The fake store's listings of what is held and why (holdings.go): each reads what has
// not ended at the store's present, as the stores' statements do.

func (f *fakeStore) ListUserAssignments(_ context.Context, user accesstypes.User) ([]policy.Assignment, error) {
	assignments := make([]policy.Assignment, 0)
	err := f.locked(func() error {
		for m, period := range f.memberships {
			if m.user != user || period.Ended(f.now()) {
				continue
			}
			assignments = append(assignments, policy.Assignment{
				Membership: policy.Membership{Scope: m.scope, Member: policy.Subject{Kind: policy.SubjectUser, Name: string(user)}, Role: m.role, Period: period},
				Reason:     f.reasons[m],
			})
		}
		slices.SortFunc(assignments, func(a, b policy.Assignment) int {
			if c := policy.CompareScopes(a.Scope, b.Scope); c != 0 {
				return c
			}

			return strings.Compare(string(a.Role), string(b.Role))
		})

		return nil
	})

	return assignments, err
}

func (f *fakeStore) CountGroupMembersIn(_ context.Context, scope accesstypes.PolicyScope) (map[accesstypes.Group]int, error) {
	counts := make(map[accesstypes.Group]int)
	err := f.locked(func() error {
		for m, period := range f.groups.members {
			if f.groups.rows[m.group].Scope == scope && !period.Ended(f.now()) {
				counts[m.group]++
			}
		}

		return nil
	})

	return counts, err
}

func (f *fakeStore) ListGroupRolesIn(_ context.Context, scope accesstypes.PolicyScope) ([]policy.Membership, error) {
	roles := make([]policy.Membership, 0)
	err := f.locked(func() error {
		for r, period := range f.groups.held {
			if f.groups.rows[r.group].Scope != scope || period.Ended(f.now()) {
				continue
			}
			roles = append(roles, policy.Membership{Scope: r.scope, Member: policy.Subject{Kind: policy.SubjectGroup, Name: string(r.group)}, Role: r.role, Period: period})
		}
		slices.SortFunc(roles, func(a, b policy.Membership) int {
			if c := strings.Compare(a.Member.Name, b.Member.Name); c != 0 {
				return c
			}
			if c := policy.CompareScopes(a.Scope, b.Scope); c != 0 {
				return c
			}

			return strings.Compare(string(a.Role), string(b.Role))
		})

		return nil
	})

	return roles, err
}
