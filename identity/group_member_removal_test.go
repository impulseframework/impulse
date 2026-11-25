package identity

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// The sentences of a person's removal from a group the tests read, word for word.
const (
	placeGoesConsequenceSays   = "dana is removed from the group Night shift. They keep the roles they hold themselves and lose the group's."
	placeGoesConfirmSays       = "Remove from the group"
	noLongerInGroupSays        = "This person is no longer in that group. Refresh the page to see who is in it."
	mayNotTakeThroughGroupSays = "Taking a person out of the group Night shift takes away the role Warden, which you may not take from them."
)

// groupMemberRemovalManager builds a manager over the store that calls a tenant a sector,
// mails through a mailbox, names Warden as the role that administers a tenant, and judges
// a request's changes to a group by grants that let the caller take every role but
// Warden.
func groupMemberRemovalManager(t *testing.T, store Store) (*Manager, *fakeEngine, *fakeMail) {
	t.Helper()

	mail := &fakeMail{}
	engine := &fakeEngine{roles: wardenRoles}
	grants := &fakeGrants{noTake: []accesstypes.Role{"Warden"}}
	m, err := New(managedAuth, store, engine, fakeSessions{mail: mail}, WithTenantWord("sector"), administrators, WithRoleGrants(grants))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m, engine, mail
}

// TestManager_GroupMemberRemovalWarning pins what an administrator is told before a
// person is taken out of a group, from the judgment the removal runs: who leaves which
// group, in one consequence with the one button and no note, where the place is the last
// thing the person holds too. A person who is not in the group, a group or an account
// that is not there, and a removal the rules refuse are answered as refusals. Each warning is one a dialog can draw (resource.CheckWarning),
// and reading it writes nothing.
func TestManager_GroupMemberRemovalWarning(t *testing.T) {
	t.Parallel()

	ended, _, _ := periodFixtures()
	goes := &resource.Warning{Consequence: placeGoesConsequenceSays, Names: nil, Confirm: placeGoesConfirmSays, Also: nil}
	tests := []struct {
		name string
		// in are the places committed and held the memberships; store adds what else the
		// case holds.
		in     []access.GroupMemberKey
		held   []access.MembershipKey
		store  func(store *fakeStore)
		person impulse.UUID // the person taken out; userID when unset
		group  impulse.UUID // the group they are taken out of; nightID when unset
		want   *resource.Warning
		// wantErr is the refusal, and wantSays the sentence it reads.
		wantErr  func(error) bool
		wantSays string
	}{
		{
			name: "a person who holds a role of their own",
			in:   []access.GroupMemberKey{memberKey(nightID, userID)}, held: []access.MembershipKey{danaSteward},
			want: goes,
		},
		{
			name: "a person in another group and with no role",
			in:   []access.GroupMemberKey{memberKey(nightID, userID), memberKey(staffID, userID)},
			want: goes,
		},
		{
			name: "the last thing the person holds: the one consequence, the one button and no note",
			in:   []access.GroupMemberKey{memberKey(nightID, userID)},
			want: goes,
		},
		{
			name:  "a place whose period is over, and the last thing held: the one consequence, the one button and no note",
			in:    []access.GroupMemberKey{memberKey(nightID, userID)},
			store: func(store *fakeStore) { store.placePeriods[memberKey(nightID, userID)] = ended },
			want:  goes,
		},
		{
			name: "a person who is no longer in the group is refused, and says to refresh",
			in:   []access.GroupMemberKey{memberKey(staffID, userID)}, held: []access.MembershipKey{danaSteward},
			wantErr: httpio.HasNotFound, wantSays: noLongerInGroupSays,
		},
		{
			name: "a group that is not there is not found",
			in:   []access.GroupMemberKey{memberKey(nightID, userID)}, group: newGroupID,
			wantErr: httpio.HasNotFound, wantSays: groupNotFoundMessage,
		},
		{
			name: "an account that is not there is not found",
			in:   []access.GroupMemberKey{memberKey(nightID, userID)}, person: frankID,
			wantErr: httpio.HasNotFound, wantSays: accountNotFoundMessage,
		},
		{
			name: "a group that holds a role the caller may not take: the removal's own refusal",
			in:   []access.GroupMemberKey{memberKey(nightID, userID)}, held: []access.MembershipKey{danaSteward},
			store: func(store *fakeStore) {
				store.groupRoles[access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Warden")] = true
			},
			wantErr: httpio.HasForbidden, wantSays: mayNotTakeThroughGroupSays,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := leavingStore()
			for _, key := range tt.in {
				store.groupMembers[key] = true
			}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			if tt.store != nil {
				tt.store(store)
			}
			m, engine, mail := groupMemberRemovalManager(t, store)
			person, group := tt.person, tt.group
			if person == impulse.NilUUID {
				person = userID
			}
			if group == impulse.NilUUID {
				group = nightID
			}

			var warning *resource.Warning
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				warning, err = m.GroupMemberRemovalWarning(asCallerThrough(ctx, managedAuth, erin), txn, group, person)

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) || httpio.Message(err) != tt.wantSays {
					t.Fatalf("GroupMemberRemovalWarning() = %v, error = %v, want the refusal saying %q", warning, err, tt.wantSays)
				}
			} else {
				if err != nil {
					t.Fatalf("GroupMemberRemovalWarning() error = %v", err)
				}
				if diff := cmp.Diff(tt.want, warning); diff != "" {
					t.Errorf("GroupMemberRemovalWarning() (-want +got):\n%s", diff)
				}
				if err := resource.CheckWarning("RemoveMembersGroupMember", warning); err != nil {
					t.Errorf("resource.CheckWarning() error = %v", err)
				}
			}
			if len(store.writes) != 0 || engine.changed != 0 || len(mail.asked) != 0 {
				t.Errorf("buffered writes = %v, the engine told %d time(s) and notices %v, want nothing from a warning", store.writes, engine.changed, mail.asked)
			}
		})
	}
}

