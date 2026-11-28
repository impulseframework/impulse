package resource

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"time"

	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/spannertxn"
	"github.com/impulseframework/impulse/spxscan"
	"github.com/impulseframework/impulse/spxscan/spxapi"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/structpb"
)

var _ Client = (*SpannerClient)(nil)

// SpannerClient is a wrapper around the database.
type SpannerClient struct {
	spanner *spanner.Client
	// stores are the application's file stores by name, where the upload frames
	// stream, the file routes open, and a committed transaction's released objects are
	// deleted from.
	stores *fileStores
	// secrets are the application's secret keys, which every transaction of the client
	// seals a patch's secret fields with; nil when the client was built with none.
	secrets *SecretKeys
	// deleteSaveTime is how long one save of a delete that takes rows runs
	// (WithDeleteSaveTime), DefaultDeleteSaveTime where it is zero.
	deleteSaveTime time.Duration
}

// NewSpannerClient creates a new Client. WithFileStore and WithNamedFileStore hand it
// the stores the frames drive and a committed transaction's released file objects are
// deleted from; a store wired twice, or two stores on one location, panics here.
// WithSecretKeys hands it the keys its transactions seal a patch's secret fields with.
// WithDeleteSaveTime sets how long one save of a delete that takes rows runs.
func NewSpannerClient(db *spanner.Client, opts ...ClientOption) *SpannerClient {
	stores, secrets, deleteSaveTime := applyClientOptions(opts)

	return &SpannerClient{
		spanner:        db,
		stores:         stores,
		secrets:        secrets,
		deleteSaveTime: deleteSaveTime,
	}
}

// secretKeys returns the application's secret keys, nil when the client holds none.
func (c *SpannerClient) secretKeys() *SecretKeys {
	return c.secrets
}

// DBType returns the database type.
func (c *SpannerClient) DBType() DBType {
	return SpannerDBType
}

// FileStore returns the store wired under name, nil when none is.
func (c *SpannerClient) FileStore(name StoreName) FileStore {
	return c.stores.get(name)
}

// SpannerReadOnlyTransaction returns a read-only transaction for the Spanner client.
func (c *SpannerClient) SpannerReadOnlyTransaction() spxapi.Querier {
	return c.spanner.Single()
}

// ExecuteFunc executes a function within a read-write transaction at the serializable
// isolation level, whatever level the application set for its Spanner client
// (spannertxn.Serializable). The library decides rules of its own inside the transaction
// by reading before it writes (the rows counted under a record before its delete, an
// auth's last super administrator, one tenant per person), and each holds only while
// Spanner serializes the read with what another transaction writes at the same moment.
// A commit Spanner refuses for a reason the caller can act on (a referential refusal, a
// duplicate key or unique value, a violated CHECK constraint, an update of a row that
// does not exist) answers as a 4xx whose message is composed from the patches the
// transaction buffered (see translateCommitError); an error the function itself returns,
// and a commit refused with any other code, pass through unchanged.
//
// A delete buffered in f that has not been told to take the rows under its record
// (PatchSet.DeleteEverythingUnder) asks inside this transaction, and what it reads stays
// locked until f returns. A request that may hold such a delete is run with
// ExecuteDeleteFunc, which asks before a read-write transaction is opened.
//
// Before the commit, a delete of a record with rows under it is refused as a 409 that
// says what is in the way ("This ship has 3 sorties. Delete them first."), from one
// count per kind inside the transaction (delete_refusal.go), of what the database held
// before it. Then a transaction whose patches release a file object of a store the
// client holds no store for is refused naming the store: the rows would go and the
// object would stay with nothing to delete it. Once the commit lands, the file objects
// the transaction's patches released (a deleted row's @file keys, the old key of a row
// pointed at another object) are deleted from their stores, synchronously, before
// ExecuteFunc returns, detached from the request's cancellation; a failed delete is
// logged naming the keys and the call still returns nil, since the rows are gone. A
// transaction that does not commit, for any reason, releases nothing. Last, what the
// transaction's writes registered for after the commit runs (AfterCommit), in
// registration order; a transaction that does not commit runs none of it.
func (c *SpannerClient) ExecuteFunc(ctx context.Context, f func(ctx context.Context, txn ReadWriteTransaction) error) error {
	return c.executeFunc(ctx, false, false, f)
}

