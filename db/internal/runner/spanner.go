package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"cloud.google.com/go/spanner"
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/internal/spannertxn"
	ccclogger "github.com/impulseframework/impulse/logger"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Spanner applies migrations to one Spanner database and keeps one version table. A DDL
// group goes up as one batch, which Spanner validates whole before applying anything and
// then applies in order; a DML group runs in one read-write transaction. The version row
// records how far a file got, so a failed file resumes from its failed statement.
//
// Two runs on one database apply every statement once between them. A run writes the
// version row only through Commit, which refuses when the row is not the one the run last
// read or wrote, so of two runs started together one takes each file and the other stops
// with ErrAnotherRun.
type Spanner struct {
	admin    *database.DatabaseAdminClient
	client   *spanner.Client
	database string
	table    string
}

// ErrAnotherRun is returned by a run that finds the version row is not the one it last read
// or wrote: another run took the file, or the version was forced. The run stops before
// applying anything more. Test for it with errors.Is.
var ErrAnotherRun = errors.New("another run, or a forced version, changed the version row under this run; it stops here and applies nothing more. Run it again when the other run has finished")

// NewSpanner returns a runner for the database at databasePath
// (projects/<p>/instances/<i>/databases/<d>) keeping its versions in table.
func NewSpanner(admin *database.DatabaseAdminClient, client *spanner.Client, databasePath, table string) *Spanner {
	return &Spanner{
		admin:    admin,
		client:   client,
		database: databasePath,
		table:    table,
	}
}

// The version table's columns. Version and Dirty are the old library's; the other three
// record the progress of a file that stopped, and are NULL when no file is in progress.
const (
	columnVersion    = "Version"
	columnDirty      = "Dirty"
	columnApplied    = "Applied"
	columnCheckpoint = "Checkpoint"
	columnOperation  = "Operation"
)

// Up applies every pending migration of the source, ascending. A dirty version resumes
// from its failed statement when the runner recorded progress and the file's applied part
// is unchanged; otherwise the run refuses and says how to recover. No pending migration
// is a normal result.
func (s *Spanner) Up(ctx context.Context, src *Source) error {
	if err := s.ensureTable(ctx); err != nil {
		return err
	}

	held, err := s.read(ctx)
	if err != nil {
		return err
	}

	current, resumed := -1, false
	if held.Known {
		current = held.Number
		switch {
		case held.Dirty:
			if err := s.resume(ctx, &held, src); err != nil {
				return err
			}
			resumed = true
		case !src.Has(current):
			return errors.Newf("the database is at version %d, which this build does not carry", current)
		}
	}

	pending := src.Pending(current)
	if len(pending) == 0 && !resumed {
		ccclogger.FromCtx(ctx).Infof("No change: the database is at %s and the source has nothing above it", held)

		return nil
	}

	for _, m := range pending {
		stmts, err := Split(m.SQL)
		if err != nil {
			return errors.Wrapf(err, "splitting %s", m.File)
		}
		if err := s.apply(ctx, &held, m, stmts, 0); err != nil {
			return err
		}
	}

	return nil
}

// resume continues the dirty version's file: an in-flight DDL operation is resolved into
// applied statements first, then the file's applied part is checked against the checkpoint.
func (s *Spanner) resume(ctx context.Context, held *Version, src *Source) error {
	file, ok := src.Up(held.Number)
	if !ok {
		_, err := Resume(*held, nil, nil)

		return err
	}

	stmts, err := Split(file.SQL)
	if err != nil {
		return errors.Wrapf(err, "splitting %s", file.File)
	}

	if held.Progress && held.Operation != "" {
		if err := s.resolveOperation(ctx, held, file, stmts); err != nil {
			return err
		}
	}

	start, err := Resume(*held, file, stmts)
	if err != nil {
		return err
	}

	return s.apply(ctx, held, file, stmts, start)
}

