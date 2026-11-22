package apptree

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestAuthPackages holds the reading of an application's auths to the rule: a package
// directly under an auth directory that constructs a session Auth, in a Go file a build
// of the application compiles. Each case that finds no auth takes one part of the rule
// away from a package that would otherwise be one.
func TestAuthPackages(t *testing.T) {
	t.Parallel()

	const (
		constructs = "package staff\n\nimport \"github.com/impulseframework/impulse/session\"\n\nfunc New() {\n\t_, _ = session.NewAuth(nil)\n}\n"
		aliased    = "package staff\n\nimport sess \"github.com/impulseframework/impulse/session\"\n\nfunc New() {\n\t_, _ = sess.NewAuth[string](nil)\n}\n"
		noAuth     = "package staff\n\nimport \"github.com/impulseframework/impulse/session\"\n\nvar _ session.Options\n"
		otherNew   = "package staff\n\nimport (\n\t\"example.com/other/session\"\n)\n\n// github.com/impulseframework/impulse/session is not what this file imports.\nfunc New() {\n\t_, _ = session.NewAuth(nil)\n}\n"
	)
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "each package under an auth directory that constructs an Auth, in name order",
			files: map[string]string{
				"pkg/auth/staff/auth.go":     constructs,
				"pkg/auth/patrons/auth.go":   aliased,
				"pkg/auth/patrons/routes.go": noAuth,
			},
			want: []string{"patrons", "staff"},
		},
		{
			name: "an auth directory anywhere in the tree, and a package found once whatever its files",
			files: map[string]string{
				"internal/auth/crew/auth.go":  constructs,
				"internal/auth/crew/again.go": constructs,
			},
			want: []string{"crew"},
		},
		{
			name: "a package under an auth directory that constructs no Auth is no auth",
			files: map[string]string{
				"pkg/auth/staff/auth.go":  constructs,
				"pkg/auth/shared/opts.go": noAuth,
				"pkg/auth/other/auth.go":  otherNew,
			},
			want: []string{"staff"},
		},
		{
			name: "a construction anywhere but directly under an auth directory is no auth package",
			files: map[string]string{
				"pkg/auth/auth.go":                constructs,
				"pkg/auth/staff/inner/auth.go":    constructs,
				"pkg/authentication/web/auth.go":  constructs,
				"cmd/server/main.go":              constructs,
				"pkg/auth/staff/inner/deep/a.go":  constructs,
				"pkg/authz/auth/not_go/auth.txt":  constructs,
				"pkg/authz/auth/not_go/README.md": constructs,
			},
		},
		{
			name: "a test file and a file the Go tool passes over are not read",
			files: map[string]string{
				"pkg/auth/staff/auth_test.go": constructs,
				"pkg/auth/crew/_auth.go":      constructs,
				"pkg/auth/patrons/.auth.go":   constructs,
			},
		},
		{
			name: "a directory a walk leaves alone is not descended into",
			files: map[string]string{
				"vendor/example.com/x/auth/staff/auth.go": constructs,
				"node_modules/x/auth/staff/auth.go":       constructs,
				"dist/auth/staff/auth.go":                 constructs,
				"_old/pkg/auth/staff/auth.go":             constructs,
				".cache/auth/staff/auth.go":               constructs,
				"pkg/testdata/auth/staff/auth.go":         constructs,
				"pkg/auth/_staff/auth.go":                 constructs,
			},
		},
		{
			name: "a module of its own under the root is another module's",
			files: map[string]string{
				"tools/go.mod":                 "module example.com/acme/beacon/tools\n\ngo 1.26\n",
				"tools/pkg/auth/staff/auth.go": constructs,
				"pkg/auth/crew/auth.go":        constructs,
			},
			want: []string{"crew"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writeTree(t, root, map[string]string{"go.mod": "module example.com/acme/beacon\n\ngo 1.26\n"})
			writeTree(t, root, tt.files)

			got, err := AuthPackages(root)
			if err != nil {
				t.Fatalf("AuthPackages() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("AuthPackages() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAuthPackages_rootIsReadWhateverItIsCalled pins that the application's own root is
// read whatever its directory is called: the walk's rules are about what lies under it.
func TestAuthPackages_rootIsReadWhateverItIsCalled(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "testdata", "_app")
	writeTree(t, root, map[string]string{
		"pkg/auth/staff/auth.go": "package staff\n\nimport \"github.com/impulseframework/impulse/session\"\n\nfunc New() {\n\t_, _ = session.NewAuth(nil)\n}\n",
	})

	got, err := AuthPackages(root)
	if err != nil {
		t.Fatalf("AuthPackages() error = %v", err)
	}
	if diff := cmp.Diff([]string{"staff"}, got); diff != "" {
		t.Errorf("AuthPackages() mismatch (-want +got):\n%s", diff)
	}
}

// TestAuthPackages_aFileThatDoesNotParse pins that a Go file of an auth package that
// imports the session package and does not parse is an error, not a package passed over:
// a run that could not read an auth would hold the sets to a list short of one.
func TestAuthPackages_aFileThatDoesNotParse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"pkg/auth/staff/auth.go": "package staff\n\nimport \"github.com/impulseframework/impulse/session\"\n\nfunc New( {\n",
	})

	if _, err := AuthPackages(root); err == nil {
		t.Fatal("AuthPackages() error = nil, want the file's parse error")
	}
}

// TestAuthPackageName pins the place an auth package's files have in the tree: directly
// in a directory under an auth directory.
func TestAuthPackageName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file string
		want string
	}{
		{file: "pkg/auth/staff/auth.go", want: "staff"},
		{file: "auth/crew/auth.go", want: "crew"},
		{file: "pkg/auth/auth.go"},
		{file: "pkg/auth/staff/inner/auth.go"},
		{file: "pkg/authz/staff/auth.go"},
		{file: "main.go"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()

			if got := AuthPackageName(tt.file); got != tt.want {
				t.Errorf("AuthPackageName(%q) = %q, want %q", tt.file, got, tt.want)
			}
		})
	}
}

// writeTree writes files, keyed by slash-separated path, under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
