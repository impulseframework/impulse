// Command sealedfromnothing is a program that does not compile, and is kept for that: it
// is a session store an application writes from nothing, with no store of the session
// library's inside it. While the write beneath an impersonated session was an exported
// method that took a key of a type the library kept to itself, such a store declared the
// method with a type parameter where the key went and took the key's type from the
// method of the library's own interface, by inference: the library then handed it a key,
// and with the key any session could be written into the library's store later. The
// write is now a method no package outside the session library's directory can name or
// declare, so a store written from nothing is no session store, whatever it declares. A
// test double written from nothing embeds sessionstorage.NoImpersonatedSessions, whose
// write refuses.
// The session package's tests type-check the program and pin each refusal
// (TestSealedStorage_storeFromNothingDoesNotCompile).
//
// It is kept outside the session library's directory on purpose: there an application
// is, and the go command lets no package outside that directory import the internal
// package the write is declared in.
package main

import (
	"context"
	"reflect"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// mine is a store written from nothing: every method the store's interface exports, and
// the write under the name it had, with a type parameter where the key went.
type mine[K any] struct {
	kept K
}

// CreateImpersonatedSession is the write under the name it had: it keeps whatever the
// library hands it.
func (s *mine[K]) CreateImpersonatedSession(_ context.Context, key K, _ *sessioninfo.NewSessionRequest, _ *sessioninfo.Impersonation) (impulse.UUID, error) {
	s.kept = key

	return impulse.NilUUID, nil
}

// lookalike is the same store with the write declared under the name the library calls
// it by: a method of this package, and not the one the store's interface asks for.
type lookalike struct {
	*mine[struct{}]
}

// createImpersonatedSession is the write under the name the library calls it by,
// declared here.
func (s *lookalike) createImpersonatedSession(context.Context, *sessioninfo.NewSessionRequest, *sessioninfo.Impersonation) (impulse.UUID, error) {
	return impulse.NilUUID, nil
}

func (s *mine[K]) Session(context.Context, impulse.UUID) (*sessioninfo.SessionData, error) {
	return nil, nil
}

func (s *mine[K]) UpdateCustomSessionData(context.Context, impulse.UUID, func(data any) error) error {
	return nil
}

func (s *mine[K]) CustomDataType() reflect.Type {
	return nil
}

func (s *mine[K]) CustomUserDataType() reflect.Type {
	return nil
}

func (s *mine[K]) UpdateSessionActivity(context.Context, impulse.UUID) error {
	return nil
}

func (s *mine[K]) DestroySession(context.Context, impulse.UUID) error {
	return nil
}

func (s *mine[K]) ImpersonationEnabled() bool {
	return true
}

func (s *mine[K]) EndImpersonation(context.Context, impulse.UUID, sessioninfo.ImpersonationEndReason) error {
	return nil
}

func (s *mine[K]) DestroyImpersonatedSessions(context.Context, string) error {
	return nil
}

func (s *mine[K]) ActiveImpersonations(context.Context, time.Time, *sessioninfo.ImpersonationQuery) ([]*sessioninfo.Impersonation, error) {
	return nil, nil
}

func (s *mine[K]) DestroyImpersonatedSession(context.Context, impulse.UUID, sessioninfo.ImpersonationEndReason) error {
	return nil
}

func (s *mine[K]) SetSessionTableName(string) error {
	return nil
}

func (s *mine[K]) SetUserTableName(string) error {
	return nil
}

func (s *mine[K]) NewSession(context.Context, string, any) (impulse.UUID, error) {
	return impulse.NilUUID, nil
}

func (s *mine[K]) DestroyAllUserSessions(context.Context, string) error {
	return nil
}

// build makes the store with its key's type taken from the write it is handed: the
// method of the library's own interface, as a function.
func build[K any](func(sessionstorage.BaseStore, context.Context, K, *sessioninfo.NewSessionRequest, *sessioninfo.Impersonation) (impulse.UUID, error)) *mine[K] {
	return &mine[K]{}
}

func main() {
	// The key's type taken from the interface's own method, by inference, and the store
	// handed to the session library as its storage. While the write was an exported
	// method this compiled, and the library handed the store its key.
	_, _ = session.NewPreauth[session.NoCustomData](build(sessionstorage.BaseStore.CreateImpersonatedSession), "")
	// The store handed to the session library with the type parameter given a type of
	// the program's own: a store that exports every method the interface exports.
	_, _ = session.NewPreauth[session.NoCustomData](&mine[struct{}]{}, "")
	// The same, with the write declared under the name the library calls it by.
	_, _ = session.NewPreauth[session.NoCustomData](&lookalike{}, "")
}
