package access

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
)

// rowStore is the engine's fake store with the row writes of the calls that store
// several rows counted. A test makes the write of one row fail, or holds one open
// before its row is stored.
type rowStore struct {
	*fakeStore
	// rows counts the row writes asked for.
	rows atomic.Int64

	mu      sync.Mutex
	failAt  int64
	err     error
	holdAt  int64
	reached chan struct{}
	release chan struct{}
}

func newRowStore(store *fakeStore) *rowStore {
	return &rowStore{fakeStore: store, reached: make(chan struct{}, 1), release: make(chan struct{})}
}

// refuseRow makes the write of the nth row asked for from now fail with err.
func (s *rowStore) refuseRow(n int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failAt = s.rows.Load() + n
	s.err = err
}

// holdRow holds the write of the nth row asked for from now, before the row is stored:
// the write makes itself known on reached, and goes on once release is called.
func (s *rowStore) holdRow(n int64) (reached <-chan struct{}, release func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holdAt = s.rows.Load() + n

	return s.reached, func() {
		close(s.release)
	}
}

// row counts one row write, and answers what the test made of it: nil for a row that
// is to be stored.
func (s *rowStore) row() error {
	n := s.rows.Add(1)
	s.mu.Lock()
	failAt, err, holdAt := s.failAt, s.err, s.holdAt
	s.mu.Unlock()

	if n == holdAt {
		s.reached <- struct{}{}
		<-s.release
	}
	if n == failAt {
		return err
	}

	return nil
}

func (s *rowStore) InsertUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, period accesstypes.Period) error {
	if err := s.row(); err != nil {
		return err
	}

	return s.fakeStore.InsertUserRole(ctx, scope, user, role, period)
}

func (s *rowStore) DeleteUserRole(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role) error {
	if err := s.row(); err != nil {
		return err
	}

	return s.fakeStore.DeleteUserRole(ctx, scope, user, role)
}

func (s *rowStore) DeleteGrant(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field, condition string) error {
	if err := s.row(); err != nil {
		return err
	}

	return s.fakeStore.DeleteGrant(ctx, scope, role, perm, resource, field, condition)
}

func (s *rowStore) DeleteGrants(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role, perm accesstypes.Permission, resource, field string) error {
	if err := s.row(); err != nil {
		return err
	}

	return s.fakeStore.DeleteGrants(ctx, scope, role, perm, resource, field)
}

func (s *rowStore) InsertGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User, period accesstypes.Period) error {
	if err := s.row(); err != nil {
		return err
	}

	return s.fakeStore.InsertGroupMember(ctx, group, user, period)
}

func (s *rowStore) DeleteGroupMember(ctx context.Context, group accesstypes.Group, user accesstypes.User) error {
	if err := s.row(); err != nil {
		return err
	}

	return s.fakeStore.DeleteGroupMember(ctx, group, user)
}

func (s *rowStore) InsertGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role, period accesstypes.Period) error {
	if err := s.row(); err != nil {
		return err
	}

	return s.fakeStore.InsertGroupRole(ctx, group, scope, role, period)
}

func (s *rowStore) DeleteGroupRole(ctx context.Context, group accesstypes.Group, scope accesstypes.PolicyScope, role accesstypes.Role) error {
	if err := s.row(); err != nil {
		return err
	}

	return s.fakeStore.DeleteGroupRole(ctx, group, scope, role)
}

// people names n people, the first of them person-01.
func people(n int) []accesstypes.User {
	users := make([]accesstypes.User, 0, n)
	for i := range n {
		users = append(users, accesstypes.User(fmt.Sprintf("person-%02d", i+1)))
	}

	return users
}

// readsEmployees reports whether a check on the client's own instance finds that user
// may read employees in the first tenant, which the Editor role grants.
func readsEmployees(t *testing.T, client *Client, user accesstypes.User) bool {
	t.Helper()
	decided, err := client.snapEngine.checkUserResources(t.Context(), accesstypes.NewEnvironment(), user, tenant1Scope, "Read", "employees")
	if err != nil {
		t.Fatalf("checkUserResources() error = %v", err)
	}

	return decided[0].granted
}

