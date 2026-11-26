package identity

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
)

// TestGroupActsRecorded pins the row of each act on a group, its people and its roles: a
// group made, described, a person put in it and taken out, a role given to it and taken
// from it, each in the tenant the group is kept in, and in none for a group kept for the
// whole application; an act that changes nothing records nothing.
func TestGroupActsRecorded(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	week := accesstypes.Period{Start: start, End: start.Add(7 * 24 * time.Hour)}
	night := func(act auditlog.Act) auditlog.Record {
		return auditlog.Record{Act: act, SubjectKind: auditlog.SubjectGroup, SubjectID: nightID.String(), SubjectName: "Night shift", TenantID: "alpha", TenantName: "Alpha Sector"}
	}
	danaIn := func(act auditlog.Act) auditlog.Record {
		return auditlog.Record{
			Act: act, SubjectKind: auditlog.SubjectAccount, SubjectID: userID.String(), SubjectName: "dana",
			GroupID: nightID.String(), GroupName: "Night shift", TenantID: "alpha", TenantName: "Alpha Sector",
		}
	}
	added := danaIn(auditlog.ActGroupMemberAdded)
	added.Period = week
	roleGiven := night(auditlog.ActGroupRoleGiven)
	roleGiven.RoleNames, roleGiven.RoleScope, roleGiven.Period = []string{"Reader"}, auditlog.RoleScopeTenant, week
	roleTaken := night(auditlog.ActGroupRoleTaken)
	roleTaken.RoleNames, roleTaken.RoleScope = []string{"Reader"}, auditlog.RoleScopeTenant
	nightReader := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")

	tests := []struct {
		name    string
		prepare func(store *fakeStore)
		act     func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		want    []auditlog.Record
		// wantWhat is what a person reads of the first row.
		wantWhat string
	}{
		{
			name: "a group made in a tenant",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				_, err := m.CreateGroup(ctx, txn, alphaScope, "Night watch", "Who keeps the night")

				return err
			},
			want: []auditlog.Record{{
				Act: auditlog.ActGroupMade, SubjectKind: auditlog.SubjectGroup, SubjectID: newGroupID.String(), SubjectName: "Night watch",
				TenantID: "alpha", TenantName: "Alpha Sector",
			}},
			wantWhat: "Made the group",
		},
		{
			name: "a group made for the whole application, in no tenant",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				_, err := m.CreateGroup(ctx, txn, globalScope, "Auditors", "Who reads the books")

				return err
			},
			want:     []auditlog.Record{{Act: auditlog.ActGroupMade, SubjectKind: auditlog.SubjectGroup, SubjectID: newGroupID.String(), SubjectName: "Auditors"}},
			wantWhat: "Made the group",
		},
		{
			name: "a group described",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.DescribeGroup(ctx, txn, nightID, "Who keeps the night desk")
			},
			want:     []auditlog.Record{night(auditlog.ActGroupChanged)},
			wantWhat: "Changed the group's description",
		},
		{
			name: "a person put in a group for a period",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AddGroupMemberFor(ctx, txn, nightID, userID, week)
			},
			want:     []auditlog.Record{added},
			wantWhat: "Put in the group Night shift from 2026-12-01 09:00 UTC until 2026-12-08 09:00 UTC",
		},
		{
			name: "a person put in a group they are in, for the period they have",
			prepare: func(store *fakeStore) {
				store.groupMembers[memberKey(nightID, userID)] = true
				store.placePeriods[memberKey(nightID, userID)] = week
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AddGroupMemberFor(ctx, txn, nightID, userID, week)
			},
		},
		{
			name:    "a person taken out of a group",
			prepare: func(store *fakeStore) { store.groupMembers[memberKey(nightID, userID)] = true },
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemoveGroupMember(ctx, txn, nightID, userID)
			},
			want:     []auditlog.Record{danaIn(auditlog.ActGroupMemberRemoved)},
			wantWhat: "Took out of the group Night shift",
		},
		{
			name: "a person taken out of a group they are not in",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemoveGroupMember(ctx, txn, nightID, userID)
			},
		},
		{
			name: "a role given to a group for a period",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.GiveGroupRoleFor(ctx, txn, nightID, alphaScope, "Reader", week)
			},
			want:     []auditlog.Record{roleGiven},
			wantWhat: "Gave the group the role Reader from 2026-12-01 09:00 UTC until 2026-12-08 09:00 UTC",
		},
		{
			name: "a role given to a group that holds it, for the period it has",
			prepare: func(store *fakeStore) {
				store.groupRoles[nightReader] = true
				store.rolePeriods[nightReader] = week
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.GiveGroupRoleFor(ctx, txn, nightID, alphaScope, "Reader", week)
			},
		},
		{
			name: "a role given to a group kept for the whole application, in no tenant",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.GiveGroupRoleFor(ctx, txn, staffID, globalScope, "Steward", accesstypes.Period{})
			},
			want: []auditlog.Record{{
				Act: auditlog.ActGroupRoleGiven, SubjectKind: auditlog.SubjectGroup, SubjectID: staffID.String(), SubjectName: "Staff",
				RoleNames: []string{"Steward"}, RoleScope: auditlog.RoleScopeGlobal,
			}},
			wantWhat: "Gave the group the role Steward for the whole application",
		},
		{
			name:    "a role taken from a group",
			prepare: func(store *fakeStore) { store.groupRoles[nightReader] = true },
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.TakeGroupRole(ctx, txn, nightID, alphaScope, "Reader")
			},
			want:     []auditlog.Record{roleTaken},
			wantWhat: "Took the role Reader from the group",
		},
		{
			name: "a role taken from a group that does not hold it",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.TakeGroupRole(ctx, txn, nightID, alphaScope, "Reader")
			},
		},
		{
			// The places go with the account, which is the one row.
			name: "an account deleted with its places in groups",
			prepare: func(store *fakeStore) {
				store.groupMembers[memberKey(nightID, userID)] = true
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				_, err := m.DeleteUser(ctx, txn, userID, false)

				return err
			},
			want:     []auditlog.Record{{Act: auditlog.ActAccountDeleted, SubjectKind: auditlog.SubjectAccount, SubjectID: userID.String(), SubjectName: "dana"}},
			wantWhat: "Deleted the account",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := groupStore()
			store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{}
			store.rolePeriods = map[access.GroupRoleKey]accesstypes.Period{}
			if tt.prepare != nil {
				tt.prepare(store)
			}
			sessions := fakeSessions{tenantNames: map[accesstypes.Domain]string{"alpha": "Alpha Sector"}}
			m, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, sessions, WithTenantWord("sector"))
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.act(ctx, txn, m)
			}); err != nil {
				t.Fatalf("%s error = %v", tt.name, err)
			}

			for i := range tt.want {
				tt.want[i].Source = eventsource.ProcessEvent(auditlog.ProcessApplication)
			}
			if diff := cmp.Diff(tt.want, store.audit, cmp.AllowUnexported(eventsource.EventSource{})); diff != "" {
				t.Fatalf("audit rows (-want +got):\n%s", diff)
			}
			if len(tt.want) == 0 {
				return
			}
			stored := storedOf(t, &store.audit[0])
			if got := stored.Sentences("sector").What; got != tt.wantWhat {
				t.Errorf("the row reads %q, want %q", got, tt.wantWhat)
			}
		})
	}
}

