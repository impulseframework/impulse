package postgres

import (
	"context"
	"fmt"

	"github.com/georgysavva/scany/v2/pgxscan"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/tracer"
	"github.com/jackc/pgx/v5"
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
// by. Under the "C" collation only the ASCII letters are letters, so lower() folds
// nothing else. The column is declared under "C" too, so the comparison and the column's
// unique index are under one collation and a lookup uses the index.
func addressFold(operand string) string {
	return fmt.Sprintf(`lower(%s COLLATE "C")`, operand)
}

// errUsernameIsEmail is the refusal of a rename, or of an address entered by hand, on a
// store whose usernames are the addresses: both change through a verified link.
func errUsernameIsEmail() error {
	return httpio.NewConflictMessage("the username is the user's email address: it changes when the person verifies a new address, not by a rename")
}

// errEmailTaken is the refusal of an address another user holds, verified or not.
func errEmailTaken(email string) error {
	return httpio.NewConflictMessagef("the email address %q is already in use", email)
}

// UserByEmail returns the user holding the address, verified or not, matched without
// regard to the case of its ASCII letters, or NotFound.
func (s *SessionStorageDriver) UserByEmail(ctx context.Context, email string) (*dbtype.SessionUser, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	query := fmt.Sprintf(`SELECT %s FROM %s WHERE "NormalizedEmail" = %s`, s.userSelectColumns(), s.userTable(), addressFold("$1"))

	user := &dbtype.SessionUser{}
	if err := pgxscan.Get(ctx, s.conn, user, query, email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpio.NewNotFoundMessagef("no user holds the email address %q", email)
		}

		return nil, errors.Wrap(err, "pgxscan.Get()")
	}

	return user, nil
}

// SetUserEmail puts an address on the user by hand, unverified, or takes it off (nil):
// an administrator's entry for an invitation or a migration, which reserves the
// address. An address another user holds is a Conflict. On a store whose usernames
// are the addresses the entry is refused: the address is the username. Taking the
// address off ends the sign-in codes mailed for the user before it (takeUserEmail).
func (s *SessionStorageDriver) SetUserEmail(ctx context.Context, userID impulse.UUID, email *string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if s.usernameIsEmail {
		return errUsernameIsEmail()
	}
	if email == nil {
		return s.takeUserEmail(ctx, userID)
	}
	query := fmt.Sprintf(`UPDATE %s SET "Email" = $2, "EmailVerifiedAt" = NULL WHERE "Id" = $1`, s.userTable())
	tag, err := s.conn.Exec(ctx, query, userID, email)
	if err != nil {
		if s.isEmailConflict(err) {
			return errEmailTaken(*email)
		}

		return errors.Wrap(err, "Queryer.Exec()")
	}
	if tag.RowsAffected() == 0 {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
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
// and taking that one off ends them all.
func (s *SessionStorageDriver) takeUserEmail(ctx context.Context, userID impulse.UUID) error {
	return s.Transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
		take := fmt.Sprintf(`UPDATE %s SET "Email" = NULL, "EmailVerifiedAt" = NULL WHERE "Id" = $1`, s.userTable())
		tag, err := tx.Exec(ctx, take, userID)
		if err != nil {
			return errors.Wrap(err, "pgx.Tx.Exec()")
		}
		if tag.RowsAffected() == 0 {
			return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
		}
		end := fmt.Sprintf(`UPDATE %s SET "ConsumedAt" = now() WHERE "ConsumedAt" IS NULL AND "PendingId" IN (SELECT "Id" FROM %s WHERE "UserId" = $1)`,
			s.factorCodesTable(), s.pendingTable())
		if _, err := tx.Exec(ctx, end, userID); err != nil {
			return errors.Wrap(err, "pgx.Tx.Exec()")
		}

		return nil
	})
}

