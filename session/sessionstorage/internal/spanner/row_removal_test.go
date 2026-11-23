package spanner

import (
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/drivertest"
)

// TestRowDeletionPolicies proves the shipped base and auth sets leave a row deletion
// policy on each table that would otherwise keep every row ever written: the sessions
// table counted from the last activity, the sign-in attempts table from the moment a row
// stops counting, and the mail links table from a link's expiry.
func TestRowDeletionPolicies(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, authSources(drivertest.Auth)...)
	if err != nil {
		t.Fatalf("prepareDatabase() error = %v", err)
	}

	stmt := spanner.NewStatement(`SELECT TABLE_NAME, ROW_DELETION_POLICY_EXPRESSION FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = '' AND ROW_DELETION_POLICY_EXPRESSION IS NOT NULL`)
	got := map[string]string{}
	if err := db.Client.Single().Query(ctx, stmt).Do(func(r *spanner.Row) error {
		var table, expression string
		if err := r.Columns(&table, &expression); err != nil {
			return err
		}
		got[table] = policyText(expression)

		return nil
	}); err != nil {
		t.Fatalf("Query() error = %v", err)
	}

	want := map[string]string{
		"Sessions":              policyText("OLDER_THAN(UpdatedAt, INTERVAL 30 DAY)"),
		"SessionSignInAttempts": policyText("OLDER_THAN(EndsAt, INTERVAL 1 DAY)"),
		"SessionMailLinks":      policyText("OLDER_THAN(ExpiresAt, INTERVAL 30 DAY)"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("row deletion policies mismatch (-want +got):\n%s", diff)
	}
}

// policyText is a policy expression with its spacing and letter case set aside.
func policyText(expression string) string {
	return strings.ToUpper(strings.Join(strings.Fields(expression), ""))
}
