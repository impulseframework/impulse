package identity

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
)

// periodText prints a period as the fake's write log holds it.
func periodText(period accesstypes.Period) string {
	if period.IsZero() {
		return "none"
	}
	instant := func(at time.Time) string {
		if at.IsZero() {
			return ""
		}

		return at.UTC().Format(time.RFC3339)
	}

	return instant(period.Start) + ".." + instant(period.End)
}

// SetMembershipPeriod logs the period the membership is given, and the reason after it
// where one is given: a write with no reason leaves the membership with none.
func (f *fakeStore) SetMembershipPeriod(_ context.Context, _ resource.ReadWriteTransaction, key *access.MembershipKey, period accesstypes.Period, reason string) error {
	if err := f.fail("SetMembershipPeriod"); err != nil {
		return err
	}
	write := "period of membership " + membershipText(key) + ": " + periodText(period)
	if reason != "" {
		write += " because " + reason
	}
	f.writes = append(f.writes, write)

	return nil
}

func (f *fakeStore) SetGroupMemberPeriod(_ context.Context, _ resource.ReadWriteTransaction, key *access.GroupMemberKey, period accesstypes.Period) error {
	if err := f.fail("SetGroupMemberPeriod"); err != nil {
		return err
	}
	f.writes = append(f.writes, "period of group member "+string(key.Group)+" "+string(key.User)+": "+periodText(period))

	return nil
}

func (f *fakeStore) SetGroupRolePeriod(_ context.Context, _ resource.ReadWriteTransaction, key *access.GroupRoleKey, period accesstypes.Period) error {
	if err := f.fail("SetGroupRolePeriod"); err != nil {
		return err
	}
	f.writes = append(f.writes, "period of group role "+groupRoleText(key)+": "+periodText(period))

	return nil
}

func (f *fakeStore) UserPeriods(_ context.Context, _ resource.ReadOnlyTransaction, userID impulse.UUID) (*UserPeriods, error) {
	if err := f.fail("UserPeriods"); err != nil {
		return nil, err
	}
	periods := &UserPeriods{
		Memberships: map[access.MembershipKey]accesstypes.Period{},
		Places:      map[access.GroupMemberKey]accesstypes.Period{},
		Reasons:     map[access.MembershipKey]string{},
	}
	for key, held := range f.memberships {
		if held && key.User == User(userID) {
			periods.Memberships[key] = f.membershipPeriods[key]
			if reason := f.membershipReasons[key]; reason != "" {
				periods.Reasons[key] = reason
			}
		}
	}
	for key, in := range f.groupMembers {
		if in && key.User == User(userID) {
			periods.Places[key] = f.placePeriods[key]
		}
	}

	return periods, nil
}

func (f *fakeStore) GroupPeriods(_ context.Context, _ resource.ReadOnlyTransaction, groupID impulse.UUID) (*GroupPeriods, error) {
	if err := f.fail("GroupPeriods"); err != nil {
		return nil, err
	}
	periods := &GroupPeriods{Members: map[accesstypes.User]accesstypes.Period{}, Roles: map[access.GroupRoleKey]accesstypes.Period{}}
	for key, in := range f.groupMembers {
		if in && key.Group == GroupID(groupID) {
			periods.Members[key.User] = f.placePeriods[key]
		}
	}
	for key, held := range f.groupRoles {
		if held && key.Group == GroupID(groupID) {
			periods.Roles[key] = f.rolePeriods[key]
		}
	}

	return periods, nil
}

// EndedAssignments answers what the fake's database clock says has ended, never what the
// wall clock does: the two are told apart by a test that sets the database's behind.
func (f *fakeStore) EndedAssignments(_ context.Context, _ resource.ReadOnlyTransaction, userID impulse.UUID) ([]access.MembershipKey, []access.GroupMemberKey, error) {
	if err := f.fail("EndedAssignments"); err != nil {
		return nil, nil, err
	}
	now := time.Now()
	if f.databaseNow != nil {
		now = f.databaseNow()
	}
	var memberships []access.MembershipKey
	for key, held := range f.memberships {
		if held && key.User == User(userID) && f.membershipPeriods[key].Ended(now) {
			memberships = append(memberships, key)
		}
	}
	var places []access.GroupMemberKey
	for key, in := range f.groupMembers {
		if in && key.User == User(userID) && f.placePeriods[key].Ended(now) {
			places = append(places, key)
		}
	}

	return memberships, places, nil
}

