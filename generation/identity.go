package generation

import (
	"bytes"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/generation/parser"
	"github.com/impulseframework/impulse/session"
	"golang.org/x/tools/go/packages"
)

// The management of an auth's people as generated code (GenerateIdentity). For every
// Auth the generated router binds, the generator writes three packages into a tree it
// owns:
//
//   - <tree>/internal/<auth>resources: a resource declaration over each of the auth's
//     tables the schema holds (the session library's and the permission store's), with
//     the query builders and patch types the resource templates write for any resource.
//     The package is internal to the tree, so no application code reaches a patch
//     constructor: the compiler refuses the import. In an application with a tenant
//     record it also holds the views that are a tenant's lists (identity_lists.go).
//   - <tree>/<auth>identity: the auth's methods, each an @rpc struct whose Execute is one
//     call into the identity package's rules with the auth's own patches behind it, and
//     the Client they run over. An application calls them in its own transactions.
//   - <tree>/<auth>handlers: the generated frames of those resources and methods, the
//     ones the handler templates write for any resource, over the Deps the application
//     hands them, and the record of what changes the tenant's lists.
//
// The declarations and the methods are written before the packages are loaded, so the
// same run extracts them as it extracts the application's own; from there they are
// resources and methods like any other, with a home of their own for everything written
// per package. The application's handler package gets the delegating methods the route
// tables name, so the routes, the collection, the TypeScript and the generated tests see
// nothing new.

// Where an instance's packages sit in the tree: the methods' and the handlers' directly
// under it, named for the auth, and the declarations' under its internal directory.
const (
	identityInternalDir     = "internal"
	identityResourcesSuffix = "resources"
	identityMethodsSuffix   = "identity"
	identityHandlersSuffix  = "handlers"
)

// identityInstance is one auth whose management the run generates.
type identityInstance struct {
	// importPath is the auth package, as the generator program declared it (Auth).
	importPath string
	// auth is the auth's name in PascalCase, the prefix of its tables (Crew).
	auth string
	// outlets are the outlets serving the instance: the auth's own first, then the
	// ones that declare Manages on it, in declaration order.
	outlets []string
	// usernameIsEmail marks a password method whose username is the person's address:
	// the users table derives the address from the username, so nothing writes it.
	usernameIsEmail bool
	// password marks an auth that declares the password method: the one kind of auth
	// whose people may hold a password, and so be required to change it.
	password bool
	// rolesSynced marks an auth whose roles a directory writes at every sign-in
	// (SyncsRoles): the directory is the authority for every membership the auth holds,
	// so the instance carries no method that assigns or removes one.
	rolesSynced bool
	// invited marks an auth with a registration that admits invited users alone
	// (InvitedUsers): its people exist before their first sign-in, so someone creates
	// them.
	invited bool
	// session and policy name the auth's tables: the session library's and the
	// permission store's, each by the library's own rule for the prefix.
	session session.Tables
	policy  access.Tables
	// resources, methods and handlers are the three packages the run writes.
	resources packageDir
	methods   packageDir
	handlers  packageDir
	// tables are the instance's tables the schema holds, by the kind the templates
	// know them under.
	tables map[identityTable]bool
	// tenant is the application's tenant record, nil when it declares none: with one
	// the instance carries the tenant's lists (identity_lists.go).
	tenant *identityTenant
}

// identityTable names one of an auth's tables by what it holds.
type identityTable string

const (
	identityUsers          identityTable = "users"
	identitySessions       identityTable = "sessions"
	identityIdentities     identityTable = "identities"
	identityTokens         identityTable = "tokens"
	identityMailLinks      identityTable = "mailLinks"
	identitySignInAttempts identityTable = "signInAttempts"
	identityAuthEvents     identityTable = "authEvents"
	identityImpersonations identityTable = "impersonations"
	identityTrustedDevices identityTable = "trustedDevices"
	identityMemberships    identityTable = "memberships"
	identityRoles          identityTable = "roles"
	identityRoleGrants     identityTable = "roleGrants"
)

// identityDeclaration is one table of an auth as the generated management declares it:
// the table by kind, the struct declared over it (the auth's name, then suffix; its
// plural is the table), and the template of the declaration.
type identityDeclaration struct {
	kind     identityTable
	suffix   string
	template string
}

// identityDeclarations are the tables the generated management declares, where the
// schema holds them: the session library's, then the permission store's. The sign-ins
// in progress and the device authorizations are the sign-in paths' own passing state
// and are not declared.
func identityDeclarations() []identityDeclaration {
	return []identityDeclaration{
		{kind: identityUsers, suffix: "SessionUser", template: identityUsersTemplate},
		{kind: identitySessions, suffix: "Session", template: identitySessionsTemplate},
		{kind: identityIdentities, suffix: "SessionIdentity", template: identityIdentitiesTemplate},
		{kind: identityTokens, suffix: "SessionToken", template: identityTokensTemplate},
		{kind: identityMailLinks, suffix: "SessionMailLink", template: identityMailLinksTemplate},
		{kind: identitySignInAttempts, suffix: "SessionSignInAttempt", template: identitySignInAttemptsTemplate},
		{kind: identityAuthEvents, suffix: "SessionAuthEvent", template: identityAuthEventsTemplate},
		{kind: identityImpersonations, suffix: "SessionImpersonation", template: identityImpersonationsTemplate},
		{kind: identityTrustedDevices, suffix: "SessionTrustedDevice", template: identityTrustedDevicesTemplate},
		{kind: identityMemberships, suffix: "UserRole", template: identityMembershipsTemplate},
		{kind: identityRoles, suffix: "Role", template: identityRolesTemplate},
		{kind: identityRoleGrants, suffix: "RoleGrant", template: identityRoleGrantsTemplate},
	}
}

