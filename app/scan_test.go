package app

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestScanReadsWhatTheGoToolReads holds the scan to the tree the Go tool reads: nothing
// under a directory whose name begins with "_" or ".", nothing under testdata, and no Go
// file whose own name begins with "_" or ".". A file placed directly in a directory the
// Go tool reads, tmp among them, is read, as go build ./... compiles it.
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
