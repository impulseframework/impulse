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

// The sentences of a person's leaving, in the word the tests' manager calls a tenant.
const (
	lastAdministratorSays = "You are the last administrator of this sector. Give another person the role before you leave."
	noAdministratorsSays  = "You cannot leave this sector yourself. Ask one of its administrators to remove you."
	notInTenantSays       = "You hold no role in that sector."
	changedSays           = "What you hold changed while this page was open. Look at the list again before you leave."
	noTenantNamedSays     = "Name the sector to leave."
)

// The rows the tests of a person's tenants seat dana and gale in: Warden administers a
// tenant, Reader and NightShiftLead do not; alpha and beta are tenants, nightID is a
// group kept in alpha, dayID one kept in beta and staffID one kept for the whole
// application (groupStore).
var (
	danaAlphaWarden = access.NewMembershipKey(alphaScope, dana, "Warden")
	danaAlphaReader = access.NewMembershipKey(alphaScope, dana, "Reader")
	danaAlphaLead   = access.NewMembershipKey(alphaScope, dana, "NightShiftLead")
	danaBetaReader  = access.NewMembershipKey(betaScope, dana, "Reader")
	danaEvery       = access.NewMembershipKey(everyScope, dana, "Reader")
	danaGlobal      = access.NewMembershipKey(globalScope, dana, "Steward")
	galeAlphaWarden = access.NewMembershipKey(alphaScope, erin, "Warden")
	galeEveryWarden = access.NewMembershipKey(everyScope, erin, "Warden")
	nightWarden     = access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Warden")
	nightWriter     = access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Writer")
	nightReader     = access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")
	staffAlphaRead  = access.NewGroupRoleKey(GroupID(staffID), alphaScope, "Reader")
	staffAlphaWard  = access.NewGroupRoleKey(GroupID(staffID), alphaScope, "Warden")
)

// leavingStore is a fake store holding the three groups (groupStore), with dana and gale
// accounts that can sign in and dana signed in to one session through one sign-in
// identity, which go with her account.
func leavingStore() *fakeStore {
	store := groupStore()
	store.disabled = map[impulse.UUID]bool{}
	store.sessions = map[impulse.UUID][]impulse.UUID{userID: {danaSession}}
	store.identities = map[impulse.UUID]impulse.UUID{identityA: userID}
	store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{}
	store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{}
	store.rolePeriods = map[access.GroupRoleKey]accesstypes.Period{}

	return store
}

// leavingManager builds a manager over the store that calls a tenant a sector, mails
// through a mailbox and, unless unnamed, names Warden as the role that administers a
// tenant.
func leavingManager(t *testing.T, store *fakeStore, unnamed bool) (*Manager, *fakeEngine, *fakeMail) {
	t.Helper()

	opts := []Option{WithTenantWord("sector")}
	if !unnamed {
		opts = append(opts, WithAdministratorRoles([]accesstypes.Role{"Warden"}))
	}
	mail := &fakeMail{}
	engine := &fakeEngine{roles: tenantRoles}
	m, err := New(managedAuth, store, engine, fakeSessions{mail: mail}, opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m, engine, mail
}

// listTenants reads dana's tenants as her own request reads them.
func listTenants(t *testing.T, m *Manager) []PersonTenant {
	t.Helper()

	var tenants []PersonTenant
	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		tenants, err = m.PersonTenants(asCaller(ctx, dana), txn, userID)

		return err
	})
	if err != nil {
		t.Fatalf("PersonTenants() error = %v", err)
	}

	return tenants
}

// leave has dana leave the tenant as her own request does.
func leave(t *testing.T, m *Manager, domain accesstypes.Domain, deletesAccount bool) (bool, error) {
	t.Helper()

	var deleted bool
	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		var err error
		deleted, err = m.LeaveTenant(asCaller(ctx, dana), txn, userID, domain, deletesAccount)

		return err
	})

	return deleted, err
}

