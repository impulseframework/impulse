package postgres

import (
	"context"
	"fmt"

	"github.com/georgysavva/scany/v2/pgxscan"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The sign-in flow (internal/signinflow) drives the primitives below inside the
// transactions Transact opens; the SQL of a sign-in lives here, its sequence there.

// identityColumns are the identities table's columns.
const identityColumns = `"Id", "UserId", "Method", "Connection", "Subject", "Tenant", "EmailAtLink", "CreatedAt", "LastUsedAt"`

func (s *SessionStorageDriver) identitiesTable() string {
	return pgx.Identifier{s.identitiesTableName}.Sanitize()
}

// Transact runs fn in a transaction and commits when fn returns nil.
func (s *SessionStorageDriver) Transact(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	txn, err := s.conn.Begin(ctx)
	if err != nil {
		return errors.Wrap(err, "Queryer.Begin()")
	}
	defer func() {
		_ = txn.Rollback(ctx)
	}()

	if err := fn(ctx, txn); err != nil {
		return err
	}
	if err := txn.Commit(ctx); err != nil {
		return errors.Wrap(err, "pgx.Tx.Commit()")
	}

	return nil
}

// SignInConflict classifies a unique violation a sign-in's transaction ended with: the
// user table's username index, or the identities table's key.
func (s *SessionStorageDriver) SignInConflict(err error) dbtype.SignInConflict {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return dbtype.NoSignInConflict
	}
	if pgErr.Code == pgerrcode.DeadlockDetected || pgErr.Code == pgerrcode.SerializationFailure {
		return dbtype.LockConflict
	}
	if pgErr.Code != pgerrcode.UniqueViolation {
		return dbtype.NoSignInConflict
	}

	switch {
	case pgErr.ConstraintName == s.usernameIndex():
		return dbtype.UsernameConflict
	case pgErr.TableName == s.identitiesTableName:
		return dbtype.IdentityConflict
	default:
		return dbtype.NoSignInConflict
	}
}

// IdentityIn returns the link for (method, connection, subject) inside tx, or nil.
func (s *SessionStorageDriver) IdentityIn(ctx context.Context, tx pgx.Tx, method sessioninfo.AuthMethod, connection, subject string) (*dbtype.SessionIdentity, error) {
	return s.lookupIdentity(ctx, tx, method, connection, subject)
}

