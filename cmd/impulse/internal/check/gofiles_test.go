package check

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

// The names a scratch file is kept under beside a package's real files: the Go tool
// passes over both, so no build compiles either.
var scratchNames = []string{"_scratch.go", ".draft.go"}

// TestChecksReadTheGoFilesTheGoToolReads holds the checks that read one directory's Go
// files for themselves to the Go tool's rule about a file's name: a scratch file kept
// beside a package's real ones is no build's, so what it holds neither satisfies a check
// nor fails one. Each case is an application its check answers one way, with a scratch
// file beside the real files whose content would turn the answer.
func TestChecksReadTheGoFilesTheGoToolReads(t *testing.T) {
	t.Parallel()

	// gofmt aligns a struct's field types in a column; the unformatted form has them unaligned.
	const (
		formatted   = "package x\n\ntype S struct {\n\tA    int\n\tLong string\n}\n"
		unformatted = "package x\n\ntype S struct {\n\tA int\n\tLong string\n}\n"
		staffDir    = "pkg/auth/staff"
		staffFile   = staffDir + "/staff.go"
	)
	with := func(files map[string]string, rel, content string) map[string]string {
		files = maps.Clone(files)
		files[rel] = content

		return files
	}
	admittedStaff := admitted()[staffFile]

	tests := []struct {
		name  string
		check Check
		// files are the application, root-relative.
		files map[string]string
		// scratch is written under each scratch name in dir.
		dir     string
		scratch string
		want    Result
	}{
		{
			name:  "auths-wired: a Roles() in a scratch file is not the package's",
			check: authsWired{},
			files: map[string]string{
				staffFile:                   staffAuthNoRoles,
				"pkg/auth/staff/roles.json": rolesJSON,
				"pkg/config/data.go":        staffConfig,
				"pkg/deploy/deploy.go":      staffDeploy,
				"cmd/bootstrap/main.go":     staffBootstrap,
				"app/app.go":                staffApp,
				"pkg/deploy/deploy_test.go": rolesValidation,
			},
			dir:     staffDir,
			scratch: staffAuth,
			want: Result{
				Name: authsWiredName, Status: Fail, Summary: "1 auth wiring problem(s)",
				Details: []string{"pkg/auth/staff: the package exports no Roles(); the staff auth's role file reaches the permission engine as staff.Roles() handed to access.WithDefaultRoles"},
			},
		},
		{
			name:    "directory-admission: a call in a scratch file sets no admission",
			check:   directoryAdmission{},
			files:   with(admitted(), staffFile, strings.Replace(admittedStaff, admissionCall, "", 1)),
			dir:     staffDir,
			scratch: admittedStaff,
			want: Result{
				Name: directoryAdmission{}.Name(), Status: Fail, Summary: "1 directory admission wiring problem(s)",
				Details: []string{"pkg/auth/staff: " + fmt.Sprintf(noAdmission, "staff")},
			},
		},
		{
			name:    "second-factor: a policy call in a scratch file is no second call on the Auth",
			check:   secondFactor{},
			files:   map[string]string{staffFile: fmt.Sprintf(ruledAuth, "\ts.SetSignInPolicy(requireSecondFactor)", secondFactorFunc)},
			dir:     staffDir,
			scratch: fmt.Sprintf(ruledAuth, "\ts.SetSignInPolicy(signin.TenantPolicy(nil, nil))", ""),
			want:    Result{Name: secondFactorName, Status: Pass, Summary: "1 auth(s) end their sign-in rule with the second factor: staff"},
		},
		{
			name:    "gofmt: a scratch file is not held to the formatting",
			check:   gofmtCheck{},
			files:   map[string]string{"main.go": formatted, "pkg/x/x.go": formatted},
			dir:     "pkg/x",
			scratch: unformatted,
			want:    Result{Name: gofmtCheck{}.Name(), Status: Pass, Summary: "2 Go file(s) gofmt-formatted"},
		},
		{
			name:    "first-administrator: a seed in a scratch file seeds nobody",
			check:   firstAdministrator{},
			files:   with(admitted(), "pkg/auth/staff/foothold.go", invitingFoothold),
			dir:     staffDir,
			scratch: seedingFoothold,
			want:    Result{Name: firstAdministratorName, Status: Pass, Summary: "the first administrator is invited: staff"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := maps.Clone(tt.files)
			for _, name := range scratchNames {
				files[path.Join(tt.dir, name)] = tt.scratch
			}
			got := tt.check.Run(context.Background(), &Env{App: discoverFiles(t, files)})
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Run() with a scratch file beside the real ones mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestGofmtFixLeavesAScratchFileAlone pins that --fix rewrites no file the Go tool passes
// over: a scratch file is the author's to keep as it is.
func TestGofmtFixLeavesAScratchFileAlone(t *testing.T) {
	t.Parallel()

	const (
		formatted   = "package x\n\ntype S struct {\n\tA    int\n\tLong string\n}\n"
		unformatted = "package x\n\ntype S struct {\n\tA int\n\tLong string\n}\n"
	)
	files := map[string]string{"main.go": unformatted}
	for _, name := range scratchNames {
		files[path.Join("pkg/x", name)] = unformatted
	}
	a := discoverFiles(t, files)
	got := gofmtCheck{}.Run(context.Background(), &Env{App: a, Fix: true})
	want := Result{Name: gofmtCheck{}.Name(), Status: Pass, Summary: "1 Go file(s) formatted; 1 checked", Details: []string{"main.go"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Run() mismatch (-want +got):\n%s", diff)
	}
	for rel, wantContent := range map[string]string{"main.go": formatted, "pkg/x/_scratch.go": unformatted, "pkg/x/.draft.go": unformatted} {
		data, err := os.ReadFile(a.Abs(rel))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != wantContent {
			t.Errorf("%s after --fix = %q, want %q", rel, data, wantContent)
		}
	}
}

// The functions of the scan and the checks that test a name for the ".go" suffix without
// asking app.GoToolReads, each with why it may: every other one must ask.
var suffixTestsWithoutTheRule = map[string]string{
	"internal/apptree/apptree.go: GoToolReads":          "the rule itself, which app.GoToolReads asks",
	"app/generate.go: directiveTarget":                  "a go run directive's target that names a file: a word of a command, and no file is read by it",
	"cmd/impulse/internal/check/sites.go: goRunTargets": "a go run command's target that names a file: a word of a command, and no file is read by it",
}

// choosesGoFilesByItself lists the functions of a parsed file that decide a Go file by
// its name for themselves: one that tests a name for the ".go" suffix
// (strings.HasSuffix(name, ".go")) or names the pattern "*.go", and nowhere asks
// GoToolReads. A test for "_test.go" is another question and is not one of them, and
// neither is a pattern that can match no scratch file ("zz_gen_*.go").
func choosesGoFilesByItself(f *ast.File) []*ast.FuncDecl {
	var found []*ast.FuncDecl
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		tests, asks := false, false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.BasicLit:
				if text, err := strconv.Unquote(node.Value); node.Kind == token.STRING && err == nil && (text == ".go" || text == "*.go") {
					tests = true
				}
			case *ast.Ident:
				if node.Name == "GoToolReads" {
					asks = true
				}
			}

			return true
		})
		if tests && !asks {
			found = append(found, fn)
		}
	}

	return found
}

// TestChoosesGoFilesByItself pins what the reading below looks for, with a case it must
// find and the cases it must leave.
func TestChoosesGoFilesByItself(t *testing.T) {
	t.Parallel()

	const src = `package x

import (
	"path/filepath"
	"strings"

	"example.com/app"
)

func bySuffix(name string) bool { return strings.HasSuffix(name, ".go") }

func byPattern(dir string) ([]string, error) { return filepath.Glob(filepath.Join(dir, "*.go")) }

func byRule(name string) bool { return app.GoToolReads(name) && !strings.HasSuffix(name, "_test.go") }

func byPatternAndRule(dir string) bool {
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))

	return len(files) > 0 && app.GoToolReads(filepath.Base(files[0]))
}

func testFilesOnly(name string) bool { return strings.HasSuffix(name, "_test.go") }

func generatedOnly(dir string) ([]string, error) { return filepath.Glob(filepath.Join(dir, "zz_gen_*.go")) }
`
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, fn := range choosesGoFilesByItself(f) {
		got = append(got, fn.Name.Name)
	}
	if diff := cmp.Diff([]string{"bySuffix", "byPattern"}, got); diff != "" {
		t.Errorf("choosesGoFilesByItself() mismatch (-want +got):\n%s", diff)
	}
}

// TestEveryReaderAsksTheGoToolsRule holds every reader impulse check is built on, the
// application scan (the app package) and the checks themselves, and impulse brand's
// beside them, to the one rule about which Go files are read: none decides it for itself
// by the ".go" suffix. A reader that did took a scratch file kept beside a package's real
// ones (_scratch.go) for the application's code, which no build compiles. The rule is
// written in the package a generation run reads the application's tree by as well (the
// apptree package), and its own reader of an auth package's files is held here too.
func TestEveryReaderAsksTheGoToolsRule(t *testing.T) {
	t.Parallel()

	// The packages, by their directory from this one and from the module's root.
	packages := map[string]string{
		".": "cmd/impulse/internal/check", "../../../../app": "app", "../brand": "cmd/impulse/internal/brand",
		"../../../../internal/apptree": "internal/apptree",
	}
	var findings []string
	allowed := map[string]bool{}
	for dir, rel := range packages {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !app.GoToolReads(name) || strings.HasSuffix(name, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, fn := range choosesGoFilesByItself(f) {
				key := path.Join(rel, name) + ": " + fn.Name.Name
				if _, ok := suffixTestsWithoutTheRule[key]; ok {
					allowed[key] = true

					continue
				}
				findings = append(findings, fmt.Sprintf("%s:%d: %s decides a Go file by the \".go\" suffix for itself, so it reads a scratch file the Go tool passes over (_scratch.go, .draft.go); ask app.GoToolReads(name) of each file's name instead",
					path.Join(rel, name), fset.Position(fn.Pos()).Line, fn.Name.Name))
			}
		}
	}
	slices.Sort(findings)
	if len(findings) > 0 {
		t.Errorf("%d reader(s) choose Go files by themselves:\n%s", len(findings), strings.Join(findings, "\n"))
	}
	for key, why := range suffixTestsWithoutTheRule {
		if !allowed[key] {
			t.Errorf("%s is listed as testing a name for the \".go\" suffix without the rule (%s) and no longer does: take it off the list", key, why)
		}
	}
}
