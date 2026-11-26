package spanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/drivertest"
)

// auditRows reads every row of the audit records table directly, the oldest first, each
// by its columns' names, with the columns that hold nothing left out.
func auditRows(ctx context.Context, t *testing.T, raw any) []map[string]any {
	t.Helper()

	names := auditlog.Columns()
	stmt := spanner.NewStatement(fmt.Sprintf(`SELECT %s FROM SessionAuditRecords ORDER BY CreatedAt, Id`, strings.Join(names, ", ")))
	var rows []map[string]any
	if err := client(t, raw).Single().Query(ctx, stmt).Do(func(r *spanner.Row) error {
		row := map[string]any{}
		for i, name := range names {
			switch name {
			case "OtherRoleNames":
				var texts []spanner.NullString
				if err := r.Column(i, &texts); err != nil {
					return errors.Wrap(err, "spanner.Row.Column()")
				}
				if texts != nil {
					values := make([]string, 0, len(texts))
					for _, text := range texts {
						values = append(values, text.StringVal)
					}
					row[name] = values
				}
			case "CreatedAt", "PeriodStart", "PeriodEnd":
				var at spanner.NullTime
				if err := r.Column(i, &at); err != nil {
					return errors.Wrap(err, "spanner.Row.Column()")
				}
				if at.Valid {
					row[name] = at.Time.UTC()
				}
			default:
				var text spanner.NullString
				if err := r.Column(i, &text); err != nil {
					return errors.Wrap(err, "spanner.Row.Column()")
				}
				if text.Valid {
					row[name] = text.StringVal
				}
			}
		}
		rows = append(rows, row)

		return nil
	}); err != nil {
		t.Fatalf("Query() error = %v", err)
	}

	return rows
}

// TestAuditRecords_RowDeletionPolicy proves that the emulator takes, on the shipped audit
// records table, the row deletion policy auditlog.SpannerDDL writes for a period of days:
// the statement an application's migration carries to keep its records 90 days. The
// removal itself is Cloud Spanner's, read in the lab.
func TestAuditRecords_RowDeletionPolicy(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	db, err := prepareDatabase(ctx, t, authSources(drivertest.Auth)...)
	if err != nil {
		t.Fatalf("prepareDatabase() error = %v", err)
	}
	statements := auditlog.SpannerDDL("SessionAuditRecords", auditlog.KeepDays(90))
	policy := statements[len(statements)-1]
	if !strings.Contains(policy, "ROW DELETION POLICY") {
		t.Fatalf("SpannerDDL()'s last statement = %q, want the row deletion policy", policy)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "000001_KeepAuditRecords.up.sql"), []byte(policy+";\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if err := db.MigrateUp("file://" + dir); err != nil {
		t.Fatalf("MigrateUp() of the policy error = %v", err)
	}

	stmt := spanner.NewStatement(`SELECT ROW_DELETION_POLICY_EXPRESSION FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = '' AND TABLE_NAME = 'SessionAuditRecords'`)
	var expression spanner.NullString
	if err := db.Client.Single().Query(ctx, stmt).Do(func(r *spanner.Row) error {
		if err := r.Column(0, &expression); err != nil {
			return errors.Wrap(err, "spanner.Row.Column()")
		}

		return nil
	}); err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if got, want := policyText(expression.StringVal), policyText("OLDER_THAN(CreatedAt, INTERVAL 90 DAY)"); got != want {
		t.Errorf("the audit records table's row deletion policy = %q, want %q", expression.StringVal, want)
	}
}

// TestSetAuditRecordsPeriod_refused holds a period handed to the Spanner store to a
// refusal that says what to write in its place: the table's own row deletion policy
// removes old rows there, so a period of days names the statement auditlog.SpannerDDL
// gives for the table, and a period of forever says that a table with no policy keeps
// every row. The store keeps no period either way.
func TestSetAuditRecordsPeriod_refused(t *testing.T) {
	t.Parallel()

	var d SessionStorageDriver
	if err := d.SetAuditRecordsTableName("CrewSessionAuditRecords"); err != nil {
		t.Fatalf("SetAuditRecordsTableName() error = %v", err)
	}
	statements := auditlog.SpannerDDL("CrewSessionAuditRecords", auditlog.KeepDays(90))
	for _, tt := range []struct {
		name   string
		period auditlog.Period
		want   []string
	}{
		{name: "a period of days", period: auditlog.KeepDays(90), want: []string{"session.KeepAuditRecords does nothing on Spanner", statements[len(statements)-1]}},
		{name: "forever", period: auditlog.KeepForever(), want: []string{"session.KeepAuditRecords does nothing on Spanner", "keeps every row"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := d.SetAuditRecordsPeriod(tt.period)
			if err == nil {
				t.Fatalf("SetAuditRecordsPeriod() error = nil, want the refusal")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("SetAuditRecordsPeriod() error = %q, want it to say %q", err, want)
				}
			}
		})
	}
}
