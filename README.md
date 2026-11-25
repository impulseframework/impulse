# Impulse Framework

The Impulse Framework builds permission-enforced, Spanner-backed web applications in Go
and Angular: a generator turns annotated Go structs and schema migrations into handlers,
routes, request structs and a typed TypeScript client, the libraries around it carry
sessions, permissions, logging, tracing and the cloud drivers, and a command-line tool
creates, checks and upgrades the applications built on it.

The framework is one Go module, `github.com/impulseframework/impulse`. An application
requires it once, and that one version pins the libraries, the generator, the tool and the
browser packages together.

## Module layout

Every package below lives at the module root unless its path says otherwise.

| Package | What it is |
| --- | --- |
| `impulse` (the root package) | Utility types and functions shared by the rest of the module. |
| `accesstypes` | The permission, role, domain, resource and user types the `access` package works with. |
| `eventsource` | Who made a change, as one typed value: the person, what their session acted as, the session, the process and the RPC method. It has no database code: `resource` keeps it on every change record, and a library that records who acted uses the same value. |
| `access` | The permission engine: roles, grants and memberships, validated against the resources an application declares. |
| `session` | Cookie-backed sessions for web applications: the `Auth`, its sign-in methods and its tables. Its subpackages carry the pieces: `sessionstorage`, `oidc`, `azure`, `google`, `signin`, `mail`, `deviceflow`, `cookie` and `sessioninfo`. |
| `httpio` | Encoding and decoding for HTTP input and output. |
| `logger` | The HTTP request logger, with correlated logging to the supported platforms. |
| `tracer` | Convenience functions for OpenTelemetry tracing. |
| `securehash` | Hashing and comparing secrets. |
| `cloud` | What the cloud drivers share: the settings struct a driver declares and an application embeds in its configuration. |
| `db` | Database testing and migrations: the Spanner and Firestore emulators in containers for test suites, and the migration runner. |
| `spxscan` | Scanning Spanner rows into Go structs and other composite types. |
| `resource` | The generator-driven data layer: permission-enforced CRUD over Spanner-backed resources, with query decoding, permission enforcement and patch sets at run time. Its subpackages hold the drivers (`database`, `live`, `jobs`), the file store (`filestore`) and the rest; see [resource/README.md](resource/README.md). |
| `generation` | The code generator library: it reads the annotated structs and the schema and writes the Go handlers, routes, request structs and TypeScript. |
| `persona` | The persona sign-in a host application offers the test environments: an OpenID Connect provider that signs a person in as a registered persona. |
| `cast` | The contract between an application and that persona sign-in: the cast the application declares and the registration its deploy sends. |
| `app` | Discovers the shape of an Impulse application from its code, for the tool and for bedrock. |
| `ci` | Renders an application's CI workflows from its code, and owns them. |
| `deployhook` | The contract between an application's hooks program and the deploy pipeline. |
| `cmd/impulse` | The command-line tool, `impulse`; see [cmd/impulse/README.md](cmd/impulse/README.md). |
| `lodestar/` | A nested module: the demonstration application (below). |
| `web/` | The bun and Angular workspace that builds the three browser packages (below). |

## The browser packages

The workspace under `web/` publishes three npm packages from `web/projects`:

- `@impulseframework/resource`, the framework-neutral TypeScript client that interprets
  the generated `zz_gen_api.ts` at run time ([README](web/projects/resource/README.md));
- `@impulseframework/resource-angular`, its Angular binding: the components, guards and
  services that render pages from the generated descriptor
  ([README](web/projects/resource-angular/README.md));
- `@impulseframework/resource-firestore`, the Firestore change feed that keeps live pages
  current ([README](web/projects/resource-firestore/README.md)).

The three release with the Go module. Their version is always the Go release without its
`v` (release `v0.3.0` publishes `0.3.0` of each): one release-please component bumps the
module and the three `package.json` files together (`release-please-config.json`), and the
`npm publish` workflow builds and publishes them from the release's tag. An application
pins all three at the same version as its Go pin, in every `package.json` that names them,
and the tool's `web-pins` check holds it to that.

## Lodestar, the demonstration application

`lodestar/` is a complete application built on the framework, with its own README
([lodestar/README.md](lodestar/README.md)): an operations console and a client portal over
a synthetic domain, where every annotation, handler shape and permission rule is something
a person can sign in to and watch working. It is a nested Go module that replaces the
framework module with this checkout, and its web workspace links the browser packages from
`web/dist`, so it always runs on the framework's current source, and CI builds, vets and
tests it. It is also the generator's committed regression baseline.

## The command-line tool

```sh
go install github.com/impulseframework/impulse/cmd/impulse@latest
```

`impulse new` creates an application from an embedded skeleton, `impulse add` and
`impulse remove` put options in and take them out, `impulse check` verifies the agreements
between an application's parts, `impulse render` writes the files the tool owns,
`impulse brand` writes an application's name, logo and colors from the one file that
declares them, and
`impulse upgrade` walks an application through the framework's releases. Inside an
application the tool is the `tool github.com/impulseframework/impulse/cmd/impulse`
directive in `go.mod`, and `go tool impulse <command>` runs it at the pinned version.
Every command, flag and check is described in [cmd/impulse/README.md](cmd/impulse/README.md).

