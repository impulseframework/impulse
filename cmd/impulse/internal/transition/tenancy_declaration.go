package transition

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/ettle/strcase"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
)

// The tenant declaration: who may enter a tenant, kept on the tenant record as the
// directory its people sign in through (a child record, one row at most for a tenant,
// and a directory in one tenant's row only) and whether it is the only way in (a
// column on the record). The
// sign-in rule reads it in the sign-in's transaction through the generated reader, and
// every instance's roster keeps a copy that filters a session's tenant list.
const (
	declarationsAccessor = "TenantDeclarations"
	// directoriesAccessor is the Settings field the directory reader travels under.
	directoriesAccessor = "TenantDirectories"
	// makerAccessor is the Settings field the maker of an invited tenant travels under,
	// on the auth whose people a tenant is made through.
	makerAccessor      = "TenantMaker"
	signinImportPath   = "github.com/impulseframework/impulse/session/signin"
	identityImportPath = "github.com/impulseframework/impulse/identity"
	spannerImportName  = "cloudspanner"
)

// DirectoryRecord is the directories record's struct name: the tenant record's with
// Directory, so Tenant's is TenantDirectory.
func (tn Tenancy) DirectoryRecord() string { return tn.Record() + "Directory" }

// DirectoryTable is the directories table: the directories record's plural.
func (tn Tenancy) DirectoryTable() string { return tn.Record() + "Directories" }

// DirectorySegment is the directories record's route under the tenant segment.
func (tn Tenancy) DirectorySegment() string { return strcase.ToKebab(tn.DirectoryTable()) }

// DeclarationReader is the generated reader's constructor in the handlers package:
// New<Record>DeclarationReader.
func (tn Tenancy) DeclarationReader() string { return "New" + tn.Record() + "DeclarationReader" }

// DirectoryReader is the generated directory reader's constructor in the handlers
// package: New<Record>DirectoryReader, the reader of every tenant's declaration of one
// directory with its default seat, which the auth's directory admission decides with.
func (tn Tenancy) DirectoryReader() string { return "New" + tn.Record() + "DirectoryReader" }

// Maker is the generated maker's constructor in the handlers package: New<Record>Maker,
// what makes a tenant from an accepted invitation over the record's own create patch.
// Nobody creates a tenant directly, so the auth whose people a tenant is made through
// is handed it in its Settings and refuses to start without it.
func (tn Tenancy) Maker() string {
	return "New" + tn.Record() + "Maker"
}

// writeDirectoryRecord adds the directories record beside the tenant record.
func (tn Tenancy) writeDirectoryRecord(a *app.App, resourceDir string, ch *Change) error {
	rel := path.Join(resourceDir, strcase.ToSnake(tn.DirectoryTable())+".go")
	if _, err := os.Stat(a.Abs(rel)); err == nil {
		ch.skipf("%s already exists, so the %s resource struct was not written; make sure it is a @resource annotated @permissionScope(domain) and @tenantDirectory, with a bare @domain column, a @directoryMethod column, a @directoryConnection column and a nullable @directoryDefaultSeat column", rel, tn.DirectoryRecord())

		return nil
	}
	pkg := packageNameOf(a, resourceDir)
	src := fmt.Sprintf(`package %[1]s

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

type (
	// %[2]s is a tenant's directory (@tenantDirectory): the organization its people
	// sign in through, as the sign-in method the directory is reached through (google,
	// azure or workos, as the session library spells them) and the connection the
	// method's identities carry, which is what the session's first auth event records:
	// a Google Workspace by its domain or by the name the registration gives it, a
	// Microsoft Entra tenant by its tenant id. A tenant has one directory and a
	// directory belongs to one tenant: the table's two unique indexes hold both, and
	// the generated write paths refuse first, each with a sentence the person can act
	// on.
	//
	// Nobody types a directory in. The row is written from a sign-in through the
	// directory: the tenant's administrator, signed in through their organization's
	// directory, makes it the tenant's on the Sign-in page with the tenant's own
	// method Take%[3]sDirectory, under its Execute grant, after a statement that they
	// are authorized to act on behalf of the organization, under which they type a
	// phrase; the record's own create is refused. The method takes nothing from the
	// request; the server reads the method and the connection from the session, and
	// refuses a session that came by password or with a personal account (a Google
	// account outside every Workspace, a Microsoft personal account), which is no
	// organization's directory. The administrator removes the directory under the
	// Delete grant, which is refused while the tenant is directory only; the roles it
	// gave stand, and the directory is free for another tenant. The change events are
	// the audit.
	//
	// DefaultSeat is the role a person is given in the tenant at their first sign-in
	// through the directory, whether their account is created then or stood before; or
	// NULL, and the tenant admits the directory's people by invitation only (a stranger
	// is refused with no account, and an invitation is the way in). It is given once
	// for the link between the account and the directory: a person whose role an
	// administrator removed is not given it back by signing in. The record shows it and
	// its own update never writes it: the tenant's own methods name it and clear it.
	//
	// AsksSecondFactor says the tenant's administrator has said the directory asks its
	// people for a second factor of its own. A person who signs in through the directory
	// is then not asked for the mailed code, in this tenant and in every tenant that gave
	// them a role; a super administrator, and anyone who signs in with a password, are
	// asked as before. It is the tenant's word and nothing checks it. The record shows it
	// and its own update never writes it: the tenant's own methods say it and take it
	// back.
	//
	// The tenancy column is on the row itself (the bare @domain binding), as the
	// directories record requires: a directory is the tenant's own declaration, stamped
	// from the request's tenant on create, never written by the wire.
	//
	// @resource
	// @permissionScope(domain)
	// @tenantDirectory
	// @order(Method asc, Connection asc)
	%[2]s struct {
		ID impulse.UUID `+"`spanner:\"Id\"`"+`
		// @domain
		%[3]sID impulse.UUID `+"`spanner:\"%[3]sId\"`"+`
		// @directoryMethod
		Method string `+"`spanner:\"Method\"`"+`
		// @directoryConnection
		Connection string `+"`spanner:\"Connection\"`"+`
		// @directoryDefaultSeat
		DefaultSeat *string `+"`spanner:\"DefaultSeat\"`"+`
		// @directorySecondFactor
		AsksSecondFactor bool `+"`spanner:\"AsksSecondFactor\"`"+`
	}
)

// Config enables change tracking: %[2]s mutations write DataChangeEvents rows in
// the same transaction, the audit of every directory set or removed.
func (%[2]s) Config() resource.Config {
	return %[4]s
}
`, pkg, tn.DirectoryRecord(), tn.Record(), trackingConfig(a, resourceDir))
	if err := writeNew(a, rel, src); err != nil {
		return err
	}
	ch.didf("%s: the %s resource struct, a tenant's directory, set from a sign-in and never typed, tenant-scoped under /%s/{%sID}/%s", rel, tn.DirectoryRecord(), tn.Segment(), strcase.ToCamel(tn.Record()), tn.DirectorySegment())

	return nil
}

