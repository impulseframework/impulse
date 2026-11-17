package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/spf13/cobra"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/brand"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
)

func newBrand() *cobra.Command {
	return newBrandWith(check.OSExec{})
}

// newBrandWith builds the brand command over the runner of the commands it runs in a
// browser workspace (the palette's generator and the formatter); tests pass a fake.
func newBrandWith(run brand.Execer) *cobra.Command {
	var appDir string

	cmd := &cobra.Command{
		Use:   "brand",
		Short: "Write each browser application's brand from its brand.json: icons, names, colors, the theme's palette, and what the server says",
		Long: `brand writes an application's brand where the application carries it. Each browser
application declares its brand once, in ` + brand.DeclarationFile + ` in its own directory (web/console in the
flat layout, apps/<site>/web in the sites layout), beside its logo:

    {
      "name": "Harbor",
      "logo": "logo.png",
      "logoSvg": "logo.svg",
      "primaryColor": "#005cbb",
      "backgroundColor": "#f6f7fb"
    }

The name is the application's as people read it. The logo is a square PNG at least 512
pixels a side, which every icon is scaled down from; logoSvg, optional, is the same logo as
an SVG, which becomes the favicon as it is. The colors are six hex digits; backgroundColor
is optional and white without it. One brand per browser application: a console and a
portal of one application carry their own.

From each declaration brand writes: the favicon (favicon.png, and favicon.svg where the
brand has an SVG) and the three app icons (192 and 512 pixels, and the 512 a device cuts to
its own shape, the logo in its central 80% on the background color); the manifest's name,
short_name, theme_color and background_color; the page's title, theme-color and icon
links; the name the frame draws (provideFrame in app.config.ts); and the theme's first
palette, generated from the primary color by Angular Material's own generator (the
theme-color schematic of the installed version, written beside the theme as
_theme-colors.scss), with its name in the list the picker offers. For the server it writes
the package ` + brand.ServerPackage + `, each brand's name as a constant and its logo embedded, which a
main hands to the maintenance page (maintenance.Serve), and the name the mail an auth
sends says (the Application constant of pkg/auth/<name>), from the brand of the browser
application the auth's people sign in to.

The palette's generator needs the workspace's packages installed. Where they are not,
everything else is written, the palette is said to be missing, and the command exits
non-zero: install the packages and run it again.

The files are the application's after the first write: brand runs when asked, never as
part of impulse render, and writes only what the declaration changed. impulse check
reminds, without failing, while a browser application still carries the skeleton's icon.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.Discover(appDir)
			if err != nil {
				return err
			}
			result, err := brand.Apply(cmd.Context(), a, run)
			if err != nil {
				return err
			}
			writeBrandReport(cmd.OutOrStdout(), result)
			if incomplete := result.Incomplete(); len(incomplete) > 0 {
				return errors.Newf("the brand of %s is written but for the theme's palette, which Angular Material's generator writes once the workspace's packages are installed: install them and run impulse brand again", strings.Join(incomplete, " and "))
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&appDir, "app", ".", "application root (the directory holding go.mod)")

	return cmd
}

// writeBrandReport says what the command did: each browser application's brand and
// files, then the server's, then what is left for a person to do.
func writeBrandReport(w io.Writer, result *brand.Result) {
	for i := range result.Apps {
		r := &result.Apps[i]
		fmt.Fprintf(w, "%s: %s\n", r.Dir, r.Brand)
		writeBrandLines(w, r.Writes)
	}
	fmt.Fprintln(w, "The server (the mail the auths send and the maintenance page):")
	writeBrandLines(w, result.Server)
	for _, dir := range result.Undeclared {
		fmt.Fprintf(w, "%s declares no brand (no %s there), so nothing was written for it.\n", dir, brand.DeclarationFile)
	}
	for _, sentence := range result.Unserved {
		fmt.Fprintf(w, "Left to you: %s.\n", sentence)
	}
}

// writeBrandLines writes one line per file: what was done with it, the file, and the note
// of a file left as it was.
func writeBrandLines(w io.Writer, writes []brand.Write) {
	for _, write := range writes {
		line := fmt.Sprintf("  %-9s  %s", write.State, write.File)
		if write.Note != "" {
			line += ": " + write.Note
		}
		fmt.Fprintln(w, line)
	}
}
