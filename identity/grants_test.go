package identity

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// fakeGrants answers the role file's grants on the methods that give a role and take one
// away as a test scripts them: the caller gives and takes every role but the ones named.
// It records what it was asked.
type fakeGrants struct {
	noGive []accesstypes.Role
	noTake []accesstypes.Role
	err    error
	asked  []string
}

func (g *fakeGrants) MayGive(_ context.Context, _ resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role) (bool, error) {
	key := access.NewMembershipKey(scope, User(userID), role)
	g.asked = append(g.asked, "give "+membershipText(&key))

	return !slices.Contains(g.noGive, role), g.err
}

func (g *fakeGrants) MayTake(_ context.Context, _ resource.ReadWriteTransaction, scope accesstypes.PolicyScope, userID impulse.UUID, role accesstypes.Role) (bool, error) {
	key := access.NewMembershipKey(scope, User(userID), role)
	g.asked = append(g.asked, "take "+membershipText(&key))

	return !slices.Contains(g.noTake, role), g.err
}

// wardenRoles are the roles the fake engine holds for the tests of a group's changes:
// engineRoles with Warden, a role of alpha the tests' caller may neither give nor take.
var wardenRoles = map[accesstypes.PolicyScope][]accesstypes.Role{
	globalScope: {"Steward"},
	alphaScope:  {"Reader", "Warden"},
	everyScope:  {"Reader"},
}

