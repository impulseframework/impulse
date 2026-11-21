package transition

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
)

// The files that hold an auth's first administrator outside the auth's own package, as
// the base writes them: the deployment package, which invites one, the migrate command,
// which runs the invitation at every release and sends it again on demand, the data
// level, which reads the address, and the environment template, which names the
// variable. A start whose first auth invites nobody takes the first administrator out of
// them (DirectoryStart.inviteNobody), and impulse add auth writes it into them for an
// auth that invites one (Auth.inviteAdministrator), from the same account of where it
// stands.
const (
	deployDir                = "pkg/deploy"
	deployFile               = deployDir + "/deploy.go"
	deployAdministrators     = deployDir + "/administrators.go"
	deployAdministratorsTest = deployDir + "/administrators_test.go"
	migrateCommand           = "cmd/deployment/migrate/main.go"
	baseDataLevel            = "pkg/config/data.go"
	baseEnvTemplate          = ".envrc.template"
)

// administratorDeclarations are the deployment package's declarations that are there for
// a first administrator alone, in the order the base declares them.
var administratorDeclarations = []string{"AdministratorAuths", "firstAdministrator", "administratorOf", "InviteAdministrators", "ReinviteAdministrator", "invitationRefused", "reportAdministrator"}

// inviteCall is how a migrate command runs the invitation of every auth that invites.
const inviteCall = "deploy.InviteAdministrators("

// piece is a stretch of a source file that is there for a first administrator alone. It
// is named by the text that stands before it and the text that stands after it, neither
// of which names an auth, and carries what stands between the two once the stretch is
// taken out.
type piece struct{ before, after, without string }

// stretch finds the piece in a text that holds it: where what follows before begins, and
// where after begins.
func (p *piece) stretch(text string) (from, to int, err error) {
	if strings.Count(text, p.before) != 1 {
		return 0, 0, errors.Newf("%q is not there once", p.before)
	}
	from = strings.Index(text, p.before) + len(p.before)
	length := strings.Index(text[from:], p.after)
	if length < 0 {
		return 0, 0, errors.Newf("%q does not follow %q", p.after, p.before)
	}

	return from, from + length, nil
}

// deployDocPiece is what the deployment package's comment says of the invitation.
var deployDocPiece = piece{
	before:  "applying the schema migrations, checking the release's role policy\n// against what the permission engine's store holds,",
	after:   "package deploy\n",
	without: " and bringing the feature\n// flags to the release's declarations.\n",
}

// dataLevelDocPiece is what the data level's comment on its mail sender says of the
// invitation: that the log is no mailbox to invite a first administrator through, so the
// mailbox comes before the first release.
var dataLevelDocPiece = piece{
	before:  "// relay takes over once the version is pinned; a release to any other environment is\n// refused until it is.",
	after:   "func openMailSender(",
	without: "\n",
}

// migratePieces are the stretches of the base's migrate command that are there for a
// first administrator alone: what its comments say of the invitation, its
// -reinvite-administrator flag with what parses, refuses and runs it, the two imports the
// flag alone reads, and the invitation a release runs after its role check, with the
// writer the step takes for it.
var migratePieces = []piece{
	{
		before:  "// Package main is the deployment's migration step: it applies the schema migrations",
		after:   "\n// reads the core and data configuration levels and nothing above them.\n",
		without: " and\n// checks the release's role policy against what the permission engine's store holds. It",
	},
	{
		before: "// being retired among them. It applies no migration and goes with no other flag.\n",
		after:  "package main\n",
	},
	{
		before: "\t\"os/signal\"\n",
		after:  "\t\"strconv\"\n",
	},
	{
		before: "\t\"strconv\"\n",
		after:  "\t\"time\"\n",
	},
	{
		before: "\tretireSecret string\n",
		after:  "}\n\n// versionFlag is a version to force",
	},
	{
		before:  "so -seed does not go with it; a retirement",
		after:   "func parse(",
		without: " goes\n// alone.\n",
	},
	{
		before: "confirm none is left, and exit; nothing is migrated\")\n",
		after:  "\tif err := fs.Parse(args); err != nil {\n",
	},
	{
		before: "\tforcing := o.force.set || o.forceData.set\n\tswitch {\n",
		after:  "\tcase o.retireSecret != \"\" && (forcing",
	},
	{
		before:  "// run does what the flags ask: the report, the force, the retirement,",
		after:   "func run(",
		without: " or the\n// migrations.\n",
	},
	{
		before:  "\t\treturn retireSecret(ctx, settings, o.retireSecret, out)\n",
		after:   "// report prints one line per migrations table",
		without: "\t}\n\n\treturn migrate(ctx, settings, o.seed)\n}\n\n",
	},
	{
		before:  "\t\treturn errors.Wrap(err, \"deploy.RetireSecret()\")\n\t}\n\n\treturn nil\n}\n\n",
		after:   "\t// The schema goes first, before any client opens",
		without: "// migrate is the deployment's migration step itself.\nfunc migrate(ctx context.Context, settings config.DatabaseSettings, seed bool) error {\n",
	},
	{
		before: "\t\treturn errors.Wrap(err, \"deploy.CheckRoles()\")\n\t}\n\n",
		after:  "\t// The feature flags the release declares",
	},
}

