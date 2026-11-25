package identity

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// The people of the tenant alpha in the tests of its kinds. anna and ben hold roles in
// alpha alone, ben through a place in its group too; cara holds a role in alpha and one
// in beta; dora signs in through alpha's directory and holds nothing; evan holds a role in
// alpha and signs in through beta's directory.
var (
	annaID    = mustUUID("0190c000-0000-7000-8000-000000000001")
	benID     = mustUUID("0190c000-0000-7000-8000-000000000002")
	caraID    = mustUUID("0190c000-0000-7000-8000-000000000003")
	doraID    = mustUUID("0190c000-0000-7000-8000-000000000004")
	evanID    = mustUUID("0190c000-0000-7000-8000-000000000005")
	annaLogin = mustUUID("0190c000-0000-7000-8000-000000000011")
	doraLogin = mustUUID("0190c000-0000-7000-8000-000000000014")
	doraLink  = mustUUID("0190c000-0000-7000-8000-000000000024")
	evanLink  = mustUUID("0190c000-0000-7000-8000-000000000025")
	invited   = mustUUID("0190c000-0000-7000-8000-000000000031")
	betaInvit = mustUUID("0190c000-0000-7000-8000-000000000032")

	annaAlpha = access.NewMembershipKey(alphaScope, User(annaID), "Reader")
	benAlpha  = access.NewMembershipKey(alphaScope, User(benID), "Reader")
	caraAlpha = access.NewMembershipKey(alphaScope, User(caraID), "Reader")
	caraBeta  = access.NewMembershipKey(accesstypes.DomainPolicyScope("beta"), User(caraID), "Reader")
	evanAlpha = access.NewMembershipKey(alphaScope, User(evanID), "Reader")

	nightInAlpha = access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")
	staffInAlpha = access.NewGroupRoleKey(GroupID(staffID), alphaScope, "Reader")
	staffInBeta  = access.NewGroupRoleKey(GroupID(staffID), accesstypes.DomainPolicyScope("beta"), "Reader")
)

// alphaStore is the auth's tables holding the tenant alpha's people, its group night
// with ben and cara in it and a role, the staff group of the whole application holding
// a role in alpha and one in beta, a custom role made in alpha and one in beta, and an
// invitation into each. The accounts in gone were deleted by an earlier save, with
// everything they held.
func alphaStore(gone ...impulse.UUID) *fakeStore {
	store := &fakeStore{
		memberships: map[access.MembershipKey]bool{annaAlpha: true, benAlpha: true, caraAlpha: true, caraBeta: true, evanAlpha: true},
		roles: map[access.RoleKey]bool{
			{Kind: "domain", Domain: "alpha", Role: "Auditor"}: true,
			{Kind: "domain", Domain: "beta", Role: "Auditor"}:  true,
		},
		usernames:   map[impulse.UUID]string{annaID: "anna", benID: "ben", caraID: "cara", doraID: "dora", evanID: "evan"},
		sessions:    map[impulse.UUID][]impulse.UUID{annaID: {annaLogin}, doraID: {doraLogin}},
		identities:  map[impulse.UUID]impulse.UUID{doraLink: doraID, evanLink: evanID},
		managing:    map[impulse.UUID][]accesstypes.Domain{doraID: {"alpha"}, evanID: {"beta"}},
		invitations: map[accesstypes.Domain][]impulse.UUID{"alpha": {invited}, "beta": {betaInvit}},
		fakeGroupTables: fakeGroupTables{
			groups: map[impulse.UUID]*Group{
				nightID: {Kind: "domain", Domain: "alpha", Name: "Night shift"},
				staffID: {Kind: "global", Name: "Staff"},
			},
			groupMembers: map[access.GroupMemberKey]bool{memberKey(nightID, benID): true, memberKey(nightID, caraID): true},
			groupRoles:   map[access.GroupRoleKey]bool{nightInAlpha: true, staffInAlpha: true, staffInBeta: true},
		},
	}
	for _, id := range gone {
		delete(store.usernames, id)
		delete(store.sessions, id)
		delete(store.managing, id)
		for key := range store.memberships {
			if key.User == User(id) {
				delete(store.memberships, key)
			}
		}
		for key := range store.groupMembers {
			if key.User == User(id) {
				delete(store.groupMembers, key)
			}
		}
		for link, user := range store.identities {
			if user == id {
				delete(store.identities, link)
			}
		}
	}

	return store
}

