package brand

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// State is what the command did with one file.
type State string

// What the command does with a file: it writes one whose content the declaration
// changed, finds one as the declaration already has it, removes one the declaration no
// longer asks for, and leaves one it could not write from the declaration, saying why.
const (
	Written   State = "written"
	Unchanged State = "unchanged"
	Removed   State = "removed"
	Left      State = "left"
)

// Write is one file of a brand and what the command did with it.
type Write struct {
	// File is the file, relative to the application root.
	File  string
	State State
	// Note says, for a file left as it was, what the command looked for and what to do.
	Note string
}

// The files of a browser application the brand is written into, by their place under the
// public directory or the source root.
const (
	faviconSVG     = "favicon.svg"
	faviconPNG     = "favicon.png"
	smallIcon      = "icons/icon-192.png"
	largeIcon      = "icons/icon-512.png"
	maskableIcon   = "icons/icon-512-maskable.png"
	manifestFile   = "manifest.webmanifest"
	appConfigFile  = "app/app.config.ts"
	themeFile      = "theme.scss"
	paletteFile    = "_theme-colors.scss"
	brandPaletteID = "brand"
)

// writer writes an application's files and keeps what it did with each. A file is
// written only where its content differs, so a second run over an unchanged declaration
// writes nothing.
type writer struct {
	a      *app.App
	writes []Write
}

// put writes the file with the content, creating its directory, unless it holds the
// content already. The write goes through a root scoped to the application, so no path
// lands outside it.
func (w *writer) put(file string, content []byte) error {
	existing, err := os.ReadFile(w.a.Abs(file))
	switch {
	case err == nil && bytes.Equal(existing, content):
		w.writes = append(w.writes, Write{File: file, State: Unchanged})

		return nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return errors.Wrap(err, "os.ReadFile()")
	}
	root, err := os.OpenRoot(w.a.Root)
	if err != nil {
		return errors.Wrap(err, "os.OpenRoot()")
	}
	defer root.Close()
	local := filepath.FromSlash(file)
	if dir := filepath.Dir(local); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return errors.Wrap(err, "os.Root.MkdirAll()")
		}
	}
	if err := root.WriteFile(local, content, 0o644); err != nil {
		return errors.Wrap(err, "os.Root.WriteFile()")
	}
	w.writes = append(w.writes, Write{File: file, State: Written})

	return nil
}

// remove removes the file where it is there.
func (w *writer) remove(file string) error {
	err := os.Remove(w.a.Abs(file))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "os.Remove()")
	}
	w.writes = append(w.writes, Write{File: file, State: Removed})

	return nil
}

// left records a file, or a place in one, the command could not write.
func (w *writer) left(file, note string) {
	w.writes = append(w.writes, Write{File: file, State: Left, Note: note})
}

// edit rewrites a text file in place: set returns the text with the brand written in and
// a note for each place it looked for and did not find. A file that is not there is left,
// with what it is for.
func (w *writer) edit(file, purpose string, set func(text string) (string, []string)) error {
	data, err := os.ReadFile(w.a.Abs(file))
	if errors.Is(err, os.ErrNotExist) {
		w.left(file, "it is not there, so "+purpose+" was not written")

		return nil
	}
	if err != nil {
		return errors.Wrap(err, "os.ReadFile()")
	}
	text, notes := set(string(data))
	if err := w.put(file, []byte(text)); err != nil {
		return err
	}
	for _, note := range notes {
		w.left(file, note)
	}

	return nil
}

// icons writes the favicon and the app icons from the logo: the SVG as it is where the
// brand names one, a PNG favicon beside it or in its place, the two app icons, and the
// icon a device cuts to its own shape. A favicon.svg the declaration no longer names is
// removed, since the page then links the PNG alone and the file would be the old icon
// nobody sees.
//
// A PNG icon is the application's once written: it is written again only when what it is
// cut from changed since the last run, the logo, or for the icon a device cuts the logo
// or the background color, or when it is not there. So an icon tuned by hand stays
// through a change of the name or of a color. Whether the logo changed is read from the
// server's copy of it, which the last run cut from the logo it had and which stays as it
// is while the logo does, and whether the background changed from the manifest, which the
// last run wrote it in.
func (w *writer) icons(r *declared) error {
	if r.svg != nil {
		if err := w.put(path.Join(r.Public, faviconSVG), r.svg); err != nil {
			return err
		}
	} else if err := w.remove(path.Join(r.Public, faviconSVG)); err != nil {
		return err
	}
	logoStands, err := w.logoStands(r)
	if err != nil {
		return err
	}
	for _, icon := range []struct {
		file string
		size int
	}{{faviconPNG, faviconSize}, {smallIcon, smallIconSize}, {largeIcon, largeIconSize}} {
		err := w.cut(path.Join(r.Public, icon.file), logoStands, func() ([]byte, error) {
			return r.logo.icon(icon.size)
		})
		if err != nil {
			return err
		}
	}

	return w.cut(path.Join(r.Public, maskableIcon), logoStands && w.backgroundStands(r), func() ([]byte, error) {
		return r.logo.maskable(largeIconSize, r.declaration.Background())
	})
}

