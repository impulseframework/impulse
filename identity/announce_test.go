package identity

import (
	"context"
	"sync"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// announceTables are the names a test's auth gives its tables.
var announceTables = AccountTables{
	Users: "Users", Sessions: "Sessions", Identities: "Identities", Memberships: "Memberships", MailLinks: "MailLinks",
}

// announcement is what one call of the publishing function was handed: the collector's
// rows, each listed under the zero domain when it carried none, and its tables touched
// whole.
type announcement struct {
	Rows   map[accesstypes.Domain]map[accesstypes.Resource][]resource.RowChange
	Tables []accesstypes.Resource
}

// announcements records each call of the publishing function a test hands a manager.
type announcements struct {
	mu    sync.Mutex
	calls []announcement
}

// publish is the function handed over (Manager.AnnounceWith).
func (a *announcements) publish(_ context.Context, touched *resource.TouchedRows) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, announcement{Rows: touched.Rows(""), Tables: touched.Tables()})
}

// got returns the calls so far.
func (a *announcements) got() []announcement {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([]announcement(nil), a.calls...)
}

// rowsOf builds an announcement's rows, all under the zero domain.
func rowsOf(rows map[accesstypes.Resource][]resource.RowChange) map[accesstypes.Domain]map[accesstypes.Resource][]resource.RowChange {
	return map[accesstypes.Domain]map[accesstypes.Resource][]resource.RowChange{"": rows}
}

// key is a row's change as a page is told it.
func key(id impulse.UUID) resource.RowChange {
	return resource.RowChange{Key: resource.RowKey(id)}
}

// announcingManager builds a manager over store that names the test's tables and has
// been handed a recorder to announce through.
func announcingManager(t *testing.T, store Store, roles map[accesstypes.PolicyScope][]accesstypes.Role) (*Manager, *announcements) {
	t.Helper()

	m, _ := newManager(t, store, roles, WithAccountTables(&announceTables))
	recorder := &announcements{}
	m.AnnounceWith(recorder.publish)

	return m, recorder
}

