# sites

An application in the sites layout on the Impulse Framework. Two sites — the console
and the portal — each its own server, hostname, router, browser application, and
generator, over one database, one policy store, and one session store. Tenancy as data, as
in a flat application: the `Tenant` record (`@tenant`) over the `Tenants` table is the
domain universe, declared in both sites' resource packages (the console serves it, the
portal's declaration has its handlers and routes suppressed), tenant-scoped routes live
under `/api/tenants/{tenantID}/`, the shared data level's tenant roster keeps the tenants
current on every instance of both sites (a tenant is usable the moment it is made,
without a restart), and tenant existence is concealed from logins that
hold nothing there. Each site's browser application sits in the library's frame
(`provideFrame` in `app.config.ts`) and mirrors the API's shape: every tenant-scoped page
lives under the tenant segment, `/tenants/{tenantID}/...` (the generated
`DomainRoutePath`), so a link to a tenant's page can be shared and a bookmark lands where
it points, and the pages of Configuration, the place outside every tenant, keep their
routes. The tenant a page works in is a consequence of its URL: the library's tenant guard
on the segment opens the tenant the URL names before the page loads, checked against the
session's tenant list, and nothing else writes it; the place switcher beside the
application's name opens the same page under the chosen tenant, and a URL naming a tenant
the session cannot reach lands on the refusal page (`/tenants/{tenantID}/refused`), which
shows the server's answer. The dashboard is every place's first page; the console manages
the staff auth's people in both places the same way, the whole application's accounts at
`/staff-accounts` and a tenant's people at `/tenants/{tenantID}/staff-people`. Groups of
people are kept beside them (`/staff-groups`, `/tenants/{tenantID}/staff-groups`): a group
holds roles the way a person does, a person in it holds the group's roles, and the role
file gives a place's groups, their members and their roles to whoever manages its people
(`Administrator` for the whole application, `TenantAdministrator` in a tenant). A group gives
nobody a power that giving the role directly would not: who is in a group, and what it
holds, is changed by someone who may give or take those roles directly, and a tenant puts
its own people in its groups, while an account from outside the tenant is put there from
the whole application's accounts. The list of groups says how many people each group has and the roles it
holds, and offers Delete only on a group that holds nothing; a person's page lists their
roles in one list that says "Their own" or names the group each comes through. On the
Break glass page (`/staff-break-glass`) a person takes a role in a tenant for a set time
and says why, when they hold the page's grant and could give themselves that role there:
the role ends by itself, and the tenant's administrator reads the reason beside it and
may end it early. The role file gives the page's lists and its method to the
`Administrator` alone: a `TenantAdministrator`, who may give themselves a role in their
own tenant, holds no grant of the page and takes none for a set time. The library
draws them from the API description (`identityRoutes` in the console's `app.routes.ts`,
placed by `identityMenuPages` in its menu file), and a tenant's pages read the tenant from
the URL (the frame provides `RESOURCE_DOMAIN`). The console's menu file (`app.menus.ts`)
shows Tenants (All tenants and Invite a tenant…) and System (the staff's accounts, the Invited tenants page and
Feature flags) in Configuration, and People and Tenant (Sign-in settings) inside a tenant; the
portal has no pages of its own yet, so its bar holds Help alone. In both, the person's own
menu holds Your account, Appearance and Sign out, and Help ends with About, a pop-up that carries
the release and, outside production, the environment's name (the server's `APP_ENVIRONMENT`, which a deployment sets everywhere but production).

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
(`staff.Settings.TenantMaker`, the generated `consoleapp.NewTenantMaker` over the roster, built before the
auth), and refuses to start without it. A person who signed up and holds no tenant
creates their own through the generated create-your-tenant route, which the dashboard
offers while an administrator has a sign-in method open to sign-up.

