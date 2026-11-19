package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/spf13/cobra"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/term"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/ci"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
	"github.com/impulseframework/impulse/cmd/impulse/internal/framework"
	"github.com/impulseframework/impulse/cmd/impulse/internal/handoff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/ledger"
	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
	"github.com/impulseframework/impulse/cmd/impulse/internal/shape"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
	transition_ "github.com/impulseframework/impulse/cmd/impulse/internal/transition"
)

// newFlags are impulse new's own flags; the composed options ride composedOptions and the
// handoff flags transitionFlags.
type newFlags struct {
	modulePath string
	name       string
	authName   string
	devRoot    string
	skipGit    bool
	opts       composedOptions
	transition transitionFlags
}

func (nf *newFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&nf.modulePath, "module", "", "module path of the application (required)")
	cmd.Flags().StringVar(&nf.name, "name", "", "the application's name: "+nameUse+" (default: the module path's last segment)")
	cmd.Flags().StringVar(&nf.authName, "auth", "", "the first auth's name: "+names.Guidance+" (asked when not given; no default)")
	cmd.Flags().StringVar(&nf.devRoot, "dev-root", "", "a checkout of the framework to build against instead of the pin (go.mod replaces the module with it; that line is not for committing)")
	cmd.Flags().BoolVar(&nf.skipGit, "skip-git", false, "do not initialize a git repository and make the first commit")
	cmd.Flags().BoolVar(&nf.opts.tenancy, "tenancy", false, "compose the tenancy option: tenant-scoped resources under a tenant segment, roles per tenant")
	cmd.Flags().StringVar(&nf.opts.tenantTable, "tenant-table", "Tenants", "the tenant-record table when --tenancy is given, PascalCase and plural")
	cmd.Flags().BoolVar(&nf.opts.tenants.oneTenant, shape.OneTenantFlag, false, "with --tenancy, "+oneTenantUsage)
	cmd.Flags().Bool(shape.DirectoriesFlag, true, "with --tenancy, "+directoriesUsage)
	bindShape(cmd, &nf.opts.shapeKey, shape.OfNew(), "the first auth's sign-in methods where the shape has no password sign-in, and the tenants' choices, composing --tenancy, where it has tenants")
	cmd.Flags().StringVar(&nf.opts.method, methodFlag, "", "with --shape "+directoryRolesKey()+", the one directory that gives the roles: google or azure, with :<registration> to name the registration (asked when not given)")
	cmd.Flags().StringVar(&nf.opts.organization, organizationFlag, "", "with --shape "+directoryRolesKey()+", the directory's one organization: a Google Workspace by its domain (fleet.example), a Microsoft Entra tenant by its tenant id (asked when not given)")
	cmd.Flags().StringArrayVar(&nf.opts.outlets, "outlet", nil, "compose a session outlet, <name>=<prefix> (repeatable), such as portal=portal/api")
	cmd.Flags().StringArrayVar(&nf.opts.apiOutlets, "api-outlet", nil, "compose a machine outlet, <name>=<prefix> (repeatable), such as machines=machines: a surface with no browser whose requests carry the tokens the console's auth issues")
	cmd.Flags().StringArrayVar(&nf.opts.sites, "site", nil, "compose the sites layout: two or more site names (repeatable), the first being what the base site becomes under apps/")
	cmd.Flags().BoolVar(&nf.opts.files, "files", false, "compose the file store: APP_FILE_STORE, the development directory, the cleanup job and its scheduled method")
	nf.transition.bindAgent(cmd)
	_ = cmd.MarkFlagRequired("module")
}

// newDeps are what impulse new reads besides its flags: the running impulse's build, whose
// version the new application's go.mod pins as its impulse tool, and the runner of the go
// command that resolves that pin through the module proxy. newNew passes the real ones;
// tests pass fakes.
type newDeps struct {
	build func() check.Build
	run   check.Execer
}