// resolveOperation settles the DDL operation a stopped run left named on the version row.
// When the name is one the runner makes, it first sends the group's batch under that name:
// Spanner accepts it if the stopped run never sent it, and answers that the operation
// exists if it did, so the batch is sent once either way. It then waits for the operation
// and counts its committed statements into Applied: all of them when it succeeded, its
// commit timestamps when it failed. The operation's statements must be the file's from
// Applied on; otherwise the file changed in its applied part.
func (s *Spanner) resolveOperation(ctx context.Context, held *Version, file *Migration, stmts []string) error {
	if err := s.sendRecorded(ctx, held, file, stmts); err != nil {
		return err
	}

	v := *held
	op := s.admin.UpdateDatabaseDdlOperation(v.Operation)
	waitErr := op.Wait(ctx)
	if !op.Done() {
		return errors.Wrapf(waitErr, "DDL operation %s, recorded for version %d, cannot be fetched; force the version that matches the database's real state", v.Operation, v.Number)
	}

	md, err := op.Metadata()
	if err != nil {
		return errors.Wrapf(err, "metadata of DDL operation %s", v.Operation)
	}

	n := len(md.GetCommitTimestamps())
	if waitErr == nil {
		n = len(md.GetStatements())
	}
	if v.Applied+n > len(stmts) || !slices.Equal(stmts[v.Applied:v.Applied+n], md.GetStatements()[:n]) {
		return errors.Newf("%s changed in the part already applied: DDL operation %s applied %s the file no longer carries at that place; restore the file or force a version",
			file.File, v.Operation, plural(n, "statement"))
	}

	v.Applied += n
	v.Checkpoint = Checkpoint(stmts[:v.Applied])
	v.Operation = ""
	if _, err := s.Commit(ctx, held, v, nil); err != nil {
		return err
	}
	ccclogger.FromCtx(ctx).Infof("DDL operation %s of %s had finished with %s applied", op.Name(), file.File, plural(n, "statement"))

	return nil
}

// sendRecorded sends the batch of the DDL group a stopped run named on the version row,
// under the recorded operation ID, when the name is the runner's own form and the file's
// next group is DDL. Another name was not made by the runner and is only fetched. A batch
// Spanner refuses applied nothing: the name comes off the row and the run stops with a
// DirtyError.
func (s *Spanner) sendRecorded(ctx context.Context, held *Version, file *Migration, stmts []string) error {
	id, ok := s.ownOperationID(held.Operation)
	if !ok {
		return nil
	}
	groups := Groups(stmts, held.Applied)
	if len(groups) == 0 || groups[0].Kind != DDL {
		return nil
	}
	g := groups[0]

	_, err := s.ddlBatch(ctx, g.Statements, id)
	switch {
	case err == nil:
		ccclogger.FromCtx(ctx).Infof("DDL operation %s of %s was recorded and never sent; it is sent now", held.Operation, file.File)

		return nil
	case status.Code(err) == codes.AlreadyExists:
		return nil
	}

	v := *held
	v.Operation = ""
	if _, commitErr := s.Commit(ctx, held, v, nil); commitErr != nil {
		return commitErr
	}

	return refused(file, stmts, g, err)
}

// apply runs a file's statements from index start, group by group, recording progress on
// the version row, and marks the version clean at the end.
func (s *Spanner) apply(ctx context.Context, held *Version, m *Migration, stmts []string, start int) error {
	log := ccclogger.FromCtx(ctx)

	if _, err := s.Commit(ctx, held, s.progress(m, stmts, start, ""), nil); err != nil {
		return err
	}
	switch {
	case start > 0 && start < len(stmts):
		log.Infof("Continuing %s from statement %d of %d", m.File, start+1, len(stmts))
	case start > 0:
		log.Infof("Finishing %s: all %s had been applied", m.File, plural(len(stmts), "statement"))
	}

	for _, g := range Groups(stmts, start) {
		var err error
		switch g.Kind {
		case DDL:
			err = s.applyDDL(ctx, held, m, stmts, g)
		case DML:
			err = s.applyDML(ctx, held, m, stmts, g)
		}
		if err != nil {
			return err
		}
	}

	if _, err := s.Commit(ctx, held, Version{Number: m.Version, Known: true}, nil); err != nil {
		return err
	}
	log.Infof("Applied %s (%s)", m.File, plural(len(stmts), "statement"))

	return nil
}

