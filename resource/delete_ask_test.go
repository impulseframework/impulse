package resource

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Test_DeleteRunOf holds how a patch request is run from its body: a request with a remove
// that does not bring its token asks first, whatever else it holds (a remove whose value
// does not say "everythingUnder": true with a token that is not empty, the operation's
// name read without regard to case); a request in which every operation is a remove that
// brings its token is a save; any other request, an empty list and a body that is not a
// list of operations are run plainly, with no error. The body reads again from its start
// once read.
func Test_DeleteRunOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want DeleteRun
	}{
		{name: "a remove with no value", body: `[{"op": "remove", "path": "/p1"}]`, want: DeleteRunAsks},
		{name: "a remove that says it takes everything under, with its token", body: `[{"op": "remove", "path": "/p1", "value": {"everythingUnder": true, "token": "t-1"}}]`, want: DeleteRunSave},
		{name: "a remove that says it takes everything under, with an empty token", body: `[{"op": "remove", "path": "/p1", "value": {"everythingUnder": true, "token": ""}}]`, want: DeleteRunAsks},
		{name: "a remove that says it takes everything under, with no token", body: `[{"op": "remove", "path": "/p1", "value": {"everythingUnder": true}}]`, want: DeleteRunAsks},
		{name: "a remove that brings a token and does not say it takes everything under", body: `[{"op": "remove", "path": "/p1", "value": {"token": "t-1"}}]`, want: DeleteRunAsks},
		{name: "a remove that says it does not", body: `[{"op": "remove", "path": "/p1", "value": {"everythingUnder": false}}]`, want: DeleteRunAsks},
		{name: "a remove named in capitals", body: `[{"op": "REMOVE", "path": "/p1"}]`, want: DeleteRunAsks},
		{name: "a remove named in capitals, with its token", body: `[{"op": "REMOVE", "path": "/p1", "value": {"everythingUnder": true, "token": "t-1"}}]`, want: DeleteRunSave},
		{name: "two removes, each with its token", body: `[{"op": "remove", "path": "/p1", "value": {"everythingUnder": true, "token": "t-1"}}, {"op": "remove", "path": "/p2", "value": {"everythingUnder": true, "token": "t-2"}}]`, want: DeleteRunSave},
		{name: "an add and a patch alone", body: `[{"op": "add", "path": "/p1", "value": {"name": "One"}}, {"op": "patch", "path": "/p2", "value": {"name": "Two"}}]`, want: DeleteRunPlain},
		{name: "a remove that says it beside one that does not", body: `[{"op": "remove", "path": "/p1", "value": {"everythingUnder": true, "token": "t-1"}}, {"op": "remove", "path": "/p2"}]`, want: DeleteRunAsks},
		{name: "an add beside a remove that does not bring a token", body: `[{"op": "add", "path": "/p1", "value": {"name": "One"}}, {"op": "remove", "path": "/p2"}]`, want: DeleteRunAsks},
		{name: "a remove with its token beside an add", body: `[{"op": "remove", "path": "/p1", "value": {"everythingUnder": true, "token": "t-1"}}, {"op": "add", "path": "/p2", "value": {"name": "Two"}}]`, want: DeleteRunPlain},
		{name: "a patch beside a remove with its token", body: `[{"op": "patch", "path": "/p2", "value": {"name": "Two"}}, {"op": "remove", "path": "/p1", "value": {"everythingUnder": true, "token": "t-1"}}]`, want: DeleteRunPlain},
		{name: "an empty list", body: `[]`, want: DeleteRunPlain},
		{name: "a body that is not a list", body: `{"op": "remove", "path": "/p1"}`, want: DeleteRunPlain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/projects", strings.NewReader(tt.body))
			got, err := DeleteRunOf(r)
			if err != nil {
				t.Fatalf("DeleteRunOf() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("DeleteRunOf() = %v, want %v", got, tt.want)
			}
			again, err := CloneRequest(r)
			if err != nil {
				t.Fatalf("CloneRequest() error = %v", err)
			}
			body, err := io.ReadAll(again.Body)
			if err != nil {
				t.Fatalf("io.ReadAll() error = %v", err)
			}
			if string(body) != tt.body {
				t.Errorf("the body read again = %q, want %q", body, tt.body)
			}
		})
	}
}

// Test_DeleteRunOfToken holds how an RPC that declares its delete's token is run: asking
// first while the token is empty, and plainly once it brings one.
func Test_DeleteRunOfToken(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		token string
		want  DeleteRun
	}{
		{token: "", want: DeleteRunAsks},
		{token: "t-1", want: DeleteRunPlain},
	} {
		if got := DeleteRunOfToken(tt.token); got != tt.want {
			t.Errorf("DeleteRunOfToken(%q) = %v, want %v", tt.token, got, tt.want)
		}
	}
}
