package live

import (
	"context"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// Refusal is a subscription the record did not take, and the record's reason: the
// store judged that one document and wrote the others of its batch.
type Refusal struct {
	Subscription Subscription
	Err          error
}

// SubscriptionRecord is the server-owned record of who is subscribed to what: one
// flat collection no client reads, with a time-to-live on the expiry.
type SubscriptionRecord interface {
	// Register writes the subscriptions with their expiry in one batch; a
	// subscription written again is the same document with the new expiry.
	Register(ctx context.Context, subs []Subscription) error
	// Renew writes the subscriptions a renewal kept with their fresh expiry in one
	// batch, each on its own: a subscription the record does not take is answered in
	// refused with the record's reason and stops none of the others, which are written.
	// err is a failure of the write as a whole, the record out of reach, say, when
	// nothing is known of any of them. The permission re-check is the caller's
	// (RenewHandler); the record writes what it is handed.
	Renew(ctx context.Context, subs []Subscription) (refused []Refusal, err error)
	// Unsubscribe deletes every subscription of the tab.
	Unsubscribe(ctx context.Context, principal, tab string) error
	// UnsubscribeAll deletes every subscription of the principal: the logout path.
	UnsubscribeAll(ctx context.Context, principal string) error
	// SubscribersOfRow lists the live (unexpired) subscriptions to the row.
	SubscribersOfRow(ctx context.Context, res accesstypes.Resource, key string) ([]Subscription, error)
	// SubscribersOfList lists the live subscriptions to the resource's list in the
	// domain (the empty domain for a global resource).
	SubscribersOfList(ctx context.Context, res accesstypes.Resource, domain accesstypes.Domain) ([]Subscription, error)
	// SubscribersOfResource lists every live subscription to the resource, rows and
	// lists in every domain: the bulk path's one lookup.
	SubscribersOfResource(ctx context.Context, res accesstypes.Resource) ([]Subscription, error)
}

// ChangePublisher writes a committed request's changes into its subscribers' change
// sets: for each written row the subscribers of the row get a row document and the
// subscribers of the resource's list in the request's domain a list document, writes to
// one target within one second coalescing; above BulkThreshold written rows of one
// resource, and for a derived resource changed wholesale, every subscriber of the
// resource gets one resource document instead (Fanout). The writes arrive expanded
// (Derivations.Expand): the derived resources a commit reaches are already among them.
// Every document carries the write's id (Writes.ID), the last write's when several
// within one second became one document, and Publish answers the documents it wrote,
// per principal, which is how a write's answer names the ones in the caller's own
// change set. The publisher never checks permission: a subscription was written for a
// permitted request and is re-checked at renewal.
type ChangePublisher interface {
	Publish(ctx context.Context, domain accesstypes.Domain, writes Writes) (Changes, error)
}

// Identity mints and revokes the browser's identity on the change set.
type Identity interface {
	// Token answers how the browser connects as uid: a custom token in production, the
	// emulator host against the emulator.
	Token(ctx context.Context, uid string) (*TokenPayload, error)
	// Revoke ends the browser's identity at logout: the refresh tokens of uid in
	// production, nothing against the emulator.
	Revoke(ctx context.Context, uid string) error
}

// Kind is a signal's kind: which of the application's shared states changed. It is
// resource.SignalKind, declared in the resource package so the resource package's
// consumers (FeatureSet) and the live service name one type; the kinds are re-exported
// here under the same names.
type Kind = resource.SignalKind

// The kinds.
const (
	// KindFeatures is a feature flag flip.
	KindFeatures = resource.KindFeatures
	// KindTenants is a change of the tenant roster.
	KindTenants = resource.KindTenants
	// KindPolicy is a change of the permission policy.
	KindPolicy = resource.KindPolicy
)

// SignalTimeout is the live service's own limit on a signal: a Signal returns within it
// whatever the caller's context allows, so a caller that brings no deadline of its own
// (a write path that signals under its request's context) is not held by a channel that
// does not answer, and no caller has to remember a limit. A caller whose own deadline is
// shorter keeps it.
const SignalTimeout = 2 * time.Second

// Signaler is the application's own channel between its instances: a signal of a
// kind, delivered to every instance subscribed to the kind, carrying nothing but the
// fact that something of the kind changed. The Firestore implementation keeps one
// signals document per application, application/signals, with a field per kind
// holding the time of the last signal and the instance that sent it; a signal of a
// kind writes that kind's field alone, so two kinds never clobber each other, and a
// subscriber served one snapshot after two quick signals of different kinds sees both.
type Signaler interface {
	// Signal signals the kind: every subscription to it, on every instance, runs its
	// onSignal once. It returns once the signal is sent, or has failed, and leaves nothing
	// to be sent behind it: an instance that gets no CPU once its requests have answered
	// would never send what a goroutine was left to. It returns within SignalTimeout, the
	// service's own limit, or sooner where ctx ends sooner: the limit covers the wait and
	// the write together, and a Signal it ends fails with an error that says so. While a
	// write of the kind is in flight, a later Signal of the kind waits for the one write
	// that covers every Signal made meanwhile, which one of their callers makes when the
	// write in flight ends: one write that begins after the last of those calls covers them
	// all, so a burst costs two writes where the channel answers well inside the limit. A
	// covering write that fails is the failure of every caller that waited for it. No
	// caller's context ends another's signal: a caller whose context ends returns, and the
	// write stays with the callers still waiting, each of which makes it in turn where the
	// one before it lost its context, passed its deadline or reached the limit under the
	// write; so no call makes more than one write or waits past its own context or the
	// limit. The error is returned for the caller to log; the caller's contract stays log
	// and never fail the request, since every subscriber also rereads at its own backstop.
	Signal(ctx context.Context, kind Kind) error
}

// Subscriber delivers the signals to this instance's consumers: one subscriber per
// instance holds the one listener on the signals document and runs, for every kind
// whose time advanced since it last looked, the onSignal of each subscription to the
// kind. Subscribe waits on nothing: a subscription made before the listener's first
// snapshot may be woken once for a kind the document already holds and misses no
// signal after it was made; one made after the listener began receives the signals
// after it was made alone; a listener the backend ends on its
// own is logged and reopened with backoff by the subscriber, and on the reopen every
// kind whose time advanced while it was down is signaled once.
type Subscriber interface {
	// Subscribe runs onSignal on every signal of the kind from now on, on the
	// subscriber's goroutine, so it must return quickly (a non-blocking send is the
	// usual shape). Several subscriptions to one kind each run. stop ends this
	// subscription alone.
	Subscribe(kind Kind, onSignal func()) (stop func(), err error)
}

// Service is the live service an application wires: the record, the change publisher,
// the identity, and the signals' signaler and subscriber together. The generated
// handlers draw on it through the application's LiveService accessor, and every
// application wires one: the Firestore service, or the Fake in a test harness.
type Service interface {
	SubscriptionRecord
	ChangePublisher
	Identity
	Signaler
	Subscriber
}
