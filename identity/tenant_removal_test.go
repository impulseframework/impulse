package identity

import (
	"context"
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

// The sentences of a person's removal from a tenant, in the word the tests' manager calls
// a tenant.
const (
	removingYourselfSays        = "You cannot remove yourself from this sector here. Leave it from your own account page."
	removingYourselfUnnamedSays = "You cannot remove yourself from this sector. Ask another of its administrators to remove you."
	removingViewedAsSays        = "You cannot remove the account you are viewing as from this sector."
	noTenantToRemoveFromSays    = "Name the sector to remove the person from."
	nothingToRemoveSays         = "This person holds nothing in this sector itself, so there is nothing to remove here."
	removalSays                 = "dana is removed from this sector: every role they hold in it is taken away."
	removalWithGroupsSays       = "dana is removed from this sector: every role they hold in it is taken away, and they are taken out of its groups."
	removalConfirmSays          = "Remove from this sector"
	removalDeletesSays          = "They hold nothing anywhere else, so their account is deleted with its sign-in identities and sessions."
	removalStaysSays            = "Their account stays, and they remain one of this sector's people through a role held outside it, which this does not take away."
	removalKeepsSays            = "Their account stays, because they hold something outside this sector."
	wardenKeptFromOutsidersSays = "Warden is a role that administers a sector, and this sector admits only people who sign in through its own directory, which your sign-in does not enter. Who holds the role there is changed by one of its administrators, or by a super administrator."
	removalTakesReaderSays      = "Removing this person from the sector takes away the role Reader, which you may not take from them."
)

// danaAddress is the address dana confirmed in the tests of a removal.
const danaAddress = "dana@example.com"

// removalStore is the store of a person's tenants (leavingStore) in which dana has
// confirmed an address.
func removalStore() *fakeStore {
	store := leavingStore()
	store.emails = map[string]impulse.UUID{danaAddress: userID}

	return store
}

// removalManager builds a manager over the store that calls a tenant a sector, mails
// through a mailbox, judges the roles a group gives by the grants and, unless unnamed,
// names Warden as the role that administers a tenant. tenantAdministrators are the roles
// the engine answers who administers a tenant by, which the rule on a directory-only
// tenant reads.
func removalManager(t *testing.T, store Store, grants *fakeGrants, unnamed bool, tenantAdministrators ...accesstypes.Role) (*Manager, *fakeEngine, *fakeMail) {
	t.Helper()

	opts := []Option{WithTenantWord("sector"), WithRoleGrants(grants)}
	if !unnamed {
		opts = append(opts, WithAdministratorRoles([]accesstypes.Role{"Warden"}))
	}
	mail := &fakeMail{}
	engine := &fakeEngine{roles: tenantRoles, tenantAdministrators: tenantAdministrators}
	m, err := New(managedAuth, store, engine, fakeSessions{mail: mail}, opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m, engine, mail
}

// asErinIn is the context of a request erin makes in a session of her own.
func asErinIn(ctx context.Context) context.Context {
	return asCaller(ctx, erin)
}

// removeDana has dana removed from the tenant by the request the context makes.
func removeDana(t *testing.T, m *Manager, request func(context.Context) context.Context, domain accesstypes.Domain) (bool, error) {
	t.Helper()

	var deleted bool
	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		deleted, err = m.RemoveFromTenant(request(ctx), txn, userID, domain)

		return err
	})

	return deleted, err
}

// judgeDana judges dana's removal from the tenant by the request the context makes.
func judgeDana(t *testing.T, m *Manager, request func(context.Context) context.Context, domain accesstypes.Domain) (*TenantRemoval, error) {
	t.Helper()

	var judged *TenantRemoval
	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		judged, err = m.JudgeTenantRemoval(request(ctx), txn, userID, domain)

		return err
	})

	return judged, err
}

