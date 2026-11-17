// Package brand writes an application's brand where the application carries it. A
// browser application declares its brand once, in brand.json in its own directory (the
// Angular project's root: web/console in the flat layout, apps/console/web in the sites
// layout, where it is the web root) beside its logo: a name, the logo, a primary color
// and, optionally, a background color. From the declaration impulse brand writes the
// favicon and the app icons, the manifest's names and colors, the page's title and
// theme-color, the name the frame draws, the theme's first palette, and, for the server,
// the name the mail an auth sends says and the name and logo of the maintenance page. One
// brand per browser application: a console and a portal of one application carry their
// own, whether they share a workspace or have one each.
//
// The files are the application's after the first write: the command runs when asked,
// never as part of a rendering, and writes only what the declaration changed, so an icon
// tuned by hand stays until the logo it was cut from changes.
package brand

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// DeclarationFile is the name a browser application's brand declaration carries.
const DeclarationFile = "brand.json"

// defaultBackground is the background color of a brand that declares none: the color
// behind the installed application while it starts and behind the logo on the icon the
// device cuts to its own shape.
const defaultBackground = "#ffffff"

// Declaration is a browser application's brand as its brand.json declares it.
type Declaration struct {
	// Name is the application's name as people read it ("Harbor"): the page's title, the
	// installed application's name, the name the frame draws, and the name the server's
	// mail and maintenance page say.
	Name string `json:"name"`
	// Logo is the logo, a square PNG at least MinLogoSize pixels a side, by its path
	// relative to the declaration's directory: every icon is scaled down from it.
	Logo string `json:"logo"`
	// LogoSVG is the same logo as an SVG, by its path relative to the declaration; where it
	// is given, it is the favicon as it is. Optional.
	LogoSVG string `json:"logoSvg,omitempty"`
	// PrimaryColor is the brand's color, as six hex digits (#005cbb): the theme's first
	// palette is generated from it, and the browser draws its own frame in it.
	PrimaryColor string `json:"primaryColor"`
	// BackgroundColor is the color behind the installed application while it starts and
	// behind the logo on the icon a device cuts to its own shape, as six hex digits;
	// white where the brand declares none. Optional.
	BackgroundColor string `json:"backgroundColor,omitempty"`
}

// Background is the brand's background color: the declared one, or white.
func (d *Declaration) Background() string {
	if d.BackgroundColor == "" {
		return defaultBackground
	}

	return d.BackgroundColor
}

// hexColor is a color as the declaration writes one: a number sign and six hex digits.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// example is the declaration a refusal shows, since a person who has none copies one.
const example = `{ "name": "Harbor", "logo": "logo.png", "primaryColor": "#005cbb" }`

// parseDeclaration reads a declaration and refuses one the command cannot write from:
// each refusal names the file and says what to write. file is the declaration's path as
// the refusal names it.
func parseDeclaration(file string, data []byte) (*Declaration, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var d Declaration
	if err := decoder.Decode(&d); err != nil {
		return nil, errors.Newf("%s does not read as a brand declaration (%v): it holds a name, a logo, a primary color and, optionally, a background color and the logo as an SVG, such as %s", file, err, example)
	}
	d.Name = strings.TrimSpace(d.Name)
	switch {
	case d.Name == "":
		return nil, errors.Newf(`%s declares no name: give the application's name as people read it ("name": "Harbor")`, file)
	case strings.ContainsAny(d.Name, "\r\n\t"):
		return nil, errors.Newf("%s declares a name that runs over more than one line: the name is a few words on one line, as the page's title shows it", file)
	case d.Logo == "":
		return nil, errors.Newf(`%s declares no logo: put a square PNG at least %d pixels a side beside it and name it ("logo": "logo.png")`, file, MinLogoSize)
	case !hexColor.MatchString(d.PrimaryColor):
		return nil, errors.Newf(`%s declares the primary color %q: write it as a number sign and six hex digits ("primaryColor": "#005cbb")`, file, d.PrimaryColor)
	case d.BackgroundColor != "" && !hexColor.MatchString(d.BackgroundColor):
		return nil, errors.Newf(`%s declares the background color %q: write it as a number sign and six hex digits ("backgroundColor": "#ffffff"), or leave it out for white`, file, d.BackgroundColor)
	}
	d.PrimaryColor = strings.ToLower(d.PrimaryColor)
	d.BackgroundColor = strings.ToLower(d.BackgroundColor)
	for _, named := range []struct{ field, file string }{{"logo", d.Logo}, {"logoSvg", d.LogoSVG}} {
		if named.file == "" {
			continue
		}
		if path.IsAbs(named.file) || !localPath(named.file) {
			return nil, errors.Newf("%s names the %s %q, which is not a file beside the declaration: name it by its path from the directory the declaration is in, such as logo.png", file, named.field, named.file)
		}
	}

	return &d, nil
}