// TestSettingActsRecorded pins the row of each change to the auth's sign-in settings:
// done to the setting by its fixed name, a sign-up setting named by its method's button
// words; and none where nothing is written.
func TestSettingActsRecorded(t *testing.T) {
	t.Parallel()

	setting := func(act auditlog.Act, id string) auditlog.Record {
		return auditlog.Record{Act: act, SubjectKind: auditlog.SubjectSetting, SubjectID: id}
	}
	opened := setting(auditlog.ActSignUpOpened, auditlog.SignUpSetting("google"))
	opened.SubjectName, opened.RoleNames = "Google", []string{"Steward"}
	makingTenants := setting(auditlog.ActSignUpOpened, auditlog.SignUpSetting("google"))
	makingTenants.SubjectName = "Google"
	closed := setting(auditlog.ActSignUpClosed, auditlog.SignUpSetting("google"))
	closed.SubjectName = "Google"
	remembered := setting(auditlog.ActRememberedBrowsersSet, auditlog.SettingRememberedBrowsers)
	remembered.NewValue = "30"

	tests := []struct {
		name    string
		prepare func(store *fakeStore)
		act     func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		want    []auditlog.Record
		// wantWhat is what a person reads of the row, and wantDoneTo what it reads the
		// act was done to.
		wantWhat, wantDoneTo string
	}{
		{
			name: "a method opened to sign-up",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.OpenSignUp(ctx, txn, &SignUp{Method: "google", DefaultRole: "Steward"})
			},
			want:     []auditlog.Record{opened},
			wantWhat: "Opened Google to sign-up with the role Steward", wantDoneTo: "Sign-up through Google",
		},
		{
			name: "a method opened to sign-up where each person makes a tenant",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.OpenSignUp(ctx, txn, &SignUp{Method: "google", CreatesTenant: true})
			},
			want:     []auditlog.Record{makingTenants},
			wantWhat: "Opened Google to sign-up, each person making a tenant of their own", wantDoneTo: "Sign-up through Google",
		},
		{
			name: "a method made invite only",
			prepare: func(store *fakeStore) {
				store.methodRows = map[string]SignInMethodRow{"google": {Name: "google", OpenSignUp: true}}
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.MakeInviteOnly(ctx, txn, "google")
			},
			want:     []auditlog.Record{closed},
			wantWhat: "Made Google invite only", wantDoneTo: "Sign-up through Google",
		},
		{
			name: "a method made invite only that is invite only",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.MakeInviteOnly(ctx, txn, "google")
			},
		},
		{
			name: "a second factor required",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RequireSecondFactor(ctx, txn)
			},
			want:     []auditlog.Record{setting(auditlog.ActSecondFactorRequired, auditlog.SettingSecondFactor)},
			wantWhat: "Required a second factor of everyone", wantDoneTo: "Second factor",
		},
		{
			name: "a second factor no longer required",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.StopRequiringSecondFactor(ctx, txn)
			},
			want:     []auditlog.Record{setting(auditlog.ActSecondFactorNotRequired, auditlog.SettingSecondFactor)},
			wantWhat: "Stopped requiring a second factor", wantDoneTo: "Second factor",
		},
		{
			name: "how long a browser is remembered",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.SetRememberedBrowsers(ctx, txn, "30 days")
			},
			want:     []auditlog.Record{remembered},
			wantWhat: "Set how long a browser is remembered to 30 days", wantDoneTo: "Remembered browsers",
		},
		{
			name: "people let to reset their own password",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.SetSelfServiceReset(ctx, txn, true)
			},
			want:     []auditlog.Record{setting(auditlog.ActSelfServiceResetOn, auditlog.SettingSelfServiceReset)},
			wantWhat: "Let people reset their own password at the sign-in page", wantDoneTo: "Password reset at the sign-in page",
		},
		{
			name: "people no longer let to reset their own password",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.SetSelfServiceReset(ctx, txn, false)
			},
			want:     []auditlog.Record{setting(auditlog.ActSelfServiceResetOff, auditlog.SettingSelfServiceReset)},
			wantWhat: "Stopped letting people reset their own password at the sign-in page", wantDoneTo: "Password reset at the sign-in page",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{}
			if tt.prepare != nil {
				tt.prepare(store)
			}
			m := newSignUpManager(t, store)
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.act(ctx, txn, m)
			}); err != nil {
				t.Fatalf("%s error = %v", tt.name, err)
			}

			for i := range tt.want {
				tt.want[i].Source = eventsource.ProcessEvent(auditlog.ProcessApplication)
			}
			if diff := cmp.Diff(tt.want, store.audit, cmp.AllowUnexported(eventsource.EventSource{})); diff != "" {
				t.Fatalf("audit rows (-want +got):\n%s", diff)
			}
			if len(tt.want) == 0 {
				return
			}
			stored := storedOf(t, &store.audit[0])
			reads := stored.Sentences("tenant")
			if reads.What != tt.wantWhat || reads.DoneTo != tt.wantDoneTo {
				t.Errorf("the row reads %q done to %q, want %q done to %q", reads.What, reads.DoneTo, tt.wantWhat, tt.wantDoneTo)
			}
		})
	}
}

