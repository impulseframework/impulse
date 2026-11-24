# lodestar

The full-capability demonstration application for `github.com/impulseframework/impulse/resource`: the
operations console of a fictional frontier rescue and salvage service (distress calls and
salvage jobs, the crews who fly them, the hangars that keep the ships airworthy, and the
droids that report on both), and the client portal its customers use. Built from the
Lodestar design plan so that every annotation, every handler shape, and the whole ABAC
runtime is something a human can log into and watch working, and so that the state of a
mission is something you can watch move. It is the generator's committed regression
baseline and the product-shaped tour in one.

Round 3 (2026-09-09) is a from-scratch rebuild born from `impulse`: the layout, the
configuration levels, the two auth packages, the environment template, the bun workspace,
and the deploy split come from the tool; the plan's domain sits on top. The core level
opens the cloud driver (`cloud/gcp`) as the skeletons do, so where the process's logs and
spans go is built from the settings the level embeds and nothing in the application names
a logging client or a trace provider ([`cloud.driver`](pkg/config/config.go)). Every struct,
method, test, component, and persona that proves a framework capability says so in a
`Demonstrates:` paragraph, and [DEMONSTRATIONS.md](DEMONSTRATIONS.md) is the generated
table of what proves what. Everything here is synthetic. The crew personas' plaintext
passwords are committed deliberately: the application runs against a local Spanner
emulator by default, or against a private test instance ([below](#running-against-a-real-spanner-instance)),
and is never published.

## Running it

With [overmind](https://github.com/DarthSim/overmind), podman, and bun installed:

```
cp .envrc.template .envrc && direnv allow
(cd ../web && bun install && bun run build)   # the framework's browser packages, which web/ links from ../web/dist
(cd web && bun install)
overmind start
```

The environment file carries `APP_SECRET_KEY_1`, version 1 of the application secret, which
a ship's transponder key is encrypted with (a secret field, `conditions:"secret"` on
[Ship.TransponderKey](pkg/resources/ships.go): the column holds the key sealed, and nothing
serves it back). The template makes the value for the checkout: 32 random bytes, written
to `.app-secret-key.dev` the first time the file is sourced from Lodestar's root and read
from it from then on, with `.gitignore` keeping that file out of git, so no key is
written in a tracked file. The server refuses to start without a version, naming the
variable, so an `.envrc` copied before the lines existed takes them from the template; an
`.envrc` that holds a key of its own keeps it, since the secrets the development database
already holds open under no other. A deployment generates the
secret and hands the service every enabled version (`APP_SECRET_KEY_1`, `APP_SECRET_KEY_2`),
and the migrate command retires one (`-retire-secret APP_SECRET_KEY_1`) by writing every key
that names it again under the current version. Demonstrates: secret, secret.rotation.

That starts a fresh Spanner emulator and a fresh Firestore emulator (the live service: the
live pages' subscription record and change sets, [below](#live-pages), and the signals
document the instances notify each other through,
[further below](#one-channel-between-instances); Firestore needs no schema, so the
bootstrap leaves it alone), bootstraps the database (schema, the demo world, and the
cast's accounts, each created under the fixed id `cmd/bootstrap/users.json` gives it:
the personas, each with the name people read (Armorer Arlo, who signs in as `armorer`),
the clients the demo world's rows name, and the droid service account; the
roles need no step, since each auth's role file rides in
the binary and the bootstrap only checks the store against it; a database that already
holds data is refused unless the bootstrap runs with `-reset`, which empties the data and
seeds it again without touching the schema), serves the application on :8090, and runs
`ng serve` for both browser apps:
the crew console on :4300 (`/console/api` proxied) and the client portal on :4301
(`/portal/api` proxied). Browse http://127.0.0.1:4300/console/ and sign in as any persona
on the crew manifest, through "Sign in with Lodestar", the fleet's Google directory, as the navigator, or with
"Sign in with Google", any Google account, as the pioneer, who holds nothing and charts her own sector once the
governor has opened that method to sign-up (the console's Sign-in page, Open sign-up: every method starts invite only), or with
"Sign in with Persona", the persona provider of the test environments, as a member of the cast
(`pkg/auth/crew/cast.json`: `hq`, `warden`, `rookie`; in development the simulated provider signs
`APP_PERSONA_USERNAME` in); browse
http://127.0.0.1:4301/portal/ and sign in through the simulated directory as `client`.
Both Go processes build with the session library's `skipAuth` tag, which simulates every
Google directory from `.envrc`: the portal's from `APP_USERNAME` and `APP_ROLES`, the
console's from `APP_GOOGLE_USERNAME` (the registration's own setting; the crew's roles
are the application's, so it names no groups), so no tenant is contacted and the persona
quick-fill still works. `APP_DROIDS_API_KEY`
opens the droid channel.

The browser packages are this checkout's own: `web/package.json` links them from
`../web/dist`, so the applications always run on the framework's current source, and
`bun install` in `web/` needs that build first. After a change to a package, rebuild it,
restart the dev server, and reload the page:

    (cd ../web && bun run build)
    overmind restart console portal

The dev server does not watch `node_modules`, so the restart is what picks the new build up.
The three packages are bundled with the application code rather than prebundled by Vite (the
`prebundle` exclusion in `angular.json`), so a plain reload shows the new build in any
browser profile: nothing is held behind an immutable URL, and no cache needs clearing.

The served application also serves the built bundles (`bun run build` in `web/`): the
console at `/console/` and the portal at `/portal/`, the bundles' directories overridable
through `APP_CONSOLE_DIST` and `APP_PORTAL_DIST`. Neither application is mounted at `/`:
an installed browser application owns every URL under its start, so two applications on
one origin each sit under their own path, each with its API beneath it (`/console/api`,
`/portal/api`), and the generated router answers the root alone with a temporary redirect
to `/console/`; every other unmatched path is 404. Uploaded files land in two file
stores, each opened from one URL by `resource/filestore`: the default store, `APP_FILE_STORE`
(`file://uploads` in development, gitignored), holds the refit photos, and the Documents
store, `APP_FILE_STORE_DOCUMENTS` (`file://uploads-documents`), holds the mission
documents, declared by the `Documents` type in `pkg/resources` and wired on the resource
client in `pkg/config/data.go`. The upload frame streams each file to the store its
method names under a minted key, the transaction's commit claims it, a failure before
commit deletes it, the generated file route reads it back, and a deleted or replaced row
releases its object from the store its column names. The bootstrap empties both directory
stores before it seeds (`cmd/bootstrap/files.go`; a fresh database or a `-reset` holds no
row that names a file), and `go run -tags skipAuth ./cmd/jobs
cleanup-files` is the orphaned-file cleanup: the job process lists a store, reads every
key the rows hold through the generated `resources.FileHolders()`, and deletes the
UUID-named objects older than the window (two days by default, a day at least) that no
row holds, refusing a store with aged objects no row holds any key in; `--dry-run` lists
what would go. The job process is a cobra command tree (`cmd/jobs/main.go` opens the data level once and
registers each command's package, `cmd/jobs/cleanupfiles` the cleanup's), the shape
`impulse add job` scaffolds. On
Cloud Run both variables are buckets (`gs://<bucket>`), which bedrock builds and names.
To run the stores against a fake Cloud Storage in development instead, add to the
Procfile `gcs: podman run --rm -p 127.0.0.1:4443:4443 docker.io/fsouza/fake-gcs-server:1.52.2
-scheme http -port 4443 -public-host 127.0.0.1:4443 -external-url http://127.0.0.1:4443`,
export `STORAGE_EMULATOR_HOST=127.0.0.1:4443` beside `APP_FILE_STORE=gs://lodestar-files`
and `APP_FILE_STORE_DOCUMENTS=gs://lodestar-files-documents`, and create each bucket once
(`curl -X POST 'http://127.0.0.1:4443/storage/v1/b?project=lodestar' -d '{"name":"lodestar-files"}'`),
since the fake starts empty and the store refuses to start on a missing bucket.

## The installed apps

Both browser apps install as progressive web apps. A production build of the console and
of the portal carries a service worker (`ngsw-worker.js` and its manifest `ngsw.json`,
built from the one worker configuration at `web/ngsw-config.json`) and a web app manifest
(`web/console/public/manifest.webmanifest` and `web/portal/public/manifest.webmanifest`,
with the icons beside each under `public/icons/`), so a browser offers to install either
app as a window of its own: the console under `/console/` and the portal under `/portal/`,
each worker's scope its own mount, on one origin. The console's manifest names it
Lodestar and the portal's Lodestar Client Portal; each carries the brand's primary color
as its theme color and its page background as its background color. The dev server
registers no worker: the worker is on in a production build alone, and the component specs
never load the app config.

Each app's name, icons and colors come from one declaration, the `brand.json` beside its
logo (`web/console/brand.json` and `web/portal/brand.json`, each with its `logo.png`): the
name, a square logo, the primary color and the page's background. `impulse brand` from the
module root (with the web workspace's packages installed) writes the rest from it: the
manifest's names and colors, the entry document's title, theme color and icon links, the
favicon and the three icons, the name the frame shows, the first palette of
`src/theme.scss` (generated into `src/_theme-colors.scss`, and offered under the brand's
name where the app offers a choice), and for the server `pkg/brand`, the names and the
logos `main.go` hands the maintenance page by mount path, with the name the mail of each
app's auth says (`Application` in `pkg/auth/crew` and `pkg/auth/members`). To change any
of it, change the declaration and run the command again; none of what it writes is edited
by hand.

An open tab survives a release. The worker keeps the files of the build the tab loaded, so
a lazy chunk the tab asks for after a deploy is still found although the server no longer
has it, and the next page load is served from the newest build the worker holds, after
which the worker checks the server for a newer one. The server's cache headers come from
the resource package's `BrowserApp`, which `app/app.go`'s asset handlers delegate to: a
file whose name carries the build hash is immutable for a year, and every other file (the
entry document, `ngsw.json`, the worker script, the web manifest, the icons) is `no-cache`
with a strong ETag, revalidated on each use, so no cache between the browser and the
server can hand the worker a stale build.

When a new build is ready, the app says so. `provideAppUpdate()` from
`@impulseframework/resource-angular/ui-app-update`, provided in each app config beside
`provideServiceWorker`, raises one persistent notice, "A new version of this application
is ready.", with a Reload action in the alert area the shell renders; the app reloads only
when the person chooses. It checks for a new build hourly while the tab is visible and
once when the tab becomes visible again, and the worker itself checks after every page
load.

The worker's navigation patterns exclude `/api/**` relative to each mount, so a navigation
to `/console/api/...` or `/portal/api/...` always reaches the server: the portal's directory
login and its callback, and a stored-file link, are never answered with the cached entry
document. The worker caches no API response and no third-party file.

The kill switch: a build deployed without `ngsw.json` makes every installed worker
unregister itself and drop its caches on its next check, after which the page is served by
the network alone. Removing `"serviceWorker": "ngsw-config.json"` from a project's
production configuration in `web/angular.json` is how a build is made without it.

## The release each request carries

Every request from a console or portal build names the release the build was made from,
and the server refuses a release it no longer answers. The build stamps the release into
the bundle: `web/angular.json` defines the global `APP_VERSION` as `dev`, and the
workspace's `build` script redefines it from the `VERSION` environment variable
(`ng build console --define "APP_VERSION='${VERSION:-dev}'"`), which the image's browser
stage carries, so `VERSION=1.2.3 bun run build` names `1.2.3` and `bun run build` alone
names `dev`. Each app config provides it as `API_VERSION`, the generated client sends it as
`X-Api-Version` on every request, and `apiVersionInterceptor`, registered with
`provideHttpClient(withInterceptors([apiVersionInterceptor]), ...)`, adds it to the app's own
`HttpClient` calls, the login POST among them. A `dev` build sends no header.

The server's release is the configuration's `APP_VERSION` (`dev` in `.envrc`), which
`App.ServerVersion()` hands the generated router. The router checks every console and
portal API route: a request whose release lies between the outlet's oldest answered
release and the server's, inclusive, is answered; any other is refused with 412 and
`X-Api-Version: <server release>` before any handler runs or a body is read. A request with
no header is answered, a `dev` build or a `dev` server is never refused, and the session
routes (login, callback, `/user/session`) and the stored-file routes (the mission
document, the refit task photo, the expense manifest) are answered at any release, so a
login always works and a file link never fails on a version. Every checked response
carries `Vary: X-Api-Version`. A refused console reloads onto the current build by itself
while it is still starting, and once a page is on screen it shows the update notice with
Reload (the library's `AppUpdateService`, provided by `provideAppUpdate()` above). The
development stack runs `dev` on both sides, so the walkthrough cannot show the refusal;
`test/integration/apiversion_test.go` proves it over a served router built from a release.

The server names the environment it runs in the same way. `APP_ENVIRONMENT` in the
configuration (`development` in `.envrc`) is what `App.ServerEnvironment()` hands the
generated router, which puts it on every answer as `X-Environment`; the client keeps it
(`client.serverEnvironment`) and the About pop-up, the last entry of the Help menu, shows it
under the release and copies both for a problem report. Production leaves the variable
unset: no header, and no environment line in the pop-up.

The console outlet's oldest answered release is declared in `cmd/generate/generator.go`,
`generation.OldestAnswered("0.1.0")`: the first release, which is what an outlet without
the option answers from, written out so the place to raise it is on record. The portal
outlet declares none. A developer raises it when a release stops answering something an
older console sent: the release that removed a field, or that dropped a `@formerly`
annotation, becomes the oldest answered, and consoles built before it are told to
reload. `generation.OldestAnswered(generation.ThisRelease)` is for a release that
changes the API inside a maintenance window, when only the server's own release is
answered.

The renamed field is the purser's line note on a sortie expense: `SortieExpense.Memo`,
formerly `Note` (`pkg/resources/sortie_expenses.go`, `@formerly(Note)`). The column is
still `Note`; the wire name moved. A console built before the rename keeps sending and
reading `note`: a body, `columns=`, `sort=` or `filter=` naming it is read as `memo`, a
body naming both is refused with 400, and every row carries both `memo` and `note`. The
role file names `memo` alone, and the permission digest carries the quartermaster's entry
under `SortieExpenses.note` beside `SortieExpenses.memo`, so a console built before the
rename keeps its column. The generated TypeScript knows only `memo`, so the flight deck and the
expense manifest moved to it. The next release that raises oldest answered past this one drops the annotation,
and `note` leaves the wire.

## The program the release ships

The droids run a program the release ships: `cmd/droid`, declared on the droids outlet
in `cmd/generate/generator.go` with `generation.ShipsProgram("cmd/droid",
generation.Platforms("linux/amd64"))` ([`outlet.programs`](cmd/droid/main.go)). The
image build compiles it for each platform into the image, and the outlet serves the
file with its checksum at `GET /droids/programs/droid/<release>/linux/amd64`, behind
the token check, so a copy on a droid fetches its next release where it already sends
its telemetry. A person installs the first copy from the release's assets or with `go
install github.com/impulseframework/impulse/lodestar/cmd/droid@<release>`. Given the
outlet's URL, the droid account's token (the key the server seeds as its application
token, `APP_DROIDS_API_KEY`, or a token a crew member issued), the sector and the ship,
each by its id (`DROID_OUTLET`, `DROID_TOKEN`, `DROID_SECTOR`, `DROID_SHIP`;
`DROID_FIRMWARE` names the firmware in the frame), `droid reactor 0.55` sends one reading through
`IngestDroidReports`, the frame written as the firmware's own JSON, and then the copy
reads the server's release from the answer (`X-Api-Version`): newer than its own, it
fetches that release's file with the token it carries, verifies the checksum, swaps the
file in beside itself (the old one kept as `droid.old`), says so and exits 0 for its
service manager to start the new release (`resource/shipped`). A copy built from a
checkout never upgrades, and the development stack runs `dev` on both sides, so the
walkthrough cannot show the upgrade; the generated router test proves the route sits
behind the token check, and `impulse check` (`shipped-programs`) builds the program for
each platform it declares.

## Live pages

The console's Ships page is live. Sign in as `harbormaster` (Harbormaster Hollis), open
Sector Ops, then Ships, and leave the fleet board on the screen. In a second browser
profile sign in as `engineer` and clear a refit on the hangar deck, or hail a ship as
`pilot`: the harbormaster's board changes without a reload, and so does a ship's own
page when she has one open. Leave the board for the dashboard and come back inside five
minutes: the page is drawn from the browser's own cache and the server sees no request.
Nothing polls and the server holds no connection open. A page that asked to be live is
told about a change through a change set of its user's own in Firestore, and asks for
the page again itself, by the change's timestamp.

To watch it in the emulator: the Firestore emulator answers Firestore's REST API on
`FIRESTORE_EMULATOR_HOST`, and `Authorization: Bearer owner` reads everything. The
database is the one the live driver opens under its own project id, `live-emulator`
(`firestore.EmulatorProject`, since `GOOGLE_CLOUD_FIRESTORE_PROJECT` is unset), the id the
token route hands the browser too. So with the stack running and `.envrc` loaded

    curl -s -H 'Authorization: Bearer owner' \
      "http://$FIRESTORE_EMULATOR_HOST/v1/projects/live-emulator/databases/(default)/documents/subscriptions"

lists every live subscription (`principal`, `tab`, `resource`, `key` for a row,
`domains` for the sectors a list is held over, `expiry`), and
`.../documents/users/crew%7Cf0000000-0000-4000-8000-000000000024/changes`
lists the documents written into Hollis's set: a `row` document for the ship she had open
and a `list` document for the fleet board in Anvil, each with its server timestamp `at`,
which is the version (`_v`) her page asks again by. The set's uid is
`crew|f0000000-0000-4000-8000-000000000024`, the crew auth's name and the id of her
account joined by `|` (written `%7C` in the URL): the id is how the permission store
names a person, so a change of username leaves her set and her subscriptions where they
are, and an account of the portal's members auth has a set of its own whatever its
username. The hazard analyst watching Cinder's fleet gets no document for an Anvil refit, and a
persona without List on Ships (the cadet) is never subscribed: the refused request
answers 403 as it always did and writes no record. The walkthrough plays this scenario
by curl (its "live pages" section), and
[`live_test.go`](test/integration/live_test.go) pins it over an in-memory live service.

How it is wired:

- The stack. The Procfile starts the Firestore emulator beside the Spanner emulator, from
  the Cloud SDK emulators image the resource package's own tests pin, with the live
  package's security rules (`resource/live/firestore/firestore.rules`) mounted;
  `.envrc.template` sets `FIRESTORE_EMULATOR_HOST`. A fresh emulator per start means an
  empty record. In production `APP_FIRESTORE_DATABASE` names the database,
  `GOOGLE_CLOUD_FIRESTORE_PROJECT` its project (required with the database: the data
  level refuses to start naming both when it is empty, since the database is not assumed
  to be in the Spanner project; against the emulator it may stay unset, and the driver
  opens the emulator under its own project id, `live-emulator`) and
  `APP_FIREBASE_API_KEY` the browser's key; the composite indexes and the time-to-live
  policies the live package's README lists are the infrastructure's to apply.
- The server. `pkg/config/data.go` reads the Firestore settings at the data level and
  constructs the live service (`resource/live/firestore`) once; every application wires
  one (the generated handlers, the feature flags and both permission engines ride it,
  [below](#one-channel-between-instances)), so a configuration naming neither a database
  nor the emulator does not start; `app.LiveService()` hands it to the generated handlers, which
  register a subscribing request's interest before the query runs, publish a commit's
  touched rows after the commit and before the answer, and send
  `Cache-Control: private, max-age=300` on a list or read answer whose request carried
  `_v`. Both session outlets serve the generated `live/renew`, `live/unsubscribe` and
  `live/token` routes; the droids outlet refuses `X-Subscribe` with a 400 naming the
  header. The content security policy's `connect-src` names the feed's origin beside the
  application, the emulator in development and Firebase's hosts in production, because
  the browser's feed connects there directly ([`live.pages`](app/app.go)).
- The browser. Every list page and record page on a table-backed resource is live by
  default: the generated metadata's `live` says the resource's changes are published, and
  the library makes such a page live unless its config says `live: false`. A page on a
  virtual or computed resource stays plain until that resource's changes are published,
  and a config asking `live: true` of one is announced once in the browser console. So
  [`ships.config.ts`](web/console/src/app/configs/ships.config.ts) says nothing about it
  and the fleet board and its row page are live, while the pickers and the edit form on
  them stay plain; [`app.config.ts`](web/console/src/app/app.config.ts) provides
  `CHANGE_FEED` with the Firestore feed from `@impulseframework/resource-firestore`, and the
  library's `AuthService`
  starts the feed after login with the identity `live/token` answers and stops it at
  logout, unsubscribing everything first. The environments carry nothing
  Firestore-specific: the project, the database, the emulator host and the key arrive in
  the token payload.
- The checks. The walkthrough section above, the integration suite, and a headless check
  driven in Chrome (playwright-core and the system browser from a scratch project outside
  the repository, as the console's other browser proofs are run) that leaves the fleet
  board and comes back with no request to the server, then sees a hail produce a refetch
  carrying the change's timestamp as `_v`.
- The derived resources. A page on a virtual or computed resource is live the same way
  once the publish knows which tables change it. The generator reads each virtual
  resource's tables off its embedded query with memefish's lexer, the names in table
  positions: Mission Boards carries Missions' rows
  (`@rowsOf`) and reads no other table; Squadron Rosters and Pilot Assignments
  carry SquadronMemberships' rows and read Squadrons, Wings and, for the rosters, Pilots;
  Client Rosters, Fee By Kinds and Open Missions By Squadrons carry no table's rows and
  read Clients, ClientContacts, Missions, Sectors and Squadrons between them. The hazard
  board is a computed resource, so it declares what its body folds,
  `@changedBy(Ships, DroidReports)`; the other computed resources declare nothing and stay
  plain, the expense manifests among them although their body reads Missions. The sets
  are recorded next to the resources, read or declared as each entry says, in
  [`pkg/virtualresources/zz_gen_live_derivations.go`](pkg/virtualresources/zz_gen_live_derivations.go)
  and [`pkg/computedresources/zz_gen_live_derivations.go`](pkg/computedresources/zz_gen_live_derivations.go),
  every table as the `Resource` method of the type over it, and
  [`app/zz_gen_live_derivations.go`](app/zz_gen_live_derivations.go) assembles them for
  every publish; each such resource's metadata says `live: true`, so its pages are live
  by default. The lists a sector keeps of the crew and of the members are views too,
  declared by each auth's generated management and recorded in the auth's handlers
  package
  ([`pkg/identity/crewhandlers/zz_gen_live_derivations.go`](pkg/identity/crewhandlers/zz_gen_live_derivations.go)),
  one more value of the assembly: a role given through the generated method reaches the
  sector's open list ([`auth.tenant-lists`](cmd/generate/generator.go)). A
  mission's write reaches the mission board's row and list subscribers under the
  mission's own key, as Missions' own; a client's write, Clients being global, reaches
  every subscriber of the client rosters in every sector as one resource document, a
  mission board that shows its clients by name among them; a ship's write
  reaches the hazard board's subscribers in its sector. The integration suite plays all
  three ([`live_test.go`](test/integration/live_test.go)).


## Feature flags

The commendations desk is behind a feature flag, and the flag is seeded off. Sign in as
`adjutant` (Adjutant Alba): the server answers the desk's routes with the router's own 404,
the permission digest carries no `Commendations` and no `PilotCards.commendations`, so the
console shows no page and no card field for it, and a request naming the card's field is
refused as an unknown field. Turn the flag on (Feature flags, under System in
Headquarters, is the library's page for anyone holding List on `FeatureFlags`: the flags by
name with who last turned each on or off and when, a History button on each row, and, for a
login holding Execute on `SetFeature`, Turn on and Turn off in the row's menu, each asking
first; the walkthrough does the same
through `POST /console/api/set-feature` with `{"name":"commendations","enabled":true}`)
and the desk exists: its routes answer, the digest carries it, the Commendations item
appears in the console's navigation without a reload, and Pilot Pax's card reads
"Cited: 2" from the field the flag gates. Every running
instance of the application follows the flip within a moment, with no restart and no
release; turn it off again and the desk is gone everywhere. The flag's value lives in the
application's database, so it is per environment by construction, and every flip is
recorded in `FeatureFlagChanges`, which the flag's History reads
(`GET /console/api/feature-flags/commendations/changes`, under List on `FeatureFlags`:
when, what the flag was turned to, and the sign-in name of whoever turned it).

How it is wired:

- The declaration. [`pkg/resources/features.go`](pkg/resources/features.go) declares
  `Commendations resource.Feature = "commendations"`, its doc comment the description an
  administrator reads (the page lists the flag as "Commendations", its name in words, and
  the description opens with the same words in place of the constant's name). `@feature(Commendations)` on the `Commendation` struct
  ([`@feature`](pkg/resources/commendations.go)) gates the desk whole; on
  `PilotCard.Commendations` ([`@feature.field`](pkg/computedresources/pilot_cards.go)) it
  gates the one field. The generator writes `Features()` and `FeatureGates()` into
  `pkg/resources/zz_gen_features.go`, wraps the desk's routes in the application's
  `FeatureGuard`, filters the digest, teaches the decoders the field, and writes
  `test/authz/zz_gen_features_test.go`, every gated route and field driven in both states;
  the TypeScript descriptor and metadata carry `feature: 'commendations'` and the client
  file declares the `Feature` union and constants.
- The table and the seed. Migration `000043` creates the desk's table, which ships whether
  or not the flag is on, with three seeded citations; migration `000042` carries the
  library's `FeatureFlags` and `FeatureFlagChanges` tables. The bootstrap and the deploy's
  migrate step call `deploy.MigrateFeatures`, which writes the declared flag off where the
  table has no row and keeps the state where it has one, so a fresh stack starts with the
  desk dark and a release never flips anything.
- The role and the persona. `FeatureAdministrator` (global: List and Read on
  `FeatureFlags`, Execute on `SetFeature`) is the one role that flips flags, held by
  `adjutant` beside `Adjutant`, the desk's own role (List, Read and Create on
  `Commendations`); no other role gained a flag grant. The crew's `PilotCards` grant carries
  the `commendations` field since the feature shipped: while the flag is off the digest
  hides it, so turning the flag on needs no role change.
- The instances. `app.New` loads the application's copy of the table and `App.Start`
  follows it (`FeatureSet.Follow`): on every signal of the `features` kind on the live
  service's signals document ([below](#one-channel-between-instances)), and every five
  minutes regardless. `SetFeature` writes the row and its change record in one
  transaction, signals the kind after the commit, reloads its own copy and answers the
  flag as written. A second server process on another port sees the flip at its next
  request; should a signal be missed, at the backstop.
- The checks. The walkthrough's "feature flags" section drives the scenario by curl, a
  second server process built from the tree included;
  [`feature_flags_test.go`](test/integration/feature_flags_test.go) pins it over two
  instances, the real engines and the in-memory live service; the generated authorization
  matrix puts every declared flag on in its own test database before it drives a route,
  since a flag that is off answers before the permission gate the matrix pins. In the
  console, `resourceRoutes` reads `feature` from the generated metadata and guards the
  desk's route with `featureMatch`, `resourcePage` carries the flag onto the desk's menu
  entry, the pilot card's "Cited" line on the dashboard sits under `*impulseFeature`, and
  Feature flags, under System, is the library's page (`featureFlagsPage(resourceMeta)` in
  the menu file, `featureFlagsRoute(resourceMeta)` among Headquarters' routes,
  [`feature.history`](web/console/src/app/app.routes.ts)); `pilot-card.component.spec.ts`
  covers the card in both states, and the walkthrough's feature-flags section drives the
  flip through `SetFeature`.

## Finding and narrowing on one list

A list page over a resource with search fields draws the box above the grid, and the
grid's column filters beside it; both narrow one request. The page sends its own filter,
the complete column filters and the box as conjuncts of one AND, the first two
parenthesized and the box bare, `(statusId:eq:open),*:search:"survey"`. The server
applies the box and the rest of the filter in one WHERE where the box searches one index,
and gathers the box's key set and applies the rest of the filter over the joined rows where
it spans several (the clients' box, with their contacts); either way the count and the
cursor pages carry the whole, and the declared order stands. The box is the
indexed touch a companion-only column waits for, so on the
[Missions page](web/console/src/app/configs/missions.config.ts), the board, whose box searches the
mission's title, with filters on status, squadron, kind, deadline, days left and the
briefing template's key, a
days-left filter rides beside the box alone and leaves when the last indexed touch, the
box or a status filter, is cleared; the deadline's menu takes a date as a person writes
it (below). The filter is placed as a whole: on the
[Clients page](web/console/src/app/configs/clients.config.ts) headquarters, whose box
reaches the contact fields, posts it with a name filter or a contact-name match beside
the box, and a ClientBrowser persona, whose box searches the name alone, keeps the same
shape in the URL and reads under the box which fields the role leaves out.

The checks: the integration suite's `search_filters_test.go` sends both pages' requests
in that wire form and pins the answers against the unfiltered lists, and a headless check
driven in Chrome (playwright-core and the system browser from a scratch project outside
the repository, as the console's other browser proofs are run) types in the box, commits
filters from the column menus, clears the box, turns the page, and reads each request's
method and filter beside the server's log, on the emulator and on a real instance.

## Dates in a filter

A filter value on a date or timestamp column is read as a person writes it. On the
[Missions page](web/console/src/app/configs/missions.config.ts) the deadline's menu takes
`2026-12-15` or `12/15/2026`, a month `2026-12` or a minute `2026-12-15T09:30`, and the
server reads the whole span it names: `eq` inside it, `ne` outside, `lte` through its
end, `gte` from its start, in the person's zone when the browser sends it as the
`Time-Zone` header and in UTC otherwise. The hold's expiry date, a
[date column](pkg/resources/consignments.go) filterable beside the indexed release
date, reads a day as the day and a month or a year as its days, with no zone to read. A
value that cannot be read is refused naming the forms, `deadline: 'yesterday' is not a
date. Write it as 2026-11-03, 11/3/2026, or 2026-11-03T09:30 with the time.`, and a
header naming no zone is refused in the header's name.

The checks: the integration suite's `filter_dates_test.go` sends each form and operator
on the board's deadline and on the consignments' expiry and release columns, with and
without the header, and pins the rows against the unfiltered lists.

## One channel between instances

Every instance holds one listener on one document. The live service keeps a signals
document per application (`application/signals` in Firestore, a field per kind), and a
signal carries nothing but which kind of shared state changed: a feature flip signals
`features`, a change of the tenant roster `tenants`, a role, grant or membership write
`policy`. The instance that wrote signals its kind through the service, every other
instance rereads that kind at once, and each reader's own periodic reread is the backstop
should a signal be missed: five minutes for the flags, the permission engines' one-minute
heartbeat for the policy. Nothing polls between instances and no instance connects to
another.

To see the policy kind: sign in as `marshal` and seat `cadet` as a harbormaster at Anvil
(`POST /console/api/sectors/{sectorID}/roles/Harbormaster/users` with `{"users":["cadet"]}`,
the segment's value Anvil's id, `a1111111-1111-4111-8111-111111111111` in the development
seed);
a second server process built from the tree (the walkthrough starts one on
`LODESTAR_SECOND_PORT` for the flags and keeps it for this) serves the cadet the fleet
board at its next request, with no restart and without waiting for its heartbeat; unseat
her (`DELETE` on the same route) and it refuses again.

To see the tenants kind, bring a sector in. Nobody creates a sector directly: a create
operation on `Sectors` (`PATCH /console/api/resources` with an `add` at `/sectors`) is
refused with 400, whoever sends it, and says how a sector is made ("a sector is not
created directly: invite one person, and the sector is made when that person first signs
in"). Sign in as `surveyor` or `governor` and invite the person who will run it (`POST
/console/api/invite-crew-sector` with
`{"email":"founder@dawn.example","sectorName":"Dawn","sectorRegion":"Outer frontier","sectorEstablished":"2226-10-02"}`,
the crew auth's generated `InviteCrewSector`). The body carries the sector's fields, each
named for the record, and no key; the invitation names no sector, since none exists yet.
Sending one takes the method's grant and the Create grant on `Sectors`, on the record and
on each field given, and the details are held to the record's own create when it is
sent: a sector named Dawn that already exists refuses the invitation with 409, the
unique index on the name being one of the record's rules. The mailed message carries
the application's name and the link and nothing of the sector; the invitation page shows
the sector's name and the invited address; and headquarters reads the invitations that
wait, with their details, at `GET /console/api/crew-invited-sectors`, each field shown
where the reader may list it on `Sectors`. To change the details, withdraw the
invitation (`POST /console/api/withdraw-crew-invited-sector`) and send another. The
invited person's first sign-in, by any method the crew auth offers, makes Dawn from
those details under an id the record's create generates, seats them as its marshal
and, where they came through a Google Workspace no sector has, makes that Workspace
Dawn's directory with the starting role the invitation names (`defaultRole`), all in
the transaction that consumes the invitation; the person enters nothing. An invitation
that says directory only (`sectorDirectoryOnly`) is accepted through a directory alone:
a password is told to sign in with the organization's account, and a first sign-in
through a directory another sector has is told the organization already has a sector;
either way nothing is made and the invitation stands
([`tenancy.invited-tenant`](pkg/resources/sectors.go)).
`Sectors` is the tenant record (`@tenant`): the data configuration builds the roster with
the generated `NewSectorRoster` over the live service and starts it beside the engines
(`pkg/config/data.go`), the generated maker (`app.NewSectorMaker`, which the crew auth
is handed) adds Dawn to the roster of the instance that took the sign-in after the
commit and signals the kind, and the second process serves
`/console/api/sectors/<Dawn's id>/...` at its next request, the roster's own five-minute reread
left as the backstop. Nothing but the founder's role is written to the permission store:
the surveyor's `SectorCrew`, the governor's `SectorMarshal`, the archivist's role and the
client's directory role are each held in every sector, so Dawn lights on their charts
with nothing written for it.

A sector that is deleted leaves nothing behind
([`tenancy.deleted-tenant`](pkg/resources/sectors.go)): the generated delete path removes,
in the transaction that deletes the row, the roles people held in the sector, the custom
roles made in it and the invitations into it that still stood, in the crew's tables and
the members' alike, and the sector's directory, whether the sector is directory only or
not, so the directory is free for another sector; a person who held roles in that sector
alone goes with it, while one who holds a role elsewhere stays and signs in as before.
No role Lodestar ships holds Delete on `Sectors`; the suite gives the governor one to
prove it (`test/integration/tenant_delete_skipauth_test.go`). The delete carries the
class `access`, so a person who acts as the governor under the `act` kind is refused it
(`TestImpersonation_sectorDeletedActingAsAPerson`). The sector's rows go with
it, and the delete takes them in code: every row under the sector, in the same
transaction, each through its resource's own delete, so a kind that keeps a change
record keeps one of the row that went. A sector with rows under it is asked first
("Delete this sector and everything under it: 1 wing, 1 sector directory, 1 hangar, 1
distress call, 1 consignment, 1 squadron, 1 ship, 1 distress relay, 1 squadron
membership, 1 refit, 1 mission, 1 hangar pass, 1 sortie, 1 refit task, 1 mission
document, 1 sortie expense, and records you can't see. This can't be undone.", counting
each kind the person may list in that sector), and the request that goes ahead says it
takes them. The schema carries no rule under that: no foreign key and no interleave of
Lodestar's records says `ON DELETE CASCADE`, as the skeletons' sample tenant table has
none, and `impulse check` refuses one (`no-delete-cascade`). So a row deleted by itself
takes nothing with it by the database, and what becomes of the rows under it is the
record's to say. A record that says nothing is refused while rows are under it, before
the commit, with a sentence that counts what is in the way and that the person may list
(`delete.rows-under-refusal`, `test/integration/delete_refusal_test.go`): a ship while
it has refits, sorties flown on it or droid reports filed on it ("This ship has 2
sorties. Delete them first."), and a squadron while it has memberships, hangar passes or
missions assigned to it ("This squadron has 1 mission. Move or delete it first."
where only missions are under it, since a mission's assigned squadron may be empty and
the missions can be given to another squadron). The rows and the record deleted in one
request go through, in either order. A commit the database refuses for a referential
reason is answered in words too (`commit.referential-refusal`,
`test/integration/commit_refusal_test.go`: a mission booked for a client that does not
exist).

A record that declares what goes with it is deleted another way
([`@deleteCascade`](pkg/resources/missions.go)): the library finds the rows under it and
deletes each in code, by that row's own delete, once the request says it takes them. A
mission declares its sorties, their expenses and its documents; a wing its squadrons
with their memberships and hangar passes, with a mission assigned to one of them
refusing its delete; a hangar its passes and its ships with their refits and tasks, with
a sortie or a droid report on one of its ships refusing it; a refit its tasks; a sortie
its expenses; a client its contacts, with a mission or a consignment refusing its
delete; a distress call the relays passed along about it; a relay the relays that answer
it. A delete of such a record that has
rows under it deletes nothing at first. It is answered 428 with the question a person is
asked ("Delete this mission and everything under it: 2 sorties, 1 mission document, 1
sortie expense. This can't be undone."), which counts each kind the person may list and
says "and records you can't see" of the rest. The same delete saying so (the remove
operation's value `{"everythingUnder": true}`) deletes the rows and the record in one
transaction; the delete dialog asks the server's question in place of its own and sends
that at the next press. Each row goes by its own resource's delete, so the person needs
Delete on every kind taken ("This mission has sortie expenses that you can't delete, so
it can't be deleted."), each kind's own delete rule is asked, a row whose kind tracks
changes leaves its change record, and a document's stored file is released. A
client with a mission or a consignment is refused either way ("This client has 10
missions and 5 consignments, so it can't be deleted."). A record with nothing under it
is deleted without the question (`test/integration/delete_cascade_test.go`). The relays
show the two links the generator follows beyond a table's key: a relay names its call by
its sector and the call's case number, which is unique within a sector and is not the
call's key, so a relay never names another sector's call
([`@deleteCascade.other-columns`](pkg/resources/distress_relays.go)), and a relay that
answers another names it in its own table, by its sector and that relay's id, so an
answer never names another sector's relay
([`@deleteCascade.own-kind`](pkg/resources/distress_relays.go)), so a call's delete takes
its relays and the relays that answer them, however deep. The Calls page lists a call's
relays under it ([`distressCalls.config.ts`](web/console/src/app/configs/distressCalls.config.ts)).

How it is wired:

- The adapter. [`pkg/auth/signal.go`](pkg/auth/signal.go) builds the engines' change
  signal with `access.ChangeSignalFunc` over the live service's `Signal` and `Subscribe`
  on `resource.KindPolicy`: announce signals the kind after a policy write (the engine
  logs a failed announce and the write stands), watch subscribes and blocks until the
  engine's context ends. `pkg/config/data.go` builds it once over the live service and
  hands it to both auths (`crew.Settings.ChangeSignal` and `members.Settings.ChangeSignal`,
  both required), so the crew store and the members store ride one listener.
- The route. A role is given and taken away in a sector through two methods of the
  management the generator writes for the crew
  (`POST /console/api/sectors/{sector}/assign-crew-sector-role` and
  `…/remove-crew-sector-role`, each taking the person's id and the role;
  `GET …/crew-sector-user-roles` lists the roles held there), which write the membership
  through the crew's identity client ([`app/users.go`](app/users.go),
  `pkg/config/data.go`): Execute on `AssignCrewSectorRole` in the sector, held by
  `SectorMarshal` under the condition `new.role != 'SectorMarshal'`, so a marshal gives
  every role but its own. The crew's roles are the application's whichever way a person
  signs in; the members' are their directory's at every sign-in (the registration's
  `SyncsRoles`, standing alone on its auth), so the members' management has no method
  that gives or takes one.
- The checks. The walkthrough's "one channel" section gives the cadet a role and takes
  it away against the second server process over the Firestore emulator;
  [`policy_signal_test.go`](test/integration/policy_signal_test.go) pins it over two
  instances, two crew engines with the heartbeat pinned to an hour, and the in-memory
  live service, which fans a signal out to every subscription.

## The nightly prune

The telemetry link keeps ninety days of droid readings, and one method keeps it so.
`PruneDroidReports` ([`@schedule`](pkg/rpc/prune_droid_reports.go)) is declared with
`@schedule("30 3 * * *", zone: "America/Denver")`: Cloud Scheduler calls it every night at
03:30 headquarters time, and it deletes the readings recorded before the window and answers
how many went. No person calls it and no role names it. The generated router serves it at
`POST /_scheduled/prune-droid-reports` behind `SchedulerAuth` alone, which admits a token
Google signed for that URL whose verified email is the invoker identity, the service account
the stack names in `APP_SCHEDULER_INVOKER`, and answers any other call 401, logging why. The
App's `SchedulerAuth` (`app/app.go`) delegates to the guard `pkg/config/site.go` builds with
`scheduled.FromEnvironment`; in development the variable is unset, so the scheduled routes are
off, the server says so when it starts, and every call is refused. The release file,
`pkg/router/zz_gen_release.json`, lists the route with its schedule and zone, which bedrock
reads to create the Cloud Scheduler job and the invoker identity in each environment.
[`test/integration/scheduled_test.go`](test/integration/scheduled_test.go) calls the route on
the served stack with tokens from a fake of Google's keys: a call with no token, with another
identity's token, or with a token for another route is refused and deletes nothing, and the
scheduler's own call deletes the stale reading and keeps the fresh one.

## The quiet surfaces

Every request Lodestar serves writes one entry to the request log, as it always did, except
on two surfaces that would flood it. The beacon pulse, `GET /beacons/{sectorID}`
([`generation.request-log`](pkg/router/hooks.go), [`app/beacon.go`](app/beacon.go)), is
asked all day by every droid in the sky, with no session and no key, outside every outlet:
the generator program declares its prefix quiet once, with
`generation.WithMountedRoutes("/beacons/", generation.LogOnEvent(), generation.TracesOff())`
([`generation.traces`](cmd/generate/generator.go)), and the generated router hands the
prefix to the request logger's prefix table and to the tracer's surface table, so a pulse
answered 200 writes no entry and records no span, while a pulse answered 404, a droid
asking after a sector that is not charted, is an event and writes its entry. The droid's
own method, `IngestDroidReports`, is declared on event on its annotation,
`@rpc(log: onEvent)` ([`@rpc.log`](pkg/rpc/ingest_droid_reports.go)): the generated routes
file sets the word ahead of the handler with `logger.WithPolicy`, so a reading that lands
writes no entry and a reading the method refuses writes the request's entry with every line
the handler logged attached. The chain comment at the top of `pkg/router/zz_gen_router.go`
names the word at each place, and the generated router test proves each decision against
the logger's console exporter. The release file, `pkg/router/zz_gen_release.json`, lists the
two surfaces under `surfaces` with their words, which bedrock reads to render the stack's own
log exclusion from the same declarations.
[`test/integration/request_log_test.go`](test/integration/request_log_test.go) drives both
on the served stack and reads the console exporter's decisions: no entry for the pulse and
the reading that land, an entry for the pulse and the reading that fail, and an entry for a
droid's list, which declares nothing.

## Running against a real Spanner instance

The emulator answers every test, but it returns no query plans, does not promise the
service's execution plans, and has behavior of its own (it refuses `NULLS FIRST` and
`NULLS LAST`; its error texts differ). Once in a while Lodestar runs against a real,
private Cloud Spanner instance to catch emulator-specific behavior and to read real plans.
A real database here is a private test target, not a published application: the committed
personas and their passwords are as valid there as on the emulator, and the login page's
persona cards stay.

The bootstrap picks its target from the environment alone, the way the Spanner client
library does: with `SPANNER_EMULATOR_HOST` set it talks to the emulator, without it to the
project the application credentials reach.

1. Have an instance. The bootstrap creates the database when it is missing but never the
   instance; the credentials need the Cloud Spanner Database Admin role on it.
2. In a shell with `.envrc` loaded: unset `SPANNER_EMULATOR_HOST`, set
   `GOOGLE_APPLICATION_CREDENTIALS` to the credentials file, and point
   `GOOGLE_CLOUD_SPANNER_PROJECT`, `GOOGLE_CLOUD_SPANNER_INSTANCE_ID`, and
   `GOOGLE_CLOUD_SPANNER_DATABASE_NAME` at the instance.
3. `go run -tags skipAuth ./cmd/bootstrap` creates the database, applies the schema, seeds
   the world, checks both auths' policy stores against their role files, and creates the
   cast's accounts under their fixed ids with the roles each holds. Schema changes on a
   real instance are slow and counted against its limits,
   which is why the schema is one migration (`000001_Baseline` applies in about two minutes
   on an Enterprise instance, where the sixty files it folds took more than thirteen,
   measured 2026-11-02), and this runs once per database; every later session starts with
   `go run -tags skipAuth ./cmd/bootstrap -reset`, which empties the data and seeds it again
   with no schema change, in about a minute.
4. `go run -tags skipAuth .` serves the application on `PORT` against the real database,
   and `overmind start -l console,portal` runs the browser apps against it.
5. `LODESTAR_LOG=<the server's log> ./walkthrough.sh` runs every persona's proof; it moves
   workflow state, so reset before running it again. A password sign-in waits for a mailed
   sign-in code, and in development the mail is the log, so the walkthrough reads each
   code from the file the server's output goes to (`go run -tags skipAuth . 2>&1 | tee
   server.log`). Then use the console and the portal by hand.
6. Reset with the server stopped, and start it again after. A running server's permission
   engine re-reads its store every minute; a reload that lands while the reset has the
   role tables empty answers every domain as unknown until the next reload after the
   reseed, and the reset ends every session anyway.
7. Anything that behaves differently from the emulator is a finding: record it as an
   issue. When done, drop the database or keep it for next time; the instance is the
   running cost.

## The world

Three **sectors** are the tenants: **Anvil** is home (nearly everyone holds a role there,
and the hangars are there), **Bastion** is where a role stops at a border, and also the
sector that admits its people through its directory only (the persona provider's, so no
password and no other sector's directory reaches it, but for the password and code of a
marshal of Bastion itself),
and **Cinder** is the dark sector only headquarters, the archivist, and the hazard analyst
can see, and the sector the fleet's Google Workspace is the directory of.
Clients post **missions** with a hazard class, a fee, and a deadline. **Squadrons**
(grouped into **wings**) claim them and fly **sorties** with **expenses**. **Ships** in
**hangars** go through **refits** bay by bay. **Droids** post telemetry over their own
channel and release **consignments** held in bond. Every list pages at the size its
`@page` annotation declares, and the seed is deep enough that every list of consequence
has a second page.

## Two populations, two auths

Each auth is one `session.Auth` with its sign-in methods registered on it, and the
generated router tells each outlet's login page what its auth offers at
`/<prefix>/user/methods`: the methods in the order the generator program declared them,
each with its kind (a password form, or a redirect to a directory), its label and the
path the router mounted it at, and the pending sign-in's routes, so the page renders from
the server's list and names no path of its own. Both pages compose the library's login
component (`<impulse-login-methods>` from `@impulseframework/resource-angular`), which reads the list
through the application's client, renders the form and the buttons, posts the password
after reading the list again (the read refreshes the XSRF cookie the post needs), sends the
browser to a directory with the page to return to, shows the sentence held for a refusal
code, and renders the confirmation step for a sign-in that waits; each page keeps its
manifest, its words for a method (the portal's "your company directory", a label override
keyed by the registration's name) and where a sign-in lands.

Every session carries a second factor, whichever way the person signed in, while its
auth's sign-in setting requires one, as it does until Headquarters turns it off on the
console's Sign-in page (`StopRequiringCrewSecondFactor`; each auth keeps its own
setting). The session library reads the setting at every sign-in and satisfies it before
it holds the sign-in. No role is granted the members' three methods, so the members'
setting stays as it starts, required, until a role file grants them. The fleet's Google
Workspace attests for its people where the deployment says the fleet administers it
(`APP_CREW_OIDC_ADMINISTERED`, `APP_MEMBERS_OIDC_ADMINISTERED`;
`auth.Directory.Administered` declares the Workspace `Attesting()` on the registration),
so the navigator and the clients are never asked; a password persona, and the pioneer
who signs up with any Google account (a directory nobody here administers), wait for the
sign-in code the auth mails to the address they hold (a persona holds none until they
give one at the step; in development the mail is the log), entered on the login page
through the pending routes (`/console/api/user/pending/code`, `…/code/verify`,
`…/email`); a browser a person asked to be remembered stands in for the code for as long
as its auth's sign-in setting says, thirty days until it is changed (the `crew-trusted`
and `members-trusted` cookies, the trusted devices table under each auth's prefix), and
headquarters forgets a person's remembered browsers from the person's account page
(`RevokeCrewUserTrustedDevices`, `RevokeMembersUserTrustedDevices`).
A sector's marshal, signed in through the sector's own directory, can say that the
directory asks its people for a second factor (`VouchForSectorDirectorySecondFactor`,
on the sector's Sign-in settings page, behind a warning and the word `vouch`): a crew
member who signs in through that directory is then not asked for the code, in that
sector and in every sector that gave them a role, on the sector's word alone; the
governor and a password persona are asked as before, and
`StopVouchingForSectorDirectorySecondFactor` takes the word back from the next sign-in.
Demonstrates: auth.second-factor, auth.attested-directory, auth.trusted-device.

The crew sign in with passwords at `/console/api/user/password/login`, or through the
fleet's Google directory at `/console/api/user/google/login`, two methods on one session:
or sign up with any Google account at `/console/api/user/sign-up/login`, three methods
on one session: whichever way a crew member signs in, the session is a crew session in
the same table under the same cookie. A password persona's roles are the application's
(`pkg/auth/crew/roles.json`, embedded in the binary and validated against the generated
permission collection when the auth opens, so a release whose roles are wrong does not
start; the store holds no row for them, and the bootstrap assigns the memberships from
`cmd/bootstrap/users.json`, a role held in every sector as one membership and a role held
in one sector under the sector's name, which the bootstrap finds the id of; a role is
held by an account, under the account's id, so the bootstrap creates each persona's
account first, under the `id` the file gives it, and assigns the roles under that id).
A person the
directory admits is provisioned under the verified email at their first sign-in, a login
on no manifest (a verified email that is a password persona's login links the identity to
that persona once their password confirms it), and their roles are the directory's groups,
reconciled at every sign-in;
a Google group is an email address, so the names the groups carry are lowercase, and the
sync resolves each at the store without regard to case and writes the role file's
spelling (`crew-pilot@` names `Pilot`). The clients sign in through their company's
Google directory at `/portal/api/user/directory/login`; their roles are the directory's
groups (`google.RoleSync`), reconciled at every login and never assigned in the
application, a global role held in the global partition and a domain role in every
sector, each under the id of the client's account; `pkg/auth/members/roles.json` only
says what a role may do, in the lowercase names Google groups carry. The directory
decides who is a super administrator of the portal too: a group that names the Super
Administrator role gives it at the client's sign-in, the role goes at the sign-in
where the directory no longer names it, the last holder's included, none is seeded, and
the second factor the library asks of a super administrator in the crew's auth, whose
roles the application gives, is not asked here beyond what the portal asks of every
client. A client's account
is made by their first sign-in; the bootstrap makes the accounts of the clients the demo
world's rows name ahead of that (the file's `members`: no password, no roles, a fixed
id), and the first sign-in through the directory finds the account under the directory's
sign-in name and links to it. The deploy's migrate step and the bootstrap print what each store
holds that the release cannot use. Each auth is a package (`pkg/auth/crew`,
`pkg/auth/members`) with its own session tables, permission store, role file, and XSRF
cookie (`crew-xsrf`, `members-xsrf`), and each outlet binds to one. The crew's memberships
are also the application's to change at run time, through the management the generator
writes for each auth (below, and [one channel](#one-channel-between-instances)): the
console serves the crew's and, by the generator program's `Manages`, the members' beside
it, so headquarters administers both populations; a directory-signed member's roles are
the directory's, reconciled at every login, and the members' management has no method
that writes them. A crew login an
administrator requires a new password of carries the forced password change, which the login page serves before anything
else.

A person is named by the id of their account, never by their username. The id (the key
of the auth's users table, as text) is what the permission store holds every membership
under and what a row condition's `subject` binds to; the username is what people read
and type, and it may change without anything moving. So every column that anchors a
person holds the id (`Pilots.UserId`, `ClientContacts.UserId`,
`PilotCertifications.UserId`, `SquadronMemberships.UserId`, `HangarPasses.UserId`,
`Sorties.PilotUserId`), and
so does every column that records who did something (`Missions.BookedByUserID`,
`DistressCalls.FiledByUserID`, `Refits.OpenedByUserID`, `Commendations.AwardedByUserID`,
`MissionDocuments.UploadedByUserID`), which the server stamps from the session with the
identity the request's permission check ran as (`resources.CurrentUser` in
`pkg/resources/resources.go`), so `bookedBy = subject` holds for a row's author. No page
shows an account id. Beside each column that records who sits a name column
(`BookedByName`, `FiledByName`, `OpenedByName`, `AwardedByName`, `UploadedByName`): the
person's username as it was when the row was written (`resources.CurrentUserName`),
written by the server alone and granted for reading wherever the column beside it is. A
record that anchors a person carries its own name (`Pilots.DisplayName`,
`ClientContacts.DisplayName`), and the purser's expense sheet names a sortie's pilot from
the pilot's record. A method that gives or takes a role names the person by `userId`
(`assign-crew-role`, `remove-crew-role`, and the sector's pair), and a role is given to
an account: an id no account holds is refused. The ship's log and the client statement
sign each change with the username, the account id and the session
(`booking (f0000000-…), session …`), the actor first when a session operates as someone
else or as a role (`marshal (…) impersonating cadet (…), session …`,
`governor (…) as role Dispatcher, session …`). The demo cast has fixed account ids so
that the seed can say whose a row is: `cmd/bootstrap/users.json` gives every account an
`id`, one prefix for each users table (`f0000000-0000-4000-8000-0000000000NN` for the
crew's accounts, numbered in the file's order with the droid service account last;
`f1000000-…` for the members'), the bootstrap creates each account under its id
(`session.CreateUserRequest.ID`), and `schema/devseed` carries the same ids, so a
development database reads the same on every machine.

The generator also writes each auth's management as code the application owns
(`generation.GenerateIdentity("pkg/identity")` in the generator program). Per auth it
writes three packages: resource declarations over every table the session library and
the permission store keep for the auth (`pkg/identity/internal/crewresources`), the
methods that change them (`pkg/identity/crewidentity`: invite an address, which is how
a person is added, and withdraw the invitation; rename, disable, enable and delete an
account; ask for a change of its email address, which the person confirms from the new
mailbox; send a password reset link, through which the person chooses their own
password, and require a password change; say whether people may ask for a reset link
themselves at the sign-in page; reset the second factor; clear a lockout; assign and
remove a role; end a session, all of a person's sessions or an impersonation; unlink an
identity; remove a password; revoke a token), and their handlers
(`pkg/identity/crewhandlers`); the members get the same three, without the methods
their auth has no use for (their roles are the directory's and the auth declares no
password, so they have no invitation, no role method and no password method). The
lists are served through the console's permission surface
(`crew-session-users`, `crew-sessions`, …) and take no write: a table changes through a
method, each a route behind an Execute grant (`disable-crew-user`, …) and also a Go call
the application makes in a transaction of its own, beside its own writes. No declaration
carries a password hash or a token hash. The rules are the methods' own: an
invitation gives a role, at least one, and an account is deleted with its last, nobody
disables or deletes their own account, a person keeps a sector's administrator role nobody else holds there (the
client is built with the roles that administer a sector, `identity.WithAdministratorRoles`
in `pkg/auth/crew/crew.go`), the last super administrator who can sign in keeps the role
and the account, and an invitation is written in the method's transaction and
mailed once it has committed, the answer saying whether the message went. The
console writes no page for any of it. The generated API description names each list and
method of an auth's management by what it is, and the browser library builds the pages
from it (`identityRoutes` in
[`web/console/src/app/app.routes.ts`](web/console/src/app/app.routes.ts)): the crew's
accounts, mailed links, sessions and failed sign-ins, and the members', each in its own
group of the navigation ("Crew accounts", "Members accounts"), with a person's row page
holding the roles held, the sign-in identities and the sessions. A crew member's page
also lists the ways they sign in, the password where they hold one and each identity
that can sign them in, as the server judges it, and above that list offers Remove
password, only for a person it shows a password and an identity that counts for, and
only to whoever manages the account. A person who has moved to the fleet's directory is
Cinder's to manage, since the fleet's Workspace is Cinder's directory: the removal is
offered to Cinder's marshal, on the person's page among Cinder's people, and not to
Headquarters, which is offered it for a person no sector manages. The members sign in
through their directory alone and have none to remove. A row's menu holds the
methods the server says the caller may run on that row, so a role that holds no grant on
a method sees no entry for it, and the members' pages offer nothing that gives a role,
adds an account or invites one. Headquarters holds the grants
(`pkg/auth/crew/roles.json`). Those that change a crew member's account or mail its
password reset link it holds only for the accounts no sector manages
(`managedByTenant = false`): a crew member a sector manages is listed like anyone, and
their row offers Headquarters none of those methods. Giving and taking the crew's roles,
ending one session, revoking a token and clearing a lockout name no account's row and
stay Headquarters' whoever manages the account. Two things follow: a role Headquarters
gives for the whole fleet, or in a second sector, makes the account of a person who
signs in through no sector's directory the fleet's to manage; and the last thing
it takes from a person takes the account with it, unless another sector's directory
signs the person in. The members' accounts are Headquarters' to change, all of them,
whoever the list says manages each: a client's roles are their company directory's,
held for the whole fleet or in every sector, and no sector's role holds a method on
a member's account, so under the condition a member who signs in through a sector's
directory would have nobody to manage the account. Those grants carry no condition.

Lodestar has a tenant record, so the generated management also declares, per auth, the
lists a sector keeps of the auth's people. An account is one row for the whole
application and a role held names where it is held, so each list is a view, written
beside the auth's tables and served under the sector's address like any tenant record:
the sector's people (`sectors/{sectorID}/crew-sector-users`: everyone who holds a
role in the sector or in every sector, and everyone who signs in through the sector's
directory, with the account's columns), the roles held there
(`crew-sector-user-roles`, one row per person and role, each with the reason it was given
for where there is one), the one list of those roles with where each comes from
(`crew-sector-held-roles`: a role a person holds themselves beside each role that comes
to them through a group, which the row names) and the sector's invitations
(`crew-sector-invitations`). Each carries facts a grant's condition reads: `managedHere`
and `managedByAnotherDirectory` on a person (this sector manages the person's account;
the person signs in through another sector's directory), `heldElsewhere` and
`heldGlobally` on a person (a role held in another sector or in every sector; a role
held for the whole application), `everyDomain` on a role (held through a membership in
every sector alone, so not the sector's to take away) and `pending` on an invitation.
The role file says what a sector sees with them. The Sector Marshal holds all three
lists, and sees a person's name, their address and whether the account is disabled, and
the rest of the account only where the sector manages it (`managedHere = true`); the
Dispatcher sees the sector's own crew, the rows whose role the sector itself gave
(`everyDomain = false`). A role given or a person's name changed through the generated
methods reaches the open lists: the views' record is part of the application's live
derivations. A sector changes its people through the methods the management writes
beside the lists (`invite-crew-sector-user`, `assign-crew-sector-role`,
`remove-crew-sector-role` and the account methods), each run against a row of the
sector's lists, so a grant's condition decides row by row: the Sector Marshal disables a
person whose account the sector manages and nobody else, and gives every role but its
own. The sector's People and Invitations pages ("Sector crew", under the
sector's URL) are built by the library from the same description.

Who may enter a sector is the sector record's declaration, data of the application
rather than deploy configuration: the sector's directory (`SectorDirectory`, one row at
most under `/console/api/sectors/{sectorID}/sector-directories`: a sign-in method and the
connection its identities carry, the fleet's Google Workspace by the name both
registrations give it, `example`) and whether its people enter through it only
(`Sector.DirectoryOnly`, which closes the sector to every sign-in but one through its
directory, and closes no other sector). A sector has one directory and a
directory belongs to one sector: the schema holds each with a unique index
(`SectorDirectoriesBySectorId`, `SectorDirectoriesByMethodConnection`), and the record's
write paths ask the generated rules first (`app.NewSectorDirectoryRules`), so a refusal
is a sentence a marshal can act on. Nobody types a directory in: the row's method and
connection are stamped from the session of the marshal who makes the directory he is
signed in through the sector's (the session read's `directory` names it), so a marshal
signed in with a password is refused with how a directory is set, a request that names a
method and a connection is refused as naming fields the record does not take, and a
personal account (any Google account, which is how the pioneer signs up) is refused as
no organization's directory. The directory of a sector that is directory only is not
removed until its marshal turns directory only off. Both auths' sign-in rule is
the library's tenant policy over it (`signin.TenantPolicy`, handed the generated
`app.NewSectorDeclarationReader`, which reads the declaration in the sign-in's own
transaction). A sector's declaration restricts its own door alone: a sector that is not
directory only admits everyone it seats, however they signed in, and a directory-only
sector admits a sign-in through its own directory and, of its own marshals alone, a
password with the code sent to their email, and nothing else, so a person is
refused at sign-in only when every sector that seats them is directory only, none has
the way they signed in as its directory, and they are no marshal of one of them signing
in with a password; a person who holds a role outside any one
sector (a global role, or one held in every sector) and a sign-in whose directory names
the roles are admitted on the library's checks, and a
person holding no sector too, since the pioneer signs up first and charts after. One part
of a sector's declaration reaches past its own door: an account that signs in through a
directory-only sector's directory signs in through that directory alone, whichever
sectors seat the person. Any other sign-in of the account is refused, a password too,
but for the password of that sector's own marshals, which waits for the code sent to
their email, and for a super administrator. Whether
anyone signs up is headquarters' choice in the running application, method by method: the
console's Sign-in page lists the crew's sign-in methods, each invite only until the
governor opens it (Open sign-up, behind a warning in the server's words, "Anyone with a
Google account will be able to sign up and create a sector of their own, without an
invitation.", confirmed by typing a word) and invite only again when he closes it (Make
invite only), and the create-sector route refuses a person whose session did not sign in
through an open method (read with `GET`, it tells a page whether the caller may chart a
sector now). A method stays
open for the accounts it admitted when it was opened: were a release to change whom the
registration admits, the method would be invite only until the governor opened it again. Which
sectors the session then reaches is decided again per sector where its chart is computed:
every sector that seats the person, except a sector that is directory only and whose
directory is not the one they signed in through, which stays on the chart marked closed
to this sign-in, with the reason ("Bastion admits only people who sign in through its
own directory. You signed in with a password."), and answers as a sector that does not
exist. Two people are exempt from a sector's declaration. A super administrator is
exempt in every sector: the holder of the role the framework defines in every auth
(`SuperAdministrator`, in no role file), which the governor holds. His password session
charts Bastion, where the dispatcher's and the archivist's, who hold global roles and a
role there, do not; the rigger's session he starts by viewing as her enters it, and his
own started by the marshal does not. The same person answers for who makes a super
administrator: the marshal, viewing as the governor, is refused the role for herself.
A sector's own marshal is exempt in that sector alone, so that a sector whose directory
has a problem can be mended by the people who run it: a person who holds `SectorMarshal`
at Bastion itself (the role the crew's role file names as a sector's administrator's,
which the crew's permission engine is built with, `access.WithTenantAdministratorRoles`),
by a role of their own or through one of Bastion's own groups, enters Bastion signed in
with a password and the code sent to their email, or from a browser they asked to be
remembered. The way in is that narrow: a marshal in every sector, as the fleet's own
team is seated, is kept out with everyone else, which is what the switch is for; so is
Bastion's marshal signed in through the fleet's Workspace, another sector's directory;
the role taken away closes the door at the next request; and the same person's password
session reaches their other sectors by those sectors' own declarations. Nor does the
fleet's own team make the way in theirs: Headquarters, which gives the crew's roles for
the whole fleet and manages the accounts no sector manages, is refused giving anyone,
itself included, the marshal's role at Bastion while Bastion is directory only, a place in a group of
Bastion's that holds it, and the changes to the account of the marshal Bastion has that
would hand over her sign-in or shut her out (a password, a sign-in name, an address, an
identity unlinked, the account disabled, enabled or deleted), each with a sentence that
says who does it:
one of Bastion's marshals, or a super administrator, as the governor is. Headquarters
reads those sentences on her account where no sector manages it, as when she holds a role
in a second sector and signs in through no sector's directory; a crew account a sector
manages is not Headquarters' to change at all, a marshal's or anyone's. Headquarters
is not shown those actions on the row of a directory-only sector's marshal: the row's
menu leaves them out, as the server would refuse them. Nor is it shown the removal of
the marshal's role there: not from the marshal, on the rows of the roles people hold and
of Break glass, and not from a group of the sector's own, on the rows of the roles the
group holds. A marshal of Bastion who changes
her password goes on in the session that made the change, a
password and the code still, so the change does not close her out of the sector she
administers. The data level's roster
keeps every sector's declaration beside its key (`resource.TenantRoster.Declarations`,
handed to `resource.SessionPermissions`), the records' write paths reload it after their
commit and signal the other instances, so a declaration decides the next request, and
the records' change events are the audit of every edit. The seed makes the fleet's
directory Cinder's, the one sector it can belong to, and makes Bastion directory only,
with the persona provider of the test environments (`oidc`, `persona`) as the directory
it is only of: the rigger charts Anvil alone, with Bastion marked closed, the bosun is
refused at sign-in, the navigator, who came through the fleet's directory, reaches
Cinder and every other sector that seats him and is not directory only, and a persona
of the cast, who came through the persona provider, enters Bastion. The switch is the
sector's own, turned by its marshal with two generated methods in the sector's scope,
which `SectorMarshal` holds beside the sector's directory: `MakeSectorDirectoryOnly`
(`POST /console/api/sectors/{sectorID}/make-sector-directory-only`) turns it on and
`EndSectorDirectoryOnly` (`.../end-sector-directory-only`) turns it off, each with an
empty body. The `Sectors` record, which headquarters writes in the global scope, shows
the flag and takes no write of it: a patch that carries it is refused, and nobody
creates a sector directly (a sector is directory only from the start where the
invitation that makes it says so, accepted through the directory it will be only of).
Turning it on is held to three rules (`resource.TenantDirectoryRules.DirectoryOnly`): the
sector has its directory; the person turning it on is signed in through that
directory themselves, which shows the directory works and that they are not shutting
themselves out; and the person has a password of their own, which is their way back in
if the directory has a problem (`PasswordsBy`, handed the crew's
`identity.Manager.CallerPassword`; an auth with no password method is asked nothing).
Each refusal is a sentence that says what to do, on the method, on its dry run and on
the warning: a marshal who has only ever signed in through a directory, as the warden
has, is told to choose a password on Your account first, and does, on a code mailed to
his confirmed address (the persona provider's sign-in confirms no address, as no
provider's word but Google's does, and the address the cast gave his account is one
nobody has confirmed, so the account is told to add an address and confirm it first;
`POST /console/api/user/first-password/code`, then
`/console/api/user/first-password` with the code and the password; refused for an
account that has a password, which changes it with the one it has, and under a view of
someone else). Turning it off is
plain; while the switch is on, the sector's routes, these among them, answer the people
the sector admits, so a sector whose directory has stopped working is opened again by
its own marshal, signed in with a password and the code, or by a
super administrator. The marshal, who does not read the `Sectors` record, reads the
switch in the sector (`GET /console/api/sectors/{sectorID}/sign-in-settings`, under the
grant of any of the sector's own sign-in methods). `MakeSectorDirectoryOnly` declares a warning
(`@warning`'s mechanism, the word `close`): asked for it (`X-Warning: true`), the method
answers what the call would do in place of running, the sentence with the count of the
people seated in the sector who sign in another way (it says too that an administrator
of the sector who has a password can still sign in with it and a code sent to their
email, and counts no such person among them), their names as a list where the
caller may list the sector's people (List on the sector's people lists and on their
`username` and `name` fields), and the words on the button; a caller who may list only
some of them is told so in the sentence, and one who may list nobody is told how many
and no names. Where a sector's own marshals keep a way in with a password, the answer
carries `also` as well: a note that counts the marshals of the sector who have a
password, and a note that counts the invitations not yet accepted that give a role that
administers the sector, each with names only for a caller who may list them. While it is
on, the sector's People page says who
it keeps out, on the row of each person with no sign-in through the directory ("Has not
signed in through this sector's directory, so cannot enter while this sector is
directory only"), but for a marshal of the sector itself who has a password, whose row
says nothing; the list keeps everyone who holds a role in the sector, whether or not they
can enter, so nobody disappears from it. Every sentence that speaks of a sector's
directory says it in words a person knows, the name the crew's sign-in registrations give
it (`example`, the fleet's Workspace; `persona`, the persona provider), and never by a
connection that is an id (`a.CrewIdentity().DirectoryName`, which the generated frames
pass to the rules and to the sector list). A
sector its own creator charted takes no directory, whichever way its creator signed in:
its marshal takes the organization's directory afterwards, signed in through it, on the
sector's Sign-in page; the pioneer signs up with a personal account, so Ember starts with
none and admits her as it admits a password. Where an application holds a person to one
sector, the create-sector route sets the directory its creator came through with the
row, through the step every such write takes (`app.SetSectorDirectory`), and refuses a
creator whose organization has a sector already; Lodestar's served suite builds such a
stack to prove it. A sector an invitation makes gets its directory from the sign-in of
the person invited, held to the same rules. The directory's row carries a default seat
(`SectorDirectory.DefaultSeat`, `@directoryDefaultSeat`): the role a person is given in
the sector when they sign in through its directory and hold nothing in the sector, at
every sign-in. A stranger the directory
vouches for is created seated, before the registration's account rule is reached:
Cinder's row names `SectorCrew`, so the navigator's first sign-in creates him seated
there; with no seat named, a stranger is refused by invitation only (`invitation_only`,
the login page's sentence) and no account is created, unless they followed an
invitation. A person whose account stood before (a password persona, or someone another
sector invited) is given the seat at their next sign-in through the directory, where they
hold nothing in the sector: a seat a marshal removed is given again at the person's next
sign-in, and a role with no privileges keeps a person out. The crew auth reads the
sectors' directories with their seats through the generated
`app.NewSectorDirectoryReader` (`SetDirectoryAdmission`, with the seat written by the
auth's generated identity client in the sign-in's transaction,
`crewidentity.Client.DirectorySeated`); the members auth, whose directory names the
roles, sets none. How a sector admits its directory's
people is the sector's own to say as well, with two more generated methods
`SectorMarshal` holds: `OpenSectorToDirectory`
(`POST /console/api/sectors/{sectorID}/open-sector-to-directory` with
`{"role": "SectorCrew"}`) names the seat, behind a warning that says who will enter and
with what role (the word `open`), and holds the role to one the caller could give
directly (a marshal may not give `SectorMarshal`, so she may not make it the role
everyone from the directory gets, and the list the role is picked from,
`CrewSectorDirectoryRoles`, does not offer it to her: it lists the sector's roles she
may give, by the grant of the method that gives one); `MakeSectorInviteOnly` clears it.
The row carries one more word of the sector's own (`SectorDirectory.AsksSecondFactor`,
`@directorySecondFactor`), which the record shows and never writes. The record's own
update never writes the seat. The console's Sign-in page (Sector Ops menu, under the
sector's URL) carries the declaration: a marshal signed in through his organization's
directory makes it the sector's there with one button and removes it once he confirms;
a marshal signed
in with a password is told how a directory is set and offered no button. Everything
else on the page is one of the six methods, run through the library's method dialog:
opening the sector to its directory, with the role picked in the dialog beside what
each role is for, and making it directory only each show the server's warning, with the
people it names as a list, and hold the button until the word is typed; making it
invite only and turning directory only off are plain confirmations, saying that the
directory asks for a second factor shows the server's warning and holds the button until
`vouch` is typed, taking that back is a plain confirmation, and a refusal is
shown on the page with no dialog. What turning directory only takes of the marshal, a
sign-in through the sector's directory and a password of his own, is said on the page
before he presses, in the server's sentences, and the button is offered only where the
sector has a directory. The first
person into a sector is its first marshal however it is made: an invitation's first
sign-in seats the invited person as `SectorMarshal` in Dawn in the transaction that
writes the row, as the create-sector route seats the pioneer in Ember (the roles
`crew.AdministratorRoles` names, written by the crew's generated identity client),
and the last holder of a sector's administrator role (`SectorMarshal`,
`identity.WithAdministratorRoles`) is refused leaving it until another holds it, so
headquarters brings a customer's sector in by inviting its first administrator and
holds no seat in it. The
whole service is kept by the Super Administrator role instead: only a holder makes or
removes another or changes how another signs in (headquarters, which manages the accounts
no sector manages, neither disables nor enables a holder's, sets no password, username
or email address on a holder and unlinks none of their identities), giving the role ends
nothing the person holds (their sessions, tokens and remembered browsers stand, and the
code is asked at their next sign-in), a holder gives and takes any role
of the crew whatever the role file
grants (`access.WithSuperAdministratorMethods`, handed the generated
`crewidentity.SuperAdministratorMethods`), and the last holder who can sign in keeps the
role and the account, whoever asks. Demonstrates: tenancy.declaration,
tenancy.directory-only, tenancy.creator-directory, tenancy.default-seat,
tenancy.directory-second-factor, tenancy.creator-seat, auth.super-administrator.

## The personas

All crew passwords are `follow-the-lodestar-home`; the login is the job word. The login page is the crew
manifest: pick a card, sign in, switch, never more than two clicks. The login is the
persona's username; the id of its account is in `cmd/bootstrap/users.json`, and it is
the id, not the login, that a role method, a view-as session and the rows of the demo
world name the persona by. On the pages nobody types an id: the roster's "view as" lists
the sector's people by username, and a record that names an account declares a picker
over the auth's people (`@enumerate(CrewSessionUsers)` on a pilot's account, the crew's
accounts; `@enumerate(CrewSectorUsers)` on a squadron member, the sector's people), which
whoever may add the record may list. The marshal views the console as the cadet with
`POST /console/api/impersonate` and
`{"kind":"view","principal":"f0000000-0000-4000-8000-000000000003"}`, the id of the
cadet's account (a username there is refused, 400); the session route and the watch
desk then name the cadet by username; and
`{"kind":"role","principal":"Dispatcher"}` operates the session as a role.

Impersonation is granted with a mask: to let someone impersonate a person with no mask
is to give them what that person holds, and the mask is the control. The console
declares three kinds (`app/impersonate.go`, `session.NewImpersonations`), each with the
grant that allows it and the widest mask that grant gives, and the role file gives each
grant on purpose. The declaration is the only way to start an impersonated session: the
mint route takes its request from it (`Impersonations.Request`), and a request written
out by hand does not compile:

| Kind | Grant | Role | The widest mask |
| --- | --- | --- | --- |
| `view`: view as a person | Execute on `ViewAsUser` | `CommandStaff` (the governor, the marshal) | Reading only |
| `act`: act as a person | Execute on `ActAsUser` | `StandIn` (the governor) | Everything except the actions classed `credential`, the actions classed `access` and the writes of the records that decide what a person may do: the session cannot set, require or remove a password, send a reset link, unlink a sign-in identity, or change a username or an address; it cannot give or remove a role, change a group or what it holds, invite anyone, make an account, change who may sign in (a sign-in method opened or made invite only, a sector opened to its directory, a sector's directory-only switch, the crew's reset link at the sign-in page), create a sector of the person's own (`CreateSector`), or rate a pilot (`SetSectorPilotClearance`, which Lodestar classes `access`); and it cannot add, change or remove a pilot's registry row, a certification, a client's contact or a place in a squadron, change a squadron, whose row says which wing its members are in, or delete a sector, which takes every role held in it. It keeps one of those records, the hangar passes: it gives, changes and withdraws a pass, for anyone but the real person and the squadrons they are in |
| `role`: act as a role | Execute on `AssumeRole` | `CommandStaff` | Everything the role holds |

A request may ask for less than its kind gives: permission kinds to keep
(`"mask":["List","Read","Execute"]`), classes to refuse beside the kind's
(`"refuseClasses":["irreversible"]`, the class Lodestar marks `ScrapShip` with) and
actions to refuse by name (`"refuseActions":["ScrapShip"]`). A request for more than its
kind gives (`"kind":"view"` with `Execute` in its mask) is refused, 403, with nothing
started, and so is `"kind":"act"` from a holder of `CommandStaff` alone: the grant to
view is not the grant to act. The answer, the session route's `impersonation.maskWords`,
the banner and the watch desk say the session's mask in words ("reading only", and for
the `act` kind "everything except access actions (still allowed: writing HangarPasses)
and credential actions"), and so does the generated list of
the impersonation records (`crew-session-impersonations`), whose `mask` column reads the
stored text as words.

Whatever the kind and its mask, nobody is the subject of what they do in such a session.
The marshal who acts as the `Headquarters` role holds, for the length of the session,
everything headquarters may do to the crew's accounts, and is refused each of those
things on her own account ("You are acting as someone else, so you cannot give yourself
a role."): a role, a place in a group, a password, an address, an
enable, the unlock of her own name, an invitation to her own address. The same act on
anyone else goes through, and her own row is still listed. The rule is the identity
library's and needs no line in the application. An action of Lodestar's own that names a
person asks it too: `SetSectorPilotClearance` reads the pilot's account off the registry
and calls `identity.RefuseImpersonatorAsSubject`, so nobody raises their own clearance
through someone else's grant.

The rule covers the records that decide what a person may do, and again Lodestar writes
no line for it. A `Pilots` row carries the clearance and the fee limit the policy reads
as `subject.clearance` and `subject.feeLimit`; a `PilotCertifications` row, a
`SquadronMemberships` row, a `HangarPasses` row and a `ClientContacts` row each put a
value in one of a person's subject sets. Each carries a `@subjectValue` or a
`@subjectSet`, so its generated write asks the rule about the person the row names,
before the write and after it. The cadet who acts as the `Headquarters` role, whose
mask refuses nothing, is refused a higher clearance on their own row ("you are acting
as someone else, so you cannot change your own pilot record"), that row handed to
someone else or removed, a certification of their own, and a veteran's row moved onto
their account ("make another pilot record your own"); the same write for another pilot
goes through, and in a session of their own a person writes their own row as they
always could.

The `act` kind goes further than the rule, which holds the real person alone. Its mask
refuses the class `access`, for what the rule cannot see (a role that reaches a person
through a group or a colleague), and the generator gives that class to the writes of
those five records, to the writes of a place in a group of the crew (a record the
application offers no write of, since the identity library's own actions write it)
and to the change of a squadron, whose row says which wing each
member is in, and to the delete of a sector, which takes every role held in it, so the
kind names no record and a record that gains a subject value is
covered when Lodestar is generated again. A stand-in therefore changes no role, no
group and no record that decides what a person may do, but for a hangar pass, which
the kind keeps, and that is what the `StandIn`
role's description promises: everything the person may do, except change how they sign
in or who may do what. Two things stay outside it, each stated where the kind is
declared: a stand-in can still disable, enable and delete another person's account,
which carry no class, and delete a hangar, which removes its passes with it. `SetSectorPilotClearance` carries the class `access` for the
same reason, so the kind refuses the action as it refuses the record's write. A mask
names a record's permission and not one plain field of it, so the squadron's name and
callsigns are refused with its wing.

Creating a sector is among what the kind refuses. The generated `create-sector` route
takes no grant, so in a session that acts as a person it would make the person acted
as a new sector's marshal, with no permission checked for a mask to deny. The route
carries an action's name for that, `CreateSector`, classed `access`, and asks the
session's mask before anything else: the stand-in who acts as the pioneer is refused
("You are acting as someone else, and this session cannot create a sector.") with
nothing written and the denial logged as the mask's, with the class it refuses, and the
route's `GET` answers that none is offered. The `role` kind is refused
the same route by the rule and not by a mask, since it has none: the marshal behind a
session that acts as a role would become the sector's marshal herself ("you are acting
as someone else, so you cannot create a sector of your own"). An application that
declares a kind of acting as a person with no mask allows the creation, and the person
acted as is the sector's marshal: that is what no mask means. `CreateSector` is an
action no role is given: nothing asks a role for it, so the generated collection marks
it (`NoRole: true`), a role file that grants it is refused, with the reason, by the
permission engine and by `impulse check`, and the console's generated `Methods`
constants leave it out.

The rule also sees a row that reaches a person without naming them. The marshal who
acts as the `Headquarters` role is refused a role, a pass or a place in a squadron
given to her own account, and she is refused a hangar pass given to a squadron she is
in, and her own squadron moved to another wing ("You are acting as someone else, so you
cannot add a hangar pass record for one of your own squadrons."). The generated writes
of `HangarPasses` and `Squadrons` name the column and the set, and the library asks the
set by its own declaration, so the rule and the policy agree on who is in a squadron.
A squadron's new name asks nothing: only a change that sets the wing does. The `act`
kind keeps the writes of `HangarPasses` out of its refusal of the class `access`
(`accesstypes.RefuseClassExcept`), because giving passes is the marshal's daily work:
a person standing in for the marshal gives a pass to another person or another
squadron, is refused one for themselves or a squadron they are in, and is refused
everything else the class holds. The banner and the watch desk say the exception in
words ("everything except access actions (still allowed: writing HangarPasses) and
credential actions"), a request to start the session may drop it (`refuseClasses:
["access"]` with no `exceptWrites`), and a request that names a record the kind does
not keep is refused with nothing started. What the rule cannot see is a role given to
a group she is in: that stays open to a session whose mask does not refuse the class
`access`, and it is a stated limit of the rule. A hangar pass also goes with its hangar
or its wing when one is deleted, through the pass's own delete, so the rule is asked
about it there: the delete of a hangar or a wing is refused whole when a pass under it
is her own, her squadron's or her group's, under either kind (proven for her own pass
and her squadron's in `test/integration/impersonation_taken_skipauth_test.go`). A pass
that goes with its sector is not asked about: a sector's delete asks nothing of a row,
the `act` kind refuses it by its class, and no role the release ships deletes a sector.

| Login | Who | What their view proves |
| --- | --- | --- |
| `governor` | Governor Greer, headquarters | The fleet's super administrator (`SuperAdministrator`, the role the framework defines in every auth): the one person no sector's sign-in settings shut out, who gives any role of the crew to anyone and whose role and account stay while he is the last who can sign in. Beside it, every global role and Sector Marshal in all three sectors: the pruned pure-RBAC baseline. Keeps the watch desk over every session held as someone else, which says what each may do, and can revoke one, as the marshal does (the command staff's grants). Holds each grant to impersonate on purpose: `CommandStaff` (view as a person, reading only; act as a role) and `StandIn` (act as a person: everything they may do, except change how anyone signs in or who may do what). On the console's Break glass page he takes a role in a sector for a set time (1 hour, 4 hours, 8 hours or 1 day) and says why (`take-crew-role`): the role is his from then until the time is over, the sector's Marshal reads the reason beside it, and whoever may take the role away ends it early; "Held right now" and "Ended" list the roles held that way. Headquarters holds the grants, and taking needs what giving himself the role would. Demonstrates: impersonation.active-list, impersonation.revoke, impersonation.act-as-role, impersonation.act-as-user, impersonation.grant-mask, computed.pushdown, auth.reasons. |
| `marshal` | Marshal Maren, Anvil | Full sector authority at Anvil, nothing at Bastion or Cinder (the fail-closed border); every transition including Scrap; the row-free `now` condition on IssueBulletin; the sector briefing with every fee; the sector's lists of the crew (its people, the roles held there, its invitations), with a person's name, address and whether the account is disabled answered for everyone, and the rest of the account only where the sector manages it (`managedHere = true`); the roles the sector picks from, every one but the Marshal's own (`name != 'SectorMarshal'` on a computed list, judged over the rows its function yields); keeps the watch desk with the governor, on the command staff's grants: lists every session held as someone else, reads who each is held as from the crew's accounts by username alone, and revokes one; releases cargo on a grant that carries no condition, so the method's own rule is what refuses her a second release (409, with the day the cargo left bond); attaches mission documents through the `@upload` method and downloads them through the generated file route, whose gate is her Read grant on `content` (the client portal lists documents and holds no such grant); views the console as another crew member, reading only (`CommandStaff`), and is refused acting as one, which is a grant of its own. Demonstrates: tenancy.concealed, @transition.multi-from, condition.now, rpc.client-form, @upload, @file.stored, impersonation.view-as, auth.tenant-lists, computed.row-condition. |
| `cadet` | Cadet Cass | `hazard IN (1, 2)`; the flight deck with only Claim lit; the two-input distress-call form (create-form narrowing). Demonstrates: execute-condition, create-form-narrowing, capability-envelope. |
| `pilot` | Pilot Pax, clearance 3 | `hazard <= subject.clearance AND (requiredCert IS NULL OR requiredCert IN subject.certifications)`; `hangarZone != 'quarantine'` on ships and on HailShip, the touch that answers No Content. Demonstrates: @subjectValue, @subjectSet.global, @attribute.nullable-fk, @attribute.join-path, touch, @answers.no-content. |
| `veteran` | Veteran Vela | `NOT (hazard IN (1, 2) OR fee < 5000)`. Demonstrates: condition.prefix-not. |
| `lead` | Flight Lead Lior, Hammer | `assignedSquadron IN subject.squadrons OR bookedBy = subject`; launch, hold, resume, complete, fail; sorties only while underway. Holds Execute on HoldMission but no Update on the notes, so the armed hold refuses inside the transaction in the grant's words, and the dry run of every lit edge says so before anything is touched. CompleteMission posts the settlement through the Paymaster role's checker (`caller.As`) and chooses its status (`@answers(200, 409)`). The expense manifests of the missions Hammer flies, and no other squadron's: the same subject set on a `@computed` list (`assignedSquadron IN subject.squadrons`), where the library reads his squadrons once for the request and judges each manifest the list's function yields, through the list, the read and the rendered file. On the console's Expense manifests page the Flight Lead is offered no search box and no sort: the server searches and orders a computed list by its rows' real values and refuses both by a field a role sees on some rows only, so the page offers neither and says why ("You cannot search this list: your role does not always see Mission."). Demonstrates: @subjectSet.domain, condition.subject-scalar, rpc.armed-write, rpc.dry-run, rpc.as-role, @answers, @transition.loop, create-under-parent, computed.subject-fact. |
| `dispatcher` | Dispatcher Dunn | `state NOT IN (...)`; two Update grants on one resource: `new.assignedSquadron IN subject.squadrons` and `new.deadline >= deadline`, the write-grouping demo; compiles the briefing without the hazard board; sees the sector's own crew, the roles the sector itself gave (`everyDomain = false` on the roles held in the sector). Demonstrates: condition.not-in, write-grouping, condition.old-vs-new, rpc.decision-as-data, auth.tenant-lists. |
| `overseer` | Overseer Orla | `deadline < now` as a right-side operand; the reassign that unlocks by the clock (the seeded three-minute mission). Demonstrates: condition.now, @attribute.timestamp. |
| `booking` | Booking Agent Bex | `new.fee <= subject.feeLimit` on create and inside an Update; `fee > 10000 OR bookedBy = subject`; delete by base decision; Stand Down from three sources. Demonstrates: @subjectValue.two-per-anchor, @attribute.decimal, @transition.multi-from. |
| `wingco` | Wing Commander Wilde, Forge Wing | `wing IN subject.wings`, a subject set with a dotted value path; `hazard >= 4`; rates the pilots who fly for Anvil alone: the clearance field of the sector's pilots, a view, and `SetSectorPilotClearance`, a method whose target is a row of that view, each under `fliesElsewhere = false`, a fact the view's query computes (over the API; the console draws no page for it). Demonstrates: @subjectSet.dotted-value, @attribute.view, @target.view. |
| `engineer` | Engineer Ezra | The refit workflow with its failed-test loop on a join-path root; `inspectedAt IS NOT NULL`; interleaved compound-key tasks; the Hail touch. Demonstrates: @transition.join-path-root, @transition.loop, interleaved-table, compound-key, client-supplied-key, transition-owned-timestamp, @defaultsUpdateType, @validateUpdateType. |
| `quartermaster` | Quartermaster Quill | `state = 'underway'` evaluated two hops deep on SortieExpenses. Demonstrates: @stateRoot.two-hop. |
| `supercargo` | Supercargo Sol | `releasedAt IS NULL` on Update and on ReleaseConsignment (shared with the droid, which reads the manifest armed and answers a receipt), so a second release is the frame's Forbidden and a released row answers no Execute; cargo is released once for every role, since the method refuses a consignment that carries its release; `expiresOn < '2026-09-01'`; `allow_filter` on Mass; the hold walked by release date across the NULL boundary. Demonstrates: @attribute.date, allow_filter, paging.nullable-sort, rpc.armed-read, rpc.typed-result, outlet.shared. |
| `salvor` | Salvor Sable | `insured IS NULL OR insured = true` on Clients, the salvage desk's one grant: the seed leaves Halvard covered, Meridian and Bastion Relay undecided, and Vellum refused, so she lists the first three and never Vellum, disagreeing with the trusted view in both directions; `= true` alone, or `!= false`, would drop the undecided outfits (the semantic differential proves that half on the same shape). Demonstrates: @attribute.nullable-bool. |
| `yeoman` | Yeoman Yael | The standing orders, a `@computed` struct with no `@primarykey`: a whole read-only list, served in the book's own order on a bare GET, sorted by section on request, never paged (a `limit` or a `cursor` is refused with a 400 naming the key as the way to page), with no read route and no row identity. Her first screen is the console's Standing Orders page, the library's list over that resource: the whole book as one page with virtual scroll, every line identified by its position, Previous and Next disabled with the count, no View column, no create, and no row route; a `pageSize` or `enableRowExpansion` on its config fails at startup naming the resource. Demonstrates: computed.keyless, list.keyless. |
| `purser` | Purser Priya | The expense manifests, a keyed `@computed` struct whose struct-scope `@file` renders each mission's booked expenses as a `text/csv` sheet on request: `GET .../expense-manifests/{missionId}/content` under her Read grant on `content`, with the sheet's digest as its validator, so a kept copy asks again and hears 304 until an expense is booked; the marshal reads no manifest and the cadet none of it. The console's Expense manifests page is the library's list over the same resource: the Purser searches the manifests by mission and sorts by any column. Demonstrates: @file.rendered. |
| `registrar` | Registrar Rhea | The document register at Anvil: Update on the documents' `title`, Delete on the documents, and Execute on `ReplaceMissionDocument`. A replaced file points the row at the new object and the old one leaves the Documents store once the transaction commits; a deleted document's object goes with the row; a dry run of the replacement, a refusal, and a retitle release nothing. No frame and no body does this: the patch machinery records the released keys with their store on the transaction, and the resource client, on which the Documents store is wired with `resource.WithNamedFileStore[resources.Documents]`, deletes them from it after each commit. Demonstrates: @file.released, @file.replaced. |
| `photographer` | Photographer Petra | The hangar's camera at Anvil: Execute on `AttachRefitPhoto`, an `@upload` with no `store:`, and Read on the refit tasks with `photo`. Her photo streams to the default store (`APP_FILE_STORE`) and its key is a plain string in `RefitTask.PhotoKey`, where the marshal's briefs stream to the Documents store (`APP_FILE_STORE_DOCUMENTS`) under keys typed `resource.Key[Documents]`: one application, two stores, each key column typed for its own, so a key of one does not compile into a column of the other. The walkthrough shows the two directories holding what each row names, and `cmd/jobs cleanup-files` removing one planted orphan from each while the brief and the photo stay. Demonstrates: @upload, filestore.named, filestore.cleanup. |
| `archivist` | Archivist Ada, all sectors | Terminal-state rows; fee and settlement redacted until completed (two read grants on one resource) and sorted over the visible projection, while the deadline, declared `masking:"positional"`, orders every page on the real column; a fee sort her grid does not display pages on the cursor's copy of the fee; the deploy warns that her fee filter sorts the partition; PII withheld; the domain-scoped ship's log; a briefing that counts her redactions. Demonstrates: cell-masking, paging.masked-sort, paging.unselected-sort-key, masking.positional, warning.concealing-key, pii, @manualAddResource.scope, rpc.armed-read. |
| `assessor` | Assessor Asa | Prices cover before launch: one List grant on Missions, the title unconditionally and the hazard level under `state = 'open'`. Hazard is a named variant of INT64 (`type HazardLevel int64`), concealing and unindexed, so a grid that sorts by hazard without displaying it pages on the cursor's copy of the visible hazard, decoded into the field's own type where the Spanner client refuses a pointer to a pointer to it, and the missions no longer open walk through the NULL region in Spanner's placement. Demonstrates: paging.named-variant-key. |
| `hazards` | Hazard Analyst Hale | A conditional (row-free `now`) grant on a computed resource, the whole board through `limit=all`. The board's search box and its Ship sort and filter are offered to the analyst: the digest lists the grant as `conditionalOnTime`, one that depends on the time alone, which the server settles at each request. Demonstrates: computed.conditional-grant, paging.limit-all, computed.fold. |
| `dock` / `watch` | Dockmaster Dara / Night Watch Nadia | `timeOfDay(now, local)` and the wrap-around `timeOfDay(now, 'America/Denver')` window; `dayOfWeek(now, local) NOT IN ('sat', 'sun')`. At any hour exactly one sees the hangar deck. Demonstrates: condition.time-of-day, condition.day-of-week, condition.local-zone. |
| `harbormaster` | Harbormaster Hollis, Anvil | List and Read on Ships, nothing else of her own: the live fleet board. Her list and the ship she has open are subscribed when they are read, the engineer's refit (or a hail) lands on both without a reload, a board left and reopened inside five minutes is the browser's own, and the hazard analyst watching Cinder's fleet and the cadet, who holds no List on Ships, receive nothing. Demonstrates: live.pages. |
| `armorer` | Armorer Arlo, Tongs | No pilot and no SectorCrew: his whole view of the hangars is the two sets the hangar passes yield, `hangar IN subject.hangarsOpen` on List and Read and `hangar IN subject.hangarsRun` on Update. A pass names one holder, a person, a squadron or a group, at a level, until it expires, and the set's own definition says which passes are his (`who: UserID = subject OR SquadronID IN subject.squadrons OR GroupID IN subject.crewGroups`, a set that follows the squadron set and the crewGroups set) and which of those count (`where:` the level, and `ExpiresAt IS NULL OR ExpiresAt > now`). He holds no pass that counts in his own name, so everything he reaches he reaches through Tongs: he lists Anvil Dock One, which Tongs runs, and the Quarantine Bay, which Tongs visits; he re-zones the dock and is refused the bay, which Hammer runs and his own expired pass no longer opens. On the Hangars page he sees those two hangars and no button to add one; the passes are the Marshal's to list and write, on the hangar's own page. Demonstrates: @subjectSet.who, @subjectSet.where, @subjectSet.on-set. |
| `sentry` | Sentry Sol, the Bay keepers | The armorer's role and none of his ties: no pass in his own name and no squadron. The Bay keepers, a group kept in Anvil, hold a pass to run the Quarantine Bay, and the sentry is in the group, so the bay is the one hangar he lists and his to re-zone; Anvil Dock One, which no pass of the group's names, is not on his list. The third holder is one more test in the same two sets (`OR GroupID IN subject.crewGroups`), and the crewGroups set is the one the generated members declaration yields, kept to the request's sector by its `domain: GroupID.DomainID`: a pass that named Bastion's group, or a group kept for the whole fleet, would open nothing in Anvil, so none is written: the pass form's picker offers neither, and the library refuses a request, or a line of the bootstrap's file, that names one ("groupId names a group that is not one of this sector's own. Choose a group kept in this sector: a group kept in another sector, or for the whole application, cannot be named here."). A person put in the group reaches the bay at their next request and one taken out loses it at theirs; a group that is deleted takes its passes with it. The Marshal gives a group a pass on the Hangars page: a hangar's page lists its passes, and Add a pass opens a dialog whose three pickers (a person, a squadron, a group of the sector) are the lists the generated metadata names, a person read by name with the login beside it ("Armorer Arlo - armorer"), as the list of passes reads its Person column; two holders, or none, is refused in the dialog in the server's words. Demonstrates: @subjectSet.on-groups, list.create-dialog. |
| `deckhand` | Deckhand Dev, the deck crew | No role in a sector of their own: Anvil's deck crew, a group kept in the sector, holds `Cadet` and `SectorCrew` there, and the deckhand is in it, so the deckhand's board is the cadet's and Bastion is concealed. A group holds roles the way a person does, and whoever is in it holds them wherever the group does, at their next request; the Marshal makes and manages the sector's groups on the Groups page, whose list says how many people each group has and which roles it holds and offers Delete only on a group that holds nothing ("1 person and 2 roles: empty it first" on the deck crew); taking a role away from a group says how many people lose it; a person's page has one list of roles that says "Their own" or names the group each comes through, with a link to the group's page; and the sector crew see the groups they are in themselves (`group IN subject.crewGroups`, the set the generated members declaration yields). Demonstrates: auth.groups, auth.holdings. |
| `relief` | Relief Rae, on a tour of Anvil | `Cadet` and `SectorCrew` at Anvil for a tour: thirty days from the bootstrap, each role given with an end and the reason for it (`tours` in `users.json`: "Covering Anvil's deck while the hangar is refitted"), which the Marshal reads beside the role and its end wherever Rae's roles are listed. While the tour lasts her board is the cadet's; once it is over the roles grant nothing, Anvil is gone from her sectors and from the sector's list of its people, and nothing was written at that moment: everything that reads a role leaves out one outside its period. The rows themselves go when she next signs in, through the hook the crew auth sets for a sign-in that stands (`crewidentity.Client.SignedIn`); no job runs on a schedule. The Marshal gives a role, a place in a group or a group's role for a period on the same pages she gives them on (the Starts and Ends fields), gives a new period by giving again, and ends one now by taking it away; the lists of roles show the period. The Super Administrator role takes none. Demonstrates: auth.periods, auth.reasons. |
| `adjutant` | Adjutant Alba, headquarters | The commendations desk, the one resource behind a feature flag, and the key that turns it: `FeatureAdministrator` (List and Read on `FeatureFlags`, Execute on `SetFeature`, global) beside `Adjutant` (List, Read and Create on `Commendations`). Off, the desk's routes answer the router's own 404, the digest leaves the desk and the card's `commendations` field out, and the field named in a request is unknown; she turns the flag on through `SetFeature`, the desk answers, Pax's card counts his citations, and a second server process serves the desk at its next request with no restart; off again, both refuse. Demonstrates: @feature, @feature.field. |
| `surveyor` | Surveyor Shen, headquarters | Charts new sectors by inviting the person who will run each: `Surveyor` (Create and Update on `Sectors`, the invitation's method, the list of the invitations that wait and the list its starting role is picked from, global) beside `SectorCrew` held in every sector. Nobody creates a sector directly, so her Create grant is what her invitation's details are checked under, and she names no starting role: she may give no role, so the invitation's pick offers her none. Her grant on the invitation's method holds only while the invitation does not say directory only (the condition `new.directoryOnly = false` in the role file): closing a new sector to everything but its directory is Headquarters' to say. `Sectors` is the tenant record (`@tenant`) and every instance holds a roster of it: when the person she invited to found Dawn first signs in, the instance that took the sign-in makes Dawn, adds it to its roster after the commit and signals the tenants kind, so her place switcher lists Dawn and Dawn's wings answer her at once, with no restart, no migrate job and no new login; the marshal's does not list it and Dawn answers her as a sector that does not exist; the client's portal, the governor and the archivist, all held in every sector, see Dawn with nothing written for them; and a second server process serves Dawn at its next request. Demonstrates: tenancy.run-time-tenant, tenancy.invited-tenant. |
| `pioneer` | Pioneer Pia, on no manifest | Signs up with any Google account (the crew auth's `sign-up` registration, once the governor has opened it to sign-up on the console's Sign-in page: no roles, the application the authority), a crew login holding nothing, and charts her own sector through the generated `create-sector` route: who may chart one is apart from who may enter one and is headquarters' choice in the running application, so while the sign-in method she came through is open the route, given the sector's fields and no key, writes Ember under an id it generates and answers, seats her as its marshal (`SectorMarshal`, the roles `SectorAdministratorRoles` names) and hands it to the roster in one call, and her chart lights Ember at once; a second sector is refused her, as the route is refused the marshal and the governor, who hold sectors already. Sign in first, then name the sector. Demonstrates: tenancy.self-service, auth.open-sign-up. |
| `rigger` | Rigger Rook, Anvil and Bastion, no global role | `SectorCrew` in Anvil and in Bastion, by password, and nothing global: Bastion admits its people through its directory only (`Sector.DirectoryOnly`), so her chart lights Anvil alone, names Bastion closed to this sign-in with the reason, and Bastion answers her as a sector that does not exist, until a marshal turns Bastion's directory only off and her chart lights it at once. Bastion's People page keeps her row, which says she cannot enter while it is on. Demonstrates: tenancy.directory-only. |
| `bosun` | Bosun Bram, Bastion only | `SectorCrew` in Bastion alone, by password: every sector of his is directory only, so his sign-in is refused naming the rule (`directory_only`), and admitted once a marshal turns Bastion's directory only off. Demonstrates: tenancy.directory-only. |
| `liaison` | Liaison Lark, Anvil and Cinder | `Cadet` and `SectorCrew` in Anvil, `Archivist` in Cinder, nothing in Bastion: the list across tenants. `Mission` declares `@listAcrossTenants`, so its list is also served without the sector's segment (`GET /console/api/missions`), and her one request reads the missions of every sector she reaches in which she may list them, with one filter, one sort, one page and one count across them. Each row is checked under her roles in its own sector and names it (`zzTenant`): Anvil's fourteen low-hazard missions carry no fee and no client, Cinder's closed sweep carries both, and Bastion, where she holds no role, contributes nothing and is never named, asked for by name (`domains=`) or not. The search box and every filter apply in each sector; `MissionsByDeadline` serves the list's order across sectors. Her page is Missions in every sector, in Headquarters: the library's list with `acrossTenants` on its config, the mission's title first, the sector named in the column after it, and each row a link to the mission itself inside its own sector. The link is the mission's own route under the sector's Missions page, which asks Read on the mission and nothing of the mission board that page lists, so her click opens the mission though her sector roles do not list the board; a row in a sector where a person may list missions and read none would draw no link. The config names no permission for the page: the server tells the global digest whether a person may list missions in at least one sector they reach, so the liaison sees the entry in Records and the yeoman, who may list missions in no sector, sees none and is turned away from its route. A list across tenants is plain unless its page asks, and this page asks (`live: true`): her one request registers one subscription over the sectors her list reads, so a mission changed in Anvil or in Cinder reaches her change set as that sector's list and her page fetches once, a mission changed in Bastion never reaches her, and the renewal works her set out again. Demonstrates: list.across-tenants, list.across-tenants.live. |
| `client` | Client Cleo, portal only | Signs in through her company's directory, whose groups are her roles, held in every sector: her session enters every sector that is not directory only, and Bastion, whose directory is not the one she came through, stays on her portal's sector list marked closed to this sign-in; the second browser app over the second TypeScript target; `client = subject.client` from the ClientContact anchor; a conditional Execute fired from a portal session; a PII field an external user writes; the portal-only client statement. Demonstrates: auth.directory-roles, auth.skipauth-directory, typescript.second-target, outlet.session, @subjectValue.second-anchor, @manualAddResource.outlet. |
| `droid-r7` | R7, service account, no login | The droids outlet, behind the crew auth's bearer tokens (the bootstrap creates its account, without a password, and gives it its roles; the server finds the account by its username at start and seeds its configured key as its application token): telemetry with no human route, one reading per call, each carrying the firmware's raw frame, a type declared in the droid link's own package whose generated methods the generator writes there (`WithTypes`); releases through the shared method under its own read grant; the program the release ships to the droids (`cmd/droid`), which sends one frame and upgrades itself from the outlet. Demonstrates: outlet.tokens, outlet.exclusive, machine-identity, rpc.row-free, typescript.types-package, outlet.programs. |

## Where things live

- `pkg/resources`: every struct and annotation (design plan §5), each with `@page` and
  all but the hull catalog with `@order` ([`order.none`](pkg/resources/ship_classes.go),
  a catalog read whole and unsorted, whose pages need a requested sort,
  [`order.required`](pkg/resources/ship_classes.go)); `pkg/rpc`: the thirteen transitions and the effect methods, including the
  client-form [`rpc.client-form`](pkg/rpc/compile_briefing.go) and the nightly prune
  Cloud Scheduler calls ([`@schedule`](pkg/rpc/prune_droid_reports.go)); `pkg/computedresources`
  (the pushdown [`computed.pushdown`](pkg/computedresources/service_ledgers.go), the
  fold, and the key-less standing orders
  [`computed.keyless`](pkg/computedresources/standing_orders.go)) and `pkg/virtualresources` (among them the sector's pilots, a view keyed by the pilot whose row carries a fact its query computes, [`@attribute.view`](pkg/virtualresources/sector_pilots.go), and is the target of a method, [`@target.view`](pkg/rpc/set_sector_pilot_clearance.go)); `pkg/router`: the generated router over three outlets
  (`/console/api`, `/portal/api`, `/droids`; each browser outlet's API under its
  application's mount path) with its chain documented at the top of
  `zz_gen_router.go`, and `hooks.go`, the console's and the portal's own routes composed
  into it and the beacon pulse mounted outside every outlet under the prefix the
  generator program declares quiet ([`generation.request-log`](cmd/generate/generator.go));
  `app/`: wiring, middleware, the ship's log, the client statement, the
  impersonation mint route, the accessors of each auth's generated
  identity client (`users.go`) and the beacon pulse. The mission document download is
  generated from `MissionDocument.StoreKey`'s `@file`
  ([`@file.stored`](pkg/resources/mission_documents.go)), and the purser's expense
  manifest is a computed resource whose struct-scope `@file` renders a CSV sheet on
  request ([`@file.rendered`](pkg/computedresources/expense_manifests.go)). The
  registrar's `ReplaceMissionDocument` points a document at a new file, and the
  resource client, on which the two stores are wired in `pkg/config/data.go`
  (`resource.WithFileStore` for the default store, `resource.WithNamedFileStore` for
  the `Documents` store declared in `pkg/resources`), deletes the object a committed
  transaction released from its store, the replaced file's and the deleted document's
  alike; the photographer's `AttachRefitPhoto` streams to the default store, the
  generated `resources.FileHolders()` names every key column with its store, and
  `pkg/jobs` with `cmd/jobs` runs the orphaned-file cleanup over both
  ([`filestore.cleanup`](pkg/jobs/cleanup.go))
  ([`@file.released`](pkg/resources/mission_documents.go),
  [`@file.replaced`](pkg/rpc/replace_mission_document.go)). No `@file` table's rows are
  deleted by the database, so every deleted row's object is released; `go run
  ./cmd/generate/resourcegenerator -audit` prints the nine `Warning:` lines and no
  `Audit:` line, `go generate ./...` prints the nine alone, and
  `cmd/generate/audit_test.go` pins that the audit pass reports nothing.
- `pkg/telemetry`: the droid link's own package, the one package the generator writes
  into without reading a resource from it: `cmd/generate` names it with `WithTypes`, so
  the frame type a `DroidReports` column holds gets its JSON and Spanner methods generated
  beside it ([`typescript.types-package`](pkg/telemetry/telemetry.go)). The briefing
  catalog's `Layout` is the one field typed `json.RawMessage` itself, a computed field
  passed through unmodelled ([`typescript.raw-json`](pkg/computedresources/briefing_templates.go)),
  and `DistressCall.Position` writes no JSON methods of its own any more
  ([`typescript.generated-json-methods`](pkg/resources/distress_calls.go)).
- `pkg/auth/crew` and `pkg/auth/members`: the two populations, each with its
  `roles.json`, every grant in §7 per auth, embedded in the binary; `pkg/auth/signal.go`:
  the policy signal both engines take, over the live service;
  `cmd/bootstrap/users.json`: the cast, each with the fixed `id` its account is created
  under: the personas (`users`, with their passwords and roles), the clients the demo
  world's rows name (`members`, who sign in through the directory and so carry neither),
  and the droid (`serviceAccounts`, its roles and no password).
- `schema/migrations` and `schema/devseed`: the schema and the world the suites and the
  demo share; one mission's deadline is written as bootstrap time plus three minutes so the
  overseer's grant flips during a live walkthrough. The seed names every person by the
  cast's fixed account ids, with the username beside each column that records who did
  something (the header of `000002_demo_world.up.sql` lists which id is whose).
- `web/`: one bun workspace, two Angular applications: `console/` (default outlet) and
  `portal/` (portal outlet), each over its own generated TypeScript client; the workspace's
  checks are `bun run build && bun run lint && bun run test`; the repository's CI runs all
  three in its `lodestar-web` job against the framework's packages built from the same
  commit, which `web/package.json` links from `../web/dist`; the specs run on Angular's unit-test builder with
  Vitest under jsdom, no browser, the wiring the
  Impulse skeleton carries (the console's dashboard, login, menu file and pilot card, the
  portal's login, menu file and tracker, over a scripted client from
  `@impulseframework/resource-angular/testing`). The console mirrors the API's shape: every
  sector-scoped page lives under the sector segment, `/sectors/{sectorID}/...` (the
  generated `DomainRoutePath`, the segment's value the sector's id; the decks at
  `/sectors/{sectorID}/flight-deck`, the config-driven pages at
  `/sectors/{sectorID}/missions`, the sector's crew at `/sectors/{sectorID}/users`), so a
  link to a deck can be shared and a bookmark lands where it points, and the global pages
  keep their routes. Every signed-in page sits in the library's frame
  ([`app.config.ts`](web/console/src/app/app.config.ts) provides it; the menus are the
  [menu file](web/console/src/app/app.menus.ts)), and the sector a deck works in is a
  consequence of its URL: the library's tenant guard on the
  [segment](web/console/src/app/components/sector/sector.routes.ts) settles the sector
  from the URL before the deck loads, checked against the session's sector list
  (user-domains, the server's own foothold test), and nothing else writes it
  (`SectorService.current` reads the library's `TenantService`). The frame's place
  switcher sets no sector either: between sectors it opens the page it is on under the
  chosen sector's URL, and Headquarters, or a sector opened from it, opens on its first
  page. A URL naming a sector the session cannot reach, a pasted link or a bookmark, lands
  on the frame's refusal page under that sector's URL (`/sectors/{sectorID}/refused`), which
  asks the server what a page there would have asked and shows the answer in the server's
  words, the concealing 404, and never switches to another sector. The guard rewrites no
  URL: the library's
  config-driven pages build their row links and after-create navigation under the page's
  sector (`/sectors/{sectorID}/missions/…`), so a row link is the row's real URL and opens in a
  new tab where it points. Two consumers page on the server: the
  hand-written flight deck, and the library's config-driven list on the
  [Missions page](web/console/src/app/configs/missions.config.ts)
  ([`list.server-paged`](web/console/src/app/configs/missions.config.ts)), which asks for
  one page of the mission board at the descriptor's size, turns it by the server's cursors,
  and draws a filter control only on the columns the generated metadata marks filterable
  ([`metadata.filterable`](pkg/resources/missions.go)). The
  [Standing Orders page](web/console/src/app/configs/standingOrders.config.ts) is the same
  list over a key-less resource ([`list.keyless`](web/console/src/app/configs/standingOrders.config.ts)):
  the whole book as one page, rows identified by position, no row route, and a config
  asking for a page size or a row expansion refused when the page is built. Four pages
  that once drew tables of their own are that list with nothing of Lodestar's drawn: the
  [salvage hold](web/console/src/app/configs/consignments.config.ts), whose rows offer
  Release on cargo still in bond, where the server's Execute answer for the row lists it,
  and say the typed receipt in the notice that follows; the
  [call log](web/console/src/app/configs/distressCalls.config.ts); the
  [hazard board](web/console/src/app/configs/sectorHazardBoards.config.ts), whose rows
  have a compound key and so no row page; and Headquarters'
  [watch desk](web/console/src/app/configs/crewSessionImpersonations.config.ts), the
  crew auth's generated impersonation records with Revoke, the generated method that
  ends one, on each session still open. Four pages
  carry the field shapes a form never types and the write-only field: the
  [Documents page](web/console/src/app/configs/missionDocuments.config.ts) shows a
  document's digest as a size in its cell and as a download on its row page
  ([`field.bytes`](web/console/src/app/configs/missionDocuments.config.ts)) and its
  provenance as JSON, the [call log](web/console/src/app/configs/distressCalls.config.ts)
  shows a call's position in words, its latitude and its longitude
  ([`field.object`](web/console/src/app/configs/distressCalls.config.ts))
  and draws nothing for its write-only transcript in view mode and a blank input in its
  create form and in its edit form, whose value travels only when typed; the edit form has
  the input because the row's Update envelope names every field the caller may write,
  projected or not, so the marshal's covers the transcript no read returns
  ([`field.write-only`](web/console/src/app/configs/distressCalls.config.ts)), and the
  Ships page's cargo bays and the Squadrons page's callsigns are joined in a cell and chips
  on the row page, read-only in edit mode ([`field.array`](web/console/src/app/configs/ships.config.ts)). The
  [Ships page](web/console/src/app/configs/ships.config.ts) puts the two picker read
  modes side by side, each decided by the maximum page size the generated descriptor
  carries: [`picker.paged`](web/console/src/app/configs/ships.config.ts) over the
  hangars, which declare one (one server page at a time, Previous and Next inside the
  panel, the chosen hangar read by key), and
  [`picker.whole`](web/console/src/app/configs/ships.config.ts) over the hull catalog,
  which declares none (read whole with `limit=all`); its Hangar and Class columns show the
  hangar's name and the hull's designation, which the server reads beside each id with the
  page of ships, and sort and filter by them
  ([`column.name-field`](web/console/src/app/configs/ships.config.ts)). The
  [Hangars page](web/console/src/app/configs/hangars.config.ts) lists a sector's hangars,
  and a hangar's own page lists its passes: who the hangar is open to. Both lists open
  their create in a dialog, as every list of the console does
  ([`list.create-dialog`](web/console/src/app/configs/hangars.config.ts)): the button
  above a list opens the form over the page under the button's own words, and no list
  draws a form on its page. Three pages of Lodestar's own follow the same rule, the Flight
  deck, the Roster and the portal's tracker: each action that adds or changes something
  is a button that opens its form in a dialog, and what the server refuses of it is shown
  in the dialog. Add a hangar pass opens the form with three pickers side by side, a person of
  the sector, a squadron and a group of the sector, each a list the generated metadata
  names; the pass names one of them, and one that names two, or none, is refused inside
  the dialog in the server's words. An empty person is nobody, in the validator and in
  the table's holder check alike. A pass's holder never changes: the three holder
  columns are immutable (`conditions:"immutable"` on `UserID`, `SquadronID` and
  `GroupID`), so an update that names one of them is refused for every role with
  `400 json field groupId is immutable`, as the request is decoded and before a grant or
  a row is read. An update that names two of them is refused for both in one sentence,
  in the order of their names (`400 json fields groupId and userId are immutable`),
  whatever order the body gives them in. The answer is therefore the same for a pass of
  the sector, a pass of another sector and an id that is no pass, and nothing tells them
  apart. A pass that
  should be somebody else's is deleted and written again; its level and its expiry are
  what the Marshal's update changes. The walkthrough's "the Hangars page" section sends
  what the page sends: the marshal adds a hangar and a pass for the Bay keepers, and the
  sentry has both hangars through that group alone. Lodestar is also the browser library's
  application: `@impulseframework/resource` and `@impulseframework/resource-angular` live in
  this repository's `web/` workspace, hold no application of their own, and are proven
  here, linked from their local build ("Running it"). The
  console carries the library surface no generated page reaches: the idle session
  configured from the build's environment, its warning the library's notice that any
  activity ends ([`idle.configured`](web/console/src/app/app.config.ts)); the Squadrons page's channel
  card and its "Other squadrons in this sector"
  ([`config.component`](web/console/src/app/configs/squadrons.config.ts),
  [`config.array`](web/console/src/app/configs/squadrons.config.ts)); the leave-page
  confirmation every config-driven route carries
  ([`form.leave-page`](web/console/src/app/app.routes.ts)); and the client's judgment
  rendered by the adapter, with no HTTP interceptor of the console's own: a 401
  mid-session returns the browser to the login page with the attempted URL kept
  ([`client.login-redirect`](web/console/src/app/app.config.ts)), and an `ApiError` nobody
  caught raises one global notice in the server's words while the flight deck's in-place
  refusals raise none
  ([`client.uncaught-notice`](web/console/src/app/components/sector/flight-deck/flight-deck.component.ts)).
- `test/authz`: the generated authorization matrix, which pins the endpoint gate with
  unconditional grants over the empty schema and so never meets a condition;
  `test/integration`: the suites (§9), where every condition is proven over the seeded
  world through the real engines (each conditional grant names its case through
  `provesGrant`, so `impulse check`'s conditions-proven check finds a grant no suite
  proves, and `grants_test.go` proves the deploy path serves every unconditional grant),
  including [`paging.nullable-sort`](test/integration/paging_test.go),
  [`rpc.armed-read`](test/integration/rpc_forms_test.go),
  [`impersonation.revoke`](test/integration/impersonation_ops_test.go), and the parity
  world provisioned from the shipped role files. The served suites need the simulated
  directory: `go test -tags skipAuth ./...`, as CI's integration job and the Procfile do.
- `demonstrations_test.go` and `DEMONSTRATIONS.md`: the demonstration index, and
  `walkthrough.sh`, the proof by hand. Demonstrates: demonstration-index, walkthrough.
- `walkthrough.sh`: every persona's proof by curl against a freshly bootstrapped or reset stack
  (`LODESTAR_LOG` names the file the server's output goes to, where the walkthrough reads the
  sign-in code each password sign-in waits for: `overmind start 2>&1 | tee stack.log`), including the
  droid channel, the portal through the simulated directory, the dry runs, the watch desk,
  the three kinds of impersonation and their masks, the feature flag's flip with a second server process built
  from the tree on `LODESTAR_SECOND_PORT` (default 8091), and the three-minute wait for
  the overdue flip (`LODESTAR_SKIP_FLIP=1` to skip). Export the `.envrc` variables before
  running it.

## Regen discipline

`go generate ./...` from the module root is idempotent from a clean tree; the `zz_gen_*`
output (Go, two TypeScript targets, two workflow DOT graphs, the bindings DOT graph) is the drift baseline, pinned
by `cmd/generate/generate_test.go` as a content snapshot. After changing schema,
annotations, or generator config: regenerate, `go test -tags skipAuth ./...`, and keep the
diff. The generate program prints the schema warnings after every run, one `Warning:`
line each; `go run ./cmd/generate/resourcegenerator -audit` runs the same generation and
also prints the audit pass's findings, one `Audit:` line each, the advisory findings
a plain `go generate` stays silent on. `impulse check` from the module root verifies the
agreements between the parts the tool laid in.
