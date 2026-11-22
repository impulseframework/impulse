package generate

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/solo/cmd/generate/internal/regen"
)

// TestGeneratedCodeIsCommitted re-runs the full generation pipeline and fails if any
// generated file differs from the state on disk. The zz_gen files (Go, TypeScript, DOT)
// are the golden output of the resource generators: any drift here is a generator
// behavior change that must be either intentional (commit the new output) or a
// regression. The comparison is regen.Exclusive's, made between taking the module's
// generation lock and the end of the test, so it holds whether the tree is committed or
// still untracked. Every test of the module that regenerates is held to it, so the first
// of them to run reports a difference, whichever it is.
//
// Requires the Spanner emulator (podman/docker), like the rest of this module's tests.
func TestGeneratedCodeIsCommitted(t *testing.T) {
	if testing.Short() {
		t.Skip("generation requires the Spanner emulator")
	}
	// go generate rewrites the tree, as each program's warnings test does from another
	// test process: one generation at a time, each compared with the tree it found.
	regen.Exclusive(t)

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	moduleRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	generate := exec.CommandContext(t.Context(), "go", "generate", "./...")
	generate.Dir = moduleRoot
	if out, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("go generate ./...: %v\n%s", err, out)
	}
}