## Sign-in shapes

There are eight shapes an application's sign-in can take. A shape is the answers to four
questions: are there tenants, who gets an account and with what role, where can one
person hold roles, and who manages that person's account. No application declares its
shape: it makes separate choices, some in code and some while it runs, and the shape is
the name of what they add up to. `impulse new`, `impulse add tenancy`, `impulse add auth`
and `impulse add method` start from a shape by name (`--shape`), and `impulse audit`
reads the choices back from the code and names the shape.

| # | Shape | Tenants | Who gets an account |
| --- | --- | --- | --- |
| 1 | [Invite only](docs/sign-in-shapes.md#1-invite-only) | None | People an administrator invites |
| 2 | [Directory members](docs/sign-in-shapes.md#2-directory-members) | None | Everyone in the organization's directory |
| 3 | [Directory roles](docs/sign-in-shapes.md#3-directory-roles) | None | Everyone the organization's directory gives a role |
| 4 | [Open sign-up](docs/sign-in-shapes.md#4-open-sign-up) | None | Anyone |
| 5 | [Tenant-owned users, invite only](docs/sign-in-shapes.md#5-tenant-owned-users-invite-only) | One per person | People the tenant's administrator invites |
| 6 | [Tenant-owned users, by directory](docs/sign-in-shapes.md#6-tenant-owned-users-by-directory) | One per person | Everyone in the tenant's own directory, and people the tenant invites |
| 7 | [Global users, invite only](docs/sign-in-shapes.md#7-global-users-invite-only) | Several per person | People any tenant invites |
| 8 | [Global users, each tenant chooses](docs/sign-in-shapes.md#8-global-users-each-tenant-chooses) | Several per person | Everyone in a directory a tenant has taken, and people it invites |

Each shape is described in full in [docs/sign-in-shapes.md](docs/sign-in-shapes.md): who
gets an account, what role they start with, who manages the account, which sign-in
methods it can use and how wide each can be opened, the command that starts an
application from it, and what `impulse audit` prints for it.

## Developing the repository

The module is Go 1.27 (`go.mod`), and the web workspace is built with bun 1.4.4, the bun
that wrote `web/bun.lock`. From the repository root:

```sh
go build ./... && go vet ./...
go test ./...
golangci-lint run ./...
golangci-lint run --build-tags dev ./...
```

The database tests start the Spanner and Firestore emulators in containers through the
testcontainers library, so Docker or podman must be running. CI lints the whole module
under both tag sets, `""` and `dev`, with the configuration at `.golangci.yml`, which
includes a US English spelling check.

The browser packages:

```sh
cd web && bun install && bun run build
bun run lint
bun run test
```

Lodestar builds against the packages in `web/dist`, so build them first; then
`cd lodestar/web && bun install && bun run build` and, for the Go side,
`cd lodestar && go build ./... && go vet ./... && go test -race -tags skipAuth ./...` (the
tag simulates the directories Lodestar's people sign in through; the served suites skip
without it). CI lints Lodestar's module as it lints the root one, whole, with the
configuration at `lodestar/.golangci.yml`, under three tag sets: the root lint's two, and
`skipAuth`, since the files that build only with that tag are read under no other:
`cd lodestar && golangci-lint run && golangci-lint run --build-tags dev && golangci-lint run --build-tags skipAuth`.
The skeletons the tool renders are validated by rendering each one and building, testing,
linting and checking the result, which CI does for every skeleton against the framework at
the commit (`impulse render <candidate> <dir> --module <path> --dev-root <checkout>`).

The web workspaces move together. `web`, `lodestar/web` and every skeleton's web root pin
the same version of each third-party package they share, so the library is tested on the
versions Lodestar installs and a new application starts from; a Go test,
`TestWebWorkspacesPinTheSameVersions` in `cmd/impulse/internal/skeleton`, fails when one is
left behind. A version is changed in every workspace's `package.json` in the same pull
request, followed by `bun install` in `web` and in `lodestar/web`, whose lock files are
committed.

A release is cut by release-please from the conventional commit titles of the merged pull
requests, as one component with the tag `vX.Y.Z`. A release that needs something of an
application beyond moving its pin records a step in the upgrade ledger
(`cmd/impulse/internal/ledger`), and CI refuses a release that would leave the ledger's
last step behind.

## Taking a release in an application

An application's `go.mod` has one framework requirement,
`github.com/impulseframework/impulse vX.Y.Z`, and the tool directive; that version is the
framework pin, and its web roots pin the three browser packages at the same version without
the `v`. Moving to a new release is `go run github.com/impulseframework/impulse/cmd/impulse@<version> upgrade`,
which walks the application through the ledger's steps after the one its pin stands at,
running each step's recipes, moving the Go pin and the web pins, regenerating and checking,
one commit per step, and ends at the release named. When no step is pending, moving the pin
is `go get -tool github.com/impulseframework/impulse/cmd/impulse@<version>`, then
`go tool impulse render`, then `go tool impulse check`.

## License

MIT; see [LICENSE](LICENSE).
