// Package main bootstraps the Lodestar demo database: it creates the database where it is
// missing, runs the deployment's migration steps (the schema, then the role check), seeds
// the demo world, and creates the demo personas. The target is the environment's, as it is
// for the Spanner client library: with SPANNER_EMULATOR_HOST set it is the emulator the
// Procfile starts, whose instance the bootstrap also creates; without it, the project the
// application credentials reach, whose instance must already exist. A database that
// already exists is refused unless -reset is given, which empties its data and seeds it
// again without touching the schema.
//
// The order matters: the demo world is seeded before the data configuration opens because
// the tenant roster is read from the Sectors table at startup. Tenancy is data, not a
// compiled-in list. The roles need no step of their own: each auth's default roles travel
// with the binary and are validated when the auth opens, and the role check only reports
// what the store holds that the release cannot use.
//
// The cast has fixed account ids. The permission store names a person by the id of their
// account, and so does every row of the demo world that says whose it is or who wrote it
// (schema/devseed), so each account is created under the id the cast file gives it
// (session.CreateUserRequest.ID) and holds its roles under that id: a development
// database reads the same on every machine.
//
// Demonstrates: bootstrap.target, bootstrap.reset.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"maps"
	"os"
	"os/signal"
	"slices"
	"time"

	"cloud.google.com/go/spanner"
	databaseadmin "cloud.google.com/go/spanner/admin/database/apiv1"
	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	instanceadmin "cloud.google.com/go/spanner/admin/instance/apiv1"
	"cloud.google.com/go/spanner/admin/instance/apiv1/instancepb"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
	"github.com/impulseframework/impulse/lodestar/pkg/config"
	"github.com/impulseframework/impulse/lodestar/pkg/deploy"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"google.golang.org/grpc/codes"
)

// usersPath is the committed demo cast: the crew personas with plaintext passwords and
// their role assignments, the members the demo world's rows name (the portal's people,
// who sign in through the directory and so get no password here), and the service
// accounts the droids outlet's tokens bind requests to, each with the fixed id its
// account is created under. The plaintext passwords are deliberate: these are fictional
// demo credentials for an application that is never published, whether its database is
// the emulator or a private test instance, and the README says so.
const usersPath = "cmd/bootstrap/users.json"

// target is where the bootstrap's database lives.
type target string

const (
	emulatorTarget target = "the Spanner emulator"
	projectTarget  target = "the project the application credentials reach"
)

// devIdentities is the file's shape.
type devIdentities struct {
	Users []devUser `json:"users"`
	// Groups are the crew's groups of people: a group holds roles the way a persona does,
	// and a persona in it holds them wherever the group does. The deckhand holds no role
	// in a sector of their own: what they may do in Anvil is the deck crew's.
	Groups []devGroup `json:"groups"`
	// Members are the portal's people whom a row of the demo world names: the contact a
	// client company's record belongs to, the client who filed a distress call. The
	// directory proves who they are and names their roles at every sign-in (under the
	// skipAuth build tag, APP_USERNAME and APP_ROLES do), so a member here is an account
	// without a password and without roles, under the name the directory presents, which
	// the member's first sign-in links to.
	Members []devMember `json:"members"`
	// ServiceAccounts are machine identities: they hold roles like any user but get no
	// login. The droids outlet's token check binds requests to them through the
	// application token Start seeds from the configured key.
	ServiceAccounts []devServiceAccount `json:"serviceAccounts"`
}

// devRoles is an identity's role assignments by where each is held: the global
// partition, every sector, and one sector at a time, each its own key, mirroring
// accesstypes.PolicyScope. A role held in every sector reaches a sector created after
// the bootstrap too. A single sector is named by its name, since its key is an id
// nobody types: the bootstrap finds the sector that carries the name
// (resource.TenantRoster.Named) and refuses a name no sector or more than one sector
// carries.
type devRoles struct {
	Global      []accesstypes.Role            `json:"global"`
	EveryDomain []accesstypes.Role            `json:"everyDomain"`
	Domains     map[string][]accesstypes.Role `json:"domains"`
}

// devUser is one persona: a crew login and its roles. ID is the fixed id its account is
// created under, which is how the permission store names the persona and what the demo
// world's rows carry for them; User is its username, which is what people read and type.
type devUser struct {
	ID       impulse.UUID `json:"id"`
	User     string       `json:"user"`
	Password string       `json:"password"`
	Roles    devRoles     `json:"roles"`
	// Tours are the roles the persona holds for a time: the relief officer is on a tour
	// of Anvil, and holds nothing there once it is over.
	Tours []devTour `json:"tours"`
}

