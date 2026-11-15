package generation

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/go-playground/errors/v5"
)

// generatedOutput is a run's record of the directories it writes generated files into
// and the files it wrote there, so the sweep at the end of the run removes exactly the
// generated files the run did not write. A phase registers its output directory where
// it used to sweep it (registerOutput), every write goes through writeGeneratedFile,
// and removeStaleOutput runs once after the last phase. No generated file is ever
// missing or partial in between: a file is replaced by an atomic rename, so a package
// compiling against the tree during the run (the drift test under go test ./...) sees
// the previous file or the new one. Safe for the concurrent phases (forEachGo); the
// resource and TypeScript generators share one record through the one client.
type generatedOutput struct {
	mu      sync.Mutex
	targets []outputTarget
	written map[string]struct{}
	// removedGo holds the Go files the run removed because it no longer writes them,
	// for the notice a run started by the go command ends with (removedFilesNotice).
	removedGo []string
}

// outputTarget is a directory a run writes generated files into, with the rule naming
// the files the run owns there.
type outputTarget struct {
	dir    string
	method generatedFileDeleteMethod
}

// registerOutput records dir as a directory the run owns generated files in, by
// method. Registering a directory twice is harmless.
func (o *generatedOutput) registerOutput(dir string, method generatedFileDeleteMethod) {
	o.mu.Lock()
	defer o.mu.Unlock()

	target := outputTarget{dir: filepath.Clean(dir), method: method}
	if !slices.Contains(o.targets, target) {
		o.targets = append(o.targets, target)
	}
}

// writeGeneratedFile writes data to path atomically, creating the directory when it is
// missing, and records the path as written for the stale sweep.
func (o *generatedOutput) writeGeneratedFile(path string, data []byte) error {
	if err := writeFileAtomic(path, data); err != nil {
		return err
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if o.written == nil {
		o.written = make(map[string]struct{})
	}
	o.written[filepath.Clean(path)] = struct{}{}

	return nil
}

// wrote reports whether the run has written path.
func (o *generatedOutput) wrote(path string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	_, ok := o.written[filepath.Clean(path)]

	return ok
}

// removeStaleOutput removes, from every registered directory, the generated files the
// run did not write, and forgets the record so a later run on the same client starts
// clean.
func (o *generatedOutput) removeStaleOutput() error {
	o.mu.Lock()
	targets, written := o.targets, o.written
	o.targets, o.written = nil, nil
	o.mu.Unlock()

	for _, target := range targets {
		removed, err := removeGeneratedFiles(target.dir, target.method, written)
		o.noteRemoved(removed...)
		if err != nil {
			return errors.Wrap(err, "removeGeneratedFiles()")
		}
	}

	return nil
}

// noteRemoved records files the run removed. The Go files among them are kept for the
// run's closing notice.
func (o *generatedOutput) noteRemoved(paths ...string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	for _, path := range paths {
		if strings.HasSuffix(path, ".go") {
			o.removedGo = append(o.removedGo, filepath.Clean(path))
		}
	}
}

// takeRemovedGo answers the Go files the run removed, sorted, and forgets them.
func (o *generatedOutput) takeRemovedGo() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	removed := o.removedGo
	o.removedGo = nil
	slices.Sort(removed)

	return removed
}

// goGenerateFileEnv is the variable the go command sets for a directive it runs: the
// name of the file that carries the directive.
const goGenerateFileEnv = "GOFILE"

// reportRemovedGo ends a run with the notice of the Go files it removed, when the go
// command started the run and it removed any (removedFilesNotice).
func (o *generatedOutput) reportRemovedGo() {
	if notice := removedFilesNotice(o.takeRemovedGo(), os.Getenv(goGenerateFileEnv) != ""); notice != "" {
		log.Print(notice)
	}
}

// removedFilesNotice is what a run started by the go command says when it removed Go
// files it no longer writes, and empty otherwise. The go command lists every package's
// files before it runs the first directive and reads each file when it reaches it, so a
// file a generator removed in a package the go command has not reached yet stops it with
// its own "no such file or directory" and exit status 1, though the generation finished.
// Nothing the generator does can keep the go command from that: the notice names the
// files, says the generation is complete, and says what finishes the command.
func removedFilesNotice(removed []string, startedByGoGenerate bool) string {
	if len(removed) == 0 || !startedByGoGenerate {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Removed %d generated Go file(s) this run no longer writes:\n", len(removed))
	for _, path := range removed {
		fmt.Fprintf(&b, "  %s\n", path)
	}
	b.WriteString("The generation is complete. The go command listed these files before the generator ran, so a go generate that covers their packages stops at the first one it has not read yet (\"no such file or directory\", exit status 1). Run it once more: nothing is left to remove, and it finishes.")

	return b.String()
}

// generatedTempPattern names the temporary file a generated file is written to before
// the rename: a dot file, which the Go tool ignores, carrying nothing of the generated
// name, so a drift test hashing zz_gen files never sees one.
const generatedTempPattern = ".gen-*.tmp"

// generatedFileMode is the mode of a generated file: world-readable, as os.Create
// writes them under the usual umask.
const generatedFileMode = 0o644

// writeFileAtomic writes data to path through a temporary file in the same directory
// and an atomic rename, so a reader never sees a missing or partial file. A failure
// leaves the previous file untouched and no temporary behind.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return errors.Wrap(err, "os.MkdirAll()")
	}

	tmp, err := os.CreateTemp(dir, generatedTempPattern)
	if err != nil {
		return errors.Wrap(err, "os.CreateTemp()")
	}
	tmpName := tmp.Name()
	if err := writeAndClose(tmp, data); err != nil {
		_ = os.Remove(tmpName)

		return errors.Wrapf(err, "writing %s", path)
	}
	if err := os.Chmod(tmpName, generatedFileMode); err != nil {
		_ = os.Remove(tmpName)

		return errors.Wrapf(err, "os.Chmod(): file: %s", path)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)

		return errors.Wrapf(err, "os.Rename(): file: %s", path)
	}

	return nil
}

// writeAndClose writes data to f and closes it, closing it on a write failure too.
func writeAndClose(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		_ = f.Close()

		return errors.Wrap(err, "os.File.Write()")
	}
	if err := f.Close(); err != nil {
		return errors.Wrap(err, "os.File.Close()")
	}

	return nil
}