// TestUserManager_callAnnouncesOnce pins that a call of the user manager that writes
// policy announces once, however many rows it writes, and that the announcement has
// been handed over when the call returns. A call that writes nothing announces nothing.
func TestUserManager_callAnnouncesOnce(t *testing.T) {
	t.Parallel()

	const crowd = 30
	threeRoles := []accesstypes.Role{"Editor", "Viewer", "Auditor"}
	// arrange prepares the store through the manager and answers the call the test
	// counts.
	type arrange func(ctx context.Context, t *testing.T, manager UserManager) (call func() error)
	withRoles := func(ctx context.Context, t *testing.T, manager UserManager) {
		t.Helper()
		for _, role := range threeRoles[1:] {
			if err := manager.AddRole(ctx, tenant1Policy, role, aDescription); err != nil {
				t.Fatalf("AddRole(%s) error = %v", role, err)
			}
		}
	}
	withGroup := func(ctx context.Context, t *testing.T, manager UserManager) accesstypes.Group {
		t.Helper()
		group, err := manager.AddGroup(ctx, tenant1Policy, "Night shift", aDescription)
		if err != nil {
			t.Fatalf("AddGroup() error = %v", err)
		}

		return group
	}
	tests := []struct {
		name          string
		arrange       arrange
		wantErr       bool
		wantRows      int64
		wantAnnounced int64
	}{
		{
			name: "a role given to thirty people",
			arrange: func(ctx context.Context, _ *testing.T, manager UserManager) func() error {
				return func() error {
					return manager.AddRoleUsers(ctx, tenant1Policy, "Editor", people(crowd)...)
				}
			},
			wantRows: crowd, wantAnnounced: 1,
		},
		{
			name: "a role taken from thirty people",
			arrange: func(ctx context.Context, t *testing.T, manager UserManager) func() error {
				t.Helper()
				if err := manager.AddRoleUsers(ctx, tenant1Policy, "Editor", people(crowd)...); err != nil {
					t.Fatalf("AddRoleUsers() error = %v", err)
				}

				return func() error {
					return manager.DeleteRoleUsers(ctx, tenant1Policy, "Editor", people(crowd)...)
				}
			},
			wantRows: crowd, wantAnnounced: 1,
		},
		{
			name: "three roles given to one person",
			arrange: func(ctx context.Context, t *testing.T, manager UserManager) func() error {
				t.Helper()
				withRoles(ctx, t, manager)

				return func() error {
					return manager.AddUserRoles(ctx, tenant1Policy, "frank", threeRoles...)
				}
			},
			wantRows: 3, wantAnnounced: 1,
		},
		{
			name: "three roles given to one person for a period",
			arrange: func(ctx context.Context, t *testing.T, manager UserManager) func() error {
				t.Helper()
				withRoles(ctx, t, manager)
				period := accesstypes.Period{Start: noon, End: noon.Add(time.Hour)}

				return func() error {
					for _, role := range threeRoles {
						if err := manager.AssignUserRole(ctx, tenant1Policy, "frank", role, period); err != nil {
							return err
						}
					}

					return nil
				}
			},
			// Three calls, one row each: each call announces for itself.
			wantRows: 3, wantAnnounced: 3,
		},
		{
			name: "three roles taken from one person",
			arrange: func(ctx context.Context, t *testing.T, manager UserManager) func() error {
				t.Helper()
				withRoles(ctx, t, manager)
				if err := manager.AddUserRoles(ctx, tenant1Policy, "frank", threeRoles...); err != nil {
					t.Fatalf("AddUserRoles() error = %v", err)
				}

				return func() error {
					return manager.DeleteUserRoles(ctx, tenant1Policy, "frank", threeRoles...)
				}
			},
			wantRows: 3, wantAnnounced: 1,
		},
		{
			name: "every permission taken from a role",
			arrange: func(ctx context.Context, t *testing.T, manager UserManager) func() error {
				t.Helper()
				if err := manager.AddRolePermissionResources(ctx, tenant1Policy, "Editor", "List", "widgets", "gadgets"); err != nil {
					t.Fatalf("AddRolePermissionResources() error = %v", err)
				}
				if err := manager.AddRolePermission(ctx, tenant1Policy, "Editor", "Export"); err != nil {
					t.Fatalf("AddRolePermission() error = %v", err)
				}

				return func() error {
					return manager.DeleteAllRolePermissions(ctx, tenant1Policy, "Editor")
				}
			},
			// Read on employees, List on widgets and on gadgets, and Export on no resource.
			wantRows: 4, wantAnnounced: 1,
		},
		{
			name: "thirty people put in a group",
			arrange: func(ctx context.Context, t *testing.T, manager UserManager) func() error {
				t.Helper()
				group := withGroup(ctx, t, manager)

				return func() error {
					return manager.AddGroupUsers(ctx, group, people(crowd)...)
				}
			},
			wantRows: crowd, wantAnnounced: 1,
		},
		{
			name: "thirty people taken out of a group",
			arrange: func(ctx context.Context, t *testing.T, manager UserManager) func() error {
				t.Helper()
				group := withGroup(ctx, t, manager)
				if err := manager.AddGroupUsers(ctx, group, people(crowd)...); err != nil {
					t.Fatalf("AddGroupUsers() error = %v", err)
				}

				return func() error {
					return manager.DeleteGroupUsers(ctx, group, people(crowd)...)
				}
			},
			wantRows: crowd, wantAnnounced: 1,
		},
		{
			name: "three roles given to a group",
			arrange: func(ctx context.Context, t *testing.T, manager UserManager) func() error {
				t.Helper()
				withRoles(ctx, t, manager)
				group := withGroup(ctx, t, manager)

				return func() error {
					return manager.AddGroupRoles(ctx, group, tenant1Policy, threeRoles...)
				}
			},
			wantRows: 3, wantAnnounced: 1,
		},
		{
			name: "three roles taken from a group",
			arrange: func(ctx context.Context, t *testing.T, manager UserManager) func() error {
				t.Helper()
				withRoles(ctx, t, manager)
				group := withGroup(ctx, t, manager)
				if err := manager.AddGroupRoles(ctx, group, tenant1Policy, threeRoles...); err != nil {
					t.Fatalf("AddGroupRoles() error = %v", err)
				}

				return func() error {
					return manager.DeleteGroupRoles(ctx, group, tenant1Policy, threeRoles...)
				}
			},
			wantRows: 3, wantAnnounced: 1,
		},
		{
			name: "a call that is refused before it writes announces nothing",
			arrange: func(ctx context.Context, _ *testing.T, manager UserManager) func() error {
				return func() error {
					return manager.AddRoleUsers(ctx, tenant1Policy, "No such role", people(crowd)...)
				}
			},
			wantErr: true, wantRows: 0, wantAnnounced: 0,
		},
		{
			name: "a call given no rows announces nothing",
			arrange: func(ctx context.Context, _ *testing.T, manager UserManager) func() error {
				return func() error {
					return manager.DeleteUserRoles(ctx, tenant1Policy, "frank")
				}
			},
			wantRows: 0, wantAnnounced: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newRowStore(engineFakeStore(t))
			signal := newAnnouncedSignal()
			// An announcement that takes a while is still under way, or not begun, when
			// the call returns unless the call waited for it.
			signal.takes = 5 * time.Millisecond
			client, reported := announcingClientOver(t, store, signal)
			call := tt.arrange(t.Context(), t, client.UserManager())
			rowsBefore, begunBefore := store.rows.Load(), signal.begun.Load()

			if err := call(); (err != nil) != tt.wantErr {
				t.Fatalf("the call error = %v, wantErr %v", err, tt.wantErr)
			}
			begun, ended := signal.begun.Load(), signal.ended.Load()
			if got := store.rows.Load() - rowsBefore; got != tt.wantRows {
				t.Errorf("rows the call wrote = %d, want %d", got, tt.wantRows)
			}
			if got := begun - begunBefore; got != tt.wantAnnounced {
				t.Errorf("announcements the call made = %d, want %d", got, tt.wantAnnounced)
			}
			if ended != begun {
				t.Errorf("as the call returned: announcements begun = %d, ended = %d, want every one handed over", begun, ended)
			}
			if got := reported(); len(got) != 0 {
				t.Errorf("errors reported = %v, want none", got)
			}
		})
	}
}

