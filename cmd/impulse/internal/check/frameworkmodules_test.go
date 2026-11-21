package check

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/mod/modfile"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/framework"
)

func TestFrameworkModules(t *testing.T) {
	t.Parallel()

	const inside = "to the go command its packages are inside the framework and may import the packages the framework keeps to itself (its internal directories)"
	const passes = "no module under the framework's path (github.com/impulseframework/impulse) but the framework's own"
	// underTheSession is the bad case the rule is for: a module of the application's own
	// given a path under the session library's, whose packages the go command lets
	// import session/internal/sealed, where the write of an impersonated session is.
	const underTheSession = "module example.com/beacon\n\ngo 1.27\n\n" +
		"require (\n\tgithub.com/impulseframework/impulse v0.1.0\n\tgithub.com/impulseframework/impulse/session/reach v0.0.0\n)\n\n" +
		"replace github.com/impulseframework/impulse/session/reach => ./internal/reach\n"
	// orOut is what a line about the go.mod of a directory the workspace uses adds.
	orOut := func(use string) string {
		return ", or take " + use + " out of the workspace: go.work uses it, and in a workspace the go command reads the requirements and applies the replaces of every module it uses"
	}
	tests := []struct {
		name string
		// goMod is the application's go.mod, and files the rest of the tree, relative to
		// the application's root, which stands one directory inside the test's own.
		goMod       string
		files       map[string]string
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name:        "an application that requires the framework and modules of its own passes",
			goMod:       "module example.com/beacon\n\ngo 1.27\n\nrequire (\n\tgithub.com/impulseframework/impulse v0.1.0\n\tgithub.com/go-chi/chi/v5 v5.5.0\n)\n",
			wantStatus:  Pass,
			wantSummary: passes,
		},
		{
			name:        "a module of the application's own under the session library's path fails, by its requirement and by its replace",
			goMod:       underTheSession,
			wantStatus:  Fail,
			wantSummary: "2 module path(s) under the framework's (github.com/impulseframework/impulse) that are not the framework's",
			wantDetails: []string{
				"go.mod requires github.com/impulseframework/impulse/session/reach, a module under the framework's path that is not the framework's: " + inside + ". The framework is the one module github.com/impulseframework/impulse; remove the requirement and what imports it",
				"go.mod replaces github.com/impulseframework/impulse/session/reach with ./internal/reach, and the replaced module is under the framework's path and is not the framework's: " + inside + ". Remove the replace",
			},
		},
		{
			name:        "an indirect requirement fails as a direct one does",
			goMod:       "module example.com/beacon\n\ngo 1.27\n\nrequire github.com/impulseframework/impulse v0.1.0\n\nrequire github.com/impulseframework/impulse/access/reach v0.3.0 // indirect\n",
			wantStatus:  Fail,
			wantSummary: "1 module path(s) under the framework's (github.com/impulseframework/impulse) that are not the framework's",
			wantDetails: []string{
				"go.mod requires github.com/impulseframework/impulse/access/reach, a module under the framework's path that is not the framework's: " + inside + ". The framework is the one module github.com/impulseframework/impulse; remove the requirement and what imports it",
			},
		},
		{
			name:        "a replace alone fails: a build that adds the requirement itself reads it",
			goMod:       "module example.com/beacon\n\ngo 1.27\n\nrequire github.com/impulseframework/impulse v0.1.0\n\nreplace github.com/impulseframework/impulse/session/reach v0.0.0 => example.com/elsewhere v1.2.0\n",
			wantStatus:  Fail,
			wantSummary: "1 module path(s) under the framework's (github.com/impulseframework/impulse) that are not the framework's",
			wantDetails: []string{
				"go.mod replaces github.com/impulseframework/impulse/session/reach with example.com/elsewhere, and the replaced module is under the framework's path and is not the framework's: " + inside + ". Remove the replace",
			},
		},
		{
			name:        "an application whose own module path is under the framework's fails",
			goMod:       "module github.com/impulseframework/impulse/session/beacon\n\ngo 1.27\n\nrequire github.com/impulseframework/impulse v0.1.0\n",
			wantStatus:  Fail,
			wantSummary: "1 module path(s) under the framework's (github.com/impulseframework/impulse) that are not the framework's",
			wantDetails: []string{
				"go.mod: the application's module path github.com/impulseframework/impulse/session/beacon is under the framework's (github.com/impulseframework/impulse): " + inside + ". Give the module a path of the application's own",
			},
		},
		{
			name:        "the framework's own test application passes, and so does a replace of the framework module, which is the pins check's",
			goMod:       "module github.com/impulseframework/impulse/lodestar\n\ngo 1.27\n\nrequire github.com/impulseframework/impulse v0.0.0-00010101000000-000000000000\n\nreplace github.com/impulseframework/impulse => ../\n",
			wantStatus:  Pass,
			wantSummary: passes,
		},
		{
			name: "a path that only opens like the framework's passes",
			goMod: "module example.com/beacon\n\ngo 1.27\n\n" +
				"require (\n\tgithub.com/impulseframework/impulse v0.1.0\n\tgithub.com/impulseframework/impulse-tools v0.1.0\n\tgithub.com/impulseframework/impulsex/session v0.1.0\n)\n",
			wantStatus:  Pass,
			wantSummary: passes,
		},
		{
			name:  "a workspace that uses a directory whose module is under the framework's path fails",
			goMod: "module example.com/beacon\n\ngo 1.27\n\nrequire github.com/impulseframework/impulse v0.1.0\n",
			files: map[string]string{
				"go.work":                "go 1.27\n\nuse (\n\t.\n\t./internal/reach\n\t./tools\n)\n",
				"internal/reach/go.mod":  "module github.com/impulseframework/impulse/session/reach\n\ngo 1.27\n",
				"tools/go.mod":           "module example.com/beacon/tools\n\ngo 1.27\n",
				"internal/reach/doc.go":  "package reach\n",
				"internal/unused/go.mod": "module github.com/impulseframework/impulse/session/unused\n\ngo 1.27\n",
			},
			wantStatus:  Fail,
			wantSummary: "1 module path(s) under the framework's (github.com/impulseframework/impulse) that are not the framework's",
			wantDetails: []string{
				"go.work uses ./internal/reach, whose module is github.com/impulseframework/impulse/session/reach, under the framework's path and not the framework's: " + inside + ". Take the directory out of the workspace",
			},
		},
		{
			name:  "a workspace that replaces a module under the framework's path fails",
			goMod: "module example.com/beacon\n\ngo 1.27\n\nrequire github.com/impulseframework/impulse v0.1.0\n",
			files: map[string]string{
				"go.work": "go 1.27\n\nuse .\n\nreplace github.com/impulseframework/impulse/resource/reach => ../reach\n",
			},
			wantStatus:  Fail,
			wantSummary: "1 module path(s) under the framework's (github.com/impulseframework/impulse) that are not the framework's",
			wantDetails: []string{
				"go.work replaces github.com/impulseframework/impulse/resource/reach with ../reach, and the replaced module is under the framework's path and is not the framework's: " + inside + ". Remove the replace",
			},
		},
		{
			name:  "a workspace that uses a directory whose go.mod requires and replaces a module under the framework's path fails: the go command applies that replace to the whole build",
			goMod: "module example.com/beacon\n\ngo 1.27\n\nrequire github.com/impulseframework/impulse v0.1.0\n",
			files: map[string]string{
				"go.work": "go 1.27\n\nuse (\n\t.\n\t./tools\n)\n",
				"tools/go.mod": "module example.com/beacon/tools\n\ngo 1.27\n\n" +
					"require github.com/impulseframework/impulse/session/reach v0.0.0\n\n" +
					"replace github.com/impulseframework/impulse/session/reach => ../reach\n",
				"reach/go.mod": "module github.com/impulseframework/impulse/session/reach\n\ngo 1.27\n",
			},
			wantStatus:  Fail,
			wantSummary: "2 module path(s) under the framework's (github.com/impulseframework/impulse) that are not the framework's",
			wantDetails: []string{
				"tools/go.mod requires github.com/impulseframework/impulse/session/reach, a module under the framework's path that is not the framework's: " + inside + ". The framework is the one module github.com/impulseframework/impulse; remove the requirement and what imports it" + orOut("./tools"),
				"tools/go.mod replaces github.com/impulseframework/impulse/session/reach with ../reach, and the replaced module is under the framework's path and is not the framework's: " + inside + ". Remove the replace" + orOut("./tools"),
			},
		},
		{
			name:  "a used directory outside the application's root is read the same way",
			goMod: "module example.com/beacon\n\ngo 1.27\n\nrequire github.com/impulseframework/impulse v0.1.0\n",
			files: map[string]string{
				"go.work":          "go 1.27\n\nuse (\n\t.\n\t../shared\n)\n",
				"../shared/go.mod": "module example.com/shared\n\ngo 1.27\n\nreplace github.com/impulseframework/impulse/access/reach => ./reach\n",
			},
			wantStatus:  Fail,
			wantSummary: "1 module path(s) under the framework's (github.com/impulseframework/impulse) that are not the framework's",
			wantDetails: []string{
				"../shared/go.mod replaces github.com/impulseframework/impulse/access/reach with ./reach, and the replaced module is under the framework's path and is not the framework's: " + inside + ". Remove the replace" + orOut("../shared"),
			},
		},
		{
			name:  "a used directory whose go.mod names modules of its own and the framework's passes",
			goMod: "module example.com/beacon\n\ngo 1.27\n\nrequire github.com/impulseframework/impulse v0.1.0\n",
			files: map[string]string{
				"go.work": "go 1.27\n\nuse (\n\t.\n\t./tools\n)\n",
				"tools/go.mod": "module example.com/beacon/tools\n\ngo 1.27\n\n" +
					"require (\n\tgithub.com/impulseframework/impulse v0.1.0\n\texample.com/elsewhere v1.2.0\n)\n\n" +
					"replace github.com/impulseframework/impulse => ../../framework\n",
			},
			wantStatus:  Pass,
			wantSummary: passes,
		},
		{
			name:  "a workspace of the application and the framework's checkout passes",
			goMod: "module example.com/beacon\n\ngo 1.27\n\nrequire github.com/impulseframework/impulse v0.1.0\n",
			files: map[string]string{
				"go.work":             "go 1.27\n\nuse (\n\t.\n\t./framework\n\t./missing\n)\n",
				"framework/go.mod":    "module github.com/impulseframework/impulse\n\ngo 1.27\n",
				"framework/README.md": "the framework's checkout\n",
			},
			wantStatus:  Pass,
			wantSummary: passes,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := filepath.Join(t.TempDir(), "beacon")
			if err := os.MkdirAll(root, 0o750); err != nil {
				t.Fatal(err)
			}
			for rel, content := range tt.files {
				abs := filepath.Join(root, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			mod, err := modfile.Parse("go.mod", []byte(tt.goMod), nil)
			if err != nil {
				t.Fatal(err)
			}
			got := frameworkModules{}.Run(context.Background(), &Env{App: &app.App{Root: root, GoMod: mod}})
			if got.Status != tt.wantStatus {
				t.Fatalf("Status = %s, want %s (%s: %v)", got.Status, tt.wantStatus, got.Summary, got.Details)
			}
			if got.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", got.Summary, tt.wantSummary)
			}
			if diff := cmp.Diff(tt.wantDetails, got.Details); diff != "" {
				t.Errorf("Details mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestFrameworkModules_aWorkspaceThatDoesNotParse fails saying so: a workspace file the
// check cannot read is one it cannot answer for.
func TestFrameworkModules_aWorkspaceThatDoesNotParse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("use (\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mod, err := modfile.Parse("go.mod", []byte("module example.com/beacon\n\ngo 1.27\n"), nil)
	if err != nil {
		t.Fatal(err)
	}

	got := frameworkModules{}.Run(context.Background(), &Env{App: &app.App{Root: root, GoMod: mod}})
	if got.Status != Fail || !strings.Contains(got.Summary, "go.work does not parse") {
		t.Errorf("Run() = %s %q, want a failure that says go.work does not parse", got.Status, got.Summary)
	}
}

// TestFrameworkModules_aUsedModuleFileThatDoesNotParse fails saying so: the go.mod of a
// directory the workspace uses is one of the files that decide the build, and one the
// check cannot read is one it cannot answer for.
func TestFrameworkModules_aUsedModuleFileThatDoesNotParse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.27\n\nuse (\n\t.\n\t./tools\n)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tools", "go.mod"), []byte("module example.com/beacon/tools\n\nrequire (\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mod, err := modfile.Parse("go.mod", []byte("module example.com/beacon\n\ngo 1.27\n"), nil)
	if err != nil {
		t.Fatal(err)
	}

	got := frameworkModules{}.Run(context.Background(), &Env{App: &app.App{Root: root, GoMod: mod}})
	if got.Status != Fail || !strings.Contains(got.Summary, "tools/go.mod, the go.mod of a directory go.work uses, does not parse") {
		t.Errorf("Run() = %s %q, want a failure that says tools/go.mod does not parse", got.Status, got.Summary)
	}
}

// TestFrameworkOwnModulesAreTheRepositorys holds the list of the framework's own modules
// (framework.OwnModules) to the module files the framework's repository holds: every
// go.mod under the repository's root that the Go tool reads, a test's fixture left out.
// A module added to the repository, or taken out of it, changes the list in the same
// commit.
func TestFrameworkOwnModulesAreTheRepositorys(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (name == "node_modules" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}

			return nil
		}
		if name == "go.mod" {
			files = append(files, p)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	found := make([]string, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, modfile.ModulePath(data))
	}
	slices.Sort(found)
	want := slices.Clone(framework.OwnModules)
	slices.Sort(want)
	if diff := cmp.Diff(want, found); diff != "" {
		t.Errorf("the repository's modules are not framework.OwnModules (-list +repository):\n%s", diff)
	}
	if found[0] != FrameworkModule {
		t.Errorf("the repository's root module = %q, want %q", found[0], FrameworkModule)
	}
}
