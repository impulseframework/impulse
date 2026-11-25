package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// The fixtures of the period phase, kept apart from the other phases': a tenant
// of its own, one custom role there, three people and two groups. The phase
// removes what it writes, so the policy read after it finds what the phases
// before it left.
var (
	periodTenant = accesstypes.DomainPolicyScope("periods")
	periodScope  = accesstypes.DomainScope("periods")
)

const (
	watch   = accesstypes.Role("Watch")
	relieve = accesstypes.Role("Relieve")

	dan = accesstypes.User("dan")
	eve = accesstypes.User("eve")
	fay = accesstypes.User("fay")
	gil = accesstypes.User("gil")
	hal = accesstypes.User("hal")

	shiftGroup  = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-000000000004")
	reliefGroup = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-000000000005")
)

// periods are the three periods the phase assigns with, around the present by
// an hour each way: one that has ended, one that has not begun and one that
// counts now. The instants carry microseconds, which every store keeps.
type periods struct {
	now                      time.Time
	ended, upcoming, current accesstypes.Period
}

func newPeriods() periods {
	now := time.Now().UTC().Truncate(time.Microsecond)

	return periods{
		now:      now,
		ended:    accesstypes.Period{Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)},
		upcoming: accesstypes.Period{Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)},
		current:  accesstypes.Period{Start: now.Add(-time.Hour), End: now.Add(time.Hour)},
	}
}

// runPeriods holds the stores to the contract of an assignment's period: the
// row keeps the two instants; the listings of what a person or a group holds
// leave out an assignment outside its period while its row still stands, and
// the policy read returns every row with its period; assigning again replaces
// the period; a deletion that assignments block counts the ones that have not
// ended and takes the ended ones with it; and a client over the store grants
// nothing before a period and after it, and inside it what the role grants,
// with no row written in between.
func runPeriods(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()
	p := newPeriods()

	if err := store.InsertRole(ctx, periodTenant, watch, "Stands the watch for the period tests."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	if err := store.InsertGrant(ctx, periodTenant, watch, readPerm, widgets, "", ""); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}
	for _, group := range []*policy.GroupRow{
		{Group: policy.Group{ID: shiftGroup, Scope: periodTenant}, Name: "Shift", Description: "Who stands the watch by the shift."},
		{Group: policy.Group{ID: reliefGroup, Scope: periodTenant}, Name: "Relief", Description: "Who relieves the watch."},
	} {
		if taken, err := store.InsertGroup(ctx, group); err != nil || taken {
			t.Fatalf("InsertGroup(%s) = (%v, %v), want it written", group.Name, taken, err)
		}
	}

	t.Run("a person's role", func(t *testing.T) { periodMemberships(t, store, &p) })
	t.Run("a place in a group and a group's role", func(t *testing.T) { periodGroups(t, store, &p) })
	t.Run("the check", func(t *testing.T) { periodChecks(t, store, &p) })
	t.Run("a role a group holds is not deleted", func(t *testing.T) { roleHeldByGroup(t, store, &p) })
	t.Run("deletions", func(t *testing.T) { periodDeletes(t, store) })
}

// periodRecords reads the policy and answers what the period phase holds in
// it: the memberships held in its tenant and the members of its two groups.
func periodRecords(t *testing.T, store access.Store) (memberships []policy.Membership, members []policy.GroupMember) {
	t.Helper()
	records, err := store.ReadPolicy(t.Context())
	if err != nil {
		t.Fatalf("ReadPolicy() error = %v", err)
	}
	sortRecords(records)
	for i := range records.Memberships {
		if records.Memberships[i].Scope == periodTenant {
			memberships = append(memberships, records.Memberships[i])
		}
	}
	for _, m := range records.GroupMembers {
		if m.Group == shiftGroup || m.Group == reliefGroup {
			members = append(members, m)
		}
	}

	return memberships, members
}

// own is a fixture shorthand for a person's own membership of the watch.
func own(user accesstypes.User, period accesstypes.Period) policy.Membership {
	return policy.Membership{Scope: periodTenant, Member: policy.Subject{Kind: policy.SubjectUser, Name: string(user)}, Role: watch, Period: period}
}