// TestManager_PersonTenants pins which tenants a person's list holds and what each says:
// a tenant they hold something in, in the tenant itself, by a role of their own or a
// place in a group of the tenant, that has not ended; the roles they hold there in words,
// each once and sorted; and nothing held in every tenant, for the whole application or
// through a group kept for the whole application.
func TestManager_PersonTenants(t *testing.T) {
	t.Parallel()

	hour := time.Hour
	for name, tt := range map[string]struct {
		held func(store *fakeStore)
		want []PersonTenant
	}{
		"roles of the person's own in two tenants list both, in words and sorted": {
			held: func(store *fakeStore) {
				store.memberships[danaAlphaReader], store.memberships[danaAlphaLead], store.memberships[danaBetaReader] = true, true, true
			},
			want: []PersonTenant{
				{Domain: "alpha", Roles: []string{"Night shift lead", "Reader"}},
				{Domain: "beta", Roles: []string{"Reader"}},
			},
		},
		"a place in a tenant's group lists the tenant with the group's roles there": {
			held: func(store *fakeStore) {
				store.groupMembers[memberKey(nightID, userID)] = true
				store.groupRoles[nightWriter], store.groupRoles[nightReader] = true, true
			},
			want: []PersonTenant{{Domain: "alpha", Roles: []string{"Reader", "Writer"}, DeletesAccount: true}},
		},
		"a role held both ways is said once": {
			held: func(store *fakeStore) {
				store.memberships[danaAlphaReader] = true
				store.groupMembers[memberKey(nightID, userID)] = true
				store.groupRoles[nightReader] = true
			},
			want: []PersonTenant{{Domain: "alpha", Roles: []string{"Reader"}, DeletesAccount: true}},
		},
		"a group's role that has ended is not said, and a place whose group holds nothing now lists the tenant with no role": {
			held: func(store *fakeStore) {
				store.groupMembers[memberKey(nightID, userID)] = true
				store.groupRoles[nightWriter] = true
				store.rolePeriods[nightWriter] = accesstypes.Period{End: time.Now().Add(-hour)}
			},
			want: []PersonTenant{{Domain: "alpha", Roles: []string{}, DeletesAccount: true}},
		},
		"a role in every tenant, a role for the whole application and a place in a whole-application group list nothing": {
			held: func(store *fakeStore) {
				store.memberships[danaEvery], store.memberships[danaGlobal] = true, true
				store.groupMembers[memberKey(staffID, userID)] = true
				store.groupRoles[staffAlphaRead] = true
			},
			want: []PersonTenant{},
		},
		"a membership that has ended lists nothing": {
			held: func(store *fakeStore) {
				store.memberships[danaAlphaReader] = true
				store.membershipPeriods[danaAlphaReader] = accesstypes.Period{End: time.Now().Add(-hour)}
			},
			want: []PersonTenant{},
		},
		"a place that has ended lists nothing": {
			held: func(store *fakeStore) {
				store.groupMembers[memberKey(nightID, userID)] = true
				store.groupRoles[nightReader] = true
				store.placePeriods[memberKey(nightID, userID)] = accesstypes.Period{End: time.Now().Add(-hour)}
			},
			want: []PersonTenant{},
		},
		"a membership that has not begun lists": {
			held: func(store *fakeStore) {
				store.memberships[danaAlphaReader] = true
				store.membershipPeriods[danaAlphaReader] = accesstypes.Period{Start: time.Now().Add(hour)}
			},
			want: []PersonTenant{{Domain: "alpha", Roles: []string{"Reader"}, DeletesAccount: true}},
		},
		"nothing held: an empty list": {
			held: func(*fakeStore) {},
			want: []PersonTenant{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := leavingStore()
			tt.held(store)
			m, _, _ := leavingManager(t, store, false)

			got := listTenants(t, m)
			if got == nil {
				t.Fatal("PersonTenants() = nil, want a list that is never nil")
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("PersonTenants() (-want +got):\n%s", diff)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes = %v, want none from a read", store.writes)
			}
		})
	}
}