// TestManager_groupChangesAreJudgedAsRoleChanges pins that a group gives nobody a power
// that giving the role directly would not: each change to who is in a group, or to what
// a group holds, is held to what the role changes it makes need of the caller. A person
// is put in a group, or taken out, by someone who may give, or take, each role the group
// holds; a group is given a role, or loses one, by someone who may give it to, or take
// it from, each person in it. A role or a place whose period has ended gives nobody
// anything and is not judged; a call outside any request is the application's own act;
// and a manager that cannot ask refuses the request, never passes it.
func TestManager_groupChangesAreJudgedAsRoleChanges(t *testing.T) {
	t.Parallel()

	ended, upcoming, _ := periodFixtures()
	warden := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Warden")
	reader := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")
	frank, danaID := frankID.String(), userID.String()
	add := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.AddGroupMember(ctx, txn, nightID, frankID)
	}
	remove := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.RemoveGroupMember(ctx, txn, nightID, userID)
	}
	give := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.GiveGroupRole(ctx, txn, nightID, alphaScope, "Warden")
	}
	take := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.TakeGroupRole(ctx, txn, nightID, alphaScope, "Warden")
	}

	tests := []struct {
		name string
		// roles are what the night shift holds, and members who is in it, each with its
		// period.
		roles   map[access.GroupRoleKey]accesstypes.Period
		members map[impulse.UUID]accesstypes.Period
		// outside says the call is made outside any request.
		outside  bool
		act      func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		wantSays string
		// wantAsked is what the grants were asked, and wantWrites what was buffered.
		wantAsked  []string
		wantWrites []string
	}{
		{
			name:  "a person is not put in a group that holds a role the caller may not give",
			roles: map[access.GroupRoleKey]accesstypes.Period{warden: {}, reader: {}}, act: add,
			wantSays:  "putting a person in the group Night shift gives them the role Warden, which you may not give them",
			wantAsked: []string{"give domain/alpha " + frank + " Reader", "give domain/alpha " + frank + " Warden"},
		},
		{
			name:  "a person is put in a group that holds only roles the caller may give",
			roles: map[access.GroupRoleKey]accesstypes.Period{reader: {}}, act: add,
			wantAsked:  []string{"give domain/alpha " + frank + " Reader"},
			wantWrites: []string{"insert group member " + nightID.String() + " " + frank},
		},
		{
			name:  "a role of the group that has ended gives nobody anything and is not judged",
			roles: map[access.GroupRoleKey]accesstypes.Period{warden: ended}, act: add,
			wantWrites: []string{"insert group member " + nightID.String() + " " + frank},
		},
		{
			name:  "a role of the group that has not begun is one the person will hold, and is judged",
			roles: map[access.GroupRoleKey]accesstypes.Period{warden: upcoming}, act: add,
			wantSays:  "the role Warden, which you may not give them",
			wantAsked: []string{"give domain/alpha " + frank + " Warden"},
		},
		{
			name:  "a person is not taken out of a group that holds a role the caller may not take",
			roles: map[access.GroupRoleKey]accesstypes.Period{warden: {}}, members: map[impulse.UUID]accesstypes.Period{userID: {}}, act: remove,
			wantSays:  "taking a person out of the group Night shift takes away the role Warden, which you may not take from them",
			wantAsked: []string{"take domain/alpha " + danaID + " Warden"},
		},
		{
			name:  "a person is taken out of a group that holds only roles the caller may take",
			roles: map[access.GroupRoleKey]accesstypes.Period{reader: {}}, members: map[impulse.UUID]accesstypes.Period{userID: {}}, act: remove,
			wantAsked:  []string{"take domain/alpha " + danaID + " Reader"},
			wantWrites: []string{"delete group member " + nightID.String() + " " + danaID},
		},
		{
			name:  "a place that has ended gives the person nothing, and goes whoever asks",
			roles: map[access.GroupRoleKey]accesstypes.Period{warden: {}}, members: map[impulse.UUID]accesstypes.Period{userID: ended}, act: remove,
			wantWrites: []string{"delete group member " + nightID.String() + " " + danaID},
		},
		{
			name:    "a group is not given a role the caller may not give to a person in it",
			members: map[impulse.UUID]accesstypes.Period{userID: {}}, act: give,
			wantSays:  "giving the group Night shift a role gives everyone in it the role Warden, which you may not give them",
			wantAsked: []string{"give domain/alpha " + danaID + " Warden"},
		},
		{
			name: "a group with nobody in it is given the role: it is given to nobody",
			act:  give,
			wantWrites: []string{"insert group role " + groupRoleText(&warden)},
		},
		{
			name:    "a person whose place has ended is given nothing by the group's new role",
			members: map[impulse.UUID]accesstypes.Period{userID: ended}, act: give,
			wantWrites: []string{"insert group role " + groupRoleText(&warden)},
		},
		{
			name:  "a group does not lose a role the caller may not take from a person in it",
			roles: map[access.GroupRoleKey]accesstypes.Period{warden: {}}, members: map[impulse.UUID]accesstypes.Period{userID: {}}, act: take,
			wantSays:  "taking a role from the group Night shift takes away from everyone in it the role Warden, which you may not take from them",
			wantAsked: []string{"take domain/alpha " + danaID + " Warden"},
		},
		{
			name:  "a role of the group that has ended goes whoever asks",
			roles: map[access.GroupRoleKey]accesstypes.Period{warden: ended}, members: map[impulse.UUID]accesstypes.Period{userID: {}}, act: take,
			wantWrites: []string{"delete group role " + groupRoleText(&warden)},
		},
		{
			name:  "a call outside any request is the application's own act and is not judged",
			roles: map[access.GroupRoleKey]accesstypes.Period{warden: {}}, outside: true, act: add,
			wantWrites: []string{"insert group member " + nightID.String() + " " + frank},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			store.usernames[frankID] = "frank"
			// dana holds a role of her own, so no removal here deletes her account.
			store.memberships[access.NewMembershipKey(globalScope, dana, "Steward")] = true
			store.rolePeriods = map[access.GroupRoleKey]accesstypes.Period{}
			store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{}
			for key, period := range tt.roles {
				store.groupRoles[key] = true
				store.rolePeriods[key] = period
			}
			for id, period := range tt.members {
				store.groupMembers[memberKey(nightID, id)] = true
				store.placePeriods[memberKey(nightID, id)] = period
			}
			grants := &fakeGrants{noGive: []accesstypes.Role{"Warden"}, noTake: []accesstypes.Role{"Warden"}}
			m, _ := newManager(t, store, wardenRoles, WithRoleGrants(grants))

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				if !tt.outside {
					ctx = asCallerThrough(ctx, managedAuth, erin)
				}

				return tt.act(ctx, txn, m)
			})
			switch {
			case tt.wantSays == "" && err != nil:
				t.Fatalf("error = %v, want the change made", err)
			case tt.wantSays != "" && (err == nil || !httpio.HasForbidden(err) || !strings.Contains(err.Error(), tt.wantSays)):
				t.Fatalf("error = %v, want Forbidden saying %q", err, tt.wantSays)
			}
			if diff := cmp.Diff(tt.wantAsked, grants.asked); diff != "" {
				t.Errorf("what the grants were asked (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("a manager with no grants refuses a request it cannot judge", func(t *testing.T) {
		t.Parallel()
		store := groupStore()
		store.usernames[frankID] = "frank"
		m, _ := newManager(t, store, wardenRoles)

		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.AddGroupMember(asCallerThrough(ctx, managedAuth, erin), txn, nightID, frankID)
		})
		if err == nil || httpio.HasForbidden(err) || !strings.Contains(err.Error(), "identity.WithRoleGrants") || len(store.writes) != 0 {
			t.Errorf("error = %v, writes = %v; want an error naming identity.WithRoleGrants and nothing written", err, store.writes)
		}
	})

	t.Run("a grant that cannot be read fails the change", func(t *testing.T) {
		t.Parallel()
		store := groupStore()
		store.usernames[frankID] = "frank"
		store.groupRoles[reader] = true
		m, _ := newManager(t, store, wardenRoles, WithRoleGrants(&fakeGrants{err: errStore}))

		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.AddGroupMember(asCallerThrough(ctx, managedAuth, erin), txn, nightID, frankID)
		})
		if !errors.Is(err, errStore) || len(store.writes) != 0 {
			t.Errorf("error = %v, writes = %v; want the grants' own error and nothing written", err, store.writes)
		}
	})
}

