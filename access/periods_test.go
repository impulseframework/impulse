package access

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// noon is the instant the period tests count their hours from.
var noon = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// hoursOn is the instant n hours after noon.
func hoursOn(n float64) time.Time {
	return noon.Add(time.Duration(n * float64(time.Hour)))
}

// between is the period from start hours after noon until end hours after it.
func between(start, end float64) accesstypes.Period {
	return accesstypes.Period{Start: hoursOn(start), End: hoursOn(end)}
}

// movedClock is a clock a test moves, safe for the engine's own goroutines.
type movedClock struct {
	nanos atomic.Int64
}

func newMovedClock(at time.Time) *movedClock {
	c := &movedClock{}
	c.set(at)

	return c
}

func (c *movedClock) set(at time.Time) {
	c.nanos.Store(at.UnixNano())
}

func (c *movedClock) now() time.Time {
	return time.Unix(0, c.nanos.Load()).UTC()
}

// Test_snapshot_periods pins what an assignment's period does to the compiled
// snapshot: before its start and from its end on it grants nothing, and
// between them what the role grants; the start is the first instant it counts
// and the end the first it does not; a person holds a group's role while their
// place in the group and the group's role both count; an assignment with no
// period always counts; and the snapshot answers for the window between the
// nearest starts and ends, no further.
func Test_snapshot_periods(t *testing.T) {
	t.Parallel()

	records := groupFixture()
	records.Memberships = []policy.Membership{
		{Scope: tenant1Policy, Member: userSubject("ann"), Role: "Editor", Period: between(1, 3)},
		{Scope: tenant1Policy, Member: groupSubject("night"), Role: "Auditor", Period: between(2, 6)},
		{Scope: tenant1Policy, Member: userSubject("cy"), Role: "Editor"},
	}
	records.GroupMembers = []policy.GroupMember{{Group: "night", User: "bob", Period: between(4, 8)}}

	tests := []struct {
		name         string
		at           time.Time
		ann, bob     bool
		since, until time.Time
	}{
		{name: "before any start", at: hoursOn(0), until: hoursOn(1)},
		{name: "at the start of a person's own role", at: hoursOn(1), ann: true, since: hoursOn(1), until: hoursOn(3)},
		{name: "the group's role has begun and the person's place has not", at: hoursOn(2.5), ann: true, since: hoursOn(1), until: hoursOn(3)},
		{name: "at the end of a person's own role", at: hoursOn(3), since: hoursOn(3), until: hoursOn(4)},
		{name: "the place and the group's role both count", at: hoursOn(4), bob: true, since: hoursOn(4), until: hoursOn(6)},
		{name: "the group's role has ended and the person's place has not", at: hoursOn(6), since: hoursOn(6)},
		{name: "after every end", at: hoursOn(9), since: hoursOn(6)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			snap, findings, err := newSnapshotAt(records, nil, declaringRecords(records), noon, tt.at)
			if err != nil || len(findings) != 0 {
				t.Fatalf("newSnapshotAt() = (%v, %v), want no finding", findings, err)
			}
			if got := holds(snap, "ann", tenant1Scope, "Read", "employees"); got != tt.ann {
				t.Errorf("ann reads employees = %v, want %v", got, tt.ann)
			}
			if got := holds(snap, "bob", tenant1Scope, "List", "widgets"); got != tt.bob {
				t.Errorf("bob lists widgets through the group = %v, want %v", got, tt.bob)
			}
			if !holds(snap, "cy", tenant1Scope, "Read", "employees") {
				t.Error("cy, whose role carries no period, does not read employees")
			}
			if !snap.since.Equal(tt.since) || !snap.until.Equal(tt.until) {
				t.Errorf("the snapshot answers from %v until %v, want from %v until %v", snap.since, snap.until, tt.since, tt.until)
			}
			if !snap.holdsAt(tt.at) {
				t.Error("holdsAt() is false at the instant the snapshot was compiled for")
			}
			if !tt.until.IsZero() && (snap.holdsAt(tt.until) || !snap.holdsAt(tt.until.Add(-time.Nanosecond))) {
				t.Errorf("holdsAt() around %v: the window ends at that instant and not before", tt.until)
			}
			if !tt.since.IsZero() && (!snap.holdsAt(tt.since) || snap.holdsAt(tt.since.Add(-time.Nanosecond))) {
				t.Errorf("holdsAt() around %v: the window begins at that instant and not before", tt.since)
			}
			if snap.timed != records {
				t.Error("the snapshot does not keep the records another window compiles from")
			}
		})
	}

	// Where no assignment carries a period there is one snapshot for all time,
	// and it keeps no records.
	untimed := groupFixture()
	untimed.Memberships = []policy.Membership{{Scope: tenant1Policy, Member: userSubject("cy"), Role: "Editor"}}
	snap, _, err := newSnapshot(untimed, nil, declaringRecords(untimed), noon)
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}
	if !snap.holdsAt(time.Time{}) || !snap.holdsAt(hoursOn(1e6)) || snap.timed != nil {
		t.Errorf("a snapshot of assignments without periods: holds from %v until %v, keeps records = %v; want all time and none", snap.since, snap.until, snap.timed != nil)
	}
}

