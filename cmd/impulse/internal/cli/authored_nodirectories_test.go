package cli

import (
	"bytes"
	"go/format"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/cmd/impulse/internal/handoff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/shape"
)

// noDirectoriesSays is what impulse audit says of the staff auth of an application whose
// tenants have no directories.
const noDirectoriesSays = "The tenants have no directories (the tenant record declares none), so a tenant's people come by invitation alone"

// googleForInvited is the method the author of an application whose tenants have no
// directories adds for the people invited into it: any Google account, the application
// giving the roles.
var googleForInvited = []string{"staff", "google", "--slot", "any", "--authority", "application"}

// startsWithNoDirectories holds an application impulse new made with tenants that have no
// directories to what was true of it before a method was added: impulse audit read the
// staff auth as wantShape, a tenant's people coming by invitation alone, and the brief the
// tenancy step left says the tenants have no directories and that laying them in is the
// author's act.
func startsWithNoDirectories(t *testing.T, made *authoredApplication, wantShape string) {
	t.Helper()

	if made.madeAs.Shape == nil || made.madeAs.Shape.String() != wantShape {
		t.Errorf("before a method is added, impulse audit reads the staff auth as %v, want %s", made.madeAs.Shape, wantShape)
	}
	if want := noDirectoriesSays + "."; made.madeAs.Says != want {
		t.Errorf("before a method is added, impulse audit says %q of the staff auth, want %q", made.madeAs.Says, want)
	}
	for _, want := range []string{"No tenant has a directory in this application (`--tenant-directories=false`)", "Giving the application tenant directories later is the author's act and no command's"} {
		if !strings.Contains(made.newBrief, want) {
			t.Errorf("the tenancy brief does not say %q", want)
		}
	}
}

// holdsNoDirectories holds an application whose tenants have no directories, with the
// Google method on it, to its shape and to every step its author and its CI run: impulse
// audit still reads the staff auth as wantShape, naming the method as a way for an invited
// person to sign in and nothing more; the brief the last command left lists no failing
// check; nothing the commands wrote or generated names a tenant's directory; the
// application regenerates to itself, builds, passes impulse check and passes its tests
// under no tag; and, last, it passes its tests under the simulated directory's tag with
// the proof that an invited person signs in through Google and is seated, and a person
// nobody invited is refused.
func holdsNoDirectories(t *testing.T, made *authoredApplication, wantShape string) {
	t.Helper()

	got := readShape(t, made.dir)
	if got.Shape == nil || got.Shape.String() != wantShape {
		t.Errorf("impulse audit reads the staff auth as %v, want %s: a method through an organization's directory does not change the shape of an application whose tenants have no directories", got.Shape, wantShape)
	}
	if want := noDirectoriesSays + ", and the google method is a way for an invited person to sign in and nothing more."; got.Says != want {
		t.Errorf("impulse audit says %q of the staff auth, want %q", got.Says, want)
	}
	if !slices.ContainsFunc(got.Methods, func(m shape.Method) bool { return m.Name == "google" }) {
		t.Errorf("impulse audit reads no google method on the staff auth: %+v", got.Methods)
	}
	if brief, want := made.read(t, handoff.File), "reports no FAIL. The obligations are the lines listed under \"What changed\""; !strings.Contains(brief, want) {
		t.Errorf("the brief the last command left does not say %q: impulse check fails in the application with the Google method on it\n%s", want, brief)
	}
	noTenantDirectory(t, made)
	everyStepAsMade(t, made, []string{"no tenant record declares directories", "1 directory method(s); the simulated directory is confined to development and tests"})
	invitedThroughGoogle(t, made)
}

