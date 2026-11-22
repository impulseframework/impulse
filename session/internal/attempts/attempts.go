// Package attempts limits the password attempts of an Auth. It runs in memory, per
// instance, before any store access and before the password hash, over a durable record
// the store keeps by the submitted name. The memory holds three kinds of key: the name,
// the client's address and the login's XSRF cookie token. A name is held by its store
// record: an attempt takes its place in the record before its password is compared
// (Take), in the store's one transaction, which says whether it was counted, so of
// attempts made at once no more than the limit are ever compared; one that was not
// counted is refused, and a right password gives the attempt back and resets the name
// (Succeeded). The record the store answered is cached here for a few seconds (readTTL),
// so an attempt during a lock is refused the moment it arrives with nothing hashed and
// the store asked at most once in that time, and a lock an administrator cleared in the
// store is gone from every instance within it. The client's keys are counted here alone:
// an attempt takes its place under them in the step that finds them free, every wrong
// and every refused attempt counts against them, and a client that keeps trying during
// a wait extends its own lock while the name's stays as it was. The memory is bounded;
// the store is the truth for the name.
package attempts

import (
	"container/list"
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Store keeps the durable record of a name's attempts.
type Store interface {
	// RecordSignInFailure records a failure on name under limit, in one transaction, and
	// returns the record, which says whether the failure was counted (Counted).
	RecordSignInFailure(ctx context.Context, name string, limit dbtype.SignInAttemptLimit) (*dbtype.SignInAttempts, error)
	// ClearSignInAttempts deletes name's record.
	ClearSignInAttempts(ctx context.Context, name string) error
}

// Client identifies the client of an attempt: its address and its login's XSRF cookie
// token. Either is empty when unknown, as both are for an attempt through the API.
type Client struct {
	Address string
	Cookie  string
}

const (
	// cacheSize bounds the entries the memory holds across every kind of key; the
	// least recently used entry goes when it is full, so a flood of names grows nothing
	// without limit. A name's lock evicted from memory is still the store's.
	cacheSize = 16384
	// readTTL is how long a lock in a name's store record is trusted before the next
	// attempt asks the store again. A lock was once trusted until it passed, which left
	// no way to clear one: the row deleted in the store, every instance that had seen
	// the lock went on refusing the name. Asked again after the window, a cleared lock
	// is gone from every instance within it, and a name still locked costs one
	// transaction on its row per instance per window, which changes nothing in it but
	// the time it was written.
	readTTL = 5 * time.Second
	// giveBackTimeout bounds the store's clear of a name whose attempt is given back. The
	// clear runs to its end though the request that entered the right password was
	// canceled meanwhile, so it is held to a time of its own.
	giveBackTimeout = 30 * time.Second
	// ClientMultiple is how many times the name limit's failures a client's address or
	// cookie may fail, across names, within the window before the client is locked for
	// the lock period.
	ClientMultiple = 10
)

// Limiter is the in-memory layer over a Store.
type Limiter struct {
	limit dbtype.SignInAttemptLimit
	store Store
	now   func() time.Time

	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List
}

// entry is one key's state. A name's entry caches the record the store last answered,
// and when. A client's entry is its counter: the attempts counted in the window ending
// at windowEnds, the lock they earned, and the attempt whose place earned it when it was
// one whose password was not compared yet (lockedBy), which lifts the lock when its
// password proves right.
type entry struct {
	key         string
	record      *dbtype.SignInAttempts
	readAt      time.Time
	failures    int
	windowEnds  time.Time
	lockedUntil time.Time
	lockedBy    *Attempt
}

// New returns a limiter under limit over store.
func New(limit dbtype.SignInAttemptLimit, store Store) *Limiter {
	return &Limiter{limit: limit, store: store, now: time.Now, entries: make(map[string]*list.Element), order: list.New()}
}

// Limit returns the limit the limiter counts under.
func (l *Limiter) Limit() dbtype.SignInAttemptLimit {
	return l.limit
}

// SetClock replaces the limiter's clock, for tests that move time.
func (l *Limiter) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.now = now
}

// Normalize returns name as the key its attempts are counted under: Unicode NFC and
// case-folded, as the users table normalizes usernames, so a name spelled in another
// case is the same name.
func Normalize(name string) string {
	return cases.Fold().String(norm.NFC.String(name))
}

// The kinds of count the library keeps in the sign-in attempts table beside the names
// people sign in under: the mail an address is sent, the sign-in codes an address is
// sent, the codes sent to accept one invitation, the codes entered to accept one
// invitation, the password reset links an address is sent, and the guesses at the code
// of an account's first password. Each is kept under its kind, a colon, and what it
// counts (OwnKey).
//
// A kind is spelled in capitals, and that is what keeps the counts apart from the
// names: a name is counted under its case-folded form (Normalize), which holds no
// capital, so nothing a person types at a sign-in is ever one of these keys. A stranger
// who fails to sign in under the text of a key counts against a name nobody holds, and
// never closes an address to its mail or an account to its first password.
const (
	KindMail            = "MAIL"
	KindCode            = "CODE"
	KindInvitation      = "INVITATION"
	KindInvitationGuess = "INVITATION-GUESS"
	KindReset           = "RESET"
	KindFirstPassword   = "FIRST-PASSWORD"
)

