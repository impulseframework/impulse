package integration

// The bootstrap-parity helpers: they open the real permission engines over the SHIPPED
// role files (crew.Roles and members.Roles, embedded in the auth packages) and assign the
// SHIPPED personas (cmd/bootstrap/users.json) their memberships over the SHIPPED demo
// world, so what a human sees logging into the running demo is exactly what the suites
// pin; the demo product and the regression suite cannot drift apart.
//
// A persona is two names. Its username is what the suites say (doRequestAs(t, h,
// "booking", ...)) and what a person types; the id of its account is how the permission
// store names it, what a membership is held under, what a row condition's subject binds
// to, and what the demo world's rows carry. The cast file gives every account a fixed
// id, the bootstrap creates the account under it, and the suites read the same file
// (account, memberAccount), so a request made as "booking" runs as the booking agent's
// account.
//
// The engines validate the role files as they open and the store holds no row for a
// default role, so provisioning is the memberships alone. The shared world is still built
// ONCE, for the read-only suites, and every mutating suite gets its own seeded database
// served over the shared engines.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/spannerstore"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/membersidentity"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
)

// demoRoles mirrors the bootstrap's per-persona role assignment shape: where each
// membership is held, the global partition, every sector, or one sector at a time, the
// sector named by its name.
type demoRoles struct {
	Global      []accesstypes.Role            `json:"global"`
	EveryDomain []accesstypes.Role            `json:"everyDomain"`
	Domains     map[string][]accesstypes.Role `json:"domains"`
}

// demoUser is one persona as cmd/bootstrap/users.json declares it: ID is the fixed id
// of its account, and User its username.
type demoUser struct {
	ID       impulse.UUID     `json:"id"`
	User     accesstypes.User `json:"user"`
	Password string           `json:"password"`
	Roles    demoRoles        `json:"roles"`
	Tours    []demoTour       `json:"tours"`
}

// demoTour is a tour of duty as cmd/bootstrap/users.json declares it: the roles a persona
// holds in one sector, named by its name, for a number of days from the day the world is
// made. Each is a role given for a period that ends, with the reason the file gives.
type demoTour struct {
	Domain string             `json:"domain"`
	Roles  []accesstypes.Role `json:"roles"`
	Days   int                `json:"days"`
	Reason string             `json:"reason"`
}

// demoAccount is an identity of the file that has no password: a member of the portal,
// whose roles are the directory's, or a service account with the roles it holds.
type demoAccount struct {
	ID    impulse.UUID     `json:"id"`
	User  accesstypes.User `json:"user"`
	Roles demoRoles        `json:"roles"`
}

// demoGroup is one group of the crew as cmd/bootstrap/users.json declares it: where it is
// kept (a sector by its name, none for the whole fleet), the roles it holds by where each
// is held, the personas in it by their usernames, and the hangar passes it holds.
type demoGroup struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Domain      string             `json:"domain"`
	Roles       demoRoles          `json:"roles"`
	Members     []accesstypes.User `json:"members"`
	Passes      []demoGroupPass    `json:"passes"`
}

// demoGroupPass is one hangar pass a group holds as the file declares it: the fixed id of
// the pass, the hangar by its name in the group's own sector, and the level.
type demoGroupPass struct {
	ID     impulse.UUID `json:"id"`
	Hangar string       `json:"hangar"`
	Level  string       `json:"level"`
}

// demoIdentities mirrors the bootstrap's identities file.
type demoIdentities struct {
	Users           []demoUser    `json:"users"`
	Groups          []demoGroup   `json:"groups"`
	Members         []demoAccount `json:"members"`
	ServiceAccounts []demoAccount `json:"serviceAccounts"`
}

// clientRoles is what the directory assigns Client Cleo: the groups APP_ROLES names in
// development, the global role held in the global partition and the domain role held in
// every sector, as role synchronization writes them. The parity world assigns them
// directly, since no login runs here.
var clientRoles = demoRoles{
	Global:      []accesstypes.Role{"ClientAccount"},
	EveryDomain: []accesstypes.Role{"ClientPortal"},
}

func loadIdentities() (*demoIdentities, error) {
	raw, err := os.ReadFile(usersPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", usersPath, err)
	}
	var identities demoIdentities
	if err := json.Unmarshal(raw, &identities); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", usersPath, err)
	}

	return &identities, nil
}

