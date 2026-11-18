// Package main is the deployment's migration step: it applies the schema migrations,
// checks each auth's role policy against what its permission engine's store holds, and
// invites each auth's first administrator while the auth has no account, by an
// invitation mailed to the address the placement names. It reads the core and data
// configuration levels and nothing above them.
//
// Three flags make it the instrument for the states the migration runner refuses to
// guess at. -version prints what each migrations table (schema, data) says about the
// database and exits; -force <n> and -force-data <n> set a table to a version, clean
// (-1 for no version), print the row before and after, and exit. None of them applies a
// migration, so a force refuses -seed and -version. The pipeline passes them from the
// operations workflow (bedrock deploy migrate); they work anywhere the job runs.
//
// -retire-secret <variable> retires one version of the application secret, named by the
// variable the version is handed over in (APP_SECRET_KEY_1): every stored secret field
// that names the version is opened and written again under the current version, and a
// final read confirms none is left. It exits 0 only then, so the deployment disables the
// version only when no stored value needs it; it prints how many values of each field it
// wrote and never a value, refuses the current version, and is safe to run again. Its
// environment is the migration's with one variable for each enabled version, the one
// being retired among them. It applies no migration and goes with no other flag.
//
// -reinvite-administrator <auth> sends the first administrator's invitation again, for
// mail that was lost: it withdraws the standing invitation of the named auth (staff or members),
// so the earlier link no longer opens, and mails a fresh one to the address the
// placement names. It refuses once the auth has an account, since somebody is then there
// to invite the others, and while the environment has no mailbox to send as. Its
// environment is the migration's. It applies no migration and goes with no other flag.
//
// The command writes what it did about an invitation, the address and when it runs out,
// and never the invitation's link: whoever opens the link becomes the administrator, so
// it is in the mailed message alone.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/auth/members"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/auth/staff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/config"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/deploy"
)

// The exit codes: the arguments refused before anything is opened (as the flag package
// exits), and a run that failed.
const (
	exitRefused = 2
	exitFailed  = 1
)

// errRefused marks arguments the flag package refused and reported itself.
var errRefused = errors.New("the arguments were refused")

func main() {
	os.Exit(execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// execute runs the command with its arguments and answers the exit code; a refusal of
// the arguments and a failed run say why on stderr.
func execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	o, err := parse(args, stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errRefused):
		return exitRefused
	case err != nil:
		fmt.Fprintln(stderr, err)

		return exitRefused
	}
	if err := run(ctx, o, stdout); err != nil {
		fmt.Fprintln(stderr, err)

		return exitFailed
	}

	return 0
}

// options are the flags, parsed.
type options struct {
	seed      bool
	version   bool
	force     versionFlag
	forceData versionFlag
	// retireSecret is the variable of the version of the application secret to retire
	// (APP_SECRET_KEY_1); empty when the command is not asked to retire one.
	retireSecret string
	// reinviteAdministrator is the auth whose first administrator's invitation is to be
	// sent again; empty when the command is not asked to send one.
	reinviteAdministrator string
}

// versionFlag is a version to force: set once given, -1 meaning no version.
type versionFlag struct {
	set bool
	n   int
}

func (f *versionFlag) String() string {
	if !f.set {
		return ""
	}

	return strconv.Itoa(f.n)
}

// Set reads the version: an integer 0 or above, or -1 for no version.
func (f *versionFlag) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n < -1 {
		return errors.Newf("%q is not a version: an integer 0 or above, or -1 for no version", s)
	}
	f.set, f.n = true, n

	return nil
}

