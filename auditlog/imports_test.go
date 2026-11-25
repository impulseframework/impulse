package auditlog

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestImports holds the package free of database code: neither it nor anything it
// depends on is a database driver, and it depends on none of the libraries that write its
// rows or on the data library, so the session library and the permission store, which run
// on PostgreSQL too, can import it. database/sql is not refused: it opens nothing without
// a driver, and the module's root package reaches it through github.com/gofrs/uuid.
func TestImports(t *testing.T) {
	t.Parallel()

	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps}, ".")
	if err != nil {
		t.Fatalf("packages.Load() error = %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("packages.Load() loaded %d packages, want 1", len(pkgs))
	}
	for _, e := range pkgs[0].Errors {
		t.Fatalf("packages.Load() package error = %v", e)
	}

	packages.Visit(pkgs, func(p *packages.Package) bool {
		if p != pkgs[0] {
			if why := forbiddenDependency(p.PkgPath); why != "" {
				t.Errorf("auditlog depends on %s, %s", p.PkgPath, why)
			}
		}

		return true
	}, nil)
}

// forbiddenDependency says why the package must not depend on the package at path, and ""
// where it may.
func forbiddenDependency(path string) string {
	for _, driver := range []string{"cloud.google.com/go/spanner", "github.com/jackc/pgx", "github.com/jackc/pgconn", "github.com/lib/pq"} {
		if strings.HasPrefix(path, driver) {
			return "which is a database driver"
		}
	}
	const module = "github.com/impulseframework/impulse/"
	if path == module+"session/sessioninfo" {
		return ""
	}
	for _, library := range []string{"resource", "identity", "access", "session"} {
		if path == module+library || strings.HasPrefix(path, module+library+"/") {
			return "which is the data library or a library that writes the log's rows"
		}
	}

	return ""
}

// TestForbiddenDependency pins what TestImports refuses: the database drivers and the
// module's data library and the libraries that write the log's rows, but session's
// sessioninfo, and not database/sql, which opens nothing without a driver (the module's
// root package reaches it through github.com/gofrs/uuid).
func TestForbiddenDependency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path      string
		forbidden bool
	}{
		{path: "database/sql", forbidden: false},
		{path: "database/sql/driver", forbidden: false},
		{path: "github.com/gofrs/uuid", forbidden: false},
		{path: "cloud.google.com/go/spanner", forbidden: true},
		{path: "cloud.google.com/go/spanner/apiv1", forbidden: true},
		{path: "github.com/jackc/pgx/v5", forbidden: true},
		{path: "github.com/jackc/pgconn", forbidden: true},
		{path: "github.com/lib/pq", forbidden: true},
		{path: "github.com/impulseframework/impulse", forbidden: false},
		{path: "github.com/impulseframework/impulse/accesstypes", forbidden: false},
		{path: "github.com/impulseframework/impulse/internal/words", forbidden: false},
		{path: "github.com/impulseframework/impulse/session/sessioninfo", forbidden: false},
		{path: "github.com/impulseframework/impulse/resource", forbidden: true},
		{path: "github.com/impulseframework/impulse/identity", forbidden: true},
		{path: "github.com/impulseframework/impulse/access", forbidden: true},
		{path: "github.com/impulseframework/impulse/access/spannerstore", forbidden: true},
		{path: "github.com/impulseframework/impulse/session", forbidden: true},
		{path: "github.com/impulseframework/impulse/session/sessionstorage", forbidden: true},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			if got := forbiddenDependency(tt.path); (got != "") != tt.forbidden {
				t.Errorf("forbiddenDependency(%q) = %q, want forbidden %v", tt.path, got, tt.forbidden)
			}
		})
	}
}