// newLong is the help text of impulse new, which names the shapes its --shape takes.
func newLong() string {
	return `new renders the base skeleton (flat layout, one auth, nothing else on) into a new or empty
directory under the module path you name, and names the application's first auth: the
population that signs in, as a lowercase plural (staff, members, partners, devices). The
auth is a package, pkg/auth/<name>, and its name is also the prefix of its tables, its
cookie, and the stem of its role file, so it is asked for when --auth is not given, and
there is no default: a default word would land in every application whose author skipped
the question.

The application is named after the module path's last segment (github.com/acme/beacon names
beacon) unless --name says otherwise; the name is the web package (<name>-web), APP_SERVICE_NAME,
and the development Spanner project, instance, and database in .envrc.template.

The rendered tree is committed as the application's first commit (--skip-git leaves it
uncommitted), so impulse add can start from a clean tree.

Options are added afterwards, each as one reviewable change: impulse add tenancy, impulse
add outlet <name>, impulse add auth <name>, impulse add site <name>. Or they are composed
into the creation: --tenancy, --outlet <name>=<prefix> (repeatable; --api-outlet for a
machine surface), and --site <name> (two or more, the first being what the base site
becomes under apps/) apply the same transitions to the fresh tree in order, tenancy first
and the sites last, and end in one check and one handoff brief carrying every obligation,
so the agent wires the whole shape in one sitting. The first commit is the base alone, so the composed options are
one reviewable diff on top of it.

--shape names a sign-in shape to start from: ` + shape.Keys(shape.OfNew()) + `.

The last four are the shapes of an application with tenants: each composes --tenancy and
fills in the tenants' choices. The two tenant-owned shapes hold a person to one tenant (--one-tenant-per-person, which
has every auth build its identity client with identity.WithOneTenantPerPerson()); in the
two global ones a person can be in several. The two invite-only shapes write an
application whose tenants have no directories (--tenant-directories=false): a tenant's
people come by invitation alone, and a Google, Microsoft or other provider's method added
later with the application giving the roles is a way for an invited person to sign in
and nothing more, so impulse audit still reads the application as the invite-only shape.
The other two write the tenant
directories. A flag given beside --shape that contradicts it is refused, naming both
(--tenancy=false, --one-tenant-per-person with a global shape, --tenant-directories with
an invite-only one).

Two shapes have no password sign-in, and an application started from one has no password
method: no password form on its login page, no password on a person's account page, and
no password account among its development logins.

  - tenant-owned-users-by-directory: every person of a tenant signs in through the
    tenant's own directory, so the first auth registers a Google method open to any Google
    Workspace and a Microsoft method open to any Microsoft Entra tenant, neither with
    personal accounts, with the application giving the roles. That is the one way the
    shape starts: nothing is asked about a provider, and no flag takes one provider or
    another width; another width is your change to what was written. The application is
    registered at both providers before it deploys. Its own people, the first
    administrator among them, accept their invitation through either method. The persona
    method is registered beside them for the test environments, and a development run
    signs its administrator in as the persona admin.
  - directory-roles: one organization's directory gives every role, so the first auth
    registers that directory's method alone, no first administrator is invited, and
    nothing in the application gives a role. --method names the directory (google or
    azure) and --organization its one organization (a Google Workspace's domain, a
    Microsoft Entra tenant's id); each is asked for when not given. It is an application
    without tenants. A development run signs in through the simulated directory, as the
    person and with the roles the environment template names (APP_USERNAME, APP_ROLES).

In global-users-by-directory the first auth keeps its password method, and the command
says which impulse add method adds the directory method a tenant's people come through;
until it is added impulse audit reads the application as the invite-only shape beside it.

The other shapes of an application without tenants are a sign-in method's (impulse add
auth and impulse add method take them). Without --shape, and in every shape but those
two, the first auth signs in with a password; a directory method joins it afterwards
with impulse add method, registered beside the password method on the same Auth.

The application's go.mod requires the framework module, github.com/impulseframework/impulse,
at the impulse running new: a release's version, or the pseudo-version of the commit an
impulse installed with go install github.com/impulseframework/impulse/cmd/impulse@<commit>
was built from. The tool directive CI's go tool impulse check runs names impulse's package
in that module, so the templates the application starts from and the check are the same
code; new resolves the pin with go get -tool, through the module proxy. The web roots pin
the browser packages at the release's version, or at the latest release's for an impulse
installed from a commit, since only a release publishes them. An impulse built from a
checkout (go build, go run) names no version the proxy serves, so new refuses it before
writing anything, unless --dev-root names the checkout to build against: then go.mod
requires the framework at a placeholder version and replaces the module with the checkout,
a line that is not for committing.`
}

func newNew() *cobra.Command {
	return newNewWith(newDeps{build: check.RunningBuild, run: check.OSExec{}})
}

