package identity

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// wantAccountRow holds a call of a rule that acts on an account to its row of the audit
// log: none where the call failed or buffered no write, and otherwise exactly one, of
// the act, done to the account.
func wantAccountRow(t *testing.T, store *fakeStore, err error, act auditlog.Act, account impulse.UUID) {
	t.Helper()

	if err != nil || len(store.writes) == 0 {
		if len(store.audit) != 0 {
			t.Errorf("audit rows = %d (%s first), want none: the call wrote nothing", len(store.audit), store.audit[0].Act)
		}

		return
	}
	if len(store.audit) != 1 {
		t.Fatalf("audit rows = %d, want the one of %s", len(store.audit), act)
	}
	if got := store.audit[0]; got.Act != act || got.SubjectKind != auditlog.SubjectAccount || got.SubjectID != account.String() {
		t.Errorf("audit row = %s done to the %s %s, want %s done to the account %s", got.Act, got.SubjectKind, got.SubjectID, act, account)
	}
}

// TestAccountActsRecorded pins the row each of an administrator's acts on an account
// writes: one, of its act, done to the account by its id and its username on the day
// (the new one for a rename), with who acted and the tenant the request was checked in;
// and outside any request, the application, in no tenant.
func TestAccountActsRecorded(t *testing.T) {
	t.Parallel()

	ahead := time.Now().Add(time.Hour)
	acts := []struct {
		name  string
		store func() *fakeStore
		act   func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		// want is the row's act and parts; its subject is dana's account, by the name
		// subjectName, or "dana" where it is empty.
		want        auditlog.Record
		subjectName string
	}{
		{
			name:  "disable",
			store: func() *fakeStore { return &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}} },
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.DisableUser(ctx, txn, userID)
			},
			want: auditlog.Record{Act: auditlog.ActAccountDisabled},
		},
		{
			name:  "enable",
			store: func() *fakeStore { return &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}} },
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.EnableUser(ctx, txn, userID)
			},
			want: auditlog.Record{Act: auditlog.ActAccountEnabled},
		},
		{
			name: "require a password change",
			store: func() *fakeStore {
				return &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, passwords: map[impulse.UUID]bool{userID: true}}
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RequirePasswordChange(ctx, txn, userID)
			},
			want: auditlog.Record{Act: auditlog.ActPasswordChangeRequired},
		},
		{
			name:  "rename",
			store: func() *fakeStore { return &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}} },
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RenameUser(ctx, txn, userID, " dana.reyes ")
			},
			want:        auditlog.Record{Act: auditlog.ActUsernameChanged, OldValue: "dana", NewValue: "dana.reyes"},
			subjectName: "dana.reyes",
		},
		{
			name: "forget the remembered browsers",
			store: func() *fakeStore {
				return &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, devices: map[impulse.UUID][]impulse.UUID{userID: {deviceA}}}
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RevokeTrustedDevices(ctx, txn, userID)
			},
			want: auditlog.Record{Act: auditlog.ActBrowsersForgotten},
		},
		{
			name: "unlink an identity",
			store: func() *fakeStore {
				return &fakeStore{
					usernames: map[impulse.UUID]string{userID: "dana"}, passwords: map[impulse.UUID]bool{userID: true},
					identities: map[impulse.UUID]impulse.UUID{identityA: userID},
				}
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.UnlinkIdentity(ctx, txn, identityA)
			},
			want: auditlog.Record{Act: auditlog.ActIdentityUnlinked, OldValue: "Lakeside"},
		},
		{
			name: "remove the password",
			store: func() *fakeStore {
				return &fakeStore{
					usernames: map[impulse.UUID]string{userID: "dana"}, passwords: map[impulse.UUID]bool{userID: true},
					identities: map[impulse.UUID]impulse.UUID{identityA: userID},
				}
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RemovePassword(ctx, txn, userID)
			},
			want: auditlog.Record{Act: auditlog.ActPasswordRemoved},
		},
		{
			name: "revoke a token, with its family",
			store: func() *fakeStore {
				return &fakeStore{
					usernames: map[impulse.UUID]string{userID: "dana"},
					tokens:    map[impulse.UUID]*Token{tokenA: {UserID: userID, FamilyID: &familyID, ExpiresAt: ahead}},
					families:  map[impulse.UUID][]impulse.UUID{familyID: {tokenA, tokenB}},
				}
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RevokeToken(ctx, txn, tokenA)
			},
			want: auditlog.Record{Act: auditlog.ActTokenRevoked},
		},
		{
			// The account's roles go with it, and have no row of their own.
			name: "delete, with three roles",
			store: func() *fakeStore {
				return &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, memberships: map[access.MembershipKey]bool{
					access.NewMembershipKey(alphaScope, User(userID), "Editor"): true,
					access.NewMembershipKey(alphaScope, User(userID), "Reader"): true,
					access.NewMembershipKey(alphaScope, User(userID), "Viewer"): true,
				}}
			},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				_, err := m.DeleteUser(ctx, txn, userID, false)

				return err
			},
			want: auditlog.Record{Act: auditlog.ActAccountDeleted},
		},
	}
	callers := []struct {
		name string
		ctx  context.Context
		// wantSource is who acted, and wantTenant and wantName the tenant.
		wantSource           eventsource.EventSource
		wantTenant, wantName string
	}{
		{
			name: "a caller in a tenant's scope", ctx: recordCaller(accesstypes.DomainScope("alpha")),
			wantSource: eventsource.UserEvent(recordCaller(accesstypes.DomainScope("alpha"))), wantTenant: "alpha", wantName: "Alpha Sector",
		},
		{name: "a call outside any request", ctx: context.Background(), wantSource: eventsource.ProcessEvent(auditlog.ProcessApplication)},
	}
	for _, act := range acts {
		for _, caller := range callers {
			t.Run(act.name+", "+caller.name, func(t *testing.T) {
				t.Parallel()

				store := act.store()
				sessions := fakeSessions{tenantNames: map[accesstypes.Domain]string{"alpha": "Alpha Sector"}, named: map[string]string{"lakeside": "Lakeside"}}
				m, err := New(managedAuth, store, &fakeEngine{}, sessions)
				if err != nil {
					t.Fatalf("New() error = %v", err)
				}
				if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
					return act.act(caller.ctx, txn, m)
				}); err != nil {
					t.Fatalf("%s error = %v", act.name, err)
				}

				want := act.want
				want.SubjectKind, want.SubjectID, want.SubjectName = auditlog.SubjectAccount, userID.String(), "dana"
				if act.subjectName != "" {
					want.SubjectName = act.subjectName
				}
				want.Source, want.TenantID, want.TenantName = caller.wantSource, caller.wantTenant, caller.wantName
				if diff := cmp.Diff([]auditlog.Record{want}, store.audit, cmp.AllowUnexported(eventsource.EventSource{})); diff != "" {
					t.Errorf("audit rows (-want +got):\n%s", diff)
				}
			})
		}
	}
}

