package storetest

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// The fixtures of the holdings phase, kept apart from the other phases': a
// tenant of its own, one custom role there, three people and two groups. The
// phase removes what it writes, so the policy read after it finds what the
// phases before it left.
var holdingsTenant = accesstypes.DomainPolicyScope("holdings")

const (
	steward = accesstypes.Role("Steward")

	ivy = accesstypes.User("ivy")
	jon = accesstypes.User("jon")
	kim = accesstypes.User("kim")

	crewGroup = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-000000000006")
	idleGroup = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-000000000007")
)

// runHoldings holds the stores to the contract of what is said of the rows
// beside the policy: a person's role keeps the reason it was given with, and
// giving the role again replaces the reason with the period; the listing of a
// person's own assignments returns the ones that have not ended, each with its
// period and its reason; and the two listings of what the groups of a place
// hold count what has not ended.
func runHoldings(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()
	p := newPeriods()

	if err := store.InsertRole(ctx, holdingsTenant, steward, "Keeps the stores for the holdings tests."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}

	t.Run("a role's reason", func(t *testing.T) { holdingReasons(t, store, &p) })
	t.Run("what the groups of a place hold", func(t *testing.T) { holdingGroups(t, store, &p) })

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

// holdingGroups holds the two listings of what the groups of a place hold: how
// many people each group has and the roles it holds, both over what has not
// ended, a place or a role whose period has not begun among it.
func holdingGroups(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()

	for _, group := range []*policy.GroupRow{
		{Group: policy.Group{ID: crewGroup, Scope: holdingsTenant}, Name: "Crew", Description: "Who keeps the stores."},
		{Group: policy.Group{ID: idleGroup, Scope: holdingsTenant}, Name: "Spare", Description: "Nobody yet."},
	} {
		if taken, err := store.InsertGroup(ctx, group); err != nil || taken {
			t.Fatalf("InsertGroup(%s) = (%v, %v), want it written", group.Name, taken, err)
		}
	}
	// The crew: ivy's place counts, jon's has not begun, kim's has ended, and the
	// role it holds has not begun.
	for _, place := range []struct {
		user   accesstypes.User
		period accesstypes.Period
	}{{ivy, p.current}, {jon, p.upcoming}, {kim, p.ended}} {
		if err := store.InsertGroupMember(ctx, crewGroup, place.user, place.period); err != nil {
			t.Fatalf("InsertGroupMember(%s) error = %v", place.user, err)
		}
	}
	if err := store.InsertGroupRole(ctx, crewGroup, holdingsTenant, steward, p.upcoming); err != nil {
		t.Fatalf("InsertGroupRole() error = %v", err)
	}
	// A second role of the crew has ended: the listing leaves it out, and the
	// deletion below takes its row with the group like the rest.
	if err := store.InsertGroupRole(ctx, crewGroup, holdingsTenant, viewer, p.ended); err != nil {
		t.Fatalf("InsertGroupRole() of a role that has ended: error = %v", err)
	}

	counts, err := store.CountGroupMembersIn(ctx, holdingsTenant)
	if err != nil {
		t.Fatalf("CountGroupMembersIn() error = %v", err)
	}
	if diff := cmp.Diff(map[accesstypes.Group]int{crewGroup: 2}, counts); diff != "" {
		t.Errorf("CountGroupMembersIn() mismatch (-want +got): a place that counts and one that has not begun are counted, one that has ended is not, and a group with nobody is absent:\n%s", diff)
	}
	roles, err := store.ListGroupRolesIn(ctx, holdingsTenant)
	if err != nil {
		t.Fatalf("ListGroupRolesIn() error = %v", err)
	}
	wantRoles := []policy.Membership{{Scope: holdingsTenant, Member: policy.Subject{Kind: policy.SubjectGroup, Name: string(crewGroup)}, Role: steward, Period: p.upcoming}}
	if diff := cmp.Diff(wantRoles, roles, equateScopes); diff != "" {
		t.Errorf("ListGroupRolesIn() mismatch (-want +got): a role whose period has not begun is listed with it, and one that has ended is not:\n%s", diff)
	}
	// Another place's groups are not this one's, and a group is kept in no
	// "every tenant".
	if counts, err := store.CountGroupMembersIn(ctx, accesstypes.GlobalPolicyScope()); err != nil || counts[crewGroup] != 0 {
		t.Errorf("CountGroupMembersIn(global) = (%v, %v), want nothing of the tenant's group", counts, err)
	}
	if _, err := store.CountGroupMembersIn(ctx, accesstypes.EveryDomainPolicyScope()); err == nil {
		t.Error("CountGroupMembersIn(every domain) error = nil, want a refusal: no group is kept in every tenant")
	}
	if _, err := store.ListGroupRolesIn(ctx, accesstypes.EveryDomainPolicyScope()); err == nil {
		t.Error("ListGroupRolesIn(every domain) error = nil, want a refusal: no group is kept in every tenant")
	}

	holdingGroupDeletion(t, store)
}

// holdingGroupDeletion deletes the groups holdingGroups arranged, each with what
// it holds: the spare group, which holds nothing, and the crew, with its places
// and roles whatever their periods; the two listings then answer nothing for the
// tenant.
func holdingGroupDeletion(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	if deleted, err := store.DeleteGroup(ctx, idleGroup); err != nil || !deleted {
		t.Fatalf("DeleteGroup() of a group listed with nobody and no role = (%v, %v), want it deleted", deleted, err)
	}
	if deleted, err := store.DeleteGroup(ctx, crewGroup); err != nil || !deleted {
		t.Fatalf("DeleteGroup() of a group listed with two people and a role = (%v, %v), want it deleted with them", deleted, err)
	}
	if counts, err := store.CountGroupMembersIn(ctx, holdingsTenant); err != nil || len(counts) != 0 {
		t.Errorf("CountGroupMembersIn() once the groups are deleted = (%v, %v), want none", counts, err)
	}
	if roles, err := store.ListGroupRolesIn(ctx, holdingsTenant); err != nil || len(roles) != 0 {
		t.Errorf("ListGroupRolesIn() once the groups are deleted = (%v, %v), want none", roles, err)
	}
}
