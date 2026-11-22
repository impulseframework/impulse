package identity

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

var (
	betaScope = accesstypes.DomainPolicyScope("beta")
	// tenantRoles are the engine's roles where two tenants exist beside the whole
	// application's and every tenant's.
	tenantRoles = map[accesstypes.PolicyScope][]accesstypes.Role{
		globalScope: {"Steward"},
		alphaScope:  {"Reader", "Writer"},
		betaScope:   {"Reader"},
		everyScope:  {"Reader"},
	}
)

// assertOtherTenant fails unless err is the refusal of a role in a second tenant: a
// Conflict, carrying the login refusal code a sign-in ends on, that names no tenant.
func assertOtherTenant(t *testing.T, err error, tenants ...string) {
	t.Helper()

	var refusal *sessioninfo.LoginRefusal
	if !httpio.HasConflict(err) || !errors.As(err, &refusal) || refusal.Code() != sessioninfo.RefusedOtherTenant {
		t.Fatalf("error = %v, want a Conflict carrying the login refusal code %s", err, sessioninfo.RefusedOtherTenant)
	}
	for _, tenant := range tenants {
		if strings.Contains(httpio.Messages(err)[0], tenant) {
			t.Errorf("the refusal %q names the tenant %s; it says the person belongs to another one and never which", httpio.Messages(err)[0], tenant)
		}
	}
}

// TestManager_AssignRole_oneTenantPerPerson pins the rule of one tenant per person on a
// role given by hand: a role in a tenant is refused for a person who holds one in
// another tenant, a role held for the whole application or in every tenant neither
// counts nor is refused, its holder is one of the application's own people and is given
// a role in any tenant, and an application that did not declare the rule gives roles in
// any number of tenants. What a person holds is what their account holds: a role another
// account holds is not held against them.
func TestManager_AssignRole_oneTenantPerPerson(t *testing.T) {
	t.Parallel()

	id := userID.String()
	holds := func(scope accesstypes.PolicyScope, role accesstypes.Role) access.MembershipKey {
		return access.NewMembershipKey(scope, dana, role)
	}
	tests := []struct {
		name    string
		holds   []access.MembershipKey
		scope   accesstypes.PolicyScope
		role    accesstypes.Role
		several bool
		// wantWrite is the membership buffered, "" for a refusal.
		wantWrite string
	}{
		{name: "a person who holds nothing is given a role in a tenant", scope: alphaScope, role: "Reader", wantWrite: "insert membership domain/alpha " + id + " Reader"},
		{
			name:  "one more role in the tenant the person belongs to is given",
			holds: []access.MembershipKey{holds(alphaScope, "Reader")}, scope: alphaScope, role: "Writer", wantWrite: "insert membership domain/alpha " + id + " Writer",
		},
		{name: "a role in another tenant is refused", holds: []access.MembershipKey{holds(betaScope, "Reader")}, scope: alphaScope, role: "Reader"},
		{
			name:  "a role another account holds in another tenant is not held against the person",
			holds: []access.MembershipKey{access.NewMembershipKey(betaScope, erin, "Reader")}, scope: alphaScope, role: "Reader", wantWrite: "insert membership domain/alpha " + id + " Reader",
		},
		{
			name:  "a role held for the whole application is no tenant's: the person is given a role in a tenant",
			holds: []access.MembershipKey{holds(globalScope, "Steward")}, scope: alphaScope, role: "Reader", wantWrite: "insert membership domain/alpha " + id + " Reader",
		},
		{
			name:  "a role held in every tenant is no tenant's either",
			holds: []access.MembershipKey{holds(everyScope, "Reader")}, scope: alphaScope, role: "Writer", wantWrite: "insert membership domain/alpha " + id + " Writer",
		},
		{
			name:  "a person of a tenant is given a role for the whole application",
			holds: []access.MembershipKey{holds(betaScope, "Reader")}, scope: globalScope, role: "Steward", wantWrite: "insert membership global/ " + id + " Steward",
		},
		{
			name:  "and a role in every tenant",
			holds: []access.MembershipKey{holds(betaScope, "Reader")}, scope: everyScope, role: "Reader", wantWrite: "insert membership every/ " + id + " Reader",
		},
		{
			name:  "one of the application's own people, who holds a role for the whole application, is given a role in a second tenant",
			holds: []access.MembershipKey{holds(globalScope, "Steward"), holds(betaScope, "Reader")}, scope: alphaScope, role: "Reader", wantWrite: "insert membership domain/alpha " + id + " Reader",
		},
		{
			name:  "and so is one who holds a role in every tenant",
			holds: []access.MembershipKey{holds(everyScope, "Reader"), holds(betaScope, "Reader")}, scope: alphaScope, role: "Writer", wantWrite: "insert membership domain/alpha " + id + " Writer",
		},
		{
			name:  "an application that did not declare the rule gives a role in a second tenant",
			holds: []access.MembershipKey{holds(betaScope, "Reader")}, scope: alphaScope, role: "Reader", several: true, wantWrite: "insert membership domain/alpha " + id + " Reader",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
			for _, key := range tt.holds {
				store.memberships[key] = true
			}
			var opts []Option
			if !tt.several {
				opts = append(opts, WithOneTenantPerPerson())
			}
			m, engine := newManager(t, store, tenantRoles, opts...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.AssignRole(ctx, txn, tt.scope, userID, tt.role)
			})
			if tt.wantWrite == "" {
				assertOtherTenant(t, err, "beta")
				if len(store.writes) != 0 || engine.changed != 0 {
					t.Errorf("a refused role buffered %v and told the engine %d time(s), want nothing", store.writes, engine.changed)
				}

				return
			}
			if err != nil {
				t.Fatalf("AssignRole() error = %v", err)
			}
			if diff := cmp.Diff([]string{tt.wantWrite}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("a failed read of what the person holds fails the call", func(t *testing.T) {
		t.Parallel()

		store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}, failOn: "Memberships"}
		m, _ := newManager(t, store, tenantRoles, WithOneTenantPerPerson())

		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.AssignRole(ctx, txn, alphaScope, userID, "Reader")
		})
		if !errors.Is(err, errStore) || len(store.writes) != 0 {
			t.Errorf("AssignRole() error = %v with %v buffered, want the store's failure and nothing written", err, store.writes)
		}
	})
}