// periodMemberships holds a person's role to its period. It leaves dan's role
// ended, eve's not begun and fay's counting.
func periodMemberships(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()

	assigned := []struct {
		user   accesstypes.User
		period accesstypes.Period
	}{{dan, p.ended}, {eve, p.upcoming}, {fay, p.current}}
	for _, a := range assigned {
		if err := store.InsertUserRole(ctx, periodTenant, a.user, watch, a.period, ""); err != nil {
			t.Fatalf("InsertUserRole(%s) error = %v", a.user, err)
		}
	}
	// The same period again is the no-op an insert always was.
	if err := store.InsertUserRole(ctx, periodTenant, fay, watch, p.current, ""); err != nil {
		t.Fatalf("InsertUserRole() with the period the membership has: error = %v", err)
	}

	// The rows all stand, each with its period.
	memberships, _ := periodRecords(t, store)
	want := []policy.Membership{own(dan, p.ended), own(eve, p.upcoming), own(fay, p.current)}
	if diff := cmp.Diff(want, memberships, equateScopes); diff != "" {
		t.Errorf("ReadPolicy() memberships of the period tenant (-want +got):\n%s", diff)
	}

	// The listings hold the one that counts now.
	if users, err := store.ListRoleUsers(ctx, periodTenant, watch); err != nil || !cmp.Equal(users, []accesstypes.User{fay}) {
		t.Errorf("ListRoleUsers() = (%v, %v), want fay alone: dan's role has ended and eve's has not begun", users, err)
	}
	for _, a := range assigned {
		counts := a.user == fay
		roles, err := store.ListUserRoles(ctx, periodTenant, a.user)
		if err != nil || (len(roles) == 1) != counts {
			t.Errorf("ListUserRoles(%s) = (%v, %v), want the role listed = %v", a.user, roles, err, counts)
		}
		held, err := store.ListUserMemberships(ctx, a.user)
		if err != nil || (len(held) == 1) != counts {
			t.Errorf("ListUserMemberships(%s) = (%v, %v), want the membership listed = %v", a.user, held, err, counts)
		}
		if counts {
			if diff := cmp.Diff([]policy.Membership{own(fay, p.current)}, held, equateScopes); diff != "" {
				t.Errorf("ListUserMemberships(fay) (-want +got):\n%s", diff)
			}
		}
	}

	// Assigning again replaces the period: with none, dan's role counts until it
	// is removed, and with the ended one again it counts for nothing.
	if err := store.InsertUserRole(ctx, periodTenant, dan, watch, accesstypes.Period{}, ""); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}
	if roles, err := store.ListUserRoles(ctx, periodTenant, dan); err != nil || !cmp.Equal(roles, []accesstypes.Role{watch}) {
		t.Errorf("ListUserRoles(dan) after the role was given again with no period = (%v, %v), want the role", roles, err)
	}
	memberships, _ = periodRecords(t, store)
	if len(memberships) != 3 || !memberships[0].Period.IsZero() {
		t.Errorf("ReadPolicy() after the role was given again = %v, want three memberships, dan's with no period", memberships)
	}
	if err := store.InsertUserRole(ctx, periodTenant, dan, watch, p.ended, ""); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}
	if roles, err := store.ListUserRoles(ctx, periodTenant, dan); err != nil || len(roles) != 0 {
		t.Errorf("ListUserRoles(dan) after the ended period was given again = (%v, %v), want none", roles, err)
	}
}

