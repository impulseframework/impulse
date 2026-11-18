//go:build skipAuth

package integration

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/resource"
)

// TestSessionRoutesRefuseADryRunOrAWarning sends the dry-run header and the warning
// header, each with the value true, to the routes the session library serves, none of
// which has a dry run or a warning: the method list anyone reads, a route a signed-in
// person changes their own account through behind the XSRF guard, a route of the
// signed-in account group, and the device flow's endpoint on the machine outlet.
// Either header asks that the call be held back, so each request is refused, 400
// naming the header, and the call is not run: the person's look is not kept.
func TestSessionRoutesRefuseADryRunOrAWarning(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	cadet := newBrowser(t, s, consoleAPI)
	cadet.signIn(ctx, "cadet")
	for _, header := range []string{resource.DryRunHeader, resource.WarningHeader} {
		asks := map[string]string{header: "true"}
		assertHeldBack(t, newBrowser(t, s, consoleAPI).doWith(ctx, http.MethodGet, consoleAPI+"/user/methods", nil, asks), header)
		assertHeldBack(t, cadet.doWith(ctx, http.MethodPost, consoleAPI+"/user/look", []byte(`{"scheme":"light"}`), asks), header)
		assertHeldBack(t, cadet.doWith(ctx, http.MethodGet, consoleAPI+"/user/tokens", nil, asks), header)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.server.URL+"/droids/oauth/device/authorize", strings.NewReader(`{"client_id":"corral","outlet":"droids"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(header, "true")
		resp, err := s.server.Client().Do(req)
		if err != nil {
			t.Fatalf("POST oauth/device/authorize under %s: %v", header, err)
		}
		assertHeldBack(t, resp, header)
	}
	if scheme, palette, present := lookOf(cadet.session(ctx)); present {
		t.Errorf("after the refused requests the session read answers the look %s/%s, want none: the call was not run", scheme, palette)
	}
}

// assertHeldBack fails unless the response is the refusal of a request that asked a
// call with no dry run or no warning for one: 400, naming the header. It closes the
// body.
func assertHeldBack(t *testing.T, resp *http.Response, header string) {
	t.Helper()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := "the " + header + " header asks for "; resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), want) {
		t.Errorf("%s %s under %s: answered %d %s, want 400 saying %q", resp.Request.Method, resp.Request.URL.Path, header, resp.StatusCode, body, want)
	}
}