// TestManager_RemoveFromTenant_rows pins what a removal takes: every row the person has in
// the tenant, a membership whose period has ended among them, and each place in a group
// of the tenant, and no row anywhere else, the person's own in another tenant or the whole
// application, a place in a whole-application group or another tenant's, or another
// person's. The engine is told once, and nobody is mailed.
func TestManager_RemoveFromTenant_rows(t *testing.T) {
	t.Parallel()

	store := removalStore()
	for _, key := range []access.MembershipKey{danaAlphaReader, danaAlphaLead, danaBetaReader, danaGlobal, erinAlphaWarden} {
		store.memberships[key] = true
	}
	store.membershipPeriods[danaAlphaLead] = accesstypes.Period{End: time.Now().Add(-time.Hour)}
	store.groupMembers[memberKey(nightID, userID)], store.groupMembers[memberKey(staffID, userID)] = true, true
	store.groupMembers[memberKey(dayID, userID)], store.groupMembers[memberKey(nightID, otherUser)] = true, true
	store.groupRoles[nightReader] = true
	m, engine, mail := removalManager(t, store, &fakeGrants{}, false)

	deleted, err := removeDana(t, m, asErinIn, "alpha")
	if err != nil {
		t.Fatalf("RemoveFromTenant() error = %v", err)
	}
	if deleted {
		t.Error("RemoveFromTenant() deleted = true, want the account kept by what dana holds elsewhere")
	}
	want := []string{
		"delete membership " + membershipText(&danaAlphaLead),
		"delete membership " + membershipText(&danaAlphaReader),
		"delete group member " + nightID.String() + " " + string(dana),
	}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
	}
	if len(mail.asked)+len(mail.sent) != 0 {
		t.Errorf("mailed %v and asked for %v, want nothing", mail.sent, mail.asked)
	}
}

