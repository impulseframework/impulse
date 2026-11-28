// Command sealedstorage is a program that does not compile, and is kept for that: it
// tries to make the write beneath an impersonated session itself, as an application
// could before the storage was sealed, with a principal, a mask and an actor of its own
// choosing and no declaration asked. The write is a method of the store whose name
// belongs to an internal package of the session library, so the program finds it under
// no name: not the one it had, not the one the library calls it by, and not through the
// library's own way to it. The session package's tests type-check the program and pin
// each refusal (TestSealedStorage_directCallDoesNotCompile).
//
// It is kept outside the session library's directory on purpose: there an application
// is, and the go command lets no package outside that directory import the internal
// package the write is declared in.
package main

import (
	"context"

	"github.com/impulseframework/impulse/session/internal/sealed"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

func main() {
	var (
		ctx                             = context.Background()
		store                           = sessionstorage.NewSpannerAuth(nil)
		behind sessionstorage.BaseStore = store
		req                             = &sessioninfo.NewSessionRequest{}
		imp                             = &sessioninfo.Impersonation{}
	)

	// The call as it was written before the storage was sealed, on the library's store
	// and on the store's interface.
	_, _ = store.CreateImpersonatedSession(ctx, req, imp)
	_, _ = behind.CreateImpersonatedSession(ctx, req, imp)
	// The write under the name the session library calls it by.
	_, _ = store.createImpersonatedSession(ctx, req, imp)
	_, _ = behind.createImpersonatedSession(ctx, req, imp)
	// The write of the store an application writes for itself, which would refuse.
	_, _ = sessionstorage.NoImpersonatedSessions{}.createImpersonatedSession(ctx, req, imp)
	// The session library's own way to the write, which only the library's directory may
	// import.
	_, _ = sealed.CreateImpersonatedSession(ctx, behind, req, imp)
}
