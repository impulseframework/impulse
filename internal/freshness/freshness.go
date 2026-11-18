// Package freshness keeps an instance's copy of something the database holds no older
// than a bound, without depending on a background goroutine.
//
// An instance answers its requests from copies: the permission policy, the tenants, the
// feature flags. A loop that rereads on a tick and on a pushed signal keeps a copy
// current only while the process has CPU, and a platform that gives a process CPU only
// while a request is in flight runs nothing between requests: the first request after an
// idle spell would be answered from whatever the copy held when the instance went idle.
//
// A Gate makes the freshness the request's. Every read of the copy passes the Gate first
// (Freshen), and the Gate reads the store before the request is answered when the copy
// is past its bound, or when a change was announced (Nudge) and no store read that began
// after the announcement has landed. One store read serves every request waiting at that
// moment. The loops remain as accelerators: they pass the same Gate sooner than a
// request would, and nothing correct depends on their having run.
package freshness

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Gate decides, for every read of a copy, whether the store is read first. It holds no
// copy itself: the read function it is built with reads the store and replaces the copy
// wherever its owner keeps it, and the Gate keeps when the last read began and what had
// been announced by then. It is safe for concurrent use.
//
// A request's context never reaches the store through Freshen: it takes none, and reads
// under the context the Gate was told to keep the copy for (Keep), with the Gate's own
// timeout. So a caller inside a database transaction, whose context the database's
// client refuses a second read under, cannot make the read fail, and a caller that goes
// away cannot cancel a read other requests are waiting on. The one read that runs under
// a caller's context is the one the caller asks for outright (Read), and what that read
// fails with is the caller's alone.
type Gate struct {
	// read reads the store and replaces the copy. The Gate calls it with its lock held,
	// one read at a time, and it must not come back through the Gate.
	read func(ctx context.Context) error
	// bound is how old the copy may be when a request is answered from it, measured
	// from the moment the read that filled it began.
	bound time.Duration
	// timeout bounds one read of the store. A request that finds the copy due waits for
	// the read in flight when it arrived, if there is one, and for at most one read of
	// the Gate's own after it: a read that began after the request arrived answers it,
	// whether it lands or fails. So a store that does not answer holds a request for
	// less than twice this, and for one timeout more for each read a caller asked for
	// outright (Read) that is queued ahead of it and fails.
	timeout time.Duration
	// clock is the Gate's present.
	clock func() time.Time
	// failed is told of each of the Gate's own reads that failed, once, with the
	// context the copy is kept for: where the failure is logged.
	failed func(ctx context.Context, err error)

	// kept is the life the copy is kept fresh for, nil until Keep. Outside it Freshen
	// reads nothing and the copy answers as it stands.
	kept atomic.Pointer[keeping]

	// nudges counts the changes announced to this instance. A generation, not a flag,
	// so a read that was already in flight when an announcement came cannot clear it:
	// the read recorded the count it began at, and the count has moved on.
	nudges atomic.Int64

	// landed is the last store read that succeeded, nil until the first.
	landed atomic.Pointer[attempt]

	// mu admits one store read at a time. The requests that find the copy due wait on
	// it, and each looks again once it has it: the read it waited for has most often
	// answered it.
	mu sync.Mutex
	// finished counts the Gate's own reads that have ended, and failure is the last of
	// them that failed, guarded by mu: how a request that waited on a read that failed
	// is answered by that read instead of making its own.
	finished atomic.Int64
	failure  *failure
}

// keeping is the context a copy is kept fresh under.
type keeping struct {
	ctx context.Context
}

// attempt is one read of the store: when it began, and the announcements recorded before
// it began, which are the ones it is known to hold once it lands.
type attempt struct {
	began  time.Time
	nudges int64
}

// failure is one of the Gate's own reads that failed: its place in the count of reads
// ended, the read that was made, and what it failed with.
type failure struct {
	finished int64
	made     attempt
	err      error
}

// New returns a Gate over read, which reads the store and replaces the copy. The copy
// is kept no older than bound; one read of the store takes no longer than timeout;
// clock is the present, time.Now outside a test; failed is told of each read of the
// Gate's own that failed. Nothing is read, and nothing is kept fresh, until Keep.
func New(read func(ctx context.Context) error, bound, timeout time.Duration, clock func() time.Time, failed func(ctx context.Context, err error)) *Gate {
	g := new(Gate)
	g.read = read
	g.bound = bound
	g.timeout = timeout
	g.clock = clock
	g.failed = failed

	return g
}

// Keep begins keeping the copy fresh, until ctx ends: from now a read of the copy that
// finds it past its bound, or behind an announcement, reads the store first. ctx is the
// life of the copy, never a request's: the Gate's own reads run under it, and once it
// has ended they stop and the copy answers as it stands.
func (g *Gate) Keep(ctx context.Context) {
	g.kept.Store(&keeping{ctx: ctx})
}

