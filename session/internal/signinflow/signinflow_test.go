package signinflow

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// These cases hold the flow to what it promises the two hooks that give roles at a
// sign-in (InvitationAcceptor, DirectorySeater), over a store kept in memory whose
// transactions buffer their writes and apply them at the commit, as Spanner's do, and
// whose commits can be made to abort and run the closure again, or to fail: a role is
// written in the transaction of the step that causes it, a hook that fails leaves
// nothing behind, and a hook's committed function runs once, for the run that
// committed, after the commit and before the policy. The drivers' suite
// (sessionstorage/internal/drivertest) holds the same on Spanner and PostgreSQL.

// commitOutcome is what the store does with the next commit.
type commitOutcome int

const (
	// commits applies the transaction's writes.
	commits commitOutcome = iota
	// aborts drops the writes and runs the closure again, as the Spanner client does
	// with a transaction the database aborted.
	aborts
	// conflicts drops the writes and ends the transaction with errIdentityConflict,
	// which the flow answers by running its resolution once more.
	conflicts
	// fails drops the writes and ends the transaction with errCommitFailed.
	fails
)

var (
	errIdentityConflict = errors.New("the identities key is taken")
	errCommitFailed     = errors.New("the commit failed")
)

// memUser is a user row of the store.
type memUser struct {
	username string
	email    string
}

// memStore is the committed state, the steps taken against it, and what the next
// commits do (commits when the list is empty).
type memStore struct {
	users       map[impulse.UUID]*memUser
	links       map[string]impulse.UUID
	invitations map[impulse.UUID]bool
	granted     map[string][]string
	sessions    int
	steps       []string
	next        []commitOutcome
}

func newMemStore() *memStore {
	return &memStore{users: map[impulse.UUID]*memUser{}, links: map[string]impulse.UUID{}, invitations: map[impulse.UUID]bool{}, granted: map[string][]string{}}
}

// notef records a step.
func (s *memStore) notef(format string, a ...any) {
	s.steps = append(s.steps, fmt.Sprintf(format, a...))
}

// invite stores a live invitation and answers it as a sign-in carries it.
func (s *memStore) invite(email, tenant string, roles ...string) *sessioninfo.Invitation {
	id := impulse.Must(impulse.NewUUID())
	s.invitations[id] = true

	return &sessioninfo.Invitation{ID: id, Email: email, Tenant: tenant, RoleNames: roles}
}

// userNamed finds a committed user by username.
func (s *memStore) userNamed(username string) (impulse.UUID, *memUser) {
	for id, user := range s.users {
		if user.username == username {
			return id, user
		}
	}

	return impulse.NilUUID, nil
}

// memTx is one run of a transaction's closure: the writes it buffered, applied at the
// commit, and the invitations its consumption has taken, which a later statement of
// the same run sees.
type memTx struct {
	run      int
	writes   []func()
	consumed map[impulse.UUID]bool
}

// buffer notes a write for the commit.
func (tx *memTx) buffer(write func()) {
	tx.writes = append(tx.writes, write)
}

// memDriver is the flow's driver over the store.
type memDriver struct {
	store *memStore
	runs  int
}

var _ Driver[*memTx] = (*memDriver)(nil)

func linkKey(method sessioninfo.AuthMethod, connection, subject string) string {
	return string(method) + "|" + connection + "|" + subject
}

func (d *memDriver) Transact(ctx context.Context, fn func(ctx context.Context, tx *memTx) error) error {
	for {
		d.runs++
		tx := &memTx{run: d.runs, consumed: map[impulse.UUID]bool{}}
		if err := fn(ctx, tx); err != nil {
			d.store.notef("rolled back")

			return err
		}
		outcome := commits
		if len(d.store.next) > 0 {
			outcome, d.store.next = d.store.next[0], d.store.next[1:]
		}
		switch outcome {
		case aborts:
			d.store.notef("aborted")

			continue
		case conflicts:
			d.store.notef("conflict")

			return errIdentityConflict
		case fails:
			d.store.notef("commit failed")

			return errCommitFailed
		case commits:
		}
		for _, write := range tx.writes {
			write()
		}
		d.store.notef("commit")

		return nil
	}
}

