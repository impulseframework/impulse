package crew

import (
	"strings"
	"testing"
)

// TestNew_refusesAMissingCollection pins that the crew auth refuses settings that leave
// the permission collection out, as it refuses each of its other required settings: with
// a sentence that names the setting and says what to pass.
func TestNew_refusesAMissingCollection(t *testing.T) {
	t.Parallel()

	const want = "crew.Settings.Collection is required: the permission engine holds every grant to the permission collection the release declares; pass the generated router's (router.Collection())"
	auth, err := New(t.Context(), nil, &Settings{})
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("New() error = %v, want one ending %q", err, want)
	}
	if auth != nil {
		t.Error("New() returned an auth beside the error, want none")
	}
}
