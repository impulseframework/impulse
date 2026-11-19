package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/spf13/cobra"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/ci"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
	"github.com/impulseframework/impulse/cmd/impulse/internal/framework"
	"github.com/impulseframework/impulse/cmd/impulse/internal/handoff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/ledger"
	"github.com/impulseframework/impulse/cmd/impulse/internal/shape"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
	transition_ "github.com/impulseframework/impulse/cmd/impulse/internal/transition"
)

func newAdd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add an option to the application: files and generator edits, then a handoff for the wiring",
		Long: `add makes the deterministic half of an option (files laid in, the generator program and
the seams edited at the source level, go generate run), stages it, and runs impulse check. The checks that fail are
exactly the obligations of the option just enabled, and add hands them to an f.agent the
way impulse handoff does: a brief at ` + handoff.File + ` with what changed, what the
option means, the failing checks verbatim, and a rendered reference application; --f.agent
launches Claude Code on it and verifies the guardrails when it returns.`,
	}
	cmd.AddCommand(newAddOutlet())
	cmd.AddCommand(newAddTenancy())
	cmd.AddCommand(newAddAuth())
	cmd.AddCommand(newAddMethod())
	cmd.AddCommand(newAddSite())
	cmd.AddCommand(newAddFeature())
	cmd.AddCommand(newAddFiles())
	cmd.AddCommand(newAddJob())

	return cmd
}

func newAddJob() *cobra.Command {
	var (
		f        transitionFlags
		schedule string
	)

	cmd := &cobra.Command{
		Use:   "job <name>",
		Short: "Add a job: a command of the job process, its work to write, and optionally the schedule that starts it",
		Long: `job scaffolds a command of the application's job process, the second binary (cmd/jobs)
bedrock deploys as the Cloud Run job beside the service: pkg/jobs gains the job's work,
a function named for the job (prune-reports becomes PruneReports) written empty, which
refuses until the work is written, with the command's name the job process runs it
under; cmd/jobs/<name> gains the command, a cobra command whose Run runs the work over
the resource client of the data level; cmd/jobs/main.go, the root that opens the data
level once and registers every command, is written by the first job and extended by the
rest; the lint configuration admits the command tree's library (github.com/spf13/cobra)
and go.mod requires it. A name carrying a group (files/cleanup) puts the command under a
group command of that word.

--schedule "<cron>" also writes the method marked @rpc and @schedule in the rpc package
that starts the job on the schedule through the job process's starter (the service
starts its job, never a schedule of the job's own), with the starter's wiring where the
application lacks it (the rpc package and its Client, the site level's scheduler guard
and job driver, the App's SchedulerAuth and RPCClient, the test configurers' fakes,
WithRPC in the generator program), as impulse add files wires it for the cleanup; then go
generate emits the method's handler and route. Without a schedule nothing starts the job
until a scheduled method or a caller does.

The work itself, the suite that proves it, and the schedule when none was given are
handed to the agent.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTransition(cmd, &f, transition_.Job{Name: args[0], Schedule: schedule}, "")
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&schedule, "schedule", "", "the cron expression Cloud Scheduler starts the job on, in UTC (\"0 3 * * *\"); a method marked @schedule is written to start it")

	return cmd
}

func newAddFiles() *cobra.Command {
	var f transitionFlags

	cmd := &cobra.Command{
		Use:   "files",
		Short: "Wire the file store: APP_FILE_STORE, the development directory, the cleanup job and its schedule",
		Long: `files wires the framework's file store into the application (resource/filestore): the
data level reads APP_FILE_STORE, opens the store it names when it is set (a directory in
development, a bucket on Cloud Run) and builds the resource client over it, so the
generated handlers keep uploaded files in it and a committed transaction's released
objects are deleted from it; .envrc.template sets file://uploads and .gitignore ignores
the directory; the bootstrap empties a directory store before it seeds; cmd/jobs gains
the orphaned-file cleanup command (pkg/jobs, filestore.Cleanup over the generated
FileHolders()); and the rpc package gains CleanUpFiles, a method marked @schedule that
starts the job process's cleanup each day at 09:00 UTC through the job driver the site
configuration opens from the template job the stack sets in APP_JOBS_TEMPLATE, the
setting its environment embeds, and the version (resource/jobs/cloudrun), behind the
scheduler guard it builds from APP_SCHEDULER_INVOKER (resource/scheduled). An application without an rpc
package gains one, with WithRPC in the generator program and the Client the generated
handlers ask the App for; the test configurers gain a nil guard and a fake starter.

Which resources record files (@file on a key column, an @upload method) is the
application's; until one does, the store is wired and idle.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTransition(cmd, &f, transition_.Files{}, "")
		},
	}
	f.bind(cmd)

	return cmd
}

