package skeleton

import (
	"encoding/base64"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The two lines every development environment template gives the application secret's
// first version its value with, and the file the value is kept in.
const (
	developmentKeyFile = ".app-secret-key.dev"
	developmentKeyMake = "[ -s .app-secret-key.dev ] || (umask 077 && head -c 32 /dev/urandom | base64 > .app-secret-key.dev)"
	developmentKeyRead = `export APP_SECRET_KEY_1="$(cat .app-secret-key.dev)"`
)

// keyConstantRE finds a key written out: a version of the application secret, or the
// name itself, assigned a run of Base64.
var keyConstantRE = regexp.MustCompile(`APP_SECRET_KEY(_[0-9]+)?=["']?[A-Za-z0-9+/]{16,}`)

// TestDevelopmentSecretKeyIsMade holds the development value of the application secret
// to a key made for the checkout, in every candidate and in Lodestar. A key written in a
// template is the key of every application made from it, and no secret; the template
// makes one instead, kept in a file .gitignore ignores. Sourced as a checkout and the
// check scripts source it, from the application's root, the template sets the variable to
// 32 random bytes in Base64, the same every time, since a changed key leaves the
// development database's secrets unreadable; two checkouts get two keys; and neither the
// template nor the README holds a key.
func TestDevelopmentSecretKeyIsMade(t *testing.T) {
	t.Parallel()

	candidates, err := Candidates()
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	applications := map[string]fs.FS{"lodestar": os.DirFS(filepath.Join(repositoryRoot(), "lodestar"))}
	for _, candidate := range candidates {
		sub, err := FS(candidate.Name)
		if err != nil {
			t.Fatalf("FS() error = %v", err)
		}
		applications[candidate.Name] = sub
	}

	for name, application := range applications {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			read := func(file string) string {
				t.Helper()

				data, err := fs.ReadFile(application, file)
				if err != nil {
					t.Fatalf("fs.ReadFile() error = %v", err)
				}

				return string(data)
			}
			template := read(".envrc.template")
			if !strings.Contains(template, "\n"+developmentKeyMake+"\n"+developmentKeyRead+"\n") {
				t.Fatalf(".envrc.template does not make the application secret's first version for the checkout; it carries the lines\n%s\n%s", developmentKeyMake, developmentKeyRead)
			}
			for _, file := range []string{".envrc.template", "README.md"} {
				if found := keyConstantRE.FindString(read(file)); found != "" {
					t.Errorf("%s holds a key (%s...): every application made from it would encrypt under that one key", file, found[:strings.Index(found, "=")+1])
				}
			}
			ignored := false
			for line := range strings.Lines(read(".gitignore")) {
				ignored = ignored || strings.TrimSpace(line) == developmentKeyFile
			}
			if !ignored {
				t.Errorf(".gitignore does not ignore %s: the checkout's key would be committed", developmentKeyFile)
			}

			source := func(dir string) string {
				t.Helper()

				if err := os.WriteFile(filepath.Join(dir, ".envrc.template"), []byte(template), 0o600); err != nil {
					t.Fatalf("os.WriteFile() error = %v", err)
				}
				cmd := exec.CommandContext(t.Context(), "sh", "-c", `set -a; . ./.envrc.template; set +a; printf %s "$APP_SECRET_KEY_1"`)
				cmd.Dir = dir
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("sourcing .envrc.template: %v", err)
				}

				return string(out)
			}
			checkout, another := t.TempDir(), t.TempDir()
			first, again, elsewhere := source(checkout), source(checkout), source(another)
			if key, err := base64.StdEncoding.DecodeString(first); err != nil || len(key) != 32 {
				t.Fatalf(".envrc.template sets APP_SECRET_KEY_1 to %d byte(s), error %v; want 32 random bytes in Base64", len(key), err)
			}
			if first != again {
				t.Error("sourced a second time in one checkout, the template made another key: the secrets the development database holds would no longer open")
			}
			if first == elsewhere {
				t.Error("two checkouts got the same key")
			}
			info, err := os.Stat(filepath.Join(checkout, developmentKeyFile))
			if err != nil {
				t.Fatalf("the key file: %v", err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("the key file's mode is %v, want only its owner to read it", info.Mode().Perm())
			}
		})
	}
}
