package generate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestWebStylesheetsCarryNoLiteralColor holds what keeps Lodestar's pages readable when a
// person picks Dark: a component stylesheet of the console or the portal draws with the
// Material theme's tokens (var(--mat-sys-...)) and Lodestar's own signal colors
// (var(--lodestar-...)), which each application's theme.scss sets in one place, each that
// colors text or a line as a light-dark pair. A literal color keeps its value in the dark
// theme: a white card under the theme's light text, a dark red on a dark card. A literal
// is allowed as the fallback of a Material token, which applies only on a page with no
// theme at all. Needs no emulator.
func TestWebStylesheetsCarryNoLiteralColor(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	web := filepath.Join(filepath.Dir(thisFile), "..", "..", "web")
	literal := regexp.MustCompile(`(?i)#[0-9a-f]{3,8}\b|\b(?:rgba?|hsla?)\(|:\s*(?:white|black|red|green|blue|gray|grey|lightgray|lightgrey)\s*[;!]`)
	fallback := regexp.MustCompile(`var\(--mat-[a-z0-9-]+,\s*[^)]*\)`)
	blockComment := regexp.MustCompile(`/\*.*?\*/`)

	var sheets, offenders []string
	for _, app := range []string{"console", "portal"} {
		root := filepath.Join(web, app, "src", "app")
		tree := os.DirFS(root)
		err := fs.WalkDir(tree, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".scss") {
				return nil
			}
			sheets = append(sheets, path)
			text, err := fs.ReadFile(tree, path)
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}
			for i, line := range strings.Split(string(text), "\n") {
				code := blockComment.ReplaceAllString(line, "")
				if at := strings.Index(code, "//"); at >= 0 {
					code = code[:at]
				}
				code = fallback.ReplaceAllString(code, "")
				if literal.MatchString(code) {
					offenders = append(offenders, fmt.Sprintf("%s/src/app/%s:%d: %s", app, path, i+1, strings.TrimSpace(line)))
				}
			}

			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if len(sheets) == 0 {
		t.Fatalf("no component stylesheet found under %s: the test reads nothing", web)
	}
	if len(offenders) > 0 {
		t.Errorf("%d literal colors in component stylesheets; draw with var(--mat-sys-...) or a --lodestar-... color the application's theme.scss sets:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}
