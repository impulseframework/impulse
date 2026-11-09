package access

import "context"

// ChangeSignal is the engine's seam for propagating policy-change hints
// between instances. The Client announces after every successful policy write
// and watches for the hints other instances announce; a hint only nudges the
// background loop to re-read the policy store. Correctness never depends on
// it: the heartbeat re-reads the policy store on a fixed interval regardless,
// so a broken signal only costs propagation latency, never accuracy.
//
// The application supplies the implementation as an adapter over its one
// signal channel, through ChangeSignalFunc.
type ChangeSignal interface {
	// Announce broadcasts that policy may have changed. The Client calls it
	// after successful policy writes; failures are reported through the
	// reload error handler and never fail the write.
	Announce(ctx context.Context) error

	// Watch delivers received hints by invoking onChange, blocking until ctx
	// ends or the subscription fails. If Watch returns early the Client
	// re-invokes it after a delay, so implementations may either reconnect
	// internally or simply return on connection loss.
	Watch(ctx context.Context, onChange func()) error
}

// ChangeSignalFunc builds a ChangeSignal from the two functions an application
// writes over its signal channel, so wiring the engine to the channel is one
// line instead of a type. announce publishes the policy kind on the channel.
// watch subscribes to the policy kind, hands every signal to onChange, and
// blocks until ctx ends, which is what the Watch contract asks: the engine
// re-invokes watch after a delay whenever it returns, and the subscription's
// stop runs when watch returns.
//
// Over the resource package's live service, where svc is the application's
// live.Service and resource.KindPolicy is the policy kind:
//
//	policySignal := access.ChangeSignalFunc(
//		func(ctx context.Context) error {
//			return svc.Signal(ctx, resource.KindPolicy)
//		},
//		func(ctx context.Context, onChange func()) error {
//			stop, err := svc.Subscribe(resource.KindPolicy, onChange)
//			if err != nil {
//				return err
//			}
//			defer stop()
//			<-ctx.Done()
//
//			return nil
//		},
//	)
//	client, err := access.New(store, access.WithChangeSignal(policySignal))
//
// Both functions must be set; a nil one panics here, at construction, rather
// than at the first policy write or the first watch.
func ChangeSignalFunc(announce func(ctx context.Context) error, watch func(ctx context.Context, onChange func()) error) ChangeSignal {
	if announce == nil || watch == nil {
		panic("access.ChangeSignalFunc: announce and watch must both be set")
	}

	return &funcSignal{announce: announce, watch: watch}
}

// funcSignal is the ChangeSignal that ChangeSignalFunc builds.
type funcSignal struct {
	announce func(ctx context.Context) error
	watch    func(ctx context.Context, onChange func()) error
}

func (f *funcSignal) Announce(ctx context.Context) error {
	return f.announce(ctx)
}

func (f *funcSignal) Watch(ctx context.Context, onChange func()) error {
	return f.watch(ctx, onChange)
}