// The cast's fixed account ids by username, read once from the identities file: the
// crew's (the personas and the service accounts, one users table) and the members'. The
// two are apart because the same username in the two auths is two people (the crew's
// surveyor and the Meridian Survey Office's contact).
var (
	castOnce       sync.Once
	crewAccounts   map[accesstypes.User]impulse.UUID
	memberAccounts map[accesstypes.User]impulse.UUID
)

func loadCast() {
	castOnce.Do(func() {
		identities, err := loadIdentities()
		if err != nil {
			panic(err)
		}
		crewAccounts, memberAccounts = map[accesstypes.User]impulse.UUID{}, map[accesstypes.User]impulse.UUID{}
		for i := range identities.Users {
			crewAccounts[identities.Users[i].User] = identities.Users[i].ID
		}
		for _, account := range identities.ServiceAccounts {
			crewAccounts[account.User] = account.ID
		}
		for _, member := range identities.Members {
			memberAccounts[member.User] = member.ID
		}
	})
}

// accountID is the fixed id of the account of a persona of the cast, named by its
// username: a member's for a portal user, the crew's otherwise. ok is false for a name
// the cast does not carry, a person a suite made up, who has no account.
func accountID(username accesstypes.User) (id impulse.UUID, ok bool) {
	loadCast()
	if portalUsers[username] {
		id, ok = memberAccounts[username]

		return id, ok
	}
	id, ok = crewAccounts[username]

	return id, ok
}

// account is a crew persona as the permission store names it: the id of its account, as
// text. It is what a suite passes where the engine or a row names the person (ForUser,
// AddUserRoles, the value of a column that records who), and it fails the test for a
// name the cast does not carry.
func account(t *testing.T, username accesstypes.User) accesstypes.User {
	t.Helper()

	loadCast()
	id, ok := crewAccounts[username]
	if !ok {
		t.Fatalf("%s names no crew account under the username %q", usersPath, username)
	}

	return accesstypes.User(id.String())
}

// memberAccount is account for a member of the portal.
func memberAccount(t *testing.T, username accesstypes.User) accesstypes.User {
	t.Helper()

	loadCast()
	id, ok := memberAccounts[username]
	if !ok {
		t.Fatalf("%s names no member under the username %q", usersPath, username)
	}

	return accesstypes.User(id.String())
}

// demoUsers lists the personas the bootstrap creates logins for.
func demoUsers(t *testing.T) []demoUser {
	t.Helper()

	identities, err := loadIdentities()
	if err != nil {
		t.Fatal(err)
	}

	return identities.Users
}

// openEngine opens a real engine over one auth's store prefix with the auth's role file,
// validated against the generated collection the way the auth packages open theirs.
func openEngine(db *initiator.SpannerDB, prefix string, roles access.RoleFile) (*access.Client, error) {
	return openEngineWith(db, prefix, roles)
}

