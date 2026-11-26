package spanner

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/spannertxn"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/spxscan"
	"github.com/impulseframework/impulse/spxscan/spxapi"
	"github.com/impulseframework/impulse/tracer"
	"google.golang.org/grpc/codes"
)

// The user's email address, in two states (see the auth set's SessionUserEmail
// migration). The address is matched without regard to the case of its ASCII letters
// through the normalized column, which the database derives; on a store whose usernames
// are the addresses the database derives the address itself from the username, so the
// driver writes the username where it would write the address and never writes Email.

// addressFold is the form the normalized column holds, over operand (the column's
// source or a parameter), which every lookup by address compares the column with: the
// address with its ASCII capital letters lowercased and every other character as
// written. It must equal util.LowerASCII, the rule the library compares two addresses
// by. LOWER over BYTES treats its argument as ASCII and leaves every byte above 127 as
// it is, so it folds nothing else, and the bytes it returns are the UTF-8 it was given.
func addressFold(operand string) string {
	return fmt.Sprintf(`CAST(LOWER(CAST(%s AS BYTES)) AS STRING)`, operand)
}

// emailVerifiedAtColumn is the column that records when the address was verified.
const (
	emailColumn           = "Email"
	emailVerifiedAtColumn = "EmailVerifiedAt"
)

// errUsernameIsEmail is the refusal of a rename, or of an address entered by hand, on a
// store whose usernames are the addresses: both change through a verified link.
func errUsernameIsEmail() error {
	return httpio.NewConflictMessage(dbtype.UsernameIsEmailMessage)
}

// insertedAddress is the address a new user row holds: the one the insert names, or,
// on a store whose usernames are the addresses, where the insert may name none, the
// username.
func insertedAddress(user *dbtype.InsertSessionUser) string {
	if user.Email != nil {
		return *user.Email
	}

	return user.Username
}

// errEmailTaken is the refusal of an address another user holds, verified or not.
func errEmailTaken(email string) error {
	return httpio.NewConflictMessageWithError(errors.Newf("the email address %q is already in use", email), dbtype.EmailInUseMessage)
}

// UserByEmail returns the user holding the address, verified or not, matched without
// regard to the case of its ASCII letters, or NotFound.
func (s *SessionStorageDriver) UserByEmail(ctx context.Context, email string) (*dbtype.SessionUser, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	stmt := spanner.NewStatement(fmt.Sprintf(`SELECT %s FROM %s WHERE NormalizedEmail = %s`, s.userSelectColumns(), s.userTableName, addressFold("@email")))
	stmt.Params["email"] = email

	user := &dbtype.SessionUser{}
	if err := spxscan.Get(ctx, s.spanner.Single(), user, stmt); err != nil {
		if errors.Is(err, spxapi.ErrNotFound) {
			return nil, dbtype.EmailNotFound(email)
		}

		return nil, errors.Wrap(err, "spxscan.Get()")
	}

	return user, nil
}

// SetUserEmail puts an address on the user by hand, unverified, or takes it off (nil):
// an administrator's entry for an invitation or a migration, which reserves the
// address. An address another user holds is a Conflict. On a store whose usernames
// are the addresses the entry is refused: the address is the username. Taking the
// address off ends the sign-in codes mailed for the user before it (takeUserEmail).
func (s *SessionStorageDriver) SetUserEmail(ctx context.Context, userID impulse.UUID, email *string, record *auditlog.Record) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if s.usernameIsEmail {
		return errUsernameIsEmail()
	}
	if email == nil {
		return s.takeUserEmail(ctx, userID, record)
	}
	row := map[string]any{idColumn: userID, emailColumn: spanner.NullString{StringVal: *email, Valid: true}, emailVerifiedAtColumn: spanner.NullTime{}}
	audit, err := s.auditRecordMutations(record)
	if err != nil {
		return err
	}
	if _, err := s.spanner.Apply(ctx, append([]*spanner.Mutation{spanner.UpdateMap(s.userTableName, row)}, audit...)); err != nil {
		switch {
		case spanner.ErrCode(err) == codes.NotFound:
			return dbtype.AccountNotFound(userID)
		case spanner.ErrCode(err) == codes.AlreadyExists && strings.Contains(err.Error(), s.emailIndex()):
			return errEmailTaken(*email)
		}

		return errors.Wrap(err, "spanner.Client.Apply()")
	}

	return nil
}

