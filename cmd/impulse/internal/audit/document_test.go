package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/shape"
)

// documentFile is the framework's document of the sign-in shapes, from this package's
// directory.
var documentFile = filepath.Join("..", "..", "..", "..", filepath.FromSlash(shape.Document))

// readingHeading is the heading under which the document quotes a whole sign-in section,
// a new application's.
const readingHeading = "Reading a shape: impulse audit"

// quote is a block of the document that quotes the sign-in section: the heading it
// stands under and its lines.
type quote struct {
	heading string
	lines   []string
}

// isShapeLine reports the line of the sign-in section that names an auth's shape,
// however it is indented.
func isShapeLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "shape: ")
}

// quotes finds the document's quotes of the sign-in section: every block of output
// (fenced as text) that has a "shape:" line, with the heading of the third level it
// stands under.
func quotes(document string) []quote {
	var (
		found   []quote
		heading string
		block   []string
		inside  bool
	)
	for line := range strings.SplitSeq(document, "\n") {
		switch {
		case inside && line == "```":
			if slices.ContainsFunc(block, isShapeLine) {
				found = append(found, quote{heading: heading, lines: block})
			}
			inside, block = false, nil
		case inside:
			block = append(block, line)
		case line == "```text":
			inside = true
		case strings.HasPrefix(line, "### "):
			heading = strings.TrimPrefix(line, "### ")
		case strings.HasPrefix(line, "## "):
			heading = ""
		}
	}

	return found
}

// written writes an application of the files, each by its path from the application's
// root, into a temporary directory and reads it.
func written(t *testing.T, files map[string]string) *app.App {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/harbor\n\ngo 1.26.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := app.Discover(root)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}

	return a
}

// documented builds, for each of the eight shapes by the name --shape takes, an
// application of the choices the document's section of that shape describes. Without
// tenants the staff auth signs in with a password (shape 1), with one Google Workspace
// beside it (2) or any Google account beside it (4), and the members auth through one
// Google Workspace whose directory gives the roles (3). With tenants a person belongs to
// one tenant (5 and 6) or can be in several (7 and 8); the tenants have no directories
// (5 and 7: a skeleton without its directories record) or can have them (6 and 8); and
// the staff auth has a Google method whose roles the application gives beside its
// password (5 and 8), a password alone (7), or no password and the two methods the
// shape starts with, Google and Microsoft each open to any organization (6), as each
// section's example leaves it.
// Each is written here or rendered from a skeleton, not made by the commands the
// document shows: what the commands write for those choices is the command package's to
// test.
func documented() map[string]func(t *testing.T) *app.App {
	const (
		password   = "\t_ = session.PasswordSignIn(authSession)\n"
		workspace  = "\tworkspaces, _ := workspaceSlot(settings)\n\t_, _ = google.SignIn(authSession, \"workspace\", nil, &google.Config{Workspaces: workspaces, RoleSync: google.DisableRoleSync()})\n"
		anyAccount = "\tworkspaces := googleSlot()\n\t_, _ = google.SignIn(authSession, \"google\", nil, &google.Config{Workspaces: workspaces, RoleSync: google.DisableRoleSync()})\n"
		synced     = "\t_, _ = google.SignIn(authSession, \"workspace\", nil, &google.Config{Workspaces: google.Workspace(\"fleet\", \"fleet.example\"), RoleSync: google.RoleSync(nil, \"\", nil)})\n"

		workspaceSlot = "func workspaceSlot(settings *Settings) (google.WorkspaceSet, error) {\n\treturn google.Workspace(\"workspace\", settings.Workspace.HostedDomain), nil\n}\n"
		googleSlot    = "func googleSlot() google.WorkspaceSet {\n\treturn google.Workspaces(google.AnyWorkspace(), google.AnyGoogleAccount())\n}\n"
	)
	// oneTenant hands the staff auth's identity client the option that says a person
	// belongs to one tenant, as impulse add tenancy --one-tenant-per-person writes it.
	oneTenant := map[string]func(text string) string{
		"pkg/auth/staff/staff.go": func(text string) string {
			return strings.Replace(text, "identity.WithAdministratorRoles(tenantAdministrators))", "identity.WithAdministratorRoles(tenantAdministrators), identity.WithOneTenantPerPerson())", 1)
		},
	}
	// without is the edits and the removal of the skeleton's directories record: an
	// application whose tenants have no directories.
	without := func(directories string, edits map[string]func(text string) string) map[string]func(text string) string {
		all := map[string]func(text string) string{directories: nil}
		for rel, edit := range edits {
			all[rel] = edit
		}

		return all
	}
	candidate := func(name string, edits map[string]func(text string) string) func(t *testing.T) *app.App {
		return func(t *testing.T) *app.App {
			t.Helper()

			return rendered(t, name, edits)
		}
	}
	staff := func(statements string, decls ...string) func(t *testing.T) *app.App {
		return func(t *testing.T) *app.App {
			t.Helper()

			return written(t, map[string]string{"pkg/auth/staff/staff.go": authFile("staff", statements, decls...)})
		}
	}

	return map[string]func(t *testing.T) *app.App{
		"invite-only":       candidate("solo", nil),
		"directory-members": staff(password+workspace, workspaceSlot),
		"directory-roles": func(t *testing.T) *app.App {
			t.Helper()

			return written(t, map[string]string{"pkg/auth/members/members.go": authFile("members", synced)})
		},
		"open-sign-up":                   staff(anyAccount+password, googleSlot),
		"tenant-owned-users-invite-only": candidate("tenanted", without("pkg/resources/tenant_directories.go", oneTenant)),
		"tenant-owned-users-by-directory": func(t *testing.T) *app.App {
			t.Helper()

			return rendered(t, "tenanted", byDirectory(t, oneTenant["pkg/auth/staff/staff.go"]))
		},
		"global-users-invite-only":         candidate("sites", without("apps/console/pkg/resources/tenant_directories.go", nil)),
		"global-users-each-tenant-chooses": candidate("tenanted", nil),
	}
}

