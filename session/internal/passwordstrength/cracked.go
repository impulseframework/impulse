package passwordstrength

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"sort"
)

//go:generate go run ./makecracked

// entrySize is how many bytes of a password's SHA-256 an entry of cracked keeps;
// makecracked writes entries of the same size.
const entrySize = 6

// cracked is the table makecracked writes: the first entrySize bytes of the SHA-256 of each
// of the 100,000 passwords most often found in data breaches, sorted ascending, each once.
//
//go:embed cracked.bin
var cracked []byte

// listed says whether password, whole and as typed, is on the list of passwords found in
// breaches.
func listed(password string) bool {
	sum := sha256.Sum256([]byte(password))
	key := sum[:entrySize]
	n := len(cracked) / entrySize
	i := sort.Search(n, func(i int) bool {
		return bytes.Compare(entryAt(i), key) >= 0
	})

	return i < n && bytes.Equal(entryAt(i), key)
}

// entryAt is the i-th entry of cracked.
func entryAt(i int) []byte {
	return cracked[i*entrySize : (i+1)*entrySize]
}
