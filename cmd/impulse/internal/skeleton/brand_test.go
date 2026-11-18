package skeleton

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/brand"
)

// noPackages stands in for the commands impulse brand runs in a workspace whose packages
// are installed: a rendered candidate has none, so the command runs nothing, and a
// command run here is the test's failure.
type noPackages struct {
	t *testing.T
}

func (n noPackages) Run(_ context.Context, dir string, _ []string, name string, _ ...string) ([]byte, error) {
	n.t.Fatalf("impulse brand ran %s in %s, where no packages are installed", name, dir)

	return nil, nil
}

// candidateBrowserApps are the browser applications each candidate carries, by directory,
// each with a brand declaration and the seed's logo beside it.
var candidateBrowserApps = map[string][]string{
	"solo":     {"web/console"},
	"tenanted": {"web/console"},
	"outlets":  {"web/console", "web/portal"},
	"sites":    {"apps/console/web", "apps/portal/web"},
}

// TestCandidateBrands holds every candidate to its own brand declarations: the package
// the server says its brands from (pkg/brand) is what impulse brand writes from the
// brand.json of the candidate's browser applications, and the name each auth package's
// mail says is the one the command writes, so a rendered application starts with its
// server saying what its browser applications declare, and the command's first run over
// it changes nothing there. Run with IMPULSE_UPDATE_BRAND=1 to rewrite the candidates'
// pkg/brand after changing a declaration, a seed logo, or what the command writes.
func TestCandidateBrands(t *testing.T) {
	t.Parallel()
	update := os.Getenv("IMPULSE_UPDATE_BRAND") != ""

	for candidate, roots := range candidateBrowserApps {
		t.Run(candidate, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), candidate)
			if _, err := Render(&Options{Version: "v0.1.0", WebVersion: "0.1.0", Candidate: candidate, Dir: dir, ModulePath: modulePrefix + candidate}); err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			a, err := app.Discover(dir)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}
			before := goFiles(t, dir, "pkg/auth")
			result, err := brand.Apply(context.Background(), a, noPackages{t: t})
			if err != nil {
				t.Fatalf("brand.Apply() error = %v", err)
			}
			if len(result.Apps) != len(roots) || len(result.Undeclared) != 0 {
				t.Fatalf("brand.Apply() wrote %d browser application(s) and found %v without a declaration, want the candidate's %d declared", len(result.Apps), result.Undeclared, len(roots))
			}
			for i, root := range roots {
				if result.Apps[i].Dir != root {
					t.Errorf("browser application %d = %s, want %s", i, result.Apps[i].Dir, root)
				}
			}
			if len(result.Unserved) != 0 {
				t.Errorf("the candidate's mains hand the maintenance page every brand but: %v", result.Unserved)
			}
			// The candidate carries every place the command writes a brand in: nothing is
			// left but the palette, whose generator comes with the packages.
			writes := result.Server
			for i := range result.Apps {
				writes = append(writes, result.Apps[i].Writes...)
			}
			for _, w := range writes {
				if w.State == brand.Left && path.Base(w.File) != "_theme-colors.scss" {
					t.Errorf("%s: impulse brand found no place to write in %s: %s", candidate, w.File, w.Note)
				}
			}

			// The auth packages say the names the declarations declare already.
			for file, want := range before {
				got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s: impulse brand changed %s: the Application constant the candidate carries is not the name its browser application declares", candidate, file)
				}
			}

			// The server's package is what the command writes.
			written, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(brand.ServerPackage)))
			if err != nil {
				t.Fatal(err)
			}
			kept := filepath.Join("_candidates", candidate, filepath.FromSlash(brand.ServerPackage))
			for _, entry := range written {
				want, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(brand.ServerPackage), entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(kept, entry.Name())
				if update {
					if err := os.MkdirAll(kept, 0o750); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(file, want, 0o600); err != nil {
						t.Fatal(err)
					}

					continue
				}
				got, err := os.ReadFile(file)
				if err != nil {
					t.Errorf("%s: %v (run with IMPULSE_UPDATE_BRAND=1 to write the candidates' %s)", candidate, err, brand.ServerPackage)

					continue
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s: %s differs from what impulse brand writes from the candidate's declarations (run with IMPULSE_UPDATE_BRAND=1 to rewrite it)", candidate, file)
				}
			}
			carried, err := os.ReadDir(kept)
			if err != nil && !update {
				t.Fatalf("%s: %v", candidate, err)
			}
			if len(carried) != len(written) && !update {
				t.Errorf("%s: %s holds %d file(s) and impulse brand writes %d", candidate, kept, len(carried), len(written))
			}
		})
	}
}

