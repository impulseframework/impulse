// Package sealed holds what the session library alone may reach: the write of an
// impersonated session and its record into the session store, and the reads that decide
// one.
//
// The write takes whatever principal, mask and actor it is handed, so a caller who
// reached it would go around the declaration of the impersonation an application offers
// (session.Impersonations), the grant it checked and the mask it gave. The reads are of
// the actor's own session when one starts, of the account acted as, and of the session
// on every later request: a store that answered them with rows of its own choosing would
// change who is recorded as the actor, whom the session acts as, what its mask keeps and
// whether it has ended.
//
// Each is a method with an unexported name, declared here. Go keeps such a method to the
// package that declares it: no other package can call it, and no other package can
// declare it, since a method of the same name declared elsewhere is a different method.
// A type of another package has it only by embedding a type that has it. The go command
// keeps the package itself to the session library's directory: no package outside that
// directory can import a package under its internal directory. So the session library's
// stores have the write and the reads, the session library calls them
// (CreateImpersonatedSession, Session, User, UserByUserName), and an application neither
// calls them, nor implements them, nor replaces them on a store it wraps: a wrapper's
// own Session and User are never asked what an impersonated session is.
//
// What holds this is the type checker. These remain outside it:
//   - An application owns the tables the write lands in. SQL on them changes any row,
//     and the resolver of custom session data an application registers
//     (sessionstorage.NewCustomSessionData) runs inside the write's own transaction,
//     once the session and its record are written there, handed the request and the
//     transaction: what it changes in the request reaches no row the library writes,
//     and what it can do to the session and its record is a write of its own on those
//     tables.
//   - A store that wraps the library's still answers for an ordinary session (its
//     Session is asked for every session the library's own read does not find
//     impersonated; it may not answer an impersonated one, and its answer is held to
//     the session asked for: that session's id, and, where the library's own store
//     holds the session, that row's account and username), and still carries the
//     writes that end or extend an impersonated session (DestroySession,
//     DestroyImpersonatedSession, DestroyImpersonatedSessions, EndImpersonation,
//     UpdateSessionActivity) and the listing of the live ones (ActiveImpersonations). A
//     wrapper that drops one of those writes leaves the session to its idle timeout and
//     its hard cap, both of which the library reads from its own store.
//   - A program that imports unsafe, or brings assembly or C of its own, goes around
//     the type checker here as anywhere. The plainest form is a go:linkname directive,
//     which links to a function or a variable of this package by its symbol's name, and
//     the linker accepts it. impulse check refuses the directive in an application's
//     tree (the check framework-linkname) and nothing else of this kind: unsafe reaches
//     the memory of a value a program holds with no directive.
//   - A module whose path is under the library's import path is, to the go command,
//     inside the library's directory, wherever a replace or a workspace finds its code,
//     and its packages may import this one. impulse check refuses such a module in an
//     application's go.mod and in a go.work at its root (the check framework-modules).
//   - An application that builds against a copy of the library it changed, through a
//     replace of the library's module or a vendor directory edited by hand, builds
//     another library. impulse check warns of the replace (the check pins).
//
// The two checks read the application's own tree: its Go files, its go.mod and a
// go.work at its root. The code of the modules it requires, and a build told to read
// another module file, are read by neither.
//
// The seal is on the library's API, for code the type checker reads.
package sealed

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// ErrSealed is returned for a write of an impersonated session the session library did
// not make, and for a read that decides one: by a store that holds no write and no read
// of the library's making (a mock of the store, or a store of an application's own,
// which embeds sessionstorage.NoImpersonatedSessions), and by a base session the library
// did not build. The storage names it sessionstorage.ErrImpersonationSealed.
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

// SessionReader is the part of a session store that reads a session as the library's
// own store holds it. The store's interface embeds it (sessionstorage.BaseStore), so
// every store has the methods, and no package but this one can call them or declare
// them. It is how the library reads what decides an impersonated session: the actor's
// own session when one starts, and the session of every later request. A store that
// wraps the library's has the read by promotion and cannot replace it, so the wrapper's
// exported Session is never asked whether a session is impersonated, what its mask
// keeps, or whether it has ended.
type SessionReader interface {
	// readSession returns the session the library's own store holds under sessionID.
	readSession(ctx context.Context, sessionID impulse.UUID) (*sessioninfo.SessionData, error)
	// sessionReadOwner returns the store the read was made for: the library's own
	// store, and nil for a read nobody built.
	sessionReadOwner() any
}

// ReadSession is a store's read of one session: what the store's SessionRead runs.
type ReadSession func(ctx context.Context, sessionID impulse.UUID) (*sessioninfo.SessionData, error)