// Test_lengthens pins what it is for an assignment to last longer with a new period
// than with the one it had: a later end or none, a start that comes sooner while the
// assignment has not begun, and any period that still counts once the old one is over.
func Test_lengthens(t *testing.T) {
	t.Parallel()

	now := time.Now()
	at := func(hours float64) time.Time { return now.Add(time.Duration(hours * float64(time.Hour))) }
	between := func(start, end float64) accesstypes.Period { return accesstypes.Period{Start: at(start), End: at(end)} }
	until := func(end float64) accesstypes.Period { return accesstypes.Period{End: at(end)} }
	from := func(start float64) accesstypes.Period { return accesstypes.Period{Start: at(start)} }
	var none accesstypes.Period

	tests := []struct {
		name      string
		had, next accesstypes.Period
		want      bool
	}{
		{name: "the same period", had: until(1), next: until(1)},
		{name: "no end in place of an end", had: until(1), next: none, want: true},
		{name: "a later end", had: until(1), next: until(2), want: true},
		{name: "an earlier end", had: until(2), next: until(1)},
		{name: "an end in place of none", had: none, next: until(1)},
		{name: "none in place of none", had: none, next: none},
		{name: "a start that comes sooner while it has not begun", had: from(2), next: from(1), want: true},
		{name: "no start while it has not begun", had: from(1), next: none, want: true},
		{name: "a later start while it has not begun", had: from(1), next: from(2)},
		{name: "another start in the past changes nothing from now on", had: between(-2, 1), next: between(-3, 1)},
		{name: "a start still to come for one that counts now", had: until(1), next: between(0.5, 1)},
		{name: "any period that counts once the old one is over", had: between(-2, -1), next: until(1), want: true},
		{name: "none once the old one is over", had: between(-2, -1), next: none, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := lengthens(tt.had, tt.next, now); got != tt.want {
				t.Errorf("lengthens(%s, %s) = %v, want %v", periodText(tt.had), periodText(tt.next), got, tt.want)
			}
		})
	}
}

