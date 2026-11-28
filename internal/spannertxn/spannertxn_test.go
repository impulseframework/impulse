package spannertxn

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/internal/apptree"
)

// The functions of the module that open a Spanner read-write transaction themselves, each
// with why it may: every other one must call Serializable.
var opensItselfAllowed = map[string]string{
	"internal/spannertxn/spannertxn.go: Serializable":   "the one opener, which names the level",
	"internal/spannertxn/spannertxn.go: RepeatableRead": "the opener of a delete's save, which names the level",
	"spxscan/spxapi/api.go: UpdateAndGet":               "the caller's statement on the caller's runner, at the caller's level",
	"spxscan/spxapi/api.go: UpdateAndSelect":            "the caller's statement on the caller's runner, at the caller's level",
}

// The words a file must hold for its parse to be worth the time: every call
// opensReadWriteTransaction finds spells one of them.
var openerWords = [][]byte{[]byte("ReadWriteTransaction"), []byte("ReadWriteStmtBasedTransaction")}

// opening is one call that opens a Spanner read-write transaction: the declared function
// it is in, empty for a call outside any function, and where the call is.
type opening struct {
	function string
	pos      token.Pos
}

// opensReadWriteTransaction lists the calls of a parsed file that open a Spanner
// read-write transaction, each with the declared function it is in: a method by its
// method name, a call inside a function literal under the function around it, and a call
// outside any function with no name. A call opens one when its function is a selector
// named ReadWriteTransaction with two arguments, ReadWriteTransactionWithOptions with
// three, or a name that starts with NewReadWriteStmtBasedTransaction. It goes by the name
// and the argument count alone, whatever the receiver, so a call through an interface or a
// field is found, and a conversion to a type of that name (one argument) is not.
func opensReadWriteTransaction(f *ast.File) []opening {
	var found []opening
	for _, decl := range f.Decls {
		var function string
		if fn, ok := decl.(*ast.FuncDecl); ok {
			function = fn.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			if (name == "ReadWriteTransaction" && len(call.Args) == 2) ||
				(name == "ReadWriteTransactionWithOptions" && len(call.Args) == 3) ||
				strings.HasPrefix(name, "NewReadWriteStmtBasedTransaction") {
				found = append(found, opening{function: function, pos: call.Pos()})
			}

			return true
		})
	}

	return found
}

// TestOpensAReadWriteTransaction pins what the reading below looks for, with the calls it
// must find and the ones it must leave.
func TestOpensAReadWriteTransaction(t *testing.T) {
	t.Parallel()

	const src = `package x

import (
	"context"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"

	"example.com/resource"
	"example.com/spannertxn"
)

type store struct{ db *spanner.Client }

var _, opened = client.ReadWriteTransaction(context.Background(), f)

func byClient(ctx context.Context, client *spanner.Client, f func(context.Context, *spanner.ReadWriteTransaction) error) {
	_, _ = client.ReadWriteTransaction(ctx, f)
}

func withOptions(ctx context.Context, client *spanner.Client, f func(context.Context, *spanner.ReadWriteTransaction) error, options spanner.TransactionOptions) {
	_, _ = client.ReadWriteTransactionWithOptions(ctx, f, options)
}

func (s *store) throughField(ctx context.Context, f func(context.Context, *spanner.ReadWriteTransaction) error) {
	_, _ = s.db.ReadWriteTransaction(ctx, f)
}

func stmtBased(ctx context.Context, client *spanner.Client) {
	_, _ = spanner.NewReadWriteStmtBasedTransaction(ctx, client)
}

func stmtBasedWithOptions(ctx context.Context, client *spanner.Client, options spanner.TransactionOptions) {
	_, _ = spanner.NewReadWriteStmtBasedTransactionWithOptions(ctx, client, options)
}

func inLiteral(ctx context.Context, client *spanner.Client, f func(context.Context, *spanner.ReadWriteTransaction) error) {
	run := func() {
		_, _ = client.ReadWriteTransaction(ctx, f)
	}
	run()
}

func apply(ctx context.Context, client *spanner.Client, ms []*spanner.Mutation) {
	_, _ = client.Apply(ctx, ms)
}

func reads(client *spanner.Client) {
	client.Single()
	client.ReadOnlyTransaction()
}

func buffer(txn *spanner.ReadWriteTransaction, ms []*spanner.Mutation) error {
	return txn.BufferWrite(ms)
}

func serializable(ctx context.Context, client *spanner.Client, f func(context.Context, *spanner.ReadWriteTransaction) error) {
	_, _ = spannertxn.Serializable(ctx, client, f)
}

func words(err error) error {
	return errors.Wrap(err, "spanner.Client.ReadWriteTransaction()")
}

func execute(ctx context.Context, c *resource.SpannerClient, f func(context.Context, resource.ReadWriteTransaction) error) error {
	return c.ExecuteFunc(ctx, f)
}

func conversion(txn *spanner.ReadWriteTransaction) resource.ReadWriteTransaction {
	return resource.ReadWriteTransaction(txn)
}
`
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range opensReadWriteTransaction(f) {
		got = append(got, o.function)
	}
	want := []string{"", "byClient", "withOptions", "throughField", "stmtBased", "stmtBasedWithOptions", "inLiteral"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("opensReadWriteTransaction() mismatch (-want +got):\n%s", diff)
	}
}

// TestEveryReadWriteTransactionNamesItsLevel holds every hand-written file of the module
// to the one opener: a read-write transaction opened on the client itself runs at the
// level the application set for the client, where a rule that reads before it writes no
// longer holds. Tests are not read; they open what they like.
func TestEveryReadWriteTransactionNamesItsLevel(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if !apptree.HoldsModule(root) {
		t.Fatalf("%s holds no go.mod: the walk starts at the module's root", root)
	}
	tree, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	var findings []string
	allowed := map[string]bool{}
	err = fs.WalkDir(tree.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "fs.WalkDir()")
		}
		name := d.Name()
		if d.IsDir() {
			if rel != "." && (apptree.SkipsDir(name) || apptree.HoldsModule(filepath.Join(root, filepath.FromSlash(rel)))) {
				return fs.SkipDir
			}

			return nil
		}
		if !apptree.GoToolReads(name) || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		data, err := fs.ReadFile(tree.FS(), rel)
		if err != nil {
			return errors.Wrap(err, "fs.ReadFile()")
		}
		if !slices.ContainsFunc(openerWords, func(word []byte) bool { return bytes.Contains(data, word) }) {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, data, parser.SkipObjectResolution)
		if err != nil {
			return errors.Wrap(err, "parser.ParseFile()")
		}
		for _, o := range opensReadWriteTransaction(f) {
			function := o.function
			if function != "" {
				key := rel + ": " + function
				if _, ok := opensItselfAllowed[key]; ok {
					allowed[key] = true

					continue
				}
			} else {
				function = "a declaration outside any function"
			}
			findings = append(findings, fmt.Sprintf("%s:%d: %s opens a Spanner read-write transaction itself, so it runs at whatever isolation level the client is set to; run it with spannertxn.Serializable (internal/spannertxn), which names the level",
				rel, fset.Position(o.pos).Line, function))
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(findings)
	if len(findings) > 0 {
		t.Errorf("%d call(s) open a Spanner read-write transaction themselves:\n%s", len(findings), strings.Join(findings, "\n"))
	}
	for key, why := range opensItselfAllowed {
		if !allowed[key] {
			t.Errorf("%s is listed as opening a Spanner read-write transaction itself (%s) and no longer does: take it off the list", key, why)
		}
	}
}