// periodEngine is an engine over a fake store that holds the custom role Editor,
// which reads employees in tenant1, with one clock for the engine and the store.
func periodEngine(t *testing.T) (*snapshotEngine, *fakeStore, *movedClock) {
	t.Helper()
	ctx := t.Context()
	store := newFakeStore()
	clock := newMovedClock(noon)
	store.clock = clock.now
	if err := store.InsertRole(ctx, tenant1Policy, "Editor", aDescription); err != nil {
		t.Fatalf("InsertRole() error = %v", err)
	}
	if err := store.InsertGrant(ctx, tenant1Policy, "Editor", "Read", "employees", "", ""); err != nil {
		t.Fatalf("InsertGrant() error = %v", err)
	}
	e := testEngine(t, store, nil)
	e.clock = clock.now

	return e, store, clock
}

// Test_snapshotEngine_periods pins the check: an assignment before its start
// and after its end grants nothing, and inside it grants what the role grants,
// with no row written in between; the instant is the Environment's, the one a
// grant's condition folds with, and the engine's own clock where the
// Environment carries none; and the answers that take no Environment follow
// the engine's clock.
func Test_snapshotEngine_periods(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	e, store, clock := periodEngine(t)
	if err := store.InsertUserRole(ctx, tenant1Policy, "erin", "Editor", between(1, 2), ""); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}
	written := store.writes

	reads := func(env accesstypes.Environment) bool {
		t.Helper()
		got, err := e.checkUserResources(ctx, env, "erin", tenant1Scope, "Read", "employees")
		if err != nil {
			t.Fatalf("checkUserResources() error = %v", err)
		}

		return got[0].granted
	}

	// The engine's own clock, moved across the period.
	for _, step := range []struct {
		at   time.Time
		want bool
	}{{hoursOn(0), false}, {hoursOn(1), true}, {hoursOn(1.5), true}, {hoursOn(2), false}, {hoursOn(3), false}} {
		clock.set(step.at)
		if got := reads(accesstypes.NewEnvironment()); got != step.want {
			t.Errorf("at %v, by the engine's clock: erin reads employees = %v, want %v", step.at, got, step.want)
		}
		if has, err := e.userHasGrants(ctx, "erin", tenant1Scope); err != nil || has != step.want {
			t.Errorf("at %v: userHasGrants() = (%v, %v), want %v", step.at, has, err, step.want)
		}
		if digest, err := e.userDigest(ctx, "erin", tenant1Scope); err != nil || (len(digest) > 0) != step.want {
			t.Errorf("at %v: userDigest() = (%v, %v), want grants = %v", step.at, digest, err, step.want)
		}
		if !e.peek().holdsAt(step.at) {
			t.Errorf("at %v: the current snapshot answers from %v until %v; want the window of the present", step.at, e.peek().since, e.peek().until)
		}
	}
	if store.writes != written {
		t.Errorf("the store took %d writes while the period began and ended, want none", store.writes-written)
	}

	// The Environment's instant decides, whatever the engine's clock says.
	clock.set(hoursOn(3))
	for _, step := range []struct {
		at   time.Time
		want bool
	}{{hoursOn(1.5), true}, {hoursOn(0.5), false}, {hoursOn(1), true}, {hoursOn(2), false}, {hoursOn(1.25), true}} {
		if got := reads(accesstypes.EnvironmentAt(step.at)); got != step.want {
			t.Errorf("an Environment at %v: erin reads employees = %v, want %v", step.at, got, step.want)
		}
	}
	if !e.peek().holdsAt(hoursOn(3)) {
		t.Error("a check at another instant moved the current snapshot off the present")
	}
	if len(e.windows) > maxWindows {
		t.Errorf("the engine keeps %d windows, want at most %d", len(e.windows), maxWindows)
	}

	// A reload that finds the same records keeps the windows; one that finds
	// others answers from the new records at once.
	settle(e)
	if got := reads(accesstypes.EnvironmentAt(hoursOn(1.5))); !got {
		t.Error("after a reload of unchanged records: the role does not count inside its period")
	}
	if err := store.InsertUserRole(ctx, tenant1Policy, "erin", "Editor", between(5, 6), ""); err != nil {
		t.Fatalf("InsertUserRole() error = %v", err)
	}
	settle(e)
	if reads(accesstypes.EnvironmentAt(hoursOn(1.5))) || !reads(accesstypes.EnvironmentAt(hoursOn(5.5))) {
		t.Error("after the period was replaced: the check still answers by the old one")
	}
}

