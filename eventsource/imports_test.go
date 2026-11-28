package eventsource

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// module is the path of the module the package is of.
const module = "github.com/impulseframework/impulse"

// refusedDrivers are the paths of the database drivers, each with the packages under it,
// that nothing the package depends on may be.
var refusedDrivers = []string{"cloud.google.com/go/spanner", "github.com/jackc/pgx", "github.com/jackc/pgconn", "github.com/lib/pq"}

// refusedLibraries are the module's libraries, each with the packages under it, that
// nothing the package depends on may be: the data library, the identity package, the
// permission engine and the session library, which take the value from here. The
// session library's sessioninfo holds types and no database code, and the value reads
// the session from it.
var refusedLibraries = []string{"resource", "identity", "access", "session"}

// TestImports pins that the package depends on no database driver and on none of the
// module's libraries that take the value, so that the session library and the permission
// store can use it without importing the data library or a driver. database/sql is not
// refused: the module's root package depends on it through its UUID package, and it opens
// nothing without a driver.
func TestImports(t *testing.T) {
	t.Parallel()

	loaded, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps}, ".")
	if err != nil {
		t.Fatalf("packages.Load(.) error = %v", err)
	}
	packages.Visit(loaded, nil, func(pkg *packages.Package) {
		for _, e := range pkg.Errors {
			t.Errorf("loading %s: %v", pkg.PkgPath, e)
		}
		if why := refusedImport(pkg.PkgPath); why != "" {
			t.Errorf("the package depends on %s, %s", pkg.PkgPath, why)
		}
	})
}

// refusedImport answers why the package may not depend on path, and the empty text where
// it may.
func refusedImport(path string) string {
	under := func(root string) bool {
		return path == root || strings.HasPrefix(path, root+"/")
	}
	for _, driver := range refusedDrivers {
		if under(driver) {
			return "a database driver"
		}
	}
	if path == module+"/session/sessioninfo" {
		return ""
	}
	for _, library := range refusedLibraries {
		if under(module + "/" + library) {
			return "one of the module's libraries that take the value"
		}
	}

	return ""
}
