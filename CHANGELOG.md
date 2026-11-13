# Changelog

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