// periodClient is superClient with the custom role Editor made in tenant1, which
// reads Widgets there, and the store beside it.
func periodClient(t *testing.T) (*Client, UserManager, *fakeStore) {
	t.Helper()
	client, store := superClient(t)
	manager := client.UserManager()
	if err := manager.AddRole(t.Context(), tenant1Policy, "Editor", aDescription); err != nil {
		t.Fatalf("AddRole() error = %v", err)
	}
	if err := manager.AddRolePermissionResources(t.Context(), tenant1Policy, "Editor", "Read", "Widgets"); err != nil {
		t.Fatalf("AddRolePermissionResources() error = %v", err)
	}

	return client, manager, store
}

// readsWidgets reports whether user reads Widgets in tenant1 at the instant.
func readsWidgets(t *testing.T, client *Client, user accesstypes.User, at time.Time) bool {
	t.Helper()
	decisions, err := client.CheckUserResources(context.Background(), accesstypes.EnvironmentAt(at), user, tenant1Scope, "Read", "Widgets")
	if err != nil {
		t.Fatalf("CheckUserResources() error = %v", err)
	}

	return decisions["Widgets"].IsGranted()
}

// Test_userManager_AssignUserRole pins the management of a period: the listing
// a sign-in decides a person by leaves out an assignment that has ended or has
// not begun while its row still stands; assigning again replaces the period,
// and a role given again with none stands until it is removed; and a period
// that ends at or before its start is refused.
func Test_userManager_AssignUserRole(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, manager, store := periodClient(t)
	now := time.Now()
	held := func() []accesstypes.Role {
		t.Helper()
		roles, err := manager.UserRoles(ctx, "ann", tenant1Policy)
		if err != nil {
			t.Fatalf("UserRoles() error = %v", err)
		}

		return roles[tenant1Policy]
	}
	everywhere := func() accesstypes.RoleCollection {
		t.Helper()
		roles, err := manager.UserRoles(ctx, "ann")
		if err != nil {
			t.Fatalf("UserRoles() error = %v", err)
		}

		return roles
	}

	// An assignment that has ended: the row stands and counts for nothing.
	ended := accesstypes.Period{End: now.Add(-time.Hour)}
	if err := manager.AssignUserRole(ctx, tenant1Policy, "ann", "Editor", ended, ""); err != nil {
		t.Fatalf("AssignUserRole() error = %v", err)
	}
	if len(store.memberships) != 1 {
		t.Fatalf("the store holds %d memberships, want the one that ended", len(store.memberships))
	}
	if got := held(); len(got) != 0 {
		t.Errorf("UserRoles(tenant1) = %v, want none: the assignment has ended", got)
	}
	if got := everywhere(); len(got) != 0 {
		t.Errorf("UserRoles() = %v, want none: the assignment has ended", got)
	}
	if users, err := manager.RoleUsers(ctx, tenant1Policy, "Editor"); err != nil || len(users) != 0 {
		t.Errorf("RoleUsers() = (%v, %v), want none", users, err)
	}
	if readsWidgets(t, client, "ann", now) {
		t.Error("ann reads Widgets after her assignment ended")
	}

	// Given again with no period, it stands until it is removed.
	if err := manager.AddUserRoles(ctx, tenant1Policy, "ann", "Editor"); err != nil {
		t.Fatalf("AddUserRoles() error = %v", err)
	}
	if got := held(); !cmp.Equal(got, []accesstypes.Role{"Editor"}) {
		t.Errorf("UserRoles(tenant1) after the role was given again = %v, want Editor", got)
	}
	if !readsWidgets(t, client, "ann", now) || !readsWidgets(t, client, "ann", now.Add(1000*time.Hour)) {
		t.Error("ann does not read Widgets after the role was given again with no end")
	}

	// A new period replaces the old one: the role now begins in an hour.
	upcoming := accesstypes.Period{Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)}
	if err := manager.AssignUserRole(ctx, tenant1Policy, "ann", "Editor", upcoming, ""); err != nil {
		t.Fatalf("AssignUserRole() error = %v", err)
	}
	if len(store.memberships) != 1 {
		t.Errorf("the store holds %d memberships, want one: assigning again replaces the period", len(store.memberships))
	}
	if got := held(); len(got) != 0 {
		t.Errorf("UserRoles(tenant1) = %v, want none: the assignment has not begun", got)
	}
	if readsWidgets(t, client, "ann", now) || !readsWidgets(t, client, "ann", now.Add(90*time.Minute)) || readsWidgets(t, client, "ann", now.Add(2*time.Hour)) {
		t.Error("the check does not follow the new period: want denied now, granted inside it, denied from its end")
	}

	// A period that never counts is refused, and the assignment stays as it was.
	backwards := accesstypes.Period{Start: now.Add(2 * time.Hour), End: now.Add(time.Hour)}
	err := manager.AssignUserRole(ctx, tenant1Policy, "ann", "Editor", backwards, "")
	if err == nil || !httpio.HasBadRequest(err) || httpio.Message(err) != PeriodNeverCountsMessage {
		t.Errorf("AssignUserRole() with an end before its start: error = %v, want a bad request saying so", err)
	}
	if got := store.memberships[fakeMembership{tenant1Policy, "ann", "Editor"}]; !got.Start.Equal(upcoming.Start) || !got.End.Equal(upcoming.End) {
		t.Errorf("the stored period after the refusal = %v, want %v", got, upcoming)
	}
}

