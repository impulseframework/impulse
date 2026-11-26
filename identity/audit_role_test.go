package identity

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// danaCaller is the context of a request dana makes in a session of her own on an outlet
// of the auth, checked in the scope.
func danaCaller(scope accesstypes.Scope) context.Context {
	ctx := context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: recordSession, Username: "dana"},
		UserID:      impulse.NullUUIDFromUUID(userID),
	})
	ctx = resource.WithAuth(ctx, managedAuth)

	return resource.WithCaller(ctx, &resource.Caller{Permissions: callerPermissions{user: dana}, Scope: scope})
}

// readerRecord is the row of the role Reader given to or taken from dana in alpha, but
// who acted.
func readerRecord(act auditlog.Act) auditlog.Record {
	return auditlog.Record{
		Act: act, SubjectKind: auditlog.SubjectAccount, SubjectID: userID.String(), SubjectName: "dana",
		TenantID: "alpha", TenantName: "Alpha Sector", RoleNames: []string{"Reader"}, RoleScope: auditlog.RoleScopeTenant,
	}
}

// TestRoleActsRecorded pins the rows of a role given and taken: a role given with its
// period and its reason, in its tenant; one given again as it is, which writes nothing
// and records nothing; one whose period changed, with the new period; a role taken, and
// one taken that the person does not hold, which records nothing.
func TestRoleActsRecorded(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	week := accesstypes.Period{Start: start, End: start.Add(7 * 24 * time.Hour)}
	month := accesstypes.Period{Start: start, End: start.Add(30 * 24 * time.Hour)}
	why := "covering nights"
	alice := recordCaller(accesstypes.DomainScope("alpha"))

	given := readerRecord(auditlog.ActRoleGiven)
	given.Period, given.Reason = week, why
	changed := readerRecord(auditlog.ActRoleGiven)
	changed.Period = month

	tests := []struct {
		name string
		// held is the period and the reason of dana's Reader in alpha before the act,
		// where holds says she holds it.
		holds  bool
		held   accesstypes.Period
		reason string
		act    func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		want   []auditlog.Record
		// wantWhat is what a person reads of the row, where there is one.
		wantWhat string
	}{
		{
			name: "a role given with a period and a reason",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AssignRoleFor(ctx, txn, alphaScope, userID, "Reader", week, why)
			},
			want:     []auditlog.Record{given},
			wantWhat: "Gave the role Reader from 2026-12-01 09:00 UTC until 2026-12-08 09:00 UTC",
		},
		{
			name:  "a role given again as it is held",
			holds: true, held: week, reason: why,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AssignRoleFor(ctx, txn, alphaScope, userID, "Reader", week, why)
			},
		},
		{
			name:  "a role given again for another period",
			holds: true, held: week, reason: why,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AssignRoleFor(ctx, txn, alphaScope, userID, "Reader", month, "")
			},
			want:     []auditlog.Record{changed},
			wantWhat: "Gave the role Reader from 2026-12-01 09:00 UTC until 2026-12-31 09:00 UTC",
		},
		{
			name:  "a role taken",
			holds: true,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemoveRole(ctx, txn, alphaScope, userID, "Reader")
			},
			want:     []auditlog.Record{readerRecord(auditlog.ActRoleTaken)},
			wantWhat: "Took the role Reader",
		},
		{
			name: "a role taken that the person does not hold",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemoveRole(ctx, txn, alphaScope, userID, "Reader")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := groupStore()
			store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{}
			store.membershipReasons = map[access.MembershipKey]string{}
			if tt.holds {
				store.memberships[danaAlphaReader] = true
				store.membershipPeriods[danaAlphaReader] = tt.held
				store.membershipReasons[danaAlphaReader] = tt.reason
			}
			sessions := fakeSessions{tenantNames: map[accesstypes.Domain]string{"alpha": "Alpha Sector"}}
			m, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, sessions, WithTenantWord("sector"))
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.act(alice, txn, m)
			}); err != nil {
				t.Fatalf("%s error = %v", tt.name, err)
			}

			for i := range tt.want {
				tt.want[i].Source = eventsource.UserEvent(alice)
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
			if stored.Reason != tt.want[0].Reason {
				t.Errorf("the row keeps the reason %q, want %q", stored.Reason, tt.want[0].Reason)
			}
		})
	}
}

