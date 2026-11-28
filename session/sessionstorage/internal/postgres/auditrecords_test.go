package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/drivertest"
)

// auditRows reads every row of the audit records table directly, the oldest first, each
// by its columns' names, with the columns that hold nothing left out.
func auditRows(ctx context.Context, t *testing.T, raw any) []map[string]any {
	t.Helper()

	names := auditlog.Columns()
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, `"`+name+`"`)
	}
	rows, err := queryer(t, raw).Query(ctx, fmt.Sprintf(`SELECT %s FROM "SessionAuditRecords" ORDER BY "CreatedAt", "Id"`, strings.Join(quoted, ", ")))
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	defer rows.Close()

	var all []map[string]any
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			t.Fatalf("pgx.Rows.Values() error = %v", err)
		}
		row := map[string]any{}
		for i, value := range values {
			switch value := value.(type) {
			case nil:
			case time.Time:
				row[names[i]] = value.UTC()
			case []any:
				texts := make([]string, 0, len(value))
				for _, text := range value {
					texts = append(texts, fmt.Sprint(text))
				}
				row[names[i]] = texts
			default:
				row[names[i]] = value
			}
		}
		all = append(all, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("pgx.Rows.Err() error = %v", err)
	}

	return all
}

// TestAuditRecords_RemovedPastThePeriod proves that a write that inserts an audit record
// removes, in its transaction, the rows older than the period the Auth keeps them for
// and keeps the younger ones: with a period of one day, a row of 25 hours ago goes and
// one of an hour ago stays; kept forever, or with no period, nothing goes.
func TestAuditRecords_RemovedPastThePeriod(t *testing.T) {
	t.Parallel()

	oneDay, forever := auditlog.KeepDays(1), auditlog.KeepForever()
	for _, tt := range []struct {
		name   string
		period *auditlog.Period
		// wantOld says the row of 25 hours ago is there after the next recorded write.
		wantOld bool
	}{
		{name: "a period of one day", period: &oneDay, wantOld: false},
		{name: "kept forever", period: &forever, wantOld: true},
		{name: "no period", wantOld: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			in := newAuthInstance(ctx, t, drivertest.Auth, drivertest.AuthConfig{Auth: true})
			d, ok := in.Driver.(*SessionStorageDriver)
			if !ok {
				t.Fatalf("the instance's driver is %T", in.Driver)
			}
			if tt.period != nil {
				if err := d.SetAuditRecordsPeriod(*tt.period); err != nil {
					t.Fatalf("SetAuditRecordsPeriod() error = %v, want the period kept", err)
				}
			}
			user := createTestUser(ctx, t, in.Driver, "pat@lakeside.edu")
			disabled := func() *auditlog.Record {
				var r auditlog.Record
				r.Act, r.Source, r.SubjectKind, r.SubjectID, r.SubjectName = auditlog.ActAccountDisabled, eventsource.ProcessEvent(auditlog.ProcessApplication),
					auditlog.SubjectAccount, user.ID.String(), user.Username

				return &r
			}
			for range 2 {
				if err := d.DeactivateUser(ctx, user.ID, disabled()); err != nil {
					t.Fatalf("DeactivateUser() error = %v", err)
				}
			}
			written := auditRows(ctx, t, in.Raw)
			if len(written) != 2 {
				t.Fatalf("audit rows = %d, want 2", len(written))
			}
			old, young := written[0]["Id"], written[1]["Id"]
			age(ctx, t, in.Raw, `UPDATE "SessionAuditRecords" SET "CreatedAt" = now() - interval '25 hours' WHERE "Id" = $1`, old)
			age(ctx, t, in.Raw, `UPDATE "SessionAuditRecords" SET "CreatedAt" = now() - interval '1 hour' WHERE "Id" = $1`, young)

			if err := d.DeactivateUser(ctx, user.ID, disabled()); err != nil {
				t.Fatalf("DeactivateUser() error = %v", err)
			}
			kept := map[any]bool{}
			for _, row := range auditRows(ctx, t, in.Raw) {
				kept[row["Id"]] = true
			}
			if kept[old] != tt.wantOld {
				t.Errorf("the row of 25 hours ago is there after the next recorded write = %t, want %t", kept[old], tt.wantOld)
			}
			if !kept[young] {
				t.Error("the row of an hour ago is gone after the next recorded write, want it kept")
			}
			if len(kept) < 2 {
				t.Errorf("audit rows after the next recorded write = %d, want the new row with the rows kept", len(kept))
			}
		})
	}
}