// progress is the version row for a file in progress with applied statements committed.
func (s *Spanner) progress(m *Migration, stmts []string, applied int, operation string) Version {
	return Version{
		Number:     m.Version,
		Known:      true,
		Dirty:      true,
		Progress:   true,
		Applied:    applied,
		Checkpoint: Checkpoint(stmts[:applied]),
		Operation:  operation,
	}
}

// applyDDL sends a DDL group as one batch under an operation ID of the run's own, whose
// name it records on the version row before it sends. Spanner accepts an operation ID once,
// so a batch whose name is on the row is sent once, whoever sends it. A synchronous refusal
// applied nothing, and the name comes off the row. A batch another run already sent under
// the name stops this run (ErrAnotherRun). On failure the operation's commit timestamps say
// how many statements applied.
func (s *Spanner) applyDDL(ctx context.Context, held *Version, m *Migration, stmts []string, g Group) error {
	id, err := newOperationID()
	if err != nil {
		return err
	}
	name := s.operationName(id)
	if _, err := s.Commit(ctx, held, s.progress(m, stmts, g.Start, name), nil); err != nil {
		return err
	}

	op, err := s.ddlBatch(ctx, g.Statements, id)
	if status.Code(err) == codes.AlreadyExists {
		return errors.Wrapf(ErrAnotherRun, "DDL operation %s of %s was sent by another run; version table %s", name, m.File, s.table)
	}
	if err != nil {
		if _, commitErr := s.Commit(ctx, held, s.progress(m, stmts, g.Start, ""), nil); commitErr != nil {
			return commitErr
		}

		return refused(m, stmts, g, err)
	}

	waitErr := op.Wait(ctx)
	if !op.Done() {
		return errors.Wrapf(waitErr, "waiting for DDL operation %s of %s; the next run resolves it", op.Name(), m.File)
	}
	if waitErr != nil {
		md, err := op.Metadata()
		if err != nil {
			return errors.Wrapf(err, "metadata of DDL operation %s", op.Name())
		}
		applied := g.Start + len(md.GetCommitTimestamps())
		if _, err := s.Commit(ctx, held, s.progress(m, stmts, applied, ""), nil); err != nil {
			return err
		}

		return &DirtyError{
			Version:   m.Version,
			File:      m.File,
			Applied:   applied,
			Total:     len(stmts),
			Statement: stmts[min(applied, len(stmts)-1)],
			Cause:     waitErr,
		}
	}

	_, err = s.Commit(ctx, held, s.progress(m, stmts, g.Start+len(g.Statements), ""), nil)

	return err
}

// refused is the error of a DDL group's batch Spanner refused when it was sent: nothing of
// it applied.
func refused(m *Migration, stmts []string, g Group, err error) *DirtyError {
	return &DirtyError{
		Version:   m.Version,
		File:      m.File,
		Applied:   g.Start,
		Total:     len(stmts),
		Statement: g.Statements[0],
		Cause:     errors.Wrapf(err, "Spanner refused the batch of %s, nothing applied", plural(len(g.Statements), "DDL statement")),
	}
}

// applyDML runs a DML group and records it on the version row in one read-write
// transaction: all of it applies and is recorded, or none.
func (s *Spanner) applyDML(ctx context.Context, held *Version, m *Migration, stmts []string, g Group) error {
	failed, err := s.Commit(ctx, held, s.progress(m, stmts, g.Start+len(g.Statements), ""), g.Statements)
	if errors.Is(err, ErrAnotherRun) {
		return err
	}
	if err != nil {
		return &DirtyError{
			Version:    m.Version,
			File:       m.File,
			Applied:    g.Start,
			Total:      len(stmts),
			RolledBack: failed,
			Statement:  g.Statements[min(failed, len(g.Statements)-1)],
			Cause:      err,
		}
	}

	return nil
}

