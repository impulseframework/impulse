package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// managedElsewhereSays is the note of the three warnings where the person then holds
// nothing and another tenant manages the account.
const managedElsewhereSays = "They then hold nothing, but their account stays: the sector whose directory they sign in through manages it."

// The words of the role's and the group's warnings the cases of an account another tenant
// manages read.
const (
	roleRemovalConfirmSays          = "Take the role away"
	roleRemovalDeletesConfirmSays   = "Take the role away and delete the account"
	groupRemovalConfirmSays         = "Remove from the group"
	groupRemovalDeletesConfirmSays  = "Remove from the group and delete the account"
	groupRemovalLastConsequenceSays = "dana is removed from the group Night shift."
)

// managedCase is one shape of who manages dana's account, for the removals that would
// leave her holding nothing: the tenants whose list says they manage it
// (Store.ManagingTenants), whether she has no identity link, and whether the account goes.
type managedCase struct {
	name     string
	managing []accesstypes.Domain
	noLink   bool
	deletes  bool
	// asked is whether the store is asked who manages the account.
	asked bool
}

// managedCases are the shapes of the rule on an account another tenant manages, for an
// act that takes the last thing dana holds, which she holds in alpha.
var managedCases = []managedCase{
	{name: "signs in through beta's directory: the account stays", managing: []accesstypes.Domain{"beta"}, asked: true},
	{name: "signs in through alpha's directory: deleted, as today", managing: []accesstypes.Domain{"alpha"}, deletes: true, asked: true},
	{name: "signs in through no tenant's directory: deleted, as today", deletes: true, asked: true},
	{name: "no identity link: deleted, and who manages the account is not asked", managing: []accesstypes.Domain{"beta"}, noLink: true, deletes: true},
}

// managedStore is the store of a removal (removalStore) in which dana holds what held
// says, and who manages her account is the case's.
func managedStore(tt *managedCase, held func(store *fakeStore)) *fakeStore {
	store := removalStore()
	held(store)
	store.managing = map[impulse.UUID][]accesstypes.Domain{userID: tt.managing}
	if tt.noLink {
		store.identities = map[impulse.UUID]impulse.UUID{}
	}

	return store
}

// accountWrites are the writes of the act that takes row: the account's delete before it
// where the account goes, with its identity link where it has one.
func accountWrites(tt *managedCase, row string) []string {
	if !tt.deletes {
		return []string{row}
	}
	writes := []string{"end session " + danaSession.String()}
	if !tt.noLink {
		writes = append(writes, "delete identity "+identityA.String())
	}

	return append(writes, "delete user "+userID.String(), row)
}

// checkManaged fails unless the act buffered want, asked who manages the account as the
// case says, and mailed notices that each say whether the account went as the case does.
func checkManaged(t *testing.T, tt *managedCase, store *fakeStore, mail *fakeMail, want []string) {
	t.Helper()

	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if asked := store.managingAsked > 0; asked != tt.asked {
		t.Errorf("ManagingTenants() asked %d time(s), want asked = %t", store.managingAsked, tt.asked)
	}
	for _, notice := range mail.asked {
		if !strings.HasSuffix(notice, " deleted="+boolText(tt.deletes)) {
			t.Errorf("notice %q, want one that says deleted=%s", notice, boolText(tt.deletes))
		}
	}
}

// warningNote is the one note of a warning, "" for none.
func warningNote(w *resource.Warning) string {
	if w == nil || len(w.Also) != 1 {
		return ""
	}

	return w.Also[0].Says
}

