package check

import (
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// envTemplate verifies that every env tag without a default appears in the development
// environment template, so a fresh checkout knows which variables it must set. The
// variables a framework settings struct the configuration embeds declares are among the
// tags, each naming the struct it came from. A comment names a variable well enough,
// since one a single process is given, or another program reads, is written so.
//
// A versioned secret (secret:"versioned") is handed over one variable for each version
// and none under its declared name, so the template carries its first version
// (APP_SECRET_KEY_1), set by a line: every command that opens the database reads it, so a
// comment does not cover it, and neither does an empty value, which the library refuses.
// And it carries no key: a key written in a file git tracks is the key of every checkout,
// and the library cannot tell it from a real one. The template makes one for the checkout
// instead, in a file .gitignore ignores (generatedSecretLines).
type envTemplate struct{}

func (envTemplate) Name() string { return "env-template" }

func (envTemplate) Describe() string {
	return "every env tag without a default appears in the development environment template, and a versioned secret's first version is set by a line that makes it for the checkout in an ignored file, never written in the template"
}

// envAssignRE finds the variables a template names: assigned by a line, or by a line a
// comment holds.
var envAssignRE = regexp.MustCompile(`(?m)^\s*#?\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=`)

// generatedSecretRE reads the value of a versioned secret's variable as the template
// makes it for a checkout: the contents of a file, whose name it captures.
var generatedSecretRE = regexp.MustCompile(`^\$\(cat ([A-Za-z0-9._/-]+)\)$`)

// envGitignore is the file that keeps a checkout's generated key out of git.
const envGitignore = ".gitignore"

// secretKeyFile is the file a checkout's development value of a versioned secret is kept
// in, at the application's root: .app-secret-key.dev for APP_SECRET_KEY.
func secretKeyFile(t app.EnvTag) string {
	return "." + strings.ToLower(strings.ReplaceAll(t.Name, "_", "-")) + ".dev"
}

// generatedSecretLines are the lines that give a versioned secret's first version its
// development value in a shell template: 32 random bytes in Base64, written to the key
// file the first time the template is sourced from the application's root and read from
// it from then on, so one checkout keeps one key and no key is in the template.
func generatedSecretLines(t app.EnvTag) []string {
	file := secretKeyFile(t)

	return []string{
		fmt.Sprintf("[ -s %s ] || (umask 077 && head -c 32 /dev/urandom | base64 > %s)", file, file),
		fmt.Sprintf(`export %s="$(cat %s)"`, t.VersionedName(1), file),
	}
}

// shellTemplate reports whether the template is shell a checkout sources (.envrc.template)
// and not a list of assignments (.env.template, .env.example), which can run nothing.
func shellTemplate(template string) bool {
	return strings.HasPrefix(path.Base(template), ".envrc")
}

// ignoresFile reports whether a .gitignore's lines ignore the file at the root.
func ignoresFile(gitignore, file string) bool {
	for line := range strings.Lines(gitignore) {
		switch strings.TrimSpace(line) {
		case file, "/" + file:
			return true
		}
	}

	return false
}

func (c envTemplate) Run(_ context.Context, env *Env) Result {
	a := env.App
	if len(a.EnvTags) == 0 {
		return skip(c.Name(), "no env tags declared")
	}
	if a.EnvTemplate == "" {
		return skip(c.Name(), "no .envrc.template (or .env.template, .env.example) at the application root")
	}

	data, err := os.ReadFile(a.Abs(a.EnvTemplate))
	if err != nil {
		return fail(c.Name(), errors.Wrap(err, "os.ReadFile()").Error())
	}
	gitignore, err := os.ReadFile(a.Abs(envGitignore))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail(c.Name(), errors.Wrap(err, "os.ReadFile()").Error())
	}

	found := c.read(a, string(data), string(gitignore))
	details := append(found.details, found.unmended...)
	if len(details) == 0 {
		return pass(c.Name(), fmt.Sprintf("%d env tag(s) covered by %s", len(a.EnvTags), a.EnvTemplate))
	}
	if len(found.unmended) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d problem(s) with %s", len(details), a.EnvTemplate), details...)
	}
	if !env.Fix {
		return fail(c.Name(), fmt.Sprintf("%d variable(s) missing from %s (--fix adds them)", len(details), a.EnvTemplate), details...)
	}
	if err := c.mend(a, found, string(gitignore)); err != nil {
		return fail(c.Name(), err.Error())
	}

	return passWithDetails(c.Name(), fmt.Sprintf("%d variable(s) added to %s", len(details), a.EnvTemplate), details...)
}

// envFindings are what the template lacks or gets wrong. --fix mends the variables the
// template lacks, the versioned secrets whose first version it does not set, and the key
// files .gitignore lets through; unmended holds what it does not, which keeps the check
// failing whatever else is added.
type envFindings struct {
	missing   []string
	versioned []app.EnvTag
	unignored []string
	details   []string
	unmended  []string
}