// TestManager_HoldsTenant pins the read the generated create-your-tenant route makes in
// the transaction that writes the tenant: whether the person holds a role in some
// tenant. A membership of their own held in one tenant counts, and so does a place in a
// group kept in one; a role held for the whole application or in every tenant is no
// tenant's, a role another account holds is not theirs, and a membership whose period
// has ended places them nowhere. The answer is the same whether or not the application
// declared one tenant per person: the route's rule is its own.
func TestManager_HoldsTenant(t *testing.T) {
	t.Parallel()

	holds := func(scope accesstypes.PolicyScope, role accesstypes.Role) access.MembershipKey {
		return access.NewMembershipKey(scope, dana, role)
	}
	crewID := impulse.Must(impulse.UUIDFromString("0190b000-0000-7000-8000-0000000007a1"))
	inGroup := access.GroupMemberKey{Group: GroupID(crewID), User: User(userID)}
	tests := []struct {
		name  string
		holds []access.MembershipKey
		// ended are the memberships whose period has ended.
		ended []access.MembershipKey
		// group is the scope a group the person is in is kept in, nil for no group.
		group *accesstypes.PolicyScope
		want  bool
	}{
		{name: "a person who holds nothing holds no tenant"},
		{name: "a role in a tenant", holds: []access.MembershipKey{holds(alphaScope, "Reader")}, want: true},
		{name: "a role for the whole application is no tenant's", holds: []access.MembershipKey{holds(globalScope, "Steward")}},
		{name: "a role in every tenant is no tenant's", holds: []access.MembershipKey{holds(everyScope, "Reader")}},
		{name: "a role for the whole application beside one in a tenant", holds: []access.MembershipKey{holds(globalScope, "Steward"), holds(betaScope, "Reader")}, want: true},
		{name: "a role another account holds in a tenant is not the person's", holds: []access.MembershipKey{access.NewMembershipKey(betaScope, erin, "Reader")}},
		{name: "a role in a tenant whose period has ended places the person nowhere", holds: []access.MembershipKey{holds(alphaScope, "Reader")}, ended: []access.MembershipKey{holds(alphaScope, "Reader")}},
		{name: "a place in a group kept in a tenant", group: &alphaScope, want: true},
		{name: "a place in a group kept for the whole application is no tenant's", group: &globalScope},
	}
	for _, tt := range tests {
		for _, declared := range []bool{false, true} {
			name := tt.name
			if declared {
				name += ", where a person belongs to one tenant"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
				for _, key := range tt.holds {
					store.memberships[key] = true
				}
				past := time.Now().Add(-time.Hour)
				store.membershipPeriods = map[access.MembershipKey]accesstypes.Period{}
				for _, key := range tt.ended {
					store.membershipPeriods[key] = accesstypes.Period{End: past}
				}
				if tt.group != nil {
					key := access.NewMembershipKey(*tt.group, "", "")
					store.groups = map[impulse.UUID]*Group{crewID: {Kind: key.Kind, Axis: key.Axis, Domain: key.Domain, Name: "Crew"}}
					store.groupMembers = map[access.GroupMemberKey]bool{inGroup: true}
				}
				var opts []Option
				if declared {
					opts = append(opts, WithOneTenantPerPerson())
				}
				m, _ := newManager(t, store, tenantRoles, opts...)

				var got bool
				err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					var err error
					got, err = m.HoldsTenant(ctx, txn, userID)

					return err
				})
				if err != nil {
					t.Fatalf("HoldsTenant() error = %v", err)
				}
				if got != tt.want {
					t.Errorf("HoldsTenant() = %v, want %v", got, tt.want)
				}
				if len(store.writes) != 0 {
					t.Errorf("HoldsTenant() buffered %v, want a read alone", store.writes)
				}
			})
		}
	}

	t.Run("a failed read of what the person holds fails the call", func(t *testing.T) {
		t.Parallel()

		store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}, failOn: "Memberships"}
		m, _ := newManager(t, store, tenantRoles)

		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			held, err := m.HoldsTenant(ctx, txn, userID)
			if held {
				t.Errorf("HoldsTenant() = true beside its error")
			}

			return err
		})
		if !errors.Is(err, errStore) {
			t.Errorf("HoldsTenant() error = %v, want the store's failure", err)
		}
	})
}