// openEngineWith opens an engine over the store with the given prefix and the release's
// role file, with further engine options: the policy-signal suite's change signal and
// heartbeat. The engine is built as the auth's package builds it, with the methods a
// super administrator always executes (the ones the auth's generated management names)
// and the roles that administer a sector, so both do in the suites what they do served.
func openEngineWith(db *initiator.SpannerDB, prefix string, roles access.RoleFile, opts ...access.Option) (*access.Client, error) {
	store, err := spannerstore.New(db.Client, spannerstore.WithPrefix(prefix))
	if err != nil {
		return nil, fmt.Errorf("spannerstore.New(): %w", err)
	}
	built := []access.Option{
		access.WithDefaultRoles(roles, authName(prefix)),
		access.WithSuperAdministratorMethods(superAdministratorMethods(prefix)...),
		access.WithTenantAdministratorRoles(sectorAdministratorRoles(prefix)...),
	}
	client, err := access.New(store, router.Collection(), append(built, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("access.New(): %w", err)
	}

	return client, nil
}

// sectorAdministratorRoles are the roles that administer a sector in the auth whose
// store carries the prefix: the crew's SectorMarshal, and none for the members, whose
// people have no password to come back with.
func sectorAdministratorRoles(prefix string) []accesstypes.Role {
	if prefix == members.TablePrefix {
		return nil
	}
	_, sector := crew.AdministratorRoles()

	return sector
}

// superAdministratorMethods are the methods that give and take the roles of the auth
// whose store carries the prefix: the crew's, and none for the members, whose roles the
// directory gives.
func superAdministratorMethods(prefix string) []accesstypes.Resource {
	if prefix == members.TablePrefix {
		return membersidentity.SuperAdministratorMethods()
	}

	return crewidentity.SuperAdministratorMethods()
}

// authName is the name of the auth a store's prefix belongs to: an engine validates its
// role file as that auth's, against the subject sets that describe its people.
func authName(prefix string) string {
	if prefix == members.TablePrefix {
		return members.Name
	}

	return crew.Name
}

// provisionDemoAccess assigns the personas and the droid their roles in the crew store and
// the client its directory roles in the members store, each held by the id of the
// identity's account as the bootstrap and the directory's sync write them, and waits for
// both snapshots. The roles themselves are the engines' role files; nothing is written
// for them.
func provisionDemoAccess(ctx context.Context, crewEngine, membersEngine *access.Client) error {
	identities, err := loadIdentities()
	if err != nil {
		return err
	}
	var droid, client accesstypes.User
	for i := range identities.Users {
		user := &identities.Users[i]
		if err := assignDemoRoles(ctx, crewEngine, accesstypes.User(user.ID.String()), user.Roles); err != nil {
			return err
		}
		if err := assignDemoTours(ctx, crewEngine, accesstypes.User(user.ID.String()), user.Tours); err != nil {
			return err
		}
	}
	for i := range identities.Groups {
		if err := seedDemoGroup(ctx, crewEngine, &identities.Groups[i]); err != nil {
			return err
		}
	}
	for _, account := range identities.ServiceAccounts {
		held := accesstypes.User(account.ID.String())
		if err := assignDemoRoles(ctx, crewEngine, held, account.Roles); err != nil {
			return err
		}
		if account.User == droidUser {
			droid = held
		}
	}
	for _, member := range identities.Members {
		if member.User == clientUser {
			client = accesstypes.User(member.ID.String())
		}
	}
	if droid == "" || client == "" {
		return fmt.Errorf("%s names no service account %q or no member %q", usersPath, droidUser, clientUser)
	}

	if err := assignDemoRoles(ctx, membersEngine, client, clientRoles); err != nil {
		return err
	}

	if err := waitForDecision(ctx, crewEngine, droid, cinder, accesstypes.Execute, "IngestDroidReports"); err != nil {
		return err
	}

	return waitForDecision(ctx, membersEngine, client, cinder, accesstypes.Execute, "StandDownMission")
}

// seedCastAccounts writes the cast's accounts into a database no auth is open over, each
// under its fixed id as the bootstrap creates it: the personas' and the service
// accounts' in the crew's users table, the members' in the members'. No password is
// written, since nobody signs in to such a world; the rows are there so that what reads
// an account by its id (the generated lists of an auth's people, a method that gives a
// role to an account) finds the cast's as it does in a bootstrapped database.
func seedCastAccounts(ctx context.Context, db *initiator.SpannerDB) error {
	identities, err := loadIdentities()
	if err != nil {
		return err
	}
	insert := func(table string, id impulse.UUID, username accesstypes.User) *spanner.Mutation {
		return spanner.InsertMap(table, map[string]any{"Id": id.String(), "Username": string(username), "CreatedAt": spanner.CommitTimestamp})
	}
	var accounts []*spanner.Mutation
	for i := range identities.Users {
		accounts = append(accounts, insert("CrewSessionUsers", identities.Users[i].ID, identities.Users[i].User))
	}
	for _, account := range identities.ServiceAccounts {
		accounts = append(accounts, insert("CrewSessionUsers", account.ID, account.User))
	}
	for _, member := range identities.Members {
		accounts = append(accounts, insert("MembersSessionUsers", member.ID, member.User))
	}
	if _, err := db.Apply(ctx, accounts); err != nil {
		return fmt.Errorf("writing the cast's accounts: %w", err)
	}

	return nil
}

// assignDemoRoles writes the memberships where each is held: the global partition, every
// sector, then one sector at a time, each seeded sector found by its name
// (sectorsByName), as the bootstrap finds it in the roster. user is the account the roles
// are held by: the id of the identity's account, as text.
func assignDemoRoles(ctx context.Context, client *access.Client, user accesstypes.User, roles demoRoles) error {
	if len(roles.Global) > 0 {
		if err := client.UserManager().AddUserRoles(ctx, accesstypes.GlobalPolicyScope(), user, roles.Global...); err != nil {
			return fmt.Errorf("AddUserRoles(%s, global): %w", user, err)
		}
	}
	if len(roles.EveryDomain) > 0 {
		if err := client.UserManager().AddUserRoles(ctx, accesstypes.EveryDomainPolicyScope(), user, roles.EveryDomain...); err != nil {
			return fmt.Errorf("AddUserRoles(%s, every domain): %w", user, err)
		}
	}
	for name, domainRoles := range roles.Domains {
		domain, ok := sectorsByName[name]
		if !ok {
			return fmt.Errorf("AddUserRoles(%s): no seeded sector is named %q", user, name)
		}
		if err := client.UserManager().AddUserRoles(ctx, accesstypes.DomainPolicyScope(domain), user, domainRoles...); err != nil {
			return fmt.Errorf("AddUserRoles(%s, %s): %w", user, name, err)
		}
	}

	return nil
}

// assignDemoTours gives a persona the roles of each of their tours as the bootstrap gives
// them: in the tour's sector, for the tour's days from now.
func assignDemoTours(ctx context.Context, client *access.Client, user accesstypes.User, tours []demoTour) error {
	for _, tour := range tours {
		domain, ok := sectorsByName[tour.Domain]
		if !ok {
			return fmt.Errorf("AssignUserRole(%s): no seeded sector is named %q", user, tour.Domain)
		}
		period := accesstypes.Period{End: time.Now().UTC().AddDate(0, 0, tour.Days)}
		for _, role := range tour.Roles {
			if err := client.UserManager().AssignUserRole(ctx, accesstypes.DomainPolicyScope(domain), user, role, period, tour.Reason); err != nil {
				return fmt.Errorf("AssignUserRole(%s, %s, %s): %w", user, tour.Domain, role, err)
			}
		}
	}

	return nil
}

// seedDemoGroupPasses writes the hangar passes the file's groups hold, as the bootstrap
// writes them once each group exists: each to the hangar of the group's own sector that
// carries the name, naming the group by the id the permission store made for it. It
// follows provisionDemoAccess, which creates the groups, in the worlds whose suites read
// the passes (the shared world and the served one); a world that leaves it out has the
// groups and none of their passes.
func seedDemoGroupPasses(ctx context.Context, db *initiator.SpannerDB) error {
	identities, err := loadIdentities()
	if err != nil {
		return err
	}
	for i := range identities.Groups {
		group := &identities.Groups[i]
		if len(group.Passes) == 0 {
			continue
		}
		domain, ok := sectorsByName[group.Domain]
		if !ok {
			return fmt.Errorf("group %s holds a hangar pass: no seeded sector is named %q", group.Name, group.Domain)
		}
		for _, pass := range group.Passes {
			var written int64
			if _, err := db.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
				var err error
				written, err = txn.Update(ctx, spanner.Statement{
					SQL: `INSERT INTO HangarPasses (Id, SectorId, HangarId, GroupId, LevelId)
						SELECT @id, h.SectorId, h.Id, g.Id, @level
						FROM Hangars h JOIN CrewGroups g ON g.DomainId = h.SectorId
						WHERE h.SectorId = @sector AND h.Name = @hangar AND g.Name = @group AND g.Kind = 'domain'`,
					Params: map[string]any{"id": pass.ID.String(), "level": pass.Level, "sector": string(domain), "hangar": pass.Hangar, "group": group.Name},
				})
				if err != nil {
					return fmt.Errorf("spanner.ReadWriteTransaction.Update(): %w", err)
				}

				return nil
			}); err != nil {
				return fmt.Errorf("the pass of the group %s to the hangar %s: %w", group.Name, pass.Hangar, err)
			}
			if written != 1 {
				return fmt.Errorf("the pass of the group %s to the hangar %q wrote %d rows, want 1: %s names a hangar of the group's own sector", group.Name, pass.Hangar, written, usersPath)
			}
		}
	}

	return nil
}