// Down reverts versions from the current one to no version, running each version's down
// file if it has one. While a down file runs the version is marked dirty without progress,
// so a failure leaves the version to be forced by hand.
func (s *Spanner) Down(ctx context.Context, src *Source) error {
	log := ccclogger.FromCtx(ctx)

	if err := s.ensureTable(ctx); err != nil {
		return err
	}

	held, err := s.read(ctx)
	if err != nil {
		return err
	}
	if !held.Known {
		log.Info("No change: the database has no version to revert")

		return nil
	}
	if held.Dirty {
		return errors.Newf("the database is at %s; force the version that matches its real state before migrating down", held)
	}
	if !src.Has(held.Number) {
		return errors.Newf("the database is at version %d, which this build does not carry", held.Number)
	}

	current := held.Number
	for {
		if m, ok := src.Down(current); ok {
			if err := s.revert(ctx, &held, m); err != nil {
				return err
			}
			log.Infof("Reverted %s", m.File)
		}

		prev, ok := src.Prev(current)
		if !ok {
			_, err := s.Commit(ctx, &held, Version{}, nil)

			return err
		}
		if _, err := s.Commit(ctx, &held, Version{Number: prev, Known: true}, nil); err != nil {
			return err
		}
		current = prev
	}
}

// revert runs a down file with the version marked dirty and no progress recorded.
func (s *Spanner) revert(ctx context.Context, held *Version, m *Migration) error {
	stmts, err := Split(m.SQL)
	if err != nil {
		return errors.Wrapf(err, "splitting %s", m.File)
	}

	if _, err := s.Commit(ctx, held, Version{Number: m.Version, Known: true, Dirty: true}, nil); err != nil {
		return err
	}

	for _, g := range Groups(stmts, 0) {
		if err := s.runGroup(ctx, held, g); err != nil {
			return errors.Wrapf(err, "%s failed; the database is dirty at version %d, force the version that matches its real state", m.File, m.Version)
		}
	}

	return nil
}

// runGroup runs a group without recording progress.
func (s *Spanner) runGroup(ctx context.Context, held *Version, g Group) error {
	switch g.Kind {
	case DDL:
		op, err := s.ddlBatch(ctx, g.Statements, "")
		if err != nil {
			return err
		}
		if err := op.Wait(ctx); err != nil {
			return errors.Wrapf(err, "DDL operation %s", op.Name())
		}
	case DML:
		if _, err := s.Commit(ctx, held, *held, g.Statements); err != nil {
			return err
		}
	}

	return nil
}

// ddlBatch sends DDL statements as one batch and returns the accepted operation.
// operationID names the operation; empty leaves the name to Spanner.
func (s *Spanner) ddlBatch(ctx context.Context, stmts []string, operationID string) (*database.UpdateDatabaseDdlOperation, error) {
	op, err := s.admin.UpdateDatabaseDdl(ctx, &databasepb.UpdateDatabaseDdlRequest{
		Database:    s.database,
		Statements:  stmts,
		OperationId: operationID,
	})
	if err != nil {
		return nil, errors.Wrap(err, "DatabaseAdminClient.UpdateDatabaseDdl()")
	}

	return op, nil
}

// operationIDPrefix begins every operation ID the runner makes. Spanner's own IDs begin
// with an underscore, and an ID must match [a-z][a-z0-9_]*.
const operationIDPrefix = "migrate_"

// ownOperationIDPattern is the form of an operation ID the runner makes: the prefix and 32
// lower-case hex digits.
var ownOperationIDPattern = regexp.MustCompile(`^` + operationIDPrefix + `[0-9a-f]{32}$`)

// newOperationID makes an operation ID of the runner's own form from 16 random bytes.
func newOperationID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", errors.Wrap(err, "rand.Read()")
	}

	return operationIDPrefix + hex.EncodeToString(b), nil
}

// operationName is the name Spanner gives the operation of a batch sent under id.
func (s *Spanner) operationName(id string) string {
	return s.database + "/operations/" + id
}