func newAddFeature() *cobra.Command {
	var (
		f    transitionFlags
		site string
	)

	cmd := &cobra.Command{
		Use:   "feature <name>",
		Short: "Add a feature flag: its constant, its development seed row, regeneration, and a handoff for the gate",
		Long: `feature declares a feature flag: a release switch whose code ships before the feature is
turned on, differs per environment, and is removed once the feature is permanent or
abandoned. The resources package gains a resource.Feature constant named after the flag
(cargo_manifest becomes CargoManifest) with a doc stub, in ` + app.FeaturesFile + `; the development
seed gains the flag's row, off, in schema/devseed (set Enabled to TRUE there to start
development and the test environments with the feature on); and go generate runs, so the
generated Features() lists the flag for the deploy's MigrateFeatures and the browser's
Feature union carries it. In the sites layout --site names the site whose resources
package declares it.

What the flag gates (@feature(<Constant>) on a resource, a field or a method, or a read of
it in Go or in the browser) and the description are handed to the agent; the Feature flags
page the menu file places lists the flag and flips it; the feature-flags check fails while
the flag gates nothing and is read nowhere.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTransition(cmd, &f, transition_.AddFeature{Name: args[0], Site: site}, "")
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&site, "site", "", "in the sites layout, the site whose resources package declares the flag")

	return cmd
}

func newAddSite() *cobra.Command {
	var (
		f        transitionFlags
		existing string
	)

	cmd := &cobra.Command{
		Use:   "site <name>",
		Short: "Add a site: a stand-alone application on a host of its own under apps/<name>/",
		Long: `site adds a site, a stand-alone application on a host of its own: its own main package,
handlers, router, resources (empty to start), authorization suite, and browser workspace
under apps/<name>/, copied from the first site with the imports renamed; its generator
program and directive; its serve and browser processes on the next ports; its TypeScript
target in the shared generator; and its router collection in the union the roles are
reconciled against. The name is one word of lowercase letters and digits starting with a
letter, at most 25 characters, since it is the second segment of every per-site resource
name bedrock renders and of the site's default hostname label (<code>-<site>): the Cloud
Run service name <prefix>-<env>-<region code>-<code>-<site>-app fits 49 characters with 25
left for the site at the organization's longest prefix, environment, region code and
application code. A name outside the rule is refused, --existing's too.

On a flat application the first site added promotes the layout to sites, the one
non-additive transition: the existing site moves under apps/<existing>/ (every import of
its packages changes), its generator becomes cmd/generate/<existing>generator, the site
level's bundle variable becomes APP_DIST (set per site process, in the Procfile), the
deployment's collection becomes the union of the sites' router collections, and a shared
generator is laid in over an empty pkg/sharedresources. --existing names what the existing
site becomes and is asked when not given, since the name is the site's directory for good.
Everything existing belongs to that site.

The new site's resources, its place in the integration suite, its browser application's
own titles and pages, and the deployment configuration outside the repository are handed
to the agent.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if existing == "" {
				a, err := app.Discover(f.appDir)
				if err != nil {
					return err
				}
				if a.Profile().Layout == app.LayoutFlat {
					answer, err := askExistingSite(cmd, a)
					if err != nil {
						return err
					}
					existing = answer
				}
			}

			return runTransition(cmd, &f, transition_.Site{Name: args[0], Existing: existing}, transition_.SitesReference)
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&existing, "existing", "", "on a flat application, the name the existing site takes under apps/ (asked when not given)")

	return cmd
}

// askExistingSite asks what the existing site is called when a flat application grows its
// second site and --existing did not say.
func askExistingSite(cmd *cobra.Command, a *app.App) (string, error) {
	if !stdinIsTerminal() {
		return "", errors.New("--existing is required on a flat application: the existing site moves under apps/<existing>/ and the name is its directory for good, so it is asked rather than defaulted")
	}
	hint := ""
	if len(a.WebApps) > 0 {
		if projects, err := a.ReadAngular(a.WebApps[0].Dir); err == nil && len(projects) > 0 {
			hint = fmt.Sprintf(" Its browser application is the %s project.", projects[0].Name)
		}
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "The application is flat: adding a second site moves the existing one under apps/<name>/.\nWhat is the existing site called? The name is its directory for good.%s\n> ", hint)
	answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && answer == "" {
		return "", errors.Wrap(err, "reading the answer")
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", errors.New("no name given: pass --existing <name>")
	}

	return answer, nil
}

// transitionFlags are the flags every add subcommand shares.
type transitionFlags struct {
	appDir       string
	agent        bool
	agentCommand string
	agentArgs    []string
	skipGenerate bool
	// start is the starting point of a command given --shape, nil without one: what it
	// says is written after the change, and again after the check where the check
	// refuses what the shape wrote into this application.
	start *startingPoint
}

func (f *transitionFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.appDir, "app", ".", "application root (the directory holding go.mod)")
	f.bindAgent(cmd)
}

// bindAgent binds the flags of the handoff alone, for a command whose application root is
// not a flag.
func (f *transitionFlags) bindAgent(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&f.agent, "agent", false, "launch the agent on the brief and verify when it returns")
	cmd.Flags().StringVar(&f.agentCommand, "agent-command", handoff.DefaultCommand, "the agent executable")
	cmd.Flags().StringArrayVar(&f.agentArgs, "agent-arg", nil, "an argument appended to the agent's command line (repeatable), such as --model or --max-budget-usd")
	cmd.Flags().BoolVar(&f.skipGenerate, "skip-generate", false, "skip the regen check (no emulator, no working-tree rewrite)")
}

// transition is one option's deterministic half.
type transition interface {
	Validate(a *app.App) error
	Apply(ctx context.Context, a *app.App, exec check.Execer) (*transition_.Change, error)
	Meaning() string
}