// read holds every tag without a default to the template: named by it, in a line or in
// a comment, and, for a versioned secret, set by a line that makes its first version for
// the checkout.
func (c envTemplate) read(a *app.App, template, gitignore string) *envFindings {
	named := map[string]bool{}
	for _, m := range envAssignRE.FindAllStringSubmatch(template, -1) {
		named[m[1]] = true
	}

	found := &envFindings{}
	seen := map[string]bool{}
	for _, t := range a.EnvTags {
		name := templateName(t)
		if t.HasDefault || seen[name] {
			continue
		}
		seen[name] = true
		at := fmt.Sprintf("%s:%d: %s%s", t.File, t.Line, name, tagNote(t))
		if t.Versioned {
			c.readVersioned(a, t, at, template, gitignore, found)

			continue
		}
		// A comment names a variable well enough: one a single process is given (the
		// Procfile sets a site's port and bundle), or one another program reads, is
		// written in the template as a comment, required or not.
		if !named[name] {
			found.missing = append(found.missing, name)
			found.details = append(found.details, fmt.Sprintf("%s is not in %s", at, a.EnvTemplate))
		}
	}

	return found
}

// readVersioned holds the first version of a versioned secret to the template. A key
// written in the template is refused in every kind of template. A shell template sets
// the variable from a file it makes for the checkout, which .gitignore ignores; a list
// of assignments can make no key, so it says nothing of the variable.
func (c envTemplate) readVersioned(a *app.App, t app.EnvTag, at, template, gitignore string, found *envFindings) {
	generated := shellTemplate(a.EnvTemplate)
	value, set := templateValue(template, t.VersionedName(1))
	switch {
	case set && value != "" && !generatedSecretRE.MatchString(value):
		found.unmended = append(found.unmended, fmt.Sprintf("%s holds a key in %s, a file git tracks: every checkout would encrypt under that one key, which is no secret. %s", at, a.EnvTemplate, c.versionedAdvice(t, generated)))
	case !generated:
	case set && value == "":
		found.unmended = append(found.unmended, fmt.Sprintf("%s is set to nothing in %s, which the application refuses when it starts. %s", at, a.EnvTemplate, c.versionedAdvice(t, generated)))
	case set:
		if file := generatedSecretRE.FindStringSubmatch(value)[1]; !ignoresFile(gitignore, file) {
			found.unignored = append(found.unignored, file)
			found.details = append(found.details, fmt.Sprintf("%s is read from %s, which %s does not ignore, so the checkout's key would be committed (--fix adds %s)", at, file, envGitignore, file))
		}
	default:
		found.versioned = append(found.versioned, t)
		found.details = append(found.details, fmt.Sprintf("%s is not set in %s", at, a.EnvTemplate))
	}
}

// versionedAdvice says what the template carries for a versioned secret in place of a
// key: the lines that make one for the checkout, in a shell template, and nothing in a
// list of assignments, which can make none.
func (envTemplate) versionedAdvice(t app.EnvTag, generated bool) string {
	if !generated {
		return fmt.Sprintf("Take the line out, and set %s in the checkout's own environment file, which git ignores (openssl rand -base64 32 makes a value)", t.VersionedName(1))
	}
	lines := generatedSecretLines(t)

	return fmt.Sprintf("Replace the line with the two that make a key for the checkout and keep it in %s, and have %s ignore that file: `%s` and `%s`", secretKeyFile(t), envGitignore, lines[0], lines[1])
}

// mend writes what --fix adds: an empty assignment for each variable the template
// lacks, for the developer to set; for each versioned secret the lines that make its
// first version for the checkout, so the application starts as it is; and each key file
// in .gitignore.
func (envTemplate) mend(a *app.App, found *envFindings, gitignore string) error {
	if len(found.missing) > 0 {
		slices.Sort(found.missing)
		lines := make([]string, 0, len(found.missing))
		for _, n := range found.missing {
			lines = append(lines, "export "+n+"=")
		}
		if err := appendLines(a.Abs(a.EnvTemplate), "# Added by impulse check --fix: set these for local development.", lines); err != nil {
			return err
		}
	}
	unignored := found.unignored
	for _, t := range found.versioned {
		file := secretKeyFile(t)
		comment := fmt.Sprintf("# Added by impulse check --fix: %s is version 1 of %s for development, 32 random bytes made once for this checkout and kept in %s, which %s ignores.", t.VersionedName(1), t.Name, file, envGitignore)
		if err := appendLines(a.Abs(a.EnvTemplate), comment, generatedSecretLines(t)); err != nil {
			return err
		}
		if !ignoresFile(gitignore, file) {
			unignored = append(unignored, file)
		}
	}
	if len(unignored) == 0 {
		return nil
	}
	slices.Sort(unignored)

	return appendLines(a.Abs(envGitignore), "", slices.Compact(unignored))
}

// templateName is the variable the template carries for a tag: its name, or, for a
// versioned secret, the variable its first version is handed over in.
func templateName(t app.EnvTag) string {
	if t.Versioned {
		return t.VersionedName(1)
	}

	return t.Name
}

// tagNote says what else the check knows of the variable: that it is required, that it
// is the first version of a versioned secret, and the framework settings struct that
// declares it when the application embeds one.
func tagNote(t app.EnvTag) string {
	var notes []string
	if t.Required {
		notes = append(notes, "required")
	}
	if t.Versioned {
		notes = append(notes, "the first version of "+t.Name+", declared secret:\"versioned\"")
	}
	if t.Origin != "" {
		notes = append(notes, "declared by the embedded "+t.Origin)
	}
	if len(notes) == 0 {
		return ""
	}

	return " (" + strings.Join(notes, ", ") + ")"
}
