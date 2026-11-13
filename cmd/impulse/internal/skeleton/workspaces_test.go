package skeleton

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
)

// candidatesDir is this package's directory of skeleton candidates, from the repository root.
const candidatesDir = "cmd/impulse/internal/skeleton/_candidates"

// ownPackages is the scope of the library's own browser packages. A workspace names them
// by a link to their builds (Lodestar) or by the placeholder the render fills (a
// candidate), and the ledger's web pins hold their versions, so they are left out here.
const ownPackages = "@impulseframework/"

// repositoryRoot returns the repository root as a path from this package's directory,
// which is where go test runs the package's tests.
func repositoryRoot() string {
	return filepath.Join("..", "..", "..", "..")
}

// TestWebWorkspacesPinTheSameVersions holds the repository's web workspaces to one version
// of every third-party package two of them name: the library workspace (web), Lodestar's
// (lodestar/web), which links the library's builds, and every web root of the skeleton
// candidates, which is what a new application starts from. Dependabot moves the library
// workspace alone, and a library tested on one version beside an application installed on
// another proves nothing about either, so a pull request that moves a version in one
// workspace moves it in all of them. The versions the published packages ask of an
// application (web/projects/*/package.json) are ranges an application chooses within, and
// are not compared.
func TestWebWorkspacesPinTheSameVersions(t *testing.T) {
	t.Parallel()

	workspaces := map[string]map[string]string{}
	for _, dir := range []string{"web", "lodestar/web"} {
		raw, err := os.ReadFile(filepath.Join(repositoryRoot(), filepath.FromSlash(dir), "package.json"))
		if err != nil {
			t.Fatalf("os.ReadFile() error = %v", err)
		}
		pins, err := workspacePins(raw)
		if err != nil {
			t.Fatalf("%s/package.json: %v", dir, err)
		}
		workspaces[dir] = pins
	}

	candidates, err := Candidates()
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	for _, candidate := range candidates {
		roots, err := candidateWebRoots(candidate.Name)
		if err != nil {
			t.Fatalf("candidateWebRoots(%s) error = %v", candidate.Name, err)
		}
		for dir, pins := range roots {
			workspaces[path.Join(candidatesDir, candidate.Name, dir)] = pins
		}
	}

	// The workspaces compared are named, so a web root that is added, moved or no longer
	// found fails here and the comparison below never passes for having less to compare.
	want := []string{
		candidatesDir + "/outlets/web",
		candidatesDir + "/sites/apps/console/web",
		candidatesDir + "/sites/apps/portal/web",
		candidatesDir + "/solo/web",
		candidatesDir + "/tenanted/web",
		"lodestar/web",
		"web",
	}
	got := make([]string, 0, len(workspaces))
	for dir := range workspaces {
		got = append(got, dir)
	}
	sort.Strings(got)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("the web workspaces compared mismatch (-want +got):\n%s", diff)
	}

	differences := pinDifferences(workspaces)
	for _, difference := range differences {
		t.Error(difference)
	}
	if len(differences) > 0 {
		t.Error("the web workspaces move together: give each package above the same version in every workspace's package.json, in the same pull request, and install in web and lodestar/web so their lock files follow")
	}
}

func TestPinDifferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		workspaces map[string]map[string]string
		want       []string
	}{
		{
			name: "every shared package at one version",
			workspaces: map[string]map[string]string{
				"web":          {"eslint": "^10.14.0", "ng-packagr": "^22.2.6"},
				"lodestar/web": {"eslint": "^10.14.0", "firebase": "^13.2.0"},
			},
		},
		{
			name: "one workspace left behind",
			workspaces: map[string]map[string]string{
				"web":          {"eslint": "^10.14.0", "vitest": "^5.1.0"},
				"lodestar/web": {"eslint": "^9.39.2", "vitest": "^5.1.0"},
				"solo/web":     {"eslint": "^9.39.2", "vitest": "^5.1.0"},
			},
			want: []string{
				"eslint is pinned at 2 versions:\n\t^10.14.0 in web/package.json\n\t^9.39.2 in lodestar/web/package.json\n\t^9.39.2 in solo/web/package.json",
			},
		},
		{
			name: "the same version written two ways is a difference",
			workspaces: map[string]map[string]string{
				"web":          {"prettier": "3.9.10"},
				"lodestar/web": {"prettier": "^3.9.10"},
			},
			want: []string{"prettier is pinned at 2 versions:\n\t3.9.10 in web/package.json\n\t^3.9.10 in lodestar/web/package.json"},
		},
		{
			name: "three versions of one package, and a second package, in name order",
			workspaces: map[string]map[string]string{
				"web":          {"jsdom": "^31.0.0", "eslint": "^10.14.0"},
				"lodestar/web": {"jsdom": "^27.1.0", "eslint": "^9.39.2"},
				"solo/web":     {"jsdom": "^30.0.0", "eslint": "^10.14.0"},
			},
			want: []string{
				"eslint is pinned at 2 versions:\n\t^10.14.0 in solo/web/package.json\n\t^10.14.0 in web/package.json\n\t^9.39.2 in lodestar/web/package.json",
				"jsdom is pinned at 3 versions:\n\t^27.1.0 in lodestar/web/package.json\n\t^30.0.0 in solo/web/package.json\n\t^31.0.0 in web/package.json",
			},
		},
		{
			name: "the library's own packages are not compared",
			workspaces: map[string]map[string]string{
				"lodestar/web": {"@impulseframework/resource": "link:../../web/dist/resource"},
				"solo/web":     {"@impulseframework/resource": "0.0.0-skeleton"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, pinDifferences(tt.workspaces)); diff != "" {
				t.Errorf("pinDifferences() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWorkspacePins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "every section that pins an install is read",
			raw:  `{"dependencies": {"rxjs": "~7.8.2"}, "devDependencies": {"eslint": "^10.14.0"}, "peerDependencies": {"date-fns": "^4.1.0"}, "optionalDependencies": {"jsdom": "^31.0.0"}, "overrides": {"tslib": "^2.8.1"}}`,
			want: map[string]string{"rxjs": "~7.8.2", "eslint": "^10.14.0", "date-fns": "^4.1.0", "jsdom": "^31.0.0"},
		},
		{
			name: "a package named twice at one version",
			raw:  `{"dependencies": {"tslib": "^2.8.1"}, "peerDependencies": {"tslib": "^2.8.1"}}`,
			want: map[string]string{"tslib": "^2.8.1"},
		},
		{
			name:    "a package named twice at two versions",
			raw:     `{"dependencies": {"tslib": "^2.8.1"}, "peerDependencies": {"tslib": "^2.3.0"}}`,
			wantErr: true,
		},
		{
			name:    "not a package.json",
			raw:     `[]`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := workspacePins([]byte(tt.raw))
			if (err != nil) != tt.wantErr {
				t.Fatalf("workspacePins() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("workspacePins() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// candidateWebRoots returns the pins of every web root of a candidate, by the root's
// directory in the candidate. A web root is a directory holding angular.json.
func candidateWebRoots(candidate string) (map[string]map[string]string, error) {
	sub, err := FS(candidate)
	if err != nil {
		return nil, err
	}

	roots := map[string]map[string]string{}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "angular.json" {
			return nil
		}
		dir := path.Dir(p)
		raw, err := fs.ReadFile(sub, path.Join(dir, "package.json"))
		if err != nil {
			return errors.Wrap(err, "fs.ReadFile()")
		}
		pins, err := workspacePins(raw)
		if err != nil {
			return errors.Wrapf(err, "%s/package.json", dir)
		}
		roots[dir] = pins

		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "fs.WalkDir()")
	}

	return roots, nil
}

// workspacePins returns the version a workspace's package.json gives each package it
// installs, over the sections that pin an install. A package the file names in two
// sections at two versions is an error: the workspace then has no one version of it.
func workspacePins(raw []byte) (map[string]string, error) {
	var file struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, errors.Wrap(err, "json.Unmarshal()")
	}

	pins := map[string]string{}
	for _, section := range []map[string]string{file.Dependencies, file.DevDependencies, file.PeerDependencies, file.OptionalDependencies} {
		for name, version := range section {
			if other, ok := pins[name]; ok && other != version {
				return nil, errors.Newf("%s is named at %s and at %s", name, other, version)
			}
			pins[name] = version
		}
	}

	return pins, nil
}

// pinDifferences returns one entry for each third-party package that two or more of the
// workspaces name at different versions, in package order: the package, then a line for
// each workspace's package.json with the version it gives, in version order. A version is
// compared as written, so 3.9.10 and ^3.9.10 differ. A package one workspace alone names
// is nobody's difference.
func pinDifferences(workspaces map[string]map[string]string) []string {
	// byPackage is package → version → the workspaces that pin the package at it.
	byPackage := map[string]map[string][]string{}
	for dir, pins := range workspaces {
		for name, version := range pins {
			if strings.HasPrefix(name, ownPackages) {
				continue
			}
			if byPackage[name] == nil {
				byPackage[name] = map[string][]string{}
			}
			byPackage[name][version] = append(byPackage[name][version], dir)
		}
	}

	var differences []string
	for _, name := range slices.Sorted(maps.Keys(byPackage)) {
		versions := byPackage[name]
		if len(versions) < 2 {
			continue
		}
		difference := fmt.Sprintf("%s is pinned at %d versions:", name, len(versions))
		for _, version := range slices.Sorted(maps.Keys(versions)) {
			dirs := versions[version]
			sort.Strings(dirs)
			for _, dir := range dirs {
				difference += fmt.Sprintf("\n\t%s in %s/package.json", version, dir)
			}
		}
		differences = append(differences, difference)
	}

	return differences
}
