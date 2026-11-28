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
)

// The roles dana holds on records in these tests: an editor of project p1 and of p2, both
// alpha's, and a viewer of q1, beta's.
var (
	danaOnP1 = access.NewRecordMembershipKey(accesstypes.NewRecordScope("alpha", projects, "p1"), dana, "ProjectEditor")
	danaOnP2 = access.NewRecordMembershipKey(accesstypes.NewRecordScope("alpha", projects, "p2"), dana, "ProjectEditor")
	danaOnQ1 = access.NewRecordMembershipKey(accesstypes.NewRecordScope("beta", projects, "q1"), dana, "ProjectViewer")
)

// recordStore is the store of the groups (groupStore) with the people of alpha's own list
// and the roles held on records the test names, each with no period.
func recordStore(alphaPeople []impulse.UUID, held ...access.RecordMembershipKey) *fakeStore {
	store := groupStore()
	store.tenantPeople = map[accesstypes.Domain][]impulse.UUID{"alpha": alphaPeople}
	store.recordRoles = map[access.RecordMembershipKey]accesstypes.Period{}
	for _, key := range held {
		store.recordRoles[key] = accesstypes.Period{}
	}

	return store
}

// inAlphaTenant runs act with alpha's rules, for a request erin makes in alpha, in a
// transaction.
func inAlphaTenant(t *testing.T, m *Manager, act func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant) error) error {
	t.Helper()

	return inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		ctx = inAlpha(ctx, erin)
		tenant, err := m.Tenant(ctx)
		if err != nil {
			return err
		}

		return act(ctx, txn, tenant)
	})
}

// TestTenant_GiveRoleOn pins a person's role given on one record of the tenant: the access
// library's rule first, in its words; the role's period held to a period that has not
// ended; the person one of the tenant's people, which a person who holds a role on
// another of its records is, and one who holds a role on another tenant's record alone is
// not; then the row written with its period and the engine told at the commit.
func TestTenant_GiveRoleOn(t *testing.T) {
	t.Parallel()

	ends := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	id := userID.String()
	tests := []struct {
		name    string
		people  []impulse.UUID
		held    []access.RecordMembershipKey
		record  accesstypes.Resource
		role    accesstypes.Role
		period  accesstypes.Period
		wantErr func(error) bool
		// wantSays is in the refusal's words.
		wantSays  string
		wantWrite string
	}{
		{
			name: "a person of the tenant is given the role on the record for the period", people: []impulse.UUID{userID}, record: projects, role: "ProjectEditor",
			period:    accesstypes.Period{End: ends},
			wantWrite: "set record membership alpha Projects/p1 " + id + " ProjectEditor .." + ends.Format(time.RFC3339),
		},
		{
			name: "a role the role file does not mark for the kind is refused in the library's words", people: []impulse.UUID{userID}, record: projects, role: "Reader",
			wantErr: httpio.HasBadRequest, wantSays: "Reader is not a role held on a project.",
		},
		{
			name: "a kind no role is held on is refused", people: []impulse.UUID{userID}, record: "Clients", role: "ProjectEditor",
			wantErr: httpio.HasBadRequest, wantSays: "No role can be held on one Clients.",
		},
		{
			name: "a period that has ended is refused", people: []impulse.UUID{userID}, record: projects, role: "ProjectEditor",
			period:  accesstypes.Period{End: time.Now().Add(-time.Hour)},
			wantErr: httpio.HasBadRequest, wantSays: "The period has already ended, so the role would count for nothing.",
		},
		{
			name: "somebody who is no person of the tenant is not found, as the tenant's group methods answer one", record: projects, role: "ProjectEditor",
			wantErr: httpio.HasNotFound,
		},
		{
			name: "a person who holds a role on another of the tenant's records alone is one of its people", held: []access.RecordMembershipKey{danaOnP2},
			record: projects, role: "ProjectEditor", wantWrite: "set record membership alpha Projects/p1 " + id + " ProjectEditor none",
		},
		{
			name: "a role on another tenant's record makes nobody one of this tenant's people", held: []access.RecordMembershipKey{danaOnQ1},
			record: projects, role: "ProjectEditor", wantErr: httpio.HasNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := recordStore(tt.people, tt.held...)
			m, engine := newManager(t, store, tenantRoles)
			err := inAlphaTenant(t, m, func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant) error {
				return tenant.GiveRoleOn(ctx, txn, tt.record, "p1", userID, tt.role, tt.period)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("GiveRoleOn() error = %v, want the refusal", err)
				}
				if tt.wantSays != "" && !strings.Contains(httpio.Message(err), tt.wantSays) {
					t.Errorf("GiveRoleOn() says %q, want %q", httpio.Message(err), tt.wantSays)
				}
				if len(store.writes) != 0 || engine.changed != 0 {
					t.Errorf("a refused give buffered %v and told the engine %d time(s), want nothing", store.writes, engine.changed)
				}

				return
			}
			if err != nil {
				t.Fatalf("GiveRoleOn() error = %v", err)
			}
			if diff := cmp.Diff([]string{tt.wantWrite}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != 1 {
				t.Errorf("the engine was told the policy changed %d time(s), want once, at the commit", engine.changed)
			}
		})
	}

	t.Run("the person behind a session that acts as someone else gives themselves nothing", func(t *testing.T) {
		t.Parallel()

		store := impersonatorStore()
		m, _ := impersonatorManager(t, store)
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			ctx = viewedBy(asErin(ctx, managedAuth, true), userID)

			return runInTenant(ctx, m, func(tenant *Tenant) error {
				return tenant.GiveRoleOn(ctx, txn, projects, "p1", userID, "ProjectEditor", accesstypes.Period{})
			})
		})
		if !httpio.HasForbidden(err) || !strings.Contains(httpio.Message(err), actingAsSomeoneElse+"give yourself a role.") {
			t.Fatalf("GiveRoleOn() error = %v, want the refusal %q", err, actingAsSomeoneElse+"give yourself a role")
		}
		if len(store.writes) != 0 {
			t.Errorf("buffered writes = %v, want none", store.writes)
		}
	})
}

