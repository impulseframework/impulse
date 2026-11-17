package authpkg

import (
	"path"
	"strings"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
)

// view is what the Auth file's template renders from.
type view struct {
	*Spec
	// Pascal is the auth's table prefix (Staff).
	Pascal string
	// Package is the import path of the application's auth package (<module>/pkg/auth),
	// which holds what the auths share.
	Package string
	// Doc is the package comment's first sentence after "Package <name> is the <name>
	// auth:".
	Doc string
	// Directory reports an auth with a directory method: it takes a LoginURL and the
	// development environment simulates the directory.
	Directory bool
	// Application is the application's name as the auth's mail says it.
	Application string
	// Password reports an auth with the password method, whose strangers sign up from a
	// mailed link while an administrator has the method open to sign-up, and
	// UsernameIsEmail one whose sign-in name is the address, so a verified change of
	// address renames the user; the roles are held by the account's id and stay.
	Password        bool
	UsernameIsEmail bool
	// RolesSynced reports an auth with a registration whose directory writes the roles
	// at every sign-in: the directory is the authority for everyone the auth holds, so
	// the application gives nobody a role at a sign-in and the auth sets no hook for it
	// (the generated identity client of such an auth answers none).
	RolesSynced bool
	// SignUpChoice reports an auth whose methods an administrator may open to sign-up
	// (signUpChoice).
	SignUpChoice bool
	// IdentityPackage is the import path of the auth's generated identity package
	// (<module>/<IdentityDir>/<name>identity); empty where the application generates no
	// management of its auths.
	IdentityPackage string
	// Methods are the methods with their rendering helpers.
	Methods []*methodView
	// Outlets are the machine outlets whose tokens the auth issues, with their rendering
	// helpers.
	Outlets []*outletView
	// Foothold is the form of the auth's first administrator the deployment seeds
	// (Foothold), AdministratorAuthored whether the spec names the roles,
	// AdministratorGlobal and AdministratorEveryDomain the roles that count as an
	// administrator's as Go literals, and FirstAdministratorGlobal and
	// FirstAdministratorEveryDomain the roles the first administrator is seeded with.
	Foothold string
	// FootholdRegistration is the constant naming the registration a membership
	// foothold's first administrator signs in through (FootholdMethod), and
	// FootholdMethodName that registration's name; empty where there is none.
	FootholdRegistration          string
	FootholdMethodName            string
	AdministratorAuthored         bool
	AdministratorGlobal           string
	AdministratorEveryDomain      string
	FirstAdministratorGlobal      string
	FirstAdministratorEveryDomain string
	// Imports are the import paths the Auth file needs beyond the fixed ones.
	Imports []string
}

// outletView is a machine outlet as the Auth file renders it.
type outletView struct {
	// Name is the outlet's name as the generator program declares it (machines).
	Name string
	// Pascal is the name in PascalCase (Machines): the constant and the getter.
	Pascal string
}

// methodView is what a method file's template renders from.
type methodView struct {
	*Method
	// Auth is the auth's name and AuthPascal its table prefix.
	Auth       string
	AuthPascal string
	// Foothold is the form of the auth's first administrator (Foothold), which the
	// persona cast's seed keeps its accounts apart from.
	Foothold string
	// Field is the Handlers field holding the method (directory, entraOne).
	Field string
	// Type is the method's Go type (*azure.Method).
	Type string
	// SlotKind is the slot's normalized name and SlotWords the slot in words, rendering
	// the function every directory method builds its slot in (<field>Slot), the one
	// place bedrock reads a Microsoft registration's tenant shape from.
	SlotKind  string
	SlotWords string
	// RoleSyncExpr is the role synchronization as the Config takes it.
	RoleSyncExpr string
	// Fields are the settings the method reads.
	Fields []Field
	// Variables are the fields' environment variables, in order.
	Variables []string
	// Imports are the method file's imports beyond the standard library's, which
	// StdImports holds, rendered as their own group.
	Imports []string
	// StdImports are the standard library packages the method file imports.
	StdImports []string
	// Simulated are the names the simulated directory signs the registration in with:
	// APP_<REGISTRATION>_USERNAME and _ROLES.
	SimulatedUsername, SimulatedRoles string
	// SignUpChoice reports an auth whose methods an administrator may open to sign-up
	// (signUpChoice).
	SignUpChoice bool
	// module is the application's module path.
	module string
}