// takeUserEmail takes the user's address off and, in the same transaction, consumes
// every live sign-in code of the user's pending sign-ins. A code is good only for the
// address the account holds when it is entered, and an account that holds none takes
// the address its own person gives at the code's step, so a code mailed to the address
// before it was taken off would otherwise read as one mailed to an address the person
// gave, and write the address back. With the codes ended, the only code that stands on
// an account that holds no address is one mailed after the change. An address replaced
// by another needs none of this: its codes are dead while the account holds the other,
// and taking that one off ends them all. record, nil for none, is buffered with the
// address's removal.
func (s *SessionStorageDriver) takeUserEmail(ctx context.Context, userID impulse.UUID, record *auditlog.Record) error {
	row := map[string]any{idColumn: userID, emailColumn: spanner.NullString{}, emailVerifiedAtColumn: spanner.NullTime{}}
	_, err := spannertxn.Serializable(ctx, s.spanner, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		stmt := spanner.NewStatement(fmt.Sprintf(`UPDATE %s SET ConsumedAt = PENDING_COMMIT_TIMESTAMP() WHERE ConsumedAt IS NULL AND PendingId IN (SELECT Id FROM %s WHERE UserId = @userId)`,
			s.factorCodesTableName, s.pendingTableName))
		stmt.Params["userId"] = userID.String()
		if _, err := txn.Update(ctx, stmt); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.Update()")
		}
		if err := txn.BufferWrite([]*spanner.Mutation{spanner.UpdateMap(s.userTableName, row)}); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
		}

		return s.bufferAuditRecord(txn, record)
	})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return dbtype.AccountNotFound(userID)
		}

		return errors.Wrap(err, "spannertxn.Serializable()")
	}

	return nil
}

// VerifyUserEmail writes the address the person has just proven they read, verified
// at the commit time, replacing whatever the user held: VerifyUserEmailIn in a
// transaction of its own. On a store whose usernames are the addresses it is written
// as the username, which renames the user and its live sessions as SetUserUsername
// does. An address another user holds, verified or not, is a Conflict.
func (s *SessionStorageDriver) VerifyUserEmail(ctx context.Context, userID impulse.UUID, email string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	_, err := spannertxn.Serializable(ctx, s.spanner, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		return s.VerifyUserEmailIn(ctx, txn, userID, email)
	})
	if err != nil {
		switch {
		case spanner.ErrCode(err) == codes.NotFound:
			return dbtype.AccountNotFound(userID)
		case s.usernameIsEmail && s.usernameConflict(err):
			return errEmailTaken(email)
		case spanner.ErrCode(err) == codes.AlreadyExists && strings.Contains(err.Error(), s.emailIndex()):
			return errEmailTaken(email)
		}

		return errors.Wrap(err, "spannertxn.Serializable()")
	}

	return nil
}

// VerifyUserEmailIn buffers the address the person has just proven they read on the
// user, verified at the commit time, inside tx, replacing whatever the user held. On a
// store whose usernames are the addresses it is written as the username, which renames
// the user, its live sessions and the live impersonations it holds as an actor
// (renameUserIn). The write is a buffered mutation, so a missing user ends the
// transaction with NotFound at the commit, and an address or a name another user holds
// with the database's AlreadyExists (SignInConflict names it).
func (s *SessionStorageDriver) VerifyUserEmailIn(ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, email string) error {
	if s.usernameIsEmail {
		return s.renameUserIn(ctx, tx, userID, email, map[string]any{emailVerifiedAtColumn: spanner.CommitTimestamp})
	}

	row := map[string]any{idColumn: userID, emailColumn: email, emailVerifiedAtColumn: spanner.CommitTimestamp}
	if err := tx.BufferWrite([]*spanner.Mutation{spanner.UpdateMap(s.userTableName, row)}); err != nil {
		return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
	}

	return nil
}

// stillHolds is the condition of a write of the user's row taken only while the user
// still holds an address: email, or none for nil.
type stillHolds struct {
	email *string
}

// met reports whether a user's row that holds held meets the condition.
func (c *stillHolds) met(held spanner.NullString) bool {
	if c.email == nil {
		return !held.Valid
	}

	return held.Valid && held.StringVal == *c.email
}

// ClaimVerifiedEmail is ClaimVerifiedEmailIn in a transaction of its own, which writes
// record with the address where the claim wrote it, and none where it wrote nothing.
func (s *SessionStorageDriver) ClaimVerifiedEmail(ctx context.Context, userID impulse.UUID, email string, record *auditlog.Record) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	var claimed bool
	_, err := spannertxn.Serializable(ctx, s.spanner, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		var err error
		if claimed, err = s.ClaimVerifiedEmailIn(ctx, txn, userID, email); err != nil || !claimed {
			return err
		}

		return s.bufferAuditRecord(txn, record)
	})
	if err != nil {
		return false, errors.Wrap(err, "spannertxn.Serializable()")
	}

	return claimed, nil
}