func (f *fakeStore) RemoveEnded(_ context.Context, _ resource.ReadWriteTransaction, memberships []access.MembershipKey, places []access.GroupMemberKey) error {
	if err := f.fail("RemoveEnded"); err != nil {
		return err
	}
	for i := range memberships {
		f.writes = append(f.writes, "remove membership "+membershipText(&memberships[i])+" ("+EndedByItself+")")
	}
	for _, place := range places {
		f.writes = append(f.writes, "remove group member "+string(place.Group)+" "+string(place.User)+" ("+EndedByItself+")")
	}

	return nil
}

// The periods the tests assign with, around the present by an hour each way.
func periodFixtures() (ended, upcoming, current accesstypes.Period) {
	now := time.Now().UTC().Truncate(time.Second)

	return accesstypes.Period{Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)},
		accesstypes.Period{Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)},
		accesstypes.Period{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
}

// TestManager_AssignRoleFor pins the period of a membership as the rules give it: a new
// membership is written with its period; one the person holds is given the period handed
// in place of the one it had, and none where it is given again with none; the same
// period again writes nothing and tells the engine nothing; a period that never counts
// and any period on the Super Administrator role are refused before anything is written.
func TestManager_AssignRoleFor(t *testing.T) {
	t.Parallel()

	ended, upcoming, current := periodFixtures()
	held := access.NewMembershipKey(alphaScope, User(userID), "Reader")
	text := "domain/alpha " + userID.String() + " Reader"
	tests := []struct {
		name       string
		holds      bool
		had        accesstypes.Period
		role       accesstypes.Role
		scope      accesstypes.PolicyScope
		period     accesstypes.Period
		wantErr    string
		wantWrites []string
		wantTold   int
	}{
		{
			name: "a new membership with a period", role: "Reader", scope: alphaScope, period: current,
			wantWrites: []string{"insert membership " + text, "period of membership " + text + ": " + periodText(current)}, wantTold: 1,
		},
		{name: "a new membership with none", role: "Reader", scope: alphaScope, wantWrites: []string{"insert membership " + text}, wantTold: 1},
		{
			name: "a membership held with no period is given one", holds: true, role: "Reader", scope: alphaScope, period: upcoming,
			wantWrites: []string{"period of membership " + text + ": " + periodText(upcoming)}, wantTold: 1,
		},
		{
			name: "a new period replaces the old one", holds: true, had: ended, role: "Reader", scope: alphaScope, period: current,
			wantWrites: []string{"period of membership " + text + ": " + periodText(current)}, wantTold: 1,
		},
		{
			name: "given again with none, an ended membership stands until it is removed", holds: true, had: ended, role: "Reader", scope: alphaScope,
			wantWrites: []string{"period of membership " + text + ": none"}, wantTold: 1,
		},
		{name: "the period it has, again", holds: true, had: current, role: "Reader", scope: alphaScope, period: current},
		{name: "none, where it has none", holds: true, role: "Reader", scope: alphaScope},
		{
			name: "a period that ends before it starts", role: "Reader", scope: alphaScope,
			period: accesstypes.Period{Start: current.End, End: current.Start}, wantErr: "not after its start",
		},
		{name: "a period that is over already", role: "Reader", scope: alphaScope, period: ended, wantErr: "take it away to end it now"},
		{name: "an end on the Super Administrator role", role: accesstypes.SuperAdministrator, scope: globalScope, period: accesstypes.Period{End: current.End}, wantErr: "The Super administrator role takes no start and no end, because there is always a super administrator and a period could leave none. Give the role with no period, and remove it when it is no longer wanted."},
		{name: "a start on the Super Administrator role", role: accesstypes.SuperAdministrator, scope: globalScope, period: accesstypes.Period{Start: upcoming.Start}, wantErr: "The Super administrator role takes no start and no end, because there is always a super administrator and a period could leave none. Give the role with no period, and remove it when it is no longer wanted."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			if tt.holds {
				store.memberships[held] = true
				store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{held: tt.had}
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.AssignRoleFor(ctx, txn, tt.scope, userID, tt.role, tt.period, "")
			})
			if tt.wantErr != "" {
				if err == nil || !httpio.HasBadRequest(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("AssignRoleFor() error = %v, want a bad request containing %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("AssignRoleFor() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantTold {
				t.Errorf("the engine was told %d time(s), want %d", engine.changed, tt.wantTold)
			}
		})
	}
}

// TestManager_groupPeriods pins the period of a place in a group and of a group's role
// as the rules give them, each replaced by giving again, and the deletion of a group
// against the periods: a member and a role that have ended are taken with the group as
// any are, by the group's own delete, and nothing their removal asks is asked of them,
// so dana, acting as someone else, deletes a group whose place of hers has ended and is
// refused one whose place of hers counts or has not begun. A role that has not begun is
// asked what taking it from the group asks, as one that counts is.
func TestManager_groupPeriods(t *testing.T) {
	t.Parallel()

	ended, upcoming, current := periodFixtures()
	place := memberKey(nightID, userID)
	role := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")
	placeText := string(place.Group) + " " + string(place.User)

	t.Run("a place and a role given for a period, and given again", func(t *testing.T) {
		t.Parallel()
		store := groupStore()
		m, engine := newManager(t, store, engineRoles)
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			if err := m.AddGroupMemberFor(ctx, txn, nightID, userID, current); err != nil {
				return err
			}

			return m.GiveGroupRoleFor(ctx, txn, nightID, alphaScope, "Reader", upcoming)
		})
		if err != nil {
			t.Fatalf("giving the place and the role: error = %v", err)
		}
		want := []string{
			"insert group member " + placeText, "period of group member " + placeText + ": " + periodText(current),
			"insert group role " + groupRoleText(&role), "period of group role " + groupRoleText(&role) + ": " + periodText(upcoming),
		}
		if diff := cmp.Diff(want, store.writes); diff != "" {
			t.Errorf("buffered writes (-want +got):\n%s", diff)
		}
		if engine.changed != 1 {
			t.Errorf("the engine was told %d time(s), want once for the transaction", engine.changed)
		}

		// Both are there with those periods: the same again writes nothing, and none
		// replaces each.
		store.writes = nil
		store.groupMembers[place], store.groupRoles[role] = true, true
		store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{place: current}
		store.rolePeriods = map[access.GroupRoleKey]accesstypes.Period{role: upcoming}
		err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			if err := m.AddGroupMemberFor(ctx, txn, nightID, userID, current); err != nil {
				return err
			}

			return m.GiveGroupRoleFor(ctx, txn, nightID, alphaScope, "Reader", upcoming)
		})
		if err != nil || len(store.writes) != 0 || engine.changed != 1 {
			t.Errorf("the same periods again: error = %v, writes = %v, the engine told %d time(s); want nothing written or told", err, store.writes, engine.changed)
		}
		err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			if err := m.AddGroupMember(ctx, txn, nightID, userID); err != nil {
				return err
			}

			return m.GiveGroupRole(ctx, txn, nightID, alphaScope, "Reader")
		})
		want = []string{"period of group member " + placeText + ": none", "period of group role " + groupRoleText(&role) + ": none"}
		if diff := cmp.Diff(want, store.writes); err != nil || diff != "" {
			t.Errorf("given again with no period: error = %v, buffered writes (-want +got):\n%s", err, diff)
		}
	})

	t.Run("a period that ends before it starts, and one that is over already", func(t *testing.T) {
		t.Parallel()
		store := groupStore()
		m, _ := newManager(t, store, engineRoles)
		for name, refused := range map[string]accesstypes.Period{
			"not after its start":        {Start: current.End, End: current.Start},
			"take it away to end it now": ended,
		} {
			for call, give := range map[string]func(ctx context.Context, txn resource.ReadWriteTransaction) error{
				"AddGroupMemberFor": func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					return m.AddGroupMemberFor(ctx, txn, nightID, userID, refused)
				},
				"GiveGroupRoleFor": func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					return m.GiveGroupRoleFor(ctx, txn, nightID, alphaScope, "Reader", refused)
				},
			} {
				if err := inTransaction(t, give); err == nil || !httpio.HasBadRequest(err) || !strings.Contains(err.Error(), name) {
					t.Errorf("%s() error = %v, want a bad request containing %q", call, err, name)
				}
			}
		}
		if len(store.writes) != 0 {
			t.Errorf("buffered writes = %v, want none", store.writes)
		}
	})

	// Each delete is asked as dana acting as someone else, or as erin, who may take
	// Reader from nobody.
	asDana := func(ctx context.Context) context.Context {
		return viewedBy(asErin(ctx, managedAuth, false), userID)
	}
	asErinTakingNoReader := func(ctx context.Context) context.Context {
		return asCallerThrough(ctx, managedAuth, erin)
	}
	deletes := []struct {
		name     string
		place    accesstypes.Period
		role     accesstypes.Period
		caller   func(context.Context) context.Context
		wantSays string
	}{
		{name: "a member and a role that have ended go with the group, and are not asked about", place: ended, role: ended, caller: asDana},
		{
			name: "a member whose place counts is asked what removing them asks", role: ended, caller: asDana,
			wantSays: actingAsSomeoneElse + "take yourself out of a group.",
		},
		{
			name: "a member whose place has not begun is asked what removing them asks", place: upcoming, role: ended, caller: asDana,
			wantSays: actingAsSomeoneElse + "take yourself out of a group.",
		},
		{
			name: "a role whose period has not begun is asked what taking it from the group asks", role: upcoming, caller: asErinTakingNoReader,
			wantSays: "Taking a role from the group Night shift takes away from everyone in it the role Reader, which you may not take from them.",
		},
	}
	for _, tt := range deletes {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			store.groupMembers[place], store.groupRoles[role] = true, true
			store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{place: tt.place}
			store.rolePeriods = map[access.GroupRoleKey]accesstypes.Period{role: tt.role}
			m, _ := newManager(t, store, engineRoles, WithRoleGrants(&fakeGrants{noTake: []accesstypes.Role{"Reader"}}))

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				_, err := m.DeleteGroup(tt.caller(ctx), txn, nightID, true)

				return err
			})
			if tt.wantSays != "" {
				if err == nil || !httpio.HasForbidden(err) || !strings.Contains(httpio.Message(err), tt.wantSays) || len(store.writes) != 0 {
					t.Fatalf("DeleteGroup() = %v with writes %v, want the refusal %q and nothing written", err, store.writes, tt.wantSays)
				}

				return
			}
			want := []string{"delete group " + nightID.String() + " with everything under it"}
			if diff := cmp.Diff(want, store.writes); err != nil || diff != "" {
				t.Errorf("DeleteGroup() error = %v, buffered writes (-want +got):\n%s", err, diff)
			}
		})
	}
}