// TestManager_Invite_oneTenantPerPerson pins the rule on an invitation when it is sent:
// an invitation into a tenant is refused where a user already holds the address and
// holds a role in another tenant, the refusal says the address has an account and never
// where, and nothing is written or mailed. An address no user holds is invited, and so
// is a person of the same tenant, a person who holds roles for the whole application
// alone, and anyone at all into the application. The account judged is the one that
// confirmed the address, by its id: a role another account holds is not held against it.
func TestManager_Invite_oneTenantPerPerson(t *testing.T) {
	t.Parallel()

	alpha := "alpha"
	tests := []struct {
		name    string
		holds   []access.MembershipKey
		domain  *string
		role    accesstypes.Role
		several bool
		refused bool
	}{
		{name: "an address no user holds is invited into a tenant", domain: &alpha, role: "Reader"},
		{
			name:  "an address whose user belongs to another tenant is refused",
			holds: []access.MembershipKey{access.NewMembershipKey(betaScope, dana, "Reader")}, domain: &alpha, role: "Reader", refused: true,
		},
		{
			name:  "a role another account holds in another tenant is not held against the address's user",
			holds: []access.MembershipKey{access.NewMembershipKey(betaScope, erin, "Reader")}, domain: &alpha, role: "Reader",
		},
		{
			name:  "a person of the tenant is invited to one more role in it",
			holds: []access.MembershipKey{access.NewMembershipKey(alphaScope, dana, "Reader")}, domain: &alpha, role: "Writer",
		},
		{
			name:  "a person who holds a role for the whole application alone is invited into a tenant",
			holds: []access.MembershipKey{access.NewMembershipKey(globalScope, dana, "Steward")}, domain: &alpha, role: "Reader",
		},
		{
			name:  "one of the application's own people who holds a role in another tenant too is invited into a tenant",
			holds: []access.MembershipKey{access.NewMembershipKey(globalScope, dana, "Steward"), access.NewMembershipKey(betaScope, dana, "Reader")}, domain: &alpha, role: "Reader",
		},
		{
			name:  "a person of a tenant is invited into the application",
			holds: []access.MembershipKey{access.NewMembershipKey(betaScope, dana, "Reader")}, role: "Steward",
		},
		{
			name:  "an application that did not declare the rule invites a person of another tenant",
			holds: []access.MembershipKey{access.NewMembershipKey(betaScope, dana, "Reader")}, domain: &alpha, role: "Reader", several: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Where a case names a membership, the account of dana has confirmed the
			// address; otherwise no user holds it.
			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}, emails: map[string]impulse.UUID{}}
			for _, key := range tt.holds {
				store.memberships[key] = true
				store.emails["dana@example.com"] = userID
			}
			mail := &fakeMail{id: invitationID}
			var opts []Option
			if !tt.several {
				opts = append(opts, WithOneTenantPerPerson())
			}
			m, err := New(managedAuth, store, &fakeEngine{roles: tenantRoles}, fakeSessions{mail: mail}, opts...)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				_, err := m.Invite(ctx, txn, &Invitation{Email: "dana@example.com", Domain: tt.domain, Roles: []accesstypes.Role{tt.role}}, nil)

				return err
			})
			if tt.refused {
				assertOtherTenant(t, err, "beta")
				if !strings.Contains(httpio.Messages(err)[0], "an account already holds the address dana@example.com") {
					t.Errorf("the refusal %q does not say the address has an account", httpio.Messages(err)[0])
				}
				if len(store.writes) != 0 || len(mail.sent) != 0 {
					t.Errorf("a refused invitation buffered %v and mailed %v, want nothing", store.writes, mail.sent)
				}

				return
			}
			if err != nil {
				t.Fatalf("Invite() error = %v", err)
			}
			if len(store.writes) != 1 || !slices.Equal(mail.sent, []string{"dana@example.com"}) {
				t.Errorf("an invitation buffered %v and mailed %v, want its row and its message", store.writes, mail.sent)
			}
		})
	}

	t.Run("a failed read of who holds the address fails the call", func(t *testing.T) {
		t.Parallel()

		store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}, failOn: "UserByVerifiedEmail"}
		m, err := New(managedAuth, store, &fakeEngine{roles: tenantRoles}, fakeSessions{mail: &fakeMail{id: invitationID}}, WithOneTenantPerPerson())
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		err = inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := m.Invite(ctx, txn, &Invitation{Email: "dana@example.com", Domain: &alpha, Roles: []accesstypes.Role{"Reader"}}, nil)

			return err
		})
		if !errors.Is(err, errStore) || len(store.writes) != 0 {
			t.Errorf("Invite() error = %v with %v buffered, want the store's failure and nothing written", err, store.writes)
		}
	})
}

