package storetest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// The fixtures of the custom role holdings phase, kept apart from the other phases':
// two tenants of their own, the custom role the holdings name, the people who hold it
// and a group kept for the whole application. The phase removes what it writes, so the
// policy read after it finds what the phases before it left.
var (
	wardTenant  = accesstypes.DomainPolicyScope("wards")
	otherTenant = accesstypes.DomainPolicyScope("wards-other")
)

const (
	warden      = accesstypes.Role("Warden")
	wardenUpper = accesstypes.Role("WARDEN")

	lou = accesstypes.User("lou")

	wardGroup   = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-000000000008")
	absentGroup = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-000000000009")

	wardenDescription = "Keeps the wards for the custom holdings tests."
	holdRound         = 16
)

// runCustomHoldings holds the two inserts that give a custom role to their contract: a
// holding is written only while the roles table holds the row it stands on (the
// scope's own, or every domain's for one domain), under exactly the role's spelling;
// where none is held nothing is written and the refusal wraps policy.ErrNoRole; and a
// holding given at the moment its role is deleted ends one of two ways, the holding
// written and the delete refused, or the role gone and nothing written.
func runCustomHoldings(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()
	p := newPeriods()

	ward := &policy.GroupRow{Group: policy.Group{ID: wardGroup, Scope: globalScope}, Name: "Wards", Description: "Who keeps the wards."}
	if taken, err := store.InsertGroup(ctx, ward); err != nil || taken {
		t.Fatalf("InsertGroup(%s) = (%v, %v), want it written", ward.Name, taken, err)
	}

	t.Run("a person's custom role is given where its row is held", func(t *testing.T) { customGiven(t, store, &p) })
	t.Run("a person's custom role is not given where no row of it is held", func(t *testing.T) { customRefused(t, store) })
	t.Run("a group's custom role", func(t *testing.T) { customGroupRoles(t, store) })
	t.Run("a role given and deleted at the same moment", func(t *testing.T) { customTogether(t, store) })
	t.Run("a role held in every domain and in one domain", func(t *testing.T) { heldInDomain(t, store, &p) })

	if deleted, err := store.DeleteGroup(ctx, wardGroup); err != nil || !deleted {
		t.Fatalf("DeleteGroup() of the phase's group = (%v, %v), want it deleted", deleted, err)
	}
}

// customGiven holds InsertCustomUserRole to what InsertUserRole writes where the role's
// row is held: the period and the reason, both replaced when the role is given again;
// and to the rows a holding stands on: the scope's own, and every domain's for one
// domain.
func customGiven(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()

	insertRole(t, store, wardTenant, warden)
	for _, step := range []struct {
		period accesstypes.Period
		reason string
	}{
		{period: p.current, reason: "Keeps the keys"},
		{period: p.upcoming, reason: "Covers the night"},
	} {
		if err := store.InsertCustomUserRole(ctx, wardTenant, lou, warden, step.period, step.reason); err != nil {
			t.Fatalf("InsertCustomUserRole() error = %v", err)
		}
		got, err := store.ListUserAssignments(ctx, lou)
		if err != nil {
			t.Fatalf("ListUserAssignments() error = %v", err)
		}
		want := []policy.Assignment{{
			Membership: policy.Membership{Scope: wardTenant, Member: policy.Subject{Kind: policy.SubjectUser, Name: string(lou)}, Role: warden, Period: step.period},
			Reason:     step.reason,
		}}
		if diff := cmp.Diff(want, got, equateScopes); diff != "" {
			t.Errorf("ListUserAssignments() after InsertCustomUserRole(%q) mismatch (-want +got):\n%s", step.reason, diff)
		}
	}
	removeUserRole(t, store, wardTenant, warden)
	deleteRoles(t, store, policy.Role{Scope: wardTenant, Name: warden})

	for _, tc := range []struct {
		name  string
		row   accesstypes.PolicyScope
		scope accesstypes.PolicyScope
	}{
		{name: "a row held in every domain gives the role in one domain", row: everyDomain, scope: wardTenant},
		{name: "a row held in every domain gives the role in every domain", row: everyDomain, scope: everyDomain},
		{name: "a row held in the global partition gives the role there", row: globalScope, scope: globalScope},
	} {
		insertRole(t, store, tc.row, warden)
		if err := store.InsertCustomUserRole(ctx, tc.scope, lou, warden, accesstypes.Period{}, ""); err != nil {
			t.Errorf("%s: InsertCustomUserRole(%s) error = %v", tc.name, tc.scope, err)
		}
		if roles, err := store.ListUserRoles(ctx, tc.scope, lou); err != nil || !slices.Equal(roles, []accesstypes.Role{warden}) {
			t.Errorf("%s: ListUserRoles(%s) = (%v, %v), want [%s]", tc.name, tc.scope, roles, err, warden)
		}
		removeUserRole(t, store, tc.scope, warden)
		deleteRoles(t, store, policy.Role{Scope: tc.row, Name: warden})
	}
}