// cut writes an icon cut from the logo, unless what it is cut from stands as the last run
// had it and the icon is there: then the icon is left as the application has it.
func (w *writer) cut(file string, stands bool, icon func() ([]byte, error)) error {
	if stands && exists(w.a.Abs(file)) {
		w.writes = append(w.writes, Write{File: file, State: Unchanged})

		return nil
	}
	data, err := icon()
	if err != nil {
		return err
	}

	return w.put(file, data)
}

// logoStands reports whether the logo is the one the last run cut the icons from: the
// server's copy of it, cut at the last run, shows what this logo is cut to. The two are
// compared as pictures, so an impulse whose encoder writes a PNG's bytes another way
// finds the logo standing all the same, and cuts nothing again.
func (w *writer) logoStands(r *declared) (bool, error) {
	kept, err := os.ReadFile(w.a.Abs(path.Join(ServerPackage, serverLogoFile(r.Project))))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.Wrap(err, "os.ReadFile()")
	}

	return shows(kept, r.logo.scaled(serverLogoSize).image()), nil
}

// manifestBackground reads the background color a manifest carries.
var manifestBackground = regexp.MustCompile(`"background_color"\s*:\s*"([^"]*)"`)

// backgroundStands reports whether the background color is the one the last run wrote:
// the manifest carries it.
func (w *writer) backgroundStands(r *declared) bool {
	data, err := os.ReadFile(w.a.Abs(path.Join(r.Public, manifestFile)))
	if err != nil {
		return false
	}
	m := manifestBackground.FindSubmatch(data)

	return len(m) == 2 && strings.EqualFold(string(m[1]), r.declaration.Background())
}

// pages writes the names and the colors into the browser application's manifest, its
// entry document and its application configuration.
func (w *writer) pages(r *declared) error {
	d := r.declaration
	if err := w.edit(path.Join(r.Public, manifestFile), "the installed application's names and colors", func(text string) (string, []string) {
		return setManifest(text, d)
	}); err != nil {
		return err
	}
	if err := w.edit(r.Index, "the page's title, theme-color and icon links", func(text string) (string, []string) {
		return setIndex(text, d, r.svg != nil)
	}); err != nil {
		return err
	}

	return w.edit(path.Join(r.Source, appConfigFile), "the name the frame draws", func(text string) (string, []string) {
		return setFrameName(text, d.Name)
	})
}

// quoted matches a JSON string, a double-quoted TypeScript string, or a single-quoted
// TypeScript string.
const (
	doubleQuoted = `"(?:[^"\\]|\\.)*"`
	singleQuoted = `'(?:[^'\\]|\\.)*'`
)

// jsonString spells a value as a JSON string, leaving the characters HTML would escape as
// they are: the manifest is not HTML.
func jsonString(value string) string {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	// A string always encodes.
	_ = encoder.Encode(value)

	return strings.TrimSuffix(b.String(), "\n")
}

// setManifest writes the brand into a web app manifest: its name under name and
// short_name, the primary color under theme_color and the background under
// background_color. The file keeps its shape; a key it lacks is named in a note.
func setManifest(text string, d *Declaration) (written string, notes []string) {
	for _, field := range []struct{ key, value, what string }{
		{"name", d.Name, "the name"},
		{"short_name", d.Name, "the name"},
		{"theme_color", d.PrimaryColor, "the primary color"},
		{"background_color", d.Background(), "the background color"},
	} {
		slot := regexp.MustCompile(`("` + field.key + `"\s*:\s*)` + doubleQuoted)
		at := slot.FindStringSubmatchIndex(text)
		if at == nil {
			notes = append(notes, fmt.Sprintf("it has no %q to write %s in; add \"%s\": %s", field.key, field.what, field.key, jsonString(field.value)))

			continue
		}
		text = text[:at[3]] + jsonString(field.value) + text[at[1]:]
	}

	return text, notes
}