// lookupIdentity returns the link for (method, connection, subject), or nil.
func (s *SessionStorageDriver) lookupIdentity(ctx context.Context, q pgxscan.Querier, method sessioninfo.AuthMethod, connection, subject string) (*dbtype.SessionIdentity, error) {
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE "Method" = $1 AND "Connection" = $2 AND "Subject" = $3`, identityColumns, s.identitiesTable())

	var identities []*dbtype.SessionIdentity
	if err := pgxscan.Select(ctx, q, &identities, query, string(method), connection, subject); err != nil {
		return nil, errors.Wrap(err, "pgxscan.Select()")
	}
	if len(identities) == 0 {
		return nil, nil
	}

	return identities[0], nil
}

// TouchIdentityIn moves the link's LastUsedAt to the database's time inside tx.
func (s *SessionStorageDriver) TouchIdentityIn(ctx context.Context, tx pgx.Tx, linkID impulse.UUID) error {
	touch := fmt.Sprintf(`UPDATE %s SET "LastUsedAt" = now() WHERE "Id" = $1`, s.identitiesTable())
	if _, err := tx.Exec(ctx, touch, linkID); err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec()")
	}

	return nil
}

// LinkIdentityIn writes a new link of identity to userID inside tx.
func (s *SessionStorageDriver) LinkIdentityIn(ctx context.Context, tx pgx.Tx, userID impulse.UUID, identity *sessioninfo.Identity, tenant string) error {
	if _, err := s.insertLink(ctx, tx, userID, identity, tenant); err != nil {
		return err
	}

	return nil
}

// insertLink writes a new link of identity to userID through q, created and last used
// at the database's time, and returns it with those times.
func (s *SessionStorageDriver) insertLink(
	ctx context.Context, q interface {
		QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	}, userID impulse.UUID, identity *sessioninfo.Identity, tenant string,
) (*dbtype.SessionIdentity, error) {
	if !dbtype.IsExternal(identity) || identity.Subject == "" {
		return nil, httpio.NewBadRequestMessage("only an external identity with a subject can be linked")
	}

	id, err := impulse.NewUUID()
	if err != nil {
		return nil, errors.Wrap(err, "impulse.NewUUID()")
	}
	link := &dbtype.SessionIdentity{
		ID: id, UserID: userID, Method: identity.Method, Connection: identity.Connection, Subject: identity.Subject,
		Tenant: dbtype.OptionalString(tenant), EmailAtLink: dbtype.OptionalString(identity.Email),
	}

	query := fmt.Sprintf(`INSERT INTO %s (%s) VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now()) RETURNING "CreatedAt", "LastUsedAt"`, s.identitiesTable(), identityColumns)
	err = q.QueryRow(ctx, query, link.ID, link.UserID, string(link.Method), link.Connection, link.Subject, link.Tenant, link.EmailAtLink).Scan(&link.CreatedAt, &link.LastUsedAt)
	if err != nil {
		return nil, errors.Wrap(err, "QueryRow().Scan()")
	}

	return link, nil
}

// UserIn reads the part of userID's user the sign-in decides on, inside tx, sharing its
// row lock so a concurrent change waits. A missing user is NotFound.
func (s *SessionStorageDriver) UserIn(ctx context.Context, tx pgx.Tx, userID impulse.UUID) (*dbtype.User, error) {
	return s.userIn(ctx, tx, userID, "FOR SHARE")
}

// linkDecisionLockClass is the first key of the advisory lock a link decision takes,
// keeping it apart from any advisory lock the application takes on its own.
const linkDecisionLockClass = "session.link-decision"

// LockUserIn reads the user as UserIn does and holds a transaction-scoped advisory lock
// keyed by the user until tx ends, so two link decisions for one user run one after the
// other and the second sees the first's link. An advisory lock rather than a stronger
// row lock: upgrading the row lock deadlocks with a resolver that shared the row before
// the decision, and twice in a row under load, while the advisory lock is independent
// of row locks, so a resolver's own share lock and the key-share lock a session insert
// takes through its foreign key go through.
func (s *SessionStorageDriver) LockUserIn(ctx context.Context, tx pgx.Tx, userID impulse.UUID) (*dbtype.User, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, linkDecisionLockClass, userID.String()); err != nil {
		return nil, errors.Wrap(err, "pgx.Tx.Exec()")
	}

	return s.userIn(ctx, tx, userID, "FOR SHARE")
}

// userIn reads the user with the row lock clause lock, and the proof the row's password
// hash answers to: under the share lock no password write lands until tx ends, so what
// tx inserts on the strength of a password proof is checked against the hash the row
// holds when it commits.
func (s *SessionStorageDriver) userIn(ctx context.Context, tx pgx.Tx, userID impulse.UUID, lock string) (*dbtype.User, error) {
	query := fmt.Sprintf(`SELECT "Id", "Username", "PasswordHash", "Disabled" FROM %s WHERE "Id" = $1 %s`, s.userTable(), lock)

	var (
		u    dbtype.User
		hash *string
	)
	if err := tx.QueryRow(ctx, query, userID).Scan(&u.ID, &u.Username, &hash, &u.Disabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpio.NewNotFoundMessagef("user id %q does not exist", userID)
		}

		return nil, errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}
	if hash != nil {
		u.HasPassword, u.PasswordProof = true, dbtype.ProofOfStored(*hash)
	}

	return &u, nil
}

// UserByUsernameIn reads the user named username inside tx, matched on the normalized
// username as UserByUserName matches it, or nil when there is none.
func (s *SessionStorageDriver) UserByUsernameIn(ctx context.Context, tx pgx.Tx, username string) (*dbtype.User, error) {
	query := fmt.Sprintf(`SELECT "Id", "Username", "PasswordHash" IS NOT NULL, "Disabled" FROM %s WHERE "NormalizedUsername" = casefold(normalize($1))`, s.userTable())

	var u dbtype.User
	if err := tx.QueryRow(ctx, query, username).Scan(&u.ID, &u.Username, &u.HasPassword, &u.Disabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}

	return &u, nil
}

// InsertUserIn writes a new user row inside tx and returns its ID: the one the insert
// names, or a generated one where it names none.
func (s *SessionStorageDriver) InsertUserIn(ctx context.Context, tx pgx.Tx, user *dbtype.InsertSessionUser) (impulse.UUID, error) {
	id := user.ID.UUID
	if !user.ID.Valid {
		var err error
		if id, err = impulse.NewUUID(); err != nil {
			return impulse.NilUUID, errors.Wrap(err, "impulse.NewUUID()")
		}
	}
	query, args := s.userInsertStatement(id, user)
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return impulse.NilUUID, errors.Wrap(err, "pgx.Tx.Exec()")
	}

	return id, nil
}

// InsertSessionIn inserts the session row, its auth events and its custom session data
// for a resolved request, inside tx.
func (s *SessionStorageDriver) InsertSessionIn(ctx context.Context, tx pgx.Tx, id impulse.UUID, insertSession *dbtype.InsertSession, req *sessioninfo.NewSessionRequest) error {
	if req.CustomData != nil && s.customData == nil {
		return errors.New("custom session data provided but no custom session data config is attached")
	}

	row := *insertSession
	row.Username = req.Username
	query, args := s.sessionInsertStatement(id, &row, req)
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec()")
	}
	if insertEvent := s.initialAuthEvent(id, req, nil, nil); insertEvent != nil {
		if err := insertEvent(ctx, tx); err != nil {
			return err
		}
	}

	data := req.CustomData
	if data == nil && s.customData != nil && s.customData.Resolver != nil {
		var err error
		data, err = s.customData.Resolver(ctx, tx, req)
		if err != nil {
			return errors.Wrap(err, "CustomSessionDataConfig.Resolver()")
		}
	}
	if data != nil {
		if err := s.insertCustomSessionData(ctx, tx, id, data, false); err != nil {
			return err
		}
	}

	return nil
}

// Identity returns the link for (method, connection, subject), or a NotFound error.
func (s *SessionStorageDriver) Identity(ctx context.Context, method sessioninfo.AuthMethod, connection, subject string) (*dbtype.SessionIdentity, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	link, err := s.lookupIdentity(ctx, s.conn, method, connection, subject)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, httpio.NewNotFoundMessagef("identity (%s, %q, %q) is not linked", method, connection, subject)
	}

	return link, nil
}

// IdentitiesByUser lists a user's links, oldest first.
func (s *SessionStorageDriver) IdentitiesByUser(ctx context.Context, userID impulse.UUID) ([]*dbtype.SessionIdentity, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return s.identitiesByUser(ctx, s.conn, userID)
}

// IdentitiesByUserIn lists a user's links inside tx, oldest first.
func (s *SessionStorageDriver) IdentitiesByUserIn(ctx context.Context, tx pgx.Tx, userID impulse.UUID) ([]*dbtype.SessionIdentity, error) {
	return s.identitiesByUser(ctx, tx, userID)
}

// identitiesByUser lists a user's links through q, oldest first.
func (s *SessionStorageDriver) identitiesByUser(ctx context.Context, q pgxscan.Querier, userID impulse.UUID) ([]*dbtype.SessionIdentity, error) {
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE "UserId" = $1 ORDER BY "CreatedAt", "Id"`, identityColumns, s.identitiesTable())
	var identities []*dbtype.SessionIdentity
	if err := pgxscan.Select(ctx, q, &identities, query, userID); err != nil {
		return nil, errors.Wrap(err, "pgxscan.Select()")
	}

	return identities, nil
}

