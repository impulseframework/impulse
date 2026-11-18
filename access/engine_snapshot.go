package access

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/internal/freshness"
)

var _ evaluator = &snapshotEngine{}

const (
	// defaultHeartbeatInterval bounds cross-instance staleness: a check is
	// never answered from a snapshot whose store read began longer ago than
	// this, and the loop re-reads the policy store on every tick of it,
	// recompiling only when the content hash changed. A ChangeSignal is the
	// intended propagation path and delivers changes in near-realtime; the
	// interval is the bound for a broken or absent signal, so it is long.
	defaultHeartbeatInterval = time.Minute

	// reloadTimeout is the engine's own bound on one read of the policy
	// store. Every read runs under it, the ones a check waits on included. A
	// check that finds the snapshot due waits for the read in flight when it
	// came, if there is one, and for at most one read after it, so a store
	// that does not answer holds a check for less than twice this before the
	// check is answered from the last good snapshot.
	reloadTimeout = 10 * time.Second

	// reloadPause is how long the engine leaves the policy store alone after a
	// reload of its own failed: the checks that come inside it are answered
	// from the last good snapshot and none of them reads, so a store that
	// refuses at once is read once a pause and not once a check, and the
	// failure is reported at that rate. A change announced meanwhile stays
	// recorded, and the first check after the pause reads.
	reloadPause = time.Second

	// watchRetryDelay is how long the engine waits before re-invoking
	// ChangeSignal.Watch after it returns.
	watchRetryDelay = 5 * time.Second

	// defaultAnnounceTimeout bounds the announcement of a policy write to the
	// other instances, which the write waits for: past it the write returns,
	// the failure is reported, and the other instances learn of the change at
	// their heartbeat interval. It is what a feature flip's signal gets.
	defaultAnnounceTimeout = 2 * time.Second

	// maxWindows bounds the snapshots kept for instants outside the current
	// one's window (snapshotEngine.windows).
	maxWindows = 4
)

