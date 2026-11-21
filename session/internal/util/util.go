// Package util is used for general utility function such as generic sorting/filtering and more.
package util

import (
	"slices"
)

// LowerASCII is s with its ASCII capital letters lowercased and every other byte as it
// was written. An email address or a domain is compared under it, never under Unicode's
// lowercasing or case folding: those take some letters from outside ASCII for ASCII ones
// (the Kelvin sign, U+212A, for k; the long s, U+017F, for s; the dotted capital I,
// U+0130, lowercases to i), and a name written with one is another name than the ASCII
// one it would then read as.
func LowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		b[i] = lowerASCII(c)
	}

	return string(b)
}

// EqualASCIIFold reports whether a and b are the same but for the case of their ASCII
// letters: what strings.EqualFold answers for ASCII text, while a character from outside
// ASCII equals only itself (see LowerASCII).
func EqualASCIIFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}

	return true
}

// lowerASCII is c lowercased when it is an ASCII capital letter, and c otherwise.
func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + ('a' - 'A')
	}

	return c
}

// Exclude returns all elements that exist in source but not exclude
func Exclude[T comparable](source, exclude []T) []T {
	list := make([]T, 0, len(source))
	for _, item := range source {
		if slices.Contains(exclude, item) {
			continue
		}
		list = append(list, item)
	}

	return list
}