// localPath reports a slash-separated path that stays inside the directory it is
// relative to.
func localPath(p string) bool {
	clean := path.Clean(p)

	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../") && !strings.Contains(p, `\`)
}

// BrowserApp is one browser application: an application project of a browser workspace,
// with the workspace that holds it. Every path is relative to the application root and
// slash-separated.
type BrowserApp struct {
	// Dir is the browser application's directory, the Angular project's root:
	// web/console in the flat layout, apps/console/web in the sites layout. The
	// declaration and the logo sit in it.
	Dir string
	// Workspace is the directory holding angular.json and the installed packages: web,
	// or the application's own directory in the sites layout.
	Workspace string
	// Project is the Angular project's name (console).
	Project string
	// Source is the project's source root (web/console/src), Public the directory the
	// build copies into the bundle root (web/console/public), and Index the entry
	// document (web/console/src/index.html).
	Source, Public, Index string
}

// DeclarationPath is where the browser application's declaration sits.
func (r *BrowserApp) DeclarationPath() string {
	return path.Join(r.Dir, DeclarationFile)
}

// BrowserApps lists the application's browser applications, in directory order: every
// application project of every browser workspace.
func BrowserApps(a *app.App) ([]BrowserApp, error) {
	var roots []BrowserApp
	for _, w := range a.WebApps {
		projects, err := a.ReadAngular(w.Dir)
		if err != nil {
			return nil, errors.Wrapf(err, "%s", path.Join(w.Dir, "angular.json"))
		}
		for i := range projects {
			p := &projects[i]
			if p.ProjectType != "" && p.ProjectType != "application" {
				continue
			}
			r := BrowserApp{Dir: path.Join(w.Dir, p.Root), Workspace: w.Dir, Project: p.Name}
			r.Source = path.Join(r.Dir, "src")
			if p.SourceRoot != "" && p.SourceRoot != "." {
				r.Source = path.Join(w.Dir, p.SourceRoot)
			}
			r.Public = path.Join(r.Dir, "public")
			if len(p.AssetDirs) > 0 {
				r.Public = path.Join(w.Dir, p.AssetDirs[0])
			}
			r.Index = path.Join(r.Source, "index.html")
			if p.Index != "" {
				r.Index = path.Join(w.Dir, p.Index)
			}
			roots = append(roots, r)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Dir < roots[j].Dir
	})

	return roots, nil
}

// declared is a browser application with the brand it declares: the declaration, and the
// logo it names, read and held to its size.
type declared struct {
	BrowserApp
	declaration *Declaration
	logo        *logo
	// svg is the logo as an SVG, as the file holds it; nil where the brand names none.
	svg []byte
}

// readDeclared reads the browser application's declaration and the logo it names. One
// with no declaration is not an error: it reads as nil.
func readDeclared(a *app.App, r *BrowserApp) (*declared, error) {
	file := r.DeclarationPath()
	data, err := os.ReadFile(a.Abs(file))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "os.ReadFile()")
	}
	d, err := parseDeclaration(file, data)
	if err != nil {
		return nil, err
	}
	logoFile := path.Join(r.Dir, d.Logo)
	png, err := os.ReadFile(a.Abs(logoFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.Newf("%s names the logo %s, and no file is there: put a square PNG at least %d pixels a side at %s", file, d.Logo, MinLogoSize, logoFile)
	}
	if err != nil {
		return nil, errors.Wrap(err, "os.ReadFile()")
	}
	mark, err := readLogo(logoFile, png)
	if err != nil {
		return nil, err
	}
	got := &declared{BrowserApp: *r, declaration: d, logo: mark}
	if d.LogoSVG != "" {
		svgFile := path.Join(r.Dir, d.LogoSVG)
		got.svg, err = os.ReadFile(a.Abs(svgFile))
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.Newf("%s names the logo's SVG %s, and no file is there: put the SVG at %s, or take logoSvg out of the declaration and the favicon is cut from the PNG", file, d.LogoSVG, svgFile)
		}
		if err != nil {
			return nil, errors.Wrap(err, "os.ReadFile()")
		}
		if !bytes.Contains(got.svg, []byte("<svg")) {
			return nil, errors.Newf("%s does not read as an SVG (it holds no <svg> element): %s names it as the logo's SVG, which becomes the favicon as it is", svgFile, file)
		}
	}

	return got, nil
}

// say spells a declaration's brand for the report: the name and the colors.
func (d *Declaration) say() string {
	return fmt.Sprintf("%s (primary %s, background %s)", d.Name, d.PrimaryColor, d.Background())
}