// customRefused holds InsertCustomUserRole to its refusal: where no row the holding
// stands on is held, under exactly the role's spelling, nothing is written and the
// error wraps policy.ErrNoRole.
func customRefused(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	for _, tc := range []struct {
		name  string
		rows  []policy.Role
		scope accesstypes.PolicyScope
	}{
		{name: "no row anywhere", scope: wardTenant},
		{name: "a row in another domain alone", rows: []policy.Role{{Scope: otherTenant, Name: warden}}, scope: wardTenant},
		{name: "a row in one domain and the holding in every domain", rows: []policy.Role{{Scope: wardTenant, Name: warden}}, scope: everyDomain},
		{name: "a row in the global partition and the holding in one domain", rows: []policy.Role{{Scope: globalScope, Name: warden}}, scope: wardTenant},
		{name: "a row in one domain and the holding in the global partition", rows: []policy.Role{{Scope: wardTenant, Name: warden}}, scope: globalScope},
		{name: "a row of the name in another case", rows: []policy.Role{{Scope: wardTenant, Name: wardenUpper}, {Scope: everyDomain, Name: wardenUpper}}, scope: wardTenant},
	} {
		for _, row := range tc.rows {
			insertRole(t, store, row.Scope, row.Name)
		}
		err := store.InsertCustomUserRole(ctx, tc.scope, lou, warden, accesstypes.Period{}, "")
		if !errors.Is(err, policy.ErrNoRole) {
			t.Errorf("%s: InsertCustomUserRole(%s) error = %v, want policy.ErrNoRole", tc.name, tc.scope, err)
		}
		if got, err := store.ListUserAssignments(ctx, lou); err != nil || len(got) != 0 {
			t.Errorf("%s: ListUserAssignments() = (%v, %v), want nothing written", tc.name, got, err)
		}
		removeUserRole(t, store, tc.scope, warden)
		deleteRoles(t, store, tc.rows...)
	}

	insertRole(t, store, wardTenant, warden)
	deleteRoles(t, store, policy.Role{Scope: wardTenant, Name: warden})
	if err := store.InsertCustomUserRole(ctx, wardTenant, lou, warden, accesstypes.Period{}, ""); !errors.Is(err, policy.ErrNoRole) {
		t.Errorf("InsertCustomUserRole() of a role deleted before it: error = %v, want policy.ErrNoRole", err)
	}
	if got, err := store.ListUserAssignments(ctx, lou); err != nil || len(got) != 0 {
		t.Errorf("ListUserAssignments() after the role was deleted and given = (%v, %v), want nothing written", got, err)
	}
	removeUserRole(t, store, wardTenant, warden)
}