// TestManager_nobodyLengthensTheirOwnHolding pins the rule on the three writes that give
// a period: a caller who may give a role does not make their own role, their own place
// in a group, or a role of a group they are in last longer than it was given for.
// Someone else who may give it does, the caller shortens it, and a call outside any
// request is the application's own.
func TestManager_nobodyLengthensTheirOwnHolding(t *testing.T) {
	t.Parallel()

	ended, upcoming, current := periodFixtures()
	var none accesstypes.Period
	shorter := accesstypes.Period{End: current.End.Add(-30 * time.Minute)}
	longer := accesstypes.Period{Start: current.Start, End: current.End.Add(time.Hour)}
	own := access.NewMembershipKey(alphaScope, dana, "Reader")
	place := memberKey(nightID, userID)
	groupRole := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")
	asDana := func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, dana) }
	asErin := func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, erin) }
	fromAnotherAuth := func(ctx context.Context) context.Context { return asCallerThrough(ctx, otherAuth, dana) }
	outside := func(ctx context.Context) context.Context { return ctx }
	role := func(period accesstypes.Period) func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
			return m.AssignRoleFor(ctx, txn, alphaScope, userID, "Reader", period)
		}
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

	tests := []struct {
		name string
		// had is the period of the membership, of the place and of the group's role dana
		// holds before the act, and placed the period of dana's place where it is not had.
		had      accesstypes.Period
		placed   *accesstypes.Period
		ctx      func(context.Context) context.Context
		act      func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		wantSays string
		// wantWrite is the one write of a change that goes through.
		wantWrite string
	}{
		{name: "their own role, with no end", had: current, ctx: asDana, act: role(none), wantSays: "you cannot make your own role last longer"},
		{name: "their own role, with a later end", had: current, ctx: asDana, act: role(longer), wantSays: "you cannot make your own role last longer"},
		{name: "their own role that has not begun, from now", had: upcoming, ctx: asDana, act: role(none), wantSays: "you cannot make your own role last longer"},
		{name: "their own role that has ended, again", had: ended, ctx: asDana, act: role(current), wantSays: "you cannot make your own role last longer"},
		{name: "their own role, with an earlier end", had: current, ctx: asDana, act: role(shorter), wantWrite: "period of membership " + membershipText(&own) + ": " + periodText(shorter)},
		{name: "somebody else gives the role a new period", had: current, ctx: asErin, act: role(none), wantWrite: "period of membership " + membershipText(&own) + ": none"},
		{name: "the application's own act gives the role a new period", had: current, ctx: outside, act: role(none), wantWrite: "period of membership " + membershipText(&own) + ": none"},
		{name: "a caller of another auth is nobody of this one", had: current, ctx: fromAnotherAuth, act: role(none), wantWrite: "period of membership " + membershipText(&own) + ": none"},

		{name: "their own place in a group, with no end", had: current, ctx: asDana, act: inGroup(none), wantSays: "you cannot make your own place in a group last longer"},
		{name: "their own place in a group, with an earlier end", had: current, ctx: asDana, act: inGroup(shorter), wantWrite: "period of group member " + string(place.Group) + " " + string(place.User) + ": " + periodText(shorter)},
		{name: "somebody else gives the place a new period", had: current, ctx: asErin, act: inGroup(none), wantWrite: "period of group member " + string(place.Group) + " " + string(place.User) + ": none"},

		{name: "a role of a group they are in, with no end", had: current, placed: &none, ctx: asDana, act: ofGroup(none), wantSays: "you cannot make a role of a group you are in last longer"},
		{name: "a role of a group they are in, with an earlier end", had: current, placed: &none, ctx: asDana, act: ofGroup(shorter), wantWrite: "period of group role " + groupRoleText(&groupRole) + ": " + periodText(shorter)},
		{name: "somebody who is not in the group gives its role a new period", had: current, placed: &none, ctx: asErin, act: ofGroup(none), wantWrite: "period of group role " + groupRoleText(&groupRole) + ": none"},
		{name: "a person whose place in the group has ended holds nothing through it", had: current, placed: &ended, ctx: asDana, act: ofGroup(none), wantWrite: "period of group role " + groupRoleText(&groupRole) + ": none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			store.memberships[own] = true
			store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{own: tt.had}
			store.groupMembers[place] = true
			store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{place: tt.had}
			if tt.placed != nil {
				store.placePeriods[place] = *tt.placed
			}
			store.groupRoles[groupRole] = true
			store.rolePeriods = map[access.GroupRoleKey]accesstypes.Period{groupRole: tt.had}
			m, _ := newManager(t, store, engineRoles, WithRoleGrants(&fakeGrants{}))

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.act(tt.ctx(ctx), txn, m)
			})
			if tt.wantSays != "" {
				if err == nil || !httpio.HasForbidden(err) || !strings.Contains(err.Error(), tt.wantSays) || !strings.Contains(err.Error(), "someone else who may give it") {
					t.Fatalf("error = %v, want Forbidden saying %q and who does it instead", err, tt.wantSays)
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
