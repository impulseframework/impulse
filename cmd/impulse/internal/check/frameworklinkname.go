package check

import (
	"bytes"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/framework"
)

// frameworkLinkname verifies that no Go file in the application's tree names a package
// of the framework in a //go:linkname directive. What the framework keeps to itself it
// keeps by the type checker: a method with an unexported name, which no other package
// can call or declare, in a package under an internal directory, which no package
// outside the framework can import (the write of an impersonated session and the reads
// that decide one, session/internal/sealed). A //go:linkname directive, in a file that
// imports unsafe, names a function or a variable of another package by its symbol and
// reaches it whatever the type checker refuses.
//
// The check reads every Go file the Go tool reads (app.GoToolReads) under the
// application's root, test files and generated files included, in every directory but
// version control's own: a vendor directory, a browser side's dependencies, and the
// directories a ./... pattern passes over (testdata, a name that begins with "_" or
// "."), since a package there is still built when another imports it by its path. A
// file whose own name begins with "_" or "." is in no package's build and is not read.
//
// A directive is refused when the symbol it links to is of a package under the
// framework's import path or of the framework's root package; the directive with one
// argument, which names no other package, is not one. The compiler reads the directive
// from a line comment wherever it stands, so the comments are what the check reads.
//
// What the check reads is the application's own tree. The code of a module the
// application requires is not read here unless it is vendored, and neither is a program
// that reaches the same memory with unsafe alone.
type frameworkLinkname struct{}

// frameworkLinknameName is the check's name.
const frameworkLinknameName = "framework-linkname"

// linknameDirective opens the directive in a line comment.
const linknameDirective = "//go:linkname"

func (frameworkLinkname) Name() string {
	return frameworkLinknameName
}

func (frameworkLinkname) Describe() string {
	return "no Go file in the application's tree links to a symbol of the framework by name (//go:linkname)"
}

func (frameworkLinkname) Meaning() string {
	return "The framework holds some writes to its own packages by the type checker, the write of an impersonated session and the reads that decide one among them: no package of an application can call them, so the declaration, the grant and the permission mask the session API checks cannot be gone around through the framework's API. " +
		"A //go:linkname directive reaches a function or a variable by its symbol's name, past the type checker, so one that names a package of the framework is refused wherever it stands in the application's tree, a test file and a vendored file included. " +
		"Remove each directive the check names, with the declaration under it, and reach the framework through what its packages export. " +
		"If the application needs something a package of the framework keeps to itself, that is a change to ask of the framework."
}

func (c frameworkLinkname) Run(_ context.Context, env *Env) Result {
	a := env.App
	files, err := c.goFiles(a.Root)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	var details []string
	for _, rel := range files {
		src, err := os.ReadFile(a.Abs(rel))
		if err != nil {
			return fail(c.Name(), errors.Wrap(err, "os.ReadFile()").Error())
		}
		for _, link := range linknames(src) {
			if !namesFramework(link.symbol) {
				continue
			}
			details = append(details, fmt.Sprintf(
				"%s:%d: %s %s %s links to a symbol of %s, a package of the framework, by its name: the framework is reached through what its packages export. Remove the directive and the declaration under it",
				rel, link.line, linknameDirective, link.local, link.symbol, symbolPackage(link.symbol),
			))
		}
	}
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d go:linkname directive(s) name a package of the framework", len(details)), details...)
	}

	return pass(c.Name(), fmt.Sprintf("%d Go file(s); no go:linkname directive names a package of the framework", len(files)))
}

// goFiles lists every Go file the Go tool reads under the root, root-relative with
// forward slashes, entering every directory but version control's.
func (frameworkLinkname) goFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == gitDir && p != root {
				return filepath.SkipDir
			}

			return nil
		}
		if !app.GoToolReads(d.Name()) {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 && !linksToAFile(p) {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return errors.Wrap(err, "filepath.Rel()")
		}
		files = append(files, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "filepath.WalkDir()")
	}

	return files, nil
}

// linksToAFile reports whether the link at p leads to a file. The Go tool reads a link
// to a file as the file, and takes a link to a directory, or one that leads nowhere,
// for no source file.
func linksToAFile(p string) bool {
	info, err := os.Stat(p)

	return err == nil && !info.IsDir()
}

// linkname is one //go:linkname directive that names a symbol: the line it is on, the
// local name it gives and the symbol it links that name to.
type linkname struct {
	line   int
	local  string
	symbol string
}

// linknames returns the //go:linkname directives of a Go file that name a symbol, the
// directive's two-argument form. The file is parsed for its comments, since a line that
// reads as a directive inside a string or a block comment is none. A file that does not
// parse is read line by line instead: what the parser stopped at is the compiler's to
// refuse, and a directive in such a file is reported all the same.
func linknames(src []byte) []linkname {
	if !bytes.Contains(src, []byte(linknameDirective)) {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return linknamesByLine(src)
	}
	var found []linkname
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if link, ok := parseLinkname(comment.Text, fset.Position(comment.Slash).Line); ok {
				found = append(found, link)
			}
		}
	}

	return found
}

// linknamesByLine reads the directives of a file that does not parse: each line's text
// from the directive's opening on.
func linknamesByLine(src []byte) []linkname {
	var found []linkname
	for i, line := range strings.Split(string(src), "\n") {
		at := strings.Index(line, linknameDirective)
		if at < 0 {
			continue
		}
		if link, ok := parseLinkname(line[at:], i+1); ok {
			found = append(found, link)
		}
	}

	return found
}

// parseLinkname reads one line comment as a //go:linkname directive that names a
// symbol: the directive, a local name and the symbol, separated by white space. The
// one-argument form names no symbol and is not returned.
func parseLinkname(comment string, line int) (linkname, bool) {
	rest, ok := strings.CutPrefix(comment, linknameDirective)
	if !ok || rest == "" || !strings.ContainsAny(rest[:1], " \t") {
		return linkname{}, false
	}
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return linkname{}, false
	}

	return linkname{line: line, local: fields[0], symbol: fields[1]}, true
}

// namesFramework reports whether a symbol is of a package of the framework: of the
// framework's root package, or of a package under the framework's import path. A
// symbol's name opens with its package's import path as the directive writes it.
func namesFramework(symbol string) bool {
	return strings.HasPrefix(symbol, FrameworkModule+".") || framework.Under(symbol)
}

// symbolPackage is the import path a symbol's name opens with: everything before the
// first dot after the last slash.
func symbolPackage(symbol string) string {
	slash := strings.LastIndex(symbol, "/")
	if dot := strings.Index(symbol[slash+1:], "."); dot >= 0 {
		return symbol[:slash+1+dot]
	}

	return symbol
}