// TestTenant_GiveRoleOn_ownPeriod pins the rule that nobody lengthens their own holding on
// a record: a caller who gives themselves their own role on the record is refused a period
// that lasts longer than the one it has, in the library's sentence, and is written one
// that does not; a role they do not hold there, another person's role, and a call outside
// any request are written.
func TestTenant_GiveRoleOn_ownPeriod(t *testing.T) {
	t.Parallel()

	until := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	ended := accesstypes.Period{End: time.Now().Add(-time.Hour).UTC().Truncate(time.Second)}
	const longer = "You cannot make your own role last longer than it was given for."
	tests := []struct {
		name string
		// caller makes the request; "" is a call outside any request.
		caller accesstypes.User
		// had is the period of dana's editor's role on p1; nil where she holds none there.
		had      *accesstypes.Period
		next     accesstypes.Period
		wantSays string
	}{
		{name: "the caller's own role, until an instant, given no end", caller: dana, had: &accesstypes.Period{End: until}, wantSays: longer},
		{name: "the caller's own role given a later end", caller: dana, had: &accesstypes.Period{End: until}, next: accesstypes.Period{End: until.Add(time.Hour)}, wantSays: longer},
		{name: "the caller's own role given a sooner end is written", caller: dana, had: &accesstypes.Period{End: until}, next: accesstypes.Period{End: until.Add(-30 * time.Minute)}},
		{name: "the caller's own role, ended, given again", caller: dana, had: &ended, wantSays: longer},
		{name: "a role the caller does not hold there is written", caller: dana},
		{name: "another person gives the caller's role a later end", caller: erin, had: &accesstypes.Period{End: until}, next: accesstypes.Period{End: until.Add(time.Hour)}},
		{name: "a call outside any request is written", had: &accesstypes.Period{End: until}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := recordStore([]impulse.UUID{userID})
			if tt.had != nil {
				store.recordRoles[danaOnP1] = *tt.had
			}
			m, engine := newManager(t, store, tenantRoles)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				tenant, err := m.Tenant(inAlpha(ctx, erin))
				if err != nil {
					return err
				}
				if tt.caller != "" {
					ctx = inAlpha(ctx, tt.caller)
				}

				return tenant.GiveRoleOn(ctx, txn, projects, "p1", userID, "ProjectEditor", tt.next)
			})
			if tt.wantSays != "" {
				if !httpio.HasForbidden(err) || httpio.Message(err) != tt.wantSays {
					t.Fatalf("GiveRoleOn() error = %v (%q), want 403 saying %q", err, httpio.Message(err), tt.wantSays)
				}
				if len(store.writes) != 0 || engine.changed != 0 {
					t.Errorf("a refused give buffered %v and told the engine %d time(s), want nothing", store.writes, engine.changed)
				}

				return
			}
			if err != nil {
				t.Fatalf("GiveRoleOn() error = %v", err)
			}
			if len(store.writes) != 1 || !strings.HasPrefix(store.writes[0], "set record membership alpha Projects/p1 "+userID.String()+" ProjectEditor") {
				t.Errorf("buffered writes = %v, want the role written", store.writes)
			}
		})
	}
}