// executeFunc is ExecuteFunc, with a delete whose token does not hold ending the
// transaction where refusesOutside is set (SpannerReadWriteTransaction.refusesOutside),
// and the transaction opened at repeatable read where repeatableRead is set, for a save of
// a delete (spannertxn.RepeatableRead), serializable elsewhere (spannertxn.Serializable).
// A commit refused at either level is answered alike (translateCommitError). Where the
// transaction does not commit, the tokens its last attempt's deletes went on are deleted
// (dropTokens), unless it ended for a reason that keeps them (keepsToken).
func (c *SpannerClient) executeFunc(ctx context.Context, refusesOutside, repeatableRead bool, f func(ctx context.Context, txn ReadWriteTransaction) error) error {
	var (
		buffered   = newBufferedPatches()
		released   = newReleasedKeys()
		touched    = newTouchedRows()
		committed  = newAfterCommit()
		honored    = &honoredTokens{ids: nil}
		funcFailed bool
	)
	open, opener := spannertxn.Serializable, "spannertxn.Serializable()"
	if repeatableRead {
		open, opener = spannertxn.RepeatableRead, "spannertxn.RepeatableRead()"
	}
	_, err := open(ctx, c.spanner, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		// A retried transaction runs f again; the records belong to the attempt that commits.
		buffered = newBufferedPatches()
		released = newReleasedKeys()
		touched = newTouchedRows()
		committed = newAfterCommit()
		honored = &honoredTokens{ids: nil}
		funcFailed = false
		wrapped := newSpannerReadWriteTransaction(txn, buffered, released, touched, committed).WithSecretKeys(c.secrets)
		wrapped.deleteSaveTime = c.deleteSaveTime
		wrapped.refusesOutside = refusesOutside
		wrapped.repeatableRead = repeatableRead
		wrapped.honored = honored
		if err := f(ctx, wrapped); err != nil {
			funcFailed = true

			return errors.Wrap(err, "f()")
		}
		if err := wrapped.refuseDeletesOver(ctx); err != nil {
			funcFailed = true

			return err
		}
		if err := refuseUnwiredRelease(c.stores, released.list()); err != nil {
			funcFailed = true

			return err
		}

		return nil
	})
	if err != nil {
		if !keepsToken(err) {
			c.dropTokens(ctx, honored.ids)
		}
		err = errors.Wrap(err, opener)
		if funcFailed {
			return err
		}

		return translateCommitError(err, buffered)
	}

	releaseFiles(ctx, c.stores, released.list())
	collectTouched(ctx, touched.list())
	committed.run(ctx)

	return nil
}

// ExecuteDeleteFunc is ExecuteFunc for a request that may hold the delete of a record
// with rows under it. Where run is DeleteRunAsks, f is first run on a question
// transaction: every read made through it goes to one read-only transaction, which reads
// the database as it stood at one moment and locks nothing; nothing buffered through it
// is written; and of the patches f buffers only a delete that asks first is run
// (PatchSet.Buffer does nothing there for any other). An error f returns there, a refusal
// or the question a person is asked first, is the answer, and no read-write transaction
// was opened. Where f returns none, or ends a dry run there (ErrDryRun), nothing was
// asked, and f is run again as ExecuteFunc runs it: the delete then reads and checks
// inside its own transaction, and a dry run makes every write's own checks before it is
// rolled back. Where run is DeleteRunPlain it is ExecuteFunc. Where run is DeleteRunSave
// it is ExecuteFunc with the transaction opened at the repeatable read isolation level
// (the request is a save of a delete): its reads lock nothing, and Spanner aborts it only
// where another transaction wrote a row it writes. f must start each run from nothing, as
// it must for ExecuteFunc, which runs it again when Spanner aborts the transaction.
//
// A delete that brings a token (PatchSet.DeleteWithToken) is not asked first: its request
// says DeleteRunSave or DeleteRunPlain, and the token is read inside the read-write
// transaction. Where it does not hold, that transaction ends without a commit and f is run
// as a request that asks. A question that passes on the question transaction writes its
// token in a short transaction of its own, and the answer carries it
// (httpio.NewPreconditionRequiredToken). Where a save that went on a token fails, the
// token is deleted: the person is asked again.
func (c *SpannerClient) ExecuteDeleteFunc(ctx context.Context, run DeleteRun, f func(ctx context.Context, txn ReadWriteTransaction) error) error {
	if run != DeleteRunAsks {
		if err := c.executeFunc(ctx, true, run == DeleteRunSave, f); !errors.Is(err, errDeleteTokenRefused) {
			return err
		}
	}
	if err := c.ask(ctx, f); err != nil && !errors.Is(err, ErrDryRun) {
		return err
	}

	return c.ExecuteFunc(ctx, f)
}

