package resource

import (
	"net/http"
	"net/url"
)

// ServerEnvironmentHeader carries the name of the environment the server runs in (a test
// environment's, for one) on the answers of a server that is told one, so a browser
// application can show where it is and a problem report can say where it happened. A
// server that is told none, which is how production runs, sends no such header. It is
// the deployment's name and nothing a permission check reads: the Environment of a
// request (RequestEnvironment) is another thing.
const ServerEnvironmentHeader = "X-Environment"

// NameEnvironment names the environment the server runs in on every response of the
// routes it wraps: ServerEnvironmentHeader set to the name before the handler runs, so a
// browser application reads it from the same answers it reads the release from
// (ReleaseHeader), the first one it gets included. The generated router puts it in front
// of every route with the name the application reports (ServerEnvironment, the
// configuration's APP_ENVIRONMENT). An empty name names nothing: production is the
// environment that is told no name. The name travels percent-encoded, as a path segment
// is, so one with a space or a letter outside ASCII arrives whole; the browser package
// decodes it.
func NameEnvironment(name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if name == "" {
			return next
		}
		encoded := url.PathEscape(name)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(ServerEnvironmentHeader, encoded)
			next.ServeHTTP(w, r)
		})
	}
}
