package shape

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// Reading is what an application's code fixes of its sign-in: each auth with its
// methods' choices and the shape they add up to, and the application's own choices. A
// choice an administrator makes while the application runs (whether a method is open to
// sign-up, whether a tenant takes a directory) is in no code, so the reading says where
// it is made and never guesses it.
type Reading struct {
	// Auths are the session Auths the application constructs, in the order of their
	// files.
	Auths []Auth
	// Tenants is the tenant record, nil in an application without tenants.
	Tenants *app.TenantRecord
	// Directories is the directories record, a tenant's own directory, nil where the
	// tenant record declares none.
	Directories *app.DirectoryRecord
}

// Auth is one auth as the reading has it.
type Auth struct {
	// Name is the auth's name, its package under the auth directory; empty for an Auth
	// constructed anywhere else. Dir is the directory of the construction, and File and
	// Line where it is.
	Name string
	Dir  string
	File string
	Line int
	// Methods are the sign-in methods registered on the Auth, in order.
	Methods []Method
	// OneTenant is where the auth's package says a person belongs to one tenant
	// (identity.WithOneTenantPerPerson, as file:line), empty where it does not.
	OneTenant string
	// MakesTenants reports the auth a tenant is made through, in an application with
	// tenants: its administrators invite the person whose first sign-in makes one, and
	// a person who signed up to it creates their own.
	MakesTenants bool
	// Shape is the shape the choices add up to, nil where they add up to none of the
	// eight. Says is what the reading says beside it: what is chosen while the
	// application runs and what that makes of the shape, or why there is no shape.
	Shape *Shape
	Says  string
}

// Method is one sign-in method with its three choices in words.
type Method struct {
	// Name is the registration name, Kind the method, File and Line where it is
	// registered.
	Name string
	Kind string
	File string
	Line int
	// Width is how wide the method is open.
	Width Width
	// Roles says who gives the roles, and Choice the Open or Invite only choice: where
	// it is made, or why the method has none.
	Roles  string
	Choice string
}

// Who gives a method's roles, in words.
const (
	rolesApplication = "the application gives the roles"
	rolesDirectory   = "the directory gives the roles"
	rolesUnread      = "who gives the roles is not read here"
)

// The Open or Invite only choice of a method, in words: where it is made, or why the
// method has none.
const (
	choiceRunning   = "invite only until an administrator opens it to sign-up on the Sign-in page, which is chosen while the application runs"
	choiceDirectory = "no invite-only or open choice, since everyone the directory signs in becomes a user with the roles it names"
	choicePersona   = "left invite only, since its accounts are the cast the deployment seeds"
	choiceNever     = "invite only and never opened to sign-up, since a person who signed up to this auth could be given no tenant"
	choiceUnread    = "whether it can be opened to sign-up follows who gives the roles"
)

// identityImportPath is the identity library, whose option says a person belongs to one
// tenant, and oneTenantOption the option.
const (
	identityImportPath = "github.com/impulseframework/impulse/identity"
	oneTenantOption    = "WithOneTenantPerPerson"
)

// Read reads the application's sign-in choices and the shape each auth's add up to.
func Read(a *app.App) (*Reading, error) {
	profile := a.Profile()
	r := &Reading{Auths: nil, Tenants: nil, Directories: nil}
	for i := range profile.Sites {
		if r.Tenants == nil {
			r.Tenants = profile.Sites[i].TenantRecord
		}
		if r.Directories == nil {
			r.Directories = profile.Sites[i].Directories
		}
	}
	for i := range a.Auths {
		auth, err := r.auth(a, &profile, &a.Auths[i])
		if err != nil {
			return nil, err
		}
		r.Auths = append(r.Auths, *auth)
	}

	return r, nil
}