// customGroupRoles holds InsertCustomGroupRole to the rule InsertCustomUserRole keeps:
// written where the role's row is held, the every-domain row for one domain among
// them, refused with policy.ErrNoRole where it is not, and refused for a group that is
// not there.
func customGroupRoles(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	if err := store.InsertCustomGroupRole(ctx, wardGroup, wardTenant, warden, accesstypes.Period{}); !errors.Is(err, policy.ErrNoRole) {
		t.Errorf("InsertCustomGroupRole() with no row of the role: error = %v, want policy.ErrNoRole", err)
	}
	if roles := groupRolesOf(t, store, wardGroup); len(roles) != 0 {
		t.Errorf("ListGroupRoles() after the refusal = %v, want nothing written", roles)
	}

	for _, tc := range []struct {
		name string
		row  accesstypes.PolicyScope
	}{
		{name: "a row held in the scope", row: wardTenant},
		{name: "a row held in every domain", row: everyDomain},
	} {
		insertRole(t, store, tc.row, warden)
		if err := store.InsertCustomGroupRole(ctx, wardGroup, wardTenant, warden, accesstypes.Period{}); err != nil {
			t.Errorf("%s: InsertCustomGroupRole() error = %v", tc.name, err)
		}
		want := []policy.Membership{{Scope: wardTenant, Member: policy.Subject{Kind: policy.SubjectGroup, Name: string(wardGroup)}, Role: warden}}
		if diff := cmp.Diff(want, groupRolesOf(t, store, wardGroup), equateScopes); diff != "" {
			t.Errorf("%s: ListGroupRoles() mismatch (-want +got):\n%s", tc.name, diff)
		}
		removeGroupRole(t, store, wardTenant, warden)
		deleteRoles(t, store, policy.Role{Scope: tc.row, Name: warden})
	}

	if err := store.InsertCustomGroupRole(ctx, absentGroup, wardTenant, warden, accesstypes.Period{}); !errors.Is(err, policy.ErrNoRole) {
		t.Errorf("InsertCustomGroupRole() with neither the role nor the group there: error = %v, want policy.ErrNoRole", err)
	}
	insertRole(t, store, wardTenant, warden)
	if err := store.InsertCustomGroupRole(ctx, absentGroup, wardTenant, warden, accesstypes.Period{}); !errors.Is(err, policy.ErrNoGroup) {
		t.Errorf("InsertCustomGroupRole() for a group that is not there: error = %v, want policy.ErrNoGroup", err)
	}
	if groups, err := store.ListRoleGroups(ctx, wardTenant, warden); err != nil || len(groups) != 0 {
		t.Errorf("ListRoleGroups() after the refusal = (%v, %v), want none", groups, err)
	}
	deleteRoles(t, store, policy.Role{Scope: wardTenant, Name: warden})
}

// customTogether gives a custom role, to a person and to a group, from one goroutine
// while another deletes the role's row, released together, sixteen rounds each.
func customTogether(t *testing.T, store access.Store) {
	t.Helper()

	t.Run("a person's role", func(t *testing.T) {
		for round := range holdRound {
			role := accesstypes.Role(fmt.Sprintf("Warden%02d", round))
			giveTogether(t, store, &together{
				row: policy.Role{Scope: wardTenant, Name: role},
				give: func(ctx context.Context) error {
					return store.InsertCustomUserRole(ctx, wardTenant, lou, role, accesstypes.Period{}, "")
				},
				holds:   func(t *testing.T) bool { return userHolds(t, store, wardTenant, role) },
				remove:  func(t *testing.T) { removeUserRole(t, store, wardTenant, role) },
				refusal: policy.ErrStillAssigned,
			})
		}
	})
	t.Run("a group's role", func(t *testing.T) {
		for round := range holdRound {
			role := accesstypes.Role(fmt.Sprintf("Warden%02d", round))
			giveTogether(t, store, &together{
				row: policy.Role{Scope: wardTenant, Name: role},
				give: func(ctx context.Context) error {
					return store.InsertCustomGroupRole(ctx, wardGroup, wardTenant, role, accesstypes.Period{})
				},
				holds:   func(t *testing.T) bool { return groupHolds(t, store, wardTenant, role) },
				remove:  func(t *testing.T) { removeGroupRole(t, store, wardTenant, role) },
				refusal: policy.ErrHeldByGroup,
			})
		}
	})
}