// clearStore is a fake store where dana holds Reader in alpha for a period that has
// ended, Steward for the whole application with no period and Reader in every tenant
// for a period that has not begun, and is in the night shift for a period that has ended
// and in the staff with none.
func clearStore() (*fakeStore, access.MembershipKey, access.GroupMemberKey) {
	ended, upcoming, _ := periodFixtures()
	gone := access.NewMembershipKey(alphaScope, User(userID), "Reader")
	stays := access.NewMembershipKey(globalScope, User(userID), "Steward")
	later := access.NewMembershipKey(everyScope, User(userID), "Reader")
	store := groupStore()
	store.memberships = map[access.MembershipKey]bool{gone: true, stays: true, later: true}
	store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{gone: ended, later: upcoming}
	store.groupMembers = map[access.GroupMemberKey]bool{memberKey(nightID, userID): true, memberKey(staffID, userID): true}
	store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): ended}

	return store, gone, memberKey(nightID, userID)
}

// TestManager_ClearEnded pins what goes at a person's sign-in: the assignments whose
// period has ended, a membership and a place in a group alike, each removed under the
// source that says it ended by itself, and nothing else. The account stays also when
// the ended assignment was the last thing the person held; the rule on a tenant's last
// administrator is not asked; and the permission engine is told nothing.
func TestManager_ClearEnded(t *testing.T) {
	t.Parallel()

	t.Run("the ended rows go and nothing else", func(t *testing.T) {
		t.Parallel()
		store, gone, place := clearStore()
		m, engine := newManager(t, store, engineRoles)

		var cleared int
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			var err error
			cleared, err = m.ClearEnded(ctx, txn, userID)

			return err
		})
		if err != nil || cleared != 2 {
			t.Fatalf("ClearEnded() = (%d, %v), want two rows cleared", cleared, err)
		}
		want := []string{
			"remove membership " + membershipText(&gone) + " (PeriodEnd)",
			"remove group member " + string(place.Group) + " " + string(place.User) + " (PeriodEnd)",
		}
		if diff := cmp.Diff(want, store.writes); diff != "" {
			t.Errorf("buffered writes (-want +got):\n%s", diff)
		}
		if engine.changed != 0 {
			t.Errorf("the engine was told %d time(s): an ended assignment already counts for nothing, so nothing is to reload", engine.changed)
		}
	})

	t.Run("the account stays when the ended assignment was the last, and the last administrator is not judged", func(t *testing.T) {
		t.Parallel()
		ended, _, _ := periodFixtures()
		last := access.NewMembershipKey(alphaScope, User(userID), "Reader")
		store := groupStore()
		store.memberships[last] = true
		store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{last: ended}
		store.sessions = map[impulse.UUID][]impulse.UUID{userID: {sessionA}}
		// Reader administers a tenant here, and dana, its only holder, is the caller: a
		// removal of her own would be refused as the tenant's last administrator's.
		m, engine := newManager(t, store, engineRoles, WithAdministratorRoles([]accesstypes.Role{"Reader"}))

		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := m.ClearEnded(asCaller(ctx, User(userID)), txn, userID)

			return err
		})
		if err != nil {
			t.Fatalf("ClearEnded() error = %v", err)
		}
		want := []string{"remove membership " + membershipText(&last) + " (PeriodEnd)"}
		if diff := cmp.Diff(want, store.writes); diff != "" {
			t.Errorf("buffered writes (-want +got): the one row goes, and no session ends and no account is deleted\n%s", diff)
		}
		if engine.changed != 0 {
			t.Errorf("the engine was told %d time(s), want none", engine.changed)
		}
	})

	t.Run("nothing has ended", func(t *testing.T) {
		t.Parallel()
		store := groupStore()
		store.memberships[access.NewMembershipKey(globalScope, User(userID), "Steward")] = true
		m, _ := newManager(t, store, engineRoles)

		var cleared int
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			var err error
			cleared, err = m.ClearEnded(ctx, txn, userID)

			return err
		})
		if err != nil || cleared != 0 || len(store.writes) != 0 {
			t.Errorf("ClearEnded() = (%d, %v) with writes %v, want nothing", cleared, err, store.writes)
		}
	})
}