// TestSettingActsRecorded_refused pins that a refused change to a sign-in setting
// records nothing: a length that is no length, and the reset of one's own password in an
// auth nobody signs in to with a password.
func TestSettingActsRecorded_refused(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	m := newSignUpManager(t, store)
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.SetRememberedBrowsers(ctx, txn, "a fortnight")
	}); err == nil {
		t.Fatal("SetRememberedBrowsers(a fortnight) error = nil, want the refusal")
	}
	directory, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, fakeSessions{methods: registeredMethods()[1:]})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return directory.SetSelfServiceReset(ctx, txn, true)
	}); err == nil {
		t.Fatal("SetSelfServiceReset() error = nil, want the refusal: nobody signs in with a password")
	}
	if len(store.audit) != 0 {
		t.Errorf("audit rows = %d (%s first), want none", len(store.audit), store.audit[0].Act)
	}
}

// TestRememberLengthsAreTheLogsWords holds the names of the lengths a browser can be
// remembered for, as the list an administrator picks from shows them, to the words the
// audit log writes for each length's days: the two say a length the same way.
func TestRememberLengthsAreTheLogsWords(t *testing.T) {
	t.Parallel()

	for _, length := range RememberLengths() {
		var row auditlog.Stored
		row.Act, row.SubjectKind, row.SubjectID = auditlog.ActRememberedBrowsersSet, auditlog.SubjectSetting, auditlog.SettingRememberedBrowsers
		row.NewValue = strconv.FormatInt(length.Days, 10)
		if got, want := row.Sentences("tenant").What, "Set how long a browser is remembered to "+length.Name; got != want {
			t.Errorf("the log reads %d days as %q, want %q", length.Days, got, want)
		}
	}
}

