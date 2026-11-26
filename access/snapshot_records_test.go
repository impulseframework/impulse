package access

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// The conditions the record tests' collection gives a holder of a role on a project:
// one for a row of Tasks that is read or changed, one for a row of Tasks being created.
const (
	heldTasks        = "projectId IN subject.heldProjects"
	heldTasksCreated = "new.projectId IN subject.heldProjects"
)

// recordRolesRelease is the release of the record tests: Projects is marked as a record
// that can hold a role, a row of Tasks is under it and Invoices is not reached; the
// method CloseTask targets a row of Tasks and Archive targets none.
type recordRolesRelease struct {
	*stubCollection
}

func newRecordRolesRelease() recordRolesRelease {
	return recordRolesRelease{stubCollection: &stubCollection{
		list: registry(
			"List", "Tasks", "List", "Invoices", "List", "Projects", "Read", "Tasks",
			"Update", "Tasks", "Update", "Tasks.title",
			"Create", "Tasks",
			"Execute", "CloseTask", "Execute", "Archive",
		),
		targets:    map[accesstypes.Resource]accesstypes.Resource{"CloseTask": "Tasks"},
		attributes: map[string]accesstypes.AttributeType{"Tasks.status": accesstypes.AttributeTypeString},
		columns:    []string{"Tasks.status"},
	}}
}

func (recordRolesRelease) RecordsHoldingRoles() []accesstypes.Resource {
	return []accesstypes.Resource{"Projects"}
}

func (recordRolesRelease) RecordWords(res accesstypes.Resource) (one, many string) {
	if res == "Projects" {
		return "project", "projects"
	}

	return "", ""
}

func (recordRolesRelease) RecordReach(record, res accesstypes.Resource) (self, under, named bool) {
	return record == "Projects" && res == "Projects", record == "Projects" && res == "Tasks", false
}

func (recordRolesRelease) RecordCondition(_ string, _ accesstypes.Role, record, res accesstypes.Resource, create bool) (string, bool) {
	if record != "Projects" || res != "Tasks" {
		return "", false
	}
	if create {
		return heldTasksCreated, true
	}

	return heldTasks, true
}

// recordRolesFile is the role file of the record tests: ProjectEditor can be held on a
// project, and Reader cannot.
var recordRolesFile = RoleFile(`{
  "auth": "crew",
  "roles": {
    "domain": [
      {"name": "ProjectEditor", "description": "Edits a project's tasks.", "heldOn": ["Projects"], "permissions": {
        "List": [{"resource": "Tasks"}, {"resource": "Invoices"}],
        "Update": [{"resource": "Tasks", "fields": ["title"], "condition": "status = 'open'"}],
        "Read": [{"resource": "Tasks", "condition": "status = 'open' OR status = 'held'"}],
        "Create": [{"resource": "Tasks"}],
        "Execute": [{"resource": "CloseTask"}, {"resource": "Archive"}]
      }},
      {"name": "Reader", "description": "Reads the tasks.", "permissions": {"List": [{"resource": "Tasks"}]}}
    ]
  }
}`)

// projectP1 is the project the record tests give roles on, in tenant1.
var projectP1 = accesstypes.NewRecordScope("tenant1", "Projects", "p-1")

// recordClient is a Client over store with the record tests' release and role file.
func recordClient(t *testing.T, store *fakeStore) *Client {
	t.Helper()
	client := newPolicyClient(t, store, newRecordRolesRelease(), WithDefaultRoles(recordRolesFile, fixtureAuth))
	if err := client.WaitReady(t.Context()); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}

	return client
}

// decisionAt is the decision of perm on res for user in scope at the instant.
func decisionAt(t *testing.T, client *Client, env accesstypes.Environment, user accesstypes.User, scope accesstypes.Scope, perm accesstypes.Permission, res accesstypes.Resource) accesstypes.Decision {
	t.Helper()
	decisions, err := client.CheckUserResources(t.Context(), env, user, scope, perm, res)
	if err != nil {
		t.Fatalf("CheckUserResources(%s, %s, %s) error = %v", user, perm, res, err)
	}

	return decisions[res]
}

