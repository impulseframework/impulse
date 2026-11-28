package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// TestManager_lastAdministrator_accountsThatSignIn pins who counts as a tenant's
// administrator beside the person who gives the role up: a person whose account can
// sign in. A holder whose account is disabled, or is no longer there, is nobody's way
// into the tenant, so the person who would leave the tenant to them is its last
// administrator and keeps the role, whether the other holds it by a membership of their
// own or through a group of the tenant. Once the account is enabled again it counts, and
// the person leaves.
func TestManager_lastAdministrator_accountsThatSignIn(t *testing.T) {
	t.Parallel()

	const (
		removing = "You are the last administrator of this tenant. Give another person the role before you remove your own."
		leaving  = "You are the last administrator of this tenant. Give another person the role before you leave the group that gives you yours."
	)
	own := access.NewMembershipKey(alphaScope, dana, "Reader")
	others := access.NewMembershipKey(alphaScope, erin, "Reader")
	groupRole := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")
	byMembership := func(store *fakeStore) {
		store.memberships[own], store.memberships[others] = true, true
	}
	otherThroughGroup := func(store *fakeStore) {
		store.memberships[own] = true
		store.groupMembers[memberKey(nightID, otherUser)] = true
		store.groupRoles[groupRole] = true
	}
	bothThroughGroup := func(store *fakeStore) {
		store.groupMembers[memberKey(nightID, userID)], store.groupMembers[memberKey(nightID, otherUser)] = true, true
		store.groupRoles[groupRole] = true
	}
	removes := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.RemoveRole(ctx, txn, alphaScope, userID, "Reader")
	}
	leaves := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.RemoveGroupMember(ctx, txn, nightID, userID)
	}

	for name, tt := range map[string]struct {
		held func(store *fakeStore)
		// disabled says the other administrator's account is disabled, and gone that it
		// is not there at all.
		disabled bool
		gone     bool
		act      func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		wantSays string
		// wantWrite is the one write of an act that goes through.
		wantWrite string
	}{
		"the other administrator can sign in: the person removes their own role": {
			held: byMembership, act: removes, wantWrite: "delete membership " + membershipText(&own),
		},
		"the other administrator's account is disabled: the person is the last one and keeps the role": {
			held: byMembership, disabled: true, act: removes, wantSays: removing,
		},
		"the other administrator's account is not there: the person is the last one and keeps the role": {
			held: byMembership, gone: true, act: removes, wantSays: removing,
		},
		"the other administrator, through a group, can sign in: the person removes their own role": {
			held: otherThroughGroup, act: removes, wantWrite: "delete membership " + membershipText(&own),
		},
		"the other administrator, through a group, is disabled: the person keeps the role": {
			held: otherThroughGroup, disabled: true, act: removes, wantSays: removing,
		},
		"the group's other member can sign in: the person leaves the group": {
			held: bothThroughGroup, act: leaves, wantWrite: "delete group member " + nightID.String() + " " + string(dana),
		},
		"the group's other member is disabled: the person stays in the group that gives the role": {
			held: bothThroughGroup, disabled: true, act: leaves, wantSays: leaving,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			// A role of the whole application beside what the row gives: the person who
			// gives up what they hold in the tenant keeps their account.
			store.memberships[access.NewMembershipKey(globalScope, dana, "Steward")] = true
			tt.held(store)
			store.disabled = map[impulse.UUID]bool{otherUser: tt.disabled}
			if tt.gone {
				delete(store.usernames, otherUser)
			}
			m, _ := newManager(t, store, tenantRoles, WithAdministratorRoles([]accesstypes.Role{"Reader"}), WithRoleGrants(&fakeGrants{}))

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.act(asCaller(ctx, dana), txn, m)
			})
			if tt.wantSays != "" {
				if err == nil || !httpio.HasForbidden(err) || !strings.Contains(err.Error(), tt.wantSays) {
					t.Fatalf("error = %v, want the refusal %q", err, tt.wantSays)
				}
				if len(store.writes) != 0 {
					t.Errorf("buffered writes after the refusal = %v, want none", store.writes)
				}

				return
			}
			if err != nil {
				t.Fatalf("error = %v, want the act to go through", err)
			}
			if diff := cmp.Diff([]string{tt.wantWrite}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}