// kindCounts are the counts of a tenant's kinds by their words.
func kindCounts(ctx context.Context, txn resource.ReadWriteTransaction, kinds []resource.TakenKind) (map[string]int64, error) {
	counts := make(map[string]int64, len(kinds))
	for _, kind := range kinds {
		rows, err := kind.Count(ctx, txn)
		if err != nil {
			return nil, errors.Wrapf(err, "counting the %s", kind.Words().Many)
		}
		counts[kind.Words().Many] = rows
	}

	return counts, nil
}

// takenRows is what one kind's Take answered.
type takenRows struct {
	Rows      int64
	Mutations int
	More      bool
}

// TestManager_TenantKinds_counts pins what the tenant's delete counts of the auth's rows:
// the accounts that are the tenant's alone (anna and ben, who hold roles in it alone, and
// dora, whom its directory manages and who holds nothing), every membership held in it and
// the role a group of the whole application holds there, its group, its custom role and
// its invitation that stands. cara, who holds a role in beta, and evan, whom beta's
// directory manages, keep their accounts; another tenant's rows are not counted.
func TestManager_TenantKinds_counts(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, alphaStore(), engineRoles)
	var got map[string]int64
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		got, err = kindCounts(ctx, txn, m.TenantKinds("alpha"))

		return err
	}); err != nil {
		t.Fatalf("inTransaction() error = %v", err)
	}
	want := map[string]int64{"accounts": 3, "roles held": 5, "groups": 1, "custom roles": 1, "invitations": 1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("counts (-want +got):\n%s", diff)
	}

	var words []resource.DeleteWords
	for _, kind := range m.TenantKinds("alpha") {
		words = append(words, kind.Words())
	}
	wantWords := []resource.DeleteWords{
		{One: "account", Many: "accounts"},
		{One: "role held", Many: "roles held"},
		{One: "group", Many: "groups"},
		{One: "custom role", Many: "custom roles"},
		{One: "invitation", Many: "invitations"},
	}
	if diff := cmp.Diff(wantWords, words); diff != "" {
		t.Errorf("the kinds' words, in the order returned (-want +got):\n%s", diff)
	}
}

// TestManager_TenantKinds_takeEverything pins a round with room for everything: the
// three accounts go whole, each with what it held in the tenant, its identity links and
// its sessions ended; cara's and evan's accounts stay, their memberships in alpha going
// with the roles held. A membership an account took is found and deleted again with the
// roles held, as every delete in a save finds the rows the database held before it. The
// group goes with everything under it, then the custom roles of alpha and its
// invitation. The permission engine is told once, after the commit.
func TestManager_TenantKinds_takeEverything(t *testing.T) {
	t.Parallel()

	store := alphaStore()
	store.costs = TenantDeleteCosts{Membership: 1, Group: 1, GroupMember: 1, GroupRole: 1, Role: 1, Identity: 1, User: 1, Invitation: 3}
	store.withUser = map[impulse.UUID]int{doraID: 2}
	m, engine := newManager(t, store, engineRoles)
	var got []takenRows
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		for _, kind := range m.TenantKinds("alpha") {
			rows, mutations, more, err := kind.Take(ctx, txn, resource.DeleteMutationLimit)
			if err != nil {
				return errors.Wrap(err, "resource.TakenKind.Take()")
			}
			got = append(got, takenRows{Rows: rows, Mutations: mutations, More: more})
		}
		if engine.changed != 0 {
			t.Errorf("the engine was told %d time(s) before the commit", engine.changed)
		}

		return nil
	}); err != nil {
		t.Fatalf("Take() error = %v", err)
	}
	want := []takenRows{
		// anna: a membership and the user (2); ben: a membership, a place and the user
		// (3); dora: a link, the user and what the database removes with it (4).
		{Rows: 3, Mutations: 9},
		{Rows: 5, Mutations: 5},
		// The group, its two members and its role.
		{Rows: 1, Mutations: 4},
		{Rows: 1, Mutations: 1},
		{Rows: 1, Mutations: 3},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Take() by kind (-want +got):\n%s", diff)
	}
	wantWrites := []string{
		"delete membership " + membershipText(&annaAlpha),
		"end session " + annaLogin.String(),
		"delete user " + annaID.String(),
		"delete membership " + membershipText(&benAlpha),
		"delete group member " + nightID.String() + " " + benID.String(),
		"delete user " + benID.String(),
		"end session " + doraLogin.String(),
		"delete identity " + doraLink.String(),
		"delete user " + doraID.String(),
		"delete membership " + membershipText(&annaAlpha),
		"delete membership " + membershipText(&benAlpha),
		"delete membership " + membershipText(&caraAlpha),
		"delete membership " + membershipText(&evanAlpha),
		"delete group role " + groupRoleText(&staffInAlpha),
		"delete group " + nightID.String() + " with everything under it",
		"delete the custom roles of alpha",
		"consume mail link " + invited.String(),
	}
	if diff := cmp.Diff(wantWrites, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
	}
}