// Test_userManager_AssignUserRole_superAdministrator pins that the Super
// Administrator role takes no end, and no start either: there is always one,
// and a period could leave none.
func Test_userManager_AssignUserRole_superAdministrator(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, manager, store := periodClient(t)
	before := len(store.memberships)

	in := time.Now().Add(time.Hour)
	for name, period := range map[string]accesstypes.Period{
		"an end":             {End: in},
		"a start":            {Start: in},
		"a start and an end": {Start: in, End: in.Add(time.Hour)},
	} {
		err := manager.AssignUserRole(ctx, globalPolicy, "root", accesstypes.SuperAdministrator, period, "")
		if err == nil || !httpio.HasBadRequest(err) || httpio.Message(err) != "The Super administrator role takes no start and no end, because there is always a super administrator and a period could leave none. Give the role with no period, and remove it when it is no longer wanted." {
			t.Errorf("AssignUserRole() of the Super Administrator role with %s: error = %v, want a bad request saying the role takes no end", name, err)
		}
	}
	if len(store.memberships) != before {
		t.Errorf("the store holds %d memberships after the refusals, want %d", len(store.memberships), before)
	}
	if err := ValidatePeriod(accesstypes.SuperAdministrator, accesstypes.Period{}); err != nil {
		t.Errorf("ValidatePeriod() of the Super Administrator role with no period: error = %v", err)
	}
	if err := ValidatePeriod("Editor", accesstypes.Period{End: time.Now().Add(time.Hour)}); err != nil {
		t.Errorf("ValidatePeriod() of another role with an end: error = %v", err)
	}
	if err := manager.AssignUserRole(ctx, globalPolicy, "root", accesstypes.SuperAdministrator, accesstypes.Period{}, ""); err != nil {
		t.Errorf("AssignUserRole() of the Super Administrator role with no period: error = %v", err)
	}
}