// conditionTexts are the condition texts a decision carries, sorted, as the engine renders
// them: a grant's own condition and a record's, joined by AND, read with no parentheses
// that change nothing.
func conditionTexts(d accesstypes.Decision) []string {
	var texts []string
	for _, group := range d.ConditionGroups() {
		texts = append(texts, group.Condition.Source())
	}
	slices.Sort(texts)

	return texts
}

// TestClient_recordRoles pins what a role held on a record gives: on what the record
// reaches, the role's grants under the condition the collection answers, joined to a
// grant's own condition by AND; nothing on what it does not reach, in another tenant, or
// through a method with no target; and a role held in the tenant gives what it gave
// before, whether or not the person also holds it on a record.
func TestClient_recordRoles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	for _, user := range []accesstypes.User{"ann", "cal"} {
		if err := store.InsertRecordUserRole(ctx, projectP1, user, "ProjectEditor", accesstypes.Period{}); err != nil {
			t.Fatalf("InsertRecordUserRole(%s) error = %v", user, err)
		}
	}
	for _, user := range []accesstypes.User{"bob", "cal"} {
		if err := store.InsertUserRole(ctx, tenant1Policy, user, "ProjectEditor", accesstypes.Period{}, "", nil); err != nil {
			t.Fatalf("InsertUserRole(%s) error = %v", user, err)
		}
	}
	client := recordClient(t, store)
	env := accesstypes.NewEnvironment()

	tests := []struct {
		name  string
		user  accesstypes.User
		scope accesstypes.Scope
		perm  accesstypes.Permission
		res   accesstypes.Resource
		want  string
		conds []string
	}{
		{name: "a row under the record is read under the collection's condition", user: "ann", scope: tenant1Scope, perm: "List", res: "Tasks", want: "conditional", conds: []string{heldTasks}},
		{name: "a grant's own condition and the record's are both held", user: "ann", scope: tenant1Scope, perm: "Update", res: "Tasks", want: "conditional", conds: []string{"status = 'open' AND " + heldTasks}},
		{name: "a grant's own condition of two parts is held whole beside the record's", user: "ann", scope: tenant1Scope, perm: "Read", res: "Tasks", want: "conditional", conds: []string{"(status = 'open' OR status = 'held') AND " + heldTasks}},
		{name: "a field grant carries both conditions too", user: "ann", scope: tenant1Scope, perm: "Update", res: "Tasks.title", want: "conditional", conds: []string{"status = 'open' AND " + heldTasks}},
		{name: "a row being created is held to the create condition", user: "ann", scope: tenant1Scope, perm: "Create", res: "Tasks", want: "conditional", conds: []string{heldTasksCreated}},
		{name: "a method whose target is under the record is conditional the same way", user: "ann", scope: tenant1Scope, perm: "Execute", res: "CloseTask", want: "conditional", conds: []string{heldTasks}},
		{name: "a method with no target gives nothing", user: "ann", scope: tenant1Scope, perm: "Execute", res: "Archive", want: "denied"},
		{name: "a kind the record does not reach gives nothing", user: "ann", scope: tenant1Scope, perm: "List", res: "Invoices", want: "denied"},
		{name: "another tenant gives nothing", user: "ann", scope: tenant2Scope, perm: "List", res: "Tasks", want: "denied"},
		{name: "the role held in the tenant gives what it gave", user: "bob", scope: tenant1Scope, perm: "List", res: "Invoices", want: "granted"},
		{name: "the role held in the tenant is unconditional on the rows under the record", user: "bob", scope: tenant1Scope, perm: "List", res: "Tasks", want: "granted"},
		{name: "the role held both ways gives the tenant's", user: "cal", scope: tenant1Scope, perm: "List", res: "Tasks", want: "granted"},
		{name: "the role held both ways gives the tenant's beyond the record", user: "cal", scope: tenant1Scope, perm: "List", res: "Invoices", want: "granted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := decisionAt(t, client, env, tt.user, tt.scope, tt.perm, tt.res)
			if got.String() != tt.want {
				t.Fatalf("%s %s on %s in %s = %s, want %s", tt.user, tt.perm, tt.res, tt.scope, got, tt.want)
			}
			if diff := cmp.Diff(tt.conds, conditionTexts(got)); diff != "" {
				t.Errorf("the decision's conditions (-want +got):\n%s", diff)
			}
		})
	}

	for _, c := range []struct {
		scope accesstypes.Scope
		want  bool
	}{{tenant1Scope, true}, {tenant2Scope, false}} {
		if has, err := client.UserHasGrants(ctx, "ann", c.scope); err != nil || has != c.want {
			t.Errorf("UserHasGrants(ann, %s) = (%v, %v), want %v", c.scope, has, err, c.want)
		}
	}
}

