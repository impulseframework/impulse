# impulse

`impulse` is the command-line tool for Impulse applications: apps built on the Impulse
Framework, the one Go module `github.com/impulseframework/impulse`, with its `resource`
package at the center.

The tool keeps no record of its own. Everything it needs it reads from files the
application already depends on: the generator program, `go.mod`, the browser apps'
`angular.json`, the process files, the test harnesses, and the config struct tags.

## Install

```sh
go install github.com/impulseframework/impulse/cmd/impulse@latest
```

Inside an application, impulse is the `tool github.com/impulseframework/impulse/cmd/impulse` directive in
`go.mod`, and `go tool impulse <command>` runs it at the version go.mod pins; the
installed binary is for creating applications and for development. The tool lives in the
framework module, so an application's `go.mod` has one framework requirement,
`github.com/impulseframework/impulse vX.Y.Z`, and that one version (the framework pin)
pins the libraries, the generator and the tool together.

## Vocabulary

The words the tool uses on the command line, in check reports, in handoff briefs, and in
the code it lays in. They are frozen: one test holds the command surface (every command,
flag, and check name) to a golden file, and another keeps the retired words out of the
templates, this README, and the tool's source.

- **Application**: one Go module built on the framework's `resource` package, with one database, one schema,
  and one deployment. Everything below is a part of one application.
- **Site**: a stand-alone server on a host of its own: a main package, handlers, a router,
  resources, and a browser application. Every application has at least one site.
- **Layout**: how the sites sit in the tree. **flat** has the one site at the root
  (`app/`, `pkg/`, `web/`); **sites** has every site under `apps/<site>/` with the shared
  packages at the root. An application starts flat and is promoted to sites when it grows
  a second site, and one left with a single site keeps the sites layout. "Multi-site"
  describes a deployment with several hosts, never a layout.
- **Outlet**: a second URL space on the same host, declared in the generator program. A
  session outlet is a browser surface bound to an auth; a machine outlet is a surface
  with no browser whose requests carry the bearer tokens an auth issues. The default
  outlet is the one `GenerateRoutes` declares. A browser outlet
  takes one shape: its application at its own mount path (`WebApp("/portal")`) with its
  API under it (`/portal/api`), so the application's scope covers its own API, login,
  and callback routes. One application alone may sit at `/`; with two or more none does,
  since an installed application's scope is every URL under its start and one at `/`
  would own the origin, and the generated router answers the root alone with a redirect
  to the default outlet's application (`/console/`). The `outlets` skeleton carries the
  shape: the console at `/console` on `/console/api`, the portal at `/portal` on
  `/portal/api`.
- **Auth**: a population that holds roles in its own permission store and signs in
  through the methods registered on its Auth session. It is a package, `pkg/auth/<name>`,
  whose name prefixes its tables and cookies and whose role file (`roles.json`, embedded
  in the package) holds the release's default roles for it. A role is named for its job
  in PascalCase (`Administrator`, `TenantAdministrator`, `FeatureAdministrator`), never
  for where it is held: the file declares that by its structure (a global role under
  `global`, a domain role under `domain`, held in every tenant), and the auth's generated
  management serves it as the role's `kind`. A role name is letters and digits starting with
  a capital letter; the permission engine refuses any other name, and a custom role made
  at run time follows the same rule. Every role carries a `description`, one or two plain
  sentences saying what the role is for, which the management serves beside the name; a role
  without one is refused, so the file does not start the release (the `roles-file` check
  reads it first).
- **Method**: one way an auth's people sign in, registered on the Auth under a
  registration name that is the segment of its routes (`user/<registration>/login`):
  `password` (always registered as `password`), `azure` (Microsoft Entra over OpenID
  Connect), `google` (Google Workspace over OpenID Connect) or `oidc` (one
  organization's identity provider over OpenID Connect, known by its issuer alone, such
  as Okta or Keycloak). An auth may register
  several, each in a file of its own in the package; the login page composes the
  library's login component, which renders from the methods route, so a method
  registered and declared is a button offered. The console writes no page for the
  auth's people: the generator program generates their management (`GenerateIdentity`:
  the accounts, the roles they hold, their sign-in identities, the invitations and
  the sessions as lists, and the methods that change them), describes it in the API
  description, and the console's routes ask the library for its pages
  (`identityRoutes`), each listed in the navigation where the permission digest
  grants List on its resource, each row offering the methods the caller may run on
  it; a tenanted console asks for the tenant's side too, under the tenant's URL.
- **Authority**: who owns role membership for a directory method: `directory` (the
  directory's roles are synchronized at every sign-in) or `application` (roles are
  assigned in the application).
- **Domain** and **tenancy**: a domain is the scope a permission is granted in. Tenancy
  makes a table the domain universe: its resource struct is the tenant record (`@tenant`),
  from which the generator derives the tenant segment tenant-scoped resources live under
  and emits the constructor of the tenant roster, the tenants as the running application
  knows them, reloaded on the `tenants` signal so a tenant made on any instance is
  usable at once, without a restart; a role held in every domain reaches every tenant. A
  tenant's key is an id generated when the tenant is made, never a word a person types:
  it is the domain of every role held in the tenant and the tenant segment's value in
  every address, so it never changes and is never used twice, while the tenant's name is
  a field its administrators change, kept from a second tenant by a unique index. A
  deleted tenant takes the roles held in it, its standing invitations and its directory
  with it, and the rows of every tenant-scoped table whose foreign key to the tenant
  table says `ON DELETE CASCADE`, as the skeleton's sample table does; `impulse check`
  fails on a table that carries a tenant's key without the rule, and on a table that
  reaches its tenant through another row where a link of that way lacks it
  (`tenant-delete-rule`). A
  session's tenant list is the roster filtered by where it holds a grant, and a tenant
  is offered only when the session may enter it: the application's tenant declaration
  (what the tenant declares about who may enter it, its directory and whether it
  admits its people through it only) decides against the way the session was
  established. A declaration restricts its own tenant alone: a session reaches every
  tenant that seats the person, except a tenant that is directory only and whose
  directory is not the one they signed in through, which stays in the list marked
  closed to this sign-in, with the reason, so the picker never offers a tenant the session cannot enter and a direct
  request for one is refused as a tenant that does not exist. The declaration is two
  things on the tenant record: the tenant's directory, the organization its people sign
  in through (a child record, `@tenantDirectory`, one row for a tenant and a directory
  in one tenant's row only, set from a sign-in through it and never typed: a sign-in
  method and the connection its identities carry) and whether its people enter through
  it only (a column, `@directoryOnly`, which closes the tenant to every sign-in but one
  through its directory, and closes no other tenant); each auth's sign-in rule
  (`signin.TenantPolicy` over the generated `New<Record>DeclarationReader`) reads it at
  sign-in, the roster keeps a copy for every request, and the records' change events are
  the audit. The skeletons' consoles carry it on the tenant's Sign-in page.
  The browser application mirrors the API: every tenant-scoped page's URL names the
  tenant by its id, under the same segment (`/tenants/<id>/users` beside
  `/api/tenants/<id>/staff-tenant-users`; the generated client exports the segment as
  `DomainRoutePath`), so a link to a tenant's page can be shared and a bookmark lands
  where it points, and global pages keep their routes. The tenant a page works in is a
  consequence of its URL, never a choice: the library's guard on the tenant segment sets
  it before the page loads, from the session's tenant list, and nothing else writes it.
  The frame's place switcher lists that same list by the tenants' names and opens the
  same page under the chosen tenant; a URL naming a tenant the session cannot reach lands
  on a refusal page that shows the server's answer, never on another tenant.

  How a tenant comes to exist is apart from who may enter one, and no declaration.
  Nobody creates a tenant directly: the record's create is refused with a sentence that
  says so. Staff bring an organization in by inviting one person, on the Invited tenants
  page the library draws from the auth's generated management: the invitation carries
  the tenant's details as staff entered them, held to the record's own create under the
  sender's Create grant when it is sent, and the invited person's first sign-in makes
  the tenant, makes them its administrator and, where they came through their
  organization's directory, makes that directory the tenant's. A person who signed up
  holding no tenant creates their own through the generated create-your-tenant route
  and becomes its administrator. There the order is sign in first, then name the tenant,
  so the route serves only while an administrator has a sign-in method of the outlet's
  auth open to sign-up, which is chosen in the running application, on the auth's
  Sign-in page.
- **Levels**: the configuration chain in `pkg/config`, one level per kind of process, each
  embedding the one below it: **core** (every process), **data** (every process that opens
  the database), and **site** (one served site: its port and its built bundle). The site
  level is `SiteConfiguration` in both layouts, since a flat application is one site.
  The core level opens the cloud driver (`cloud/gcp`), which builds where the process's
  logs and spans go from the settings the level embeds (`cloud.Settings`: the logging
  project and the trace sampling); the generated router installs tracing and the request
  logger from the App's `LogExporter`, so nothing in the application names a logging
  client or a trace provider. The data level opens the database driver
  (`resource/database/spanner`) and the live driver (`resource/live/firestore`), and
  the site level the job driver (`resource/jobs/cloudrun`), each driver's `Settings`
  embedded in a wrapper named for the kind (`DatabaseSettings`, `LiveSettings`) and
  opened with its `Open`. Every driver is bound under one neutral import alias
  (`cloud`, `database`, `liveservice`, `jobstarter`), so the application names the
  vendor on the import line alone, and another provider is another import line.
- **Live pages**: list pages and record pages that stay current without polling. A
  request the page asked to be live carries `X-Subscribe`; the server registers the
  subscription before the query runs and publishes each commit's rows into the
  subscribers' change sets, and the browser refetches with `_v`, an answer it caches for
  five minutes. The server side is the data level's Firestore settings
  (`APP_FIRESTORE_DATABASE` in a deployment, `FIRESTORE_EMULATOR_HOST` in development)
  and the App's `LiveService`; the generated router serves the live routes
  (`live/renew`, `live/unsubscribe`, `live/token`) on every session outlet. The live
  service is required: every application wires one, the data level refuses to start
  with neither a database nor the emulator configured, and the test harnesses wire
  `live.NewFake()`. The skeletons carry the server side, the Firestore emulator in the
  Procfile, and the rules and indexes the database needs under `schema/firestore`; the
  browser side is the client packages' live option, which a page opts into.
- **Change signal**: the one channel an application's instances tell each other on that
  something shared changed, carried by the live service: one document per application,
  `application/signals`, with a field per kind of change (`features`, `tenants`,
  `policy`; `resource.SignalKind`), written by `Signal(ctx, kind)` and followed by
  `Subscribe(kind, onSignal)`. A signal carries nothing but the fact of a change; every
  subscriber rereads what it keeps, and rereads at its own backstop regardless. A
  feature flag flip rides the `features` kind, and every permission engine rides the
  `policy` kind: each `access.New` takes `access.WithChangeSignal` over the live
  service, which an auth package builds from the `Settings.Signals` the data level
  passes (`access.ChangeSignalFunc(announcePolicy(signals), watchPolicy(signals))`), so
  a role, grant or membership written on one instance reaches every instance's snapshot
  at once rather than at the engine's next heartbeat. The `change-signal` check holds
  every engine to it.
- **Feature flag**: a release switch owned by the application: a feature's code ships
  before the feature is turned on, the switch differs per environment, and the flag is
  removed once the feature is permanent or abandoned. A flag is a `resource.Feature`
  constant in the resources package (`const Debriefs resource.Feature = "debriefs"`, the
  doc comment its description, declared by `impulse add feature`); `@feature(Debriefs)`
  gates a resource, a field or a method, and off means absent (404 on the routes, left
  out of the permission digest, the field unknown). Its value lives in the application's
  `FeatureFlags` table, one row per environment, written by the deploy's
  `MigrateFeatures` from the declarations and flipped through the generated `SetFeature`
  method, which the `FeatureAdministrator` role in the roles file holds; the development
  seed's row (`schema/devseed/..._dev_feature_flags.up.sql`) is the flag's state in
  development and the test environments. Per-user and per-tenant enablement are
  permissions, not flags.
- **Warning**: a schema finding the generator raises on every run and prints as a
  `Warning:` line, never a refusal. Most are performance matters the application decides
  (an index a listed tenant-scoped resource wants, a tenant resolved through a join
  path, an enumeration table too large to bake). One is about access: a table a fact
  about people is read through that no resource declares. The generate program's
  warnings test pins the accepted set as typed values, so a new warning fails the
  application's CI until it is fixed or pinned.
- **Audit finding**: an advisory finding about a shape the framework handles under a
  stated limitation (a table storing files whose rows the database deletes by cascade),
  printed as an `Audit:` line only when asked: the generate program's `-audit` flag, or
  `impulse audit`, which runs every program that way. A normal generation never prints
  one, and no test pins them.

## impulse new

`new` creates an application: the base skeleton (flat layout, one auth, nothing else on)
rendered under your module path, with the first auth named by you. The application is
named after the module path's last segment unless `--name` says otherwise: the name is the
web package (`<name>-web`), `APP_SERVICE_NAME`, and the development Spanner project,
instance, and database in `.envrc.template`.

```sh
impulse new ../beacon --module example.com/acme/beacon --auth staff
impulse new ../harbor --module example.com/acme/harbor            # asks for the auth name
impulse new ../harbor --module example.com/acme/harbor --auth members --dev-root ../impulse
impulse new ../svc --module example.com/acme/beacon.service --auth staff --name beacon   # the last segment is not a name
```