// seedDemoGroup creates one group of the crew as the bootstrap creates it: kept where the
// file keeps it, holding its roles where each is held, with its personas in it, each by
// the id of their account.
func seedDemoGroup(ctx context.Context, client *access.Client, group *demoGroup) error {
	manager := client.UserManager()
	sector := func(name string) (accesstypes.PolicyScope, error) {
		domain, ok := sectorsByName[name]
		if !ok {
			return accesstypes.PolicyScope{}, fmt.Errorf("group %s: no seeded sector is named %q", group.Name, name)
		}

		return accesstypes.DomainPolicyScope(domain), nil
	}
	kept := accesstypes.GlobalPolicyScope()
	if group.Domain != "" {
		var err error
		if kept, err = sector(group.Domain); err != nil {
			return err
		}
	}
	id, err := manager.AddGroup(ctx, kept, group.Name, group.Description)
	if err != nil {
		return fmt.Errorf("AddGroup(%s): %w", group.Name, err)
	}
	if len(group.Roles.Global) > 0 {
		if err := manager.AddGroupRoles(ctx, id, accesstypes.GlobalPolicyScope(), group.Roles.Global...); err != nil {
			return fmt.Errorf("AddGroupRoles(%s, global): %w", group.Name, err)
		}
	}
	if len(group.Roles.EveryDomain) > 0 {
		if err := manager.AddGroupRoles(ctx, id, accesstypes.EveryDomainPolicyScope(), group.Roles.EveryDomain...); err != nil {
			return fmt.Errorf("AddGroupRoles(%s, every domain): %w", group.Name, err)
		}
	}
	for name, domainRoles := range group.Roles.Domains {
		scope, err := sector(name)
		if err != nil {
			return err
		}
		if err := manager.AddGroupRoles(ctx, id, scope, domainRoles...); err != nil {
			return fmt.Errorf("AddGroupRoles(%s, %s): %w", group.Name, name, err)
		}
	}
	loadCast()
	people := make([]accesstypes.User, 0, len(group.Members))
	for _, username := range group.Members {
		account, ok := crewAccounts[username]
		if !ok {
			return fmt.Errorf("group %s: %s names no crew account under the username %q", group.Name, usersPath, username)
		}
		people = append(people, accesstypes.User(account.String()))
	}
	if len(people) > 0 {
		if err := manager.AddGroupUsers(ctx, id, people...); err != nil {
			return fmt.Errorf("AddGroupUsers(%s): %w", group.Name, err)
		}
	}

	return nil
}