// TestManager_signInHooks_oneTenantPerPerson pins the rule on the roles a sign-in gives.
// The two hooks of a sign-in run in its one transaction, and neither reads what the
// other buffered, so they share what they have given under the transaction they were
// handed: a person the directory's default role seats in one tenant is not given
// another tenant's invitation in the same sign-in. The person is the account whose id
// the session library hands the hooks, beside the username people read.
func TestManager_signInHooks_oneTenantPerPerson(t *testing.T) {
	t.Parallel()

	id := userID.String()
	seat := func(tenants ...accesstypes.Domain) []signin.DirectorySeat {
		seats := make([]signin.DirectorySeat, 0, len(tenants))
		for _, tenant := range tenants {
			seats = append(seats, signin.DirectorySeat{Tenant: tenant, Role: "Reader"})
		}

		return seats
	}
	invitation := func(tenant string, role string) *sessioninfo.Invitation {
		return &sessioninfo.Invitation{Email: "dana@example.com", TenantID: tenant, RoleNames: []string{role}}
	}
	build := func(t *testing.T, holds ...access.MembershipKey) (*Manager, *fakeStore) {
		t.Helper()
		store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
		for _, key := range holds {
			store.memberships[key] = true
		}
		m, _ := newManager(t, store, tenantRoles, WithOneTenantPerPerson())

		return m, store
	}

	t.Run("a stranger from one tenant's directory who carries another tenant's invitation is refused the invitation", func(t *testing.T) {
		t.Parallel()
		m, store := build(t)
		tx := new(spanner.ReadWriteTransaction)

		if _, err := m.DirectorySeated("Staff").Seat(t.Context(), tx, userID, "dana", seat("alpha")); err != nil {
			t.Fatalf("the directory's default role: error = %v", err)
		}
		_, err := m.InvitationAccepted("Staff")(t.Context(), tx, userID, "dana", invitation("beta", "Reader"))
		assertOtherTenant(t, err, "alpha")
		if diff := cmp.Diff([]string{"insert membership domain/alpha " + id + " Reader"}, store.writes); diff != "" {
			t.Errorf("buffered writes (-want +got):\n%s", diff)
		}
	})

	t.Run("the invitation of the tenant whose directory seated the person is accepted", func(t *testing.T) {
		t.Parallel()
		m, store := build(t)
		tx := new(spanner.ReadWriteTransaction)

		if _, err := m.DirectorySeated("Staff").Seat(t.Context(), tx, userID, "dana", seat("alpha")); err != nil {
			t.Fatalf("the directory's default role: error = %v", err)
		}
		if _, err := m.InvitationAccepted("Staff")(t.Context(), tx, userID, "dana", invitation("alpha", "Writer")); err != nil {
			t.Fatalf("the invitation: error = %v", err)
		}
		want := []string{"insert membership domain/alpha " + id + " Reader", "insert membership domain/alpha " + id + " Writer"}
		if diff := cmp.Diff(want, store.writes); diff != "" {
			t.Errorf("buffered writes (-want +got):\n%s", diff)
		}
	})

	t.Run("what one transaction gave is not held against another: each attempt of a sign-in has its own", func(t *testing.T) {
		t.Parallel()
		m, _ := build(t)

		if _, err := m.DirectorySeated("Staff").Seat(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", seat("alpha")); err != nil {
			t.Fatalf("the directory's default role: error = %v", err)
		}
		// That attempt aborted. The next one finds no default role, and its invitation
		// into another tenant is the only role the person is given.
		if _, err := m.InvitationAccepted("Staff")(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", invitation("beta", "Reader")); err != nil {
			t.Errorf("the invitation in a transaction of its own: error = %v, want it accepted", err)
		}
	})

	t.Run("a directory two tenants declare with a default role seats the person in neither", func(t *testing.T) {
		t.Parallel()
		m, _ := build(t)

		committed, err := m.DirectorySeated("Staff").Seat(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", seat("alpha", "beta"))
		assertOtherTenant(t, err)
		if committed != nil {
			t.Error("a hook that failed answered a committed function")
		}
	})

	t.Run("an invitation into a second tenant is refused at its acceptance for a person who came to belong to another since it was sent", func(t *testing.T) {
		t.Parallel()
		m, store := build(t, access.NewMembershipKey(alphaScope, dana, "Reader"))

		_, err := m.InvitationAccepted("Staff")(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", invitation("beta", "Reader"))
		assertOtherTenant(t, err, "alpha")
		if len(store.writes) != 0 {
			t.Errorf("a refused acceptance buffered %v", store.writes)
		}
	})

	t.Run("an invitation into the application is given to a person of a tenant", func(t *testing.T) {
		t.Parallel()
		m, store := build(t, access.NewMembershipKey(alphaScope, dana, "Reader"))

		if _, err := m.InvitationAccepted("Staff")(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", invitation("", "Steward")); err != nil {
			t.Fatalf("the invitation: error = %v", err)
		}
		if diff := cmp.Diff([]string{"insert membership global/ " + id + " Steward"}, store.writes); diff != "" {
			t.Errorf("buffered writes (-want +got):\n%s", diff)
		}
	})

	t.Run("an invitation into the application is given in the sign-in whose directory seated the person in a tenant", func(t *testing.T) {
		t.Parallel()
		m, store := build(t)
		tx := new(spanner.ReadWriteTransaction)

		if _, err := m.DirectorySeated("Staff").Seat(t.Context(), tx, userID, "dana", seat("alpha")); err != nil {
			t.Fatalf("the directory's default role: error = %v", err)
		}
		if _, err := m.InvitationAccepted("Staff")(t.Context(), tx, userID, "dana", invitation("", "Steward")); err != nil {
			t.Fatalf("the invitation: error = %v, want it accepted: a role for the whole application is no tenant's", err)
		}
		want := []string{"insert membership domain/alpha " + id + " Reader", "insert membership global/ " + id + " Steward"}
		if diff := cmp.Diff(want, store.writes); diff != "" {
			t.Errorf("buffered writes (-want +got):\n%s", diff)
		}
	})

	t.Run("what a transaction's hooks noted is forgotten once it has committed, and when the sign-in's context ends", func(t *testing.T) {
		t.Parallel()
		m, _ := build(t)
		noted := func() int {
			m.oneTenant.mu.Lock()
			defer m.oneTenant.mu.Unlock()

			return len(m.oneTenant.notes)
		}

		committed, err := m.DirectorySeated("Staff").Seat(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", seat("alpha"))
		if err != nil || noted() != 1 {
			t.Fatalf("the hook's error = %v with %d transaction(s) noted, want the one", err, noted())
		}
		committed(t.Context())
		if noted() != 0 {
			t.Errorf("%d transaction(s) noted after the commit, want none", noted())
		}

		// A transaction that never commits: its note goes with the sign-in's context.
		ctx, cancel := context.WithCancel(t.Context())
		if _, err := m.DirectorySeated("Staff").Seat(ctx, new(spanner.ReadWriteTransaction), userID, "dana", seat("alpha")); err != nil || noted() != 1 {
			t.Fatalf("the hook's error = %v with %d transaction(s) noted, want the one", err, noted())
		}
		cancel()
		for deadline := time.Now().Add(5 * time.Second); noted() != 0 && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		if noted() != 0 {
			t.Errorf("%d transaction(s) noted after the context ended, want none", noted())
		}
	})
}

// TestManager_RemoveRole_oneTenantPerPerson pins the rule on a role being taken away:
// the last role a person holds outside the tenants stays while they hold roles in more
// than one tenant, since it is what lets them; it goes once another role outside the
// tenants remains or they are down to one tenant, a role in a tenant always goes, and an
// application that did not declare the rule takes any role.
func TestManager_RemoveRole_oneTenantPerPerson(t *testing.T) {
	t.Parallel()

	id := userID.String()
	holds := func(scope accesstypes.PolicyScope, role accesstypes.Role) access.MembershipKey {
		return access.NewMembershipKey(scope, dana, role)
	}
	tests := []struct {
		name    string
		holds   []access.MembershipKey
		scope   accesstypes.PolicyScope
		role    accesstypes.Role
		several bool
		// wantWrite is the removal buffered, "" for a refusal.
		wantWrite string
	}{
		{
			name:  "the only role outside the tenants stays while the person holds roles in two tenants",
			holds: []access.MembershipKey{holds(globalScope, "Steward"), holds(alphaScope, "Reader"), holds(betaScope, "Reader")}, scope: globalScope, role: "Steward",
		},
		{
			name:  "and so does the only role held in every tenant",
			holds: []access.MembershipKey{holds(everyScope, "Reader"), holds(alphaScope, "Writer"), holds(betaScope, "Reader")}, scope: everyScope, role: "Reader",
		},
		{
			name:  "it goes while another role outside the tenants remains",
			holds: []access.MembershipKey{holds(globalScope, "Steward"), holds(everyScope, "Reader"), holds(alphaScope, "Writer"), holds(betaScope, "Reader")}, scope: globalScope, role: "Steward",
			wantWrite: "delete membership global/ " + id + " Steward",
		},
		{
			name:  "it goes from a person who holds roles in one tenant, however many",
			holds: []access.MembershipKey{holds(globalScope, "Steward"), holds(alphaScope, "Reader"), holds(alphaScope, "Writer")}, scope: globalScope, role: "Steward",
			wantWrite: "delete membership global/ " + id + " Steward",
		},
		{
			name:  "a role in a tenant always goes",
			holds: []access.MembershipKey{holds(globalScope, "Steward"), holds(alphaScope, "Reader"), holds(betaScope, "Reader")}, scope: alphaScope, role: "Reader",
			wantWrite: "delete membership domain/alpha " + id + " Reader",
		},
		{
			name:  "and it goes from a person in two tenants who holds nothing outside them",
			holds: []access.MembershipKey{holds(alphaScope, "Reader"), holds(alphaScope, "Writer"), holds(betaScope, "Reader")}, scope: alphaScope, role: "Writer",
			wantWrite: "delete membership domain/alpha " + id + " Writer",
		},
		{
			name:  "an application that did not declare the rule takes the role",
			holds: []access.MembershipKey{holds(globalScope, "Steward"), holds(alphaScope, "Reader"), holds(betaScope, "Reader")}, scope: globalScope, role: "Steward", several: true,
			wantWrite: "delete membership global/ " + id + " Steward",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
			for _, key := range tt.holds {
				store.memberships[key] = true
			}
			var opts []Option
			if !tt.several {
				opts = append(opts, WithOneTenantPerPerson())
			}
			m, engine := newManager(t, store, tenantRoles, opts...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveRole(ctx, txn, tt.scope, userID, tt.role)
			})
			if tt.wantWrite == "" {
				if !httpio.HasConflict(err) || !strings.Contains(httpio.Messages(err)[0], "this account holds roles in more than one tenant") {
					t.Fatalf("RemoveRole() error = %v, want a Conflict that says the person holds roles in more than one tenant", err)
				}
				for _, tenant := range []string{"alpha", "beta"} {
					if strings.Contains(httpio.Messages(err)[0], tenant) {
						t.Errorf("the refusal %q names the tenant %s", httpio.Messages(err)[0], tenant)
					}
				}
				if len(store.writes) != 0 || engine.changed != 0 {
					t.Errorf("a refused removal buffered %v and told the engine %d time(s), want nothing", store.writes, engine.changed)
				}

				return
			}
			if err != nil {
				t.Fatalf("RemoveRole() error = %v", err)
			}
			if diff := cmp.Diff([]string{tt.wantWrite}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// watchedContext counts the functions registered to run when it ends and how many of
// them were stopped first. It is a context of its own that never ends, with a Done
// channel of its own, so context.AfterFunc registers through it and not through a
// context under it.
type watchedContext struct {
	context.Context

	done                chan struct{}
	registered, stopped int
}

func (w *watchedContext) Done() <-chan struct{} {
	return w.done
}

func (w *watchedContext) AfterFunc(func()) (stop func() bool) {
	w.registered++

	return func() bool {
		w.stopped++

		return true
	}
}

// TestSignInTenants_oneWatchForATransaction: the hooks of one sign-in's transaction
// watch the sign-in's context once between them, and the watch ends when the note is
// forgotten at the commit, so a context that lives long (a job's, a server's) is left
// holding nothing of a sign-in that is over. A context that has ended opens no note.
func TestSignInTenants_oneWatchForATransaction(t *testing.T) {
	t.Parallel()

	s := &signInTenants{notes: map[any]*signInNote{}}
	ctx := &watchedContext{Context: context.Background(), done: make(chan struct{})}
	tx := new(spanner.ReadWriteTransaction)

	first := s.within(ctx, tx)
	second := s.within(ctx, tx)
	if ctx.registered != 1 || ctx.stopped != 0 {
		t.Fatalf("two hooks of one transaction: %d watch(es) on the context, %d stopped; want one, still standing", ctx.registered, ctx.stopped)
	}
	if !s.give(first, userID, "alpha") || s.give(second, userID, "beta") {
		t.Error("the second hook did not see the tenant the first gave the person a role in")
	}
	s.forget(tx)
	if ctx.registered != 1 || ctx.stopped != 1 || len(s.notes) != 0 {
		t.Errorf("after the commit: %d watch(es) on the context, %d stopped, %d note(s); want the one watch, stopped, and no note", ctx.registered, ctx.stopped, len(s.notes))
	}
	// Forgetting twice, as two hooks' committed functions do, stops nothing twice.
	s.forget(tx)
	if ctx.stopped != 1 {
		t.Errorf("a second forget stopped the watch again (%d)", ctx.stopped)
	}

	ended, cancel := context.WithCancel(t.Context())
	cancel()
	s.within(ended, new(spanner.ReadWriteTransaction))
	if len(s.notes) != 0 {
		t.Errorf("a context that has ended opened %d note(s), want none", len(s.notes))
	}
}

// TestManager_RenameUser_oneTenantPerPerson pins that the rule of one tenant per person
// has nothing to judge on a rename: a role is held by an account and never by a name, so
// a rename writes the username and no membership, and nothing another account holds
// refuses it, nor does a membership that names the new username and no account. A person
// in two tenants from before the rule is renamed like anybody else, and the rename is
// the same in an application that did not declare the rule.
func TestManager_RenameUser_oneTenantPerPerson(t *testing.T) {
	t.Parallel()

	holds := func(scope accesstypes.PolicyScope, role accesstypes.Role) access.MembershipKey {
		return access.NewMembershipKey(scope, dana, role)
	}
	// named is a membership that names the new username and no account.
	named := func(scope accesstypes.PolicyScope, role accesstypes.Role) access.MembershipKey {
		return access.NewMembershipKey(scope, accesstypes.User("dana.r"), role)
	}
	tests := []struct {
		name    string
		holds   []access.MembershipKey
		without bool
	}{
		{name: "the person holds a role in one tenant, and another account holds one in another", holds: []access.MembershipKey{holds(alphaScope, "Reader"), access.NewMembershipKey(betaScope, erin, "Reader")}},
		{name: "a membership in another tenant names the new username and no account", holds: []access.MembershipKey{holds(alphaScope, "Reader"), named(betaScope, "Reader")}},
		{name: "a membership for the whole application names the new username and no account", holds: []access.MembershipKey{holds(alphaScope, "Reader"), named(globalScope, "Steward")}},
		{name: "one of the application's own people is renamed", holds: []access.MembershipKey{holds(globalScope, "Steward"), holds(alphaScope, "Reader"), named(betaScope, "Reader")}},
		{name: "a person in two tenants from before the rule keeps both", holds: []access.MembershipKey{holds(alphaScope, "Reader"), holds(betaScope, "Reader")}},
		{name: "an application that did not declare the rule renames the same way", holds: []access.MembershipKey{holds(alphaScope, "Reader"), named(betaScope, "Reader")}, without: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{
				usernames:   map[impulse.UUID]string{userID: "dana", otherUser: "erin"},
				memberships: map[access.MembershipKey]bool{},
				roles:       map[access.RoleKey]bool{},
			}
			for _, key := range tt.holds {
				store.memberships[key] = true
			}
			var opts []Option
			if !tt.without {
				opts = append(opts, WithOneTenantPerPerson())
			}
			m, engine := newManager(t, store, tenantRoles, opts...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RenameUser(ctx, txn, userID, "dana.r")
			})
			if err != nil {
				t.Fatalf("RenameUser() error = %v, want the rename: a name holds nothing", err)
			}
			if diff := cmp.Diff([]string{"update user " + userID.String() + " username=dana.r"}, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got): the username alone, and no membership\n%s", diff)
			}
			if engine.changed != 0 {
				t.Errorf("the engine was told %d time(s), want none: a rename changes nothing the engine holds", engine.changed)
			}
		})
	}
}

// TestOutsideTenants: a membership is outside the tenants by the kind of its row, for
// the whole application or in every tenant, and never by a domain that is empty: a row
// held in one tenant whose name is empty is still one tenant's.
func TestOutsideTenants(t *testing.T) {
	t.Parallel()

	unnamed := access.NewMembershipKey(accesstypes.DomainPolicyScope(""), dana, "Reader")
	tests := []struct {
		name        string
		held        []access.MembershipKey
		wantOutside bool
		wantTenants int
	}{
		{name: "nothing held"},
		{name: "a role for the whole application", held: []access.MembershipKey{access.NewMembershipKey(globalScope, dana, "Steward")}, wantOutside: true},
		{name: "a role in every tenant", held: []access.MembershipKey{access.NewMembershipKey(everyScope, dana, "Reader")}, wantOutside: true},
		{name: "roles in one tenant", held: []access.MembershipKey{access.NewMembershipKey(alphaScope, dana, "Reader"), access.NewMembershipKey(alphaScope, dana, "Writer")}, wantTenants: 1},
		{name: "roles in two tenants", held: []access.MembershipKey{access.NewMembershipKey(alphaScope, dana, "Reader"), access.NewMembershipKey(betaScope, dana, "Reader")}, wantTenants: 2},
		{name: "a role in a tenant whose name is empty is one tenant's", held: []access.MembershipKey{unnamed}, wantTenants: 1},
		{name: "a tenant's role beside one for the whole application", held: []access.MembershipKey{access.NewMembershipKey(alphaScope, dana, "Reader"), access.NewMembershipKey(globalScope, dana, "Steward")}, wantOutside: true, wantTenants: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := outsideTenants(tt.held); got != tt.wantOutside {
				t.Errorf("outsideTenants() = %v, want %v", got, tt.wantOutside)
			}
			if got := tenantCount(tt.held); got != tt.wantTenants {
				t.Errorf("tenantCount() = %d, want %d", got, tt.wantTenants)
			}
		})
	}
}