// withoutPieces is a source file with every piece taken out. It refuses a file that does
// not hold a piece where the base holds it: the file is then not what the caller knows.
func withoutPieces(src []byte, pieces []piece) ([]byte, error) {
	text := string(src)
	for i := range pieces {
		p := &pieces[i]
		from, to, err := p.stretch(text)
		if err != nil {
			return nil, err
		}
		text = text[:from] + p.without + text[to:]
	}

	return []byte(text), nil
}

// withPieces is a source file with every piece put back, each as the rendering holds it:
// the base's file of the same name, rendered for the auth the pieces are to speak of. It
// refuses a file that does not stand, at a piece's place, as the file stands with the
// piece taken out.
func withPieces(src, rendering []byte, pieces []piece) ([]byte, error) {
	text, rendered := string(src), string(rendering)
	for i := range pieces {
		p := &pieces[i]
		from, to, err := p.stretch(rendered)
		if err != nil {
			return nil, errors.Wrap(err, "the base's file")
		}
		taken := p.before + p.without + p.after
		if strings.Count(text, taken) != 1 {
			return nil, errors.Newf("%q is not there once", taken)
		}
		text = strings.Replace(text, taken, p.before+rendered[from:to]+p.after, 1)
	}

	return []byte(text), nil
}

// administratorEnvRE matches the block of an environment template that names an auth's
// first administrator, as the base writes it: its comment and the variable, unset.
func administratorEnvRE(variable string) *regexp.Regexp {
	name := regexp.QuoteMeta(variable)

	return regexp.MustCompile(`(?m)^# ` + name + ` is the email address[^\n]*\n(?:#[^\n]*\n)*?# export ` + name + `=\n`)
}

// declarationText is one top-level declaration of a Go source file, its comment
// included, as the file writes it. It refuses a file that declares nothing of the name.
func declarationText(rel string, src []byte, name string) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
	if err != nil {
		return "", errors.Wrapf(err, "parser.ParseFile(): %s", rel)
	}
	for _, decl := range file.Decls {
		declared, from := declaredNames(decl)
		for _, one := range declared {
			if one == name {
				return string(src[fset.Position(from).Offset:lineEnd(src, fset.Position(decl.End()).Offset)]), nil
			}
		}
	}

	return "", errors.Newf("%s declares no %s", rel, name)
}

// importBlock is the first import declaration of a Go source file, as the file writes
// it, or empty where the file imports nothing.
func importBlock(rel string, src []byte) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.ImportsOnly)
	if err != nil {
		return "", errors.Wrapf(err, "parser.ParseFile(): %s", rel)
	}
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			return string(src[fset.Position(gen.Pos()).Offset:lineEnd(src, fset.Position(gen.End()).Offset)]), nil
		}
	}

	return "", nil
}