// ask runs f once on a question transaction (SpannerReadWriteTransaction.asking) over one
// read-only transaction of the client, under the context it was given, and closes the
// read-only transaction when f returns. Where f answers with the question a delete asked
// there (httpio.HasPreconditionRequired) and the context has a holder (holderOf), the
// question's token is written in a transaction of its own (giveToken) and the question is
// answered with it (httpio.NewPreconditionRequiredToken). Nothing else happens: no other
// read-write transaction is opened, nothing is counted before a commit, no file is
// released and nothing registered for after a commit runs.
func (c *SpannerClient) ask(ctx context.Context, f func(ctx context.Context, txn ReadWriteTransaction) error) error {
	snapshot := c.spanner.ReadOnlyTransaction()
	defer snapshot.Close()

	question := newSpannerQuestionTransaction(snapshot, c.secrets)
	err := f(ctx, question)
	if err == nil {
		return nil
	}
	if asked, holder := question.question, holderOf(ctx); asked != nil && holder != nil && httpio.HasPreconditionRequired(err) {
		id, err := c.giveToken(ctx, asked, holder)
		if err != nil {
			return err
		}

		return httpio.NewPreconditionRequiredToken(asked.sentence, asked.records, id)
	}

	return errors.Wrap(err, "f()")
}

// ReadOnlyTransaction returns a read-only transaction that can be used for multiple reads from the database.
// You must call Close() when the ReadOnlyTransaction is no longer needed to release resources on the server.
func (c *SpannerClient) ReadOnlyTransaction() ReadOnlyTransactionCloser {
	return newSpannerReadOnlyTransaction(c.spanner)
}

// PostgresReadOnlyTransaction panics because it is not implemented for the SpannerClient.
func (c *SpannerClient) PostgresReadOnlyTransaction() any {
	panic("SpannerClient.PostgresReadOnlyTransaction() should never be called.")
}

var _ Reader[nilResource] = (*spannerReader[nilResource])(nil)

// spannerReader is a reader for Spanner.
type spannerReader[Resource Resourcer] struct {
	readTxn func() spxapi.Querier
}

// DBType returns the database type.
func (c *spannerReader[Resource]) DBType() DBType {
	return SpannerDBType
}

// envelopeScan reports whether the statement carries reserved metadata
// columns (masked names, capability checks, the cursor's sort-key copies) or
// an assembly plan, requiring the lenient per-row scan instead of the plain
// spxscan path.
func envelopeScan(stmt *Statement) bool {
	return stmt.maskedNamesColumn != "" || stmt.capabilityPlan != nil || len(stmt.cursorColumns) > 0 || stmt.tenantColumn != ""
}

// querier returns the transaction a statement runs on. A statement that names a query
// optimizer version (a search predicate, see searchOptimizerVersion) runs through
// QueryWithOptions with the version in its request options; every other statement runs
// plainly, byte-identical to before.
func (c *spannerReader[Resource]) querier(stmt *Statement) spxapi.Querier {
	txn := c.readTxn()
	if stmt.optimizerVersion == "" {
		return txn
	}

	return &optionsQuerier{
		Querier: txn,
		options: spanner.QueryOptions{Options: &sppb.ExecuteSqlRequest_QueryOptions{OptimizerVersion: stmt.optimizerVersion}},
	}
}

// queryOptioner is the part of a Spanner transaction that takes per-query options:
// spanner.ReadOnlyTransaction and spanner.ReadWriteTransaction both implement it.
type queryOptioner interface {
	QueryWithOptions(ctx context.Context, statement spanner.Statement, opts spanner.QueryOptions) *spanner.RowIterator
}

// optionsQuerier runs every query with fixed options over a transaction that takes
// them, and plainly over one that does not (a test double).
type optionsQuerier struct {
	spxapi.Querier
	options spanner.QueryOptions
}

// Query runs the statement with the querier's options.
func (q *optionsQuerier) Query(ctx context.Context, statement spanner.Statement) *spanner.RowIterator {
	if o, ok := q.Querier.(queryOptioner); ok {
		return o.QueryWithOptions(ctx, statement, q.options)
	}

	return q.Querier.Query(ctx, statement)
}

