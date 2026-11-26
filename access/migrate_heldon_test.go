package access

// These tests pin the warning on a grant that gives nothing when its role is held on one
// record (HeldOnWarning): a grant on a resource apart, a Create on a resource the record
// only names or on the record's own kind, and an Execute on a method that names no row.

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// heldOnRelease is the release of the warning tests: a project holds roles, a task and
// a comment are under it, it names a client, and an invoice is apart; CloseTask targets
// a task and Archive targets nothing.
type heldOnRelease struct {
	*stubCollection
}

func newHeldOnRelease() heldOnRelease {
	return heldOnRelease{stubCollection: &stubCollection{
		list: registry(
			"Read", "Projects", "Update", "Projects", "Create", "Projects",
			"List", "Tasks", "Read", "Tasks", "Create", "Tasks", "Update", "Tasks",
			"Update", "Tasks.title", "Update", "Tasks.due", "Update", "Tasks.state",
			"List", "Comments", "Create", "Comments",
			"Read", "Clients", "Create", "Clients",
			"List", "Invoices", "Update", "Invoices", "Update", "Invoices.number", "Update", "Invoices.total", "Update", "Invoices.due",
			"Execute", "CloseTask", "Execute", "Archive",
		),
		targets: map[accesstypes.Resource]accesstypes.Resource{"CloseTask": "Tasks"},
	}}
}

func (heldOnRelease) RecordsHoldingRoles() []accesstypes.Resource {
	return []accesstypes.Resource{"Projects"}
}

func (heldOnRelease) RecordWords(res accesstypes.Resource) (one, many string) {
	switch res {
	case "Projects":
		return "project", "projects"
	case "Clients":
		return "client", "clients"
	default:
		return "", ""
	}
}

func (heldOnRelease) RecordReach(record, res accesstypes.Resource) (self, under, named bool) {
	if record != "Projects" {
		return false, false, false
	}

	return res == "Projects", res == "Tasks" || res == "Comments", res == "Clients"
}

func (heldOnRelease) RecordCondition(string, accesstypes.Role, accesstypes.Resource, accesstypes.Resource, bool) (string, bool) {
	return "", false
}

// heldOnWarningsOf validates a file whose one domain role, ProjectEditor, is held on
// projects with the grants given (or on nothing, with heldOn false), and answers the
// heldOn warnings it raises.
func heldOnWarningsOf(t *testing.T, heldOn bool, grants map[accesstypes.Permission][]Grant) []string {
	t.Helper()
	role := &Role{Name: "ProjectEditor", Description: "Edits a project.", Permissions: grants}
	if heldOn {
		role.HeldOn = []accesstypes.Resource{"Projects"}
	}
	warnings, err := ValidateRoles(newHeldOnRelease(), &RoleConfig{Roles: ScopedRoles{Domain: []*Role{role}}}, "")
	if err != nil {
		t.Fatalf("ValidateRoles() error = %v", err)
	}
	var lines []string
	for _, w := range warnings {
		if _, ok := w.(HeldOnWarning); ok {
			lines = append(lines, w.String())
		}
	}

	return lines
}

// TestValidateRoles_heldOnWarnings pins each case of the warning, in its sentence, and
// where it is not raised.
func TestValidateRoles_heldOnWarnings(t *testing.T) {
	t.Parallel()

	example := map[accesstypes.Permission][]Grant{
		"Read":   {{Resource: "Projects"}, {Resource: "Tasks"}, {Resource: "Clients"}},
		"Update": {{Resource: "Projects"}, {Resource: "Tasks", Fields: []accesstypes.Tag{"title"}}},
		"List":   {{Resource: "Tasks"}, {Resource: "Comments"}},
		"Create": {{Resource: "Tasks"}, {Resource: "Comments"}, {Resource: "Clients"}},
	}
	tests := []struct {
		name   string
		heldOn bool
		grants map[accesstypes.Permission][]Grant
		want   []string
	}{
		{
			name:   "the example's Create on the client the project names",
			heldOn: true,
			grants: example,
			want: []string{"ProjectEditor can be held on a project, and a project names one client and makes none, so its Create on Clients gives nothing when the role is held on one project. " +
				"It still adds clients when the role is held across the tenant."},
		},
		{
			name:   "a List on a resource apart",
			heldOn: true,
			grants: map[accesstypes.Permission][]Grant{"List": {{Resource: "Invoices"}}},
			want: []string{"ProjectEditor can be held on a project, and Invoices is neither under a project nor named by one, so its List on Invoices gives nothing when the role is held on one project. " +
				"It still counts when the role is held across the tenant."},
		},
		{
			name:   "a Create on the record's own kind",
			heldOn: true,
			grants: map[accesstypes.Permission][]Grant{"Create": {{Resource: "Projects"}}},
			want: []string{"ProjectEditor can be held on a project, and a role held on one project makes no other, so its Create on Projects gives nothing when the role is held on one project. " +
				"It still adds projects when the role is held across the tenant."},
		},
		{
			name:   "an Execute on a method that names no row",
			heldOn: true,
			grants: map[accesstypes.Permission][]Grant{"Execute": {{Resource: "Archive"}, {Resource: "CloseTask"}}},
			want: []string{"ProjectEditor can be held on a project, and Archive names no row, so its Execute on Archive gives nothing when the role is held on one project. " +
				"It still counts when the role is held across the tenant."},
		},
		{
			name:   "a role held on no record is warned of nothing",
			grants: map[accesstypes.Permission][]Grant{"List": {{Resource: "Invoices"}}, "Create": {{Resource: "Clients"}}, "Execute": {{Resource: "Archive"}}},
		},
		{
			name:   "a grant of three fields warns once",
			heldOn: true,
			grants: map[accesstypes.Permission][]Grant{"Update": {{Resource: "Invoices", Fields: []accesstypes.Tag{"number", "total", "due"}}}},
			want: []string{"ProjectEditor can be held on a project, and Invoices is neither under a project nor named by one, so its Update on Invoices gives nothing when the role is held on one project. " +
				"It still counts when the role is held across the tenant."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, heldOnWarningsOf(t, tt.heldOn, tt.grants)); diff != "" {
				t.Errorf("the heldOn warnings (-want +got):\n%s", diff)
			}
		})
	}
}

// TestValidateRoles_heldOnWarningLiteral pins that a consumer's literal of the warning, all
// ten fields spelled, equals the warning the role file's check makes, under the
// comparison a role-file test pins its accepted warnings with (reflect.DeepEqual).
func TestValidateRoles_heldOnWarningLiteral(t *testing.T) {
	t.Parallel()

	role := &Role{
		Name: "ProjectEditor", Description: "Edits a project.", HeldOn: []accesstypes.Resource{"Projects"},
		Permissions: map[accesstypes.Permission][]Grant{"Create": {{Resource: "Clients"}}},
	}
	got, err := ValidateRoles(newHeldOnRelease(), &RoleConfig{Roles: ScopedRoles{Domain: []*Role{role}}}, "")
	if err != nil {
		t.Fatalf("ValidateRoles() error = %v", err)
	}
	want := []Warning{HeldOnWarning{
		Role: "ProjectEditor", Record: "Projects", Permission: "Create", Resource: "Clients", Row: "Clients", Case: "namedOnly",
		RecordOne: "project", RecordMany: "projects", RowOne: "client", RowMany: "clients",
	}}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("ValidateRoles() warnings = %#v, want %#v", got, want)
	}
}