// TestManager_SignedIn_announces pins what the after-sign-in hook announces: the session
// and the account by their ids, the identities and the memberships whole, on every
// return of the hook, a failed one included.
func TestManager_SignedIn_announces(t *testing.T) {
	t.Parallel()

	signedIn := &session.SignedIn{SessionID: sessionA, UserID: userID, Username: "dana"}
	want := []announcement{{
		Rows:   rowsOf(map[accesstypes.Resource][]resource.RowChange{"Sessions": {key(sessionA)}, "Users": {key(userID)}}),
		Tables: []accesstypes.Resource{"Identities", "Memberships"},
	}}
	tests := []struct {
		name    string
		store   func() *fakeStore
		failOn  string
		wantErr bool
	}{
		{name: "the ended assignments go", store: func() *fakeStore { s, _, _ := clearStore(); return s }},
		{name: "nothing to clear", store: groupStore},
		{name: "the read fails", store: func() *fakeStore { s, _, _ := clearStore(); return s }, failOn: "EndedAssignments", wantErr: true},
		{name: "the removal fails", store: func() *fakeStore { s, _, _ := clearStore(); return s }, failOn: "RemoveEnded", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := tt.store()
			store.failOn = tt.failOn
			m, recorder := announcingManager(t, store, engineRoles)
			client := resource.NewMockClient(stubTxn{}, nil, nil)

			if err := m.SignedIn(managedAuth, client)(t.Context(), signedIn); (err != nil) != tt.wantErr {
				t.Fatalf("the hook's error = %v, want an error = %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(want, recorder.got(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("announced (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_committedHooks_announce pins what the committed function of each hook a
// sign-in's own transaction runs announces, once the transaction has committed and not
// before: the account by its id, the invitation's mail link where one was accepted, and
// the identities whole.
func TestManager_committedHooks_announce(t *testing.T) {
	t.Parallel()

	inv := &sessioninfo.Invitation{ID: invitationID, Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Reader"}}
	seats := []signin.DirectorySeat{{Tenant: "alpha", Role: "Reader", Directory: acmeDirectory}}
	accepted := announcement{
		Rows:   rowsOf(map[accesstypes.Resource][]resource.RowChange{"Users": {key(userID)}, "MailLinks": {key(invitationID)}}),
		Tables: []accesstypes.Resource{"Identities"},
	}
	seated := announcement{
		Rows:   rowsOf(map[accesstypes.Resource][]resource.RowChange{"Users": {key(userID)}}),
		Tables: []accesstypes.Resource{"Identities"},
	}
	freshStore := func() *fakeStore {
		return &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}, memberships: map[access.MembershipKey]bool{}, roles: map[access.RoleKey]bool{}}
	}

	tests := []struct {
		name string
		// build returns the manager and the hook's run, which answers its committed
		// function.
		build func(t *testing.T) (*announcements, func(ctx context.Context) (func(ctx context.Context), error))
		want  announcement
	}{
		{
			name: "an accepted invitation",
			build: func(t *testing.T) (*announcements, func(ctx context.Context) (func(ctx context.Context), error)) {
				t.Helper()
				m, recorder := announcingManager(t, freshStore(), engineRoles)
				hook := m.InvitationAccepted("Staff")

				return recorder, func(ctx context.Context) (func(ctx context.Context), error) {
					return hook(ctx, nil, userID, "dana", inv)
				}
			},
			want: accepted,
		},
		{
			name: "an accepted invitation that makes a tenant",
			build: func(t *testing.T) (*announcements, func(ctx context.Context) (func(ctx context.Context), error)) {
				t.Helper()
				m := newTenantInviter(t, freshStore(), &fakeMaker{seats: true}, &fakeMail{}, &fakeGrants{}, WithAccountTables(&announceTables))
				recorder := &announcements{}
				m.AnnounceWith(recorder.publish)
				hook := m.InvitationAccepted("Staff")

				return recorder, func(ctx context.Context) (func(ctx context.Context), error) {
					return hook(ctx, new(spanner.ReadWriteTransaction), userID, "dana", tenantInvitation(false, sessioninfo.MethodPassword, ""))
				}
			},
			want: accepted,
		},
		{
			name: "a directory's default role",
			build: func(t *testing.T) (*announcements, func(ctx context.Context) (func(ctx context.Context), error)) {
				t.Helper()
				m, recorder := announcingManager(t, freshStore(), engineRoles)
				hook := m.DirectorySeated("Staff")

				return recorder, func(ctx context.Context) (func(ctx context.Context), error) {
					return hook.Seat(ctx, nil, userID, "dana", seats)
				}
			},
			want: seated,
		},
		{
			name: "a sign-up's default role",
			build: func(t *testing.T) (*announcements, func(ctx context.Context) (func(ctx context.Context), error)) {
				t.Helper()
				m, recorder := announcingManager(t, freshStore(), engineRoles)
				hook := m.SignedUp("Staff")

				return recorder, func(ctx context.Context) (func(ctx context.Context), error) {
					return hook(ctx, nil, userID, "dana", "google", "Steward")
				}
			},
			want: seated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder, hook := tt.build(t)
			// The first run's transaction aborts: its committed function is never called.
			if _, err := hook(t.Context()); err != nil {
				t.Fatalf("the hook's first run error = %v", err)
			}
			committed, err := hook(t.Context())
			if err != nil {
				t.Fatalf("the hook's second run error = %v", err)
			}
			if got := recorder.got(); len(got) != 0 {
				t.Fatalf("announced %+v before the commit, want nothing", got)
			}
			committed(t.Context())
			if diff := cmp.Diff([]announcement{tt.want}, recorder.got(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("announced after the commit (-want +got):\n%s", diff)
			}
		})
	}
}

// leftMembership is a row a test store writes through the transaction as it takes a
// membership away, so what the transaction wrote can be seen to reach the announcement.
type leftMembership struct {
	ID   impulse.UUID `spanner:"Id"`
	Note string       `spanner:"Note"`
}

func (leftMembership) Resource() accesstypes.Resource {
	return "Memberships"
}

// writingStore is the fake store whose DeleteMembership also buffers a row of its own
// through the transaction.
type writingStore struct {
	*fakeStore
	row impulse.UUID
}

func (s *writingStore) DeleteMembership(ctx context.Context, txn resource.ReadWriteTransaction, key *access.MembershipKey) error {
	p := resource.NewPatchSet(resource.NewMetadata[leftMembership]()).SetPatchType(resource.UpdatePatchType)
	p.SetKey("ID", s.row)
	p.Set("Note", "left")
	if err := p.Buffer(ctx, txn); err != nil {
		return err //nolint:wrapcheck // the test's own patch
	}

	return s.fakeStore.DeleteMembership(ctx, txn, key)
}

// TestManager_OwnTenants_Leave_announces pins a person's leaving a tenant from their own
// account page: what its transaction wrote is announced once it commits, and a refused
// leaving announces nothing.
func TestManager_OwnTenants_Leave_announces(t *testing.T) {
	t.Parallel()

	row := mustUUID("77777777-7777-4777-8777-777777777777")
	newStore := func() *writingStore {
		store := leavingStore()
		store.memberships[danaAlphaReader] = true
		store.memberships[danaBetaReader] = true

		return &writingStore{fakeStore: store, row: row}
	}
	build := func(t *testing.T, store Store) (*Manager, *announcements) {
		t.Helper()
		m, err := New(managedAuth, store, &fakeEngine{roles: tenantRoles}, fakeSessions{mail: &fakeMail{}},
			WithTenantWord("sector"), WithAdministratorRoles([]accesstypes.Role{"Warden"}), WithAccountTables(&announceTables))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		recorder := &announcements{}
		m.AnnounceWith(recorder.publish)

		return m, recorder
	}

	t.Run("a leaving that commits announces what its transaction wrote", func(t *testing.T) {
		t.Parallel()

		m, recorder := build(t, newStore())
		client := resource.NewMockClient(stubTxn{}, nil, nil)
		if err := m.OwnTenants(managedAuth, client).Leave(asCaller(t.Context(), dana), userID, "alpha"); err != nil {
			t.Fatalf("Leave() error = %v", err)
		}
		want := []announcement{{Rows: rowsOf(map[accesstypes.Resource][]resource.RowChange{"Memberships": {key(row)}})}}
		if diff := cmp.Diff(want, recorder.got(), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("announced (-want +got):\n%s", diff)
		}
	})

	t.Run("a refused leaving announces nothing", func(t *testing.T) {
		t.Parallel()

		m, recorder := build(t, newStore())
		client := resource.NewMockClient(stubTxn{}, nil, nil)
		if err := m.OwnTenants(managedAuth, client).Leave(asCaller(t.Context(), dana), userID, "gamma"); err == nil {
			t.Fatal("Leave() of a tenant the person holds nothing in error = nil, want a refusal")
		}
		if got := recorder.got(); len(got) != 0 {
			t.Errorf("announced %+v, want nothing", got)
		}
	})
}

// TestManager_AnnounceWith pins the hand-over: without a function nothing is announced
// and the hook answers as it does today; the first function is kept, and nil and a
// later one are ignored; and a table the auth did not name is left out.
func TestManager_AnnounceWith(t *testing.T) {
	t.Parallel()

	signedIn := &session.SignedIn{SessionID: sessionA, UserID: userID, Username: "dana"}

	t.Run("no function handed over: the hook answers as today", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, groupStore(), engineRoles, WithAccountTables(&announceTables))
		if err := m.SignedIn(managedAuth, resource.NewMockClient(stubTxn{}, nil, nil))(t.Context(), signedIn); err != nil {
			t.Errorf("the hook's error = %v, want nil", err)
		}
	})

	t.Run("the first function is kept", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, groupStore(), engineRoles, WithAccountTables(&announceTables))
		first, second := &announcements{}, &announcements{}
		m.AnnounceWith(nil)
		m.AnnounceWith(first.publish)
		m.AnnounceWith(second.publish)
		m.AnnounceWith(nil)
		if err := m.SignedIn(managedAuth, resource.NewMockClient(stubTxn{}, nil, nil))(t.Context(), signedIn); err != nil {
			t.Fatalf("the hook's error = %v", err)
		}
		if got := len(first.got()); got != 1 {
			t.Errorf("the first function was called %d time(s), want once", got)
		}
		if got := second.got(); len(got) != 0 {
			t.Errorf("the second function was called with %+v, want never", got)
		}
	})

	t.Run("a table the auth did not name is left out", func(t *testing.T) {
		t.Parallel()

		m, _ := newManager(t, groupStore(), engineRoles, WithAccountTables(&AccountTables{Sessions: "Sessions", Memberships: "Memberships"}))
		recorder := &announcements{}
		m.AnnounceWith(recorder.publish)
		if err := m.SignedIn(managedAuth, resource.NewMockClient(stubTxn{}, nil, nil))(t.Context(), signedIn); err != nil {
			t.Fatalf("the hook's error = %v", err)
		}
		want := []announcement{{
			Rows:   rowsOf(map[accesstypes.Resource][]resource.RowChange{"Sessions": {key(sessionA)}}),
			Tables: []accesstypes.Resource{"Memberships"},
		}}
		if diff := cmp.Diff(want, recorder.got(), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("announced (-want +got):\n%s", diff)
		}
	})
}
