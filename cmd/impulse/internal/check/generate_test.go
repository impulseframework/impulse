package check

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runsExec answers each go generate ./... in turn, running the side effect the run has on
// the tree before it answers.
type runsExec struct {
	t     *testing.T
	runs  []fakeAnswer
	calls int
}

func (f *runsExec) Run(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, error) {
	if name != "go" || len(args) != 2 || args[0] != "generate" || args[1] != "./..." {
		f.t.Fatalf("unexpected command %s %v", name, args)
	}
	if f.calls >= len(f.runs) {
		f.t.Fatalf("go generate ./... ran %d time(s), want %d", f.calls+1, len(f.runs))
	}
	run := f.runs[f.calls]
	f.calls++
	if run.run != nil {
		run.run()
	}

	return []byte(run.out), run.err
}

// TestGenerate holds the one case go generate ./... is run twice for: the go command
// stopped at a Go file of the application that the run itself removed. The second run
// is the answer, whatever it is; every other failure is answered as it came.
func TestGenerate(t *testing.T) {
	t.Parallel()

	const stale = "pkg/router/zz_gen_user_surfaces.go"

	tests := []struct {
		name string
		// runs are the go generate runs in turn; stopAt formats the go command's last line
		// for a file under the root.
		runs        func(root string, remove func()) []fakeAnswer
		wantCalls   int
		wantOut     string
		wantRemoved string
		wantErr     bool
	}{
		{
			name: "a run that succeeds is run once",
			runs: func(string, func()) []fakeAnswer {
				return []fakeAnswer{{out: "Finished Resource generation in 1s\n"}}
			},
			wantCalls: 1, wantOut: "Finished Resource generation in 1s\n",
		},
		{
			name: "the go command stopped at a file the run removed: the second run is the answer",
			runs: func(root string, remove func()) []fakeAnswer {
				return []fakeAnswer{
					{out: "Finished Resource generation in 1s\ngenerate: open " + filepath.Join(root, stale) + ": no such file or directory\n", err: errExit, run: remove},
					{out: "Finished Resource generation in 2s\n"},
				}
			},
			wantCalls: 2, wantOut: "Finished Resource generation in 2s\n", wantRemoved: stale,
		},
		{
			name: "the system's words for a missing file are not read, only the file",
			runs: func(root string, remove func()) []fakeAnswer {
				return []fakeAnswer{
					{out: "generate: open " + filepath.Join(root, stale) + ": The system cannot find the file specified.\n", err: errExit, run: remove},
					{out: "second\n"},
				}
			},
			wantCalls: 2, wantOut: "second\n", wantRemoved: stale,
		},
		{
			name: "a second run that fails is answered as it failed",
			runs: func(root string, remove func()) []fakeAnswer {
				return []fakeAnswer{
					{out: "generate: open " + filepath.Join(root, stale) + ": no such file or directory\n", err: errExit, run: remove},
					{out: "generation: boom\n", err: errExit},
				}
			},
			wantCalls: 2, wantOut: "generation: boom\n", wantRemoved: stale, wantErr: true,
		},
		{
			name: "a file the go command could not open that is still there is not this case",
			runs: func(root string, _ func()) []fakeAnswer {
				return []fakeAnswer{{out: "generate: open " + filepath.Join(root, stale) + ": permission denied\n", err: errExit}}
			},
			wantCalls: 1, wantOut: "generate: open ROOT/" + stale + ": permission denied\n", wantErr: true,
		},
		{
			name: "a missing file outside the application is not this case",
			runs: func(root string, _ func()) []fakeAnswer {
				return []fakeAnswer{{out: "generate: open " + filepath.Join(filepath.Dir(root), "elsewhere", "zz_gen_x.go") + ": no such file or directory\n", err: errExit}}
			},
			wantCalls: 1, wantErr: true,
		},
		{
			name: "a generator's own failure is answered as it came",
			runs: func(string, func()) []fakeAnswer {
				return []fakeAnswer{{out: "emulator not running\n", err: errExit}}
			},
			wantCalls: 1, wantOut: "emulator not running\n", wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(stale))
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("package router\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			remove := func() {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}

			runs := &runsExec{t: t, runs: tt.runs(root, remove)}
			out, removed, err := Generate(context.Background(), runs, root)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Generate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if runs.calls != tt.wantCalls {
				t.Errorf("go generate ./... ran %d time(s), want %d", runs.calls, tt.wantCalls)
			}
			if removed != tt.wantRemoved {
				t.Errorf("Generate() removed = %q, want %q", removed, tt.wantRemoved)
			}
			if want := strings.Replace(tt.wantOut, "ROOT/", root+"/", 1); tt.wantOut != "" && string(out) != want {
				t.Errorf("Generate() output = %q, want %q", out, want)
			}
		})
	}
}

// countingExec runs the commands for real and counts them.
type countingExec struct {
	OSExec
	calls int
}

func (c *countingExec) Run(ctx context.Context, dir string, extraEnv []string, name string, args ...string) ([]byte, error) {
	c.calls++

	return c.OSExec.Run(ctx, dir, extraEnv, name, args...)
}

// TestGenerate_theGoCommand runs the real go command over a module whose one directive
// removes a Go file in a package the go command reads after the directive's own. It
// holds the two things Generate reads to the go command itself: that the command stops
// at the file with a line of the form Generate matches, and that a second run finishes.
func TestGenerate_theGoCommand(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("go"); err != nil {
		t.Fatalf("the go command is not on the path: %v", err)
	}

	root := t.TempDir()
	for rel, content := range map[string]string{
		"go.mod":                     "module example.com/removes\n\ngo 1.24\n",
		"cmd/generate/gen.go":        "package main\n\n//go:generate go run . ../../pkg/router/zz_gen_stale.go\n\nimport \"os\"\n\nfunc main() {\n\tif len(os.Args) > 1 {\n\t\t_ = os.Remove(os.Args[1])\n\t}\n}\n",
		"pkg/router/router.go":       "package router\n",
		"pkg/router/zz_gen_stale.go": "package router\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The go command alone, first: it stops at the removed file and exits 1.
	plain, err := OSExec{}.Run(t.Context(), root, nil, "go", "generate", "./...")
	if err == nil {
		t.Fatalf("go generate ./... over a module whose directive removes a later package's file succeeded; the go command no longer stops there, and Generate's second run has nothing left to do:\n%s", plain)
	}
	if got := removedDuringGenerate(plain, root); got != "pkg/router/zz_gen_stale.go" {
		t.Fatalf("removedDuringGenerate() = %q on the go command's own output, want the removed file:\n%s", got, plain)
	}

	// Then through Generate, with the file back: two runs, and the second one finishes.
	if err := os.WriteFile(filepath.Join(root, "pkg", "router", "zz_gen_stale.go"), []byte("package router\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	counting := &countingExec{}
	out, removed, err := Generate(t.Context(), counting, root)
	if err != nil {
		t.Fatalf("Generate() error = %v, want the second run to finish:\n%s", err, out)
	}
	if counting.calls != 2 || removed != "pkg/router/zz_gen_stale.go" {
		t.Errorf("Generate() ran go generate %d time(s) and names %q, want 2 runs naming the removed file", counting.calls, removed)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "router", "zz_gen_stale.go")); !os.IsNotExist(err) {
		t.Errorf("the removed file is still there after Generate(): %v", err)
	}
}