// waitForDecision blocks until the engine's snapshot reflects the written memberships:
// the store writes signal a reload, but the swap is asynchronous, so it polls the
// last-provisioned identity's authority until it stops being Denied.
func waitForDecision(ctx context.Context, client *access.Client, user accesstypes.User, domain accesstypes.Domain, perm accesstypes.Permission, res accesstypes.Resource) error {
	checker := client.ForUser(user)
	deadline := time.Now().Add(30 * time.Second)
	for {
		decisions, err := checker.Check(ctx, accesstypes.NewEnvironment().WithNow(time.Now()), accesstypes.DomainScope(domain), perm, res)
		if err == nil && !decisions[res].IsDenied() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("policy snapshot never became visible; last decisions for %s: %v (err %w)", user, decisions, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// demoAccessClient returns the shared provisioned crew engine. The demo policy is the same
// for every suite and no suite changes it, so one engine serves them all while each
// mutating suite keeps its own database for the rows it changes.
func demoAccessClient(t *testing.T) *access.Client {
	t.Helper()

	_, _, client := sharedWorld(t)

	return client
}

// newAccessClient opens a crew-store engine over db with the given role file, closed with
// the test.
func newAccessClient(t *testing.T, db *initiator.SpannerDB, roles access.RoleFile) *access.Client {
	t.Helper()

	client, err := openEngine(db, crew.TablePrefix, roles)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("access.Client.Close() error = %v", err)
		}
	})

	return client
}