// VerifyUserEmail writes the address the person has just proven they read, verified
// at the transaction's time, replacing whatever the user held. On a store whose
// usernames are the addresses it is written as the username, which renames the user
// and its live sessions as SetUserUsername does. An address another user holds,
// verified or not, is a Conflict.
func (s *SessionStorageDriver) VerifyUserEmail(ctx context.Context, userID impulse.UUID, email string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if s.usernameIsEmail {
		err := s.renameUser(ctx, userID, email, `, "EmailVerifiedAt" = now()`, nil)
		if httpio.HasConflict(err) {
			return errEmailTaken(email)
		}

		return err
	}

	query := fmt.Sprintf(`UPDATE %s SET "Email" = $2, "EmailVerifiedAt" = now() WHERE "Id" = $1`, s.userTable())
	tag, err := s.conn.Exec(ctx, query, userID, email)
	if err != nil {
		if s.isEmailConflict(err) {
			return errEmailTaken(email)
		}

		return errors.Wrap(err, "Queryer.Exec()")
	}
	if tag.RowsAffected() == 0 {
		return httpio.NewNotFoundMessagef("user id %q does not exist", userID)
	}

	return nil
}

// errEmailChanged is the refusal of a write taken only while the user still holds an
// address, once the user holds another, or none, or is gone.
func errEmailChanged(userID impulse.UUID) error {
	return httpio.NewConflictMessagef("user id %q no longer holds the email address the write was taken for", userID)
}

// stillHolds is the condition of a write of the user's row taken only while the user
// still holds an address: email, or none for nil.
type stillHolds struct {
	email *string
}

// errNotHeld is renameUser's answer when its condition did not hold.
var errNotHeld = errors.New("the user no longer holds the address")

// VerifyUserEmailIfHeld is VerifyUserEmail taken only while the user still holds held
// (nil: no address): the write of the address a mailed sign-in code proved, which is
// good only for the address the account held when the code was checked. The condition
// is in the statement that writes the user's row, so a change of the address that
// commits between the check and the write is never written over: a user that holds
// another address by then, or none, or is gone, is a Conflict, and so is an address
// another user holds, and nothing is written.
func (s *SessionStorageDriver) VerifyUserEmailIfHeld(ctx context.Context, userID impulse.UUID, held *string, email string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if s.usernameIsEmail {
		err := s.renameUser(ctx, userID, email, `, "EmailVerifiedAt" = now()`, &stillHolds{email: held})
		switch {
		case errors.Is(err, errNotHeld), httpio.HasNotFound(err):
			return errEmailChanged(userID)
		case httpio.HasConflict(err):
			return errEmailTaken(email)
		}

		return err
	}

	query := fmt.Sprintf(`UPDATE %s SET "Email" = $2, "EmailVerifiedAt" = now() WHERE "Id" = $1 AND "Email" IS NOT DISTINCT FROM $3`, s.userTable())
	tag, err := s.conn.Exec(ctx, query, userID, email, held)
	if err != nil {
		if s.isEmailConflict(err) {
			return errEmailTaken(email)
		}

		return errors.Wrap(err, "Queryer.Exec()")
	}
	if tag.RowsAffected() == 0 {
		return errEmailChanged(userID)
	}

	return nil
}

// ClaimVerifiedEmail is ClaimVerifiedEmailIn in a transaction of its own.
func (s *SessionStorageDriver) ClaimVerifiedEmail(ctx context.Context, userID impulse.UUID, email string) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	var claimed bool
	err := s.Transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		claimed, err = s.ClaimVerifiedEmailIn(ctx, tx, userID, email)

		return err
	})
	if err != nil {
		return false, err
	}

	return claimed, nil
}

// ClaimVerifiedEmailIn writes email on the user as verified, at the transaction's
// time, when the user holds no verified address yet and no other user holds email: a
// Google sign-in's verified address, or the address an accepted invitation was sent
// to. It reports whether it wrote. On a store whose usernames are the addresses only
// the verified time is written, and only when the address is the user's username. A
// user that already holds a verified address keeps it.
func (s *SessionStorageDriver) ClaimVerifiedEmailIn(ctx context.Context, tx pgx.Tx, userID impulse.UUID, email string) (bool, error) {
	var query string
	if s.usernameIsEmail {
		query = fmt.Sprintf(`
			UPDATE %s SET "EmailVerifiedAt" = now()
			WHERE "Id" = $1 AND "EmailVerifiedAt" IS NULL AND "NormalizedEmail" = %s`, s.userTable(), addressFold("$2"))
	} else {
		query = fmt.Sprintf(`
			UPDATE %s u SET "Email" = $2, "EmailVerifiedAt" = now()
			WHERE u."Id" = $1 AND u."EmailVerifiedAt" IS NULL
			  AND NOT EXISTS (SELECT 1 FROM %s other WHERE other."NormalizedEmail" = %s AND other."Id" <> $1)`, s.userTable(), s.userTable(), addressFold("$2"))
	}
	tag, err := tx.Exec(ctx, query, userID, email)
	if err != nil {
		return false, errors.Wrap(err, "pgx.Tx.Exec()")
	}

	return tag.RowsAffected() > 0, nil
}