// TestManager_ClearEnded_readsTheDatabasesClock pins whose clock says an assignment has
// ended when it is removed: the database's, the one the lists and the sign-in rule read.
// A server whose clock runs ahead of the database's removes nothing that still counts
// there, and one whose clock runs behind removes what the database says is over.
func TestManager_ClearEnded_readsTheDatabasesClock(t *testing.T) {
	t.Parallel()

	now := time.Now()
	role := access.NewMembershipKey(alphaScope, User(userID), "Reader")
	place := memberKey(nightID, userID)
	tests := []struct {
		name string
		// ends is when the role and the place end, and database what the database's clock
		// reads, each from the server's present.
		ends, database time.Duration
		wantCleared    int
		wantOpened     int
	}{
		{name: "over by the server's clock and not by the database's: nothing is removed", ends: -30 * time.Minute, database: -time.Hour},
		{name: "over by the database's clock and not by the server's: the rows go", ends: 30 * time.Minute, database: time.Hour, wantCleared: 2, wantOpened: 1},
		{name: "over by both", ends: -time.Hour, database: 0, wantCleared: 2, wantOpened: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			newStore := func() *fakeStore {
				store := groupStore()
				store.memberships[role] = true
				store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{role: {End: now.Add(tt.ends)}}
				store.groupMembers[place] = true
				store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{place: {End: now.Add(tt.ends)}}
				store.databaseNow = func() time.Time { return now.Add(tt.database) }

				return store
			}

			store := newStore()
			m, _ := newManager(t, store, engineRoles)
			var cleared int
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				cleared, err = m.ClearEnded(ctx, txn, userID)

				return err
			})
			if err != nil || cleared != tt.wantCleared || len(store.writes) != tt.wantCleared {
				t.Errorf("ClearEnded() = (%d, %v) with writes %v, want %d row(s) cleared", cleared, err, store.writes, tt.wantCleared)
			}

			// The hook of a sign-in decides by the same clock whether to open a transaction.
			store = newStore()
			m, _ = newManager(t, store, engineRoles)
			client := &countingClient{MockClient: resource.NewMockClient(stubTxn{}, nil, nil)}
			if err := m.SignedIn(managedAuth, client)(t.Context(), &session.SignedIn{SessionID: sessionA, UserID: userID, Username: "dana"}); err != nil {
				t.Fatalf("the hook's error = %v", err)
			}
			if client.opened != tt.wantOpened || len(store.writes) != tt.wantCleared {
				t.Errorf("the hook opened %d transaction(s) and wrote %v, want %d and %d removal(s)", client.opened, store.writes, tt.wantOpened, tt.wantCleared)
			}
		})
	}
}