// alsoDeclared is the permission collection of a release that declared what this one
// does and more: the generated collection, with the names in also listed beside its own,
// each as that release held it, a sector's resource a role may be given the permission
// on, and with the attributes in attributes on the resources that key them, each a text
// a condition may compare. Every other answer is the generated collection's.
type alsoDeclared struct {
	access.PermissionCollection
	also       map[accesstypes.Permission][]accesstypes.Resource
	attributes map[accesstypes.Resource][]string
}

// lists reports whether res is one of the names in also.
func (d alsoDeclared) lists(res accesstypes.Resource) bool {
	for _, resources := range d.also {
		if slices.Contains(resources, res) {
			return true
		}
	}

	return false
}

// Scope is the scope of a sector for a name in also, which a write to a custom role
// holds the role's scope to, and the generated collection's answer for any other name.
func (d alsoDeclared) Scope(res accesstypes.Resource) accesstypes.PermissionScope {
	if d.lists(res) {
		return accesstypes.DomainPermissionScope
	}

	return d.PermissionCollection.Scope(res)
}

// IsResourceImmutable is false for a name in also, whatever this release makes of it:
// the release that declared Update on the name let it be written. It is the generated
// collection's answer for any other name.
func (d alsoDeclared) IsResourceImmutable(scope accesstypes.PermissionScope, res accesstypes.Resource) bool {
	if d.lists(res) {
		return false
	}

	return d.PermissionCollection.IsResourceImmutable(scope, res)
}

// AttributeComparisonType is text for an attribute in attributes, which the release that
// declared it let a grant's condition read, and the generated collection's answer for
// any other.
func (d alsoDeclared) AttributeComparisonType(scope accesstypes.PermissionScope, res accesstypes.Resource, name string) (accesstypes.AttributeType, bool) {
	if slices.Contains(d.attributes[res], name) {
		return accesstypes.AttributeTypeString, true
	}

	return d.PermissionCollection.AttributeComparisonType(scope, res, name)
}

// List is what the generated collection lists, and the names in also under their
// permissions.
func (d alsoDeclared) List() map[accesstypes.Permission][]accesstypes.Resource {
	listed := maps.Clone(d.PermissionCollection.List())
	for permission, resources := range d.also {
		listed[permission] = append(slices.Clone(listed[permission]), resources...)
	}

	return listed
}

// engineOfAReleaseThatDeclared opens an engine over the crew's store for a release that
// declared what this one does and the names in also beside it. An engine cannot be built
// without what its release declares, and the release's own engine refuses to write a
// role a grant the release does not declare, so a test that needs one in the store
// writes it through this engine, as the release that declared the name would have left
// it there. The engine is handed no role file: it writes a custom role's rows and
// answers nothing. It is closed when the test ends.
func engineOfAReleaseThatDeclared(t *testing.T, db *initiator.SpannerDB, also map[accesstypes.Permission][]accesstypes.Resource) *access.Client {
	t.Helper()

	return engineOfARelease(t, db, alsoDeclared{PermissionCollection: router.Collection(), also: also})
}

// engineOfAReleaseWithAttributes is engineOfAReleaseThatDeclared for a release whose
// resources declared attributes this one's do not: the attributes named, each on the
// resource that keys it. A condition that reads one is refused by the release's own
// engine, so a test that needs one in the store writes it through this engine.
func engineOfAReleaseWithAttributes(t *testing.T, db *initiator.SpannerDB, attributes map[accesstypes.Resource][]string) *access.Client {
	t.Helper()

	return engineOfARelease(t, db, alsoDeclared{PermissionCollection: router.Collection(), attributes: attributes})
}

// engineOfARelease opens an engine over the crew's store for the release whose
// collection is given, handed no role file, and closes it when the test ends.
func engineOfARelease(t *testing.T, db *initiator.SpannerDB, collection access.PermissionCollection) *access.Client {
	t.Helper()

	store, err := spannerstore.New(db.Client, spannerstore.WithPrefix(crew.TablePrefix))
	if err != nil {
		t.Fatalf("spannerstore.New() error = %v", err)
	}
	client, err := access.New(store, collection)
	if err != nil {
		t.Fatalf("access.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("access.Client.Close() error = %v", err)
		}
	})

	return client
}

