package check

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/mod/modfile"

	"github.com/impulseframework/impulse/app"
)

// TestPackageManager_noLockfile holds an application none of whose browser apps has a
// lockfile: its workflows install from one, so the check fails and names the install
// that writes it, in the words of the package manager the application invokes; it
// passes once the lockfile is there. An application built against a framework checkout
// is skipped, since its browser packages are linked from the checkout.
func TestPackageManager_noLockfile(t *testing.T) {
	t.Parallel()

	const released = "module example.com/acme/beacon\n\ngo 1.27\n\nrequire " + FrameworkModule + " v0.5.0\n"
	tests := []struct {
		name        string
		goMod       string
		files       map[string]string
		webs        []string
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name:  "an application as impulse new renders it: no lockfile, bun in the Procfile and the scripts",
			goMod: released,
			files: map[string]string{
				"Procfile":         "server: go run .\nweb: cd web && bun run start\n",
				"web/package.json": `{"scripts": {"start": "ng serve", "lint": "bunx eslint ."}}`,
			},
			webs:        []string{"web"},
			wantStatus:  Fail,
			wantSummary: "1 browser app(s) have no lockfile, and the workflows install from one: run bun install in each web root, which writes the bun.lock the repository keeps",
			wantDetails: []string{"web: no lockfile"},
		},
		{
			name:  "two sites, neither installed",
			goMod: released,
			files: map[string]string{
				"Procfile":                      "console: cd apps/console/web && bun run start\nportal: cd apps/portal/web && bun run start\n",
				"apps/console/web/package.json": `{"scripts": {"start": "ng serve"}}`,
				"apps/portal/web/package.json":  `{"scripts": {"start": "ng serve"}}`,
			},
			webs:        []string{"apps/console/web", "apps/portal/web"},
			wantStatus:  Fail,
			wantSummary: "2 browser app(s) have no lockfile, and the workflows install from one: run bun install in each web root, which writes the bun.lock the repository keeps",
			wantDetails: []string{"apps/console/web: no lockfile", "apps/portal/web: no lockfile"},
		},
		{
			name:  "an application that names no package manager is told to use its own",
			goMod: released,
			files: map[string]string{
				"Procfile":         "server: go run .\n",
				"web/package.json": `{"scripts": {"start": "ng serve"}}`,
			},
			webs:        []string{"web"},
			wantStatus:  Fail,
			wantSummary: "1 browser app(s) have no lockfile, and the workflows install from one: install each web root's packages with the application's package manager, which writes the lockfile the repository keeps",
			wantDetails: []string{"web: no lockfile"},
		},
		{
			name:  "the lockfile the install wrote",
			goMod: released,
			files: map[string]string{
				"Procfile":         "server: go run .\nweb: cd web && bun run start\n",
				"web/package.json": `{"scripts": {"start": "ng serve"}}`,
				"web/bun.lock":     "{}\n",
			},
			webs:        []string{"web"},
			wantStatus:  Pass,
			wantSummary: "1 browser app(s) on bun",
		},
		{
			name:  "an application built against a framework checkout links its browser packages from it",
			goMod: released + "\nreplace " + FrameworkModule + " => ../impulse\n",
			files: map[string]string{
				"Procfile":         "server: go run .\nweb: cd web && bun run start\n",
				"web/package.json": `{"scripts": {"start": "ng serve"}}`,
			},
			webs:        []string{"web"},
			wantStatus:  Skip,
			wantSummary: "no browser app has a lockfile; go.mod replaces the framework module with the checkout at ../impulse, whose browser packages the web roots link (weblib.sh local)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for rel, content := range tt.files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			mod, err := modfile.Parse("go.mod", []byte(tt.goMod), nil)
			if err != nil {
				t.Fatalf("modfile.Parse() error = %v", err)
			}
			a := &app.App{Root: root, GoMod: mod}
			for _, dir := range tt.webs {
				a.WebApps = append(a.WebApps, app.WebApp{Dir: dir})
			}

			got := packageManager{}.Run(context.Background(), &Env{App: a})
			want := Result{Name: "package-manager", Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