// TestManager_LeaveTenant_rows pins what a leaving takes: every row the person has in the
// tenant, a membership whose period has ended among them, and each place in a group of
// the tenant, and no row anywhere else, the person's own in another tenant, every tenant
// or the whole application, a place in a whole-application group, or another person's.
// The engine is told once, and nothing is mailed.
func TestManager_LeaveTenant_rows(t *testing.T) {
	t.Parallel()

	store := leavingStore()
	for _, key := range []access.MembershipKey{danaAlphaReader, danaAlphaLead, danaBetaReader, danaEvery, danaGlobal, galeAlphaWarden} {
		store.memberships[key] = true
	}
	store.membershipPeriods[danaAlphaLead] = accesstypes.Period{End: time.Now().Add(-time.Hour)}
	store.groupMembers[memberKey(nightID, userID)], store.groupMembers[memberKey(staffID, userID)] = true, true
	store.groupMembers[memberKey(dayID, userID)], store.groupMembers[memberKey(nightID, otherUser)] = true, true
	store.groupRoles[nightReader], store.groupRoles[staffAlphaRead] = true, true
	m, engine, mail := leavingManager(t, store, false)

	deleted, err := leave(t, m, "alpha", false)
	if err != nil {
		t.Fatalf("LeaveTenant() error = %v", err)
	}
	if deleted {
		t.Error("LeaveTenant() deleted = true, want the account kept by what dana holds elsewhere")
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

// TestManager_LeaveTenant_lastAdministrator pins the rule that keeps a tenant's last
// administrator, judged for the whole leaving, on the list and on the act alike: a person
// who administers the tenant, by a membership of their own or through a group of the
// tenant, is refused while nobody else who can sign in does; another administrator of the
// tenant's own lets them go; one whose account is disabled, one who holds the role in
// every tenant only and one who holds it through a group kept for the whole application
// do not.
func TestManager_LeaveTenant_lastAdministrator(t *testing.T) {
	t.Parallel()

	byMembership := func(store *fakeStore) { store.memberships[danaAlphaWarden] = true }
	throughGroup := func(store *fakeStore) {
		store.groupMembers[memberKey(nightID, userID)] = true
		store.groupRoles[nightWarden] = true
	}
	for name, tt := range map[string]struct {
		held     func(store *fakeStore)
		other    func(store *fakeStore)
		disabled bool
		wantSays string
	}{
		"the only administrator, by a membership: refused": {
			held: byMembership, other: func(*fakeStore) {}, wantSays: lastAdministratorSays,
		},
		"the only administrator, through a group of the tenant: refused": {
			held: throughGroup, other: func(*fakeStore) {}, wantSays: lastAdministratorSays,
		},
		"the only administrator by a membership and through a group: refused": {
			held: func(store *fakeStore) { byMembership(store); throughGroup(store) }, other: func(*fakeStore) {}, wantSays: lastAdministratorSays,
		},
		"another administrator who can sign in: the person leaves": {
			held: byMembership, other: func(store *fakeStore) { store.memberships[galeAlphaWarden] = true },
		},
		"another administrator through the group: the person leaves": {
			held: throughGroup, other: func(store *fakeStore) { store.groupMembers[memberKey(nightID, otherUser)] = true },
		},
		"the other administrator is disabled: refused": {
			held: byMembership, other: func(store *fakeStore) { store.memberships[galeAlphaWarden] = true }, disabled: true, wantSays: lastAdministratorSays,
		},
		"the other holds the role in every tenant only: refused": {
			held: byMembership, other: func(store *fakeStore) { store.memberships[galeEveryWarden] = true }, wantSays: lastAdministratorSays,
		},
		"the other holds the role through a group kept for the whole application: refused": {
			held: byMembership,
			other: func(store *fakeStore) {
				store.groupMembers[memberKey(staffID, otherUser)] = true
				store.groupRoles[staffAlphaWard] = true
			},
			wantSays: lastAdministratorSays,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := leavingStore()
			// A role of the whole application keeps the account, so the act is judged
			// on the rule alone.
			store.memberships[danaGlobal] = true
			tt.held(store)
			tt.other(store)
			store.disabled[otherUser] = tt.disabled
			m, _, _ := leavingManager(t, store, false)

			listed := listTenants(t, m)
			if len(listed) != 1 || listed[0].Refusal != tt.wantSays {
				t.Errorf("PersonTenants() = %+v, want alpha alone with the refusal %q", listed, tt.wantSays)
			}
			_, err := leave(t, m, "alpha", false)
			if tt.wantSays == "" {
				if err != nil {
					t.Fatalf("LeaveTenant() error = %v, want the person to leave", err)
				}
				if len(store.writes) == 0 {
					t.Error("LeaveTenant() buffered nothing, want the person's rows in alpha deleted")
				}

				return
			}
			if !httpio.HasForbidden(err) || httpio.Message(err) != tt.wantSays {
				t.Fatalf("LeaveTenant() error = %v, want the refusal %q", err, tt.wantSays)
			}
			if len(store.writes) != 0 {
				t.Errorf("buffered writes after the refusal = %v, want none", store.writes)
			}
		})
	}
}

// TestManager_LeaveTenant_noAdministratorRoles pins that where the application named no
// administrator roles nobody leaves a tenant themselves, as nobody removes their own role
// in one: the list says so on each row and the act is refused with the same words.
func TestManager_LeaveTenant_noAdministratorRoles(t *testing.T) {
	t.Parallel()

	store := leavingStore()
	store.memberships[danaAlphaReader], store.memberships[danaBetaReader] = true, true
	m, _, _ := leavingManager(t, store, true)

	want := []PersonTenant{
		{Domain: "alpha", Roles: []string{"Reader"}, Refusal: noAdministratorsSays},
		{Domain: "beta", Roles: []string{"Reader"}, Refusal: noAdministratorsSays},
	}
	if diff := cmp.Diff(want, listTenants(t, m)); diff != "" {
		t.Errorf("PersonTenants() (-want +got):\n%s", diff)
	}
	_, err := leave(t, m, "alpha", false)
	if !httpio.HasForbidden(err) || httpio.Message(err) != noAdministratorsSays {
		t.Fatalf("LeaveTenant() error = %v, want the refusal %q", err, noAdministratorsSays)
	}
	if len(store.writes) != 0 {
		t.Errorf("buffered writes after the refusal = %v, want none", store.writes)
	}
}

// TestManager_LeaveTenant_account pins when the account goes with the leaving: when
// nothing else keeps it, with its sign-in identities and sessions, in the same
// transaction; it stays for anything held anywhere else, ended or not. The list's
// DeletesAccount says the same in each case, and no notice is mailed either way.
func TestManager_LeaveTenant_account(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		elsewhere   func(store *fakeStore)
		wantDeleted bool
	}{
		"nothing else: the account goes": {
			elsewhere: func(*fakeStore) {}, wantDeleted: true,
		},
		"a role for the whole application keeps it": {
			elsewhere: func(store *fakeStore) { store.memberships[danaGlobal] = true },
		},
		"a role in every tenant keeps it": {
			elsewhere: func(store *fakeStore) { store.memberships[danaEvery] = true },
		},
		"a role in another tenant keeps it": {
			elsewhere: func(store *fakeStore) { store.memberships[danaBetaReader] = true },
		},
		"an ended row in another tenant keeps it": {
			elsewhere: func(store *fakeStore) {
				store.memberships[danaBetaReader] = true
				store.membershipPeriods[danaBetaReader] = accesstypes.Period{End: time.Now().Add(-time.Hour)}
			},
		},
		"a place in a whole-application group keeps it": {
			elsewhere: func(store *fakeStore) { store.groupMembers[memberKey(staffID, userID)] = true },
		},
		"a place in another tenant's group keeps it": {
			elsewhere: func(store *fakeStore) { store.groupMembers[memberKey(dayID, userID)] = true },
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := leavingStore()
			store.memberships[danaAlphaReader] = true
			tt.elsewhere(store)
			m, engine, mail := leavingManager(t, store, false)

			listed := listTenants(t, m)
			if len(listed) == 0 || listed[0].Domain != "alpha" || listed[0].DeletesAccount != tt.wantDeleted {
				t.Errorf("PersonTenants() = %+v, want alpha first with DeletesAccount %v", listed, tt.wantDeleted)
			}
			deleted, err := leave(t, m, "alpha", tt.wantDeleted)
			if err != nil {
				t.Fatalf("LeaveTenant() error = %v", err)
			}
			if deleted != tt.wantDeleted {
				t.Errorf("LeaveTenant() deleted = %v, want %v", deleted, tt.wantDeleted)
			}
			want := []string{"delete membership " + membershipText(&danaAlphaReader)}
			if tt.wantDeleted {
				want = []string{
					"end session " + danaSession.String(),
					"delete identity " + identityA.String(),
					"delete user " + userID.String(),
					"delete membership " + membershipText(&danaAlphaReader),
				}
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

// TestManager_LeaveTenant_toldOtherwise pins that a leaving does what the list told the
// person or nothing: a request that says the account stays where it would go, or goes
// where it would stay, is refused as a Conflict and writes nothing.
func TestManager_LeaveTenant_toldOtherwise(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		held           []access.MembershipKey
		deletesAccount bool
	}{
		"told the account stays, where it would go":  {held: []access.MembershipKey{danaAlphaReader}, deletesAccount: false},
		"told the account goes, where it would stay": {held: []access.MembershipKey{danaAlphaReader, danaBetaReader}, deletesAccount: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := leavingStore()
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			m, engine, _ := leavingManager(t, store, false)

			_, err := leave(t, m, "alpha", tt.deletesAccount)
			if !httpio.HasConflict(err) || httpio.Message(err) != changedSays {
				t.Fatalf("LeaveTenant() error = %v, want the refusal %q", err, changedSays)
			}
			if len(store.writes) != 0 || engine.changed != 0 {
				t.Errorf("buffered writes = %v and the engine told %d time(s), want nothing", store.writes, engine.changed)
			}
		})
	}
}

// TestManager_LeaveTenant_notIn pins the refusals of a tenant the person cannot leave
// because they are not in it: one they hold nothing in, one that does not exist, one
// spelled otherwise, and one where all they hold has ended are Not Found alike, and a
// request that names no tenant is a Bad Request. None writes anything.
func TestManager_LeaveTenant_notIn(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		domain   accesstypes.Domain
		wantErr  func(error) bool
		wantSays string
	}{
		"a tenant the person holds nothing in": {domain: "beta", wantErr: httpio.HasNotFound, wantSays: notInTenantSays},
		"a tenant that does not exist":         {domain: "gamma", wantErr: httpio.HasNotFound, wantSays: notInTenantSays},
		"the tenant spelled in capitals":       {domain: "ALPHA", wantErr: httpio.HasNotFound, wantSays: notInTenantSays},
		"the tenant with space around it":      {domain: " alpha ", wantErr: httpio.HasNotFound, wantSays: notInTenantSays},
		"a tenant where all held has ended":    {domain: "delta", wantErr: httpio.HasNotFound, wantSays: notInTenantSays},
		"no tenant named":                      {domain: "", wantErr: httpio.HasBadRequest, wantSays: noTenantNamedSays},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := leavingStore()
			ended := access.NewMembershipKey(accesstypes.DomainPolicyScope("delta"), dana, "Reader")
			store.memberships[danaAlphaReader], store.memberships[ended] = true, true
			store.membershipPeriods[ended] = accesstypes.Period{End: time.Now().Add(-time.Hour)}
			m, engine, _ := leavingManager(t, store, false)

			_, err := leave(t, m, tt.domain, false)
			if err == nil || !tt.wantErr(err) || httpio.Message(err) != tt.wantSays {
				t.Fatalf("LeaveTenant() error = %v, want the refusal %q", err, tt.wantSays)
			}
			if len(store.writes) != 0 || engine.changed != 0 {
				t.Errorf("buffered writes = %v and the engine told %d time(s), want nothing", store.writes, engine.changed)
			}
		})
	}
}

// TestManager_LeaveTenant_impersonator pins that the real person behind a session that
// acts as someone else does not leave a tenant of their own from it, by the rule that
// nobody is the subject of what they do while they act as someone else.
func TestManager_LeaveTenant_impersonator(t *testing.T) {
	t.Parallel()

	store := leavingStore()
	store.memberships[danaAlphaReader], store.memberships[danaGlobal] = true, true
	m, _, _ := leavingManager(t, store, false)

	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		_, err := m.LeaveTenant(viewedBy(asCaller(ctx, erin), userID), txn, userID, "alpha", false)

		return err
	})
	want := actingAsSomeoneElse + "leave a sector."
	if !httpio.HasForbidden(err) || !strings.Contains(httpio.Message(err), want) {
		t.Fatalf("LeaveTenant() error = %v, want the refusal %q", err, want)
	}
	if len(store.writes) != 0 {
		t.Errorf("buffered writes = %v, want none", store.writes)
	}
}