// TestUserManager_callThatFailsPartWay pins a call whose store refuses one of its rows:
// the rows stored before it stand and are seen by this instance's next check, and the
// call announces once for them. A call refused at its first row wrote nothing and
// announces nothing.
func TestUserManager_callThatFailsPartWay(t *testing.T) {
	t.Parallel()

	const crowd = 30
	errRefused := errors.New("the store refused the row")
	tests := []struct {
		name string
		// refused is the row the store refuses, 0 for none.
		refused       int64
		wantAnnounced int64
	}{
		{name: "the first row is refused", refused: 1, wantAnnounced: 0},
		{name: "the second row is refused", refused: 2, wantAnnounced: 1},
		{name: "the eleventh row is refused", refused: 11, wantAnnounced: 1},
		{name: "the last row is refused", refused: crowd, wantAnnounced: 1},
		{name: "no row is refused", refused: 0, wantAnnounced: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newRowStore(engineFakeStore(t))
			signal := newAnnouncedSignal()
			client, reported := announcingClientOver(t, store, signal)
			users := people(crowd)
			// The snapshot is loaded before the call, so what the checks below see they
			// see because the call told the engine.
			if readsEmployees(t, client, users[0]) {
				t.Fatal("the first person reads employees before the call")
			}
			wantStored := int64(crowd)
			if tt.refused != 0 {
				store.refuseRow(tt.refused, errRefused)
				wantStored = tt.refused - 1
			}

			err := client.UserManager().AddRoleUsers(t.Context(), tenant1Policy, "Editor", users...)
			if tt.refused != 0 && !errors.Is(err, errRefused) {
				t.Fatalf("AddRoleUsers() error = %v, want the store's refusal", err)
			}
			if tt.refused == 0 && err != nil {
				t.Fatalf("AddRoleUsers() error = %v", err)
			}
			if begun, ended := signal.begun.Load(), signal.ended.Load(); begun != tt.wantAnnounced || ended != tt.wantAnnounced {
				t.Errorf("as the call returned: announcements begun = %d, ended = %d, want %d and %d", begun, ended, tt.wantAnnounced, tt.wantAnnounced)
			}
			if got := reported(); len(got) != 0 {
				t.Errorf("errors reported = %v, want none: the store's refusal is the caller's", got)
			}
			for i, user := range users {
				if got, want := readsEmployees(t, client, user), int64(i) < wantStored; got != want {
					t.Errorf("%s reads employees = %v after the call, want %v: %d rows were stored", user, got, want, wantStored)
				}
			}
		})
	}
}