// trackingConfig is the expression of a resource config with change tracking on: the
// package's own defaultConfig with tracking set when the package declares one (the
// skeletons do), a literal otherwise.
func trackingConfig(a *app.App, resourceDir string) string {
	entries, err := os.ReadDir(a.Abs(resourceDir))
	if err != nil {
		return "resource.Config{TrackChanges: true}"
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(a.Abs(path.Join(resourceDir, e.Name())))
		if err != nil {
			continue
		}
		if strings.Contains(string(src), "func defaultConfig() resource.Config") {
			return "defaultConfig().SetTrackChanges(true)"
		}
	}

	return "resource.Config{TrackChanges: true}"
}

// editAuths sets the sign-in rule on every auth and hands each the generated reader:
// the auth's Settings carry the reader, New refuses a nil one and sets the library's
// tenant policy over it on the session, and the data level's settings literal passes
// the reader in. The auth a tenant is made through (makesTenants) is handed the
// generated maker too, over the roster the data level built ahead of it (early). Where
// no tenant has a directory (NoDirectories) the reader is the one that answers nothing,
// and no auth is handed a directory reader or sets a directory admission.
func (tn Tenancy) editAuths(a *app.App, site *app.Site, g *app.Generator, early *earlyRoster, ch *Change) error {
	if len(a.AuthPackages) == 0 {
		ch.skipf("no auth package constructs a session.Auth, so no sign-in rule was set; where one is, set signin.TenantPolicy(resource.SignInRoles(<engine>.UserManager()), <handlers>.%[1]s()) on the session with SetSignInPolicy before the first request, so a sign-in is admitted by what the person's tenants declare, and set signin.PasswordsDisabledByTenants(<engine>.UserManager(), resource.OutsideSignIn(<the Spanner client>, <handlers>.%[1]s())) on it with SetPasswordDisabledCheck, which the account page's Password line follows", tn.DeclarationReader())

		return nil
	}
	for i := range a.AuthPackages {
		pkg := &a.AuthPackages[i]
		var auth *app.Auth
		for j := range a.Auths {
			if path.Dir(a.Auths[j].File) == pkg.Dir {
				auth = &a.Auths[j]

				break
			}
		}
		if auth == nil {
			ch.skipf("%s: the %s auth constructs no session.NewAuth the scan reads, so no sign-in rule was set on it; set signin.TenantPolicy over the generated reader on its session with SetSignInPolicy, and signin.PasswordsDisabledByTenants over the same reader (through resource.OutsideSignIn) with SetPasswordDisabledCheck, which the account page's Password line follows", pkg.Dir, pkg.Name)

			continue
		}
		// The record is written with no @outlet, so the default outlet serves it: the auth
		// bound there is the one whose people create a tenant of their own, and the one a
		// tenant is made through when staff invite one person.
		serves := servesRecord(site, pkg)
		handlers := packageNameOf(a, g.HandlersDir())
		if err := tn.editAuth(a, auth, pkg, serves, handlers+"."+tn.DeclarationReader(), handlers+"."+tn.DirectoryReader(), handlers+"."+tn.Maker(), ch); err != nil {
			return err
		}
		if err := tn.editSettings(a, g, auth, pkg, serves && setsAdmission(a, auth), early, ch); err != nil {
			return err
		}
	}

	return nil
}

