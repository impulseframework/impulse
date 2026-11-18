package brand

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// Result is what impulse brand did.
type Result struct {
	// Apps are the browser applications a brand was written for, in directory order.
	Apps []AppResult
	// Undeclared are the browser applications that declare no brand, by directory, for
	// which nothing was written.
	Undeclared []string
	// Server is what the command did with the files the server says the brands from:
	// the server's package and the auth packages' names.
	Server []Write
	// Unserved are the brands no main hands to the maintenance page yet, each as the
	// sentence that says what to add.
	Unserved []string
}

// AppResult is what the command did for one browser application.
type AppResult struct {
	// Dir is the browser application's directory, and Brand the brand it declares, as
	// the report says it.
	Dir, Brand string
	// Writes are the browser application's files, in the order they were written.
	Writes []Write
	// Palette reports whether the theme's palette of the brand's primary color is there:
	// false where the generator could not be run, which the palette file's own line
	// explains.
	Palette bool
}

// Incomplete lists the browser applications whose palette could not be generated: everything else
// of their brand is written, and the theme still carries the palette it had.
func (r *Result) Incomplete() []string {
	var dirs []string
	for i := range r.Apps {
		if !r.Apps[i].Palette {
			dirs = append(dirs, r.Apps[i].Dir)
		}
	}

	return dirs
}

// Apply writes the brand of every browser application that declares one, and what the
// server says of them. Every declaration and logo is read and held to its shape before
// anything is written, so a refusal leaves the tree as it was. exec runs the palette's
// generator in the workspaces whose packages are installed.
func Apply(ctx context.Context, a *app.App, exec Execer) (*Result, error) {
	roots, err := BrowserApps(a)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return nil, errors.Newf("%s has no browser application to brand: one is an application project of a directory holding angular.json", a.Root)
	}
	result := &Result{}
	var brands []*declared
	for i := range roots {
		d, err := readDeclared(a, &roots[i])
		if err != nil {
			return nil, err
		}
		if d == nil {
			result.Undeclared = append(result.Undeclared, roots[i].Dir)

			continue
		}
		brands = append(brands, d)
	}
	if len(brands) == 0 {
		return nil, errors.Newf("no browser application declares a brand: write %s beside a square PNG logo at least %d pixels a side, such as %s (\"backgroundColor\" and \"logoSvg\" are optional), and run impulse brand again", roots[0].DeclarationPath(), MinLogoSize, example)
	}

	for _, r := range brands {
		w := &writer{a: a}
		if err := w.icons(r); err != nil {
			return nil, err
		}
		if err := w.pages(r); err != nil {
			return nil, err
		}
		generated, err := w.palette(ctx, exec, r)
		if err != nil {
			return nil, err
		}
		if generated {
			if err := w.theme(r); err != nil {
				return nil, err
			}
		}
		result.Apps = append(result.Apps, AppResult{Dir: r.Dir, Brand: r.declaration.say(), Writes: w.result(), Palette: generated})
	}

	w := &writer{a: a}
	if err := w.server(brands); err != nil {
		return nil, err
	}
	if err := w.mailNames(brands); err != nil {
		return nil, err
	}
	result.Server = w.result()
	missing, err := unserved(a, brands)
	if err != nil {
		return nil, err
	}
	for _, r := range missing {
		result.Unserved = append(result.Unserved, fmt.Sprintf("no main hands the maintenance page the brand of %s: add it to the brands the main serving it gives maintenance.Serve, under its mount path (maintenance.Brands{\"/%s\": %s.%s()}, or \"/\" for an application at the root)", r.Dir, r.Project, path.Base(ServerPackage), identifier(r.Project)))
	}

	return result, nil
}

// result is what the writer did, one line per file and one per place it could not write:
// a file edited twice is said once, as written where either edit wrote it.
func (w *writer) result() []Write {
	var out []Write
	at := map[string]int{}
	for _, write := range w.writes {
		if write.State == Left {
			out = append(out, write)

			continue
		}
		i, seen := at[write.File]
		if !seen {
			at[write.File] = len(out)
			out = append(out, write)

			continue
		}
		if write.State != Unchanged {
			out[i].State = write.State
		}
	}

	return out
}

// The seed's icons, by the SHA-256 of each file as every skeleton carries it: the
// placeholder mark a new application starts with, in the favicon, the three app icons,
// and the logo the seed's declaration names. A browser application still carrying one of
// them shows the skeleton's mark where the application's own belongs.
var seedIcons = map[string]string{
	"c92a2a539b274199a33a4e46c8947e272def4ed0b20a1c79ce249f5964a12856": "public/favicon.svg",
	"a3479f267e85c2261351df5aa5099a2e8d125050a44dfec5290b63fc7455f7c4": "public/icons/icon-192.png",
	"8858b74707da7e7c5909bbd8624832ddf9ee5d112d2206994bf5af93526daa17": "public/icons/icon-512.png and logo.png",
	"18b80609d1e31ad99a07947e638a33172f85b881827db667e10a9e06a48e3317": "public/icons/icon-512-maskable.png",
}

// seedFiles are the files of a browser application a seed's icon is looked for in, by
// their place under the public directory; the logo the declaration names is looked at
// beside them.
var seedFiles = []string{faviconSVG, smallIcon, largeIcon, maskableIcon}

// SeedIcons lists, for each browser application still carrying an icon of the seed's, the
// files that are the seed's, relative to its directory: the placeholder mark every new
// application starts with. dirs is in directory order, and declared says which of them
// declare a brand, since what to do differs: one with a declaration replaces its logo,
// and one without writes the declaration first.
func SeedIcons(a *app.App) (dirs []string, files map[string][]string, declared map[string]bool, err error) {
	roots, err := BrowserApps(a)
	if err != nil {
		return nil, nil, nil, err
	}
	files, declared = map[string][]string{}, map[string]bool{}
	for i := range roots {
		r := &roots[i]
		candidates := make([]string, 0, len(seedFiles)+1)
		if data, err := os.ReadFile(a.Abs(r.DeclarationPath())); err == nil {
			declared[r.Dir] = true
			if d, err := parseDeclaration(r.DeclarationPath(), data); err == nil {
				candidates = append(candidates, path.Join(r.Dir, d.Logo))
			}
		}
		for _, f := range seedFiles {
			candidates = append(candidates, path.Join(r.Public, f))
		}
		for _, file := range candidates {
			data, err := os.ReadFile(a.Abs(file))
			if err != nil {
				continue
			}
			if IsSeedIcon(data) {
				files[r.Dir] = append(files[r.Dir], strings.TrimPrefix(strings.TrimPrefix(file, r.Dir), "/"))
			}
		}
		if len(files[r.Dir]) > 0 {
			dirs = append(dirs, r.Dir)
		}
	}

	return dirs, files, declared, nil
}

// IsSeedIcon reports whether the content is one of the seed's icons.
func IsSeedIcon(content []byte) bool {
	sum := sha256.Sum256(content)
	_, seed := seedIcons[hex.EncodeToString(sum[:])]

	return seed
}
