package access

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// TestValidateReason pins the shape of the reason a person's role carries: one line of
// plain text, kept less the space around it, with nothing as no reason.
func TestValidateReason(t *testing.T) {
	t.Parallel()

	kept := []struct {
		name   string
		reason string
		want   string
	}{
		{name: "none", reason: "", want: ""},
		{name: "spaces alone are none", reason: "   ", want: ""},
		{name: "kept less the space around it", reason: "  Covering the audit ", want: "Covering the audit"},
		{name: "as long as a reason may be", reason: strings.Repeat("é", MaxReason), want: strings.Repeat("é", MaxReason)},
	}
	for _, tt := range kept {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ValidateReason(tt.reason)
			if err != nil || got != tt.want {
				t.Errorf("ValidateReason() = (%q, %v), want %q", got, err, tt.want)
			}
		})
	}

	refused := []struct {
		name   string
		reason string
		want   string
	}{
		{name: "two lines", reason: "Covering the audit\nand the stores", want: "one line of plain text"},
		{name: "a tab", reason: "Covering\tthe audit", want: "one line of plain text"},
		{name: "too long", reason: strings.Repeat("r", MaxReason+1), want: "longer than 500 characters"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := ValidateReason(tt.reason)
			if !httpio.HasBadRequest(err) || !strings.Contains(httpio.Message(err), tt.want) {
				t.Errorf("ValidateReason() error = %v, want a Bad Request saying %q", err, tt.want)
			}
		})
	}
}

// Test_userManager_AssignUserRole_reason pins the reason a person's role carries: it is
// kept with the membership and listed with it, giving the role again replaces it with
// the period, a role given with none carries none, and a reason of two lines is refused
// with nothing written.
func Test_userManager_AssignUserRole_reason(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, manager, store := periodClient(t)
	now := time.Now()
	hour := accesstypes.Period{End: now.Add(time.Hour)}
	day := accesstypes.Period{End: now.Add(24 * time.Hour)}

	held := func(t *testing.T, want ...HeldRole) {
		t.Helper()
		got, err := manager.UserRolesHeld(ctx, "ann")
		if err != nil {
			t.Fatalf("UserRolesHeld() error = %v", err)
		}
		if diff := cmp.Diff(want, got, cmp.AllowUnexported(accesstypes.PolicyScope{})); diff != "" {
			t.Errorf("UserRolesHeld() (-want +got):\n%s", diff)
		}
	}

	if err := manager.AssignUserRole(ctx, tenant1Policy, "ann", "Editor", hour, "  Covering the audit "); err != nil {
		t.Fatalf("AssignUserRole() error = %v", err)
	}
	held(t, HeldRole{Scope: tenant1Policy, Role: "Editor", Period: hour, Reason: "Covering the audit"})

	// Given again: the new time and the new reason take the old ones' place.
	if err := manager.AssignUserRole(ctx, tenant1Policy, "ann", "Editor", day, "The audit runs on"); err != nil {
		t.Fatalf("AssignUserRole() again error = %v", err)
	}
	held(t, HeldRole{Scope: tenant1Policy, Role: "Editor", Period: day, Reason: "The audit runs on"})

	// A reason that is not one line is refused, and the membership stays as it was.
	writes := store.writes
	err := manager.AssignUserRole(ctx, tenant1Policy, "ann", "Editor", day, "The audit\nruns on")
	if !httpio.HasBadRequest(err) || !strings.Contains(httpio.Message(err), "one line of plain text") {
		t.Errorf("AssignUserRole() with a reason of two lines: error = %v, want a Bad Request saying it is one line", err)
	}
	if store.writes != writes {
		t.Errorf("AssignUserRole() with a refused reason wrote %d row(s), want none", store.writes-writes)
	}
	held(t, HeldRole{Scope: tenant1Policy, Role: "Editor", Period: day, Reason: "The audit runs on"})

	// Given the ordinary way, with no period and no reason: both go.
	if err := manager.AddUserRoles(ctx, tenant1Policy, "ann", "Editor"); err != nil {
		t.Fatalf("AddUserRoles() error = %v", err)
	}
	held(t, HeldRole{Scope: tenant1Policy, Role: "Editor"})
}