// snapshotEngine answers permission checks from a compiled snapshot of the
// policy store. A check that finds the snapshot inside its bound, with no
// change announced since its store read began, takes no lock and does not
// touch the database.
//
// Freshness is the check's own (fresh): a check that finds the snapshot past
// the heartbeat interval, or behind a change this instance wrote or was told
// of, reads the store before it answers, and one read serves every check
// waiting at that moment. The background loop and the push hint do the same
// sooner, so on an instance that is serving the check seldom reads; nothing
// depends on them, so an instance that got no CPU between requests still
// answers its next check from a read no older than the interval.
//
// No read of the store runs under a caller's context. A check made inside a
// database transaction carries that transaction on its context, and a store
// whose client refuses a second read there would fail the reload and leave
// every check on the old snapshot; so the engine reads under its own
// context, with its own timeout (reloadTimeout), whoever asked.
//
// Failure semantics: the first load must succeed before checks are served
// (errors are returned, never panicked); once a snapshot exists, a failed
// reload serves the last good snapshot and reports through onError, and the
// store is tried again by the first check after a short pause (reloadPause).
//
// An assignment counts inside its period alone, and a snapshot holds the ones
// that count in a window of time (snapshot.holdsAt). A check is answered from
// the snapshot of its instant: the current one while the instant is in its
// window, and one compiled from the same records, with no store read, once
// the instant has left it. So a role stops granting at its end and starts at
// its start with nothing written and nothing run on a schedule: the first
// check past the instant compiles the next window.
type snapshotEngine struct {
	store      Store
	defaults   *defaultRoles        // may be nil: no default roles
	collection PermissionCollection // may be nil: grant names are then not checked
	// tenantAdministrators are the roles whose holders administer a tenant
	// (WithTenantAdministratorRoles); none where the application named none.
	tenantAdministrators []accesstypes.Role
	heartbeatInterval    time.Duration
	// announceTimeout bounds one announcement of a policy write
	// (defaultAnnounceTimeout outside a test).
	announceTimeout time.Duration
	signal          ChangeSignal // may be nil
	onError         func(error)  // never nil

	// fresh decides, for every check, whether the store is read before the
	// check is answered: when the snapshot is past the heartbeat interval, and
	// when a policy write of this instance or a hint from another has been
	// recorded and no store read that began after it has landed. It counts
	// the writes and hints as a generation (rather than a flag), which makes
	// the protocol race-free against in-flight reloads: a snapshot can never
	// appear current while missing a change that was announced.
	fresh *freshness.Gate

	snap atomic.Pointer[snapshot]

	// clock is the engine's present: the instant a snapshot is compiled for at
	// a load, and the instant of a check whose Environment carries none.
	clock func() time.Time

	// windows are the snapshots compiled from the current records for instants
	// outside the current snapshot's window, the newest last: the window the
	// present moved into until the current snapshot is swapped for it, and the
	// ones a check asked for with an instant of its own. windowMu guards them.
	windowMu sync.Mutex
	windows  []*snapshot

	// ready is closed by the first successful load; WaitReady blocks on it.
	ready     chan struct{}
	readyOnce sync.Once

	// hintCh coalesces push hints for the background loop.
	hintCh chan struct{}

	// noLoops is a test's stand-in for an instance that gets no CPU between
	// requests: the engine starts neither its reload loop nor its signal
	// watch, so nothing but a check reads the store. Set before first use.
	noLoops bool

	started     atomic.Bool
	lifecycleMu sync.Mutex // guards start/close transitions
	closed      bool
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func newSnapshotEngine(store Store, defaults *defaultRoles, opts *clientOptions) *snapshotEngine {
	s := &snapshotEngine{
		store:                store,
		defaults:             defaults,
		collection:           opts.collection,
		tenantAdministrators: slices.Clone(opts.tenantAdministrators),
		heartbeatInterval:    opts.heartbeatInterval,
		announceTimeout:      defaultAnnounceTimeout,
		signal:               opts.signal,
		onError:              opts.onReloadError,
		clock:                time.Now,
		ready:                make(chan struct{}),
		hintCh:               make(chan struct{}, 1),
	}
	s.fresh = freshness.New(s.reload, opts.heartbeatInterval, reloadTimeout, reloadPause, s.now, s.reloadFailed)

	return s
}

// now is the engine's present, read through clock at every call: a test sets
// the clock after the engine is built.
func (s *snapshotEngine) now() time.Time {
	return s.clock()
}

// reloadFailed reports a reload the engine made itself that failed: the
// check's or the loop's, refused or failed. The last good snapshot answers,
// and the first check after the pause tries the store again.
func (s *snapshotEngine) reloadFailed(_ context.Context, err error) {
	s.onError(err)
}

// policyChanged is invoked by the policy write path after every successful
// write: the local snapshot reloads on the next check (read-your-writes; on
// the first check after the pause, where a reload has just failed), the
// background loop is nudged, and the announcement is handed to the change
// signal before it returns.
func (s *snapshotEngine) policyChanged() {
	s.invalidate()
	s.hint()
	s.announce()
}

// invalidate marks the current snapshot as predating a local policy write:
// the next check reads the store before it answers, or the first check after
// the pause a failed reload left. Safe to call concurrently.
func (s *snapshotEngine) invalidate() {
	s.fresh.Nudge()
}

// hinted is what the change signal's watch calls when another instance
// announces a change. The hint is recorded at once, before any reload is
// tried, so the next check reads the store before it answers whether or not
// the loop got to reload (the first check after the pause, where a reload has
// just failed), and then the loop is woken to reload ahead of it.
func (s *snapshotEngine) hinted() {
	s.fresh.Nudge()
	s.hint()
}

// hint wakes the background loop to reload; extra hints coalesce.
func (s *snapshotEngine) hint() {
	select {
	case s.hintCh <- struct{}{}:
	default:
	}
}

// announce tells the other instances of the change, and returns once the
// announcement has been handed to the signal or its time has run out. It is
// the last thing a policy write does, inside the write, so the engine leaves
// nothing to publish once the write has returned: an instance that gets no
// CPU after it has answered holds no announcement of the engine's back, and
// a write that has returned before Close has handed its announcement to the
// channel the signal rides on. What a signal does with an announcement it has
// taken is the signal's own: one that takes it and sends it later holds that
// part itself. The announcement runs under the engine's own timeout and no
// caller's context. One that fails or runs out does not fail the write, which
// is committed: it is reported through onError, as a failed reload is, and
// the other instances learn of the change at their heartbeat interval. A
// write path that stores several rows calls this after each, so its
// announcements are made one after another, each under its own timeout.
func (s *snapshotEngine) announce() {
	if s.signal == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.announceTimeout)
	defer cancel()
	if err := s.signal.Announce(ctx); err != nil {
		s.onError(errors.Wrap(err, "ChangeSignal.Announce()"))
	}
}

