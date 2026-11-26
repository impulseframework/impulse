package integration

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/impulseframework/impulse/session"
)

// TestAuditLog_eachActOnAnAccountIsARow proves the staff auth's audit log in an
// application with no tenants, on the served stack. The administrator gives a person a
// role for three days with a reason, then requires them to choose a new password. Each
// act is one row of the whole application's list (StaffListedAuditRecords), which the
// role file grants the Administrator: who acted, through the method's RPC; the act and
// whom it was done to, in words; and the reason the act carried, where it carried one.
func TestAuditLog_eachActOnAnAccountIsARow(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	admin := newBrowser(t, s)
	admin.signIn(ctx, adminUser)

	password := adminPassword
	keeper, err := s.auth.Session().API().CreateSessionUser(ctx, &session.CreateUserRequest{Username: "keeper", Password: &password})
	if err != nil {
		t.Fatalf("CreateSessionUser(keeper) error = %v", err)
	}
	end := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Minute)
	const reason = "Covering the release"
	giving, err := json.Marshal(map[string]any{"userId": keeper.String(), "role": "FeatureAdministrator", "endsAt": end.Format(time.RFC3339), "reason": reason})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/assign-staff-role", giving); status != http.StatusNoContent {
		t.Fatalf("the administrator giving the keeper a role for three days: status %d, want 204: %s", status, body)
	}
	requiring, err := json.Marshal(map[string]string{"userId": keeper.String()})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := admin.do(ctx, http.MethodPost, "/api/require-staff-password-change", requiring); status != http.StatusNoContent {
		t.Fatalf("the administrator requiring the keeper to choose a new password: status %d, want 204: %s", status, body)
	}

	// line is what a row says, as a person reads it.
	type line struct{ act, who, what, doneTo, reason string }
	var rows []line
	for _, row := range groupRows(ctx, t, admin, "/api/staff-listed-audit-records") {
		if row["subjectName"] != "keeper" {
			continue
		}
		said := line{}
		said.act, _ = row["act"].(string)
		said.who, _ = row["who"].(string)
		said.what, _ = row["what"].(string)
		said.doneTo, _ = row["doneTo"].(string)
		said.reason, _ = row["reason"].(string)
		rows = append(rows, said)
	}
	// Newest first.
	want := []line{
		{act: "passwordChangeRequired", who: "admin, through the RPC RequireStaffPasswordChange", what: "Required a new password", doneTo: "keeper"},
		{
			act: "roleGiven", who: "admin, through the RPC AssignStaffRole", doneTo: "keeper", reason: reason,
			what: "Gave the role Feature administrator for the whole application until " + end.Format("2006-01-02 15:04") + " UTC",
		},
	}
	if len(rows) != len(want) {
		t.Fatalf("the rows of the log done to the keeper = %+v, want %+v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("the row %d of the log done to the keeper = %+v, want %+v", i, rows[i], want[i])
		}
	}
}
