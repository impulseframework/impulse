package resource

import (
	"context"
	"crypto/rand"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/spannertxn"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"google.golang.org/api/iterator"
)

// DeleteTokensDDL is the statements of the DeleteTokens table, where the question a
// request's delete asks first leaves a token once everything under the record may go, for
// the application database's type. An application copies them into a schema migration;
// tests pin Lodestar's and each skeleton's to them, and impulse check holds an
// application's to them. A row is one token: its id, which the question's answer carries;
// the record it was given for (TableName, RowKey); the session it was given to and who
// that session's checks were made as (SessionId, and PrincipalUser or PrincipalRole); when
// it was written and when it expires. The delete that takes the rows under the record
// brings the id back, and takes them only while the row is there, names the record, the
// session and the person, and has not expired. The row is deleted in the commit that takes
// the record, and after a save that failed; the second statement has Spanner remove a row
// a day after it expired. There are statements for Spanner alone.
func DeleteTokensDDL(dbType DBType) []string {
	switch dbType {
	case SpannerDBType:
		return []string{
			`CREATE TABLE DeleteTokens (
  Id STRING(MAX) NOT NULL,
  TableName STRING(MAX) NOT NULL,
  RowKey STRING(MAX) NOT NULL,
  SessionId STRING(MAX) NOT NULL,
  PrincipalUser STRING(MAX),
  PrincipalRole STRING(MAX),
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
  ExpiresAt TIMESTAMP NOT NULL,
) PRIMARY KEY (Id)`,
			"ALTER TABLE DeleteTokens ADD ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 1 DAY))",
		}
	default:
		return nil
	}
}

// DeleteTokenLife is how long a delete's token holds from when the question wrote it, and
// from each save that goes on it.
const DeleteTokenLife = 5 * time.Minute

// deleteTokensTable is the table the tokens are kept in (DeleteTokensDDL), and
// deleteTokenID the column of its key.
const (
	deleteTokensTable = "DeleteTokens"
	deleteTokenID     = "Id"
)

// deleteTokenWrite is how long the delete of the tokens of a save that failed may run,
// detached from the request's cancellation (SpannerClient.ExecuteFunc).
const deleteTokenWrite = 5 * time.Second

// errDeleteTokenRefused ends a read-write transaction whose delete brought a token that
// does not hold: the executor then asks the delete's question with no read-write
// transaction open (SpannerClient.ExecuteDeleteFunc).
var errDeleteTokenRefused = errors.New("the delete's token does not hold: the delete is asked first")

// tokenHolder is whom a token is given to: a session, by its id, and who that session's
// permission checks are made as (sessioninfo.PrincipalFromCtx), a user as the permission
// store names them or a role. Exactly one of user and role is set.
type tokenHolder struct {
	session string
	user    spanner.NullString
	role    spanner.NullString
}

// holderOf reads the holder of a token off the context: nil where the context carries no
// session, as a call made outside any request does. No token is written for such a
// context, and none holds for it.
func holderOf(ctx context.Context) *tokenHolder {
	session, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	if !ok || session == nil || session.SessionInfo == nil {
		return nil
	}
	holder := &tokenHolder{session: session.ID.String(), user: spanner.NullString{}, role: spanner.NullString{}}
	principal := sessioninfo.PrincipalFromCtx(ctx)
	if role, isRole := principal.Role(); isRole {
		holder.role = spanner.NullString{StringVal: string(role), Valid: true}

		return holder
	}
	user, _ := principal.User()
	holder.user = spanner.NullString{StringVal: string(user), Valid: true}

	return holder
}

// askedQuestion is what a question transaction notes of the question a delete asked
// there (deleteTaken): the record it was asked for, by its table and its key as
// rowMatchKey spells it, the question's sentence and its count of records. A token given
// for the question names that record.
type askedQuestion struct {
	table    string
	rowKey   string
	sentence string
	records  int64
}

// noteQuestion notes the question a delete asked on the question transaction, read off
// the error it answered with; an error that carries no client message notes nothing.
func (c *SpannerReadWriteTransaction) noteQuestion(table, rowKey string, err error) {
	message := &httpio.ClientMessage{}
	if !errors.As(err, &message) {
		return
	}
	c.question = &askedQuestion{table: table, rowKey: rowKey, sentence: message.Message(), records: message.Records()}
}