// waitReady blocks until the first successful load or ctx expiry.
func (s *snapshotEngine) waitReady(ctx context.Context) error {
	s.ensureStarted()
	select {
	case <-s.ready:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "waiting for first policy load")
	}
}

// close ends the engine's life: it stops the background loops. Checks keep
// serving the last snapshot, and read the store no more. No announcement is
// left to wait for: each is made inside the write it follows.
func (s *snapshotEngine) close() error {
	s.lifecycleMu.Lock()
	if s.closed {
		s.lifecycleMu.Unlock()

		return nil
	}
	s.closed = true
	cancel := s.cancel
	s.lifecycleMu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.wg.Wait()

	return nil
}

// ensureStarted lazily begins the engine's life on first use, so constructing
// a Client never touches the database: from here until close the snapshot is
// kept fresh for every check, and the background heartbeat and signal watch
// run to do it ahead of the checks.
func (s *snapshotEngine) ensureStarted() {
	if s.started.Load() {
		return
	}

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started.Load() || s.closed {
		return
	}

	// The engine's own context: every read of the store runs under it, and
	// it ends at close.
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.fresh.Keep(ctx)
	if !s.noLoops {
		s.wg.Add(1)
		go s.run(ctx)
		if s.signal != nil {
			s.wg.Add(1)
			go s.watch(ctx)
		}
	}
	s.started.Store(true)
}

// run reloads ahead of the checks: an immediate initial load, then a reload
// on every heartbeat tick and push hint. It is an accelerator and nothing
// more: it asks for the store read a check would make, sooner, so on an
// instance that is serving the check finds the snapshot fresh and reads
// nothing. Reload failures serve the last good snapshot and report through
// onError.
func (s *snapshotEngine) run(ctx context.Context) {
	defer s.wg.Done()

	s.refresh()

	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		case <-s.hintCh:
			// Whoever hinted recorded it already (invalidate, hinted).
			s.refresh()
		}
	}
}

// tick is the loop's heartbeat: it reads the store whatever the snapshot's
// age. The tick asks for the read as a hint does, since the snapshot the
// loop read one interval ago is not yet past the interval when the tick
// fires: left to the bound alone the loop would read on every other tick,
// and the check that found the interval ended would read on the rest.
func (s *snapshotEngine) tick() {
	s.fresh.Nudge()
	s.refresh()
}

// refresh reads the store unless a read that holds everything announced has
// landed already, which a check may have made first. A failure was reported
// where it happened (reloadFailed), and the last good snapshot answers.
func (s *snapshotEngine) refresh() {
	_ = s.fresh.Freshen()
}

// watch runs the push-hint subscription, re-invoking it with a delay whenever
// it returns. A hint is recorded and wakes the reload loop; a broken signal
// never affects correctness.
func (s *snapshotEngine) watch(ctx context.Context) {
	defer s.wg.Done()

	for {
		if err := s.signal.Watch(ctx, s.hinted); err != nil && ctx.Err() == nil {
			s.onError(errors.Wrap(err, "ChangeSignal.Watch()"))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(watchRetryDelay):
		}
	}
}