// TestManager_RemoveFromTenant_account pins when the account goes with a removal: when the
// person then holds nothing that has not ended, with its sign-in identities and sessions
// and every row that had ended elsewhere, in the same transaction. Anything held elsewhere that has not ended keeps it, and a row elsewhere the
// server's clock reads as ended and the database's does not keeps it too, and stays.
func TestManager_RemoveFromTenant_account(t *testing.T) {
	t.Parallel()

	ended := accesstypes.Period{End: time.Now().Add(-time.Hour)}
	for name, tt := range map[string]struct {
		elsewhere   func(store *fakeStore)
		wantDeleted bool
		// wantGone are the rows outside the tenant that go with the account.
		wantGone []string
	}{
		"nothing else: the account goes": {
			elsewhere: func(*fakeStore) {}, wantDeleted: true,
		},
		"a role in another tenant keeps it": {
			elsewhere: func(store *fakeStore) { store.memberships[danaBetaReader] = true },
		},
		"a role in another tenant that has ended goes with the account": {
			elsewhere: func(store *fakeStore) {
				store.memberships[danaBetaReader] = true
				store.membershipPeriods[danaBetaReader] = ended
			},
			wantDeleted: true,
			wantGone:    []string{"delete membership " + membershipText(&danaBetaReader)},
		},
		"a place in another tenant's group that has ended goes with the account": {
			elsewhere: func(store *fakeStore) {
				store.groupMembers[memberKey(dayID, userID)] = true
				store.placePeriods[memberKey(dayID, userID)] = ended
			},
			wantDeleted: true,
			wantGone:    []string{"delete group member " + dayID.String() + " " + string(dana)},
		},
		"a role in another tenant ended by the server's clock and not the database's keeps it": {
			elsewhere: func(store *fakeStore) {
				store.memberships[danaBetaReader] = true
				store.membershipPeriods[danaBetaReader] = ended
				store.databaseNow = func() time.Time { return time.Now().Add(-2 * time.Hour) }
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := removalStore()
			store.memberships[danaAlphaReader] = true
			tt.elsewhere(store)
			m, engine, mail := removalManager(t, store, &fakeGrants{}, false)

			judged, err := judgeDana(t, m, asErinIn, "alpha")
			if err != nil {
				t.Fatalf("JudgeTenantRemoval() error = %v", err)
			}
			if judged.DeletesAccount != tt.wantDeleted {
				t.Errorf("JudgeTenantRemoval() DeletesAccount = %v, want %v", judged.DeletesAccount, tt.wantDeleted)
			}
			deleted, err := removeDana(t, m, asErinIn, "alpha")
			if err != nil {
				t.Fatalf("RemoveFromTenant() error = %v", err)
			}
			if deleted != tt.wantDeleted {
				t.Errorf("RemoveFromTenant() deleted = %v, want %v", deleted, tt.wantDeleted)
			}
			want := []string{"delete membership " + membershipText(&danaAlphaReader)}
			if tt.wantDeleted {
				want = append([]string{
					"end session " + danaSession.String(),
					"delete identity " + identityA.String(),
					"delete user " + userID.String(),
					"delete membership " + membershipText(&danaAlphaReader),
				}, tt.wantGone...)
			}
			if diff := cmp.Diff(want, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != 1 {
				t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
			}
			if len(mail.asked)+len(mail.sent) != 0 {
				t.Errorf("mailed %v and asked for %v, want nothing", mail.sent, mail.asked)
			}
		})
	}
}

// TestManager_RemoveFromTenant_noNotice pins that nobody is mailed where the person stays
// one of the tenant's people another way, by a role of their own held in every tenant or
// through a group kept for the whole application that holds a role in the tenant or in
// every tenant for them, or where they confirmed no address. Their rows in the tenant go
// all the same.
func TestManager_RemoveFromTenant_noNotice(t *testing.T) {
	t.Parallel()

	staffEveryRead := access.NewGroupRoleKey(GroupID(staffID), everyScope, "Reader")
	for name, held := range map[string]func(store *fakeStore){
		"a role of their own in every tenant": func(store *fakeStore) { store.memberships[danaEvery] = true },
		"a group kept for the whole application that holds a role in the tenant": func(store *fakeStore) {
			store.groupMembers[memberKey(staffID, userID)] = true
			store.groupRoles[staffAlphaRead] = true
		},
		"a group kept for the whole application that holds a role in every tenant": func(store *fakeStore) {
			store.groupMembers[memberKey(staffID, userID)] = true
			store.groupRoles[staffEveryRead] = true
		},
		"no confirmed address": func(store *fakeStore) {
			store.emails = nil
			store.memberships[danaBetaReader] = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := removalStore()
			store.memberships[danaAlphaReader] = true
			held(store)
			m, _, mail := removalManager(t, store, &fakeGrants{}, false)

			deleted, err := removeDana(t, m, asErinIn, "alpha")
			if err != nil {
				t.Fatalf("RemoveFromTenant() error = %v", err)
			}
			if deleted {
				t.Error("RemoveFromTenant() deleted = true, want the account kept")
			}
			if diff := cmp.Diff([]string{"delete membership " + membershipText(&danaAlphaReader)}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if len(mail.asked)+len(mail.sent) != 0 {
				t.Errorf("mailed %v and asked for %v, want nothing", mail.sent, mail.asked)
			}
		})
	}
}

// TestManager_RemoveFromTenant_staysNow pins that a person stays one of the tenant's people
// only by what reaches it now: a role of their own held in every tenant, a place in a
// group kept for the whole application, or that group's role, whose period has not begun
// keeps nobody among the tenant's people. The warning says the account stays and nothing
// more, nobody is mailed, and the account stands, since what has not begun is still held.
func TestManager_RemoveFromTenant_staysNow(t *testing.T) {
	t.Parallel()

	upcoming := accesstypes.Period{Start: time.Now().Add(time.Hour)}
	staffEveryRead := access.NewGroupRoleKey(GroupID(staffID), everyScope, "Reader")
	for name, held := range map[string]func(store *fakeStore){
		"a role of their own in every tenant that has not begun": func(store *fakeStore) {
			store.memberships[danaEvery] = true
			store.membershipPeriods[danaEvery] = upcoming
		},
		"a place that has not begun in a group kept for the whole application": func(store *fakeStore) {
			store.groupMembers[memberKey(staffID, userID)] = true
			store.placePeriods[memberKey(staffID, userID)] = upcoming
			store.groupRoles[staffEveryRead] = true
		},
		"a place in a group kept for the whole application whose role in the tenant has not begun": func(store *fakeStore) {
			store.groupMembers[memberKey(staffID, userID)] = true
			store.groupRoles[staffAlphaRead] = true
			store.rolePeriods[staffAlphaRead] = upcoming
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := removalStore()
			store.memberships[danaAlphaReader] = true
			held(store)
			m, _, mail := removalManager(t, store, &fakeGrants{}, false)

			judged, err := judgeDana(t, m, asErinIn, "alpha")
			if err != nil {
				t.Fatalf("JudgeTenantRemoval() error = %v", err)
			}
			if judged.Stays || judged.DeletesAccount {
				t.Errorf("JudgeTenantRemoval() Stays = %v and DeletesAccount = %v, want neither", judged.Stays, judged.DeletesAccount)
			}
			var warning *resource.Warning
			err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = asErin(ctx, managedAuth, true)

				return runInTenant(ctx, m, func(tenant *Tenant) error {
					var err error
					warning, err = tenant.RemovalWarning(ctx, txn, userID)

					return err
				})
			})
			if err != nil {
				t.Fatalf("RemovalWarning() error = %v", err)
			}
			if diff := cmp.Diff([]resource.WarningNote{{Says: removalKeepsSays, Names: nil}}, warning.Also); diff != "" {
				t.Errorf("RemovalWarning() notes (-want +got):\n%s", diff)
			}
			deleted, err := removeDana(t, m, asErinIn, "alpha")
			if err != nil {
				t.Fatalf("RemoveFromTenant() error = %v", err)
			}
			if deleted {
				t.Error("RemoveFromTenant() deleted = true, want the account kept by what has not begun")
			}
			if len(mail.asked)+len(mail.sent) != 0 {
				t.Errorf("mailed %v and asked for %v, want nothing", mail.sent, mail.asked)
			}
		})
	}
}