// The places of an entry document the brand is written in: the title, the theme-color
// meta, and the icon links, each a line of its own.
var (
	titleSlot      = regexp.MustCompile(`(<title>)[^<]*(</title>)`)
	themeColorSlot = regexp.MustCompile(`(<meta\s+name="theme-color"\s+content=")[^"]*(")`)
	iconLinkLine   = regexp.MustCompile(`(?m)^([ \t]*)<link\s+rel="icon"[^>]*>[ \t]*\n`)
)

// setIndex writes the brand into an entry document: the name as the title, the primary
// color as the theme-color, and the icon links for the favicons the command wrote, the
// PNG first and the SVG after it where the brand has one, since a browser that reads
// both takes the last it can draw.
func setIndex(text string, d *Declaration, svg bool) (written string, notes []string) {
	if titleSlot.MatchString(text) {
		text = replaceFirst(titleSlot, text, "${1}"+dollars(html.EscapeString(d.Name))+"${2}")
	} else {
		notes = append(notes, fmt.Sprintf("it has no <title> to write the name in; add <title>%s</title> to its head", html.EscapeString(d.Name)))
	}
	if themeColorSlot.MatchString(text) {
		text = replaceFirst(themeColorSlot, text, "${1}"+d.PrimaryColor+"${2}")
	} else {
		notes = append(notes, fmt.Sprintf("it has no theme-color to write the primary color in; add <meta name=\"theme-color\" content=%q /> to its head", d.PrimaryColor))
	}

	links := []string{fmt.Sprintf(`<link rel="icon" type="image/png" sizes="%dx%d" href="%s" />`, faviconSize, faviconSize, faviconPNG)}
	if svg {
		links = append(links, fmt.Sprintf(`<link rel="icon" type="image/svg+xml" href="%s" />`, faviconSVG))
	}
	lines := iconLinkLine.FindAllStringSubmatchIndex(text, -1)
	if lines == nil {
		return text, append(notes, "it links no icon (no <link rel=\"icon\"> on a line of its own); add to its head: "+strings.Join(links, " "))
	}
	indent := text[lines[0][2]:lines[0][3]]
	var block strings.Builder
	for _, link := range links {
		block.WriteString(indent + link + "\n")
	}
	var b strings.Builder
	last := 0
	for i, line := range lines {
		b.WriteString(text[last:line[0]])
		if i == 0 {
			b.WriteString(block.String())
		}
		last = line[1]
	}
	b.WriteString(text[last:])

	return b.String(), notes
}

// replaceFirst replaces the first match of the expression with the template.
func replaceFirst(re *regexp.Regexp, text, template string) string {
	at := re.FindStringSubmatchIndex(text)
	if at == nil {
		return text
	}

	return text[:at[0]] + string(re.ExpandString(nil, template, text, at)) + text[at[1]:]
}

// dollars spells a value so a replacement template takes it as it is.
func dollars(value string) string {
	return strings.ReplaceAll(value, "$", "$$")
}

// frameName is the name the frame is given in the application configuration: the first
// name of provideFrame's own keys.
var frameName = regexp.MustCompile(`(provideFrame\(\{[^{}]*?\bname:\s*)(?:` + singleQuoted + `|` + doubleQuoted + `)`)

// setFrameName writes the name the frame draws into the application configuration.
func setFrameName(text, name string) (written string, notes []string) {
	if !frameName.MatchString(text) {
		return text, []string{fmt.Sprintf("it gives the frame no name the command could find (provideFrame({ name: '...' })); write the name there: name: %s", tsString(name))}
	}

	return replaceFirst(frameName, text, "${1}"+dollars(tsString(name))), nil
}