// TestAccountActsRecorded_wroteNothing pins the acts that write nothing and so record
// nothing: a rename to the name the account holds, and a reset of the second factor of a
// person with no browser remembered.
func TestAccountActsRecorded_wroteNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		act  func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
	}{
		{
			name: "a rename to the name held",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RenameUser(ctx, txn, userID, "dana")
			},
		},
		{
			name: "no browser remembered",
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RevokeTrustedDevices(ctx, txn, userID)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}}
			m, _ := newManager(t, store, nil)
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.act(ctx, txn, m)
			}); err != nil {
				t.Fatalf("%s error = %v", tt.name, err)
			}
			if len(store.audit) != 0 {
				t.Errorf("audit rows = %d (%s), want none", len(store.audit), store.audit[0].Act)
			}
		})
	}
}

// TestAccountActsRecorded_unlinkedDirectory pins how the row of an unlink names the
// directory the identity came through: by the name the auth's sign-in registrations give
// it, by its button's label where they give none, and by the method's own name where they
// give neither; and the sentence a person reads of it.
func TestAccountActsRecorded_unlinkedDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sessions fakeSessions
		want     string
		wantWhat string
	}{
		{
			name:     "a directory the registrations name",
			sessions: fakeSessions{named: map[string]string{"lakeside": "Lakeside"}, labels: map[sessioninfo.AuthMethod]string{sessioninfo.MethodGoogle: "Google"}},
			want:     "Lakeside", wantWhat: "Unlinked the sign-in through Lakeside",
		},
		{
			name:     "a directory known by its button alone",
			sessions: fakeSessions{labels: map[sessioninfo.AuthMethod]string{sessioninfo.MethodGoogle: "Google"}},
			want:     "Google", wantWhat: "Unlinked the sign-in through Google",
		},
		{
			name:     "a directory no registration takes any more",
			sessions: fakeSessions{},
			want:     "google", wantWhat: "Unlinked the sign-in through google",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{
				usernames: map[impulse.UUID]string{userID: "dana"}, passwords: map[impulse.UUID]bool{userID: true},
				identities: map[impulse.UUID]impulse.UUID{identityA: userID},
			}
			m, err := New(managedAuth, store, &fakeEngine{}, tt.sessions)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.UnlinkIdentity(ctx, txn, identityA)
			}); err != nil {
				t.Fatalf("UnlinkIdentity() error = %v", err)
			}
			if len(store.audit) != 1 {
				t.Fatalf("audit rows = %d, want one", len(store.audit))
			}
			if got := store.audit[0].OldValue; got != tt.want {
				t.Errorf("the row names the directory %q, want %q", got, tt.want)
			}
			stored := storedOf(t, &store.audit[0])
			if got := stored.Sentences("tenant").What; got != tt.wantWhat {
				t.Errorf("the row reads %q, want %q", got, tt.wantWhat)
			}
		})
	}
}

// TestDeleteUser_recordedOnce pins the row of an account's delete in two saves: none in
// the save that leaves the account standing, and one, done to the account by its
// username, in the save that deletes its row.
func TestDeleteUser_recordedOnce(t *testing.T) {
	t.Parallel()

	store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, userLeft: &resource.DeleteLeft{Deleted: 2}}
	m, err := New(managedAuth, store, &fakeEngine{}, fakeSessions{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	round := func(t *testing.T) *resource.DeleteLeft {
		t.Helper()

		var left *resource.DeleteLeft
		if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			var err error
			left, err = m.DeleteUser(ctx, txn, userID, true)

			return err
		}); err != nil {
			t.Fatalf("DeleteUser() error = %v", err)
		}

		return left
	}

	if left := round(t); left == nil {
		t.Fatal("the first save's DeleteUser() left nothing, want what the fake left")
	}
	if len(store.audit) != 0 {
		t.Fatalf("the save that left the account standing wrote %d audit rows (%s first), want none", len(store.audit), store.audit[0].Act)
	}
	store.userLeft = nil
	if left := round(t); left != nil {
		t.Fatalf("the second save's DeleteUser() left %+v, want the account gone", left)
	}
	want := []auditlog.Record{{
		Act: auditlog.ActAccountDeleted, Source: eventsource.ProcessEvent(auditlog.ProcessApplication),
		SubjectKind: auditlog.SubjectAccount, SubjectID: userID.String(), SubjectName: "dana",
	}}
	if diff := cmp.Diff(want, store.audit, cmp.AllowUnexported(eventsource.EventSource{})); diff != "" {
		t.Errorf("audit rows (-want +got):\n%s", diff)
	}
}