func (d *memDriver) IdentityIn(_ context.Context, _ *memTx, method sessioninfo.AuthMethod, connection, subject string) (*dbtype.SessionIdentity, error) {
	userID, ok := d.store.links[linkKey(method, connection, subject)]
	if !ok {
		return nil, nil
	}

	return &dbtype.SessionIdentity{ID: impulse.Must(impulse.NewUUID()), UserID: userID, Method: method, Connection: connection, Subject: subject}, nil
}

func (d *memDriver) TouchIdentityIn(context.Context, *memTx, impulse.UUID) error {
	return nil
}

func (d *memDriver) LinkIdentityIn(_ context.Context, tx *memTx, userID impulse.UUID, identity *sessioninfo.Identity, _ string) error {
	tx.buffer(func() { d.store.links[linkKey(identity.Method, identity.Connection, identity.Subject)] = userID })

	return nil
}

func (d *memDriver) IdentitiesByUserIn(context.Context, *memTx, impulse.UUID) ([]*dbtype.SessionIdentity, error) {
	return nil, nil
}

// UserIn reads a committed user: a user the same run buffered is not there yet.
func (d *memDriver) UserIn(_ context.Context, _ *memTx, userID impulse.UUID) (*dbtype.User, error) {
	user, ok := d.store.users[userID]
	if !ok {
		return nil, httpio.NewNotFoundMessagef("user %s not found", userID)
	}

	return &dbtype.User{ID: userID, Username: user.username}, nil
}

func (d *memDriver) LockUserIn(ctx context.Context, tx *memTx, userID impulse.UUID) (*dbtype.User, error) {
	return d.UserIn(ctx, tx, userID)
}

func (d *memDriver) UserByUsernameIn(_ context.Context, _ *memTx, username string) (*dbtype.User, error) {
	id, user := d.store.userNamed(username)
	if user == nil {
		return nil, nil
	}

	return &dbtype.User{ID: id, Username: user.username}, nil
}

func (d *memDriver) UserByVerifiedEmailIn(context.Context, *memTx, string) (*dbtype.User, error) {
	return nil, nil
}

func (d *memDriver) InsertUserIn(_ context.Context, tx *memTx, user *dbtype.InsertSessionUser) (impulse.UUID, error) {
	id := impulse.Must(impulse.NewUUID())
	row := &memUser{username: user.Username}
	if user.Email != nil {
		row.email = *user.Email
	}
	tx.buffer(func() { d.store.users[id] = row })

	return id, nil
}

func (d *memDriver) InsertSessionIn(_ context.Context, tx *memTx, _ impulse.UUID, _ *dbtype.InsertSession, _ *sessioninfo.NewSessionRequest) error {
	tx.buffer(func() { d.store.sessions++ })

	return nil
}

func (d *memDriver) SignInConflict(err error) dbtype.SignInConflict {
	if errors.Is(err, errIdentityConflict) {
		return dbtype.IdentityConflict
	}

	return dbtype.NoSignInConflict
}

func (d *memDriver) ConsumeMailLinkIn(_ context.Context, tx *memTx, id impulse.UUID, _ time.Time) error {
	if !d.store.invitations[id] || tx.consumed[id] {
		return httpio.NewNotFoundMessage("no live mail link")
	}
	tx.consumed[id] = true
	tx.buffer(func() { d.store.invitations[id] = false })

	return nil
}

func (d *memDriver) ClaimVerifiedEmailIn(_ context.Context, tx *memTx, userID impulse.UUID, email string) (bool, error) {
	tx.buffer(func() { d.store.users[userID].email = email })

	return true, nil
}