func newAddOutlet() *cobra.Command {
	var (
		f      transitionFlags
		prefix string
		auth   string
		tokens string
	)

	cmd := &cobra.Command{
		Use:   "outlet <name>",
		Short: "Add a router outlet: a second URL space on the same host",
		Long: `outlet adds a router outlet to a flat application. A session outlet (--auth <name>) is a
second browser surface bound to the named auth package, pkg/auth/<name>: the generator
program gains WithRouterOutlet with that auth's Auth and WebApp("/<name>") and a
GenerateTypescript target for the outlet, and the console's browser project is copied to
web/<name> with its API prefix, base path, and ports rewritten and registered in
angular.json, the package scripts, and the Procfile. A console that was alone at / moves
to /console with its API at /console/api, since no browser application is mounted at /
beside another (an installed application's scope is every URL under its start), and the
regenerated router answers the root alone with a redirect there; the brief lists the Go
and prose that still name /api. A machine outlet (--tokens <name>) is a surface with no
browser whose requests carry the bearer tokens the named auth issues to its people and
their programs: the generator program gains WithRouterOutlet with Tokens(<pkg/auth/name>),
the generated router composes the token check in front of the outlet's routes and the
device flow's endpoints under its prefix, and the auth's session outlet gains the token
routes where a signed-in person issues, lists and revokes their tokens and approves a
command-line client. (An application that kept a hand-written router gains
ServesSessions or nothing, as before, and the brief names the auth the outlet binds to.)

Either way go generate emits the outlet's routes, handlers, and client, and the
generated router mounts the outlet from its declaration. The App's handlers the router
now requires (the outlet's session getter and browser-application pair, or its token
surface), the configuration, the members, and the tests are handed to the agent with
the failing checks as the obligations.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (auth != "") == (tokens != "") {
				return errors.New("choose --auth <name> (a browser surface bound to that auth) or --tokens <name> (a machine surface carrying that auth's tokens)")
			}
			bound := auth
			if tokens != "" {
				bound = tokens
			}

			return runTransition(cmd, &f, transition_.Outlet{Name: args[0], Prefix: prefix, Sessions: auth != "", Auth: bound}, transition_.ReferenceCandidate)
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&prefix, "prefix", "", "the outlet's URL prefix without slashes at either end, such as portal/api (required)")
	cmd.Flags().StringVar(&auth, "auth", "", "a session outlet: a browser surface bound to this auth package (pkg/auth/<name>), with its own client and browser project")
	cmd.Flags().StringVar(&tokens, "tokens", "", "a machine outlet: a surface with no browser whose requests carry the bearer tokens this auth package (pkg/auth/<name>) issues to its people and their programs")
	_ = cmd.MarkFlagRequired("prefix")

	return cmd
}

func newAddTenancy() *cobra.Command {
	var f transitionFlags

	cmd := &cobra.Command{
		Use:   "tenancy",
		Short: "Make the application tenanted: a tenant table as the domain universe",
		Long: `tenancy makes a flat, untenanted application tenanted. The tenant table becomes the
next schema migration, with two development tenants seeded as a data migration beside
it; the tenant-record struct is added to the resource package as a global resource
annotated @tenant, from which the generator derives the tenant segment and emits the
constructor of the tenant roster; the generator program gains WithConcealedDomains; the
data level gains the roster, built with that constructor over the live service's tenants
signal and started where the configuration is built (a new file plus a field and the
start in DataConfiguration), so a tenant made on any instance is usable at once,
without a restart; the app exposes the roster to the generated code as TenantRoster()
and hands its Domains to the session permissions (a new file plus the Configurer, App
and UserPermissions edits); and the reference's tenant pages (the tenant's sign-in page
where a tenant can have a directory, All tenants and the dialog a person who signed up
creates their own tenant through) are copied into the browser app, whose frame carries
the tenant the URL names, the guard and the place switcher.

Nobody creates a tenant directly. Staff of the auth whose outlets serve the record bring
an organization in by inviting one person, and that person's first sign-in makes the
tenant: the auth's settings gain the generated maker (TenantMaker), its New refuses a
nil one and hands it to the identity client, and where the data level has the
skeleton's shape the roster is built ahead of that auth and the maker passed in.

--one-tenant-per-person says a person belongs to one tenant, as in an application whose
tenants own their people: every auth whose roles the application gives builds its
identity client with identity.WithOneTenantPerPerson(), so a role in a second tenant is
refused however it would be given. Without it a person can be in several tenants.

--tenant-directories says whether a tenant can have a directory of its own, and is on
unless told otherwise. With it the command writes the directories record and its table,
the tenant record's directory-only switch, each auth's directory admission and the
tenant's Sign-in page, so a tenant's administrator, signed in through their
organization's directory, makes it the tenant's, opens the tenant to it and makes the
tenant directory only. With --tenant-directories=false it writes none of them: no tenant
has a directory, a tenant's people come by invitation alone, and a Google, Microsoft or
other provider's method is a way for an invited person to sign in and nothing more,
whichever methods the application has or gets later. Laying the directories in
afterward is the author's act; no impulse add method does it.

--shape names a sign-in shape to start from, one of the four an application with tenants
has, and fills in the tenants' choices: ` + shape.Keys(shape.OfTenants()) + `.
The two tenant-owned shapes hold a person to one tenant (--one-tenant-per-person), and in
the two global ones a person can be in several. In the two invite-only shapes a tenant's
people come by invitation alone: the command writes no tenant directories
(--tenant-directories=false), whatever sign-in methods the application already
registers, and names the ones that go through an organization's directory as ways for
an invited person to sign in. In the other two a tenant's people come through its own
directory: the command writes the tenant directories, and where no sign-in method goes
through an organization's directory yet it says which impulse add method adds it.
Whether a tenant takes a directory, and whether that directory's people get an account
without an invitation, is each tenant's choice, made while the application runs. A flag
given beside --shape that contradicts it is refused, naming both, and impulse audit
reads the shape back from the code (its sign-in section).

Which resources become tenant-scoped and how their rows are assigned, the bootstrap
order, the harnesses, the tests, the tenant segment in the browser's routes, and the
tenant menus in its menu file are handed to the agent with the failing checks as the
obligations.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			step, start, err := tenancyAsked(cmd)
			if err != nil {
				return err
			}
			if start != nil {
				if f.start, err = tenancyStartingPoint(start, f.appDir); err != nil {
					return err
				}
			}

			return runTransition(cmd, &f, step, transition_.TenancyReferenceCandidate)
		},
	}
	f.bind(cmd)
	// The step's own flags are read back from the command (tenancyAsked).
	cmd.Flags().String(tenantTableFlag, "Tenants", "the tenant-record table, PascalCase and plural")
	cmd.Flags().Bool(shape.OneTenantFlag, false, oneTenantUsage)
	cmd.Flags().Bool(shape.DirectoriesFlag, true, directoriesUsage)
	bindShape(cmd, new(string), shape.OfTenants(), "the tenants' choices")

	return cmd
}

// tenantTableFlag is add tenancy's flag for the tenant record's table.
const tenantTableFlag = "tenant-table"

// tenancyAsked is the tenancy step add tenancy was asked for, read from its flags: the
// tenant record's table and the tenants' choices, which the shape settles where one is
// given and the flags settle otherwise. start is the shape, nil without --shape. A flag
// that says the opposite of the shape is refused.
func tenancyAsked(cmd *cobra.Command) (step transition_.Tenancy, start *shape.Shape, err error) {
	table, err := cmd.Flags().GetString(tenantTableFlag)
	if err != nil {
		return transition_.Tenancy{}, nil, errors.Wrap(err, "pflag.FlagSet.GetString()")
	}
	shapeKey, err := cmd.Flags().GetString(shape.FlagName)
	if err != nil {
		return transition_.Tenancy{}, nil, errors.Wrap(err, "pflag.FlagSet.GetString()")
	}
	oneTenant, err := cmd.Flags().GetBool(shape.OneTenantFlag)
	if err != nil {
		return transition_.Tenancy{}, nil, errors.Wrap(err, "pflag.FlagSet.GetBool()")
	}
	start, chosen, err := tenantShape(cmd, shapeKey, oneTenant)
	if err != nil {
		return transition_.Tenancy{}, nil, err
	}

	return transition_.Tenancy{Table: table, OneTenantPerPerson: chosen.oneTenant, NoDirectories: chosen.noDirectories}, start, nil
}

