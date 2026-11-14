package transition

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
)

// withoutPackages is the hand-written Go source of one package as it reads once it no
// longer names the packages imported from under a module path prefix: what a copy of the
// package is when the generated code those imports name is not copied with it.
type withoutPackages struct {
	// Files are the package's files by name, edited. A file every declaration of which
	// named the packages is not among them.
	Files map[string]string
	// Dropped names what was removed, sorted: the methods, functions and types.
	Dropped []string
	// Left are the lines that still call a removed method, as file:line: name. The
	// package does not compile until each is rewritten, which is the agent's.
	Left []string
}

// span is a run of whole lines of a file's text, as byte offsets.
type span struct {
	start, end int
}

// sourceFile is one file of the package while it is edited.
type sourceFile struct {
	name string
	text string
	ast  *ast.File
	pos  *token.File
	// pkgs are the local names of the file's imports from under the prefix.
	pkgs  map[string]bool
	spans []span
}

// dropPackages removes from the files of one package every declaration that names a
// package imported from under prefix (an import path that is the prefix or lies below
// it), and what the removal leaves without a use:
//
//   - a function or method whose signature or body names one, whole;
//   - a method of an interface and a field of a struct whose type names one, and the
//     element that sets such a field in a composite literal of the struct;
//   - an unexported type that was used by the removed declarations alone, with its
//     methods;
//   - the imports the removed declarations alone used.
//
// A call of a removed method that stands in a declaration that was kept is not rewritten:
// it is reported, and the package does not compile until someone decides what the caller
// does instead. Files that import nothing from under the prefix are returned as they are.
func dropPackages(files map[string]string, prefix string) (*withoutPackages, error) {
	fset := token.NewFileSet()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var parsed []*sourceFile
	found := false
	for _, name := range names {
		f, err := parser.ParseFile(fset, name, files[name], parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, errors.Wrapf(err, "parser.ParseFile(): %s", name)
		}
		s := &sourceFile{name: name, text: files[name], ast: f, pos: fset.File(f.Pos()), pkgs: map[string]bool{}}
		for _, spec := range f.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil || (importPath != prefix && !strings.HasPrefix(importPath, prefix+"/")) {
				continue
			}
			s.pkgs[importName(spec, importPath)] = true
			s.cut(spec.Pos(), spec.End())
			found = true
		}
		parsed = append(parsed, s)
	}
	out := &withoutPackages{Files: map[string]string{}}
	if !found {
		for name, text := range files {
			out.Files[name] = text
		}

		return out, nil
	}

	d := &packageDrop{files: parsed, fields: map[string]map[string]bool{}, methods: map[string]bool{}, dropped: map[string]bool{}}
	d.direct()
	d.literals()
	d.orphanTypes()
	d.imports()

	for _, s := range parsed {
		if len(s.spans) == 0 {
			out.Files[s.name] = s.text

			continue
		}
		edited := s.edited()
		kept, err := declares(s.name, edited)
		if err != nil {
			return nil, err
		}
		if kept {
			out.Files[s.name] = edited
		}
	}
	for name := range d.dropped {
		out.Dropped = append(out.Dropped, name)
	}
	sort.Strings(out.Dropped)
	out.Left = d.left()

	return out, nil
}

// packageDrop is the state of one dropPackages run.
type packageDrop struct {
	files []*sourceFile
	// fields are the removed struct fields, by struct type name.
	fields map[string]map[string]bool
	// methods are the removed methods and functions, by name.
	methods map[string]bool
	// dropped are the names reported: methods, functions and types.
	dropped map[string]bool
}

// direct removes the declarations that name the packages themselves.
func (d *packageDrop) direct() {
	for _, s := range d.files {
		if len(s.pkgs) == 0 {
			continue
		}
		for _, decl := range s.ast.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if s.names(decl) {
					s.cutDecl(decl.Doc, decl)
					d.methods[decl.Name.Name] = true
					d.dropped[decl.Name.Name] = true
				}
			case *ast.GenDecl:
				d.directGen(s, decl)
			}
		}
	}
}

