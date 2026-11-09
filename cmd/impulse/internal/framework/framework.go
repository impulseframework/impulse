// Package framework names the Impulse Framework as an application sees it: the one Go
// module every application requires, the command line's package in it, which the tool
// directive names, and the browser packages, which release with the module at its version.
package framework

import "strings"

const (
	// Module is the framework's module path: the one framework requirement in an
	// application's go.mod, whose version says which release the application builds
	// against.
	Module = "github.com/impulseframework/impulse"
	// Tool is impulse's own package path: the tool directive every application's go.mod
	// carries, so that CI runs go tool impulse check at the release go.mod names, verified
	// by Go's checksum database, with no version written into the workflow. It lives in
	// Module, so the tool and the libraries are one requirement and move together.
	Tool = Module + "/cmd/impulse"
	// Name is how messages name the framework pin ("impulse v0.1.0").
	Name = "impulse"
)

// WebPackages are the browser packages a release publishes beside the module, at the
// module's version without its v: the client, its Angular components and its Firestore
// live transport. Every web root's package.json pins them at the release the application
// builds against.
var WebPackages = []string{"@impulseframework/resource", "@impulseframework/resource-angular", "@impulseframework/resource-firestore"}

// WebVersion is the browser packages' version for a release: the release without its v
// (v0.1.0 publishes 0.1.0).
func WebVersion(release string) string {
	return strings.TrimPrefix(release, "v")
}

// WebPins are the browser packages' pins for a release, by package.
func WebPins(release string) map[string]string {
	pins := make(map[string]string, len(WebPackages))
	for _, name := range WebPackages {
		pins[name] = WebVersion(release)
	}

	return pins
}

// Label names the framework pin at a version as messages and commits do: "impulse v0.1.0".
func Label(version string) string {
	return Name + " " + version
}