// structField finds the field of a name in the struct types a Go source file declares at
// its top level: where the field begins, its comment included, and where it ends. It
// reports false where no struct declares one.
func structField(rel string, src []byte, name string) (from, to int, found bool, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
	if err != nil {
		return 0, 0, false, errors.Wrapf(err, "parser.ParseFile(): %s", rel)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			declared, ok := spec.(*ast.TypeSpec).Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range declared.Fields.List {
				if len(field.Names) != 1 || field.Names[0].Name != name {
					continue
				}
				start := field.Pos()
				if field.Doc != nil {
					start = field.Doc.Pos()
				}

				return fset.Position(start).Offset, fset.Position(field.End()).Offset, true, nil
			}
		}
	}

	return 0, 0, false, nil
}

// cutField takes one field out of the struct that declares it, with the comment above it
// and the blank line that sets it apart from the field before. It refuses a file no
// struct of which declares the field.
func cutField(rel string, src []byte, name string) ([]byte, error) {
	from, to, found, err := structField(rel, src, name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.Newf("it declares no field %s", name)
	}
	start, end := lineStart(src, from), lineEnd(src, to)
	if before := lineStart(src, max(start-1, 0)); start > 0 && strings.TrimSpace(string(src[before:start])) == "" {
		start = before
	}

	return append(append([]byte(nil), src[:start]...), src[end:]...), nil
}

// environmentStruct names the struct a data level reads its environment into: the type
// its DataConfiguration keeps under the field env, which the base's accessors of a
// setting read (c.env.<Setting>). It is empty where DataConfiguration keeps none.
func environmentStruct(rel string, src []byte) string {
	file, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
	if err != nil {
		return ""
	}
	name := ""
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "DataConfiguration" {
			return true
		}
		declared, ok := spec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, field := range declared.Fields.List {
			if len(field.Names) != 1 || field.Names[0].Name != "env" {
				continue
			}
			if pointer, ok := field.Type.(*ast.StarExpr); ok {
				if named, ok := pointer.X.(*ast.Ident); ok {
					name = named.Name
				}
			}
		}

		return false
	})

	return name
}

// rewritten is a file of the application as a step will write it.
type rewritten struct {
	rel  string
	data []byte
	mode os.FileMode
}

