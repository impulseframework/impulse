package identity

import (
	"context"
	"strings"
	"testing"
	"time"

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

// roleKeyIn is the key of the roles table's row for role in scope: a custom role.
func roleKeyIn(scope accesstypes.PolicyScope, role accesstypes.Role) access.RoleKey {
	key := access.NewMembershipKey(scope, "", role)

	return access.RoleKey{Kind: key.Kind, Axis: key.Axis, Domain: key.Domain, Role: role}
}

// TestManager_giveInvitedRoles pins what an accepted invitation gives: its roles in the
// place it stores, which is its tenant, every tenant, or the application's global scope,
// and nowhere else, whatever roles exist by then, every one under the id of the
// person's account; a role that can no longer be given there fails the transaction, in
// an error that names the person by username, and a failed write is not taken for a
// role that does not exist.
func TestManager_giveInvitedRoles(t *testing.T) {
	t.Parallel()

	id := userID.String()

	tests := []struct {
		name string
		inv  *sessioninfo.Invitation
		held []access.MembershipKey
		// roles are the rows of the roles table: the custom roles that exist when the
		// invitation is accepted.
		roles map[access.RoleKey]bool
		// periods are the periods of the memberships the person holds.
		periods     map[access.MembershipKey]accesstypes.Period
		failOn      string
		wantErr     func(error) bool
		wantWrites  []string
		wantReads   []string
		wantChanged int
	}{
		{
			name:        "an invitation into a tenant gives its roles in that tenant",
			inv:         &sessioninfo.Invitation{Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Reader"}},
			wantWrites:  []string{"insert membership domain/alpha " + id + " Reader"},
			wantChanged: 1,
		},
		{
			name:        "an invitation into the application gives its roles in the global scope, and the engine is told once",
			inv:         &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward", "Auditor"}},
			roles:       map[access.RoleKey]bool{roleKeyIn(globalScope, "Auditor"): true},
			wantReads:   []string{"role global/ Auditor"},
			wantWrites:  []string{"insert membership global/ " + id + " Steward", "insert membership global/ " + id + " Auditor"},
			wantChanged: 1,
		},
		{
			name:        "an invitation for every tenant gives its roles in every tenant",
			inv:         &sessioninfo.Invitation{Email: "dana@example.com", EveryTenant: true, RoleNames: []string{"Reader"}},
			wantWrites:  []string{"insert membership every/ " + id + " Reader"},
			wantChanged: 1,
		},
		{
			// An invitation stored with a tenant's role and no place: the acceptance does
			// not go looking for the role in every tenant.
			name:      "an invitation that names no place gives nothing in every tenant: a role held only in the tenants is refused",
			inv:       &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Reader"}},
			wantErr:   httpio.HasNotFound,
			wantReads: []string{"role global/ Reader"},
		},
		{
			// The global scope gained a role named Reader after the invitation was sent.
			name:        "an invitation for every tenant gives the tenants' role where the global scope has since gained a role of its name",
			inv:         &sessioninfo.Invitation{Email: "dana@example.com", EveryTenant: true, RoleNames: []string{"Reader"}},
			roles:       map[access.RoleKey]bool{roleKeyIn(globalScope, "Reader"): true},
			wantWrites:  []string{"insert membership every/ " + id + " Reader"},
			wantChanged: 1,
		},
		{
			// The global custom role Auditor was deleted after the invitation was sent, and
			// every tenant holds a custom role of its name.
			name:      "an invitation into the application whose global role was deleted since is refused, though every tenant holds a role of its name",
			inv:       &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Auditor"}},
			roles:     map[access.RoleKey]bool{roleKeyIn(everyScope, "Auditor"): true},
			wantErr:   httpio.HasNotFound,
			wantReads: []string{"role global/ Auditor"},
		},
		{
			name:      "a role of an invitation for every tenant that is not held there is refused, though the global scope holds it",
			inv:       &sessioninfo.Invitation{Email: "dana@example.com", EveryTenant: true, RoleNames: []string{"Steward"}},
			wantErr:   httpio.HasNotFound,
			wantReads: []string{"role every/ Steward"},
		},
		{
			name:       "a role the person holds already is left as it is, and the others are given",
			inv:        &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward", "Auditor"}},
			held:       []access.MembershipKey{access.NewMembershipKey(globalScope, dana, "Steward")},
			roles:      map[access.RoleKey]bool{roleKeyIn(globalScope, "Auditor"): true},
			wantReads:  []string{"role global/ Auditor"},
			wantWrites: []string{"insert membership global/ " + id + " Auditor"},
			// One membership was written, so the engine is told.
			wantChanged: 1,
		},
		{
			name: "a role the person holds for a period keeps its period: the invitation gives what is not held, and erases nothing",
			inv:  &sessioninfo.Invitation{Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Reader"}},
			held: []access.MembershipKey{access.NewMembershipKey(alphaScope, dana, "Reader")},
			periods: map[access.MembershipKey]accesstypes.Period{
				access.NewMembershipKey(alphaScope, dana, "Reader"): {End: time.Now().Add(time.Hour)},
			},
		},
		{
			name: "a role the person holds for a period that has not begun keeps it too",
			inv:  &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward"}},
			held: []access.MembershipKey{access.NewMembershipKey(globalScope, dana, "Steward")},
			periods: map[access.MembershipKey]accesstypes.Period{
				access.NewMembershipKey(globalScope, dana, "Steward"): {Start: time.Now().Add(time.Hour)},
			},
		},
		{
			name: "a role whose period has ended is nothing the person holds: the invitation gives it afresh, with no period",
			inv:  &sessioninfo.Invitation{Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Reader"}},
			held: []access.MembershipKey{access.NewMembershipKey(alphaScope, dana, "Reader")},
			periods: map[access.MembershipKey]accesstypes.Period{
				access.NewMembershipKey(alphaScope, dana, "Reader"): {End: time.Now().Add(-time.Hour)},
			},
			// The row stands, so the write of the membership finds it and the period is what changes.
			wantWrites:  []string{"period of membership domain/alpha " + id + " Reader: none"},
			wantChanged: 1,
		},
		{
			name:    "a role that exists nowhere any more fails the transaction, so the role given before it is not committed",
			inv:     &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward", "Retired"}},
			wantErr: httpio.HasNotFound,
			// Retired is read from the roles table in the invitation's place alone.
			wantReads:  []string{"role global/ Retired"},
			wantWrites: []string{"insert membership global/ " + id + " Steward"},
		},
		{
			name:    "a role of a tenant's invitation that does not exist in the tenant fails the transaction",
			inv:     &sessioninfo.Invitation{Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Steward"}},
			wantErr: httpio.HasNotFound,
			// The global role is not looked for anywhere but in the tenant.
			wantReads: []string{"role domain/alpha Steward"},
		},
		{
			name:    "a failed write fails the transaction and is not taken for a role that does not exist",
			inv:     &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward"}},
			failOn:  "InsertMembership",
			wantErr: func(err error) bool { return errors.Is(err, errStore) && !httpio.HasNotFound(err) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}, failOn: tt.failOn}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			for key := range tt.roles {
				store.roles[key] = true
			}
			store.membershipPeriods = tt.periods
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.giveInvitedRoles(ctx, txn, userID, "dana", tt.inv)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("giveInvitedRoles() error = %v, want the refusal", err)
				}
				if !strings.Contains(err.Error(), `gives "dana" the role`) {
					t.Errorf("giveInvitedRoles() error = %v, want one that names the person by username", err)
				}
			} else if err != nil {
				t.Fatalf("giveInvitedRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantReads, store.reads); diff != "" {
				t.Errorf("reads of the roles table (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told the policy changed %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// TestManager_giveInvitedRoles_firstAdministrator pins where the first administrator's
// invitation gives its roles. The deployment sends it for the whole application, and it
// is the one invitation whose roles are held in two places: each role is given where the
// application's first-administrator setup declares it (WithFirstAdministratorRoles), the
// first list in the whole application and the second in every tenant. No other
// invitation reads the lists: one a user sent, and one that does not give the Super
// Administrator role, give a tenants' role with no tenant named nowhere, and the
// acceptance fails, as it does for the first administrator's where the application
// declared nothing.
func TestManager_giveInvitedRoles_firstAdministrator(t *testing.T) {
	t.Parallel()

	const super = string(accesstypes.SuperAdministrator)
	id := userID.String()
	// The role file: Keeper is a role of the whole application and of every tenant, by
	// the same name.
	roles := map[accesstypes.PolicyScope][]accesstypes.Role{
		globalScope: {accesstypes.SuperAdministrator, "Steward", "Keeper"},
		everyScope:  {"Reader", "Keeper"},
	}
	declared := WithFirstAdministratorRoles([]accesstypes.Role{"Steward"}, []accesstypes.Role{"Reader"})
	sender := impulse.NullUUIDFromUUID(otherUser)

	tests := []struct {
		name       string
		opts       []Option
		inv        *sessioninfo.Invitation
		wantErr    func(error) bool
		wantWhere  string
		wantWrites []string
		wantReads  []string
	}{
		{
			name: "the first administrator's invitation gives the first list in the whole application and the second in every tenant",
			opts: []Option{declared},
			inv:  &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{super, "Steward", "Reader"}},
			wantWrites: []string{
				"insert membership global/ " + id + " " + super,
				"insert membership global/ " + id + " Steward",
				"insert membership every/ " + id + " Reader",
			},
		},
		{
			name: "a role both lists name is given in both places",
			opts: []Option{WithFirstAdministratorRoles([]accesstypes.Role{"Keeper"}, []accesstypes.Role{"Keeper"})},
			inv:  &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{super, "Keeper"}},
			wantWrites: []string{
				"insert membership global/ " + id + " " + super,
				"insert membership global/ " + id + " Keeper",
				"insert membership every/ " + id + " Keeper",
			},
		},
		{
			// Keeper exists in every tenant too, and nothing is looked up by a role's kind.
			name: "a role neither list names is given in the whole application, where the invitation stores it",
			opts: []Option{declared},
			inv:  &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{super, "Keeper"}},
			wantWrites: []string{
				"insert membership global/ " + id + " " + super,
				"insert membership global/ " + id + " Keeper",
			},
		},
		{
			name:       "a role declared for every tenant that no tenant holds any more fails the acceptance, in words that say where",
			opts:       []Option{WithFirstAdministratorRoles(nil, []accesstypes.Role{"Steward"})},
			inv:        &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{super, "Steward"}},
			wantErr:    httpio.HasNotFound,
			wantWhere:  "the role Steward in every tenant",
			wantReads:  []string{"role every/ Steward"},
			wantWrites: []string{"insert membership global/ " + id + " " + super},
		},
		{
			name:       "where the application declared nothing, the first administrator's invitation gives every role in the whole application: a tenants' role is refused",
			inv:        &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{super, "Reader"}},
			wantErr:    httpio.HasNotFound,
			wantWhere:  "the role Reader in the whole application",
			wantReads:  []string{"role global/ Reader"},
			wantWrites: []string{"insert membership global/ " + id + " " + super},
		},
		{
			name:       "an invitation a user sent is not the first administrator's: a tenants' role with no tenant named is still refused",
			opts:       []Option{declared},
			inv:        &sessioninfo.Invitation{Email: "dana@example.com", InvitedByUserID: sender, RoleNames: []string{super, "Reader"}},
			wantErr:    httpio.HasNotFound,
			wantWhere:  "the role Reader in the whole application",
			wantReads:  []string{"role global/ Reader"},
			wantWrites: []string{"insert membership global/ " + id + " " + super},
		},
		{
			name:       "an invitation that does not give the Super Administrator role is not the first administrator's: a tenants' role with no tenant named is still refused",
			opts:       []Option{declared},
			inv:        &sessioninfo.Invitation{Email: "dana@example.com", RoleNames: []string{"Steward", "Reader"}},
			wantErr:    httpio.HasNotFound,
			wantWhere:  "the role Reader in the whole application",
			wantReads:  []string{"role global/ Reader"},
			wantWrites: []string{"insert membership global/ " + id + " Steward"},
		},
		{
			name:       "an invitation into a tenant gives its roles in the tenant alone, whatever the lists name",
			opts:       []Option{WithFirstAdministratorRoles(nil, []accesstypes.Role{"Keeper"})},
			inv:        &sessioninfo.Invitation{Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Keeper"}},
			wantErr:    httpio.HasNotFound,
			wantWhere:  "the role Keeper in alpha",
			wantReads:  []string{"role domain/alpha Keeper"},
			wantWrites: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
			m, _ := newManager(t, store, roles, tt.opts...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.giveInvitedRoles(ctx, txn, userID, "dana", tt.inv)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("giveInvitedRoles() error = %v, want the refusal", err)
				}
				if !strings.Contains(err.Error(), tt.wantWhere) {
					t.Errorf("giveInvitedRoles() error = %v, want one that says %q", err, tt.wantWhere)
				}
			} else if err != nil {
				t.Fatalf("giveInvitedRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantReads, store.reads); diff != "" {
				t.Errorf("reads of the roles table (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_giveDirectoryRoles pins what a directory's default role gives a person at
// their first sign-in: each role in its tenant, under the id of the person's account,
// and a role that cannot be given fails the transaction that creates the person, in an
// error that names them by username.
func TestManager_giveDirectoryRoles(t *testing.T) {
	t.Parallel()

	id := userID.String()

	tests := []struct {
		name  string
		seats []signin.DirectorySeat
		// held are the memberships the person holds before the sign-in, as committed, and
		// periods the periods of the ones that carry one.
		held        []access.MembershipKey
		periods     map[access.MembershipKey]accesstypes.Period
		custom      []access.RoleKey
		wantErr     func(error) bool
		wantWrites  []string
		wantGiven   []signin.DirectorySeat
		wantChanged int
	}{
		{
			name:        "a person who holds a role in another tenant is given the default role in the directory's tenant",
			seats:       []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			held:        []access.MembershipKey{access.NewMembershipKey(accesstypes.DomainPolicyScope("beta"), User(userID), "Reader")},
			wantWrites:  []string{"insert membership domain/alpha " + id + " Reader"},
			wantGiven:   []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			wantChanged: 1,
		},
		{
			name:  "a person who holds a role in the tenant keeps what they hold and is given nothing",
			seats: []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			held:  []access.MembershipKey{access.NewMembershipKey(accesstypes.DomainPolicyScope("alpha"), User(userID), "Editor")},
		},
		{
			name:  "a person who holds the default role itself in the tenant is given nothing",
			seats: []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			held:  []access.MembershipKey{access.NewMembershipKey(accesstypes.DomainPolicyScope("alpha"), User(userID), "Reader")},
		},
		{
			name:  "a person whose only role in the tenant has ended holds none there: the default role is given, though the ended row still stands",
			seats: []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			held:  []access.MembershipKey{access.NewMembershipKey(alphaScope, User(userID), "Editor")},
			periods: map[access.MembershipKey]accesstypes.Period{
				access.NewMembershipKey(alphaScope, User(userID), "Editor"): {End: time.Now().Add(-time.Hour)},
			},
			wantWrites:  []string{"insert membership domain/alpha " + id + " Reader"},
			wantGiven:   []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			wantChanged: 1,
		},
		{
			name:  "a person whose default role itself has ended is given it again, with no period",
			seats: []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			held:  []access.MembershipKey{access.NewMembershipKey(alphaScope, User(userID), "Reader")},
			periods: map[access.MembershipKey]accesstypes.Period{
				access.NewMembershipKey(alphaScope, User(userID), "Reader"): {End: time.Now().Add(-time.Hour)},
			},
			wantWrites:  []string{"period of membership domain/alpha " + id + " Reader: none"},
			wantGiven:   []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			wantChanged: 1,
		},
		{
			name:  "a person whose role in the tenant has not begun will hold it, and is given nothing beside it",
			seats: []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			held:  []access.MembershipKey{access.NewMembershipKey(alphaScope, User(userID), "Editor")},
			periods: map[access.MembershipKey]accesstypes.Period{
				access.NewMembershipKey(alphaScope, User(userID), "Editor"): {Start: time.Now().Add(time.Hour)},
			},
		},
		{
			name:        "a role held for the whole application is no role in the tenant: the default role is given",
			seats:       []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			held:        []access.MembershipKey{access.NewMembershipKey(accesstypes.GlobalPolicyScope(), User(userID), "Operator")},
			wantWrites:  []string{"insert membership domain/alpha " + id + " Reader"},
			wantGiven:   []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}},
			wantChanged: 1,
		},
		{
			name:  "each default role is given in its tenant, and the engine is told once",
			seats: []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}, {Tenant: "beta", Role: "Auditor"}},
			// beta's role is a custom one, read from the roles table.
			custom:      []access.RoleKey{{Kind: "domain", Domain: "beta", Role: "Auditor"}},
			wantWrites:  []string{"insert membership domain/alpha " + id + " Reader", "insert membership domain/beta " + id + " Auditor"},
			wantGiven:   []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}, {Tenant: "beta", Role: "Auditor"}},
			wantChanged: 1,
		},
		{
			name:       "a default role that does not exist in its tenant fails the transaction",
			seats:      []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}, {Tenant: "beta", Role: "Auditor"}},
			wantErr:    httpio.HasNotFound,
			wantWrites: []string{"insert membership domain/alpha " + id + " Reader"},
		},
		{
			name: "no default role gives nothing and tells the engine nothing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
			for _, key := range tt.custom {
				store.roles[key] = true
			}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			store.membershipPeriods = tt.periods
			m, engine := newManager(t, store, engineRoles)

			var given []signin.DirectorySeat
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				given, err = m.giveDirectoryRoles(ctx, txn, userID, "dana", tt.seats)

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("giveDirectoryRoles() error = %v, want the refusal", err)
				}
				if !strings.Contains(err.Error(), `gives "dana" the role`) {
					t.Errorf("giveDirectoryRoles() error = %v, want one that names the person by username", err)
				}
			} else if err != nil {
				t.Fatalf("giveDirectoryRoles() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if tt.wantErr == nil {
				if diff := cmp.Diff(tt.wantGiven, given); diff != "" {
					t.Errorf("seats given (-want +got):\n%s", diff)
				}
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told the policy changed %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// TestManager_DirectorySeated_onlyIn pins what the session library asks before it links
// an identity, on its sign-in name alone, to an account that has never signed in: the
// tenant is the only place the person holds anything. The link hands the tenant's
// directory everything the account holds, so a role in another tenant, in every tenant
// or outside the tenants beside the role in the tenant, or a place in a group kept
// anywhere else, makes the account more than one tenant's, and a role the tenant's
// administrator gives such a person changes nothing. What has ended counts for nothing,
// and what has not begun counts where it will.
func TestManager_DirectorySeated_onlyIn(t *testing.T) {
	t.Parallel()

	user := accesstypes.User(userID.String())
	inAlpha := access.NewMembershipKey(accesstypes.DomainPolicyScope("alpha"), user, "Member")
	inBeta := access.NewMembershipKey(accesstypes.DomainPolicyScope("beta"), user, "Member")
	everywhere := access.NewMembershipKey(accesstypes.EveryDomainPolicyScope(), user, "Support")
	global := access.NewMembershipKey(accesstypes.GlobalPolicyScope(), user, "Administrator")
	now := time.Now()
	ended := accesstypes.Period{End: now.Add(-time.Hour)}
	upcoming := accesstypes.Period{Start: now.Add(time.Hour)}
	tests := []struct {
		name string
		held []access.MembershipKey
		// groups are the groups the person has a place in (groupStore: nightID is kept
		// in alpha, dayID in beta and staffID for the whole application).
		groups  []impulse.UUID
		periods map[access.MembershipKey]accesstypes.Period
		places  map[impulse.UUID]accesstypes.Period
		want    bool
	}{
		{name: "a role in the tenant itself and nothing else", held: []access.MembershipKey{inAlpha}, want: true},
		{name: "a role in the tenant that has not begun", held: []access.MembershipKey{inAlpha}, periods: map[access.MembershipKey]accesstypes.Period{inAlpha: upcoming}, want: true},
		{name: "a place in a group of the tenant and nothing else", groups: []impulse.UUID{nightID}, want: true},
		{name: "a role in the tenant beside a place in its group", held: []access.MembershipKey{inAlpha}, groups: []impulse.UUID{nightID}, want: true},
		{name: "a role in another tenant alone", held: []access.MembershipKey{inBeta}},
		{name: "a role in every tenant alone", held: []access.MembershipKey{everywhere}},
		{name: "a role outside the tenants alone", held: []access.MembershipKey{global}},
		{name: "no role"},
		{name: "a role in the tenant beside one in another tenant", held: []access.MembershipKey{inAlpha, inBeta}},
		{name: "a role in the tenant beside one in every tenant", held: []access.MembershipKey{inAlpha, everywhere}},
		{name: "a role in the tenant beside one outside the tenants", held: []access.MembershipKey{inAlpha, global}},
		{name: "a role in the tenant beside a place in another tenant's group", held: []access.MembershipKey{inAlpha}, groups: []impulse.UUID{dayID}},
		{name: "a role in the tenant beside a place in a group kept for the whole application", held: []access.MembershipKey{inAlpha}, groups: []impulse.UUID{staffID}},
		{
			name: "a role in the tenant beside one in every tenant that has not begun", held: []access.MembershipKey{inAlpha, everywhere},
			periods: map[access.MembershipKey]accesstypes.Period{everywhere: upcoming},
		},
		{
			name: "a role in the tenant beside one elsewhere that has ended", held: []access.MembershipKey{inAlpha, inBeta},
			periods: map[access.MembershipKey]accesstypes.Period{inBeta: ended}, want: true,
		},
		{
			name: "a role in the tenant beside a place elsewhere that has ended", held: []access.MembershipKey{inAlpha}, groups: []impulse.UUID{staffID},
			places: map[impulse.UUID]accesstypes.Period{staffID: ended}, want: true,
		},
		{name: "a role in the tenant that has ended", held: []access.MembershipKey{inAlpha}, periods: map[access.MembershipKey]accesstypes.Period{inAlpha: ended}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := groupStore()
			store.membershipPeriods = tt.periods
			store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			for _, group := range tt.groups {
				store.groupMembers[memberKey(group, userID)] = true
				if period, ok := tt.places[group]; ok {
					store.placePeriods[memberKey(group, userID)] = period
				}
			}
			m, _ := newManager(t, store, engineRoles)

			// The store here is in memory and reads nothing off the transaction, so the
			// hook is handed none.
			got, err := m.DirectorySeated("Staff").OnlyIn(t.Context(), nil, userID, "alpha")
			if err != nil {
				t.Fatalf("OnlyIn() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("OnlyIn(alpha) = %t, want %t", got, tt.want)
			}
		})
	}
}

// TestManager_signInHooks pins the two hooks as the session library calls them: the
// roles are given under the id of the account the library hands the hook, never under
// the username beside it; nothing is told to the engine until the committed function
// runs, a run whose transaction did not commit (Spanner ran the closure again) tells it
// nothing, and a hook that fails answers no committed function.
func TestManager_signInHooks(t *testing.T) {
	t.Parallel()

	inv := &sessioninfo.Invitation{Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Reader"}}
	seats := []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader"}}

	hooks := map[string]func(m *Manager) func(ctx context.Context) (func(ctx context.Context), error){
		"an accepted invitation": func(m *Manager) func(ctx context.Context) (func(ctx context.Context), error) {
			hook := m.InvitationAccepted("Staff")

			return func(ctx context.Context) (func(ctx context.Context), error) {
				// The store here is in memory and reads nothing off the transaction, so
				// the hook is handed none.
				return hook(ctx, nil, userID, "dana", inv)
			}
		},
		"a directory's default role": func(m *Manager) func(ctx context.Context) (func(ctx context.Context), error) {
			hook := m.DirectorySeated("Staff")

			return func(ctx context.Context) (func(ctx context.Context), error) {
				return hook.Seat(ctx, nil, userID, "dana", seats)
			}
		},
	}
	for name, build := range hooks {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
			m, engine := newManager(t, store, engineRoles)
			hook := build(m)

			// The first run's transaction aborts: its committed function is never called.
			if _, err := hook(t.Context()); err != nil {
				t.Fatalf("the hook's first run error = %v", err)
			}
			committed, err := hook(t.Context())
			if err != nil {
				t.Fatalf("the hook's second run error = %v", err)
			}
			if engine.changed != 0 {
				t.Fatalf("the engine was told %d time(s) before any commit", engine.changed)
			}
			committed(t.Context())
			if engine.changed != 1 {
				t.Errorf("the engine was told the policy changed %d time(s) after the commit, want 1", engine.changed)
			}
			given := "insert membership domain/alpha " + userID.String() + " Reader"
			want := []string{given, given}
			if diff := cmp.Diff(want, store.writes); diff != "" {
				t.Errorf("buffered writes of the two runs (-want +got):\n%s", diff)
			}
		})

		t.Run(name+" that cannot be given", func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
			// An engine whose role file holds no role: the role the hook gives does not exist.
			m, engine := newManager(t, store, map[accesstypes.PolicyScope][]accesstypes.Role{})

			committed, err := build(m)(t.Context())
			if !httpio.HasNotFound(err) {
				t.Fatalf("the hook's error = %v, want NotFound", err)
			}
			if committed != nil {
				t.Error("a hook that failed answered a committed function")
			}
			if engine.changed != 0 || len(store.writes) != 0 {
				t.Errorf("a hook that failed told the engine %d time(s) and buffered %v", engine.changed, store.writes)
			}
		})
	}
}