// TestTenant_GiveGroupRoleOn_ownPeriod pins the same rule for a group's role on a record: a
// caller who is one of the group's people, whose place has not ended, is refused a period
// for the group's role that lasts longer than the one it has, in the library's sentence; a
// caller who is not in the group, and one whose place in it has ended, are written.
func TestTenant_GiveGroupRoleOn_ownPeriod(t *testing.T) {
	t.Parallel()

	until := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	nightOnP1 := access.NewRecordGroupRoleKey(GroupID(nightID), accesstypes.NewRecordScope("alpha", projects, "p1"), "ProjectViewer")
	tests := []struct {
		name string
		// in says the caller, dana, is one of the night shift; place is her place's period.
		in       bool
		place    accesstypes.Period
		wantSays string
	}{
		{name: "a caller in the group gives its role no end", in: true, wantSays: "You cannot make a role of a group you are in last longer than it was given for."},
		{name: "a caller who is not in the group is written"},
		{name: "a caller whose place in the group has ended is written", in: true, place: accesstypes.Period{End: time.Now().Add(-time.Hour)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := recordStore(nil)
			store.recordGroupRoles = map[access.RecordGroupRoleKey]accesstypes.Period{nightOnP1: {End: until}}
			if tt.in {
				store.groupMembers[memberKey(nightID, userID)] = true
				store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): tt.place}
			}
			m, _ := newManager(t, store, tenantRoles, WithRoleGrants(&fakeGrants{}))
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = inAlpha(ctx, dana)
				tenant, err := m.Tenant(ctx)
				if err != nil {
					return err
				}

				return tenant.GiveGroupRoleOn(ctx, txn, projects, "p1", nightID, "ProjectViewer", accesstypes.Period{})
			})
			if tt.wantSays != "" {
				if !httpio.HasForbidden(err) || httpio.Message(err) != tt.wantSays {
					t.Fatalf("GiveGroupRoleOn() error = %v (%q), want 403 saying %q", err, httpio.Message(err), tt.wantSays)
				}
				if len(store.writes) != 0 {
					t.Errorf("a refused give buffered %v, want nothing", store.writes)
				}

				return
			}
			if err != nil {
				t.Fatalf("GiveGroupRoleOn() error = %v", err)
			}
			if len(store.writes) != 1 || !strings.HasPrefix(store.writes[0], "set record group role alpha Projects/p1 "+nightID.String()+" ProjectViewer") {
				t.Errorf("buffered writes = %v, want the group's role written", store.writes)
			}
		})
	}
}

