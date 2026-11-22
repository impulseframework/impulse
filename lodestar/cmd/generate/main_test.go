package generate

import (
	"flag"
	"fmt"
	"os"
	"testing"
)

// generatedAtStart is the hash of every generated file of the module as it was when
// this package's tests started, before any of them regenerated the tree in place
// (TestAuditFindings and TestSchemaWarnings run the generator in-process). It is nil
// under -short, where nothing regenerates.
var generatedAtStart map[string][32]byte

// TestMain hashes the generated files before any test of the package runs, so
// TestGeneratedCodeIsCommitted compares the run's output with the tree as it was on
// disk, whichever test regenerated it first.
func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Short() {
		root, err := moduleRoot()
		if err == nil {
			generatedAtStart, err = hashGeneratedFiles(root)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "hashing the generated files before the tests: %v\n", err)
			os.Exit(1)
		}
	}

	os.Exit(m.Run())
}
