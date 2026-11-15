package sessioninfo

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/httpio"
)

func TestCleanName(t *testing.T) {
	t.Parallel()

	text := func(s string) *string {
		return &s
	}
	tests := []struct {
		name    string
		in      *string
		want    *string
		refused bool
	}{
		{name: "no name is none", in: nil, want: nil},
		{name: "an empty name is none", in: text(""), want: nil},
		{name: "nothing but space is none", in: text(" \t\n"), want: nil},
		{name: "the space around a name goes", in: text("  Gale Reyes \n"), want: text("Gale Reyes")},
		{name: "the space inside a name stays", in: text("Gale  de la  Reyes"), want: text("Gale  de la  Reyes")},
		{name: "the longest name is kept, counted in characters", in: text(strings.Repeat("é", MaxNameLength)), want: text(strings.Repeat("é", MaxNameLength))},
		{name: "one character more is refused", in: text(strings.Repeat("é", MaxNameLength+1)), refused: true},
		{name: "the length is taken after the space around the name has gone", in: text(" " + strings.Repeat("a", MaxNameLength) + " "), want: text(strings.Repeat("a", MaxNameLength))},
		{name: "a name in any script is kept", in: text("吉田 さくら"), want: text("吉田 さくら")},
		{name: "a NUL in a name is refused: a database keeps none in text", in: text("Gale\x00Reyes"), refused: true},
		{name: "a line break inside a name is refused", in: text("Gale\nReyes"), refused: true},
		{name: "a tab inside a name is refused", in: text("Gale\tReyes"), refused: true},
		{name: "bytes that are no text are refused", in: text("Gale\xffReyes"), refused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := CleanName(tt.in)
			if tt.refused {
				if !httpio.HasBadRequest(err) {
					t.Fatalf("CleanName() error = %v, want a bad request", err)
				}

				return
			}
			if err != nil {
				t.Fatalf("CleanName() error = %v", err)
			}
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("CleanName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKeptName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "a name is kept without the space around it", in: "  Pat Example ", want: "Pat Example"},
		{name: "no name is none", in: "", want: ""},
		{name: "a name too long to keep is dropped", in: strings.Repeat("a", MaxNameLength+1), want: ""},
		{name: "a name that is not one line of text is dropped", in: "Pat\x00Example", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := KeptName(tt.in); got != tt.want {
				t.Errorf("KeptName() = %q, want %q", got, tt.want)
			}
		})
	}
}