// ownOperationID returns the operation ID of a recorded name when the name is one the
// runner makes for this database. Any other text in the column is not the runner's: a name
// Spanner made, or whatever a person forced into it.
func (s *Spanner) ownOperationID(name string) (string, bool) {
	id, ok := strings.CutPrefix(name, s.operationName(""))
	if !ok || !ownOperationIDPattern.MatchString(id) {
		return "", false
	}

	return id, true
}

// Commit runs the DML statements, if any, and replaces the version row with next, in one
// read-write transaction at the serializable isolation level (spannertxn.Serializable),
// provided the row is still held: the row as this run last read or wrote it. The
// transaction reads the row first; when it is not held, nothing is run or written and the
// error is ErrAnotherRun. So every write of a run is decided by a read in its own
// transaction, and of two runs that hold the same row one commits and the other stops. On
// success held becomes next. When a statement fails nothing is applied, the row stays, and
// the number of statements that ran before it comes back with the error.
func (s *Spanner) Commit(ctx context.Context, held *Version, next Version, stmts []string) (failed int, err error) {
	batch := make([]spanner.Statement, len(stmts))
	for i, sql := range stmts {
		batch[i] = spanner.Statement{SQL: sql}
	}

	_, err = spannertxn.Serializable(ctx, s.client, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		failed = 0
		row, err := s.readWith(ctx, txn)
		if err != nil {
			return err
		}
		if row != *held {
			return ErrAnotherRun
		}

		if len(batch) > 0 {
			counts, err := txn.BatchUpdate(ctx, batch)
			if err != nil {
				failed = len(counts)

				return errors.Wrap(err, "spanner.ReadWriteTransaction.BatchUpdate()")
			}
		}

		return txn.BufferWrite(s.mutations(next))
	})
	if err != nil {
		if errors.Is(err, ErrAnotherRun) {
			return 0, errors.Wrapf(ErrAnotherRun, "version table %s", s.table)
		}

		return failed, errors.Wrap(err, "spannertxn.Serializable()")
	}
	*held = next

	return 0, nil
}

// Version reads the version table, creating it when the database has none.
func (s *Spanner) Version(ctx context.Context) (Version, error) {
	if err := s.ensureTable(ctx); err != nil {
		return Version{}, err
	}

	return s.read(ctx)
}

// Force sets the version clean, with no progress recorded. Version -1 deletes the row, so
// the database has no version. It writes whatever the row holds: forcing is the operator's
// word on the database's real state. A run in progress stops with ErrAnotherRun at its next
// write.
func (s *Spanner) Force(ctx context.Context, version int) error {
	if version < -1 {
		return errors.Newf("version %d cannot be forced: versions are 0 or above, and -1 means no version", version)
	}

	if err := s.ensureTable(ctx); err != nil {
		return err
	}

	v := Version{}
	if version >= 0 {
		v = Version{Number: version, Known: true}
	}
	if err := s.write(ctx, v); err != nil {
		return err
	}
	ccclogger.FromCtx(ctx).Infof("Forced %s to %s", s.table, v)

	return nil
}

// ensureTable creates the version table, or adds the progress columns to one the old
// library created.
func (s *Spanner) ensureTable(ctx context.Context) error {
	columns, err := s.columns(ctx)
	if err != nil {
		return err
	}

	// The progress columns, added to a version table the old library created.
	progressColumns := []struct {
		name string
		ddl  string
	}{
		{name: columnApplied, ddl: "Applied INT64"},
		{name: columnCheckpoint, ddl: "Checkpoint STRING(64)"},
		{name: columnOperation, ddl: "Operation STRING(MAX)"},
	}

	var ddl []string
	if len(columns) == 0 {
		ddl = append(ddl, fmt.Sprintf("CREATE TABLE %#q (Version INT64 NOT NULL, Dirty BOOL NOT NULL, Applied INT64, Checkpoint STRING(64), Operation STRING(MAX)) PRIMARY KEY (Version)", s.table))
	} else {
		for _, column := range progressColumns {
			if !slices.Contains(columns, column.name) {
				ddl = append(ddl, fmt.Sprintf("ALTER TABLE %#q ADD COLUMN %s", s.table, column.ddl))
			}
		}
	}
	if len(ddl) == 0 {
		return nil
	}

	op, err := s.ddlBatch(ctx, ddl, "")
	if err != nil {
		return errors.Wrapf(err, "creating the version table %s", s.table)
	}
	if err := op.Wait(ctx); err != nil {
		return errors.Wrapf(err, "creating the version table %s: DDL operation %s", s.table, op.Name())
	}

	return nil
}