func newAddAuth() *cobra.Command {
	var (
		f               transitionFlags
		methods         []string
		authority       string
		usernameIsEmail bool
		outlet          string
		shapeKey        string
	)

	cmd := &cobra.Command{
		Use:   "auth <name>",
		Short: "Add an auth: a population that holds roles in its own store and signs in through the methods on its Auth",
		Long:  addAuthLong,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseRetiredAccounts(cmd); err != nil {
				return err
			}
			start, err := methodShape(cmd, shapeKey)
			if err != nil {
				return err
			}
			specs, err := authMethods(cmd, methods, authority, usernameIsEmail, start)
			if err != nil {
				return err
			}
			if outlet == "" {
				answer, err := askOutlet(cmd, f.appDir, args[0])
				if err != nil {
					return err
				}
				outlet = answer
			}
			if start != nil {
				if f.start, err = authStartingPoint(start, f.appDir, outlet, specs); err != nil {
					return err
				}
			}

			return runTransition(cmd, &f, transition_.Auth{Name: args[0], Methods: specs, Outlet: outlet, Shape: shapeKey}, transition_.ReferenceCandidate)
		},
	}
	f.bind(cmd)
	bindRetiredAccounts(cmd)
	bindShape(cmd, &shapeKey, shape.OfMethods(), "each method's choices")
	cmd.Flags().StringArrayVar(&methods, "method", nil, "a sign-in method the auth starts with (repeatable): password, azure:<registration>, google:<registration>, or oidc:<registration>; password when none is given")
	cmd.Flags().StringVar(&authority, "authority", "", "who owns role membership for the directory methods: directory (roles synchronized at every sign-in) or application (roles assigned in the application); asked when not given")
	cmd.Flags().BoolVar(&usernameIsEmail, "username-is-email", false, "the password method's sign-in name is the person's email address")
	cmd.Flags().StringVar(&outlet, "outlet", "", "the outlet the auth serves, whose declaration is pointed at it: default for the console's, or a WithRouterOutlet name; asked when not given")

	return cmd
}

// authMethods reads add auth's --method flags and settles the methods' authority, which
// the command takes once for all of them: it is asked when a directory method is given
// without it and refused with the password method alone. --username-is-email goes to
// the password method, and is refused without one. Under --shape (start) the shape
// fills in what the flags left open, so the authority is not asked, and a method or a
// flag that contradicts the shape is refused.
func authMethods(cmd *cobra.Command, methods []string, authority string, usernameIsEmail bool, start *shape.Shape) ([]authpkg.Method, error) {
	specs, err := parseMethods(methods)
	if err != nil {
		return nil, err
	}
	directory, password := false, false
	for i := range specs {
		if specs[i].Directory() {
			directory = true
		} else {
			password = true
		}
	}
	if directory && authority == "" && start == nil {
		answer, err := askAuthority(cmd)
		if err != nil {
			return nil, err
		}
		authority = answer
	}
	if !directory && authority != "" {
		return nil, errors.New("--authority goes with a directory method (azure, google or oidc); the password method's role membership is the application's")
	}
	if !password && usernameIsEmail {
		return nil, errors.New("--username-is-email goes with the password method, whose sign-in name it makes the person's email address; a directory method's people sign in as the directory names them")
	}
	for i := range specs {
		switch {
		case specs[i].Directory():
			specs[i].Authority = authority
		default:
			specs[i].UsernameIsEmail = usernameIsEmail
		}
	}
	if start != nil {
		if err := fillMethods(start, specs); err != nil {
			return nil, err
		}
	}

	return specs, nil
}

// outletQuestion opens what a person at the terminal is asked when --outlet is not given;
// the application's outlets follow it, one per line.
const outletQuestion = `Which outlet does the %s auth serve? Its declaration in the generator program is
pointed at the auth, so the generated router mounts the auth's login routes under the
outlet's prefix.
`

// askOutlet asks which outlet the auth serves when the flag did not say and a person is
// at the terminal, listing the application's outlets. Without a terminal the flag is
// required: there is no default, because an auth is reachable only through the outlet
// bound to it, and the tool cannot know which population an outlet is for.
func askOutlet(cmd *cobra.Command, appDir, auth string) (string, error) {
	a, err := app.Discover(appDir)
	if err != nil {
		return "", err
	}
	choices := transition_.OutletChoices(a)
	var keys []string
	for i := range choices {
		if !choices[i].Outlet.Machine() {
			keys = append(keys, choices[i].Key)
		}
	}
	if len(keys) == 0 {
		return "", errors.New("no outlet for the auth to serve: the generator programs declare no session outlet (GenerateRoutes, WithRouterOutlet with Auth); impulse add outlet adds one")
	}
	if !stdinIsTerminal() {
		return "", errors.Newf("--outlet is required: the outlet the %s auth serves, one of %s; its declaration is pointed at the auth, and there is no default because an auth is reachable only through the outlet bound to it", auth, strings.Join(keys, ", "))
	}
	w := cmd.ErrOrStderr()
	fmt.Fprintf(w, outletQuestion, auth)
	width := 0
	for i := range choices {
		width = max(width, len(choices[i].Key))
	}
	for i := range choices {
		fmt.Fprintf(w, "  %-*s  %s\n", width, choices[i].Key, outletDescription(&choices[i]))
	}
	fmt.Fprint(w, "[outlet]: ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.Wrap(err, "reading the answer")
	}
	answer := strings.TrimSpace(line)
	for i := range choices {
		if choices[i].Key == answer {
			return answer, nil
		}
	}

	return "", errors.Newf("%q is not an outlet: %s", answer, strings.Join(keys, ", "))
}