// directoryAuthority reports an auth with a registration whose directory writes the
// roles: the directory is the authority for everyone the auth holds, so the auth sets
// no directory admission (its strangers are provisioned with the roles the directory
// names).
func directoryAuthority(auth *app.Auth) bool {
	for i := range auth.Methods {
		if auth.Methods[i].Authority == app.AuthorityDirectory {
			return true
		}
	}

	return false
}

// identityClientRE matches where an auth's New builds the auth's identity client, as the
// rendered auth packages name it: the client of the auth's generated management, whose
// DirectorySeated hook the directory admission gives a default role through.
var identityClientRE = regexp.MustCompile(`(?m)^\tidentityClient, err := \w+identity\.NewClient\(`)

// identityClientHeadRE matches that call through its first two arguments, the engine and
// the session's API, as the rendered auth packages write them: the maker of an invited
// tenant is the client's third.
var identityClientHeadRE = regexp.MustCompile(`(?m)^(\tidentityClient, err := \w+identity\.NewClient\(\w+, \w+\.API\(\))`)

// identityClientCallRE matches the whole call that builds an auth's identity client, as
// the rendered auth packages write it on one line, up to its closing parenthesis: the
// identity package's options are the call's last arguments, so one more goes there.
var identityClientCallRE = regexp.MustCompile(`(?m)^(\tidentityClient, err := \w+identity\.NewClient\(.*)\)$`)

// oneTenantOption is the identity package's option that says a person belongs to one
// tenant, as an auth's New passes it.
const oneTenantOption = "identity.WithOneTenantPerPerson()"

// declareOneTenant says a person belongs to one tenant on every auth whose roles the
// application assigns: the identity client its New builds gains the identity package's
// option, which every way a person comes to hold a role in a tenant is then held to (a
// role given by hand, an invitation sent or accepted, a directory's default role). An
// auth whose directory writes the roles gives none of its own, so it is left as it is;
// an auth whose New builds the client in another form is recorded as the agent's.
func (tn Tenancy) declareOneTenant(a *app.App, ch *Change) error {
	const rule = "a role in a second tenant is refused however it would be given, and the application's own people, who hold a role for the whole application or in every tenant, are outside the rule"
	held := 0
	for i := range a.AuthPackages {
		pkg := &a.AuthPackages[i]
		var auth *app.Auth
		for j := range a.Auths {
			if path.Dir(a.Auths[j].File) == pkg.Dir {
				auth = &a.Auths[j]

				break
			}
		}
		if auth == nil || directoryAuthority(auth) {
			continue
		}
		held++
		rel := auth.File
		src, mode, err := readFile(a, rel)
		if err != nil {
			return err
		}
		call := identityClientCallRE.FindSubmatchIndex(src)
		if call == nil {
			ch.skipf("%s: New builds no identity client in the form the tool reads (identityClient, err := %sidentity.NewClient(...) on one line), so one tenant per person was not declared on the %s auth; pass %s among the options of its identity client: %s", rel, pkg.Name, pkg.Name, oneTenantOption, rule)

			continue
		}
		edited := slices.Concat(src[:call[3]], []byte(", "+oneTenantOption), src[call[3]:])
		if edited, err = app.AddImport(rel, edited, identityImportPath); err != nil {
			return err
		}
		if err := os.WriteFile(a.Abs(rel), edited, mode); err != nil {
			return errors.Wrap(err, "os.WriteFile()")
		}
		ch.didf("%s: the %s auth's identity client is built with %s, so a person belongs to one tenant: %s", rel, pkg.Name, oneTenantOption, rule)
	}
	if held == 0 {
		ch.skipf("no auth whose roles the application assigns constructs a session.Auth the scan reads, so one tenant per person was declared nowhere; where an auth builds its identity client, pass %s among its options: %s", oneTenantOption, rule)
	}

	return nil
}

// servesRecord reports the auth the default outlet binds: the tenant record is written
// with no @outlet, so that outlet serves it.
func servesRecord(site *app.Site, pkg *app.AuthPackage) bool {
	return site.Default.Auth != nil && site.Default.Auth.ImportPath == pkg.Path
}

// makerAuth is the auth a tenant is made through, nil when the application has none: the
// one whose session outlets serve the tenant record, where the application assigns its
// roles and generates its management. Staff of that auth invite one person with a
// tenant's details, and the person's first sign-in makes the tenant, so its generated
// identity client is constructed over the maker.
func (tn Tenancy) makerAuth(a *app.App, site *app.Site) *app.AuthPackage {
	for i := range a.AuthPackages {
		pkg := &a.AuthPackages[i]
		if !servesRecord(site, pkg) {
			continue
		}
		for j := range a.Auths {
			if path.Dir(a.Auths[j].File) == pkg.Dir && setsAdmission(a, &a.Auths[j]) {
				return pkg
			}
		}
	}

	return nil
}

