package members

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/resource"
)

// TestNew_refusesAMissingCollection pins that the members auth refuses settings that leave
// the permission collection out, as it refuses each of its other required settings: with
// a sentence that names the setting and says what to pass. A nil pointer of a collection's
// type is a missing collection too: a variable nothing was assigned to hands one over,
// and the interface that holds it is not nil.
func TestNew_refusesAMissingCollection(t *testing.T) {
	t.Parallel()

	const want = "members.Settings.Collection is required: the permission engine holds every grant to the permission collection the release declares; pass the generated router's (router.Collection())"
	var unassigned *resource.GeneratedCollection
	tests := []struct {
		name       string
		collection access.PermissionCollection
	}{
		{name: "left out"},
		{name: "a nil pointer of a collection's type", collection: unassigned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			auth, err := New(t.Context(), nil, &Settings{Collection: tt.collection})
			if err == nil || !strings.HasSuffix(err.Error(), want) {
				t.Fatalf("New() error = %v, want one ending %q", err, want)
			}
			if auth != nil {
				t.Error("New() returned an auth beside the error, want none")
			}
		})
	}
}