// Test_userManager_groupPeriods pins the period of a place in a group and of a
// group's role: the person holds the role while both count, on the check and
// in the listings a sign-in reads; each is replaced by assigning again; and a
// group is deleted with its members and roles whatever their periods, one that
// has not begun among them.
func Test_userManager_groupPeriods(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, manager, store := periodClient(t)
	now := time.Now()
	ended := accesstypes.Period{End: now.Add(-time.Hour)}
	upcoming := accesstypes.Period{Start: now.Add(time.Hour)}
	night := mustAddGroup(t, manager, tenant1Policy, "Night shift")
	holdsEditor := func() bool {
		t.Helper()
		roles, err := manager.UserRoles(ctx, "bob")
		if err != nil {
			t.Fatalf("UserRoles() error = %v", err)
		}

		return cmp.Equal(roles[tenant1Policy], []accesstypes.Role{"Editor"})
	}
	assign := func(place, role accesstypes.Period) {
		t.Helper()
		if err := manager.AssignGroupUser(ctx, night, "bob", place); err != nil {
			t.Fatalf("AssignGroupUser() error = %v", err)
		}
		if err := manager.AssignGroupRole(ctx, night, tenant1Policy, "Editor", role); err != nil {
			t.Fatalf("AssignGroupRole() error = %v", err)
		}
	}

	tests := []struct {
		name        string
		place, role accesstypes.Period
		want        bool
		inAnHour    bool
	}{
		{name: "both without a period", want: true, inAnHour: true},
		{name: "the place has ended", place: ended},
		{name: "the group's role has ended", role: ended},
		{name: "the place has not begun", place: upcoming, inAnHour: true},
		{name: "the group's role has not begun", role: upcoming, inAnHour: true},
		{name: "both count now and end in an hour", place: accesstypes.Period{End: now.Add(time.Hour)}, role: accesstypes.Period{Start: now.Add(-time.Hour)}, want: true},
	}
	for _, tt := range tests {
		assign(tt.place, tt.role)
		if got := holdsEditor(); got != tt.want {
			t.Errorf("%s: UserRoles() holds Editor = %v, want %v", tt.name, got, tt.want)
		}
		if got := readsWidgets(t, client, "bob", now); got != tt.want {
			t.Errorf("%s: bob reads Widgets now = %v, want %v", tt.name, got, tt.want)
		}
		if got := readsWidgets(t, client, "bob", now.Add(time.Hour+time.Minute)); got != tt.inAnHour {
			t.Errorf("%s: bob reads Widgets in an hour = %v, want %v", tt.name, got, tt.inAnHour)
		}
		users, err := manager.GroupUsers(ctx, night)
		if err != nil {
			t.Fatalf("GroupUsers() error = %v", err)
		}
		if got, want := len(users) == 1, tt.place.Contains(now); got != want {
			t.Errorf("%s: GroupUsers() lists bob = %v, want %v", tt.name, got, want)
		}
		groups, err := manager.UserGroups(ctx, "bob")
		if err != nil {
			t.Fatalf("UserGroups() error = %v", err)
		}
		if got, want := len(groups) == 1, tt.place.Contains(now); got != want {
			t.Errorf("%s: UserGroups() lists the group = %v, want %v", tt.name, got, want)
		}
		roles, err := manager.GroupRoles(ctx, night)
		if err != nil {
			t.Fatalf("GroupRoles() error = %v", err)
		}
		if got, want := len(roles[tenant1Policy]) == 1, tt.role.Contains(now); got != want {
			t.Errorf("%s: GroupRoles() lists Editor = %v, want %v", tt.name, got, want)
		}
	}
	if len(store.groups.members) != 1 || len(store.groups.held) != 1 {
		t.Errorf("the store holds %d places and %d group roles, want one of each: assigning again replaces the period", len(store.groups.members), len(store.groups.held))
	}
	backwards := accesstypes.Period{Start: now.Add(time.Hour), End: now}
	if err := manager.AssignGroupUser(ctx, night, "bob", backwards); err == nil || !httpio.HasBadRequest(err) {
		t.Errorf("AssignGroupUser() with an end before its start: error = %v, want a bad request", err)
	}
	if err := manager.AssignGroupRole(ctx, night, tenant1Policy, "Editor", backwards); err == nil || !httpio.HasBadRequest(err) {
		t.Errorf("AssignGroupRole() with an end before its start: error = %v, want a bad request", err)
	}

	// A member whose period has not begun goes with the group, and so does a role
	// that has ended.
	assign(upcoming, ended)
	if err := manager.DeleteGroup(ctx, night); err != nil {
		t.Fatalf("DeleteGroup() of a group with a member who has not begun: error = %v", err)
	}
	if len(store.groups.members) != 0 || len(store.groups.held) != 0 {
		t.Errorf("the store holds %d places and %d group roles after the group went, want none", len(store.groups.members), len(store.groups.held))
	}
}