func (s *snapshotEngine) checkUser(_ context.Context, env accesstypes.Environment, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission) (resourceDecision, error) {
	snap, err := s.snapshotAt(s.instant(env))
	if err != nil {
		return resourceDecision{}, err
	}

	return snap.checkUser(user, scope, perm), nil
}

func (s *snapshotEngine) checkUserResources(
	_ context.Context, env accesstypes.Environment, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource,
) ([]resourceDecision, error) {
	snap, err := s.snapshotAt(s.instant(env))
	if err != nil {
		return nil, err
	}

	return snap.decideUserResources(user, scope, perm, resources...), nil
}

func (s *snapshotEngine) userDigest(_ context.Context, user accesstypes.User, scope accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	snap, err := s.snapshotAt(s.clock())
	if err != nil {
		return nil, err
	}

	return snap.userDigest(scope, user), nil
}

func (s *snapshotEngine) userHasGrants(_ context.Context, user accesstypes.User, scope accesstypes.Scope) (bool, error) {
	snap, err := s.snapshotAt(s.clock())
	if err != nil {
		return false, err
	}

	return snap.userHasGrants(scope, user), nil
}

func (s *snapshotEngine) userIsSuperAdministrator(_ context.Context, user accesstypes.User) (bool, error) {
	snap, err := s.snapshotAt(s.clock())
	if err != nil {
		return false, err
	}

	return snap.userIsSuperAdministrator(user), nil
}

func (s *snapshotEngine) userPermissions(_ context.Context, user accesstypes.User, scope accesstypes.Scope) (accesstypes.UserScopePermissions, error) {
	snap, err := s.snapshotAt(s.clock())
	if err != nil {
		return accesstypes.UserScopePermissions{}, err
	}

	return snap.userPermissions(scope, user), nil
}

func (s *snapshotEngine) checkRole(_ context.Context, role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission) (resourceDecision, error) {
	snap, err := s.currentSnapshot()
	if err != nil {
		return resourceDecision{}, err
	}

	return snap.checkRole(role, scope, perm), nil
}

func (s *snapshotEngine) checkRoleResources(_ context.Context, role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) ([]resourceDecision, error) {
	snap, err := s.currentSnapshot()
	if err != nil {
		return nil, err
	}

	return snap.decideRoleResources(role, scope, perm, resources...), nil
}

func (s *snapshotEngine) roleDigest(_ context.Context, role accesstypes.Role, scope accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	snap, err := s.currentSnapshot()
	if err != nil {
		return nil, err
	}

	return snap.roleDigest(scope, role), nil
}

func (s *snapshotEngine) roleHasGrants(_ context.Context, role accesstypes.Role, scope accesstypes.Scope) (bool, error) {
	snap, err := s.currentSnapshot()
	if err != nil {
		return false, err
	}

	return snap.roleHasGrants(scope, role), nil
}

func (s *snapshotEngine) rolePermissions(_ context.Context, role accesstypes.Role, scope accesstypes.Scope) (accesstypes.RolePermissionCollection, error) {
	snap, err := s.currentSnapshot()
	if err != nil {
		return nil, err
	}

	return snap.rolePermissions(scope, role), nil
}

// instant is the instant a check is made at: the Environment's now, the one a
// grant's condition folds with, so an assignment's period and a condition's
// window can never disagree about a boundary, and the engine's own clock where
// the Environment carries none.
func (s *snapshotEngine) instant(env accesstypes.Environment) time.Time {
	if now, ok := env.Now(); ok {
		return now
	}

	return s.clock()
}

// snapshotAt returns the snapshot that answers at the instant: the current one
// while the instant is in its window, which is every instant where no
// assignment carries a period, and otherwise the window compiled for it.
func (s *snapshotEngine) snapshotAt(at time.Time) (*snapshot, error) {
	current, err := s.currentSnapshot()
	if err != nil {
		return nil, err
	}
	if current.holdsAt(at) {
		return current, nil
	}

	return s.windowAt(current, at)
}