// tableName is the name of the instance's table of a kind.
func (i *identityInstance) tableName(kind identityTable) string {
	switch kind {
	case identityUsers:
		return i.session.Users
	case identitySessions:
		return i.session.Sessions
	case identityIdentities:
		return i.session.Identities
	case identityTokens:
		return i.session.Tokens
	case identityMailLinks:
		return i.session.MailLinks
	case identitySignInAttempts:
		return i.session.SignInAttempts
	case identityAuthEvents:
		return i.session.AuthEvents
	case identityImpersonations:
		return i.session.Impersonations
	case identityTrustedDevices:
		return i.session.TrustedDevices
	case identityMemberships:
		return i.policy.UserRoles
	case identityRoles:
		return i.policy.Roles
	case identityRoleGrants:
		return i.policy.RoleGrants
	default:
		panic(fmt.Sprintf("unexpected identityTable: %q", kind))
	}
}

// has reports whether the schema holds the instance's table of a kind.
func (i *identityInstance) has(kind identityTable) bool {
	return i.tables[kind]
}

// accessor is the method the application hands the instance's client through
// (CrewIdentity).
func (i *identityInstance) accessor() string {
	return i.auth + "Identity"
}

// identityInstances derives the instances of the run: one per declared Auth, served on
// the outlets that declare the Auth and on those that manage it (Manages). Nothing
// without GenerateIdentity.
func (r *resourceGenerator) identityInstances() ([]*identityInstance, error) {
	if r.identityTree == "" {
		return nil, nil
	}
	if !r.genRoutes || !r.genHandlers {
		return nil, errors.New("GenerateIdentity requires GenerateRoutes and GenerateHandlers: the generated management is served on the generated route tables through the application's handlers")
	}

	var instances []*identityInstance
	byPath := make(map[string]*identityInstance)
	for _, outlet := range r.allOutlets() {
		if outlet.auth == nil {
			continue
		}
		inst, ok := byPath[outlet.auth.importPath]
		if !ok {
			var err error
			if inst, err = r.newIdentityInstance(outlet.auth); err != nil {
				return nil, err
			}
			byPath[outlet.auth.importPath] = inst
			instances = append(instances, inst)
		}
		inst.outlets = append(inst.outlets, outlet.name)
	}
	for _, outlet := range r.allOutlets() {
		for _, managed := range outlet.manages {
			inst, ok := byPath[managed]
			if !ok {
				return nil, errors.Newf("outlet %q: Manages(%q) names no declared auth; declare it with Auth on the outlet it signs in", outlet.name, managed)
			}
			if outlet.auth != nil && outlet.auth.importPath == managed {
				return nil, errors.Newf("outlet %q: Manages(%q) names the outlet's own auth, which it serves the management of already; remove it", outlet.name, managed)
			}
			if !slices.Contains(inst.outlets, outlet.name) {
				inst.outlets = append(inst.outlets, outlet.name)
			}
		}
	}
	for i, inst := range instances {
		for _, prior := range instances[:i] {
			if prior.auth == inst.auth {
				return nil, errors.Newf("Auth(%q) and Auth(%q) both name their tables %s…: the auth packages' names must differ", prior.importPath, inst.importPath, inst.auth)
			}
		}
	}
	if len(instances) == 0 {
		return nil, errors.New("GenerateIdentity names a tree and the program declares no Auth: the management is generated per auth (Auth on GenerateRoutes or WithRouterOutlet)")
	}

	return instances, nil
}

// newIdentityInstance derives one instance from its Auth declaration: the prefix is the
// auth package's name in PascalCase, the rule the auth packages name their tables by
// (TablePrefix), and the tables are the two libraries' own names under it. The tables
// the schema does not hold are left out; an auth with no users table in the schema is
// refused, since nothing could be managed.
func (r *resourceGenerator) newIdentityInstance(auth *outletAuth) (*identityInstance, error) {
	name := caser.ToPascal(assumedPackageName(auth.importPath))
	sessionTables, err := session.TablesWithPrefix(name)
	if err != nil {
		return nil, errors.Wrapf(err, "Auth(%q): session.TablesWithPrefix()", auth.importPath)
	}
	policyTables, err := access.TablesWithPrefix(name, "")
	if err != nil {
		return nil, errors.Wrapf(err, "Auth(%q): access.TablesWithPrefix()", auth.importPath)
	}
	lower := strings.ToLower(name)
	inst := &identityInstance{
		importPath: auth.importPath,
		auth:       name,
		session:    sessionTables,
		policy:     policyTables,
		resources:  packageDir(filepath.Join(r.identityTree.Dir(), identityInternalDir, lower+identityResourcesSuffix)),
		methods:    packageDir(filepath.Join(r.identityTree.Dir(), lower+identityMethodsSuffix)),
		handlers:   packageDir(filepath.Join(r.identityTree.Dir(), lower+identityHandlersSuffix)),
		tables:     make(map[identityTable]bool),
	}
	for _, m := range auth.methods {
		if m.kind == methodPassword {
			inst.password = true
			inst.usernameIsEmail = m.usernameIsEmail
		}
		if m.syncsRoles {
			inst.rolesSynced = true
		}
		if m.invited {
			inst.invited = true
		}
	}
	for _, decl := range identityDeclarations() {
		if _, ok := r.tableMap[inst.tableName(decl.kind)]; ok {
			inst.tables[decl.kind] = true
		}
	}
	// The permission store's tables come together: a membership's role is read from
	// the roles table in the transaction that writes the membership.
	if inst.has(identityMemberships) != inst.has(identityRoles) {
		return nil, errors.Newf("Auth(%q): the schema holds one of %s and %s and not the other; they are the permission store's tables under the auth's prefix and are migrated together", auth.importPath, inst.policy.UserRoles, inst.policy.Roles)
	}
	for _, kind := range []identityTable{identityUsers, identitySessions} {
		if !inst.has(kind) {
			return nil, errors.Newf("Auth(%q): the schema holds no table %s, so there is nothing of the auth to manage; its tables are named by its package's name in PascalCase (TablePrefix = %q), each the session library's own name under it", auth.importPath, inst.tableName(kind), name)
		}
	}

	return inst, nil
}