// TestManager_RemoveFromTenant_managedElsewhere pins that a removal from a tenant deletes
// the account it leaves holding nothing only where no other tenant manages it: an account
// another tenant's directory signs in stays, with the rows in the tenant gone, and the
// judgment, the warning, the act's answer and the notice say it stays. The tenant's own
// directory, no directory and no identity link delete it as today.
func TestManager_RemoveFromTenant_managedElsewhere(t *testing.T) {
	t.Parallel()

	for i := range managedCases {
		tt := &managedCases[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := managedStore(tt, func(store *fakeStore) { store.memberships[danaAlphaReader] = true })
			m, _, mail := removalManager(t, store, &fakeGrants{}, false)

			judged, err := judgeDana(t, m, asErinIn, "alpha")
			if err != nil {
				t.Fatalf("JudgeTenantRemoval() error = %v", err)
			}
			if judged.DeletesAccount != tt.deletes || judged.ManagedElsewhere != !tt.deletes {
				t.Errorf("JudgeTenantRemoval() DeletesAccount = %t, ManagedElsewhere = %t, want %t and %t", judged.DeletesAccount, judged.ManagedElsewhere, tt.deletes, !tt.deletes)
			}
			var warning *resource.Warning
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				tenant, err := m.Tenant(inAlpha(ctx, erin))
				if err != nil {
					return err
				}
				warning, err = tenant.RemovalWarning(inAlpha(ctx, erin), txn, userID)

				return err
			}); err != nil {
				t.Fatalf("RemovalWarning() error = %v", err)
			}
			wantNote := removalDeletesSays
			if !tt.deletes {
				wantNote = managedElsewhereSays
			}
			if got := warningNote(warning); got != wantNote || warning.Confirm != removalConfirmSays {
				t.Errorf("RemovalWarning() note %q, button %q, want %q and %q", got, warning.Confirm, wantNote, removalConfirmSays)
			}
			store.managingAsked = 0
			deleted, err := removeDana(t, m, asErinIn, "alpha")
			if err != nil {
				t.Fatalf("RemoveFromTenant() error = %v", err)
			}
			if deleted != tt.deletes {
				t.Errorf("RemoveFromTenant() deleted = %t, want %t", deleted, tt.deletes)
			}
			checkManaged(t, tt, store, mail, accountWrites(tt, "delete membership "+membershipText(&danaAlphaReader)))
			if len(mail.asked) != 1 {
				t.Errorf("notices asked for = %v, want one", mail.asked)
			}
		})
	}
}

// TestManager_RemoveRole_managedElsewhere pins that taking away the last role a person
// holds deletes the account only where no tenant other than the role's manages it, by the
// tenant's method and by the whole application's, and that both warnings say what
// follows: an account another tenant's directory signs in stays with the button and the
// note that say so. A role held for the whole application is held in no tenant, so any
// tenant that manages the account keeps it.
func TestManager_RemoveRole_managedElsewhere(t *testing.T) {
	t.Parallel()

	global := []managedCase{
		{name: "a role for the whole application, signs in through beta's directory: the account stays", managing: []accesstypes.Domain{"beta"}, asked: true},
		{name: "a role for the whole application, signs in through no tenant's directory: deleted", deletes: true, asked: true},
	}
	for _, through := range []string{"the tenant's method", "the whole application's method"} {
		for i := range managedCases {
			tt := &managedCases[i]
			t.Run(through+", "+tt.name, func(t *testing.T) {
				t.Parallel()

				store := managedStore(tt, func(store *fakeStore) { store.memberships[danaAlphaReader] = true })
				m, _, mail := removalManager(t, store, &fakeGrants{}, false)
				var warning *resource.Warning
				if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					ctx = inAlpha(ctx, erin)
					if through == "the whole application's method" {
						var err error
						warning, err = m.RoleRemovalWarning(ctx, txn, alphaScope, userID, "Reader")

						return err
					}
					tenant, err := m.Tenant(ctx)
					if err != nil {
						return err
					}
					warning, err = tenant.RoleRemovalWarning(ctx, txn, userID, "Reader")

					return err
				}); err != nil {
					t.Fatalf("RoleRemovalWarning() error = %v", err)
				}
				checkRoleWarning(t, tt, warning)
				store.managingAsked = 0
				if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					ctx = inAlpha(ctx, erin)
					if through == "the whole application's method" {
						return m.RemoveRole(ctx, txn, alphaScope, userID, "Reader")
					}
					tenant, err := m.Tenant(ctx)
					if err != nil {
						return err
					}

					return tenant.RemoveRole(ctx, txn, userID, "Reader")
				}); err != nil {
					t.Fatalf("RemoveRole() error = %v", err)
				}
				checkManaged(t, tt, store, mail, accountWrites(tt, "delete membership "+membershipText(&danaAlphaReader)))
				if len(mail.asked) != 1 {
					t.Errorf("notices asked for = %v, want one", mail.asked)
				}
			})
		}
	}
	for i := range global {
		tt := &global[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := managedStore(tt, func(store *fakeStore) { store.memberships[danaGlobal] = true })
			m, _, mail := removalManager(t, store, &fakeGrants{}, false)
			var warning *resource.Warning
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				warning, err = m.RoleRemovalWarning(asErinIn(ctx), txn, globalScope, userID, "Steward")

				return err
			}); err != nil {
				t.Fatalf("RoleRemovalWarning() error = %v", err)
			}
			checkRoleWarning(t, tt, warning)
			store.managingAsked = 0
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveRole(asErinIn(ctx), txn, globalScope, userID, "Steward")
			}); err != nil {
				t.Fatalf("RemoveRole() error = %v", err)
			}
			checkManaged(t, tt, store, mail, accountWrites(tt, "delete membership "+membershipText(&danaGlobal)))
		})
	}
}