func newNewWith(d newDeps) *cobra.Command {
	var nf newFlags

	cmd := &cobra.Command{
		Use:   "new <dir>",
		Short: "Create an Impulse application: the base skeleton under your module path, its first auth named",
		Long:  newLong(),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			start, err := nf.opts.settleShape(cmd)
			if err != nil {
				return err
			}
			pins, err := frameworkPins(d.build(), nf.devRoot)
			if err != nil {
				return err
			}
			reserved, err := skeleton.Reserved(skeleton.Base)
			if err != nil {
				return err
			}
			if nf.authName == "" {
				nf.authName, err = askAuthName(cmd)
				if err != nil {
					return err
				}
			}
			if err := names.ValidateAuth(nf.authName, reserved); err != nil {
				return err
			}
			nf.opts.authName = nf.authName
			if err := nf.opts.settleStart(cmd, start); err != nil {
				return err
			}
			appName, err := appName(nf.name, nf.modulePath)
			if err != nil {
				return err
			}
			transitions, err := nf.opts.transitions()
			if err != nil {
				return err
			}
			if len(transitions) > 0 && nf.skipGit {
				return errors.New("composed options need the first commit to build on; drop --skip-git, or add them afterwards with impulse add")
			}
			if start != nil {
				nf.transition.start = nf.opts.startingPoint(start)
			}

			dir := args[0]
			base := scaffold{
				dir: dir, modulePath: nf.modulePath, name: appName, authName: nf.authName, devRoot: nf.devRoot,
				pins: pins, skipGit: nf.skipGit, composing: len(transitions) > 0,
				signIn: nf.opts.developmentSignIn(start),
			}
			if err := renderBase(cmd, d.run, &base); err != nil {
				return err
			}
			if len(transitions) == 0 {
				return nil
			}

			return composeOptions(cmd, &nf.transition, dir, &nf.opts, transitions)
		},
	}
	nf.bind(cmd)

	return cmd
}

// scaffold is the base rendering impulse new makes: where, under which module path and
// names, and the framework pin its go.mod takes.
type scaffold struct {
	dir, modulePath, name, authName, devRoot string
	// pins is what go.mod and the web roots pin the framework at (frameworkPins).
	pins frameworkPin
	// skipGit leaves the tree uncommitted; composing says options are composed after the
	// base, which needs the first commit.
	skipGit, composing bool
	// signIn is how a development run signs a person in where the application starts
	// with no password sign-in; nil for the base's development login.
	signIn *developmentSignIn
}

// renderBase renders the base skeleton with its first auth, resolves its framework pin,
// makes the first commit unless told not to, and writes the report. With options to
// compose, the commit is required, since the transitions build on it.
func renderBase(cmd *cobra.Command, run check.Execer, s *scaffold) error {
	got, err := skeleton.Render(&skeleton.Options{Candidate: skeleton.Base, Dir: s.dir, ModulePath: s.modulePath, Name: s.name, DevRoot: s.devRoot, Auth: s.authName, Version: s.pins.version, WebVersion: s.pins.web})
	if err != nil {
		return err
	}
	if err := settlePin(cmd.Context(), run, s.dir, s.pins); err != nil {
		return err
	}
	// The owned files are written from the code before the first commit, so the
	// application owns them from its first commit; the base's committed workflow equals
	// the rendering, and a composed option that changes the workspaces rewrites it.
	if err := writeOwned(s.dir); err != nil {
		return err
	}
	gitNote, committed := "", false
	if !s.skipGit {
		gitNote, committed = initRepo(cmd.Context(), s.dir, s.modulePath, s.authName)
	}
	if s.composing && !committed {
		return errors.Newf("the base was rendered but not committed (%s), so the options were not added; commit it and add them with impulse add", gitNote)
	}

	port, emulator, firestore := templatePorts(s.dir)
	goProcs, err := goProcesses(s.dir)
	if err != nil {
		return err
	}
	web, err := webWorkspaces(s.dir)
	if err != nil {
		return err
	}
	report := renderReport{
		headline: fmt.Sprintf("Created %s at %s with the %s auth (%d files).", s.modulePath, s.dir, s.authName, got.Files),
		gitNote:  gitNote, options: !s.composing, pin: s.pins.resolved(),
		candidate: skeleton.Base, dir: s.dir, modulePath: s.modulePath, name: s.name, devRoot: s.devRoot,
		rendered: got, port: port, emulator: emulator, firestore: firestore, goProcs: goProcs, web: web,
		signIn: s.signIn, styled: isTerminal(cmd.OutOrStdout()),
	}
	report.write(cmd.OutOrStdout())

	return nil
}

