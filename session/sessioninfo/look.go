package sessioninfo

import (
	"strings"
	"unicode/utf8"

	"github.com/impulseframework/impulse/httpio"
)

// The two schemes a look is drawn in.
const (
	SchemeLight = "light"
	SchemeDark  = "dark"
)

// MaxPaletteLength is the longest palette id a user holds, in characters.
const MaxPaletteLength = 64

// Look is the look a person chose for the application's pages: light or dark, and one of
// the palettes the application offers. The user keeps it, so it follows the person to
// every browser they sign in from; nobody else sets it. Either half may be none: the
// page then draws its default.
type Look struct {
	// Scheme is SchemeLight or SchemeDark, "" for none chosen.
	Scheme string `json:"scheme,omitempty"`
	// Palette is the id of a palette as the application names its palettes, "" for
	// none chosen. The session library does not know the palettes: a page drops an id
	// its application no longer offers.
	Palette string `json:"palette,omitempty"`
}

// IsZero reports whether the look names nothing: no scheme and no palette.
func (l Look) IsZero() bool {
	return l.Scheme == "" && l.Palette == ""
}

// CleanLook is a look as a user row keeps it: the scheme one of the two or none, the
// palette's id without the space around it. A scheme that is neither, a palette id
// longer than MaxPaletteLength, and one that is not text on one line (a control
// character in it) are refused as a bad request. Everything that writes a look holds it
// to this one rule.
func CleanLook(look Look) (Look, error) {
	scheme := strings.TrimSpace(look.Scheme)
	if scheme != "" && scheme != SchemeLight && scheme != SchemeDark {
		return Look{}, httpio.NewBadRequestMessagef("a scheme is %s or %s", SchemeLight, SchemeDark)
	}
	palette := strings.TrimSpace(look.Palette)
	if !oneLineOfText(palette) {
		return Look{}, httpio.NewBadRequestMessage("a palette's id is text on one line: it holds no control character")
	}
	if utf8.RuneCountInString(palette) > MaxPaletteLength {
		return Look{}, httpio.NewBadRequestMessagef("a palette's id is at most %d characters", MaxPaletteLength)
	}

	return Look{Scheme: scheme, Palette: palette}, nil
}