Who may enter a tenant is the record's declaration, data of the application rather than
deploy configuration: the tenant's directory, the organization its people sign in
through (`TenantDirectory`, the console site's, one row at most under
`/api/tenants/{tenantID}/tenant-directories`: a sign-in method and the connection its
identities carry, a Google Workspace by its domain or a Microsoft Entra tenant by its
tenant id) and whether its people enter through it only (`Tenant.DirectoryOnly`, which
closes the tenant to every sign-in but one through its directory). A tenant has one directory and a directory belongs to one tenant: the
table's two unique indexes hold both, and the generated write paths refuse first, each
with a sentence the person can act on. The staff auth's sign-in rule, one for both sites, is the library's
tenant policy over it (`signin.TenantPolicy`, handed the console site's generated
`NewTenantDeclarationReader`, which reads the declaration in the sign-in's own
transaction): a tenant's declaration restricts its own door alone, so a tenant that is
not directory only admits everyone it seats, however they signed in, a directory-only
tenant admits a sign-in through its own directory and, of its own administrators alone, a
password with a code sent to their email, and nothing else, and a person is
refused only when every tenant that seats them is closed to the way they signed in. One
part of a declaration reaches past its own tenant: an account that signs in through a
directory-only tenant's directory signs in through that directory alone, whichever
tenants seat the person; that tenant's own administrators keep the password. A
super administrator is admitted whatever a tenant declares, an administrator of a
directory-only tenant (a holder of `TenantAdministrator` in that tenant itself, by a role
of their own or through one of the tenant's own groups, never one held in every tenant)
who signs in with a password is held for the code sent to their email and then enters
that tenant, which is the tenant's way back in when its directory has a problem (who
holds that role in a directory-only tenant, and the account of a person who does, is
changed only by a caller whose own session the tenant admits or by a super administrator,
so the application's own staff do not give themselves the way in), a person
holding a role
outside any one tenant (a global role, or one held in every tenant) is admitted and then
enters a tenant only where its declaration admits their session, and a person holding no
tenant is refused.
Which tenants the session then reaches is decided again per tenant where its tenant list
is computed, on either site, and a tenant that seats the person and is closed to their
session stays in the list marked closed, with the reason: the shared roster keeps every tenant's declaration beside
its key (`resource.TenantRoster.Declarations`, handed to `resource.SessionPermissions` by
both Apps), the console's write paths reload it after their commit and signal the other
instances, so a declaration decides the next request on both sites, and the records'
change events are the audit of every edit. The console's tenant pages carry the
declaration at `/tenants/{tenantID}/sign-in`: the tenant's administrator sees the
tenant's directory there and removes it, and changes everything else with the tenant's own generated
methods, which `TenantAdministrator` holds and the page runs through the library's
method dialog: `OpenTenantToDirectory`
(`POST /api/tenants/{tenantID}/open-tenant-to-directory`) names the role each of
the directory's people is given at their first sign-in through it, a role the caller
could give directly, picked from the list of the roles they may give
(`StaffTenantDirectoryRoles`), and `MakeTenantInviteOnly` clears it; `MakeTenantDirectoryOnly`
(`POST /api/tenants/{tenantID}/make-tenant-directory-only`) and
`EndTenantDirectoryOnly` turn directory only (the `Tenants` record shows the flag and
takes no write of it), which the server turns on only once the tenant has its directory
and only for a person signed in through it who has a password of their own, their way
back in if the directory has a problem; the page says both before the person presses,
in the server's own sentences, and offers the button only where the tenant has a
directory. Opening the tenant and making it directory
only each warn first: the dialog shows the server's sentence (who will enter and with
what role; the count of the people it would keep out and, to a person who may list the
tenant's people, their names as a list) and holds its button until the person has typed
the method's word; the other two, and removing the directory, are plain confirmations. Every sentence names the
directory in words a person knows, the name a sign-in registration gives it or the
label of its button, and never by a connection that is an id. Nobody types a directory in: it is set from a
sign-in, by an administrator who is signed in through their organization's directory,
whom the page offers to make it the tenant's. The staff auth signs in by password until
a directory method joins it (`impulse add method`), so until then the page offers no
such action, a call is refused with how a directory is set, and directory only cannot be
turned on, since that takes a person who is signed in through the tenant's directory.

## Layout

- `apps/<site>/` is one site: `main.go`, `app` (its handlers), `pkg/router`, `pkg/resources`
  (the resources this site serves — a table both sites serve is declared in both, since a
  generator reads one package), `test/authz` (its generated authorization matrix), and
  `web/` (its Angular workspace, with its own `go.mod` fence).
- `pkg/config` is the shared configuration chain: core (every process), data (every
  process that opens the database), site (one served site: `PORT` and `APP_DIST`). The
  site level is declared once; each site's process supplies its own values, so the Procfile
  and each site's deployment carry them while `.envrc.template` carries the shared levels.
- `apps/console/pkg/identity` holds the staff auth's management of its people, written
  by the console's generator (`GenerateIdentity`) as code of this application; the
  generator owns the tree whole. Both sites sign the same auth in, and one generator
  program owns a tree, so one site carries the management: the console, where the
  application manages its people. The portal's generator does not set the option and the
  portal serves none of it. The tree has two sides. The whole application's: the lists
  (`StaffSessionUsers`, `StaffUserRoles`, `StaffSessionMailLinks`, `StaffSessions` and the
  rest) and the methods (`InviteStaffUser`, `AssignStaffRole`,
  `SendStaffUserPasswordResetLink` and the rest). A tenant's, under the tenant segment and
  named with the tenant record: the lists of its people, the roles held in it, its
  invitations, its people's sign-in identities and the roles it gives (`StaffTenantUsers`,
  `StaffTenantUserRoles`, `StaffTenantInvitations`, `StaffTenantIdentities`,
  `StaffTenantRoles`), and the methods (`InviteStaffTenantUser`,
  `AssignStaffTenantRole` and the rest). All of it is served like every other resource
  and method and granted in the role file. The shared auth builds the client
  the console's handlers act through when it opens (`staff.New`, answered by
  `staff.Auth.Identity`) with the roles that count as an administrator's
  (`staff.AdministratorRoles`), so neither the application nor a tenant is left with
  nobody to give the next role, and sets the client's two hooks on its session manager
  in every process that signs people in, the portal's included: through them a sign-in
  gives a role in the transaction of the step that causes it, an accepted invitation's
  roles and the default role of a directory a tenant declares. No auth is open without
  them.