func (d *memDriver) UserByEmailIn(context.Context, *memTx, string) (*dbtype.User, error) {
	return nil, nil
}

func (d *memDriver) UsernameIsEmail() bool {
	return false
}

// roleHooks are the two hooks as an application writes them: each buffers what it
// gives in the transaction it is handed (the store's granted rows, under the username)
// and answers a committed function that notes its run. fail, when set, is what the
// hook answers after its write, beside the committed function, which the flow must
// then never run.
type roleHooks struct {
	store      *memStore
	acceptFail error
	seatFail   error
	// committed counts the committed functions' runs.
	committed int
}

func (h *roleHooks) accept(_ context.Context, tx *memTx, _ impulse.UUID, username string, inv *sessioninfo.Invitation) (func(ctx context.Context), error) {
	h.store.notef("accept in run %d", tx.run)
	tx.buffer(func() { h.store.granted[username] = append(h.store.granted[username], inv.RoleNames...) })

	return func(context.Context) {
		h.committed++
		h.store.notef("accept of run %d committed: user=%t invitation live=%t", tx.run, h.userCommitted(username), h.store.invitations[inv.ID])
	}, h.acceptFail
}

func (h *roleHooks) seat(_ context.Context, tx *memTx, _ impulse.UUID, username string, seats []signin.DirectorySeat) (func(ctx context.Context), error) {
	h.store.notef("seat in run %d", tx.run)
	tx.buffer(func() {
		for _, seat := range seats {
			h.store.granted[username] = append(h.store.granted[username], string(seat.Tenant)+"/"+string(seat.Role))
		}
	})

	return func(context.Context) {
		h.committed++
		h.store.notef("seat of run %d committed: user=%t", tx.run, h.userCommitted(username))
	}, h.seatFail
}

func (h *roleHooks) userCommitted(username string) bool {
	_, user := h.store.userNamed(username)

	return user != nil
}

// policy is a sign-in policy that notes what it finds granted, and allows.
func (h *roleHooks) policy(_ context.Context, _ *memTx, req *sessioninfo.NewSessionRequest) (*signin.Decision, error) {
	h.store.notef("policy: %s holds %v after %d committed", req.Username, h.store.granted[req.Username], h.committed)

	return signin.Allow(), nil
}

const stranger = "pat@lakeside.edu"

// strangerRequest is a first sign-in through a directory by a person no user was
// created for, carrying inv (nil for none).
func strangerRequest(inv *sessioninfo.Invitation) *sessioninfo.NewSessionRequest {
	return &sessioninfo.NewSessionRequest{
		Reason:     sessioninfo.ReasonLogin,
		Identity:   &sessioninfo.Identity{Method: sessioninfo.MethodAzure, Connection: "tenant-1", Subject: "oid-pat", Email: stranger, SignInName: stranger},
		Invitation: inv,
	}
}

// declaredWithSeat is a directory reader: Lakeside declares the directory with Member
// as its default seat.
func declaredWithSeat(context.Context, *memTx, signin.Directory) ([]signin.DeclaredDirectory, error) {
	return []signin.DeclaredDirectory{{Tenant: "lakeside", DefaultSeat: "Member"}}, nil
}

func signInWith(t *testing.T, d *memDriver, req *sessioninfo.NewSessionRequest, rules Rules[*memTx]) error {
	t.Helper()

	return SignIn(t.Context(), d, impulse.Must(impulse.NewUUID()), &dbtype.InsertSession{}, req, rules)
}

// assertNothingCommitted fails unless the store holds no user, no link, no grant and no
// session, and inv is still live and not accepted.
func assertNothingCommitted(t *testing.T, store *memStore, hooks *roleHooks, inv *sessioninfo.Invitation) {
	t.Helper()

	if len(store.users) != 0 || len(store.links) != 0 || len(store.granted) != 0 || store.sessions != 0 {
		t.Errorf("the store holds %d user(s), %d link(s), grants %v and %d session(s), want nothing committed", len(store.users), len(store.links), store.granted, store.sessions)
	}
	if inv != nil {
		if !store.invitations[inv.ID] {
			t.Error("the invitation was consumed, want it standing")
		}
		if inv.Accepted {
			t.Error("Invitation.Accepted = true, want false")
		}
	}
	if hooks.committed != 0 {
		t.Errorf("a committed function ran %d time(s) for a transaction that did not commit, want 0", hooks.committed)
	}
}

