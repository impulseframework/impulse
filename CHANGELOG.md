# Changelog

## [0.3.0](https://github.com/impulseframework/impulse/compare/v0.2.0...v0.3.0) (2026-11-14)


### ⚠ BREAKING CHANGES

* resource.UserSurface and everything declared with it, the package resource/sessionusers, resource.SeatFirstAdministrator (a tenant's first administrator is written in the tenant's transaction), the user-management entry point of @impulseframework/resource-angular, and the users module and the three User descriptors of @impulseframework/resource are removed; an application declares generation.GenerateIdentity and grants the generated lists and methods in its role files
* session.InvitationAcceptedHook and session.DirectorySeatedHook take the transaction and answer a function to run once it has committed; sessionstorage.Auth.AcceptInvitation takes the hook of the same shape
* resource.ExecuteGate.Enforce and VerifyTenancy take the key values as variadic arguments, and resource.NewComputedFileDecoder and MustNewComputedFileDecoder take the generated collection

### Features

* a list and a row page offer the generated methods the caller may run against a row, each through a dialog drawn from the method's metadata that rehearses a call whose grant is conditional and sends files for a method that takes them ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* a lockout can be cleared: a remembered lock is read again after five seconds, and the generated management has the method that clears it ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* a role a sign-in gives is written in the transaction of the step that causes it: the session library hands its transaction to the invitation and directory hooks, and a role both would give is given once ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* a view declares facts about its rows that a grant's condition reads, and a method takes a row of a view as its target, by one key column or several, judged by the Execute gate ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* a write registers what its transaction owes once it has committed (resource.AfterCommit), and the access library answers a membership row for a caller's own transaction ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* an Execute grant's condition reads the request fields its method declares (new.&lt;name&gt;), a condition reads a list column item by item, a grant on a computed resource takes a condition that reads its rows and facts about the caller, and a method's request field takes several picked keys ([@enumerate](https://github.com/enumerate) on a list field) ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* impulse and its four skeletons take their user management from the generated management: an Auth's package builds its identity client, add site and add auth keep one program generating an Auth's management, and impulse check gains tenant-accounts and holds the identity pages to the API description ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* in an application with a tenant record the generated management declares a tenant's lists of an Auth's people, the roles held there, its invitations and its people's identities, with facts a grant's condition reads, and the methods a tenant changes them through ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* the Execute gate takes the target's key as one value for each key column ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* the generator writes the management of every declared Auth's people (generation.GenerateIdentity): lists over the Auth's tables and the methods that create, invite, rename, disable, delete and unlock a person, give and take a role, set a temporary password and end sessions, served under the role file like any resource and method, with the rules in the new identity package ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* the pages over an Auth's people are built by the Angular library from the generated API description (identityRoutes), and a navigation group none of whose links is drawn is not drawn ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* the user management the library carried is removed: an Auth's people are managed through the generated management, and its pages are built from the API description ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))


### Bug Fixes

