package cli

import (
	"fmt"
	"io"

	"github.com/go-playground/errors/v5"
	"github.com/spf13/cobra"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
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

// tenantChoices are the tenants' choices a command settles before it lays tenants in:
// whether a person belongs to one tenant, and whether a tenant can have a directory of
// its own. The zero value is the tenancy step's own default: a person can be in several
// tenants, and a tenant can have a directory.
type tenantChoices struct {
	oneTenant     bool
	noDirectories bool
}

// tenantShape is the shape impulse new or add tenancy was given, nil without --shape:
// one of shapes 5 to 8, which the tenants' choices tell apart. It settles the choices,
// from the shape or, without one, from the flags as given (oneTenant is
// --one-tenant-per-person, and --tenant-directories is read from the command), and
// refuses a flag that says the opposite of the shape.
func tenantShape(cmd *cobra.Command, key string, oneTenant bool) (*shape.Shape, tenantChoices, error) {
	directories, err := cmd.Flags().GetBool(shape.DirectoriesFlag)
	if err != nil {
		return nil, tenantChoices{}, errors.Wrap(err, "pflag.FlagSet.GetBool()")
	}
	if key == "" {
		return nil, tenantChoices{oneTenant: oneTenant, noDirectories: !directories}, nil
	}
	s, err := shape.Find(key, shape.OfTenants(), cmd.CommandPath())
	if err != nil {
		return nil, tenantChoices{}, err
	}
	oneTenant, err = s.OneTenantChoice(cmd.Flags().Changed(shape.OneTenantFlag), oneTenant)
	if err != nil {
		return nil, tenantChoices{}, err
	}
	directories, err = s.DirectoriesChoice(cmd.Flags().Changed(shape.DirectoriesFlag), directories)
	if err != nil {
		return nil, tenantChoices{}, err
	}

	return &s, tenantChoices{oneTenant: oneTenant, noDirectories: !directories}, nil
}

// oneTenantUsage describes the flag that says a person belongs to one tenant.
const oneTenantUsage = "a person belongs to one tenant: every auth whose roles the application gives builds its identity client with identity.WithOneTenantPerPerson(), so a role in a second tenant is refused; without it a person can be in several"

// directoriesUsage describes the flag that says whether a tenant can have a directory
// of its own.
const directoriesUsage = "a tenant can have a directory of its own: the directories record and its table, the tenant record's directory-only switch and each auth's directory admission are written, so a tenant's administrator can make their organization's directory the tenant's, open the tenant to it and make it directory only; with --" + shape.DirectoriesFlag + "=false none of it is written, and a tenant's people come by invitation alone, whatever sign-in methods the application has or gets"

// tenancyStartingPoint is what add tenancy says of the shape it started from, read
// against the application as it stood: the sign-in methods it already registers through
// an organization's directory are named, as what a tenant's people come through its
// directory by or, where the tenants have no directories, as a way for an invited
// person to sign in.
func tenancyStartingPoint(s *shape.Shape, appDir string) (*startingPoint, error) {
	a, err := app.Discover(appDir)
	if err != nil {
		return nil, err
	}
	var auths []string
	for i := range a.AuthPackages {
		auths = append(auths, a.AuthPackages[i].Name)
	}

	return &startingPoint{shape: *s, says: s.TenantsSay(auths, shape.DirectoryMethods(a))}, nil
}

// errTenancyOff is the refusal of --shape beside --tenancy=false on impulse new.
func errTenancyOff(s *shape.Shape) error {
	return errors.Newf("%s and --tenancy=false contradict each other: %s is an application with tenants, and --tenancy=false leaves them out. Leave --tenancy out, or leave --%s out", s.Flag(), s.Name, shape.FlagName)
}