// TestClient_recordRoles_periods pins a role on a record to its period, a person's own
// and a group's: it gives nothing before its start or from its end, a member of a group
// of the tenant holds the group's role on the record while the place and the role both
// count, and the snapshot that answers inside the period answers no further than its end.
func TestClient_recordRoles_periods(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	if err := store.InsertRecordUserRole(ctx, projectP1, "ann", "ProjectEditor", between(1, 2)); err != nil {
		t.Fatalf("InsertRecordUserRole() error = %v", err)
	}
	if taken, err := store.InsertGroup(ctx, &policy.GroupRow{Group: policy.Group{ID: "crew", Scope: tenant1Policy}, Name: "Crew", Description: "Works the projects."}); err != nil || taken {
		t.Fatalf("InsertGroup() = (%v, %v)", taken, err)
	}
	if err := store.InsertGroupMember(ctx, "crew", "dan", between(0, 3)); err != nil {
		t.Fatalf("InsertGroupMember() error = %v", err)
	}
	if err := store.InsertRecordGroupRole(ctx, "crew", projectP1, "ProjectEditor", accesstypes.Period{}); err != nil {
		t.Fatalf("InsertRecordGroupRole() error = %v", err)
	}
	client := recordClient(t, store)

	for _, step := range []struct {
		user accesstypes.User
		at   float64
		want string
	}{
		{"ann", 0.5, "denied"},
		{"ann", 1, "conditional"},
		{"ann", 1.5, "conditional"},
		{"ann", 2, "denied"},
		{"dan", -1, "denied"},
		{"dan", 0, "conditional"},
		{"dan", 2.5, "conditional"},
		{"dan", 3, "denied"},
	} {
		got := decisionAt(t, client, accesstypes.EnvironmentAt(hoursOn(step.at)), step.user, tenant1Scope, "List", "Tasks")
		if got.String() != step.want {
			t.Errorf("%s List on Tasks %v hours after noon = %s, want %s", step.user, step.at, got, step.want)
		}
	}

	records, err := store.ReadPolicy(ctx)
	if err != nil {
		t.Fatalf("ReadPolicy() error = %v", err)
	}
	defaults, err := compileDefaultRoles(newRecordRolesRelease(), mustParse(t, recordRolesFile), nil, fixtureAuth)
	if err != nil {
		t.Fatalf("compileDefaultRoles() error = %v", err)
	}
	snap, _, err := newSnapshotAt(records, defaults, newRecordRolesRelease(), hoursOn(1.5), hoursOn(1.5))
	if err != nil {
		t.Fatalf("newSnapshotAt() error = %v", err)
	}
	if !snap.until.Equal(hoursOn(2)) || !snap.since.Equal(hoursOn(1)) {
		t.Errorf("the snapshot at 1.5 hours answers from %v until %v, want from the start of ann's role until its end", snap.since, snap.until)
	}
}