// auth reads one Auth construction: its methods' choices, what its package says of one
// tenant per person, and the shape.
func (r *Reading) auth(a *app.App, profile *app.Profile, construction *app.Auth) (*Auth, error) {
	dir := path.Dir(construction.File)
	auth := &Auth{
		Name: app.AuthPackageName(construction.File), Dir: dir, File: construction.File, Line: construction.Line,
		Methods: nil, OneTenant: "", MakesTenants: false, Shape: nil, Says: "",
	}
	creates := r.Tenants != nil && createsTenant(a, profile, importPathOf(a, dir))
	for i := range construction.Methods {
		m := &construction.Methods[i]
		width := ReadWidth(m)
		if rolesUnreadOf(m) && (m.Kind == app.MethodPersona || m.Kind == app.MethodOIDC) {
			// The scan tells the two registrations of the generic OpenID Connect method
			// apart by the configuration, and read none here, so which of them this is,
			// and with it how wide it is open, is not known.
			width = Width{Words: "", Limited: false, Read: false}
		}
		auth.Methods = append(auth.Methods, Method{
			Name: m.Name, Kind: m.Kind, File: m.File, Line: m.Line,
			Width: width, Roles: rolesOf(m), Choice: choiceOf(m, r.Tenants != nil, creates),
		})
	}
	if r.Tenants != nil {
		declared, err := oneTenantDeclared(a, dir)
		if err != nil {
			return nil, err
		}
		auth.OneTenant = declared
		auth.MakesTenants = creates && firstMethod(construction.Methods, syncsRoles) < 0
	}
	r.judge(auth, construction.Methods)

	return auth, nil
}

// importPathOf is the import path of the auth package in the directory, empty for an
// Auth constructed outside one.
func importPathOf(a *app.App, dir string) string {
	for i := range a.AuthPackages {
		if a.AuthPackages[i].Dir == dir {
			return a.AuthPackages[i].Path
		}
	}

	return ""
}

// firstMethod is the place of the first method the test reports, or -1.
func firstMethod(methods []app.Method, is func(m *app.Method) bool) int {
	for i := range methods {
		if is(&methods[i]) {
			return i
		}
	}

	return -1
}

// syncsRoles reports a method whose directory gives the roles.
func syncsRoles(m *app.Method) bool {
	return m.Authority == app.AuthorityDirectory
}

// rolesUnreadOf reports a directory method whose authority the scan does not read.
func rolesUnreadOf(m *app.Method) bool {
	return m.Directory() && m.Authority == ""
}

// givesRolesItself reports a method through an organization's directory whose roles the
// application gives: the kind of method a tenant's people come through its own
// directory by. The persona method is the test environments' and no organization's.
func givesRolesItself(m *app.Method) bool {
	return m.Directory() && m.Kind != app.MethodPersona && m.Authority == app.AuthorityApplication
}

// rolesOf says who gives a method's roles.
func rolesOf(m *app.Method) string {
	switch {
	case rolesUnreadOf(m):
		return rolesUnread
	case syncsRoles(m):
		return rolesDirectory
	default:
		return rolesApplication
	}
}

// choiceOf says where a method's Open or Invite only choice is made, or why it has
// none: a method whose directory gives the roles has none, the persona method is left
// invite only, and in an application with tenants only the auth whose people create a
// tenant of their own carries the choice.
func choiceOf(m *app.Method, tenanted, creates bool) string {
	switch {
	case rolesUnreadOf(m):
		return choiceUnread
	case syncsRoles(m):
		return choiceDirectory
	case m.Kind == app.MethodPersona:
		return choicePersona
	case tenanted && !creates:
		return choiceNever
	default:
		return choiceRunning
	}
}

// createsTenant reports the auth whose people create a tenant of their own, as the
// generator decides it for the auth's management: a site has a tenant record, a session
// outlet of the site serves it, and every session outlet that does is bound to the
// auth.
func createsTenant(a *app.App, profile *app.Profile, importPath string) bool {
	if importPath == "" {
		return false
	}
	for i := range profile.Sites {
		site := &profile.Sites[i]
		if site.TenantRecord == nil {
			continue
		}
		serving := recordOutlets(a, site.TenantRecord)
		served, bound := false, true
		outlets := site.AllOutlets()
		for j := range outlets {
			o := &outlets[j]
			if !o.ServesSessions || !slices.Contains(serving, o.Name) {
				continue
			}
			served = true
			if o.Auth == nil || o.Auth.ImportPath != importPath {
				bound = false
			}
		}
		if served && bound {
			return true
		}
	}

	return false
}

// recordOutlets names the outlets the tenant record is served on: the ones its @outlet
// lists, or the default outlet when it lists none.
func recordOutlets(a *app.App, record *app.TenantRecord) []string {
	for i := range a.OutletMembers {
		if member := &a.OutletMembers[i]; member.File == record.File && member.Name == record.Name {
			return member.Outlets
		}
	}

	return []string{app.DefaultOutletName}
}