// tsString spells a value as a TypeScript string the way the skeletons' prettier leaves
// one: in single quotes, unless the value holds more of them than double quotes.
func tsString(value string) string {
	quote := `'`
	if strings.Count(value, `'`) > strings.Count(value, `"`) {
		quote = `"`
	}
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, quote, `\`+quote)

	return quote + escaped + quote
}

// The palettes an application offers, as its configuration lists them for the picker:
// the list provideTheme is given, and one entry of it.
var (
	themePalettes = regexp.MustCompile(`provideTheme\(\{\s*palettes:\s*\[`)
	paletteEntry  = regexp.MustCompile(`\{\s*id:\s*'([^']*)',\s*label:\s*(?:` + singleQuoted + `|` + doubleQuoted + `)\s*\}`)
)

// setPaletteLabel names the brand's palette in the list the picker offers: the entry
// with the brand's id takes the brand's name as its label, and where no entry has that
// id the first one becomes it, as the theme's first palette became the brand's. A
// configuration that lists no palettes is left as it is.
func setPaletteLabel(text, name string) (written string, notes []string) {
	entry := fmt.Sprintf("{ id: '%s', label: %s }", brandPaletteID, tsString(name))
	missing := []string{fmt.Sprintf("the palettes it lists for the picker name none the command could find ({ id: '...', label: '...' }); list the brand's first: %s", entry)}
	start := themePalettes.FindStringIndex(text)
	if start == nil {
		// An application offering one palette lists none: its picker offers light and
		// dark alone, and there is no name to write.
		return text, nil
	}
	end := strings.Index(text[start[1]:], "]")
	if end < 0 {
		return text, missing
	}
	list := text[start[1] : start[1]+end]
	entries := paletteEntry.FindAllStringSubmatchIndex(list, -1)
	if entries == nil {
		return text, missing
	}
	chosen := entries[0]
	for _, e := range entries {
		if list[e[2]:e[3]] == brandPaletteID {
			chosen = e

			break
		}
	}
	list = list[:chosen[0]] + entry + list[chosen[1]:]

	return text[:start[1]] + list + text[start[1]+end:], nil
}

// The theme sheet's own places: its module uses, the palettes it hands the library's
// mixin, and one palette of them by its quoted id.
var (
	sassUse       = regexp.MustCompile(`(?m)^@use [^;]*;[ \t]*\n`)
	paletteUse    = regexp.MustCompile(`(?m)^@use\s+['"](?:\./)?theme-colors['"]`)
	palettesMixin = regexp.MustCompile(`@include\s+[\w-]+\.palettes\(`)
	paletteKey    = regexp.MustCompile(`(?m)^([ \t]*)'([^']+)'\s*:\s*\(`)
)

// paletteUseLine is how the theme sheet takes the generated palette, under the name the
// brand's entry reads it by.
const paletteUseLine = "@use './theme-colors' as brand;\n"

// setTheme makes the brand's palette the theme's first: the sheet uses the generated
// palette file, and the first palette it hands the library's mixin becomes the brand's,
// reading the generated primary and tertiary. A sheet that already carries the brand's
// palette is left as it is: only the generated file changes with the color.
func setTheme(text string) (written string, notes []string) {
	mixin := palettesMixin.FindStringIndex(text)
	if mixin == nil {
		return text, []string{"it hands no palettes to the library's mixin (@include theme.palettes((...))); make the brand's the theme's first palette: " + brandEntry("", " ")}
	}
	body := text[mixin[1]:]
	keys := paletteKey.FindAllStringSubmatchIndex(body, -1)
	if keys == nil {
		return text, []string{"the palettes it hands the library's mixin name none the command could find ('id': (...)); make the brand's the first: " + brandEntry("", " ")}
	}
	for _, key := range keys {
		if body[key[4]:key[5]] == brandPaletteID {
			return useGenerated(text)
		}
	}
	first := keys[0]
	end := closing(body, first[1]-1)
	if end < 0 {
		return text, []string{"its first palette does not close; make the brand's the first: " + brandEntry("", " ")}
	}
	indent := body[first[2]:first[3]]
	body = body[:first[0]] + brandEntry(indent, "\n") + body[end+1:]

	return useGenerated(text[:mixin[1]] + body)
}

// brandEntry is the brand's palette as the theme sheet hands it to the library's mixin,
// indented as its place is and broken as separator says.
func brandEntry(indent, separator string) string {
	inner := indent + "  "
	if separator == " " {
		inner = ""
	}

	return indent + "'" + brandPaletteID + "': (" + separator +
		inner + "primary: brand.$primary-palette," + separator +
		inner + "tertiary: brand.$tertiary-palette," + separator +
		indent + ")"
}

// useGenerated adds the use of the generated palette file after the sheet's last use,
// where the sheet does not take it yet.
func useGenerated(text string) (written string, notes []string) {
	if paletteUse.MatchString(text) {
		return text, nil
	}
	uses := sassUse.FindAllStringIndex(text, -1)
	if uses == nil {
		return text, []string{"it uses no module the command could add the generated palette after; add at its top: " + strings.TrimSpace(paletteUseLine)}
	}
	at := uses[len(uses)-1][1]

	return text[:at] + paletteUseLine + text[at:], nil
}

// closing finds the parenthesis that closes the one at open, or -1.
func closing(text string, open int) int {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}