// TestTenant_TakeRoleOn pins a person's role taken from one record of the tenant: the row
// goes and the engine is told at the commit, and a role the person does not hold there is
// no error. The person behind a session that acts as someone else takes nothing from
// themselves.
func TestTenant_TakeRoleOn(t *testing.T) {
	t.Parallel()

	store := recordStore(nil)
	m, engine := newManager(t, store, tenantRoles)
	err := inAlphaTenant(t, m, func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant) error {
		return tenant.TakeRoleOn(ctx, txn, projects, "p1", userID, "ProjectEditor")
	})
	if err != nil {
		t.Fatalf("TakeRoleOn() of a role not held there: error = %v, want none", err)
	}
	if diff := cmp.Diff([]string{"delete record membership " + recordMembershipText(&danaOnP1)}, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
	}

	t.Run("the person behind a session that acts as someone else takes nothing from themselves", func(t *testing.T) {
		t.Parallel()

		store := impersonatorStore()
		m, _ := impersonatorManager(t, store)
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			ctx = viewedBy(asErin(ctx, managedAuth, true), userID)

			return runInTenant(ctx, m, func(tenant *Tenant) error {
				return tenant.TakeRoleOn(ctx, txn, projects, "p1", userID, "ProjectEditor")
			})
		})
		if !httpio.HasForbidden(err) || !strings.Contains(httpio.Message(err), actingAsSomeoneElse+"take a role from yourself.") {
			t.Fatalf("TakeRoleOn() error = %v, want the refusal %q", err, actingAsSomeoneElse+"take a role from yourself")
		}
		if len(store.writes) != 0 {
			t.Errorf("buffered writes = %v, want none", store.writes)
		}
	})
}

// TestTenant_GiveGroupRoleOn pins a group's role given on one record of the tenant: the
// group one kept in the tenant, and a group kept for the whole application or in another
// tenant not found there, as every tenant's group method answers it; then the library's
// rule on the role, the row written with its period and the engine told.
func TestTenant_GiveGroupRoleOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		group     impulse.UUID
		role      accesstypes.Role
		wantErr   func(error) bool
		wantWrite string
	}{
		{name: "a group of the tenant is given the role on the record", group: nightID, role: "ProjectViewer", wantWrite: "set record group role alpha Projects/p1 " + nightID.String() + " ProjectViewer none"},
		{name: "a group kept for the whole application is not found in the tenant", group: staffID, role: "ProjectViewer", wantErr: httpio.HasNotFound},
		{name: "a group kept in another tenant is not found either", group: dayID, role: "ProjectViewer", wantErr: httpio.HasNotFound},
		{name: "a role the role file does not mark for the kind is refused", group: nightID, role: "Reader", wantErr: httpio.HasBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := recordStore(nil)
			m, engine := newManager(t, store, tenantRoles, WithRoleGrants(&fakeGrants{}))
			err := inAlphaTenant(t, m, func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant) error {
				return tenant.GiveGroupRoleOn(ctx, txn, projects, "p1", tt.group, tt.role, accesstypes.Period{})
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("GiveGroupRoleOn() error = %v, want the refusal", err)
				}
				if len(store.writes) != 0 || engine.changed != 0 {
					t.Errorf("a refused give buffered %v and told the engine %d time(s), want nothing", store.writes, engine.changed)
				}

				return
			}
			if err != nil {
				t.Fatalf("GiveGroupRoleOn() error = %v", err)
			}
			if diff := cmp.Diff([]string{tt.wantWrite}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != 1 {
				t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
			}
		})
	}
}

