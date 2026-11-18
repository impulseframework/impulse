package brand

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestEdits holds each edit of a browser file to its place: the brand written where the
// place is, the text around it kept, and a note that says what to add where the place is
// not there.
func TestEdits(t *testing.T) {
	t.Parallel()

	d := &Declaration{Name: `Joe's "Harbor"`, Logo: "logo.png", PrimaryColor: "#b3261e"}
	tests := []struct {
		name      string
		edit      func(text string) (string, []string)
		text      string
		want      string
		wantNotes []string
	}{
		{
			name: "a manifest on one line keeps its shape",
			edit: func(text string) (string, []string) { return setManifest(text, d) },
			text: `{"name":"Solo","short_name":"Solo","theme_color":"#005cbb","background_color":"#f6f7fb","icons":[{"src":"icons/icon-192.png"}]}`,
			want: `{"name":"Joe's \"Harbor\"","short_name":"Joe's \"Harbor\"","theme_color":"#b3261e","background_color":"#ffffff","icons":[{"src":"icons/icon-192.png"}]}`,
		},
		{
			name:      "a manifest without colors is told which to add",
			edit:      func(text string) (string, []string) { return setManifest(text, d) },
			text:      "{\n  \"name\": \"Solo\",\n  \"short_name\": \"Solo\"\n}\n",
			want:      "{\n  \"name\": \"Joe's \\\"Harbor\\\"\",\n  \"short_name\": \"Joe's \\\"Harbor\\\"\"\n}\n",
			wantNotes: []string{`it has no "theme_color" to write the primary color in; add "theme_color": "#b3261e"`, `it has no "background_color" to write the background color in; add "background_color": "#ffffff"`},
		},
		{
			name: "an entry document: the title escaped, the icon links replaced where the first stood",
			edit: func(text string) (string, []string) { return setIndex(text, d, true) },
			text: "<head>\n  <title>Solo</title>\n  <meta name=\"theme-color\" content=\"#005cbb\" />\n  <link rel=\"icon\" type=\"image/svg+xml\" href=\"favicon.svg\" />\n  <link rel=\"manifest\" href=\"manifest.webmanifest\" />\n  <link rel=\"icon\" href=\"old.ico\" />\n</head>\n",
			want: "<head>\n  <title>Joe&#39;s &#34;Harbor&#34;</title>\n  <meta name=\"theme-color\" content=\"#b3261e\" />\n  <link rel=\"icon\" type=\"image/png\" sizes=\"96x96\" href=\"favicon.png\" />\n  <link rel=\"icon\" type=\"image/svg+xml\" href=\"favicon.svg\" />\n  <link rel=\"manifest\" href=\"manifest.webmanifest\" />\n</head>\n",
		},
		{
			name: "an entry document with none of the places is told each",
			edit: func(text string) (string, []string) { return setIndex(text, d, false) },
			text: "<head>\n</head>\n",
			want: "<head>\n</head>\n",
			wantNotes: []string{
				"it has no <title> to write the name in; add <title>Joe&#39;s &#34;Harbor&#34;</title> to its head",
				`it has no theme-color to write the primary color in; add <meta name="theme-color" content="#b3261e" /> to its head`,
				`it links no icon (no <link rel="icon"> on a line of its own); add to its head: <link rel="icon" type="image/png" sizes="96x96" href="favicon.png" />`,
			},
		},
		{
			name: "the frame's name over several lines, in the quotes the formatter would choose",
			edit: func(text string) (string, []string) { return setFrameName(text, "Joe's Harbor") },
			text: "    provideFrame({\n      name: 'Tenanted',\n      home: 'dashboard',\n      outside: { label: 'Configuration', icon: 'settings' },\n    }),\n    provideOther({ name: 'kept' }),\n",
			want: "    provideFrame({\n      name: \"Joe's Harbor\",\n      home: 'dashboard',\n      outside: { label: 'Configuration', icon: 'settings' },\n    }),\n    provideOther({ name: 'kept' }),\n",
		},
		{
			name:      "a configuration giving the frame no name is told where it goes",
			edit:      func(text string) (string, []string) { return setFrameName(text, "Harbor") },
			text:      "    provideFrame(frameOptions),\n",
			want:      "    provideFrame(frameOptions),\n",
			wantNotes: []string{"it gives the frame no name the command could find (provideFrame({ name: '...' })); write the name there: name: 'Harbor'"},
		},
		{
			name: "the picker's list: the brand's entry is renamed where it is, wherever it stands",
			edit: func(text string) (string, []string) { return setPaletteLabel(text, "Harbor") },
			text: "    provideTheme({\n      palettes: [\n        { id: 'workspace', label: 'Workspace' },\n        { id: 'brand', label: 'Solo' },\n      ],\n    }),\n",
			want: "    provideTheme({\n      palettes: [\n        { id: 'workspace', label: 'Workspace' },\n        { id: 'brand', label: 'Harbor' },\n      ],\n    }),\n",
		},
		{
			name: "the picker's list on one line: the first entry becomes the brand's",
			edit: func(text string) (string, []string) { return setPaletteLabel(text, "Harbor") },
			text: "provideTheme({ palettes: [{ id: 'blue', label: 'Blue' }, { id: 'workspace', label: 'Workspace' }] })",
			want: "provideTheme({ palettes: [{ id: 'brand', label: 'Harbor' }, { id: 'workspace', label: 'Workspace' }] })",
		},
		{
			name: "a configuration listing no palettes is left without a word",
			edit: func(text string) (string, []string) { return setPaletteLabel(text, "Harbor") },
			text: "    provideTheme(),\n",
			want: "    provideTheme(),\n",
		},
		{
			name: "a theme sheet: the first palette becomes the brand's and the others stay",
			edit: setTheme,
			text: "@use '@angular/material' as mat;\n@use '@impulseframework/resource-angular/theme';\n\n@include theme.palettes(\n  (\n    'blue': (\n      primary: mat.$azure-palette,\n      tertiary: mat.$blue-palette,\n    ),\n    'night': (\n      primary: mat.$violet-palette,\n    ),\n  )\n);\n",
			want: "@use '@angular/material' as mat;\n@use '@impulseframework/resource-angular/theme';\n@use './theme-colors' as brand;\n\n@include theme.palettes(\n  (\n    'brand': (\n      primary: brand.$primary-palette,\n      tertiary: brand.$tertiary-palette,\n    ),\n    'night': (\n      primary: mat.$violet-palette,\n    ),\n  )\n);\n",
		},
		{
			name: "a theme sheet carrying the brand's palette is left as it is, wherever the palette stands",
			edit: setTheme,
			text: "@use '@angular/material' as mat;\n@use './theme-colors' as brand;\n\n@include theme.palettes(\n  (\n    'night': (\n      primary: mat.$violet-palette,\n    ),\n    'brand': (\n      primary: brand.$primary-palette,\n      tertiary: mat.$orange-palette,\n    ),\n  )\n);\n",
			want: "@use '@angular/material' as mat;\n@use './theme-colors' as brand;\n\n@include theme.palettes(\n  (\n    'night': (\n      primary: mat.$violet-palette,\n    ),\n    'brand': (\n      primary: brand.$primary-palette,\n      tertiary: mat.$orange-palette,\n    ),\n  )\n);\n",
		},
		{
			name:      "a theme sheet handing the mixin no palettes is told what to add",
			edit:      setTheme,
			text:      "@use '@angular/material' as mat;\n\nhtml {\n  @include mat.theme((color: mat.$azure-palette));\n}\n",
			want:      "@use '@angular/material' as mat;\n\nhtml {\n  @include mat.theme((color: mat.$azure-palette));\n}\n",
			wantNotes: []string{"it hands no palettes to the library's mixin (@include theme.palettes((...))); make the brand's the theme's first palette: 'brand': ( primary: brand.$primary-palette, tertiary: brand.$tertiary-palette, )"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, notes := tt.edit(tt.text)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the edit mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantNotes, notes); diff != "" {
				t.Errorf("the notes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestIdentifier holds a project's name to the identifier the server's package calls its
// brand by.
func TestIdentifier(t *testing.T) {
	t.Parallel()

	for project, want := range map[string]string{"console": "Console", "admin-console": "AdminConsole", "fieldOps": "FieldOps", "portal2": "Portal2", "2go": "Go", "--": ""} {
		if got := identifier(project); got != want {
			t.Errorf("identifier(%q) = %q, want %q", project, got, want)
		}
	}
}
