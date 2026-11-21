package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

func TestRolesFileAccepted(t *testing.T) {
	t.Parallel()

	// The role files under testdata/rolesfile: one the engine accepts, one naming a role
	// outside the grammar, and one whose role carries an empty description.
	fixture := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("testdata", "rolesfile", name))
		if err != nil {
			t.Fatal(err)
		}

		return string(data)
	}
	withRoles := func(roles string) map[string]string {
		return map[string]string{
			"pkg/auth/staff/staff.go":   staffAuth,
			"pkg/auth/staff/roles.json": roles,
			"pkg/config/data.go":        staffConfig,
			"app/app.go":                staffApp,
		}
	}

	tests := []struct {
		name        string
		files       map[string]string
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name: "a file the engine accepts", files: withRoles(fixture("good.json")),
			wantStatus: Pass, wantSummary: "1 role file(s) parse the way the permission engine reads them: pkg/auth/staff/roles.json",
		},
		{
			name: "an empty file, as add auth writes it", files: withRoles(rolesJSON),
			wantStatus: Pass, wantSummary: "1 role file(s) parse the way the permission engine reads them: pkg/auth/staff/roles.json",
		},
		{
			name: "a role named with an underscore", files: withRoles(fixture("underscore.json")),
			wantStatus: Fail, wantSummary: "1 role file(s) the permission engine would refuse at start",
			wantDetails: []string{"pkg/auth/staff/roles.json: role Administrator_Global: a role name is letters and digits starting with a capital letter, such as Administrator or TenantAdministrator; the scope is never part of the name"},
		},
		{
			name:       "a role taking the name of the framework's role",
			files:      withRoles(`{"roles": {"global": [{"name": "SuperAdministrator", "description": "Runs the application.", "permissions": {}}]}}`),
			wantStatus: Fail, wantSummary: "1 role file(s) the permission engine would refuse at start",
			wantDetails: []string{"pkg/auth/staff/roles.json: role SuperAdministrator: the name is reserved: every auth has the Super Administrator role, which the framework defines, and an application defines no role of that name; remove it from the global roles and give what it grants to a role of another name"},
		},
		{
			name:       "a tenant role taking the name in another case",
			files:      withRoles(`{"roles": {"domain": [{"name": "Superadministrator", "description": "Runs one tenant.", "permissions": {}}]}}`),
			wantStatus: Fail, wantSummary: "1 role file(s) the permission engine would refuse at start",
			wantDetails: []string{"pkg/auth/staff/roles.json: role Superadministrator: the name is reserved: every auth has the Super Administrator role, which the framework defines, and an application defines no role of that name; remove it from the domain roles and give what it grants to a role of another name"},
		},
		{
			name: "a role with an empty description", files: withRoles(fixture("undescribed.json")),
			wantStatus: Fail, wantSummary: "1 role file(s) the permission engine would refuse at start",
			wantDetails: []string{"pkg/auth/staff/roles.json: role Auditor: a role carries a description, one or two plain sentences saying what the role is for"},
		},
		{
			name: "a file that is not JSON", files: withRoles("{\"roles\": {\"global\": [}\n"),
			wantStatus: Fail, wantSummary: "1 role file(s) the permission engine would refuse at start",
			wantDetails: []string{"pkg/auth/staff/roles.json: the role file does not parse: invalid character '}' looking for beginning of value"},
		},
		{
			name: "the role file is missing", files: withRoles(""),
			wantStatus: Fail, wantSummary: "1 role file(s) the permission engine would refuse at start",
			wantDetails: []string{"pkg/auth/staff/roles.json does not exist; the staff auth embeds its role file from there"},
		},
		{
			name: "no auth package", files: map[string]string{"pkg/config/doc.go": "package config\n"},
			wantStatus: Skip, wantSummary: "no auth package (pkg/auth/<name>) holds a role file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			write := func(rel, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", "module example.com/harbor\n\ngo 1.26.6\n")
			for rel, content := range tt.files {
				if content == "" {
					continue
				}
				write(rel, content)
			}
			a, err := app.Discover(root)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}
			got := rolesFileAccepted{}.Run(context.Background(), &Env{App: a})
			want := Result{Name: "roles-file", Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestRolesFileAccepted_servedGrants pins that a role file granting what the application
// does not serve is refused by the check, as the permission engine refuses it when the
// release starts: the check passed such a file while it read the file alone, and the
// application then did not start.
func TestRolesFileAccepted_servedGrants(t *testing.T) {
	t.Parallel()

	const (
		roles      = "pkg/auth/staff/roles.json"
		collection = "pkg/router/zz_gen_collection.go"
		// anchor opens the base's first role, so a grant written after it is the
		// Administrator's.
		anchor = `"permissions": {`
	)
	tests := []struct {
		name        string
		candidate   string
		edit        candidateEdit
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name: "the base as it is rendered", candidate: "solo",
			wantStatus: Pass, wantSummary: "1 role file(s) parse the way the permission engine reads them and grant only what the application serves (" + collection + "): " + roles,
		},
		{
			name: "the tenanted skeleton as it is rendered", candidate: "tenanted",
			wantStatus: Pass, wantSummary: "1 role file(s) parse the way the permission engine reads them and grant only what the application serves (" + collection + "): " + roles,
		},
		{
			name: "a grant on a list the application no longer serves", candidate: "tenanted",
			edit:       candidateEdit{file: roles, old: anchor, new: anchor + `"Approve": [{"resource": "StaffSignUpRoles"}],`},
			wantStatus: Fail, wantSummary: "1 role file(s) the permission engine would refuse at start",
			wantDetails: []string{roles + ": the global role Administrator grants Approve on StaffSignUpRoles, which the application does not serve (no resource of that name is in " + collection + "): take the grant out of the role, or serve the resource and run go generate ./..."},
		},
		{
			name: "a global role granting on what is served in a tenant", candidate: "tenanted",
			edit:       candidateEdit{file: roles, old: anchor, new: anchor + `"Approve": [{"resource": "StaffTenantUsers"}],`},
			wantStatus: Fail, wantSummary: "1 role file(s) the permission engine would refuse at start",
			wantDetails: []string{roles + ": Administrator is a global role and grants Approve on StaffTenantUsers, a domain resource: a role's grants are at its own scope, so move the grant to a domain role"},
		},
		{
			name: "a permission the resource does not take", candidate: "solo",
			edit:       candidateEdit{file: roles, old: anchor, new: anchor + `"Approve": [{"resource": "StaffSessionUsers"}],`},
			wantStatus: Fail, wantSummary: "1 role file(s) the permission engine would refuse at start",
			wantDetails: []string{roles + ": the global role Administrator grants Approve on StaffSessionUsers, which takes List, Read and not Approve: take the grant out of the role, or grant a permission the resource takes"},
		},
		{
			name: "a field the resource does not have", candidate: "solo",
			edit:       candidateEdit{file: roles, old: "\"fields\": [\n                \"username\",", new: "\"fields\": [\n                \"username\",\n                \"shoeSize\","},
			wantStatus: Fail, wantSummary: "1 role file(s) the permission engine would refuse at start",
			wantDetails: []string{roles + ": the global role Administrator grants List on StaffSessionUsers with the field(s) shoeSize, which the resource does not have for List: take them out of the grant's fields, or run go generate ./... if the resource just gained them"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var edits []candidateEdit
			if tt.edit.file != "" {
				edits = append(edits, tt.edit)
			}
			a := renderCandidate(t, tt.candidate, edits...)
			got := rolesFileAccepted{}.Run(context.Background(), &Env{App: a})
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %v, want %v: %s %v", got.Status, tt.wantStatus, got.Summary, got.Details)
			}
			if got.Summary != tt.wantSummary {
				t.Errorf("summary = %q, want %q", got.Summary, tt.wantSummary)
			}
			if tt.wantDetails != nil {
				if diff := cmp.Diff(tt.wantDetails, got.Details); diff != "" {
					t.Errorf("details mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}

	t.Run("every skeleton's role files grant what its collection serves", func(t *testing.T) {
		t.Parallel()

		for _, candidate := range []string{"solo", "tenanted", "outlets", "sites"} {
			got := rolesFileAccepted{}.Run(context.Background(), &Env{App: renderCandidate(t, candidate)})
			if got.Status != Pass || !strings.Contains(got.Summary, "grant only what the application serves") {
				t.Errorf("%s: status = %v, want a pass over its collection: %s %v", candidate, got.Status, got.Summary, got.Details)
			}
		}
	})

	t.Run("a collection that cannot be read is a finding", func(t *testing.T) {
		t.Parallel()

		a := renderCandidate(t, "solo", candidateEdit{file: collection, old: "resource.CollectionData{", new: "resource.Other{"})
		got := rolesFileAccepted{}.Run(context.Background(), &Env{App: a})
		if got.Status != Fail || !strings.Contains(got.Summary, "permission collection cannot be read") {
			t.Errorf("status = %v, want a failure saying the collection cannot be read: %s %v", got.Status, got.Summary, got.Details)
		}
	})
}