// directGen removes what a type, variable or constant declaration names of the packages:
// the interface methods and struct fields typed by them, and any other specification
// whole.
func (d *packageDrop) directGen(s *sourceFile, decl *ast.GenDecl) {
	if decl.Tok == token.IMPORT {
		return
	}
	for _, spec := range decl.Specs {
		var doc *ast.CommentGroup
		var name string
		switch spec := spec.(type) {
		case *ast.TypeSpec:
			doc, name = spec.Doc, spec.Name.Name
			switch t := spec.Type.(type) {
			case *ast.InterfaceType:
				for _, method := range t.Methods.List {
					if !s.names(method) {
						continue
					}
					s.cutDecl(method.Doc, method)
					for _, ident := range method.Names {
						d.methods[ident.Name] = true
						d.dropped[ident.Name] = true
					}
				}

				continue
			case *ast.StructType:
				for _, field := range t.Fields.List {
					if !s.names(field) {
						continue
					}
					s.cutDecl(field.Doc, field)
					if d.fields[name] == nil {
						d.fields[name] = map[string]bool{}
					}
					for _, ident := range field.Names {
						d.fields[name][ident.Name] = true
					}
				}

				continue
			}
		case *ast.ValueSpec:
			doc = spec.Doc
			if len(spec.Names) > 0 {
				name = spec.Names[0].Name
			}
		}
		if !s.names(spec) {
			continue
		}
		d.dropped[name] = true
		if len(decl.Specs) == 1 {
			s.cutDecl(decl.Doc, decl)
		} else {
			s.cutDecl(doc, spec)
		}
	}
}

// literals removes the elements that set a removed field in a composite literal of its
// struct.
func (d *packageDrop) literals() {
	if len(d.fields) == 0 {
		return
	}
	for _, s := range d.files {
		ast.Inspect(s.ast, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			typ, ok := lit.Type.(*ast.Ident)
			if !ok || d.fields[typ.Name] == nil {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && d.fields[typ.Name][key.Name] {
					s.cutDecl(s.commentAbove(kv.Pos()), kv)
				}
			}

			return true
		})
	}
}

// declaredType is an unexported type the package declares, with its methods.
type declaredType struct {
	file    *sourceFile
	decl    *ast.GenDecl
	spec    *ast.TypeSpec
	methods []*ast.FuncDecl
}

// orphanTypes removes the unexported types the removed declarations used and nothing
// that was kept uses, with their methods, until no removal leaves another.
func (d *packageDrop) orphanTypes() {
	types := d.unexportedTypes()
	for changed := true; changed; {
		changed = false
		for name, t := range types {
			if t.file.removed(t.spec.Pos()) || !d.orphaned(name, t) {
				continue
			}
			t.file.cutDecl(t.decl.Doc, t.decl)
			for _, fn := range t.methods {
				// A method lives in any file of the package.
				if s := d.fileOf(fn); s != nil {
					s.cutDecl(fn.Doc, fn)
				}
			}
			d.dropped[name] = true
			changed = true
		}
	}
}

// unexportedTypes lists the unexported types the package declares one to a declaration,
// by name, each with the methods declared on it in any file.
func (d *packageDrop) unexportedTypes() map[string]*declaredType {
	types := map[string]*declaredType{}
	for _, s := range d.files {
		for _, decl := range s.ast.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE || len(gen.Specs) != 1 {
				continue
			}
			if spec, ok := gen.Specs[0].(*ast.TypeSpec); ok && !spec.Name.IsExported() {
				types[spec.Name.Name] = &declaredType{file: s, decl: gen, spec: spec}
			}
		}
	}
	for _, s := range d.files {
		for _, decl := range s.ast.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if t := types[receiverType(fn)]; t != nil {
					t.methods = append(t.methods, fn)
				}
			}
		}
	}

	return types
}

// orphaned reports whether a type was used by the removed declarations and is used by
// nothing that was kept. The type's own methods are not a use of it.
func (d *packageDrop) orphaned(name string, t *declaredType) bool {
	inRemoved, inKept := false, false
	for _, s := range d.files {
		ast.Inspect(s.ast, func(n ast.Node) bool {
			if fn, ok := n.(*ast.FuncDecl); ok && receiverType(fn) == name {
				return false
			}
			ident, ok := n.(*ast.Ident)
			if !ok || ident.Name != name || ident == t.spec.Name {
				return true
			}
			if s.removed(ident.Pos()) {
				inRemoved = true
			} else {
				inKept = true
			}

			return true
		})
	}

	return inRemoved && !inKept
}

// fileOf returns the file of the package a declaration stands in.
func (d *packageDrop) fileOf(n ast.Node) *sourceFile {
	for _, s := range d.files {
		if s.pos.Base() <= int(n.Pos()) && int(n.Pos()) <= s.pos.Base()+s.pos.Size() {
			return s
		}
	}

	return nil
}

