# tenanted

A tenant-scoped application on the Impulse Framework: the smallest one a person can
sign in to. One site, one browser application,
tenancy as data: the `Tenant` record (`@tenant`) over the `Tenants` table is the domain
universe, a tenant's key is an id the server generates while its name is a field its
administrators change, tenant-scoped routes live under `/api/tenants/{tenantID}/`, the data level's
tenant roster keeps the tenants current on every instance (a tenant is usable the moment
it is made, without a restart), and tenant existence is concealed from logins
that hold nothing there. `Announcement` is the first tenant-scoped resource — a login's tenant list
is where it holds a grant, and a grant needs a tenant-scoped resource to land on, so the
application ships one. The console sits in the library's frame (`provideFrame` in
`app.config.ts`) and mirrors the API's shape: every tenant-scoped page lives under the
tenant segment, `/tenants/{tenantID}/...` (the generated `DomainRoutePath`), so a link to
a tenant's page can be shared and a bookmark lands where it points; the pages of
Configuration, the place outside every tenant, keep their routes, the tenant record's own
among them (`/tenants`, a row at `/tenants/{tenantID}`). The tenant a page works in is a
consequence of its URL: the library's tenant guard on the segment opens the tenant the URL
names before the page loads, checked against the session's tenant list (the generated
user-domains answer, the server's own foothold test, with each tenant's name), and nothing
else writes it. The place switcher beside the application's name lists Configuration and
the tenants by name and opens the same page under the chosen tenant; a URL naming a tenant
the session cannot reach lands on the refusal page under that tenant's URL
(`/tenants/{tenantID}/refused`), which shows the server's answer — with concealed domains,
the 404 every route under the tenant gives — and never switches to another tenant. The
dashboard is every place's first page, the global digest in Configuration and the
tenant's under a tenant, and the staff auth's people are managed in both places the same
way: the whole application's accounts at `/staff-accounts`, a tenant's people at
`/tenants/{tenantID}/staff-people`. Groups of people are kept beside them
(`/staff-groups`, `/tenants/{tenantID}/staff-groups`): a group holds roles the way a
person does, a person in it holds the group's roles, and the role file gives a place's
groups, their members and their roles to whoever manages its people (`Administrator`
for the whole application, `TenantAdministrator` in a tenant). A group gives
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
own tenant, holds no grant of the page and takes none for a set time. The library draws them from the API description
(`identityRoutes` in `app.routes.ts`, placed by `identityMenuPages` in the menu file), and
a tenant's pages read the tenant from the URL (the frame provides `RESOURCE_DOMAIN`).
The menu file (`app.menus.ts`) shows Tenants (All tenants, the library's list page over the
record) and System (the staff's accounts, the Invited tenants page and Feature flags) in
Configuration, and People and Tenant (Sign-in settings) inside a tenant; the person's own
menu holds Your account (the username, name, email address and password, as lines that
open in place to change them, and Forget this browser),
Appearance and Sign out; Help ends with About, a pop-up that carries the release and, outside production, the environment's name (the server's `APP_ENVIRONMENT`, which a deployment sets everywhere but production). A spec beside
the menu file fails on a page the routes carry and no menu places (`unplacedPages`).

How a tenant comes to exist is decided in the running application, apart from who may
enter one. Nobody creates a tenant directly: the record's create is refused, with a
sentence that says so. An administrator brings an organization in by inviting one person,
on the console's Invited tenants page (System), which the library draws from the staff
auth's generated `InviteStaffTenant`, `StaffInvitedTenants` and
`WithdrawStaffInvitedTenant`: the invitation carries the tenant's name and, where the
administrator says so, that the tenant is entered through its directory only and the role
its directory's people start with. Sending it takes the `Administrator` role's Create
grant on the record, and a name a tenant holds is refused there. No tenant exists while
the invitation waits; the invited person's first sign-in makes it under an id the server
generates, gives them the roles the App names in `TenantAdministratorRoles`
(`TenantAdministrator`), makes the directory they came through the tenant's where they
came through one, and hands the tenant to the roster, so their next request stands in
it. An invitation that says directory only is accepted through an organization's
directory alone, and one that still waits is withdrawn on the same page. The staff auth
is handed what makes the tenant by the data level (`staff.Settings.TenantMaker`, the
generated `app.NewTenantMaker` over the roster, built before the auth), and refuses to
start without it.