// countingClient is a database client that counts the transactions it opens.
type countingClient struct {
	*resource.MockClient

	opened int
}

func (c *countingClient) ExecuteFunc(ctx context.Context, f func(ctx context.Context, txn resource.ReadWriteTransaction) error) error {
	c.opened++

	return c.MockClient.ExecuteFunc(ctx, f) //nolint:wrapcheck // the mock's own error is the test's to read
}

// TestManager_SignedIn pins the hook a sign-in runs once it stands: the person's ended
// assignments are removed in a transaction of the hook's own, and the engine is told
// nothing; a sign-in with nothing to clear opens no transaction; and a failure of the
// removal is the hook's error to answer, which the session library logs while the
// session stands.
func TestManager_SignedIn(t *testing.T) {
	t.Parallel()

	signedIn := &session.SignedIn{SessionID: sessionA, UserID: userID, Username: "dana"}
	tests := []struct {
		name       string
		store      func() *fakeStore
		failOn     string
		wantErr    bool
		wantOpened int
		wantWrites int
	}{
		{name: "the ended assignments go", store: func() *fakeStore { s, _, _ := clearStore(); return s }, wantOpened: 1, wantWrites: 2},
		{name: "nothing to clear opens no transaction", store: groupStore},
		{name: "the removal fails", store: func() *fakeStore { s, _, _ := clearStore(); return s }, failOn: "RemoveEnded", wantErr: true, wantOpened: 1},
		{name: "the read fails", store: func() *fakeStore { s, _, _ := clearStore(); return s }, failOn: "EndedAssignments", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := tt.store()
			store.failOn = tt.failOn
			m, engine := newManager(t, store, engineRoles)
			client := &countingClient{MockClient: resource.NewMockClient(stubTxn{}, nil, nil)}

			err := m.SignedIn(managedAuth, client)(t.Context(), signedIn)
			if (err != nil) != tt.wantErr {
				t.Fatalf("the hook's error = %v, want an error = %v", err, tt.wantErr)
			}
			if client.opened != tt.wantOpened {
				t.Errorf("the hook opened %d transaction(s), want %d", client.opened, tt.wantOpened)
			}
			if len(store.writes) != tt.wantWrites || slices.ContainsFunc(store.writes, func(w string) bool { return !strings.HasSuffix(w, "("+EndedByItself+")") }) {
				t.Errorf("buffered writes = %v, want %d removals, each ended by itself", store.writes, tt.wantWrites)
			}
			if engine.changed != 0 {
				t.Errorf("the engine was told %d time(s), want none", engine.changed)
			}
		})
	}
}

