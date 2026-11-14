package check

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/app"
)

// regen re-runs code generation and fails when any generated file changes. The generated
// files on disk are the golden output of the generators, so the comparison is content
// against content — it holds whether the tree is committed, dirty, or not yet tracked.
// A generated file is one named zz_gen, or any file under a directory a generator owns
// whole (generatedTrees).
type regen struct{}

func (regen) Name() string { return "regen" }

func (regen) Describe() string {
	return "go generate ./... reproduces the generated files on disk and its schema warnings are listed (needs the Spanner emulator)"
}

// warningPrefix is how a generate program prints one schema warning: a performance
// finding about the schema the application decides on, never a failure here. The
// program's warnings test is what gates the accepted set.
const warningPrefix = "Warning: "

func (c regen) Run(ctx context.Context, env *Env) Result {
	if env.SkipGenerate {
		return skip(c.Name(), "--skip-generate")
	}

	trees := generatedTrees(env.App)
	before, err := hashGenerated(env.App.Root, trees)
	if err != nil {
		return fail(c.Name(), errors.Wrap(err, "hashGenerated()").Error())
	}

	env.notef("running go generate ./... (needs the Spanner emulator)")
	out, removed, err := Generate(ctx, env.Exec, env.App.Root)
	if removed != "" {
		env.notef("%s", RanAgain(removed))
	}
	if err != nil {
		return GenerateFailed(out)
	}

	after, err := hashGenerated(env.App.Root, trees)
	if err != nil {
		return fail(c.Name(), errors.Wrap(err, "hashGenerated()").Error())
	}

	drift := generatedDrift(before, after)
	if len(drift) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d generated file(s) changed when regenerated; commit the regenerated files if the change is intentional", len(drift)), drift...)
	}

	warnings := warningLines(out)
	if len(warnings) > 0 {
		result := pass(c.Name(), fmt.Sprintf("%s; %d schema warning(s)", regenClean, len(warnings)))
		result.Details = warnings

		return result
	}

	return pass(c.Name(), regenClean)
}

// goGenerateStopped matches the go command's own last line when a file it listed is
// gone by the time it reads it: "generate: open <path>: <the system's words>".
var goGenerateStopped = regexp.MustCompile(`(?m)^generate: open (.+\.go): `)

// Generate runs go generate ./... in the application at root, and runs it once more when
// the first run was stopped by a file the run itself removed. The go command lists every
// package's files before it runs the first directive and reads each file when it reaches
// it, so a generated file a generator removed, in a package the go command had not
// reached, ends the run with exit status 1 though the generator finished; the directives
// of the packages after that one did not run. The second run meets no such file and is
// the one whose output and error are answered. removed is the file the first run stopped
// at, relative to root, and empty when one run was all it took.
func Generate(ctx context.Context, exec Execer, root string) (out []byte, removed string, err error) {
	out, err = exec.Run(ctx, root, nil, "go", "generate", "./...")
	if err == nil {
		return out, "", nil
	}
	removed = removedDuringGenerate(out, root)
	if removed == "" {
		return out, "", err
	}
	out, err = exec.Run(ctx, root, nil, "go", "generate", "./...")

	return out, removed, err
}

// removedDuringGenerate reads a failed go generate's output for the go command's stop
// at a Go file under root that is no longer there, and answers the file relative to
// root, or nothing: any other failure is the run's own.
func removedDuringGenerate(out []byte, root string) string {
	m := goGenerateStopped.FindSubmatch(out)
	if m == nil {
		return ""
	}
	rel, err := filepath.Rel(root, string(m[1]))
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	if _, err := os.Stat(filepath.Join(root, rel)); !errors.Is(err, fs.ErrNotExist) {
		return ""
	}

	return filepath.ToSlash(rel)
}

// RanAgain says, for a caller that reports its steps, why go generate ./... ran twice.
func RanAgain(removed string) string {
	return fmt.Sprintf("go generate ./... ran a second time: its first run removed %s, a generated file no longer written, and the go command stops at a file that is gone once it has listed it", removed)
}

// regenClean is the summary of a regeneration that changed nothing.
const regenClean = "regeneration reproduces the generated files on disk"

// GenerateFailed is the regen check's result for a go generate ./... that failed, the
// output's last lines its details, for a caller that ran the generation itself (impulse
// upgrade's step) and hands the failure off as the check would.
func GenerateFailed(out []byte) Result {
	return fail(regen{}.Name(), "go generate ./... failed", outputLines(out, 40)...)
}

// warningLines returns the schema warnings in a generation's output, prefix included, in
// the order the programs printed them.
func warningLines(out []byte) []string {
	var warnings []string
	for line := range strings.Lines(string(out)) {
		if line = strings.TrimRight(line, "\r\n"); strings.HasPrefix(line, warningPrefix) {
			warnings = append(warnings, line)
		}
	}

	return warnings
}

// skippedDirs are trees that never hold generated sources and are expensive to walk.
var skippedDirs = map[string]bool{".git": true, "node_modules": true, ".angular": true, "dist": true, "vendor": true}

// generatedTrees lists the directories a generator owns whole, slash-separated and
// relative to the application's root: every file under one is generated, whatever its
// name. The GenerateIdentity tree is one: the declarations and the methods the generator
// writes there carry plain names, since the same run reads them as its sources.
func generatedTrees(a *app.App) []string {
	var trees []string
	for _, g := range a.Generators {
		if dir := g.IdentityDir(); dir != "" && !slices.Contains(trees, dir) {
			trees = append(trees, dir)
		}
	}

	return trees
}

// hashGenerated maps every generated file under dir, by slash-separated path relative to
// dir, to its content hash: every zz_gen file, and every file under one of the trees.
// The walk stays inside dir (os.Root), so a symlink cannot lead it out of the
// application.
func hashGenerated(dir string, trees []string) (map[string][sha256.Size]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.Wrap(err, "os.OpenRoot()")
	}
	defer root.Close()

	fsys := root.FS()
	sums := make(map[string][sha256.Size]byte)
	err = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrapf(err, "walking %q", path)
		}
		if d.IsDir() {
			if path != "." && skippedDirs[d.Name()] {
				return fs.SkipDir
			}

			return nil
		}
		owned := slices.ContainsFunc(trees, func(tree string) bool { return strings.HasPrefix(path, tree+"/") })
		if !owned && !strings.Contains(d.Name(), "zz_gen") {
			return nil
		}
		raw, err := fs.ReadFile(fsys, path)
		if err != nil {
			return errors.Wrapf(err, "fs.ReadFile(%q)", path)
		}
		sums[path] = sha256.Sum256(raw)

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "fs.WalkDir()")
	}

	return sums, nil
}

// generatedDrift lists the generated files whose content differs between the two
// snapshots: changed, written, or removed by the regeneration. Sorted by path.
func generatedDrift(before, after map[string][sha256.Size]byte) []string {
	var drift []string
	for path, sum := range after {
		if prev, ok := before[path]; !ok {
			drift = append(drift, path+" (written)")
		} else if prev != sum {
			drift = append(drift, path+" (changed)")
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			drift = append(drift, path+" (removed)")
		}
	}
	slices.Sort(drift)

	return drift
}