// setsAdmission reports whether the directory admission is set on the auth: its roles
// are the application's (no registration's directory writes them) and its New builds
// the identity client the admission's hook comes from. An auth whose file the tool
// cannot read sets none.
func setsAdmission(a *app.App, auth *app.Auth) bool {
	if directoryAuthority(auth) {
		return false
	}
	src, _, err := readFile(a, auth.File)

	return err == nil && identityClientRE.Match(src)
}

// editAuth gives the auth's Settings the readers (reader and directoryReader are their
// constructors as the data level qualifies them) and its New the sign-in rule and, on
// an auth whose roles the application assigns, the directory admission, worded as the
// rendered auth packages word them. serves reports the auth whose session outlets
// serve the tenant record: where the application assigns its roles and generates its
// management, its people create a tenant of their own through the generated route, so
// its rule admits a person who holds no tenant yet, and a tenant is made through it when
// staff invite one person: its Settings carry the generated maker (maker is its
// constructor as the data level qualifies it), New refuses a nil one and hands it to the
// identity client.
func (tn Tenancy) editAuth(a *app.App, auth *app.Auth, pkg *app.AuthPackage, serves bool, reader, directoryReader, maker string, ch *Change) error {
	rel := auth.File
	src, mode, err := readFile(a, rel)
	if err != nil {
		return err
	}
	spanner, err := app.ImportName(rel, src, spannerImportPath)
	if err != nil {
		return err
	}
	edited := src
	if spanner == "" {
		spanner = spannerImportName
		if edited, err = app.AddNamedImport(rel, edited, spannerImportName, spannerImportPath); err != nil {
			return err
		}
	}
	field := fmt.Sprintf("// %[1]s reads what a tenant declares about who may enter it, in the\n// sign-in's transaction, which the sign-in rule decides with: the generated reader\n// over the %[2]s record and its directories (%[3]s).\n// Required; New refuses nil.\n%[1]s signin.DeclarationReader[*%[4]s.ReadWriteTransaction]", declarationsAccessor, tn.Record(), reader, spanner)
	if tn.NoDirectories {
		field = fmt.Sprintf("// %[1]s reads what a tenant declares about who may enter it, in the\n// sign-in's transaction, which the sign-in rule decides with: the generated reader\n// over the %[2]s record (%[3]s), which declares no\n// directories, so the reader answers nothing for every tenant. Required; New refuses\n// nil.\n%[1]s signin.DeclarationReader[*%[4]s.ReadWriteTransaction]", declarationsAccessor, tn.Record(), reader, spanner)
	}
	// assigns reports an auth whose roles the application gives through the identity
	// client its New builds: the auth a tenant can be made through, and, where a tenant
	// can have a directory, the auth that sets the directory admission.
	assigns := setsAdmission(a, auth)
	admission := assigns && !tn.NoDirectories
	if !assigns && !directoryAuthority(auth) && !tn.NoDirectories {
		ch.skipf("%s: New builds no identity client (identityClient, err := %sidentity.NewClient(...), the client of the auth's generated management), so the directory admission was not set and a directory's default role gives nobody a role; generate the auth's management (GenerateIdentity on the generator program), build the client in New, and set %s.SetDirectoryAdmission(settings.%s, identityClient.DirectorySeated()) beside the sign-in rule", rel, pkg.Name, sessionVar(auth), directoriesAccessor)
	}
	edited, err = app.AddStructField(rel, edited, "Settings", field)
	if errors.Is(err, app.ErrNoAnchor) {
		ch.skipf("%s: no Settings struct to carry the reader; hand %s.%s() to the %s auth where it is built and set signin.TenantPolicy over it on its session, and signin.PasswordsDisabledByTenants over the same reader (through resource.OutsideSignIn) with SetPasswordDisabledCheck, which the account page's Password line follows", rel, pkg.Name, tn.DeclarationReader(), pkg.Name)

		return nil
	} else if err != nil {
		return err
	}
	if admission {
		directoryField := fmt.Sprintf("// %[1]s reads every tenant's declaration of one directory with the\n// default seat its row carries, in the sign-in's transaction, which the directory\n// admission decides a stranger's first sign-in with: the generated reader\n// (%[2]s). Required; New refuses nil.\n%[1]s signin.DirectoryReader[*%[3]s.ReadWriteTransaction]", directoriesAccessor, directoryReader, spanner)
		if edited, err = app.AddStructField(rel, edited, "Settings", directoryField); err != nil {
			return err
		}
	}
	makes := assigns && serves
	if makes {
		if edited, makes, err = tn.carryMaker(rel, edited, pkg.Name, maker, ch); err != nil {
			return err
		}
	}
	for _, imp := range []string{signinImportPath, resourceImportPath} {
		if edited, err = app.AddImport(rel, edited, imp); err != nil {
			return err
		}
	}
	edited, checked, err := tn.refuseNil(rel, edited, pkg.Name, reader, directoryReader, maker, admission, makes)
	if err != nil {
		return err
	}
	edited, set := tn.setRule(rel, edited, sessionVar(auth), admission, assigns && serves, ch)
	if err := os.WriteFile(a.Abs(rel), edited, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: Settings carries %s", rel, authCarries(checked, set, admission, makes))

	return nil
}

// setRule writes the auth's one sign-in rule, the tenant policy, before New's return and
// reports whether it was set. An Auth has one sign-in rule, and a second SetSignInPolicy
// call panics when the auth opens. An auth package rendered for an application without
// tenants sets none, so the tenant policy is written as the Auth's rule, and the comment
// saying it sets none (authpkg.NoSignInRule) is taken out. Where New set a rule already,
// both calls stand and the change says they must become one; where New does not end as
// the skeleton's does, no rule is written and the change says which to set. admission
// says the directory admission is set beside the rule, and admitWithoutTenant that the
// auth's people create a tenant of their own.
func (tn Tenancy) setRule(rel string, edited []byte, session string, admission, admitWithoutTenant bool, ch *Change) (out []byte, set bool) {
	loc := returnAuthRE.FindIndex(edited)
	if loc == nil {
		if bytes.Contains(edited, []byte(session+".SetSignInPolicy(")) {
			// A rule stands, and the tool wrote none beside it: a second call would panic.
			ch.skipf("%s: New does not end in `return &Auth{access: accessClient, handlers: handlers, identity: identityClient}, nil` as the skeleton's does, so the sign-in rule was not changed, and New sets one already; an Auth has one rule and a second SetSignInPolicy call panics when the auth opens, so make the call New makes now the tenant policy with the rule that stood chained after it: %s.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.%s).Then(<the rule New sets now>)); and set the check the account page's Password line follows beside it: %s.SetPasswordDisabledCheck(signin.PasswordsDisabledByTenants(accessClient.UserManager(), resource.OutsideSignIn(db, settings.%s)))", rel, session, declarationsAccessor, session, declarationsAccessor)

			return edited, false
		}
		ch.skipf("%s: New does not end in `return &Auth{access: accessClient, handlers: handlers, identity: identityClient}, nil` as the skeleton's does, so the sign-in rule was not set; call %s.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.%s)) before the first request, and %s.SetPasswordDisabledCheck(signin.PasswordsDisabledByTenants(accessClient.UserManager(), resource.OutsideSignIn(db, settings.%s))) beside it, which the account page's Password line follows", rel, session, declarationsAccessor, session, declarationsAccessor)

		return edited, false
	}
	if bytes.Contains(edited, []byte(session+".SetSignInPolicy(")) {
		ch.skipf("%s: New set a sign-in rule before this change, and now sets the tenant policy beside it; an Auth has one rule and a second SetSignInPolicy call panics when the auth opens, so make the two one call: the tenant policy first, and the rule that stood chained after it with .Then", rel)
	}
	policy := signInRule(session, tn.oneLower(), admission, admitWithoutTenant, tn.NoDirectories)
	edited = append(edited[:loc[0]:loc[0]], append([]byte(policy), edited[loc[0]:]...)...)

	return bytes.Replace(edited, []byte("\n"+authpkg.NoSignInRule+"\n"), nil, 1), true
}

// carryMaker gives the auth a tenant is made through what makes one. The generated
// client of that auth takes the maker as its third argument, so the call is rewritten
// with the Settings field it comes from, and the field and the identity import are
// added. It reports false, with the obligation, for an auth whose New builds the client
// in another form, which is left for the agent.
func (tn Tenancy) carryMaker(rel string, edited []byte, pkgName, maker string, ch *Change) (out []byte, made bool, err error) {
	head := identityClientHeadRE.FindSubmatchIndex(edited)
	if head == nil {
		ch.skipf("%s: New builds its identity client in a form the tool does not read (identityClient, err := %sidentity.NewClient(accessClient, authSession.API(), ...)), so the auth is not handed what makes a %s from an accepted invitation; give Settings a %s identity.TenantMaker field, refuse a nil one in New, and pass it as the client's third argument (%s is its constructor)", rel, pkgName, tn.oneLower(), makerAccessor, maker)

		return edited, false, nil
	}
	edited = append(edited[:head[3]:head[3]], append([]byte(", settings."+makerAccessor), edited[head[3]:]...)...)
	makerField := fmt.Sprintf("// %[1]s makes a %[2]s from an accepted invitation: the generated maker over\n// the record's own create patch (%[3]s). Nobody creates a %[4]s\n// directly: staff invite one person with its details, which the auth's identity\n// client checks with the maker when the invitation is sent, and that person's first\n// sign-in makes the %[4]s with it. Required; New refuses nil.\n%[1]s identity.TenantMaker", makerAccessor, tn.Record(), maker, tn.oneLower())
	if edited, err = app.AddStructField(rel, edited, "Settings", makerField); err != nil {
		return nil, false, err
	}
	if edited, err = app.AddImport(rel, edited, identityImportPath); err != nil {
		return nil, false, err
	}

	return edited, true, nil
}

// refuseNil has New refuse a nil reader, and a nil maker on the auth a tenant is made
// through, ahead of its store, where the file imports the errors package the refusals
// are written with. It answers the words the change's did line says it in, empty where
// New was left as it is.
func (tn Tenancy) refuseNil(rel string, src []byte, pkgName, reader, directoryReader, maker string, admission, makes bool) (edited []byte, checked string, err error) {
	if ok, _ := app.HasImport(rel, src, errorsImportPath); !ok {
		return src, "", nil
	}
	checks := nilReaderChecks(pkgName, reader, directoryReader, admission)
	if makes {
		checks += nilMakerCheck(pkgName, tn.Record(), maker)
	}
	edited, err = app.AddStatementsBeforeCall(rel, src, "", "New", "spannerstore.New", checks)
	switch {
	case errors.Is(err, app.ErrNoAnchor):
		return src, "", nil
	case err != nil:
		return nil, "", err
	}

	return edited, ", New refuses a nil one", nil
}

// nilMakerCheck is the refusal New makes of a nil maker in its Settings, worded as the
// rendered auth packages word it.
func nilMakerCheck(pkgName, record, maker string) string {
	return fmt.Sprintf("\n\tif settings.%[1]s == nil {\n\t\treturn nil, errors.New(\"%[2]s.Settings.%[1]s is nil: a tenant is made by an invitation's first sign-in, through the generated maker over the %[3]s record (%[4]s), which the data level hands in\")\n\t}", makerAccessor, pkgName, record, maker)
}

// nilReaderChecks is the refusal New makes of a nil reader in its Settings: the
// declaration reader, and the directory reader where the admission is set.
func nilReaderChecks(pkgName, reader, directoryReader string, admission bool) string {
	check := fmt.Sprintf("\tif settings.%[1]s == nil {\n\t\treturn nil, errors.New(\"%[2]s.Settings.%[1]s is nil: the sign-in rule reads what each tenant declares about who may enter it through the generated reader (%[3]s), which the data level hands in\")\n\t}", declarationsAccessor, pkgName, reader)
	if admission {
		check += fmt.Sprintf("\n\tif settings.%[1]s == nil {\n\t\treturn nil, errors.New(\"%[2]s.Settings.%[1]s is nil: the directory admission reads what the tenants declare about one directory, its default seat included, through the generated reader (%[3]s), which the data level hands in\")\n\t}", directoriesAccessor, pkgName, directoryReader)
	}

	return check
}

// authCarries words what the auth's Settings carries after the edit, for the change's
// did line: the readers, the maker of an invited tenant on the auth a tenant is made
// through, whether New refuses a nil one, and the rules New sets.
func authCarries(checked string, ruleSet, admission, makes bool) string {
	set := ""
	if ruleSet {
		set = ", and New sets the sign-in rule on the session (signin.TenantPolicy over the reader, the memberships read through resource.SignInRoles) with the password-disabled check beside it (signin.PasswordsDisabledByTenants over the same reader, through resource.OutsideSignIn)"
		if admission {
			set += " and the directory admission (session.Auth.SetDirectoryAdmission over the directory reader, the default role given by the identity client's DirectorySeated in the transaction that creates the person)"
		}
	}
	carried := []string{"the declaration reader (" + declarationsAccessor + ")"}
	if admission {
		carried = append(carried, "the directory reader ("+directoriesAccessor+")")
	}
	if makes {
		carried = append(carried, "what makes a tenant from an accepted invitation ("+makerAccessor+", which New hands the identity client)")
	}

	return andList(carried) + checked + set
}

// andList joins the parts of a sentence's list: "a", "a and b", "a, b and c".
func andList(parts []string) string {
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}

	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// sessionVar is the variable the auth's New holds its session manager in: the one the
// scan read, or the rendered auth packages' name.
func sessionVar(auth *app.Auth) string {
	if auth.Var != "" {
		return auth.Var
	}

	return "authSession"
}

// signInRule is the source set before New's return: the tenant policy on the session,
// the check that asks the same rule outside a sign-in whose password is disabled, and,
// on an auth whose roles the application assigns where a tenant can have a directory
// (admission), the directory admission. record is the tenant record's word in
// a sentence. admitWithoutTenant marks the auth whose people create a tenant of their
// own, which admits a person who holds none: a person who signed up through a method an
// administrator opened. noDirectories words the rule for an application whose tenants
// have no directories, where no tenant declares anything.
func signInRule(session, record string, admission, admitWithoutTenant, noDirectories bool) string {
	without, option := fmt.Sprintf("\t// A person who holds no tenant is refused (no_tenant): nobody signs up to this auth\n\t// and creates a %s of their own, since another auth's outlets serve the\n\t// record or a directory gives this one's roles.\n", record), ""
	if admitWithoutTenant {
		without = fmt.Sprintf("\t// A person who holds no tenant is admitted too (signin.AdmitWithoutTenant): a\n\t// person who signs up through a sign-in method an administrator opened signs in\n\t// first and creates a %s of their own, and keeps their account when the method\n\t// is made invite only again. Nobody gets an account that way while every method is\n\t// invite only, and the create-your-own route refuses while none is open.\n", record)
		option = ", signin.AdmitWithoutTenant()"
	}
	admits := "\t// The sign-in rule: the library's tenant policy. A person's tenants are where they\n\t// hold a role (the engine's memberships), and what each tenant admits is its\n\t// declaration, read in the sign-in's transaction. A declaration restricts its own\n\t// tenant alone: a tenant that is not directory only admits everyone it seats,\n\t// however they signed in, and a directory-only tenant admits a sign-in through its\n\t// own directory (its method and the connection its identities carry) and nothing\n\t// else, so a person is refused only when every tenant that seats them is closed to\n\t// the way they signed in; a person who holds a role outside any one tenant (a\n\t// global role, or one held in every tenant) is admitted on the library's checks,\n\t// and a super administrator before any of it. An administrator of a directory-only\n\t// tenant, in that tenant itself, who signs in with a password is held for the code\n\t// sent to their email and then enters their own tenant: the way back in when the\n\t// directory has a problem.\n"
	if admission {
		admits += "\t// One part of a declaration reaches past its own tenant: an account that signs in\n\t// through a directory-only tenant's directory signs in through that directory\n\t// alone, whichever tenants seat the person. Any other sign-in of the account is\n\t// refused, a password too, with the two exceptions above: that tenant's own\n\t// administrator's password, held for the mailed code, and a super administrator.\n"
	}
	if noDirectories {
		admits = "\t// The sign-in rule: the library's tenant policy. A person's tenants are where they\n\t// hold a role (the engine's memberships). No tenant has a directory in this\n\t// application (the tenant record declares none), so the reader the settings carry\n\t// answers nothing for every tenant and a tenant admits everyone it seats, however\n\t// they signed in: a sign-in method through an organization's directory is a way\n\t// for an invited person to sign in and nothing more. A person who holds a role\n\t// outside any one tenant (a global role, or one held in every tenant) is admitted\n\t// on the library's checks, and a super administrator before any of it.\n"
	}
	policy := fmt.Sprintf("%[5]s%[3]s\t// Which tenants the session then reaches is decided again, tenant by tenant, where\n\t// its tenant list is computed (UserPermissions). The memberships are read outside\n\t// the sign-in's transaction (resource.SignInRoles): the Spanner client refuses the\n\t// engine's read under the transaction's context as a nested transaction.\n\t%[1]s.SetSignInPolicy(signin.TenantPolicy(resource.SignInRoles(accessClient.UserManager()), settings.%[2]s%[4]s))\n", session, declarationsAccessor, without, option, admits)
	policy += fmt.Sprintf("\n\t// The same rule, asked outside a sign-in, says whose password is disabled: a person\n\t// the rule would refuse a sign-in with a password as directory only. Their account\n\t// page draws no Password line and they are refused a first password\n\t// (SetPasswordDisabledCheck). The declarations are read with the sign-in's reader,\n\t// each in a transaction of its own (resource.OutsideSignIn).\n\t%s.SetPasswordDisabledCheck(signin.PasswordsDisabledByTenants(accessClient.UserManager(), resource.OutsideSignIn(db, settings.%s)))\n", session, declarationsAccessor)
	if admission {
		policy += fmt.Sprintf("\n\t// The directory admission: what a person gets at their first sign-in through a\n\t// tenant's directory, before the registration's account rule is reached. The row of\n\t// the tenant whose directory the identity came through decides. A row with a default\n\t// seat creates a stranger and gives them that role in the tenant in one transaction\n\t// (the identity client's DirectorySeated, which the session library runs inside the\n\t// transaction that links the account to the directory), and the sign-in rule then\n\t// finds the membership; a row with none refuses a stranger (invitation_only) and no\n\t// account is created, unless they followed an invitation link, which is the way in.\n\t// A person whose account stood before is given the default seat too, at the sign-in\n\t// that links the account to the directory, where they hold no role in the tenant; a\n\t// role an administrator removes afterwards is not given back by signing in. A\n\t// directory no tenant has falls to the registration's rule. The auth's roles are the\n\t// application's, so the admission is set here; an auth whose directory writes the\n\t// roles sets none.\n\t%s.SetDirectoryAdmission(settings.%s, identityClient.DirectorySeated())\n", session, directoriesAccessor)
	}

	return policy
}

// editSettings passes the generated readers in the data level's settings literal of
// the auth: the declaration reader, and, where a tenant can have a directory, the
// directory reader on an auth whose roles the application assigns. The auth a tenant is made through (makes) is passed the
// generated maker too, over the roster the level built ahead of it (early); where the
// level builds the roster after its auths, or builds the auth's settings in a shape the
// tool does not read, handing the maker in is recorded as the agent's.
func (tn Tenancy) editSettings(a *app.App, g *app.Generator, auth *app.Auth, pkg *app.AuthPackage, makes bool, early *earlyRoster, ch *Change) error {
	handlersPath := a.GoMod.Module.Mod.Path + "/" + g.HandlersDir()
	handlersPkg := packageNameOf(a, g.HandlersDir())
	reader := handlersPkg + "." + tn.DeclarationReader() + "()"
	directoryReader := handlersPkg + "." + tn.DirectoryReader() + "()"
	maker := handlersPkg + "." + tn.Maker()
	// unmade records the maker as the agent's wherever this edit does not hand it in.
	unmade := func() {
		if makes {
			ch.skipf("the %[1]s auth is not handed what makes a %[2]s from an accepted invitation, and refuses to start without it (%[1]s.Settings.%[3]s is nil): a %[2]s is made through this auth, by the first sign-in of a person its staff invited, so build the roster before the auth where the data level is opened (%[4]s.New%[5]sRoster(<the client>, resource.WithTenantSignals(<the live service>)), which %[6]s then only starts) and pass %[7]s(<the roster>, <the live service>) in the auth's Settings", pkg.Name, tn.oneLower(), makerAccessor, handlersPkg, tn.Record(), rosterStart, maker)
		}
	}
	rel, src, mode, err := findDeclaringFile(a, "DataConfiguration")
	if err != nil {
		return err
	}
	if rel == "" {
		ch.skipf("no file declares a DataConfiguration struct, so the %s auth is not handed the declaration reader; pass %s in its Settings (%s) where it is built", pkg.Name, reader, declarationsAccessor)
		unmade()

		return nil
	}
	structName := ""
	if env := envVarRE.FindSubmatch(src); env != nil {
		structName = string(env[1])
	}
	place := settingsPlace(rel, src, structName, pkg.Name)
	edited, err := place.add(src, declarationsAccessor+": "+reader)
	switch {
	case errors.Is(err, app.ErrNoAnchor):
		ch.skipf("%s: no %sSettings method and no %s.Settings{...} literal in NewDataConfiguration, so the %s auth is not handed the declaration reader; pass %s in its Settings (%s) where it is built", rel, pkg.Name, pkg.Name, pkg.Name, reader, declarationsAccessor)
		unmade()

		return nil
	case err != nil:
		return err
	}
	passed := []string{"the declaration reader (" + reader + ")"}
	if setsAdmission(a, auth) && !tn.NoDirectories {
		if edited, err = place.add(edited, directoriesAccessor+": "+directoryReader); err != nil {
			return err
		}
		passed = append(passed, "the directory reader ("+directoryReader+")")
	}
	if makes {
		withMaker, made, err := passMaker(rel, edited, place, pkg.Name, maker, early)
		if err != nil {
			return err
		}
		if made == "" {
			unmade()
		} else {
			edited = withMaker
			passed = append(passed, "what makes a tenant from an accepted invitation ("+made+", over the roster built ahead of the auth)")
		}
	}
	if edited, err = app.AddImport(rel, edited, handlersPath); err != nil {
		return err
	}
	if err := os.WriteFile(a.Abs(rel), edited, mode); err != nil {
		return errors.Wrap(err, "os.WriteFile()")
	}
	ch.didf("%s: %s passes %s to the %s auth", rel, place.name, andList(passed), pkg.Name)

	return nil
}

// passMaker hands the auth a tenant is made through the generated maker in its settings
// on the data level, constructed over the roster the level built ahead of the auth and
// its live service (early). Where the settings literal stands inline in
// NewDataConfiguration the element is the maker's construction; where a settings
// function builds it, the function gains a parameter the auth's construction passes the
// maker through, as the skeletons' data level has it. made is the construction as
// written, empty where nothing was changed: the level builds the roster after its auths,
// or the settings function or the auth's construction is not in the skeleton's form.
func passMaker(rel string, src []byte, place literalPlace, auth, maker string, early *earlyRoster) (edited []byte, made string, err error) {
	if early == nil {
		return src, "", nil
	}
	made = maker + "(" + early.roster + ", " + early.live + ")"
	if place.method == "" {
		if edited, err = place.add(src, makerAccessor+": "+made); err != nil {
			return nil, "", err
		}

		return edited, made, nil
	}
	declared := regexp.MustCompile(`(?m)^(func \(\w+ \*?\w+\) ` + regexp.QuoteMeta(place.method) + `\([^()]*)(\) \*` + regexp.QuoteMeta(auth) + `\.Settings \{)$`)
	built := regexp.MustCompile(`(?m)^(\t\w+, err := ` + regexp.QuoteMeta(auth) + `\.New\(.*\b\w+\.` + regexp.QuoteMeta(place.method) + `\(.*)(\)\))$`)
	if !declared.Match(src) || !built.Match(src) {
		return src, "", nil
	}
	edited = declared.ReplaceAll(src, []byte("${1}, tenants identity.TenantMaker${2}"))
	edited = built.ReplaceAll(edited, []byte("${1}, "+made+"${2}"))
	if edited, err = place.add(edited, makerAccessor+": tenants"); err != nil {
		return nil, "", err
	}
	if edited, err = app.AddImport(rel, edited, identityImportPath); err != nil {
		return nil, "", err
	}

	return edited, made, nil
}