// TestUserManager_failedAnnouncement pins a call over many rows whose announcement
// fails or runs out of time: the call succeeds, its rows stand and are seen by this
// instance's next check, and the failure is reported once for the call, after one
// announcement, not once for each row.
func TestUserManager_failedAnnouncement(t *testing.T) {
	t.Parallel()

	const crowd = 30
	errRefused := errors.New("the channel refused the signal")
	tests := []struct {
		name    string
		prepare func(signal *announcedSignal, engine *snapshotEngine)
		wantErr error
		// atLeast is how long the call takes at the least.
		atLeast time.Duration
	}{
		{
			name: "the signal refuses the announcement",
			prepare: func(signal *announcedSignal, _ *snapshotEngine) {
				signal.err = errRefused
			},
			wantErr: errRefused,
		},
		{
			name: "the channel is down and the announcement's time runs out",
			prepare: func(signal *announcedSignal, engine *snapshotEngine) {
				signal.hangs = true
				engine.announceTimeout = 50 * time.Millisecond
			},
			wantErr: context.DeadlineExceeded,
			atLeast: 50 * time.Millisecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newRowStore(engineFakeStore(t))
			signal := newAnnouncedSignal()
			client, reported := announcingClientOver(t, store, signal)
			tt.prepare(signal, client.snapEngine)
			users := people(crowd)
			if readsEmployees(t, client, users[crowd-1]) {
				t.Fatal("the last person reads employees before the call")
			}

			asked := time.Now()
			if err := client.UserManager().AddRoleUsers(t.Context(), tenant1Policy, "Editor", users...); err != nil {
				t.Fatalf("AddRoleUsers() error = %v, want the call to succeed whatever became of its announcement", err)
			}
			if took := time.Since(asked); took < tt.atLeast {
				t.Errorf("the call took %s, want at least the announcement's %s: it waits for its announcement", took, tt.atLeast)
			}

			if begun, ended := signal.begun.Load(), signal.ended.Load(); begun != 1 || ended != 1 {
				t.Errorf("announcements begun = %d, ended = %d, want 1 and 1 for the %d rows: one wait for the call", begun, ended, crowd)
			}
			got := reported()
			if len(got) != 1 || !errors.Is(got[0], tt.wantErr) {
				t.Fatalf("errors reported = %v, want the one failure of the announcement (%v)", got, tt.wantErr)
			}
			if want := "ChangeSignal.Announce()"; !strings.Contains(got[0].Error(), want) {
				t.Errorf("the error reported = %q, want it to name %s", got[0], want)
			}
			if got := store.rows.Load(); got != crowd {
				t.Errorf("rows written = %d, want %d", got, crowd)
			}
			for _, user := range users {
				if !readsEmployees(t, client, user) {
					t.Errorf("%s does not read employees after the call, though only the announcement failed", user)
				}
			}
		})
	}
}