// outletDescription says what an outlet is for the question: its prefix and the auth it
// binds to, or that it is a machine surface.
func outletDescription(c *transition_.OutletChoice) string {
	o := &c.Outlet
	switch {
	case o.Tokens != "":
		return fmt.Sprintf("/%s, a machine outlet: a surface with no sign-in, carrying the %s auth's tokens", o.Prefix, path.Base(o.Tokens))
	case o.APIKey:
		return fmt.Sprintf("/%s, a machine outlet of the retired APIKey form: a surface with no sign-in", o.Prefix)
	case o.Auth != nil:
		return fmt.Sprintf("/%s, bound to the %s auth", o.Prefix, path.Base(o.Auth.ImportPath))
	default:
		return fmt.Sprintf("/%s, no auth declared", o.Prefix)
	}
}

// parseMethods reads --method flags: password, or <kind>:<registration>. The registration
// is the kind's name when a Microsoft, Google or persona method gives none; an identity
// provider's is the application's word for the provider and has no default.
func parseMethods(flags []string) ([]authpkg.Method, error) {
	if len(flags) == 0 {
		return []authpkg.Method{{Kind: app.MethodPassword}}, nil
	}
	var specs []authpkg.Method
	for _, flag := range flags {
		kind, name, _ := strings.Cut(flag, ":")
		switch kind {
		case app.MethodPassword:
			if name != "" {
				return nil, errors.Newf("--method %s: the password method takes no registration name", flag)
			}
			specs = append(specs, authpkg.Method{Kind: app.MethodPassword})
		case app.MethodAzure, app.MethodGoogle, app.MethodPersona:
			if name == "" {
				name = kind
			}
			specs = append(specs, authpkg.Method{Kind: kind, Name: name})
		case app.MethodOIDC:
			if name == "" {
				return nil, errors.Newf("--method %s: an identity provider's registration needs a name, oidc:<registration>: the application's word for the provider (okta, company-sso), which is the segment of its routes and the connection every link made through it carries", flag)
			}
			specs = append(specs, authpkg.Method{Kind: kind, Name: name})
		default:
			return nil, errors.Newf("--method %s: password, azure:<registration>, google:<registration>, oidc:<registration>, or persona", flag)
		}
	}

	return specs, nil
}

func newAddMethod() *cobra.Command {
	var (
		f               transitionFlags
		authority       string
		slot            string
		usernameIsEmail bool
		shapeKey        string
		label           string
	)

	cmd := &cobra.Command{
		Use:   "method <auth> <kind> [<registration>]",
		Short: "Add a sign-in method to an auth: a registration on its Auth, in a file of its own",
		Long:  addMethodLong,
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseRetiredAccounts(cmd); err != nil {
				return err
			}
			start, err := methodShape(cmd, shapeKey)
			if err != nil {
				return err
			}
			registration := ""
			if len(args) == 3 {
				registration = args[2]
			}
			spec, err := methodSpec(cmd, args[1], registration, slot, authority, label, usernameIsEmail, start)
			if err != nil {
				return err
			}
			if start != nil {
				if f.start, err = methodStartingPoint(start, f.appDir, args[0], &spec); err != nil {
					return err
				}
			}

			return runTransition(cmd, &f, &transition_.Method{Auth: args[0], Spec: spec}, transition_.ReferenceCandidate)
		},
	}
	f.bind(cmd)
	bindRetiredAccounts(cmd)
	bindShape(cmd, &shapeKey, shape.OfMethods(), "the method's choices")
	cmd.Flags().StringVar(&authority, "authority", "", "who owns role membership for a directory method: directory (roles synchronized at every sign-in, one organization only) or application (roles assigned in the application); asked when not given, and application for the persona method")
	cmd.Flags().StringVar(&slot, "slot", "", "the organization slot of a Microsoft or Google method: azure one|listed|any|personal|one-personal|any-personal, google one|listed|any-workspace|any|mixed (default: one); oidc and persona take none")
	cmd.Flags().BoolVar(&usernameIsEmail, "username-is-email", false, "the password method's sign-in name is the person's email address")
	cmd.Flags().StringVar(&label, "label", "", "the words people read for an oidc method's provider, on the login page's button and in the lists of a person's ways to sign in (\"Lakeside ID\"); without it they are made from the registration name (company-sso reads Company Sso)")

	return cmd
}

// labelGiven refuses --label where it says nothing a method can carry, before anything
// is asked or read: given with no words, or given for a kind other than an
// organization's identity provider, whose words alone are made from a registration
// name. Every other kind is shown by what it is or by its brand.
func labelGiven(cmd *cobra.Command, kind, label string) error {
	if !cmd.Flags().Changed("label") {
		return nil
	}
	if strings.TrimSpace(label) == "" {
		return errors.New("--label takes the words people read for the provider (--label \"Lakeside ID\"); leave the flag out for the words made from the registration name")
	}
	if kind != app.MethodOIDC {
		probe := authpkg.Method{Kind: kind, Label: label}

		return probe.Validate()
	}

	return nil
}

// methodSpec reads add method's arguments and flags into the method to add: its kind,
// its registration name (the kind's own when none is given, registration being empty
// then, except for an identity provider, which has no default), its slot, its
// authority and, for an identity provider, the words --label gives it. The password
// method takes neither --slot nor --authority, the persona method no slot and the
// application's authority.
// Under --shape (start) the shape fills in what the flags left open and refuses a
// method or a flag that contradicts it; without one, a directory method's authority is
// asked when the flag did not say.
func methodSpec(cmd *cobra.Command, kind, registration, slot, authority, label string, usernameIsEmail bool, start *shape.Shape) (authpkg.Method, error) {
	spec := authpkg.Method{Kind: kind, Name: registration, Slot: slot, Authority: authority, UsernameIsEmail: usernameIsEmail, Label: label}
	if err := labelGiven(cmd, kind, label); err != nil {
		return spec, err
	}
	switch {
	case registration != "":
	case spec.Kind == app.MethodOIDC:
		return spec, errors.New("the oidc method needs a registration name: impulse add method <auth> oidc <registration>, the application's word for the provider (okta, company-sso), which is the segment of its routes and the connection every link made through it carries")
	case spec.Kind != app.MethodPassword:
		spec.Name = spec.Kind
	}
	if spec.Kind == app.MethodPassword && (slot != "" || authority != "") {
		return spec, errors.New("the password method takes no --slot or --authority")
	}
	if spec.Kind == app.MethodPersona {
		if slot != "" {
			return spec, errors.New("the persona method takes no --slot: the persona provider is one issuer, and which personas it offers is the deployment's cast")
		}
		if authority == "" {
			spec.Authority = app.AuthorityApplication
		}
	}
	if start != nil {
		// The shape fills in what the flags left open, so nothing is asked.
		if err := start.Method(&spec); err != nil {
			return spec, err
		}
	}
	if spec.Directory() && spec.Authority == "" {
		answer, err := askAuthority(cmd)
		if err != nil {
			return spec, err
		}
		spec.Authority = answer
	}

	return spec, nil
}