// goModFile is the module file at an application's root.
const goModFile = "go.mod"

// frameworkPin is what a rendering writes for the framework: the version go.mod requires
// the module at, the version the web roots pin the browser packages at, and whether the
// pin is then resolved through the module proxy (go get -tool), which a rendering under
// --dev-root is not: its go.mod replaces the module with the checkout, and the version is
// a placeholder the proxy never serves.
type frameworkPin struct {
	version, web string
	resolve      bool
}

// resolved is the version the report names as pinned: the version when the pin was
// resolved through the proxy, "" for a rendering that builds against a checkout.
func (p frameworkPin) resolved() string {
	if !p.resolve {
		return ""
	}

	return p.version
}

// frameworkPins is what a new application pins the framework at: the version of the impulse
// that scaffolds it, so the templates the application starts from and the go tool impulse
// check its CI runs are the same code. A release pins its own version, for the module and
// the browser packages alike, since they release together; an impulse installed from a
// commit (go install <package>@<commit>) pins the module at that commit's pseudo-version
// and the browser packages at the latest release's, since a commit publishes none. A build
// that cannot name its commit, one built from a checkout (go build, go run, whatever
// version control stamped on it) or reporting (devel), is refused before anything is
// written: no module proxy serves it, so no pin could fetch it. With a dev root the module
// is pinned at the zero pseudo-version and replaced by the checkout, so nothing is
// resolved; the browser packages take the latest release's version there too.
func frameworkPins(b check.Build, devRoot string) (frameworkPin, error) {
	latest := ledger.Latest()
	if devRoot != "" {
		return frameworkPin{version: module.ZeroPseudoVersion(""), web: framework.WebVersion(latest)}, nil
	}
	if !b.FromModule || !semver.IsValid(b.Version) {
		return frameworkPin{}, errors.Newf("an application pins the framework at the impulse that renders it, and this impulse is a build from a checkout (%s), which names no version the module proxy serves: install impulse from a release or a pushed commit (go install %s@<version|commit>) and run it again, or name the framework checkout to build against with --dev-root; nothing was written", b.Version, check.ImpulseModule)
	}
	web := framework.WebVersion(b.Version)
	if !ledger.IsRelease(b.Version) {
		web = framework.WebVersion(latest)
	}

	return frameworkPin{version: b.Version, web: web, resolve: true}, nil
}

// settlePin finishes go.mod after a rendering: a resolved pin goes through go get -tool,
// which fetches the framework at the version, verifies it against Go's checksum database,
// raises any requirement of the application the framework needs higher, and records the
// checksums in go.sum; then go mod tidy settles the requirements the template carried for
// another version. A rendering against a checkout tidies alone: the replace directive
// resolves the module, and tidy reads its requirements from the checkout. GOWORK=off
// keeps a workspace above the directory out of either.
func settlePin(ctx context.Context, run check.Execer, dir string, p frameworkPin) error {
	if p.resolve {
		at := check.ImpulseModule + "@" + p.version
		if out, err := run.Run(ctx, dir, []string{"GOWORK=off"}, "go", "get", "-tool", at); err != nil {
			return errors.Newf("go get -tool %s could not resolve the framework pin through the module proxy (%v): %s. %s is rendered but not committed: once the proxy answers, remove it and run again", at, errors.Cause(err), strings.TrimSpace(string(out)), dir)
		}
	}
	if out, err := run.Run(ctx, dir, []string{"GOWORK=off"}, "go", "mod", "tidy"); err != nil {
		return errors.Newf("go mod tidy could not settle the requirements of the rendered application (%v): %s. %s is rendered but not committed", errors.Cause(err), strings.TrimSpace(string(out)), dir)
	}

	return nil
}

// writeOwned writes the files impulse owns from the code of the application at dir.
func writeOwned(dir string) error {
	a, err := app.Discover(dir)
	if err != nil {
		return err
	}
	if _, err := ci.Write(a); err != nil {
		return err
	}

	return nil
}

