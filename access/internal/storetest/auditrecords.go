package storetest

import (
	"slices"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
)

// AuditRow is a row of the audit records table as the audit phase reads it: the act, who
// acted (the process and the directory), and the account and the role it names.
type AuditRow struct {
	Act, Process, Directory, SubjectID, RoleName string
}

// AuditRows reads the rows of the audit records table, oldest first.
type AuditRows func(t *testing.T) []AuditRow

// The audit phase's fixtures: a tenant and people no other phase writes, and the
// directory its records name.
var auditTenant = accesstypes.DomainPolicyScope("audited")

const (
	auditDirectory = "Acme"
	nia            = accesstypes.User("nia")
	oli            = accesstypes.User("oli")
	pam            = accesstypes.User("pam")
	// ghost is a custom role no row of which is held anywhere.
	ghost = accesstypes.Role("Ghost")
)

// RunAuditRecords exercises the record a write of a person's role carries, against an
// empty, ready store: recording names the audit records table (WithAuditRecords), plain
// is a store of the same tables that names none, and rows reads the audit records table.
// A record is written with the role it records, in the write's transaction, by each of
// the three writes; plain writes the role and no record; a removal of a membership that
// is not there, a custom role that has no row, and a record that does not validate write
// nothing.
func RunAuditRecords(t *testing.T, recording, plain access.Store, rows AuditRows) {
	t.Helper()
	ctx := t.Context()
	want := make([]AuditRow, 0, 3)
	check := func(t *testing.T, after string) {
		t.Helper()
		if diff := cmp.Diff(want, rows(t), cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("the audit records table, after %s (-want +got):\n%s", after, diff)
		}
	}
	holds := func(t *testing.T, user accesstypes.User, role accesstypes.Role) bool {
		t.Helper()
		roles, err := recording.ListUserRoles(ctx, auditTenant, user)
		if err != nil {
			t.Fatalf("ListUserRoles(%s) error = %v", user, err)
		}

		return slices.Contains(roles, role)
	}

	if err := recording.InsertUserRole(ctx, auditTenant, nia, editor, accesstypes.Period{}, "", directoryRecord(auditlog.ActRoleGiven, nia, editor)); err != nil {
		t.Fatalf("InsertUserRole() with a record error = %v", err)
	}
	want = append(want, auditRow(auditlog.ActRoleGiven, nia, editor))
	check(t, "a role given with a record")

	insertRole(t, recording, auditTenant, admin)
	if err := recording.InsertCustomUserRole(ctx, auditTenant, nia, admin, accesstypes.Period{}, "", directoryRecord(auditlog.ActRoleGiven, nia, admin)); err != nil {
		t.Fatalf("InsertCustomUserRole() with a record error = %v", err)
	}
	want = append(want, auditRow(auditlog.ActRoleGiven, nia, admin))
	check(t, "a custom role given with a record")

	if err := plain.InsertUserRole(ctx, auditTenant, oli, editor, accesstypes.Period{}, "", directoryRecord(auditlog.ActRoleGiven, oli, editor)); err != nil {
		t.Fatalf("InsertUserRole() on a store that names no table error = %v", err)
	}
	if !holds(t, oli, editor) {
		t.Errorf("a store that names no table did not give the role")
	}
	check(t, "a role given with a record by a store that names no table")

	if err := recording.DeleteUserRole(ctx, auditTenant, pam, editor, directoryRecord(auditlog.ActRoleTaken, pam, editor)); err != nil {
		t.Fatalf("DeleteUserRole() of a membership that is not there error = %v", err)
	}
	check(t, "the removal of a membership that is not there")

	err := recording.InsertCustomUserRole(ctx, auditTenant, pam, ghost, accesstypes.Period{}, "", directoryRecord(auditlog.ActRoleGiven, pam, ghost))
	if !errors.Is(err, policy.ErrNoRole) {
		t.Fatalf("InsertCustomUserRole() of a role with no row error = %v, want policy.ErrNoRole", err)
	}
	check(t, "a custom role with no row refused")

	invalid := directoryRecord(auditlog.ActRoleGiven, pam, editor)
	invalid.SubjectID = ""
	if err := recording.InsertUserRole(ctx, auditTenant, pam, editor, accesstypes.Period{}, "", invalid); err == nil {
		t.Fatalf("InsertUserRole() with a record that does not validate error = nil, want its refusal")
	}
	if holds(t, pam, editor) {
		t.Errorf("a record that does not validate was refused and its role given")
	}
	check(t, "a record that does not validate")

	if err := recording.DeleteUserRole(ctx, auditTenant, nia, editor, directoryRecord(auditlog.ActRoleTaken, nia, editor)); err != nil {
		t.Fatalf("DeleteUserRole() with a record error = %v", err)
	}
	if holds(t, nia, editor) {
		t.Errorf("DeleteUserRole() with a record left the role held")
	}
	want = append(want, auditRow(auditlog.ActRoleTaken, nia, editor))
	check(t, "a role taken with a record")
}

// directoryRecord is the record of act on user's role in the audit phase's tenant, by the
// directory Acme at a sign-in, as the permission store is handed one.
func directoryRecord(act auditlog.Act, user accesstypes.User, role accesstypes.Role) *auditlog.Record {
	var r auditlog.Record
	r.Act, r.Source, r.Directory = act, eventsource.ProcessEvent(auditlog.ProcessDirectorySignIn), auditDirectory
	r.SubjectKind, r.SubjectID, r.SubjectName = auditlog.SubjectAccount, string(user), string(user)
	r.RoleNames, r.RoleScope = []string{string(role)}, auditlog.RoleScopeTenant
	r.TenantID, r.TenantName = "audited", "Audited"

	return &r
}

// auditRow is the row directoryRecord's record is read back as.
func auditRow(act auditlog.Act, user accesstypes.User, role accesstypes.Role) AuditRow {
	return AuditRow{Act: string(act), Process: auditlog.ProcessDirectorySignIn, Directory: auditDirectory, SubjectID: string(user), RoleName: string(role)}
}