// Test_userManager_DeleteRole_periods pins a custom role's deletion against the
// assignments of it: one that has ended goes with the role, and one that has
// not begun refuses the deletion in words.
func Test_userManager_DeleteRole_periods(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, manager, store := periodClient(t)
	now := time.Now()

	if err := manager.AssignUserRole(ctx, tenant1Policy, "ann", "Editor", accesstypes.Period{Start: now.Add(time.Hour)}, ""); err != nil {
		t.Fatalf("AssignUserRole() error = %v", err)
	}
	if _, err := manager.DeleteRole(ctx, tenant1Policy, "Editor"); err == nil || !httpio.HasBadRequest(err) || !strings.Contains(err.Error(), "has not begun") {
		t.Errorf("DeleteRole() of a role assigned for a period that has not begun: error = %v, want a bad request saying so", err)
	}
	if err := manager.AssignUserRole(ctx, tenant1Policy, "ann", "Editor", accesstypes.Period{End: now.Add(-time.Hour)}, ""); err != nil {
		t.Fatalf("AssignUserRole() error = %v", err)
	}
	if deleted, err := manager.DeleteRole(ctx, tenant1Policy, "Editor"); err != nil || !deleted {
		t.Fatalf("DeleteRole() of a role whose only assignment has ended = (%v, %v), want it deleted", deleted, err)
	}
	if len(store.memberships) != 0 {
		t.Errorf("the store holds %d memberships after the role went, want none: the ended one goes with it", len(store.memberships))
	}
}

// Test_userManager_DeleteRole_heldByGroup pins that a role a group holds is not deleted
// from under it. A role deleted there would leave the group's row behind, and a role of
// the name made again would at once be held by everyone in the group. The group's row
// stands in the way while it counts or will, whether or not the group has anyone in it,
// and goes with the role once it has ended.
func Test_userManager_DeleteRole_heldByGroup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, manager, store := periodClient(t)
	now := time.Now()
	night := mustAddGroup(t, manager, tenant1Policy, "Night shift")

	for _, held := range []struct {
		name   string
		period accesstypes.Period
	}{
		{name: "with no period"},
		{name: "for a period that has not begun", period: accesstypes.Period{Start: now.Add(time.Hour)}},
	} {
		if err := manager.AssignGroupRole(ctx, night, tenant1Policy, "Editor", held.period); err != nil {
			t.Fatalf("AssignGroupRole() error = %v", err)
		}
		deleted, err := manager.DeleteRole(ctx, tenant1Policy, "Editor")
		if deleted || err == nil || !httpio.HasBadRequest(err) || httpio.Message(err) != "A group holds this role, now or for a period that has not begun yet. Take it away from the group first, then delete it." {
			t.Errorf("DeleteRole() of a role an empty group holds %s = (%v, %v), want a bad request saying a group holds it", held.name, deleted, err)
		}
		if exists, err := store.RoleExists(ctx, tenant1Policy, "Editor"); err != nil || !exists {
			t.Fatalf("Store.RoleExists() after the refusal = (%v, %v), want the role there", exists, err)
		}
	}

	if err := manager.AssignGroupRole(ctx, night, tenant1Policy, "Editor", accesstypes.Period{End: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("AssignGroupRole() error = %v", err)
	}
	if deleted, err := manager.DeleteRole(ctx, tenant1Policy, "Editor"); err != nil || !deleted {
		t.Fatalf("DeleteRole() of a role whose only holder is a group whose role has ended = (%v, %v), want it deleted", deleted, err)
	}
	if len(store.groups.held) != 0 {
		t.Errorf("the store holds %d group roles after the role went, want none: the ended one goes with it", len(store.groups.held))
	}

	// The name made again is held by nobody: no row of the group was left behind.
	if err := manager.AddRole(ctx, tenant1Policy, "Editor", aDescription); err != nil {
		t.Fatalf("AddRole() error = %v", err)
	}
	if err := manager.AddGroupUsers(ctx, night, "bob"); err != nil {
		t.Fatalf("AddGroupUsers() error = %v", err)
	}
	if roles, err := manager.UserRoles(ctx, "bob"); err != nil || len(roles) != 0 {
		t.Errorf("UserRoles(bob) once the role is made again = (%v, %v), want none: the group no longer holds it", roles, err)
	}
}