// periodGroups holds a person's place in a group and a group's role to their
// periods. It leaves the shift with dan's place ended, eve's not begun and
// fay's counting, holding the watch for a period that has ended.
func periodGroups(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()

	places := []struct {
		user   accesstypes.User
		period accesstypes.Period
	}{{dan, p.ended}, {eve, p.upcoming}, {fay, p.current}}
	for _, place := range places {
		if err := store.InsertGroupMember(ctx, shiftGroup, place.user, place.period); err != nil {
			t.Fatalf("InsertGroupMember(%s) error = %v", place.user, err)
		}
	}
	if err := store.InsertGroupRole(ctx, shiftGroup, periodTenant, watch, p.current); err != nil {
		t.Fatalf("InsertGroupRole() error = %v", err)
	}

	_, members := periodRecords(t, store)
	wantMembers := []policy.GroupMember{
		{Group: shiftGroup, User: dan, Period: p.ended},
		{Group: shiftGroup, User: eve, Period: p.upcoming},
		{Group: shiftGroup, User: fay, Period: p.current},
	}
	if diff := cmp.Diff(wantMembers, members); diff != "" {
		t.Errorf("ReadPolicy() members of the shift (-want +got):\n%s", diff)
	}
	if users, err := store.ListGroupMembers(ctx, shiftGroup); err != nil || !cmp.Equal(users, []accesstypes.User{fay}) {
		t.Errorf("ListGroupMembers() = (%v, %v), want fay alone", users, err)
	}
	groupRole := policy.Membership{Scope: periodTenant, Member: policy.Subject{Kind: policy.SubjectGroup, Name: string(shiftGroup)}, Role: watch, Period: p.current}
	if roles, err := store.ListGroupRoles(ctx, shiftGroup); err != nil || !cmp.Equal(roles, []policy.Membership{groupRole}, equateScopes) {
		t.Errorf("ListGroupRoles() = (%v, %v), want the watch with its period", roles, err)
	}
	for _, place := range places {
		counts := place.user == fay
		groups, err := store.ListUserGroups(ctx, place.user)
		if err != nil || (len(groups) == 1) != counts {
			t.Errorf("ListUserGroups(%s) = (%v, %v), want the shift listed = %v", place.user, groups, err, counts)
		}
		through, err := store.ListUserGroupRoles(ctx, place.user)
		if err != nil || (len(through) == 1) != counts {
			t.Errorf("ListUserGroupRoles(%s) = (%v, %v), want the watch listed = %v", place.user, through, err, counts)
		}
	}

	// The group's role given again for a period that has ended: nobody holds it
	// through the group, whoever's place counts.
	if err := store.InsertGroupRole(ctx, shiftGroup, periodTenant, watch, p.ended); err != nil {
		t.Fatalf("InsertGroupRole() error = %v", err)
	}
	if roles, err := store.ListGroupRoles(ctx, shiftGroup); err != nil || len(roles) != 0 {
		t.Errorf("ListGroupRoles() after the role's period was replaced by one that has ended = (%v, %v), want none", roles, err)
	}
	if through, err := store.ListUserGroupRoles(ctx, fay); err != nil || len(through) != 0 {
		t.Errorf("ListUserGroupRoles(fay) after the group's role ended = (%v, %v), want none", through, err)
	}
	memberships, _ := periodRecords(t, store)
	groupRole.Period = p.ended
	if !slices.ContainsFunc(memberships, func(m policy.Membership) bool { return cmp.Equal(m, groupRole, equateScopes) }) {
		t.Errorf("ReadPolicy() memberships = %v, want the group's role with the period that replaced the first", memberships)
	}
}