// TestRoleActsRecorded_removalFromTenant pins the rows of a person's removal from a
// tenant: one for each role taken there and one for each place in its groups, the group
// by its name, each in the tenant.
func TestRoleActsRecorded_removalFromTenant(t *testing.T) {
	t.Parallel()

	store := removalStore()
	for _, key := range []access.MembershipKey{danaAlphaReader, danaAlphaLead, danaBetaReader, erinAlphaWarden} {
		store.memberships[key] = true
	}
	store.groupMembers[memberKey(nightID, userID)], store.groupMembers[memberKey(dayID, userID)] = true, true
	m, _, _ := removalManager(t, store, &fakeGrants{}, false)
	if err := removeDana(t, m, asErinIn, "alpha"); err != nil {
		t.Fatalf("RemoveFromTenant() error = %v", err)
	}

	type row struct {
		act                 auditlog.Act
		role, group, tenant string
	}
	var got []row
	for _, r := range store.audit {
		if r.SubjectKind != auditlog.SubjectAccount || r.SubjectID != userID.String() {
			t.Errorf("a row is done to the %s %s, want dana's account", r.SubjectKind, r.SubjectID)
		}
		role := ""
		if len(r.RoleNames) == 1 {
			role = r.RoleNames[0]
		}
		got = append(got, row{act: r.Act, role: role, group: r.GroupName, tenant: r.TenantID})
	}
	want := []row{
		{act: auditlog.ActRoleTaken, role: "NightShiftLead", tenant: "alpha"},
		{act: auditlog.ActRoleTaken, role: "Reader", tenant: "alpha"},
		{act: auditlog.ActGroupMemberRemoved, group: "Night shift", tenant: "alpha"},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(row{})); diff != "" {
		t.Errorf("audit rows (-want +got):\n%s", diff)
	}
	if len(store.audit) == 3 {
		stored := storedOf(t, &store.audit[2])
		if what := stored.Sentences("sector").What; what != "Took out of the group Night shift" {
			t.Errorf("the place's row reads %q, want %q", what, "Took out of the group Night shift")
		}
	}
}

// TestRoleActsRecorded_takeRole pins the row of a role a person takes for a set time:
// given, by the person themselves, with the reason they gave.
func TestRoleActsRecorded_takeRole(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	store := groupStore()
	m, _ := newManager(t, store, engineRoles, WithRoleGrants(&fakeGrants{}))
	m.now = func() time.Time { return now }
	ctx := danaCaller(accesstypes.DomainScope("alpha"))
	if err := inTransaction(t, func(_ context.Context, txn resource.ReadWriteTransaction) error {
		return m.TakeRole(ctx, txn, alphaScope, "Reader", TakeOneHour, "covering the night desk")
	}); err != nil {
		t.Fatalf("TakeRole() error = %v", err)
	}

	want := readerRecord(auditlog.ActRoleGiven)
	want.TenantName = "alpha"
	want.Period, want.Reason = accesstypes.Period{Start: now, End: now.Add(time.Hour)}, "covering the night desk"
	want.Source = eventsource.UserEvent(ctx)
	if diff := cmp.Diff([]auditlog.Record{want}, store.audit, cmp.AllowUnexported(eventsource.EventSource{})); diff != "" {
		t.Fatalf("audit rows (-want +got):\n%s", diff)
	}
	stored := storedOf(t, &store.audit[0])
	if who := stored.Sentences("tenant").Who; who != "dana" {
		t.Errorf("the row reads as done by %q, want dana", who)
	}
}