// Test_userManager_UserRolesHeld pins the one list of a person's roles: a role of their
// own, with its period and its reason, and a role through each group that gives it,
// named by the group; a role held both ways is listed once for each; an own role whose
// period has not begun is listed and one that has ended is not; and what a group gives
// nobody is not listed.
func Test_userManager_UserRolesHeld(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, manager, store := periodClient(t)
	now := time.Now()
	night := mustAddGroup(t, manager, tenant1Policy, "Night shift")
	staff := mustAddGroup(t, manager, globalPolicy, "Staff")
	gone := mustAddGroup(t, manager, tenant1Policy, "Gone")
	later := mustAddGroup(t, manager, tenant1Policy, "Later")
	nightInfo := GroupInfo{ID: night, Scope: tenant1Policy, Name: "Night shift", Description: "The people the tests put in Night shift."}
	staffInfo := GroupInfo{ID: staff, Scope: globalPolicy, Name: "Staff", Description: "The people the tests put in Staff."}
	upcoming := accesstypes.Period{Start: now.Add(time.Hour)}

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("arranging the roles: %v", err)
		}
	}
	// ann's own: Editor in tenant1 with a reason, Reader there from an hour on, and
	// Steward for the whole application, which has ended.
	must(manager.AssignUserRole(ctx, tenant1Policy, "ann", "Editor", accesstypes.Period{}, "Runs the desk"))
	must(manager.AssignUserRole(ctx, tenant1Policy, "ann", "Reader", upcoming, ""))
	must(manager.AssignUserRole(ctx, globalPolicy, "ann", "Steward", accesstypes.Period{End: now.Add(-time.Hour)}, "Was the steward"))
	// Through groups: the night shift gives Editor too, the staff give Reader in every
	// tenant; the group she has left, in effect, and the one that does not hold its
	// role yet give nothing.
	must(manager.AddGroupRoles(ctx, night, tenant1Policy, "Editor"))
	must(manager.AddGroupRoles(ctx, staff, everyDomainPolicy, "Reader"))
	must(manager.AddGroupRoles(ctx, gone, tenant1Policy, "Reader"))
	must(manager.AssignGroupRole(ctx, later, tenant1Policy, "Reader", upcoming))
	must(manager.AddGroupUsers(ctx, night, "ann"))
	must(manager.AddGroupUsers(ctx, staff, "ann"))
	must(manager.AssignGroupUser(ctx, gone, "ann", accesstypes.Period{End: now.Add(-time.Hour)}))
	must(manager.AddGroupUsers(ctx, later, "ann"))
	// Two rows only a write past the manager leaves: the night shift, kept in tenant1,
	// holding a role in another tenant, and the staff holding the Super Administrator
	// role. A group gives neither to the people in it, so neither is listed.
	must(store.InsertGroupRole(ctx, night, tenant2Policy, "Reader", accesstypes.Period{}))
	must(store.InsertGroupRole(ctx, staff, globalPolicy, accesstypes.SuperAdministrator, accesstypes.Period{}))

	want := []HeldRole{
		{Scope: tenant1Policy, Role: "Editor", Reason: "Runs the desk"},
		{Scope: tenant1Policy, Role: "Editor", Group: &nightInfo},
		{Scope: tenant1Policy, Role: "Reader", Period: upcoming},
		{Scope: everyDomainPolicy, Role: "Reader", Group: &staffInfo},
	}
	got, err := manager.UserRolesHeld(ctx, "ann")
	if err != nil {
		t.Fatalf("UserRolesHeld() error = %v", err)
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(accesstypes.PolicyScope{})); diff != "" {
		t.Errorf("UserRolesHeld() (-want +got):\n%s", diff)
	}
	if got, err := manager.UserRolesHeld(ctx, "nobody"); err != nil || len(got) != 0 {
		t.Errorf("UserRolesHeld() of a person who holds nothing = (%v, %v), want none", got, err)
	}
}

// Test_userManager_GroupContents pins what a list of groups says of each: how many
// people are in it and which roles it holds, counting what has not ended (a place or a
// role whose period has not begun among it) and nothing that has.
func Test_userManager_GroupContents(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, manager, _ := periodClient(t)
	now := time.Now()
	ended := accesstypes.Period{End: now.Add(-time.Hour)}
	upcoming := accesstypes.Period{Start: now.Add(time.Hour)}
	day := mustAddGroup(t, manager, tenant1Policy, "Day shift")
	night := mustAddGroup(t, manager, tenant1Policy, "Night shift")
	spare := mustAddGroup(t, manager, tenant1Policy, "Spare")
	staff := mustAddGroup(t, manager, globalPolicy, "Staff")
	info := func(id accesstypes.Group, scope accesstypes.PolicyScope, name string) GroupInfo {
		return GroupInfo{ID: id, Scope: scope, Name: name, Description: "The people the tests put in " + name + "."}
	}

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("arranging the groups: %v", err)
		}
	}
	// The day shift: two people now, one from an hour on, one whose place has ended; it
	// holds Editor, and Reader from an hour on.
	must(manager.AddGroupUsers(ctx, day, "ann", "bob"))
	must(manager.AssignGroupUser(ctx, day, "cat", upcoming))
	must(manager.AssignGroupUser(ctx, day, "dee", ended))
	must(manager.AddGroupRoles(ctx, day, tenant1Policy, "Editor"))
	must(manager.AssignGroupRole(ctx, day, tenant1Policy, "Reader", upcoming))
	// The night shift: everything it had has ended.
	must(manager.AssignGroupUser(ctx, night, "eve", ended))
	must(manager.AssignGroupRole(ctx, night, tenant1Policy, "Editor", ended))
	// The staff, kept for the whole application, hold a role in every tenant.
	must(manager.AddGroupUsers(ctx, staff, "ann"))
	must(manager.AddGroupRoles(ctx, staff, everyDomainPolicy, "Reader"))

	want := []GroupContent{
		{GroupInfo: info(day, tenant1Policy, "Day shift"), People: 3, Roles: []GroupHolding{{Scope: tenant1Policy, Role: "Editor"}, {Scope: tenant1Policy, Role: "Reader", Period: upcoming}}},
		{GroupInfo: info(night, tenant1Policy, "Night shift")},
		{GroupInfo: info(spare, tenant1Policy, "Spare")},
	}
	got, err := manager.GroupContents(ctx, tenant1Policy)
	if err != nil {
		t.Fatalf("GroupContents() error = %v", err)
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(accesstypes.PolicyScope{})); diff != "" {
		t.Errorf("GroupContents() of the tenant (-want +got):\n%s", diff)
	}
	wantGlobal := []GroupContent{{GroupInfo: info(staff, globalPolicy, "Staff"), People: 1, Roles: []GroupHolding{{Scope: everyDomainPolicy, Role: "Reader"}}}}
	got, err = manager.GroupContents(ctx, globalPolicy)
	if err != nil {
		t.Fatalf("GroupContents() error = %v", err)
	}
	if diff := cmp.Diff(wantGlobal, got, cmp.AllowUnexported(accesstypes.PolicyScope{})); diff != "" {
		t.Errorf("GroupContents() of the whole application (-want +got):\n%s", diff)
	}
}

