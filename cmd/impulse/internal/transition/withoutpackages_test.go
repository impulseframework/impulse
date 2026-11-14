package transition

import (
	"go/format"
	"slices"
	"testing"
)

// The package a site's copy is made of, as the skeleton shapes it: the App takes the
// staff auth's identity client through its Configurer, and the authorization harness
// builds one over a session API that answers nothing.
const (
	dropAppFile = `package app

import (
	"example.com/acme/beacon/apps/console/pkg/identity/staffidentity"
	"example.com/acme/beacon/pkg/auth/staff"
	"github.com/impulseframework/impulse/resource"
)

// Configurer is what the App is built from.
type Configurer interface {
	ResourceClient() resource.Client
	// Staff returns the auth this surface binds to.
	Staff() *staff.Auth
	// StaffIdentity is the staff auth's identity client (generated): what the generated
	// methods that manage the staff's people run over.
	StaffIdentity() *staffidentity.Client
	// CursorKey seals the cursors.
	CursorKey() *resource.CursorKey
}

// App implements the application's http handlers.
type App struct {
	*staff.Handlers
	// staffIdentity is the staff auth's identity client.
	staffIdentity  *staffidentity.Client
	resourceClient resource.Client
}

// New constructs an App from its dependencies.
func New(cfg Configurer) *App {
	return &App{
		Handlers:       cfg.Staff().Handlers(),
		// The client the generated handlers read.
		staffIdentity:  cfg.StaffIdentity(),
		resourceClient: cfg.ResourceClient(),
	}
}
`
	dropUsersFile = `package app

import (
	"example.com/acme/beacon/apps/console/pkg/identity/staffidentity"
)

// The management of the staff auth's people is generated.

// StaffIdentity is the staff auth's identity client.
func (a *App) StaffIdentity() *staffidentity.Client {
	return a.staffIdentity
}
`
	dropHarnessFile = `package authz

import (
	"context"

	"example.com/acme/beacon/apps/console/pkg/identity/staffidentity"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/session"
)

type testConfigurer struct {
	access access.Controller
}

func (c *testConfigurer) Access() access.Controller {
	return c.access
}

// noSessionAPI stands where the matrix serves no session manager.
type noSessionAPI struct{}

func (noSessionAPI) HashPassword(string) (string, error) {
	return "", errors.New("the authorization matrix serves no session manager")
}

func (noSessionAPI) PrepareInvitation(context.Context, *session.InvitationRequest) (session.PreparedInvitation, error) {
	return nil, errors.New("the authorization matrix serves no session manager")
}

// StaffIdentity is the staff auth's identity client over the scripted engine.
func (c *testConfigurer) StaffIdentity() *staffidentity.Client {
	client, err := staffidentity.NewClient(c.Access(), noSessionAPI{})
	if err != nil {
		panic(err)
	}

	return client
}

// kept is used by what stays.
type kept struct{}

func use() any {
	return kept{}
}
`
)