// TestDeleteGroup_recorded pins the row of a group's delete in two saves: none in the
// save that leaves the group standing, and one in the save that deletes it, done to the
// group by its name, in the tenant it is kept in. Its members and its roles have no row of
// their own.
func TestDeleteGroup_recorded(t *testing.T) {
	t.Parallel()

	store := groupStore()
	store.groupMembers[memberKey(nightID, userID)] = true
	store.groupRoles[access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")] = true
	store.groupLeft = &resource.DeleteLeft{Deleted: 2, Left: 1}
	sessions := fakeSessions{tenantNames: map[accesstypes.Domain]string{"alpha": "Alpha Sector"}}
	m, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, sessions, WithTenantWord("sector"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	round := func(t *testing.T) *resource.DeleteLeft {
		t.Helper()

		var left *resource.DeleteLeft
		if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			var err error
			left, err = m.DeleteGroup(ctx, txn, nightID, true)

			return err
		}); err != nil {
			t.Fatalf("DeleteGroup() error = %v", err)
		}

		return left
	}

	if left := round(t); left == nil {
		t.Fatal("the first save's DeleteGroup() left nothing, want what the fake left")
	}
	if len(store.audit) != 0 {
		t.Fatalf("the save that left the group standing wrote %d audit rows (%s first), want none", len(store.audit), store.audit[0].Act)
	}
	store.groupLeft = nil
	if left := round(t); left != nil {
		t.Fatalf("the second save's DeleteGroup() left %+v, want the group gone", left)
	}
	want := []auditlog.Record{{
		Act: auditlog.ActGroupRemoved, Source: eventsource.ProcessEvent(auditlog.ProcessApplication), SubjectKind: auditlog.SubjectGroup,
		SubjectID: nightID.String(), SubjectName: "Night shift", TenantID: "alpha", TenantName: "Alpha Sector",
	}}
	if diff := cmp.Diff(want, store.audit, cmp.AllowUnexported(eventsource.EventSource{})); diff != "" {
		t.Fatalf("audit rows (-want +got):\n%s", diff)
	}
	stored := storedOf(t, &store.audit[0])
	if got := stored.Sentences("sector").What; got != "Removed the group" {
		t.Errorf("the row reads %q, want Removed the group", got)
	}
}
