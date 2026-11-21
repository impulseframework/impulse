package util

import (
	"reflect"
	"testing"
)

func TestExclude(t *testing.T) {
	t.Parallel()

	type args struct {
		source  []int
		exclude []int
	}
	tests := []struct {
		name string
		args args
		want []int
	}{
		{
			name: "intersection",
			args: args{
				source:  []int{1, 2, 3, 4},
				exclude: []int{2, 4},
			},
			want: []int{1, 3},
		},
		{
			name: "no intersection",
			args: args{
				source:  []int{1, 2, 3, 4},
				exclude: []int{5, 6},
			},
			want: []int{1, 2, 3, 4},
		},
		{
			name: "complete overlap",
			args: args{
				source:  []int{1, 2},
				exclude: []int{1, 2},
			},
			want: []int{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Exclude(tt.args.source, tt.args.exclude); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Exclude() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLowerASCII(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "ASCII capitals are lowercased", in: "Kim@Kirkwood.EDU", want: "kim@kirkwood.edu"},
		{name: "the Kelvin sign, which Unicode lowercases to k, stays as written", in: "\u212Aim@KIRKWOOD.edu", want: "\u212Aim@kirkwood.edu"},
		{name: "the dotted capital I, which Unicode lowercases to i, stays as written", in: "K\u0130M", want: "k\u0130m"},
		{name: "bytes that are no text stay as written", in: "A\xffB", want: "a\xffb"},
		{name: "nothing", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := LowerASCII(tt.in); got != tt.want {
				t.Errorf("LowerASCII(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEqualASCIIFold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "the same text", a: "kim@kirkwood.edu", b: "kim@kirkwood.edu", want: true},
		{name: "ASCII letters in another case", a: "Kim@Kirkwood.EDU", b: "kIM@kirkwood.edu", want: true},
		{name: "the Kelvin sign is not k", a: "\u212Aim@kirkwood.edu", b: "kim@kirkwood.edu"},
		{name: "the Kelvin sign is not K", a: "\u212Aim@kirkwood.edu", b: "Kim@kirkwood.edu"},
		{name: "the long s is not s", a: "ki\u017F@kirkwood.edu", b: "kis@kirkwood.edu"},
		{name: "a letter from outside ASCII equals itself beside ASCII letters in another case", a: "\u212Aim@KIRKWOOD.edu", b: "\u212Aim@kirkwood.EDU", want: true},
		{name: "a letter from outside ASCII does not equal its own other case", a: "\u00C9a", b: "\u00E9a"},
		{name: "bytes that are no text equal only themselves", a: "a\xff", b: "a\xfe"},
		{name: "another length", a: "kim", b: "kimm"},
		{name: "nothing equals nothing", a: "", b: "", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := EqualASCIIFold(tt.a, tt.b); got != tt.want {
				t.Errorf("EqualASCIIFold(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			if got := EqualASCIIFold(tt.b, tt.a); got != tt.want {
				t.Errorf("EqualASCIIFold(%q, %q) = %v, want %v", tt.b, tt.a, got, tt.want)
			}
		})
	}
}