// devTour is a tour of duty: the roles a persona holds in one sector, named by its name,
// for a number of days from the bootstrap. Each is a role given for a period that ends
// (accesstypes.Period): it counts until its end and for nothing after, whether or not
// its row still stands, and the row goes when the persona next signs in.
//
// Demonstrates: auth.periods.
type devTour struct {
	Domain string             `json:"domain"`
	Roles  []accesstypes.Role `json:"roles"`
	Days   int                `json:"days"`
}

// devGroup is one group of the crew: its name and description, the sector it is kept in
// by the sector's name (none keeps it for the whole fleet), the roles it holds by where
// each is held, and the personas in it by their usernames.
//
// Demonstrates: auth.groups.
type devGroup struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Domain      string   `json:"domain"`
	Roles       devRoles `json:"roles"`
	Members     []string `json:"members"`
}

// devMember is one of the portal's people the demo world names: ID is the fixed id of
// the account, and User the username the directory will present for them.
type devMember struct {
	ID   impulse.UUID `json:"id"`
	User string       `json:"user"`
}

// devServiceAccount is one machine identity: an account without a password, under its
// fixed id, and the roles it holds. The server finds the account by its username when
// it seeds the account's token (crew.Handlers.SeedToken).
//
// Demonstrates: outlet.tokens.
type devServiceAccount struct {
	ID    impulse.UUID `json:"id"`
	User  string       `json:"user"`
	Roles devRoles     `json:"roles"`
}

func main() {
	reset := flag.Bool("reset", false, "empty the existing database's data and seed it again; the schema stays")
	flag.Parse()

	if err := run(context.Background(), *reset); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, reset bool) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	settings, err := config.LoadDatabaseSettings(ctx)
	if err != nil {
		return errors.Wrap(err, "config.LoadDatabaseSettings()")
	}

	where := targetOf(settings.EmulatorHost)
	fmt.Printf("Bootstrapping %s in %s\n", settings.DatabasePath(), where)

	if where == emulatorTarget {
		if err := ensureInstance(ctx, settings); err != nil {
			return err
		}
	}

	existed, err := ensureDatabase(ctx, settings)
	if err != nil {
		return err
	}
	if existed && !reset {
		return errors.Newf("database %s already exists: run with -reset to empty its data and seed it again, or drop it first", settings.DatabasePath())
	}

	// From here the bootstrap runs the deployment's own steps.
	if err := deploy.MigrateSchema(ctx, settings); err != nil {
		return errors.Wrap(err, "deploy.MigrateSchema()")
	}
	fmt.Println("Applied the schema migrations")

	if existed {
		if err := resetData(ctx, settings); err != nil {
			return err
		}
		fmt.Println("Emptied the database's data; the schema stays")
	}

	// The file stores are emptied before the seed (files.go): no row holds a file yet.
	if err := emptyFileStores(ctx); err != nil {
		return err
	}

	if err := deploy.SeedDevelopmentData(ctx, settings); err != nil {
		return errors.Wrap(err, "deploy.SeedDevelopmentData()")
	}
	fmt.Printf("Seeded the demo world from %s\n", deploy.DevSeedSource)

	data, err := config.NewDataConfiguration(ctx)
	if err != nil {
		return errors.Wrap(err, "config.NewDataConfiguration()")
	}
	defer data.Close()

	// Each auth's default roles are the binary's, validated when the auth opened; the
	// check reports the role file's warnings and anything the store holds that this
	// release cannot use. The members roles say what a client may do; who holds them is
	// the directory's business (RoleSync), so no member's role is seeded here, and in
	// development APP_ROLES names the groups every simulated login is in.
	if err := deploy.CheckRoles(ctx, data.Crew().Access(), crew.Name); err != nil {
		return errors.Wrap(err, "deploy.CheckRoles(crew)")
	}
	if err := deploy.CheckRoles(ctx, data.Members().Access(), members.Name); err != nil {
		return errors.Wrap(err, "deploy.CheckRoles(members)")
	}

	// The feature flags the binary declares, written off where the table has no row
	// for them, as the deploy writes them.
	if err := deploy.MigrateFeatures(ctx, data.ResourceClient()); err != nil {
		return errors.Wrap(err, "deploy.MigrateFeatures()")
	}

	if err := seedIdentities(ctx, data); err != nil {
		return errors.Wrap(err, "seedIdentities()")
	}

	return nil
}

