// Command makecracked writes cracked.bin, the list of passwords found in breaches that the
// judgment of a password refuses whatever its estimate. The list is the 100,000 passwords
// most often found in data breaches, which the UK's national cyber security agency, the NCSC,
// published from Have I Been Pwned's Pwned Passwords. The NCSC's own address no longer serves
// it, so the copy read is the one the SecLists project keeps, pinned to one commit and checked
// against its SHA-256 before anything is written. For each line it writes the first six
// bytes of the line's SHA-256, sorted ascending and each once, with no header: the passwords
// themselves are not in the repository, and every run writes the same bytes. It takes no
// arguments and writes cracked.bin in the directory it runs in, which go generate makes the
// package's.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
)

const (
	// source is the list, at the SecLists commit that last changed it.
	source = "https://raw.githubusercontent.com/danielmiessler/SecLists/1a7bb9127eca9e6ff2fc0301c597fe6e16a0cb56/" +
		"Passwords/Common-Credentials/100k-most-used-passwords-NCSC.txt"

	// sourceSHA256 is the SHA-256 of the list's bytes as source served them when it was
	// chosen: other bytes are refused.
	sourceSHA256 = "c2e5696882c603b76bb67a47ee970897e5a76fc4c3f5547abe3d0ca340c576e0"

	// entrySize is how many bytes of each password's SHA-256 an entry keeps; cracked.go reads
	// entries of the same size.
	entrySize = 6

	// maxSource bounds what is read from source.
	maxSource = 16 << 20

	output = "cracked.bin"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "makecracked:", err)
		os.Exit(1)
	}
}

// run fetches the list, checks it and writes output.
func run(ctx context.Context) error {
	if len(os.Args) > 1 {
		return errors.New("makecracked takes no arguments")
	}

	list, err := fetch(ctx)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(list)
	if got := hex.EncodeToString(sum[:]); got != sourceSHA256 {
		return errors.Newf("the list's SHA-256 is %s, not %s: the source changed, and nothing was written", got, sourceSHA256)
	}

	if err := os.WriteFile(output, entriesOf(list), 0o644); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}

	return nil
}

// fetch answers the bytes source serves.
func fetch(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, http.NoBody)
	if err != nil {
		return nil, errors.Wrap(err, "http.NewRequestWithContext()")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "http.Client.Do()")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Newf("%s answered %s", source, resp.Status)
	}
	list, err := io.ReadAll(io.LimitReader(resp.Body, maxSource))
	if err != nil {
		return nil, errors.Wrap(err, "io.ReadAll()")
	}

	return list, nil
}

// entriesOf is the table written for list: for each line without its line ending, empty
// lines skipped, the first entrySize bytes of the line's SHA-256, sorted ascending and each
// once.
func entriesOf(list []byte) []byte {
	var entries [][]byte
	for line := range bytes.SplitSeq(list, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 {
			continue
		}
		sum := sha256.Sum256(line)
		entries = append(entries, sum[:entrySize])
	}
	slices.SortFunc(entries, bytes.Compare)
	entries = slices.CompactFunc(entries, bytes.Equal)

	return bytes.Join(entries, nil)
}