// SessionRead implements SessionReader for the store that embeds it. The session
// library's stores embed one built over their own read (NewSessionRead). The zero value
// reads nothing and answers ErrSealed: it is what a mock of the store holds, and a store
// of an application's own (sessionstorage.NoImpersonatedSessions).
type SessionRead struct {
	read  ReadSession
	owner any
}

// NewSessionRead returns the read that runs read, made for owner: the store whose
// exported Session is that same read, as a pointer. Only a package in the session
// library's directory can call it.
func NewSessionRead(read ReadSession, owner any) SessionRead {
	return SessionRead{read: read, owner: owner}
}

// readSession runs the store's read. A read nobody built (the zero value) answers
// ErrSealed.
func (r SessionRead) readSession(ctx context.Context, sessionID impulse.UUID) (*sessioninfo.SessionData, error) {
	if r.read == nil {
		return nil, ErrSealed
	}

	return r.read(ctx, sessionID)
}

// sessionReadOwner returns the store the read was made for.
func (r SessionRead) sessionReadOwner() any {
	return r.owner
}

// Session returns the session the library's own store holds under sessionID, whatever
// the store handed in answers from its exported Session. The store's error comes back
// as it is, so a caller can tell a session the store does not hold (a not-found error)
// from a store that holds no read of the library's making (ErrSealed) and from a read
// that failed.
func Session(ctx context.Context, store SessionReader, sessionID impulse.UUID) (*sessioninfo.SessionData, error) {
	return store.readSession(ctx, sessionID)
}

// SessionIsOwn reports whether store is the very store its read was made for: one the
// library made, handed over as it is, whose exported Session is the same read. It is
// false for a store that wraps one (the wrapper has the read by promotion, and is not
// the store the read names), and for a store that holds no read at all. The library
// asks it to read an unwrapped store's session once.
func SessionIsOwn(store SessionReader) bool {
	owner := store.sessionReadOwner()

	return owner != nil && owner == any(store)
}

// UserReader is the part of a store with users that reads an account as the library's
// own store holds it. The store's interface embeds it (sessionstorage.UserStore). It is
// how the library reads the account an impersonated session acts as, and whether an
// actor's name is an account's: a wrapper's exported User and UserByUserName are never
// asked either.
type UserReader interface {
	// readUser returns the account the library's own store holds under id.
	readUser(ctx context.Context, id impulse.UUID) (*dbtype.SessionUser, error)
	// readUserByUserName returns the account the library's own store holds under
	// username.
	readUserByUserName(ctx context.Context, username string) (*dbtype.SessionUser, error)
}

// UserRead implements UserReader for the store that embeds it. The session library's
// stores with users embed one built over their own reads (NewUserRead). The zero value
// reads nothing and answers ErrSealed.
type UserRead struct {
	byID   func(ctx context.Context, id impulse.UUID) (*dbtype.SessionUser, error)
	byName func(ctx context.Context, username string) (*dbtype.SessionUser, error)
}

// NewUserRead returns the reads that run byID and byName. Only a package in the session
// library's directory can call it.
func NewUserRead(
	byID func(ctx context.Context, id impulse.UUID) (*dbtype.SessionUser, error),
	byName func(ctx context.Context, username string) (*dbtype.SessionUser, error),
) UserRead {
	return UserRead{byID: byID, byName: byName}
}

// readUser runs the store's read of an account by id. A read nobody built (the zero
// value) answers ErrSealed.
func (r UserRead) readUser(ctx context.Context, id impulse.UUID) (*dbtype.SessionUser, error) {
	if r.byID == nil {
		return nil, ErrSealed
	}

	return r.byID(ctx, id)
}

// readUserByUserName runs the store's read of an account by username. A read nobody
// built (the zero value) answers ErrSealed.
func (r UserRead) readUserByUserName(ctx context.Context, username string) (*dbtype.SessionUser, error) {
	if r.byName == nil {
		return nil, ErrSealed
	}

	return r.byName(ctx, username)
}

// User returns the account the library's own store holds under id, whatever the store
// handed in answers from its exported User. The store's error comes back as it is.
func User(ctx context.Context, store UserReader, id impulse.UUID) (*dbtype.SessionUser, error) {
	return store.readUser(ctx, id)
}

// UserByUserName returns the account the library's own store holds under username,
// whatever the store handed in answers from its exported UserByUserName. The store's
// error comes back as it is.
func UserByUserName(ctx context.Context, store UserReader, username string) (*dbtype.SessionUser, error) {
	return store.readUserByUserName(ctx, username)
}
