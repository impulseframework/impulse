# outlets

A tenant-scoped application on the Impulse Framework with every outlet kind on one
site. One hostname, three outlets — the console on `/console/api` with its browser
application at `/console`, a second browser application (the portal) on `/portal/api`
behind the same session handling with its application at `/portal`, and a machine REST
API on `/devices` behind bearer tokens the staff auth issues: a staff login issues an
application token for the outlet on the console's API keys page (Your API keys in the
person's menu, `/console/api-keys`), or
approves a command-line client's device authorization on its device page
(`/console/device`, the page the staff auth names as its `DevicePage`), and the token
acts as them, with their roles, through the same permission checks as the browser. Every browser outlet's API sits under its application's mount path, and no
application is mounted at `/`: an installed browser application's scope is every URL under
its start, so one at `/` would own the origin and the other would never get its own install
prompt. The root alone redirects to `/console/`; every other unmatched path is 404.
Tenancy as data: the `Tenant` record (`@tenant`) over the `Tenants` table is the domain
universe, tenant-scoped routes live under `/console/api/tenants/{tenantID}/` (and under each
outlet's prefix), the data level's tenant roster keeps the tenants current on every
instance (a tenant is usable the moment it is made, without a restart), and
tenant existence is concealed from logins that hold nothing there. `Announcement` is the first tenant-scoped resource — a login's tenant list
is where it holds a grant, and a grant needs a tenant-scoped resource to land on, so the
application ships one. `Reading` is devices-only. The console and the portal each mirror
the API's shape: every tenant-scoped page lives under the tenant segment,
`/tenants/{tenantID}/...` (the generated `DomainRoutePath`), so a link to a tenant's page
can be shared and a bookmark lands where it points, and the global pages keep their routes.
Each sits in the library's frame (`provideFrame` in `app.config.ts`). The tenant a page
works in is a consequence of its URL: the library's tenant guard on the segment opens the
tenant the URL names before the page loads, checked against the session's tenant list,
and nothing else writes it; the place switcher beside the application's name opens the
same page under the chosen tenant, and a URL naming a tenant the session cannot reach
lands on the refusal page (`/tenants/{tenantID}/refused`), which shows the server's
answer. The dashboard is every place's first page, and each application manages its
auth's people in both places the same way: the whole application's accounts
(`/staff-accounts` on the console, `/members-accounts` on the portal), a tenant's people
under the tenant (`/tenants/{tenantID}/staff-people`, `/tenants/{tenantID}/members-people`).
Groups of people are kept beside them (`/staff-groups` on the console,
`/tenants/{tenantID}/staff-groups` and `/tenants/{tenantID}/members-groups` under the
tenant): a group holds roles the way a person does, a person in it holds the group's
roles, and each auth's role file gives a place's groups, their members and their roles to
whoever manages its people (`Administrator` for the whole application,
`TenantAdministrator` in a tenant). A group gives
nobody a power that giving the role directly would not: who is in a group, and what it
holds, is changed by someone who may give or take those roles directly, and a tenant puts
its own people in its groups, while an account from outside the tenant is put there from
the whole application's accounts. The list of groups says how many people each group has and the roles it
holds, and offers Delete only on a group that holds nothing; a person's page lists their
roles in one list that says "Their own" or names the group each comes through. On the
Break glass page (`/staff-break-glass`) a person who may give themselves a role takes one
in a tenant for a set time and says why: the role ends by itself, the tenant's
administrator reads the reason beside it and may end it early, and the role file gives
the page's lists and its method to the staff auth's `Administrator`. The library draws them from the API description (`identityRoutes` in each application's
`app.routes.ts`, placed by `identityMenuPages` in its menu file), and a tenant's pages read
the tenant from the URL (the frame provides `RESOURCE_DOMAIN`). The console's menu file
(`app.menus.ts`) shows Tenants (All tenants) and System (the staff's accounts, the
Invited tenants page and Feature flags…) in Configuration, and People and Tenant (Sign-in settings)
inside a tenant; the portal's shows System (the members' accounts) and People. The
person's own menu holds Your account (the console's: the email address, Change your
password, Forget this browser), Your API keys on the console, Appearance and Sign out; a
portal member signs in through their company's directory and has nothing of their sign-in
to change there. Help ends with About, which carries the release.

