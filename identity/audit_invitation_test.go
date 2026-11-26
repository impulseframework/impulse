package identity

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
)

// preparedTokenHash is the hash of the token the fake session API makes for an
// invitation: a secret of the invitation's row, which no row of the audit log holds.
const preparedTokenHash = "the prepared invitation's token hash"

// wantInvitationRow holds a call to its row of the audit log: exactly one, of the act,
// done to the invitation, where the call buffered a write, and none where it buffered
// nothing.
func wantInvitationRow(t *testing.T, store *fakeStore, act auditlog.Act, invitation impulse.UUID) {
	t.Helper()

	kind, subject := auditlog.SubjectInvitation, invitation

	if len(store.writes) == 0 {
		if len(store.audit) != 0 {
			t.Errorf("audit rows = %d (%s first), want none: the call wrote nothing", len(store.audit), store.audit[0].Act)
		}

		return
	}
	if len(store.audit) != 1 {
		t.Fatalf("audit rows = %d, want the one of %s", len(store.audit), act)
	}
	if got := store.audit[0]; got.Act != act || got.SubjectKind != kind || got.SubjectID != subject.String() {
		t.Errorf("audit row = %s done to the %s %s, want %s done to the %s %s", got.Act, got.SubjectKind, got.SubjectID, act, kind, subject)
	}
}

// holdsText reports whether a column of the record, as the store writes it, holds the
// text.
func holdsText(t *testing.T, record *auditlog.Record, text string) bool {
	t.Helper()

	columns, err := record.Columns()
	if err != nil {
		t.Fatalf("Record.Columns() error = %v", err)
	}
	for _, value := range columns {
		switch value := value.(type) {
		case string:
			if strings.Contains(value, text) {
				return true
			}
		case []string:
			if slices.ContainsFunc(value, func(v string) bool { return strings.Contains(v, text) }) {
				return true
			}
		}
	}

	return false
}