- `pkg/sharedresources` is what every site's browser application needs in the same shape
  (the enumerations). The shared generator emits its TypeScript into each site's web
  application and generates nothing else.
- `pkg/deploy` holds the database steps a deployment runs: the schema migrations, then
  the role policy check (`CheckRoles`), which prints what the store holds that this
  release cannot use as written. Then `MigrateFeatures` brings the `FeatureFlags` table to the
  flags the release declares (section Feature flags). Between the two, `InviteAdministrators` invites each
  auth's first administrator while nobody of the auth holds the Super Administrator
  role, the foothold a deployed environment starts from: an invitation mailed to the address the placement names for
  the environment (`APP_STAFF_ADMINISTRATOR`), which gives the Super Administrator role
  and the development administrator's roles. The person opens the link and chooses a
  password there, and enters the code mailed to the address. No account is made ahead of them and
  no password is generated. A release leaves an invitation that stands as it is, mails a new one once
  it has run out (seven days), and withdraws one for an address the placement no longer
  names; the migrate command's `-reinvite-administrator staff` withdraws the
  standing one and mails a fresh one on demand, for mail that was lost, while nobody
  holds the role. Both refuse while nobody holds it and the environment has no mailbox
  (`APP_MAIL_MAILBOX` with `APP_MAIL_APP_PASSWORD`): an invitation's link is never
  written to a log, so the mailbox comes before the first release. Once somebody holds
  the Super Administrator role nobody is invited, and neither an address nor a mailbox
  is asked for; an account that holds no such role (a person who signed up, a persona)
  stops no invitation, and a holder whose account is disabled is a holder still. The
  auth's management invites every person after the first. The bootstrap creates the
  development logins instead, and its `admin` login holds the role, so a migrate run
  against a bootstrapped database invites nobody. An application that adds a cast of
  personas seeds it before `InviteAdministrators`, so an environment whose cast holds
  the role invites nobody. The staff auth's role file validates against the union
  of the sites' generated collections (`config.Collection`) when its engine opens: one
  policy store, one role file, one identity across sites.
  `cmd/deployment/migrate` is the deploy step; `cmd/bootstrap` reuses it for the emulator
  and adds the development tenants and logins, each login with its memberships by where
  they are held (`global`, `everyDomain`, `domains`). Its test runs each auth's role file
  through the validation the engine performs when it opens (`access.ValidateRoles`) and
  pins the warnings the deploy would print as typed values, none expected: a warning is
  accepted by pinning it there, or the role is fixed.
  A development seed under `schema/devseed` (data files as migrations, tracked apart from
  the schema, so a seeded database takes nothing twice) is applied by `cmd/bootstrap` and
  by the migrate command with `-seed`, which the pipeline passes in test environments and
  never in production; an application without the directory has nothing to apply. The
  migrate command's `-version` prints what each migrations table says about the database,
  and `-force <n>` and `-force-data <n>` set a table to a version (-1 for no version), for
  the states the migration runner refuses to guess at; the pipeline passes them from the
  operations workflow (`bedrock migration`), and none of them applies a migration.
  Every run but those opens the application's data and gives the database one minute to
  answer: a database that has not answered by then, as one that does not exist never does,
  stops the run with one sentence that names the database and its instance, and a non-zero
  exit.
  `-retire-secret APP_SECRET_KEY_1` retires a version of the application secret: it writes
  every stored secret field that names the version again under the current one, confirms
  none is left and exits 0 only then, so the deployment disables the version only when no
  stored value needs it (see "Secret fields").
