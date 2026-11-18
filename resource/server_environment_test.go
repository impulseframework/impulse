package resource

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/go-cmp/cmp"
)

// TestNameEnvironment pins what an answer says of the environment: its name where the
// server is told one, on a route's answer and on the answer for no route alike, encoded
// so the browser's decoding gives the name back whole; and no header at all where the
// server is told none, which is production.
func TestNameEnvironment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		environment string
		want        []string
	}{
		{name: "a test environment is named", environment: "tst", want: []string{"tst"}},
		{name: "a name with a space travels encoded", environment: "Morgan Wells demo", want: []string{"Morgan%20Wells%20demo"}},
		{name: "a name outside ASCII travels encoded", environment: "prüfung/2", want: []string{"pr%C3%BCfung%2F2"}},
		{name: "a line break never reaches the header", environment: "tst\r\nX-Other: 1", want: []string{"tst%0D%0AX-Other:%201"}},
		{name: "production, told no name, names nothing", environment: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := chi.NewRouter()
			r.Use(NameEnvironment(tt.environment))
			r.Get("/api/user/methods", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			for _, path := range []string{"/api/user/methods", "/no-such-route"} {
				rr := httptest.NewRecorder()
				r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
				got := rr.Header().Values(ServerEnvironmentHeader)
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("%s on %s mismatch (-want +got):\n%s", ServerEnvironmentHeader, path, diff)
				}
				for _, value := range got {
					decoded, err := url.PathUnescape(value)
					if err != nil {
						t.Fatalf("url.PathUnescape(%q) error = %v", value, err)
					}
					if decoded != tt.environment {
						t.Errorf("%s on %s decodes to %q, want %q", ServerEnvironmentHeader, path, decoded, tt.environment)
					}
				}
			}
		})
	}
}
