package check

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// TestBrandIcons holds the reminder to the skeleton's icon: a freshly rendered
// application is reminded, naming each file that is still the skeleton's and what to do
// about it; an application whose every icon is its own passes; and the reminder never
// fails the run.
func TestBrandIcons(t *testing.T) {
	t.Parallel()

	const own = "the application's own mark"
	icons := []string{"public/favicon.svg", "public/icons/icon-192.png", "public/icons/icon-512.png", "public/icons/icon-512-maskable.png"}
	tests := []struct {
		name      string
		candidate string
		// replace are the files given the application's own content, and remove the
		// files taken away, by path from the application root.
		replace     []string
		remove      []string
		wantStatus  Status
		wantSummary string
		wantDetails []string
	}{
		{
			name: "a freshly rendered application still carries the skeleton's icon", candidate: skeleton.Base,
			wantStatus: Warn, wantSummary: "1 of 1 browser application(s) still carry the skeleton's icon",
			wantDetails: []string{"web/console: logo.png, public/favicon.svg, public/icons/icon-192.png, public/icons/icon-512.png, public/icons/icon-512-maskable.png are the skeleton's: put the application's own logo where web/console/brand.json names it (a square PNG at least 512 pixels a side) and run impulse brand"},
		},
		{
			name: "a logo of its own with the icons not yet written from it", candidate: skeleton.Base,
			replace:    []string{"web/console/logo.png"},
			wantStatus: Warn, wantSummary: "1 of 1 browser application(s) still carry the skeleton's icon",
			wantDetails: []string{"web/console: public/favicon.svg, public/icons/icon-192.png, public/icons/icon-512.png, public/icons/icon-512-maskable.png are the skeleton's: put the application's own logo where web/console/brand.json names it (a square PNG at least 512 pixels a side) and run impulse brand"},
		},
		{
			name: "the skeleton's logo behind icons written again from it", candidate: skeleton.Base,
			replace: inConsole(icons[1:]), remove: []string{"web/console/public/favicon.svg"},
			wantStatus: Warn, wantSummary: "1 of 1 browser application(s) still carry the skeleton's icon",
			wantDetails: []string{"web/console: logo.png is the skeleton's: put the application's own logo where web/console/brand.json names it (a square PNG at least 512 pixels a side) and run impulse brand"},
		},
		{
			name: "every icon the application's own", candidate: skeleton.Base,
			replace:    append([]string{"web/console/logo.png"}, inConsole(icons)...),
			wantStatus: Pass, wantSummary: "1 browser application(s) carry an icon of their own",
		},
		{
			name: "an application that declares no brand is told to declare one", candidate: skeleton.Base,
			remove: []string{"web/console/brand.json"}, replace: inConsole(icons[1:]),
			wantStatus: Warn, wantSummary: "1 of 1 browser application(s) still carry the skeleton's icon",
			wantDetails: []string{"web/console: public/favicon.svg is the skeleton's: declare its brand in web/console/brand.json (a name, a square PNG logo at least 512 pixels a side, a primary color) and run impulse brand"},
		},
		{
			name: "one of two browser applications still carries it", candidate: "outlets",
			replace:    append([]string{"web/console/logo.png"}, inConsole(icons)...),
			wantStatus: Warn, wantSummary: "1 of 2 browser application(s) still carry the skeleton's icon",
			wantDetails: []string{"web/portal: logo.png, public/favicon.svg, public/icons/icon-192.png, public/icons/icon-512.png, public/icons/icon-512-maskable.png are the skeleton's: put the application's own logo where web/portal/brand.json names it (a square PNG at least 512 pixels a side) and run impulse brand"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "beacon")
			if _, err := skeleton.Render(&skeleton.Options{Version: "v0.1.0", WebVersion: "0.1.0", Candidate: tt.candidate, Dir: dir, ModulePath: "example.com/acme/beacon"}); err != nil {
				t.Fatalf("skeleton.Render() error = %v", err)
			}
			for _, rel := range tt.replace {
				if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(own), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, rel := range tt.remove {
				if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
					t.Fatal(err)
				}
			}
			a, err := app.Discover(dir)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}

			got := brandIcons{}.Run(context.Background(), &Env{App: a})
			if got.Status != tt.wantStatus {
				t.Fatalf("Status = %s, want %s (%s: %v)", got.Status, tt.wantStatus, got.Summary, got.Details)
			}
			if got.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", got.Summary, tt.wantSummary)
			}
			if diff := cmp.Diff(tt.wantDetails, got.Details); diff != "" {
				t.Errorf("Details mismatch (-want +got):\n%s", diff)
			}
			if Failed([]Result{got}) {
				t.Errorf("the reminder failed the run: %s", got.Summary)
			}
		})
	}
}

// inConsole puts the console's directory before each path.
func inConsole(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, "web/console/"+f)
	}

	return out
}

// TestBrandIconsWithoutBrowserApps holds the reminder to saying nothing of an
// application with no browser application.
func TestBrandIconsWithoutBrowserApps(t *testing.T) {
	t.Parallel()

	got := brandIcons{}.Run(context.Background(), &Env{App: &app.App{Root: t.TempDir()}})
	if got.Status != Skip || got.Summary != "no browser apps" {
		t.Errorf("Run() = %s %q, want SKIP no browser apps", got.Status, got.Summary)
	}
}