// createsUsers reports whether the auth's people are created by hand: a password method
// or a registration that admits invited users alone gives it that, and a directory that
// writes the roles takes it away, since a person is created holding a role.
func (inst *identityInstance) createsUsers() bool {
	return (inst.password || inst.invited) && !inst.rolesSynced
}

// prepareIdentity derives the run's instances and writes their sources, the declarations
// and the methods, before the packages are loaded: the load that follows reads them as
// it reads the application's own.
func (r *resourceGenerator) prepareIdentity() error {
	instances, err := r.identityInstances()
	if err != nil {
		return err
	}
	r.identity = instances
	if len(instances) == 0 {
		return nil
	}
	begin := time.Now()

	if err := r.settleIdentityTree(); err != nil {
		return err
	}
	// The tenant record decides whether an instance carries the tenant's lists, and
	// names them: it is read off the sources and the schema, since nothing is loaded yet.
	tenant, err := r.readIdentityTenant()
	if err != nil {
		return err
	}
	for _, inst := range instances {
		inst.tenant = tenant
		if err := inst.refuseListNamesTaken(); err != nil {
			return err
		}
		for _, dir := range []packageDir{inst.resources, inst.methods, inst.handlers} {
			r.output.registerOutput(dir.Dir(), headerComment)
		}
		if err := r.writeIdentitySources(inst); err != nil {
			return errors.Wrapf(err, "the generated management of Auth(%q)", inst.importPath)
		}
		if err := r.removeStaleIdentitySources(inst); err != nil {
			return errors.Wrapf(err, "the generated management of Auth(%q)", inst.importPath)
		}
		for _, dir := range []packageDir{inst.resources, inst.methods} {
			if !slices.Contains(r.loadPackages, dir.Dir()) {
				r.loadPackages = append(r.loadPackages, dir.Dir())
			}
		}
		if !slices.Contains(r.outputs, inst.handlers) {
			r.outputs = append(r.outputs, inst.handlers)
		}
	}
	log.Printf("Wrote the identity sources of %d auth(s) in %s\n", len(instances), time.Since(begin))

	return nil
}

// removeStaleIdentitySources removes what an earlier run wrote for an instance and this
// one has no source for (a table the schema no longer holds, a method the declaration
// no longer calls for): the source, and the generated file that stood beside it. It runs
// before the packages are loaded, since the sweep at the end of the run would come too
// late: the load would have read the declaration and the run generated for it once
// more, or met a generated file whose declaration is gone. The files generated for the
// package as a whole are left to that sweep.
func (r *resourceGenerator) removeStaleIdentitySources(inst *identityInstance) error {
	for _, dir := range []packageDir{inst.resources, inst.methods} {
		entries, err := os.ReadDir(dir.Dir())
		if err != nil {
			return errors.Wrap(err, "os.ReadDir()")
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || name == generatedGoFileName(rpcInterfaceOutputName) {
				continue
			}
			source := strings.TrimPrefix(name, genPrefix+"_")
			if r.output.wrote(filepath.Join(dir.Dir(), source)) {
				continue
			}
			if err := os.Remove(filepath.Join(dir.Dir(), name)); err != nil {
				return errors.Wrap(err, "os.Remove()")
			}
		}
	}

	return nil
}

// settleIdentityTree reads the tree before the run writes into it and leaves it holding
// only what the generator writes. What the generator did not write is refused: a Go file
// without the generation header, or one in a package at a place the generator puts none.
// The tree is the generator's alone, since a package under it may import the declarations
// the tree keeps internal. What an earlier run wrote for an auth the program no longer
// declares (its three packages, every file under the header) is removed, as the generator
// removes any output it no longer writes: an auth taken out of the program, or renamed,
// leaves nothing for a person to clear before the next run. Nothing is removed from a
// tree that is refused.
func (r *resourceGenerator) settleIdentityTree() error {
	foreign, retired, err := r.readIdentityTree()
	if err != nil {
		return err
	}
	if len(foreign) > 0 {
		return errors.Newf("GenerateIdentity(%q): the tree holds %d file(s) the generator did not write (%s); the tree is the generator's alone, since a package under it may import the declarations it keeps internal: move them out of the tree", r.identityTree.Dir(), len(foreign), strings.Join(foreign, ", "))
	}

	var emptied []string
	for _, file := range retired {
		if err := os.Remove(file); err != nil {
			return errors.Wrap(err, "os.Remove()")
		}
		if dir := filepath.Dir(file); !slices.Contains(emptied, dir) {
			emptied = append(emptied, dir)
		}
	}
	for _, dir := range emptied {
		// A directory that still holds something that is no Go file is left with it:
		// removing a directory that is not empty answers fs.ErrExist.
		switch err := os.Remove(dir); {
		case err == nil:
			log.Printf("Removed %s: the program no longer declares its auth\n", dir)
		case !errors.Is(err, fs.ErrExist):
			return errors.Wrap(err, "os.Remove()")
		}
	}

	return nil
}

