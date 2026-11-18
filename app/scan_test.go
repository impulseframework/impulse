package app

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestScanReadsWhatTheGoToolReads holds the scan to the Go files the application's builds
// compile: nothing under a directory whose name begins with "_" or ".", nothing under
// testdata, unless the application imports the package by its path; no Go file whose own
// name begins with "_" or "."; and nothing of a module of its own under the root. A file
// placed directly in a directory the Go tool reads, tmp among them, is read, as go build
// ./... compiles it.
func TestScanReadsWhatTheGoToolReads(t *testing.T) {
	t.Parallel()

	const (
		program = "package main\n\nfunc main() {}\n"
		harness = "package main\n\nimport \"testing\"\n\nfunc TestMain(m *testing.M) {\n\tc, _ := dbinitiator.NewSpannerContainer(ctx, \"1.5.56\")\n\t_ = c\n}\n"
	)
	tests := []struct {
		name string
		// root is the application's root under the test's directory; empty for the
		// directory itself.
		root          string
		files         map[string]string
		wantGoFiles   []string
		wantWebApps   []WebApp
		wantHarnesses []EmulatorRef
		wantMains     []string
	}{
		{
			name: "a scratch program under tmp/_name is not the application's, a file placed directly in tmp is",
			files: map[string]string{
				"cmd/tool/main.go":    program,
				"tmp/main.go":         program,
				"tmp/_sqlrun/main.go": program,
			},
			wantGoFiles: []string{"cmd/tool/main.go", "tmp/main.go"},
			wantMains:   []string{"cmd/tool", "tmp"},
		},
		{
			name: "a directory whose name begins with an underscore or a dot is passed over, however deep",
			files: map[string]string{
				"cmd/tool/main.go":           program,
				"_scratch/main.go":           program,
				"_scratch/inner/main.go":     program,
				".cache/gen/main.go":         program,
				"pkg/thing/.idea/main.go":    program,
				"pkg/thing/_old/cmd/main.go": program,
			},
			wantGoFiles: []string{"cmd/tool/main.go"},
			wantMains:   []string{"cmd/tool"},
		},
		{
			name: "testdata is passed over, a directory that only has the word in its name is read",
			files: map[string]string{
				"cmd/tool/main.go":              program,
				"cmd/tool/testdata/main.go":     program,
				"pkg/thing/testdata/x/main.go":  program,
				"pkg/testdatabase/cmd/main.go":  program,
				"pkg/thing/testdata_v2/main.go": program,
			},
			wantGoFiles: []string{"cmd/tool/main.go", "pkg/testdatabase/cmd/main.go", "pkg/thing/testdata_v2/main.go"},
			wantMains:   []string{"cmd/tool", "pkg/testdatabase/cmd", "pkg/thing/testdata_v2"},
		},
		{
			name: "a Go file whose own name begins with an underscore or a dot is passed over, one that only holds one is read",
			files: map[string]string{
				"cmd/tool/main.go":      program,
				"cmd/tool/_draft.go":    program,
				"cmd/tool/.main.go":     program,
				"cmd/tool/my_draft.go":  program,
				"cmd/tool/zz_gen_x.go":  program,
				"cmd/other/_main.go":    program,
				"cmd/other/_x_test.go":  harness,
				"cmd/other/.x_test.go":  harness,
				"cmd/other/x_test.go":   harness,
				"cmd/other/placeholder": "",
			},
			wantGoFiles:   []string{"cmd/tool/main.go", "cmd/tool/my_draft.go", "cmd/tool/zz_gen_x.go"},
			wantHarnesses: []EmulatorRef{{File: "cmd/other/x_test.go", Line: 6, Version: "1.5.56"}},
			wantMains:     []string{"cmd/tool"},
		},
		{
			name: "a test file and a browser application under a passed-over directory are not the application's",
			files: map[string]string{
				"cmd/tool/main.go":                 program,
				"web/console/angular.json":         "{}\n",
				"_backup/web/console/angular.json": "{}\n",
				"test/testdata/web/angular.json":   "{}\n",
				"test/integration/main_test.go":    harness,
				"test/testdata/main_test.go":       harness,
				"_scratch/main_test.go":            harness,
				".tools/main_test.go":              harness,
			},
			wantGoFiles:   []string{"cmd/tool/main.go"},
			wantWebApps:   []WebApp{{Dir: "web/console"}},
			wantHarnesses: []EmulatorRef{{File: "test/integration/main_test.go", Line: 6, Version: "1.5.56"}},
			wantMains:     []string{"cmd/tool"},
		},
		{
			name: "a directory under the root with a go.mod is another module: its Go files are not read, its browser application is found",
			files: map[string]string{
				"cmd/tool/main.go":    program,
				"web/go.mod":          "module example.com/acme/beacon/web\n\ngo 1.26\n",
				"web/angular.json":    "{}\n",
				"web/tools/gen.go":    program,
				"web/tools/deep/x.go": program,
				"web/main_test.go":    harness,
				"tools/go.mod":        "module example.com/acme/tools\n\ngo 1.26\n",
				"tools/main.go":       program,
				"tools/go.mod.go":     program,
				"pkg/gomod/main.go":   program,
			},
			wantGoFiles: []string{"cmd/tool/main.go", "pkg/gomod/main.go"},
			wantWebApps: []WebApp{{Dir: "web"}},
			wantMains:   []string{"cmd/tool", "pkg/gomod"},
		},
		{
			name: "a package under a passed-over directory that the application imports is read, with what it imports in turn and nothing beside it",
			files: map[string]string{
				"cmd/tool/main.go":                    "package main\n\nimport _ \"example.com/acme/beacon/internal/_cfg\"\n\nfunc main() {}\n",
				"internal/_cfg/cfg.go":                "package cfg\n\nimport _ \"example.com/acme/beacon/internal/_cfg/testdata/deep\"\n",
				"internal/_cfg/_draft.go":             "package cfg\n",
				"internal/_cfg/testdata/deep/deep.go": "package deep\n",
				"internal/_cfg/other/other.go":        "package other\n",
				"internal/_unused/unused.go":          "package unused\n",
			},
			wantGoFiles: []string{"cmd/tool/main.go", "internal/_cfg/cfg.go", "internal/_cfg/testdata/deep/deep.go"},
			wantMains:   []string{"cmd/tool"},
		},
		{
			name: "a fixture package a test imports from testdata is read, as go test compiles it, and its harness with it",
			files: map[string]string{
				"pkg/thing/thing.go":                 "package thing\n",
				"pkg/thing/thing_test.go":            "package thing\n\nimport (\n\t\"testing\"\n\n\t_ \"example.com/acme/beacon/pkg/thing/testdata/fix\"\n)\n\nfunc TestThing(t *testing.T) {}\n",
				"pkg/thing/testdata/fix/fix.go":      "package fix\n",
				"pkg/thing/testdata/fix/fix_test.go": harness,
				"pkg/thing/testdata/other/other.go":  "package other\n",
			},
			wantGoFiles:   []string{"pkg/thing/testdata/fix/fix.go", "pkg/thing/thing.go"},
			wantHarnesses: []EmulatorRef{{File: "pkg/thing/testdata/fix/fix_test.go", Line: 6, Version: "1.5.56"}},
		},
		{
			name: "an import of another module, of a module of its own under a passed-over directory, or of a directory the tree does not hold leads nowhere",
			files: map[string]string{
				"cmd/tool/main.go":  "package main\n\nimport (\n\t_ \"example.com/acme/beacon/_tools/gen\"\n\t_ \"example.com/acme/beacon/internal/_gone\"\n\t_ \"example.com/other/_cfg\"\n)\n\nfunc main() {}\n",
				"_tools/go.mod":     "module example.com/acme/beacon/_tools\n\ngo 1.26\n",
				"_tools/gen/gen.go": program,
				"_scratch/main.go":  program,
			},
			wantGoFiles: []string{"cmd/tool/main.go"},
			wantMains:   []string{"cmd/tool"},
		},
		{
			name: "the application's own root is read whatever it is called",
			root: "testdata/_app",
			files: map[string]string{
				"cmd/tool/main.go": program,
				"_scratch/main.go": program,
			},
			wantGoFiles: []string{"cmd/tool/main.go"},
			wantMains:   []string{"cmd/tool"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := filepath.Join(t.TempDir(), filepath.FromSlash(tt.root))
			writeTree(t, root, map[string]string{"go.mod": "module example.com/acme/beacon\n\ngo 1.26\n"})
			writeTree(t, root, tt.files)
			a, err := Discover(root)
			if err != nil {
				t.Fatalf("Discover() error = %v", err)
			}
			got := slices.Clone(a.GoFiles())
			slices.Sort(got)
			if diff := cmp.Diff(tt.wantGoFiles, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("GoFiles() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantWebApps, a.WebApps, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("WebApps mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantHarnesses, a.EmulatorHarnesses, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("EmulatorHarnesses mismatch (-want +got):\n%s", diff)
			}
			mains := slices.Clone(a.MainPackages)
			slices.Sort(mains)
			if diff := cmp.Diff(tt.wantMains, mains, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("MainPackages mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestGoToolReads pins the one rule about which files are Go files the Go tool reads, by
// name: the suffix, and no name that begins with "_" or ".". A test file and a generated
// file are read; a reader that leaves them out says so itself.
func TestGoToolReads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want bool
	}{
		{name: "main.go", want: true},
		{name: "harness_test.go", want: true},
		{name: "zz_gen_router.go", want: true},
		{name: "sql_windows.go", want: true},
		{name: "_scratch.go", want: false},
		{name: ".draft.go", want: false},
		{name: "_scratch_test.go", want: false},
		{name: ".go", want: false},
		{name: "notes.txt", want: false},
		{name: "go", want: false},
		{name: "main.go.bak", want: false},
	}
	for _, tt := range tests {
		if got := GoToolReads(tt.name); got != tt.want {
			t.Errorf("GoToolReads(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
