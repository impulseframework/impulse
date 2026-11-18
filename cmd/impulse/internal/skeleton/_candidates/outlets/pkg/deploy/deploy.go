// Package deploy holds the database steps a deployment runs and the development
// bootstrap reuses: applying the schema migrations, checking the release's role policy
// against what the permission engine's store holds, inviting each auth's first
// administrator while the auth has no account, and bringing the feature flags to the
// release's declarations.
package deploy

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/auth/members"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/auth/staff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/config"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/resources"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
)

// MigrationsSource is where the schema migrations live, relative to the module root.
const MigrationsSource = "file://schema/migrations"

// DevSeedSource is the development data (the tenants a developer signs in to), relative
// to the module root: applied by cmd/bootstrap in development and by the migrate
// command with -seed in test environments (the pipeline passes it there and never in
// production), as data migrations tracked apart from the schema, so a seeded database
// takes nothing twice and a new seed file reaches it.
const DevSeedSource = "file://schema/devseed"

// MigrateSchema connects to the existing database and applies every pending schema
// migration. Creating the database is not its business: a deployment's database exists
// before its first migration runs, and cmd/bootstrap creates the emulator's.
func MigrateSchema(ctx context.Context, settings config.DatabaseSettings) error {
	migrator, err := initiator.NewSpannerMigrator(ctx, settings.ProjectID, settings.InstanceID, settings.DatabaseName)
	if err != nil {
		return errors.Wrapf(err, "initiator.NewSpannerMigrator(): %s", settings.DatabasePath())
	}
	defer migrator.Close()

	// A database already at the latest migration is not a failure: the migrator has
	// nothing to apply and returns nil.
	if err := migrator.MigrateUpSchema(ctx, MigrationsSource); err != nil {
		return errors.Wrap(err, "initiator.SpannerMigrator.MigrateUpSchema()")
	}

	return nil
}

// SeedDevelopmentData applies the development seed to the database as data migrations.
// An application without a seed directory, or with an empty one, has nothing to apply.
func SeedDevelopmentData(ctx context.Context, settings config.DatabaseSettings) error {
	files, err := filepath.Glob(filepath.Join(strings.TrimPrefix(DevSeedSource, "file://"), "*.up.sql"))
	if err != nil {
		return errors.Wrap(err, "filepath.Glob()")
	}
	if len(files) == 0 {
		return nil
	}
	migrator, err := initiator.NewSpannerMigrator(ctx, settings.ProjectID, settings.InstanceID, settings.DatabaseName)
	if err != nil {
		return errors.Wrapf(err, "initiator.NewSpannerMigrator(): %s", settings.DatabasePath())
	}
	defer migrator.Close()

	if err := migrator.MigrateUpData(ctx, DevSeedSource); err != nil {
		return errors.Wrap(err, "initiator.SpannerMigrator.MigrateUpData()")
	}

	return nil
}

// Table names one of the two migrations tables the migrate command reports and forces:
// the schema migrations' and the data migrations'.
type Table string

// The two tables, as the command's flags and output name them.
const (
	SchemaTable Table = "schema"
	DataTable   Table = "data"
)

// Versions reads what the schema and data migrations tables say about the database: no
// version, clean at a version, or dirty at one with the progress the runner recorded.
// Nothing is applied.
func Versions(ctx context.Context, settings config.DatabaseSettings) (schema, data initiator.Version, err error) {
	migrator, err := initiator.NewSpannerMigrator(ctx, settings.ProjectID, settings.InstanceID, settings.DatabaseName)
	if err != nil {
		return initiator.Version{}, initiator.Version{}, errors.Wrapf(err, "initiator.NewSpannerMigrator(): %s", settings.DatabasePath())
	}
	defer migrator.Close()

	schema, err = migrator.SchemaVersion(ctx)
	if err != nil {
		return initiator.Version{}, initiator.Version{}, errors.Wrap(err, "initiator.SpannerMigrator.SchemaVersion()")
	}
	data, err = migrator.DataVersion(ctx)
	if err != nil {
		return initiator.Version{}, initiator.Version{}, errors.Wrap(err, "initiator.SpannerMigrator.DataVersion()")
	}

	return schema, data, nil
}