// signUpChoice reports whether an administrator may open the auth's methods to sign-up
// in the running application: every auth of an application without tenants, and in one
// with tenants the auth whose people create a tenant of their own (the tenant policy
// admits a person who holds none). Another auth of a tenanted application has nothing to
// give a person who signed up, so its methods stay invite only.
func signUpChoice(spec *Spec) bool {
	return spec.TenantPolicy == nil || spec.TenantPolicy.AdmitWithoutTenant
}

// Module is the application's module path, for the imports of the application's own
// packages.
func (mv *methodView) Module() string {
	return mv.module
}

func newView(spec *Spec) *view {
	v := &view{Spec: spec, Pascal: names.Pascal(spec.Name), Package: path.Join(spec.Module, spec.AuthDir)}
	v.Doc = spec.Doc
	if v.Doc == "" {
		v.Doc = "the people who sign in to the application"
	}
	v.Application = Application(spec)
	v.SignUpChoice = signUpChoice(spec)
	for i := range spec.Methods {
		mv := newMethodView(spec, &spec.Methods[i])
		v.Methods = append(v.Methods, mv)
		v.RolesSynced = v.RolesSynced || spec.Methods[i].SyncsRoles()
		if mv.Directory() {
			v.Directory = true
		} else {
			v.Password = true
			v.UsernameIsEmail = v.UsernameIsEmail || mv.UsernameIsEmail
		}
	}
	for _, outlet := range spec.MachineOutlets {
		v.Outlets = append(v.Outlets, &outletView{Name: outlet, Pascal: names.Pascal(outlet)})
	}
	v.Foothold = Foothold(spec)
	if m := FootholdMethod(spec); m != nil {
		v.FootholdRegistration, v.FootholdMethodName = m.Registration(), m.Name
	}
	v.AdministratorAuthored = len(spec.AdministratorRoles.Global)+len(spec.AdministratorRoles.EveryDomain) > 0
	v.AdministratorGlobal = roleLiteral(spec.AdministratorRoles.Global)
	v.AdministratorEveryDomain = roleLiteral(spec.AdministratorRoles.EveryDomain)
	first := spec.FirstAdministratorRoles
	if len(first.Global)+len(first.EveryDomain) == 0 {
		first = spec.AdministratorRoles
	}
	v.FirstAdministratorGlobal = roleLiteral(first.Global)
	v.FirstAdministratorEveryDomain = roleLiteral(first.EveryDomain)
	seen := map[string]bool{}
	for _, mv := range v.Methods {
		var imp string
		switch mv.Kind {
		case app.MethodAzure:
			imp = "github.com/impulseframework/impulse/session/azure"
		case app.MethodGoogle:
			imp = "github.com/impulseframework/impulse/session/google"
		case app.MethodOIDC, app.MethodPersona:
			imp = oidcImport
		}
		if imp != "" && !seen[imp] {
			seen[imp] = true
			v.Imports = append(v.Imports, imp)
		}
	}
	// The sign-in rule every auth sets (requireSecondFactor) names the library's decision
	// and the factor's method.
	v.Imports = append(v.Imports, "github.com/impulseframework/impulse/session/sessioninfo", signinImport)
	// The auth's identity client, and its option naming the administrator roles.
	if spec.IdentityDir != "" {
		v.IdentityPackage = path.Join(spec.Module, spec.IdentityDir, spec.Name+"identity")
		v.Imports = append(v.Imports, v.IdentityPackage)
	}
	if v.IdentityPackage != "" && v.Foothold != "" {
		v.Imports = append(v.Imports, "github.com/impulseframework/impulse/identity")
	}

	return v
}

// Application is the application's name as the auth's mail says it: the spec's, or the
// module path's last element in PascalCase (example.com/acme/harbor is Harbor).
func Application(spec *Spec) string {
	if spec.Application != "" {
		return spec.Application
	}

	return names.Pascal(path.Base(spec.Module))
}