// readIdentityTree sorts the tree's Go files the run does not own into the ones the
// generator did not write (foreign) and the ones an earlier run wrote for an auth the
// program no longer declares (retired): a file under the generation header in a
// directory where the generator puts an instance's package.
func (r *resourceGenerator) readIdentityTree() (foreign, retired []string, err error) {
	owned := make(map[string]bool)
	for _, inst := range r.identity {
		for _, dir := range []packageDir{inst.resources, inst.methods, inst.handlers} {
			owned[filepath.Clean(dir.Dir())] = true
		}
	}
	// The tree is read through a root confined to it, never by a walked path.
	tree := filepath.Clean(r.identityTree.Dir())
	root, err := os.OpenRoot(tree)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}

		return nil, nil, errors.Wrap(err, "os.OpenRoot()")
	}
	defer root.Close()

	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "fs.WalkDirFunc")
		}
		if d.IsDir() || !strings.HasSuffix(name, ".go") {
			return nil
		}
		file := filepath.Join(tree, filepath.FromSlash(name))
		content, err := root.ReadFile(name)
		if err != nil {
			return errors.Wrap(err, "os.Root.ReadFile()")
		}
		switch generated := bytes.HasPrefix(content, []byte(generationHeader)); {
		case !generated:
			foreign = append(foreign, file)
		case owned[filepath.Dir(file)]:
		case identityPackagePlace(path.Dir(name)):
			retired = append(retired, file)
		default:
			foreign = append(foreign, file)
		}

		return nil
	})
	if err != nil {
		return nil, nil, errors.Wrap(err, "fs.WalkDir()")
	}

	return foreign, retired, nil
}

// identityPackagePlace reports whether a directory of the tree, slash-separated and
// relative to it, is where the generator puts a package of an instance: the methods'
// (<auth>identity) and the handlers' (<auth>handlers) directly under the tree, the
// declarations' (<auth>resources) under its internal directory.
func identityPackagePlace(dir string) bool {
	parent, name := path.Split(dir)
	named := func(suffix string) bool {
		return len(name) > len(suffix) && strings.HasSuffix(name, suffix)
	}
	switch path.Clean(parent) {
	case ".":
		return named(identityMethodsSuffix) || named(identityHandlersSuffix)
	case identityInternalDir:
		return named(identityResourcesSuffix)
	default:
		return false
	}
}

// identitySourceData is what the instance's source templates render from.
type identitySourceData struct {
	Source string
	// Package is the package the file is written into.
	Package string
	*identityInstance
	// ResourcesPackage and MethodsPackage are the instance's two loaded packages by
	// name, and LocalPackageImports the import lines the templates share.
	ResourcesPackage    string
	MethodsPackage      string
	LocalPackageImports string
	// Outlets is the @outlet argument of every declaration and method.
	Outlets string
	// Tables and Policy name the instance's tables: the session library's and the
	// permission store's.
	Tables session.Tables
	Policy access.Tables
	// ForTenant marks a file that holds a method one tenant runs (identity_tenant.go):
	// the templates the two kinds of method share render the tenant's from it.
	ForTenant bool
}

// Singular names of the instance's resources, the structs over its tables.
func (d *identitySourceData) User() string          { return d.auth + "SessionUser" }
func (d *identitySourceData) Session() string       { return d.auth + "Session" }
func (d *identitySourceData) Identity() string      { return d.auth + "SessionIdentity" }
func (d *identitySourceData) Token() string         { return d.auth + "SessionToken" }
func (d *identitySourceData) MailLink() string      { return d.auth + "SessionMailLink" }
func (d *identitySourceData) SignInAttempt() string { return d.auth + "SessionSignInAttempt" }
func (d *identitySourceData) AuthEvent() string     { return d.auth + "SessionAuthEvent" }
func (d *identitySourceData) Impersonation() string { return d.auth + "SessionImpersonation" }
func (d *identitySourceData) TrustedDevice() string { return d.auth + "SessionTrustedDevice" }
func (d *identitySourceData) Membership() string    { return d.auth + "UserRole" }
func (d *identitySourceData) Role() string          { return d.auth + "Role" }
func (d *identitySourceData) RoleGrant() string     { return d.auth + "RoleGrant" }

// Auth is the auth's name in PascalCase, and Accessor the method the application hands
// the instance's client through.
func (d *identitySourceData) Auth() string     { return d.auth }
func (d *identitySourceData) Accessor() string { return d.accessor() }

// Has reports whether the schema holds the instance's table of a kind.
func (d *identitySourceData) Has(kind string) bool { return d.has(identityTable(kind)) }