// TestTenant_GiveGroupRoleOn_eachPerson pins that giving a group a role on a record needs
// what giving it to each person in the group needs: each person whose place has not ended
// is asked for, in order, one whose place has not begun among them, and the first the
// caller may not give the role on the record refuses the give, in the sentence a group's
// role in the tenant is refused with, with nothing written and the engine not told. A
// group with nobody in it asks nothing, a call outside any request is not judged, and a
// caller in the group is asked for like anyone.
func TestTenant_GiveGroupRoleOn_eachPerson(t *testing.T) {
	t.Parallel()

	ended, upcoming, _ := periodFixtures()
	asked := func(ids ...impulse.UUID) []string {
		var want []string
		for _, id := range ids {
			want = append(want, "give on Projects p1 in alpha "+id.String()+" ProjectViewer")
		}

		return want
	}
	const refused = "Giving the group Night shift a role gives everyone in it the role Project viewer on one project, which you may not give them there."
	tests := []struct {
		name string
		// places are the night shift's people, each with the period of their place.
		places     map[impulse.UUID]accesstypes.Period
		noGiveOnTo []impulse.UUID
		// outside says the give is made outside any request.
		outside   bool
		wantSays  string
		wantAsked []string
	}{
		{
			name:      "a group of two the caller may each give the role on the record",
			places:    map[impulse.UUID]accesstypes.Period{userID: {}, frankID: {}},
			wantAsked: asked(userID, frankID),
		},
		{
			name:       "a group of two, one of whom the caller may not give the role on the record",
			places:     map[impulse.UUID]accesstypes.Period{userID: {}, frankID: {}},
			noGiveOnTo: []impulse.UUID{frankID},
			wantSays:   refused,
			wantAsked:  asked(userID, frankID),
		},
		{
			name: "a group with nobody in it asks nothing",
		},
		{
			name:       "a person whose place has ended is not asked for",
			places:     map[impulse.UUID]accesstypes.Period{userID: {}, frankID: ended},
			noGiveOnTo: []impulse.UUID{frankID},
			wantAsked:  asked(userID),
		},
		{
			name:       "a person whose place has not begun is asked for",
			places:     map[impulse.UUID]accesstypes.Period{frankID: upcoming},
			noGiveOnTo: []impulse.UUID{frankID},
			wantSays:   refused,
			wantAsked:  asked(frankID),
		},
		{
			name:       "a call outside any request is not judged",
			places:     map[impulse.UUID]accesstypes.Period{frankID: {}},
			noGiveOnTo: []impulse.UUID{frankID},
			outside:    true,
		},
		{
			name:       "the caller in the group is asked for like anyone",
			places:     map[impulse.UUID]accesstypes.Period{otherUser: {}},
			noGiveOnTo: []impulse.UUID{otherUser},
			wantSays:   refused,
			wantAsked:  asked(otherUser),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := recordStore(nil)
			store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{}
			for id, place := range tt.places {
				store.groupMembers[memberKey(nightID, id)] = true
				store.placePeriods[memberKey(nightID, id)] = place
			}
			grants := &fakeGrants{noGiveOnTo: tt.noGiveOnTo}
			m, engine := newManager(t, store, tenantRoles, WithRoleGrants(grants))
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				tenant, err := m.Tenant(inAlpha(ctx, erin))
				if err != nil {
					return err
				}
				if !tt.outside {
					ctx = inAlpha(ctx, erin)
				}

				return tenant.GiveGroupRoleOn(ctx, txn, projects, "p1", nightID, "ProjectViewer", accesstypes.Period{})
			})
			if diff := cmp.Diff(tt.wantAsked, grants.asked); diff != "" {
				t.Errorf("what the grants were asked (-want +got):\n%s", diff)
			}
			if tt.wantSays != "" {
				toldPlainly(t, err, tt.wantSays, "on Projects p1 in alpha")
				if len(store.writes) != 0 || engine.changed != 0 {
					t.Errorf("a refused give buffered %v and told the engine %d time(s), want nothing", store.writes, engine.changed)
				}

				return
			}
			if err != nil {
				t.Fatalf("GiveGroupRoleOn() error = %v", err)
			}
			if diff := cmp.Diff([]string{"set record group role alpha Projects/p1 " + nightID.String() + " ProjectViewer none"}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != 1 {
				t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
			}
		})
	}
}

// TestTenant_TakeGroupRoleOn pins a group's role taken from one record of the tenant: the
// row goes, a role the group does not hold there is no error, and the engine is told.
func TestTenant_TakeGroupRoleOn(t *testing.T) {
	t.Parallel()

	store := recordStore(nil)
	m, engine := newManager(t, store, tenantRoles)
	err := inAlphaTenant(t, m, func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant) error {
		return tenant.TakeGroupRoleOn(ctx, txn, projects, "p1", nightID, "ProjectViewer")
	})
	if err != nil {
		t.Fatalf("TakeGroupRoleOn() error = %v", err)
	}
	key := access.NewRecordGroupRoleKey(GroupID(nightID), accesstypes.NewRecordScope("alpha", projects, "p1"), "ProjectViewer")
	if diff := cmp.Diff([]string{"delete record group role " + recordGroupRoleText(&key)}, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
	}
}

