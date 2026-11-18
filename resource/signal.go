package resource

import "context"

// SignalKind names one kind of shared state an application's instances signal each
// other about: a signal carries nothing but the fact that something of the kind
// changed, and every instance subscribed to the kind rereads what it keeps of it. The
// live service carries the signals (live.Signaler and live.Subscriber over one signals
// document per application, a field per kind), and the kinds are declared here, in the
// package the live package imports, so the live package, the access adapter and the
// tenant roster all name one type without an import cycle; the live package re-exports
// it as live.Kind with the same constants.
type SignalKind string

// The kinds.
const (
	// KindFeatures is a feature flag flip: every FeatureSet following it rereads the
	// FeatureFlags table.
	KindFeatures SignalKind = "features"
	// KindTenants is a change of the tenant roster: an instance holding the roster
	// rereads it.
	KindTenants SignalKind = "tenants"
	// KindPolicy is a change of the permission policy (roles, grants, memberships): an
	// instance holding a policy snapshot rereads it.
	KindPolicy SignalKind = "policy"
)

// Signaler signals every instance of the application that a kind of shared state
// changed: what a flip signals through. Signal returns once the signal is sent, or has
// failed, and leaves nothing to be sent behind it: the caller signals before it
// answers, and an instance that gets no CPU once it has answered would never send what
// was left to a goroutine. The live service implements it (live.Signaler); a failure
// is the caller's to log and never fails the request, since every subscriber also holds
// its copy to its own backstop: no request is answered from a copy older than that,
// signal or no signal.
type Signaler interface {
	Signal(ctx context.Context, kind SignalKind) error
}

// SignalSubscriber delivers the signals of a kind: what a FeatureSet follows. onSignal
// runs on every signal of the kind after the subscription began, on the subscriber's
// goroutine, so it must return quickly; stop ends the subscription. The live service
// implements it (live.Subscriber).
type SignalSubscriber interface {
	Subscribe(kind SignalKind, onSignal func()) (stop func(), err error)
}