// TestManager_TenantKinds_takeInRounds pins the accounts kind in a save with room for one
// account: it takes the first whole and says accounts are left. The next save, once the
// first has committed, counts the two left and takes the next.
func TestManager_TenantKinds_takeInRounds(t *testing.T) {
	t.Parallel()

	costs := TenantDeleteCosts{Membership: 1, GroupMember: 1, Identity: 1, User: 1}
	// anna costs her membership, her user and the two rows the database removes with it.
	first := alphaStore()
	first.costs = costs
	first.withUser = map[impulse.UUID]int{annaID: 2}
	m, _ := newManager(t, first, engineRoles)
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		accounts := m.TenantKinds("alpha")[0]
		rows, mutations, more, err := accounts.Take(ctx, txn, 4)
		if err != nil {
			return errors.Wrap(err, "resource.TakenKind.Take()")
		}
		if got, want := (takenRows{Rows: rows, Mutations: mutations, More: more}), (takenRows{Rows: 1, Mutations: 4, More: true}); got != want {
			t.Errorf("the first save's Take() = %+v, want %+v", got, want)
		}

		return nil
	}); err != nil {
		t.Fatalf("the first save: %v", err)
	}
	wantFirst := []string{
		"delete membership " + membershipText(&annaAlpha),
		"end session " + annaLogin.String(),
		"delete user " + annaID.String(),
	}
	if diff := cmp.Diff(wantFirst, first.writes); diff != "" {
		t.Errorf("the first save's writes (-want +got):\n%s", diff)
	}

	// ben costs his membership, his place and his user: 3, and dora 2 more than is left.
	next := alphaStore(annaID)
	next.costs = costs
	m, _ = newManager(t, next, engineRoles)
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		accounts := m.TenantKinds("alpha")[0]
		count, err := accounts.Count(ctx, txn)
		if err != nil {
			return errors.Wrap(err, "resource.TakenKind.Count()")
		}
		if count != 2 {
			t.Errorf("the next save's Count() = %d, want the 2 accounts left", count)
		}
		rows, mutations, more, err := accounts.Take(ctx, txn, 4)
		if err != nil {
			return errors.Wrap(err, "resource.TakenKind.Take()")
		}
		if got, want := (takenRows{Rows: rows, Mutations: mutations, More: more}), (takenRows{Rows: 1, Mutations: 3, More: true}); got != want {
			t.Errorf("the next save's Take() = %+v, want %+v", got, want)
		}

		return nil
	}); err != nil {
		t.Fatalf("the next save: %v", err)
	}
	wantNext := []string{
		"delete membership " + membershipText(&benAlpha),
		"delete group member " + nightID.String() + " " + benID.String(),
		"delete user " + benID.String(),
	}
	if diff := cmp.Diff(wantNext, next.writes); diff != "" {
		t.Errorf("the next save's writes (-want +got):\n%s", diff)
	}
}