// Force sets one migrations table to a version, clean, with no progress recorded; -1
// leaves the table with no version. It is for the states the runner refuses to guess at
// (a database the old library left dirty with no progress recorded, an in-flight
// operation Spanner no longer has, a file changed in its applied part): a person reads
// the database's state, decides what it really holds, and forces that version, and the
// next run continues from it.
func Force(ctx context.Context, settings config.DatabaseSettings, table Table, version int) error {
	migrator, err := initiator.NewSpannerMigrator(ctx, settings.ProjectID, settings.InstanceID, settings.DatabaseName)
	if err != nil {
		return errors.Wrapf(err, "initiator.NewSpannerMigrator(): %s", settings.DatabasePath())
	}
	defer migrator.Close()

	switch table {
	case SchemaTable:
		if err := migrator.ForceSchema(ctx, version); err != nil {
			return errors.Wrap(err, "initiator.SpannerMigrator.ForceSchema()")
		}
	case DataTable:
		if err := migrator.ForceData(ctx, version); err != nil {
			return errors.Wrap(err, "initiator.SpannerMigrator.ForceData()")
		}
	default:
		return errors.Newf("table %q is neither %s nor %s", table, SchemaTable, DataTable)
	}

	return nil
}

// CheckRoles reports what the deploy should hear about the named auth's role policy
// before the release takes traffic: the warnings its role file raises, and what its
// store holds that this release cannot use as written (a grant it skips, a custom role a
// default role of the same name shadows, memberships naming a role nothing defines). Each
// is printed on its own "Warning:" line. Nothing is written: the default roles travel with
// the release, which validated them when it opened the engine, so there is nothing to
// reconcile; a store that cannot be read is the error.
func CheckRoles(ctx context.Context, client *access.Client, name string) error {
	warnings, err := client.CheckPolicy(ctx)
	if err != nil {
		return errors.Wrapf(err, "access.Client.CheckPolicy(): the %s auth", name)
	}
	for _, w := range warnings {
		fmt.Printf("Warning: %s\n", w)
	}

	return nil
}

// AdministratorAuths names the auths whose first administrator the deployment invites, in
// the order a release invites them: the staff auth, whose people sign in with a password,
// and the members auth, whose people sign in through the directory and whose roles the
// application assigns. They are what the migrate command's -reinvite-administrator takes.
func AdministratorAuths() []string {
	return []string{staff.Name, members.Name}
}

// firstAdministrator is what the deployment knows of one auth's first administrator: the
// address the placement names, and the auth's two ways to invite them.
type firstAdministrator struct {
	email    string
	invite   func(ctx context.Context, email string) (*session.FirstAdministrator, error)
	reinvite func(ctx context.Context, email string) (*session.FirstAdministrator, error)
}

// administratorOf answers the named auth's first administrator, and refuses a name that
// is none of AdministratorAuths.
func administratorOf(data *config.DataConfiguration, auth string) (*firstAdministrator, error) {
	switch auth {
	case staff.Name:
		return &firstAdministrator{email: data.StaffAdministrator(), invite: data.Staff().InviteAdministrator, reinvite: data.Staff().ReinviteAdministrator}, nil
	case members.Name:
		return &firstAdministrator{email: data.MembersAdministrator(), invite: data.Members().InviteAdministrator, reinvite: data.Members().ReinviteAdministrator}, nil
	default:
		return nil, errors.Newf("no auth named %q invites a first administrator: name one of %s", auth, strings.Join(AdministratorAuths(), ", "))
	}
}

// InviteAdministrators invites each auth's first administrator while the auth has no
// account, the way into a deployed environment, and writes what it did to out, never an
// invitation's link. The invitation is mailed to the address the placement names for the
// environment (APP_STAFF_ADMINISTRATOR, APP_MEMBERS_ADMINISTRATOR) and gives the Super
// Administrator role and the auth's first administrator's roles; the person signs in from
// its link by a method of the auth, a staff administrator choosing a password there and a
// members administrator through the directory, whose address for them must be the
// invited one. No account is created ahead of either and no password is generated.
//
// Every release runs it, after the role check, so the invitation names roles the release
// ships. An invitation that stands for the address is left as it is and nothing is
// mailed; one that ran out (seven days) is replaced; one that stands for an address the
// placement no longer names is withdrawn, so its link no longer opens, and the address
// named now is mailed. Once the auth has an account nobody is invited: every person after
// the first comes from the administration surface, and the development bootstrap creates
// its own logins instead; an invitation of the first administrator's that still stands
// then for an address the placement no longer names is withdrawn all the same. It
// refuses, with nothing mailed, while the auth has no account and the placement names no
// address, and while the environment has no mailbox to send as: an invitation's link is
// never written to a log, so the mailbox comes before the first release.
func InviteAdministrators(ctx context.Context, data *config.DataConfiguration, out io.Writer) error {
	for _, auth := range AdministratorAuths() {
		administrator, err := administratorOf(data, auth)
		if err != nil {
			return err
		}
		invited, err := administrator.invite(ctx, administrator.email)
		if err != nil {
			return invitationRefused(err, auth, "InviteAdministrator")
		}
		reportAdministrator(out, auth, invited)
	}

	return nil
}