// Test_userManager_RoleHolders pins the list of who holds a role: the people who hold it
// by a membership of their own and the people who hold it through a group, each once,
// with how. Removing a person's own membership leaves what they hold through a group. A
// place or a group's role outside its period makes nobody a holder, and no group makes
// anybody a super administrator.
func Test_userManager_RoleHolders(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, manager, _ := periodClient(t)
	now := time.Now()
	night := mustAddGroup(t, manager, tenant1Policy, "Night shift")
	day := mustAddGroup(t, manager, tenant1Policy, "Day shift")
	nightInfo := GroupInfo{ID: night, Scope: tenant1Policy, Name: "Night shift", Description: "The people the tests put in Night shift."}
	dayInfo := GroupInfo{ID: day, Scope: tenant1Policy, Name: "Day shift", Description: "The people the tests put in Day shift."}

	if err := manager.AddUserRoles(ctx, tenant1Policy, "ann", "Editor"); err != nil {
		t.Fatalf("AddUserRoles() error = %v", err)
	}
	if err := manager.AddUserRoles(ctx, tenant1Policy, "bob", "Editor"); err != nil {
		t.Fatalf("AddUserRoles() error = %v", err)
	}
	for _, group := range []accesstypes.Group{night, day} {
		if err := manager.AddGroupRoles(ctx, group, tenant1Policy, "Editor"); err != nil {
			t.Fatalf("AddGroupRoles() error = %v", err)
		}
	}
	if err := manager.AddGroupUsers(ctx, night, "bob", "cat"); err != nil {
		t.Fatalf("AddGroupUsers() error = %v", err)
	}
	if err := manager.AddGroupUsers(ctx, day, "cat"); err != nil {
		t.Fatalf("AddGroupUsers() error = %v", err)
	}
	// dee's place has ended and eve's has not begun: neither holds the role now.
	if err := manager.AssignGroupUser(ctx, night, "dee", accesstypes.Period{End: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("AssignGroupUser() error = %v", err)
	}
	if err := manager.AssignGroupUser(ctx, night, "eve", accesstypes.Period{Start: now.Add(time.Hour)}); err != nil {
		t.Fatalf("AssignGroupUser() error = %v", err)
	}

	want := []RoleHolder{
		{User: "ann", Own: true},
		{User: "bob", Own: true, Groups: []GroupInfo{nightInfo}},
		{User: "cat", Groups: []GroupInfo{dayInfo, nightInfo}},
	}
	holders, err := manager.RoleHolders(ctx, tenant1Policy, "Editor")
	if err != nil {
		t.Fatalf("RoleHolders() error = %v", err)
	}
	if diff := cmp.Diff(want, holders, cmp.AllowUnexported(accesstypes.PolicyScope{})); diff != "" {
		t.Errorf("RoleHolders() (-want +got):\n%s", diff)
	}
	users, err := manager.RoleUsers(ctx, tenant1Policy, "Editor")
	if err != nil || !cmp.Equal(users, []accesstypes.User{"ann", "bob", "cat"}) {
		t.Errorf("RoleUsers() = (%v, %v), want ann, bob and cat, each once", users, err)
	}

	// bob's own membership goes, and bob holds the role still: through the group.
	if err := manager.DeleteUserRoles(ctx, tenant1Policy, "bob", "Editor"); err != nil {
		t.Fatalf("DeleteUserRoles() error = %v", err)
	}
	users, err = manager.RoleUsers(ctx, tenant1Policy, "Editor")
	if err != nil || !cmp.Equal(users, []accesstypes.User{"ann", "bob", "cat"}) {
		t.Errorf("RoleUsers() once bob's own membership is removed = (%v, %v), want bob listed still: he holds the role through the group", users, err)
	}

	// The night shift's role ends: its people hold the role through the day shift alone.
	if err := manager.AssignGroupRole(ctx, night, tenant1Policy, "Editor", accesstypes.Period{End: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("AssignGroupRole() error = %v", err)
	}
	want = []RoleHolder{{User: "ann", Own: true}, {User: "cat", Groups: []GroupInfo{dayInfo}}}
	holders, err = manager.RoleHolders(ctx, tenant1Policy, "Editor")
	if err != nil {
		t.Fatalf("RoleHolders() error = %v", err)
	}
	if diff := cmp.Diff(want, holders, cmp.AllowUnexported(accesstypes.PolicyScope{})); diff != "" {
		t.Errorf("RoleHolders() once the night shift's role has ended (-want +got):\n%s", diff)
	}
}