// periodChecks holds a client over the store to the period on the check: a
// person's own role, and a role held through a group whose role and whose
// place each carry a period, grant nothing before the period and from its end
// on, and inside it what the role grants, with no row written in between. The
// instants are the Environment's, so nothing waits for a clock.
func periodChecks(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()
	hours := func(n float64) time.Time { return p.now.Add(time.Duration(n * float64(time.Hour))) }
	between := func(start, end float64) accesstypes.Period {
		return accesstypes.Period{Start: hours(start), End: hours(end)}
	}

	// gil holds the watch himself from the third hour until the fourth; hal is
	// in the relief from the sixth hour until the ninth, and the relief holds
	// the watch from the seventh until the eighth.
	if err := store.InsertUserRole(ctx, periodTenant, gil, watch, between(3, 4), ""); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}
	if err := store.InsertGroupMember(ctx, reliefGroup, hal, between(6, 9)); err != nil {
		t.Fatalf("InsertGroupMember() error = %v", err)
	}
	if err := store.InsertGroupRole(ctx, reliefGroup, periodTenant, watch, between(7, 8)); err != nil {
		t.Fatalf("InsertGroupRole() error = %v", err)
	}

	client, err := access.New(store, suiteCollection, access.WithHeartbeatInterval(time.Hour))
	if err != nil {
		t.Fatalf("access.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() error = %v", err)
		}
	})
	before, err := store.ReadPolicy(ctx)
	if err != nil {
		t.Fatalf("ReadPolicy() error = %v", err)
	}

	steps := []struct {
		user accesstypes.User
		at   float64
		want bool
	}{
		{gil, 2.5, false},
		{gil, 3, true},
		{gil, 3.5, true},
		{gil, 4, false},
		{gil, 5, false},
		{hal, 6.5, false},
		{hal, 7, true},
		{hal, 7.5, true},
		{hal, 8, false},
		{hal, 8.5, false},
		// An instant behind the ones already asked is answered by its own window.
		{gil, 3.25, true},
		{hal, 7.25, true},
		{gil, 0, false},
		// fay's role counts now and ends in an hour; dan's has ended and eve's has not begun.
		{fay, 0, true},
		{fay, 1, false},
		{dan, 0, false},
		{eve, 0, false},
		{eve, 1.5, true},
	}
	for _, step := range steps {
		decisions, err := client.CheckUserResources(ctx, accesstypes.EnvironmentAt(hours(step.at)), step.user, periodScope, readPerm, widgets)
		if err != nil {
			t.Fatalf("CheckUserResources(%s) error = %v", step.user, err)
		}
		if got := decisions[widgets].IsGranted(); got != step.want {
			t.Errorf("%s reads widgets %v hours on = %v, want %v", step.user, step.at, got, step.want)
		}
	}
	after, err := store.ReadPolicy(ctx)
	if err != nil {
		t.Fatalf("ReadPolicy() error = %v", err)
	}
	if before.Hash() != after.Hash() {
		t.Error("the store's rows changed while the periods began and ended: nothing is written in between")
	}

	// The check's own assignments go, so the deletions after it find the rows
	// the phases before it left.
	if err := errors.Join(
		store.DeleteUserRole(ctx, periodTenant, gil, watch),
		store.DeleteGroupMember(ctx, reliefGroup, hal),
		store.DeleteGroupRole(ctx, reliefGroup, periodTenant, watch),
	); err != nil {
		t.Errorf("removing the check's assignments: %v", err)
	}
}