// oneTenantDeclared reads the hand-written files of an auth's package for the option
// that says a person belongs to one tenant, identity.WithOneTenantPerPerson, which the
// package hands its identity client: where it is called (file:line), or empty.
func oneTenantDeclared(a *app.App, dir string) (string, error) {
	entries, err := os.ReadDir(a.Abs(dir))
	if err != nil {
		return "", errors.Wrap(err, "os.ReadDir()")
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, "zz_gen_") {
			continue
		}
		rel := path.Join(dir, name)
		src, err := os.ReadFile(a.Abs(rel))
		if err != nil {
			return "", errors.Wrap(err, "os.ReadFile()")
		}
		if !strings.Contains(string(src), oneTenantOption) {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
		if err != nil {
			return "", errors.Wrap(err, "parser.ParseFile()")
		}
		if call := oneTenantCall(f); call != nil {
			return fmt.Sprintf("%s:%d", rel, fset.Position(call.Pos()).Line), nil
		}
	}

	return "", nil
}

// oneTenantCall is the file's first call of the identity library's option that says a
// person belongs to one tenant, nil where it calls none.
func oneTenantCall(f *ast.File) *ast.CallExpr {
	local := ""
	for _, spec := range f.Imports {
		if strings.Trim(spec.Path.Value, `"`) != identityImportPath {
			continue
		}
		local = path.Base(identityImportPath)
		if spec.Name != nil {
			local = spec.Name.Name
		}
	}
	if local == "" {
		return nil
	}
	var found *ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found != nil {
			return found == nil
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == oneTenantOption {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == local {
				found = call
			}
		}

		return found == nil
	})

	return found
}

// judge names the shape the auth's choices add up to, or says why they add up to none
// of the eight. Without tenants an auth whose directory gives the roles is Directory
// roles, and any other starts as Invite only and becomes Directory members or Open
// sign-up by the method an administrator opens. With tenants the shape is told by
// whether a person belongs to one tenant and whether a tenant's people come through its
// own directory.
func (r *Reading) judge(auth *Auth, methods []app.Method) {
	none := func(format string, args ...any) {
		auth.Shape, auth.Says = nil, fmt.Sprintf(format, args...)
	}
	synced := firstMethod(methods, syncsRoles)
	switch unread := firstMethod(methods, rolesUnreadOf); {
	case len(methods) == 0:
		none("no sign-in method is read on its Auth")
	case unread >= 0:
		none("who gives the roles of its %s method is not read here, and the shape follows it", methods[unread].Name)
	case synced >= 0 && r.Tenants != nil:
		none("its %s method takes the roles from the directory, and no shape has both tenants and roles from a directory", methods[synced].Name)
	case synced >= 0 && len(methods) > 1:
		none("its %s method takes the roles from the directory beside another method, and a method whose roles come from the directory stands alone in its auth (impulse check refuses it)", methods[synced].Name)
	case synced >= 0:
		auth.named(directoryRoles, "")
	case r.Tenants == nil:
		auth.named(inviteOnly, whenOpened(methods))
	default:
		r.judgeTenants(auth, methods)
	}
}

// named sets the auth's shape by its number, and what is said beside it.
func (auth *Auth) named(number int, says string) {
	s := numbered(number)
	auth.Shape, auth.Says = &s, says
}

// whenOpened says, of an auth without tenants whose roles the application gives, what
// the choice made while the application runs makes of its shape: every method starts
// invite only, a method limited to its organizations is Directory members once opened,
// and a method anyone can use is Open sign-up.
func whenOpened(methods []app.Method) string {
	var members, open, unread []string
	for i := range methods {
		m := &methods[i]
		if m.Kind == app.MethodPersona {
			continue
		}
		switch width := ReadWidth(m); {
		case !width.Read:
			unread = append(unread, m.Name)
		case width.Limited:
			members = append(members, m.Name)
		default:
			open = append(open, m.Name)
		}
	}
	var makes []string
	if len(members) > 0 {
		makes = append(makes, fmt.Sprintf("opening %s makes it %s", orList(members), numbered(directoryMembers)))
	}
	if len(open) > 0 {
		makes = append(makes, fmt.Sprintf("opening %s makes it %s", orList(open), numbered(openSignUp)))
	}
	if len(unread) > 0 {
		makes = append(makes, fmt.Sprintf("opening %s makes it %s or %s, by how wide the method is", orList(unread), numbered(directoryMembers), numbered(openSignUp)))
	}
	if len(makes) == 0 {
		return "No method of it is opened to sign-up."
	}

	return "It starts so, and stays so until an administrator opens a method to sign-up on the Sign-in page, which is chosen while the application runs: " + strings.Join(makes, ", and ") + "."
}

