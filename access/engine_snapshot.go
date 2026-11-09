package access

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

var _ evaluator = &snapshotEngine{}

const (
	// defaultHeartbeatInterval bounds cross-instance staleness: every tick
	// re-reads the policy store, recompiling only when the content hash
	// changed. A ChangeSignal is the intended propagation path and delivers
	// changes in near-realtime; the heartbeat is only the correctness
	// backstop for a broken or absent signal, so it runs infrequently.
	defaultHeartbeatInterval = time.Minute

	// watchRetryDelay is how long the engine waits before re-invoking
	// ChangeSignal.Watch after it returns.
	watchRetryDelay = 5 * time.Second

	// announceTimeout bounds the best-effort change broadcast after a write.
	announceTimeout = 5 * time.Second
)

// snapshotEngine answers permission checks from a compiled snapshot of the
// policy store. Checks never take a lock or touch the database; a background
// heartbeat (plus an optional push hint) keeps the snapshot fresh.
//
// Failure semantics: the first load must succeed before checks are served
// (errors are returned, never panicked); once a snapshot exists, a failed
// reload serves the last good snapshot and reports through onError.
type snapshotEngine struct {
	store             Store
	defaults          *defaultRoles        // may be nil: no default roles
	collection        PermissionCollection // may be nil: grant names are then not checked
	heartbeatInterval time.Duration
	signal            ChangeSignal // may be nil
	onError           func(error)  // never nil

	// writeGen counts local policy writes. A snapshot is trusted for
	// read-your-writes only when its recorded generation has caught up to
	// writeGen; otherwise the check reloads first. A generation (rather than
	// a flag) makes the protocol race-free against in-flight reloads: a
	// snapshot can never appear current while missing a local write.
	writeGen atomic.Int64

	// syncFailedGen records the generation a synchronous (check-path) reload
	// last failed at, so a store outage right after a local write degrades to
	// serving the last good snapshot instead of every check re-attempting the
	// reload; the heartbeat keeps retrying.
	syncFailedGen atomic.Int64

	snap atomic.Pointer[snapshot]

	// ready is closed by the first successful load; WaitReady blocks on it.
	ready     chan struct{}
	readyOnce sync.Once

	// hintCh coalesces push hints for the background loop.
	hintCh chan struct{}

	mu sync.Mutex // guards reloads

	started     atomic.Bool
	lifecycleMu sync.Mutex // guards start/close transitions
	closed      bool
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	// announcing counts the change announcements in flight, so close waits
	// for them: a write right before Close, the migrate command's seed, must
	// reach the other instances before the application closes the channel
	// the signal rides on.
	announcing sync.WaitGroup
}

func newSnapshotEngine(store Store, defaults *defaultRoles, opts *clientOptions) *snapshotEngine {
	return &snapshotEngine{
		store:             store,
		defaults:          defaults,
		collection:        opts.collection,
		heartbeatInterval: opts.heartbeatInterval,
		signal:            opts.signal,
		onError:           opts.onReloadError,
		ready:             make(chan struct{}),
		hintCh:            make(chan struct{}, 1),
	}
}

// policyChanged is invoked by the policy write path after every successful
// write: the local snapshot reloads on the next check (read-your-writes), the
// background loop is nudged, and other instances are hinted best-effort.
func (s *snapshotEngine) policyChanged() {
	s.invalidate()
	s.hint()
	s.announceAsync()
}

// invalidate marks the current snapshot as predating a local policy write.
// Safe to call concurrently.
func (s *snapshotEngine) invalidate() {
	s.writeGen.Add(1)
}

// hint nudges the background loop to reload; extra hints coalesce.
func (s *snapshotEngine) hint() {
	select {
	case s.hintCh <- struct{}{}:
	default:
	}
}

// announceAsync broadcasts the change without holding up the write. The
// announcement is bounded by announceTimeout and counted, so close waits for
// it instead of letting the process close the channel under it.
func (s *snapshotEngine) announceAsync() {
	if s.signal == nil {
		return
	}
	s.announcing.Add(1)
	go func() {
		defer s.announcing.Done()
		ctx, cancel := context.WithTimeout(context.Background(), announceTimeout)
		defer cancel()
		if err := s.signal.Announce(ctx); err != nil {
			s.onError(errors.Wrap(err, "ChangeSignal.Announce()"))
		}
	}()
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

// close stops the background loops and waits for the announcements in flight,
// each bounded by announceTimeout. Checks keep serving the last snapshot.
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
	s.announcing.Wait()

	return nil
}

// ensureStarted lazily starts the background heartbeat and signal watch on
// first use, so constructing a Client never touches the database.
func (s *snapshotEngine) ensureStarted() {
	if s.started.Load() {
		return
	}

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started.Load() || s.closed {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go s.run(ctx)
	if s.signal != nil {
		s.wg.Add(1)
		go s.watch(ctx)
	}
	s.started.Store(true)
}

// run owns snapshot freshness: an immediate initial load, then a reload on
// every heartbeat tick and push hint. Reload failures serve the last good
// snapshot and report through onError.
func (s *snapshotEngine) run(ctx context.Context) {
	defer s.wg.Done()

	s.tryReload(ctx)

	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tryReload(ctx)
		case <-s.hintCh:
			s.tryReload(ctx)
		}
	}
}

// watch runs the push-hint subscription, re-invoking it with a delay whenever
// it returns. Hints only nudge the reload loop; a broken signal never affects
// correctness.
func (s *snapshotEngine) watch(ctx context.Context) {
	defer s.wg.Done()

	for {
		if err := s.signal.Watch(ctx, s.hint); err != nil && ctx.Err() == nil {
			s.onError(errors.Wrap(err, "ChangeSignal.Watch()"))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(watchRetryDelay):
		}
	}
}