// Read reads a single resource from the database. A row that is not there answers 404 in
// plain words (recordNotFound): the statement's WHERE clause is kept for the server's
// log and is never sent to the caller.
func (c *spannerReader[Resource]) Read(ctx context.Context, stmt *Statement) (*Row[Resource], error) {
	if envelopeScan(stmt) {
		row, err := c.readEnvelope(ctx, stmt)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, recordNotFound[Resource](stmt.resolvedWhereClause)
		}

		return row, nil
	}

	row := new(Row[Resource])
	if err := spxscan.Get(ctx, c.querier(stmt), &row.Data, stmt.SpannerStatement()); err != nil {
		if errors.Is(err, spxapi.ErrNotFound) {
			return nil, recordNotFound[Resource](stmt.resolvedWhereClause)
		}

		return nil, errors.Wrap(err, "spxscan.Get()")
	}

	return row, nil
}

// readEnvelope reads the first row of an envelope statement; a nil row with
// no error means no row matched.
func (c *spannerReader[Resource]) readEnvelope(ctx context.Context, stmt *Statement) (*Row[Resource], error) {
	it := c.querier(stmt).Query(ctx, stmt.SpannerStatement())
	defer it.Stop()

	spannerRow, err := it.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return nil, nil
		}

		return nil, errors.Wrap(err, "spanner.RowIterator.Next()")
	}

	return scanEnvelopeRow[Resource](spannerRow, stmt)
}

// List reads a list of resources from the database.
func (c *spannerReader[Resource]) List(ctx context.Context, stmt *Statement) iter.Seq2[*Row[Resource], error] {
	if envelopeScan(stmt) {
		return c.listEnvelope(ctx, stmt)
	}

	return func(yield func(*Row[Resource], error) bool) {
		for r, err := range spxscan.SelectSeq[Resource](ctx, c.querier(stmt), stmt.SpannerStatement()) {
			if err != nil {
				yield(nil, errors.Wrap(err, "spxscan.SelectSeq()"))

				return
			}
			if !yield(&Row[Resource]{Data: *r, tenant: stmt.tenant}, nil) {
				return
			}
		}
	}
}

// Count runs a COUNT(*) statement and returns its one value.
func (c *spannerReader[Resource]) Count(ctx context.Context, stmt *Statement) (int64, error) {
	it := c.querier(stmt).Query(ctx, stmt.SpannerStatement())
	defer it.Stop()

	spannerRow, err := it.Next()
	if err != nil {
		return 0, errors.Wrap(err, "spanner.RowIterator.Next()")
	}
	var total int64
	if err := spannerRow.Columns(&total); err != nil {
		return 0, errors.Wrap(err, "spanner.Row.Columns()")
	}

	return total, nil
}

// listEnvelope iterates an envelope statement, scanning each row's data and
// its reserved metadata columns into the Row envelope.
func (c *spannerReader[Resource]) listEnvelope(ctx context.Context, stmt *Statement) iter.Seq2[*Row[Resource], error] {
	return func(yield func(*Row[Resource], error) bool) {
		it := c.querier(stmt).Query(ctx, stmt.SpannerStatement())
		defer it.Stop()

		for {
			spannerRow, err := it.Next()
			if err != nil {
				if !errors.Is(err, iterator.Done) {
					yield(nil, errors.Wrap(err, "spanner.RowIterator.Next()"))
				}

				return
			}

			row, err := scanEnvelopeRow[Resource](spannerRow, stmt)
			if err != nil {
				yield(nil, err)

				return
			}
			if !yield(row, nil) {
				return
			}
		}
	}
}