// windowAt returns the snapshot of current's records that answers at the
// instant, compiling it where no window kept holds it. Where the engine's
// present is in that window it becomes the current snapshot, so the checks
// after it take no lock; a reload that swapped the current snapshot meanwhile
// keeps its own, and the next check out of its window lands here again.
func (s *snapshotEngine) windowAt(current *snapshot, at time.Time) (*snapshot, error) {
	s.windowMu.Lock()
	defer s.windowMu.Unlock()

	if len(s.windows) > 0 && s.windows[0].recordsHash != current.recordsHash {
		// The windows were compiled from records the engine has moved on from.
		s.windows = nil
	}
	window := s.keptWindow(at)
	if window == nil {
		compiled, _, err := newSnapshotAt(current.timed, s.defaults, s.collection, current.loadedAt, at)
		if err != nil {
			return nil, errors.Wrap(err, "newSnapshotAt()")
		}
		// The window is compiled from the current snapshot's records, so the
		// same people administer the same tenants in the same periods.
		compiled.administrators = current.administrators
		if len(s.windows) == maxWindows {
			s.windows = slices.Delete(s.windows, 0, 1)
		}
		s.windows = append(s.windows, compiled)
		window = compiled
	}
	if window.holdsAt(s.clock()) {
		s.snap.CompareAndSwap(current, window)
	}

	return window, nil
}

// keptWindow returns the window kept that answers at the instant, nil where
// none does. The caller holds windowMu.
func (s *snapshotEngine) keptWindow(at time.Time) *snapshot {
	for _, window := range s.windows {
		if window.holdsAt(at) {
			return window
		}
	}

	return nil
}

// peek returns the current snapshot without triggering loads. Nil until the
// first successful load.
func (s *snapshotEngine) peek() *snapshot {
	return s.snap.Load()
}

// currentSnapshot returns the snapshot a check is answered from, reading the
// store first when the snapshot is due (fresh). It takes no context: no
// caller's context, and so no caller's transaction, reaches the store.
func (s *snapshotEngine) currentSnapshot() (*snapshot, error) {
	s.ensureStarted()

	err := s.fresh.Freshen()
	if snap := s.snap.Load(); snap != nil {
		// A reload that failed was reported where it happened: the last good
		// snapshot answers, never an empty one.
		return snap, nil
	}
	if err == nil {
		// No snapshot, and nothing was read: the engine was closed before its
		// first load.
		return nil, errors.New("the engine was closed before it loaded the policy")
	}

	// No snapshot yet: the first requests get a real error, whether the
	// check's own load failed or the one it waited on.
	return nil, errors.Wrap(err, "freshness.Gate.Freshen()")
}

// reload reads the policy store and swaps in a freshly compiled snapshot when
// its content changed. It is the read fresh makes, one at a time, under the
// engine's own context with the engine's timeout.
func (s *snapshotEngine) reload(ctx context.Context) error {
	records, err := s.store.ReadPolicy(ctx)
	if err != nil {
		return errors.Wrap(err, "access.Store.ReadPolicy()")
	}

	if cur := s.snap.Load(); cur != nil && cur.recordsHash == records.Hash() {
		// The store holds what the snapshot was compiled from: the snapshot
		// stays, and fresh records that it was read again.
		return nil
	}

	snap, findings, err := newSnapshot(records, s.defaults, s.collection, s.clock())
	if err != nil {
		return errors.Wrap(err, "newSnapshot()")
	}
	snap.administrators = compileTenantAdministrators(records, s.tenantAdministrators)
	s.snap.Store(snap)
	s.readyOnce.Do(func() { close(s.ready) })

	// What the store holds that this release cannot use as written is
	// reported once per compiled snapshot: an unchanged store does not
	// recompile, so a persistent finding is not reported on every heartbeat.
	for _, f := range findings {
		s.onError(f)
	}

	return nil
}