// noTenantDirectory holds that nothing the commands wrote or the generator emitted gives
// a tenant a directory: no record or table, no switch on the tenant record, no reader or
// admission in the auth or the data level, no route and no page. (The generated code that
// every application carries still says in its comments what the library does with a
// directory, and the generated maker refuses an invitation that asks for one.)
func noTenantDirectory(t *testing.T, made *authoredApplication) {
	t.Helper()

	tree, err := os.OpenRoot(made.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	// absent are the words no file under the path may hold.
	absent := []struct {
		under string
		words []string
	}{
		{under: "schema/", words: []string{"TenantDirector", "DirectoryOnly"}},
		{under: "pkg/resources/", words: []string{"TenantDirector", "DirectoryOnly", "@tenantDirectory", "@directoryOnly", "@directorySecondFactor"}},
		{under: "pkg/auth/", words: []string{"TenantDirectories", "SetDirectoryAdmission", "DirectoryReader"}},
		{under: "pkg/config/", words: []string{"TenantDirectories", "DirectoryReader"}},
		{under: "pkg/router/", words: []string{"TenantDirector", "tenant-director", "DirectoryOnly", "directory-only", "to-directory", "vouch-for", "TenantSignInSettings"}},
		{under: "web/", words: []string{"TenantDirector", "tenantDirector", "tenant-director", "directoryOnly", "DirectoryOnly", "tenantSignInPage", "tenantSignInRoute", "sign-in.routes"}},
	}
	err = fs.WalkDir(tree.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "fs.WalkDir()")
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}

			return nil
		}
		if name := d.Name(); name == "tenant_directories.go" || strings.HasPrefix(name, "zz_gen_tenant_director") || strings.HasPrefix(name, "tenant-sign-in.") {
			t.Errorf("%s is a file of a tenant's directory, in an application whose tenants have no directories", p)
		}
		if !slices.Contains([]string{".go", ".ts", ".html", ".sql", ".json"}, path.Ext(p)) {
			return nil
		}
		src, err := fs.ReadFile(tree.FS(), p)
		if err != nil {
			return errors.Wrap(err, "fs.ReadFile()")
		}
		for _, held := range absent {
			if !strings.HasPrefix(p, held.under) {
				continue
			}
			for _, word := range held.words {
				if bytes.Contains(src, []byte(word)) {
					t.Errorf("%s names %s, in an application whose tenants have no directories", p, word)
				}
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// invitedThroughGoogle drops the framework's proof of the Google method of an application
// whose tenants have no directories into the application's served suite and runs the
// application's tests with it: that one run is the application's own tests with the
// method on it, and the proof that a person invited into a tenant signs in through Google
// and is seated there, that another person of the same Workspace whom nobody invited is
// refused, and that no route of a tenant's directory is served. The proof is written
// against the module example.com/ci/yard, which becomes the application's module path. It
// leaves a file the application's last commit does not hold, so it runs last.
func invitedThroughGoogle(t *testing.T, made *authoredApplication) {
	t.Helper()

	const proof = "testdata/authored/invited_through_google_test.go.txt"
	src, err := os.ReadFile(filepath.FromSlash(proof))
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := format.Source(src)
	if err != nil {
		t.Fatalf("%s does not parse: %v", proof, err)
	}
	if !bytes.Equal(formatted, src) {
		t.Fatalf("%s is not gofmt-clean", proof)
	}
	module := authoredModule + "/" + filepath.Base(made.dir)
	src = bytes.ReplaceAll(src, []byte("example.com/ci/yard"), []byte(module))
	if err := os.WriteFile(filepath.Join(made.dir, "test", "integration", "invited_through_google_test.go"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := made.run(t, "go", "vet", "-tags", "skipAuth", "./test/integration/"); err != nil {
		t.Errorf("go vet -tags skipAuth ./test/integration/ with the proof of the Google method: %v\n%s", err, out)
	}
	if out, err := made.run(t, "go", "test", "-tags", "skipAuth", "-count=1", "./..."); err != nil {
		t.Errorf("go test -tags skipAuth ./... with the proof of the Google method: %v: the application passes its own tests with the Google method on it, and an invited person signs in through Google and is seated while a person nobody invited is refused\n%s", err, out)
	}
}
