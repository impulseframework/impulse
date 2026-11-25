package resource

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/spxscan"
)

// errTenantDisabledUnsupportedDatabase is the answer on a database whether a tenant is
// disabled is not read on.
var errTenantDisabledUnsupportedDatabase = errors.New("resource: whether a tenant is disabled is read on Spanner only")

// RequireTenantDisabled refuses the delete of a tenant that is not disabled, in the
// caller's transaction, and writes nothing: a tenant is disabled before it is deleted,
// so nobody of it is at work in it while its records go. The refusal is a Conflict that
// says to disable it first. It reads what the database held before the transaction's
// own writes, so a request that disables a tenant and deletes it at once is refused. A
// tenant the table does not hold is refused nothing. The generated delete of the tenant
// record calls it before the row's removal is buffered; an application that deletes a
// tenant in a transaction of its own calls it where it wants the rule.
//
// table, keyColumn and disabledColumn are the tenant record's table, its key column and
// its @tenantDisabled column; word is what the application calls one tenant ("tenant",
// "sector"), "tenant" when empty.
func RequireTenantDisabled(ctx context.Context, txn ReadWriteTransaction, table, keyColumn, disabledColumn string,
	tenant accesstypes.Domain, word string,
) error {
	if txn.DBType() != SpannerDBType {
		return errTenantDisabledUnsupportedDatabase
	}
	if word == "" {
		word = "tenant"
	}
	g := newSQLGenerator(Spanner)
	statement := spanner.Statement{
		SQL: fmt.Sprintf("SELECT %s AS Disabled FROM %s WHERE CAST(%s AS %s) = @%s",
			g.quoteIdentifier(disabledColumn), g.quoteIdentifier(table), g.quoteIdentifier(keyColumn), columnTypeString, domainParamName),
		Params: map[string]any{domainParamName: string(tenant)},
	}
	var rows []struct {
		Disabled bool `spanner:"Disabled"`
	}
	if err := spxscan.Select(ctx, txn.SpannerReadOnlyTransaction(), &rows, statement); err != nil {
		return errors.Wrap(err, "spxscan.Select()")
	}
	for _, row := range rows {
		if !row.Disabled {
			return httpio.NewConflictMessagef("Disable this %s before deleting it.", word)
		}
	}

	return nil
}