A person who signs in holding no tenant creates their own through the generated
create-your-tenant route (`POST /api/create-tenant`), which takes the tenant's name, writes the row under an id it generates and answers, gives them
the roles the App names in `TenantAdministratorRoles` (`TenantAdministrator`) and
hands the tenant to the roster, so their next request stands in it; a person who already
holds a tenant is refused, an administrator of one tenant never creates another, and the
console's dashboard offers the new-tenant dialog to a session without a tenant. The order is sign in
first, then name the tenant: the staff auth carries a Google registration as wide as any
Google account (`pkg/auth/staff/google.go`), and every sign-in method starts invite only.
An administrator opens one to sign-up on the console's Sign-in page (Open sign-up, which
says who that admits and asks for a typed word), and from then a stranger signs in
through it and creates their tenant; Make invite only closes it again, and the people
who have accounts keep them. The create-your-tenant route refuses while every method is
invite only, so nothing in code says whether a stranger may create a tenant.

Who may enter a tenant is the record's declaration, data of the application rather than
deploy configuration: the tenant's directory, the organization its people sign in
through (`TenantDirectory`, one row at most under
`/api/tenants/{tenantID}/tenant-directories`: a sign-in method and the connection its
identities carry, a Google Workspace by its domain or a Microsoft Entra tenant by its
tenant id) and whether its people enter through it only (`Tenant.DirectoryOnly`, which
closes the tenant to every sign-in but one through its directory). A tenant has one directory and a directory belongs to one tenant: the
table's two unique indexes hold both, and the generated write paths refuse first, each
with a sentence the person can act on. The staff auth's sign-in rule is
the library's tenant policy over it (`signin.TenantPolicy`, handed the generated
`app.NewTenantDeclarationReader`, which reads the declaration in the sign-in's own
transaction): a tenant's declaration restricts its own door alone, so a tenant that is
not directory only admits everyone it seats, however they signed in, a directory-only
tenant admits a sign-in through its own directory and, of its own administrators alone, a
password with a code sent to their email, and nothing else, and a person is
refused only when every tenant that seats them is closed to the way they signed in. A
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
tenant is admitted too, since
the record lets a stranger sign in first and create their own. Which tenants the session
then reaches is decided again per tenant where its tenant list is computed: the data
level's roster keeps every tenant's declaration beside its key
(`resource.TenantRoster.Declarations`, handed to `resource.SessionPermissions`), the
records' generated write paths reload it after their commit and signal every instance,
so a declaration edited anywhere decides the next request everywhere, and a tenant that
seats the person and that their session may not enter is listed as closed to this
sign-in, with the reason, and answers as one that does not exist. The
console's Sign-in page under the tenant (`/tenants/{tenantID}/sign-in`) shows the
tenant's directory, sets it and removes it, and changes everything else with the
tenant's own generated methods, each run through the library's method dialog under the
tenant's own grants (`TenantAdministrator`). Two say how the tenant admits its
directory's people: `OpenTenantToDirectory`
(`POST /api/tenants/{tenantID}/open-tenant-to-directory`) names the role each of them is
given at their first sign-in through the directory, a role the caller could give
directly, picked from the list of the roles they may give (`StaffTenantDirectoryRoles`),
and `MakeTenantInviteOnly` clears it. Two turn directory only:
`MakeTenantDirectoryOnly` (`POST /api/tenants/{tenantID}/make-tenant-directory-only`)
and `EndTenantDirectoryOnly`; the `Tenants` record, which the application's
`Administrator` writes, shows the flag and takes no write of it, and the page reads the
switch in the tenant (`GET /api/tenants/{tenantID}/directory-only`). The server allows
turning it on only once the tenant has its directory, and only to a person who is signed
in through that directory themselves and has a password of their own, which is their way
back in if the directory has a problem: a person who has only ever signed in through a
directory chooses one on Your account first, with a code sent to their confirmed address.
The page says both things before the person presses, in the server's own sentences (the
read of the switch carries them for the person viewing, with what they do about the one
they do not meet), and offers the button only where the tenant has a directory. Opening the tenant and making it directory only
each warn first: the dialog shows what the server says the call will do (who will
enter and with what role; who will be able to enter, the count of the people it would
keep out and, to a person who may list the tenant's people, their names as a list) and
holds its button until the person has typed the method's word (`open`, `close`); a
refusal is shown on the page and no dialog opens. Every sentence names the directory in
words a person knows, the Workspace's name or its domain as the staff auth's Google
registration gives it, and never by a connection that is an id (a Microsoft Entra
tenant its registration lists by id alone reads "your organization's Microsoft
directory", by the label of its button). Making the tenant invite only, turning
directory only off and removing the directory are plain confirmations; while a tenant is directory only its
routes answer the people it admits, so a tenant whose directory has stopped working is
opened again by its own administrator, signed in with a password and the code, or by a
super administrator. The tenant's People page marks the people it
keeps out for as long as it is on. Both records write change events, the audit of every
edit. Nobody types a directory in. It is set from a sign-in: an invitation that makes a
tenant, accepted through an organization's directory, sets it as the tenant is made, the
create-your-tenant route sets the one its creator came through, each refusing an
organization that already has a tenant, and afterwards the page offers an administrator who is
signed in through their organization's directory to make it the tenant's ("Make
acme.example this tenant's directory"), with a create that names no directory, since the
server sets the one the session came through. A session that came by password is
offered no such action and its call is refused, as is a personal account, which is no
organization's directory; the directory of a tenant that is directory only is not
removed. A person is given the directory's default role at their first sign-in through
it, whether their account is created then or stood before, once for the link between
the account and the directory. The staff auth's google registration admits any
Workspace account (its domain is the connection of the tenant's directory) beside any
Google account.

## Layout

- `main.go` serves the API and the console's built bundle.
- `pkg/config` is the configuration, as a chain of levels: core (every process), data
  (every process that opens the database), site (the served site). Each process
  constructs the level it needs — `cmd/bootstrap` and `cmd/deployment/migrate` stop at
  data — so a deploy step supplies exactly the variables its process reads.
- `app` holds the handlers: generated resource handlers plus the hand-written middleware
  and static-asset surface. `pkg/router` composes them behind the session.
- `pkg/resources` holds the resource structs the generator reads: `Tenant` (the tenant
  record, a global resource) and `Announcement` (tenant-scoped, `@domain` on its
  TenantId). `schema/migrations` holds the tables they describe; `schema/devseed` the
  development tenants; `pkg/auth/staff/roles.json` the staff auth's role file: the
  default roles the release ships, embedded in the binary and validated against the
  generated collection when the engine opens. A role is named for its job in PascalCase
  (`Administrator`, `TenantAdministrator`), never for where it is held: the file's
  structure says that. Every role carries a description, one or two plain sentences
  saying what it is for, which the lists of the roles (`StaffRoles`, and a tenant's
  `StaffTenantRoles`) answer beside the name; a role without one is refused. It authors
  the development logins' roles,
  `Administrator` over the tenant record and the whole application's side of the staff
  auth's generated management (`pkg/identity`), `TenantAdministrator` over every
  tenant-scoped resource and the management's tenant side; a domain role is
  held in every tenant domain, the seeded ones
  and those created later, so nothing provisions the file per tenant. An account is one
  row for the whole application, whichever tenants its person holds a role in, so the
  methods a tenant changes an account through (a new name, a new address, a delete and
  the rest) are granted to `TenantAdministrator` under a condition: the
  tenant holds the person alone (`heldElsewhere = false AND heldGlobally = false`).
  Without it one tenant's administrator could rename, disable or delete an account that
  also manages another tenant or the application. The address and the directory's subject of
  a person another tenant holds too are left out of the tenant's lists the same way
  (`heldElsewhere = false`), a role held in every tenant is not one tenant's to take
  (`everyDomain = false`), and an invitation is withdrawn while it stands
  (`pending = true`); `test/integration/tenant_people_test.go` proves each of them on the
  served stack. A new resource
  stays invisible to every login until a role in this file is granted it. Every grant in
  it is proven live by `test/integration/grants_test.go`, which opens the auth the way
  the deployment does and asks the engine about each unconditional grant; a conditional
  grant is proven by a test case that names it through `provesGrant`, and `impulse check`
  fails on one no case names.
- `pkg/identity` holds the staff auth's management of its people, written by the
  generator (`GenerateIdentity`) as code of this application; the generator owns the
  tree whole. It has two sides. The whole application's: the lists (`StaffSessionUsers`,
  `StaffUserRoles`, `StaffSessionMailLinks`, `StaffSessions` and the rest) and the methods
  (`InviteStaffUser`, `AssignStaffRole`, `SendStaffUserPasswordResetLink` and the
  rest). A tenant's, under the tenant segment and named with the tenant record:
  the lists of its people, the roles held in it, its invitations, its people's sign-in
  identities and the roles it gives (`StaffTenantUsers`, `StaffTenantUserRoles`,
  `StaffTenantInvitations`, `StaffTenantIdentities`, `StaffTenantRoles`), and the methods
  (`InviteStaffTenantUser`, `AssignStaffTenantRole` and the rest). All of it is served like every other resource and method and granted in the role
  file. The auth builds the client the handlers act through when it opens (`staff.New`,
  answered by `staff.Auth.Identity`) with the roles that count as an administrator's
  (`staff.AdministratorRoles`), so neither the application nor a tenant is left with
  nobody to give the next role, and sets the client's two hooks on its session manager,
  through which a sign-in gives a role in the transaction of the step that causes it:
  an accepted invitation's roles, and the default role of a tenant's directory.
  No auth is open without them. The console writes no page for any of it: the library builds
  them from the API description (`identityRoutes` in `web/console/src/app/app.routes.ts`).
- `pkg/deploy` holds the database steps a deployment runs: the schema migrations, then
  the role policy check (`CheckRoles`), which prints what the store holds that this
  release cannot use as written. Then `MigrateFeatures` brings the `FeatureFlags` table to the
  flags the release declares (section Feature flags). Between the two, `InviteAdministrators` invites each
  auth's first administrator while nobody of the auth holds the Super Administrator
  role, the foothold a deployed environment starts from: an invitation mailed to the address the placement names for
  the environment (`APP_STAFF_ADMINISTRATOR`), which gives the Super Administrator role
  and the development administrator's roles. The person opens the link and signs in by a
  method of the auth: they choose a password there and enter the code mailed to the address, or
  sign in through Google, where the invitation is what ties the sign-in to the roles,
  whoever else the method admits. No account is made ahead of them and no password is
  generated. A release leaves an invitation that stands as it is, mails a new one once
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
  the role invites nobody. `cmd/deployment/migrate` is the deploy step;
  `cmd/bootstrap` reuses it to stand up an emulator database, seeds the development
  tenants (tenancy is data), and adds the development logins from
  `cmd/bootstrap/users.json`, each with its memberships by where they are held: the
  administrator's domain role in every tenant domain (`everyDomain`), the member's in
  North alone (`domains`, where a tenant is named by its name and the bootstrap finds
  its id), so concealment is observable. Its test runs each auth's role
  file through the validation the engine performs when it opens (`access.ValidateRoles`)
  and pins the warnings the deploy would print as typed values, none expected: a warning
  is accepted by pinning it there, or the role is fixed.
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
- `test/authz` is the generated authorization matrix over the generated test router;
  `test/integration` drives the served stack (real router, session, engine) end to end.
  How a tenant comes to exist is held in both: `test/integration/invited_tenant_test.go`
  plays the invitation that makes a tenant from the sending to the first sign-in, and
  `test/authz/invited_tenant_test.go` holds the sending to the Create grant on the record.
- `web/` is the Angular workspace, one project per browser application (`console`).
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
`000007_FeatureFlags`, the library's own statements, which `impulse check` compares
with `resource.FeatureFlagsDDL`): the deploy's `MigrateFeatures` writes a new flag off,
keeps a known flag's state, and deletes what the release no longer declares, and every
flip is recorded in `FeatureFlagChanges`. The App reads the table when it is built
(`FeatureSet`) and follows it from `Start` (the live service's signals document, the
`features` kind, and the five-minute backstop reread), so the generated routes, decoders,
digest and features route answer from one copy. `GET /api/features` lists what is on to anyone signed in. Flipping is the generated
`SetFeature` method, held by the `FeatureAdministrator` role in the staff role file
(Execute on `SetFeature`, List and Read on `FeatureFlags`), which the development `admin`
login holds; the library's Feature flags page runs it: the list of the flags, each in words with who last turned it on or off, a row's menu that turns the flag on or off after asking, and a History button that opens the flag's flips in a pop-up. The menu file places the page (`featureFlagsPage`) in
the System menu, shown to anyone holding List on `FeatureFlags`. A flag's
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

That starts fresh Spanner and Firestore emulators, bootstraps the database (schema, the North and South tenants,
roles, the `admin`, `member` and `newcomer` logins with password `open-the-console-first`; `newcomer`
holds no tenant and creates one from the dashboard once `admin` has opened a sign-in
method to sign-up on the Sign-in page), and serves the API on
`$PORT` (:8092). The first run compiles and bootstraps before it listens; wait for
"Starting Server" in the server pane.

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
simulates the staff auth's Google registration: every sign-in through the Google button is
`APP_GOOGLE_USERNAME` from `.envrc` (`newcomer`, or `APP_USERNAME` when that is unset), no
directory is contacted, and only `APP_STAFF_GOOGLE_REDIRECT_URL` is read. A person the auth
has never seen is refused until an administrator opens the registration to sign-up on the
console's Sign-in page, and becomes a user under that name once it is open. To sign in through Google
for real, register the application there, fill in the `APP_STAFF_GOOGLE_*` variables, and
drop the tag. A deployed build never carries it.

### Browser apps

The console is an Angular workspace under `web/` that installs the published
`@impulseframework/resource` and `@impulseframework/resource-angular` (pinned in `package.json`). Install them
once with `bun install` in `web/`: it writes `bun.lock`, which the workflows install
from (`bun ci`) and the repository keeps, and `impulse check` fails while the web root
has none (`package-manager`). To develop
against a local checkout of the framework, attach its web packages through yalc, a local
state that is never committed (the script builds the three packages, publishes them to the
local yalc store, links them, and runs `bun install`; `weblib.sh restore` puts the pins back):

    (cd web && ./weblib.sh local)

`weblib.sh` expects the framework checkout beside this application as `impulse`; set
`IMPULSE` to point elsewhere. Then `overmind start` runs everything, with `ng serve` for the console at
http://127.0.0.1:4301 proxying `/api` to the server.

The browser files are held to the prettier the workspace pins (`.prettierrc`): `impulse
check` names a file prettier would change, and `impulse check --only prettier --fix` formats
it. Run that once after the first install: the files were written under this application's
names before the formatter was there to wrap their lines.

After a change in the framework's web packages, push the rebuilt packages, restart the dev
server, and reload the page:

    (cd web && ./weblib.sh push)
    overmind restart console

The dev server does not watch `node_modules`, so the restart is what picks the new build up.
The two packages are bundled with the application code rather than prebundled by Vite (the
`prebundle` exclusion in `angular.json`), so a plain reload shows the new build in any
browser profile: nothing is held behind an immutable URL, and no cache needs clearing.

The console installs as a progressive web app. The production build emits a service worker
(`@angular/service-worker`, configured by `web/ngsw-config.json`) that keeps the files of the
build an open tab loaded, so a release never breaks a tab still running the previous build, and
the library's update notice offers Reload when a new build is ready; the worker is off under `ng
serve`. The manifest and the icons sit in `web/console/public/` (`manifest.webmanifest`,
`icons/`): a placeholder set, the skeleton's mark, until the application's own logo is in place.

The brand is declared once, in `web/console/brand.json`: the application's name, its logo
(`logo.png` beside the declaration, a square PNG at least 512 pixels a side; `logoSvg` names the
same logo as an SVG, which then is the favicon as it is), a primary color and, optionally, a
background color. `impulse brand` writes from it the favicon and the app icons, the manifest's
names and colors, the page's title and `theme-color`, the name the frame draws, the theme's
first palette (generated from the primary color by Angular Material's own generator, once the
workspace's packages are installed, into `src/_theme-colors.scss`), and what the server says:
`pkg/brand`, the name and the logo `main.go` hands to the maintenance page, and the name the
mail says, the `Application` constant of `pkg/auth/staff`. The declaration the skeleton carries
names the placeholder: put the application's own logo in its place, set the name and the colors,
and run `impulse brand`, then again whenever the declaration changes; `impulse check` (`brand`)
reminds while the placeholder is still there.

The server serves every bundle through the resource package's `BrowserApp`, which sets
the cache headers a worker needs; a build deployed without `ngsw.json` makes every installed
worker unregister itself and drop its caches. `impulse check` (`installable`) holds the wiring.

The production build stamps the release into the console (`VERSION=1.2.3 bun run build`;
`bun run build` alone stamps `dev`, as the dev server does), and the application sends it in
`X-Api-Version` with every request. The server answers releases from the outlet's `OldestAnswered` (in
`cmd/generate`) up to its own `APP_VERSION` and refuses the rest with 412, which the update
notice turns into a reload onto the current build; a `dev` build on either side checks
nothing.

The console's component specs run on Angular's unit-test builder (`@angular/build:unit-test`)
with Vitest under jsdom in Node: no browser, no Karma. `bun run test` runs them once, the
form the Checks section and CI use; `bun ng test console` watches. The specs beside the
skeleton's components are the pattern for the application's own: the dashboard renders a
permission digest over a scripted client from `@impulseframework/resource-angular/testing`
(`provideResourceTesting` puts the generated client on a transport that records every
request and answers from the test); the new-tenant dialog posts to the create-your-tenant
route and reports the server's refusal in place; the tenant's sign-in page has its own; and the
menu file's spec holds every page the routes carry to a place in the menus. The frame
itself (the tenant guard, the switcher, the menus and the person's menu) is the library's,
tested there.

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
through `LiveService()`, and the generated router serves the live routes on the session outlet (`/api/live/renew`, `/api/live/unsubscribe`, `/api/live/token`). The
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
rereads the table; the staff auth's permission engine is constructed with
`access.WithChangeSignal` over the `policy` kind (`pkg/auth/staff`), so a role, grant or membership written on one
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
                                  # Google registration, and the sign-up test skips without it
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
