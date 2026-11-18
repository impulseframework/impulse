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