// authorityQuestion is what a person at the terminal is asked when --authority is not
// given for a directory method.
const authorityQuestion = `Who is the authority for this method's role membership?
  directory    the directory's roles are synchronized at every sign-in; roles it does
               not name are removed, and a sign-in naming no known role is refused
  application  roles are assigned in the application: the bootstrap now, an
               administration surface later
[directory/application]: `

// askAuthority asks who owns role membership when the flag did not say and a person is at
// the terminal. Without a terminal the flag is required: there is no default, because the
// wrong answer deletes hand-assigned roles at the next sign-in.
func askAuthority(cmd *cobra.Command) (string, error) {
	if !stdinIsTerminal() {
		return "", errors.New("--authority is required for a directory method: directory (the directory's roles are synchronized at every sign-in and roles it does not name are removed) or application (roles are assigned in the application). It is asked because the wrong answer deletes hand-assigned roles at the next sign-in")
	}
	fmt.Fprint(cmd.ErrOrStderr(), authorityQuestion)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.Wrap(err, "reading the answer")
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != transition_.AuthorityDirectory && answer != transition_.AuthorityApplication {
		return "", errors.Newf("%q is not an answer: directory or application", strings.TrimSpace(line))
	}

	return answer, nil
}

// runTransition is the flow every add subcommand shares: a clean tree, the
// deterministic half, go generate through the transition, the check with its fixes,
// staging, and the handoff.
func runTransition(cmd *cobra.Command, f *transitionFlags, t transition, reference string) error {
	ctx := cmd.Context()
	a, err := app.Discover(f.appDir)
	if err != nil {
		return err
	}
	repo := handoff.For(a, check.OSExec{})
	if err := repo.Check(ctx); err != nil {
		return err
	}
	dirty, err := repo.Dirty(ctx)
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return errors.Newf("the working tree is not clean (%d path(s)): commit or stash first, so the transition is one reviewable diff", len(dirty))
	}

	return runTransitions(cmd, f, repo, []transition{t}, reference)
}

// runTransitions applies the transitions in order, each on the tree the one before it
// left, then runs the check once, stages everything, and hands what is left to the agent
// in one brief. impulse new composes its options this way; add runs one.
func runTransitions(cmd *cobra.Command, f *transitionFlags, repo handoff.Repo, ts []transition, reference string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	exec := check.OSExec{}
	var changes, meanings []string
	// left counts the obligations the changes list themselves (the hand-written uses a
	// removal leaves, a scaffolded job's work), which the check cannot see, so the
	// handoff happens for them even when the check is clean.
	left := 0
	for _, t := range ts {
		// Each transition reads the tree the one before it left.
		a, err := app.Discover(f.appDir)
		if err != nil {
			return err
		}
		if err := t.Validate(a); err != nil {
			return err
		}
		change, err := t.Apply(ctx, a, exec)
		if err != nil {
			return err
		}
		writeChange(out, change)
		changes = append(changes, change.Text())
		meanings = append(meanings, t.Meaning())
		left += len(change.Left)
	}
	if f.start != nil {
		f.start.write(out)
	}

	// The tree changed, so the application is read again for the checks.
	a, err := app.Discover(f.appDir)
	if err != nil {
		return err
	}
	// The owned files follow the code: a transition that changed the browser workspaces
	// rewrote the CI workflow as part of its change, and this leaves every flow, impulse
	// new's composed options included, with the owned files current before the check.
	owned, err := ci.Write(a)
	if err != nil {
		return err
	}
	if owned.Written {
		fmt.Fprintf(out, "Rewrote %s from the code.\n\n", ci.List(owned.WrittenFiles()))
	}
	env := &check.Env{App: a, Exec: exec, SkipGenerate: f.skipGenerate, Fix: true, Out: cmd.ErrOrStderr(), Ledger: ledger.Current{}}
	results := check.Run(ctx, env, check.All())
	if err := repo.StageAll(ctx); err != nil {
		return err
	}
	check.Report(out, results)
	if !check.Failed(results) && left == 0 {
		if len(ts) == 1 {
			fmt.Fprintf(out, "\nThe check is clean: the option is wired. Review the diff and open the pull request.\n")
		} else {
			fmt.Fprintf(out, "\nThe check is clean: the %d options are wired. Review the diff and open the pull request.\n", len(ts))
		}

		return nil
	}

	referenceDir := ""
	if reference != "" {
		if referenceDir, err = renderReference(reference); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "impulse: no reference application: %s\n", message(err))
		}
	}
	guard, err := handoff.Take(a, handoff.FromTree(a))
	if err != nil {
		return err
	}
	brief := &handoff.Brief{App: a, Change: strings.Join(changes, "\n"), Meaning: strings.Join(meanings, "\n\n"), Results: results, Reference: referenceDir, Guard: guard, Left: left}
	ag := &handoff.Agent{Command: f.agentCommand, ExtraArgs: f.agentArgs}

	return completeHandoff(ctx, out, f.appDir, env, repo, brief, ag, f.agent)
}

// writeChange tells the user what the transition did and left.
func writeChange(w io.Writer, change *transition_.Change) {
	fmt.Fprintf(w, "Changed:\n")
	for _, d := range change.Did {
		fmt.Fprintf(w, "  - %s\n", d)
	}
	if len(change.Skipped) > 0 {
		fmt.Fprintf(w, "Left to the agent:\n")
		for _, s := range change.Skipped {
			fmt.Fprintf(w, "  - %s\n", s)
		}
	}
	if len(change.Left) > 0 {
		fmt.Fprintf(w, "Yours (%s):\n", change.LeftNote)
		for _, u := range change.Left {
			fmt.Fprintf(w, "  - %s\n", u)
		}
	}
	fmt.Fprintln(w)
}