// targetOf picks the target the way the Spanner client library does: an emulator host in
// the database settings (SPANNER_EMULATOR_HOST, the variable the library reads too) means
// the emulator, none means the real project.
func targetOf(emulatorHost string) target {
	if emulatorHost != "" {
		return emulatorTarget
	}

	return projectTarget
}

// ensureInstance creates the emulator's instance when it is missing. A real instance is
// never created here: db-initiator's instance call carries no configuration or size, and
// a real one is provisioned by whoever owns the project.
func ensureInstance(ctx context.Context, settings config.DatabaseSettings) error {
	admin, err := instanceadmin.NewInstanceAdminClient(ctx)
	if err != nil {
		return errors.Wrap(err, "instance.NewInstanceAdminClient()")
	}
	defer admin.Close()

	name := fmt.Sprintf("projects/%s/instances/%s", settings.ProjectID, settings.InstanceID)
	_, err = admin.GetInstance(ctx, &instancepb.GetInstanceRequest{Name: name})
	switch {
	case err == nil:
		fmt.Printf("Instance %s exists\n", settings.InstanceID)

		return nil
	case spanner.ErrCode(err) != codes.NotFound:
		return errors.Wrapf(err, "instance.InstanceAdminClient.GetInstance(): %s", name)
	}

	if err := initiator.NewSpannerInstance(ctx, settings.ProjectID, settings.InstanceID); err != nil {
		return errors.Wrapf(err, "initiator.NewSpannerInstance(): %s", settings.InstanceID)
	}
	fmt.Printf("Created instance %s\n", settings.InstanceID)

	return nil
}

// ensureDatabase creates the database when it is missing and reports whether it already
// existed.
func ensureDatabase(ctx context.Context, settings config.DatabaseSettings) (bool, error) {
	admin, err := databaseadmin.NewDatabaseAdminClient(ctx)
	if err != nil {
		return false, errors.Wrap(err, "database.NewDatabaseAdminClient()")
	}
	defer admin.Close()

	_, err = admin.GetDatabase(ctx, &databasepb.GetDatabaseRequest{Name: settings.DatabasePath()})
	switch {
	case err == nil:
		fmt.Printf("Database %s exists\n", settings.DatabaseName)

		return true, nil
	case spanner.ErrCode(err) != codes.NotFound:
		return false, errors.Wrapf(err, "database.DatabaseAdminClient.GetDatabase(): %s", settings.DatabasePath())
	}

	db, err := initiator.NewSpannerDatabase(ctx, settings.ProjectID, settings.InstanceID, settings.DatabaseName)
	if err != nil {
		return false, errors.Wrapf(err, "initiator.NewSpannerDatabase(): %s", settings.DatabaseName)
	}
	if err := db.Close(); err != nil {
		return false, errors.Wrap(err, "initiator.SpannerDB.Close()")
	}
	fmt.Printf("Created database %s\n", settings.DatabaseName)

	return false, nil
}

// resetData empties the existing database's data through the deploy package, over a
// client of its own: the data level's clients are not open yet.
func resetData(ctx context.Context, settings config.DatabaseSettings) error {
	client, err := spanner.NewClient(ctx, settings.DatabasePath())
	if err != nil {
		return errors.Wrap(err, "spanner.NewClient()")
	}
	defer client.Close()

	if err := deploy.ResetDevelopmentData(ctx, client, deploy.MigrationsSource); err != nil {
		return errors.Wrap(err, "deploy.ResetDevelopmentData()")
	}

	return nil
}