// Nudge records that a change was announced: a pushed signal arrived, or this instance
// wrote the store itself. It is recorded at once, before any read is tried, and from
// then every read of the copy reads the store first, exactly as one past the bound
// does, until a store read that began after the announcement has landed. So a change
// that was announced is never answered from the old copy on an instance that heard the
// announcement, whether or not its loop got to reread.
func (g *Gate) Nudge() {
	g.nudges.Add(1)
}

// Freshen is what every read of the copy calls before it answers. Inside the bound and
// with nothing announced it compares two numbers and returns. Otherwise it reads the
// store before it returns, or waits for the read another request is making: one read
// serves every request waiting at that moment, and a request reads again only when the
// read it waited for began before an announcement the request had already seen, or
// began before the request arrived and is itself past the bound.
//
// A read that fails is told to failed, and its error is returned to the request that
// made it and to the ones that waited on it while it could still have succeeded. The
// copy stays as it was, so a caller that holds a copy answers from it; the next request
// tries the store again.
func (g *Gate) Freshen() error {
	kept := g.kept.Load()
	if kept == nil || kept.ctx.Err() != nil {
		return nil
	}
	// What this request arrives to, taken before it looks at the copy: the reads that
	// have ended, so a read that fails while it waits is known to have been in flight
	// then; the announcements made so far; and the moment it came. It owes those
	// announcements and no later one: a request that chased the live count would read
	// again for every change that lands while it waits, and under steady writes every
	// waiting request would make a read of its own.
	finished := g.finished.Load()
	nudges := g.nudges.Load()
	arrived := g.clock()
	if g.current(g.landed.Load(), nudges, arrived) {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if kept.ctx.Err() != nil {
		return nil
	}
	if g.answered(nudges, arrived) {
		return nil
	}
	if f := g.failure; f != nil && g.shared(f, finished, nudges, arrived) {
		return f.err
	}

	ctx, cancel := context.WithTimeout(kept.ctx, g.timeout)
	defer cancel()
	made, err := g.land(ctx)
	ended := g.finished.Add(1)
	if err == nil {
		return nil
	}
	g.failure = &failure{finished: ended, made: made, err: err}
	if kept.ctx.Err() == nil {
		g.failed(kept.ctx, err)
	}

	return err
}

// Read reads the store now, whatever the copy's age: what a write path calls after its
// commit so the instance that wrote answers from the write at once, and what loads the
// copy the first time. It waits for a read in flight and then makes its own, since the
// one in flight may have begun before the caller's write. The read runs under ctx, the
// caller's, held to the Gate's timeout; its failure is returned to the caller alone,
// and the copy stays as it was.
//
// The caller's change is recorded first, as Nudge records one, so a read that fails
// leaves the copy owing it: the next request reads the store under the Gate's own
// context, where a caller's context that has ended, or that the store refuses, cannot
// fail the read a second time.
func (g *Gate) Read(ctx context.Context) error {
	g.Nudge()

	g.mu.Lock()
	defer g.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	_, err := g.land(ctx)

	return err
}

// land reads the store and, when the read succeeds, records it as the one the copy
// came from. It returns the read it made: when it began and the announcements it
// holds, which is the count taken before the read began, so every announcement in it
// was made before the read and a later one leaves the copy behind it. The caller holds
// mu.
func (g *Gate) land(ctx context.Context) (attempt, error) {
	made := attempt{began: g.clock(), nudges: g.nudges.Load()}
	if err := g.read(ctx); err != nil {
		return made, err
	}
	g.landed.Store(&made)

	return made, nil
}

// current reports whether the read that landed answers a request that arrives now
// having seen the given announcements: the read began after every one of them, and no
// longer ago than the bound.
func (g *Gate) current(l *attempt, nudges int64, now time.Time) bool {
	return l != nil && l.nudges >= nudges && now.Sub(l.began) <= g.bound
}

// answered reports whether the read that landed answers a request that has waited its
// turn at the store: the read began after every announcement the request had seen, and
// either after the request arrived or no longer ago than the bound. A read that began
// after the request arrived is as new as any the request could make, however long it
// took; so where the store answers slower than the bound, each read still serves every
// request that was waiting when it began, and the requests do not read one after
// another. The caller holds mu.
func (g *Gate) answered(nudges int64, arrived time.Time) bool {
	l := g.landed.Load()
	if l == nil || l.nudges < nudges {
		return false
	}
	if !l.began.Before(arrived) {
		return true
	}

	return g.clock().Sub(l.began) <= g.bound
}

// shared reports whether a read that failed answers a request with its failure, in
// place of a read of the request's own: the read ended while the request waited, it
// would have held every announcement the request had seen, and it still had time to
// succeed when the request arrived. A read whose time had run out before the request
// came was never made for it. That is the read an instance leaves in flight when it
// loses its CPU: it fails at its timeout the moment the next request wakes the
// instance, the store was not asked on that request's behalf, and the request reads.
func (g *Gate) shared(f *failure, finished, nudges int64, arrived time.Time) bool {
	return f.finished > finished && f.made.nudges >= nudges && arrived.Sub(f.made.began) < g.timeout
}
