package check

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

// The flat fixture's config as the test edits it to embed the cloud driver's settings
// struct: the import line it replaces and the import block replacing it, the field the
// embedding follows and the embedding, and the struct's name as a tag's origin.
const (
	flatTimeImport  = "import \"time\"\n"
	flatCloudImport = "import (\n\t\"time\"\n\n\t\"github.com/impulseframework/impulse/cloud/gcp\"\n)\n"
	flatDataField   = "\tData      Data   `env:\",prefix=LIGHTHOUSE_\"`\n"
	flatEmbedding   = "\t// The Google Cloud driver's variables: the logging project and the trace sampling.\n\tgcp.Settings\n"
	gcpSettings     = "github.com/impulseframework/impulse/cloud/gcp.Settings"
)

// TestEnvTemplateEmbeddedSettings runs the check on a configuration embedding the cloud
// driver's settings struct: the variable the struct declares without a default is
// checked as the application's own are, failing with the struct named when the template
// lacks it, covered when the template documents it, and added by --fix the same way.
func TestEnvTemplateEmbeddedSettings(t *testing.T) {
	t.Parallel()

	const (
		loggingMissing = "pkg/config/config.go:<embedding>: GOOGLE_CLOUD_LOGGING_PROJECT (declared by the embedded " + gcpSettings + ") is not in .envrc.template"
		beaconMissing  = "pkg/config/config.go:<beacon>: LIGHTHOUSE_BEACON_API_KEY (required) is not in .envrc.template"
	)
	tests := []struct {
		name string
		// template is appended to the fixture's template.
		template string
		fix      bool
		// want is the result, with <embedding> and <beacon> for the lines of the embedding
		// and of the application's own missing variable.
		want Result
		// wantTemplate is the template after --fix.
		wantTemplate string
	}{
		{
			name: "a framework variable the template lacks fails, naming the struct",
			want: Result{Name: envTemplate{}.Name(), Status: Fail, Summary: "2 variable(s) missing from .envrc.template (--fix adds them)", Details: []string{loggingMissing, beaconMissing}},
		},
		{
			name:     "the template documenting the variable covers it",
			template: "# export GOOGLE_CLOUD_LOGGING_PROJECT=\nexport LIGHTHOUSE_BEACON_API_KEY=\n",
			want:     pass(envTemplate{}.Name(), "9 env tag(s) covered by .envrc.template"),
		},
		{
			name:         "--fix adds it as it adds the application's",
			fix:          true,
			want:         Result{Name: envTemplate{}.Name(), Status: Pass, Summary: "2 variable(s) added to .envrc.template", Details: []string{loggingMissing, beaconMissing}},
			wantTemplate: "# Lighthouse local development\nexport LIGHTHOUSE_PROJECT_ID=\nexport LIGHTHOUSE_SPANNER_DATABASE=\n# export LIGHTHOUSE_COOKIE_KEY=\n# Added by impulse check --fix: set these for local development.\nexport GOOGLE_CLOUD_LOGGING_PROJECT=\nexport LIGHTHOUSE_BEACON_API_KEY=\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, src := embeddingFixture(t, tt.template)
			want := tt.want
			want.Details = atLines(t, src, want.Details)
			got := envTemplate{}.Run(context.Background(), &Env{App: a, Fix: tt.fix})
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
			if tt.wantTemplate == "" {
				return
			}
			data, err := os.ReadFile(a.Abs(a.EnvTemplate))
			if err != nil {
				t.Fatalf("os.ReadFile() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantTemplate, string(data)); diff != "" {
				t.Errorf("template after --fix mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The versioned secret the tests declare, the file its development value is kept in, and
// the two lines that make the value for a checkout.
const (
	versionedField = "\tSecretKey string `env:\"LIGHTHOUSE_SECRET_KEY\" secret:\"versioned\"`\n"
	versionedFile  = ".lighthouse-secret-key.dev"
	versionedMake  = "[ -s .lighthouse-secret-key.dev ] || (umask 077 && head -c 32 /dev/urandom | base64 > .lighthouse-secret-key.dev)\n"
	versionedRead  = "export LIGHTHOUSE_SECRET_KEY_1=\"$(cat .lighthouse-secret-key.dev)\"\n"
	versionedAt    = "pkg/config/config.go:<secret>: LIGHTHOUSE_SECRET_KEY_1 (the first version of LIGHTHOUSE_SECRET_KEY, declared secret:\"versioned\")"
	// versionedAdvice is what a refusal tells the author to write in place of a key.
	versionedAdvice = "Replace the line with the two that make a key for the checkout and keep it in .lighthouse-secret-key.dev, and have .gitignore ignore that file: `[ -s .lighthouse-secret-key.dev ] || (umask 077 && head -c 32 /dev/urandom | base64 > .lighthouse-secret-key.dev)` and `export LIGHTHOUSE_SECRET_KEY_1=\"$(cat .lighthouse-secret-key.dev)\"`"
)

// versionedFixture copies the flat fixture, declares a versioned secret in its
// configuration, appends the lines to its template (renamed when the test asks for
// another kind of template) and writes its .gitignore; it returns the directory and the
// line the secret is declared on.
func versionedFixture(t *testing.T, templateName, template, gitignore string) (dir string, line int) {
	t.Helper()

	dir = t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "..", "..", "app", "testdata", "flat"))); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	src := editFile(t, filepath.Join(dir, "pkg", "config", "config.go"), func(src string) string {
		if !strings.Contains(src, flatDataField) {
			t.Fatalf("the fixture's config does not contain %q", flatDataField)
		}

		return strings.Replace(src, flatDataField, flatDataField+versionedField, 1)
	})
	editFile(t, filepath.Join(dir, ".envrc.template"), func(src string) string {
		return src + template
	})
	if templateName != ".envrc.template" {
		if err := os.Rename(filepath.Join(dir, ".envrc.template"), filepath.Join(dir, templateName)); err != nil {
			t.Fatalf("os.Rename() error = %v", err)
		}
	}
	if gitignore != "" {
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
	}

	return dir, sourceLine(t, src, "SecretKey")
}

// TestEnvTemplateVersionedSecret runs the check on a configuration declaring a versioned
// secret. The service is handed one variable for each version and none under the declared
// name, so the template sets the first version's variable, and it writes no key: a key
// in a file git tracks is every checkout's key. The template makes one for the checkout
// in a file .gitignore ignores, which is what --fix writes, so the application starts as
// --fix leaves it. A comment sets nothing, an empty value is refused at start, and a
// list of assignments, which can make no key, says nothing of the variable.
func TestEnvTemplateVersionedSecret(t *testing.T) {
	t.Parallel()

	const (
		covered         = "export LIGHTHOUSE_BEACON_API_KEY=\n"
		fixtureTemplate = "# Lighthouse local development\nexport LIGHTHOUSE_PROJECT_ID=\nexport LIGHTHOUSE_SPANNER_DATABASE=\n# export LIGHTHOUSE_COOKIE_KEY=\n"
		addedByFix      = "# Added by impulse check --fix: LIGHTHOUSE_SECRET_KEY_1 is version 1 of LIGHTHOUSE_SECRET_KEY for development, 32 random bytes made once for this checkout and kept in .lighthouse-secret-key.dev, which .gitignore ignores.\n"
		aKey            = "ZGV2ZWxvcG1lbnQtb25seSBhcHBsaWNhdGlvbiBrZXk="
		missing         = "1 variable(s) missing from .envrc.template (--fix adds them)"
		notSet          = versionedAt + " is not set in .envrc.template"
	)
	name := envTemplate{}.Name()
	tests := []struct {
		name string
		// templateName is the template's file, .envrc.template unless set; template is
		// appended to the fixture's template; gitignore is the application's .gitignore.
		templateName string
		template     string
		gitignore    string
		fix          bool
		// want is the result, with <secret> for the line of the versioned declaration.
		want Result
		// wantTemplate and wantGitignore are the files after --fix.
		wantTemplate  string
		wantGitignore string
	}{
		{
			name:     "a template without the first version fails, naming its variable",
			template: covered,
			want:     Result{Name: name, Status: Fail, Summary: missing, Details: []string{notSet}},
		},
		{
			name:     "the declared name alone does not cover it: no variable has that name",
			template: covered + "export LIGHTHOUSE_SECRET_KEY=\n",
			want:     Result{Name: name, Status: Fail, Summary: missing, Details: []string{notSet}},
		},
		{
			name:     "a comment sets nothing",
			template: covered + "# export LIGHTHOUSE_SECRET_KEY_1=\n",
			want:     Result{Name: name, Status: Fail, Summary: missing, Details: []string{notSet}},
		},
		{
			name:     "set to nothing, the application refuses to start, and --fix does not take the line for set",
			template: covered + "export LIGHTHOUSE_SECRET_KEY_1=\n",
			fix:      true,
			want:     Result{Name: name, Status: Fail, Summary: "1 problem(s) with .envrc.template", Details: []string{versionedAt + " is set to nothing in .envrc.template, which the application refuses when it starts. " + versionedAdvice}},
		},
		{
			name:      "a key written in the template is every checkout's key",
			template:  covered + "export LIGHTHOUSE_SECRET_KEY_1=" + aKey + "\n",
			gitignore: versionedFile + "\n",
			want:      Result{Name: name, Status: Fail, Summary: "1 problem(s) with .envrc.template", Details: []string{versionedAt + " holds a key in .envrc.template, a file git tracks: every checkout would encrypt under that one key, which is no secret. " + versionedAdvice}},
		},
		{
			name:      "made for the checkout in an ignored file, it is covered",
			template:  covered + versionedMake + versionedRead,
			gitignore: ".envrc\n" + versionedFile + "\n",
			want:      pass(name, "8 env tag(s) covered by .envrc.template"),
		},
		{
			name:      "the file the key is kept in is not ignored",
			template:  covered + versionedMake + versionedRead,
			gitignore: ".envrc\n",
			want:      Result{Name: name, Status: Fail, Summary: missing, Details: []string{versionedAt + " is read from .lighthouse-secret-key.dev, which .gitignore does not ignore, so the checkout's key would be committed (--fix adds .lighthouse-secret-key.dev)"}},
		},
		{
			name:          "--fix has .gitignore ignore the file",
			template:      covered + versionedMake + versionedRead,
			gitignore:     ".envrc\n",
			fix:           true,
			want:          Result{Name: name, Status: Pass, Summary: "1 variable(s) added to .envrc.template", Details: []string{versionedAt + " is read from .lighthouse-secret-key.dev, which .gitignore does not ignore, so the checkout's key would be committed (--fix adds .lighthouse-secret-key.dev)"}},
			wantTemplate:  fixtureTemplate + covered + versionedMake + versionedRead,
			wantGitignore: ".envrc\n" + versionedFile + "\n",
		},
		{
			name:          "--fix adds the lines that make a key for the checkout, never an empty variable",
			template:      covered,
			gitignore:     ".envrc\n",
			fix:           true,
			want:          Result{Name: name, Status: Pass, Summary: "1 variable(s) added to .envrc.template", Details: []string{notSet}},
			wantTemplate:  fixtureTemplate + covered + addedByFix + versionedMake + versionedRead,
			wantGitignore: ".envrc\n" + versionedFile + "\n",
		},
		{
			name:         "a list of assignments can make no key, and says nothing of the variable",
			templateName: ".env.template",
			template:     covered,
			want:         pass(name, "8 env tag(s) covered by .env.template"),
		},
		{
			name:         "a key written in a list of assignments is refused all the same",
			templateName: ".env.template",
			template:     covered + "LIGHTHOUSE_SECRET_KEY_1=" + aKey + "\n",
			want:         Result{Name: name, Status: Fail, Summary: "1 problem(s) with .env.template", Details: []string{versionedAt + " holds a key in .env.template, a file git tracks: every checkout would encrypt under that one key, which is no secret. Take the line out, and set LIGHTHOUSE_SECRET_KEY_1 in the checkout's own environment file, which git ignores (openssl rand -base64 32 makes a value)"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			templateName := tt.templateName
			if templateName == "" {
				templateName = ".envrc.template"
			}
			dir, line := versionedFixture(t, templateName, tt.template, tt.gitignore)
			a, err := app.Discover(dir)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}

			want := tt.want
			for i, d := range want.Details {
				want.Details[i] = strings.ReplaceAll(d, "<secret>", strconv.Itoa(line))
			}
			got := envTemplate{}.Run(context.Background(), &Env{App: a, Fix: tt.fix})
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
			for file, want := range map[string]string{templateName: tt.wantTemplate, ".gitignore": tt.wantGitignore} {
				if want == "" {
					continue
				}
				data, err := os.ReadFile(filepath.Join(dir, file))
				if err != nil {
					t.Fatalf("os.ReadFile() error = %v", err)
				}
				if diff := cmp.Diff(want, string(data)); diff != "" {
					t.Errorf("%s after --fix mismatch (-want +got):\n%s", file, diff)
				}
			}
		})
	}
}

// TestEnvTemplateFixStarts holds what --fix writes for a versioned secret to what it is
// for: an application that starts. The template --fix leaves is sourced as a checkout
// sources it, twice: the variable holds 32 random bytes in Base64, the same both times,
// kept in a file only its owner reads, which .gitignore ignores; the template itself
// holds no key; and the check passes on what it wrote.
func TestEnvTemplateFixStarts(t *testing.T) {
	t.Parallel()

	dir, _ := versionedFixture(t, ".envrc.template", "export LIGHTHOUSE_BEACON_API_KEY=\n", ".envrc\n")
	a, err := app.Discover(dir)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}
	if got := (envTemplate{}).Run(context.Background(), &Env{App: a, Fix: true}); got.Status != Pass {
		t.Fatalf("Run() with --fix = %+v, want a pass", got)
	}

	source := func() string {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "sh", "-c", `set -a; . ./.envrc.template; set +a; printf %s "$LIGHTHOUSE_SECRET_KEY_1"`)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("sourcing the template: %v", err)
		}

		return string(out)
	}
	first, second := source(), source()
	if key, err := base64.StdEncoding.DecodeString(first); err != nil || len(key) != 32 {
		t.Fatalf("the template sets LIGHTHOUSE_SECRET_KEY_1 to %d byte(s), error %v; want 32 random bytes in Base64", len(key), err)
	}
	if first != second {
		t.Error("the template made another key the second time it was sourced: the secrets a checkout stored would no longer open")
	}

	info, err := os.Stat(filepath.Join(dir, versionedFile))
	if err != nil {
		t.Fatalf("the key file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the key file's mode is %v, want only its owner to read it", info.Mode().Perm())
	}
	template, err := os.ReadFile(filepath.Join(dir, ".envrc.template"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if strings.Contains(string(template), first) {
		t.Error("the template holds the key")
	}
	gitignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || !ignoresFile(string(gitignore), versionedFile) {
		t.Errorf(".gitignore does not ignore the key file, error %v", err)
	}

	a, err = app.Discover(dir)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}
	if got := (envTemplate{}).Run(context.Background(), &Env{App: a}); got.Status != Pass {
		t.Errorf("Run() on what --fix wrote = %+v, want a pass", got)
	}
}

// TestEnvTemplateNamedInAComment pins what a comment covers. A variable one process is
// given, or another program reads, is written in the template as a comment, required or
// not, and that names it; the first version of a versioned secret is not covered so
// (TestEnvTemplateVersionedSecret), since every command that opens the database reads it.
func TestEnvTemplateNamedInAComment(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "..", "..", "app", "testdata", "flat"))); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	editFile(t, filepath.Join(dir, ".envrc.template"), func(src string) string {
		return src + "# export LIGHTHOUSE_BEACON_API_KEY=\n"
	})
	a, err := app.Discover(dir)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}
	if diff := cmp.Diff(pass(envTemplate{}.Name(), "7 env tag(s) covered by .envrc.template"), envTemplate{}.Run(context.Background(), &Env{App: a})); diff != "" {
		t.Errorf("Run() mismatch (-want +got):\n%s", diff)
	}
}

