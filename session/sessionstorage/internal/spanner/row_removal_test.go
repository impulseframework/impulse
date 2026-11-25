package spanner

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/drivertest"
)

// TestRowDeletionPolicies proves the shipped base and auth sets leave a row deletion
// policy on each table that would otherwise keep every row ever written: the sessions
// table counted from the last activity, the sign-in attempts table from the moment a row
// stops counting, and the mail links, tokens, trusted devices, device codes and pending
// sign-ins tables from a row's expiry.
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
			return errors.Wrap(err, "spanner.Row.Columns()")
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
		"SessionTokens":         policyText("OLDER_THAN(ExpiresAt, INTERVAL 30 DAY)"),
		"SessionTrustedDevices": policyText("OLDER_THAN(ExpiresAt, INTERVAL 30 DAY)"),
		"SessionDeviceCodes":    policyText("OLDER_THAN(ExpiresAt, INTERVAL 1 DAY)"),
		"SessionPendingSignIns": policyText("OLDER_THAN(ExpiresAt, INTERVAL 1 DAY)"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("row deletion policies mismatch (-want +got):\n%s", diff)
	}
}

// olderThan takes the column and the number of days out of a row deletion policy.
var olderThan = regexp.MustCompile(`(?i)^\s*OLDER_THAN\s*\(\s*(\w+)\s*,\s*INTERVAL\s+(\d+)\s+DAY\s*\)\s*$`)

// TestRowDeletionPolicies_takeTheRowsPastTheirPeriod holds the policy of each of the four
// tables whose rows are removed after they expire against rows the driver wrote at five
// expiries. It reads the policy as the database reports it, takes its column and its
// days, and selects the rows that are older than that: the query is the documented
// meaning of OLDER_THAN, written out by the test, and it finds exactly the rows past
// their period. It does not show the removal: the emulator removes no row, and the
// removal itself is read on Cloud Spanner in the lab.
func TestRowDeletionPolicies_takeTheRowsPastTheirPeriod(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
	user, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: "pat@lakeside.edu"}, nil)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	// Spanner's inserts remove nothing, so the past expiries are written as given.
	expiries := []time.Duration{-744 * time.Hour, -696 * time.Hour, -25 * time.Hour, -23 * time.Hour, time.Hour}
	rows := make([]drivertest.Expiring, len(expiries))
	for i, expiry := range expiries {
		rows[i] = drivertest.InsertExpiring(ctx, t, in.Driver, user.ID, byte(i+1), time.Now().Add(expiry))
	}

	for _, tt := range []struct {
		name  string
		table string
		id    func(*drivertest.Expiring) impulse.UUID
		// due says, of each row by its expiry, whether it is past its period.
		due []bool
	}{
		{name: "tokens", table: "SessionTokens", id: expiringToken, due: []bool{true, false, false, false, false}},
		{name: "trusted devices", table: "SessionTrustedDevices", id: expiringTrustedDevice, due: []bool{true, false, false, false, false}},
		{name: "device codes", table: "SessionDeviceCodes", id: expiringDeviceCode, due: []bool{true, true, true, false, false}},
		{name: "pending sign-ins", table: "SessionPendingSignIns", id: expiringPendingSignIn, due: []bool{true, true, true, false, false}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			c := client(t, in.Raw)
			stmt := spanner.NewStatement(`SELECT ROW_DELETION_POLICY_EXPRESSION FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = '' AND TABLE_NAME = @table`)
			stmt.Params["table"] = tt.table
			var policy spanner.NullString
			if err := c.Single().Query(ctx, stmt).Do(func(r *spanner.Row) error {
				if err := r.Columns(&policy); err != nil {
					return errors.Wrap(err, "spanner.Row.Columns()")
				}

				return nil
			}); err != nil {
				t.Fatalf("Query() of the policy error = %v", err)
			}
			m := olderThan.FindStringSubmatch(policy.StringVal)
			if m == nil {
				t.Fatalf("the table %s has the row deletion policy %q, want OLDER_THAN(<column>, INTERVAL <days> DAY)", tt.table, policy.StringVal)
			}

			due := spanner.NewStatement(fmt.Sprintf(`SELECT Id FROM %s WHERE %s < TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL %s DAY)`, tt.table, m[1], m[2]))
			got := map[string]bool{}
			if err := c.Single().Query(ctx, due).Do(func(r *spanner.Row) error {
				var id string
				if err := r.Columns(&id); err != nil {
					return errors.Wrap(err, "spanner.Row.Columns()")
				}
				got[id] = true

				return nil
			}); err != nil {
				t.Fatalf("Query() of the rows past the period error = %v", err)
			}
			want := map[string]bool{}
			for i := range rows {
				if tt.due[i] {
					want[tt.id(&rows[i]).String()] = true
				}
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("the rows of %s past its policy's period (%s) mismatch (-want +got):\n%s", tt.table, policy.StringVal, diff)
			}
		})
	}
}

// expiringToken is the token of e.
func expiringToken(e *drivertest.Expiring) impulse.UUID {
	return e.Token
}

// expiringTrustedDevice is the trusted device of e.
func expiringTrustedDevice(e *drivertest.Expiring) impulse.UUID {
	return e.TrustedDevice
}

// expiringDeviceCode is the device code of e.
func expiringDeviceCode(e *drivertest.Expiring) impulse.UUID {
	return e.DeviceCode
}

// expiringPendingSignIn is the pending sign-in of e.
func expiringPendingSignIn(e *drivertest.Expiring) impulse.UUID {
	return e.PendingSignIn
}

// policyText is a policy expression with its spacing and letter case set aside.
func policyText(expression string) string {
	return strings.ToUpper(strings.Join(strings.Fields(expression), ""))
}