// parse reads the arguments and refuses, before anything is opened, what cannot run
// together: a force applies no migration, so neither -seed nor -version goes with it,
// and -version applies none either, so -seed does not go with it; a retirement and a
// fresh invitation each go alone, and the invitation names an auth that invites.
func parse(args []string, stderr io.Writer) (*options, error) {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.BoolVar(&o.seed, "seed", false, "apply the development seed (schema/devseed) after the schema migrations, as data migrations; the pipeline passes it in test environments and never in production")
	fs.BoolVar(&o.version, "version", false, "print what the schema and data migrations tables say about the database (no version, a version, or dirty at one with the progress recorded) and exit; nothing is applied")
	fs.Var(&o.force, "force", "set the schema migrations table to this `version`, clean, print the row before and after, and exit; -1 means no version; the next run continues from it")
	fs.Var(&o.forceData, "force-data", "the same for the data migrations table")
	fs.StringVar(&o.retireSecret, "retire-secret", "", "retire one version of the application secret, named by the `variable` it is handed over in (APP_SECRET_KEY_1): write every stored secret that names it again under the current version, confirm none is left, and exit; nothing is migrated")
	fs.StringVar(&o.reinviteAdministrator, "reinvite-administrator", "", "withdraw the standing invitation of the named `auth`'s first administrator ("+strings.Join(deploy.AdministratorAuths(), ", ")+") and mail a fresh one to the address the placement names, for mail that was lost, and exit; refused once the auth has an account; nothing is migrated")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errors.Wrap(err, "flag.FlagSet.Parse()")
		}

		return nil, errRefused
	}
	if fs.NArg() > 0 {
		return nil, errors.Newf("the command takes no argument: %q", fs.Arg(0))
	}
	forcing := o.force.set || o.forceData.set
	switch {
	case o.reinviteAdministrator != "" && (forcing || o.seed || o.version || o.retireSecret != ""):
		return nil, errors.New("-reinvite-administrator applies no migration and reports no version: it goes with no other flag")
	case o.reinviteAdministrator != "" && !slices.Contains(deploy.AdministratorAuths(), o.reinviteAdministrator):
		return nil, errors.Newf("-reinvite-administrator %s: no auth of that name invites a first administrator; name one of %s", o.reinviteAdministrator, strings.Join(deploy.AdministratorAuths(), ", "))
	case o.retireSecret != "" && (forcing || o.seed || o.version):
		return nil, errors.New("-retire-secret applies no migration and reports no version: it goes with no other flag")
	case forcing && o.seed:
		return nil, errors.New("-force and -force-data apply no migration: -seed cannot go with them")
	case forcing && o.version:
		return nil, errors.New("-version reports and -force changes: give one or the other")
	case o.version && o.seed:
		return nil, errors.New("-version applies no migration: -seed cannot go with it")
	}

	return &o, nil
}

// run does what the flags ask: the report, the force, the retirement, the fresh
// invitation, or the migrations.
func run(ctx context.Context, o *options, out io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	settings, err := config.LoadDatabaseSettings(ctx)
	if err != nil {
		return errors.Wrap(err, "config.LoadDatabaseSettings()")
	}
	switch {
	case o.version:
		return report(ctx, settings, out)
	case o.force.set || o.forceData.set:
		return force(ctx, settings, o, out)
	case o.retireSecret != "":
		return retireSecret(ctx, o.retireSecret, out)
	case o.reinviteAdministrator != "":
		return reinviteAdministrator(ctx, o.reinviteAdministrator, out)
	}

	return migrate(ctx, settings, o.seed, out)
}

// report prints one line per migrations table: what it says about the database.
func report(ctx context.Context, settings config.DatabaseSettings, out io.Writer) error {
	schema, data, err := deploy.Versions(ctx, settings)
	if err != nil {
		return errors.Wrap(err, "deploy.Versions()")
	}
	fmt.Fprintf(out, "%s: %s\n%s: %s\n", deploy.SchemaTable, schema, deploy.DataTable, data)

	return nil
}

// force sets each table asked for to its version, the schema's first, printing the row
// before and after each.
func force(ctx context.Context, settings config.DatabaseSettings, o *options, out io.Writer) error {
	forces := []struct {
		table deploy.Table
		flag  versionFlag
	}{{deploy.SchemaTable, o.force}, {deploy.DataTable, o.forceData}}
	for _, f := range forces {
		if !f.flag.set {
			continue
		}
		before, err := row(ctx, settings, f.table)
		if err != nil {
			return err
		}
		if err := deploy.Force(ctx, settings, f.table, f.flag.n); err != nil {
			return errors.Wrap(err, "deploy.Force()")
		}
		after, err := row(ctx, settings, f.table)
		if err != nil {
			return err
		}
		to := "no version"
		if f.flag.n >= 0 {
			to = "version " + strconv.Itoa(f.flag.n)
		}
		fmt.Fprintf(out, "%s: %s\nforced %s to %s\n%s: %s\n", f.table, before, f.table, to, f.table, after)
	}

	return nil
}