// scanEnvelopeRow scans one envelope-statement row: the resource columns into
// the envelope's data (leniently, so the reserved columns are skipped), the
// reserved masked-names column into the mask list, the reserved cursor
// columns into the cursor values, and the reserved capability-checks column
// through the plan into the capability answers. A NULL check boolean reads as
// false — a condition permits only on TRUE.
func scanEnvelopeRow[Resource Resourcer](spannerRow *spanner.Row, stmt *Statement) (*Row[Resource], error) {
	row := new(Row[Resource])
	if err := spannerRow.ToStructLenient(&row.Data); err != nil {
		return nil, errors.Wrap(err, "spanner.Row.ToStructLenient()")
	}
	row.tenant = stmt.tenant
	if stmt.tenantColumn != "" {
		var tenant spanner.NullString
		if err := spannerRow.ColumnByName(stmt.tenantColumn, &tenant); err != nil {
			return nil, errors.Wrap(err, "spanner.Row.ColumnByName()")
		}
		row.tenant = accesstypes.Domain(tenant.StringVal)
	}
	if stmt.maskedNamesColumn != "" {
		if err := spannerRow.ColumnByName(stmt.maskedNamesColumn, &row.masked); err != nil {
			return nil, errors.Wrap(err, "spanner.Row.ColumnByName()")
		}
	}
	for _, column := range stmt.cursorColumns {
		copied, err := scanCursorColumn(spannerRow, column)
		if err != nil {
			return nil, err
		}
		if row.cursorValues == nil {
			row.cursorValues = make(map[accesstypes.Field]reflect.Value, len(stmt.cursorColumns))
		}
		row.cursorValues[column.field] = copied
	}
	if plan := stmt.capabilityPlan; plan != nil {
		var checks []bool
		if plan.checksColumn != "" {
			var scanned []spanner.NullBool
			if err := spannerRow.ColumnByName(plan.checksColumn, &scanned); err != nil {
				return nil, errors.Wrap(err, "spanner.Row.ColumnByName()")
			}
			if len(scanned) != plan.groups {
				return nil, errors.Newf("capability checks column carries %d booleans, plan expects %d", len(scanned), plan.groups)
			}
			checks = make([]bool, len(scanned))
			for i, b := range scanned {
				checks[i] = b.Valid && b.Bool
			}
		}
		row.capabilities = plan.assemble(checks)
	}

	return row, nil
}

// scanCursorColumn reads one cursor copy off the row. The copy arrives as a
// fresh value of the field's own type, so the cursor encodes it exactly as it
// would the cell; a copy that can be NULL where the type cannot (a concealing
// key's CASE) arrives as a pointer to the type, nil for NULL, which the cursor
// writes as the NULL key.
//
// The copy is not scanned into a pointer to a pointer to the field's type. The
// client decodes a pointer to a pointer for the base kinds, for its own value
// types, and for a Decoder, and refuses it for a named variant of a base kind
// (type HazardLevel int64), the type a resource column may well declare. So
// the copy is read as a generic column value: NULL is seen on the wire and
// never decoded, and a value decodes into a single pointer to the field's type,
// the client's own definition of decoding into that type, which admits every
// kind the sortable rule admits, named variants included. A type the client
// cannot decode at all still fails with the client's error, naming the alias.
func scanCursorColumn(spannerRow *spanner.Row, column cursorColumn) (reflect.Value, error) {
	var generic spanner.GenericColumnValue
	if err := spannerRow.ColumnByName(column.alias, &generic); err != nil {
		return reflect.Value{}, errors.Wrapf(err, "spanner.Row.ColumnByName(%s)", column.alias)
	}
	pointerCopy := column.nullable && !isNullableType(column.fieldType)
	if _, isNull := generic.Value.GetKind().(*structpb.Value_NullValue); isNull && pointerCopy {
		return reflect.Zero(reflect.PointerTo(column.fieldType)), nil
	}
	dest := reflect.New(column.fieldType)
	if err := generic.Decode(dest.Interface()); err != nil {
		return reflect.Value{}, errors.Wrapf(err, "spanner.GenericColumnValue.Decode(%s)", column.alias)
	}
	if pointerCopy {
		return dest, nil
	}

	return dest.Elem(), nil
}

var _ ReadOnlyTransactionCloser = (*SpannerReadOnlyTransaction)(nil)

// SpannerReadOnlyTransaction represents a database transaction that can only be used for reads.
type SpannerReadOnlyTransaction struct {
	txn              *spanner.ReadOnlyTransaction
	resourceRowIndex map[string]int
}

// newSpannerReadOnlyTransaction creates a new SpannerReadOnlyTransaction from a spanner.Client
func newSpannerReadOnlyTransaction(client *spanner.Client) ReadOnlyTransactionCloser {
	return &SpannerReadOnlyTransaction{
		txn:              client.ReadOnlyTransaction(),
		resourceRowIndex: make(map[string]int),
	}
}

// Close closes the readonly transaction
func (c *SpannerReadOnlyTransaction) Close() {
	c.txn.Close()
}

// SpannerReadOnlyTransaction returns a read-only transaction for the Spanner client.
func (c *SpannerReadOnlyTransaction) SpannerReadOnlyTransaction() spxapi.Querier {
	return c.txn
}