// seedIdentities creates the cast's accounts, each under the fixed id the file gives it,
// and assigns the roles each holds under that id: the crew logins with their passwords,
// the members without one (their roles are the directory's, written at their sign-in),
// and the service accounts without one. The account comes first: a role is held by an
// account. The crew's groups follow its logins, each with its roles and its members.
func seedIdentities(ctx context.Context, data *config.DataConfiguration) error {
	raw, err := os.ReadFile(usersPath)
	if err != nil {
		return errors.Wrapf(err, "os.ReadFile(%q)", usersPath)
	}
	var identities devIdentities
	if err := json.Unmarshal(raw, &identities); err != nil {
		return errors.Wrapf(err, "json.Unmarshal(%q)", usersPath)
	}

	crewAPI := data.Crew().Session().API()
	accounts := make(map[string]accesstypes.User, len(identities.Users))
	for i := range identities.Users {
		user := &identities.Users[i]
		id, err := createAccount(ctx, crewAPI, &session.CreateUserRequest{Username: user.User, Password: &user.Password}, user.ID)
		if err != nil {
			return err
		}
		fmt.Printf("Created login %s (%s)\n", user.User, id)
		accounts[user.User] = accesstypes.User(id.String())

		if err := assignRoles(ctx, data.UserManager(), data.TenantRoster(), user.User, accesstypes.User(id.String()), user.Roles); err != nil {
			return err
		}
		if err := assignTours(ctx, data.UserManager(), data.TenantRoster(), user.User, accesstypes.User(id.String()), user.Tours); err != nil {
			return err
		}
	}

	for i := range identities.Groups {
		if err := seedGroup(ctx, data.UserManager(), data.TenantRoster(), &identities.Groups[i], accounts); err != nil {
			return err
		}
	}

	membersAPI := data.Members().Session().API()
	for _, member := range identities.Members {
		id, err := createAccount(ctx, membersAPI, &session.CreateUserRequest{Username: member.User}, member.ID)
		if err != nil {
			return err
		}
		fmt.Printf("Created member %s (%s)\n", member.User, id)
	}

	for _, account := range identities.ServiceAccounts {
		id, err := createAccount(ctx, crewAPI, &session.CreateUserRequest{Username: account.User}, account.ID)
		if err != nil {
			return err
		}
		fmt.Printf("Created service account %s (%s)\n", account.User, id)

		if err := assignRoles(ctx, data.UserManager(), data.TenantRoster(), account.User, accesstypes.User(id.String()), account.Roles); err != nil {
			return err
		}
	}

	return nil
}

// assignTours gives a persona the roles of each of their tours, in the tour's sector, for
// the tour's days from now: a role given for a period, which nobody has to remember to
// take away.
func assignTours(ctx context.Context, manager access.UserManager, sectors *resource.TenantRoster, login string, user accesstypes.User, tours []devTour) error {
	for i := range tours {
		tour := &tours[i]
		domain, err := sectors.Named(tour.Domain)
		if err != nil {
			return errors.Wrapf(err, "resource.TenantRoster.Named(): user %s", login)
		}
		if tour.Days <= 0 {
			return errors.Newf("%s: the tour of %s in %s lasts %d days: a tour lasts at least one", usersPath, login, tour.Domain, tour.Days)
		}
		ends := time.Now().UTC().AddDate(0, 0, tour.Days)
		for _, role := range tour.Roles {
			if err := manager.AssignUserRole(ctx, accesstypes.DomainPolicyScope(domain), user, role, accesstypes.Period{End: ends}); err != nil {
				return errors.Wrapf(err, "access.UserManager.AssignUserRole(): user %s in the sector %s (%s)", login, tour.Domain, domain)
			}
		}
		fmt.Printf("Assigned %v to %s in the sector %s (%s) until %s\n", tour.Roles, login, tour.Domain, domain, ends.Format(time.RFC3339))
	}

	return nil
}