// embeddingFixture copies the flat fixture, embeds the cloud driver's settings struct in
// its core configuration, appends the lines to its template, and discovers it; it returns
// the application and the edited config source.
func embeddingFixture(t *testing.T, template string) (fixture *app.App, source string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "..", "..", "app", "testdata", "flat"))); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	src := editFile(t, filepath.Join(dir, "pkg", "config", "config.go"), func(src string) string {
		for _, edit := range []struct{ old, new string }{{flatTimeImport, flatCloudImport}, {flatDataField, flatDataField + flatEmbedding}} {
			if !strings.Contains(src, edit.old) {
				t.Fatalf("the fixture's config does not contain %q", edit.old)
			}
			src = strings.Replace(src, edit.old, edit.new, 1)
		}

		return src
	})
	if template != "" {
		editFile(t, filepath.Join(dir, ".envrc.template"), func(src string) string {
			return src + template
		})
	}
	a, err := app.Discover(dir)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}

	return a, src
}

// editFile rewrites a file of the fixture copy and returns the new content.
func editFile(t *testing.T, path string, edit func(src string) string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	src := edit(string(data))
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	return src
}

// atLines fills <embedding> and <beacon> in the details with the lines the embedding and
// the fixture's own missing variable are declared on in the edited source; a result
// without details stays without.
func atLines(t *testing.T, src string, details []string) []string {
	t.Helper()

	if details == nil {
		return nil
	}
	filled := make([]string, 0, len(details))
	for _, d := range details {
		d = strings.ReplaceAll(d, "<embedding>", strconv.Itoa(sourceLine(t, src, "gcp.Settings")))
		d = strings.ReplaceAll(d, "<beacon>", strconv.Itoa(sourceLine(t, src, "BeaconAPIKey")))
		filled = append(filled, d)
	}

	return filled
}

// sourceLine is the line the needle's first occurrence in the source ends on.
func sourceLine(t *testing.T, src, needle string) int {
	t.Helper()

	at := strings.Index(src, needle)
	if at < 0 {
		t.Fatalf("the source does not contain %q", needle)
	}

	return strings.Count(src[:at+len(needle)], "\n") + 1
}