// imports removes the imports the removed declarations used and nothing kept uses. An
// import whose package name the file does not spell is told by its path: the last
// element, or the one before a major version.
func (d *packageDrop) imports() {
	for _, s := range d.files {
		if len(s.spans) == 0 {
			continue
		}
		for _, spec := range s.ast.Imports {
			if s.removed(spec.Pos()) {
				continue
			}
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			name := importName(spec, importPath)
			if name == "_" || name == "." {
				continue
			}
			inRemoved, inKept := false, false
			ast.Inspect(s.ast, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == name {
					if s.removed(x.Pos()) {
						inRemoved = true
					} else {
						inKept = true
					}
				}

				return true
			})
			if inRemoved && !inKept {
				s.cut(spec.Pos(), spec.End())
			}
		}
	}
}

// left lists the calls of a removed method that stand in what was kept.
func (d *packageDrop) left() []string {
	var left []string
	for _, s := range d.files {
		ast.Inspect(s.ast, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !d.methods[sel.Sel.Name] || s.removed(sel.Pos()) {
				return true
			}
			left = append(left, s.name+":"+strconv.Itoa(s.pos.Line(sel.Pos()))+": "+sel.Sel.Name)

			return true
		})
	}

	return left
}

// majorVersionRE is a path element that is a major version, not a package name.
var majorVersionRE = regexp.MustCompile(`^v\d+$`)

// importName is the name a file refers to an import by: the one the import spells, or
// the one its path tells.
func importName(spec *ast.ImportSpec, importPath string) string {
	if spec.Name != nil {
		return spec.Name.Name
	}
	name := path.Base(importPath)
	if majorVersionRE.MatchString(name) {
		name = path.Base(path.Dir(importPath))
	}

	return name
}

// receiverType is the name of the type a method is declared on, or empty for a function.
func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	t := fn.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if ident, ok := t.(*ast.Ident); ok {
		return ident.Name
	}

	return ""
}

// names reports whether the node names one of the file's packages from under the prefix.
func (s *sourceFile) names(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && s.pkgs[x.Name] {
				found = true
			}
		}

		return !found
	})

	return found
}

// cutDecl marks a node for removal with the comment that documents it.
func (s *sourceFile) cutDecl(doc *ast.CommentGroup, n ast.Node) {
	from := n.Pos()
	if doc != nil {
		from = doc.Pos()
	}
	s.cut(from, n.End())
}

// cut marks the whole lines from the one pos stands on through the one end stands on.
func (s *sourceFile) cut(pos, end token.Pos) {
	start := s.pos.Offset(s.pos.LineStart(s.pos.Line(pos)))
	stop := len(s.text)
	if line := s.pos.Line(end); line < s.pos.LineCount() {
		stop = s.pos.Offset(s.pos.LineStart(line + 1))
	}
	s.spans = append(s.spans, span{start: start, end: stop})
}

// removed reports whether the position stands in a line marked for removal.
func (s *sourceFile) removed(pos token.Pos) bool {
	if int(pos) < s.pos.Base() || int(pos) > s.pos.Base()+s.pos.Size() {
		return false
	}
	offset := s.pos.Offset(pos)
	for _, sp := range s.spans {
		if sp.start <= offset && offset < sp.end {
			return true
		}
	}

	return false
}

// commentAbove returns the comment that ends on the line before pos, which documents
// what pos starts where the syntax tree attaches it to nothing (an element of a literal).
func (s *sourceFile) commentAbove(pos token.Pos) *ast.CommentGroup {
	line := s.pos.Line(pos)
	for _, group := range s.ast.Comments {
		if s.pos.Line(group.End()) == line-1 && strings.TrimSpace(s.text[s.pos.Offset(s.pos.LineStart(s.pos.Line(group.Pos()))):s.pos.Offset(group.Pos())]) == "" {
			return group
		}
	}

	return nil
}

// edited returns the file's text without the lines marked for removal.
func (s *sourceFile) edited() string {
	sort.Slice(s.spans, func(i, j int) bool {
		return s.spans[i].start < s.spans[j].start
	})
	var b strings.Builder
	at := 0
	for _, sp := range s.spans {
		if sp.end <= at {
			continue
		}
		if sp.start > at {
			b.WriteString(s.text[at:sp.start])
		}
		at = sp.end
	}
	b.WriteString(s.text[at:])

	return b.String()
}

// declares reports whether a file's text still declares anything beside its imports.
func declares(name, text string) (bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, text, parser.SkipObjectResolution)
	if err != nil {
		return false, errors.Wrapf(err, "parser.ParseFile(): %s without the removed declarations", name)
	}
	for _, decl := range f.Decls {
		if gen, ok := decl.(*ast.GenDecl); !ok || gen.Tok != token.IMPORT {
			return true, nil
		}
	}

	return false, nil
}