// composeOptions runs the composed options' transitions on the fresh tree: the same ones
// add would run one at a time, in order, ending in one check and one brief.
func composeOptions(cmd *cobra.Command, f *transitionFlags, dir string, opts *composedOptions, transitions []transition) error {
	fmt.Fprintf(cmd.OutOrStdout(), "\nAdding %s.\n\n", opts.describe())
	f.appDir = dir
	a, err := app.Discover(dir)
	if err != nil {
		return err
	}
	repo := handoff.For(a, check.OSExec{})
	if err := repo.Check(cmd.Context()); err != nil {
		return err
	}

	return runTransitions(cmd, f, repo, transitions, opts.reference())
}

// composedOptions are the options impulse new composes into the creation.
type composedOptions struct {
	// authName names the first auth, the base's password auth.
	authName    string
	tenancy     bool
	tenantTable string
	// tenants are the tenants' choices: whether a person belongs to one tenant
	// (--one-tenant-per-person, or a tenant-owned shape) and whether a tenant can have a
	// directory of its own (--tenant-directories, or a shape); shapeKey is the sign-in
	// shape the creation starts from (--shape), which composes tenancy and settles both.
	tenants  tenantChoices
	shapeKey string
	// method and organization are the one directory of Directory roles and its one
	// organization (--method, --organization), each asked for when not given. directory
	// is the step that writes the first auth with no password method, for a shape that
	// has no password sign-in (settleStart); nil for any other start.
	method       string
	organization string
	directory    *transition_.DirectoryStart
	outlets      []string
	apiOutlets   []string
	sites        []string
	files        bool
}

// The flags that name the one directory of Directory roles and its one organization.
const (
	methodFlag       = "method"
	organizationFlag = "organization"
)

// directoryRolesKey is the name --shape takes for Directory roles, the one shape of an
// application without tenants impulse new starts from.
func directoryRolesKey() string {
	return shape.OfNew()[0].Key
}

// settleShape reads --shape and the flags it fills in, before anything is asked or
// written: a shape composes tenancy and settles whether a person belongs to one tenant
// and whether a tenant can have a directory, and a flag that says the opposite of it
// (--tenancy=false, --one-tenant-per-person, --tenant-directories) is refused, naming
// both. Without a shape, either of the tenants' choices needs --tenancy.
func (o *composedOptions) settleShape(cmd *cobra.Command) (*shape.Shape, error) {
	if o.shapeKey != "" {
		// The key is read against everything impulse new offers first, so a refusal names
		// all of it, and Directory roles, which has no tenants, is settled on its own.
		named, err := shape.Find(o.shapeKey, shape.OfNew(), cmd.CommandPath())
		if err != nil {
			return nil, err
		}
		if !named.HasTenants() {
			return o.settleDirectoryRoles(cmd, &named)
		}
	}
	if err := o.directoryFlagsAlone(cmd); err != nil {
		return nil, err
	}
	start, chosen, err := tenantShape(cmd, o.shapeKey, o.tenants.oneTenant)
	if err != nil {
		return nil, err
	}
	o.tenants = chosen
	switch {
	case start == nil && o.tenants.oneTenant && !o.tenancy:
		return nil, errors.Newf("--%s goes with --tenancy: it says a person belongs to one tenant, and the application is given none", shape.OneTenantFlag)
	case start == nil && cmd.Flags().Changed(shape.DirectoriesFlag) && !o.tenancy:
		return nil, errors.Newf("--%s goes with --tenancy: it says whether a tenant can have a directory of its own, and the application is given no tenants", shape.DirectoriesFlag)
	case start == nil:
		return nil, nil
	case cmd.Flags().Changed("tenancy") && !o.tenancy:
		return nil, errTenancyOff(start)
	}
	o.tenancy = true

	return start, nil
}

// settleDirectoryRoles settles a start from Directory roles, before anything is asked or
// written: it is an application without tenants, so --tenancy and the tenants' choices
// beside it are refused, naming both.
func (o *composedOptions) settleDirectoryRoles(cmd *cobra.Command, start *shape.Shape) (*shape.Shape, error) {
	for _, flag := range []string{"tenancy", shape.OneTenantFlag, shape.DirectoriesFlag, "tenant-table"} {
		if !cmd.Flags().Changed(flag) {
			continue
		}
		if flag == "tenancy" && !o.tenancy {
			continue
		}

		return nil, errors.Newf("%s and --%s contradict each other: %s is an application without tenants, in which one organization's directory gives every role, and --%s is a choice of an application with tenants, which gives its roles itself, tenant by tenant. Leave --%s out, or start from a shape with tenants (%s)",
			start.Flag(), flag, start.Name, flag, flag, shape.Keys(shape.OfTenants()))
	}

	return start, nil
}

