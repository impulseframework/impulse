# access

Go library for role-based access control (RBAC) with domain-partitioned permission management. The release's default roles travel with the release, in a role file embedded in the application binary; custom roles and role memberships live in typed tables owned by this library. Permission checks are answered by an immutable in-memory snapshot compiled from both — lock-free, allocation-free, and never touching the database on the request path.

## Overview

Manages user permissions and roles across multiple domains or tenants. Supports PostgreSQL and Google Cloud Spanner as persistence backends through the `postgresstore` and `spannerstore` subpackages.

## Features

- Role-based access control (RBAC)
- Multi-tenant support with structural global and every-domain scopes
- Resource- and field-level permissions (`employees`, `employees.name`, `employees.*`)
- Compiled in-memory snapshot evaluation with near-realtime change propagation
- User, role, and permission management APIs
- HTTP handlers for REST endpoints
- Default roles shipped in the application binary, with a deploy-time policy check

## Installation

```bash
go get github.com/impulseframework/impulse/access
```

## Core Concepts

- **Scope**: Where a request is — the global partition (`accesstypes.GlobalScope()`) or one tenant domain (`accesstypes.DomainScope(domain)`). Permission checks, permission digests and the foothold question take a `Scope`, because a request is in exactly one partition. Global-ness is structural: no domain string means "global", so any tenant name is legal data (a tenant literally named "global" is an ordinary tenant). Scopes are opaque labels to access — the application owns its tenant list, and checks fail closed on unknown tenants.
- **PolicyScope**: Where policy is held — a role membership, a custom role and its grants live in the global partition (`accesstypes.GlobalPolicyScope()`), in one tenant domain (`accesstypes.DomainPolicyScope(domain)`), or in every tenant domain (`accesstypes.EveryDomainPolicyScope()`). The policy store and the user manager take a `PolicyScope`. A membership held in every domain reaches every tenant, including a tenant the store holds no other row in, so a tenant created at run time needs nothing written for its every-domain members. Every domain is not a place a request can be in, so nothing converts a `PolicyScope` into a `Scope`, and passing a `PolicyScope` to a check fails to compile. `Scope.PolicyScope()` converts the other way, and `PolicyScope.Covers(scope)` answers whether policy held somewhere applies in a partition.
- **User**: Individual with assigned roles
- **Role**: Named collection of permissions, of one of two kinds. A default role is declared in the release's role file (see Default Roles): a global default is held in the global partition, a domain default in every tenant domain, and the store holds no row for either. A custom role is created at run time through the user manager and held where it was created; its identity is `(policy scope, role)`.
- **Permission**: Action that can be performed (List, Read, Create, Update, Delete, ...)
- **Resource**: What a permission applies to. A resource name has at most one dot: `employees` is a parent resource, `employees.name` is one field on it, and `employees.*` grants all fields by implication (covering fields that don't exist yet). An endpoint grant (`employees`) gives no field visibility, and field grants don't grant the endpoint — that separation is the point of field-level control.

## Policy Stores

Each store owns three tables named `{Prefix}{Store}{Roles|UserRoles|RoleGrants}` — defaults yield `AccessRoles`, `AccessUserRoles`, `AccessRoleGrants`. Applications with several independent permission stores in one database give each a store name (`WithStore("AdminPortal")` → `AccessAdminPortalRoles`, ...); separate tables make cross-store leakage structurally impossible.

The tables hold what is written at run time: custom roles, their grants, and role memberships. The release's default roles have no rows. Each row records where it is held in its own columns, never in a distinguished domain value: `Kind` is `global`, `domain` or `every` and is checked in the database, `Domain` carries the tenant name on a `domain` row and is empty otherwise, and `Axis` is the empty string on every row today. Any tenant name is therefore ordinary data.

The `access.Store` interface the two subpackages implement is sealed: only this module's stores satisfy it. The stores are thin; validation, change signaling and snapshot compilation live in the access package, one code path for every store. Both stores keep the same contract:

- `Roles` holds custom roles only, each with the description it was created with.
- `UserRoles` stands alone. A membership names its role by name alone, since a default role has no row to reference, so `InsertUserRole` writes by role name; the access package checks that the name resolves before it writes. An index led by the user (`{Prefix}{Store}UserRolesByUser`) serves `ListUserMemberships`, which lists every membership a user holds wherever it is held.
- `RoleGrants` belongs to its custom role: interleaved in `Roles` on Spanner and a foreign key on PostgreSQL, both with cascading delete. A grant needs its role's row, and deleting a custom role deletes its grants.
- `DeleteRole` refuses while memberships held in that scope still name the role, checked in the same transaction as the delete.
- `ReadPolicy` reads the custom roles, their grants and the memberships with one consistent view of the store, for the snapshot to compile.
- Inserting an existing row and deleting an absent one are no-ops, and list results are sorted.

The library never runs DDL — applications own their schema lifecycle. `DDL()` returns the canonical schema rendered with the configured names; copy it into your migration file. The library's own test suites execute exactly these statements, so the shipped DDL is the tested DDL.

### PostgreSQL

```go
import "github.com/impulseframework/impulse/access/postgresstore"

// Rides the application's existing pgx pool.
store, err := postgresstore.New(pool /* *pgxpool.Pool */)

// Or a named store with a custom prefix:
store, err := postgresstore.New(pool, postgresstore.WithStore("AdminPortal"))

// Schema for your migration file:
for _, stmt := range store.DDL() { ... }
```

### Google Cloud Spanner

```go
import "github.com/impulseframework/impulse/access/spannerstore"

store, err := spannerstore.New(client /* *spanner.Client */)

// Or a named store:
store, err := spannerstore.New(client, spannerstore.WithStore("AdminPortal"))
```

## Quick Start

```go
package main

import (
    "context"
    "log"

    "github.com/impulseframework/impulse/access"
    "github.com/impulseframework/impulse/access/postgresstore"
    "github.com/impulseframework/impulse/accesstypes"
    "github.com/jackc/pgx/v5/pgxpool"
)

func main() {
    ctx := context.Background()

    pool, _ := pgxpool.New(ctx, "postgresql://user:pass@localhost/db")

    store, err := postgresstore.New(pool)
    if err != nil {
        log.Fatal(err)
    }

    // An application that ships default roles adds
    // access.WithDefaultRoles(collection, roles.File); see Default Roles.
    client, err := access.New(store)
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    mgr := client.UserManager()

    // Where the request is, and where the policy for it is held.
    tenant1 := accesstypes.DomainScope("tenant1")
    tenant1Policy := tenant1.PolicyScope()

    // Create a custom role, saying what it is for, and grant permissions
    mgr.AddRole(ctx, tenant1Policy, "Administrator", "Administers the tenant.")
    mgr.AddRolePermissionResources(ctx, tenant1Policy, "Administrator", "Read", "documents", "documents.*")

    // Assign role to user
    mgr.AddUserRoles(ctx, tenant1Policy, "john.doe", "Administrator")

    // Check permissions: one Decision per resource, from one policy snapshot.
    env := accesstypes.NewEnvironment() // per-request decision context
    decisions, err := client.CheckUserResources(ctx, env, "john.doe", tenant1, "Read", "documents", "documents.title")
    if err != nil {
        log.Fatal(err)
    }
    if denied := decisions.DeniedResources(); len(denied) > 0 {
        // deny: shape your own Forbidden response
    }
}
```

## Policy Snapshot, Freshness, and Lifecycle

Permission checks are served from an immutable in-memory snapshot: lock-free,
allocation-free, and never touching the database on the request path. Policy
changes propagate between instances in near-realtime through a change signal
the application supplies (see Change signal below), with a background heartbeat
as the correctness backstop: it re-reads the policy store (default every 1m) and
swaps in a new snapshot only when the content changed, so cross-instance
staleness is bounded by the heartbeat interval even if the signal breaks. Writes
made through this client are visible to its own checks immediately.

The snapshot compiles the release's default roles from the role file beside the
store's rows, in three parts: the global partition; the policy held in every
domain, which is the domain default roles, the custom roles held in every
domain and the memberships held in every domain; and, for each domain the
store holds rows in, what that domain's rows add on top of the every-domain
part. A check in a tenant the store holds no rows for is answered from the
every-domain part alone, so a membership held in every domain reaches that
tenant, and a thousand tenants share one compiled copy of the defaults.

```go
client, err := access.New(store,
    // The release's default roles: the role file embedded in the binary,
    // validated against what the release declares (see Default Roles). The
    // collection also lets a load skip and report a stored grant naming a
    // permission, resource or field this release does not have, instead of
    // carrying it. An application with no role file passes
    // access.WithPermissionCollection(router.Collection()) for that check.
    access.WithDefaultRoles(router.Collection(), roles.File),
    // Recommended: propagate changes between instances in near-realtime over
    // the application's signal channel (see Change signal).
    access.WithChangeSignal(policySignal),
    // Optional: tune the heartbeat backstop (default 1m).
    access.WithHeartbeatInterval(30*time.Second),
    // Optional: replace the hook that receives background reload/signal
    // failures and the findings a load reports. The default writes each one
    // to the standard log; while reloads fail, checks keep serving the last
    // good snapshot.
    access.WithReloadErrorHandler(func(err error) { alert(err) }),
)

// Readiness: block until the first policy snapshot has loaded.
if err := client.WaitReady(ctx); err != nil { ... }

// Shutdown: stop background reloading (checks keep serving the last snapshot).
defer client.Close()
```

A grant the running release cannot use — a condition that does not parse, a
row-referencing condition on a scope-wide grant, or, with the collection from
`WithDefaultRoles` or `WithPermissionCollection`, a permission, resource or
field the release does not declare — does not stop the load: it is left out of
the snapshot and reported to the reload-error hook as a `*access.SkippedGrant`.
The permission it would grant is denied, the rest of the policy loads, and the
instance becomes ready. That is what makes a stored grant safe to outlive the
release that wrote it: after a rollback, or after a release removes a field,
the grant is denied and reported, and everything else keeps working.

A load reports two more findings the same way. A custom role whose name a
default role of the running release has, in the same kind of scope, is a
`*access.ShadowedRole`: the default wins, the custom role's grants are skipped,
and its members hold the default role's grants; rename or delete the custom
role to clear it. Memberships naming a role that is neither a default role nor
a custom role in their scope are an `*access.OrphanedMembership`: the members
hold nothing from it, as happens when a release retires a default role and its
memberships remain. Each finding reaches the hook once per compiled snapshot,
and `CheckPolicy` returns the same findings to the deploy (see Default Roles).

A custom role's grant set changes through `ChangeRoleGrants`, one store write
for the removals and the additions together, so a reader never sees a role
with neither its old grant nor its new one.

### Change signal

The engine announces on the change signal after every successful policy write
and watches it for the hints other instances announce. A hint only nudges the
background loop to re-read the policy store: the signal carries no policy, so a
lost or duplicated hint costs latency and nothing else. Announce failures and
watch errors reach the reload-error hook and never fail the write. `Close`
waits for an announcement still in flight, bounded by a few seconds, so a write
made right before it (a migrate command's seed) is heard by the other instances
before the application closes the channel.

The application supplies the signal as an adapter over its one signal channel,
through `access.ChangeSignalFunc(announce, watch)`. `announce` publishes the
policy kind on the channel. `watch` subscribes to the policy kind, hands every
signal to the `onChange` the engine passes, and blocks until its context ends;
that is the `Watch` contract, and it keeps the subscription open for the life of
the client, its stop running when `watch` returns. Whenever `watch` returns
early (a subscription that ended), the engine re-invokes it after a delay, so a
`watch` that returns on connection loss is enough.

The heartbeat is the backstop. With or without a signal, the engine re-reads the
policy store every heartbeat interval (default 1m, `WithHeartbeatInterval`) and
swaps in a new snapshot only when the content changed, so cross-instance
staleness is bounded by that interval while the signal is broken or absent.
Correctness never depends on the signal.

An application built on the resource package has the channel already: its
`live.Service` (package `github.com/impulseframework/impulse/resource/live`) carries
`Signal(ctx, kind)` and `Subscribe(kind, onSignal) (stop, err)` over the signal
kinds the resource package declares, and `resource.KindPolicy` is the policy
kind. The adapter over it, where `svc` is the application's `live.Service`:

```go
policySignal := access.ChangeSignalFunc(
    func(ctx context.Context) error {
        return svc.Signal(ctx, resource.KindPolicy)
    },
    func(ctx context.Context, onChange func()) error {
        stop, err := svc.Subscribe(resource.KindPolicy, onChange)
        if err != nil {
            return err
        }
        defer stop()
        <-ctx.Done()

        return nil
    },
)

client, err := access.New(store, access.WithChangeSignal(policySignal))
```

Any channel that publishes a kind to every instance's subscribers adapts the
same way.

## API Usage

### Permission Checking

The base name / `Resources` suffix pairing is the API's naming standard: the
base method checks a permission held scope-wide (attached to no resource);
the `Resources` variant checks specific resources.

The checks — for users and for roles alike — are the request-path seams and
are ABAC-ready: they take the per-request decision context
(`accesstypes.Environment`, immediately after `ctx`) and answer with
Decisions (`Denied` / `Granted` / `Conditional`).
`CheckUserResources` returns one Decision per resource, all from a single
policy snapshot; `Decisions.DeniedResources()` lists what was denied — empty
means everything passed. One permission per call; batch as many resources as
you like. `CheckUser` returns the Decision for a permission held scope-wide.
A grant may carry a condition — opaque expression text on its store row
(`Condition`, empty = unconditional, and part of the row's identity) — and a
resource covered only by conditional grants answers `Conditional`; any
unconditional cover answers `Granted` outright. A role may hold several grants
on one resource for one permission, each under its own condition, stored as
one row per condition, so the engine sees them exactly as it would from
separate roles. Grouping is the engine's job: within one
`CheckUserResources` call, a Conditional decision's
`ConditionGroup.Resources` lists every checked resource sharing that
covering-grant set, the same group appearing in each member's Decision —
deduplicate by sorted-Resources equality. Conditions never attach to
scope-wide grants (there is no row for them to see — rejected at snapshot
load; interim — this narrows to row-referencing conditions once the
condition package classifies row-free, with environment/subject-only
conditions folding at check time), so `CheckUser` never answers
`Conditional`. Nothing authors
conditions yet (the expression language is undesigned), so in practice every
Decision is `Granted` or `Denied` and an empty `Environment` is the normal
argument.

The role checks are the same seams evaluated against a role's effective
grants — its own and, transitively, every role it inherits — with no member
involved: `CheckRole` and `CheckRoleResources` answer exactly what
`CheckUser` and `CheckUserResources` answer a user holding only that role.
They serve sessions that operate *as a role* (an administrator working a
partner portal under a role chosen for the session — the session library's
impersonated sessions) as well as policy introspection. A role has no
identity of its own: a `subject` term in a row condition is bound by the
resource layer to the session's effective identity at render time, and a
scope-wide condition that needs a subject is a check error, as it is for a
user. `RoleHasGrants` and `RolePermissionDigest` are the role twins of the
user foothold and digest questions.

```go
env := accesstypes.NewEnvironment()

decisions, err := client.CheckUserResources(ctx, env, user, scope, "Read", "documents", "documents.title", "images")
decision, err := client.CheckUser(ctx, env, user, scope, "ExportReports") // scope-wide

decisions, err = client.CheckRoleResources(ctx, env, role, scope, "Update", "documents")
decision, err = client.CheckRole(ctx, env, role, scope, "ExportReports")
```

A request binds its principal once: `client.ForUser(user)` returns a
`*UserChecker` and `client.ForRole(role)` a `*RoleChecker`, each carrying
`Check`, `PermissionDigest` and `HasGrants` over the bound subject — the
canonical implementations of the resource package's `UserPermissions` and
`RolePermissions` seams. Only the `UserChecker` has `User()`: a role is not
anyone, and the session's effective identity is the resource layer's to
supply.

`UserHasGrants`, and `HasGrants` on a `UserChecker`, answer the foothold
question: whether a user holds at least one grant in a scope. An application
that hides which tenants exist answers a caller with no foothold in a domain
exactly as if the domain did not exist. A tenant picker filters the
application's own tenant list by it; access holds no tenant list, so it never
enumerates domains. A membership that resolves to no grants is not a foothold,
and a membership held in every domain is a foothold in each tenant.

```go
checker := client.ForUser(user)

var visible []accesstypes.Domain
for _, tenant := range tenants { // the application's own tenant list
    ok, err := checker.HasGrants(ctx, accesstypes.DomainScope(tenant))
    if err != nil {
        return err
    }
    if ok {
        visible = append(visible, tenant)
    }
}
```

There is no tenant validation on the check path: an unknown tenant scope
holds no grants, so everything comes back denied (fail closed). If your API
wants to answer 400 for an invalid tenant rather than 403, validate the
tenant in your own guard — your application owns the tenant table.

### User Management

```go
mgr := client.UserManager()

// Where policy is held: one tenant domain, every tenant domain, or the
// global partition (accesstypes.GlobalPolicyScope()).
tenant1 := accesstypes.DomainPolicyScope("tenant1")
tenant2 := accesstypes.DomainPolicyScope("tenant2")
everyDomain := accesstypes.EveryDomainPolicyScope()

mgr.AddUserRoles(ctx, tenant1, "john.doe", "Administrator", "Editor")
mgr.DeleteUserRoles(ctx, tenant1, "john.doe", "Editor")

// Held in every domain: john.doe is a Viewer in every tenant, including one
// created after this call.
mgr.AddUserRoles(ctx, everyDomain, "john.doe", "Viewer")

// Memberships as stored, keyed by where each is held: with no scopes, every
// membership the user holds; with scopes, the memberships held in each.
roles, err := mgr.UserRoles(ctx, "john.doe")
roles, err = mgr.UserRoles(ctx, "john.doe", tenant1, tenant2)

// Effective permissions are answered where a request is, from the snapshot.
// Access holds no tenant list of its own, so at least one scope is required.
permissions, err := mgr.UserPermissions(ctx, "john.doe", accesstypes.DomainScope("tenant1"))
```

`UserRoles` reports memberships where they are held, so a membership held in
every domain is one entry under `EveryDomainPolicyScope()`, never one per
tenant. `UserPermissions` reports what the user holds in each scope from the
memberships held there and in every domain, default and custom roles alike.

### Role Management

```go
// A custom role's name follows the grammar every role name does (letters and
// digits starting with a capital letter), and it is created with a
// description: one or two plain sentences saying what it is for. A name
// outside the grammar, or a role without a description, is refused.
mgr.AddRole(ctx, tenant1, "Moderator", "Reviews and removes the tenant's posts.")
deleted, err := mgr.DeleteRole(ctx, tenant1, "Moderator") // scoped to tenant1

roles, err := mgr.Roles(ctx, tenant1) // []access.RoleInfo: each role's name and description
role, exists, err := mgr.ResolveRole(ctx, tenant1, "administrator") // "Administrator": the spelling the store holds, matched without regard to case

users, err := mgr.RoleUsers(ctx, tenant1, "Administrator")
mgr.AddRoleUsers(ctx, tenant1, "Administrator", "user1", "user2")
mgr.DeleteRoleUsers(ctx, tenant1, "Administrator", "user1")

// A custom role held in every domain exists in each tenant.
mgr.AddRole(ctx, everyDomain, "Auditor", "Reads every tenant's records without changing them.")
users, err = mgr.RoleUsers(ctx, everyDomain, "Auditor")

// Global-scope roles live in their own partition.
mgr.AddRole(ctx, accesstypes.GlobalPolicyScope(), "SystemAdmin", "Administers the application itself.")
```

`Roles` lists the roles that exist in a scope, each as its name and
description (`RoleInfo`): the release's default roles of the scope's kind,
described by the role file, and the custom roles held there, and, for one
domain, the custom roles held in every domain too, described as `AddRole` was
told. A name is listed once however many places hold it. `ResolveRole` finds the role a scope holds
under a name, matched without regard to case, and returns it in the spelling the
store holds: in one domain a domain default role, a custom role held in that
domain, or a custom role held in every domain. The exact spelling wins when it
exists; otherwise the one role that matches case-insensitively, and two such roles
with no exact match is an error. It is how a name from outside the application,
a directory's group (a Google group is an email address, so its name is
lowercase), finds the role the role file spelled. A custom role held in one
domain alone cannot be assigned in every domain.

No two roles in a scope differ only in case: the role file is refused at `New`
when it declares such a pair, and `AddRole` refuses a custom role whose name
matches an existing role of the scope without regard to case, so a resolution
is never ambiguous. `AddRole` also holds a custom role's name to the one grammar
every role name follows — letters and digits starting with a capital letter, such
as `Administrator` or `TenantAdministrator` (see Default Roles) — and refuses a
name outside it with the same words the role file is refused with.

A default role's name is taken: `AddRole` refuses it, and `DeleteRole` refuses
to delete a default role. `DeleteRole` also refuses while users hold the role
in that scope. A custom role held in every domain is changed where it is held,
through `EveryDomainPolicyScope()`, not through one domain.

### Permission Management

```go
// Resource- and field-specific permissions
mgr.AddRolePermissionResources(ctx, tenant1, "Editor", "Read", "documents", "documents.*")
mgr.DeleteRolePermissionResources(ctx, tenant1, "Editor", "Read", "documents.*")

// A scope-wide permission (not tied to any resource) is granted through the
// base-name method — there is no resource value that means it.
mgr.AddRolePermission(ctx, tenant1, "Administrator", "CreateUsers")
mgr.DeleteRolePermission(ctx, tenant1, "Administrator", "CreateUsers")

// What the role holds where a request is, from the snapshot, inheritance
// folded: the file's grants for a default role, the store's for a custom one.
permissions, err := mgr.RolePermissions(ctx, accesstypes.DomainScope("tenant1"), "Administrator")

// The role's resource grants as held, with the conditions each is granted under.
grants, err := mgr.RoleGrants(ctx, tenant1, "Administrator")
```

Grant writes enforce the resource shape fail-closed: at most one dot, both
segments non-empty (`a.b.c`, `.name`, and `employees.` are rejected at
declaration time). The grant methods change custom roles only: a default
role's grants are the release's, so a call naming one is refused; copy its
grants into a custom role under another name to change them.

## HTTP Handlers

```go
logHandler := func(handler func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if err := handler(w, r); err != nil {
            // Handle error
        }
    }
}

handlers := client.Handlers(logHandler)

r := chi.NewRouter()
r.Get("/domains/{domain}/roles", handlers.Roles())
r.Post("/domains/{domain}/roles", handlers.AddRole())
r.Delete("/domains/{domain}/roles/{role}", handlers.DeleteRole())
r.Get("/domains/{domain}/roles/{role}/permissions", handlers.RolePermissions())
r.Get("/domains/{domain}/roles/{role}/users", handlers.RoleUsers())
r.Post("/domains/{domain}/roles/{role}/users", handlers.AddRoleUsers())
r.Delete("/domains/{domain}/roles/{role}/users", handlers.DeleteRoleUsers())

// Memberships held in every tenant domain have no domain to name.
r.Get("/every-domain/roles/{role}/users", handlers.RoleUsersEveryDomain())
r.Post("/every-domain/roles/{role}/users", handlers.AddRoleUsersEveryDomain())
r.Delete("/every-domain/roles/{role}/users", handlers.DeleteRoleUsersEveryDomain())
```

The handlers read the `{domain}` and `{role}` path parameters from a chi
router; the paths are the application's choice. The `{domain}` forms address
roles and memberships held in one tenant domain. `AddRoleUsersEveryDomain`,
`DeleteRoleUsersEveryDomain` and `RoleUsersEveryDomain` address memberships
held in every tenant domain. No handler addresses the global partition.
`Roles` lists the domain's default roles and the custom roles held there or in
every domain, each as `{"name": ..., "description": ...}`; `AddRole` takes
`roleName` and `description` in its body and refuses a role without a
description; and `RolePermissions` answers what a role holds in the domain,
read as the scope of a request, for default and custom roles alike.

## Default Roles

Default roles are policy that travels with the release. They are authored as a
role file, JSON in the `RoleConfig` shape (see JSON Configuration), embedded in
the application binary and handed to `New` through `WithDefaultRoles`. `New`
parses the file and validates it against the collection the release declares:
a file that does not parse, names a role outside the grammar, leaves a role
without a description, declares a role twice or at both scopes, grants what
the collection does not declare, or carries a condition the vocabulary refuses
fails `New`, so a release whose default roles are wrong does not start.

Every role name follows one grammar, a default role's in the file and a custom
role's given to `AddRole` alike: letters and digits starting with a capital
letter, such as `Administrator` or `TenantAdministrator` — no underscore,
hyphen, space or dot. The scope is never part of the name: a role is declared
under its scope key, so `Administrator_Global` is refused where
`Administrator` under `global` is meant. Every role carries a description, one
or two plain sentences saying what the role is for, the way a cloud provider
describes its roles; `Roles` returns it beside the name.

The store holds no row for a default role. A global default role is held in the
global partition and a domain default role in every tenant domain. A membership
names a default role by name alone, the snapshot compiles its grants from the
file, and the user manager, with the HTTP handlers over it, lists it beside the
custom roles, refuses to create, change or delete it, and answers its grants
from the file. Nothing copies the file into the store, so a deploy has no step to run
per tenant and a rollback has no step of its own: the running binary's file is
the default policy.

### Usage

Embed the role file in a package of the application:

```go
// Package roles holds the release's default roles.
package roles

import (
    _ "embed"

    "github.com/impulseframework/impulse/access"
)

// File is the release's role file.
//
//go:embed roles.json
var File access.RoleFile
```

Hand it to `New` with the collection it validates against:

```go
client, err := access.New(store,
    access.WithDefaultRoles(router.Collection(), roles.File),
    access.WithChangeSignal(policySignal), // see Change signal
)
if err != nil {
    return err // includes a role file that does not parse or does not validate
}
```

`WithDefaultRoles` also gives the client the collection `WithPermissionCollection`
would, so an application with a role file passes only `WithDefaultRoles`.

The deploy's migrate step calls `CheckPolicy`. It reads the store once and
returns the role file's warnings (see Warnings) beside what the store holds that
this release cannot use as written: a grant the load would skip
(`*access.SkippedGrant`), a custom role a default of the same name shadows
(`*access.ShadowedRole`), and memberships naming a role nothing defines
(`*access.OrphanedMembership`). It writes nothing; a store that cannot be read
is the error.

```go
func checkPolicy(ctx context.Context, client *access.Client) error {
    warnings, err := client.CheckPolicy(ctx)
    if err != nil {
        return err
    }
    for _, w := range warnings {
        fmt.Printf("Warning: %s\n", w)
    }

    return nil
}
```

Every warning prints as one line. A consumer that wants the fields switches on
the kind: `access.GrantWarning` and `access.ConcealingKeyWarning` come from the
role file, and `*access.SkippedGrant`, `*access.ShadowedRole` and
`*access.OrphanedMembership` from the store.

### Behavior

- A global default role is held in the global partition; a domain default role is held in every tenant domain, including a tenant created after the release started, with nothing written for it
- A membership names a default role by name alone: a global default is assigned in the global partition, a domain default in one domain or in every domain
- `Roles` and `ResolveRole` include the default roles of the scope's kind, `Roles` with the file's description, and `RolePermissions` and `RoleGrants` answer a default role's grants from the file
- `AddRole` refuses a default role's name, `DeleteRole` refuses a default role, and the grant methods refuse to change one; copy its grants into a custom role under another name to change them
- A role whose file entry has no grants exists and holds nothing until a grant is authored
- Validates resources, permissions, and conditions against the collection before the client starts, and refuses a grant on a resource whose scope is not its role's
- Prevents update permissions on immutable resources
- Warns, without rejecting, when a role holds a conditional Delete, Update, or targeted Execute on a row it can neither Read nor List, or a conditional List on a concealing field the resource sorts or filters by (see Warnings)
- A release that introduces a default role under a custom role's name shadows the custom role, and a release that retires a default role leaves its memberships holding nothing; both are reported, and the store is left as it is

**Note**: `CheckPolicy` writes nothing, so it is safe to run on every deploy and from any tool. A rollback's binary carries its own role file, and what the store holds that the older release cannot use is reported the same way.

### Warnings

A Delete, an Update, and an Execute on a method with a `@target` row locate the row first
and answer NotFound when it is absent, then evaluate the grant's condition and answer
Forbidden when it fails. A read hides a row its condition does not select behind the same
NotFound. So a role holding a conditional write on a row it can neither Read nor List
learns, from the response code alone, that a row exists in its tenant, without changing
it. The role file may carry such a grant, and the release enforces it as written;
`CheckPolicy` returns one warning for it, which the migrate step prints as one line:

```
Warning: role Paymaster: Delete on Missions is granted under "state = 'open'" without Read or List on Missions: a Forbidden answer tells the caller a Missions row exists where a read would answer NotFound. Grant Read or List on Missions in this role or in a role assigned with it, or accept the disclosure; a Read whose condition is narrower than this one leaks the same way.
```

The check is per role. A reader role meant to be assigned alongside the writer role does
not silence it, because the role file holds no assignments; the line tells the role's
author which role to change. Unconditional writes are not flagged: they reveal existence
only by succeeding, which is what the grant permits. The check is a heuristic. Holding
Read does not close the channel when the Read condition is narrower than the write's.

The second warning is about cost, not disclosure. A sort or filter on a conditionally
visible field runs over the visible projection, `CASE WHEN <condition> THEN column END`,
so a masked cell is `NULL` wherever the query looks at it and nothing about a hidden value
leaks through order or match. No index serves that expression: every such page sorts the
tenant's whole partition. The query drops the `CASE` when the row filter has already
proven the field's condition on every surviving row, which is the case when every field
the role lists on the resource is granted under a condition that implies the field's own;
it keeps the `CASE` when the role lists another field unconditionally, or under a
condition the field's condition does not cover. The warning is raised exactly there, for a
field the resource orders by (`@order`, so every page pays) or admits as a sort or filter
key (an indexed or `allow_filter` field, so a page sorted or filtered by it pays), and
only where the field's masked cells conceal. The resource package's `masking:"positional"`
struct tag declares the other behavior: the cell stays hidden but the query runs on the
real column, the index serves the page, and where the hidden values fall is disclosed; the
generated collection leaves such a field out of the check.

```
Warning: role Archivist: List on Missions.fee is granted under "state = 'completed'", and fee is a sort or filter key of Missions whose masked cells conceal; this role also lists Missions fields under "state IN ('completed', 'failed', 'stood_down')", which the field's condition does not cover, so the row filter does not prove the field's condition and a page this role sorts or filters by fee orders on CASE WHEN <condition> THEN column END, which no index serves: it sorts the tenant's whole partition. Grant fee unconditionally in this role, tag the field masking:"positional" and disclose where its hidden values fall, or accept the cost for a table that never pages at volume.
```

The covering test admits implication on one attribute under a closed rule set: a
condition covers another that spells it the same way, an equality or `IN` list whose
values all sit inside its own `IN` list, a `!=` or `NOT IN` whose values include all of its
own, and a conjunction any one of whose terms it covers; every other shape (another
attribute, a range, a subject set, `1` against `1.0`) must be spelled the same. So the
archivist's row fields, granted on the closed states, are silent, because her completed
grant implies them (`state = 'completed'` is one of `state IN ('completed', 'failed',
'stood_down')`); her fee, granted on completed alone, is the one warning above, because
the closed-states grant admits rows the fee's condition does not select.

Both warnings are about the role file's roles. A custom role's grants are written at run
time through the user manager, and these two checks do not run on them.

`ValidateRoles` runs the validation `New` runs on the role file and returns its warnings
as `[]access.Warning`, with no store and no client involved, so a project test or a tool
gets the answer the release's start would:

```go
config, err := roles.File.Parse()
if err != nil {
    return err
}
warnings, err := access.ValidateRoles(router.Collection(), config)
if err != nil {
    return err // the refusals that would fail New
}
for _, w := range warnings {
    fmt.Println(w)
}
```

### Several Sites, One Policy Store

A deployment whose sites each generate their own collection over one schema shares one
policy store: a login is one identity and a role is one set of powers across the
application. `UnionCollection` presents the sites' collections as one registry for
`WithDefaultRoles`: the registries merge, the subject namespace is the union of the sites'
declarations, and every other question about a resource is answered by the first
collection registering it. A resource several sites serve must be declared identically
in each; `UnionCollection` refuses collections that disagree on a shared resource's
permissions (fields included), scope, immutability, computed marking, or method target.

```go
collection, err := access.UnionCollection(consolerouter.Collection(), portalrouter.Collection())
if err != nil {
    return err
}

client, err := access.New(store, access.WithDefaultRoles(collection, roles.File))
```

### JSON Configuration

```json
{
  "roles": {
    "global": [],
    "domain": [
      {
        "name": "Editor",
        "description": "Writes and edits the documents of the tenant.",
        "permissions": {
          "List":   [{"resource": "Documents", "fields": ["title", "author"]}],
          "Read":   [{"resource": "Documents", "fields": ["title", "author", "body"]}],
          "Create": [{"resource": "Documents", "fields": ["title", "body"]}],
          "Update": [{"resource": "Documents", "fields": ["title", "body"], "condition": "author = subject"}]
        }
      },
      {
        "name": "Viewer",
        "description": "Reads the documents of the tenant.",
        "permissions": {
          "List": [{"resource": "Documents", "fields": ["title", "author"]}],
          "Read": [{"resource": "Documents", "fields": ["title", "author", "body"]}]
        }
      }
    ]
  }
}
```

Each role is declared under exactly one scope key, with a `name` in the grammar (letters
and digits starting with a capital letter; the scope is never part of the name) and a
`description` saying what it is for. A `global` role carries grants on global-scoped
resources and is held in the global partition; a `domain` role carries grants on
domain-scoped resources and is held in every tenant domain. A job function that needs
both kinds of powers is two roles assigned to one user. Each grant names a base
`resource`, the `fields` it covers as their wire tags, and an optional `condition`; a role
may carry several grants on one resource for one permission, each under its own condition.

The file is decoded strictly: a key the shape does not declare is an error, since a
misspelled key would otherwise silently drop the roles under it. `RoleFile.Parse` decodes
it and checks its shape the way `New` does — the name grammar, the descriptions, each
grant's grammar and condition syntax — with no permission collection, for a test or a tool
that wants the `RoleConfig` or the refusals a release's start would give before it looks
at the collection. `ValidateRoles` then checks what the file grants against the
collection.

## License

See LICENSE file.

---

Created and maintained by the CCC team.