// LinkIdentity links identity to userID outside the sign-in flow. An identity that is
// already linked is a Conflict.
func (s *SessionStorageDriver) LinkIdentity(ctx context.Context, userID impulse.UUID, identity *sessioninfo.Identity, tenant string) (*dbtype.SessionIdentity, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	link, err := s.insertLink(ctx, s.conn, userID, identity, tenant)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return nil, httpio.NewConflictMessagef("identity (%s, %q, %q) is already linked", identity.Method, identity.Connection, identity.Subject)
		}

		return nil, err
	}

	return link, nil
}

// UnlinkIdentity removes a link, refusing to remove the last means of sign-in of an
// user that has no password.
func (s *SessionStorageDriver) UnlinkIdentity(ctx context.Context, identityID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	txn, err := s.conn.Begin(ctx)
	if err != nil {
		return errors.Wrap(err, "Queryer.Begin()")
	}
	defer func() {
		_ = txn.Rollback(ctx)
	}()

	var userID impulse.UUID
	if err := txn.QueryRow(ctx, fmt.Sprintf(`SELECT "UserId" FROM %s WHERE "Id" = $1`, s.identitiesTable()), identityID).Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpio.NewNotFoundMessagef("identity %q does not exist", identityID)
		}

		return errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}

	// The user row is locked, so concurrent unlinks of its identities run one at a
	// time and can't both leave it without a means of sign-in.
	var hasPassword bool
	lockUser := fmt.Sprintf(`SELECT "PasswordHash" IS NOT NULL FROM %s WHERE "Id" = $1 FOR UPDATE`, s.userTable())
	if err := txn.QueryRow(ctx, lockUser, userID).Scan(&hasPassword); err != nil {
		return errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}
	if !hasPassword {
		var links int
		if err := txn.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE "UserId" = $1`, s.identitiesTable()), userID).Scan(&links); err != nil {
			return errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
		}
		if links <= 1 {
			return httpio.NewConflictMessageWithError(dbtype.ErrLastSignInMethod, "the user has no other means of sign-in")
		}
	}

	if _, err := txn.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE "Id" = $1`, s.identitiesTable()), identityID); err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec()")
	}

	if err := txn.Commit(ctx); err != nil {
		return errors.Wrap(err, "pgx.Tx.Commit()")
	}

	return nil
}

