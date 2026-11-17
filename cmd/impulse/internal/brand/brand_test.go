package brand

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
)

// rendered renders a skeleton into a temporary directory and discovers it: the
// application the command is run over.
func rendered(t *testing.T, candidate string) *app.App {
	t.Helper()

	dir := filepath.Join(t.TempDir(), candidate)
	if _, err := skeleton.Render(&skeleton.Options{Version: "v0.1.0", WebVersion: "0.1.0", Candidate: candidate, Dir: dir, ModulePath: "example.com/acme/" + candidate}); err != nil {
		t.Fatalf("skeleton.Render() error = %v", err)
	}

	return discover(t, dir)
}

// discover reads the application at the directory again, as each run of the command does.
func discover(t *testing.T, dir string) *app.App {
	t.Helper()

	a, err := app.Discover(dir)
	if err != nil {
		t.Fatalf("app.Discover() error = %v", err)
	}

	return a
}

// put writes a file of the application.
func put(t *testing.T, a *app.App, rel string, content []byte) {
	t.Helper()

	abs := a.Abs(rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// read reads a file of the application.
func read(t *testing.T, a *app.App, rel string) string {
	t.Helper()

	data, err := os.ReadFile(a.Abs(rel))
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// disc is a logo for a test: a disc of the color on a transparent square, width by
// height pixels, as a PNG.
func disc(t *testing.T, width, height int, fill color.NRGBA) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	cx, cy, radius := float64(width)/2, float64(height)/2, float64(min(width, height))*0.45
	for y := range height {
		for x := range width {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dx*dx+dy*dy <= radius*radius {
				img.SetNRGBA(x, y, fill)
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}

	return b.Bytes()
}

// decoded reads a PNG the command wrote.
func decoded(t *testing.T, a *app.App, rel string) image.Image {
	t.Helper()

	img, err := png.Decode(strings.NewReader(read(t, a, rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}

	return img
}

// installedWorkspace stands in for a browser workspace whose packages are installed: the
// Angular command line and Angular Material's theme-color schematic are there, and the
// schematic, run through it, writes the palette file naming the color it was generated
// from, as the real one does. It keeps every command line it was run with.
type installedWorkspace struct {
	t     *testing.T
	a     *app.App
	calls []string
}

// install marks the workspace's packages installed.
func (w *installedWorkspace) install(workspace string) {
	put(w.t, w.a, workspace+"/"+ngBin, []byte("#!/usr/bin/env node\n"))
	put(w.t, w.a, workspace+"/"+prettierBin, []byte("#!/usr/bin/env node\n"))
	put(w.t, w.a, workspace+"/"+themeSchematicDir+"/schema.json", []byte("{}\n"))
}

func (w *installedWorkspace) Run(_ context.Context, dir string, _ []string, name string, args ...string) ([]byte, error) {
	rel, err := filepath.Rel(w.a.Root, dir)
	if err != nil {
		w.t.Fatal(err)
	}
	w.calls = append(w.calls, filepath.ToSlash(rel)+": "+filepath.Base(name)+" "+strings.Join(args, " "))
	if filepath.Base(name) != "ng" {
		return nil, nil
	}
	var primary, directory string
	for _, arg := range args {
		if v, ok := strings.CutPrefix(arg, "--primary-color="); ok {
			primary = v
		}
		if v, ok := strings.CutPrefix(arg, "--directory="); ok {
			directory = v
		}
	}
	palette := "// Note: Color palettes are generated from primary: " + primary + "\n$primary-palette: (\n  40: " + primary + ",\n);\n$tertiary-palette: (\n  40: #000000,\n);\n"
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(directory), paletteFile), []byte(palette), 0o600); err != nil {
		w.t.Fatal(err)
	}

	return []byte("CREATE " + directory + paletteFile + "\n"), nil
}

// states lists what the command did with each file, as "<state> <file>" lines.
func states(writes []Write) []string {
	lines := make([]string, 0, len(writes))
	for _, w := range writes {
		line := string(w.State) + " " + w.File
		if w.Note != "" {
			line += ": " + w.Note
		}
		lines = append(lines, line)
	}

	return lines
}

// treeSum is a digest of every file under the application root, by path and content.
func treeSum(t *testing.T, a *app.App) string {
	t.Helper()

	sum := sha256.New()
	tree := os.DirFS(a.Root)
	err := fs.WalkDir(tree, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "fs.WalkDir()")
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(tree, p)
		if err != nil {
			return errors.Wrap(err, "fs.ReadFile()")
		}
		sum.Write([]byte(p + "\x00"))
		sum.Write(data)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return hex.EncodeToString(sum.Sum(nil))
}

const (
	harborSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><circle cx="5" cy="5" r="4" fill="#b3261e" /></svg>` + "\n"
	harbor    = `{
  "name": "Harbor Works",
  "logo": "mark.png",
  "logoSvg": "mark.svg",
  "primaryColor": "#B3261E",
  "backgroundColor": "#101820"
}
`
)

// TestApply runs the command over the solo skeleton's console with a brand of its own and
// reads every file it writes from the declaration; then runs it again, over the same
// declaration and over one changed a field at a time, and holds each run to writing what
// the declaration changed and nothing else.
func TestApply(t *testing.T) {
	t.Parallel()

	a := rendered(t, skeleton.Base)
	red := color.NRGBA{R: 0xb3, G: 0x26, B: 0x1e, A: 0xff}
	put(t, a, "web/console/brand.json", []byte(harbor))
	put(t, a, "web/console/mark.png", disc(t, 640, 640, red))
	put(t, a, "web/console/mark.svg", []byte(harborSVG))
	workspace := &installedWorkspace{t: t, a: a}
	workspace.install("web")

	result, err := Apply(context.Background(), a, workspace)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(result.Apps) != 1 || result.Apps[0].Dir != "web/console" || result.Apps[0].Brand != "Harbor Works (primary #b3261e, background #101820)" || !result.Apps[0].Palette {
		t.Fatalf("Apply() = %+v, want the console's brand written with its palette", result.Apps)
	}
	wantWrites := []string{
		"written web/console/public/favicon.svg",
		"written web/console/public/favicon.png",
		"written web/console/public/icons/icon-192.png",
		"written web/console/public/icons/icon-512.png",
		"written web/console/public/icons/icon-512-maskable.png",
		"written web/console/public/manifest.webmanifest",
		"written web/console/src/index.html",
		"written web/console/src/app/app.config.ts",
		"written web/console/src/_theme-colors.scss",
		"written web/console/src/theme.scss",
	}
	if diff := cmp.Diff(wantWrites, states(result.Apps[0].Writes)); diff != "" {
		t.Errorf("the console's writes mismatch (-want +got):\n%s", diff)
	}
	wantServer := []string{"written pkg/brand/console.png", "written pkg/brand/brand.go", "written pkg/auth/staff/staff.go"}
	if diff := cmp.Diff(wantServer, states(result.Server)); diff != "" {
		t.Errorf("the server's writes mismatch (-want +got):\n%s", diff)
	}
	if len(result.Unserved) != 0 || len(result.Undeclared) != 0 || len(result.Incomplete()) != 0 {
		t.Errorf("Apply() left %v unserved, %v undeclared and %v incomplete, want none", result.Unserved, result.Undeclared, result.Incomplete())
	}
	wantCalls := []string{
		"web: ng generate @angular/material:theme-color --primary-color=#b3261e --directory=console/src/ --is-scss --include-high-contrast=false --interactive=false --force",
		"web: prettier --write console/src/_theme-colors.scss",
	}
	if diff := cmp.Diff(wantCalls, workspace.calls); diff != "" {
		t.Errorf("the commands run in the workspace mismatch (-want +got):\n%s", diff)
	}

	// The icons: the SVG as it is, and every PNG at its size, cut from the logo.
	if got := read(t, a, "web/console/public/favicon.svg"); got != harborSVG {
		t.Errorf("favicon.svg = %q, want the declared SVG as it is", got)
	}
	for file, size := range map[string]int{
		"web/console/public/favicon.png":                 96,
		"web/console/public/icons/icon-192.png":          192,
		"web/console/public/icons/icon-512.png":          512,
		"web/console/public/icons/icon-512-maskable.png": 512,
		"pkg/brand/console.png":                          192,
	} {
		img := decoded(t, a, file)
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Errorf("%s is %dx%d, want %dx%d", file, b.Dx(), b.Dy(), size, size)
		}
	}
	// An icon keeps the logo's transparency: its corner is clear and its center the
	// logo's color.
	icon := decoded(t, a, "web/console/public/icons/icon-192.png")
	if got := color.NRGBAModel.Convert(icon.At(0, 0)); got != (color.NRGBA{}) {
		t.Errorf("the icon's corner = %v, want transparent", got)
	}
	if got := color.NRGBAModel.Convert(icon.At(96, 96)); got != red {
		t.Errorf("the icon's center = %v, want the logo's %v", got, red)
	}
	// The maskable icon is the logo in the central 80% on the background color: the
	// background at the corner and just inside the margin's edge, the logo at the center.
	maskable := decoded(t, a, "web/console/public/icons/icon-512-maskable.png")
	background := color.NRGBA{R: 0x10, G: 0x18, B: 0x20, A: 0xff}
	for _, at := range []image.Point{{0, 0}, {511, 511}, {256, 50}, {50, 256}} {
		if got := color.NRGBAModel.Convert(maskable.At(at.X, at.Y)); got != background {
			t.Errorf("the maskable icon at %v = %v, want the background %v", at, got, background)
		}
	}
	if got := color.NRGBAModel.Convert(maskable.At(256, 256)); got != red {
		t.Errorf("the maskable icon's center = %v, want the logo's %v", got, red)
	}
	// The disc reaches 45% of the logo's side from its center, so 36% of the icon's side
	// once the logo is scaled into the central 80%: 184 pixels of 512. A pixel inside
	// that is the logo's, and one beyond it, still inside the safe zone, the background.
	if got := color.NRGBAModel.Convert(maskable.At(256+170, 256)); got != red {
		t.Errorf("the maskable icon inside the scaled logo = %v, want the logo's %v", got, red)
	}
	if got := color.NRGBAModel.Convert(maskable.At(256+195, 256)); got != background {
		t.Errorf("the maskable icon beyond the scaled logo = %v, want the background %v", got, background)
	}

	// The names and colors, each where the application carries it.
	for file, wants := range map[string][]string{
		"web/console/public/manifest.webmanifest": {`"name": "Harbor Works",`, `"short_name": "Harbor Works",`, `"theme_color": "#b3261e",`, `"background_color": "#101820",`, `"id": "/",`},
		"web/console/src/index.html": {
			"<title>Harbor Works</title>", `<meta name="theme-color" content="#b3261e" />`,
			"    <link rel=\"icon\" type=\"image/png\" sizes=\"96x96\" href=\"favicon.png\" />\n    <link rel=\"icon\" type=\"image/svg+xml\" href=\"favicon.svg\" />\n    <link rel=\"manifest\"",
		},
		"web/console/src/app/app.config.ts": {"provideFrame({ name: 'Harbor Works', home: 'dashboard', menus: appMenus }),", "        { id: 'brand', label: 'Harbor Works' },\n        { id: 'workspace', label: 'Workspace' },\n"},
		"web/console/src/theme.scss": {
			"@use '@impulseframework/resource-angular/palettes';\n@use './theme-colors' as brand;\n",
			"    'brand': (\n      primary: brand.$primary-palette,\n      tertiary: brand.$tertiary-palette,\n    ),\n    'workspace': (\n      primary: palettes.$workspace-primary,",
		},
		"web/console/src/_theme-colors.scss": {"generated from primary: #b3261e"},
		"pkg/brand/brand.go":                 {"// ConsoleName is the name web/console/brand.json declares.\nconst ConsoleName = \"Harbor Works\"\n", "//go:embed console.png\nvar consoleLogo []byte\n", "func Console() maintenance.Brand {\n\treturn maintenance.Brand{Name: ConsoleName, Logo: consoleLogo}\n}\n"},
		"pkg/auth/staff/staff.go":            {"\tApplication = \"Harbor Works\"\n"},
	} {
		got := read(t, a, file)
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("%s lacks %q:\n%s", file, want, got)
			}
		}
	}
	for file, lacks := range map[string][]string{
		"web/console/src/theme.scss":        {"'blue'", "azure-palette"},
		"web/console/src/app/app.config.ts": {"'blue'", "'Solo'"},
		"web/console/src/index.html":        {"Solo"},
	} {
		got := read(t, a, file)
		for _, lack := range lacks {
			if strings.Contains(got, lack) {
				t.Errorf("%s still carries %q:\n%s", file, lack, got)
			}
		}
	}

	// A second run over the same declaration writes nothing.
	again, err := Apply(context.Background(), discover(t, a.Root), workspace)
	if err != nil {
		t.Fatalf("Apply() again error = %v", err)
	}
	for _, line := range append(states(again.Apps[0].Writes), states(again.Server)...) {
		if !strings.HasPrefix(line, string(Unchanged)+" ") {
			t.Errorf("the second run over the same declaration: %s, want every file unchanged", line)
		}
	}

	// A run after one field changed writes the files that field is in, and no other.
	changes := []struct {
		name        string
		declaration string
		want        []string
	}{
		{
			name:        "the name",
			declaration: strings.Replace(harbor, "Harbor Works", "Harbor & Sons", 1),
			want: []string{
				"written web/console/public/manifest.webmanifest", "written web/console/src/index.html", "written web/console/src/app/app.config.ts",
				"written pkg/brand/brand.go", "written pkg/auth/staff/staff.go",
			},
		},
		{
			name:        "the primary color",
			declaration: strings.Replace(strings.Replace(harbor, "Harbor Works", "Harbor & Sons", 1), "#B3261E", "#00695c", 1),
			want:        []string{"written web/console/public/manifest.webmanifest", "written web/console/src/index.html", "written web/console/src/_theme-colors.scss"},
		},
		{
			name:        "the background color",
			declaration: strings.Replace(strings.Replace(strings.Replace(harbor, "Harbor Works", "Harbor & Sons", 1), "#B3261E", "#00695c", 1), "#101820", "#fafafa", 1),
			want:        []string{"written web/console/public/icons/icon-512-maskable.png", "written web/console/public/manifest.webmanifest"},
		},
		{
			name:        "the SVG taken out",
			declaration: "{\n  \"name\": \"Harbor & Sons\",\n  \"logo\": \"mark.png\",\n  \"primaryColor\": \"#00695c\",\n  \"backgroundColor\": \"#fafafa\"\n}\n",
			want:        []string{"removed web/console/public/favicon.svg", "written web/console/src/index.html"},
		},
	}
	for _, change := range changes {
		put(t, a, "web/console/brand.json", []byte(change.declaration))
		got, err := Apply(context.Background(), discover(t, a.Root), workspace)
		if err != nil {
			t.Fatalf("Apply() after %s changed: error = %v", change.name, err)
		}
		var changed []string
		for _, line := range append(states(got.Apps[0].Writes), states(got.Server)...) {
			if !strings.HasPrefix(line, string(Unchanged)+" ") {
				changed = append(changed, line)
			}
		}
		if diff := cmp.Diff(change.want, changed); diff != "" {
			t.Errorf("the run after %s changed wrote (-want +got):\n%s", change.name, diff)
		}
	}
	// The last declaration's name, with its ampersand, as each file spells it, and the
	// page linking the PNG favicon alone.
	for file, wants := range map[string][]string{
		"web/console/public/manifest.webmanifest": {`"name": "Harbor & Sons",`},
		"web/console/src/index.html":              {"<title>Harbor &amp; Sons</title>", "    <link rel=\"icon\" type=\"image/png\" sizes=\"96x96\" href=\"favicon.png\" />\n    <link rel=\"manifest\""},
		"web/console/src/app/app.config.ts":       {"name: 'Harbor & Sons',", "{ id: 'brand', label: 'Harbor & Sons' },"},
		"pkg/brand/brand.go":                      {`const ConsoleName = "Harbor & Sons"`},
	} {
		got := read(t, a, file)
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("%s lacks %q:\n%s", file, want, got)
			}
		}
	}
}

// TestApplyKeepsATunedIcon holds the command to leaving an icon as the application has
// it once written: an icon tuned by hand stays through a change of the name and, but for
// the one a device cuts to its own shape, of the background color, and is cut again only
// when the logo changes; an icon that is not there is cut again.
func TestApplyKeepsATunedIcon(t *testing.T) {
	t.Parallel()

	a := rendered(t, skeleton.Base)
	green, blue, tuned := color.NRGBA{G: 0x80, B: 0x40, A: 0xff}, color.NRGBA{R: 0x20, G: 0x40, B: 0xc0, A: 0xff}, color.NRGBA{R: 0xff, G: 0xaa, A: 0xff}
	const (
		declaration = `{"name": "Harbor", "logo": "logo.png", "primaryColor": "#008040", "backgroundColor": "#101820"}`
		small       = "web/console/public/icons/icon-192.png"
		maskable    = "web/console/public/icons/icon-512-maskable.png"
		favicon     = "web/console/public/favicon.png"
	)
	put(t, a, "web/console/brand.json", []byte(declaration))
	put(t, a, "web/console/logo.png", disc(t, 512, 512, green))
	workspace := &installedWorkspace{t: t, a: a}
	workspace.install("web")
	run := func(what string) {
		t.Helper()

		if _, err := Apply(context.Background(), discover(t, a.Root), workspace); err != nil {
			t.Fatalf("Apply() %s: error = %v", what, err)
		}
	}
	center := func(file string) color.Color {
		t.Helper()

		img := decoded(t, a, file)

		return color.NRGBAModel.Convert(img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2))
	}
	run("the first time")

	// Two icons tuned by hand, then the name changed: both stay.
	put(t, a, small, disc(t, 192, 192, tuned))
	put(t, a, maskable, disc(t, 512, 512, tuned))
	put(t, a, "web/console/brand.json", []byte(strings.Replace(declaration, "Harbor", "Harbor Works", 1)))
	run("after the name changed")
	for _, file := range []string{small, maskable} {
		if got := center(file); got != tuned {
			t.Errorf("after the name changed, %s shows %v at its center, want the icon tuned by hand (%v)", file, got, tuned)
		}
	}

	// The background changed: the icon a device cuts is cut again on it, and the other
	// stays.
	put(t, a, "web/console/brand.json", []byte(strings.Replace(declaration, "#101820", "#fafafa", 1)))
	run("after the background changed")
	if got := center(small); got != tuned {
		t.Errorf("after the background changed, %s shows %v at its center, want the icon tuned by hand (%v)", small, got, tuned)
	}
	if got := center(maskable); got != green {
		t.Errorf("after the background changed, %s shows %v at its center, want the logo's %v", maskable, got, green)
	}
	if got := color.NRGBAModel.Convert(decoded(t, a, maskable).At(0, 0)); got != (color.NRGBA{R: 0xfa, G: 0xfa, B: 0xfa, A: 0xff}) {
		t.Errorf("after the background changed, %s stands on %v, want the new background", maskable, got)
	}

	// An icon that is not there is cut again, and the tuned one still stays.
	if err := os.Remove(a.Abs(favicon)); err != nil {
		t.Fatal(err)
	}
	run("after an icon was removed")
	if got := center(favicon); got != green {
		t.Errorf("after it was removed, %s shows %v at its center, want the logo's %v", favicon, got, green)
	}
	if got := center(small); got != tuned {
		t.Errorf("after another icon was removed, %s shows %v at its center, want the icon tuned by hand (%v)", small, got, tuned)
	}

	// The server's copy of the logo written again by another encoder, the same picture in
	// other bytes, as an impulse built with another Go might write it: the logo still
	// stands, so the tuned icon stays and the copy is left as it is.
	var other bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&other, decoded(t, a, "pkg/brand/console.png")); err != nil {
		t.Fatal(err)
	}
	if other.String() == read(t, a, "pkg/brand/console.png") {
		t.Fatal("the other encoder wrote the same bytes, so the case tests nothing")
	}
	put(t, a, "pkg/brand/console.png", other.Bytes())
	run("after the server's logo was encoded another way")
	if got := center(small); got != tuned {
		t.Errorf("after the server's logo was encoded another way, %s shows %v at its center, want the icon tuned by hand (%v)", small, got, tuned)
	}
	if read(t, a, "pkg/brand/console.png") != other.String() {
		t.Error("the server's logo was written again though it shows the same picture")
	}

	// The logo changed: every icon is cut from it again, the tuned one too.
	put(t, a, "web/console/logo.png", disc(t, 512, 512, blue))
	run("after the logo changed")
	for _, file := range []string{small, maskable, favicon, "web/console/public/icons/icon-512.png", "pkg/brand/console.png"} {
		if got := center(file); got != blue {
			t.Errorf("after the logo changed, %s shows %v at its center, want the new logo's %v", file, got, blue)
		}
	}
}

// TestApplyTwoBrowserApps runs the command over the outlets skeleton, whose console and
// portal share a workspace, with a brand of its own for each: each keeps its own name,
// colors and icons, the server's package holds both, and each auth's mail says the name
// of the browser application its people sign in to.
func TestApplyTwoBrowserApps(t *testing.T) {
	t.Parallel()

	a := rendered(t, "outlets")
	green, blue := color.NRGBA{G: 0x80, B: 0x40, A: 0xff}, color.NRGBA{R: 0x20, G: 0x40, B: 0xc0, A: 0xff}
	put(t, a, "web/console/brand.json", []byte(`{"name": "Harbor", "logo": "logo.png", "primaryColor": "#008040"}`))
	put(t, a, "web/console/logo.png", disc(t, 512, 512, green))
	put(t, a, "web/portal/brand.json", []byte(`{"name": "Harbor for Partners", "logo": "logo.png", "primaryColor": "#2040c0", "backgroundColor": "#eef2ff"}`))
	put(t, a, "web/portal/logo.png", disc(t, 1024, 1024, blue))
	workspace := &installedWorkspace{t: t, a: a}
	workspace.install("web")

	result, err := Apply(context.Background(), a, workspace)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	var brands []string
	for _, r := range result.Apps {
		brands = append(brands, r.Dir+": "+r.Brand)
	}
	wantBrands := []string{"web/console: Harbor (primary #008040, background #ffffff)", "web/portal: Harbor for Partners (primary #2040c0, background #eef2ff)"}
	if diff := cmp.Diff(wantBrands, brands); diff != "" {
		t.Errorf("the brands written mismatch (-want +got):\n%s", diff)
	}
	for file, wants := range map[string][]string{
		"web/console/public/manifest.webmanifest": {`"name": "Harbor",`, `"theme_color": "#008040",`, `"background_color": "#ffffff",`, `"id": "/console/",`},
		"web/portal/public/manifest.webmanifest":  {`"name": "Harbor for Partners",`, `"theme_color": "#2040c0",`, `"background_color": "#eef2ff",`, `"id": "/portal/",`},
		"web/console/src/index.html":              {"<title>Harbor</title>", `content="#008040"`},
		"web/portal/src/index.html":               {"<title>Harbor for Partners</title>", `content="#2040c0"`},
		"web/console/src/app/app.config.ts":       {"name: 'Harbor',", "{ id: 'brand', label: 'Harbor' },"},
		// The portal offers one palette and lists none, so only its frame is named.
		"web/portal/src/app/app.config.ts":   {"name: 'Harbor for Partners',", "provideTheme(),"},
		"web/console/src/_theme-colors.scss": {"generated from primary: #008040"},
		"web/portal/src/_theme-colors.scss":  {"generated from primary: #2040c0"},
		"web/portal/src/theme.scss":          {"@use './theme-colors' as brand;\n", "    'brand': (\n      primary: brand.$primary-palette,\n      tertiary: brand.$tertiary-palette,\n    ),\n"},
		"pkg/brand/brand.go":                 {`const ConsoleName = "Harbor"`, `const PortalName = "Harbor for Partners"`, "//go:embed console.png", "//go:embed portal.png", "func Console() maintenance.Brand {", "func Portal() maintenance.Brand {"},
		"pkg/auth/staff/staff.go":            {"\tApplication = \"Harbor\"\n"},
		"pkg/auth/members/members.go":        {"\tApplication = \"Harbor for Partners\"\n"},
	} {
		got := read(t, a, file)
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("%s lacks %q:\n%s", file, want, got)
			}
		}
	}
	for file, want := range map[string]color.NRGBA{
		"web/console/public/icons/icon-512.png": green, "web/portal/public/icons/icon-512.png": blue,
		"pkg/brand/console.png": green, "pkg/brand/portal.png": blue,
	} {
		img := decoded(t, a, file)
		center := img.Bounds().Dx() / 2
		if got := color.NRGBAModel.Convert(img.At(center, center)); got != want {
			t.Errorf("%s shows %v at its center, want its own logo's %v", file, got, want)
		}
	}
	// A brand declaring no background color stands on white.
	if got := color.NRGBAModel.Convert(decoded(t, a, "web/console/public/icons/icon-512-maskable.png").At(0, 0)); got != (color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
		t.Errorf("the console's maskable icon stands on %v, want white", got)
	}
	wantCalls := []string{
		"web: ng generate @angular/material:theme-color --primary-color=#008040 --directory=console/src/ --is-scss --include-high-contrast=false --interactive=false --force",
		"web: prettier --write console/src/_theme-colors.scss",
		"web: ng generate @angular/material:theme-color --primary-color=#2040c0 --directory=portal/src/ --is-scss --include-high-contrast=false --interactive=false --force",
		"web: prettier --write portal/src/_theme-colors.scss",
	}
	if diff := cmp.Diff(wantCalls, workspace.calls); diff != "" {
		t.Errorf("the commands run in the workspace mismatch (-want +got):\n%s", diff)
	}
	if len(result.Unserved) != 0 {
		t.Errorf("the skeleton's main hands the maintenance page both brands, and the command says: %v", result.Unserved)
	}
}

// TestApplySites runs the command over the sites skeleton, where each browser application
// is a workspace of its own: each is written from its own declaration in its own
// workspace, and one that declares no brand is said to and left.
func TestApplySites(t *testing.T) {
	t.Parallel()

	a := rendered(t, "sites")
	if err := os.Remove(a.Abs("apps/portal/web/brand.json")); err != nil {
		t.Fatal(err)
	}
	put(t, a, "apps/console/web/brand.json", []byte(`{"name": "Fleet", "logo": "logo.png", "primaryColor": "#6750a4"}`))
	put(t, a, "apps/console/web/logo.png", disc(t, 512, 512, color.NRGBA{R: 0x67, G: 0x50, B: 0xa4, A: 0xff}))
	workspace := &installedWorkspace{t: t, a: a}
	workspace.install("apps/console/web")

	result, err := Apply(context.Background(), a, workspace)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(result.Apps) != 1 || result.Apps[0].Dir != "apps/console/web" {
		t.Fatalf("Apply() wrote %+v, want the console alone", result.Apps)
	}
	if diff := cmp.Diff([]string{"apps/portal/web"}, result.Undeclared); diff != "" {
		t.Errorf("the browser applications declaring no brand mismatch (-want +got):\n%s", diff)
	}
	wantCalls := []string{
		"apps/console/web: ng generate @angular/material:theme-color --primary-color=#6750a4 --directory=src/ --is-scss --include-high-contrast=false --interactive=false --force",
		"apps/console/web: prettier --write src/_theme-colors.scss",
	}
	if diff := cmp.Diff(wantCalls, workspace.calls); diff != "" {
		t.Errorf("the commands run in the workspace mismatch (-want +got):\n%s", diff)
	}
	for file, want := range map[string]string{
		"apps/console/web/public/manifest.webmanifest": `"name": "Fleet",`,
		"apps/console/web/src/index.html":              "<title>Fleet</title>",
		"apps/portal/web/src/index.html":               "<title>Sites Portal</title>",
		"pkg/brand/brand.go":                           `const ConsoleName = "Fleet"`,
		"pkg/auth/staff/staff.go":                      "\tApplication = \"Fleet\"\n",
	} {
		if got := read(t, a, file); !strings.Contains(got, want) {
			t.Errorf("%s lacks %q:\n%s", file, want, got)
		}
	}
	// The portal's main still asks the server's package for the portal's brand, which
	// the package no longer holds: the compiler says so, and the command has nothing to
	// add about a brand that is not declared.
	if len(result.Unserved) != 0 {
		t.Errorf("the command says of the brands it wrote: %v, want nothing", result.Unserved)
	}
}

// TestApplyWithoutPackages runs the command where the workspace's packages are not
// installed: everything is written but the palette, which is left with what to do, the
// theme keeps the palette it had, and the result says the brand is not whole. A palette
// already generated from the declared color stands.
func TestApplyWithoutPackages(t *testing.T) {
	t.Parallel()

	a := rendered(t, skeleton.Base)
	put(t, a, "web/console/brand.json", []byte(`{"name": "Harbor", "logo": "logo.png", "primaryColor": "#008040"}`))
	put(t, a, "web/console/logo.png", disc(t, 512, 512, color.NRGBA{G: 0x80, B: 0x40, A: 0xff}))
	theme := read(t, a, "web/console/src/theme.scss")
	workspace := &installedWorkspace{t: t, a: a}

	result, err := Apply(context.Background(), a, workspace)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if diff := cmp.Diff([]string{"web/console"}, result.Incomplete()); diff != "" {
		t.Errorf("Incomplete() mismatch (-want +got):\n%s", diff)
	}
	wantLeft := "left web/console/src/_theme-colors.scss: the palette is generated by Angular Material's theme-color schematic, which comes with the packages of web, and they are not installed (no web/node_modules/.bin/ng); install them and run impulse brand again"
	writes := states(result.Apps[0].Writes)
	if writes[len(writes)-1] != wantLeft {
		t.Errorf("the palette's line = %q, want %q", writes[len(writes)-1], wantLeft)
	}
	if got := read(t, a, "web/console/src/theme.scss"); got != theme {
		t.Errorf("theme.scss changed without a palette to read:\n%s", got)
	}
	if got := read(t, a, "web/console/src/index.html"); !strings.Contains(got, "<title>Harbor</title>") {
		t.Errorf("index.html lacks the name, which needs no packages:\n%s", got)
	}
	if len(workspace.calls) != 0 {
		t.Errorf("the command ran %v in a workspace with no packages", workspace.calls)
	}

	// The palette of the declared color, generated on a machine that had the packages,
	// stands where they are not installed.
	put(t, a, "web/console/src/_theme-colors.scss", []byte("// Note: Color palettes are generated from primary: #008040\n"))
	result, err = Apply(context.Background(), discover(t, a.Root), workspace)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(result.Incomplete()) != 0 {
		t.Errorf("Incomplete() = %v, want none: the palette of the declared color is there", result.Incomplete())
	}
	if got := read(t, a, "web/console/src/theme.scss"); !strings.Contains(got, "'brand': (") {
		t.Errorf("theme.scss does not read the palette that is there:\n%s", got)
	}
}

// TestApplyRefuses holds the command to refusing a declaration or a logo it cannot write
// from, in words that say what is wrong and what to do, before anything is written.
func TestApplyRefuses(t *testing.T) {
	t.Parallel()

	fill := color.NRGBA{R: 0x33, G: 0x66, B: 0x99, A: 0xff}
	const declaration = `{"name": "Harbor", "logo": "logo.png", "primaryColor": "#336699"}`
	tests := []struct {
		name        string
		declaration string
		// logo is the PNG at logo.png, nil for none.
		logo []byte
		// plain is a file at logo.png that is no PNG.
		plain string
		want  string
	}{
		{
			name: "a logo that is not square", declaration: declaration, logo: disc(t, 640, 512, fill),
			want: "web/console/logo.png is 640 by 512 pixels, which is not square: the logo is a square PNG at least 512 by 512 pixels, since every icon is square and is cut from it",
		},
		{
			name: "a logo that is too small", declaration: declaration, logo: disc(t, 256, 256, fill),
			want: "web/console/logo.png is 256 by 256 pixels, which is too small: the logo is a square PNG at least 512 by 512 pixels, since the largest icon is cut from it at that size and an icon is never scaled up",
		},
		{
			name: "a logo larger than any icon needs", declaration: declaration, logo: blank(t, 4097),
			want: "web/console/logo.png is 4097 by 4097 pixels, which is more than any icon needs: the logo is a square PNG between 512 and 4096 pixels a side",
		},
		{
			name: "a logo that is not a PNG", declaration: declaration, plain: "<svg></svg>",
			want: "web/console/logo.png does not read as a PNG (png: invalid format: not a PNG file): the logo is a square PNG at least 512 pixels a side",
		},
		{
			name: "a logo that is not there", declaration: `{"name": "Harbor", "logo": "mark.png", "primaryColor": "#336699"}`,
			want: "web/console/brand.json names the logo mark.png, and no file is there: put a square PNG at least 512 pixels a side at web/console/mark.png",
		},
		{
			name: "no name", declaration: `{"name": " ", "logo": "logo.png", "primaryColor": "#336699"}`, logo: disc(t, 512, 512, fill),
			want: `web/console/brand.json declares no name: give the application's name as people read it ("name": "Harbor")`,
		},
		{
			name: "no logo", declaration: `{"name": "Harbor", "primaryColor": "#336699"}`,
			want: `web/console/brand.json declares no logo: put a square PNG at least 512 pixels a side beside it and name it ("logo": "logo.png")`,
		},
		{
			name: "a color by name", declaration: `{"name": "Harbor", "logo": "logo.png", "primaryColor": "teal"}`, logo: disc(t, 512, 512, fill),
			want: `web/console/brand.json declares the primary color "teal": write it as a number sign and six hex digits ("primaryColor": "#005cbb")`,
		},
		{
			name: "a background color of three digits", declaration: `{"name": "Harbor", "logo": "logo.png", "primaryColor": "#336699", "backgroundColor": "#fff"}`, logo: disc(t, 512, 512, fill),
			want: `web/console/brand.json declares the background color "#fff": write it as a number sign and six hex digits ("backgroundColor": "#ffffff"), or leave it out for white`,
		},
		{
			name: "a field the declaration does not have", declaration: `{"name": "Harbor", "logo": "logo.png", "primaryColour": "#336699"}`, logo: disc(t, 512, 512, fill),
			want: `web/console/brand.json does not read as a brand declaration (json: unknown field "primaryColour"): it holds a name, a logo, a primary color and, optionally, a background color and the logo as an SVG, such as { "name": "Harbor", "logo": "logo.png", "primaryColor": "#005cbb" }`,
		},
		{
			name: "a logo outside the declaration's directory", declaration: `{"name": "Harbor", "logo": "../../logo.png", "primaryColor": "#336699"}`,
			want: `web/console/brand.json names the logo "../../logo.png", which is not a file beside the declaration: name it by its path from the directory the declaration is in, such as logo.png`,
		},
		{
			name: "an SVG that is not there", declaration: `{"name": "Harbor", "logo": "logo.png", "logoSvg": "logo.svg", "primaryColor": "#336699"}`, logo: disc(t, 512, 512, fill),
			want: "web/console/brand.json names the logo's SVG logo.svg, and no file is there: put the SVG at web/console/logo.svg, or take logoSvg out of the declaration and the favicon is cut from the PNG",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := rendered(t, skeleton.Base)
			put(t, a, "web/console/brand.json", []byte(tt.declaration))
			if err := os.Remove(a.Abs("web/console/logo.png")); err != nil {
				t.Fatal(err)
			}
			switch {
			case tt.logo != nil:
				put(t, a, "web/console/logo.png", tt.logo)
			case tt.plain != "":
				put(t, a, "web/console/logo.png", []byte(tt.plain))
			}
			workspace := &installedWorkspace{t: t, a: a}
			workspace.install("web")
			before := treeSum(t, a)

			_, err := Apply(context.Background(), a, workspace)
			if err == nil {
				t.Fatal("Apply() error = nil, want a refusal")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Apply() error = %v\nwant one saying %q", err, tt.want)
			}
			if treeSum(t, a) != before {
				t.Error("the refusal left the tree changed, want nothing written")
			}
			if len(workspace.calls) != 0 {
				t.Errorf("the refusal came after the command ran %v", workspace.calls)
			}
		})
	}
}

// blank is a transparent square PNG of the size, for a logo too large to take.
func blank(t *testing.T, size int) []byte {
	t.Helper()

	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, size, size))); err != nil {
		t.Fatal(err)
	}

	return b.Bytes()
}