// renderReference renders the candidate with the option wired into a temporary directory
// for the f.agent to read, and returns its path.
func renderReference(candidate string) (string, error) {
	dir, err := os.MkdirTemp("", "impulse-reference-"+candidate+"-")
	if err != nil {
		return "", errors.Wrap(err, "os.MkdirTemp()")
	}
	if _, err := skeleton.Render(&skeleton.Options{Candidate: candidate, Dir: dir, ModulePath: "example.com/reference/" + candidate, Version: ledger.Latest(), WebVersion: framework.WebVersion(ledger.Latest())}); err != nil {
		return "", err
	}

	return dir, nil
}

// addAuthLong is the help of add auth.
const addAuthLong = `auth adds an auth package, pkg/auth/<name>, rendered from the same source as the skeleton's
auths, so the new population owns its own session, user, identity, auth event and pending
sign-in tables, cookie, store prefix, and role file from the start, with one file per
sign-in method. Its session tables are written as migrations under the new prefix and its
access tables copied from an auth the application has; an empty role file is written in
the package (the file its Roles() embeds and hands to the permission engine), and the data
level constructs it beside that auth. The package carries a foothold (foothold.go) for
an auth whose roles the application assigns, with the password method or without: the
first administrator the migrate command invites while nobody of the auth holds the
Super Administrator role, by an invitation mailed to the address the placement names
for each environment
(APP_<AUTH>_ADMINISTRATOR). The invitation gives the Super Administrator role and the
first administrator's roles, and the person accepts it as everyone invited after them
does, by signing in from the link through a method of the auth: with the password method
they choose a password there, and through a directory the invitation is what ties the
sign-in to the roles, however many organizations the method admits. No account is made
ahead of the person and no password is generated. A release leaves a standing invitation
as it is, replaces one that ran out, and withdraws one for an address the placement no
longer names; the migrate command's -reinvite-administrator mails a fresh one on demand;
and both refuse while nobody holds the role and the application has no mailbox to send
as, since the link is never written to a log. Once somebody holds the Super Administrator
role nobody is invited, and an account that holds no such role stops no invitation. The foothold's roles, its setting in the data level and its invitation
from pkg/deploy are handed to the agent; impulse check holds the package to inviting
(first-administrator). An auth the application generates no management for
(generation.GenerateIdentity) invites nobody, since nothing there would give an accepted
invitation its roles, and impulse check refuses it until the application does: it has no
way in.

--method names the sign-in methods the Auth starts with, repeatable: password (the default
when none is given), azure:<registration> (Microsoft Entra over OpenID Connect),
google:<registration> (Google Workspace over OpenID Connect) or oidc:<registration> (an
organization's own identity provider over OpenID Connect, one known by its issuer alone,
such as Okta or Keycloak), the registration being the segment of the method's routes
(user/<registration>/login). An identity provider's registration is the application's word
for the provider (okta), which every link made through it carries, so it has no default. A
directory method added here takes one organization; impulse add method composes the
other slots. Every method starts invite only: a person with no account needs an
invitation, or an account an administrator made for them. Whether a method admits a
person with no invitation is chosen in the running application, for each method, on the
application's Sign-in page (Open sign-up, with the role each new person gets, and Make
invite only), by whoever holds the grant on it; nothing in code or on this command line
says it. A method whose directory gives the
roles (--authority directory) has no such choice: everyone the directory signs in becomes
a user with the roles the directory names. --username-is-email makes the
password method's sign-in name the person's email address (session.UsernameIsEmail(), and
.UsernameIsEmail() on the declaration): the generated management derives the address from
the username, and a verified change of address renames the user, whose roles stay with
the account. Every auth
mails, through the application's one sender (Settings.Mail) and to the login page of the
surface bound to it (Settings.MailLoginURL): an administrator invites a person by address
with the roles the invitation gives, and a signed-in person adds and verifies an address.
A directory method's settings are read from the environment under the registration's
variables (APP_<NAME>_<REGISTRATION>_*), the Procfile builds with the session library's
skipAuth tag (the directory simulated until the application is registered with one), and
the role-membership authority is set by --authority, which is asked when it is not given:
"directory" makes the directory's roles the authority (every sign-in reconciles the
person's roles to them and removes what they do not name), "application" keeps role
assignment in the application. There is no default, because the wrong answer deletes
hand-assigned roles at the next sign-in.

--outlet names the outlet the new auth serves, asked when it is not given: default for the
one GenerateRoutes declares (the console's), or a WithRouterOutlet name such as portal; in
the sites layout the site's name for its default outlet. The outlet must exist (impulse
add outlet adds one, bound to an auth the application has), and an API-key outlet is
refused, since a machine surface has no sign-in. The outlet's declaration in the generator
program is pointed at the new auth with its methods, in place of the auth it declared, so
the regenerated router mounts the auth's session group under the outlet's prefix: its
login routes, the methods route its login page renders from, and the pending sign-in
routes. There is no default, because an auth is reachable only through the outlet bound
to it. Under a hand-written router the program declares no Auth to point, and the brief
says to mount the group in the router.

--shape names a sign-in shape to start from, one of the four a sign-in method's choices
tell apart, and fills in each method's choices: invite-only (1 Invite only: the
application gives the roles, and the methods stay invite only), directory-members (2
Directory members: a directory method limited to one organization, which an
administrator opens so that everyone in the organization's directory gets an account; a
password method beside it is left invite only), directory-roles (3 Directory roles: one
directory method whose directory gives the roles, alone in its auth) and open-sign-up (4
Open sign-up: methods anyone can use, the password method as it is, a Microsoft method
with any tenant and personal accounts, a Google method with any Google account, which
an administrator opens). The shape settles --authority, so it is not asked. Open is
never written: for directory-members and open-sign-up the command says that it is
chosen on the application's Sign-in page while the application runs. A method or a
flag given beside --shape that contradicts it is refused, naming both (--shape
directory-roles with --authority application, --shape open-sign-up with an identity
provider's method). impulse audit reads the shape back from the code (its sign-in
section).

Checking the auth's roles in the deploy, the App's handlers the generated router now
requires for the outlet, its development identities, and the tests that prove its people
are strangers to the other auths are handed to the agent.`