// TestUserManager_rowsAreSeenAsTheyAreStored pins what a held call does while it runs:
// a row it has stored is seen by this instance's checks at once, before the call has
// ended, and nothing is announced until the call's last write is made.
func TestUserManager_rowsAreSeenAsTheyAreStored(t *testing.T) {
	t.Parallel()

	store := newRowStore(engineFakeStore(t))
	signal := newAnnouncedSignal()
	client, _ := announcingClientOver(t, store, signal)
	users := people(3)
	if readsEmployees(t, client, users[0]) {
		t.Fatal("the first person reads employees before the call")
	}
	reached, release := store.holdRow(2)

	called := make(chan error, 1)
	go func() {
		called <- client.UserManager().AddRoleUsers(t.Context(), tenant1Policy, "Editor", users...)
	}()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the call never came to its second row")
	}

	if !readsEmployees(t, client, users[0]) {
		t.Error("the first row is stored and a check on this instance does not see it: the call's writes wait for its end")
	}
	if readsEmployees(t, client, users[1]) {
		t.Error("the second person reads employees before their row is stored")
	}
	if got := signal.begun.Load(); got != 0 {
		t.Errorf("announcements begun before the call's last write = %d, want 0", got)
	}

	release()
	select {
	case err := <-called:
		if err != nil {
			t.Fatalf("AddRoleUsers() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the call did not return")
	}
	if begun, ended := signal.begun.Load(), signal.ended.Load(); begun != 1 || ended != 1 {
		t.Errorf("as the call returned: announcements begun = %d, ended = %d, want 1 and 1", begun, ended)
	}
	if !readsEmployees(t, client, users[2]) {
		t.Error("the last person does not read employees after the call")
	}
}