// demoWorld prepares a fresh seeded database and the application over it, with the shared
// provisioned engines, for suites that MUTATE the seeded rows. Read-only suites use
// sharedWorld instead.
func demoWorld(t *testing.T) (context.Context, *initiator.SpannerDB, http.Handler) {
	t.Helper()

	ctx := t.Context()
	db, err := prepareDatabase(ctx, t, migrationsSource, demoSeedSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedCastAccounts(ctx, db); err != nil {
		t.Fatal(err)
	}
	sharedWorld(t)

	sharedMu.RLock()
	defer sharedMu.RUnlock()

	return ctx, db, newTestAppWithEngines(db, sharedCrew, sharedMembers)
}

// The shared world: one seeded database and one pair of provisioned engines for every
// read-only bootstrap-parity suite in the binary, built on first use and torn down by
// TestMain after the run.
var (
	sharedOnce    sync.Once
	sharedDB      *initiator.SpannerDB
	sharedCrew    *access.Client
	sharedMembers *access.Client
	sharedApp     http.Handler
	sharedErr     error
	// sharedMu guards the published engines: membersEngineFor reads them from tests that
	// never enter sharedWorld, concurrently with the once-guarded write.
	sharedMu sync.RWMutex
)

// sharedWorld returns the shared seeded database, its application over the real engines,
// and the crew engine itself. Suites using it must not mutate seeded rows.
func sharedWorld(t *testing.T) (*initiator.SpannerDB, http.Handler, *access.Client) {
	t.Helper()

	sharedOnce.Do(func() {
		ctx := context.Background()
		db, err := container.CreateDatabase(ctx, "shared-demo-world")
		if err != nil {
			sharedErr = fmt.Errorf("initiator.SpannerContainer.CreateDatabase(): %w", err)

			return
		}
		sharedDB = db
		if err := db.MigrateUp(migrationsSource, demoSeedSource); err != nil {
			sharedErr = fmt.Errorf("initiator.SpannerDB.MigrateUp(): %w", err)

			return
		}
		// The demo seed writes the Corvid mission's deadline three minutes after the seed, so
		// the overseer's grant flips during a live walkthrough. This world lives for the whole
		// run, which is longer than three minutes on a small runner, and its suites pin the
		// mission as not yet overdue: here its deadline is a day ahead, still the first one to
		// come (the seed's other open deadlines are weeks out).
		if _, err := db.Apply(ctx, []*spanner.Mutation{
			spanner.UpdateMap("Missions", map[string]any{"Id": missionCorvidID, "Deadline": time.Now().UTC().AddDate(0, 0, 1)}),
		}); err != nil {
			sharedErr = fmt.Errorf("holding the Corvid mission's deadline ahead: %w", err)

			return
		}
		if err := seedCastAccounts(ctx, db); err != nil {
			sharedErr = err

			return
		}
		crewEngine, err := openEngine(db, crew.TablePrefix, crew.Roles())
		if err != nil {
			sharedErr = err

			return
		}
		membersEngine, err := openEngine(db, members.TablePrefix, members.Roles())
		if err != nil {
			sharedErr = err

			return
		}
		if err := provisionDemoAccess(ctx, crewEngine, membersEngine); err != nil {
			sharedErr = err

			return
		}
		if err := seedDemoGroupPasses(ctx, db); err != nil {
			sharedErr = err

			return
		}
		handler := newTestAppWithEngines(db, crewEngine, membersEngine)

		// Published together, under the lock membersEngineFor reads them with.
		sharedMu.Lock()
		sharedCrew, sharedMembers, sharedApp = crewEngine, membersEngine, handler
		sharedMu.Unlock()
	})
	if sharedErr != nil {
		t.Fatalf("shared demo world: %v", sharedErr)
	}

	sharedMu.RLock()
	defer sharedMu.RUnlock()

	return sharedDB, sharedApp, sharedCrew
}

// closeSharedWorld tears the shared world down; TestMain calls it after m.Run.
func closeSharedWorld() {
	if sharedCrew != nil {
		_ = sharedCrew.Close()
	}
	if sharedMembers != nil {
		_ = sharedMembers.Close()
	}
	if sharedDB != nil {
		_ = sharedDB.DropDatabase(context.Background())
		_ = sharedDB.Close()
	}
}
