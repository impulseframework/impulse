package signin

import "testing"

func TestAddressDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		email string
		want  string
	}{
		{email: "pat@lakeside.edu", want: "lakeside.edu"},
		{email: "Pat@Lakeside.EDU", want: "lakeside.edu"},
		{email: `"pat@home"@lakeside.edu`, want: "lakeside.edu"},
		{email: "pat", want: ""},
		{email: "", want: ""},
		// Only ASCII letters are lowercased. Unicode lowercases the dotted capital I
		// (U+0130) to i and the Kelvin sign (U+212A) to k: a domain written with one is
		// not the ASCII domain it would then read as.
		{email: "pat@gma\u0130l.com", want: "gma\u0130l.com"},
		{email: "pat@\u212Airkwood.edu", want: "\u212Airkwood.edu"},
		{email: "pat@\u212AIRKWOOD.EDU", want: "\u212Airkwood.edu"},
	}
	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			t.Parallel()

			if got := AddressDomain(tt.email); got != tt.want {
				t.Errorf("AddressDomain(%q) = %q, want %q", tt.email, got, tt.want)
			}
		})
	}
}
