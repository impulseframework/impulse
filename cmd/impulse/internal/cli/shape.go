package cli

import (
	"fmt"
	"io"

	"github.com/go-playground/errors/v5"
	"github.com/spf13/cobra"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
	"github.com/impulseframework/impulse/cmd/impulse/internal/shape"
	transition_ "github.com/impulseframework/impulse/cmd/impulse/internal/transition"
)

// startingPoint is what a command given --shape says beside its change: the shape it
// started from, what the shape filled in, and what is still to choose, by the author or
// while the application runs. Nothing acts on the shape afterwards: the application
// keeps the separate choices, and impulse audit reads the shape back from them.
type startingPoint struct {
	shape shape.Shape
	says  []string
}

// write prints the starting point after the change it rode on.
func (s *startingPoint) write(w io.Writer) {
	fmt.Fprintf(w, "Starting point: %s.\n", s.shape)
	for _, line := range s.says {
		fmt.Fprintf(w, "  - %s\n", line)
	}
	fmt.Fprintln(w)
}

// firstAdministratorCheck is the check that refuses a first administrator the migrate
// command would seed and nobody could sign in as: where the application's tenants have
// directories, an auth with no password method whose directory method is open to more
// than one organization.
const firstAdministratorCheck = "first-administrator"

// writeHeld says, after the check's report, that the starting point as written into
// this application fails the first-administrator check, with what the author does next
// in the check's own words. A starting point writes the choices its shape names
// whatever the application around them is, and this is the one combination of them the
// check refuses, so the command says it itself and does not leave it to the report.
func (s *startingPoint) writeHeld(w io.Writer, results []check.Result) {
	for i := range results {
		r := &results[i]
		if r.Name != firstAdministratorCheck || r.Status != check.Fail {
			continue
		}
		fmt.Fprintf(w, "\nWith the %s starting point written, the migrate command would seed a first administrator who cannot sign in, which impulse check refuses (%s). What to do next, in the check's words:\n", s.shape.Name, r.Name)
		for _, detail := range r.Details {
			fmt.Fprintf(w, "  - %s\n", detail)
		}
	}
}

// heldFirstAdministrator refuses, before anything is written, the one starting point of
// add auth whose first administrator nobody could sign in as: Open sign-up on an auth
// with no password method, in an application whose tenants have directories
// (check.DeclaresDirectories, the first-administrator check's own condition). The shape
// opens every directory method to more than one organization, so the migrate command
// would seed the administrator for no registration, the sign-in rule would link the
// seeded account through none, and the check would fail what the command had just
// written. writeHeld stays for a combination this does not foresee.
func heldFirstAdministrator(s *shape.Shape, a *app.App, specs []authpkg.Method) error {
	password := false
	for i := range specs {
		password = password || specs[i].Kind == app.MethodPassword
	}
	if !s.OpenSignUp() || password || !check.DeclaresDirectories(a) {
		return nil
	}

	return errors.Newf("%s: this application's tenants have directories, so the first administrator the migrate command would seed for an auth with no password method, whose directory methods the shape opens to more than one organization, could not sign in, and impulse check refuses it (%s). Give the auth the password method as well (--method password beside the others), so the administrator is seeded with a password; or add the auth without the shape, for one organization, and open another method with impulse add method afterward", s.Flag(), firstAdministratorCheck)
}

// bindShape gives a command its --shape flag, naming the shapes it offers and what a
// starting point fills in there.
func bindShape(cmd *cobra.Command, key *string, offered []shape.Shape, fills string) {
	cmd.Flags().StringVar(key, shape.FlagName, "", shape.Usage(offered, fills))
}

// methodShape is the shape add auth or add method was given, nil without --shape: one of
// shapes 1 to 4, which a sign-in method's choices tell apart.
func methodShape(cmd *cobra.Command, key string) (*shape.Shape, error) {
	if key == "" {
		return nil, nil
	}
	s, err := shape.Find(key, shape.OfMethods(), cmd.CommandPath())
	if err != nil {
		return nil, err
	}

	return &s, nil
}