// Test_userManager_holdAnnouncement pins the hold itself: the manager a call goes on
// with is its own and the receiver is left as it was; a call made inside a held one
// holds nothing and announces nothing, so the outer call announces for both; calls made
// together each announce for themselves; and a manager that is not wired to the engine
// is returned as it is.
func Test_userManager_holdAnnouncement(t *testing.T) {
	t.Parallel()

	t.Run("a call inside a held call leaves the announcement to the outer one", func(t *testing.T) {
		t.Parallel()

		signal := newAnnouncedSignal()
		client, _ := announcingClient(t, signal)
		manager := client.userManager
		stored := manager.store

		held, announce := manager.holdAnnouncement()
		if held == manager {
			t.Fatal("holdAnnouncement() returned its receiver, want a manager of the call's own")
		}
		if manager.store != stored {
			t.Error("holdAnnouncement() changed its receiver's store")
		}
		inner, announceInner := held.holdAnnouncement()
		if inner != held {
			t.Error("holdAnnouncement() inside a held call returned a manager of its own, want the outer call's")
		}
		for _, user := range people(2) {
			if err := inner.store.addUserRole(t.Context(), tenant1Policy, user, "Editor", accesstypes.Period{}); err != nil {
				t.Fatalf("addUserRole() error = %v", err)
			}
		}

		announceInner()
		if got := signal.begun.Load(); got != 0 {
			t.Errorf("announcements made as the inner call ended = %d, want 0: the outer call announces", got)
		}
		announce()
		if got := signal.begun.Load(); got != 1 {
			t.Errorf("announcements made as the outer call ended = %d, want 1", got)
		}
		// A write that is not held announces for itself, as before the call.
		if err := manager.store.addUserRole(t.Context(), tenant1Policy, "frank", "Editor", accesstypes.Period{}); err != nil {
			t.Fatalf("addUserRole() error = %v", err)
		}
		if got := signal.begun.Load(); got != 2 {
			t.Errorf("announcements after a write outside any call = %d, want 2", got)
		}
	})

	t.Run("calls made together each announce for themselves", func(t *testing.T) {
		t.Parallel()

		const callers = 8
		signal := newAnnouncedSignal()
		client, _ := announcingClient(t, signal)

		failed := make(chan error, callers)
		var wg sync.WaitGroup
		for i := range callers {
			wg.Go(func() {
				users := make([]accesstypes.User, 0, 5)
				for j := range 5 {
					users = append(users, accesstypes.User(fmt.Sprintf("caller-%d-person-%d", i, j)))
				}
				if err := client.UserManager().AddRoleUsers(t.Context(), tenant1Policy, "Editor", users...); err != nil {
					failed <- err
				}
			})
		}
		wg.Wait()
		close(failed)
		for err := range failed {
			t.Errorf("AddRoleUsers() error = %v", err)
		}
		if begun, ended := signal.begun.Load(), signal.ended.Load(); begun != callers || ended != callers {
			t.Errorf("as %d calls made together returned: announcements begun = %d, ended = %d, want %d and %d", callers, begun, ended, callers, callers)
		}
	})

	t.Run("a manager whose store reports its writes to nothing is returned as it is", func(t *testing.T) {
		t.Parallel()

		store := engineFakeStore(t)
		manager := newUserManager(newStoreManager(store), nil, testEngine(t, store, nil), nil)
		held, announce := manager.holdAnnouncement()
		if held != manager {
			t.Error("holdAnnouncement() returned a manager of its own where nothing is announced, want its receiver")
		}
		announce()
	})
}

// TestUserManager_everyWritingCallHoldsItsAnnouncement reads the package's own source
// and pins that every public call of the user manager that can reach a store write
// begins by holding its announcement, with the two lines holdAnnouncement's
// documentation gives, and that no other public call does. A call that wrote through
// the manager without them would announce once for each row.
//
// What reaches a write is read from the code: a method of the store manager that
// reports a policy change, or calls one that does, writes; a method of the user manager
// that calls one of those on its store, or calls a method of its own that does, writes.
func TestUserManager_everyWritingCallHoldsItsAnnouncement(t *testing.T) {
	t.Parallel()

	stores := declaredMethods(t, "storeManager")
	storeWrites := reaching(stores, func(fn *ast.FuncDecl) bool {
		return slices.Contains(selectedOn(fn, ""), "notifyPolicyChange")
	})
	for _, want := range []string{"addUserRole", "deleteRole", "addGroupUser"} {
		if !storeWrites[want] {
			t.Fatalf("the store manager's %s was not read as a write: the test no longer reads the code as it is written", want)
		}
	}
	if storeWrites["userRoles"] {
		t.Fatal("the store manager's userRoles was read as a write")
	}

	managers := declaredMethods(t, "userManager")
	writes := reaching(managers, func(fn *ast.FuncDecl) bool {
		return slices.ContainsFunc(selectedOn(fn, "store"), func(name string) bool {
			return storeWrites[name]
		})
	})

	var held int
	for name, fn := range managers {
		if !ast.IsExported(name) {
			continue
		}
		holds := holdsAnnouncement(t, fn)
		switch {
		case writes[name] && !holds:
			t.Errorf("%s writes policy and does not hold its announcement: begin it, before anything is asked of the manager, with\n\tu, announce := u.holdAnnouncement()\n\tdefer announce()", name)
		case !writes[name] && holds:
			t.Errorf("%s holds an announcement and writes no policy", name)
		case holds:
			held++
		}
	}
	if held == 0 {
		t.Error("no public call of the user manager holds its announcement")
	}
}