// judgeTenants names the shape of an auth whose roles the application gives, in an
// application with tenants: tenant-owned users where its package says a person belongs
// to one tenant, global users otherwise; by directory, or each tenant chooses, where
// the tenants can have directories and a method of the auth goes through an
// organization's directory, and invite only where its people cannot come through one.
func (r *Reading) judgeTenants(auth *Auth, methods []app.Method) {
	var through []string
	for i := range methods {
		if givesRolesItself(&methods[i]) {
			through = append(through, methods[i].Name)
		}
	}
	invited, directory := globalInviteOnly, globalEachTenantChooses
	if auth.OneTenant != "" {
		invited, directory = tenantOwnedInviteOnly, tenantOwnedByDirectory
	}
	switch {
	case r.Directories != nil && len(through) > 0:
		auth.named(directory, fmt.Sprintf("A tenant's people can come through its organization's directory (%s). Whether a tenant takes a directory, and whether that directory's people get an account without an invitation, is each tenant's choice, made while the application runs on the tenant's Sign-in page.", methodsNamed(through)))
	case r.Directories != nil:
		auth.named(invited, fmt.Sprintf("The tenants can have directories, but no method of this auth goes through an organization's directory, so its people come by invitation alone; a directory method whose roles the application gives (impulse add method) makes it %s.", numbered(directory)))
	case len(through) > 0:
		auth.named(invited, "The tenant record declares no directories, so no tenant's people come through one.")
	default:
		auth.named(invited, "")
	}
}

// DirectoryMethods lists the application's sign-in methods a tenant's people could come
// through its own directory by: every method through an organization's directory whose
// roles the application gives, each as a refusal names it, with the auth it is on and
// where it is registered.
func DirectoryMethods(a *app.App) []string {
	var found []string
	for i := range a.Auths {
		construction := &a.Auths[i]
		name := app.AuthPackageName(construction.File)
		if name == "" {
			name = path.Dir(construction.File)
		}
		for j := range construction.Methods {
			if m := &construction.Methods[j]; givesRolesItself(m) {
				found = append(found, fmt.Sprintf("the %s auth's %s method (%s:%d)", name, m.Name, m.File, m.Line))
			}
		}
	}

	return found
}

// OpeningOf is what opening a sign-in method to sign-up does on an auth the application
// has, by its name.
func OpeningOf(a *app.App, auth string) Opening {
	profile := a.Profile()
	if !slices.ContainsFunc(profile.Sites, func(site app.Site) bool { return site.TenantRecord != nil }) {
		return OpensWithRole
	}
	for i := range a.AuthPackages {
		if p := &a.AuthPackages[i]; p.Name == auth && createsTenant(a, &profile, p.Path) {
			return OpensToCreateTenant
		}
	}

	return NeverOpened
}

// OpeningAt is what opening a sign-in method to sign-up does on an auth about to be
// bound to the outlet of the site: with no tenant record a person who signs up is given
// a role, and with one they create a tenant where the outlet is the only session outlet
// that serves the record.
func OpeningAt(a *app.App, site *app.Site, outlet *app.Outlet) Opening {
	if site.TenantRecord == nil {
		return OpensWithRole
	}
	serving := recordOutlets(a, site.TenantRecord)
	if !slices.Contains(serving, outlet.Name) {
		return NeverOpened
	}
	outlets := site.AllOutlets()
	for i := range outlets {
		if o := &outlets[i]; o.Name != outlet.Name && o.ServesSessions && slices.Contains(serving, o.Name) {
			return NeverOpened
		}
	}

	return OpensToCreateTenant
}

// methodsNamed names sign-in methods in a sentence: "the google method", "the google
// and sign-up methods".
func methodsNamed(names []string) string {
	if len(names) == 1 {
		return "the " + names[0] + " method"
	}

	return "the " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + " methods"
}

// orList joins names as a sentence lists alternatives: "a", "a or b", "a, b or c".
func orList(names []string) string {
	if len(names) == 1 {
		return names[0]
	}

	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