func TestSignIn_aHookThatFailsLeavesNothing(t *testing.T) {
	t.Parallel()

	// The acceptor's error is a NotFound, as a role that no longer exists is: the flow
	// must not take it for the invitation's own, a link that died.
	roleGone := httpio.NewNotFoundMessage("the role the invitation names no longer exists")
	seatRefused := errors.New("the default seat could not be given")

	tests := []struct {
		name       string
		acceptFail error
		seatFail   error
		// seated declares the directory with a default seat; invited carries an invitation.
		seated    bool
		invited   bool
		noSeater  bool
		wantErr   error
		wantSteps []string
	}{
		{
			name:       "the acceptor fails for a stranger the invitation alone admits: the invitation stands and no user is provisioned",
			acceptFail: roleGone, invited: true, wantErr: roleGone,
			wantSteps: []string{"accept in run 1", "rolled back"},
		},
		{
			name:     "the seater fails for a stranger the default seat admits: no user is provisioned",
			seatFail: seatRefused, seated: true, wantErr: seatRefused,
			wantSteps: []string{"seat in run 1", "rolled back"},
		},
		{
			name:       "the seater works and the acceptor then fails: the seat it buffered goes with the rest",
			acceptFail: roleGone, seated: true, invited: true, wantErr: roleGone,
			wantSteps: []string{"seat in run 1", "accept in run 1", "rolled back"},
		},
		{
			name:   "default seats on rules with no seater fail the transaction: no user is provisioned",
			seated: true, noSeater: true,
			wantSteps: []string{"rolled back"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			d := &memDriver{store: store}
			hooks := &roleHooks{store: store, acceptFail: tt.acceptFail, seatFail: tt.seatFail}
			rules := Rules[*memTx]{Standard: signin.InvitedUsersOnly, Policy: hooks.policy, Accept: hooks.accept, Seat: hooks.seat}
			if tt.seated {
				rules.Directories = declaredWithSeat
			}
			if tt.noSeater {
				rules.Seat = nil
			}
			var inv *sessioninfo.Invitation
			if tt.invited {
				inv = store.invite(stranger, "lakeside", "Teacher")
			}
			req := strangerRequest(inv)

			err := signInWith(t, d, req, rules)

			if err == nil {
				t.Fatal("SignIn() error = nil, want the hook's failure")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("SignIn() error = %v, want %v", err, tt.wantErr)
			}
			var refusal *sessioninfo.LoginRefusal
			if errors.As(err, &refusal) {
				t.Errorf("SignIn() error = %v, want a failure, not a refusal", err)
			}
			assertNothingCommitted(t, store, hooks, inv)
			if diff := cmp.Diff(tt.wantSteps, store.steps); diff != "" {
				t.Errorf("steps (-want +got):\n%s", diff)
			}
			if req.User != nil {
				t.Errorf("req.User = %+v, want none: no user was resolved", req.User)
			}
		})
	}
}