// directoryFlagsAlone refuses --method and --organization beside any start but Directory
// roles: they name that shape's one directory, and no other start takes a provider.
func (o *composedOptions) directoryFlagsAlone(cmd *cobra.Command) error {
	for _, flag := range []string{methodFlag, organizationFlag} {
		if !cmd.Flags().Changed(flag) {
			continue
		}
		start, err := shape.Find(directoryRolesKey(), shape.OfNew(), cmd.CommandPath())
		if err != nil {
			return err
		}
		if o.shapeKey == "" {
			return errors.Newf("--%s goes with %s: it names the one directory that gives every role in that shape. Without it the first auth signs in with a password, and a directory method joins it afterwards with impulse add method", flag, start.Flag())
		}
		given, err := shape.Find(o.shapeKey, shape.OfNew(), cmd.CommandPath())
		if err != nil {
			return err
		}
		if given.NoPassword() {
			return errors.Newf("%s takes no --%s: the shape starts with a Google method open to any Google Workspace and a Microsoft method open to any Microsoft Entra tenant, and no flag takes one provider or another width. Leave --%s out; another width is your change to what the command writes", given.Flag(), flag, flag)
		}

		return errors.Newf("%s takes no --%s: the flag names the one directory of %s, and in %s the first auth signs in with a password. Leave --%s out, and add a directory method afterwards with impulse add method", given.Flag(), flag, start.Flag(), given.Name, flag)
	}

	return nil
}

// settleStart settles the first auth of a start that has no password sign-in, once the
// auth is named and before anything is rendered: the methods written in the password's
// place, which Directory roles is told (--method, --organization) or asks for, and
// Tenant-owned users, by directory, has fixed. Any other start keeps the base's password
// auth, and nothing is settled.
func (o *composedOptions) settleStart(cmd *cobra.Command, start *shape.Shape) error {
	if start == nil || !start.NoPassword() {
		return nil
	}
	step := &transition_.DirectoryStart{Auth: o.authName, ShapeKey: start.Key, ShapeName: start.String()}
	if start.HasTenants() {
		step.Methods = append(shape.ByDirectoryMethods(), authpkg.Method{Kind: app.MethodPersona, Authority: app.AuthorityApplication})
	} else {
		method, err := o.directoryMethod(cmd, start)
		if err != nil {
			return err
		}
		if o.organization == "" {
			o.organization, err = askOrganization(cmd, method)
			if err != nil {
				return err
			}
		}
		step.Methods, step.Organization = []authpkg.Method{*method}, o.organization
	}
	if err := step.ValidateChoices(); err != nil {
		return err
	}
	o.directory = step

	return nil
}

// directoryMethod is the one method of a start from Directory roles: the directory
// --method names, or the one a person at the terminal answers with, with the choices the
// shape fixes filled in (the directory gives the roles, and the method admits one
// organization). impulse new starts the shape from a Google Workspace or a Microsoft
// Entra tenant; another identity provider's directory is an auth of its own.
func (o *composedOptions) directoryMethod(cmd *cobra.Command, start *shape.Shape) (*authpkg.Method, error) {
	if o.method == "" {
		answer, err := askDirectory(cmd)
		if err != nil {
			return nil, err
		}
		o.method = answer
	}
	kind, _, _ := strings.Cut(o.method, ":")
	if kind != app.MethodGoogle && kind != app.MethodAzure {
		return nil, errors.Newf("--%s %s: impulse new starts %s from a Google Workspace (google) or a Microsoft Entra tenant (azure), with :<registration> to name the registration. Another identity provider's directory is an auth of its own, added with impulse add auth <name> %s --method oidc:<registration>", methodFlag, o.method, start.Name, start.Flag())
	}
	specs, err := parseMethods([]string{o.method})
	if err != nil {
		return nil, err
	}
	method := &specs[0]
	if err := start.Method(method); err != nil {
		return nil, err
	}

	return method, nil
}