func (s *snapshotEngine) tryReload(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.reload(ctx); err != nil {
		s.onError(err)
	}
}

func (s *snapshotEngine) checkUser(ctx context.Context, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission) (resourceDecision, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return resourceDecision{}, err
	}

	return snap.checkUser(user, scope, perm), nil
}

func (s *snapshotEngine) checkUserResources(ctx context.Context, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) ([]resourceDecision, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	return snap.decideUserResources(user, scope, perm, resources...), nil
}

func (s *snapshotEngine) userDigest(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	return snap.userDigest(scope, user), nil
}

func (s *snapshotEngine) userHasGrants(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (bool, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return false, err
	}

	return snap.userHasGrants(scope, user), nil
}

func (s *snapshotEngine) userPermissions(ctx context.Context, user accesstypes.User, scope accesstypes.Scope) (accesstypes.UserScopePermissions, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return accesstypes.UserScopePermissions{}, err
	}

	return snap.userPermissions(scope, user), nil
}

func (s *snapshotEngine) checkRole(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission) (resourceDecision, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return resourceDecision{}, err
	}

	return snap.checkRole(role, scope, perm), nil
}

func (s *snapshotEngine) checkRoleResources(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope, perm accesstypes.Permission, resources ...accesstypes.Resource) ([]resourceDecision, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	return snap.decideRoleResources(role, scope, perm, resources...), nil
}

func (s *snapshotEngine) roleDigest(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	return snap.roleDigest(scope, role), nil
}

func (s *snapshotEngine) roleHasGrants(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope) (bool, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return false, err
	}

	return snap.roleHasGrants(scope, role), nil
}

func (s *snapshotEngine) rolePermissions(ctx context.Context, role accesstypes.Role, scope accesstypes.Scope) (accesstypes.RolePermissionCollection, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	return snap.rolePermissions(scope, role), nil
}

// peek returns the current snapshot without triggering loads. Nil until the
// first successful load.
func (s *snapshotEngine) peek() *snapshot {
	return s.snap.Load()
}

func (s *snapshotEngine) currentSnapshot(ctx context.Context) (*snapshot, error) {
	s.ensureStarted()

	snap := s.snap.Load()
	if snap == nil {
		// No snapshot yet: block on the load so the first requests get a
		// real answer (or a real error), whether or not the background
		// loop's initial load has finished.
		s.mu.Lock()
		defer s.mu.Unlock()
		if snap := s.snap.Load(); snap != nil {
			return snap, nil
		}

		return s.reload(ctx)
	}

	if gen := s.writeGen.Load(); snap.writeGen < gen && s.syncFailedGen.Load() < gen {
		// This instance wrote policy after the snapshot's store read: reload
		// before answering so the write is visible to its own subsequent
		// requests (matching the shared-enforcer behavior casbin had).
		// Blocking is fine here — it only happens in the brief window after
		// a local write.
		s.mu.Lock()
		defer s.mu.Unlock()
		// Compare against gen (the counter as of this check's entry), never
		// the live counter: this check only owes visibility of writes that
		// happened before it began. Chasing the live counter would send every
		// check queued behind a reload into its own serial reload whenever
		// writes land continuously, convoying the check path on mu.
		if cur := s.snap.Load(); cur.writeGen >= gen {
			// A concurrent reload already covered this check's writes.
			return cur, nil
		}
		fresh, err := s.reload(ctx)
		if err == nil {
			return fresh, nil
		}
		// Reload failed: serve the last good snapshot, never an empty one.
		// Remember the failed generation so checks stop re-attempting until
		// the next write; the heartbeat keeps retrying regardless.
		s.syncFailedGen.Store(gen)
	}

	return s.snap.Load(), nil
}

// reload reads the policy store and swaps in a freshly compiled snapshot when
// its content changed. The caller must hold mu.
func (s *snapshotEngine) reload(ctx context.Context) (*snapshot, error) {
	// Capture the write generation BEFORE reading: every local write at or
	// below this generation committed before the read, so the snapshot can
	// honestly claim it. A write racing the read lands above it and keeps
	// the snapshot untrusted for read-your-writes.
	gen := s.writeGen.Load()

	records, err := s.store.ReadPolicy(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "access.Store.ReadPolicy()")
	}

	recordsHash := records.Hash()
	if cur := s.snap.Load(); cur != nil && cur.recordsHash == recordsHash {
		if cur.writeGen >= gen {
			return cur, nil
		}
		// Content is unchanged but this read proves generation gen: publish
		// it so checks stop treating the snapshot as predating local writes.
		// A shallow copy suffices — compiled structures are immutable.
		fresh := *cur
		fresh.writeGen = gen
		fresh.loadedAt = time.Now()
		s.snap.Store(&fresh)

		return &fresh, nil
	}

	snap, findings, err := newSnapshot(records, s.defaults, s.collection, time.Now())
	if err != nil {
		return nil, errors.Wrap(err, "newSnapshot()")
	}
	snap.writeGen = gen
	s.snap.Store(snap)
	s.readyOnce.Do(func() { close(s.ready) })

	// What the store holds that this release cannot use as written is
	// reported once per compiled snapshot: an unchanged store does not
	// recompile, so a persistent finding is not reported on every heartbeat.
	for _, f := range findings {
		s.onError(f)
	}

	return snap, nil
}