func TestSignIn_aCommittedFunctionRunsOnceAfterTheCommitBeforeThePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// next is what the store does with the resolution's commits.
		next      []commitOutcome
		wantSteps []string
	}{
		{
			name: "one run: the seat, then the invitation's roles, in the transaction; their committed functions in that order once it has committed; then the policy",
			wantSteps: []string{
				"seat in run 1", "accept in run 1", "commit",
				"seat of run 1 committed: user=true", "accept of run 1 committed: user=true invitation live=false",
				"policy: pat@lakeside.edu holds [lakeside/Member Teacher] after 2 committed", "commit",
			},
		},
		{
			name: "the database aborts the first run and the client runs the closure again: only the second run's committed functions run, once",
			next: []commitOutcome{aborts},
			wantSteps: []string{
				"seat in run 1", "accept in run 1", "aborted",
				"seat in run 2", "accept in run 2", "commit",
				"seat of run 2 committed: user=true", "accept of run 2 committed: user=true invitation live=false",
				"policy: pat@lakeside.edu holds [lakeside/Member Teacher] after 2 committed", "commit",
			},
		},
		{
			name: "the first resolution loses a race on the identities key and the flow resolves once more: only the second resolution's committed functions run",
			next: []commitOutcome{conflicts},
			wantSteps: []string{
				"seat in run 1", "accept in run 1", "conflict",
				"seat in run 2", "accept in run 2", "commit",
				"seat of run 2 committed: user=true", "accept of run 2 committed: user=true invitation live=false",
				"policy: pat@lakeside.edu holds [lakeside/Member Teacher] after 2 committed", "commit",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			store.next = tt.next
			d := &memDriver{store: store}
			hooks := &roleHooks{store: store}
			rules := Rules[*memTx]{Standard: signin.InvitedUsersOnly, Policy: hooks.policy, Accept: hooks.accept, Seat: hooks.seat, Directories: declaredWithSeat}
			inv := store.invite(stranger, "lakeside", "Teacher")
			req := strangerRequest(inv)

			if err := signInWith(t, d, req, rules); err != nil {
				t.Fatalf("SignIn() error = %v", err)
			}

			if diff := cmp.Diff(tt.wantSteps, store.steps); diff != "" {
				t.Errorf("steps (-want +got):\n%s", diff)
			}
			if hooks.committed != 2 {
				t.Errorf("the committed functions ran %d time(s), want 2: the seater's and the acceptor's, once each", hooks.committed)
			}
			if !inv.Accepted || store.invitations[inv.ID] {
				t.Errorf("Invitation.Accepted = %t, live = %t; want accepted and consumed", inv.Accepted, store.invitations[inv.ID])
			}
			if diff := cmp.Diff(map[string][]string{stranger: {"lakeside/Member", "Teacher"}}, store.granted); diff != "" {
				t.Errorf("granted (-want +got):\n%s", diff)
			}
			if len(store.users) != 1 || store.sessions != 1 {
				t.Errorf("the store holds %d user(s) and %d session(s), want one of each", len(store.users), store.sessions)
			}
		})
	}
}