// declaredMethods parses the package's own files, its tests apart, and returns the
// methods declared on the type named, by name.
func declaredMethods(t *testing.T, receiver string) map[string]*ast.FuncDecl {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("os.ReadDir() error = %v", err)
	}
	methods := make(map[string]*ast.FuncDecl)
	files := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(files, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parser.ParseFile(%s) error = %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || receiverType(fn) != receiver {
				continue
			}
			methods[fn.Name.Name] = fn
		}
	}
	if len(methods) == 0 {
		t.Fatalf("no method of %s found in the package's files", receiver)
	}

	return methods
}

// receiverType names the type a method is declared on, with or without a pointer, and
// is empty for a function.
func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	ident, ok := typ.(*ast.Ident)
	if !ok {
		return ""
	}

	return ident.Name
}

// receiverName is the name a method gives its receiver, empty where it gives none.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 || len(fn.Recv.List[0].Names) != 1 {
		return ""
	}

	return fn.Recv.List[0].Names[0].Name
}

// selectedOn lists the names a method selects on its receiver (u.name), or, with a
// field named, on that field of its receiver (u.field.name).
func selectedOn(fn *ast.FuncDecl, field string) []string {
	receiver := receiverName(fn)
	var names []string
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		on := selector.X
		if field != "" {
			inner, ok := on.(*ast.SelectorExpr)
			if !ok || inner.Sel.Name != field {
				return true
			}
			on = inner.X
		}
		if ident, ok := on.(*ast.Ident); ok && ident.Name == receiver {
			names = append(names, selector.Sel.Name)
		}

		return true
	})

	return names
}

// reaching returns the methods that are a write themselves, as direct reports, or call
// a method of the same type that is one, however far down.
func reaching(methods map[string]*ast.FuncDecl, direct func(fn *ast.FuncDecl) bool) map[string]bool {
	reaches := make(map[string]bool)
	for name, fn := range methods {
		if direct(fn) {
			reaches[name] = true
		}
	}
	for grew := true; grew; {
		grew = false
		for name, fn := range methods {
			if reaches[name] {
				continue
			}
			if slices.ContainsFunc(selectedOn(fn, ""), func(called string) bool {
				return reaches[called]
			}) {
				reaches[name] = true
				grew = true
			}
		}
	}

	return reaches
}

// holdsAnnouncement reports whether the method holds its announcement as
// holdAnnouncement's documentation gives it: the two lines, one after the other, with
// nothing asked of the receiver before them.
func holdsAnnouncement(t *testing.T, fn *ast.FuncDecl) bool {
	t.Helper()

	receiver := receiverName(fn)
	hold := receiver + ", announce := " + receiver + ".holdAnnouncement()"
	for i, statement := range fn.Body.List {
		if source(t, statement) == hold {
			return i+1 < len(fn.Body.List) && source(t, fn.Body.List[i+1]) == "defer announce()"
		}
		if strings.Contains(source(t, statement), receiver+".") {
			return false
		}
	}

	return false
}

// source is a statement as gofmt writes it.
func source(t *testing.T, node ast.Node) string {
	t.Helper()

	var written bytes.Buffer
	if err := format.Node(&written, token.NewFileSet(), node); err != nil {
		t.Fatalf("format.Node() error = %v", err)
	}

	return written.String()
}