// VerifyUserEmailIfHeldIn writes email on the user as their address, verified at the
// transaction's time, inside tx, only while the user's row still holds held byte for
// byte (nil: no address) and no other user holds email, and reports whether it wrote:
// the address a mailed sign-in code confirms, written in the transaction that inserts
// the session. The conditions are in the one statement that writes the row, so a
// change of the address that commits before it is never written over, and a write it
// does not make fails no statement: tx goes on and can commit. On a store whose
// usernames are the addresses only the verified time is written, and only where the
// row holds held and email is the user's username; held nil writes nothing there.
func (s *SessionStorageDriver) VerifyUserEmailIfHeldIn(ctx context.Context, tx pgx.Tx, userID impulse.UUID, held *string, email string) (bool, error) {
	var query string
	if s.usernameIsEmail {
		if held == nil {
			return false, nil
		}
		query = fmt.Sprintf(`
			UPDATE %s SET "EmailVerifiedAt" = now()
			WHERE "Id" = $1 AND "Email" IS NOT DISTINCT FROM $3 AND "NormalizedEmail" = %s`, s.userTable(), addressFold("$2"))
	} else {
		query = fmt.Sprintf(`
			UPDATE %s u SET "Email" = $2, "EmailVerifiedAt" = now()
			WHERE u."Id" = $1 AND u."Email" IS NOT DISTINCT FROM $3
			  AND NOT EXISTS (SELECT 1 FROM %s other WHERE other."NormalizedEmail" = %s AND other."Id" <> $1)`, s.userTable(), s.userTable(), addressFold("$2"))
	}
	tag, err := tx.Exec(ctx, query, userID, email, held)
	if err != nil {
		return false, errors.Wrap(err, "pgx.Tx.Exec()")
	}

	return tag.RowsAffected() > 0, nil
}

// UserByEmailIn reads the user holding email inside tx, verified or not, matched
// without regard to the case of its ASCII letters, or nil when there is none.
func (s *SessionStorageDriver) UserByEmailIn(ctx context.Context, tx pgx.Tx, email string) (*dbtype.User, error) {
	return s.userByEmailIn(ctx, tx, email, "")
}

// UserByVerifiedEmailIn reads the user who holds email as a verified address inside
// tx, matched without regard to the case of its ASCII letters, or nil when there is
// none; an address a user holds unverified matches no one.
func (s *SessionStorageDriver) UserByVerifiedEmailIn(ctx context.Context, tx pgx.Tx, email string) (*dbtype.User, error) {
	return s.userByEmailIn(ctx, tx, email, `AND "EmailVerifiedAt" IS NOT NULL`)
}

// userByEmailIn reads the part of the user holding email that the sign-in decides on,
// matched on the normalized address and narrowed by the clause more, or nil when
// there is none.
func (s *SessionStorageDriver) userByEmailIn(ctx context.Context, tx pgx.Tx, email, more string) (*dbtype.User, error) {
	query := fmt.Sprintf(`SELECT "Id", "Username", "PasswordHash" IS NOT NULL, "Disabled" FROM %s WHERE "NormalizedEmail" = %s %s`, s.userTable(), addressFold("$1"), more)

	var u dbtype.User
	if err := tx.QueryRow(ctx, query, email).Scan(&u.ID, &u.Username, &u.HasPassword, &u.Disabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, errors.Wrap(err, "pgx.Tx.QueryRow().Scan()")
	}

	return &u, nil
}