// UsernameIsEmail marks an auth whose usernames are the addresses.
func (d *identitySourceData) UsernameIsEmail() bool { return d.usernameIsEmail }

// Password marks an auth that declares the password method.
func (d *identitySourceData) Password() bool { return d.password }

// RolesSynced reports whether a directory writes the roles of the auth's people at
// every sign-in: no role is given at a sign-in by the application's hand, so the client
// answers none of the hooks that give one.
func (d *identitySourceData) RolesSynced() bool { return d.rolesSynced }

// PasswordOnCreate says what a create does with a password: "required" where the
// password is the auth's one way in, "optional" beside a registration that admits
// invited users alone, and "none" where the auth has no password method.
func (d *identitySourceData) PasswordOnCreate() string {
	switch {
	case !d.password:
		return "none"
	case d.invited:
		return "optional"
	default:
		return "required"
	}
}

// AuthImportPath is the auth package the instance was declared from.
func (d *identitySourceData) AuthImportPath() string { return d.importPath }

// identitySourceFile is one source file of an instance: where it goes, the template it
// renders from, the tables whose presence in the schema it depends on, and whether it
// holds a method one tenant runs.
type identitySourceFile struct {
	dir      packageDir
	name     string
	template string
	needs    []identityTable
	tenant   bool
}

// writeIdentitySources writes one instance's declarations, methods and handler base.
func (r *resourceGenerator) writeIdentitySources(inst *identityInstance) error {
	data := &identitySourceData{
		identityInstance: inst,
		ResourcesPackage: inst.resources.Package(),
		MethodsPackage:   inst.methods.Package(),
		Outlets:          strings.Join(inst.outlets, ", "),
		Tables:           inst.session,
		Policy:           inst.policy,
	}
	// The struct names are held to the engine's rule here, where the refusal can say
	// what went wrong: a resource's table is its struct's name in the plural.
	files := []identitySourceFile{{dir: inst.resources, name: "resources", template: identityResourcesTemplate}}
	for _, decl := range identityDeclarations() {
		struc, table := inst.auth+decl.suffix, inst.tableName(decl.kind)
		if got := r.pluralize(struc); got != table {
			return errors.Newf("the declaration over table %s would be struct %s, whose plural is %s: the generator's plural rules do not carry the auth's name %q back to its table (see WithPluralOverrides)", table, struc, got, inst.auth)
		}
		files = append(files, identitySourceFile{dir: inst.resources, name: fileStem(table), template: decl.template, needs: []identityTable{decl.kind}})
	}
	// The tenant's lists, beside the tables they read, where the application has a
	// tenant record.
	files = append(files, r.listSources(inst)...)
	files = append(files, []identitySourceFile{
		{dir: inst.methods, name: "client", template: identityClientTemplate},
		{dir: inst.methods, name: "store", template: identityStoreTemplate},
		{dir: inst.methods, name: fileStem("Disable" + inst.auth + "User"), template: identityDisableUserTemplate, needs: []identityTable{identityUsers}},
		{dir: inst.methods, name: fileStem("Delete" + inst.auth + "User"), template: identityDeleteUserTemplate, needs: []identityTable{identityUsers}},
		{dir: inst.methods, name: fileStem("Revoke" + inst.auth + "UserTrustedDevices"), template: identityRevokeTrustedDevicesTemplate, needs: []identityTable{identityTrustedDevices}},
		{dir: inst.methods, name: fileStem("Unlock" + inst.auth + "SignIn"), template: identityUnlockSignInTemplate, needs: []identityTable{identitySignInAttempts}},
		{dir: inst.methods, name: fileStem("Enable" + inst.auth + "User"), template: identityEnableUserTemplate, needs: []identityTable{identityUsers}},
		{dir: inst.methods, name: fileStem("End" + data.Session()), template: identityEndSessionTemplate, needs: []identityTable{identitySessions}},
		{dir: inst.methods, name: fileStem("End" + inst.auth + "UserSessions"), template: identityEndUserSessionsTemplate, needs: []identityTable{identitySessions}},
		{dir: inst.methods, name: fileStem("End" + data.Impersonation()), template: identityEndImpersonationTemplate, needs: []identityTable{identityImpersonations}},
		{dir: inst.methods, name: fileStem(inst.unlinkIdentityMethod(false)), template: identityUnlinkIdentityTemplate, needs: []identityTable{identityIdentities}},
		{dir: inst.methods, name: fileStem("Revoke" + data.Token()), template: identityRevokeTokenTemplate, needs: []identityTable{identityTokens}},
		{dir: inst.handlers, name: "handlers", template: identityHandlersTemplate},
	}...)
	// Only an auth that declares the password method has people who hold one.
	if inst.password {
		files = append(files,
			identitySourceFile{dir: inst.methods, name: fileStem("Require" + inst.auth + "PasswordChange"), template: identityRequirePasswordChangeTemplate, needs: []identityTable{identityUsers}},
			identitySourceFile{dir: inst.methods, name: fileStem("Set" + inst.auth + "TemporaryPassword"), template: identitySetTemporaryPasswordTemplate, needs: []identityTable{identityUsers}},
		)
	}
	// Where the username is the person's address it changes when the person verifies
	// a new address, never by a rename, which the session library refuses there too.
	// The address is the person's own entry there too, so nobody sets it by hand.
	if !inst.usernameIsEmail {
		files = append(files,
			identitySourceFile{dir: inst.methods, name: fileStem("Rename" + inst.auth + "User"), template: identityRenameUserTemplate, needs: []identityTable{identityUsers}},
			identitySourceFile{dir: inst.methods, name: fileStem("Set" + inst.auth + "UserEmail"), template: identitySetUserEmailTemplate, needs: []identityTable{identityUsers}},
		)
	}
	// A person is created by hand where a password is a way in or a registration admits
	// invited users alone; elsewhere a directory admits its own, and the first sign-in
	// creates the row. A person is created holding a role, so an auth whose directory
	// writes the roles creates nobody here either.
	if inst.createsUsers() {
		files = append(files, identitySourceFile{dir: inst.methods, name: fileStem("Create" + inst.auth + "User"), template: identityCreateUserTemplate, needs: []identityTable{identityUsers, identityMemberships}})
	}
	// The memberships of an auth whose directory writes them are read here and written
	// nowhere: a role assigned by hand would be gone at the person's next sign-in.
	if !inst.rolesSynced {
		files = append(files,
			// An invitation gives a role, which the acceptance seats the person in.
			identitySourceFile{dir: inst.methods, name: fileStem("Invite" + inst.auth + "User"), template: identityInviteUserTemplate, needs: []identityTable{identityMailLinks, identityMemberships}},
			identitySourceFile{dir: inst.methods, name: fileStem("Withdraw" + inst.auth + "Invitation"), template: identityWithdrawInvitationTemplate, needs: []identityTable{identityMailLinks, identityMemberships}},
			identitySourceFile{dir: inst.methods, name: fileStem("Assign" + inst.auth + "Role"), template: identityAssignRoleTemplate, needs: []identityTable{identityMemberships}},
			identitySourceFile{dir: inst.methods, name: fileStem("Remove" + inst.auth + "Role"), template: identityRemoveRoleTemplate, needs: []identityTable{identityMemberships}},
		)
	}
	// The methods one tenant runs, and the computed resources beside them, where the
	// application has a tenant record.
	files = append(files, inst.tenantMethodSources()...)
	files = append(files, r.tenantComputedSources(inst)...)
	for _, file := range files {
		if slices.ContainsFunc(file.needs, func(kind identityTable) bool { return !inst.has(kind) }) {
			continue
		}
		fileData := *data
		fileData.ForTenant = file.tenant
		fileData.Source = fmt.Sprintf("Auth(%q)", inst.importPath)
		fileData.Package = file.dir.Package()
		fileData.LocalPackageImports = r.identityImports(inst)
		destination := filepath.Join(file.dir.Dir(), file.name+".go")
		if err := r.writeFormattedGoFile(destination, "identity:"+file.name, file.template, &fileData); err != nil {
			return errors.Wrap(err, "writeFormattedGoFile()")
		}
	}

	return nil
}