// TestTenant_GroupMemberRemovalWarning pins the warning before a tenant's method takes a
// person out of a group: a group of the tenant answers the warning, and a group kept
// anywhere else is not found, as the removal finds it. Reading it writes nothing.
func TestTenant_GroupMemberRemovalWarning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		group    impulse.UUID
		want     *resource.Warning
		wantErr  func(error) bool
		wantSays string
	}{
		{
			name: "a group of the tenant", group: nightID,
			want: &resource.Warning{Consequence: placeGoesConsequenceSays, Names: nil, Confirm: placeGoesConfirmSays, Also: nil},
		},
		{name: "a group of the whole application is not the tenant's", group: staffID, wantErr: httpio.HasNotFound, wantSays: groupNotFoundMessage},
		{name: "a group of another tenant is not the tenant's", group: dayID, wantErr: httpio.HasNotFound, wantSays: groupNotFoundMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := leavingStore()
			store.memberships[danaSteward] = true
			store.groupMembers[memberKey(nightID, userID)] = true
			m, engine, mail := groupMemberRemovalManager(t, store)

			var warning *resource.Warning
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = inAlpha(ctx, erin)
				tenant, err := m.Tenant(ctx)
				if err != nil {
					return err
				}
				warning, err = tenant.GroupMemberRemovalWarning(ctx, txn, tt.group, userID)

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) || httpio.Message(err) != tt.wantSays {
					t.Fatalf("GroupMemberRemovalWarning() = %v, error = %v, want the refusal saying %q", warning, err, tt.wantSays)
				}
			} else {
				if err != nil {
					t.Fatalf("GroupMemberRemovalWarning() error = %v", err)
				}
				if diff := cmp.Diff(tt.want, warning); diff != "" {
					t.Errorf("GroupMemberRemovalWarning() (-want +got):\n%s", diff)
				}
			}
			if len(store.writes) != 0 || engine.changed != 0 || len(mail.asked) != 0 {
				t.Errorf("buffered writes = %v, the engine told %d time(s) and notices %v, want nothing from a warning", store.writes, engine.changed, mail.asked)
			}
		})
	}
}

// TestManager_GroupMemberRemovalWarning_agreesWithRemoveGroupMember pins that the warning
// and the act read one judgment: the warning says nothing of the account and the removal
// that follows deletes none, where the place is the last thing the person holds and where
// it is not, and a person who is not in the group is not found before anything is said.
func TestManager_GroupMemberRemovalWarning_agreesWithRemoveGroupMember(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []access.GroupMemberKey
		held []access.MembershipKey
	}{
		{name: "a person who is not in the group"},
		{name: "the last thing a person held", in: []access.GroupMemberKey{memberKey(nightID, userID)}},
		{name: "a person in another group", in: []access.GroupMemberKey{memberKey(nightID, userID), memberKey(staffID, userID)}},
		{
			name: "a person with a role of their own",
			in:   []access.GroupMemberKey{memberKey(nightID, userID)},
			held: []access.MembershipKey{access.NewMembershipKey(betaScope, dana, "Reader")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := groupStore()
			for _, key := range tt.in {
				store.groupMembers[key] = true
			}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			m, _ := newManager(t, store, engineRoles)

			var warning *resource.Warning
			warned := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				warning, err = m.GroupMemberRemovalWarning(ctx, txn, nightID, userID)

				return err
			})
			if len(tt.in) == 0 {
				if warned == nil || !httpio.HasNotFound(warned) {
					t.Fatalf("GroupMemberRemovalWarning() = %v, error = %v, want the person not found in the group", warning, warned)
				}
			} else if warned != nil {
				t.Fatalf("GroupMemberRemovalWarning() error = %v", warned)
			}
			if len(store.writes) != 0 {
				t.Fatalf("buffered writes = %v, want nothing from a warning", store.writes)
			}
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveGroupMember(ctx, txn, nightID, userID)
			}); err != nil {
				t.Fatalf("RemoveGroupMember() error = %v", err)
			}
			if warning != nil && (len(warning.Also) != 0 || warning.Confirm != placeGoesConfirmSays) {
				t.Errorf("GroupMemberRemovalWarning() = %+v, want the one button and no note", warning)
			}
			var want []string
			if len(tt.in) > 0 {
				want = []string{"delete group member " + nightID.String() + " " + string(dana)}
			}
			if diff := cmp.Diff(want, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}