// TestSignIn_aRoleTheInvitationNamesIsGivenOnce pins what the flow does when a sign-in
// would give one role in one tenant twice: the directory's default seat and the
// invitation the sign-in consumes name it both. The two are written in one transaction
// by two hooks, neither of which reads what the other buffered, and a database refuses a
// row written twice, so the flow leaves the role to the invitation: the seat hook is
// handed only the seats the invitation does not name, and is not called for none.
func TestSignIn_aRoleTheInvitationNamesIsGivenOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// tenant and roles are the invitation's.
		tenant      string
		roles       []string
		wantSteps   []string
		wantGranted []string
		wantRuns    int
	}{
		{
			name:   "the invitation names the directory's default seat: the invitation alone gives it",
			tenant: "lakeside", roles: []string{"Member"},
			wantSteps: []string{
				"accept in run 1", "commit",
				"accept of run 1 committed: user=true invitation live=false",
				"policy: pat@lakeside.edu holds [Member] after 1 committed", "commit",
			},
			wantGranted: []string{"Member"},
			wantRuns:    1,
		},
		{
			name:   "the invitation names the seat among other roles: each is given once",
			tenant: "lakeside", roles: []string{"Teacher", "Member"},
			wantSteps: []string{
				"accept in run 1", "commit",
				"accept of run 1 committed: user=true invitation live=false",
				"policy: pat@lakeside.edu holds [Teacher Member] after 1 committed", "commit",
			},
			wantGranted: []string{"Teacher", "Member"},
			wantRuns:    1,
		},
		{
			name:   "the same role in another tenant is another membership: both are given",
			tenant: "hillside", roles: []string{"Member"},
			wantSteps: []string{
				"seat in run 1", "accept in run 1", "commit",
				"seat of run 1 committed: user=true", "accept of run 1 committed: user=true invitation live=false",
				"policy: pat@lakeside.edu holds [lakeside/Member Member] after 2 committed", "commit",
			},
			wantGranted: []string{"lakeside/Member", "Member"},
			wantRuns:    2,
		},
		{
			name:  "an invitation into the application names no tenant and shares no membership with a seat",
			roles: []string{"Member"},
			wantSteps: []string{
				"seat in run 1", "accept in run 1", "commit",
				"seat of run 1 committed: user=true", "accept of run 1 committed: user=true invitation live=false",
				"policy: pat@lakeside.edu holds [lakeside/Member Member] after 2 committed", "commit",
			},
			wantGranted: []string{"lakeside/Member", "Member"},
			wantRuns:    2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			d := &memDriver{store: store}
			hooks := &roleHooks{store: store}
			rules := Rules[*memTx]{Standard: signin.InvitedUsersOnly, Policy: hooks.policy, Accept: hooks.accept, Seat: hooks.seat, Directories: declaredWithSeat}
			inv := store.invite(stranger, tt.tenant, tt.roles...)

			if err := signInWith(t, d, strangerRequest(inv), rules); err != nil {
				t.Fatalf("SignIn() error = %v", err)
			}

			if diff := cmp.Diff(tt.wantSteps, store.steps); diff != "" {
				t.Errorf("steps (-want +got):\n%s", diff)
			}
			if hooks.committed != tt.wantRuns {
				t.Errorf("the committed functions ran %d time(s), want %d", hooks.committed, tt.wantRuns)
			}
			if diff := cmp.Diff(map[string][]string{stranger: tt.wantGranted}, store.granted); diff != "" {
				t.Errorf("granted (-want +got):\n%s", diff)
			}
			if !inv.Accepted || store.invitations[inv.ID] {
				t.Errorf("Invitation.Accepted = %t, live = %t; want accepted and consumed", inv.Accepted, store.invitations[inv.ID])
			}
		})
	}
}

