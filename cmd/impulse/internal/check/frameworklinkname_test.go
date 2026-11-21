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

func TestFrameworkLinkname(t *testing.T) {
	t.Parallel()

	const plain = "package x\n\nfunc F() {}\n"
	// sealedWrite is the bad case the rule is for: the write of an impersonated session,
	// which the session library keeps to itself, linked to by its symbol's name.
	const sealedWrite = "package x\n\nimport _ \"unsafe\"\n\n" +
		"//go:linkname write github.com/impulseframework/impulse/session/internal/sealed.CreateImpersonatedSession\n" +
		"func write()\n"
	const sealedWriteLine = "//go:linkname write github.com/impulseframework/impulse/session/internal/sealed.CreateImpersonatedSession links to a symbol of github.com/impulseframework/impulse/session/internal/sealed, a package of the framework, by its name: " +
		"the framework is reached through what its packages export. Remove the directive and the declaration under it"
	tests := []struct {
		name string
		// files are the tree, root-relative.
		files       map[string]string
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name:        "a tree with no directive passes, every Go file counted",
			files:       map[string]string{"main.go": plain, "pkg/x/x_test.go": plain, "app/zz_gen_x.go": plain, "README.md": "//go:linkname a github.com/impulseframework/impulse.b\n"},
			wantStatus:  Pass,
			wantSummary: "3 Go file(s); no go:linkname directive names a package of the framework",
		},
		{
			name:        "the sealed write linked to by name fails, with the file and the line",
			files:       map[string]string{"main.go": plain, "pkg/store/reach.go": sealedWrite},
			wantStatus:  Fail,
			wantSummary: "1 go:linkname directive(s) name a package of the framework",
			wantDetails: []string{"pkg/store/reach.go:5: " + sealedWriteLine},
		},
		{
			name: "a variable of the root package and a method of a type fail, each named by its package",
			files: map[string]string{"reach.go": "package x\n\nimport _ \"unsafe\"\n\n" +
				"//go:linkname nilID github.com/impulseframework/impulse.NilUUID\n" +
				"var nilID [16]byte\n\n" +
				"//go:linkname insert github.com/impulseframework/impulse/session/internal/sealed.ImpersonatedSessionWrite.createImpersonatedSession\n" +
				"func insert()\n"},
			wantStatus:  Fail,
			wantSummary: "2 go:linkname directive(s) name a package of the framework",
			wantDetails: []string{
				"reach.go:5: //go:linkname nilID github.com/impulseframework/impulse.NilUUID links to a symbol of github.com/impulseframework/impulse, a package of the framework, by its name: " +
					"the framework is reached through what its packages export. Remove the directive and the declaration under it",
				"reach.go:8: //go:linkname insert github.com/impulseframework/impulse/session/internal/sealed.ImpersonatedSessionWrite.createImpersonatedSession links to a symbol of github.com/impulseframework/impulse/session/internal/sealed, a package of the framework, by its name: " +
					"the framework is reached through what its packages export. Remove the directive and the declaration under it",
			},
		},
		{
			name: "a test file, a generated file, a vendored file and the directories a pattern passes over are read",
			files: map[string]string{
				"pkg/x/x_test.go":         sealedWrite,
				"app/zz_gen_x.go":         sealedWrite,
				"vendor/v/v.go":           sealedWrite,
				"pkg/testdata/t.go":       sealedWrite,
				"_scratch/s.go":           sealedWrite,
				".hidden/h.go":            sealedWrite,
				"web/node_modules/m/m.go": sealedWrite,
				"dist/d.go":               sealedWrite,
			},
			wantStatus:  Fail,
			wantSummary: "8 go:linkname directive(s) name a package of the framework",
			wantDetails: []string{
				".hidden/h.go:5: " + sealedWriteLine,
				"_scratch/s.go:5: " + sealedWriteLine,
				"app/zz_gen_x.go:5: " + sealedWriteLine,
				"dist/d.go:5: " + sealedWriteLine,
				"pkg/testdata/t.go:5: " + sealedWriteLine,
				"pkg/x/x_test.go:5: " + sealedWriteLine,
				"vendor/v/v.go:5: " + sealedWriteLine,
				"web/node_modules/m/m.go:5: " + sealedWriteLine,
			},
		},
		{
			name: "a directive indented, after a tab and behind a statement is read",
			files: map[string]string{"reach.go": "package x\n\nimport _ \"unsafe\"\n\nvar (\n" +
				"\t//go:linkname\ta\tgithub.com/impulseframework/impulse/session.a\n" +
				"\ta int\n" +
				"\tb int //go:linkname b github.com/impulseframework/impulse/access.b\n" +
				")\n"},
			wantStatus:  Fail,
			wantSummary: "2 go:linkname directive(s) name a package of the framework",
			wantDetails: []string{
				"reach.go:6: //go:linkname a github.com/impulseframework/impulse/session.a links to a symbol of github.com/impulseframework/impulse/session, a package of the framework, by its name: " +
					"the framework is reached through what its packages export. Remove the directive and the declaration under it",
				"reach.go:8: //go:linkname b github.com/impulseframework/impulse/access.b links to a symbol of github.com/impulseframework/impulse/access, a package of the framework, by its name: " +
					"the framework is reached through what its packages export. Remove the directive and the declaration under it",
			},
		},
		{
			name: "a file that does not parse is read line by line",
			files: map[string]string{"broken.go": "package x\n\ntype\n\n" +
				"//go:linkname write github.com/impulseframework/impulse/session/internal/sealed.CreateImpersonatedSession\n" +
				"func write()\n"},
			wantStatus:  Fail,
			wantSummary: "1 go:linkname directive(s) name a package of the framework",
			wantDetails: []string{"broken.go:5: " + sealedWriteLine},
		},
		{
			name: "a directive that names another package passes, a path that only opens like the framework's included",
			files: map[string]string{"clock.go": "package x\n\nimport _ \"unsafe\"\n\n" +
				"//go:linkname nanotime runtime.nanotime\n" +
				"func nanotime() int64\n\n" +
				"//go:linkname other github.com/impulseframework/impulse-tools/x.y\n" +
				"func other()\n\n" +
				"//go:linkname third github.com/impulseframework/impulsex.y\n" +
				"func third()\n"},
			wantStatus:  Pass,
			wantSummary: "1 Go file(s); no go:linkname directive names a package of the framework",
		},
		{
			name: "the one-argument form, which names no other package, passes",
			files: map[string]string{"push.go": "package x\n\nimport _ \"unsafe\"\n\n" +
				"//go:linkname exported\n" +
				"func exported() {}\n"},
			wantStatus:  Pass,
			wantSummary: "1 Go file(s); no go:linkname directive names a package of the framework",
		},
		{
			name: "the directive's text in a string, a block comment and a comment about it is no directive",
			files: map[string]string{"doc.go": "package x\n\n" +
				"// The check refuses a line such as\n" +
				"//\n" +
				"//\t//go:linkname write github.com/impulseframework/impulse/session/internal/sealed.CreateImpersonatedSession\n" +
				"//\n" +
				"// and a go:linkname github.com/impulseframework/impulse.x named in passing.\n" +
				"const fixture = `\n" +
				"//go:linkname write github.com/impulseframework/impulse/session/internal/sealed.CreateImpersonatedSession\n" +
				"`\n\n" +
				"/*\n" +
				"//go:linkname write github.com/impulseframework/impulse/session/internal/sealed.CreateImpersonatedSession\n" +
				"*/\n" +
				"//go:linknamed write github.com/impulseframework/impulse/session.x\n" +
				"func F() {}\n"},
			wantStatus:  Pass,
			wantSummary: "1 Go file(s); no go:linkname directive names a package of the framework",
		},
		{
			name:        "a file the Go tool passes over by its own name is in no package's build and is not read",
			files:       map[string]string{"main.go": plain, "pkg/x/_draft.go": sealedWrite, "pkg/x/.draft.go": sealedWrite},
			wantStatus:  Pass,
			wantSummary: "1 Go file(s); no go:linkname directive names a package of the framework",
		},
		{
			name:        "version control's directory is not entered",
			files:       map[string]string{"main.go": plain, ".git/hooks/h.go": sealedWrite, "sub/.git/x.go": sealedWrite},
			wantStatus:  Pass,
			wantSummary: "1 Go file(s); no go:linkname directive names a package of the framework",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for rel, content := range tt.files {
				abs := filepath.Join(root, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := frameworkLinkname{}.Run(context.Background(), &Env{App: &app.App{Root: root}})
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

// TestFrameworkLinkname_aLinkedFile reads a link to a file as the Go tool does, as the
// file, and takes a link to a directory for no source file.
func TestFrameworkLinkname_aLinkedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	kept := filepath.Join(t.TempDir(), "kept.txt")
	src := "package x\n\nimport _ \"unsafe\"\n\n//go:linkname write github.com/impulseframework/impulse/session/internal/sealed.CreateImpersonatedSession\nfunc write()\n"
	if err := os.WriteFile(kept, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(kept, filepath.Join(root, "reach.go")); err != nil {
		t.Skipf("the file system makes no symbolic link: %v", err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "dir.go")); err != nil {
		t.Fatal(err)
	}

	got := frameworkLinkname{}.Run(context.Background(), &Env{App: &app.App{Root: root}})
	if got.Status != Fail || len(got.Details) != 1 {
		t.Fatalf("Run() = %s %q %v, want one directive refused in the linked file", got.Status, got.Summary, got.Details)
	}
	if want := "reach.go:5: //go:linkname write github.com/impulseframework/impulse/session/internal/sealed.CreateImpersonatedSession"; len(got.Details[0]) < len(want) || got.Details[0][:len(want)] != want {
		t.Errorf("Details[0] = %q, want it to open %q", got.Details[0], want)
	}
}

// TestFrameworkLinkname_aLinkedDirectory follows a symbolic link to a directory, as the
// go command does when a package behind one is imported by its path: a directive in a
// file there is refused under the path the link gives it, wherever the link leads. A
// link that leads back above itself ends the walk, and one that leads nowhere is passed
// over.
func TestFrameworkLinkname_aLinkedDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	elsewhere := t.TempDir()
	src := "package shim\n\nimport _ \"unsafe\"\n\n//go:linkname create github.com/impulseframework/impulse/session/internal/sealed.CreateImpersonatedSession\nfunc create()\n"
	if err := os.WriteFile(filepath.Join(elsewhere, "shim.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(root, "internal", "shim")); err != nil {
		t.Skipf("the file system makes no symbolic link: %v", err)
	}
	// A second link to the same directory, a link back to the root and a link to nowhere.
	for name, target := range map[string]string{"again": elsewhere, "loop": root, "gone": filepath.Join(elsewhere, "missing")} {
		if err := os.Symlink(target, filepath.Join(root, "internal", name)); err != nil {
			t.Fatal(err)
		}
	}

	got := frameworkLinkname{}.Run(context.Background(), &Env{App: &app.App{Root: root}})
	if got.Status != Fail || len(got.Details) != 1 {
		t.Fatalf("Run() = %s %q %v, want one directive refused in the linked directory, read once", got.Status, got.Summary, got.Details)
	}
	// The directory is read under the first of its two names.
	if want := "internal/again/shim.go:5: //go:linkname create github.com/impulseframework/impulse/session/internal/sealed.CreateImpersonatedSession"; !strings.HasPrefix(got.Details[0], want) {
		t.Errorf("Details[0] = %q, want it to open %q", got.Details[0], want)
	}
}