// startingPoint is what impulse new says of the shape it started from.
func (o *composedOptions) startingPoint(start *shape.Shape) *startingPoint {
	switch {
	case o.directory == nil:
		// The first auth signs in with a password alone.
		return &startingPoint{shape: *start, says: start.TenantsSay([]string{o.authName}, nil)}
	case start.HasTenants():
		var through []string
		for _, m := range shape.ByDirectoryMethods() {
			through = append(through, fmt.Sprintf("the %s auth's %s method", o.authName, m.Name))
		}

		return &startingPoint{shape: *start, says: append(start.TenantsSay([]string{o.authName}, through), start.StartsWithoutPassword(o.authName, transition_.DevelopmentPersona))}
	default:
		return &startingPoint{shape: *start, says: []string{start.Says(&o.directory.Methods[0], shape.OpensWithRole), start.StartsWithoutPassword(o.authName, "")}}
	}
}

// developmentSignIn is how a development run of the new application signs a person in
// where its first auth is written with no password method: through the persona method,
// as the persona the cast holds, where the application gives the roles, and through the
// one directory method, as the person the environment template names, where the
// directory gives them. It is nil for a start that keeps the base's password auth, whose
// development login the bootstrap seeds.
func (o *composedOptions) developmentSignIn(start *shape.Shape) *developmentSignIn {
	switch {
	case o.directory == nil:
		return nil
	case start.HasTenants():
		return &developmentSignIn{method: authpkg.PersonaRegistration, as: "the persona " + transition_.DevelopmentPersona}
	default:
		return &developmentSignIn{method: o.directory.Methods[0].Name, as: "the person APP_USERNAME names in the environment template, with the roles APP_ROLES names there"}
	}
}

// transitions returns the transitions the options ask for, in the order add would run
// them: the first auth's methods first, where the start has no password sign-in, since
// every other option builds on the auth; then tenancy, since an outlet's members may be
// tenant-scoped, then the outlets, then the file store, which is wired into the flat
// tree, then the sites, since promotion moves what the others laid in.
func (o *composedOptions) transitions() ([]transition, error) {
	var ts []transition
	if o.directory != nil {
		ts = append(ts, *o.directory)
	}
	if o.tenancy {
		ts = append(ts, transition_.Tenancy{Table: o.tenantTable, OneTenantPerPerson: o.tenants.oneTenant, NoDirectories: o.tenants.noDirectories})
	}
	for _, kind := range []struct {
		specs    []string
		sessions bool
		flag     string
	}{{o.outlets, true, "--outlet"}, {o.apiOutlets, false, "--api-outlet"}} {
		for _, spec := range kind.specs {
			name, prefix, ok := strings.Cut(spec, "=")
			if !ok || name == "" || prefix == "" {
				return nil, errors.Newf("%s %q: name the outlet and its prefix as <name>=<prefix>, such as portal=portal/api", kind.flag, spec)
			}
			ts = append(ts, transition_.Outlet{Name: name, Prefix: prefix, Sessions: kind.sessions})
		}
	}
	if o.files {
		ts = append(ts, transition_.Files{})
	}
	if len(o.sites) == 1 {
		return nil, errors.Newf("--site %s: name at least two sites, the first being what the base site becomes under apps/ (an application with one site stays flat)", o.sites[0])
	}
	for i, site := range o.sites {
		if i == 0 {
			continue
		}
		s := transition_.Site{Name: site}
		if i == 1 {
			s.Existing = o.sites[0]
		}
		ts = append(ts, s)
	}

	return ts, nil
}

// describe names the composed options for the report.
func (o *composedOptions) describe() string {
	var parts []string
	if o.directory != nil {
		kinds := make([]string, 0, len(o.directory.Methods))
		for i := range o.directory.Methods {
			kinds = append(kinds, o.directory.Methods[i].Kind)
		}
		parts = append(parts, fmt.Sprintf("the %s auth's sign-in in the password's place (%s)", o.authName, strings.Join(kinds, ", ")))
	}
	if o.tenancy {
		tenancy := "tenancy (" + o.tenantTable
		if o.tenants.oneTenant {
			tenancy += ", one tenant per person"
		}
		if o.tenants.noDirectories {
			tenancy += ", no tenant directories"
		}
		parts = append(parts, tenancy+")")
	}
	for _, spec := range o.outlets {
		parts = append(parts, "the session outlet "+spec)
	}
	for _, spec := range o.apiOutlets {
		parts = append(parts, "the machine outlet "+spec)
	}
	if o.files {
		parts = append(parts, "the file store")
	}
	if len(o.sites) > 1 {
		parts = append(parts, "the sites "+strings.Join(o.sites, ", ")+" (the base site becomes "+o.sites[0]+")")
	}

	return strings.Join(parts, ", ")
}