// tokenHolds reads the token's row by its id through the transaction's reads, in one
// statement, and reports whether it holds for the record (its table and its key as
// rowMatchKey spells it) and for the context: the row is there, names the record, the
// context has a holder, and the row's session and principal are the holder's, and it has
// not expired.
func (c *SpannerReadWriteTransaction) tokenHolds(ctx context.Context, id, table, rowKey string) (bool, error) {
	holder := holderOf(ctx)
	if holder == nil {
		return false, nil
	}
	statement := spanner.Statement{
		SQL:    "SELECT TableName, RowKey, SessionId, PrincipalUser, PrincipalRole, ExpiresAt FROM " + deleteTokensTable + " WHERE " + deleteTokenID + " = @id",
		Params: map[string]any{"id": id},
	}
	rows := c.reads().Query(ctx, statement)
	defer rows.Stop()
	row, err := rows.Next()
	if errors.Is(err, iterator.Done) {
		return false, nil
	}
	if err != nil {
		return false, errors.Wrap(err, "reading the delete's token")
	}
	var (
		gotTable, gotRowKey, gotSession string
		gotUser, gotRole                spanner.NullString
		expires                         time.Time
	)
	if err := row.Columns(&gotTable, &gotRowKey, &gotSession, &gotUser, &gotRole, &expires); err != nil {
		return false, errors.Wrap(err, "reading the delete's token")
	}

	return gotTable == table && gotRowKey == rowKey && gotSession == holder.session &&
		gotUser == holder.user && gotRole == holder.role && expires.After(time.Now()), nil
}

// honorToken notes that the transaction's delete went on the token: the executor deletes
// the token's row where the transaction does not commit (SpannerClient.ExecuteFunc).
func (c *SpannerReadWriteTransaction) honorToken(id string) {
	if c.honored != nil {
		c.honored.ids = append(c.honored.ids, id)
	}
}

// writeTokenAfterSave buffers the write of the token a save went on, in the save's own
// commit: its row is deleted where the save takes the record, and otherwise its expiry is
// moved on to DeleteTokenLife from now.
func (c *SpannerReadWriteTransaction) writeTokenAfterSave(id string, finished bool) error {
	mutation := spanner.Delete(deleteTokensTable, spanner.Key{id})
	if !finished {
		mutation = spanner.UpdateMap(deleteTokensTable, map[string]any{deleteTokenID: id, "ExpiresAt": time.Now().Add(DeleteTokenLife)})
	}

	return bufferWrite(c.txn, mutation)
}

// bufferWrite buffers the mutations in the transaction.
func bufferWrite(txn *spanner.ReadWriteTransaction, mutations ...*spanner.Mutation) error {
	if err := txn.BufferWrite(mutations); err != nil {
		return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
	}

	return nil
}

// honoredTokens are the ids of the tokens the deletes of one attempt of a read-write
// transaction went on (SpannerReadWriteTransaction.honorToken).
type honoredTokens struct {
	ids []string
}

// giveToken writes the token of a question that passed, for the holder, in a read-write
// transaction of its own that buffers that one insert, and returns its id.
func (c *SpannerClient) giveToken(ctx context.Context, question *askedQuestion, holder *tokenHolder) (string, error) {
	id := rand.Text()
	insert := spanner.InsertMap(deleteTokensTable, map[string]any{
		deleteTokenID:   id,
		"TableName":     question.table,
		"RowKey":        question.rowKey,
		"SessionId":     holder.session,
		"PrincipalUser": holder.user,
		"PrincipalRole": holder.role,
		"CreatedAt":     spanner.CommitTimestamp,
		"ExpiresAt":     time.Now().Add(DeleteTokenLife),
	})
	if _, err := spannertxn.Serializable(ctx, c.spanner, func(_ context.Context, txn *spanner.ReadWriteTransaction) error {
		return bufferWrite(txn, insert)
	}); err != nil {
		return "", errors.Wrap(err, "writing the delete token: the DeleteTokens table is declared by resource.DeleteTokensDDL and created by a migration of the application")
	}

	return id, nil
}

// dropTokens deletes the rows of the tokens a save that failed went on, in a short
// transaction of its own, detached from the request's cancellation and for
// deleteTokenWrite at most: the person is asked again. A failure is logged and changes
// nothing in what the request answers; the token expires by itself.
func (c *SpannerClient) dropTokens(ctx context.Context, ids []string) {
	if len(ids) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deleteTokenWrite)
	defer cancel()
	deletes := make([]*spanner.Mutation, 0, len(ids))
	for _, id := range ids {
		deletes = append(deletes, spanner.Delete(deleteTokensTable, spanner.Key{id}))
	}
	if _, err := spannertxn.Serializable(ctx, c.spanner, func(_ context.Context, txn *spanner.ReadWriteTransaction) error {
		return bufferWrite(txn, deletes...)
	}); err != nil {
		logger.FromCtx(ctx).Errorf("resource: the delete token of a save that failed could not be deleted; it expires by itself: %v", err)
	}
}

// keepsToken reports whether a read-write transaction that ended with err keeps the
// tokens its deletes went on: one that ended because a token did not hold, and a dry run
// and a method's warning, which roll back by design.
func keepsToken(err error) bool {
	return errors.Is(err, errDeleteTokenRefused) || errors.Is(err, ErrDryRun) || errors.Is(err, ErrWarningAsked)
}