// checkRoleWarning fails unless the warning of a role's removal has the button and the
// note the case says: the deleting ones where the account goes, the plain button and the
// note of an account another tenant manages where it stays.
func checkRoleWarning(t *testing.T, tt *managedCase, warning *resource.Warning) {
	t.Helper()

	wantConfirm, wantNote := roleRemovalConfirmSays, managedElsewhereSays
	if tt.deletes {
		wantConfirm, wantNote = roleRemovalDeletesConfirmSays, roleTakesAccountSays
	}
	if got := warningNote(warning); got != wantNote || warning.Confirm != wantConfirm {
		t.Errorf("RoleRemovalWarning() note %q, button %q, want %q and %q", got, warning.Confirm, wantNote, wantConfirm)
	}
}

// TestManager_RemoveGroupMember_managedElsewhere pins that taking a person out of the last
// group they are in deletes the account only where no tenant other than the group's
// manages it, by the tenant's method and by the whole application's, and that both
// warnings say what follows.
func TestManager_RemoveGroupMember_managedElsewhere(t *testing.T) {
	t.Parallel()

	place := memberKey(nightID, userID)
	for _, through := range []string{"the tenant's method", "the whole application's method"} {
		for i := range managedCases {
			tt := &managedCases[i]
			t.Run(through+", "+tt.name, func(t *testing.T) {
				t.Parallel()

				store := managedStore(tt, func(store *fakeStore) {
					store.groupMembers[place] = true
					store.groupRoles[nightReader] = true
				})
				m, _, mail := removalManager(t, store, &fakeGrants{}, false)
				var warning *resource.Warning
				if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					ctx = inAlpha(ctx, erin)
					if through == "the whole application's method" {
						var err error
						warning, err = m.GroupMemberRemovalWarning(ctx, txn, nightID, userID)

						return err
					}
					tenant, err := m.Tenant(ctx)
					if err != nil {
						return err
					}
					warning, err = tenant.GroupMemberRemovalWarning(ctx, txn, nightID, userID)

					return err
				}); err != nil {
					t.Fatalf("GroupMemberRemovalWarning() error = %v", err)
				}
				wantConfirm, wantNote := groupRemovalConfirmSays, managedElsewhereSays
				if tt.deletes {
					wantConfirm, wantNote = groupRemovalDeletesConfirmSays, roleTakesAccountSays
				}
				if got := warningNote(warning); got != wantNote || warning.Confirm != wantConfirm || warning.Consequence != groupRemovalLastConsequenceSays {
					t.Errorf("GroupMemberRemovalWarning() = %+v, want the consequence %q, the button %q and the note %q", warning, groupRemovalLastConsequenceSays, wantConfirm, wantNote)
				}
				store.managingAsked = 0
				if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					ctx = inAlpha(ctx, erin)
					if through == "the whole application's method" {
						return m.RemoveGroupMember(ctx, txn, nightID, userID)
					}
					tenant, err := m.Tenant(ctx)
					if err != nil {
						return err
					}

					return tenant.RemoveGroupMember(ctx, txn, nightID, userID)
				}); err != nil {
					t.Fatalf("RemoveGroupMember() error = %v", err)
				}
				checkManaged(t, tt, store, mail, accountWrites(tt, "delete group member "+nightID.String()+" "+string(dana)))
				if len(mail.asked) != 1 {
					t.Errorf("notices asked for = %v, want one", mail.asked)
				}
			})
		}
	}
}

// TestManager_LeaveTenant_managedElsewhere pins that a person's own leaving of their last
// tenant deletes their account as before, also where another tenant's directory manages
// it: it is their own account and their own typed word.
func TestManager_LeaveTenant_managedElsewhere(t *testing.T) {
	t.Parallel()

	tt := &managedCase{name: "signs in through beta's directory", managing: []accesstypes.Domain{"beta"}, deletes: true}
	store := managedStore(tt, func(store *fakeStore) { store.memberships[danaAlphaReader] = true })
	m, _, mail := leavingManager(t, store, false)

	deleted, err := leave(t, m, "alpha", true)
	if err != nil {
		t.Fatalf("LeaveTenant() error = %v", err)
	}
	if !deleted {
		t.Errorf("LeaveTenant() deleted = false, want the account gone")
	}
	checkManaged(t, tt, store, mail, accountWrites(tt, "delete membership "+membershipText(&danaAlphaReader)))
}