// PostgresReadOnlyTransaction panics because it is not implemented for the SpannerReadOnlyTransaction.
func (c *SpannerReadOnlyTransaction) PostgresReadOnlyTransaction() any {
	panic("SpannerReadOnlyTransaction.PostgresReadOnlyTransaction() should never be called.")
}

var _ ReadWriteTransaction = (*SpannerReadWriteTransaction)(nil)

// SpannerReadWriteTransaction represents a database transaction that can be used for both reads and writes.
// It records the resource and patch type of every patch it buffers, so a commit Spanner
// refuses can be answered in terms of what the transaction was asked to do; the file
// objects its patches release, so the executor deletes them once the commit lands; and
// the rows its patches write, so the request publishes them to the live pages once the
// commit lands.
type SpannerReadWriteTransaction struct {
	txn              *spanner.ReadWriteTransaction
	resourceRowIndex map[string]int
	buffered         *bufferedPatches
	released         *releasedKeys
	touchedRows      *touchedRows
	committed        *afterCommit
	// secrets are the secret keys of the client the transaction runs on, nil when it
	// holds none: a patch that sets a secret field is then refused.
	secrets *SecretKeys
	// directories notes what the transaction's writes do to tenants' directories and
	// directory-only switches, which its own reads do not see (TenantDirectoryRules).
	directories *directoryWrites
	// deleted notes the deletes to count over before the commit (delete_refusal.go).
	deleted *transactionDeletes
	// rows notes the columns the transaction's patches buffered and the point reads it
	// made, by which a change record finds its row's tenant and a group a row names is
	// held to it (tenant_binding.go).
	rows *transactionRows
	// snapshot is the read-only transaction a question transaction reads through, nil on
	// a transaction that writes. A question transaction has no read-write transaction
	// (txn is nil): what is buffered through it is dropped.
	snapshot *spanner.ReadOnlyTransaction
	// deleteSaveTime is how long one save of a delete that takes rows runs in the
	// transaction, the client's (WithDeleteSaveTime); DefaultDeleteSaveTime where it is
	// zero, as on a transaction wrapped by hand.
	deleteSaveTime time.Duration
	// question notes the question a delete asked on a question transaction, nil where none
	// did: the token a passed question gives names its record (SpannerClient.ask).
	question *askedQuestion
	// refusesOutside says a delete whose token does not hold ends the transaction
	// (errDeleteTokenRefused), so the executor asks its question with no read-write
	// transaction open: the first run of SpannerClient.ExecuteDeleteFunc. Elsewhere such a
	// delete asks inside the transaction.
	refusesOutside bool
	// repeatableRead notes that the transaction was opened at repeatable read, as a save of
	// a delete (DeleteRunSave); the tests read it, and no code branches on it.
	repeatableRead bool
	// honored notes the tokens the transaction's deletes went on, which the executor
	// deletes where the transaction does not commit; nil on a transaction with no executor.
	honored *honoredTokens
}

// NewSpannerReadWriteTransaction creates a new SpannerReadWriteTransaction from a
// spanner.ReadWriteTransaction. A transaction an application wraps itself and commits
// outside ExecuteFunc has no executor to delete the file objects its patches released:
// read them off the wrapper with Released after the commit and delete them from the
// store yourself. It also has no executor to hand the rows it wrote to the collector its
// context carries, nor to run what its writes registered for after the commit
// (AfterCommit): call Committed once the commit has landed, under a context that carries
// the collector (CollectTouchedRows) when the rows are to reach the live pages. It has
// no executor to count the rows under a record it deletes either: a
// delete the database refuses over them is answered by whoever translates the commit's
// error. And it has no client to take the application's secret keys from: a
// transaction whose patches set a secret field is given them with WithSecretKeys. A
// delete that asks first on it reads its question inside the transaction: only
// ExecuteDeleteFunc asks outside one.
func NewSpannerReadWriteTransaction(txn *spanner.ReadWriteTransaction) *SpannerReadWriteTransaction {
	return newSpannerReadWriteTransaction(txn, newBufferedPatches(), newReleasedKeys(), newTouchedRows(), newAfterCommit())
}