// heldInDomain holds DeleteRole of a role held in every domain to its refusal while a
// person or a group holds the role in one domain whose holding reads the every-domain
// row: the refusal wraps policy.ErrHeldInDomain, names the lowest such domain and
// deletes nothing; a holding that has not begun refuses as one that counts now; the
// ended ones go with the role; and a domain that holds a role of the name of its own
// neither refuses the delete nor loses what it holds. Of a holding given in one domain
// at the moment the every-domain row is deleted, one of the two stands.
func heldInDomain(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()
	everyWarden := policy.Role{Scope: everyDomain, Name: warden}

	for _, tc := range []struct {
		name   string
		give   func() error
		remove func(t *testing.T)
		holder string
	}{
		{
			name: "a person holds it in two domains now",
			give: func() error {
				if err := store.InsertCustomUserRole(ctx, otherTenant, lou, warden, accesstypes.Period{}, ""); err != nil {
					return errors.Wrapf(err, "access.Store.InsertCustomUserRole() in %s", otherTenant)
				}

				return store.InsertCustomUserRole(ctx, wardTenant, lou, warden, accesstypes.Period{}, "")
			},
			remove: func(t *testing.T) {
				removeUserRole(t, store, otherTenant, warden)
				removeUserRole(t, store, wardTenant, warden)
			},
			holder: `a person holds it in the domain "wards"`,
		},
		{
			name:   "a person holds it in one domain for a period that has not begun",
			give:   func() error { return store.InsertCustomUserRole(ctx, wardTenant, lou, warden, p.upcoming, "") },
			remove: func(t *testing.T) { removeUserRole(t, store, wardTenant, warden) },
			holder: `a person holds it in the domain "wards"`,
		},
		{
			name: "a group kept for the whole application holds it in one domain",
			give: func() error {
				return store.InsertCustomGroupRole(ctx, wardGroup, wardTenant, warden, accesstypes.Period{})
			},
			remove: func(t *testing.T) { removeGroupRole(t, store, wardTenant, warden) },
			holder: `a group holds it in the domain "wards"`,
		},
	} {
		insertRole(t, store, everyDomain, warden)
		if err := tc.give(); err != nil {
			t.Fatalf("%s: giving the role error = %v", tc.name, err)
		}
		deleted, err := store.DeleteRole(ctx, everyDomain, warden)
		if deleted || !errors.Is(err, policy.ErrHeldInDomain) {
			t.Errorf("%s: DeleteRole(every domain) = (%v, %v), want it refused with policy.ErrHeldInDomain", tc.name, deleted, err)
		} else if !strings.Contains(err.Error(), tc.holder) {
			t.Errorf("%s: DeleteRole(every domain) error = %v, want it to say %s", tc.name, err, tc.holder)
		}
		if !roleExists(t, store, everyDomain, warden) {
			t.Errorf("%s: the every-domain role is gone after the refusal, want it there", tc.name)
		}
		tc.remove(t)
		if deleted, err := store.DeleteRole(ctx, everyDomain, warden); err != nil || !deleted {
			t.Errorf("%s: DeleteRole(every domain) once the holding was taken away = (%v, %v), want (true, nil)", tc.name, deleted, err)
		}
	}

	// The ended holdings in a domain that reads the every-domain row go with it.
	insertRole(t, store, everyDomain, warden)
	if err := store.InsertCustomUserRole(ctx, wardTenant, lou, warden, p.ended, ""); err != nil {
		t.Fatalf("InsertCustomUserRole(ended) error = %v", err)
	}
	if err := store.InsertCustomGroupRole(ctx, wardGroup, wardTenant, warden, p.ended); err != nil {
		t.Fatalf("InsertCustomGroupRole(ended) error = %v", err)
	}
	if deleted, err := store.DeleteRole(ctx, everyDomain, warden); err != nil || !deleted {
		t.Errorf("DeleteRole(every domain) over ended holdings in one domain = (%v, %v), want (true, nil)", deleted, err)
	}
	if held := heldIn(t, store, wardTenant, warden); len(held) != 0 {
		t.Errorf("ReadPolicy() after the every-domain role was deleted holds %v in %s, want nothing: the ended holdings go with the role", held, wardTenant)
	}

	ownRowStandsApart(t, store, p)

	for round := range holdRound {
		role := accesstypes.Role(fmt.Sprintf("Warden%02d", round))
		giveTogether(t, store, &together{
			row: policy.Role{Scope: everyDomain, Name: role},
			give: func(ctx context.Context) error {
				return store.InsertCustomUserRole(ctx, wardTenant, lou, role, accesstypes.Period{}, "")
			},
			holds:   func(t *testing.T) bool { return userHolds(t, store, wardTenant, role) },
			remove:  func(t *testing.T) { removeUserRole(t, store, wardTenant, role) },
			refusal: policy.ErrHeldInDomain,
		})
	}
	deleteRoles(t, store, everyWarden)
}