// TestManager_RemoveFromTenant_recordRoles pins that a role on one of the tenant's records
// is something the person holds there: a person who holds such roles alone is removed,
// the warning names the roles, and the removal takes their roles on the tenant's records
// and leaves their role on another tenant's record.
func TestManager_RemoveFromTenant_recordRoles(t *testing.T) {
	t.Parallel()

	store := removalStore()
	store.recordRoles = map[access.RecordMembershipKey]accesstypes.Period{danaOnP1: {}, danaOnP2: {}, danaOnQ1: {}}
	m, engine, _ := removalManager(t, store, &fakeGrants{}, false)

	judged, err := judgeDana(t, m, asErinIn, "alpha")
	if err != nil {
		t.Fatalf("JudgeTenantRemoval() error = %v", err)
	}
	if diff := cmp.Diff([]string{"Project editor"}, judged.Roles); diff != "" {
		t.Errorf("the roles the warning names (-want +got):\n%s", diff)
	}
	if err := removeDana(t, m, asErinIn, "alpha"); err != nil {
		t.Fatalf("RemoveFromTenant() error = %v", err)
	}
	want := []string{
		"delete record membership " + recordMembershipText(&danaOnP1),
		"delete record membership " + recordMembershipText(&danaOnP2),
	}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
	}
}

// TestManager_PersonTenants_recordRoles pins a person's own tenants where they hold roles
// on records alone: each record's tenant is listed with the roles held there, the person
// leaves one, and the leaving takes their roles on that tenant's records alone. A role on a
// record whose period has ended places the person nowhere.
func TestManager_PersonTenants_recordRoles(t *testing.T) {
	t.Parallel()

	store := leavingStore()
	store.recordRoles = map[access.RecordMembershipKey]accesstypes.Period{
		danaOnP1: {}, danaOnQ1: {},
		access.NewRecordMembershipKey(accesstypes.NewRecordScope("gamma", projects, "g1"), dana, "ProjectViewer"): {End: time.Now().Add(-time.Hour)},
	}
	m, engine, _ := leavingManager(t, store, false)

	var tenants []PersonTenant
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		tenants, err = m.PersonTenants(asCaller(ctx, dana), txn, userID)

		return err
	}); err != nil {
		t.Fatalf("PersonTenants() error = %v", err)
	}
	want := []PersonTenant{{Domain: "alpha", Roles: []string{"Project editor"}}, {Domain: "beta", Roles: []string{"Project viewer"}}}
	if diff := cmp.Diff(want, tenants); diff != "" {
		t.Errorf("PersonTenants() (-want +got):\n%s", diff)
	}

	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.LeaveTenant(asCaller(ctx, dana), txn, userID, "alpha")
	}); err != nil {
		t.Fatalf("LeaveTenant() error = %v", err)
	}
	if diff := cmp.Diff([]string{"delete record membership " + recordMembershipText(&danaOnP1)}, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
	}
}

// TestManager_oneTenantPerPerson_recordRoles pins the rule of one tenant per person on a
// person who holds a role on a record alone: it holds them in the record's tenant, so a
// role in another tenant is refused and one in that tenant is given; and a directory's
// tenant that takes the person takes their roles on another tenant's records, and leaves
// their roles on its own.
func TestManager_oneTenantPerPerson_recordRoles(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		scope   accesstypes.PolicyScope
		refused bool
	}{{scope: alphaScope, refused: true}, {scope: betaScope}} {
		store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
		store.recordRoles = map[access.RecordMembershipKey]accesstypes.Period{danaOnQ1: {}}
		m, _ := newManager(t, store, tenantRoles, WithOneTenantPerPerson())
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.AssignRole(ctx, txn, tt.scope, userID, "Reader")
		})
		if tt.refused {
			assertOtherTenant(t, err, "beta")

			continue
		}
		if err != nil {
			t.Errorf("AssignRole(%s) error = %v, want the role given in the tenant the person belongs to", tt.scope, err)
		}
	}

	store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
	store.recordRoles = map[access.RecordMembershipKey]accesstypes.Period{danaOnP1: {}, danaOnQ1: {}}
	m, _ := newManager(t, store, tenantRoles, WithOneTenantPerPerson())
	var from []accesstypes.Domain
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		rows, err := m.readHeld(ctx, txn, userID)
		if err != nil {
			return err
		}
		from, err = m.takeOtherTenants(ctx, txn, rows, "beta")

		return err
	}); err != nil {
		t.Fatalf("takeOtherTenants() error = %v", err)
	}
	if diff := cmp.Diff([]string{"delete record membership " + recordMembershipText(&danaOnP1)}, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if !slices.Equal(from, []accesstypes.Domain{"alpha"}) {
		t.Errorf("takeOtherTenants() took from %v, want alpha", from)
	}
}