// identityImports renders the import lines an instance's source files share: its own
// packages and the router's, which the first run of a tree cannot yet read off loaded
// packages. The import fixer keeps the ones each file references.
func (r *resourceGenerator) identityImports(inst *identityInstance) string {
	var paths []string
	for _, dir := range []packageDir{inst.resources, inst.methods, inst.handlers, r.router} {
		if p, ok := r.modulePath(dir); ok {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return ""
	}

	return `"` + strings.Join(paths, "\"\n\t\"") + `"`
}

// modulePath is the import path of a directory of the main module, read off go.mod: the
// sources are written before any package is loaded, when the module the run records
// from the resources package (outputPath) is not known yet.
func (r *resourceGenerator) modulePath(dir packageDir) (string, bool) {
	if dir == "" || r.modulePathRoot == "" {
		return "", false
	}
	rel := filepath.ToSlash(filepath.Clean(dir.Dir()))
	if rel == "." {
		return r.modulePathRoot, true
	}
	if strings.HasPrefix(rel, "../") || filepath.IsAbs(dir.Dir()) {
		return "", false
	}

	return r.modulePathRoot + "/" + rel, true
}

// hasRPC reports whether the run holds RPC methods to extract: the application's RPC
// package (WithRPC), or the methods of an auth's generated management.
func (c *client) hasRPC() bool {
	return c.genRPCMethods || len(c.identity) > 0
}

// refuseIdentityNamesTaken refuses an application's own declaration under a name an
// instance's declaration or method holds: the two would be one entry of the permission
// collection and one route. own are the application's names by where each is declared.
func refuseIdentityNamesTaken[T interface{ Name() string }](generated []T, instanceOf func(T) *identityInstance, own map[string]string) error {
	var errs []error
	for _, item := range generated {
		if where, taken := own[item.Name()]; taken {
			errs = append(errs, errors.Newf("%s in %s takes a name of the generated management of Auth(%q): an auth's tables and the methods that change them are declared by GenerateIdentity alone; rename it or remove it", item.Name(), where, instanceOf(item).importPath))
		}
	}

	if err := errors.Join(errs...); err != nil {
		return errors.Wrap(err, "the generated management's names")
	}

	return nil
}

// sortMethods orders methods by name: the order every generated listing of them carries,
// whichever package each was declared in.
func sortMethods(methods []*rpcMethodInfo) {
	slices.SortStableFunc(methods, func(a, b *rpcMethodInfo) int {
		return strings.Compare(a.Name(), b.Name())
	})
}

// identityResources reads each instance's declarations as resources of the run, each
// marked with its instance: the declarations over its tables and, in the same package,
// the views that are a tenant's lists. fsetPkg is the package whose file set places a
// declaration in its file.
func (c *client) identityResources(packageMap map[string]*packages.Package, fsetPkg *packages.Package) ([]*resourceInfo, error) {
	var resources []*resourceInfo
	for _, inst := range c.identity {
		pkg := packageMap[inst.resources.Package()]
		if pkg == nil {
			return nil, errors.Newf("no packages found in %q", inst.resources.Dir())
		}
		structs := parser.ParsePackage(pkg).Structs
		extracted, err := c.structsToResources(structs, c.validateStructNameMatchesFile(fsetPkg, true), validateNoPermTags, validateConditionsTags, validateMaskingTags)
		if err != nil {
			return nil, errors.Wrapf(err, "the generated declarations of Auth(%q)", inst.importPath)
		}
		lists, err := c.structsToVirtualResources(structs, c.validateStructNameMatchesFile(fsetPkg, true), validateNoPermTags, validateConditionsTags, validateMaskingTags)
		if err != nil {
			return nil, errors.Wrapf(err, "the generated lists of Auth(%q)", inst.importPath)
		}
		extracted = append(extracted, lists...)
		for _, res := range extracted {
			res.identity = inst
		}
		resources = append(resources, extracted...)
	}

	return resources, nil
}

// identityComputedResources reads the computed resources each instance declares beside
// its methods as computed resources of the run, each marked with its instance: a
// computed resource's list function takes the methods' client, so it is declared in
// their package. fsetPkg is the package whose file set places a declaration in its file.
func (c *client) identityComputedResources(packageMap map[string]*packages.Package, fsetPkg *packages.Package) ([]*computedResource, error) {
	var resources []*computedResource
	for _, inst := range c.identity {
		pkg := packageMap[inst.methods.Package()]
		if pkg == nil {
			return nil, errors.Newf("no packages found in %q", inst.methods.Dir())
		}
		extracted, err := c.structsToCompResources(parser.ParsePackage(pkg).Structs, c.validateStructNameMatchesFile(fsetPkg, true), validateNoPermTags, validateConditionsTags, validateMaskingTags)
		if err != nil {
			return nil, errors.Wrapf(err, "the generated computed resources of Auth(%q)", inst.importPath)
		}
		for _, res := range extracted {
			res.identity = inst
		}
		resources = append(resources, extracted...)
	}

	return resources, nil
}

// identityMethods reads each instance's methods as methods of the run, each marked with
// its instance.
func (c *client) identityMethods(packageMap map[string]*packages.Package, fsetPkg *packages.Package) ([]*rpcMethodInfo, error) {
	var methods []*rpcMethodInfo
	for _, inst := range c.identity {
		pkg := packageMap[inst.methods.Package()]
		if pkg == nil {
			return nil, errors.Newf("no packages found in %q", inst.methods.Dir())
		}
		extracted, err := c.structsToRPCMethods(parser.ParsePackage(pkg).Structs, c.validateStructNameMatchesFile(fsetPkg, false), validateNoPermTags, validateConditionsTags, validateMaskingTags)
		if err != nil {
			return nil, errors.Wrapf(err, "the generated methods of Auth(%q)", inst.importPath)
		}
		for _, method := range extracted {
			method.identity = inst
		}
		methods = append(methods, extracted...)
	}

	return methods, nil
}

// handlerHome is where the handlers of a resource or a method are written, and what the
// handler templates name there: the application's handler package for its own, the
// instance's handlers package for an instance's.
type handlerHome struct {
	// instance is the instance the home belongs to, nil for the application's.
	instance *identityInstance
	dir      packageDir
	// applicationName and receiverName are the type the handlers are methods of.
	applicationName string
	receiverName    string
	// resources and rpc are the packages the handlers' resources and methods are
	// declared in, and views the package their views are: the application's virtual
	// package, and an instance's declarations package, which holds its views too.
	// computed is the package the computed resources are declared in: the application's
	// computed package, and an instance's methods package, whose client their list
	// functions take.
	resources packageDir
	rpc       packageDir
	views     packageDir
	computed  packageDir
}

// identityHandlersType and identityHandlersReceiver name the type an instance's handlers
// are methods of.
const (
	identityHandlersType     = "Handlers"
	identityHandlersReceiver = "h"
)

// homeOf answers the home of an instance, or the application's for nil.
func (r *resourceGenerator) homeOf(inst *identityInstance) *handlerHome {
	if inst == nil {
		return &handlerHome{dir: r.handler, applicationName: r.applicationName, receiverName: r.receiverName, resources: r.resource, rpc: r.rpc, views: r.virtual, computed: r.computed}
	}

	return &handlerHome{
		instance: inst, dir: inst.handlers, applicationName: identityHandlersType, receiverName: identityHandlersReceiver,
		resources: inst.resources, rpc: inst.methods, views: inst.resources, computed: inst.methods,
	}
}

// liveDerivationsName names the application's live derivations where a handler reaches
// them: the assembly the application's handler package holds, and the field an
// instance's handlers keep the same value in.
const liveDerivationsName = "liveDerivations"

// LiveDerivations is what a handler of the home hands to every publish: the
// application's live derivations. The application's handlers name the assembly in their
// own package. An instance's cannot, since nothing under the tree may import the
// application's handler package, which imports the tree: the application hands them the
// value when it constructs them, and they name the field that keeps it. A nil home is
// the application's, so a template rendered with none names the assembly.
func (h *handlerHome) LiveDerivations() string {
	if h == nil || h.instance == nil {
		return liveDerivationsName
	}

	return h.receiverName + "." + liveDerivationsName
}

// homes lists every home of the run: the application's first, then each instance's.
func (r *resourceGenerator) homes() []*handlerHome {
	homes := make([]*handlerHome, 0, 1+len(r.identity))
	homes = append(homes, r.homeOf(nil))
	for _, inst := range r.identity {
		homes = append(homes, r.homeOf(inst))
	}

	return homes
}

// resourcesOf lists the run's table and view resources that belong to a home.
func (r *resourceGenerator) resourcesOf(home *handlerHome) []*resourceInfo {
	var out []*resourceInfo
	for _, res := range r.resources {
		if res.identity == home.instance {
			out = append(out, res)
		}
	}

	return out
}

// methodsOf lists the run's methods that belong to a home.
func (r *resourceGenerator) methodsOf(home *handlerHome) []*rpcMethodInfo {
	var out []*rpcMethodInfo
	for _, method := range r.rpcMethods {
		if method.identity == home.instance {
			out = append(out, method)
		}
	}

	return out
}

// computedOf lists the run's computed resources that belong to a home.
func (r *resourceGenerator) computedOf(home *handlerHome) []*computedResource {
	var out []*computedResource
	for _, res := range r.computedResources {
		if res.identity == home.instance {
			out = append(out, res)
		}
	}

	return out
}

// computedDirOf is the package a computed resource is declared in: the instance's
// methods package for an instance's, the application's computed package otherwise.
func (c *client) computedDirOf(res *computedResource) packageDir {
	if res.identity != nil {
		return res.identity.methods
	}

	return c.computed
}

// resourcesDirOf is the package a resource is declared in, where its generated set is
// written: the instance's for an instance's resource (its tables and its views alike),
// the virtual package for a view of the application's, the resources package otherwise.
func (c *client) resourcesDirOf(res *resourceInfo) packageDir {
	switch {
	case res.identity != nil:
		return res.identity.resources
	case res.IsVirtual:
		return c.virtual
	default:
		return c.resource
	}
}

// identityDelegate is one handler of an instance as the application's handler package
// delegates to it.
type identityDelegate struct {
	Name string
}

// identityDelegatesData is what the application's delegating file renders from.
type identityDelegatesData struct {
	Source              string
	Package             string
	LocalPackageImports string
	ApplicationName     string
	ReceiverName        string
	Instances           []*identityDelegates
}

// identityDelegates are one instance's handlers.
type identityDelegates struct {
	Auth string
	// AuthPackage is the auth package by name, MethodsPackage and HandlersPackage the
	// instance's own.
	AuthPackage     string
	MethodsPackage  string
	HandlersPackage string
	Handlers        []identityDelegate
}

// generateIdentityDelegates writes the application's delegating methods: one per handler
// of every instance, under the name the route tables call, each handing the application
// to the instance's handlers. With them the application's type carries the generated
// handler interfaces whole, and the assertion that it is what each instance's handlers
// take (Deps) fails the build where it is not.
func (r *resourceGenerator) generateIdentityDelegates() error {
	if len(r.identity) == 0 {
		return nil
	}
	data := &identityDelegatesData{
		Source:              r.identityTree.Dir(),
		Package:             r.handler.Package(),
		LocalPackageImports: r.localPackageImports(),
		ApplicationName:     r.applicationName,
		ReceiverName:        r.receiverName,
	}
	for _, inst := range r.identity {
		home := r.homeOf(inst)
		delegates := &identityDelegates{
			Auth: inst.auth, AuthPackage: assumedPackageName(inst.importPath),
			MethodsPackage: inst.methods.Package(), HandlersPackage: inst.handlers.Package(),
		}
		// The auth package is no package of the run: its path joins the imports the
		// file may reference.
		data.LocalPackageImports += "\n\t\"" + inst.importPath + "\""
		for _, res := range r.resourcesOf(home) {
			for _, handlerType := range resourceEndpoints(res) {
				delegates.Handlers = append(delegates.Handlers, identityDelegate{Name: r.handlerName(res.Name(), handlerType)})
			}
		}
		for _, res := range r.computedOf(home) {
			if res.RoutingDisabled() {
				continue
			}
			if !res.SuppressListHandler {
				delegates.Handlers = append(delegates.Handlers, identityDelegate{Name: r.handlerName(res.Name(), ListHandler)})
			}
			if !res.ReadHandlerDisabled() {
				delegates.Handlers = append(delegates.Handlers, identityDelegate{Name: r.handlerName(res.Name(), ReadHandler)})
			}
		}
		for _, method := range r.methodsOf(home) {
			if !method.SuppressHandler {
				delegates.Handlers = append(delegates.Handlers, identityDelegate{Name: method.Name()})
			}
		}
		data.Instances = append(data.Instances, delegates)
	}
	destination := filepath.Join(r.handler.Dir(), generatedGoFileName(identityDelegatesOutputName))
	if err := r.writeFormattedGoFile(destination, "identityDelegatesTemplate", identityDelegatesTemplate, data); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}

	return nil
}

// identityDelegatesOutputName is the application's delegating file.
const identityDelegatesOutputName = "identity"