// ownRowStandsApart pins that a domain holding a role of the name of its own neither
// refuses the deletion of the every-domain row nor loses what it holds: its holdings,
// lou's and the group's ended one, read the domain's own row, which DeleteRole of the
// domain then refuses while lou holds it.
func ownRowStandsApart(t *testing.T, store access.Store, p *periods) {
	t.Helper()
	ctx := t.Context()

	insertRole(t, store, everyDomain, warden)
	insertRole(t, store, wardTenant, warden)
	if err := store.InsertCustomUserRole(ctx, wardTenant, lou, warden, accesstypes.Period{}, ""); err != nil {
		t.Fatalf("InsertCustomUserRole() error = %v", err)
	}
	if err := store.InsertCustomGroupRole(ctx, wardGroup, wardTenant, warden, p.ended); err != nil {
		t.Fatalf("InsertCustomGroupRole(ended) error = %v", err)
	}
	if deleted, err := store.DeleteRole(ctx, everyDomain, warden); err != nil || !deleted {
		t.Errorf("DeleteRole(every domain) with the domain's own role of the name = (%v, %v), want (true, nil)", deleted, err)
	}
	if !userHolds(t, store, wardTenant, warden) {
		t.Errorf("lou does not hold %s in %s after the every-domain role was deleted, want the holding of the domain's own role kept", warden, wardTenant)
	}
	if held := heldIn(t, store, wardTenant, warden); len(held) != 2 {
		t.Errorf("ReadPolicy() after the every-domain role was deleted holds %v in %s, want lou's and the group's ended one: they read the domain's own row", held, wardTenant)
	}
	if deleted, err := store.DeleteRole(ctx, wardTenant, warden); deleted || !errors.Is(err, policy.ErrStillAssigned) {
		t.Errorf("DeleteRole(%s) of the domain's own role lou holds = (%v, %v), want it refused with policy.ErrStillAssigned", wardTenant, deleted, err)
	}
	removeUserRole(t, store, wardTenant, warden)
	removeGroupRole(t, store, wardTenant, warden)
	deleteRoles(t, store, policy.Role{Scope: wardTenant, Name: warden})
}

// heldIn lists, from the policy read, every holding of role in scope, a person's or a
// group's, ended or not.
func heldIn(t *testing.T, store access.Store, scope accesstypes.PolicyScope, role accesstypes.Role) []policy.Membership {
	t.Helper()
	records, err := store.ReadPolicy(t.Context())
	if err != nil {
		t.Fatalf("ReadPolicy() error = %v", err)
	}
	var held []policy.Membership
	for i := range records.Memberships {
		if m := &records.Memberships[i]; m.Scope == scope && m.Role == role {
			held = append(held, *m)
		}
	}

	return held
}

// together is one round of a custom role given at the moment the role row the holding
// stands on is deleted.
type together struct {
	// row is the role row that is made and then deleted beside the give.
	row policy.Role
	// give writes the holding.
	give func(ctx context.Context) error
	// holds reports whether the holding is there, and remove takes it away.
	holds  func(t *testing.T) bool
	remove func(t *testing.T)
	// refusal is what the delete wraps where the holding was written before it.
	refusal error
}