- `cmd/generate` runs the three generators: console, portal, shared. `impulse check`'s
  sites-generators check fails the build when a generator reads a different schema or the
  shared generator misses a site.
- `schema/migrations` is the one schema; `pkg/auth/staff/roles.json` the staff auth's
  role file, the default roles the release ships, embedded in the binary. A role is named
  for its job in PascalCase (`Administrator`, `TenantAdministrator`), never for where it
  is held: the file's structure says that. Every role carries a description, one or two
  plain sentences saying what it is for, which the lists of the roles (`StaffRoles`, and
  a tenant's `StaffTenantRoles`) answer beside the name; a role without one is refused. A
  domain role is
  held in every tenant domain, so nothing provisions the file per tenant. An account is
  one row for the whole application, whichever tenants its person holds a role in.
  A tenant's administrator sees every person of the tenant with the name they sign in
  with, their name, their email address and whether the account is disabled. The rest of
  an account, how its person signs in, and the methods that change it are the
  administrator's only where the tenant manages the account (`managedHere = true`): the
  person signs in through the tenant's directory, or signs in through no tenant's
  directory and holds something in this tenant and nothing anywhere else. A password
  reset link goes to everyone in the list but a person another tenant's directory
  manages (`managedHere = true OR managedByAnotherDirectory = false`). A role held in
  every tenant is not one tenant's to take
  (`everyDomain = false`), and an invitation is withdrawn while it stands
  (`pending = true`); `test/integration/tenant_people_test.go` proves each of them on the
  console. The application's `Administrator` changes an account only where no tenant
  manages it (`managedByTenant = false`), and `test/integration/grants_test.go` proves that
  too. Every grant in
  it is proven live by `test/integration/grants_test.go`, which opens the auth the way
  the deployment does and asks the engine about each unconditional grant; a conditional
  grant is proven by a test case that names it through `provesGrant`, and
  `impulse check` fails on one no case names.
- `test/integration` serves both sites over one database and drives each the way its
  browser application does. How a tenant comes to exist is held there and beside the
  console's matrix: `test/integration/invited_tenant_test.go` plays the invitation that
  makes a tenant from the sending to the first sign-in, and
  `apps/console/test/authz/invited_tenant_test.go` holds the sending to the Create grant
  on the record.

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

A feature flag is a release switch: a `resource.Feature` constant in a site's `pkg/resources` (each site's generator resolves `@feature` against its own package, and `MigrateFeatures` writes both sites' declarations into the one table)
(`impulse add feature <name>` declares one, with a doc comment that is its description),
`@feature(<Constant>)` on a resource, a field or an RPC method to put it behind the flag,
and off means absent: 404 on its routes, left out of the permission digest, a gated field
unknown to the decoders. The value lives in the `FeatureFlags` table (migration
`000008_FeatureFlags`, the library's own statements, which `impulse check` compares
with `resource.FeatureFlagsDDL`): the deploy's `MigrateFeatures` writes a new flag off,
keeps a known flag's state, and deletes what the release no longer declares, and every
flip is recorded in `FeatureFlagChanges`. The App reads the table when it is built
(`FeatureSet`) and follows it from `Start` (the live service's signals document, the
`features` kind, and the five-minute backstop reread), so the generated routes, decoders,
digest and features route answer from one copy. Each site serves `features` under its `/api`. Flipping is the generated
`SetFeature` method, held by the `FeatureAdministrator` role in the staff role file
(Execute on `SetFeature`, List and Read on `FeatureFlags`), which the development `admin`
login holds; the library's Feature flags page runs it: the list of the flags, each in words with who last turned it on or off, a row's menu that turns the flag on or off after asking, and a History button that opens the flag's flips in a pop-up. The menu file places the page (`featureFlagsPage`) in
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
    overmind start -l spanner,firestore,console,portal

That starts fresh Spanner and Firestore emulators, bootstraps the database (schema, the North and South tenants,
roles, the logins `admin` — every tenant — `member` and `client` — North only — with
password `open-the-console-first`), and serves the console site on :8094 and the portal site on :8095.
The first run compiles and bootstraps before it listens; wait for "Starting Server" in
the console pane, then the portal pane.

The auths mail: an administrator invites a person by address with the roles the invitation
gives, a signed-in person adds and verifies an address, and every sign-in that carries no
second factor of its own (a password, or a directory the deployment does not administer)
is mailed a sign-in code, which the login page asks for; a browser the person asks to be
remembered is not asked again. In development the mail is written to the server's log,
link and code included, and nothing is sent; a deployment names a
Workspace mailbox (`APP_MAIL_MAILBOX`, `APP_MAIL_APP_PASSWORD`, and `APP_MAIL_FROM` where the
mail is from an alias of that account) and the relay sends it. Every
link opens the login page under `APP_PUBLIC_URL`.

### Browser apps

Each site has its own Angular workspace, `apps/console/web` and `apps/portal/web`, that
installs the published `@impulseframework/resource`, `@impulseframework/resource-angular` and
`@impulseframework/resource-firestore` (pinned in `package.json`). Install them
once with `bun install` in each: it writes the workspace's `bun.lock`, which the
workflows install from (`bun ci`) and the repository keeps, and `impulse check` fails
while a web root has none (`package-manager`). To develop
against a local checkout of the framework, attach its web packages to each through yalc, a local
state that is never committed (the script builds the three packages, publishes them to the
local yalc store, links them, and runs `bun install`; `weblib.sh restore` puts the pins back):

    (cd apps/console/web && ./weblib.sh local)
    (cd apps/portal/web && ./weblib.sh local)

`weblib.sh` expects the framework checkout beside this application as `impulse`; set
`IMPULSE` to point elsewhere. Then `overmind start` runs everything, with `ng serve` for the console at
http://127.0.0.1:4304 and the portal at http://127.0.0.1:4305.

The browser files are held to the prettier the workspace pins (`.prettierrc`): `impulse
check` names a file prettier would change, and `impulse check --only prettier --fix` formats
it. Run that once after the first install: the files were written under this application's
names before the formatter was there to wrap their lines.

After a change in the framework's web packages, push the rebuilt packages, restart the dev
server, and reload the page:

    (cd apps/console/web && ./weblib.sh push)   # a push updates every attached workspace
    overmind restart console-web portal-web

The dev server does not watch `node_modules`, so the restart is what picks the new build up.
The packages are bundled with the application code rather than prebundled by Vite (the
`prebundle` exclusion in `angular.json`), so a plain reload shows the new build in any
browser profile: nothing is held behind an immutable URL, and no cache needs clearing.

Each site's browser application installs as a progressive web app. The production build emits a
service worker (`@angular/service-worker`, configured by its workspace's `ngsw-config.json`)
that keeps the files of the build an open tab loaded, so a release never breaks a tab still
running the previous build, and the library's update notice offers Reload when a new build is
ready; the worker is off under `ng serve`. The manifest and the icons sit in its `public/`
directory (`manifest.webmanifest`, `icons/`): a placeholder set, the skeleton's mark, until the
application's own logo is in place.

The brand is declared once, in each site's `brand.json` (`apps/console/web/brand.json`,
`apps/portal/web/brand.json`: the console and the portal carry a brand each): the application's
name, its logo (`logo.png` beside the declaration, a square PNG at least 512 pixels a side;
`logoSvg` names the same logo as an SVG, which then is the favicon as it is), a primary color
and, optionally, a background color. `impulse brand` writes from it the favicon and the app
icons, the manifest's names and colors, the page's title and `theme-color`, the name the frame
draws, the theme's first palette (generated from the primary color by Angular Material's own
generator, once the workspace's packages are installed, into `src/_theme-colors.scss`), and what
the server says: `pkg/brand`, the name and the logo each site's `main.go` hands to the
maintenance page, and the name the mail says, the `Application` constant of the auth whose
people sign in to it (`pkg/auth/staff`). The declaration the skeleton carries names the
placeholder: put the application's own logo in its place, set the name and the colors, and run
`impulse brand`, then again whenever the declaration changes; `impulse check` (`brand`) reminds
while the placeholder is still there.

The server serves every bundle through the resource package's `BrowserApp`, which sets
the cache headers a worker needs; a build deployed without `ngsw.json` makes every installed
worker unregister itself and drop its caches. `impulse check` (`installable`) holds the wiring.

Each site's production build stamps the release into its bundle (`VERSION=1.2.3 bun run build`;
`bun run build` alone stamps `dev`, as the dev server does), and the application sends it in
`X-Api-Version` with every request. The server answers releases from the outlet's `OldestAnswered` (in
`cmd/generate`) up to its own `APP_VERSION` and refuses the rest with 412, which the update
notice turns into a reload onto the current build; a `dev` build on either side checks
nothing.

Each workspace's component specs run on Angular's unit-test builder (`@angular/build:unit-test`)
with Vitest under jsdom in Node: no browser, no Karma. `bun run test` in a workspace runs
its specs once, the form the Checks section and CI use; `bun ng test console` (or `portal`)
watches. The specs beside the skeleton's components are the pattern for the application's
own: each dashboard renders a permission digest over a scripted client from
`@impulseframework/resource-angular/testing` (`provideResourceTesting` puts the generated client on
a transport that records every request and answers from the test), and each menu file's
spec holds every page the routes carry to a place in the menus (`unplacedPages`); the
frame itself is the library's, tested there.

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
through `LiveService()`, and the generated router serves the live routes on each site's session outlet (`/api/live/renew`, `/api/live/unsubscribe`, `/api/live/token`), the sites sharing one database and one service as they share the data level. The
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
rereads the table; the staff auth's permission engine, shared by both sites, is
constructed with `access.WithChangeSignal` over the `policy` kind (`pkg/auth/staff`), so a role, grant or membership written on one
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
    go test ./...               # needs podman for the emulator
    golangci-lint-v2 run
    (cd apps/console/web && bun run build && bun run lint && bun run test)
    (cd apps/portal/web && bun run build && bun run lint && bun run test)

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
