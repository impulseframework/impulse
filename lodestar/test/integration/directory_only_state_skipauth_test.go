//go:build skipAuth

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// TestDirectoryOnlyState_saysHowTheSectorStands proves on the served stack that the read
// of a sector's directory-only switch, which the Sign-in settings page draws from, names
// the sector's directory as people know it: Cinder's is the fleet's Google Workspace,
// whose identities carry the connection example.
func TestDirectoryOnlyState_saysHowTheSectorStands(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	if _, err := s.crew.SeedCast(ctx, io.Discard); err != nil {
		t.Fatalf("SeedCast() error = %v", err)
	}
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")

	status, body := governor.do(ctx, http.MethodGet, sectorPath(cinder, "directory-only"), nil)
	var read struct {
		Directory string `json:"directory"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &read) != nil {
		t.Fatalf("reading Cinder's switch as the governor: status %d: %s", status, body)
	}
	if want := "Google Workspace example"; read.Directory != want {
		t.Errorf("the directory Cinder's switch names = %q, want %q", read.Directory, want)
	}
}
