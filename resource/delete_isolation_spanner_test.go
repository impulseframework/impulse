package resource

import (
	"context"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/internal/spannertxn"
	"google.golang.org/grpc/codes"
)

// TestRepeatableRead_onTheEmulator runs a transaction at repeatable read on the emulator
// every test of a save runs on: it reads a row by a query and buffers the insert of
// another, and both rows are there after the commit.
func TestRepeatableRead_onTheEmulator(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "repeatable-read")
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.Insert("CascadeProjects", []string{"Id"}, []any{"read-first"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	var read []string
	_, err := spannertxn.RepeatableRead(t.Context(), db, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		read = nil
		if err := txn.Query(ctx, spanner.Statement{
			SQL:    "SELECT Id FROM CascadeProjects WHERE Id = @id",
			Params: map[string]any{"id": "read-first"},
		}).Do(func(row *spanner.Row) error {
			var id string
			if err := row.Columns(&id); err != nil {
				return errors.Wrap(err, "spanner.Row.Columns()")
			}
			read = append(read, id)

			return nil
		}); err != nil {
			return errors.Wrap(err, "spanner.RowIterator.Do()")
		}

		if err := txn.BufferWrite([]*spanner.Mutation{
			spanner.Insert("CascadeProjects", []string{"Id"}, []any{"written-after"}),
		}); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("spannertxn.RepeatableRead() error = %v, want the insert committed", err)
	}
	if len(read) != 1 || read[0] != "read-first" {
		t.Errorf("spannertxn.RepeatableRead() read %v, want [read-first]", read)
	}
	for _, id := range []string{"read-first", "written-after"} {
		if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeProjects WHERE Id = @project", id); got != 1 {
			t.Errorf("spannertxn.RepeatableRead(): %d projects %q after the commit, want one", got, id)
		}
	}
}

// TestRepeatableRead_aForeignKeyStillRefuses deletes a client an order still names by a
// foreign key with no action on delete, at repeatable read and at the serializable level:
// the database refuses both commits with the same code, and both rows stay.
func TestRepeatableRead_aForeignKeyStillRefuses(t *testing.T) {
	t.Parallel()

	db := cascadeDatabase(t, "repeatable-read-fk")
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{
		spanner.Insert("CascadeClients", []string{"Id"}, []any{"named"}),
		spanner.Insert("CascadeOrders", []string{"Id", "ClientId"}, []any{"naming", "named"}),
	}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
	deleteClient := func(_ context.Context, txn *spanner.ReadWriteTransaction) error {
		if err := txn.BufferWrite([]*spanner.Mutation{spanner.Delete("CascadeClients", spanner.Key{"named"})}); err != nil {
			return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
		}

		return nil
	}
	_, repeatable := spannertxn.RepeatableRead(t.Context(), db, deleteClient)
	_, serializable := spannertxn.Serializable(t.Context(), db, deleteClient)
	if repeatable == nil {
		t.Fatal("spannertxn.RepeatableRead() error = nil, want the foreign key to refuse the commit")
	}
	if serializable == nil {
		t.Fatal("spannertxn.Serializable() error = nil, want the foreign key to refuse the commit")
	}
	if got, want := spanner.ErrCode(repeatable), spanner.ErrCode(serializable); got != want {
		t.Errorf("spannertxn.RepeatableRead() error code = %v, want %v as at the serializable level (%v)", got, want, repeatable)
	}
	if got := spanner.ErrCode(repeatable); got != codes.FailedPrecondition {
		t.Errorf("spannertxn.RepeatableRead() error code = %v, want %v (%v)", got, codes.FailedPrecondition, repeatable)
	}
	if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeClients WHERE Id = @project", "named"); got != 1 {
		t.Errorf("%d clients named after the refused deletes, want one", got)
	}
	if got := roundCount(t, db, "SELECT COUNT(*) FROM CascadeOrders WHERE ClientId = @project", "named"); got != 1 {
		t.Errorf("%d orders naming the client after the refused deletes, want one", got)
	}
}