// OwnKey returns the key of one of the library's own counts: kind, then what the count
// is for, as the caller normalized it.
func OwnKey(kind, of string) string {
	return kind + ":" + of
}

// IsOwnKey reports whether key is one of the library's own counts and no name's.
func IsOwnKey(key string) bool {
	for _, kind := range []string{KindMail, KindCode, KindInvitation, KindInvitationGuess, KindReset, KindFirstPassword} {
		if strings.HasPrefix(key, kind+":") {
			return true
		}
	}

	return false
}

// Attempt is an attempt whose place under the limit was taken before its password was
// compared (Limiter.Take): what the limiter gives back when the password proves right.
type Attempt struct {
	// Lock is how long the name is held now that the attempt was counted: zero unless
	// the attempt is the one that reached the limit.
	Lock time.Duration

	key    string
	client map[string]time.Time
}

// Take takes an attempt on name by client before its password is compared. The attempt
// answered is nil when it was refused, and wait is then how long the caller is to wait;
// otherwise the password may be compared, and a right one hands the attempt to
// Succeeded.
//
// The name's lock answers from memory while a record that holds it is cached (readTTL),
// the client's locks from memory alone, and an attempt refused there asks the store
// nothing. An attempt that goes on is counted first: against the client's keys in the
// step that found them free, and against the name in the store's one transaction, which
// says whether it was counted. Of attempts made at once, as many as the limit are so
// counted and go on, and every other is refused, uncompared; the one that reaches the
// limit locks the name and goes on itself. A refused attempt counts against the client's
// keys and never against the name. err is a store failure, which leaves nothing counted
// against the client.
func (l *Limiter) Take(ctx context.Context, name string, client Client) (taken *Attempt, wait time.Duration, err error) {
	key := Normalize(name)

	l.mu.Lock()
	now := l.now()
	nameWait := l.nameHeld(key, now)
	if clientWait := l.clientHeld(client, now); nameWait > 0 || clientWait > 0 {
		defer l.mu.Unlock()

		return nil, max(nameWait, l.countClient(client, now)), nil
	}
	taken = &Attempt{key: key}
	l.takeClient(taken, client, now)
	l.mu.Unlock()

	record, err := l.store.RecordSignInFailure(ctx, key, l.limit)

	l.mu.Lock()
	defer l.mu.Unlock()

	now = l.now()
	if err != nil {
		l.giveBackClient(taken)

		return nil, 0, errors.Wrap(err, "sessionstorage.AuthStore.RecordSignInFailure()")
	}
	e := l.entry(nameKey(key))
	e.record, e.readAt = record, now
	lock, _ := record.Locked(now)
	if !record.Counted {
		return nil, max(lock, l.clientHeld(client, now)), nil
	}
	taken.Lock = lock

	return taken, 0, nil
}

// Failed records a failed attempt on name by client that took no place beforehand: a
// failure learned after the attempt's own was given back. It is counted in the store
// under the limit and against the client's keys in memory. The record the store answers
// is kept in memory, and lock is how long it now holds the name, zero while it does not.
func (l *Limiter) Failed(ctx context.Context, name string, client Client) (lock time.Duration, err error) {
	key := Normalize(name)
	record, err := l.store.RecordSignInFailure(ctx, key, l.limit)

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.countClient(client, now)
	if err != nil {
		return 0, errors.Wrap(err, "sessionstorage.AuthStore.RecordSignInFailure()")
	}
	e := l.entry(nameKey(key))
	e.record, e.readAt = record, now
	lock, _ = record.Locked(now)

	return lock, nil
}

// Succeeded gives taken back after its password proved right: the name's record is
// deleted in the store, the attempt's own count and every other with it, the failures
// before it and the places of attempts still being compared, the name leaves memory,
// and the client's keys are given the attempt's count back. The clear does not end with
// the request (giveBackTimeout): a person whose request was canceled once their right
// password was compared keeps no count for it. The store is cleared before memory is,
// so a lock another attempt cached while the clear was on its way goes with it; one an
// attempt caches after that, from an answer the store gave before the clear, is trusted
// for readTTL at most, as a lock cleared by another instance is.
func (l *Limiter) Succeeded(ctx context.Context, taken *Attempt) error {
	clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), giveBackTimeout)
	defer cancel()
	err := l.clear(clearCtx, taken.key)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.drop(nameKey(taken.key))
	l.giveBackClient(taken)

	return err
}

// Clear resets name whatever memory holds: an MFA completion or a password change.
func (l *Limiter) Clear(ctx context.Context, name string) error {
	key := Normalize(name)

	l.mu.Lock()
	l.drop(nameKey(key))
	l.mu.Unlock()

	return l.clear(ctx, key)
}

func (l *Limiter) clear(ctx context.Context, key string) error {
	if err := l.store.ClearSignInAttempts(ctx, key); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.ClearSignInAttempts()")
	}

	return nil
}