// addMethodLong is the help of add method.
const addMethodLong = `method adds a sign-in method to an auth the application has: password, azure (Microsoft
Entra over OpenID Connect), google (Google Workspace over OpenID Connect), oidc (an
organization's own identity provider over OpenID Connect) or persona (the persona sign-in
of the test environments, over OpenID Connect against the persona provider), under the
registration name given (the segment of its routes, user/<registration>/login; the kind's
name when none is given, except for oidc, which has no default; the password method is
always registered as password). The method gets a file of its own in the auth's package
with its registration constant, its settings, its handlers and the registration New makes;
the Auth file's Handlers and Settings gain it; the data level reads its settings from the
environment under the registration's variables (APP_<AUTH>_<REGISTRATION>_*); every outlet
bound to the auth declares it, so the generated router mounts its routes and the login page,
which renders from the methods route, offers it; and for a directory method the Procfile
builds with the session library's skipAuth tag, which simulates the directory until the
application is registered with it.

The oidc method is the session library's generic OpenID Connect method, for a provider
known by its issuer alone, such as Okta or Keycloak. The provider is one organization's and
is named in code: its issuer, client id, client secret and callback are the registration's
settings, read from the environment, so it serves the whole application and changes with a
release. The registration name is required: it is the application's word for the provider
(okta, company-sso), and the connection every link made through it carries, so it is chosen
once. It takes no slot. With --authority directory
every sign-in reconciles the person's roles to the role names the provider's ID token
carries in its roles claim, which the provider has to be set up to send.

--label gives the words people read for an oidc method's provider: the login page's
button says "Sign in with" and those words, and the application names the method by them
wherever it lists a person's ways to sign in. They are written on the method's
declaration in the generator program (.Labeled), which is where they are changed, with go
generate run after. Without the flag the words are made from the registration name
(company-sso reads "Company Sso") and the handoff says where to change them. The flag is
the oidc method's alone: every other kind is shown by what it is (Password, Persona) or
by its brand (Google, Microsoft), and the registration name stays the key in routes,
variables and stored links whatever the words are.

The persona method is registered only where the persona provider is configured: the
placement's persona block names the provider's issuer and the application's client ID root,
bedrock sets them in the first environment and its pull requests, and a later environment,
where they are empty, never offers the method. It takes no slot and the application
authority, and is left invite only: the personas are the accounts of the auth's cast (pkg/auth/<auth>/cast.json,
written empty here and named by hand), which the migrate command seeds and the deploy
registers with the provider, and a sign-in links the persona's identity to its account.

--slot is the organization slot of a directory method, which organizations' accounts it
admits. For azure: one (one tenant, the default), listed (named tenants), any (any tenant),
personal (Microsoft personal accounts alone), one-personal (one tenant and personal
accounts), any-personal (any tenant and personal accounts). For google: one (one
Workspace, the default), listed (named Workspaces), any-workspace (any Workspace), any
(any Google account), mixed (one Workspace and any Google account).

Every method starts invite only: a person with no account needs an invitation, or an
account an administrator made for them. Whether the method admits a person with no
invitation is chosen in the running application, on the application's Sign-in page (Open
sign-up, which says who that admits for a method this wide and asks for the role each
new person gets, and Make invite only), by whoever holds the grant on it. The slot says
how wide the method is, which stays in code. Through the password method a stranger
gives their address on the login page, follows the link mailed to it and chooses a
password, while the method is open.

--username-is-email makes the password method's sign-in name the person's email address
(session.UsernameIsEmail(), and .UsernameIsEmail() on the declaration): the generated
management derives the address from the username, and a verified change of address renames
the user, whose roles stay with the account.

--authority is who owns role membership for a directory method, asked when not given:
directory (the directory's roles are synchronized at every sign-in, allowed with one
organization only) or application (roles are assigned in the application).

--shape names a sign-in shape to start from, one of the four a sign-in method's choices
tell apart, and fills in the choices the flags left open: invite-only (1 Invite only:
the application gives the roles, any slot, and the method stays invite only),
directory-members (2 Directory members: a method limited to the organizations it names,
--slot one or listed or an identity provider's, which an administrator opens so that
everyone in the organization's directory gets an account), directory-roles (3 Directory
roles: --authority directory with one organization, alone in its auth) and open-sign-up
(4 Open sign-up: a method anyone can use, the password method, azure with --slot
any-personal unless a wider-than-one slot is given, google with --slot any, which an
administrator opens). The shape settles --authority, so it is not asked. Open is never
written: for directory-members and open-sign-up the command says that it is chosen on
the application's Sign-in page while the application runs, and what it does there. A
method or a flag given beside --shape that contradicts it is refused, naming both
(--shape directory-members with --slot any, --shape invite-only with --authority
directory, --shape directory-members with the password method). impulse audit reads
the shape back from the code (its sign-in section).`

// retiredAccountsFlag is the flag add auth and add method took for a method's standard
// account rule, and accountsRetired what either says of it now.
const (
	retiredAccountsFlag = "accounts"
	accountsRetired     = "--accounts is no longer an option: whether a sign-in method admits a person with no account and no invitation is chosen in the running application, for each method. Every method starts invite only, and an administrator opens one to sign-up, or makes it invite only again, on the application's Sign-in page. Remove --accounts and run the command again"
)

// bindRetiredAccounts keeps --accounts known to a command, hidden, so that giving it is
// answered with what replaced it (refuseRetiredAccounts) and not as a flag nobody ever
// heard of.
func bindRetiredAccounts(cmd *cobra.Command) {
	cmd.Flags().String(retiredAccountsFlag, "", "retired: the choice is made in the running application")
	_ = cmd.Flags().MarkHidden(retiredAccountsFlag)
}

// refuseRetiredAccounts refuses a command given --accounts, whatever its value, saying
// where the choice is made now.
func refuseRetiredAccounts(cmd *cobra.Command) error {
	if cmd.Flags().Changed(retiredAccountsFlag) {
		return errors.New(accountsRetired)
	}

	return nil
}