func TestDropPackages(t *testing.T) {
	t.Parallel()

	const tree = "example.com/acme/beacon/apps/console/pkg/identity"
	got, err := dropPackages(map[string]string{"app.go": dropAppFile, "users.go": dropUsersFile, "doc.go": "// Package app is the application.\npackage app\n"}, tree)
	if err != nil {
		t.Fatalf("dropPackages() error = %v", err)
	}
	if want := []string{"StaffIdentity"}; !slices.Equal(got.Dropped, want) {
		t.Errorf("Dropped = %q, want %q", got.Dropped, want)
	}
	if len(got.Left) != 0 {
		t.Errorf("Left = %q, want nothing", got.Left)
	}
	if _, ok := got.Files["users.go"]; ok {
		t.Errorf("users.go is kept though it declares nothing any longer:\n%s", got.Files["users.go"])
	}
	if got.Files["doc.go"] != "// Package app is the application.\npackage app\n" {
		t.Errorf("doc.go changed though it imports nothing of the tree:\n%s", got.Files["doc.go"])
	}
	wantApp := `package app

import (
	"example.com/acme/beacon/pkg/auth/staff"
	"github.com/impulseframework/impulse/resource"
)

// Configurer is what the App is built from.
type Configurer interface {
	ResourceClient() resource.Client
	// Staff returns the auth this surface binds to.
	Staff() *staff.Auth
	// CursorKey seals the cursors.
	CursorKey() *resource.CursorKey
}

// App implements the application's http handlers.
type App struct {
	*staff.Handlers
	resourceClient resource.Client
}

// New constructs an App from its dependencies.
func New(cfg Configurer) *App {
	return &App{
		Handlers:       cfg.Staff().Handlers(),
		resourceClient: cfg.ResourceClient(),
	}
}
`
	if got.Files["app.go"] != wantApp {
		t.Errorf("app.go =\n%s\nwant\n%s", got.Files["app.go"], wantApp)
	}

	// The harness: the accessor goes, and with it the type it alone used, that type's
	// methods, and the imports they alone used.
	got, err = dropPackages(map[string]string{"harness_test.go": dropHarnessFile}, tree)
	if err != nil {
		t.Fatalf("dropPackages() error = %v", err)
	}
	if want := []string{"StaffIdentity", "noSessionAPI"}; !slices.Equal(got.Dropped, want) {
		t.Errorf("Dropped = %q, want %q", got.Dropped, want)
	}
	formatted, err := format.Source([]byte(got.Files["harness_test.go"]))
	if err != nil {
		t.Fatalf("the harness does not parse without the client:\n%s", got.Files["harness_test.go"])
	}
	wantHarness := `package authz

import (
	"github.com/impulseframework/impulse/access"
)

type testConfigurer struct {
	access access.Controller
}

func (c *testConfigurer) Access() access.Controller {
	return c.access
}

// kept is used by what stays.
type kept struct{}

func use() any {
	return kept{}
}
`
	if string(formatted) != wantHarness {
		t.Errorf("harness_test.go =\n%s\nwant\n%s", formatted, wantHarness)
	}
}

// TestDropPackagesReportsWhatIsLeft pins the limit: a call of a removed method in a
// declaration that names nothing of the tree is kept and reported, never rewritten.
func TestDropPackagesReportsWhatIsLeft(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"app.go": dropAppFile,
		"welcome.go": `package app

import "context"

// Welcome gives a new person their first role.
func (a *App) Welcome(ctx context.Context, cfg Configurer) error {
	return cfg.StaffIdentity().Welcome(ctx)
}
`,
	}
	got, err := dropPackages(files, "example.com/acme/beacon/apps/console/pkg/identity")
	if err != nil {
		t.Fatalf("dropPackages() error = %v", err)
	}
	if want := []string{"welcome.go:7: StaffIdentity"}; !slices.Equal(got.Left, want) {
		t.Errorf("Left = %q, want %q", got.Left, want)
	}
	if got.Files["welcome.go"] != files["welcome.go"] {
		t.Errorf("welcome.go was rewritten:\n%s", got.Files["welcome.go"])
	}
}

// TestDropPackagesLeavesOtherPackages pins that a package which imports nothing from
// under the prefix is returned as it is, a sibling path with the same beginning included.
func TestDropPackagesLeavesOtherPackages(t *testing.T) {
	t.Parallel()

	files := map[string]string{"a.go": "package a\n\nimport \"example.com/acme/beacon/apps/console/pkg/identityx/staffidentity\"\n\nvar _ = staffidentity.TablePrefix\n"}
	got, err := dropPackages(files, "example.com/acme/beacon/apps/console/pkg/identity")
	if err != nil {
		t.Fatalf("dropPackages() error = %v", err)
	}
	if got.Files["a.go"] != files["a.go"] || len(got.Dropped) != 0 {
		t.Errorf("dropPackages() = %q dropped %q, want the file as it was", got.Files["a.go"], got.Dropped)
	}
}