// fillMethods fills in each method's choices from the shape add auth was given, and
// refuses a method or a flag that contradicts it: Directory roles is one method alone,
// so more than one is refused before any is judged. Directory members is a directory
// method's shape, and the choice is made for each method, so a password method beside
// one is left as it is, invite only; with no directory method there is nothing for the
// shape to be.
func fillMethods(s *shape.Shape, specs []authpkg.Method) error {
	named := make([]string, 0, len(specs))
	for i := range specs {
		named = append(named, "--method "+methodName(&specs[i]))
	}
	if err := s.StandsAlone(named); err != nil {
		return err
	}
	filled := 0
	for i := range specs {
		spec := &specs[i]
		if s.DirectoryMembers() && spec.Kind == app.MethodPassword {
			continue
		}
		if err := s.Method(spec); err != nil {
			return err
		}
		filled++
	}
	if filled == 0 {
		return s.NeedsDirectoryMethod("add auth was given the password method alone")
	}

	return nil
}

// methodName is a method as add auth's --method names it: the password method by its
// kind, any other by its kind and registration.
func methodName(m *authpkg.Method) string {
	if m.Kind == app.MethodPassword {
		return app.MethodPassword
	}

	return m.Kind + ":" + m.Name
}

// authStartingPoint is what add auth says of the shape it started from: one line for
// each method the shape filled in, read against the outlet the auth is bound to, since
// what opening a method does depends on whether that outlet serves a tenant record.
func authStartingPoint(s *shape.Shape, appDir, outlet string, specs []authpkg.Method) (*startingPoint, error) {
	a, err := app.Discover(appDir)
	if err != nil {
		return nil, err
	}
	if err := heldFirstAdministrator(s, a, specs); err != nil {
		return nil, err
	}
	opening := shape.OpensWithRole
	choices := transition_.OutletChoices(a)
	for i := range choices {
		if choices[i].Key == outlet {
			opening = shape.OpeningAt(a, &choices[i].Site, &choices[i].Outlet)
		}
	}
	start := &startingPoint{shape: *s}
	for i := range specs {
		if s.DirectoryMembers() && specs[i].Kind == app.MethodPassword {
			start.says = append(start.says, "The password method beside it is left as it is: invite only.")

			continue
		}
		start.says = append(start.says, s.Says(&specs[i], opening))
	}

	return start, nil
}

// methodStartingPoint is what add method says of the shape it started from, read
// against the auth the method goes on.
func methodStartingPoint(s *shape.Shape, appDir, auth string, spec *authpkg.Method) (*startingPoint, error) {
	a, err := app.Discover(appDir)
	if err != nil {
		return nil, err
	}

	return &startingPoint{shape: *s, says: []string{s.Says(spec, shape.OpeningOf(a, auth))}}, nil
}

// tenantShape is the shape impulse new or add tenancy was given, nil without --shape:
// one of shapes 5 to 8, which the tenants' choices tell apart. It settles whether a
// person belongs to one tenant, from the shape or, without one, from the flag, and
// refuses the flag where it says the opposite of the shape.
func tenantShape(cmd *cobra.Command, key string, oneTenant bool) (*shape.Shape, bool, error) {
	if key == "" {
		return nil, oneTenant, nil
	}
	s, err := shape.Find(key, shape.OfTenants(), cmd.CommandPath())
	if err != nil {
		return nil, false, err
	}
	oneTenant, err = s.OneTenantChoice(cmd.Flags().Changed(shape.OneTenantFlag), oneTenant)
	if err != nil {
		return nil, false, err
	}

	return &s, oneTenant, nil
}

// oneTenantUsage describes the flag that says a person belongs to one tenant.
const oneTenantUsage = "a person belongs to one tenant: every auth whose roles the application gives builds its identity client with identity.WithOneTenantPerPerson(), so a role in a second tenant is refused; without it a person can be in several"

// tenancyStartingPoint is what add tenancy says of the shape it started from, read
// against the application as it stood: a shape without tenant directories is refused
// where a sign-in method already goes through an organization's directory.
func tenancyStartingPoint(s *shape.Shape, appDir string) (*startingPoint, error) {
	a, err := app.Discover(appDir)
	if err != nil {
		return nil, err
	}
	methods := shape.DirectoryMethods(a)
	if err := s.RefuseDirectoryMethods(methods); err != nil {
		return nil, err
	}
	var auths []string
	for i := range a.AuthPackages {
		auths = append(auths, a.AuthPackages[i].Name)
	}

	return &startingPoint{shape: *s, says: s.TenantsSay(auths, methods)}, nil
}

// errTenancyOff is the refusal of --shape beside --tenancy=false on impulse new.
func errTenancyOff(s *shape.Shape) error {
	return errors.Newf("%s and --tenancy=false contradict each other: %s is an application with tenants, and --tenancy=false leaves them out. Leave --tenancy out, or leave --%s out", s.Flag(), s.Name, shape.FlagName)
}
