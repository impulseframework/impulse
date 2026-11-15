package skeleton

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
)

// TestBrowserProcessesRunWithoutPORT holds the development process files of Lodestar and
// of every skeleton candidate to one rule: a process that starts a browser application's
// development server hands the proxy its target as API_PORT and takes PORT out of its
// own environment. Angular's development server takes PORT for its own port, over
// angular.json and the command line; with the API server's PORT inherited it asks for a
// port the server already holds and waits at a question nobody sees, so the browser
// application is never served. No other test starts a development server.
func TestBrowserProcessesRunWithoutPORT(t *testing.T) {
	t.Parallel()

	roots := []string{"lodestar"}
	candidates, err := Candidates()
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	for _, candidate := range candidates {
		roots = append(roots, filepath.ToSlash(filepath.Join(candidatesDir, candidate.Name)))
	}

	// What is read is named, so a process or a proxy that moves fails here and the rule
	// never passes for having nothing to read.
	wantProcesses := map[string][]string{
		"lodestar":                  {"console", "portal"},
		candidatesDir + "/outlets":  {"console", "portal"},
		candidatesDir + "/sites":    {"console-web", "portal-web"},
		candidatesDir + "/solo":     {"console"},
		candidatesDir + "/tenanted": {"console"},
	}
	wantProxies := map[string][]string{
		"lodestar":                  {"web/console/proxy.conf.js", "web/portal/proxy.conf.js"},
		candidatesDir + "/outlets":  {"web/console/proxy.conf.js", "web/portal/proxy.conf.js"},
		candidatesDir + "/sites":    {"apps/console/web/proxy.conf.js", "apps/portal/web/proxy.conf.js"},
		candidatesDir + "/solo":     {"web/console/proxy.conf.js"},
		candidatesDir + "/tenanted": {"web/console/proxy.conf.js"},
	}

	gotProcesses, gotProxies := map[string][]string{}, map[string][]string{}
	for _, root := range roots {
		dir := filepath.Join(repositoryRoot(), filepath.FromSlash(root))

		raw, err := os.ReadFile(filepath.Join(dir, "Procfile"))
		if err != nil {
			t.Fatalf("os.ReadFile() error = %v", err)
		}
		for line := range strings.Lines(string(raw)) {
			name, command, ok := strings.Cut(line, ":")
			if !ok || strings.HasPrefix(line, "#") || !strings.Contains(command, "bun run start") {
				continue
			}
			gotProcesses[root] = append(gotProcesses[root], name)
			if problem := browserStartProblem(command); problem != "" {
				t.Errorf("%s/Procfile, process %s: %s", root, name, problem)
			}
		}

		// The tree is walked and read through one file system rooted at the directory, so
		// every path is the proxy's path under it.
		tree := os.DirFS(dir)
		err = fs.WalkDir(tree, ".", func(rel string, d fs.DirEntry, err error) error {
			if err != nil {
				return errors.Wrap(err, "fs.WalkDir()")
			}
			if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "dist") {
				return fs.SkipDir
			}
			if d.Name() != "proxy.conf.js" {
				return nil
			}
			gotProxies[root] = append(gotProxies[root], rel)
			conf, err := fs.ReadFile(tree, rel)
			if err != nil {
				return errors.Wrapf(err, "fs.ReadFile(): %s", rel)
			}
			if !strings.Contains(string(conf), "process.env.API_PORT") || strings.Contains(string(conf), "process.env.PORT") {
				t.Errorf("%s/%s: the proxy's target is read from process.env.API_PORT, never from PORT, which the browser process runs without", root, rel)
			}

			return nil
		})
		if err != nil {
			t.Fatalf("fs.WalkDir(%s) error = %v", root, err)
		}
		slices.Sort(gotProxies[root])
	}

	if diff := cmp.Diff(wantProcesses, gotProcesses); diff != "" {
		t.Errorf("the processes that start a development server (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantProxies, gotProxies); diff != "" {
		t.Errorf("the proxies read (-want +got):\n%s", diff)
	}
}

// browserStartProblem says what is wrong with a process that starts a development
// server, or nothing: the start runs under `env -u PORT`, and is handed API_PORT.
func browserStartProblem(command string) string {
	before, _, _ := strings.Cut(command, "bun run start")
	switch {
	case !strings.HasSuffix(before, "env -u PORT "):
		return "the development server starts with PORT in its environment: run it as `env -u PORT bun run start…`, since Angular's development server takes PORT for its own port"
	case !strings.Contains(before, " API_PORT="):
		return "the process hands the proxy no target: set API_PORT to the API server's port before `env -u PORT`"
	default:
		return ""
	}
}

// Test_browserStartProblem holds the rule to what it refuses.
func Test_browserStartProblem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "the skeletons' start", command: " bash -c 'cd web && bun install && API_PORT=${PORT} env -u PORT bun run start:console'"},
		{name: "a site's start", command: " bash -c 'cd apps/console/web && API_PORT=8094 env -u PORT bun run start'"},
		{name: "a start that inherits PORT is refused", command: " bash -c 'cd web && bun install && bun run start:console'", want: "starts with PORT in its environment"},
		{name: "a start given PORT is refused", command: " bash -c 'cd apps/console/web && PORT=8094 bun run start'", want: "starts with PORT in its environment"},
		{name: "a start that hands the proxy nothing is refused", command: " bash -c 'cd web && env -u PORT bun run start:console'", want: "hands the proxy no target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := browserStartProblem(tt.command)
			if (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
				t.Errorf("browserStartProblem() = %q, want %q", got, tt.want)
			}
		})
	}
}