// TestWriteSignIn_document holds the framework's document of the sign-in shapes to what
// this section prints. Under the heading of each of the eight shapes the document quotes
// the line that names an auth's shape, and every line of that quote is a line the
// section prints for an application of the choices that heading's section describes;
// the quoted shape line names the heading's shape. Under "Reading a shape" the document
// quotes a new application's whole section. No other heading has such a quote, so a
// quote added to the document is held here or fails. A position is compared as the
// document writes it, with N for the line.
func TestWriteSignIn_document(t *testing.T) {
	t.Parallel()

	text, err := os.ReadFile(documentFile)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	quoted := make(map[string][]quote)
	for _, q := range quotes(string(text)) {
		quoted[q.heading] = append(quoted[q.heading], q)
	}

	type held struct {
		heading string
		names   string
		app     func(t *testing.T) *app.App
	}
	applications := documented()
	tests := []held{{heading: readingHeading, names: "", app: applications["invite-only"]}}
	for _, s := range shape.All() {
		build, ok := applications[s.Key]
		if !ok {
			t.Fatalf("no application of the shape %s is built here", s)
		}
		tests = append(tests, held{heading: fmt.Sprintf("%d. %s", s.Number, s.Name), names: s.String(), app: build})
	}
	for _, tt := range tests {
		t.Run(tt.heading, func(t *testing.T) {
			t.Parallel()

			if len(quoted[tt.heading]) != 1 {
				t.Fatalf("the document has %d quotes of the sign-in section under %q, want 1", len(quoted[tt.heading]), tt.heading)
			}
			lines := quoted[tt.heading][0].lines
			section := signIn(tt.app(t))
			printed := strings.Split(strings.TrimSuffix(section, "\n"), "\n")
			for _, line := range lines {
				if !slices.Contains(printed, line) {
					t.Errorf("under %q the document quotes a line the section does not print for that application:\n%s\nthe section:\n%s", tt.heading, line, section)
				}
				if isShapeLine(line) && !strings.Contains(line, tt.names) {
					t.Errorf("under %q the quoted shape line does not name %s:\n%s", tt.heading, tt.names, line)
				}
			}
			if tt.heading != readingHeading {
				return
			}
			if diff := cmp.Diff(section, strings.Join(lines, "\n")+"\n"); diff != "" {
				t.Errorf("under %q the document does not quote the whole section (-printed +quoted):\n%s", tt.heading, diff)
			}
		})
	}
	headings := make([]string, 0, len(tests))
	for _, tt := range tests {
		headings = append(headings, tt.heading)
	}
	for heading := range quoted {
		if !slices.Contains(headings, heading) {
			t.Errorf("the document quotes the sign-in section under %q, which this test holds to no application", heading)
		}
	}
}