// TestClient_recordRoles_skipped pins the rows that give nothing: a role no role file
// marks for the record's kind, a group's whose own row is gone, and a group's kept
// anywhere but the record's tenant, each reported once by the deploy check.
func TestClient_recordRoles_skipped(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	store := newFakeStore()
	if err := store.InsertRecordUserRole(ctx, projectP1, "ann", "Reader", accesstypes.Period{}); err != nil {
		t.Fatalf("InsertRecordUserRole() error = %v", err)
	}
	for _, group := range []*policy.GroupRow{
		{Group: policy.Group{ID: "staff", Scope: globalPolicy}, Name: "Staff", Description: "The application's own people."},
		{Group: policy.Group{ID: "others", Scope: tenant2Policy}, Name: "Others", Description: "Another tenant's people."},
	} {
		if taken, err := store.InsertGroup(ctx, group); err != nil || taken {
			t.Fatalf("InsertGroup(%s) = (%v, %v)", group.ID, taken, err)
		}
		if err := store.InsertGroupMember(ctx, group.ID, "eve", accesstypes.Period{}); err != nil {
			t.Fatalf("InsertGroupMember() error = %v", err)
		}
		if err := store.InsertRecordGroupRole(ctx, group.ID, projectP1, "ProjectEditor", accesstypes.Period{}); err != nil {
			t.Fatalf("InsertRecordGroupRole(%s) error = %v", group.ID, err)
		}
	}
	client := recordClient(t, store)

	for _, user := range []accesstypes.User{"ann", "eve"} {
		if got := decisionAt(t, client, accesstypes.NewEnvironment(), user, tenant1Scope, "List", "Tasks"); !got.IsDenied() {
			t.Errorf("%s List on Tasks = %s, want denied: the row gives nothing", user, got)
		}
	}
	warnings, err := client.CheckPolicy(ctx)
	if err != nil {
		t.Fatalf("CheckPolicy() error = %v", err)
	}
	var listed []string
	for _, w := range warnings {
		if strings.HasPrefix(w.String(), "skipped the role") {
			listed = append(listed, w.String())
		}
	}
	slices.Sort(listed)
	want := []string{
		"skipped the role ProjectEditor held on Projects p-1 in tenant1 by others: the group is not kept in the record's tenant, and a role on a record is held by a person or by a group of that tenant",
		"skipped the role ProjectEditor held on Projects p-1 in tenant1 by staff: the group is not kept in the record's tenant, and a role on a record is held by a person or by a group of that tenant",
		"skipped the role Reader held on Projects p-1 in tenant1 by ann: the role is not one held on a record of this kind",
	}
	if diff := cmp.Diff(want, listed); diff != "" {
		t.Errorf("CheckPolicy() skipped record roles (-want +got):\n%s", diff)
	}
}

// Test_snapshot_recordRoles_groupGone pins the row of a group whose own row is not there:
// it gives nothing and is reported, as a group's role in a tenant is.
func Test_snapshot_recordRoles_groupGone(t *testing.T) {
	t.Parallel()

	records := &policy.Records{
		GroupMembers:      []policy.GroupMember{{Group: "gone", User: "ann"}},
		RecordMemberships: []policy.RecordMembership{{Scope: projectP1, Member: groupSubject("gone"), Role: "ProjectEditor"}},
	}
	defaults, err := compileDefaultRoles(newRecordRolesRelease(), mustParse(t, recordRolesFile), nil, fixtureAuth)
	if err != nil {
		t.Fatalf("compileDefaultRoles() error = %v", err)
	}
	snap, findings, err := newSnapshot(records, defaults, newRecordRolesRelease(), noon)
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}
	if holds(snap, "ann", tenant1Scope, "List", "Tasks") || snap.userHasGrants(tenant1Scope, "ann") {
		t.Error("a member of a group that is gone holds the group's role on the record")
	}
	var reported []string
	for _, f := range findings {
		reported = append(reported, f.String())
	}
	want := []string{"skipped the role ProjectEditor held on Projects p-1 in tenant1 by gone: the group's own row is not there"}
	if diff := cmp.Diff(want, reported); diff != "" {
		t.Errorf("the findings (-want +got):\n%s", diff)
	}
}

// mustParse parses a role file, failing the test where it does not.
func mustParse(t *testing.T, file RoleFile) *RoleConfig {
	t.Helper()
	config, err := file.Parse()
	if err != nil {
		t.Fatalf("RoleFile.Parse() error = %v", err)
	}

	return config
}
