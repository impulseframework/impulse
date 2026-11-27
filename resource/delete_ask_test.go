package resource

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Test_RemovesAsk holds which patch requests hold a remove that asks first: a remove whose
// value does not say "everythingUnder": true, the operation's name read without regard to
// case. A body that is not a list of operations answers false and no error, and the body
// reads again from its start once asked.
func Test_RemovesAsk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "a remove with no value", body: `[{"op": "remove", "path": "/p1"}]`, want: true},
		{name: "a remove that says it takes everything under", body: `[{"op": "remove", "path": "/p1", "value": {"everythingUnder": true}}]`, want: false},
		{name: "a remove that says it does not", body: `[{"op": "remove", "path": "/p1", "value": {"everythingUnder": false}}]`, want: true},
		{name: "a remove named in capitals", body: `[{"op": "REMOVE", "path": "/p1"}]`, want: true},
		{name: "an add and a patch alone", body: `[{"op": "add", "path": "/p1", "value": {"name": "One"}}, {"op": "patch", "path": "/p2", "value": {"name": "Two"}}]`, want: false},
		{name: "a remove that says it beside one that does not", body: `[{"op": "remove", "path": "/p1", "value": {"everythingUnder": true}}, {"op": "remove", "path": "/p2"}]`, want: true},
		{name: "an empty list", body: `[]`, want: false},
		{name: "a body that is not a list", body: `{"op": "remove", "path": "/p1"}`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/projects", strings.NewReader(tt.body))
			got, err := RemovesAsk(r)
			if err != nil {
				t.Fatalf("RemovesAsk() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("RemovesAsk() = %v, want %v", got, tt.want)
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