// The forms of the first administrator the deployment seeds while the auth's users table
// holds no rows: a user named administrator with the password the stack generates, for an
// auth with a password method; a user without a password under the sign-in name the
// placement names, which that person's first sign-in links to, for an auth whose directory
// methods leave role membership to the application (seeded for the registration that
// person comes through, FootholdMethod). Either holds the administrator roles
// from the seed, by the id of its account. An auth whose directory methods all follow the
// directory's roles needs none.
const (
	FootholdPassword   = "password"
	FootholdMembership = "membership"
	footholdFile       = "foothold.go"
)

// Foothold is the form of the first administrator the deployment seeds for the auth, or
// empty when the directory ranks its people.
func Foothold(spec *Spec) string {
	membership := false
	for i := range spec.Methods {
		m := &spec.Methods[i]
		switch {
		case m.Kind == app.MethodPassword:
			return FootholdPassword
		case m.Directory() && m.Authority == app.AuthorityApplication:
			membership = true
		}
	}
	if membership {
		return FootholdMembership
	}

	return ""
}

// FootholdMethod is the method the first administrator of a membership foothold signs
// in through: the first directory method whose roles the application gives and whose
// registration admits one organization and nobody else, which is a Microsoft or a
// Google method in the slot of one organization, or an organization's identity
// provider, which is one issuer. The seed creates the administrator for that
// registration (session.SeedUserIDThrough), so the account's id says which of the
// auth's registrations its name is believed from, and the person's first sign-in
// through it links the account where the application's tenants have directories and a
// name alone links nothing. The persona method is no foothold's: its provider is the
// test environments', registered where it is configured, and declares its names the
// application's own.
//
// Nil for a password foothold, for an auth that seeds nobody, and for a membership
// foothold none of whose directory methods admits one organization alone: nothing then
// says which organization the administrator's name is believed from.
func FootholdMethod(spec *Spec) *Method {
	if Foothold(spec) != FootholdMembership {
		return nil
	}
	for i := range spec.Methods {
		m := &spec.Methods[i]
		if !m.Directory() || m.Persona() || m.Authority != app.AuthorityApplication {
			continue
		}
		if !m.slotted() || m.slot() == SlotOne {
			return m
		}
	}

	return nil
}

// roleLiteral is the roles as a Go slice literal, nil for none.
func roleLiteral(roles []string) string {
	if len(roles) == 0 {
		return "nil"
	}
	quoted := make([]string, 0, len(roles))
	for _, r := range roles {
		quoted = append(quoted, `"`+r+`"`)
	}

	return "[]accesstypes.Role{" + strings.Join(quoted, ", ") + "}"
}

func newMethodView(spec *Spec, m *Method) *methodView {
	mv := &methodView{Method: m, Auth: spec.Name, AuthPascal: names.Pascal(spec.Name), Fields: m.Fields(), SlotKind: m.slot(), SlotWords: slotWords(m), module: spec.Module}
	mv.Field = lowerFirst(m.Pascal())
	mv.Foothold = Foothold(spec)
	mv.SignUpChoice = signUpChoice(spec)
	for i := range mv.Fields {
		mv.Variables = append(mv.Variables, m.Variable(spec.Name, &mv.Fields[i]))
	}
	simulated := strings.ToUpper(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, m.Name))
	mv.SimulatedUsername, mv.SimulatedRoles = "APP_"+simulated+"_USERNAME", "APP_"+simulated+"_ROLES"
	settings := "settings." + m.Pascal()
	switch m.Kind {
	case app.MethodPassword:
		mv.Type = "*session.PasswordMethod"
		mv.Imports = []string{netHTTP, "github.com/impulseframework/impulse/session"}
	case app.MethodAzure:
		azureView(mv, m)
	case app.MethodGoogle:
		googleView(mv, m, settings)
	case app.MethodOIDC:
		oidcView(mv, m)
	case app.MethodPersona:
		personaView(mv)
	}
	mv.StdImports, mv.Imports = splitImports(mv.Imports)

	return mv
}

// splitImports separates the standard library's packages from the rest, so the file
// renders them as gofumpt groups them: the standard library first, then everything else.
func splitImports(imports []string) (std, other []string) {
	for _, imp := range imports {
		if first, _, _ := strings.Cut(imp, "/"); strings.Contains(first, ".") {
			other = append(other, imp)
		} else {
			std = append(std, imp)
		}
	}

	return std, other
}

