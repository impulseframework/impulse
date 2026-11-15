package sessioninfo

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/httpio"
)

// TestCleanLook pins the one rule a look is held to: the scheme is light, dark or none,
// the palette's id is kept without the space around it and is at most MaxPaletteLength
// characters, and a look of nothing names nothing.
func TestCleanLook(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		look    Look
		want    Look
		wantErr string
	}{
		{name: "a scheme and a palette are kept", look: Look{Scheme: "dark", Palette: "workspace"}, want: Look{Scheme: "dark", Palette: "workspace"}},
		{name: "the space around each is dropped", look: Look{Scheme: " light ", Palette: "  blue "}, want: Look{Scheme: "light", Palette: "blue"}},
		{name: "a scheme alone", look: Look{Scheme: "dark"}, want: Look{Scheme: "dark"}},
		{name: "a palette alone", look: Look{Palette: "blue"}, want: Look{Palette: "blue"}},
		{name: "nothing names nothing", look: Look{Scheme: " ", Palette: " "}, want: Look{}},
		{name: "a scheme that is neither is refused", look: Look{Scheme: "sepia"}, wantErr: "a scheme is light or dark"},
		{name: "a scheme in another case is refused", look: Look{Scheme: "Dark"}, wantErr: "a scheme is light or dark"},
		{name: "the longest palette id is kept", look: Look{Palette: strings.Repeat("p", MaxPaletteLength)}, want: Look{Palette: strings.Repeat("p", MaxPaletteLength)}},
		{name: "a longer one is refused", look: Look{Palette: strings.Repeat("p", MaxPaletteLength+1)}, wantErr: "a palette's id is at most 64 characters"},
		{name: "a palette id with a control character is refused", look: Look{Palette: "blue\x00"}, wantErr: "a palette's id is text on one line: it holds no control character"},
		{name: "a palette id of bytes that are no text is refused", look: Look{Palette: "bl\xffue"}, wantErr: "a palette's id is text on one line: it holds no control character"},
		{name: "the length is counted in characters", look: Look{Palette: strings.Repeat("é", MaxPaletteLength)}, want: Look{Palette: strings.Repeat("é", MaxPaletteLength)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := CleanLook(tt.look)
			if tt.wantErr != "" {
				if !httpio.HasBadRequest(err) || httpio.Message(err) != tt.wantErr {
					t.Fatalf("CleanLook() error = %v, want the bad request %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("CleanLook() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("CleanLook() = %+v, want %+v", got, tt.want)
			}
			if got.IsZero() != (tt.want == Look{}) {
				t.Errorf("IsZero() = %v for %+v", got.IsZero(), got)
			}
		})
	}
}