func TestSeatsBeyondInvitation(t *testing.T) {
	t.Parallel()

	seats := []signin.DirectorySeat{{Tenant: "lakeside", Role: "Member"}, {Tenant: "hillside", Role: "Member"}, {Tenant: "lakeside", Role: "Aide"}}
	tests := []struct {
		name string
		inv  *sessioninfo.Invitation
		want []signin.DirectorySeat
	}{
		{name: "no invitation", want: seats},
		{name: "an invitation into the application", inv: &sessioninfo.Invitation{RoleNames: []string{"Member"}}, want: seats},
		{name: "another role of the tenant", inv: &sessioninfo.Invitation{Tenant: "lakeside", RoleNames: []string{"Teacher"}}, want: seats},
		{
			name: "the seat's role in the seat's tenant",
			inv:  &sessioninfo.Invitation{Tenant: "lakeside", RoleNames: []string{"Teacher", "Member"}},
			want: []signin.DirectorySeat{{Tenant: "hillside", Role: "Member"}, {Tenant: "lakeside", Role: "Aide"}},
		},
		{
			name: "every seat of the tenant",
			inv:  &sessioninfo.Invitation{Tenant: "lakeside", RoleNames: []string{"Aide", "Member"}},
			want: []signin.DirectorySeat{{Tenant: "hillside", Role: "Member"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			given := slices.Clone(seats)
			if diff := cmp.Diff(tt.want, seatsBeyondInvitation(given, tt.inv)); diff != "" {
				t.Errorf("seatsBeyondInvitation() (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(seats, given); diff != "" {
				t.Errorf("the seats handed in were changed (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSignIn_aCommitThatFailsRunsNoCommittedFunction(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.next = []commitOutcome{fails}
	d := &memDriver{store: store}
	hooks := &roleHooks{store: store}
	rules := Rules[*memTx]{Standard: signin.InvitedUsersOnly, Policy: hooks.policy, Accept: hooks.accept, Seat: hooks.seat, Directories: declaredWithSeat}
	inv := store.invite(stranger, "lakeside", "Teacher")

	err := signInWith(t, d, strangerRequest(inv), rules)

	if !errors.Is(err, errCommitFailed) {
		t.Fatalf("SignIn() error = %v, want the commit's failure", err)
	}
	assertNothingCommitted(t, store, hooks, inv)
	if diff := cmp.Diff([]string{"seat in run 1", "accept in run 1", "commit failed"}, store.steps); diff != "" {
		t.Errorf("steps (-want +got):\n%s", diff)
	}
}

func TestAcceptInvitation_inOneTransaction(t *testing.T) {
	t.Parallel()

	roleGone := httpio.NewNotFoundMessage("the role the invitation names no longer exists")
	newUser := func(store *memStore) impulse.UUID {
		id := impulse.Must(impulse.NewUUID())
		store.users[id] = &memUser{username: "sam"}

		return id
	}
	passwordRequest := func(id impulse.UUID, inv *sessioninfo.Invitation) *sessioninfo.NewSessionRequest {
		return &sessioninfo.NewSessionRequest{
			Reason: sessioninfo.ReasonLogin, Username: "sam", UserID: id,
			Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: id.String()}, Invitation: inv,
		}
	}

	t.Run("by hand: the acceptor's roles commit with the consumption and the address, and its committed function runs once after the commit", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		store.next = []commitOutcome{aborts}
		d := &memDriver{store: store}
		hooks := &roleHooks{store: store}
		id := newUser(store)
		inv := store.invite("sam@lakeside.edu", "", "Admin")

		if err := AcceptInvitation(t.Context(), d, id, "sam", "", inv, hooks.accept); err != nil {
			t.Fatalf("AcceptInvitation() error = %v", err)
		}

		want := []string{"accept in run 1", "aborted", "accept in run 2", "commit", "accept of run 2 committed: user=true invitation live=false"}
		if diff := cmp.Diff(want, store.steps); diff != "" {
			t.Errorf("steps (-want +got):\n%s", diff)
		}
		if !inv.Accepted || store.users[id].email != "sam@lakeside.edu" || hooks.committed != 1 {
			t.Errorf("Accepted = %t, address = %q, committed runs = %d; want accepted, the invited address and one run", inv.Accepted, store.users[id].email, hooks.committed)
		}
		if diff := cmp.Diff(map[string][]string{"sam": {"Admin"}}, store.granted); diff != "" {
			t.Errorf("granted (-want +got):\n%s", diff)
		}
	})

	t.Run("by hand: an acceptor that fails leaves the invitation standing and the address unset, and its NotFound is not a dead invitation's", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		d := &memDriver{store: store}
		hooks := &roleHooks{store: store, acceptFail: roleGone}
		id := newUser(store)
		inv := store.invite("sam@lakeside.edu", "", "Admin")

		err := AcceptInvitation(t.Context(), d, id, "sam", "", inv, hooks.accept)

		if !errors.Is(err, roleGone) || errors.Is(err, signin.ErrInvitationExpired) {
			t.Fatalf("AcceptInvitation() error = %v, want the acceptor's failure and not a dead invitation's", err)
		}
		if inv.Accepted || !store.invitations[inv.ID] || store.users[id].email != "" || len(store.granted) != 0 || hooks.committed != 0 {
			t.Errorf("Accepted = %t, live = %t, address = %q, granted = %v, committed runs = %d; want nothing written", inv.Accepted, store.invitations[inv.ID], store.users[id].email, store.granted, hooks.committed)
		}
	})

	t.Run("by hand: an invitation that died is NotFound with ErrInvitationExpired, and the acceptor is not asked", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		d := &memDriver{store: store}
		hooks := &roleHooks{store: store}
		id := newUser(store)
		inv := store.invite("sam@lakeside.edu", "", "Admin")
		store.invitations[inv.ID] = false

		err := AcceptInvitation(t.Context(), d, id, "sam", "", inv, hooks.accept)

		if !httpio.HasNotFound(err) || !errors.Is(err, signin.ErrInvitationExpired) {
			t.Fatalf("AcceptInvitation() error = %v, want NotFound with ErrInvitationExpired", err)
		}
		if diff := cmp.Diff([]string{"rolled back"}, store.steps); diff != "" {
			t.Errorf("steps (-want +got):\n%s", diff)
		}
	})

	t.Run("a password sign-in: an acceptor that fails ends the sign-in with its error, the invitation standing and no session written", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		d := &memDriver{store: store}
		hooks := &roleHooks{store: store, acceptFail: roleGone}
		id := newUser(store)
		inv := store.invite("sam@lakeside.edu", "lakeside", "Coach")

		err := signInWith(t, d, passwordRequest(id, inv), Rules[*memTx]{Policy: hooks.policy, Accept: hooks.accept})

		if !errors.Is(err, roleGone) {
			t.Fatalf("SignIn() error = %v, want the acceptor's failure", err)
		}
		if inv.Accepted || !store.invitations[inv.ID] || store.sessions != 0 || len(store.granted) != 0 || hooks.committed != 0 {
			t.Errorf("Accepted = %t, live = %t, sessions = %d, granted = %v, committed runs = %d; want nothing written", inv.Accepted, store.invitations[inv.ID], store.sessions, store.granted, hooks.committed)
		}
		if diff := cmp.Diff([]string{"accept in run 1", "rolled back"}, store.steps); diff != "" {
			t.Errorf("steps (-want +got):\n%s", diff)
		}
	})

	t.Run("a password sign-in: the acceptance commits, its committed function runs, and then the policy decides; a dead invitation is skipped", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		d := &memDriver{store: store}
		hooks := &roleHooks{store: store}
		id := newUser(store)
		inv := store.invite("sam@lakeside.edu", "lakeside", "Coach")

		if err := signInWith(t, d, passwordRequest(id, inv), Rules[*memTx]{Policy: hooks.policy, Accept: hooks.accept}); err != nil {
			t.Fatalf("SignIn() error = %v", err)
		}
		want := []string{
			"accept in run 1", "commit", "accept of run 1 committed: user=true invitation live=false",
			"policy: sam holds [Coach] after 1 committed", "commit",
		}
		if diff := cmp.Diff(want, store.steps); diff != "" {
			t.Errorf("steps (-want +got):\n%s", diff)
		}

		// The same invitation again: dead now, skipped, and the sign-in goes on.
		store.steps = nil
		again := &sessioninfo.Invitation{ID: inv.ID, Email: inv.Email, Tenant: inv.Tenant, RoleNames: inv.RoleNames}
		if err := signInWith(t, d, passwordRequest(id, again), Rules[*memTx]{Policy: hooks.policy, Accept: hooks.accept}); err != nil {
			t.Fatalf("SignIn() with a dead invitation error = %v, want the sign-in to go on", err)
		}
		if again.Accepted || hooks.committed != 1 {
			t.Errorf("Accepted = %t, committed runs = %d; want the dead invitation skipped and nothing more run", again.Accepted, hooks.committed)
		}
		if diff := cmp.Diff([]string{"rolled back", "policy: sam holds [Coach] after 1 committed", "commit"}, store.steps); diff != "" {
			t.Errorf("steps with a dead invitation (-want +got):\n%s", diff)
		}
	})
}
