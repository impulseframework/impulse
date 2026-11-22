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
// of the 99,839 passwords of the list published as the 100,000 most often found in data
// breaches, sorted ascending, each once.
//
//go:embed cracked.bin
var cracked []byte

// listed says whether password, whole and as typed, is on the list of passwords found in
// breaches. The password is hashed in pieces of 1,024 bytes, each copied into one array, so a
// long password is never copied whole.
func listed(password string) bool {
	hash := sha256.New()
	var piece [1024]byte
	for rest := password; rest != ""; {
		n := copy(piece[:], rest)
		hash.Write(piece[:n])
		rest = rest[n:]
	}
	key := hash.Sum(nil)[:entrySize]
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