// TestManager_RemoveFromTenant_refused pins the refusals of a removal, each writing,
// mailing and telling nothing, and the judgment refusing each the same: nobody removes
// themselves, the account they view as, or, in a session that acts as someone else, the
// real person behind it; a request names the tenant; a person who holds nothing in the
// tenant itself, a role in every tenant alone among them, is not found there, nor is an
// account that does not exist; an administrator's role of a directory-only tenant is
// taken only by a caller whose session enters it; a role of the person's own goes only
// where the caller may take it away; and a place in a group of the tenant goes only where
// the caller may take each role the group gives.
func TestManager_RemoveFromTenant_refused(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		held    func(store *fakeStore)
		request func(context.Context) context.Context
		domain  accesstypes.Domain
		unnamed bool
		grants  *fakeGrants
		// tenantAdministrators are the roles the engine answers who administers a tenant
		// by.
		tenantAdministrators []accesstypes.Role
		wantErr              func(error) bool
		// wantSays is the refusal's words, and wantContains words the refusal holds.
		wantSays     string
		wantContains string
	}{
		"the caller's own account": {
			request: func(ctx context.Context) context.Context { return asCaller(ctx, dana) },
			domain:  "alpha", wantErr: httpio.HasForbidden, wantSays: removingYourselfSays,
		},
		"the caller's own account, where the application named no administrator roles": {
			request: func(ctx context.Context) context.Context { return asCaller(ctx, dana) },
			domain:  "alpha", unnamed: true, wantErr: httpio.HasForbidden, wantSays: removingYourselfUnnamedSays,
		},
		"the account the session views as": {
			request: func(ctx context.Context) context.Context { return viewedBy(asCaller(ctx, dana), otherUser) },
			domain:  "alpha", wantErr: httpio.HasForbidden, wantSays: removingViewedAsSays,
		},
		"the real person behind a session that acts as someone else": {
			request: func(ctx context.Context) context.Context { return viewedBy(asCaller(ctx, erin), userID) },
			domain:  "alpha", wantErr: httpio.HasForbidden, wantContains: actingAsSomeoneElse + "remove yourself from a sector.",
		},
		"no tenant named": {
			request: asErinIn, domain: "", wantErr: httpio.HasBadRequest, wantSays: noTenantToRemoveFromSays,
		},
		"a person who holds nothing in the tenant": {
			request: asErinIn, domain: "beta", wantErr: httpio.HasNotFound, wantSays: nothingToRemoveSays,
		},
		"a person who holds a role in every tenant alone among what reaches it": {
			held:    func(store *fakeStore) { store.memberships[danaEvery] = true },
			request: asErinIn, domain: "beta", wantErr: httpio.HasNotFound, wantSays: nothingToRemoveSays,
		},
		"an account that does not exist": {
			held:    func(store *fakeStore) { delete(store.usernames, userID) },
			request: asErinIn, domain: "alpha", wantErr: httpio.HasNotFound, wantSays: accountNotFoundMessage,
		},
		"an administrator's role of a directory-only tenant the caller's session does not enter": {
			held:                 func(store *fakeStore) { store.memberships[danaAlphaWarden] = true },
			request:              func(ctx context.Context) context.Context { return asEntering(ctx, erin) },
			domain:               "alpha",
			tenantAdministrators: []accesstypes.Role{"Warden"},
			wantErr:              httpio.HasForbidden, wantSays: wardenKeptFromOutsidersSays,
		},
		"a role of the person's own in the tenant that the caller may not take": {
			request: asErinIn, domain: "alpha", grants: &fakeGrants{noTake: []accesstypes.Role{"Reader"}},
			wantErr: httpio.HasForbidden, wantSays: removalTakesReaderSays,
		},
		"a place in a group of the tenant that gives a role the caller may not take": {
			// dana holds nothing of her own in the tenant, so the group's role alone is
			// judged.
			held: func(store *fakeStore) {
				delete(store.memberships, danaAlphaReader)
				store.groupMembers[memberKey(nightID, userID)] = true
				store.groupRoles[nightReader] = true
			},
			request: asErinIn, domain: "alpha", grants: &fakeGrants{noTake: []accesstypes.Role{"Reader"}},
			wantErr: httpio.HasForbidden, wantSays: "Taking a person out of the group Night shift takes away the role Reader, which you may not take from them.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := removalStore()
			// A role of the whole application keeps the account, so each case is judged on
			// its rule alone.
			store.memberships[danaAlphaReader], store.memberships[danaGlobal] = true, true
			if tt.held != nil {
				tt.held(store)
			}
			grants := tt.grants
			if grants == nil {
				grants = &fakeGrants{}
			}
			m, engine, mail := removalManager(t, store, grants, tt.unnamed, tt.tenantAdministrators...)
			refused := func(what string, err error) {
				t.Helper()
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("%s error = %v, want the refusal %q", what, err, tt.wantSays+tt.wantContains)
				}
				if tt.wantSays != "" && httpio.Message(err) != tt.wantSays {
					t.Errorf("%s refusal = %q, want %q", what, httpio.Message(err), tt.wantSays)
				}
				if !strings.Contains(httpio.Message(err), tt.wantContains) {
					t.Errorf("%s refusal = %q, want it to hold %q", what, httpio.Message(err), tt.wantContains)
				}
			}

			_, err := judgeDana(t, m, tt.request, tt.domain)
			refused("JudgeTenantRemoval()", err)
			_, err = removeDana(t, m, tt.request, tt.domain)
			refused("RemoveFromTenant()", err)
			if len(store.writes) != 0 || engine.changed != 0 {
				t.Errorf("buffered writes = %v and the engine told %d time(s), want nothing", store.writes, engine.changed)
			}
			if len(mail.asked)+len(mail.sent) != 0 {
				t.Errorf("mailed %v and asked for %v, want nothing", mail.sent, mail.asked)
			}
		})
	}
}