// roleHeldByGroup holds a role's deletion to the groups that hold the role: a
// group's row of it that counts, or will, refuses the deletion as
// policy.ErrHeldByGroup, whether or not the group has anyone in it, and one
// that has ended goes with the role. A role deleted from under a group would
// leave the row behind, and a role of the name made again would at once be
// held by everyone in the group. The listing of the groups a role is held
// through reads the rows that count now. It removes what it writes.
func roleHeldByGroup(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()

	if err := store.InsertRole(ctx, periodTenant, relieve, "Relieves the watch for the period tests."); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	relief := policy.GroupRow{Group: policy.Group{ID: reliefGroup, Scope: periodTenant}, Name: "Relief", Description: "Who relieves the watch."}
	for _, held := range []struct {
		name   string
		period accesstypes.Period
		listed []policy.GroupRow
	}{
		{name: "now", period: p.current, listed: []policy.GroupRow{relief}},
		{name: "for a period that has not begun", period: p.upcoming, listed: []policy.GroupRow{}},
	} {
		if err := store.InsertGroupRole(ctx, reliefGroup, periodTenant, relieve, held.period); err != nil {
			t.Fatalf("InsertGroupRole() error = %v", err)
		}
		groups, err := store.ListRoleGroups(ctx, periodTenant, relieve)
		if err != nil {
			t.Fatalf("ListRoleGroups() error = %v", err)
		}
		if diff := cmp.Diff(held.listed, groups, equateScopes); diff != "" {
			t.Errorf("ListRoleGroups() while the group holds the role %s (-want +got):\n%s", held.name, diff)
		}
		if _, err := store.DeleteRole(ctx, periodTenant, relieve); !errors.Is(err, policy.ErrHeldByGroup) {
			t.Fatalf("DeleteRole() while a group holds the role %s: error = %v, want policy.ErrHeldByGroup, which the manager words for the caller", held.name, err)
		}
		if exists, err := store.RoleExists(ctx, periodTenant, relieve); err != nil || !exists {
			t.Fatalf("RoleExists() after the refused delete = (%v, %v), want the role still there", exists, err)
		}
	}
	if groups, err := store.ListRoleGroups(ctx, periodTenant, watch); err != nil || len(groups) != 0 {
		t.Errorf("ListRoleGroups() of a role no group holds now = (%v, %v), want none", groups, err)
	}

	if err := store.InsertGroupRole(ctx, reliefGroup, periodTenant, relieve, p.ended); err != nil {
		t.Fatalf("InsertGroupRole() error = %v", err)
	}
	if deleted, err := store.DeleteRole(ctx, periodTenant, relieve); err != nil || !deleted {
		t.Fatalf("DeleteRole() once the group's role has ended = (%v, %v), want it deleted", deleted, err)
	}
	memberships, _ := periodRecords(t, store)
	if slices.ContainsFunc(memberships, func(m policy.Membership) bool { return m.Role == relieve }) {
		t.Errorf("ReadPolicy() still holds a row of the role among %v: the group's row that had ended goes with the role", memberships)
	}
}

// periodDeletes holds the two deletions to the periods: a role's, which an
// assignment that has not begun stands in the way of as one that counts does,
// refused as policy.ErrStillAssigned, the ones that have ended going with the
// role; and a group's, which takes every place and role of the group, whatever
// their periods. It removes the phase's role and groups.
func periodDeletes(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	// The watch: dan's role has ended, eve's has not begun, fay's counts.
	for _, holder := range []accesstypes.User{fay, eve} {
		if _, err := store.DeleteRole(ctx, periodTenant, watch); !errors.Is(err, policy.ErrStillAssigned) {
			t.Fatalf("DeleteRole() while %s holds the role or will: error = %v, want policy.ErrStillAssigned", holder, err)
		}
		if err := store.DeleteUserRole(ctx, periodTenant, holder, watch); err != nil {
			t.Fatalf("DeleteUserRole(%s) error = %v", holder, err)
		}
	}
	if deleted, err := store.DeleteRole(ctx, periodTenant, watch); err != nil || !deleted {
		t.Fatalf("DeleteRole() once the only membership left has ended = (%v, %v), want it deleted", deleted, err)
	}
	memberships, _ := periodRecords(t, store)
	if slices.ContainsFunc(memberships, func(m policy.Membership) bool { return m.Member.Kind == policy.SubjectUser }) {
		t.Errorf("ReadPolicy() still holds a person's membership among %v: the one that had ended goes with the role", memberships)
	}

	// The shift: dan's place has ended, eve's has not begun, fay's counts, and
	// the role it holds has ended. All of it goes with the group.
	if deleted, err := store.DeleteGroup(ctx, shiftGroup); err != nil || !deleted {
		t.Fatalf("DeleteGroup() of the shift with its places and its role = (%v, %v), want it deleted", deleted, err)
	}
	if deleted, err := store.DeleteGroup(ctx, reliefGroup); err != nil || !deleted {
		t.Fatalf("DeleteGroup() of the relief = (%v, %v), want it deleted", deleted, err)
	}
	memberships, members := periodRecords(t, store)
	if len(memberships) != 0 || len(members) != 0 {
		t.Errorf("ReadPolicy() after the deletions holds %v and %v, want nothing of the period phase", memberships, members)
	}
	if roles, err := store.ListRoles(ctx, periodTenant); err != nil || len(roles) != 0 {
		t.Errorf("ListRoles() of the period tenant = (%v, %v), want none", roles, err)
	}
}