// reference picks the finished application the brief points the agent at: the one with
// the most of the composed options wired.
func (o *composedOptions) reference() string {
	switch {
	case len(o.sites) > 1:
		return transition_.SitesReference
	case len(o.outlets)+len(o.apiOutlets) > 0, o.directory != nil && !o.tenancy:
		// The outlets candidate also holds the reference directory auth, which a start
		// from Directory roles is read against.
		return transition_.ReferenceCandidate
	default:
		return transition_.TenancyReferenceCandidate
	}
}

// authQuestion is what a person at the terminal is asked when --auth is not given.
const authQuestion = `Name the application's first auth: the population that signs in, plural, lowercase, one
word (staff, members, partners, devices). It names the package pkg/auth/<name>, the
tables, the cookie, and the role file. There is no default.
Auth name: `

// askAuthName asks for the first auth's name when the flag did not give it and a person is
// at the terminal. Without a terminal the flag is required.
func askAuthName(cmd *cobra.Command) (string, error) {
	if !stdinIsTerminal() {
		return "", errors.New("--auth is required: name the application's first auth, " + names.Guidance + ". There is no default")
	}

	return ask(cmd, authQuestion)
}

// directoryQuestion and the two organization questions are what a person at the terminal
// is asked when a start from Directory roles was not told its directory.
const (
	directoryQuestion = `Directory roles: one organization's directory gives every role. Which directory is it:
google (a Google Workspace) or azure (a Microsoft Entra tenant)?
Directory: `
	workspaceQuestion = `Name the Google Workspace whose directory gives the roles, by its domain (fleet.example).
It is written as the development value; a deployment names its own per environment.
Workspace domain: `
	tenantQuestion = `Name the Microsoft Entra tenant whose directory gives the roles, by its tenant id (a GUID,
as the Entra admin center shows it). It is written as the development value; a deployment
names its own per environment.
Tenant id: `
)

// askDirectory asks which directory gives the roles when --method did not say and a
// person is at the terminal. Without a terminal the flag is required.
func askDirectory(cmd *cobra.Command) (string, error) {
	if !stdinIsTerminal() {
		return "", errors.Newf("--%s is required with --shape %s: name the one directory that gives the roles, google (a Google Workspace) or azure (a Microsoft Entra tenant), and its one organization with --%s", methodFlag, directoryRolesKey(), organizationFlag)
	}

	return ask(cmd, directoryQuestion)
}

// askOrganization asks for the directory's one organization when --organization did not
// give it and a person is at the terminal. Without a terminal the flag is required.
func askOrganization(cmd *cobra.Command, m *authpkg.Method) (string, error) {
	question, what := workspaceQuestion, "the Google Workspace whose directory gives the roles, by its domain (fleet.example)"
	if m.Kind == app.MethodAzure {
		question, what = tenantQuestion, "the Microsoft Entra tenant whose directory gives the roles, by its tenant id"
	}
	if !stdinIsTerminal() {
		return "", errors.Newf("--%s is required with --shape %s: name %s", organizationFlag, directoryRolesKey(), what)
	}

	return ask(cmd, question)
}

// ask puts a question to the person at the terminal and returns the line they answer.
func ask(cmd *cobra.Command, question string) (string, error) {
	fmt.Fprint(cmd.ErrOrStderr(), question)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.Wrap(err, "reading the answer")
	}

	return strings.TrimSpace(line), nil
}

// stdinIsTerminal reports whether a person is at the terminal to answer a question.
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// initRepo makes the rendered tree a git repository with its first commit, so impulse add
// starts from a clean tree. It returns a note for the report and whether the commit was
// made: a missing git or an unset identity is the person's to sort out, not a reason to
// undo the render.
func initRepo(ctx context.Context, dir, modulePath, authName string) (note string, committed bool) {
	steps := [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"commit", "-q", "-m", fmt.Sprintf("impulse new: %s with the %s auth", modulePath, authName)},
	}
	for _, step := range steps {
		cmd := exec.CommandContext(ctx, "git", step...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Sprintf("git %s failed (%v): %s. Commit the tree yourself before impulse add.", step[0], err, strings.TrimSpace(string(out))), false
		}
	}

	return "Committed the tree as the application's first commit.", true
}