// TestManager_RemoveFromTenant_takingRoles pins that a removal gives nobody a power that
// taking each role away directly would not: each role of the person's own held in the
// tenant that has not ended is asked of the grant that takes one role away, for that
// role, and the removal goes where each is one the caller may take. A role whose period
// has ended is not asked, a call outside any request is not judged, and a request made
// to a manager built without the grants is an error, never a pass.
func TestManager_RemoveFromTenant_takingRoles(t *testing.T) {
	t.Parallel()

	ended := accesstypes.Period{End: time.Now().Add(-time.Hour)}
	t.Run("every role one the caller may take: each that has not ended is asked, and the removal goes", func(t *testing.T) {
		t.Parallel()

		store := removalStore()
		for _, key := range []access.MembershipKey{danaAlphaReader, danaAlphaLead, danaBetaReader} {
			store.memberships[key] = true
		}
		store.membershipPeriods[danaAlphaLead] = ended
		grants := &fakeGrants{}
		m, _, _ := removalManager(t, store, grants, false)

		if _, err := judgeDana(t, m, asErinIn, "alpha"); err != nil {
			t.Fatalf("JudgeTenantRemoval() error = %v", err)
		}
		if diff := cmp.Diff([]string{"take " + membershipText(&danaAlphaReader)}, grants.asked); diff != "" {
			t.Errorf("the grants asked by the judgment (-want +got):\n%s", diff)
		}
		grants.asked = nil
		if _, err := removeDana(t, m, asErinIn, "alpha"); err != nil {
			t.Fatalf("RemoveFromTenant() error = %v", err)
		}
		if diff := cmp.Diff([]string{"take " + membershipText(&danaAlphaReader)}, grants.asked); diff != "" {
			t.Errorf("the grants asked by the removal (-want +got):\n%s", diff)
		}
		want := []string{
			"delete membership " + membershipText(&danaAlphaLead),
			"delete membership " + membershipText(&danaAlphaReader),
		}
		if diff := cmp.Diff(want, store.writes); diff != "" {
			t.Errorf("buffered writes (-want +got):\n%s", diff)
		}
	})

	t.Run("an ended role the caller may not take does not refuse", func(t *testing.T) {
		t.Parallel()

		store := removalStore()
		for _, key := range []access.MembershipKey{danaAlphaReader, danaAlphaLead, danaBetaReader} {
			store.memberships[key] = true
		}
		store.membershipPeriods[danaAlphaLead] = ended
		m, _, _ := removalManager(t, store, &fakeGrants{noTake: []accesstypes.Role{"NightShiftLead"}}, false)

		if _, err := removeDana(t, m, asErinIn, "alpha"); err != nil {
			t.Fatalf("RemoveFromTenant() error = %v, want the ended role taken with the rest", err)
		}
		if len(store.writes) != 2 {
			t.Errorf("buffered writes = %v, want both of dana's rows in the tenant deleted", store.writes)
		}
	})

	t.Run("a call outside any request is not judged", func(t *testing.T) {
		t.Parallel()

		store := removalStore()
		store.memberships[danaAlphaReader], store.memberships[danaBetaReader] = true, true
		grants := &fakeGrants{noTake: []accesstypes.Role{"Reader"}}
		m, _, _ := removalManager(t, store, grants, false)

		outside := func(ctx context.Context) context.Context { return ctx }
		if _, err := removeDana(t, m, outside, "alpha"); err != nil {
			t.Fatalf("RemoveFromTenant() error = %v, want the application's own act unjudged", err)
		}
		if len(grants.asked) != 0 {
			t.Errorf("the grants were asked %v, want nothing asked outside a request", grants.asked)
		}
		if diff := cmp.Diff([]string{"delete membership " + membershipText(&danaAlphaReader)}, store.writes); diff != "" {
			t.Errorf("buffered writes (-want +got):\n%s", diff)
		}
	})

	t.Run("a request to a manager without the grants is an error", func(t *testing.T) {
		t.Parallel()

		store := removalStore()
		store.memberships[danaAlphaReader], store.memberships[danaBetaReader] = true, true
		engine := &fakeEngine{roles: tenantRoles}
		m, err := New(managedAuth, store, engine, fakeSessions{mail: &fakeMail{}}, WithTenantWord("sector"), WithAdministratorRoles([]accesstypes.Role{"Warden"}))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		const says = "a request removes a person from a tenant and the manager holds no role grants to judge it by"
		if _, err := judgeDana(t, m, asErinIn, "alpha"); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("JudgeTenantRemoval() error = %v, want it to say %q", err, says)
		}
		if _, err := removeDana(t, m, asErinIn, "alpha"); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("RemoveFromTenant() error = %v, want it to say %q", err, says)
		}
		if len(store.writes) != 0 || engine.changed != 0 {
			t.Errorf("buffered writes = %v and the engine told %d time(s), want nothing", store.writes, engine.changed)
		}
	})
}