// TestApplyNoDeclaration holds the command to saying how a brand is declared where no
// browser application declares one.
func TestApplyNoDeclaration(t *testing.T) {
	t.Parallel()

	a := rendered(t, skeleton.Base)
	if err := os.Remove(a.Abs("web/console/brand.json")); err != nil {
		t.Fatal(err)
	}
	_, err := Apply(context.Background(), a, &installedWorkspace{t: t, a: a})
	want := `no browser application declares a brand: write web/console/brand.json beside a square PNG logo at least 512 pixels a side, such as { "name": "Harbor", "logo": "logo.png", "primaryColor": "#005cbb" } ("backgroundColor" and "logoSvg" are optional), and run impulse brand again`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Apply() error = %v\nwant one saying %q", err, want)
	}
}

// TestApplyUnserved holds the command to saying which brand no main hands to the
// maintenance page: the brand of a browser application an option added, until the main
// that serves it names it.
func TestApplyUnserved(t *testing.T) {
	t.Parallel()

	a := rendered(t, "outlets")
	main := read(t, a, "main.go")
	put(t, a, "main.go", []byte(strings.Replace(main, `, "/portal": brand.Portal()`, "", 1)))
	result, err := Apply(context.Background(), a, &installedWorkspace{t: t, a: a})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := []string{`no main hands the maintenance page the brand of web/portal: add it to the brands the main serving it gives maintenance.Serve, under its mount path (maintenance.Brands{"/portal": brand.Portal()}, or "/" for an application at the root)`}
	if diff := cmp.Diff(want, result.Unserved); diff != "" {
		t.Errorf("Unserved mismatch (-want +got):\n%s", diff)
	}
}