// columns lists the version table's columns; none means the table does not exist.
func (s *Spanner) columns(ctx context.Context) ([]string, error) {
	stmt := spanner.Statement{
		SQL:    "SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_CATALOG = '' AND TABLE_SCHEMA = '' AND TABLE_NAME = @table",
		Params: map[string]any{"table": s.table},
	}

	var columns []string
	if err := s.client.Single().Query(ctx, stmt).Do(func(row *spanner.Row) error {
		var name string
		if err := row.Columns(&name); err != nil {
			return errors.Wrap(err, "spanner.Row.Columns()")
		}
		columns = append(columns, name)

		return nil
	}); err != nil {
		return nil, errors.Wrapf(err, "reading the columns of %s", s.table)
	}

	return columns, nil
}

// read returns the version row. No row, or the old library's -1 row, is no version.
func (s *Spanner) read(ctx context.Context) (Version, error) {
	return s.readWith(ctx, s.client.Single())
}

// querier runs a query: a single-use read, or a read-write transaction.
type querier interface {
	Query(ctx context.Context, statement spanner.Statement) *spanner.RowIterator
}

// readWith reads the version row with q, as read describes it.
func (s *Spanner) readWith(ctx context.Context, q querier) (Version, error) {
	stmt := spanner.Statement{SQL: fmt.Sprintf("SELECT %s FROM %#q LIMIT 1", strings.Join([]string{columnVersion, columnDirty, columnApplied, columnCheckpoint, columnOperation}, ", "), s.table)}
	iter := q.Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if errors.Is(err, iterator.Done) {
		return Version{}, nil
	}
	if err != nil {
		return Version{}, errors.Wrapf(err, "reading the version from %s", s.table)
	}

	var (
		version    int64
		dirty      bool
		applied    spanner.NullInt64
		checkpoint spanner.NullString
		operation  spanner.NullString
	)
	if err := row.Columns(&version, &dirty, &applied, &checkpoint, &operation); err != nil {
		return Version{}, errors.Wrapf(err, "reading the version row of %s", s.table)
	}
	if version < 0 {
		return Version{}, nil
	}

	return Version{
		Number:     int(version),
		Known:      true,
		Dirty:      dirty,
		Progress:   applied.Valid,
		Applied:    int(applied.Int64),
		Checkpoint: checkpoint.StringVal,
		Operation:  operation.StringVal,
	}, nil
}

// write replaces the version row: every row is deleted and, for a known version, one is
// inserted, in one commit. Only Force writes this way, whatever the row holds; a run writes
// through Commit.
func (s *Spanner) write(ctx context.Context, v Version) error {
	if _, err := s.client.Apply(ctx, s.mutations(v)); err != nil {
		return errors.Wrapf(err, "writing %s to %s", v, s.table)
	}

	return nil
}

// mutations replace the version row with v: every row is deleted and, for a known version,
// one is inserted.
func (s *Spanner) mutations(v Version) []*spanner.Mutation {
	mutations := []*spanner.Mutation{spanner.Delete(s.table, spanner.AllKeys())}
	if v.Known {
		mutations = append(mutations, spanner.Insert(s.table,
			[]string{columnVersion, columnDirty, columnApplied, columnCheckpoint, columnOperation},
			[]any{
				int64(v.Number),
				v.Dirty,
				spanner.NullInt64{Int64: int64(v.Applied), Valid: v.Progress},
				spanner.NullString{StringVal: v.Checkpoint, Valid: v.Progress},
				spanner.NullString{StringVal: v.Operation, Valid: v.Operation != ""},
			},
		))
	}

	return mutations
}
