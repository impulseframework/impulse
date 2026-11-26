package identity

import (
	"context"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// actedRow is a row of the audit log as these tests read it: the act, what it was done
// to, the role or the group it names, the tenant, and who a person reads acted.
type actedRow struct {
	act         auditlog.Act
	subject     string
	role, group string
	tenant      string
	who         string
}

// actedRows reads the rows the store was handed as actedRows, in order.
func actedRows(t *testing.T, store *fakeStore) []actedRow {
	t.Helper()

	rows := make([]actedRow, 0, len(store.audit))
	for i := range store.audit {
		r := &store.audit[i]
		role := ""
		if len(r.RoleNames) > 0 {
			role = r.RoleNames[0]
		}
		stored := storedOf(t, r)
		rows = append(rows, actedRow{
			act: r.Act, subject: string(r.SubjectKind) + " " + r.SubjectName, role: role, group: r.GroupName, tenant: r.TenantID,
			who: stored.Sentences("tenant").Who,
		})
	}

	return rows
}

// TestSignInActsRecorded pins the rows of what a sign-in gives and takes, by what acted:
// an invitation accepted, one row of the acceptance and one for each role it gives, in
// its tenant, by an invitation; an invitation that makes a tenant, the same, in the tenant
// it made; a sign-up's starting role, by a sign-up; and the assignments whose period has
// ended, cleared at a sign-in, by the end of their period.
func TestSignInActsRecorded(t *testing.T) {
	t.Parallel()

	nightKey := access.NewMembershipKey(alphaScope, "", "NightShiftLead")
	alphaNightShiftLead := access.RoleKey{Kind: nightKey.Kind, Axis: nightKey.Axis, Domain: nightKey.Domain, Role: "NightShiftLead"}
	danaAccount := "account dana"

	t.Run("an invitation accepted, with two roles", func(t *testing.T) {
		t.Parallel()

		store := groupStore()
		store.roles[alphaNightShiftLead] = true
		m, _ := newManager(t, store, engineRoles)
		inv := &sessioninfo.Invitation{ID: invitationID, Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Reader", "NightShiftLead"}}
		if _, err := m.InvitationAccepted("Staff")(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", inv); err != nil {
			t.Fatalf("InvitationAccepted() error = %v", err)
		}
		want := []actedRow{
			{act: auditlog.ActRoleGiven, subject: danaAccount, role: "Reader", tenant: "alpha", who: "an invitation"},
			{act: auditlog.ActRoleGiven, subject: danaAccount, role: "NightShiftLead", tenant: "alpha", who: "an invitation"},
			{act: auditlog.ActInvitationAccepted, subject: danaAccount, tenant: "alpha", who: "an invitation"},
		}
		if diff := cmp.Diff(want, actedRows(t, store), cmp.AllowUnexported(actedRow{})); diff != "" {
			t.Errorf("audit rows (-want +got):\n%s", diff)
		}
	})

	t.Run("an invitation that makes a tenant, accepted", func(t *testing.T) {
		t.Parallel()

		store := &fakeStore{usernames: map[impulse.UUID]string{userID: "dana"}}
		m := newTenantInviter(t, store, &fakeMaker{}, &fakeMail{}, &fakeGrants{})
		inv := &sessioninfo.Invitation{ID: invitationID, Email: "dana@example.com", NewTenant: &sessioninfo.NewTenant{Name: "Dawn"}}
		if _, err := m.InvitationAccepted("Staff")(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", inv); err != nil {
			t.Fatalf("InvitationAccepted() error = %v", err)
		}
		want := []actedRow{
			{act: auditlog.ActRoleGiven, subject: danaAccount, role: "Keeper", tenant: string(madeTenant), who: "an invitation"},
			{act: auditlog.ActInvitationAccepted, subject: danaAccount, tenant: string(madeTenant), who: "an invitation"},
		}
		if diff := cmp.Diff(want, actedRows(t, store), cmp.AllowUnexported(actedRow{})); diff != "" {
			t.Errorf("audit rows (-want +got):\n%s", diff)
		}
	})

	t.Run("an invitation whose role can no longer be given fails, and records no acceptance", func(t *testing.T) {
		t.Parallel()

		store := groupStore()
		m, _ := newManager(t, store, engineRoles)
		inv := &sessioninfo.Invitation{ID: invitationID, Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Reader", "NoSuchRole"}}
		if _, err := m.InvitationAccepted("Staff")(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", inv); err == nil {
			t.Fatal("InvitationAccepted() error = nil, want the role's failure: the session library's transaction does not commit")
		}
		for _, row := range actedRows(t, store) {
			if row.act == auditlog.ActInvitationAccepted {
				t.Errorf("a failed acceptance buffered its row %+v", row)
			}
		}
	})

	t.Run("a sign-up", func(t *testing.T) {
		t.Parallel()

		store := groupStore()
		m, _ := newManager(t, store, engineRoles)
		if _, err := m.SignedUp("Staff")(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", "google", "Steward"); err != nil {
			t.Fatalf("SignedUp() error = %v", err)
		}
		want := []actedRow{{act: auditlog.ActRoleGiven, subject: danaAccount, role: "Steward", who: "a sign-up"}}
		if diff := cmp.Diff(want, actedRows(t, store), cmp.AllowUnexported(actedRow{})); diff != "" {
			t.Errorf("audit rows (-want +got):\n%s", diff)
		}
	})

	t.Run("a sign-up that makes the account: the row names it by the username the hook is handed", func(t *testing.T) {
		t.Parallel()

		// The account is buffered in the sign-in's transaction, and a read in it answers
		// no user.
		store := groupStore()
		delete(store.usernames, userID)
		m, _ := newManager(t, store, engineRoles)
		if _, err := m.SignedUp("Staff")(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", "google", "Steward"); err != nil {
			t.Fatalf("SignedUp() error = %v", err)
		}
		want := []actedRow{{act: auditlog.ActRoleGiven, subject: danaAccount, role: "Steward", who: "a sign-up"}}
		if diff := cmp.Diff(want, actedRows(t, store), cmp.AllowUnexported(actedRow{})); diff != "" {
			t.Errorf("audit rows (-want +got):\n%s", diff)
		}
	})

	t.Run("an invitation accepted by the account it makes: the rows name it by the username the hook is handed", func(t *testing.T) {
		t.Parallel()

		store := groupStore()
		delete(store.usernames, userID)
		m, _ := newManager(t, store, engineRoles)
		inv := &sessioninfo.Invitation{ID: invitationID, Email: "dana@example.com", TenantID: "alpha", RoleNames: []string{"Reader"}}
		if _, err := m.InvitationAccepted("Staff")(t.Context(), new(spanner.ReadWriteTransaction), userID, "dana", inv); err != nil {
			t.Fatalf("InvitationAccepted() error = %v", err)
		}
		want := []actedRow{
			{act: auditlog.ActRoleGiven, subject: danaAccount, role: "Reader", tenant: "alpha", who: "an invitation"},
			{act: auditlog.ActInvitationAccepted, subject: danaAccount, tenant: "alpha", who: "an invitation"},
		}
		if diff := cmp.Diff(want, actedRows(t, store), cmp.AllowUnexported(actedRow{})); diff != "" {
			t.Errorf("audit rows (-want +got):\n%s", diff)
		}
	})

	t.Run("two ended assignments cleared, in a request of the person's own", func(t *testing.T) {
		t.Parallel()

		store, _, _ := clearStore()
		m, _ := newManager(t, store, engineRoles)
		if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := m.ClearEnded(danaCaller(accesstypes.GlobalScope()), txn, userID)

			return err
		}); err != nil {
			t.Fatalf("ClearEnded() error = %v", err)
		}
		want := []actedRow{
			{act: auditlog.ActRoleEnded, subject: danaAccount, role: "Reader", tenant: "alpha", who: "the end of its period"},
			{act: auditlog.ActGroupPlaceEnded, subject: danaAccount, group: "Night shift", tenant: "alpha", who: "the end of its period"},
		}
		if diff := cmp.Diff(want, actedRows(t, store), cmp.AllowUnexported(actedRow{})); diff != "" {
			t.Errorf("audit rows (-want +got):\n%s", diff)
		}
		if len(store.audit) == 2 {
			if source := store.audit[0].Source; source != eventsource.ProcessEvent(auditlog.ProcessPeriodEnd) {
				t.Errorf("the ended role's row is by %s, want the process %s alone", source, auditlog.ProcessPeriodEnd)
			}
			stored := storedOf(t, &store.audit[0])
			if what := stored.Sentences("tenant").What; what != "The role Reader ended with its period" {
				t.Errorf("the ended role's row reads %q", what)
			}
		}
	})

	t.Run("a place in a group the store no longer answers, cleared: the group by its id", func(t *testing.T) {
		t.Parallel()

		store, _, place := clearStore()
		delete(store.groups, nightID)
		m, _ := newManager(t, store, engineRoles)
		if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := m.ClearEnded(ctx, txn, userID)

			return err
		}); err != nil {
			t.Fatalf("ClearEnded() error = %v", err)
		}
		rows := actedRows(t, store)
		if len(rows) != 2 || rows[1].group != string(place.Group) || rows[1].tenant != "" {
			t.Errorf("audit rows = %+v, want the place's row naming the group by its id %s, in no tenant", rows, place.Group)
		}
	})
}
