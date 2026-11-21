package identity

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// TestManager_ClearTenant pins what goes with a tenant: every membership held in it,
// its custom roles, the invitations into it that still stand, and the account of a
// person who held roles nowhere else. A role held in another tenant, in every tenant or
// in the global scope stays with its holder, and the permission engine is told once the
// transaction has committed, only when a membership or a role went. A membership names
// its person by the id of their account: one whose id no user holds, or whose user is
// no id at all, goes with the tenant and has no account to delete.
func TestManager_ClearTenant(t *testing.T) {
	t.Parallel()

	var (
		betaScope = accesstypes.DomainPolicyScope("beta")

		// mira, dana and gale are users of the auth; nora's id is one no user holds.
		miraID     = mustUUID("0190a000-0000-7000-8000-000000000001")
		danaID     = mustUUID("0190a000-0000-7000-8000-000000000002")
		galeID     = mustUUID("0190a000-0000-7000-8000-000000000003")
		noraID     = mustUUID("0190a000-0000-7000-8000-000000000004")
		miraLink   = mustUUID("0190a000-0000-7000-8000-000000000011")
		miraLogin  = mustUUID("0190a000-0000-7000-8000-000000000012")
		invitedOne = mustUUID("0190a000-0000-7000-8000-000000000021")
		invitedTwo = mustUUID("0190a000-0000-7000-8000-000000000022")

		danaAlpha   = access.NewMembershipKey(alphaScope, User(danaID), "Reader")
		danaAuditor = access.NewMembershipKey(alphaScope, User(danaID), "Auditor")
		danaBeta    = access.NewMembershipKey(betaScope, User(danaID), "Reader")
		miraAlpha   = access.NewMembershipKey(alphaScope, User(miraID), "Reader")
		galeEvery   = access.NewMembershipKey(everyScope, User(galeID), "Reader")
		galeAlpha   = access.NewMembershipKey(alphaScope, User(galeID), "Reader")
		noraAlpha   = access.NewMembershipKey(alphaScope, User(noraID), "Reader")
		// namedAlpha is a membership whose user is no account's id: a name.
		namedAlpha = access.NewMembershipKey(alphaScope, "mira", "Reader")
	)

	tests := []struct {
		name        string
		domain      accesstypes.Domain
		held        []access.MembershipKey
		custom      []access.RoleKey
		invitations map[accesstypes.Domain][]impulse.UUID
		failOn      string
		rollBack    bool
		wantErr     func(error) bool
		wantWrites  []string
		wantChanged int
	}{
		{
			name:   "a tenant the auth holds nothing for is no error, and the engine is not told",
			domain: "alpha",
			held:   []access.MembershipKey{danaBeta},
		},
		{
			name:   "every membership held in the tenant goes, and a person with a role elsewhere stays",
			domain: "alpha",
			held:   []access.MembershipKey{danaAlpha, danaAuditor, danaBeta},
			wantWrites: []string{
				"delete membership " + membershipText(&danaAuditor),
				"delete membership " + membershipText(&danaAlpha),
			},
			wantChanged: 1,
		},
		{
			name:   "a role held in every tenant is no one tenant's and stays, with its holder",
			domain: "alpha",
			held:   []access.MembershipKey{galeEvery, galeAlpha},
			wantWrites: []string{
				"delete membership " + membershipText(&galeAlpha),
			},
			wantChanged: 1,
		},
		{
			name:   "a person who held roles in the tenant alone is deleted with their links and sessions",
			domain: "alpha",
			held:   []access.MembershipKey{miraAlpha, danaAlpha, danaBeta},
			wantWrites: []string{
				"delete membership " + membershipText(&miraAlpha),
				"delete membership " + membershipText(&danaAlpha),
				"end session " + miraLogin.String(),
				"delete identity " + miraLink.String(),
				"delete user " + miraID.String(),
			},
			wantChanged: 1,
		},
		{
			name:   "a membership under an id no user holds goes, and there is no account to delete",
			domain: "alpha",
			held:   []access.MembershipKey{noraAlpha},
			wantWrites: []string{
				"delete membership " + membershipText(&noraAlpha),
			},
			wantChanged: 1,
		},
		{
			// The name is one a user holds, and the account stays: a name is not who
			// holds a membership.
			name:   "a membership whose user is a name and no id goes, and no account is read for it",
			domain: "alpha",
			held:   []access.MembershipKey{namedAlpha},
			wantWrites: []string{
				"delete membership " + membershipText(&namedAlpha),
			},
			wantChanged: 1,
		},
		{
			name:   "the custom roles made in the tenant go, and another tenant's stay",
			domain: "alpha",
			custom: []access.RoleKey{
				{Kind: "domain", Domain: "alpha", Role: "Auditor"},
				{Kind: "domain", Domain: "beta", Role: "Auditor"},
			},
			wantWrites:  []string{"delete the custom roles of alpha"},
			wantChanged: 1,
		},
		{
			name:        "the invitations into the tenant that still stand are withdrawn, and the engine is not told",
			domain:      "alpha",
			invitations: map[accesstypes.Domain][]impulse.UUID{"alpha": {invitedOne, invitedTwo}, "beta": {miraLink}},
			wantWrites: []string{
				"consume mail link " + invitedOne.String(),
				"consume mail link " + invitedTwo.String(),
			},
		},
		{
			name:    "no tenant named is an error",
			domain:  "",
			wantErr: func(err error) bool { return err != nil },
		},
		{
			name:    "a failed read of the tenant's memberships fails the call before anything is buffered",
			domain:  "alpha",
			held:    []access.MembershipKey{danaAlpha, danaBeta},
			failOn:  "TenantMemberships",
			wantErr: func(err error) bool { return errors.Is(err, errStore) },
		},
		{
			name:    "a failed read of the invitations fails the call before anything is buffered",
			domain:  "alpha",
			held:    []access.MembershipKey{danaAlpha, danaBeta},
			failOn:  "TenantInvitations",
			wantErr: func(err error) bool { return errors.Is(err, errStore) },
		},
		{
			name:     "a transaction that does not commit tells the engine nothing",
			domain:   "alpha",
			held:     []access.MembershipKey{danaAlpha, danaBeta},
			rollBack: true,
			wantErr:  func(err error) bool { return errors.Is(err, resource.ErrDryRun) },
			wantWrites: []string{
				"delete membership " + membershipText(&danaAlpha),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{
				memberships: map[access.MembershipKey]bool{},
				roles:       map[access.RoleKey]bool{},
				usernames:   map[impulse.UUID]string{miraID: "mira", danaID: "dana", galeID: "gale"},
				sessions:    map[impulse.UUID][]impulse.UUID{miraID: {miraLogin}},
				identities:  map[impulse.UUID]impulse.UUID{miraLink: miraID},
				invitations: tt.invitations,
				failOn:      tt.failOn,
			}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			for _, key := range tt.custom {
				store.roles[key] = true
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				if err := m.ClearTenant(ctx, txn, tt.domain); err != nil {
					return err
				}
				if engine.changed != 0 {
					t.Errorf("the engine was told %d time(s) before the commit", engine.changed)
				}
				if tt.rollBack {
					return resource.ErrDryRun
				}

				return nil
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("ClearTenant() error = %v, want the failure", err)
				}
			} else if err != nil {
				t.Fatalf("ClearTenant() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told the policy changed %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// TestManager_ClearTenant_theImpersonatorsOwnAccount holds the delete of a tenant to
// the rule that nobody is the subject of what they do in a session that acts as someone
// else. Mira holds her roles in the tenant alpha alone, so the tenant's going deletes
// her account. While she acts as someone who may delete alpha, the clear is refused
// before anything is buffered, in the rule's sentence. The same clear goes through, and
// deletes her account, in a session of anyone's own and in a session that acts as
// someone else and was started by a person the tenant's going leaves standing.
func TestManager_ClearTenant_theImpersonatorsOwnAccount(t *testing.T) {
	t.Parallel()

	var (
		miraID    = mustUUID("0190a000-0000-7000-8000-000000000001")
		danaID    = mustUUID("0190a000-0000-7000-8000-000000000002")
		miraLogin = mustUUID("0190a000-0000-7000-8000-000000000012")

		miraAlpha = access.NewMembershipKey(alphaScope, User(miraID), "Reader")
		danaAlpha = access.NewMembershipKey(alphaScope, User(danaID), "Reader")
		danaBeta  = access.NewMembershipKey(accesstypes.DomainPolicyScope("beta"), User(danaID), "Reader")
	)
	const refusal = "You are acting as someone else, so you cannot delete the only tenant your own account holds a role in."
	cleared := []string{
		"delete membership " + membershipText(&miraAlpha),
		"delete membership " + membershipText(&danaAlpha),
		"end session " + miraLogin.String(),
		"delete user " + miraID.String(),
	}

	tests := []struct {
		name string
		// session makes the request's session; nil leaves the call with none.
		session     func(ctx context.Context) context.Context
		wantRefused bool
		wantWrites  []string
		wantChanged int
	}{
		{
			name:        "the person who started the session holds roles in the tenant alone: refused, and nothing is buffered",
			session:     func(ctx context.Context) context.Context { return viewedBy(ctx, miraID) },
			wantRefused: true,
		},
		{
			name:        "the person who started the session holds a role elsewhere: the tenant is cleared",
			session:     func(ctx context.Context) context.Context { return viewedBy(ctx, danaID) },
			wantWrites:  cleared,
			wantChanged: 1,
		},
		{
			name:        "a call with no session, a job of the application's own: the tenant is cleared",
			wantWrites:  cleared,
			wantChanged: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{
				memberships: map[access.MembershipKey]bool{miraAlpha: true, danaAlpha: true, danaBeta: true},
				roles:       map[access.RoleKey]bool{},
				usernames:   map[impulse.UUID]string{miraID: "mira", danaID: "dana"},
				sessions:    map[impulse.UUID][]impulse.UUID{miraID: {miraLogin}},
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				if tt.session != nil {
					ctx = tt.session(ctx)
				}

				return m.ClearTenant(ctx, txn, "alpha")
			})
			switch {
			case tt.wantRefused && (!httpio.HasForbidden(err) || httpio.Message(err) != refusal):
				t.Fatalf("ClearTenant() error = %v, want the refusal %q", err, refusal)
			case !tt.wantRefused && err != nil:
				t.Fatalf("ClearTenant() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told the policy changed %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}
