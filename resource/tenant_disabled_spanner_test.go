package resource

import (
	"context"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// TestRequireTenantDisabled proves the rule of a tenant's delete over the emulator: a
// disabled station is deleted freely, a station that is not disabled is refused with a
// Conflict that says to disable it first, in the application's word for one tenant or
// "tenant" where it gives none, a station the table does not hold is refused nothing,
// and a station disabled in the same transaction is still refused, since the rule reads
// what the database held before the transaction's own writes.
func TestRequireTenantDisabled(t *testing.T) {
	t.Parallel()

	// alpha is disabled; beta and gamma are not, and gamma is the one a transaction
	// disables before it asks, so a commit of that write could reach no other case.
	db, client := tenantDatabase(t, "require-tenant-disabled", "alpha", "beta", "gamma")
	disableStation(t, db, "alpha", true)

	tests := []struct {
		name    string
		station accesstypes.Domain
		word    string
		// disableFirst buffers the station's disabled column set to true in the same
		// transaction before the rule is asked.
		disableFirst bool
		// wantRefusal is the Conflict's sentence; empty where the delete may go ahead.
		wantRefusal string
	}{
		{name: "a disabled station is deleted freely", station: "alpha", word: "station"},
		{name: "a station that is not disabled is refused, in the application's word", station: "beta", word: "station", wantRefusal: "Disable this station before deleting it."},
		{name: "a station that is not disabled is refused, as a tenant where the application gives no word", station: "beta", wantRefusal: "Disable this tenant before deleting it."},
		{name: "a station the table does not hold is refused nothing", station: "nowhere", word: "station"},
		{name: "a station disabled in the same transaction is still refused: the rule reads what the database held", station: "gamma", word: "station", disableFirst: true, wantRefusal: "Disable this station before deleting it."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := client.ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
				if tt.disableFirst {
					rw, ok := txn.(*SpannerReadWriteTransaction)
					if !ok {
						panic("the fixture writes through the Spanner transaction")
					}
					if err := rw.txn.BufferWrite([]*spanner.Mutation{spanner.Update(stationsTable, []string{stationsKey, "Disabled"}, []any{string(tt.station), true})}); err != nil {
						return errors.Wrap(err, "spanner.ReadWriteTransaction.BufferWrite()")
					}
				}

				return RequireTenantDisabled(ctx, txn, stationsTable, stationsKey, "Disabled", tt.station, tt.word)
			})
			if tt.wantRefusal == "" {
				if err != nil {
					t.Fatalf("RequireTenantDisabled() error = %v, want none", err)
				}

				return
			}
			// A Conflict answers 409, with its sentence for the person.
			if !httpio.HasConflict(err) || httpio.Message(err) != tt.wantRefusal {
				t.Fatalf("RequireTenantDisabled() error = %v, want the Conflict %q", err, tt.wantRefusal)
			}
		})
	}
}
