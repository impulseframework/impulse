package identity

import (
	"context"
	"encoding/json"
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

// madeTenant is the tenant the fake maker makes.
const madeTenant = accesstypes.Domain("made")

// fakeMaker is the application's maker of a tenant as a test scripts it: it records what
// it was asked to make, and answers the tenant or the failure the test names.
type fakeMaker struct {
	// seats and directoryOnly say what the application's records keep: a default role
	// on a tenant's directory, and the directory-only switch.
	seats         bool
	directoryOnly bool
	err           error
	made          []string
	// committed counts the transactions that made a tenant and committed.
	committed int
}

func (f *fakeMaker) Make(_ context.Context, txn resource.ReadWriteTransaction, tenant *NewTenant) (accesstypes.Domain, error) {
	armed := "trusted"
	if tenant.Caller != nil {
		armed = "armed as " + string(tenant.Caller.Permissions.User())
	}
	f.made = append(f.made, strings.Join([]string{
		"fields=" + string(tenant.Fields), "directory-only=" + boolText(tenant.DirectoryOnly),
		"directory=" + string(tenant.Directory.Method) + "/" + tenant.Directory.Connection, "role=" + string(tenant.DefaultRole), armed,
	}, " "))
	if f.err != nil {
		return "", f.err
	}
	if err := resource.AfterCommit(txn, func(context.Context) { f.committed++ }); err != nil {
		return "", errors.Wrap(err, "resource.AfterCommit()")
	}

	return madeTenant, nil
}

func (f *fakeMaker) SeatsDefaultRole() bool {
	return f.seats
}

func (f *fakeMaker) DeclaresDirectoryOnly() bool {
	return f.directoryOnly
}

// invitedTenantRoles are the roles the fake engine holds for the tests of a tenant invitation:
// Reader and Keeper are roles of every tenant, and of the tenant the fake maker makes.
var invitedTenantRoles = map[accesstypes.PolicyScope][]accesstypes.Role{
	globalScope: {"Steward", accesstypes.SuperAdministrator},
	everyScope:  {"Reader", "Keeper"},
	accesstypes.DomainPolicyScope(madeTenant): {"Reader", "Keeper"},
}

// newTenantInviter builds a manager that makes tenants with the maker, seats their
// first administrator as Keeper, mails to the mailbox and judges roles by the grants.
func newTenantInviter(t *testing.T, store Store, maker TenantMaker, mail *fakeMail, grants RoleGrants, opts ...Option) *Manager {
	t.Helper()
	opts = append([]Option{WithAdministratorRoles([]accesstypes.Role{"Keeper"}), WithRoleGrants(grants)}, opts...)
	if maker != nil {
		opts = append(opts, WithTenantMaker(maker))
	}
	m, err := New(managedAuth, store, &fakeEngine{roles: invitedTenantRoles}, fakeSessions{mail: mail}, opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m
}

// TestManager_InviteTenant pins the invitation that makes a tenant as it is sent: the
// row names no tenant and no role and carries the tenant's name, whether it is directory
// only, and the details; the starting role of the tenant's directory is held to a role
// of a tenant the sender could give directly; and where a person belongs to one tenant,
// an address whose account belongs to one already is not invited.
func TestManager_InviteTenant(t *testing.T) {
	t.Parallel()

	fields := json.RawMessage(`{"Name":"Dawn"}`)
	tests := []struct {
		name    string
		inv     TenantInvitation
		caller  accesstypes.User
		maker   *fakeMaker
		noMaker bool
		grants  *fakeGrants
		opts    []Option
		emails  map[string]impulse.UUID
		holds   []access.MembershipKey
		wantErr func(error) bool
		// wantWrite is the buffered row, "" for none, and wantAsked what the role file
		// was asked.
		wantWrite string
		wantAsked int
	}{
		{
			name:      "the row names no tenant and no role, and carries the tenant's name and details",
			inv:       TenantInvitation{Email: " pat@example.com ", Name: ptr("Pat"), TenantName: " Dawn ", Fields: fields},
			caller:    dana,
			maker:     &fakeMaker{},
			wantWrite: "insert invitation " + invitationID.String() + ` pat@example.com tenant= roles= by=` + userID.String() + ` name=Pat new-tenant=Dawn directory-only=false details={"fields":{"Name":"Dawn"}}`,
		},
		{
			name:      "directory only and the starting role ride the invitation where the records keep them",
			inv:       TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", Fields: fields, DirectoryOnly: true, DefaultRole: ptr("Reader")},
			caller:    dana,
			maker:     &fakeMaker{seats: true, directoryOnly: true},
			wantWrite: "insert invitation " + invitationID.String() + ` pat@example.com tenant= roles= by=` + userID.String() + ` new-tenant=Dawn directory-only=true details={"defaultRole":"Reader","fields":{"Name":"Dawn"}}`,
			wantAsked: 1,
		},
		{
			name:   "a starting role the sender may not give is refused, naming it",
			inv:    TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", Fields: fields, DefaultRole: ptr("Keeper")},
			caller: dana,
			maker:  &fakeMaker{seats: true},
			grants: &fakeGrants{noGive: []accesstypes.Role{"Keeper"}},
			wantErr: func(err error) bool {
				return httpio.HasForbidden(err) && strings.Contains(httpio.Message(err), "Keeper")
			},
			wantAsked: 1,
		},
		{
			// The sender's grants answer first, so the sender who may give it is the one
			// told it is no role.
			name:      "a starting role that is no role of a tenant is refused",
			inv:       TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", Fields: fields, DefaultRole: ptr("Steward")},
			caller:    dana,
			maker:     &fakeMaker{seats: true},
			wantErr:   httpio.HasBadRequest,
			wantAsked: 1,
		},
		{
			name:   "a sender who may give no such role is told so before whether it is a role at all",
			inv:    TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", Fields: fields, DefaultRole: ptr("Steward")},
			caller: dana,
			maker:  &fakeMaker{seats: true},
			grants: &fakeGrants{noGive: []accesstypes.Role{"Steward"}},
			wantErr: func(err error) bool {
				return httpio.HasForbidden(err) && !httpio.HasBadRequest(err)
			},
			wantAsked: 1,
		},
		{
			name:   "the Super Administrator role is never a starting role",
			inv:    TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", Fields: fields, DefaultRole: ptr(string(accesstypes.SuperAdministrator))},
			caller: dana,
			maker:  &fakeMaker{seats: true},
			wantErr: func(err error) bool {
				return httpio.HasBadRequest(err) && strings.Contains(httpio.Message(err), "whole application")
			},
			wantAsked: 1,
		},
		{
			name:    "a starting role where a directory keeps none is refused",
			inv:     TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", Fields: fields, DefaultRole: ptr("Reader")},
			caller:  dana,
			maker:   &fakeMaker{},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "directory only where the record declares no switch is refused",
			inv:     TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", Fields: fields, DirectoryOnly: true},
			caller:  dana,
			maker:   &fakeMaker{},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "an invitation with no name for the tenant is refused",
			inv:     TenantInvitation{Email: "pat@example.com", TenantName: "  ", Fields: fields},
			caller:  dana,
			maker:   &fakeMaker{},
			wantErr: httpio.HasBadRequest,
		},
		{
			name:    "a manager built without a maker sends none",
			inv:     TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", Fields: fields},
			caller:  dana,
			noMaker: true,
			wantErr: func(err error) bool { return strings.Contains(err.Error(), "identity.WithTenantMaker") },
		},
		{
			name:   "where a person belongs to one tenant, an address whose account belongs to one is not invited",
			inv:    TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", Fields: fields},
			caller: dana,
			maker:  &fakeMaker{},
			opts:   []Option{WithOneTenantPerPerson()},
			emails: map[string]impulse.UUID{"pat@example.com": otherUser},
			holds:  []access.MembershipKey{access.NewMembershipKey(alphaScope, erin, "Reader")},
			wantErr: func(err error) bool {
				return httpio.HasConflict(err) && !strings.Contains(httpio.Message(err), "alpha")
			},
		},
		{
			name:      "where a person belongs to one tenant, one of the application's own people is invited",
			inv:       TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", Fields: fields},
			caller:    dana,
			maker:     &fakeMaker{},
			opts:      []Option{WithOneTenantPerPerson()},
			emails:    map[string]impulse.UUID{"pat@example.com": otherUser},
			holds:     []access.MembershipKey{access.NewMembershipKey(alphaScope, erin, "Reader"), access.NewMembershipKey(globalScope, erin, "Steward")},
			wantWrite: "insert invitation " + invitationID.String() + ` pat@example.com tenant= roles= by=` + userID.String() + ` new-tenant=Dawn directory-only=false details={"fields":{"Name":"Dawn"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, emails: tt.emails, memberships: map[access.MembershipKey]bool{}}
			for _, key := range tt.holds {
				store.memberships[key] = true
			}
			grants := tt.grants
			if grants == nil {
				grants = &fakeGrants{}
			}
			var maker TenantMaker
			if !tt.noMaker {
				maker = tt.maker
			}
			m := newTenantInviter(t, store, maker, &fakeMail{id: invitationID}, grants, tt.opts...)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				_, err := m.InviteTenant(asCaller(ctx, tt.caller), txn, &tt.inv, nil)

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("InviteTenant() error = %v, want the refusal", err)
				}
				if len(store.writes) != 0 {
					t.Errorf("writes = %v, want none: a refused invitation writes nothing", store.writes)
				}
			} else if err != nil {
				t.Fatalf("InviteTenant() error = %v", err)
			}
			var want []string
			if tt.wantWrite != "" {
				want = []string{tt.wantWrite}
			}
			if diff := cmp.Diff(want, store.writes); diff != "" {
				t.Errorf("writes mismatch (-want +got):\n%s", diff)
			}
			if len(grants.asked) != tt.wantAsked {
				t.Errorf("the role file was asked %v, want %d question(s)", grants.asked, tt.wantAsked)
			}
			if tt.maker != nil && len(tt.maker.made) != 0 {
				t.Errorf("the maker made %v at the send, want nothing: the tenant is made at the acceptance", tt.maker.made)
			}
		})
	}
}

// TestManager_InviteTenant_startingRoleIsAskedForATenantToBe pins what the role file is
// asked about a starting role: giving the role in one tenant, which no row names yet,
// to nobody in particular, so a grant that holds for named tenants alone does not admit it.
func TestManager_InviteTenant_startingRoleIsAskedForATenantToBe(t *testing.T) {
	t.Parallel()

	store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}}
	grants := &fakeGrants{}
	m := newTenantInviter(t, store, &fakeMaker{seats: true}, &fakeMail{id: invitationID}, grants)
	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		_, err := m.InviteTenant(asCaller(ctx, dana), txn, &TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", DefaultRole: ptr("Reader")}, nil)

		return err
	})
	if err != nil {
		t.Fatalf("InviteTenant() error = %v", err)
	}
	if len(grants.asked) != 1 || !strings.HasPrefix(grants.asked[0], "give domain/") || strings.Contains(grants.asked[0], "/alpha ") || !strings.HasSuffix(grants.asked[0], " Reader") {
		t.Errorf("the role file was asked %q, want one question: giving Reader in one tenant that is no tenant yet", grants.asked)
	}
}

// TestManager_CheckInvitedTenant pins the check a send makes: the tenant is made as the
// acceptance will make it, armed with the request's caller and with no directory, in a
// transaction that is rolled back; the maker's refusal is the answer; and a call outside
// any request checks nothing.
func TestManager_CheckInvitedTenant(t *testing.T) {
	t.Parallel()

	inv := &TenantInvitation{Email: "pat@example.com", TenantName: "Dawn", Fields: json.RawMessage(`{"Name":"Dawn"}`), DirectoryOnly: true, DefaultRole: ptr("Reader")}
	client := resource.NewMockClient(stubTxn{}, nil, nil)

	t.Run("the caller's grants are asked through the record's own create, and nothing is committed", func(t *testing.T) {
		t.Parallel()

		maker := &fakeMaker{}
		m := newTenantInviter(t, &fakeStore{}, maker, &fakeMail{}, &fakeGrants{})
		if err := m.CheckInvitedTenant(asCaller(t.Context(), dana), client, inv); err != nil {
			t.Fatalf("CheckInvitedTenant() error = %v", err)
		}
		want := []string{`fields={"Name":"Dawn"} directory-only=false directory=/ role= armed as ` + string(dana)}
		if diff := cmp.Diff(want, maker.made); diff != "" {
			t.Errorf("the maker was asked (-want +got):\n%s", diff)
		}
		if maker.committed != 0 {
			t.Errorf("the check committed %d transaction(s), want none: it is rolled back", maker.committed)
		}
	})
	t.Run("what the record's create refuses the caller is refused", func(t *testing.T) {
		t.Parallel()

		m := newTenantInviter(t, &fakeStore{}, &fakeMaker{err: httpio.NewForbiddenMessage("no Create on the record")}, &fakeMail{}, &fakeGrants{})
		if err := m.CheckInvitedTenant(asCaller(t.Context(), dana), client, inv); !httpio.HasForbidden(err) {
			t.Fatalf("CheckInvitedTenant() error = %v, want the record's Forbidden", err)
		}
	})
	t.Run("a call outside any request is the application's own act", func(t *testing.T) {
		t.Parallel()

		maker := &fakeMaker{err: errStore}
		m := newTenantInviter(t, &fakeStore{}, maker, &fakeMail{}, &fakeGrants{})
		if err := m.CheckInvitedTenant(t.Context(), client, inv); err != nil || len(maker.made) != 0 {
			t.Fatalf("CheckInvitedTenant() error = %v, made = %v; want nothing asked", err, maker.made)
		}
	})
}

// tenantInvitation is an invitation that makes a tenant as a sign-in carries it.
func tenantInvitation(directoryOnly bool, method sessioninfo.AuthMethod, connection string) *sessioninfo.Invitation {
	return &sessioninfo.Invitation{
		ID: invitationID, Email: "pat@example.com", SignInMethod: method, SignInConnection: connection,
		NewTenant: &sessioninfo.NewTenant{Name: "Dawn", DirectoryOnly: directoryOnly, Details: json.RawMessage(`{"defaultRole":"Reader","fields":{"Name":"Dawn"}}`)},
	}
}

// TestManager_acceptTenantInvitation pins the acceptance of an invitation that makes a
// tenant: the tenant is made from the invitation's details, with the directory the
// person signed in through where it is an organization's and with none otherwise, and
// the person is seated with the roles that administer a tenant; a directory-only
// invitation makes nothing without a directory; and whatever refuses, the maker or the
// membership, nothing is left written by the rules.
func TestManager_acceptTenantInvitation(t *testing.T) {
	t.Parallel()

	seat := "insert membership " + membershipText(ptr(access.NewMembershipKey(accesstypes.DomainPolicyScope(madeTenant), dana, "Keeper")))
	refusal := sessioninfo.NewLoginRefusal(sessioninfo.RefusedOrganizationHasTenant, httpio.NewConflictMessage("the organization already has a tenant here"))
	tests := []struct {
		name     string
		inv      *sessioninfo.Invitation
		maker    *fakeMaker
		opts     []Option
		holds    []access.MembershipKey
		wantMade []string
		wantErr  func(error) bool
		// wantWrites are the memberships buffered.
		wantWrites []string
	}{
		{
			name:       "through a Google Workspace: the tenant takes it as its directory, with the starting role",
			inv:        tenantInvitation(false, sessioninfo.MethodGoogle, "acme.example"),
			maker:      &fakeMaker{seats: true},
			wantMade:   []string{`fields={"Name":"Dawn"} directory-only=false directory=google/acme.example role=Reader trusted`},
			wantWrites: []string{seat},
		},
		{
			name:       "by password: the tenant has no directory",
			inv:        tenantInvitation(false, sessioninfo.MethodPassword, ""),
			maker:      &fakeMaker{seats: true},
			wantMade:   []string{`fields={"Name":"Dawn"} directory-only=false directory=/ role=Reader trusted`},
			wantWrites: []string{seat},
		},
		{
			name:       "with a personal Google account: the tenant has no directory",
			inv:        tenantInvitation(false, sessioninfo.MethodGoogle, signin.GoogleAnyAccountConnection),
			maker:      &fakeMaker{seats: true},
			wantMade:   []string{`fields={"Name":"Dawn"} directory-only=false directory=/ role=Reader trusted`},
			wantWrites: []string{seat},
		},
		{
			name:       "directory only, through an organization's directory: made directory only with its directory",
			inv:        tenantInvitation(true, sessioninfo.MethodAzure, "9a2c1d0e-0000-4000-8000-000000000001"),
			maker:      &fakeMaker{seats: true, directoryOnly: true},
			wantMade:   []string{`fields={"Name":"Dawn"} directory-only=true directory=azure/9a2c1d0e-0000-4000-8000-000000000001 role=Reader trusted`},
			wantWrites: []string{seat},
		},
		{
			name:  "directory only, by password: refused, and nothing is made",
			inv:   tenantInvitation(true, sessioninfo.MethodPassword, ""),
			maker: &fakeMaker{seats: true, directoryOnly: true},
			wantErr: func(err error) bool {
				return sessioninfo.LoginRefusalCodeOf(err) == sessioninfo.RefusedOrganizationAccountRequired
			},
		},
		{
			name:     "a directory another tenant has: the maker's refusal is the sign-in's, and nobody is seated",
			inv:      tenantInvitation(false, sessioninfo.MethodGoogle, "acme.example"),
			maker:    &fakeMaker{seats: true, err: refusal},
			wantMade: []string{`fields={"Name":"Dawn"} directory-only=false directory=google/acme.example role=Reader trusted`},
			wantErr: func(err error) bool {
				return sessioninfo.LoginRefusalCodeOf(err) == sessioninfo.RefusedOrganizationHasTenant
			},
		},
		{
			name:     "where a person belongs to one tenant, a person of another tenant is refused and told no more",
			inv:      tenantInvitation(false, sessioninfo.MethodPassword, ""),
			maker:    &fakeMaker{seats: true},
			opts:     []Option{WithOneTenantPerPerson()},
			holds:    []access.MembershipKey{access.NewMembershipKey(alphaScope, dana, "Reader")},
			wantMade: []string{`fields={"Name":"Dawn"} directory-only=false directory=/ role=Reader trusted`},
			wantErr: func(err error) bool {
				return sessioninfo.LoginRefusalCodeOf(err) == sessioninfo.RefusedOtherTenant && !strings.Contains(httpio.Message(err), "alpha")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, memberships: map[access.MembershipKey]bool{}}
			for _, key := range tt.holds {
				store.memberships[key] = true
			}
			m := newTenantInviter(t, store, tt.maker, &fakeMail{}, &fakeGrants{}, tt.opts...)

			var domain accesstypes.Domain
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				domain, err = m.acceptTenantInvitation(ctx, txn, userID, "dana", tt.inv)

				return err
			})
			switch {
			case tt.wantErr != nil:
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("acceptTenantInvitation() error = %v, want the refusal", err)
				}
			case err != nil:
				t.Fatalf("acceptTenantInvitation() error = %v", err)
			case domain != madeTenant:
				t.Errorf("acceptTenantInvitation() = %q, want the tenant the maker made", domain)
			}
			if diff := cmp.Diff(tt.wantMade, tt.maker.made); diff != "" {
				t.Errorf("the maker was asked (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("writes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_InvitationAccepted_makesATenant pins that the session library's hook hands
// an invitation that makes a tenant to its own acceptance, and gives no role by name.
func TestManager_InvitationAccepted_makesATenant(t *testing.T) {
	t.Parallel()

	maker := &fakeMaker{err: errStore}
	m := newTenantInviter(t, &fakeStore{}, maker, &fakeMail{}, &fakeGrants{})
	_, err := m.InvitationAccepted("Staff")(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", tenantInvitation(false, sessioninfo.MethodPassword, ""))
	if !errors.Is(err, errStore) || len(maker.made) != 1 {
		t.Fatalf("InvitationAccepted() error = %v, made = %v; want the maker asked once and its failure answered", err, maker.made)
	}
}

// TestManager_InvitedTenants pins the list staff read and the withdrawal: the
// invitations that make a tenant and still stand, each with its details read out of the
// row, an expired one left out, and a withdrawal that holds to these invitations alone.
func TestManager_InvitedTenants(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	standing := mustUUID("55555555-5555-4555-8555-555555555555")
	expired := mustUUID("66666666-6666-4666-8666-666666666666")
	document := `{"name":"Dawn","directoryOnly":true,"details":{"defaultRole":"Reader","fields":{"Name":"Dawn"}}}`
	newStore := func() *fakeStore {
		return &fakeStore{invited: []InvitedTenantRow{
			{ID: standing, Email: "pat@example.com", Name: "Pat", NewTenant: document, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)},
			{ID: expired, Email: "lee@example.com", NewTenant: document, CreatedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-time.Hour)},
		}}
	}
	build := func(store *fakeStore) *Manager {
		m := newTenantInviter(t, store, &fakeMaker{}, &fakeMail{}, &fakeGrants{})
		m.now = func() time.Time {
			return now
		}

		return m
	}

	t.Run("the standing invitations, each with its details", func(t *testing.T) {
		t.Parallel()

		got, err := build(newStore()).InvitedTenants(t.Context(), stubTxn{})
		if err != nil {
			t.Fatalf("InvitedTenants() error = %v", err)
		}
		want := []*InvitedTenant{{
			ID: standing, Email: "pat@example.com", Name: "Pat", TenantName: "Dawn", DirectoryOnly: true, DefaultRole: "Reader",
			Fields: json.RawMessage(`{"Name":"Dawn"}`), CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("InvitedTenants() mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("a standing one is withdrawn", func(t *testing.T) {
		t.Parallel()

		store := newStore()
		m := build(store)
		if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.WithdrawTenantInvitation(ctx, txn, standing)
		}); err != nil {
			t.Fatalf("WithdrawTenantInvitation() error = %v", err)
		}
		if diff := cmp.Diff([]string{"consume mail link " + standing.String()}, store.writes); diff != "" {
			t.Errorf("writes mismatch (-want +got):\n%s", diff)
		}
	})
	for name, id := range map[string]impulse.UUID{"one that has expired": expired, "an invitation of another kind": invitationID} {
		t.Run(name+" is not found, and nothing is written", func(t *testing.T) {
			t.Parallel()

			store := newStore()
			m := build(store)
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.WithdrawTenantInvitation(ctx, txn, id)
			})
			if !httpio.HasNotFound(err) || len(store.writes) != 0 {
				t.Fatalf("WithdrawTenantInvitation() error = %v, writes = %v; want NotFound and nothing written", err, store.writes)
			}
		})
	}
}