// inviteNobody takes what invites a first administrator out of an application whose
// first auth invites nobody, since its directory gives every role: the deployment
// package's declarations, the migrate command's invitation and its
// -reinvite-administrator flag, the data level's address setting with its accessor and
// what its mail sender's comment says of an invitation, the deployment package's test of
// what it says of an invitation, and the variable in the environment template. The auth has no foothold to invite through, so the base's
// deployment package does not compile against it, and code that invites nobody would
// stand in the application for no auth. It is all taken out or none of it: a file that
// is not as the base writes it leaves the three as they are, and the change says what to
// take out by hand.
func (d *DirectoryStart) inviteNobody(a *app.App, ch *Change) error {
	variable := authpkg.AdministratorVariable(d.Auth)
	files, why, err := d.withoutAdministrator(a)
	if err != nil {
		return err
	}
	if why != "" {
		ch.skipf("%s, so what invites a first administrator was left as it is. The %s auth invites nobody, since its directory gives every role, the Super Administrator's included, and the application does not compile while its deployment invites one: take what invites one out of the deployment package (%s), of the migrate command (the call of InviteAdministrators and the -reinvite-administrator flag) and of the data level (the %s setting and its accessor)", why, d.Auth, strings.Join(administratorDeclarations, ", "), variable)

		return nil
	}
	taken := make([]string, 0, len(files)+2)
	for i := range files {
		f := &files[i]
		if err := os.WriteFile(a.Abs(f.rel), f.data, f.mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		taken = append(taken, f.rel)
	}
	parts := []string{
		"the deployment package's AdministratorAuths, InviteAdministrators and ReinviteAdministrator with what they stand on",
		"the migrate command's invitation after the role check and its -reinvite-administrator flag",
		fmt.Sprintf("the data level's address setting (%s) with its accessor, and what its comment on the mail sender says of an invitation", variable),
	}
	switch {
	case !exists(a, deployAdministratorsTest):
	case asTheBaseRenders(a, deployAdministratorsTest, d.Auth):
		if err := os.Remove(a.Abs(deployAdministratorsTest)); err != nil {
			return errors.Wrap(err, "os.Remove()")
		}
		taken = append(taken, deployAdministratorsTest)
		parts = append(parts, "the test of what the deployment writes about an invitation")
	default:
		ch.skipf("%s: it tests what the deployment writes about a first administrator's invitation, which was taken out, and it is not as the base writes it, so it was left as it is; take those tests out of it", deployAdministratorsTest)
	}
	named, err := withoutAdministratorEnv(a, variable, ch)
	if err != nil {
		return err
	}
	if named {
		taken = append(taken, a.EnvTemplate)
		parts = append(parts, "the environment template's variable")
	}
	ch.didf("%s: what invites a first administrator was taken out, since the %s auth's directory gives every role, the Super Administrator's included, and nobody is invited to it: %s. A release invites nobody and asks for no mailbox on that account, and impulse add auth writes the invitation for an auth added later that invites a first administrator", strings.Join(taken, ", "), d.Auth, strings.Join(parts, "; "))

	return nil
}

// withoutAdministrator is the deployment package's file, the migrate command and the
// data level with what invites the auth's first administrator taken out of each, to
// write together. Where one of them is not there or is not as the base writes it, it
// answers no file and says which and why.
func (d *DirectoryStart) withoutAdministrator(a *app.App) (files []rewritten, why string, err error) {
	accessor := names.Pascal(d.Auth) + "Administrator"
	level, _, _, err := findDeclaringFile(a, "DataConfiguration")
	if err != nil {
		return nil, "", err
	}
	if level == "" {
		return nil, "no file declares a DataConfiguration struct", nil
	}
	cuts := []struct {
		rel string
		cut func(src []byte) ([]byte, error)
	}{
		{deployFile, func(src []byte) ([]byte, error) {
			out, err := cutSource(deployFile, src, administratorDeclarations, "", nil, "")
			if err != nil {
				return nil, err
			}

			return withoutPieces(out, []piece{deployDocPiece})
		}},
		{migrateCommand, func(src []byte) ([]byte, error) {
			return withoutPieces(src, migratePieces)
		}},
		{level, func(src []byte) ([]byte, error) {
			out, err := cutSource(level, src, []string{accessor}, "", nil, "")
			if err != nil {
				return nil, err
			}

			out, err = cutField(level, out, accessor)
			if err != nil {
				return nil, err
			}

			return withoutPieces(out, []piece{dataLevelDocPiece})
		}},
	}
	for _, one := range cuts {
		if !exists(a, one.rel) {
			return nil, one.rel + " is not there", nil
		}
		src, mode, err := readFile(a, one.rel)
		if err != nil {
			return nil, "", err
		}
		out, err := one.cut(src)
		if err == nil {
			out, err = finishSource(one.rel, src, out)
		}
		if err != nil {
			return nil, fmt.Sprintf("%s is not as the base writes it (%v)", one.rel, errors.Cause(err)), nil
		}
		files = append(files, rewritten{rel: one.rel, data: out, mode: mode})
	}

	return files, "", nil
}

// withoutAdministratorEnv takes the variable that names a first administrator out of the
// environment template, with its comment, and reports whether it did. A template that
// names the variable some other way is left as it is, and the change says so.
func withoutAdministratorEnv(a *app.App, variable string, ch *Change) (bool, error) {
	if a.EnvTemplate == "" || !exists(a, a.EnvTemplate) {
		return false, nil
	}
	data, mode, err := readFile(a, a.EnvTemplate)
	if err != nil {
		return false, err
	}
	block := administratorEnvRE(variable)
	if len(block.FindAll(data, -1)) != 1 {
		if bytes.Contains(data, []byte(variable)) {
			ch.skipf("%s: it names %s, which nothing reads now, and not as the base writes it, so it was left as it is; take the variable out", a.EnvTemplate, variable)
		}

		return false, nil
	}
	if err := os.WriteFile(a.Abs(a.EnvTemplate), block.ReplaceAll(data, nil), mode); err != nil {
		return false, errors.Wrap(err, "os.WriteFile()")
	}

	return true, nil
}
