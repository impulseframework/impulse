package audit

import (
	"fmt"
	"io"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/shape"
)

// The heading and lines of the sign-in section.
const (
	signInHeading    = "sign-in"
	noAuthLine       = "  no auth (no session Auth is constructed outside tests)"
	signInError      = "  the sign-in choices could not be read: %v"
	applicationLine  = "  the application"
	noTenantsLine    = "    tenants: none"
	noShape          = "no named shape"
	widthUnread      = "how wide it is open is not read here"
	severalTenants   = "    a person can be in several tenants (the auth's package hands its identity client no identity.WithOneTenantPerPerson)"
	noDirectoryLine  = "    a tenant's own directory: none (no record is annotated @tenantDirectory)"
	nobodyMakesLine  = "    who creates a tenant: nobody directly, and no auth makes one: the session outlets that serve the tenant record are not all bound to one auth whose roles the application gives"
	whoCreatesFormat = "    who creates a tenant: nobody directly; an administrator of the %[1]s auth invites one person, whose first sign-in makes the tenant, and a person who signed up to the %[1]s auth creates their own while one of its methods is open to sign-up, which is chosen while the application runs\n"
)

// WriteSignIn writes the sign-in section: every auth with its sign-in methods, each
// method with its three choices in words (how wide it is open, who gives the roles, and
// Open or Invite only, which is chosen while the application runs and is said to be so,
// never guessed), and the shape the choices add up to by number and name, or "no named
// shape" with why; then the application's own choices: tenants or none, whether a
// tenant can have a directory of its own, and who creates a tenant. In an application
// with tenants each auth also says whether a person belongs to one tenant or can be in
// several. Nothing acts on a shape's name, and a combination outside the eight is no
// error.
func WriteSignIn(out io.Writer, a *app.App) {
	fmt.Fprintln(out, signInHeading)
	reading, err := shape.Read(a)
	if err != nil {
		fmt.Fprintf(out, signInError+"\n", err)

		return
	}
	if len(reading.Auths) == 0 {
		fmt.Fprintln(out, noAuthLine)
	}
	var makers []string
	for i := range reading.Auths {
		auth := &reading.Auths[i]
		if auth.Name == "" {
			fmt.Fprintf(out, "  the Auth constructed at %s:%d, outside an auth package\n", auth.File, auth.Line)
		} else {
			fmt.Fprintf(out, "  %s (%s)\n", auth.Name, auth.Dir)
		}
		for j := range auth.Methods {
			fmt.Fprintf(out, "    %s\n", methodLine(&auth.Methods[j]))
		}
		switch {
		case reading.Tenants == nil:
		case auth.OneTenant != "":
			fmt.Fprintf(out, "    a person belongs to one tenant (identity.WithOneTenantPerPerson, %s)\n", auth.OneTenant)
		default:
			fmt.Fprintln(out, severalTenants)
		}
		fmt.Fprintf(out, "    shape: %s\n", shapeLine(auth))
		if auth.MakesTenants && auth.Name != "" {
			makers = append(makers, auth.Name)
		}
	}
	fmt.Fprintln(out, applicationLine)
	if reading.Tenants == nil {
		fmt.Fprintln(out, noTenantsLine)

		return
	}
	fmt.Fprintf(out, "    tenants: the %s record (%s:%d)\n", reading.Tenants.Name, reading.Tenants.File, reading.Tenants.Line)
	if d := reading.Directories; d != nil {
		fmt.Fprintf(out, "    a tenant's own directory: a tenant can have one, set from a sign-in through it (the %s record, %s:%d)\n", d.Name, d.File, d.Line)
	} else {
		fmt.Fprintln(out, noDirectoryLine)
	}
	if len(makers) == 0 {
		fmt.Fprintln(out, nobodyMakesLine)
	}
	for _, maker := range makers {
		fmt.Fprintf(out, whoCreatesFormat, maker)
	}
}

// methodLine is one sign-in method with its three choices: its registration name, with
// its kind where the name does not say it, where it is registered, whom it admits, who
// gives the roles, and the Open or Invite only choice.
func methodLine(m *shape.Method) string {
	name := m.Name
	if m.Kind != m.Name {
		name += ", " + m.Kind
	}
	width := widthUnread
	if m.Width.Read {
		width = "admits " + m.Width.Words
	}

	return fmt.Sprintf("%s (%s:%d): %s; %s; %s", name, m.File, m.Line, width, m.Roles, m.Choice)
}

// shapeLine is the shape an auth's choices add up to, by number and name, with what the
// reading says beside it, or "no named shape" with why.
func shapeLine(auth *shape.Auth) string {
	switch {
	case auth.Shape == nil:
		return noShape + ": " + auth.Says
	case auth.Says == "":
		return auth.Shape.String()
	default:
		return auth.Shape.String() + ". " + auth.Says
	}
}