// row is what one migrations table says about the database.
func row(ctx context.Context, settings config.DatabaseSettings, table deploy.Table) (string, error) {
	schema, data, err := deploy.Versions(ctx, settings)
	if err != nil {
		return "", errors.Wrap(err, "deploy.Versions()")
	}
	if table == deploy.DataTable {
		return data.String(), nil
	}

	return schema.String(), nil
}

// retireSecret retires one version of the application secret: it opens the data level,
// whose resource client holds the secret keys built from every version in the
// environment, and writes every stored secret that names the version again under the
// current one.
func retireSecret(ctx context.Context, variable string, out io.Writer) error {
	data, err := config.NewDataConfiguration(ctx)
	if err != nil {
		return errors.Wrap(err, "config.NewDataConfiguration()")
	}
	defer data.Close()

	if err := deploy.RetireSecret(ctx, data.ResourceClient(), variable, out); err != nil {
		return errors.Wrap(err, "deploy.RetireSecret()")
	}

	return nil
}

// reinviteAdministrator sends the named auth's first administrator's invitation again:
// it opens the data level, whose auths mail through the environment's mailbox, withdraws
// the standing invitation and mails a fresh one to the address the placement names.
func reinviteAdministrator(ctx context.Context, auth string, out io.Writer) error {
	data, err := config.NewDataConfiguration(ctx)
	if err != nil {
		return errors.Wrap(err, "config.NewDataConfiguration()")
	}
	defer data.Close()

	if err := deploy.ReinviteAdministrator(ctx, data, auth, out); err != nil {
		return errors.Wrap(err, "deploy.ReinviteAdministrator()")
	}

	return nil
}

// migrate is the deployment's migration step itself; out takes what it did about each
// first administrator's invitation.
func migrate(ctx context.Context, settings config.DatabaseSettings, seed bool, out io.Writer) error {
	// The schema goes first, before any client opens: the tables the data level's
	// clients read may not exist yet.
	if err := deploy.MigrateSchema(ctx, settings); err != nil {
		return errors.Wrap(err, "deploy.MigrateSchema()")
	}

	// The seed, when this deployment asks for it: the development data as data
	// migrations, tracked apart from the schema, so a seeded database takes nothing
	// twice and a new seed file reaches it. Before the roles, as cmd/bootstrap does.
	if seed {
		if err := deploy.SeedDevelopmentData(ctx, settings); err != nil {
			return errors.Wrap(err, "deploy.SeedDevelopmentData()")
		}
	}

	data, err := config.NewDataConfiguration(ctx)
	if err != nil {
		return errors.Wrap(err, "config.NewDataConfiguration()")
	}
	defer data.Close()

	// The default roles travel with the release: opening the data level validated each
	// auth's role file against the collection, so a release whose roles are wrong stopped
	// above. A domain role is held in every tenant domain, so a tenant created after this
	// deployment needs no step of its own. What is left is to print what each store holds
	// that this release cannot use as written.
	if err := deploy.CheckRoles(ctx, data.Staff().Access(), staff.Name); err != nil {
		return errors.Wrap(err, "deploy.CheckRoles(staff)")
	}
	if err := deploy.CheckRoles(ctx, data.Members().Access(), members.Name); err != nil {
		return errors.Wrap(err, "deploy.CheckRoles(members)")
	}

	// The foothold: an auth with no account has nobody who can sign in, so each auth's
	// first administrator is invited now, after the roles the release ships are in place,
	// by an invitation mailed to the address the placement names, and nobody is invited
	// once the auth has an account. A release that cannot mail it stops here: the link is
	// never written to a log.
	if err := deploy.InviteAdministrators(ctx, data, out); err != nil {
		return errors.Wrap(err, "deploy.InviteAdministrators()")
	}

	// The feature flags the release declares: a new flag is written off, a known one keeps
	// its state and takes the release's description, and one the release no longer
	// declares is deleted, so every instance of this release lists the same flags.
	if err := deploy.MigrateFeatures(ctx, data.ResourceClient()); err != nil {
		return errors.Wrap(err, "deploy.MigrateFeatures()")
	}

	return nil
}