* a generator that sets no Spanner emulator version is refused with a message that names WithSpannerEmulatorVersion, where the container runtime refused an image with an empty tag; the latest tag is no longer a default, only a version a generator may name ([#23](https://github.com/impulseframework/impulse/issues/23)) ([985e207](https://github.com/impulseframework/impulse/commit/985e207f2ad0dc2e55f9a481c3190cca26df2df5))
* a used sign-in code or link resets its address's mail limit, which allows five unused mails in fifteen minutes and refuses the next with its own code, too_many_emails; the login page and the email-address component keep their send buttons disabled until the wait has passed ([#24](https://github.com/impulseframework/impulse/issues/24)) ([9e74065](https://github.com/impulseframework/impulse/commit/9e74065e2c008d97b4d3ca625a4761f2100d7ae0))
* impulse add auth --username-is-email renders the users table with the address column the session library reads ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* Lodestar's walkthrough signs its personas in again: it enters the sign-in code every password sign-in waits for, read from the server's log ([#25](https://github.com/impulseframework/impulse/issues/25)) ([c8414f8](https://github.com/impulseframework/impulse/commit/c8414f8bc257484cb56f18a61263c1e347de8bbd))
* the Resource Generator reads the schema again when the emulator version or the generator version changes, where it kept loading the schema an earlier version saved: the schema cache is keyed on both versions beside the migration files, and a generator on the latest emulator tag is told to pin a version ([#21](https://github.com/impulseframework/impulse/issues/21)) ([bbe9812](https://github.com/impulseframework/impulse/commit/bbe981230e4773e08fd44adde99f9c4534bc21c4))
* the tenant Sign-in page lists its directories when it is opened by its address, where it kept the answer to its permission questions from before the tenant's permissions loaded; the skeletons and Lodestar's sector Sign-in page alike ([#24](https://github.com/impulseframework/impulse/issues/24)) ([9e74065](https://github.com/impulseframework/impulse/commit/9e74065e2c008d97b4d3ca625a4761f2100d7ae0))
* the tenant Sign-in page's notes and Lodestar's sector, deck, graph and tracker styles take the theme's color tokens, where hard-coded black left text and borders nearly invisible in the dark theme ([#24](https://github.com/impulseframework/impulse/issues/24)) ([9e74065](https://github.com/impulseframework/impulse/commit/9e74065e2c008d97b4d3ca625a4761f2100d7ae0))

## [0.2.0](https://github.com/impulseframework/impulse/compare/v0.1.0...v0.2.0) (2026-11-13)


### ⚠ BREAKING CHANGES

* **deps:** @impulseframework/resource-firestore asks for firebase 13; an application moves its firebase pin to ^13.2.0
* customComponent is removed from RPCConfigOptions and RPCConfig in @impulseframework/resource-angular/types
* **deps:** @impulseframework/resource-angular asks for Angular 22; an application moves its web workspaces with ng update @angular/core@22 @angular/cli@22 @angular/material@22 angular-eslint@22
* @impulseframework/resource-angular/ui-idle-service is now @impulseframework/resource-angular/auth-idle-service, and @impulseframework/resource-angular/ui-sidenav is now @impulseframework/resource-angular/auth-sidenav

### Features

* a generated application signs its people in through an organization's own identity provider, the session library's generic OpenID Connect method, declared generation.OIDCMethod(name) and composed by impulse add method and add auth ([#8](https://github.com/impulseframework/impulse/issues/8)) ([3b952bb](https://github.com/impulseframework/impulse/commit/3b952bb69f09f8270fe72ff28e9096d60691e0bb))
* a page fills the sign-in component's password form with prefill, as Lodestar's persona cards do, and the person still presses Sign in ([#17](https://github.com/impulseframework/impulse/issues/17)) ([cd890c1](https://github.com/impulseframework/impulse/commit/cd890c1af66a9ff32b50bb5af412afa06b178ed2))
* Lodestar and the applications of the four skeletons run without zone.js: the page is drawn again when a signal, an input or a template event changes it, and each build loses the 36 kB polyfills file ([#19](https://github.com/impulseframework/impulse/issues/19)) ([287676e](https://github.com/impulseframework/impulse/commit/287676e62e7fc07fe4d1ffad1336ee898dbbd68e))


### Bug Fixes

* a test database is handed over with none of the superuser's sessions on it, so its owner can always drop it: PostgresContainer.CreateDatabase waits until the server has ended the session that created the extension, where the drop was refused now and then with permission denied to terminate process ([#12](https://github.com/impulseframework/impulse/issues/12)) ([d42f4d1](https://github.com/impulseframework/impulse/commit/d42f4d1cb7a67f8e1f8d3c6745e99893898abb7b))
* Lodestar's Procfile copies the Firestore rules from where they are, resource/live/firestore ([#17](https://github.com/impulseframework/impulse/issues/17)) ([cd890c1](https://github.com/impulseframework/impulse/commit/cd890c1af66a9ff32b50bb5af412afa06b178ed2))
* on the users page, the error raised when a role cannot be assigned or removed keeps the server's error as its cause ([#19](https://github.com/impulseframework/impulse/issues/19)) ([287676e](https://github.com/impulseframework/impulse/commit/287676e62e7fc07fe4d1ffad1336ee898dbbd68e))
* the cached resource wrapper (swrRxResource) makes its cache key from params that wait on another resource or say the resource is idle, and reads nothing from the cache until they can be told ([#17](https://github.com/impulseframework/impulse/issues/17)) ([cd890c1](https://github.com/impulseframework/impulse/commit/cd890c1af66a9ff32b50bb5af412afa06b178ed2))
* the RPC button's customComponent option is removed: a button that named one logged an error and did nothing ([#19](https://github.com/impulseframework/impulse/issues/19)) ([287676e](https://github.com/impulseframework/impulse/commit/287676e62e7fc07fe4d1ffad1336ee898dbbd68e))


### Code Refactoring

* the idle service and the sidenav are imported from auth-idle-service and auth-sidenav, since both need the session, and the lint rule between the UI and the auth entry points holds with no exception ([#17](https://github.com/impulseframework/impulse/issues/17)) ([cd890c1](https://github.com/impulseframework/impulse/commit/cd890c1af66a9ff32b50bb5af412afa06b178ed2))


### Code Upgrade

* **deps-dev:** eslint-plugin-boundaries moves from 5.4.0 to 7.4.2 with its configuration, and eslint-import-resolver-typescript 4.4.5 joins the browser workspace's tools ([#15](https://github.com/impulseframework/impulse/issues/15)) ([7c6fcf4](https://github.com/impulseframework/impulse/commit/7c6fcf442c00bc61a637cbe86cdef8db46f083c9))
* **deps:** bump the go-dependencies group across 1 directory with 2 updates ([#13](https://github.com/impulseframework/impulse/issues/13)) ([2bea362](https://github.com/impulseframework/impulse/commit/2bea36204d5e508fbf2c6a5717a11565466b40ff))
* **deps:** Go 1.27.4; the module, Lodestar and the skeleton candidates build with go1.27.4, so their binaries carry net/http GO-2026-6859's fix ([#8](https://github.com/impulseframework/impulse/issues/8)) ([3b952bb](https://github.com/impulseframework/impulse/commit/3b952bb69f09f8270fe72ff28e9096d60691e0bb))
* **deps:** Lodestar and the skeletons move to prettier 3.9.10 and @types/node 26, the versions the library workspace is on ([#20](https://github.com/impulseframework/impulse/issues/20)) ([c96c931](https://github.com/impulseframework/impulse/commit/c96c931c03dc9faa0b73d9a98c6523f5a8ccfced))
* **deps:** the browser packages build on Angular 22 with TypeScript 6, and Lodestar and the four skeletons follow; a component is drawn again when something of its own changes, Angular 22's default, and requests go out over fetch ([#17](https://github.com/impulseframework/impulse/issues/17)) ([cd890c1](https://github.com/impulseframework/impulse/commit/cd890c1af66a9ff32b50bb5af412afa06b178ed2))
* **deps:** the Firestore feed builds on firebase 13, and Lodestar and the skeletons follow ([#19](https://github.com/impulseframework/impulse/issues/19)) ([287676e](https://github.com/impulseframework/impulse/commit/287676e62e7fc07fe4d1ffad1336ee898dbbd68e))
* **deps:** the Go modules move to cbor v2.10.0, go-jose v4.1.8, pgx v5.12.0, pflag v1.1.0, zitadel/oidc v3.51.17, x/sync v0.25.0 and google.golang.org/api v0.311.0, in the module, in Lodestar and in the skeletons' requirements; the npm publish workflow's setup-node and three of the browser workspace's tools move with them ([#12](https://github.com/impulseframework/impulse/issues/12)) ([d42f4d1](https://github.com/impulseframework/impulse/commit/d42f4d1cb7a67f8e1f8d3c6745e99893898abb7b))
* **deps:** the library workspace, Lodestar and the skeletons lint with ESLint 10 and run their specs on Vitest 5 and jsdom 31; each declares @eslint/js, which ESLint 10 no longer brings ([#19](https://github.com/impulseframework/impulse/issues/19)) ([287676e](https://github.com/impulseframework/impulse/commit/287676e62e7fc07fe4d1ffad1336ee898dbbd68e))

## 0.1.0 (2026-11-09)


### Features

* the Impulse Framework as one Go module, with its command-line tool, browser packages and demonstration application ([#1](https://github.com/impulseframework/impulse/issues/1)) ([baafdfe](https://github.com/impulseframework/impulse/commit/baafdfef9c2c9bece255dc9e39bb7e5d7688f444))
