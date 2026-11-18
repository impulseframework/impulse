package storetest

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// The fixtures of the holdings phase, kept apart from the other phases': a
// tenant of its own, one custom role there and two people. The phase removes
// what it writes, so the policy read after it finds what the phases before it
// left.
var holdingsTenant = accesstypes.DomainPolicyScope("holdings")

const (
	steward = accesstypes.Role("Steward")

	ivy = accesstypes.User("ivy")
	jon = accesstypes.User("jon")
)

// runHoldings holds the stores to the contract of what is said of the rows
// beside the policy: a person's role keeps the reason it was given with, and
// giving the role again replaces the reason with the period; and the listing of
// a person's own assignments returns the ones that have not ended, each with
// its period and its reason.
func runHoldings(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()
	p := newPeriods()

	if err := store.InsertRole(ctx, holdingsTenant, steward, "Keeps the stores for the holdings tests."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}

	t.Run("a role's reason", func(t *testing.T) { holdingReasons(t, store, &p) })

	if deleted, err := store.DeleteRole(ctx, holdingsTenant, steward); err != nil || !deleted {
		t.Fatalf("DeleteRole() of the phase's role = (%v, %v), want it deleted", deleted, err)
	}
}

// assigned is one membership of ivy's own as ListUserAssignments returns it.
func assigned(period accesstypes.Period, reason string) policy.Assignment {
	return policy.Assignment{
		Membership: policy.Membership{Scope: holdingsTenant, Member: policy.Subject{Kind: policy.SubjectUser, Name: string(ivy)}, Role: steward, Period: period},
		Reason:     reason,
	}
}

// holdingReasons holds the reason of a person's role: written with the period,
// replaced with it when the role is given again, and listed with the role while
// the role has not ended.
func holdingReasons(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()

	steps := []struct {
		name   string
		period accesstypes.Period
		reason string
		want   []policy.Assignment
	}{
		{name: "given for a period with a reason", period: p.current, reason: "Covering the audit", want: []policy.Assignment{assigned(p.current, "Covering the audit")}},
		{name: "given again: the new period and the new reason take the old ones' place", period: accesstypes.Period{End: p.current.End}, reason: "Still covering", want: []policy.Assignment{assigned(accesstypes.Period{End: p.current.End}, "Still covering")}},
		{name: "given again with no reason: the reason it had goes", period: accesstypes.Period{}, reason: "", want: []policy.Assignment{assigned(accesstypes.Period{}, "")}},
		{name: "one that has not begun is listed", period: p.upcoming, reason: "From next week", want: []policy.Assignment{assigned(p.upcoming, "From next week")}},
		{name: "one that has ended is not", period: p.ended, reason: "Over", want: []policy.Assignment{}},
	}
	for _, step := range steps {
		if err := store.InsertUserRole(ctx, holdingsTenant, ivy, steward, step.period, step.reason); err != nil {
			t.Fatalf("%s: InsertUserRole() error = %v", step.name, err)
		}
		got, err := store.ListUserAssignments(ctx, ivy)
		if err != nil {
			t.Fatalf("%s: ListUserAssignments() error = %v", step.name, err)
		}
		if diff := cmp.Diff(step.want, got, equateScopes); diff != "" {
			t.Errorf("%s: ListUserAssignments() mismatch (-want +got):\n%s", step.name, diff)
		}
	}
	if got, err := store.ListUserAssignments(ctx, jon); err != nil || len(got) != 0 {
		t.Errorf("ListUserAssignments() of a person who holds nothing = (%v, %v), want none", got, err)
	}
	if err := store.DeleteUserRole(ctx, holdingsTenant, ivy, steward); err != nil {
		t.Fatalf("DeleteUserRole() error = %v", err)
	}
}