// TestManager_TenantKinds_otherKindsInRounds pins the other kinds where the save has less
// room than they need: each takes what fits in key order and says rows are left, the
// custom roles go together or not at all, and a group its own delete could not take in
// one save leaves the kind with rows.
func TestManager_TenantKinds_otherKindsInRounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		kind       int
		budget     int
		groupLeft  *resource.DeleteLeft
		want       takenRows
		wantWrites []string
	}{
		{
			name: "roles held: two of five", kind: 1, budget: 2,
			want: takenRows{Rows: 2, Mutations: 2, More: true},
			wantWrites: []string{
				"delete membership " + membershipText(&annaAlpha),
				"delete membership " + membershipText(&benAlpha),
			},
		},
		{
			name: "groups: no room for the group with its members and its role", kind: 2, budget: 3,
			want: takenRows{More: true},
		},
		{
			name: "groups: the group's own delete left rows under it", kind: 2, budget: 4,
			groupLeft:  &resource.DeleteLeft{Deleted: 2, Left: 1},
			want:       takenRows{Mutations: 4, More: true},
			wantWrites: []string{"delete group " + nightID.String() + " with everything under it"},
		},
		{
			name: "custom roles: no room", kind: 3, budget: 0,
			want: takenRows{More: true},
		},
		{
			name: "invitations: no room for one", kind: 4, budget: 2,
			want: takenRows{More: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := alphaStore()
			store.costs = TenantDeleteCosts{Membership: 1, Group: 1, GroupMember: 1, GroupRole: 1, Role: 1, Invitation: 3}
			store.groupLeft = tt.groupLeft
			m, _ := newManager(t, store, engineRoles)
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				rows, mutations, more, err := m.TenantKinds("alpha")[tt.kind].Take(ctx, txn, tt.budget)
				if err != nil {
					return errors.Wrap(err, "resource.TakenKind.Take()")
				}
				if got := (takenRows{Rows: rows, Mutations: mutations, More: more}); got != tt.want {
					t.Errorf("Take() = %+v, want %+v", got, tt.want)
				}

				return nil
			}); err != nil {
				t.Fatalf("Take() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_TenantKinds_theImpersonatorsOwnAccount holds the tenant's delete to the rule
// that nobody is the subject of what they do in a session that acts as someone else. anna
// holds her roles in alpha alone, so the delete would delete her account: while she acts
// as someone else, the accounts kind refuses before anything is buffered, in the rule's
// sentence, both where the delete asks first and where it takes. A session started by evan,
// whose account stays, is refused nothing.
func TestManager_TenantKinds_theImpersonatorsOwnAccount(t *testing.T) {
	t.Parallel()

	const refusal = "You are acting as someone else, so you cannot delete the only tenant your own account holds a role in."
	for _, tt := range []struct {
		name        string
		actor       impulse.UUID
		wantRefused bool
	}{
		{name: "the person who started the session holds roles in the tenant alone: refused", actor: annaID, wantRefused: true},
		{name: "the person who started the session keeps their account: not refused", actor: evanID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := alphaStore()
			m, _ := newManager(t, store, engineRoles)
			var checked, taken error
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = viewedBy(ctx, tt.actor)
				accounts := m.TenantKinds("alpha")[0]
				checked = accounts.Check(ctx, txn)
				if tt.wantRefused {
					_, _, _, taken = accounts.Take(ctx, txn, resource.DeleteMutationLimit)
				}

				return resource.ErrDryRun
			})
			if !errors.Is(err, resource.ErrDryRun) {
				t.Fatalf("inTransaction() error = %v, want the dry run's", err)
			}
			if !tt.wantRefused {
				if checked != nil {
					t.Fatalf("Check() error = %v, want none", checked)
				}

				return
			}
			for call, err := range map[string]error{"Check()": checked, "Take()": taken} {
				if !httpio.HasForbidden(err) || httpio.Message(err) != refusal {
					t.Errorf("%s error = %v, want the refusal %q", call, err, refusal)
				}
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none", store.writes)
			}
		})
	}
}

// TestManager_TenantKinds_nothingHeld pins a tenant the auth holds nothing for: every count
// is zero, every kind takes nothing and says nothing is left, and the engine is not told.
// A delete that names no tenant is refused.
func TestManager_TenantKinds_nothingHeld(t *testing.T) {
	t.Parallel()

	store := alphaStore()
	store.costs = TenantDeleteCosts{Membership: 1, Group: 1, GroupMember: 1, GroupRole: 1, Role: 1, Identity: 1, User: 1, Invitation: 1}
	m, engine := newManager(t, store, engineRoles)
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		kinds := m.TenantKinds("gamma")
		want := map[string]int64{"accounts": 0, "roles held": 0, "groups": 0, "custom roles": 0, "invitations": 0}
		got, err := kindCounts(ctx, txn, kinds)
		if err != nil {
			return err
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("counts (-want +got):\n%s", diff)
		}
		for _, kind := range kinds {
			rows, mutations, more, err := kind.Take(ctx, txn, resource.DeleteMutationLimit)
			if err != nil {
				return errors.Wrap(err, "resource.TakenKind.Take()")
			}
			if got := (takenRows{Rows: rows, Mutations: mutations, More: more}); got != (takenRows{}) {
				t.Errorf("%s: Take() = %+v, want nothing", kind.Words().Many, got)
			}
		}
		for _, kind := range m.TenantKinds("") {
			if _, err := kind.Count(ctx, txn); err == nil {
				t.Errorf("%s: Count() of no tenant error = nil, want a refusal", kind.Words().Many)
			}
		}

		return nil
	}); err != nil {
		t.Fatalf("inTransaction() error = %v", err)
	}
	if len(store.writes) != 0 {
		t.Errorf("buffered writes = %v, want none", store.writes)
	}
	if engine.changed != 0 {
		t.Errorf("the engine was told the policy changed %d time(s), want never", engine.changed)
	}
}