// RemoveUserPassword removes a user's password, and with it the mark that the user must
// change it, while one of the identities standing names is still linked to the user,
// and refuses to remove the user's last means of sign-in otherwise. The user's sessions
// stand. A user that holds no password is a BadRequest. It follows no proof of the
// password and removes whatever hash the row holds.
func (s *SessionStorageDriver) RemoveUserPassword(ctx context.Context, userID impulse.UUID, standing []impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if !s.users {
		return errors.New("RemoveUserPassword needs the auth schema: use Auth storage")
	}

	return s.removeUserPassword(ctx, userID, false, nil, standing)
}

// RemoveProvenUserPassword removes the password a user proved (proven), as
// RemoveUserPassword removes one. The hash is compared in the database under the lock
// the removal holds on the user row, so the removal goes through only while the row
// still holds it: a password changed, reset or removed since the caller read it stands
// as it is (ErrPasswordChanged, a Conflict).
func (s *SessionStorageDriver) RemoveProvenUserPassword(ctx context.Context, userID impulse.UUID, proven *securehash.Hash, standing []impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if !s.users {
		return errors.New("RemoveProvenUserPassword needs the auth schema: use Auth storage")
	}

	return s.removeUserPassword(ctx, userID, true, proven, standing)
}

// removeUserPassword removes a user's password and its change mark in one transaction.
// With followsProof set, the row must still hold proven, the hash the caller proved.
// standing names the identities the caller judged a means of sign-in: one of them must
// still be linked to the user when the password goes, counted under the lock an unlink
// takes too.
func (s *SessionStorageDriver) removeUserPassword(ctx context.Context, userID impulse.UUID, followsProof bool, proven *securehash.Hash, standing []impulse.UUID) error {
	txn, err := s.conn.Begin(ctx)
	if err != nil {
		return errors.Wrap(err, "Queryer.Begin()")
	}
	defer func() {
		_ = txn.Rollback(ctx)
	}()

	// The user row is locked, as an unlink locks it, so a removal and an unlink of the
	// user's last identity run one at a time and can't both leave it without a means of
	// sign-in. The same lock holds the hash still between the comparison with the proven
	// one and the write.
	var hasPassword, holdsProven bool
	lockUser := fmt.Sprintf(`
		SELECT "PasswordHash" IS NOT NULL, COALESCE("PasswordHash" = $2, FALSE)
		FROM %s WHERE "Id" = $1 FOR UPDATE`, s.userTable())
	if err := txn.QueryRow(ctx, lockUser, userID, proven).Scan(&hasPassword, &holdsProven); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
		}

		return errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}
	if followsProof && !holdsProven {
		return errPasswordChanged()
	}
	if !hasPassword {
		return httpio.NewBadRequestMessage("the user has no password to remove")
	}
	// Only an identity the caller named counts: one that signs nobody in any more is no
	// means of sign-in, however many of them the user holds.
	named := make([]string, len(standing))
	for i, id := range standing {
		named[i] = id.String()
	}
	var links int
	count := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE "UserId" = $1 AND "Id" = ANY($2::uuid[])`, s.identitiesTable())
	if err := txn.QueryRow(ctx, count, userID, named).Scan(&links); err != nil {
		return errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}
	if links == 0 {
		return httpio.NewConflictMessageWithError(dbtype.ErrLastSignInMethod, "the user has no other means of sign-in: link a directory sign-in first")
	}

	removal := fmt.Sprintf(`UPDATE %s SET "PasswordHash" = NULL, "PasswordChangeRequired" = FALSE WHERE "Id" = $1`, s.userTable())
	if _, err := txn.Exec(ctx, removal, userID); err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec()")
	}

	if err := txn.Commit(ctx); err != nil {
		return errors.Wrap(err, "pgx.Tx.Commit()")
	}

	return nil
}

// DestroyUserSessions expires every live session of a user by UserId. That covers
// its own sessions, user-principal impersonations of it, and the sessions it holds as a
// local actor. Their live impersonation records end Revoked.
func (s *SessionStorageDriver) DestroyUserSessions(ctx context.Context, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if !s.users {
		return errors.New("DestroyUserSessions needs the auth schema: use Auth storage")
	}

	txn, err := s.conn.Begin(ctx)
	if err != nil {
		return errors.Wrap(err, "Queryer.Begin()")
	}
	defer func() {
		_ = txn.Rollback(ctx)
	}()

	if err := s.destroyUserSessionsIn(ctx, txn, userID); err != nil {
		return err
	}

	if err := txn.Commit(ctx); err != nil {
		return errors.Wrap(err, "pgx.Tx.Commit()")
	}

	return nil
}

// destroyUserSessionsIn expires every live session of a user inside txn, as
// DestroyUserSessions does in a transaction of its own: a user's own change of
// password ends their sessions in the transaction that writes the password
// (ChangeUserPassword).
func (s *SessionStorageDriver) destroyUserSessionsIn(ctx context.Context, txn pgx.Tx, userID impulse.UUID) error {
	if s.impersonation == nil {
		expire := fmt.Sprintf(`UPDATE %s SET "Expired" = TRUE, "UpdatedAt" = now() WHERE "Expired" = FALSE AND "UserId" = $1`, s.sessionTable())
		if _, err := txn.Exec(ctx, expire, userID); err != nil {
			return errors.Wrap(err, "pgx.Tx.Exec()")
		}

		return nil
	}

	// The local-actor records name the actor by username.
	var username string
	err := txn.QueryRow(ctx, fmt.Sprintf(`SELECT "Username" FROM %s WHERE "Id" = $1`, s.userTable()), userID).Scan(&username)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}

	// Sessions first: the expiry predicate reads the records while they are still live.
	expire := fmt.Sprintf(`
		UPDATE %s s SET "Expired" = TRUE, "UpdatedAt" = now()
		WHERE s."Expired" = FALSE AND (s."UserId" = $2 OR %s)`, s.sessionTable(), s.heldByLocalActor())
	if _, err := txn.Exec(ctx, expire, username, userID); err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec()")
	}

	endRecords := fmt.Sprintf(`
		UPDATE %s i SET "EndedAt" = now(), "EndReason" = $3
		WHERE i."EndedAt" IS NULL AND (
			EXISTS (SELECT 1 FROM %s s WHERE s."Id" = i."SessionId" AND s."UserId" = $2)
			OR (i."ActorUsername" = $1 AND i."ActorRealm" IS NULL))`,
		pgx.Identifier{s.impersonation.TableName}.Sanitize(), s.sessionTable())
	if _, err := txn.Exec(ctx, endRecords, username, userID, string(sessioninfo.ImpersonationEndedByRevocation)); err != nil {
		return errors.Wrap(err, "pgx.Tx.Exec()")
	}

	return nil
}

// AppendAuthEvent records a step-up or confirmation on a live session, after its
// earlier events, and moves the session's AuthenticatedAt to the event's time. A zero
// event time is the database's time.
func (s *SessionStorageDriver) AppendAuthEvent(ctx context.Context, sessionID impulse.UUID, event *sessioninfo.AuthEvent) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if s.authEvents == nil {
		return errors.New("auth events are not configured: attach WithAuthEvents")
	}
	if event.Method == "" {
		return httpio.NewBadRequestMessage("an auth event needs a method")
	}

	txn, err := s.conn.Begin(ctx)
	if err != nil {
		return errors.Wrap(err, "Queryer.Begin()")
	}
	defer func() {
		_ = txn.Rollback(ctx)
	}()

	// The session row is locked, so concurrent appends take sequence numbers in turn.
	var expired bool
	if err := txn.QueryRow(ctx, fmt.Sprintf(`SELECT "Expired" FROM %s WHERE "Id" = $1 FOR UPDATE`, s.sessionTable()), sessionID).Scan(&expired); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpio.NewNotFoundMessagef("session %q not found", sessionID)
		}

		return errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}
	if expired {
		return httpio.NewBadRequestMessage("cannot record an auth event on an expired session")
	}

	var seq int64
	last := fmt.Sprintf(`SELECT COALESCE(MAX("Seq"), 0) FROM %s WHERE "SessionId" = $1`, pgx.Identifier{s.authEvents.TableName}.Sanitize())
	if err := txn.QueryRow(ctx, last, sessionID).Scan(&seq); err != nil {
		return errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}
	if err := s.insertAuthEvent(ctx, txn, sessionID, seq+1, event); err != nil {
		return err
	}
	if s.users {
		if _, err := txn.Exec(ctx, fmt.Sprintf(`UPDATE %s SET "AuthenticatedAt" = COALESCE($2, now()) WHERE "Id" = $1`, s.sessionTable()), sessionID, optionalTime(event.At)); err != nil {
			return errors.Wrap(err, "pgx.Tx.Exec()")
		}
	}

	if err := txn.Commit(ctx); err != nil {
		return errors.Wrap(err, "pgx.Tx.Commit()")
	}

	return nil
}