// newSpannerReadWriteTransaction wraps a transaction over the records its buffered
// patches, released file keys, written rows, and after-commit registrations are noted
// in; ExecuteFunc holds the same records when the commit comes back.
func newSpannerReadWriteTransaction(txn *spanner.ReadWriteTransaction, buffered *bufferedPatches, released *releasedKeys, touched *touchedRows, committed *afterCommit) *SpannerReadWriteTransaction {
	return &SpannerReadWriteTransaction{
		txn:              txn,
		resourceRowIndex: make(map[string]int),
		buffered:         buffered,
		released:         released,
		touchedRows:      touched,
		committed:        committed,
		secrets:          nil,
		directories:      newDirectoryWrites(),
		deleted:          newTransactionDeletes(),
		rows:             newTransactionRows(),
		snapshot:         nil,
		deleteSaveTime:   0,
		question:         nil,
		refusesOutside:   false,
		repeatableRead:   false,
		honored:          nil,
	}
}

// newSpannerQuestionTransaction makes a question transaction (asking) that reads through
// the read-only transaction given and holds the client's secret keys. It has no
// read-write transaction, and its notes are its own: nothing shares them.
func newSpannerQuestionTransaction(snapshot *spanner.ReadOnlyTransaction, secrets *SecretKeys) *SpannerReadWriteTransaction {
	question := newSpannerReadWriteTransaction(nil, newBufferedPatches(), newReleasedKeys(), newTouchedRows(), newAfterCommit()).WithSecretKeys(secrets)
	question.snapshot = snapshot

	return question
}

// reads is where the transaction's reads are sent: the read-write transaction itself,
// or, on a question transaction (asking), its read-only transaction.
func (c *SpannerReadWriteTransaction) reads() spxapi.Querier {
	if c.snapshot != nil {
		return c.snapshot
	}

	return c.txn
}

// asking reports a question transaction: the one a request that may hold a delete that
// asks first is run on before any read-write transaction is opened
// (SpannerClient.ExecuteDeleteFunc). It reads the database as it stood at one moment,
// locks nothing and writes nothing.
func (c *SpannerReadWriteTransaction) asking() bool {
	return c.snapshot != nil
}

// directoryWrites answers the transaction's note of what its writes do to tenants'
// directories and directory-only switches.
func (c *SpannerReadWriteTransaction) directoryWrites() *directoryWrites {
	return c.directories
}

// WithSecretKeys gives the transaction the application's secret keys, which it seals a
// patch's secret fields with, and returns the transaction. ExecuteFunc gives a
// transaction its client's; this is for one an application wraps itself
// (NewSpannerReadWriteTransaction).
func (c *SpannerReadWriteTransaction) WithSecretKeys(keys *SecretKeys) *SpannerReadWriteTransaction {
	c.secrets = keys

	return c
}

// secretKeys returns the secret keys the transaction seals with, nil when it holds none.
func (c *SpannerReadWriteTransaction) secretKeys() *SecretKeys {
	return c.secrets
}

// recordAfterCommit notes a function to run once the transaction has committed.
func (c *SpannerReadWriteTransaction) recordAfterCommit(key any, f func(ctx context.Context)) {
	c.committed.record(key, f)
}

// Committed hands the rows the transaction's patches wrote to the collector ctx carries
// (CollectTouchedRows), then runs what the transaction's writes registered for after the
// commit (AfterCommit), for a transaction committed outside ExecuteFunc, whose executor
// makes the calls itself. Call it once, after the commit has landed, and never for a
// transaction that did not commit.
func (c *SpannerReadWriteTransaction) Committed(ctx context.Context) {
	collectTouched(ctx, c.touched())
	c.committed.run(ctx)
}

// touched returns the rows the transaction's patches wrote so far.
func (c *SpannerReadWriteTransaction) touched() []touchedRow {
	return c.touchedRows.list()
}

// DBType returns the database type.
func (c *SpannerReadWriteTransaction) DBType() DBType {
	return SpannerDBType
}

// recordReleased notes file keys of one store the transaction's patches let go of.
func (c *SpannerReadWriteTransaction) recordReleased(store StoreName, keys ...string) {
	c.released.record(store, keys...)
}

// Released returns the file objects the transaction's patches released so far, each
// with its store: a deleted row's @file keys, and the old key of a row pointed at
// another object. Under ExecuteFunc the executor deletes them from the client's stores
// after the commit; a transaction committed outside it leaves that to its caller.
func (c *SpannerReadWriteTransaction) Released() []ReleasedKey {
	return c.released.list()
}

