package firestore

import (
	"context"
	"time"

	"github.com/go-playground/errors/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/impulseframework/impulse/resource/live"
)

// DropListener ends the service's current signals listener as Firestore would end it on
// its own, so a test can watch the subscriber reopen it.
func DropListener(s *Service) {
	s.dropListener()
}

// StallFirstSnapshots makes the next n listeners' first snapshots go unseen, as a stream
// that never speaks would leave them, so a test can watch the first snapshot bound
// reopen the listener.
func StallFirstSnapshots(s *Service, n int) {
	s.stallFirstSnapshots(n)
}

// IgnoreListenerSnapshots drops every snapshot the service's listener delivers, so a test
// can watch the reconcile reads wake the subscriptions on their own.
func IgnoreListenerSnapshots(s *Service) {
	s.ignoreListenerSnapshots()
}

// LateReopens is how many listeners the service reopened for a first snapshot that
// never came.
func LateReopens(s *Service) int {
	return s.lateReopenCount()
}

// SignalsIdle reports whether no signal write is in flight or owed for any kind.
func SignalsIdle(s *Service) bool {
	return s.signalsIdle()
}

// SignalsWaiting is how many callers wait for the kind's covering write that is not yet
// due, so a test can tell that the signals it made have all joined it.
func SignalsWaiting(s *Service, kind live.Kind) int {
	return s.signalsWaiting(kind)
}

// ObserveWrites sets the function every signal write is reported to before it is sent,
// with the context it is sent under, so a test can count writes, hold one in flight and
// tell whose it is; what the function returns, when it returns something, is told the
// write's outcome.
func ObserveWrites(s *Service, onWrite func(ctx context.Context, kind live.Kind) (written func(err error))) {
	s.observeWrites(onWrite)
}

// ObserveSnapshots sets the function every snapshot's times are reported to before the
// kinds it advances fire, so a test can explain the wakes it counted.
func ObserveSnapshots(s *Service, onSnapshot func(times map[live.Kind]time.Time)) {
	s.observeSnapshots(onSnapshot)
}

// ReadSignals reads the signals document back with a plain get, outside any listener,
// and returns the times it holds: a test that waited a listener out tells a write that
// never landed (the document lacks the kind) from an update the listener was not
// delivered (the document holds it). A document that does not exist holds no kind.
func ReadSignals(ctx context.Context, s *Service) (map[live.Kind]time.Time, error) {
	snapshot, err := s.signalsDoc().Get(ctx)
	if err != nil && status.Code(err) != codes.NotFound {
		return nil, errors.Wrap(err, "firestore.DocumentRef.Get()")
	}

	return signalTimes(snapshot), nil
}

// ErrSignalLimit is what a Signal fails with when the service's own limit ended it.
var ErrSignalLimit = errSignalLimit

// WithSignalLimit sets the service's own limit on one Signal, so a test that holds a
// write in flight is not ended by it.
func WithSignalLimit(limit time.Duration) Option {
	return withSignalLimit(limit)
}

// WithDialOptions adds gRPC dial options to the connection the service opens to the
// emulator, so a test can count the store's operations through an interceptor.
func WithDialOptions(opts ...grpc.DialOption) Option {
	return withDialOptions(opts...)
}

// StoredSubscriptions counts the principal's documents in the record: how many
// subscriptions the store holds for them.
func StoredSubscriptions(ctx context.Context, s *Service, principal string) (int, error) {
	docs, err := s.subscriptions().Where("principal", "==", principal).Documents(ctx).GetAll()
	if err != nil {
		return 0, errors.Wrap(err, "firestore.DocumentIterator.GetAll()")
	}

	return len(docs), nil
}
