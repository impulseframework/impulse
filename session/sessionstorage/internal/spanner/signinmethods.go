package spanner

import (
	"context"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/tracer"
	"google.golang.org/api/iterator"
)

// signInMethodColumns are the sign-in methods table's columns.
var signInMethodColumns = []string{"Name", "OpenSignUp", "DefaultRole", updatedAtColumn}

// signInMethodRow is a sign-in method's row as the table holds it.
type signInMethodRow struct {
	Name        string             `spanner:"Name"`
	OpenSignUp  bool               `spanner:"OpenSignUp"`
	DefaultRole spanner.NullString `spanner:"DefaultRole"`
	UpdatedAt   time.Time          `spanner:"UpdatedAt"`
}

func (r *signInMethodRow) record() *dbtype.SignInMethod {
	rec := &dbtype.SignInMethod{Name: r.Name, OpenSignUp: r.OpenSignUp, UpdatedAt: r.UpdatedAt}
	if r.DefaultRole.Valid {
		role := r.DefaultRole.StringVal
		rec.DefaultRole = &role
	}

	return rec
}

// readSignInMethods reads the rows of keys from the sign-in methods table through read.
// A key with no row yields nothing; a read that fails, as it does when the table is
// missing, is returned naming the table, never taken for "no row", since no row is a
// method that is invite only.
func readSignInMethods(ctx context.Context, read rowReader, table string, keys spanner.KeySet) ([]*dbtype.SignInMethod, error) {
	iter := read(ctx, table, keys, signInMethodColumns)
	defer iter.Stop()

	var methods []*dbtype.SignInMethod
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return methods, nil
		}
		if err != nil {
			return nil, errors.Wrapf(err, "reading %s", table)
		}
		var r signInMethodRow
		if err := row.ToStruct(&r); err != nil {
			return nil, errors.Wrap(err, "spanner.Row.ToStruct()")
		}
		methods = append(methods, r.record())
	}
}

// SignInMethodIn reads the row of the sign-in method registered under name inside tx,
// or nil when the method has none, which is a method that is invite only.
func (s *SessionStorageDriver) SignInMethodIn(ctx context.Context, tx *spanner.ReadWriteTransaction, name string) (*dbtype.SignInMethod, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	methods, err := readSignInMethods(ctx, tx.Read, s.signInMethodsTableName, spanner.Key{name})
	if err != nil {
		return nil, err
	}
	if len(methods) == 0 {
		return nil, nil
	}

	return methods[0], nil
}

// SignInMethods lists every row of the sign-in methods table, by name: the methods an
// administrator has made a choice for. A method with no row is not among them.
func (s *SessionStorageDriver) SignInMethods(ctx context.Context) ([]*dbtype.SignInMethod, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return readSignInMethods(ctx, s.spanner.Single().Read, s.signInMethodsTableName, spanner.AllKeys())
}

// SetSignInMethod writes the row of the sign-in method m names, replacing the one it
// had, with UpdatedAt the commit time.
func (s *SessionStorageDriver) SetSignInMethod(ctx context.Context, m *dbtype.SignInMethod) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	row := map[string]any{"Name": m.Name, "OpenSignUp": m.OpenSignUp, "DefaultRole": spanner.NullString{}, updatedAtColumn: spanner.CommitTimestamp}
	if m.DefaultRole != nil {
		row["DefaultRole"] = spanner.NullString{StringVal: *m.DefaultRole, Valid: true}
	}
	if _, err := s.spanner.Apply(ctx, []*spanner.Mutation{spanner.InsertOrUpdateMap(s.signInMethodsTableName, row)}); err != nil {
		return errors.Wrap(err, "spanner.Client.Apply()")
	}

	return nil
}