// TestManager_endedAssignmentsInTheRules pins what an ended assignment is to the rules
// that read what a person holds: it places nobody in a tenant, so a person whose role in
// one tenant has ended is given a role in another where a person belongs to one; it
// makes nobody a tenant's administrator, so the last one whose role counts keeps it. The
// removal of the last role that counts takes the membership alone: the account stays.
func TestManager_endedAssignmentsInTheRules(t *testing.T) {
	t.Parallel()

	ended, upcoming, _ := periodFixtures()
	inAlpha := access.NewMembershipKey(alphaScope, User(userID), "Reader")
	inBeta := access.NewMembershipKey(betaScope, User(userID), "Reader")

	for name, tt := range map[string]struct {
		had     accesstypes.Period
		refused bool
	}{
		"a role in another tenant that has ended places the person nowhere": {had: ended},
		"one that has not begun places them where it will count":            {had: upcoming, refused: true},
		"one with no period places them there":                              {refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			store.memberships[inAlpha] = true
			store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{inAlpha: tt.had}
			m, _ := newManager(t, store, tenantRoles, WithOneTenantPerPerson())

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.AssignRole(ctx, txn, betaScope, userID, "Reader")
			})
			if tt.refused != (err != nil && httpio.HasConflict(err)) || (!tt.refused && err != nil) {
				t.Errorf("AssignRole() in a second tenant: error = %v, want refused = %v", err, tt.refused)
			}
		})
	}

	for name, tt := range map[string]struct {
		other   accesstypes.Period
		refused bool
	}{
		"the other administrator's role has ended":     {other: ended, refused: true},
		"the other administrator's role has not begun": {other: upcoming, refused: true},
		"the other administrator's role counts":        {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			other := access.NewMembershipKey(alphaScope, User(otherUser), "Reader")
			store := groupStore()
			store.memberships[inAlpha], store.memberships[other] = true, true
			store.memberships[access.NewMembershipKey(globalScope, User(userID), "Steward")] = true
			store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{other: tt.other}
			m, _ := newManager(t, store, engineRoles, WithAdministratorRoles([]accesstypes.Role{"Reader"}))

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveRole(asCaller(ctx, User(userID)), txn, alphaScope, userID, "Reader")
			})
			if tt.refused != (err != nil && httpio.HasForbidden(err)) || (!tt.refused && err != nil) {
				t.Errorf("RemoveRole() of the caller's own administrator role: error = %v, want refused = %v", err, tt.refused)
			}
		})
	}

	t.Run("the last role that counts is removed, and the account stays", func(t *testing.T) {
		t.Parallel()
		store := groupStore()
		store.memberships[inAlpha], store.memberships[inBeta] = true, true
		store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{inAlpha: ended}
		m, _ := newManager(t, store, tenantRoles)

		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.RemoveRole(ctx, txn, betaScope, userID, "Reader")
		})
		want := []string{"delete membership " + membershipText(&inBeta)}
		if diff := cmp.Diff(want, store.writes); err != nil || diff != "" {
			t.Errorf("RemoveRole() error = %v, buffered writes (-want +got): the membership goes and the account stays\n%s", err, diff)
		}
	})
}