// nullifyNilPointers reads every typed nil pointer in the patch as the untyped nil the
// Spanner client encodes as NULL. A nullable column typed by a pointer to a type with
// Spanner methods (a struct on a JSON column, whose EncodeSpanner the generator writes
// on the value receiver so the type's own MarshalJSON is honored) would otherwise reach
// the client as a nil pointer that satisfies spanner.Encoder, and the client calls the
// value method through it.
func nullifyNilPointers(patch map[string]any) {
	for column, value := range patch {
		if rv := reflect.ValueOf(value); rv.Kind() == reflect.Pointer && rv.IsNil() {
			patch[column] = nil
		}
	}
}

// touchedRow reports whether a patch of this transaction wrote the given patch's row,
// and whether the last such write deleted it (an OrCreate or OrSkip patch asks, since no
// read sees a buffered write).
func (c *SpannerReadWriteTransaction) touchedRow(patch PatchSetMetadata) (written, deleted bool) {
	return c.touchedRows.find(patch)
}

// DataChangeEventIndex provides a sequence number for data change events on the same Resource inside the same transaction.
func (c *SpannerReadWriteTransaction) DataChangeEventIndex(res accesstypes.Resource, rowID string) int {
	indexID := fmt.Sprintf("%s_%s", res, rowID)
	c.resourceRowIndex[indexID]++

	return c.resourceRowIndex[indexID]
}

// SpannerReadOnlyTransaction returns what a read through the transaction is sent on
// (reads): the transaction itself, or the read-only transaction of a delete's question.
func (c *SpannerReadWriteTransaction) SpannerReadOnlyTransaction() spxapi.Querier {
	return c.reads()
}

// BufferMap buffers a map of changes to be applied to the database. On a question
// transaction (asking) it sends nothing, notes nothing and returns nil.
func (c *SpannerReadWriteTransaction) BufferMap(r PatchSetMetadata, patch map[string]any) error {
	if c.asking() {
		return nil
	}
	nullifyNilPointers(patch)

	var m *spanner.Mutation

	switch r.PatchType() {
	case CreatePatchType:
		m = spanner.InsertMap(string(r.Resource()), patch)
	case UpdatePatchType:
		m = spanner.UpdateMap(string(r.Resource()), patch)
	case DeletePatchType:
		m = spanner.Delete(string(r.Resource()), r.PrimaryKey().KeySet())
	case CreateOrUpdatePatchType:
		m = spanner.InsertOrUpdateMap(string(r.Resource()), patch)
	default:
		panic(fmt.Sprintf("unsupported operation: %s", r.PatchType()))
	}

	if err := c.txn.BufferWrite([]*spanner.Mutation{m}); err != nil {
		return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
	}
	c.buffered.record(r)
	c.touchedRows.record(r)
	// A change record is no row a later read of the transaction asks for, so the record
	// of the transaction's rows keeps nothing of it, as the two records above keep nothing.
	if _, isEvent := r.(*DataChangeEvent); !isEvent {
		c.rows.note(r.Resource(), r.PatchType(), rowMatchKey(r.PrimaryKey()), patchKeyByColumn(r), patch)
	}

	return nil
}

// BufferStruct buffers a struct of changes to be applied to the database. On a question
// transaction (asking) it sends nothing, notes nothing and returns nil.
func (c *SpannerReadWriteTransaction) BufferStruct(patch PatchSetMetadata) error {
	if c.asking() {
		return nil
	}
	var m *spanner.Mutation
	var err error

	switch patch.PatchType() {
	case CreatePatchType:
		m, err = spanner.InsertStruct(string(patch.Resource()), patch)
		if err != nil {
			return errors.Wrap(err, "spanner.InsertStruct()")
		}
	case UpdatePatchType:
		m, err = spanner.UpdateStruct(string(patch.Resource()), patch)
		if err != nil {
			return errors.Wrap(err, "spanner.UpdateStruct()")
		}
	case CreateOrUpdatePatchType:
		m, err = spanner.InsertOrUpdateStruct(string(patch.Resource()), patch)
		if err != nil {
			return errors.Wrap(err, "spanner.InsertOrUpdateStruct()")
		}
	default:
		panic(fmt.Sprintf("unsupported operation: %s", patch.PatchType()))
	}

	if err := c.txn.BufferWrite([]*spanner.Mutation{m}); err != nil {
		return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
	}
	c.buffered.record(patch)
	c.touchedRows.record(patch)

	return nil
}

// PostgresReadOnlyTransaction panics because it is not implemented for the SpannerReadWriteTransaction.
func (c *SpannerReadWriteTransaction) PostgresReadOnlyTransaction() any {
	panic("SpannerReadWriteTransaction.PostgresReadOnlyTransaction() should never be called.")
}
