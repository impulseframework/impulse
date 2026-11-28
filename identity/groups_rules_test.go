package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// auditID is a second group kept for the whole application, beside the staff.
var auditID = mustUUID("0190b000-0000-7000-8000-000000000004")

// TestTenant_AddGroupMember_takesAPersonOfTheTenant pins who a tenant puts in its
// groups: the people it already has, the rows of its own list of the auth's people. An
// account from outside the tenant is not found there, whatever it holds elsewhere, so a
// tenant's administrator pulls nobody into the tenant through a group, and the answer
// never says whether an id belongs to somebody of another tenant.
func TestTenant_AddGroupMember_takesAPersonOfTheTenant(t *testing.T) {
	t.Parallel()

	_, _, current := periodFixtures()
	tests := []struct {
		name string
		opts []Option
		user impulse.UUID
		// elsewhere are the memberships the person holds outside the tenant.
		elsewhere  []access.MembershipKey
		failOn     string
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name: "one of the tenant's people is put in its group", user: userID,
			wantWrites: []string{
				"insert group member " + nightID.String() + " " + userID.String(),
				"period of group member " + nightID.String() + " " + userID.String() + ": " + periodText(current),
			},
		},
		{name: "an account that holds nothing in the tenant is not found there", user: otherUser, wantErr: httpio.HasNotFound},
		{
			name: "an account another tenant holds is not found there either, where a person belongs to one tenant",
			opts: []Option{WithOneTenantPerPerson()}, user: otherUser,
			elsewhere: []access.MembershipKey{access.NewMembershipKey(betaScope, erin, "Reader")},
			wantErr:   httpio.HasNotFound,
		},
		{name: "an id no account holds is not found there", user: frankID, wantErr: httpio.HasNotFound},
		{name: "a list that cannot be read fails the change", user: userID, failOn: "TenantPerson", wantErr: func(err error) bool { return errors.Is(err, errStore) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			store.tenantPeople = map[accesstypes.Domain][]impulse.UUID{"alpha": {userID}}
			store.failOn = tt.failOn
			for _, key := range tt.elsewhere {
				store.memberships[key] = true
			}
			m, _ := newManager(t, store, engineRoles, tt.opts...)
			tenant := &Tenant{manager: m, domain: "alpha"}

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tenant.AddGroupMemberFor(ctx, txn, nightID, tt.user, current)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("Tenant.AddGroupMemberFor() error = %v, want the refusal", err)
				}
				if httpio.HasNotFound(err) && !strings.Contains(err.Error(), "does not exist") {
					t.Errorf("Tenant.AddGroupMemberFor() error = %v, want the words of a person who is not there", err)
				}
			} else if err != nil {
				t.Fatalf("Tenant.AddGroupMemberFor() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_RemoveGroupMember_severalTenants pins that a person's removal from a group
// is held to the rule of one tenant per person as a role's removal is: a place in a
// group kept for the whole application is a role held outside the tenants, and the last
// one stays while the person holds roles in more than one tenant.
func TestManager_RemoveGroupMember_severalTenants(t *testing.T) {
	t.Parallel()

	inAlpha := access.NewMembershipKey(alphaScope, dana, "Reader")
	inBeta := access.NewMembershipKey(betaScope, dana, "Reader")
	removal := "delete group member " + staffID.String() + " " + userID.String()
	tests := []struct {
		name       string
		opts       []Option
		held       []access.MembershipKey
		in         []impulse.UUID
		wantErr    func(error) bool
		wantWrites []string
	}{
		{
			name: "the last thing held outside the tenants stays while the person holds roles in two",
			opts: []Option{WithOneTenantPerPerson()}, held: []access.MembershipKey{inAlpha, inBeta}, in: []impulse.UUID{staffID},
			wantErr: httpio.HasConflict,
		},
		{
			name: "a place in another group of the whole application is held outside the tenants still",
			opts: []Option{WithOneTenantPerPerson()}, held: []access.MembershipKey{inAlpha, inBeta}, in: []impulse.UUID{staffID, auditID},
			wantWrites: []string{removal},
		},
		{
			name: "a person who holds a role in one tenant alone leaves the group",
			opts: []Option{WithOneTenantPerPerson()}, held: []access.MembershipKey{inAlpha}, in: []impulse.UUID{staffID},
			wantWrites: []string{removal},
		},
		{
			name: "where a person may hold roles in several tenants the rule is not asked",
			held: []access.MembershipKey{inAlpha, inBeta}, in: []impulse.UUID{staffID},
			wantWrites: []string{removal},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			store.groups[auditID] = &Group{Kind: "global", Name: "Audit"}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			for _, group := range tt.in {
				store.groupMembers[memberKey(group, userID)] = true
			}
			m, _ := newManager(t, store, engineRoles, tt.opts...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveGroupMember(ctx, txn, staffID, userID)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) || !strings.Contains(err.Error(), "more than one tenant") {
					t.Fatalf("RemoveGroupMember() error = %v, want the refusal of a person left in several tenants", err)
				}
			} else if err != nil {
				t.Fatalf("RemoveGroupMember() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_lastAdministrator_groups pins the rule on a tenant's last administrator
// where groups are in play, in both directions. A person who holds the administrator
// role through a group kept in the tenant is a holder: they keep the tenant
// administered when another removes their own role, and they are held to the rule when
// they leave the group, or take the role from it, themselves. A place or a group's role
// outside its period administers nothing. And a group kept for the whole application
// gives the tenant no administrator of its own, where the permission engine counts none
// (access.AdministersTenant): its people do not keep the tenant administered, and they
// leave it, or take the role from it, whoever else the tenant has.
func TestManager_lastAdministrator_groups(t *testing.T) {
	t.Parallel()

	ended, upcoming, _ := periodFixtures()
	// eveningID is a second group kept in the tenant, beside the night shift.
	eveningID := mustUUID("0190b000-0000-7000-8000-000000000004")
	nightWarden := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Warden")
	eveningWarden := access.NewGroupRoleKey(GroupID(eveningID), alphaScope, "Warden")
	staffWarden := access.NewGroupRoleKey(GroupID(staffID), alphaScope, "Warden")
	danaWarden := access.NewMembershipKey(alphaScope, dana, "Warden")
	erinWarden := access.NewMembershipKey(alphaScope, erin, "Warden")
	danaSteward := access.NewMembershipKey(globalScope, dana, "Steward")
	leave := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.RemoveGroupMember(ctx, txn, nightID, userID)
	}
	takeFromGroup := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.TakeGroupRole(ctx, txn, nightID, alphaScope, "Warden")
	}
	removeOwn := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.RemoveRole(ctx, txn, alphaScope, userID, "Warden")
	}
	leaveStaff := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.RemoveGroupMember(ctx, txn, staffID, userID)
	}
	takeFromStaff := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.TakeGroupRole(ctx, txn, staffID, alphaScope, "Warden")
	}
	left := "delete group member " + nightID.String() + " " + userID.String()
	leftStaff := "delete group member " + staffID.String() + " " + userID.String()
	taken := "delete group role " + groupRoleText(&nightWarden)
	takenFromStaff := "delete group role " + groupRoleText(&staffWarden)
	removed := "delete membership " + membershipText(&danaWarden)

	tests := []struct {
		name   string
		opts   []Option
		caller accesstypes.User
		// held are the memberships, roles what groups hold and places who is in which
		// group, each with its period.
		held     []access.MembershipKey
		roles    map[access.GroupRoleKey]accesstypes.Period
		places   map[access.GroupMemberKey]accesstypes.Period
		act      func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		wantSays string
		wantDone string
	}{
		{
			name: "the last administrator, who holds the role through a group, does not leave the group",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}},
			act:    leave, wantSays: "You are the last administrator of this tenant. Give another person the role before you leave the group that gives you yours.",
		},
		{
			name: "they leave once another person holds the role in the tenant",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward, erinWarden},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}},
			act:    leave, wantDone: left,
		},
		{
			name: "they leave once another person holds it through another group of the tenant",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}, eveningWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}, memberKey(eveningID, otherUser): {}},
			act:    leave, wantDone: left,
		},
		{
			name: "another person whose place in that group has not begun administers nothing yet",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}, eveningWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}, memberKey(eveningID, otherUser): upcoming},
			act:    leave, wantSays: "You are the last administrator of this tenant. Give another person the role before you leave the group that gives you yours.",
		},
		{
			name: "another person who holds it through a group kept for the whole application is no administrator of the tenant's own",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}, staffWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}, memberKey(staffID, otherUser): {}},
			act:    leave, wantSays: "You are the last administrator of this tenant. Give another person the role before you leave the group that gives you yours.",
		},
		{
			name: "a person leaves a group kept for the whole application that holds the role in the tenant: they were no administrator of the tenant's own",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{staffWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(staffID, userID): {}},
			act:    leaveStaff, wantDone: leftStaff,
		},
		{
			name: "a person in a group kept for the whole application takes from it the role it holds in the tenant",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{staffWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(staffID, userID): {}},
			act:    takeFromStaff, wantDone: takenFromStaff,
		},
		{
			name: "they leave where they hold the same role themselves",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward, danaWarden},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}},
			act:    leave, wantDone: left,
		},
		{
			name: "somebody else takes the last administrator out of the group",
			opts: []Option{administrators}, caller: erin, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}},
			act:    leave, wantDone: left,
		},
		{
			name:   "without the administrator roles nobody takes themselves out of a group that gives them a role in a tenant",
			caller: dana, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}},
			act:    leave, wantSays: "You cannot take yourself out of a group that gives you a role in a tenant.",
		},
		{
			name:   "without the administrator roles, the refusal says an before a word for a tenant that begins with a vowel",
			opts:   []Option{WithTenantWord("organization")},
			caller: dana, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}},
			act:    leave, wantSays: "You cannot take yourself out of a group that gives you a role in an organization.",
		},
		{
			name:   "without the administrator roles nobody takes a role away from a group they are in, where the group holds it in a tenant",
			caller: dana, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}},
			act:    takeFromGroup, wantSays: "You cannot take a role away from a group you are in, where the group holds it in a tenant.",
		},
		{
			name:   "without the administrator roles, the refusal to take a role from one's group says an before a word for a tenant that begins with a vowel",
			opts:   []Option{WithTenantWord("organization")},
			caller: dana, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}},
			act:    takeFromGroup, wantSays: "You cannot take a role away from a group you are in, where the group holds it in an organization.",
		},
		{
			name:   "without the administrator roles nobody removes a role of their own in a tenant",
			caller: dana, held: []access.MembershipKey{danaSteward, danaWarden},
			act: removeOwn, wantSays: "You cannot remove a role from yourself.",
		},
		{
			name: "a person in the group does not take from it the role that makes them the last administrator",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}, memberKey(nightID, otherUser): {}},
			act:    takeFromGroup, wantSays: "You are the last administrator of this tenant. Give another person the role before you take it from the group that gives you yours.",
		},
		{
			name: "they take it once another person holds the role some other way",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward, erinWarden},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}},
			act:    takeFromGroup, wantDone: taken,
		},
		{
			name: "somebody who is not in the group takes the role from it",
			opts: []Option{administrators}, caller: erin, held: []access.MembershipKey{danaSteward},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}},
			act:    takeFromGroup, wantDone: taken,
		},
		{
			name: "a person removes their own role while another holds it through a group",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward, danaWarden},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, otherUser): {}},
			act:    removeOwn, wantDone: removed,
		},
		{
			name: "a person removes their own role while they hold it through a group too",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward, danaWarden},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: {}},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): {}},
			act:    removeOwn, wantDone: removed,
		},
		{
			name: "a group whose role has ended makes nobody an administrator",
			opts: []Option{administrators}, caller: dana, held: []access.MembershipKey{danaSteward, danaWarden},
			roles:  map[access.GroupRoleKey]accesstypes.Period{nightWarden: ended},
			places: map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, otherUser): {}},
			act:    removeOwn, wantSays: "You are the last administrator of this tenant. Give another person the role before you remove your own.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			store.groups[eveningID] = &Group{Kind: "domain", Domain: "alpha", Name: "Evening shift"}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			store.rolePeriods = map[access.GroupRoleKey]accesstypes.Period{}
			for key, period := range tt.roles {
				store.groupRoles[key] = true
				store.rolePeriods[key] = period
			}
			store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{}
			for key, period := range tt.places {
				store.groupMembers[key] = true
				store.placePeriods[key] = period
			}
			m, _ := newManager(t, store, engineRoles, append(tt.opts, WithRoleGrants(&fakeGrants{}))...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.act(asCallerThrough(ctx, managedAuth, tt.caller), txn, m)
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
				t.Fatalf("error = %v, want the removal made", err)
			}
			if diff := cmp.Diff([]string{tt.wantDone}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}