// ClaimVerifiedEmailIn writes email on the user as verified, at the commit time, when
// the user holds no verified address yet and no other user holds email: a Google
// sign-in's verified address, or the address an accepted invitation was sent to. It
// reports whether it wrote. On a store whose usernames are the addresses only the
// verified time is written, and only when the address is the user's username. A user
// that already holds a verified address keeps it. The checks are reads in the
// transaction and the write a buffered mutation: the emulator refuses a DML update of
// a column a generated column derives from when the statement reads the table too,
// and the serializable transaction keeps read and write together.
func (s *SessionStorageDriver) ClaimVerifiedEmailIn(ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, email string) (bool, error) {
	holder, err := s.UserByEmailIn(ctx, tx, email)
	if err != nil {
		return false, err
	}
	switch {
	case holder != nil && holder.ID != userID:
		return false, nil
	case s.usernameIsEmail && holder == nil:
		return false, nil
	}
	row, err := tx.ReadRow(ctx, s.userTableName, spanner.Key{userID.String()}, []string{emailVerifiedAtColumn})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return false, nil
		}

		return false, errors.Wrap(err, "spanner.ReadWriteTransaction.ReadRow()")
	}
	var verifiedAt spanner.NullTime
	if err := row.Column(0, &verifiedAt); err != nil {
		return false, errors.Wrap(err, "spanner.Row.Column()")
	}
	if verifiedAt.Valid {
		return false, nil
	}
	update := map[string]any{idColumn: userID, emailVerifiedAtColumn: spanner.CommitTimestamp}
	if !s.usernameIsEmail {
		update[emailColumn] = email
	}
	if err := tx.BufferWrite([]*spanner.Mutation{spanner.UpdateMap(s.userTableName, update)}); err != nil {
		return false, errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
	}

	return true, nil
}

// VerifyUserEmailIfHeldIn writes email on the user as their address, verified at the
// commit time, inside tx, only while the user's row still holds held byte for byte
// (nil: no address) and no other user holds email, and reports whether it wrote: the
// address a mailed sign-in code confirms, written in the transaction that inserts the
// session. The checks are reads in the transaction and the write a buffered mutation,
// as in ClaimVerifiedEmailIn, and the serializable transaction keeps read and write
// together, so a change of the address that commits before it is never written over;
// a write it does not make buffers nothing, and tx goes on and can commit. On a store
// whose usernames are the addresses only the verified time is written, and only where
// the row holds held and email is the user's username; held nil writes nothing there.
func (s *SessionStorageDriver) VerifyUserEmailIfHeldIn(
	ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, held *string, email string,
) (bool, error) {
	if s.usernameIsEmail && held == nil {
		return false, nil
	}
	row, err := tx.ReadRow(ctx, s.userTableName, spanner.Key{userID.String()}, []string{emailColumn})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return false, nil
		}

		return false, errors.Wrap(err, "spanner.ReadWriteTransaction.ReadRow()")
	}
	var stored spanner.NullString
	if err := row.Column(0, &stored); err != nil {
		return false, errors.Wrap(err, "spanner.Row.Column()")
	}
	if !(&stillHolds{email: held}).met(stored) {
		return false, nil
	}
	holder, err := s.UserByEmailIn(ctx, tx, email)
	if err != nil {
		return false, err
	}
	switch {
	case holder != nil && holder.ID != userID:
		return false, nil
	case s.usernameIsEmail && holder == nil:
		return false, nil
	}
	update := map[string]any{idColumn: userID, emailVerifiedAtColumn: spanner.CommitTimestamp}
	if !s.usernameIsEmail {
		update[emailColumn] = email
	}
	if err := tx.BufferWrite([]*spanner.Mutation{spanner.UpdateMap(s.userTableName, update)}); err != nil {
		return false, errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
	}

	return true, nil
}

// UserByEmailIn reads the user holding email inside tx, verified or not, matched
// without regard to the case of its ASCII letters, or nil when there is none.
func (s *SessionStorageDriver) UserByEmailIn(ctx context.Context, tx *spanner.ReadWriteTransaction, email string) (*dbtype.User, error) {
	return s.userByEmailIn(ctx, tx, email, "")
}

// UserByVerifiedEmailIn reads the user who holds email as a verified address inside
// tx, matched without regard to the case of its ASCII letters, or nil when there is
// none; an address a user holds unverified matches no one.
func (s *SessionStorageDriver) UserByVerifiedEmailIn(ctx context.Context, tx *spanner.ReadWriteTransaction, email string) (*dbtype.User, error) {
	return s.userByEmailIn(ctx, tx, email, "AND EmailVerifiedAt IS NOT NULL")
}

// userByEmailIn reads the part of the user holding email that the sign-in decides on,
// matched on the normalized address and narrowed by the clause more, or nil when
// there is none.
func (s *SessionStorageDriver) userByEmailIn(ctx context.Context, tx *spanner.ReadWriteTransaction, email, more string) (*dbtype.User, error) {
	stmt := spanner.NewStatement(fmt.Sprintf(`SELECT Id, Username, PasswordHash, Disabled FROM %s WHERE NormalizedEmail = %s %s`, s.userTableName, addressFold("@email"), more))
	stmt.Params["email"] = email

	var user *dbtype.User
	err := tx.Query(ctx, stmt).Do(func(row *spanner.Row) error {
		u, err := userOf(row)
		if err != nil {
			return err
		}
		user = u

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "spanner.RowIterator.Do()")
	}

	return user, nil
}