// seedGroup creates one group of the crew where the file keeps it, gives it its roles
// where each is held, and puts its personas in it, each by the account the bootstrap
// created for them. The group comes first: a role and a member each name a group.
func seedGroup(ctx context.Context, manager access.UserManager, sectors *resource.TenantRoster, group *devGroup, accounts map[string]accesstypes.User) error {
	kept := accesstypes.GlobalPolicyScope()
	if group.Domain != "" {
		domain, err := sectors.Named(group.Domain)
		if err != nil {
			return errors.Wrapf(err, "resource.TenantRoster.Named(): group %s", group.Name)
		}
		kept = accesstypes.DomainPolicyScope(domain)
	}
	id, err := manager.AddGroup(ctx, kept, group.Name, group.Description)
	if err != nil {
		return errors.Wrapf(err, "access.UserManager.AddGroup(): group %s", group.Name)
	}
	fmt.Printf("Created group %s (%s)\n", group.Name, id)

	if len(group.Roles.Global) > 0 {
		if err := manager.AddGroupRoles(ctx, id, accesstypes.GlobalPolicyScope(), group.Roles.Global...); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddGroupRoles(): group %s in the global scope", group.Name)
		}
		fmt.Printf("Gave %v to the group %s in the global scope\n", group.Roles.Global, group.Name)
	}
	if len(group.Roles.EveryDomain) > 0 {
		if err := manager.AddGroupRoles(ctx, id, accesstypes.EveryDomainPolicyScope(), group.Roles.EveryDomain...); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddGroupRoles(): group %s in every domain", group.Name)
		}
		fmt.Printf("Gave %v to the group %s in every domain\n", group.Roles.EveryDomain, group.Name)
	}
	for _, name := range slices.Sorted(maps.Keys(group.Roles.Domains)) {
		domain, err := sectors.Named(name)
		if err != nil {
			return errors.Wrapf(err, "resource.TenantRoster.Named(): group %s", group.Name)
		}
		domainRoles := group.Roles.Domains[name]
		if err := manager.AddGroupRoles(ctx, id, accesstypes.DomainPolicyScope(domain), domainRoles...); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddGroupRoles(): group %s in the sector %s (%s)", group.Name, name, domain)
		}
		fmt.Printf("Gave %v to the group %s in the sector %s (%s)\n", domainRoles, group.Name, name, domain)
	}

	people := make([]accesstypes.User, 0, len(group.Members))
	for _, login := range group.Members {
		account, ok := accounts[login]
		if !ok {
			return errors.Newf("%s: the group %s names the member %q, and the file carries no user of that name", usersPath, group.Name, login)
		}
		people = append(people, account)
	}
	if len(people) > 0 {
		if err := manager.AddGroupUsers(ctx, id, people...); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddGroupUsers(): group %s", group.Name)
		}
		fmt.Printf("Put %v in the group %s\n", group.Members, group.Name)
	}

	return nil
}

// accountCreator is the part of an auth's session API the bootstrap creates accounts
// through; the crew's and the members' both answer it.
type accountCreator interface {
	CreateSessionUser(ctx context.Context, req *session.CreateUserRequest, customData ...*session.NoCustomData) (impulse.UUID, error)
}

// createAccount creates one account of the cast under its fixed id and answers the id.
// An entry without one is refused: the demo world's rows name the cast by these ids, so
// an account under a generated id would be one no row belongs to.
func createAccount(ctx context.Context, api accountCreator, req *session.CreateUserRequest, id impulse.UUID) (impulse.UUID, error) {
	if id.IsNil() {
		return impulse.NilUUID, errors.Newf("%s: %s carries no id: every account of the cast is created under a fixed id, which the demo world's rows name it by", usersPath, req.Username)
	}
	req.ID = impulse.NullUUIDFromUUID(id)
	created, err := api.CreateSessionUser(ctx, req)
	if err != nil {
		return impulse.NilUUID, errors.Wrapf(err, "session.AuthAPI.CreateSessionUser(): %s", req.Username)
	}

	return created, nil
}

// assignRoles writes the identity's memberships where each is held: the global partition
// first, then every sector, then each single sector in the order of the names, each
// found by its name in the roster. user is the account the roles are held by, the id of
// the identity's account, and login its username, which the lines printed read.
func assignRoles(ctx context.Context, manager access.UserManager, sectors *resource.TenantRoster, login string, user accesstypes.User, roles devRoles) error {
	if len(roles.Global) > 0 {
		if err := manager.AddUserRoles(ctx, accesstypes.GlobalPolicyScope(), user, roles.Global...); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddUserRoles(): user %s in the global scope", login)
		}
		fmt.Printf("Assigned %v to %s in the global scope\n", roles.Global, login)
	}
	if len(roles.EveryDomain) > 0 {
		if err := manager.AddUserRoles(ctx, accesstypes.EveryDomainPolicyScope(), user, roles.EveryDomain...); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddUserRoles(): user %s in every domain", login)
		}
		fmt.Printf("Assigned %v to %s in every domain\n", roles.EveryDomain, login)
	}
	for _, name := range slices.Sorted(maps.Keys(roles.Domains)) {
		domain, err := sectors.Named(name)
		if err != nil {
			return errors.Wrapf(err, "resource.TenantRoster.Named(): user %s", login)
		}
		domainRoles := roles.Domains[name]
		if err := manager.AddUserRoles(ctx, accesstypes.DomainPolicyScope(domain), user, domainRoles...); err != nil {
			return errors.Wrapf(err, "access.UserManager.AddUserRoles(): user %s in the sector %s (%s)", login, name, domain)
		}
		fmt.Printf("Assigned %v to %s in the sector %s (%s)\n", domainRoles, login, name, domain)
	}

	return nil
}
