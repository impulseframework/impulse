package check

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// envTemplate verifies that every env tag without a default appears in the development
// environment template, so a fresh checkout knows which variables it must set. The
// variables a framework settings struct the configuration embeds declares are among the
// tags, each naming the struct it came from. A versioned secret (secret:"versioned") is
// handed over one variable for each version and none under its declared name, so the
// template carries its first version (APP_SECRET_KEY_1).
type envTemplate struct{}

func (envTemplate) Name() string { return "env-template" }

func (envTemplate) Describe() string {
	return "every env tag without a default appears in the development environment template"
}

var envAssignRE = regexp.MustCompile(`(?m)^\s*#?\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=`)

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
	declared := map[string]bool{}
	for _, m := range envAssignRE.FindAllStringSubmatch(string(data), -1) {
		declared[m[1]] = true
	}

	var details, missing []string
	seen := map[string]bool{}
	for _, t := range a.EnvTags {
		name := templateName(t)
		if t.HasDefault || declared[name] || seen[name] {
			continue
		}
		seen[name] = true
		missing = append(missing, name)
		details = append(details, fmt.Sprintf("%s:%d: %s%s is not in %s", t.File, t.Line, name, tagNote(t), a.EnvTemplate))
	}

	if len(missing) == 0 {
		return pass(c.Name(), fmt.Sprintf("%d env tag(s) covered by %s", len(a.EnvTags), a.EnvTemplate))
	}
	if env.Fix {
		slices.Sort(missing)
		lines := make([]string, 0, len(missing))
		for _, n := range missing {
			lines = append(lines, "export "+n+"=")
		}
		if err := appendLines(a.Abs(a.EnvTemplate), "# Added by impulse check --fix: set these for local development.", lines); err != nil {
			return fail(c.Name(), err.Error())
		}

		return passWithDetails(c.Name(), fmt.Sprintf("%d variable(s) added to %s", len(lines), a.EnvTemplate), details...)
	}

	return fail(c.Name(), fmt.Sprintf("%d variable(s) missing from %s (--fix adds them)", len(missing), a.EnvTemplate), details...)
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