// TestInvitationActsRecorded pins the row of each act on an invitation: one, done to the
// invitation by its id and the invited address; an invitation with the roles and the
// place it stores, in its tenant; one that makes a tenant in its form, with no role and
// no tenant; a withdrawal; and an invitation sent again as one row of its own, done to
// the new invitation. A withdrawal and a second sending are in the tenant the invitation
// invites into, whoever acted, and in the tenant the request was checked in where the
// invitation names none. No row holds the invitation's secret.
func TestInvitationActsRecorded(t *testing.T) {
	t.Parallel()

	oldID := mustUUID("66666666-6666-4666-8666-666666666666")
	ahead := time.Now().Add(time.Hour)
	nightKey := access.NewMembershipKey(alphaScope, "", "NightShiftLead")
	alphaNightShiftLead := access.RoleKey{Kind: nightKey.Kind, Axis: nightKey.Axis, Domain: nightKey.Domain, Role: "NightShiftLead"}
	alphaCaller := recordCaller(accesstypes.DomainScope("alpha"))
	// globalCaller is an administrator of the whole application, checked in no tenant.
	globalCaller := recordCaller(accesstypes.GlobalScope())
	// The roles of the tests' engine, and Keeper, the role that administers a tenant an
	// invitation makes.
	roles := map[accesstypes.PolicyScope][]accesstypes.Role{globalScope: {"Steward"}, alphaScope: {"Reader"}, everyScope: {"Reader", "Keeper"}}
	makes := []Option{WithTenantMaker(&fakeMaker{}), WithAdministratorRoles([]accesstypes.Role{"Keeper"})}

	tests := []struct {
		name  string
		store func() *fakeStore
		opts  []Option
		ctx   context.Context
		act   func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		// want is the row, but who acted; wantWhat what a person reads of it.
		want     auditlog.Record
		wantWhat string
	}{
		{
			name:  "an invitation into a tenant, of two roles",
			store: func() *fakeStore { return &fakeStore{roles: map[access.RoleKey]bool{alphaNightShiftLead: true}} },
			ctx:   alphaCaller,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				_, err := m.Invite(ctx, txn, &Invitation{Email: "pat@example.com", Domain: ptr("alpha"), Roles: []accesstypes.Role{"Reader", "NightShiftLead"}}, nil)

				return err
			},
			want: auditlog.Record{
				Act: auditlog.ActInvited, SubjectKind: auditlog.SubjectInvitation, SubjectID: invitationID.String(), SubjectName: "pat@example.com",
				RoleNames: []string{"Reader", "NightShiftLead"}, RoleScope: auditlog.RoleScopeTenant, TenantID: "alpha", TenantName: "Alpha Sector",
			},
			wantWhat: "Invited with the roles Reader, Night shift lead",
		},
		{
			name:  "an invitation into every tenant",
			store: func() *fakeStore { return &fakeStore{} },
			ctx:   context.Background(),
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				_, err := m.Invite(ctx, txn, &Invitation{Email: "pat@example.com", EveryDomain: true, Roles: []accesstypes.Role{"Reader"}}, nil)

				return err
			},
			want: auditlog.Record{
				Act: auditlog.ActInvited, SubjectKind: auditlog.SubjectInvitation, SubjectID: invitationID.String(), SubjectName: "pat@example.com",
				RoleNames: []string{"Reader"}, RoleScope: auditlog.RoleScopeEveryTenant,
			},
			wantWhat: "Invited with the role Reader in every sector",
		},
		{
			name:  "an invitation into the whole application",
			store: func() *fakeStore { return &fakeStore{} },
			ctx:   context.Background(),
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				_, err := m.Invite(ctx, txn, &Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}}, nil)

				return err
			},
			want: auditlog.Record{
				Act: auditlog.ActInvited, SubjectKind: auditlog.SubjectInvitation, SubjectID: invitationID.String(), SubjectName: "pat@example.com",
				RoleNames: []string{"Steward"}, RoleScope: auditlog.RoleScopeGlobal,
			},
			wantWhat: "Invited with the role Steward for the whole application",
		},
		{
			name:  "an invitation that makes a tenant",
			store: func() *fakeStore { return &fakeStore{} },
			opts:  makes,
			ctx:   context.Background(),
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				_, err := m.InviteTenant(ctx, txn, &TenantInvitation{Email: "pat@example.com", TenantName: "Dawn"}, nil)

				return err
			},
			want: auditlog.Record{
				Act: auditlog.ActInvited, Form: auditlog.FormNewTenant, SubjectKind: auditlog.SubjectInvitation, SubjectID: invitationID.String(),
				SubjectName: "pat@example.com",
			},
			wantWhat: "Invited to make a sector",
		},
		{
			name: "a withdrawal",
			store: func() *fakeStore {
				return &fakeStore{
					links:           map[impulse.UUID]*MailLink{invitationID: {Invitation: true, ExpiresAt: ahead}},
					sentInvitations: map[impulse.UUID]*SentInvitation{invitationID: {Invitation: Invitation{Email: "pat@example.com", Roles: []accesstypes.Role{"Steward"}}, ExpiresAt: ahead}},
				}
			},
			ctx: alphaCaller,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.WithdrawInvitation(ctx, txn, invitationID)
			},
			want: auditlog.Record{
				Act: auditlog.ActInvitationWithdrawn, SubjectKind: auditlog.SubjectInvitation, SubjectID: invitationID.String(), SubjectName: "pat@example.com",
				TenantID: "alpha", TenantName: "Alpha Sector",
			},
			wantWhat: "Withdrew the invitation",
		},
		{
			name: "a withdrawal of an invitation into a tenant, by an administrator checked in no tenant",
			store: func() *fakeStore {
				return &fakeStore{
					links: map[impulse.UUID]*MailLink{invitationID: {Invitation: true, ExpiresAt: ahead}},
					sentInvitations: map[impulse.UUID]*SentInvitation{
						invitationID: {Invitation: Invitation{Email: "pat@example.com", Domain: ptr("alpha"), Roles: []accesstypes.Role{"Reader"}}, ExpiresAt: ahead},
					},
				}
			},
			ctx: globalCaller,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.WithdrawInvitation(ctx, txn, invitationID)
			},
			want: auditlog.Record{
				Act: auditlog.ActInvitationWithdrawn, SubjectKind: auditlog.SubjectInvitation, SubjectID: invitationID.String(), SubjectName: "pat@example.com",
				TenantID: "alpha", TenantName: "Alpha Sector",
			},
			wantWhat: "Withdrew the invitation",
		},
		{
			name: "a withdrawal of an invitation whose address the store does not answer",
			store: func() *fakeStore {
				return &fakeStore{links: map[impulse.UUID]*MailLink{invitationID: {Invitation: true, ExpiresAt: ahead}}}
			},
			ctx: context.Background(),
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.WithdrawInvitation(ctx, txn, invitationID)
			},
			want: auditlog.Record{
				Act: auditlog.ActInvitationWithdrawn, SubjectKind: auditlog.SubjectInvitation, SubjectID: invitationID.String(), SubjectName: invitationID.String(),
			},
			wantWhat: "Withdrew the invitation",
		},
		{
			name: "a withdrawal of an invitation that makes a tenant",
			store: func() *fakeStore {
				return &fakeStore{invited: []InvitedTenantRow{{ID: invitationID, Email: "founder@dawn.example", NewTenant: `{"name":"Dawn"}`, ExpiresAt: ahead}}}
			},
			opts: makes,
			ctx:  context.Background(),
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.WithdrawTenantInvitation(ctx, txn, invitationID)
			},
			want: auditlog.Record{
				Act: auditlog.ActInvitationWithdrawn, SubjectKind: auditlog.SubjectInvitation, SubjectID: invitationID.String(), SubjectName: "founder@dawn.example",
			},
			wantWhat: "Withdrew the invitation",
		},
		{
			name: "an invitation sent again, as one row done to the new invitation",
			store: func() *fakeStore {
				return &fakeStore{sentInvitations: map[impulse.UUID]*SentInvitation{
					oldID: {Invitation: Invitation{Email: "pat@example.com", Domain: ptr("alpha"), Roles: []accesstypes.Role{"Reader"}}, ExpiresAt: ahead},
				}}
			},
			ctx: alphaCaller,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				_, err := m.ResendInvitation(ctx, txn, oldID, nil)

				return err
			},
			want: auditlog.Record{
				Act: auditlog.ActInvitationSentAgain, SubjectKind: auditlog.SubjectInvitation, SubjectID: invitationID.String(), SubjectName: "pat@example.com",
				TenantID: "alpha", TenantName: "Alpha Sector",
			},
			wantWhat: "Sent the invitation again",
		},
		{
			name: "an invitation into a tenant sent again by an administrator checked in no tenant",
			store: func() *fakeStore {
				return &fakeStore{sentInvitations: map[impulse.UUID]*SentInvitation{
					oldID: {Invitation: Invitation{Email: "pat@example.com", Domain: ptr("alpha"), Roles: []accesstypes.Role{"Reader"}}, ExpiresAt: ahead},
				}}
			},
			ctx: globalCaller,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				_, err := m.ResendInvitation(ctx, txn, oldID, nil)

				return err
			},
			want: auditlog.Record{
				Act: auditlog.ActInvitationSentAgain, SubjectKind: auditlog.SubjectInvitation, SubjectID: invitationID.String(), SubjectName: "pat@example.com",
				TenantID: "alpha", TenantName: "Alpha Sector",
			},
			wantWhat: "Sent the invitation again",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := tt.store()
			sessions := fakeSessions{mail: &fakeMail{id: invitationID}, tenantNames: map[accesstypes.Domain]string{"alpha": "Alpha Sector"}}
			opts := append([]Option{WithTenantWord("sector")}, tt.opts...)
			m, err := New(managedAuth, store, &fakeEngine{roles: roles}, sessions, opts...)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.act(tt.ctx, txn, m)
			}); err != nil {
				t.Fatalf("%s error = %v", tt.name, err)
			}

			want := tt.want
			want.Source = eventsource.ProcessEvent(auditlog.ProcessApplication)
			if tt.ctx == alphaCaller || tt.ctx == globalCaller {
				want.Source = eventsource.UserEvent(tt.ctx)
			}
			if diff := cmp.Diff([]auditlog.Record{want}, store.audit, cmp.AllowUnexported(eventsource.EventSource{})); diff != "" {
				t.Fatalf("audit rows (-want +got):\n%s", diff)
			}
			stored := storedOf(t, &store.audit[0])
			if got := stored.Sentences("sector").What; got != tt.wantWhat {
				t.Errorf("the row reads %q, want %q", got, tt.wantWhat)
			}
			if holdsText(t, &store.audit[0], preparedTokenHash) {
				t.Errorf("the row holds the invitation's token hash: %+v", store.audit[0])
			}
		})
	}
}