// netHTTP is the import every method's handlers need.
const (
	netHTTP      = "net/http"
	accessImport = "github.com/impulseframework/impulse/access"
	signinImport = "github.com/impulseframework/impulse/session/signin"
	oidcImport   = "github.com/impulseframework/impulse/session/oidc"
)

// The role-synchronization expressions a method file registers with: the token's roles
// reconciled through the auth's user manager, or the application the authority.
const (
	roleSyncExpr        = "signin.RoleSync(accessClient.UserManager())"
	disableRoleSyncExpr = "signin.DisableRoleSync()"
)

// azureView fills the view of a Microsoft Entra method: its role-synchronization
// expression and its imports.
func azureView(mv *methodView, m *Method) {
	mv.Type = "*azure.Method"
	if m.SyncsRoles() {
		mv.RoleSyncExpr = roleSyncExpr
	} else {
		mv.RoleSyncExpr = disableRoleSyncExpr
	}
	mv.Imports = []string{netHTTP, accessImport, "github.com/impulseframework/impulse/session/azure", signinImport}
	if m.Administered() {
		mv.Imports = append(mv.Imports, "strings")
	}
	if m.slot() == SlotListed {
		mv.Imports = append(mv.Imports, "slices")
	}
	if m.SlotFallible() {
		mv.Imports = append(mv.Imports, "github.com/go-playground/errors/v5")
	}
}

// personaView fills the view of the persona method: the session library's generic OpenID
// Connect method against the persona provider, with the application's authority for roles
// and the cast's accounts to link to.
func personaView(mv *methodView) {
	mv.Type = oidcMethodType
	mv.RoleSyncExpr = disableRoleSyncExpr
	mv.Imports = []string{netHTTP, accessImport, oidcImport, signinImport}
}

// oidcMethodType is the Go type of a registration of the generic OpenID Connect method.
const oidcMethodType = "*oidc.Method"

// oidcView fills the view of an organization's identity provider: the session library's
// generic OpenID Connect method, with the token's roles claim as the authority for roles
// when the registration synchronizes them.
func oidcView(mv *methodView, m *Method) {
	mv.Type = oidcMethodType
	if m.SyncsRoles() {
		mv.RoleSyncExpr = roleSyncExpr
	} else {
		mv.RoleSyncExpr = disableRoleSyncExpr
	}
	mv.Imports = []string{netHTTP, accessImport, oidcImport, signinImport}
}

// slotWords is a method's organization slot in words, for the slot function's comment.
func slotWords(m *Method) string {
	switch m.slot() {
	case SlotOne:
		if m.Kind == app.MethodGoogle {
			return "one Workspace (HostedDomain)"
		}

		return "one tenant (TenantID)"
	case SlotListed:
		if m.Kind == app.MethodGoogle {
			return "the Workspaces listed (Workspaces)"
		}

		return "the tenants listed (Tenants)"
	case SlotAny:
		if m.Kind == app.MethodGoogle {
			return "any Google account"
		}

		return "any work or school tenant"
	case SlotOnePersonal:
		return "one tenant (TenantID) and Microsoft personal accounts"
	case SlotAnyPersonal:
		return "any work or school tenant and Microsoft personal accounts"
	case SlotPersonal:
		return "Microsoft personal accounts alone"
	case SlotAnyWorkspace:
		return "any Workspace account"
	case SlotMixed:
		return "one Workspace (HostedDomain) and any Google account"
	default:
		return m.slot()
	}
}

// googleView fills the view of a Google Workspace method: its role-synchronization
// expression and its imports.
func googleView(mv *methodView, m *Method, settings string) {
	mv.Type = "*google.Method"
	if m.SyncsRoles() {
		mv.RoleSyncExpr = "google.RoleSync(accessClient.UserManager(), " + settings + ".GroupPrefix, lookup)"
	} else {
		mv.RoleSyncExpr = "google.DisableRoleSync()"
	}
	mv.Imports = []string{netHTTP, "strings", accessImport, "github.com/impulseframework/impulse/session/google"}
	if m.slot() == SlotListed {
		mv.Imports = append(mv.Imports, "slices")
	}
	if m.SlotFallible() || m.SyncsRoles() {
		mv.Imports = append(mv.Imports, "github.com/go-playground/errors/v5")
	}
}

func lowerFirst(s string) string {
	if s == "" {
		return ""
	}

	return strings.ToLower(s[:1]) + s[1:]
}