// goFiles reads the Go files under a directory of the rendered tree, by their path from
// the tree's root.
func goFiles(t *testing.T, root, dir string) map[string][]byte {
	t.Helper()

	files := map[string][]byte{}
	tree := os.DirFS(root)
	err := fs.WalkDir(tree, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "fs.WalkDir()")
		}
		if d.IsDir() || path.Ext(p) != goExt {
			return nil
		}
		data, err := fs.ReadFile(tree, p)
		if err != nil {
			return errors.Wrap(err, "fs.ReadFile()")
		}
		files[p] = data

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return files
}

// TestSeedIcons holds the seed's icons the brand check looks for to the icons the
// candidates carry: the favicon, the three app icons and the logo of every browser
// application are the placeholder mark the check reminds an application to replace. A
// candidate whose icon is redrawn fails here until the check knows the new one.
func TestSeedIcons(t *testing.T) {
	t.Parallel()

	for candidate, roots := range candidateBrowserApps {
		for _, root := range roots {
			for _, file := range []string{"logo.png", "public/favicon.svg", "public/icons/icon-192.png", "public/icons/icon-512.png", "public/icons/icon-512-maskable.png"} {
				rel := path.Join(dir, candidate, root, file)
				data, err := fs.ReadFile(skeletonRoot(), rel)
				if err != nil {
					t.Errorf("%s: %v", rel, err)

					continue
				}
				if !brand.IsSeedIcon(data) {
					t.Errorf("%s is not an icon the brand check knows as the seed's: add its SHA-256 to the brand package's seedIcons, and take out the one it replaces", rel)
				}
			}
		}
	}
}

// TestCandidateWorkersKeepTheFavicon holds every candidate's service worker to keeping
// the favicon with the application's shell, whichever one impulse brand writes: the files
// its configuration fetches when the worker installs name both the PNG favicon, which the
// command always writes, and the SVG one, which it writes where the brand has one. A
// configuration naming the skeleton's favicon.svg alone would leave the brand's favicon
// out of the shell once the command took the skeleton's away.
func TestCandidateWorkersKeepTheFavicon(t *testing.T) {
	t.Parallel()

	candidates, err := Candidates()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		roots, err := candidateWebRoots(candidate.Name)
		if err != nil {
			t.Fatal(err)
		}
		for root := range roots {
			file := path.Join(dir, candidate.Name, root, "ngsw-config.json")
			data, err := fs.ReadFile(skeletonRoot(), file)
			if err != nil {
				t.Errorf("%s: %v", file, err)

				continue
			}
			var config struct {
				AssetGroups []struct {
					InstallMode string `json:"installMode"`
					Resources   struct {
						Files []string `json:"files"`
					} `json:"resources"`
				} `json:"assetGroups"`
			}
			if err := json.Unmarshal(data, &config); err != nil {
				t.Errorf("%s: %v", file, err)

				continue
			}
			for _, favicon := range []string{"/favicon.png", "/favicon.svg"} {
				kept := false
				for _, group := range config.AssetGroups {
					if group.InstallMode != "prefetch" {
						continue
					}
					for _, pattern := range group.Resources.Files {
						if ok, err := path.Match(pattern, favicon); err == nil && ok {
							kept = true
						}
					}
				}
				if !kept {
					t.Errorf("%s: no group the worker fetches at install names %s, the favicon impulse brand writes: name it there (\"/favicon.*\" names both)", file, favicon)
				}
			}
		}
	}
}

// skeletonRoot is the embedded template tree.
func skeletonRoot() fs.FS {
	return root
}