// TestManager_GivableRecordRoles pins the list a sharing dialog picks a role from: the
// roles the role file marks for the record's kind that the caller could give in the
// request's tenant with the dialog's own method, asked inside a transaction of the list's
// own, and no role of the tenant's that is held on no record. A call outside any request
// lists every one and asks nothing.
func TestManager_GivableRecordRoles(t *testing.T) {
	t.Parallel()

	const gives accesstypes.Resource = "GiveMembersRoleOnProject"
	for _, tt := range []struct {
		name      string
		ctx       func(context.Context) context.Context
		want      []accesstypes.Role
		wantAsked []string
	}{
		{
			name: "in a tenant's request, the roles the method's grant lets the caller give", ctx: func(ctx context.Context) context.Context { return inAlpha(ctx, erin) },
			want:      []accesstypes.Role{"ProjectEditor"},
			wantAsked: []string{string(gives) + " could give alpha ProjectEditor", string(gives) + " could give alpha ProjectViewer"},
		},
		{
			name: "outside any request, every role held on the kind", ctx: func(ctx context.Context) context.Context { return ctx },
			want: []accesstypes.Role{"ProjectEditor", "ProjectViewer"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			grants := &fakeGrants{noneGives: []accesstypes.Role{"ProjectViewer"}}
			m, _ := newManager(t, recordStore(nil), tenantRoles, WithRoleGrants(grants))
			roles, err := m.GivableRecordRoles(tt.ctx(t.Context()), fakeDatabase{}, gives, projects)
			if err != nil {
				t.Fatalf("GivableRecordRoles() error = %v", err)
			}
			got := make([]accesstypes.Role, 0, len(roles))
			for _, role := range roles {
				got = append(got, role.Name)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("GivableRecordRoles() (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantAsked, grants.asked); diff != "" {
				t.Errorf("what the grants were asked (-want +got):\n%s", diff)
			}
			if len(grants.askedOutside) != 0 {
				t.Errorf("questions asked outside a transaction = %v, want none", grants.askedOutside)
			}
		})
	}
}

// TestGivingRecord holds the record a list of the roles to give on a record is asked about to
// one equality on its key: none names no record and the list holds no rows, and any other
// way of naming records is refused with what to send.
func TestGivingRecord(t *testing.T) {
	t.Parallel()

	eq := func(value any) resource.Condition {
		return resource.Condition{Field: "RecordID", Operator: "eq", Value: value}
	}
	tests := []struct {
		name    string
		on      []resource.Condition
		want    string
		wantOK  bool
		wantErr bool
	}{
		{name: "no condition names no record, and the list holds no rows"},
		{name: "one record", on: []resource.Condition{eq("p-1")}, want: "p-1", wantOK: true},
		{name: "no key", on: []resource.Condition{eq("")}, wantErr: true},
		{name: "two conditions", on: []resource.Condition{eq("p-1"), eq("p-2")}, wantErr: true},
		{name: "a set of records", on: []resource.Condition{{Field: "RecordID", Operator: "in", Values: []any{"p-1", "p-2"}}}, wantErr: true},
		{name: "every record but one", on: []resource.Condition{{Field: "RecordID", Operator: "ne", Value: "p-1"}}, wantErr: true},
		{name: "a value that is no text", on: []resource.Condition{eq(7)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok, err := GivingRecord("StaffProjectRoles", tt.on)
			if tt.wantErr {
				if !httpio.HasBadRequest(err) {
					t.Errorf("GivingRecord() error = %v, want a bad request", err)
				}
				if message := httpio.Message(err); !strings.Contains(message, "StaffProjectRoles lists the roles to give on one record at a time") || !strings.Contains(message, "recordId:eq:<key>") {
					t.Errorf("GivingRecord() refusal = %q, want the list, what it answers for and what to send", message)
				}

				return
			}
			if err != nil {
				t.Fatalf("GivingRecord() error = %v", err)
			}
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("GivingRecord() = %q, %t, want %q, %t", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
