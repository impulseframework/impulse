package check

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// secondFactor verifies that no auth package still carries requireSecondFactor, the
// function the auth packages impulse wrote once gave their sign-in rule so that every
// session carried a second factor. Whether everyone is asked for one is now a setting of
// the running application: the auth's sign-in settings table keeps it, an administrator
// changes it on the Sign-in page, and the session library reads it at every sign-in. A
// rule that still names the function asks everyone whatever the setting says, so the
// check names every SetSignInPolicy call whose argument names it and every package-level
// function of that name. A rule of the application's own that returns signin.RequireMFA
// under another name is the application's choice and is not reported. The check rewrites
// nothing.
type secondFactor struct{}

const secondFactorName = "second-factor"

func (secondFactor) Name() string { return secondFactorName }

func (secondFactor) Describe() string {
	return "no auth package still carries requireSecondFactor: whether everyone is asked for a second factor is a setting of the running application, which the session library reads at every sign-in"
}

// SecondFactorMeaning says what the rule means in this framework, for the brief of the
// check.
const SecondFactorMeaning = "Whether everyone is asked for a second factor is a setting an administrator changes on the application's Sign-in page. The auth's sign-in settings table keeps it and the session library reads it at every sign-in. An auth package whose sign-in rule still ends with requireSecondFactor asks everyone for the code whatever the setting says, so the switch on the Sign-in page would do nothing."

func (secondFactor) Meaning() string { return SecondFactorMeaning }

// The identifiers the check reads: the function the auth packages no longer hold and the
// session library's setter of the sign-in rule.
const (
	ruleFunc  = "requireSecondFactor"
	setPolicy = "SetSignInPolicy"
)

func (c secondFactor) Run(_ context.Context, env *Env) Result {
	a := env.App
	if len(a.AuthPackages) == 0 {
		return skip(c.Name(), "no auth package")
	}
	var details, carrying, left []string
	for i := range a.AuthPackages {
		p := &a.AuthPackages[i]
		found, err := leftoverRule(a, p)
		if err != nil {
			return fail(c.Name(), err.Error())
		}
		if len(found) > 0 {
			details = append(details, found...)
			carrying = append(carrying, p.Name)

			continue
		}
		left = append(left, p.Name)
	}
	if len(carrying) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d auth(s) still carry requireSecondFactor", len(carrying)), details...)
	}

	return pass(c.Name(), fmt.Sprintf("%d auth(s) leave the second factor to the sign-in setting: %s", len(left), strings.Join(left, ", ")))
}

// leftoverRule reads the package's non-test files for what still carries
// requireSecondFactor: each SetSignInPolicy call whose argument names it anywhere, and each
// package-level function of that name, as findings in file then line order.
func leftoverRule(a *app.App, p *app.AuthPackage) ([]string, error) {
	entries, err := os.ReadDir(a.Abs(p.Dir))
	if err != nil {
		return nil, errors.Wrap(err, "os.ReadDir()")
	}
	var findings []string
	for _, e := range entries {
		if e.IsDir() || !app.GoToolReads(e.Name()) || isTestFile(e.Name()) {
			continue
		}
		rel := path.Join(p.Dir, e.Name())
		src, err := os.ReadFile(a.Abs(rel))
		if err != nil {
			return nil, errors.Wrap(err, "os.ReadFile()")
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, errors.Wrap(err, "parser.ParseFile()")
		}
		findings = append(findings, fileFindings(fset, f, rel, p.Name)...)
	}

	return findings, nil
}

// fileFindings lists one file's findings in line order.
func fileFindings(fset *token.FileSet, f *ast.File, rel, auth string) []string {
	type finding struct {
		line int
		text string
	}
	var found []finding
	ast.Inspect(f, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != setPolicy || len(ce.Args) != 1 || !namesRuleFunc(ce.Args[0]) {
			return true
		}
		line := fset.Position(ce.Pos()).Line
		found = append(found, finding{line, fmt.Sprintf("%s:%d: the %s auth's sign-in rule still names requireSecondFactor, so everyone is asked for the code whatever the Sign-in page's setting says; take it off the rule (remove .Then(requireSecondFactor), or the whole SetSignInPolicy call where it is the only rule): the session library asks for the second factor by the auth's sign-in setting", rel, line, auth)})

		return true
	})
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name.Name != ruleFunc {
			continue
		}
		line := fset.Position(fd.Pos()).Line
		found = append(found, finding{line, fmt.Sprintf("%s:%d: requireSecondFactor is no longer the second factor's rule; remove it: whether everyone is asked is the auth's sign-in setting, changed on the Sign-in page", rel, line)})
	}
	slices.SortStableFunc(found, func(x, y finding) int { return x.line - y.line })
	texts := make([]string, 0, len(found))
	for _, fd := range found {
		texts = append(texts, fd.text)
	}

	return texts
}

// namesRuleFunc reports whether the expression names the identifier requireSecondFactor
// anywhere in it: alone, chained with .Then, or deeper.
func namesRuleFunc(e ast.Expr) bool {
	names := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == ruleFunc {
			names = true
		}

		return !names
	})

	return names
}
