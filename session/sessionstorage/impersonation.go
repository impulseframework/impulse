package sessionstorage

import (
	"context"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/internal/sealed"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/postgres"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/spanner"
	"github.com/impulseframework/impulse/tracer"
)

// ImpersonationTable is the validated impersonation configuration for storage of
// either backend: the name of the impersonation record table. Build it with
// NewImpersonationTable and attach it via WithImpersonation.
//
// The library ships the table's canonical DDL under
// schema/{spanner,postgresql}/impersonation/migrations; the record deliberately has
// no foreign key to the session table, so it outlives the session as evidence.
type ImpersonationTable struct {
	tableName string
}

// NewImpersonationTable validates and builds an impersonation configuration for the
// record table named tableName.
func NewImpersonationTable(tableName string) (*ImpersonationTable, error) {
	if !validIdentifier.MatchString(tableName) {
		return nil, errors.Newf("invalid table name: %s. Table names must start with a letter or underscore, followed by up to 127 letters, numbers, or underscores.", tableName)
	}

	return &ImpersonationTable{tableName: tableName}, nil
}

// TableName returns the impersonation record table name.
func (t *ImpersonationTable) TableName() string {
	return t.tableName
}

type impersonationOption struct {
	tableName string
}

func (o impersonationOption) applySpanner(driver *spanner.SessionStorageDriver) {
	driver.SetImpersonation(&spanner.ImpersonationConfig{TableName: o.tableName})
}

func (o impersonationOption) applyPostgres(driver *postgres.SessionStorageDriver) {
	driver.SetImpersonation(&postgres.ImpersonationConfig{TableName: o.tableName})
}

// WithImpersonation enables impersonated sessions on storage of either backend. The
// record table is LEFT JOINed into every session read, so an impersonated session's
// record reaches the request context together with the session, and it is written
// atomically with the session insert when an impersonated session starts. Without this option
// no session is ever impersonated and the impersonation APIs return a configuration
// error. See the "Impersonated sessions" section of the README.
func WithImpersonation(table *ImpersonationTable) Option {
	return impersonationOption{tableName: table.tableName}
}

// ImpersonationEnabled reports whether an impersonation record table is configured.
func (s *sessionStorage) ImpersonationEnabled() bool {
	return s.db.ImpersonationEnabled()
}

// EndImpersonation records how an impersonated session ended, once: it sets EndedAt
// and EndReason on the session's impersonation record when the record exists and has
// not already ended, and is a no-op otherwise.
func (s *sessionStorage) EndImpersonation(ctx context.Context, sessionID impulse.UUID, reason sessioninfo.ImpersonationEndReason) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := s.db.EndImpersonation(ctx, sessionID, string(reason)); err != nil {
		return errors.Wrap(err, "db.EndImpersonation()")
	}

	return nil
}

// ErrImpersonationSealed is returned for a write of an impersonated session the session
// library did not make: by a store that holds no write (a mock of the store, or a store
// of an application's own, which embeds NoImpersonatedSessions), and by a session the
// library did not build. The storage beneath an impersonated session is the library's
// alone to write.
var ErrImpersonationSealed = sealed.ErrSealed

// impersonatedSessionWrite is the write of an impersonated session a store embeds, under
// a name of this package's. The field it makes in a store is unexported, so a program
// that holds a store can neither take the write out of it nor put another in its place.
type impersonatedSessionWrite = sealed.ImpersonatedSessionWrite

// NoImpersonatedSessions gives a store written outside the session library the one
// method of BaseStore that no package outside the library can declare: the write of an
// impersonated session. The write it gives refuses with ErrImpersonationSealed and
// writes nothing, so the store that embeds it is a session store that starts no
// impersonated session. It is for a test double written by hand, and it is what the
// mocks of the store hold (mock_sessionstorage).
//
// An application's store that wraps one of the library's embeds that store instead, and
// has the library's write by promotion.
type NoImpersonatedSessions struct {
	impersonatedSessionWrite
}

// impersonatedSessions is the storage's insert of an impersonated session and its
// record through its driver: what the write of a store the library made runs
// (newSessionStorage).
type impersonatedSessions struct {
	db db
}

// create creates a new session for the request together with its impersonation record,
// atomically, and returns the session ID. Custom session data follows the session
// type's creation semantics inside the same transaction. The request's Username is
// written as the session's effective identity; no user record is consulted here: the
// session type resolved the identity before calling.
func (i impersonatedSessions) create(ctx context.Context, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	session := &dbtype.InsertSession{
		Username: req.Username,
	}

	id, err := i.db.InsertImpersonatedSession(ctx, session, req, dbtype.NewInsertImpersonation(imp))
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "db.InsertImpersonatedSession()")
	}

	return id, nil
}

// DestroyImpersonatedSessions expires every live impersonated session established by
// actor and ends their records with reason Revoked.
func (s *sessionStorage) DestroyImpersonatedSessions(ctx context.Context, actor string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := s.db.DestroyImpersonatedSessions(ctx, actor); err != nil {
		return errors.Wrap(err, "db.DestroyImpersonatedSessions()")
	}

	return nil
}

// ActiveImpersonations lists the impersonated sessions that are live, newest first:
// record not ended, hard cap not passed, session row not expired, and session activity
// after activeSince; q narrows the listing by actor and/or principal.
func (s *sessionStorage) ActiveImpersonations(ctx context.Context, activeSince time.Time, q *sessioninfo.ImpersonationQuery) ([]*sessioninfo.Impersonation, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	rows, err := s.db.ActiveImpersonations(ctx, activeSince, q)
	if err != nil {
		return nil, errors.Wrap(err, "db.ActiveImpersonations()")
	}

	imps := make([]*sessioninfo.Impersonation, len(rows))
	for i, row := range rows {
		imps[i] = impersonationRecord(ctx, row)
	}

	return imps, nil
}

// impersonationRecord converts a stored row into the record the library works with. A
// row whose mask column is no mask's text is read as keeping nothing
// (dbtype.MaskFromColumn), and the fault is logged here with the session it holds: a
// session that can do nothing because nobody can read its mask says so on the log,
// where each of its denials names only a mask that keeps nothing.
func impersonationRecord(ctx context.Context, row *dbtype.Impersonation) *sessioninfo.Impersonation {
	if err := row.MaskFault(); err != nil {
		logger.FromCtx(ctx).Errorf("impersonation: the mask stored for session %s (%q) does not read, so the session is held to nothing: %v", row.SessionID, *row.Mask, err)
	}

	return row.ToSessionInfo()
}

// DestroyImpersonatedSession expires one live impersonated session and ends its record
// with reason, atomically; a session without a live record is left untouched.
func (s *sessionStorage) DestroyImpersonatedSession(ctx context.Context, sessionID impulse.UUID, reason sessioninfo.ImpersonationEndReason) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := s.db.DestroyImpersonatedSession(ctx, sessionID, string(reason)); err != nil {
		return errors.Wrap(err, "db.DestroyImpersonatedSession()")
	}

	return nil
}