How a tenant comes to exist is apart from who may enter one. Nobody creates a tenant
directly: the record's create is refused, with a sentence that says so. An administrator
brings an organization in by inviting one person, on the console's Invited tenants page
(System), which the library draws from the staff auth's generated `InviteStaffTenant`,
`StaffInvitedTenants` and `WithdrawStaffInvitedTenant`. Sending the invitation takes the
`Administrator` role's Create grant on the record, and a name a tenant holds is refused
there. No tenant exists while the invitation waits; the invited person's first sign-in
makes it under an id the server generates, gives them the roles the App names in
`TenantAdministratorRoles` (`TenantAdministrator`) and hands the tenant to the roster, so
their next request stands in it. An invitation that still waits is withdrawn on the same
page. The staff auth is handed what makes the tenant by the data level
(`staff.Settings.TenantMaker`, the generated `app.NewTenantMaker` over the roster, built before the
auth), and refuses to start without it. A person who signed up and holds no tenant
creates their own through the generated create-your-tenant route, which the dashboard
offers while an administrator has a sign-in method open to sign-up.

Who may enter a tenant is the record's declaration, data of the application rather than
deploy configuration: the tenant's directory, the organization its people sign in
through (`TenantDirectory`, one row at most under `/console/api/tenants/{tenantID}/tenant-directories`:
a sign-in method and the connection its identities carry, a Microsoft Entra tenant by its
tenant id or a Google Workspace by its domain) and whether its people enter through it
only (`Tenant.DirectoryOnly`, which closes the tenant to every sign-in but one through
its directory). A tenant has one directory and a
directory belongs to one tenant: the table's two unique indexes hold both, and the
generated write paths refuse first, each with a sentence the person can act on. Both
auths' sign-in rules are the
library's tenant policy over it (`signin.TenantPolicy`, handed the generated
`app.NewTenantDeclarationReader`, which reads the declaration in the sign-in's own
transaction): a tenant's declaration restricts its own door alone, so a tenant that is
not directory only admits everyone it seats, however they signed in, a directory-only
tenant admits a sign-in through its own directory and, of its own administrators alone, a
password with a code sent to their email, and nothing else, and a person is
refused only when every tenant that seats them is closed to the way they signed in (the
portal's members come through the Entra directory the members auth registers, which the
development seed makes North's, and reach South as well while South is not directory
only). A super administrator is admitted whatever a tenant declares, an administrator of a
directory-only tenant (a holder of `TenantAdministrator` in that tenant itself, by a role
of their own or through one of the tenant's own groups, never one held in every tenant)
who signs in with a password is held for the code sent to their email and then enters
that tenant, which is the tenant's way back in when its directory has a problem (who
holds that role in a directory-only tenant, and the account of a person who does, is
changed only by a caller whose own session the tenant admits or by a super administrator,
so the application's own staff do not give themselves the way in), a person
holding a
role outside any one tenant (a global role, or one held in every tenant) is admitted and
then enters a tenant only where its declaration admits their session, and a person
holding no tenant is refused. Which tenants the session
then reaches is decided again per tenant where its tenant list is computed, and a tenant
that seats the person and is closed to their session stays in the list marked closed,
with the reason: the data
level's roster keeps every tenant's declaration beside its key
(`resource.TenantRoster.Declarations`, handed to `resource.SessionPermissions`), the
records' write paths reload it after their commit and signal the other instances, so a
declaration decides the next request, and the records' change events are the audit of
every edit. The console's tenant pages carry the declaration at
`/tenants/{tenantID}/sign-in`: the tenant's administrator sees the tenant's directory
there and removes it, and changes everything else with the tenant's own generated
methods, which `TenantAdministrator` holds and the page runs through the library's
method dialog: `OpenTenantToDirectory`
(`POST /console/api/tenants/{tenantID}/open-tenant-to-directory`) names the role each of
the directory's people is given at their first sign-in through it, a role the caller
could give directly, picked from the list of the roles they may give
(`StaffTenantDirectoryRoles`), and `MakeTenantInviteOnly` clears it; `MakeTenantDirectoryOnly`
(`POST /console/api/tenants/{tenantID}/make-tenant-directory-only`) and
`EndTenantDirectoryOnly` turn directory only (the `Tenants` record shows the flag and
takes no write of it), which the server turns on only once the tenant has its directory
and only for a person signed in through it who has a password of their own, their way
back in if the directory has a problem. Opening the tenant and making it directory
only each warn first: the dialog shows the server's sentence (who will enter and with
what role; the count of the people it would keep out and, to a person who may list the
tenant's people, their names as a list) and holds its button until the person has typed
the method's word; the other two are plain confirmations. Every sentence names the
directory in words a person knows, never by its connection, which for a Microsoft Entra
tenant is an id: the name the members auth's registration gives the tenant where it
lists it with one (`azure.NamedTenant` in `pkg/auth/members`), and otherwise the label
of the portal's sign-in button, "your organization's Microsoft directory", which is
what this application says until its registration names its tenant. Nobody types a directory in: it is set from a sign-in, by an
administrator who is signed in through their organization's directory, whom the page
offers to make it the tenant's. The staff auth signs in by password until a directory
method joins it (`impulse add method`), so until then the page offers no such action, a
call is refused with how a directory is set, the directory a tenant has is the one
the seed gave it, and directory only cannot be turned on, since that takes a person who
is signed in through the tenant's directory.

The release ships a program for the devices: the sensor, `cmd/sensor`, declared on the
devices outlet in `cmd/generate/resourcegenerator/generator.go` with
`generation.ShipsProgram("cmd/sensor", generation.Platforms("linux/amd64"))`. The image
build compiles it for each platform into the image, and the outlet serves the file with
its checksum at `GET /devices/programs/sensor/<release>/linux/amd64`, behind the token
check, so a copy installed on a device fetches its next release where it already posts
its readings. A person installs the first copy from the release's assets or with `go
install <module>/cmd/sensor@<release>`. Given the outlet's URL, an application token a
staff login issued for the outlet on the console's API keys page, and the tenant's id,
which the tenant's page in the console shows
(`SENSOR_OUTLET`, `SENSOR_TOKEN`, `SENSOR_TENANT`; `SENSOR_SOURCE` names the device in
its readings, the hostname when unset), `sensor post 21.5` posts one reading and `sensor
list` lists the latest, and after either the copy reads the server's release from the
answer (`X-Api-Version`): newer than its own, it fetches that release's file with the
token it carries, verifies the checksum, swaps the file in beside itself (the old one
kept as `sensor.old`), says so and exits 0 for its service manager to start the new
release (`resource/shipped`). A copy built from a checkout never upgrades. `impulse
check` (`shipped-programs`) builds the program for each platform it declares, and the
generated router test proves the route sits behind the token check.

## Layout

- `main.go` serves the API and the console's built bundle.
- `pkg/config` is the configuration, as a chain of levels: core (every process), data
  (every process that opens the database), site (the served site). Each process
  constructs the level it needs — `cmd/bootstrap` and `cmd/deployment/migrate` stop at
  data — so a deploy step supplies exactly the variables its process reads.
- `pkg/auth` holds the auths, one package per population: `staff` (the console's people,
  who sign in with a password) and `members` (the portal's people, who sign in through the
  organization's directory over OpenID Connect). Each owns its session tables and cookie,
  its permission store, and its role file, so a member and a staff login with the same
  name are two unrelated principals. The members auth leaves role membership to the
  application (`session.DisableRoleSync`): the bootstrap assigns the development members
  their roles.
- `app` holds the handlers: generated resource handlers for every outlet plus the
  hand-written middleware, the devices outlet's token surface, and the two
  static-asset surfaces. `pkg/router` composes them: the staff auth's session group
  around the console, the members auth's around the portal (login redirect, directory
  callback, front-channel logout), the devices outlet's group behind the staff auth's
  token check, with the device flow's two endpoints under its prefix and the token routes
  (`/console/api/user/tokens`, `/console/api/user/device`) on the console, which the
  console's API keys page (the frame's) and device page (the client library's `impulse-tokens` entry) read
  and write through, one Angular application per browser outlet under its mount path,
  and the root redirect to the console's.
- `pkg/resources` holds the resource structs the generator reads: `Tenant` (the tenant
  record, a global resource), `Announcement` (tenant-scoped, on the console and the
  portal), and `Reading` (tenant-scoped, devices-only). `schema/migrations` holds the tables they describe; `schema/devseed` the
  development tenants; `pkg/auth/staff/roles.json` and `pkg/auth/members/roles.json` each
  auth's role file: the default roles the release ships, embedded in the binary and
  validated against the generated collection when the auth's engine opens. A domain role
  is held in every tenant domain, so nothing provisions a file per tenant. A role is named
  for its job in PascalCase (`Administrator`, `TenantAdministrator`, `DeviceOperator`),
  never for where it is held: the file's structure says that. Every role carries a
  description, one or two plain sentences saying what it is for, which the lists of the
  roles (`StaffRoles`, and a tenant's `StaffTenantRoles`) answer beside the name; a role
  without one is refused. The staff file
  authors `Administrator` over the tenant record and the whole application's side of the
  staff auth's generated management (`pkg/identity`), `TenantAdministrator` over the
  console's announcements and the management's tenant side, and `DeviceOperator`,
  the role of the people whose programs read and write the readings through the devices
  outlet, over the readings; the members file authors
  `TenantAdministrator` over the portal's announcements and the tenant side of the
  members auth's management, and no role of the whole application, so the members' lists
  and methods for the whole application are served and granted to nobody until a role is
  written for them. An account is one row for the whole application, whichever tenants
  its person holds a role in, so the methods a tenant changes an account through (a
  temporary password, a new name, a delete and the rest) are granted to a tenant's
  administrator under a condition: the tenant holds the person alone
  (`heldElsewhere = false AND heldGlobally = false`). Without it one tenant's
  administrator could set a password on an account that also manages another tenant or
  the application. The address and the directory's subject of a person another tenant
  holds too are left out of the tenant's lists the same way (`heldElsewhere = false`), a
  role held in every tenant is not one tenant's to take (`everyDomain = false`), and an
  invitation is withdrawn while it stands (`pending = true`);
  `test/integration/tenant_people_test.go` proves each of them for the staff on the
  console and `tenant_members_test.go` for the members on the portal. A new resource stays invisible until a role in its auth's file is granted
  it. Every grant in either file is proven live by `test/integration/grants_test.go`, which
  opens each auth the way the deployment does and asks its engine about each
  unconditional grant; a conditional grant is proven by a test case that names it through
  `provesGrant`, and `impulse check` fails on one no case names.
- `pkg/identity` holds each auth's management of its people, written by the generator
  (`GenerateIdentity`) as code of this application; the generator owns the tree whole.
  Each auth has two sides. The whole application's: the lists (`StaffSessionUsers`,
  `StaffUserRoles`, `StaffSessionMailLinks`, `StaffSessions` and the rest) and the methods
  (`CreateStaffUser`, `InviteStaffUser`, `AssignStaffRole`, `SetStaffTemporaryPassword`
  and the rest). A tenant's, under the tenant segment and named with the tenant record:
  the lists of its people, the roles held in it, its invitations, its people's sign-in
  identities and the roles it gives (`StaffTenantUsers`, `StaffTenantUserRoles`,
  `StaffTenantInvitations`, `StaffTenantIdentities`, `StaffTenantRoles`), and the methods
  (`CreateStaffTenantUser`, `InviteStaffTenantUser`, `AssignStaffTenantRole` and the
  rest). The members' carry the same names with `Members` for `Staff`, served on the
  portal, without the methods that create an account or set a password: the members
  sign in through a directory, which presents the account, and hold no password. All of it is served like every other resource and method and
  granted in the auth's role file. Each auth builds its client when it opens
  (its package's `New`, answered by its `Auth.Identity`) with the roles that count as an
  administrator's (the auth package's `AdministratorRoles`), so neither the application
  nor a tenant is left with nobody to give the next role, and sets the client's two hooks
  on its session manager, through which a sign-in gives a role in the transaction of the
  step that causes it: an accepted invitation's roles, and the default role of a
  directory a tenant declares. No auth is open without them. Neither browser application writes a
  page for any of it: the library builds them from the API description.
- `pkg/deploy` holds the database steps a deployment runs: the schema migrations, then
  each auth's role policy check (`CheckRoles`), which prints what its store holds that
  this release cannot use as written. Then `MigrateFeatures` brings the `FeatureFlags` table
  to the flags the release declares (section Feature flags). Between the two, `SeedAdministrators` gives each auth its first
  administrator while its users table holds no rows, the foothold a deployed environment
  starts from: the staff auth's `administrator`, with the password the stack generates
  (`APP_STAFF_ADMINISTRATOR_PASSWORD`, a generated secret), holding the development
  administrator's roles; the members auth's user without a password under the
  sign-in name the placement names (`APP_MEMBERS_ADMINISTRATOR`), holding the administrator
  roles in every domain, which that person's first sign-in through the directory links to.
  The tenants here have directories, where a name alone links no account that has never
  signed in, so the seed creates this one for the `directory` registration
  (`session.SeedUserIDThrough`): the id is the deployment's word on which registration
  the name is believed from, and the sign-in rule links the account at the person's
  first sign-in through it while it admits one organization alone (`azure.Tenant`).
  The staff auth's generated password is for the first sign-in alone: sign in as
  `administrator` with it, give an email address and confirm it with the sign-in code
  mailed there (the account starts with no address), then choose a new password, which
  the login page asks for before it opens anything else; the generated password signs
  nobody in after that. A user that exists stops the seed; the auth's management
  creates every user after the first, and the bootstrap creates the development logins
  instead. `cmd/deployment/migrate` is the deploy step;
  `cmd/bootstrap` reuses it to stand up an emulator database, seeds the development
  tenants (tenancy is data), and adds the development logins and the development member
  from `cmd/bootstrap/users.json`, each with its
  memberships by where they are held (`global`, `everyDomain`, `domains`). Its test runs
  each auth's role file through the validation the engine performs when it opens
  (`access.ValidateRoles`) and pins the warnings the deploy would print as typed values,
  none expected: a warning is accepted by pinning it there, or the role is fixed.
  A development seed under `schema/devseed` (data files as migrations, tracked apart from
  the schema, so a seeded database takes nothing twice) is applied by `cmd/bootstrap` and
  by the migrate command with `-seed`, which the pipeline passes in test environments and
  never in production; an application without the directory has nothing to apply. The
  migrate command's `-version` prints what each migrations table says about the database,
  and `-force <n>` and `-force-data <n>` set a table to a version (-1 for no version), for
  the states the migration runner refuses to guess at; the pipeline passes them from the
  operations workflow (`bedrock migration`), and none of them applies a migration.
  `-retire-secret APP_SECRET_KEY_1` retires a version of the application secret: it writes
  every stored secret field that names the version again under the current one, confirms
  none is left and exits 0 only then, so the deployment disables the version only when no
  stored value needs it (see "Secret fields").
- `test/authz` is the generated authorization matrix over the generated test router;
  `test/integration` drives the served stack (real router, session, engine) end to end.
  How a tenant comes to exist is held in both: `test/integration/invited_tenant_test.go`
  plays the invitation that makes a tenant from the sending to the first sign-in, and
  `test/authz/invited_tenant_test.go` holds the sending to the Create grant on the record.
- `web/` is the Angular workspace, one project per browser application (`console`,
  `portal`); each has its own generated client (`GenerateTypescript` per outlet).
  `web/go.mod` exists only to keep Go tooling out of `node_modules`.

## RPC methods

When a write is not a row mutation — a workflow transition, a batch, a command with an
answer — it is an RPC method: an `@rpc` struct in an rpc package whose `Execute`
signature says how it runs (inside the handler's transaction, or against the resource
client outside one) and what it answers (`error`, or `(Result, error)`). The generator
mirrors the request and the result into the handler, gates the route on `Execute`, and
types the browser client's handle. Three conventions it cannot enforce are stated in
the resource package's README (§7): a method answers with identifiers and outcomes,
never rows; a method that only answers a question is a computed resource; and a
transaction-form body keeps its effects inside the transaction, so `X-Dry-Run` tells
the truth. Bodies are trusted by default; `Enforce(caller)` on a generated builder
arms a write or read against the caller's own grants.

## Feature flags

A feature flag is a release switch: a `resource.Feature` constant in `pkg/resources`
(`impulse add feature <name>` declares one, with a doc comment that is its description),
`@feature(<Constant>)` on a resource, a field or an RPC method to put it behind the flag,
and off means absent: 404 on its routes, left out of the permission digest, a gated field
unknown to the decoders. The value lives in the `FeatureFlags` table (migration
`000011_FeatureFlags`, the library's own statements, which `impulse check` compares
with `resource.FeatureFlagsDDL`): the deploy's `MigrateFeatures` writes a new flag off,
keeps a known flag's state, and deletes what the release no longer declares, and every
flip is recorded in `FeatureFlagChanges`. The App reads the table when it is built
(`FeatureSet`) and follows it from `Start` (the live service's signals document, the
`features` kind, and the five-minute backstop reread), so the generated routes, decoders,
digest and features route answer from one copy. Every outlet serves `features` under its prefix (`/console/api`, `/portal/api`, `/devices`), and the two session outlets serve `feature-flags` and `set-feature`. Flipping is the generated
`SetFeature` method, held by the `FeatureAdministrator` role in the staff role file
(Execute on `SetFeature`, List and Read on `FeatureFlags`), which the development `admin`
login holds, so a member of the portal sees the flags' effect and flips nothing; the library's `FeatureFlagsDialog` calls it, opened from Feature flags… in
the console's System menu, shown to anyone holding List on `FeatureFlags`. A flag's
development state is its row in `schema/devseed/<n>_dev_feature_flags.up.sql`, which
`impulse add feature` writes off (set it to TRUE to start development and the test
environments with the feature on) and `impulse remove feature` deletes with the constant
and every `@feature` naming it. The application declares no flag yet.

## Lists and paging

A list answers one page at a time: `limit` rows, or the resource's declared default
(`@page(default: N, max: M)`, otherwise 50), positioned by a sealed cursor the server
issues in its `Link` header (`rel="next"`, `rel="prev"`), never by an offset. A resource
declares the order a sort-less list takes with `@order(Field asc, …)`; the primary key is
appended so the order is total. Every paged request needs an order from somewhere: a
listed resource declares its `@order` or every page of it names a `sort`, and a request
with neither is refused with a 400 unless it asks `limit=all`, the whole list, unsorted.
The resources here declare theirs, on the column a person reads first, and an index
leading with the tenant column and then the order columns serves each tenant-scoped one's
pages (the generator warns where one is missing). `count=true` on a first page answers the
total in `Total-Count`; `limit=all` returns every row where the resource declares no
maximum. The cursors are sealed under a key derived from the cookie key: `pkg/config`
builds it with `resource.NewCursorKey` and the App hands it to every generated decoder
through `CursorKey()`. In the browser, `@impulseframework/resource` follows the relations with
`page()` and reads every row with `all()`. Computed resources answer the same query
surface; their List function may take the conditions, sort, or page it pushes down and the
generated handler applies the rest.

## Secret fields

A field that holds a secret the application must use itself (an API key, a signing key) is
declared `conditions:"secret"` and typed `resource.Secret`, on a `BYTES` column with room
for the 42 bytes sealing adds: `APIKey *resource.Secret` on `ApiKey BYTES(106)` takes a key
of up to 64 bytes. The field is never served, the form masks it, a change record names it
without its values, and the database holds it encrypted: the resource client seals the
value when it is written, and code that needs it opens it with the row's generated method
(`row.OpenAPIKey(conf.SecretKeys())`, or `row.OpenAPIKeyIn(ctx, txn)` inside a
transaction). The generator refuses a `secret` field typed a plain `string`.

The key is the application secret, which `pkg/config` declares
(`env:"APP_SECRET_KEY" secret:"versioned"`) and reads into the resource client's secret
keys. It is kept in versions, each in a variable of its own. `.envrc.template` makes
`APP_SECRET_KEY_1` for the checkout: 32 random bytes, written to `.app-secret-key.dev` the
first time the file is sourced from the application's root and read from it from then on,
with `.gitignore` keeping that file out of git, so no key is written in a tracked file
and deleting the file leaves the development database's secrets unreadable. A deployment
generates the secret and hands the service every enabled version (`APP_SECRET_KEY_1`,
`APP_SECRET_KEY_2`), the highest the current one. An application with a secret field
refuses to start without a version, naming the variable. A
version is retired, before the deployment disables it, by the migrate command
(`-retire-secret APP_SECRET_KEY_1`), which writes every stored value that names it again
under the current version. It reads the resources packages `pkg/deploy` hands it
(`SecretHolders()`, which the generator writes into each), and `impulse check`
(`secret-holders`) fails when a package is left out of that call. The framework's
`resource/README.md`, "Secrets at rest", has the whole of it.

## Running it

    cp .envrc.template .envrc && direnv allow
    overmind start -l spanner,firestore,server

That starts fresh Spanner and Firestore emulators, bootstraps the database (schema, tenants, both auths' roles, the
`admin` and `member` staff logins with password `password`, and the `client` member), and
serves every outlet on `$PORT` (:8093). The first run
compiles and bootstraps before it listens; wait for "Starting Server" in the server pane.

The auths mail: an administrator invites a person by address with the roles the invitation
gives, a signed-in person adds and verifies an address, and every sign-in that carries no
second factor of its own (a password, or a directory the deployment does not administer)
is mailed a sign-in code, which the login page asks for; a browser the person asks to be
remembered is not asked again. In development the mail is written to the server's log,
link and code included, and nothing is sent; a deployment names a
Workspace mailbox (`APP_MAIL_MAILBOX`, `APP_MAIL_APP_PASSWORD`, and `APP_MAIL_FROM` where the
mail is from an alias of that account) and the relay sends it. Every
link opens the login page under `APP_PUBLIC_URL`.

The server builds with the session library's `skipAuth` tag (see the Procfile), which
simulates the portal's directory: every portal login is `APP_USERNAME` from `.envrc`
(`client`), no directory is contacted, and only `APP_MEMBERS_DIRECTORY_REDIRECT_URL` is read.
To sign in through a real directory, register the application there, fill in the
`APP_MEMBERS_DIRECTORY_*` variables, and drop the tag. A deployed build never carries it.

### Browser apps

The console and the portal are two projects in one Angular workspace under `web/` that
installs the published `@impulseframework/resource`, `@impulseframework/resource-angular` and
`@impulseframework/resource-firestore` (pinned in `package.json`). To develop
against a local checkout of the framework, attach its web packages through yalc, a local
state that is never committed (the script builds the three packages, publishes them to the
local yalc store, links them, and runs `bun install`; `weblib.sh restore` puts the pins back):

    (cd web && ./weblib.sh local)

`weblib.sh` expects the framework checkout beside this application as `impulse`; set
`IMPULSE` to point elsewhere. Then `overmind start` runs everything: the console at
http://127.0.0.1:4301/console/ (sign in as `admin` / `password`) and the portal at
http://127.0.0.1:4303/portal/ (the sign-in button signs in as the simulated directory's
`client`).

The browser files are held to the prettier the workspace pins (`.prettierrc`): `impulse
check` names a file prettier would change, and `impulse check --only prettier --fix` formats
it. Run that once after the first install: the files were written under this application's
names before the formatter was there to wrap their lines.

After a change in the framework's web packages, push the rebuilt packages, restart the dev
server, and reload the page:

    (cd web && ./weblib.sh push)
    overmind restart console portal

The dev server does not watch `node_modules`, so the restart is what picks the new build up.
The packages are bundled with the application code rather than prebundled by Vite (the
`prebundle` exclusion in `angular.json`), so a plain reload shows the new build in any
browser profile: nothing is held behind an immutable URL, and no cache needs clearing.

The console and the portal install as a progressive web app. The production build emits a
service worker (`@angular/service-worker`, configured by `web/ngsw-config.json`) that keeps the
files of the build an open tab loaded, so a release never breaks a tab still running the
previous build, and the library's update notice offers Reload when a new build is ready; the
worker is off under `ng serve`. The manifest and the icons sit in each project's `public/`
directory (`manifest.webmanifest`, `icons/`): a placeholder set, the skeleton's mark, until the
application's own logo is in place.

The brand is declared once, in each project's `brand.json` (`web/console/brand.json`,
`web/portal/brand.json`: the console and the portal carry a brand each): the application's name,
its logo (`logo.png` beside the declaration, a square PNG at least 512 pixels a side; `logoSvg`
names the same logo as an SVG, which then is the favicon as it is), a primary color and,
optionally, a background color. `impulse brand` writes from it the favicon and the app icons,
the manifest's names and colors, the page's title and `theme-color`, the name the frame draws,
the theme's first palette (generated from the primary color by Angular Material's own generator,
once the workspace's packages are installed, into `src/_theme-colors.scss`), and what the server
says: `pkg/brand`, the name and the logo `main.go` hands to the maintenance page, and the name
the mail says, the `Application` constant of the auth whose people sign in to it
(`pkg/auth/staff` for the console, `pkg/auth/members` for the portal). The declaration the
skeleton carries names the placeholder: put the application's own logo in its place, set the
name and the colors, and run `impulse brand`, then again whenever the declaration changes;
`impulse check` (`brand`) reminds while the placeholder is still there.

The server serves every bundle through the resource package's `BrowserApp`, which sets
the cache headers a worker needs; a build deployed without `ngsw.json` makes every installed
worker unregister itself and drop its caches. `impulse check` (`installable`) holds the wiring.

The production build stamps the release into each bundle (`VERSION=1.2.3 bun run build`;
`bun run build` alone stamps `dev`, as the dev server does), and the application sends it in
`X-Api-Version` with every request. The server answers releases from the outlet's `OldestAnswered` (in
`cmd/generate`) up to its own `APP_VERSION` and refuses the rest with 412, which the update
notice turns into a reload onto the current build; a `dev` build on either side checks
nothing.

Both projects' component specs run on Angular's unit-test builder (`@angular/build:unit-test`)
with Vitest under jsdom in Node: no browser, no Karma. `bun run test` runs the console's and
then the portal's once, the form the Checks section and CI use; `bun ng test console` or
`bun ng test portal` watches one. The specs beside the skeleton's components are the
pattern for the application's own: each dashboard renders a permission digest over a
scripted client from `@impulseframework/resource-angular/testing` (`provideResourceTesting` puts the
generated client on a transport that records every request and answers from the test),
each menu file's spec holds every page the routes carry to a place in the menus
(`unplacedPages`), and the portal's login spec is the directory sign-in's; the frame
itself is the library's, tested there.

## Live pages

A list page or a record page can stay current without polling. A request the page asked
to be live carries `X-Subscribe: <tab>`; a permitted list or read registers the page's
interest (who, which tab, which resource, which row or which tenant, until when) before
the query runs, every commit publishes the rows it touched into its subscribers' change
sets, and the browser refetches with `_v=<the change's timestamp>`, an answer the browser
caches for five minutes (`Cache-Control: private, max-age=300`). Nothing is polled and
the server holds no connection open.

The server side is wired. The data level opens the live service over the Firestore
database the configuration names: `APP_FIRESTORE_DATABASE` in a deployment, the emulator
through `FIRESTORE_EMULATOR_HOST` in development (`pkg/config`, `LiveSettings`;
with no project named, the driver opens the emulator under its own project id). The live service is required: with neither
a database nor the emulator configured the data level refuses to start, naming the two
variables. The App hands it to the generated handlers
through `LiveService()`, and the generated router serves the live routes on both browser outlets (`/console/api/live/...` and `/portal/api/live/...`: `renew`, `unsubscribe`, `token`), while the devices outlet, a machine surface, refuses a request carrying the header. The
content security policy the App sends names the change feed's origins beside the
application (the emulator in development, Firebase's hosts in production), since the
browser's feed connects to them directly rather than through the API.
`schema/firestore` holds the rules the browser's reads run under and the indexes and
time-to-live policies the subscription record needs; the Procfile's emulator runs the
rules, and a deployment applies both files to the database.

The live service also carries the one channel the application's instances signal each
other on: one document per application, `application/signals`, with a field per kind of
change the resource package declares (`features`, `tenants`, `policy`), written by
`Signal(ctx, kind)` and followed by `Subscribe(kind, onSignal)`. This application uses
two. A feature flag flip signals the `features` kind and every instance's `FeatureSet`
rereads the table; the staff and members auths' permission engines are
constructed with `access.WithChangeSignal` over the `policy` kind (`pkg/auth/staff`,
`pkg/auth/members`), so a role, grant or membership written on one
instance reaches every instance's policy snapshot at once rather than at the engine's
next heartbeat. The `change-signal` check proves every engine the application constructs
is handed it, and the authorization and integration suites wire the in-memory
`live.NewFake()`.

The browser side is the client packages' live option: a list view or record view opting
in (`live: true` in its view configuration) with the Firestore change feed provided at
the application root, started after login and stopped at logout. It follows the client
packages' release that carries the option; until then the pages read as they do today.
The resource package's README describes the design (section 14, "Live pages").

## Checks

    go generate ./...           # regenerate; cmd/generate's test fails on drift, and each
                                # program prints its schema warnings as Warning: lines
    go test -tags skipAuth ./...  # needs podman for the emulator; the tag simulates the
                                  # portal's directory, and the portal login tests skip without it
    golangci-lint-v2 run
    cd web && bun run build && bun run lint && bun run test

A run that removes a generated Go file it no longer writes (after a resource, a method, an
auth or an outlet is removed or renamed) says so in its last lines, and `go generate ./...`
then exits 1 with `no such file or directory`: the go command lists every package's files
before it runs the generator and stops at one that is gone when it reaches it. The generation
is complete; run the command once more and it finishes. `impulse` runs it the second time
itself wherever it regenerates.

A generate program is three files in its directory: `generator.go` declares the generator
(`newGenerator`), `main.go` runs it and prints every schema warning the run raised (an index a
listed tenant-scoped resource wants, a tenant resolved through a join path, an enumeration
table too large to bake), and `warnings_test.go` pins the accepted set as typed values, none
to start: a new warning fails `go test` until the schema is fixed or the warning's value is
added to the test, which records the acceptance in code. The two tests that regenerate
(`cmd/generate`'s and each program's) take the module's generation lock first
(`cmd/generate/internal/regen`): `go test` runs packages in parallel processes, and one
module is regenerated once at a time. Run with `-audit`, the program also
prints the audit pass's findings (`Audit:` lines, advisory and never printed by a plain
generation); `impulse audit` runs every program that way, and `impulse check` lists the
warning lines under its regen result.
