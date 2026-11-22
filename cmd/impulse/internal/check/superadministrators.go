package check

import (
	"context"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/impulseframework/impulse/app"
)

// superAdministratorsTold verifies that every auth package tells its Auth who the auth's
// own super administrators are. An auth with a role file has the Super Administrator
// role, which the framework defines in every auth, and the session library holds its
// holders to a second factor whatever the sign-in rule asks of anyone else, except at a
// sign-in through a directory that writes the roles, where the directory decides who
// holds the role; it learns
// who they are from the argument session.NewAuth takes after the cookie key. The
// compiler holds an application to passing the argument, and nothing but this holds it
// to passing the right one: session.NoSuperAdministrators() compiles in an auth whose
// people hold roles and leaves every holder to the application's rule alone, and a check
// built over another auth's permission engine holds that auth's holders to the factor
// and none of this one's.
//
// The check reads what the constructor shows: the form of the argument, the engine the
// same function opens behind the check (access.New) and the role file that engine is
// handed. An engine opened anywhere else, the data level say, cannot be followed from
// here, so whose it is goes unjudged and is a warning; so is a check written by hand
// as a function, and an engine handed a role file that is no auth package's.
type superAdministratorsTold struct{}

// superAdministratorsToldName is the check's name.
const superAdministratorsToldName = "super-administrators"

func (superAdministratorsTold) Name() string {
	return superAdministratorsToldName
}

func (superAdministratorsTold) Describe() string {
	return "every auth package with a role file tells its Auth who the auth's own super administrators are: session.NewAuth is handed session.SuperAdministratorsBy over a check built on the permission engine the package opens with its own Roles(), never session.NoSuperAdministrators() and never a check built over another auth's engine or taken from another auth package; a check written by hand, or one whose engine is opened outside the constructor, cannot be followed and is reported (WARN)"
}

// superAdministratorsForm is the argument an auth package passes, as impulse add auth
// writes it.
const superAdministratorsForm = "session.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(accessClient.UserManager())))"

func (c superAdministratorsTold) Run(_ context.Context, env *Env) Result {
	a := env.App
	if len(a.AuthPackages) == 0 {
		return skip(c.Name(), "no auth package (pkg/auth/<name>) constructs a session Auth")
	}

	var details, warnings, told []string
	for i := range a.AuthPackages {
		p := &a.AuthPackages[i]
		if _, err := os.Stat(a.Abs(rolesFileOf(p))); err != nil {
			continue // roles-file and auths-wired report an auth package without its role file
		}
		for j := range a.Auths {
			auth := &a.Auths[j]
			if path.Dir(auth.File) != p.Dir {
				continue
			}
			finding, warning := c.judge(a, p, auth)
			switch {
			case finding != "":
				details = append(details, finding)
			case warning != "":
				warnings = append(warnings, warning)
			default:
				told = append(told, p.Name)
			}
		}
	}
	switch {
	case len(details) > 0:
		return fail(c.Name(), fmt.Sprintf("%d Auth(s) not told who their own auth's super administrators are", len(details)), append(details, warnings...)...)
	case len(warnings) > 0:
		return warn(c.Name(), fmt.Sprintf("%d Auth(s) whose super administrators cannot be followed to an engine", len(warnings)), warnings...)
	case len(told) == 0:
		return skip(c.Name(), "no auth package holds a role file")
	}

	return pass(c.Name(), fmt.Sprintf("%d Auth(s) told their super administrators by the engine their package opens with its own role file: %s", len(told), strings.Join(told, ", ")))
}

// judge reads one construction of an auth package that has a role file: a finding when
// the Auth is told of no super administrators or of another auth's, a warning when the
// argument or the engine behind it does not read, and neither when the check is built
// over the engine the constructor opens with the package's own Roles().
func (superAdministratorsTold) judge(a *app.App, p *app.AuthPackage, auth *app.Auth) (finding, warning string) {
	at := fmt.Sprintf("%s:%d", auth.File, auth.Line)
	told := &auth.SuperAdministrators
	own := fmt.Sprintf("pass %s over the engine %s.New opens with access.WithDefaultRoles(Roles(), Name)", superAdministratorsForm, p.Name)
	switch told.Form {
	case app.SuperAdministratorsNone:
		return fmt.Sprintf("%s: the %s auth has a role file (%s), so its people hold roles and the Super Administrator role is among them, and its Auth is built with session.NoSuperAdministrators(), which leaves a holder to the sign-in rule alone, with no second factor of the library's own; %s",
			at, p.Name, rolesFileOf(p), own), ""
	case app.SuperAdministratorsChecked:
	default:
		return "", fmt.Sprintf("%s: what the %s auth's Auth is told about its super administrators does not read here: the argument after the cookie key is neither session.SuperAdministratorsBy(...) nor session.NoSuperAdministrators(), written in the call or assigned to the name the call takes (name := ... or name = ...) earlier in the same function, so it is not judged; %s",
			at, p.Name, own)
	}

	for i := range a.AuthPackages {
		other := &a.AuthPackages[i]
		if other.Path != "" && other.Path != p.Path && slices.Contains(told.Packages, other.Path) {
			return fmt.Sprintf("%s: the %s auth's Auth takes its super administrators from the %s auth's package (%s), so it holds that auth's holders to the second factor and none of its own; %s",
				at, p.Name, other.Name, other.Path, own), ""
		}
	}
	switch {
	case told.EngineLine == 0:
		return "", fmt.Sprintf("%s: the check the %s auth's Auth is handed does not lead to a permission engine opened in the same function (access.New), so whose roles it reads is not judged here; %s",
			at, p.Name, own)
	case told.RolesPackage == "":
		return "", fmt.Sprintf("%s: the check the %s auth's Auth is handed is built over the engine opened at %s:%d, and the role file that engine is handed does not read there (no access.WithDefaultRoles(Roles(), Name) among its arguments), so whose engine it is is not judged here; %s",
			at, p.Name, auth.File, told.EngineLine, own)
	case told.RolesPackage == p.Path:
		return "", ""
	}
	if other := authPackageAt(a, told.RolesPackage); other != nil {
		return fmt.Sprintf("%s: the %s auth's Auth takes its super administrators from the engine opened at %s:%d, which is handed the %s auth's role file and is that auth's engine, so it holds that auth's holders to the second factor and none of its own; %s",
			at, p.Name, auth.File, told.EngineLine, other.Name, own), ""
	}

	return "", fmt.Sprintf("%s: the check the %s auth's Auth is handed is built over the engine opened at %s:%d, which is handed the Roles() of %s, no auth package of the application, and not the package's own, so whose engine it is is not judged here; %s",
		at, p.Name, auth.File, told.EngineLine, told.RolesPackage, own)
}

// authPackageAt returns the application's auth package at the import path, or nil.
func authPackageAt(a *app.App, pkg string) *app.AuthPackage {
	for i := range a.AuthPackages {
		if a.AuthPackages[i].Path == pkg {
			return &a.AuthPackages[i]
		}
	}

	return nil
}
