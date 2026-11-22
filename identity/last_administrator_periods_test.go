package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// TestManager_ownPeriod_lastAdministrator pins the rule on a tenant's last administrator
// over the dates of a person's own holding. A start moved ahead and an end brought
// nearer take the role away as its removal does, so the tenant's last administrator is
// refused them in the words the removal is refused in: on a membership of their own, on
// their place in the group that gives them the role, and on the role of a group they
// are in. The change goes through once another person administers the tenant, for a
// role that is no administrator's, for a holding that does not count today, and where
// somebody else makes it.
func TestManager_ownPeriod_lastAdministrator(t *testing.T) {
	t.Parallel()

	_, upcoming, current := periodFixtures()
	var none accesstypes.Period
	sooner := accesstypes.Period{End: current.End.Add(-30 * time.Minute)}
	laterStart := accesstypes.Period{Start: upcoming.Start.Add(30 * time.Minute), End: upcoming.End}
	const (
		removing = "you are the last administrator of this tenant: give another person the role before you remove your own"
		leaving  = "you are the last administrator of this tenant: give another person the role before you leave the group that gives you yours"
		taking   = "you are the last administrator of this tenant: give another person the role before you take it from the group that gives you yours"
	)
	own := access.NewMembershipKey(alphaScope, dana, "Reader")
	ownWriter := access.NewMembershipKey(alphaScope, dana, "Writer")
	others := access.NewMembershipKey(alphaScope, erin, "Reader")
	place := memberKey(nightID, userID)
	groupRole := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")
	asDana := func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) }
	asErin := func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, erin) }
	role := func(name accesstypes.Role, period accesstypes.Period) func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.AssignRoleFor(ctx, txn, alphaScope, userID, name, period, "")
		}
	}
	taken := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.TakeRole(ctx, txn, alphaScope, "Reader", TakeOneHour, "the night's incident")
	}
	inGroup := func(period accesstypes.Period) func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.AddGroupMemberFor(ctx, txn, nightID, userID, period)
		}
	}
	ofGroup := func(period accesstypes.Period) func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.GiveGroupRoleFor(ctx, txn, nightID, alphaScope, "Reader", period)
		}
	}
	periodOf := func(key *access.MembershipKey, period accesstypes.Period) string {
		return "period of membership " + membershipText(key) + ": " + periodText(period)
	}
	placePeriod := func(period accesstypes.Period) string {
		return "period of group member " + string(place.Group) + " " + string(place.User) + ": " + periodText(period)
	}
	groupRolePeriod := func(period accesstypes.Period) string {
		return "period of group role " + groupRoleText(&groupRole) + ": " + periodText(period)
	}

	tests := []struct {
		name string
		// held are the memberships that stand, each with the period it has; through says
		// dana holds the administrator role through the night shift and by no membership
		// of her own, with placed the period of her place there and groupHeld the period
		// of the group's role.
		held      map[access.MembershipKey]accesstypes.Period
		through   bool
		placed    accesstypes.Period
		groupHeld accesstypes.Period
		ctx       func(context.Context) context.Context
		act       func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		wantSays  string
		// wantWrite is the one write of a change that goes through.
		wantWrite string
	}{
		{
			name: "the last administrator's own role, with a start that has not come",
			held: map[access.MembershipKey]accesstypes.Period{own: none}, ctx: asDana, act: role("Reader", upcoming), wantSays: removing,
		},
		{
			name: "the last administrator's own role, with an end where it had none",
			held: map[access.MembershipKey]accesstypes.Period{own: none}, ctx: asDana, act: role("Reader", sooner), wantSays: removing,
		},
		{
			name: "the last administrator's own role, with an end that comes sooner",
			held: map[access.MembershipKey]accesstypes.Period{own: current}, ctx: asDana, act: role("Reader", sooner), wantSays: removing,
		},
		{
			name: "the last administrator's own role, taken for an hour over the one that had no end",
			held: map[access.MembershipKey]accesstypes.Period{own: none}, ctx: asDana, act: taken, wantSays: removing,
		},
		{
			name: "an administrator's own role, with an end, once another person administers the tenant",
			held: map[access.MembershipKey]accesstypes.Period{own: none, others: none}, ctx: asDana, act: role("Reader", sooner), wantWrite: periodOf(&own, sooner),
		},
		{
			name: "the other administrator's role has not begun, so it stands in for nobody's",
			held: map[access.MembershipKey]accesstypes.Period{own: none, others: upcoming}, ctx: asDana, act: role("Reader", sooner), wantSays: removing,
		},
		{
			name: "a role of their own that is no administrator's, with an end",
			held: map[access.MembershipKey]accesstypes.Period{own: none, ownWriter: none}, ctx: asDana, act: role("Writer", sooner), wantWrite: periodOf(&ownWriter, sooner),
		},
		{
			name: "an administrator role of their own that has not begun administers nothing today",
			held: map[access.MembershipKey]accesstypes.Period{own: upcoming}, ctx: asDana, act: role("Reader", laterStart), wantWrite: periodOf(&own, laterStart),
		},
		{
			name: "somebody else gives the last administrator's role an end",
			held: map[access.MembershipKey]accesstypes.Period{own: none}, ctx: asErin, act: role("Reader", sooner), wantWrite: periodOf(&own, sooner),
		},

		{
			name:    "the last administrator's place in the group that gives the role, with a start that has not come",
			through: true, ctx: asDana, act: inGroup(upcoming), wantSays: leaving,
		},
		{
			name:    "the last administrator's place in the group that gives the role, with an end",
			through: true, ctx: asDana, act: inGroup(sooner), wantSays: leaving,
		},
		{
			name:    "their place in the group, with an end, once another person administers the tenant",
			through: true, held: map[access.MembershipKey]accesstypes.Period{others: none}, ctx: asDana, act: inGroup(sooner), wantWrite: placePeriod(sooner),
		},
		{
			name:    "their place in a group whose role has not begun gives them nothing today",
			through: true, groupHeld: upcoming, ctx: asDana, act: inGroup(sooner), wantWrite: placePeriod(sooner),
		},

		{
			name:    "the role of the group that makes them the last administrator, with a start that has not come",
			through: true, ctx: asDana, act: ofGroup(upcoming), wantSays: taking,
		},
		{
			name:    "the role of the group that makes them the last administrator, with an end",
			through: true, ctx: asDana, act: ofGroup(sooner), wantSays: taking,
		},
		{
			name:    "the role of the group they are in, with an end, once another person administers the tenant",
			through: true, held: map[access.MembershipKey]accesstypes.Period{others: none}, ctx: asDana, act: ofGroup(sooner), wantWrite: groupRolePeriod(sooner),
		},
		{
			name:    "somebody who is not in the group gives its role an end",
			through: true, ctx: asErin, act: ofGroup(sooner), wantWrite: groupRolePeriod(sooner),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{}
			for key, period := range tt.held {
				store.memberships[key] = true
				store.membershipPeriods[key] = period
			}
			if tt.through {
				store.groupMembers[place] = true
				store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{place: tt.placed}
				store.groupRoles[groupRole] = true
				store.rolePeriods = map[access.GroupRoleKey]accesstypes.Period{groupRole: tt.groupHeld}
			}
			m, _ := newManager(t, store, tenantRoles, WithAdministratorRoles([]accesstypes.Role{"Reader"}), WithRoleGrants(&fakeGrants{}))

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.act(tt.ctx(ctx), txn, m)
			})
			if tt.wantSays != "" {
				if err == nil || !httpio.HasForbidden(err) || !strings.Contains(err.Error(), tt.wantSays) {
					t.Fatalf("error = %v, want Forbidden saying %q", err, tt.wantSays)
				}
				if len(store.writes) != 0 {
					t.Errorf("buffered writes = %v, want none", store.writes)
				}

				return
			}
			if err != nil {
				t.Fatalf("error = %v, want the period given", err)
			}
			if diff := cmp.Diff([]string{tt.wantWrite}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_RemoveRole_holdingNotInForce pins that the rule on a tenant's last
// administrator counts the holdings in force today and nothing else: a person removes
// their own administrator role that has ended, or has not begun, though nobody else
// administers the tenant, since the role makes them no administrator today and its
// removal takes nothing from the tenant. An application that names no administrator
// roles lets a person remove their own ended role too.
func TestManager_RemoveRole_holdingNotInForce(t *testing.T) {
	t.Parallel()

	ended, upcoming, _ := periodFixtures()
	own := access.NewMembershipKey(alphaScope, dana, "Reader")
	steward := access.NewMembershipKey(globalScope, dana, "Steward")
	named := WithAdministratorRoles([]accesstypes.Role{"Reader"})

	for name, tt := range map[string]struct {
		opts []Option
		had  accesstypes.Period
	}{
		"an administrator role of their own that has ended":                          {opts: []Option{named}, had: ended},
		"an administrator role of their own that has not begun":                      {opts: []Option{named}, had: upcoming},
		"a role of their own that has ended, where no administrator roles are named": {had: ended},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			store.memberships[own], store.memberships[steward] = true, true
			store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{own: tt.had}
			m, _ := newManager(t, store, engineRoles, tt.opts...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveRole(asCaller(ctx, dana), txn, alphaScope, userID, "Reader")
			})
			if err != nil {
				t.Fatalf("RemoveRole() error = %v, want the role removed", err)
			}
			if diff := cmp.Diff([]string{"delete membership " + membershipText(&own)}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}