// nameHeld answers from memory: the wait while the cached record locks the name, zero
// when it does not or no record is cached. A record answers for readTTL from when the
// store answered it: past it the store is asked again, so a lock cleared there is not
// held here.
func (l *Limiter) nameHeld(key string, now time.Time) time.Duration {
	el, found := l.entries[nameKey(key)]
	if !found {
		return 0
	}
	e := valueOf(el)
	l.order.MoveToFront(el)
	if !now.Before(e.readAt.Add(readTTL)) {
		return 0
	}
	wait, _ := e.record.Locked(now)

	return wait
}

// clientHeld is the longest wait the client's keys hold.
func (l *Limiter) clientHeld(client Client, now time.Time) time.Duration {
	var wait time.Duration
	for _, key := range clientKeys(client) {
		if el, found := l.entries[key]; found {
			e := valueOf(el)
			l.order.MoveToFront(el)
			wait = max(wait, e.lockedUntil.Sub(now))
		}
	}

	return max(wait, 0)
}

// countClient counts one attempt against the client's keys, taken, failed or refused,
// and returns the longest wait they then hold: a key in its lock is extended by the
// lock period, one past its window starts a new one, and the attempt that reaches the
// client's limit locks it.
func (l *Limiter) countClient(client Client, now time.Time) time.Duration {
	var wait time.Duration
	for _, key := range clientKeys(client) {
		e := l.entry(key)
		switch {
		case e.lockedUntil.After(now):
			e.lockedUntil = now.Add(l.limit.Lock)
		default:
			if !e.windowEnds.After(now) {
				e.failures, e.windowEnds = 0, now.Add(l.limit.Window)
			}
			e.failures++
			if e.failures >= ClientMultiple*l.limit.Failures {
				e.lockedUntil, e.lockedBy = now.Add(l.limit.Lock), nil
			}
		}
		wait = max(wait, e.lockedUntil.Sub(now))
	}

	return max(wait, 0)
}

// takeClient counts taken against the client's keys before its password is compared,
// and keeps in it the window each key counted it in, for giveBackClient. It is called in
// the step that found none of the keys locked, so of attempts made at once as many as
// the client's limit are counted before a key locks, and the rest find it locked. A key
// the count locks remembers that taken locked it.
func (l *Limiter) takeClient(taken *Attempt, client Client, now time.Time) {
	keys := clientKeys(client)
	if len(keys) == 0 {
		return
	}
	l.countClient(client, now)
	taken.client = make(map[string]time.Time, len(keys))
	for _, key := range keys {
		e := l.entry(key)
		taken.client[key] = e.windowEnds
		if e.lockedUntil.After(now) {
			e.lockedBy = taken
		}
	}
}

// giveBackClient takes the count of taken off the client's keys: its password proved
// right, or the store failed before it was compared. The keys are left as they would be
// had the attempt never been made: a lock its place earned is lifted, however often
// refused attempts extended it meanwhile; a key gives the count back only in the window
// it was counted in; and a lock the remaining count does not earn is lifted. Right
// passwords so leave nothing held against an address people share.
func (l *Limiter) giveBackClient(taken *Attempt) {
	for key, window := range taken.client {
		el, found := l.entries[key]
		if !found {
			continue
		}
		e := valueOf(el)
		if e.lockedBy == taken {
			e.lockedUntil, e.lockedBy = time.Time{}, nil
		}
		if e.failures == 0 || !e.windowEnds.Equal(window) {
			continue
		}
		e.failures--
		if e.failures < ClientMultiple*l.limit.Failures {
			e.lockedUntil, e.lockedBy = time.Time{}, nil
		}
	}
}

// entry returns key's entry, making one when there is none and evicting the least
// recently used entry when the memory is full.
func (l *Limiter) entry(key string) *entry {
	if el, found := l.entries[key]; found {
		l.order.MoveToFront(el)

		return valueOf(el)
	}
	e := &entry{key: key}
	l.entries[key] = l.order.PushFront(e)
	for l.order.Len() > cacheSize {
		oldest := l.order.Back()
		l.order.Remove(oldest)
		delete(l.entries, valueOf(oldest).key)
	}

	return e
}

// valueOf is the entry an element of the order holds.
func valueOf(el *list.Element) *entry {
	e, _ := el.Value.(*entry)

	return e
}

func (l *Limiter) drop(key string) {
	if el, found := l.entries[key]; found {
		l.order.Remove(el)
		delete(l.entries, key)
	}
}

// Keys are a kind and the SHA-256 of the value, so an entry costs the same whatever
// the value's length.
const (
	kindName    = 'n'
	kindAddress = 'a'
	kindCookie  = 'c'
)

func keyOf(kind byte, value string) string {
	sum := sha256.Sum256([]byte(value))

	return string(append([]byte{kind}, sum[:]...))
}

func nameKey(normalized string) string {
	return keyOf(kindName, normalized)
}

func clientKeys(client Client) []string {
	keys := make([]string, 0, 2)
	if client.Address != "" {
		keys = append(keys, keyOf(kindAddress, client.Address))
	}
	if client.Cookie != "" {
		keys = append(keys, keyOf(kindCookie, client.Cookie))
	}

	return keys
}