// ReinviteAdministrator withdraws the standing invitation of the named auth's first
// administrator and mails a fresh one to the address the placement names, for mail that
// was lost: the earlier link no longer opens. It writes what it did to out, never the
// link. It is what the migrate command's -reinvite-administrator runs, on demand from the
// deployment's operations, and it refuses once the auth has an account, since somebody is
// then there to invite the others; it refuses as InviteAdministrators does besides.
func ReinviteAdministrator(ctx context.Context, data *config.DataConfiguration, auth string, out io.Writer) error {
	administrator, err := administratorOf(data, auth)
	if err != nil {
		return err
	}
	invited, err := administrator.reinvite(ctx, administrator.email)
	if err != nil {
		return invitationRefused(err, auth, "ReinviteAdministrator")
	}
	reportAdministrator(out, auth, invited)

	return nil
}

// invitationRefused says where a refusal of the first administrator's invitation came
// from, and, for one made for want of a mailbox, which settings give the environment its
// mailbox: the auth has already said which setting names the address.
func invitationRefused(err error, auth, method string) error {
	if errors.Is(err, session.ErrFirstAdministratorNoMailbox) {
		return errors.Wrapf(err, "%s.Auth.%s(): give the environment its mailbox before this release, the deployment's hand step for the mailbox: name the mailbox (APP_MAIL_MAILBOX) and add its app password (the secret APP_MAIL_APP_PASSWORD), then run the release again", auth, method)
	}

	return errors.Wrapf(err, "%s.Auth.%s()", auth, method)
}

// reportAdministrator writes what was done about one auth's first administrator: the
// address and when the invitation runs out, never its link, which is in the message alone.
func reportAdministrator(out io.Writer, auth string, invited *session.FirstAdministrator) {
	for _, address := range invited.Withdrawn {
		fmt.Fprintf(out, "Withdrew the invitation of the %s auth's first administrator mailed to %s: its link no longer opens\n", auth, address)
	}
	switch invited.Outcome {
	case session.FirstAdministratorInvited:
		fmt.Fprintf(out, "Invited the %s auth's first administrator: the invitation was mailed to %s and runs out %s\n", auth, invited.Email, invited.ExpiresAt.UTC().Format(time.RFC3339))
	case session.FirstAdministratorStanding:
		fmt.Fprintf(out, "The %s auth's first administrator is invited: the invitation mailed to %s stands until %s, and nothing was mailed\n", auth, invited.Email, invited.ExpiresAt.UTC().Format(time.RFC3339))
	case session.FirstAdministratorHasAccount:
		fmt.Fprintf(out, "The %s auth has an account: no first administrator invited\n", auth)
	}
}

// MigrateFeatures brings the FeatureFlags table to the release's declarations
// (resources.Features): a flag declared for the first time is inserted off, a known
// flag keeps its state and takes the release's description, and a flag the release no
// longer declares is deleted. It runs after the schema migration, so the table exists,
// and after the role check, before the release takes traffic, so every instance's copy
// lists the same flags; the development seed's rows, applied before it, decide a flag's
// state in development and the test environments.
func MigrateFeatures(ctx context.Context, client resource.Client) error {
	declared := resources.Features()
	if err := resource.MigrateFeatures(ctx, client, declared); err != nil {
		return errors.Wrap(err, "resource.MigrateFeatures()")
	}
	fmt.Printf("Migrated the feature flags: %d declared\n", len(declared))

	return nil
}

// RetireSecret retires one version of the application secret, named by the variable the
// version is handed over in (APP_SECRET_KEY_1): every stored secret field that names the
// version (resources.SecretHolders) is opened and written again under the current
// version, and a final read confirms none is left, which is what makes the version safe
// to disable. It prints how many values of each field it wrote, never a value. The client
// holds the secret keys, built from every enabled version; the current version is
// refused, and a second run writes nothing. The retirement reads the packages named here
// and no other: a resources package the application gains is added to this call, and
// impulse check (secret-holders) fails until it is.
func RetireSecret(ctx context.Context, client resource.Client, variable string, out io.Writer) error {
	retirement, err := resource.RetireSecretVersion(ctx, client, variable, resources.SecretHolders()...)
	if err != nil {
		return errors.Wrap(err, "resource.RetireSecretVersion()")
	}
	for _, field := range retirement.Fields {
		fmt.Fprintf(out, "%s.%s: %d value(s) written again under version %d\n", field.Resource, field.Field, field.Rewritten, retirement.Current)
	}
	fmt.Fprintf(out, "Retired %s: no stored value names version %d\n", retirement.Variable, retirement.Version)

	return nil
}
