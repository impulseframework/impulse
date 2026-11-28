package identity

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// keptAccountAct is one of the four acts that can take the last thing a person holds:
// what dana holds before it (held), the warning before it where it has one (warn) with
// the button it says (confirm), the act itself, and the one row it writes.
type keptAccountAct struct {
	name    string
	held    func(store *fakeStore)
	warn    func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) (*resource.Warning, error)
	confirm string
	act     func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
	row     string
}

// keptAccountActs are the four acts, each of the last thing dana holds anywhere: the
// last role taken away, the last place in a group, the removal from the only tenant she
// is in, and her own leaving of it.
func keptAccountActs() []keptAccountAct {
	ownReader := func(store *fakeStore) { store.memberships[danaAlphaReader] = true }
	nightPlace := func(store *fakeStore) { store.groupMembers[memberKey(nightID, userID)] = true }

	return []keptAccountAct{
		{
			name: "the last role anywhere taken away", held: ownReader,
			warn: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) (*resource.Warning, error) {
				return m.RoleRemovalWarning(asCallerThrough(ctx, managedAuth, erin), txn, alphaScope, userID, "Reader")
			},
			confirm: roleGoesConfirmSays,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemoveRole(asCallerThrough(ctx, managedAuth, erin), txn, alphaScope, userID, "Reader")
			},
			row: "delete membership " + membershipText(&danaAlphaReader),
		},
		{
			name: "the last place in a group", held: nightPlace,
			warn: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) (*resource.Warning, error) {
				return m.GroupMemberRemovalWarning(asCallerThrough(ctx, managedAuth, erin), txn, nightID, userID)
			},
			confirm: placeGoesConfirmSays,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemoveGroupMember(asCallerThrough(ctx, managedAuth, erin), txn, nightID, userID)
			},
			row: "delete group member " + nightID.String() + " " + string(dana),
		},
		{
			name: "the removal from the only tenant", held: ownReader,
			warn: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) (*resource.Warning, error) {
				ctx = asErin(ctx, managedAuth, true)
				var warning *resource.Warning
				err := runInTenant(ctx, m, func(tenant *Tenant) error {
					var err error
					warning, err = tenant.RemovalWarning(ctx, txn, userID)

					return err
				})

				return warning, err
			},
			confirm: removalConfirmSays,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemoveFromTenant(asErinIn(ctx), txn, userID, "alpha")
			},
			row: "delete membership " + membershipText(&danaAlphaReader),
		},
		{
			name: "the person's own leaving of the only tenant", held: ownReader,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.LeaveTenant(asCaller(ctx, dana), txn, userID, "alpha")
			},
			row: "delete membership " + membershipText(&danaAlphaReader),
		},
	}
}

// TestManager_removalsKeepTheAccount pins that none of the four acts that can take the
// last thing a person holds deletes the account: the account's row and its identity link
// stand and no session of the person ends, for a person who signs in through no tenant's
// directory and for one whom another tenant's directory signs in. The warning before a
// removal carries no note and the plain button, and the person's own list offers the
// leaving with nothing said of the account.
func TestManager_removalsKeepTheAccount(t *testing.T) {
	t.Parallel()

	for _, managing := range []struct {
		name    string
		tenants []accesstypes.Domain
	}{
		{name: "signs in through no tenant's directory"},
		{name: "signs in through another tenant's directory", tenants: []accesstypes.Domain{"beta"}},
	} {
		for _, tt := range keptAccountActs() {
			t.Run(tt.name+", "+managing.name, func(t *testing.T) {
				t.Parallel()

				store := removalStore()
				store.managing = map[impulse.UUID][]accesstypes.Domain{userID: managing.tenants}
				tt.held(store)
				m, _, mail := removalManager(t, store, &fakeGrants{}, false)

				if tt.warn != nil {
					var warning *resource.Warning
					if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
						var err error
						warning, err = tt.warn(ctx, txn, m)

						return err
					}); err != nil {
						t.Fatalf("the warning's error = %v", err)
					}
					if warning.Confirm != tt.confirm || len(warning.Also) != 0 {
						t.Errorf("the warning = %+v, want the button %q and no note", warning, tt.confirm)
					}
				} else {
					want := []PersonTenant{{Domain: "alpha", Roles: []string{"Reader"}, Refusal: ""}}
					if diff := cmp.Diff(want, listTenants(t, m)); diff != "" {
						t.Errorf("PersonTenants() (-want +got):\n%s", diff)
					}
				}
				if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					return tt.act(ctx, txn, m)
				}); err != nil {
					t.Fatalf("the act's error = %v", err)
				}
				if diff := cmp.Diff([]string{tt.row}, store.writes); diff != "" {
					t.Errorf("buffered writes (-want +got):\n%s", diff)
				}
				if len(mail.asked)+len(mail.sent) != 0 {
					t.Errorf("mailed %v and asked for %v, want nothing", mail.sent, mail.asked)
				}
			})
		}
	}
}
