package skeleton

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// liveStoreDir is the Firestore live driver's directory, from the repository root: its
// rules and index declarations sit beside its code.
const liveStoreDir = "resource/live/firestore"

// TestCandidateFirestoreFiles holds the rules and the index declarations every candidate
// carries (schema/firestore) to the files the Firestore live driver keeps beside its
// code. A new application starts with its candidate's copies and its deployment applies
// them, and a real database answers the driver's lookups only with the indexes the
// driver's own file declares (the emulator needs none, so no test of the driver sees a
// copy fall behind). A lookup that changes its index changes the four copies in the same
// change, or an application made after it deploys an index the driver no longer asks.
func TestCandidateFirestoreFiles(t *testing.T) {
	t.Parallel()

	candidates, err := Candidates()
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}

	for _, name := range []string{"firestore.rules", "firestore.indexes.json"} {
		want, err := os.ReadFile(filepath.Join(repositoryRoot(), filepath.FromSlash(liveStoreDir), name))
		if err != nil {
			t.Fatalf("os.ReadFile() error = %v", err)
		}

		for _, candidate := range candidates {
			t.Run(candidate.Name+"/"+name, func(t *testing.T) {
				t.Parallel()

				sub, err := FS(candidate.Name)
				if err != nil {
					t.Fatalf("FS() error = %v", err)
				}
				got, err := fs.ReadFile(sub, "schema/firestore/"+name)
				if err != nil {
					t.Fatalf("fs.ReadFile() error = %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("the candidate's schema/firestore/%s is not the driver's %s/%s: copy the driver's file over it", name, liveStoreDir, name)
				}
			})
		}
	}
}
