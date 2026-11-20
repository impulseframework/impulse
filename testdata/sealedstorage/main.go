// Command sealedstorage is a program that does not compile, and is kept for that: it
// calls the write beneath an impersonated session (the store's
// CreateImpersonatedSession) itself, as an application could before the storage was
// sealed, with a principal, a mask and an actor of its own choosing and no declaration
// asked. The session package's tests type-check it and pin each refusal
// (TestSealedStorage_directCallDoesNotCompile).
//
// It is kept outside the session library's directory on purpose. The key the call takes
// lives in an internal package of the session library, which the go command lets the
// library's own directory import and nobody else: a program kept inside that directory
// could import the key, and an application cannot.
package main

import (
	"context"

	"github.com/impulseframework/impulse/session/internal/sealed"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// lookalike has the shape of the library's key and is not the library's key.
type lookalike struct {
	held bool
}

func main() {
	var (
		ctx                             = context.Background()
		store                           = sessionstorage.NewSpannerAuth(nil)
		behind sessionstorage.BaseStore = store
		req                             = &sessioninfo.NewSessionRequest{}
		imp                             = &sessioninfo.Impersonation{}
	)

	// The call as it was written before the storage was sealed.
	_, _ = store.CreateImpersonatedSession(ctx, req, imp)
	// A key of the program's own making.
	_, _ = store.CreateImpersonatedSession(ctx, nil, req, imp)
	_, _ = store.CreateImpersonatedSession(ctx, struct{}{}, req, imp)
	_, _ = store.CreateImpersonatedSession(ctx, lookalike{held: true}, req, imp)
	// The library's own key, which only the library's directory may import.
	_, _ = behind.CreateImpersonatedSession(ctx, sealed.NewImpersonationKey(), req, imp)
}
