// Package sealed holds what the session library alone may reach: the write of an
// impersonated session and its record into the session store. That write takes whatever
// principal, mask and actor it is handed, so a caller who reached it would go around the
// declaration of the impersonation an application offers (session.Impersonations), the
// grant it checked and the mask it gave.
//
// The write is a method with an unexported name, declared here. Go keeps such a method
// to the package that declares it: no other package can call it, and no other package
// can declare it, since a method of the same name declared elsewhere is a different
// method. A type of another package has it only by embedding a type that has it. The go
// command keeps the package itself to the session library's directory: no package
// outside that directory can import a package under its internal directory. So the
// session library's stores have the write, the session library calls it
// (CreateImpersonatedSession), and an application neither calls it, nor implements it,
// nor replaces it on a store it wraps.
//
// What holds this is the type checker. A program that imports unsafe, or names a symbol
// of this package by a go:linkname directive, goes around the type checker here as
// anywhere, and an application owns the tables the write lands in: the seal is on the
// library's API, for code the type checker reads.
package sealed

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// ErrSealed is returned for a write of an impersonated session the session library did
// not make: by a store that holds no write (a mock of the store, or a store of an
// application's own, which embeds sessionstorage.NoImpersonatedSessions), and by a base
// session the library did not build. The storage names it
// sessionstorage.ErrImpersonationSealed.
var ErrSealed = errors.New(
	"the storage beneath an impersonated session is sealed: only a store the session library made writes an impersonated session and its record, " +
		"and only for a session the library built. A mock of the store, and a store that embeds sessionstorage.NoImpersonatedSessions, write none. " +
		"Declare the kinds of impersonation the application offers (session.Impersonations) and start one with the session's StartImpersonatedSession, " +
		"over a store the library made (sessionstorage.NewSpannerAuth, NewPostgresAuth, NewSpannerPreauth or NewPostgresPreauth) or a store that embeds one",
)

// ImpersonatedSessionWriter is the part of a session store that writes an impersonated
// session with its record. The store's interface embeds it (sessionstorage.BaseStore),
// so every store has the method, and no package but this one can call it or declare it.
type ImpersonatedSessionWriter interface {
	// createImpersonatedSession creates a new session for the request together with its
	// impersonation record, atomically, and returns the session ID.
	createImpersonatedSession(ctx context.Context, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error)
}

// Insert is a store's insert of an impersonated session and its record: what the
// store's ImpersonatedSessionWrite runs.
type Insert func(ctx context.Context, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error)

// ImpersonatedSessionWrite implements ImpersonatedSessionWriter for the store that
// embeds it. The session library's stores embed one built over their insert
// (NewImpersonatedSessionWrite). The zero value writes nothing and refuses with
// ErrSealed: it is what a mock of the store holds, and a store of an application's own
// (sessionstorage.NoImpersonatedSessions).
type ImpersonatedSessionWrite struct {
	insert Insert
}

// NewImpersonatedSessionWrite returns the write that runs insert. Only a package in the
// session library's directory can call it, so only the library decides what a write of
// an impersonated session does.
func NewImpersonatedSessionWrite(insert Insert) ImpersonatedSessionWrite {
	return ImpersonatedSessionWrite{insert: insert}
}

// createImpersonatedSession runs the store's insert. A write nobody built (the zero
// value) refuses with ErrSealed, with nothing written.
func (w ImpersonatedSessionWrite) createImpersonatedSession(ctx context.Context, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error) {
	if w.insert == nil {
		return impulse.NilUUID, ErrSealed
	}

	return w.insert(ctx, req, imp)
}

// CreateImpersonatedSession has store write an impersonated session for the request
// together with its record, and returns the session ID. It is how the session library
// reaches the write (basesession.BaseSession.StartImpersonatedSession, from a request
// the application's declaration made), and the only way anything does: the method it
// calls has no name outside this package.
func CreateImpersonatedSession(ctx context.Context, store ImpersonatedSessionWriter, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error) {
	id, err := store.createImpersonatedSession(ctx, req, imp)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "sealed.ImpersonatedSessionWriter.createImpersonatedSession()")
	}

	return id, nil
}