// giveTogether makes the round's role row, then gives the holding and deletes the row
// from two goroutines released together, and checks that the two ended one of two
// ways: the holding written, the delete refused and the row there; or the row deleted,
// the give refused with policy.ErrNoRole and no holding written. It removes what the
// round left.
func giveTogether(t *testing.T, store access.Store, round *together) {
	t.Helper()
	ctx := t.Context()
	insertRole(t, store, round.row.Scope, round.row.Name)

	var (
		giveErr, deleteErr error
		deleted            bool
	)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		giveErr = round.give(ctx)
	})
	wg.Go(func() {
		<-start
		deleted, deleteErr = store.DeleteRole(ctx, round.row.Scope, round.row.Name)
	})
	close(start)
	wg.Wait()

	held := round.holds(t)
	there := roleExists(t, store, round.row.Scope, round.row.Name)
	switch {
	case giveErr == nil:
		if deleted || !errors.Is(deleteErr, round.refusal) {
			t.Errorf("%v: the holding was written and DeleteRole() = (%v, %v), want it refused with %v", round.row, deleted, deleteErr, round.refusal)
		}
		if !held || !there {
			t.Errorf("%v: the give answered nil and the delete refused: the holding is there %v and the role %v, want both", round.row, held, there)
		}
	case errors.Is(giveErr, policy.ErrNoRole):
		if !deleted || deleteErr != nil {
			t.Errorf("%v: the give found the role gone and DeleteRole() = (%v, %v), want (true, nil)", round.row, deleted, deleteErr)
		}
		if held || there {
			t.Errorf("%v: the role was deleted and the give refused: the holding is there %v and the role %v, want neither", round.row, held, there)
		}
	default:
		t.Errorf("%v: the give answered %v (DeleteRole() = (%v, %v)), want nil or policy.ErrNoRole", round.row, giveErr, deleted, deleteErr)
	}
	round.remove(t)
	deleteRoles(t, store, round.row)
}

// insertRole makes the custom role in scope for the phase.
func insertRole(t *testing.T, store access.Store, scope accesstypes.PolicyScope, role accesstypes.Role) {
	t.Helper()
	if err := store.InsertRole(t.Context(), scope, role, wardenDescription); err != nil {
		t.Fatalf("InsertRole(%s, %q) error = %v", scope, role, err)
	}
}

// removeUserRole takes lou's membership of role in scope away; one that is not there is
// no failure.
func removeUserRole(t *testing.T, store access.Store, scope accesstypes.PolicyScope, role accesstypes.Role) {
	t.Helper()
	if err := store.DeleteUserRole(context.WithoutCancel(t.Context()), scope, lou, role); err != nil {
		t.Errorf("DeleteUserRole(%s, %q) error = %v", scope, role, err)
	}
}

// removeGroupRole takes the ward group's role in scope away; one it does not hold is no
// failure.
func removeGroupRole(t *testing.T, store access.Store, scope accesstypes.PolicyScope, role accesstypes.Role) {
	t.Helper()
	if err := store.DeleteGroupRole(context.WithoutCancel(t.Context()), wardGroup, scope, role); err != nil {
		t.Errorf("DeleteGroupRole(%s, %q) error = %v", scope, role, err)
	}
}

// userHolds reports whether lou holds role in scope now.
func userHolds(t *testing.T, store access.Store, scope accesstypes.PolicyScope, role accesstypes.Role) bool {
	t.Helper()
	roles, err := store.ListUserRoles(t.Context(), scope, lou)
	if err != nil {
		t.Fatalf("ListUserRoles(%s) error = %v", scope, err)
	}

	return slices.Contains(roles, role)
}

// groupHolds reports whether the ward group holds role in scope now.
func groupHolds(t *testing.T, store access.Store, scope accesstypes.PolicyScope, role accesstypes.Role) bool {
	t.Helper()

	return slices.ContainsFunc(groupRolesOf(t, store, wardGroup), func(m policy.Membership) bool {
		return m.Scope == scope && m.Role == role
	})
}

// groupRolesOf lists the roles group holds now.
func groupRolesOf(t *testing.T, store access.Store, group accesstypes.Group) []policy.Membership {
	t.Helper()
	roles, err := store.ListGroupRoles(t.Context(), group)
	if err != nil {
		t.Fatalf("ListGroupRoles(%s) error = %v", group, err)
	}

	return roles
}
