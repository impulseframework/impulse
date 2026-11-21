// Command sealedwrapper is a program that does not compile, and is kept for that: it is
// an application's store that wraps the session library's, and tries each way such a
// wrapper once had, or might look for, to get between the library and the write beneath
// an impersonated session. While that write was an exported method that took a key of a
// type the library kept to itself, a wrapper declared the method with a type parameter
// where the key went, took the key's type by inference from the library's own store,
// changed the request and the record in flight, and kept the key to write any session
// later. The write is now a method no package outside the session library's directory
// can name, so the wrapper has nothing to take a type from, nothing to call, and nothing
// to replace. The session package's tests type-check the program and pin each refusal
// (TestSealedStorage_wrapperDoesNotCompile).
//
// A wrapper is still a session store: the line in main that hands one over as a store
// is among the lines that compile. What the wrapper declares under the write's name is
// a method of its own, which the library never calls; the session library's end-to-end
// tests run such a wrapper against both databases and see that
// (TestSealedStorage_wrapperCannotReplaceTheWrite).
//
// The program is kept outside the session library's directory on purpose: there an
// application is, and the go command lets no package outside that directory import the
// internal package the write is declared in.
package main

import (
	"context"

	cloudspanner "cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// write is the store's write of an impersonated session as a function, with a type
// parameter where the key went.
type write[K any] func(ctx context.Context, key K, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error)

// keeper embeds the library's store and declares the write itself, with a type
// parameter where the key went: instantiated at the key's type, it was the method the
// library called.
type keeper[K, Tx any] struct {
	sessionstorage.AuthStore[Tx]
	real write[K]
	kept K
}

// CreateImpersonatedSession is the write under the name it had, with whatever the
// library hands it kept and the mask of the record widened in flight.
func (k *keeper[K, Tx]) CreateImpersonatedSession(ctx context.Context, key K, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error) {
	k.kept = key
	imp.Mask = accesstypes.AllowAll()

	return k.real(ctx, key, req, imp)
}

// wrap builds the keeper around the library's store, with the store's own write to pass
// the call on to.
func wrap[Tx, K any](store sessionstorage.AuthStore[Tx], real write[K]) *keeper[K, Tx] {
	return &keeper[K, Tx]{AuthStore: store, real: real}
}

// renamer embeds the library's store and declares the write under the name the library
// calls it by. The method is this package's and not the library's, so it puts nothing
// in the way of the library's own, and it cannot pass the call on: the embedded store's
// method has no name here.
type renamer struct {
	sessionstorage.AuthStore[*cloudspanner.ReadWriteTransaction]
}

// createImpersonatedSession is the write under the name the library calls it by, with
// the mask of the record widened in flight.
func (r *renamer) createImpersonatedSession(ctx context.Context, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error) {
	imp.Mask = accesstypes.AllowAll()

	return r.AuthStore.createImpersonatedSession(ctx, req, imp)
}

func main() {
	var (
		ctx   = context.Background()
		store = sessionstorage.NewSpannerAuth(nil)
		req   = &sessioninfo.NewSessionRequest{}
		imp   = &sessioninfo.Impersonation{}
	)

	// The key's type taken from the store's own write, by inference, and the wrapper
	// handed over as a session store. While the write was an exported method this
	// compiled, and the wrapper's method was the one the library called.
	var kept sessionstorage.AuthStore[*cloudspanner.ReadWriteTransaction] = wrap[*cloudspanner.ReadWriteTransaction](store, store.CreateImpersonatedSession)
	_ = kept
	// A wrapper is a session store, with the library's write by promotion: this compiles.
	var wrapped sessionstorage.AuthStore[*cloudspanner.ReadWriteTransaction] = &renamer{AuthStore: store}
	// The write of the wrapped store under the name the library calls it by, on the
	// store and as the function the method is.
	_, _ = wrapped.createImpersonatedSession(ctx, req, imp)
	_ = sessionstorage.BaseStore.createImpersonatedSession
}