// Test_userManager_GroupRoleLosers pins who loses a role a group stops holding: the
// people in the group now who hold the role there through it and in no other way. A
// person who also holds it themselves, through another group, or in every tenant keeps
// it; a person whose place has not begun or has ended holds nothing to lose; and while
// the group's role does not count nobody loses anything.
func Test_userManager_GroupRoleLosers(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, manager, _ := periodClient(t)
	now := time.Now()
	ended := accesstypes.Period{End: now.Add(-time.Hour)}
	upcoming := accesstypes.Period{Start: now.Add(time.Hour)}
	night := mustAddGroup(t, manager, tenant1Policy, "Night shift")
	day := mustAddGroup(t, manager, tenant1Policy, "Day shift")
	staff := mustAddGroup(t, manager, globalPolicy, "Staff")

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("arranging the roles: %v", err)
		}
	}
	must(manager.AddGroupRoles(ctx, night, tenant1Policy, "Reader"))
	must(manager.AddGroupRoles(ctx, day, tenant1Policy, "Reader"))
	must(manager.AddGroupRoles(ctx, staff, everyDomainPolicy, "Reader"))
	// ann and gus hold it through the night shift alone. bob holds it themselves too, cat
	// through the day shift too, dee in every tenant themselves and eve in every tenant
	// through the staff. fay's place has not begun and hal's has ended. ivy's own
	// membership has ended, and jon's has not begun: neither holds it another way now.
	must(manager.AddGroupUsers(ctx, night, "ann", "bob", "cat", "dee", "eve", "gus", "ivy", "jon"))
	must(manager.AssignGroupUser(ctx, night, "fay", upcoming))
	must(manager.AssignGroupUser(ctx, night, "hal", ended))
	must(manager.AddUserRoles(ctx, tenant1Policy, "bob", "Reader"))
	must(manager.AddGroupUsers(ctx, day, "cat"))
	must(manager.AddUserRoles(ctx, everyDomainPolicy, "dee", "Reader"))
	must(manager.AddGroupUsers(ctx, staff, "eve"))
	must(manager.AssignUserRole(ctx, tenant1Policy, "ivy", "Reader", ended, ""))
	must(manager.AssignUserRole(ctx, tenant1Policy, "jon", "Reader", upcoming, ""))

	losers := func(t *testing.T, group accesstypes.Group, scope accesstypes.PolicyScope, want ...accesstypes.User) {
		t.Helper()
		got, err := manager.GroupRoleLosers(ctx, group, scope, "Reader")
		if err != nil {
			t.Fatalf("GroupRoleLosers() error = %v", err)
		}
		if len(want) == 0 {
			want = []accesstypes.User{}
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GroupRoleLosers() (-want +got):\n%s", diff)
		}
	}
	losers(t, night, tenant1Policy, "ann", "gus", "ivy", "jon")
	// The staff's role in every tenant: eve holds it there through the staff alone.
	losers(t, staff, everyDomainPolicy, "eve")
	// A place a group does not hold the role in loses nobody anything.
	losers(t, night, globalPolicy)

	// The night shift's role has not begun: nobody holds it through the group yet.
	must(manager.AssignGroupRole(ctx, night, tenant1Policy, "Reader", upcoming))
	losers(t, night, tenant1Policy)
	// It has ended: the same.
	must(manager.AssignGroupRole(ctx, night, tenant1Policy, "Reader", ended))
	losers(t, night, tenant1Policy)
}