// TestManager_RemoveFromTenant_lastAdministrator pins that the rule on a tenant's last
// administrator is a person's own leaving's, not a removal's: another caller removes the
// tenant's only administrator.
func TestManager_RemoveFromTenant_lastAdministrator(t *testing.T) {
	t.Parallel()

	store := removalStore()
	store.memberships[danaAlphaWarden], store.memberships[danaGlobal] = true, true
	m, _, _ := removalManager(t, store, &fakeGrants{}, false)

	if _, err := removeDana(t, m, asErinIn, "alpha"); err != nil {
		t.Fatalf("RemoveFromTenant() error = %v, want the last administrator removed by another caller", err)
	}
	if diff := cmp.Diff([]string{"delete membership " + membershipText(&danaAlphaWarden)}, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
}

// TestTenant_RemovalWarning pins what a tenant's administrator is told before a removal,
// from the judgment the act runs: the consequence names the person and says whether they
// are taken out of the tenant's groups, the roles they hold there are named, the button
// says what it does, and one note says what becomes of the account: it goes, it stays
// and the person remains one of the tenant's people through a role held outside it, or it
// stays. Each warning is one a dialog can draw (resource.CheckWarning), and reading it
// writes nothing.
func TestTenant_RemovalWarning(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		held        func(store *fakeStore)
		wantJudged  *TenantRemoval
		wantWarning *resource.Warning
	}{
		"roles alone, and nothing elsewhere: the account goes": {
			held: func(*fakeStore) {},
			wantJudged: &TenantRemoval{
				Username: "dana", Roles: []string{"Night shift lead", "Reader"}, InGroups: false, DeletesAccount: true, Stays: false,
			},
			wantWarning: &resource.Warning{
				Consequence: removalSays, Names: []string{"Night shift lead", "Reader"}, Confirm: removalConfirmSays,
				Also: []resource.WarningNote{{Says: removalDeletesSays, Names: nil}},
			},
		},
		"a place in a group of the tenant, and a role in every tenant: the person stays one of its people": {
			held: func(store *fakeStore) {
				store.memberships[danaEvery] = true
				store.groupMembers[memberKey(nightID, userID)] = true
			},
			wantJudged: &TenantRemoval{
				Username: "dana", Roles: []string{"Night shift lead", "Reader"}, InGroups: true, DeletesAccount: false, Stays: true,
			},
			wantWarning: &resource.Warning{
				Consequence: removalWithGroupsSays, Names: []string{"Night shift lead", "Reader"}, Confirm: removalConfirmSays,
				Also: []resource.WarningNote{{Says: removalStaysSays, Names: nil}},
			},
		},
		"a role in another tenant: the account stays": {
			held: func(store *fakeStore) { store.memberships[danaBetaReader] = true },
			wantJudged: &TenantRemoval{
				Username: "dana", Roles: []string{"Night shift lead", "Reader"}, InGroups: false, DeletesAccount: false, Stays: false,
			},
			wantWarning: &resource.Warning{
				Consequence: removalSays, Names: []string{"Night shift lead", "Reader"}, Confirm: removalConfirmSays,
				Also: []resource.WarningNote{{Says: removalKeepsSays, Names: nil}},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := removalStore()
			store.memberships[danaAlphaReader], store.memberships[danaAlphaLead] = true, true
			tt.held(store)
			m, engine, mail := removalManager(t, store, &fakeGrants{}, false)

			judged, err := judgeDana(t, m, asErinIn, "alpha")
			if err != nil {
				t.Fatalf("JudgeTenantRemoval() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantJudged, judged); diff != "" {
				t.Errorf("JudgeTenantRemoval() (-want +got):\n%s", diff)
			}
			var warning *resource.Warning
			err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				ctx = asErin(ctx, managedAuth, true)

				return runInTenant(ctx, m, func(tenant *Tenant) error {
					var err error
					warning, err = tenant.RemovalWarning(ctx, txn, userID)

					return err
				})
			})
			if err != nil {
				t.Fatalf("RemovalWarning() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWarning, warning); diff != "" {
				t.Errorf("RemovalWarning() (-want +got):\n%s", diff)
			}
			if err := resource.CheckWarning("RemoveSectorsUser", warning); err != nil {
				t.Errorf("resource.CheckWarning() error = %v", err)
			}
			if len(store.writes) != 0 || engine.changed != 0 || len(mail.asked) != 0 {
				t.Errorf("buffered writes = %v, the engine told %d time(s) and notices %v, want nothing from a warning", store.writes, engine.changed, mail.asked)
			}
		})
	}

	t.Run("a place in a group of the tenant that holds no role there: no roles are named", func(t *testing.T) {
		t.Parallel()

		store := removalStore()
		store.groupMembers[memberKey(nightID, userID)] = true
		store.memberships[danaBetaReader] = true
		m, _, _ := removalManager(t, store, &fakeGrants{}, false)

		var warning *resource.Warning
		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			ctx = asErin(ctx, managedAuth, true)

			return runInTenant(ctx, m, func(tenant *Tenant) error {
				var err error
				warning, err = tenant.RemovalWarning(ctx, txn, userID)

				return err
			})
		})
		if err != nil {
			t.Fatalf("RemovalWarning() error = %v", err)
		}
		want := &resource.Warning{
			Consequence: removalWithGroupsSays, Names: nil, Confirm: removalConfirmSays,
			Also: []resource.WarningNote{{Says: removalKeepsSays, Names: nil}},
		}
		if diff := cmp.Diff(want, warning); diff != "" {
			t.Errorf("RemovalWarning() (-want +got):\n%s", diff)
		}
	})
}

// TestTenant_RemovePerson pins that a tenant's method removes the person from the tenant
// its request was checked in, and from no other.
func TestTenant_RemovePerson(t *testing.T) {
	t.Parallel()

	store := removalStore()
	store.memberships[danaAlphaReader], store.memberships[danaBetaReader] = true, true
	m, engine, _ := removalManager(t, store, &fakeGrants{}, false)

	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		ctx = asErin(ctx, managedAuth, true)

		return runInTenant(ctx, m, func(tenant *Tenant) error {
			return tenant.RemovePerson(ctx, txn, userID)
		})
	})
	if err != nil {
		t.Fatalf("RemovePerson() error = %v", err)
	}
	if diff := cmp.Diff([]string{"delete membership " + membershipText(&danaAlphaReader)}, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told the policy changed %d time(s), want once", engine.changed)
	}
}