An auth is a population that signs in one way and holds roles in its own permission
store, and it is a package, `pkg/auth/<name>`, whose name is also the prefix of its tables
(`<Name>Sessions`, `<Name>Roles`, ...), its cookies (`<name>` for the session, `<name>-xsrf`
for the XSRF token, so two auths on one host never overwrite each other's), and the stem of
the home of its role file (`pkg/auth/<name>/roles.json`, embedded and handed to the permission
engine as the release's default roles). The web app that binds to an auth names the same XSRF cookie in its
HttpClient configuration (`withXsrfConfiguration`), since the browser echoes that cookie
in the `X-XSRF-TOKEN` header. So the name is asked for when `--auth` is not given, and there is no default: a default word would
land in every application whose author skipped the question. A good name is the population
that signs in, plural, lowercase, one word: staff, members, partners, devices. It cannot be
a Go keyword, a package the application imports, or a directory it has.

`.envrc.template` carries the development ports (the server's, the Spanner emulator's,
and the Firestore emulator's, which the render report states) and, commented out, the
Firestore variables the live pages read, which a deployment supplies:
`APP_FIRESTORE_DATABASE`, `GOOGLE_CLOUD_FIRESTORE_PROJECT` (the database's project) and
`APP_FIREBASE_API_KEY`. A process given the database without its project refuses to start
and names both variables, since the database is not assumed to be in the Spanner project
(where environments share a Spanner instance, that project is the shared instance's). In
development the emulator stands in through `FIRESTORE_EMULATOR_HOST` and takes the Spanner
project; with neither a database nor the emulator set the process refuses to start, since
the live service is required.

The application's `go.mod` requires the framework module, `github.com/impulseframework/impulse`,
once, and carries the `tool github.com/impulseframework/impulse/cmd/impulse` directive,
which names impulse's package in that module; CI's `go tool impulse check` runs at the
version the requirement names. That one requirement is the framework pin: the libraries,
the generator and the tool are one module, so one version pins them all. `new` sets it to
the impulse that created the application, so the templates the application starts from and
the check its CI runs are the same code:

- An impulse release pins its own version, and the web roots pin the browser packages
  (`@impulseframework/resource`, `@impulseframework/resource-angular` and
  `@impulseframework/resource-firestore`) at the release without its `v`, since the
  packages release with the module at its version.
- An impulse installed from a commit (`go install github.com/impulseframework/impulse/cmd/impulse@<commit>`)
  pins that commit's pseudo-version, the version Go gives a commit that has no release tag
  (`v0.0.0-20261004053859-89401d630235`); the browser packages take the latest release's
  version, since a commit publishes none.
- An impulse built from a checkout (`go build`, `go run`) names no commit the module proxy
  serves, so `new` refuses it before writing anything and says to install impulse from a
  release or a pushed commit, or to name the checkout to build against with `--dev-root`.

The templates carry a placeholder (`v0.0.0-skeleton` in `go.mod`, `0.0.0-skeleton` in each
`package.json`), which the rendering fills with the version. `new` then resolves the pin
with `go get -tool github.com/impulseframework/impulse/cmd/impulse@<version>`, through the
module proxy: Go fetches that impulse, verifies it against its checksum database, raises
any requirement the application has that the framework needs higher, and records the
checksums in `go.sum`; `go mod tidy` settles the rest. Both run with `GOWORK=off`, so a
workspace above the directory stays out of it. With `--dev-root <checkout>` the pin is the
zero pseudo-version and `go.mod` gains `replace github.com/impulseframework/impulse =>
<checkout>` (a relative path when the application lives beside the checkout), only
`go mod tidy` runs, and the application builds against the checkout; that replace line is
for development and never for committing.

The templates' generated files are the generator's own output under the templates' auth
name, `staff`, and the rendering renames the auth in them as text. The generator orders
what it writes by name and wraps a long list at a width, so under another name it writes
some of those files in another order and with other line breaks. Under any name but
`staff`, `new` therefore runs the application's generators once (`go generate ./...`)
after the requirements are settled and before the first commit: the application is
committed as its generators write it, a regeneration right after changes nothing, and
`impulse check`'s `regen` passes as the application was made. The generators read the
schema through the Spanner emulator, so a container runtime has to be running, as for
every `impulse add`. A regeneration that fails stops `new` before the first commit, with
the generator's output and the tree left rendered.

The tree is committed as the application's first commit (`--skip-git` leaves it
uncommitted), so `impulse add` can start from a clean tree. `new` installs nothing, and
two things follow, each a step it prints. The application has no lockfile: the versions
one would lock are the release's own, written into `package.json` by the rendering, so
`bun install` in each web root writes `bun.lock`, which the workflows install from
(`bun ci`) and the application commits; `impulse check` fails until every web root has
one (`package-manager`). An application made with `--dev-root` is told to run
`./weblib.sh local` instead, which links the browser packages built from the checkout.
And the browser files written under the application's own names were not wrapped again
by the web root's prettier, which the install brings: the steps say to run
`impulse check --only prettier --fix` once after it, which wraps again any line the names
made longer or shorter and changes nothing where they left every line as it was (names
as long as the skeleton's do); `impulse check` fails on a file that needs it (section
impulse check, `prettier`). Options come afterwards, one reviewable change each:
`impulse add tenancy`, `impulse add outlet`, `impulse add auth`, `impulse add files`,
`impulse add job`.

Options can be composed into the creation: `--tenancy` (with `--tenant-table`, default
`Tenants`), `--outlet <name>=<prefix>` (repeatable; `--api-outlet` for a machine surface),
`--files` (the file store, section add files) and `--site <name>` (two or more, the first
being what the base site becomes under `apps/`) run the same transitions `impulse add`
runs, on the fresh tree in order, tenancy first, the file store after the outlets and the
sites last, and end in one check and one handoff brief carrying every obligation, so the agent wires the whole shape in one sitting (`--agent` launches it). The
first commit is the base alone, so the composed options are one reviewable diff on top of
it; the brief's reference is the `sites` skeleton when sites are composed, `outlets` when
an outlet is, else `tenanted`.

The first auth signs in with a password; a directory method joins it afterwards with
`impulse add method` (section add auth), registered beside the password method on the
same Auth, so nothing about the auth's tables moves. Two sign-in shapes have no password
sign-in, and an application started from either (`--shape`, below) has its first auth
written with the shape's methods in the password's place: no password method, no
password form on the login page, no password on a person's account page, and development
logins that hold no password.

`--one-tenant-per-person` goes with `--tenancy` and says a person belongs to one tenant
(section add tenancy); without it a person can be in several. `--tenant-directories` goes
with `--tenancy` too and says whether a tenant can have a directory of its own; it is on
unless `--tenant-directories=false` is given (section add tenancy). `--shape <name>` starts the
application from a sign-in shape by name (section impulse audit lists the eight): `new`
takes the four an application with tenants has, `tenant-owned-users-invite-only` (5),
`tenant-owned-users-by-directory` (6), `global-users-invite-only` (7) and
`global-users-each-tenant-chooses` (8), and `directory-roles` (3), the one shape of an
application without tenants that has no password sign-in. A shape with tenants composes
`--tenancy` and fills in the
tenants' choices: the two tenant-owned shapes hold a person to one tenant, and in the two
global ones a person can be in several. The two invite-only shapes write an application
whose tenants have no directories (`--tenant-directories=false`): a tenant's people come
by invitation alone, a directory method added afterward with the application giving
the roles is a way for an invited person to sign in and nothing more, and `impulse
audit` still reads the application as the invite-only shape. The other two write the
tenant directories. For `global-users-each-tenant-chooses` the command says
which `impulse add method` adds the directory method a tenant's people come through;
until it is added `impulse audit` reads the application as the invite-only shape beside
it. A flag that contradicts the shape is refused before anything is written, naming both
(`--shape global-users-invite-only --one-tenant-per-person`, `--shape
global-users-invite-only --tenant-directories`, `--shape` with `--tenancy=false`), and a
name the command does not know is refused with the names it knows. The other three shapes
of an application without tenants are told apart by a sign-in
method's choices on an auth that has the password method, so `impulse add auth` and
`impulse add method` take those.

`--shape tenant-owned-users-by-directory` writes the first auth's sign-in methods too,
since every person of a tenant signs in through the tenant's own directory: a Google
method open to any Google Workspace and a Microsoft method open to any Microsoft Entra
tenant, neither with personal accounts, with the application giving the roles, and no
password method. Nothing is asked about a provider, and no flag takes one provider or
another width; another width is the author's change to what was written, and `impulse
audit` names it in a sentence. The application is registered at both providers before
it deploys, since each method needs a client id and a secret of its own. The
application's own people, its first administrator among them, come by invitation and
accept it through either method. The command also registers the persona method, the
sign-in of the test environments, with a cast of one persona, `admin`: a development run
signs its administrator in as that persona, and the bootstrap seeds no password account.

`--shape directory-roles` writes the first auth with one sign-in method, a directory's,
with the directory giving every role, and no password method. `--method` names the
directory, `google` (a Google Workspace) or `azure` (a Microsoft Entra tenant), with
`:<registration>` to name the registration, or `oidc:<registration>` (another identity
provider, which always takes a name), and `--organization` names its one organization, a
Workspace by its domain, an Entra tenant by its tenant id or another identity provider by
its issuer, an `https` URL with no query or fragment (`https://id.fleet.example`), which
becomes the development value of `APP_<AUTH>_<METHOD>_ISSUER`; each is asked for when it
is not given, and refused as missing where nobody is at the terminal, and an organization
that is not what its provider knows one by is refused before anything is written.
No first administrator is invited and nothing in the application gives a role, so the
application holds nothing that invites one: the deployment package's
`InviteAdministrators` and `ReinviteAdministrator`, the migrate command's invitation
after its role check and its `-reinvite-administrator` flag, and the
`APP_<AUTH>_ADMINISTRATOR` setting with its variable in the environment template are
taken out of what the base writes, and the change says so. `impulse add auth` writes
them for an auth added later that invites a first administrator. The
application mails although it invites nobody: a sign-in the directory does not answer
for with a second factor waits for a code mailed to the person's address, and through
Microsoft each person's first sign-in waits for one whatever the directory says, since
Microsoft Entra ID vouches for no address and the code is what proves the one the person
gives. So each deployed environment needs its mailbox (`APP_MAIL_MAILBOX` with
`APP_MAIL_APP_PASSWORD`) though no release asks for one, and the brief says so; without
it the code is written to the server's log and reaches nobody. A
development run signs in the person `APP_USERNAME` names with the roles `APP_ROLES`
names, both in the environment template, through the simulated directory. The shape has
no tenants, so it is refused beside `--tenancy` and the tenants' flags, and `--method`
and `--organization` are refused with any other start.

The tests the base starts with are written for a password sign-in, so either start
writes the application's tests for the sign-in it has. The served suite
(`test/integration`) signs in through the simulated directory, each test proving what
the base's test of its name proves: run it as the Procfile and CI do, with `go test
-tags skipAuth ./...`, since under the default build the cases that sign in are skipped
and say so. The base's tests of what the auth does not have are taken out, and the
change names each with the reason: a password reset and a sign-up in both starts, and
with `--shape directory-roles` the first administrator's invitation and the groups too.
With `--shape directory-roles` the deployment's tests (`pkg/deploy`,
`cmd/deployment/migrate`) lose the cases of a first administrator, the test of what the
deployment reports about an invitation goes with what it tested, and the migrate
command's test proves that a release invites nobody and that the command takes no
`-reinvite-administrator`; with `--shape tenant-owned-users-by-directory` the two harnesses
(`test/integration`, `test/authz`) build the tenant roster and the generated maker, and
the first administrator's invitation is proven accepted through Google. The new
application passes `go vet` with and without the tag as it is made. The spec of the
browser application's login page is written for the same sign-in: it answers the methods
route with the methods the auth registers, and expects a button for each and no password
form. In a start with tenants the two served suites run as the application was made,
since the command writes the role a tenant's administrator holds, without which the
application's router is not built (`impulse add tenancy`, below, says which grants it
carries). A start with tenants that keeps the password sign-in (`--tenancy`, or one of
the other three shapes with tenants) has its tests brought up to its tenants as well:
the two harnesses are written again, the base's two tests of sign-up are taken out and
its test of a password reset opens sign-up with no role (`impulse add tenancy`, below),
so it too passes `go vet` and its own tests as it is made.

```sh
impulse new ./harbor --module example.com/harbor --auth staff --tenancy --outlet portal=portal/api --agent
impulse new ./fleet --module example.com/fleet --auth crew --site console --site portal
impulse new ./depot --module example.com/depot --auth staff --shape tenant-owned-users-by-directory
impulse new ./ferry --module example.com/ferry --auth staff --shape directory-roles --method google --organization ferry.example
impulse new ./quay --module example.com/quay --auth staff --shape directory-roles --method oidc:okta --organization https://id.quay.example
impulse new ./yard --module example.com/yard --auth staff --tenancy --tenant-directories=false
```

The application ships its CI, and impulse owns it. Three workflows are rendered from the
code: `.github/workflows/ci.yml`, the checks on every pull request;
`.github/workflows/ci-cache.yml`, the run after a push to the default branch (`main` or
`master`, or the one the `//impulse:ci` line declares) or a `patch/` branch that fills
the caches the checks restore; and `.github/workflows/security-scan.yml`, the daily run
of the vulnerability check and the image scan over the default branch and the latest
release, which reports to an issue of the repository (below). `impulse new`
writes them at creation, `impulse render` (with no arguments, inside the application)
rewrites them, `impulse add site` and `impulse remove site` rewrite them as part of their
change since the browser workspaces change, and `impulse check` (`ci-workflow`) compares
the committed files with the same rendering, so a hand edit fails the check. The pull
request workflow is plain jobs. No job calls a reusable workflow of another repository,
and each job's id is the check name the pull request reports, so a repository rule can
require them by name: six are fixed (`title`, `go`, `web`, `image`, `secrets`,
`migrations`; bedrock reads the list from impulse's `ci` package and its organization
layer requires them), and the browser jobs between `go` and `web` are named per workspace
and gated by `web`. The pull request is the one gate for every change, whoever opens it,
so the gate covers every job:

- `title`: the pull request's title is a conventional commit line, the one the squash
  merge carries and release-please reads, and its type decides what the merge does. A
  merge titled `feat` or `feature` releases a minor version; one titled `fix`, `perf`,
  `revert`, `docs`, `deps`, `upgrade`, `infra` or `config` releases a patch; a merge of
  only the hidden types (`style`, `chore`, `refactor`, `cleanup`, `test`, `build`, `ci`)
  opens no release pull request and reaches no environment until a releasing merge
  follows. The seeded `release-please-config.json` carries a changelog section per type
  (bedrock's `check` refuses one without), since release-please drops a merge whose type
  has no section exactly as it drops a hidden one; an application seeded before `upgrade`,
  `infra`, `config` and `cleanup` had sections adds the four by hand.
- `go-build`, `go-test`, `go-test-skipauth`, `go-lint`, `go-lint-skipauth`, `go-vuln`,
  `go-semgrep` and `go-check`, the Go legs, run at once and each reports on its own: the
  module builds and vets without tags and with `skipAuth` (the tag that simulates the
  directory a directory method signs in through); its tests run under the race detector, one leg
  per tag; golangci-lint runs at the version the skeleton's `.golangci.yml` is written
  for, one leg per tag; govulncheck; Semgrep over the findings the pull request introduces
  (its baseline is the pull request's base branch, so a patch release-line pull request diffs
  against its own base); and `go tool impulse check` with its regeneration (the generators
  start the Spanner emulator in a container, which the runner has), failing on a tree the
  checks changed. `go-check` installs every browser workspace first (`bun ci`, as the
  workspace's own job does), so the `prettier` check runs there and a browser file that is
  not formatted fails the pull request: the formatter comes with the install, and where
  the packages are not installed the check only says it was not run. The install is in
  this job and not the check in the workspace's, since the job has Go and the tool
  already and an install costs seconds, where a workspace's job would have to set Go up
  and compile the tool to run one check.
  The pins fix the engines, not what they know: govulncheck reads
  vuln.go.dev, Grype (in `image`) downloads its vulnerability database and Semgrep fetches
  its registry rules when the job runs, so a workflow at an old pin still finds a
  vulnerability published after it; TruffleHog's detectors, golangci-lint's linters and
  the actions move with impulse releases.
- `go`: the gate over the Go legs, one fixed name a repository rule can require. It needs
  every leg, runs whether they passed or not, and fails when any of them did not succeed.
- The larger runner: the jobs that take the most machine run on the runner the GitHub
  Actions variable `CI_LARGE_RUNNER` names, a runner label or a runner group set on the
  repository or the organization (an organization sets it once for every application;
  bedrock's organization placement declares it, `ciLargeRunner`). While the variable is
  unset they run on GitHub's standard runner, as every other job does. Which jobs: the
  two test legs and `image` unless the application's `//impulse:ci` line says otherwise
  (below).
- `angular-<workspace>`, one per browser workspace (`angular-web` for the flat workspace,
  `angular-<site>` for a site's at `apps/<site>/web`): Bun at the version that wrote
  `bun.lock` installs from the lockfile exactly (`bun ci`), then the package scripts build,
  lint and test.
- `web`: the gate over the browser jobs, one fixed name a repository rule can require
  where the workspace jobs' names vary per application. It needs every `angular-<workspace>`
  job, runs whether they passed or not, and fails when any of them did not succeed, so a
  failed browser build, lint or test blocks the merge; an application without a browser
  workspace gets a `web` that needs nothing and passes with nothing to check.
- `image`: once the application has a Dockerfile (bedrock seeds it), hadolint over it, the
  build, and Grype over the built image, failing on a high or critical vulnerability. The
  build's inputs are read from the Dockerfile when the job runs: every `ARG` without a
  default (`VERSION`, `COMMIT` and `PROGRAMS` aside, which the job passes itself) is
  passed as `ci`, and every build secret a `RUN` line mounts
  (`--mount=type=secret,id=NAME`) is served from a placeholder file holding `ci`, so a
  Dockerfile that requires a secret builds on the pull request without the real value.
  `PROGRAMS` is the shipped programs the Dockerfile compiles into the image, one word per
  program as `<dir>=<os>/<arch>[,<os>/<arch>...]` (empty when no outlet ships one),
  composed from the generator programs' `ShipsProgram` declarations when `impulse render`
  writes the workflow, so the job proves the shipped programs compile, and a change to the
  declarations is followed by `impulse render` (`ci-workflow` holds the file to it). The
  image is built to be scanned, never deployed. Without a Dockerfile the job passes with
  nothing to build, so the check exists on every pull request.
- `secrets`: TruffleHog over the whole history reachable from the pull request's head; a
  secret confirmed live, or one whose check could not finish, fails.
- `migrations`: against the base branch, `schema/migrations` gains files only; a committed
  migration is never modified or deleted once the repository has a release tag (before the
  first release nothing has run the migrations but disposable environments, and a pull
  request may still rewrite the sequence). A consolidation (`bedrock migration
  consolidate`: every committed file folded into one at the index where the sequence
  ended) is the one change that removes committed migrations, and the job accepts it by
  that shape alone, the fold being the concatenation it is. Then the whole directory, and the seed directory
  beside it, is read by the sequence rule the migrate command needs (`bedrock migration
  check`, run with the bedrock the placement pins, which the job fetches with `go run` from
  bedrock's own module, `github.com/impulseframework/bedrock`), so a gap already on the
  default branch fails the next pull request; before the application is registered there
  is no placement, and the step says so and passes.

The security scan. The pull request checks run only when a pull request changes, and a
vulnerability is published on its own day, so `security-scan.yml` runs every day at 14:00
UTC (and on demand, from the Actions tab) over what is deployed and what is next: `refs`
lists the refs to scan, the default branch and the latest release's tag when the
repository has a release (`gh release view`); `go-vuln` runs govulncheck at each ref as the
pull request's leg does, restoring `go-build`'s caches and saving nothing; `image` builds
the image at each ref with the inputs the Dockerfile declares, as the pull request's job
does, and runs Grype over it (hadolint runs on the pull request alone); and `report`,
which runs whatever the legs did, keeps one issue of the repository under the fixed title
`Security scan: a vulnerability check or an image scan failed`. A failed leg opens the
issue naming the failed legs (`go-vuln (the default branch)`, `image (v1.4.0)`) and the
run, or comments that on the open one; a run with every leg passed closes the open issue
with a comment naming the clean run; a canceled or skipped leg does neither. The
application stands alone: the scan calls no reusable workflow and posts nowhere but the
repository's own issues, and nothing in it gates anything.

The caches. A pull request's `go-build`, `go-test` and `go-test-skipauth` restore the Go
module cache and build cache from the nearest entry saved under their own job's key (the
same `go.sum` first, then any), and `go-lint`, `go-lint-skipauth`, `go-vuln` and
`go-check` restore `go-build`'s, whose compile they share: golangci-lint loads every
package through it, and a lint leg that started from nothing after a dependency change
ran past its five minutes on a cold runner. Neither cache can serve a stale result: a
module is verified against `go.sum` when read, and a build output is addressed by the
hash of its inputs (the toolchain, the flags, the sources, the dependencies' outputs), so
an entry from an older commit is either exactly what this commit computes or unused. The
entries come from `ci-cache.yml`: after each push to the default branch or a patch branch
it builds and tests what those jobs build and test, at the branch's head (the tests run
rather than compile, since what a test builds as it runs is in the cache only when it
ran), and saves under their keys with the commit's, so a pull request's first run starts
from the branch it targets and compiles what it changed and nothing else. A pull request
run saves an entry of its own only when it found none for its `go.sum` (a dependency
change), so the caches grow with merges, not with pushes; GitHub keeps the newest ten
gigabytes and drops an entry unused for a week. Nothing else is cached: the image build
compiles fresh by design (its download stages are a minute of the job, and exporting
them to the Actions cache cost more than it saved), govulncheck, Grype and Semgrep fetch
their databases and rules when the job runs, the emulator images are pulled, and the lint
legs keep golangci-lint's own cache beside the Go caches they restore.

Go's cached test results are the one reuse whose inputs Go cannot see in full, so the test
legs run every test (`go test -count=1`) unless the application turns the reuse on. Go
reuses a passed test's result when the test binary, the files it read and the environment
variables it read are unchanged (a failure is never cached); it cannot see what a running
emulator's image holds. An application whose tests' inputs are all in the tree, in the
environment or pinned by digest turns it on with the `//impulse:ci` line, and then the
test legs restore every tracked file's modification time from git before the run (the
checkout gives every file the time of the checkout, which would miss every result that
read a file) and `ci-cache.yml`'s test runs on the branch save their results, so a pull
request reuses the results of the packages it did not touch.

The `//impulse:ci` line is a comment line in any non-test Go file of the application, at
most one in the tree. It is a directive in Go's sense (no space after `//`), so gofmt
keeps it as written; at the end of a doc comment gofmt wants a blank `//` line before it,
as it does for `//go:generate`:

```go
// main serves the application.
//
// The CI choices: the test legs and the image build on the larger runner, the test results reused.
//
//impulse:ci large-runner=go-test,go-test-skipauth,image test-cache=on
package main
```

`large-runner` lists the jobs on the runner `CI_LARGE_RUNNER` names, by job id, any job
the workflow renders but the gates (`none` puts every job on the standard runner);
`test-cache` is `on` or `off`; `default-branch` names the repository's default branch
when it is neither `main` nor `master`, so `ci-cache.yml` runs on it (without the
setting it runs on both names, of which a repository has one). A setting the line leaves
out keeps the default (the two test legs and `image` on the larger runner; the test
results not reused; `main` and `master`), and a line with a setting it does not know, a
job the workflow does not render, or a second line in the tree fails `impulse check` and
`impulse render`. The rendered workflows say in their header what the line chose, and
`impulse check` asks origin for its default branch (`git ls-remote --symref origin
HEAD`): a default branch `ci-cache.yml` does not fill fails the check with the setting
to declare, since nothing else would ever say that the caches are never filled; without
a remote or a network the check says it could not ask.

Every action is pinned by commit with its tag in a comment, and every tool version is a
constant in impulse, so the pins travel with impulse releases: a bump is an impulse
release, never an edit to the file, and dependabot must not run over the workflow files
impulse or bedrock own. The impulse an application runs is the `tool
github.com/impulseframework/impulse/cmd/impulse` directive in `go.mod`, at the framework
pin, which `impulse check` (`pins`) holds: CI runs `go tool impulse check` at that pin,
verified by Go's checksum database, and no version is written into the workflow. When the
pin stands at the ledger's last step, moving it is three commands: `go get -tool
github.com/impulseframework/impulse/cmd/impulse@<version>`, then `go tool impulse render`
(the owned files follow the new impulse), then `go tool impulse check`; when it stands
behind a step, `impulse upgrade` moves it (section impulse upgrade). Run the check locally
before pushing; the pull request is the one gate. CodeQL and the rest of GitHub's Code
Security are optional and not rendered.

A Google method under the `directory` authority assigns roles from the directory's groups
by name. A group's email is lowercase, so the names reach the store in lowercase; the store
resolves a directory's name without regard to case and writes the membership in the role
file's spelling, so the file keeps its names as they are (the base's `Administrator`, which
the group `<prefix>administrator@` names).
Two roles differing only in case are refused when the engine opens, so a resolution is never
a guess.

## impulse check

`check` verifies the agreements between an application's parts and exits non-zero when
any of them is broken. Run it from the application root, or point it at one:

```sh
impulse check
impulse check --app path/to/app --skip-generate
impulse check --only prettier-ignore,env-template --fix
impulse check --only prettier --fix             # once the web roots' packages are installed
impulse check --list
```

The scan the checks are built on reads the Go files the application's builds compile, as
`go build ./...` and `go test ./...` find them. A directory `./...` matches nothing under
is not read: one whose name begins with `_` or `.`, and one named `testdata`. Nor is a Go
file whose own name begins with `_` or `.` (`_scratch.go` beside a package's real files),
by the scan or by a check that reads a package's directory for itself, nor any Go file of
a directory under the root that holds a `go.mod` of its own, which is another module (the
skeleton's `web/` is one).
So a scratch program kept beside the application (`tmp/_sqlrun/main.go`), a test's
fixture and a tool's own directory fail no check. Two things are the application's all
the same. A Go file placed directly in a directory the Go tool reads, `tmp/` among them,
is read, as `go build ./...` compiles it. And a package under a passed-over directory that
the application imports by its path (`internal/_cfg`, a fixture package a test imports
from `testdata`) is read, with what it imports in turn, since the build compiles it: a
read of the environment does not pass `env-reads` by living there. A file a build
constraint leaves out (a `//go:build` line, a `_windows.go` name) is read, since some
build compiles it, and a directory named `vendor` or `dist` is left alone as dependencies
and build output. The uses of a feature flag's constant are found in the same Go files,
and in every source file of a browser application whatever its directory is called, since
the Go tool's rule about names is no rule of the browser's build.

| Check | Verifies |
| --- | --- |
| `generator-program` | Every generator program uses options this release knows, with literal arguments: strings, booleans, lists and maps; a whole number or a constant expression of literals for `WithBodyLimit` (`2<<20`); and, for a request log word or a trace setting, the generation package's own constructor (`generation.LogOnEvent()`, `generation.TracesCapped(0.1)`, with `.MinSeverity(logging.Warning)` chained when wanted). A program the tool cannot read completely is one it cannot later edit or migrate. An option the framework has since removed (`WithDomainRoute`) is named with where its declaration went. A program that never reads `Warnings()` after it generates (in its own package, or in the main package that runs a declaring package) warns: the schema warnings a generation raises go unseen. The skeletons' runner prints them and pins the accepted set in a test. |
| `options` | The generator programs declare one coherent option set, and the report states it: layout (flat or sites), sites, tenancy (the resource package's `@tenant` record, `WithConcealedDomains`), outlets, targets, and the request log words and trace settings a site declares (`WithRequestLog`, `OutletRequestLog`, `OutletTraces`, `WithMountedRoutes`). Handlers come with routes, `ForOutlet` names a declared session-serving outlet, the referenced directories exist, a `//go:generate` directive runs every program (its own directory, or a main package of the module that imports the package declaring it, the layout an application takes when its tests run the declaration in-process), the sites agree on tenancy, a second site lives under `apps/<site>/`, and each `GenerateIdentity` tree is one program's (a program removes from its tree the packages it does not write). |
| `tenancy-wired` | A site whose resource package declares a tenant record (`@tenant`) has at least one struct annotated `@permissionScope(domain)` and a tenant roster built outside tests with the record's generated constructor (`New<Record>Roster`), handed the live service's tenants signal (`resource.WithTenantSignals`) and started (`Start`) in the package that binds it; a site declaring no record has no tenant-scoped structs. The generator refuses the rest (a tenant-scoped resource with no record, a record that is not a global, table-backed resource with one string key, a second record), the compiler holds the App to the generated contract's `TenantRoster()`, and the permission engine reaches every tenant with the roles held in every domain, so roles need no provisioning per tenant. |
| `tenant-delete-rule` | Every table that carries a tenant's key on the row itself (a struct annotated `@resource` and `@permissionScope(domain)` whose `// @domain` field is its own column, `TenantId` in the skeletons) references the tenant table on that column with `ON DELETE CASCADE`, or is interleaved in the tenant table with it, so the one request that deletes a tenant takes the table's rows in the same transaction. The check reads the up migrations of the site's `file://` sources in order, statement by statement (`CREATE TABLE`, `ALTER TABLE ... ADD` and `DROP CONSTRAINT`, `DROP TABLE`), and fails on a table whose foreign key to the tenant table carries no delete rule (the delete of a tenant that still holds one of its rows would be refused) and on one that keeps the key with no foreign key at all (the rows of a deleted tenant would stay behind). A table that reaches its tenant through another row (`@domain(via: ...)`) is held to the rule on every link of that way: the check follows the join path from the table's own column to the row it names, then through the column of each field the path names, to the tenant table, and each link must be a foreign key that holds the column (alone or beside others) with `ON DELETE CASCADE`, or, where the table is interleaved in the row's table, the interleave with it: a table interleaved in the row it names goes by the interleave's rule, whatever a foreign key beside it says. Where several foreign keys hold a column, the way is through the table whose struct holds the path's field. It fails naming the first link that carries no rule, once for each table whose way passes that link, and a table whose last column declares no foreign key to the tenant table fails as a table that carries the key would. Every rule on a way is the database's, so it fires for a row of the way deleted by itself too: the rows behind it go with it, past their own resources' Delete grants and row conditions and with no change event. A reference that is on no table's way to its tenant is not read: the application rules it either way. The check does not judge the directories record, whose row the generated delete of the tenant removes with its change event. A table no migration it reads creates, a migration source that is not a `file://` path, a column of a way that no foreign key holds in a table interleaved in no parent, a join path on a field with no spanner tag, and a field of a join path that no struct of the resource package holds for the table the way reaches are a warning that names what was not read, never a pass. |
| `outlet-wired` | Every outlet a program declares (the default from `GenerateRoutes` and each `WithRouterOutlet`) has its generated routes mounted: by the generated router (`GenerateRouter`) from the declaration itself, or, in an application that kept a hand-written router, by a hand-written file in the router package calling `generated<Outlet>Routes`. A session-serving outlet has a `GenerateTypescript` target naming it, and that browser project's development proxy forwards the outlet's prefix. An outlet with no `@outlet` members yet is noted, not failed. Each session outlet's line says `live` when the site's App wires the live service (its `LiveService` returns more than nil), since the generated router mounts the live routes on every session outlet; an App whose `LiveService` returns nil fails: every session outlet serves the live routes, and the live service is required in every application. |
| `outlet-shape` | Every session outlet serving a browser application (`WebApp`) has its API prefix under the application's mount path: `<mount>/api`, `api` for an application at `/`. That is the one shape the skeletons carry, so each application's scope covers its own API, login, and callback routes; an outlet shaped otherwise works and the generator accepts it, so the check warns, naming the outlet, its prefix, and its mount path. The mount rule itself is the generator's: a browser application at `/` beside another is refused at generation, since an installed application's scope is every URL under its start. |
| `sites-wired` | In the sites layout, every site has a main package under `apps/<site>/`, a process in the Procfile or process-compose file running it on its own `PORT`, and every site's router is imported by some package passing a collection to `access.WithDefaultRoles` (the data level, or the callers of an auth package that makes the call itself), so the collection the permission engine validates the roles against (the union, or one per auth) knows the site's resources. |
| `web-wired` | Every browser application bound to a session outlet is bound to the outlet's auth: every `Resources.<Name>` its hand-written TypeScript takes from the generated constants is one its generated client exports; every request it makes for the pages of an auth's generated management (`identityRoutes` or `identityPages` naming `auth: '<Auth>'` and a `scope`) names an auth and a side the outlet's generated API description carries (`zz_gen_api.ts`, its `identity` entries, which a program without `GenerateIdentity` leaves empty); and the cookie its `withXsrfConfiguration` names is the XSRF cookie the outlet's auth issues (`<auth>-xsrf`, as `pkg/auth/<auth>` declares it; every auth's under a hand-written router). A site's browser application starts as a copy of the console's, bound to the console's auth; a stale resource name fails the browser's own build (TS2339), which no Go build sees, a request for pages the description does not carry is refused by the library when the application loads, which no build sees at all, and a stale cookie has the server refuse every mutating request. `impulse add auth --outlet <site>` rewrites the routes and the cookie in the sites layout, and each line under the check names the file, what it carries, and what to write. |
| `login-pages` | Every auth bound to a browser outlet (an outlet declaring `Auth(...)` and `WebApp("<mount>")`) returns a refused or waiting sign-in to that outlet's login page: the `LoginURL` its settings construction on the data level names (`<auth>.Settings{...}`, outside tests) is the mount path joined with `/login` (`/login` at the root, `/console/login`, `/portal/login`), the session library's default `/login` standing for a construction naming none when the outlet declares a directory method (the password method answers its own post and sends nobody anywhere, so a password-only auth's construction may name none), and where the construction names `MailLoginURL` (`env.PublicURL + "<path>"`) the quoted path is the same page, so every mailed link opens it. The library sends a refused directory sign-in (`?code=`) and a sign-in waiting for its confirmation (`?pending=`) to `LoginURL`; a console moved from `/` to `/console/` that kept `/login` sends both to a page nothing serves, which no build or generation sees. When a machine outlet carries the auth's tokens (`Tokens("<module>/pkg/auth/<name>")`), the construction's `DevicePage` is the mount path joined with `/device` (`/console/device`), the page a device authorization sends a command-line client's person to for approval, the library's default `/device` standing for a construction naming none; a construction naming a `DevicePage` is held to the page whether or not a machine outlet carries the auth's tokens. A mismatch fails naming the file, the auth, what it holds and the outlet's page, with the value to set; a value that is not a string literal fails too, since the check cannot read it. An auth bound to no browser outlet is passed over with a reason, an auth whose settings no file outside tests builds warns, and an application whose outlets serve no browser application is skipped. |
| `login-held` | Every login page rendering the library's login component (`<impulse-login-methods>`) holds its card back while the component's arrival reads are in flight: the page gives the component a template reference (`<impulse-login-methods #methods ...>`), reads its arriving signal (`methods.arriving()`, the component's word that the methods the server lists and the session are still being read), and, where the read binds a class on the card (`[class.arriving]="methods.arriving()"` on the `<mat-card class="login-card">`), a stylesheet of the page (the ones the component names in `styleUrl`/`styleUrls`, its inline `styles`, or the template's sibling) hides it (`.login-card.arriving { visibility: hidden }`); a page reading the signal another way (`@if`, `[hidden]`) holds its card as well. The component renders nothing while it is arriving, so a page that does not hold its card shows an empty card and then fills it, where a page shows nothing until it knows what it renders and then renders whole, the way a page loads, with no progress bar on every load. The skeletons' login pages carry the shape; an application's own login page takes it by hand, which no build or generation sees. A browser application rendering the component nowhere is a warning, since the check cannot read a login page of the application's own making. |
| `maintenance-switch` | Every site's main checks `maintenance.Requested()` before it builds the site configuration (`config.NewSiteConfiguration`), and serves `maintenance.Serve(ctx, brands)` when it is set, with the brands of the browser applications it serves by mount path (`pkg/brand`, which `impulse brand` writes), so the page says whose it is: the deploy pipeline starts a maintenance revision of the application's own image with `APP_MAINTENANCE` set before a release that replaces or interrupts the database, and that revision must open no database, session store or secret. A main that builds the configuration first, or checks the switch after it, fails; a main that builds no site configuration is a warning, since the check cannot tell where the switch belongs. |
| `session-tables` | Every session Auth constructed outside tests (`session.NewAuth`) reads tables a migration creates: its sessions, users, identities, pending sign-ins and sign-in attempts tables, the mail links, sign-in codes and trusted devices tables when it sends mail (`session.WithMail`), the tokens and device codes tables when a machine outlet names it as its issuer, and the auth events, impersonation and custom data tables when the storage attaches them. Two Auths never share a table. Every Auth registers at least one sign-in method (`session.PasswordSignIn`, `azure.SignIn`, `google.SignIn`, `oidc.SignIn` for the persona method), each under a registration name the scan can read, once; a method registered on an identifier that names no construction the scan sees is a finding. The report lists the auths, each named by its package when it lives in one (`pkg/auth/<name>`), with its methods (the registration name, the method behind it when the two differ, and a directory method's role-membership authority: directory for `RoleSync`, application for `DisableRoleSync`), its tables, and its session and XSRF cookies when named. |
| `migrations-reader` | The schema sequence creates the `migrations_reader` database role and grants it select on the `SchemaMigrations` table (`CREATE ROLE migrations_reader`, `GRANT SELECT ON TABLE SchemaMigrations TO ROLE migrations_reader`, read statement by statement over the up files), which the pipeline's migration guard reads each environment's migration version through on a consolidation; the environment layer grants the test environment's deploy identity the role on the other environments' databases. The skeleton's first migration carries the statements; a sequence without them fails naming the migration to add at its next free index, which `impulse upgrade` writes. |
| `auths-wired` | Every auth package (`pkg/auth/<name>`, constructing a session Auth) is constructed by the data level (`<name>.New` called outside tests), carries its role file (`pkg/auth/<name>/roles.json`, embedded by a `//go:embed roles.json` directive, exported as `<name>.Roles()`, and handed to the permission engine by an `access.WithDefaultRoles` call outside tests, in the package or on the data level; the file exists), and bound by a surface: an outlet declaring `Auth("<module>/pkg/auth/<name>", <methods>)` naming the methods the package registers (the generated router mounts the login routes of the methods declared, so a method declared but not registered, registered but not declared, or declared as another kind is a finding; the password method's `.UsernameIsEmail()` is said on the declaration and the registration both or neither, and a directory method's `.SyncsRoles()` is chained exactly where the registration's `RoleSync` writes the roles, since the generated management leaves a role to the directory on the declaration's word alone; a registration that writes the roles stands alone on its auth, so a second method beside it is a finding, and the session library refuses it at construction), or a package outside `config` and `cmd/` taking `*<name>.Auth`. An outlet bound to a package that is no auth package is a finding. No two auth packages issue the same cookie, session, XSRF or trusted-device, a name left unset being the session library's default (`auth`, `XSRF-TOKEN`, `auth-trusted`); the browser keeps one cookie of a name per host, so a login to one auth would overwrite the other's, and a browser one auth remembers after a sign-in code would forget the other's remembered browsers. A directory method that hands role membership to its directory (`RoleSync`) has no role writer in the application reaching its auth's store, since the directory removes those roles at the next sign-in. A role file handed to the engine that no test validates warns: a `_test.go` calling `access.ValidateRoles` while parsing the auth's `Roles()` is where the warnings the deploy prints are pinned as typed values, so without it a warning is accepted nowhere in code. Auths outside auth packages warn. |
| `roles-file` | Every auth package's role file (`pkg/auth/<name>/roles.json`) parses the way the permission engine reads it when the release starts (`access.RoleFile.Parse`): every role is named in the one grammar, letters and digits starting with a capital letter (`Administrator`, `TenantAdministrator`), with the scope never part of the name, and none takes the name of the role the framework defines in every auth (`SuperAdministrator`, in any case); every role carries a `description`, one or two plain sentences saying what the role is for; and every grant names a resource, with a condition that parses. Where the application's generators have written a permission collection (`zz_gen_collection.go` in each routes directory), every grant is also held to what the application serves: a resource the collection names, at the scope of the role that grants it (a global role grants on global resources, a domain role on a tenant's), for a permission the resource takes, and on fields the resource has for that permission. So a grant left on a list the application stopped serving is a finding, with the role, the resource and what to do, where the release would otherwise refuse the file as it starts. And no grant names an action no role is given: an entry the collection writes with `NoRole: true`, as the generator marks the action of the create-your-tenant route. Nothing asks for a grant of such an action, so the grant would do nothing, and the action is registered only so that a permission mask can refuse it to a session that acts as someone; the finding names the file, the role and the action, and says to remove the grant, and it is the one finding such a grant gets, as the engine refuses it for that before it looks at the action's scope. A file the engine would refuse stops the release at start, so the refusal is a finding here, naming the file, the role and the rule. A grant's condition against the collection's vocabulary stays with the engine and with the validation test `auths-wired` asks for. The collection is parsed as Go source and not compiled. An application with no auth package is skipped, and one whose generators have written no collection is read by its files alone. |
| `super-administrators` | Every auth package with a role file tells its Auth who the auth's own super administrators are. An auth with a role file has the Super Administrator role, which the framework defines in every auth, and the session library holds its holders to a second factor whatever the sign-in rule asks of anyone else, except at a sign-in through a directory that writes the roles, where the directory decides who holds the role; it learns who they are from the argument `session.NewAuth` takes after the cookie key. The compiler holds an application to passing the argument, and this check to passing the right one: `session.SuperAdministratorsBy(signin.SuperAdministrators(resource.SignInRoles(accessClient.UserManager())))`, over the permission engine the package's `New` opens with its own `Roles()` (`access.New` with `access.WithDefaultRoles(collection, Roles(), Name)`), as `add auth` writes it. Three things fail: `session.NoSuperAdministrators()`, which compiles and leaves a holder to the sign-in rule alone; a check over an engine the constructor opens with another auth package's role file (`<other>.Roles()`), which is that auth's engine; and a check taken from another auth's package. Either holds another auth's holders, or nobody, to the factor. The check reads what the constructor shows, following the check through the identifiers the function assigns. An argument that reads some other way, a check written by hand as a function, an engine opened outside the constructor (handed in by the data level, say), or an engine whose role file does not read there or is no auth package's cannot be followed, so whose engine it is goes unjudged and the result is a warning naming the construction. An auth package without its role file is `roles-file`'s and `auths-wired`'s finding, and an application with no auth package is skipped. |
| `persona-cast` | Every Auth registering the persona method (`oidc.SignIn`) has its cast beside it (`pkg/auth/<name>/cast.json`): the file parses the way the migrate command seeds it and the deploy registers it with the persona provider (at least one persona; every login lowercase letters, digits, dots, underscores and hyphens starting with a letter; no login twice), and every role a persona names is defined in the auth's role file, since `SeedCast` seats each persona in its roles and a role the file does not define grants nothing, or is the framework's `SuperAdministrator`, which no role file defines and `SeedCast` seats in the global partition. The empty cast `add method persona` writes is a finding until it is filled in. An application whose Auths register no persona method is skipped. |
| `directory-admission` | An application whose tenant record declares directories (a struct annotated `@tenantDirectory` in a site's resource package, the skeletons' `TenantDirectory`) is wired to seat a stranger the directory vouches for: the directories record declares a column annotated `@directoryDefaultSeat` (the skeletons' nullable `DefaultSeat`, the tenant's role the stranger is seated with at their first sign-in, NULL where the tenant admits them by invitation only); every auth whose roles the application assigns (one registering no method with `RoleSync`, whose directory would write them) calls `SetDirectoryAdmission` on its session Auth (`authSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())`, over the generated directory reader its Settings carry and the hook of the identity client its `New` builds, which gives the role in the transaction that links the person's account to the directory), so a stranger from a tenant's directory is created seated with the row's role or refused with no account (`invitation_only`), and a person whose account stood before is given the role at their first sign-in through the directory; and a role in a role file handed to the permission engine grants `Execute` on the two generated methods that say how a tenant admits its directory's people (`Open<Record>ToDirectory`, which names the role behind its warning, and `Make<Record>InviteOnly`, which clears it; the skeletons' `OpenTenantToDirectory` and `MakeTenantInviteOnly`), so the tenant's administrator makes the choice on the Sign-in page; the record's own update never writes the default seat. An application whose tenant record declares no directories is skipped. When the check fails under `impulse handoff`, the brief carries the pattern. |
| `first-administrator` | An auth that brings in a first administrator invites one, an auth whose roles the application gives can invite one, and a cast of personas is seeded before the invitation. The first person into a deployed application comes in as everyone after them does: while nobody of the auth holds the Super Administrator role, the migrate command has an invitation mailed to the address the placement names for the environment in `APP_<AUTH>_ADMINISTRATOR` (`InviteAdministrator` in `pkg/auth/<name>/foothold.go`, over `session.AuthAPI.InviteFirstAdministrator`), giving the Super Administrator role and the roles `FirstAdministratorRoles` names, and the person opens the link and signs in by a method of the auth: with a password they choose there, or through a directory, where the invitation is what ties the sign-in to the roles, however many organizations the method admits. No account is created ahead of the person and no password is generated: nobody can read a generated password, and an account that has never signed in is tied to whoever signs in first by its name alone. The session library holds the rest, which is why the check holds the package to asking it: nobody is invited once somebody holds the Super Administrator role, an account that holds no such role stops no invitation, an invitation that stands is left as it is, one that ran out is replaced, one for an address or for roles the placement no longer names is withdrawn, `ReinviteAdministrator` (the migrate command's `-reinvite-administrator <auth>`, over `session.AuthAPI.ReinviteFirstAdministrator`) withdraws the standing one and mails a fresh one while nobody holds the role, and both refuse while nobody holds it and the application has no mailbox to send as, since an invitation's link is never written to a log. The check reads the functions a first administrator is brought in by (`InviteAdministrator`, `ReinviteAdministrator`, and `SeedAdministrator` with `seedFoothold`, which an earlier `impulse add auth` wrote the seed in) and fails for a package that still seeds an account (`SeedAdministrator`: replace it with the two invitations, read the address in the data level in place of the password, whose `APP_<AUTH>_ADMINISTRATOR_PASSWORD` declaration and `secret:"generated"` tag go, and call the two from `pkg/deploy` and the migrate command), for one that creates a user where it invites (`CreateSessionUser`, a `session.CreateUserRequest`), and for one whose `InviteAdministrator` or `ReinviteAdministrator` does not ask the session library. A user the package creates elsewhere, for some other account, is not taken for the first administrator's, and its tests and generated files are not read. It fails, too, for an auth whose roles the application gives (a role file, and no method registered with `RoleSync`) whose package imports no generated identity client (`<name>identity`, the package `generation.GenerateIdentity` writes for the auth): the client's acceptance hook gives an accepted invitation its roles, so nobody can be invited as that auth's first administrator, and a seed by hand is refused above, which leaves a deployed auth with no way in; the finding says to generate the management, build the client in the auth's `New` and invite from `foothold.go`. An auth that has the client and invites is held to setting the hook in its package (`SetInvitationAccepted`), which the session library refuses to invite without. And it fails for a function that seeds a cast of personas (`SeedCast`, or `SeedCastFile`, the same seed over a cast file handed to it) after it invites, for every auth (`InviteAdministrators`) or for the cast's own auth (`InviteAdministrator` on the same receiver): who holds the Super Administrator role decides the invitation, and a persona may hold it, so an auth's cast is seeded before its invitation; the order is read inside one function, where the migrate command runs its steps, so a cast seeded in a function of its own or when the application starts is not judged. Skipped where no auth brings in a first administrator and nothing is found: an auth whose directory writes the roles, where the directory decides who its administrators are. |
| `tenant-accounts` | In an application whose program generates the management of its auths' people (`GenerateIdentity`) beside a tenant record, every grant a tenant's role (a domain role of the auth's role file) carries on a generated method that changes a person's account carries a condition that reads the person's row. The methods are the tenant's side of the account methods, named as the generator names them (`Disable<Auth><Tenant>User`, `Enable…`, `Delete…`, `Rename…`, `Set<Auth><Tenant>UserName`, `Set<Auth><Tenant>UserEmail`, `Set<Auth><Tenant>TemporaryPassword`, `Require<Auth><Tenant>PasswordChange`, `End<Auth><Tenant>UserSessions`, `Revoke<Auth><Tenant>UserTrustedDevices`, `Unlink<Auth><Tenant>SessionIdentity`, `Remove<Auth><Tenant>Password`). An account is one for the whole application, so what a tenant may do to a person another tenant or the application's own staff also holds is a rule, and the generated management leaves it to the role file: each of these methods names a row of the tenant's list of people, whose facts a condition reads (`heldElsewhere`, `heldGlobally`). The skeletons grant them under `heldElsewhere = false AND heldGlobally = false`; another rule over the row is the application's to choose. A grant with no condition, or with one that reads the request alone, states no rule about the person and is a finding. The methods that give or take a role in the tenant and that invite to it are the tenant's own and are not held to this. An application with no tenant record, or whose programs generate no management, is skipped. |
| `second-factor` | Every auth package's `New` ends its sign-in rule with the second factor: its one `SetSignInPolicy` call on the Auth it constructs (the session library panics at a second) is given `requireSecondFactor`, a function of the package returning `signin.RequireMFA`, alone or chained last with `.Then` after the tenant policy. Every session carries a second factor, and the rule is the whole of it: the sign-in codes and trusted devices tables (`session-tables`) and the pending sign-in's code handlers (the router compiles against them) ask nobody for the code by themselves, so an auth that sets no rule, or a rule that ends elsewhere, admits a session on the password alone. `--fix` writes the rule as `impulse upgrade`'s `second-factor` recipe writes it: `.Then(requireSecondFactor)` chained on the call's policy, or the call set after the construction's error check when the package makes none, and the function at the end of the file with its imports when the package has none returning `signin.RequireMFA`; it then names what the rule leaves to the author, which no check sees: the application's tests that sign in with a password now meet the code step, and complete it as the skeleton's integration tests do. |
| `change-signal` | Every permission engine constructed outside tests (`access.New`) is handed a change signal: an `access.WithChangeSignal` option among the call's arguments, the skeletons' being `access.ChangeSignalFunc(announcePolicy(signals), watchPolicy(signals))` over the live service's `policy` kind. Without it a role, grant or membership written on one instance reaches the others at the engine's next heartbeat alone. A package constructing an engine without the option fails on its own line, naming each construction by file and line; a call forwarding its options (`opts...`) fails too, since the signal cannot be read there. Skipped when the application constructs no engine. |
| `conditions-proven` | Every conditional grant in a role file the release hands to the permission engine (`pkg/auth/<auth>/roles.json`, the `<auth>.Roles()` an `access.WithDefaultRoles` call takes) is named by a test case calling the harness helper `provesGrant(t, <auth>.Roles(), role, permission, resource, condition)` with literal coordinates. The generated authorization matrix runs a fake engine with unconditional grants and the deploy-time validation reads the grammar, so whether a condition does what its author meant on real rows is proven only by a case over seeded rows: a row the condition admits answers, a row it refuses is refused. The check reads the call; the helper, in `test/integration/harness_test.go`, parses the role file when the test runs and fails when the grant is gone or its condition reads differently, so the case and the file hold each other from both sides. A grant no case names fails the check on its own line; a call naming a grant the file does not carry, or one the check cannot read, is a finding too; a file with no conditional grant reads "0 conditional grants". Unconditional grants need no case: the generic test `test/integration/grants_test.go` parses the auths' role files and proves the engine serves every one of them. A case may still pin an unconditional grant its suite depends on, by a `provesGrant` call with an empty condition: the helper fails when the file puts a condition on that grant or drops it, and the check takes the call while the file carries the grant with no condition, and reports it once the file does not. When the check fails under `impulse handoff`, the brief carries the pattern a case follows. |
| `skipauth` | When an auth registers a directory method (azure, google, oidc), the simulated directory stays in development and tests: no application code reads `APP_USERNAME`, `APP_ROLES` or a registration's `APP_<REGISTRATION>_USERNAME` and `_ROLES` (only the session library's `skipAuth` build does), and no build description (Dockerfile, cloudbuild, Makefile) carries the tag, which would let a deployed build accept any name as a login. |
| `emulator-version` | The generator option, the process files' image tags, and the test harnesses name one Spanner emulator version; the process files and test harnesses that start the Firestore emulator (the Cloud SDK emulators image, `google-cloud-cli:<version>-emulators`, or the `db` package's `NewFirestoreContainer`, which takes the same version) name one version of it. The summary states both; an application whose process files and test harnesses name no Firestore emulator fails, since development cannot start without it: the data level refuses to start with neither `APP_FIRESTORE_DATABASE` nor `FIRESTORE_EMULATOR_HOST` set. |
| `prettier-ignore` | Each browser app's `.prettierignore` excludes the generated TypeScript. Prettier reflowing generated files breaks generate idempotence. `--fix` adds the entry. |
| `eslint-ignore` | Each browser app receiving generated TypeScript ignores it in its eslint flat config (`ignores: ['**/zz_gen_*.ts']`) or `.eslintignore`. Generated shapes trip stylistic rules, and the output is not the developer's to change. |
| `resource-styles` | Each browser app that depends on `@impulseframework/resource-angular` imports the library's stylesheet (`@use '@impulseframework/resource-angular/styles';`) from a global stylesheet its `angular.json` build names. The form and list components request its classes by name; without it fields lose their grid and section labels pile up in a corner. `--fix` prepends the import. |
| `package-manager` | Every browser app carries the same kind of lockfile (bun, npm, yarn, or pnpm), and the process files and package scripts invoke that tool and no other. Two tools in one repository means two lockfiles drifting apart. A browser app with no lockfile fails: the workflows install from the lockfile exactly (`bun ci`), so an application without one cannot pass its own pipeline, and no rendering carries one (the versions it would lock are the release's own). The failure names the install that writes it (`bun install` in each web root, once) and the file the repository keeps; it gives no order to commit, since a handoff brief quotes it to an agent that is told not to. An application built against a framework checkout (`--dev-root`, `go.mod` replacing the framework module with a directory) is skipped instead, as its browser packages are linked from the checkout. |
| `registry-pins` | Every browser app installs its packages from the registry: a committed `file:.yalc/<package>` spec (or a lockfile recording one) is a local yalc attachment that a clean checkout cannot install, so the pipeline's install fails. `weblib.sh restore` puts the registry pins back. |
| `web-pins` | Every web root (a directory holding `angular.json` whose `package.json` names the browser packages) pins `@impulseframework/resource`, `@impulseframework/resource-angular` and `@impulseframework/resource-firestore` at the version of the ledger step the application stands at, which is the step's release without its `v`, read from the framework pin in `go.mod` as `impulse upgrade` reads it, in every section that names them (dependencies, overrides). The server libraries and the browser packages of one release were written for each other, so a pin behind the step's fails, naming the file, the section, the package, what it holds and what the step pins; `impulse upgrade` moves it. A pin ahead of the step's warns and stays, as a Go pin bumped by hand ahead of its step does, with one exception: a pin at the application's own release, the one `go.mod` requires the framework at, is where `impulse new` wrote it, so a new application made by a release later than the last step passes, and the answer says its pins stand at its own release. A package the file does not name fails. A web root naming none of the library's packages is passed over, and an application whose pin reaches no step is skipped. |
| `test-runner` | Every browser application project runs its component specs on Angular's unit-test builder, the runner `ng new` scaffolds (`@angular/build:unit-test`: Vitest under jsdom in Node, no browser): a `test` target on that builder, the spec tsconfig it reads (named in the target, or `tsconfig.spec.json` in the project root), and a package script running `ng test <project>`, so `bun run test` runs every project's specs once. A project with no `*.spec.ts` under its source root warns: the runner is wired and nothing runs on it yet. |
| `installable` | Every browser application bound to a session outlet installs as a progressive web app, and the server serves it through the resource package's served browser app. On the browser side: `@angular/service-worker` is a dependency at the workspace's Angular line (the line of `@angular/core`); the project's production configuration names a worker config (`"serviceWorker": "ngsw-config.json"`) that exists, whose `navigationUrls` exclude the outlet's API under the mount (`!/api/**`, relative to the mount, so the login, callback and stored-file navigations reach the server; an API prefix outside the mount is outside the worker's scope and needs none); the app config provides the worker (`provideServiceWorker`) and the library's update provider (`provideAppUpdate`); `index.html` links the web app manifest, which parses with `id` the mount path with a trailing slash and `scope` and `start_url` `./`; and every icon the manifest declares is there with PNG dimensions matching its `sizes`, read from the file's header. The release reaches the browser: the project's build defines `APP_VERSION` (`"define": { "APP_VERSION": "'dev'" }` in its build options) and the workspace's `build` script redefines it from the `VERSION` environment variable (`ng build console --define \"APP_VERSION='${VERSION:-dev}'\"`, which the image's browser stage sets), so a release build stamps its release and any other `dev`; the app config provides it as `API_VERSION` (`{ provide: API_VERSION, useValue: APP_VERSION }`) and registers `apiVersionInterceptor` through `provideHttpClient(withInterceptors([...]))`, so every request carries the release in `X-Api-Version` and the server can refuse a build it no longer answers. On the server side: the application's hand-written handlers build the asset handlers from `resource.NewBrowserApp(dir, "<mount>")`, `github.com/jtwatson/spaassets` is imported nowhere and gone from `go.mod`. A project with none of the browser side warns as not installable; a project with part of it fails, naming the first missing piece by file. |
| `brand` | A reminder, never a failure: every browser application carries an icon of its own. A new application starts with the skeleton's mark, in its favicon, its three app icons and the logo its brand declaration names (`brand.json`), and the check warns while any of them is still there, naming the browser application and the files, with what to do: put the application's own logo where the declaration names it, or declare the brand where there is no declaration, and run `impulse brand` (section impulse brand). It compares each file with the skeleton's own, so a mark redrawn by hand passes. |
| `ci-workflow` | The committed `.github/workflows/ci.yml`, `.github/workflows/ci-cache.yml` and `.github/workflows/security-scan.yml` equal what impulse renders from the code: one browser job per workspace with the `web` gate over them, the jobs the `//impulse:ci` line puts on the larger runner and whether the test results are reused, the action and tool pins this impulse carries, and the daily security scan. A missing `ci.yml` fails: the pull requests run no checks at all; a missing `ci-cache.yml` fails: the checks start cold on every pull request; a missing `security-scan.yml` fails: nothing rescans the default branch and the latest release between pull requests. Once the files match, the check asks origin for its default branch and fails when `ci-cache.yml` does not fill it (declare it with `default-branch=<name>` on the `//impulse:ci` line); without a remote or a network it says it could not ask. A differing file fails naming the first differing line, what the code renders and what the file has; the fix is `impulse render`, since the file is impulse's: change the code or impulse, not the file. A browser workspace without its job is a difference like any other. An application that is a directory of a larger repository is skipped: GitHub runs the workflows at a repository's root alone, so a workflow in the application's own directory would never run. |
| `paging` | No application code positions a list by offset: the generated query builders have no `Offset`, the server refuses the `offset` parameter, and pages are positioned by the cursor the `Link` header carries. Go code calling `.Offset(` or `SetOffset(` and browser code sending an `offset` query parameter are reported, so a hand-written caller is found before the upgrade breaks it; tests and specs are not read, since a spec describes the server's answer (whose page state carries an `offset` field) as often as a request. |
| `rpc-execute` | Every `@rpc` struct declares `Execute` in one of the three forms the generator classifies by signature (`resource.ReadWriteTransaction` second for the transaction form, `resource.Client` for the client form, `resource.ReadWriteTransaction` second and `resource.Files` third for the upload form, or `resource.FilesIn[S]` where the method names its store; `error` the only or last result), and every generated RPC handler calls it. A handler an older generator could not type-check decodes and returns without running the method. A `TxnRunner` or `DBRunner` interface left in the RPC package warns: the generator reads the signature and no longer consults it, so delete it. |
| `feature-flags` | The `FeatureFlags` and `FeatureFlagChanges` tables the generated feature flag routes and the deploy's `MigrateFeatures` read are created by a migration as the `resource` package of the framework module the application pins declares them: the check reads `resource.FeatureFlagsDDL(resource.SpannerDBType)` from that module's source (found through `go list` in the module cache, so the comparison is against the framework the application builds with, a replace included) and compares each statement with the migration's, whitespace aside; a table that differs is brought to the library's statement by a new migration. Every declared flag (`resource.Feature` constant) gates something (`@feature(<Constant>)` on a resource, a field or a method) or is read somewhere outside tests (`a.FeatureSet().Enabled(resources.<Constant>)` in Go, `Feature.<Constant>` in a browser application), or it is a switch wired to nothing and fails by name and position; a flag declared twice and an annotation naming no declared constant fail too. Skipped when the generator emits no feature flags (no `zz_gen_features.go` in a resources package) and none is declared. |
| `sites-generators` | In the sites layout, every generator reads the one schema and the shared generator's TypeScript reaches every site's browser app. |
| `env-template` | Every `env` struct tag without a default appears in the development environment template (`.envrc.template`, `.env.template`, or `.env.example`), the variables a framework settings struct the configuration embeds declares (`gcp.Settings`) among them; a missing one of those names the struct. A comment names a variable well enough, since one a single process is given, or another program reads, is written so. A versioned secret (`secret:"versioned"`, the application secret that seals stored secret fields) is handed over one variable for each version and none under its declared name, so the template sets its first version (`APP_SECRET_KEY_1` for `APP_SECRET_KEY`); the declared name alone, a comment, and an empty value (which the application refuses at start) do not cover it. The template writes no key, since a key in a tracked file is every checkout's: a shell template (`.envrc.template`) makes one for the checkout, 32 random bytes written to `.app-secret-key.dev` the first time it is sourced and read from there after, and `.gitignore` ignores that file; a list of assignments (`.env.template`, `.env.example`) can make none and says nothing of the variable. `--fix` adds the missing lines: an empty assignment for a variable to set, the two lines that make the key for a versioned secret, and the key's file in `.gitignore`. A key written in the template, and an empty first version, are refused with the lines to write in their place. |
| `env-reads` | No Go file outside the config package reads the environment: `os.Getenv`, `os.LookupEnv`, `os.Environ`, `os.ExpandEnv`, `syscall.Getenv`, and go-envconfig's `Process`, `ProcessWith` and `MustProcess`, each resolved by the package's import path so an alias does not escape it. The config package is the package holding the application's `env` tags, and bedrock derives the stack's variables from those tags, so a read anywhere else names a variable the stack never learns of and no deployment sets. Two places are exempt: the main package of a shipped program (`ShipsProgram` on a machine outlet), which runs on the machine it is installed on and reads its own tagged settings there, which bedrock leaves out of the application's configuration, and `_test.go` files. Each failure names the file and line, the call, and the two places a read may live; an application whose `env` tags are in more than one package outside the programs fails too, since the config package cannot be told. |
| `file-store` | Every file store variable the code declares (`APP_FILE_STORE`, `APP_FILE_STORE_<NAME>`) names, in the development environment template, a store the framework opens (`file://<dir>`, `gs://<bucket>` or `mem://`); a directory store's directory is in `.gitignore` (`--fix` adds it); and a resource or method recording files (`@file`, `@upload`) has a store wired, the failure naming `impulse add files`. |
| `secret-holders` | A retirement of a version of the application secret reads every resources package. The generator writes each resources package's list of the resources that store a secret (`SecretHolders()` in `zz_gen_secret_holders.go`, nil for a package that stores none), and the call that retires a version (`resource.RetireSecretVersion`, which the migrate command's `-retire-secret` runs) names the packages by hand: a package it does not name is never read, and the retirement still reports that no stored value names the version. The check fails naming each package a call is not handed, a call whose holders come from where it cannot read them (a parameter, a function of the application's own, a list written by hand; a package's `SecretHolders()`, several joined by `slices.Concat` or `append`, and a variable of the calling function assigned from those are read), and an application whose packages no code retires. Skipped when no package carries the generated file. |
| `pins` | `go.mod` requires the framework module, `github.com/impulseframework/impulse`, at a released version: the one framework requirement, whose version is the framework pin. A pseudo-version, or a `replace` of the module (a local path or another version), warns but does not fail, since the application then builds against unreleased framework code, legitimate while a change is in flight. `go.mod` carries the `tool github.com/impulseframework/impulse/cmd/impulse` directive, or the check fails with the commands that add the requirement and the directive together (`go get -tool github.com/impulseframework/impulse/cmd/impulse@<version>`, then `go tool impulse render`, then `go tool impulse check`). When the running impulse was built from a module version (`go tool impulse`, `go install github.com/impulseframework/impulse/cmd/impulse@<version>`) and that version is not the pin, the check fails: with the same three commands to move the pin when the pin stands at the ledger's last step, and with `impulse upgrade` (`go run github.com/impulseframework/impulse/cmd/impulse@<version> upgrade`) when it stands behind a step, since `go get -tool` ahead of the walk would skip the steps' recipes; an impulse built from a checkout is a development build, noted and not compared. |
| `framework-modules` | The application names no module under the framework's import path but the framework's own. The go command opens an internal directory to every package whose import path is under the directory's parent, by the path and not by where the code is kept, so a module whose path is under `github.com/impulseframework/impulse` is inside the framework to it: a directory of the application's own given such a path (by a `replace`, or a workspace) may import the packages the framework keeps to itself, the write of an impersonated session among them (`session/internal/sealed`), and go around the declaration, the grant and the permission mask the session API holds that write to. The framework is one module; its repository holds its test application's two beside it. The check fails naming any other module path under the framework's: the application's own module path, a requirement (direct or indirect) and the replaced side of a `replace` in `go.mod`; the module of a used directory and the replaced side of a `replace` in a `go.work` at the application's root; and a requirement and the replaced side of a `replace` in the `go.mod` of each directory that `go.work` uses, inside the application's root or outside it, since in a workspace the go command reads the requirements and applies the replaces of every module it uses. A used directory's `go.mod` that does not parse fails the check. A `replace` of the framework module itself is the `pins` check's. It reads those files as they stand: the application's `go.mod`, the `go.work` at its root and the `go.mod` of each directory that file uses. It does not read a build told to read files from elsewhere (the `-modfile` flag, which names another module file, and the `-overlay` flag, which replaces or adds source files from anywhere, each on the command line or in the `GOFLAGS` variable), a workspace file that is not at the application's root (a `go.work` in a directory above it, which the go command finds by itself, and the file the `GOWORK` variable names, either of which can use a directory whose module path is under the framework's), or the code of the modules the application requires. |
| `framework-linkname` | No Go file in the application's tree links to a symbol of the framework by name. What the framework keeps to itself it keeps by the type checker, and a `//go:linkname` directive in a file that imports `unsafe` reaches a function or a variable of another package by its symbol, whatever the type checker refuses. The check reads every Go file the Go tool reads under the application's root, test files and generated files included, in every directory but version control's own (a vendor directory, a browser side's dependencies, and the directories a `./...` pattern passes over, since a package there is still built when another imports it by its path; a file whose own name begins with `_` or `.` is in no package's build and is not read). It follows a symbolic link to a file and to a directory, as the go command does for a package imported by its path, reads each directory once however many links lead to it, and passes over a link that leads nowhere. It fails naming the file and the line of each directive whose symbol is of the framework's root package or of a package under its import path. The directive's one-argument form, and one that names another package, pass. It does not refuse `unsafe` itself, and it does not read the code of a module the application requires unless it is vendored, or a build told to read files from elsewhere (the `-overlay` flag, which replaces or adds source files from anywhere, on the command line or in the `GOFLAGS` variable: a file it names need not be in the application's tree). |
| `gofmt` | Every Go file the author owns is gofmt-formatted, test files included, since the rendered CI's lint gate refuses an unformatted file; the generated files (`zz_gen_*`) are the generator's and are left out. `--fix` formats the files named, so a handoff's edit or a recipe's rewrite that left one is caught before the walk commits the step. |
| `prettier` | Every web root's browser files are formatted as the prettier the root pins formats them, as `gofmt` holds the Go files: the check runs the root's own prettier (`node_modules/.bin/prettier`, the version `package.json` pins) over the whole root with its `.prettierrc`, `.prettierignore` and `.gitignore`, so the generated client and the build's output stay out of it, and fails naming each file prettier would change. Nothing else holds them: the browser apps' lint does not run prettier, and a command that writes browser files under the application's own names (`impulse new`, `impulse add tenancy`) cannot wrap the lines again, since the formatter exists only once the root's packages are installed and those commands install nothing. So a web root whose packages are not installed is said to be not checked and never fails (the check's own line reads `SKIP`, not run, when no root is installed), and one whose `package.json` pins no prettier has nothing to be held to. The application's own CI installs every browser workspace in the job that runs the check (`go-check`), so there the check runs and an unformatted file fails the pull request. `--fix` formats the files named and changes no other: run `impulse check --only prettier --fix` once after the first install. |
| `gowork-off` | `GOWORK=off go build ./...` and `go vet ./...` succeed, so the pin in `go.mod` resolves on its own, without a workspace file above the application. |
| `shipped-programs` | Every program a release ships (`ShipsProgram("cmd/<name>", Platforms("linux/amd64"))` on an outlet) is a main package on a machine outlet that builds for each platform it declares: the outlet declares `Tokens`, since a session outlet serves no programs; a package main is at the directory; and `go build` of the package succeeds for each platform with `CGO_ENABLED=0`, as the image build compiles it after the tag, the compiler's output reported under a failure. An application whose outlets ship no program passes saying so. |
| `regen` | `go generate ./...` reproduces the generated files on disk: every `zz_gen` file, and every file under a `GenerateIdentity` tree, which is the generator's whole (content compared before and after, so it holds in untracked trees too). The `Warning:` lines the generate programs printed are listed under the result and counted in its summary; they never fail the check, since the program's warnings test is what gates the accepted set. A run that removes a generated Go file it no longer writes is stopped by the go command at that file, which it listed before the generator ran, though the generation finished; the check, like every `impulse` command that regenerates, runs `go generate ./...` once more in that one case and reads the second run. Needs the Spanner emulator and rewrites the working tree; `--skip-generate` leaves it out. |

Statuses: `PASS`, `FAIL`, `WARN` (reported, does not fail the run), `SKIP` (with the
reason).

## impulse audit

`audit` runs every generator program of the application with `-audit`, from the module
root, and prints what each raised: the schema warnings a generation always prints, and
the audit pass's findings, advisory findings about shapes the framework handles under a
stated limitation, which a normal generation never prints. Today's one finding names a
resource that stores files on a table whose rows the database deletes by cascade
(`ON DELETE CASCADE` on an interleave or a foreign key): those rows never pass through
the patch machinery, so the release of their objects never runs for them and the
application's sweep removes the objects later. The section is one heading per program,
`cmd/generate/resourcegenerator/generator.go (go run ./cmd/generate/resourcegenerator -audit)`,
with the lines beneath it or `no findings`. A section, `feature flags`, lists every
declared flag with its description and where it is used: the resources, fields and
methods its `@feature` gates and the Go and browser code that reads it (tests and specs
marked), or that it gates nothing and is read nowhere, which the `feature-flags` check
fails on; an application declaring none reads `none declared`. A section,
`permission engines`, lists every engine the application constructs (`access.New`
outside tests) by package, file and line, and whether it is handed a change signal,
which the `change-signal` check fails without; an application constructing none reads
`none constructed`. A last section, `sign-in`, reads the application's sign-in choices
back from its code and names the shape they add up to (below).

| Section | What it lists |
| --- | --- |
| one per generator program | The `Warning:` and `Audit:` lines the program raised, or `no findings`. |
| `feature flags` | Every declared flag, what it gates and where it is read. |
| `permission engines` | Every engine the application constructs, and whether it is handed a change signal. |
| `sign-in` | Every auth's sign-in methods with their choices in words, the application's own choices, and the shape they add up to. |

```sh
impulse audit
impulse audit --app path/to/app
```

The `sign-in` section lists every auth by name and package, and under it every sign-in
method the auth registers, where it is registered, and its three choices in words: how
wide it is open (the accounts of one Microsoft Entra tenant, any Google account, anyone
with an email address), who gives the roles (the directory or the application), and Open
or Invite only. That last choice is made while the application runs, on the auth's
Sign-in page, and is no line of code, so the section says where it is chosen and never
guesses it: it says what opening each method would make of the auth. A choice written
where the command does not read it (a slot built from settings, a configuration built in
another function) is said to be not read, and is not guessed either. The auth ends in the
shape its choices add up to, by number and name, or in `no named shape` with why. Then
come the application's own choices: tenants or none, whether a tenant can have a
directory of its own, and who creates a tenant; in an application with tenants each auth
also says whether a person belongs to one tenant or can be in several.

```text
sign-in
  staff (pkg/auth/staff)
    password (pkg/auth/staff/password.go:81): admits anyone with an email address; the application gives the roles; invite only until an administrator opens it to sign-up on the Sign-in page, which is chosen while the application runs
    shape: 1 Invite only. It starts so, and stays so until an administrator opens a method to sign-up on the Sign-in page, which is chosen while the application runs: opening password makes it 4 Open sign-up.
  the application
    tenants: none
```

The shapes are the eight combinations of those choices that have a name:

| Shape | The choices that add up to it |
| --- | --- |
| 1 Invite only | No tenants. The application gives the roles, and every method is invite only. An auth without tenants starts here. |
| 2 Directory members | No tenants. The application gives the roles, and a method that admits the organizations it names and nobody else (one or listed Microsoft tenants or Google Workspaces, an identity provider) is open, so everyone in the organization's directory gets an account. |
| 3 Directory roles | No tenants. One directory method whose directory gives the roles, alone in its auth. |
| 4 Open sign-up | No tenants. The application gives the roles, and a method anyone can use (the password method, any Microsoft tenant, personal accounts, any Google account) is open. |
| 5 Tenant-owned users, invite only | Tenants, with no tenant directories. A person belongs to one tenant, and a tenant's people come by invitation alone, whatever methods the auth has. |
| 6 Tenant-owned users, by directory | Tenants. A person belongs to one tenant, and a tenant's people can come through its own directory. The auth has no password method. |
| 7 Global users, invite only | Tenants, with no tenant directories. A person can be in several tenants, and a tenant's people come by invitation alone, whatever methods the auth has. |
| 8 Global users, each tenant chooses | Tenants. A person can be in several tenants, and each tenant chooses whether its people come through its own directory. |

Shapes 1, 2 and 4 are one auth at different moments: it is written as 1, and the section
says which method's opening makes it 2 and which makes it 4. Among the tenants' shapes, a
person belongs to one tenant where the auth's package hands its identity client
`identity.WithOneTenantPerPerson()`, and a tenant's people can come through its own
directory where the tenant record declares directories (a record annotated
`@tenantDirectory`) and the auth registers a directory method whose roles the
application gives; what a tenant then does with its directory is the tenant's choice,
made while the application runs. Where the tenant record declares no directories, an
auth whose roles the application gives is read as the invite-only shape with a
directory method as without one, and the section says in one sentence that the tenants
have no directories, naming a directory method as a way for an invited person to sign
in. Where it declares them and no such method is
registered yet, the auth is read as the invite-only shape too, and the section says
which method makes it the shape beside it. An auth outside the eight (a directory that gives the
roles in an application with tenants, or beside another method; a password method on an
auth that would otherwise be Tenant-owned users, by directory, a shape with no password
sign-in, where the reason names the files to change by hand; a method whose authority
is not read) reads `no named shape`. The persona method is part of no shape: an auth that
registers it beside the methods that make a shape is read as that shape, with a sentence
saying the persona method is registered in the test environments alone. That is no error in itself: nothing in the framework
acts on a shape's name, and no check of `impulse check` reads one. One of those auths is
refused for its own reason: a directory that gives the roles beside another method fails
`impulse check` (`auths-wired`), and the session library refuses the registration when
the application starts. The `--shape` flag of
`impulse new`, `impulse add tenancy`, `impulse add auth` and `impulse add method` starts
an application from a shape by its name or its number (`--shape
global-users-each-tenant-chooses` or `--shape 8`), and this section is where the result is
read back.

Each shape is described in full in
[docs/sign-in-shapes.md](../../docs/sign-in-shapes.md): who gets an account, what role
they start with, who manages the account, which sign-in methods it can use, the command
that starts from it and the line this section prints for it.

Findings never fail the command: it exits 0 with or without them. A program that fails
exits 1 with its output tail, and a program that does not take `-audit` exits 1 saying to
adopt the runner shape: the skeletons' generate program is three files in one directory,
`generator.go` declaring the generator (`newGenerator`), `main.go` running it and
printing the `Warning:` lines and, under `-audit`, the `Audit:` lines, and
`warnings_test.go` pinning the accepted warnings. It regenerates the tree like
`go generate` does, so it needs the Spanner emulator. It is a command of its own and not
a check: the check is the gate on every change, and the audit is read by decision, before
a release, after a schema change to a table that stores files, or when a stated
limitation is in question.

## impulse advise

`advise` is the one command that asks an agent for judgment, and it runs only when asked:
the check gates every change and the audit is read by decision, and neither becomes slow
or expensive on an ordinary run. It runs every generator program with `-audit`, as
`impulse audit` does, and writes a brief for an agent to `.impulse-advice.md` at the
application root: the option set in force; the `Warning:` and `Audit:` lines each program
raised, with the warnings test that pins the accepted set beside it; the deploy test that
pins the accepted role warnings (`pkg/deploy/deploy_test.go` in the skeletons), whose
values the agent reads; impulse's own reading of each kind, fixed and deterministic (for a
concealing key, whether the field is the default order, so every page pays, or a sort or
filter key, so only a caller who asks pays, and why the CASE stands; for an index warning,
the `CREATE INDEX` it wants; for a join path, that every list scans the table; for an
enumeration, its size in the metadata; for a cascade, that the sweep removes the objects;
for a grant warning, the disclosure a Forbidden answer makes); and the question each kind
leaves to judgment (whether the field's rank is sensitive and the table will page at
volume, whether the list pages at volume, whether the table will grow and the tenant
column is worth adding while it is cheap, whether the table is really an enumeration,
whether lingering objects are acceptable, whether the disclosure is intended). impulse
reads no test file itself; the agent does.

```sh
impulse advise                      # write the brief and print the command to run the agent
impulse advise --agent              # ask Claude Code, print its answer, remove the brief
impulse advise --skip-generate      # no emulator: the brief names each program's warnings test instead
```

With `--agent` the tool launches Claude Code non-interactively with the brief on standard
input and the read-only tools alone (`Read`, `Glob`, `Grep`), prints the answer, and
removes the brief; `--agent-command` and `--agent-arg` are `impulse handoff`'s. Without
`--agent` the brief stays and the command to run the agent is printed. The brief's rules
are an answer and no edit: per warning and finding, in prose, which branch of the reading
holds for this application, the answer to the question from what the code tells, and the
edit recommended (`masking:"positional"`, the index migration, the tenant column, the
runtime resource, the Read grant, or the accepted pin and where it goes), with the
questions the code cannot answer stated for the developer; nothing is run that writes.
There is no verify step, since nothing was to be changed. A program that fails, or does
not take `-audit`, is an error with `impulse audit`'s message: the brief would miss its
input.

## impulse render

`render` has two forms, and the arguments decide which.

With no arguments, inside an application, `render` writes the files impulse owns from the
application's code: today the CI workflows, `.github/workflows/ci.yml` with one browser
job per workspace, the `//impulse:ci` line's choices and the pins this impulse carries,
`.github/workflows/ci-cache.yml` beside it, and `.github/workflows/security-scan.yml`,
the daily vulnerability check and image scan of the default branch and the latest
release. It says for each file whether it was written or already read as the code
renders. `impulse check` compares the committed file
with the same rendering, so this is the command that brings the file back into agreement
after a change to the code, and the second step of moving the framework pin.

```sh
go tool impulse render          # inside the application, at the pin go.mod names
impulse render                  # the same, with the installed impulse
```

With a candidate and a directory, `render` copies one embedded skeleton into a new or
empty directory under the module path `--module` names, rewriting every import and
`go.mod` to it, and pins the framework as `new` does: `go.mod` requires the framework
module at the running impulse's version and the web roots pin the browser packages at it
(an impulse installed from a commit pins them at the latest release's, since a commit
publishes none), resolved with `go get -tool` and settled with `go mod tidy`. It is the
primitive `new` builds on and the way the templates are validated: render one, then
build, test, and `impulse check` the result. The templates carry `staff` as their
placeholder auth; `new` renames it, `render` keeps it. Both name the application after the
module path's last segment unless `--name` says otherwise.

```sh
impulse render solo ../beacon --module example.com/acme/beacon
impulse render sites ../harbor --module example.com/acme/harbor --dev-root ../impulse
```

`--dev-root` names a checkout of the framework (a directory whose `go.mod` declares
`github.com/impulseframework/impulse`). The rendered `go.mod` then requires the framework
at the zero pseudo-version and replaces the module with the checkout
(`replace github.com/impulseframework/impulse => ../impulse`, relative when the
application lives beside the checkout, absolute otherwise), so the application builds
against local framework work instead of a release, and `go mod tidy` reads the
requirements from the checkout; nothing is resolved through the module proxy, so an
impulse built from a checkout, which names no version the proxy serves, renders only this
way. The browser packages take the latest release's version. That replace line is for
development only; never commit it.

## impulse brand

`brand` writes an application's brand where the application carries it. Each browser
application declares its brand once, in `brand.json` in its own directory (the Angular
project's root: `web/console` in the flat layout, `apps/<site>/web` in the sites layout),
beside its logo:

```json
{
  "name": "Harbor",
  "logo": "logo.png",
  "logoSvg": "logo.svg",
  "primaryColor": "#005cbb",
  "backgroundColor": "#f6f7fb"
}
```

- `name` is the application's name as people read it.
- `logo` is the logo, a square PNG at least 512 pixels a side (and at most 4096), by its
  path from the declaration. Every icon is scaled down from it, each pixel the average of
  the logo's pixels it covers, and never up, which is why 512, the largest icon, is the
  least it can be.
- `logoSvg`, optional, is the same logo as an SVG, which becomes the favicon as it is.
  The command draws no SVG: the PNG is the source of every other icon.
- `primaryColor` is the brand's color, and `backgroundColor`, optional and white without
  it, the color behind the installed application while it starts and behind the logo on
  the icon a device cuts to its own shape. Both are a number sign and six hex digits.

One brand per browser application: a console and a portal of one application carry their
own, whether they share a workspace (`web/console/brand.json` and `web/portal/brand.json`
in the flat layout) or have one each (the sites layout). A browser application with no
`brand.json` is said to have none and left as it is; a declaration the command cannot
write from (a logo that is not square or is too small, with the size wanted; a color that
is not six hex digits; a field it does not know) is refused before anything is written.

```sh
impulse brand
impulse brand --app path/to/app
```

From each declaration `brand` writes:

| Where | What |
| --- | --- |
| `public/favicon.png`, `public/favicon.svg` | The favicon: the PNG at 96 pixels, and the SVG as it is where the brand names one. A `favicon.svg` the declaration no longer names is removed, since the page then links the PNG alone. The skeletons' worker configuration (`ngsw-config.json`) keeps whichever is there with the application's shell (`/favicon.*`). |
| `public/icons/icon-192.png`, `icon-512.png`, `icon-512-maskable.png` | The app icons the manifest names. The maskable one is the logo scaled into its central 80% on the background color, so nothing of the logo is cut away whatever shape a device gives it. |
| `public/manifest.webmanifest` | `name`, `short_name`, `theme_color` (the primary color) and `background_color`. |
| `src/index.html` | The title, the `theme-color`, and the icon links: the PNG, then the SVG where there is one. |
| `src/app/app.config.ts` | The name the frame draws (`provideFrame({ name })`) and the name of the brand's palette in the list the picker offers (`provideTheme`). |
| `src/_theme-colors.scss`, `src/theme.scss` | The theme's first palette. Angular Material's own generator, the `theme-color` schematic of the version the workspace has installed, generates the palette from the primary color (`ng generate @angular/material:theme-color --primary-color=<color> --directory=<source root>/ --is-scss --include-high-contrast=false --interactive=false --force`, run through the workspace's own `ng`, every answer a flag and no prompt asked), so the palette is the one that version's components are drawn for and the brand's exact color is its source. The theme sheet takes it as its first palette, `'brand'`, in the place of the skeleton's, and the other palettes stay. Once the sheet carries the brand's palette it is left alone: a change of color changes the generated file only. |
| `pkg/brand` | What the server says: each brand's name as a constant (`ConsoleName`) and its logo embedded (`console.png`, 192 pixels), behind an accessor (`brand.Console()`), named for the Angular project. A main hands them to the maintenance page by mount path, `maintenance.Serve(ctx, maintenance.Brands{"/": brand.Console()})`, and the page shows the name and the logo of the application a request is for. The source is composed whole on every run, and the logo's copy is written when the logo changed. |
| `pkg/auth/<name>` | The name the mail an auth sends says, its `Application` constant, from the brand of the browser application the auth's people sign in to: the one receiving the client of the first browser outlet that declares the auth. It is written into the package's file that declares the constant, among the Go files the Go tool reads: never into a scratch copy kept beside it (`_scratch.go`, `.draft.go`), and a call in such a file hands the maintenance page no brand either. |

The server's part is written rather than read, because the server cannot embed the
declaration where it sits: a browser application lies under the web workspace's own
`go.mod`, the boundary that keeps Go's tools out of the installed packages, and `go:embed`
refuses a file of another module. So the declaration stays in the browser application,
where a designer changes a color without touching Go, and `brand` carries the name and
the logo into the module. A brand no main hands to the maintenance page yet (the brand of
a browser application `impulse add outlet` or `impulse add site` copied in) is named at
the end of the run, with the line to add.

The palette's generator needs the workspace's packages installed. Where they are not,
everything else is written, the palette file is said to be missing with what to do, the
theme keeps the palette it had, and the command exits non-zero: install the packages and
run it again. A palette already generated from the declared color stands, so a run on a
machine without the packages changes nothing once the brand is written. Where the
workspace has a prettier installed, the generated palette is formatted with it, as
`impulse check` (`prettier`) holds every browser file.

The files are the application's after the first write. `brand` runs when asked, never as
part of `impulse render`, and writes a file only where the declaration changed what it
holds, so a second run over the same declaration writes nothing and a hand-tuned icon is
kept until the logo or the background changes. A new application starts with a
declaration under its own name, the skeleton's colors and the skeleton's logo;
`impulse check` (`brand`) reminds, without failing, while a browser application still
carries that logo or the icons cut from it.

## impulse upgrade

`upgrade` moves an application through the steps the ledger records after the one its
pin stands at, then to the running impulse, one commit each. The framework is one module,
so an application's position is one version: the framework pin, the version its `go.mod`
requires `github.com/impulseframework/impulse` at, which the tool directive shares. A step
names the release that added it (`Impulse`), together with the recipes an application at
the step before it needs (`Recipes`), or a note that none is needed (`Note`); the release's
browser packages carry the module's version, so a step's web pins follow from its release
too. Where the application stands is read, never recorded: the latest step whose release
the pin reaches is the position, and every step after it is pending. An impulse release
that changes nothing an application has to do adds no step, and the walk moves the pin to
it last, by itself.

```sh
go run github.com/impulseframework/impulse/cmd/impulse@<version> upgrade --dry-run   # what the walk would do
go run github.com/impulseframework/impulse/cmd/impulse@<version> upgrade             # walk, one commit per step
```

Upgrade impulse first, then bedrock (`bedrock upgrade`), and land both in one change:
bedrock's checks read what the walk writes (the drivers' settings the configuration embeds,
the release file, the processes), and the walk reads nothing bedrock renders; the code after
a step may need what only the newer stack provides.

The impulse to upgrade to is run, not pinned first: a `go get -tool` ahead of the walk
would skip the steps' recipes. The walk refuses to start on a working tree that is not
clean, so each step is one commit, and refuses to start when it moves web pins and bun
cannot run, before anything moves. Each pending step is one commit. Its recipes run first:
a recipe detects the old form in the application (the generator program, the annotations,
the known seams) and edits only where it finds it, so running it twice is safe, and so is
running it on an application whose pin was bumped by hand ahead of its code. Then the pin
moves to the step's release with `go get -tool github.com/impulseframework/impulse/cmd/impulse@<release>`
(the tool and the libraries are one module, so one requirement moves both; a pin already at
or beyond the step's stays, and Go resolves the release against the application's whole
requirement list, refusing in its own words a release no list holds), `go mod tidy` runs,
and `go.mod` is read back: a pin below the step's version after the move was held back by
another requirement, and one missing was dropped by the tidy, so nothing in the application
needs the framework; either refuses the step, naming what `go.mod` holds and what the step
pins, and a pin carried beyond the release by another requirement stays, noted. Then the
step's web pins move: the release publishes `@impulseframework/resource`,
`@impulseframework/resource-angular` and `@impulseframework/resource-firestore` at its
version without the `v`, and the walk rewrites that version into every web root of the
application (each directory holding an `angular.json` whose `package.json` names the
packages), editing the version strings in place wherever they appear (dependencies,
peerDependencies, overrides) so the file keeps its formatting, then runs `bun install` in
that web root so `bun.lock` records the registry versions; both files go into the step's
commit. A web pin at or beyond the step's stays, like a Go pin bumped by hand ahead of its
step, and a web root already at the step's pins is said to be there already; a `bun install`
that fails stops the step with the install's words and the edit in the tree. The dry run
lists the web roots and what moves in each. An application without a web root naming the
packages has nowhere to move them, and the plan says so. A release's server and its login
page are held together in this repository, not at the walk: the sign-in steps the generated
server asks the page for (`cmd/impulse/internal/authpkg`'s `SignInSteps`) are held by their
test to the steps the login page declares (`impulseframework.signInSteps` in
`web/projects/resource-angular/package.json`), and both ship in one release at one version.
Then the step is rendered and checked by that release: the owned files are rendered again
(`go tool impulse render`), `go generate ./...` runs (the application's own generator
program, built against its pin), and `go tool impulse check` runs, so every commit the walk
makes builds against the release it names and is checked by the impulse that knew it; a
step the running impulse added itself is rendered and checked in process. A clean check is
committed as `upgrade: impulse <version>` (`upgrade: impulse v0.2.0 (recipe paging)`, the
web pins moved listed after it, and the versions moved into the body when the subject would
run long) with the step's note and recipes in the body (the `upgrade` type releases a
patch, as the `title` check's list says, since an upgrade moves the pin and re-renders the
owned files and must not wait for a later releasing merge). Last, when the running impulse
is a release the pin is still behind, one that added no step, the pin moves to it the same
way (`go get -tool`, then `go mod tidy`, the web roots' pins with it), the owned files are
rendered again, `go generate ./...` and `impulse check` run, and a clean check is committed
as `upgrade: impulse <version>`; an impulse built from a checkout makes no such move, and
one older than the pin refuses, since the pinned one is the impulse to run (`go tool
impulse upgrade`). A failing check stops the walk with the step's changes staged and the
handoff brief written (`impulse handoff`, below), and so does a `go generate ./...` that
fails after the move: its brief quotes the generation's last lines as the obligations, says
what the step moved, and carries the step's note under "What it means", so the author
learns what the step needs of the generator program. At a step checked by the pinned
impulse, that impulse writes the brief from the staged tree and, with `--agent`, runs the
agent and verifies its work, through `go tool impulse handoff --staged` with each recipe's
change and meaning passed through, so the brief, the agent and the verification are the
release that added the step's; every release of the framework as one module takes
`--staged`, from v0.1.0 on. A step the agent cleared, its work verified clean, is
committed under the step's own subject with the body saying the agent did the wiring, and
the walk goes on to the next step; it stops when the agent's work does not verify, or when
no agent ran. Then fix the obligations or hand them to the agent, commit, and run `upgrade`
again; it resumes from whatever `go.mod` says, since the pin is the checkpoint and nothing
else records progress. No step is skipped: a recipe is written against the shape the step
before it left behind. A step with no recipe is a pin move and a commit.

The ledger (`cmd/impulse/internal/ledger`) opens with the framework's first release as one
module, v0.1.0, whose step says that no code change is needed. A step is appended when a
release needs something of an application beyond the pin move (a recipe, or a change made
by hand that the note describes), and never otherwise, by the pull request of the release
that carries it, which the step names (`Impulse`); the step's Go pin and web pins are
derived from that release, so nothing else is recorded. The ledger's validation, which its
test runs over the recorded steps, holds every step to its shape: at least one step, each
naming the release that added it as a canonical version (no pre-release or build part) and
a note, no release behind the step before's, a step at the same release as the step before
naming a recipe (the pin moves nowhere, so the recipe is the step), and no recipe named
twice in one step. The repository's CI holds the last step's release to the release being
cut (`release ledger`, `.github/scripts/release-ledger.sh`): on release-please's branch,
where the release manifest (`.release-please-manifest.json`) carries the version the
release takes, a step names that release or an earlier one, never one the release being
cut leaves behind, since the walk would then take an application to a step no release
carries. A release that asks nothing of an application adds no step, so the last step may
stand behind the last release.
A breaking change in the framework is not done until the step that carries it records its
recipe, or says that no code change is needed. Applications that predate the first release
are adopted once by hand, not upgraded.

## impulse handoff

`handoff` is the protocol between the tool and an agent for the work the tool cannot do
mechanically. It runs `impulse check` on a clean working tree in a git repository and,
when checks fail, writes a brief for an agent to `.impulse-handoff.md` at the application
root: the failing checks verbatim as the obligations, the option set in force (read from
the generator programs), a reference application when one is given, and the rules. The
brief is Markdown any agent can read; the tool never reads it back. It is transient: the
skeleton's `.gitignore` names it, and a command that stages its changes (`impulse add`,
`impulse upgrade`) leaves a brief standing from an earlier handoff out of the index, so it
is never committed with the next change. A brief an application committed before its
`.gitignore` named it stays as it was committed until it is removed from the repository
by hand. A failing check that
carries a meaning (`conditions-proven`: how a conditional grant is proven over seeded rows)
adds it to the brief under "What it means".

```sh
impulse handoff                      # write the brief and print the command to run the agent
impulse handoff --agent              # launch Claude Code on the brief, then verify
impulse handoff --verify             # after an agent run by hand: check + guardrails
impulse handoff --reference ../tenanted --skip-generate
impulse handoff --staged             # the change staged in the index is the one under review
```

With `--agent` the tool launches Claude Code non-interactively (`claude -p`) with the
brief on standard input and a tool set restricted to reading, editing, and the build
commands; `--agent-command` names another executable, and each `--agent-arg` appends an
argument to its command line (a model, a turn limit, a budget). When the agent returns, or on
`--verify`, the tool re-runs the check and adds a `guardrails` result: the generator
programs' option set and the lint configuration (`.golangci.*` at the root, the eslint
configuration of each browser app) must read the same as the git index holds them, and
with `--agent` the commit and the staged paths must not have moved. A weakened check is a
failure, not a pass. A clean verification removes the brief; the pull request is the
review, and there is no gate before it.

The brief's rules are the ones the verification enforces: run the check until it is
clean, do not edit generated files, the generator programs, or the lint configuration,
do not stage or commit, keep the tests table-driven, stop when the check is clean.

With `--staged` the change under review is what is staged in the index, and the working
tree may hold it: the brief lists the staged paths under "What changed", with what each
`--change` says was changed and what each `--meaning` says it means (both repeatable), and
the check, the agent and the verification run as without the flag. A change beside the
staged one, unstaged or untracked, is refused, so the index holds exactly the change under
review and the agent's work is exactly its own. This is how `impulse upgrade` hands a
failing step to the release that added it: the walk stages the step and runs `go tool
impulse handoff --staged` through the pinned impulse, each recipe's change and meaning
passed through, so the brief, the agent and the verification after it are that release's.

## impulse add

`add` makes the deterministic half of an option, stages it, runs the check, and hands
the failing checks to an agent exactly as `impulse handoff` does, with two additions to
the brief: what the tool changed (and what it could not), and what the option means in
this framework, alongside a rendered reference application with the option wired.

Where no check fails, `add` says the option is wired and writes no brief, unless the
change itself lists work the check cannot see: an edit the tool could not make ("Left to
the agent" on the screen, "they are yours" in the brief), or a line it leaves to the
author. Then the brief is written with the check clean, and its obligations are the lines
under "What changed". A new application that passes its checks as `impulse new` makes it
ends in a brief the same way wherever its start leaves such a line.

Every option ends in the one regeneration. `go mod tidy` runs first, so `go.mod` requires
what the option's new code imports (a directory method's file imports the session
library's `google` or `azure` package, which signs in through `github.com/coreos/go-oidc/v3`,
a module an application with password methods alone never required), and each requirement
it adds, moves or drops is reported among the changes; then `go generate ./...` runs. A
tidy or regeneration that fails refuses the command with the failing command's output,
rather than leaving a note for the agent: an application whose router, generated management
and browser client were not regenerated is half-bound, still describing the application before
the option. The edits already made stay on disk, to fix the cause and regenerate by hand.

```sh
impulse add outlet portal --prefix portal/api --auth staff --agent
impulse add outlet kiosk --prefix kiosk/api --auth members
impulse add outlet machines --prefix machines --tokens staff
```

`add outlet` adds a router outlet to a flat application. A session outlet binds to an
auth, and `--auth <name>` says which: the generator program gains `WithRouterOutlet(name,
prefix, Auth(<pkg/auth/name>, <its methods>), WebApp("/<name>"))` after `GenerateRoutes`
and a `GenerateTypescript` target for the outlet copied from the default target's, and the
console's browser project is copied to `web/<name>` with its API prefix, base path, and
compiler output rewritten (and its XSRF cookie, when the auth is not the console's) and
registered in `angular.json` (serving under `/<name>` on the next port), the package
scripts, and the Procfile. The copy carries the console's brand declaration where the
console has one (`brand.json`), which the report says: the brief names giving the new
application its own and running `impulse brand`.
A console that was alone at `/` moves when the second browser
application arrives, since the generator refuses an application at `/` beside another (an
installed application's scope is every URL under its start): it goes to `/console`, the
name of its Angular project, with its API at `/console/api`, in the generator program
(`GenerateRoutes("pkg/router", "console/api", ..., WebApp("/console"))`), the project's
`baseHref` and `servePath`, its proxy, its environments, and its base element, and the
regenerated router answers the root alone with a redirect to `/console/`. The command
follows the move through the application's own files and reports each by file and line:
the App's served browser app is built under `/console` (`NewBrowserApp(..., "/console")`),
the console's auth is told its pages there (`LoginURL`, `MailLoginURL` and `DevicePage` of
the auth's `Settings`, where the application builds them outside its tests), and the
hand-written Go (tests included), the README, the process files and the environment
template that named `/api` name `/console/api`. That last rewrite is of text, so the report
lists every line for reading. A path after a host's port (`http://127.0.0.1:8090/api`), a
login page named outside the auth's settings or as a whole address, a `DevicePage` a test's
settings name, and a test that asks for a page at the root are left as they are. For a
machine outlet (`--tokens
<name>`) the program gains `WithRouterOutlet(name, prefix, Tokens("<module>/pkg/auth/<name>"))`,
naming the auth whose Auth issues the outlet's tokens: the generated router composes the
token check in front of the outlet's routes and the device flow's two endpoints under its
prefix, the auth's session outlet gains the token routes where a signed-in person issues,
lists and revokes their application tokens and approves a command-line client, the
console shows the token page over those routes through the library's frame (Your API keys
in the person's menu, `<mount>/api-keys`) and mounts the client library's device page at
`<mount>/device` (the `outlets` skeleton carries both), the auth's
settings name the device page as its `DevicePage`, where a device authorization sends
the client's person, and the console stays where it is. (An
application that kept a hand-written router gains `ServesSessions()` or nothing, as
before.) `go generate` then emits the outlet's routes, handlers, and client, and the
generated router mounts the outlet from its declaration: its group, its login routes, its
not-found handler, and its browser application.

Under the generated router the command then writes what the generated `Handlers` now
requires of the application, so the application builds and passes its checks as the
command leaves it. For a session outlet, a new file of the App's package
(`<name>_outlet.go`) declares the outlet's session getter and its deep-link and assets
pair, over the resource package's served browser app the App now holds for the outlet's
built bundle; the `Configurer` gains `<Outlet>Dist()`, the site configuration reads it from
`APP_<OUTLET>_DIST` (the outlet's directory under the web workspace's `dist` by default),
the environment template names the variable, and the test configurers answer it with no
directory, as they answer the console's. For a machine outlet, the auth's file gains the
outlet's name, the `DevicePage` setting passed to the Auth's construction
(`session.WithDeviceVerificationURL`), the registration in `New`
(`session.RegisterMachineOutlet`, once, before the first request) and `<Outlet>Tokens()` on
its `Handlers`, as the tool renders an auth that issues a machine outlet's tokens; every
construction of the auth's `Settings` names the device page, a test harness's too, since
`New` refuses an empty one; the console gains the device page (`app/components/ui/device/`,
from the reference) and its route among the signed-in pages; and a new file of the App's
package declares the outlet's token check. The command refuses a machine outlet named like
something the auth's `Handlers` holds already (a sign-in method's handle, such as
`password`), since the outlet's token check is held under the outlet's name. Where a file
does not read as the tool writes it (no `Configurer` interface, an App that holds no
handlers of the auth, an auth file whose `New` is laid out otherwise, routes that do not
open the frame's children as the skeletons do, a file of the outlet's name there already),
nothing is written there and the report lists what is missing under what the command could
not make. The members (`@outlet`), the outlet's own routes and its tests are the agent's,
and the compiler, `outlet-wired`, `outlet-shape`, and `login-pages` hold the application to
the rest.

`add tenancy` makes a flat, untenanted application tenanted. Nobody creates a tenant
directly: staff bring one in by inviting the person who will administer it, and that
person's first sign-in makes it. The tenant table becomes the
next schema migration, with two development tenants as a data migration under
`schema/devseed`; the tenant-record struct joins the resource package as a global resource
annotated `@tenant`, from which the generator derives the tenant segment and emits
`New<Record>Roster`, the constructor of the tenant roster, and
`New<Record>DeclarationReader`, the reader of a tenant's declaration; the generator
program gains `WithConcealedDomains`; the data level gains the roster, built with that
constructor over the live service's tenants signal and started where the configuration
is built (a new `tenancy.go` plus a field and the start inserted into
`DataConfiguration`), so a tenant made on any instance is usable at once, without a
restart, and handed to each auth the level holds (`SetTenantNames`), so the invitation
screen names the tenant an invitation seats a person in and not its id; the app exposes the roster and the roster's copy of the declarations to the
generated code as `TenantRoster()` and `TenantDeclarations()` and hands the roster's
`Domains` and the declarations to the session permissions (a new `tenancy.go` plus the
`Configurer`, `App` and `UserPermissions` edits); and the browser app takes the tenant's
shape: the tenant pages are copied in (the tenant's sign-in page where a tenant can have
a directory, the configuration of All
tenants, the library's list page over the record, and the dialog a person who signed up
creates their own tenant through), the menu
file gains the tenant menus ahead of its others (Tenants with All tenants,
People with the tenant's side of the auth's people, and, where a tenant can have a
directory, Tenant with Sign-in settings;
the Invited tenants page, where staff bring a tenant in, is among the auth's pages the
System menu already places, and the menu file's test, where it lists that menu's pages as
the skeleton's does, gains it after Mailed links), the
frame's configuration names the place outside every tenant (Configuration, with a gear),
and routes still as the skeleton rendered them are written in the tenant shape, while the
library's frame carries the tenant the URL names, the guard, the refusal page and the
place switcher. The record's name, marked `@tenantName`, is what the session's tenant
list shows. A record of another name gets everything in its own words: with
`--tenant-table ServiceAreas` the pages, the menus and the routes say service area, and
every name the generator derives from the record is the record's; the files are written
without wrapping their lines again, so the brief says to run `impulse check --only prettier
--fix` once the web root's packages are installed. The declaration rides along, unless the command is given `--tenant-directories=false`: the tenant table carries `DirectoryOnly` and a
`<Record>Directories` table beside it, with the two unique indexes that hold a tenant to
one directory and a directory to one tenant, the directories record joins the resource
package (`@tenantDirectory`, tenant-scoped, its directory set from a sign-in and never
typed), both records track changes, each auth's `Settings`
carry the reader and its `New` sets the library's sign-in rule over it
(`signin.TenantPolicy`, the memberships read through `resource.SignInRoles`), and the
data level's settings literal passes the reader in. An Auth has one sign-in rule, and a
second `SetSignInPolicy` call panics when the auth opens, so where `New` set the second
factor alone, as the base's auth package does, that call is taken out and the tenant
policy is written with the second factor chained last
(`signin.TenantPolicy(...).Then(requireSecondFactor)`); where `New` sets a rule in a form
the command does not read, the brief says the two must become one call, in the words the
`second-factor` check uses. The auth a tenant is made through,
the one whose outlets serve the record, where the application assigns its roles and
generates its management, is handed what makes a tenant from an accepted invitation: its
`Settings` gain `TenantMaker`, its `New` refuses a nil one with a sentence and passes it
to the generated identity client as its third argument, and on a data level in the
skeleton's shape the roster is built ahead of that auth in `NewDataConfiguration`
(`tenants := <handlers>.New<Record>Roster(...)`), the auth's settings function gains a
parameter the construction passes `<handlers>.New<Record>Maker(tenants, liveService)`
through, and `startTenants` only starts the roster. On a level the command cannot
follow (no live service field, a client the literal builds in place, an auth built
elsewhere) the roster is built in `startTenants` as before and handing the maker in is
recorded as the agent's, since the auth refuses to start without it. Every edit whose anchor the
application lacks is recorded in the brief as the agent's. What described the application
without tenants is brought up where it still reads as the skeleton rendered it: the
README's opening says the application has tenancy, in the record's names, and the brief
says the rest of it is the application's to write; the comment over `UserPermissions`
in `app/app.go` says what the function does once it is handed the roster and the
declarations. The tests the base started with are brought up to an application with
tenants where they are the base's still: the two harnesses (`test/authz`,
`test/integration`) are written again to build the roster, the generated maker and what
each tenant declares, the test of the role files holds a tenant's role in a tenant of
the development seed, `sign_up_test.go` and `sign_up_role_test.go` are taken out (the
integration harness keeps what the other tests shared with them), and
`password_reset_test.go` opens sign-up with no role, since an application with tenants
refuses a request that names one. So the application passes `go vet` and its own tests
as it was made. Where a harness or a test of sign-up is no longer the base's, none of
them is written over, and the suites the base carried and a tenanted application does
not are each named in the brief with what becomes of them (`sign_up_test.go` and
`sign_up_role_test.go` are removed, `first_administrator_test.go` follows the harness).
Each auth's role file loses the grants on what the regeneration stopped serving: an
application with tenants lists no roles for a person who signs up, so the base's grant on
`<Auth>SignUpRoles` goes, since the permission engine refuses a role file that grants a
resource the application does not serve. The command then writes the role a tenant's
administrator holds, since an application with tenants builds no router until one is
named: `<Record>Administrator` (`TenantAdministrator` over `Tenants`), a domain role in the
role file of the auth whose people make tenants, with the grants the tenanted skeleton's
`TenantAdministrator` holds on what the application serves (a tenant's people, their
groups and the roles they hold there, and the tenant's directory where it can have one),
each under the condition the skeleton holds it to. The grants under a condition are the
methods that change a person and the lists that show more of one, each kept from a
person another tenant or the whole application also holds, with the withdrawal of an
invitation that still stands and the removal of a role held in the tenant. `impulse
check` holds every conditional grant to a test that proves it over seeded rows
(`conditions-proven`), so the command writes that test with the role:
`test/integration/tenant_people_test.go`, the tenanted skeleton's test of the same name
in the application's names, which serves the application, signs a tenant's administrator
in and proves each condition both ways round. Where the auth has the password method the
test signs people in with a password; where it has none (the start of Tenant-owned
users, by directory) the role carries no grant on a person's password, which such an
application does not serve, and the test signs people in through the simulated directory,
so it runs under `-tags skipAuth` and is skipped, saying so, under the default build.
Every grant the test proves is the one role's, and the application's lint refuses a
parameter that takes one value in every call, so the command writes the harness again
beside the test with its `provesGrant` marked for the lint (`//nolint:unparam`, with the
reason), as the tenanted skeleton's harness is; a harness with no such test beside it
carries no mark, since the lint refuses a mark that silences nothing. The
command writes the test only where it holds as the application is: where a test of that
name is there already, where the harness, `grants_test.go` or the sign-in helpers it
stands on are no longer as the command writes them, where another test of the package
declares one of its names, or where the conditional grants the application serves are not
the ones the test proves, the role is written with the grants that hold under no
condition, and a line under what the command could not make names each grant left out
with its condition and says why the test was not written. Until the author adds those,
each with its proof, a tenant's administrator does none of them. The auth's
`AdministratorRoles()` and
`FirstAdministratorRoles()` (`foothold.go`) name it as held in every tenant, the
deployment package's test of the first administrator's roles holds it to the role file,
and the application's own administrator role gains the grants a tenant is made with: the
record, the invitation that makes one, its withdrawal and the list of the invitations
that stand. So the application starts, and its served tests run, as it was made. An
application that names a role held in every tenant already keeps its own, and where the
command cannot write the role (a foothold or a role file that is not as the skeleton
writes it) a line under what it could not make says why and gives the step. Which
resources become tenant-scoped and how their rows are assigned, the bootstrap order, the
harnesses where they were not the base's, the tests an application with tenants adds,
routes edited since the skeleton rendered them, the dashboard's
tenant and the directories the development seed declares are the agent's, and
`tenancy-wired` holds it to them.

`--one-tenant-per-person` says a person belongs to one tenant, as in an application whose
tenants own their people: every auth whose roles the application gives builds its
identity client with `identity.WithOneTenantPerPerson()`, so a role in a second tenant is
refused however it would be given. Without it a person can be in several tenants. An
auth whose client is built in a form the command does not follow is recorded in the brief
as the agent's.

`--tenant-directories` says whether a tenant can have a directory of its own, and is on
unless told otherwise. With `--tenant-directories=false` the command writes an
application whose tenants have no directories: no directories record or table, no
`DirectoryOnly` on the tenant table or the record, no directory reader in an auth's
`Settings` and no directory admission in its `New`, no tenant sign-in page, menu or
route, and All tenants without its directory-only column. Everything else is written as
above: each auth's `Settings` still carry the generated declaration reader, which
answers nothing for every tenant, and its `New` still sets the library's sign-in rule
over it, so a tenant admits everyone it seats, however they signed in. In such an
application a directory method is a way for an invited person to sign in and nothing
more, whichever methods it has or gets: no tenant takes a directory, none is opened to
one or made directory only, and a person from an organization's directory who has no
account and no invitation joins no tenant (they are refused while the method is invite
only, and where it is open to sign-up they hold no tenant and may create their own).
Laying the tenant directories in afterward is the author's act, which the brief
describes; no `impulse add method` does it.

`--shape <name>` starts from a sign-in shape by name (section impulse audit lists the
eight) and fills in the tenants' choices. `add tenancy` takes the four an application
with tenants has: `tenant-owned-users-invite-only` (5), `tenant-owned-users-by-directory`
(6), `global-users-invite-only` (7) and `global-users-each-tenant-chooses` (8). The two
tenant-owned shapes set `--one-tenant-per-person`, and the two global ones leave it off.
The two invite-only shapes set `--tenant-directories=false`: a tenant's people come by
invitation alone, whatever sign-in methods the application already registers, and the
command names the ones that go through an organization's directory as ways for an
invited person to sign in. The other two write the tenant directories: a tenant's people
come through its own directory. For `global-users-each-tenant-chooses`, where no sign-in
method goes through an organization's directory yet the command says which `impulse add
method` adds it. `tenant-owned-users-by-directory` writes the sign-in methods too: on
each auth whose roles the application gives that registers neither a Google nor a
Microsoft method, a Google method open to any Google Workspace and a Microsoft method
open to any Microsoft Entra tenant, neither with personal accounts, ahead of the tenants
(the same two `impulse new` writes for the shape); an auth that has either is left as it
is. The shape has no password sign-in, and no command removes a sign-in method, so the
password method stays where the auth has it: the command says that it keeps the
application outside the shape and names the files to change by hand, and `impulse audit`
says the same until it is gone.
Whether a tenant takes a directory, and whether that directory's people get an account
without an invitation, is each tenant's choice, made while the application runs, so the
command writes nothing for it and says where it is chosen. A flag that contradicts the
shape is refused, naming both (`--shape global-users-invite-only
--one-tenant-per-person`, `--shape global-users-each-tenant-chooses
--tenant-directories=false`), and a name the command does not know is refused with the
names it knows. What the shape filled in is printed after
the change as `Starting point:`, and `impulse audit` reads it back from the code.

```sh
impulse add tenancy --agent
impulse add tenancy --tenant-table Organizations
impulse add tenancy --one-tenant-per-person
impulse add tenancy --tenant-directories=false
impulse add tenancy --shape global-users-each-tenant-chooses
```

How a tenant comes to exist is separate from who may enter one, and it is no declaration.
Nobody creates a tenant directly: an `add` on the record's collection answers 400 with a
sentence saying how one is made. Staff bring an organization in by inviting one person:
the auth's generated `Invite<Auth><Record>` takes the person's address and the record's
fields, each named for the record, with whether the tenant is entered through its
directory only and the role its directory's people start with; sending it takes the
method's grant and the Create grant on the record, and a name a tenant holds is refused
there. The invited person's first sign-in makes the tenant, seats them with the roles
that administer a tenant in the auth and, where they came through an organization's
directory, makes it the tenant's own, in one transaction; `<Auth>Invited<Record>s` lists
the invitations that still stand and `Withdraw<Auth>Invited<Record>` withdraws one. The
library draws the page from the generated description, and each tenanted skeleton's role
file gives its `Administrator` role the three, beside Create on the record. A person who
signs up creates their own while an administrator has a sign-in
method open to sign-up: every method starts invite only, and an administrator opens one on
the auth's Sign-in page in the running application (`Open sign-up`, behind a warning that
says who will be admitted and is confirmed by typing a word) and makes it invite only again
there. A stranger then signs in through the open method, holds no tenant, and the
application offers one page, create your tenant; the generated route
(`POST <prefix>/create-<record>`, no grant needed) reads the choice in the transaction that
writes the row and refuses while every method is invite only, writes the row, seats the
person with the domain roles the App names in `<Record>AdministratorRoles()` and hands the
tenant to the roster, so their next request stands in it, and a person who already holds a
tenant is refused, an administrator of one tenant never creating another. So is the real
person behind a session that acts as a role, who would become the administrator: nobody is
the subject of what they do while they act as someone else. The generated
client exports the route and the body's shape (`create<Record>Route`, `Create<Record>`) for
the page, which each tenanted skeleton's console offers from its dashboard to a session
without a tenant, and only while the route would serve: the dashboard asks the same route
with `GET`, which answers whether the caller may create a tenant now
(`Create<Record>Offered`, by the three facts the creation refuses on), and shows its card
on a yes alone, so nobody is offered a dialog the server would refuse while every method is
invite only. The route is generated wherever it can serve: the record is on session
outlets of one auth whose management the program generates and whose roles no directory
writes. `@tenant(creation: ...)`, which declared the choice in code, is refused at
generation with what replaced it, so no application ships open sign-up by a line of code.

`add auth` adds an auth: a population that holds roles in its own permission store and
signs in through the methods on its Auth. An auth is a package, `pkg/auth/<name>`, and
the base has one, `staff`. The new package is rendered from the same source as the
skeletons' auths, so it owns `<Name>Sessions`, `<Name>SessionUsers`,
`<Name>SessionIdentities`, `<Name>SessionAuthEvents`, `<Name>SessionPendingSignIns`,
`<Name>SessionSignInAttempts`, `<Name>SessionMailLinks`, `<Name>SessionFactorCodes` and
`<Name>SessionTrustedDevices`, the `<Name>` store prefix, the `<name>`, `<name>-xsrf` and
`<name>-trusted` cookies, and
`pkg/auth/<name>/roles.json` (empty, embedded by the package) from the start, to be
authored with roles named for their job in PascalCase and never for where they are held,
each with a description saying what it is for (section Vocabulary, Auth), with one
file per method; its session tables are written as migrations under the new prefix and
its access tables copied from an auth the application has; and the data level constructs
it beside that auth, its settings built by a function of its own on the environment
struct (`<name>Settings`, copied from the auth it is written beside, so the constructor
stays one call per auth; a directory registration of that auth's stays behind, and the new
auth's own join the copy), with an accessor in a new file. The package carries the engine's
change signal over the live service as the skeletons' auths do (`access.WithChangeSignal`
from `Settings.Signals`), so the `change-signal` check passes for it. The outlet the auth
serves is pointed at it: its declaration in the generator program gains
`generation.Auth("<module>/pkg/auth/<name>", <methods>)` in place of the one it carried,
so the regenerated router mounts the auth's session group under the outlet's prefix.
Where the outlet's program generates each auth's management (`GenerateIdentity`), the
package is rendered with the auth's identity client: `New` builds it from the tree's
`<name>identity` package, over the auth's permission engine and session manager, and sets
the hooks a sign-in gives roles through from it, and `Identity()` returns it. The command
hands the auth to the App where the App is the base's, so the application it leaves
builds: the Configurer asks for the auth and its identity client (`<Name>()`,
`<Name>Identity()`), which the data level answers and every test configurer answers too.
A harness that opens the first auth and builds the App over it (the served suites',
`test/integration`) opens the added auth beside it, over the same database, in each
function that opens the first: with the settings the application has one of as the first
auth's opening writes them, a mail recorder of its own and its links to its outlet's
login page, and with none of the first auth's directory registrations, so the suites
serve the added auth's outlet as the application does. A configurer that holds no auth
(the authorization harness, `test/authz`) answers none. A harness the command cannot
read (one that opens the first auth otherwise, or builds the App over an auth handed to
a function) is left answering none, and a Skipped line says what its suites need.
The App holds the auth's session
handlers, its identity client and its permission engine, declares `BindAuth`, which the
regenerated router's `Handlers` requires once two auths are declared, over the record of
the auth a request came through (`pkg/auth`, `Bind` and `Name`), and answers a session's
permissions from that auth's engine; and the accessor of an outlet `add outlet` laid in
returns the new auth's session handlers in place of the handlers of the auth the outlet
was laid in with, so the new auth's people sign in there and nobody else's session opens
there. An App that is not the base's keeps what it has, and a Skipped line says what it
still needs; the default outlet's session surface is the handlers the App embeds, which
stays the agent's to change. A program that
generates no management gains the option, with a tree beside its resources package, where
the new auth is the only auth its outlets declare (a site `add site` laid in, bound to an
auth of its own); where another outlet of the program declares an auth, the option would
generate that auth's management too, so none is added, the auth is rendered without a
client, and a Skipped line says what to add. The outlet's browser application is rebound
with the outlet, in the flat layout and in the sites layout: its routes ask the library for the pages of the
new auth's management in place of the old one's (`identityRoutes` with `auth: '<Name>'`),
or for none where the program generates none, and its HttpClient names the new auth's XSRF
cookie; routes that asked for no pages are named for the agent with the request to spread.
The migrate command's test is brought to a release that invites the new auth's first
administrator where the test is as the command writes it: the cases that give the first
auth a holder of the Super Administrator role give the new auth one too, and the test of
a start whose directory gives every role says that a release stops for the new auth's
first administrator and that `-reinvite-administrator` takes that auth's name alone.
Checking its roles in the deploy, its development identities, and the stranger tests are
the agent's.

Every auth mails, through the application's one sender and to the login page of the
surface bound to it: an administrator invites a person by address with the roles the
invitation gives (`Invite<Name>User`, a method of the auth's generated management the role
file grants), the invitee follows the link to accept it, signed in or signing up, and a
signed-in person adds an address and proves it by following the link sent to it. The
package's `Settings` take the sender (`Mail`, which the data level opens: the log in
development, where nothing is sent and a developer follows the link from the log, and the
Google Workspace SMTP relay where the deployment names a mailbox, `APP_MAIL_MAILBOX` with
its app password `APP_MAIL_APP_PASSWORD` and, where the mail is from an alias of that
account rather than the account itself, `APP_MAIL_FROM`) and the login page (`MailLoginURL`, the
application's public address `APP_PUBLIC_URL` and the surface's login path); the settings
copied from the source auth carry both, and the command names the login page of the new
auth's outlet in the copy (`<mount>/login`), so the invitation a release mails to the
auth's first administrator opens where the auth's people sign in. Where the outlet serves
no browser application the auth has no login page of its own: the first Skipped line says
so, before any release, and names the setting to give. An accepted invitation gives its roles in the transaction that
consumes it (`SetInvitationAccepted` over the identity client's `InvitationAccepted`, which
the auth's `New` sets where it builds the client), so the invitation is spent and its roles
given together, or neither.

The package carries a foothold (`foothold.go`) for an auth whose roles the application
assigns, with the password method or without: the first administrator of a deployed
environment comes in by an invitation, as everyone after them does. While nobody of the
auth holds the Super Administrator role, the migrate command has one mailed to the
address the placement names for the environment (`InviteAdministrator`, handed the setting `APP_<AUTH>_ADMINISTRATOR`, an
email address and no secret), giving the Super Administrator role and the roles
`FirstAdministratorRoles` names. The person opens the link and signs in by a method of
the auth: with the password method they choose a password there, their address proven by
the link, and through a directory the invitation is what ties the sign-in to the roles,
whoever else the directory admits and however many organizations the method is open to,
the address the directory gives being held to the invited one. No account is created
ahead of the person and no password is generated, so no secret exists that somebody
would have to read. Every release runs the step: an invitation that stands is left as it
is, one that ran out (seven days) is replaced, and one for an address the placement no
longer names, or for roles the release no longer names, is withdrawn, so its link no
longer opens; once somebody holds the Super Administrator role nobody is invited, and
neither an address nor a mailbox is asked for. The role is what is asked, read from the
auth's own permission engine (the foothold hands the session library its user manager):
an account that holds no such role (a person who signed up, one a directory seated, a
persona) stops no invitation, and a holder whose account is disabled is a holder still.
`ReinviteAdministrator`, which the migrate command's
`-reinvite-administrator <auth>` runs on demand from the deployment's operations,
withdraws the standing invitation and mails a fresh one, for mail that was lost, and is
refused once somebody holds the Super Administrator role. Both refuse while nobody holds
it and the application has no mailbox to
send as (the data level's log sender, `mail.Delivers`): an invitation's link is never
written to a log, since whoever opens it becomes the administrator, so the mailbox comes
before the first release. The command writes what it did, the address and when the
invitation runs out, and never the link.

The command writes the invitation outside the package too, as `impulse new` writes it
for the auth an application starts with: the address on the data level
(`APP_<AUTH>_ADMINISTRATOR`, a field with no secret tag in the struct the level reads
its environment into, behind a `<Auth>Administrator()` accessor beside the auth's own),
the variable in the environment template, unset, and the auth among those `pkg/deploy`
invites (`AdministratorAuths`, `administratorOf`), so every release invites its first
administrator after the others' and `-reinvite-administrator` takes its name. An
application started with `--shape directory-roles` holds none of that, so there the
command writes it whole for the new auth: `pkg/deploy/administrators.go` with the test
of what it reports, the migrate command's invitation after its role check and the
`-reinvite-administrator` flag. It is all written or none of it: where the data level,
the deployment package or the migrate command is not as the tool knows it, nothing that
invites is written outside the package, and the change says which file stopped it and
what to write by hand. Two things stay the agent's, and the change names both: the
foothold's roles (`AdministratorRoles`, `FirstAdministratorRoles`), which go with the
role file, and the migrate command's test (`cmd/deployment/migrate/main_test.go`), whose
cases that run a release now need the address or a holder of the role. `impulse check`
holds the package to inviting (`first-administrator`). An auth the application generates no management for invites
nobody, since nothing there would give an accepted invitation its roles; the foothold
says so, and `impulse check` refuses the auth as it stands (`first-administrator`),
since it has no way in: the application generates the management
(`generation.GenerateIdentity`), builds the auth's identity client and invites. Local
development is as it was: the bootstrap creates the development logins, one of them
holding the Super Administrator role, so a migrate run against a bootstrapped database
invites nobody for that auth.

`--method` names the methods the Auth starts with, repeatable: `password` (the default
when none is given), `azure:<registration>`, `google:<registration>`,
`oidc:<registration>` (one organization's identity provider, section add method; its
registration name has no default) or `persona` (the persona sign-in of the test
environments, section add method), the registration
being the segment of the method's routes (`user/<registration>/login`). A directory method
added here takes one organization; `add method` composes the other slots. No method is
given an account rule: every method starts invite only, a person signs in once they are
invited or created, and an administrator opens a method to sign-up, or makes it invite
only again, on the auth's Sign-in page in the running application. A password method's
sign-up routes are always on the router and answer not found while the method is invite
only; once it is open a stranger gives their address on the login page, follows the link
mailed to it and chooses a password. `--accounts`, which wrote the rule into code, is
refused with these words. `--username-is-email` makes the password method's sign-in name the person's email
address (`session.UsernameIsEmail()` and `.UsernameIsEmail()`): the generated management
derives the address from the username, and a verified change of address renames the user,
whose roles stay where they are (the permission store names a person by the id of their
account).
`--authority` says who owns role
membership for a directory method and is asked when not given, never defaulted, because
the wrong answer deletes hand-assigned roles at the next sign-in: `directory` writes
`RoleSync` (the directory's roles are reconciled at every sign-in and roles they do not
name are removed; the bootstrap seeds no roles) and `.SyncsRoles()` on the method's
declaration, so the generated management shows everyone's roles as the directory's
and never writes them (a role removed by hand there would come back at the next sign-in).
A method under the `directory` authority stands alone on its auth: `add auth` refuses it
beside another `--method`, `add method` refuses it on an auth that registers a method
already and refuses any method on an auth whose registration writes the roles, and
`check` names the mix, since the directory is then the authority for everyone the auth
holds and a second way in would be a second authority over the same people (a person it
provisions holds nothing, and the generated management refuses to give them a role because
the directory will not); two populations with two authorities are two auths;
`application` writes `DisableRoleSync` (roles are assigned in the application). `--outlet`
names the outlet the new auth serves
and is asked when not given, listing the application's outlets: `default` for the one
`GenerateRoutes` declares (the console's), or a `WithRouterOutlet` name such as `portal`;
in the sites layout the site's name for its default outlet. The outlet must exist
(`add outlet` adds one, bound to an auth the application has, and `add auth` moves it to
the new auth), and a machine outlet is refused, since it has no sign-in.
There is no default, because an auth is reachable only through the outlet bound to it.
Under a hand-written router the program declares no Auth to point, and the brief says to
mount the auth's session group in the router. In a tenanted application the new auth's
`New` sets the sign-in rule over the tenant record's declaration, as the staff auth's does
(`signin.TenantPolicy` through the reader its settings carry, `TenantDeclarations`, which
the settings copied from the source auth pass in); the auth whose session outlets serve
the tenant record, and whose roles the application assigns, has the rule admit a person
who holds no tenant yet (`signin.AdmitWithoutTenant`), since a person who signs up through
it creates a tenant of their own. That auth is the one a tenant is made through, so its
`Settings` carry `TenantMaker` and its `New` passes it to the identity client; the
construction copied from the source auth carries the maker only where the source is that
auth too, an auth a tenant is not made through leaves it behind, and an auth that takes
the default outlet over is handed none, which the brief records as the agent's.

`--shape <name>` starts the auth from a sign-in shape by name (section impulse audit
lists the eight) and fills in each method's choices. `add auth` and `add method` take the
four a sign-in method's choices tell apart:

| `--shape` | What it fills in |
| --- | --- |
| `invite-only` | 1 Invite only: the application gives the roles (`--authority application`), and every method stays invite only. |
| `directory-members` | 2 Directory members: a directory method limited to the organizations it names, with the application giving the roles, which an administrator opens so that everyone in the organization's directory gets an account. A password method beside it is left invite only; the shape is refused with the password method alone. |
| `directory-roles` | 3 Directory roles: one directory method with one organization whose directory gives the roles (`--authority directory`), alone in its auth. |
| `open-sign-up` | 4 Open sign-up: methods anyone can use, with the application giving the roles, which an administrator opens: the password method as it is, a Microsoft method with any tenant and personal accounts, a Google method with any Google account. |

The shape settles `--authority`, so it is not asked. Open is never written, since it is
chosen while the application runs: for `directory-members` and `open-sign-up` the command
says that an administrator opens the method on the auth's Sign-in page, and what opening
does on this auth (a new person gets the role chosen there, or creates a tenant of their
own, or the auth's methods are never opened because a person who signed up to it could be
given no tenant). What the shape filled in is printed after the change as
`Starting point:`, and `impulse audit` reads it back from the code. A method or a flag
that contradicts the shape is refused, naming both and saying what each means (`--shape
directory-roles --authority application`, `--shape open-sign-up` with an identity
provider's method, `--shape directory-roles` with two methods), and a name the command
does not know is refused with the names it knows; the four shapes of an application with
tenants are `impulse new`'s and `add tenancy`'s. A starting point that leaves an auth
with no password method and a directory method open to more than one organization is
one like any other: its first administrator is invited, and the invitation is what ties
the person's first sign-in through the directory to the roles.

```sh
impulse add outlet partners --prefix partners/api --auth staff
impulse add auth partners --outlet partners --agent
impulse add auth members --method azure:directory --authority application --outlet portal --agent
impulse add auth members --method google:workspace --authority directory --outlet portal
impulse add auth members --method google:google --authority application --outlet portal
impulse add auth clients --method password --username-is-email --outlet portal
impulse add auth employees --method oidc:company-sso --authority directory --outlet portal
impulse add auth members --method google:workspace --shape directory-roles --outlet portal
impulse add auth visitors --method password --method google:google --shape open-sign-up --outlet portal
```

`add method` adds a sign-in method to an auth the application has: `password`, `azure`
(Microsoft Entra over OpenID Connect), `google` (Google Workspace over OpenID Connect),
`oidc` (one organization's identity provider over OpenID Connect, below) or
`persona` (the persona sign-in of the test environments, below), under the registration
name given (the kind's name when none is given; an identity provider always gives one,
and the password method is always `password`). The method is a file of its own in the auth's package,
`pkg/auth/<auth>/<registration>.go`, holding its registration constant, its settings, its
handlers and the `register<Registration>` call New makes before the first request; the
Auth file's `Handlers` and `Settings` gain it, its package comment lists the registration,
and where that comment's first sentence says how the auth's people sign in (the base's
says "with a password") the new way joins it, so the sentence stays true of the methods
the auth has; the data level reads a directory method's
settings from the environment under the registration's variables,
`APP_<AUTH>_<REGISTRATION>_TENANT_ID`, `_ADMINISTERED_TENANTS`, `_CLIENT_ID`,
`_CLIENT_SECRET` and `_REDIRECT_URL` for azure (`_TENANTS` for the listed slot; an open
slot declares no `_TENANT_ID`, and personal accounts alone no `_ADMINISTERED_TENANTS`
either), `_CLIENT_ID`, `_CLIENT_SECRET`, `_REDIRECT_URL`, `_HOSTED_DOMAIN` and
`_ADMINISTERED_WORKSPACES` for google (`_WORKSPACES` for the listed slot, `_GROUP_PREFIX`
and `_GROUP_LOOKUP` under the directory authority), `_ISSUER`, `_CLIENT_ID`,
`_CLIENT_SECRET` and `_REDIRECT_URL` for oidc; the administered setting names the
organizations the deployment administers among those the slot admits, by tenant id or
domain, whose sign-ins carry the directory's second factor and are not asked for the
sign-in code (empty where it administers none); the secret's field
carrying `secret:"true"` beside its env tag (the marker bedrock reads to serve a variable
from Secret Manager; `secret:"generated"` marks one whose value bedrock makes and nobody
types, and `secret:"versioned"` a generated one kept in versions, every enabled one of
which the service is handed in a variable of its own: the application secret that seals
stored secret fields, `APP_SECRET_KEY_1`, `APP_SECRET_KEY_2`), and passes them in the auth's settings function (`<auth>Settings` on the
environment struct, which grows by the registration while `NewDataConfiguration` stays
one call per auth; in an application composed before the function, the `Settings`
literal in `NewDataConfiguration`, which the did line says); every outlet bound to the
auth declares it (`generation.AzureMethod("directory")` on the outlet's `Auth` option,
with `.SyncsRoles()` chained where the method file's authority is the directory, so the
generated management reads the same authority the registration enforces), so the
generated router mounts its routes and the login
page, which renders from the methods route, offers it; and for a directory method the
Procfile builds with the session library's `skipAuth` tag, which simulates the directory
until the application is registered with it (every sign-in through the registration is
`APP_<REGISTRATION>_USERNAME`, or `APP_USERNAME` when that is unset, with the roles of
`APP_<REGISTRATION>_ROLES` or `APP_ROLES`), and the environment template gains the
registration's variables.

`--slot` is the organization slot of a directory method, which organizations' accounts it
admits. For azure: `one` (one tenant, the default), `listed` (named tenants), `any` (any
tenant), `personal` (Microsoft personal accounts alone), `one-personal` (one tenant and
personal accounts), `any-personal` (any tenant and personal accounts). For google: `one`
(one Workspace, the default), `listed` (named Workspaces), `any-workspace` (any
Workspace), `any` (any Google account, with any Workspace named beside it, so a Workspace
account's link carries its domain as its connection, what a tenant's declaration names a
Google directory by), `mixed` (one Workspace and any Google account). The method file builds the slot in a
function of its own, `<registration>Slot`, where the organizations the deployment
administers are declared attesting; for a Microsoft registration that function is the one
place bedrock reads the tenant shape from, for the app registration's supported account
type in the stack's hand steps, and a registration that builds its tenants any other way is
refused by `bedrock check`.
Who a method admits at a first sign-in is not written into code. Under the application
authority every method is invite only: a user created beforehand or an invitation is
linked, anyone else is refused and nothing is written. An administrator opens a method to
sign-up on the auth's Sign-in page in the running application, where the server says how
wide the method is (the slot: one organization's accounts, any Google account, an email
address for the password method) and what a new person gets, and makes it invite only
again there. The choice holds for the width it was made for: a release that changes whom
a method admits (another organization in its slot, a wider account kind) leaves the method
invite only until an administrator opens it again, reading the new width in the warning.
A registration given an account rule of its own in code (`signin.AccountRule`) is listed on
the page as decided by the application, with nothing to choose. Under the directory authority the directory decides: a person it gives a role
signs in, and nobody else. `--accounts`, which declared the rule, is refused with where the
choice is made now.
`--username-is-email` makes the password method's sign-in name the person's email address
(`session.UsernameIsEmail()`, `.UsernameIsEmail()` on the declaration): the generated
management derives the address from the username, and a verified change of address renames
the user, whose roles stay where they are (the permission store names a person by the id of
their account). `--authority` is as for `add auth`; the directory authority is allowed with one organization only, and
only on an auth that registers no other method, as no method is added beside a
registration that writes the roles: the command names the two ways out, an auth of its
own or the application authority.

`--label "<words>"` gives the words people read for an `oidc` method's provider: the login
page's button says "Sign in with" and those words, and the generated lists of a person's
ways to sign in name the method by them. They are written on the method's declaration in
the generator program (`generation.OIDCMethod("company-sso").Labeled("Lakeside ID")`),
which is where they are changed, with `go generate ./...` after; the handoff names the
file. Without the flag the words are made from the registration name (`company-sso` reads
"Company Sso"), and the handoff says so and names the same file. The flag is the `oidc`
method's alone, since its words are the only ones made from a registration name: every
other kind is shown by what it is (Password, Persona) or by its brand (Google,
Microsoft), and the command refuses the flag there, naming `.Labeled` on the declaration
for an application that wants other words. The registration name stays the key in the
routes, the variables and the stored links, whatever the words are.

`--shape <name>` is as for `add auth` and fills in the choices the flags left open:
`invite-only` sets the application authority and takes any slot; `directory-members`
takes a slot limited to the organizations it names (`one`, the default, or `listed`, or an
identity provider's method) with the application authority; `directory-roles` sets the
directory authority with one organization; `open-sign-up` takes the password method as it
is, azure with `--slot any-personal` unless another slot wider than the named
organizations is given, and google with `--slot any`. A method or a flag that contradicts
the shape is refused, naming both (`--shape directory-members --slot any`, `--shape
invite-only --authority directory`, `--shape directory-members` with the password
method).

`oidc` is one organization's identity provider: a provider with nothing vendor-specific
about it, which the session library knows by its issuer alone (Okta, Keycloak, an identity
gateway). The method is the session library's generic OpenID Connect method
(`oidc.SignIn`), the application a client the provider gave a secret. The provider is named
in the registration's settings (`APP_<AUTH>_<REGISTRATION>_ISSUER`, `_CLIENT_ID`,
`_CLIENT_SECRET`, `_REDIRECT_URL`) and registered in the method file, so it is the same
provider for everyone the auth holds and it changes with a release. The registration name
is required: it is the application's word for the provider (`okta`, `company-sso`), the
segment of the method's routes, the connection every identity link carries and, with each
word capitalized, the label of its button on the login page (`.Labeled(text)` on the
declaration says another). The method takes no `--slot`, since the issuer names the one
organization. A person signs in under the name the provider presents, the token's
`preferred_username` or else its `email`; under the application authority the method is
invite only until an administrator opens it to the provider's organization.
`--authority` is as for a
directory: under `directory` every sign-in reconciles the person's roles to the ID
token's `roles` claim, a list of role names at the top level of the token, and removes any
role the claim does not name, and a sign-in whose claim names no role the role file
defines is refused. The claim's name is fixed, so the provider is set up to send it for
this client (a custom claim on Okta, a mapper on Keycloak, whose own `realm_access.roles`
is nested and is not read). Under `application` the claim is not read. In a tenanted
application whose tenants each have a directory their people enter through, a sign-in
through the provider is a directory's to the sign-in policy, method `oidc` under the
registration's name as the connection: a tenant's administrator who is signed in through
it is offered to make it the tenant's directory on the console's Sign-in page, and a
tenant that is directory only then admits the people who sign in through it and nobody
else, while a tenant that is not admits everyone it seats, however they signed in. The
directories record `add tenancy` composes admits `google`, `azure`, `workos` and `oidc`
in its method column, and the Sign-in page names the method's directory
(`directoryKinds`, "OpenID Connect provider"); a table composed with fewer methods in
the column's check needs a migration that adds `oidc`, which the command says. A sign-in whose
token names `mfa` in its `amr` claim carries the second factor; any other is asked for the
sign-in code the Auth mails, where the sign-in policy requires a second factor. The
declaration is `generation.OIDCMethod("<registration>")`, with `.SyncsRoles()` chained
where the method file's authority is the directory. `check` tells this method from the
persona method, which registers with the same call, by the configuration: one that sets
`ClientSecret` is an identity provider, one that sets none is the persona method. Under
`skipAuth` the provider is simulated like a directory; the registration validates its
issuer in every build, so the environment template sets a well-formed placeholder
(`https://provider.example.com`) and the command names the same value for the test
harness.

`persona` is the sign-in of the test environments: a reviewer, a tester or an agent opens
the application in a pull-request environment or the first environment, clicks one button
on its login page, picks a persona from the deployment's cast at the persona provider
(the hosting platform's, which signs the person in itself and decides whether they may
enter the application's test environments), and is signed in as that persona with its
roles; the
application's own methods stay on the page. The method is the session library's generic
OpenID Connect method (`oidc.SignIn`), the application a public client (no secret, PKCE),
the application the authority for role membership (`--authority` defaults to
`application` and takes nothing else) and the method invite only, as every method starts,
with seeded users declared (`SeededUsers: true`): the provider says which
persona signed in, and the persona's identity (its login as the subject) is linked to the
account seeded under that login, on the login alone, since the declaration takes the
provider's names for the application's own. A seeded account has no password and has
never signed in, so without the declaration an application whose tenants have
directories links it only through the directory of the one tenant it belongs to, or
with its own invitation, and a persona holds its roles in every tenant or in none. The method takes no `--slot`. Its settings are
`APP_<AUTH>_<REGISTRATION>_ISSUER`, `_CLIENT_ID` and `_REDIRECT_URL`, and
`register<Registration>` answers nil when the issuer is empty, so the method is registered
only where the provider is configured: bedrock sets the issuer in the first environment and
its pull requests from the placement's `persona` block (`{"issuer": ..., "clientId": ...}`,
both from the provider's registration of the application), derives the client id (the root with
the pull request or the environment appended) and the callback, and registers each
deployment, its callbacks and its cast with the provider after traffic; a later
environment carries no issuer, so it never offers the method and nothing in the build
differs. The generated router mounts the method's routes and the methods route offers it
only where the handlers are non-nil (`generation.PersonaMethod("<registration>")`
on the declaration). Beside the method file the command writes the cast,
`pkg/auth/<auth>/cast.json` (the personas: `login`, `name`, `email`, `roles` from the
auth's role file or the framework's `SuperAdministrator`), empty to fill in, and `cast.go`, which embeds it (`Cast()`) and seeds
its accounts with their roles (`SeedCast`; `SeedCastFile` is the same seed over a cast file a suite hands it); the did lines say
to call `data.<Auth>().SeedCast(ctx, out)` in the migrate command under `-seed`, once the data
level is open and before `InviteAdministrators`: the first administrator is invited
while nobody of the auth holds the Super Administrator role, so an environment whose
cast gives a persona that role invites nobody and needs no mailbox, and one whose cast
gives it to nobody invites and needs the address and the mailbox. A persona who holds no
such role stops no invitation. A persona seeded earlier is given the roles its entry names
that it does not hold, each said on a line of the migrate command's output, and nothing is
taken: a role that left the cast stays on the account.
`impulse check` refuses a function that seeds a cast after it invites that cast's auth
(`first-administrator`). The `persona-cast` check holds the cast to its shape and its roles to
the role file. Under `skipAuth` the provider is simulated like a directory.

Under the `directory` authority with google, the directory's groups assign roles by name:
a group named `<prefix><role>@<domain>` assigns `<role>`, read through Google's Cloud
Identity Groups API with the signing-in person's own access token (one extra OAuth scope,
the groups read-only scope, and the Cloud Identity API enabled in the Google Cloud project
that owns the OAuth client). `_GROUP_LOOKUP` is how far the lookup reaches: `direct` (the
default when unset) reads the groups the person is a direct member of; `nested` climbs
from those to the groups they are in. A group whose member list the person may not view is
left out, which is not an error. Under `skipAuth` the lookup is simulated: every sign-in
is in the groups the roles variable names.

```sh
impulse add method members azure directory --authority application --agent
impulse add method members google workspace --slot listed --authority application
impulse add method partners password
impulse add method clients password --username-is-email
impulse add method staff oidc okta --authority application
impulse add method staff oidc company-sso --authority application --label "Lakeside ID"
impulse add method staff persona
impulse add method staff google workspace --shape directory-members
impulse add method staff azure entra --shape open-sign-up
```

### add site

`add site <name>` adds a site: a stand-alone application on a host of its own under
`apps/<name>/`, with its own main package, handlers, router, resources (empty to start),
authorization suite, and browser workspace, copied from the first site with the imports
renamed (its brand declaration with it, `brand.json`, which the report says and the brief
names giving a brand of its own);
its generator program and directive; its serve and browser processes on the next
ports; its TypeScript target in the shared generator; and its router collection in the union
the roles are reconciled against. The name is one word of lowercase letters and digits
starting with a letter, at most 25 characters, and a name outside the rule is refused
(`--existing`'s too): it is the second segment of every per-site resource name bedrock
renders and of the site's default hostname label (`<code>-<site>`), and the tightest of
those names, the Cloud Run service's `<prefix>-<env>-<region code>-<code>-<site>-app`,
fits Cloud Run's 49 characters with 25 left for the site at the organization's longest
prefix, environment, region code and application code. On a flat application the first
site added promotes the layout to sites, the one non-additive transition: the existing
site moves under `apps/<existing>/` and every import of its packages follows, its generator becomes
`cmd/generate/<existing>generator`, the site level's bundle variable becomes `APP_DIST` (set
per site process, inline in the Procfile, as `PORT` is), the deployment's collection
becomes the union of the sites' router collections (`access.UnionCollection`, which
refuses sites that declare a shared resource differently), and a shared generator is laid
in over an empty `pkg/sharedresources`. The new site's router package is generated code
alone, so the copy lays nothing in it: the site's generate program runs first, from
`cmd/generate` as its directive runs it, and the package exists before `go mod tidy` reads
the site's imports of it; the regeneration then runs every program. The CI workflow is
rewritten from the code, since impulse owns it: an `angular-<name>` job builds, lints and
tests the new site's browser workspace, and on a promotion the existing site's job becomes
`angular-<existing>`.
`--existing` names what the existing site becomes and is asked when not given, never
defaulted, since the name is the site's directory for good. Everything existing belongs to
that site, the generated management of the auths' people included: the tree the existing
site's program writes it into (`GenerateIdentity`) moves under `apps/<existing>/` with the
site, and the new site is laid in without it, since an auth's package builds its identity
client from one tree and so one program generates an auth's management. The new site's
program carries no `GenerateIdentity`, its copied packages leave out what names the tree
(the accessor on the Configurer and the App, the client the authorization harness builds),
and its routes ask the library for none of the pages (`identityRoutes`); a call of the
accessor in code that was kept is named for the agent. People are managed on the existing
site. A site bound to an auth of its own takes that auth's management: `impulse add auth
<name> --outlet <site>` adds `GenerateIdentity` with a tree under the site to a program
that generates none, where the new auth is the only auth the program declares, and names
the routes' request for the pages as the agent's. The new site's resources, its place in the integration suite, its browser
application's own titles and pages, and the deployment configuration outside the repository
(build path filters, source directories, a host) are the agent's; the reference is the
`sites` skeleton.

```sh
impulse add site portal --existing console   # promotes a flat application, then adds portal
impulse add site kiosk                    # a third site, copied from the first
```

### add feature

`add feature <name>` declares a feature flag. The resources package gains a
`resource.Feature` constant named after the flag (`cargo_manifest` becomes
`CargoManifest`) with a doc stub, in `features.go` (started when the package has none);
the development seed gains the flag's row, off, in
`schema/devseed/<n>_dev_feature_flags.up.sql` (started at the next data migration number
when the seed has none), where setting `Enabled` to `TRUE` starts development and the
test environments with the feature on, since the deploy's `MigrateFeatures` keeps
`Enabled` where a row exists and production has no seed; and `go generate` runs, so the
generated `Features()` lists the flag for the deploy and the browser's `Feature` union
carries it. The name is 1 to 64 characters of `[a-z0-9_]` opening with a letter, and a
name already declared is refused. In the sites layout `--site` names the site whose
resources package declares the flag, since each site's generator resolves `@feature`
against its own package.

The check then fails on `feature-flags`, because the new flag gates nothing and is read
nowhere, and the brief hands the agent what is left: what to gate (`@feature(<Constant>)`
on a resource, a field or a method, or a read in Go or in the browser), the description to
write in place of the stub, how a flag is flipped (the generated `SetFeature` method
under the `FeatureAdministrator` role, which the development login holds), and that the
menu file places the library's Feature flags page (`featureFlagsPage(resourceMeta)`,
shown under List on `FeatureFlags`), which lists each flag with who last turned it on or
off, flips one after asking, and opens a flag's history in a pop-up.

```sh
impulse add feature cargo_manifest --agent
impulse add feature debriefs --site console
```

### add files

`add files` wires the framework's file store (`resource/filestore`) into a flat
application, the whole of it, so the check is clean when it ends. The data level gains
`FileStoreSettings` and `LoadFileStoreSettings` in `pkg/config/files.go`, reads
`APP_FILE_STORE` into its environment struct, opens the store the variable names before
the database driver opens (`openFileStore`; unset leaves the store closed, so the migrate
and bootstrap commands run without one), hands the driver the store's options
(`database.Open(ctx, env.Database.Settings, fileStoreOptions(files)...)`, which is
`resource.WithFileStore` on the resource client the driver builds) and releases it in
`Close`. `.envrc.template` sets
`APP_FILE_STORE=file://uploads` in the data block and `.gitignore` ignores `uploads/`;
`cmd/bootstrap` gains `emptyFileStore`, called before the development seed, which empties
a `file://` store since no row holds a file then. The cleanup is a job of the job
process's scaffold (section add job): `pkg/jobs/cleanup.go` declares the command
(`CleanupCommand`, `cleanup-files`) and the work (`CleanupFiles`, `filestore.Cleanup`
over the default store through the generated `FileHolders()`),
`cmd/jobs/cleanupfiles` is the cobra command running it with `--window` and `--dry-run`,
and `cmd/jobs/main.go`, the root that opens the data level once and registers every
command, is written when the application has none and extended when it has one. The rpc
package gains `CleanUpFiles`, a method marked `@rpc` and `@schedule("0 9 * * *")` whose
`Execute` starts the job process on the cleanup command through the client's `Jobs()`;
an application without an rpc package gains `pkg/rpc` with a `Client` carrying the
starter, and `WithRPC("pkg/rpc")` in the generator program. The site level builds the scheduler guard (`scheduled.FromEnvironment`, from
`APP_SCHEDULER_INVOKER`) and opens the job driver (`cloudrun.Open`, over the
`cloudrun.Settings` its environment struct embeds, which declare `APP_JOBS_TEMPLATE`, and
the version the image bakes in), exposes them as `Scheduler()` and `Jobs()`, and closes
the driver with the level; `.envrc.template` documents `APP_JOBS_TEMPLATE` in the site
block, unset. The `Configurer` asks for both, the `App` carries the guard and the RPC
client built over the starter, and `app/scheduled.go` declares `SchedulerAuth` (the
middleware the generated router mounts the scheduled routes behind) and `RPCClient`. Every test configurer (a type in a test file declaring
`LogExporter`) gains a nil guard, `jobs.NewFake()`, and a memory store its resource client
is built over (`files *filestore.Mem`, passed as `resource.WithFileStore`), so a file route
the application declares later is served in the suites. What the application wired already
(a scheduled method of its own brought the guard, the accessor, the middleware) is left as
it is: the editors add nothing the code declares, and the report says what was there. Then
`go generate` emits the method's handler, its route under `/_scheduled` and the router's
requirement.

An application already holding an rpc package keeps its `Client`: the method is written
into the package, and giving the client a `Jobs()` accessor fed from the configuration is
the agent's unless the client declares one already; a `cmd/jobs/main.go` without a
`root.AddCommand` call in `run` leaves the cleanup's registration to the agent too. The
image is bedrock's: the Dockerfile it seeds builds `/jobs` beside the other binaries
once the job process exists. Which resources record
files (`@file` on a `resource.Key` column, an `@upload` method) is the application's;
until one does, the store is wired and idle. The `file-store` check watches the variable
from then on, and the stack reads it to make the bucket (`gs://<bucket>` on Cloud Run),
deploy the job process beside the service and schedule the method.

```sh
impulse add files
impulse new ./harbor --module example.com/harbor --auth staff --files
```

### add job

`add job <name>` scaffolds a command of the application's job process, the second binary
(`cmd/jobs`) bedrock deploys as the Cloud Run job beside the service, so adding a job is
the work alone. The job process is a cobra command tree: `cmd/jobs/main.go` is the root,
which opens the data level once (`config.NewDataConfiguration`) and registers every
command over it, written by the first job and extended by each one after (the
`root.AddCommand` call in `run` gains a line); a command is a package under `cmd/jobs`
named for the job (`prune-reports` becomes `cmd/jobs/prunereports`) whose `Command(data)`
builds the cobra command and whose `Run` runs the job's work over the resource client;
and the work is a function in `pkg/jobs` named for the job (`PruneReports`, in
`prune_reports.go`), written empty, which refuses until it is written, beside the
constant the job process runs it under (`PruneReportsCommand`, `"prune-reports"`). A
name carrying a group (`reports/materialize`) puts the command under a group command of
that word (`cmd/jobs/reports`, `jobs reports materialize`), written with the group's
first job, with the group's name in `pkg/jobs` (`ReportsGroup`). The lint configuration's
depguard list admits the command tree's library (`github.com/spf13/cobra`) and `go mod
tidy` makes it a direct requirement.

`--schedule "<cron>"` also writes the method marked `@rpc` and `@schedule` in the rpc
package (`PruneReports`, answering `PruneReportsStarted`) whose `Execute` starts the job
through the client's `Jobs()`, since a job is started by the running service and never
by a schedule of its own; where the application lacks the starter's wiring the job
brings it, exactly as `add files` wires it for the cleanup (the rpc package and its
`Client`, the site level's scheduler guard and job driver, `SchedulerAuth` and
`RPCClient` on the `App`, the test configurers' fakes, `WithRPC` in the generator program,
`APP_JOBS_TEMPLATE` documented in the template), and what is wired already is found in
place; then `go generate` emits the method's handler and its route under `/_scheduled`.
The schedule is read in UTC; another zone is `zone:` on the annotation. Without a
schedule nothing starts the job until a scheduled method or a caller does.

The work, its suite (a test in `test/integration` driving the function over the test
database, as the cleanup's does), and the schedule when none was given are handed to
the agent; the brief lists them even when the check is clean, since no check sees an
empty job.

```sh
impulse add job prune-reports --schedule "0 3 * * *" --agent
impulse add job reports/materialize
```

## impulse remove

`remove` takes an option out the way `add` puts one in: the deterministic half is made
(the option's files deleted, the generator program and the registrations edited, `go
generate` run), staged, and checked, and what still names the option in the hand-written
code is handed to an agent with the same brief, the rendered reference showing what the
option's wiring looks like so it is recognizable on the way out.

`remove outlet <name>` removes a router outlet from every site that declares it. The
generator program loses `WithRouterOutlet` (with the comment lines that introduced it) and,
for a session outlet, the `GenerateTypescript` target for the outlet; the outlet's browser
project is deleted and taken out of `angular.json`, the package scripts (`start:<name>`,
`build:<name>`, `lint:<name>`, `test:<name>`, and its part of `build`, `lint`, and `test`),
and the Procfile; and
an `@outlet` list that names the outlet beside others drops it, so those structs keep
their other outlets. A struct on the outlet alone keeps its annotation, which fails
generation, because whether it moves to the default outlet (the console's people reach it
then) or leaves the application with its table is a decision about who may reach it; the
brief lists each. Regeneration drops the outlet's group from the generated router along
with its `Hooks` field, so a hook naming it fails to compile until removed; the App's
handlers for the outlet, its configuration and environment lines (the brief quotes them),
and its tests are the agent's (and, under a hand-written router, the group that mounted
it). The auth the outlet was bound to stays. There is no data consequence.

`remove site <name>` removes a site from an application in the sites layout: `apps/<name>/` is
deleted with its generator program and directive, its TypeScript target leaves the shared
generator, its router collection leaves the union the roles are reconciled against, its
processes leave the Procfile, and the CI workflow is rewritten from the code without the
site's browser job. The remaining sites stay where they are: an application
left with one site keeps the sites layout, with the site under `apps/`, the
shared generator emitting into it, and a union of one element; nothing moves back to the
root. The application's last site is not removed. The integration suite that served the
site (and any other file still importing its packages, which the brief lists), the
deployment configuration outside the repository, and the tables only the site's resources
declared (declare them in the site that serves them now, or drop them by migration) are
the agent's.

`remove feature <name>` retires a feature flag, the step that makes a feature permanent or
abandons it. The constant goes (and `features.go` with it when it declared nothing else,
or the resource import when nothing else reads it), every `@feature(<Constant>)`
annotation naming it goes (a line of its own whole, a shared line keeping its other
text), so the resources, fields and methods it gated are served unconditionally, the
flag's row leaves the development seed, and `go generate` runs, so `Features()` and the
browser's `Feature` union lose the name. The hand-written Go and TypeScript that still
read the flag are listed by file and line: each fails to compile until it changes, and the
remaining code runs unconditionally, so the on branch is inlined to make the feature
permanent or the feature's code is deleted to abandon it. That list is handed to the agent
in the brief even when the check is clean (a browser use fails the browser's own build,
not the check), and `--agent` launches it. Nothing in the database refuses the removal:
the next deploy's `MigrateFeatures` deletes the row, and the `FeatureFlagChanges` rows
stay as the record of every flip.

```sh
impulse remove outlet portal --agent
impulse remove site kiosk
impulse remove feature debriefs --agent
```

## Templates

The application skeletons `new` and `add` will render live under
`internal/skeleton/_candidates/`, one directory per shape (`solo`, `tenanted`, `outlets`,
`sites`), and are embedded in the binary, so a release ships exactly the trees it was
tested with and renders them offline.

While embedded they are not Go modules: each carries its `go.mod` as `go.mod.tmpl`, and
the leading underscore keeps the tree out of `./...` so nothing compiles it in place.
Rendering writes `go.mod` back under the target module path and rewrites every import.
A template's `go.mod` requires the framework at the placeholder `v0.0.0-skeleton`, and its
`package.json` files pin the browser packages at `0.0.0-skeleton`: the rendering fills both
with the version of the impulse that runs it (the zero pseudo-version with a `replace`
under `--dev-root`), so no version is written into a template and nobody moves one by
hand.
A template names its application by its own candidate name in these places only: the
workspace name in `package.json` and `bun.lock`, the service name and development
database ids in `.envrc.template`, the README heading, and, in display form, each browser
project's titles (the page title, the component title fields, the login card), the
`name` and `short_name` of its web app manifest (`public/manifest.webmanifest`, the
portal's ending in Portal), the `name` of its brand declaration (`brand.json`) with the
name constants of the server's brands package written from it (`pkg/brand/brand.go`), and
the auth packages' `Application` constant. Rendering puts the application's name there; everywhere
else the prose says "the application", since the candidate names are English words.
The templates are therefore validated by rendering them and running the rendered
application's build, tests, and `impulse check`, never in place, and under a module path
outside `github.com/impulseframework/impulse/cmd/impulse` (the repository's CI renders them as
`example.com/ci/<candidate>`): the placeholder path lies inside impulse's module, which the
`tool` directive puts in the application's module graph, so under the placeholder path
every package of the rendered candidate would be an ambiguous import. Their browser workspaces
use bun, with `bun.lock` committed. Two settings travel with them: an `overrides` entry in
package.json that points @impulseframework/resource-angular's peer dependency on the client at
the spec the dependencies name (the yalc link while a framework checkout is attached), and
`peer = false` in bunfig.toml, because bun would otherwise install a second copy of the client
beneath the Angular library and TypeScript would see two declarations of every client type.
Every peer an application needs is therefore a direct dependency. Build products and
local state (`node_modules`, `.angular`, `dist`, `.impulse-cache`, `.yalc`, a stray
`go.work`) are never embedded; `internal/skeleton`'s tests enforce that.

## Deploy hooks

An application's own work at a fixed point of a deploy (a check against a dependency after
the migrations, a check against the new revision before traffic moves to it, a smoke test
after) is a hook. A hook has no database and never starts the application's job process:
work on the data is the running service's, which starts the job of its own build.
The package `github.com/impulseframework/impulse/deployhook` is the contract for writing hooks
in Go: a program at `cmd/deployment/hooks`, beside the migrate command, passes one
`deployhook.Hooks` literal to `deployhook.Main`, with a function for each stage it
implements:

```go
func main() {
	deployhook.Main(deployhook.Hooks{
		BeforeTraffic: checkNextRevision,
		AfterTraffic:  smokeTest,
	})
}
```

Each function receives the build's facts as typed values (`deployhook.Facts`: the
application, the environment, the release, the image and its digest, the pull request,
the next revision's URL) and answers an error to stop the build at its stage. bedrock
reads the stages from the literal, renders a pipeline step for each and checks that the
Dockerfile builds the program into the image as `/hooks`; the pipeline takes it out of the
image it built and runs it on the build worker, where a hook script runs. The contract
holds only the four stages after the image build (`BeforeMigrate`, `AfterMigrate`,
`BeforeTraffic`, `AfterTraffic`), so a program cannot implement the two stages that come
before an image exists (`before-build`, `after-down`); those take a script,
`infrastructure/hooks/<stage>.sh`. A program and scripts mix, one or the other per stage.

## Development

The tool is a package of the framework module, so it is built, tested and linted with the
module, from the repository root:

```sh
go build ./... && go vet ./...
go test ./cmd/impulse/...
golangci-lint run ./...
golangci-lint run --build-tags dev ./...
```

The repository's CI lints the module under both tag sets, `""` and `dev`, with the
configuration at the repository root. Fixture applications for the tests live under `app/testdata/`. They are
synthetic: a lighthouse-keeping flat application (`flat`), a harbor application in the sites
layout (`sites`), and a generator program the tool cannot read (`badprogram`).
